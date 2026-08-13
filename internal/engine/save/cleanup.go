package save

import (
	"os"
	"time"
)

// cleanupStaleStaging sweeps root/.staging for entries whose last
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
//
// BUG-129: this raw implementation has ZERO coordination with an
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
// BUG-186: this is now unexported precisely so the package's public API
// shape cannot express the BUG-129 race by accident. There are exactly
// two supported, mutually exclusive ways to reach it from outside the
// package: [Manager.CleanupStaleStaging] (takes m.mu first — the only
// safe choice when a live *Manager is open on root) and
// [ScanStaleStagingOffline] (no locking at all — the only safe choice
// when NO live *Manager is open on root, e.g. offline tooling or a
// one-shot sweep before any Manager is constructed). Previously both
// use cases were served by a single exported function distinguished
// only by a doc-comment warning ("MUST use the Manager method
// instead"), which nothing mechanically enforced; a caller holding a
// live *Manager could still dial this function directly and reintroduce
// the exact race Manager.CleanupStaleStaging exists to prevent. Renaming
// it out of the exported API removes that call path at compile time —
// the two exported entry points now name their own precondition instead
// of relying on a comment nobody is compiler-forced to read.
func cleanupStaleStaging(root string, olderThan time.Duration, now time.Time) (removed int, err error) {
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

// ScanStaleStagingOffline sweeps root/.staging exactly like
// [Manager.CleanupStaleStaging] does, for the one case that method
// cannot cover: a save root with NO live *Manager open on it at all
// (offline tooling inspecting/repairing a save directory, or a one-shot
// sweep run before any Manager for that root has been constructed —
// e.g. as part of a maintenance CLI). There is no m.mu to take because
// there is no *Manager instance in scope; the name says so explicitly
// so a call site can't mistake it for the Manager-locked path.
//
// Do NOT call this against a root that a live *Manager already has
// open — that reintroduces the exact BUG-129 race
// [Manager.CleanupStaleStaging] exists to prevent (this function
// performs no locking whatsoever). If a *Manager is in scope, use its
// CleanupStaleStaging method instead.
func ScanStaleStagingOffline(root string, olderThan time.Duration, now time.Time) (removed int, err error) {
	return cleanupStaleStaging(root, olderThan, now)
}

// CleanupStaleStaging sweeps m's save root the same way the package-
// level cleanupStaleStaging function does, but takes m.mu FIRST
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
	return cleanupStaleStaging(m.root, olderThan, now)
}
