package attract

import (
	"math"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/households"
)

// TestTermDrillThroughIsolation is AC-1: the lever and modifier lines are
// independently readable, and changing one lever's input changes only that
// lever's points and the composite A(), never another lever's line, the
// reputation, or the housing value. A (housing built at 100, empty city),
// jobs 10, services 20, parks 40, safety 50, environment 30 at A_world 50:
//
//	50 + 0.1*(100-50) + 0.1*(10-50) + 0.1*(20-50) + 0.1*(40-50)
//	   + 0.1*(50-50) + 0.1*(30-50) + 0.15*0 = 50 + 5 - 4 - 3 - 1 + 0 - 2 = 45
func TestTermDrillThroughIsolation(t *testing.T) {
	a, _, _, _ := newAPI(t, validConfig())

	var base TermInputs
	base.Levers[LeverHousing] = LeverTerm{Built: true} // value computed by attract
	base.Levers[LeverJobs] = LeverTerm{Value: 10, Built: true}
	base.Levers[LeverServices] = LeverTerm{Value: 20, Built: true}
	base.Levers[LeverParks] = LeverTerm{Value: 40, Built: true}
	base.Modifiers[ModifierSafety] = LeverTerm{Value: 50, Built: true}
	base.Modifiers[ModifierEnvironment] = LeverTerm{Value: 30, Built: true}
	if err := a.SetTermInputs(base); err != nil {
		t.Fatalf("SetTermInputs: %v", err)
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
	if want := 45.0; math.Abs(a0-want) > 1e-9 {
		t.Fatalf("A = %v, want %v", a0, want)
	}

	// Change exactly one lever's input (jobs 10 -> 90): +8 points.
	changed := base
	changed.Levers[LeverJobs].Value = 90
	if err := a.SetTermInputs(changed); err != nil {
		t.Fatalf("SetTermInputs (changed): %v", err)
	}
	if got := a.Reputation(); got != 0 {
		t.Fatalf("Reputation changed by a term-input edit (reputation only advances on migration): %v", got)
	}
	if got, err := a.HousingAffordability(); err != nil || got != 100 {
		t.Fatalf("HousingAffordability changed by a jobs edit: %v, %v", got, err)
	}
	a1, err := a.A()
	if err != nil {
		t.Fatalf("A (changed): %v", err)
	}
	if want := a0 + 8; math.Abs(a1-want) > 1e-9 {
		t.Fatalf("A after jobs 10->90 = %v, want %v (only the jobs line moves, by 0.1 x 80)", a1, want)
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

	if err := a.SetTermInputs(uniformTI(80, []uint64{hid}, 1000)); err != nil {
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
