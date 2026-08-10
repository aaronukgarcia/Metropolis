package invariant

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

	actualDelta := reading.Closing - reading.Opening
	if actualDelta == reading.TrackedDelta {
		return Result{Ran: true}
	}

	entityIDs := append([]string(nil), reading.Suspects...) // defensive copy
	return Result{
		Ran:       true,
		Violation: newViolation(s.name, state.Tick, reading.TrackedDelta, actualDelta, entityIDs),
	}
}
