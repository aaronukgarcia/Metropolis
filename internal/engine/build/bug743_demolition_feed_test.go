package build

import (
	"reflect"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
)

// ---------------------------------------------------------------------------
// BUG-743 — the demolition feed: BuildAPI.DemolishedSince(sinceDemolitionSeq)
// and the demolitionSeq stamped by SubmitDemolishCommand.
//
// Covers: exactly-once delivery over a long run with save/load boundaries
// (including a build-then-demolish-before-any-poll shape, both feeds
// delivered in order); demolish-then-rebuild on the same tile producing
// DISTINCT records; determinism of the demolitionSeq axis across identical
// runs. Mirrors completedbuildings_test.go's own BUG-734 suite shape.
// ---------------------------------------------------------------------------

// demolishAt demolishes the structure at (row, col) in tile00, failing the
// test on any error.
func demolishAt(t *testing.T, b *BuildAPI, row, col int) DemolishResult {
	t.Helper()
	res, err := b.SubmitDemolishCommand(DemolishCommand{
		Tile: tile00(), Local: world.CellLocal{Row: row, Col: col}, OwnerID: testOwner,
	})
	if err != nil {
		t.Fatalf("SubmitDemolishCommand(%d,%d): %v", row, col, err)
	}
	return res
}

// --- Exactly-once across save/load boundaries -------------------------------

