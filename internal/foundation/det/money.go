package det

import (
	"math"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// Micropounds is fixed-point money. Every money value in the simulation is
// a Micropounds (§1.2 point 4: "Money is int64 micro-pounds") — float64
// must never touch a money computation, because summation order changes a
// float64's rounding and this project's entire determinism guarantee
// depends on identical inputs producing identical outputs regardless of
// worker/summation order.
//
// BUG-452 (2026-09-01, Aaron's ruling): the base scale was rebased from
// 1e-6 GBP (micro-pound) to 1e-3 GBP (milli-pound) for megacity int64
// headroom — see MicropoundsPerPound's doc comment. The type keeps its
// historical name deliberately: the four independent declarations of this
// scale factor across the codebase (this one, engine.finance.Money's
// MicropoundsPerPound/micropoundsScale, engine.unlocks.micropoundsPerPound,
// engine.market.Micropounds — see code.json's units registry,
// money.micropound) are already a documented duplication (GR#3 flags it as
// an extraction candidate), and a mechanical Micropounds->Millipounds
// identifier rename touches ~230 occurrences across ~40 files codebase-wide
// (verified by grep, 2026-09-01) — cascading well beyond this single,
// cleanly-reviewable increment's blast radius (per BUG-452's own STOP
// guardrail). Rescaling the constant here (and its three sibling
// declarations, kept in lock-step) achieves the headroom goal with a
// contained, single-increment diff; the name/scale mismatch is a
// documented, deliberate trade-off, not an oversight — a future dedicated
// increment MAY do the identifier rename separately if Aaron wants the name
// corrected, verified purely by `go build` (a rename cannot silently
// miscompile: every missed reference fails to build).
type Micropounds int64

// MicropoundsPerPound is the fixed-point scale factor: 1 GBP = 1,000
// Micropounds (BUG-452 rebase, 2026-09-01 — was 1,000,000 pre-rebase, i.e.
// 1e-6 GBP/unit; now 1e-3 GBP/unit). Megacity (100M-citizen) design-target
// headroom: a £100B aggregate figure is 1e14 units, ~92,233x under int64
// max (~9.22e18) — roughly 1000x more headroom than the pre-rebase scale
// gave at the same real-£ magnitude, which is the entire point of the
// rebase (BUG-452's task: "milli-£ base unit... for megacity headroom").
const MicropoundsPerPound Micropounds = 1_000

// FromPounds converts a whole-pound integer amount to Micropounds.
// Unchecked: pounds values would need to exceed roughly ±9.2 quadrillion
// pounds to overflow int64 after the ×1e3 scale (BUG-452 rebase — was ±9.2
// trillion pre-rebase, at the old ×1e6 scale), several orders of magnitude
// beyond any balance this simulation's economy models — so FromPounds is
// deliberately infallible (no correlation ID plumbing) to keep the common
// construction path ergonomic; the checked helpers below (Add, Sub,
// MulRat) are what guard the arithmetic that can plausibly overflow
// through repeated accumulation.
func FromPounds(pounds int64) Micropounds {
	return Micropounds(pounds) * MicropoundsPerPound
}

// ToPounds truncates back to a whole-pound integer amount (Go's integer
// division truncates toward zero). FromPounds(x).ToPounds() == x for
// every x that does not overflow FromPounds's documented range.
func (m Micropounds) ToPounds() int64 {
	return int64(m) / int64(MicropoundsPerPound)
}

// Add returns a+b, or a registry-sourced error (ErrMoneyOverflow) if the
// result overflows int64, rather than silently wrapping (AC-11).
func Add(correlationID string, a, b Micropounds) (Micropounds, error) {
	sum := a + b
	if (b > 0 && sum < a) || (b < 0 && sum > a) {
		return 0, errs.New(ErrMoneyOverflow, correlationID, map[string]any{
			"op": "add", "a": int64(a), "b": int64(b),
		})
	}
	return sum, nil
}

// Sub returns a-b, or a registry-sourced error (ErrMoneyOverflow) if the
// result overflows int64, rather than silently wrapping (AC-11). Handled
// directly (not as Add(a, -b)) because negating math.MinInt64 itself
// overflows int64.
func Sub(correlationID string, a, b Micropounds) (Micropounds, error) {
	diff := a - b
	if (b < 0 && diff < a) || (b > 0 && diff > a) {
		return 0, errs.New(ErrMoneyOverflow, correlationID, map[string]any{
			"op": "sub", "a": int64(a), "b": int64(b),
		})
	}
	return diff, nil
}

// MulRat returns a scaled by the rational num/den (integer division,
// truncating toward zero, applied after the checked multiply — so the
// only overflow surface is the a*num step, not the division), or a
// registry-sourced error (ErrMoneyOverflow) if a*num overflows int64, or
// if den is zero. This is the fixed-point helper for "money * rate"
// computations (tax rates, interest, splits) that must never touch
// float64.
func MulRat(correlationID string, a Micropounds, num, den int64) (Micropounds, error) {
	if den == 0 {
		return 0, errs.New(ErrMoneyOverflow, correlationID, map[string]any{
			"op": "mulrat", "a": int64(a), "num": num, "den": den, "cause": "division by zero",
		})
	}
	product, ok := checkedMul64(int64(a), num)
	if !ok {
		return 0, errs.New(ErrMoneyOverflow, correlationID, map[string]any{
			"op": "mulrat", "a": int64(a), "num": num, "den": den,
		})
	}
	// product / den can overflow int64 when product == MinInt64 && den == -1
	// (dividing MinInt64 by -1 would be MaxInt64+1, which overflows).
	// Handle this explicitly to prevent silent wrapping (AC-11).
	if product == math.MinInt64 && den == -1 {
		return 0, errs.New(ErrMoneyOverflow, correlationID, map[string]any{
			"op": "mulrat", "a": int64(a), "num": num, "den": den, "cause": "division overflow",
		})
	}
	return Micropounds(product / den), nil
}

// checkedMul64 returns a*b and true, or (0, false) if the product
// overflows int64. math.MinInt64 is handled explicitly because negating
// or dividing it by -1 is itself an overflow.
func checkedMul64(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	if (a == -1 && b == math.MinInt64) || (b == -1 && a == math.MinInt64) {
		return 0, false
	}
	p := a * b
	if p/b != a {
		return 0, false
	}
	return p, true
}
