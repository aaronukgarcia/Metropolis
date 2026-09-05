// attack-bug397-round.test.mjs — INDEPENDENT destructive round against
// BUG-397 (transit fare revenue, sub-linear service costs, policy cost cap).
// Attacker: opus-round-bug397. NOT the author.
//
// Every assertion here is written to be able to FAIL: the mutation section at
// the bottom of the round's report records the three mutations run by hand
// against a scratch copy of fiscal.ts/engine.ts.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { computeFlows, reducer, initialState, LEDGER_CAP } from '../src/sim/engine.ts';
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
import { transitServedCapacity, SPECS, countByKindOnline, isOnline } from '../src/sim/data.ts';
import { runConsistencyChecks } from '../src/sim/consistency.ts';

/** Mirrors engine.ts computeFlows()'s own harbourBoost derivation exactly
 * (GR#3 — the two must never drift): 1.4 while an online land_harbour
 * building exists, 1 otherwise. */
function harbourBoostOf(s) {
  return s.buildings.some((b) => b.spec === 'land_harbour' && isOnline(s, b)) ? 1.4 : 1;
}

/** BUG-397 F2 — the reference-rate cap base a fixed computeFlows() now uses:
 * max(actual tax income, tax income at the reference rate). Recomputed here
 * from the SAME exported fiscal.ts helper engine.ts uses (never a hand copy). */
function referenceCapBase(s) {
  const c = countByKindOnline(s);
  return taxIncomeAtRate(
    s.population,
    c.commercial,
    c.industrial,
    c.mine,
    POLICY_CAP_REFERENCE_TAX_RATE,
    harbourBoostOf(s),
  );
}

// ---------------------------------------------------------------------------
// Fixture: a real reducer-built, road-connected city with money, jobs and a
// transit building, so every flow line is genuinely nonzero.
// ---------------------------------------------------------------------------
function seedCity() {
  let s = initialState();
  const services = [
    ['road', 30, 30],
    ['pylon', 31, 30],
    ['wat_clean', 32, 30],
    ['edu_primary', 34, 30],
    ['park', 36, 30],
    ['com_shop', 37, 30],
  ];
  for (const [spec, x, y] of services) s = reducer(s, { type: 'place', spec, x, y });
  s = { ...s, funds: 5_000_000_000, population: 20_000 };
  return s;
}

function spliceBuilding(s, spec, x, y) {
  return {
    ...s,
    buildings: [...s.buildings, { id: s.nextId, spec, x, y, builtTick: null }],
    nextId: s.nextId + 1,
  };
}

function sum(a) {
  return a.reduce((t, f) => t + f.value, 0);
}

// ===========================================================================
// ANGLE 1 — Money conservation across 300 ticks with Free Transit toggling.
// ===========================================================================
test('A1: 300 ticks, Free Transit toggled every 50 — funds delta == inflows - outflows every tick', () => {
  let s = seedCity();
  s = spliceBuilding(s, 'bus_station', 40, 30);
  s = spliceBuilding(s, 'off_suite', 300, 200);
  let breaks = 0;
  for (let i = 0; i < 300; i++) {
    if (i % 50 === 0 && i > 0) s = reducer(s, { type: 'policy', id: 'transitSubsidy' });
    const before = s.funds;
    s = reducer(s, { type: 'tick' });
    const expected = before + sum(s.lastFlows.inflows) - sum(s.lastFlows.outflows);
    if (s.funds !== expected) {
      breaks++;
      if (breaks === 1)
        console.log(`first conservation break at i=${i} funds=${s.funds} expected=${expected}`);
    }
    assert.ok(Number.isFinite(s.funds), `funds went non-finite at i=${i}`);
  }
  assert.equal(breaks, 0, `${breaks} ticks broke funds conservation`);
});

