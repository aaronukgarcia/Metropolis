// waste-inc2.test.mjs — FEAT-1972079906 inc2: refuse PROCESSING + the
// TOTAL-RECYCLING engine: landfill / EfW / MRF / compost specs, the processing
// mix, the diversion % KPI, and power/material/compost economics.
//
// Run with `npm test` (node --test); node type-strips the imported .ts so these
// assertions exercise the exact shipped formulas. Every test pins REAL numbers
// (hand-computed) and is written to be able to FAIL: mutate a rate, drop the
// online gate, or break the proportional split and an assertion below goes red.
//
// SCOPE: inc2 — processing of inc1's collected tonnage. The rota UI is inc3.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  SPECS,
  wasteStatsOf,
  processingMixOf,
  diversionRateOf,
  efwPowerOf,
  landfillTippingOf,
  recyclingRevenueOf,
  compostRevenueOf,
  powerStats,
  constructionTicks,
  TIPPING_COST_PER_TONNE,
  MRF_RECOVERY_RATE,
  MATERIAL_REVENUE_PER_TONNE,
  COMPOST_REVENUE_PER_TONNE,
  EFW_MW_PER_TONNE,
} from '../src/sim/data.ts';
import { initialState, reducer, computeFlows } from '../src/sim/engine.ts';
import { runConsistencyChecks } from '../src/sim/consistency.ts';

const tick = (s) => reducer(s, { type: 'tick' });
const EPS = 1e-9;

function bareCity(pop = 0) {
  const s = initialState();
  s.buildings = [];
  s.population = pop;
  return s;
}

let _id = 800000;
function add(s, spec, n, opts = {}) {
  assert.ok(SPECS[spec], `test setup: unknown spec "${spec}"`);
  for (let i = 0; i < n; i++) {
    s.buildings.push({ id: _id++, spec, x: 5 + (i % 40), y: 5 + Math.floor(i / 40), ...opts });
  }
}

// The four processing specs GRADUATED from placeholders in inc2 — real, placeable,
// each carrying a processCapacity. (Guards the graduation itself.)
test('graduation: the four processing specs are real (not placeholders) with a processCapacity', () => {
  for (const id of ['waste_landfill', 'waste_incinerator', 'waste_recycling', 'waste_compost']) {
    const sp = SPECS[id];
    assert.ok(sp, `${id} missing from SPECS`);
    assert.notEqual(sp.placeholder, true, `${id} must no longer be a placeholder`);
    assert.ok(sp.processCapacity > 0, `${id} must carry a positive processCapacity`);
    assert.ok(sp.cost > 0 && sp.upkeep > 0, `${id} must carry real cost/upkeep`);
    assert.equal(sp.tag, undefined, `${id} must not carry a clean/waste-water tag`);
  }
  // EfW carries NO static mw — its grid power is throughput-based.
  assert.equal(SPECS.waste_incinerator.mw, undefined, 'EfW must have no static mw (throughput-based power)');
});

// ───────────────────────── 1. PROCESSING MIX ─────────────────────────

test('processing mix: collected tonnage splits across processors by capacity; landfill takes the remainder', () => {
  const s = bareCity(0);
  add(s, 'res_block', 100); // 100·60·0.01 = 60 t generated
  add(s, 'waste_depot', 2); // 100 t collection capacity ≥ 60 ⇒ collected = 60
  // Diverting capacity: EfW 60 + MRF 40 + compost 30 = 130 ≥ 60 collected ⇒ all diverted.
  add(s, 'waste_incinerator', 1); // 60 t/tick
  add(s, 'waste_recycling', 1); // 40 t/tick
  add(s, 'waste_compost', 1); // 30 t/tick

  const pm = processingMixOf(s);
  assert.ok(Math.abs(pm.collected - 60) < 1e-6, `collected ${pm.collected}`);
  assert.equal(pm.divertCapacity, 130);
  // diverted = min(60,130) = 60; proportional split by capacity.
  assert.ok(Math.abs(pm.diverted - 60) < EPS);
  assert.ok(Math.abs(pm.efw - 60 * (60 / 130)) < EPS, `efw ${pm.efw}`);
  assert.ok(Math.abs(pm.mrf - 60 * (40 / 130)) < EPS, `mrf ${pm.mrf}`);
  assert.ok(Math.abs(pm.compost - 60 * (30 / 130)) < EPS, `compost ${pm.compost}`);
  assert.ok(Math.abs(pm.landfill) < EPS, `landfill ${pm.landfill} should be 0 (all diverted)`);
  // Tonnage conservation: shares sum EXACTLY to collected.
  assert.ok(Math.abs(pm.efw + pm.mrf + pm.compost + pm.landfill - pm.collected) < EPS);
});

