package compose

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-1972079936 Phase 1 inc3 — durable SNAPSHOT CADENCE + snapshot-aware
// restore. This file is the payoff of FEAT-1972079943 (Composition.Save/Load
// round-trip the full StateDigest byte-exact) and FEAT-1972079944
// (Composition.LoadAt seeds the clock so a snapshot restore is genuinely
// tick-continuous, not a frozen-at-tick-0 state blob): together they make it
// safe to restore from "latest snapshot + replay the journal tail" instead
// of always replaying the entire journal from genesis.
//
// # Design (Aaron's 2026-08-31 19:01 ruling on the BOW item)
//
//  1. CADENCE: every SnapshotCadenceTicks (N=360 == 1 simulated year, since
//     core.DailyTicksPerMonth==30 and a year is 12 months) a snapshot is
//     durably written via persist.Store.PutSnapshot. N is a placeholder per
//     the balance-number regime (Aaron retunes later) — it is a single
//     named constant, never a magic literal (mirrors
//     internal/engine/checkpoint's MaxRetainedForks convention).
//  2. JOURNAL RETENTION: the FULL journal is always kept. A snapshot is a
//     restore-SPEED optimization, not a replacement for the journal — the
//     genesis-replay path (RestoreCommands, already landed inc2) keeps
//     working forever, which is what GR#27's before/after comparison and
//     the Phase-3 convergence A/B parity gate both depend on.
//  3. RESTORE: GetSnapshot(latest) -> Composition.LoadAt(root, snapshotTick)
//     -> replay ONLY the journal commands recorded AFTER snapshotTick. Falls
//     back to a full genesis replay when no snapshot exists yet.
//
// # Snapshot payload shape
//
// A snapshot payload is exactly what Composition.Save already writes (a
// directory of save-bundle files), zipped into one opaque []byte so it fits
// persist.Store's []byte-shaped PutSnapshot/GetSnapshot. This deliberately
// reuses Save/Load's own on-disk bundle format as the single source of
// truth for "what a snapshot contains" (GR#3) rather than inventing a
// second serialization — the snapshot's recorded tick is read back from the
// bundle's own save.SaveSummary.CreatedAtTick (Save's save.Context already
// stamps this at save time, see save_wire.go's Save method), so no
// additional envelope/header is needed.

// SnapshotCadenceTicks is the number of engine ticks between durable
// snapshots (PLACEHOLDER, Aaron's 2026-08-31 ruling: N=360, one simulated
// year). A single named constant, never re-derived ad hoc at each call
// site, so retuning it later (balance-number regime) is a one-line change.
const SnapshotCadenceTicks int64 = 12 * core.DailyTicksPerMonth

// MaxRetainedSnapshots bounds how many durable snapshots MaybeSnapshot
// keeps for one city — mirrors internal/engine/checkpoint's
// MaxRetainedForks bounded-retention pattern (the Phase 1 inc3 design
// ruling explicitly calls for reusing it). Pruning NEVER touches the
// journal — only snapshots (a restore-speed optimization) are bounded;
// the full journal is always kept (this increment's ruling, "journal
// retention: KEEP THE FULL JOURNAL for Phase 1"). A single named
// constant, not a magic literal, so retuning it later is a one-line
// change.
const MaxRetainedSnapshots = 5

// ShouldSnapshot reports whether tick is a snapshot-cadence boundary: a
// strictly positive multiple of SnapshotCadenceTicks. Tick 0 (a freshly
// wired, never-ticked engine) is deliberately excluded — there is nothing
// meaningful to snapshot before the first tick, and a tick-0 snapshot would
// be indistinguishable from "no snapshot yet" for splitJournalAtTick's
// snapshotTick<=0 fast path below.
//
// A thin wrapper over ShouldSnapshotEvery at the package default cadence
// (SnapshotCadenceTicks) — kept as its own exported name because every
// existing caller/test spells it this way; ShouldSnapshotEvery is the
// cadence-parameterized primitive underneath (FEAT-1972079936 Phase 1
// inc3b, metroserve's `--snapshot-every` flag).
func ShouldSnapshot(tick int64) bool {
	return ShouldSnapshotEvery(tick, SnapshotCadenceTicks)
}

