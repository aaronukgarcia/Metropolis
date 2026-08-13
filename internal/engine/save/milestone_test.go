package save

import (
	"reflect"
	"testing"
)

// TestMilestone_DistinctBundlesAndRestore is AC-5: crossing at least two
// thresholds (Hamlet at 100, Small Town at 5,000) against a fixture
// world produces a distinct bundle per crossing (not overwritten by the
// next), and loading at least one back reconstructs the live state at
// that crossing (GR#12) with the recorded tier matching the crossing
// that produced it.
func TestMilestone_DistinctBundlesAndRestore(t *testing.T) {
	root := t.TempDir()
	widgets := newWidgetParticipant(widget{ID: 1, Name: "hamlet-state", Score: 100})
	mgr := NewManager(root, []Participant{widgets}, "test-corr")

	hamlet := Tiers[1] // {2, "Hamlet", 100}
	if hamlet.Name != "Hamlet" || hamlet.Population != 100 {
		t.Fatalf("test assumption wrong: Tiers[1] = %+v, want Hamlet/100", hamlet)
	}
	if err := mgr.Milestone(fixtureContext(10, 1), hamlet); err != nil {
		t.Fatalf("Milestone(Hamlet): %v", err)
	}

	widgets.items = []widget{{ID: 2, Name: "small-town-state", Score: 5000}}
	smallTown := Tiers[3] // {4, "Small Town", 5000}
	if smallTown.Name != "Small Town" || smallTown.Population != 5000 {
		t.Fatalf("test assumption wrong: Tiers[3] = %+v, want Small Town/5000", smallTown)
	}
	if err := mgr.Milestone(fixtureContext(50, 5), smallTown); err != nil {
		t.Fatalf("Milestone(Small Town): %v", err)
	}

	hamletDir := milestoneDir(root, hamlet)
	smallTownDir := milestoneDir(root, smallTown)
	if hamletDir == smallTownDir {
		t.Fatalf("both milestone crossings resolved to the same bundle directory %q", hamletDir)
	}

	summaries, readErrs, err := List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(readErrs) != 0 {
		t.Fatalf("List readErrs = %v", readErrs)
	}
	if len(summaries) != 2 {
		t.Fatalf("List returned %d summaries, want 2 distinct milestone bundles", len(summaries))
	}

	// Restore-test: load the Hamlet crossing back and confirm both the
	// state and the recorded tier match.
	loadWidgets := newWidgetParticipant()
	loadMgr := NewManager(root, []Participant{loadWidgets}, "test-corr")
	_, meta, err := loadMgr.Load(hamletDir)
	if err != nil {
		t.Fatalf("Load(hamletDir): %v", err)
	}
	if meta.SaveKind != KindMilestone {
		t.Fatalf("meta.SaveKind = %q, want %q", meta.SaveKind, KindMilestone)
	}
	if meta.MilestoneTierNumber != hamlet.Number || meta.MilestoneTierName != hamlet.Name {
		t.Fatalf("meta tier = (%d, %q), want (%d, %q)", meta.MilestoneTierNumber, meta.MilestoneTierName, hamlet.Number, hamlet.Name)
	}
	want := []widget{{ID: 1, Name: "hamlet-state", Score: 100}}
	if !reflect.DeepEqual(loadWidgets.State(), want) {
		t.Fatalf("loaded Hamlet-crossing state = %+v, want %+v", loadWidgets.State(), want)
	}
}

// TestMilestone_NeverPrunedByAutosaveRetention is AC-5's "never pruned"
// clause: 11 autosaves plus a milestone save must leave the milestone
// bundle present after autosave retention rotates.
func TestMilestone_NeverPrunedByAutosaveRetention(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(root, []Participant{newWidgetParticipant(widget{ID: 1})}, "test-corr")

	if err := mgr.Milestone(fixtureContext(0, 0), Tiers[0]); err != nil {
		t.Fatalf("Milestone: %v", err)
	}
	for y := 0; y < autosaveRetentionSlots+3; y++ {
		if err := mgr.Autosave(fixtureContext(int64(y), int64(y))); err != nil {
			t.Fatalf("Autosave %d: %v", y, err)
		}
	}

	summaries, readErrs, err := List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(readErrs) != 0 {
		t.Fatalf("List readErrs = %v", readErrs)
	}
	var milestoneCount, autosaveCount int
	for _, s := range summaries {
		switch s.SaveKind {
		case KindMilestone:
			milestoneCount++
		case KindAutosave:
			autosaveCount++
		}
	}
	if milestoneCount != 1 {
		t.Fatalf("milestone count = %d after autosave rotation, want 1 (never pruned)", milestoneCount)
	}
	if autosaveCount != autosaveRetentionSlots {
		t.Fatalf("autosave count = %d, want %d", autosaveCount, autosaveRetentionSlots)
	}
}

// TestSaveKind_RoundTripsThroughListAndLoad is AC-6: a save produced via
// each of SaveManual/Autosave/Milestone reports the correct SaveKind
// through both List and Load.
func TestSaveKind_RoundTripsThroughListAndLoad(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(root, []Participant{newWidgetParticipant(widget{ID: 1})}, "test-corr")

	if err := mgr.SaveManual(fixtureContext(1, 0), "m1"); err != nil {
		t.Fatalf("SaveManual: %v", err)
	}
	if err := mgr.Autosave(fixtureContext(2, 1)); err != nil {
		t.Fatalf("Autosave: %v", err)
	}
	if err := mgr.Milestone(fixtureContext(3, 2), Tiers[0]); err != nil {
		t.Fatalf("Milestone: %v", err)
	}

	summaries, readErrs, err := List(root)
	if err != nil || len(readErrs) != 0 {
		t.Fatalf("List: %v / readErrs=%v", err, readErrs)
	}
	got := map[SaveKind]int{}
	for _, s := range summaries {
		got[s.SaveKind]++
	}
	for _, k := range []SaveKind{KindManual, KindAutosave, KindMilestone} {
		if got[k] != 1 {
			t.Fatalf("List reported %d entries of kind %q, want 1", got[k], k)
		}
	}

	loadMgr := NewManager(root, []Participant{newWidgetParticipant()}, "test-corr")
	for _, s := range summaries {
		_, meta, err := loadMgr.Load(s.Path)
		if err != nil {
			t.Fatalf("Load(%s): %v", s.Path, err)
		}
		if meta.SaveKind != s.SaveKind {
			t.Fatalf("Load(%s).SaveKind = %q, List reported %q", s.Path, meta.SaveKind, s.SaveKind)
		}
	}
}
