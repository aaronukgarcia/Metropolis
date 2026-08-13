package save

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// TestList_FiltersStrayReplacedSibling is BUG-158 finding 1: a crash
// between writeBundle's displace and promote renames leaves a fully-
// valid ".replaced-stage-<random>" sibling bundle on disk (header.json +
// save-meta.json intact, same shape as any real bundle). Before this
// fix, List enumerated it as an independent, permanent phantom
// duplicate save entry. This test manufactures that exact stray
// directly (bypassing writeBundle, so it proves List's OWN filtering
// logic, not merely "writeBundle never leaves one behind") and confirms
// List no longer returns it.
func TestList_FiltersStrayReplacedSibling(t *testing.T) {
	root := t.TempDir()
	widgets := newWidgetParticipant(widget{ID: 1, Name: "real", Score: 1})
	mgr := NewManager(root, []Participant{widgets}, "test-corr")
	if err := mgr.SaveManual(fixtureContext(1, 0), "slot"); err != nil {
		t.Fatalf("SaveManual: %v", err)
	}

	realDir := manualDir(root, "slot")
	strayDir := realDir + ".replaced-stage-abc123"
	copyBundleDir(t, realDir, strayDir)

	summaries, readErrs, err := List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(readErrs) != 0 {
		t.Fatalf("List readErrs = %v, want none", readErrs)
	}
	if len(summaries) != 1 {
		t.Fatalf("List returned %d summaries, want exactly 1 (the stray .replaced-stage- sibling must be filtered out): %+v", len(summaries), summaries)
	}
	if summaries[0].Path != realDir {
		t.Fatalf("List returned Path=%q, want the real slot %q — a phantom entry leaked through", summaries[0].Path, realDir)
	}
}

// TestWriteBundle_ReapsStrayReplacedSiblingOnNextSave is BUG-158's
// second requirement for finding 1: a stray ".replaced-stage-<random>"
// sibling must not just be hidden from List forever — it must actually
// be removed from disk the next time writeBundle runs against that same
// slot, so it stops accumulating disk usage.
func TestWriteBundle_ReapsStrayReplacedSiblingOnNextSave(t *testing.T) {
	root := t.TempDir()
	widgets := newWidgetParticipant(widget{ID: 1, Name: "v1", Score: 1})
	mgr := NewManager(root, []Participant{widgets}, "test-corr")
	if err := mgr.SaveManual(fixtureContext(1, 0), "slot"); err != nil {
		t.Fatalf("SaveManual (first): %v", err)
	}

	realDir := manualDir(root, "slot")
	strayDir := realDir + ".replaced-stage-def456"
	copyBundleDir(t, realDir, strayDir)

	if _, err := os.Stat(strayDir); err != nil {
		t.Fatalf("test setup: stray sibling %q not present before second save: %v", strayDir, err)
	}

	// A second save to the SAME slot is the real production save-over
	// path (BUG-157) — this is where the reap sweep must fire.
	widgets.items = []widget{{ID: 2, Name: "v2", Score: 2}}
	if err := mgr.SaveManual(fixtureContext(2, 0), "slot"); err != nil {
		t.Fatalf("SaveManual (second, save-over): %v", err)
	}

	if _, err := os.Stat(strayDir); !os.IsNotExist(err) {
		t.Fatalf("stray sibling %q still exists after a subsequent save to the same slot (stat err=%v), want reaped", strayDir, err)
	}
	if _, err := serialize.ValidateBundle(realDir); err != nil {
		t.Fatalf("real slot failed ValidateBundle after save-over + reap: %v", err)
	}
}

// copyBundleDir manufactures a byte-for-byte copy of a promoted bundle
// directory at dst, standing in for the displaced sibling writeBundle
// itself would have produced had it crashed between the displace and
// promote renames — the stray directory's CONTENTS are a real, valid
// bundle (that's exactly what makes BUG-158 dangerous); only its name
// marks it as stray.
func copyBundleDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copyBundleDir(%q -> %q): %v", src, dst, err)
	}
}

