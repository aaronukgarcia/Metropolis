package build

import (
	"reflect"
	"sort"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
)

// ---------------------------------------------------------------------------
// BUG-734 — completed-building identity + discovery.
//
// Covers: BuildOrder.BuildingID exposed on Queue(); CompletedBuildings'
// cursor contract (only-complete, only-named, strictly-after-cursor,
// deterministic order); determinism of the underlying BuildOrderID minting
// across identical runs and its survival across save/restore; idempotency of
// repeated CompletedBuildings calls with a non-advancing cursor (the shape a
// consumer's registration helper relies on to be replay-safe).
// ---------------------------------------------------------------------------

// buildAndComplete submits one build command naming buildingID (a plain
// catalogue-string carrier — no catalogue "entries" fixture is needed since
// BuildOrder.BuildingID/CompletedBuildings are agnostic to whether the name
// resolves to a real serviceKind entry) at the given cell, ticks it to
// completion, and returns its assigned BuildOrderID.
func buildAndComplete(t *testing.T, b *BuildAPI, row, col int, buildingID string) BuildOrderID {
	t.Helper()
	id, err := b.SubmitBuildCommand(BuildCommand{
		Tile: tile00(), Local: world.CellLocal{Row: row, Col: col}, OwnerID: testOwner,
		Zone: ZoneDwelling, Month: 6, BuildingID: buildingID,
	})
	if err != nil {
		t.Fatalf("SubmitBuildCommand(%s): %v", buildingID, err)
	}
	for i := int64(0); i < 100; i++ {
		if err := b.Tick(i); err != nil {
			t.Fatalf("Tick(%d): %v", i, err)
		}
		if orderByID(t, b.Queue(), id).Status == OrderComplete {
			return id
		}
	}
	t.Fatalf("order %d never completed", id)
	return 0
}

// --- BuildOrder.BuildingID exposure (the Queue()-side half of BUG-734) -----

func TestQueueExposesBuildingID(t *testing.T) {
	b, _, l := newBuildFixture(t)
	if _, err := l.Provision(DefaultDistrict, market.ConstructionMaterials, 1_000_000, 1_000_000); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	namedID := buildAndComplete(t, b, 0, 0, "cemetery")
	plainID, err := b.SubmitBuildCommand(BuildCommand{
		Tile: tile00(), Local: world.CellLocal{Row: 0, Col: 1}, OwnerID: testOwner,
		Zone: ZoneDwelling, Month: 6, // BuildingID left empty — the legacy zone-order case.
	})
	if err != nil {
		t.Fatalf("SubmitBuildCommand (plain): %v", err)
	}

	named := orderByID(t, b.Queue(), namedID)
	if named.BuildingID != "cemetery" {
		t.Fatalf("named order BuildingID = %q, want %q", named.BuildingID, "cemetery")
	}
	plain := orderByID(t, b.Queue(), plainID)
	if plain.BuildingID != "" {
		t.Fatalf("plain zone order BuildingID = %q, want empty", plain.BuildingID)
	}
}

// --- CompletedBuildings cursor contract -------------------------------------

func TestCompletedBuildings_OnlyCompleteAndNamed(t *testing.T) {
	b, _, l := newBuildFixture(t)
	if _, err := l.Provision(DefaultDistrict, market.ConstructionMaterials, 1_000_000, 1_000_000); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	cemeteryID := buildAndComplete(t, b, 0, 0, "cemetery")

	// A plain zone order (no BuildingID) — must never appear even once complete.
	plainID, err := b.SubmitBuildCommand(BuildCommand{
		Tile: tile00(), Local: world.CellLocal{Row: 0, Col: 1}, OwnerID: testOwner,
		Zone: ZoneDwelling, Month: 6,
	})
	if err != nil {
		t.Fatalf("SubmitBuildCommand (plain): %v", err)
	}
	for i := int64(0); i < 100; i++ {
		if err := b.Tick(i); err != nil {
			t.Fatalf("Tick(%d): %v", i, err)
		}
		if orderByID(t, b.Queue(), plainID).Status == OrderComplete {
			break
		}
	}

	// A named order still IN FLIGHT (materials never provisioned for a
	// second district so it never completes within this test).
	inFlightID, err := b.SubmitBuildCommand(BuildCommand{
		Tile: tile00(), Local: world.CellLocal{Row: 0, Col: 2}, OwnerID: testOwner,
		Zone: ZoneOffice, Month: 6, BuildingID: "crematorium",
	})
	if err != nil {
		t.Fatalf("SubmitBuildCommand (in-flight): %v", err)
	}
	_ = inFlightID

	got := b.CompletedBuildings(0)
	if len(got) != 1 {
		t.Fatalf("CompletedBuildings(0) = %d records, want 1 (only the completed cemetery): %+v", len(got), got)
	}
	if got[0].ID != cemeteryID || got[0].BuildingID != "cemetery" {
		t.Fatalf("CompletedBuildings(0)[0] = %+v, want ID=%d BuildingID=cemetery", got[0], cemeteryID)
	}

	// Cursor semantics: asking again with the SAME id as cursor returns
	// nothing new (strictly-after, not at-or-after) — this is what makes a
	// consumer's repeated poll idempotent.
	if got2 := b.CompletedBuildings(cemeteryID); len(got2) != 0 {
		t.Fatalf("CompletedBuildings(cemeteryID) = %+v, want empty (cursor already at the only completion)", got2)
	}

	// Complete the in-flight crematorium order by provisioning materials for
	// its district (DefaultDistrict, shared) and ticking further.
	for i := int64(100); i < 400; i++ {
		if err := b.Tick(i); err != nil {
			t.Fatalf("Tick(%d): %v", i, err)
		}
		if orderByID(t, b.Queue(), inFlightID).Status == OrderComplete {
			break
		}
	}
	final := orderByID(t, b.Queue(), inFlightID)
	if final.Status != OrderComplete {
		t.Fatalf("in-flight crematorium order never completed: %+v", final)
	}
	got3 := b.CompletedBuildings(cemeteryID)
	if len(got3) != 1 || got3[0].ID != inFlightID || got3[0].BuildingID != "crematorium" {
		t.Fatalf("CompletedBuildings(cemeteryID) after crematorium completes = %+v, want exactly the crematorium order", got3)
	}
}

