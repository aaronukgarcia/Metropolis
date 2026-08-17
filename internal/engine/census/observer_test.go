package census

import (
	"reflect"
	"sync"
	"testing"
)

// fakeStateSnapshot is a deep copy of every consumed fake's state, used to
// prove the observer threads never mutate the consumed modules (AC-2).
type fakeStateSnapshot struct {
	Citizens    map[uint64]CitizenView
	Education   map[uint64]EducationView
	CrimeRate   float64
	Happiness   float64
	Unfed       float64
	Hospital    int64
	Unfilled    int64
	SkillDemand int64
	PolicyCoef  float64
	Income      map[uint64]int64
	GDP         int64
	Land        int64
}

func snapshotFakeState(w wired) fakeStateSnapshot {
	w.citizens.mu.RLock()
	citizens := make(map[uint64]CitizenView, len(w.citizens.views))
	for k, v := range w.citizens.views {
		citizens[k] = v
	}
	w.citizens.mu.RUnlock()

	w.education.mu.RLock()
	education := make(map[uint64]EducationView, len(w.education.m))
	for k, v := range w.education.m {
		education[k] = v
	}
	w.education.mu.RUnlock()

	w.crime.mu.RLock()
	crime := w.crime.rate
	w.crime.mu.RUnlock()

	w.wellbeing.mu.RLock()
	happiness := w.wellbeing.happiness
	unfed := w.wellbeing.unfed
	w.wellbeing.mu.RUnlock()

	w.services.mu.RLock()
	hospital := w.services.hospital
	unfilled := w.services.unfilled
	demand := w.services.skillDemand
	w.services.mu.RUnlock()

	w.policies.mu.RLock()
	coef := w.policies.coef
	w.policies.mu.RUnlock()

	w.finance.mu.RLock()
	income := make(map[uint64]int64, len(w.finance.income))
	for k, v := range w.finance.income {
		income[k] = v
	}
	gdp := w.finance.gdp
	land := w.finance.land
	w.finance.mu.RUnlock()

	return fakeStateSnapshot{
		Citizens:    citizens,
		Education:   education,
		CrimeRate:   crime,
		Happiness:   happiness,
		Unfed:       unfed,
		Hospital:    hospital,
		Unfilled:    unfilled,
		SkillDemand: demand,
		PolicyCoef:  coef,
		Income:      income,
		GDP:         gdp,
		Land:        land,
	}
}

// TestObserverNoMutateConsumedState proves the four threads are observers:
// running them over a given tick leaves the consumed modules' state
// byte-identical (AC-2, GR#21).
func TestObserverNoMutateConsumedState(t *testing.T) {
	c := newTestCensus(t)
	w := wire(t, c)

	w.citizens.set(mkCitizen(1))
	w.citizens.set(mkCitizen(2))
	w.education.set(1, EducationView{Attainment: 50})
	w.education.set(2, EducationView{Attainment: 90})
	w.finance.setIncome(1, 100_000_000)
	w.finance.setIncome(2, 200_000_000)
	w.crime.setRate(0.01)
	w.wellbeing.happiness = 72
	w.wellbeing.unfed = 0.02
	w.services.hospital = 120
	w.services.unfilled = 30
	w.services.skillDemand = 80
	w.policies.coef = 0.1
	w.finance.gdp = 9_000_000_000
	w.finance.land = 4_000_000_000

	before := snapshotFakeState(w)
	if err := c.RunObservers(10, "test"); err != nil {
		t.Fatalf("RunObservers: %v", err)
	}
	after := snapshotFakeState(w)

	if !reflect.DeepEqual(before, after) {
		t.Fatalf("observer threads mutated consumed state\nbefore=%+v\nafter=%+v", before, after)
	}
}

// TestDeterministicThreadsOverIdenticalSnapshot proves the stats generator
// (and the whole observer pipeline) is a deterministic function of the
// snapshot: repeated runs over an identical snapshot produce byte-identical
// output (AC-3/AC-8/AC-23).
func TestDeterministicThreadsOverIdenticalSnapshot(t *testing.T) {
	c := newTestCensus(t)
	w := wire(t, c)
	for i := uint64(1); i <= 20; i++ {
		cv := mkCitizen(i)
		cv.Sex = SexMale
		cv.Sector = SectorTertiary
		w.citizens.set(cv)
		w.education.set(i, EducationView{Attainment: int64(40 + i)})
		w.finance.setIncome(i, int64(i)*1_000_000)
	}
	w.crime.setRate(0.03)
	w.wellbeing.happiness = 60

	snap, err := c.Snapshot(7, "test")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	first := c.Stats(snap)
	for i := 0; i < 50; i++ {
		again := c.Stats(snap)
		if !reflect.DeepEqual(first, again) {
			t.Fatalf("Stats not deterministic: %+v != %+v", first, again)
		}
	}
}

// TestConcurrentObserversRace runs the four threads concurrently with each
// other and with a tick resolving the sources, asserting the snapshot
// discipline holds under -race (AC-25).
func TestConcurrentObserversRace(t *testing.T) {
	c := newTestCensus(t)
	w := wire(t, c)
	for i := uint64(1); i <= 50; i++ {
		cv := mkCitizen(i)
		w.citizens.set(cv)
		w.education.set(i, EducationView{Attainment: 55})
		w.finance.setIncome(i, 100_000_000)
	}
	w.crime.setRate(0.01)

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			if err := c.RunObservers(int64(g), "test"); err != nil {
				t.Errorf("RunObservers(%d): %v", g, err)
			}
			_ = c.LatestAggregates()
			_, _ = c.HistoryAt(0)
			_ = c.TrackedObjects()
			_, _ = c.CheckIn(citizenGUID(1))
			_ = c.Findings()
		}(g)
	}
	wg.Wait()
}
