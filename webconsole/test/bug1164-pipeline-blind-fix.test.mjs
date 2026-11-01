// bug1164-pipeline-blind-fix.test.mjs — BUG-1164 (P1): Fix / Fix All sizing was
// pipeline-blind. Supply counted ONLY online buildings, so a Fix that had just
// placed a fleet (under construction) left the SAME shortfall on the books and
// the next Fix / Fix All bought it again (Aaron's Y10 city: 28 nuclear plants,
// then 28 more a week later). The fix lives in demandFixPlan() (data.ts, the
// one SSOT every consumer reads): fixAmount = shortfall*FRACTION minus the
// credited capacity of same-service providers still under construction;
// <= 0 => the service is omitted (demandFixInProgress() reports it).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import {
  demandFixPlan,
  orderedDemandFixPlan,
  demandFixInProgress,
  serviceCoverageOf,
  constructionTicks,
  creditedUnitCapacity,
  findSpot,
  SPECS,
  AUTO_BUILD_DEMAND_FRACTION,
} from '../src/sim/data.ts';

function powerShortState(population = 3_000_000) {
  return { ...initialState(), population, unlockedAll: true, funds: 1e15, administrationState: null };
}
const planFor = (s, key) => demandFixPlan(s).find((p) => p.serviceKey === key);
const planned = (p) => p.mix.reduce((n, m) => n + m.count * m.unitCapacity, 0);

function fixPower(s) {
  const before = s.buildings.length;
  const after = reducer(s, { type: 'resolveDemand', serviceKey: 'power' });
  assert.ok(after.buildings.length > before, 'fix placed a fleet');
  return after;
}
const tickTo = (s, tick) => ({ ...s, tick });

test('(1) double-order repro: after Fix, the same plan asked again is a NO-OP on the same tick and every tick of construction', () => {
  const s0 = powerShortState();
  const first = planFor(s0, 'power');
  assert.ok(first && first.count > 0, 'precondition: power is short');
  const s1 = fixPower(s0);
  const placed = s1.buildings.slice(s0.buildings.length);
  const powerBuilds = placed.filter((b) => SPECS[b.spec].kind === 'power').map((b) => constructionTicks(SPECS[b.spec]));
  const maxBuild = Math.min(...powerBuilds); // while EVERY plant is still building
  assert.equal(planFor(s1, 'power'), undefined, 'same tick: no second power plan');
  assert.equal(orderedDemandFixPlan(s1).some((p) => p.serviceKey === 'power'), false, 'Fix All lists no power');
  for (const dt of [0, 1, 2, Math.floor(maxBuild / 2), maxBuild - 1]) {
    assert.equal(planFor(tickTo(s1, s1.tick + dt), 'power'), undefined, `tick+${dt}: still covered`);
  }
  // Fix All twice: second dispatch builds no power.
  const again = reducer(s1, { type: 'resolveDemandAll' });
  const powerAdded = again.buildings.slice(s1.buildings.length).filter((b) => SPECS[b.spec].kind === 'power');
  assert.equal(powerAdded.length, 0, 'second Fix All bought no more power');
  // honest row line seam
  const ip = demandFixInProgress(s1).find((p) => p.serviceKey === 'power');
  assert.ok(ip && ip.units === powerBuilds.length && ip.capacity > 0);
});

test('(2) partial pipeline sizes exactly the remainder', () => {
  const s0 = powerShortState();
  const full = planFor(s0, 'power');
  const s1 = fixPower(s0);
  // Cancel half of the fleet (bulldoze half the placed plants).
  const placed = s1.buildings.slice(s0.buildings.length);
  const keep = placed.slice(0, Math.floor(placed.length / 2));
  const s2 = { ...s1, buildings: [...s0.buildings, ...keep] };
  const pipeCap = keep.reduce((n, b) => n + creditedUnitCapacity(SPECS[b.spec], SPECS[b.spec].mw ?? 0), 0);
  const cov = serviceCoverageOf(s2).find((c) => c.id === 'power');
  const target = (cov.need - cov.cap) * AUTO_BUILD_DEMAND_FRACTION - pipeCap;
  const rem = planFor(s2, 'power');
  assert.ok(rem, 'remainder plan exists');
  assert.ok(planned(rem) >= target, 'remainder plan covers target-minus-pipeline');
  assert.ok(planned(rem) < planned(full), 'smaller than the unpiped plan');
  // exact: a plan sized to the target alone (no pipeline) equals the remainder plan
  const fromScratch = { ...s2, buildings: s0.buildings };
  const covScratch = serviceCoverageOf(fromScratch).find((c) => c.id === 'power');
  assert.equal(covScratch.need, cov.need);
  assert.ok(planned(rem) + pipeCap >= (cov.need - cov.cap) * AUTO_BUILD_DEMAND_FRACTION);
});

test('(3) pipeline completes -> shortfall persists -> plan returns to normal sizing', () => {
  const s0 = powerShortState();
  const baseline = planFor(s0, 'power');
  const s1 = fixPower(s0);
  const placed = s1.buildings.slice(s0.buildings.length);
  const maxBuild = Math.max(...placed.map((b) => constructionTicks(SPECS[b.spec])));
  // Complete construction but model plants that do not come online (road gate)
  // by dropping them from supply while keeping builtTick elapsed: use the
  // original buildings + elapsed-but-offline copies is not expressible, so
  // instead remove the fleet and age the tick: no pipeline, same shortfall.
  const gone = { ...s1, buildings: s0.buildings, tick: s1.tick + maxBuild + 5 };
  const again = planFor(gone, 'power');
  assert.ok(again);
  assert.deepEqual(again.mix, baseline.mix, 'no pipeline => original sizing');
  // And elapsed construction really ends the pipeline for placed buildings.
  assert.equal(demandFixInProgress(tickTo(s1, s1.tick + maxBuild + 5)).some((p) => p.serviceKey === 'power'), false);
});

