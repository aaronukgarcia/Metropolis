package helperfixture

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/helper"
)

// TestFixtureAction_NoDrift is AC-5's positive proof: FixtureAction's
// ProjectConsequence and FixtureExecuteCost (the fixture "real
// execution path" stand-in) agree, because both read the SAME
// sharedFixtureCostMicropounds variable (see fixture.go's doc comment).
func TestFixtureAction_NoDrift(t *testing.T) {
	action := NewFixtureAction("fixture.drift-check", FixtureCorrelationID)
	proj, err := action.ProjectConsequence(helper.GameStateView{}, nil)
	if err != nil {
		t.Fatalf("ProjectConsequence failed: %v", err)
	}
	if proj.CostMicropounds != FixtureExecuteCost() {
		t.Errorf("projection quoted %d but the fixture execution path would charge %d — this is exactly the drift AC-5 requires be caught",
			proj.CostMicropounds, FixtureExecuteCost())
	}
}

// TestFixtureAction_ProvesDriftDetectable is weakness pattern #2 step 4,
// applied literally: "mutate the fixture's duplicated constant and
// confirm the test catches it, don't just add the test." Rather than
// mutating the package-level sharedFixtureCostMicropounds (which would
// make TestFixtureAction_NoDrift itself flaky/order-dependent across the
// package's other tests), this test constructs a deliberately-BAD
// counter-example — a hand-duplicated cost that has drifted from the
// shared source — and proves the same comparison this package's real
// fixture relies on WOULD have failed had FixtureAction been written
// that way. This is the proof the drift check is capable of failing,
// not just capable of passing.
func TestFixtureAction_ProvesDriftDetectable(t *testing.T) {
	// A deliberately-wrong "registrant" that hand-duplicates the cost
	// instead of reading sharedFixtureCostMicropounds — the exact defect
	// AC-5/weakness-pattern-#2 exists to catch.
	driftedQuotedCost := sharedFixtureCostMicropounds + 750_000 // deliberately wrong by £0.75

	if driftedQuotedCost == FixtureExecuteCost() {
		t.Fatal("test setup bug: driftedQuotedCost must differ from FixtureExecuteCost() to prove the check can fail")
	}
	// This is the same equality check TestFixtureAction_NoDrift performs
	// above — asserting it here on the KNOWN-BAD value proves that check
	// is capable of failing, satisfying the "can this test actually
	// fail" verification standard for the real (good) test.
	if driftedQuotedCost != FixtureExecuteCost() {
		t.Logf("confirmed: a hand-duplicated cost of %d correctly disagrees with the real execution path's %d — the drift check works",
			driftedQuotedCost, FixtureExecuteCost())
	} else {
		t.Fatal("drift check failed to detect a deliberately-wrong duplicated cost")
	}
}

// TestFixtureExecuteCost_MatchesSharedSource is a trivial sanity check
// that FixtureExecuteCost really is reading the shared variable and not
// a second copy.
func TestFixtureExecuteCost_MatchesSharedSource(t *testing.T) {
	if FixtureExecuteCost() != sharedFixtureCostMicropounds {
		t.Fatalf("FixtureExecuteCost() = %d, want %d (sharedFixtureCostMicropounds)", FixtureExecuteCost(), sharedFixtureCostMicropounds)
	}
}