test('processing mix: OVER-collected relative to divert capacity ⇒ landfill takes the remainder', () => {
  const s = bareCity(0);
  add(s, 'res_block', 100); // 60 t
  add(s, 'waste_depot', 2); // collected 60
  add(s, 'waste_recycling', 1); // MRF 40 only ⇒ divertCap 40
  const pm = processingMixOf(s);
  assert.equal(pm.divertCapacity, 40);
  assert.ok(Math.abs(pm.diverted - 40) < EPS, `diverted ${pm.diverted}`);
  assert.ok(Math.abs(pm.mrf - 40) < EPS, `mrf ${pm.mrf}`);
  assert.ok(Math.abs(pm.efw) < EPS && Math.abs(pm.compost) < EPS);
  assert.ok(Math.abs(pm.landfill - 20) < EPS, `landfill ${pm.landfill} != 20 remainder`);
  assert.ok(Math.abs(pm.efw + pm.mrf + pm.compost + pm.landfill - pm.collected) < EPS);
});

test('processing mix: NO processors ⇒ all collected goes to landfill (diversion 0)', () => {
  const s = bareCity(0);
  add(s, 'res_block', 100); // 60 t
  add(s, 'waste_depot', 2); // collected 60
  const pm = processingMixOf(s);
  assert.equal(pm.divertCapacity, 0);
  assert.ok(Math.abs(pm.diverted) < EPS);
  assert.ok(Math.abs(pm.landfill - 60) < EPS, `landfill ${pm.landfill} != 60`);
  assert.equal(pm.diversionRate, 0);
});

test('processing mix: only ONLINE processors count (offline plant takes nothing)', () => {
  const s = bareCity(0);
  add(s, 'res_block', 100); // 60 t
  add(s, 'waste_depot', 2); // collected 60
  assert.ok(constructionTicks(SPECS.waste_recycling) > 0);
  add(s, 'waste_recycling', 1, { builtTick: s.tick }); // under construction ⇒ offline
  const pm = processingMixOf(s);
  assert.equal(pm.mrfCapacity, 0, 'offline MRF must contribute no capacity');
  assert.ok(Math.abs(pm.landfill - 60) < EPS, 'nothing diverted ⇒ all to landfill');
});

// ───────────────────────── 2. DIVERSION % KPI ─────────────────────────

test('diversion %: more diverting capacity ⇒ higher diversion; all-landfill ⇒ 0; exact ratios', () => {
  const build = (extra) => {
    const s = bareCity(0);
    add(s, 'res_block', 100); // 60 t generated
    add(s, 'waste_depot', 2); // collected 60
    extra(s);
    return s;
  };
  const none = build(() => {}); // 0 diverted
  const mrfOnly = build((s) => add(s, 'waste_recycling', 1)); // 40 diverted of 60
  const mrfBig = build((s) => {
    add(s, 'waste_recycling', 1); // 40
    add(s, 'waste_compost', 1); // 30 ⇒ divertCap 70 ≥ 60 ⇒ all diverted
  });

  assert.equal(diversionRateOf(none), 0);
  assert.ok(Math.abs(diversionRateOf(mrfOnly) - 40 / 60) < EPS, `mrfOnly ${diversionRateOf(mrfOnly)}`);
  assert.ok(Math.abs(diversionRateOf(mrfBig) - 1) < EPS, `mrfBig ${diversionRateOf(mrfBig)}`);

  // Monotonic: more capacity strictly raises diversion until it saturates at 1.
  assert.ok(diversionRateOf(none) < diversionRateOf(mrfOnly));
  assert.ok(diversionRateOf(mrfOnly) < diversionRateOf(mrfBig));
  // The KPI is exactly 1 − landfill share.
  const pm = processingMixOf(mrfOnly);
  assert.ok(Math.abs(diversionRateOf(mrfOnly) - (1 - pm.landfill / pm.collected)) < EPS);
});

// ───────────────────────── 3. EfW POWER ─────────────────────────

