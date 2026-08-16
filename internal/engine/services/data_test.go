package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validServicesJSON is a minimal, schema-valid services.json fixture for
// loader validation tests.
func validServicesJSON() string {
	return `{
		"version": 1,
		"wagePerStaffPerMonthMicropounds": 2000000000,
		"staffingPools": [
			{"id": "nursing", "label": "Nursing", "members": ["healthcare", "elder-care"], "specRef": "§26"}
		],
		"pie": {
			"specRef": "§54",
			"severityHalfPointPopulation": 100000,
			"benchmarks": [
				{"id": "police", "label": "Police officers", "perThousand": 2.4, "perPupil": 0, "placeholder": false, "specRef": "§54"}
			]
		}
	}`
}

func writeFixture(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, fileServices), []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

// TestLoadServices_HappyPath proves the loader accepts a valid file and the
// returned API carries the loaded data.
func TestLoadServices_HappyPath(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, validServicesJSON())

	a, err := Load(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if a.SeverityHalfPoint() != 100000 {
		t.Errorf("SeverityHalfPoint = %v, want 100000", a.SeverityHalfPoint())
	}
	if err := a.SetPoolStaff("nursing", 5); err != nil {
		t.Errorf("SetPoolStaff(nursing) after Load: %v", err)
	}
	if _, err := a.BenchmarkRatio("police"); err != nil {
		t.Errorf("BenchmarkRatio(police) after Load: %v", err)
	}
}

// TestLoadServices_MalformedJSON (AC-11 / GR#1): malformed JSON is a
// registry-sourced ErrServiceDataInvalid, never a panic or silent default.
func TestLoadServices_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, `{ not valid json`)

	_, err := Load(dir, testCorrelationID())
	assertCode(t, err, ErrServiceDataInvalid)
}

// TestLoadServices_SchemaViolations exercises the module-specific Validate
// rules foundation/data's generic loader cannot know about. Each mutation
// must be rejected as ErrServiceDataInvalid.
func TestLoadServices_SchemaViolations(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(string) string
	}{
		{
			name: "negative wage",
			mutate: func(j string) string {
				return strings.Replace(j, `"wagePerStaffPerMonthMicropounds": 2000000000`, `"wagePerStaffPerMonthMicropounds": -1`, 1)
			},
		},
		{
			name: "empty pool members",
			mutate: func(j string) string {
				return strings.Replace(j, `"members": ["healthcare", "elder-care"]`, `"members": []`, 1)
			},
		},
		{
			name: "duplicate pool id",
			mutate: func(j string) string {
				return strings.Replace(j, `"staffingPools": [`, `"staffingPools": [
			{"id": "nursing", "label": "Nursing", "members": ["x"], "specRef": "§26"},
			{"id": "nursing", "label": "Nursing", "members": ["y"], "specRef": "§26"},`, 1)
			},
		},
		{
			name: "non-positive severity half-point",
			mutate: func(j string) string {
				return strings.Replace(j, `"severityHalfPointPopulation": 100000`, `"severityHalfPointPopulation": 0`, 1)
			},
		},
		{
			name: "benchmark with both denominators",
			mutate: func(j string) string {
				return strings.Replace(j, `"perThousand": 2.4, "perPupil": 0`, `"perThousand": 2.4, "perPupil": 1`, 1)
			},
		},
		{
			name: "benchmark with zero ratio",
			mutate: func(j string) string {
				return strings.Replace(j, `"perThousand": 2.4, "perPupil": 0`, `"perThousand": 0, "perPupil": 0`, 1)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFixture(t, dir, tc.mutate(validServicesJSON()))
			_, err := Load(dir, testCorrelationID())
			assertCode(t, err, ErrServiceDataInvalid)
		})
	}
}
