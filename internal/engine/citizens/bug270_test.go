package citizens

import (
	"sort"
	"testing"
)

// BUG-270 regression suite: the daily life-event pass (mortality + fertility)
// previously advanced only COLD citizens. A citizen elevated to HOT/WARM was
// skipped by applyMonthly's mortality draw (isHot skip) AND by
// applyFertilityLocked's hot-actor skip, and NO other daily path advanced
// them -- so at high fidelity an elevated citizen could neither die nor bear
// children, indefinitely. These tests prove an elevated citizen now
// experiences the same demographic draws as a cold one, exactly once per
// citizen per month regardless of tier, deterministically, and that an
// elevated death dissolves its household through the same
// removeHouseholdMemberLocked path a cold death (BUG-369) does.

// elevate promotes a citizen to HOT (an "elevated" tier: hot map membership,
// which is what both exclusion sites keyed on -- WARM lives in the same map,
// so HOT exercises the identical skip path).
func elevate(t *testing.T, api *CitizensAPI, id uint64, target Fidelity) {
	t.Helper()
	if err := api.ApplyFidelityCommand(FidelityCommand{CorrelationID: "corr", CitizenID: id, Target: target}); err != nil {
		t.Fatalf("ApplyFidelityCommand(%d,%d): %v", id, target, err)
	}
	if _, ok := api.hot[id]; !ok {
		t.Fatalf("citizen %d not elevated into hot map (fixture broken)", id)
	}
}

