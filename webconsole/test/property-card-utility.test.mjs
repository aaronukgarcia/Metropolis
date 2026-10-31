// property-card-utility.test.mjs — FEAT-2326609841 AC-6 (the pack's load-bearing
// gate): the per-building water / waste-water share helper (buildingUtility.ts)
// must sum EXACTLY to the citywide waterDemandOf(s) — clean AND waste — so the
// card can never become a second water model (GR#3).
//
// Run: node ../tools/test/scoped.mjs test/property-card-utility.test.mjs
// Red-proof: see the mutation table in the lane report (scratch copy + md5).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState } from '../src/sim/engine.ts';
import { computeRoadConnectivity, waterDemandOf, isOnline, SPECS } from '../src/sim/data.ts';
import { utilitySharesOf, buildingWaterShareOf, buildingPowerOf } from '../src/sim/buildingUtility.ts';

let _id = 2326609841000;
const B = (spec, x, y, extra = {}) => ({ id: _id++, spec, x, y: SPECS[spec].category === 'zones' && SPECS[spec].kind === 'residential' ? 10 - SPECS[spec].h : y, builtTick: 0, ...extra });

function city(buildings, tick, population) {
  const st = { ...initialState(), buildings, tick, population };
  st.roadConnectivity = computeRoadConnectivity(st);
  return st;
}

function roads() {
  return Array.from({ length: 24 }, (_, i) => B('road', i, 10));
}

function fixture(population, { reverse = false } = {}) {
  _id = 2326609841000; // fixed ids so the order test compares like for like
  const homes = [
    B('res_hut', 1, 8),
    B('res_block', 3, 8),
    B('res_lowrise', 6, 8),
    B('res_midrise', 9, 8, { capacityTier: 1 }),
    B('res_highrise', 12, 8),
    B('res_terrace', 16, 8),
    B('res_block', 19, 8),
  ];
  const all = [...roads(), ...homes];
  if (reverse) all.reverse();
  return city(all, 5000, population);
}

function sumShares(s) {
  const sh = utilitySharesOf(s);
  let clean = 0;
  let waste = 0;
  for (const v of sh.byId.values()) {
    clean += v.clean;
    waste += v.waste;
  }
  return { clean, waste, unallocated: sh.unallocated };
}

for (const pop of [1, 7, 1234, 98765, 1000003]) {
  test(`AC-6 identity: sum of per-building shares + unallocated === waterDemandOf (pop ${pop}), clean and waste`, () => {
    const s = fixture(pop);
    const d = waterDemandOf(s);
    assert.equal(d.clean, pop, 'fixture sanity: demand model is population-driven');
    const { clean, waste, unallocated } = sumShares(s);
    assert.equal(clean + unallocated.clean, d.clean);
    assert.equal(waste + unallocated.waste, d.waste);
    // With online housing present nothing is left unattributed.
    assert.equal(unallocated.clean, 0);
    assert.equal(unallocated.waste, 0);
  });
}

test('AC-6 identity holds independent of building order (GR#21)', () => {
  const a = utilitySharesOf(fixture(98765));
  const b = utilitySharesOf(fixture(98765, { reverse: true }));
  assert.deepEqual([...a.byId.entries()].sort((x, y) => x[0] - y[0]), [...b.byId.entries()].sort((x, y) => x[0] - y[0]));
});

test('AC-6: an under-construction home draws no share; its demand lands on the online homes', () => {
  const s = fixture(5000);
  const homes = s.buildings.filter((b) => SPECS[b.spec].kind === 'residential');
  const pending = { ...homes[0], builtTick: s.tick - 1 };
  const s2 = city(s.buildings.map((b) => (b.id === pending.id ? pending : b)), s.tick, 5000);
  assert.equal(isOnline(s2, pending), false, 'sanity: pending home is offline');
  assert.equal(buildingWaterShareOf(s2, pending), null);
  const { clean, waste, unallocated } = sumShares(s2);
  assert.equal(clean + unallocated.clean, waterDemandOf(s2).clean);
  assert.equal(waste + unallocated.waste, waterDemandOf(s2).waste);
});

test('AC-6: population with no online housing is reported unallocated, never invented onto a building', () => {
  const s = city(roads(), 5000, 321);
  const sh = utilitySharesOf(s);
  assert.equal(sh.byId.size, 0);
  assert.equal(sh.unallocated.clean, waterDemandOf(s).clean);
  assert.equal(sh.unallocated.waste, waterDemandOf(s).waste);
});

test('AC-6: a non-housing building has no water share (null -> "—", not a measured zero)', () => {
  const shop = B('com_shop', 3, 8);
  const s = city([...roads(), shop, B('res_block', 6, 8)], 5000, 100);
  assert.equal(buildingWaterShareOf(s, shop), null);
});

test('AC-6: proportionality - larger homes carry larger shares (same helper, no second constant)', () => {
  const s = fixture(98765);
  const sh = utilitySharesOf(s).byId;
  const hut = s.buildings.find((b) => b.spec === 'res_hut');
  const hi = s.buildings.find((b) => b.spec === 'res_highrise');
  assert.ok(sh.get(hi.id).clean > sh.get(hut.id).clean * 10);
});

test('AC-6 power: plants report generation, consumers draw, spec without mw is null', () => {
  assert.deepEqual(buildingPowerOf(B('pow_wind', 0, 0)), { kind: 'generation', mw: 6 });
  assert.equal(buildingPowerOf(B('road', 0, 0)), null);
});

test('AC-9: the share map is computed once per state object (memoised)', () => {
  const s = fixture(1234);
  assert.equal(utilitySharesOf(s), utilitySharesOf(s));
});
