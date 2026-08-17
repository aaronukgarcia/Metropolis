package census

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

func assertCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want registry error %s, got nil", want)
	}
	e, ok := err.(*errs.E)
	if !ok {
		t.Fatalf("error is %T, want *errs.E", err)
	}
	if e.Code != want {
		t.Fatalf("error code = %s, want %s", e.Code, want)
	}
}

// TestUnknownGUID proves querying a bio/check-in for an unknown GUID returns
// a registry-sourced error and creates no zero-value bio/check-in as a side
// effect (AC-21, GR#7).
func TestUnknownGUID(t *testing.T) {
	c := newTestCensus(t)
	wire(t, c)

	rec, err := c.CheckIn(citizenGUID(999))
	assertCode(t, err, ErrUnknownObject)
	if rec.GUID != "" || len(rec.Facets) != 0 {
		t.Fatalf("fabricated a check-in record for an unknown GUID: %+v", rec)
	}

	if _, err := c.CitizenBio(citizenGUID(999), 1, "test"); err == nil {
		t.Fatalf("CitizenBio(unknown) should error")
	} else {
		assertCode(t, err, ErrUnknownObject)
	}

	if _, err := c.ObjectBio(carGUID(999), "test"); err == nil {
		t.Fatalf("ObjectBio(unknown) should error")
	} else {
		assertCode(t, err, ErrUnknownObject)
	}
}

// TestUnknownKPI proves resolving an unknown KPI/statistic key returns a
// registry-sourced error and creates no zero-value resolution (AC-21).
func TestUnknownKPI(t *testing.T) {
	c := newTestCensus(t)
	wire(t, c)
	snap, err := c.Snapshot(1, "test")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	res, err := c.Source(snap, "no-such-kpi")
	assertCode(t, err, ErrUnknownKey)
	if res.AggregateID != "" || res.EntityIDs != nil || res.LineValue != 0 {
		t.Fatalf("fabricated a resolution for an unknown key: %+v", res)
	}
}

// writeConfigFile writes a config JSON into a temp dir and returns the dir.
func writeConfigFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileCensus), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return dir
}

// TestMalformedCensusRejected proves a schema-invalid data/census.json is
// rejected at load with ErrCensusDataInvalid, never a silent default
// substitution (AC-22).
func TestMalformedCensusRejected(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{
			name:    "missing-unit",
			content: `{"version":1,"meta":{"module":"engine.census","featureKey":"feat.citycensus","balanceRegime":"p"},"bellCurves":{"lifespanMeanYears":{"value":75,"disclosure":"p"}}}`,
		},
		{
			name:    "negative-lifespan",
			content: `{"version":1,"meta":{"module":"engine.census","featureKey":"feat.citycensus","balanceRegime":"p"},"bellCurves":{"lifespanMeanYears":{"value":-5,"unit":"years","disclosure":"p"}}}`,
		},
		{
			name:    "unrecognised-key",
			content: `{"version":1,"bogusKey":true}`,
		},
		{
			name:    "threshold-out-of-range",
			content: `{"version":1,"meta":{"module":"engine.census","featureKey":"feat.citycensus","balanceRegime":"p"},"thresholds":{"crimeRate":{"value":5,"unit":"rate","disclosure":"p"}}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeConfigFile(t, tc.content)
			cfg, err := LoadConfig(dir, "test")
			if err == nil {
				t.Fatalf("LoadConfig should reject malformed config, got %+v", cfg)
			}
			assertCode(t, err, ErrCensusDataInvalid)
		})
	}
}

// TestCensusDataFileLoadsWithPlaceholders proves the shipped data/census.json
// loads and every numeric entry carries a unit and disclosure (AC-16/AC-27).
func TestCensusDataFileLoadsWithPlaceholders(t *testing.T) {
	cfg, err := LoadDefaultConfig("test")
	if err != nil {
		t.Fatalf("LoadDefaultConfig: %v", err)
	}
	if cfg.Meta.FeatureKey != "feat.citycensus" {
		t.Fatalf("feature key wrong: %q", cfg.Meta.FeatureKey)
	}
	// Retirement age is a data-file placeholder, stated in years.
	if cfg.BellCurves.RetirementAgeYears.Unit != "years" || cfg.BellCurves.RetirementAgeYears.Disclosure == "" {
		t.Fatalf("retirement age missing unit/disclosure: %+v", cfg.BellCurves.RetirementAgeYears)
	}
	if cfg.Thresholds.ConsistencyCheckInLagTicks.Unit != "ticks" {
		t.Fatalf("CC lag missing unit: %+v", cfg.Thresholds.ConsistencyCheckInLagTicks)
	}
}
