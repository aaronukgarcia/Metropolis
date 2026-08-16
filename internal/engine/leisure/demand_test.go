package leisure

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
)

// TestUnmetTasteDemand proves AC-7: the unmet-taste-demand signal is
// decomposed per category, and removing all venues of one category raises
// that category's figure while an unaffected category's figure does not.
func TestUnmetTasteDemand(t *testing.T) {
	a, _, _, _ := newWiredAPI(t, 1)

	var d TasteDistribution
	d[CategorySport] = 80
	d[CategoryDining] = 50
	if err := a.SetPopulationTaste(d, "test"); err != nil {
		t.Fatalf("set population taste: %v", err)
	}

	if err := a.OpenVenue(Venue{ID: 1, Category: CategorySport, District: 1, Capacity: 10}, "test"); err != nil {
		t.Fatalf("open sport venue: %v", err)
	}
	if err := a.OpenVenue(Venue{ID: 2, Category: CategoryDining, District: 1, Capacity: 30}, "test"); err != nil {
		t.Fatalf("open dining venue: %v", err)
	}

	before, err := a.UnmetTasteDemand(0, "test")
	if err != nil {
		t.Fatalf("before: %v", err)
	}

	if err := a.RemoveVenue(1, "test"); err != nil { // remove all sport venues
		t.Fatalf("remove venue: %v", err)
	}
	after, err := a.UnmetTasteDemand(0, "test")
	if err != nil {
		t.Fatalf("after: %v", err)
	}

	if after.Category[CategorySport] <= before.Category[CategorySport] {
		t.Fatalf("sport unmet demand must rise after removing sport venues: %v → %v",
			before.Category[CategorySport], after.Category[CategorySport])
	}
	if after.Category[CategoryDining] != before.Category[CategoryDining] {
		t.Fatalf("dining unmet demand must be unaffected: %v → %v",
			before.Category[CategoryDining], after.Category[CategoryDining])
	}
}

// TestLeisureFitAggregate proves AC-9: the citywide leisureFit aggregate is a
// distinct accessor taking a personality-distribution parameter, and two
// different migrant distributions yield different values.
func TestLeisureFitAggregate(t *testing.T) {
	a, _, _, _ := newWiredAPI(t, 1)

	if err := a.OpenVenue(Venue{ID: 1, Category: CategorySport, District: 1, Capacity: 1000}, "test"); err != nil {
		t.Fatalf("open sport venue: %v", err)
	}

	var sporty TasteDistribution
	sporty[CategorySport] = 1.0
	var foody TasteDistribution
	foody[CategoryDining] = 1.0

	fSport, err := a.LeisureFitAggregate(sporty, "test")
	if err != nil {
		t.Fatalf("sporty: %v", err)
	}
	fFood, err := a.LeisureFitAggregate(foody, "test")
	if err != nil {
		t.Fatalf("foody: %v", err)
	}

	// A sport-only venue mix fits a sport-seeking migrant distribution better
	// than a dining-seeking one.
	if fSport == fFood {
		t.Fatalf("distinct migrant distributions must yield distinct leisureFit values, both %v", fSport)
	}
	if fSport <= fFood {
		t.Fatalf("sport-only venue mix should fit sporty migrants better: sport=%v food=%v", fSport, fFood)
	}
}

// TestLeisureFitPush proves AC-10: per-citizen leisure-fit is pushed through
// the engine.wellbeing edge, and a citizen with zero matching venues in range
// yields a lower value than the same citizen with several matching venues.
func TestLeisureFitPush(t *testing.T) {
	a, c, _, wb := newWiredAPI(t, 1)

	var sporty [citizens.NumPersonalityAxes]int32
	sporty[citizens.AxisPhysicality] = 100 // sport taste high
	seedCitizen(t, c, 1, 0, sporty, citizens.EmploymentEmployed)

	// No sport venues — only dining.
	if err := a.OpenVenue(Venue{ID: 1, Category: CategoryDining, District: 1, Capacity: 1000}, "test"); err != nil {
		t.Fatalf("open dining venue: %v", err)
	}
	low, err := a.LeisureFit(1, "test")
	if err != nil {
		t.Fatalf("low fit: %v", err)
	}

	// Add matching sport venues.
	if err := a.OpenVenue(Venue{ID: 2, Category: CategorySport, District: 1, Capacity: 1000}, "test"); err != nil {
		t.Fatalf("open sport venue: %v", err)
	}
	high, err := a.LeisureFit(1, "test")
	if err != nil {
		t.Fatalf("high fit: %v", err)
	}

	if high <= low {
		t.Fatalf("matching venues must raise leisure-fit: %v → %v", low, high)
	}

	// Both fits were pushed to wellbeing with the values returned.
	if wb.count() < 2 {
		t.Fatalf("leisure-fit was not pushed to wellbeing (count=%d)", wb.count())
	}
	if got, ok := wb.fitFor(1); !ok || got != high {
		t.Fatalf("pushed leisure-fit = %v (ok=%v), want %v", got, ok, high)
	}
}

// TestUnknownCitizenAndInvalidDistrict proves AC-11: querying a nonexistent
// citizen or district returns a registry-sourced error (never a silently
// zero-valued record), and the returned error's code matches.
func TestUnknownCitizenAndInvalidDistrict(t *testing.T) {
	a, _, _, _ := newWiredAPI(t, 1)

	if err := a.OpenVenue(Venue{ID: 1, Category: CategoryDining, District: 1, Capacity: 1000}, "test"); err != nil {
		t.Fatalf("open venue: %v", err)
	}

	// Unknown citizen across the query surface.
	if _, err := a.Patronage(999, "test"); err == nil {
		t.Fatal("Patronage must error for an unknown citizen")
	} else {
		assertErrCode(t, err, ErrUnknownCitizen)
	}
	if _, err := a.LeisureFit(999, "test"); err == nil {
		t.Fatal("LeisureFit must error for an unknown citizen")
	} else {
		assertErrCode(t, err, ErrUnknownCitizen)
	}
	if _, err := a.DiscretionaryHours(999, "test"); err == nil {
		t.Fatal("DiscretionaryHours must error for an unknown citizen")
	} else {
		assertErrCode(t, err, ErrUnknownCitizen)
	}
	if _, err := a.Freshness(999, 1, "test"); err == nil {
		t.Fatal("Freshness must error for an unknown citizen")
	} else {
		assertErrCode(t, err, ErrUnknownCitizen)
	}

	// Unknown district across the district-query surface (district 7 has no venues).
	if _, err := a.UnmetTasteDemand(7, "test"); err == nil {
		t.Fatal("UnmetTasteDemand must error for an unknown district")
	} else {
		assertErrCode(t, err, ErrUnknownDistrict)
	}
	if _, err := a.VenueMix(7, "test"); err == nil {
		t.Fatal("VenueMix must error for an unknown district")
	} else {
		assertErrCode(t, err, ErrUnknownDistrict)
	}
}
