package build

import (
	"fmt"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
)

// ---------------------------------------------------------------------------
// BUG-734 INDEPENDENT DESTRUCTIVE ROUND (attacker != author).
//
// Attacks CompletedBuildings' cursor contract under adversity: out-of-order
// completion, demolition, cursors past the end, save/restore, and a
// long-run exactly-once ledger.
// ---------------------------------------------------------------------------

// bug734Fixture returns a funded build fixture.
func bug734Fixture(t *testing.T) (*BuildAPI, *world.WorldAPI) {
	t.Helper()
	b, w, l := newBuildFixture(t)
	if _, err := l.Provision(DefaultDistrict, market.ConstructionMaterials, 100_000_000, 100_000_000); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	return b, w
}

// submitNamed submits one named catalogue order at (row,col) without
// ticking it.
func submitNamed(t *testing.T, b *BuildAPI, row, col int, buildingID string) BuildOrderID {
	t.Helper()
	id, err := b.SubmitBuildCommand(BuildCommand{
		Tile: tile00(), Local: world.CellLocal{Row: row, Col: col}, OwnerID: testOwner,
		Zone: ZoneDwelling, Month: 6, BuildingID: buildingID,
	})
	if err != nil {
		t.Fatalf("SubmitBuildCommand(%s at %d,%d): %v", buildingID, row, col, err)
	}
	return id
}

// --- 1. Exactly-once ledger over a long run --------------------------------

// TestAttackBUG734_ExactlyOnceLedgerOverLongRun drives 24 "months" of ticks
// while continuously enqueuing named orders, polling CompletedBuildings with
// a persisted cursor exactly the way the documented one-line hook does, and
// asserts every completion is delivered EXACTLY once — never skipped, never
// repeated — with the ledger proving the count matches the queue's own
// complete+named population at the end.
func TestAttackBUG734_ExactlyOnceLedgerOverLongRun(t *testing.T) {
	b, _ := bug734Fixture(t)

	// Cursor idiom updated per the lead's ruling on this round's own F1
	// follow-up: BuildOrder.ID stays the real submission id in EVERY
	// accessor (never overloaded), so the cursor this consumer tracks is
	// the MAXIMUM BuildOrder.CompletionSeq it has seen — never ID. This is
	// adapting the test to the corrected API (CompletionSeq is now its own
	// exported field), not weakening it: every other assertion below,
	// including the per-ID exactly-once accounting, is unchanged.
	seen := map[BuildOrderID]int{}
	var cursor uint64
	poll := func() {
		completions := b.CompletedBuildings(BuildOrderID(cursor))
		var prev uint64
		for i, c := range completions {
			if i > 0 && c.CompletionSeq <= prev {
				t.Fatalf("CompletedBuildings not strictly ascending: %d after %d", c.CompletionSeq, prev)
			}
			prev = c.CompletionSeq
			if c.CompletionSeq <= cursor {
				t.Fatalf("CompletedBuildings returned completionSeq %d at/below cursor %d", c.CompletionSeq, cursor)
			}
			seen[c.ID]++
			if seen[c.ID] > 1 {
				t.Fatalf("order %d delivered %d times (exactly-once violated)", c.ID, seen[c.ID])
			}
			if c.CompletionSeq > cursor {
				cursor = c.CompletionSeq
			}
		}
	}

	row, col := 0, 0
	next := func() (int, int) {
		r, c := row, col
		col++
		if col >= 16 {
			col = 0
			row++
		}
		return r, c
	}

	for month := int64(0); month < 24*30; month++ {
		if month%7 == 0 {
			r, c := next()
			submitNamed(t, b, r, c, fmt.Sprintf("named-%d", month))
		}
		if err := b.Tick(month); err != nil {
			t.Fatalf("Tick(%d): %v", month, err)
		}
		// Poll on an irregular cadence (some ticks polled twice, some not
		// at all) — a consumer's cursor must survive both.
		if month%3 == 0 {
			poll()
			poll() // second poll with the SAME cursor must yield nothing new
		}
	}
	poll()

	want := 0
	for _, o := range b.Queue() {
		if o.Status == OrderComplete && o.BuildingID != "" {
			want++
		}
	}
	if len(seen) != want {
		t.Fatalf("ledger saw %d completions, queue holds %d complete+named orders (skip/loss)", len(seen), want)
	}
	if want == 0 {
		t.Fatal("vacuous: no orders completed during the run")
	}
	t.Logf("exactly-once ledger: %d completions over 720 ticks, final cursor %d", len(seen), cursor)
}

// --- 2. Cursor edge cases --------------------------------------------------

