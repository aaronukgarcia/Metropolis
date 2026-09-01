package main

import (
	"context"
	"fmt"
	"io"

	"github.com/aaronukgarcia/Metropolis/internal/engine/compose"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// ErrHostedSnapshotFailed (MET-P035, module int.protocol — the mkey this
// binary's own doc comment in main.go claims, "a transport host for
// int.protocol's existing envelope/subscription machinery") is raised when
// comp.MaybeSnapshotEvery's durable PutSnapshot/prune fails for a hosted
// city's tick-cadence boundary. Registered via
// `node tools/plan/add-error.js add MET-P035 ...` (GR#7) under the
// P035-P039 block claimed for int.protocol on 2026-09-01. See
// startCommandLoop's doc comment below for the fail-loud-but-continue
// policy this code exists to carry.
const ErrHostedSnapshotFailed = "MET-P035"

// FEAT-1972079936 Phase 1 inc3b — wiring compose's snapshot cadence
// (snapshot.go, landed 9dd1872) into metroserve's live tick driver.
//
// # Why this is NOT "call MaybeSnapshot from tickLoop"
//
// tickLoop (main.go) only ever ENQUEUES an AdvanceTicksPayload{N:1} command
// via transport.SendCommand — it has no *core.Engine, no *compose.Composition,
// and (this is the load-bearing part) no way to know WHEN that command has
// actually been processed: e.HandleCommand runs on a SEPARATE goroutine,
// e.RunCommandLoop's own (started in buildCity/run()), which pulls commands
// off transport.Commands() one at a time. Calling MaybeSnapshot from
// tickLoop itself right after SendCommand would race the tick's own journal
// append (persistCommandJournaler.ObserveCommand runs INSIDE HandleCommand,
// on the command-loop goroutine, not synchronously with SendCommand's
// caller) — the snapshot could be taken a command early or a command late,
// which is exactly the "snapshot+tail replay is not exact" bug deliverable 3
// exists to prove absent.
//
// # Why this is NOT "read transport.Results()" either
//
// wsserver's per-connection pump goroutine (internal/protocol/wsserver/server.go)
// is already the SOLE consumer of transport.Results() — it forwards every
// CommandResult (tick-advance results included) to the connected WS client.
// A second reader competing for the same channel would silently steal every
// other result away from that forwarding loop (a channel has AT MOST one
// effective consumer per message), corrupting the wire protocol for real
// clients. This driver must never touch Results().
//
// # The actual seam: intercept SendResult, not SendCommand or Results()
//
// e.RunCommandLoop's body (internal/engine/core/commands.go) is exactly:
//
//	t.SendResult(e.HandleCommand(cmd))
//
// on its own single goroutine — the SAME goroutine, in the SAME call, that
// just finished HandleCommand (and therefore just finished journalAccepted,
// and therefore the tick's ObserveCommand durable append, if it was
// accepted). snapshotCommandSource below wraps the CommandSource
// (core.CommandSource: Commands()+SendResult(), satisfied by
// *protocol.InProcTransport unmodified) that RunCommandLoop is given, and
// intercepts SendResult calls for exactly the tick driver's OWN correlation
// ID (tickLoop stamps every AdvanceTicks command with one fixed
// correlationID per city — see main.go/cityhost.go). The wrapper forwards
// to the real transport first (so wsserver's pump goroutine sees the exact
// same result stream as before, byte-for-byte, when snapshotting is off or
// the tick is not a cadence boundary — MaybeSnapshotEvery's own fast no-op
// path costs one int64 modulo) and ONLY THEN, still on RunCommandLoop's own
// goroutine, calls MaybeSnapshotEvery — so the snapshot always observes the
// engine and its durable journal in EXACTLY the state they were left in by
// the tick that just completed, with no other command able to interleave
// (RunCommandLoop is the sole HandleCommand caller for this transport, and
// it cannot pull the NEXT command off Commands() until this SendResult call
// returns).
//
// This costs nothing when persistence is off (store == nil) or
// snapshotEvery <= 0 ("off"): startCommandLoop below does not even
// construct the wrapper in that case, so RunCommandLoop is driven by the
// bare *protocol.InProcTransport exactly as it was before this increment
// (AC: default/no-persist behaviour byte-for-byte unchanged).

// snapshotCommandSource wraps a core.CommandSource and, for every
// CommandResult whose CorrelationID matches tickCorrelationID, invokes
// onTickResult AFTER forwarding the result to the wrapped source — see this
// file's package doc comment above for why this is the exact
// tick-boundary/journal-consistent hook point.
type snapshotCommandSource struct {
	core.CommandSource
	tickCorrelationID string
	onTickResult      func(accepted bool, tick int64)
}

// SendResult forwards to the wrapped CommandSource first (so the wrapped
// transport's own SendResult return value — whether the result actually
// reached a subscriber — is exactly what the caller (RunCommandLoop) sees,
// unchanged), then fires onTickResult for a matching tick correlation ID.
func (s *snapshotCommandSource) SendResult(r protocol.CommandResult) bool {
	ok := s.CommandSource.SendResult(r)
	if s.onTickResult != nil && string(r.CorrelationID) == s.tickCorrelationID {
		s.onTickResult(r.Accepted, int64(r.Tick))
	}
	return ok
}

// startCommandLoop starts e.RunCommandLoop for city on its own goroutine,
// driven by transport directly UNLESS persistence AND a positive
// snapshotEvery cadence are both configured — in which case it is driven
// through a snapshotCommandSource that takes a durable snapshot
// (comp.MaybeSnapshotEvery) exactly when the tick correlation ID's own
// CommandResult reports a cadence-boundary tick was accepted.
//
// comp/store/city are nil-checked defensively (nil comp or nil store both
// disable the wrapper — snapshotting is meaningless with either absent) so
// every existing no-persist call site (comp non-nil, store nil) keeps
// running the bare transport, unchanged.
//
// A snapshot write failure is FAIL-LOUD but never stops the sim: it is
// surfaced via a registry-coded error (MET-P035, GR#1/GR#7) and printed to
// logw — deliberately DIFFERENT from BUG-472's later "HALT + SURFACE"
// policy for a failed COMMAND-JOURNAL append (engine.core's MET-E021/
// MET-E023, commands.go's journalAccepted): the command that triggered
// this cadence boundary was already accepted and durably JOURNALED before
// this snapshot attempt ever ran (startCommandLoop's own doc comment above
// explains why that ordering is guaranteed), so a snapshot write failure
// here has NOT lost anything durable — only the restore-SPEED optimization,
// a durable snapshot blob, is missing for this one cadence boundary; the
// next boundary retries, and journal-only genesis replay, or a tail-replay
// from an older snapshot, remain fully correct in the meantime. This is
// why MaybeSnapshotEvery's own failure stays fail-loud-continue even though
// the sibling command-journal-append failure now halts the whole
// composition -- the two failures have different consequences for what is,
// and is not, durably recoverable.
func startCommandLoop(ctx context.Context, e *core.Engine, transport *protocol.InProcTransport, comp *compose.Composition, store persist.Store, city persist.CityKey, snapshotEvery int64, tickCorrelationID string, logw io.Writer) <-chan error {
	var source core.CommandSource = transport
	if comp != nil && store != nil && snapshotEvery > 0 {
		source = &snapshotCommandSource{
			CommandSource:     transport,
			tickCorrelationID: tickCorrelationID,
			onTickResult: func(accepted bool, tick int64) {
				if !accepted {
					return
				}
				// context.Background(), not ctx: ctx is the city's own
				// lifetime context, which is already cancelled by the time
				// a shutdown-adjacent snapshot write would run — using it
				// here would turn "we are shutting down" into a spurious
				// MET-P035 for a snapshot boundary that was otherwise
				// perfectly healthy. Mirrors persistCommandJournaler's own
				// fixed context.Background() (persistjournal.go).
				if _, _, err := comp.MaybeSnapshotEvery(context.Background(), store, city, snapshotEvery); err != nil {
					logged := errs.Wrap(ErrHostedSnapshotFailed, tickCorrelationID, err, map[string]any{
						"city": city.CityID,
						"tick": tick,
					})
					_, _ = fmt.Fprintf(logw, "metroserve: %v\n", logged)
				}
			},
		}
	}
	loopDone := make(chan error, 1)
	go func() { loopDone <- e.RunCommandLoop(ctx, source) }()
	return loopDone
}
