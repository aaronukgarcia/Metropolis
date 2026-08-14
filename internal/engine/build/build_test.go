package build

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/logistics"
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/engine/season"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

const testOwner uint32 = 1

func testCorr() string { return errs.NewCorrelationID() }

func tile00() world.TileCoord  { return world.TileCoord{X: 0, Y: 0} }
func local00() world.CellLocal { return world.CellLocal{Row: 0, Col: 0} }

// writeBuildings writes a synthetic buildings.json into dir so Load(dir)
// succeeds against a deterministic fixture rather than the repo's real data.
func writeBuildings(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, data.FileBuildings), []byte(content), 0o644); err != nil {
		t.Fatalf("write buildings.json fixture: %v", err)
	}
}

// fixtureBuildingsJSON builds a schema-valid buildings.json carrying the
// eight §34 zone types with the given dwelling-zone materials bill and
// base lead time, so a test can drive exact numbers (AC-8).
func fixtureBuildingsJSON(dwellingMaterials, dwellingLead int64) string {
	type z struct {
		id, name       string
		mat, lab, lead int64
	}
	zones := []z{
		{"dwelling", "Dwelling", dwellingMaterials, 40, dwellingLead},
		{"shop", "Shop", 80, 30, 30},
		{"office", "Office", 150, 50, 60},
		{"entertainment", "Entertainment", 200, 60, 75},
		{"farming", "Farming", 60, 20, 20},
		{"manufacturing", "Manufacturing", 250, 80, 90},
		{"heavy_industry", "Heavy Industry", 400, 120, 150},
		{"mining", "Mining", 300, 100, 120},
	}
	var sb strings.Builder
	sb.WriteString(`{"version":1,"meta":{"labourPerTick":1},"zones":[`)
	for i, z := range zones {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `{"id":%q,"name":%q,"materialsBill":{"constructionMaterials":%d},"labour":%d,"baseLeadTimeDays":%d}`,
			z.id, z.name, z.mat, z.lab, z.lead)
	}
	sb.WriteString(`],"entries":[]}`)
	return sb.String()
}

// newOwnedWorld builds a *world.WorldAPI whose tile (0,0) is purchased by
// testOwner, so commands against cells in that tile pass the ownership gate.
func newOwnedWorld(t *testing.T) *world.WorldAPI {
	t.Helper()
	w := world.NewWorldAPI(world.TileCoord{X: 0, Y: 0})
	res := w.PurchaseTile(world.PurchaseCommand{
		CorrelationID: testCorr(),
		Tile:          world.TileCoord{X: 0, Y: 0},
		BuyerID:       testOwner,
	})
	if !res.Accepted {
		t.Fatalf("PurchaseTile: %v", res.Error)
	}
	return w
}

// newBuildFixture returns a fully-wired *BuildAPI against a fixture
// buildings.json, an owned world, the real engine.season, and the real
// engine.logistics — everything a functional test needs.
func newBuildFixture(t *testing.T) (*BuildAPI, *world.WorldAPI, *logistics.LogisticsAPI) {
	t.Helper()
	dir := t.TempDir()
	writeBuildings(t, dir, fixtureBuildingsJSON(100, 45))
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
	return b, w, l
}

// loadCatalogueOnly loads a BuildAPI with no wired dependencies — enough
// for catalogue/demand queries that never touch world/season/logistics.
func loadCatalogueOnly(t *testing.T) *BuildAPI {
	t.Helper()
	dir := t.TempDir()
	writeBuildings(t, dir, fixtureBuildingsJSON(100, 45))
	b, err := Load(dir, testCorr())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return b
}

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
		t.Errorf("error code = %s, want %s (err: %v)", e.Code, wantCode, err)
	}
}

func orderByID(t *testing.T, orders []BuildOrder, id BuildOrderID) BuildOrder {
	t.Helper()
	for _, o := range orders {
		if o.ID == id {
			return o
		}
	}
	t.Fatalf("build order %d not found in queue", id)
	return BuildOrder{}
}

func zoneInfoFor(t *testing.T, b *BuildAPI, zt ZoneType) ZoneInfo {
	t.Helper()
	for _, zi := range b.ZoneCatalogue() {
		if zi.Zone == zt {
			return zi
		}
	}
	t.Fatalf("zone %q not in catalogue", zt)
	return ZoneInfo{}
}

// --- AC-2: eight zone types, each resolvable by name ---------------------