test('A1b: amount:0 cap-notice ledger entries never change any ledger sum and carry unique ids', () => {
  // Force the cap to bind: 0% tax => baseTaxIncome ~ 0 => cap ~ 0 while the
  // uncapped subsidy is large.
  let s = seedCity();
  s = spliceBuilding(s, 'off_suite', 300, 200);
  s = reducer(s, { type: 'tax', which: 'residential', rate: 0 });
  s = reducer(s, { type: 'tax', which: 'commercial', rate: 0 });
  s = reducer(s, { type: 'tax', which: 'industrial', rate: 0 });
  s = reducer(s, { type: 'policy', id: 'transitSubsidy' });
  for (let i = 0; i < 20; i++) s = reducer(s, { type: 'tick' });
  const ids = s.ledger.map((e) => e.id);
  assert.equal(new Set(ids).size, ids.length, 'duplicate ledger ids');
  for (const e of s.ledger) {
    assert.ok(Number.isFinite(e.amount), `non-finite ledger amount: ${JSON.stringify(e)}`);
    assert.equal(typeof e.label, 'string');
  }
  const capRows = s.ledger.filter((e) => e.label.startsWith('Transit Subsidy capped'));
  assert.ok(capRows.length > 0, 'expected the cap notice to bind at 0% tax');
  for (const r of capRows) assert.equal(r.amount, 0);
});

// FIX VERIFICATION (was FINDING F1 — ledger flooding; fixed 2026-09-05). The
// old code prepended a cap notice EVERY TICK the cap bound, into the same
// 200-entry ring BUG-400 explicitly cleaned up (that was a row every 30
// ticks and was judged a defect; this was a row every tick). Reachable,
// non-degenerate config: a legitimate LOW-TAX playstyle (rate 1) on a live
// 300k city — the cap genuinely binds every tick and the city is nowhere
// near dead. Fixed to fire once on bind + once on release (transition-only,
// via the journalled `transitSubsidyCapBound` flag).
test('A1c: FIXED — a continuously-bound cap emits exactly one notice, never floods the ledger', () => {
  let s = seedCity();
  s = spliceBuilding(s, 'off_suite', 300, 200);
  s = reducer(s, { type: 'tax', which: 'residential', rate: 1 });
  s = reducer(s, { type: 'tax', which: 'commercial', rate: 1 });
  s = reducer(s, { type: 'tax', which: 'industrial', rate: 1 });
  // A real player event the player should still be able to see later.
  s = reducer(s, { type: 'place', spec: 'road', x: 30, y: 31 });
  const marker = s.ledger[0];
  assert.ok(marker, 'expected a real player ledger event from the place action');
  s = reducer(s, { type: 'policy', id: 'transitSubsidy' });
  // 300 ticks with the cap continuously bound. BUG-397 F2's reference-rate
  // floor (fiscal.ts's POLICY_CAP_REFERENCE_TAX_RATE) grows the cap base
  // LINEARLY with population while the subsidy itself grows SUB-linearly
  // (population^0.85) — the two curves cross around ~110k population at this
  // fixture's rates, so a population pinned ABOVE that (as the pre-fix test
  // used, 300k) no longer binds at all post-fix. 50k stays safely below the
  // crossover (verified: uncapped 14,798 vs actual capped 13,056 at this
  // exact fixture), so the cap genuinely binds every one of these 300 ticks.
  for (let i = 0; i < 300; i++) {
    s = { ...s, population: 50_000 };
    s = reducer(s, { type: 'tick' });
  }
  const capRows = s.ledger.filter((e) => e.label.startsWith('Transit Subsidy capped'));
  const releaseRows = s.ledger.filter((e) => e.label.startsWith('Transit Subsidy cap released'));
  const stillThere = s.ledger.some((e) => e.id === marker.id);
  console.log(
    `LEDGER (fixed): ${capRows.length} bind notice(s), ${releaseRows.length} release notice(s) of ${s.ledger.length} rows; player event survived=${stillThere}`,
  );
  assert.equal(capRows.length, 1, 'expected EXACTLY one bind notice across 300 continuously-bound ticks');
  assert.equal(releaseRows.length, 0, 'the cap never released mid-run — no release notice expected');
  assert.ok(stillThere, 'a real player event must never be evicted by cap notices alone');

  // Now release the cap. Counterintuitively this means RAISING population,
  // not lowering it: the cap base grows LINEARLY with population (the
  // reference-rate floor) while the subsidy itself grows SUB-linearly
  // (population^0.85), so the ratio subsidy/cap is DECREASING in population
  // — a city below the ~110k crossover binds at every population down to 1,
  // and only crosses into "not bound" by growing past the threshold, exactly
  // like the 300k fixture in A4a/A4b above.
  s = { ...s, population: 500_000 };
  s = reducer(s, { type: 'tick' });
  const capRows2 = s.ledger.filter((e) => e.label.startsWith('Transit Subsidy capped'));
  const releaseRows2 = s.ledger.filter((e) => e.label.startsWith('Transit Subsidy cap released'));
  assert.equal(capRows2.length, 1, 'bind notice count must not change on release');
  assert.equal(releaseRows2.length, 1, 'expected EXACTLY one release notice');
});

