// water-info.test.mjs — FEAT-1972079896: the water info panel gains DEMAND and
// PIPE UTILISATION read-outs (capacity alone hid whether the network was short
// and which pipes were maxed out).
//
// Run with `npm test` (node --test); node type-strips the real TS modules, so
// these assertions exercise the exact shipped code.
//
// DISPLAY-ONLY: nothing here touches water mechanics or capacity math — the
// tests assert the new READ-OUTS only.
//
// Balance-number regime: demand reuses the ONE existing model (serviceCoverageOf
// `need`); no new demand curve is pinned. Pipe utilisation asserts SHAPE
// (at-widest-diameter ⇒ util 1.0 ⇒ at-capacity flag), not a tuned throughput
// ceiling — an absolute per-diameter ceiling is NOT defined in the data and is
// documented as a PLACEHOLDER gap (see waterPipeInfo in data.ts).
//
// RED-proven by scratch-copy (cp/mv, never git):
//   1) Break waterDemandOf to `{ clean: 0, waste: 0 }` → 'demand equals the
//      serviceCoverageOf need (SSOT)' and the debug.json demand test FAIL.
//   2) Break waterPipeInfo's atCeiling to `false` → 'a plant on the widest pipe
//      tier is flagged at-capacity' FAILS.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  SPECS,
  PIPE_TIERS,
  serviceCoverageOf,
  waterDemandOf,
  waterPipeInfo,
  waterCaps,
} from '../src/sim/data.ts';
import { initialState } from '../src/sim/engine.ts';
import { buildDebugJson, debugJsonText } from '../src/sim/debugjson.ts';

/** A city with the given population and water plants (spec → count). */
function waterCity(pop, specCounts = {}, mutate = (s) => s) {
  const s = initialState();
  s.population = pop;
  let id = 60000;
  for (const [spec, n] of Object.entries(specCounts)) {
    assert.ok(SPECS[spec], `test setup: unknown spec "${spec}"`);
    for (let i = 0; i < n; i++) {
      s.buildings.push({ id: id++, spec, x: 10 + i * 5, y: 10 });
    }
  }
  return mutate(s);
}

const testUi = () => ({
  appVersion: 'v9.9.9-test',
  frameAtMs: 1_700_000_000_000,
  map: { view: { zoom: 3, cx: 100, cy: 60 }, selectedBuildingId: null, showWater: true },
  errors: [],
});

const MAX_TIER = PIPE_TIERS.length - 1;

// ---------- DEMAND ----------

test('demand: waterDemandOf equals the serviceCoverageOf need (SSOT, no new model)', () => {
  const s = waterCity(5000, { wat_clean: 1, wat_waste: 1 });
  const cov = serviceCoverageOf(s);
  const clean = cov.find((c) => c.id === 'cleanwater').need;
  const waste = cov.find((c) => c.id === 'waste').need;
  const d = waterDemandOf(s);
  assert.equal(d.clean, clean, 'clean demand must be the cleanwater coverage need');
  assert.equal(d.waste, waste, 'waste demand must be the sewage coverage need');
  // Population-driven: both needs are the population reach.
  assert.equal(d.clean, s.population);
  assert.equal(d.waste, s.population);
});

test('demand: debug.json info.water surfaces cleanDemand/wasteDemand equal to waterDemandOf', () => {
  const s = waterCity(8000, { wat_clean: 1, wat_waste: 1 });
  const dj = JSON.parse(debugJsonText(buildDebugJson(s, testUi())));
  const d = waterDemandOf(s);
  assert.equal(dj.info.water.cleanDemand, d.clean);
  assert.equal(dj.info.water.wasteDemand, d.waste);
  // And capacity is still present alongside it, so headroom is visible.
  const caps = waterCaps(s);
  assert.equal(dj.info.water.cleanCapacity, caps.clean);
  assert.equal(dj.info.water.wasteCapacity, caps.waste);
});

test('demand: headroom is capacity − demand and goes negative when the city outgrows supply', () => {
  // One clean plant (served 20,000) but 50,000 people ⇒ demand > capacity.
  const s = waterCity(50000, { wat_clean: 1 });
  const caps = waterCaps(s);
  const d = waterDemandOf(s);
  assert.ok(caps.clean - d.clean < 0, 'a city larger than its water supply must show negative clean headroom');
});

// ---------- PIPE UTILISATION ----------

test('pipe: a plant on the widest pipe tier is flagged at-capacity (util 1.0)', () => {
  const s = waterCity(5000, { wat_clean: 1 });
  const plant = s.buildings.find((b) => SPECS[b.spec]?.kind === 'water');
  s.pipeTier[plant.id] = MAX_TIER; // widest main fitted
  const info = waterPipeInfo(s);
  const pu = info.plants.find((p) => p.id === plant.id);
  assert.ok(pu, 'plant must appear in pipe utilisation');
  assert.equal(pu.atCeiling, true, 'widest tier ⇒ at ceiling');
  assert.equal(pu.tierUtil, 1, 'widest tier ⇒ diameter utilisation 1.0');
});

test('pipe: a plant on the narrowest tier has headroom and is NOT at capacity', () => {
  const s = waterCity(5000, { wat_clean: 1 });
  const plant = s.buildings.find((b) => SPECS[b.spec]?.kind === 'water');
  s.pipeTier[plant.id] = 0; // narrowest
  const info = waterPipeInfo(s);
  const pu = info.plants.find((p) => p.id === plant.id);
  assert.equal(pu.atCeiling, false, 'narrowest tier is upgradeable, not at capacity');
  assert.ok(pu.tierUtil < 1, 'narrowest tier has diameter headroom (< 1.0)');
  assert.equal(pu.tierUtil, PIPE_TIERS[0].mult / PIPE_TIERS[MAX_TIER].mult);
});

test('pipe: pipeTiers aggregate is populated per tier with plant count and Σ effServed', () => {
  const s = waterCity(5000, { wat_clean: 2, wat_waste: 1 });
  const plants = s.buildings.filter((b) => SPECS[b.spec]?.kind === 'water');
  // Put the two clean plants on tier 0 and the waste plant on the widest tier.
  s.pipeTier[plants[0].id] = 0;
  s.pipeTier[plants[1].id] = 0;
  s.pipeTier[plants[2].id] = MAX_TIER;
  const info = waterPipeInfo(s);
  assert.equal(info.perTier[0].plants, 2, 'two plants on tier 0');
  assert.equal(info.perTier[MAX_TIER].plants, 1, 'one plant on the widest tier');
  assert.equal(info.perTier[MAX_TIER].atCeiling, true);
  assert.ok(info.perTier[0].effServedTotal > 0, 'tier 0 carries a nonzero served total');
});

test('pipe: debug.json info.water carries the per-tier aggregate and per-plant flags', () => {
  const s = waterCity(5000, { wat_clean: 1 });
  const plant = s.buildings.find((b) => SPECS[b.spec]?.kind === 'water');
  s.pipeTier[plant.id] = MAX_TIER;
  const dj = JSON.parse(debugJsonText(buildDebugJson(s, testUi())));
  // pipeTiers is no longer the empty {} the dump showed — it is populated.
  assert.ok(Object.keys(dj.info.water.pipeTiers).length > 0, 'pipeTiers must be populated when plants exist');
  assert.equal(dj.info.water.pipeTiers[MAX_TIER].atCeiling, true);
  const djPlant = dj.info.water.plants.find((p) => p.id === plant.id);
  assert.equal(djPlant.atCeiling, true, 'per-plant at-capacity flag lands in debug.json');
  assert.equal(djPlant.tierUtil, 1);
});