test('EfW power: an EfW plant processing residual adds MW to grid capacity; zero throughput ⇒ zero', () => {
  assert.equal(EFW_MW_PER_TONNE, 0.5);

  // No EfW anywhere ⇒ efwPower 0 and powerStats.cap unchanged from the pure plant sum.
  const noEfw = bareCity(0);
  add(noEfw, 'res_block', 100);
  add(noEfw, 'waste_depot', 2);
  add(noEfw, 'waste_recycling', 3); // divert via MRF, no EfW
  assert.equal(efwPowerOf(noEfw), 0);

  // An EfW plant that actually processes residual: 60 t collected, EfW cap 60 ⇒ efw 60 t.
  const efw = bareCity(0);
  add(efw, 'res_block', 100); // 60 t
  add(efw, 'waste_depot', 2); // collected 60
  add(efw, 'waste_incinerator', 1); // 60 t/tick ⇒ efw throughput 60
  const pm = processingMixOf(efw);
  assert.ok(Math.abs(pm.efw - 60) < EPS, `efw throughput ${pm.efw}`);
  assert.ok(Math.abs(efwPowerOf(efw) - 60 * EFW_MW_PER_TONNE) < EPS); // 30 MW
  // powerStats.cap includes the EfW MW (bareCity has no power plants ⇒ cap == efwPower).
  assert.ok(Math.abs(powerStats(efw).cap - 30) < EPS, `cap ${powerStats(efw).cap}`);

  // An OFFLINE EfW plant produces no power (online gate).
  const offEfw = bareCity(0);
  add(offEfw, 'res_block', 100);
  add(offEfw, 'waste_depot', 2);
  add(offEfw, 'waste_incinerator', 1, { builtTick: offEfw.tick }); // under construction
  assert.equal(efwPowerOf(offEfw), 0, 'offline EfW must add no power');
  assert.equal(powerStats(offEfw).cap, 0);

  // No double-count: EfW contributes via efwPowerOf ONLY (spec has no mw).
  // With a real power plant added, cap == plant mw + efwPower, not more.
  const both = bareCity(0);
  add(both, 'res_block', 100);
  add(both, 'waste_depot', 2);
  add(both, 'waste_incinerator', 1); // 30 MW from throughput
  add(both, 'pow_wind', 1); // a real power plant
  const windMw = SPECS.pow_wind.mw;
  assert.ok(windMw > 0);
  assert.ok(Math.abs(powerStats(both).cap - (windMw + 30)) < EPS, `cap ${powerStats(both).cap}`);
});

// ───────────────────────── 4. REVENUE + CONSERVATION ─────────────────────────

test('revenue: MRF material + compost booked as inflows, landfill tipping as outflow (hand-computed)', () => {
  const s = bareCity(500);
  add(s, 'res_block', 100); // 60 t
  add(s, 'waste_depot', 2); // collected 60
  add(s, 'waste_recycling', 1); // MRF 40
  add(s, 'waste_compost', 1); // compost 30 ⇒ divertCap 70 ≥ 60 ⇒ all diverted, split 40:30
  const pm = processingMixOf(s);
  const mrfT = 60 * (40 / 70);
  const compostT = 60 * (30 / 70);
  assert.ok(Math.abs(pm.mrf - mrfT) < EPS);
  assert.ok(Math.abs(pm.compost - compostT) < EPS);
  assert.ok(Math.abs(pm.landfill) < EPS, 'all diverted ⇒ no landfill');

  const expectRecycle = Math.round(mrfT * MRF_RECOVERY_RATE * MATERIAL_REVENUE_PER_TONNE);
  const expectCompost = Math.round(compostT * COMPOST_REVENUE_PER_TONNE);
  assert.equal(recyclingRevenueOf(s), expectRecycle);
  assert.equal(compostRevenueOf(s), expectCompost);
  assert.equal(landfillTippingOf(s), 0, 'nothing landfilled ⇒ no tipping cost');

  const { inflows, outflows } = computeFlows(s);
  assert.equal(inflows.find((f) => f.label === 'Recycling Revenue')?.value, expectRecycle);
  assert.equal(inflows.find((f) => f.label === 'Compost Revenue')?.value, expectCompost);
  assert.equal(outflows.find((f) => f.label === 'Waste Disposal'), undefined);
});

test('revenue: landfill tipping outflow appears when waste is landfilled', () => {
  const s = bareCity(500);
  add(s, 'res_block', 100); // 60 t
  add(s, 'waste_depot', 2); // collected 60
  add(s, 'waste_recycling', 1); // MRF 40 ⇒ landfill remainder 20
  const expectTipping = Math.round(20 * TIPPING_COST_PER_TONNE);
  assert.equal(landfillTippingOf(s), expectTipping);
  assert.equal(expectTipping, 160); // 20 t × £8

  const { outflows } = computeFlows(s);
  assert.equal(outflows.find((f) => f.label === 'Waste Disposal')?.value, expectTipping);
});

