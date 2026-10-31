// attack-feat764-inc2-r2-round.test.mjs — FEAT-2326609764 inc2 (DIRTY TRACKING),
// independent destructive round r2 `Destructive-FEAT764-inc2-r2` (GR#23,
// attacker != author), 2026-10-31, against the rework cut from main fffd464.
//
// Every comparison is against the whole-city oracle (totalJobsWholeCity /
// the flag-OFF cold sector index), never the partitioned path against itself.
// Tests marked OPEN reproduce a finding filed against the round; they are RED
// until it is fixed. The rest are regression pins that held under attack.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import { initialState, reducer } from '../src/sim/engine.ts';
import { totalJobsWholeCity, constructionTicks, SPECS } from '../src/sim/data.ts';
import { FAST_BUILD_FLAG_KEY, resetFastBuildFlagCache } from '../src/sim/debugBuildSpeed.ts';
import {
  sectorKeyOf,
  sectorIndexOf,
  dirtySectorsSince,
  totalJobsPartitioned,
  __resetDirtyTrackingForTest,
  __setPartitionedDerivationsForTest,
} from '../src/sim/sectorPartition.ts';

function serialiseIndex(index) {
  return [...index.keys()]
    .sort((a, b) => a - b)
    .map((k) => {
      const a = index.get(k);
      return `${k}:${a.buildingCount}:${a.jobs}:${a.x0},${a.y0}`;
    })
    .join('|');
}

function coldIndexOf(s) {
  __setPartitionedDerivationsForTest(false);
  try {
    return sectorIndexOf(s);
  } finally {
    __setPartitionedDerivationsForTest(true);
  }
}

function staleness(s) {
  __setPartitionedDerivationsForTest(true);
  const inc = serialiseIndex(sectorIndexOf(s));
  const pj = totalJobsPartitioned(s);
  const cold = serialiseIndex(coldIndexOf(s));
  const wj = totalJobsWholeCity(s);
  return { stale: pj !== wj || inc !== cold, pj, wj };
}

function assertNotStale(s, where) {
  const r = staleness(s);
  assert.equal(r.pj, r.wj, `${where}: partitioned totalJobs ${r.pj} diverged from the whole-city oracle ${r.wj}`);
  assert.equal(r.stale, false, `${where}: the incremental sector index diverged from a cold rebuild`);
}

function freshCity() {
  __resetDirtyTrackingForTest();
  __setPartitionedDerivationsForTest(true);
  return { ...initialState(), unlockedAll: true, funds: 5_000_000_000 };
}

function settledCity() {
  let s = freshCity();
  for (let x = 0; x < 60; x++) s = reducer(s, { type: 'place', spec: 'road', x, y: 10 });
  for (let t = 0; t < 25; t++) s = reducer(s, { type: 'tick' });
  return s;
}

const SLOW_SPEC = 'com_hypermarket';

// ── fast-build debug toggle stub (ConfigMenu flips the same localStorage key) ─
function setFastBuild(on) {
  globalThis.localStorage = { getItem: (k) => (k === FAST_BUILD_FLAG_KEY && on ? '1' : null) };
  resetFastBuildFlagCache();
}
function clearFastBuild() {
  delete globalThis.localStorage;
  resetFastBuildFlagCache();
}

// ════════════════════════════════════════════════════════════════════════════
// Attack 4 — first transition out of an un-banded state
// ════════════════════════════════════════════════════════════════════════════

test('R2-4a OPEN: a legacy/hand-built state with NO roadConnectivity entering the reducer directly goes permanently stale', () => {
  let s = settledCity();
  s = reducer(s, { type: 'place', spec: 'com_market', x: 2, y: 11 });
  for (let t = 0; t < 10; t++) s = reducer(s, { type: 'tick' });
  // A finished, NOT road-adjacent building far from the spine (hand-built, as a
  // legacy save or fixture carries it). Under real connectivity it is offline.
  const lonely = { id: s.nextId, spec: 'com_market', x: 30, y: 40, builtTick: 0 };
  assert.notEqual(sectorKeyOf(30, 40), sectorKeyOf(2, 11), 'fixture sanity: different sectors');
  const legacy = { ...s, buildings: [...s.buildings, lonely], nextId: s.nextId + 1, roadConnectivity: undefined };
  assert.equal(dirtySectorsSince(legacy), null, 'sanity: un-dispatched state reads null');
  let live = legacy;
  for (let t = 0; t < 5; t++) live = reducer(live, { type: 'tick' });
  assertNotStale(live, 'R2-4a five ticks after a connectivity-less state entered the reducer');
});

