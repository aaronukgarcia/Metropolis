// attack-bug646-cap-exact.test.mjs — BUG-646 (Aaron, 2026-09-03: "the autofix
// looks to have a 250 limit, not enough, make it 2000"). Exercises the EXACT
// boundary of RESOLVE_DEMAND_ALL_MAX_UNITS (2000): a plan of exactly the cap
// must complete cleanly with no "click again" leftover notice, a plan of
// cap+1 must place exactly the cap and DOES report the leftover notice, and a
// funds-limited batch must stop on money — never mis-blame the unit cap. Also
// carries the perf regression gate for the cap raise (data.ts's
// RESOLVE_DEMAND_ALL_MAX_UNITS doc comment has the full root-cause writeup
// and measurement history this bound is calibrated against).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import { demandFixPlan, RESOLVE_DEMAND_ALL_MAX_UNITS } from '../src/sim/data.ts';

function bigState(population, funds = 1e13) {
  return { ...initialState(), population, unlockedAll: true, funds, administrationState: null };
}

function parksCountAt(population) {
  const s = bigState(population);
  const plan = demandFixPlan(s).find((p) => p.serviceKey === 'parks');
  return plan ? plan.count : 0;
}

/** BUG-685: count placements of EVERY spec in a (possibly mixed) plan, not
 *  just the primary/largest one — a largestFirstFill() mix places several
 *  specs, so a single-specId filter undercounts. */
function countMixPlaced(before, after, mix) {
  const ids = new Set(mix.map((m) => m.specId));
  const countOf = (bs) => bs.filter((b) => ids.has(b.spec)).length;
  return countOf(after) - countOf(before);
}

/**
 * GR#15 (validators derive from data, never a hardcoded constant): rather
 * than hand-pick a population that HAPPENED to produce parks.count===target
 * when this test was written (liable to silently go stale the moment
 * AUTO_BUILD_DEMAND_FRACTION, a park spec's capacity, or the population
 * curve changes), binary-search demandFixPlan() itself, at TEST-RUN time,
 * for the smallest population whose parks plan needs >= target units — this
 * stays correct under any future re-tuning of those inputs.
 */
function findPopulationForParksCount(target) {
  let lo = 0;
  let hi = 20_000_000;
  while (lo < hi) {
    const mid = Math.floor((lo + hi) / 2);
    if (parksCountAt(mid) >= target) hi = mid;
    else lo = mid + 1;
  }
  return lo;
}

test('BUG-646 EXACT CAP: a plan of EXACTLY RESOLVE_DEMAND_ALL_MAX_UNITS units completes with NO leftover/cap-limit notice', () => {
  const pop = findPopulationForParksCount(RESOLVE_DEMAND_ALL_MAX_UNITS);
  const s = bigState(pop);
  const plan = demandFixPlan(s).find((p) => p.serviceKey === 'parks');
  assert.ok(plan, 'precondition: a parks shortfall must exist');
  assert.equal(plan.count, RESOLVE_DEMAND_ALL_MAX_UNITS, `precondition: parks plan must be EXACTLY the cap (got ${plan.count})`);

  const result = reducer(s, { type: 'resolveDemand', serviceKey: 'parks' });
  const placed = countMixPlaced(s.buildings, result.buildings, plan.mix);

  assert.equal(placed, RESOLVE_DEMAND_ALL_MAX_UNITS, 'must place exactly the cap when the plan needs exactly the cap');
  assert.equal(result.placeNotice, null, `a fully-cleared shortfall must NOT report a leftover/cap-limit notice, got: ${result.placeNotice}`);
  assert.ok(result.funds >= 0, 'funds must never go negative');
});

test('BUG-646 OVER CAP: a plan of RESOLVE_DEMAND_ALL_MAX_UNITS + 1 places exactly the cap and DOES report the "click Fix again" notice', () => {
  const pop = findPopulationForParksCount(RESOLVE_DEMAND_ALL_MAX_UNITS + 1);
  const s = bigState(pop);
  const plan = demandFixPlan(s).find((p) => p.serviceKey === 'parks');
  assert.ok(plan, 'precondition: a parks shortfall must exist');
  assert.ok(plan.count > RESOLVE_DEMAND_ALL_MAX_UNITS, `precondition: parks plan (${plan.count}) must exceed the cap`);

  const result = reducer(s, { type: 'resolveDemand', serviceKey: 'parks' });
  const placed = countMixPlaced(s.buildings, result.buildings, plan.mix);

  assert.equal(placed, RESOLVE_DEMAND_ALL_MAX_UNITS, 'a plan of cap+1 must place EXACTLY the cap, never more');
  assert.match(
    result.placeNotice ?? '',
    new RegExp(`reached the ${RESOLVE_DEMAND_ALL_MAX_UNITS}-unit per-click build limit — click Fix again for the rest`),
    `expected the cap-limit notice, got: ${result.placeNotice}`
  );
  assert.ok(result.funds >= 0, 'funds must never go negative');
});

