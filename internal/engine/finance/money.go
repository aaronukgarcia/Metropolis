package finance

import (
	"math"
	"math/bits"
)

// Money is the single monetary type in this package: int64 micro-pounds
// (AC-2, M0-ENG §1.2). One pound sterling is 1,000,000 micro-pounds, so
// a value of 2_500_000 is £2.50. Every field in this package that holds
// money is a Money (or, where the amount is a ratio, an int64 fixed-point
// value with a documented scale — see BasisPoints and the factorScale
// constant in land.go). float32/float64 never touches a monetary field.
type Money int64

// MicropoundsPerPound is the fixed scale factor: 1 GBP = 1,000,000
// micro-pounds. It is the exact scale engine.market's Micropounds type
// uses, so a price crossing the Market/finance boundary never silently
// loses precision (US-5).
const MicropoundsPerPound Money = 1_000_000

// micropoundsScale is MicropoundsPerPound as a plain int64, for
// arithmetic that multiplies or divides an int64 by the scale.
const micropoundsScale = int64(MicropoundsPerPound)

// safeMul returns a*b and whether the product overflows int64. On
// overflow it returns the saturated value (MaxInt64 for positive
// overflow, MinInt64 for negative underflow) and true. It uses a
// full-width 128-bit multiply (math/bits.Mul64) over the operands'
// magnitudes, so every sign combination is checked exactly — including
// mixed signs, where |a*b| can exceed int64's range even though the
// product is negative (e.g. MaxInt64 * -2 underflows, MinInt64 * 2
// underflows). A division-based check would be wrong here: MinInt64/-1
// itself overflows, so `a > MinInt64/b` mis-flags b = -1.
func safeMul(a, b int64) (int64, bool) {
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

// mulDiv computes (a*b)/d in fixed-point arithmetic, keeping the
// intermediate product inside int64 by saturating to MaxInt64 if it
// would overflow. a and b are non-negative, d is positive; the result is
// truncated (rounded toward zero). This is the one place factor
// multiplication happens for land pricing, so a deterministic,
// non-overflowing result is guaranteed rather than a wrapped negative
// price (GR#21, GR#16). Saturation is order-independent only because
// this helper multiplies exactly two already-clamped factors and divides
// immediately — the sequential shape in LandPrice keeps every
// intermediate value small enough that saturation is a theoretical
// guard, not a reachable outcome for placeholder magnitudes.
func mulDiv(a, b, d int64) int64 {
	if a <= 0 || b <= 0 || d <= 0 {
		return 0
	}
	p, overflow := safeMul(a, b)
	if overflow {
		return math.MaxInt64
	}
	return p / d
}

// clampScore clamps v into [0, 1000], the documented credit-score range.
func clampScore(v int64) int64 {
	if v < 0 {
		return 0
	}
	if v > 1000 {
		return 1000
	}
	return v
}

// minI64 returns the smaller of a and b.
func minI64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// maxI64 returns the larger of a and b.
func maxI64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// satAddI64 returns a+b saturated at the int64 extremes when the true sum
// overflows, so a fixed-point factor never wraps (GR#16).
func satAddI64(a, b int64) int64 {
	c := a + b
	if b > 0 && c < a {
		return math.MaxInt64
	}
	if b < 0 && c > a {
		return math.MinInt64
	}
	return c
}

// satSubI64 returns a-b saturated at the int64 extremes when the true
// difference overflows.
func satSubI64(a, b int64) int64 {
	c := a - b
	if b < 0 && c < a {
		return math.MaxInt64
	}
	if b > 0 && c > a {
		return math.MinInt64
	}
	return c
}

// satAddMoney returns a+b saturated at the int64 extremes, plus whether
// saturation occurred — used by the ledger's debit/credit summation so an
// overflowing sum is detectable rather than silently wrapping (GR#16).
func satAddMoney(a, b Money) (Money, bool) {
	c := a + b
	if b > 0 && c < a {
		return Money(math.MaxInt64), true
	}
	if b < 0 && c > a {
		return Money(math.MinInt64), true
	}
	return c, false
}

// satSubMoney returns a-b saturated at the int64 extremes when the true
// difference overflows, so a running money total never wraps (GR#16).
// Handled directly rather than as satAddMoney(a, -b) because negating
// math.MinInt64 itself overflows.
func satSubMoney(a, b Money) Money {
	c := a - b
	if b < 0 && c < a {
		return Money(math.MaxInt64)
	}
	if b > 0 && c > a {
		return Money(math.MinInt64)
	}
	return c
}
