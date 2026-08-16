package num

import (
	"math"
	"math/bits"
)

// SatAdd returns a+b saturated at the int64 extremes when the true sum
// overflows, instead of silently wrapping (GR#16). The predicate is the
// two's-complement one: a positive b must not leave the result below a,
// and a negative b must not leave it above a.
func SatAdd(a, b int64) int64 {
	c := a + b
	if b > 0 && c < a {
		return math.MaxInt64
	}
	if b < 0 && c > a {
		return math.MinInt64
	}
	return c
}

// SatSub returns a-b saturated at the int64 extremes when the true
// difference overflows (GR#16). Handled directly (not SatAdd(a, -b))
// because negating math.MinInt64 itself overflows int64.
func SatSub(a, b int64) int64 {
	c := a - b
	if b < 0 && c < a {
		return math.MaxInt64
	}
	if b > 0 && c > a {
		return math.MinInt64
	}
	return c
}

// SatAddChecked returns a+b saturated at the int64 extremes, plus whether
// saturation occurred. Callers that must distinguish "summed exactly" from
// "saturated" (e.g. a ledger that rejects an overflowing debit/credit sum
// rather than accepting a wrapped balance, GR#16) use the bool; callers
// that only need the clamped value use [SatAdd].
func SatAddChecked(a, b int64) (int64, bool) {
	c := a + b
	if b > 0 && c < a {
		return math.MaxInt64, true
	}
	if b < 0 && c > a {
		return math.MinInt64, true
	}
	return c, false
}

// SafeMul returns a*b and whether the product overflows int64. On
// overflow it returns the saturated value (MaxInt64 for positive
// overflow, MinInt64 for negative underflow) and true. It uses a
// full-width 128-bit multiply (math/bits.Mul64) over the operands'
// magnitudes, so every sign combination is checked exactly — including
// mixed signs, where |a*b| can exceed int64's range even though the
// product is negative (e.g. MaxInt64 * -2 underflows, MinInt64 * 2
// underflows). A division-based check would be wrong here: MinInt64/-1
// itself overflows, so `a > MinInt64/b` mis-flags b = -1.
func SafeMul(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, false
	}
	negative := (a < 0) != (b < 0)

	hi, lo := bits.Mul64(absU64(a), absU64(b))
	if hi != 0 {
		// Magnitude product exceeds 2^64-1, so it certainly exceeds the
		// int64 range regardless of sign.
		if negative {
			return math.MinInt64, true
		}
		return math.MaxInt64, true
	}

	if negative {
		// Result is in [-2^63, -1]. The largest representable magnitude
		// is 2^63 (= |MinInt64|).
		if lo > 1<<63 {
			return math.MinInt64, true
		}
		if lo == 1<<63 {
			return math.MinInt64, false // exactly MinInt64
		}
		return -int64(lo), false
	}

	// Non-negative result: valid iff lo <= MaxInt64.
	if lo > uint64(math.MaxInt64) {
		return math.MaxInt64, true
	}
	return int64(lo), false
}

// absU64 returns |v| as a uint64, handling MinInt64 (whose magnitude
// 2^63 has no positive int64 representation) without overflowing.
func absU64(v int64) uint64 {
	if v >= 0 {
		return uint64(v)
	}
	return uint64(-(v + 1)) + 1
}

// ClampInt64FromFloat converts a float64 to int64 with saturation (GR#16):
// NaN clamps to 0, +Inf (and anything at or past float64(math.MaxInt64),
// which is exactly 2^63) to math.MaxInt64, and -Inf (and anything at or
// past float64(math.MinInt64)) to math.MinInt64. It is the single choke
// point for every float64→int64 conversion — a bare int64(f) would wrap
// 2^63 into a negative value on amd64 (implementation-defined).
func ClampInt64FromFloat(f float64) int64 {
	if math.IsNaN(f) {
		return 0
	}
	if f >= float64(math.MaxInt64) {
		return math.MaxInt64
	}
	if f <= float64(math.MinInt64) {
		return math.MinInt64
	}
	return int64(f)
}

// IsFinite reports whether f is neither NaN nor ±Inf — the guard a caller
// uses before feeding a float64 result into int64 arithmetic or conserved
// accounting, so a non-finite value is rejected rather than silently
// propagated (GR#16).
func IsFinite(f float64) bool {
	return !math.IsNaN(f) && !math.IsInf(f, 0)
}

// GuardFinite returns f and whether f is finite (neither NaN nor ±Inf).
// It is the "never leak +Inf/NaN from a finite input" choke point in
// predicate form: a caller that must distinguish a finite result from a
// poisoned one uses the bool to reject (or fall back) rather than letting
// +Inf/NaN propagate. On a non-finite input it returns 0, false; the 0 is
// a neutral placeholder, never to be mistaken for a real finite result.
func GuardFinite(f float64) (float64, bool) {
	if IsFinite(f) {
		return f, true
	}
	return 0, false
}
