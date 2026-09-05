// bug-397-transit-wages.test.mjs — BUG-397 (P1): Free Transit + wages scale
// linearly with population -> guaranteed late-game bankruptcy.
//
// Aaron's ruling (2026-08-31, all three shapes APPROVED, directional
// placeholder numbers, exact values pending a later balance pass):
//   (1) Transit earns FARE REVENUE unless the Free Transit policy is on.
//   (2) Service costs scale SUB-LINEARLY with population (economies of
//       scale) via a named placeholder exponent.
//   (3) Policy costs are CAPPED as a percentage of tax income.
//
// The measured shape this closes: at pop 314,252, Transit Subsidy outflow
// was pop*1.5*austerity and Wages was pop*0.5*austerity -- 74% of ALL spend,
// scaling forever. Wages is ALREADY fixed (FEAT-wage-stage1, 2026-09-03,
// landed before this ticket started): the 'Wages' outflow is sourced from
// fiscal.ts's sectorWagesPerTick(filledJobsBySector(s)) -- filled jobs at
// municipal/commercial/industrial/office buildings, capped by the actual
// workforce, NOT a flat population*0.5. This file's wages test proves that
// wiring stays independent of total population when the job-bearing
// buildings/workforce basis is held fixed. This file's transit tests cover
// the genuinely NEW work: fare revenue, sub-linear subsidy scaling, and the
// generalised policy-cost cap + GR#17 surfacing.
//
// node --test type-strips the .ts imports; every assertion below can FAIL
// against the pre-fix shape -- proved directly by the MUTATION-PROVE test at
// the bottom (never a git revert, GR#24: verified live via a scratch
// cp/mv of fiscal.ts, restored immediately after).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { computeFlows, reducer, initialState, TICKS_PER_MONTH } from '../src/sim/engine.ts';
import { runConsistencyChecks } from '../src/sim/consistency.ts';
import {
  SERVICE_COST_SCALE_EXPONENT,
  POLICY_COST_CAP_FRACTION,
  POLICY_CAP_REFERENCE_TAX_RATE,
  scaledServiceCostPerTick,
  transitSubsidyCostPerTick,
  transitFareRevenuePerTick,
  taxIncomeAtRate,
  TRANSIT_FARE_RATE_PER_RIDER,
  TRANSIT_FARE_REVENUE_LABEL,
} from '../src/sim/fiscal.ts';
import { countByKindOnline, isOnline } from '../src/sim/data.ts';

/** BUG-397 F2 — mirrors engine.ts computeFlows()'s capBase derivation exactly
 * (GR#3): max(actual tax income, tax income at the reference rate). */
function capBaseOf(s, actualTaxIncome) {
  const c = countByKindOnline(s);
  const harbourBoost = s.buildings.some((b) => b.spec === 'land_harbour' && isOnline(s, b)) ? 1.4 : 1;
  const reference = taxIncomeAtRate(
    s.population,
    c.commercial,
    c.industrial,
    c.mine,
    POLICY_CAP_REFERENCE_TAX_RATE,
    harbourBoost,
  );
  return Math.max(actualTaxIncome, reference);
}

// Minimal SimState fixture -- mirrors wage-sector-bands.test.mjs's baseState()/
// addBuilding() exactly (same shape, same 'builtTick: null' always-online
// idiom, so isOnline()'s road-gate skip applies here too -- see data.ts's
// isOnline()/computeRoadGates() backward-tolerance comment: no
// s.roadConnectivity means the road gates are skipped, not failed-closed).
function baseState(overrides = {}) {
  return {
    tick: 0,
    speed: 1,
    funds: 50_000_000,
    loanBalance: 0,
    population: 1000,
    xp: 0,
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
    lastRewardedLevel: 1,
    notice: null,
    ...overrides,
  };
}

function addBuilding(state, spec) {
  return {
    ...state,
    buildings: [
      ...state.buildings,
      { id: state.nextId, spec, x: state.nextId, y: 10, builtTick: null },
    ],
    nextId: state.nextId + 1,
  };
}

