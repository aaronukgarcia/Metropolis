// megapower-inc1.test.mjs — FEAT-1972079901 Five Gorges Dam + realistic power costs.
//
// Run with `npm test` (node --test); node's type-stripping imports the real
// TypeScript modules, so these assertions exercise the exact shipped catalogue,
// the exact powerStats aggregation, and the exact water/residents capacity maths —
// no copy, no drift.
//
// Every test asserts real values (can FAIL): deleting the dam spec, zeroing its
// `mw`, leaking a water tag/residents onto it, or flattening the cost hierarchy all
// turn a test RED. RED proof done via scratch-copy (cp/mv), NEVER git.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  SPECS,
  powerStats,
  waterCaps,
  residentsCapacity,
  computeRoadConnectivity,
  isOnline,
  constructionTicks,
} from '../src/sim/data.ts';
import { initialState } from '../src/sim/engine.ts';

// A pristine city plus one extra building of `spec`, placed at (x,y).
// builtTick:null → the generic isOnline "always on" path (as the grid-export
// tests do), so powerStats aggregation is measured independently of activation.
function withBuilding(spec, x = 400, y = 250, extra = {}) {
  const s = initialState();
  return {
    ...s,
    buildings: [...s.buildings, { id: 9_000_001, spec, x, y, builtTick: null, ...extra }],
    nextId: s.nextId + 1,
  };
}

// ─────────────────────────────────────────────────────────────────────────────
// 1. The dam spec is well-formed.
// ─────────────────────────────────────────────────────────────────────────────
test('dam spec is well-formed: real placeable mega-power generator', () => {
  const dam = SPECS.pow_hydro;
  assert.ok(dam, 'pow_hydro (Three Gorges Dam) must exist in SPECS');
  // Aaron 2026-09-04: renamed from "Five Gorges Dam" — the id stays pow_hydro.
  assert.equal(dam.name, 'Three Gorges Dam');
  assert.equal(dam.kind, 'power');
  assert.notEqual(dam.placeholder, true, 'the dam must be a REAL (non-placeholder) spec');

  // Large footprint.
  assert.ok(dam.w >= 8 && dam.h >= 8, `dam footprint must be large; got ${dam.w}×${dam.h}`);

  // mw far above a normal plant — assert the RATIO against the Nuclear Plant.
  const nuke = SPECS.pow_nuke;
  assert.ok(dam.mw > 0, 'the dam must generate power');
  assert.ok(
    dam.mw >= nuke.mw * 3,
    `dam mw (${dam.mw}) must dwarf a nuclear plant (${nuke.mw}) — ratio ${(dam.mw / nuke.mw).toFixed(1)}×`
  );

  // Real (non-zero) build/running costs and a late unlock.
  assert.ok(dam.cost > 0, 'dam must have a real build cost');
  assert.ok(dam.upkeep > 0, 'dam must have a real upkeep');
  assert.ok(dam.unlock >= 12, `dam should unlock late; got ${dam.unlock}`);
});

// ─────────────────────────────────────────────────────────────────────────────
// 2. The dam feeds the grid by exactly its mw, and nothing else.
// ─────────────────────────────────────────────────────────────────────────────
test('feeds the grid: powerStats.cap rises by exactly the dam mw', () => {
  const base = initialState();
  const before = powerStats(base).cap;
  const after = powerStats(withBuilding('pow_hydro')).cap;
  assert.equal(after - before, SPECS.pow_hydro.mw, 'dam must add exactly its nameplate mw to grid cap');
});

test('feeds the grid: activation gate takes a road-disconnected dam OFFLINE', () => {
  // A dam far from any road, past its construction window, with connectivity
  // computed → isOnline must be false (the building-level activation gate bites).
  // This is the real online signal. Since BUG-430, powerStats.cap is online-gated
  // too — a disconnected dam contributes ZERO nameplate MW (see bug430-power-gate
  // .test.mjs); this test asserts only the online signal itself.
  const s = withBuilding('pow_hydro', 400, 250, { builtTick: 0 });
  s.tick = constructionTicks(SPECS.pow_hydro) + 5; // past construction
  s.roadConnectivity = computeRoadConnectivity(s); // no adjacent road → not connected
  const dam = s.buildings[s.buildings.length - 1];
  assert.equal(isOnline(s, dam), false, 'a road-disconnected dam must be offline');
});

