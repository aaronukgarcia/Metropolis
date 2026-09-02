package compose

import (
	"context"
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/harness/replay"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-1972079936 Phase 1 inc2 — durable command-journal write-through +
// restore. These tests prove the persisted journal restores
// byte-identically (GR#12: no durable backup without a restore test), that
// persistence is a pure side channel (persist-on state == persist-off
// state), that the durable bytes equal the in-memory Recorder's bytes
// (codec consistency), and that a durable-persist failure is fail-closed
// (surfaced, never swallowed).

const persistTestSeed = uint64(20260831)

// rtCommands is a deterministic sequence of accepted commands driven
// through the real composed engine's command path (core.Engine.HandleCommand
// — the same path the live engine and every restore takes). It mixes clock
// advances (which run the phase pipeline, so the build order below actually
// progresses) with the buy/zone/build gameplay vocabulary, so the resulting
// StateDigest genuinely moves off the boot state.
func rtCommands() []protocol.Command {
	cell := protocol.CellRef{X: 3, Y: 3}
	mk := func(id string, kind protocol.Kind, payload protocol.CommandPayload) protocol.Command {
		return protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID:   protocol.CorrelationID(id),
			Kind:            kind,
			Payload:         payload,
		}
	}
	return []protocol.Command{
		mk("rt-adv-1", protocol.KindAdvanceTicks, protocol.AdvanceTicksPayload{N: 5}),
		mk("rt-buy", protocol.KindBuy, protocol.BuyPayload{Cell: cell}),
		mk("rt-zone", protocol.KindZone, protocol.ZonePayload{Cell: cell, ZoneType: "dwelling"}),
		mk("rt-build", protocol.KindBuild, protocol.BuildPayload{Cell: cell, BuildingType: "dwelling"}),
		mk("rt-adv-2", protocol.KindAdvanceTicks, protocol.AdvanceTicksPayload{N: 60}),
	}
}

// submitAll drives cmds through e.HandleCommand in order, failing the test
// if any command is rejected (the sequence is designed to be fully accepted
// against a fresh composition).
func submitAll(t *testing.T, e *core.Engine, cmds []protocol.Command) {
	t.Helper()
	for i, cmd := range cmds {
		if res := e.HandleCommand(cmd); !res.Accepted {
			t.Fatalf("command %d (%s) rejected: %+v", i, cmd.Kind, res.Error)
		}
	}
}

// newPersistComposition builds a composition at persistTestSeed with the
// given Store wired for durable persistence under the default placeholder
// city.
func newPersistComposition(t *testing.T, store persist.Store) (*core.Engine, *Composition) {
	t.Helper()
	e := core.NewEngine(core.WithWorldSeed(persistTestSeed), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{PersistStore: store})
	if err != nil {
		t.Fatalf("Wire (persist on): %v", err)
	}
	return e, comp
}

// TestPersistJournal_RoundTripDeterministic is the acceptance bar (AC-5):
// submit N accepted commands through a persist-wired composition, read the
// journal back via RestoreCommands, replay into a FRESH composition, and
// assert the two engines' StateDigest snapshots are byte-identical. Then
// prove the test can fail: replaying a MUTATED command sequence diverges.
func TestPersistJournal_RoundTripDeterministic(t *testing.T) {
	ctx := context.Background()
	store := persist.NewMemStore()

	// (a)+(b): live run, durably persisted.
	eLive, compLive := newPersistComposition(t, store)
	submitAll(t, eLive, rtCommands())
	liveDigest := compLive.StateDigest()

	// (c): read the durable journal back.
	restored, err := RestoreCommands(ctx, store, defaultPersistCity)
	if err != nil {
		t.Fatalf("RestoreCommands: %v", err)
	}
	if len(restored) != len(rtCommands()) {
		t.Fatalf("RestoreCommands returned %d commands, want %d", len(restored), len(rtCommands()))
	}

	// (d): restore into a FRESH composition (persist off — the restore path
	// must not depend on persistence being enabled) and replay.
	eFresh := core.NewEngine(core.WithWorldSeed(persistTestSeed), core.WithPoolSize(1))
	compFresh, err := Wire(eFresh, nil)
	if err != nil {
		t.Fatalf("Wire (fresh restore target): %v", err)
	}
	submitAll(t, eFresh, restored)
	restoredDigest := compFresh.StateDigest()

	if liveDigest != restoredDigest {
		t.Fatalf("restore is lossy: live digest %x != restored digest %x", liveDigest, restoredDigest)
	}

	// Prove-can-fail: mutate ONE restored command (bump the final tick
	// advance by one) and confirm the digest diverges — otherwise the
	// equality above would be meaningless.
	mutated := append([]protocol.Command(nil), restored...)
	last := len(mutated) - 1
	p, ok := mutated[last].Payload.(protocol.AdvanceTicksPayload)
	if !ok {
		t.Fatalf("expected last restored command to be AdvanceTicks, got %T", mutated[last].Payload)
	}
	p.N++
	mutated[last].Payload = p

	eMut := core.NewEngine(core.WithWorldSeed(persistTestSeed), core.WithPoolSize(1))
	compMut, err := Wire(eMut, nil)
	if err != nil {
		t.Fatalf("Wire (mutated target): %v", err)
	}
	submitAll(t, eMut, mutated)
	if compMut.StateDigest() == liveDigest {
		t.Fatal("mutated command sequence produced an identical digest — the round-trip equality check cannot detect divergence (false-pass)")
	}
}