func TestZoneCatalogueHasExactlyEightTypes(t *testing.T) {
	b := loadCatalogueOnly(t)
	types := b.ZoneTypes()
	if len(types) != 8 {
		t.Fatalf("ZoneTypes() returned %d types, want exactly 8: %v", len(types), types)
	}
	want := []ZoneType{
		ZoneDwelling, ZoneShop, ZoneOffice, ZoneEntertainment,
		ZoneFarming, ZoneManufacturing, ZoneHeavyIndustry, ZoneMining,
	}
	for _, w := range want {
		if _, ok := b.ZoneTypeByID(string(w)); !ok {
			t.Errorf("zone type %q not resolvable by name", w)
		}
	}
	if len(b.ZoneCatalogue()) != 8 {
		t.Errorf("ZoneCatalogue() returned %d records, want 8", len(b.ZoneCatalogue()))
	}
}

// --- AC-3: ownership gate, negative state change -------------------------

func TestOwnershipGateRejectsUnownedCellNoStateChange(t *testing.T) {
	dir := t.TempDir()
	writeBuildings(t, dir, fixtureBuildingsJSON(100, 45))
	b, err := Load(dir, testCorr())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// World NOT purchased — tile (0,0) is unowned.
	w := world.NewWorldAPI(world.TileCoord{X: 0, Y: 0})
	if err := b.SetWorld(w); err != nil {
		t.Fatalf("SetWorld: %v", err)
	}

	tile := world.TileCoord{X: 0, Y: 0}
	local := world.CellLocal{Row: 0, Col: 0}
	before := b.Queue()

	if err := b.SubmitZoneCommand(ZoneCommand{Tile: tile, Local: local, OwnerID: testOwner, Zone: ZoneDwelling}); err == nil {
		t.Fatal("expected SubmitZoneCommand against an unowned cell to fail")
	} else {
		assertCode(t, err, ErrCellNotOwned)
	}

	// Negative state-change assertion: zone/queue state byte-identical.
	after := b.Queue()
	if !reflect.DeepEqual(before, after) {
		t.Errorf("queue changed on rejected command: before=%v after=%v", before, after)
	}
	if zt, ok := b.ZoneState(tile, local); ok {
		t.Errorf("zone state written on rejected command: %v", zt)
	}
}

func TestOwnershipGateRejectsCellOwnedByAnother(t *testing.T) {
	b, w, _ := newBuildFixture(t)
	// Purchase tile (1,0) by a different owner, then command it as testOwner.
	other := uint32(2)
	res := w.PurchaseTile(world.PurchaseCommand{
		CorrelationID: testCorr(), Tile: world.TileCoord{X: 1, Y: 0}, BuyerID: other,
	})
	if !res.Accepted {
		t.Fatalf("PurchaseTile: %v", res.Error)
	}
	tile := world.TileCoord{X: 1, Y: 0}
	local := world.CellLocal{Row: 0, Col: 0}
	err := b.SubmitZoneCommand(ZoneCommand{Tile: tile, Local: local, OwnerID: testOwner, Zone: ZoneShop})
	assertCode(t, err, ErrCellNotOwned)
	if zt, ok := b.ZoneState(tile, local); ok {
		t.Errorf("zone state written for a cell owned by another: %v", zt)
	}
}

func TestOwnershipGateAllowsOwnedCell(t *testing.T) {
	b, _, _ := newBuildFixture(t)
	tile := world.TileCoord{X: 0, Y: 0}
	local := world.CellLocal{Row: 0, Col: 0}
	if err := b.SubmitZoneCommand(ZoneCommand{Tile: tile, Local: local, OwnerID: testOwner, Zone: ZoneDwelling}); err != nil {
		t.Fatalf("SubmitZoneCommand on owned cell: %v", err)
	}
	if zt, ok := b.ZoneState(tile, local); !ok || zt != ZoneDwelling {
		t.Errorf("ZoneState = %v, %v; want dwelling, true", zt, ok)
	}
}

// --- AC-4: build queue, starved materials blocks completion --------------

