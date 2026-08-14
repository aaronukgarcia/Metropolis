package build

import (
	"math"
	"math/big"
	"testing"
)

// This file proves the FEAT-086 numeric-safety mandate: every saturating
// helper is checked against a math/big reference over a cross-product of
// extreme int64 values, so a MaxInt64 / MinInt64 / mixed-sign input can
// never wrap negative, produce +Inf/NaN, or invent/destroy units.

var extremeInt64s = []int64{
	math.MinInt64, math.MinInt64 + 1, -1 << 62, -1 << 31, -2, -1,
	0, 1, 2, 1 << 31, 1 << 62, math.MaxInt64 - 1, math.MaxInt64,
}

// clampToInt64 clamps a big.Int to the int64 range — the reference
// saturation every saturating helper must agree with.
func clampToInt64(r *big.Int) int64 {
	if r.Cmp(big.NewInt(math.MaxInt64)) > 0 {
		return math.MaxInt64
	}
	if r.Cmp(big.NewInt(math.MinInt64)) < 0 {
		return math.MinInt64
	}
	return r.Int64()
}

func TestSatAddNeverWraps(t *testing.T) {
	for _, a := range extremeInt64s {
		for _, b := range extremeInt64s {
			ref := big.NewInt(a)
			ref.Add(ref, big.NewInt(b))
			want := clampToInt64(ref)
			if got := satAdd(a, b); got != want {
				t.Errorf("satAdd(%d, %d) = %d, want %d", a, b, got, want)
			}
		}
	}
}

func TestSatSubNeverWraps(t *testing.T) {
	for _, a := range extremeInt64s {
		for _, b := range extremeInt64s {
			ref := big.NewInt(a)
			ref.Sub(ref, big.NewInt(b))
			want := clampToInt64(ref)
			if got := satSub(a, b); got != want {
				t.Errorf("satSub(%d, %d) = %d, want %d", a, b, got, want)
			}
		}
	}
}

func TestSafeMulNeverWraps(t *testing.T) {
	for _, a := range extremeInt64s {
		for _, b := range extremeInt64s {
			ref := big.NewInt(a)
			ref.Mul(ref, big.NewInt(b))
			want := clampToInt64(ref)
			overflow := ref.Cmp(big.NewInt(math.MaxInt64)) > 0 || ref.Cmp(big.NewInt(math.MinInt64)) < 0
			got, gotOverflow := safeMul(a, b)
			if got != want {
				t.Errorf("safeMul(%d, %d) = %d, want %d", a, b, got, want)
			}
			if gotOverflow != overflow {
				t.Errorf("safeMul(%d, %d) overflow = %v, want %v", a, b, gotOverflow, overflow)
			}
		}
	}
}

func TestSafeMulMixedSignExtremes(t *testing.T) {
	// The specific cases the Destructive fuzzes: mixed-sign products whose
	// magnitude exceeds int64 even though the product is negative.
	cases := []struct {
		a, b     int64
		want     int64
		overflow bool
	}{
		{math.MaxInt64, 2, math.MaxInt64, true},
		{math.MaxInt64, -2, math.MinInt64, true},
		{math.MinInt64, 2, math.MinInt64, true},
		{math.MinInt64, -1, math.MaxInt64, true}, // |MinInt64| * 1 = 2^63 overflows
		{math.MinInt64, 1, math.MinInt64, false},
		{-1, math.MinInt64, math.MaxInt64, true},
	}
	for _, c := range cases {
		got, overflow := safeMul(c.a, c.b)
		if got != c.want || overflow != c.overflow {
			t.Errorf("safeMul(%d, %d) = (%d, %v), want (%d, %v)", c.a, c.b, got, overflow, c.want, c.overflow)
		}
	}
}

func TestClampInt64FromFloatNeverWraps(t *testing.T) {
	cases := []struct {
		f    float64
		want int64
	}{
		{float64(math.MaxInt64), math.MaxInt64}, // 2^63, would wrap negative without a clamp
		{float64(math.MaxInt64) + 1, math.MaxInt64},
		{math.Inf(1), math.MaxInt64},
		{math.NaN(), 0},
		{float64(math.MinInt64), math.MinInt64},
		{math.Inf(-1), math.MinInt64},
		{0, 0},
		{123.0, 123},
		{-123.0, -123},
	}
	for _, c := range cases {
		if got := clampInt64FromFloat(c.f); got != c.want {
			t.Errorf("clampInt64FromFloat(%v) = %d, want %d", c.f, got, c.want)
		}
	}
}

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
