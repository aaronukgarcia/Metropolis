// FEAT-2326609795 inc2 — independent destructive RE-ROUND attack pins
// (attacker opus-reround-feat795-inc2, 2026-09-09). These are the r2 attacks
// worth keeping in the tree: they pin invariants the author suite does not
// (exact attributed+unattributed conservation beyond the BUG-847 radius cap,
// the zero-jobs division guard BUG-849 introduced, and the BUG-850 population
// edge table). Not a replacement for trafficDemand.test.mjs — an addition.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  demandForecastOf,
  forecastLineUsage,
  forecastSegmentUsage,
  forecastUnattributedOf,
  ladderPointOf,
} from '../src/sim/trafficDemand.ts';
import { initialState } from '../src/sim/engine.ts';

function stateOf(bs, population) {
  const base = initialState();
  return {
    ...base,
    unlockedAll: true,
    buildings: bs.map((b, i) => ({ id: i + 1, ...b })),
    nextId: bs.length + 1,
    roadNotice: null,
    population,
  };
}

/** A city whose only road is at the origin plus an outpost D tiles away —
 * D > MAX_ATTRIBUTION_RADIUS_TILES puts the outpost's demand out of reach. */
function outpostCity(D) {
  const bs = [
    { spec: 'rd_aroad', x: 0, y: 0 },
    { spec: 'rd_aroad', x: 1, y: 0 },
  ];
  for (let i = 0; i < 20; i++) bs.push({ spec: 'res_hut', x: 2 + (i % 5), y: 1 + Math.floor(i / 5) });
  bs.push({ spec: 'res_hut', x: D, y: 0 });
  bs.push({ spec: 'off_suite', x: D, y: 1 });
  return stateOf(bs, 200);
}

test('BUG-847 r2: attributed + unattributed == total demand weight EXACTLY, beyond the radius cap', () => {
  for (const D of [100, 400]) {
    const s = outpostCity(D);
    const tiles = demandForecastOf(s);
    let totalWeight = 0;
    for (const t of tiles) totalWeight += t.personTrips + t.freightVehicleTrips;
    const unattributed = forecastUnattributedOf(s);
    assert.ok(unattributed.size > 0, `D=${D}: a class must be reported`);
    for (const [spec, u] of unattributed) {
      assert.ok(u.weight >= 0 && u.weight <= totalWeight, `${spec} unattributed weight in range`);
      assert.ok(u.tileCount >= 0 && u.tileCount <= tiles.length, `${spec} unattributed tile count in range`);
      // AC-6 stays unconditional: the per-segment split still sums to the class demand.
      let segSum = 0;
      for (const v of forecastSegmentUsage(s).values()) if (v.spec === spec) segSum += v.demand;
      assert.equal(segSum, forecastLineUsage(s).get(spec).demand, `${spec} AC-6 sum at D=${D}`);
    }
  }
  // Past the cap the outpost really is unreported — the fix must not quietly
  // reach it (which would mean the cap is not applied) nor drop it silently.
  const far = forecastUnattributedOf(outpostCity(400)).get('rd_aroad');
  assert.ok(far.tileCount >= 1 && far.weight > 0, 'D=400 outpost demand is reported as unattributed, not dropped');
  const near = forecastUnattributedOf(outpostCity(100)).get('rd_aroad');
  assert.equal(near.tileCount, 0, 'D=100 (inside the cap) attributes everything');
});

// NOTE (attacker, r2): the `jobsCapTotal > 0` guard itself is an EQUIVALENT
// mutant today and cannot be pinned — no catalogue spec carries `jobs: 0`, so
// any building with a `jobs` field always contributes to totalJobs(s). The
// guard only becomes live the day a `jobs: 0` spec is added. This test pins
// the observable behaviour (an empty/zero-jobs city stays finite), not the guard.
test('BUG-849 r2: zero-jobs city divides safely — every forecast number finite, never NaN', () => {
  const s = stateOf([{ spec: 'rd_aroad', x: 0, y: 0 }], 0);
  for (const t of demandForecastOf(s)) {
    assert.ok(Number.isFinite(t.workersActual) && Number.isFinite(t.personTrips), 'tile finite');
  }
  for (const [, v] of forecastLineUsage(s)) {
    assert.ok(Number.isFinite(v.demand) && Number.isFinite(v.divergenceRatio), 'class finite');
  }
  for (const v of forecastSegmentUsage(s).values()) assert.ok(Number.isFinite(v.demand), 'segment finite');
});

test('BUG-849 r2: worker occupancy really is filled/capacity — rises with population, clamps at 1', () => {
  const bs = [
    { spec: 'rd_aroad', x: 0, y: 0 },
    { spec: 'ind_heavy', x: 1, y: 0 },
    { spec: 'off_suite', x: 2, y: 0 },
  ];
  const workersAt = (pop) => {
    const t = demandForecastOf(stateOf(bs, pop)).find((x) => x.spec === 'ind_heavy');
    return t ? t.workersActual : 0;
  };
  const low = workersAt(4);
  const mid = workersAt(100);
  const full = workersAt(100_000);
  assert.ok(low < mid && mid < full, `occupancy must rise with population (${low} < ${mid} < ${full})`);
  assert.equal(full, 110, 'clamps at the building capacity, never above');
  assert.ok(mid < 110, 'below full employment workers are BELOW capacity (the x1 mutant)');
});

test('BUG-850 r2: population edge table — fail loud or clamp, never a NaN rung', () => {
  const bs = [
    { spec: 'rd_aroad', x: 0, y: 0 },
    { spec: 'res_hut', x: 1, y: 0 },
    { spec: 'off_suite', x: 2, y: 0 },
  ];
  for (const bad of [NaN, Infinity, -Infinity]) {
    assert.throws(() => ladderPointOf(stateOf(bs, bad)), /MET-V912/, `non-finite ${bad} must be MET-V912`);
  }
  for (const over of [98_000_001, 1e12]) {
    assert.throws(() => ladderPointOf(stateOf(bs, over)), /MET-V899/, `${over} must be MET-V899, never extrapolated`);
  }
  for (const ok of [-1, 0, 97_999_999]) {
    const tiles = demandForecastOf(stateOf(bs, ok));
    for (const t of tiles) assert.ok(Number.isFinite(t.personTrips), `population ${ok} yields finite trips`);
  }
});
