// attack-bug397-reround2.test.mjs — INDEPENDENT destructive re-round 2 on
// BUG-397 (attacker: opus-reround2-bug397, NOT the author). Round 1 REJECTED
// on F1 (cap-notice ledger flood), F2 (0% tax = free Free Transit), F3 (false
// SSOT doc claim), F4 (+£0 rendered as income). This file attacks the REWORK.
//
// Attack axes:
//   1. Transition semantics under adversarial / realistic oscillation.
//   2. Factoring proof: freightTaxPerTick / taxIncomeAtRate vs the live flows.
//   3. Reference-rate ruling edge cases (at / below / above / 0 / 30 / hostile).
//   4. Journal / save / replay of the new transitSubsidyCapBound boolean.
//   5. Ledger-consumer and conservation safety of the new amount:0 + fare rows.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { computeFlows, reducer, initialState, LEDGER_CAP } from '../src/sim/engine.ts';
import {
  POLICY_COST_CAP_FRACTION,
  POLICY_CAP_REFERENCE_TAX_RATE,
  TRANSIT_FARE_RATE_PER_RIDER,
  TRANSIT_FARE_REVENUE_LABEL,
  transitSubsidyCostPerTick,
  transitFareRevenuePerTick,
  taxIncomeAtRate,
  freightTaxPerTick,
  councilTaxPerTick,
  businessTaxPerTick,
  scaledServiceCostPerTick,
} from '../src/sim/fiscal.ts';
import {
  countByKindOnline,
  isOnline,
  isBrownoutActive,
  congestionFactorOf,
  transitServedCapacity,
} from '../src/sim/data.ts';

const TAX_LABELS = ['Council Tax', 'Business Tax', 'Freight Tax'];
const CAP_BIND_RE = /^Transit Subsidy capped at /;
const CAP_RELEASE_RE = /^Transit Subsidy cap released/;

function harbourBoostOf(s) {
  return s.buildings.some((b) => b.spec === 'land_harbour' && isOnline(s, b)) ? 1.4 : 1;
}

function baseTaxOf(inflows) {
  return inflows.filter((f) => TAX_LABELS.includes(f.label)).reduce((a, f) => a + f.value, 0);
}

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
  return { ...s, funds: 5_000_000_000, population: 20_000 };
}

function withPolicyOn(s) {
  return { ...s, policies: { ...s.policies, transitSubsidy: true } };
}

function setAllRates(s, rate) {
  return { ...s, taxRates: { residential: rate, commercial: rate, industrial: rate } };
}

function capNoticeRows(s) {
  return s.ledger.filter((e) => CAP_BIND_RE.test(e.label) || CAP_RELEASE_RE.test(e.label));
}

// ===========================================================================
// AXIS 1 — transition semantics under oscillation.
// ===========================================================================

// 1a. Worst case the MECHANISM can produce: a boundary that flips every single
// tick. This is the chatter bound. If the engine can be driven to alternate,
// the transition gate degenerates back to one row per tick.
test('A1a: MECHANISM BOUND — a per-tick bind/release flip emits one notice per tick', () => {
  // Drive computeFlows directly, threading the flag exactly as advance() does,
  // with a population that straddles the cap boundary every other tick.
  const base = withPolicyOn(setAllRates(seedCity(), 1));
  // find a population where the cap binds and one where it does not
  let lowPop = null;
  let highPop = null;
  for (let p = 1_000; p <= 4_000_000; p = Math.round(p * 1.15)) {
    const r = computeFlows({ ...base, population: p });
    if (r.transitSubsidyCapBound) highPop = p;
    else lowPop = p;
    if (lowPop !== null && highPop !== null) break;
  }
  assert.ok(lowPop !== null && highPop !== null, 'need a bound and an unbound population to straddle');

  let flag = false;
  let notices = 0;
  for (let t = 0; t < 200; t++) {
    const pop = t % 2 === 0 ? lowPop : highPop;
    const r = computeFlows({ ...base, population: pop, transitSubsidyCapBound: flag });
    notices += r.policyCapNotices.length;
    flag = r.transitSubsidyCapBound;
  }
  // Documented, not asserted-as-good: this records the worst case for the report.
  assert.ok(notices > 0, 'sanity: an oscillating boundary does emit notices');
  console.log(`A1a chatter bound: ${notices} notices / 200 ticks (lowPop=${lowPop} highPop=${highPop})`);
  assert.ok(notices <= 200, 'never more than one notice per tick');
});

