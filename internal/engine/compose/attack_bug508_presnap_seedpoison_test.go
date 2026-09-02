package compose

import (
	"context"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// BUG-508 — a PRE-FIX SNAPSHOT city (a snapshot exists, but the city
// predates BUG-488 so no seed.json sidecar was ever stamped) let a DOOMED
// cross-city restore attempt (the wrong seed) POISON the seed of record.
//
// checkOrStampWorldSeed (BUG-488) ran at the very top of
// RestoreLatestSnapshotOrGenesis, before the snapshot branch's own
// bundle-header seed check (BUG-479, inside tryRestoreCandidate ->
// restoreFromSnapshotBytes -> LoadAt) ever got to run. For a never-recorded
// seed it unconditionally stamped the RESTORING engine's seed as the seed
// of record and proceeded. So a wrong-seed restore attempt against a
// pre-fix snapshot city: (a) was still correctly refused overall (fail
// closed — the bundle-header check inside the snapshot branch caught the
// mismatch), but (b) had ALREADY written the wrong seed to seed.json before
// that refusal happened. Every SUBSEQUENT restore attempt — including the
// legitimate one, at the city's real seed — was then refused too, against
// the now-poisoned value.
//
// The fix defers the STAMP (never the CHECK) to a point where a restore
// path has actually earned it: for the snapshot walk-back, only once
// tryRestoreCandidate has proven a candidate's own bundle header matches.

const (
	bug508SeedX = uint64(508101) // the pre-fix snapshot city's real, correct seed.
	bug508SeedY = uint64(508102) // a different engine's seed — the doomed cross-city attempt.
)

// bug508Commands is a short, deterministic, fully-accepted command sequence
// whose single AdvanceTicks lands the engine at tick 40 exactly — this
// becomes both the snapshot's own CreatedAtTick and the exact boundary
// splitJournalAtTick must resolve to an empty tail (the snapshot is the
// newest state; nothing was journaled after it).
func bug508Commands() []protocol.Command {
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
		mk("bug508-buy", protocol.KindBuy, protocol.BuyPayload{Cell: cell}),
		mk("bug508-zone", protocol.KindZone, protocol.ZonePayload{Cell: cell, ZoneType: "dwelling"}),
		mk("bug508-build", protocol.KindBuild, protocol.BuildPayload{Cell: cell, BuildingType: "dwelling"}),
		mk("bug508-adv", protocol.KindAdvanceTicks, protocol.AdvanceTicksPayload{N: 40}),
	}
}

// buildBug508PreFixSnapshotCity fabricates the exact pre-fix shape BUG-508
// is about: a city with a durable SNAPSHOT (built directly via
// buildSnapshotBytes + store.PutSnapshot, bypassing the cadence gate — the
// cadence value itself is irrelevant to this bug) and a durable JOURNAL
// whose commands reproduce the SAME sequence (the "full journal is always
// kept" design invariant), but with NO seed.json sidecar at all —
// Wire(e, nil) (no Deps.PersistStore) never calls SetWorldSeedIfAbsent,
// exactly matching a journal/city that predates BUG-488's stamping.
func buildBug508PreFixSnapshotCity(t *testing.T, ctx context.Context, store persist.Store, city persist.CityKey, seed uint64) {
	t.Helper()
	e := core.NewEngine(core.WithWorldSeed(seed), core.WithPoolSize(1))
	comp, err := Wire(e, nil) // no persist deps: no seed.json stamp, no auto-journaling.
	if err != nil {
		t.Fatalf("Wire (fixture): %v", err)
	}
	cmds := bug508Commands()
	submitAll(t, e, cmds)

	// Reproduce the durable journal by hand (the full journal is always
	// kept, independent of snapshot cadence).
	for i, cmd := range cmds {
		data, encErr := protocol.EncodeCommand(cmd)
		if encErr != nil {
			t.Fatalf("EncodeCommand %d: %v", i, encErr)
		}
		if appendErr := store.AppendJournal(ctx, city, data); appendErr != nil {
			t.Fatalf("AppendJournal %d: %v", i, appendErr)
		}
	}

	data, err := comp.buildSnapshotBytes()
	if err != nil {
		t.Fatalf("buildSnapshotBytes: %v", err)
	}
	if _, err := store.PutSnapshot(ctx, city, data); err != nil {
		t.Fatalf("PutSnapshot: %v", err)
	}
}

