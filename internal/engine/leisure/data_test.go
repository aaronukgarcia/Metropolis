package leisure

import "testing"

// TestLoadDefaultData proves data/leisure.json is present, well-formed, and
// schema-valid (GR#15: every numeric magnitude is data-sourced, and a broken
// data file must fail loudly rather than silently default). It also proves
// the load-time registry error surfaces on a missing file.
func TestLoadDefaultData(t *testing.T) {
	a, err := LoadDefault("test")
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	if a.cfg.HoursPerWeek != 168 {
		t.Fatalf("hoursPerWeek = %v, want 168 (§42)", a.cfg.HoursPerWeek)
	}
}

// TestLoadMissingData proves the load-time error is registry-sourced.
func TestLoadMissingData(t *testing.T) {
	if _, err := Load(t.TempDir(), "test"); err == nil {
		t.Fatal("Load must fail for a missing data file")
	} else {
		assertErrCode(t, err, ErrLeisureDataInvalid)
	}
}
