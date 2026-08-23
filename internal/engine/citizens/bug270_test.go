package citizens

import "testing"

// mkBug270ElderlyCouple seeds two partnered citizens (ids 10, 11) via the
// real SeedColdRecords + LifeEventPartner paths: citizen 10 is elderly to
// the point of a clamped-to-1 monthly mortality hazard at sim month 4000
// (age 4000 months), citizen 11 is a prime-aged adult whose own hazard is
// negligible over a one-month window. Returns nothing; fails the test on
// any setup error.
func mkBug270ElderlyCouple(t *testing.T, api *CitizensAPI) {
	t.Helper()
	elderly := mkRecord(10, 0)
	elderly.BirthMonth = 0
	elderly.Household = 0
	elderly.Partner = 0
	elderly.ChildCount = 0
	young := mkRecord(11, 0)
	young.BirthMonth = 4000 - 28*12
	young.Household = 0
	young.Partner = 0
	young.ChildCount = 0
	if err := api.SeedColdRecords([]ColdRecord{elderly, young}, "corr"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	if err := api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventPartner, CitizenID: 10, PartnerID: 11}); err != nil {
		t.Fatalf("partner: %v", err)
	}
}

// advanceTicks calls AdvanceDayTick n times, returning the summed per-tick
// births/deaths (the composition root's conservation seam inputs).
func advanceTicks(t *testing.T, api *CitizensAPI, n int) (births, deaths int) {
	t.Helper()
	for i := 0; i < n; i++ {
		b, d, err := api.AdvanceDayTick("corr")
		if err != nil {
			t.Fatalf("AdvanceDayTick: %v", err)
		}
		births += b
		deaths += d
	}
	return births, deaths
}

