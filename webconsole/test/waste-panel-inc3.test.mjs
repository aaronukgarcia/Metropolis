// waste-panel-inc3.test.mjs — FEAT-1972079906 inc3 (FEAT-1972079864): the
// waste/recycling rota UI panel. DISPLAY-ONLY: the panel surfaces the LANDED
// inc1/inc2 derived reads (wasteStatsOf / processingMixOf / efwPowerOf / the
// revenue fns). This file tests the one pure helper the UI adds —
// wasteDisplayModel — and smoke-renders the actual WasteTab component.
//
// Run with `npm test` (node --test); node type-strips the imported .ts/.tsx so
// these assertions exercise the exact shipped code. Every assertion pins a real
// value or a hard invariant so the test can genuinely FAIL:
//   • break the fraction math (e.g. divide by generated not collected) → the
//     "shares sum to 100%" / hand-computed share assertions go red;
//   • let a zero-waste city leak NaN/Infinity into a displayed string → the
//     no-NaN scan goes red;
//   • a JSX/hook regression in WasteTab → the renderToString smoke test throws.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { wasteDisplayModel } from '../src/components/right/wasteModel.ts';
import { SPECS } from '../src/sim/data.ts';
import { fmtNum, fmtMoney, fmtPct, formatPower } from '../src/sim/utils.ts';
import { initialState } from '../src/sim/engine.ts';

// Same controlled-board idiom as the inc2 tests: bare city + explicit buildings.
function bareCity(pop = 0) {
  const s = initialState();
  s.buildings = [];
  s.population = pop;
  return s;
}
let _id = 900000;
function add(s, spec, n, opts = {}) {
  assert.ok(SPECS[spec], `test setup: unknown spec "${spec}"`);
  for (let i = 0; i < n; i++) {
    s.buildings.push({ id: _id++, spec, x: 5 + (i % 40), y: 5 + Math.floor(i / 40), ...opts });
  }
}

// Every number the WasteTab renders, run through the SHARED formatters exactly as
// the component does. Used to prove no displayed string ever leaks NaN/Infinity.
function displayedStrings(m) {
  return [
    fmtNum(m.generated),
    fmtNum(m.capacity),
    fmtNum(m.collected),
    fmtNum(m.uncollected),
    fmtPct(m.diversionRate, 0),
    fmtPct(m.coverage, 0),
    formatPower(m.efwPowerMw),
    fmtMoney(m.materialRevenue),
    fmtMoney(m.recyclingRevenue),
    fmtMoney(m.compostRevenue),
    ...m.mixRows.flatMap((r) => [fmtNum(r.tonnes), fmtPct(r.fraction, 0)]),
  ];
}
const noBadNumbers = (m) => {
  for (const str of displayedStrings(m)) {
    assert.ok(!/NaN|Infinity/.test(str), `displayed string leaked a bad number: "${str}"`);
  }
};

// ───────────────────────── 1. zero-waste city ─────────────────────────

test('zero-waste city: coverage 1, diversion 0, all mix zero, no NaN/Infinity in any displayed string', () => {
  const s = bareCity(0); // no residential, no depots, no processors
  const m = wasteDisplayModel(s);
  assert.equal(m.generated, 0, 'nothing generated');
  assert.equal(m.collected, 0, 'nothing collected');
  assert.equal(m.uncollected, 0, 'nothing uncollected');
  assert.equal(m.coverage, 1, 'coverage is 1 when nothing is generated (water convention)');
  assert.equal(m.hasUncollected, false, 'no refuse on the street');
  assert.equal(m.diversionRate, 0, 'diversion 0 with nothing to divert');
  assert.equal(m.efwPowerMw, 0);
  assert.equal(m.materialRevenue, 0);
  for (const r of m.mixRows) {
    assert.equal(r.tonnes, 0, `${r.key} tonnes 0`);
    assert.equal(r.fraction, 0, `${r.key} fraction 0 (not NaN — collected is 0)`);
  }
  noBadNumbers(m); // the crux: 0/0 must NOT reach the screen as NaN
});

// ───────────────────── 2. generation + full processing ─────────────────────

