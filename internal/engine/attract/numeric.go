package attract

import (
	"math"
	"math/bits"
)

// This file is engine.attract's saturating-arithmetic core (FEAT-086 /
// GR#16), mirroring engine.build/numeric.go's, engine.finance/money.go's,
// and engine.households/numeric.go's helpers (all unexported in their own
// packages, so re-derived here with the identical predicates so semantics
// stay identical across the module boundary). Every int64 quantity in this
// package — admitted/departed citizen counts, vacancy/throughput capacities,
// rent/income magnitudes — routes through these helpers, and every
// float64→int64 conversion routes through clampInt64FromFloat, so a
// MaxInt64 / MinInt64 / mixed-sign / NaN / ±Inf input can never wrap
// negative or invent/destroy citizens.

// satAdd returns a+b saturated at the int64 extremes when the true sum
// overflows, instead of silently wrapping. The predicate is the two's-
// complement one every sibling package uses: a positive b must not leave
// the result below a, and a negative b must not leave it above a.
func satAdd(a, b int64) int64 {
	c := a + b
	if b > 0 && c < a {
		return math.MaxInt64
	}
	if b < 0 && c > a {
		return math.MinInt64
	}
	return c
}

// satSub returns a-b saturated at the int64 extremes when the true
// difference overflows. Handled directly (not satAdd(a,-b)) because
// negating math.MinInt64 itself overflows int64.
func satSub(a, b int64) int64 {
	c := a - b
	if b < 0 && c < a {
		return math.MaxInt64
	}
	if b > 0 && c > a {
		return math.MinInt64
	}
	return c
}

// safeMul returns a*b and whether the product overflowed int64, saturating
// to math.MaxInt64 / math.MinInt64 on overflow. It uses a full-width
// 128-bit multiply (math/bits.Mul64) over the operands' magnitudes, so
// every sign combination is checked exactly — including mixed signs, where
// |a*b| can exceed int64's range even though the product is negative.
func safeMul(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, false
	}
	negative := (a < 0) != (b < 0)

	hi, lo := bits.Mul64(absU64(a), absU64(b))
	if hi != 0 {
		if negative {
			return math.MinInt64, true
		}
		return math.MaxInt64, true
	}

	if negative {
		if lo > 1<<63 {
			return math.MinInt64, true
		}
		if lo == 1<<63 {
			return math.MinInt64, false // exactly MinInt64
		}
		return -int64(lo), false
	}

	if lo > uint64(math.MaxInt64) {
		return math.MaxInt64, true
	}
	return int64(lo), false
}

// absU64 returns |v| as a uint64, handling MinInt64 (whose magnitude 2^63
// has no positive int64 representation) without overflowing.
func absU64(v int64) uint64 {
	if v >= 0 {
		return uint64(v)
	}
	return uint64(-(v + 1)) + 1
}

// clampInt64FromFloat is the single choke point for every float64 → int64
// conversion in this package (GR#16): float64(math.MaxInt64) is exactly
// 2^63, which a bare int64(f) would convert to a NEGATIVE value on amd64
// (implementation-defined wrap) without this clamp. NaN clamps to 0,
// +Inf to math.MaxInt64, -Inf to math.MinInt64, and any value at or past
// the int64 extremes saturates to the nearest extreme.
func clampInt64FromFloat(f float64) int64 {
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

// isFinite reports whether f is neither NaN nor ±Inf.
func isFinite(f float64) bool {
	return !math.IsNaN(f) && !math.IsInf(f, 0)
}

// clampFloat clamps v into [lo, hi]; NaN clamps to lo (documented — the
// only sane NaN answer for a bounded quality/hazard value).
func clampFloat(v, lo, hi float64) float64 {
	if math.IsNaN(v) {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
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

// positiveDiv divides a non-negative numerator by a positive denominator,
// truncating toward zero; a negative numerator or a non-positive
// denominator yields 0 (defensive — a per-household average must never go
// negative or divide by zero).
func positiveDiv(numerator, denominator int64) int64 {
	if numerator <= 0 || denominator <= 0 {
		return 0
	}
	return numerator / denominator
}
