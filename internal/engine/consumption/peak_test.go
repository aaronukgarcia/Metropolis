package consumption

import "testing"

// TestPeakLoadAtLeastBase is AC-9: a network distinguishes a peak-load
// figure from a base-load figure, and peak ≥ base for a synthetic demand
// profile (so generation/storage is sized against peak, not average).
func TestPeakLoadAtLeastBase(t *testing.T) {
	n := NewNetwork(UtilityPower, testCorrelationID())

	profile := []float64{10, 5, 20, 8, 15}
	peak := n.PeakLoad(profile)
	base := n.BaseLoad(profile)

	if peak < base {
		t.Errorf("peak %v < base %v for profile %v", peak, base, profile)
	}
	if peak != 20 {
		t.Errorf("peak = %v, want 20 (the max of the profile)", peak)
	}
	if base != 5 {
		t.Errorf("base = %v, want 5 (the min of the profile)", base)
	}
}

// TestPeakLoadEmptyProfile documents the empty-profile boundary: both
// queries return 0 rather than panicking on an empty slice.
func TestPeakLoadEmptyProfile(t *testing.T) {
	n := NewNetwork(UtilityPower, testCorrelationID())
	if got := n.PeakLoad(nil); got != 0 {
		t.Errorf("PeakLoad(nil) = %v, want 0", got)
	}
	if got := n.BaseLoad(nil); got != 0 {
		t.Errorf("BaseLoad(nil) = %v, want 0", got)
	}
}
