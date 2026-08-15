package unlocks

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
)

// --- AC-9: money-only Buy path, independent of the points economy ------

// TestBuyOffMapCapacityIndependentOfDP is the AC-9 proof. It builds a
// ZERO-DP world (no milestone crossing, so no DP grant) and asserts the
// player can still purchase a tranche given sufficient money. It then
// builds a world WITH a positive DP balance and asserts that balance is
// UNCHANGED after a Buy purchase — the false-pass guard AC-9 names (a
// build that silently deducts DP in the background would fail this second
// assertion).
func TestBuyOffMapCapacityIndependentOfDP(t *testing.T) {
	// Zero-DP world: no milestone crossed, so DevelopmentPoints() == 0.
	api, f := realAPIWithFinance(t)
	if api.DevelopmentPoints() != 0 {
		t.Fatalf("fresh world has %d DP, want 0 (zero-DP baseline)", api.DevelopmentPoints())
	}
	fundTreasury(t, f, offMapBuyPrices[OffMapGrid]*10)

	moneyBefore := f.TotalMoneyInCirculation()
	if err := api.BuyOffMapCapacity(OffMapGrid, testCorrelationID()); err != nil {
		t.Fatalf("BuyOffMapCapacity(grid) with zero DP: %v", err)
	}
	if got, err := api.OffMapCapacity(OffMapGrid); err != nil || got != 1 {
		t.Errorf("OffMapCapacity(grid) = %d, %v; want 1, nil (the tranche was granted)", got, err)
	}
	if api.DevelopmentPoints() != 0 {
		t.Errorf("DevelopmentPoints = %d after Buy, want 0 (Buy must not touch DP)", api.DevelopmentPoints())
	}
	// Money was actually debited.
	if delta := f.TotalMoneyInCirculation() - moneyBefore; delta != -offMapBuyPrices[OffMapGrid] {
		t.Errorf("money delta after Buy = %d, want %d (debit the purchase price)", delta, -int64(offMapBuyPrices[OffMapGrid]))
	}

	// Positive-DP world: cross tier 1 to gain DP, then Buy — DP unchanged.
	api2, f2 := realAPIWithFinance(t)
	if _, err := api2.AdvancePopulation(0, testCorrelationID()); err != nil {
		t.Fatalf("AdvancePopulation(0): %v", err)
	}
	dpBefore := api2.DevelopmentPoints()
	if dpBefore <= 0 {
		t.Fatalf("positive-DP world has %d DP, want > 0", dpBefore)
	}
	fundTreasury(t, f2, offMapBuyPrices[OffMapPort]*10)
	if err := api2.BuyOffMapCapacity(OffMapPort, testCorrelationID()); err != nil {
		t.Fatalf("BuyOffMapCapacity(port) with positive DP: %v", err)
	}
	if api2.DevelopmentPoints() != dpBefore {
		t.Errorf("DevelopmentPoints changed after Buy: %d -> %d; Buy is coupled to the points economy (US-5 violation)",
			dpBefore, api2.DevelopmentPoints())
	}
}

// fundTreasury posts an external inflow crediting the treasury, so Buy
// tests start from a city that can actually afford a purchase.
func fundTreasury(t *testing.T, f *finance.FinanceAPI, amount finance.Money) {
	t.Helper()
	if _, err := f.Post(finance.Transaction{
		Description: "test treasury funding",
		Entries: []finance.Entry{
			{Account: finance.AcctExternal, Side: finance.SideDebit, Amount: amount},
			{Account: finance.AcctTreasury, Side: finance.SideCredit, Amount: amount},
		},
	}); err != nil {
		t.Fatalf("fund treasury: %v", err)
	}
}

// TestBuyRejectsUnknownKind rejects a kind outside the five §22 names.
func TestBuyRejectsUnknownKind(t *testing.T) {
	api, _ := realAPIWithFinance(t)
	err := api.BuyOffMapCapacity(OffMapKind("helicopters"), testCorrelationID())
	assertCode(t, err, ErrUnknownOffMapKind)
	if _, err := api.OffMapCapacity(OffMapKind("helicopters")); err == nil {
		t.Error("OffMapCapacity(unknown kind) returned nil error; want ErrUnknownOffMapKind")
	} else {
		assertCode(t, err, ErrUnknownOffMapKind)
	}
}

// TestBuyRequiresFinanceWired proves the Buy path fails loudly rather than
// silently no-op'ing when finance is unwired (GR#17).
func TestBuyRequiresFinanceWired(t *testing.T) {
	api := realAPI(t) // no finance wired
	err := api.BuyOffMapCapacity(OffMapGrid, testCorrelationID())
	assertCode(t, err, ErrFinanceNotWired)
	if got, _ := api.OffMapCapacity(OffMapGrid); got != 0 {
		t.Errorf("OffMapCapacity(grid) = %d after a rejected unwired Buy, want 0", got)
	}
}

// TestBuyInsufficientFundsSurfaced proves a treasury overdraft is surfaced
// (finance's ErrInsufficientFunds) rather than swallowed, when the city
// has no money to cover the purchase.
func TestBuyInsufficientFundsSurfaced(t *testing.T) {
	api, f := realAPIWithFinance(t)
	// The treasury starts at zero; a Buy debit (no credit line) is an
	// overdraft finance.Post rejects.
	err := api.BuyOffMapCapacity(OffMapPort, testCorrelationID())
	assertCode(t, err, finance.ErrInsufficientFunds)
	_ = f
}
