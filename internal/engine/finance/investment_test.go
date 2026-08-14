package finance

import "testing"

// TestInvestmentPaybackCurveSpansMultipleYears (AC-8) proves an
// investment programme exposes a multi-year payback curve, not a
// single-month lump.
func TestInvestmentPaybackCurveSpansMultipleYears(t *testing.T) {
	f := NewFinanceAPI("ac8")
	seedTreasury(t, f, gbp(10000))

	prog, err := f.StartInvestment("hospital", gbp(1000), gbp(100), 36)
	if err != nil {
		t.Fatalf("StartInvestment: %v", err)
	}

	curve := prog.PaybackCurve()
	if len(curve) != 37 { // 0..36 inclusive
		t.Fatalf("payback curve length = %d, want 37", len(curve))
	}
	if len(curve) < 24 { // spans multiple simulated years
		t.Fatalf("payback curve should span multiple years, got %d points", len(curve))
	}
	// Incremental, not a single-month lump: the cumulative return grows
	// month over month across the curve.
	if curve[1].CumulativeReturn <= curve[0].CumulativeReturn {
		t.Fatalf("curve should grow from month 0 to 1, got %d then %d", curve[0].CumulativeReturn, curve[1].CumulativeReturn)
	}
	if curve[36].CumulativeReturn <= curve[1].CumulativeReturn {
		t.Fatal("cumulative return should keep growing across the curve")
	}
	if got := curve[36].CumulativeReturn; got != gbp(100)*36 {
		t.Fatalf("month-36 cumulative = %d, want %d", got, gbp(100)*36)
	}
	// Break-even: £1000 capex / £100 per month = month 10.
	if got := prog.BreakEvenMonth(); got != 10 {
		t.Fatalf("BreakEvenMonth = %d, want 10", got)
	}
}

// TestReserveInterestAccrual (AC-8) proves reserve interest accrues money
// into reserves and increases the money stock (an external inflow).
func TestReserveInterestAccrual(t *testing.T) {
	f := NewFinanceAPI("ac8-reserve")
	seedTreasury(t, f, gbp(1000))

	if _, err := f.AllocateToReserves(gbp(1000)); err != nil {
		t.Fatalf("AllocateToReserves: %v", err)
	}
	before := f.TotalMoneyInCirculation()

	interest, err := f.AccrueReserveInterest(100) // 1% monthly placeholder
	if err != nil {
		t.Fatalf("AccrueReserveInterest: %v", err)
	}
	if interest != gbp(10) {
		t.Fatalf("interest = %d, want 1%% of £1000 = £10", interest)
	}
	if after := f.TotalMoneyInCirculation(); after != before+gbp(10) {
		t.Fatalf("money stock should grow by the accrued interest: before %d, after %d", before, after)
	}
	if bal, _ := f.AccountBalance(AcctReserves); bal != gbp(1010) {
		t.Fatalf("reserves balance = %d, want %d", bal, gbp(1010))
	}
}
