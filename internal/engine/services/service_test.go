package services

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
)

// --- AC-2: extensible kind registration ----------------------------------

// TestExtensibleServiceKindRegistration registers a synthetic kind at
// runtime and asserts it is queryable through the same ServicesAPI methods
// as a built-in §10 kind — the extensibility contract (no enum change
// required for a new service category).
func TestExtensibleServiceKindRegistration(t *testing.T) {
	a := testAPI(t)

	const synthetic ServiceKind = "synthetic.test-service"
	if err := a.RegisterKind(synthetic, KindDef{Name: "Synthetic test service", Benchmark: "police"}); err != nil {
		t.Fatalf("RegisterKind: %v", err)
	}
	if def, ok := a.KindDef(synthetic); !ok || def.Name != "Synthetic test service" {
		t.Fatalf("KindDef(synthetic) = %+v, %v; want registered", def, ok)
	}

	// The synthetic kind registers a service through the same path a
	// built-in kind would, and its capacity is queryable back.
	registerService(t, a, "synthetic-1", synthetic, 42, 10, 0)
	if cap, err := a.Capacity("synthetic-1"); err != nil || cap != 42 {
		t.Fatalf("Capacity(synthetic-1) = %v, %v; want 42, nil", cap, err)
	}
}

// --- AC-9: upgrade path raises the capacity ceiling ----------------------

// TestUpgradeRaisesCapacityCeiling asserts an upgraded service instance has
// a HIGHER capacity ceiling than its pre-upgrade state at identical
// funding (AC-9): upgrading changes the ceiling, funding only affects
// realised quality within it.
func TestUpgradeRaisesCapacityCeiling(t *testing.T) {
	a := testAPI(t)

	spec := ServiceSpec{
		ID:   "clinic-1",
		Kind: ServiceHealthcare,
		UpgradePath: []UpgradeStep{
			{BuildingID: "clinic", Name: "Clinic", Milestone: 2, CapacityCeiling: 150},
			{BuildingID: "small_hospital", Name: "Small hospital", Milestone: 6, CapacityCeiling: 500},
		},
	}
	if err := a.RegisterService(spec); err != nil {
		t.Fatalf("RegisterService: %v", err)
	}

	before, err := a.Capacity("clinic-1")
	if err != nil {
		t.Fatalf("Capacity before upgrade: %v", err)
	}
	if err := a.SetFunding("clinic-1", 1.0); err != nil {
		t.Fatalf("SetFunding: %v", err)
	}
	if err := a.Upgrade("clinic-1"); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	after, err := a.Capacity("clinic-1")
	if err != nil {
		t.Fatalf("Capacity after upgrade: %v", err)
	}
	if after <= before {
		t.Fatalf("upgraded capacity ceiling = %v, want > pre-upgrade %v", after, before)
	}

	// Funding is unchanged by the upgrade — the slider and the upgrade path
	// are distinct (AC-9's false-pass guard).
	if lvl, _ := a.FundingLevel("clinic-1"); lvl != 1.0 {
		t.Errorf("funding after upgrade = %v, want unchanged 1.0", lvl)
	}

	// Upgrading past the final step is rejected, never a silent no-op.
	if err := a.Upgrade("clinic-1"); err == nil {
		t.Fatal("Upgrade past the final step returned nil, want ErrUpgradeUnavailable")
	} else {
		assertCode(t, err, ErrUpgradeUnavailable)
	}
}

// --- AC-10: capacity sourced from the catalogue --------------------------

// TestCatalogueCapacitySourcedFromEntry loads a buildings.json fixture
// entry (the real data/buildings.json "clinic") and asserts the registered
// service's verbatim capacity equals the catalogue entry's capacityRaw
// field — not a duplicate hand-authored value.
func TestCatalogueCapacitySourcedFromEntry(t *testing.T) {
	dir, err := data.ResolveDataDir(testCorrelationID())
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	buildings, err := data.LoadBuildings(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("LoadBuildings: %v", err)
	}

	var clinic data.BuildingEntry
	for _, e := range buildings.Entries {
		if e.ID == "clinic" {
			clinic = e
			break
		}
	}
	if clinic.ID == "" {
		t.Fatal("clinic entry not found in data/buildings.json")
	}

	a := testAPI(t)
	spec := ServiceSpecFromBuilding("clinic-1", ServiceHealthcare, clinic)
	if err := a.RegisterService(spec); err != nil {
		t.Fatalf("RegisterService: %v", err)
	}

	got, err := a.CapacityRaw("clinic-1")
	if err != nil {
		t.Fatalf("CapacityRaw: %v", err)
	}
	if got != clinic.CapacityRaw {
		t.Fatalf("CapacityRaw = %q, want catalogue capacityRaw %q (capacity must be sourced, not re-authored)", got, clinic.CapacityRaw)
	}

	// The numeric ceiling is seeded from the catalogue string's leading
	// number ("150 visits/d" → 150), proving the quality math also derives
	// from the catalogue rather than inventing a figure.
	if cap, err := a.Capacity("clinic-1"); err != nil || cap != 150 {
		t.Errorf("Capacity(clinic-1) = %v, %v; want 150, nil (parsed from %q)", cap, err, clinic.CapacityRaw)
	}
}

