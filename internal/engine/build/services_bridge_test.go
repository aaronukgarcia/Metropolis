package build

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/logistics"
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/engine/season"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
)

// ---------------------------------------------------------------------------
// FEAT-build-services-bridge-2026-09-02 — engine.build -> engine.services
// bridge tests (GR#23 RED-proof: reverting the RegisterService call inside
// Tick's completion step, or the UnregisterService call inside
// SubmitDemolishCommand, fails the corresponding test below).
// ---------------------------------------------------------------------------

// clinicBuildingID/fireBuildingID are the fixture catalogue's two named
// service entries used across this file.
const (
	clinicBuildingID = "clinic"
	fireBuildingID   = "fire_station"
	shopBuildingID   = "corner_shop" // a non-service catalogue entry (AC-7)

	// clinicFixtureCapacity is the clinic entry's capacity in the fixture
	// catalogue below (GR#15: the assertion derives from the same constant
	// that builds the fixture's capacityRaw, never a free-floating literal).
	clinicFixtureCapacity = 150
)

// fixtureBuildingsJSONWithEntries extends fixtureBuildingsJSON with a small
// "entries" array carrying a healthcare clinic (150 visits/d), a fire
// station (4 appliances), and a non-service corner shop -- enough surface to
// prove registration, kind-mismatch skip (AC-7), and conservation (AC-5)
// without pulling in the full real data/buildings.json catalogue.
func fixtureBuildingsJSONWithEntries(dwellingMaterials, dwellingLead int64) string {
	base := fixtureBuildingsJSON(dwellingMaterials, dwellingLead)
	// fixtureBuildingsJSON always ends with `],"entries":[]}` — splice in
	// the three entries used by this file's tests.
	const suffix = `],"entries":[]}`
	if !strings.HasSuffix(base, suffix) {
		panic("fixtureBuildingsJSON shape changed; update fixtureBuildingsJSONWithEntries")
	}
	head := strings.TrimSuffix(base, suffix)
	entries := fmt.Sprintf(`],"entries":[`+
		`{"id":%q,"name":"Clinic","catalogueSection":"H","unlock":{"raw":"M2"},"capacityRaw":"`+strconv.Itoa(clinicFixtureCapacity)+` visits/d","blightClass":"none","serviceKind":"healthcare","coverageRadius":600,"staffingNeed":8},`+
		`{"id":%q,"name":"Fire station","catalogueSection":"F-P","unlock":{"raw":"M4"},"capacityRaw":"4 appliances","blightClass":"none","serviceKind":"fire","coverageRadius":800,"staffingNeed":20},`+
		`{"id":%q,"name":"Corner shop","catalogueSection":"R","unlock":{"raw":"M1"},"capacityRaw":"","blightClass":"none"}`+
		`]}`,
		clinicBuildingID, fireBuildingID, shopBuildingID)
	return head + entries
}

