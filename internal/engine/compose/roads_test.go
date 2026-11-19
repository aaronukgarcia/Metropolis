package compose

import (
	"encoding/json"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-9995385628 (roads lane 1a): a road laid through the real command path.

var roadsTile = world.TileCoord{X: defaultStartCoordX, Y: defaultStartCoordY}

// roadsLocal is the build module's address of the protocol cell.
func roadsLocal(c protocol.CellRef) world.CellLocal {
	return world.CellLocal{Row: c.Y, Col: c.X}
}

// roadsCity is a fresh city at the live version with the home tile bought.
func roadsCity(t *testing.T, store persist.Store) *ruleCity {
	t.Helper()
	rc := newRuleCity(t, store, -1)
	rc.advance(t, 5)
	rc.do(t, protocol.KindBuy, protocol.BuyPayload{Cell: roadHomeCell})
	return rc
}

// roadOrder returns the queued order for the cell with the given zone.
func roadOrder(rc *ruleCity, cell protocol.CellRef, zone build.ZoneType) (build.BuildOrder, bool) {
	for _, o := range rc.c.state.buildAPI.Queue() {
		if o.Zone == zone && o.Local == roadsLocal(cell) {
			return o, true
		}
	}
	return build.BuildOrder{}, false
}

// roadFinish advances in bounded steps until the road order completes.
func roadFinish(t *testing.T, rc *ruleCity) {
	t.Helper()
	for i := 0; i < 60; i++ {
		if o, ok := roadOrder(rc, roadCell, build.ZoneRoad); ok && o.Status == build.OrderComplete {
			return
		}
		rc.advance(t, 100)
	}
	t.Fatal("the road order never completed")
}

func roadViewportLabel(t *testing.T, rc *ruleCity, cell protocol.CellRef) string {
	t.Helper()
	raw, err := rc.c.state.buildViewportPatch()
	if err != nil {
		t.Fatal(err)
	}
	var p viewportWirePatch
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	for _, c := range p.Cells {
		if c.X == cell.X && c.Y == cell.Y {
			return c.Road
		}
	}
	t.Fatalf("cell %v missing from the viewport patch", cell)
	return ""
}

// TestRoads_Command_QueuedChargedCompletes: the order is accepted, queued at
// the data file's materials, drawn as the days pass, completes after its lead
// time, and only then is the cell a road cell and published as "laid". The
// F3 queue view lists it as a road order.
func TestRoads_Command_QueuedChargedCompletes(t *testing.T) {
	rc := roadsCity(t, nil)
	before := rc.queueLen()
	if res := rc.tryRoad(roadCell); !res.Accepted {
		t.Fatalf("road order refused: %+v", res.Error)
	}
	if rc.queueLen() != before+1 {
		t.Fatalf("queue %d, want %d", rc.queueLen(), before+1)
	}
	o, ok := roadOrder(rc, roadCell, build.ZoneRoad)
	if !ok || o.Status == build.OrderComplete {
		t.Fatalf("queued road order missing or already complete: %+v ok=%v", o, ok)
	}
	if o.MaterialsBillTotal <= 0 {
		t.Fatalf("the road order carries no materials bill: %+v", o)
	}
	if rc.c.state.buildAPI.HasRoad(roadsTile, roadsLocal(roadCell)) {
		t.Fatal("a queued road must not be a road cell yet")
	}
	if got := roadViewportLabel(t, rc, roadCell); got != "" {
		t.Fatalf("a queued road is published as %q, want nothing", got)
	}
	raw, err := rc.c.state.buildF3Patch()
	if err != nil {
		t.Fatal(err)
	}
	var f3 buildWirePatch
	if err := json.Unmarshal(raw, &f3); err != nil || f3.Queue == nil {
		t.Fatalf("F3 patch: %v", err)
	}
	found := false
	for _, q := range *f3.Queue {
		if q.Zone == string(build.ZoneRoad) && q.Cell.X == roadCell.X && q.Cell.Y == roadCell.Y {
			found = true
		}
	}
	if !found {
		t.Fatal("the F3 build queue view does not list the road order")
	}

	roadFinish(t, rc)
	done, _ := roadOrder(rc, roadCell, build.ZoneRoad)
	if done.MaterialsDrawn != done.MaterialsBillTotal {
		t.Fatalf("materials drawn %d differ from the bill %d", done.MaterialsDrawn, done.MaterialsBillTotal)
	}
	if !rc.c.state.buildAPI.HasRoad(roadsTile, roadsLocal(roadCell)) {
		t.Fatal("a completed road must be a road cell")
	}
	if got := roadViewportLabel(t, rc, roadCell); got != roadLabelLaid {
		t.Fatalf("a completed road is published as %q, want %q", got, roadLabelLaid)
	}
	if got := roadViewportLabel(t, rc, roadHomeCell); got != "" {
		t.Fatalf("a cell with no road is published as %q", got)
	}
}

// TestRoads_HomeOnARoadCellIsAccepted: BUG-1224's refusal does not apply to a
// home on a road cell, and the road and the home both remain.
func TestRoads_HomeOnARoadCellIsAccepted(t *testing.T) {
	rc := roadsCity(t, nil)
	if res := rc.tryRoad(roadCell); !res.Accepted {
		t.Fatalf("road order refused: %+v", res.Error)
	}
	roadFinish(t, rc)
	rc.do(t, protocol.KindZone, protocol.ZonePayload{Cell: roadCell, ZoneType: "dwelling"})
	rc.do(t, protocol.KindBuild, protocol.BuildPayload{Cell: roadCell, BuildingType: "dwelling"})
	if _, ok := roadOrder(rc, roadCell, build.ZoneDwelling); !ok {
		t.Fatal("the home order on the road cell is not queued")
	}
	rc.advance(t, 400)
	if !rc.c.state.buildAPI.HasRoad(roadsTile, roadsLocal(roadCell)) {
		t.Fatal("the road did not remain after the home was ordered")
	}
	if res := rc.tryRoad(roadCell); res.Accepted {
		t.Fatal("a second road on the same cell was accepted")
	}
}

// TestRoads_SaveLoadContinueEqualsUninterrupted: snapshots taken with the
// road queued and with the road built, and the journal alone, restore to the
// state the uninterrupted run reached, and then continue identically.
func TestRoads_SaveLoadContinueEqualsUninterrupted(t *testing.T) {
	store := persist.NewMemStore()
	rc := roadsCity(t, store)
	if res := rc.tryRoad(roadCell); !res.Accepted {
		t.Fatalf("road order refused: %+v", res.Error)
	}
	rc.advance(t, 1)
	rc.snapshot(t, store, 1) // snapshot 0: the road is queued
	roadFinish(t, rc)
	rc.snapshot(t, store, 1) // snapshot 1: the road is built
	rc.advance(t, 50)
	want := rc.outcome(t)

	for _, keep := range [][]int{nil, {0}, {1}} {
		w, _ := wake(t, cloneStore(t, store, keep...))
		if got := w.outcome(t); got != want {
			t.Fatalf("restore keeping %v differs from the uninterrupted run:\n got %+v\nwant %+v", keep, got, want)
		}
		if !w.c.state.buildAPI.HasRoad(roadsTile, roadsLocal(roadCell)) {
			t.Fatalf("restore keeping %v lost the road", keep)
		}
		w.advance(t, 30)
		rc2, _ := wake(t, cloneStore(t, store))
		rc2.advance(t, 30)
		if w.outcome(t) != rc2.outcome(t) {
			t.Fatalf("continuing after a restore keeping %v differs from a genesis restore continued alike", keep)
		}
	}
}