// ===========================================================================
// ANGLE 2 — Fare revenue honesty.
// ===========================================================================
test('A2a: population but ZERO transit buildings earns zero fares', () => {
  let s = seedCity();
  assert.equal(transitServedCapacity(s), 0);
  const { inflows } = computeFlows(s);
  assert.equal(
    inflows.find((f) => f.label === TRANSIT_FARE_REVENUE_LABEL),
    undefined,
    'fare revenue booked with no transit network',
  );
});

test('A2b: fares are min(population, capacity) * rate — never more', () => {
  let s = seedCity();
  s = spliceBuilding(s, 'bus_station', 40, 30);
  const cap = transitServedCapacity(s);
  assert.ok(cap > 0, 'bus_station should provide served capacity');

  // capacity > population case
  const small = { ...s, population: Math.max(1, Math.floor(cap / 2)) };
  const fSmall = computeFlows(small).inflows.find((f) => f.label === TRANSIT_FARE_REVENUE_LABEL);
  assert.equal(fSmall.value, transitFareRevenuePerTick(small.population, TRANSIT_FARE_RATE_PER_RIDER));

  // population > capacity case — riders clamp at capacity
  const big = { ...s, population: cap * 100 };
  const fBig = computeFlows(big).inflows.find((f) => f.label === TRANSIT_FARE_REVENUE_LABEL);
  assert.equal(fBig.value, transitFareRevenuePerTick(cap, TRANSIT_FARE_RATE_PER_RIDER));
  assert.ok(fBig.value < transitFareRevenuePerTick(big.population, TRANSIT_FARE_RATE_PER_RIDER));
});

test('A2c: removing the station drops fares in the same computeFlows call', () => {
  let s = seedCity();
  s = spliceBuilding(s, 'bus_station', 40, 30);
  const withStation = computeFlows(s).inflows.find((f) => f.label === TRANSIT_FARE_REVENUE_LABEL);
  assert.ok(withStation && withStation.value > 0);
  const removed = { ...s, buildings: s.buildings.filter((b) => b.spec !== 'bus_station') };
  const after = computeFlows(removed).inflows.find((f) => f.label === TRANSIT_FARE_REVENUE_LABEL);
  assert.equal(after, undefined, 'fares survived the station removal (stale memo?)');
});

test('A2d: no double-count — fares are zero the tick Free Transit is ON, resume when OFF', () => {
  let s = seedCity();
  s = spliceBuilding(s, 'bus_station', 40, 30);
  const off1 = computeFlows(s);
  assert.ok(off1.inflows.some((f) => f.label === TRANSIT_FARE_REVENUE_LABEL));
  assert.ok(!off1.outflows.some((f) => f.label === 'Transit Subsidy'));

  const on = { ...s, policies: { ...s.policies, transitSubsidy: true } };
  const onFlows = computeFlows(on);
  assert.ok(!onFlows.inflows.some((f) => f.label === TRANSIT_FARE_REVENUE_LABEL), 'fares booked while free transit ON');
  assert.ok(onFlows.outflows.some((f) => f.label === 'Transit Subsidy'), 'subsidy missing while ON');

  const off2 = computeFlows({ ...on, policies: { ...on.policies, transitSubsidy: false } });
  assert.ok(off2.inflows.some((f) => f.label === TRANSIT_FARE_REVENUE_LABEL));
});

