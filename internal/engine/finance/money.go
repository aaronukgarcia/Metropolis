package finance

import (
	"math"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// Money is the single monetary type in this package: int64 fixed-point
// micro-pounds by name (AC-2, M0-ENG §1.2). Since the BUG-452 rebase
// (2026-09-01) one pound sterling is 1,000 units (was 1,000,000
// pre-rebase — see MicropoundsPerPound's doc comment for why the name is
// kept despite the scale change), so a value of 2_500 is £2.50. Every
// field in this package that holds money is a Money (or, where the amount
// is a ratio, an int64 fixed-point value with a documented scale — see
// BasisPoints and the factorScale constant in land.go). float32/float64
// never touches a monetary field.
type Money int64

// MicropoundsPerPound is the fixed scale factor: 1 GBP = 1,000 units
// (BUG-452 rebase, 2026-09-01 — was 1,000,000/1e-6 GBP pre-rebase, now
// 1e-3 GBP/unit for megacity int64 headroom; see
// internal/foundation/det/money.go's MicropoundsPerPound doc comment for
// the full rationale and why the identifier keeps its historical name).
// It is the exact scale engine.market's Micropounds type uses, so a price
// crossing the Market/finance boundary never silently loses precision
// (US-5).
const MicropoundsPerPound Money = 1_000

// micropoundsScale is MicropoundsPerPound as a plain int64, for
// arithmetic that multiplies or divides an int64 by the scale.
const micropoundsScale = int64(MicropoundsPerPound)

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
	p, overflow := num.SafeMul(a, b)
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

// satAddMoney returns a+b saturated at the int64 extremes, plus whether
// saturation occurred — used by the ledger's debit/credit summation so an
// overflowing sum is detectable rather than silently wrapping (GR#16).
// It is a thin Money-typed adapter over foundation/num's canonical
// SatAddChecked: the overflow predicate lives in num, not here.
func satAddMoney(a, b Money) (Money, bool) {
	v, ok := num.SatAddChecked(int64(a), int64(b))
	return Money(v), ok
}

// satSubMoney returns a-b saturated at the int64 extremes when the true
// difference overflows, so a running money total never wraps (GR#16). It
// is a thin Money-typed adapter over foundation/num's canonical SatSub:
// the overflow predicate lives in num, not here.
func satSubMoney(a, b Money) Money {
	return Money(num.SatSub(int64(a), int64(b)))
}
