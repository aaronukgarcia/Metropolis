package defence

import "testing"

// TestLoadDefault_ReadsRealData loads the shipped data/defence.json and
// asserts it is schema-valid and its mandate events are readable (GR#15: the
// real data file is the source of truth, and this test catches a malformed
// data edit at the load boundary).
func TestLoadDefault_ReadsRealData(t *testing.T) {
	d, err := LoadDefault(7, "corr-defence")
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	naval := d.PendingMandates(100_000)
	if !hasMandate(naval, "naval-100k") {
		t.Fatal("shipped data/defence.json did not fire the naval mandate at 100k")
	}
	// The mandate must offer ≥2 compliant choices from the real data file.
	for _, m := range naval {
		if m.ID == "naval-100k" && len(m.Choices) < 2 {
			t.Fatalf("naval mandate offers %d choices, want >= 2", len(m.Choices))
		}
	}
}

// TestNew_RejectsBadConfig asserts the Validate gate rejects a config whose
// facility table is missing a referenced facility type (GR#15/GR#17 — the
// reference is enforced at load, never a missing-config index at runtime).
func TestNew_RejectsBadConfig(t *testing.T) {
	cfg := validConfig()
	cfg.Facilities = map[string]FacilityConfig{} // empty — mandates reference missing types
	if _, err := New(cfg, 1, "corr-defence"); err == nil {
		t.Fatal("New accepted a config missing referenced facility types")
	}
}

// TestNew_RejectsSingleChoiceMandate asserts a mandate with fewer than two
// choices is rejected (AC-5's choice-within-compliance is a load-time
// invariant, not merely a runtime nicety).
func TestNew_RejectsSingleChoiceMandate(t *testing.T) {
	cfg := validConfig()
	cfg.Mandates[0].Choices = cfg.Mandates[0].Choices[:1]
	if _, err := New(cfg, 1, "corr-defence"); err == nil {
		t.Fatal("New accepted a mandate with a single compliant choice")
	}
}

// TestNew_RejectsFloorAboveNominal asserts a payroll floor above the nominal
// wage bill is rejected (the floor is a guarantee of "at least floor" bounded
// by the nominal — a floor above nominal is a mis-authored data set).
func TestNew_RejectsFloorAboveNominal(t *testing.T) {
	cfg := validConfig()
	fc := cfg.Facilities["naval"]
	fc.PayrollFloorMicropounds = fc.PayrollMicropounds + 1
	cfg.Facilities["naval"] = fc
	if _, err := New(cfg, 1, "corr-defence"); err == nil {
		t.Fatal("New accepted a payroll floor above the nominal wage bill")
	}
}
