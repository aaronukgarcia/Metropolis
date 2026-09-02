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
import { safeSetItem } from './safeStorage.ts';

/** Max actions kept in the journal ring-buffer. PLACEHOLDER per spec. */
export const JOURNAL_CAP = 50000;

export const JOURNAL_KEY = 'metropolis.journal';

/** Max JSON characters written to localStorage (UTF-16 ≈ 2× this in quota). */
export const JOURNAL_PERSIST_MAX_CHARS = 400_000;

/**
 * BUG-458: default idle debounce before a scheduled journal write actually
 * hits localStorage. Multiple actions dispatched within this window coalesce
 * into a single setItem instead of one full-journal stringify+write PER action.
 */
export const JOURNAL_PERSIST_DEBOUNCE_MS = 1_000;

/**
 * BUG-458: even under a debounce, a sustained burst (e.g. turbo-speed ticking)
 * must not go unbounded-long without hitting disk — force a write every this
 * many scheduled actions regardless of the idle timer.
 */
export const JOURNAL_PERSIST_ACTION_INTERVAL = 200;

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
    // FEAT-1972079923 inc1 — UI-only dismiss actions, mirror dismissNotice.
    case 'dismissPlaceNotice':
      return false;
    case 'dismissInsolvencyPopup':
      return false;

    // Placement and demolition — state-affecting.
    case 'place':
      return true;
    case 'placeRoadPath':
      return true;
    case 'bulldoze':
      return true;
    // FEAT-1972079923 inc2 (AC-4): forced asset sale — removes a building and
    // credits the treasury, exactly like bulldoze — state-affecting.
    case 'sellAsset':
      return true;
    // FEAT-1972079923 inc3 (AC-5): administration entry mutates administrationState/
    // bailoutState/insolvencyState — state-affecting, must replay identically.
    case 'enterAdministration':
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
    // FEAT-2326609711 inc1 (AC-5): grid-import toggle mutates gridImportEnabled —
    // state-affecting, must journal + replay like every other toggle (tax/policy).
    case 'toggleGridImport':
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

    case 'hydrate':
      return false;

    // FEAT-2326609723: Play Mode's one-way latch mutates funds/insolvency
    // overlays — state-affecting, must journal + replay identically (mirrors
    // enterAdministration above). See genesisReplay.ts's
    // canUseAsReplayReference for why a LATCHED session is nonetheless
    // excluded from being used as a determinism reference.
    case 'enterPlayMode':
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
  let entries = journal.entries;
  for (;;) {
    const payload = JSON.stringify({ entries });
    if (payload.length > JOURNAL_PERSIST_MAX_CHARS && entries.length > 200) {
      entries = entries.slice(-Math.max(200, Math.floor(entries.length / 2)));
      continue;
    }
    const result = safeSetItem(storage, JOURNAL_KEY, payload);
    if (result.ok) return true;
    if (entries.length <= 200) {
      try {
        storage.removeItem(JOURNAL_KEY);
      } catch {
        /* ignore */
      }
      return false;
    }
    entries = entries.slice(-Math.max(200, Math.floor(entries.length / 2)));
  }
}

/**
 * BUG-458: coalescing persister. Recording a journal entry on EVERY dispatched
 * action used to synchronously JSON.stringify + setItem the WHOLE journal —
 * O(n) work per action, worse as the city ages (750KB+ journals meant every
 * single click/tick paid a full stringify+write). This debounces: `schedule`
 * coalesces bursts into one write after `debounceMs` of idle (or after
 * `actionInterval` scheduled calls, whichever comes first, so a sustained
 * high-rate burst — e.g. turbo speed — still flushes regularly instead of
 * starving forever). `flush` forces an immediate synchronous write and cancels
 * any pending timer — callers MUST flush at every boundary where losing
 * unpersisted entries would be unacceptable: before a save, before a wipe/
 * capture, and on page unload/hide. A crash between flushes loses at most the
 * debounce window of actions (acceptable, per GR#27's line: an unsaved SAVE or
 * pre-wipe capture is not acceptable, a few seconds of journal tail is).
 */
export interface JournalPersister {
  /** Coalesce a write: cheap to call on every dispatch. */
  schedule(journal: Journal): void;
  /** Force an immediate synchronous persist, bypassing/cancelling any pending debounce. */
  flush(journal: Journal): boolean;
  /** Cancel any pending scheduled write without persisting (teardown). */
  cancel(): void;
}

export interface JournalPersisterOptions {
  debounceMs?: number;
  actionInterval?: number;
  /** Injectable timer functions — tests can pass a fake scheduler for determinism. */
  setTimeoutFn?: (fn: () => void, ms: number) => unknown;
  clearTimeoutFn?: (id: unknown) => void;
}

export function createJournalPersister(
  storage: Storage | null | undefined,
  opts: JournalPersisterOptions = {},
): JournalPersister {
  const debounceMs = opts.debounceMs ?? JOURNAL_PERSIST_DEBOUNCE_MS;
  const actionInterval = opts.actionInterval ?? JOURNAL_PERSIST_ACTION_INTERVAL;
  const scheduleTimer = opts.setTimeoutFn ?? ((fn: () => void, ms: number) => setTimeout(fn, ms));
  const cancelTimer = opts.clearTimeoutFn ?? ((id: unknown) => clearTimeout(id as ReturnType<typeof setTimeout>));

  let timerId: unknown = null;
  let pendingJournal: Journal | null = null;
  let actionsSincePersist = 0;

  const clearPendingTimer = () => {
    if (timerId !== null) {
      cancelTimer(timerId);
      timerId = null;
    }
  };

  const doFlush = (journal: Journal): boolean => {
    clearPendingTimer();
    pendingJournal = null;
    actionsSincePersist = 0;
    return persistJournal(storage, journal);
  };

  return {
    schedule(journal: Journal) {
      pendingJournal = journal;
      actionsSincePersist += 1;
      if (actionsSincePersist >= actionInterval) {
        doFlush(journal);
        return;
      }
      if (timerId !== null) return; // already scheduled — will pick up the latest pendingJournal
      timerId = scheduleTimer(() => {
        timerId = null;
        if (pendingJournal) doFlush(pendingJournal);
      }, debounceMs);
    },
    flush(journal: Journal): boolean {
      return doFlush(journal);
    },
    cancel() {
      clearPendingTimer();
      pendingJournal = null;
      actionsSincePersist = 0;
    },
  };
}

/**
 * Load journal from localStorage (called on boot for restore).
 * Fail-safe: missing/corrupt JSON degrades to empty journal.
 */
export function loadJournal(storage: Storage | null | undefined): Journal {
  if (!storage) return emptyJournal();
  try {
    const raw = storage.getItem(JOURNAL_KEY);
    if (!raw) return emptyJournal();
    const parsed = JSON.parse(raw) as Journal;
    return Array.isArray(parsed.entries) ? parsed : emptyJournal();
  } catch {
    return emptyJournal();
  }
}