// TestDemolishedSince_ExactlyOnceAcrossSaveLoadBoundaries drives a long run
// (36 simulated ticks' worth of build/demolish activity) with THREE
// save/load boundaries in the middle, polling DemolishedSince after each
// boundary and advancing the cursor by the MAXIMUM DemolitionSeq returned
// (never by OrderID — the exact discipline CompletedBuildings' own doc
// requires). Every demolition must be delivered EXACTLY once, in ascending
// DemolitionSeq order, across the whole run.
func TestDemolishedSince_ExactlyOnceAcrossSaveLoadBoundaries(t *testing.T) {
	b, _, l := newBuildFixture(t)
	if _, err := l.Provision(DefaultDistrict, market.ConstructionMaterials, 10_000_000, 10_000_000); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	var allDelivered []Demolition
	var cursor BuildOrderID
	poll := func(cid string) {
		t.Helper()
		got := b.DemolishedSince(cursor)
		for _, d := range got {
			allDelivered = append(allDelivered, d)
			if BuildOrderID(d.DemolitionSeq) > cursor {
				cursor = BuildOrderID(d.DemolitionSeq)
			}
		}
	}

	// Round 1: two named orders, one demolished immediately after
	// completing — WITHOUT any prior poll of either feed (the
	// build-then-demolish-before-poll shape). Both CompletedBuildings and
	// DemolishedSince must still deliver it correctly once polled.
	id1 := buildAndComplete(t, b, 0, 0, "cemetery")
	id2 := buildAndComplete(t, b, 0, 1, "crematorium")
	demolishAt(t, b, 0, 0) // demolishes id1, before any CompletedBuildings/DemolishedSince poll

	completed := b.CompletedBuildings(0)
	if len(completed) != 1 || completed[0].ID != id2 {
		t.Fatalf("CompletedBuildings(0) after build-then-demolish-before-poll = %+v, want exactly id2 (id1 was demolished before it ever registered as a standing completion)", completed)
	}
	poll("r1")
	if len(allDelivered) != 1 || allDelivered[0].OrderID != id1 || allDelivered[0].BuildingID != "cemetery" {
		t.Fatalf("first poll = %+v, want exactly one Demolition record for id1", allDelivered)
	}

	// Save/load boundary 1.
	root1 := saveInto(t, b, "boundary1")
	b2, _, l2 := newBuildFixture(t)
	loadInto(t, root1, b2, "boundary1-reloaded")
	if _, err := l2.Provision(DefaultDistrict, market.ConstructionMaterials, 10_000_000, 10_000_000); err != nil {
		t.Fatalf("Provision (b2): %v", err)
	}
	b = b2

	// Round 2: demolish id2 (survived the boundary), then build+demolish a
	// third order.
	demolishAt(t, b, 0, 1) // demolishes id2
	id3 := buildAndComplete(t, b, 0, 2, "cemetery")
	demolishAt(t, b, 0, 2) // demolishes id3
	poll("r2")
	if len(allDelivered) != 3 {
		t.Fatalf("after round 2, delivered %d records, want 3: %+v", len(allDelivered), allDelivered)
	}

	// Save/load boundary 2.
	root2 := saveInto(t, b, "boundary2")
	b3, _, l3 := newBuildFixture(t)
	loadInto(t, root2, b3, "boundary2-reloaded")
	if _, err := l3.Provision(DefaultDistrict, market.ConstructionMaterials, 10_000_000, 10_000_000); err != nil {
		t.Fatalf("Provision (b3): %v", err)
	}
	b = b3

	// Poll again with NO new activity: idempotent, zero new records.
	poll("r2-repeat")
	if len(allDelivered) != 3 {
		t.Fatalf("idempotent repoll after boundary 2 added records: now %d, want still 3", len(allDelivered))
	}

	// Round 3: one more build+demolish cycle.
	id4 := buildAndComplete(t, b, 0, 3, "crematorium")
	demolishAt(t, b, 0, 3)
	poll("r3")
	if len(allDelivered) != 4 {
		t.Fatalf("after round 3, delivered %d records, want 4: %+v", len(allDelivered), allDelivered)
	}

	// Save/load boundary 3 — final poll after the last reload proves the
	// cursor discipline survives a third round-trip.
	root3 := saveInto(t, b, "boundary3")
	b4, _, _ := newBuildFixture(t)
	loadInto(t, root3, b4, "boundary3-reloaded")
	b = b4
	poll("r3-final")
	if len(allDelivered) != 4 {
		t.Fatalf("final poll after boundary 3 added records: now %d, want still 4 (idempotent)", len(allDelivered))
	}

	// Exactly-once + ascending order over the WHOLE run.
	wantOrder := []BuildOrderID{id1, id2, id3, id4}
	if len(allDelivered) != len(wantOrder) {
		t.Fatalf("total delivered = %d, want %d", len(allDelivered), len(wantOrder))
	}
	seen := map[BuildOrderID]int{}
	for i, d := range allDelivered {
		seen[d.OrderID]++
		if d.OrderID != wantOrder[i] {
			t.Fatalf("delivered[%d].OrderID = %d, want %d (out of order)", i, d.OrderID, wantOrder[i])
		}
		if i > 0 && allDelivered[i-1].DemolitionSeq >= d.DemolitionSeq {
			t.Fatalf("DemolitionSeq not strictly ascending at index %d: %d then %d", i, allDelivered[i-1].DemolitionSeq, d.DemolitionSeq)
		}
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("order %d delivered %d times, want exactly 1 (exactly-once violated)", id, n)
		}
	}
}

// --- Demolish-then-rebuild on the same tile: distinct records --------------

// TestDemolishedSince_DemolishThenRebuildSameTile_DistinctRecords proves a
// cell that is built, demolished, and then built again on the SAME
// tile/local cell produces TWO distinct Demolition records (distinct
// OrderID, distinct DemolitionSeq) when both structures are eventually
// demolished — the demolition feed never conflates two different structures
// that happened to occupy the same ground.
func TestDemolishedSince_DemolishThenRebuildSameTile_DistinctRecords(t *testing.T) {
	b, _, l := newBuildFixture(t)
	if _, err := l.Provision(DefaultDistrict, market.ConstructionMaterials, 10_000_000, 10_000_000); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	firstID := buildAndComplete(t, b, 0, 0, "cemetery")
	demolishAt(t, b, 0, 0)

	secondID := buildAndComplete(t, b, 0, 0, "cemetery") // same cell, new order
	if secondID == firstID {
		t.Fatalf("rebuild on the same cell reused the same BuildOrderID (%d) — ids must be distinct even on the same ground", firstID)
	}
	demolishAt(t, b, 0, 0)

	got := b.DemolishedSince(0)
	if len(got) != 2 {
		t.Fatalf("DemolishedSince(0) = %d records, want 2 (both demolitions on the same tile): %+v", len(got), got)
	}
	if got[0].OrderID != firstID || got[1].OrderID != secondID {
		t.Fatalf("DemolishedSince(0) OrderIDs = [%d, %d], want [%d, %d]", got[0].OrderID, got[1].OrderID, firstID, secondID)
	}
	if got[0].DemolitionSeq == got[1].DemolitionSeq {
		t.Fatalf("both demolitions share DemolitionSeq %d -- must be distinct", got[0].DemolitionSeq)
	}
	if got[0].Tile != got[1].Tile || got[0].Local != got[1].Local {
		t.Fatalf("demolition records disagree on the shared cell's coordinates: %+v vs %+v", got[0], got[1])
	}
}

