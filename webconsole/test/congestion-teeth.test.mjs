// congestion-teeth.test.mjs — FEAT-congestion-teeth-2026-09-02 (Q100057 A1
// Aaron-approved, Q100071 rec-on-all): sustained road/motorway saturation
// imposes a Traffic/Commute wellbeing penalty (+ AC-9 income drag). RED-proofs
// for the spec's 9 acceptance criteria
// (docs/planning/acceptance/FEAT-congestion-teeth-2026-09-02.md).
//
// Every test states its OWN mutant/failure scenario per the AC doc so a
// broken implementation demonstrably fails these, not just "looks green".

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  SPECS,
  lineUsageOf,
  congestionLinesOf,
  congestionFactorOf,
  advanceCongestionTicks,
  sanitizeCongestionTicksBySpec,
  CONGESTION_CONSTANTS,
} from '../src/sim/data.ts';
import { initialState, wellbeingOf, reducer, computeFlows, TICKS_PER_MONTH } from '../src/sim/engine.ts';

const { CONGESTION_PENALTY_THRESHOLD, CONGESTION_SUSTAINED_TICKS, CONGESTION_INCOME_K } = CONGESTION_CONSTANTS;

/** A city: initial state + population + extra buildings (same helper shape
 *  as crime-mechanic.test.mjs — coordinates don't affect the traffic math). */
// NOTE: unlike crime-mechanic.test.mjs's helper, this one REPLACES the
// starter city's own road network (buildings = []) rather than pushing onto
// it — the traffic model (lineUsageOf) shares capacity across EVERY road
// tile on the map, so leaving the starter roads in place would dilute/inflate
// the saturation the tests are trying to pin exactly.
function city(pop, specCounts = {}, overrides = {}) {
  const s = initialState();
  s.population = pop;
  s.buildings = [];
  let id = 91000;
  let slot = 0;
  for (const [spec, n] of Object.entries(specCounts)) {
    assert.ok(SPECS[spec], `test setup: unknown spec "${spec}"`);
    for (let i = 0; i < n; i++) {
      s.buildings.push({ id: id++, spec, x: 5 + (slot % 40) * 5, y: 5 + Math.floor(slot / 40) * 5 });
      slot++;
    }
  }
  return { ...s, ...overrides };
}

/**
 * A single tier-1 'road' tile plus enough res_highrise (600 residents each,
 * feederTrafficWeight = residents, ROAD_TRAFFIC_POP_WEIGHT=1) to push that
 * one road's saturation to 1.0 (activity=1 once population >= 500). Capacity
 * of a single tier-1 road tile is ROAD_TIER_CAPACITY[1] = 100 (data.ts), so
 * even ONE res_highrise (600 feeder weight) massively oversaturates it.
 */
function saturatedRoadCity(overrides = {}) {
  return city(2000, { road: 1, res_highrise: 1 }, overrides);
}

/** A road network with generous tier-5 (m20) capacity relative to the same
 *  population — saturation stays comfortably under threshold. */
function uncongestedRoadCity(overrides = {}) {
  return city(2000, { m20: 4, res_highrise: 1 }, overrides);
}

function tickN(s, n) {
  let out = s;
  for (let i = 0; i < n; i++) out = reducer(out, { type: 'tick' });
  return out;
}

// ---------------------------------------------------------------------------
// AC-1: sustained congestion is measurable
// ---------------------------------------------------------------------------
test('AC-1: a saturated line accrues sustained ticks and isSustained flips true at the threshold (mutant: isSustained hardcoded false / factor stuck at 1.0)', () => {
  let s = saturatedRoadCity();
  // Pre-check: the road really is saturated from tick 0 (test setup sanity).
  const usage0 = lineUsageOf(s).find((l) => l.spec === 'road');
  assert.ok(usage0, 'test setup: no road line present');
  assert.ok(usage0.saturation >= CONGESTION_PENALTY_THRESHOLD, `test setup must saturate the road, got ${usage0.saturation}`);

  s = tickN(s, CONGESTION_SUSTAINED_TICKS - 1);
  let lines = congestionLinesOf(s);
  let road = lines.find((l) => l.spec === 'road');
  assert.ok(road, 'road line missing from congestionLinesOf');
  assert.strictEqual(road.isSustained, false, `must NOT be sustained one tick short of the threshold, got sustainedTicks=${road.sustainedTicks}`);
  assert.strictEqual(road.congestionFactor, 1, 'congestion factor must still be 1.0 (no penalty) before sustained');

  s = reducer(s, { type: 'tick' }); // the exact tick that crosses the threshold
  lines = congestionLinesOf(s);
  road = lines.find((l) => l.spec === 'road');
  assert.strictEqual(road.isSustained, true, `must be sustained at exactly ${CONGESTION_SUSTAINED_TICKS} ticks, got sustainedTicks=${road.sustainedTicks}`);
  assert.ok(road.congestionFactor < 1, `congestion factor must reflect the saturation excess once sustained, got ${road.congestionFactor}`);
});

