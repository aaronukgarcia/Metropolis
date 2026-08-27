// capture-before-wipe.test.mjs — BUG-420 / GR#27 "Capture Before Wipe".
//
// Run with `npm test` (node --test).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  captureBeforeWipe,
  resetWithCapture,
  readPreWipeArchive,
  PREWIPE_ARCHIVE_KEY,
  PREWIPE_CAP,
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

test('reset with working capture → archive has an entry at pre-wipe tick AND state is the fresh city', () => {
  const storage = memStorage();
  const state = dirtyCity();
  const preTick = state.tick;
  const preFunds = state.funds;
  const preBuildings = state.buildings.length;
  const fresh = initialState();
  assert.ok(preTick !== fresh.tick || preFunds !== fresh.funds || preBuildings !== fresh.buildings.length);

  const next = resetWithCapture(state, APP_VERSION, storage, NOW_MS);

  const archive = readPreWipeArchive(storage);
  assert.equal(archive.length, 1);
  assert.equal(archive[0].tick, preTick);
  assert.equal(archive[0].debug.meta.tick, preTick);
  assert.equal(archive[0].debug.sim.tick, preTick);
  assert.equal(storage.getItem(PREWIPE_ARCHIVE_KEY) !== null, true);

  assert.equal(next.tick, fresh.tick);
  assert.equal(next.funds, fresh.funds);
  assert.equal(next.buildings.length, fresh.buildings.length);
  assert.deepEqual(next, fresh);
  assert.equal(state.tick, preTick);
  assert.equal(state.funds, preFunds);
  assert.equal(state.buildings.length, preBuildings);
});

test('reset with throwing capture → state UNCHANGED (funds/tick/buildings intact)', () => {
  const state = dirtyCity();
  const funds = state.funds;
  const tick = state.tick;
  const buildings = state.buildings.slice();
  const storage = throwingStorage();

  assert.throws(
    () => captureBeforeWipe(state, APP_VERSION, storage, NOW_MS),
    /setItem blocked/,
  );

  const after = resetWithCapture(state, APP_VERSION, storage, NOW_MS);
  assert.equal(after, state);
  assert.equal(after.funds, funds);
  assert.equal(after.tick, tick);
  assert.equal(after.buildings.length, buildings.length);
  assert.deepEqual(after.buildings, buildings);
  assert.notEqual(after.funds, initialState().funds);
  assert.notEqual(after.tick, initialState().tick);
});

test('archived JSON has format metropolis-debug/1', () => {
  const storage = memStorage();
  const state = dirtyCity();
  resetWithCapture(state, APP_VERSION, storage, NOW_MS);
  const archive = readPreWipeArchive(storage);
  assert.equal(archive[0].debug.meta.format, DEBUG_JSON_FORMAT);
  assert.equal(archive[0].debug.meta.format, 'metropolis-debug/1');
  for (const k of ['meta', 'sim', 'flows', 'demand', 'fiscal', 'info', 'map', 'buildings', 'errors', 'consistency']) {
    assert.ok(Object.prototype.hasOwnProperty.call(archive[0].debug, k), `missing key ${k}`);
  }
});

test('capture does not inject Date.now into SimState', () => {
  const storage = memStorage();
  const state = dirtyCity();
  const before = structuredClone(state);

  let dateNowCalls = 0;
  const orig = Date.now;
  Date.now = () => {
    dateNowCalls += 1;
    return NOW_MS;
  };
  try {
    captureBeforeWipe(state, APP_VERSION, storage, NOW_MS);
  } finally {
    Date.now = orig;
  }

  assert.equal(dateNowCalls, 0, 'injected nowMs must be used; Date.now must not run');
  assert.deepEqual(state, before);
  assert.equal(JSON.stringify(state).includes(String(NOW_MS)), false);
  const archive = readPreWipeArchive(storage);
  assert.equal(archive[0].capturedAtMs, NOW_MS);
  assert.equal(archive[0].debug.meta.generatedAtMs, NOW_MS);
});

test('pre-wipe archive ring buffer keeps newest PREWIPE_CAP entries', () => {
  const storage = memStorage();
  const base = initialState();
  for (let i = 0; i < PREWIPE_CAP + 3; i++) {
    captureBeforeWipe({ ...base, tick: base.tick + i }, APP_VERSION, storage, NOW_MS + i);
  }
  const archive = readPreWipeArchive(storage);
  assert.equal(archive.length, PREWIPE_CAP);
  assert.equal(archive[0].tick, base.tick + 3);
  assert.equal(archive[PREWIPE_CAP - 1].tick, base.tick + PREWIPE_CAP + 2);
});
