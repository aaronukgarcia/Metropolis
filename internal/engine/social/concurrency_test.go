package social

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentCaseloadUpdates (AC-17): concurrent caseload updates across
// categories within a tick are safe — the goroutines hammer AdvanceMonth on a
// single *SocialAPI, and the final open-case count reconciles to the exact
// sum of every concurrent open (no lost updates). The -race build verifies
// there is no data race; this test asserts only what it can guarantee under
// any schedule (the conserved total), never a timing-dependent order.
func TestConcurrentCaseloadUpdates(t *testing.T) {
	a, err := New(testConfig(), 1, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	const workers = 16
	var expected int64
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			in := DriverInputs{Deprivation: 0.4, UnemploymentMonths: int64(i), NightlifeDensity: 0.5}
			cases, err := a.GenerateCaseload(int64(i+1), in)
			if err != nil {
				t.Errorf("GenerateCaseload: %v", err)
				return
			}
			sum := int64(len(cases))
			atomic.AddInt64(&expected, sum)
			if err := a.AdvanceMonth(int64(i+1), in); err != nil {
				t.Errorf("AdvanceMonth: %v", err)
			}
		}(i)
	}
	wg.Wait()

	if got := a.totalOpenCases(); got != expected {
		t.Fatalf("total open cases = %d, want %d (exact sum of concurrent opens)", got, expected)
	}
}
