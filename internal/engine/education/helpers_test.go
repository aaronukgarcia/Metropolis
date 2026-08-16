package education

import (
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/projections"
	"github.com/aaronukgarcia/Metropolis/internal/engine/season"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
)

// testConfig returns a Config whose entry-age gates all land on September
// months (absolute month index m where m % 12 == 8), so a synthetic cohort
// can be advanced through the whole pipeline in a handful of months rather
// than the spec's real 5/11/16/18/25/60-year gates. The real magnitudes are
// in data/education.json; these fixture values only make the mechanism fast
// to exercise (GR#15's test-fixture latitude — a validator derives from
// data, but a unit test may inject a fixture).
func testConfig() Config {
	var c Config
	c.EntryAgeMonths = [numStages]int64{
		StageNursery:          0,
		StagePrimary:          20,
		StageSecondary:        32,
		StageSixthForm:        44,
		StageTechnicalCollege: 44,
		StageLeaveAt16:        44,
		StageUniversity:       56,
		StageAdultEducation:   68,
		StageU3A:              80,
	}
	c.BaselineQuality = 0.5
	c.AttainmentScale = 100
	c.ResearchPointsPerGraduate = 1
	c.HallsCapacity = 100
	c.DropoutRate = 0
	return c
}

// seedCitizen appends one valid cold citizen record with the given birth
// month and (optional) education attainment, and a neutral 50-axis
// personality.
func seedCitizen(t *testing.T, c *citizens.CitizensAPI, id uint64, birthMonth int64, attainment int16) {
	t.Helper()
	var p [citizens.NumPersonalityAxes]int8
	for i := range p {
		p[i] = 50
	}
	r := citizens.ColdRecord{
		ID:             id,
		BirthMonth:     birthMonth,
		Sex:            citizens.SexFemale,
		Personality:    p,
		Attainment:     attainment,
		Stage:          citizens.StageNone,
		HealthBand:     citizens.HealthExcellent,
		SatHousing:     50,
		SatServices:    50,
		SatEnvironment: 50,
		SatLeisureFit:  50,
		SatCommute:     50,
	}
	if err := c.SeedColdRecords([]citizens.ColdRecord{r}, "test"); err != nil {
		t.Fatalf("seed citizen: %v", err)
	}
}

// advanceCitizens advances the citizens clock to the given absolute month.
func advanceCitizens(t *testing.T, c *citizens.CitizensAPI, months int64) {
	t.Helper()
	for i := int64(0); i < months; i++ {
		if err := c.AdvanceMonth("test"); err != nil {
			t.Fatalf("advance month: %v", err)
		}
	}
}

// fakeTraffic is a test double for the engine.traffic trip-generation
// surface (AC-5): it records demand so a test can assert an N-dependent
// increase.
type fakeTraffic struct {
	mu     sync.Mutex
	demand map[uint64]int64
	trips  []TripDemand
}

func newFakeTraffic() *fakeTraffic {
	return &fakeTraffic{demand: make(map[uint64]int64)}
}

func (f *fakeTraffic) RegisterTrip(d TripDemand) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trips = append(f.trips, d)
	return nil
}

func (f *fakeTraffic) AddDemand(schoolID uint64, count int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.demand[schoolID] += count
	return nil
}

func (f *fakeTraffic) totalDemand() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	var t int64
	for _, v := range f.demand {
		t += v
	}
	return t
}

// newWiredAPI builds a fully-wired EducationAPI over real citizens/services/
// season/projections and a fake traffic, with all stages registered and the
// projection provider registered.
func newWiredAPI(t *testing.T, seed uint64) (*EducationAPI, *citizens.CitizensAPI, *services.ServicesAPI, *projections.ProjectionsAPI) {
	return newWiredAPIWithConfig(t, testConfig(), seed)
}

// newWiredAPIWithConfig is newWiredAPI over an explicit Config.
func newWiredAPIWithConfig(t *testing.T, cfg Config, seed uint64) (*EducationAPI, *citizens.CitizensAPI, *services.ServicesAPI, *projections.ProjectionsAPI) {
	t.Helper()
	c, err := citizens.NewCitizensAPI(seed, "test")
	if err != nil {
		t.Fatalf("citizens: %v", err)
	}
	svc := services.New("test")
	seas, err := season.LoadDefault("test")
	if err != nil {
		t.Fatalf("season: %v", err)
	}
	proj := projections.NewProjectionsAPI()

	a, err := New(cfg, seed, "test")
	if err != nil {
		t.Fatalf("education: %v", err)
	}
	if err := a.SetCitizens(c); err != nil {
		t.Fatalf("set citizens: %v", err)
	}
	if err := a.SetServices(svc); err != nil {
		t.Fatalf("set services: %v", err)
	}
	if err := a.SetSeason(seas); err != nil {
		t.Fatalf("set season: %v", err)
	}
	if err := a.SetProjections(proj); err != nil {
		t.Fatalf("set projections: %v", err)
	}
	if err := a.RegisterStages(); err != nil {
		t.Fatalf("register stages: %v", err)
	}
	if err := a.RegisterProjectionProvider(); err != nil {
		t.Fatalf("register projection provider: %v", err)
	}
	return a, c, svc, proj
}
