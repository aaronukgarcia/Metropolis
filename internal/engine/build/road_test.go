package build

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/logistics"
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/engine/rules"
	"github.com/aaronukgarcia/Metropolis/internal/engine/season"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// FEAT-9995385628 (roads lane 1a, rule version 2, rules.LayRoad): the build
// module's road path. These tests also prove the registry's Old and New text
// for LayRoad is true.

// roadLayJSON renders a roadlay.json with the given economics.
func roadLayJSON(tonnes, labour, lead int64) string {
	return fmt.Sprintf(`{"version":1,"materialsTonnes":%d,"labourDays":%d,"leadDays":%d,"class":"gravel"}`, tonnes, labour, lead)
}

// newRoadFixtureIn wires a BuildAPI against dir, which already holds a
// roadlay.json when the caller wants roads.
func newRoadFixtureIn(t *testing.T, dir string, v rules.Version) (*BuildAPI, *world.WorldAPI) {
	t.Helper()
	writeBuildings(t, dir, fixtureBuildingsJSON(100, 45))
	if err := os.WriteFile(filepath.Join(dir, "roads.json"), []byte(`{"classes":[{"id":"gravel"},{"id":"alley"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := Load(dir, testCorr())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	w := newOwnedWorld(t)
	ckErr(t, b.SetWorld(w))
	s, err := season.LoadDefault(testCorr())
	if err != nil {
		t.Fatal(err)
	}
	ckErr(t, b.SetSeason(s))
	l, err := logistics.LoadDefault(testCorr())
	if err != nil {
		t.Fatal(err)
	}
	ckErr(t, b.SetLogistics(l))
	if _, err := l.Provision(DefaultDistrict, market.ConstructionMaterials, 100000, 100000); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	b.SetRuleVersionSource(func() rules.Version { return v })
	return b, w
}

// roadFixtureAt is the standard fixture: 5 tonnes, 5 labour days, 5 lead days.
func roadFixtureAt(t *testing.T, v rules.Version) *BuildAPI {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "roadlay.json"), []byte(roadLayJSON(5, 5, 5)), 0o644); err != nil {
		t.Fatal(err)
	}
	b, _ := newRoadFixtureIn(t, dir, v)
	return b
}

func cellAt(row, col int) world.CellLocal { return world.CellLocal{Row: row, Col: col} }

func submitRoad(b *BuildAPI, row, col int) (BuildOrderID, error) {
	return b.SubmitRoadCommand(BuildCommand{Tile: tile00(), Local: cellAt(row, col), OwnerID: testOwner, Month: 6})
}

func submitHome(b *BuildAPI, row, col int) (BuildOrderID, error) {
	return b.SubmitBuildCommand(BuildCommand{Tile: tile00(), Local: cellAt(row, col), OwnerID: testOwner, Zone: ZoneDwelling, Month: 6})
}

// untouched asserts a refused road order left no trace.
func untouched(t *testing.T, b *BuildAPI, queueLen int, next BuildOrderID, what string) {
	t.Helper()
	if n := len(b.Queue()); n != queueLen {
		t.Errorf("%s: queue length %d, want %d", what, n, queueLen)
	}
	if got := nextOrderOf(b); got != next {
		t.Errorf("%s: nextOrder %d, want %d", what, got, next)
	}
}

// --- the version branch (Old and New text of the LayRoad entry) ---------

func TestRoad_BelowLayRoadIsRefusedAsUnknownZone(t *testing.T) {
	for _, v := range []rules.Version{0, rules.RefuseOccupiedCell} {
		b := roadFixtureAt(t, v)
		_, err := submitRoad(b, 0, 0)
		assertCode(t, err, ErrUnknownZoneType)
		untouched(t, b, 0, 0, fmt.Sprintf("version %d", v))
		if b.HasRoad(tile00(), cellAt(0, 0)) {
			t.Errorf("version %d: road present after a refused order", v)
		}
	}
}

func TestRoad_AtLayRoadIsAcceptedAndCharged(t *testing.T) {
	b := roadFixtureAt(t, rules.LayRoad)
	id, err := submitRoad(b, 0, 0)
	if err != nil {
		t.Fatalf("road refused at the LayRoad version: %v", err)
	}
	if id != 1 || nextOrderOf(b) != 1 {
		t.Errorf("order id %d, nextOrder %d; want 1, 1", id, nextOrderOf(b))
	}
	o := b.Queue()[0]
	const wantUnits = 5 // tonnes and labour days in the test's road data
	if o.Zone != ZoneRoad || o.MaterialsBillTotal != wantUnits || o.MaterialsRemaining != wantUnits || o.LabourRemaining != wantUnits {
		t.Errorf("queued order = %+v; want zone road, 5 tonnes, 5 labour days", o)
	}
	if o.LeadTimeRemaining < 5 {
		t.Errorf("lead time %d; want at least the 5 base days", o.LeadTimeRemaining)
	}
	runTicks(t, b, 40)
	var drawn int64
	for _, q := range b.Queue() {
		drawn += q.MaterialsDrawn
	}
	if drawn != 5 {
		t.Errorf("materials drawn = %d, want exactly the file's 5 tonnes", drawn)
	}
}

// --- cost is data -------------------------------------------------------

func TestRoad_CostComesFromTheFile(t *testing.T) {
	for _, tonnes := range []int64{3, 9} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "roadlay.json"), []byte(roadLayJSON(tonnes, 2, 2)), 0o644); err != nil {
			t.Fatal(err)
		}
		b, _ := newRoadFixtureIn(t, dir, rules.LayRoad)
		if _, err := submitRoad(b, 0, 0); err != nil {
			t.Fatal(err)
		}
		if got := b.Queue()[0].MaterialsBillTotal; got != tonnes {
			t.Errorf("file says %d tonnes, order bill is %d", tonnes, got)
		}
	}
}

func TestRoad_MissingFileIsALoudRoadOrderError(t *testing.T) {
	dir := t.TempDir()
	b, _ := newRoadFixtureIn(t, dir, rules.LayRoad)
	_, err := submitRoad(b, 0, 0)
	if err == nil {
		t.Fatal("a road order was accepted with no roadlay.json")
	}
	untouched(t, b, 0, 0, "no roadlay.json")
}

func TestRoad_BadFileFailsLoad(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "roadlay.json"), []byte(roadLayJSON(0, 1, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	writeBuildings(t, dir, fixtureBuildingsJSON(100, 45))
	if _, err := Load(dir, testCorr()); err == nil {
		t.Fatal("Load accepted zero tonnes")
	}
}

// --- completion --------------------------------------------------------

func TestRoad_CompleteExactlyAtLeadTime(t *testing.T) {
	b := roadFixtureAt(t, rules.LayRoad)
	if _, err := submitRoad(b, 4, 7); err != nil {
		t.Fatal(err)
	}
	lead := b.Queue()[0].LeadTimeRemaining
	for day := int64(1); day <= lead; day++ {
		if b.HasRoad(tile00(), cellAt(4, 7)) {
			t.Fatalf("road present on day %d, before day %d", day-1, lead)
		}
		if err := b.Tick(day); err != nil {
			t.Fatal(err)
		}
	}
	if !b.HasRoad(tile00(), cellAt(4, 7)) {
		t.Fatalf("no road after %d days", lead)
	}
	if _, ok := b.Structure(tile00(), cellAt(4, 7)); ok {
		t.Error("a road created a structure")
	}
	if _, ok := b.ZoneState(tile00(), cellAt(4, 7)); ok {
		t.Error("a road created a zone")
	}
	if n := len(b.StandingCapacities()); n != 0 {
		t.Errorf("a road produced %d standing capacity rows", n)
	}
}

// --- a road never holds a cell ------------------------------------------

func TestRoad_NeverHoldsACell(t *testing.T) {
	cases := []struct {
		name string
		do   func(b *BuildAPI) error
	}{
		{"home then road", func(b *BuildAPI) error {
			if _, err := submitHome(b, 1, 1); err != nil {
				return err
			}
			_, err := submitRoad(b, 1, 1)
			return err
		}},
		{"road then home", func(b *BuildAPI) error {
			if _, err := submitRoad(b, 1, 1); err != nil {
				return err
			}
			_, err := submitHome(b, 1, 1)
			return err
		}},
		{"built home, then road", func(b *BuildAPI) error {
			if _, err := submitHome(b, 1, 1); err != nil {
				return err
			}
			runTicks(t, b, 200)
			_, err := submitRoad(b, 1, 1)
			return err
		}},
		{"home on a completed road", func(b *BuildAPI) error {
			if _, err := submitRoad(b, 1, 1); err != nil {
				return err
			}
			runTicks(t, b, 200)
			if !b.HasRoad(tile00(), cellAt(1, 1)) {
				return fmt.Errorf("precondition: road did not complete")
			}
			_, err := submitHome(b, 1, 1)
			return err
		}},
	}
	for _, c := range cases {
		b := roadFixtureAt(t, rules.LayRoad)
		if err := c.do(b); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

func TestRoad_HomeStillRefusesAHomeOnAHome(t *testing.T) {
	b := roadFixtureAt(t, rules.LayRoad)
	if _, err := submitRoad(b, 1, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := submitHome(b, 1, 1); err != nil {
		t.Fatal(err)
	}
	_, err := submitHome(b, 1, 1)
	assertCode(t, err, ErrCellInUse)
}

// --- a second road is refused -------------------------------------------

func TestRoad_SecondRoadRefusedBuiltOrQueued(t *testing.T) {
	b := roadFixtureAt(t, rules.LayRoad)
	if _, err := submitRoad(b, 2, 2); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		_, err := submitRoad(b, 2, 2)
		assertCode(t, err, ErrRoadAlreadyLaid)
	}
	untouched(t, b, 1, 1, "ten presses on a queued road")
	runTicks(t, b, 200)
	_, err := submitRoad(b, 2, 2)
	assertCode(t, err, ErrRoadAlreadyLaid)
	untouched(t, b, 1, 1, "press on a built road")
	var drawn int64
	for _, o := range b.Queue() {
		drawn += o.MaterialsDrawn
	}
	if drawn != 5 {
		t.Errorf("ten presses drew %d tonnes, want one road's 5", drawn)
	}
}

// --- ownership and bounds -----------------------------------------------

func TestRoad_UnownedAndOutOfBoundsRefused(t *testing.T) {
	b := roadFixtureAt(t, rules.LayRoad)
	_, err := b.SubmitRoadCommand(BuildCommand{Tile: tile00(), Local: cellAt(0, 0), OwnerID: testOwner + 1, Month: 6})
	assertCode(t, err, ErrCellNotOwned)
	_, err = b.SubmitRoadCommand(BuildCommand{Tile: tile00(), Local: cellAt(-1, 0), OwnerID: testOwner, Month: 6})
	assertCode(t, err, ErrCellOutOfBounds)
	untouched(t, b, 0, 0, "unowned and out of bounds")
}

// --- demolish leaves the road -------------------------------------------

func TestRoad_DemolishingAHomeLeavesTheRoad(t *testing.T) {
	b := roadFixtureAt(t, rules.LayRoad)
	if _, err := submitRoad(b, 3, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := submitHome(b, 3, 3); err != nil {
		t.Fatal(err)
	}
	runTicks(t, b, 200)
	if _, err := b.SubmitDemolishCommand(DemolishCommand{Tile: tile00(), Local: cellAt(3, 3), OwnerID: testOwner}); err != nil {
		t.Fatalf("demolish: %v", err)
	}
	if !b.HasRoad(tile00(), cellAt(3, 3)) {
		t.Error("demolishing the home removed the road")
	}
}

// --- read API -----------------------------------------------------------

func TestRoad_RoadCellsAreSortedWhateverTheOrderLaid(t *testing.T) {
	want := []world.CellLocal{cellAt(0, 5), cellAt(0, 9), cellAt(2, 1), cellAt(7, 0), cellAt(7, 3)}
	orders := [][]int{{0, 1, 2, 3, 4}, {4, 3, 2, 1, 0}, {2, 4, 0, 3, 1}, {3, 0, 4, 1, 2}}
	for _, ord := range orders {
		b := roadFixtureAt(t, rules.LayRoad)
		for _, i := range ord {
			if _, err := submitRoad(b, want[i].Row, want[i].Col); err != nil {
				t.Fatal(err)
			}
		}
		runTicks(t, b, 200)
		if got := b.RoadCells(tile00()); !reflect.DeepEqual(got, want) {
			t.Errorf("order %v: RoadCells = %v, want %v", ord, got, want)
		}
		if got := b.RoadCells(world.TileCoord{X: 5, Y: 5}); len(got) != 0 {
			t.Errorf("another tile's roads = %v", got)
		}
	}
}

func TestRoad_QueuedRoadIsNotYetARoad(t *testing.T) {
	b := roadFixtureAt(t, rules.LayRoad)
	if _, err := submitRoad(b, 0, 0); err != nil {
		t.Fatal(err)
	}
	if b.HasRoad(tile00(), cellAt(0, 0)) || len(b.RoadCells(tile00())) != 0 {
		t.Error("a queued road reads as a road")
	}
}

// --- saves --------------------------------------------------------------

// roadsAndQueue lays three finished and two queued roads, the finished ones in
// the given order.
func roadsAndQueue(t *testing.T, order []int) *BuildAPI {
	t.Helper()
	b := roadFixtureAt(t, rules.LayRoad)
	built := []world.CellLocal{cellAt(1, 1), cellAt(1, 2), cellAt(9, 0)}
	queued := []world.CellLocal{cellAt(5, 5), cellAt(0, 8)}
	for _, i := range order {
		if _, err := submitRoad(b, built[i].Row, built[i].Col); err != nil {
			t.Fatal(err)
		}
	}
	runTicks(t, b, 200)
	for _, c := range queued {
		if _, err := submitRoad(b, c.Row, c.Col); err != nil {
			t.Fatal(err)
		}
	}
	return b
}

func TestRoad_SaveLoadContinueEqualsUninterrupted(t *testing.T) {
	b1 := roadsAndQueue(t, []int{0, 1, 2})
	root := saveInto(t, b1, "roads1")
	b2 := roadFixtureAt(t, rules.LayRoad)
	loadInto(t, root, b2, "roads1")
	if !reflect.DeepEqual(b1.roadCells, b2.roadCells) || len(b2.roadCells) != 3 {
		t.Fatalf("roads after load = %v, want %v", b2.roadCells, b1.roadCells)
	}
	if !reflect.DeepEqual(b1.Queue(), b2.Queue()) {
		t.Fatalf("queue differs after load:\n%+v\n%+v", b1.Queue(), b2.Queue())
	}
	// Continue both: the two queued roads complete identically.
	runTicks(t, b1, 100)
	runTicks(t, b2, 100)
	if !reflect.DeepEqual(b1.RoadCells(tile00()), b2.RoadCells(tile00())) || len(b2.RoadCells(tile00())) != 5 {
		t.Errorf("after continuing: %v vs %v", b1.RoadCells(tile00()), b2.RoadCells(tile00()))
	}
	if !reflect.DeepEqual(b1.Queue(), b2.Queue()) {
		t.Error("queues differ after continuing")
	}
}

func TestRoad_SaveBytesDoNotDependOnTheOrderRoadsWereLaid(t *testing.T) {
	snapA, err := roadsAndQueue(t, []int{0, 1, 2}).snapshotForSave()
	if err != nil {
		t.Fatal(err)
	}
	snapB, err := roadsAndQueue(t, []int{2, 0, 1}).snapshotForSave()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapA.roads, snapB.roads) || len(snapA.roads) != 3 {
		t.Errorf("road records depend on insertion order: %v vs %v", snapA.roads, snapB.roads)
	}
	for i := 1; i < len(snapA.roads); i++ {
		prev := cellKey{tile: snapA.roads[i-1].Tile, local: snapA.roads[i-1].Local}
		cur := cellKey{tile: snapA.roads[i].Tile, local: snapA.roads[i].Local}
		if !lessCellKey(prev, cur) {
			t.Errorf("road records not strictly sorted: %v", snapA.roads)
		}
	}
	assertBundlesByteIdentical(t, saveInto(t, roadsAndQueue(t, []int{0, 1, 2}), "same"), saveInto(t, roadsAndQueue(t, []int{0, 1, 2}), "same"))
}

func TestRoad_SaveWithNoRoadsCarriesNoRoadRecord(t *testing.T) {
	b := roadFixtureAt(t, rules.LayRoad)
	src := NewSaveParticipant(b).Source()
	for {
		rec, ok, err := src()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		if rec.Kind == recBuildRoad {
			t.Fatal("a city with no roads wrote a road record")
		}
	}
}

func TestRoad_OldSaveWithoutRoadRecordsLoadsAsNoRoads(t *testing.T) {
	b := roadFixtureAt(t, rules.LayRoad)
	if _, err := submitRoad(b, 0, 0); err != nil {
		t.Fatal(err)
	}
	runTicks(t, b, 200)
	if !b.HasRoad(tile00(), cellAt(0, 0)) {
		t.Fatal("precondition: road did not complete")
	}
	h := NewSaveParticipant(b).Handler()
	if err := h(serialize.Record{Kind: recBuildMeta, Data: []byte(`{"district":"` + DefaultDistrict + `","nextOrder":0}`)}); err != nil {
		t.Fatal(err)
	}
	if len(b.roadCells) != 0 || b.HasRoad(tile00(), cellAt(0, 0)) {
		t.Errorf("roads survived a load of a save with no road records: %v", b.roadCells)
	}
}
