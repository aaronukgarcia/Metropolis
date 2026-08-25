package finance

import (
	"testing"
)

// FEAT-233 (FEAT-1972079848) — the FlowMatrix query seam's acceptance
// tests: month-close money in/out per ASM-1220 (budget inflow vs EXTERNAL
// outflow only; opex/wages/construction are internal redistribution and
// must never appear), fixed row order (GR#21), saturating totals, and
// drill-through agreement with LinesByCategory over the same window.

// flowMatrixTestMonth seeds a treasury, then runs one BeginMonth window
// of mixed postings: all three tax categories, an import, debt service,
// AND internal redistribution (wages + spend + opex + construction) that
// ASM-1220 says must NOT appear in the matrix.
func flowMatrixTestMonth(t *testing.T, f *FinanceAPI) FlowMatrix {
	t.Helper()
	seedTreasury(t, f, gbp(1000))
	if err := f.BeginMonth(7); err != nil {
		t.Fatalf("BeginMonth: %v", err)
	}

	// Fund the payer accounts first (wages -> households; household spend
	// -> firms) so every CollectTax leg can clear.
	if _, err := f.PostWages(gbp(100)); err != nil {
		t.Fatalf("PostWages: %v", err)
	}
	if _, err := f.PostHouseholdSpend(1, gbp(50)); err != nil {
		t.Fatalf("PostHouseholdSpend: %v", err)
	}

	receipts, err := f.CollectTax(TaxRates{IncomeRate: 2000, SalesRate: 1000, CorpRate: 5000}, gbp(100), gbp(50), gbp(40))
	if err != nil {
		t.Fatalf("CollectTax: %v", err)
	}
	if receipts.Total() != receipts.Income+receipts.Sales+receipts.Corp {
		t.Fatalf("TaxReceipts.Total() = %d, want %d", receipts.Total(), receipts.Income+receipts.Sales+receipts.Corp)
	}

	if _, err := f.SettleImports(gbp(10)); err != nil {
		t.Fatalf("SettleImports: %v", err)
	}
	if err := f.ServiceDebt(gbp(3), 0); err != nil {
		t.Fatalf("ServiceDebt: %v", err)
	}
	// More internal redistribution — present in the ledger window, absent
	// from the matrix by ASM-1220.
	if _, err := f.SettleOpex(gbp(15)); err != nil {
		t.Fatalf("SettleOpex: %v", err)
	}
	if _, err := f.SettleConstruction(gbp(25)); err != nil {
		t.Fatalf("SettleConstruction: %v", err)
	}
	return f.FlowMatrix()
}

func TestFlowMatrix_DecomposesASM1220Flows(t *testing.T) {
	f := NewFinanceAPI("feat233")
	m := flowMatrixTestMonth(t, f)

	if m.Month != 7 {
		t.Fatalf("Month = %d, want 7", m.Month)
	}

	// Inflows: exactly the three tax categories, in taxCategories order,
	// each equal to its LinesByCategory drill-through over the same window.
	if len(m.Inflows) != len(taxCategories) {
		t.Fatalf("Inflows has %d rows, want %d (one per tax category)", len(m.Inflows), len(taxCategories))
	}
	var drillTotal Money
	for i, cat := range taxCategories {
		row := m.Inflows[i]
		if row.Category != cat {
			t.Fatalf("Inflows[%d].Category = %q, want %q (fixed GR#21 order)", i, row.Category, cat)
		}
		var drill Money
		for _, e := range f.LinesByCategory(cat) {
			if e.Account == AcctTreasury && e.Side == SideCredit {
				drill += e.Amount
			}
		}
		if row.Amount != drill {
			t.Fatalf("Inflows[%d] (%s) = %d, drill-through sum = %d (AC-11)", i, cat, row.Amount, drill)
		}
		drillTotal += drill
	}
	if m.TotalIn != drillTotal {
		t.Fatalf("TotalIn = %d, want %d (sum of rows)", m.TotalIn, drillTotal)
	}
	// Cross-check against the package's own tax aggregate over the same
	// window: money-in per ASM-1220 IS the budget's tax inflow.
	if m.TotalIn != f.TaxRevenue() {
		t.Fatalf("TotalIn = %d, FinanceAPI.TaxRevenue() = %d — the matrix and the ledger aggregate disagree", m.TotalIn, f.TaxRevenue())
	}

	// External outflows: exactly imports then debt interest, in
	// externalOutflowCategories order.
	if len(m.ExternalOutflows) != 2 {
		t.Fatalf("ExternalOutflows has %d rows, want 2", len(m.ExternalOutflows))
	}
	if m.ExternalOutflows[0].Category != CatImports || m.ExternalOutflows[0].Amount != gbp(10) {
		t.Fatalf("ExternalOutflows[0] = %+v, want imports %d", m.ExternalOutflows[0], gbp(10))
	}
	if m.ExternalOutflows[1].Category != CatDebtService || m.ExternalOutflows[1].Amount != gbp(3) {
		t.Fatalf("ExternalOutflows[1] = %+v, want debt.service %d", m.ExternalOutflows[1], gbp(3))
	}
	if m.TotalOut != gbp(13) {
		t.Fatalf("TotalOut = %d, want %d", m.TotalOut, gbp(13))
	}
}