test('BUG-646 FUNDS-LIMITED: a plan that exceeds the cap but has LESS money than that many units cost stops on FUNDS, not the unit cap', () => {
  // hea_clinic (a paid service, unlike free parks) at a population whose
  // shortfall genuinely exceeds the cap, but funded for only a handful of
  // units — the funds guard (placePlanItem's cost>0 && funds<cost break)
  // must fire long before RESOLVE_DEMAND_ALL_MAX_UNITS ever could.
  const pop = 8_000_000; // gp plan comfortably exceeds 2000 at this scale (see attack-bug606-cap.test.tsx's POP_FOR_PARKS_OVER_CAP sibling scale)
  const probe = bigState(pop, 1e13);
  const plan = demandFixPlan(probe).find((p) => p.serviceKey === 'gp');
  assert.ok(plan, 'precondition: a gp (hea_clinic) shortfall must exist');
  assert.ok(plan.count > RESOLVE_DEMAND_ALL_MAX_UNITS, `precondition: gp plan (${plan.count}) must exceed the cap`);
  const unitCost = plan.planCost / plan.count;
  const affordableUnits = 5;
  const funds = Math.floor(unitCost * (affordableUnits + 0.5)); // enough for 5, not 6

  const s = bigState(pop, funds);
  const result = reducer(s, { type: 'resolveDemand', serviceKey: 'gp' });
  const placed = countMixPlaced(s.buildings, result.buildings, plan.mix);

  assert.ok(placed > 0, `precondition: at least one unit must be affordable (placed ${placed})`);
  assert.ok(placed < RESOLVE_DEMAND_ALL_MAX_UNITS, `placed (${placed}) must stop well short of the cap — this is a FUNDS-limited scenario`);
  assert.match(result.placeNotice ?? '', /insufficient funds/i, `must report insufficient funds, not the cap, got: ${result.placeNotice}`);
  assert.doesNotMatch(result.placeNotice ?? '', /per-click build limit/i, `must NOT blame the unit cap when funds ran out first: ${result.placeNotice}`);
  assert.ok(result.funds >= 0, 'funds must never go negative');
});

// ---------------------------------------------------------------------------
// PERF REGRESSION GATE (task step 4: "a perf regression test with a bound
// derived from ~4x YOUR CI-measured median (never max, never wall-clock
// inside the sim)"). The sim itself has no clock in its hot path (GR#21) —
// this measures the TEST's OWN wall-clock cost of dispatching a real
// resolveDemand, exactly as scale-gate.test.mjs's existing pattern does for
// tick cost, never a bound INSIDE the reducer.
//
// CALIBRATION (this session, Windows, Node 25.3.0, dev machine — same
// disclaimer as scale-gate.test.mjs: CI hardware/Node version differ, so
// this is a generous multiple of a local measurement, never tightened
// without a fresh CI-side one): 3 independent resolveDemand('parks')
// dispatches at pop 8,000,000 (parks plan ~2,667 units, capped at 2000)
// measured 26.6s, 27.2s, 29.8s — median ~27.2s. PERF_BOUND_MS is 4x that,
// rounded up.
// ---------------------------------------------------------------------------
const PERF_SAMPLE_COUNT = 3;
const PERF_BOUND_MS = 4 * 27_200; // ~109s

function median(values) {
  const sorted = [...values].sort((a, b) => a - b);
  const n = sorted.length;
  return n % 2 ? sorted[(n - 1) / 2] : (sorted[n / 2 - 1] + sorted[n / 2]) / 2;
}

test('BUG-646 PERF: median resolveDemand time at a cap-exceeding parks batch stays under 4x the calibrated median', { timeout: 200_000 }, () => {
  const pop = 8_000_000;
  const s = bigState(pop);
  const plan = demandFixPlan(s).find((p) => p.serviceKey === 'parks');
  assert.ok(plan && plan.count > RESOLVE_DEMAND_ALL_MAX_UNITS, `precondition: parks plan (${plan?.count}) must exceed the cap for this to exercise the capped path`);

  const times = [];
  for (let i = 0; i < PERF_SAMPLE_COUNT; i++) {
    const t0 = performance.now();
    const result = reducer(s, { type: 'resolveDemand', serviceKey: 'parks' });
    times.push(performance.now() - t0);
    assert.ok(result.funds >= 0);
  }
  const med = median(times);
  console.log(`  resolveDemand(parks) at pop ${pop}: ${times.map((t) => t.toFixed(0)).join(', ')}ms, median ${med.toFixed(0)}ms`);
  assert.ok(med < PERF_BOUND_MS, `median resolveDemand time ${med.toFixed(0)}ms exceeds the ${PERF_BOUND_MS}ms perf regression bound`);
});
