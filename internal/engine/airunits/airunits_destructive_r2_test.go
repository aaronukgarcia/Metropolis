package airunits

// GR#23 round-2 independent destructive attack on MOD-074 r1's
// ErrPilotAlreadyAssigned fix (AssignPilot double-assignment guard). Probes
// the release/reassignment lifecycle for stale-index and double-release
// bugs the single-guard fix could plausibly have missed, plus a copy-guard
// check on the two exported accessors that could leak internal state.

import "testing"

// TestReleasePilotThenReassignSameUnit attacks the release->reassign cycle:
// assign P to A, release P from A, reassign P to A again. Must succeed
// cleanly with no stale conflict residue from the first assignment.
func TestReleasePilotThenReassignSameUnit(t *testing.T) {
	e := newTestEnv(t)
	a, err := e.a.Purchase(UnitPolice, 10)
	if err != nil {
		t.Fatalf("Purchase: %v", err)
	}
	mustSet(t, e.a.AssignPilot(a, pilotQualified))
	assertPilotOn(t, e.a, a, pilotQualified)

	mustSet(t, e.a.RemovePilot(a))
	assertPilotOn(t, e.a, a, 0)

	// Reassigning the SAME pilot to the SAME (now-vacant) unit must succeed:
	// findPilotLocked must not still report a conflict from stale state.
	if err := e.a.AssignPilot(a, pilotQualified); err != nil {
		t.Fatalf("AssignPilot after RemovePilot on same unit must succeed, got: %v", err)
	}
	assertPilotOn(t, e.a, a, pilotQualified)
}

// TestDoubleRemovePilotIsHarmlessNoOp attacks RemovePilot called twice in a
// row on the same chopper: the second call must not error and must not
// corrupt state (still no pilot, still out-of-service-for-no-pilot).
func TestDoubleRemovePilotIsHarmlessNoOp(t *testing.T) {
	e := newTestEnv(t)
	a, err := e.a.Purchase(UnitPolice, 10)
	if err != nil {
		t.Fatalf("Purchase: %v", err)
	}
	mustSet(t, e.a.AssignPilot(a, pilotQualified))

	mustSet(t, e.a.RemovePilot(a))
	// Second RemovePilot on an already-pilotless chopper: doc says "harmless
	// no-op" — must not error.
	if err := e.a.RemovePilot(a); err != nil {
		t.Fatalf("second RemovePilot on already-pilotless unit must be a harmless no-op, got: %v", err)
	}
	assertPilotOn(t, e.a, a, 0)
}

// TestRemovePilotNeverAssignedIsHarmlessNoOp attacks RemovePilot on a
// chopper that was purchased but never had a pilot assigned at all.
func TestRemovePilotNeverAssignedIsHarmlessNoOp(t *testing.T) {
	e := newTestEnv(t)
	a, err := e.a.Purchase(UnitPolice, 10)
	if err != nil {
		t.Fatalf("Purchase: %v", err)
	}
	if err := e.a.RemovePilot(a); err != nil {
		t.Fatalf("RemovePilot on a never-assigned unit must be a harmless no-op, got: %v", err)
	}
	assertPilotOn(t, e.a, a, 0)
}

// TestFleetSnapshotIsIndependentCopy attacks the Fleet() accessor for an
// aliasing leak: mutating the returned slice (or fields of its elements)
// must never be observable through a subsequent Fleet()/UnitStatus() call,
// since UnitStatus is a value type built fresh per call (unitStatusLocked),
// not a pointer into the live chopper.
func TestFleetSnapshotIsIndependentCopy(t *testing.T) {
	e := newTestEnv(t)
	a, err := e.a.Purchase(UnitPolice, 10)
	if err != nil {
		t.Fatalf("Purchase: %v", err)
	}
	mustSet(t, e.a.AssignPilot(a, pilotQualified))

	snap := e.a.Fleet()
	if len(snap) != 1 {
		t.Fatalf("Fleet() len = %d, want 1", len(snap))
	}
	// Mutate the returned snapshot's element directly.
	snap[0].Pilot = 999999
	snap[0].State = StateOutOfService

	// Re-fetch: the live chopper must be unaffected by the mutation above.
	st, ok, err := e.a.UnitStatus(a)
	if err != nil || !ok {
		t.Fatalf("UnitStatus: ok=%v err=%v", ok, err)
	}
	if st.Pilot != pilotQualified {
		t.Fatalf("Fleet() snapshot mutation leaked into live state: Pilot = %v, want %v (aliasing bug)", st.Pilot, pilotQualified)
	}
	if st.State != StateAvailable {
		t.Fatalf("Fleet() snapshot mutation leaked into live state: State = %v, want Available (aliasing bug)", st.State)
	}
}
