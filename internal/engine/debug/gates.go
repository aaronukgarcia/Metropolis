package debug

// This file holds the simple boolean-style gates: capabilities this
// package only needs to permit-or-reject, with no injected seam of
// their own (unlike the entity inspector or fidelity dial). Each is a
// thin, documented wrapper over requireOn so every call site names its
// capability explicitly rather than callers reaching into requireOn
// directly.

// AllowSpeed8x reports whether Speed8xDebug (engine.core's clock.go
// Speed8xDebug) is currently reachable (AC-5). engine.core's own doc
// comment on Speed8xDebug notes it accepts the value via SetSpeed but
// does not itself enforce the debug gate — that handler is expected to
// call this before accepting Speed8xDebug. Wiring that call site is out
// of scope for this package (engine.core is a separate item's
// territory); see the dispatch report for this gap.
func (s *State) AllowSpeed8x(correlationID string) error {
	return s.requireOn(correlationID, "speed-8x")
}

// RequireConsole reports whether the "`" console is currently reachable
// (AC-10). This package gates access only — the console itself is a
// UI-layer concern out of scope here.
func (s *State) RequireConsole(correlationID string) error {
	return s.requireOn(correlationID, "console")
}

// RequireFixtureControls reports whether fixture record/replay controls
// are currently reachable (AC-10). harness.replay (MOD-013, Sprint 2)
// does not exist yet — this package gates ACCESS to whatever controls
// that module eventually exposes; it does not build the record/replay
// mechanism itself (see doc.go's "Out of scope" and the acceptance
// doc's own note on this seam).
func (s *State) RequireFixtureControls(correlationID string) error {
	return s.requireOn(correlationID, "fixture-record-replay")
}