// TestPersistJournal_CodecConsistency proves the durably-persisted bytes are
// byte-identical to the in-memory Recorder's bytes for the same command
// (AC-6) — both go through protocol.EncodeCommand, so the durable journal is
// the same wire form replay already depends on.
func TestPersistJournal_CodecConsistency(t *testing.T) {
	ctx := context.Background()
	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID("codec-consistency"),
		Kind:            protocol.KindBuild,
		Payload:         protocol.BuildPayload{Cell: protocol.CellRef{X: 3, Y: 3}, BuildingType: "dwelling"},
	}

	// In-memory Recorder bytes.
	rec := replay.NewRecorder()
	if err := rec.ObserveCommand(cmd); err != nil {
		t.Fatalf("Recorder.ObserveCommand: %v", err)
	}
	recRecords, err := rec.Records()
	if err != nil {
		t.Fatalf("Recorder.Records: %v", err)
	}
	if len(recRecords) != 1 {
		t.Fatalf("Recorder captured %d records, want 1", len(recRecords))
	}
	inMem := recRecords[0].Data

	// Durable bytes via the adapter.
	store := persist.NewMemStore()
	adapter := newPersistCommandJournaler(replay.NewRecorder(), store, defaultPersistCity)
	if err := adapter.ObserveCommand(cmd); err != nil {
		t.Fatalf("adapter.ObserveCommand: %v", err)
	}
	frames, err := store.ReadJournal(ctx, defaultPersistCity)
	if err != nil {
		t.Fatalf("ReadJournal: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("durable journal has %d frames, want 1", len(frames))
	}
	durable := frames[0]

	if string(inMem) != string(durable) {
		t.Fatalf("durable bytes != in-memory Recorder bytes:\n  in-mem:  %s\n  durable: %s", inMem, durable)
	}
	// Both must also equal a direct EncodeCommand (no bespoke serialization).
	direct, err := protocol.EncodeCommand(cmd)
	if err != nil {
		t.Fatalf("EncodeCommand: %v", err)
	}
	if string(durable) != string(direct) {
		t.Fatalf("durable bytes != protocol.EncodeCommand bytes:\n  durable: %s\n  direct:  %s", durable, direct)
	}
}

// TestPersistJournal_PersistOffEqualsPersistOn proves persistence is a pure
// side channel (AC-6): the identical command sequence yields byte-identical
// engine state whether persist is wired or not.
func TestPersistJournal_PersistOffEqualsPersistOn(t *testing.T) {
	// Persist OFF (default path).
	eOff := core.NewEngine(core.WithWorldSeed(persistTestSeed), core.WithPoolSize(1))
	compOff, err := Wire(eOff, nil)
	if err != nil {
		t.Fatalf("Wire (persist off): %v", err)
	}
	submitAll(t, eOff, rtCommands())
	offDigest := compOff.StateDigest()

	// Persist ON.
	eOn, compOn := newPersistComposition(t, persist.NewMemStore())
	submitAll(t, eOn, rtCommands())
	onDigest := compOn.StateDigest()

	if offDigest != onDigest {
		t.Fatalf("persistence influenced engine state (must be a pure side channel): off %x != on %x", offDigest, onDigest)
	}
}

// TestPersistJournal_DefaultJournalerStillRecorder proves the default
// (persist off) path is EXACTLY unchanged: Composition.Journaler() is still
// the in-memory *replay.Recorder, never wrapped.
func TestPersistJournal_DefaultJournalerStillRecorder(t *testing.T) {
	e := core.NewEngine(core.WithWorldSeed(persistTestSeed), core.WithPoolSize(1))
	comp, err := Wire(e, nil)
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	if _, ok := comp.Journaler().(*replay.Recorder); !ok {
		t.Fatalf("persist-off Journaler() = %T, want *replay.Recorder (default path was altered)", comp.Journaler())
	}
}

