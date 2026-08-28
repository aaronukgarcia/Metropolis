// captureBeforeWipe.ts — BUG-420 / GR#27 "Capture Before Wipe".
//
// Fail-closed: no wipe/reset of SimState proceeds unless the full debug JSON
// of the CURRENT city has been persisted. Envelope may carry a wall-clock
// timestamp; nothing here writes Date.now/Math.random into SimState.

import type { SimState } from './types.ts';
import { buildDebugJson, debugJsonText, type DebugJson } from './debugjson.ts';
import { currentMapUi } from './uistate.ts';
import { recentErrors } from './backend.ts';
import { reducer } from './engine.ts';

/** localStorage key holding the pre-wipe ring buffer (newest last). */
export const PREWIPE_ARCHIVE_KEY = 'metropolis.preWipeArchive';
/** Ring-buffer cap: newest PREWIPE_CAP captures kept; oldest dropped. */
export const PREWIPE_CAP = 10;

export interface CaptureStorage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

/** One archived wipe: wall-clock envelope + full debug JSON (buildDebugJson schema). */
export interface PreWipeArchiveEntry {
  capturedAtMs: number;
  tick: number;
  debug: DebugJson;
}

export function readPreWipeArchive(storage: CaptureStorage): PreWipeArchiveEntry[] {
  const raw = storage.getItem(PREWIPE_ARCHIVE_KEY);
  if (!raw) return [];
  const parsed = JSON.parse(raw);
  if (!Array.isArray(parsed)) {
    throw new Error('pre-wipe archive is not an array');
  }
  return parsed as PreWipeArchiveEntry[];
}

function loadEntriesForAppend(storage: CaptureStorage): PreWipeArchiveEntry[] {
  const raw = storage.getItem(PREWIPE_ARCHIVE_KEY);
  if (!raw) return [];
  try {
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? (parsed as PreWipeArchiveEntry[]) : [];
  } catch {
    return [];
  }
}

/**
 * Persist the full debug JSON of `state` to the pre-wipe archive.
 * Throws on write failure — caller MUST abort the wipe.
 * `nowMs` is the envelope/debug-meta wall-clock (defaults to Date.now); it is
 * NOT written into SimState.
 */
export function captureBeforeWipe(
  state: SimState,
  appVersion: string,
  storage: CaptureStorage,
  nowMs: number = Date.now(),
): void {
  const dj = buildDebugJson(state, {
    appVersion,
    frameAtMs: nowMs,
    map: currentMapUi(),
    errors: recentErrors(),
  });
  debugJsonText(dj);
  const entry: PreWipeArchiveEntry = {
    capturedAtMs: nowMs,
    tick: state.tick,
    debug: dj,
  };
  const entries = loadEntriesForAppend(storage);
  entries.push(entry);
  storage.setItem(PREWIPE_ARCHIVE_KEY, JSON.stringify(entries.slice(-PREWIPE_CAP)));
}

/**
 * Capture first; run `applyWipe` only if capture did not throw.
 * Capture failure propagates — `applyWipe` is never called.
 */
export function attemptWipe(
  state: SimState,
  appVersion: string,
  storage: CaptureStorage,
  applyWipe: () => void,
): void {
  captureBeforeWipe(state, appVersion, storage);
  applyWipe();
}

/**
 * BUG-427 / GR#27 reload boundary — BEST-EFFORT capture for page unload.
 *
 * A page RELOAD / version-restart wipes the running in-memory sim (boot then
 * restores from the savepoint), but unlike the in-app `reset` action there is no
 * point at which we can fail the wipe closed: `beforeunload` cannot be blocked or
 * awaited, so this is deliberately FAIL-OPEN. It does only synchronous
 * localStorage work and swallows every error so the unload is never obstructed.
 *
 * This is the reload counterpart to `attemptWipe`'s fail-CLOSED guarantee for the
 * reset action: `attemptWipe` aborts the wipe when the capture throws; here the
 * wipe (the browser navigating away) happens regardless, so the most we can do is
 * archive on a best-effort basis. That asymmetry is expected and by design.
 *
 * `getState` is called at unload time so the LATEST state is archived — passing a
 * function (not a snapshot) avoids a stale-closure capture of an old state.
 * Returns true if an entry was written, false if anything went wrong.
 */
export function captureOnUnload(
  getState: () => SimState,
  appVersion: string,
  storage: CaptureStorage,
  nowMs: number = Date.now(),
): boolean {
  try {
    captureBeforeWipe(getState(), appVersion, storage, nowMs);
    return true;
  } catch {
    // Fail-OPEN: beforeunload must never throw. A failed archive just means no
    // pre-wipe snapshot for this reload — the savepoint restore still runs at boot.
    return false;
  }
}

/**
 * Reducer-shaped reset: capture current state, then `{type:'reset'}`.
 * On capture failure, returns the previous state unchanged (same reference).
 */
export function resetWithCapture(
  state: SimState,
  appVersion: string,
  storage: CaptureStorage,
  nowMs?: number,
): SimState {
  try {
    captureBeforeWipe(state, appVersion, storage, nowMs ?? Date.now());
  } catch {
    return state;
  }
  return reducer(state, { type: 'reset' });
}
