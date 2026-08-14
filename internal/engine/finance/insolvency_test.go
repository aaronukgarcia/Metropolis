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
