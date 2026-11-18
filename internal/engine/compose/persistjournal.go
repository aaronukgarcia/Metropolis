package compose

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/rules"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-1972079936 Phase 1 inc2 — the durable write-through journaler
// adapter. It is the ONE place that knows both engine.core's
// CommandJournaler seam AND internal/persist's opaque-bytes Store (the
// GR#25 edge feat.compositionroot -> int.persist, committed ee61083). By
// keeping this knowledge here, engine.core never imports internal/persist
// and internal/persist stays a pure opaque-bytes leaf that never imports
// protocol/engine — the dependency-inversion shape Aaron's
// engine-owns-journal DD requires.
//
// # Fail-closed durability (the whole point of Phase 1)
//
// Phase 1's job is to kill the localStorage/data-loss class: an accepted
// command that the durable Store failed to persist MUST be visible, never
// swallowed. So ObserveCommand returns the AppendJournal error rather than
// logging-and-continuing — aligned with GR#27's fail-closed capture
// principle. engine.core's journalAccepted (commands.go) surfaces a
// journaler error through the registry (MET-E021) AND — per BUG-472's
// later "HALT + SURFACE" ruling (Aaron, 2026-09-01), which supersedes this
// package's original swallow-and-continue policy — rejects the failing
// command and permanently halts the Engine (MET-E023,
// ErrSimulationPersistHalted) rather than crashing the tick. A
// durable-persist failure degrades loudly at the seam this adapter sits
// on, exactly as intended; this adapter's own dirty/dirtyLogged latches
// (below) are orthogonal to that halt — they exist for snapshot.go's
// MaybeSnapshotEvery gate, which must never write a snapshot for a city
// whose journal is now permanently missing a frame, independent of
// whatever engine.core does with the command's own wire result.

// persistCommandJournaler wraps an inner core.CommandJournaler (never
// replaces it — the in-memory replay.Recorder still records every command
// for the in-process replay path) and additionally writes the SAME
// protocol.EncodeCommand bytes through to a durable persist.Store. Because
// both this adapter and the inner Recorder serialize via
// protocol.EncodeCommand, the durable journal is byte-identical to the
// in-memory one (AC-6).
//
// It implements EXACTLY core.CommandJournaler (a single ObserveCommand
// method — see internal/engine/core/commands.go). The interface declares no
// other Observe* methods, so there are none to delegate; were the interface
// ever widened, any non-command Observe* would delegate straight to inner
// (they are not part of the command journal).
//
// No mutex (GR#21): every field set at construction (inner/store/city/ctx)
// stays immutable for the adapter's life; concurrency safety for THOSE lives
// in the wrapped Store (persist.MemStore/DiskStore each guard their own
// state) and the wrapped Recorder (its own mutex). BUG-480 (deliverable b)
// added the ONE piece of adapter-owned mutable state, the dirty/dirtyLogged
// latches — both plain atomic.Bool, set-once-ever (dirty) or
// CompareAndSwap-raced (dirtyLogged), so no lock is needed for either and
// the astgate SEC-020 copyguard scan still finds no non-atomic mutable
// field shared without a mutex.
type persistCommandJournaler struct {
	inner core.CommandJournaler
	store persist.Store
	city  persist.CityKey
	// ctx is the context threaded into every Store call. Phase 1 needs no
	// per-call ctx plumbing (no cancellation/timeout story until the
	// transport-side rehydrate of inc3/Phase 2), so it is fixed at
	// construction to context.Background(); a later increment can widen the
	// constructor to accept a caller ctx without changing this seam's shape.
	ctx context.Context

	// version reports the live rule version a frame is accepted under
	// (FEAT-9995385612). nil means version 0: the frame is the plain
	// protocol.EncodeCommand bytes, exactly as before rule versions existed.
	// Wire sets it to the composition's live version; it is read once per
	// frame, on the serialised command path, so a frame is never stamped
	// with a version it was not accepted under.
	version func() rules.Version

	// dirty latches true the FIRST time store.AppendJournal fails. Under
	// BUG-472's later "HALT + SURFACE" ruling (2026-09-01, superseding
	// this field's original swallow-and-continue rationale), engine.core's
	// journalAccepted now REJECTS the failing command and permanently
	// halts the Engine rather than keeping it accepted — but the command's
	// own state effect had already run before that decision (journalAccepted's
	// doc comment, commands.go, explains why that ordering is unavoidable),
	// so the journal frame is still permanently missing for whatever DID
	// apply, exactly as before. dirty is this adapter's own independent
	// record of that fact, for snapshot.go's gate below — it does not need
	// to agree with, or even know about, engine.core's own persistHalt
	// latch (two different consumers of the same underlying fault). It
	// NEVER clears for the process lifetime (BUG-480's
	// deliverable (b) design ruling): a snapshot taken after a lost command
	// can never be proven tail-consistent with the journal again — the
	// journal's own AdvanceTicks total is now permanently short of what the
	// live engine's tick actually is, by exactly the ticks (if any) the
	// dropped command would have advanced, and there is no way for this
	// adapter to know in general whether a LATER successful append somehow
	// makes up the shortfall (it cannot, since the shortfall is a specific
	// missing frame, not a running balance). The only genuinely safe
	// "re-sync" is a fresh, from-genesis journal for this city, which is an
	// operator action (or a process restart against a store that has since
	// been repaired), not something this adapter can detect or perform
	// itself — so simplest-and-honest is never-clears-in-process rather than
	// inventing a heuristic recovery condition. snapshot.go's
	// MaybeSnapshotEvery consults Dirty() via this exact reasoning to refuse
	// writing a new (inevitably still-inconsistent) snapshot while dirty.
	dirty atomic.Bool

	// dirtyLogged guards BUG-480's "log once, not every tick" requirement:
	// the first caller to observe dirty via MarkDirtyLoggedOnce logs
	// ErrSnapshotRefusedDirty; every later cadence boundary while still
	// dirty is a silent, cheap no-op instead of a per-tick log flood.
	dirtyLogged atomic.Bool
}