// 1b. The realism question: does a NORMAL game, run long, chatter? A growing
// city crosses the boundary once because the cap grows LINEARLY with
// population (council tax) while the cost grows SUB-linearly (pop^0.85).
test('A1b: REALISM — 400 ticks of a live growing city emit at most a handful of cap notices', () => {
  let s = withPolicyOn(setAllRates(seedCity(), 2));
  s = { ...s, population: 200_000 };
  for (let t = 0; t < 400; t++) s = reducer(s, { type: 'tick' });
  const rows = capNoticeRows(s);
  console.log(`A1b live-city notices over 400 ticks: ${rows.length}`);
  assert.ok(rows.length <= 10, `cap notices flooded the ledger: ${rows.length} rows in 400 ticks`);
  assert.ok(s.ledger.length <= LEDGER_CAP);
});

// 1c. Monotone-crossing property that makes 1b true: once unbound at some
// population, the subsidy stays unbound at every LARGER population (linear cap
// vs sub-linear cost). This is the structural reason chatter is not realistic.
test('A1c: cap-bound is monotone-decreasing in population (single crossing, no chatter)', () => {
  const base = withPolicyOn(setAllRates(seedCity(), 3));
  let sawUnbound = false;
  for (let p = 1_000; p <= 20_000_000; p = Math.round(p * 1.2)) {
    const bound = computeFlows({ ...base, population: p }).transitSubsidyCapBound;
    if (!bound) sawUnbound = true;
    else
      assert.ok(
        !sawUnbound,
        `cap re-bound at pop ${p} after having released at a smaller population — population alone can chatter the notice`,
      );
  }
});

// 1d. Player-driven chatter: toggling the policy off/on every tick while the
// cap binds. Each OFF forces the flag false -> a release notice; each ON
// re-binds -> a bind notice. Two rows per two ticks.
test('A1d: player toggling the policy every tick emits a notice every tick (documented)', () => {
  let s = withPolicyOn(setAllRates(seedCity(), 1));
  s = { ...s, population: 800_000 };
  for (let t = 0; t < 30; t++) s = reducer(s, { type: 'tick' });
  assert.equal(s.transitSubsidyCapBound, true, 'fixture must actually be capped before toggling');
  const baseline = capNoticeRows(s).length;
  for (let t = 0; t < 60; t++) {
    s = reducer(s, { type: 'tick' });
    s = reducer(s, { type: 'policy', id: 'transitSubsidy' });
  }
  const rows = capNoticeRows(s).length - baseline;
  console.log(`A1d toggle-chatter notices over 60 ticks: ${rows}`);
  assert.ok(rows <= 60, 'never more than one per tick');
});

// ===========================================================================
// AXIS 2 — factoring proof (GR#3): the refactor must not have changed the
// normal path. We cannot run the old code, so we prove the extracted helpers
// reproduce the LIVE flow lines exactly.
// ===========================================================================

