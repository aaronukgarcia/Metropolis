package firms

import (
	"math"
	"testing"
)

// MOD-034 round: the productivity getter is caller-supplied and unvalidated.
// applyInputScalingLocked does `int64(float64(scale) * mod)` before
// clampPerMille -- an int64 conversion of NaN/+Inf is implementation-defined
// in Go, so prove OutputScale still lands inside [0,1000] for every hostile
// value a wellbeing getter could return.
func TestAttackMOD034_OutputScaleClampSurvivesHostileModifier(t *testing.T) {
	for _, mod := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -1, -1e300, 1e300, 0, 0.5, 1, 2, 1e18} {
		mod := mod
		f, err := LoadDefault(0, "attack-mod034")
		if err != nil {
			t.Fatalf("LoadDefault: %v", err)
		}
		if err := f.SetProductivityModifier(func() float64 { return mod }); err != nil {
			t.Fatalf("SetProductivityModifier(%v): %v", mod, err)
		}
		fs := &firmState{firm: Firm{Financial: Financial{OutputScale: 1000}}}
		f.mu.Lock()
		f.applyInputScalingLocked(fs)
		f.mu.Unlock()
		got := fs.firm.Financial.OutputScale
		if got < 0 || got > 1000 {
			t.Fatalf("modifier %v produced OutputScale %d outside [0,1000]", mod, got)
		}
		t.Logf("modifier %v -> OutputScale %d", mod, got)
	}
}

// A nil getter must be a documented exact no-op.
func TestAttackMOD034_NilProductivityGetterIsNeutral(t *testing.T) {
	f, err := LoadDefault(0, "attack-mod034")
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	fs := &firmState{firm: Firm{Financial: Financial{OutputScale: 0}}}
	f.mu.Lock()
	f.applyInputScalingLocked(fs)
	f.mu.Unlock()
	if fs.firm.Financial.OutputScale != 1000 {
		t.Fatalf("nil getter: OutputScale=%d, want 1000 (neutral)", fs.firm.Financial.OutputScale)
	}
	// Explicitly setting nil after a real getter must restore neutral.
	if err := f.SetProductivityModifier(func() float64 { return 0.5 }); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := f.SetProductivityModifier(nil); err != nil {
		t.Fatalf("Set(nil): %v", err)
	}
	fs2 := &firmState{firm: Firm{Financial: Financial{OutputScale: 0}}}
	f.mu.Lock()
	f.applyInputScalingLocked(fs2)
	f.mu.Unlock()
	if fs2.firm.Financial.OutputScale != 1000 {
		t.Fatalf("nil-restore: OutputScale=%d, want 1000", fs2.firm.Financial.OutputScale)
	}
}
