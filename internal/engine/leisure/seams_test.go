package leisure

import (
	"math"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/wellbeing"
)

// testWellbeingFile returns a minimal schema-valid wellbeing file with a
// non-zero LeisureFit weight — enough for the real engine.wellbeing to run
// the AC-10 LeisureFit driver path without loading a data file (mirroring
// engine.social's test fixture).
func testWellbeingFile() wellbeing.WellbeingFile {
	return wellbeing.WellbeingFile{
		Version:  1,
		Baseline: wellbeing.BaselineFile{Physical: 50, Mental: 50},
		Headline: wellbeing.HeadlineFile{PhysicalWeight: 1, MentalWeight: 1},
		Physical: wellbeing.PhysicalFile{
			AgeCurve: []wellbeing.AgeCurvePoint{
				{AgeYears: 0, Delta: 0}, {AgeYears: 100, Delta: 0},
			},
		},
		Mental: wellbeing.MentalFile{
			CommuteThresholdMinutes:   30,
			CommuteStressAtThreshold:  1,
			CommuteStressAt100Minutes: 2,
			LeisureFitWeight:          10,
			RentBurdenThreshold:       0.35,
			UnemploymentCapMonths:     60,
		},
	}
}

// TestWellbeingSeamAdapterBridgesRealWellbeing proves AC-10 through the
// GR#20 seam: the [WellbeingAPI] seam is bridged to the REAL engine.wellbeing
// via [WellbeingLeisureFitAdapter]. A citizen with zero matching venues in
// range gets a LOWER pushed leisure-fit than an otherwise-identical citizen
// with several matching venues, and the pushed fit is readable back through
// the adapter's ContextInputs.LeisureFit surface along with the real
// LeisureFit driver delta computed by wellbeing's own Attribute engine.
func TestWellbeingSeamAdapterBridgesRealWellbeing(t *testing.T) {
	wb, err := wellbeing.New(testWellbeingFile(), 1, "test")
	if err != nil {
		t.Fatalf("wellbeing.New: %v", err)
	}
	adapter := &WellbeingLeisureFitAdapter{Wellbeing: wb, Month: 0}

	a, c, _, _ := newWiredAPI(t, 1)
	if err := a.SetWellbeing(adapter); err != nil {
		t.Fatalf("SetWellbeing(adapter): %v", err)
	}

	var sporty [citizens.NumPersonalityAxes]int32
	sporty[citizens.AxisPhysicality] = 100 // sport taste high
	seedCitizen(t, c, 1, 0, sporty, citizens.EmploymentEmployed)

	// Zero matching venues in range — only dining.
	if err := a.OpenVenue(Venue{ID: 1, Category: CategoryDining, District: 1, Capacity: 1000}, "test"); err != nil {
		t.Fatalf("open dining venue: %v", err)
	}
	low, err := a.LeisureFit(1, "test")
	if err != nil {
		t.Fatalf("low fit: %v", err)
	}

	// Add several matching sport venues.
	for id := uint64(2); id <= 4; id++ {
		if err := a.OpenVenue(Venue{ID: id, Category: CategorySport, District: 1, Capacity: 1000}, "test"); err != nil {
			t.Fatalf("open sport venue %d: %v", id, err)
		}
	}
	high, err := a.LeisureFit(1, "test")
	if err != nil {
		t.Fatalf("high fit: %v", err)
	}

	if high <= low {
		t.Fatalf("matching venues must raise leisure-fit: %v → %v", low, high)
	}

	// The adapter recorded the LAST push (the high fit) for the citizen, and
	// wellbeing's own Attribute computed a matching LeisureFit driver delta
	// (leisureFitWeight 10 × the fit).
	ctx, delta, ok := adapter.LeisureFitContext(1)
	if !ok {
		t.Fatal("adapter has no recorded leisure-fit push for the citizen")
	}
	if ctx.LeisureFit != high {
		t.Fatalf("adapter ContextInputs.LeisureFit = %v, want the pushed fit %v", ctx.LeisureFit, high)
	}
	if want := 10.0 * high; math.Abs(delta-want) > 1e-12 {
		t.Fatalf("adapter LeisureFit driver delta = %v, want %v (wellbeing's own Attribute)", delta, want)
	}
}