test('A2a: taxIncomeAtRate(current rate) === the live flows tax income, 50 random states', () => {
  let checked = 0;
  for (let i = 0; i < 50; i++) {
    // deterministic pseudo-random (GR#21: no Math.random in a sim test)
    const h = (i * 2654435761) >>> 0;
    const rate = (h % 31); // 0..30, all three sliders equal so one rate applies
    const pop = 1_000 + ((h >>> 5) % 500_000);
    const s = setAllRates({ ...seedCity(), population: pop }, rate);
    if (isBrownoutActive(s)) continue;
    if (congestionFactorOf(s) < 1) continue;
    checked++;
    const { inflows } = computeFlows(s);
    const c = countByKindOnline(s);
    const expected = taxIncomeAtRate(pop, c.commercial, c.industrial, c.mine, rate, harbourBoostOf(s));
    assert.equal(
      baseTaxOf(inflows),
      expected,
      `tax income drifted from taxIncomeAtRate at rate ${rate}, pop ${pop}`,
    );
  }
  assert.ok(checked >= 40, `too few clean states checked (${checked}) — test is vacuous`);
});

test('A2b: freightTaxPerTick reproduces the live Freight Tax line exactly', () => {
  for (const rate of [0, 1, 3, 9, 13, 30]) {
    const s = setAllRates({ ...seedCity(), population: 50_000 }, rate);
    if (isBrownoutActive(s) || congestionFactorOf(s) < 1) continue;
    const { inflows } = computeFlows(s);
    const c = countByKindOnline(s);
    const live = inflows.find((f) => f.label === 'Freight Tax')?.value ?? 0;
    assert.equal(live, freightTaxPerTick(c.industrial, c.mine, rate, harbourBoostOf(s)));
  }
});

test('A2c: taxIncomeAtRate is monotonic non-decreasing in rate and exactly 0 at rate 0', () => {
  const s = { ...seedCity(), population: 123_456 };
  const c = countByKindOnline(s);
  assert.equal(taxIncomeAtRate(s.population, c.commercial, c.industrial, c.mine, 0, 1), 0);
  let prev = -1;
  for (let r = 0; r <= 30; r++) {
    const v = taxIncomeAtRate(s.population, c.commercial, c.industrial, c.mine, r, 1);
    assert.ok(Number.isFinite(v), `non-finite tax income at rate ${r}`);
    assert.ok(v >= prev, `taxIncomeAtRate not monotonic: rate ${r} -> ${v} < ${prev}`);
    prev = v;
  }
  // and it is the exact sum of its three parts (no hidden term)
  assert.equal(
    taxIncomeAtRate(s.population, c.commercial, c.industrial, c.mine, 7, 1.4),
    councilTaxPerTick(s.population, 7) + businessTaxPerTick(c.commercial, 7) + freightTaxPerTick(c.industrial, c.mine, 7, 1.4),
  );
});

// ===========================================================================
// AXIS 3 — reference-rate ruling edge cases.
// ===========================================================================

test('A3a: at the reference rate exactly, the cap equals the pre-ruling (actual-income) cap', () => {
  const s = withPolicyOn(setAllRates({ ...seedCity(), population: 300_000 }, POLICY_CAP_REFERENCE_TAX_RATE));
  const { inflows, outflows } = computeFlows(s);
  const legacyCap = Math.round(baseTaxOf(inflows) * POLICY_COST_CAP_FRACTION);
  const sub = outflows.find((f) => f.label === 'Transit Subsidy').value;
  const uncapped = transitSubsidyCostPerTick(s.population);
  // outflow policies are identity here (no recycling/austerity in the fixture)
  assert.equal(sub, Math.min(uncapped, legacyCap), 'reference-rate city must behave exactly as before the ruling');
});

test('A3b: BELOW the reference rate the cap FLOORS (0% tax is no longer free)', () => {
  const pop = 300_000;
  const s0 = withPolicyOn(setAllRates({ ...seedCity(), population: pop }, 0));
  const { inflows, outflows } = computeFlows(s0);
  assert.equal(baseTaxOf(inflows), 0, 'fixture must actually have zero tax income');
  const sub = outflows.find((f) => f.label === 'Transit Subsidy').value;
  assert.ok(sub > 0, 'Free Transit is FREE at 0% tax — the F2 exploit is back');
  const c = countByKindOnline(s0);
  const ref = taxIncomeAtRate(pop, c.commercial, c.industrial, c.mine, POLICY_CAP_REFERENCE_TAX_RATE, harbourBoostOf(s0));
  assert.equal(sub, Math.min(transitSubsidyCostPerTick(pop), Math.round(ref * POLICY_COST_CAP_FRACTION)));
});

