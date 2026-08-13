package save

import (
	"os"
	"time"
)

// CleanupStaleStaging sweeps root/.staging for entries whose last
// modification time is older than now.Add(-olderThan) and removes them
// (Vex's secondary, non-blocking Destructive finding on FEAT-011: a
// process killed mid-writeBundle leaves an orphaned
// root/.staging/<random>/ directory behind forever — writeBundle's own
// defer only cleans up on a return from the SAME call that created it,
// never across a crash/restart).
//
// now is a caller-supplied reference time rather than read internally
// from the wall clock (AC-15/TestNoWallClockInNonTestFiles forbids
// wall-clock calls in this package's non-test files, the same discipline
// GR#21 applies engine-wide): callers construct/inject their own clock
// (e.g. cmd/metropolis's boot sequence, immediately after NewManager)
// and pass its current reading in here, keeping this package itself
// determinism-agnostic while still giving a real caller a usable sweep.
//
// Best-effort: a per-entry Stat/RemoveAll failure is skipped rather than
// aborting the whole sweep (an orphaned staging directory is forensic
// clutter, not a correctness hazard — nothing under root/.staging is
// ever reachable via List/Load), so the returned error is only non-nil
// if the .staging directory itself could not be listed at all. removed
// is the count of directories actually deleted, for a caller that wants
// to log/report what happened.
func CleanupStaleStaging(root string, olderThan time.Duration, now time.Time) (removed int, err error) {
	base := stagingRoot(root)
	entries, readErr := os.ReadDir(base)
	if os.IsNotExist(readErr) {
		return 0, nil
	}
	if readErr != nil {
		return 0, readErr
	}

	cutoff := now.Add(-olderThan)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, statErr := e.Info()
		if statErr != nil {
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		path := base + string(os.PathSeparator) + e.Name()
		if os.RemoveAll(path) == nil {
			removed++
		}
	}
	return removed, nil
}