test('R2-4b OPEN: a state carrying a STRIPPED connectivity graph (connectedRoadTiles: [] — commitqueue/captureBeforeWipe shape) goes permanently stale', () => {
  let s = settledCity();
  for (const x of [2, 22, 42]) s = reducer(s, { type: 'place', spec: 'com_market', x, y: 11 });
  for (let t = 0; t < 10; t++) s = reducer(s, { type: 'tick' });
  const stripped = { ...s, roadConnectivity: { connectedRoadTiles: [] } };
  let live = stripped;
  for (let t = 0; t < 5; t++) live = reducer(live, { type: 'tick' });
  assertNotStale(live, 'R2-4b five ticks after a stripped-connectivity state entered the reducer');
});

test('R2-4c: hand-edited builtTick (buildings rebuilt outside the reducer, un-banded) completes and is marked', () => {
  let s = settledCity();
  s = reducer(s, { type: 'place', spec: SLOW_SPEC, x: 5, y: 11 });
  const ct = constructionTicks(SPECS[SLOW_SPEC]);
  for (let t = 0; t < ct + 3; t++) s = reducer(s, { type: 'tick' });
  // Re-open construction from outside the reducer: a fresh state, a fresh
  // buildings array, no side-band entry, and a precise dirty set on the next tick.
  const edited = { ...s, buildings: s.buildings.map((b) => (b.spec === SLOW_SPEC ? { ...b, builtTick: s.tick - 1 } : b)) };
  let live = edited;
  for (let t = 0; t < ct + 3; t++) {
    live = reducer(live, { type: 'tick' });
    assertNotStale(live, `R2-4c tick+${t + 1}`);
  }
  assert.ok(totalJobsWholeCity(live) > 0, 'non-vacuity: the re-opened building finished');
});

test('R2-4d: structuredClone of a mid-construction banded state, then ticked past completion', () => {
  let s = settledCity();
  s = reducer(s, { type: 'place', spec: SLOW_SPEC, x: 25, y: 11 });
  for (let t = 0; t < 3; t++) s = reducer(s, { type: 'tick' });
  let live = structuredClone(s);
  const ct = constructionTicks(SPECS[SLOW_SPEC]);
  for (let t = 0; t < ct + 3; t++) live = reducer(live, { type: 'tick' });
  assertNotStale(live, 'R2-4d');
  assert.ok(totalJobsWholeCity(live) > 0, 'non-vacuity');
});

// ════════════════════════════════════════════════════════════════════════════
// Attack 5 — missed dirty triggers
// ════════════════════════════════════════════════════════════════════════════

// BUG-1138 is deferred (lead ruling): honest skip, NOT a deletion. Set METRO_RUN_BUG1138=1 to run them (they go red).
const BUG1138_OPTS = process.env.METRO_RUN_BUG1138 === '1' ? {} : { skip: 'BUG-1138 deferred by lead ruling (set METRO_RUN_BUG1138=1 to run)' };

test('R2-5a OPEN (BUG-1138 DEFERRED by lead ruling; run with METRO_RUN_BUG1138=1): toggling the DEBUG fast-build flag mid-construction (OFF -> ON) leaves the completion unmarked', BUG1138_OPTS, () => {
  try {
    setFastBuild(false);
    let s = settledCity();
    s = reducer(s, { type: 'place', spec: SLOW_SPEC, x: 25, y: 11 });
    const slow = constructionTicks(SPECS[SLOW_SPEC]);
    setFastBuild(true);
    const fast = constructionTicks(SPECS[SLOW_SPEC]);
    assert.ok(fast < slow, `fixture sanity: fast-build must shorten construction (${fast} < ${slow})`);
    for (let t = 0; t < fast + 2; t++) s = reducer(s, { type: 'tick' });
    assert.ok(totalJobsWholeCity(s) > 0, 'sanity: under fast-build the building is online now');
    assertNotStale(s, 'R2-5a after fast-build was switched on mid-construction');
  } finally {
    clearFastBuild();
  }
});

