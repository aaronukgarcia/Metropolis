package wellbeing

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestLoadWellbeingRealData loads the repository's own data/wellbeing.json
// (via ResolveDataDir) and checks the real balance surface round-trips.
func TestLoadWellbeingRealData(t *testing.T) {
	dir, err := data.ResolveDataDir(errs.NewCorrelationID())
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	f, err := LoadWellbeing(dir, errs.NewCorrelationID())
	if err != nil {
		t.Fatalf("LoadWellbeing(real data/wellbeing.json): %v", err)
	}
	if _, err := New(f, 1, errs.NewCorrelationID()); err != nil {
		t.Fatalf("New(real config): %v", err)
	}
}

func writeWellbeingFixture(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileWellbeing), []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return dir
}

// TestLoadWellbeingRejectsUnsortedAgeCurve proves a hand-edited age curve
// with out-of-order anchors is rejected, not silently interpolated.
func TestLoadWellbeingRejectsUnsortedAgeCurve(t *testing.T) {
	dir := writeWellbeingFixture(t, `{
		"version": 1,
		"baseline": {"physical": 62, "mental": 62},
		"headline": {"physicalWeight": 0.4, "mentalWeight": 0.4, "satisfactionWeight": 0.2},
		"physical": {
			"ageCurve": [{"ageYears": 30, "delta": 0}, {"ageYears": 0, "delta": 0}],
			"healthcareAccessWeight": 15, "dietWeight": 10, "activeTravelWeight": 8,
			"pollutionWeight": 12, "sportParticipationWeight": 10
		},
		"mental": {
			"commuteWeight": 10, "commuteThresholdMinutes": 45,
			"commuteStressAtThreshold": 0.5, "commuteStressAt100Minutes": 2.0,
			"jobAmbitionMismatchWeight": 10, "greenSpaceWeight": 8, "leisureFitWeight": 10,
			"crowdingWeight": 8, "isolationWeight": 10, "noiseWeight": 8,
			"financialStressWeight": 12, "rentBurdenThreshold": 0.35,
			"unemploymentWeight": 10, "unemploymentCapMonths": 60
		},
		"modifiers": {"mortalitySlope": 0.01, "productivitySlope": 0.01, "satisfactionSlope": 0.01, "emigrationSlope": 0.01}
	}`)
	if _, err := LoadWellbeing(dir, errs.NewCorrelationID()); err == nil {
		t.Fatalf("expected ErrDataInvalid for unsorted age curve, got nil")
	} else if e, ok := err.(*errs.E); !ok || e.Code != ErrDataInvalid {
		t.Errorf("err = %v, want code %s", err, ErrDataInvalid)
	}
}

// TestNewRejectsNonFiniteConfig proves New refuses a programmatically-built
// config whose numeric fields are NaN or ±Inf (SEC-093) — the contract's
// "non-finite weight/baseline/threshold" is rejected with ErrDataInvalid,
// never silently folded into a NaN/±Inf total or modifier.
func TestNewRejectsNonFiniteConfig(t *testing.T) {
	cases := []struct {
		name string
		mut  func(cfg *WellbeingFile)
	}{
		{"NaN mental weight", func(c *WellbeingFile) { c.Mental.CrowdingWeight = math.NaN() }},
		{"+Inf physical weight", func(c *WellbeingFile) { c.Physical.DietWeight = math.Inf(1) }},
		{"-Inf modifier slope", func(c *WellbeingFile) { c.Modifiers.MortalitySlope = math.Inf(-1) }},
		{"NaN baseline", func(c *WellbeingFile) { c.Baseline.Mental = math.NaN() }},
		{"+Inf headline weight", func(c *WellbeingFile) { c.Headline.PhysicalWeight = math.Inf(1) }},
		{"NaN rent-burden threshold", func(c *WellbeingFile) { c.Mental.RentBurdenThreshold = math.NaN() }},
		{"+Inf commute threshold", func(c *WellbeingFile) { c.Mental.CommuteThresholdMinutes = math.Inf(1) }},
		{"NaN age-curve years", func(c *WellbeingFile) { c.Physical.AgeCurve[0].AgeYears = math.NaN() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testCfg()
			tc.mut(&cfg)
			if _, err := New(cfg, 1, errs.NewCorrelationID()); err == nil {
				t.Fatalf("New accepted a non-finite config (%s)", tc.name)
			} else if e, ok := err.(*errs.E); !ok || e.Code != ErrDataInvalid {
				t.Errorf("err = %v, want code %s", err, ErrDataInvalid)
			}
		})
	}
}

// TestNewRejectsHugeCoefficient proves New refuses a config whose weight or
// slope exceeds maxCoefficient — the sane-coefficient bound that keeps AC-2's
// additive identity exact (a saturated delta would break Baseline + Σdelta ==
// Total). A finite-but-huge coefficient is a data error, not a value to
// silently saturate.
func TestNewRejectsHugeCoefficient(t *testing.T) {
	cases := []struct {
		name string
		mut  func(cfg *WellbeingFile)
	}{
		{"huge mortality slope", func(c *WellbeingFile) { c.Modifiers.MortalitySlope = math.MaxFloat64 }},
		{"huge headline weight", func(c *WellbeingFile) { c.Headline.PhysicalWeight = math.MaxFloat64 }},
		{"huge crowding weight", func(c *WellbeingFile) { c.Mental.CrowdingWeight = math.MaxFloat64 }},
		{"huge age-curve delta", func(c *WellbeingFile) { c.Physical.AgeCurve[2].Delta = math.MaxFloat64 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testCfg()
			tc.mut(&cfg)
			if _, err := New(cfg, 1, errs.NewCorrelationID()); err == nil {
				t.Fatalf("New accepted a config with an over-bound coefficient (%s)", tc.name)
			} else if e, ok := err.(*errs.E); !ok || e.Code != ErrDataInvalid {
				t.Errorf("err = %v, want code %s", err, ErrDataInvalid)
			}
		})
	}
}

// TestNewRejectsInvalidConfig proves New refuses a config whose commute
// shape has been flattened (violating AC-4's structural nonlinearity).
func TestNewRejectsInvalidConfig(t *testing.T) {
	cfg := testCfg()
	cfg.Mental.CommuteStressAt100Minutes = cfg.Mental.CommuteStressAtThreshold // flatten
	if _, err := New(cfg, 1, errs.NewCorrelationID()); err == nil {
		t.Fatalf("New accepted a flattened commute shape")
	} else if e, ok := err.(*errs.E); !ok || e.Code != ErrDataInvalid {
		t.Errorf("err = %v, want %s", err, ErrDataInvalid)
	}
}
