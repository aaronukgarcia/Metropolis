package mining

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
)

// This file pins BUG-585's fix: globalCell/cellFromGlobal's Y (northing)
// axis must invert CellLocal.Row against engine.world's own documented
// north-first convention, the same convention engine.build's serviceLocation
// pins in internal/engine/build/services_bridge_test.go
// (TestServiceLocationRowAxisMatchesWorldNorthFirstConvention). Evidence,
// cited straight from engine.world's own source (VERIFIED, not assumed):
//
//   - internal/engine/world/terrain_import.go's SourceGrid doc comment:
//     "row-major elevation samples... row 0 is the northernmost" (the ESRI
//     ASCII-grid convention OS Terrain 50 ships in).
//   - ImportTerrain's own inline comment: "outputV: 0 at the output grid's
//     south edge (row TileSizeCells-1), 1 at its north edge (row 0) --
//     output row 0 is north per ESRI's north-first convention".
//   - populateTerrainFromHeightmap writes that SAME row value into
//     localIndex(col, row) -- the identical index space CellAt reads via
//     CellLocal.Row, so the convention is CellLocal.Row's generally.
//
// Row 0 is therefore a tile's NORTH edge and Row grows SOUTHWARD -- the
// OPPOSITE of TileCoord.Y, which grows north (grid.go's TileCoord doc
// comment). Before this fix, globalCell computed
// `gy = tile.Y*TileSizeCells + local.Row` with no inversion: within one
// tile this is merely a mirror flip (harmless, since EffectAt only ever
// compares relative distances/LOS within the flip), but ACROSS a tile-Y
// boundary it made gy non-monotonic with true geographic northing -- see
// TestGlobalCellMonotonicAcrossTileBoundary. Because EffectAt's home cell
// and a blighting object routinely sit in different tiles across the
// 30x30 expansion grid, this was a live defect, not a self-contained
// internal index (BUG-585 investigation). Save-compat is not a concern:
// (gx,gy) is never persisted -- every blightingObject/extractionSite
// stores the real world.TileCoord/world.CellLocal verbatim, and
// globalCell/cellFromGlobal are transient, in-package geometry recomputed
// every call.

// TestGlobalCellRowAxisMatchesWorldNorthFirstConvention pins the corrected
// direction directly, mirroring engine.build's own pinning test: within a
// single tile, Row=0 (north edge) must produce a STRICTLY LARGER gy (i.e.
// further north) than Row=TileSizeCells-1 (south edge).
func TestGlobalCellRowAxisMatchesWorldNorthFirstConvention(t *testing.T) {
	tile := world.TileCoord{X: 5, Y: 5}
	_, gyNorth := globalCell(tile, world.CellLocal{Row: 0, Col: 0})
	_, gySouth := globalCell(tile, world.CellLocal{Row: world.TileSizeCells - 1, Col: 0})
	if gyNorth <= gySouth {
		t.Fatalf("Row=0 (north edge) produced gy=%d, Row=%d (south edge) produced gy=%d -- "+
			"Row=0 must be north (a LARGER gy) of Row=%d, per engine.world's own "+
			"north-first convention (terrain_import.go)",
			gyNorth, world.TileSizeCells-1, gySouth, world.TileSizeCells-1)
	}
	if gyNorth-gySouth != world.TileSizeCells-1 {
		t.Fatalf("north/south span within one tile = %d cells, want %d (one cell short of the full tile)",
			gyNorth-gySouth, world.TileSizeCells-1)
	}
}

// TestGlobalCellMonotonicAcrossTileBoundary is the RED-proof for the actual
// live defect: continuing due north from the northernmost cell of tile
// (X,5) into the southernmost cell of tile (X,6) -- its true geographic
// neighbour to the north, since TileCoord.Y grows north -- must advance gy
// by exactly one cell, never jump backward or leap forward by most of a
// tile. Flipping the fix back to the naive `tile.Y*TileSizeCells+local.Row`
// makes this fail (verified by hand: naive gy at the north edge of tile Y=5
// is 5*200+0=1000, and naive gy at the south edge of tile Y=6 is
// 6*200+199=1399 -- a 399-cell jump for what should be a 1-cell step).
func TestGlobalCellMonotonicAcrossTileBoundary(t *testing.T) {
	south := world.TileCoord{X: 5, Y: 5}
	north := world.TileCoord{X: 5, Y: 6}
	_, gyNorthEdgeOfSouth := globalCell(south, world.CellLocal{Row: 0, Col: 0})
	_, gySouthEdgeOfNorth := globalCell(north, world.CellLocal{Row: world.TileSizeCells - 1, Col: 0})
	if gySouthEdgeOfNorth != gyNorthEdgeOfSouth+1 {
		t.Fatalf("crossing the tile boundary due north stepped gy from %d to %d (delta %d), want delta 1 -- "+
			"gy is not a consistent northing axis across tiles",
			gyNorthEdgeOfSouth, gySouthEdgeOfNorth, gySouthEdgeOfNorth-gyNorthEdgeOfSouth)
	}
}

// TestCellFromGlobalInvertsGlobalCell proves the round-trip pair stays
// exact under the new inverted encoding, across the full Row range and a
// couple of tiles -- elevation lookups (elevationAt) depend on this being
// lossless.
func TestCellFromGlobalInvertsGlobalCell(t *testing.T) {
	tiles := []world.TileCoord{{X: 0, Y: 0}, {X: 15, Y: 15}, {X: 29, Y: 29}}
	for _, tile := range tiles {
		for row := 0; row < world.TileSizeCells; row = nextStride(row, 7, world.TileSizeCells) {
			for col := 0; col < world.TileSizeCells; col = nextStride(col, 11, world.TileSizeCells) {
				local := world.CellLocal{Row: row, Col: col}
				gx, gy := globalCell(tile, local)
				gotTile, gotLocal, ok := cellFromGlobal(gx, gy)
				if !ok {
					t.Fatalf("cellFromGlobal(%d,%d) reported out-of-bounds for tile=%v local=%v", gx, gy, tile, local)
				}
				if gotTile != tile || gotLocal != local {
					t.Fatalf("round-trip mismatch: tile=%v local=%v -> (%d,%d) -> tile=%v local=%v",
						tile, local, gx, gy, gotTile, gotLocal)
				}
			}
		}
	}
}

// nextStride advances a sweep index by stride but guarantees the FINAL index
// (limit-1) is always visited before the loop terminates -- the round-2
// coverage-gap fix: a bare +=7/+=11 stride never reached TileSizeCells-1,
// leaving the south/east edge cells untested by the round-trip sweep.
func nextStride(i, stride, limit int) int {
	if i < limit-1 && i+stride >= limit {
		return limit - 1
	}
	return i + stride
}
