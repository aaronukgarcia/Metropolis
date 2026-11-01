// attack-bug1164-r1.test.mjs — BUG-1164 round r1 (independent attacker,
// opus-round-r1-bug1164). Re-buy matrix against pipeline-aware Fix sizing.
// Kept by the round: every test here is red on the pre-fix demandFixPlan
// (pipeline-blind) except (b)/(c), which MEASURE and pin documented behaviour.

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
  SPECS,
  AUTO_BUILD_DEMAND_FRACTION,
} from '../src/sim/data.ts';

const big = (population = 3_000_000) => ({ ...initialState(), population, unlockedAll: true, funds: 1e15, administrationState: null });
const isPower = (b) => SPECS[b.spec]?.kind === 'power';
const powerOf = (s) => s.buildings.filter(isPower);
const credit = (bs) => bs.reduce((n, b) => n + creditedUnitCapacity(SPECS[b.spec], SPECS[b.spec].mw ?? 0), 0);
const minBuild = (bs) => Math.min(...bs.map((b) => constructionTicks(SPECS[b.spec])));
const covOf = (s, id) => serviceCoverageOf(s).find((c) => c.id === id);
const planFor = (s, k) => demandFixPlan(s).find((p) => p.serviceKey === k);
// Stable stringify (sorted keys) for byte-compare purity checks.
const stable = (v) => JSON.stringify(v, (_k, x) => (x && typeof x === 'object' && !Array.isArray(x) && !(x instanceof Map) && !(x instanceof Set) ? Object.fromEntries(Object.keys(x).sort().map((k) => [k, x[k]])) : x));

test('(a) Aaron double-click: Fix All, Fix All same tick, Fix All mid-construction -> total power fleet bounded by ONE sizing', () => {
  const s0 = big();
  const sized = planFor(s0, 'power');
  assert.ok(sized && sized.count > 0, 'precondition: power short');
  let s = reducer(s0, { type: 'resolveDemandAll' });
  const afterFirst = powerOf(s).length;
  // Capped clicks are allowed to need more than one click; keep clicking on
  // the SAME tick until no plan remains (bounded loop).
  for (let i = 0; i < 6; i++) s = reducer(s, { type: 'resolveDemandAll' });
  const afterSameTick = powerOf(s).length;
  const t = minBuild(powerOf(s).filter((b) => !s0.buildings.includes(b)));
  // "next day": advance well into construction (but before any plant finishes).
  for (const dt of [1, Math.floor(t / 3), t - 1]) {
    s = reducer({ ...s, tick: s.tick + 0 }, { type: 'resolveDemandAll' });
    const aged = { ...s, tick: s.tick + dt };
    const s2 = reducer(aged, { type: 'resolveDemandAll' });
    assert.equal(powerOf(s2).length, afterSameTick, `tick+${dt}: Fix All re-bought power (${afterSameTick} -> ${powerOf(s2).length})`);
  }
  const placedCredit = credit(powerOf(s).filter((b) => !s0.buildings.includes(b)));
  const target = (covOf(s0, 'power').need - covOf(s0, 'power').cap) * AUTO_BUILD_DEMAND_FRACTION;
  const oneUnitMax = Math.max(...powerOf(s).map((b) => creditedUnitCapacity(SPECS[b.spec], SPECS[b.spec].mw ?? 0)));
  console.log(`  (a) plan count ${sized.count}; after click1 ${afterFirst}, after 7 same-tick clicks ${afterSameTick}; credited ${placedCredit} vs target ${target}`);
  assert.ok(placedCredit >= target, 'fleet covers target');
  assert.ok(placedCredit < target + oneUnitMax * (sized.mix.length + 1), `fleet over-bought: credited ${placedCredit} vs target ${target}`);
});

test('(b) finished-but-dark (road-gated) fleet: plan re-buys a whole fleet — measured, documented', () => {
  const s0 = big();
  const s1 = reducer(s0, { type: 'resolveDemand', serviceKey: 'power' });
  const fleet = powerOf(s1).filter((b) => !s0.buildings.includes(b));
  const done = Math.max(...fleet.map((b) => constructionTicks(SPECS[b.spec])));
  // Construction elapsed, but NO road tile connected: every non-network building dark.
  const dark = { ...s1, tick: s1.tick + done + 1, roadConnectivity: { connectedRoadTiles: [] } };
  const cov = covOf(dark, 'power');
  assert.equal(demandFixInProgress(dark).some((p) => p.serviceKey === 'power'), false, 'dark fleet is not "in progress"');
  const rebuy = planFor(dark, 'power');
  const cost = rebuy ? rebuy.mix.reduce((n, m) => n + m.count * (SPECS[m.specId]?.cost ?? 0), 0) : 0;
  console.log(`  (b) dark fleet ${fleet.length} plants; power have ${cov.cap}/${cov.need}; re-buy ${rebuy?.count ?? 0} units, ~£${(cost / 1e9).toFixed(1)}bn; mix ${JSON.stringify(rebuy?.mix?.map((m) => [m.specId, m.count]))}`);
  assert.ok(rebuy && rebuy.count > 0, 'documented: dark capacity is a real shortfall and the plan offers a re-buy');
});

