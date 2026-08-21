package world

import (
	"testing"
)

// BUG-323's proof set for TileCells (worldapi.go) — the bulk read the
// "f1.viewport" view publishes a whole tile through.
//
// The load-bearing property is EQUIVALENCE: TileCells is a performance
// shape, not a new contract, so anything it returns that CellAt would
// not return for the same coordinate is a bug. A fast accessor that
// quietly answers differently from the slow one it replaced is worse
// than the slow one.

// TestTileCells_MatchesCellAt_ForEveryCell walks the entire tile — all
// 40,000 cells, not a sample — comparing both accessors field for
// field. An off-by-one in the localIndex arithmetic (the one genuinely
// easy mistake in a row-major bulk copy) shows up here and nowhere else.
func TestTileCells_MatchesCellAt_ForEveryCell(t *testing.T) {
	coord := TileCoord{X: 15, Y: 15}
	a := NewWorldAPI(coord)

	bulk, err := a.TileCells(coord, "bug323-test")
	if err != nil {
		t.Fatalf("TileCells: %v", err)
	}
	if len(bulk) != CellsPerTile {
		t.Fatalf("TileCells returned %d cells, want %d", len(bulk), CellsPerTile)
	}

	for row := 0; row < TileSizeCells; row++ {
		for col := 0; col < TileSizeCells; col++ {
			want, err := a.CellAt(coord, CellLocal{Row: row, Col: col}, "bug323-test")
			if err != nil {
				t.Fatalf("CellAt(%d,%d): %v", col, row, err)
			}
			got := bulk[localIndex(col, row)]
			if got != want {
				t.Fatalf("TileCells[localIndex(%d,%d)] = %+v, CellAt says %+v — the bulk read disagrees with the per-cell read", col, row, got, want)
			}
		}
	}
}

// TestTileCells_ReflectsOwnedTileSimState proves the sim half of the
// Cell contract survives the bulk path: a purchased tile's landValue is
// seeded non-zero by PurchaseTile, and TileCells must show it. Without
// this, a bulk reader that only ever copied the terrain SoA would pass
// the equivalence test above on an unowned tile and silently drop every
// ownership/zoning/structure field on an owned one.
func TestTileCells_ReflectsOwnedTileSimState(t *testing.T) {
	coord := TileCoord{X: 15, Y: 15}
	a := NewWorldAPI(coord)

	before, err := a.TileCells(coord, "bug323-test")
	if err != nil {
		t.Fatalf("TileCells (unowned): %v", err)
	}
	if before[0].LandValue != 0 || before[0].Owner != 0 {
		t.Fatalf("unowned tile reports Owner=%d LandValue=%v, want both zero", before[0].Owner, before[0].LandValue)
	}

	if res := a.PurchaseTile(PurchaseCommand{CorrelationID: "bug323-test", Tile: coord, BuyerID: 7}); !res.Accepted {
		t.Fatalf("PurchaseTile rejected: %+v", res.Error)
	}
	if res := a.ApplyOwnershipCommand(OwnershipCommand{
		CorrelationID: "bug323-test", Tile: coord,
		Local: CellLocal{Row: 3, Col: 4}, NewOwner: 7, NewZoning: ZoningResidential,
	}); !res.Accepted {
		t.Fatalf("ApplyOwnershipCommand rejected: %+v", res.Error)
	}

	after, err := a.TileCells(coord, "bug323-test")
	if err != nil {
		t.Fatalf("TileCells (owned): %v", err)
	}
	if after[0].LandValue == 0 {
		t.Error("owned tile's cell 0 reports LandValue 0 — PurchaseTile seeds it non-zero, so the bulk read is dropping simGrid fields")
	}
	zoned := after[localIndex(4, 3)]
	if zoned.Owner != 7 || zoned.Zoning != ZoningResidential {
		t.Errorf("zoned cell (4,3) reads Owner=%d Zoning=%v, want 7/%v", zoned.Owner, zoned.Zoning, ZoningResidential)
	}
}

// TestTileCells_RejectsOutOfExtentTile pins the rejection path — an
// out-of-extent coordinate must return a registry-sourced error, never a
// zero-length slice a caller could mistake for "an empty tile".
func TestTileCells_RejectsOutOfExtentTile(t *testing.T) {
	a := NewWorldAPI(TileCoord{X: 15, Y: 15})
	got, err := a.TileCells(TileCoord{X: TilesPerSide, Y: 0}, "bug323-test")
	if err == nil {
		t.Fatalf("TileCells on an out-of-extent tile returned no error (and %d cells)", len(got))
	}
	if got != nil {
		t.Errorf("TileCells returned %d cells alongside its error, want nil", len(got))
	}
}

// The SEC-016/BUG-064 copy-guard case for TileCells lives with its
// eleven siblings in copyguard_test.go's
// TestBUG064_WorldAPI_CopiedWorldRejected enumeration, not here — that
// test is the single place this package asserts "every a.w.mu-touching
// method rejects a copied World", and splitting the twelfth out into its
// own file would be exactly the kind of drift that lets the thirteenth
// be forgotten.
