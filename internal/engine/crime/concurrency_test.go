package crime

import (
	"sync"
	"testing"
)

// AC-18 (SG-7 scoped; GR#21): a *CrimeAPI is safe for concurrent per-district
// generation reads within a tick. Every accessor takes a read lock, so many
// goroutines can query generation/safety/justice figures concurrently with
// no data race (run with -race).
func TestConcurrentPerDistrictGeneration(t *testing.T) {
	a := testAPI(t)
	for m := int64(0); m < 3; m++ {
		advance(t, a, m,
			defaultDistrict(1),
			defaultDistrict(2),
			defaultDistrict(3),
			defaultDistrict(4),
		)
	}

	const workers = 16
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for w := 0; w < workers; w++ {
		worker := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := DistrictID(worker%4 + 1)
			for i := 0; i < 200; i++ {
				for _, ty := range crimeTypeKeys {
					if _, err := a.Generation(id, ty); err != nil {
						errs <- err
						return
					}
				}
				if _, err := a.SafetyTerm(id); err != nil {
					errs <- err
					return
				}
				if _, err := a.EligiblePool(id); err != nil {
					errs <- err
					return
				}
			}
			a.ThreatLevel()
			a.GangIDs()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent accessor error: %v", err)
	}
}