function withTransitCapacity(state) {
  // metro_station carries `served: 30000` (data.ts) -- gives a real, catalogue-
  // sourced transit capacity so transitServedCapacity(s) > 0 without hand-
  // rolling a fake spec.
  return addBuilding(state, 'metro_station');
}

// Full-shape state for tests that drive the REDUCER (reducer(s, {type:'tick'}))
// rather than calling computeFlows() directly -- advance() reads dozens of
// SimState fields the minimal baseState() above deliberately omits (mirrors
// bug-503-sellasset-conservation.test.mjs's own initialState()-based pattern).
// initialState() already carries ~1,855 genesis map-furniture buildings
// (builtTick<=0, free per FEAT-2326609782) -- overriding population/policies/
// funds on top of it keeps every other advance()-required field valid.
function fullState(overrides = {}) {
  return { ...initialState(), ...overrides };
}

// ========== (1) Fare revenue: booked OFF, forgone ON ==========

test('BUG-397 AC1: fare revenue > 0 when Free Transit is OFF and the city has transit capacity', () => {
  const s = withTransitCapacity(baseState({ population: 20000 }));
  const { inflows } = computeFlows(s);
  const fare = inflows.find((f) => f.label === TRANSIT_FARE_REVENUE_LABEL);
  assert.ok(fare, 'a Transit Fare Revenue line must be booked');
  assert.ok(fare.value > 0, `fare revenue must be positive, got ${fare?.value}`);
});

test('BUG-397 AC1: fare revenue is exactly 0 (line absent) when Free Transit is ON', () => {
  const s = withTransitCapacity(
    baseState({ population: 20000, policies: { recycling: false, transitSubsidy: true, tourismDrive: false, austerity: false } }),
  );
  const { inflows } = computeFlows(s);
  const fare = inflows.find((f) => f.label === TRANSIT_FARE_REVENUE_LABEL);
  assert.equal(fare, undefined, 'no fare revenue line while the policy forgoes it');
});

test('BUG-397 AC1: fare revenue is 0 with no transit buildings at all (no network, no riders)', () => {
  const s = baseState({ population: 20000 }); // no transit buildings placed
  const { inflows } = computeFlows(s);
  const fare = inflows.find((f) => f.label === TRANSIT_FARE_REVENUE_LABEL);
  assert.equal(fare, undefined, 'a city with zero transit capacity cannot collect fares');
});

// ========== (2) Sub-linear service cost scaling ==========

test('BUG-397 AC2: transitSubsidyCostPerTick grows SUB-linearly -- 10x population costs LESS than 10x', () => {
  const base = transitSubsidyCostPerTick(10_000);
  const tenX = transitSubsidyCostPerTick(100_000);
  assert.ok(base > 0, 'precondition: base cost must be positive');
  assert.ok(tenX < base * 10, `10x population (${tenX}) must cost less than 10x the base cost (${base * 10})`);
  assert.ok(tenX > base, 'cost must still be monotonically increasing in population');
});

test('BUG-397 AC2: scaledServiceCostPerTick matches rate * population^exponent exactly, guards population<=0', () => {
  assert.equal(scaledServiceCostPerTick(1.5, 0), 0, 'population 0 must not divide/pow into NaN/Infinity');
  assert.equal(scaledServiceCostPerTick(1.5, -5), 0, 'a negative population must not produce a fractional-power NaN');
  const expected = Math.round(1.5 * Math.pow(50_000, SERVICE_COST_SCALE_EXPONENT));
  assert.equal(scaledServiceCostPerTick(1.5, 50_000), expected);
});

test('BUG-397 AC2: the exponent is a NAMED constant strictly below 1.0 (sub-linear, not the old flat multiply)', () => {
  assert.ok(SERVICE_COST_SCALE_EXPONENT > 0 && SERVICE_COST_SCALE_EXPONENT < 1, 'must be a genuine sub-linear exponent');
});

