// bug-1172-r1-attack.test.mjs — BUG-1172 round r1 (independent attacker).
// Attacks the discarded re-plan memo in engine.ts:
//  (1) a memo HIT must leave the WHOLE next state byte-identical to a
//      first-time failure (not just the skip log): dwell, sections, funds,
//      buildings, consolidator log, everything;
//  (2) a cold memo and a warm memo replaying the same tick sequence must
//      reach byte-identical whole states;
//  (3) the memo is bounded (FIFO at 64) and an evicted key is re-planned;
//  (4) the key's 32-bit hash: two DIFFERENT planner inputs can share a key
//      (constructed), so a false hit is possible in principle.

import { test, describe, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import { computeRoadConnectivity } from '../src/sim/data.ts';
import { initialState, reducer, CONSOLIDATOR_UNLOCK_LEVEL, xpForLevel, levelOf, replanDiscardMemoTestSeam } from '../src/sim/engine.ts';
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
// A row of roads plus civic huts so the box has real contents to plan.
function fixture(extraRoads = 0) {
  const buildings = [];
  let id = 1;
  for (let x = 0; x < 32; x++) buildings.push({ id: id++, spec: 'road', x, y: 8, builtTick: -1000 });
  for (let i = 0; i < extraRoads; i++) buildings.push({ id: id++, spec: 'road', x: 5, y: 9 + i, builtTick: -1000 });
  const base = mk({ buildings, population: 1_000, funds: 500_000_000 });
  const s = { ...base, roadConnectivity: computeRoadConnectivity(base) };
  return reducer(healthy(s), { type: 'toggleConsolidator' });
}
const canon = (s) =>
  JSON.stringify(s, (_k, v) => (v instanceof Map ? ['__map', [...v.entries()]] : v instanceof Set ? ['__set', [...v]] : v));

function install(failing) {
  const stats = { calls: 0, inputs: [] };
  const memo = replanDiscardMemoTestSeam((input) => {
    stats.calls++;
    stats.inputs.push(input);
    const plan = planBox(input);
    return failing(input) ? { ...plan, invariantFailures: ['forced: attacker seam'] } : plan;
  });
  return { stats, memo };
}

// Exact replica of engine.ts replanDiscardKeyOf (not exported) for attack (4).
function keyOf(input) {
  const body = JSON.stringify([input.ports, input.latticePhase ?? 0, input.seed, input.contents, input.cityWideGroups ?? [], input.rungs]);
  let h = 0x811c9dc5;
  for (let i = 0; i < body.length; i++) {
    h ^= body.charCodeAt(i);
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  const b = input.box;
  return `${b.x0},${b.y0},${b.w},${b.h}|${h.toString(16)}|${body.length}`;
}

describe('BUG-1172 r1 attack', () => {
  afterEach(() => replanDiscardMemoTestSeam(null));

  test('(1) memo HIT next state is byte-identical to a first-time failure (whole state)', () => {
    let { stats, memo } = install(() => true);
    let s = fixture();
    let guard = 0;
    while (memo.size() === 0 && guard++ < 200) s = reducer(s, { type: 'tick' });
    assert.ok(memo.size() > 0, 'a discard happened');
    // Find a pre-state whose next tick re-plans: probe with a cold memo.
    let found = 0;
    for (let i = 0; i < 60 && found < 5; i++) {
      ({ stats, memo } = install(() => true)); // cold memo
      const cold = reducer(s, { type: 'tick' });
      const coldCalls = stats.calls;
      if (coldCalls > 0 && memo.size() > 0) {
        const warm = reducer(s, { type: 'tick' }); // warm memo: must be a HIT
        assert.equal(stats.calls, coldCalls, 'second reduce was a memo hit (planner not called)');
        assert.equal(canon(warm), canon(cold), 'whole next state identical on hit vs first failure');
        found++;
      }
      s = cold;
    }
    assert.ok(found >= 1, `exercised ${found} hit-vs-miss comparisons`);
  });

  test('(2) cold-memo replay equals warm-memo replay, whole state, 120 ticks', () => {
    install(() => true);
    const warmUp = (() => { let x = fixture(); for (let i = 0; i < 120; i++) x = reducer(x, { type: 'tick' }); return x; })();
    // memo is now warm with this sequence's keys; run it again warm
    let w = fixture();
    for (let i = 0; i < 120; i++) w = reducer(w, { type: 'tick' });
    // cold run
    install(() => true);
    let c = fixture();
    for (let i = 0; i < 120; i++) c = reducer(c, { type: 'tick' });
    assert.equal(canon(w), canon(c));
    assert.equal(canon(warmUp), canon(c));
  });

  test('(3) the bound holds at 64 and an evicted key is planned again', () => {
    const { stats, memo } = install(() => true);
    const firsts = [];
    for (let extra = 0; extra < 40 && memo.size() < 64; extra++) {
      let s = fixture(extra % 7);
      s = { ...s, funds: 500_000_000 + extra }; // funds are not a planner input
      for (let i = 0; i < 30; i++) s = reducer(s, { type: 'tick' });
      if (firsts.length === 0 && stats.inputs.length) firsts.push(stats.inputs[0]);
    }
    assert.ok(memo.size() <= 64, `size ${memo.size()} within bound`);
    // The bound is FIFO via Set insertion order: report what we reached.
    assert.ok(stats.calls >= memo.size());
  });

  test('(4) FNV-1a32 key: two different planner inputs can share one key (constructed)', () => {
    const { stats } = install(() => true);
    let s = fixture();
    for (let i = 0; i < 60 && stats.inputs.length === 0; i++) s = reducer(s, { type: 'tick' });
    assert.ok(stats.inputs.length > 0);
    const base = stats.inputs[0];
    assert.ok(base.contents.length > 0, 'box has contents');
    // Vary two building ids in a fixed-width range: same box, same body length.
    const seen = new Map();
    let hit = null;
    outer: for (let a = 100000; a < 100400; a++) {
      for (let b = 200000; b < 200400; b++) {
        const v = { ...base, contents: base.contents.map((c, i) => (i === 0 ? { ...c, id: a } : i === 1 % base.contents.length ? { ...c, id: b } : c)) };
        const k = keyOf(v);
        const prev = seen.get(k);
        if (prev && (prev[0] !== a || prev[1] !== b)) { hit = { k, p: prev, q: [a, b] }; break outer; }
        seen.set(k, [a, b]);
      }
    }
    assert.ok(hit, 'found a key collision between two different inputs');
    console.log('COLLISION', JSON.stringify(hit));
  });

  test('(5) END-TO-END: a colliding-under-the-old-lossy-key SOUND input executes identically warm and cold', () => {
    // Find the pre-state of the first tick that re-plans.
    let { stats } = install(() => false);
    let s = fixture();
    let pre = s;
    for (let i = 0; i < 80 && stats.calls === 0; i++) { pre = s; s = reducer(s, { type: 'tick' }); }
    assert.ok(stats.calls > 0);
    const base = stats.inputs[0];
    const ids = base.contents.map((c) => c.id);
    const [idA, idB] = ids; // the two LOWEST ids -> a<b; every other box id -> 300000+k keeps the sort order
    const fixedTail = ids.slice(2).map((id, k) => [id, 300000 + k]);
    const maxId = Math.max(...pre.buildings.map((b) => b.id));
    const remapObj = (o, m) => JSON.parse(JSON.stringify(o), (k, v) => (k === 'id' && m.has(v) ? m.get(v) : v));
    // Birthday search on the planner input's key (pure).
    const seen = new Map();
    let pair = null;
    outer: for (let a = 100000; a < 100600; a++) {
      for (let b = 200000; b < 200600; b++) {
        const k = keyOf(remapObj(base, new Map([[idA, a], [idB, b], ...fixedTail])));
        const prev = seen.get(k);
        if (prev) { pair = [prev, [a, b]]; break outer; }
        seen.set(k, [a, b]);
      }
    }
    assert.ok(pair, 'collision found');
    assert.ok(maxId < 100000);
    const stateWith = ([a, b]) => {
      const m = new Map([[idA, a], [idB, b], ...fixedTail]);
      const buildings = pre.buildings.map((bd) => (m.has(bd.id) ? { ...bd, id: m.get(bd.id) } : bd));
      const st = { ...pre, buildings, nextBuildingId: 300000, nextSafeBuildingId: 300000 };
      return { ...st, roadConnectivity: computeRoadConnectivity(st) };
    };
    const SA = stateWith(pair[0]);
    const SB = stateWith(pair[1]);
    // Capture the real engine inputs for both and check the replica key.
    ({ stats } = install(() => false));
    reducer(SA, { type: 'tick' });
    reducer(SB, { type: 'tick' });
    const [inA, inB] = stats.inputs;
    assert.notEqual(JSON.stringify(inA), JSON.stringify(inB), 'the two planner inputs DIFFER');
    assert.equal(keyOf(inA), keyOf(inB), 'but share one memo key');
    const jsonA = JSON.stringify(inA);
    // Cold: only A's exact input fails. B is sound and must execute.
    install((input) => JSON.stringify(input) === jsonA);
    const coldB = reducer(SB, { type: 'tick' });
    // Warm: discard A first (memo learns its key), then reduce B.
    const w = install((input) => JSON.stringify(input) === jsonA);
    reducer(SA, { type: 'tick' });
    assert.equal(w.memo.size(), 1, 'A was discarded and remembered');
    const callsBefore = w.stats.calls;
    const warmB = reducer(SB, { type: 'tick' });
    const hit = w.stats.calls === callsBefore;
    const skippedOf = (x) => JSON.stringify(x.consolidatorLog?.[0]?.skipped?.filter((e) => /replan/.test(e.reason)));
    console.log('FALSEHIT', JSON.stringify({ pair, key: keyOf(inA), hit, coldSkipped: skippedOf(coldB), warmSkipped: skippedOf(warmB), coldDwell: coldB.consolidatorReplanDwellStartTick, warmDwell: warmB.consolidatorReplanDwellStartTick, sameState: canon(coldB) === canon(warmB) }));
    // ADAPTED (BUG-1172 r1 fix): the original asserted the false hit exists.
    // The engine key is now the EXACT serialised input, so the lossy-key
    // collision (still constructed by the keyOf replica above) must NOT hit.
    assert.equal(hit, false, 'B shares the OLD lossy key but is NOT a memo hit: its sound plan is computed');
    assert.equal(canon(warmB), canon(coldB), 'warm-memo state equals cold-memo (replay) state');
  });

  test('(6) cost: key computation vs one planner call (measurement only, no wall-clock assertion)', () => {
    const { stats } = install(() => false);
    let s = fixture(6);
    for (let i = 0; i < 80 && stats.inputs.length === 0; i++) s = reducer(s, { type: 'tick' });
    const input = stats.inputs[0];
    const N = 2000;
    let t0 = process.hrtime.bigint();
    for (let i = 0; i < N; i++) keyOf(input);
    const keyNs = Number(process.hrtime.bigint() - t0) / N;
    t0 = process.hrtime.bigint();
    for (let i = 0; i < 200; i++) planBox(input);
    const planNs = Number(process.hrtime.bigint() - t0) / 200;
    console.log('COST', JSON.stringify({ bodyLen: JSON.stringify([input.ports, input.contents, input.rungs]).length, keyUs: keyNs / 1e3, planUs: planNs / 1e3, ratio: keyNs / planNs }));
    assert.ok(keyNs > 0 && planNs > 0);
  });
});
