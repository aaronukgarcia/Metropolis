package attract

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/households"
)

// TestTermDrillThroughIsolation is AC-1: the seven §11 terms are
// independently queryable, and changing one term's input changes only that
// term's accessor output and the composite A(), never the other six
// accessors. The five pushed terms and the composite are asserted; the
// HousingAffordability term is exercised separately (it needs the
// households+finance wiring).
func TestTermDrillThroughIsolation(t *testing.T) {
	a, _, _, _ := newAPI(t, validConfig())

	base := TermInputs{
		JobAvailability:        10,
		ServiceCoverage:        20,
		Environment:            30,
		LeisureFit:             40,
		Safety:                 50,
		HouseholdIDs:           nil, // empty → housing affordability reads 100
		MonthlyRentMicroPounds: 0,
	}
	if err := a.SetTermInputs(base); err != nil {
		t.Fatalf("SetTermInputs: %v", err)
	}

	if got := a.JobAvailability(); got != 10 {
		t.Fatalf("JobAvailability = %v, want 10", got)
	}
	if got := a.ServiceCoverage(); got != 20 {
		t.Fatalf("ServiceCoverage = %v, want 20", got)
	}
	if got := a.Environment(); got != 30 {
		t.Fatalf("Environment = %v, want 30", got)
	}
	if got := a.LeisureFit(); got != 40 {
		t.Fatalf("LeisureFit = %v, want 40", got)
	}
	if got := a.Safety(); got != 50 {
		t.Fatalf("Safety = %v, want 50", got)
	}
	if got := a.Reputation(); got != 0 {
		t.Fatalf("Reputation = %v, want 0 before any month advances", got)
	}
	aff, err := a.HousingAffordability()
	if err != nil {
		t.Fatalf("HousingAffordability: %v", err)
	}
	if aff != 100 {
		t.Fatalf("HousingAffordability (empty city) = %v, want 100", aff)
	}
	a0, err := a.A()
	if err != nil {
		t.Fatalf("A: %v", err)
	}
	// 0.2*10 + 0.2*100 + 0.15*20 + 0.1*30 + 0.1*40 + 0.1*50 + 0.15*0 = 37
	if want := 37.0; a0 != want {
		t.Fatalf("A = %v, want %v", a0, want)
	}

	// Change exactly one pushed term's input.
	changed := base
	changed.JobAvailability = 90
	if err := a.SetTermInputs(changed); err != nil {
		t.Fatalf("SetTermInputs (changed): %v", err)
	}

	if got := a.JobAvailability(); got != 90 {
		t.Fatalf("JobAvailability after change = %v, want 90", got)
	}
	// The other six accessors are untouched.
	if got := a.ServiceCoverage(); got != 20 {
		t.Fatalf("ServiceCoverage changed by JobAvailability edit: %v", got)
	}
	if got := a.Environment(); got != 30 {
		t.Fatalf("Environment changed by JobAvailability edit: %v", got)
	}
	if got := a.LeisureFit(); got != 40 {
		t.Fatalf("LeisureFit changed by JobAvailability edit: %v", got)
	}
	if got := a.Safety(); got != 50 {
		t.Fatalf("Safety changed by JobAvailability edit: %v", got)
	}
	if got := a.Reputation(); got != 0 {
		t.Fatalf("Reputation changed by a term-input edit (reputation only advances on migration): %v", got)
	}
	if got, err := a.HousingAffordability(); err != nil || got != 100 {
		t.Fatalf("HousingAffordability changed by JobAvailability edit: %v, %v", got, err)
	}
	// The composite changed.
	a1, err := a.A()
	if err != nil {
		t.Fatalf("A (changed): %v", err)
	}
	if a1 == a0 {
		t.Fatalf("A did not change when a term input changed")
	}
}

// TestHousingAffordabilityNeedsWiring is AC-3's dependency half: the
// HousingAffordability term is genuinely computed from engine.households +
// engine.finance, so it errors before both are wired — never a silent
// zero or a constant.
func TestHousingAffordabilityNeedsWiring(t *testing.T) {
	a, err := New(validConfig(), 7, "corr-attract")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := a.HousingAffordability(); err == nil {
		t.Fatal("HousingAffordability before wiring should error, got nil")
	} else {
		isErr(t, err, ErrDependencyMissing)
	}
}

// TestHousingAffordabilityUsesHouseholdsAndFinance is AC-3's computation
// half: the term reflects real households state (an unhoused household
// drags affordability to 0) AND real finance income context (posting a wage
// bill that clears the rent-burden threshold restores it to 100). A
// constant or households-independent implementation fails both.
func TestHousingAffordabilityUsesHouseholdsAndFinance(t *testing.T) {
	a, ca, h, f := newAPI(t, validConfig())

	// One household (a partnered couple), preferred typology terrace,
	// stocked so it is NOT unhoused-by-preference.
	hid := partnerCouple(t, ca, mkResident(1, 60), mkResident(2, 60))
	// Stock both catalogue typologies so the household is not unhoused-by-
	// preference regardless of its (deterministic, tie-broken) preference.
	for _, typ := range []string{"terrace", "bungalow"} {
		if err := h.ReportStock(households.StockCommand{TypologyID: typ, Count: 10}); err != nil {
			t.Fatalf("ReportStock(%s): %v", typ, err)
		}
	}

	if err := a.SetTermInputs(TermInputs{
		JobAvailability:        80,
		ServiceCoverage:        80,
		Environment:            80,
		LeisureFit:             80,
		Safety:                 80,
		HouseholdIDs:           []uint64{hid},
		MonthlyRentMicroPounds: 1000,
	}); err != nil {
		t.Fatalf("SetTermInputs: %v", err)
	}

	// No wages posted → income 0 → rent-burden sentinel → Index 0.
	if got, err := a.HousingAffordability(); err != nil || got != 0 {
		t.Fatalf("HousingAffordability with zero income = %v, %v; want 0", got, err)
	}

	// Post a wage bill that clears the 35% rent-burden threshold:
	// income = 10000/1 household = 10000, rent 1000 → ratio 0.1 < 0.35.
	seedTreasury(t, f, 1_000_000)
	if _, err := f.PostWages(10000); err != nil {
		t.Fatalf("PostWages: %v", err)
	}
	if got, err := a.HousingAffordability(); err != nil || got != 100 {
		t.Fatalf("HousingAffordability after wage context = %v, %v; want 100", got, err)
	}
}
