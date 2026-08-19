package districts

// Independent-destructive addition (GR#23): doc.go repeatedly labels AC-2,
// AC-3, AC-4, AC-5 and AC-8 "BLOCKED (tripwire)" but, as shipped, no test in
// this package actually fires when the blocking condition (engine.policies
// landing on main) goes away -- the prose says "tripwire", the package has
// none. A tripwire that can never fire is a fabrication, not a safeguard:
// nobody would be told to come back and build AC-2/3/4/5/8 once
// engine.policies merges. This test makes the check mechanical: it fails
// loudly the moment internal/engine/policies exists in this module tree,
// forcing a human to read this comment and revisit the five BLOCKED ACs
// instead of AC-2/3/4/5/8 silently staying unbuilt forever after their
// blocker clears.
//
// Path arithmetic: this package lives at internal/ui/screens/districts: two
// levels up is internal/ui/, three levels up is internal/, so
// "../../../engine/policies" is internal/engine/policies -- verified
// against internal/engine/tax's actual location the same way (AC-6 is live
// today because that directory exists; this test asserts its policies
// sibling does not, yet).

import (
	"os"
	"testing"
)

func TestTripwire_EnginePoliciesNotYetLanded(t *testing.T) {
	const policiesDir = "../../../engine/policies"
	info, err := os.Stat(policiesDir)
	if os.IsNotExist(err) {
		// Expected today: engine.policies is REJECT-state on lane/bob only,
		// not on main. AC-2/AC-3/AC-4/AC-5/AC-8 remain correctly BLOCKED.
		return
	}
	if err != nil {
		t.Fatalf("unexpected error stat'ing %s: %v", policiesDir, err)
	}
	if info.IsDir() {
		t.Fatalf(
			"TRIPWIRE FIRED: %s now exists -- engine.policies has landed on main. "+
				"AC-2 (district drawing/naming), AC-3 (policy library browser), "+
				"AC-4 (impact preview confidence-honest rendering), AC-5 (conflict "+
				"warnings) and AC-8 (ResolveScope cell-highlight) in "+
				"docs/planning/acceptance/ui.screen.districts.md are no longer "+
				"correctly BLOCKED and this package (doc.go's Provenance section) "+
				"must be revisited to build against the real, now-registered "+
				"PoliciesAPI shape -- do not leave this pane rendering "+
				"RenderBlockedFeature once its data source is real.",
			policiesDir,
		)
	}
}
