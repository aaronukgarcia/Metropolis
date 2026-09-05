package finance

import "testing"

// TestLoadDefaultOpexConfig proves data/opexintegration.json loads and
// validates cleanly, and every value the AC-6 major-drain test derives
// from it is positive (GR#15 — a genuine engineering-scaled figure
// derived from a data file, never a Go literal).
func TestLoadDefaultOpexConfig(t *testing.T) {
	cfg, err := LoadDefaultOpexConfig("opex-data-load")
	if err != nil {
		t.Fatalf("LoadDefaultOpexConfig: %v", err)
	}
	if cfg.CostPerEngineerDay <= 0 {
		t.Fatalf("CostPerEngineerDay = %d, want positive", cfg.CostPerEngineerDay)
	}
	if cfg.BacklogEfficiencyDivisor <= 0 {
		t.Fatalf("BacklogEfficiencyDivisor = %d, want positive", cfg.BacklogEfficiencyDivisor)
	}
	if cfg.MajorDrainMinFractionBps <= 0 || cfg.MajorDrainMinFractionBps > 10000 {
		t.Fatalf("MajorDrainMinFractionBps = %d, want (0, 10000]", cfg.MajorDrainMinFractionBps)
	}
}

// TestSetOpexConfigInstallsAndReports proves SetOpexConfig/OpexConfig
// round-trip and that OpexConfig reports "not set" before it is called.
func TestSetOpexConfigInstallsAndReports(t *testing.T) {
	f := NewFinanceAPI("opex-config-roundtrip")
	if _, ok := f.OpexConfig(); ok {
		t.Fatal("expected no config before SetOpexConfig")
	}
	want := testOpexConfig()
	if err := f.SetOpexConfig(want); err != nil {
		t.Fatalf("SetOpexConfig: %v", err)
	}
	got, ok := f.OpexConfig()
	if !ok {
		t.Fatal("expected config set")
	}
	if got != want {
		t.Fatalf("OpexConfig = %+v, want %+v", got, want)
	}
}
