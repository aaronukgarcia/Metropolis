// waste-inc1.test.mjs — FEAT-1972079906 inc1: garbage GENERATION + collection
// COVERAGE + waste-health penalty + collection OPEX.
//
// Run with `npm test` (node --test); node type-strips the imported .ts so these
// assertions exercise the exact shipped formulas. Every test pins REAL numbers
// (hand-computed) and is written to be able to FAIL: mutate a rate or drop the
// online gate and an assertion below goes red.
//
// SCOPE: inc1 only — single residual stream, collection coverage, wellbeing
// penalty, rounds OPEX. Processing / recycling / landfill / diversion are inc2.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  SPECS,
  wasteGeneratedOf,
  collectionCapacityOf,
  collectionCoverageOf,
  wasteStatsOf,
  collectionOpexOf,
  WASTE_PER_RESIDENT,
  WASTE_PER_JOB,
  COLLECTION_OPEX_PER_TONNE,
  constructionTicks,
} from '../src/sim/data.ts';
import { initialState, reducer, wellbeingOf, computeFlows } from '../src/sim/engine.ts';
import { runConsistencyChecks } from '../src/sim/consistency.ts';

/** Advance one full tick via the public reducer. */
const tick = (s) => reducer(s, { type: 'tick' });

const EPS = 1e-9;

/** A city with the starter map cleared, so only the buildings we add matter. */
function bareCity(pop = 0) {
  const s = initialState();
  s.buildings = [];
  s.population = pop;
  return s;
}

let _id = 700000;
/** Push n copies of a spec. `opts` (e.g. { builtTick }) merge onto each building. */
function add(s, spec, n, opts = {}) {
  assert.ok(SPECS[spec], `test setup: unknown spec "${spec}"`);
  for (let i = 0; i < n; i++) {
    s.buildings.push({ id: _id++, spec, x: 5 + (i % 40), y: 5 + Math.floor(i / 40), ...opts });
  }
}

// ───────────────────────── 1. GENERATION ─────────────────────────

test('generation: tonnage from online residents + jobs, hand-computed', () => {
  // Sanity: the placeholder rates are what the maths below assumes.
  assert.equal(WASTE_PER_RESIDENT, 0.01);
  assert.equal(WASTE_PER_JOB, 0.02);

  const s = bareCity(0);
  add(s, 'res_block', 2); // 60 residents each → 2·60·0.01 = 1.2 t
  add(s, 'ind_factory', 1); // industrial, no explicit jobs → 18 jobs → 18·0.02 = 0.36 t
  add(s, 'off_suite', 1); // 25 office jobs → 25·0.02 = 0.50 t

  const expected = 2 * 60 * WASTE_PER_RESIDENT + 18 * WASTE_PER_JOB + 25 * WASTE_PER_JOB; // 2.06
  const gen = wasteGeneratedOf(s);
  assert.ok(Math.abs(gen - expected) < EPS, `generation ${gen} != expected ${expected}`);
  assert.ok(Math.abs(gen - 2.06) < EPS, `generation ${gen} != 2.06 t`);
});

test('generation: an EMPTY city (and the bare starter map) generate zero waste', () => {
  assert.equal(wasteGeneratedOf(bareCity(0)), 0);
  // The real starter map (roads/rails only) also produces nothing.
  const starter = initialState();
  assert.equal(wasteGeneratedOf(starter), 0);
});

test('generation: OFFLINE buildings generate zero (online gate)', () => {
  const online = bareCity(0);
  add(online, 'res_block', 1); // builtTick omitted ⇒ online ⇒ 0.6 t
  assert.ok(Math.abs(wasteGeneratedOf(online) - 0.6) < EPS);

  const offline = bareCity(0);
  // builtTick = tick ⇒ 0 ticks since built < constructionTicks ⇒ under construction ⇒ offline.
  assert.ok(constructionTicks(SPECS.res_block) > 0);
  add(offline, 'res_block', 1, { builtTick: offline.tick });
  assert.equal(wasteGeneratedOf(offline), 0, 'an under-construction (offline) block must generate no waste');
});

