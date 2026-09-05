package build

import (
	"encoding/json"
	"fmt"
	"runtime"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// ---------------------------------------------------------------------------
// BUG-734 independent re-round 2 (attacker: opus-reround2-bug734).
// Attacks completionSeq monotonicity/uniqueness, the legacy backfill's
// collision surface, exactly-once across MULTIPLE save/load boundaries,
// demolish+rebuild-same-tile, and pool-size determinism.
// ---------------------------------------------------------------------------

// rr2Fixture provides a heavily provisioned build API.
func rr2Fixture(t *testing.T) (*BuildAPI, *world.WorldAPI, string) {
	t.Helper()
	b, w, l := newBuildFixture(t)
	if _, err := l.Provision(DefaultDistrict, market.ConstructionMaterials, 1_000_000_000, 1_000_000_000); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	return b, w, "rr2"
}

// rr2AllSeqs returns every queue order's (id, completionSeq) pair.
func rr2AllSeqs(b *BuildAPI) map[BuildOrderID]BuildOrderID {
	out := map[BuildOrderID]BuildOrderID{}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, o := range b.queue {
		out[o.id] = o.completionSeq
	}
	return out
}

// --- RR2-A: same-tick multi-completion uniqueness + determinism ------------

// TestRR2_SameTickCompletionsUniqueAndInsertionOrdered submits N identical
// orders on the same tick so they all satisfy their completion predicate on
// the SAME Tick call, then proves every one got a distinct, strictly
// increasing completionSeq and that the assignment follows the queue's own
// insertion (submission-id) order — the only deterministic tie-break
// available inside one tick.
func TestRR2_SameTickCompletionsUniqueAndInsertionOrdered(t *testing.T) {
	const n = 8
	seen := make([]string, 0, 3)
	for run := 0; run < 3; run++ {
		b, _, _ := rr2Fixture(t)
		ids := make([]BuildOrderID, 0, n)
		for i := 0; i < n; i++ {
			ids = append(ids, submitNamed(t, b, 0, i, "cemetery"))
		}
		for tick := int64(0); tick < 100; tick++ {
			if err := b.Tick(tick); err != nil {
				t.Fatalf("Tick(%d): %v", tick, err)
			}
			if orderByID(t, b.Queue(), ids[n-1]).Status == OrderComplete {
				break
			}
		}
		seqs := rr2AllSeqs(b)
		uniq := map[BuildOrderID]bool{}
		for i, id := range ids {
			s := seqs[id]
			if s == 0 {
				t.Fatalf("order %d never stamped a completionSeq", id)
			}
			if uniq[s] {
				t.Fatalf("DUPLICATE completionSeq %d (order %d)", s, id)
			}
			uniq[s] = true
			if i > 0 && s <= seqs[ids[i-1]] {
				t.Fatalf("completionSeq not insertion-ordered: order %d seq %d after order %d seq %d",
					id, s, ids[i-1], seqs[ids[i-1]])
			}
		}
		got := b.CompletedBuildings(0)
		if len(got) != n {
			t.Fatalf("CompletedBuildings(0) = %d, want %d", len(got), n)
		}
		sig := ""
		for _, c := range got {
			sig += fmt.Sprintf("%d:%d;", c.ID, c.CompletionSeq)
		}
		seen = append(seen, sig)
	}
	for i := 1; i < len(seen); i++ {
		if seen[i] != seen[0] {
			t.Fatalf("non-deterministic across runs:\n run0 %s\n run%d %s", seen[0], i, seen[i])
		}
	}
}

// --- RR2-B: legacy backfill cannot collide with a fresh completion ---------

// rr2LoadLegacy installs a hand-built LEGACY save shape into b: a build.meta
// with NO nextCompletionSeq field at all and build.order records with NO
// completionSeq field — exactly what a bundle written before BUG-734's fix
// decodes to. Uses the package-internal record path so the bytes are the
// real wire shape, not a mocked one.
func rr2LoadLegacy(t *testing.T, b *BuildAPI, completeIDs []BuildOrderID, nextOrder BuildOrderID) {
	t.Helper()
	if err := b.resetForLoad(); err != nil {
		t.Fatalf("resetForLoad: %v", err)
	}
	meta := fmt.Sprintf(`{"district":%q,"nextOrder":%d}`, DefaultDistrict, nextOrder)
	if err := b.applyLoadRecord(serialize.Record{Kind: recBuildMeta, Data: []byte(meta)}); err != nil {
		t.Fatalf("applyLoadRecord(meta): %v", err)
	}
	for i, id := range completeIDs {
		ord := fmt.Sprintf(`{"id":%d,"tile":{"x":0,"y":0},"local":{"row":0,"col":%d},"zone":%q,`+
			`"buildingID":"cemetery","materialsTotal":0,"materialsRemaining":0,"materialsDrawn":0,`+
			`"labourRemaining":0,"leadTimeRemaining":0,"complete":true}`, id, i, ZoneDwelling)
		if err := b.applyLoadRecord(serialize.Record{Kind: recBuildOrder, Data: []byte(ord)}); err != nil {
			t.Fatalf("applyLoadRecord(order %d): %v", id, err)
		}
		st := fmt.Sprintf(`{"tile":{"x":0,"y":0},"local":{"row":0,"col":%d},"orderID":%d}`, i, id)
		if err := b.applyLoadRecord(serialize.Record{Kind: recBuildStructure, Data: []byte(st)}); err != nil {
			t.Fatalf("applyLoadRecord(structure %d): %v", id, err)
		}
	}
	// Sanity: the legacy shape really did decode to zero seqs.
	for _, s := range rr2AllSeqs(b) {
		if s != 0 {
			t.Fatalf("legacy fixture did not decode to completionSeq 0 (got %d)", s)
		}
	}
}

func TestRR2_LegacyBackfillNeverCollidesWithFreshCompletions(t *testing.T) {
	b, _, _ := rr2Fixture(t)
	legacy := []BuildOrderID{7, 3, 11, 5}
	rr2LoadLegacy(t, b, legacy, 20)

	// Poll BEFORE any sweep: a legacy complete order carries seq 0 which the
	// strictly-greater filter excludes, so nothing is delivered yet — this
	// must be a DEFERRAL, never a permanent loss.
	if got := b.CompletedBuildings(0); len(got) != 0 {
		t.Fatalf("pre-sweep CompletedBuildings(0) = %d, want 0 (seq-0 orders deferred): %+v", len(got), got)
	}

	// The compose.Load path: explicit sweep, then keep building.
	if err := b.RegisterCompletedServices(); err != nil {
		t.Fatalf("RegisterCompletedServices: %v", err)
	}
	backfilled := rr2AllSeqs(b)
	seen := map[BuildOrderID]BuildOrderID{}
	for _, id := range legacy {
		s := backfilled[id]
		if s == 0 {
			t.Fatalf("legacy order %d not backfilled", id)
		}
		if prev, dup := seen[s]; dup {
			t.Fatalf("backfill COLLISION: orders %d and %d both got seq %d", prev, id, s)
		}
		seen[s] = id
	}
	// Backfill must follow queue insertion order (7,3,11,5), not id order.
	for i := 1; i < len(legacy); i++ {
		if backfilled[legacy[i]] <= backfilled[legacy[i-1]] {
			t.Fatalf("backfill not in queue insertion order: %v", backfilled)
		}
	}

	// Now complete a FRESH order and prove its seq cannot equal a backfilled one.
	fresh := submitNamed(t, b, 1, 0, "crematorium")
	for tick := int64(0); tick < 100; tick++ {
		if err := b.Tick(tick); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		if orderByID(t, b.Queue(), fresh).Status == OrderComplete {
			break
		}
	}
	freshSeq := rr2AllSeqs(b)[fresh]
	if freshSeq == 0 {
		t.Fatal("fresh order never completed")
	}
	if id, dup := seen[freshSeq]; dup {
		t.Fatalf("FRESH completion seq %d COLLIDES with backfilled legacy order %d", freshSeq, id)
	}
	// A cursor-following consumer sees all five, exactly once, ascending.
	var cursor BuildOrderID
	delivered := map[BuildOrderID]int{}
	for round := 0; round < 3; round++ {
		for _, c := range b.CompletedBuildings(cursor) {
			delivered[c.ID]++
			if BuildOrderID(c.CompletionSeq) > cursor {
				cursor = BuildOrderID(c.CompletionSeq)
			}
		}
	}
	if len(delivered) != 5 {
		t.Fatalf("delivered %d distinct orders, want 5: %v", len(delivered), delivered)
	}
	for id, n := range delivered {
		if n != 1 {
			t.Fatalf("order %d delivered %d times, want exactly once", id, n)
		}
	}
}

// TestRR2_LegacyBackfillWithoutSweepIsNotLostAcrossTick proves the deferral
// is healed by a plain Tick too (the sweep is dirty-gated; a caller that
// never calls RegisterCompletedServices must still not lose the legacy
// estate).
func TestRR2_LegacyBackfillWithoutSweepIsNotLostAcrossTick(t *testing.T) {
	b, _, _ := rr2Fixture(t)
	rr2LoadLegacy(t, b, []BuildOrderID{2, 9}, 30)
	if err := b.Tick(0); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	got := b.CompletedBuildings(0)
	if len(got) != 2 {
		t.Fatalf("post-Tick CompletedBuildings(0) = %d, want 2: %+v", len(got), got)
	}
	if got[0].CompletionSeq == 0 || got[1].CompletionSeq <= got[0].CompletionSeq {
		t.Fatalf("backfilled seqs not strictly ascending non-zero: %+v", got)
	}
}

// --- RR2-C: exactly-once across FOUR persistence boundaries ---------------

func TestRR2_ExactlyOnceAcrossFourSaveLoadBoundaries(t *testing.T) {
	b, _, _ := rr2Fixture(t)
	var cursor BuildOrderID
	delivered := map[BuildOrderID]int{}
	poll := func() {
		for _, c := range b.CompletedBuildings(cursor) {
			delivered[c.ID]++
			if BuildOrderID(c.CompletionSeq) > cursor {
				cursor = BuildOrderID(c.CompletionSeq)
			}
		}
	}
	submitted := 0
	col := 0
	// 36 "months": submit continuously, poll every tick, save/load at 4 points.
	boundaries := map[int]bool{7: true, 15: true, 23: true, 31: true}
	for month := 0; month < 36; month++ {
		for k := 0; k < 2; k++ {
			submitNamed(t, b, month%40, col%40, "cemetery")
			col++
			submitted++
		}
		for d := int64(0); d < 30; d++ {
			if err := b.Tick(int64(month)*30 + d); err != nil {
				t.Fatalf("Tick: %v", err)
			}
		}
		poll()
		if boundaries[month] {
			root := saveInto(t, b, "rr2")
			fresh, _, l2 := newBuildFixture(t)
			if _, err := l2.Provision(DefaultDistrict, market.ConstructionMaterials, 1_000_000_000, 1_000_000_000); err != nil {
				t.Fatalf("Provision: %v", err)
			}
			loadInto(t, root, fresh, "rr2")
			if err := fresh.RegisterCompletedServices(); err != nil {
				t.Fatalf("post-load sweep: %v", err)
			}
			b = fresh
			poll() // an immediate post-restore poll must not redeliver
		}
	}
	poll()
	if len(delivered) == 0 {
		t.Fatal("nothing delivered at all — vacuous")
	}
	for id, n := range delivered {
		if n != 1 {
			t.Fatalf("order %d delivered %d times across boundaries, want exactly once", id, n)
		}
	}
	// Every complete+standing named order in the FINAL state must have been
	// delivered (no loss).
	final := b.CompletedBuildings(0)
	for _, c := range final {
		if delivered[c.ID] != 1 {
			t.Fatalf("order %d (seq %d) present in final state but delivered %d times — LOST",
				c.ID, c.CompletionSeq, delivered[c.ID])
		}
	}
	if len(final) < 10 {
		t.Fatalf("only %d completions in 36 months — fixture too weak to be a real attack", len(final))
	}
	t.Logf("submitted=%d delivered=%d finalStanding=%d cursor=%d", submitted, len(delivered), len(final), cursor)
}

// --- RR2-D: demolish then REBUILD the same tile before the consumer polls --

func TestRR2_DemolishThenRebuildSameTileBeforePoll(t *testing.T) {
	b, _, _ := rr2Fixture(t)
	first := submitNamed(t, b, 2, 2, "cemetery")
	for tick := int64(0); tick < 100; tick++ {
		if err := b.Tick(tick); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		if orderByID(t, b.Queue(), first).Status == OrderComplete {
			break
		}
	}
	firstSeq := rr2AllSeqs(b)[first]
	// Demolish BEFORE the consumer ever polls.
	if _, err := b.SubmitDemolishCommand(DemolishCommand{
		Tile: tile00(), Local: world.CellLocal{Row: 2, Col: 2}, OwnerID: testOwner,
	}); err != nil {
		t.Fatalf("SubmitDemolishCommand: %v", err)
	}
	if got := b.CompletedBuildings(0); len(got) != 0 {
		t.Fatalf("demolished-before-poll order still delivered: %+v", got)
	}
	// Rebuild the SAME tile.
	second := submitNamed(t, b, 2, 2, "crematorium")
	for tick := int64(100); tick < 250; tick++ {
		if err := b.Tick(tick); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		if orderByID(t, b.Queue(), second).Status == OrderComplete {
			break
		}
	}
	got := b.CompletedBuildings(0)
	if len(got) != 1 {
		t.Fatalf("after rebuild CompletedBuildings(0) = %d, want exactly 1 (the new order): %+v", len(got), got)
	}
	if got[0].ID != second {
		t.Fatalf("delivered order %d, want the REBUILT order %d", got[0].ID, second)
	}
	if BuildOrderID(got[0].CompletionSeq) <= firstSeq {
		t.Fatalf("rebuilt order seq %d not above the demolished one's %d", got[0].CompletionSeq, firstSeq)
	}
	if got[0].BuildingID != "crematorium" {
		t.Fatalf("delivered BuildingID %q, want crematorium", got[0].BuildingID)
	}
}

// TestRR2_DemolishAfterDeliveryEmitsNothing pins that a demolition of an
// ALREADY-delivered order produces no further stream event at all — the
// documented consequence being that a consumer holding a registration has
// no way to learn the building is gone (reported as a P2, not a reject).
func TestRR2_DemolishAfterDeliveryEmitsNothing(t *testing.T) {
	b, _, _ := rr2Fixture(t)
	id := submitNamed(t, b, 3, 3, "cemetery")
	for tick := int64(0); tick < 100; tick++ {
		if err := b.Tick(tick); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		if orderByID(t, b.Queue(), id).Status == OrderComplete {
			break
		}
	}
	var cursor BuildOrderID
	got := b.CompletedBuildings(cursor)
	if len(got) != 1 {
		t.Fatalf("want 1 delivery, got %d", len(got))
	}
	cursor = BuildOrderID(got[0].CompletionSeq)
	if _, err := b.SubmitDemolishCommand(DemolishCommand{
		Tile: tile00(), Local: world.CellLocal{Row: 3, Col: 3}, OwnerID: testOwner,
	}); err != nil {
		t.Fatalf("SubmitDemolishCommand: %v", err)
	}
	for tick := int64(100); tick < 130; tick++ {
		if err := b.Tick(tick); err != nil {
			t.Fatalf("Tick: %v", err)
		}
	}
	if again := b.CompletedBuildings(cursor); len(again) != 0 {
		t.Fatalf("demolition re-emitted through the completion stream: %+v", again)
	}
	// And the whole-history view no longer shows it — the consumer's only
	// (polling, non-streaming) route to noticing the loss.
	if all := b.CompletedBuildings(0); len(all) != 0 {
		t.Fatalf("demolished order still in CompletedBuildings(0): %+v", all)
	}
}

// --- RR2-E: pool-size / GOMAXPROCS determinism -----------------------------

func TestRR2_DeterministicAcrossPoolSizes(t *testing.T) {
	orig := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(orig)
	sigs := map[int]string{}
	for _, procs := range []int{1, 4, 20} {
		runtime.GOMAXPROCS(procs)
		b, _, _ := rr2Fixture(t)
		for i := 0; i < 12; i++ {
			kind := "cemetery"
			if i%3 == 0 {
				kind = "crematorium"
			}
			submitNamed(t, b, i/6, i%6, kind)
		}
		for tick := int64(0); tick < 200; tick++ {
			if err := b.Tick(tick); err != nil {
				t.Fatalf("Tick: %v", err)
			}
		}
		// Save/restore in the middle to fold the backfill path in too.
		root := saveInto(t, b, "rr2")
		fresh, _, l2 := newBuildFixture(t)
		if _, err := l2.Provision(DefaultDistrict, market.ConstructionMaterials, 1_000_000_000, 1_000_000_000); err != nil {
			t.Fatalf("Provision: %v", err)
		}
		loadInto(t, root, fresh, "rr2")
		if err := fresh.RegisterCompletedServices(); err != nil {
			t.Fatalf("sweep: %v", err)
		}
		sig := ""
		for _, c := range fresh.CompletedBuildings(0) {
			sig += fmt.Sprintf("%d/%d/%s;", c.ID, c.CompletionSeq, c.BuildingID)
		}
		if sig == "" {
			t.Fatal("vacuous: no completions")
		}
		sigs[procs] = sig
	}
	if sigs[1] != sigs[4] || sigs[1] != sigs[20] {
		t.Fatalf("pool-size dependent output:\n p1  %s\n p4  %s\n p20 %s", sigs[1], sigs[4], sigs[20])
	}
}

// --- RR2-F: corrupt/mixed save (meta seq behind an order's seq) -----------

// TestRR2_MixedSaveMetaBehindOrderSeq probes decode-time sanitisation: a
// bundle whose meta claims nextCompletionSeq below an order's own recorded
// completionSeq. Not reachable from this package's own writer (documented),
// so this test RECORDS the behaviour rather than asserting a fix.
func TestRR2_MixedSaveMetaBehindOrderSeq(t *testing.T) {
	b, _, _ := rr2Fixture(t)
	if err := b.resetForLoad(); err != nil {
		t.Fatalf("resetForLoad: %v", err)
	}
	meta, _ := json.Marshal(map[string]any{"district": DefaultDistrict, "nextOrder": 50, "nextCompletionSeq": 0})
	if err := b.applyLoadRecord(serialize.Record{Kind: recBuildMeta, Data: meta}); err != nil {
		t.Fatalf("meta: %v", err)
	}
	// order 1: a real seq 1; order 2: legacy zero.
	o1 := `{"id":1,"tile":{"x":0,"y":0},"local":{"row":0,"col":0},"zone":"dwelling","buildingID":"cemetery","materialsTotal":0,"materialsRemaining":0,"materialsDrawn":0,"labourRemaining":0,"leadTimeRemaining":0,"complete":true,"completionSeq":1}`
	o2 := `{"id":2,"tile":{"x":0,"y":0},"local":{"row":0,"col":1},"zone":"dwelling","buildingID":"cemetery","materialsTotal":0,"materialsRemaining":0,"materialsDrawn":0,"labourRemaining":0,"leadTimeRemaining":0,"complete":true}`
	for i, rec := range []string{o1, o2} {
		if err := b.applyLoadRecord(serialize.Record{Kind: recBuildOrder, Data: []byte(rec)}); err != nil {
			t.Fatalf("order %d: %v", i, err)
		}
		st := fmt.Sprintf(`{"tile":{"x":0,"y":0},"local":{"row":0,"col":%d},"orderID":%d}`, i, i+1)
		if err := b.applyLoadRecord(serialize.Record{Kind: recBuildStructure, Data: []byte(st)}); err != nil {
			t.Fatalf("structure: %v", err)
		}
	}
	if err := b.RegisterCompletedServices(); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	seqs := rr2AllSeqs(b)
	t.Logf("mixed-save outcome: order1 seq=%d order2 seq=%d", seqs[1], seqs[2])
	if seqs[1] == seqs[2] {
		t.Logf("FINDING (P3, reported not asserted): backfill collided on a hand-corrupt bundle — both orders seq %d", seqs[1])
	}
}
