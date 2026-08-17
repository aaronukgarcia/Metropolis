package news

import (
	"fmt"
	"math"
	"testing"
)

// TestRoadNameStory_UsesSeededRoadName is AC-3: a congestion event on a
// seeded road must produce a story whose road-name field equals the seeded
// road's actual Name, not a hardcoded example or an independently-generated
// string.
func TestRoadNameStory_UsesSeededRoadName(t *testing.T) {
	api, err := New("road-name-correlation")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := api.SetRoadNamer(fakeNamer{names: map[string]string{"road-1": "Pent Lane"}}); err != nil {
		t.Fatalf("SetRoadNamer: %v", err)
	}
	if _, err := api.Ingest(Event{ID: "cong-1", Tick: 0, Category: CategoryCrisis, Magnitude: 5, EntityID: "road-1", Text: "congestion on the road"}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	stories := Bulletin(api.History(), 0, api.Config())
	if len(stories) != 1 {
		t.Fatalf("got %d stories, want 1", len(stories))
	}
	if got := stories[0].Name; got != "Pent Lane" {
		t.Errorf("story road-name = %q, want the seeded road's actual Name %q", got, "Pent Lane")
	}
	if got := stories[0].EntityID; got != "road-1" {
		t.Errorf("story entity reference = %q, want %q (traceable to the seeded road)", got, "road-1")
	}
}

// TestAnnualReconcile_SameEventLogAsBulletin is AC-4: the annual review's
// "year in numbers" is computed from the SAME event log the bulletin draws
// from, and reconciles — total deaths reported by the annual review equals
// the sum the year's death-category events independently total.
func TestAnnualReconcile_SameEventLogAsBulletin(t *testing.T) {
	h := NewHistory()
	cfg := testConfig()
	events := []Event{
		{ID: "d1", Tick: 0, Category: CategoryDeath, Magnitude: 10, Text: "10 deaths"},
		{ID: "d2", Tick: 30, Category: CategoryDeath, Magnitude: 5, Text: "5 deaths"},
		{ID: "f1", Tick: 60, Category: CategoryFirst, Magnitude: 1, Text: "first uni"},
		{ID: "c1", Tick: 90, Category: CategoryCrisis, Magnitude: 7, Text: "crisis"},
	}
	for _, ev := range events {
		h.append(ev, "")
	}

	// The bulletin for month 0 draws from the same *History:
	b0 := Bulletin(h, 0, cfg)
	if len(b0) != 1 || b0[0].EventID != "d1" {
		t.Fatalf("month-0 bulletin = %v, want just the d1 death event", eventIDs(b0))
	}

	// The annual review for year 0 is computed from the same log:
	ar := AnnualReview(h, 0, cfg)

	// Reconcile: total deaths = 10 + 5 = 15, independently summable from
	// the injected death events (not a second running total).
	if got := numberValue(t, ar.Numbers, "Deaths"); got != 15 {
		t.Errorf("annual review deaths = %d, want 15 (the sum the year's death events independently total)", got)
	}
	if got := numberValue(t, ar.Numbers, "Firsts"); got != 1 {
		t.Errorf("annual review firsts = %d, want 1", got)
	}
	if ar.Year != 0 {
		t.Errorf("annual review year = %d, want 0", ar.Year)
	}
	// Biggest story of year 0 must be the highest-salience event (d1: 10×3.0=30).
	if !ar.HasBiggest || ar.BiggestStory.EventID != "d1" {
		t.Errorf("biggest story = %+v (HasBiggest=%v), want the d1 event", ar.BiggestStory, ar.HasBiggest)
	}
}

// TestEpilogueRedaction_RemovedMilestoneNotStated is AC-5 (the sharpest
// attributability check): redacting a milestone record from an
// otherwise-complete log must make the epilogue stop stating that milestone
// was reached — proof the epilogue is generated from the log, not a live
// counter or a fabricated summary.
func TestEpilogueRedaction_RemovedMilestoneNotStated(t *testing.T) {
	cfg := testConfig()
	complete := []Event{
		{ID: "m1", Tick: 30, Category: CategoryMilestone, Magnitude: 1, Text: "First Cheriton University graduates"},
		{ID: "d1", Tick: 0, Category: CategoryDeath, Magnitude: 3, Text: "3 deaths"},
	}

	control := buildEpilogue(recordsOf(complete), cfg)
	if !hasMilestone(control, "m1") {
		t.Fatal("control epilogue must state the milestone m1 (test setup)")
	}
	if len(control.Milestones) != 1 {
		t.Fatalf("control epilogue milestones = %d, want 1", len(control.Milestones))
	}

	// Redact the milestone record: only the death event remains.
	redacted := []Event{complete[1]}
	without := buildEpilogue(recordsOf(redacted), cfg)
	if hasMilestone(without, "m1") {
		t.Error("epilogue must NOT state the redacted milestone m1")
	}
	if len(without.Milestones) != 0 {
		t.Errorf("redacted epilogue milestones = %d, want 0", len(without.Milestones))
	}
}

// TestArchiveRetainsUnselectedEvents is AC-9: an event considered by the
// editor but not selected for the bulletin's story slots must remain
// queryable in the archive — "didn't make the front page" is a
// distinguishable state from "never happened".
func TestArchiveRetainsUnselectedEvents(t *testing.T) {
	api, err := New("archive-retain-correlation")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	const injected = 7 // more than maxBulletinStories
	lowID := "ev-0"    // lowest magnitude => lowest salience
	// Distinct IDs, strictly increasing magnitudes, all in month 0, so ev-0
	// is the lowest-salience event and must fall below the top-5 cutoff.
	for i := 0; i < injected; i++ {
		id := fmt.Sprintf("ev-%d", i)
		if _, err := api.Ingest(Event{ID: id, Tick: int64(i), Category: CategoryRecord, Magnitude: int64(i + 1), Text: "story"}); err != nil {
			t.Fatalf("Ingest %s: %v", id, err)
		}
	}

	cfg := api.Config()
	b := Bulletin(api.History(), 0, cfg)
	selected := make(map[string]bool, len(b))
	for _, bs := range b {
		selected[bs.EventID] = true
	}
	if selected[lowID] {
		t.Fatalf("test setup: %s should be below the bulletin's top-%d cutoff", lowID, maxBulletinStories)
	}

	// The unselected, lowest-salience event must still be retrievable via
	// the archive's query path.
	found := false
	for range api.Query(func(st Story) bool { return st.EventID == lowID }) {
		found = true
	}
	if !found {
		t.Errorf("unselected event %s is not queryable in the archive (discard-after-rank would drop it)", lowID)
	}
}

// TestAggregateNumbers_SaturatesDeathOverflow is SEC-206: the death total
// accumulates through num.SatAdd, so two valid death events each at
// math.MaxInt64 saturate to math.MaxInt64 rather than wrapping to a
// negative "Deaths" figure — a wrapped total would be a hallucinated fact
// in the year-in-numbers.
func TestAggregateNumbers_SaturatesDeathOverflow(t *testing.T) {
	rs := []record{
		{ev: Event{Category: CategoryDeath, Magnitude: math.MaxInt64}},
		{ev: Event{Category: CategoryDeath, Magnitude: math.MaxInt64}},
	}
	nums := aggregateNumbers(rs)
	if got := numberValue(t, nums, "Deaths"); got != math.MaxInt64 {
		t.Errorf("deaths = %d, want saturated %d (SEC-206)", got, int64(math.MaxInt64))
	}
}