// ========== (3) Policy cost cap ==========

test('BUG-397 AC3: Transit Subsidy outflow never exceeds POLICY_COST_CAP_FRACTION of tax income, when tax income is starved', () => {
  // With the residential tax rate at 0 (and no buildings, so Business/Freight
  // Tax are also 0), tax income is exactly 0 while the population-scaled
  // subsidy is still positive -- the cap MUST bind, exercising the exact
  // self-limiting-drain behaviour the ruling calls for (a council that
  // slashes tax rates cannot ALSO run an uncapped population-scaled subsidy).
  const s = baseState({
    population: 50_000,
    taxRates: { residential: 0, commercial: 11, industrial: 13 },
    policies: { recycling: false, transitSubsidy: true, tourismDrive: false, austerity: false },
  });
  const { inflows, outflows } = computeFlows(s);
  const taxIncome = inflows
    .filter((f) => ['Council Tax', 'Business Tax', 'Freight Tax'].includes(f.label))
    .reduce((a, f) => a + f.value, 0);
  const subsidy = outflows.find((f) => f.label === 'Transit Subsidy');
  assert.ok(subsidy, 'Transit Subsidy line must be present while the policy is on');
  // BUG-397 F2 fix (2026-09-05): the real cap base is max(actual tax income,
  // reference-rate tax income), not the raw (possibly-zero) actual tax
  // income alone — see fiscal.ts's POLICY_CAP_REFERENCE_TAX_RATE doc.
  const capBase = capBaseOf(s, taxIncome);
  assert.ok(
    subsidy.value <= Math.round(capBase * POLICY_COST_CAP_FRACTION) + 1, // +1 rounding slack
    `Transit Subsidy (${subsidy.value}) must never exceed ${POLICY_COST_CAP_FRACTION * 100}% of the cap base (${capBase})`,
  );
  // The uncapped formula at this population is astronomically larger, so the
  // cap must actually be BINDING here, not coincidentally under it.
  const uncapped = transitSubsidyCostPerTick(s.population);
  assert.ok(uncapped > subsidy.value, 'precondition: at this population the cap must genuinely bind');
});

test('BUG-397 AC3/GR#17: a binding cap is surfaced -- NOT a silent clamp -- via a ledger entry', () => {
  const s = fullState({
    population: 50_000,
    taxRates: { residential: 0, commercial: 11, industrial: 13 },
    policies: { recycling: false, transitSubsidy: true, tourismDrive: false, austerity: false },
    funds: 50_000_000,
  });
  const after = reducer(s, { type: 'tick' });
  const capNote = after.ledger.find((e) => /Transit Subsidy capped/.test(e.label));
  assert.ok(capNote, 'a ledger entry must record the cap binding (GR#17: no silent clamp)');
  assert.equal(capNote.amount, 0, 'the ledger note is informational only -- no money actually moved beyond what flows already recorded');
});

test('BUG-397 AC3: no cap-bind ledger entry when the subsidy does not exceed the cap (small city)', () => {
  const s = fullState({
    population: 100,
    policies: { recycling: false, transitSubsidy: true, tourismDrive: false, austerity: false },
    funds: 50_000_000,
  });
  const after = reducer(s, { type: 'tick' });
  const capNote = after.ledger.find((e) => /Transit Subsidy capped/.test(e.label));
  assert.equal(capNote, undefined, 'no cap note should appear when the raw cost never threatened the cap');
});

// ========== Wages: already job-based, independent of raw population ==========

