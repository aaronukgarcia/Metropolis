package fiscal

import (
	"testing"
)

// TestMunicipalityQualitySweep sweeps the planning funding level across low /
// mid / high and asserts each of the four outputs moves in its documented
// direction (AC-5): permit speed rises, build-cost error falls, layout bonus
// rises, corruption risk never rises (and in fact falls) — with corruption
// specifically asserted near-zero at mid/high and materially non-zero at the
// lowest tested level. The sweep-and-compare-across-levels shape is what
// rules out a constant-output "dial connected to nothing".
func TestMunicipalityQualitySweep(t *testing.T) {
	f, _, _ := newTestFiscal(t)

	levels := []struct {
		name string
		lvl  float64
	}{
		{"low", 0.0},
		{"mid", 0.5},
		{"high", 1.0},
	}

	var prevPermit, prevLayout, prevErr, prevCorrupt float64
	for i, lc := range levels {
		if err := f.SetPlanningFunding(lc.lvl); err != nil {
			t.Fatalf("SetPlanningFunding(%s): %v", lc.name, err)
		}
		permit := f.PermitSpeedMultiplier()
		errRate := f.BuildCostErrorRate()
		layout := f.LayoutQualityBonus()
		corrupt := f.CorruptionRisk()

		if i > 0 {
			if permit <= prevPermit {
				t.Errorf("%s permit-speed multiplier %v not > %s %v (higher funding must be faster)", lc.name, permit, levels[i-1].name, prevPermit)
			}
			if errRate >= prevErr {
				t.Errorf("%s build-cost error %v not < %s %v (lower funding must error higher)", lc.name, errRate, levels[i-1].name, prevErr)
			}
			if layout <= prevLayout {
				t.Errorf("%s layout bonus %v not > %s %v (higher funding must bonus higher)", lc.name, layout, levels[i-1].name, prevLayout)
			}
			if corrupt > prevCorrupt {
				t.Errorf("%s corruption risk %v rose above %s %v (corruption must not rise with funding)", lc.name, corrupt, levels[i-1].name, prevCorrupt)
			}
		}
		prevPermit, prevLayout, prevErr, prevCorrupt = permit, layout, errRate, corrupt
	}

	// Corruption specifically: near-zero at mid/high, materially non-zero at
	// rock-bottom funding (the "only rises at the low end" §54 claim).
	if err := f.SetPlanningFunding(0.5); err != nil {
		t.Fatal(err)
	}
	if r := f.CorruptionRisk(); r != 0 {
		t.Errorf("CorruptionRisk at mid funding = %v, want 0 (near-zero)", r)
	}
	if err := f.SetPlanningFunding(1.0); err != nil {
		t.Fatal(err)
	}
	if r := f.CorruptionRisk(); r != 0 {
		t.Errorf("CorruptionRisk at full funding = %v, want 0", r)
	}
	if err := f.SetPlanningFunding(0.0); err != nil {
		t.Fatal(err)
	}
	if r := f.CorruptionRisk(); r <= 0.3 {
		t.Errorf("CorruptionRisk at rock-bottom funding = %v, want materially non-zero (> 0.3)", r)
	}
}

// TestSetPlanningFundingBoundary asserts the funding-level input boundary
// (AC-5): a level outside [0,1] or non-finite is rejected, never silently
// clamped.
func TestSetPlanningFundingBoundary(t *testing.T) {
	f, _, _ := newTestFiscal(t)
	for _, bad := range []float64{-0.1, 1.1, 2.0} {
		if err := f.SetPlanningFunding(bad); err == nil {
			t.Errorf("SetPlanningFunding(%v) returned nil error, want ErrInvalidFundingLevel", bad)
		}
	}
	// The previous (valid) level is preserved, not silently clamped.
	if err := f.SetPlanningFunding(0.75); err != nil {
		t.Fatalf("SetPlanningFunding(0.75): %v", err)
	}
	if err := f.SetPlanningFunding(1.5); err == nil {
		t.Fatal("SetPlanningFunding(1.5) returned nil error")
	}
	if got := f.PlanningFunding(); got != 0.75 {
		t.Errorf("PlanningFunding() = %v after rejected set, want 0.75 (unchanged)", got)
	}
}