test('A2e: offline / under-construction transit contributes no capacity', () => {
  let s = seedCity();
  // builtTick far in the future => still under construction => not online.
  s = {
    ...s,
    buildings: [...s.buildings, { id: s.nextId, spec: 'bus_station', x: 40, y: 30, builtTick: 999999 }],
    nextId: s.nextId + 1,
  };
  assert.equal(transitServedCapacity(s), 0, 'under-construction transit earned fares');
});

// ===========================================================================
// ANGLE 3 — Sub-linear scaling.
// ===========================================================================
test('A3a: cost(10x pop) < 10x cost(pop), strictly', () => {
  for (const p of [10, 100, 1_000, 10_000, 100_000]) {
    const c1 = transitSubsidyCostPerTick(p);
    const c10 = transitSubsidyCostPerTick(p * 10);
    assert.ok(c10 < c1 * 10, `not sub-linear at pop=${p}: ${c10} vs ${c1 * 10}`);
    assert.ok(c10 > c1, `absolute cost must still grow: ${c10} vs ${c1}`);
  }
});

test('A3b: monotonic non-decreasing in population; pop 0 -> 0; no NaN / negative', () => {
  let prev = -1;
  for (let p = 0; p <= 3000; p += 1) {
    const c = transitSubsidyCostPerTick(p);
    assert.ok(Number.isFinite(c), `non-finite at pop=${p}`);
    assert.ok(c >= 0, `negative at pop=${p}: ${c}`);
    assert.ok(c >= prev, `NON-MONOTONIC at pop=${p}: ${c} < ${prev}`);
    prev = c;
  }
  assert.equal(transitSubsidyCostPerTick(0), 0);
  assert.equal(scaledServiceCostPerTick(1.5, -5), 0, 'negative population must not produce NaN');
  assert.ok(Number.isFinite(scaledServiceCostPerTick(1.5, 100_000_000)));
});

test('A3c: exponent is genuinely < 1 (a 1.0 exponent would be the old linear bug)', () => {
  assert.ok(SERVICE_COST_SCALE_EXPONENT < 1 && SERVICE_COST_SCALE_EXPONENT > 0);
  // The measured bankruptcy shape: pop 314,252 * 1.5 = 471,378 linear.
  const linear = Math.round(314_252 * 1.5);
  const scaled = transitSubsidyCostPerTick(314_252);
  assert.ok(scaled < linear / 2, `expected a big cut at the measured pop: ${scaled} vs ${linear}`);
});

// ===========================================================================
// ANGLE 4 — Cap semantics.
// ===========================================================================
test('A4a: policy cost never exceeds POLICY_COST_CAP_FRACTION * capBase (max(baseTaxIncome, reference))', () => {
  let s = seedCity();
  s = spliceBuilding(s, 'off_suite', 300, 200);
  s = { ...s, policies: { ...s.policies, transitSubsidy: true }, population: 500_000 };
  for (const rate of [0, 1, 5, 9, 20]) {
    const st = { ...s, taxRates: { residential: rate, commercial: rate, industrial: rate } };
    const { inflows, outflows } = computeFlows(st);
    const base = inflows
      .filter((f) => ['Council Tax', 'Business Tax', 'Freight Tax'].includes(f.label))
      .reduce((a, f) => a + f.value, 0);
    // BUG-397 F2 fix: the real cap base is max(actual, reference-rate income),
    // not the raw (possibly zero) actual base — see fiscal.ts's
    // POLICY_CAP_REFERENCE_TAX_RATE doc for the full ruling.
    const capBase = Math.max(base, referenceCapBase(st));
    const sub = outflows.find((f) => f.label === 'Transit Subsidy')?.value ?? 0;
    assert.ok(
      sub <= Math.round(capBase * POLICY_COST_CAP_FRACTION),
      `cap breached at rate ${rate}: ${sub} > ${Math.round(capBase * POLICY_COST_CAP_FRACTION)}`,
    );
  }
});