func TestAttackBUG734_CursorEdges(t *testing.T) {
	b, _ := bug734Fixture(t)

	// Cursor 0 on a completely empty queue.
	if got := b.CompletedBuildings(0); len(got) != 0 {
		t.Fatalf("empty queue: CompletedBuildings(0) = %v, want empty", got)
	}
	// A never-negative cursor far past any minted id.
	if got := b.CompletedBuildings(1 << 40); len(got) != 0 {
		t.Fatalf("huge cursor on empty queue returned %v", got)
	}

	id := buildAndComplete(t, b, 0, 0, "cemetery")

	// Cursor exactly at the latest id — nothing new (strictly-after).
	if got := b.CompletedBuildings(id); len(got) != 0 {
		t.Fatalf("cursor==latest returned %v, want empty (must be STRICTLY after)", got)
	}
	// Cursor past the latest id.
	if got := b.CompletedBuildings(id + 100); len(got) != 0 {
		t.Fatalf("cursor past latest returned %v, want empty", got)
	}
	// Cursor below it — exactly one.
	if got := b.CompletedBuildings(id - 1); len(got) != 1 || got[0].ID != id {
		t.Fatalf("cursor==id-1 returned %v, want exactly order %d", got, id)
	}
	// BuildOrderID is UNSIGNED, so an underflowed cursor wraps to the max
	// value rather than going negative — a consumer that decrements a
	// zero-valued cursor silently loses EVERY completion forever. Pin the
	// shape so the hazard is documented rather than discovered in prod.
	var zero BuildOrderID
	if got := b.CompletedBuildings(zero - 1); len(got) != 0 {
		t.Fatalf("underflowed cursor returned %v; expected the (documented) total blackout", got)
	}
}

// --- 3. Out-of-order completion (different lead times) ---------------------

// TestAttackBUG734_OutOfOrderCompletionStillAscending enqueues a SLOW order
// before a FAST one so completion order != enqueue order, and proves the
// cursor feed still delivers each exactly once even though the later-id
// order completes first — a low-id order completing AFTER a high-id one has
// already advanced a REAL-ID-based cursor is the classic skip hazard this
// pins against; the fixed cursor tracks CompletionSeq, not ID.
func TestAttackBUG734_OutOfOrderCompletionStillAscending(t *testing.T) {
	// Two BuildAPIs are not needed: differing lead times come from the
	// per-zone catalogue, so use two different zones with different
	// baseLeadTimeDays (dwelling=45 vs farming=20 in the fixture).
	b, _, l := newBuildFixture(t)
	if _, err := l.Provision(DefaultDistrict, market.ConstructionMaterials, 10_000_000, 10_000_000); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	slow, err := b.SubmitBuildCommand(BuildCommand{
		Tile: tile00(), Local: world.CellLocal{Row: 0, Col: 0}, OwnerID: testOwner,
		Zone: ZoneHeavyIndustry, Month: 6, BuildingID: "slow-one", // lead 150
	})
	if err != nil {
		t.Fatalf("submit slow: %v", err)
	}
	fast, err := b.SubmitBuildCommand(BuildCommand{
		Tile: tile00(), Local: world.CellLocal{Row: 0, Col: 1}, OwnerID: testOwner,
		Zone: ZoneFarming, Month: 6, BuildingID: "fast-one", // lead 20
	})
	if err != nil {
		t.Fatalf("submit fast: %v", err)
	}
	if slow >= fast {
		t.Fatalf("precondition: slow id %d should be below fast id %d", slow, fast)
	}

	// Cursor idiom updated per the lead's ruling on this round's own F1
	// follow-up: BuildOrder.ID stays the real submission id in EVERY
	// accessor (never overloaded), so the cursor this consumer tracks is
	// the MAXIMUM BuildOrder.CompletionSeq it has seen — never ID. This is
	// adapting the test to the corrected API, not weakening it: every
	// other assertion below — the seen/sawFastFirst identity checks against
	// the REAL slow/fast submission ids, and the final exactly-once
	// check — is unchanged.
	seen := map[BuildOrderID]int{}
	var cursor uint64
	sawFastFirst := false
	for tick := int64(0); tick < 400; tick++ {
		if err := b.Tick(tick); err != nil {
			t.Fatalf("Tick(%d): %v", tick, err)
		}
		for _, c := range b.CompletedBuildings(BuildOrderID(cursor)) {
			seen[c.ID]++
			if c.ID == fast && seen[slow] == 0 {
				sawFastFirst = true
			}
			if c.CompletionSeq > cursor {
				cursor = c.CompletionSeq
			}
		}
	}
	if !sawFastFirst {
		t.Skip("fixture did not produce out-of-order completion; nothing to attack")
	}
	// Non-vacuity: the slow order really DID complete — it is not "missing
	// because it never finished".
	if st := orderByID(t, b.Queue(), slow).Status; st != OrderComplete {
		t.Fatalf("non-vacuity failed: slow order %d status = %s, never completed", slow, st)
	}
	if seen[slow] != 1 || seen[fast] != 1 {
		t.Fatalf("SKIP/DUPLICATE under out-of-order completion: slow(%d) delivered %d times, fast(%d) delivered %d times (a low-id order completing after the cursor advanced past it is LOST)",
			slow, seen[slow], fast, seen[fast])
	}
}

