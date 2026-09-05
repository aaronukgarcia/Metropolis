package deathservices

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
)

// ---------------------------------------------------------------------------
// BUG-734 INDEPENDENT DESTRUCTIVE ROUND — UnregisterCemetery /
// UnregisterCrematorium. Attacker != author.
// ---------------------------------------------------------------------------

func bug734WiredAPI(t *testing.T, citizenIDs ...uint64) (*DeathServicesAPI, *services.ServicesAPI) {
	t.Helper()
	d := newIntakenAPI(t, citizenIDs...)
	sv := services.New("corr")
	if err := d.Wire(sv, nil, "corr"); err != nil {
		t.Fatalf("Wire: %v", err)
	}
	return d, sv
}

// serviceRegistered reports whether CrematoriumServiceID is currently live in
// engine.services, via the one probe that cannot lie: a duplicate
// registration attempt.
func serviceRegistered(t *testing.T, sv *services.ServicesAPI) bool {
	t.Helper()
	err := sv.RegisterService(services.ServiceSpec{ID: CrematoriumServiceID, Kind: services.ServiceDeathcare})
	if err == nil {
		// It was NOT registered; undo the probe's side effect.
		if uerr := sv.UnregisterService(CrematoriumServiceID); uerr != nil {
			t.Fatalf("probe cleanup UnregisterService: %v", uerr)
		}
		return false
	}
	return true
}

// --- Double unregister / unknown id ---------------------------------------

func TestAttackBUG734_DoubleUnregisterIsAnErrorNotAPanic(t *testing.T) {
	d, _ := bug734WiredAPI(t)
	if err := d.RegisterCemetery("cem", "corr"); err != nil {
		t.Fatalf("RegisterCemetery: %v", err)
	}
	if err := d.UnregisterCemetery("cem", "corr"); err != nil {
		t.Fatalf("first UnregisterCemetery: %v", err)
	}
	if err := d.UnregisterCemetery("cem", "corr"); err == nil {
		t.Fatal("second UnregisterCemetery succeeded, want ErrUnknownCemetery")
	} else {
		assertRegistryCode(t, err, ErrUnknownCemetery)
	}

	if err := d.RegisterCrematorium("crem", "corr"); err != nil {
		t.Fatalf("RegisterCrematorium: %v", err)
	}
	if err := d.UnregisterCrematorium("crem", "corr"); err != nil {
		t.Fatalf("first UnregisterCrematorium: %v", err)
	}
	if err := d.UnregisterCrematorium("crem", "corr"); err == nil {
		t.Fatal("second UnregisterCrematorium succeeded, want ErrUnknownCrematorium")
	} else {
		assertRegistryCode(t, err, ErrUnknownCrematorium)
	}
}

// --- Shared service deregistration is LAST-one-only, exactly once ---------

func TestAttackBUG734_SharedServiceDeregisteredOnlyWithLastCrematorium(t *testing.T) {
	d, sv := bug734WiredAPI(t)
	for _, id := range []string{"c1", "c2", "c3"} {
		if err := d.RegisterCrematorium(id, "corr"); err != nil {
			t.Fatalf("RegisterCrematorium(%s): %v", id, err)
		}
	}
	if !serviceRegistered(t, sv) {
		t.Fatal("precondition: CrematoriumServiceID should be registered after the first crematorium")
	}
	if err := d.UnregisterCrematorium("c1", "corr"); err != nil {
		t.Fatalf("Unregister c1: %v", err)
	}
	if !serviceRegistered(t, sv) {
		t.Fatal("CrematoriumServiceID deregistered while 2 crematoria still stand — the whole city's deathcare contribution zeroed")
	}
	if err := d.UnregisterCrematorium("c2", "corr"); err != nil {
		t.Fatalf("Unregister c2: %v", err)
	}
	if !serviceRegistered(t, sv) {
		t.Fatal("CrematoriumServiceID deregistered while 1 crematorium still stands")
	}
	if err := d.UnregisterCrematorium("c3", "corr"); err != nil {
		t.Fatalf("Unregister c3 (last): %v", err)
	}
	if serviceRegistered(t, sv) {
		t.Fatal("CrematoriumServiceID still registered after the LAST crematorium was removed")
	}
	// Re-register: the shared service must come back, exactly once, and the
	// second crematorium must not double-register it.
	if err := d.RegisterCrematorium("c4", "corr"); err != nil {
		t.Fatalf("re-RegisterCrematorium: %v", err)
	}
	if !serviceRegistered(t, sv) {
		t.Fatal("CrematoriumServiceID not restored after re-registering a crematorium")
	}
	if err := d.RegisterCrematorium("c5", "corr"); err != nil {
		t.Fatalf("second RegisterCrematorium after restore (duplicate-service handling): %v", err)
	}
}

