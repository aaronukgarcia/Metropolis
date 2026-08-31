// capture-before-wipe.test.mjs — BUG-420 / GR#27 "Capture Before Wipe".
//
// Run with `npm test` (node --test).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  captureBeforeWipe,
  compactDebugForArchive,
  resetWithCapture,
  attemptWipe,
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

test('compactDebugForArchive keeps failures-only checks and nulls perfHud', () => {
  const compact = compactDebugForArchive({
    consistency: {
      failures: 1,
      checks: [
        { id: 'colour.1.defined', ok: true, detail: 'ok' },
        { id: 'funds.mismatch', ok: false, detail: 'fail' },
      ],
    },
    perfHud: { note: 'wall-clock', fps: null, tick: null, memoryMB: 1, networkCalls: 0, networkKB: 0 },
    sim: { tick: 9, funds: 100, population: 4 },
    buildings: { count: 2 },
    meta: { tick: 9 },
  });
  assert.equal(compact.perfHud, null);
  assert.deepEqual(compact.consistency.checks, [{ id: 'funds.mismatch', ok: false, detail: 'fail' }]);
  assert.equal(compact.consistency.failures, 1);
  assert.equal(compact.sim.tick, 9);
  assert.equal(compact.sim.funds, 100);
  assert.equal(compact.sim.population, 4);
  assert.equal(compact.buildings.count, 2);
  assert.deepEqual(compact.buildings.list, []);
  assert.equal(compact.meta.tick, 9);
});

// ===== BUG-437 BAR-1: attemptWipe direct coverage =====
//
// attemptWipe is the GR#27 wrapper wired to the reset dispatch at store.tsx:319.
// r1 REJECTed because nothing exercised attemptWipe itself (only the lower-level
// captureBeforeWipe/resetWithCapture were covered) — a swallowing try/catch around
// its internal captureBeforeWipe call would still pass every prior test.

test('attemptWipe: capture failure THROWS and does NOT invoke applyWipe', () => {
  const state = dirtyCity();
  const storage = throwingStorage();
  let applyWipeCalls = 0;
  assert.throws(
    () => attemptWipe(state, APP_VERSION, storage, () => { applyWipeCalls += 1; }),
    /setItem blocked/,
  );
  assert.equal(applyWipeCalls, 0, 'applyWipe must never run when the capture throws');
});

test('attemptWipe: working capture invokes applyWipe exactly once, AFTER the archive write lands', () => {
  const state = dirtyCity();
  const storage = memStorage();
  let applyWipeCalls = 0;
  let archiveSeenInsideCallback = null;

  attemptWipe(state, APP_VERSION, storage, () => {
    applyWipeCalls += 1;
    // Read the raw storage (not a cached reference) so this proves ordering:
    // the capture's setItem must have already landed by the time applyWipe runs.
    archiveSeenInsideCallback = storage.getItem(PREWIPE_ARCHIVE_KEY);
  });

  assert.equal(applyWipeCalls, 1, 'applyWipe must run exactly once on a successful capture');
  assert.ok(archiveSeenInsideCallback, 'the pre-wipe archive write must be visible in storage before applyWipe runs');

  const archive = readPreWipeArchive(storage);
  assert.equal(archive.length, 1);
  assert.equal(archive[0].tick, state.tick);
});

test('compact archive has no ok:true consistency checks', () => {
  const storage = memStorage();
  const state = dirtyCity();
  captureBeforeWipe(state, APP_VERSION, storage, NOW_MS);
  const archive = readPreWipeArchive(storage);
  assert.equal(archive.length, 1);
  const cons = archive[0].debug.consistency;
  const checks = cons.checks;
  if (Array.isArray(checks)) {
    for (const row of checks) {
      assert.equal(row.ok, false, row.id);
    }
    if (cons.failures === 0) {
      assert.equal(checks.length, 0);
    }
  }
  assert.equal(archive[0].tick, state.tick);
  assert.equal(archive[0].debug.meta.tick, state.tick);
  assert.equal(archive[0].debug.sim.tick, state.tick);
  assert.equal(archive[0].debug.sim.funds, state.funds);
  assert.equal(archive[0].debug.sim.population, state.population);
  assert.equal(archive[0].debug.buildings.count, state.buildings.length);
  assert.equal(archive[0].debug.perfHud, null);
});

// ---------- FEAT-1972079916 BAR-F1: readPreWipeArchive throws a NAMED code ----------

test('readPreWipeArchive on a non-array archive throws with .code MET-V807 (not a bare Error)', () => {
  const storage = memStorage();
  storage.setItem(PREWIPE_ARCHIVE_KEY, JSON.stringify({ not: 'an array' }));
  assert.throws(
    () => readPreWipeArchive(storage),
    (err) => {
      assert.equal(err.code, 'MET-V807', 'thrown error must carry the registry code MET-V807');
      assert.match(err.message, /not an array/);
      return true;
    },
  );
});