// --- 4. Demolition: a completion for a structure that no longer stands ----

// TestAttackBUG734_DemolishedCompletionStillReported is the attack against
// the feed's SEMANTICS, not its mechanics: engine.build's own
// registerCompletedServicesLocked filters completed orders through
// b.structures ("standing") precisely because a demolished order stays in
// b.queue forever with complete==true and its buildingID intact. If
// CompletedBuildings does NOT apply that same filter, a consumer polling
// after a build-then-demolish registers a service building that does not
// exist, permanently.
func TestAttackBUG734_DemolishedCompletionStillReported(t *testing.T) {
	b, _ := bug734Fixture(t)

	id := buildAndComplete(t, b, 0, 0, "cemetery")
	if _, ok := b.Structure(tile00(), world.CellLocal{Row: 0, Col: 0}); !ok {
		t.Fatalf("precondition: order %d should have a standing structure", id)
	}
	if _, err := b.SubmitDemolishCommand(DemolishCommand{
		Tile: tile00(), Local: world.CellLocal{Row: 0, Col: 0}, OwnerID: testOwner,
	}); err != nil {
		t.Fatalf("SubmitDemolishCommand: %v", err)
	}
	if _, ok := b.Structure(tile00(), world.CellLocal{Row: 0, Col: 0}); ok {
		t.Fatalf("precondition: structure should be gone after demolish")
	}

	got := b.CompletedBuildings(0)
	if len(got) != 0 {
		t.Fatalf("BUG-734 FINDING: CompletedBuildings(0) returned %d demolished completion(s) %+v — a consumer polling after a build-then-demolish registers a service building for a structure that no longer stands (engine.build's own registerCompletedServicesLocked filters by b.structures; this feed does not)", len(got), got)
	}
}

// --- 5. Save/restore ------------------------------------------------------

// TestAttackBUG734_CursorSurvivesSaveRestore proves ids and the cursor
// semantics survive a save/restore boundary, and that new orders enqueued
// after a restore never collide with restored ids.
func TestAttackBUG734_CursorSurvivesSaveRestore(t *testing.T) {
	orig, _ := bug734Fixture(t)
	a := buildAndComplete(t, orig, 0, 0, "cemetery")
	bID := buildAndComplete(t, orig, 0, 1, "crematorium")

	before := orig.CompletedBuildings(0)
	if len(before) != 2 {
		t.Fatalf("pre-save CompletedBuildings(0) = %d, want 2", len(before))
	}
	cursor := before[len(before)-1].ID

	root := saveInto(t, orig, "orig")
	reloaded, _, l2 := newBuildFixture(t)
	if _, err := l2.Provision(DefaultDistrict, market.ConstructionMaterials, 10_000_000, 10_000_000); err != nil {
		t.Fatalf("Provision (reloaded): %v", err)
	}
	loadInto(t, root, reloaded, "reloaded")

	after := reloaded.CompletedBuildings(0)
	if len(after) != 2 || after[0].ID != a || after[1].ID != bID {
		t.Fatalf("post-restore CompletedBuildings(0) = %+v, want ids [%d %d] in that order", after, a, bID)
	}
	for i := range after {
		if after[i].BuildingID != before[i].BuildingID {
			t.Fatalf("BuildingID lost across restore: %q -> %q", before[i].BuildingID, after[i].BuildingID)
		}
	}
	// The persisted cursor must still mean "nothing new".
	if got := reloaded.CompletedBuildings(cursor); len(got) != 0 {
		t.Fatalf("persisted cursor %d re-delivered %+v after restore (double-registration)", cursor, got)
	}

	// A new order after restore must mint an id ABOVE every restored id.
	newID := buildAndComplete(t, reloaded, 0, 2, "cemetery")
	if newID <= bID {
		t.Fatalf("ID COLLISION after restore: new order id %d <= restored max %d", newID, bID)
	}
	fresh := reloaded.CompletedBuildings(cursor)
	if len(fresh) != 1 || fresh[0].ID != newID {
		t.Fatalf("post-restore cursor query = %+v, want exactly the new order %d", fresh, newID)
	}
}

