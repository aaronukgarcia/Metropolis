package build

import (
	"encoding/json"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
)

// ---------------------------------------------------------------------------
// GR#23 INDEPENDENT DESTRUCTIVE ROUND 2 — the remedy for round 1's REJECT.
// Focus: can the RegisterCompletedServices sweep RESURRECT a demolished
// building, DOUBLE-COUNT a rebuilt cell, drift nondeterministically, or cost
// the perf gate?
// ---------------------------------------------------------------------------

// --- R2 ATTACK 1: demolish -> save -> restore must NOT resurrect ------------

func TestAttackRound2_DemolishedBuildingIsNotResurrectedByTheLoadSweep(t *testing.T) {
	svc := services.New(testCorr())
	orig, _ := newBuildServicesFixtureIn(t, svc)
	tile, local := tile00(), local00()
	id, err := orig.SubmitBuildCommand(BuildCommand{
		Tile: tile, Local: local, OwnerID: testOwner,
		Zone: ZoneDwelling, Month: 6, BuildingID: clinicBuildingID,
	})
	if err != nil {
		t.Fatalf("SubmitBuildCommand: %v", err)
	}
	tickToCompletion(t, orig, id, 60)
	if _, err := orig.SubmitDemolishCommand(DemolishCommand{Tile: tile, Local: local, OwnerID: testOwner}); err != nil {
		t.Fatalf("demolish: %v", err)
	}
	if cs, _ := svc.CoverageSummary(); cs.ServiceCount != 0 {
		t.Fatalf("setup: demolish left %d services", cs.ServiceCount)
	}
	// The demolished order is still in the queue with complete=true and a
	// buildingID — exactly the shape the sweep must NOT pick up.
	if o := orderByID(t, orig.Queue(), id); o.Status != OrderComplete {
		t.Fatalf("setup: demolished order is no longer complete in the queue: %+v", o)
	}

	// Explicit sweep on the LIVE api first (Tick's every-tick call also runs it).
	if err := orig.RegisterCompletedServices(); err != nil {
		t.Fatalf("RegisterCompletedServices: %v", err)
	}
	if cs, _ := svc.CoverageSummary(); cs.ServiceCount != 0 {
		t.Fatalf("in-process sweep RESURRECTED a demolished building: %+v", cs)
	}
	for i := int64(0); i < 5; i++ {
		if err := orig.Tick(i); err != nil {
			t.Fatalf("Tick: %v", err)
		}
	}
	if cs, _ := svc.CoverageSummary(); cs.ServiceCount != 0 {
		t.Fatalf("Tick's sweep RESURRECTED a demolished building: %+v", cs)
	}

	// ...and across a save/restore, where serviceByOrder is wiped and the
	// gate has to come entirely from the RESTORED b.structures.
	root := saveInto(t, orig, "orig")
	svc2 := services.New(testCorr())
	reloaded, _ := newBuildServicesFixtureIn(t, svc2)
	loadInto(t, root, reloaded, "reloaded")
	if err := reloaded.RegisterCompletedServices(); err != nil {
		t.Fatalf("post-load RegisterCompletedServices: %v", err)
	}
	for i := int64(0); i < 5; i++ {
		if err := reloaded.Tick(i); err != nil {
			t.Fatalf("post-load Tick: %v", err)
		}
	}
	if cs, _ := svc2.CoverageSummary(); cs.ServiceCount != 0 || cs.TotalCapacity != 0 {
		t.Fatalf("RESURRECTION: the load sweep re-registered a building demolished "+
			"before the save: %+v", cs)
	}
}

