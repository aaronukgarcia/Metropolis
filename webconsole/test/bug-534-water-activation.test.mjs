// bug-534-water-activation.test.mjs — BUG-534: the same activation-consistency
// class just fixed for sumBy/totalJobs (BUG-525/527), now on the water-plant
// path. Three water-plant aggregation sites in data.ts iterated `s.buildings`
// WITHOUT an `isOnline(s, b)` gate, unlike powerStats() (data.ts ~1918:
// `if (!isOnline(s,b)) continue;`):
//
//   - serviceCoverageOf()'s own inline clean/waste accumulation loop (feeds
//     the cleanwater/waste ServiceCoverage rows).
//   - waterCaps() (duplicated water-plant capacity sum, backs waterBalanceOf
//     and waterDemandOf's headroom callers).
//   - waterPipeInfo() (the water panel's per-plant/per-tier pipe-utilisation
//     read-out).
//
// An OFFLINE / road-disconnected / under-construction water plant contributed
// its FULL served capacity while (correctly) drawing zero upkeep — free
// clean-water/sewage coverage from a non-functioning plant.
//
// Run with `npm test` (node --test); node type-strips the imported .ts so
// these assertions exercise the exact shipped aggregation — no copy, drift.
//
// Every RED-proof assertion is written to FAIL if the corresponding gate is
// dropped: temporarily strip `if (!isOnline(s, b)) continue;` from
// serviceCoverageOf / waterCaps / waterPipeInfo (scratch cp/mv, NEVER git —
// GR#24) and the offline-contributes-zero assertions below go RED (an
// offline plant would count its full served capacity). See report for the
// captured RED output.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  SPECS,
  serviceCoverageOf,
  waterCaps,
  waterPipeInfo,
  isOnline,
  computeRoadConnectivity,
  constructionTicks,
} from '../src/sim/data.ts';
import { initialState } from '../src/sim/engine.ts';

let _id = 534000;
const B = (spec, x, y, extra = {}) => ({ id: _id++, spec, x, y, ...extra });

// A fresh state whose ONLY buildings are the given list, with road
// connectivity computed exactly as advance() does every tick. Fresh arrays
// each call so the per-array / per-connectivity WeakMap memos in data.ts
// never go stale (same harness as bug-525-527-activation-coverage.test.mjs).
function city(buildings, tick = 20, population = 0) {
  const s = initialState();
  const st = { ...s, buildings: [...buildings], population, tick };
  st.roadConnectivity = computeRoadConnectivity(st);
  return st;
}

const CLEAN = SPECS.wat_clean; // water/clean, served: 20000
const CLEAN_SERVED = CLEAN.served;
const WASTE = SPECS.wat_waste; // water/waste, served: 20000
const WASTE_SERVED = WASTE.served;

// ─────────────────────────────────────────────────────────────────────────
// serviceCoverageOf() — inline clean/waste water accumulation loop.
// ─────────────────────────────────────────────────────────────────────────
test('BUG-534: an ONLINE water works contributes its full served capacity to clean-water coverage', () => {
  const roads = [B('road', 0, 10, { builtTick: 0 })];
  const s = city([B('wat_clean', 1, 10, { builtTick: 0 }), ...roads], 200, 1000);
  const plant = s.buildings[0];
  assert.equal(isOnline(s, plant), true, 'setup: connected + built-out water works must be online');

  const row = serviceCoverageOf(s).find((r) => r.id === 'cleanwater');
  assert.ok(row, 'sanity: the cleanwater coverage row exists');
  assert.equal(row.cap, CLEAN_SERVED, 'an online water works must contribute its full served capacity');
  assert.ok(CLEAN_SERVED > 0, 'sanity: the plant actually has service capacity to contribute');
});

test('BUG-534: an OFFLINE (road-disconnected) water works contributes ZERO to clean-water coverage', () => {
  // DISCONNECTED: one road stub at (1,10) touches the plant but the stub
  // itself is interior — not a map edge, not near a trunk — so it never
  // joins the connected network. Plant is road-adjacent but NOT road-
  // connected (same disconnection pattern as bug430's test 1).
  const s = city([B('wat_clean', 2, 10, { builtTick: 0 }), B('road', 1, 10, { builtTick: 0 })], 200, 1000);
  const plant = s.buildings[0];
  assert.equal(isOnline(s, plant), false, 'setup: disconnected water works must be offline');

  const row = serviceCoverageOf(s).find((r) => r.id === 'cleanwater');
  assert.equal(row.cap, 0, 'an OFFLINE water works must contribute ZERO to clean-water coverage (was the bug)');
});

