package capexport

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
)

// TestTradeLedgerBidirectional (AC-7): contract revenue and cancellation
// penalties are BOTH posted through FinanceAPI tagged with the distinct
// trade/export category, and they have opposite net effect on the city's
// balance — revenue credits the city, the penalty debits it. A model that only
// ever posts revenue (penalties tracked as an internal counter) fails this
// test: both must be retrievable through the same trade-tagged drill-through.
func TestTradeLedgerBidirectional(t *testing.T) {
	a, svc, fin, _ := newTestAPI(t)
	id := registerService(t, svc, "hospital", 100)
	bindLine(t, a, ExportHospitalBeds, id)

	c, err := a.IssueContract(IssueRequest{Line: ExportHospitalBeds, Quantity: 2, TermMonths: 12, RateMicropounds: 1_000_000})
	if err != nil {
		t.Fatalf("IssueContract: %v", err)
	}

	// Revenue: 3 months × rate 1,000,000 × quantity 2 = 6,000,000 micro-pounds.
	revenue, err := a.AccrueRevenue(c.ID, 3)
	if err != nil {
		t.Fatalf("AccrueRevenue: %v", err)
	}
	if revenue != 6_000_000 {
		t.Fatalf("revenue = %d, want 6000000", revenue)
	}

	// Penalty: full remaining term 12 × rate 1,000,000 × quantity 2 = 24,000,000.
	canc, err := a.PayCancellationPenalty(c.ID)
	if err != nil {
		t.Fatalf("PayCancellationPenalty: %v", err)
	}
	if canc.Penalty != 24_000_000 {
		t.Fatalf("penalty = %d, want 24000000", canc.Penalty)
	}

	// Both must be retrievable through the same trade-tagged drill-through.
	var sawRevenueCredit, sawPenaltyDebit bool
	for _, e := range fin.LinesByCategory(CatTradeExport) {
		if e.Account != finance.AcctTreasury {
			continue
		}
		if e.Side == finance.SideCredit && e.Amount == revenue {
			sawRevenueCredit = true
		}
		if e.Side == finance.SideDebit && e.Amount == canc.Penalty {
			sawPenaltyDebit = true
		}
	}
	if !sawRevenueCredit {
		t.Fatalf("revenue %d not found as a treasury credit in the %s category", revenue, CatTradeExport)
	}
	if !sawPenaltyDebit {
		t.Fatalf("penalty %d not found as a treasury debit in the %s category", canc.Penalty, CatTradeExport)
	}

	// The two flows have opposite net effect on the city treasury balance.
	bal, ok := fin.AccountBalance(finance.AcctTreasury)
	if !ok {
		t.Fatalf("treasury account missing")
	}
	// revenue (credit) − penalty (debit) = net effect; with the seeded credit
	// line the balance reflects the net (6,000,000 − 24,000,000 = −18,000,000).
	if want := finance.Money(6_000_000 - 24_000_000); bal != want {
		t.Fatalf("treasury balance = %d, want %d (revenue − penalty)", bal, want)
	}
}