// --- R2 ATTACK 2: rebuild cycles — stale orders must not double-count ------
//
// After N build/demolish cycles on ONE cell the queue holds N complete
// orders, all with a buildingID, and only the LAST is standing. If the
// sweep's gate keyed on the cell (or on "complete && buildingID") instead of
// the standing ORDER id, a restore would register every historical order and
// multiply capacity by N.
func TestAttackRound2_RebuildCyclesDoNotDoubleCountAcrossRestore(t *testing.T) {
	const cycles = 4
	svc := services.New(testCorr())
	orig, _ := newBuildServicesFixtureIn(t, svc)
	tile, local := tile00(), local00()
	for i := 0; i < cycles; i++ {
		id, err := orig.SubmitBuildCommand(BuildCommand{
			Tile: tile, Local: local, OwnerID: testOwner,
			Zone: ZoneDwelling, Month: 6, BuildingID: clinicBuildingID,
		})
		if err != nil {
			t.Fatalf("cycle %d build: %v", i, err)
		}
		tickToCompletion(t, orig, id, 60)
		if i < cycles-1 {
			if _, err := orig.SubmitDemolishCommand(DemolishCommand{Tile: tile, Local: local, OwnerID: testOwner}); err != nil {
				t.Fatalf("cycle %d demolish: %v", i, err)
			}
		}
	}
	// Queue now holds `cycles` complete clinic orders; exactly one stands.
	if got := len(orig.Queue()); got != cycles {
		t.Fatalf("setup: queue length %d, want %d", got, cycles)
	}
	before, err := svc.CoverageSummary()
	if err != nil {
		t.Fatalf("CoverageSummary: %v", err)
	}
	if before.ServiceCount != 1 {
		t.Fatalf("setup: %d services standing, want 1", before.ServiceCount)
	}

	root := saveInto(t, orig, "orig")
	svc2 := services.New(testCorr())
	reloaded, _ := newBuildServicesFixtureIn(t, svc2)
	loadInto(t, root, reloaded, "reloaded")
	if err := reloaded.RegisterCompletedServices(); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	for i := int64(0); i < 5; i++ {
		if err := reloaded.Tick(i); err != nil {
			t.Fatalf("Tick: %v", err)
		}
	}
	after, err := svc2.CoverageSummary()
	if err != nil {
		t.Fatalf("CoverageSummary: %v", err)
	}
	if after.ServiceCount != 1 || after.TotalCapacity != before.TotalCapacity {
		t.Fatalf("DOUBLE-COUNT: %d build/demolish cycles restored as %+v (want exactly "+
			"the one standing clinic, %+v)", cycles, after, before)
	}
	// And the surviving registration must be the STANDING order's id, not a
	// demolished predecessor's.
	ids, err := svc2.ServiceIDs()
	if err != nil {
		t.Fatalf("ServiceIDs: %v", err)
	}
	if len(ids) != 1 || string(ids[0]) != "build-order-4" {
		t.Fatalf("restored service ids = %v, want exactly [build-order-4] (the standing order)", ids)
	}
	// Demolishing the survivor after the restore must still clear it — proof
	// the sweep rebuilt serviceByOrder with the RIGHT key.
	if _, err := reloaded.SubmitDemolishCommand(DemolishCommand{Tile: tile, Local: local, OwnerID: testOwner}); err != nil {
		t.Fatalf("post-restore demolish: %v", err)
	}
	if cs, _ := svc2.CoverageSummary(); cs.ServiceCount != 0 {
		t.Fatalf("post-restore demolish left %+v — serviceByOrder was rebuilt with the wrong key", cs)
	}
}

// --- R2 ATTACK 3: many standing services restore exactly once each --------

func TestAttackRound2_ManyStandingServicesRestoreExactlyOnce(t *testing.T) {
	svc := services.New(testCorr())
	orig, _ := newBuildServicesFixtureIn(t, svc)
	type placed struct {
		row, col int
		bid      string
	}
	plan := []placed{
		{0, 0, clinicBuildingID},
		{0, 5, fireBuildingID},
		{5, 0, shopBuildingID}, // non-service
		{5, 5, clinicBuildingID},
		{10, 10, fireBuildingID},
		{10, 15, clinicBuildingID},
	}
	for _, p := range plan {
		id, err := orig.SubmitBuildCommand(BuildCommand{
			Tile: tile00(), Local: world.CellLocal{Row: p.row, Col: p.col},
			OwnerID: testOwner, Zone: ZoneDwelling, Month: 6, BuildingID: p.bid,
		})
		if err != nil {
			t.Fatalf("build %v: %v", p, err)
		}
		tickToCompletion(t, orig, id, 60)
	}
	// Demolish two of them (one service, one non-service).
	for _, p := range []placed{{0, 5, fireBuildingID}, {5, 0, shopBuildingID}} {
		if _, err := orig.SubmitDemolishCommand(DemolishCommand{
			Tile: tile00(), Local: world.CellLocal{Row: p.row, Col: p.col}, OwnerID: testOwner,
		}); err != nil {
			t.Fatalf("demolish %v: %v", p, err)
		}
	}
	before, err := svc.CoverageSummary()
	if err != nil {
		t.Fatalf("CoverageSummary: %v", err)
	}
	beforeIDs, err := svc.ServiceIDs()
	if err != nil {
		t.Fatalf("ServiceIDs: %v", err)
	}

	root := saveInto(t, orig, "orig")
	svc2 := services.New(testCorr())
	reloaded, _ := newBuildServicesFixtureIn(t, svc2)
	loadInto(t, root, reloaded, "reloaded")
	if err := reloaded.RegisterCompletedServices(); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	after, err := svc2.CoverageSummary()
	if err != nil {
		t.Fatalf("CoverageSummary: %v", err)
	}
	afterIDs, err := svc2.ServiceIDs()
	if err != nil {
		t.Fatalf("ServiceIDs: %v", err)
	}
	if after != before {
		t.Fatalf("restore did not reproduce coverage exactly: before=%+v after=%+v", before, after)
	}
	bj, _ := json.Marshal(beforeIDs)
	aj, _ := json.Marshal(afterIDs)
	if string(bj) != string(aj) {
		t.Fatalf("restored service id SET differs: before=%s after=%s", bj, aj)
	}
}

