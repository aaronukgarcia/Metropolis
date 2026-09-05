package deathservices

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
)

// ---------------------------------------------------------------------------
// BUG-734 — UnregisterCemetery/UnregisterCrematorium (the bulldoze seam
// DeathServicesAPI never had). Mirrors engine.services.UnregisterService's
// own test shapes: unknown-id rejection, a clean register/unregister cycle,
// and conservation across it.
// ---------------------------------------------------------------------------

func TestUnregisterCemetery_UnknownIDRejected(t *testing.T) {
	d := NewDeathServicesAPI(testConfig(t), "corr")
	if err := d.UnregisterCemetery("never-registered", "corr"); err == nil {
		t.Fatalf("UnregisterCemetery(unknown id) succeeded, want ErrUnknownCemetery")
	} else {
		assertRegistryCode(t, err, ErrUnknownCemetery)
	}
	if err := d.UnregisterCemetery("", "corr"); err == nil {
		t.Fatalf("UnregisterCemetery(\"\") succeeded")
	}
}

func TestUnregisterCrematorium_UnknownIDRejected(t *testing.T) {
	d := NewDeathServicesAPI(testConfig(t), "corr")
	if err := d.UnregisterCrematorium("never-registered", "corr"); err == nil {
		t.Fatalf("UnregisterCrematorium(unknown id) succeeded, want ErrUnknownCrematorium")
	} else {
		assertRegistryCode(t, err, ErrUnknownCrematorium)
	}
	if err := d.UnregisterCrematorium("", "corr"); err == nil {
		t.Fatalf("UnregisterCrematorium(\"\") succeeded")
	}
}

// TestUnregisterCemetery_FuturePlacementStopsButBodiesStay proves the
// documented semantics literally: a body already buried keeps its
// BodyBuried/CemeteryID record untouched, while a NEW burial attempt against
// the removed id is rejected exactly like an id that was never registered.
func TestUnregisterCemetery_FuturePlacementStopsButBodiesStay(t *testing.T) {
	d := newIntakenAPI(t, 1, 2)
	if err := d.RegisterCemeteryWithCapacity("cem-1", 10, "corr"); err != nil {
		t.Fatalf("RegisterCemeteryWithCapacity: %v", err)
	}
	if err := d.Bury(1, "cem-1", 1, "corr"); err != nil {
		t.Fatalf("Bury: %v", err)
	}
	before, err := d.Body(1, "corr")
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	if before.State != BodyBuried || before.CemeteryID != "cem-1" {
		t.Fatalf("body before unregister = %+v, want Buried at cem-1", before)
	}

	if err := d.UnregisterCemetery("cem-1", "corr"); err != nil {
		t.Fatalf("UnregisterCemetery: %v", err)
	}

	// The already-buried body's record is unchanged.
	after, err := d.Body(1, "corr")
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	if after != before {
		t.Fatalf("body record changed by UnregisterCemetery: before=%+v after=%+v", before, after)
	}

	// Future placement against the removed id is rejected.
	if err := d.Bury(2, "cem-1", 1, "corr"); err == nil {
		t.Fatalf("Bury against an unregistered (removed) cemetery succeeded")
	} else {
		assertRegistryCode(t, err, ErrUnknownCemetery)
	}

	// Capacity accounting: CemeteryOccupancy against the removed id is the
	// same "unknown" answer as never having registered it — no orphaned
	// capacity figure survives the removal.
	if _, _, err := d.CemeteryOccupancy("cem-1", "corr"); err == nil {
		t.Fatalf("CemeteryOccupancy against a removed cemetery succeeded")
	} else {
		assertRegistryCode(t, err, ErrUnknownCemetery)
	}
}

// TestUnregisterCrematorium_FutureCremationStopsButBodiesStay is
// UnregisterCemetery's mirror for crematoria.
func TestUnregisterCrematorium_FutureCremationStopsButBodiesStay(t *testing.T) {
	d := newIntakenAPI(t, 1, 2)
	if err := d.RegisterCrematorium("crem-1", "corr"); err != nil {
		t.Fatalf("RegisterCrematorium: %v", err)
	}
	cremated, _, err := d.Cremate([]uint64{1}, "crem-1", 1, "corr")
	if err != nil || len(cremated) != 1 {
		t.Fatalf("Cremate: cremated=%v err=%v", cremated, err)
	}
	before, err := d.Body(1, "corr")
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	if before.State != BodyCremated || before.CrematoriumID != "crem-1" {
		t.Fatalf("body before unregister = %+v, want Cremated at crem-1", before)
	}

	if err := d.UnregisterCrematorium("crem-1", "corr"); err != nil {
		t.Fatalf("UnregisterCrematorium: %v", err)
	}

	after, err := d.Body(1, "corr")
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	if after != before {
		t.Fatalf("body record changed by UnregisterCrematorium: before=%+v after=%+v", before, after)
	}

	if _, _, err := d.Cremate([]uint64{2}, "crem-1", 2, "corr"); err == nil {
		t.Fatalf("Cremate against an unregistered (removed) crematorium succeeded")
	} else {
		assertRegistryCode(t, err, ErrUnknownCrematorium)
	}
}

