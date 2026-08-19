package cafe

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/season"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

type mockWellbeing struct {
	access map[uint64]float64
}

func (m *mockWellbeing) SetCommunityVenueAccess(citizenID uint64, access float64) error {
	m.access[citizenID] = access
	return nil
}

func TestCafe_AC2_TermDrillThrough(t *testing.T) {
	api := New()
	_ = api.RegisterCentre(1, 100.0, 10.0)
	_ = api.RegisterPatronage(1, 200)
	_ = api.SetVenueCount(1, 5)
	_ = api.SetDwellQuality(1, 1.2)

	// Fetch initial terms
	f0, _ := api.Footfall(1)
	d0, _ := api.VenueDensity(1)
	dq0, _ := api.DwellQuality(1)
	s0, _ := api.Safety(1)
	c0, _ := api.WeatherAdjustedCapacity(1, 0)

	v0, _ := api.VitalityIndex(1, 0)
	expectedComposite := f0 * d0 * dq0 * s0 * c0
	if math.Abs(v0-expectedComposite) > 1e-9 {
		t.Errorf("composite vitality %f != expected product %f", v0, expectedComposite)
	}

	// Change ONE term: Patronage (affects footfall only)
	_ = api.RegisterPatronage(1, 100)
	f1, _ := api.Footfall(1)
	d1, _ := api.VenueDensity(1)
	v1, _ := api.VitalityIndex(1, 0)

	if f1 == f0 {
		t.Error("footfall should have changed")
	}
	if d1 != d0 {
		t.Error("venue density should NOT have changed")
	}
	if v1 == v0 {
		t.Error("composite vitality should have changed")
	}
}

func TestCafe_AC3_VenueDensity(t *testing.T) {
	api := New()
	_ = api.RegisterCentre(1, 50.0, 10.0)

	// Check venue density normalisation
	_ = api.SetVenueCount(1, 0)
	den0, _ := api.VenueDensity(1)
	if den0 != 0 {
		t.Errorf("expected 0 density, got %f", den0)
	}

	_ = api.SetVenueCount(1, 10)
	den1, _ := api.VenueDensity(1)
	expectedDen := 10.0 / 50.0
	if math.Abs(den1-expectedDen) > 1e-9 {
		t.Errorf("expected density %f, got %f", expectedDen, den1)
	}
}

func TestCafe_AC4_JanuaryGales(t *testing.T) {
	api := New()
	_ = api.RegisterCentre(1, 100.0, 20.0)

	// Query without SeasonAPI loaded should default to multiplier 1.0
	cap0, _ := api.WeatherAdjustedCapacity(1, 0)
	if cap0 != 20.0 {
		t.Errorf("expected capacity 20.0, got %f", cap0)
	}

	// Load real SeasonAPI
	seasonAPI, err := season.LoadDefault("test-cafe")
	if err != nil {
		t.Fatalf("failed to load season api: %v", err)
	}
	_ = api.SetSeason(seasonAPI)

	// July is calendar month 6 (0-indexed absolute month index 6)
	// January is calendar month 0 (0-indexed absolute month index 0)
	capJuly, err := api.WeatherAdjustedCapacity(1, 6)
	if err != nil {
		t.Fatalf("failed to query July capacity: %v", err)
	}
	capJan, err := api.WeatherAdjustedCapacity(1, 0)
	if err != nil {
		t.Fatalf("failed to query Jan capacity: %v", err)
	}

	// "Mediterranean July pavement is dead in January gales"
	if capJuly <= capJan {
		t.Errorf("expected July capacity (%f) > Jan capacity (%f)", capJuly, capJan)
	}
}

