package wellbeing

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/season"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// testCitizen builds a hot citizen record for the gather-path tests: age 30
// (neutral age-curve delta), a known home cell, ambition matching an
// Employed/Tertiary job (neutral mismatch), full sociability.
func testCitizen() citizens.Citizen {
	var p citizens.Personality
	p[citizens.AxisSociability] = 100
	p[citizens.AxisAmbition] = 85
	p[citizens.AxisPhysicality] = 80
	return citizens.Citizen{
		ID:           7,
		BirthMonth:   0,
		Home:         1234,
		Personality:  p,
		Employment:   citizens.Employment{State: citizens.EmploymentEmployed, Sector: citizens.SectorTertiary},
		Satisfaction: citizens.Satisfaction{50, 50, 50, 50, 50},
		Month:        360, // age 30
	}
}

// neutralContext is the pushed ContextInputs whose every derived driver is
// neutral (one person per room, no rent burden, no unemployment).
func neutralContext() ContextInputs {
	return ContextInputs{
		PersonsPerRoom:           1,
		MonthlyRentMicroPounds:   0,
		MonthlyIncomeMicroPounds: 1000000,
		UnemploymentMonths:       0,
		CommunityVenueAccess:     1,
		SportVenueAccess:         0,
		LeisureFit:               0,
	}
}

// --- fake sources ---------------------------------------------------------

type fakeShopping struct {
	share float64
	ok    bool
}

func (f fakeShopping) FreshFoodShare(uint64, string) (float64, bool, error) {
	return f.share, f.ok, nil
}

type fakeTraffic struct {
	commute, active float64
	ok              bool
}

func (f fakeTraffic) CommuteMinutes(uint64, string) (float64, bool, error) {
	return f.commute, f.ok, nil
}
func (f fakeTraffic) ActiveTravelShare(uint64, string) (float64, bool, error) {
	return f.active, f.ok, nil
}

type fakeHealthcare struct {
	access float64
	ok     bool
}

func (f fakeHealthcare) HealthcareAccess(uint64, string) (float64, bool, error) {
	return f.access, f.ok, nil
}

type fakeNeighbourhood struct {
	green, noise float64
	ok           bool
}

func (f fakeNeighbourhood) GreenSpace400m(uint64, string) (float64, bool, error) {
	return f.green, f.ok, nil
}
func (f fakeNeighbourhood) Noise(uint64, string) (float64, bool, error) { return f.noise, f.ok, nil }

type fakePollution struct {
	level float64
	ok    bool
}

func (f fakePollution) Pollution(uint32, string) (float64, bool, error) { return f.level, f.ok, nil }

// --- AC-10: season HealthWaveModifier consumption --------------------------

func seasonFixtureJSON(healthWave string) string {
	return `{
		"version": 1,
		"curves": {
			"electricityWinterPeak": {"multipliers": [1.15,1.15,1,1,1,1,1,1,1,1,1,1.15]},
			"waterSummerPeak": {"multipliers": [1,1,1,1,1,1.25,1.25,1.25,1,1,1,1]},
			"gasSeasonal": {"multipliers": [2.2,1,1,1,1,1,0.2,1,1,1,1,1]},
			"harvestCalendar": {"multipliers": [0.1,0.1,0.1,0.15,0.2,0.3,0.5,1,1,0.8,0.2,0.1]},
			"constructionSpeedMultiplier": {"multipliers": [0.8,0.8,0.9,1,1,1,1,1,1,1,0.95,0.8]},
			"schoolIntakeGate": {"multipliers": [0,0,0,0,0,0,0,0,1,0,0,0]},
			"leisureBeachWeight": {"multipliers": [0.1,0.1,0.15,0.2,0.3,0.6,0.8,0.8,0.5,0.2,0.1,0.1]},
			"leisureIndoorWeight": {"multipliers": [0.8,0.8,0.7,0.6,0.4,0.2,0.15,0.15,0.3,0.6,0.8,0.85]},
			"healthWaveModifier": {"multipliers": ` + healthWave + `}
		}
	}`
}

func loadSeasonFixture(t *testing.T, healthWave string) *season.SeasonAPI {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "seasonal.json"), []byte(seasonFixtureJSON(healthWave)), 0o644); err != nil {
		t.Fatalf("write seasonal.json: %v", err)
	}
	api, err := season.Load(dir, errs.NewCorrelationID())
	if err != nil {
		t.Fatalf("season.Load: %v", err)
	}
	return api
}