func TestBuildQueueStarvedMaterialsStaysPending(t *testing.T) {
	b, _, l := newBuildFixture(t)
	// Provision an empty construction-materials shelf: every draw is a full
	// shortfall, so the order can never draw its bill.
	if _, err := l.Provision(DefaultDistrict, market.ConstructionMaterials, 1000, 0); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	tile := world.TileCoord{X: 0, Y: 0}
	local := world.CellLocal{Row: 0, Col: 0}
	id, err := b.SubmitBuildCommand(BuildCommand{Tile: tile, Local: local, OwnerID: testOwner, Zone: ZoneDwelling, Month: 6})
	if err != nil {
		t.Fatalf("SubmitBuildCommand: %v", err)
	}

	// Tick far past the dwelling zone's 45-day base lead time.
	for i := int64(0); i < 200; i++ {
		if err := b.Tick(i); err != nil {
			t.Fatalf("Tick(%d): %v", i, err)
		}
	}

	o := orderByID(t, b.Queue(), id)
	if o.Status != OrderPendingMaterials {
		t.Errorf("starved order status = %s, want materials-pending", o.Status)
	}
	if o.MaterialsRemaining <= 0 {
		t.Errorf("starved order MaterialsRemaining = %d, want > 0", o.MaterialsRemaining)
	}
	if o.MaterialsDrawn != 0 {
		t.Errorf("starved order drew %d materials, want 0", o.MaterialsDrawn)
	}
	// The cell must not have gained a structure.
	if _, ok := b.Structure(tile, local); ok {
		t.Errorf("starved order still landed a structure")
	}
}

func TestBuildQueueCompletesWhenAllThreeMet(t *testing.T) {
	b, _, l := newBuildFixture(t)
	if _, err := l.Provision(DefaultDistrict, market.ConstructionMaterials, 100000, 100000); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	tile := world.TileCoord{X: 0, Y: 0}
	local := world.CellLocal{Row: 0, Col: 0}
	id, err := b.SubmitBuildCommand(BuildCommand{Tile: tile, Local: local, OwnerID: testOwner, Zone: ZoneDwelling, Month: 6})
	if err != nil {
		t.Fatalf("SubmitBuildCommand: %v", err)
	}
	for i := int64(0); i < 200; i++ {
		if err := b.Tick(i); err != nil {
			t.Fatalf("Tick(%d): %v", i, err)
		}
	}
	o := orderByID(t, b.Queue(), id)
	if o.Status != OrderComplete {
		t.Errorf("order status = %s, want complete (materials %d, labour %d, lead %d)",
			o.Status, o.MaterialsRemaining, o.LabourRemaining, o.LeadTimeRemaining)
	}
	if _, ok := b.Structure(tile, local); !ok {
		t.Errorf("completed order did not land a structure")
	}
	if zt, ok := b.ZoneState(tile, local); !ok || zt != ZoneDwelling {
		t.Errorf("completed order did not land its zone: %v, %v", zt, ok)
	}
}

// --- AC-5: self-explaining demand bars -----------------------------------

func TestDemandBarCarriesReasonCodes(t *testing.T) {
	b := loadCatalogueOnly(t)

	if err := b.ReportDemand(ZoneOffice, DemandInput{Unfilled: 5, LabourStarved: true}); err != nil {
		t.Fatalf("ReportDemand: %v", err)
	}
	bar, err := b.Demand(ZoneOffice)
	if err != nil {
		t.Fatalf("Demand: %v", err)
	}
	if bar.Unfilled != 5 {
		t.Errorf("Demand.Unfilled = %d, want 5", bar.Unfilled)
	}
	if !containsReason(bar.Reasons, ReasonNoLabour) {
		t.Errorf("Demand.Reasons = %v, want to include no-labour", bar.Reasons)
	}

	// Multiple starved inputs → ordered, deterministic reason codes.
	if err := b.ReportDemand(ZoneShop, DemandInput{PowerStarved: true, FreightStarved: true}); err != nil {
		t.Fatalf("ReportDemand: %v", err)
	}
	bar2, err := b.Demand(ZoneShop)
	if err != nil {
		t.Fatalf("Demand: %v", err)
	}
	want := []DemandReason{ReasonNoPower, ReasonNoFreightCapacity}
	if !reflect.DeepEqual(bar2.Reasons, want) {
		t.Errorf("Demand.Reasons = %v, want %v", bar2.Reasons, want)
	}
}

func containsReason(rs []DemandReason, r DemandReason) bool {
	for _, x := range rs {
		if x == r {
			return true
		}
	}
	return false
}

// --- AC-6: winter construction slowdown via live engine.season call ------