// TestAutosave_TOCTOU_ConcurrentCallsNeverCollapseIntoOneSlot is
// BUG-158 finding 2: Autosave() used to call nextAutosaveSeq() OUTSIDE
// m.mu, before writeBundle's own TryLock — a TOCTOU gap in which two
// overlapping Autosave calls could both read the same "next" seq off
// disk before either had written anything, then race writeBundle's
// save-over path against each other, with the second one silently
// displacing-and-deleting the first's already-promoted, real autosave
// data while returning nil (success) to both callers.
//
// This reproduces the exact repro shape from the Destructive finding:
// two Autosave-shaped calls arriving close enough together that, under
// the old code, both would have observed the same pre-write disk state.
// It forces call A to block mid-write (deterministic overlap window,
// same technique as TestConcurrentSaves_NoInterleaving) and fires call B
// while A is still holding the lock, then asserts the fixed
// (seq-allocation-inside-the-lock) behavior: exactly ONE autosave slot
// ever exists, A's real data is never displaced/lost, and B's outcome is
// an honestly-observable rejection (ErrSaveInProgress) rather than a
// silent, data-destroying "success".
func TestAutosave_TOCTOU_ConcurrentCallsNeverCollapseIntoOneSlot(t *testing.T) {
	root := t.TempDir()

	blocker := &widgetParticipant{
		items:              []widget{{ID: 1, Name: "first-autosave", Score: 1}},
		blockOnFirstSource: make(chan struct{}),
		releaseSource:      make(chan struct{}),
	}
	mgr := NewManager(root, []Participant{blocker}, "test-corr")

	errCh := make(chan error, 1)
	go func() {
		errCh <- mgr.Autosave(fixtureContext(1, 0))
	}()

	// Wait until A has taken the lock, computed its seq, and is mid-
	// write — the exact window the old code's TOCTOU gap could be hit
	// in, now closed because seq allocation happens under the same lock
	// writeBundle's write does.
	<-blocker.blockOnFirstSource

	secondErr := mgr.Autosave(fixtureContext(2, 0))

	close(blocker.releaseSource)
	firstErr := <-errCh

	if firstErr != nil {
		t.Fatalf("first (in-flight) Autosave failed: %v", firstErr)
	}
	if secondErr == nil {
		t.Fatalf("second (overlapping) Autosave returned nil error — it must never silently succeed by colliding with the first call's seq; want ErrSaveInProgress")
	}
	if !isSaveInProgress(secondErr) {
		t.Fatalf("second Autosave error = %v, want ErrSaveInProgress (MET-E800) — serialization must be real, not lucky timing", secondErr)
	}

	seqs, err := listAutosaveSeqs(root)
	if err != nil {
		t.Fatalf("listAutosaveSeqs: %v", err)
	}
	if len(seqs) != 1 {
		t.Fatalf("autosave seqs on disk = %v, want exactly 1 — the second call must never have collapsed into (and destroyed) the first's slot", seqs)
	}

	dir := autosaveDir(root, seqs[0])
	loadMgr := NewManager(root, []Participant{newWidgetParticipant()}, "test-corr")
	if _, _, err := loadMgr.Load(dir); err != nil {
		t.Fatalf("Load of the surviving autosave: %v", err)
	}
	got := loadMgr.participants[0].(*widgetParticipant).State()
	if len(got) != 1 || got[0].Name != "first-autosave" {
		t.Fatalf("surviving autosave data = %+v, want the first call's data intact (never displaced by the second's collision)", got)
	}

	// No stray displaced sibling should be left behind by a rejected
	// second call either — it must never have gotten far enough to
	// stage/displace anything.
	matches, _ := filepath.Glob(replacedSiblingGlob(dir))
	if len(matches) != 0 {
		t.Fatalf("stray .replaced-stage- siblings after the rejected second Autosave: %v, want none", matches)
	}
}