// --- Determinism -------------------------------------------------------------

// TestDemolishedSince_DeterministicAcrossIdenticalRuns runs the identical
// deterministic build/demolish sequence twice from fresh fixtures and
// asserts the resulting DemolishedSince feeds are byte-for-byte identical
// (GR#21) -- mirrors TestBuildOrderIDsSequentialAndDeterministic's own
// two-run comparison for the completion axis.
func TestDemolishedSince_DeterministicAcrossIdenticalRuns(t *testing.T) {
	run := func() []Demolition {
		b, _, l := newBuildFixture(t)
		if _, err := l.Provision(DefaultDistrict, market.ConstructionMaterials, 10_000_000, 10_000_000); err != nil {
			t.Fatalf("Provision: %v", err)
		}
		names := []string{"cemetery", "crematorium", "cemetery", "crematorium", "cemetery"}
		for i, name := range names {
			buildAndComplete(t, b, 0, i, name)
			demolishAt(t, b, 0, i)
		}
		return b.DemolishedSince(0)
	}

	a := run()
	c := run()
	if !reflect.DeepEqual(a, c) {
		t.Fatalf("two identical build/demolish sequences produced different DemolishedSince feeds:\n a=%+v\n c=%+v", a, c)
	}
	if len(a) != 5 {
		t.Fatalf("test setup: got %d demolitions, want 5", len(a))
	}
	for i, d := range a {
		if d.DemolitionSeq != uint64(i+1) {
			t.Fatalf("DemolitionSeq[%d] = %d, want %d (sequential from 1)", i, d.DemolitionSeq, i+1)
		}
	}
}

// --- Cursor boundary: strictly-after, never at-or-after ---------------------

// TestDemolishedSince_CursorIsStrictlyAfter proves DemolishedSince(cursor)
// excludes a record whose DemolitionSeq equals cursor -- the exact
// exclusive-lower-bound contract CompletedBuildings itself documents. This
// is the property the required mutation (making the comparison inclusive)
// must break.
func TestDemolishedSince_CursorIsStrictlyAfter(t *testing.T) {
	b, _, l := newBuildFixture(t)
	if _, err := l.Provision(DefaultDistrict, market.ConstructionMaterials, 10_000_000, 10_000_000); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	buildAndComplete(t, b, 0, 0, "cemetery")
	demolishAt(t, b, 0, 0)

	all := b.DemolishedSince(0)
	if len(all) != 1 {
		t.Fatalf("setup: DemolishedSince(0) = %d records, want 1", len(all))
	}
	seq := BuildOrderID(all[0].DemolitionSeq)

	// Asking again with the cursor AT the just-returned seq must return
	// nothing new.
	if got := b.DemolishedSince(seq); len(got) != 0 {
		t.Fatalf("DemolishedSince(seq) = %+v, want empty (cursor already at the only demolition)", got)
	}
	// Asking with the cursor one BELOW must still return it.
	if got := b.DemolishedSince(seq - 1); len(got) != 1 {
		t.Fatalf("DemolishedSince(seq-1) = %+v, want the one record", got)
	}
}
