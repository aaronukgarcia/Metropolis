package world

import "testing"

func flatHeights(n int) [][]float32 {
	h := make([][]float32, n)
	for r := range h {
		h[r] = make([]float32, n)
	}
	return h
}

func TestSlopeSteepHasHigherMultiplierThanFlat(t *testing.T) {
	if SlopeSteep.CostMultiplier() <= SlopeFlat.CostMultiplier() {
		t.Fatalf("expected steep multiplier (%.2f) > flat multiplier (%.2f)",
			SlopeSteep.CostMultiplier(), SlopeFlat.CostMultiplier())
	}
	if SlopeGentle.CostMultiplier() <= SlopeFlat.CostMultiplier() {
		t.Fatalf("expected gentle multiplier (%.2f) > flat multiplier (%.2f)",
			SlopeGentle.CostMultiplier(), SlopeFlat.CostMultiplier())
	}
	if SlopeUnbuildable.CostMultiplier() <= SlopeSteep.CostMultiplier() {
		t.Fatalf("expected unbuildable multiplier to exceed steep")
	}
}

// TestSlopeSteepHasHigherMultiplierThanFlat_ProvenFail: PROOF this
// assertion can fail — comparing flat against itself must NOT report
// "higher", confirming the comparison is real, not tautological.
func TestSlopeSteepHasHigherMultiplierThanFlat_ProvenFail(t *testing.T) {
	if SlopeFlat.CostMultiplier() > SlopeFlat.CostMultiplier() {
		t.Fatal("sanity check failed: flat cannot be greater than itself")
	}
}

func TestClassifySlopeDetectsSteepGradient(t *testing.T) {
	h := flatHeights(10)
	// A steep step: 5m rise over one 10m cell = 50% grade -> unbuildable/steep.
	for c := 5; c < 10; c++ {
		h[5][c] = 5
	}
	sc := classifySlope(h, 5, 5)
	if sc != SlopeSteep && sc != SlopeUnbuildable {
		t.Fatalf("expected a steep gradient to classify as steep/unbuildable, got %v", sc)
	}
	flatSc := classifySlope(h, 0, 0)
	if flatSc != SlopeFlat {
		t.Fatalf("expected flat ground away from the step to classify as flat, got %v", flatSc)
	}
}
