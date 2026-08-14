package finance

import (
	"testing"
)

// TestHouseholdSpend (AC-3's spend stage) proves the household-spend
// stage is a distinct, visible link: its own ledger entries, its own
// category, and spend = quantity × price.
func TestHouseholdSpend(t *testing.T) {
	f := NewFinanceAPI("ac3-spend")
	seedTreasury(t, f, gbp(2000))

	if _, err := f.PostWages(gbp(1000)); err != nil {
		t.Fatalf("PostWages: %v", err)
	}
	spend, err := f.PostHouseholdSpend(300, gbp(2))
	if err != nil {
		t.Fatalf("PostHouseholdSpend: %v", err)
	}
	if spend != gbp(600) {
		t.Fatalf("spend = quantity × price = 300 × £2 = £600, got %d", spend)
	}
	if got := f.SpendPosted(); got != gbp(600) {
		t.Fatalf("SpendPosted() = %d, want %d", got, gbp(600))
	}
	// Distinct from wages: the spend stage posts its own entries, tagged
	// CatSpend, never folded into the wages category.
	if f.WagesPosted() == f.SpendPosted() {
		t.Fatal("wages and spend should be distinct posted amounts")
	}
	if len(f.LinesByCategory(CatSpend)) == 0 {
		t.Fatal("the spend stage must post its own CatSpend entries")
	}
	if len(f.LinesByCategory(CatWages)) == 0 {
		t.Fatal("the wages stage must post CatWages entries")
	}
}

// TestMonthlyFlowFullChain (AC-3) runs the full §7 chain for one
// synthetic month with hand-computable inputs and asserts each stage's
// own posted amount, not just a plausible final balance.
func TestMonthlyFlowFullChain(t *testing.T) {
	f := NewFinanceAPI("ac3-chain")
	seedTreasury(t, f, gbp(5000))
	f.BeginMonth(1)

	// Known inputs.
	wages := gbp(1000)        // fixed wage bill
	quantity := int64(300)    // fixed utility-consumption quantity
	price := gbp(2)           // fixed Market price per unit
	rate := BasisPoints(2000) // 20% headline rate

	// (1) wages
	if got, err := f.PostWages(wages); err != nil || got != wages {
		t.Fatalf("PostWages = %d, %v; want %d", got, err, wages)
	}
	// (2) spend = quantity × price
	spend, err := f.PostHouseholdSpend(quantity, price)
	if err != nil {
		t.Fatalf("PostHouseholdSpend: %v", err)
	}
	if spend != gbp(600) {
		t.Fatalf("spend = %d, want %d", spend, gbp(600))
	}
	// (3) tax = rate × (wages + spend)
	receipts, err := f.CollectTax(TaxRates{IncomeRate: rate, SalesRate: rate, CorpRate: 0}, wages, spend, 0)
	if err != nil {
		t.Fatalf("CollectTax: %v", err)
	}
	if want := gbp(320); receipts.Total() != want {
		t.Fatalf("tax total = %d, want rate × (wages + spend) = %d", receipts.Total(), want)
	}
	// (5) outflows
	if _, err := f.SettleOpex(gbp(100)); err != nil {
		t.Fatalf("SettleOpex: %v", err)
	}
	if err := f.ServiceDebt(gbp(50), gbp(100)); err != nil {
		t.Fatalf("ServiceDebt: %v", err)
	}
	if _, err := f.SettleConstruction(gbp(50)); err != nil {
		t.Fatalf("SettleConstruction: %v", err)
	}

	// Each stage's own posted amount, hand-computed.
	if got := f.WagesPosted(); got != wages {
		t.Errorf("WagesPosted = %d, want %d", got, wages)
	}
	if got := f.SpendPosted(); got != spend {
		t.Errorf("SpendPosted = %d, want %d", got, spend)
	}
	if got := f.TaxRevenue(); got != receipts.Total() {
		t.Errorf("TaxRevenue = %d, want %d", got, receipts.Total())
	}
	if got := f.OpexTotal(); got != gbp(100) {
		t.Errorf("OpexTotal = %d, want %d", got, gbp(100))
	}
	if got := f.DebtServiceTotal(); got != gbp(50) {
		t.Errorf("DebtServiceTotal = %d, want %d", got, gbp(50))
	}
	if got := f.ConstructionTotal(); got != gbp(50) {
		t.Errorf("ConstructionTotal = %d, want %d", got, gbp(50))
	}
	// budget = tax − opex − debt − construction (imports zero).
	if want, got := gbp(120), f.BudgetBalance(); got != want {
		t.Errorf("BudgetBalance = %d, want %d", got, want)
	}

	// Money conservation across the chain: income == expenditure + delta.
	stock := f.MoneyStock()
	if delta := stock.Closing - stock.Opening; delta != stock.TrackedDelta {
		t.Errorf("conservation broken: Closing-Opening = %d, TrackedDelta = %d", delta, stock.TrackedDelta)
	}
}