// TestAttackBUG508_DoomedCrossCityRestore_DoesNotPoisonSeedOfRecord is the
// RED proof. A pre-fix snapshot city is built at seed X with no seed.json.
// A doomed cross-city restore attempt at seed Y is made — it MUST be
// refused (fail-closed, never a silent cross-city acceptance, exactly as
// documented). A SUBSEQUENT legitimate restore at the city's real seed X
// must then SUCCEED. WITHOUT the fix, the doomed attempt at Y stamps
// seed.json to Y before the bundle-header check refuses it, so the
// legitimate restore at X is wrongly refused too (MET-E819 against the
// poisoned value) — this assertion is what turns the fix off and on.
func TestAttackBUG508_DoomedCrossCityRestore_DoesNotPoisonSeedOfRecord(t *testing.T) {
	ctx := context.Background()
	store := persist.NewMemStore()
	city := persist.CityKey{TenantID: "tenant-508", CityID: "presnap-city"}

	buildBug508PreFixSnapshotCity(t, ctx, store, city, bug508SeedX)

	// Precondition: exactly the pre-fix shape — a snapshot exists, a
	// matching journal exists, but NO seed of record.
	if _, ok, err := store.WorldSeed(ctx, city); err != nil {
		t.Fatalf("WorldSeed precondition: %v", err)
	} else if ok {
		t.Fatal("precondition: fixture already has a recorded seed — not a pre-fix city")
	}
	if ids, err := store.ListSnapshots(ctx, city); err != nil {
		t.Fatalf("ListSnapshots precondition: %v", err)
	} else if len(ids) != 1 {
		t.Fatalf("precondition: got %d snapshots, want 1", len(ids))
	}

	// The DOOMED cross-city restore attempt, at a different seed. It must
	// be refused — never a silent cross-city acceptance.
	eDoomed := core.NewEngine(core.WithWorldSeed(bug508SeedY), core.WithPoolSize(1))
	compDoomed, err := Wire(eDoomed, nil)
	if err != nil {
		t.Fatalf("Wire (doomed attempt): %v", err)
	}
	if _, _, err := RestoreLatestSnapshotOrGenesis(ctx, eDoomed, compDoomed, store, city); err == nil {
		t.Fatal("doomed cross-city restore at the wrong seed succeeded with no error — a cross-city restore must always be refused (fail-closed)")
	}

	// The subsequent LEGITIMATE restore, at the city's real seed, must
	// still succeed. This is the RED assertion: without the fix, the
	// doomed attempt above poisoned seed.json to bug508SeedY, so this
	// legitimate restore at bug508SeedX is wrongly refused too.
	eLegit := core.NewEngine(core.WithWorldSeed(bug508SeedX), core.WithPoolSize(1))
	compLegit, err := Wire(eLegit, nil)
	if err != nil {
		t.Fatalf("Wire (legitimate restore): %v", err)
	}
	usedSnapshot, tick, err := RestoreLatestSnapshotOrGenesis(ctx, eLegit, compLegit, store, city)
	if err != nil {
		t.Fatalf("legitimate restore at the city's real seed was refused: %v — this proves the doomed cross-city attempt above POISONED the seed of record (BUG-508)", err)
	}
	if !usedSnapshot {
		t.Fatal("legitimate restore did not use the snapshot")
	}
	if tick != 40 {
		t.Fatalf("legitimate restore tick = %d, want 40", tick)
	}

	// The seed of record must now be the LEGITIMATE seed, never the
	// doomed attempt's.
	recorded, ok, err := store.WorldSeed(ctx, city)
	if err != nil {
		t.Fatalf("WorldSeed after legitimate restore: %v", err)
	}
	if !ok {
		t.Fatal("legitimate restore did not backfill a seed of record")
	}
	if recorded != bug508SeedX {
		t.Fatalf("seed of record = %d, want %d (bug508SeedX) — got %d (bug508SeedY, the doomed attempt) if this fails on the pre-fix code", recorded, bug508SeedX, bug508SeedY)
	}
}