// coldSurvivorSet returns the id set currently live in the cold store (the
// single source of truth), sorted, for deterministic comparison.
func coldSurvivorSet(api *CitizensAPI) []uint64 {
	api.mu.RLock()
	defer api.mu.RUnlock()
	var ids []uint64
	for _, s := range api.cold {
		for i := 0; i < s.count(); i++ {
			ids = append(ids, s.ids[i])
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// TestHotElevatedCitizenDies (BUG-270 (a), RED on pre-fix code): an ancient
// citizen whose Gompertz-Makeham hazard clamps to 1 (death guaranteed for
// every possible hash draw, GR#21) must die within one month EVEN WHILE
// ELEVATED to HOT -- and be removed from BOTH the cold store (SSOT) and the
// hot elevation cache. Pre-fix, applyMonthly's isHot skip meant the elevated
// citizen was never drawn and survived forever.
func TestHotElevatedCitizenDies(t *testing.T) {
	api, err := NewCitizensAPI(42, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	const month = int64(20000)
	const id uint64 = 7001
	r := mkRecord(id, 0)
	r.BirthMonth = month - 12000 // age 1000y: hazard clamps to 1
	r.Household = 0
	r.Partner = 0
	if err := api.SeedColdRecords([]ColdRecord{r}, "corr"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	api.mu.Lock()
	api.month = month
	api.mu.Unlock()

	elevate(t, api, id, FidelityHot)

	if err := api.AdvanceMonth("corr"); err != nil {
		t.Fatalf("AdvanceMonth: %v", err)
	}

	if _, ok := api.coldRecord(id); ok {
		t.Fatal("elevated ancient citizen survived a hazard>=1 month (mortality skipped for HOT tier -- BUG-270)")
	}
	api.mu.RLock()
	_, stillHot := api.hot[id]
	api.mu.RUnlock()
	if stillHot {
		t.Fatal("dead elevated citizen still present in the hot elevation cache (dangling hot entry)")
	}
	if got := api.TotalPopulation("corr"); got != 0 {
		t.Fatalf("TotalPopulation = %d, want 0", got)
	}
}

// TestHotElevatedCoupleGivesBirth (BUG-270 (a) fertility, RED on pre-fix
// code): a couple at peak childbearing age, both ELEVATED to HOT, still bears
// a child on the same deterministic (seed, household, month) draw a cold
// couple does. Uses the exact fixture TestFertilityBirthOccursForEligibleCouple
// proves produces a birth at month 405 for a COLD couple -- only the tier
// differs. Pre-fix, applyFertilityLocked's hot-actor skip suppressed it.
func TestHotElevatedCoupleGivesBirth(t *testing.T) {
	api, err := NewCitizensAPI(2, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	const peakAgeMonths = 28 * 12
	const startMonth = 400 - peakAgeMonths
	parentA, parentB, householdID := mkFertilityCouple(t, api, 10, startMonth, 0)

	api.mu.Lock()
	api.month = 400
	api.mu.Unlock()

	// Elevate BOTH partners: the acting (lower-id) partner is the one the
	// fertility pass keyed its hot-actor skip on.
	elevate(t, api, parentA, FidelityHot)
	elevate(t, api, parentB, FidelityHot)

	popBefore := api.TotalPopulation("corr")
	for i := 0; i < 6; i++ {
		if err := api.AdvanceMonth("corr"); err != nil {
			t.Fatalf("AdvanceMonth: %v", err)
		}
	}
	if got := api.TotalPopulation("corr"); got != popBefore+1 {
		t.Fatalf("population = %d, want %d (an elevated couple must still bear a child -- BUG-270)", got, popBefore+1)
	}

	child, ok := api.CitizenAt(fertilityChildIDBase, "corr")
	if !ok {
		t.Fatalf("expected child %d to exist", fertilityChildIDBase)
	}
	if child.Household != householdID {
		t.Fatalf("child.Household = %d, want %d", child.Household, householdID)
	}
	// The birth wired through to both elevated parents' lineage (cold SSOT).
	if got := coldChildCount(t, api, parentA); got != 1 {
		t.Fatalf("parentA childCount = %d, want 1", got)
	}
	if got := coldChildCount(t, api, parentB); got != 1 {
		t.Fatalf("parentB childCount = %d, want 1", got)
	}
}

// TestHotColdMortalityParityAndConservation (BUG-270 (b): exactly-one-draw-
// per-citizen-per-day / no double-count, and same demographic probabilities
// across tiers). A population containing guaranteed-death ancients plus a
// spread of ordinary ages is advanced one month twice: once entirely COLD,
// once entirely HOT. The mortality draw is keyed (seed,id,month) -- tier- and
// iteration-order-independent -- so:
//   - the survivor SET is byte-identical between the two runs (an elevated
//     citizen experiences exactly the same mortality draw as a cold one), and
//   - in each run TotalPopulation change == VitalEvents deaths exactly
//     (conservation: every citizen removed is counted once and only once --
//     no missed citizen, no double-count).
//
// The ancients make deaths>0 deterministically, so the parity assertion is
// never vacuous.
func TestHotColdMortalityParityAndConservation(t *testing.T) {
	const month = int64(20000)
	build := func(allHot bool) (survivors []uint64, deaths, popBefore, popAfter int) {
		api, err := NewCitizensAPI(99, "corr")
		if err != nil {
			t.Fatalf("NewCitizensAPI: %v", err)
		}
		var recs []ColdRecord
		// Three guaranteed-death ancients (hazard clamps to 1).
		for _, id := range []uint64{8001, 8002, 8003} {
			r := mkRecord(id, 0)
			r.BirthMonth = month - 12000
			r.Household = 0
			r.Partner = 0
			recs = append(recs, r)
		}
		// A spread of ordinary-age citizens (tiny hazard; may or may not die,
		// but identically in both runs).
		for id := uint64(8100); id < 8160; id++ {
			r := mkRecord(id, 0)
			r.BirthMonth = month - int64((id%80)*12)
			r.Household = 0
			r.Partner = 0
			recs = append(recs, r)
		}
		if err := api.SeedColdRecords(recs, "corr"); err != nil {
			t.Fatalf("SeedColdRecords: %v", err)
		}
		api.mu.Lock()
		api.month = month
		api.mu.Unlock()
		if allHot {
			for _, r := range recs {
				elevate(t, api, r.ID, FidelityHot)
			}
		}
		popBefore = api.TotalPopulation("corr")
		if err := api.AdvanceMonth("corr"); err != nil {
			t.Fatalf("AdvanceMonth: %v", err)
		}
		popAfter = api.TotalPopulation("corr")
		_, deaths = api.VitalEvents("corr")
		return coldSurvivorSet(api), deaths, popBefore, popAfter
	}

	survCold, deathsCold, beforeCold, afterCold := build(false)
	survHot, deathsHot, beforeHot, afterHot := build(true)

	// Conservation in each run: removed count == reported deaths (exactly once).
	if beforeCold-afterCold != deathsCold {
		t.Fatalf("cold run: removed %d but VitalEvents deaths %d (conservation/exactly-once violated)", beforeCold-afterCold, deathsCold)
	}
	if beforeHot-afterHot != deathsHot {
		t.Fatalf("hot run: removed %d but VitalEvents deaths %d (conservation/exactly-once violated)", beforeHot-afterHot, deathsHot)
	}
	if deathsCold <= 0 {
		t.Fatalf("fixture broken: expected the ancients to die, deathsCold=%d", deathsCold)
	}
	// Tier parity: same demographic outcome regardless of elevation.
	if deathsHot != deathsCold {
		t.Fatalf("elevated population suffered %d deaths, cold suffered %d -- HOT/WARM must experience the same mortality (BUG-270)", deathsHot, deathsCold)
	}
	if len(survCold) != len(survHot) {
		t.Fatalf("survivor counts differ by tier: cold=%d hot=%d", len(survCold), len(survHot))
	}
	for i := range survCold {
		if survCold[i] != survHot[i] {
			t.Fatalf("survivor sets differ by tier at %d: cold=%d hot=%d (mortality draw must be tier-independent)", i, survCold[i], survHot[i])
		}
	}
}

// TestHotElevatedDeathDissolvesHousehold (BUG-270 (d): BUG-369 parity for the
// elevated path). The BUG-369 dissolution scenario -- an elder dies while
// partnered to a widow who shares the household with a genuine child -- must
// dissolve the pairing and prune the household identically when the elder is
// ELEVATED to HOT, routing through the same removeHouseholdMemberLocked path.
func TestHotElevatedDeathDissolvesHousehold(t *testing.T) {
	api, err := NewCitizensAPI(42, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	const month = int64(20000)
	const elderID uint64 = 6001
	const widowID uint64 = 6002
	const childID uint64 = 6003

	elder := mkRecord(elderID, 0)
	elder.BirthMonth = month - 12000 // hazard clamps to 1
	elder.Household = 0
	elder.Partner = 0
	widow := mkRecord(widowID, 0)
	widow.BirthMonth = month - 1100 // above the fertility window
	widow.Household = 0
	widow.Partner = 0
	if err := api.SeedColdRecords([]ColdRecord{elder, widow}, "corr"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	if err := api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventPartner, CitizenID: elderID, PartnerID: widowID}); err != nil {
		t.Fatalf("partner: %v", err)
	}
	hh, ok := api.HouseholdOf(elderID, "corr")
	if !ok {
		t.Fatal("household not formed")
	}
	child := mkRecord(childID, 0)
	child.BirthMonth = month - 1
	child.Household = safeUint32(hh.ID)
	child.Partner = 0
	if err := api.SeedColdRecords([]ColdRecord{child}, "corr"); err != nil {
		t.Fatalf("SeedColdRecords child: %v", err)
	}
	api.mu.Lock()
	api.households[hh.ID].AddMember(child.ID)
	api.month = month
	api.mu.Unlock()

	// Elevate the elder (and widow) to HOT so the death happens on the hot path.
	elevate(t, api, elderID, FidelityHot)
	elevate(t, api, widowID, FidelityHot)

	if err := api.AdvanceMonth("corr"); err != nil {
		t.Fatalf("AdvanceMonth: %v", err)
	}

	if _, ok := api.coldRecord(elderID); ok {
		t.Fatal("elevated elder survived a hazard>=1 month -- fixture/fix broken")
	}
	api.mu.RLock()
	_, elderStillHot := api.hot[elderID]
	api.mu.RUnlock()
	if elderStillHot {
		t.Fatal("dead elevated elder still in the hot cache")
	}

	widowNow, ok := api.CitizenAt(widowID, "corr")
	if !ok {
		t.Fatal("widow vanished alongside the elder (over-removal)")
	}
	if widowNow.Partner != 0 {
		t.Fatalf("widow Partner = %d after elevated elder's death, want 0 (pairing must dissolve via removeHouseholdMemberLocked)", widowNow.Partner)
	}
	if widowNow.Household != hh.ID {
		t.Fatalf("widow Household = %d, want %d (survivor keeps the household)", widowNow.Household, hh.ID)
	}

	h, ok := api.Household(hh.ID, "corr")
	if !ok {
		t.Fatal("household deleted despite surviving members (widow + child)")
	}
	for _, m := range h.Members {
		if m == elderID {
			t.Fatal("household Members still lists the dead elevated elder (leaked member)")
		}
	}
	foundWidow, foundChild := false, false
	for _, m := range h.Members {
		switch m {
		case widowID:
			foundWidow = true
		case childID:
			foundChild = true
		}
	}
	if !foundWidow || !foundChild {
		t.Fatalf("household Members = %v, want [%d %d]", h.Members, widowID, childID)
	}
}

// TestHotLifeEventPassDeterministic (BUG-270 (c), GR#21): a mixed scenario
// exercising the new elevated life-event draws -- a HOT couple who bears a
// child plus an extra HOT/WARM citizen whose per-month mortality draw now
// runs on the elevated path -- produces byte-identical PopulationHash across
// two identically-seeded, identically-elevated runs. (The extra citizen sits
// in the low-month regime where the actuarial hazard is tiny, so what is
// under test is that the elevated draw is DETERMINISTIC, not that it kills.)
func TestHotLifeEventPassDeterministic(t *testing.T) {
	run := func() [32]byte {
		api, err := NewCitizensAPI(2, "corr")
		if err != nil {
			t.Fatalf("NewCitizensAPI: %v", err)
		}
		const peakAgeMonths = 28 * 12
		const startMonth = 400 - peakAgeMonths
		parentA, parentB, _ := mkFertilityCouple(t, api, 10, startMonth, 0)

		extra := mkRecord(6500, 0)
		extra.BirthMonth = 0 // oldest representable at month 400; draw runs each month
		extra.Household = 0
		extra.Partner = 0
		if err := api.SeedColdRecords([]ColdRecord{extra}, "corr"); err != nil {
			t.Fatalf("SeedColdRecords: %v", err)
		}
		api.mu.Lock()
		api.month = 400
		api.mu.Unlock()

		elevate(t, api, parentA, FidelityHot)
		elevate(t, api, parentB, FidelityHot)
		elevate(t, api, extra.ID, FidelityWarm)

		for i := 0; i < 8; i++ {
			if err := api.AdvanceMonth("corr"); err != nil {
				t.Fatalf("AdvanceMonth: %v", err)
			}
		}
		return api.PopulationHash("corr")
	}
	h1 := run()
	h2 := run()
	if h1 != h2 {
		t.Fatalf("PopulationHash differs across identical elevated runs: %x vs %x", h1, h2)
	}
}
