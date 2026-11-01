// buildings-tranche2.test.mjs — FEAT-9995385590: the 12 "tranche 2" roadmap
// placeholders graduate to REAL placeable buildings. Name/footprint are the
// original placeholder values; stats are PLACEHOLDER BALANCE (Aaron's pass).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SPECS, isPlaceable } from '../src/sim/data.ts';
import { initialState, reducer } from '../src/sim/engine.ts';

const EXPECT = {
  pow_hvdc: ['HVDC Interconnector', 2, 2],
  pow_reprocess: ['THORP Reprocessing Plant', 4, 4],
  rail_branch: ['Branch Line', 1, 1],
  trans_parkride: ['Park & Ride', 2, 2],
  trans_interchange: ['Interchange Hub', 2, 2],
  rail_freightyard: ['Freight Yard', 3, 2],
  ev_charging_hub: ['EV Charging Hub', 1, 1],
  land_cern: ['Particle Accelerator', 5, 5],
  land_containerport: ['Container Port', 4, 4],
  land_ferryterminal: ['Ferry Terminal', 3, 3],
  land_gigafactory: ['Gigafactory', 5, 5],
  land_semifab: ['Semiconductor Fab', 4, 4],
};

function rich() {
  return { ...initialState(), unlockedAll: true, funds: 1e12 };
}

test('all 12 are real: exist, not placeholder, placeable, cost > 0, name/footprint unchanged', () => {
  const s = rich();
  for (const [id, [name, w, h]] of Object.entries(EXPECT)) {
    const sp = SPECS[id];
    assert.ok(sp, `${id} missing`);
    assert.notEqual(sp.placeholder, true, `${id} still placeholder`);
    assert.equal(isPlaceable(s, sp), true, `${id} not placeable`);
    assert.ok(sp.cost > 0, `${id} cost`);
    assert.equal(sp.name, name);
    assert.equal(sp.w, w);
    assert.equal(sp.h, h);
  }
});

function finite(v, path) {
  if (typeof v === 'number') assert.ok(Number.isFinite(v), `non-finite at ${path}`);
  else if (Array.isArray(v)) v.forEach((x, i) => finite(x, `${path}[${i}]`));
  else if (v && typeof v === 'object') for (const k of Object.keys(v)) finite(v[k], `${path}.${k}`);
}

function smoke(ids, at) {
  let s = rich();
  let n = 0;
  for (const id of ids) {
    const before = s.buildings.length;
    for (let dy = 0; dy < 40 && s.buildings.length === before; dy += 7) {
      for (let dx = 0; dx < 120 && s.buildings.length === before; dx += 9) {
        s = reducer(s, { type: 'place', spec: id, x: at.x + dx, y: at.y + dy });
      }
    }
    if (s.buildings.length > before) n++;
  }
  assert.equal(n, ids.length, `placed ${n}/${ids.length} of ${ids.join(',')}`);
  for (let i = 0; i < 6; i++) s = reducer(s, { type: 'tick' });
  assert.ok(Number.isFinite(s.funds), 'funds finite');
  finite(s.lastFlows, 'lastFlows');
  return s;
}

test('smoke: power group places and ticks without NaN', () => {
  smoke(['pow_hvdc', 'pow_reprocess'], { x: 20, y: 20 });
});

test('smoke: transport group places and ticks without NaN', () => {
  smoke(['rail_branch', 'trans_parkride', 'trans_interchange', 'rail_freightyard', 'ev_charging_hub'], { x: 20, y: 60 });
});

test('smoke: mega-facility group places and ticks without NaN', () => {
  smoke(['land_cern', 'land_containerport', 'land_ferryterminal', 'land_gigafactory', 'land_semifab'], { x: 20, y: 100 });
});
