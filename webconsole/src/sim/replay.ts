// replay.ts — FEAT-1972079854: savepoint/restore and deterministic replay.
//
// A savepoint captures: (1) a snapshot of SimState at a known tick, (2) the journal
// tail since that snapshot, (3) a wall-clock saved timestamp. Restoring a savepoint
// replays the journal tail onto the snapshot, re-deriving the original end state
// deterministically. State consistency is verified before and after replay.
//
// Fail-safe: any localStorage error (quota, private mode, corruption) must degrade
// gracefully — never crash the app. Restore returns { success: false } and the app
// boots with a fresh initial state.

import type { SimState } from './types.ts';
import type { Journal, JournalEntry } from './journal.ts';
import { reducer, initialState as getInitialState, nextSafeBuildingId } from './engine.ts';
import { runConsistencyChecks } from './consistency.ts';
import { emptyJournal } from './journal.ts';

/**
 * Complete savepoint persisted to localStorage. Includes snapshot, journal tail,
 * and metadata for diagnostics. Stable schema — never change field names without
 * a migration.
 */
export interface Savepoint {
  /** ISO-8601 timestamp when the savepoint was saved (wall-clock). */
  savedAt: string;
  /** Game tick of the snapshot. */
  snapshotTick: number;
  /** Complete SimState snapshot at snapshotTick (before journal tail was replayed). */
  snapshot: SimState;
  /** Journal entries added since the snapshot (replay these to reach current state). */
  journalTail: JournalEntry[];
}

/**
 * Result of a restore attempt. Always returns { success: boolean } so the caller
 * doesn't need try/catch; localStorage errors degrade to { success: false }.
 */
export interface RestoreResult {
  success: boolean;
  /** If success=true, the restored state. Otherwise undefined. */
  state?: SimState;
  /** Diagnostic: why restore failed (empty string on success). */
  reason?: string;
  /** If success=true, the number of journal entries replayed. */
  replayed?: number;
}

/**
 * The localStorage key for savepoints. Follow metropolis.* naming convention.
 * Multiple savepoints rotate through keys: savepoint.0, savepoint.1, savepoint.2
 * (see SAVEPOINT_CAP).
 */
export const SAVEPOINT_KEY_PREFIX = 'metropolis.savepoint';

/** Number of rolling savepoints kept (newest SAVEPOINT_CAP). PLACEHOLDER per spec. */
export const SAVEPOINT_CAP = 3;

/** Time in milliseconds between autosaves. PLACEHOLDER per spec; wall-clock timer in UI. */
export const AUTOSAVE_INTERVAL_MS = 30000; // 30 seconds

/**
 * The subset of the Web Storage API the replay module needs — injectable for tests.
 */
