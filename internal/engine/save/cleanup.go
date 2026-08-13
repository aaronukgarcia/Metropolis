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
// BUG-129: this package-level function has ZERO coordination with an
// in-flight writeBundle — a staging directory's mtime is set once at
// creation (newStagingDir's os.MkdirTemp) and never re-touched while
// shards stream into its shards/ subdirectory, so an aggressive
// olderThan threshold racing a genuinely slow save (this project's
// premise is up to 100M citizens) can sweep a LIVE staging directory
// out from under it. Proven live by a Destructive round on FEAT-011
// (attacker Sable): backdate a staging dir's mtime to mimic a slow
// save, run this function concurrently with an active writeBundle,
// confirm it deletes the directory and the next shard write then
// fails. Contained (writeBundleLocked's participant loop returns a
// clean, registry-wrapped ErrParticipantWriteFailed — nothing
// corrupts or half-promotes), but a real hazard once wired to any
// scheduler/call path.
//
// This function itself is kept exactly as before (safe, well-tested,
// and still the right tool for scanning a save root that has no live
// *Manager open on it — e.g. offline tooling, or a one-shot sweep
// before any Manager is constructed) — the fix is the new
// [Manager.CleanupStaleStaging] method below, which takes m.mu before
// delegating here. A caller that DOES have a live *Manager (the
// documented real-caller pattern: "cmd/metropolis's boot sequence,
// immediately after NewManager") MUST use the Manager method instead
// of calling this bare function directly against that Manager's root,
// or the race this comment describes is exactly what will happen.
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

// CleanupStaleStaging sweeps m's save root the same way the package-
// level CleanupStaleStaging function does, but takes m.mu FIRST
// (BUG-129) so a sweep can never run concurrently with an in-flight
// SaveManual/Autosave/Milestone call on this Manager. writeBundle (via
// writeBundleLocked) already holds m.mu for its entire duration —
// staging-dir creation, every shard write, header/meta write,
// validation, and promotion-or-cleanup-on-failure — so a sweep that
// also requires m.mu can only ever observe root/.staging in a
// quiescent state: either before a write has created its staging
// directory, or after that write has already removed it (failure) or
// renamed it away to finalDir (success). Either way, this method can
// never see, let alone delete, a staging directory an active write is
// still populating — the exact race the Destructive finding proved
// against the bare package-level function.
//
// Deliberately a blocking m.mu.Lock(), not TryLock: a background
// sweep has no caller waiting on an immediate answer the way
// SaveManual/Autosave/Milestone's players do (AC-11 rejects a
// concurrent SAVE outright so a player action is never silently
// queued behind another one) — a sweep can simply wait for the
// in-flight save to finish, then run against whatever root/.staging
// looks like at that point.
func (m *Manager) CleanupStaleStaging(olderThan time.Duration, now time.Time) (removed int, err error) {
	// SEC-020-class: identity check BEFORE m.mu is ever touched — see
	// Manager.checkNotCopied's doc comment (manager.go) for why a copy
	// must never attempt to acquire its own mu.
	if err := m.checkNotCopied(map[string]any{"method": "CleanupStaleStaging"}); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return CleanupStaleStaging(m.root, olderThan, now)
}
