package roads

import (
	"errors"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestNamingDeterministic (AC-9) asserts naming the same seed+id twice
// produces the identical name, for both the road and the polymorphic paths.
func TestNamingDeterministic(t *testing.T) {
	a := newTestAPI(t)

	for _, class := range []RoadClass{ClassAlley, ClassTwoLane, ClassDualCarriageway, ClassMotorway} {
		n1, err := a.NameRoad(42, 12345, class)
		if err != nil {
			t.Fatalf("NameRoad: %v", err)
		}
		n2, err := a.NameRoad(42, 12345, class)
		if err != nil {
			t.Fatalf("NameRoad: %v", err)
		}
		if n1 != n2 || n1 == "" {
			t.Fatalf("NameRoad(%s) not stable: %q vs %q", class.String(), n1, n2)
		}
	}

	for _, kind := range []ObjectKind{KindCivicBuilding, KindInfrastructure, KindDistrict, KindTransit} {
		n1, err := a.NameFor(kind, 42, 777)
		if err != nil {
			t.Fatalf("NameFor(%s): %v", kind.String(), err)
		}
		n2, err := a.NameFor(kind, 42, 777)
		if err != nil {
			t.Fatalf("NameFor(%s): %v", kind.String(), err)
		}
		if n1 != n2 || n1 == "" {
			t.Fatalf("NameFor(%s) not stable: %q vs %q", kind.String(), n1, n2)
		}
	}
}

// TestNumberedClassesUseNumberingScheme (AC-9) asserts the two highest rungs
// use §20's M-/A- numbering rather than a place-name+suffix pair.
func TestNumberedClassesUseNumberingScheme(t *testing.T) {
	a := newTestAPI(t)
	m, err := a.NameRoad(42, 1, ClassMotorway)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(m, "M") {
		t.Fatalf("motorway name %q, want M- prefix", m)
	}
	x, err := a.NameRoad(42, 2, ClassUrbanExpressway)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(x, "A") {
		t.Fatalf("urban expressway name %q, want A- prefix", x)
	}
}

// TestNamingContinuation (AC-9) asserts a road continuing straight through a
// same-class junction keeps its name, and a class change breaks the
// continuation (yielding a fresh name).
func TestNamingContinuation(t *testing.T) {
	a := newTestAPI(t)
	first := addRoad(t, a, 1, 100, 100, 100, 110, ClassTwoLane)

	// Continue straight through the junction, same class: same name.
	cont := addRoad(t, a, 2, 100, 110, 100, 120, ClassTwoLane)
	cont = continueFrom(t, a, cont.ID, first.ID, ClassTwoLane)
	if cont.Name != first.Name {
		t.Fatalf("continuation name %q != first name %q", cont.Name, first.Name)
	}

	// A class change breaks the continuation: fresh name.
	changed := addRoad(t, a, 3, 100, 120, 100, 130, ClassGravel)
	changed = continueFrom(t, a, changed.ID, first.ID, ClassGravel)
	if changed.Name == first.Name {
		t.Fatalf("class-change continuation kept name %q, want a fresh name", changed.Name)
	}
}

// continueFrom re-adds a road with a ContinueFrom hint (the addRoad helper
// does not carry one) and returns the updated view.
func continueFrom(t *testing.T, a *RoadsAPI, id, from RoadID, class RoadClass) Road {
	t.Helper()
	rs, ok := a.roads[id]
	if !ok {
		t.Fatalf("road %d missing", id)
	}
	// Re-add the same edge with the continuation hint (idempotent overwrite).
	r, err := a.AddRoad(AddRoadCommand{
		CorrelationID: "test", ID: id,
		Start: rs.start, End: rs.end, Class: class, ContinueFrom: from,
	})
	if err != nil {
		t.Fatalf("AddRoad(continue): %v", err)
	}
	return r
}

// TestCivicAndInfrastructureNamingDistinctFromRoad (AC-10) asserts civic
// building and infrastructure naming are distinct from road naming and from
// each other.
func TestCivicAndInfrastructureNamingDistinctFromRoad(t *testing.T) {
	a := newTestAPI(t)

	road, err := a.NameRoad(42, 5, ClassTwoLane)
	if err != nil {
		t.Fatal(err)
	}
	civic, err := a.NameFor(KindCivicBuilding, 42, 5)
	if err != nil {
		t.Fatal(err)
	}
	infra, err := a.NameFor(KindInfrastructure, 42, 5)
	if err != nil {
		t.Fatal(err)
	}
	if civic == road {
		t.Errorf("civic name %q == road name %q", civic, road)
	}
	if infra == road {
		t.Errorf("infrastructure name %q == road name %q", infra, road)
	}
	if civic == infra {
		t.Errorf("civic name %q == infrastructure name %q", civic, infra)
	}
	if !strings.Contains(infra, "No. ") {
		t.Errorf("infrastructure name %q, want functional numbering (\"No. N\")", infra)
	}
}

// TestRenameSurvivesLaterNamingPass (AC-11) asserts a renamed road's name
// survives a subsequent naming-service invocation for a DIFFERENT object —
// the specific false-pass the AC calls out.
func TestRenameSurvivesLaterNamingPass(t *testing.T) {
	a := newTestAPI(t)
	r := addRoad(t, a, 1, 100, 100, 100, 110, ClassTwoLane)
	original := r.Name

	if err := a.Rename(RenameCommand{CorrelationID: "test", Kind: KindRoad, Seed: 42, ID: uint64(r.ID), NewName: "My Favourite Road"}); err != nil {
		t.Fatalf("Rename: %v", err)
	}

	// Invoke the naming service again for a DIFFERENT object (AC-11's check).
	if _, err := a.NameFor(KindCivicBuilding, 42, 999); err != nil {
		t.Fatalf("NameFor (unrelated): %v", err)
	}
	if _, err := a.NameRoad(42, 888, ClassGravel); err != nil {
		t.Fatalf("NameRoad (unrelated): %v", err)
	}

	info, err := a.RoadInfo(r.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "My Favourite Road" {
		t.Fatalf("renamed road name %q, want %q (auto-name would be %q)", info.Name, "My Favourite Road", original)
	}
	if !info.Renamed {
		t.Errorf("renamed flag not set on the road view")
	}

	// NameRoad for the renamed (seed, id) also returns the player's name.
	got, err := a.NameRoad(42, uint64(r.ID), ClassTwoLane)
	if err != nil {
		t.Fatal(err)
	}
	if got != "My Favourite Road" {
		t.Fatalf("NameRoad after rename = %q, want %q", got, "My Favourite Road")
	}
}

// TestUnrecognisedKindRejected (AC-13) asserts an unknown ObjectKind is
// rejected loudly, and that KindRoad through NameFor is steered to NameRoad.
func TestUnrecognisedKindRejected(t *testing.T) {
	a := newTestAPI(t)
	if _, err := a.NameFor(ObjectKind(99), 42, 1); !errors.Is(err, &errs.E{Code: ErrUnknownObjectKind}) {
		t.Fatalf("got %v, want ErrUnknownObjectKind", err)
	}
	if _, err := a.NameFor(KindRoad, 42, 1); !errors.Is(err, &errs.E{Code: ErrInvalidInput}) {
		t.Fatalf("got %v, want ErrInvalidInput for KindRoad through NameFor", err)
	}
}