// TestUnregisterCrematorium_SharedServiceIDOnlyDeregisteredWhenLastRemoved
// proves the multi-instance nuance documented on UnregisterCrematorium:
// CrematoriumServiceID is ONE shared engine.services registration across
// every crematorium instance, so removing one of two standing crematoria
// must NOT deregister it — only removing the last one does.
func TestUnregisterCrematorium_SharedServiceIDOnlyDeregisteredWhenLastRemoved(t *testing.T) {
	d := newIntakenAPI(t)
	sv := services.New("corr")
	if err := d.Wire(sv, nil, "corr"); err != nil {
		t.Fatalf("Wire: %v", err)
	}
	if err := d.RegisterCrematorium("crem-1", "corr"); err != nil {
		t.Fatalf("RegisterCrematorium(crem-1): %v", err)
	}
	if err := d.RegisterCrematorium("crem-2", "corr"); err != nil {
		t.Fatalf("RegisterCrematorium(crem-2): %v", err)
	}
	if _, err := sv.ServiceIDs(); err != nil {
		t.Fatalf("ServiceIDs: %v", err)
	}
	ids, err := sv.ServiceIDs()
	if err != nil || len(ids) != 1 {
		t.Fatalf("ServiceIDs() = %v, %v; want exactly one shared registration", ids, err)
	}

	// Remove the first of two — the shared service registration must survive.
	if err := d.UnregisterCrematorium("crem-1", "corr"); err != nil {
		t.Fatalf("UnregisterCrematorium(crem-1): %v", err)
	}
	ids, err = sv.ServiceIDs()
	if err != nil || len(ids) != 1 {
		t.Fatalf("ServiceIDs() after removing 1-of-2 = %v, %v; want still registered (crem-2 stands)", ids, err)
	}

	// Remove the LAST one — now the shared registration must go too.
	if err := d.UnregisterCrematorium("crem-2", "corr"); err != nil {
		t.Fatalf("UnregisterCrematorium(crem-2): %v", err)
	}
	ids, err = sv.ServiceIDs()
	if err != nil || len(ids) != 0 {
		t.Fatalf("ServiceIDs() after removing the last crematorium = %v, %v; want empty", ids, err)
	}

	// A THIRD unregister, once nothing stands, is still a clean
	// ErrUnknownCrematorium (never a services-layer panic/error leak) and
	// never touches engine.services again.
	if err := d.UnregisterCrematorium("crem-2", "corr"); err == nil {
		t.Fatalf("UnregisterCrematorium(already-removed) succeeded")
	} else {
		assertRegistryCode(t, err, ErrUnknownCrematorium)
	}
}

// TestConservation_AcrossRegisterUnregisterCycle (AC-14): unregistering a
// cemetery/crematorium mid-simulation must never disturb the conservation
// identity — every already-disposed body stays counted exactly once, and
// unregistering touches no Body record.
func TestConservation_AcrossRegisterUnregisterCycle(t *testing.T) {
	d := NewDeathServicesAPI(testConfig(t), "corr")
	if err := d.RegisterCemetery("cem-1", "corr"); err != nil {
		t.Fatalf("RegisterCemetery: %v", err)
	}
	if err := d.RegisterCrematorium("crem-1", "corr"); err != nil {
		t.Fatalf("RegisterCrematorium: %v", err)
	}

	const n = 30
	deaths := make([]citizens.RealisedDeath, n)
	for i := 0; i < n; i++ {
		deaths[i] = citizens.RealisedDeath{CitizenID: uint64(i + 1), DeathMonth: 1}
	}
	if _, err := d.Intake(deaths, "corr"); err != nil {
		t.Fatalf("Intake: %v", err)
	}
	checkConservation(t, d)

	half := n / 2
	for i := 1; i <= half; i++ {
		if err := d.Bury(uint64(i), "cem-1", 1, "corr"); err != nil {
			t.Fatalf("Bury(%d): %v", i, err)
		}
	}
	checkConservation(t, d)

	// Unregister the cemetery NOW — every already-buried body must still be
	// counted, and the identity must still hold exactly.
	if err := d.UnregisterCemetery("cem-1", "corr"); err != nil {
		t.Fatalf("UnregisterCemetery: %v", err)
	}
	checkConservation(t, d)

	// Register a NEW cemetery under a different id and finish the batch —
	// conservation must accumulate across the whole cycle, not reset.
	if err := d.RegisterCemeteryWithCapacity("cem-2", int64(n), "corr"); err != nil {
		t.Fatalf("RegisterCemeteryWithCapacity(cem-2): %v", err)
	}
	for i := half + 1; i <= n; i++ {
		if err := d.Bury(uint64(i), "cem-2", 1, "corr"); err != nil {
			t.Fatalf("Bury(%d): %v", i, err)
		}
	}
	checkConservation(t, d)

	// And the crematorium too — unregister after use, conservation still holds.
	if err := d.UnregisterCrematorium("crem-1", "corr"); err != nil {
		t.Fatalf("UnregisterCrematorium: %v", err)
	}
	checkConservation(t, d)
}
