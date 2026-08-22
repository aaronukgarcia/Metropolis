package services

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/dash"
	mapscreen "github.com/aaronukgarcia/Metropolis/internal/ui/screens/map"
)

// TestCoverageJumpTarget_NamesARealRegisteredView is SF-5's "not a
// fabricated non-view" check, mirroring ui.screen.menu's
// TestDrillTargets_SaveSlotResolvesToRegisteredViewport exactly (the
// drift-test shape: services holds the literal coverageJumpView, this
// test imports the real source and asserts agreement — if F1's view is
// ever renamed, this fails and forces reconciliation). Importing
// ui.screen.map from a _test.go file is the sanctioned exception SF-1
// already carves out for tests (SF-1 forbids internal/engine imports in
// non-test source; a same-layer cross-screen import in a test file is not
// an SF-1 concern at all).
func TestCoverageJumpTarget_NamesARealRegisteredView(t *testing.T) {
	target := CoverageJumpTarget("police")

	if target.ViewName != mapscreen.ViewSubscriptionName {
		t.Fatalf("CoverageJumpTarget ViewName = %q, want the registered F1 view %q (a fabricated non-view is a dead end)",
			target.ViewName, mapscreen.ViewSubscriptionName)
	}
	if target.EntityID != "coverage.police" {
		t.Errorf("CoverageJumpTarget EntityID = %q, want %q", target.EntityID, "coverage.police")
	}
	if _, err := dash.NewDrillTarget(target.ViewName, target.EntityID); err != nil {
		t.Errorf("CoverageJumpTarget(%q) is not a valid dash.DrillTarget: %v", "police", err)
	}
}

// TestCoverageJumpTarget_DoesNotYetResolve documents SVC-3's BLOCKED
// state mechanically rather than merely in prose: ui.screen.map never
// marks a per-service coverage entity live (its own AC-3 overlay cycle
// was deferred at FEAT-005 dispatch — see doc.go), so a fresh
// dash.MapResolver with nothing marked correctly fails to resolve this
// screen's coverage jump. If this test ever starts failing because
// resolution unexpectedly succeeds, that is a signal ui.screen.map's
// AC-3 has landed and doc.go's BLOCKED note (and this test) should be
// retired, not silently left stale.
func TestCoverageJumpTarget_DoesNotYetResolve(t *testing.T) {
	target := CoverageJumpTarget("police")
	res := dash.NewMapResolver()
	if res.Resolve(target) {
		t.Error("CoverageJumpTarget resolved through an empty dash.MapResolver — SVC-3 should still be BLOCKED pending ui.screen.map's AC-3; update doc.go if this is now expected")
	}
}