// TestCapacityFromRawParsesLeadingNumber pins the parser's domain: a plain
// number, a unit-suffixed figure, and a non-numeric string.
func TestCapacityFromRawParsesLeadingNumber(t *testing.T) {
	cases := []struct {
		raw  string
		want float64
	}{
		{"150 visits/d", 150},
		{"240", 240},
		{"4 appliances", 4},
		{"", 0},
		{"reserved", 0},
		{"1,200 beds", 1}, // leading number stops at the comma — documented behaviour
	}
	for _, tc := range cases {
		if got := CapacityFromRaw(tc.raw); got != tc.want {
			t.Errorf("CapacityFromRaw(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

// --- AC-11: unregistered kind/service → registry error -------------------

// TestRegisterServiceUnknownKindRejected (AC-11): registering a service
// against a never-registered kind returns ErrUnknownServiceKind, not a
// silently-created "empty service".
func TestRegisterServiceUnknownKindRejected(t *testing.T) {
	a := testAPI(t)
	err := a.RegisterService(ServiceSpec{ID: "x", Kind: "no-such-kind"})
	assertCode(t, err, ErrUnknownServiceKind)
}

// TestQueryUnregisteredServiceRejected (AC-11, BUG-100's explicit shape):
// querying a service that was never registered returns ErrServiceNotRegistered,
// and — the GR#7 assertion stated explicitly — no zero-value capacity/
// quality record is silently created: the query returns an error, not a
// plausible-looking "0".
func TestQueryUnregisteredServiceRejected(t *testing.T) {
	a := testAPI(t)

	if _, err := a.Capacity("never-registered"); err == nil {
		t.Fatal("Capacity(unregistered) returned nil error")
	} else {
		assertCode(t, err, ErrServiceNotRegistered)
	}
	if _, err := a.Quality("never-registered"); err == nil {
		t.Fatal("Quality(unregistered) returned nil error")
	} else {
		assertCode(t, err, ErrServiceNotRegistered)
	}
	if _, err := a.GrossWageCost("never-registered"); err == nil {
		t.Fatal("GrossWageCost(unregistered) returned nil error")
	} else {
		assertCode(t, err, ErrServiceNotRegistered)
	}
}

// TestUnregisteredServiceDoesNotPanic: an unregistered query never panics.
func TestUnregisteredServiceDoesNotPanic(t *testing.T) {
	a := testAPI(t)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("querying an unregistered service panicked: %v", r)
		}
	}()
	_, _ = a.Capacity("nope")
}

// --- AC-12: invalid funding is rejected, never clamped -------------------

// TestInvalidFundingRejected (AC-12): a funding level outside [0,1]
// (negative, or above 100%) is rejected with ErrInvalidFunding, never
// silently clamped without the caller knowing a clamp occurred.
func TestInvalidFundingRejected(t *testing.T) {
	a := testAPI(t)
	registerService(t, a, "s", ServiceHealthcare, 100, 10, 0)

	for _, level := range []float64{-0.1, 1.1, 2.0} {
		if err := a.SetFunding("s", level); err == nil {
			t.Errorf("SetFunding(%v) returned nil, want ErrInvalidFunding", level)
		} else {
			assertCode(t, err, ErrInvalidFunding)
		}
	}

	// A valid level within [0,1] is accepted.
	if err := a.SetFunding("s", 0.5); err != nil {
		t.Fatalf("SetFunding(0.5): %v", err)
	}
}

// --- AC-7: §4 tier gating via the injected unlock gate -------------------

// TestTierGateClinicAtTier2 exercises §4's literal early-tier examples
// (AC-7): funding the basic clinic (a tier-2/Hamlet unlock) is rejected
// below tier-2 and accepted at/above it.
func TestTierGateClinicAtTier2(t *testing.T) {
	a := New(testCorrelationID())
	// Clinic enabling building unlocks at tier 2 (§4).
	err := a.RegisterService(ServiceSpec{ID: "clinic-1", Kind: ServiceHealthcare, Milestone: 2})
	if err != nil {
		t.Fatalf("RegisterService: %v", err)
	}

	// Below tier 2: the gate reports tier 2 not reached → funding rejected.
	if err := a.SetUnlockGate(UnlockGateFunc(func(tier int) bool { return tier <= 1 })); err != nil {
		t.Fatalf("SetUnlockGate: %v", err)
	}
	if err := a.SetFunding("clinic-1", 1.0); err == nil {
		t.Fatal("funding a tier-2 clinic below tier-2 returned nil, want ErrNotUnlocked")
	} else {
		assertCode(t, err, ErrNotUnlocked)
	}

	// At/above tier 2: funding succeeds.
	if err := a.SetUnlockGate(UnlockGateFunc(func(tier int) bool { return tier <= 2 })); err != nil {
		t.Fatalf("SetUnlockGate: %v", err)
	}
	if err := a.SetFunding("clinic-1", 1.0); err != nil {
		t.Fatalf("funding a tier-2 clinic at tier-2: %v, want nil", err)
	}
}

// TestTierGateFirePoliceAtTier4 exercises AC-7's second anchored example:
// fire & police posts are a tier-4/Small Town (5,000-population) unlock,
// rejected below tier 4 and accepted at/above it.
func TestTierGateFirePoliceAtTier4(t *testing.T) {
	a := New(testCorrelationID())
	if err := a.RegisterService(ServiceSpec{ID: "fire-1", Kind: ServiceFire, Milestone: 4}); err != nil {
		t.Fatalf("RegisterService: %v", err)
	}
	if err := a.RegisterService(ServiceSpec{ID: "police-1", Kind: ServicePoliceJail, Milestone: 4}); err != nil {
		t.Fatalf("RegisterService: %v", err)
	}

	// Below tier 4 (e.g. only tiers 1-3 reached): both rejected.
	if err := a.SetUnlockGate(UnlockGateFunc(func(tier int) bool { return tier <= 3 })); err != nil {
		t.Fatalf("SetUnlockGate: %v", err)
	}
	for _, id := range []ServiceID{"fire-1", "police-1"} {
		if err := a.SetFunding(id, 1.0); err == nil {
			t.Errorf("funding %s below tier-4 returned nil, want ErrNotUnlocked", id)
		} else {
			assertCode(t, err, ErrNotUnlocked)
		}
	}

	// At/above tier 4: both accepted.
	if err := a.SetUnlockGate(UnlockGateFunc(func(tier int) bool { return tier <= 4 })); err != nil {
		t.Fatalf("SetUnlockGate: %v", err)
	}
	for _, id := range []ServiceID{"fire-1", "police-1"} {
		if err := a.SetFunding(id, 1.0); err != nil {
			t.Errorf("funding %s at tier-4: %v, want nil", id, err)
		}
	}
}

// TestTierGateFailsClosedWhenNoGateWired: with no gate installed, funding a
// milestone-carrying service fails closed (the seam engine.unlocks would
// otherwise leave milestone state unknown).
func TestTierGateFailsClosedWhenNoGateWired(t *testing.T) {
	a := New(testCorrelationID())
	if err := a.RegisterService(ServiceSpec{ID: "clinic-1", Kind: ServiceHealthcare, Milestone: 2}); err != nil {
		t.Fatalf("RegisterService: %v", err)
	}
	if err := a.SetFunding("clinic-1", 1.0); err == nil {
		t.Fatal("funding without a gate returned nil, want ErrNotUnlocked")
	} else {
		assertCode(t, err, ErrNotUnlocked)
	}
}

// --- GR#3: no silent last-write-wins on registration --------------------

func TestRegisterDuplicateServiceRejected(t *testing.T) {
	a := testAPI(t)
	registerService(t, a, "dup", ServiceHealthcare, 100, 10, 0)
	err := a.RegisterService(ServiceSpec{ID: "dup", Kind: ServiceHealthcare})
	assertCode(t, err, ErrDuplicateService)
}