func TestCafe_AC4_SeasonalMutation(t *testing.T) {
	api := New()
	_ = api.RegisterCentre(1, 100.0, 50.0)

	// Create a temp directory
	tempDir, err := os.MkdirTemp("", "cafe-season-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Write mutated seasonal.json where January = 3.5 and July = 0.05
	mutatedJSON := `{
		"version": 1,
		"meta": {},
		"curves": {
			"electricityWinterPeak": { "multipliers": [1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0] },
			"waterSummerPeak": { "multipliers": [1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0] },
			"gasSeasonal": { "multipliers": [1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0] },
			"harvestCalendar": { "multipliers": [1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0] },
			"constructionSpeedMultiplier": { "multipliers": [1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0] },
			"schoolIntakeGate": { "multipliers": [0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 1.0, 0.0, 0.0, 0.0] },
			"leisureBeachWeight": { "multipliers": [3.5, 1.0, 1.0, 1.0, 1.0, 1.0, 0.05, 1.0, 1.0, 1.0, 1.0, 1.0] },
			"leisureIndoorWeight": { "multipliers": [1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0] },
			"healthWaveModifier": { "multipliers": [0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0] }
		}
	}`
	_ = os.WriteFile(filepath.Join(tempDir, "seasonal.json"), []byte(mutatedJSON), 0644)

	// Load SeasonAPI from mutated config
	seasonAPI, err := season.Load(tempDir, "test-cafe-mutation")
	if err != nil {
		t.Fatalf("failed to load mutated SeasonAPI: %v", err)
	}
	_ = api.SetSeason(seasonAPI)

	// January capacity (month index 0)
	capJan, err := api.WeatherAdjustedCapacity(1, 0)
	if err != nil {
		t.Fatalf("unexpected error January: %v", err)
	}
	expectedJan := 50.0 * 3.5
	if math.Abs(capJan-expectedJan) > 1e-9 {
		t.Errorf("January mutated capacity = %f, want %f", capJan, expectedJan)
	}

	// July capacity (month index 6)
	capJul, err := api.WeatherAdjustedCapacity(1, 6)
	if err != nil {
		t.Fatalf("unexpected error July: %v", err)
	}
	expectedJul := 50.0 * 0.05
	if math.Abs(capJul-expectedJul) > 1e-9 {
		t.Errorf("July mutated capacity = %f, want %f", capJul, expectedJul)
	}
}

func TestCafe_AC6_IsolationReduction(t *testing.T) {
	api := New()
	mockWB := &mockWellbeing{access: make(map[uint64]float64)}
	_ = api.SetWellbeing(mockWB)

	// High vs low sociability citizen
	redHigh, err := api.PushIsolationReduction(101, 80.0, 0.7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	redLow, err := api.PushIsolationReduction(102, 20.0, 0.7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Assert the higher sociability citizen gets a larger isolation reduction
	if redHigh <= redLow {
		t.Errorf("expected high-sociability reduction (%f) > low (%f)", redHigh, redLow)
	}

	// Check mocked push to wellbeing
	if mockWB.access[101] != 0.7 {
		t.Errorf("expected pushed access to be 0.7, got %f", mockWB.access[101])
	}
}

func TestCafe_AC7_PedestrianisationAndMarketDay(t *testing.T) {
	api := New()
	_ = api.RegisterCentre(1, 100.0, 10.0)

	// Initial footfall
	ff0, _ := api.Footfall(1)

	// Enable pedestrianisation
	_ = api.SetPedestrianised(1, true)
	ff1, _ := api.Footfall(1)
	if ff1 <= ff0 {
		t.Errorf("expected pedestrianised footfall (%f) > initial (%f)", ff1, ff0)
	}

	// Enable market day
	_ = api.SetMarketDay(1, true)
	ff2, _ := api.Footfall(1)
	if ff2 <= ff1 {
		t.Errorf("expected market day footfall (%f) > pedestrianised (%f)", ff2, ff1)
	}

	// Initial dwell quality
	dq0, _ := api.DwellQuality(1)
	_ = api.SetStreetPerformanceLicensed(1, true)
	dq1, _ := api.DwellQuality(1)
	if dq1 <= dq0 {
		t.Errorf("expected performance licensed quality (%f) > initial (%f)", dq1, dq0)
	}
}

func TestCafe_AC8_LeverageRatio(t *testing.T) {
	api := New()
	_ = api.RegisterCentre(1, 100.0, 10.0)

	// Load dynamic config to prove it's data-driven (GR#15)
	tempDir, err := os.MkdirTemp("", "cafe-config-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	configData := `{"pedestrianizationBoost": 25.0, "pedestrianizationCost": 1250.0}`
	_ = os.WriteFile(filepath.Join(tempDir, "cafe.json"), []byte(configData), 0644)

	err = api.LoadConfig(tempDir)
	if err != nil {
		t.Fatalf("unexpected error loading config: %v", err)
	}

	ratio, err := api.LeverageRatio(1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expectedRatio := 25.0 / 1250.0 // data-driven
	if math.Abs(ratio-expectedRatio) > 1e-9 {
		t.Errorf("expected leverage ratio %f, got %f", expectedRatio, ratio)
	}
}

func TestCafe_AC10_UnknownCentre(t *testing.T) {
	api := New()
	_, err := api.VitalityIndex(999, 0)
	if err == nil {
		t.Error("expected error for unregistered centre")
	}

	var re *errs.E
	if !errors.As(err, &re) || re.Code != "MET-G5101" {
		t.Errorf("expected error code MET-G5101, got %v", err)
	}

	// Assert no zero-vitality record is created in the map
	api.mu.RLock()
	_, ok := api.centres[999]
	api.mu.RUnlock()
	if ok {
		t.Error("centre 999 was silently created")
	}
}

func TestCafe_AC11_MalformedVitalityConfig(t *testing.T) {
	api := New()
	tempDir, err := os.MkdirTemp("", "cafe-config-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Negative weight config
	badConfig := `{"footfallWeight": -1.0}`
	_ = os.WriteFile(filepath.Join(tempDir, "cafe.json"), []byte(badConfig), 0644)

	err = api.LoadConfig(tempDir)
	if err == nil {
		t.Error("expected error for negative weight config")
	}
}

func TestCafe_AC13_Determinism(t *testing.T) {
	api1 := New()
	api2 := New()

	_ = api1.RegisterCentre(1, 100.0, 10.0)
	_ = api1.RegisterPatronage(1, 150)
	_ = api1.SetVenueCount(1, 3)

	_ = api2.RegisterCentre(1, 100.0, 10.0)
	_ = api2.RegisterPatronage(1, 150)
	_ = api2.SetVenueCount(1, 3)

	v1, _ := api1.VitalityIndex(1, 0)
	v2, _ := api2.VitalityIndex(1, 0)

	if v1 != v2 {
		t.Errorf("expected deterministic index to be equal, got %f and %f", v1, v2)
	}
}

func TestCafe_AC14_Concurrency(t *testing.T) {
	api := New()
	_ = api.RegisterCentre(1, 100.0, 10.0)
	_ = api.RegisterPatronage(1, 100)
	_ = api.SetVenueCount(1, 5)

	var wg sync.WaitGroup
	workers := 10
	wg.Add(workers)

	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = api.VitalityIndex(1, 0)
			}
		}()
	}

	wg.Wait()
}
