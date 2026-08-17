package roads

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
)

// newTestAPI loads a RoadsAPI from the real data/ directory with a fixed
// world seed, failing the test if the data files do not resolve or validate.
func newTestAPI(t *testing.T) *RoadsAPI {
	t.Helper()
	a, err := LoadDefault(42, "test-correlation")
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	return a
}

// newTestWorld returns a purchased, owned start tile so zoning can be set
// on specific cells for the AC-5 widening test.
func newTestWorld(t *testing.T) *world.WorldAPI {
	t.Helper()
	w := world.NewWorldAPI(world.TileCoord{X: 0, Y: 0})
	if res := w.PurchaseTile(world.PurchaseCommand{
		CorrelationID: "test-correlation",
		Tile:          world.TileCoord{X: 0, Y: 0},
		BuyerID:       1,
	}); !res.Accepted {
		t.Fatalf("PurchaseTile rejected: %v", res.Error)
	}
	return w
}

// zoneCell sets zoning on a specific cell of the owned start tile.
func zoneCell(t *testing.T, w *world.WorldAPI, row, col int, z world.Zoning) {
	t.Helper()
	if res := w.ApplyOwnershipCommand(world.OwnershipCommand{
		CorrelationID: "test-correlation",
		Tile:          world.TileCoord{X: 0, Y: 0},
		Local:         world.CellLocal{Row: row, Col: col},
		NewZoning:     z,
	}); !res.Accepted {
		t.Fatalf("ApplyOwnershipCommand rejected: %v", res.Error)
	}
}

// addRoad is a small convenience wrapper for the common AddNode+AddRoad pair
// in tests.
func addRoad(t *testing.T, a *RoadsAPI, id RoadID, startRow, startCol, endRow, endCol int, class RoadClass) Road {
	t.Helper()
	startID, endID := NodeID(2*id+1), NodeID(2*id+2)
	if err := a.AddNode(AddNodeCommand{CorrelationID: "test", ID: startID, Pos: CellRef{
		Tile: world.TileCoord{X: 0, Y: 0}, Local: world.CellLocal{Row: startRow, Col: startCol},
	}}); err != nil {
		t.Fatalf("AddNode start: %v", err)
	}
	if err := a.AddNode(AddNodeCommand{CorrelationID: "test", ID: endID, Pos: CellRef{
		Tile: world.TileCoord{X: 0, Y: 0}, Local: world.CellLocal{Row: endRow, Col: endCol},
	}}); err != nil {
		t.Fatalf("AddNode end: %v", err)
	}
	road, err := a.AddRoad(AddRoadCommand{CorrelationID: "test", ID: id, Start: startID, End: endID, Class: class})
	if err != nil {
		t.Fatalf("AddRoad: %v", err)
	}
	return road
}
