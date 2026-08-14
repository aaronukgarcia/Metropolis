package finance

import "testing"

// TestCreditRatingDirection (AC-5) proves worse inputs produce a worse
// (lower) rating, and that a worse rating maps to a higher interest rate.
func TestCreditRatingDirection(t *testing.T) {
	lessDebt := CreditRating(int64(gbp(500)), int64(gbp(1000)), 0, 0)
	moreDebt := CreditRating(int64(gbp(1500)), int64(gbp(1000)), 0, 0)
	if moreDebt >= lessDebt {
		t.Errorf("more debt/revenue (%d) should rate below less (%d)", moreDebt, lessDebt)
	}

	noMiss := CreditRating(int64(gbp(1000)), int64(gbp(1000)), 0, 0)
	oneMiss := CreditRating(int64(gbp(1000)), int64(gbp(1000)), 1, 0)
	if oneMiss >= noMiss {
		t.Errorf("a missed payment (%d) should rate below clean history (%d)", oneMiss, noMiss)
	}

	fewReserves := CreditRating(int64(gbp(1000)), int64(gbp(1000)), 0, 0)
	manyReserves := CreditRating(int64(gbp(1000)), int64(gbp(1000)), 0, 3)
	if manyReserves <= fewReserves {
		t.Errorf("more reserve months (%d) should rate above fewer (%d)", manyReserves, fewReserves)
	}

	if InterestRate(moreDebt) <= InterestRate(lessDebt) {
		t.Error("a worse rating should produce a higher interest rate")
	}
}

// TestLoanMilestoneGate (AC-5) proves loan facilities are gated by the
// injected MilestoneGate, not a hardcoded tier check.
func TestLoanMilestoneGate(t *testing.T) {
	f := NewFinanceAPI("ac5-gate")

	// No gate installed: every facility is unavailable.
	if _, err := f.Borrow(LoanRequest{Tier: 0, Principal: gbp(100), TermMonths: 12}); !hasCode(err, ErrLoanUnavailable) {
		t.Fatalf("expected ErrLoanUnavailable with no gate, got %v", err)
	}

	// A denying gate: still unavailable.
	if err := f.SetMilestoneGate(denyGate{}); err != nil {
		t.Fatalf("SetMilestoneGate: %v", err)
	}
	if _, err := f.Borrow(LoanRequest{Tier: 0, Principal: gbp(100), TermMonths: 12}); !hasCode(err, ErrLoanUnavailable) {
		t.Fatalf("expected ErrLoanUnavailable with a denying gate, got %v", err)
	}

	// An allowing gate: available.
	if err := f.SetMilestoneGate(allowAllGate{}); err != nil {
		t.Fatalf("SetMilestoneGate: %v", err)
	}
	loan, err := f.Borrow(LoanRequest{Tier: 0, Principal: gbp(100), TermMonths: 12})
	if err != nil {
		t.Fatalf("expected Borrow to succeed with an allowing gate, got %v", err)
	}
	if loan.Outstanding != gbp(100) {
		t.Fatalf("loan outstanding = %d, want %d", loan.Outstanding, gbp(100))
	}
}

// TestRefinancingSpiral (AC-6) proves missing a payment degrades the
// rating, raising the rate on the next borrowing, compounding over two
// missed-payment months.
func TestRefinancingSpiral(t *testing.T) {
	f := NewFinanceAPI("ac6")
	if err := f.SetMilestoneGate(allowAllGate{}); err != nil {
		t.Fatalf("SetMilestoneGate: %v", err)
	}

	first, err := f.Borrow(LoanRequest{Tier: 0, Principal: gbp(1000), TermMonths: 120})
	if err != nil {
		t.Fatalf("first Borrow: %v", err)
	}

	// One missed payment.
	if err := f.MissPayment(first.ID); err != nil {
		t.Fatalf("MissPayment: %v", err)
	}
	second, err := f.Borrow(LoanRequest{Tier: 0, Principal: gbp(1000), TermMonths: 120})
	if err != nil {
		t.Fatalf("second Borrow: %v", err)
	}
	if second.RateBp <= first.RateBp {
		t.Fatalf("after one miss the new rate %d should exceed the clean rate %d", second.RateBp, first.RateBp)
	}

	// A second missed payment compounds the spiral further.
	if err := f.MissPayment(second.ID); err != nil {
		t.Fatalf("MissPayment: %v", err)
	}
	third, err := f.Borrow(LoanRequest{Tier: 0, Principal: gbp(1000), TermMonths: 120})
	if err != nil {
		t.Fatalf("third Borrow: %v", err)
	}
	if third.RateBp <= second.RateBp {
		t.Fatalf("after the second miss the rate %d should exceed the previous %d (compounding)", third.RateBp, second.RateBp)
	}
}
