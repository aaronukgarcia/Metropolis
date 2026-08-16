package services

import (
	"math"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
)

// This file is the SEC-093/SEC-094/SEC-095 regression suite (Destructive
// reject round). Each test below FAILS against the pre-fix code it was
// written against, and passes only with the fix in place.

// --- SEC-093: non-finite inputs are rejected at the boundary -------------

// TestNonFiniteInputsRejected exercises every float-taking command handler
// with NaN/±Inf and asserts a registry-sourced ErrNonFiniteInput rather
// than a silently-stored non-finite value. Pre-fix, SetFunding's
// `level < 0 || level > 1` is false for NaN (NaN is stored, then collapsed
// by clamp01), and UpdateDemand/UpdateStaffing only clamp `< 0` (NaN passes
// through untouched) — so every arm below fails against the pre-fix code.
func TestNonFiniteInputsRejected(t *testing.T) {
	a := testLoadedAPI(t)
	registerService(t, a, "svc", ServiceHealthcare, 100, 10, 0)

	nonFinite := []float64{math.NaN(), math.Inf(1), math.Inf(-1)}
	for _, v := range nonFinite {
		if err := a.SetFunding("svc", v); err == nil {
			t.Errorf("SetFunding(%v) returned nil, want ErrNonFiniteInput", v)
		} else {
			assertCode(t, err, ErrNonFiniteInput)
		}
	}

	for _, v := range nonFinite {
		if err := a.UpdateDemand("svc", v, 1); err == nil {
			t.Errorf("UpdateDemand(demand=%v) returned nil, want ErrNonFiniteInput", v)
		} else {
			assertCode(t, err, ErrNonFiniteInput)
		}
		if err := a.UpdateDemand("svc", 1, v); err == nil {
			t.Errorf("UpdateDemand(distance=%v) returned nil, want ErrNonFiniteInput", v)
		} else {
			assertCode(t, err, ErrNonFiniteInput)
		}
	}

	for _, v := range nonFinite {
		if err := a.UpdateStaffing("svc", v); err == nil {
			t.Errorf("UpdateStaffing(%v) returned nil, want ErrNonFiniteInput", v)
		} else {
			assertCode(t, err, ErrNonFiniteInput)
		}
	}

	for _, v := range nonFinite {
		if err := a.SetPoolStaff("nursing", v); err == nil {
			t.Errorf("SetPoolStaff(%v) returned nil, want ErrNonFiniteInput", v)
		} else {
			assertCode(t, err, ErrNonFiniteInput)
		}
	}
}

// TestRegisterServiceNonFiniteRejected closes SEC-093's remaining command
// surface: RegisterService must also reject a non-finite float field in the
// spec rather than storing it (a NaN coverage radius / staffing need /
// capacity ceiling would flow into the quality arithmetic).
func TestRegisterServiceNonFiniteRejected(t *testing.T) {
	a := testAPI(t)
	base := ServiceSpec{ID: "svc", Kind: ServiceHealthcare, CoverageRadius: 10, X: 1, Y: 1, StaffingNeed: 5}

	withRadius := base
	withRadius.CoverageRadius = math.NaN()
	if err := a.RegisterService(withRadius); err == nil {
		t.Error("RegisterService with NaN coverage radius returned nil, want ErrNonFiniteInput")
	} else {
		assertCode(t, err, ErrNonFiniteInput)
	}

	withNeed := base
	withNeed.StaffingNeed = math.Inf(1)
	if err := a.RegisterService(withNeed); err == nil {
		t.Error("RegisterService with +Inf staffing need returned nil, want ErrNonFiniteInput")
	} else {
		assertCode(t, err, ErrNonFiniteInput)
	}

	withCeiling := base
	withCeiling.UpgradePath = []UpgradeStep{{BuildingID: "base", Name: "base", CapacityCeiling: math.NaN()}}
	if err := a.RegisterService(withCeiling); err == nil {
		t.Error("RegisterService with NaN capacity ceiling returned nil, want ErrNonFiniteInput")
	} else {
		assertCode(t, err, ErrNonFiniteInput)
	}
}

// --- SEC-094: NetFiscalCost overflow is surfaced, not silently wrong -----