export interface StorageLike {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

/**
 * Generate the localStorage key for the Nth savepoint slot (0-based).
 */
function savepointKey(slot: number): string {
  return `${SAVEPOINT_KEY_PREFIX}.${slot}`;
}

/**
 * Read all savepoints from localStorage, in order (slot 0, 1, 2).
 * Fail-safe: corrupt JSON or missing keys degrade to an empty list.
 */
export function readAllSavepoints(storage: StorageLike): Savepoint[] {
  const savepoints: Savepoint[] = [];
  for (let slot = 0; slot < SAVEPOINT_CAP; slot++) {
    try {
      const raw = storage.getItem(savepointKey(slot));
      if (!raw) continue;
      const parsed = JSON.parse(raw);
      savepoints.push(parsed as Savepoint);
    } catch {
      // Corrupt JSON or parse error — skip this slot.
      continue;
    }
  }
  return savepoints;
}

/**
 * Find the most recent savepoint by savedAt timestamp.
 */
export function mostRecentSavepoint(savepoints: Savepoint[]): Savepoint | null {
  if (savepoints.length === 0) return null;
  return savepoints.reduce((best, sp) => (sp.savedAt > best.savedAt ? sp : best));
}

/**
 * Persist a new savepoint, rotating slots (round-robin, keeping only the newest
 * SAVEPOINT_CAP). Fail-safe: localStorage errors are caught and logged silently;
 * the app continues without the savepoint.
 *
 * Returns whether the save succeeded (true = persisted, false = failed).
 * The caller should display a quiet indicator on failure (FEAT-1972079854 spec).
 */
export function persistSavepoint(
  storage: StorageLike,
  savepoint: Savepoint
): boolean {
  try {
    // Read existing savepoints to determine the next slot.
    const existing = readAllSavepoints(storage);
    const nextSlot = existing.length % SAVEPOINT_CAP;
    // Overwrite the oldest slot (round-robin).
    storage.setItem(savepointKey(nextSlot), JSON.stringify(savepoint));
    return true;
  } catch {
    // QuotaExceededError, SecurityError (private mode), or JSON.stringify error.
    return false;
  }
}

/**
 * Restore from the most recent savepoint. Replays the journal tail to reconstruct
 * the end state, verifies consistency, and returns the restored state. Always fail-safe:
 * any error (missing savepoint, corrupt JSON, consistency check failure) returns
 * { success: false } and the app boots fresh.
 */
export function restoreFromSavepoint(storage: StorageLike): RestoreResult {
  try {
    const savepoints = readAllSavepoints(storage);
    if (savepoints.length === 0) {
      return { success: false, reason: 'No savepoint found' };
    }

    const most = mostRecentSavepoint(savepoints);
    if (!most) {
      return { success: false, reason: 'No valid savepoint found' };
    }

    // Start from the snapshot state.
    let state = most.snapshot;

    // BUG-413 FIX: Recalculate nextId to ensure it's > max existing building id.
    // This prevents collision when new buildings are placed after restore.
    // The saved nextId may be stale if journal replayed actions added buildings.
    state = { ...state, nextId: nextSafeBuildingId(state.buildings) };

    // Verify consistency BEFORE replay (snapshot should already be consistent).
    const beforeReport = runConsistencyChecks(state);
    if (beforeReport.failures > 0) {
      return {
        success: false,
        reason: `Snapshot failed consistency (${beforeReport.failures} failures)`,
      };
    }

    // Replay the journal tail to reconstruct the final state.
    let replayed = 0;
    for (const entry of most.journalTail) {
      state = reducer(state, entry.action);
      replayed++;
    }

    // BUG-413 FIX: After replay, recalculate nextId again to ensure it accounts
    // for any buildings added during the replay.
    state = { ...state, nextId: nextSafeBuildingId(state.buildings) };

    // Verify consistency AFTER replay (should still be consistent after deterministic replay).
    const afterReport = runConsistencyChecks(state);
    if (afterReport.failures > 0) {
      return {
        success: false,
        reason: `Replay failed consistency (${afterReport.failures} failures) after ${replayed} actions`,
      };
    }

    return { success: true, state, replayed };
  } catch (e) {
    // Catch-all for JSON.parse errors, missing fields, type mismatches, etc.
    return { success: false, reason: `Restore error: ${String(e)}` };
  }
}

/**
 * Create a new savepoint from current state and journalTail.
 *
 * The savepoint captures:
 * - snapshot: the full SimState at this moment (self-contained)
 * - journalTail: actions added AFTER this savepoint (for incremental recovery)
 *
 * DESIGN: The snapshot is a complete state. For autosave, we save every 30s and
 * the journalTail represents a small delta since the last savepoint. On restore,
 * we load the snapshot + replay its tail to reconstruct any actions taken after
 * the snapshot was saved.
 *
 * CALLER RESPONSIBILITY: Calculate the tail before calling this function.
 * Typically: tail = journal.entries.slice(lastSaveIndex) where lastSaveIndex
 * is tracked since the previous savepoint. Pass the tail here; this function
 * does NOT calculate it.
 *
 * Call this whenever the app wants to persist a recovery point (typically every
 * AUTOSAVE_INTERVAL_MS via a wall-clock timer in the React layer).
 */
export function createSavepoint(
  state: SimState,
  journalTail: JournalEntry[],
  now: Date = new Date()
): Savepoint {
  return {
    savedAt: now.toISOString(),
    snapshotTick: state.tick,
    snapshot: state,
    journalTail, // Real tail: actions recorded since last snapshot
  };
}

/**
 * Initialise a fresh game session. Returns the initial state and an empty journal.
 * Used on app boot if no savepoint is available.
 */
export function freshStart(): { state: SimState; journal: Journal } {
  return {
    state: getInitialState(),
    journal: emptyJournal(),
  };
}
