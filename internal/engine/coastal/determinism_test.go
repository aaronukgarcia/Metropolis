package coastal

import (
	"reflect"
	"sort"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
)

// TestDeterminism (AC-15): the same world seed with the same Advance sequence
// produces byte-identical arrival-event sequences and pipeline outcomes —
// every draw uses det.NewStream (hash(worldSeed, i, m, purpose)), never an
// unseeded PRNG or the wall clock.
func TestDeterminism(t *testing.T) {
	run := func() (*CoastalAPI, int) {
		cit, err := citizens.NewCitizensAPI(7, "corr-test")
		if err != nil {
			t.Fatalf("NewCitizensAPI: %v", err)
		}
		cfg := testConfig()
		cfg.BaseArrivalRate = 2.0
		cfg.MaxBoatSize = 5
		cfg.Pipeline.GrantRate = 0.5
		cfg.Pipeline.MinMonths = 2
		cfg.Pipeline.MaxMonths = 2

		api := mustAPI(t, cfg, newFakeShore(oneCell, CellCoord{X: 2, Y: 2}, CellCoord{X: 3, Y: 3}))
		if err := api.SetCitizens(cit); err != nil {
			t.Fatalf("SetCitizens: %v", err)
		}
		for m := int64(0); m < 8; m++ {
			if _, err := api.Advance(m); err != nil {
				t.Fatalf("Advance(%d): %v", m, err)
			}
		}
		return api, cit.TotalPopulation("corr-test")
	}

	a, popA := run()
	b, popB := run()

	if !reflect.DeepEqual(a.Arrivals(), b.Arrivals()) {
		t.Fatalf("arrival-event sequences diverged for the same seed")
	}
	if popA != popB {
		t.Fatalf("granted population diverged for the same seed: %d vs %d", popA, popB)
	}
	if a.HotelCost() != b.HotelCost() || a.DepartureCost() != b.DepartureCost() {
		t.Fatalf("cost ledger diverged for the same seed")
	}

	// Per-case outcomes identical (sorted for a deterministic comparison).
	compareCases(t, a, b)
}

// compareCases asserts two APIs' case sets are byte-identical, comparing in
// sorted case-ID order (GR#21).
func compareCases(t *testing.T, a, b *CoastalAPI) {
	t.Helper()
	aIDs := sortedCaseIDs(a)
	bIDs := sortedCaseIDs(b)
	if len(aIDs) != len(bIDs) {
		t.Fatalf("case count diverged: %d vs %d", len(aIDs), len(bIDs))
	}
	for i, id := range aIDs {
		ak, err := a.Case(id)
		if err != nil {
			t.Fatalf("Case(%d) on a: %v", id, err)
		}
		bk, err := b.Case(id)
		if err != nil {
			t.Fatalf("Case(%d) on b: %v", id, err)
		}
		if ak != bk {
			t.Fatalf("case %d diverged: %+v vs %+v", id, ak, bk)
		}
		if aIDs[i] != bIDs[i] {
			t.Fatalf("case order diverged at index %d", i)
		}
	}
}

func sortedCaseIDs(c *CoastalAPI) []CaseID {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ids := make([]CaseID, 0, len(c.cases))
	for id := range c.cases {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