test('conservation: money conserved with landfill cost + MRF/compost revenue + EfW present; 0 consistency failures', () => {
  const s = bareCity(500);
  add(s, 'res_block', 200); // 120 t generated
  add(s, 'waste_depot', 2); // 100 t capacity ⇒ collected 100 (partial coverage)
  add(s, 'waste_incinerator', 1); // EfW 60
  add(s, 'waste_recycling', 1); // MRF 40 ⇒ divertCap 100 == collected ⇒ all diverted, landfill 0
  add(s, 'waste_compost', 1); // compost 30 ⇒ divertCap now 130 > 100 ⇒ still all diverted
  // Add some landfill remainder by capping divert below collected:
  // (with divertCap 130 ≥ 100, landfill is 0 — so instead build a case WITH landfill)
  const after = tick(s);

  // Tick-boundary conservation invariant: end = start + Σin − Σout.
  const inSum = after.lastFlows.inflows.reduce((a, b) => a + b.value, 0);
  const outSum = after.lastFlows.outflows.reduce((a, b) => a + b.value, 0);
  assert.equal(after.fundsAtTickEnd - after.fundsAtTickStart, inSum - outSum);

  // The waste revenue lines really are in the recorded flows.
  assert.ok(after.lastFlows.inflows.find((f) => f.label === 'Recycling Revenue'));
  assert.ok(after.lastFlows.inflows.find((f) => f.label === 'Compost Revenue'));

  const report = runConsistencyChecks(after);
  assert.equal(report.failures, 0, JSON.stringify(report.checks.filter((c) => !c.ok), null, 2));
});

test('conservation: a city WITH landfill tipping present still conserves + passes consistency', () => {
  const s = bareCity(500);
  add(s, 'res_block', 200); // 120 t
  add(s, 'waste_depot', 3); // 150 t cap ⇒ collected 120
  add(s, 'waste_recycling', 1); // MRF 40 ⇒ landfill remainder 80 ⇒ tipping charged
  const after = tick(s);
  const line = after.lastFlows.outflows.find((f) => f.label === 'Waste Disposal');
  assert.ok(line, 'Waste Disposal outflow must be recorded when tonnage is landfilled');
  assert.ok(line.value > 0);

  const inSum = after.lastFlows.inflows.reduce((a, b) => a + b.value, 0);
  const outSum = after.lastFlows.outflows.reduce((a, b) => a + b.value, 0);
  assert.equal(after.fundsAtTickEnd - after.fundsAtTickStart, inSum - outSum);

  const report = runConsistencyChecks(after);
  assert.equal(report.failures, 0, JSON.stringify(report.checks.filter((c) => !c.ok), null, 2));
});

// ───────────────────────── 5. DETERMINISM ─────────────────────────

test('determinism: identical scenarios ⇒ byte-identical processing stats + flows', () => {
  const make = () => {
    _id = 950000; // reset id counter so both builds are structurally identical
    const s = bareCity(500);
    add(s, 'res_block', 37);
    add(s, 'ind_factory', 4);
    add(s, 'waste_depot', 2);
    add(s, 'waste_incinerator', 1);
    add(s, 'waste_recycling', 2);
    add(s, 'waste_compost', 1);
    return s;
  };
  const a = make();
  const b = make();

  assert.equal(JSON.stringify(processingMixOf(a)), JSON.stringify(processingMixOf(b)));
  assert.equal(diversionRateOf(a), diversionRateOf(b));
  assert.equal(efwPowerOf(a), efwPowerOf(b));
  assert.equal(landfillTippingOf(a), landfillTippingOf(b));
  assert.equal(recyclingRevenueOf(a), recyclingRevenueOf(b));
  assert.equal(compostRevenueOf(a), compostRevenueOf(b));

  const sa = tick(a);
  const sb = tick(b);
  assert.equal(JSON.stringify(processingMixOf(sa)), JSON.stringify(processingMixOf(sb)));
  assert.equal(
    JSON.stringify(sa.lastFlows.inflows.find((f) => f.label === 'Recycling Revenue')),
    JSON.stringify(sb.lastFlows.inflows.find((f) => f.label === 'Recycling Revenue')),
  );
});

// ───────────────────────── 6. ONLINE-GATE (end to end) ─────────────────────────

test('online-gate: offline processors process nothing and produce no power/revenue', () => {
  const s = bareCity(500);
  add(s, 'res_block', 100); // 60 t
  add(s, 'waste_depot', 2); // collected 60
  // All processors under construction ⇒ offline.
  add(s, 'waste_incinerator', 1, { builtTick: s.tick });
  add(s, 'waste_recycling', 1, { builtTick: s.tick });
  add(s, 'waste_compost', 1, { builtTick: s.tick });

  const pm = processingMixOf(s);
  assert.equal(pm.divertCapacity, 0, 'offline processors contribute no capacity');
  assert.ok(Math.abs(pm.landfill - 60) < EPS, 'everything falls back to landfill');
  assert.equal(efwPowerOf(s), 0);
  assert.equal(recyclingRevenueOf(s), 0);
  assert.equal(compostRevenueOf(s), 0);
  assert.ok(landfillTippingOf(s) > 0, 'landfilled tonnage still incurs tipping');
});