test('A3c: ABOVE the reference rate the cap follows the HIGHER actual income', () => {
  const pop = 3_000_000;
  const lo = withPolicyOn(setAllRates({ ...seedCity(), population: pop }, POLICY_CAP_REFERENCE_TAX_RATE));
  const hi = withPolicyOn(setAllRates({ ...seedCity(), population: pop }, 30));
  const subLo = computeFlows(lo).outflows.find((f) => f.label === 'Transit Subsidy').value;
  const subHi = computeFlows(hi).outflows.find((f) => f.label === 'Transit Subsidy').value;
  assert.ok(subHi >= subLo, 'a richer city must not get a SMALLER cap');
  // and the floor never drags a high-tax city's cap DOWN
  const capHi = Math.round(baseTaxOf(computeFlows(hi).inflows) * POLICY_COST_CAP_FRACTION);
  assert.equal(subHi, Math.min(transitSubsidyCostPerTick(pop), capHi));
});

test('A3d: the cap is monotone non-decreasing across the whole 0..30 rate slider', () => {
  const pop = 5_000_000;
  let prev = -1;
  for (let r = 0; r <= 30; r++) {
    const s = withPolicyOn(setAllRates({ ...seedCity(), population: pop }, r));
    const sub = computeFlows(s).outflows.find((f) => f.label === 'Transit Subsidy').value;
    assert.ok(Number.isFinite(sub), `non-finite subsidy at rate ${r}`);
    assert.ok(sub >= prev, `subsidy cap dips at rate ${r}: ${sub} < ${prev}`);
    prev = sub;
  }
});

test('A3e: hostile rates — negative rates never produce a negative or non-finite subsidy', () => {
  for (const r of [-1, -30, -1e9]) {
    const s = withPolicyOn(setAllRates({ ...seedCity(), population: 300_000 }, r));
    const { outflows } = computeFlows(s);
    const sub = outflows.find((f) => f.label === 'Transit Subsidy').value;
    assert.ok(Number.isFinite(sub), `non-finite subsidy at rate ${r}`);
    assert.ok(sub >= 0, `negative subsidy (income!) at rate ${r}: ${sub}`);
  }
});

test('A3f: NaN tax rate — record whether the money path is NaN-poisoned (inherited vs introduced)', () => {
  const s = withPolicyOn(setAllRates({ ...seedCity(), population: 300_000 }, Number.NaN));
  const { inflows, outflows } = computeFlows(s);
  const council = inflows.find((f) => f.label === 'Council Tax')?.value;
  const sub = outflows.find((f) => f.label === 'Transit Subsidy')?.value;
  const councilNaN = !Number.isFinite(council);
  const subNaN = !Number.isFinite(sub);
  console.log(`A3f NaN-rate: Council Tax finite=${!councilNaN}, Transit Subsidy finite=${!subNaN}`);
  // The claim under test is only that the BUG-397 cap does not introduce a NEW
  // NaN source: if the subsidy is NaN, the pre-existing tax line must be too.
  if (subNaN) assert.ok(councilNaN, 'BUG-397 cap introduced a NaN the pre-existing tax path did not have');
});

test('A3g: no NaN in funds after 100 ticks at rate 0 and at rate 30', () => {
  for (const rate of [0, 30]) {
    let s = withPolicyOn(setAllRates({ ...seedCity(), population: 400_000 }, rate));
    for (let t = 0; t < 100; t++) {
      s = reducer(s, { type: 'tick' });
      assert.ok(Number.isFinite(s.funds), `funds went non-finite at rate ${rate}, tick ${t}: ${s.funds}`);
    }
    assert.ok(Number.isFinite(s.funds));
  }
});