func TestSeasonalHealthWaveComponent(t *testing.T) {
	api := newTestAPI(t)

	mild := loadSeasonFixture(t, "[0.05,0.05,0.02,0,0,0,0,0,0,0,0.02,0.05]")
	if err := api.SetSeason(mild); err != nil {
		t.Fatalf("SetSeason(mild): %v", err)
	}
	aMild, err := api.AttributeCitizen(testCitizen(), 0, neutralContext())
	if err != nil {
		t.Fatalf("AttributeCitizen(mild): %v", err)
	}

	severe := loadSeasonFixture(t, "[0.3,0.3,0.2,0.1,0,0,0,0,0,0,0.2,0.3]")
	if err := api.SetSeason(severe); err != nil {
		t.Fatalf("SetSeason(severe): %v", err)
	}
	aSevere, err := api.AttributeCitizen(testCitizen(), 0, neutralContext())
	if err != nil {
		t.Fatalf("AttributeCitizen(severe): %v", err)
	}

	// The physical seasonal component must differ, and the physical baseline
	// (which folds it in) must shift by exactly that difference.
	if aMild.Physical.SeasonalHealthWave == aSevere.Physical.SeasonalHealthWave {
		t.Errorf("seasonal health wave did not change: %v vs %v", aMild.Physical.SeasonalHealthWave, aSevere.Physical.SeasonalHealthWave)
	}
	if !(aSevere.Physical.SeasonalHealthWave < aMild.Physical.SeasonalHealthWave) {
		t.Errorf("severe winter wave (%v) is not more negative than mild (%v)", aSevere.Physical.SeasonalHealthWave, aMild.Physical.SeasonalHealthWave)
	}
	baseDelta := aSevere.Physical.Baseline - aMild.Physical.Baseline
	waveDelta := aSevere.Physical.SeasonalHealthWave - aMild.Physical.SeasonalHealthWave
	if baseDelta != waveDelta {
		t.Errorf("baseline shift %v != seasonal wave shift %v", baseDelta, waveDelta)
	}
}

// --- AC-12a: shopping Diet -------------------------------------------------

func TestDietSourcedFromShopping(t *testing.T) {
	api := newTestAPI(t)
	if err := api.SetSeason(loadSeasonFixture(t, "[0,0,0,0,0,0,0,0,0,0,0,0]")); err != nil {
		t.Fatalf("SetSeason: %v", err)
	}

	low := neutralContext()
	_ = api.SetShopping(fakeShopping{share: 0.1, ok: true})
	aLow, err := api.AttributeCitizen(testCitizen(), 0, low)
	if err != nil {
		t.Fatalf("AttributeCitizen(low): %v", err)
	}

	_ = api.SetShopping(fakeShopping{share: 0.9, ok: true})
	aHigh, err := api.AttributeCitizen(testCitizen(), 0, neutralContext())
	if err != nil {
		t.Fatalf("AttributeCitizen(high): %v", err)
	}

	if aHigh.Physical.Diet.Delta <= aLow.Physical.Diet.Delta {
		t.Errorf("Diet delta did not rise with fresh-food share: %v -> %v", aLow.Physical.Diet.Delta, aHigh.Physical.Diet.Delta)
	}
	if aHigh.Physical.Diet.Source != "engine.shopping" {
		t.Errorf("Diet source = %q, want engine.shopping", aHigh.Physical.Diet.Source)
	}
}

// --- AC-12b: world pollution ----------------------------------------------

func TestPollutionSourcedFromWorld(t *testing.T) {
	api := newTestAPI(t)
	if err := api.SetSeason(loadSeasonFixture(t, "[0,0,0,0,0,0,0,0,0,0,0,0]")); err != nil {
		t.Fatalf("SetSeason: %v", err)
	}

	_ = api.SetPollution(fakePollution{level: 0.1, ok: true})
	aClean, err := api.AttributeCitizen(testCitizen(), 0, neutralContext())
	if err != nil {
		t.Fatalf("AttributeCitizen(clean): %v", err)
	}

	_ = api.SetPollution(fakePollution{level: 0.9, ok: true})
	aDirty, err := api.AttributeCitizen(testCitizen(), 0, neutralContext())
	if err != nil {
		t.Fatalf("AttributeCitizen(dirty): %v", err)
	}

	if aDirty.Physical.PollutionExposure.Delta >= aClean.Physical.PollutionExposure.Delta {
		t.Errorf("pollution delta did not worsen with exposure: %v -> %v", aClean.Physical.PollutionExposure.Delta, aDirty.Physical.PollutionExposure.Delta)
	}
	if aDirty.Physical.PollutionExposure.Source != "engine.world" {
		t.Errorf("pollution source = %q, want engine.world", aDirty.Physical.PollutionExposure.Source)
	}
}

