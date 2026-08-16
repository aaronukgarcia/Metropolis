package leisure

import (
	"errors"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// testConfig returns a Config whose novelty-decay base is zero so a
// zero-novelty citizen does NOT decay (making AC-4's "decreases less or not
// at all" crisp) and whose magnitudes are small round numbers. The real
// magnitudes live in data/leisure.json; these fixture values only make the
// mechanisms fast and unambiguous to exercise (GR#15's test-fixture latitude
// — a validator derives from data, a unit test may inject a fixture).
func testConfig() Config {
	var c Config
	c.HoursPerWeek = 168
	c.Work = [numLifeStages]float64{StageChild: 0, StageStudent: 0, StageEmployed: 40, StageUnemployed: 0, StageRetired: 0}
	c.Education = [numLifeStages]float64{StageChild: 30, StageStudent: 35, StageEmployed: 0, StageUnemployed: 0, StageRetired: 0}
	c.Sleep = [numLifeStages]float64{StageChild: 63, StageStudent: 56, StageEmployed: 56, StageUnemployed: 56, StageRetired: 56}
	c.Chores = [numLifeStages]float64{StageChild: 2, StageStudent: 7, StageEmployed: 10, StageUnemployed: 10, StageRetired: 10}
	c.AccessFreeMinutes = 15
	c.AccessBudgetMinutes = 90
	c.OvertimeWageRate = 1.0
	c.NoveltyDecayBase = 0.0
	c.NoveltyDecayPerNovelty = 0.10
	c.FreshnessRecovery = 1.0
	c.EventCrowd = [numEventKinds]int64{
		EventFestival: 4000, EventFoodFair: 2000, EventMatchDay: 8000,
		EventConcert: 3000, EventChristmasMarket: 5000,
	}
	c.MatchThreshold = 40
	for i := range c.DefaultTaste {
		c.DefaultTaste[i] = 50
	}
	return c
}

// seedCitizen appends one valid cold citizen record with the given birth
// month, personality, and employment state.
func seedCitizen(t *testing.T, c *citizens.CitizensAPI, id uint64, birthMonth int64, p [citizens.NumPersonalityAxes]int32, emp citizens.EmploymentState) {
	t.Helper()
	var p8 [citizens.NumPersonalityAxes]int8
	for i := range p {
		p8[i] = int8(p[i])
	}
	r := citizens.ColdRecord{
		ID:              id,
		BirthMonth:      birthMonth,
		Sex:             citizens.SexFemale,
		Personality:     p8,
		Attainment:      0,
		Stage:           citizens.StageNone,
		HealthBand:      citizens.HealthExcellent,
		SatHousing:      50,
		SatServices:     50,
		SatEnvironment:  50,
		SatLeisureFit:   50,
		SatCommute:      50,
		EmploymentState: emp,
	}
	if err := c.SeedColdRecords([]citizens.ColdRecord{r}, "test"); err != nil {
		t.Fatalf("seed citizen: %v", err)
	}
}

// fakeTraffic is a thread-safe test double for the engine.traffic contract
// shape (AC-2/AC-3/AC-6): configurable per-citizen commute, per-citizen
// per-category access minutes, and recorded trip demand.
type fakeTraffic struct {
	mu         sync.Mutex
	commute    map[uint64]float64
	access     map[uint64]map[Category]float64
	demands    []TripDemand
	commuteErr error // when non-nil, CommuteHours returns this error
}

func newFakeTraffic() *fakeTraffic {
	return &fakeTraffic{commute: make(map[uint64]float64), access: make(map[uint64]map[Category]float64)}
}

func (f *fakeTraffic) CommuteHours(citizenID uint64, _ string) (float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.commuteErr != nil {
		return 0, f.commuteErr
	}
	return f.commute[citizenID], nil
}

func (f *fakeTraffic) AccessMinutes(citizenID uint64, category Category, _ string) (float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m, ok := f.access[citizenID]; ok {
		if v, ok := m[category]; ok {
			return v, nil
		}
	}
	return 15, nil // default: free access
}

func (f *fakeTraffic) AddTripDemand(d TripDemand) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.demands = append(f.demands, d)
	return nil
}

func (f *fakeTraffic) setAccess(citizenID uint64, category Category, minutes float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.access[citizenID] == nil {
		f.access[citizenID] = make(map[Category]float64)
	}
	f.access[citizenID][category] = minutes
}

func (f *fakeTraffic) totalDemand() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	var t int64
	for _, d := range f.demands {
		t += d.Count
	}
	return t
}

// fakeWellbeing is a thread-safe test double for the engine.wellbeing
// LeisureFit-driver contract shape (AC-10): it records every pushed fit.
type fakeWellbeing struct {
	mu     sync.Mutex
	pushes []struct {
		citizenID uint64
		fit       float64
	}
}

func newFakeWellbeing() *fakeWellbeing { return &fakeWellbeing{} }

func (w *fakeWellbeing) SetLeisureFit(citizenID uint64, fit float64) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pushes = append(w.pushes, struct {
		citizenID uint64
		fit       float64
	}{citizenID, fit})
	return nil
}

func (w *fakeWellbeing) fitFor(citizenID uint64) (float64, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i := len(w.pushes) - 1; i >= 0; i-- {
		if w.pushes[i].citizenID == citizenID {
			return w.pushes[i].fit, true
		}
	}
	return 0, false
}

func (w *fakeWellbeing) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.pushes)
}

// newWiredAPI builds a fully-wired LeisureAPI over a real citizens API and
// fake traffic/wellbeing doubles.
func newWiredAPI(t *testing.T, seed uint64) (*LeisureAPI, *citizens.CitizensAPI, *fakeTraffic, *fakeWellbeing) {
	t.Helper()
	c, err := citizens.NewCitizensAPI(seed, "test")
	if err != nil {
		t.Fatalf("citizens: %v", err)
	}
	a, err := New(testConfig(), seed, "test")
	if err != nil {
		t.Fatalf("leisure: %v", err)
	}
	if err := a.SetCitizens(c); err != nil {
		t.Fatalf("set citizens: %v", err)
	}
	tr := newFakeTraffic()
	if err := a.SetTraffic(tr); err != nil {
		t.Fatalf("set traffic: %v", err)
	}
	wb := newFakeWellbeing()
	if err := a.SetWellbeing(wb); err != nil {
		t.Fatalf("set wellbeing: %v", err)
	}
	return a, c, tr, wb
}

// assertErrCode asserts err is a registry-sourced *errs.E with the given code.
func assertErrCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %s, got nil", want)
	}
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("expected *errs.E, got %T (%v)", err, err)
	}
	if e.Code != want {
		t.Fatalf("expected code %s, got %s", want, e.Code)
	}
}
