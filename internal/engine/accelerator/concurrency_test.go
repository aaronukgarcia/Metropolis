package accelerator

import (
	"sync"
	"testing"
)

// TestConcurrentReadsDuringTickAreRaceFree is AC-16: the draw/spillover
// surface is read concurrently with a tick applying the draw, with no data
// race. Readers hit ResolvedDemand/PeakDemand/Prestige/ResearchMultiplier/
// HealthSpillover/IsBuilt while a writer runs Operate; every mutable field is
// guarded by mu, so `go test -race` reports no race.
func TestConcurrentReadsDuringTickAreRaceFree(t *testing.T) {
	a, u := loadTestAPI(t)
	wireAll(t, a, u, a.ExpertGateThreshold())
	if err := a.Build(BuildCommand{Key: CatalogueKey}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	const readers = 8
	var wg sync.WaitGroup

	// Writer: apply ticks.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for tick := int64(1); tick <= 200; tick++ {
			_ = a.Operate(tick)
		}
	}()

	// Readers: query the draw/spillover/prestige surface concurrently.
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_, _ = a.ResolvedDemand(demandOptions())
				_, _ = a.PeakDemand(demandOptions())
				_ = a.Prestige()
				_ = a.ResearchMultiplier()
				_ = a.HealthSpillover()
				_ = a.FdiAnchorDraw()
				_ = a.IsBuilt()
				_ = a.IsOnline()
			}
		}()
	}

	wg.Wait()
}