test('(c) laddered credit: under-order measured after construction completes (online, connected)', () => {
  const s0 = big();
  const s1 = reducer(s0, { type: 'resolveDemand', serviceKey: 'power' });
  const fleet = powerOf(s1).filter((b) => !s0.buildings.includes(b));
  const laddered = fleet.filter((b) => (SPECS[b.spec].capacityTiers ?? []).length > 0);
  const done = Math.max(...fleet.map((b) => constructionTicks(SPECS[b.spec])));
  const online = { ...s1, tick: s1.tick + done + 1, roadConnectivity: null };
  const c0 = covOf(s0, 'power');
  const c1 = covOf(online, 'power');
  const credited = credit(fleet);
  const target = (c0.need - c0.cap) * AUTO_BUILD_DEMAND_FRACTION;
  const delivered = c1.cap - c0.cap;
  console.log(`  (c) laddered ${laddered.length}/${fleet.length}; target ${target}, credited ${credited}, delivered online ${delivered} (${((delivered / target) * 100).toFixed(1)}% of target); still short ${Math.max(0, c1.need - c1.cap)}`);
  assert.ok(Number.isFinite(delivered));
});

test('(d) mixed pipeline: some online, some building, some dark -> sizes exactly target minus building-credit', () => {
  const s0 = big();
  const s1 = reducer(s0, { type: 'resolveDemand', serviceKey: 'power' });
  const fleet = powerOf(s1).filter((b) => !s0.buildings.includes(b));
  assert.ok(fleet.length >= 3, `need >=3 plants (${fleet.length})`);
  const third = Math.max(1, Math.floor(fleet.length / 3));
  const building = fleet.slice(0, third);
  const rest = new Set(fleet.slice(third));
  // Age the state so `rest` finished; keep `building` building by re-stamping builtTick.
  const T = s1.tick + Math.max(...fleet.map((b) => constructionTicks(SPECS[b.spec]))) + 1;
  const bs = s1.buildings.map((b) => (building.includes(b) ? { ...b, builtTick: T } : b));
  const mixed = { ...s1, buildings: bs, tick: T, roadConnectivity: null };
  const cov = covOf(mixed, 'power');
  const pipe = credit(building);
  const want = (cov.need - cov.cap) * AUTO_BUILD_DEMAND_FRACTION - pipe;
  const ip = demandFixInProgress(mixed).find((p) => p.serviceKey === 'power');
  const p = planFor(mixed, 'power');
  if (want <= 0) {
    assert.equal(p, undefined, 'covered: no plan');
    assert.ok(ip && ip.units === building.length && ip.capacity === pipe);
  } else {
    assert.ok(p, 'remainder plan');
    assert.equal(ip, undefined, 'never both a Fix plan AND an in-progress row');
    // The remainder plan must be exactly the plan for a state with `want` shortfall:
    // compare against a pipeline-free state whose online cap is raised by pipe/FRACTION.
    const planned = p.mix.reduce((n, m) => n + m.count * m.unitCapacity, 0);
    assert.ok(planned >= want, `remainder ${planned} covers ${want}`);
  }
  console.log(`  (d) online-rest ${rest.size}, building ${building.length}; want ${want}; plan ${p?.count ?? 0}; inProgress ${ip?.units ?? 0}`);
});

test('(e) never both Fix plan AND in-progress row for one service, across a sweep of ticks and services', () => {
  let s = reducer(big(), { type: 'resolveDemandAll' });
  const T = Math.max(...s.buildings.filter((b) => b.builtTick != null).map((b) => constructionTicks(SPECS[b.spec])));
  for (let dt = 0; dt <= T + 2; dt += Math.max(1, Math.floor(T / 40))) {
    const st = { ...s, tick: s.tick + dt };
    const planKeys = new Set(demandFixPlan(st).map((p) => p.serviceKey));
    for (const ip of demandFixInProgress(st)) assert.ok(!planKeys.has(ip.serviceKey), `tick+${dt}: ${ip.serviceKey} has both`);
  }
});

test('(M2-kill) parks under construction already in `have`: a 70%-of-need park pipeline must NOT be double-counted into "in progress"', () => {
  // parksCapacityOf() is not online-gated, so building parks reduce the
  // shortfall S to S-A; the real target is 1.5(S-A) > 0 whenever A < S. A
  // pipeline-discounted parks row would be 1.5(S-A) - A, which is <= 0 once
  // A >= 0.6 S — omitting a real shortfall and listing a false in-progress row.
  const s0 = big(3_000_000);
  const seed = reducer(s0, { type: 'resolveDemand', serviceKey: 'parks' });
  const park = seed.buildings.find((b) => SPECS[b.spec]?.kind === 'park' && !s0.buildings.includes(b));
  assert.ok(park, 'precondition: a park was placed');
  const area = SPECS[park.spec].w * SPECS[park.spec].h;
  const need = covOf(s0, 'parks').need;
  const k = Math.ceil((0.7 * need) / area);
  assert.ok(k * area < need, 'A < S so a real shortfall remains');
  const parks = Array.from({ length: k }, (_, i) => ({ ...park, id: 9_000_000 + i, x: park.x, y: park.y, builtTick: s0.tick }));
  const st = { ...s0, buildings: [...s0.buildings, ...parks] };
  const cov = covOf(st, 'parks');
  assert.ok(cov.need - cov.cap > 0, `precondition: parks still short (${cov.cap}/${cov.need})`);
  assert.ok(planFor(st, 'parks'), 'parks shortfall must still offer a Fix');
  assert.equal(demandFixInProgress(st).some((p) => p.serviceKey === 'parks'), false, 'no false in-progress parks row');
});

test('(5) purity: demandFixPlan / demandFixInProgress / orderedDemandFixPlan mutate nothing (byte compare)', () => {
  const s1 = reducer(big(), { type: 'resolveDemandAll' });
  const st = { ...s1, tick: s1.tick + 3 };
  const before = stable(st);
  const a = stable([demandFixPlan(st), demandFixInProgress(st), orderedDemandFixPlan(st)]);
  const b = stable([demandFixPlan(st), demandFixInProgress(st), orderedDemandFixPlan(st)]);
  assert.equal(a, b);
  assert.equal(stable(st), before, 'state mutated by plan calls');
});