test('BUG-534: an OFFLINE (under-construction) waste-water plant contributes ZERO to sewage coverage; built → full', () => {
  const buildTicks = constructionTicks(WASTE);
  assert.ok(buildTicks >= 1, 'sanity: the waste-water plant has a real construction window');
  const roads = [B('road', 0, 10, { builtTick: 0 })];

  const underConstruction = city([B('wat_waste', 1, 10, { builtTick: 20 }), ...roads], 20, 1000);
  const plantUC = underConstruction.buildings[0];
  assert.equal(isOnline(underConstruction, plantUC), false, 'setup: still under construction');
  const rowUC = serviceCoverageOf(underConstruction).find((r) => r.id === 'waste');
  assert.equal(rowUC.cap, 0, 'an under-construction waste-water plant must add ZERO sewage capacity');

  const built = city([B('wat_waste', 1, 10, { builtTick: 20 }), ...roads], 20 + buildTicks + 1, 1000);
  assert.equal(isOnline(built, built.buildings[0]), true, 'setup: construction complete → online');
  const rowBuilt = serviceCoverageOf(built).find((r) => r.id === 'waste');
  assert.equal(rowBuilt.cap, WASTE_SERVED, 'a completed + connected waste-water plant adds its full served capacity');
});

// ─────────────────────────────────────────────────────────────────────────
// waterCaps() — duplicated water-plant capacity sum.
// ─────────────────────────────────────────────────────────────────────────
test('BUG-534: waterCaps() counts an ONLINE water works, zero for an OFFLINE one', () => {
  const roads = [B('road', 0, 10, { builtTick: 0 })];
  const online = city([B('wat_clean', 1, 10, { builtTick: 0 }), ...roads], 200, 1000);
  assert.equal(isOnline(online, online.buildings[0]), true, 'setup: online water works');
  assert.equal(waterCaps(online).clean, CLEAN_SERVED, 'waterCaps must count an online plant in full');

  const offline = city([B('wat_clean', 2, 10, { builtTick: 0 }), B('road', 1, 10, { builtTick: 0 })], 200, 1000);
  assert.equal(isOnline(offline, offline.buildings[0]), false, 'setup: disconnected water works');
  assert.equal(waterCaps(offline).clean, 0, 'waterCaps must count ZERO for an offline plant (was the bug)');
});

test('BUG-534: waterCaps() counts an ONLINE waste-water plant, zero for an OFFLINE one', () => {
  const roads = [B('road', 0, 10, { builtTick: 0 })];
  const online = city([B('wat_waste', 1, 10, { builtTick: 0 }), ...roads], 200, 1000);
  assert.equal(waterCaps(online).waste, WASTE_SERVED, 'waterCaps must count an online plant in full');

  const offline = city([B('wat_waste', 2, 10, { builtTick: 0 }), B('road', 1, 10, { builtTick: 0 })], 200, 1000);
  assert.equal(waterCaps(offline).waste, 0, 'waterCaps must count ZERO for an offline plant (was the bug)');
});

// ─────────────────────────────────────────────────────────────────────────
// waterPipeInfo() — pipe-utilisation panel read-out.
// ─────────────────────────────────────────────────────────────────────────
test('BUG-534: waterPipeInfo() lists an ONLINE plant, excludes an OFFLINE one', () => {
  const roads = [B('road', 0, 10, { builtTick: 0 })];
  const online = city([B('wat_clean', 1, 10, { builtTick: 0 }), ...roads], 200, 1000);
  const infoOnline = waterPipeInfo(online);
  assert.equal(infoOnline.plants.length, 1, 'an online plant must appear in the pipe-utilisation list');
  assert.equal(infoOnline.plants[0].effServed, CLEAN_SERVED, 'an online plant reports its full effServed');

  const offline = city([B('wat_clean', 2, 10, { builtTick: 0 }), B('road', 1, 10, { builtTick: 0 })], 200, 1000);
  const infoOffline = waterPipeInfo(offline);
  assert.equal(infoOffline.plants.length, 0, 'an OFFLINE plant must be excluded from the pipe-utilisation list (was the bug)');
  assert.deepEqual(infoOffline.perTier, {}, 'an OFFLINE plant must not appear in any tier aggregate');
});

// ─────────────────────────────────────────────────────────────────────────
// Determinism (GR#21) — pure functions of state; no Date/Math.random.
// ─────────────────────────────────────────────────────────────────────────
test('BUG-534: serviceCoverageOf, waterCaps, waterPipeInfo are deterministic across identical states', () => {
  const mk = () =>
    city(
      [
        B('wat_clean', 1, 10, { builtTick: 0 }),
        B('wat_waste', 5, 10, { builtTick: 0 }),
        B('road', 0, 10, { builtTick: 0 }),
        B('road', 1, 11, { builtTick: 0 }),
      ],
      200,
      1000
    );
  assert.deepEqual(serviceCoverageOf(mk()), serviceCoverageOf(mk()), 'identical states must yield identical coverage rows');
  assert.deepEqual(waterCaps(mk()), waterCaps(mk()), 'identical states must yield identical waterCaps');
  // waterPipeInfo's per-plant entries key on building id, which B() mints
  // fresh (monotonic counter) on every call — so compare REPEAT calls on the
  // SAME state object, not two independently-minted mk() states, to isolate
  // "is the function itself pure" from "did the harness mint the same ids".
  const s = mk();
  assert.deepEqual(waterPipeInfo(s), waterPipeInfo(s), 'repeat call on one state yields identical waterPipeInfo');
  assert.deepEqual(serviceCoverageOf(s), serviceCoverageOf(s), 'repeat call on one state is stable');
});
