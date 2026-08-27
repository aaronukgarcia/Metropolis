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
