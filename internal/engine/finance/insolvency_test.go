package finance

import "testing"

// TestInsolvencyThreeConsecutiveMonthsGameOver (AC-7) proves game over
// fires at exactly three consecutive months of unmet obligations with no
// credit available.
func TestInsolvencyThreeConsecutiveMonthsGameOver(t *testing.T) {
	f := NewFinanceAPI("ac7-3")

	f.RecordMonthResult(false, false)
	f.RecordMonthResult(false, false)
	if f.IsInsolvent() {
		t.Fatal("should not be insolvent before the third consecutive failing month")
	}
	res := f.RecordMonthResult(false, false)
	if !res.GameOver || !f.IsInsolvent() {
		t.Fatalf("expected game over at the third consecutive failure, got %+v", res)
	}
	if res.ConsecutiveFailedMonths != 3 {
		t.Fatalf("consecutive months = %d, want 3", res.ConsecutiveFailedMonths)
	}
}

// TestInsolvencyResetOnSuccess (AC-7) proves a successful month resets
// the counter to zero (not decrements), so a later second failure does
// not game-over.
func TestInsolvencyResetOnSuccess(t *testing.T) {
	f := NewFinanceAPI("ac7-reset")

	f.RecordMonthResult(false, false) // 1
	f.RecordMonthResult(false, false) // 2
	f.RecordMonthResult(true, false)  // obligations met → reset to 0
	if f.InsolvencyMonths() != 0 {
		t.Fatalf("counter should reset to 0 after a successful month, got %d", f.InsolvencyMonths())
	}
	f.RecordMonthResult(false, false) // 1
	if f.IsInsolvent() {
		t.Fatal("a single failure after a reset must not game-over")
	}
	if f.InsolvencyMonths() != 1 {
		t.Fatalf("counter should be 1, got %d", f.InsolvencyMonths())
	}
}

// TestInsolvencyResetOnCreditAvailable (AC-7) proves available-but-unused
// credit resets the counter.
func TestInsolvencyResetOnCreditAvailable(t *testing.T) {
	f := NewFinanceAPI("ac7-credit")

	f.RecordMonthResult(false, false) // 1
	f.RecordMonthResult(false, true)  // credit available (even if unused) → reset
	if f.InsolvencyMonths() != 0 {
		t.Fatalf("counter should reset when credit was available, got %d", f.InsolvencyMonths())
	}
}

// TestCremationShortfallAccruesAdditively (BUG-733, GR#17) proves
// RecordCremationShortfall ADDS onto the running balance across multiple
// calls — unlike RecordPayrollShortfall, which overwrites a single
// this-month value — and that CremationShortfall() reports the LAST
// month a new shortfall was recorded alongside the running total.
func TestCremationShortfallAccruesAdditively(t *testing.T) {
	f := NewFinanceAPI("bug733-accrue")

	f.RecordCremationShortfall(1, gbp(150))
	if got := f.CremationShortfallOwed(); got != gbp(150) {
		t.Fatalf("after first accrual, CremationShortfallOwed() = %d, want %d", got, gbp(150))
	}
	f.RecordCremationShortfall(2, gbp(300))
	if got := f.CremationShortfallOwed(); got != gbp(450) {
		t.Fatalf("after second accrual, CremationShortfallOwed() = %d, want %d (additive, not overwritten)", got, gbp(450))
	}
	month, owed := f.CremationShortfall()
	if month != 2 || owed != gbp(450) {
		t.Fatalf("CremationShortfall() = (month=%d, owed=%d), want (month=2, owed=%d)", month, owed, gbp(450))
	}
}

// TestCremationShortfallRecordIgnoresNegative (GR#15) proves a negative
// amount passed to RecordCremationShortfall is ignored rather than
// silently driving the debt negative or being clamped to some other
// substituted value.
func TestCremationShortfallRecordIgnoresNegative(t *testing.T) {
	f := NewFinanceAPI("bug733-negative")
	f.RecordCremationShortfall(1, gbp(100))
	f.RecordCremationShortfall(1, -gbp(999))
	if got := f.CremationShortfallOwed(); got != gbp(100) {
		t.Fatalf("a negative RecordCremationShortfall call must be ignored — CremationShortfallOwed() = %d, want %d", got, gbp(100))
	}
}

// TestCremationShortfallRepayFloorsAtZero proves RepayCremationShortfall
// never drives the balance negative even when repaid MORE than is owed
// (a caller bug elsewhere must never manufacture a negative "debt",
// which would nonsensically read as the treasury being OWED money).
func TestCremationShortfallRepayFloorsAtZero(t *testing.T) {
	f := NewFinanceAPI("bug733-repay-floor")
	f.RecordCremationShortfall(1, gbp(100))
	f.RepayCremationShortfall(gbp(9999))
	if got := f.CremationShortfallOwed(); got != 0 {
		t.Fatalf("RepayCremationShortfall must floor at zero, got %d", got)
	}
}

// TestCremationShortfallRepayIgnoresNonPositive proves a zero or negative
// repayment amount is a no-op (never a phantom negative-amount posting
// side effect on the debt tracker).
func TestCremationShortfallRepayIgnoresNonPositive(t *testing.T) {
	f := NewFinanceAPI("bug733-repay-nonpositive")
	f.RecordCremationShortfall(1, gbp(100))
	f.RepayCremationShortfall(0)
	f.RepayCremationShortfall(-gbp(50))
	if got := f.CremationShortfallOwed(); got != gbp(100) {
		t.Fatalf("a non-positive RepayCremationShortfall call must be a no-op — CremationShortfallOwed() = %d, want %d", got, gbp(100))
	}
}