// --- R2 ATTACK 4: sweep idempotence + no churn ----------------------------

func TestAttackRound2_SweepIsIdempotentAndDoesNotChurn(t *testing.T) {
	svc := services.New(testCorr())
	b, _ := newBuildServicesFixtureIn(t, svc)
	for i := 0; i < 3; i++ {
		id, err := b.SubmitBuildCommand(BuildCommand{
			Tile: tile00(), Local: world.CellLocal{Row: i, Col: i},
			OwnerID: testOwner, Zone: ZoneDwelling, Month: 6, BuildingID: clinicBuildingID,
		})
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		tickToCompletion(t, b, id, 60)
	}
	snap := func() string {
		ids, err := svc.ServiceIDs()
		if err != nil {
			t.Fatalf("ServiceIDs: %v", err)
		}
		cs, err := svc.CoverageSummary()
		if err != nil {
			t.Fatalf("CoverageSummary: %v", err)
		}
		blob, _ := json.Marshal(struct {
			IDs []services.ServiceID
			CS  services.CoverageSummary
		}{ids, cs})
		return string(blob)
	}
	first := snap()
	for i := 0; i < 25; i++ {
		if err := b.RegisterCompletedServices(); err != nil {
			t.Fatalf("sweep %d: %v", i, err)
		}
		if got := snap(); got != first {
			t.Fatalf("sweep %d churned state: %s -> %s", i, first, got)
		}
	}
	// The serviceByOrder index must hold exactly the three standing orders.
	b.mu.Lock()
	n := len(b.serviceByOrder)
	b.mu.Unlock()
	if n != 3 {
		t.Fatalf("serviceByOrder holds %d entries after 25 sweeps, want 3", n)
	}
}

// --- R2 ATTACK 5: sweep determinism across identical runs -----------------

func TestAttackRound2_SweepOrderIsDeterministicAcrossRestores(t *testing.T) {
	// The sweep iterates b.queue (slice) but builds its `standing` set by
	// ranging b.structures (a MAP). Prove across many runs that the map range
	// cannot leak an order into the result (GR#21 / the map-range-with-break
	// class).
	build := func() string {
		svc := services.New(testCorr())
		orig, _ := newBuildServicesFixtureIn(t, svc)
		for i := 0; i < 8; i++ {
			bid := clinicBuildingID
			if i%3 == 0 {
				bid = fireBuildingID
			}
			if i%4 == 0 {
				bid = shopBuildingID
			}
			id, err := orig.SubmitBuildCommand(BuildCommand{
				Tile: tile00(), Local: world.CellLocal{Row: i, Col: 2 * i},
				OwnerID: testOwner, Zone: ZoneDwelling, Month: 6, BuildingID: bid,
			})
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			tickToCompletion(t, orig, id, 60)
			if i%5 == 0 {
				if _, err := orig.SubmitDemolishCommand(DemolishCommand{
					Tile: tile00(), Local: world.CellLocal{Row: i, Col: 2 * i}, OwnerID: testOwner,
				}); err != nil {
					t.Fatalf("demolish: %v", err)
				}
			}
		}
		root := saveInto(t, orig, "orig")
		svc2 := services.New(testCorr())
		reloaded, _ := newBuildServicesFixtureIn(t, svc2)
		loadInto(t, root, reloaded, "reloaded")
		if err := reloaded.RegisterCompletedServices(); err != nil {
			t.Fatalf("sweep: %v", err)
		}
		ids, err := svc2.ServiceIDs()
		if err != nil {
			t.Fatalf("ServiceIDs: %v", err)
		}
		cs, err := svc2.CoverageSummary()
		if err != nil {
			t.Fatalf("CoverageSummary: %v", err)
		}
		blob, _ := json.Marshal(struct {
			IDs []services.ServiceID
			CS  services.CoverageSummary
		}{ids, cs})
		return string(blob)
	}
	first := build()
	for i := 0; i < 8; i++ {
		if got := build(); got != first {
			t.Fatalf("restore run %d diverged (GR#21):\n first=%s\n got  =%s", i, first, got)
		}
	}
}

