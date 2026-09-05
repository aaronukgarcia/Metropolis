package compose

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/engine/deathservices"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// ---------------------------------------------------------------------------
// BUG-734 INDEPENDENT DESTRUCTIVE ROUND — registerCompletedServiceBuildings.
// Attacker != author.
// ---------------------------------------------------------------------------

// dsWithPlotCapacity builds a DeathServicesAPI whose data/deathservices.json
// fixture carries an OVERRIDDEN graveyardPlotCapacity, so the helper's claim
// that capacity is data-sourced (GR#15) can be proved by MUTATING the data
// and watching the registered capacity follow.
func dsWithPlotCapacity(t *testing.T, capacity int) *deathservices.DeathServicesAPI {
	t.Helper()
	base, err := deathservices.LoadDefaultConfig("bug734-attack")
	if err != nil {
		t.Fatalf("LoadDefaultConfig: %v", err)
	}
	base.Params.GraveyardPlotCapacity.Value = float64(capacity)
	blob, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, deathservices.FileDeathServices), blob, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := deathservices.LoadConfig(dir, "bug734-attack")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	return deathservices.NewDeathServicesAPI(cfg, "bug734-attack")
}

// TestAttackBUG734_CapacityFollowsData mutates the config's plot capacity and
// requires the registered cemetery's capacity to follow — the GR#15 proof
// that no second capacity formula lives in compose.
func TestAttackBUG734_CapacityFollowsData(t *testing.T) {
	for _, capacity := range []int{7, 137} {
		ds := dsWithPlotCapacity(t, capacity)
		completions := []build.BuildOrder{{ID: 1, BuildingID: "cemetery", Status: build.OrderComplete}}
		if err := registerCompletedServiceBuildings(completions, ds, "corr"); err != nil {
			t.Fatalf("registerCompletedServiceBuildings: %v", err)
		}
		_, capVal, err := ds.CemeteryOccupancy(cemeteryInstanceID(1), "corr")
		if err != nil {
			t.Fatalf("CemeteryOccupancy: %v", err)
		}
		if capVal != int64(capacity) {
			t.Fatalf("registered capacity = %d, want the data-sourced %d (a second capacity formula exists)", capVal, capacity)
		}
	}
}

// TestAttackBUG734_ReplayIsIdempotent re-delivers the SAME completions
// repeatedly (the replay / non-advancing-cursor shape) and requires exactly
// one live registration with unchanged occupancy — a re-registration must
// never reset a cemetery that already holds bodies.
func TestAttackBUG734_ReplayIsIdempotent(t *testing.T) {
	ds := dsWithPlotCapacity(t, 16)
	completions := []build.BuildOrder{
		{ID: 1, BuildingID: "cemetery", Status: build.OrderComplete},
		{ID: 2, BuildingID: "crematorium", Status: build.OrderComplete},
	}
	for i := 0; i < 5; i++ {
		if err := registerCompletedServiceBuildings(completions, ds, "corr"); err != nil {
			t.Fatalf("replay %d: %v", i, err)
		}
	}
	occ, capVal, err := ds.CemeteryOccupancy(cemeteryInstanceID(1), "corr")
	if err != nil {
		t.Fatalf("CemeteryOccupancy: %v", err)
	}
	if capVal != 16 || occ != 0 {
		t.Fatalf("after 5 replays: occupancy=%d capacity=%d, want 0/16", occ, capVal)
	}
	// The city-wide drain capacity must reflect ONE cemetery and ONE
	// crematorium, not five of each.
	single := dsWithPlotCapacity(t, 16)
	if err := registerCompletedServiceBuildings(completions, single, "corr"); err != nil {
		t.Fatalf("single pass: %v", err)
	}
	if got, want := ds.MonthlyDrainCapacity(1), single.MonthlyDrainCapacity(1); got != want {
		t.Fatalf("replayed drain capacity = %d, single-pass = %d (replay double-counted)", got, want)
	}
}

// TestAttackBUG734_OverlappingWindowsRegisterOnce models a caller whose
// cursor lags: successive calls carry OVERLAPPING slices.
func TestAttackBUG734_OverlappingWindowsRegisterOnce(t *testing.T) {
	ds := dsWithPlotCapacity(t, 5)
	all := []build.BuildOrder{
		{ID: 1, BuildingID: "cemetery"},
		{ID: 2, BuildingID: "cemetery"},
		{ID: 3, BuildingID: "crematorium"},
	}
	windows := [][]build.BuildOrder{all[0:2], all[1:3], all[0:3], all[2:3]}
	for i, w := range windows {
		if err := registerCompletedServiceBuildings(w, ds, "corr"); err != nil {
			t.Fatalf("window %d: %v", i, err)
		}
	}
	single := dsWithPlotCapacity(t, 5)
	if err := registerCompletedServiceBuildings(all, single, "corr"); err != nil {
		t.Fatalf("single: %v", err)
	}
	if got, want := ds.MonthlyDrainCapacity(1), single.MonthlyDrainCapacity(1); got != want {
		t.Fatalf("overlapping windows drain capacity = %d, want %d", got, want)
	}
	// Distinct orders must get DISTINCT ids (no collision between the two
	// cemeteries, nor between cemetery 1 and crematorium 1).
	if cemeteryInstanceID(1) == cemeteryInstanceID(2) || cemeteryInstanceID(1) == crematoriumInstanceID(1) {
		t.Fatal("instance-id derivation collides across orders/kinds")
	}
}

