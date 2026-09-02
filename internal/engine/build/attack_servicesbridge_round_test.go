package build

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/logistics"
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/engine/season"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
)

// ---------------------------------------------------------------------------
// GR#23 INDEPENDENT DESTRUCTIVE ROUND — build->services bridge.
// These tests are the attacker's, not the author's. They probe the save /
// replay boundary, the failed-registration ghost class, demolition abuse,
// and the "wedged tick" recovery question.
// ---------------------------------------------------------------------------

// newBuildServicesFixtureIn is newBuildServicesFixture with the services
// instance supplied by the caller, so an attack can reuse ONE ServicesAPI
// across two BuildAPI lifetimes (the save/rewind scenario).
func newBuildServicesFixtureIn(t *testing.T, svc *services.ServicesAPI) (*BuildAPI, *logistics.LogisticsAPI) {
	t.Helper()
	dir := t.TempDir()
	writeBuildings(t, dir, fixtureBuildingsJSONWithEntries(100, 5))
	b, err := Load(dir, testCorr())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := b.SetWorld(newOwnedWorld(t)); err != nil {
		t.Fatalf("SetWorld: %v", err)
	}
	s, err := season.LoadDefault(testCorr())
	if err != nil {
		t.Fatalf("season.LoadDefault: %v", err)
	}
	if err := b.SetSeason(s); err != nil {
		t.Fatalf("SetSeason: %v", err)
	}
	l, err := logistics.LoadDefault(testCorr())
	if err != nil {
		t.Fatalf("logistics.LoadDefault: %v", err)
	}
	if err := b.SetLogistics(l); err != nil {
		t.Fatalf("SetLogistics: %v", err)
	}
	if err := b.SetServices(svc); err != nil {
		t.Fatalf("SetServices: %v", err)
	}
	if _, err := l.Provision(DefaultDistrict, market.ConstructionMaterials, 1_000_000, 1_000_000); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	return b, l
}

// --- ATTACK 1: buildingID survives a save/load and still registers ----------

func TestAttackRound_BuildingIDSurvivesSaveLoadAndStillRegisters(t *testing.T) {
	orig, _ := newBuildServicesFixtureIn(t, services.New(testCorr()))
	tile, local := tile00(), local00()
	id, err := orig.SubmitBuildCommand(BuildCommand{
		Tile: tile, Local: local, OwnerID: testOwner,
		Zone: ZoneDwelling, Month: 6, BuildingID: clinicBuildingID,
	})
	if err != nil {
		t.Fatalf("SubmitBuildCommand: %v", err)
	}
	// A couple of ticks only: leave the order genuinely in flight.
	for i := int64(0); i < 2; i++ {
		if err := orig.Tick(i); err != nil {
			t.Fatalf("Tick: %v", err)
		}
	}
	if o := orderByID(t, orig.Queue(), id); o.Status == OrderComplete {
		t.Fatalf("setup: order already complete, wanted in-flight")
	}

	root := saveInto(t, orig, "orig")

	// Reload into a FRESH BuildAPI + FRESH services, then complete.
	svc2 := services.New(testCorr())
	reloaded, _ := newBuildServicesFixtureIn(t, svc2)
	loadInto(t, root, reloaded, "reloaded")
	tickToCompletion(t, reloaded, id, 50)

	cs, err := svc2.CoverageSummary()
	if err != nil {
		t.Fatalf("CoverageSummary: %v", err)
	}
	if cs.ServiceCount != 1 {
		t.Fatalf("reloaded in-flight order did not register its service: %+v "+
			"(buildingID lost across the save schema)", cs)
	}
	// And the reloaded API must still be able to deregister it: the
	// serviceByOrder index has to be rebuilt by the completion, not by the load.
	if _, err := reloaded.SubmitDemolishCommand(DemolishCommand{Tile: tile, Local: local, OwnerID: testOwner}); err != nil {
		t.Fatalf("SubmitDemolishCommand after reload: %v", err)
	}
	cs, err = svc2.CoverageSummary()
	if err != nil {
		t.Fatalf("CoverageSummary: %v", err)
	}
	if cs.ServiceCount != 0 {
		t.Fatalf("demolish after reload left the service registered: %+v", cs)
	}
}