// TestAttackBUG734_LegacyOrderWithoutBuildingIDIsSkipped models a save
// written before BuildingID existed: the field decodes to "", and the feed
// must skip it silently rather than emitting an unnamed order a registry
// helper would then have to classify.
func TestAttackBUG734_LegacyOrderWithoutBuildingIDIsSkipped(t *testing.T) {
	orig, _ := bug734Fixture(t)
	// A completed order with NO BuildingID — byte-identical on the wire to a
	// legacy save (buildingID is `omitempty`).
	legacy, err := orig.SubmitBuildCommand(BuildCommand{
		Tile: tile00(), Local: world.CellLocal{Row: 0, Col: 0}, OwnerID: testOwner,
		Zone: ZoneFarming, Month: 6,
	})
	if err != nil {
		t.Fatalf("submit legacy: %v", err)
	}
	for i := int64(0); i < 200; i++ {
		if err := orig.Tick(i); err != nil {
			t.Fatalf("Tick: %v", err)
		}
	}
	if orderByID(t, orig.Queue(), legacy).Status != OrderComplete {
		t.Fatalf("precondition: legacy order never completed")
	}

	root := saveInto(t, orig, "orig")
	reloaded, _, _ := newBuildFixture(t)
	loadInto(t, root, reloaded, "reloaded")

	if got := reloaded.CompletedBuildings(0); len(got) != 0 {
		t.Fatalf("legacy (no BuildingID) completion leaked into the feed: %+v", got)
	}
	if got := orderByID(t, reloaded.Queue(), legacy); got.BuildingID != "" {
		t.Fatalf("legacy order BuildingID = %q, want empty", got.BuildingID)
	}
}

// --- 6. Determinism -------------------------------------------------------

// TestAttackBUG734_IdsDeterministicAcrossRuns runs the same command script
// twice against two independent BuildAPIs and requires identical ids,
// BuildingIDs and feed order (GR#21: no map iteration anywhere in the
// minting or query path).
func TestAttackBUG734_IdsDeterministicAcrossRuns(t *testing.T) {
	run := func() []BuildOrder {
		b, _ := bug734Fixture(t)
		names := []string{"cemetery", "crematorium", "corner_shop", "cemetery", "crematorium"}
		for i, n := range names {
			submitNamed(t, b, i/16, i%16, n)
		}
		for tick := int64(0); tick < 300; tick++ {
			if err := b.Tick(tick); err != nil {
				t.Fatalf("Tick: %v", err)
			}
		}
		return b.CompletedBuildings(0)
	}
	a, bb := run(), run()
	if len(a) == 0 {
		t.Fatal("vacuous: no completions")
	}
	if len(a) != len(bb) {
		t.Fatalf("nondeterministic length: %d vs %d", len(a), len(bb))
	}
	for i := range a {
		if a[i].ID != bb[i].ID || a[i].BuildingID != bb[i].BuildingID {
			t.Fatalf("nondeterministic at %d: %+v vs %+v", i, a[i], bb[i])
		}
	}
	// Repeat the query 50x on one instance — a map-backed query path would
	// eventually shuffle.
	b, _ := bug734Fixture(t)
	for i := 0; i < 5; i++ {
		submitNamed(t, b, 0, i, "cemetery")
	}
	for tick := int64(0); tick < 300; tick++ {
		if err := b.Tick(tick); err != nil {
			t.Fatalf("Tick: %v", err)
		}
	}
	first := b.CompletedBuildings(0)
	for n := 0; n < 50; n++ {
		got := b.CompletedBuildings(0)
		for i := range first {
			if got[i].ID != first[i].ID {
				t.Fatalf("query order shuffled on repeat %d: %d vs %d", n, got[i].ID, first[i].ID)
			}
		}
	}
}

// --- 7. Concurrency -------------------------------------------------------

// TestAttackBUG734_ConcurrentReadersUnderTick hammers CompletedBuildings
// from several goroutines while Tick mutates the queue — for -race.
func TestAttackBUG734_ConcurrentReadersUnderTick(t *testing.T) {
	b, _ := bug734Fixture(t)
	for i := 0; i < 8; i++ {
		submitNamed(t, b, 0, i, "cemetery")
	}
	done := make(chan struct{})
	for r := 0; r < 8; r++ {
		go func() {
			for {
				select {
				case <-done:
					return
				default:
					_ = b.CompletedBuildings(0)
				}
			}
		}()
	}
	for tick := int64(0); tick < 200; tick++ {
		if err := b.Tick(tick); err != nil {
			close(done)
			t.Fatalf("Tick: %v", err)
		}
	}
	close(done)
	if len(b.CompletedBuildings(0)) == 0 {
		t.Fatal("vacuous: nothing completed")
	}
}
