package num

import (
	"math"
	"math/big"
	"testing"
)

// This file proves the GR#16 numeric-safety mandate: every helper is
// checked against a math/big reference over a cross-product of extreme
// int64 values (plus the specific mixed-sign cases the Destructive rounds
// fuzzed), so a MaxInt64 / MinInt64 / mixed-sign / NaN / ±Inf input can
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
			if got := SatAdd(a, b); got != want {
				t.Errorf("SatAdd(%d, %d) = %d, want %d", a, b, got, want)
			}
			got, over := SatAddChecked(a, b)
			if got != want {
				t.Errorf("SatAddChecked(%d, %d) = %d, want %d", a, b, got, want)
			}
			if wantOver := ref.Cmp(big.NewInt(math.MaxInt64)) > 0 || ref.Cmp(big.NewInt(math.MinInt64)) < 0; over != wantOver {
				t.Errorf("SatAddChecked(%d, %d) overflow = %v, want %v", a, b, over, wantOver)
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
			if got := SatSub(a, b); got != want {
				t.Errorf("SatSub(%d, %d) = %d, want %d", a, b, got, want)
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
			got, gotOverflow := SafeMul(a, b)
			if got != want {
				t.Errorf("SafeMul(%d, %d) = %d, want %d", a, b, got, want)
			}
			if gotOverflow != overflow {
				t.Errorf("SafeMul(%d, %d) overflow = %v, want %v", a, b, gotOverflow, overflow)
			}
		}
	}
}

func TestSafeMulMixedSignExtremes(t *testing.T) {
	// The specific cases the Destructive fuzzes: mixed-sign products whose
	// magnitude exceeds int64 even though the product is negative, plus the
	// representable negative boundary (which must NOT be flagged).
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
		{2, -(1 << 62), math.MinInt64, false}, // exactly MinInt64: not overflow
		{-5, 3, -15, false},
		{5, -3, -15, false},
		{6, 7, 42, false},
		{0, math.MaxInt64, 0, false},
	}
	for _, c := range cases {
		got, overflow := SafeMul(c.a, c.b)
		if got != c.want || overflow != c.overflow {
			t.Errorf("SafeMul(%d, %d) = (%d, %v), want (%d, %v)", c.a, c.b, got, overflow, c.want, c.overflow)
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
		{12.5, 12},
	}
	for _, c := range cases {
		if got := ClampInt64FromFloat(c.f); got != c.want {
			t.Errorf("ClampInt64FromFloat(%v) = %d, want %d", c.f, got, c.want)
		}
	}
}

// TestClampInt64FromUint64NeverWraps proves BUG-305's guard: a bare
// int64(x) for x > math.MaxInt64 wraps into a large NEGATIVE value
// (well-defined truncation, but numerically wrong for a byte/count
// delta) -- ClampInt64FromUint64 must saturate at MaxInt64 instead. This
// test fails against a naive int64(x) implementation, proving it can
// catch the regression it guards against.
func TestClampInt64FromUint64NeverWraps(t *testing.T) {
	cases := []struct {
		x    uint64
		want int64
	}{
		{0, 0},
		{1, 1},
		{math.MaxInt64, math.MaxInt64},
		{math.MaxInt64 + 1, math.MaxInt64}, // would wrap to MinInt64 via a bare int64(x)
		{math.MaxUint64, math.MaxInt64},
	}
	for _, c := range cases {
		if got := ClampInt64FromUint64(c.x); got != c.want {
			t.Errorf("ClampInt64FromUint64(%d) = %d, want %d", c.x, got, c.want)
		}
	}
}

func TestIsFiniteAndGuardFinite(t *testing.T) {
	for _, f := range []float64{0, 1, -1, math.MaxFloat64, math.SmallestNonzeroFloat64} {
		if !IsFinite(f) {
			t.Errorf("IsFinite(%v) = false, want true", f)
		}
		if v, ok := GuardFinite(f); !ok || v != f {
			t.Errorf("GuardFinite(%v) = (%v, %v), want (%v, true)", f, v, ok, f)
		}
	}
	for _, f := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if IsFinite(f) {
			t.Errorf("IsFinite(%v) = true, want false", f)
		}
		if v, ok := GuardFinite(f); ok || v != 0 {
			t.Errorf("GuardFinite(%v) = (%v, %v), want (0, false)", f, v, ok)
		}
	}
}