// ShouldSnapshotEvery is ShouldSnapshot generalized to an explicit cadence
// (inc3b): a strictly positive multiple of cadence, tick 0 always excluded
// (see ShouldSnapshot's doc comment for why). cadence<=0 means "snapshotting
// is off" (the metroserve `--snapshot-every 0` case) and always reports
// false — never a divide-by-zero panic.
func ShouldSnapshotEvery(tick, cadence int64) bool {
	if cadence <= 0 {
		return false
	}
	return tick > 0 && tick%cadence == 0
}

// MaybeSnapshot writes a durable snapshot for city via store IF the
// composition's current engine tick is a cadence boundary (ShouldSnapshot);
// otherwise it is a fast, allocation-free no-op (ok=false, no error). This
// is the entry point a driving loop (a headless harness, or the metroserve
// tick loop once inc4 wires it) calls after every AdvanceTicks: cheap to
// call unconditionally every tick, since the common case is just one
// int64 modulo.
//
// A thin wrapper over MaybeSnapshotEvery at the package default cadence —
// see MaybeSnapshotEvery's doc comment (inc3b) for the cadence-configurable
// primitive metroserve's `--snapshot-every` flag calls instead.
func (c *Composition) MaybeSnapshot(ctx context.Context, store persist.Store, city persist.CityKey) (id persist.SnapshotID, ok bool, err error) {
	return c.MaybeSnapshotEvery(ctx, store, city, SnapshotCadenceTicks)
}

// MaybeSnapshotEvery is MaybeSnapshot generalized to an explicit cadence in
// ticks (FEAT-1972079936 Phase 1 inc3b): writes a durable snapshot IFF the
// composition's current engine tick is a cadence boundary
// (ShouldSnapshotEvery(tick, cadence)); otherwise a fast no-op (ok=false,
// no error). cadence<=0 disables snapshotting entirely (metroserve's
// `--snapshot-every 0` off switch) — always a no-op, never an error, since
// "snapshotting turned off" is a valid, intentional operating mode, not a
// fault.
func (c *Composition) MaybeSnapshotEvery(ctx context.Context, store persist.Store, city persist.CityKey, cadence int64) (id persist.SnapshotID, ok bool, err error) {
	clock, err := c.state.e.Clock()
	if err != nil {
		return "", false, errs.Wrap(ErrModuleFailed, c.state.cid, err, map[string]any{"module": "snapshot", "step": "clock"})
	}
	tick := clock.Tick()
	if !ShouldSnapshotEvery(tick, cadence) {
		return "", false, nil
	}
	// BUG-480 deliverable (b) — JOURNAL-DIRTY GATE: once the composition's
	// durable journaler has recorded ANY failed AppendJournal (BUG-472's
	// swallow class -- persistCommandJournaler.dirty, persistjournal.go), a
	// snapshot taken from here on can never be proven tail-consistent with
	// the journal (its recorded tick could again run ahead of what the
	// journal's AdvanceTicks frames sum to, exactly the class
	// RestoreLatestSnapshotOrGenesis's walk-back exists to route around --
	// refusing the write here means there is one fewer inconsistent
	// snapshot for a future restore to have to skip). This is a REFUSAL,
	// not a fault: ok=false, err=nil, mirroring the cadence<=0 "off" no-op
	// above -- a dirty journaler is a documented, permanent operating mode
	// for this process, not a new failure each boundary. The registry
	// error is logged the FIRST time only (dirtyJournaler.MarkDirtyLoggedOnce),
	// never once per cadence boundary, so an ongoing dirty condition never
	// floods the log.
	if dj, isDirty := c.Journaler().(dirtyJournaler); isDirty && dj.Dirty() {
		if dj.MarkDirtyLoggedOnce() {
			_ = errs.New(ErrSnapshotRefusedDirty, c.state.cid, map[string]any{
				"city": city.TenantID + "/" + city.CityID,
				"tick": tick,
			})
		}
		return "", false, nil
	}
	data, err := c.buildSnapshotBytes()
	if err != nil {
		return "", false, err
	}
	id, err = store.PutSnapshot(ctx, city, data)
	if err != nil {
		// store.PutSnapshot's own error is already a durable-Store-level
		// failure (mirrors persistCommandJournaler.ObserveCommand, which
		// likewise returns a raw AppendJournal error unwrapped) — surfaced
		// as-is rather than re-wrapped under a compose-specific code.
		return "", false, err
	}
	// Prune down to MaxRetainedSnapshots — best-effort bounded retention,
	// same shape as checkpoint.Manager's abandoned-branch pruning. The new
	// snapshot just written is always kept (it is the newest); pruning
	// failures are surfaced (never swallowed — an unbounded, ever-growing
	// snapshot set is exactly the storage-cost problem this bound exists
	// to prevent), but the snapshot itself was already durably written and
	// PutSnapshot's success is not undone by a later prune failure.
	if err := pruneSnapshots(ctx, store, city); err != nil {
		return id, true, err
	}
	return id, true, nil
}

