// bug431-demand-gate.test.mjs — BUG-431: the power-DEMAND activation gate.
//
// The mirror of BUG-430 (power GENERATION gate) on the demand side. powerStats(s)
// .need = round(pop*0.012 + industrial*6 + office*4 + mine*8). Before BUG-431 the
// per-building consumer term counted EVERY industry/office/mine via countByKind
// regardless of isOnline(), so a road-disconnected or still-under-construction
// consumer added power DEMAND it could never draw — inconsistent with the
// activation gate that zeroes an offline building's jobs/draw and with BUG-430's
// online-gated cap. An OFFLINE consumer must draw ZERO, exactly as an offline
// plant generates zero.
//
// Water demand (waterDemandOf / serviceCoverageOf cleanwater+waste rows) is
// PURELY population-driven (need = s.population), and s.population already
// reflects only online-housed residents — so it never counted offline buildings
// per-building and needs NO gate. Test 3 pins THAT: adding an offline (or online)
// non-residential building does not change water need, because water need was
// never per-building. This documents the investigation finding.
//
// Run with `npm test` (node --test); node type-strips the imported .ts so these
// assertions exercise the exact shipped powerStats aggregation — no copy, drift.
// Every test pins REAL numbers and is written to FAIL if the demand gate is
// dropped: delete the online-gating of the consumer counts in powerStats and
// tests 1 & 2 go RED. RED proof via scratch copy (cp/mv), NEVER git (GR#24).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  SPECS,
  powerStats,
  waterDemandOf,
  isOnline,
  computeRoadConnectivity,
  constructionTicks,
} from '../src/sim/data.ts';
import { initialState } from '../src/sim/engine.ts';

let _id = 431000;
const B = (spec, x, y, extra = {}) => ({ id: _id++, spec, x, y, ...extra });

// A fresh state whose ONLY buildings are the given list, with road connectivity
// computed exactly as advance() does every tick. Fresh arrays each call so the
// per-array / per-connectivity WeakMap memos in data.ts never go stale.
function city(buildings, tick = 20, population = 0) {
  const s = initialState();
  const st = { ...s, buildings: [...buildings], population, tick };
  st.roadConnectivity = computeRoadConnectivity(st);
  return st;
}

// Consumer specs pulled from the catalogue (GR#15) — never inline the weights.
const IND = 'ind_factory'; // kind 'industrial' → +6 to need
const OFF = 'off_suite'; //   kind 'office'     → +4 to need
const IND_W = 6;
const OFF_W = 4;

// A ring of edge-connected roads that reach the map network, so a building placed
// beside one of them is road-ADJACENT and road-CONNECTED (→ online). Mirrors the
// BUG-430 test's connected-plant setup.
const connectedRoads = () => [
  B('road', 4, 10, { builtTick: 0 }),
  B('road', 3, 10, { builtTick: 0 }),
  B('road', 2, 10, { builtTick: 0 }),
  B('road', 1, 10, { builtTick: 0 }),
  B('road', 0, 10, { builtTick: 0 }),
];

// ─────────────────────────────────────────────────────────────────────────────
// 1. A road-DISCONNECTED consumer adds ZERO demand; connecting it → full demand.
// ─────────────────────────────────────────────────────────────────────────────
test('BUG-431: a road-disconnected power consumer adds ZERO to need; connected → full demand', () => {
  // DISCONNECTED: one road stub at (4,10) touches the consumer at (5,10) but the
  // stub is interior — not a map edge, not near a trunk — so it never joins the
  // connected network. Consumer is road-ADJACENT but NOT road-CONNECTED → offline.
  const off = city([B(IND, 5, 10, { builtTick: 0 }), B('road', 4, 10, { builtTick: 0 })]);
  assert.equal(isOnline(off, off.buildings[0]), false, 'setup: disconnected consumer must be offline');
  assert.equal(powerStats(off).need, 0, 'an OFFLINE industrial consumer must add ZERO to need');

  // CONNECTED: same consumer beside a road ring that reaches the network edge.
  const on = city([B(IND, 5, 10, { builtTick: 0 }), ...connectedRoads()]);
  assert.equal(isOnline(on, on.buildings[0]), true, 'setup: connected consumer must be online');
  assert.equal(powerStats(on).need, IND_W, 'a connected (online) industrial consumer adds exactly its demand');
});

