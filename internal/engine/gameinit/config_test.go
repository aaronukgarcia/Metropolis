package gameinit

import (
	"os"
	"path/filepath"
	"testing"
)

func writeGameInitJSON(t *testing.T, dir string, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, FileGameInit), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

const validGameInitJSON = `{
  "version": 1,
  "$comment": "test fixture",
  "meta": {"module": "feat.gameinit", "bowCode": "FEAT-143"},
  "params": {
    "startingCapitalMicropounds": {
      "value": 5000000000,
      "unit": "micro-pounds",
      "placeholder": true,
      "disclosure": "test fixture placeholder"
    }
  }
}`

// TestLoadConfig_DataSourced (AC-6): the real-mode starting capital comes
// from the data file, is finite and strictly positive, and is NOT a bare
// Go literal — proven here by round-tripping a value this test controls,
// not by asserting any particular magnitude (GR#15).
func TestLoadConfig_DataSourced(t *testing.T) {
	dir := t.TempDir()
	writeGameInitJSON(t, dir, validGameInitJSON)

	cfg, err := LoadConfig(dir, "t-load")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := cfg.StartingCapitalMicropounds(); got != 5000000000 {
		t.Fatalf("StartingCapitalMicropounds() = %d, want 5000000000 (round-tripped from the data file)", got)
	}
}

// TestLoadConfig_RejectsNonPositiveCapital (AC-6's directional check: real
// mode starts with a finite POSITIVE capital — zero or negative would
// make "real mode" indistinguishable from an already-insolvent genesis).
func TestLoadConfig_RejectsNonPositiveCapital(t *testing.T) {
	for _, value := range []string{"0", "-100"} {
		dir := t.TempDir()
		body := `{
			"version": 1, "$comment": "x",
			"meta": {"module": "feat.gameinit", "bowCode": "FEAT-143"},
			"params": {"startingCapitalMicropounds": {"value": ` + value + `, "unit": "micro-pounds", "disclosure": "x"}}
		}`
		writeGameInitJSON(t, dir, body)
		if _, err := LoadConfig(dir, "t-nonpositive"); err == nil {
			t.Fatalf("LoadConfig with startingCapitalMicropounds=%s succeeded, want ErrGameInitDataInvalid", value)
		}
	}
}

// TestLoadConfig_RejectsMissingDisclosure (GR#15's disclosure requirement
// mirrors deathservices.Config's own schema check).
func TestLoadConfig_RejectsMissingDisclosure(t *testing.T) {
	dir := t.TempDir()
	body := `{
		"version": 1, "$comment": "x",
		"meta": {"module": "feat.gameinit", "bowCode": "FEAT-143"},
		"params": {"startingCapitalMicropounds": {"value": 5000000000, "unit": "micro-pounds"}}
	}`
	writeGameInitJSON(t, dir, body)
	if _, err := LoadConfig(dir, "t-nodisclosure"); err == nil {
		t.Fatalf("LoadConfig with no disclosure succeeded, want ErrGameInitDataInvalid")
	}
}

// TestLoadConfig_RejectsInt64Overflow (AC-6 round finding P3): a value
// that passes the plain finite+positive float64 check can still overflow
// the int64 cast StartingCapitalMicropounds performs (1e30 wraps to a
// large NEGATIVE int64), which would silently defeat AC-6's "finite
// positive capital" guarantee at the accessor rather than at validation
// time. validate must reject it outright, with a registry error.
func TestLoadConfig_RejectsInt64Overflow(t *testing.T) {
	dir := t.TempDir()
	body := `{
		"version": 1, "$comment": "x",
		"meta": {"module": "feat.gameinit", "bowCode": "FEAT-143"},
		"params": {"startingCapitalMicropounds": {"value": 1e30, "unit": "micro-pounds", "disclosure": "x"}}
	}`
	writeGameInitJSON(t, dir, body)
	cfg, err := LoadConfig(dir, "t-overflow")
	if err == nil {
		t.Fatalf("LoadConfig with startingCapitalMicropounds=1e30 succeeded, StartingCapitalMicropounds()=%d -- want a registry error (int64 overflow)", cfg.StartingCapitalMicropounds())
	}
}

// TestLoadConfig_MissingFile proves a missing data file fails rather than
// silently substituting a default (GR#15).
func TestLoadConfig_MissingFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadConfig(dir, "t-missing"); err == nil {
		t.Fatalf("LoadConfig with no data file succeeded, want ErrGameInitDataInvalid")
	}
}

// TestLoad_UnlimitedDoesNotDependOnCapitalMagnitude (AC-2's "genuine
// bypass, not a huge balance" ruling): Unlimited mode loads successfully
// from the SAME config as Real mode, and StartingCapitalMicropounds still
// reports the data-sourced real-mode figure regardless of the locked
// mode — an Unlimited GameInit is never internally represented as "a
// really big number".
func TestLoad_UnlimitedDoesNotDependOnCapitalMagnitude(t *testing.T) {
	dir := t.TempDir()
	writeGameInitJSON(t, dir, validGameInitJSON)

	real, err := Load(dir, ModeReal, "t-real")
	if err != nil {
		t.Fatalf("Load(ModeReal): %v", err)
	}
	unlimited, err := Load(dir, ModeUnlimited, "t-unlimited")
	if err != nil {
		t.Fatalf("Load(ModeUnlimited): %v", err)
	}
	realCapital, err := real.StartingCapitalMicropounds("t-real")
	if err != nil {
		t.Fatalf("real.StartingCapitalMicropounds: %v", err)
	}
	unlimitedCapital, err := unlimited.StartingCapitalMicropounds("t-unlimited")
	if err != nil {
		t.Fatalf("unlimited.StartingCapitalMicropounds: %v", err)
	}
	if realCapital != unlimitedCapital {
		t.Fatalf("real and unlimited disagree on the data-sourced figure: %d vs %d", realCapital, unlimitedCapital)
	}
	isUnlimited, err := unlimited.Unlimited("t-unlimited")
	if err != nil {
		t.Fatalf("unlimited.Unlimited: %v", err)
	}
	if !isUnlimited {
		t.Fatalf("unlimited.Unlimited() = false, want true")
	}
}
