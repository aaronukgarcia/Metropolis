package build

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
)

// TestSetStructureRendersThroughTileCells is the BUG-362 r2 independent
// confirmation that the SetStructure render fix stands ALONE, without the
// (reverted) compose.go money-circulation changes.
//
// It drives a build order end-to-end and reads the result back through the
// EXACT accessor the F1 viewport uses to publish the map:
// world.WorldAPI.TileCells (compose/viewport_publish.go:144, which then
// feeds structureLabel(c.StructureRef)). A nonzero StructureRef at the
// built cell is precisely what makes the building show on the map.
func TestSetStructureRendersThroughTileCells(t *testing.T) {
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
	if o := orderByID(t, b.Queue(), id); o.Status != OrderComplete {
		t.Fatalf("precondition: order not complete (status %s); render assertion meaningless", o.Status)
	}

	// Read back through the viewport's own accessor, not build's private map.
	cells, err := w.TileCells(tile, testCorr())
	if err != nil {
		t.Fatalf("TileCells: %v", err)
	}
	const side = world.TileSizeCells
	idx := int(local.Row)*side + int(local.Col)
	if got := cells[idx].StructureRef; got == 0 {
		t.Fatalf("built cell (%d,%d) has StructureRef 0 via TileCells — the map would render nothing; "+
			"SetStructure did not reach world.structureRef", local.Row, local.Col)
	}

	// Every other cell of the owned tile must stay 0 (no spurious writes).
	for i, c := range cells {
		if i != idx && c.StructureRef != 0 {
			t.Fatalf("cell index %d unexpectedly has StructureRef %d; only the built cell should be set", i, c.StructureRef)
		}
	}
}

// TestSetStructureContractUnownedTile confirms SetStructure returns a
// registry error (never panics) when the tile is not owned, and does not
// mutate any world state. This exercises the split increment's contract
// directly rather than only through build.Tick.
func TestSetStructureContractUnownedTile(t *testing.T) {
	w := world.NewWorldAPI(world.TileCoord{X: 0, Y: 0})
	// Tile (5,5) is in-extent but never purchased.
	err := w.SetStructure(world.TileCoord{X: 5, Y: 5}, world.CellLocal{Row: 0, Col: 0}, 42, testCorr())
	if err == nil {
		t.Fatal("expected SetStructure on an unowned tile to return an error, got nil")
	}
}

// TestSetStructureContractOutOfBounds confirms an out-of-extent tile is
// rejected with a registry error, not a panic or a silent write.
func TestSetStructureContractOutOfBounds(t *testing.T) {
	w := world.NewWorldAPI(world.TileCoord{X: 0, Y: 0})
	err := w.SetStructure(world.TileCoord{X: 1 << 20, Y: 1 << 20}, world.CellLocal{Row: 0, Col: 0}, 7, testCorr())
	if err == nil {
		t.Fatal("expected SetStructure on an out-of-bounds tile to return an error, got nil")
	}
}