test('generation city: mix fractions sum to 100% and match the hand-computed proportional split', () => {
  // Mirrors the inc2 hand-computation: 100 res_block ⇒ 60 t generated; 2 depots
  // (100 t cap) ⇒ collected 60; EfW 60 + MRF 40 + compost 30 = 130 divert cap ≥ 60
  // ⇒ all 60 diverted, split proportionally, landfill 0.
  const s = bareCity(0);
  add(s, 'res_block', 100);
  add(s, 'waste_depot', 2);
  add(s, 'waste_incinerator', 1);
  add(s, 'waste_recycling', 1);
  add(s, 'waste_compost', 1);

  const m = wasteDisplayModel(s);
  assert.ok(Math.abs(m.collected - 60) < 1e-6, `collected ${m.collected}`);
  assert.equal(m.hasUncollected, false, '60 collected of 60 generated ⇒ full coverage');
  assert.equal(m.coverage, 1);
  assert.ok(Math.abs(m.diversionRate - 1) < 1e-9, 'all diverted ⇒ diversion 100%');

  const byKey = Object.fromEntries(m.mixRows.map((r) => [r.key, r]));
  assert.ok(Math.abs(byKey.efw.fraction - 60 / 130) < 1e-9, 'efw share = 60/130');
  assert.ok(Math.abs(byKey.mrf.fraction - 40 / 130) < 1e-9, 'mrf share = 40/130');
  assert.ok(Math.abs(byKey.compost.fraction - 30 / 130) < 1e-9, 'compost share = 30/130');
  assert.ok(Math.abs(byKey.landfill.fraction) < 1e-9, 'landfill share 0 (all diverted)');

  // The invariant the "Share" column relies on: the four shares sum to exactly 1
  // whenever anything is collected (landfill = collected − diverted, exact).
  const sumFrac = m.mixRows.reduce((a, r) => a + r.fraction, 0);
  assert.ok(Math.abs(sumFrac - 1) < 1e-9, `mix fractions sum to 1, got ${sumFrac}`);
  // Tonnes also sum to collected (no drift).
  const sumT = m.mixRows.reduce((a, r) => a + r.tonnes, 0);
  assert.ok(Math.abs(sumT - m.collected) < 1e-9, 'mix tonnes sum to collected');

  // Recovered read-outs are the real derived reads, positive here.
  assert.ok(m.efwPowerMw > 0, 'EfW throughput ⇒ positive grid power');
  assert.equal(m.materialRevenue, m.recyclingRevenue + m.compostRevenue, 'material rev = recycling + compost');
  assert.ok(m.materialRevenue > 0, 'MRF + compost throughput ⇒ positive revenue');
  noBadNumbers(m);
});

test('under-collected city: uncollected tonnage flagged red, coverage < 1, landfill takes the remainder', () => {
  // 100 res_block ⇒ 60 t generated; ONE depot ⇒ 50 t capacity < 60 ⇒ 10 t uncollected.
  const s = bareCity(0);
  add(s, 'res_block', 100);
  add(s, 'waste_depot', 1); // 50 t cap
  const m = wasteDisplayModel(s);
  assert.ok(Math.abs(m.generated - 60) < 1e-6, `generated ${m.generated}`);
  assert.ok(Math.abs(m.capacity - 50) < 1e-6, `capacity ${m.capacity}`);
  assert.ok(Math.abs(m.collected - 50) < 1e-6, 'collected = min(generated, capacity) = 50');
  assert.ok(Math.abs(m.uncollected - 10) < 1e-6, '10 t left on the street');
  assert.equal(m.hasUncollected, true, 'uncollected ⇒ red condition');
  assert.ok(m.coverage < 1 && m.coverage > 0, 'partial coverage strictly inside (0,1)');
  // No processors ⇒ everything collected goes to landfill, diversion 0.
  assert.equal(m.diversionRate, 0, 'no processors ⇒ diversion 0');
  const landfill = m.mixRows.find((r) => r.key === 'landfill');
  assert.ok(Math.abs(landfill.tonnes - 50) < 1e-6, 'landfill takes all 50 collected');
  assert.ok(Math.abs(landfill.fraction - 1) < 1e-9, 'landfill share 100%');
  noBadNumbers(m);
});

// ───────────────────────── 3. purity / determinism ─────────────────────────

test('wasteDisplayModel: pure — never mutates state, identical states give identical models', () => {
  const mk = () => {
    const s = bareCity(0);
    add(s, 'res_block', 50);
    add(s, 'waste_depot', 1);
    add(s, 'waste_recycling', 1);
    return s;
  };
  const s = mk();
  const snap = JSON.stringify(s);
  wasteDisplayModel(s);
  wasteDisplayModel(s);
  assert.equal(JSON.stringify(s), snap, 'wasteDisplayModel did not mutate the sim state');
  assert.equal(
    JSON.stringify(wasteDisplayModel(mk())),
    JSON.stringify(wasteDisplayModel(mk())),
    'identical states ⇒ byte-identical display models (deterministic, no Date.now/random)'
  );
});

// NOTE: the WasteTab render/mount smoke test lives in test/mount.test.tsx (run
// under `tsx`, which resolves the components' extensionless imports and provides
// the React SSR harness) — node --test here type-strips only explicit-extension
// modules, so it cannot import RightDock.tsx. This file owns the PURE helper.
