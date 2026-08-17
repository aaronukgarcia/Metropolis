package social

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/wellbeing"
)

// TestCategoryIsolation (AC-2): raising exactly one driver — unemployment
// duration — measurably increases the category §40 couples it to
// (homelessness) while leaving a category §40 does NOT tie to it (disability
// & carers) materially unchanged. This is the load-bearing half of "decomposed
// caseload": the five categories are five demand signals, not one blended
// score split into five labels.
func TestCategoryIsolation(t *testing.T) {
	a, err := New(testConfig(), 1, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	base := mustGenerateCaseload(t, a, 0, DriverInputs{Deprivation: 0.5, UnemploymentMonths: 0, NightlifeDensity: 0.5})
	raised := mustGenerateCaseload(t, a, 0, DriverInputs{Deprivation: 0.5, UnemploymentMonths: 30, NightlifeDensity: 0.5})

	if got := count(raised, CategoryHomelessness); got <= count(base, CategoryHomelessness) {
		t.Fatalf("unemployment duration must increase homelessness caseload: base=%d raised=%d",
			count(base, CategoryHomelessness), got)
	}
	if got := count(raised, CategoryDisabilityCarers); got != count(base, CategoryDisabilityCarers) {
		t.Fatalf("disability & carers must NOT respond to unemployment duration (AC-2 isolation): base=%d raised=%d",
			count(base, CategoryDisabilityCarers), got)
	}
}

// TestFamilyStressInputConsumesWellbeingDrivers (AC-3): the family-stress
// caseload input is sourced from engine.wellbeing's registered Crowding and
// FinancialStress drivers via the registered seam — crossing wellbeing's 35%
// rent-burden threshold changes this module's family-stress input without
// social independently re-deriving rent burden from raw rent/income figures.
func TestFamilyStressInputConsumesWellbeingDrivers(t *testing.T) {
	wb, err := wellbeing.New(testWellbeingFile(0.35, 10, 10), 1, "test")
	if err != nil {
		t.Fatalf("wellbeing.New: %v", err)
	}
	a, err := New(testConfig(), 1, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := a.SetFamilyStressSource(WellbeingFamilyStress{Wellbeing: wb}); err != nil {
		t.Fatalf("SetFamilyStressSource: %v", err)
	}

	below, err := a.FamilyStressInput(FamilyStressQuery{CitizenID: 1, Month: 0, RentBurden: 0.30})
	if err != nil {
		t.Fatalf("FamilyStressInput below threshold: %v", err)
	}
	above, err := a.FamilyStressInput(FamilyStressQuery{CitizenID: 1, Month: 0, RentBurden: 0.40})
	if err != nil {
		t.Fatalf("FamilyStressInput above threshold: %v", err)
	}

	if below.FinancialStress != 0 {
		t.Fatalf("rent burden below 35%% must yield zero financial stress, got %v", below.FinancialStress)
	}
	if above.FinancialStress == 0 {
		t.Fatal("rent burden at/above 35% must yield non-zero financial stress")
	}

	// And the decomposed generator must move family-support caseload with the
	// consumed driver, closing the AC-3 loop from driver value to caseload.
	lo := mustGenerateCaseload(t, a, 0, DriverInputs{FinancialStress: below.FinancialStress})
	hi := mustGenerateCaseload(t, a, 0, DriverInputs{FinancialStress: above.FinancialStress})
	if count(hi, CategoryFamilySupport) <= count(lo, CategoryFamilySupport) {
		t.Fatal("financial stress driver must increase family-support caseload")
	}
}

// TestAddictionCaseloadRisesWithDeprivation (AC-4, demand side): raising
// deprivation raises the addiction-services caseload via the
// nightlife/deprivation coupling.
func TestAddictionCaseloadRisesWithDeprivation(t *testing.T) {
	a, err := New(testConfig(), 1, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	lo := mustGenerateCaseload(t, a, 0, DriverInputs{Deprivation: 0.2, NightlifeDensity: 0.8})
	hi := mustGenerateCaseload(t, a, 0, DriverInputs{Deprivation: 0.8, NightlifeDensity: 0.8})
	if count(hi, CategoryAddiction) <= count(lo, CategoryAddiction) {
		t.Fatalf("raising deprivation must raise addiction caseload: lo=%d hi=%d",
			count(lo, CategoryAddiction), count(hi, CategoryAddiction))
	}
}

// TestCrisisEventIsTraceable (AC-5): a discrete domestic-crisis event opens
// an immediate family-support/child-protection caseload spike whose entries
// are individually traceable to that event — not folded into the anonymous
// monthly aggregate.
func TestCrisisEventIsTraceable(t *testing.T) {
	a, err := New(testConfig(), 1, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	id, err := a.InjectCrisis(CrisisEvent{ID: "crisis-1", Month: 3})
	if err != nil {
		t.Fatalf("InjectCrisis: %v", err)
	}
	c, err := a.Case(id)
	if err != nil {
		t.Fatalf("Case: %v", err)
	}
	if c.CrisisID != "crisis-1" {
		t.Fatalf("case must be traceable to the crisis event: CrisisID=%q", c.CrisisID)
	}
	if c.Category != CategoryFamilySupport {
		t.Fatalf("crisis case must be family-support, got %s", c.Category)
	}
	if c.OpenedMonth != 3 {
		t.Fatalf("crisis case must open at the event month, got %d", c.OpenedMonth)
	}
}

// TestDeterministicCaseloadAndPlacement (AC-15/GR#21): two identically-seeded
// APIs driven through the same command sequence produce byte-identical
// accounting, across categories and months — no map-iteration order leaks
// into allocation or accounting.
func TestDeterministicCaseloadAndPlacement(t *testing.T) {
	run := func() map[Category]AccountingSnapshot {
		a, err := New(testConfig(), 7, "test")
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		_ = a.AdvanceMonth(1, DriverInputs{Deprivation: 0.6, UnemploymentMonths: 20, CrowdingStress: 2, FinancialStress: 3, NightlifeDensity: 0.7})
		_ = a.AdvanceMonth(2, DriverInputs{Deprivation: 0.6, UnemploymentMonths: 20, CrowdingStress: 2, FinancialStress: 3, NightlifeDensity: 0.7})
		_, _ = a.InjectCrisis(CrisisEvent{ID: "c1", Month: 2})
		_ = a.RouteHomelessness(2)
		out := make(map[Category]AccountingSnapshot)
		for _, cat := range categoryOrder {
			for _, m := range []int64{1, 2} {
				s, err := a.Accounting(cat, m)
				if err != nil {
					t.Fatalf("Accounting(%s,%d): %v", cat, m, err)
				}
				out[cat] = s
			}
		}
		return out
	}
	first := run()
	second := run()
	for cat, s1 := range first {
		if s2, ok := second[cat]; !ok || s1 != s2 {
			t.Fatalf("non-deterministic accounting for %s: %+v vs %+v", cat, s1, second[cat])
		}
	}
}
