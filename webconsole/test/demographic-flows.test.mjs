// demographic-flows.test.mjs — FEAT-1972079925: population demographic FLOWS
// (births/deaths/move-ins/move-outs) replacing the bare converge-to-capacity
// rule (BUG-394), plus the monthly aggregation history that backs the
// population Sankey.
//
// Determinism (GR#21): every assertion here runs the SAME reducer path twice
// from identical inputs and checks byte-identical output — no Date/Math.random
// anywhere in the model under test.
//
// RED proof (scratch cp/mv, GR#23 — see the session report for the executed
// mutation-kill matrix): zeroing BIRTH_RATE_PER_TICK kills the births-nonzero
// assertion; removing the `deaths -` term from the population update breaks
// the conservation-identity test; removing the effectiveHeadroom cap on
// moveIns breaks the headroom-bound test.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { reducer, TICKS_PER_MONTH } from '../src/sim/engine.ts';
import { residentsCapacity } from '../src/sim/data.ts';
import { demographicSankeyModel } from '../src/components/populationSankeyModel.ts';

// A minimal, fully-controlled base state — deliberately WITHOUT roadConnectivity
// so isOnline()'s road-adjacency gates are skipped (backward tolerance path),
// letting every residential building here count toward capacity regardless of
// map position. Mirrors the pattern already used by bug394-freeze.test.mjs.
function baseState(overrides = {}) {
  return {
    tick: 0,
    speed: 1,
    funds: 10_000_000,
    loanBalance: 0,
    population: 0,
    xp: 30,
    taxRates: { residential: 9, commercial: 11, industrial: 13 },
    policies: { recycling: false, transitSubsidy: false, tourismDrive: false, austerity: false },
    buildings: [],
    nextId: 1,
    movingId: null,
    tool: { mode: 'select' },
    clipboard: null,
    pipeTier: {},
    history: [],
    ledger: [],
    nextLedgerId: 1,
    lastFlows: { inflows: [], outflows: [] },
    fundsAtTickStart: 10_000_000,
    fundsAtTickEnd: 10_000_000,
    pendingRewards: [],
    lastRewardedLevel: 1,
    notice: null,
    unlockedAll: false,
    roadNotice: null,
    railNotice: null,
    placeNotice: null,
    roadMonitors: [],
    buildingMonitors: [],
    ...overrides,
  };
}

function residentialBuildings(n, spec = 'res_hut') {
  const out = [];
  for (let i = 0; i < n; i++) out.push({ id: i + 1, spec, x: 10 + i, y: 10 });
  return out;
}

test('flows conserve: pop_after == pop_before + births + moveIns - deaths - moveOuts (capacity-clamped)', () => {
  // A large-enough population (res_estate x 40 = 60,000 capacity) that EVERY
  // flow term (including the small death/birth fractions) rounds to a
  // nonzero integer most ticks — a small population would let a dropped
  // term hide behind rounding-to-zero and defeat this exact-equality check.
  let state = baseState({ population: 40_000, buildings: residentialBuildings(40, 'res_estate') });
  // res_hut/res_estate has no builtTick and no roadConnectivity gate in this
  // bespoke state, so isOnline() is trivially true for every building —
  // capacity is exactly residentsCapacity(state) throughout (buildings never change).
  const capacity = residentsCapacity(state);
  for (let t = 0; t < 20; t++) {
    const before = state.population;
    state = reducer(state, { type: 'tick' });
    const d = state.lastDemographics;
    assert.ok(d, 'lastDemographics recorded');
    const raw = before + d.births + d.moveIns - d.deaths - d.moveOuts;
    const expected = Math.max(0, Math.min(capacity, raw));
    assert.equal(
      state.population,
      expected,
      `conservation identity must hold exactly (before=${before} d=${JSON.stringify(d)} raw=${raw})`
    );
  }
});

