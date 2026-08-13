package save

import (
	"strings"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// TestConcurrentSaves_NoInterleaving_SecondOutcomeObservable is AC-11:
// fire a manual-save trigger and an autosave trigger against the same
// Manager at effectively the same time. Asserts (a) no data race (run
// with -race), (b) the two saves' shard writes are never interleaved
// (each produced bundle is internally ValidateBundle-clean, never a mix
// of two states), and (c) the second trigger's outcome — this package's
// chosen answer, ASM #5: reject with ErrSaveInProgress — is itself
// observable in the test, not silently absorbed.
func TestConcurrentSaves_NoInterleaving_SecondOutcomeObservable(t *testing.T) {
	root := t.TempDir()

	blocker := &widgetParticipant{
		items:              []widget{{ID: 1, Name: "slow", Score: 1}},
		blockOnFirstSource: make(chan struct{}),
		releaseSource:      make(chan struct{}),
	}
	mgr := NewManager(root, []Participant{blocker}, "test-corr")

	var wg sync.WaitGroup
	var manualErr, autosaveErr error

	wg.Add(1)
	go func() {
		defer wg.Done()
		manualErr = mgr.SaveManual(fixtureContext(1, 0), "concurrent-manual")
	}()

	// Wait until the manual save's participant is mid-Source (guarantees
	// a genuine overlap window) before firing the second trigger.
	<-blocker.blockOnFirstSource

	autosaveErr = mgr.Autosave(fixtureContext(2, 0))

	// Release the manual save's blocked Source call and let it finish.
	close(blocker.releaseSource)
	wg.Wait()

	if manualErr != nil {
		t.Fatalf("manual save (the one that was already in flight) failed: %v", manualErr)
	}
	// The autosave arrived while the manual save held the guard — it
	// must have been rejected (this package's chosen, documented
	// outcome), never silently dropped, never allowed to interleave.
	if autosaveErr == nil {
		t.Fatalf("concurrent Autosave returned nil error, want ErrSaveInProgress (the second trigger's outcome must be observable, AC-11)")
	}
	if !isSaveInProgress(autosaveErr) {
		t.Fatalf("concurrent Autosave error = %v, want ErrSaveInProgress (MET-E800)", autosaveErr)
	}

	// The manual save's bundle must be internally clean — no
	// interleaving occurred.
	dir := manualDir(root, "concurrent-manual")
	if _, err := serialize.ValidateBundle(dir); err != nil {
		t.Fatalf("manual save bundle failed ValidateBundle after a concurrent (rejected) autosave attempt: %v", err)
	}
	// No autosave bundle should exist at all — its trigger was
	// rejected before any staging began.
	seqs, err := listAutosaveSeqs(root)
	if err != nil {
		t.Fatalf("listAutosaveSeqs: %v", err)
	}
	if len(seqs) != 0 {
		t.Fatalf("autosave dirs = %v, want none — the rejected concurrent Autosave must never have staged or promoted anything", seqs)
	}
}

func isSaveInProgress(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), ErrSaveInProgress)
}

// TestConcurrentReaders_NeverObserveTornBundle is AC-16: while an
// autosave is in flight in the background, concurrent List/Load calls
// from other goroutines must only ever observe either the pre-
// promotion (old) state or the fully-promoted (new) state — every
// bundle any List/Load call returns must be independently
// ValidateBundle-clean at the moment it is read.
func TestConcurrentReaders_NeverObserveTornBundle(t *testing.T) {
	root := t.TempDir()

	// Seed one existing manual save so readers have something to see
	// even before the background autosave promotes.
	seedMgr := NewManager(root, []Participant{newWidgetParticipant(widget{ID: 0, Name: "seed", Score: 0})}, "test-corr")
	if err := seedMgr.SaveManual(fixtureContext(0, 0), "seed"); err != nil {
		t.Fatalf("seed SaveManual: %v", err)
	}

	blocker := &widgetParticipant{
		items:              []widget{{ID: 1, Name: "bg", Score: 1}},
		blockOnFirstSource: make(chan struct{}),
		releaseSource:      make(chan struct{}),
	}
	mgr := NewManager(root, []Participant{blocker}, "test-corr")

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := mgr.Autosave(fixtureContext(1, 0)); err != nil {
			t.Errorf("background Autosave: %v", err)
		}
	}()

	<-blocker.blockOnFirstSource

	// Hammer List/Load concurrently with the in-flight (blocked, staged
	// but not yet promoted) autosave.
	stop := make(chan struct{})
	var readerWG sync.WaitGroup
	readerWG.Add(2)
	go func() {
		defer readerWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			summaries, readErrs, err := List(root)
			if err != nil {
				t.Errorf("concurrent List: %v", err)
				return
			}
			if len(readErrs) != 0 {
				t.Errorf("concurrent List readErrs: %v", readErrs)
				return
			}
			for _, s := range summaries {
				if _, err := serialize.ValidateBundle(s.Path); err != nil {
					t.Errorf("concurrent List returned a bundle that fails ValidateBundle: %s: %v", s.Path, err)
					return
				}
			}
		}
	}()
	go func() {
		defer readerWG.Done()
		loadMgr := NewManager(root, []Participant{newWidgetParticipant()}, "test-corr")
		seedDir := manualDir(root, "seed")
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, _, err := loadMgr.Load(seedDir); err != nil {
				t.Errorf("concurrent Load of the untouched seed bundle: %v", err)
				return
			}
		}
	}()

	close(blocker.releaseSource)
	wg.Wait()
	close(stop)
	readerWG.Wait()

	if t.Failed() {
		return
	}

	// After the background autosave completes, it must now be visible
	// and clean too.
	summaries, readErrs, err := List(root)
	if err != nil || len(readErrs) != 0 {
		t.Fatalf("List after background autosave: err=%v readErrs=%v", err, readErrs)
	}
	found := false
	for _, s := range summaries {
		if s.SaveKind == KindAutosave {
			found = true
		}
	}
	if !found {
		t.Fatalf("List after background autosave completed does not show the new autosave: %v", summaries)
	}
}