// ---------------------------------------------------------------------------
// AC-2: wellbeing has a Traffic/Commute part
// ---------------------------------------------------------------------------
test('AC-2: wellbeingOf() exposes a Traffic/Commute part responsive to congestion (mutant: part omitted, or hardcoded)', () => {
  const uncongested = wellbeingOf(uncongestedRoadCity());
  const before = uncongested.parts.find((p) => p.label === 'Traffic/Commute');
  assert.ok(before, 'Traffic/Commute part missing from wellbeingOf().parts');
  assert.strictEqual(before.value, 100, `uncongested Traffic/Commute part must read 100, got ${before.value}`);

  const congestedState = tickN(saturatedRoadCity(), CONGESTION_SUSTAINED_TICKS);
  const congested = wellbeingOf(congestedState);
  const after = congested.parts.find((p) => p.label === 'Traffic/Commute');
  assert.ok(after, 'Traffic/Commute part missing after sustained congestion');
  assert.ok(after.value < before.value, `Traffic/Commute part must drop once sustained: before=${before.value}, after=${after.value}`);
});

// ---------------------------------------------------------------------------
// AC-3: wellbeing overall drops when congestion is sustained
// ---------------------------------------------------------------------------
test('AC-3: wellbeing overall decreases once a network transitions to sustained congestion (mutant: part not folded into overall)', () => {
  const overallBefore = wellbeingOf(saturatedRoadCity()).overall; // not sustained yet (tick 0)
  const congestedState = tickN(saturatedRoadCity(), CONGESTION_SUSTAINED_TICKS);
  const overallAfter = wellbeingOf(congestedState).overall;
  assert.ok(overallAfter < overallBefore, `overall must fall once congestion is sustained: before=${overallBefore}, after=${overallAfter}`);
});

// ---------------------------------------------------------------------------
// AC-4: uncongested network has zero penalty
// ---------------------------------------------------------------------------
test('AC-4: an uncongested network (generous capacity) never accrues a penalty, even across many ticks (mutant: factor drifts below 1.0 regardless of saturation)', () => {
  let s = uncongestedRoadCity();
  const usage = lineUsageOf(s).find((l) => l.spec === 'm20');
  assert.ok(usage.saturation < CONGESTION_PENALTY_THRESHOLD, `test setup must stay under threshold, got ${usage.saturation}`);

  s = tickN(s, CONGESTION_SUSTAINED_TICKS * 2);
  assert.strictEqual(congestionFactorOf(s), 1, 'congestionFactorOf must stay exactly 1.0 for an uncongested network');
  const wb = wellbeingOf(s);
  const traffic = wb.parts.find((p) => p.label === 'Traffic/Commute');
  assert.strictEqual(traffic.value, 100, `Traffic/Commute part must stay 100 with zero penalty, got ${traffic.value}`);
});

// ---------------------------------------------------------------------------
// Burst tolerance: a SHORT spike under the sustained window imposes nothing
// (Aaron's auto-road-improvement design language / AC-4's "or below
// CONGESTION_SUSTAINED_TICKS elapsed" clause)
// ---------------------------------------------------------------------------
test('burst tolerance: a saturation spike shorter than CONGESTION_SUSTAINED_TICKS never sustains or penalizes (mutant: counter never resets / sustains early)', () => {
  let s = saturatedRoadCity();
  const burst = Math.floor(CONGESTION_SUSTAINED_TICKS / 2);
  s = tickN(s, burst); // half the sustained window, still saturated
  let road = congestionLinesOf(s).find((l) => l.spec === 'road');
  assert.strictEqual(road.isSustained, false, 'a half-window burst must not count as sustained');

  // Relieve the road (bulldoze the traffic source) BEFORE the window closes.
  s = { ...s, buildings: s.buildings.filter((b) => b.spec !== 'res_highrise') };
  s = tickN(s, burst); // ample time for the counter to have crossed threshold had it not reset
  road = congestionLinesOf(s).find((l) => l.spec === 'road');
  assert.ok(!road || road.sustainedTicks === 0, `counter must have reset once relieved, got sustainedTicks=${road && road.sustainedTicks}`);
  assert.strictEqual(congestionFactorOf(s), 1, 'a relieved burst must never have imposed a lasting penalty');
});

