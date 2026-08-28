// journal.ts — FEAT-1972079854: deterministic input journal for replay and autosave.
//
// The reducer is pure and deterministic: same action sequence on the same initial seed
// → identical final state. The journal records every state-affecting action with its
// tick position, forming a complete replay log. Non-sim UI actions (speed, tool, UI
// state) are excluded; only actions that change SimState are recorded.
//
// Ring-buffer design (capped array): oldest entries are evicted when the cap is
// reached, keeping the newest JOURNAL_CAP actions. Persisted alongside savepoints
// (the tail-since-snapshot is the recovery journal).

import type { Action } from './engine.ts';

/** Max actions kept in the journal ring-buffer. PLACEHOLDER per spec. */
export const JOURNAL_CAP = 50000;

/** One recorded action with its tick position. */
export interface JournalEntry {
  /** Game tick when action was dispatched. */
  tick: number;
  /** The action itself (state-affecting only). */
  action: Action;
}

/** Complete journal state for persistence. */
export interface Journal {
  /** Ring-buffer of recorded actions. */
  entries: JournalEntry[];
}

/**
 * Classify whether an action affects SimState and should be journaled.
 * UI-only actions (speed, tool, dismissNotice) are excluded; debug actions
 * (debugFunds, debugXp) ARE included because they modify game state.
 *
 * SSOT for action classification. Changes here must update:
 * - journalEntry() call sites
 * - test coverage (ensure RED/GREEN prove coverage)
 * - replay implementation (process all classified actions)
 */
export function isStateAffecting(action: Action): boolean {
  switch (action.type) {
    // Deterministic game ticks — core of the journal.
    case 'tick':
      return true;

    // UI-only actions — not journaled.
    case 'speed':
      return false;
    case 'tool':
      return false;
    case 'setClipboard':
      return false;
    case 'dismissNotice':
      return false;

    // Placement and demolition — state-affecting.
    case 'place':
      return true;
    case 'placeRoadPath':
      return true;
    case 'bulldoze':
      return true;

    // Moving buildings — state-affecting only for the actual move, not UI pickup/cancel.
    case 'pickup':
      return false; // UI only (sets movingId)
    case 'relocate':
      return true; // State-affecting (changes building coords, deducts funds)
    case 'cancelMove':
      return false; // UI only (clears movingId)

    // Utility systems.
    case 'pipeUpgrade':
      return true;
    case 'tax':
      return true;
    case 'policy':
      return true;
    case 'loan':
      return true;
    case 'repay':
      return true;

    // Clone-stamp — state-affecting (places/flattens buildings, deducts cost).
    case 'stampRegion':
      return true;

    // Debug actions — state-affecting.
    case 'debugFunds':
      return true;
    case 'debugXp':
      return true;

    // God-mode "Unlock all" — state-affecting (deducts funds, flips unlockedAll).
    case 'unlockAll':
      return true;

    // System reset.
    case 'reset':
      return true;

    // Exhaustiveness check (TypeScript will error if Action gains a new case).
    default: {
      const _exhaustive: never = action;
      return _exhaustive;
    }
  }
}

/**
 * Create an empty journal.
 */
export function emptyJournal(): Journal {
  return { entries: [] };
}

/**
 * Record a state-affecting action. Returns the updated journal, capped at
 * JOURNAL_CAP (oldest entries evicted first, ring-buffer style).
 */
export function recordAction(journal: Journal, tick: number, action: Action): Journal {
  if (!isStateAffecting(action)) {
    return journal; // UI-only actions are ignored.
  }
  const entries = [...journal.entries, { tick, action }].slice(-JOURNAL_CAP);
  return { entries };
}

/**
 * Get the current journal size (number of entries).
 */
export function journalSize(journal: Journal): number {
  return journal.entries.length;
}

/**
 * Get a snapshot of the journal's tail (last N entries).
 * Used to extract the recovery journal (changes since last savepoint).
 */
export function journalTail(journal: Journal, fromIndex: number): JournalEntry[] {
  return journal.entries.slice(fromIndex);
}

/**
 * Drain the journal, returning all entries and resetting to empty.
 * Used to rebuild the journal from a savepoint's tail on restore.
 */
export function drainJournal(journal: Journal): { entries: JournalEntry[]; journal: Journal } {
  return { entries: journal.entries, journal: emptyJournal() };
}

/**
 * Persist journal to localStorage (FEAT-1972079854: journal survival across reload).
 * Stores the full journal as JSON. Call this after appending to journal.
 * Fail-safe: errors are caught and logged silently; app continues.
 */
export function persistJournal(storage: Storage | null | undefined, journal: Journal): boolean {
  if (!storage) return false;
  try {
    storage.setItem('metropolis.journal', JSON.stringify(journal));
    return true;
  } catch {
    return false; // QuotaExceededError, SecurityError, etc.
  }
}

/**
 * Load journal from localStorage (called on boot for restore).
 * Fail-safe: missing/corrupt JSON degrades to empty journal.
 */
export function loadJournal(storage: Storage | null | undefined): Journal {
  if (!storage) return emptyJournal();
  try {
    const raw = storage.getItem('metropolis.journal');
    if (!raw) return emptyJournal();
    const parsed = JSON.parse(raw) as Journal;
    return Array.isArray(parsed.entries) ? parsed : emptyJournal();
  } catch {
    return emptyJournal();
  }
}
