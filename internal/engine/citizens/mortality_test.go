package citizens

import "testing"

// TestMortalityHazardIncreasesWithAge (AC-11): for fixed health/access the
// Gompertz-Makeham hazard is monotonically increasing in age.
func TestMortalityHazardIncreasesWithAge(t *testing.T) {
	h0 := MortalityHazard(0, HealthGood, 100)
	h40 := MortalityHazard(12*40, HealthGood, 100)
	h80 := MortalityHazard(12*80, HealthGood, 100)
	if !(h0 < h40 && h40 < h80) {
		t.Fatalf("hazard must increase with age, got h(0)=%g h(40)=%g h(80)=%g", h0, h40, h80)
	}
}

// TestMortalityHazardDecreasesWithBetterHealth (AC-11, extra directional):
// for fixed age/access, a better health band lowers the hazard.
func TestMortalityHazardDecreasesWithBetterHealth(t *testing.T) {
	poor := MortalityHazard(12*60, HealthPoor, 50)
	excellent := MortalityHazard(12*60, HealthExcellent, 50)
	if !(poor > excellent) {
		t.Fatalf("worse health must raise hazard, got poor=%g excellent=%g", poor, excellent)
	}
}

// TestMortalityHazardDecreasesWithBetterAccess (AC-11, extra directional):
// for fixed age/health, more healthcare access lowers the hazard.
func TestMortalityHazardDecreasesWithBetterAccess(t *testing.T) {
	noAccess := MortalityHazard(12*60, HealthGood, 0)
	fullAccess := MortalityHazard(12*60, HealthGood, 100)
	if !(noAccess > fullAccess) {
		t.Fatalf("better access must lower hazard, got 0=%g 100=%g", noAccess, fullAccess)
	}
}

// TestMortalityHazardClamped: the hazard never leaves [0,1], even at absurd
// ages/inputs (GR#16: never trust a stored field's declared range).
func TestMortalityHazardClamped(t *testing.T) {
	for _, age := range []int64{0, 12 * 200, 12 * 1000} {
		h := MortalityHazard(age, HealthCritical, 0)
		if h < 0 || h > 1 {
			t.Fatalf("hazard %g out of [0,1] at age %d", h, age)
		}
	}
	// Out-of-range access/band inputs degrade, never produce NaN or >1.
	if h := MortalityHazard(12*100, 200, 255); h < 0 || h > 1 {
		t.Fatalf("out-of-range inputs produced hazard %g", h)
	}
}

// TestMortalityDeathDeterministic (AC-15): the per-person death decision is
// a pure function of (seed, id, month, age, health, access).
func TestMortalityDeathDeterministic(t *testing.T) {
	a := MortalityDeath(99, 42, 10, 12*50, HealthGood, 80)
	b := MortalityDeath(99, 42, 10, 12*50, HealthGood, 80)
	if a != b {
		t.Fatalf("MortalityDeath not deterministic: %v vs %v", a, b)
	}
}
