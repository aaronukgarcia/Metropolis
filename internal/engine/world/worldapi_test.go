package world

import (
	"strings"
	"testing"
)

// TestImportAndPlaceStartTilePreservesOwnershipAndProspecting is
// BUG-062's regression test: the exact Destructive-2 reproduction
// (purchase + prospect + zone the start tile, then re-import it — a
// legitimate replay per BUG-062's own description) must leave
// ownership/prospecting/per-cell owner+zoning UNCHANGED. Before the fix,
// ImportAndPlaceStartTile's delete(a.w.tiles, a.w.startCoord) destroyed
// the whole *tile struct, silently reverting all four to zero with no
// error returned; this test failed (red) against that code and passes
// (green) against ImportAndPlaceStartTile's partial-reset fix — see the
// dispatch report for the red-run transcript (the fix was reverted
// in-place, this test run, and the fix re-applied; not left reverted in
// the tree, since a live-broken worldapi.go would sabotage every other
// agent working in this tree right now).
func TestImportAndPlaceStartTilePreservesOwnershipAndProspecting(t *testing.T) {
	startCoord := TileCoord{15, 13}
	api := NewWorldAPI(startCoord)

	if err := api.ImportAndPlaceStartTile(a90x90Fixture(), "test-corr"); err != nil {
		t.Fatalf("first import: %v", err)
	}

	// The attacker's setup: purchase, prospect, and zone the start tile.
	if res := api.PurchaseTile(PurchaseCommand{CorrelationID: "test-corr", Tile: startCoord, BuyerID: 42}); !res.Accepted {
		t.Fatalf("purchase failed: %+v", res.Error)
	}
	if err := api.Prospect(startCoord, "test-corr"); err != nil {
		t.Fatalf("prospect failed: %v", err)
	}
	ownCmd := OwnershipCommand{
		CorrelationID: "test-corr", Tile: startCoord, Local: CellLocal{Row: 0, Col: 0},
		NewOwner: 42, NewZoning: ZoningResidential,
	}
	if res := api.ApplyOwnershipCommand(ownCmd); !res.Accepted {
		t.Fatalf("ownership command failed: %+v", res.Error)
	}

	// Sanity: confirm the baseline actually took, before attacking it.
	before, err := api.TileAt(startCoord, "test-corr")
	if err != nil || !before.Owned {
		t.Fatalf("setup check failed: expected start tile owned before reimport, got %+v err=%v", before, err)
	}
	if prospected, err := api.IsProspected(startCoord); err != nil || !prospected {
		t.Fatalf("setup check failed: expected start tile prospected before reimport, got prospected=%v err=%v", prospected, err)
	}
	cellBefore, err := api.CellAt(startCoord, CellLocal{Row: 0, Col: 0}, "test-corr")
	if err != nil || cellBefore.Owner != 42 || cellBefore.Zoning != ZoningResidential {
		t.Fatalf("setup check failed: expected cell(0,0) owner=42/zoning=Residential before reimport, got %+v err=%v", cellBefore, err)
	}

	// The attack: re-import the start tile, exactly as a retry/second
	// importer run/scenario re-run would.
	if err := api.ImportAndPlaceStartTile(a90x90Fixture(), "test-corr"); err != nil {
		t.Fatalf("second import: %v", err)
	}

	// BUG-062: none of the player's purchased/prospected/zoned state may
	// have been silently discarded by a terrain refresh.
	after, err := api.TileAt(startCoord, "test-corr")
	if err != nil || !after.Owned {
		t.Fatalf("BUG-062 regression: expected start tile still Owned after reimport, got %+v err=%v", after, err)
	}
	if after.OwnerID != 42 {
		t.Fatalf("BUG-062 regression: expected OwnerID to survive reimport as 42, got %d", after.OwnerID)
	}
	if prospected, err := api.IsProspected(startCoord); err != nil || !prospected {
		t.Fatalf("BUG-062 regression: expected start tile still IsProspected after reimport, got prospected=%v err=%v", prospected, err)
	}
	cellAfter, err := api.CellAt(startCoord, CellLocal{Row: 0, Col: 0}, "test-corr")
	if err != nil {
		t.Fatalf("CellAt after reimport: %v", err)
	}
	if cellAfter.Owner != 42 {
		t.Fatalf("BUG-062 regression: expected cell(0,0).Owner to survive reimport as 42, got %d", cellAfter.Owner)
	}
	if cellAfter.Zoning != ZoningResidential {
		t.Fatalf("BUG-062 regression: expected cell(0,0).Zoning to survive reimport as Residential, got %v", cellAfter.Zoning)
	}
}

