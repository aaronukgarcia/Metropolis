package maintenance

import (
	"sync"
	"testing"
)

// TestConcurrentQueriesWithTick proves AC-14: the maintenance query surface
// (per-instance view, per-class/total backlog, city-wide demand) is safe to
// read concurrently with a tick aging objects and applying a crew. Run with
// -race. The concurrent hammer asserts only what holds under ANY schedule —
// every read is a success (or the one documented error), never a third
// outcome and never a panic.
func TestConcurrentQueriesWithTick(t *testing.T) {
	a := newTestAPI(t)
	if err := a.Register(1, "dwelling", RegisterOptions{}, "test"); err != nil {
		t.Fatalf("register 1: %v", err)
	}
	if err := a.Register(2, "heavy_industry", RegisterOptions{}, "test"); err != nil {
		t.Fatalf("register 2: %v", err)
	}
	if err := a.AdvanceMonth(24, "test"); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if _, err := a.EnqueueJob("dwelling", 5, "test"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := a.SetDailyBudget(1000, "test"); err != nil {
		t.Fatalf("set budget: %v", err)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 256)

	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := a.View(1, "test"); err != nil {
				errCh <- err
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := a.Backlog("dwelling", "test"); err != nil {
				errCh <- err
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := a.CityDemand("test"); err != nil {
				errCh <- err
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			if total, err := a.TotalBacklog("test"); err != nil {
				errCh <- err
			} else if total < 0 {
				errCh <- errNegBacklog{}
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := a.AdvanceMonth(1, "test"); err != nil {
				errCh <- err
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := a.RunCrewDay("test"); err != nil {
				errCh <- err
			}
		}()
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent operation error: %v", err)
	}
}

// errNegBacklog is a sentinel error for the "backlog went negative" outcome —
// it can never be returned by the API, so reaching it means an invariant broke
// under concurrency.
type errNegBacklog struct{}

func (errNegBacklog) Error() string { return "backlog went negative under concurrency" }
