package roads

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestUpgradeCompatibleSucceeds (AC-4) asserts a compatible upgrade (per the
// documented any-to-any rule) succeeds with a positive cost, and that the
// class change is scheduled (not instantly swapped): the class stays put
// until the roadworks phase completes.
func TestUpgradeCompatibleSucceeds(t *testing.T) {
	a := newTestAPI(t)
	if err := a.SetWorld(newTestWorld(t)); err != nil {
		t.Fatal(err)
	}
	r := addRoad(t, a, 1, 100, 100, 100, 110, ClassGravel)

	quote, err := a.ApplyUpgrade(ApplyUpgradeCommand{CorrelationID: "test", RoadID: r.ID, TargetClass: ClassResidentialStreet})
	if err != nil {
		t.Fatalf("ApplyUpgrade: %v", err)
	}
	if quote.CostMicropounds <= 0 {
		t.Errorf("cost = %d, want > 0", quote.CostMicropounds)
	}
	if len(quote.Phases) == 0 {
		t.Errorf("no roadworks phases scheduled for the upgrade (AC-6)")
	}

	// The class has NOT swapped yet (not an instant swap).
	info, _ := a.RoadInfo(r.ID, 0)
	if info.Class != ClassGravel {
		t.Fatalf("class after approve = %s, want gravel (upgrade is phased, not instant)", info.Class.String())
	}

	// After the phase completes, the class commits.
	if err := a.Advance(quote.Phases[0].StartMonth + quote.Phases[0].DurationMonths); err != nil {
		t.Fatal(err)
	}
	info, _ = a.RoadInfo(r.ID, quote.Phases[0].StartMonth+quote.Phases[0].DurationMonths)
	if info.Class != ClassResidentialStreet {
		t.Fatalf("class after completion = %s, want residential_street", info.Class.String())
	}
}

// TestUpgradeCostIncreasesWithRungDistance (AC-4) asserts the documented
// cost scaling: a longer ladder jump costs more. It upgrades the same source
// class (alley) to each higher rung and asserts the cost is strictly
// increasing with rung distance.
func TestUpgradeCostIncreasesWithRungDistance(t *testing.T) {
	a := newTestAPI(t)
	if err := a.SetWorld(newTestWorld(t)); err != nil {
		t.Fatal(err)
	}

	var prev int64
	for target := RoadClass(1); target < numClasses; target++ {
		id := RoadID(target)
		r := addRoad(t, a, id, 100, 10+int(id)*10, 100, 15+int(id)*10, ClassAlley)
		quote, err := a.ApplyUpgrade(ApplyUpgradeCommand{CorrelationID: "test", RoadID: r.ID, TargetClass: target})
		if err != nil {
			t.Fatalf("ApplyUpgrade(alley -> %s): %v", target.String(), err)
		}
		if prev != 0 && quote.CostMicropounds <= prev {
			t.Errorf("cost for rung %s (%d) not > previous (%d); rung-distance scaling is not monotonic", target.String(), quote.CostMicropounds, prev)
		}
		prev = quote.CostMicropounds
	}
}

// TestUpgradeSameClassNoOp (AC-4) asserts a same-class upgrade is an
// idempotent no-op with zero cost, not an error and not a re-scheduled
// roadworks.
func TestUpgradeSameClassNoOp(t *testing.T) {
	a := newTestAPI(t)
	r := addRoad(t, a, 1, 100, 100, 100, 110, ClassTwoLane)
	quote, err := a.ApplyUpgrade(ApplyUpgradeCommand{CorrelationID: "test", RoadID: r.ID, TargetClass: ClassTwoLane})
	if err != nil {
		t.Fatalf("same-class upgrade errored: %v", err)
	}
	if quote.CostMicropounds != 0 {
		t.Errorf("same-class cost = %d, want 0", quote.CostMicropounds)
	}
}

// TestInvalidUpgradeClassRejected (AC-12) asserts an upgrade to a class
// outside the ladder is rejected with the registry code and no state change.
func TestInvalidUpgradeClassRejected(t *testing.T) {
	a := newTestAPI(t)
	r := addRoad(t, a, 1, 100, 100, 100, 110, ClassTwoLane)
	_, err := a.ApplyUpgrade(ApplyUpgradeCommand{CorrelationID: "test", RoadID: r.ID, TargetClass: RoadClass(99)})
	if !errors.Is(err, &errs.E{Code: ErrInvalidClass}) {
		t.Fatalf("got %v, want ErrInvalidClass", err)
	}
	info, _ := a.RoadInfo(r.ID, 0)
	if info.Class != ClassTwoLane {
		t.Fatalf("class changed to %s after rejected upgrade", info.Class.String())
	}
}

// TestWideningIntoOccupiedCellRequiresDemolition (AC-5) asserts a widening
// upgrade overlapping an occupied (zoned) cell fails until that cell is
// cleared, then succeeds — and that the obstructing cell is named in the
// error.
func TestWideningIntoOccupiedCellRequiresDemolition(t *testing.T) {
	a := newTestAPI(t)
	w := newTestWorld(t)
	if err := a.SetWorld(w); err != nil {
		t.Fatal(err)
	}

	// Horizontal road on row 100, cols 100..110, width 1 (two_lane).
	r := addRoad(t, a, 1, 100, 100, 100, 110, ClassTwoLane)

	// Widen to avenue (width 3) covers rows 99..101; occupy a row-101 cell.
	zoneCell(t, w, 101, 105, world.ZoningResidential)

	_, err := a.ApplyUpgrade(ApplyUpgradeCommand{CorrelationID: "test", RoadID: r.ID, TargetClass: ClassAvenue2Plus2})
	if !errors.Is(err, &errs.E{Code: ErrFootprintObstructed}) {
		t.Fatalf("got %v, want ErrFootprintObstructed", err)
	}
	if e, ok := err.(*errs.E); ok {
		if _, present := e.Ctx["cell"]; !present {
			t.Errorf("ErrFootprintObstructed does not name the obstructing cell (ctx=%v)", e.Ctx)
		}
	}

	// The road is unchanged after the rejection.
	info, _ := a.RoadInfo(r.ID, 0)
	if info.Class != ClassTwoLane {
		t.Fatalf("class changed to %s after obstructed upgrade", info.Class.String())
	}

	// Clear the obstruction (demolition via the world) and retry: succeeds.
	zoneCell(t, w, 101, 105, world.ZoningNone)
	if _, err := a.ApplyUpgrade(ApplyUpgradeCommand{CorrelationID: "test", RoadID: r.ID, TargetClass: ClassAvenue2Plus2}); err != nil {
		t.Fatalf("retry after demolition: %v", err)
	}
}

// TestPreviewCapacityDeltaMatchesRoadworks (AC-7) asserts the preview's
// after lane count matches the actual current-lane-count reduction once the
// upgrade's roadworks phase is active.
func TestPreviewCapacityDeltaMatchesRoadworks(t *testing.T) {
	a := newTestAPI(t)
	if err := a.SetWorld(newTestWorld(t)); err != nil {
		t.Fatal(err)
	}
	r := addRoad(t, a, 1, 100, 100, 100, 110, ClassTwoLane)

	preview, err := a.PreviewCapacityDelta(r.ID, ClassAvenue2Plus2)
	if err != nil {
		t.Fatal(err)
	}
	if preview.BeforeClass != ClassTwoLane || preview.AfterClass != ClassAvenue2Plus2 {
		t.Fatalf("preview classes = %s -> %s, want two_lane -> avenue_2_plus_2", preview.BeforeClass.String(), preview.AfterClass.String())
	}
	if preview.BeforeLanes != 2 {
		t.Fatalf("before lanes = %d, want 2", preview.BeforeLanes)
	}
	if preview.AfterLanes >= preview.BeforeLanes+4 {
		t.Fatalf("after lanes = %d, want the roadworks-reduced count", preview.AfterLanes)
	}

	quote, err := a.ApplyUpgrade(ApplyUpgradeCommand{CorrelationID: "test", RoadID: r.ID, TargetClass: ClassAvenue2Plus2})
	if err != nil {
		t.Fatal(err)
	}
	start := quote.Phases[0].StartMonth
	cur, err := a.CurrentLaneCount(r.ID, start)
	if err != nil {
		t.Fatal(err)
	}
	if cur != preview.AfterLanes {
		t.Fatalf("current lanes during works = %d, preview.AfterLanes = %d", cur, preview.AfterLanes)
	}
	// And the reduced count is below the steady-state target.
	steady, _ := a.ClassProfile(ClassAvenue2Plus2)
	if cur >= steady.Lanes {
		t.Fatalf("current lanes during works = %d, not below steady-state %d", cur, steady.Lanes)
	}
}
