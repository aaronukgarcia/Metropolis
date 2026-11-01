// attack-buildings-r1-round.test.mjs - FEAT-9995385590 destructive round r1
// (opus-round-r1-buildings). Regressions kept from the round over the 87
// former-placeholder buildings.
//
// F1 (money / GR#3): fiscal.ts KIND_TO_WAGE_SECTOR has no 'leisure' entry, so
// a leisure-kind spec that carries `jobs` counts toward totalJobs() (employment)
// but lands in no wage sector and pays ZERO wages - the exact BUG-652 near-miss
// the map's own doc comment warns about. The round found eight such specs
// (lei_gym, lei_sportsground, lei_stables, stay_bb, stay_hotel, stay_luxury,
// stay_resort, stay_caravan; 856 wage-free jobs).
// F2 (honesty): land_containerport is in FREIGHT_BOOST_SPECS, but its blurb
// still says no freight boost is modelled.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SPECS, totalJobs } from '../src/sim/data.ts';
import { KIND_TO_WAGE_SECTOR } from '../src/sim/fiscal.ts';
import { computeFlows, initialState, FREIGHT_BOOST_SPECS } from '../src/sim/engine.ts';

test('F1: every spec that carries jobs has a kind that maps to a wage sector (no wage-free employment)', () => {
  const wageFree = Object.values(SPECS)
    .filter((sp) => !sp.placeholder && (sp.jobs ?? 0) > 0 && KIND_TO_WAGE_SECTOR[sp.kind] == null)
    .map((sp) => `${sp.id}(${sp.kind}, ${sp.jobs} jobs)`);
  assert.deepEqual(wageFree, [], `jobs counted toward employment but paid no wages: ${wageFree.join(', ')}`);
});

test('F1: placing a staffed hotel adds both jobs AND a wage bill', () => {
  const OFF = 300;
  const mk = (buildings) => ({ ...initialState(), unlockedAll: true, buildings, nextId: 999, population: 50000, tick: 5000 });
  const wages = (s) => computeFlows(s).outflows.find((f) => f.label === 'Wages')?.value ?? 0;
  const s0 = mk([]);
  const s1 = mk([{ id: 1, spec: 'stay_resort', x: OFF, y: OFF }]);
  assert.ok(totalJobs(s1) - totalJobs(s0) > 0, 'precondition: the resort adds jobs');
  assert.ok(wages(s1) > wages(s0), `the resort's ${SPECS.stay_resort.jobs} jobs must carry wages (was ${wages(s0)} -> ${wages(s1)})`);
});

test('F2: a spec that grants the freight boost does not tell the player it has none', () => {
  for (const id of FREIGHT_BOOST_SPECS) {
    const blurb = SPECS[id]?.blurb ?? '';
    assert.doesNotMatch(blurb, /no [^.]*freight boost/i, `${id}: blurb "${blurb}" denies a boost the engine grants`);
  }
});
