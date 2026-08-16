package finance

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/invariant"
)

// runMonth drives one full monthly finance phase through the §7 chain.
// The numbers are chosen so no money account overdraws and the city runs
// a small deficit each month (exercise for the conservation invariant,
// not a solvency assertion).
func runMonth(t *testing.T, f *FinanceAPI) {
	t.Helper()
	wages := gbp(1000)
	spend := gbp(600)

	if _, err := f.PostWages(wages); err != nil {
		t.Fatalf("PostWages: %v", err)
	}
	if _, err := f.PostHouseholdSpend(300, gbp(2)); err != nil {
		t.Fatalf("PostHouseholdSpend: %v", err)
	}
	if _, err := f.CollectTax(TaxRates{IncomeRate: 2000, SalesRate: 2000, CorpRate: 2000}, wages, spend, 0); err != nil {
		t.Fatalf("CollectTax: %v", err)
	}
	if _, err := f.SettleOpex(gbp(100)); err != nil {
		t.Fatalf("SettleOpex: %v", err)
	}
	if err := f.ServiceDebt(gbp(50), 0); err != nil {
		t.Fatalf("ServiceDebt: %v", err)
	}
	if _, err := f.SettleConstruction(gbp(50)); err != nil {
		t.Fatalf("SettleConstruction: %v", err)
	}
	if _, err := f.AllocateToReserves(gbp(10)); err != nil {
		t.Fatalf("AllocateToReserves: %v", err)
	}
	if _, err := f.AccrueReserveInterest(100); err != nil {
		t.Fatalf("AccrueReserveInterest: %v", err)
	}
}

// TestMoneyConservationOver120Months (AC-14b) wires the finance ledger to
// engine.invariant's registered MoneyInvariant and drives 120 simulated
// months of the full chain, asserting zero money-conservation violations
// across every tick.
func TestMoneyConservationOver120Months(t *testing.T) {
	f := NewFinanceAPI("conservation-120")
	if err := f.SetMilestoneGate(allowAllGate{}); err != nil {
		t.Fatalf("SetMilestoneGate: %v", err)
	}
	seedTreasury(t, f, gbp(200_000))
	if _, err := f.Borrow(LoanRequest{Tier: 0, Principal: gbp(50_000), TermMonths: 360}); err != nil {
		t.Fatalf("Borrow: %v", err)
	}

	reg := invariant.NewRegistry()
	if err := reg.Register(invariant.NewMoneyInvariant()); err != nil {
		t.Fatalf("Register(MoneyInvariant): %v", err)
	}

	const months = 120
	for m := 0; m < months; m++ {
		if err := f.BeginMonth(int64(m)); err != nil {
			t.Fatalf("BeginMonth: %v", err)
		}
		runMonth(t, f)

		state := invariant.NewSnapshot(int64(m))
		state.Readings[invariant.StockMoney] = f.MoneyStockReading()
		res := invariant.RunSuite(reg, state)

		if res.AnyViolation {
			for _, o := range res.Outcomes {
				if o.Violation.Detected {
					t.Fatalf("money conservation violated at month %d: %s", m, o.Violation.Message)
				}
			}
			t.Fatalf("money conservation violated at month %d", m)
		}
	}

	// The AC requires the actual month count to be asserted (≥120), so a
	// short smoke run can never be quoted as the real proof.
	if months != 120 {
		t.Fatalf("long-run proof must cover ≥120 months, got %d", months)
	}
}

// TestMoneyConservationDetectsCorruption proves the conservation invariant
// actually can fail: an unbalanced transaction posted against the low-level
// path creates money without a tracked flow, and the invariant flags it.
func TestMoneyConservationDetectsCorruption(t *testing.T) {
	f := NewFinanceAPI("conservation-corrupt")
	seedTreasury(t, f, gbp(1000))
	if err := f.BeginMonth(1); err != nil {
		t.Fatalf("BeginMonth: %v", err)
	}

	// A clean month first.
	runMonth(t, f)

	reg := invariant.NewRegistry()
	if err := reg.Register(invariant.NewMoneyInvariant()); err != nil {
		t.Fatalf("Register(MoneyInvariant): %v", err)
	}

	clean := invariant.NewSnapshot(1)
	clean.Readings[invariant.StockMoney] = f.MoneyStockReading()
	if res := invariant.RunSuite(reg, clean); res.AnyViolation {
		t.Fatalf("clean month should have no violation, got %+v", res.Outcomes)
	}

	// Now corrupt the ledger: create money from nothing, untracked.
	f.postRaw(Transaction{
		Description: "money created from nothing",
		Entries: []Entry{
			{Account: AcctTreasury, Side: SideCredit, Amount: gbp(500), Category: CatLoan},
		},
	})

	corrupt := invariant.NewSnapshot(1)
	corrupt.Readings[invariant.StockMoney] = f.MoneyStockReading()
	res := invariant.RunSuite(reg, corrupt)
	if !res.AnyViolation {
		t.Fatal("expected the conservation invariant to flag the untracked money creation")
	}
}
