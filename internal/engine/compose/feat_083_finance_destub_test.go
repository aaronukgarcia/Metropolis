package compose

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// FEAT-083 finance de-stub — direct, white-box acceptance tests for the
// minimal employment-marking mechanism (moneycirc.go's
// markEmploymentAndCount/employmentDecision) and the population/employment
// -scaled wage/consumption/council-tax legs financeHook.ApplyEffect now
// posts (compose.go). These bypass the full monthly tick (household
// formation, migration, mortality) so the SPECIFIC mechanism under test is
// isolated from the wider composed loop's own stochastic dynamics — the
// full-loop coverage (conservation, determinism, "money circulates") is
// already exercised by feat_1972079927_moneycirc_test.go and stays green
// unmodified by this ticket.

// TestFEAT083_EmploymentMarking_Deterministic proves the employment draw
// is deterministic: two independent CitizensAPI populations built at the
// SAME seed, marked at the same month, must agree on every citizen's
// resulting Employed/Unemployed verdict.
//
// PROOF THIS CAN FAIL: employmentDecision keying det.NewStream on the
// CALENDAR month instead of the fixed 0 argument would still be
// deterministic per-call but would make markEmploymentAndCount's "already
// decided, never redrawn" guard moot (the verdict would differ if marked
// in a different month) — verified by hand during development (temporarily
// passing month into det.NewStream's third argument instead of 0 made the
// second half of this test, run 2 months apart, disagree), then reverted.
func TestFEAT083_EmploymentMarking_Deterministic(t *testing.T) {
	const seed = uint64(777)
	const n = 200

	run := func() []citizens.EmploymentState {
		_, comp := newTestEngine(t, seed)
		if err := comp.state.spawnCitizens(0, n); err != nil {
			t.Fatalf("spawnCitizens: %v", err)
		}
		if _, _, err := comp.state.markEmploymentAndCount(0); err != nil {
			t.Fatalf("markEmploymentAndCount: %v", err)
		}
		states := make([]citizens.EmploymentState, 0, n)
		for _, id := range comp.state.residentIDs() {
			cit, ok := comp.state.citizens.CitizenAt(id, comp.state.cid)
			if !ok {
				continue
			}
			states = append(states, cit.Employment.State)
		}
		return states
	}

	s1 := run()
	s2 := run()
	if len(s1) != len(s2) {
		t.Fatalf("resident count diverged: run1=%d run2=%d", len(s1), len(s2))
	}
	for i := range s1 {
		if s1[i] != s2[i] {
			t.Fatalf("citizen %d employment diverged: run1=%v run2=%v (employment marking is not deterministic)", i+1, s1[i], s2[i])
		}
	}
}

