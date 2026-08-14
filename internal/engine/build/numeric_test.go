package build

import (
	"math"
	"testing"
)

// This file covers engine.build's remaining local numeric helper
// (effectiveLeadTime). The shared saturating-arithmetic helpers that used
// to live in this package's numeric.go now live in foundation/num and are
// covered by foundation/num's own test suite (FEAT-086 DRY refactor).

func TestEffectiveLeadTimeSeasonalDirection(t *testing.T) {
	base := int64(45)
	// Winter slowdown (<1.0) lengthens lead time; summer (>=1.0) does not
	// shorten below base.
	winter := effectiveLeadTime(base, 0.8)
	summer := effectiveLeadTime(base, 1.0)
	if winter <= summer {
		t.Errorf("winter effective lead time %d should exceed summer %d", winter, summer)
	}
	if summer != base {
		t.Errorf("summer effective lead time = %d, want base %d", summer, base)
	}

	// Defensive: a non-positive/NaN/Inf multiplier saturates rather than
	// dividing by zero or producing +Inf.
	if got := effectiveLeadTime(base, 0); got != math.MaxInt64 {
		t.Errorf("effectiveLeadTime(base, 0) = %d, want MaxInt64", got)
	}
	if got := effectiveLeadTime(base, math.NaN()); got != math.MaxInt64 {
		t.Errorf("effectiveLeadTime(base, NaN) = %d, want MaxInt64", got)
	}
	if got := effectiveLeadTime(base, math.Inf(1)); got != math.MaxInt64 {
		t.Errorf("effectiveLeadTime(base, +Inf) = %d, want MaxInt64", got)
	}
	// A MaxInt64 base never wraps negative through the float round-trip.
	if got := effectiveLeadTime(math.MaxInt64, 1.0); got <= 0 {
		t.Errorf("effectiveLeadTime(MaxInt64, 1.0) = %d, want positive (no wrap)", got)
	}
}
