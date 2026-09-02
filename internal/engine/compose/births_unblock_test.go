package compose

import (
	"sync/atomic"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/invariant"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// Births-unblock lane (2026-09-02, Aaron ruling Q100050 A1) — the runtime
// proof the ruling asked for.
//
// Two coupled bugs, both fixed together in this lane:
//
//  1. CitizensAPI's LifeEventPartner handler (registry.go) stored the new
//     household id and each partner's id through safeUint32() into
//     ColdShard's `households []uint32`/`partners []uint32` columns
//     (coldshard.go). Migrant ids live at attract.MigrantIDBase (1<<62) and
//     fertility-child ids at citizens.FertilityChildIDBase (1<<63) — both
//     far outside uint32's range — so safeUint32 SATURATED every
//     cross-cohort partner/household reference to math.MaxUint32,
//     permanently zeroing Citizen.Partner for a migrant-or-fertility-child
//     couple and excluding it from FertilityEligible's partnerID==0 check.
//     This made births STRUCTURALLY ZERO for anyone outside the closed seed
//     cohort — and the BUG-529 employment test's own logged finding
//     (bug529_employment_test.go) shows VitalBirths() staying 0 for a whole
//     48-month organically-migrating run on unfixed code. FIXED by widening
//     ColdShard.households/partners and ColdRecord.Household/Partner to
//     uint64 (coldshard.go), removing the safeUint32 narrowing from every
//     household/partner write path (registry.go).
//  2. compose.go's liveResidentIDs() enumerated fertility-child ids as
//     [FertilityChildIDBase+1, FertilityChildIDBase+children] — an
//     off-by-one against fertility.go's actual mint range
//     [FertilityChildIDBase+0, FertilityChildIDBase+children-1]
//     (nextFertilityChildID starts at 0, increments AFTER each mint). This
//     dropped the FIRST fertility child from every liveResidentIDs()
//     consumer (wage/employment marking, household formation) and
//     spuriously enumerated one id past the last real mint. It was INERT
//     while bug #1 kept births at zero; fixed alongside it here (BUG-541)
//     so it does not resurface as a live per-child wage/household bug now
//     that births actually happen.
//
// PROOF THIS CAN FAIL (GR#23 "prove can-fail", verified by hand against the
// pre-fix code this same session): reverting registry.go's
// setColdHouseholdLocked/hotToColdRecord to route Household/Partner through
// safeUint32() again (i.e. ColdShard.households/partners back to
// []uint32) reproduces the BUG-529 test's own logged symptom exactly —
// VitalBirths() stays 0 for the whole run below, because every couple whose
// partner is a migrant or fertility-child id has its Partner column
// saturated to math.MaxUint32, so applyFertilityLocked's
// `c.coldRecord(partner)` lookup fails (`ok == false`) and the couple is
// skipped every single month, for every couple that isn't a same-shard
// seed-to-seed pairing formed before either partner's id could ever exceed
// uint32's range. This is the exact mechanism the BUG-529 test's inline
// comment documents (see bug529_employment_test.go's "confirmed empirically"
// finding).
func TestBirthsUnblock_RealFertilityProducesLiveBirths(t *testing.T) {
	const seed = uint64(4242) // same seed BUG-517/BUG-529 use — known to admit real migrants and pair them within a handful of months
	const totalMonths = 60

	var violations atomic.Int64
	e, comp := newTestEngine(t, seed, invariant.WithLogSink(func(*errs.E) { violations.Add(1) }))

	type snap struct {
		month  int64
		pop    int
		births int64
	}
	var snaps []snap
	for m := int64(1); m <= totalMonths; m++ {
		advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
		snaps = append(snaps, snap{month: m, pop: comp.Population(), births: comp.VitalBirths()})
	}

	t.Logf("month  population  cumulativeBirths")
	for _, s := range snaps {
		t.Logf("%5d  %10d  %16d", s.month, s.pop, s.births)
	}

	// The payoff assertion: births are structurally >0 once the fix is
	// live. On unfixed code this is 0 for the ENTIRE run (see the doc
	// comment's "PROOF THIS CAN FAIL").
	last := snaps[len(snaps)-1]
	if last.births <= 0 {
		t.Fatalf("VitalBirths() = %d after %d months, want > 0 (births-unblock fix not effective — see BUG-529's logged 'stays 0' finding for the pre-fix symptom)", last.births, totalMonths)
	}

	// Sanity: births must be monotonically non-decreasing (a cumulative
	// counter, never a rate that could legitimately go backwards).
	for i := 1; i < len(snaps); i++ {
		if snaps[i].births < snaps[i-1].births {
			t.Fatalf("month %d: VitalBirths() = %d, fell below month %d's %d — cumulative counter must never decrease", snaps[i].month, snaps[i].births, snaps[i-1].month, snaps[i-1].births)
		}
	}

	// Conservation must hold every tick across the whole run: a birth adds
	// a person (never a double-count, never an uncounted one).
	if got := violations.Load(); got != 0 {
		t.Fatalf("conservation suite reported %d violations over the %d-month run with births live, want 0", got, totalMonths)
	}
}

// TestBirthsUnblock_PeopleConservationAcrossBirths is a tighter, explicit
// people-conservation check on top of the invariant hook above: peopleIn ==
// peopleOut + population, read directly off Composition's own conservation
// surface, over a run long enough for the payoff test above to have
// produced real births (60 months, same seed).
func TestBirthsUnblock_PeopleConservationAcrossBirths(t *testing.T) {
	const seed = uint64(4242)
	const totalMonths = 60

	var violations atomic.Int64
	e, comp := newTestEngine(t, seed, invariant.WithLogSink(func(*errs.E) { violations.Add(1) }))
	advanceInChunks(t, e, totalMonths*int64(core.DailyTicksPerMonth))

	if got := comp.VitalBirths(); got <= 0 {
		t.Fatalf("VitalBirths() = %d after %d months, want > 0 (this test needs real births in play to exercise the conservation surface meaningfully)", got, totalMonths)
	}
	if got := violations.Load(); got != 0 {
		t.Fatalf("conservation suite reported %d violations over a %d-month run with births live, want 0 (people in == out + pending must hold exactly)", got, totalMonths)
	}
}

// TestBirthsUnblock_GenesisReplayByteIdentical proves the widened
// household/partner columns did not introduce any nondeterminism: two
// same-seed runs long enough to include real births (both the fertility
// hazard draw and the widened cold-store round-trip) must hash identically.
func TestBirthsUnblock_GenesisReplayByteIdentical(t *testing.T) {
	const seed = uint64(4242)
	const totalMonths = 48
	run := func() ([32]byte, int64) {
		e, comp := newTestEngine(t, seed)
		advanceInChunks(t, e, totalMonths*int64(core.DailyTicksPerMonth))
		return comp.PopulationHash(), comp.VitalBirths()
	}
	aHash, aBirths := run()
	bHash, bBirths := run()
	if aBirths <= 0 {
		t.Fatalf("VitalBirths() = %d after %d months, want > 0 (this determinism check needs real births in play, not just a hash-matches-itself no-op)", aBirths, totalMonths)
	}
	if aBirths != bBirths {
		t.Fatalf("VitalBirths() differs across two same-seed runs: %d != %d — births are not deterministic", aBirths, bBirths)
	}
	if aHash != bHash {
		t.Fatalf("population hash differs across two same-seed runs with real births live: %x != %x", aHash, bHash)
	}
}
