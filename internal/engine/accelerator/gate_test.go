package accelerator

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/consumption"
)

// TestMoneyCantBuyUnmetGateNoPartialState is AC-3: a build with ample
// treasury, development points, and a qualifying milestone — but an education
// research output BELOW the data-file threshold — is rejected. The fake
// permit is granted and the fake decommission/FDI sinks are ready, so the
// only thing blocking the build is the expert gate verdict (money alone
// cannot buy it).
func TestMoneyCantBuyUnmetGateNoPartialState(t *testing.T) {
	a, u := loadTestAPI(t)
	threshold := a.ExpertGateThreshold()
	d := wireAll(t, a, u, threshold-1) // rich in everything except education output

	err := a.Build(BuildCommand{Key: CatalogueKey})
	if err == nil {
		t.Fatal("expected the build to be rejected below the expert-gate threshold")
	}
	assertCode(t, err, ErrExpertGateUnmet)

	// AC-13: no partial state — no facility record, no draw, no effect.
	if a.IsBuilt() {
		t.Error("IsBuilt = true after a rejected build; no facility record may be created")
	}
	if a.IsOnline() {
		t.Error("IsOnline = true after a rejected build")
	}
	if a.Prestige() != 0 {
		t.Errorf("prestige = %d after rejection, want 0 (no partial state)", a.Prestige())
	}
	if d.fdi.prospects != 0 {
		t.Errorf("FDI prospects = %d after rejection, want 0 (no anchor draw posted)", d.fdi.prospects)
	}
	if d.decomm.accrued != "" {
		t.Errorf("decommission accrued for %q after rejection, want none", d.decomm.accrued)
	}
	if d.wellbeing.posts != 0 {
		t.Errorf("wellbeing posts = %d after rejection, want 0", d.wellbeing.posts)
	}
}

// TestExpertGateThresholdFlipsVerdict is AC-3's flip: raising ONLY the
// education research output above the data-file threshold — holding every
// other input unchanged — flips the rejected build to accepted.
func TestExpertGateThresholdFlipsVerdict(t *testing.T) {
	a, u := loadTestAPI(t)
	threshold := a.ExpertGateThreshold()
	d := wireAll(t, a, u, threshold-1)

	if err := a.Build(BuildCommand{Key: CatalogueKey}); err == nil {
		t.Fatal("expected rejection while below threshold")
	}

	// Raise ONLY the education output above threshold; nothing else moves.
	d.research.output = threshold
	if err := a.Build(BuildCommand{Key: CatalogueKey}); err != nil {
		t.Fatalf("build rejected even after the education output reached threshold: %v", err)
	}
	if !a.IsBuilt() {
		t.Error("IsBuilt = false after the threshold flip accepted the build")
	}
}

// TestUnknownAcceleratorRejectsBuild is AC-13's unknown-key case: an
// out-of-taxonomy key is rejected with a registry code and no state.
func TestUnknownAcceleratorRejectsBuild(t *testing.T) {
	a, u := loadTestAPI(t)
	wireAll(t, a, u, a.ExpertGateThreshold())

	err := a.Build(BuildCommand{Key: "some_other_ring"})
	if err == nil {
		t.Fatal("expected an unknown accelerator key to be rejected")
	}
	assertCode(t, err, ErrUnknownAccelerator)
	if a.IsBuilt() {
		t.Error("IsBuilt = true after an unknown-key rejection")
	}
}

// TestNoPermitRejectsBuild is AC-11's inherited-permit case: with the permit
// source reporting no valid permit, the build is rejected with ErrNoPermit
// even though the expert gate and education output qualify.
func TestNoPermitRejectsBuild(t *testing.T) {
	a, u := loadTestAPI(t)
	d := wireAll(t, a, u, a.ExpertGateThreshold()) // education output meets threshold
	d.permits.permitted = false                    // but no permit

	err := a.Build(BuildCommand{Key: CatalogueKey})
	if err == nil {
		t.Fatal("expected the build to be rejected without a valid permit")
	}
	assertCode(t, err, ErrNoPermit)
	if a.IsBuilt() {
		t.Error("IsBuilt = true after a no-permit rejection")
	}
}

// TestDrawOnUnbuiltFacilityRejected is AC-13's draw-on-unbuilt case.
func TestDrawOnUnbuiltFacilityRejected(t *testing.T) {
	a, u := loadTestAPI(t)
	wireAll(t, a, u, a.ExpertGateThreshold())

	if _, err := a.ResolvedDemand(demandOptions()); err == nil {
		t.Fatal("expected a draw on an unbuilt facility to be rejected")
	} else {
		assertCode(t, err, ErrDrawUnbuilt)
	}
	if _, err := a.PeakDemand(demandOptions()); err == nil {
		t.Fatal("expected a peak draw on an unbuilt facility to be rejected")
	} else {
		assertCode(t, err, ErrDrawUnbuilt)
	}
	if err := a.Operate(1); err == nil {
		t.Fatal("expected Operate on an unbuilt facility to be rejected")
	} else {
		assertCode(t, err, ErrDrawUnbuilt)
	}
}

// demandOptions is the neutral per-tick context shared by draw tests: month
// index 2 (March) has all three seasonal multipliers at 1.0, so the raw
// coefficient-driven draw is observed unmodified.
func demandOptions() consumption.DemandOptions {
	return consumption.DemandOptions{MonthIndex: 2, GasNetworkPresent: true}
}