test('R2-5b OPEN (BUG-1138 DEFERRED by lead ruling; run with METRO_RUN_BUG1138=1): toggling fast-build ON -> OFF mid-construction: the building comes online later with nothing marking its sector', BUG1138_OPTS, () => {
  try {
    setFastBuild(true);
    let s = settledCity();
    s = reducer(s, { type: 'place', spec: SLOW_SPEC, x: 25, y: 11 });
    const fast = constructionTicks(SPECS[SLOW_SPEC]);
    setFastBuild(false);
    const slow = constructionTicks(SPECS[SLOW_SPEC]);
    for (let t = 0; t < slow + 3; t++) s = reducer(s, { type: 'tick' });
    assert.ok(fast < slow && totalJobsWholeCity(s) > 0, 'sanity: online under the slow schedule');
    assertNotStale(s, 'R2-5b after fast-build was switched off mid-construction');
  } finally {
    clearFastBuild();
  }
});

test('R2-5c: enterAdministration never touches buildings and never leaves the index stale', () => {
  let s = settledCity();
  s = reducer(s, { type: 'place', spec: SLOW_SPEC, x: 25, y: 11 });
  // Put the city into a bailout so the case is not a no-op.
  s = { ...s, bailoutState: { offeredAt: s.tick } };
  const adm = reducer(s, { type: 'enterAdministration' });
  assert.notEqual(adm, s, 'sanity: enterAdministration must have changed the state');
  assert.equal(adm.buildings, s.buildings, 'enterAdministration must not touch buildings');
  let live = adm;
  const ct = constructionTicks(SPECS[SLOW_SPEC]);
  for (let t = 0; t < ct + 3; t++) live = reducer(live, { type: 'tick' });
  assertNotStale(live, 'R2-5c after administration then completion');
});

test('R2-5d: reset mid-construction, then the NEW city is ticked past the old completion tick', () => {
  let s = settledCity();
  s = reducer(s, { type: 'place', spec: SLOW_SPEC, x: 25, y: 11 });
  let r = reducer(s, { type: 'reset' });
  assertNotStale(r, 'R2-5d immediately after reset');
  r = { ...r, unlockedAll: true, funds: 5_000_000_000 };
  for (let x = 0; x < 60; x++) r = reducer(r, { type: 'place', spec: 'road', x, y: 10 });
  const ct = constructionTicks(SPECS[SLOW_SPEC]);
  for (let t = 0; t < ct + 30; t++) r = reducer(r, { type: 'tick' });
  assertNotStale(r, 'R2-5d after the old completion tick passed in the new city');
});

test('R2-5e: hydrate(load) of a mid-construction save INTO a live banded state, then ticked', () => {
  let live = settledCity();
  live = reducer(live, { type: 'place', spec: 'com_market', x: 45, y: 11 });
  let other = settledCity();
  other = reducer(other, { type: 'place', spec: SLOW_SPEC, x: 5, y: 11 });
  const save = JSON.parse(JSON.stringify(other));
  let loaded = reducer(live, { type: 'hydrate', state: save, source: 'load' });
  const ct = constructionTicks(SPECS[SLOW_SPEC]);
  for (let t = 0; t < ct + 3; t++) loaded = reducer(loaded, { type: 'tick' });
  assertNotStale(loaded, 'R2-5e');
  assert.ok(totalJobsWholeCity(loaded) > 0, 'non-vacuity');
});

// ════════════════════════════════════════════════════════════════════════════
// Attack 6 — two (three) timelines in one process
// ════════════════════════════════════════════════════════════════════════════

