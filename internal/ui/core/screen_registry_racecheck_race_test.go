//go:build race

package core

// allocCountingReliable mirrors the Go toolchain's own race/norace build
// tag split (the same mechanism internal/race uses) so a single boolean,
// resolved at compile time rather than guessed at runtime, tells
// screen_registry_test.go whether testing.AllocsPerRun's raw mallocs-
// counter diff can be trusted for an exact-equality assertion. Under
// -race it cannot — see TestScreenRegistry_Activate_CostIsConstant_
// NotProportionalToScreenCount's own doc comment for the investigation
// (2026-08-21) that established why.
const allocCountingReliable = false
