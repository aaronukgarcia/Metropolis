package deathservices

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// assertRegistryCode fails the test unless err is a registry-sourced *errs.E
// carrying the given code (GR#7's "test asserts error code match" contract
// for AC-17).
func assertRegistryCode(t *testing.T, err error, code string) {
	t.Helper()
	e, ok := err.(*errs.E)
	if !ok {
		t.Fatalf("error %v (%T) is not a *errs.E registry-sourced error", err, err)
	}
	if e.Code != code {
		t.Fatalf("error code = %s, want %s", e.Code, code)
	}
}

// writeConfigFixture writes a modified copy of the real
// data/deathservices.json into a fresh t.TempDir(), applying mutate to the
// loaded default config before re-encoding, then returns the directory
// (LoadConfig-ready). This is how AC-3's "mutate the fixture's horizon
// value and assert the boundary shifts with the data" check proves the
// mechanism is DATA-driven, not compiled (GR#15) -- mirrors
// citizens.deathwave_test.go's own t.TempDir()+os.WriteFile(FileMortality)
// idiom exactly, applied to this package's own config file.
func writeConfigFixture(t *testing.T, mutate func(*Config)) Config {
	t.Helper()
	cfg := testConfig(t)
	if mutate != nil {
		mutate(&cfg)
	}
	dir := t.TempDir()
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("json.Marshal fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileDeathServices), b, 0o644); err != nil {
		t.Fatalf("WriteFile fixture: %v", err)
	}
	loaded, err := LoadConfig(dir, "corr")
	if err != nil {
		t.Fatalf("LoadConfig fixture: %v", err)
	}
	return loaded
}
