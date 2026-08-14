package invariant

import "math"

// stockCheck is the shared conservation-balance logic every v1
// invariant (people.go, money.go, goods.go, vehicle.go) wraps: given a
// Snapshot, look up the invariant's own StockName and verify
// Closing - Opening == TrackedDelta. Kept in one place so the four
// stock-specific types differ only in name, StockName, and doc comment
// (AC-18) — never in the actual balance arithmetic, which is identical
// by construction across every conserved stock.
type stockCheck struct {
	name  string
	stock StockName
}

// Name implements Invariant.
func (s stockCheck) Name() string { return s.name }

// Check implements Invariant: verifies s.stock's balance identity for
// state.Tick, or reports Ran: false if s.stock is not (yet) registered
// in state (AC-12).
func (s stockCheck) Check(state Snapshot) Result {
	reading, ok := state.Reading(s.stock)
	if !ok {
		return Result{Ran: false}
	}

	actualDelta, overflowed := satSub(reading.Closing, reading.Opening)
	if !overflowed && actualDelta == reading.TrackedDelta {
		return Result{Ran: true}
	}

	entityIDs := append([]string(nil), reading.Suspects...) // defensive copy
	return Result{
		Ran:       true,
		Violation: newViolation(s.name, state.Tick, reading.TrackedDelta, actualDelta, entityIDs),
	}
}

// satAdd returns a+b saturated at math.MaxInt64 / math.MinInt64 when the true
// sum would overflow int64, instead of silently wrapping (SEC-055). overflowed
// reports whether saturation occurred. The overflow predicate is the same one
// foundation/det/money.go's Add uses: under two's-complement wrapping, a
// positive b must not leave the result below a, and a negative b must not
// leave it above a.
func satAdd(a, b int64) (int64, bool) {
	c := a + b
	if b > 0 && c < a {
		return math.MaxInt64, true
	}
	if b < 0 && c > a {
		return math.MinInt64, true
	}
	return c, false
}

// satSub returns a-b saturated at math.MaxInt64 / math.MinInt64 when the true
// difference would overflow int64, instead of silently wrapping (SEC-055).
// overflowed reports whether saturation occurred. Handled directly (not as
// satAdd(a, -b)) because negating math.MinInt64 itself overflows int64 — the
// same reason foundation/det/money.go's Sub does not route through Add.
func satSub(a, b int64) (int64, bool) {
	c := a - b
	if b < 0 && c < a {
		return math.MaxInt64, true
	}
	if b > 0 && c > a {
		return math.MinInt64, true
	}
	return c, false
}
