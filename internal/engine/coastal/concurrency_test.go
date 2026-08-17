package coastal

import (
	"sync"
	"testing"
)

// TestConcurrentQueriesRaceFree (AC-17): CoastalAPI's read path is safe for
// concurrent use. Multiple goroutines query the event stream, per-case stage,
// backlog, and policy/cost metrics simultaneously; the -race build catches any
// unsynchronised access. Advance is deliberately NOT driven concurrently —
// callers (the tick loop) serialize months, and the criterion only demands
// the read path be safe ahead of engine.news/UI wiring.
func TestConcurrentQueriesRaceFree(t *testing.T) {
	cfg := testConfig()
	cfg.BaseArrivalRate = 2.0
	cfg.MaxBoatSize = 3
	api := mustAPI(t, cfg, newFakeShore(oneCell, CellCoord{X: 2, Y: 2}))

	// Populate some state (serially).
	for m := int64(0); m < 4; m++ {
		if _, err := api.Advance(m); err != nil {
			t.Fatalf("Advance(%d): %v", m, err)
		}
	}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_ = api.Arrivals()
				_ = api.ArrivalCount()
				_ = api.Backlog()
				_ = api.ProcessingOpex()
				_ = api.IntegrationOpex()
				_ = api.HotelCost()
				_ = api.DepartureCost()
				_ = api.SatisfactionFriction()
				_ = api.TotalCost()
				_ = api.ProcessingFunding()
				_ = api.HousingApproach()
				_ = api.IntegrationInvestment()
				_ = api.IntegrationSpeed()
				if _, err := api.CaseStage(CaseID(1)); err != nil {
					// Case 1 exists after Advance; a race would panic, not error.
					continue
				}
				_, _ = api.Case(CaseID(1))
			}
		}()
	}
	wg.Wait()
}