// TestNetFiscalCostOverflowSurfaced drives the gross wage bill up to int64
// saturation so gross×incomeRate overflows, and asserts NetFiscalCost
// returns ErrFiscalOverflow rather than a net that silently reads ≈gross
// at a 100% rate. Pre-fix, the SafeMul overflow bool is discarded, so the
// clawback is MaxInt64/10000 and net ≈ gross with a nil error.
func TestNetFiscalCostOverflowSurfaced(t *testing.T) {
	a := testLoadedAPI(t)
	// A staffing need large enough that gross wage (need × wage placeholder)
	// saturates ClampInt64FromFloat at MaxInt64.
	registerService(t, a, "big", ServiceHealthcare, 100, 10, 1e20)

	gross, err := a.GrossWageCost("big")
	if err != nil {
		t.Fatalf("GrossWageCost: %v", err)
	}
	if gross <= 0 {
		t.Fatalf("GrossWageCost = %v, want positive (saturated)", gross)
	}

	if _, err := a.NetFiscalCost("big", finance.BasisPoints(incomeTaxBasisPointScale)); err == nil {
		t.Fatal("NetFiscalCost with an overflowing clawback returned nil, want ErrFiscalOverflow")
	} else {
		assertCode(t, err, ErrFiscalOverflow)
	}
}

// --- SEC-095: Upgrade is tier-gated, UpgradeStep.Milestone is live --------

// TestUpgradeTierGatedOnNextStep: a tier-2 clinic whose upgrade path
// reaches a tier-6 hospital cannot be upgraded while the gate only reaches
// tier 2 (the next step's milestone is a live check, not dead data), and
// succeeds once the gate reaches tier 6. Pre-fix, Upgrade never consulted
// the gate at all, so the first assertion fails.
func TestUpgradeTierGatedOnNextStep(t *testing.T) {
	a := New(testCorrelationID())
	err := a.RegisterService(ServiceSpec{
		ID:   "clinic-1",
		Kind: ServiceHealthcare,
		UpgradePath: []UpgradeStep{
			{BuildingID: "clinic", Name: "Clinic", Milestone: 2, CapacityCeiling: 150},
			{BuildingID: "small_hospital", Name: "Small hospital", Milestone: 6, CapacityCeiling: 500},
		},
	})
	if err != nil {
		t.Fatalf("RegisterService: %v", err)
	}

	// The gate reaches only tier 2: upgrading to the tier-6 hospital must be
	// rejected.
	if err := a.SetUnlockGate(UnlockGateFunc(func(tier int) bool { return tier <= 2 })); err != nil {
		t.Fatalf("SetUnlockGate: %v", err)
	}
	if err := a.Upgrade("clinic-1"); err == nil {
		t.Fatal("Upgrade to a tier-6 hospital while the gate reaches tier 2 returned nil, want ErrNotUnlocked")
	} else {
		assertCode(t, err, ErrNotUnlocked)
	}

	// The gate now reaches tier 6: the upgrade succeeds.
	if err := a.SetUnlockGate(UnlockGateFunc(func(tier int) bool { return tier <= 6 })); err != nil {
		t.Fatalf("SetUnlockGate: %v", err)
	}
	if err := a.Upgrade("clinic-1"); err != nil {
		t.Fatalf("Upgrade with the gate reaching tier 6: %v, want nil", err)
	}
}

// TestUpgradeFailsClosedWhenNoGateWired: with no gate installed, an
// upgrade whose next step carries a milestone fails closed (the same
// fail-closed rule SetFunding applies).
func TestUpgradeFailsClosedWhenNoGateWired(t *testing.T) {
	a := New(testCorrelationID())
	if err := a.RegisterService(ServiceSpec{
		ID:   "clinic-1",
		Kind: ServiceHealthcare,
		UpgradePath: []UpgradeStep{
			{BuildingID: "clinic", Name: "Clinic", Milestone: 2, CapacityCeiling: 150},
			{BuildingID: "small_hospital", Name: "Small hospital", Milestone: 6, CapacityCeiling: 500},
		},
	}); err != nil {
		t.Fatalf("RegisterService: %v", err)
	}
	if err := a.Upgrade("clinic-1"); err == nil {
		t.Fatal("Upgrade without a gate returned nil, want ErrNotUnlocked")
	} else {
		assertCode(t, err, ErrNotUnlocked)
	}
}