test('R2-6: three timelines from one base — relocate in A, reset in B, load in C — lockstep', () => {
  const base = settledCity();
  let A = reducer(base, { type: 'place', spec: SLOW_SPEC, x: 5, y: 11 });
  let B = reducer(base, { type: 'place', spec: SLOW_SPEC, x: 25, y: 11 });
  let C = reducer(base, { type: 'hydrate', state: JSON.parse(JSON.stringify(A)), source: 'load' });
  const idA = A.buildings[A.buildings.length - 1].id;
  const ct = constructionTicks(SPECS[SLOW_SPEC]);
  for (let t = 0; t < ct + 5; t++) {
    if (t === 2) {
      A = reducer(A, { type: 'pickup', id: idA });
      A = reducer(A, { type: 'relocate', x: 45, y: 11 });
    }
    if (t === 4) B = reducer(B, { type: 'reset' });
    A = reducer(A, { type: 'tick' });
    B = reducer(B, { type: 'tick' });
    C = reducer(C, { type: 'tick' });
  }
  assertNotStale(A, 'R2-6 A');
  assertNotStale(B, 'R2-6 B');
  assertNotStale(C, 'R2-6 C');
  assert.ok(totalJobsWholeCity(A) > 0 && totalJobsWholeCity(C) > 0, 'non-vacuity');
});

// ════════════════════════════════════════════════════════════════════════════
// Attack 7 — worker route (hydrate source 'tick', then main-thread fallback)
// ════════════════════════════════════════════════════════════════════════════

test('R2-7a: a worker-tick hydrate commits the full-recompute fallback on the main thread', () => {
  let main = settledCity();
  main = reducer(main, { type: 'place', spec: SLOW_SPEC, x: 5, y: 11 });
  // The worker ticks a structured clone (its own context) and posts it back.
  let w = structuredClone(main);
  for (let t = 0; t < 3; t++) w = reducer(w, { type: 'tick' });
  const h = reducer(main, { type: 'hydrate', state: structuredClone(w), source: 'tick' });
  assert.equal(dirtySectorsSince(h), null, 'a worker tick hydrate must read null on the main thread');
  assertNotStale(h, 'R2-7a');
  // Fallback: the main thread resumes ticking itself (worker error/timeout).
  let live = h;
  const ct = constructionTicks(SPECS[SLOW_SPEC]);
  for (let t = 0; t < ct + 3; t++) live = reducer(live, { type: 'tick' });
  assertNotStale(live, 'R2-7a main-thread fallback after worker ticks');
});

// ════════════════════════════════════════════════════════════════════════════
// Attack 8 — flag flip ON -> OFF -> ON mid-session
// ════════════════════════════════════════════════════════════════════════════

test('R2-8: flag ON -> OFF (place + relocate + ticks while OFF) -> ON never carries a stale aggregate over', () => {
  let s = settledCity();
  s = reducer(s, { type: 'place', spec: SLOW_SPEC, x: 5, y: 11 });
  for (let t = 0; t < 2; t++) s = reducer(s, { type: 'tick' });
  __setPartitionedDerivationsForTest(false);
  s = reducer(s, { type: 'place', spec: SLOW_SPEC, x: 25, y: 11 });
  const id = s.buildings[s.buildings.length - 1].id;
  s = reducer(s, { type: 'pickup', id });
  s = reducer(s, { type: 'relocate', x: 45, y: 11 });
  for (let t = 0; t < 2; t++) s = reducer(s, { type: 'tick' });
  __setPartitionedDerivationsForTest(true);
  const ct = constructionTicks(SPECS[SLOW_SPEC]);
  for (let t = 0; t < ct + 3; t++) {
    s = reducer(s, { type: 'tick' });
    assertNotStale(s, `R2-8 tick+${t + 1} after the flip back ON`);
  }
  assert.ok(totalJobsWholeCity(s) > 0, 'non-vacuity');
});

test('R2-8b: a banded state from BEFORE an OFF period, dispatched from again after ON (undo-to-older-state shape)', () => {
  let s = settledCity();
  s = reducer(s, { type: 'place', spec: SLOW_SPEC, x: 5, y: 11 });
  const older = s;
  __setPartitionedDerivationsForTest(false);
  for (let t = 0; t < 5; t++) s = reducer(s, { type: 'tick' });
  __setPartitionedDerivationsForTest(true);
  let back = older;
  const ct = constructionTicks(SPECS[SLOW_SPEC]);
  for (let t = 0; t < ct + 3; t++) back = reducer(back, { type: 'tick' });
  assertNotStale(back, 'R2-8b');
});
