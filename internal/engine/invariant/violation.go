package invariant

import "fmt"

// Violation is what an Invariant's Check reports when a conserved
// stock's tracked flows do not explain its actual change for the tick.
// The zero value (Detected: false) means balanced — AC-1's "returns
// nil/zero-value Violation when balanced" — so a caller only needs to
// test Detected (or call IsZero) rather than compare every field.
//
// # Downstream sanitisation obligation (AC-11b)
//
// InvariantName, EntityIDs, and Message are DISPLAY DATA — opaque text
// this package hands to a caller, never bytes this package itself
// writes to a terminal or log sink. Whatever renders them next (F12's
// error-tail, a future log formatter) MUST sanitise or escape them
// before they reach a terminal or log verbatim, exactly as SEC-011
// found for "any rendered string, e.g. an error message" elsewhere in
// this codebase. This package's own, narrower obligation — which it
// meets — is to never construct a further format string by
// interpolating an ID into it (no `fmt.Sprintf("...%s...", someID)`
// whose RESULT is itself later treated as a format string); EntityIDs
// stays a plain []string field, never spliced into Message.
type Violation struct {
	// Detected is false for the zero value (balanced, no violation).
	Detected bool

	// InvariantName is the Name() of the Invariant that reported this
	// violation (e.g. "people", "money").
	InvariantName string

	// Tick is the daily-tick index (core.Clock.Tick(), see hook.go) the
	// violation was detected at — "the tick it happens", per this
	// item's WHY (surfacing a bug at the tick it happens, not fifty
	// ticks later as drift).
	Tick int64

	// Expected is the stock's TrackedDelta for the tick (what the
	// tracked flows say the change should have been).
	Expected int64

	// Actual is the stock's observed change for the tick (Closing -
	// Opening).
	Actual int64

	// EntityIDs names entities implicated in the imbalance, where the
	// invariant can identify them (e.g. vehicle IDs present in Closing's
	// count with no matching spawn event) — AC-11's "affected entity IDs
	// where applicable". May be nil when no specific entity is
	// implicated (e.g. a purely aggregate money mismatch). See this
	// type's doc comment for the sanitisation obligation this field
	// carries downstream.
	EntityIDs []string

	// Message is a human-readable diagnostic naming the invariant, the
	// tick, and the imbalance amount (AC-8's dev-mode hard-fail
	// requirement: "diagnostic output naming the invariant, the tick,
	// and the imbalance amount"). Never has an EntityIDs value spliced
	// into it — see this type's doc comment.
	Message string
}

// IsZero reports whether v is the zero-value "balanced, no violation"
// result.
func (v Violation) IsZero() bool { return !v.Detected }

// newViolation constructs a Detected Violation for stock conservation
// invariants (conservation.go). expected/actual are the stock's
// TrackedDelta and observed delta respectively; entityIDs is passed
// through unchanged (never interpolated into Message — AC-11b).
func newViolation(name string, tick, expected, actual int64, entityIDs []string) Violation {
	unexplained := actual - expected
	return Violation{
		Detected:      true,
		InvariantName: name,
		Tick:          tick,
		Expected:      expected,
		Actual:        actual,
		EntityIDs:     entityIDs,
		Message: fmt.Sprintf(
			"%s conservation violated at tick %d: expected delta %d, actual delta %d, unexplained %d",
			name, tick, expected, actual, unexplained,
		),
	}
}

// Result is what Invariant.Check returns: whether the invariant ran at
// all (Ran — false when its stock is not yet registered in the
// Snapshot, AC-12) and, if it ran, whether it found a Violation.
type Result struct {
	// Ran is false when the invariant's stock was not present/registered
	// in the Snapshot it was checked against — a legitimate, structurally
	// distinguishable "skipped", never conflated with "checked and
	// balanced" (AC-1b, AC-12).
	Ran bool

	// Violation is the zero value (IsZero() == true) when Ran is false,
	// or when Ran is true and the stock balanced.
	Violation Violation
}
