// bug-1172-replan-discard-memo.test.mjs — BUG-1172. A re-plan discarded for an
// invariant failure (MET-V871) is a pure function of its input, so it used to
// re-plan and re-log on EVERY glide pass (14 records in ~8 s). The engine now
// keeps a bounded session memo of discarded plan inputs. replanBox is
// wrapped through the engine's test seam: it calls the REAL planner and
// forces an invariant failure so the discard path is reachable.

import { test, describe, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import { computeRoadConnectivity } from '../src/sim/data.ts';
import { initialState, reducer, CONSOLIDATOR_UNLOCK_LEVEL, xpForLevel, levelOf, replanDiscardMemoTestSeam } from '../src/sim/engine.ts';
import { recentErrors } from '../src/sim/backend.ts';
import { planBox } from '../src/sim/consolidatorReplan.ts';

function mk(over) {
  return {
    ...initialState(),
    unlockedAll: true,
    roadMonitors: [],
    buildingMonitors: [],
    buildings: [],
    population: 0,
    funds: 1_000_000_000,
    tick: 0,
    consolidatorEnabled: false,
    consolidatorLayoutEnabled: true,
    consolidatorLog: [],
    consolidatorMode: 'glide',
    xp: xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL),
    lastRewardedLevel: levelOf(xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL)),
    ...over,
  };
}

function healthy(s) {
  let cur = { ...s, consolidatorLayoutEnabled: false };
  cur = reducer(cur, { type: 'tick' });
  return { ...cur, consolidatorLayoutEnabled: s.consolidatorLayoutEnabled, tick: s.tick, consolidatorLog: s.consolidatorLog ?? [] };
}

const countCode = (code) => recentErrors().filter((e) => e.code === code).length;

function fixture(extraRoads = 0) {
  const buildings = [];
  let id = 1;
  for (let x = 0; x < 32; x++) buildings.push({ id: id++, spec: 'road', x, y: 8, builtTick: -1000 });
  for (let i = 0; i < extraRoads; i++) buildings.push({ id: id++, spec: 'road', x: 5, y: 9 + i, builtTick: -1000 });
  const base = mk({ buildings, population: 1_000, funds: 500_000_000 });
  const s = { ...base, roadConnectivity: computeRoadConnectivity(base) };
  return reducer(healthy(s), { type: 'toggleConsolidator' });
}

function install(failing) {
  const stats = { calls: 0, perInput: new Map() };
  const memo = replanDiscardMemoTestSeam((input) => {
    stats.calls++;
    const k = JSON.stringify(input);
    stats.perInput.set(k, (stats.perInput.get(k) ?? 0) + 1);
    const plan = planBox(input);
    return failing(input) ? { ...plan, invariantFailures: ['forced: test seam'] } : plan;
  });
  return { stats, memo };
}

function run(s, n) {
  for (let i = 0; i < n; i++) s = reducer(s, { type: 'tick' });
  return s;
}

