package finance

import (
	"sync"
	"testing"
)

// BUG-723 round findings F5 (atomic combined read) and F7 (months counts
// distinct MONTHS, not calls).

// TestPayrollShortfallMonths_CountsDistinctMonthsNotCalls (F7) is the
// RED-PROOF: calling RecordPayrollShortfall twice for the SAME month
// (a defensive re-post/retry, or simply a caller bug — nothing upstream
// enforces "once per month") must NOT double-count the streak. Before
// the F7 fix (unconditional payrollShortfallMonths++ on any
// shortfall>0 call), this test's second same-month call would have
// advanced the streak to 2 despite only one real month having elapsed.
func TestPayrollShortfallMonths_CountsDistinctMonthsNotCalls(t *testing.T) {
	f := NewFinanceAPI("bug723-f7")

	f.RecordPayrollShortfall(5, gbp(100))
	if got := f.PayrollShortfallMonths(); got != 1 {
		t.Fatalf("after the first call for month 5: PayrollShortfallMonths() = %d, want 1", got)
	}

	// A SECOND call for the SAME month (a re-post/retry shape) — the
	// streak must NOT advance again.
	f.RecordPayrollShortfall(5, gbp(150))
	if got := f.PayrollShortfallMonths(); got != 1 {
		t.Fatalf("BUG-723 F7 regression: a second call for the SAME month (5) advanced PayrollShortfallMonths() to %d, want 1 (still counts as ONE month in shortfall)", got)
	}
	// The latest amount for that month still wins (most-recent-call
	// semantics for the amount itself are unchanged by F7).
	if _, amount := f.PayrollShortfall(); amount != gbp(150) {
		t.Fatalf("after the repeat call: PayrollShortfall() amount = %d, want the latest call's 150", int64(amount))
	}

	// A genuinely NEW month DOES advance the streak.
	f.RecordPayrollShortfall(6, gbp(200))
	if got := f.PayrollShortfallMonths(); got != 2 {
		t.Fatalf("after a NEW month (6): PayrollShortfallMonths() = %d, want 2", got)
	}

	// Three calls for month 6 (still the same month) must still read 2.
	f.RecordPayrollShortfall(6, gbp(210))
	f.RecordPayrollShortfall(6, gbp(220))
	if got := f.PayrollShortfallMonths(); got != 2 {
		t.Fatalf("three calls for the SAME month (6) advanced PayrollShortfallMonths() to %d, want 2", got)
	}

	// Clearing resets to 0 regardless of how many months preceded it.
	f.RecordPayrollShortfall(7, 0)
	if got := f.PayrollShortfallMonths(); got != 0 {
		t.Fatalf("after a clearing call: PayrollShortfallMonths() = %d, want 0", got)
	}
}

// TestPayrollShortfallStatus_AtomicUnderConcurrentAccess (F5) is the
// concurrent RED-PROOF: PayrollShortfallStatus's three return values must
// never be torn relative to each other. The invariant this test hammers:
// amount>0 if-and-only-if months>0 (RecordPayrollShortfall always sets or
// clears BOTH together under one write-lock critical section) — a reader
// using two SEPARATE lock acquisitions (the pre-F5 shape: PayrollShortfall()
// then PayrollShortfallMonths()) could observe amount>0 paired with
// months==0 (or the reverse) if a RecordPayrollShortfall call landed
// between the two reads. PayrollShortfallStatus reads all three fields
// under ONE RLock, so that torn combination must never appear no matter
// how many goroutines hammer Record concurrently with Status reads.
func TestPayrollShortfallStatus_AtomicUnderConcurrentAccess(t *testing.T) {
	f := NewFinanceAPI("bug723-f5")

	const writers = 8
	const readers = 8
	const opsPerGoroutine = 2000

	var wg sync.WaitGroup
	wg.Add(writers + readers)

	for w := 0; w < writers; w++ {
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				month := int64(seed*opsPerGoroutine + i)
				if i%2 == 0 {
					f.RecordPayrollShortfall(month, gbp(int64(100+i)))
				} else {
					f.RecordPayrollShortfall(month, 0)
				}
			}
		}(w)
	}

	tornCh := make(chan string, readers)
	for r := 0; r < readers; r++ {
		go func() {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				month, amount, months := f.PayrollShortfallStatus()
				if (amount > 0) != (months > 0) {
					select {
					case tornCh <- (func() string {
						return "torn read observed"
					})():
					default:
					}
					_ = month
				}
			}
		}()
	}

	wg.Wait()
	close(tornCh)
	for msg := range tornCh {
		t.Fatalf("BUG-723 F5 regression: %s — PayrollShortfallStatus returned amount>0 with months==0 (or the reverse), meaning the three fields were read torn relative to each other", msg)
	}
}