// steepQualityFixture returns a small OS-Terrain-50-SHAPED fixture whose
// elevation ramps up by a full 50m per source post (matching the 50m
// native post spacing) north-to-south. After ImportTerrain's bilinear
// resample onto the 10m-spaced 200x200 output grid, adjacent output
// cells still differ by several metres, giving a slope grade far past
// classifySlope's (slope.go) 20% SlopeUnbuildable threshold for EVERY
// cell, which drives terrainQualityFactor (tile_price.go) to its
// documented floor for on-land terrain, 0.4 — the opposite extreme from
// a90x90Fixture's much gentler profile and, per this test's own
// self-check, provably different from whatever quality
// synthesizeTerrain's placeholder happened to produce for the start
// tile's coordinate.
func steepQualityFixture() *SourceGrid {
	const n = 10 // small: this fixture only needs to cover the sampled span, not realism
	src := fixtureAsciiGrid(n, n, 620000, 132500, 50, func(row, col int) float64 {
		return float64(row) * 50.0
	})
	sg, err := ParseAsciiGrid(strings.NewReader(src), "TR23-steep-fixture", "test-corr")
	if err != nil {
		panic(err)
	}
	return sg
}

// TestImportAndPlaceStartTileRecomputesLandValueForOwnedTile is BUG-066's
// regression test: the exact Destructive-3 reproduction. PurchaseTile
// seeds an owned tile's per-cell landValue from
// terrainQualityFactor(tile) (tile_price.go) at purchase time — here that
// happens BEFORE any real import, so landValue seeds from
// synthesizeTerrain's synthetic placeholder terrain (grid.go's
// ensureTile falls back to synthesizeTerrain because w.startHeight is
// still nil pre-import). Re-importing with real fixture terrain of
// GENUINELY different quality (steepQualityFixture's steep ramp, forced
// to terrainQualityFactor's 0.4 floor — see that helper's doc comment)
// must recompute landValue against the NEW terrain, never leave
// it stranded at the stale pre-reimport value. Before the fix, terrain
// (elevation/slope/surface) was refreshed by populateTerrainFromHeightmap
// but landValue was left untouched, silently mismatching the tile's
// actual terrain (repro: 1500.00 stale vs 1346.00 fresh in the
// Destructive-3 report); this test fails (red) against that code and
// passes (green) against ImportAndPlaceStartTile's landValue-recompute
// fix (worldapi.go, BUG-066 doc comment, ~line 107).
func TestImportAndPlaceStartTileRecomputesLandValueForOwnedTile(t *testing.T) {
	startCoord := TileCoord{15, 13}
	api := NewWorldAPI(startCoord)

	// Purchase BEFORE any real import: PurchaseTile's ensureTile call
	// synthesizes placeholder terrain (w.startHeight is nil pre-import),
	// so landValue seeds from that placeholder's terrainQualityFactor —
	// NOT from real (or fixture) Kent ground.
	if res := api.PurchaseTile(PurchaseCommand{CorrelationID: "test-corr", Tile: startCoord, BuyerID: 7}); !res.Accepted {
		t.Fatalf("purchase failed: %+v", res.Error)
	}

	// White-box (same package) reach into the tile struct: this test
	// needs to compute terrainQualityFactor directly, which has no public
	// WorldAPI accessor (deliberately — it is an internal pricing/
	// valuation detail, not part of the AC-1/AC-10 query surface).
	tl := api.w.tiles[startCoord]
	if tl == nil || tl.sim == nil {
		t.Fatalf("setup check failed: expected start tile allocated with sim after purchase, got %+v", tl)
	}
	staleQ := terrainQualityFactor(tl)
	staleLandValue := tl.sim.landValue[0]
	if float32(staleQ*1000) != staleLandValue {
		t.Fatalf("setup check failed: expected seeded landValue %v to equal placeholder terrainQualityFactor*1000 (%v)", staleLandValue, staleQ*1000)
	}
	for i, lv := range tl.sim.landValue {
		if lv != staleLandValue {
			t.Fatalf("setup check failed: expected uniform pre-reimport landValue, cell %d = %v want %v", i, lv, staleLandValue)
		}
	}

	// The attack: re-import the (already-owned) start tile with real
	// fixture terrain of deliberately different quality.
	if err := api.ImportAndPlaceStartTile(steepQualityFixture(), "test-corr"); err != nil {
		t.Fatalf("reimport: %v", err)
	}

	tl = api.w.tiles[startCoord]
	freshQ := terrainQualityFactor(tl)

	// Self-check: this test must not be able to pass vacuously. If the
	// fixture happened to produce the SAME quality as the placeholder,
	// a stale (unrecomputed) landValue would coincidentally match the
	// fresh one too, and the assertions below would pass whether or not
	// BUG-066's fix is present. Fail loudly instead of silently proving
	// nothing.
	if freshQ == staleQ {
		t.Fatalf("fixture setup invalid: flat fixture quality %v must differ from placeholder quality %v for this test to be able to fail", freshQ, staleQ)
	}

	wantLandValue := float32(freshQ * 1000)
	if wantLandValue == staleLandValue {
		t.Fatalf("fixture setup invalid: fresh landValue %v must differ from stale landValue %v for this test to be able to fail", wantLandValue, staleLandValue)
	}
	for i, lv := range tl.sim.landValue {
		if lv != wantLandValue {
			t.Fatalf("BUG-066 regression: cell %d landValue = %v, want %v (recomputed terrainQualityFactor against the NEW post-reimport terrain); found the stale pre-reimport value %v instead", i, lv, wantLandValue, staleLandValue)
		}
	}
}

