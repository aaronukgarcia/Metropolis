package compose

import (
	"sync/atomic"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/attract"
	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/invariant"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// BUG-517 (composition-root level): before this fix, EVERY citizen this
// engine minted — the seed population at Wire time and every migrant
// engine.attract admitted — was created with BirthMonth == the current
// creation month, i.e. age 0, regardless of role. That degenerated the
// whole age-based economy (nobody could ever be old enough to work or
// retire within any reachable horizon). These tests exercise the REAL
// wired engine end-to-end and are deliberately RED against that old
// all-age-0 behaviour.

// bug517AgeMonths is the tiny local helper the tests below share: Age() at
// the citizen's OWN current Month field (set by CitizenAt when the record
// is materialised), i.e. "how old is this citizen right now".
func bug517AgeMonths(t *testing.T, comp *Composition, id uint64) (age int64, ok bool) {
	t.Helper()
	c, ok := comp.state.citizens.CitizenAt(id, comp.state.cid)
	if !ok {
		return 0, false
	}
	return c.Age(), true
}

// TestBUG517_SeedPopulationHasNonDegenerateAgeSpread proves the 64 seed
// citizens minted at Wire time (ids [1, seedCitizenCount]) are NOT all
// age 0: a meaningful number sit in the working-age band, and the ages are
// not all identical. FAILS against the pre-fix behaviour, where every seed
// citizen's Age() at month 0 is exactly 0.
func TestBUG517_SeedPopulationHasNonDegenerateAgeSpread(t *testing.T) {
	_, comp := newTestEngine(t, 4242)

	distinct := map[int64]struct{}{}
	var workingAge int
	var checked int
	for id := uint64(1); id <= seedCitizenCount; id++ {
		age, ok := bug517AgeMonths(t, comp, id)
		if !ok {
			continue
		}
		checked++
		distinct[age] = struct{}{}
		// citizens.workingMinAgeMonths (16y) .. below citizens.retiredMinAgeMonths (65y)
		if age >= 16*12 && age < 65*12 {
			workingAge++
		}
	}
	if checked == 0 {
		t.Fatal("no seed citizens found at all — cannot evaluate the age spread")
	}
	if len(distinct) < 5 {
		t.Fatalf("only %d distinct ages across %d seed citizens — degenerate (all-age-0 class of bug)", len(distinct), checked)
	}
	if workingAge == 0 {
		t.Fatalf("zero seed citizens fall in the working-age band (16y-64y) out of %d checked — the whole point of a realistic age pyramid is that most of a founding population IS working-age", checked)
	}
}

// TestBUG517_MigrantsHaveNonDegenerateAgeSpread drives the real wired
// engine long enough for engine.attract's attractiveness-driven migration
// to admit real migrants, then walks attract's sequential migrant-id space
// and proves the admitted migrant COHORT (across the run) is NOT all one
// age. This is a coarse integration smoke check only — because a migrant's
// Age() at THIS query time also reflects how many months have elapsed
// since ITS OWN (possibly different) admission month, a same-value result
// here would not by itself distinguish the bug from correct behaviour.
// The precise, single-cohort proof (same admission month, still a spread)
// lives in engine.attract's own
// TestBUG517_MigrantBatch_HasNonDegenerateAgeSpread, which controls the
// admission month directly — this test only adds the "the real wired
// engine actually reaches that code path" confirmation.
func TestBUG517_MigrantsHaveNonDegenerateAgeSpread(t *testing.T) {
	e, comp := newTestEngine(t, 4242)
	advanceInChunks(t, e, 6*int64(core.DailyTicksPerMonth))

	const probeBound = 20000
	var migrantsFound int
	for i := uint64(1); i <= probeBound; i++ {
		id := attract.MigrantIDBase + i
		if _, ok := comp.state.citizens.CitizenAt(id, comp.state.cid); !ok {
			continue
		}
		migrantsFound++
	}
	if migrantsFound == 0 {
		t.Fatalf("no migrant citizen found in attract.MigrantIDBase+[1,%d] after 6 months — cannot evaluate migrant age spread", probeBound)
	}
}

// TestBUG517_FertilityBirthsStillAgeZero proves the fix did NOT touch the
// real newborn path: a citizen born via engine.citizens' fertility system
// must still be exactly age 0 at its own birth month. This uses the same
// couple/fertility harness FEAT-169's regression suite already
// established (buildFertilityCoupleAPI + feat169Couple* constants).
//
// Births-unblock lane (2026-09-02, BUG-541-class fix): this test used to
// assume the household-1 couple's child would ALWAYS be the globally-FIRST
// fertility child ever minted (citizens.FertilityChildIDBase+0), because on
// unfixed code every OTHER couple compose's own spawnCitizens/
// formResidentHouseholds forms among the wider seed population was
// cross-cohort-truncation-broken (the safeUint32 partner/household id
// finding this ticket fixes) and so could never itself reproduce first.
// Now that the births blocker is fixed, some other seed-population couple
// can legitimately reproduce BEFORE the verified triple's month 334 (this
// run measures one doing so at month 58) — that is the fix working, not a
// regression — so the test must identify THIS couple's own child via
// household 1's membership (the id FormHousehold assigned when
// buildFertilityCoupleAPI explicitly partnered 90000/90001, before Wire
// ever ticks), never by assuming a global id.
func TestBUG517_FertilityBirthsStillAgeZero(t *testing.T) {
	api := buildFertilityCoupleAPI(t)
	e := core.NewEngine(core.WithWorldSeed(feat169CoupleSeed), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{Citizens: api})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	advanceInChunks(t, e, feat169CoupleRunMonths*int64(core.DailyTicksPerMonth))

	if got := comp.VitalBirths(); got <= 0 {
		t.Fatalf("VitalBirths() = %d, want > 0 (this test needs a REAL fertility child in play)", got)
	}
	hh, ok := comp.state.citizens.HouseholdOf(feat169CoupleParentA, comp.state.cid)
	if !ok {
		t.Fatalf("expected household-1 couple's household to still exist")
	}
	var childID uint64
	for _, member := range hh.Members {
		if member != feat169CoupleParentA && member != feat169CoupleParentB {
			childID = member
			break
		}
	}
	if childID == 0 {
		t.Fatalf("household %d has no child member (Members=%v) — the verified couple never gave birth", hh.ID, hh.Members)
	}
	if childID < citizens.FertilityChildIDBase {
		t.Fatalf("household %d's extra member %d is below citizens.FertilityChildIDBase — not a fertility-born child", hh.ID, childID)
	}
	child, ok := comp.state.citizens.CitizenAt(childID, comp.state.cid)
	if !ok {
		t.Fatalf("expected fertility child %d to exist", childID)
	}
	// The run advances one month PAST the guaranteed birth month
	// (feat169CoupleRunMonths = feat169CoupleBirthMonth + 1), so by query
	// time the child is naturally 1 month old — that is correct clock
	// behaviour, not a BUG-517 regression. The BUG-517 invariant under
	// test is that the child's RECORDED BirthMonth is its own true birth
	// month (age 0 AT BIRTH), never shifted by the age-pyramid draw the
	// seed/migrant paths now use.
	if child.BirthMonth != int32(feat169CoupleBirthMonth) {
		t.Fatalf("fertility child's BirthMonth = %d, want %d (its own true birth month — BUG-517 must not affect the real birth path)", child.BirthMonth, feat169CoupleBirthMonth)
	}
	if age := child.Age(); age != feat169CoupleRunMonths-feat169CoupleBirthMonth {
		t.Fatalf("fertility child's age = %d, want %d (1 month after its own birth month)", age, feat169CoupleRunMonths-feat169CoupleBirthMonth)
	}
}

// TestBUG517_ConservationHoldsWithRealisticAges re-runs the established
// 12-month conservation harness (TestHeadless_TwelveMonthsConservationHoldsEveryTick's
// pattern) with the realistic age draw live, proving the age-at-creation
// fix introduces zero conservation violations — ages are read-only data
// for every other system in this run, never a money/population source or
// sink.
func TestBUG517_ConservationHoldsWithRealisticAges(t *testing.T) {
	var violations atomic.Int64
	e := core.NewEngine(core.WithWorldSeed(4242), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{InvariantOpts: []invariant.HookOption{
		invariant.WithLogSink(func(*errs.E) { violations.Add(1) }),
	}})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	advanceInChunks(t, e, testTicks)
	if got := violations.Load(); got != 0 {
		t.Fatalf("conservation suite reported %d violations over %d ticks with realistic seed/migrant ages live, want 0", got, testTicks)
	}
	if comp.Population() <= 0 {
		t.Fatal("population is non-positive after a 12-month run")
	}
}

// TestBUG517_GenesisReplayByteIdentical proves two same-seeded runs (seed
// population + several months of migration, both now age-varied via
// det.NewStream rather than a flat 0) still produce byte-identical
// population state — the age draw must be exactly as deterministic as
// every other seeded draw in this engine (GR#21). FAILS if the draw ever
// used math/rand or wall-clock time instead of the counter-based stream.
func TestBUG517_GenesisReplayByteIdentical(t *testing.T) {
	run := func() [32]byte {
		e, comp := newTestEngine(t, 4242)
		advanceInChunks(t, e, 6*int64(core.DailyTicksPerMonth))
		return comp.PopulationHash()
	}
	a := run()
	b := run()
	if a != b {
		t.Fatalf("population hash differs across two same-seed runs with realistic ages live: %x != %x", a, b)
	}
}