// TestWorldPollutionAdapter exercises the real engine.world bridge: an
// unowned home cell reports ok=false (no simulated overlay yet), while an
// owned cell reports the (currently zero) pollution byte as 0.0.
func TestWorldPollutionAdapter(t *testing.T) {
	wapi := world.NewWorldAPI(world.TileCoord{X: 0, Y: 0})
	adapter := WorldPollution{World: wapi}

	// home 1234 maps to tile (0,0) local (34,6) via homeToWorld — unowned,
	// so no overlay record yet.
	v, ok, err := adapter.Pollution(1234, errs.NewCorrelationID())
	if err != nil {
		t.Fatalf("unowned Pollution: %v", err)
	}
	if ok {
		t.Errorf("unowned home cell reported ok=true (value %v), want ok=false", v)
	}

	// Purchase tile (0,0) so the home cell has simulated storage.
	res := wapi.PurchaseTile(world.PurchaseCommand{
		CorrelationID: errs.NewCorrelationID(),
		Tile:          world.TileCoord{X: 0, Y: 0},
		BuyerID:       1,
	})
	if !res.Accepted {
		t.Fatalf("PurchaseTile rejected: %+v", res.Error)
	}
	v, ok, err = adapter.Pollution(1234, errs.NewCorrelationID())
	if err != nil {
		t.Fatalf("owned Pollution: %v", err)
	}
	if !ok {
		t.Errorf("owned home cell reported ok=false, want ok=true")
	}
	if v != 0 {
		t.Errorf("freshly-owned cell pollution = %v, want 0", v)
	}
}

// --- AC-14: missing upstream degrades to neutral + low confidence ----------

func TestMissingUpstreamDegradesToLowConfidence(t *testing.T) {
	api := newTestAPI(t)
	if err := api.SetSeason(loadSeasonFixture(t, "[0,0,0,0,0,0,0,0,0,0,0,0]")); err != nil {
		t.Fatalf("SetSeason: %v", err)
	}

	// healthcare returns ok=false (a district with no coverage record yet).
	_ = api.SetHealthcare(fakeHealthcare{access: 0, ok: false})
	_ = api.SetShopping(fakeShopping{share: 0, ok: false})
	_ = api.SetTraffic(fakeTraffic{commute: 0, active: 0, ok: false})
	_ = api.SetNeighbourhood(fakeNeighbourhood{green: 0, noise: 0, ok: false})
	_ = api.SetPollution(fakePollution{level: 0, ok: false})

	attr, err := api.AttributeCitizen(testCitizen(), 0, neutralContext())
	if err != nil {
		t.Fatalf("AttributeCitizen: %v", err)
	}

	if attr.Physical.HealthcareAccess.Confidence != 0 {
		t.Errorf("healthcare confidence = %v, want 0 (missing upstream)", attr.Physical.HealthcareAccess.Confidence)
	}
	if attr.Physical.HealthcareAccess.Delta != 0 {
		t.Errorf("healthcare delta = %v, want neutral 0 for missing upstream", attr.Physical.HealthcareAccess.Delta)
	}
	if attr.Physical.Diet.Confidence != 0 || attr.Physical.Diet.Delta != 0 {
		t.Errorf("diet degraded wrong: conf=%v delta=%v", attr.Physical.Diet.Confidence, attr.Physical.Diet.Delta)
	}
	if attr.Mental.CommuteTime.Confidence != 0 || attr.Mental.CommuteTime.Delta != 0 {
		t.Errorf("commute degraded wrong: conf=%v delta=%v", attr.Mental.CommuteTime.Confidence, attr.Mental.CommuteTime.Delta)
	}
	// No NaN/Inf leaked into Total.
	if attr.Physical.Total != attr.Physical.Total || attr.Mental.Total != attr.Mental.Total {
		t.Errorf("a track total is NaN after degradation")
	}
}