// TestApplyOwnershipCommandRejectsAliasingCoordinate is BUG-063's
// regression test: the exact Destructive-2 reproduction. Local{Col:-200,
// Row:1} is individually far outside [0,TileSizeCells) but
// localIndex(-200,1) = 1*200+(-200) = 0 — the same composite index as
// the legitimate cell (0,0). Before the fix (bounds-checking only the
// composite idx), this command was ACCEPTED and silently overwrote cell
// (0,0)'s owner/zoning. After the fix (CellLocal.InBounds validates Col
// and Row individually before ever computing the composite index), the
// command must be REJECTED with ErrTileOutOfBounds AND cell (0,0) must
// be provably untouched.
func TestApplyOwnershipCommandRejectsAliasingCoordinate(t *testing.T) {
	if -TileSizeCells != -200 {
		t.Fatalf("test assumption broken: TileSizeCells changed from 200 (now %d) — update the aliasing coordinate below to stay -1*TileSizeCells", TileSizeCells)
	}

	api := NewWorldAPI(TileCoord{15, 13})
	tc := TileCoord{8, 8}
	if res := api.PurchaseTile(PurchaseCommand{CorrelationID: "test-corr", Tile: tc, BuyerID: 1}); !res.Accepted {
		t.Fatalf("purchase failed: %+v", res.Error)
	}

	// Establish a known baseline at the real cell (0,0) the aliasing
	// coordinate targets.
	baseline := OwnershipCommand{
		CorrelationID: "test-corr", Tile: tc, Local: CellLocal{Row: 0, Col: 0},
		NewOwner: 1, NewZoning: ZoningNone,
	}
	if res := api.ApplyOwnershipCommand(baseline); !res.Accepted {
		t.Fatalf("baseline ownership command failed: %+v", res.Error)
	}

	// Sanity: confirm baseline landed at (0,0) before attacking it.
	before, err := api.CellAt(tc, CellLocal{Row: 0, Col: 0}, "test-corr")
	if err != nil || before.Owner != 1 || before.Zoning != ZoningNone {
		t.Fatalf("setup check failed: expected cell(0,0) owner=1/zoning=None before attack, got %+v err=%v", before, err)
	}

	// The attack: a command naming a nominally out-of-range coordinate
	// that aliases (0,0) via localIndex's composite-only arithmetic.
	attack := OwnershipCommand{
		CorrelationID: "test-corr", Tile: tc, Local: CellLocal{Col: -TileSizeCells, Row: 1},
		NewOwner: 99, NewZoning: ZoningIndustrial,
	}
	result := api.ApplyOwnershipCommand(attack)

	// BUG-063: the aliasing command must be REJECTED, not accepted.
	if result.Accepted {
		t.Fatal("BUG-063 regression: expected the aliasing coordinate {Col:-200,Row:1} to be REJECTED, got Accepted")
	}
	if result.Error == nil || result.Error.Code != ErrTileOutOfBounds {
		t.Fatalf("BUG-063 regression: expected ErrTileOutOfBounds, got: %+v", result.Error)
	}

	// BUG-063: cell (0,0) must be provably UNTOUCHED by the rejected
	// command — this is the part that made the original bug worse than
	// a rejected no-op: it was a cross-cell WRITE.
	after, err := api.CellAt(tc, CellLocal{Row: 0, Col: 0}, "test-corr")
	if err != nil {
		t.Fatalf("CellAt after attack: %v", err)
	}
	if after.Owner != 1 || after.Zoning != ZoningNone {
		t.Fatalf("BUG-063 regression: expected cell(0,0) to remain owner=1/zoning=None after the rejected aliasing command, got owner=%d zoning=%v — cell (0,0) was corrupted by a coordinate that named a different cell", after.Owner, after.Zoning)
	}
}

