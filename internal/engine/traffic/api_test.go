package traffic

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/education"
	"github.com/aaronukgarcia/Metropolis/internal/engine/leisure"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

func TestTraffic_AC2_DataSourcedWages(t *testing.T) {
	api := New()
	tempDir, err := os.MkdirTemp("", "traffic-config-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	configData := `{"baseCommuteHours": 7.5, "baseAccessMinutes": 22.0, "baseCommuteMinutes": 45.0, "baseActiveTravelShare": 0.25}`
	_ = os.WriteFile(filepath.Join(tempDir, "traffic.json"), []byte(configData), 0644)

	err = api.LoadConfig(tempDir)
	if err != nil {
		t.Fatalf("unexpected error loading config: %v", err)
	}

	if api.cfg.BaseCommuteHours != 7.5 || api.cfg.BaseAccessMinutes != 22.0 {
		t.Errorf("expected data-sourced config 7.5/22.0, got %f/%f", api.cfg.BaseCommuteHours, api.cfg.BaseAccessMinutes)
	}
}

func TestTraffic_AC11_CommuteAccounting(t *testing.T) {
	api := New()

	h, err := api.CommuteHours(1234, "test-commute")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h != 5.0 {
		t.Errorf("expected default hours 5.0, got %f", h)
	}

	// Verify un-registered citizen ID 0 returns error code MET-G4501 (AC-9)
	_, err = api.CommuteHours(0, "test-error")
	if err == nil {
		t.Error("expected error for citizen ID 0")
	}
	var re *errs.E
	if !errors.As(err, &re) || re.Code != ErrUnknownCitizen {
		t.Errorf("expected unknown citizen error MET-G4501, got: %v", err)
	}
}

func TestTraffic_AC11_LeisureAccessMinutes(t *testing.T) {
	api := New()

	// Direct access query
	m, err := api.AccessMinutes(5678, leisure.Category(1), "test-access")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m != 15.0 {
		t.Errorf("expected default minutes 15.0, got %f", m)
	}

	// Verify un-registered citizen ID 0 returns error code MET-G4501 (AC-9)
	_, err = api.AccessMinutes(0, leisure.Category(1), "test-error")
	if err == nil {
		t.Error("expected error for citizen ID 0")
	}
	var re *errs.E
	if !errors.As(err, &re) || re.Code != ErrUnknownCitizen {
		t.Errorf("expected unknown citizen error MET-G4501, got: %v", err)
	}
}

func TestTraffic_TripFiling(t *testing.T) {
	api := New()

	// Verify leisure trip filing
	err := api.AddTripDemand(leisure.TripDemand{
		District: 12,
		Count:    150,
	})
	if err != nil {
		t.Fatalf("unexpected leisure filing error: %v", err)
	}

	// Verify education trip filing
	err = api.RegisterTrip(education.TripDemand{
		SchoolID: 301,
		Count:    50,
	})
	if err != nil {
		t.Fatalf("unexpected education filing error: %v", err)
	}

	// Verify direct school demand filing
	err = api.AddDemand(301, 25)
	if err != nil {
		t.Fatalf("unexpected direct demand error: %v", err)
	}

	api.mu.RLock()
	d12 := api.demands[12]
	d301 := api.demands[301]
	api.mu.RUnlock()

	if d12 != 150 {
		t.Errorf("expected demand for district 12 to be 150, got %d", d12)
	}
	if d301 != 75 {
		t.Errorf("expected aggregate demand for school 301 to be 75, got %d", d301)
	}
}

func TestTraffic_AC9_InvalidInputValidation(t *testing.T) {
	api := New()

	// Register negative count leisure trip should fail with MET-G4502
	err := api.AddTripDemand(leisure.TripDemand{
		District: 12,
		Count:    -50,
	})
	if err == nil {
		t.Error("expected error for negative trip count")
	}
	var re *errs.E
	if !errors.As(err, &re) || re.Code != ErrInvalidInput {
		t.Errorf("expected invalid input error MET-G4502, got: %v", err)
	}
}

func TestTraffic_AC11_Determinism(t *testing.T) {
	api1 := New()
	api2 := New()

	_ = api1.AddDemand(301, 100)
	_ = api2.AddDemand(301, 100)

	api1.mu.RLock()
	d1 := api1.demands[301]
	api2.mu.RLock()
	d2 := api2.demands[301]
	api1.mu.RUnlock()
	api2.mu.RUnlock()

	if d1 != d2 {
		t.Errorf("expected demand to be equal, got %d and %d", d1, d2)
	}
}

func TestTraffic_AC13_Concurrency(t *testing.T) {
	api := New()

	var wg sync.WaitGroup
	workers := 10
	wg.Add(workers)

	for i := 0; i < workers; i++ {
		go func(schoolID uint64) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = api.AddDemand(schoolID, 2)
				_, _ = api.CommuteHours(1234, "test-concurrency")
			}
		}(uint64(i * 100))
	}

	wg.Wait()
}

func TestTraffic_AC15_AdvanceTickReset(t *testing.T) {
	api := New()

	_ = api.AddDemand(301, 1000)
	c1, _ := api.CommuteHours(1234, "test-reset")

	_ = api.AdvanceTick("test-reset")
	c2, _ := api.CommuteHours(1234, "test-reset")

	if c1 <= c2 {
		t.Errorf("expected commute hours to be higher before tick reset (%f) than after (%f)", c1, c2)
	}
	if c2 != 5.0 {
		t.Errorf("expected commute hours to return to base 5.0 after reset, got %f", c2)
	}
}

func TestTraffic_Int64Overflow(t *testing.T) {
	api := New()

	maxInt64 := int64(^uint64(0) >> 1)

	_ = api.AddDemand(301, maxInt64)
	_ = api.AddDemand(301, 10) // attempt to overflow

	api.mu.RLock()
	d := api.demands[301]
	api.mu.RUnlock()

	if d < 0 {
		t.Errorf("expected saturating add to prevent negative overflow, got %d", d)
	}
	if d != maxInt64 {
		t.Errorf("expected saturating add to cap at MaxInt64, got %d", d)
	}
}
