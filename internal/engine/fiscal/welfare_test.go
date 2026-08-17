package fiscal

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
)

// TestSocialHousingBuildPostsThroughConstruction asserts the social-housing
// build programme posts through engine.finance's own construction-settlement
// stage — the same ledger category (CatConstruction) ordinary construction
// uses — not a parallel fiscal-only construction cost model (AC-7).
func TestSocialHousingBuildPostsThroughConstruction(t *testing.T) {
	f, fin, _ := newTestFiscal(t)
	if err := fin.BeginMonth(1); err != nil {
		t.Fatalf("BeginMonth: %v", err)
	}
	seedTreasury(t, fin, 10_000_000_000)

	const cost finance.Money = 500_000
	if _, err := f.PostSocialHousingBuild(cost); err != nil {
		t.Fatalf("PostSocialHousingBuild: %v", err)
	}

	if got := fin.ConstructionTotal(); got != cost {
		t.Errorf("ConstructionTotal() = %d, want %d (build must reuse finance's construction stage)", int64(got), int64(cost))
	}
	lines := fin.LinesByCategory(finance.CatConstruction)
	if len(lines) != 2 {
		t.Fatalf("expected 2 CatConstruction ledger lines, got %d", len(lines))
	}
}

// TestBenefitsPostThroughFinanceLedger asserts unemployment support and
// housing benefit post through engine.finance's double-entry ledger as
// balanced treasury→households transfers, tagged with fiscal's benefit
// categories, and conserved (treasury down, households up, money stock
// unchanged) (AC-7).
func TestBenefitsPostThroughFinanceLedger(t *testing.T) {
	f, fin, _ := newTestFiscal(t)
	if err := fin.BeginMonth(1); err != nil {
		t.Fatalf("BeginMonth: %v", err)
	}
	seedTreasury(t, fin, 10_000_000_000)

	before, ok := fin.AccountBalance(finance.AcctTreasury)
	if !ok {
		t.Fatal("treasury balance unavailable")
	}
	stockBefore := fin.TotalMoneyInCirculation()

	const support finance.Money = 250_000
	if _, err := f.PostUnemploymentSupport(support); err != nil {
		t.Fatalf("PostUnemploymentSupport: %v", err)
	}
	const housing finance.Money = 150_000
	if _, err := f.PostHousingBenefit(housing); err != nil {
		t.Fatalf("PostHousingBenefit: %v", err)
	}

	after, _ := fin.AccountBalance(finance.AcctTreasury)
	if want := before - support - housing; after != want {
		t.Errorf("treasury balance = %d, want %d", int64(after), int64(want))
	}
	// Benefits are internal transfers (treasury → households): the money stock
	// is conserved (no money created or destroyed).
	if stock := fin.TotalMoneyInCirculation(); stock != stockBefore {
		t.Errorf("TotalMoneyInCirculation = %d, want %d (benefits must conserve money)", int64(stock), int64(stockBefore))
	}

	// The postings are tagged with the fiscal benefit categories, so they are
	// drill-through-able from the ledger.
	if got := len(fin.LinesByCategory(catBenefitUnemployment)); got != 2 {
		t.Errorf("LinesByCategory(unemployment) = %d lines, want 2", got)
	}
	if got := len(fin.LinesByCategory(catBenefitHousing)); got != 2 {
		t.Errorf("LinesByCategory(housing) = %d lines, want 2", got)
	}
}

// TestBenefitNegativeAmountRejected asserts the benefit input boundary
// (GR#16 — money is never negative).
func TestBenefitNegativeAmountRejected(t *testing.T) {
	f, _, _ := newTestFiscal(t)
	if _, err := f.PostUnemploymentSupport(-1); err == nil {
		t.Fatal("PostUnemploymentSupport(-1) returned nil error, want ErrInvalidInput")
	}
	if _, err := f.PostHousingBenefit(-1); err == nil {
		t.Fatal("PostHousingBenefit(-1) returned nil error, want ErrInvalidInput")
	}
	if _, err := f.PostSocialHousingBuild(-1); err == nil {
		t.Fatal("PostSocialHousingBuild(-1) returned nil error, want ErrInvalidInput")
	}
}