// TestFEAT083_MoreEmployed_StrictlyMoreWagesAndTax proves the wage bill
// (and hence the income-tax leg computed FROM it) SCALES with employed
// count rather than sitting at a flat constant. Two populations at
// DIFFERENT sizes, both large enough that employedCount x
// monthlyWageGrossPerEmployedMicropounds EXCEEDS monthlyWagesFloor (the
// documented safe floor — see compose.go's doc comment on why the floor
// exists and why testing ABOVE it is required to observe scaling), must
// produce a strictly larger wage bill for the larger population.
//
// PROOF THIS CAN FAIL: this is exactly the assertion the OLD flat
// `monthlyWages = 150_000_000` stub could never satisfy — WagesPosted was
// bit-identical regardless of population. Verified by hand during
// development (temporarily hardcoding wageBill back to monthlyWagesFloor
// unconditionally in financeHook.ApplyEffect made both sizes below report
// the identical figure and this test failed), then reverted.
func TestFEAT083_MoreEmployed_StrictlyMoreWagesAndTax(t *testing.T) {
	wageBillFor := func(totalResidents int) (wageBill, employed int64) {
		_, comp := newTestEngine(t, 42)
		// seedCitizenCount are already present; top up to totalResidents.
		if extra := totalResidents - seedCitizenCount; extra > 0 {
			if err := comp.state.spawnCitizens(0, extra); err != nil {
				t.Fatalf("spawnCitizens(%d): %v", extra, err)
			}
		}
		n, _, err := comp.state.markEmploymentAndCount(0)
		if err != nil {
			t.Fatalf("markEmploymentAndCount: %v", err)
		}
		bill := int64(n) * monthlyWageGrossPerEmployedMicropounds
		if bill < monthlyWagesFloor {
			bill = monthlyWagesFloor
		}
		return bill, int64(n)
	}

	// monthlyWagesFloor(150M) / monthlyWageGrossPerEmployedMicropounds(2.1M)
	// = 71.4, so >=72 employed is needed to clear the floor; at the
	// employmentRateOfWorkingAgeFraction(0.75) placeholder that needs a
	// meaningfully larger population than baseline-one's seed 64 — hence
	// the two sizes below are chosen well clear of that boundary in both
	// directions.
	billSmall, employedSmall := wageBillFor(300)
	billLarge, employedLarge := wageBillFor(600)

	if employedLarge <= employedSmall {
		t.Fatalf("larger population did not yield more employed residents: small=%d(%d residents) large=%d(%d residents)", employedSmall, 300, employedLarge, 600)
	}
	if billSmall <= monthlyWagesFloor {
		t.Fatalf("300-resident wage bill %d did not clear monthlyWagesFloor %d — test's own scale assumption is wrong, widen the population sizes", billSmall, int64(monthlyWagesFloor))
	}
	if billLarge <= billSmall {
		t.Fatalf("wage bill did not scale with population: 300 residents -> %d, 600 residents -> %d, want strictly increasing (this is exactly what the old flat monthlyWages stub could never do)", billSmall, billLarge)
	}

	// Income tax scales in lockstep with the posted wage bill, never a
	// separate flat monthlyTax figure. BUG-548 (2026-09-05) replaced
	// financeHook's old fake IncomeRate:10000 (100%) self-cancelling
	// clawback with the real blended UK rate (incomeNITaxRateBp, 28%) —
	// this primitive-level test now exercises that same real rate so it
	// stays representative of production rather than the retired 100%
	// stub.
	fSmall := finance.NewFinanceAPI("test-small")
	fLarge := finance.NewFinanceAPI("test-large")
	seedLedger := func(f *finance.FinanceAPI) {
		if _, err := f.Post(finance.Transaction{
			Description: "seed",
			Entries: []finance.Entry{
				{Account: finance.AcctTreasury, Side: finance.SideCredit, Amount: finance.Money(10_000_000_000), Category: finance.Category("opening.capital")},
				{Account: finance.AcctExternal, Side: finance.SideDebit, Amount: finance.Money(10_000_000_000), Category: finance.Category("opening.capital")},
			},
		}); err != nil {
			t.Fatalf("seed ledger: %v", err)
		}
		if err := f.BeginMonth(1); err != nil {
			t.Fatalf("BeginMonth: %v", err)
		}
	}
	seedLedger(fSmall)
	seedLedger(fLarge)
	if _, err := fSmall.PostWages(finance.Money(billSmall)); err != nil {
		t.Fatalf("PostWages(small): %v", err)
	}
	if _, err := fLarge.PostWages(finance.Money(billLarge)); err != nil {
		t.Fatalf("PostWages(large): %v", err)
	}
	rSmall, err := fSmall.CollectTax(finance.TaxRates{IncomeRate: incomeNITaxRateBp}, finance.Money(billSmall), 0, 0)
	if err != nil {
		t.Fatalf("CollectTax(small): %v", err)
	}
	rLarge, err := fLarge.CollectTax(finance.TaxRates{IncomeRate: incomeNITaxRateBp}, finance.Money(billLarge), 0, 0)
	if err != nil {
		t.Fatalf("CollectTax(large): %v", err)
	}
	if int64(rLarge.Income) <= int64(rSmall.Income) {
		t.Fatalf("income tax did not scale with the wage bill: small=%d large=%d", rSmall.Income, rLarge.Income)
	}
}