// ───────────────────────── 2. COLLECTION COVERAGE ─────────────────────────

test('collection coverage: depots ≥ generation ⇒ coverage 1.0, nothing uncollected', () => {
  const s = bareCity(0);
  add(s, 'res_block', 100); // 100·60·0.01 = 60 t
  add(s, 'waste_depot', 2); // 2·50 = 100 t capacity ≥ 60
  const w = wasteStatsOf(s);
  assert.ok(Math.abs(w.generated - 60) < 1e-6, `generated ${w.generated}`);
  assert.equal(w.capacity, 100);
  assert.equal(w.coverage, 1);
  assert.ok(Math.abs(w.uncollected) < 1e-6, `uncollected ${w.uncollected} should be 0`);
});

test('collection coverage: UNDER capacity ⇒ coverage < 1 at the exact ratio', () => {
  const s = bareCity(0);
  add(s, 'res_block', 100); // 60 t generated
  add(s, 'waste_depot', 1); // 50 t capacity
  const gen = wasteGeneratedOf(s); // ≈ 60
  const w = wasteStatsOf(s);
  assert.equal(w.capacity, 50);
  const expected = 50 / gen; // ≈ 0.8333
  assert.ok(Math.abs(w.coverage - expected) < EPS, `coverage ${w.coverage} != ${expected}`);
  assert.ok(w.coverage > 0.8 && w.coverage < 1, `coverage ${w.coverage} not strictly in (0.8,1)`);
  assert.ok(Math.abs(w.collected - 50) < EPS, `collected ${w.collected} != 50`);
  assert.ok(Math.abs(w.uncollected - (gen - 50)) < EPS, `uncollected ${w.uncollected} != ${gen - 50}`);
});

test('collection coverage: NO depots + generation ⇒ coverage 0 (all uncollected)', () => {
  const s = bareCity(0);
  add(s, 'res_block', 100);
  assert.equal(collectionCapacityOf(s), 0);
  assert.equal(collectionCoverageOf(s), 0);
  assert.ok(Math.abs(wasteStatsOf(s).uncollectedFraction - 1) < EPS);
});

test('collection coverage: no waste at all ⇒ coverage defined as 1 (nothing to collect)', () => {
  const s = bareCity(0); // no generators
  assert.equal(wasteGeneratedOf(s), 0);
  assert.equal(collectionCoverageOf(s), 1);
});

test('collection coverage: an OFFLINE depot collects nothing', () => {
  const s = bareCity(0);
  add(s, 'res_block', 100); // 60 t
  add(s, 'waste_depot', 1, { builtTick: s.tick }); // under construction ⇒ offline
  assert.equal(collectionCapacityOf(s), 0, 'offline depot must contribute no capacity');
  assert.equal(collectionCoverageOf(s), 0);
});

// ───────────────────────── 3. WASTE-HEALTH PENALTY (wellbeing) ─────────────────────────

const refuseOf = (s) => wellbeingOf(s).parts.find((p) => p.label === 'Refuse').value;

test('waste-health: higher uncollected fraction ⇒ lower wellbeing (monotonic)', () => {
  const build = (depots) => {
    const s = bareCity(100000); // pop high ⇒ earlyGameFactor = 1 (no blend toward 55)
    add(s, 'res_block', 100); // 60 t generated
    if (depots > 0) add(s, 'waste_depot', depots);
    return s;
  };
  const full = build(2); // 100 t cap ≥ 60 ⇒ coverage 1
  const partial = build(1); // 50 t cap ⇒ coverage 0.833
  const none = build(0); // coverage 0

  // Refuse part: strictly decreasing as more waste goes uncollected.
  assert.ok(refuseOf(none) < refuseOf(partial), `none ${refuseOf(none)} !< partial ${refuseOf(partial)}`);
  assert.ok(refuseOf(partial) < refuseOf(full), `partial ${refuseOf(partial)} !< full ${refuseOf(full)}`);

  // Overall wellbeing moves the same way (only the Refuse part differs between them).
  const ov = (s) => wellbeingOf(s).overall;
  assert.ok(ov(none) < ov(partial), `overall none ${ov(none)} !< partial ${ov(partial)}`);
  assert.ok(ov(partial) < ov(full), `overall partial ${ov(partial)} !< full ${ov(full)}`);
});

