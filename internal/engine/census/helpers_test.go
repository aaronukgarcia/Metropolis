package census

import (
	"sync"
	"testing"
)

// testConfig returns a valid Config literal mirroring data/census.json's
// placeholder values, so tests are self-contained (never dependent on the
// data directory) while staying faithful to the shipped defaults.
func testConfig() Config {
	return Config{
		Version: 1,
		Meta: Meta{
			Module:        "engine.census",
			FeatureKey:    "feat.citycensus",
			SpecRefs:      []string{"§13 F6", "§18", "§40", "§45", "§46", "UI-SPEC §4", "§5.2"},
			BalanceRegime: "placeholder",
		},
		BellCurves: BellCurves{
			LifespanMeanYears:            Number{Value: 75, Unit: "years", Disclosure: "placeholder"},
			LifespanSpreadYears:          Number{Value: 10, Unit: "years", Disclosure: "placeholder"},
			RetirementAgeYears:           Number{Value: 68, Unit: "years", Disclosure: "placeholder"},
			AnnualMileage:                Number{Value: 10000, Unit: "miles/year", Disclosure: "placeholder"},
			CrimeEducationElasticity:     Number{Value: -0.5, Unit: "points", Disclosure: "placeholder"},
			BlueWhiteCollarBaselineBlue:  Number{Value: 0.6, Unit: "fraction", Disclosure: "placeholder"},
			BlueWhiteCollarBaselineWhite: Number{Value: 0.4, Unit: "fraction", Disclosure: "placeholder"},
			HappinessWeightPhysical:      Number{Value: 0.34, Unit: "weight", Disclosure: "placeholder"},
			HappinessWeightMental:        Number{Value: 0.33, Unit: "weight", Disclosure: "placeholder"},
			HappinessWeightSatisfaction:  Number{Value: 0.33, Unit: "weight", Disclosure: "placeholder"},
		},
		Thresholds: Thresholds{
			ConsistencyCheckInLagTicks: Number{Value: 2, Unit: "ticks", Disclosure: "placeholder"},
			CrimeRate:                  Number{Value: 0.05, Unit: "rate", Disclosure: "placeholder"},
			UnfedFraction:              Number{Value: 0.10, Unit: "fraction", Disclosure: "placeholder"},
			UneducatedFraction:         Number{Value: 0.20, Unit: "fraction", Disclosure: "placeholder"},
			UneducatedAttainmentFloor:  Number{Value: 30, Unit: "attainment points", Disclosure: "placeholder"},
		},
	}
}

// newTestCensus builds a wired census over fakes, ready to observe.
func newTestCensus(t *testing.T) *CensusAPI {
	t.Helper()
	c, err := New(testConfig(), "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- fakes (thread-safe, since the race test runs observers concurrently) ---

type fakeCitizens struct {
	mu    sync.RWMutex
	views map[uint64]CitizenView
}

func newFakeCitizens() *fakeCitizens { return &fakeCitizens{views: map[uint64]CitizenView{}} }

func (f *fakeCitizens) set(v CitizenView) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.views[v.ID] = v
}

func (f *fakeCitizens) remove(id uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.views, id)
}

func (f *fakeCitizens) AllCitizens(string) ([]CitizenView, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]CitizenView, 0, len(f.views))
	for _, v := range f.views {
		out = append(out, v)
	}
	sortCitizens(out)
	return out, nil
}

func (f *fakeCitizens) CitizenFor(id uint64, _ string) (CitizenView, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	v, ok := f.views[id]
	return v, ok
}

type fakeEducation struct {
	mu sync.RWMutex
	m  map[uint64]EducationView
}

func newFakeEducation() *fakeEducation { return &fakeEducation{m: map[uint64]EducationView{}} }

func (f *fakeEducation) set(id uint64, ev EducationView) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[uint64(id)] = ev
}

func (f *fakeEducation) EducationFor(id uint64, _ string) (EducationView, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	ev, ok := f.m[id]
	return ev, ok
}

type fakeCrime struct {
	mu   sync.RWMutex
	rate float64
}

func (f *fakeCrime) setRate(r float64) { f.mu.Lock(); defer f.mu.Unlock(); f.rate = r }
func (f *fakeCrime) CityCrimeRate(string) (float64, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.rate, nil
}

type fakeWellbeing struct {
	mu        sync.RWMutex
	happiness float64
	unfed     float64
}

func (f *fakeWellbeing) HeadlineHappiness(string) (float64, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.happiness, nil
}

func (f *fakeWellbeing) UnfedFraction(string) (float64, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.unfed, nil
}

type fakeServices struct {
	mu          sync.RWMutex
	hospital    int64
	unfilled    int64
	skillDemand int64
}

func (f *fakeServices) HospitalWaitingList(string) (int64, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.hospital, nil
}

func (f *fakeServices) UnfilledJobs(string) (int64, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.unfilled, nil
}

func (f *fakeServices) JobSkillDemand(string) (int64, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.skillDemand, nil
}

type fakePolicies struct {
	mu   sync.RWMutex
	coef float64
}

func (f *fakePolicies) EducationPolicyCoefficient(string) (float64, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.coef, nil
}

type fakeFinance struct {
	mu     sync.RWMutex
	income map[uint64]int64
	gdp    int64
	land   int64
}

func newFakeFinance() *fakeFinance { return &fakeFinance{income: map[uint64]int64{}} }

func (f *fakeFinance) setIncome(id uint64, inc int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.income[id] = inc
}

func (f *fakeFinance) removeIncome(id uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.income, id)
}

func (f *fakeFinance) IncomeFor(id uint64, _ string) (int64, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	v, ok := f.income[id]
	return v, ok
}

func (f *fakeFinance) GDPFlows(string) (int64, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.gdp, nil
}

func (f *fakeFinance) LandValue(string) (int64, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.land, nil
}

// wired bundles the fakes so a test can mutate them and then re-observe.
type wired struct {
	citizens  *fakeCitizens
	education *fakeEducation
	crime     *fakeCrime
	wellbeing *fakeWellbeing
	services  *fakeServices
	policies  *fakePolicies
	finance   *fakeFinance
}

func wire(t *testing.T, c *CensusAPI) wired {
	t.Helper()
	w := wired{
		citizens:  newFakeCitizens(),
		education: newFakeEducation(),
		crime:     &fakeCrime{},
		wellbeing: &fakeWellbeing{},
		services:  &fakeServices{},
		policies:  &fakePolicies{},
		finance:   newFakeFinance(),
	}
	must(t, c.WireCitizens(w.citizens))
	must(t, c.WireEducation(w.education))
	must(t, c.WireCrime(w.crime))
	must(t, c.WireWellbeing(w.wellbeing))
	must(t, c.WireServices(w.services))
	must(t, c.WirePolicies(w.policies))
	must(t, c.WireFinance(w.finance))
	return w
}

// mkCitizen builds a citizen view with sane defaults for a test.
func mkCitizen(id uint64) CitizenView {
	return CitizenView{
		ID:         id,
		BirthMonth: 0,
		Sex:        SexFemale,
		Household:  1,
		Home:       1000 + id,
		Workplace:  2000 + id,
		Employment: EmploymentEmployed,
		Sector:     SectorSecondary,
		HealthBand: 3,
		Wealth:     50_000_000,
	}
}
