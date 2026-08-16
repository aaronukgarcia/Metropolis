package leisure

import (
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
)

// TestConcurrentQueries proves AC-15: LeisureAPI is safe for concurrent
// querying by the two registered consumers (engine.tourism and
// ui.screen.demo) simultaneously. Run with -race.
func TestConcurrentQueries(t *testing.T) {
	a, c, tr, _ := newWiredAPI(t, 1)

	var p [citizens.NumPersonalityAxes]int32
	p[citizens.AxisNovelty] = 100
	seedCitizen(t, c, 1, 0, p, citizens.EmploymentEmployed)
	seedCitizen(t, c, 2, 0, p, citizens.EmploymentEmployed)
	tr.commute[1] = 5
	tr.commute[2] = 9
	if err := a.OpenVenue(Venue{ID: 1, Category: CategoryGaming, District: 1, Capacity: 500}, "test"); err != nil {
		t.Fatalf("open venue: %v", err)
	}
	var d TasteDistribution
	d[CategoryGaming] = 80
	if err := a.SetPopulationTaste(d, "test"); err != nil {
		t.Fatalf("set population taste: %v", err)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 64)

	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := a.Patronage(1, "test"); err != nil {
				errCh <- err
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := a.UnmetTasteDemand(0, "test"); err != nil {
				errCh <- err
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := a.LeisureFit(2, "test"); err != nil {
				errCh <- err
			}
		}()
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent query error: %v", err)
	}
}
