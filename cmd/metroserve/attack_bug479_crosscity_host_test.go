package main

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
)

// BUG-479 independent destructive round (Opus r2, 2026-09-01).
//
// compose/attack_bug479_crosscity_test.go proves the refusal at the
// COMPOSE layer, with hand-picked seeds (roundTripSeed vs roundTripSeed+1)
// and a direct RestoreLatestSnapshotOrGenesis call. Nothing proved it at
// the layer that actually owns per-city seeding in production:
// cmd/metroserve's buildCity derives the seed with seedForCity(key) and
// restores through wireAndRehydrate. That production pairing — the SAME
// seedForCity value at create and at restore — is exactly what the r1
// round found violated inside the inc3b test itself, so it deserves a
// regression of its own rather than only a comment in the fixed test.
//
// This test drives two real cities through a real CityHost, then attempts
// the mis-keyed restore (city A's durable records into an engine seeded
// for city B) end-to-end through wireAndRehydrate, and requires
// MET-E819. The same-city control in the same test proves the refusal is
// caused by the seed and not by the cross-city plumbing generally.
func TestAttackBUG479_HostCrossCityRehydrate_Refused(t *testing.T) {
	dir := t.TempDir()
	h, err := NewCityHost(dir, time.Millisecond, WithSnapshotEvery(2))
	if err != nil {
		t.Fatalf("NewCityHost: %v", err)
	}
	h.engineOpts = testEngineOpts()

	cityA := persist.CityKey{TenantID: persistTenantID, CityID: "xcity-a"}
	cityB := persist.CityKey{TenantID: persistTenantID, CityID: "xcity-b"}

	// Precondition for the whole attack: the two cities really do get
	// different world seeds. If seedForCity ever collided for these keys
	// the test below would be vacuous, so assert it rather than assume it.
	if seedForCity(cityA) == seedForCity(cityB) {
		t.Fatalf("fixture bug: seedForCity collided for %q and %q (%d) — the cross-city premise is gone",
			cityA.CityID, cityB.CityID, seedForCity(cityA))
	}

	for _, k := range []persist.CityKey{cityA, cityB} {
		if _, err := h.GetOrCreate(context.Background(), k); err != nil {
			t.Fatalf("GetOrCreate(%s): %v", k.CityID, err)
		}
	}

	disk, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}

	// Wait until city A has a durable SNAPSHOT (not merely a journal): the
	// seed check lives on the snapshot/LoadAt path, so a journal-only
	// genesis replay would not exercise it.
	deadline := time.Now().Add(10 * time.Second)
	ready := func() bool {
		ids, err := disk.ListSnapshots(context.Background(), cityA)
		if err != nil {
			t.Fatalf("ListSnapshots(%s): %v", cityA.CityID, err)
		}
		return len(ids) >= 1
	}
	for time.Now().Before(deadline) && !ready() {
		time.Sleep(5 * time.Millisecond)
	}
	if !ready() {
		t.Fatal("city A never snapshotted within the deadline — the snapshot path this test attacks was never reached")
	}
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// THE ATTACK: restore city A's durable records into an engine seeded
	// for city B — the mis-keyed store lookup / copied-blob / tenant-typo
	// scenario, run through production's own rehydrate seam.
	var xlog bytes.Buffer
	eB := core.NewEngine(core.WithWorldSeed(seedForCity(cityB)), core.WithPoolSize(1))
	_, err = wireAndRehydrate(context.Background(), eB, disk, cityA, &xlog)
	if err == nil {
		t.Fatalf("city A's snapshot rehydrated into a city-B-seeded engine with NO error — a foreign-seed restore must be refused (log=%q)", xlog.String())
	}
	if !errors.Is(err, &errs.E{Code: save.ErrSaveSeedMismatch}) {
		t.Fatalf("cross-city rehydrate error = %v, want %s", err, save.ErrSaveSeedMismatch)
	}

	// CONTROL (prove-can-fail): the identical call with the CORRECT
	// seedForCity(cityA) engine must succeed. If this ever fails, the
	// refusal above is not seed-specific and the assertion is worthless.
	var ok1log bytes.Buffer
	eA := core.NewEngine(core.WithWorldSeed(seedForCity(cityA)), core.WithPoolSize(1))
	if _, err := wireAndRehydrate(context.Background(), eA, disk, cityA, &ok1log); err != nil {
		t.Fatalf("control: correctly-seeded rehydrate of city A FAILED: %v — log=%q", err, ok1log.String())
	}
}
