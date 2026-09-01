package compose

import (
	"context"
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
)

// BUG-479 independent destructive round (Opus r1, 2026-09-01) — the
// production-shaped version of the defect.
//
// cmd/metroserve's CityHost gives every city its OWN world seed, derived
// per-city from the city key (seedForCity: sha256(tenant)+sha256(city) ->
// the first 8 bytes as a uint64, cityhost.go). So the realistic way a
// foreign-seeded bundle reaches a composition is not a hand-built fixture:
// it is a snapshot written for city A being restored into city B's
// composition — a mis-keyed store lookup, a copied snapshot blob, a
// tenant-id typo. That path runs through
// RestoreLatestSnapshotOrGenesis -> restoreFromSnapshotBytes -> LoadAt,
// i.e. through the check BUG-479 added, and must now be REFUSED rather
// than silently producing a city whose every seed-derived draw diverges
// from the state it just loaded.
//
// Note also, from seedForCity's shape, that real city seeds are FULL
// 64-bit values with roughly a 50% chance of the top bit set (i.e.
// negative once cast to the header's int64) — see
// attack_bug479_seedbits_test.go for the width/sign regressions that
// keeps honest.

// TestAttackBUG479_CrossCitySnapshotRestore_Refused writes a durable
// snapshot from a composition on city A's seed and attempts the standard
// RestoreLatestSnapshotOrGenesis restore into a freshly Wired composition
// on city B's seed. It must fail with save.ErrSaveSeedMismatch, and the
// restore target must be left at its pristine digest and tick 0 — no
// half-restored state, and no journal tail replayed on top of a refused
// snapshot.
func TestAttackBUG479_CrossCitySnapshotRestore_Refused(t *testing.T) {
	ctx := context.Background()

	// City A: a persisted composition (seed roundTripSeed, as
	// buildPersistedComposition wires it), driven past the snapshot
	// cadence boundary and snapshotted durably.
	store := persist.NewMemStore()
	cityA := persist.CityKey{TenantID: "tenant-a", CityID: "city-a"}
	eLive, compLive := buildPersistedComposition(t, store, cityA)
	driveGameplayOnly(t, eLive)
	advanceViaCommand(t, eLive, 160)
	advanceViaCommand(t, eLive, 200) // tick 360 — the cadence boundary
	if _, ok, err := compLive.MaybeSnapshot(ctx, store, cityA); err != nil {
		t.Fatalf("MaybeSnapshot: %v", err)
	} else if !ok {
		t.Fatal("precondition: expected a snapshot at tick 360, got none")
	}
	advanceViaCommand(t, eLive, 10)

	// City B's composition: a DIFFERENT world seed, exactly as
	// seedForCity would produce for a different city key. It is pointed
	// at city A's records (the mis-keyed-restore scenario).
	const cityBSeed = roundTripSeed + 1
	eB := core.NewEngine(core.WithWorldSeed(cityBSeed), core.WithPoolSize(1))
	compB, err := Wire(eB, nil)
	if err != nil {
		t.Fatalf("Wire (city B): %v", err)
	}
	_, pristineB := buildCompositionWithSeed(t, cityBSeed)
	pristineDigest := pristineB.StateDigest()
	if compB.StateDigest() != pristineDigest {
		t.Fatal("fixture bug: two freshly Wired city-B compositions disagree before any restore")
	}

	usedSnapshot, tick, err := RestoreLatestSnapshotOrGenesis(ctx, eB, compB, store, cityA)
	if err == nil {
		t.Fatalf("city A's snapshot restored into city B's composition with NO error (usedSnapshot=%v tick=%d) — a foreign-seed snapshot restore must be refused", usedSnapshot, tick)
	}
	if !errors.Is(err, &errs.E{Code: save.ErrSaveSeedMismatch}) {
		t.Fatalf("restore error = %v, want %s", err, save.ErrSaveSeedMismatch)
	}

	if got := compB.StateDigest(); got != pristineDigest {
		t.Fatalf("city B's composition state CHANGED on a refused cross-city restore: pristine=%x post-refusal=%x", pristineDigest, got)
	}
	clockB, err := eB.Clock()
	if err != nil {
		t.Fatalf("eB.Clock: %v", err)
	}
	if clockB.Tick() != 0 {
		t.Fatalf("refused cross-city restore still moved city B's clock to tick %d (the snapshot's clock seed, or a replayed journal tail, leaked through the refusal)", clockB.Tick())
	}
}

// TestAttackBUG479_SameCitySnapshotRestore_StillWorks is the
// prove-can-fail companion: the identical restore into a composition on
// the SAME seed must still succeed and use the snapshot, so the refusal
// above is attributable to the seed check and not to anything else about
// the cross-city fixture (store keying, journal contents, cadence).
func TestAttackBUG479_SameCitySnapshotRestore_StillWorks(t *testing.T) {
	ctx := context.Background()

	store := persist.NewMemStore()
	cityA := persist.CityKey{TenantID: "tenant-a", CityID: "city-a"}
	eLive, compLive := buildPersistedComposition(t, store, cityA)
	driveGameplayOnly(t, eLive)
	advanceViaCommand(t, eLive, 160)
	advanceViaCommand(t, eLive, 200)
	if _, ok, err := compLive.MaybeSnapshot(ctx, store, cityA); err != nil {
		t.Fatalf("MaybeSnapshot: %v", err)
	} else if !ok {
		t.Fatal("precondition: expected a snapshot at tick 360, got none")
	}
	advanceViaCommand(t, eLive, 10)

	eR := core.NewEngine(core.WithWorldSeed(roundTripSeed), core.WithPoolSize(1))
	compR, err := Wire(eR, nil)
	if err != nil {
		t.Fatalf("Wire (restore target): %v", err)
	}
	usedSnapshot, tick, err := RestoreLatestSnapshotOrGenesis(ctx, eR, compR, store, cityA)
	if err != nil {
		t.Fatalf("same-seed snapshot restore failed: %v — the cross-city refusal cannot be attributed to the seed check unless this succeeds", err)
	}
	if !usedSnapshot {
		t.Fatal("same-seed restore did not use the snapshot")
	}
	if tick != 370 {
		t.Fatalf("same-seed restore tick = %d, want 370", tick)
	}
	if compR.StateDigest() != compLive.StateDigest() {
		t.Fatalf("same-seed restore digest mismatch: got %x, want %x", compR.StateDigest(), compLive.StateDigest())
	}
}