// Dirty reports whether store.AppendJournal has EVER failed for this
// journaler (see the dirty field's doc comment for why this never clears
// for the process lifetime).
func (p *persistCommandJournaler) Dirty() bool { return p.dirty.Load() }

// Inner returns the wrapped core.CommandJournaler this adapter durably
// mirrors — the SAME instance Wire resolved before deciding whether to wrap
// it (compose.go: journaler := deps.CommandJournaler, defaulting to
// replay.NewRecorder(), THEN wrapped here when Deps.PersistStore != nil). It
// exists solely so a caller holding only a core.CommandJournaler (e.g.
// Composition.JournalStatus, compose_journal.go) can chase through exactly
// one level of wrapping to reach the concrete *replay.Recorder underneath —
// GR#17's "never a hardcoded second source of truth": the entry count still
// comes from the one real Recorder.Len(), this is just the accessor that
// lets JournalStatus find it on the production (Deps.PersistStore-backed)
// path, where BUG-740 previously left it unreachable and EntriesKnown fell
// back to false exactly where durability matters.
func (p *persistCommandJournaler) Inner() core.CommandJournaler { return p.inner }

// MarkDirtyLoggedOnce reports true exactly once — for whichever caller
// first observes dirty==true — and false for every call after that,
// including calls that race the first one (CompareAndSwap makes exactly one
// winner). Callers use this to log a refusal exactly once rather than at
// every subsequent cadence boundary.
func (p *persistCommandJournaler) MarkDirtyLoggedOnce() bool {
	return p.dirtyLogged.CompareAndSwap(false, true)
}

// compile-time proof the adapter satisfies the seam exactly.
var _ core.CommandJournaler = (*persistCommandJournaler)(nil)

// newPersistCommandJournaler wraps inner so that every command inner
// accepts is ALSO durably appended to store under city. inner is preserved
// (never replaced) so the in-memory replay path keeps working; the durable
// write is a pure side channel layered on top.
func newPersistCommandJournaler(inner core.CommandJournaler, store persist.Store, city persist.CityKey) *persistCommandJournaler {
	return &persistCommandJournaler{
		inner: inner,
		store: store,
		city:  city,
		ctx:   context.Background(),
	}
}