// ---------------------------------------------------------------------------
// AC-5: widening a congested road relieves the penalty
// ---------------------------------------------------------------------------
test('AC-5: widening a sustained-congested road relieves the penalty within one sustained window (mutant: counter never resets after capacity increases)', () => {
  let s = tickN(saturatedRoadCity(), CONGESTION_SUSTAINED_TICKS);
  assert.ok(congestionFactorOf(s) < 1, 'test setup: must be sustained-congested before widening');
  const wbBefore = wellbeingOf(s).overall;

  // "Widen" the road: since this is the ONLY line class present, ALL city
  // traffic routes onto it regardless of capacity (lineUsageOf's share-by-
  // capacity math degenerates to 100% for a single class) — so widening
  // must grow capacity past totalTraffic/threshold, not just "more than
  // before". Add nine more tier-1 tiles (10x total capacity = 1000, well
  // above the ~600 feeder weight / 0.75 = 800 needed to clear the threshold).
  const widenedTiles = Array.from({ length: 9 }, (_, i) => ({ id: 99001 + i, spec: 'road', x: 90 + i * 5, y: 5 }));
  s = { ...s, buildings: [...s.buildings, ...widenedTiles] };
  const usageAfterWiden = lineUsageOf(s).find((l) => l.spec === 'road');
  assert.ok(usageAfterWiden.saturation < CONGESTION_PENALTY_THRESHOLD, `widening must drop saturation under threshold, got ${usageAfterWiden.saturation}`);

  s = tickN(s, CONGESTION_SUSTAINED_TICKS); // give the counter a full window to reset+confirm
  assert.strictEqual(congestionFactorOf(s), 1, 'congestion factor must fully recover once widened');
  const wbAfter = wellbeingOf(s).overall;
  assert.ok(wbAfter > wbBefore, `wellbeing overall must rise after widening relieves congestion: before=${wbBefore}, after=${wbAfter}`);
});

// ---------------------------------------------------------------------------
// AC-6: congestion factor is bounded [0,1]
// ---------------------------------------------------------------------------
test('AC-6: congestion factor and wellbeing part stay bounded even at extreme oversaturation (mutant: ramp escapes [0,1] / part goes negative)', () => {
  // Massively oversaturate: many high-traffic buildings on a single road tile.
  let s = city(50000, { road: 1, res_highrise: 20 });
  s = tickN(s, CONGESTION_SUSTAINED_TICKS * 3);
  const factor = congestionFactorOf(s);
  assert.ok(Number.isFinite(factor), `congestion factor must be finite, got ${factor}`);
  assert.ok(factor >= 0 && factor <= 1, `congestion factor must stay in [0,1], got ${factor}`);
  const wb = wellbeingOf(s);
  const traffic = wb.parts.find((p) => p.label === 'Traffic/Commute');
  assert.ok(traffic.value >= 0 && traffic.value <= 100, `Traffic/Commute part must stay in [0,100], got ${traffic.value}`);
  assert.ok(Number.isFinite(wb.overall), `overall wellbeing must stay finite, got ${wb.overall}`);
});

