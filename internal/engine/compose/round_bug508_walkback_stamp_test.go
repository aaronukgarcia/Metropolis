package compose

import (
	"context"
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// Independent round (BUG-508). Attacks the one combination the builder's
// single-snapshot BUG-508 fixture does not exercise: a PRE-FIX (never
// seed-stamped) city with MULTIPLE snapshots where the NEWEST candidate is
// tail-inconsistent (walked past) and an OLDER candidate is the one that
// passes. The deferred stamp (stampWorldSeedIfNeeded) must fire EXACTLY
// ONCE — from the candidate that actually passed, not the skipped one, and
// never zero times — so the seed of record is correctly backfilled to the
// legitimate seed and a subsequent wrong-seed attempt is then refused.

const roundBug508SeedX = uint64(50899001)

// TestRoundBUG508_WalkbackSkipStillStampsOnce builds a never-recorded city
// with two snapshots: an OLDER valid one (tick 40, tail empty) and a NEWER
// one whose recorded tick (80) runs ahead of what the journal sums to (40),
// so the walk-back skips the newer (ErrSnapshotTailShort) and lands on the
// older. The restore must succeed, backfill the seed exactly once, and the
// backfilled value must be the legitimate seed (proven by a follow-up
// wrong-seed restore being refused).
func TestRoundBUG508_WalkbackSkipStillStampsOnce(t *testing.T) {
	ctx := context.Background()
	store := persist.NewMemStore()
	city := persist.CityKey{TenantID: "round-508", CityID: "walkback-presnap"}

	e := core.NewEngine(core.WithWorldSeed(roundBug508SeedX), core.WithPoolSize(1))
	comp, err := Wire(e, nil) // no persist deps: no seed.json stamp — pre-fix shape.
	if err != nil {
		t.Fatalf("Wire (fixture): %v", err)
	}

	cell := protocol.CellRef{X: 3, Y: 3}
	mk := func(id string, kind protocol.Kind, payload protocol.CommandPayload) protocol.Command {
		return protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID:   protocol.CorrelationID(id),
			Kind:            kind,
			Payload:         payload,
		}
	}
	// Journal reaches tick 40 exactly and NO further.
	journalCmds := []protocol.Command{
		mk("r508-buy", protocol.KindBuy, protocol.BuyPayload{Cell: cell}),
		mk("r508-zone", protocol.KindZone, protocol.ZonePayload{Cell: cell, ZoneType: "dwelling"}),
		mk("r508-build", protocol.KindBuild, protocol.BuildPayload{Cell: cell, BuildingType: "dwelling"}),
		mk("r508-adv", protocol.KindAdvanceTicks, protocol.AdvanceTicksPayload{N: 40}),
	}
	submitAll(t, e, journalCmds)
	for i, cmd := range journalCmds {
		data, encErr := protocol.EncodeCommand(cmd)
		if encErr != nil {
			t.Fatalf("EncodeCommand %d: %v", i, encErr)
		}
		if appendErr := store.AppendJournal(ctx, city, data); appendErr != nil {
			t.Fatalf("AppendJournal %d: %v", i, appendErr)
		}
	}

	// OLDER snapshot A at tick 40 — valid, tail empty.
	dataA, err := comp.buildSnapshotBytes()
	if err != nil {
		t.Fatalf("buildSnapshotBytes A: %v", err)
	}
	if _, err := store.PutSnapshot(ctx, city, dataA); err != nil {
		t.Fatalf("PutSnapshot A: %v", err)
	}

	// Drive the SAME engine to tick 80 WITHOUT journaling those ticks, so a
	// snapshot taken now records tick 80 while the journal still sums to 40
	// — a tail-short candidate the walk-back must skip.
	submitAll(t, e, []protocol.Command{
		mk("r508-adv2", protocol.KindAdvanceTicks, protocol.AdvanceTicksPayload{N: 40}),
	})
	dataB, err := comp.buildSnapshotBytes()
	if err != nil {
		t.Fatalf("buildSnapshotBytes B: %v", err)
	}
	if _, err := store.PutSnapshot(ctx, city, dataB); err != nil {
		t.Fatalf("PutSnapshot B: %v", err)
	}

	// Preconditions: two snapshots, no recorded seed.
	if ids, err := store.ListSnapshots(ctx, city); err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	} else if len(ids) != 2 {
		t.Fatalf("precondition: got %d snapshots, want 2", len(ids))
	}
	if _, ok, err := store.WorldSeed(ctx, city); err != nil {
		t.Fatalf("WorldSeed precondition: %v", err)
	} else if ok {
		t.Fatal("precondition: fixture already has a recorded seed — not pre-fix")
	}

	// A DOOMED wrong-seed attempt FIRST — on this multi-snapshot pre-fix
	// city — must be refused AND must not poison the (still absent) seed of
	// record. Under the pre-fix top-stamp, this stamps the wrong seed and
	// dooms the legitimate restore below; under the fix it never reaches the
	// stamp (fails closed on the bundle-header check).
	eDoomed := core.NewEngine(core.WithWorldSeed(roundBug508SeedX+7), core.WithPoolSize(1))
	compDoomed, err := Wire(eDoomed, nil)
	if err != nil {
		t.Fatalf("Wire (doomed): %v", err)
	}
	if _, _, derr := RestoreLatestSnapshotOrGenesis(ctx, eDoomed, compDoomed, store, city); derr == nil {
		t.Fatal("doomed wrong-seed walk-back succeeded — must be refused")
	}
	if _, ok, err := store.WorldSeed(ctx, city); err != nil {
		t.Fatalf("WorldSeed after doomed attempt: %v", err)
	} else if ok {
		t.Fatal("doomed wrong-seed attempt POISONED the seed of record (multi-snapshot BUG-508 case)")
	}

	// Legitimate restore at the real seed: newest (tick 80) is tail-short →
	// skipped; older (tick 40) passes. Must succeed and land at tick 40.
	eR := core.NewEngine(core.WithWorldSeed(roundBug508SeedX), core.WithPoolSize(1))
	compR, err := Wire(eR, nil)
	if err != nil {
		t.Fatalf("Wire (restore): %v", err)
	}
	usedSnapshot, tick, err := RestoreLatestSnapshotOrGenesis(ctx, eR, compR, store, city)
	if err != nil {
		t.Fatalf("walk-back restore refused: %v", err)
	}
	if !usedSnapshot {
		t.Fatal("walk-back restore did not use a snapshot (fell through to genesis)")
	}
	if tick != 40 {
		t.Fatalf("walk-back restore tick = %d, want 40 (the older valid candidate)", tick)
	}

	// The stamp must have fired EXACTLY once, recording the legitimate seed.
	recorded, ok, err := store.WorldSeed(ctx, city)
	if err != nil {
		t.Fatalf("WorldSeed after restore: %v", err)
	}
	if !ok {
		t.Fatal("walk-back restore did not backfill a seed of record — stamp fired zero times")
	}
	if recorded != roundBug508SeedX {
		t.Fatalf("backfilled seed = %d, want %d", recorded, roundBug508SeedX)
	}

	// And the backfill must now protect the city: a wrong-seed restore is
	// refused with MET-E819.
	eWrong := core.NewEngine(core.WithWorldSeed(roundBug508SeedX+1), core.WithPoolSize(1))
	compWrong, err := Wire(eWrong, nil)
	if err != nil {
		t.Fatalf("Wire (wrong-seed): %v", err)
	}
	if _, _, err := RestoreLatestSnapshotOrGenesis(ctx, eWrong, compWrong, store, city); err == nil {
		t.Fatal("wrong-seed restore after backfill succeeded — seed of record not enforced")
	} else if !errors.Is(err, &errs.E{Code: save.ErrSaveSeedMismatch}) {
		t.Fatalf("wrong-seed restore error = %v, want %s", err, save.ErrSaveSeedMismatch)
	}
}