describe('BUG-1172: discarded re-plan memo', () => {
  afterEach(() => replanDiscardMemoTestSeam(null));

  test('a discarded plan is planned and logged ONCE per key, not every glide pass', () => {
    const { stats, memo } = install(() => true);
    const before = countCode('MET-V871');
    const s = run(fixture(), 40);
    const logged = countCode('MET-V871') - before;
    assert.ok(logged >= 1, 'the forced failure really reached MET-V871');
    assert.ok(memo.size() < 64, 'stayed under the memo bound so the ring count is exact');
    assert.equal(logged, memo.size(), 'exactly one record per remembered key');
    assert.equal(stats.calls, memo.size(), 'replanBox ran once per discarded key, never again for it');
    assert.equal(Math.max(...stats.perInput.values()), 1, 'no identical input was ever planned twice');
    assert.ok((s.consolidatorLog ?? []).length > 0);
  });

  test('the SAME state re-reduced (re-pass / StrictMode double invoke) re-plans and re-logs nothing', () => {
    const { stats, memo } = install(() => true);
    let s = fixture();
    // Advance until a tick actually discards (the glide day arrives).
    let guard = 0;
    while (memo.size() === 0 && guard++ < 200) s = reducer(s, { type: 'tick' });
    assert.ok(memo.size() > 0, 'a discard happened');
    // Find a state whose next tick hits the replan stage; re-reduce it 5 more times.
    const callsBefore = stats.calls;
    const v871Before = countCode('MET-V871');
    const first = reducer(s, { type: 'tick' });
    const afterFirstCalls = stats.calls;
    const afterFirstLogs = countCode('MET-V871');
    for (let i = 0; i < 5; i++) {
      const again = reducer(s, { type: 'tick' });
      assert.equal(JSON.stringify(again.consolidatorLog?.[0]?.skipped), JSON.stringify(first.consolidatorLog?.[0]?.skipped), 'identical outcome on a memo hit');
    }
    assert.equal(stats.calls, afterFirstCalls, 'replanBox not re-invoked for an already-discarded key');
    assert.equal(countCode('MET-V871'), afterFirstLogs, 'MET-V871 not re-logged');
    assert.ok(callsBefore <= afterFirstCalls && v871Before <= afterFirstLogs);
  });

  // First pre-state whose next tick reaches the planner, with its input.
  function firstPlanning(failing = () => true) {
    const { stats, memo } = install(failing);
    let s = fixture();
    let pre = s;
    for (let i = 0; i < 200 && stats.calls === 0; i++) { pre = s; s = reducer(s, { type: 'tick' }); }
    assert.ok(stats.calls > 0, 'reached the planner');
    return { pre, input: stats.inputs0 ?? null, stats, memo, next: s };
  }
  const canon = (s) => JSON.stringify(s, (_k, v) => (v instanceof Map ? ['__map', [...v.entries()]] : v instanceof Set ? ['__set', [...v]] : v));

  test('ONLY contents change (same box, ports, phase): a different key, re-planned', () => {
    const { stats } = install(() => true);
    let s = fixture();
    let pre = s;
    const lastInputs = [];
    const orig = stats;
    for (let i = 0; i < 200 && orig.calls === 0; i++) { pre = s; s = reducer(s, { type: 'tick' }); }
    assert.equal(orig.calls, 1);
    // Re-install to capture the input the engine builds for `pre`.
    let captured = null;
    replanDiscardMemoTestSeam((input) => { captured = input; return { ...planBox(input), invariantFailures: ['forced'] }; });
    reducer(pre, { type: 'tick' });
    assert.ok(captured, 'captured the planner input');
    const box = captured.box;
    const occupied = new Set(pre.buildings.map((b) => `${b.x},${b.y}`));
    let spot = null;
    for (let dy = 0; dy < box.h && !spot; dy++) for (let dx = 0; dx < box.w && !spot; dx++) if (!occupied.has(`${box.x0 + dx},${box.y0 + dy}`)) spot = { x: box.x0 + dx, y: box.y0 + dy };
    assert.ok(spot, 'free tile inside the box');
    const maxId = Math.max(...pre.buildings.map((b) => b.id));
    const variant = { ...pre, buildings: [...pre.buildings, { id: maxId + 1, spec: 'res_terrace', x: spot.x, y: spot.y, builtTick: -1000 }] };
    // Warm memo already holds `pre`'s input; count planner calls from here.
    let calls = 0;
    let seenInput = null;
    // Re-warm: discard pre's input first.
    replanDiscardMemoTestSeam((input) => { calls++; return { ...planBox(input), invariantFailures: ['forced'] }; });
    reducer(pre, { type: 'tick' });
    assert.equal(calls, 1);
    reducer(pre, { type: 'tick' });
    assert.equal(calls, 1, 'identical input is a memo hit');
    reducer(variant, { type: 'tick' });
    seenInput = calls;
    assert.equal(seenInput, 2, 'contents-only change was planned afresh');
    void lastInputs;
  });

  test('a hit reached with a NON-NULL dwell yields the byte-identical state to a first-time failure', () => {
    const { pre } = firstPlanning();
    const dwelling = { ...pre, consolidatorReplanDwellStartTick: pre.tick };
    let calls = 0;
    replanDiscardMemoTestSeam((input) => { calls++; return { ...planBox(input), invariantFailures: ['forced'] }; });
    const cold = reducer(dwelling, { type: 'tick' });
    assert.equal(calls, 1);
    assert.equal(cold.consolidatorReplanDwellStartTick, null, 'a discard clears the dwell');
    const warm = reducer(dwelling, { type: 'tick' });
    assert.equal(calls, 1, 'second reduce is a memo hit');
    assert.equal(canon(warm), canon(cold), 'hit state byte-identical to first-time failure');
  });

  test('an evicted key is planned again; the memo never exceeds 64', () => {
    const { stats, memo } = install(() => true);
    let s = fixture();
    let pre0 = null;
    for (let i = 0; i < 400 && !(memo.size() === 64 && stats.calls > 64); i++) {
      const before = stats.calls;
      const pre = s;
      s = reducer(s, { type: 'tick' });
      if (pre0 === null && stats.calls > before) pre0 = pre;
      assert.ok(memo.size() <= 64);
    }
    assert.equal(memo.size(), 64);
    assert.ok(stats.calls > 64, 'more distinct discards than the bound');
    assert.ok(pre0, 'recorded the first planned pre-state');
    const before = stats.calls;
    reducer(pre0, { type: 'tick' });
    assert.equal(stats.calls, before + 1, 'the evicted (oldest) key re-plans');
  });

  test('changed contents (a different key) re-plan', () => {
    const { stats, memo } = install(() => true);
    run(fixture(), 60);
    const keysA = memo.size();
    const callsA = stats.calls;
    run(fixture(3), 60);
    assert.ok(memo.size() > keysA && stats.calls > callsA, 'changed inputs were planned afresh');
  });

  test('a sound plan is never memoised', () => {
    const { memo } = install(() => false);
    run(fixture(), 60);
    assert.equal(memo.size(), 0);
  });

  test('determinism: the same sequence gives the same outcome with a warm memo', () => {
    install(() => true);
    const a = run(fixture(), 100);
    const b = run(fixture(), 100);
    const strip = (s) => JSON.stringify({ b: s.buildings, f: s.funds, t: s.tick, l: (s.consolidatorLog ?? []).map((p) => p.skipped) });
    assert.equal(strip(b), strip(a));
  });
});
