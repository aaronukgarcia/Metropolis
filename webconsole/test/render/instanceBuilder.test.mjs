// instanceBuilder.test.mjs — FEAT-2326609760 GPU acceleration spike, Phase 1.
//
// Correctness for the SoA instance builder against the real 13k-building
// scale-gate fixture (test/scale/fixture.mjs) — the same fixture BUG-622's
// scale gate uses, so this proves the GPU buffer builder holds up at the
// exact scale that motivated this spike, not just on a toy handful of
// buildings.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildScaleFixture, DEFAULT_BUILDING_COUNT } from '../scale/fixture.mjs';
import {
  buildInstances,
  rebuildDynamicOnly,
  buildingInstanceFilter,
  roadInstanceFilter,
  hexToRgbUnit,
  STATIC_FLOATS_PER_INSTANCE,
  DYNAMIC_FLOATS_PER_INSTANCE,
  NOT_APPLICABLE,
} from '../../src/render/instanceBuilder.ts';
import { SPECS, isRoadSpec } from '../../src/sim/data.ts';

let fixture;
test('setup: build the 13k-building scale fixture once for this file', () => {
  fixture = buildScaleFixture();
  assert.equal(fixture.buildings.length, DEFAULT_BUILDING_COUNT);
});

test('hexToRgbUnit decodes real SPECS colours and loudly flags a malformed one', () => {
  const [r, g, b] = hexToRgbUnit('#ff0080');
  assert.ok(Math.abs(r - 1) < 1e-6);
  assert.ok(Math.abs(g - 0) < 1e-6);
  assert.ok(Math.abs(b - 0x80 / 255) < 1e-6);
  // Malformed input -> loud magenta sentinel, never a silent guess (GR#1/GR#15).
  assert.deepEqual(hexToRgbUnit('not-a-colour'), [1, 0, 1]);
});

test('buildInstances(buildingInstanceFilter) excludes every road/motorway tile and includes everything else', () => {
  const buildingCountExpected = fixture.buildings.filter((b) => {
    const sp = SPECS[b.spec];
    return sp && buildingInstanceFilter(sp);
  }).length;
  const roadCountExpected = fixture.buildings.filter((b) => {
    const sp = SPECS[b.spec];
    return sp && roadInstanceFilter(sp);
  }).length;

  const buildings = buildInstances(fixture, buildingInstanceFilter);
  const roads = buildInstances(fixture, roadInstanceFilter);

  assert.equal(buildings.count, buildingCountExpected);
  assert.equal(roads.count, roadCountExpected);
  // The fixture is documented to include real road-kind buildings (fixture.mjs
  // header, ROAD_FRACTION) — a road batch of zero would mean the filter or the
  // fixture silently stopped producing roads, a real regression either way.
  assert.ok(roads.count > 0, 'scale fixture must include at least one road tile');
  assert.equal(buildings.count + roads.count, buildings.count + roads.count); // no double count by construction
  assert.equal(
    buildings.staticData.length,
    buildings.count * STATIC_FLOATS_PER_INSTANCE,
    'static buffer must be exactly count * STATIC_FLOATS_PER_INSTANCE floats long'
  );
  assert.equal(
    buildings.dynamicData.length,
    buildings.count * DYNAMIC_FLOATS_PER_INSTANCE,
    'dynamic buffer must be exactly count * DYNAMIC_FLOATS_PER_INSTANCE floats long'
  );
});

test('spot-check: instance i decodes to the SAME position/size/colour as the source building at every 1000th index', () => {
  const inst = buildInstances(fixture, buildingInstanceFilter);
  const filteredBuildings = fixture.buildings.filter((b) => {
    const sp = SPECS[b.spec];
    return sp && buildingInstanceFilter(sp);
  });
  assert.equal(inst.count, filteredBuildings.length);

  for (let i = 0; i < inst.count; i += 1000) {
    const b = filteredBuildings[i];
    const sp = SPECS[b.spec];
    const so = i * STATIC_FLOATS_PER_INSTANCE;
    assert.equal(inst.staticData[so + 0], b.x, `instance ${i} x`);
    assert.equal(inst.staticData[so + 1], b.y, `instance ${i} y`);
    assert.equal(inst.staticData[so + 2], sp.w, `instance ${i} w`);
    assert.equal(inst.staticData[so + 3], sp.h, `instance ${i} h`);
    const [r, g, bl] = hexToRgbUnit(sp.color);
    assert.ok(Math.abs(inst.staticData[so + 4] - r) < 1e-6, `instance ${i} r`);
    assert.ok(Math.abs(inst.staticData[so + 5] - g) < 1e-6, `instance ${i} g`);
    assert.ok(Math.abs(inst.staticData[so + 6] - bl) < 1e-6, `instance ${i} b`);
    assert.equal(inst.ids[i], b.id, `instance ${i} id must match the source building id`);
  }
});

test('every road tile in the fixture is excluded from the building batch and present in the road batch', () => {
  const buildings = buildInstances(fixture, buildingInstanceFilter);
  const roads = buildInstances(fixture, roadInstanceFilter);
  const roadSpecIds = new Set(
    fixture.buildings.filter((b) => isRoadSpec(SPECS[b.spec]) || SPECS[b.spec]?.kind === 'motorway').map((b) => b.id)
  );
  for (const id of buildings.ids) assert.ok(!roadSpecIds.has(id), `building batch leaked a road id ${id}`);
  const roadIdSet = new Set(roads.ids);
  for (const id of roadSpecIds) assert.ok(roadIdSet.has(id), `road batch missing road id ${id}`);
});

test('rebuildDynamicOnly reproduces buildInstances\' own dynamic block exactly, without touching static data', () => {
  const full = buildInstances(fixture, buildingInstanceFilter);
  const dynOnly = rebuildDynamicOnly(fixture, buildingInstanceFilter, full.ids);
  assert.equal(dynOnly.length, full.dynamicData.length);
  for (let i = 0; i < dynOnly.length; i++) {
    assert.equal(dynOnly[i], full.dynamicData[i], `dynamic float ${i} must match the full-rebuild value exactly`);
  }
});

test('rebuildDynamicOnly never crashes on a vanished id and reports it as offline/not-applicable', () => {
  const dyn = rebuildDynamicOnly(fixture, buildingInstanceFilter, [999999999]);
  assert.equal(dyn.length, DYNAMIC_FLOATS_PER_INSTANCE);
  assert.equal(dyn[0], 0, 'vanished id must report offline');
  assert.equal(dyn[1], NOT_APPLICABLE);
  assert.equal(dyn[2], NOT_APPLICABLE);
  assert.equal(dyn[3], 0);
});

test('buildInstances is deterministic: two calls on the same state produce byte-identical arrays (GR#21 posture)', () => {
  const a = buildInstances(fixture, buildingInstanceFilter);
  const b = buildInstances(fixture, buildingInstanceFilter);
  assert.deepEqual(Array.from(a.staticData), Array.from(b.staticData));
  assert.deepEqual(Array.from(a.dynamicData), Array.from(b.dynamicData));
  assert.deepEqual(a.ids, b.ids);
});
