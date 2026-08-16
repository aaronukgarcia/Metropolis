package leisure

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
)

// buildFixture builds a deterministic fixture city (citizens, venues, an
// event, visits, a month advance) over the given seed.
func buildFixture(t *testing.T, seed uint64) *LeisureAPI {
	t.Helper()
	a, c, tr, _ := newWiredAPI(t, seed)

	var p1 [citizens.NumPersonalityAxes]int32
	p1[citizens.AxisNovelty] = 100
	p1[citizens.AxisSociability] = 80
	var p2 [citizens.NumPersonalityAxes]int32
	p2[citizens.AxisPhysicality] = 90
	seedCitizen(t, c, 1, 0, p1, citizens.EmploymentEmployed)
	seedCitizen(t, c, 2, 0, p2, citizens.EmploymentEmployed)

	if err := a.OpenVenue(Venue{ID: 1, Category: CategoryGaming, District: 1, Capacity: 500}, "test"); err != nil {
		t.Fatalf("open venue 1: %v", err)
	}
	if err := a.OpenVenue(Venue{ID: 2, Category: CategoryDining, District: 1, Capacity: 700}, "test"); err != nil {
		t.Fatalf("open venue 2: %v", err)
	}
	if err := a.OpenVenue(Venue{ID: 3, Category: CategorySport, District: 2, Capacity: 300}, "test"); err != nil {
		t.Fatalf("open venue 3: %v", err)
	}

	tr.commute[1] = 6
	tr.commute[2] = 11

	for i := 0; i < 3; i++ {
		if err := a.Visit(1, 1, "test"); err != nil {
			t.Fatalf("visit: %v", err)
		}
	}
	if err := a.ScheduleEvent(Event{ID: 7, Kind: EventConcert, District: 1, Day: 12, VenueID: 2}, "test"); err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if err := a.AdvanceMonth("test"); err != nil {
		t.Fatalf("advance: %v", err)
	}
	var d TasteDistribution
	d[CategoryGaming] = 70
	d[CategoryDining] = 40
	if err := a.SetPopulationTaste(d, "test"); err != nil {
		t.Fatalf("set population taste: %v", err)
	}
	return a
}

// fingerprint encodes the module's full observable output (patronage,
// allocation, budget, freshness, novelty, leisure-fit, unmet demand, venue
// mix, aggregate) as a fixed-width binary string.
func fingerprint(t *testing.T, a *LeisureAPI) []byte {
	t.Helper()
	var buf bytes.Buffer
	put := func(f float64) {
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], math.Float64bits(f))
		buf.Write(b[:])
	}
	putU := func(u uint64) {
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], u)
		buf.Write(b[:])
	}

	for _, id := range []uint64{1, 2} {
		p, err := a.Patronage(id, "test")
		if err != nil {
			t.Fatalf("patronage %d: %v", id, err)
		}
		put(p.Budget.Discretionary)
		put(p.Budget.LeisureHours)
		put(p.Budget.RestHours)
		put(p.Budget.OvertimeWage)
		for c := 0; c < NumCategories; c++ {
			put(p.Hours[c])
			put(p.Probability[c])
		}
		putU(p.DrawnVenue)

		f, err := a.LeisureFit(id, "test")
		if err != nil {
			t.Fatalf("leisure fit %d: %v", id, err)
		}
		put(f)

		pr, err := a.PatronageProbability(id, 1, "test")
		if err != nil {
			t.Fatalf("patronage probability %d: %v", id, err)
		}
		put(pr)

		fr, err := a.Freshness(id, 1, "test")
		if err != nil {
			t.Fatalf("freshness %d: %v", id, err)
		}
		put(fr)
	}

	um, err := a.UnmetTasteDemand(0, "test")
	if err != nil {
		t.Fatalf("unmet: %v", err)
	}
	for c := 0; c < NumCategories; c++ {
		put(um.Category[c])
	}
	vm, err := a.VenueMix(0, "test")
	if err != nil {
		t.Fatalf("venue mix: %v", err)
	}
	for c := 0; c < NumCategories; c++ {
		put(vm[c])
	}
	var agg TasteDistribution
	agg[CategoryGaming] = 60
	agg[CategoryDining] = 30
	f, err := a.LeisureFitAggregate(agg, "test")
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	put(f)

	return buf.Bytes()
}

// TestDeterminism proves AC-14: the same fixture run twice with the same
// seed produces byte-identical patronage, novelty-decay, and unmet-demand
// output (no wall-clock, no unseeded randomness).
func TestDeterminism(t *testing.T) {
	a1 := buildFixture(t, 42)
	a2 := buildFixture(t, 42)

	f1 := fingerprint(t, a1)
	f2 := fingerprint(t, a2)

	if !bytes.Equal(f1, f2) {
		t.Fatalf("determinism failure: identical seed produced different output (%d vs %d bytes)",
			len(f1), len(f2))
	}
}

// TestVenueMixSummationDeterminism proves GR#21 for the venue-supply sum: the
// per-category capacity sum must be byte-identical across repeated calls even
// when float64 addition would round differently in map-iteration order. The
// probe is 9 same-category venues — capacity 2^53+2 plus eight 1s — which the
// Destructive reviewer observed returning 3 distinct float64 bit patterns
// before the venue-key sort.
func TestVenueMixSummationDeterminism(t *testing.T) {
	a, err := New(testConfig(), 1, "test")
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	// 2^53+2 is the smallest float64-scale capacity whose +1 neighbours round
	// to even, so summing the eight 1s in a different order lands on a
	// different representable result.
	const bigCapacity int64 = 1<<53 + 2
	for id := uint64(1); id <= 9; id++ {
		capacity := int64(1)
		if id == 1 {
			capacity = bigCapacity
		}
		if err := a.OpenVenue(Venue{ID: id, Category: CategoryDining, District: 0, Capacity: capacity}, "test"); err != nil {
			t.Fatalf("open venue %d: %v", id, err)
		}
	}

	base, err := a.VenueMix(0, "test")
	if err != nil {
		t.Fatalf("venue mix: %v", err)
	}

	const calls = 2000
	for i := 0; i < calls; i++ {
		got, err := a.VenueMix(0, "test")
		if err != nil {
			t.Fatalf("venue mix call %d: %v", i, err)
		}
		for c := 0; c < NumCategories; c++ {
			if math.Float64bits(got[c]) != math.Float64bits(base[c]) {
				t.Fatalf("VenueMix(0) non-deterministic: call %d category %d = %v (0x%016x), first call = %v (0x%016x)",
					i, c, got[c], math.Float64bits(got[c]), base[c], math.Float64bits(base[c]))
			}
		}
	}
}
