package leisure

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
)

// TestTasteWeightedAllocation proves AC-3's first half: hours are allocated
// across venue categories in proportion to each citizen's OWN taste weights
// (personality-derived), not a population average. Two citizens identical
// except for novelty-seeking allocate differently, in the direction the
// taste weight implies (novelty-seeking → gaming).
func TestTasteWeightedAllocation(t *testing.T) {
	a, c, _, _ := newWiredAPI(t, 1)

	var hi [citizens.NumPersonalityAxes]int32
	hi[citizens.AxisNovelty] = 100
	var lo [citizens.NumPersonalityAxes]int32
	lo[citizens.AxisNovelty] = 0

	seedCitizen(t, c, 1, 0, hi, citizens.EmploymentEmployed)
	seedCitizen(t, c, 2, 0, lo, citizens.EmploymentEmployed)

	hiHours, err := a.VenueHours(1, "test")
	if err != nil {
		t.Fatalf("high-novelty: %v", err)
	}
	loHours, err := a.VenueHours(2, "test")
	if err != nil {
		t.Fatalf("low-novelty: %v", err)
	}

	// Novelty-seeking maps to the gaming category (§5.1), so the high-novelty
	// citizen must allocate strictly more gaming hours.
	if hiHours[CategoryGaming] <= loHours[CategoryGaming] {
		t.Fatalf("high-novelty gaming hours (%v) must exceed low-novelty (%v)",
			hiHours[CategoryGaming], loHours[CategoryGaming])
	}
}

// TestAccessTimeAllocation proves AC-3's second half: reducing simulated
// access-time capacity to a venue category strictly decreases the hours
// allocated to that category.
func TestAccessTimeAllocation(t *testing.T) {
	a, c, tr, _ := newWiredAPI(t, 1)

	var p [citizens.NumPersonalityAxes]int32
	p[citizens.AxisNovelty] = 100 // gaming weight high → nonzero gaming hours
	seedCitizen(t, c, 1, 0, p, citizens.EmploymentEmployed)

	before, err := a.VenueHours(1, "test")
	if err != nil {
		t.Fatalf("before: %v", err)
	}
	if before[CategoryGaming] <= 0 {
		t.Fatalf("fixture must allocate nonzero gaming hours, got %v", before[CategoryGaming])
	}

	// Full block: access time at the budget ceiling zeroes the access factor.
	tr.setAccess(1, CategoryGaming, 90)
	after, err := a.VenueHours(1, "test")
	if err != nil {
		t.Fatalf("after: %v", err)
	}

	if after[CategoryGaming] >= before[CategoryGaming] {
		t.Fatalf("access-time penalty must decrease gaming hours: %v → %v",
			before[CategoryGaming], after[CategoryGaming])
	}
}

// TestPatronagePropagatesCommuteError proves Patronage and DiscretionaryHours
// share the same commute-error policy: a CommuteHours error is propagated,
// never silently zeroed into a fabricated commute=0.
func TestPatronagePropagatesCommuteError(t *testing.T) {
	a, c, tr, _ := newWiredAPI(t, 1)

	var p [citizens.NumPersonalityAxes]int32
	p[citizens.AxisSociability] = 100
	seedCitizen(t, c, 1, 0, p, citizens.EmploymentEmployed)

	sentinel := errors.New("traffic commute unavailable")
	tr.commuteErr = sentinel

	if _, err := a.Patronage(1, "test"); !errors.Is(err, sentinel) {
		t.Fatalf("Patronage must propagate the CommuteHours error, got %v", err)
	}
	if _, err := a.DiscretionaryHours(1, "test"); !errors.Is(err, sentinel) {
		t.Fatalf("DiscretionaryHours must propagate the CommuteHours error, got %v", err)
	}
}
