package compose

import (
	"fmt"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// ---------------------------------------------------------------------------
// BUG-734 independent re-round 2 (attacker: opus-reround2-bug734) — compose
// side. Attacks F3's observability under a LARGE unknown batch (does the
// ring evict the known-good entries a monitor relies on?), F4's completeness
// guard, and the ID semantics the instance-id derivation now depends on.
// ---------------------------------------------------------------------------

// TestRR2_UnknownIDBatchOfFifty drives 50 unknown ids interleaved with real
// cemetery/crematorium completions and pins what a GR#17 monitor actually
// gets: errs coalesces per Code (SEC-030/033), so the batch produces ONE
// MET-G815 slot carrying a Repeat count and the LAST unknown id — it does
// NOT flood or evict other codes' entries. The KNOWN ids in the same batch
// must still register.
func TestRR2_UnknownIDBatchOfFifty(t *testing.T) {
	ds := dsWithPlotCapacity(t, 11)
	baselineDrain := ds.MonthlyDrainCapacity(1)

	// A DIFFERENT-code canary a monitor would be watching: it must survive
	// the unknown storm untouched (the eviction question the brief asks).
	const canaryCode = ErrSnapshotSkipped // MET-G812, an unrelated registered code
	_ = errs.New(canaryCode, "rr2-canary", map[string]any{"probe": "canary"})

	completions := make([]build.BuildOrder, 0, 52)
	completions = append(completions, build.BuildOrder{ID: 1000, BuildingID: "cemetery", Status: build.OrderComplete})
	for i := 0; i < 50; i++ {
		completions = append(completions, build.BuildOrder{
			ID:         build.BuildOrderID(2000 + i),
			BuildingID: fmt.Sprintf("unknown-kind-%02d", i),
			Status:     build.OrderComplete,
		})
	}
	completions = append(completions, build.BuildOrder{ID: 3000, BuildingID: "crematorium", Status: build.OrderComplete})

	if err := registerCompletedServiceBuildings(completions, ds, "rr2"); err != nil {
		t.Fatalf("a batch containing 50 unknown ids ABORTED the whole batch: %v", err)
	}

	// The two KNOWN ids in the same batch must still be registered.
	if _, _, err := ds.CemeteryOccupancy(cemeteryInstanceID(1000), "rr2"); err != nil {
		t.Fatalf("known cemetery in a mostly-unknown batch was NOT registered: %v", err)
	}
	if got := ds.MonthlyDrainCapacity(1); got <= baselineDrain {
		t.Fatalf("known crematorium in a mostly-unknown batch added no drain capacity (%d -> %d)", baselineDrain, got)
	}

	entries := errs.Recent()
	slots, repeat, lastID := 0, 0, ""
	canaryAlive := false
	for _, e := range entries {
		switch e.Code {
		case ErrUnknownDeathServiceBuildingKind:
			slots++
			repeat = e.Repeat
			lastID = fmt.Sprint(e.Ctx["buildingID"])
		case canaryCode:
			canaryAlive = true
		}
	}
	if slots != 1 {
		t.Fatalf("MET-G815 occupies %d ring slots, want exactly 1 (errs coalesces by Code)", slots)
	}
	if repeat < 49 {
		t.Fatalf("MET-G815 Repeat = %d after 50 unknown ids — occurrences are being LOST, not coalesced", repeat)
	}
	if !canaryAlive {
		t.Fatalf("the 50-unknown batch EVICTED an unrelated code's entry from errs.Recent() — a real flood surface")
	}
	t.Logf("RR2 F3 OBSERVABILITY (informational, not a defect of this lane): the batch yields ONE MET-G815 slot, Repeat=%d, Ctx.buildingID=%q (the LAST unknown only). errs coalesces by Code (SEC-030/033) so there is no ring flood and no eviction of other codes — but a monitor CANNOT enumerate WHICH of the 50 ids were unrecognised, only that %d occurrences happened. Package-wide errs semantics, not a BUG-734 regression.", repeat, lastID, repeat+1)
}

// TestRR2_UnknownStormDoesNotEvictOtherCodes: 300 unknown ids in one call
// still occupy exactly one ring slot and leave every other code alone.
func TestRR2_UnknownStormDoesNotEvictOtherCodes(t *testing.T) {
	ds := dsWithPlotCapacity(t, 3)
	const canaryCode = ErrSnapshotTailShort // MET-G810, an unrelated registered code
	_ = errs.New(canaryCode, "rr2", map[string]any{"probe": "storm-canary"})

	completions := make([]build.BuildOrder, 0, 300)
	for i := 0; i < 300; i++ {
		completions = append(completions, build.BuildOrder{
			ID:         build.BuildOrderID(i),
			BuildingID: fmt.Sprintf("storm-%03d", i),
			Status:     build.OrderComplete,
		})
	}
	if err := registerCompletedServiceBuildings(completions, ds, "rr2"); err != nil {
		t.Fatalf("storm returned an error: %v", err)
	}
	slots, canaryAlive := 0, false
	for _, e := range errs.Recent() {
		if e.Code == ErrUnknownDeathServiceBuildingKind {
			slots++
		}
		if e.Code == canaryCode {
			canaryAlive = true
		}
	}
	if slots != 1 {
		t.Fatalf("300 unknown ids occupied %d ring slots, want 1", slots)
	}
	if !canaryAlive {
		t.Fatal("a 300-unknown storm evicted an unrelated code from the errs ring — flood surface")
	}
}

// TestRR2_CompletenessGuardHoldsForEveryNonCompleteStatus sweeps every
// BuildOrderStatus and requires that only OrderComplete ever registers (F4).
func TestRR2_CompletenessGuardHoldsForEveryNonCompleteStatus(t *testing.T) {
	statuses := []build.BuildOrderStatus{
		build.OrderPendingMaterials, build.OrderPendingLabour, build.OrderInProgress,
	}
	for i, st := range statuses {
		ds := dsWithPlotCapacity(t, 5)
		baseline := ds.MonthlyDrainCapacity(1)
		id := build.BuildOrderID(500 + i)
		if err := registerCompletedServiceBuildings([]build.BuildOrder{
			{ID: id, BuildingID: "cemetery", Status: st},
			{ID: id + 100, BuildingID: "crematorium", Status: st},
		}, ds, "rr2"); err != nil {
			t.Fatalf("status %v: %v", st, err)
		}
		if _, _, err := ds.CemeteryOccupancy(cemeteryInstanceID(id), "rr2"); err == nil {
			t.Fatalf("F4 REGRESSION: status %v registered a cemetery for an unfinished order", st)
		}
		if got := ds.MonthlyDrainCapacity(1); got != baseline {
			t.Fatalf("F4 REGRESSION: status %v registered a crematorium (drain=%d)", st, got)
		}
	}
	// The zero value of BuildOrderStatus must also not register.
	ds := dsWithPlotCapacity(t, 5)
	var zero build.BuildOrderStatus
	if zero == build.OrderComplete {
		t.Skip("zero BuildOrderStatus IS OrderComplete — guard is vacuous, see finding")
	}
	if err := registerCompletedServiceBuildings([]build.BuildOrder{{ID: 777, BuildingID: "cemetery", Status: zero}}, ds, "rr2"); err != nil {
		t.Fatalf("zero status: %v", err)
	}
	if _, _, err := ds.CemeteryOccupancy(cemeteryInstanceID(777), "rr2"); err == nil {
		t.Fatal("F4 REGRESSION: a zero-value Status registered a cemetery")
	}
}

// TestRR2_InstanceIDsKeyOnSubmissionIDNotCompletionSeq pins the identity
// semantics the helper now depends on: two DIFFERENT orders must never
// collide on an instance id, and the SAME order re-delivered (replay) must
// resolve to the same instance id, even though CompletionSeq differs from ID.
func TestRR2_InstanceIDsKeyOnSubmissionIDNotCompletionSeq(t *testing.T) {
	ds := dsWithPlotCapacity(t, 9)
	// Two orders whose CompletionSeq values are SWAPPED relative to their
	// IDs — the out-of-order completion shape F1 exists for.
	first := build.BuildOrder{ID: 41, BuildingID: "cemetery", CompletionSeq: 2, Status: build.OrderComplete}
	second := build.BuildOrder{ID: 42, BuildingID: "cemetery", CompletionSeq: 1, Status: build.OrderComplete}
	if err := registerCompletedServiceBuildings([]build.BuildOrder{first, second}, ds, "rr2"); err != nil {
		t.Fatalf("register: %v", err)
	}
	if cemeteryInstanceID(first.ID) == cemeteryInstanceID(second.ID) {
		t.Fatal("two distinct orders collided on one cemetery instance id")
	}
	for _, o := range []build.BuildOrder{first, second} {
		if _, _, err := ds.CemeteryOccupancy(cemeteryInstanceID(o.ID), "rr2"); err != nil {
			t.Fatalf("order %d not registered under its submission-id instance: %v", o.ID, err)
		}
	}
	// Replay with a DIFFERENT CompletionSeq on the same order id must not
	// mint a second cemetery.
	replay := build.BuildOrder{ID: 41, BuildingID: "cemetery", CompletionSeq: 99, Status: build.OrderComplete}
	if err := registerCompletedServiceBuildings([]build.BuildOrder{replay}, ds, "rr2"); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if _, _, err := ds.CemeteryOccupancy(cemeteryInstanceID(41), "rr2"); err != nil {
		t.Fatalf("replay lost the original registration: %v", err)
	}
}

// TestRR2_DocumentedHookRecipeIsTheROUND1DEFECT executes the cursor-advance
// loop EXACTLY as compose_buildregistry.go's "One-line hook" doc block still
// spells it (cursor tracks c.ID, the submission id) against a real
// out-of-order completion stream, and proves it loses completions — i.e. the
// prose the next lane is instructed to paste reinstates round 1's F1.
func TestRR2_DocumentedHookRecipeIsTheROUND1DEFECT(t *testing.T) {
	// Model the stream the doc's recipe consumes: CompletedBuildings filters
	// on CompletionSeq, so replicate that filter here and drive it with the
	// doc's ID-keyed cursor.
	type rec struct {
		id  build.BuildOrderID
		seq build.BuildOrderID
	}
	// Submission ids 1..3; completion order reversed (long lead times).
	all := []rec{{id: 3, seq: 1}, {id: 2, seq: 2}, {id: 1, seq: 3}}
	completedBuildings := func(since build.BuildOrderID) []build.BuildOrder {
		out := []build.BuildOrder{}
		for _, r := range all {
			if r.seq > since {
				out = append(out, build.BuildOrder{ID: r.id, CompletionSeq: uint64(r.seq),
					BuildingID: "cemetery", Status: build.OrderComplete})
			}
		}
		return out
	}
	var cursor build.BuildOrderID
	delivered := map[build.BuildOrderID]int{}
	// First poll: only seq 1 has completed at this point in sim time.
	if first := completedBuildings(cursor); len(first) > 0 {
		c := first[0]
		delivered[c.ID]++
		// THE DOC'S OWN LINE:
		if c.ID > cursor {
			cursor = c.ID
		}
	}
	// Later polls, after the rest complete.
	for round := 0; round < 3; round++ {
		for _, c := range completedBuildings(cursor) {
			delivered[c.ID]++
			if c.ID > cursor { // THE DOC'S OWN LINE
				cursor = c.ID
			}
		}
	}
	if delivered[2] == 0 || delivered[1] == 0 {
		t.Logf("RR2 FINDING (P1): the doc-block hook recipe in compose_buildregistry.go still advances the cursor with c.ID (the pre-ruling API where ID WAS the completionSeq). Replayed against an out-of-order stream it set cursor=3 after the first delivery and PERMANENTLY LOST orders %v — round 1's F1 verbatim. delivered=%v cursor=%d", []int{1, 2}, delivered, cursor)
	} else {
		t.Fatalf("expected the documented ID-keyed recipe to lose completions but it did not: delivered=%v", delivered)
	}
	// The CORRECT recipe, for contrast.
	cursor = 0
	ok := map[build.BuildOrderID]int{}
	for round := 0; round < 4; round++ {
		for _, c := range completedBuildings(cursor) {
			ok[c.ID]++
			if build.BuildOrderID(c.CompletionSeq) > cursor {
				cursor = build.BuildOrderID(c.CompletionSeq)
			}
		}
	}
	for _, id := range []build.BuildOrderID{1, 2, 3} {
		if ok[id] != 1 {
			t.Fatalf("CompletionSeq-keyed cursor delivered order %d %d times, want 1", id, ok[id])
		}
	}
}
