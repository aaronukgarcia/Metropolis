// buildings-hooks.test.mjs — FEAT-9995385590: two functional hooks for buildings that
// stopped being placeholders.
//   (a) land_containerport earns the same x1.4 Freight Tax boost as land_harbour
//       (engine.ts FREIGHT_BOOST_SPECS, read by computeFlows).
//   (b) air_heliport (Air-Ambulance Pad) is an ambulance dispatch point alongside
//       hea_ambulance (emergencyResponse.ts AMBULANCE_STATION_SPEC_IDS), while the
//       other health specs (hospital, clinic) stay decoys that are NOT dispatch points.
//
// RED proof: revert FREIGHT_BOOST_SPECS to {'land_harbour'} or AMBULANCE_STATION_SPEC_IDS to
// {'hea_ambulance'} and (a)/(b) fail.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { computeFlows, initialState, FREIGHT_BOOST_SPECS } from '../src/sim/engine.ts';
import { emergencyIsochroneOf } from '../src/sim/emergencyResponse.ts';
import { optimalProvider } from '../src/sim/data.ts';

const OFFSET = 300;
const rd = (id, spec, x, y) => ({ id, spec, x: x + OFFSET, y: y + OFFSET, builtTick: 0 });
// No builtTick: the same shape emergencyResponse.test.mjs uses, so isOnline reads the building as built.
const bldg = (id, spec, x, y) => ({ id, spec, x: x + OFFSET, y: y + OFFSET });
function board(buildings, extra = {}) {
  let maxId = 0;
  for (const b of buildings) if (b.id > maxId) maxId = b.id;
  return { ...initialState(), unlockedAll: true, buildings, nextId: maxId + 1, roadNotice: null, population: 50000, ...extra };
}

test('(a) the freight boost set is exactly the harbour and the container port', () => {
  assert.deepEqual([...FREIGHT_BOOST_SPECS].sort(), ['land_containerport', 'land_harbour']);
});

const freight = (r) => r.inflows.find((f) => f.label === 'Freight Tax')?.value ?? 0;
/** A city with real industrial stock so the Freight Tax line is non-zero. */
function freightCity(extra, tick = 5000) {
  const ind = [0, 1, 2, 3].map((i) => bldg(100 + i, 'ind_heavy', 20 + i * 4, 20));
  return board([...ind, ...extra], { tick });
}

test('(a) an ONLINE container port grants the same x1.4 Freight Tax boost as the harbour; an under-construction one grants nothing', () => {
  const none = freight(computeFlows(freightCity([])));
  assert.ok(none > 0, 'precondition: the control city pays Freight Tax');
  const harbour = freight(computeFlows(freightCity([bldg(1, 'land_harbour', 0, 0)])));
  const port = freight(computeFlows(freightCity([bldg(1, 'land_containerport', 0, 0)])));
  const both = freight(computeFlows(freightCity([bldg(1, 'land_harbour', 0, 0), bldg(2, 'land_containerport', 10, 0)])));
  assert.equal(harbour, Math.round(none * 1.4), 'harbour boost (the tax line is rounded to whole pounds)');
  assert.equal(port, harbour, 'the container port must grant exactly the harbour boost');
  assert.equal(both, harbour, 'one boost, never stacked');
  const building = freight(computeFlows(freightCity([{ id: 1, spec: 'land_containerport', x: OFFSET, y: OFFSET, builtTick: 5000 }])));
  assert.equal(building, none, 'an under-construction port grants nothing yet (BUG-569 gate)');
});

test('(b) an air_heliport is an ambulance dispatch source; a hospital decoy is not', () => {
  const road = rd(1, 'rd_aroad', 0, 0);
  const withPad = emergencyIsochroneOf(board([road, bldg(2, 'air_heliport', 0, 0)]), 'ambulance');
  assert.equal(withPad.size, 1, 'exactly one ambulance source segment from the air-ambulance pad');
  const withHospital = emergencyIsochroneOf(board([road, bldg(2, 'hea_hospital', 0, 0)]), 'ambulance');
  assert.equal(withHospital.size, 0, 'a hospital (same kind, wrong id) is still not a dispatch point');
  const withStation = emergencyIsochroneOf(board([road, bldg(2, 'hea_ambulance', 0, 0)]), 'ambulance');
  assert.equal(withStation.size, 1, 'the road ambulance station still counts');
  // The pad does not leak into the other services.
  assert.equal(emergencyIsochroneOf(board([road, bldg(2, 'air_heliport', 0, 0)]), 'fire').size, 0);
});

test('(c) Demand Fix never picks a heli base on cost (they enter the fire/police providers by kind but cost more per person)', () => {
  const s = { ...initialState(), unlockedAll: true };
  for (const svc of ['fire', 'police']) {
    for (const budget of [1e6, 1e7, 5e7, 1e8, 1e9, 1e10, 1e12]) {
      for (const shortfall of [1000, 10000, 40000, 100000, 1e6, 5e6, 5e7]) {
        const id = optimalProvider(s, svc, budget, shortfall)?.id;
        assert.ok(id && !id.startsWith('air_'), `${svc} budget=${budget} shortfall=${shortfall} picked ${id}`);
      }
    }
  }
});

// F3 (round r1, FEAT-9995385590): pow_hvdc is an interconnector, not a power-station
// footprint - capped at 2 per city and never a Demand Fix power candidate.
import { SPECS, largestFirstFill } from '../src/sim/data.ts';

test('(c) pow_hvdc is capped at 2 per city and the Demand Fix never builds it', () => {
  assert.equal(SPECS.pow_hvdc.maxPerCity, 2);
  const plan = largestFirstFill(board([]), 'power', 50000);
  assert.ok(plan.length > 0, 'precondition: a big power shortfall yields a plan');
  assert.ok(!plan.some((m) => m.specId === 'pow_hvdc'), `plan used pow_hvdc: ${JSON.stringify(plan).slice(0, 300)}`);
});