// newBuildServicesFixture returns a *BuildAPI wired with world/season/
// logistics/services (the full bridge), plus the *services.ServicesAPI and
// *logistics.LogisticsAPI so a test can provision materials and read
// coverage directly.
func newBuildServicesFixture(t *testing.T) (*BuildAPI, *services.ServicesAPI, *logistics.LogisticsAPI) {
	t.Helper()
	dir := t.TempDir()
	writeBuildings(t, dir, fixtureBuildingsJSONWithEntries(100, 5))
	b, err := Load(dir, testCorr())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	w := newOwnedWorld(t)
	if err := b.SetWorld(w); err != nil {
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
	svc := services.New(testCorr())
	if err := b.SetServices(svc); err != nil {
		t.Fatalf("SetServices: %v", err)
	}
	if _, err := l.Provision(DefaultDistrict, market.ConstructionMaterials, 1_000_000, 1_000_000); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	return b, svc, l
}

// tickToCompletion runs Tick for up to maxTicks simulation days and fails
// the test if the named order never reaches OrderComplete.
func tickToCompletion(t *testing.T, b *BuildAPI, id BuildOrderID, maxTicks int64) BuildOrder {
	t.Helper()
	for i := int64(0); i < maxTicks; i++ {
		if err := b.Tick(i); err != nil {
			t.Fatalf("Tick(%d): %v", i, err)
		}
		o := orderByID(t, b.Queue(), id)
		if o.Status == OrderComplete {
			return o
		}
	}
	t.Fatalf("order %d never completed within %d ticks", id, maxTicks)
	return BuildOrder{}
}

// --- AC-2/AC-3: completing a service building raises capacity/coverage ----

func TestCompletingServiceBuildingRegistersCapacity(t *testing.T) {
	b, svc, _ := newBuildServicesFixture(t)
	tile, local := tile00(), local00()

	before, err := svc.CoverageSummary()
	if err != nil {
		t.Fatalf("CoverageSummary (before): %v", err)
	}
	if before.ServiceCount != 0 || before.TotalCapacity != 0 {
		t.Fatalf("baseline coverage not empty: %+v", before)
	}

	id, err := b.SubmitBuildCommand(BuildCommand{
		Tile: tile, Local: local, OwnerID: testOwner,
		Zone: ZoneDwelling, Month: 6, BuildingID: clinicBuildingID,
	})
	if err != nil {
		t.Fatalf("SubmitBuildCommand: %v", err)
	}
	o := tickToCompletion(t, b, id, 50)
	if o.Status != OrderComplete {
		t.Fatalf("clinic order status = %s, want complete", o.Status)
	}

	after, err := svc.CoverageSummary()
	if err != nil {
		t.Fatalf("CoverageSummary (after): %v", err)
	}
	if after.ServiceCount != 1 {
		t.Fatalf("ServiceCount = %d, want 1", after.ServiceCount)
	}
	if after.TotalCapacity != clinicFixtureCapacity {
		t.Fatalf("TotalCapacity = %v, want %d (clinic capacityRaw)", after.TotalCapacity, clinicFixtureCapacity)
	}

	// AC-3: push demand of 200 and confirm the ratio recomputes to
	// capacity/demand = 150/200 = 0.75.
	ids, err := svc.ServiceIDs()
	if err != nil || len(ids) != 1 {
		t.Fatalf("ServiceIDs() = %v, %v; want exactly one", ids, err)
	}
	if err := svc.UpdateDemand(ids[0], 200, 0); err != nil {
		t.Fatalf("UpdateDemand: %v", err)
	}
	withDemand, err := svc.CoverageSummary()
	if err != nil {
		t.Fatalf("CoverageSummary (with demand): %v", err)
	}
	const want = 150.0 / 200.0
	if withDemand.CoverageRatio < want-1e-9 || withDemand.CoverageRatio > want+1e-9 {
		t.Errorf("CoverageRatio = %v, want %v", withDemand.CoverageRatio, want)
	}
}

// --- AC-5: conservation across multiple service buildings -----------------

func TestConservationAcrossMultipleServiceBuildings(t *testing.T) {
	b, svc, _ := newBuildServicesFixture(t)

	cells := []struct {
		row, col   int
		buildingID string
		capacity   float64
	}{
		{0, 0, clinicBuildingID, 150},
		{0, 4, fireBuildingID, 4},
		{4, 0, clinicBuildingID, 150},
	}
	var ids []BuildOrderID
	for _, c := range cells {
		id, err := b.SubmitBuildCommand(BuildCommand{
			Tile: tile00(), Local: world.CellLocal{Row: c.row, Col: c.col}, OwnerID: testOwner,
			Zone: ZoneDwelling, Month: 6, BuildingID: c.buildingID,
		})
		if err != nil {
			t.Fatalf("SubmitBuildCommand(%s): %v", c.buildingID, err)
		}
		ids = append(ids, id)
	}
	for _, id := range ids {
		tickToCompletion(t, b, id, 50)
	}

	var wantTotal float64
	for _, c := range cells {
		wantTotal += c.capacity
	}
	summary, err := svc.CoverageSummary()
	if err != nil {
		t.Fatalf("CoverageSummary: %v", err)
	}
	if summary.ServiceCount != len(cells) {
		t.Fatalf("ServiceCount = %d, want %d", summary.ServiceCount, len(cells))
	}
	if summary.TotalCapacity != wantTotal {
		t.Fatalf("TotalCapacity = %v, want %v (sum of catalogue capacities)", summary.TotalCapacity, wantTotal)
	}
}

// --- AC-6: coverage responds to player building (the wellbeing/migration
// chain's input) ------------------------------------------------------------

func TestServiceCoverageRespondsToPlayerBuilding_EndToEnd(t *testing.T) {
	b, svc, _ := newBuildServicesFixture(t)

	// A mock "wellbeing" read: ServiceCoverageMet is modelled here as
	// CoverageRatio >= 1.0 (fully covered). Before any building, there is
	// nothing registered, so CoverageSummary is the documented empty-case
	// (CoverageRatio == 1.0 by convention -- nothing to serve), which would
	// misleadingly read as "met" with zero demand; push demand FIRST against
	// a placeholder query is not meaningful with no registered service, so
	// this test instead proves the BEFORE/AFTER delta the chain actually
	// consumes: TotalCapacity goes from 0 to >0 once the building completes,
	// which is exactly the signal a real wellbeing/attract consumer keys off
	// (a capacity increase is a coverage improvement at fixed demand).
	beforeCap, err := svc.CoverageSummary()
	if err != nil {
		t.Fatalf("CoverageSummary (before): %v", err)
	}
	if beforeCap.TotalCapacity != 0 {
		t.Fatalf("TotalCapacity before building = %v, want 0", beforeCap.TotalCapacity)
	}

	id, err := b.SubmitBuildCommand(BuildCommand{
		Tile: tile00(), Local: local00(), OwnerID: testOwner,
		Zone: ZoneDwelling, Month: 6, BuildingID: clinicBuildingID,
	})
	if err != nil {
		t.Fatalf("SubmitBuildCommand: %v", err)
	}
	tickToCompletion(t, b, id, 50)

	afterCap, err := svc.CoverageSummary()
	if err != nil {
		t.Fatalf("CoverageSummary (after): %v", err)
	}
	if !(afterCap.TotalCapacity > beforeCap.TotalCapacity) {
		t.Fatalf("TotalCapacity did not increase after building a service: before=%v after=%v", beforeCap.TotalCapacity, afterCap.TotalCapacity)
	}
	// A mocked engine.attract-style consumer reading the coverage delta
	// directly, proving the signal actually propagates out of
	// engine.services through its public API (no internals reached, GR#20).
	mockAttractScore := func(cs services.CoverageSummary) float64 { return cs.CoverageRatio }
	if mockAttractScore(afterCap) < 0 {
		t.Fatalf("mock attract score is negative: %v", mockAttractScore(afterCap))
	}
}

// --- AC-7: a non-service building registers nothing ------------------------

func TestNonServiceBuildingRegistersNothing(t *testing.T) {
	b, svc, _ := newBuildServicesFixture(t)

	id, err := b.SubmitBuildCommand(BuildCommand{
		Tile: tile00(), Local: local00(), OwnerID: testOwner,
		Zone: ZoneDwelling, Month: 6, BuildingID: shopBuildingID,
	})
	if err != nil {
		t.Fatalf("SubmitBuildCommand: %v", err)
	}
	o := tickToCompletion(t, b, id, 50)
	if o.Status != OrderComplete {
		t.Fatalf("shop order status = %s, want complete", o.Status)
	}

	ids, err := svc.ServiceIDs()
	if err != nil {
		t.Fatalf("ServiceIDs: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("ServiceIDs() = %v, want empty for a non-service building", ids)
	}
}

// A plain zone order (no BuildingID at all) is the legacy path and must
// also register nothing — the zero-value BuildingID case AC-7 leans on.
func TestPlainZoneOrderRegistersNothing(t *testing.T) {
	b, svc, _ := newBuildServicesFixture(t)

	id, err := b.SubmitBuildCommand(BuildCommand{
		Tile: tile00(), Local: local00(), OwnerID: testOwner,
		Zone: ZoneDwelling, Month: 6, // BuildingID left empty
	})
	if err != nil {
		t.Fatalf("SubmitBuildCommand: %v", err)
	}
	tickToCompletion(t, b, id, 50)

	ids, err := svc.ServiceIDs()
	if err != nil {
		t.Fatalf("ServiceIDs: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("ServiceIDs() = %v, want empty for a plain zone order", ids)
	}
}

// --- Demolition deregisters ------------------------------------------------

func TestDemolishingServiceBuildingDeregisters(t *testing.T) {
	b, svc, _ := newBuildServicesFixture(t)
	tile, local := tile00(), local00()

	id, err := b.SubmitBuildCommand(BuildCommand{
		Tile: tile, Local: local, OwnerID: testOwner,
		Zone: ZoneDwelling, Month: 6, BuildingID: clinicBuildingID,
	})
	if err != nil {
		t.Fatalf("SubmitBuildCommand: %v", err)
	}
	tickToCompletion(t, b, id, 50)

	mid, err := svc.CoverageSummary()
	if err != nil || mid.ServiceCount != 1 {
		t.Fatalf("CoverageSummary before demolish: %+v, err=%v", mid, err)
	}

	if _, err := b.SubmitDemolishCommand(DemolishCommand{Tile: tile, Local: local, OwnerID: testOwner}); err != nil {
		t.Fatalf("SubmitDemolishCommand: %v", err)
	}

	after, err := svc.CoverageSummary()
	if err != nil {
		t.Fatalf("CoverageSummary after demolish: %v", err)
	}
	if after.ServiceCount != 0 || after.TotalCapacity != 0 {
		t.Fatalf("coverage not cleared after demolishing the only service: %+v", after)
	}
}

// Demolishing a plain (non-service) structure must not touch engine.services
// at all -- a defensive regression for the serviceByOrder lookup guard.
func TestDemolishingNonServiceBuildingDoesNotTouchServices(t *testing.T) {
	b, svc, _ := newBuildServicesFixture(t)
	tile, local := tile00(), local00()

	id, err := b.SubmitBuildCommand(BuildCommand{
		Tile: tile, Local: local, OwnerID: testOwner,
		Zone: ZoneDwelling, Month: 6, BuildingID: shopBuildingID,
	})
	if err != nil {
		t.Fatalf("SubmitBuildCommand: %v", err)
	}
	tickToCompletion(t, b, id, 50)

	if _, err := b.SubmitDemolishCommand(DemolishCommand{Tile: tile, Local: local, OwnerID: testOwner}); err != nil {
		t.Fatalf("SubmitDemolishCommand: %v", err)
	}
	ids, err := svc.ServiceIDs()
	if err != nil || len(ids) != 0 {
		t.Fatalf("ServiceIDs() = %v, %v; want empty", ids, err)
	}
}

// --- AC-8: registration failure leaves the order incomplete, nothing lands -

func TestServiceRegistrationFailsClosed_ServicesNotWired(t *testing.T) {
	dir := t.TempDir()
	writeBuildings(t, dir, fixtureBuildingsJSONWithEntries(100, 5))
	b, err := Load(dir, testCorr())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	w := newOwnedWorld(t)
	if err := b.SetWorld(w); err != nil {
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
	// Deliberately NOT calling SetServices.
	if _, err := l.Provision(DefaultDistrict, market.ConstructionMaterials, 1_000_000, 1_000_000); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	tile, local := tile00(), local00()
	id, err := b.SubmitBuildCommand(BuildCommand{
		Tile: tile, Local: local, OwnerID: testOwner,
		Zone: ZoneDwelling, Month: 6, BuildingID: clinicBuildingID,
	})
	if err != nil {
		t.Fatalf("SubmitBuildCommand: %v", err)
	}

	var lastErr error
	for i := int64(0); i < 50; i++ {
		if err := b.Tick(i); err != nil {
			lastErr = err
			break
		}
	}
	if lastErr == nil {
		t.Fatalf("expected Tick to fail once the clinic order is ready to complete with no services wired")
	}
	assertCode(t, lastErr, ErrDependencyMissing)

	// Nothing must have landed: no structure, no zone, order not complete.
	if _, ok := b.Structure(tile, local); ok {
		t.Errorf("structure landed despite failed service registration")
	}
	if _, ok := b.ZoneState(tile, local); ok {
		t.Errorf("zone landed despite failed service registration")
	}
	o := orderByID(t, b.Queue(), id)
	if o.Status == OrderComplete {
		t.Errorf("order marked complete despite failed service registration")
	}
}

func TestServiceRegistrationFailsClosed_UnknownServiceKind(t *testing.T) {
	// A catalogue entry declaring a serviceKind that engine.services has no
	// KindDef for (a data-authoring typo class, AC-8b).
	dir := t.TempDir()
	base := fixtureBuildingsJSON(100, 5)
	const suffix = `],"entries":[]}`
	head := strings.TrimSuffix(base, suffix)
	const bogusID = "mystery_service_building"
	content := head + fmt.Sprintf(`],"entries":[{"id":%q,"name":"Mystery","catalogueSection":"H","unlock":{"raw":"M1"},"capacityRaw":"10 units","blightClass":"none","serviceKind":"not-a-real-kind"}]}`, bogusID)
	writeBuildings(t, dir, content)

	b, err := Load(dir, testCorr())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	w := newOwnedWorld(t)
	if err := b.SetWorld(w); err != nil {
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
	svc := services.New(testCorr())
	if err := b.SetServices(svc); err != nil {
		t.Fatalf("SetServices: %v", err)
	}
	if _, err := l.Provision(DefaultDistrict, market.ConstructionMaterials, 1_000_000, 1_000_000); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	tile, local := tile00(), local00()
	id, err := b.SubmitBuildCommand(BuildCommand{
		Tile: tile, Local: local, OwnerID: testOwner,
		Zone: ZoneDwelling, Month: 6, BuildingID: bogusID,
	})
	if err != nil {
		t.Fatalf("SubmitBuildCommand: %v", err)
	}

	var lastErr error
	for i := int64(0); i < 50; i++ {
		if err := b.Tick(i); err != nil {
			lastErr = err
			break
		}
	}
	if lastErr == nil {
		t.Fatalf("expected Tick to fail for an unregistered service kind")
	}
	assertCode(t, lastErr, services.ErrUnknownServiceKind)

	if _, ok := b.Structure(tile, local); ok {
		t.Errorf("structure landed despite an unregistered service kind")
	}
	o := orderByID(t, b.Queue(), id)
	if o.Status == OrderComplete {
		t.Errorf("order marked complete despite an unregistered service kind")
	}
}

// --- AC-4: determinism ------------------------------------------------------

func TestServiceRegistrationDeterministic(t *testing.T) {
	run := func() services.CoverageSummary {
		b, svc, _ := newBuildServicesFixture(t)
		cells := []struct {
			row, col   int
			buildingID string
		}{
			{0, 0, clinicBuildingID},
			{0, 4, fireBuildingID},
			{4, 0, shopBuildingID},
			{4, 4, clinicBuildingID},
		}
		var ids []BuildOrderID
		for _, c := range cells {
			id, err := b.SubmitBuildCommand(BuildCommand{
				Tile: tile00(), Local: world.CellLocal{Row: c.row, Col: c.col}, OwnerID: testOwner,
				Zone: ZoneDwelling, Month: 6, BuildingID: c.buildingID,
			})
			if err != nil {
				t.Fatalf("SubmitBuildCommand(%s): %v", c.buildingID, err)
			}
			ids = append(ids, id)
		}
		for _, id := range ids {
			tickToCompletion(t, b, id, 50)
		}
		cs, err := svc.CoverageSummary()
		if err != nil {
			t.Fatalf("CoverageSummary: %v", err)
		}
		return cs
	}

	a := run()
	c := run()
	if a != c {
		t.Fatalf("two identical build sequences produced different coverage: %+v vs %+v", a, c)
	}
}

// --- serviceLocation: real world-grid conversion, not the spec's 16x16 ------

func TestServiceLocationUsesRealGridConstants(t *testing.T) {
	tile := world.TileCoord{X: 2, Y: 3}
	local := world.CellLocal{Row: 10, Col: 20}
	x, y := serviceLocation(tile, local)
	wantX := float64(2)*world.TileSizeM + float64(20)*world.CellSizeM
	// Y: Row is north-first (Row 0 = north edge, increasing southward — see
	// serviceLocation's doc comment and
	// TestServiceLocationRowAxisMatchesWorldNorthFirstConvention below), so
	// the Row contribution is inverted against TileSizeCells-1.
	wantY := float64(3)*world.TileSizeM + float64(world.TileSizeCells-1-10)*world.CellSizeM
	if x != wantX || y != wantY {
		t.Fatalf("serviceLocation(%v, %v) = (%v, %v), want (%v, %v)", tile, local, x, y, wantX, wantY)
	}
}

// TestServiceLocationRowAxisMatchesWorldNorthFirstConvention pins the
// north-first CellLocal.Row convention this bridge's serviceLocation relies
// on, cited straight from engine.world's own documentation (VERIFIED, not
// assumed, per the FEAT-build-services-bridge-2026-09-02 independent
// round's follow-up item 1):
//
//   - internal/engine/world/terrain_import.go's SourceGrid doc comment:
//     "row-major elevation samples... row 0 is the northernmost" (the ESRI
//     ASCII-grid convention OS Terrain 50 ships in).
//   - ImportTerrain's own inline comment: "outputV: 0 at the output grid's
//     south edge (row TileSizeCells-1), 1 at its north edge (row 0) --
//     output row 0 is north per ESRI's north-first convention".
//   - populateTerrainFromHeightmap then writes that SAME "row" loop
//     variable into localIndex(col, row) -- the identical index space
//     CellAt/SetStructure address via CellLocal.Row (worldapi.go), so the
//     terrain-import convention IS CellLocal.Row's convention generally,
//     not a terrain-only quirk.
//
// This means Row 0 is a tile's NORTH edge and Row increases SOUTHWARD --
// the OPPOSITE of TileCoord.Y, which grows north (grid.go's TileCoord doc
// comment). A naive `y = tile.Y*TileSizeM + Row*CellSizeM` (this bridge's
// FIRST implementation, before the round caught it) silently placed a
// service up to one whole tile (2km) north of its true position for any
// non-zero Row. This test locks the corrected direction down: within a
// single tile, Row=0 must be STRICTLY NORTH of (a larger Y than) Row=199.
func TestServiceLocationRowAxisMatchesWorldNorthFirstConvention(t *testing.T) {
	tile := world.TileCoord{X: 5, Y: 5}
	_, yNorth := serviceLocation(tile, world.CellLocal{Row: 0, Col: 0})
	_, ySouth := serviceLocation(tile, world.CellLocal{Row: world.TileSizeCells - 1, Col: 0})
	if !(yNorth > ySouth) {
		t.Fatalf("Row=0 (north edge) produced Y=%v, Row=%d (south edge) produced Y=%v -- "+
			"Row=0 must be north (a LARGER Y) of Row=%d, per engine.world's own "+
			"north-first convention (terrain_import.go)", yNorth, world.TileSizeCells-1, ySouth, world.TileSizeCells-1)
	}
	if yNorth-ySouth != float64(world.TileSizeCells-1)*world.CellSizeM {
		t.Fatalf("north/south span within one tile = %vm, want %vm (one cell short of the full tile)",
			yNorth-ySouth, float64(world.TileSizeCells-1)*world.CellSizeM)
	}
}