// TestApplyOwnershipCommandRejectsAliasingCoordinate_ProvenFail: PROOF —
// a genuinely in-bounds command against the SAME tile (Col:0,Row:1, a
// real neighbouring cell, not an alias) must be ACCEPTED, confirming
// the rejection above is specifically about the aliasing coordinate's
// out-of-domain Col, not a command that always fails against this tile.
func TestApplyOwnershipCommandRejectsAliasingCoordinate_ProvenFail(t *testing.T) {
	api := NewWorldAPI(TileCoord{15, 13})
	tc := TileCoord{9, 9}
	if res := api.PurchaseTile(PurchaseCommand{CorrelationID: "test-corr", Tile: tc, BuyerID: 1}); !res.Accepted {
		t.Fatalf("purchase failed: %+v", res.Error)
	}
	legit := OwnershipCommand{
		CorrelationID: "test-corr", Tile: tc, Local: CellLocal{Col: 0, Row: 1},
		NewOwner: 99, NewZoning: ZoningIndustrial,
	}
	res := api.ApplyOwnershipCommand(legit)
	if !res.Accepted {
		t.Fatalf("sanity check failed: expected a genuinely in-bounds command to be accepted, got: %+v", res.Error)
	}
}

func TestUnownedTileQueryableButMutationRejected(t *testing.T) {
	api := NewWorldAPI(TileCoord{15, 13})
	tc := TileCoord{5, 5}

	info, err := api.TileAt(tc, "test-corr")
	if err != nil {
		t.Fatalf("TileAt on an unowned tile should succeed, got: %v", err)
	}
	if info.Owned {
		t.Fatal("expected a fresh tile to be unowned")
	}
	if info.Price <= 0 {
		t.Fatalf("expected a positive price for an unowned tile, got %.2f", info.Price)
	}

	_, err = api.CellAt(tc, CellLocal{Row: 0, Col: 0}, "test-corr")
	if err != nil {
		t.Fatalf("CellAt on an unowned tile should succeed (terrain-only), got: %v", err)
	}

	res := api.ApplyOwnershipCommand(OwnershipCommand{
		CorrelationID: "test-corr", Tile: tc, Local: CellLocal{Row: 0, Col: 0}, NewOwner: 1,
	})
	if res.Accepted {
		t.Fatal("expected a mutation command against an unowned tile to be rejected")
	}
	if res.Error == nil || res.Error.Code != ErrTileNotOwned {
		t.Fatalf("expected ErrTileNotOwned, got: %+v", res.Error)
	}
}

// TestUnownedTileQueryableButMutationRejected_ProvenFail: PROOF — after
// PurchaseTile succeeds, the SAME mutation command must be ACCEPTED,
// confirming the rejection above is genuinely ownership-gated rather
// than a command that always fails.
func TestUnownedTileQueryableButMutationRejected_ProvenFail(t *testing.T) {
	api := NewWorldAPI(TileCoord{15, 13})
	tc := TileCoord{6, 6}

	purchase := api.PurchaseTile(PurchaseCommand{CorrelationID: "test-corr", Tile: tc, BuyerID: 1})
	if !purchase.Accepted {
		t.Fatalf("expected purchase to succeed, got: %+v", purchase.Error)
	}
	res := api.ApplyOwnershipCommand(OwnershipCommand{
		CorrelationID: "test-corr", Tile: tc, Local: CellLocal{Row: 0, Col: 0}, NewOwner: 1,
	})
	if !res.Accepted {
		t.Fatalf("sanity check failed: expected mutation against an OWNED tile to be accepted, got: %+v", res.Error)
	}
}

