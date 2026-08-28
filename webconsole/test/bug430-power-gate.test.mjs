// bug430-power-gate.test.mjs — BUG-430: the power-GENERATION activation gate.
//
// The mirror of the building-activation draw-gate. powerStats(s).cap is grid
// CAPACITY = Σ (online power plant nameplate mw) + efwPowerOf(s). Before BUG-430
// the static-plant sum counted EVERY power plant regardless of isOnline(), so a
// road-disconnected or still-under-construction plant (incl. the Five Gorges Dam)
// fed the grid at full nameplate — inconsistent with the just-landed gate that
// zeroes an offline building's draw/jobs/residents/waste. An OFFLINE plant must
// generate ZERO, exactly like an offline building consumes/works/houses zero.
//
// Run with `npm test` (node --test); node type-strips the imported .ts so these
// assertions exercise the exact shipped powerStats aggregation — no copy, drift.
//
// Every test pins REAL numbers and is written to FAIL if the gate is dropped:
// delete the `if (!isOnline(s, b)) continue;` guard in powerStats and tests 1 & 2
// go RED (a disconnected / under-construction plant would count its full mw).
// RED proof via scratch copy (cp/mv), NEVER git (GR#24).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  SPECS,
  powerStats,
  efwPowerOf,
  isOnline,
  computeRoadConnectivity,
  constructionTicks,
  EFW_MW_PER_TONNE,
} from '../src/sim/data.ts';
import { initialState } from '../src/sim/engine.ts';

let _id = 430000;
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

const WIND = SPECS.pow_wind; // 1×1 service plant, 8 MW, cost 1400 → 3 build ticks
const WIND_MW = WIND.mw; // 8, sourced from the catalogue (GR#15) — never inlined

// ─────────────────────────────────────────────────────────────────────────────
// 1. A road-DISCONNECTED plant contributes ZERO; connecting it → full mw.
// ─────────────────────────────────────────────────────────────────────────────
test('BUG-430: a road-disconnected power plant contributes ZERO to cap; connected → full mw', () => {
  // pow_wind at (5,10), built long ago (past its construction window).
  const plant = () => B('pow_wind', 5, 10, { builtTick: 0 });

  // DISCONNECTED: one road stub at (4,10) touches the plant but the stub itself
  // is interior — not a map edge, not near a trunk — so it never joins the
  // connected network. Plant is road-ADJACENT but NOT road-CONNECTED → offline.
  const off = city([plant(), B('road', 4, 10, { builtTick: 0 })]);
  const offPlant = off.buildings[0];
  assert.equal(isOnline(off, offPlant), false, 'setup: disconnected plant must be offline');
  assert.equal(powerStats(off).cap, 0, 'an OFFLINE (disconnected) plant must add ZERO mw');

  // CONNECTED: extend the road stub to the map edge at x=0. (0,10) is an edge
  // seed; the BFS connects 0→1→2→3→4, so the (4,10) tile the plant sits beside
  // is now in the connected network → plant online → full nameplate mw.
  const on = city([
    plant(),
    B('road', 4, 10, { builtTick: 0 }),
    B('road', 3, 10, { builtTick: 0 }),
    B('road', 2, 10, { builtTick: 0 }),
    B('road', 1, 10, { builtTick: 0 }),
    B('road', 0, 10, { builtTick: 0 }),
  ]);
  const onPlant = on.buildings[0];
  assert.equal(isOnline(on, onPlant), true, 'setup: edge-connected plant must be online');
  assert.equal(powerStats(on).cap, WIND_MW, 'a connected (online) plant adds exactly its nameplate mw');
  assert.ok(WIND_MW > 0, 'sanity: the plant actually has nameplate power to add');
});

