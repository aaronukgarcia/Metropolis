package consumption

import (
	"sync"
	"testing"
)

// TestConcurrentNetworksNoRace is AC-17: the four networks solve
// concurrently within a single daily tick with no data race (run with
// -race). Each goroutine owns its own Network (mutable), while the shared
// *UtilityAPI is only ever read (its loaded maps are populated once at
// Load and never mutated), so the shared read path and the per-goroutine
// writes cannot race.
func TestConcurrentNetworksNoRace(t *testing.T) {
	api := realAPI(t)

	var wg sync.WaitGroup
	for _, kind := range allUtilities {
		kind := kind
		wg.Add(1)
		go func() {
			defer wg.Done()

			n := NewNetwork(kind, testCorrelationID())
			n.AddSource(Source{ID: "source", Capacity: 100})
			res, err := n.Solve([]Consumer{{EntityRef: "entity-a", Demand: 50}})
			if err != nil {
				t.Errorf("%s solve: %v", kind, err)
				return
			}
			if res.Delivered != 50 {
				t.Errorf("%s delivered = %v, want 50", kind, res.Delivered)
			}

			// Concurrent read-only query against the shared API.
			if _, err := api.ClassDemand("hospital", 1, DemandOptions{MonthIndex: 0, GasNetworkPresent: true}); err != nil {
				t.Errorf("%s ClassDemand: %v", kind, err)
			}
		}()
	}
	wg.Wait()
}