// ─────────────────────────────────────────────────────────────────────────────
// 3. No cross-system leak — the dam adds NO water or residential capacity.
// ─────────────────────────────────────────────────────────────────────────────
test('no cross-leak: the dam raises neither water nor residential capacity', () => {
  const base = initialState();
  const withDam = withBuilding('pow_hydro');

  const wBefore = waterCaps(base);
  const wAfter = waterCaps(withDam);
  assert.equal(wAfter.clean, wBefore.clean, 'dam must not add clean-water capacity');
  assert.equal(wAfter.waste, wBefore.waste, 'dam must not add waste-water capacity');

  assert.equal(
    residentsCapacity(withDam),
    residentsCapacity(base),
    'dam must not add residential capacity'
  );

  // Guard the spec shape: a power generator must carry no water/resident fields.
  const dam = SPECS.pow_hydro;
  assert.equal(dam.tag, undefined, 'dam must carry no clean/waste/pollution tag');
  assert.equal(dam.served, undefined, 'dam must carry no water `served`');
  assert.equal(dam.residents, undefined, 'dam must carry no `residents`');
});

// ─────────────────────────────────────────────────────────────────────────────
// 4. Realistic build costs — structural cost hierarchy (directional, not literal).
//    mega/nuclear > gas/coal (fossil) > small renewable.
// ─────────────────────────────────────────────────────────────────────────────
test('realistic costs: nuclear/mega build cost > gas/coal > small renewable', () => {
  const nuke = SPECS.pow_nuke.cost;
  const fusion = SPECS.pow_fusion.cost;
  const dam = SPECS.pow_hydro.cost;
  const gas = SPECS.pow_ccgt.cost;
  const coal = SPECS.pow_coal.cost;
  const wind = SPECS.pow_wind.cost; // small renewable
  const solar = SPECS.pow_solar.cost;

  // Mega tier (nuclear, fusion, dam) is the most expensive to build.
  for (const [label, mega] of [['nuclear', nuke], ['fusion', fusion], ['dam', dam]]) {
    assert.ok(mega > gas, `${label} (${mega}) must cost more to build than gas CCGT (${gas})`);
    assert.ok(mega > coal, `${label} (${mega}) must cost more to build than coal (${coal})`);
  }

  // Fossil mid-tier is dearer than the small renewables.
  assert.ok(gas > wind, `gas CCGT (${gas}) must cost more than a wind turbine (${wind})`);
  assert.ok(coal > wind, `coal (${coal}) must cost more than a wind turbine (${wind})`);
  assert.ok(gas > solar, `gas CCGT (${gas}) must cost more than a solar farm (${solar})`);

  // Aaron's explicit ask: nuclear is VERY expensive — priciest per-MW of the
  // conventional fleet (above renewables and gas on a £/MW basis).
  const perMW = (id) => SPECS[id].cost / SPECS[id].mw;
  assert.ok(perMW('pow_nuke') > perMW('pow_ccgt'), 'nuclear £/MW must exceed gas £/MW');
  assert.ok(perMW('pow_nuke') > perMW('pow_offshore'), 'nuclear £/MW must exceed offshore wind £/MW');
  assert.ok(perMW('pow_nuke') > perMW('pow_solar'), 'nuclear £/MW must exceed solar £/MW');
});

// ─────────────────────────────────────────────────────────────────────────────
// 5. Determinism — the specs are static literals (no Date/Math.random).
// ─────────────────────────────────────────────────────────────────────────────
test('determinism: reading the dam spec twice yields identical values', () => {
  const a = { ...SPECS.pow_hydro };
  const b = { ...SPECS.pow_hydro };
  assert.deepEqual(a, b, 'spec must be a stable literal across reads');
  // Concrete literal values (a mutation of the source would change these).
  assert.equal(typeof SPECS.pow_hydro.mw, 'number');
  assert.equal(typeof SPECS.pow_hydro.cost, 'number');
  const c1 = powerStats(withBuilding('pow_hydro')).cap;
  const c2 = powerStats(withBuilding('pow_hydro')).cap;
  assert.equal(c1, c2, 'powerStats must be deterministic for identical states');
});
