package services

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

func testCorrelationID() string {
	return errs.NewCorrelationID()
}

// allowAllGate is a permissive UnlockGate for tests that do not care about
// tier gating (AC-7 has its own dedicated tests with a controllable gate).
func allowAllGate() UnlockGate {
	return UnlockGateFunc(func(int) bool { return true })
}

// testAPI builds a ready ServicesAPI with the built-in kinds registered and
// a permissive unlock gate wired, so funding works without tier concerns.
func testAPI(t *testing.T) *ServicesAPI {
	t.Helper()
	a := New(testCorrelationID())
	if err := a.SetUnlockGate(allowAllGate()); err != nil {
		t.Fatalf("SetUnlockGate: %v", err)
	}
	return a
}

// testLoadedAPI builds a ServicesAPI from the repository's own
// data/services.json (via ResolveDataDir), for tests that check the actual
// spec-transcribed/placeholder data (AC-4/AC-5/AC-6/AC-8) rather than a
// synthetic fixture.
func testLoadedAPI(t *testing.T) *ServicesAPI {
	t.Helper()
	dir, err := data.ResolveDataDir(testCorrelationID())
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	a, err := Load(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("Load real data/services.json: %v", err)
	}
	if err := a.SetUnlockGate(allowAllGate()); err != nil {
		t.Fatalf("SetUnlockGate: %v", err)
	}
	return a
}

// assertCode asserts err is a registry-sourced *errs.E with the given code.
func assertCode(t *testing.T, err error, wantCode string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error with code %s, got nil", wantCode)
	}
	e, ok := err.(*errs.E)
	if !ok {
		t.Fatalf("expected *errs.E, got %T: %v", err, err)
	}
	if e.Code != wantCode {
		t.Errorf("e.Code = %s, want %s (err: %v)", e.Code, wantCode, err)
	}
}

// registerHealthService registers a healthcare service instance with a
// controlled capacity ceiling, coverage radius, staffing need and upgrade
// path — the common fixture for quality/staffing/upgrade tests.
func registerService(t *testing.T, a *ServicesAPI, id ServiceID, kind ServiceKind, capCeiling, coverage, staffingNeed float64) {
	t.Helper()
	err := a.RegisterService(ServiceSpec{
		ID:             id,
		Kind:           kind,
		CapacityRaw:    "fixture",
		CoverageRadius: coverage,
		StaffingNeed:   staffingNeed,
		Milestone:      0, // no tier constraint for these fixtures
		UpgradePath:    []UpgradeStep{{BuildingID: "base", Name: "base", CapacityCeiling: capCeiling}},
	})
	if err != nil {
		t.Fatalf("RegisterService(%s): %v", id, err)
	}
	// Fully fund by default: the quality fixtures measure degradation from a
	// funded reference point, and Milestone 0 skips the tier gate so this
	// needs no gate wiring.
	if err := a.SetFunding(id, 1.0); err != nil {
		t.Fatalf("SetFunding(%s, 1.0): %v", id, err)
	}
}