// --- R2 ATTACK 6: the stale-ghost residue of a LIVE-composition rewind ----
//
// The wedge is gone (duplicate is now idempotent success), but engine.services
// still holds instances no restored state accounts for when a savepoint is
// loaded into a LIVE composition that had already registered them. Documented
// here as an attacker observation, not asserted as a pass/fail gate — the
// in-tree restore path (RestoreLatestSnapshotOrGenesis) always uses a freshly
// wired composition, and Composition.Load's own doc scopes it to that.
func TestAttackRound2_LiveRewindStaleGhostObservation(t *testing.T) {
	svc := services.New(testCorr())
	b, _ := newBuildServicesFixtureIn(t, svc)
	tile, local := tile00(), local00()
	// Savepoint BEFORE the build command exists at all.
	root := saveInto(t, b, "pre-build")

	id, err := b.SubmitBuildCommand(BuildCommand{
		Tile: tile, Local: local, OwnerID: testOwner,
		Zone: ZoneDwelling, Month: 6, BuildingID: clinicBuildingID,
	})
	if err != nil {
		t.Fatalf("SubmitBuildCommand: %v", err)
	}
	tickToCompletion(t, b, id, 60)
	if cs, _ := svc.CoverageSummary(); cs.ServiceCount != 1 {
		t.Fatalf("setup: %+v", cs)
	}

	// Rewind INTO the live api, services untouched.
	loadInto(t, root, b, "rewound")
	if err := b.RegisterCompletedServices(); err != nil {
		t.Fatalf("sweep after rewind: %v", err)
	}
	// No wedge: ticking must be clean now (round 1's failure).
	for i := int64(0); i < 10; i++ {
		if err := b.Tick(i); err != nil {
			t.Fatalf("REGRESSION: tick after a live rewind still fails: %v", err)
		}
	}
	cs, err := svc.CoverageSummary()
	if err != nil {
		t.Fatalf("CoverageSummary: %v", err)
	}
	if cs.ServiceCount != 0 {
		t.Logf("FOLLOW-UP (not a blocker): after rewinding a LIVE composition to a "+
			"savepoint predating the build, engine.services still holds %d orphan "+
			"instance(s) (%+v) that no restored build state accounts for. The wedge "+
			"is closed — ticking is clean and a re-completion is idempotent — but a "+
			"live-composition Load leaves phantom capacity until the same order is "+
			"rebuilt. In-tree restores use a fresh composition, so this is not "+
			"reachable today.", cs.ServiceCount, cs)
	}
	// The sweep must at least not have made it WORSE (no double instance).
	ids, _ := svc.ServiceIDs()
	if len(ids) > 1 {
		t.Fatalf("rewind + sweep produced %d instances: %v", len(ids), ids)
	}
}

// --- R2 ATTACK 7: perf — the every-tick sweep's cost ----------------------

func BenchmarkAttackRound2_TickWithLargeStandingEstate(bench *testing.B) {
	t := &testing.T{}
	svc := services.New(testCorr())
	b, _ := newBuildServicesFixtureIn(t, svc)
	// 2000 standing structures, half of them services.
	const n = 2000
	for i := 0; i < n; i++ {
		bid := shopBuildingID
		if i%2 == 0 {
			bid = clinicBuildingID
		}
		id, err := b.SubmitBuildCommand(BuildCommand{
			Tile: tile00(), Local: world.CellLocal{Row: i / 100, Col: i % 100},
			OwnerID: testOwner, Zone: ZoneDwelling, Month: 6, BuildingID: bid,
		})
		if err != nil {
			bench.Fatalf("build %d: %v", i, err)
		}
		for j := int64(0); j < 60; j++ {
			if err := b.Tick(j); err != nil {
				bench.Fatalf("tick: %v", err)
			}
			if orderByID(t, b.Queue(), id).Status == OrderComplete {
				break
			}
		}
	}
	bench.ResetTimer()
	bench.ReportAllocs()
	for i := 0; i < bench.N; i++ {
		if err := b.Tick(int64(i)); err != nil {
			bench.Fatalf("Tick: %v", err)
		}
	}
}
