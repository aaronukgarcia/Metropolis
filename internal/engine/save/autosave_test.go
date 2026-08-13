package save

import (
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// TestAutosave_RollingRetention is AC-4: drive >=11 year-boundaries
// through Autosave and assert exactly 10 remain (the 10 most recent),
// with at least one of the retained ones loading back correctly (GR#12
// restore-test for the autosave path).
func TestAutosave_RollingRetention(t *testing.T) {
	root := t.TempDir()
	widgets := newWidgetParticipant(widget{ID: 1, Name: "seed", Score: 0})
	mgr := NewManager(root, []Participant{widgets}, "test-corr")

	const years = 13
	for y := 0; y < years; y++ {
		widgets.items = []widget{{ID: y, Name: fmt.Sprintf("year-%d", y), Score: float64(y)}}
		if err := mgr.Autosave(fixtureContext(int64(y*12), int64(y))); err != nil {
			t.Fatalf("Autosave year %d: %v", y, err)
		}
	}

	seqs, err := listAutosaveSeqs(root)
	if err != nil {
		t.Fatalf("listAutosaveSeqs: %v", err)
	}
	if len(seqs) != autosaveRetentionSlots {
		t.Fatalf("retained %d autosaves, want exactly %d", len(seqs), autosaveRetentionSlots)
	}
	wantSeqs := make([]int, 0, autosaveRetentionSlots)
	for i := years - autosaveRetentionSlots; i < years; i++ {
		wantSeqs = append(wantSeqs, i)
	}
	if !reflect.DeepEqual(seqs, wantSeqs) {
		t.Fatalf("retained seqs = %v, want the 10 most recent %v", seqs, wantSeqs)
	}

	// Restore-test (GR#12): load the newest retained autosave and
	// confirm reconstructed state matches what was live at that year.
	loadWidgets := newWidgetParticipant()
	loadMgr := NewManager(root, []Participant{loadWidgets}, "test-corr")
	newestDir := autosaveDir(root, years-1)
	if _, _, err := loadMgr.Load(newestDir); err != nil {
		t.Fatalf("Load newest retained autosave: %v", err)
	}
	want := []widget{{ID: years - 1, Name: fmt.Sprintf("year-%d", years-1), Score: float64(years - 1)}}
	if !reflect.DeepEqual(loadWidgets.State(), want) {
		t.Fatalf("loaded newest autosave state = %+v, want %+v", loadWidgets.State(), want)
	}
}

// TestAutosave_FailedWriteLeavesRetentionUntouched is AC-13: an 11th
// autosave whose participant fails mid-write must leave all 10
// previously-retained autosaves present, unmodified, and still
// individually ValidateBundle-clean.
func TestAutosave_FailedWriteLeavesRetentionUntouched(t *testing.T) {
	root := t.TempDir()
	widgets := newWidgetParticipant(widget{ID: 0, Name: "ok", Score: 1})
	mgr := NewManager(root, []Participant{widgets}, "test-corr")

	for y := 0; y < autosaveRetentionSlots; y++ {
		if err := mgr.Autosave(fixtureContext(int64(y), int64(y))); err != nil {
			t.Fatalf("Autosave setup year %d: %v", y, err)
		}
	}
	seqsBefore, err := listAutosaveSeqs(root)
	if err != nil {
		t.Fatalf("listAutosaveSeqs before: %v", err)
	}
	if len(seqsBefore) != autosaveRetentionSlots {
		t.Fatalf("setup: retained %d, want %d", len(seqsBefore), autosaveRetentionSlots)
	}

	// Snapshot bundle contents (header.json bytes) before the forced
	// failure, to prove they are byte-for-byte UNCHANGED afterward, not
	// merely "still present".
	before := map[string][]byte{}
	for _, seq := range seqsBefore {
		dir := autosaveDir(root, seq)
		b, err := os.ReadFile(serialize.HeaderPath(dir))
		if err != nil {
			t.Fatalf("reading header for seq %d: %v", seq, err)
		}
		before[dir] = b
	}

	// The 11th autosave: a participant that fails after 1 record.
	failMgr := NewManager(root, []Participant{&erroringParticipant{kind: "widget", failAfter: 1}}, "test-corr")
	if err := failMgr.Autosave(fixtureContext(999, 999)); err == nil {
		t.Fatalf("Autosave with a forced participant failure returned nil error, want non-nil")
	}

	seqsAfter, err := listAutosaveSeqs(root)
	if err != nil {
		t.Fatalf("listAutosaveSeqs after: %v", err)
	}
	if !reflect.DeepEqual(seqsBefore, seqsAfter) {
		t.Fatalf("retained seqs changed after failed write: before=%v after=%v", seqsBefore, seqsAfter)
	}
	for _, seq := range seqsAfter {
		dir := autosaveDir(root, seq)
		b, err := os.ReadFile(serialize.HeaderPath(dir))
		if err != nil {
			t.Fatalf("reading header for seq %d after failure: %v", seq, err)
		}
		if string(b) != string(before[dir]) {
			t.Fatalf("autosave %d's header.json changed after a failed 11th write", seq)
		}
		if _, err := serialize.ValidateBundle(dir); err != nil {
			t.Fatalf("autosave %d failed ValidateBundle after the unrelated failed write: %v", seq, err)
		}
	}
}