// ObserveCommand records cmd into the inner journaler first (preserving
// in-memory replay), then durably persists the SAME protocol.EncodeCommand
// bytes through the Store.
//
// Order and fail-closed policy (AC-1):
//  1. inner.ObserveCommand(cmd) first. If it errors, return that error and
//     do NOT persist — a command the inner journaler rejected must not reach
//     durable storage (keeps the two journals in lock-step).
//  2. protocol.EncodeCommand(cmd) — the SAME codec the in-memory Recorder
//     uses (record.go:101), so the durable bytes are byte-identical to the
//     in-memory journal. An encode failure surfaces (never a bespoke
//     serialization).
//  3. store.AppendJournal(ctx, city, data). If it errors, return that error
//     — a failed durable persist is visible, never swallowed.
func (p *persistCommandJournaler) ObserveCommand(cmd protocol.Command) error {
	if err := p.inner.ObserveCommand(cmd); err != nil {
		return err
	}
	var v rules.Version
	if p.version != nil {
		v = p.version()
	}
	data, err := encodeJournalFrame(cmd, v)
	if err != nil {
		return err
	}
	if err := p.store.AppendJournal(p.ctx, p.city, data); err != nil {
		// BUG-480 deliverable (b): latch dirty BEFORE returning, so any
		// caller observing this failure (including engine.core's own
		// journalAccepted, which now rejects the command and halts the
		// Engine on this exact error per BUG-472's "HALT + SURFACE" ruling
		// — untouched here) has already made the dirty state visible to
		// snapshot.go's MaybeSnapshotEvery by the time this call returns.
		p.dirty.Store(true)
		return err
	}
	return nil
}

// JournalFrame is one durable journal frame decoded: the command and the
// rule version it was accepted under (FEAT-9995385612). It embeds the
// command so read-only uses (frame.Kind, frame.Payload) stay natural, but
// the version is a field of the same value, so a caller cannot take the
// command without having had the version in hand. Version 0 is a frame
// with no stamp (every frame written before rule versions existed).
type JournalFrame struct {
	protocol.Command
	Version rules.Version
}

// journalEnvelope is the on-disk shape of a frame accepted under rule
// version N >= 1: {"rv":N,"cmd":<the plain command JSON>}. A frame accepted
// under version 0 is NOT enveloped; it is exactly today's bytes.
//
// The envelope exists so that (a) every frame names the rules it was judged
// by, with no marker entries, no segment table and no wire surface, and (b)
// a build that predates rule versions meets a frame with no "kind", fails
// with an unknown-kind decode error and refuses the city, instead of
// silently ignoring an unknown field and misreplaying (rollback protection).
// It is written and read only in this file.
type journalEnvelope struct {
	RV  json.RawMessage `json:"rv"`
	Cmd json.RawMessage `json:"cmd"`
}

// encodeJournalFrame returns the durable bytes for cmd accepted under v:
// the plain protocol.EncodeCommand bytes at version 0, the envelope at
// version >= 1. A negative version is refused (never written).
func encodeJournalFrame(cmd protocol.Command, v rules.Version) ([]byte, error) {
	if v < 0 {
		return nil, errs.New(rules.ErrMalformedRuleStamp, errs.NewCorrelationID(), map[string]any{
			"reason": "refusing to journal a frame under negative rule version " + strconv.Itoa(int(v)),
		})
	}
	plain, err := protocol.EncodeCommand(cmd)
	if err != nil {
		return nil, err
	}
	if v == 0 {
		return plain, nil
	}
	out := make([]byte, 0, len(plain)+len(`{"rv":,"cmd":}`)+8)
	out = append(out, `{"rv":`...)
	out = strconv.AppendInt(out, int64(v), 10)
	out = append(out, `,"cmd":`...)
	out = append(out, plain...)
	out = append(out, '}')
	return out, nil
}