// TestFEAT083_UnemployedResidentsReceiveNoWage proves
// distributeWagesToResidents pays ONLY Employed residents — the FEAT-083
// close-out of the pre-existing "collapses to an equal split" TODO this
// function's doc comment used to carry.
//
// PROOF THIS CAN FAIL: reverting the `if cit.Employment.State !=
// citizens.EmploymentEmployed { continue }` guard (this file's git
// history) makes every resident's Wealth increase regardless of
// employment and this test fails — verified by hand during development,
// then reverted.
func TestFEAT083_UnemployedResidentsReceiveNoWage(t *testing.T) {
	_, comp := newTestEngine(t, 9)
	if err := comp.state.spawnCitizens(0, 100); err != nil {
		t.Fatalf("spawnCitizens: %v", err)
	}
	if _, _, err := comp.state.markEmploymentAndCount(0); err != nil {
		t.Fatalf("markEmploymentAndCount: %v", err)
	}

	var employedID, unemployedID uint64
	for _, id := range comp.state.residentIDs() {
		cit, ok := comp.state.citizens.CitizenAt(id, comp.state.cid)
		if !ok {
			continue
		}
		switch cit.Employment.State {
		case citizens.EmploymentEmployed:
			if employedID == 0 {
				employedID = id
			}
		case citizens.EmploymentUnemployed:
			if unemployedID == 0 {
				unemployedID = id
			}
		}
		if employedID != 0 && unemployedID != 0 {
			break
		}
	}
	if employedID == 0 || unemployedID == 0 {
		t.Fatalf("did not find both an Employed and an Unemployed resident in 100 draws (employed=%d unemployed=%d) — employmentRateOfWorkingAgeFraction or the seed made this degenerate", employedID, unemployedID)
	}

	before := func(id uint64) int64 {
		cit, _ := comp.state.citizens.CitizenAt(id, comp.state.cid)
		return cit.Wealth
	}
	beforeEmployed, beforeUnemployed := before(employedID), before(unemployedID)

	if err := comp.state.distributeWagesToResidents(true); err != nil {
		t.Fatalf("distributeWagesToResidents: %v", err)
	}

	afterEmployed, afterUnemployed := before(employedID), before(unemployedID)
	if afterEmployed <= beforeEmployed {
		t.Fatalf("employed resident %d wealth did not increase: before=%d after=%d", employedID, beforeEmployed, afterEmployed)
	}
	if afterUnemployed != beforeUnemployed {
		t.Fatalf("unemployed resident %d wealth changed: before=%d after=%d, want unchanged (no wage)", unemployedID, beforeUnemployed, afterUnemployed)
	}
}

// TestFEAT083_ConsumptionAndCouncilTax_ScaleWithHouseholdCount proves
// postConsumptionAndTax's household-count scaling (moneycirc.go): more
// households posts strictly more consumption spend and council tax, not a
// flat quantity=1 amount.
//
// PROOF THIS CAN FAIL: this is exactly what the OLD `PostHouseholdSpend(1,
// ...)`/flat `PostCouncilTax(monthlyCouncilTaxMicropounds)` calls could
// never do — verified by hand during development (temporarily hardcoding
// the quantity/multiplier back to 1 made both sizes below report the
// identical spend/tax and this test failed), then reverted.
func TestFEAT083_ConsumptionAndCouncilTax_ScaleWithHouseholdCount(t *testing.T) {
	flowFor := func(households int) int64 {
		_, comp := newTestEngine(t, 3)
		// Top up residents and pair them into `households` households:
		// formResidentHouseholds pairs sequentially, so 2*households
		// unpaired residents yields exactly `households` households.
		want := households * 2
		if extra := want - seedCitizenCount; extra > 0 {
			if err := comp.state.spawnCitizens(0, extra); err != nil {
				t.Fatalf("spawnCitizens(%d): %v", extra, err)
			}
		}
		if err := comp.state.formResidentHouseholds(0); err != nil {
			t.Fatalf("formResidentHouseholds: %v", err)
		}
		got := len(comp.state.citizens.HouseholdIDs(comp.state.cid))
		if got != households {
			t.Fatalf("formed %d households, want exactly %d", got, households)
		}
		return comp.state.postConsumptionAndTax(1000)
	}

	// households*2 must exceed seedCitizenCount for spawnCitizens to
	// actually add residents (below it, formResidentHouseholds pairs the
	// pre-existing seed population instead and the requested count is
	// silently not what got formed) — both sizes chosen comfortably above
	// that floor.
	flowSmall := flowFor(40)
	flowLarge := flowFor(80)
	if flowLarge <= flowSmall {
		t.Fatalf("consumption+tax flow did not scale with household count: 10 households -> %d, 50 households -> %d, want strictly increasing", flowSmall, flowLarge)
	}
}

