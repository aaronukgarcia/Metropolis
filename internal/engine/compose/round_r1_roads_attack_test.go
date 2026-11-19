package compose

import (
	"context"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// Round r1 (FEAT-9995385628), independent reviewer: refusals through the real
// command path leave no trace, and the cell-sharing rules hold both ways.

// TestRoundR1Roads_TenPressesOneOrderNoTrace: ten road orders on one cell
// give one order; the nine refusals are MET-G511, journal nothing, change
// neither treasury nor queue, and take no order number (the next accepted
// order is numbered one above the first).
func TestRoundR1Roads_TenPressesOneOrderNoTrace(t *testing.T) {
	ctx := context.Background()
	store := persist.NewMemStore()
	rc := roadsCity(t, store)
	if res := rc.tryRoad(roadCell); !res.Accepted {
		t.Fatalf("first road refused: %+v", res.Error)
	}
	first, _ := roadOrder(rc, roadCell, build.ZoneRoad)
	frames0, err := store.ReadJournal(ctx, ruleTestCity)
	if err != nil {
		t.Fatal(err)
	}
	money0, q0 := rc.c.Treasury(), rc.queueLen()
	for i := 0; i < 9; i++ {
		res := rc.tryRoad(roadCell)
		if res.Accepted || res.Error == nil || res.Error.Code != "MET-G511" {
			t.Fatalf("press %d: want MET-G511, got %+v", i+2, res)
		}
	}
	frames1, err := store.ReadJournal(ctx, ruleTestCity)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames1) != len(frames0) || rc.c.Treasury() != money0 || rc.queueLen() != q0 {
		t.Fatalf("refusals left a trace: frames %d->%d money %d->%d queue %d->%d",
			len(frames0), len(frames1), money0, rc.c.Treasury(), q0, rc.queueLen())
	}
	other := protocol.CellRef{X: 6, Y: 3}
	if res := rc.tryRoad(other); !res.Accepted {
		t.Fatalf("road on a fresh cell refused: %+v", res.Error)
	}
	next, _ := roadOrder(rc, other, build.ZoneRoad)
	if next.ID != first.ID+1 {
		t.Fatalf("a refusal took an order number: first %d next %d", first.ID, next.ID)
	}
	// Built: still refused, still no trace.
	roadFinish(t, rc)
	frames2, _ := store.ReadJournal(ctx, ruleTestCity)
	money2, q2 := rc.c.Treasury(), rc.queueLen()
	if res := rc.tryRoad(roadCell); res.Accepted || res.Error == nil || res.Error.Code != "MET-G511" {
		t.Fatalf("road on a built road: want MET-G511, got %+v", res)
	}
	frames3, _ := store.ReadJournal(ctx, ruleTestCity)
	if len(frames3) != len(frames2) || rc.c.Treasury() != money2 || rc.queueLen() != q2 {
		t.Fatal("refusal on a built road left a trace")
	}
}

// TestRoundR1Roads_SharingBothWays: a road on a cell with a QUEUED home and a
// home on a cell with a QUEUED road are both accepted; a second home on the
// queued-home cell is still refused (MET-G510); demolishing the finished home
// through the command path leaves the road.
func TestRoundR1Roads_SharingBothWays(t *testing.T) {
	rc := roadsCity(t, nil)
	homeFirst := protocol.CellRef{X: 7, Y: 3}
	rc.do(t, protocol.KindZone, protocol.ZonePayload{Cell: homeFirst, ZoneType: "dwelling"})
	rc.do(t, protocol.KindBuild, protocol.BuildPayload{Cell: homeFirst, BuildingType: "dwelling"})
	if res := rc.tryRoad(homeFirst); !res.Accepted {
		t.Fatalf("road on a queued-home cell refused: %+v", res.Error)
	}
	if res := rc.tryBuild(homeFirst); res.Accepted || res.Error == nil || res.Error.Code != "MET-G510" {
		t.Fatalf("second home on a queued-home cell: want MET-G510, got %+v", res)
	}
	roadFirst := protocol.CellRef{X: 8, Y: 3}
	if res := rc.tryRoad(roadFirst); !res.Accepted {
		t.Fatalf("road refused: %+v", res.Error)
	}
	rc.do(t, protocol.KindZone, protocol.ZonePayload{Cell: roadFirst, ZoneType: "dwelling"})
	if res := rc.tryBuild(roadFirst); !res.Accepted {
		t.Fatalf("home on a queued-road cell refused: %+v", res.Error)
	}
	rc.advance(t, 400)
	for _, c := range []protocol.CellRef{homeFirst, roadFirst} {
		if !rc.c.state.buildAPI.HasRoad(roadsTile, roadsLocal(c)) {
			t.Fatalf("cell %v: road missing", c)
		}
		if _, ok := rc.c.state.buildAPI.Structure(roadsTile, roadsLocal(c)); !ok {
			t.Fatalf("cell %v: home missing", c)
		}
	}
	rc.do(t, protocol.KindDemolish, protocol.DemolishPayload{Cell: homeFirst})
	if _, ok := rc.c.state.buildAPI.Structure(roadsTile, roadsLocal(homeFirst)); ok {
		t.Fatal("home still standing after demolish")
	}
	if !rc.c.state.buildAPI.HasRoad(roadsTile, roadsLocal(homeFirst)) {
		t.Fatal("demolishing the home removed the road")
	}
	if got := roadViewportLabel(t, rc, homeFirst); got != roadLabelLaid {
		t.Fatalf("road under a demolished home published as %q", got)
	}
}