test('waste-health: fully collected ⇒ NO penalty (same Refuse part as a zero-waste city)', () => {
  const zero = bareCity(100000); // no waste generated at all
  const fully = bareCity(100000);
  add(fully, 'res_block', 100);
  add(fully, 'waste_depot', 2); // covers all 60 t

  assert.equal(collectionCoverageOf(fully), 1);
  assert.equal(
    refuseOf(fully),
    refuseOf(zero),
    'a fully-collected city must incur no more waste penalty than a city with no waste'
  );
});

// ───────────────────────── 4. COLLECTION OPEX + CONSERVATION ─────────────────────────

test('collection OPEX: charged ∝ collected tonnage, through the flows', () => {
  const s = bareCity(500);
  add(s, 'res_block', 100); // 60 t generated
  add(s, 'waste_depot', 1); // collects 50 t
  const opex = collectionOpexOf(s);
  const expected = Math.round(50 * COLLECTION_OPEX_PER_TONNE); // 50 t × 12 = 600
  assert.equal(opex, expected);
  assert.equal(opex, 600);

  const { outflows } = computeFlows(s);
  const line = outflows.find((f) => f.label === 'Refuse Collection');
  assert.ok(line, 'expected a "Refuse Collection" outflow line');
  assert.equal(line.value, opex);
});

test('collection OPEX: zero waste ⇒ zero OPEX and no outflow line', () => {
  const s = bareCity(500); // no generators, no depots
  assert.equal(collectionOpexOf(s), 0);
  const { outflows } = computeFlows(s);
  assert.equal(outflows.find((f) => f.label === 'Refuse Collection'), undefined);
});

test('conservation: money is conserved with the refuse OPEX present', () => {
  const s = bareCity(500);
  add(s, 'res_block', 100);
  add(s, 'waste_depot', 1);
  const after = tick(s);

  // The refuse line is really in the recorded flows and matches the OPEX.
  const line = after.lastFlows.outflows.find((f) => f.label === 'Refuse Collection');
  assert.ok(line, 'advance() must record the Refuse Collection outflow');
  assert.equal(line.value, collectionOpexOf(s));

  // Tick-boundary conservation invariant: end = start + Σin − Σout (delta booked exactly).
  const inSum = after.lastFlows.inflows.reduce((a, b) => a + b.value, 0);
  const outSum = after.lastFlows.outflows.reduce((a, b) => a + b.value, 0);
  assert.equal(after.fundsAtTickEnd - after.fundsAtTickStart, inSum - outSum);

  // The full consistency suite (incl. conservation + upkeep reconciliation) stays green.
  const report = runConsistencyChecks(after);
  assert.equal(report.failures, 0, JSON.stringify(report.checks.filter((c) => !c.ok), null, 2));
});

// ───────────────────────── 5. DETERMINISM ─────────────────────────

test('determinism: identical scenarios ⇒ byte-identical waste stats + flows', () => {
  const make = () => {
    _id = 900000; // reset id counter so both builds are structurally identical
    const s = bareCity(500);
    add(s, 'res_block', 37);
    add(s, 'com_retail', 5);
    add(s, 'off_tower', 3);
    add(s, 'waste_depot', 2);
    return s;
  };
  const a = make();
  const b = make();

  assert.equal(JSON.stringify(wasteStatsOf(a)), JSON.stringify(wasteStatsOf(b)));
  assert.equal(collectionOpexOf(a), collectionOpexOf(b));
  assert.equal(refuseOf(a), refuseOf(b));

  // And through a full tick.
  const sa = tick(a);
  const sb = tick(b);
  assert.equal(
    JSON.stringify(sa.lastFlows.outflows.find((f) => f.label === 'Refuse Collection')),
    JSON.stringify(sb.lastFlows.outflows.find((f) => f.label === 'Refuse Collection'))
  );
  assert.equal(JSON.stringify(wasteStatsOf(sa)), JSON.stringify(wasteStatsOf(sb)));
});