// ─────────────────────────────────────────────────────────────────────────────
// 2. An UNDER-CONSTRUCTION consumer adds zero; built + connected → full demand.
// ─────────────────────────────────────────────────────────────────────────────
test('BUG-431: an under-construction power consumer adds ZERO to need; built+connected → full', () => {
  const buildTicks = constructionTicks(SPECS[OFF]);

  // builtTick == tick ⇒ 0 ticks elapsed < buildTicks ⇒ under construction ⇒ offline.
  const building = city([B(OFF, 5, 10, { builtTick: 20 }), ...connectedRoads()], 20);
  assert.equal(isOnline(building, building.buildings[0]), false, 'setup: consumer must be under construction');
  assert.equal(powerStats(building).need, 0, 'an under-construction office adds ZERO to need');

  // Advance past the construction window ⇒ online (and road-connected) ⇒ full demand.
  const built = city([B(OFF, 5, 10, { builtTick: 20 }), ...connectedRoads()], 20 + buildTicks + 1);
  assert.equal(isOnline(built, built.buildings[0]), true, 'setup: consumer must be built + online');
  assert.equal(powerStats(built).need, OFF_W, 'a completed + connected office adds its full demand');
});

// ─────────────────────────────────────────────────────────────────────────────
// 3. WATER demand is population-driven, NOT per-building: an offline (or online)
//    non-residential building never changes water need. (Investigation finding —
//    waterDemandOf reuses serviceCoverageOf cleanwater/waste rows whose need = pop,
//    and s.population is already online-gated, so no per-building water gate exists
//    or is needed.)
// ─────────────────────────────────────────────────────────────────────────────
test('BUG-431: water demand is population-driven — a non-residential building does not change it', () => {
  const pop = 5000;
  const baseline = city([...connectedRoads()], 20, pop);
  const base = waterDemandOf(baseline);
  assert.equal(base.clean, pop, 'water clean need equals population (population-driven)');
  assert.equal(base.waste, pop, 'water waste need equals population (population-driven)');

  // Add an OFFLINE consumer (interior stub road only, never reaches the network).
  // water need must be unchanged (never per-building).
  const withOffline = city([B(IND, 40, 40, { builtTick: 0 }), B('road', 41, 40, { builtTick: 0 })], 20, pop);
  assert.equal(isOnline(withOffline, withOffline.buildings[0]), false, 'setup: consumer offline');
  assert.deepEqual(waterDemandOf(withOffline), base, 'an OFFLINE non-residential building leaves water need unchanged');

  // Add an ONLINE consumer — still unchanged (water need is not per-building at all).
  const withOnline = city([B(IND, 5, 10, { builtTick: 0 }), ...connectedRoads()], 20, pop);
  assert.equal(isOnline(withOnline, withOnline.buildings[0]), true, 'setup: consumer online');
  assert.deepEqual(waterDemandOf(withOnline), base, 'an ONLINE non-residential building leaves water need unchanged');
});

// ─────────────────────────────────────────────────────────────────────────────
// 4. Determinism (GR#21): identical states → identical need; repeat calls stable.
// ─────────────────────────────────────────────────────────────────────────────
test('BUG-431: powerStats.need is deterministic across identical states', () => {
  // Both consumers sit beside the connected road at (4,10) → both online.
  const mk = () => city([B(IND, 5, 10, { builtTick: 0 }), B(OFF, 4, 11, { builtTick: 0 }), ...connectedRoads()], 20, 1000);
  const a = powerStats(mk());
  const b = powerStats(mk());
  assert.equal(a.need, b.need, 'two structurally-identical states yield identical need');
  assert.equal(a.need, Math.round(1000 * 0.012 + IND_W + OFF_W), 'need = pop term + online consumer demands');

  const s = mk();
  assert.deepEqual(powerStats(s), powerStats(s), 'repeat call on one state is stable');
});
