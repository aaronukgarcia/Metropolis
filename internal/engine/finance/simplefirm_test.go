package finance

import "testing"

// TestFirmOpensOnProfit (AC-9) proves a firm with positive projected
// profitability opens.
func TestFirmOpensOnProfit(t *testing.T) {
	firm, err := NewSimpleFirm("shop", gbp(1000), gbp(400), gbp(300), gbp(100))
	if err != nil {
		t.Fatalf("NewSimpleFirm: %v", err)
	}
	if got := firm.MonthlyProfit(); got != gbp(200) {
		t.Fatalf("MonthlyProfit = %d, want %d", got, gbp(200))
	}
	if !firm.Open {
		t.Fatal("a profitable firm should open")
	}
}

// TestFirmClosesOnLoss (AC-9) proves a firm closes on a sustained
// (consecutive-month) negative P&L, not a single loss month.
func TestFirmClosesOnLoss(t *testing.T) {
	firm, err := NewSimpleFirm("shop", gbp(1000), gbp(400), gbp(300), gbp(100))
	if err != nil {
		t.Fatalf("NewSimpleFirm: %v", err)
	}
	if !firm.Open {
		t.Fatal("firm should start open (positive projected profit)")
	}

	// Demand collapses: the firm is now loss-making every month.
	firm.Revenue = gbp(600) // profit = 600 - 400 - 300 - 100 = -200
	firm.AdvanceMonth()     // streak 1
	firm.AdvanceMonth()     // streak 2
	if !firm.Open {
		t.Fatal("a firm should survive two loss months before closing")
	}
	firm.AdvanceMonth() // streak 3 → close
	if firm.Open {
		t.Fatal("a firm should close after a sustained loss streak")
	}
}

// TestFirmInvalidRejected proves a negative revenue or cost input is
// rejected, never silently defaulted.
func TestFirmInvalidRejected(t *testing.T) {
	if _, err := NewSimpleFirm("bad", -gbp(1), 0, 0, 0); !hasCode(err, ErrInvalidFirm) {
		t.Fatalf("expected ErrInvalidFirm for negative revenue, got %v", err)
	}
	if _, err := NewSimpleFirm("bad", gbp(1), -gbp(2), 0, 0); !hasCode(err, ErrInvalidFirm) {
		t.Fatalf("expected ErrInvalidFirm for a negative cost, got %v", err)
	}
}
