package maintenance

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// testConfig returns a small in-memory Config fixture standing in for
// data/maintenance.json in tests that exercise the runtime path directly.
// Every magnitude here is a TEST FIXTURE, not a production balance value —
// the production values live in data/maintenance.json (GR#15).
func testConfig() Config {
	return Config{
		Classes: map[Class]ClassConfig{
			"dwelling":       {EngineerDaysPerYear: 10, LifetimeYears: 50},
			"shop":           {EngineerDaysPerYear: 8, LifetimeYears: 40},
			"heavy_industry": {EngineerDaysPerYear: 40, LifetimeYears: 25},
		},
		CrewCostPerEngineerDay:       100,
		ContractorCostPerEngineerDay: 300,
	}
}

// newTestAPI constructs a ready MaintenanceAPI from testConfig.
func newTestAPI(t *testing.T) *MaintenanceAPI {
	t.Helper()
	a, err := New(testConfig(), "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

// mustTotalBacklog returns the city-wide backlog, failing the test if the
// query errors (e.g. a copy-guard rejection). Test-goroutine use only.
func mustTotalBacklog(t *testing.T, a *MaintenanceAPI) int64 {
	t.Helper()
	total, err := a.TotalBacklog("test")
	if err != nil {
		t.Fatalf("TotalBacklog: %v", err)
	}
	return total
}

// wantCode asserts err is a registry-sourced *errs.E carrying the exact code,
// via errors.Is (GR#7 — not merely a non-nil error).
func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("got nil error, want code %s", code)
	}
	if !errors.Is(err, &errs.E{Code: code}) {
		t.Fatalf("err = %v, want code %s", err, code)
	}
}

// loadFixture writes the given JSON to a fresh temp dir as maintenance.json
// and Loads it, returning the API and any error.
func loadFixture(t *testing.T, jsonBody string) (*MaintenanceAPI, error) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, fileName)
	if err := os.WriteFile(path, []byte(jsonBody), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	return Load(dir, "test")
}

// validFixture is a well-formed data/maintenance.json body for the load-path
// tests.
const validFixture = `{
  "version": 1,
  "meta": {"note": "placeholder", "rateUnit": "engineer-days per year", "lifetimeUnit": "simulation-years", "costUnit": "micro-pounds per engineer-day"},
  "crewCostPerEngineerDay": 100,
  "contractorCostPerEngineerDay": 300,
  "classes": {
    "dwelling": {"engineerDaysPerYear": 10, "lifetimeYears": 50, "disclosure": "placeholder pending Aaron's balance pass"},
    "shop": {"engineerDaysPerYear": 8, "lifetimeYears": 40, "disclosure": "placeholder pending Aaron's balance pass"}
  }
}`