test('A3h: scaledServiceCostPerTick is finite and >= 0 for hostile populations', () => {
  for (const p of [0, -1, -1e9, 1, 1e8, Number.MAX_SAFE_INTEGER]) {
    const v = scaledServiceCostPerTick(1.5, p);
    assert.ok(Number.isFinite(v), `non-finite scaled cost at population ${p}`);
    assert.ok(v >= 0, `negative scaled cost at population ${p}`);
  }
});

// ===========================================================================
// AXIS 4 — journal / save / replay of the new boolean.
// ===========================================================================

test('A4a: determinism — two identical seeded runs produce identical flags and notice sequences', () => {
  const mk = () => withPolicyOn(setAllRates({ ...seedCity(), population: 800_000 }, 1));
  let a = mk();
  let b = mk();
  for (let t = 0; t < 60; t++) {
    a = reducer(a, { type: 'tick' });
    b = reducer(b, { type: 'tick' });
    assert.equal(a.transitSubsidyCapBound, b.transitSubsidyCapBound, `flag diverged at tick ${t}`);
  }
  assert.deepEqual(
    capNoticeRows(a).map((e) => [e.tick, e.label, e.amount]),
    capNoticeRows(b).map((e) => [e.tick, e.label, e.amount]),
  );
  assert.equal(JSON.stringify(a.lastFlows), JSON.stringify(b.lastFlows));
});

test('A4b: JSON save/load round-trip mid-run is byte-identical thereafter', () => {
  let s = withPolicyOn(setAllRates({ ...seedCity(), population: 800_000 }, 1));
  for (let t = 0; t < 30; t++) s = reducer(s, { type: 'tick' });
  const saved = JSON.parse(JSON.stringify(s));
  assert.equal(typeof saved.transitSubsidyCapBound, 'boolean', 'the flag must survive JSON serialisation');
  let live = s;
  let loaded = saved;
  for (let t = 0; t < 30; t++) {
    live = reducer(live, { type: 'tick' });
    loaded = reducer(loaded, { type: 'tick' });
  }
  assert.equal(live.transitSubsidyCapBound, loaded.transitSubsidyCapBound);
  assert.equal(JSON.stringify(live.ledger), JSON.stringify(loaded.ledger));
  assert.equal(live.funds, loaded.funds);
});

test('A4c: a LEGACY save with the field ABSENT re-emits at most ONE extra bind notice', () => {
  let s = withPolicyOn(setAllRates({ ...seedCity(), population: 800_000 }, 1));
  for (let t = 0; t < 30; t++) s = reducer(s, { type: 'tick' });
  assert.equal(s.transitSubsidyCapBound, true, 'fixture must be mid-cap when saved');
  const legacy = JSON.parse(JSON.stringify(s));
  delete legacy.transitSubsidyCapBound;
  const before = capNoticeRows(s).length;
  let modern = s;
  let old = legacy;
  for (let t = 0; t < 40; t++) {
    modern = reducer(modern, { type: 'tick' });
    old = reducer(old, { type: 'tick' });
  }
  const extra = capNoticeRows(old).length - capNoticeRows(modern).length;
  console.log(`A4c legacy-save extra notices: ${extra} (baseline before load: ${before})`);
  assert.ok(extra >= 0 && extra <= 1, `legacy save produced ${extra} extra cap notices, expected 0..1`);
  // and after the one-off, the two converge on state
  assert.equal(old.transitSubsidyCapBound, modern.transitSubsidyCapBound);
  assert.equal(old.funds, modern.funds, 'the legacy default must not move money');
});

