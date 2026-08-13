package save

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// TestCleanupStaleStaging_RemovesOnlyOldEntries proves the sweep can
// fail: a fresh staging directory (well within olderThan) must survive,
// while one whose mtime is pushed back past the cutoff must be removed —
// mutating the actual mtime on disk, not just asserting the function
// returns without error.
func TestCleanupStaleStaging_RemovesOnlyOldEntries(t *testing.T) {
	root := t.TempDir()

	freshDir, err := newStagingDir(root, "test-corr")
	if err != nil {
		t.Fatalf("newStagingDir (fresh): %v", err)
	}
	staleDir, err := newStagingDir(root, "test-corr")
	if err != nil {
		t.Fatalf("newStagingDir (stale): %v", err)
	}

	now := time.Now()
	oldTime := now.Add(-2 * time.Hour)
	if err := os.Chtimes(staleDir, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	removed, err := ScanStaleStagingOffline(root, 1*time.Hour, now)
	if err != nil {
		t.Fatalf("ScanStaleStagingOffline: %v", err)
	}
	if removed != 1 {
		t.Fatalf("ScanStaleStagingOffline removed = %d, want 1 (only the stale entry)", removed)
	}
	if _, err := os.Stat(staleDir); !os.IsNotExist(err) {
		t.Fatalf("stale staging dir %q still exists (stat err=%v), want removed", staleDir, err)
	}
	if _, err := os.Stat(freshDir); err != nil {
		t.Fatalf("fresh staging dir %q was removed or errored (%v), want it left alone", freshDir, err)
	}
}

// TestManagerCleanupStaleStaging_NeverRacesActiveWrite is BUG-129's
// regression test: reproduce the original race (a staging directory
// backdated to look stale, while a writeBundle is genuinely still
// writing shards into it) and prove Manager.CleanupStaleStaging — the
// fix — no longer deletes the live staging directory out from under
// the in-flight save, unlike the unexported, un-locked
// cleanupStaleStaging implementation it wraps.
func TestManagerCleanupStaleStaging_NeverRacesActiveWrite(t *testing.T) {
	root := t.TempDir()

	blocker := &widgetParticipant{
		items:              []widget{{ID: 1, Name: "slow", Score: 1}},
		blockOnFirstSource: make(chan struct{}),
		releaseSource:      make(chan struct{}),
	}
	mgr := NewManager(root, []Participant{blocker}, "test-corr")

	var wg sync.WaitGroup
	var saveErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		saveErr = mgr.SaveManual(fixtureContext(1, 0), "slow-manual")
	}()

	// Wait until the save's participant is mid-Source (guarantees a
	// genuine overlap window: writeBundleLocked has already created
	// the staging dir and is actively populating shards/ inside it,
	// and — critically — is still holding m.mu the entire time).
	<-blocker.blockOnFirstSource

	// Find the (single) staging directory the in-flight save created,
	// and backdate its mtime to mimic a genuinely slow save racing an
	// aggressive cleanup threshold (this is the original Destructive
	// finding's exact setup).
	stagingDirs, err := os.ReadDir(stagingRoot(root))
	if err != nil {
		t.Fatalf("reading .staging while save is in flight: %v", err)
	}
	if len(stagingDirs) != 1 {
		t.Fatalf("staging dirs while save is in flight = %d, want exactly 1", len(stagingDirs))
	}
	liveStagingDir := filepath.Join(stagingRoot(root), stagingDirs[0].Name())
	oldTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(liveStagingDir, oldTime, oldTime); err != nil {
		t.Fatalf("backdating live staging dir mtime: %v", err)
	}

	// Run the sweep CONCURRENTLY with the still-blocked write, with an
	// aggressive threshold that would sweep the backdated dir the
	// instant it got a chance to run. Since it must take m.mu first,
	// it will block until the write below releases and finishes.
	cleanupDone := make(chan struct{})
	var removed int
	var cleanupErr error
	go func() {
		removed, cleanupErr = mgr.CleanupStaleStaging(1*time.Hour, time.Now())
		close(cleanupDone)
	}()

	// Give the sweep goroutine a real chance to run and block on m.mu
	// before we release the write — if the fix were absent (bare
	// package-level CleanupStaleStaging, no lock), this window is
	// exactly where the original bug deleted the live staging dir.
	select {
	case <-cleanupDone:
		t.Fatalf("CleanupStaleStaging returned before the in-flight write released its lock -- it did not synchronize against m.mu")
	case <-time.After(50 * time.Millisecond):
	}

	// The staging directory must still be intact -- the sweep must not
	// have touched it while the write was still in progress.
	if _, err := os.Stat(liveStagingDir); err != nil {
		t.Fatalf("live staging dir was removed while the write was still in flight (the BUG-129 race): %v", err)
	}

	close(blocker.releaseSource)
	wg.Wait()
	<-cleanupDone

	if saveErr != nil {
		t.Fatalf("SaveManual (racing a concurrent CleanupStaleStaging) failed: %v", saveErr)
	}
	if cleanupErr != nil {
		t.Fatalf("CleanupStaleStaging: %v", cleanupErr)
	}
	// By the time the sweep actually ran (after the write finished and
	// released m.mu), the staging directory had already been renamed
	// away to finalDir by promotion -- there was nothing stale left
	// for it to remove.
	if removed != 0 {
		t.Fatalf("CleanupStaleStaging removed = %d, want 0 (the promoted save's former staging dir is already gone, not stale-and-present)", removed)
	}

	// The save itself must have completed successfully and be a valid,
	// complete bundle -- the race must not have corrupted or truncated
	// it.
	dir := manualDir(root, "slow-manual")
	if _, err := serialize.ValidateBundle(dir); err != nil {
		t.Fatalf("manual save bundle failed ValidateBundle after racing a concurrent CleanupStaleStaging: %v", err)
	}
}