// dirtyJournaler is the narrow capability persistCommandJournaler exposes
// (persistjournal.go) that MaybeSnapshotEvery's dirty gate needs: whether a
// durable append has ever failed, and a one-shot latch so the refusal is
// logged exactly once. Composition.Journaler() returns the plain
// core.CommandJournaler interface (a single ObserveCommand method), so this
// local interface plus a type assertion is how snapshot.go reaches the
// concrete adapter's extra state without compose.go's journaler field ever
// needing a wider public type -- every non-persisted journaler (the default
// replay.Recorder, or a test spy) simply fails the assertion and the dirty
// gate is a no-op, exactly matching pre-BUG-480 behaviour when persistence
// is off.
type dirtyJournaler interface {
	Dirty() bool
	MarkDirtyLoggedOnce() bool
}

// pruneSnapshots deletes the oldest snapshots for city beyond
// MaxRetainedSnapshots (ListSnapshots' documented oldest-first order makes
// "the excess prefix" exactly the snapshots to remove). A no-op when the
// count is already within bound.
func pruneSnapshots(ctx context.Context, store persist.Store, city persist.CityKey) error {
	ids, err := store.ListSnapshots(ctx, city)
	if err != nil {
		return err
	}
	if len(ids) <= MaxRetainedSnapshots {
		return nil
	}
	excess := ids[:len(ids)-MaxRetainedSnapshots]
	for _, id := range excess {
		if err := store.DeleteSnapshot(ctx, city, id); err != nil {
			return err
		}
	}
	return nil
}

