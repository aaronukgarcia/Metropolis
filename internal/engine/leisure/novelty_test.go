package leisure

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
)

// TestNoveltyDecay proves AC-4: a per-citizen, per-venue freshness value
// decreases with repeated visits, faster for a novelty-seeking citizen, and
// the decayed freshness reduces that citizen's patronage probability. Two
// otherwise-identical citizens (same dining taste) are compared over the same
// visit sequence; the high-novelty citizen's probability strictly decreases
// while the low-novelty citizen's does not (testConfig has zero base decay).
func TestNoveltyDecay(t *testing.T) {
	a, c, _, _ := newWiredAPI(t, 1)

	if err := a.OpenVenue(Venue{ID: 100, Category: CategoryDining, District: 1, Capacity: 1000}, "test"); err != nil {
		t.Fatalf("open venue: %v", err)
	}

	var hi [citizens.NumPersonalityAxes]int32
	hi[citizens.AxisSociability] = 100
	hi[citizens.AxisNovelty] = 100
	var lo [citizens.NumPersonalityAxes]int32
	lo[citizens.AxisSociability] = 100
	lo[citizens.AxisNovelty] = 0

	seedCitizen(t, c, 1, 0, hi, citizens.EmploymentEmployed)
	seedCitizen(t, c, 2, 0, lo, citizens.EmploymentEmployed)

	hiBefore, err := a.PatronageProbability(1, 100, "test")
	if err != nil {
		t.Fatalf("hi before: %v", err)
	}
	loBefore, err := a.PatronageProbability(2, 100, "test")
	if err != nil {
		t.Fatalf("lo before: %v", err)
	}

	for i := 0; i < 5; i++ {
		if err := a.Visit(1, 100, "test"); err != nil {
			t.Fatalf("visit hi: %v", err)
		}
		if err := a.Visit(2, 100, "test"); err != nil {
			t.Fatalf("visit lo: %v", err)
		}
	}

	hiAfter, err := a.PatronageProbability(1, 100, "test")
	if err != nil {
		t.Fatalf("hi after: %v", err)
	}
	loAfter, err := a.PatronageProbability(2, 100, "test")
	if err != nil {
		t.Fatalf("lo after: %v", err)
	}

	if hiAfter >= hiBefore {
		t.Fatalf("high-novelty probability must strictly decrease: %v → %v", hiBefore, hiAfter)
	}
	if loAfter != loBefore {
		t.Fatalf("low-novelty probability must not decrease: %v → %v", loBefore, loAfter)
	}
}

// TestRefurbish proves AC-5: refurbishing a venue resets the freshness value
// novelty decay had reduced, for citizens whose taste matches the venue's
// category — and not for a non-matching citizen.
func TestRefurbish(t *testing.T) {
	a, c, _, _ := newWiredAPI(t, 1)

	if err := a.OpenVenue(Venue{ID: 100, Category: CategoryDining, District: 1, Capacity: 1000}, "test"); err != nil {
		t.Fatalf("open venue: %v", err)
	}

	// Matching citizen: dining taste (sociability=100) ≥ threshold, AND
	// novelty=100 so it actually decays.
	var match [citizens.NumPersonalityAxes]int32
	match[citizens.AxisSociability] = 100
	match[citizens.AxisNovelty] = 100
	// Non-matching citizen: dining taste 0 (< threshold), but decays too.
	var nonMatch [citizens.NumPersonalityAxes]int32
	nonMatch[citizens.AxisNovelty] = 100

	seedCitizen(t, c, 1, 0, match, citizens.EmploymentEmployed)
	seedCitizen(t, c, 2, 0, nonMatch, citizens.EmploymentEmployed)

	for i := 0; i < 8; i++ {
		if err := a.Visit(1, 100, "test"); err != nil {
			t.Fatalf("visit match: %v", err)
		}
		if err := a.Visit(2, 100, "test"); err != nil {
			t.Fatalf("visit non-match: %v", err)
		}
	}

	matchBefore, err := a.Freshness(1, 100, "test")
	if err != nil {
		t.Fatalf("match before: %v", err)
	}
	nonMatchBefore, err := a.Freshness(2, 100, "test")
	if err != nil {
		t.Fatalf("non-match before: %v", err)
	}
	if matchBefore >= 1.0 {
		t.Fatalf("fixture must have decayed the matching citizen, got %v", matchBefore)
	}

	if err := a.RefurbishVenue(100, "test"); err != nil {
		t.Fatalf("refurbish: %v", err)
	}

	matchAfter, err := a.Freshness(1, 100, "test")
	if err != nil {
		t.Fatalf("match after: %v", err)
	}
	nonMatchAfter, err := a.Freshness(2, 100, "test")
	if err != nil {
		t.Fatalf("non-match after: %v", err)
	}

	if matchAfter <= matchBefore {
		t.Fatalf("matching citizen freshness must rise after refurbish: %v → %v", matchBefore, matchAfter)
	}
	if nonMatchAfter != nonMatchBefore {
		t.Fatalf("non-matching citizen freshness must be unaffected: %v → %v", nonMatchBefore, nonMatchAfter)
	}
}