// TestCleanupStaleStaging_NoStagingDir confirms a save root that never
// had a .staging directory created (nothing has ever been saved there)
// is not an error -- the common case for a fresh save root.
func TestCleanupStaleStaging_NoStagingDir(t *testing.T) {
	root := t.TempDir()
	removed, err := ScanStaleStagingOffline(root, 1*time.Hour, time.Now())
	if err != nil {
		t.Fatalf("ScanStaleStagingOffline on a root with no .staging dir: %v", err)
	}
	if removed != 0 {
		t.Fatalf("ScanStaleStagingOffline removed = %d, want 0", removed)
	}
}

// TestBUG186_FreeFunctionUnexported_NoBypassOfManagerLock is the BUG-186
// regression: it proves, by API shape rather than by runtime behaviour,
// that the old bypass ("call the package-level CleanupStaleStaging
// directly against a root a live *Manager already has open, skipping
// m.mu entirely") is no longer expressible. The raw sweep is now
// unexported (cleanupStaleStaging), so this same-package test is the
// only place outside the package itself that can still name it — any
// external package attempting `save.CleanupStaleStaging(...)` fails to
// compile, which is the mechanical enforcement BUG-186 asked for. The
// only two exported entry points left are Manager.CleanupStaleStaging
// (locked) and ScanStaleStagingOffline (documented Manager-less use),
// and calling the latter concurrently with a live Manager's write is a
// misuse this test does NOT bless -- it only confirms the unlocked path
// exists at all, unexported, for legitimate offline callers.
func TestBUG186_FreeFunctionUnexported_NoBypassOfManagerLock(t *testing.T) {
	root := t.TempDir()
	now := time.Now()

	// This call is only reachable because this test lives inside
	// package save. Nothing outside this package can spell
	// cleanupStaleStaging -- that identifier is unexported, so the
	// compiler itself refuses any external "dial the lock-free sweep
	// directly" call, which is exactly the BUG-186 gap being closed.
	if _, err := cleanupStaleStaging(root, time.Hour, now); err != nil {
		t.Fatalf("cleanupStaleStaging (in-package only): %v", err)
	}

	// The two supported external entry points must still both exist and
	// behave: the locked Manager method and the explicitly-named
	// offline scan.
	mgr := NewManager(root, nil, "test-corr")
	if _, err := mgr.CleanupStaleStaging(time.Hour, now); err != nil {
		t.Fatalf("Manager.CleanupStaleStaging: %v", err)
	}
	if _, err := ScanStaleStagingOffline(root, time.Hour, now); err != nil {
		t.Fatalf("ScanStaleStagingOffline: %v", err)
	}
}