// errAppendFailed is the sentinel a failing Store returns from AppendJournal.
var errAppendFailed = errors.New("persist stub: AppendJournal forced failure")

// failingStore is a persist.Store whose AppendJournal always errors; every
// other method is unused by these tests (they panic if ever called, so a
// silent wrong-method dependency is caught, not hidden).
type failingStore struct{}

func (failingStore) AppendJournal(context.Context, persist.CityKey, []byte) error {
	return errAppendFailed
}
func (failingStore) ReadJournal(context.Context, persist.CityKey) ([][]byte, error) {
	panic("failingStore.ReadJournal called")
}
func (failingStore) PutSnapshot(context.Context, persist.CityKey, []byte) (persist.SnapshotID, error) {
	panic("failingStore.PutSnapshot called")
}
func (failingStore) GetSnapshot(context.Context, persist.CityKey, persist.SnapshotID) ([]byte, error) {
	panic("failingStore.GetSnapshot called")
}
func (failingStore) ListSnapshots(context.Context, persist.CityKey) ([]persist.SnapshotID, error) {
	panic("failingStore.ListSnapshots called")
}
func (failingStore) DeleteSnapshot(context.Context, persist.CityKey, persist.SnapshotID) error {
	panic("failingStore.DeleteSnapshot called")
}
func (failingStore) ListCities(context.Context, string) ([]persist.CityKey, error) {
	panic("failingStore.ListCities called")
}
func (failingStore) Exists(context.Context, persist.CityKey) (bool, error) {
	panic("failingStore.Exists called")
}
func (failingStore) SetWorldSeedIfAbsent(context.Context, persist.CityKey, uint64) (uint64, error) {
	panic("failingStore.SetWorldSeedIfAbsent called")
}
func (failingStore) WorldSeed(context.Context, persist.CityKey) (uint64, bool, error) {
	panic("failingStore.WorldSeed called")
}

// TestPersistJournal_FailClosed proves a durable-persist failure is
// surfaced, never swallowed (AC-1 fail-closed): the adapter's ObserveCommand
// returns the AppendJournal error verbatim.
func TestPersistJournal_FailClosed(t *testing.T) {
	adapter := newPersistCommandJournaler(replay.NewRecorder(), failingStore{}, defaultPersistCity)
	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID("fail-closed"),
		Kind:            protocol.KindSetSpeed,
		Payload:         protocol.SetSpeedPayload{Speed: 2},
	}
	err := adapter.ObserveCommand(cmd)
	if err == nil {
		t.Fatal("ObserveCommand returned nil on a failing AppendJournal — a durable-persist failure was swallowed (data-loss reopened)")
	}
	if !errors.Is(err, errAppendFailed) {
		t.Fatalf("ObserveCommand returned %v, want the AppendJournal sentinel %v", err, errAppendFailed)
	}
}

// spyInnerJournaler records observed commands and can be told to fail, to
// prove the adapter calls inner FIRST and short-circuits on its error
// without persisting.
type spyInnerJournaler struct {
	observed []protocol.Command
	failWith error
}

func (s *spyInnerJournaler) ObserveCommand(cmd protocol.Command) error {
	if s.failWith != nil {
		return s.failWith
	}
	s.observed = append(s.observed, cmd)
	return nil
}

// TestPersistJournal_InnerErrorShortCircuits proves the adapter calls the
// inner journaler first and, if it errors, returns that error WITHOUT
// persisting (keeping the durable and in-memory journals in lock-step).
func TestPersistJournal_InnerErrorShortCircuits(t *testing.T) {
	ctx := context.Background()
	sentinel := errors.New("inner rejected")
	inner := &spyInnerJournaler{failWith: sentinel}
	store := persist.NewMemStore()
	adapter := newPersistCommandJournaler(inner, store, defaultPersistCity)

	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID("inner-error"),
		Kind:            protocol.KindSetSpeed,
		Payload:         protocol.SetSpeedPayload{Speed: 2},
	}
	if err := adapter.ObserveCommand(cmd); !errors.Is(err, sentinel) {
		t.Fatalf("ObserveCommand = %v, want the inner sentinel %v", err, sentinel)
	}
	frames, err := store.ReadJournal(ctx, defaultPersistCity)
	if err != nil {
		t.Fatalf("ReadJournal: %v", err)
	}
	if len(frames) != 0 {
		t.Fatalf("durable journal has %d frames, want 0 (a command the inner journaler rejected must not be persisted)", len(frames))
	}
}
