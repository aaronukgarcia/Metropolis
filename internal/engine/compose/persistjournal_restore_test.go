package compose

import (
	"context"
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/harness/replay"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-1972079936 Phase 1 inc2 — destructive-round regression tests for the
// restore path's failure and isolation behaviour, covering gaps the
// round-trip/codec/fail-closed tests do not: a complete-but-undecodable
// frame must SURFACE (never a silent skip = restore-side data loss), an
// unknown command kind must surface, an empty journal restores cleanly, and
// two cities never cross-contaminate.

// TestRestore_DecodeErrorSurfaces proves a corrupt-but-complete frame (not a
// torn tail — the Store returns it whole) makes RestoreCommands FAIL rather
// than silently drop it. A silent skip would be exactly the restore-side
// data loss this epic exists to kill.
func TestRestore_DecodeErrorSurfaces(t *testing.T) {
	ctx := context.Background()
	store := persist.NewMemStore()
	good, err := protocol.EncodeCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   "ok",
		Kind:            protocol.KindSetSpeed,
		Payload:         protocol.SetSpeedPayload{Speed: 2},
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := store.AppendJournal(ctx, defaultPersistCity, good); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendJournal(ctx, defaultPersistCity, []byte("{not valid json")); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreCommands(ctx, store, defaultPersistCity); err == nil {
		t.Fatal("RestoreCommands silently accepted a garbage frame — restore-side data loss")
	}
}

// TestRestore_UnknownKindSurfaces proves a well-formed envelope with an
// unregistered Kind also surfaces (errors.Is ErrUnknownCommandKind), rather
// than being silently dropped.
func TestRestore_UnknownKindSurfaces(t *testing.T) {
	ctx := context.Background()
	store := persist.NewMemStore()
	frame := []byte(`{"protocolVersion":"1.0.0","correlationId":"x","kind":"totally-made-up","payload":{}}`)
	if err := store.AppendJournal(ctx, defaultPersistCity, frame); err != nil {
		t.Fatal(err)
	}
	_, err := RestoreCommands(ctx, store, defaultPersistCity)
	if err == nil {
		t.Fatal("unknown-kind frame silently skipped on restore")
	}
	if !errors.Is(err, protocol.ErrUnknownCommandKind) {
		t.Fatalf("want ErrUnknownCommandKind, got %v", err)
	}
}

// TestRestore_EmptyJournal proves an empty journal restores to zero commands
// with no error and no panic (genesis).
func TestRestore_EmptyJournal(t *testing.T) {
	ctx := context.Background()
	cmds, err := RestoreCommands(ctx, persist.NewMemStore(), defaultPersistCity)
	if err != nil {
		t.Fatalf("empty restore errored: %v", err)
	}
	if len(cmds) != 0 {
		t.Fatalf("empty restore returned %d commands, want 0", len(cmds))
	}
}

// TestRestore_CityIsolation proves two cities in one Store keep separate
// journals and restore never crosses them (Phase 2 multi-tenant precondition).
func TestRestore_CityIsolation(t *testing.T) {
	ctx := context.Background()
	store := persist.NewMemStore()
	cityA := persist.CityKey{TenantID: "local", CityID: "A"}
	cityB := persist.CityKey{TenantID: "local", CityID: "B"}
	adA := newPersistCommandJournaler(replay.NewRecorder(), store, cityA)
	adB := newPersistCommandJournaler(replay.NewRecorder(), store, cityB)

	mustObserve := func(ad *persistCommandJournaler, id string, kind protocol.Kind, p protocol.CommandPayload) {
		t.Helper()
		if err := ad.ObserveCommand(protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID:   protocol.CorrelationID(id),
			Kind:            kind,
			Payload:         p,
		}); err != nil {
			t.Fatalf("observe %s: %v", id, err)
		}
	}
	mustObserve(adA, "a1", protocol.KindSetSpeed, protocol.SetSpeedPayload{Speed: 2})
	mustObserve(adB, "b1", protocol.KindPause, protocol.PausePayload{})
	mustObserve(adB, "b2", protocol.KindResume, protocol.ResumePayload{})

	a, err := RestoreCommands(ctx, store, cityA)
	if err != nil {
		t.Fatal(err)
	}
	b, err := RestoreCommands(ctx, store, cityB)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 1 || len(b) != 2 {
		t.Fatalf("city isolation broken: A=%d (want 1) B=%d (want 2)", len(a), len(b))
	}
	if a[0].CorrelationID != "a1" || b[0].CorrelationID != "b1" {
		t.Fatalf("cross-contamination: A[0]=%s B[0]=%s", a[0].CorrelationID, b[0].CorrelationID)
	}
}
