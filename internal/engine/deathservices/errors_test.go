package deathservices

import (
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
)

// deathServicesAPIByteCopy performs SEC-020's attack -- a plain
// DeathServicesAPI struct copy -- via a raw byte-for-byte memcpy through
// unsafe.Pointer, mirroring internal/engine/logistics/security_test.go's
// logisticsAPIByteCopy (the sanctioned pattern, GR#24-safe): a literal
// `cp := *d` is legal Go but go vet's copylocks check statically flags it,
// and this package must pass `go vet ./...`.
func deathServicesAPIByteCopy(d *DeathServicesAPI) *DeathServicesAPI {
	cp := new(DeathServicesAPI)
	*(*[unsafe.Sizeof(DeathServicesAPI{})]byte)(unsafe.Pointer(cp)) = *(*[unsafe.Sizeof(DeathServicesAPI{})]byte)(unsafe.Pointer(d))
	return cp
}

// TestRegistryErrorUnknownCemeteryNoSideEffect (AC-17): burying against an
// unregistered cemetery returns the registry code and leaves no side
// effect (no plot allocated, body stays awaiting).
func TestRegistryErrorUnknownCemeteryNoSideEffect(t *testing.T) {
	d := NewDeathServicesAPI(testConfig(t), "corr")
	if _, err := d.Intake([]citizens.RealisedDeath{{CitizenID: 1, DeathMonth: 1}}, "corr"); err != nil {
		t.Fatalf("Intake: %v", err)
	}
	err := d.Bury(1, "no-such-cemetery", 1, "corr")
	if err == nil {
		t.Fatalf("Bury against an unregistered cemetery succeeded")
	}
	assertRegistryCode(t, err, ErrUnknownCemetery)
	b, _ := d.Body(1, "corr")
	if b.State != BodyAwaiting {
		t.Fatalf("body state = %s after a failed burial, want awaiting (no side effect)", b.State)
	}
}

// TestRegistryErrorUnknownCrematoriumNoSideEffect (AC-17): cremating
// against an unregistered crematorium returns the registry code and
// creates no cremation record.
func TestRegistryErrorUnknownCrematoriumNoSideEffect(t *testing.T) {
	d := NewDeathServicesAPI(testConfig(t), "corr")
	if _, err := d.Intake([]citizens.RealisedDeath{{CitizenID: 1, DeathMonth: 1}}, "corr"); err != nil {
		t.Fatalf("Intake: %v", err)
	}
	_, cost, err := d.Cremate([]uint64{1}, "no-such-crematorium", 1, "corr")
	if err == nil {
		t.Fatalf("Cremate against an unregistered crematorium succeeded")
	}
	assertRegistryCode(t, err, ErrUnknownCrematorium)
	if cost != 0 {
		t.Fatalf("cost = %d after a failed cremation, want 0 (no phantom charge)", cost)
	}
	b, _ := d.Body(1, "corr")
	if b.State != BodyAwaiting {
		t.Fatalf("body state = %s after a failed cremation, want awaiting", b.State)
	}
}

// TestRegistryErrorNoPlotAvailableNoSideEffect (AC-4/AC-17): already
// covered structurally by TestFullCemeteryLandPressureTriage; this test
// pins the registry-code assertion in isolation.
func TestRegistryErrorNoPlotAvailableNoSideEffect(t *testing.T) {
	d := NewDeathServicesAPI(testConfig(t), "corr")
	if err := d.RegisterCemeteryWithCapacity("cem-1", 1, "corr"); err != nil {
		t.Fatalf("RegisterCemeteryWithCapacity: %v", err)
	}
	if _, err := d.Intake([]citizens.RealisedDeath{{CitizenID: 1, DeathMonth: 1}, {CitizenID: 2, DeathMonth: 1}}, "corr"); err != nil {
		t.Fatalf("Intake: %v", err)
	}
	if err := d.Bury(1, "cem-1", 1, "corr"); err != nil {
		t.Fatalf("Bury(1): %v", err)
	}
	err := d.Bury(2, "cem-1", 1, "corr")
	if err == nil {
		t.Fatalf("Bury(2) into a full cemetery succeeded")
	}
	assertRegistryCode(t, err, ErrNoPlotAvailable)
}