// ─────────────────────────────────────────────────────────────────────────────
// 2. An UNDER-CONSTRUCTION plant contributes zero; once built + connected → full.
// ─────────────────────────────────────────────────────────────────────────────
test('BUG-430: an under-construction plant contributes zero; once built + connected → full mw', () => {
  // Road-connected placement (edge road at (0,10) beside a plant at (1,10)), so
  // the ONLY gate in play is construction time — isolating the G1 window.
  const roads = () => [B('road', 0, 10, { builtTick: 0 })];
  const buildTicks = constructionTicks(WIND);
  assert.ok(buildTicks >= 3, 'sanity: wind has a real construction window');

  // builtTick == tick ⇒ 0 ticks elapsed < buildTicks ⇒ under construction ⇒ offline.
  const building = city([B('pow_wind', 1, 10, { builtTick: 20 }), ...roads()], 20);
  assert.equal(isOnline(building, building.buildings[0]), false, 'setup: still under construction');
  assert.equal(powerStats(building).cap, 0, 'an under-construction plant must add ZERO mw');

  // Advance past the construction window (same connected placement) ⇒ online.
  const built = city([B('pow_wind', 1, 10, { builtTick: 20 }), ...roads()], 20 + buildTicks + 1);
  assert.equal(isOnline(built, built.buildings[0]), true, 'setup: construction complete → online');
  assert.equal(powerStats(built).cap, WIND_MW, 'a completed + connected plant adds full nameplate mw');
});

// ─────────────────────────────────────────────────────────────────────────────
// 3. The EfW power term still works and is itself online-gated (regression).
// ─────────────────────────────────────────────────────────────────────────────
test('BUG-430: EfW power term intact — online EfW adds throughput power, offline EfW adds zero', () => {
  // A city with residents (waste source), collection, and an EfW incinerator.
  // builtTick omitted ⇒ isOnline true (generic path) ⇒ EfW online and processing.
  const onEfw = city([
    ...Array.from({ length: 100 }, (_, i) => B('res_block', 10 + (i % 30), 30 + Math.floor(i / 30))),
    B('waste_depot', 5, 5),
    B('waste_depot', 6, 5),
    B('waste_incinerator', 8, 5),
  ]);
  const efwMw = efwPowerOf(onEfw);
  assert.ok(efwMw > 0, 'an online EfW plant with throughput must generate power');
  assert.equal(EFW_MW_PER_TONNE, 0.5, 'sanity: EfW power rate unchanged');
  // No static power plants here ⇒ cap is exactly the EfW term (the fix left it alone).
  assert.equal(powerStats(onEfw).cap, efwMw, 'cap equals the EfW power term when there are no static plants');

  // Same city but the incinerator is UNDER CONSTRUCTION ⇒ offline ⇒ processes
  // nothing ⇒ efwPowerOf 0 ⇒ the EfW term drops out of cap. (efwPowerOf is
  // online-gated via processCapacityOf/isOnline — the fix must NOT double-gate it.)
  const offEfw = city([
    ...Array.from({ length: 100 }, (_, i) => B('res_block', 10 + (i % 30), 30 + Math.floor(i / 30))),
    B('waste_depot', 5, 5),
    B('waste_depot', 6, 5),
    B('waste_incinerator', 8, 5, { builtTick: 20 }), // 0 ticks elapsed → under construction
  ], 20);
  assert.equal(efwPowerOf(offEfw), 0, 'an offline (under-construction) EfW must add no power');
  assert.equal(powerStats(offEfw).cap, 0, 'cap drops the EfW term when the EfW plant is offline');
});

// ─────────────────────────────────────────────────────────────────────────────
// 4. Determinism (GR#21) — pure function of state; no Date/Math.random.
// ─────────────────────────────────────────────────────────────────────────────
test('BUG-430: powerStats is deterministic across identical states', () => {
  const mk = () =>
    city([
      B('pow_wind', 5, 10, { builtTick: 0 }),
      B('road', 4, 10, { builtTick: 0 }),
      B('road', 3, 10, { builtTick: 0 }),
      B('road', 2, 10, { builtTick: 0 }),
      B('road', 1, 10, { builtTick: 0 }),
      B('road', 0, 10, { builtTick: 0 }),
    ]);
  const a = powerStats(mk());
  const b = powerStats(mk());
  assert.deepEqual(a, b, 'identical states must yield identical {need, cap}');
  // Repeated evaluation of the SAME state is also stable (no hidden mutation).
  const s = mk();
  assert.deepEqual(powerStats(s), powerStats(s), 'repeat call on one state is stable');
});