// FIX VERIFICATION (was FINDING F2 — the zero-tax exploit the brief asked to
// adjudicate; fixed 2026-09-05 per the lead's reference-rate-floor ruling).
test('A4b: FIXED — at 0% tax the cap floors at the reference-rate income, so Free Transit is NOT free', () => {
  let s = seedCity();
  s = spliceBuilding(s, 'bus_station', 40, 30);
  s = spliceBuilding(s, 'off_suite', 300, 200);
  s = {
    ...s,
    population: 500_000,
    policies: { ...s.policies, transitSubsidy: true },
    taxRates: { residential: 0, commercial: 0, industrial: 0 },
  };
  const { inflows, outflows } = computeFlows(s);
  const base = inflows
    .filter((f) => ['Council Tax', 'Business Tax', 'Freight Tax'].includes(f.label))
    .reduce((a, f) => a + f.value, 0);
  const sub = outflows.find((f) => f.label === 'Transit Subsidy')?.value ?? 0;
  const uncapped = transitSubsidyCostPerTick(s.population);
  const expectedCap = Math.round(referenceCapBase(s) * POLICY_COST_CAP_FRACTION);
  console.log(
    `ZERO-TAX: baseTax=${base} subsidyCharged=${sub} uncapped=${uncapped} expectedCap=${expectedCap}`,
  );
  assert.equal(base, 0, 'fixture did not actually starve tax income');
  assert.ok(expectedCap > 0, 'the reference-rate floor must be nonzero for this fixture');
  assert.equal(sub, Math.min(uncapped, expectedCap), 'subsidy must clamp to the reference-rate cap, not £0');
  assert.ok(sub > 0, 'the exploit (a genuinely free policy at 0% tax) must no longer be reachable');
  assert.ok(uncapped > 100_000, 'the forgone cost is large — the exploit would have been material');
  // The policy still delivers +25% growth and +8 approval, but now for a real, nonzero price.
});

// F2 regression: at or above the reference rate, the cap is UNCHANGED from
// today's baseTaxIncome-only behaviour (the reference floor must never
// tighten the cap for a city taxing normally).
test('A4d: at tax >= reference rate, the cap is unchanged from the pre-fix baseTaxIncome basis', () => {
  let s = seedCity();
  s = spliceBuilding(s, 'off_suite', 300, 200);
  s = {
    ...s,
    policies: { ...s.policies, transitSubsidy: true },
    population: 500_000,
    taxRates: {
      residential: POLICY_CAP_REFERENCE_TAX_RATE,
      commercial: POLICY_CAP_REFERENCE_TAX_RATE,
      industrial: POLICY_CAP_REFERENCE_TAX_RATE,
    },
  };
  const { inflows, outflows } = computeFlows(s);
  const base = inflows
    .filter((f) => ['Council Tax', 'Business Tax', 'Freight Tax'].includes(f.label))
    .reduce((a, f) => a + f.value, 0);
  const sub = outflows.find((f) => f.label === 'Transit Subsidy')?.value ?? 0;
  const uncapped = transitSubsidyCostPerTick(s.population);
  assert.equal(sub, Math.min(uncapped, Math.round(base * POLICY_COST_CAP_FRACTION)));
});

test('A4c: cap applies to the SUBSIDY only — other outflows are untouched by it', () => {
  let s = seedCity();
  s = spliceBuilding(s, 'off_suite', 300, 200);
  s = {
    ...s,
    population: 500_000,
    policies: { ...s.policies, transitSubsidy: true },
    taxRates: { residential: 0, commercial: 0, industrial: 0 },
  };
  const { outflows } = computeFlows(s);
  const wages = outflows.find((f) => f.label === 'Wages')?.value ?? 0;
  assert.ok(wages > 0, 'wages should still be charged even when the policy cap is 0');
});