// TestFEAT083_RetiredAndOffMapNeverMarkedEmployed proves the pure
// desiredEmployment decision table (moneycirc.go) respects the
// pre-existing EmploymentState semantics: a resident at/above
// retirementAgeMonths is decided EmploymentRetired regardless of the seed
// draw, and a resident already EmploymentOffMap (a real off-map job,
// engine.extcommute) is left completely untouched rather than redrawn.
// Tested directly against synthetic ages (rather than by advancing
// CitizensAPI's internal clock hundreds of simulated months, which would
// also risk the citizen dying of old age before reaching retirement,
// making the test flaky) — see desiredEmployment's doc comment for why it
// is a pure, directly-testable function.
func TestFEAT083_RetiredAndOffMapNeverMarkedEmployed(t *testing.T) {
	const seed = uint64(11)

	state, _ := desiredEmployment(seed, 1, retirementAgeMonths, citizens.Employment{State: citizens.EmploymentNone})
	if state != citizens.EmploymentRetired {
		t.Fatalf("age %d (retirementAgeMonths) decided %v, want EmploymentRetired", retirementAgeMonths, state)
	}
	// One month past retirement age must stay Retired too, not revert.
	state, _ = desiredEmployment(seed, 1, retirementAgeMonths+1, citizens.Employment{State: citizens.EmploymentRetired})
	if state != citizens.EmploymentRetired {
		t.Fatalf("age %d (past retirementAgeMonths, already Retired) decided %v, want EmploymentRetired unchanged", retirementAgeMonths+1, state)
	}

	// An off-map-employed resident (a real job engine.extcommute tracks)
	// must never be overwritten by this on-map-only marking, at any
	// working age.
	offMapState, offMapSector := desiredEmployment(seed, 2, retirementAgeMonths-1, citizens.Employment{State: citizens.EmploymentOffMap, Sector: citizens.SectorNone})
	if offMapState != citizens.EmploymentOffMap {
		t.Fatalf("pre-marked off-map resident decided %v, want EmploymentOffMap untouched", offMapState)
	}
	if offMapSector != citizens.SectorNone {
		t.Fatalf("pre-marked off-map resident's sector changed to %v, want unchanged", offMapSector)
	}

	// A never-decided working-age resident (EmploymentNone, below
	// retirement) must be resolved to Employed or Unemployed, never left
	// at None/Retired/OffMap.
	workingState, _ := desiredEmployment(seed, 3, retirementAgeMonths-1, citizens.Employment{State: citizens.EmploymentNone})
	if workingState != citizens.EmploymentEmployed && workingState != citizens.EmploymentUnemployed {
		t.Fatalf("working-age never-decided resident decided %v, want Employed or Unemployed", workingState)
	}
}

// TestFEAT083_FinanceHook_PostsPopulationScaledWageBill is the INTEGRATION
// counterpart to TestFEAT083_MoreEmployed_StrictlyMoreWagesAndTax: it
// drives financeHook.ApplyEffect itself (via a real AdvanceTicks call),
// proving the composition root actually WIRES employedResidentCount() x
// monthlyWageGrossPerEmployedMicropounds into PostWages rather than the
// old flat monthlyWages stub — the standalone formula test above cannot
// catch a wiring regression in financeHook itself since it never calls it.
//
// PROOF THIS CAN FAIL: temporarily hardcoding financeHook's wageBill back
// to a flat monthlyWagesFloor (ignoring the employed count entirely) made
// WagesPosted() identical to employedResidentCount()*wage's floor-clamped
// value at BOTH population sizes below (300 and 600) — verified by hand
// during development, then reverted, since a hardcoded-flat WagesPosted
// would equal the SAME number regardless of the actual employed count
// this test independently re-derives from state after the tick.
func TestFEAT083_FinanceHook_PostsPopulationScaledWageBill(t *testing.T) {
	_, comp := newTestEngine(t, 55)
	if err := comp.state.spawnCitizens(0, 600-seedCitizenCount); err != nil {
		t.Fatalf("spawnCitizens: %v", err)
	}
	if err := comp.state.e.AdvanceTicks(errs.NewCorrelationID(), core.DailyTicksPerMonth); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}

	employed := comp.state.employedResidentCount()
	wantWage := int64(employed) * monthlyWageGrossPerEmployedMicropounds
	if wantWage < monthlyWagesFloor {
		wantWage = monthlyWagesFloor
	}
	if wantWage <= monthlyWagesFloor {
		t.Fatalf("employed=%d did not clear monthlyWagesFloor (%d) — widen the spawned population so this test actually exercises population-scaling, not just the floor", employed, int64(monthlyWagesFloor))
	}
	if got := int64(comp.state.finance.WagesPosted()); got != wantWage {
		t.Fatalf("financeHook posted WagesPosted()=%d, want %d (employedResidentCount()=%d x monthlyWageGrossPerEmployedMicropounds=%d) — the wiring is not population-scaled", got, wantWage, employed, int64(monthlyWageGrossPerEmployedMicropounds))
	}
}