// ---------------------------------------------------------------------------
// AC-7: deterministic penalty
// ---------------------------------------------------------------------------
test('AC-7: congestion factor and wellbeing are deterministic — identical states replay identically (mutant: Math.random()/Date.now()/map-order creeps in)', () => {
  const s = saturatedRoadCity();
  // Same-state repeat calls (no tick advance).
  assert.strictEqual(congestionFactorOf(s), congestionFactorOf(s));
  assert.deepStrictEqual(congestionLinesOf(s), congestionLinesOf(s));

  // Two independent replays through the counter's full lifecycle (build up,
  // then widen) must produce byte-identical SimState congestion fields.
  function replay() {
    let st = tickN(saturatedRoadCity(), CONGESTION_SUSTAINED_TICKS + 10);
    st = { ...st, buildings: [...st.buildings, { id: 99010, spec: 'road', x: 90, y: 5 }] };
    st = tickN(st, CONGESTION_SUSTAINED_TICKS);
    return st;
  }
  const r1 = replay();
  const r2 = replay();
  assert.deepStrictEqual(r1.congestionTicksBySpec, r2.congestionTicksBySpec);
  assert.strictEqual(congestionFactorOf(r1), congestionFactorOf(r2));
  assert.deepStrictEqual(wellbeingOf(r1), wellbeingOf(r2));
});

// ---------------------------------------------------------------------------
// AC-8: penalty visible to player (generic parts.map renders every label —
// verified here at the data level; UI is not touched per the build brief)
// ---------------------------------------------------------------------------
test('AC-8: Traffic/Commute is a first-class labelled part alongside every other wellbeing part (mutant: part computed but not appended to parts[])', () => {
  const wb = wellbeingOf(tickN(saturatedRoadCity(), CONGESTION_SUSTAINED_TICKS));
  const labels = wb.parts.map((p) => p.label);
  assert.ok(labels.includes('Traffic/Commute'), `Traffic/Commute must be a rendered part, got labels=${JSON.stringify(labels)}`);
  // Every part must carry a finite numeric value the generic parts.map()
  // renderer (populationTabs.tsx) can bar-chart without special-casing.
  for (const p of wb.parts) {
    assert.ok(Number.isFinite(p.value), `part "${p.label}" must have a finite value`);
  }
});

// ---------------------------------------------------------------------------
// AC-9: income penalty on congestion (Q100057/Q100071 rec-on-all: INCLUDED)
// ---------------------------------------------------------------------------
test('AC-9: sustained congestion reduces Business/Freight/Office Tax inflows (mutant: income untouched by congestion)', () => {
  // A commercial city (Business Tax present) PLUS a heavy traffic source
  // (res_highrise) so the road/motorway actually carries load — com_shop
  // alone has negligible feeder weight (data.ts feederTrafficWeight).
  function commuteCity(specCounts) {
    return city(2000, { com_shop: 4, res_highrise: 1, ...specCounts });
  }
  const uncongested = tickN(commuteCity({ m20: 4 }), CONGESTION_SUSTAINED_TICKS);
  const congested = tickN(commuteCity({ road: 1 }), CONGESTION_SUSTAINED_TICKS);
  assert.ok(congestionFactorOf(uncongested) === 1, 'uncongested control must have zero congestion');
  assert.ok(congestionFactorOf(congested) < 1, 'congested case must actually be sustained-congested');

  const flowsUncongested = computeFlows(uncongested).inflows;
  const flowsCongested = computeFlows(congested).inflows;
  const bizUncongested = flowsUncongested.find((f) => f.label === 'Business Tax');
  const bizCongested = flowsCongested.find((f) => f.label === 'Business Tax');
  assert.ok(bizUncongested && bizCongested, 'Business Tax inflow missing from computeFlows');
  assert.ok(
    bizCongested.value < bizUncongested.value,
    `congested Business Tax must be lower: uncongested=${bizUncongested.value}, congested=${bizCongested.value}`
  );
  // Bounded: the loss can never exceed CONGESTION_INCOME_K (the fully-
  // penalized case, congestionFactor -> 0) of the uncongested basis.
  const maxLoss = bizUncongested.value * CONGESTION_INCOME_K;
  assert.ok(
    bizUncongested.value - bizCongested.value <= maxLoss + 1, // +1 rounding slack
    `congestion income loss must not exceed CONGESTION_INCOME_K=${CONGESTION_INCOME_K} of the basis`
  );
});