func TestWinterSlowdownLongerLeadTimeThanSummer(t *testing.T) {
	b, _, _ := newBuildFixture(t)
	// Month 0 = January (constructionSpeedMultiplier 0.8); month 6 = July (1.0).
	winterID, err := b.SubmitBuildCommand(BuildCommand{
		Tile: world.TileCoord{X: 0, Y: 0}, Local: world.CellLocal{Row: 0, Col: 0},
		OwnerID: testOwner, Zone: ZoneDwelling, Month: 0,
	})
	if err != nil {
		t.Fatalf("winter SubmitBuildCommand: %v", err)
	}
	summerID, err := b.SubmitBuildCommand(BuildCommand{
		Tile: world.TileCoord{X: 0, Y: 0}, Local: world.CellLocal{Row: 0, Col: 1},
		OwnerID: testOwner, Zone: ZoneDwelling, Month: 6,
	})
	if err != nil {
		t.Fatalf("summer SubmitBuildCommand: %v", err)
	}

	winterLT := orderByID(t, b.Queue(), winterID).LeadTimeRemaining
	summerLT := orderByID(t, b.Queue(), summerID).LeadTimeRemaining
	if winterLT <= summerLT {
		t.Errorf("winter lead time %d should be strictly greater than summer lead time %d", winterLT, summerLT)
	}
}

// --- AC-7: demolition with compensation via engine.finance ---------------

func TestDemolishClearsCellAndPaysCompensation(t *testing.T) {
	b, w, l := newBuildFixture(t)
	if _, err := l.Provision(DefaultDistrict, market.ConstructionMaterials, 100000, 100000); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	tile := world.TileCoord{X: 0, Y: 0}
	local := world.CellLocal{Row: 0, Col: 0}
	id, err := b.SubmitBuildCommand(BuildCommand{Tile: tile, Local: local, OwnerID: testOwner, Zone: ZoneDwelling, Month: 6})
	if err != nil {
		t.Fatalf("SubmitBuildCommand: %v", err)
	}
	for i := int64(0); i < 200; i++ {
		if err := b.Tick(i); err != nil {
			t.Fatalf("Tick(%d): %v", i, err)
		}
	}
	if orderByID(t, b.Queue(), id).Status != OrderComplete {
		t.Fatalf("build order did not complete")
	}

	// The compensation figure must be exactly engine.finance's LandPrice for
	// this cell's terrain (via finance, not a build-local number).
	cell, err := w.CellAt(tile, local, testCorr())
	if err != nil {
		t.Fatalf("CellAt: %v", err)
	}
	expected := int64(finance.LandPrice(finance.LandCell{Terrain: terrainKindFor(cell.Surface)}))

	res, err := b.SubmitDemolishCommand(DemolishCommand{Tile: tile, Local: local, OwnerID: testOwner})
	if err != nil {
		t.Fatalf("SubmitDemolishCommand: %v", err)
	}
	if res.Compensation != expected {
		t.Errorf("Compensation = %d, want %d (finance.LandPrice of the cell)", res.Compensation, expected)
	}
	if res.Compensation < 0 {
		t.Errorf("Compensation = %d, want non-negative", res.Compensation)
	}
	// Cell state cleared.
	if _, ok := b.Structure(tile, local); ok {
		t.Errorf("structure still present after demolish")
	}
	if zt, ok := b.ZoneState(tile, local); ok {
		t.Errorf("zone %v still present after demolish", zt)
	}
}

func TestDemolishEmptyCellRejected(t *testing.T) {
	b, _, _ := newBuildFixture(t)
	tile := world.TileCoord{X: 0, Y: 0}
	local := world.CellLocal{Row: 0, Col: 0}
	_, err := b.SubmitDemolishCommand(DemolishCommand{Tile: tile, Local: local, OwnerID: testOwner})
	assertCode(t, err, ErrNoStructure)
}

// --- AC-10: registry errors for bad commands, never silent no-ops -------

func TestUnknownZoneTypeRejected(t *testing.T) {
	b, _, _ := newBuildFixture(t)
	err := b.SubmitZoneCommand(ZoneCommand{
		Tile: world.TileCoord{X: 0, Y: 0}, Local: world.CellLocal{Row: 0, Col: 0},
		OwnerID: testOwner, Zone: ZoneType("not-a-zone"),
	})
	assertCode(t, err, ErrUnknownZoneType)
}

func TestOutOfBoundsCellRejected(t *testing.T) {
	b, _, _ := newBuildFixture(t)
	err := b.SubmitZoneCommand(ZoneCommand{
		Tile: world.TileCoord{X: -1, Y: 0}, Local: world.CellLocal{Row: 0, Col: 0},
		OwnerID: testOwner, Zone: ZoneDwelling,
	})
	assertCode(t, err, ErrCellOutOfBounds)
}

// --- AC-12: determinism ---------------------------------------------------