// buildSnapshotBytes serializes the composition's CURRENT state (via
// Composition.Save, unchanged) into a temp directory, then packs that
// directory into a single opaque []byte payload. The temp directory is
// always removed before returning, success or failure.
func (c *Composition) buildSnapshotBytes() ([]byte, error) {
	dir, err := os.MkdirTemp("", "metropolis-snapshot-*")
	if err != nil {
		return nil, errs.Wrap(ErrSnapshotPackFailed, c.state.cid, err, map[string]any{"step": "mkdtemp"})
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := c.Save(dir); err != nil {
		return nil, errs.Wrap(ErrSnapshotPackFailed, c.state.cid, err, map[string]any{"step": "save"})
	}
	data, err := zipDir(dir)
	if err != nil {
		return nil, errs.Wrap(ErrSnapshotPackFailed, c.state.cid, err, map[string]any{"step": "zip"})
	}
	return data, nil
}

// restoreFromSnapshotBytes unpacks a snapshot payload previously produced
// by buildSnapshotBytes into a temp directory, locates the composition
// save bundle inside it, and restores c via LoadAt at the tick that bundle
// was saved at (save.SaveSummary.CreatedAtTick — the SAME field
// Composition.Save stamps via save.Context.CreatedAtTick, see
// save_wire.go). Returns the restored tick.
func (c *Composition) restoreFromSnapshotBytes(data []byte) (int64, error) {
	dir, err := os.MkdirTemp("", "metropolis-restore-*")
	if err != nil {
		return 0, errs.Wrap(ErrSnapshotUnpackFailed, c.state.cid, err, map[string]any{"step": "mkdtemp"})
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := unzipDir(data, dir); err != nil {
		return 0, errs.Wrap(ErrSnapshotUnpackFailed, c.state.cid, err, map[string]any{"step": "unzip"})
	}
	summaries, _, err := save.List(dir)
	if err != nil {
		return 0, errs.Wrap(ErrSnapshotUnpackFailed, c.state.cid, err, map[string]any{"step": "list"})
	}
	tick := int64(-1)
	for _, s := range summaries {
		if s.DisplayName == compositionSaveName {
			tick = s.CreatedAtTick
			break
		}
	}
	if tick < 0 {
		return 0, errs.New(ErrSnapshotUnpackFailed, c.state.cid, map[string]any{
			"step": "locate",
		})
	}
	if err := c.LoadAt(dir, tick); err != nil {
		return 0, err // LoadAt already wraps with its own compose error context.
	}
	return tick, nil
}

// RestoreLatestSnapshotOrGenesis restores composition c — which MUST be a
// freshly Wired, never-yet-ticked Composition over e (the same precondition
// LoadAt itself carries, since a snapshot restore ends in a LoadAt call) —
// from store's durable records for city:
//
//   - If store holds at least one snapshot, restore WALKS BACK from the
//     newest (BUG-480): GetSnapshot it, LoadAt it (state + clock,
//     tick-continuous), then replay only the journal commands recorded
//     strictly AFTER that snapshot's tick (splitJournalAtTick below). If
//     that candidate's tail cannot be reconciled with the journal (a
//     TAIL-INCONSISTENCY — ErrSnapshotTailShort or
//     ErrSnapshotTailReplayRejected, the class BUG-472's swallowed-append
//     policy can produce when the swallow lands at or before a cadence
//     boundary), the candidate is skipped (logged via ErrSnapshotSkipped)
//     and the NEXT-older snapshot is tried, and so on. A CORRUPT snapshot
//     payload (ErrSnapshotUnpackFailed — a decode/unzip failure, i.e. real
//     data corruption rather than a tail mismatch) or a Store-level read
//     failure is NEVER walked past — BUG-480 explicitly forbids widening
//     the fallback to hide corruption, so either fails the whole restore
//     closed immediately, exactly as before this increment.
//   - If store holds no snapshot yet, or every snapshot's tail proved
//     inconsistent, restore falls back to the pre-existing genesis-replay
//     path: every durably journaled command replayed from tick 0
//     (RestoreCommands, inc2).
//
// Returns whether a snapshot was used (false == genesis fallback) and the
// final restored tick (read back from the engine's own clock after replay,
// since a tail can itself contain further AdvanceTicks commands that move
// the tick past the snapshot's own recorded value).
func RestoreLatestSnapshotOrGenesis(ctx context.Context, e *core.Engine, c *Composition, store persist.Store, city persist.CityKey) (usedSnapshot bool, tick int64, err error) {
	ids, err := store.ListSnapshots(ctx, city)
	if err != nil {
		return false, 0, err
	}
	cmds, err := RestoreCommands(ctx, store, city)
	if err != nil {
		return false, 0, err
	}

	if len(ids) == 0 {
		return restoreGenesis(e, c, cmds)
	}

	// Walk back newest -> oldest (ListSnapshots is documented oldest-first,
	// so this iterates the slice in reverse). Each candidate is validated
	// on a THROWAWAY engine/composition first (tryRestoreCandidate) — never
	// on the caller's real e/c — because core.Engine seals permanently the
	// first time an AdvanceTicks command is accepted (see core.Engine's
	// sealed field), so a partially-replayed tail on the real engine could
	// never be retried against an older snapshot. Only once a candidate is
	// PROVEN clean is it (deterministically, GR#21) replayed for real onto
	// e/c — exactly once, so the real engine is never touched by a
	// candidate that turns out to be skipped.
	for i := len(ids) - 1; i >= 0; i-- {
		id := ids[i]
		data, getErr := store.GetSnapshot(ctx, city, id)
		if getErr != nil {
			// A Store-level read failure is not a tail-inconsistency signal
			// — never walked past (see doc comment above).
			return false, 0, getErr
		}
		_, tail, ok, valErr := tryRestoreCandidate(data, cmds, city, c.state.cid, e.WorldSeed())
		if valErr != nil {
			// Corrupt/undecodable snapshot payload — fail closed, no
			// further walk-back (BUG-480: never widen the fallback to hide
			// real corruption).
			return false, 0, valErr
		}
		if !ok {
			// Tail-inconsistency — already logged inside
			// tryRestoreCandidate via ErrSnapshotSkipped. Try the
			// next-older snapshot.
			continue
		}
		// Candidate validated clean: apply it for real, exactly once, onto
		// the caller's own e/c (never touched until this point).
		if _, err := c.restoreFromSnapshotBytes(data); err != nil {
			return false, 0, err
		}
		if err := replayCommands(e, tail); err != nil {
			return false, 0, err
		}
		finalTick, err := engineTick(e, c.state.cid)
		if err != nil {
			return false, 0, err
		}
		return true, finalTick, nil
	}

	// Every snapshot's tail proved inconsistent with the journal: fall back
	// to a full genesis replay, which never depends on a snapshot at all.
	return restoreGenesis(e, c, cmds)
}

// restoreGenesis replays cmds (the full durable journal) onto e/c from tick
// 0 — the pre-inc3 always-correct fallback, reached both when no snapshot
// has ever been taken and when every existing snapshot's tail proved
// inconsistent with the journal (BUG-480's walk-back exhaustion case).
func restoreGenesis(e *core.Engine, c *Composition, cmds []protocol.Command) (usedSnapshot bool, tick int64, err error) {
	if err := replayCommands(e, cmds); err != nil {
		return false, 0, err
	}
	finalTick, err := engineTick(e, c.state.cid)
	if err != nil {
		return false, 0, err
	}
	return false, finalTick, nil
}

// tryRestoreCandidate validates whether a snapshot payload's tail
// reconciles with cmds WITHOUT ever touching the caller's real
// engine/composition: it restores into a brand-new throwaway engine wired
// with Wire(e, nil) — persistence is irrelevant to validation, since
// Load/LoadAt fully overwrite whatever the throwaway engine's default
// construction left in place. The throwaway MUST still carry the caller's
// real world seed (worldSeed, below): BUG-479's Load-time seed check
// compares the snapshot's bundle seed against the loading engine's seed,
// so a probe built with a mismatched (e.g. default-zero) seed would refuse
// every real candidate with MET-E819 before the tail-consistency logic
// this function exists to test ever runs. This is what lets BUG-480's
// walk-back retry cheaply: a candidate that fails here never seals or
// partially mutates the REAL engine RestoreLatestSnapshotOrGenesis was
// handed.
//
// Returns ok=true with the resolved snapshot tick + tail commands when the
// candidate reconciles — the caller then replays this SAME data
// deterministically (GR#21) onto the real e/c. ok=false with err=nil means
// a TAIL-inconsistency (ErrSnapshotTailShort from splitJournalAtTick, or
// ErrSnapshotTailReplayRejected from replayCommands) — logged here via
// ErrSnapshotSkipped and safe to walk back past. A non-nil err is a
// corrupt-frame failure (ErrSnapshotUnpackFailed) — BUG-480 requires this
// to fail the whole restore closed immediately, never walked past.
func tryRestoreCandidate(data []byte, cmds []protocol.Command, city persist.CityKey, correlationID string, worldSeed uint64) (snapTick int64, tail []protocol.Command, ok bool, err error) {
	valE := core.NewEngine(core.WithWorldSeed(worldSeed))
	valC, wireErr := Wire(valE, nil)
	if wireErr != nil {
		return 0, nil, false, errs.Wrap(ErrModuleFailed, correlationID, wireErr, map[string]any{"module": "snapshot", "step": "validate-wire"})
	}
	snapTick, unpackErr := valC.restoreFromSnapshotBytes(data)
	if unpackErr != nil {
		return 0, nil, false, unpackErr // corrupt snapshot payload — fail closed, no walk-back.
	}
	t, splitErr := splitJournalAtTick(cmds, snapTick, city, correlationID)
	if splitErr != nil {
		// isTailInconsistency is a defensive double-check, not the primary
		// dispatch: splitJournalAtTick only ever returns
		// ErrSnapshotTailShort today, but skip-vs-fail-closed is exactly
		// the distinction BUG-480 requires never drift silently, so any
		// error this function does NOT recognise as a tail-inconsistency
		// fails the whole restore closed rather than being walked past.
		if !isTailInconsistency(splitErr) {
			return 0, nil, false, splitErr
		}
		logSnapshotSkip(city, snapTick, splitErr, correlationID)
		return 0, nil, false, nil
	}
	if replayErr := replayCommands(valE, t); replayErr != nil {
		if !isTailInconsistency(replayErr) {
			return 0, nil, false, replayErr
		}
		logSnapshotSkip(city, snapTick, replayErr, correlationID)
		return 0, nil, false, nil
	}
	return snapTick, t, true, nil
}

// logSnapshotSkip records ErrSnapshotSkipped (GR#7, registry-sourced, GR#1
// auto-logged by errs.New) for one walked-past snapshot candidate — loud
// and specific about which tick was skipped and why, per BUG-480's
// deliverable (a).
func logSnapshotSkip(city persist.CityKey, snapTick int64, cause error, correlationID string) {
	_ = errs.New(ErrSnapshotSkipped, correlationID, map[string]any{
		"city":  city.TenantID + "/" + city.CityID,
		"tick":  snapTick,
		"cause": cause.Error(),
	})
}

// isTailInconsistency reports whether err is one of the two
// tail-inconsistency codes (ErrSnapshotTailShort / ErrSnapshotTailReplayRejected)
// BUG-480's walk-back is allowed to skip past, as opposed to a genuine
// corrupt-frame failure (ErrSnapshotUnpackFailed) which must fail closed.
// Retained as a documented, testable predicate even though
// tryRestoreCandidate's own call sites already know statically which code
// each failure carries — a future caller reasoning about an *errs.E value
// from elsewhere can use this instead of re-deriving the code list.
func isTailInconsistency(err error) bool {
	var e *errs.E
	if !errors.As(err, &e) {
		return false
	}
	return e.Code == ErrSnapshotTailShort || e.Code == ErrSnapshotTailReplayRejected
}

// engineTick is a tiny helper so RestoreLatestSnapshotOrGenesis's two
// success paths share one clock-read-and-wrap call site.
func engineTick(e *core.Engine, correlationID string) (int64, error) {
	clock, err := e.Clock()
	if err != nil {
		return 0, errs.Wrap(ErrModuleFailed, correlationID, err, map[string]any{"module": "snapshot", "step": "final-clock"})
	}
	return clock.Tick(), nil
}

// replayCommands applies cmds to e in order via e.HandleCommand — the SAME
// command path a live client took — surfacing the first rejection as a
// fatal restore error rather than silently skipping it (a silently-dropped
// journal command is restore-side data loss, exactly the class GR#27 and
// this whole epic exist to kill).
func replayCommands(e *core.Engine, cmds []protocol.Command) error {
	for _, cmd := range cmds {
		res := e.HandleCommand(cmd)
		if !res.Accepted {
			return errs.New(ErrSnapshotTailReplayRejected, string(cmd.CorrelationID), map[string]any{
				"kind":  string(cmd.Kind),
				"error": fmt.Sprintf("%v", res.Error),
			})
		}
	}
	return nil
}

// splitJournalAtTick returns the suffix of cmds (in the same order) that
// were journaled AFTER the engine reached snapshotTick — i.e. exactly the
// commands a snapshot-aware restore still needs to replay on top of a
// LoadAt(snapshotTick) restore, because everything before that point is
// already captured in the snapshot's own state.
//
// snapshotTick<=0 is a degenerate case (should not occur — ShouldSnapshot
// never fires at tick<=0 — but is handled honestly rather than assumed
// away): the entire journal is returned as the tail.
//
// The tick counter only ever advances via protocol.KindAdvanceTicks
// commands (core.Engine.AdvanceTicks is the only mutator of the clock, and
// it is reached from the command path via handleAdvanceTicks — see
// core/commands.go), so this walks the journal tracking a running tick
// total and returns everything after the point that total reaches
// snapshotTick. A KindAdvanceTicks command whose N would carry the running
// total PAST snapshotTick (rather than landing exactly on it — possible
// when a caller advances many ticks in one command, e.g. driving a whole
// month at once while the cadence boundary falls mid-batch) is split: the
// portion that reaches snapshotTick is already captured by the snapshot and
// is dropped, and a synthetic AdvanceTicks command carrying only the
// REMAINDER is replayed in its place, so the tail still advances the clock
// by exactly the same total amount the original journal would have.
func splitJournalAtTick(cmds []protocol.Command, snapshotTick int64, city persist.CityKey, correlationID string) ([]protocol.Command, error) {
	if snapshotTick <= 0 {
		return cmds, nil
	}
	cityLabel := city.TenantID + "/" + city.CityID
	running := int64(0)
	for i, cmd := range cmds {
		if cmd.Kind != protocol.KindAdvanceTicks {
			continue
		}
		payload, ok := cmd.Payload.(protocol.AdvanceTicksPayload)
		if !ok {
			return nil, errs.New(ErrSnapshotTailShort, correlationID, map[string]any{
				"city": cityLabel,
				"tick": snapshotTick,
			})
		}
		switch {
		case running+payload.N < snapshotTick:
			running += payload.N
		case running+payload.N == snapshotTick:
			tail := make([]protocol.Command, len(cmds[i+1:]))
			copy(tail, cmds[i+1:])
			return tail, nil
		default: // running+payload.N > snapshotTick: this command straddles the boundary.
			remainder := running + payload.N - snapshotTick
			remCmd := cmd
			remCmd.Payload = protocol.AdvanceTicksPayload{N: remainder}
			tail := make([]protocol.Command, 0, len(cmds)-i)
			tail = append(tail, remCmd)
			tail = append(tail, cmds[i+1:]...)
			return tail, nil
		}
	}
	// Walked the whole journal without the running tick total ever reaching
	// snapshotTick: the journal and the snapshot are out of sync (a corrupt
	// Store, or a snapshot written against a different city's journal).
	return nil, errs.New(ErrSnapshotTailShort, correlationID, map[string]any{
		"city": cityLabel,
		"tick": snapshotTick,
	})
}

// zipDir packs every regular file under dir into a single in-memory zip
// archive, using forward-slash-normalized paths relative to dir as entry
// names (so the archive is portable across OSes) in filepath.Walk's
// lexical order (deterministic — GR#21 — never map/directory-listing
// order).
func zipDir(dir string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	walkErr := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		w, err := zw.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		_, err = io.Copy(w, f)
		return err
	})
	if walkErr != nil {
		return nil, walkErr
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// unzipDir extracts a zip archive built by zipDir into dir (which must
// already exist). Every entry name is defended against zip-slip (a ".."
// segment or an absolute path escaping dir) — a hostile/corrupt snapshot
// payload can never write outside the target directory.
func unzipDir(data []byte, dir string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		clean := filepath.Clean(f.Name)
		if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.IsAbs(clean) {
			return fmt.Errorf("compose: unsafe snapshot zip entry %q", f.Name)
		}
		target := filepath.Join(dir, clean)
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := extractZipFile(f, target); err != nil {
			return err
		}
	}
	return nil
}

// extractZipFile copies one zip.File's content to target, closing both the
// zip reader and the destination file even on a copy error (isolated into
// its own function so unzipDir's loop body needs no defer-in-loop).
func extractZipFile(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	out, err := os.Create(target)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	_, err = io.Copy(out, rc)
	return err
}