// TestAttackBUG734_UnregisterLastCrematoriumTwiceOverServicesReset proves the
// best-effort deregistration path: engine.services already lost the service
// (an independent reset) must not turn a legitimate unregister into an error.
func TestAttackBUG734_UnregisterToleratesAlreadyGoneService(t *testing.T) {
	d, sv := bug734WiredAPI(t)
	if err := d.RegisterCrematorium("c1", "corr"); err != nil {
		t.Fatalf("RegisterCrematorium: %v", err)
	}
	if err := sv.UnregisterService(CrematoriumServiceID); err != nil {
		t.Fatalf("independent UnregisterService: %v", err)
	}
	if err := d.UnregisterCrematorium("c1", "corr"); err != nil {
		t.Fatalf("UnregisterCrematorium with the service already gone returned %v, want nil (best-effort)", err)
	}
}

// TestAttackBUG734_UnregisterWorksUnwired: no engine.services at all.
func TestAttackBUG734_UnregisterWorksUnwired(t *testing.T) {
	d := NewDeathServicesAPI(testConfig(t), "corr")
	if err := d.RegisterCrematorium("c1", "corr"); err != nil {
		t.Fatalf("RegisterCrematorium: %v", err)
	}
	if err := d.UnregisterCrematorium("c1", "corr"); err != nil {
		t.Fatalf("UnregisterCrematorium unwired: %v", err)
	}
}

// --- Conservation across a mid-batch unregister ---------------------------

// TestAttackBUG734_UnregisterMidBatchConservationHolds pulls a crematorium
// out from under a city that has bodies awaiting/en-route and proves AC-14's
// identity still balances, that no body is stranded in EnRoute, and that the
// only observable change is that future cremation at that id is refused.
func TestAttackBUG734_UnregisterMidBatchConservationHolds(t *testing.T) {
	ids := []uint64{1, 2, 3, 4, 5, 6, 7, 8}
	d, _ := bug734WiredAPI(t, ids...)
	if err := d.RegisterCemeteryWithCapacity("cem", 4, "corr"); err != nil {
		t.Fatalf("RegisterCemeteryWithCapacity: %v", err)
	}
	if err := d.RegisterCrematorium("crem", "corr"); err != nil {
		t.Fatalf("RegisterCrematorium: %v", err)
	}

	// Move some bodies through the hearse (EnRoute -> Buried) and cremate
	// some others, leaving a genuinely mixed population.
	if _, _, err := d.RunHearseTransport([]uint64{1, 2}, "cem", 1, "corr"); err != nil {
		t.Fatalf("RunHearseTransport: %v", err)
	}
	if _, _, err := d.Cremate([]uint64{3, 4}, "crem", 1, "corr"); err != nil {
		t.Fatalf("Cremate: %v", err)
	}

	before, err := d.Snapshot("corr")
	if err != nil {
		t.Fatalf("Snapshot before: %v", err)
	}
	if before.BodiesReleased != before.Sum() {
		t.Fatalf("precondition conservation already broken: %+v", before)
	}
	if before.BodiesCremated == 0 || before.BodiesBuried == 0 {
		t.Fatalf("vacuous fixture: %+v", before)
	}

	// Pull the crematorium mid-flight.
	if err := d.UnregisterCrematorium("crem", "corr"); err != nil {
		t.Fatalf("UnregisterCrematorium: %v", err)
	}

	after, err := d.Snapshot("corr")
	if err != nil {
		t.Fatalf("Snapshot after: %v", err)
	}
	if after.BodiesReleased != after.Sum() {
		t.Fatalf("CONSERVATION BROKEN by UnregisterCrematorium: %+v", after)
	}
	if after != before {
		t.Fatalf("UnregisterCrematorium mutated the body population: before=%+v after=%+v", before, after)
	}
	if after.BodiesEnRoute != 0 {
		t.Fatalf("%d bodies stranded EnRoute after unregister", after.BodiesEnRoute)
	}
	// Future cremation at the removed id is refused, exactly like an id that
	// never existed.
	if _, _, err := d.Cremate([]uint64{5}, "crem", 2, "corr"); err == nil {
		t.Fatal("Cremate against the unregistered crematorium succeeded")
	} else {
		assertRegistryCode(t, err, ErrUnknownCrematorium)
	}
	// And the population STILL balances after the rejection.
	post, err := d.Snapshot("corr")
	if err != nil {
		t.Fatalf("Snapshot post-rejection: %v", err)
	}
	if post != before {
		t.Fatalf("a rejected Cremate mutated state: before=%+v post=%+v", before, post)
	}
}

