package coastal

import (
	"path/filepath"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
)

// TestLoadConfigFromDataFile (GR#15): the shipped data/coastal.json loads and
// validates into a Config with sensible placeholder magnitudes — never a Go
// literal fallback.
func TestLoadConfigFromDataFile(t *testing.T) {
	dir, err := data.ResolveDataDir("corr-test")
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	cfg, err := LoadConfig(filepath.Join(dir, fileCoastal), "corr-test")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.BaseArrivalRate <= 0 {
		t.Fatalf("base arrival rate not positive: %v", cfg.BaseArrivalRate)
	}
	if cfg.MaxBoatSize <= 0 || cfg.MaxArrivalsPerMonth <= 0 {
		t.Fatalf("frequency caps not positive: %+v", cfg)
	}
	if cfg.Reception.CaseworkerThroughputPerMonth <= 0 {
		t.Fatalf("caseworker throughput not positive: %v", cfg.Reception.CaseworkerThroughputPerMonth)
	}
	if cfg.Pipeline.MinMonths <= 0 || cfg.Pipeline.MaxMonths < cfg.Pipeline.MinMonths {
		t.Fatalf("pipeline duration invalid: %+v", cfg.Pipeline)
	}
	if cfg.Rescue.CoastguardServiceID == "" || cfg.Rescue.LifeboatServiceID == "" {
		t.Fatalf("rescue service IDs empty: %+v", cfg.Rescue)
	}
}

// TestLoadDefault (GR#15): LoadDefault resolves the data directory and loads
// the real data file end-to-end.
func TestLoadDefault(t *testing.T) {
	api, err := LoadDefault(42, "corr-test")
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	if api == nil {
		t.Fatalf("LoadDefault returned nil")
	}
}

// TestConfigValidateRejectsInvalid (GR#15/GR#16): a Config outside its
// documented domains is rejected, never silently defaulted.
func TestConfigValidateRejectsInvalid(t *testing.T) {
	bad := testConfig()
	bad.Pipeline.GrantRate = 1.5
	if err := bad.Validate(); err == nil {
		t.Fatalf("expected a grantRate > 1 to be rejected")
	}

	bad2 := testConfig()
	bad2.Reception.CaseworkerThroughputPerMonth = 0
	if err := bad2.Validate(); err == nil {
		t.Fatalf("expected a zero caseworker throughput to be rejected")
	}

	bad3 := testConfig()
	bad3.EraMultipliers[0] = -1
	if err := bad3.Validate(); err == nil {
		t.Fatalf("expected a negative era multiplier to be rejected")
	}
}