test('A4d: the flag is DERIVED each tick, never remembered — a forged flag self-corrects in one tick', () => {
  let s = withPolicyOn(setAllRates({ ...seedCity(), population: 800_000 }, 1));
  for (let t = 0; t < 10; t++) s = reducer(s, { type: 'tick' });
  const truth = computeFlows(s).transitSubsidyCapBound;
  const forged = reducer({ ...s, transitSubsidyCapBound: !truth }, { type: 'tick' });
  const honest = reducer(s, { type: 'tick' });
  assert.equal(forged.transitSubsidyCapBound, honest.transitSubsidyCapBound, 'flag must be recomputed, not carried');
  assert.equal(forged.funds, honest.funds, 'a forged flag must not move money');
});

// ===========================================================================
// AXIS 5 — ledger consumers + conservation with the new rows.
// ===========================================================================

test('A5a: the cap-notice row is amount 0 and moves no money (conservation holds)', () => {
  let s = withPolicyOn(setAllRates({ ...seedCity(), population: 800_000 }, 1));
  for (let t = 0; t < 40; t++) {
    const before = s.funds;
    const next = reducer(s, { type: 'tick' });
    const inflow = next.lastFlows.inflows.reduce((a, f) => a + f.value, 0);
    const outflow = next.lastFlows.outflows.reduce((a, f) => a + f.value, 0);
    // Ledger rows are RECORDS of flow money (milestone rewards etc. are booked
    // as inflows), so funds must move by exactly in - out; an amount:0 notice
    // row must therefore be invisible to this identity.
    assert.equal(
      next.funds,
      before + inflow - outflow,
      `funds conservation broke at tick ${t}: in=${inflow} out=${outflow}`,
    );
    s = next;
  }
  for (const e of capNoticeRows(s)) assert.equal(e.amount, 0, 'a cap notice must never carry money');
});

test('A5b: every ledger row is shaped for its consumers (finite numeric amount, string label, unique id)', () => {
  let s = withPolicyOn(setAllRates({ ...seedCity(), population: 800_000 }, 1));
  for (let t = 0; t < 60; t++) s = reducer(s, { type: 'tick' });
  const ids = new Set();
  for (const e of s.ledger) {
    assert.equal(typeof e.amount, 'number');
    assert.ok(Number.isFinite(e.amount), `non-finite ledger amount: ${JSON.stringify(e)}`);
    assert.equal(typeof e.label, 'string');
    assert.ok(e.label.length > 0);
    assert.equal(typeof e.tick, 'number');
    assert.ok(!ids.has(e.id), `duplicate ledger id ${e.id}`);
    ids.add(e.id);
  }
  assert.ok(s.ledger.length <= LEDGER_CAP);
  // and the amount:0 rows are still JSON-round-trippable for save/export
  assert.equal(JSON.stringify(s.ledger), JSON.stringify(JSON.parse(JSON.stringify(s.ledger))));
});

test('A5c: Free Transit ON forgoes the fare inflow; OFF books it, riders capped by network reach', () => {
  const off = setAllRates({ ...seedCity(), population: 800_000 }, 9);
  const on = withPolicyOn(off);
  const fOff = computeFlows(off).inflows.find((f) => f.label === TRANSIT_FARE_REVENUE_LABEL);
  const fOn = computeFlows(on).inflows.find((f) => f.label === TRANSIT_FARE_REVENUE_LABEL);
  assert.equal(fOn, undefined, 'Free Transit must forgo fare revenue');
  const cap = transitServedCapacity(off);
  const expected = transitFareRevenuePerTick(Math.min(off.population, cap), TRANSIT_FARE_RATE_PER_RIDER);
  assert.equal(fOff?.value ?? 0, expected);
  assert.ok(expected <= off.population * TRANSIT_FARE_RATE_PER_RIDER + 1, 'riders must never exceed population');
});

test('A5d: fare revenue is non-negative and finite for hostile populations/capacities', () => {
  for (const [riders, rate] of [[0, 0.4], [-5, 0.4], [1e9, 0.4], [100, 0]]) {
    const v = transitFareRevenuePerTick(riders, rate);
    assert.ok(Number.isFinite(v) && v >= 0, `bad fare revenue for riders=${riders} rate=${rate}: ${v}`);
  }
});