func TestPurchaseTileRejectsAlreadyOwned(t *testing.T) {
	api := NewWorldAPI(TileCoord{15, 13})
	tc := TileCoord{7, 7}
	first := api.PurchaseTile(PurchaseCommand{CorrelationID: "test-corr", Tile: tc, BuyerID: 1})
	if !first.Accepted {
		t.Fatalf("expected first purchase to succeed, got: %+v", first.Error)
	}
	second := api.PurchaseTile(PurchaseCommand{CorrelationID: "test-corr", Tile: tc, BuyerID: 2})
	if second.Accepted {
		t.Fatal("expected a second purchase of the same tile to be rejected")
	}
	if second.Error == nil || second.Error.Code != ErrPurchaseRejected {
		t.Fatalf("expected ErrPurchaseRejected, got: %+v", second.Error)
	}
}

func TestMutationOutOfBoundsRejected(t *testing.T) {
	api := NewWorldAPI(TileCoord{15, 13})
	res := api.ApplyOwnershipCommand(OwnershipCommand{
		CorrelationID: "test-corr", Tile: TileCoord{X: 999, Y: 999}, Local: CellLocal{Row: 0, Col: 0}, NewOwner: 1,
	})
	if res.Accepted {
		t.Fatal("expected a mutation against an out-of-bounds tile to be rejected")
	}
	if res.Error == nil || res.Error.Code != ErrTileOutOfBounds {
		t.Fatalf("expected ErrTileOutOfBounds, got: %+v", res.Error)
	}
}

func TestTilePriceVariesByAdjacency(t *testing.T) {
	api := NewWorldAPI(TileCoord{15, 13})
	anchor := TileCoord{10, 10}
	if res := api.PurchaseTile(PurchaseCommand{CorrelationID: "test-corr", Tile: anchor, BuyerID: 1}); !res.Accepted {
		t.Fatalf("setup purchase failed: %+v", res.Error)
	}

	adjacent := TileCoord{11, 10} // borders the owned anchor tile
	distant := TileCoord{25, 25}  // does not border any owned tile

	priceAdjacent, err := api.TilePrice(adjacent, "test-corr")
	if err != nil {
		t.Fatalf("TilePrice(adjacent): %v", err)
	}
	priceDistant, err := api.TilePrice(distant, "test-corr")
	if err != nil {
		t.Fatalf("TilePrice(distant): %v", err)
	}
	if priceAdjacent <= priceDistant {
		t.Fatalf("expected adjacency premium: adjacent price (%.2f) should exceed a non-adjacent tile's base price for equivalent terrain (%.2f) — note this compares different terrain too, see the ProvenFail test for a controlled comparison", priceAdjacent, priceDistant)
	}
}

// TestTilePriceVariesByAdjacency_ProvenFail: PROOF — a controlled
// comparison of the SAME tile's price before and after a neighbour is
// purchased must show adjacency alone raising the price, isolating the
// adjacency factor from terrain-quality differences.
func TestTilePriceVariesByAdjacency_ProvenFail(t *testing.T) {
	api := NewWorldAPI(TileCoord{15, 13})
	target := TileCoord{20, 20}
	neighbour := TileCoord{21, 20}

	priceBefore, err := api.TilePrice(target, "test-corr")
	if err != nil {
		t.Fatalf("TilePrice before: %v", err)
	}
	if res := api.PurchaseTile(PurchaseCommand{CorrelationID: "test-corr", Tile: neighbour, BuyerID: 1}); !res.Accepted {
		t.Fatalf("neighbour purchase failed: %+v", res.Error)
	}
	priceAfter, err := api.TilePrice(target, "test-corr")
	if err != nil {
		t.Fatalf("TilePrice after: %v", err)
	}
	if priceAfter <= priceBefore {
		t.Fatalf("sanity check failed: expected price to rise once a neighbour is owned (before=%.2f after=%.2f)", priceBefore, priceAfter)
	}
}