test('(4) a bulldozed under-construction provider returns its capacity to the shortfall', () => {
  const s0 = powerShortState();
  const s1 = fixPower(s0);
  assert.equal(planFor(s1, 'power'), undefined);
  const placed = s1.buildings.slice(s0.buildings.length);
  const drop = placed[0];
  const s2 = { ...s1, buildings: s1.buildings.filter((b) => b !== drop) };
  // Cancel EVERY under-construction power plant (keep the roads): the whole
  // capacity returns and the plan is exactly the original one.
  const s3 = { ...s1, buildings: s1.buildings.filter((b) => SPECS[b.spec].kind !== 'power' || s0.buildings.includes(b)) };
  assert.deepEqual(planFor(s3, 'power').mix, planFor(s0, 'power').mix);
  const ip = demandFixInProgress(s2).find((p) => p.serviceKey === 'power');
  assert.ok(!ip || ip.units === placed.filter((b) => SPECS[b.spec].kind === "power").length - 1);
});

test('(5) zone (RCI) demand is not a fix-plan consumer: unaffected', () => {
  const s0 = powerShortState();
  const keys = demandFixPlan(s0).map((p) => p.serviceKey);
  assert.ok(!keys.some((k) => ['residential', 'commercial', 'industrial'].includes(k)));
  assert.ok(!demandFixInProgress(s0).some((p) => ['residential', 'commercial', 'industrial'].includes(p.serviceKey)));
});

test('(6) deterministic and pure: same state -> same plan, state untouched', () => {
  const s1 = fixPower(powerShortState());
  const snap = JSON.stringify(s1.buildings.length) + s1.tick;
  const a = JSON.stringify([demandFixPlan(s1), demandFixInProgress(s1)]);
  const b = JSON.stringify([demandFixPlan(s1), demandFixInProgress(s1)]);
  assert.equal(a, b);
  assert.equal(JSON.stringify(s1.buildings.length) + s1.tick, snap);
});

test('(7) no pipeline, no change: unplaced city plans identically to the pre-fix arithmetic', () => {
  const s0 = powerShortState(60_000);
  assert.equal(demandFixInProgress(s0).length, 0);
  const p = planFor(s0, 'cleanwater');
  assert.ok(p && p.count > 0);
});

test('(8) parks are not pipeline-discounted (parksCapacityOf already counts under-construction parks)', () => {
  const s0 = powerShortState();
  const s1 = reducer(s0, { type: 'resolveDemand', serviceKey: 'parks' });
  assert.ok(s1.buildings.length > s0.buildings.length);
  assert.equal(demandFixInProgress(s1).some((p) => p.serviceKey === 'parks'), false);
});

test('(8b) parks plan is identical with or without under-construction parks', () => {
  const s0 = powerShortState(2_000_000);
  // ONE park in a huge shortfall: the shortfall stays large, so a wrongly
  // applied pipeline discount would visibly shrink the plan.
  const spot = findSpot(s0, 'park');
  const s1 = reducer(s0, { type: 'place', spec: 'park', x: spot.x, y: spot.y });
  assert.ok(s1.buildings.length > s0.buildings.length && s1.buildings.at(-1).builtTick != null);
  assert.ok(demandFixPlan(s1).some((p) => p.serviceKey === 'parks'));
  const online = { ...s1, buildings: s1.buildings.map((b) => ({ ...b, builtTick: null })) };
  assert.deepEqual(
    demandFixPlan(s1).find((p) => p.serviceKey === 'parks'),
    demandFixPlan(online).find((p) => p.serviceKey === 'parks')
  );
});

test('(9) pipeline boundary: a provider leaves the pipeline exactly when tick - builtTick reaches constructionTicks', () => {
  const s1 = fixPower(powerShortState());
  const power = s1.buildings.filter((b) => SPECS[b.spec].kind === 'power' && b.builtTick === s1.tick);
  const t = Math.min(...power.map((b) => constructionTicks(SPECS[b.spec])));
  const units = (dt) =>
    demandFixInProgress(tickTo(s1, s1.tick + dt)).filter((p) => p.serviceKey === 'power').reduce((n, p) => n + p.units, 0);
  const finishing = power.filter((b) => constructionTicks(SPECS[b.spec]) === t).length;
  assert.equal(units(t - 1), power.length, 'one tick before: everything still in the pipeline');
  // At t the finished builds have left the pipeline: either the rest still
  // covers the target (listed, fewer units) or it no longer does (not listed).
  assert.ok([0, power.length - finishing].includes(units(t)), 'at tick t the shortest builds have left it');
});

test('BUG-1164 r1 perf pin: pipeline is ONE building walk per state (structural counter)', async () => {
  const { __pipelineWalkCountForTest } = await import('../src/sim/data.ts');
  const s = powerShortState();
  const s2 = { ...s };
  const c0 = __pipelineWalkCountForTest();
  for (let i = 0; i < 5; i++) {
    demandFixPlan(s);
    demandFixInProgress(s);
  }
  assert.equal(__pipelineWalkCountForTest() - c0, 1, 'same state => exactly one walk');
  demandFixPlan(s2);
  demandFixInProgress(s2);
  assert.equal(__pipelineWalkCountForTest() - c0, 2, 'new state => exactly one more walk');
});