// ===========================================================================
// ANGLE 5 — Determinism / purity.
// ===========================================================================
test('A5a: two identical runs produce byte-identical flows and funds (determinism)', () => {
  function run() {
    let s = seedCity();
    s = spliceBuilding(s, 'bus_station', 40, 30);
    s = spliceBuilding(s, 'off_suite', 300, 200);
    const trace = [];
    for (let i = 0; i < 120; i++) {
      if (i === 40 || i === 80) s = reducer(s, { type: 'policy', id: 'transitSubsidy' });
      s = reducer(s, { type: 'tick' });
      trace.push([s.funds, s.population, JSON.stringify(s.lastFlows)]);
    }
    return JSON.stringify(trace);
  }
  assert.equal(run(), run());
});

test('A5b: repeated computeFlows on the same state is pure (no accumulation, no memo drift)', () => {
  let s = seedCity();
  s = spliceBuilding(s, 'bus_station', 40, 30);
  const a = JSON.stringify(computeFlows(s));
  const b = JSON.stringify(computeFlows(s));
  const c = JSON.stringify(computeFlows(s));
  assert.equal(a, b);
  assert.equal(b, c);
});

// ===========================================================================
// ANGLE 6 — Old / degenerate save shapes must not NaN.
// ===========================================================================
test('A6a: an old-shape state (no capacityTier, unknown spec, missing served) ticks without NaN', () => {
  let s = seedCity();
  s = {
    ...s,
    buildings: [
      ...s.buildings,
      { id: 9001, spec: 'bus_station', x: 41, y: 30 }, // no builtTick, no capacityTier
      { id: 9002, spec: 'bus_stop', x: 42, y: 30, builtTick: null }, // transit-family, likely no `served`
      { id: 9003, spec: 'nonexistent_spec_xyz', x: 43, y: 30, builtTick: null },
    ],
    nextId: 9100,
  };
  const cap = transitServedCapacity(s);
  assert.ok(Number.isFinite(cap) && cap >= 0, `capacity was ${cap}`);
  const { inflows, outflows } = computeFlows(s);
  for (const f of [...inflows, ...outflows])
    assert.ok(Number.isFinite(f.value), `non-finite flow ${f.label}=${f.value}`);
  for (let i = 0; i < 10; i++) s = reducer(s, { type: 'tick' });
  assert.ok(Number.isFinite(s.funds));
});

test('A6b: save/load round trip — flows and cap notices survive JSON serialisation identically', () => {
  let s = seedCity();
  s = spliceBuilding(s, 'bus_station', 40, 30);
  s = spliceBuilding(s, 'off_suite', 300, 200);
  for (let i = 0; i < 30; i++) s = reducer(s, { type: 'tick' });
  const round = JSON.parse(JSON.stringify(s));
  assert.equal(JSON.stringify(computeFlows(round)), JSON.stringify(computeFlows(s)));
  const a = reducer(s, { type: 'tick' });
  const b = reducer(round, { type: 'tick' });
  assert.equal(JSON.stringify(a.lastFlows), JSON.stringify(b.lastFlows));
  assert.equal(a.funds, b.funds);
  assert.equal(JSON.stringify(a.ledger), JSON.stringify(b.ledger));
});

// ===========================================================================
// ANGLE 7 — Consistency gate must not red because of the new inflow.
// ===========================================================================
test('A7: consistency checks stay green across a Free-Transit-toggling run', () => {
  let s = seedCity();
  s = spliceBuilding(s, 'bus_station', 40, 30);
  s = spliceBuilding(s, 'off_suite', 300, 200);
  const reds = new Map();
  for (let i = 0; i < 60; i++) {
    if (i === 20 || i === 40) s = reducer(s, { type: 'policy', id: 'transitSubsidy' });
    s = reducer(s, { type: 'tick' });
    const rep = runConsistencyChecks(s);
    for (const c of rep.checks)
      if (!c.ok) reds.set(c.id, (reds.get(c.id) ?? 0) + 1);
  }
  // conservation must never red; the two graceable lines are allowed transients.
  const graceable = new Set(['flows.wages-matches', 'flows.upkeep-total-matches']);
  const hard = [...reds.entries()].filter(([id]) => !graceable.has(id));
  assert.deepEqual(hard, [], `hard consistency reds: ${JSON.stringify(hard)}`);
});
