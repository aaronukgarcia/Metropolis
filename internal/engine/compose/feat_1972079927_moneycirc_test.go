package compose

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// FEAT-1972079927 "money circulation inc1" — composition-root acceptance
// tests, per Aaron's 2026-08-31 rulings: money circulates
// (treasury/households/firms all change), TotalMoneyInCirculation is
// conserved, HousingAffordability is no longer pinned at 100 once
// households exist, migrant wealth varies (see engine/attract's own
// feat_1972079927_migrantwealth_test.go), and the whole loop is
// deterministic.

// TestFEAT1972079927_Q1_SeedResidentGetsRealHousehold proves Q1's monthly
// household formation actually reaches the SEED population (not just
// admitted migrants, who already got a real household via
// attract.applyImmigration's own pre-existing LifeEventPartner call):
// resident citizen id 1 (part of the seedCitizenCount=64 population, never
// touched by immigration) must carry a non-zero Household after the first
// month's formResidentHouseholds pairing pass.
//
// PROOF THIS CAN FAIL: temporarily short-circuiting formResidentHouseholds
// to `return nil` before it runs makes this fail — verified by hand during
// development (household stayed 0), then reverted.
func TestFEAT1972079927_Q1_SeedResidentGetsRealHousehold(t *testing.T) {
	e, comp := newTestEngine(t, 42)
	if err := e.AdvanceTicks(errs.NewCorrelationID(), core.DailyTicksPerMonth); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}
	cit, ok := comp.state.citizens.CitizenAt(1, comp.state.cid)
	if !ok {
		t.Fatalf("CitizenAt(1): not found")
	}
	if cit.Household == 0 {
		t.Fatal("seed resident 1 has no household after month 1 — formResidentHouseholds did not run")
	}
}

// TestFEAT1972079927_MoneyCirculates_AllThreePotsChange proves Q4's
// consumption-spend wiring actually moves money through all three
// RoleMoney pots (treasury, households, firms) — not just the pre-existing
// treasury<->households wage/tax pair. Before this increment, AcctFirms
// never moved at all (PostHouseholdSpend/CollectTax's sales+corp legs were
// never called from compose).
//
// PROOF THIS CAN FAIL: temporarily deleting postConsumptionAndTax's call
// from financeHook.ApplyEffect pins AcctFirms at exactly 0 for the whole
// run and this test's firms-changed assertion fails — verified by hand
// during development, then reverted.
func TestFEAT1972079927_MoneyCirculates_AllThreePotsChange(t *testing.T) {
	e, comp := newTestEngine(t, 42)
	openingTreasury := ledgerBalance(comp.state.finance, finance.AcctTreasury)
	openingHouseholds := ledgerBalance(comp.state.finance, finance.AcctHouseholds)
	openingFirms := ledgerBalance(comp.state.finance, finance.AcctFirms)

	if err := e.AdvanceTicks(errs.NewCorrelationID(), testTicks); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}

	closingTreasury := ledgerBalance(comp.state.finance, finance.AcctTreasury)
	closingHouseholds := ledgerBalance(comp.state.finance, finance.AcctHouseholds)
	closingFirms := ledgerBalance(comp.state.finance, finance.AcctFirms)

	if closingTreasury == openingTreasury {
		t.Fatalf("AcctTreasury unchanged after %d months: %d", testMonths, closingTreasury)
	}
	if closingHouseholds == openingHouseholds {
		t.Fatalf("AcctHouseholds unchanged after %d months: %d", testMonths, closingHouseholds)
	}
	if closingFirms == openingFirms {
		t.Fatalf("AcctFirms unchanged after %d months: %d (Q4 consumption spend never reached firms)", testMonths, closingFirms)
	}
	if closingFirms <= openingFirms {
		t.Fatalf("AcctFirms did not GROW (opening %d -> closing %d) — household spend never credited firms", openingFirms, closingFirms)
	}
}

// TestFEAT1972079927_TotalMoneyConserved proves AC-10/the brief's Q6 "money
// circulates but the conserved total never leaks": TotalMoneyInCirculation
// (which sums every RoleMoney account — treasury, households, firms,
// reserves) is IDENTICAL at the start and end of a 12-month run despite
// the new legs moving money between all three pots, checked at every
// month boundary (not just start/end) so a leak that nets to zero over 12
// months but is non-zero mid-run cannot hide.
func TestFEAT1972079927_TotalMoneyConserved(t *testing.T) {
	e, comp := newTestEngine(t, 42)
	want := comp.state.finance.TotalMoneyInCirculation()
	if want <= 0 {
		t.Fatalf("opening TotalMoneyInCirculation = %d, want > 0", want)
	}
	for m := int64(1); m <= testMonths; m++ {
		if err := e.AdvanceTicks(errs.NewCorrelationID(), core.DailyTicksPerMonth); err != nil {
			t.Fatalf("AdvanceTicks(month %d): %v", m, err)
		}
		if got := comp.state.finance.TotalMoneyInCirculation(); got != want {
			t.Fatalf("month %d: TotalMoneyInCirculation = %d, want conserved %d", m, got, want)
		}
		// AC-10b: the ledger's own from-scratch recomputation must agree
		// with the maintained running total — catches a running-total
		// drift a pure before/after check could miss.
		if recomputed := comp.state.finance.RecomputeMoneyStock(); recomputed != want {
			t.Fatalf("month %d: RecomputeMoneyStock = %d, want %d (running total drifted from the ledger)", m, recomputed, want)
		}
	}
}