// TestHotElevatedCitizenDiesAndDissolvesHousehold (BUG-270): a HOT-elevated
// citizen must receive the same monthly mortality pass a COLD citizen gets.
// The fixture makes the outcome deterministic without pinning a magnitude:
// the elevated citizen's age drives the Gompertz-Makeham hazard past its
// [0,1] clamp, so the death is certain under any data-sourced parameters —
// pre-fix the elevated tier was advanced by no path at all and survived
// forever. Also asserts the death lands in AdvanceDayTick's per-tick
// conservation seam AND VitalEvents' completed-month totals, and that the
// surviving partner's pairing dissolves (LifeEventDeath's unwiring contract,
// applied on the elevated path too).
func TestHotElevatedCitizenDiesAndDissolvesHousehold(t *testing.T) {
	api, err := NewCitizensAPI(2, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	mkBug270ElderlyCouple(t, api)
	if err := api.ApplyFidelityCommand(FidelityCommand{CorrelationID: "corr", CitizenID: 10, Target: FidelityHot}); err != nil {
		t.Fatalf("promote 10 to HOT: %v", err)
	}

	api.mu.Lock()
	api.month = 4000
	api.mu.Unlock()

	popBefore := api.TotalPopulation("corr")
	_, tickDeaths := advanceTicks(t, api, DaysPerMonth)

	if _, ok := api.CitizenAt(10, "corr"); ok {
		t.Fatal("HOT-elevated citizen still alive after a certain-fatal month: the elevated tier receives no mortality pass (BUG-270)")
	}
	if got := api.TotalPopulation("corr"); got != popBefore-1 {
		t.Fatalf("TotalPopulation = %d, want %d (exactly the elevated death)", got, popBefore-1)
	}
	if tickDeaths != 1 {
		t.Fatalf("per-tick deaths = %d, want 1 (an elevated death must land in the same-tick conservation seam)", tickDeaths)
	}
	births, deaths := api.VitalEvents("corr")
	if births != 0 || deaths != 1 {
		t.Fatalf("VitalEvents = (%d births, %d deaths), want (0, 1)", births, deaths)
	}

	survivor, ok := api.CitizenAt(11, "corr")
	if !ok {
		t.Fatal("surviving partner vanished")
	}
	if survivor.Partner != 0 {
		t.Fatalf("survivor.Partner = %d, want 0 (the pairing must dissolve on an elevated death)", survivor.Partner)
	}
	hh, ok := api.HouseholdOf(11, "corr")
	if !ok {
		t.Fatal("survivor lost their household (household must persist while any member remains)")
	}
	if len(hh.Members) != 1 || hh.Members[0] != 11 {
		t.Fatalf("household members = %v, want [11] (the departed elevated citizen must be pruned)", hh.Members)
	}
}

// TestHotElevatedCoupleConceives (BUG-270): the identical guaranteed-birth
// fixture TestFertilityBirthOccursForEligibleCouple proves fertile for a
// COLD couple (seed 2, household 1, birth at month 405) must stay equally
// fertile with BOTH partners elevated to HOT — the fertility draw keys on
// (seed, householdID, month), never on fidelity tier. Pre-fix the elevated
// acting partner was skipped and no birth ever occurred.
func TestHotElevatedCoupleConceives(t *testing.T) {
	api, err := NewCitizensAPI(2, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	const peakAgeMonths = 28 * 12
	const startMonth = 400 - peakAgeMonths
	parentA, parentB, householdID := mkFertilityCouple(t, api, 10, startMonth, 0)
	for _, id := range []uint64{parentA, parentB} {
		if err := api.ApplyFidelityCommand(FidelityCommand{CorrelationID: "corr", CitizenID: id, Target: FidelityHot}); err != nil {
			t.Fatalf("promote %d to HOT: %v", id, err)
		}
	}

	api.mu.Lock()
	api.month = 400
	api.mu.Unlock()

	popBefore := api.TotalPopulation("corr")
	tickBirths, _ := advanceTicks(t, api, 6*DaysPerMonth)

	if got := api.TotalPopulation("corr"); got != popBefore+1 {
		t.Fatalf("TotalPopulation = %d, want %d (exactly one birth expected within 6 months)", got, popBefore+1)
	}
	if tickBirths != 1 {
		t.Fatalf("per-tick births = %d, want 1 (an elevated birth must land in the same-tick conservation seam)", tickBirths)
	}

	childID := fertilityChildIDBase
	child, ok := api.CitizenAt(childID, "corr")
	if !ok {
		t.Fatalf("expected child %d to exist", childID)
	}
	if child.BirthMonth != 405 {
		t.Fatalf("child.BirthMonth = %d, want 405", child.BirthMonth)
	}
	if child.Household != householdID {
		t.Fatalf("child.Household = %d, want %d", child.Household, householdID)
	}

	hh, ok := api.Household(householdID, "corr")
	if !ok {
		t.Fatal("household vanished")
	}
	foundChild := false
	for _, m := range hh.Members {
		if m == childID {
			foundChild = true
		}
	}
	if !foundChild || len(hh.Members) != 3 {
		t.Fatalf("household members = %v, want the two parents plus child %d", hh.Members, childID)
	}
	if got := coldChildCount(t, api, parentA); got != 1 {
		t.Fatalf("parentA childCount = %d, want 1", got)
	}
	if got := coldChildCount(t, api, parentB); got != 1 {
		t.Fatalf("parentB childCount = %d, want 1", got)
	}
}

// TestHotLifeEventPassDeterministic (GR#21): two independently constructed
// APIs given the identical seed, commands, and clock produce byte-identical
// state through the elevated life-event pass.
func TestHotLifeEventPassDeterministic(t *testing.T) {
	run := func() (*CitizensAPI, [32]byte) {
		api, err := NewCitizensAPI(2, "corr")
		if err != nil {
			t.Fatalf("NewCitizensAPI: %v", err)
		}
		mkBug270ElderlyCouple(t, api)
		if err := api.ApplyFidelityCommand(FidelityCommand{CorrelationID: "corr", CitizenID: 10, Target: FidelityHot}); err != nil {
			t.Fatalf("promote 10 to HOT: %v", err)
		}
		api.mu.Lock()
		api.month = 4000
		api.mu.Unlock()
		advanceTicks(t, api, DaysPerMonth)
		return api, api.PopulationHash("corr")
	}

	_, hashA := run()
	_, hashB := run()
	if hashA != hashB {
		t.Fatalf("PopulationHash differs across identical runs: %x vs %x", hashA, hashB)
	}
}
