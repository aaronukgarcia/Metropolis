package world

import (
	"testing"
	"unsafe"
)

// FEAT-199 tests: per-cell zoning density storage. The command path is
// ApplyOwnershipCommand (the ONLY writer, AC-1); the read paths are
// CellAt and TileCells. The rejection case must prove NO field of the
// target cell mutated — a rejected command mutating no zone state.

func newOwnedTileForDensityTest(t *testing.T) *WorldAPI {
	t.Helper()
	a := NewWorldAPI(TileCoord{X: 15, Y: 13})
	if res := a.PurchaseTile(PurchaseCommand{CorrelationID: "corr-feat199", Tile: TileCoord{X: 15, Y: 13}, BuyerID: 7}); !res.Accepted {
		t.Fatalf("PurchaseTile rejected: %+v", res.Error)
	}
	return a
}

// TestZoningDensityRoundTrip: a command carrying NewZoning + NewDensity
// reads back identically through BOTH query paths (CellAt and TileCells),
// and a later command overwriting density to 0 clears it (repaintable).
func TestZoningDensityRoundTrip(t *testing.T) {
	a := newOwnedTileForDensityTest(t)
	tc := TileCoord{X: 15, Y: 13}
	local := CellLocal{Row: 3, Col: 4}

	if res := a.ApplyOwnershipCommand(OwnershipCommand{
		CorrelationID: "corr-feat199", Tile: tc, Local: local,
		NewOwner: 7, NewZoning: ZoningResidential, NewDensity: 3,
	}); !res.Accepted {
		t.Fatalf("ApplyOwnershipCommand(density=3) rejected: %+v", res.Error)
	}

	cell, err := a.CellAt(tc, local, "corr-feat199")
	if err != nil {
		t.Fatalf("CellAt: %v", err)
	}
	if cell.Zoning != ZoningResidential || cell.ZoningDensity != 3 {
		t.Errorf("CellAt = zoning %v density %d, want Residential/3", cell.Zoning, cell.ZoningDensity)
	}

	cells, err := a.TileCells(tc, "corr-feat199")
	if err != nil {
		t.Fatalf("TileCells: %v", err)
	}
	zoned := cells[localIndex(local.Col, local.Row)]
	if zoned.Zoning != ZoningResidential || zoned.ZoningDensity != 3 {
		t.Errorf("TileCells = zoning %v density %d, want Residential/3 (both read paths must agree)", zoned.Zoning, zoned.ZoningDensity)
	}

	// Repaint at a different level (FEAT-199 desc: repaintable later).
	if res := a.ApplyOwnershipCommand(OwnershipCommand{
		CorrelationID: "corr-feat199", Tile: tc, Local: local,
		NewOwner: 7, NewZoning: ZoningResidential, NewDensity: 5,
	}); !res.Accepted {
		t.Fatalf("ApplyOwnershipCommand(density=5) rejected: %+v", res.Error)
	}
	cell, err = a.CellAt(tc, local, "corr-feat199")
	if err != nil {
		t.Fatalf("CellAt after repaint: %v", err)
	}
	if cell.ZoningDensity != 5 {
		t.Errorf("density after repaint = %d, want 5", cell.ZoningDensity)
	}

	// Density 0 with ZoningNone clears both.
	if res := a.ApplyOwnershipCommand(OwnershipCommand{
		CorrelationID: "corr-feat199", Tile: tc, Local: local,
		NewOwner: 7, NewZoning: ZoningNone, NewDensity: 0,
	}); !res.Accepted {
		t.Fatalf("ApplyOwnershipCommand(clear) rejected: %+v", res.Error)
	}
	cell, err = a.CellAt(tc, local, "corr-feat199")
	if err != nil {
		t.Fatalf("CellAt after clear: %v", err)
	}
	if cell.Zoning != ZoningNone || cell.ZoningDensity != 0 {
		t.Errorf("cell after clear = zoning %v density %d, want None/0", cell.Zoning, cell.ZoningDensity)
	}
}