// TestRegistryErrorMultiBodyOutsideDispensationNoSideEffect (AC-11/AC-17):
// already covered structurally by TestDispensationRevertsOnEventEnd; this
// test pins the registry-code assertion for a multi-body attempt that was
// NEVER preceded by an active dispensation event at all.
func TestRegistryErrorMultiBodyOutsideDispensationNoSideEffect(t *testing.T) {
	d := NewDeathServicesAPI(testConfig(t), "corr")
	if _, err := d.Intake([]citizens.RealisedDeath{{CitizenID: 1, DeathMonth: 1}, {CitizenID: 2, DeathMonth: 1}}, "corr"); err != nil {
		t.Fatalf("Intake: %v", err)
	}
	_, err := d.Dispense([]uint64{1, 2}, 1, "corr")
	if err == nil {
		t.Fatalf("multi-body Dispense with no active event succeeded")
	}
	assertRegistryCode(t, err, ErrMultiBodyOutsideDispensation)
	for _, id := range []uint64{1, 2} {
		b, _ := d.Body(id, "corr")
		if b.State != BodyAwaiting {
			t.Fatalf("body %d state = %s after a rejected multi-body dispense, want awaiting", id, b.State)
		}
	}
}

// TestRegistryErrorUnknownBodyNoSideEffect (AC-14/AC-17): every disposal
// entrypoint rejects an unknown bodyID with the registry code.
func TestRegistryErrorUnknownBodyNoSideEffect(t *testing.T) {
	d := NewDeathServicesAPI(testConfig(t), "corr")
	if err := d.RegisterCemetery("cem-1", "corr"); err != nil {
		t.Fatalf("RegisterCemetery: %v", err)
	}
	if err := d.RegisterCrematorium("crem-1", "corr"); err != nil {
		t.Fatalf("RegisterCrematorium: %v", err)
	}

	if err := d.Bury(999, "cem-1", 1, "corr"); err == nil {
		t.Fatalf("Bury of an unknown body succeeded")
	} else {
		assertRegistryCode(t, err, ErrUnknownBody)
	}
	if _, _, err := d.Cremate([]uint64{999}, "crem-1", 1, "corr"); err == nil {
		t.Fatalf("Cremate of an unknown body succeeded")
	} else {
		assertRegistryCode(t, err, ErrUnknownBody)
	}
	if _, err := d.Body(999, "corr"); err == nil {
		t.Fatalf("Body(999) succeeded for an unknown body")
	} else {
		assertRegistryCode(t, err, ErrUnknownBody)
	}
}

// TestRegistryErrorUnknownBuildingTypeOnEmptyID (AC-17): registering a
// cemetery/crematorium with an empty ID is rejected with the registry
// code.
func TestRegistryErrorUnknownBuildingTypeOnEmptyID(t *testing.T) {
	d := NewDeathServicesAPI(testConfig(t), "corr")
	if err := d.RegisterCemetery("", "corr"); err == nil {
		t.Fatalf("RegisterCemetery(\"\") succeeded")
	} else {
		assertRegistryCode(t, err, ErrUnknownBuildingType)
	}
	if err := d.RegisterCrematorium("", "corr"); err == nil {
		t.Fatalf("RegisterCrematorium(\"\") succeeded")
	} else {
		assertRegistryCode(t, err, ErrUnknownBuildingType)
	}
}

// TestCopyGuardRejectsStructCopy (SEC-020 family, exercised alongside the
// registry-error suite): a method called on a struct copy of the API
// returns ErrDeathServicesCopied rather than corrupting shared state.
func TestCopyGuardRejectsStructCopy(t *testing.T) {
	d := NewDeathServicesAPI(testConfig(t), "corr")
	cp := deathServicesAPIByteCopy(d)
	_, err := cp.Intake(nil, "corr")
	if err == nil {
		t.Fatalf("Intake on a struct copy returned nil error")
	}
	assertRegistryCode(t, err, ErrDeathServicesCopied)
}
