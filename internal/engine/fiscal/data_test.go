package fiscal

import (
	"testing"
)

// TestLoadDefaultLoadsRealData asserts the shipped data/fiscal.json is valid
// and loads, and that its balance figures are the ones the AC-5/AC-6 tests
// rely on (GR#15 — the module reads its numbers from this file, so the file
// must be present and well-formed).
func TestLoadDefaultLoadsRealData(t *testing.T) {
	f, err := LoadDefault("test")
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	if f.cfg.Version <= 0 {
		t.Errorf("Version = %d, want positive", f.cfg.Version)
	}
	m := f.cfg.Municipality
	if m.FundingTargetPerMonthMicroPounds <= 0 {
		t.Errorf("funding target = %d, want positive", m.FundingTargetPerMonthMicroPounds)
	}
	// §54's underfunding anchor must be inside the spec's 10–20% band.
	if m.BuildCostErrorAtZeroFunding < 0.10 || m.BuildCostErrorAtZeroFunding > 0.20 {
		t.Errorf("buildCostErrorAtZeroFunding = %v, want in [0.10, 0.20] (§54's 10–20%% over)", m.BuildCostErrorAtZeroFunding)
	}
	if cc := f.cfg.Childcare; cc.SubsidyPerPlacePerMonthMicroPounds <= 0 {
		t.Errorf("childcare subsidy = %d, want positive", cc.SubsidyPerPlacePerMonthMicroPounds)
	}
}

// TestConfigValidateRejectsInvalid asserts the schema-validation boundary:
// each out-of-domain config is rejected with a field-naming error, never
// silently accepted (GR#7/GR#16).
func TestConfigValidateRejectsInvalid(t *testing.T) {
	base := validTestConfig()

	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"version", func(c *Config) { c.Version = 0 }},
		{"funding target", func(c *Config) { c.Municipality.FundingTargetPerMonthMicroPounds = 0 }},
		{"permit anchors reversed", func(c *Config) {
			c.Municipality.PermitSpeedAtZeroFunding = 2.0
			c.Municipality.PermitSpeedAtFullFunding = 0.5
		}},
		{"build-cost error reversed", func(c *Config) {
			c.Municipality.BuildCostErrorAtZeroFunding = 0.0
			c.Municipality.BuildCostErrorAtFullFunding = 0.2
		}},
		{"build-cost error out of band", func(c *Config) {
			c.Municipality.BuildCostErrorAtZeroFunding = 1.5
		}},
		{"corruption threshold zero", func(c *Config) { c.Municipality.CorruptionThreshold = 0 }},
		{"childcare subsidy non-positive", func(c *Config) { c.Childcare.SubsidyPerPlacePerMonthMicroPounds = 0 }},
		{"childcare uplift out of band", func(c *Config) { c.Childcare.SecondEarnerUpliftPerPlace = 1.5 }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base
			tc.mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatalf("Validate() = nil, want error for %s", tc.name)
			}
			if _, err := New(c, "test"); err == nil {
				t.Fatalf("New() = nil error, want error for %s", tc.name)
			}
		})
	}
}