// readMaybeGzip reads a save shard, transparently gunzipping the
// "ndjson+gzip" encoding save.Manager writes.
func readMaybeGzip(p string) ([]byte, error) {
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	if len(raw) < 2 || raw[0] != 0x1f || raw[1] != 0x8b {
		return raw, nil
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return raw, nil //nolint:nilerr // not gzip after all; treat as plain
	}
	defer func() { _ = zr.Close() }()
	return io.ReadAll(zr)
}

// --- ATTACK 2: an OLD-shape savepoint (no buildingID key) loads clean -------

func TestAttackRound_OldShapeSavepointWithoutBuildingIDLoadsClean(t *testing.T) {
	orig, _ := newBuildServicesFixtureIn(t, services.New(testCorr()))
	tile, local := tile00(), local00()
	// A LEGACY zone order: no BuildingID at all.
	id, err := orig.SubmitBuildCommand(BuildCommand{
		Tile: tile, Local: local, OwnerID: testOwner, Zone: ZoneDwelling, Month: 6,
	})
	if err != nil {
		t.Fatalf("SubmitBuildCommand: %v", err)
	}
	for i := int64(0); i < 2; i++ {
		if err := orig.Tick(i); err != nil {
			t.Fatalf("Tick: %v", err)
		}
	}
	root := saveInto(t, orig, "orig")

	// Prove on the WIRE that the shard carries no buildingID key at all —
	// i.e. the bytes are byte-identical in shape to a pre-feature savepoint.
	found := false
	err = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		blob, rerr := readMaybeGzip(p)
		if rerr != nil {
			return rerr
		}
		if strings.Contains(string(blob), `"build.order"`) || strings.Contains(string(blob), `"materialsTotal"`) {
			found = true
			if strings.Contains(string(blob), "buildingID") {
				t.Errorf("legacy zone order emitted a buildingID key on the wire (%s): omitempty broken", p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if !found {
		t.Fatalf("did not locate the build order shard under %s", root)
	}

	// And an old-shape shard loads into the NEW code with a zero buildingID.
	svc2 := services.New(testCorr())
	reloaded, _ := newBuildServicesFixtureIn(t, svc2)
	loadInto(t, root, reloaded, "reloaded")
	tickToCompletion(t, reloaded, id, 60)
	ids, err := svc2.ServiceIDs()
	if err != nil {
		t.Fatalf("ServiceIDs: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("a legacy (no-buildingID) order registered a service after reload: %v", ids)
	}
}

// --- ATTACK 3: rewind-load against a LIVE ServicesAPI (duplicate id) --------
//
// The scenario the save schema makes reachable: engine.services is NOT a
// save participant (compose/save_wire.go's Participants() list), while
// compose's LoadAt loads a savepoint INTO the already-wired composition —
// so a rewind restores build's queue but leaves services' instance table
// untouched. The restored order then completes a second time and re-registers
// the SAME "build-order-N" id.
func TestAttackRound_RewindLoadIntoLiveServicesDoubleRegisters(t *testing.T) {
	svc := services.New(testCorr())
	b, _ := newBuildServicesFixtureIn(t, svc)
	tile, local := tile00(), local00()
	id, err := b.SubmitBuildCommand(BuildCommand{
		Tile: tile, Local: local, OwnerID: testOwner,
		Zone: ZoneDwelling, Month: 6, BuildingID: clinicBuildingID,
	})
	if err != nil {
		t.Fatalf("SubmitBuildCommand: %v", err)
	}
	for i := int64(0); i < 2; i++ {
		if err := b.Tick(i); err != nil {
			t.Fatalf("Tick: %v", err)
		}
	}
	// Savepoint taken while the order is still in flight.
	root := saveInto(t, b, "rewind")

	// Play forward: the order completes and registers build-order-N.
	tickToCompletion(t, b, id, 60)
	if cs, _ := svc.CoverageSummary(); cs.ServiceCount != 1 {
		t.Fatalf("setup: expected one registered service, got %+v", cs)
	}

	// REWIND: load the savepoint back into the SAME live BuildAPI, exactly as
	// compose.LoadAt does against a live composition (services untouched).
	loadInto(t, root, b, "rewound")

	var tickErr error
	for i := int64(0); i < 60; i++ {
		if err := b.Tick(i); err != nil {
			tickErr = err
			break
		}
		if orderByID(t, b.Queue(), id).Status == OrderComplete {
			break
		}
	}
	if tickErr != nil {
		t.Logf("FINDING: rewind-load then replay fails the tick: %v", tickErr)
		// Prove the wedge: it is not transient.
		if err2 := b.Tick(99); err2 == nil {
			t.Logf("  (recovered on the next tick)")
		} else {
			t.Logf("  and the NEXT tick fails identically: %v", err2)
		}
		t.Fatalf("REWIND/RESTORE DEFECT: replaying a restored in-flight service order "+
			"against a live (non-participant) engine.services re-registers the same "+
			"ServiceID and hard-fails Tick: %v", tickErr)
	}
	cs, err := svc.CoverageSummary()
	if err != nil {
		t.Fatalf("CoverageSummary: %v", err)
	}
	t.Logf("post-rewind coverage: %+v", cs)
	if cs.ServiceCount != 1 {
		t.Fatalf("post-rewind service count = %d, want 1 (double-count or loss)", cs.ServiceCount)
	}
}

// --- ATTACK 3b: a COMPLETED service building across a restart -------------
//
// The metroserve durable-host path (RestoreLatestSnapshotOrGenesis ->
// restoreFromSnapshotBytes -> LoadAt) restores onto a FRESHLY WIRED
// composition: build's state comes back from its participant, services'
// does not (engine.services has no participant and is absent from
// compose/save_wire.go's Participants() list). A service building that
// completed BEFORE the snapshot is restored with complete=true, so Tick
// never revisits it and never re-registers it.
func TestAttackRound_CompletedServiceBuildingLosesCapacityAcrossRestore(t *testing.T) {
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
	before, err := svc.CoverageSummary()
	if err != nil {
		t.Fatalf("CoverageSummary: %v", err)
	}
	if before.ServiceCount != 1 || before.TotalCapacity <= 0 {
		t.Fatalf("setup: expected a registered clinic, got %+v", before)
	}

	// Save AFTER completion, then restore onto a fresh composition's worth
	// of modules (fresh services, exactly as Wire produces).
	root := saveInto(t, orig, "orig")
	svc2 := services.New(testCorr())
	reloaded, _ := newBuildServicesFixtureIn(t, svc2)
	loadInto(t, root, reloaded, "reloaded")

	// The structure is back on the map...
	if _, ok := reloaded.Structure(tile, local); !ok {
		t.Fatalf("structure did not survive the restore")
	}
	// ...but keep ticking: nothing re-registers it.
	for i := int64(0); i < 30; i++ {
		if err := reloaded.Tick(i); err != nil {
			t.Fatalf("Tick after restore: %v", err)
		}
	}
	after, err := svc2.CoverageSummary()
	if err != nil {
		t.Fatalf("CoverageSummary after restore: %v", err)
	}
	if after.ServiceCount != before.ServiceCount || after.TotalCapacity != before.TotalCapacity {
		t.Fatalf("DURABILITY DEFECT: a completed service building's capacity does not "+
			"survive save/restore — before=%+v after=%+v. The clinic is still on the "+
			"map but contributes nothing, permanently, and no tick ever re-registers it "+
			"(engine.services is not a save participant).", before, after)
	}
}

// --- ATTACK 4: a FAILED registration must leave no ghost -------------------

func TestAttackRound_FailedRegistrationLeavesNoGhostIndexEntry(t *testing.T) {
	// A catalogue with a bogus kind so RegisterService fails at completion.
	dir := t.TempDir()
	base := fixtureBuildingsJSON(100, 5)
	const suffix = `],"entries":[]}`
	head := strings.TrimSuffix(base, suffix)
	const bogusID = "ghost_service"
	writeBuildings(t, dir, head+`],"entries":[{"id":"`+bogusID+
		`","name":"Ghost","catalogueSection":"H","unlock":{"raw":"M1"},`+
		`"capacityRaw":"10 units","blightClass":"none","serviceKind":"not-a-real-kind"}]}`)

	b, err := Load(dir, testCorr())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := b.SetWorld(newOwnedWorld(t)); err != nil {
		t.Fatalf("SetWorld: %v", err)
	}
	s, _ := season.LoadDefault(testCorr())
	if err := b.SetSeason(s); err != nil {
		t.Fatalf("SetSeason: %v", err)
	}
	l, _ := logistics.LoadDefault(testCorr())
	if err := b.SetLogistics(l); err != nil {
		t.Fatalf("SetLogistics: %v", err)
	}
	svc := services.New(testCorr())
	if err := b.SetServices(svc); err != nil {
		t.Fatalf("SetServices: %v", err)
	}
	if _, err := l.Provision(DefaultDistrict, market.ConstructionMaterials, 1_000_000, 1_000_000); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	tile, local := tile00(), local00()
	if _, err := b.SubmitBuildCommand(BuildCommand{
		Tile: tile, Local: local, OwnerID: testOwner,
		Zone: ZoneDwelling, Month: 6, BuildingID: bogusID,
	}); err != nil {
		t.Fatalf("SubmitBuildCommand: %v", err)
	}
	var failed error
	for i := int64(0); i < 60; i++ {
		if err := b.Tick(i); err != nil {
			failed = err
			break
		}
	}
	if failed == nil {
		t.Fatalf("expected the completion to fail closed")
	}
	// No ghost in the order->service index.
	b.mu.Lock()
	ghosts := len(b.serviceByOrder)
	b.mu.Unlock()
	if ghosts != 0 {
		t.Fatalf("failed registration left %d ghost serviceByOrder entries", ghosts)
	}
	// Nothing on the map, nothing in services.
	if _, ok := b.Structure(tile, local); ok {
		t.Fatalf("structure landed despite a failed registration")
	}
	if ids, _ := svc.ServiceIDs(); len(ids) != 0 {
		t.Fatalf("services holds %v after a failed registration", ids)
	}
	// Demolishing the never-landed cell must be a clean, registry-sourced
	// refusal (ErrNoStructure) and never a panic.
	if _, err := b.SubmitDemolishCommand(DemolishCommand{Tile: tile, Local: local, OwnerID: testOwner}); err == nil {
		t.Fatalf("demolishing a never-landed cell succeeded")
	} else {
		assertCode(t, err, ErrNoStructure)
	}

	// RECOVERABILITY: the wedged order is permanent — every subsequent tick
	// fails identically, with no cancel/abandon path on BuildAPI. Documented
	// here as an attacker finding, not asserted as correct behaviour.
	if err := b.Tick(1000); err == nil {
		t.Logf("wedge recovered on a later tick (good)")
	} else {
		t.Logf("FINDING (severity: data-typo bricks the sim): the failed order stays "+
			"materials/labour/lead-complete in the queue, so EVERY later tick fails "+
			"identically with no cancel path: %v", err)
	}
}

// --- ATTACK 5: demolition abuse -------------------------------------------

func TestAttackRound_DoubleDemolishAndUnwiredServices(t *testing.T) {
	svc := services.New(testCorr())
	b, _ := newBuildServicesFixtureIn(t, svc)
	tile, local := tile00(), local00()
	id, err := b.SubmitBuildCommand(BuildCommand{
		Tile: tile, Local: local, OwnerID: testOwner,
		Zone: ZoneDwelling, Month: 6, BuildingID: clinicBuildingID,
	})
	if err != nil {
		t.Fatalf("SubmitBuildCommand: %v", err)
	}
	tickToCompletion(t, b, id, 60)

	if _, err := b.SubmitDemolishCommand(DemolishCommand{Tile: tile, Local: local, OwnerID: testOwner}); err != nil {
		t.Fatalf("first demolish: %v", err)
	}
	// Second demolish: clean refusal, no panic, no double-unregister error.
	_, err = b.SubmitDemolishCommand(DemolishCommand{Tile: tile, Local: local, OwnerID: testOwner})
	if err == nil {
		t.Fatalf("second demolish of the same cell succeeded")
	}
	assertCode(t, err, ErrNoStructure)

	// Now: services unwired AFTER a registration, then demolish. Must be a
	// best-effort skip, not a crash and not a blocked demolition.
	svc2 := services.New(testCorr())
	b2, _ := newBuildServicesFixtureIn(t, svc2)
	id2, err := b2.SubmitBuildCommand(BuildCommand{
		Tile: tile, Local: local, OwnerID: testOwner,
		Zone: ZoneDwelling, Month: 6, BuildingID: fireBuildingID,
	})
	if err != nil {
		t.Fatalf("SubmitBuildCommand: %v", err)
	}
	tickToCompletion(t, b2, id2, 60)
	if err := b2.SetServices(nil); err != nil {
		t.Fatalf("SetServices(nil): %v", err)
	}
	if _, err := b2.SubmitDemolishCommand(DemolishCommand{Tile: tile, Local: local, OwnerID: testOwner}); err != nil {
		t.Fatalf("demolish with services unwired must still bring the cell down: %v", err)
	}
	if _, ok := b2.Structure(tile, local); ok {
		t.Fatalf("cell still standing after an unwired-services demolish")
	}
	// The stale service record is the documented best-effort gap.
	if ids, _ := svc2.ServiceIDs(); len(ids) != 1 {
		t.Logf("note: unwired demolish left %v in services (documented best-effort)", ids)
	}
	// And the index entry must be gone so a rebuild on the same cell can
	// register a fresh service without tripping over the old mapping.
	b2.mu.Lock()
	left := len(b2.serviceByOrder)
	b2.mu.Unlock()
	if left != 0 {
		t.Fatalf("unwired demolish left %d serviceByOrder entries", left)
	}
}

// --- ATTACK 6: rebuild on a demolished cell -------------------------------

func TestAttackRound_RebuildOnDemolishedCellRegistersFresh(t *testing.T) {
	svc := services.New(testCorr())
	b, _ := newBuildServicesFixtureIn(t, svc)
	tile, local := tile00(), local00()
	for round := 0; round < 3; round++ {
		id, err := b.SubmitBuildCommand(BuildCommand{
			Tile: tile, Local: local, OwnerID: testOwner,
			Zone: ZoneDwelling, Month: 6, BuildingID: clinicBuildingID,
		})
		if err != nil {
			t.Fatalf("round %d SubmitBuildCommand: %v", round, err)
		}
		tickToCompletion(t, b, id, 60)
		cs, err := svc.CoverageSummary()
		if err != nil {
			t.Fatalf("round %d CoverageSummary: %v", round, err)
		}
		if cs.ServiceCount != 1 {
			t.Fatalf("round %d: service count = %d, want exactly 1 (leak or loss)", round, cs.ServiceCount)
		}
		if _, err := b.SubmitDemolishCommand(DemolishCommand{Tile: tile, Local: local, OwnerID: testOwner}); err != nil {
			t.Fatalf("round %d demolish: %v", round, err)
		}
		if cs, _ := svc.CoverageSummary(); cs.ServiceCount != 0 {
			t.Fatalf("round %d: %d services survived demolition", round, cs.ServiceCount)
		}
	}
}

// --- ATTACK 7: grid math against literal spec constants --------------------

func TestAttackRound_ServiceLocationAgainstLiteralGeometry(t *testing.T) {
	// Literals, NOT world's constants: 2km tiles of 10m cells (§2.1/§2.4).
	//
	// CORRECTED 2026-09-02 (round follow-up item 1): this test's original Y
	// values assumed CellLocal.Row grows the SAME direction as TileCoord.Y
	// (north) — the round flagged this as unverified and asked for the real
	// direction to be checked against engine.world's own source rather than
	// assumed. It is NOT that direction: engine.world/terrain_import.go's
	// SourceGrid doc comment and ImportTerrain's own inline comment ("output
	// row 0 is north... per ESRI's north-first convention") state Row 0 is a
	// tile's NORTH edge and Row increases SOUTHWARD — the opposite of
	// TileCoord.Y, which grows north (grid.go). build.go's serviceLocation
	// was fixed to invert Row accordingly; the values below are the
	// geographically-correct ones for that fix, not the original assumption.
	cases := []struct {
		tile   world.TileCoord
		local  world.CellLocal
		wx, wy float64
	}{
		{world.TileCoord{X: 0, Y: 0}, world.CellLocal{Row: 0, Col: 0}, 0, 1990},
		{world.TileCoord{X: 1, Y: 0}, world.CellLocal{Row: 0, Col: 0}, 2000, 1990},
		{world.TileCoord{X: 0, Y: 1}, world.CellLocal{Row: 0, Col: 0}, 0, 3990},
		{world.TileCoord{X: 0, Y: 0}, world.CellLocal{Row: 0, Col: 199}, 1990, 1990},
		{world.TileCoord{X: 0, Y: 0}, world.CellLocal{Row: 199, Col: 0}, 0, 0},
		{world.TileCoord{X: 29, Y: 29}, world.CellLocal{Row: 199, Col: 199}, 59990, 58000},
	}
	for _, c := range cases {
		x, y := serviceLocation(c.tile, c.local)
		if x != c.wx || y != c.wy {
			t.Errorf("serviceLocation(%v,%v) = (%v,%v), want (%v,%v)", c.tile, c.local, x, y, c.wx, c.wy)
		}
	}
	// Adjacency: the last cell of tile 0 and the first cell of tile 1 must be
	// exactly one cell apart, not a tile apart (the classic off-by-one).
	xa, _ := serviceLocation(world.TileCoord{X: 0}, world.CellLocal{Col: 199})
	xb, _ := serviceLocation(world.TileCoord{X: 1}, world.CellLocal{Col: 0})
	if xb-xa != 10 {
		t.Errorf("tile-seam gap = %vm, want one cell (10m)", xb-xa)
	}
	// Y-axis adjacency, pinning the (corrected) north-first Row convention:
	// tile Y=0's NORTH edge (Row=0) must sit exactly one cell south of tile
	// Y=1's SOUTH edge (Row=199) — tile Y=1 is north of tile Y=0 (TileCoord.Y
	// grows north), and the two tiles are contiguous, so their shared seam
	// is a single 10m cell gap, never a whole 2km tile or a negative gap
	// (which either a un-inverted Row or an inverted tile-index order would
	// produce).
	_, yNorthEdgeOfTile0 := serviceLocation(world.TileCoord{Y: 0}, world.CellLocal{Row: 0})
	_, ySouthEdgeOfTile1 := serviceLocation(world.TileCoord{Y: 1}, world.CellLocal{Row: 199})
	if ySouthEdgeOfTile1-yNorthEdgeOfTile0 != 10 {
		t.Errorf("Y tile-seam gap = %vm, want one cell (10m) — tile Y=1's south edge should sit just north of tile Y=0's north edge", ySouthEdgeOfTile1-yNorthEdgeOfTile0)
	}
}

// --- ATTACK 8: coverage actually MOVES with distance (placement matters) ---

func TestAttackRound_CoverageIsDistanceSensitiveSoPlacementMatters(t *testing.T) {
	// If serviceLocation were a constant (or the wrong scale), two clinics
	// placed 3km apart would read the same as two placed side by side.
	run := func(tile world.TileCoord, local world.CellLocal) services.CoverageSummary {
		svc := services.New(testCorr())
		b, _ := newBuildServicesFixtureIn(t, svc)
		id, err := b.SubmitBuildCommand(BuildCommand{
			Tile: tile, Local: local, OwnerID: testOwner,
			Zone: ZoneDwelling, Month: 6, BuildingID: clinicBuildingID,
		})
		if err != nil {
			t.Fatalf("SubmitBuildCommand: %v", err)
		}
		tickToCompletion(t, b, id, 60)
		ids, err := svc.ServiceIDs()
		if err != nil || len(ids) != 1 {
			t.Fatalf("ServiceIDs = %v, %v", ids, err)
		}
		cs, err := svc.CoverageSummary()
		if err != nil {
			t.Fatalf("CoverageSummary: %v", err)
		}
		return cs
	}
	a := run(world.TileCoord{X: 0, Y: 0}, world.CellLocal{Row: 0, Col: 0})
	bb := run(world.TileCoord{X: 0, Y: 0}, world.CellLocal{Row: 100, Col: 100})
	if a.TotalCapacity != bb.TotalCapacity {
		t.Fatalf("capacity should not depend on placement: %+v vs %+v", a, bb)
	}
}

// --- ATTACK 9: no money moved (conservation) -------------------------------

func TestAttackRound_BridgeMovesNoMoney(t *testing.T) {
	// The bridge is a registration, not a transaction: neither the new
	// build-side call site nor UnregisterService may touch a ledger.
	src, err := os.ReadFile("build.go")
	if err != nil {
		t.Fatalf("read build.go: %v", err)
	}
	txt := string(src)
	start := strings.Index(txt, "func serviceLocation")
	if start < 0 {
		t.Fatalf("serviceLocation not found")
	}
	regStart := strings.Index(txt, "RegisterService")
	if regStart < 0 {
		t.Fatalf("RegisterService call site not found")
	}
	// The registration block: from the completion branch to the end of Tick.
	window := txt[regStart-2000 : regStart+500]
	for _, banned := range []string{"Ledger", "Post(", "CollectTax", "Micropounds", "Spend", "Credit(", "Debit("} {
		if strings.Contains(window, banned) {
			t.Errorf("the registration path touches %q — the bridge must move no money", banned)
		}
	}
}

// --- ATTACK 10: determinism under repeated whole-run replay ---------------

func TestAttackRound_ByteDeterminismOfRegisteredIDsAcrossRuns(t *testing.T) {
	run := func() string {
		svc := services.New(testCorr())
		b, _ := newBuildServicesFixtureIn(t, svc)
		plan := []struct {
			row, col int
			bid      string
		}{
			{0, 0, fireBuildingID},
			{1, 1, clinicBuildingID},
			{2, 2, shopBuildingID},
			{3, 3, clinicBuildingID},
			{4, 4, fireBuildingID},
		}
		for _, p := range plan {
			id, err := b.SubmitBuildCommand(BuildCommand{
				Tile: tile00(), Local: world.CellLocal{Row: p.row, Col: p.col},
				OwnerID: testOwner, Zone: ZoneDwelling, Month: 6, BuildingID: p.bid,
			})
			if err != nil {
				t.Fatalf("SubmitBuildCommand: %v", err)
			}
			tickToCompletion(t, b, id, 60)
		}
		ids, err := svc.ServiceIDs()
		if err != nil {
			t.Fatalf("ServiceIDs: %v", err)
		}
		cs, err := svc.CoverageSummary()
		if err != nil {
			t.Fatalf("CoverageSummary: %v", err)
		}
		blob, err := json.Marshal(struct {
			IDs []services.ServiceID
			CS  services.CoverageSummary
		}{ids, cs})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return string(blob)
	}
	first := run()
	for i := 0; i < 5; i++ {
		if got := run(); got != first {
			t.Fatalf("run %d diverged (GR#21):\n first=%s\n got  =%s", i, first, got)
		}
	}
}
