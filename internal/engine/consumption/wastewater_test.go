package consumption

import "testing"

// TestWastewaterOutput is AC-5: wastewater output defaults to 95% of the
// same entity's water draw. For the §17.1 baseline figures, 0.95 × 145 L
// = 137.75 L, which is the spec's "~138 L (95% rule)".
func TestWastewaterOutput(t *testing.T) {
	api := realAPI(t)

	if got := api.WastewaterFraction(); got != 0.95 {
		t.Errorf("wastewater fraction = %v, want 0.95 (§17)", got)
	}

	got := api.WastewaterOutput(145.0)
	want := 0.95 * 145.0 // 137.75 ≈ 138 (§17.1 "~138 L")
	if got != want {
		t.Errorf("residential wastewater = %v, want 0.95 × 145 = %v", got, want)
	}
}

// TestWastewaterOutputScalesWithWater proves the 95% rule is a proportion,
// not a fixed 138L constant: doubling the water draw doubles the
// wastewater output.
func TestWastewaterOutputScalesWithWater(t *testing.T) {
	api := realAPI(t)
	if got := api.WastewaterOutput(300.0); got != 0.95*300.0 {
		t.Errorf("wastewater(300 L) = %v, want %v", got, 0.95*300.0)
	}
}
