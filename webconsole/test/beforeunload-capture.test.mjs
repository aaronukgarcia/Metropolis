// beforeunload-capture.test.mjs — BUG-427 / GR#27 "Capture Before Wipe" on RELOAD.
//
// BUG-420 guards the in-app `reset` action (fail-closed via attemptWipe). BUG-427
// closes the gap where a page RELOAD / version-restart wipes the running sim
// without firing that guard: the SimProvider registers a `beforeunload` handler
// that best-effort archives the CURRENT state to the same pre-wipe ring.
//
// This tests the pure/testable seam `captureOnUnload(getState, version, storage)`:
//   - it archives an entry for the CURRENT state (debug JSON keys + the tick);
//   - a throwing storage does NOT propagate (fail-open — beforeunload never throws);
//   - it reads the LATEST state from getState, not a stale one.
//
// Run with `npm test` (node --test).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  captureOnUnload,
  readPreWipeArchive,
  PREWIPE_ARCHIVE_KEY,
} from '../src/sim/captureBeforeWipe.ts';
import { DEBUG_JSON_FORMAT } from '../src/sim/debugjson.ts';
import { initialState, reducer } from '../src/sim/engine.ts';

const APP_VERSION = 'v9.9.9-test';
const NOW_MS = 1_701_234_567_890;

function memStorage() {
  const map = new Map();
  return {
    getItem(k) {
      return map.has(k) ? map.get(k) : null;
    },
    setItem(k, v) {
      map.set(k, String(v));
    },
  };
}

function throwingStorage() {
  return {
    getItem() {
      return null;
    },
    setItem() {
      throw new Error('QuotaExceededError: setItem blocked');
    },
  };
}

function dirtyCity() {
  let state = initialState();
  state = reducer(state, { type: 'debugFunds', amount: 50_000 });
  state = reducer(state, { type: 'place', spec: 'res_hut', x: 5, y: 5 });
  state = reducer(state, { type: 'tick' });
  state = reducer(state, { type: 'tick' });
  return state;
}

test('archives on unload: writes a pre-wipe entry for the CURRENT state', () => {
  const storage = memStorage();
  const state = dirtyCity();
  const preTick = state.tick;

  const wrote = captureOnUnload(() => state, APP_VERSION, storage, NOW_MS);

  assert.equal(wrote, true);
  assert.equal(storage.getItem(PREWIPE_ARCHIVE_KEY) !== null, true);
  const archive = readPreWipeArchive(storage);
  assert.equal(archive.length, 1);
  assert.equal(archive[0].tick, preTick);
  assert.equal(archive[0].debug.meta.tick, preTick);
  assert.equal(archive[0].debug.sim.tick, preTick);
  assert.equal(archive[0].debug.meta.format, DEBUG_JSON_FORMAT);
  // Full debug JSON envelope — same top-level keys as any pre-wipe capture.
  for (const k of ['meta', 'sim', 'flows', 'demand', 'fiscal', 'info', 'map', 'buildings', 'errors', 'consistency']) {
    assert.ok(Object.prototype.hasOwnProperty.call(archive[0].debug, k), `missing key ${k}`);
  }
});

test('fail-open: a throwing storage does NOT propagate out of captureOnUnload', () => {
  const state = dirtyCity();
  const storage = throwingStorage();
  let result;
  assert.doesNotThrow(() => {
    result = captureOnUnload(() => state, APP_VERSION, storage, NOW_MS);
  });
  assert.equal(result, false);
});

test('reads the LATEST state from getState, not a stale snapshot', () => {
  const storage = memStorage();
  // getState returns whatever `live` points at when captureOnUnload runs.
  let live = dirtyCity();
  const early = live.tick;
  // Mutate the reference AFTER wiring getState but BEFORE the unload fires.
  live = reducer(live, { type: 'tick' });
  live = reducer(live, { type: 'tick' });
  const latest = live.tick;
  assert.notEqual(latest, early);

  captureOnUnload(() => live, APP_VERSION, storage, NOW_MS);

  const archive = readPreWipeArchive(storage);
  assert.equal(archive.length, 1);
  assert.equal(archive[0].tick, latest, 'must archive the latest tick, not the early one');
  assert.equal(archive[0].debug.sim.tick, latest);
});
