package wellbeing

import (
	"sync"
	"testing"
)

// TestConcurrentAttributionIsRaceFree (AC-17) drives the pure engine and the
// gather path from many goroutines at once, against a single *WellbeingAPI,
// proving the immutable config and the source-pointer snapshotting are
// race-free under `go test -race`.
func TestConcurrentAttributionIsRaceFree(t *testing.T) {
	api := newTestAPI(t)
	if err := api.SetSeason(loadSeasonFixture(t, "[0.05,0.05,0.02,0,0,0,0,0,0,0,0.02,0.05]")); err != nil {
		t.Fatalf("SetSeason: %v", err)
	}
	_ = api.SetShopping(fakeShopping{share: 0.6, ok: true})
	_ = api.SetTraffic(fakeTraffic{commute: 35, active: 0.4, ok: true})
	_ = api.SetHealthcare(fakeHealthcare{access: 0.7, ok: true})
	_ = api.SetNeighbourhood(fakeNeighbourhood{green: 0.5, noise: 0.2, ok: true})
	_ = api.SetPollution(fakePollution{level: 0.3, ok: true})

	var wg sync.WaitGroup
	errCh := make(chan error, 128)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(id uint64) {
			defer wg.Done()

			in := neutralInputs()
			in.CommuteMinutes = float64(id % 90)
			if _, err := api.Attribute(id, 12, in); err != nil {
				errCh <- err
				return
			}
			if _, err := api.AttributeCitizen(testCitizen(), 0, neutralContext()); err != nil {
				errCh <- err
			}
			_ = api.Wellbeing(50, 50, 50)
			_ = api.MortalityModifier(40, 40)
		}(uint64(i + 1))
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent error: %v", err)
	}
}

// TestConcurrentReconstructNoStoredState proves reconstruction carries no
// stored intermediate between concurrent callers (AC-18): the same inputs
// yield the same result regardless of what another goroutine computed first.
func TestConcurrentReconstructNoStoredState(t *testing.T) {
	api := newTestAPI(t)
	in := neutralInputs()
	in.CommuteMinutes = 42

	first, err := api.Attribute(7, 12, in)
	if err != nil {
		t.Fatalf("Attribute: %v", err)
	}

	var wg sync.WaitGroup
	results := make([]TrackAttribution, 32)
	for i := range results {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], _ = api.Attribute(7, 12, in)
		}(i)
	}
	wg.Wait()

	for i, r := range results {
		if r != first {
			t.Errorf("goroutine %d reconstructed a different attribution", i)
		}
	}
}
