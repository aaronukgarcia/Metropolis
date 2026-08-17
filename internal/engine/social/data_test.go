package social

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestLoadDefault (GR#15): the real data/social.json loads and populates the
// runtime Config — the balance magnitudes live in data, never as Go literals.
func TestLoadDefault(t *testing.T) {
	a, err := LoadDefault("test")
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	if a.cfg.RoughSleepingLocation == "" {
		t.Fatal("roughSleepingLocation must be loaded from data")
	}
	if a.cfg.HostelCapacity <= 0 {
		t.Fatalf("hostelCapacity must be a positive data value, got %d", a.cfg.HostelCapacity)
	}
	if a.cfg.Caseload.AddictionPerPressure <= 0 {
		t.Fatal("addictionPerPressure must be a positive data value")
	}
}

// TestConfigValidationRejectsBadData (GR#7): an out-of-contract Config is
// rejected with a registry-sourced error, never a silently-defaulted rate.
func TestConfigValidationRejectsBadData(t *testing.T) {
	cfg := testConfig()
	cfg.Caseload.FamilyPerDeprivation = -1
	_, err := New(cfg, 1, "test")
	if err == nil {
		t.Fatal("expected a negative caseload rate to be rejected")
	}
	if !errors.Is(err, &errs.E{Code: ErrSocialDataInvalid}) {
		t.Fatalf("error code = %v, want %s", err, ErrSocialDataInvalid)
	}
}
