package refuse

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/logistics"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
)

// Regression (Destructive-MOD039 r4): a duplicate siteID in any disposal
// Register call must be REJECTED with ErrDisposalSiteUnavailable, never
// silently replacing the existing site and destroying its durable state.
// The three breaks this family guards:
//
//  1. RegisterLandfill re-register wiped a delivered-but-unprocessed backlog
//     (AC-11 identity: generated=495 vs collected+uncollected+inTransit+
//     backlog=0).
//  2. CapAndReclaim → RegisterLandfill(same ID) reset the local `used` fill,
//     so a later different-instance Wire re-seeded an empty shelf (AC-8
//     RemainingCapacity rose 0→1000).
//  3. Incinerator/compost re-register zeroed energy/airshed/compost
//     (AC-9/AC-10).

// Break 1: re-registering a landfill must not wipe its delivered backlog.
func TestRegisterLandfillDuplicateRejectedBacklogPreserved(t *testing.T) {
	api, _ := newWiredAPI(t)
	if err := api.RegisterDepot("d1"); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterLandfill("L1", 1_000_000, nil); err != nil {
		t.Fatal(err)
	}
	if err := api.SetGeneralSite("L1"); err != nil {
		t.Fatal(err)
	}
	if err := api.RegisterCell("c1", LandUseResidential, "Dup Road"); err != nil {
		t.Fatal(err)
	}
	if err := api.Generate("c1", 500); err != nil {
		t.Fatal(err)
	}
	if err := api.ScheduleRound("r1", "d1", []string{"c1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.RunRound("r1"); err != nil {
		t.Fatal(err)
	}

	// Precondition: the round delivered general waste into L1's backlog
	// (not yet processed).
	backlogBefore, err := api.TonnesDisposalBacklog(StreamGeneral)
	if err != nil {
		t.Fatal(err)
	}
	if backlogBefore <= 0 {
		t.Fatalf("precondition: expected a nonzero general backlog, got %d", backlogBefore)
	}

	// Duplicate registration must be rejected, not silently replace.
	err = api.RegisterLandfill("L1", 1_000_000, nil)
	assertErrCode(t, err, ErrDisposalSiteUnavailable)

	// The backlog survives and the mass-conservation identity still holds.
	backlogAfter, err := api.TonnesDisposalBacklog(StreamGeneral)
	if err != nil {
		t.Fatal(err)
	}
	if backlogAfter != backlogBefore {
		t.Fatalf("re-register wiped the landfill backlog: %d -> %d", backlogBefore, backlogAfter)
	}
	assertIdentity(t, api)
}

// Break 2: re-registering a capped-and-reclaimed landfill must not reset the
// local `used` fill, or a later different-instance Wire re-seeds an empty
// shelf and RemainingCapacity rises 0→1000.
func TestRegisterLandfillAfterReclaimRejectedFillPreserved(t *testing.T) {
	api := newTestAPI(t)
	lg1, err := logistics.LoadDefault("refuse-test")
	if err != nil {
		t.Fatal(err)
	}
	sv, err := services.LoadDefault("refuse-test")
	if err != nil {
		t.Fatal(err)
	}
	w := &recordingWellbeing{}
	if err := api.Wire(lg1, sv, w); err != nil {
		t.Fatal(err)
	}
	if err := api.SetFunding(1.0); err != nil {
		t.Fatal(err)
	}
	if err := api.SetTrucks(1000); err != nil {
		t.Fatal(err)
	}

	if err := api.RegisterLandfill("L1", 1000, []string{"n1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.RouteGeneralToSite("L1", 1000); err != nil {
		t.Fatal(err)
	}
	rem, err := api.RemainingCapacity("L1")
	if err != nil {
		t.Fatal(err)
	}
	if rem != 0 {
		t.Fatalf("precondition: after fill remaining capacity = %d, want 0", rem)
	}
	if err := api.CapAndReclaim("L1"); err != nil {
		t.Fatal(err)
	}

	// Re-opening the same site ID must be rejected, preserving the local
	// `used` fill record.
	err = api.RegisterLandfill("L1", 1000, []string{"n1"})
	assertErrCode(t, err, ErrDisposalSiteUnavailable)

	// A different-instance Wire must re-seed the shelf from the preserved
	// fill, so RemainingCapacity stays 0 rather than rising to 1000.
	lg2, err := logistics.LoadDefault("refuse-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := api.Wire(lg2, sv, w); err != nil {
		t.Fatal(err)
	}

	remAfter, err := api.RemainingCapacity("L1")
	if err != nil {
		t.Fatal(err)
	}
	if remAfter != 0 {
		t.Fatalf("re-register reset the landfill fill: remaining capacity rose 0 -> %d", remAfter)
	}
}

// Break 3 (incinerator): re-registering must not zero energy/airshed.
func TestRegisterIncineratorDuplicateRejected(t *testing.T) {
	api, _ := newWiredAPI(t)
	if err := api.RegisterIncinerator("I1"); err != nil {
		t.Fatal(err)
	}
	if _, err := api.RouteGeneralToSite("I1", 1000); err != nil {
		t.Fatal(err)
	}
	energyBefore, err := api.EnergyOutput("I1")
	if err != nil {
		t.Fatal(err)
	}
	airshedBefore, err := api.AirshedPollution("I1")
	if err != nil {
		t.Fatal(err)
	}
	if energyBefore <= 0 || airshedBefore <= 0 {
		t.Fatalf("precondition: incineration should produce energy/airshed, got %d/%v", energyBefore, airshedBefore)
	}

	err = api.RegisterIncinerator("I1")
	assertErrCode(t, err, ErrDisposalSiteUnavailable)

	energyAfter, _ := api.EnergyOutput("I1")
	airshedAfter, _ := api.AirshedPollution("I1")
	if energyAfter != energyBefore {
		t.Fatalf("re-register zeroed incinerator energy: %d -> %d", energyBefore, energyAfter)
	}
	if airshedAfter != airshedBefore {
		t.Fatalf("re-register zeroed incinerator airshed: %v -> %v", airshedBefore, airshedAfter)
	}
}

// Break 3 (compost): re-registering must not zero compost output.
func TestRegisterCompostSiteDuplicateRejected(t *testing.T) {
	api, _ := newWiredAPI(t)
	if err := api.RegisterCompostSite("C1"); err != nil {
		t.Fatal(err)
	}
	if _, err := api.RouteFoodToCompost("C1", 1000); err != nil {
		t.Fatal(err)
	}
	compostBefore, err := api.CompostOutput("C1")
	if err != nil {
		t.Fatal(err)
	}
	if compostBefore <= 0 {
		t.Fatalf("precondition: compost output should be positive, got %d", compostBefore)
	}

	err = api.RegisterCompostSite("C1")
	assertErrCode(t, err, ErrDisposalSiteUnavailable)

	compostAfter, _ := api.CompostOutput("C1")
	if compostAfter != compostBefore {
		t.Fatalf("re-register zeroed compost output: %d -> %d", compostBefore, compostAfter)
	}
}