// TestApplyOwnershipCommandRejectsDensityAboveMax: a NewDensity above
// MaxZoningDensity rejects with MET-E407 and mutates NOTHING — not owner,
// not zoning, not density.
func TestApplyOwnershipCommandRejectsDensityAboveMax(t *testing.T) {
	a := newOwnedTileForDensityTest(t)
	tc := TileCoord{X: 15, Y: 13}
	local := CellLocal{Row: 1, Col: 2}

	// Baseline state to protect.
	if res := a.ApplyOwnershipCommand(OwnershipCommand{
		CorrelationID: "corr-feat199", Tile: tc, Local: local,
		NewOwner: 7, NewZoning: ZoningCommercial, NewDensity: 2,
	}); !res.Accepted {
		t.Fatalf("baseline command rejected: %+v", res.Error)
	}

	res := a.ApplyOwnershipCommand(OwnershipCommand{
		CorrelationID: "corr-feat199", Tile: tc, Local: local,
		NewOwner: 99, NewZoning: ZoningMining, NewDensity: MaxZoningDensity + 1,
	})
	if res.Accepted {
		t.Fatal("ApplyOwnershipCommand accepted density above MaxZoningDensity, want MET-E407 rejection")
	}
	if res.Error == nil || res.Error.Code != ErrZoningDensityOutOfRange {
		t.Fatalf("rejection code = %+v, want %s", res.Error, ErrZoningDensityOutOfRange)
	}

	cell, err := a.CellAt(tc, local, "corr-feat199")
	if err != nil {
		t.Fatalf("CellAt: %v", err)
	}
	if cell.Owner != 7 || cell.Zoning != ZoningCommercial || cell.ZoningDensity != 2 {
		t.Errorf("cell after rejected command = owner %d zoning %v density %d, want 7/Commercial/2 — a rejected command must mutate no zone state", cell.Owner, cell.Zoning, cell.ZoningDensity)
	}
}

// TestUnownedTileReadsZeroDensity: an unowned tile's sim storage does not
// exist; density (like every sim field) reads as its zero value honestly.
func TestUnownedTileReadsZeroDensity(t *testing.T) {
	a := NewWorldAPI(TileCoord{X: 15, Y: 13})
	tc := TileCoord{X: 14, Y: 13} // in extent, never purchased

	cell, err := a.CellAt(tc, CellLocal{Row: 0, Col: 0}, "corr-feat199")
	if err != nil {
		t.Fatalf("CellAt on unowned tile: %v", err)
	}
	if cell.ZoningDensity != 0 {
		t.Errorf("unowned tile density = %d, want 0", cell.ZoningDensity)
	}
}

// TestDensitySurvivesTerrainReimport extends BUG-062's preserved-state
// contract to the FEAT-199 byte: re-import refreshes terrain only; player
// zoning density lives on sim and must survive untouched.
func TestDensitySurvivesTerrainReimport(t *testing.T) {
	a := newOwnedTileForDensityTest(t)
	tc := TileCoord{X: 15, Y: 13}
	local := CellLocal{Row: 5, Col: 6}

	if res := a.ApplyOwnershipCommand(OwnershipCommand{
		CorrelationID: "corr-feat199", Tile: tc, Local: local,
		NewOwner: 7, NewZoning: ZoningOffice, NewDensity: 4,
	}); !res.Accepted {
		t.Fatalf("ApplyOwnershipCommand rejected: %+v", res.Error)
	}

	src := steepQualityFixture()
	if err := a.ImportAndPlaceStartTile(src, "corr-feat199"); err != nil {
		t.Fatalf("ImportAndPlaceStartTile: %v", err)
	}

	cell, err := a.CellAt(tc, local, "corr-feat199")
	if err != nil {
		t.Fatalf("CellAt after reimport: %v", err)
	}
	if cell.Zoning != ZoningOffice || cell.ZoningDensity != 4 {
		t.Errorf("cell after reimport = zoning %v density %d, want Office/4 — re-import preserves sim state (BUG-062 contract extended to density)", cell.Zoning, cell.ZoningDensity)
	}
}

// TestMemoryModelCountsDensityByte keeps memory_test.go's perCellSimBytes
// lower-bound honest: the FEAT-199 density byte is part of the SoA field
// list now, so the accounting must include it (GR#12's completeness
// discipline applied to the budget model itself). This recomputes the
// field-sum independently, WITH the density byte; if perCellSimBytes is
// ever left behind by a future SoA field addition, this catches the drift
// at fast-test speed.
func TestMemoryModelCountsDensityByte(t *testing.T) {
	var owner, structRef uint32
	var zoning Zoning
	var landValue float32
	var bytes [5]uint8 // traffic/utility/pollution/decay/zoningDensity

	want := unsafe.Sizeof(owner) + unsafe.Sizeof(zoning) + unsafe.Sizeof(structRef) +
		unsafe.Sizeof(landValue) + unsafe.Sizeof(bytes)
	if got := perCellSimBytes(); got != want {
		t.Fatalf("perCellSimBytes() = %d, want %d — the sim SoA field list and the memory model disagree (density byte missing from one of them?)", got, want)
	}
}
