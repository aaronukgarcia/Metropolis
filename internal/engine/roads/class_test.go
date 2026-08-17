package roads

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestClassLadderHasElevenRungs (AC-3) asserts the full §51 ladder is
// represented: eleven distinct rungs, each with its data-driven lane count,
// parking presence and tree/verge option, and that at least two ADJACENT
// rungs differ in default lane count (gravel=1 vs residential_street=2, and
// two_lane=2 vs avenue_2_plus_2=4).
func TestClassLadderHasElevenRungs(t *testing.T) {
	a := newTestAPI(t)

	if int(numClasses) != 11 {
		t.Fatalf("numClasses = %d, want 11", numClasses)
	}

	// Every rung is a valid class with a distinct data-backed profile.
	seen := make(map[string]bool)
	for c := RoadClass(0); c < numClasses; c++ {
		p, err := a.ClassProfile(c)
		if err != nil {
			t.Fatalf("ClassProfile(%s): %v", c.String(), err)
		}
		if p.Lanes <= 0 {
			t.Errorf("%s lanes = %d, want > 0", c.String(), p.Lanes)
		}
		if p.WidthCells <= 0 {
			t.Errorf("%s widthCells = %d, want > 0", c.String(), p.WidthCells)
		}
		if p.SpeedLimit < p.SpeedMin || p.SpeedLimit > p.SpeedMax {
			t.Errorf("%s speedLimit %d outside [%d,%d]", c.String(), p.SpeedLimit, p.SpeedMin, p.SpeedMax)
		}
		if seen[c.String()] {
			t.Errorf("duplicate class slug %s", c.String())
		}
		seen[c.String()] = true
	}

	// Adjacent rungs with different default lane counts (the AC-3 check).
	gravel, _ := a.ClassProfile(ClassGravel)
	residential, _ := a.ClassProfile(ClassResidentialStreet)
	if gravel.Lanes == residential.Lanes {
		t.Errorf("adjacent rungs gravel(%d) and residential_street(%d) have equal lane counts; at least one adjacent pair must differ", gravel.Lanes, residential.Lanes)
	}
	twoLane, _ := a.ClassProfile(ClassTwoLane)
	avenue, _ := a.ClassProfile(ClassAvenue2Plus2)
	if twoLane.Lanes == avenue.Lanes {
		t.Errorf("adjacent rungs two_lane(%d) and avenue_2_plus_2(%d) have equal lane counts", twoLane.Lanes, avenue.Lanes)
	}

	// Parking / tree-verge are present and differ across the ladder (AC-3's
	// "parking presence, and tree/verge-option fields").
	alley, _ := a.ClassProfile(ClassAlley)
	motorway, _ := a.ClassProfile(ClassMotorway)
	if !alley.Parking || motorway.Parking {
		t.Errorf("parking should differ between alley(%v) and motorway(%v)", alley.Parking, motorway.Parking)
	}
	if alley.TreeVerge || !motorway.TreeVerge {
		t.Errorf("treeVerge should differ between alley(%v) and motorway(%v)", alley.TreeVerge, motorway.TreeVerge)
	}
}

// TestInvalidClassRejected (AC-12) asserts a class outside the ladder is
// rejected with the registry code, never silently defaulted.
func TestInvalidClassRejected(t *testing.T) {
	a := newTestAPI(t)
	_, err := a.ClassProfile(RoadClass(200))
	if err == nil {
		t.Fatal("ClassProfile(200) returned nil error, want ErrInvalidClass")
	}
	if !errors.Is(err, &errs.E{Code: ErrInvalidClass}) {
		t.Fatalf("got %v, want code %s", err, ErrInvalidClass)
	}
}
