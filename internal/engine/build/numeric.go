package build

import (
	"math"
	"math/bits"
)

// This file is engine.build's saturating-arithmetic core (FEAT-086 /
// GR#16). Every Money/int64 quantity in this package — materials
// quantities, labour, lead times, compensation — routes through these
// helpers, never a raw + / - / *, so a MaxInt64 / MinInt64 / mixed-sign
// input can never wrap negative, produce +Inf/NaN, or invent/destroy
// units. They mirror internal/engine/finance/money.go's and
// internal/engine/invariant/conservation.go's helpers (which are unexported
// in those packages, so re-derived here — the codebase already carries
// per-package copies of this exact arithmetic, and this package keeps the
// same predicate so the semantics stay identical across the boundary).

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
// |a*b| can exceed int64's range even though the product is negative
// (MaxInt64 * -2 underflows, MinInt64 * 2 underflows).
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
		// Result in [-2^63, -1]; the largest representable magnitude is 2^63.
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

// effectiveLeadTime computes a §13-F3 base lead time (in simulation days)
// modulated by §9's construction-speed multiplier (winter < 1.0 → longer),
// as the integer number of simulation days one build order will spend in
// the queue:
//
//	effective = ceil(base / multiplier)
//
// It is the ONLY place a float64 seasonal multiplier touches this package's
// integer lead-time arithmetic, and it clamps every conversion. A
// non-positive, NaN, or infinite multiplier is rejected by the caller with
// ErrInvalidSeasonalMultiplier — this helper saturates defensively (to
// math.MaxInt64, "effectively never completes") rather than dividing by
// zero or producing +Inf. base is assumed non-negative (validated at load).
func effectiveLeadTime(base int64, multiplier float64) int64 {
	if !(multiplier > 0) || math.IsNaN(multiplier) || math.IsInf(multiplier, 0) {
		return math.MaxInt64
	}
	eff := math.Ceil(float64(base) / multiplier)
	return clampInt64FromFloat(eff)
}
