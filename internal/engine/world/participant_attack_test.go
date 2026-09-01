package world

import (
	"testing"
)

// ---------------------------------------------------------------------------
// FEAT-1972079941 inc8 — independent Destructive-round regression tests
// (GR#23). These close two coverage gaps the builder's suite left open:
//
//  1. The owned-vs-query-only distinction is only asserted from the SAVE side
//     (a query-only tile is absent from the snapshot). Nothing proved the
//     LOAD side: that after a full round trip a purely-queried tile still
//     regenerates its terrain IDENTICALLY (it is pure derived state, so it
//     must not be lost by being omitted from the save) AND acquires no
//     spurious durable state.
//
//  2. Nothing asserted the snapshot's heightmap is a genuine DEEP COPY — that
//     mutating the live World.startHeight after a snapshot cannot corrupt the
//     bytes a still-streaming Source would emit (the doc comment claims a
//     deep copy; this pins it).
// ---------------------------------------------------------------------------

// TestWorldAttack_QueryOnlyTileRegeneratesAfterLoad proves that a tile which
// was only QUERIED before the save (terrain generated, never owned or
// prospected) is correctly NOT serialized, yet after loading the save into a
// FRESH world it regenerates byte-identical terrain on first query and carries
// NO durable state — the two halves of "query-only tiles have no durable
// state, so omitting them loses nothing" that the inc rests on.
func TestWorldAttack_QueryOnlyTileRegeneratesAfterLoad(t *testing.T) {
	const qx, qy = 20, 20 // the query-only coord injectRichWorld touches

	orig := freshW()
	injectRichWorld(t, orig)

	// Capture the query-only tile's terrain in the ORIGINAL (this is what a
	// correct load must reproduce).
	origCells, err := orig.TileCells(TileCoord{X: qx, Y: qy}, "q-orig")
	ckErrW(t, err)

	// It must be absent from the durable snapshot.
	snap, err := orig.w.snapshotForSave()
	ckErrW(t, err)
	for _, tw := range snap.tiles {
		if tw.Coord == (TileCoord{X: qx, Y: qy}) {
			t.Fatalf("query-only tile %v,%v was serialized -- it holds no durable state and must be skipped", qx, qy)
		}
	}

	root := saveIntoW(t, orig, "orig")
	reloaded := freshW()
	loadIntoW(t, root, reloaded, "reloaded")

	// After load into a FRESH world the tile is not in the map at all (it was
	// never saved) -- prove that.
	reloaded.w.mu.RLock()
	_, present := reloaded.w.tiles[TileCoord{X: qx, Y: qy}]
	reloaded.w.mu.RUnlock()
	if present {
		t.Fatalf("query-only tile unexpectedly present in the reloaded map before first query")
	}

	// First query regenerates its terrain -- and it must be IDENTICAL to the
	// original, cell for cell (pure fn of coord, so nothing was lost by not
	// saving it).
	reCells, err := reloaded.TileCells(TileCoord{X: qx, Y: qy}, "q-reld")
	ckErrW(t, err)
	if len(origCells) != len(reCells) {
		t.Fatalf("query-only cell count %d != %d", len(origCells), len(reCells))
	}
	for i := range origCells {
		if origCells[i] != reCells[i] {
			t.Fatalf("query-only tile cell %d differs after load:\n orig=%+v\n reld=%+v", i, origCells[i], reCells[i])
		}
	}

	// And it acquired NO durable state on regeneration: still unowned,
	// unprospected, no simGrid -- so it is still correctly excluded from a
	// subsequent save.
	reloaded.w.mu.RLock()
	tl := reloaded.w.tiles[TileCoord{X: qx, Y: qy}]
	owned, prospected, hasSim, ownerID := tl.owned, tl.prospected, tl.sim != nil, tl.ownerID
	reloaded.w.mu.RUnlock()
	if owned || prospected || hasSim || ownerID != 0 {
		t.Fatalf("query-only tile gained durable state after regeneration: owned=%v prospected=%v hasSim=%v ownerID=%d", owned, prospected, hasSim, ownerID)
	}
	snap2, err := reloaded.w.snapshotForSave()
	ckErrW(t, err)
	for _, tw := range snap2.tiles {
		if tw.Coord == (TileCoord{X: qx, Y: qy}) {
			t.Fatalf("query-only tile leaked into the reloaded world's durable snapshot after a query")
		}
	}
}

// TestWorldAttack_SnapshotHeightmapIsDeepCopy proves the snapshot's heightmap
// wire does not alias the live World.startHeight: after taking a snapshot, a
// mutation to the live map must NOT change the snapshot's captured bytes. If it
// aliased, a save streaming concurrently with any startHeight write (e.g. a
// mid-save re-import) would emit torn/wrong terrain.
func TestWorldAttack_SnapshotHeightmapIsDeepCopy(t *testing.T) {
	a := freshW()
	a.w.mu.Lock()
	a.w.startHeight = slopedHeights(TileSizeCells, 23)
	a.w.mu.Unlock()

	snap, err := a.w.snapshotForSave()
	ckErrW(t, err)

	// Independently record what the snapshot captured at a probe cell.
	before := snap.meta.StartHeight[100][100]

	// Mutate the LIVE map after the snapshot.
	a.w.mu.Lock()
	a.w.startHeight[100][100] += 1000
	a.w.mu.Unlock()

	if snap.meta.StartHeight[100][100] != before {
		t.Fatalf("snapshot heightmap ALIASES the live slice: probe cell changed from %v to %v after a live mutation", before, snap.meta.StartHeight[100][100])
	}

	// Also confirm the load-side install deep-copies: an applied meta must not
	// alias the wire it was given, so a later wire mutation cannot reach into
	// installed state.
	target := freshW()
	m := worldMetaWire{StartCoord: TileCoord{X: 1, Y: 2}, MilestoneTier: 3, StartHeight: slopedHeights(TileSizeCells, 71)}
	ckErrW(t, target.w.applyMetaRecord(m))
	probe := m.StartHeight[50][50]
	m.StartHeight[50][50] += 500
	target.w.mu.RLock()
	installed := target.w.startHeight[50][50]
	target.w.mu.RUnlock()
	if installed != probe {
		t.Fatalf("applyMetaRecord ALIASES the wire slice: installed cell changed to %v after mutating the source wire (was %v)", installed, probe)
	}
}