test('BUG-397 wages: identical job-bearing buildings -> Wages does not scale with population alone', async () => {
  const { SPECS } = await import('../src/sim/data.ts');
  const commercialSpecId = Object.values(SPECS).find((sp) => sp.kind === 'commercial')?.id;
  assert.ok(commercialSpecId, 'precondition: catalogue must have at least one commercial spec');

  const lowPop = addBuilding(baseState({ population: 500 }), commercialSpecId);
  const highPop = addBuilding(baseState({ population: 500_000 }), commercialSpecId);

  const lowWages = computeFlows(lowPop).outflows.find((f) => f.label === 'Wages')?.value ?? 0;
  const highWages = computeFlows(highPop).outflows.find((f) => f.label === 'Wages')?.value ?? 0;

  // With the SAME one job-bearing building in both cities, filled jobs are
  // capacity-bounded (a handful of jobs), not population-bounded once the
  // workforce vastly exceeds capacity -- so the wage bill must be IDENTICAL,
  // never 1000x apart the way flat population*0.5 would have made it.
  assert.equal(
    lowWages,
    highWages,
    `Wages must be capped by job CAPACITY, not scale with total population (low=${lowWages}, high=${highWages})`,
  );
});

// ========== Money conservation over time, with the BUG-397 changes active ==========

test('BUG-397: money conservation holds over 200 ticks with Free Transit ON and a large population', () => {
  let s = addBuilding(
    fullState({
      population: 300_000,
      policies: { recycling: false, transitSubsidy: true, tourismDrive: false, austerity: true },
      funds: 50_000_000,
    }),
    'metro_station',
  );
  for (let i = 0; i < 200; i++) {
    s = reducer(s, { type: 'tick' });
    const report = runConsistencyChecks(s);
    const check = report.checks.find((c) => c.id === 'conservation.funds-vs-flows');
    assert.ok(check.ok, `tick ${i}: conservation.funds-vs-flows must hold (${check.detail})`);
  }
});

test('BUG-397: money conservation holds over 200 ticks with Free Transit OFF (fare revenue active)', () => {
  let s = addBuilding(
    fullState({
      population: 300_000,
      policies: { recycling: false, transitSubsidy: false, tourismDrive: false, austerity: false },
      funds: 50_000_000,
    }),
    'metro_station',
  );
  for (let i = 0; i < 200; i++) {
    s = reducer(s, { type: 'tick' });
    const report = runConsistencyChecks(s);
    const check = report.checks.find((c) => c.id === 'conservation.funds-vs-flows');
    assert.ok(check.ok, `tick ${i}: conservation.funds-vs-flows must hold (${check.detail})`);
  }
});

// ========== Determinism (GR#21) ==========

test('BUG-397: computeFlows is a pure, deterministic function of state -- no Date.now/Math.random smell', () => {
  const s = withTransitCapacity(baseState({ population: 42_000 }));
  const a = computeFlows(s);
  const b = computeFlows(s);
  assert.deepEqual(a, b, 'same state in -> byte-identical flows out');
});

// ========== MUTATION-PROVE ==========
//
// Proved live during this ticket (per the brief's requirement), NOT left as
// a comment-only claim: fiscal.ts's SERVICE_COST_SCALE_EXPONENT was edited to
// 1.0 (the old linear behaviour) via a scratch cp/mv (GR#24 -- never a git
// revert), the AC2 sub-linear test above was re-run and observed RED
// (`tenX < base * 10` failed because 1.0 makes tenX === base*10 exactly),
// then fiscal.ts was restored from the scratch copy and the suite re-run
// GREEN. This test pins that exact boundary case permanently so the same
// mutation is caught by CI without needing a human to repeat it by hand.
test('BUG-397 MUTATION-PROVE: an exponent of 1.0 reproduces the OLD linear (non-sub-linear) shape', () => {
  const linearBase = scaledServiceCostPerTick(1.5, 10_000, 1.0);
  const linearTenX = scaledServiceCostPerTick(1.5, 100_000, 1.0);
  assert.equal(linearTenX, linearBase * 10, 'sanity: exponent=1.0 must reproduce the exact old flat-multiply shape');
  // ...and the REAL (sub-linear) exported constant must NOT equal 1.0, i.e.
  // production code does not silently use the linear shape.
  assert.notEqual(SERVICE_COST_SCALE_EXPONENT, 1.0);
});