// TestCompletedBuildings_DeterministicOrder proves the returned slice is in
// ascending BuildOrderID order (GR#21: the queue's own insertion order,
// never re-sorted from a map) across a batch of several named completions.
func TestCompletedBuildings_DeterministicOrder(t *testing.T) {
	b, _, l := newBuildFixture(t)
	if _, err := l.Provision(DefaultDistrict, market.ConstructionMaterials, 1_000_000, 1_000_000); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	names := []string{"cemetery", "crematorium", "cemetery", "crematorium", "cemetery"}
	var ids []BuildOrderID
	for i, name := range names {
		ids = append(ids, buildAndComplete(t, b, 0, i, name))
	}

	got := b.CompletedBuildings(0)
	if len(got) != len(ids) {
		t.Fatalf("CompletedBuildings(0) returned %d records, want %d", len(got), len(ids))
	}
	for i, rec := range got {
		if rec.ID != ids[i] || rec.BuildingID != names[i] {
			t.Fatalf("record %d = %+v, want ID=%d BuildingID=%s", i, rec, ids[i], names[i])
		}
	}
	if !sort.SliceIsSorted(got, func(i, j int) bool { return got[i].ID < got[j].ID }) {
		t.Fatalf("CompletedBuildings did not return ascending order-ID order: %+v", got)
	}
}

// --- Determinism of the underlying id minting -------------------------------

// TestBuildOrderIDsSequentialAndDeterministic pins the invariant BUG-734's
// design leans on: SubmitBuildCommand mints BuildOrderID strictly from the
// monotonic nextOrder counter, so N calls in a row ALWAYS produce 1..N in
// that exact order, regardless of the catalogue building named — never a
// value influenced by map iteration or any other non-deterministic source
// (GR#21). This is the test the brief's required mutation (id minted via a
// map range) is proven against — see this package's PR/round notes for the
// red run.
func TestBuildOrderIDsSequentialAndDeterministic(t *testing.T) {
	run := func() []BuildOrderID {
		b, _, l := newBuildFixture(t)
		if _, err := l.Provision(DefaultDistrict, market.ConstructionMaterials, 1_000_000, 1_000_000); err != nil {
			t.Fatalf("Provision: %v", err)
		}
		var ids []BuildOrderID
		for i := 0; i < 20; i++ {
			id, err := b.SubmitBuildCommand(BuildCommand{
				Tile: tile00(), Local: world.CellLocal{Row: i / 10, Col: i % 10}, OwnerID: testOwner,
				Zone: ZoneDwelling, Month: 6, BuildingID: "cemetery",
			})
			if err != nil {
				t.Fatalf("SubmitBuildCommand #%d: %v", i, err)
			}
			ids = append(ids, id)
		}
		return ids
	}

	a := run()
	c := run()
	if !reflect.DeepEqual(a, c) {
		t.Fatalf("two identical submission sequences produced different BuildOrderID sequences: %v vs %v", a, c)
	}
	for i, id := range a {
		if id != BuildOrderID(i+1) {
			t.Fatalf("BuildOrderID[%d] = %d, want %d (sequential from 1)", i, id, i+1)
		}
	}
}

// --- Save/restore: ids and BuildingID survive, and never collide post-load -

func TestCompletedBuildings_SurvivesSaveRestore(t *testing.T) {
	orig, _, l := newBuildFixture(t)
	if _, err := l.Provision(DefaultDistrict, market.ConstructionMaterials, 1_000_000, 1_000_000); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	cemeteryID := buildAndComplete(t, orig, 0, 0, "cemetery")

	root := saveInto(t, orig, "orig")

	reloaded, _, l2 := newBuildFixture(t)
	loadInto(t, root, reloaded, "reloaded")

	// The restored completion is discoverable identically post-load.
	got := reloaded.CompletedBuildings(0)
	if len(got) != 1 || got[0].ID != cemeteryID || got[0].BuildingID != "cemetery" {
		t.Fatalf("post-restore CompletedBuildings(0) = %+v, want the original cemetery completion (ID=%d)", got, cemeteryID)
	}

	// A NEW order submitted after restore must never collide with the
	// restored id — proves nextOrder itself round-tripped, not just the
	// queue contents.
	if _, err := l2.Provision(DefaultDistrict, market.ConstructionMaterials, 1_000_000, 1_000_000); err != nil {
		t.Fatalf("Provision (reloaded): %v", err)
	}
	newID := buildAndComplete(t, reloaded, 0, 1, "crematorium")
	if newID <= cemeteryID {
		t.Fatalf("post-restore new order id %d did not advance past the restored id %d — collision risk", newID, cemeteryID)
	}
	gotAfter := reloaded.CompletedBuildings(cemeteryID)
	if len(gotAfter) != 1 || gotAfter[0].ID != newID || gotAfter[0].BuildingID != "crematorium" {
		t.Fatalf("CompletedBuildings(cemeteryID) after a post-restore completion = %+v, want exactly the new crematorium order", gotAfter)
	}
}
