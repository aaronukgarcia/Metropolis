package citizens

import (
	"sync"
	"testing"
)

// TestConcurrentColdPassAndHotUpdates (AC-21): the amortised cold-pass
// batch and hot-tier daily updates (fidelity + life-event commands) run
// concurrently under `go test -race` with no data race — the API's mutex
// serializes the two writers, and each shard is touched by exactly one
// worker inside a pass.
func TestConcurrentColdPassAndHotUpdates(t *testing.T) {
	api, err := NewCitizensAPI(17, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	api.workers = 8
	records := make([]ColdRecord, 500)
	for i := range records {
		records[i] = mkRecord(uint64(i+1), uint16(i%10))
		records[i].BirthMonth = 0
	}
	if err := api.SeedColdRecords(records, "corr"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	for id := uint64(1); id <= 20; id++ {
		if err := api.ApplyFidelityCommand(FidelityCommand{CorrelationID: "corr", CitizenID: id, Target: FidelityHot}); err != nil {
			t.Fatalf("promote %d: %v", id, err)
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 3; i++ {
			_ = api.AdvanceMonth("corr") // amortised cold pass (the batch path)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			id := uint64(i%20 + 1)
			_ = api.ApplyFidelityCommand(FidelityCommand{CorrelationID: "corr", CitizenID: id, Target: FidelityWarm})
			_ = api.ApplyLifeEventCommand(LifeEventCommand{CorrelationID: "corr", Kind: LifeEventHealth, CitizenID: id, HealthBand: HealthBand(i % 6)})
		}
	}()
	wg.Wait()
}
