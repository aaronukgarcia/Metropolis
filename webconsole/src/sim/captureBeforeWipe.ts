// captureBeforeWipe.ts — BUG-420 / GR#27 "Capture Before Wipe".
//
// Fail-closed: no wipe/reset of SimState proceeds unless the full debug JSON
// of the CURRENT city has been persisted. Envelope may carry a wall-clock
// timestamp; nothing here writes Date.now/Math.random into SimState.

import type { SimState } from './types.ts';
import { buildDebugJson, type DebugJson, type ConsistencyReportJson } from './debugjson.ts';
import { currentMapUi } from './uistate.ts';
import { recentErrors, codedError } from './backend.ts';
import { reducer } from './engine.ts';
import { getPrewipeCap } from './storageConfig.ts';
import { safeSetItem } from './safeStorage.ts';
import { encode, decode } from './saveCodec.ts';

export { PREWIPE_CAP, getPrewipeCap, setPrewipeCap } from './storageConfig.ts';

/** localStorage key holding the pre-wipe ring buffer (newest last). */
export const PREWIPE_ARCHIVE_KEY = 'metropolis.preWipeArchive';

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
  // FEAT-1972079935: decode() is a no-op on a legacy uncompressed value (no
  // LZv1: prefix), so this reads both old and new archives unchanged.
  const parsed = JSON.parse(decode(raw));
  if (!Array.isArray(parsed)) {
    // FEAT-1972079916/GR#7 (BAR-F1): real registry-sourced code MET-V807 via .code.
    throw codedError('MET-V807', 'pre-wipe archive is not an array');
  }
  return parsed as PreWipeArchiveEntry[];
}

function loadEntriesForAppend(storage: CaptureStorage): PreWipeArchiveEntry[] {
  const raw = storage.getItem(PREWIPE_ARCHIVE_KEY);
  if (!raw) return [];
  try {
    const parsed = JSON.parse(decode(raw));
    return Array.isArray(parsed) ? (parsed as PreWipeArchiveEntry[]) : [];
  } catch {
    return [];
  }
}

export function compactDebugForArchive(dj: DebugJson): DebugJson {
  const failed = (dj.consistency?.checks ?? []).filter((c) => c.ok === false);
  const failures = dj.consistency?.failures ?? 0;
  const consistency: ConsistencyReportJson = {
    failures,
    checks: failures === 0 ? [] : failed,
    // BUG-624: the capture path never threads grace (buildDebugJson is called
    // here with no priorFailedLineIds — see captureBeforeWipe below), so
    // rawFailedLineIds is already the raw, ungraced truth; carried through
    // unfiltered so the archived snapshot keeps the full RAW picture even
    // when the compact `checks` array above is trimmed to empty on success.
    rawFailedLineIds: dj.consistency?.rawFailedLineIds ?? [],
  };
  return {
    ...dj,
    consistency,
    perfHud: null,
    buildings: {
      count: dj.buildings.count,
      byKind: dj.buildings.byKind,
      list: [],
    },
    sim: {
      ...dj.sim,
      roadMonitors: [],
      roadConnectivity: { connectedRoadTiles: [] },
    },
  };
}

function downloadPreWipeEntry(entry: PreWipeArchiveEntry): void {
  const filename = `pre-wipe-tick-${entry.tick}.json`;
  const blob = new Blob([JSON.stringify(entry)], { type: 'application/json' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  a.click();
  URL.revokeObjectURL(url);
}

function persistCompactArchive(
  storage: CaptureStorage,
  entries: PreWipeArchiveEntry[],
  entry: PreWipeArchiveEntry,
  cap: number,
): void {
  // BUG-457: routed through the shared quota-safe helper (isQuotaError
  // classification lives in one place now) — the fail-closed CONTRACT is
  // unchanged: exhaust every fallback, then throw, so attemptWipe's caller
  // aborts the wipe (GR#27).
  // FEAT-1972079935: encode() compresses each candidate payload before it hits
  // setItem — this makes the capped/single tiers MORE likely to fit, but never
  // changes the fail-closed contract: a compressed write can still legitimately
  // exceed quota (a single huge debug snapshot, or quota already near-exhausted
  // by other keys), and that failure must still fall through to the next tier
  // and ultimately throw exactly as before.
  const first = safeSetItem(storage, PREWIPE_ARCHIVE_KEY, encode(JSON.stringify(entries.slice(-cap))));
  if (first.ok) return;
  const second = safeSetItem(storage, PREWIPE_ARCHIVE_KEY, encode(JSON.stringify([entry])));
  if (second.ok) return;
  const failure = new Error(second.error ?? 'pre-wipe archive persist failed');
  if (typeof document === 'undefined') {
    throw failure;
  }
  try {
    downloadPreWipeEntry(entry);
    return;
  } catch {
    throw failure;
  }
}

/**
 * Persist a compact debug JSON of `state` to the pre-wipe archive.
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
  // BUG-624: intentionally NOT threading priorFailedLineIds — a pre-wipe
  // capture is the RAW-truth record GR#27 exists for, so it must show any
  // consistency divergence exactly as it stood at that instant, ungraced,
  // even a transient online-flip one. Grace is for a repeatedly-refreshed
  // live panel only (DebugTab), never for a one-shot forensic snapshot.
  const dj = compactDebugForArchive(
    buildDebugJson(state, {
      appVersion,
      frameAtMs: nowMs,
      map: currentMapUi(),
      errors: recentErrors(),
    }),
  );
  const entry: PreWipeArchiveEntry = {
    capturedAtMs: nowMs,
    tick: state.tick,
    debug: dj,
  };
  const entries = loadEntriesForAppend(storage);
  entries.push(entry);
  persistCompactArchive(storage, entries, entry, getPrewipeCap(storage));
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