// TestFEAT1972079927_AffordabilityUnpinnedFrom100 proves Q1+Q2 together:
// once households form from residents, HousingAffordability must move away
// from the old permanent 100 (households.affordabilityIndex's own
// "total==0 -> 100" branch, which is exactly what a vacant/no-households
// city reads forever). Real households existing and being queried for
// overcrowding/rent-burden/unhoused-by-preference is what makes the term
// move at all.
//
// PROOF THIS CAN FAIL: temporarily reverting applyMigration's HouseholdIDs
// field back to nil (the pre-FEAT-1972079927 stub) pins this at 100 for
// every month and this test fails — verified by hand during development
// (see this file's git history), then reverted.
func TestFEAT1972079927_AffordabilityUnpinnedFrom100(t *testing.T) {
	e, comp := newTestEngine(t, 42)
	sawHouseholds := false
	sawNot100 := false
	for m := int64(1); m <= testMonths; m++ {
		if err := e.AdvanceTicks(errs.NewCorrelationID(), core.DailyTicksPerMonth); err != nil {
			t.Fatalf("AdvanceTicks(month %d): %v", m, err)
		}
		ids := comp.state.citizens.HouseholdIDs(comp.state.cid)
		if len(ids) > 0 {
			sawHouseholds = true
		}
		// housingAffordability is captured INSIDE applyMigration, in the
		// same instant SetTermInputs freshly snapshotted this month's
		// household-id set (see simState's doc comment) — unlike a later
		// call to AttractAPI.HousingAffordability(), which re-queries
		// against that snapshot and can hit a stale-household error if
		// this SAME month's emigration dissolves a household moments
		// after it formed.
		if len(ids) > 0 && comp.state.housingAffordability != 100 {
			sawNot100 = true
		}
	}
	if !sawHouseholds {
		t.Fatal("no households ever formed over 12 months — Q1 wiring did not run")
	}
	if !sawNot100 {
		t.Fatal("HousingAffordability stayed pinned at 100 every month households existed — Q2 wiring did not move the term")
	}
}

// TestFEAT1972079927_Determinism proves the whole money-circulation loop
// (household formation, consumption spend/tax, per-citizen wage accrual,
// migrant wealth) is byte-reproducible: two independent 12-month runs at
// the same world seed must agree exactly on population, the citizens
// population hash (covers every citizen's Wealth/Household/Employment
// field), treasury, household wealth, and cumulative money flow.
func TestFEAT1972079927_Determinism(t *testing.T) {
	run := func() (population int, hash [32]byte, treasury, citizenWealth, moneyFlows int64) {
		e, comp := newTestEngine(t, 123)
		if err := e.AdvanceTicks(errs.NewCorrelationID(), testTicks); err != nil {
			t.Fatalf("AdvanceTicks: %v", err)
		}
		return comp.Population(), comp.state.citizens.PopulationHash(comp.state.cid), comp.Treasury(), comp.CitizenWealth(), comp.MoneyFlows()
	}

	pop1, hash1, tr1, hh1, flows1 := run()
	pop2, hash2, tr2, hh2, flows2 := run()

	if pop1 != pop2 {
		t.Fatalf("population diverged: run1=%d run2=%d", pop1, pop2)
	}
	if hash1 != hash2 {
		t.Fatalf("PopulationHash diverged: run1=%x run2=%x (covers per-citizen Wealth — the bell-curve migrant draw and monthly wage accrual must be deterministic)", hash1, hash2)
	}
	if tr1 != tr2 {
		t.Fatalf("Treasury diverged: run1=%d run2=%d", tr1, tr2)
	}
	if hh1 != hh2 {
		t.Fatalf("CitizenWealth (ledger) diverged: run1=%d run2=%d", hh1, hh2)
	}
	if flows1 != flows2 {
		t.Fatalf("MoneyFlows diverged: run1=%d run2=%d", flows1, flows2)
	}
	if flows1 <= 0 {
		t.Fatalf("MoneyFlows = %d, want > 0 (money should have moved over %d months)", flows1, testMonths)
	}
}
