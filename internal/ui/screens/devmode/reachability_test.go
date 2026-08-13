package devmode

// AC-DM12: none of DoD #1-#3's surfaces are reachable through any path
// that does not first pass through debug.State's existing gates. This
// package cannot prove the FULL claim on its own (that requires an
// integration test booting cmd/metropolis's real construction path,
// which is outside this item's file ownership — see doc.go's
// "Reachability boundary" section and the FEAT-065 dispatch report's
// "left undone"), but it CAN mechanically prove its own, narrower half:
// every exported method on *Console is one of the known, gate-checked
// set. A future contributor adding a new exported Console method that
// forgets to gate on IsOpen()/the wired seams would silently widen the
// bypass surface AC-DM12 exists to close off — this test catches that
// by failing closed on any unrecognised exported method name, rather
// than needing a human to remember to update this file too.

import (
	"reflect"
	"sort"
	"testing"
)

// knownGatedMethods is the exhaustive, reviewed set of *Console's
// exported methods, each of which either calls into a wired debug.State
// seam directly (Open) or requires Console.IsOpen()==true first
// (Inspect, SubmitFeedback) before doing so — see console.go's own doc
// comments for each. IsOpen/IsPaused/DebugTouched/Close are read-only or
// housekeeping and gate nothing themselves by design (they expose no
// capability, only state).
var knownGatedMethods = map[string]bool{
	"Open":           true, // gates via requireConsole, then enable/pause
	"Close":          true, // housekeeping only, grants nothing
	"IsOpen":         true, // read-only query
	"IsPaused":       true, // read-only query
	"DebugTouched":   true, // read-only query
	"Inspect":        true, // gated on IsOpen(), then the wired inspect seam
	"SubmitFeedback": true, // gated on IsOpen(), then the wired submitFeedback seam
}

// TestConsoleExportedSurface_IsExhaustivelyReviewed fails if *Console
// ever grows an exported method not in knownGatedMethods above — a
// deliberate trip-wire for AC-DM12's "no bypass path" guarantee, since a
// new exported method is exactly the shape a future accidental bypass
// would take.
func TestConsoleExportedSurface_IsExhaustivelyReviewed(t *testing.T) {
	typ := reflect.TypeOf(&Console{})
	var unexpected []string
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		if !knownGatedMethods[name] {
			unexpected = append(unexpected, name)
		}
	}
	if len(unexpected) > 0 {
		sort.Strings(unexpected)
		t.Fatalf("Console has new exported method(s) %v not reviewed in knownGatedMethods — confirm they gate on IsOpen()/a wired debug.State seam (AC-DM12), then add them to this test's known-safe set", unexpected)
	}

	// Also fail if a previously-known method disappears silently renamed
	// (defensive: catches a rename that accidentally drops the check
	// coverage this test provides for the OLD name while a differently
	// named replacement goes unreviewed above).
	seen := map[string]bool{}
	for i := 0; i < typ.NumMethod(); i++ {
		seen[typ.Method(i).Name] = true
	}
	for name := range knownGatedMethods {
		if !seen[name] {
			t.Fatalf("knownGatedMethods names %q, but Console has no such exported method anymore — update this test to match the real surface", name)
		}
	}
}