// ---------------------------------------------------------------------------
// GR#16 — corruption shapes for the stored counter
// ---------------------------------------------------------------------------
test('GR#16: sanitizeCongestionTicksBySpec never lets a corrupt save produce NaN/negative/oversized counters (mutant: bare ?? guard, no per-entry coercion)', () => {
  const shapes = [
    undefined,
    null,
    'abc',
    42,
    [],
    ['road'],
    { road: 'abc' },
    { road: -5 },
    { road: 1e9 },
    { road: NaN },
    { road: Infinity },
    { road: 12.7 },
    { road: {} },
    { '': 5 },
  ];
  for (const bad of shapes) {
    const out = sanitizeCongestionTicksBySpec(bad);
    assert.strictEqual(typeof out, 'object');
    assert.ok(out !== null);
    for (const [k, v] of Object.entries(out)) {
      assert.ok(Number.isFinite(v), `sanitizeCongestionTicksBySpec(${JSON.stringify(bad)}) entry "${k}" not finite: ${v}`);
      assert.ok(v >= 0 && v <= CONGESTION_SUSTAINED_TICKS, `entry "${k}" out of [0,${CONGESTION_SUSTAINED_TICKS}]: ${v}`);
      assert.ok(Number.isInteger(v), `entry "${k}" not an integer: ${v}`);
    }
  }
});

test('GR#16: a hand-corrupted congestionTicksBySpec cannot poison congestionFactorOf/wellbeingOf/population/funds after a month (mutant: bare ?? guard, NaN leaks through)', () => {
  const corruptValues = ['abc', [], { road: -5 }, { road: 1e9 }, { road: NaN }, { road: 'x' }];
  for (const bad of corruptValues) {
    const s = saturatedRoadCity({ congestionTicksBySpec: bad });
    const factor = congestionFactorOf(s);
    assert.ok(Number.isFinite(factor), `congestionFactorOf must stay finite for corrupt state, got ${factor}`);
    assert.ok(factor >= 0 && factor <= 1, `congestionFactorOf must stay in [0,1] for corrupt state, got ${factor}`);

    const wb = wellbeingOf(s);
    assert.ok(Number.isFinite(wb.overall), `wellbeingOf().overall must stay finite for corrupt state, got ${wb.overall}`);

    let after = s;
    for (let i = 0; i < TICKS_PER_MONTH; i++) after = reducer(after, { type: 'tick' });
    assert.ok(Number.isFinite(after.population), `population must stay finite after a month with corrupt state, got ${after.population}`);
    assert.ok(Number.isFinite(after.funds), `funds must stay finite after a month with corrupt state, got ${after.funds}`);
    for (const v of Object.values(after.congestionTicksBySpec ?? {})) {
      assert.ok(Number.isFinite(v), `congestionTicksBySpec must self-heal to finite entries, got ${v}`);
    }
  }
});

// ---------------------------------------------------------------------------
// advanceCongestionTicks — pure unit coverage of the counter idiom itself
// ---------------------------------------------------------------------------
test('advanceCongestionTicks increments while saturated, hard-resets below threshold, caps at CONGESTION_SUSTAINED_TICKS, ignores rail (mutant: no cap / no reset / rail counted)', () => {
  const roadSaturated = { spec: 'road', kind: 'road', name: 'Road', tiles: 1, capacity: 100, usage: 200, saturation: 1, headroom: -100, overCapacity: true };
  const roadUnsaturated = { ...roadSaturated, saturation: 0.5, usage: 50, overCapacity: false };
  const railSaturated = { spec: 'rail', kind: 'rail', name: 'Rail', tiles: 1, capacity: 100, usage: 200, saturation: 1, headroom: -100, overCapacity: true };

  // Increments from 0.
  let ticks = advanceCongestionTicks({}, [roadSaturated]);
  assert.strictEqual(ticks.road, 1);
  // Keeps incrementing.
  ticks = advanceCongestionTicks(ticks, [roadSaturated]);
  assert.strictEqual(ticks.road, 2);
  // Caps at CONGESTION_SUSTAINED_TICKS.
  ticks = advanceCongestionTicks({ road: CONGESTION_SUSTAINED_TICKS }, [roadSaturated]);
  assert.strictEqual(ticks.road, CONGESTION_SUSTAINED_TICKS);
  // Hard reset the instant saturation drops below threshold.
  ticks = advanceCongestionTicks({ road: 45 }, [roadUnsaturated]);
  assert.strictEqual(ticks.road, undefined, 'a reset-to-zero entry must be omitted (self-pruning)');
  // Rail is never tracked, regardless of its own saturation.
  ticks = advanceCongestionTicks({}, [railSaturated]);
  assert.strictEqual(ticks.rail, undefined, 'rail lines must never accrue congestion ticks');
});