func TestTickDeterministic(t *testing.T) {
	run := func() []BuildOrder {
		b, _, l := newBuildFixture(t)
		if _, err := l.Provision(DefaultDistrict, market.ConstructionMaterials, 1000, 700); err != nil {
			t.Fatalf("Provision: %v", err)
		}
		cmds := []BuildCommand{
			{Tile: world.TileCoord{X: 0, Y: 0}, Local: world.CellLocal{Row: 0, Col: 0}, OwnerID: testOwner, Zone: ZoneDwelling, Month: 0},
			{Tile: world.TileCoord{X: 0, Y: 0}, Local: world.CellLocal{Row: 0, Col: 1}, OwnerID: testOwner, Zone: ZoneShop, Month: 6},
			{Tile: world.TileCoord{X: 0, Y: 0}, Local: world.CellLocal{Row: 0, Col: 2}, OwnerID: testOwner, Zone: ZoneOffice, Month: 11},
		}
		for _, c := range cmds {
			if _, err := b.SubmitBuildCommand(c); err != nil {
				t.Fatalf("SubmitBuildCommand: %v", err)
			}
		}
		for i := int64(0); i < 120; i++ {
			if err := b.Tick(i); err != nil {
				t.Fatalf("Tick: %v", err)
			}
		}
		return b.Queue()
	}

	a := run()
	bq := run()
	if !reflect.DeepEqual(a, bq) {
		t.Errorf("identical command sequences produced different queue states:\nA=%v\nB=%v", a, bq)
	}
}

// --- Conservation invariant: every build spends exactly its materials
// budget, no units created or destroyed --------------------------------

func TestConservationEveryBuildSpendsExactlyItsBudget(t *testing.T) {
	b, _, l := newBuildFixture(t)
	stock := int64(1000)
	if _, err := l.Provision(DefaultDistrict, market.ConstructionMaterials, stock, stock); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	zi := zoneInfoFor(t, b, ZoneDwelling)

	var ids []BuildOrderID
	for col := 0; col < 3; col++ {
		id, err := b.SubmitBuildCommand(BuildCommand{
			Tile: world.TileCoord{X: 0, Y: 0}, Local: world.CellLocal{Row: 0, Col: col},
			OwnerID: testOwner, Zone: ZoneDwelling, Month: 6,
		})
		if err != nil {
			t.Fatalf("SubmitBuildCommand: %v", err)
		}
		ids = append(ids, id)
	}
	for i := int64(0); i < 500; i++ {
		if err := b.Tick(i); err != nil {
			t.Fatalf("Tick: %v", err)
		}
	}

	var drawnTotal, billTotal int64
	for _, id := range ids {
		o := orderByID(t, b.Queue(), id)
		if o.Status != OrderComplete {
			t.Fatalf("order %d not complete: %s", id, o.Status)
		}
		if o.MaterialsDrawn != o.MaterialsBillTotal {
			t.Errorf("order %d drew %d but its budget was %d — units created/destroyed",
				id, o.MaterialsDrawn, o.MaterialsBillTotal)
		}
		drawnTotal = satAdd(drawnTotal, o.MaterialsDrawn)
		billTotal = satAdd(billTotal, o.MaterialsBillTotal)
	}

	expected := satAdd(zi.Materials, satAdd(zi.Materials, zi.Materials))
	if billTotal != expected {
		t.Errorf("total budget = %d, want %d", billTotal, expected)
	}
	if drawnTotal != billTotal {
		t.Errorf("total drawn %d != total budget %d — conservation violated", drawnTotal, billTotal)
	}

	// The logistics shelf must have decreased by exactly the drawn total.
	s, err := l.Stock(DefaultDistrict, market.ConstructionMaterials)
	if err != nil {
		t.Fatalf("Stock: %v", err)
	}
	wantLevel := satSub(stock, drawnTotal)
	if s.Level != wantLevel {
		t.Errorf("logistics stock level = %d, want %d (stock - drawn)", s.Level, wantLevel)
	}
}

// --- AC-14: concurrent query + tick race ---------------------------------

func TestConcurrentQueueQueryAndTickNoRace(t *testing.T) {
	b, _, l := newBuildFixture(t)
	if _, err := l.Provision(DefaultDistrict, market.ConstructionMaterials, 100000, 100000); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if _, err := b.SubmitBuildCommand(BuildCommand{
		Tile: world.TileCoord{X: 0, Y: 0}, Local: world.CellLocal{Row: 0, Col: 0},
		OwnerID: testOwner, Zone: ZoneDwelling, Month: 6,
	}); err != nil {
		t.Fatalf("SubmitBuildCommand: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := int64(0); i < 500; i++ {
			if err := b.Tick(i); err != nil {
				t.Errorf("Tick: %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			_ = b.Queue()
			_ = b.ZoneTypes()
		}
	}()
	wg.Wait()
}
