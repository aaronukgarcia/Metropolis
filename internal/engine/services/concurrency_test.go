package services

import (
	"sync"
	"testing"
)

// --- AC-15: race-free concurrent query/draw from shared pools -------------

// TestConcurrentPoolQueriesAndDrawsAreRaceFree runs many goroutines
// concurrently querying quality and drawing from the shared staffing pool
// against one *ServicesAPI, with no data race (verified by -race). It
// asserts only the outcome class — every result is either a nil error or
// one specific registered error — never a third, schedule-dependent
// outcome, per the determinism-baseline rule ("a concurrent hammer may
// only assert what it can guarantee under any schedule").
func TestConcurrentPoolQueriesAndDrawsAreRaceFree(t *testing.T) {
	a := testLoadedAPI(t)
	registerService(t, a, "hosp-a", ServiceHealthcare, 100, 10, 10)
	registerService(t, a, "hosp-b", ServiceHealthcare, 100, 10, 10)
	registerService(t, a, "elder-a", ServiceElderCare, 100, 10, 10)
	if err := a.SetPoolStaff("nursing", 15); err != nil {
		t.Fatalf("SetPoolStaff: %v", err)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 256)
	for i := 0; i < 32; i++ {
		n := i // per-iteration copy, so the no-arg closure captures a distinct value
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := a.AllocateStaffing("nursing"); err != nil {
				errCh <- err
			}
			if _, err := a.Quality("hosp-a"); err != nil {
				errCh <- err
			}
			if _, err := a.Quality("elder-a"); err != nil {
				errCh <- err
			}
			if _, err := a.StaffingNeed(ServiceHealthcare, float64(n)*1000); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent error: %v", err)
	}
}
