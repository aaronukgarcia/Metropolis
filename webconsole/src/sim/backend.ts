// backend.ts — the browser-facing debug backend shim (FEAT-1972079885/86).
// Error capture (window listeners in main.tsx feed recordError) plus the
// snapshot commit path. The commit QUEUE itself is the pure module
// commitqueue.ts (see its ASM-453 contract note: no backend exists yet, so
// every commit queues client-side in localStorage until one arrives); this
// file only binds it to the real window.localStorage and the real network.

import { enqueueCommit, pendingCount, type StorageLike } from './commitqueue.ts';

const errorLog: { at: number; msg: string }[] = [];

export function recordError(msg: string): void {
  errorLog.unshift({ at: Date.now(), msg });
  if (errorLog.length > 25) errorLog.pop();
}

export function recentErrors(): { at: number; msg: string }[] {
  return [...errorLog];
}

/** One row of the DebugTab "Errors captured" list. */
export interface ErrorRow {
  /** Local wall-clock time the error was captured. */
  time: string;
  msg: string;
}

/**
 * Pure presentation model for the "Errors captured" display: `empty` selects
 * the "No errors captured this session." hint; otherwise `rows` render
 * newest-first (recordError unshifts) as "time  message".
 */
export function errorListModel(errors: readonly { at: number; msg: string }[]): {
  empty: boolean;
  rows: ErrorRow[];
} {
  return {
    empty: errors.length === 0,
    rows: errors.map((e) => ({ time: new Date(e.at).toLocaleTimeString(), msg: e.msg })),
  };
}

export interface CommitResult {
  ok: boolean;
  queued: boolean;
  id: string;
  message: string;
}

// Fail-safe storage binding: in any context without localStorage (or where
// touching it throws) fall back to a volatile in-memory store — the Debug
// tab must keep working, just without cross-reload persistence.
const memoryStore = new Map<string, string>();
const memoryStorage: StorageLike = {
  getItem: (k) => memoryStore.get(k) ?? null,
  setItem: (k, v) => void memoryStore.set(k, v),
};

function queueStorage(): StorageLike {
  try {
    if (typeof localStorage !== 'undefined') return localStorage;
  } catch {
    /* fall through to memory */
  }
  return memoryStorage;
}

export function pendingCommits(): number {
  return pendingCount(queueStorage());
}

export async function commitDebug(payload: unknown): Promise<CommitResult> {
  const id = `DBG-${Date.now().toString(36).toUpperCase()}`;
  try {
    const res = await fetch('/api/debug/commit', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id, at: new Date().toISOString(), payload }),
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    return { ok: true, queued: false, id, message: `Committed to backend as ${id}` };
  } catch {
    // ASM-453: no backend exists — queue locally and report honestly.
    const n = enqueueCommit(queueStorage(), id, payload, new Date().toISOString());
    return {
      ok: true,
      queued: true,
      id,
      message: `Backend unreachable — queued locally as ${id} (${n} awaiting processing)`,
    };
  }
}
