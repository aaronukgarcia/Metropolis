package save

import (
	"os"
	"testing"
	"time"
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

	removed, err := CleanupStaleStaging(root, 1*time.Hour, now)
	if err != nil {
		t.Fatalf("CleanupStaleStaging: %v", err)
	}
	if removed != 1 {
		t.Fatalf("CleanupStaleStaging removed = %d, want 1 (only the stale entry)", removed)
	}
	if _, err := os.Stat(staleDir); !os.IsNotExist(err) {
		t.Fatalf("stale staging dir %q still exists (stat err=%v), want removed", staleDir, err)
	}
	if _, err := os.Stat(freshDir); err != nil {
		t.Fatalf("fresh staging dir %q was removed or errored (%v), want it left alone", freshDir, err)
	}
}

// TestCleanupStaleStaging_NoStagingDir confirms a save root that never
// had a .staging directory created (nothing has ever been saved there)
// is not an error -- the common case for a fresh save root.
func TestCleanupStaleStaging_NoStagingDir(t *testing.T) {
	root := t.TempDir()
	removed, err := CleanupStaleStaging(root, 1*time.Hour, time.Now())
	if err != nil {
		t.Fatalf("CleanupStaleStaging on a root with no .staging dir: %v", err)
	}
	if removed != 0 {
		t.Fatalf("CleanupStaleStaging removed = %d, want 0", removed)
	}
}
