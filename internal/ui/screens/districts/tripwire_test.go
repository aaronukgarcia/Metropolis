package districts

// Independent-destructive addition (GR#23), second life. Originally this
// tripwire fired the moment internal/engine/policies appeared in the module
// tree, forcing a human to revisit the five BLOCKED ACs (AC-2/3/4/5/8 in
// docs/planning/acceptance/ui.screen.districts.md) instead of letting them
// stay silently unbuilt after their blocker cleared.
//
// IT FIRED, AS DESIGNED, on 2026-08-20 when the lane/bob sweep landed
// engine.policies on main. The bookkeeping it demanded was done at that
// moment: FEAT-210 tracks building the five ACs against the real
// PoliciesAPI (fixture-testable now; live data arrives with FEAT-208's
// publish path). The test now guards the INVERSE drift: FEAT-210's premise
// is that engine.policies exists on main -- if the module were ever
// reverted/renamed without FEAT-210 and this package's doc.go being
// re-scoped, the panes would reference a phantom module. Fail loudly in
// that case too.
//
// Path arithmetic: this package lives at internal/ui/screens/districts;
// "../../../engine/policies" is internal/engine/policies.

import (
	"os"
	"testing"
)

func TestTripwire_EnginePoliciesLanded_FEAT210Pending(t *testing.T) {
	const policiesDir = "../../../engine/policies"
	info, err := os.Stat(policiesDir)
	if os.IsNotExist(err) {
		t.Fatalf(
			"TRIPWIRE FIRED (inverse): %s no longer exists, but FEAT-210 and "+
				"this package's doc.go assume engine.policies is on main "+
				"(landed 2026-08-20, lane/bob sweep). If the module was "+
				"deliberately reverted, re-scope FEAT-210 and restore the "+
				"BLOCKED state in doc.go; do not leave AC-2/3/4/5/8 pointing "+
				"at a phantom module.",
			policiesDir,
		)
	}
	if err != nil {
		t.Fatalf("unexpected error stat'ing %s: %v", policiesDir, err)
	}
	if !info.IsDir() {
		t.Fatalf("%s exists but is not a directory -- investigate", policiesDir)
	}
	// engine.policies is on main and FEAT-210 tracks building AC-2/3/4/5/8
	// against it. This passing test is the record that the original
	// tripwire fired and was actioned, not a claim that the ACs are built.
}