// TestAttackBUG734_UnknownBuildingIDIsRecordedNotSilent (F3, FIXED, round-2
// follow-up rename): a catalogue id the helper does not know (a
// renamed/added deathservices building, or a typo in data/buildings.json)
// registers nothing, exactly like round-1's original finding — but is now
// ALSO recorded via a discarded [ErrUnknownDeathServiceBuildingKind]
// (GR#17), observable through [errs.Recent] rather than vanishing with zero
// trace. This test previously pinned the SILENT (pre-fix) behaviour under
// the name ...IsSILENTLYSkipped and a t.Log-only "FINDING"; it now asserts
// the fixed behaviour directly so a regression back to silence reds here
// instead of merely being logged.
func TestAttackBUG734_UnknownBuildingIDIsRecordedNotSilent(t *testing.T) {
	ds := dsWithPlotCapacity(t, 5)
	before := ds.MonthlyDrainCapacity(1)
	const probeCorrelationID = "attack-bug734-unknown-kind-probe"
	unknown := []build.BuildOrder{
		{ID: 1, BuildingID: "Cemetery", Status: build.OrderComplete},       // case drift
		{ID: 2, BuildingID: "cemetery_large", Status: build.OrderComplete}, // a plausible future catalogue id
		{ID: 3, BuildingID: "graveyard", Status: build.OrderComplete},      // the spec's own synonym
		{ID: 4, BuildingID: " cemetery ", Status: build.OrderComplete},     // whitespace
		{ID: 5, BuildingID: "", Status: build.OrderComplete},               // a plain zone order — never a skip worth recording
	}
	if err := registerCompletedServiceBuildings(unknown, ds, probeCorrelationID); err != nil {
		t.Fatalf("unknown ids returned an error: %v", err)
	}
	if got := ds.MonthlyDrainCapacity(1); got != before {
		t.Fatalf("an unknown BuildingID registered something: %d -> %d", before, got)
	}

	// FIXED: unlike round-1, this is no longer a zero-trace silent drop —
	// at least one MET-G815 (ErrUnknownDeathServiceBuildingKind) entry for
	// THIS call's correlation ID must be observable. errs' in-memory ring
	// coalesces consecutive same-Code entries (Repeat incremented, only the
	// MOST RECENT occurrence's Ctx retained — see the helper's own doc note
	// on this), so this only asserts "recorded at least once", not "once
	// per near-miss id" — the coalescing itself is a documented, accepted
	// tradeoff, not a regression to prove here.
	found := false
	for _, e := range errs.Recent() {
		if e.Code == ErrUnknownDeathServiceBuildingKind && e.CorrelationID == probeCorrelationID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("BUG-734 REGRESSION: unknown BuildingIDs are silent again — no ErrUnknownDeathServiceBuildingKind entry (correlation %q) found in errs.Recent()", probeCorrelationID)
	}
}

// TestAttackBUG734_IncompleteOrdersAreSkipped (F4, FIXED, round-2 follow-up
// rename): the helper now checks Status — a caller mistakenly passing a
// Queue() slice (containing in-progress orders) never opens a cemetery for
// a building site that has not finished. This test previously pinned the
// UNGUARDED (pre-fix) behaviour under the name ...AreRegisteredAnyway and a
// t.Log-only "FINDING"; it now asserts the guard directly.
func TestAttackBUG734_IncompleteOrdersAreSkipped(t *testing.T) {
	ds := dsWithPlotCapacity(t, 5)
	inflight := []build.BuildOrder{{ID: 9, BuildingID: "cemetery", Status: build.OrderInProgress}}
	if err := registerCompletedServiceBuildings(inflight, ds, "corr"); err != nil {
		t.Fatalf("registerCompletedServiceBuildings: %v", err)
	}
	if _, _, err := ds.CemeteryOccupancy(cemeteryInstanceID(9), "corr"); err == nil {
		t.Fatalf("BUG-734 REGRESSION: an in-progress (not OrderComplete) order registered a cemetery — the Status guard is gone")
	}
}

// TestAttackBUG734_NilAPIAndEmptyInput: the documented no-op paths.
func TestAttackBUG734_NilAPIAndEmptyInput(t *testing.T) {
	if err := registerCompletedServiceBuildings(nil, nil, "corr"); err != nil {
		t.Fatalf("nil/nil returned %v, want nil", err)
	}
	if err := registerCompletedServiceBuildings([]build.BuildOrder{{ID: 1, BuildingID: "cemetery"}}, nil, "corr"); err != nil {
		t.Fatalf("nil ds returned %v, want nil (documented no-op)", err)
	}
	ds := dsWithPlotCapacity(t, 5)
	if err := registerCompletedServiceBuildings(nil, ds, "corr"); err != nil {
		t.Fatalf("empty completions returned %v", err)
	}
}
