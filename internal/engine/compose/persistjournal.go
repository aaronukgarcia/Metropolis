package compose

import (
	"context"
	"fmt"

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
// principle. engine.core's journalAccepted (commands.go) already surfaces a
// journaler error through the registry (MET-E021) rather than crashing the
// tick, so a durable-persist failure degrades loudly at the seam this
// adapter sits on, exactly as intended.

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
// No mutex, by construction (GR#21): the adapter holds only immutable
// fields set at construction and never mutates its own state, so it is an
// astgate SEC-020 non-candidate (zero copyguard findings expected). All
// concurrency safety lives in the wrapped Store (persist.MemStore/DiskStore
// each guard their own state) and the wrapped Recorder (its own mutex).
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