func TestFlowMatrix_ExcludesInternalRedistribution(t *testing.T) {
	f := NewFinanceAPI("feat233")
	m := flowMatrixTestMonth(t, f)

	// Wages/spend/opex/construction all posted this window; none of those
	// categories may appear anywhere in the matrix (ASM-1220). TotalOut
	// is imports+interest ONLY even though the treasury debited far more.
	for _, row := range append(append([]FlowLine{}, m.Inflows...), m.ExternalOutflows...) {
		switch row.Category {
		case CatWages, CatSpend, CatOpex, CatConstruction:
			t.Fatalf("internal redistribution category %q leaked into the FlowMatrix (ASM-1220)", row.Category)
		}
	}
	if m.TotalOut != gbp(13) {
		t.Fatalf("TotalOut = %d, want %d — an internal outflow (opex/wages/construction) was counted as external (ASM-1220)", m.TotalOut, gbp(13))
	}
}

func TestFlowMatrix_MonthCloseWindowResets(t *testing.T) {
	f := NewFinanceAPI("feat233")
	warmed := flowMatrixTestMonth(t, f)
	if warmed.TotalIn == 0 || warmed.TotalOut == 0 {
		t.Fatalf("seeded window read in=%d out=%d, want non-zero before the reset check", warmed.TotalIn, warmed.TotalOut)
	}

	// A new BeginMonth opens a fresh close window: the matrix must read
	// zero until new lines post (the compose financeHook's monthly tick
	// contract).
	if err := f.BeginMonth(8); err != nil {
		t.Fatalf("BeginMonth(8): %v", err)
	}
	m2 := f.FlowMatrix()
	if m2.Month != 8 || m2.TotalIn != 0 || m2.TotalOut != 0 {
		t.Fatalf("after BeginMonth(8): month=%d in=%d out=%d, want a clean empty window", m2.Month, m2.TotalIn, m2.TotalOut)
	}
}

func TestFlowMatrix_EmptyWindowIsAllZeroRows(t *testing.T) {
	f := NewFinanceAPI("feat233")
	seedTreasury(t, f, gbp(5))
	if err := f.BeginMonth(1); err != nil {
		t.Fatalf("BeginMonth: %v", err)
	}
	m := f.FlowMatrix()
	if len(m.Inflows) != len(taxCategories) || len(m.ExternalOutflows) != 2 {
		t.Fatalf("row sets missing: %d inflow rows, %d outflow rows — honest zeros, not absent rows", len(m.Inflows), len(m.ExternalOutflows))
	}
	if m.TotalIn != 0 || m.TotalOut != 0 {
		t.Fatalf("empty window read in=%d out=%d, want 0/0", m.TotalIn, m.TotalOut)
	}
}

func TestLoans_SortedSnapshotOwnedByCaller(t *testing.T) {
	f := NewFinanceAPI("feat233")
	f.SetMilestoneGate(allowAllGate{})
	seedTreasury(t, f, gbp(10000))

	borrowed := map[LoanID]bool{}
	for range [3]struct{}{} {
		ln, err := f.Borrow(LoanRequest{Tier: 1, Principal: gbp(100), TermMonths: 12})
		if err != nil {
			t.Fatalf("Borrow: %v", err)
		}
		borrowed[ln.ID] = true
	}
	loans := f.Loans()
	if len(loans) != 3 {
		t.Fatalf("Loans() returned %d loans, want 3", len(loans))
	}
	for i := 1; i < len(loans); i++ {
		if loans[i-1].ID >= loans[i].ID {
			t.Fatalf("Loans() not ascending by ID at %d: %d then %d (GR#21)", i, loans[i-1].ID, loans[i].ID)
		}
		if !borrowed[loans[i].ID] {
			t.Fatalf("loan %d was never borrowed", loans[i].ID)
		}
	}
	// Caller-owned copy: mutating the slice must not touch the book.
	loans[0].Principal = 12345
	again := f.Loans()
	if again[0].Principal == 12345 {
		t.Fatal("Loans() aliased internal loan state — caller mutation reached the book (weakness pattern #1)")
	}
}