// TestAttackBUG734_UnregisterCemeteryKeepsBuriedAndReuseSemantics proves the
// documented cemetery claims: buried bodies untouched, drain capacity drops,
// PlotEligibleForReuse on the removed id errors rather than lying, and a
// re-registered cemetery does NOT inherit the old occupancy (fresh pool).
func TestAttackBUG734_UnregisterCemeteryKeepsBuriedAndReuseSemantics(t *testing.T) {
	d, _ := bug734WiredAPI(t, 1, 2, 3)
	if err := d.RegisterCemeteryWithCapacity("cem", 8, "corr"); err != nil {
		t.Fatalf("RegisterCemeteryWithCapacity: %v", err)
	}
	if err := d.Bury(1, "cem", 1, "corr"); err != nil {
		t.Fatalf("Bury: %v", err)
	}
	capBefore := d.MonthlyDrainCapacity(1)

	before, err := d.Snapshot("corr")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if err := d.UnregisterCemetery("cem", "corr"); err != nil {
		t.Fatalf("UnregisterCemetery: %v", err)
	}
	after, err := d.Snapshot("corr")
	if err != nil {
		t.Fatalf("Snapshot after: %v", err)
	}
	if after != before {
		t.Fatalf("UnregisterCemetery mutated the body population: %+v -> %+v", before, after)
	}
	body, err := d.Body(1, "corr")
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	if body.State != BodyBuried || body.CemeteryID != "cem" {
		t.Fatalf("buried body disturbed by unregister: %+v", body)
	}
	if capAfter := d.MonthlyDrainCapacity(1); capAfter >= capBefore {
		t.Fatalf("drain capacity %d did not fall below %d after removing the only cemetery", capAfter, capBefore)
	}
	if _, err := d.PlotEligibleForReuse("cem", 2, 5, "corr"); err == nil {
		t.Fatal("PlotEligibleForReuse against the removed cemetery succeeded, want ErrUnknownCemetery")
	}
	if err := d.Bury(2, "cem", 2, "corr"); err == nil {
		t.Fatal("Bury into the removed cemetery succeeded")
	} else {
		assertRegistryCode(t, err, ErrUnknownCemetery)
	}

	// Re-register: a FRESH pool, not a resurrection of the old occupancy —
	// and capacity is not double-counted.
	if err := d.RegisterCemeteryWithCapacity("cem", 8, "corr"); err != nil {
		t.Fatalf("re-RegisterCemeteryWithCapacity: %v", err)
	}
	occ, capVal, err := d.CemeteryOccupancy("cem", "corr")
	if err != nil {
		t.Fatalf("CemeteryOccupancy: %v", err)
	}
	if capVal != 8 {
		t.Fatalf("re-registered capacity = %d, want 8 (double-counted?)", capVal)
	}
	if occ != 0 {
		t.Fatalf("re-registered occupancy = %d, want 0 (fresh pool)", occ)
	}
	// Capacity must not be DOUBLE counted (one cemetery's worth, not two).
	// It legitimately comes back ONE plot higher than before the unregister,
	// because the re-registered pool is fresh and the previously-occupied
	// plot is free again — the documented "fresh pool" semantics, not a
	// double count. Pinned here so the distinction is explicit.
	got := d.MonthlyDrainCapacity(1)
	if got < capBefore || got > capBefore+1 {
		t.Fatalf("drain capacity after re-register = %d, want %d or %d (double count?)", got, capBefore, capBefore+1)
	}
}