test('at-capacity churn: population holds near the ceiling while flows stay nonzero', () => {
  // A large city (100 res_estate x 1,500 residents = 150,000 capacity) so the
  // per-tick placeholder rates (fractions of a percent) round to a nonzero
  // integer on every flow — a small city can legitimately show births/deaths
  // rounding to exactly 0 some ticks (that is fine; the point proven here is
  // that a city AT the ceiling shows real churn on every axis, not a small-
  // number rounding artefact of the test scenario itself).
  const buildings = residentialBuildings(100, 'res_estate');
  const capacity = residentsCapacity(baseState({ buildings }));
  let state = baseState({ population: capacity, buildings });
  const sawNonzero = { births: false, deaths: false, moveIns: false, moveOuts: false };
  for (let t = 0; t < 50; t++) {
    state = reducer(state, { type: 'tick' });
    assert.ok(state.population <= capacity, 'population never runs away above the ceiling');
    const d = state.lastDemographics;
    if (d.births > 0) sawNonzero.births = true;
    if (d.deaths > 0) sawNonzero.deaths = true;
    if (d.moveIns > 0) sawNonzero.moveIns = true;
    if (d.moveOuts > 0) sawNonzero.moveOuts = true;
  }
  // ALL FOUR flow kinds must independently show up nonzero at least once — a
  // city at capacity must show real churn on every axis, not just one. This
  // is the assertion a zeroed placeholder rate (any of the four) kills.
  assert.ok(sawNonzero.births, 'births must be nonzero at least once at capacity');
  assert.ok(sawNonzero.deaths, 'deaths must be nonzero at least once at capacity');
  assert.ok(sawNonzero.moveIns, 'move-ins must be nonzero at least once at capacity (backfilling departures)');
  assert.ok(sawNonzero.moveOuts, 'move-outs must be nonzero at least once at capacity');
});

test('determinism: two identical runs produce byte-identical demographic flows', () => {
  const mkState = () => baseState({ population: 1200, buildings: residentialBuildings(200) });
  let a = mkState();
  let b = mkState();
  for (let t = 0; t < 40; t++) {
    a = reducer(a, { type: 'tick' });
    b = reducer(b, { type: 'tick' });
  }
  assert.deepEqual(a.lastDemographics, b.lastDemographics, 'last-tick flows identical');
  assert.deepEqual(a.demographicHistory, b.demographicHistory, 'monthly history identical');
  assert.equal(a.population, b.population, 'population identical');
});

test('headroom bound: move-ins never exceed headroom + this-tick departures (effective headroom)', () => {
  // Maximise the attractiveness multiplier (zero tax, transit subsidy ON,
  // heavy job surplus vs. a small population so demand.residential pegs near
  // its +100 ceiling) so MOVE_IN_RATE * attractiveness pushes comfortably
  // past 1 — this is the regime that actually exercises the effectiveHeadroom
  // CAP (below it, a low attractiveness keeps moveIns under headroom anyway
  // even without an explicit min(), so the cap's own removal would go
  // undetected — see the session's mutation-proof notes).
  const jobBuildings = [];
  for (let i = 0; i < 60; i++) jobBuildings.push({ id: 900 + i, spec: 'off_tower', x: 200 + i, y: 5 });
  let state = baseState({
    population: 300,
    buildings: [...residentialBuildings(40), ...jobBuildings],
    taxRates: { residential: 0, commercial: 0, industrial: 0 },
    policies: { recycling: false, transitSubsidy: true, tourismDrive: false, austerity: false },
  });
  const capacity = residentsCapacity(state); // buildings never change in this test
  for (let t = 0; t < 30; t++) {
    const beforePop = state.population;
    state = reducer(state, { type: 'tick' });
    const d = state.lastDemographics;
    const headroom = Math.max(0, capacity - beforePop);
    const effectiveHeadroom = headroom + d.deaths + d.moveOuts;
    assert.ok(
      d.moveIns <= effectiveHeadroom,
      `moveIns (${d.moveIns}) must never exceed headroom+departures (${effectiveHeadroom}) at pop=${beforePop}/cap=${capacity}`
    );
  }
});