// malformedStamp builds the MET-G5613 error for frame i of n.
func malformedStamp(i, n int, reason string) error {
	return errs.New(rules.ErrMalformedRuleStamp, errs.NewCorrelationID(), map[string]any{
		"reason": fmt.Sprintf("journal frame %d of %d: %s", i, n, reason),
	})
}

// decodeJournalFrame decodes one durable frame. A frame with neither an
// "rv" nor a "cmd" key is a plain command at version 0. Any frame that has
// either key is an envelope and must be exactly {"rv":N,"cmd":{...}} with N
// a positive base-10 integer: a non-integer, zero, negative or missing rv,
// a missing cmd, or any other key is MET-G5613, never read as version 0.
// A version above rules.Current is NOT refused here: classification is the
// restore's job (it needs the whole journal), this only parses.
func decodeJournalFrame(frame []byte, i, n int) (JournalFrame, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(frame, &probe); err != nil {
		return JournalFrame{}, fmt.Errorf("compose: restore command frame %d of %d: %w", i, n, err)
	}
	_, hasRV := probe["rv"]
	_, hasCmd := probe["cmd"]
	if !hasRV && !hasCmd {
		cmd, err := protocol.DecodeCommand(frame)
		if err != nil {
			return JournalFrame{}, fmt.Errorf("compose: restore command frame %d of %d: %w", i, n, err)
		}
		return JournalFrame{Command: cmd}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(frame))
	dec.DisallowUnknownFields()
	var env journalEnvelope
	if err := dec.Decode(&env); err != nil {
		return JournalFrame{}, malformedStamp(i, n, "envelope does not decode: "+err.Error())
	}
	if len(env.RV) == 0 {
		return JournalFrame{}, malformedStamp(i, n, "envelope has no rv")
	}
	if len(env.Cmd) == 0 || bytes.Equal(bytes.TrimSpace(env.Cmd), []byte("null")) {
		return JournalFrame{}, malformedStamp(i, n, "envelope has no cmd")
	}
	rv, err := strconv.ParseInt(string(bytes.TrimSpace(env.RV)), 10, 32)
	if err != nil {
		return JournalFrame{}, malformedStamp(i, n, "rv is not a base-10 integer: "+string(env.RV))
	}
	if rv < 1 {
		return JournalFrame{}, malformedStamp(i, n, "rv is "+strconv.FormatInt(rv, 10)+", must be 1 or more (version 0 is never stamped)")
	}
	cmd, err := protocol.DecodeCommand(env.Cmd)
	if err != nil {
		return JournalFrame{}, fmt.Errorf("compose: restore command frame %d of %d: %w", i, n, err)
	}
	return JournalFrame{Command: cmd, Version: rules.Version(rv)}, nil
}

// RestoreCommands reads every durably-persisted command frame for city and
// decodes it back into a JournalFrame sequence (each command with the rule
// version it was accepted under), in append order (GR#12: no durable backup
// without a restore path, proved by the round-trip determinism test in
// persistjournal_test.go).
//
// The return type carries the version on purpose (FEAT-9995385612): a
// caller cannot hold the commands without the versions, so replay cannot
// forget to apply them.
//
// Decode errors surface (a corrupt frame is a real failure, never a silent
// skip): the Store already guarantees a torn/partial record left by a crash
// mid-append never reaches here (persist.Store.ReadJournal's contract), so
// any decode failure at this layer is a genuine corruption worth stopping
// on, not routine torn-frame noise. A malformed rule stamp is MET-G5613.
//
// The returned frames are replayed into a fresh engine through that
// engine's normal command path (core.Engine.HandleCommand) by replayFrames,
// which applies each frame's version, so no bespoke command-submission
// mechanism is introduced.
func RestoreCommands(ctx context.Context, store persist.Store, city persist.CityKey) ([]JournalFrame, error) {
	frames, err := store.ReadJournal(ctx, city)
	if err != nil {
		return nil, err
	}
	out := make([]JournalFrame, 0, len(frames))
	for i, frame := range frames {
		f, err := decodeJournalFrame(frame, i, len(frames))
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}
