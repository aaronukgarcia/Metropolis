package devmode

// Registry error codes for feat.devmode (FEAT-065). This package claims
// U400-U499 (see data/errors.json's "U400-U499" reserved-range entry) —
// checked against that table AND `grep -rn "MET-U4" internal/ cmd/`
// before claiming, per BUG-008's lesson that the reserved-range table
// alone is not always current. Every code below is registered in
// data/errors.json with real severity/module/message/remedy fields
// (GR#7).
const (
	// ErrRequireConsoleNotConfigured: Console.Open was called on a
	// Console constructed without WithRequireConsole. This is a
	// construction-time misconfiguration, distinct from the gate itself
	// denying the request — a genuine gate denial (debug off) returns the
	// wired RequireConsoleFunc's own error verbatim (feat.debugmode's
	// ErrDebugRequired, MET-E200), never wrapped in a devconsole-local
	// code, per AC-DM1's "not a devconsole-local error code" check.
	ErrRequireConsoleNotConfigured = "MET-U400"

	// ErrConsoleNotOpen: Inspect or SubmitFeedback was called before
	// Console.Open succeeded. Carries forward AC-DM1's gate to every
	// other surface this package exposes (AC-DM7) — there is no exported
	// path into inspection or feedback submission that does not first
	// require a successfully opened console.
	ErrConsoleNotOpen = "MET-U401"

	// ErrCapabilityNotConfigured: an action was invoked whose matching
	// Option (WithInspect, WithSubmitFeedback, WithPause, ...) was never
	// supplied at construction. An unwired optional seam is refused
	// explicitly — never silently treated as a no-op success.
	ErrCapabilityNotConfigured = "MET-U402"
)