test('history aggregation: one closed month sums exactly the per-tick flows recorded over it', () => {
  let state = baseState({ population: 500, buildings: residentialBuildings(60) });
  let sumBirths = 0;
  let sumDeaths = 0;
  let sumMoveIns = 0;
  let sumMoveOuts = 0;
  for (let t = 0; t < TICKS_PER_MONTH; t++) {
    state = reducer(state, { type: 'tick' });
    sumBirths += state.lastDemographics.births;
    sumDeaths += state.lastDemographics.deaths;
    sumMoveIns += state.lastDemographics.moveIns;
    sumMoveOuts += state.lastDemographics.moveOuts;
  }
  assert.equal(state.tick, TICKS_PER_MONTH, 'reached exactly the month boundary');
  assert.equal(state.demographicHistory.length, 1, 'exactly one month closed into history');
  const entry = state.demographicHistory[0];
  assert.equal(entry.tick, TICKS_PER_MONTH);
  assert.equal(entry.births, sumBirths, 'births aggregate matches the per-tick sum');
  assert.equal(entry.deaths, sumDeaths, 'deaths aggregate matches the per-tick sum');
  assert.equal(entry.moveIns, sumMoveIns, 'moveIns aggregate matches the per-tick sum');
  assert.equal(entry.moveOuts, sumMoveOuts, 'moveOuts aggregate matches the per-tick sum');
  assert.equal(entry.population, state.population, 'aggregate records the population AT month-close');

  // The running accumulator resets to zero right after the flush.
  assert.deepEqual(state.demographicAccum, { births: 0, deaths: 0, moveIns: 0, moveOuts: 0 });

  // One more tick starts accumulating the NEXT month from zero.
  state = reducer(state, { type: 'tick' });
  assert.equal(state.demographicHistory.length, 1, 'no new month closed yet');
  assert.deepEqual(state.demographicAccum, state.lastDemographics, 'accumulator now holds exactly this one tick');
});

test('history ring is bounded at DEMOGRAPHIC_HISTORY_CAP months', () => {
  let state = baseState({ population: 500, buildings: residentialBuildings(60) });
  const months = 130; // > the 120-month cap
  for (let t = 0; t < TICKS_PER_MONTH * months; t++) {
    state = reducer(state, { type: 'tick' });
  }
  assert.ok(state.demographicHistory.length <= 120, `history ring must stay bounded (got ${state.demographicHistory.length})`);
  assert.equal(state.demographicHistory.length, 120, 'ring is full and capped at exactly 120 after 130 months');
});

// === Sankey data-shaping (pure function, independent of SVG rendering) ===

test('Sankey model: empty history renders an honest empty state, never a fabricated split', () => {
  const model = demographicSankeyModel([], 'month');
  assert.equal(model.empty, true);
  assert.equal(model.totalIn, 0);
  assert.equal(model.totalOut, 0);

  const modelUndefined = demographicSankeyModel(undefined, 'year');
  assert.equal(modelUndefined.empty, true);
});

test('Sankey model: "month" sums only the last recorded month', () => {
  const history = [
    { tick: 30, population: 100, births: 5, deaths: 1, moveIns: 10, moveOuts: 2 },
    { tick: 60, population: 112, births: 6, deaths: 1, moveIns: 8, moveOuts: 3 },
  ];
  const model = demographicSankeyModel(history, 'month');
  assert.equal(model.empty, false);
  assert.equal(model.monthsCovered, 1);
  assert.equal(model.births, 6);
  assert.equal(model.deaths, 1);
  assert.equal(model.moveIns, 8);
  assert.equal(model.moveOuts, 3);
  assert.equal(model.totalIn, 14);
  assert.equal(model.totalOut, 4);
});

test('Sankey model: "year" sums the trailing 12 recorded months (or fewer if history is shorter)', () => {
  const history = Array.from({ length: 15 }, (_, i) => ({
    tick: (i + 1) * 30,
    population: 100 + i,
    births: 1,
    deaths: 1,
    moveIns: 2,
    moveOuts: 1,
  }));
  const model = demographicSankeyModel(history, 'year');
  assert.equal(model.monthsCovered, 12, 'only the trailing 12 months are summed, not all 15');
  assert.equal(model.births, 12);
  assert.equal(model.moveIns, 24);

  const shortHistory = history.slice(0, 3);
  const shortModel = demographicSankeyModel(shortHistory, 'year');
  assert.equal(shortModel.monthsCovered, 3, 'fewer than 12 recorded months sums what exists, not a fabricated 12');
});

test('directional: births/deaths/moveIns/moveOuts are all non-negative integers, every tick', () => {
  let state = baseState({ population: 900, buildings: residentialBuildings(70) });
  for (let t = 0; t < 60; t++) {
    state = reducer(state, { type: 'tick' });
    const d = state.lastDemographics;
    for (const [k, v] of Object.entries(d)) {
      assert.ok(Number.isInteger(v) && v >= 0, `${k} must be a non-negative integer, got ${v}`);
    }
  }
});
