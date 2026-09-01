package compose

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
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
	data, err := protocol.EncodeCommand(cmd)
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

// RestoreCommands reads every durably-persisted command frame for city and
// decodes it back into a protocol.Command sequence, in append order
// (GR#12: no durable backup without a restore path, proved by the
// round-trip determinism test in persistjournal_test.go).
//
// Decode errors surface (a corrupt frame is a real failure, never a silent
// skip): the Store already guarantees a torn/partial record left by a crash
// mid-append never reaches here (persist.Store.ReadJournal's contract), so
// any decode failure at this layer is a genuine corruption worth stopping
// on, not routine torn-frame noise.
//
// The returned commands are replayed into a fresh engine through that
// engine's normal command path (core.Engine.HandleCommand) — the same path
// the live commands took — so no bespoke command-submission mechanism is
// introduced.
func RestoreCommands(ctx context.Context, store persist.Store, city persist.CityKey) ([]protocol.Command, error) {
	frames, err := store.ReadJournal(ctx, city)
	if err != nil {
		return nil, err
	}
	cmds := make([]protocol.Command, 0, len(frames))
	for i, frame := range frames {
		cmd, err := protocol.DecodeCommand(frame)
		if err != nil {
			return nil, fmt.Errorf("compose: restore command frame %d of %d: %w", i, len(frames), err)
		}
		cmds = append(cmds, cmd)
	}
	return cmds, nil
}
