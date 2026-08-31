// ATTACK 4: Tier-rule edge cases
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import { SPECS } from '../src/sim/data.ts';

function board(buildings) {
  const base = initialState();
  let maxId = 0;
  for (const b of buildings) if (b.id > maxId) maxId = b.id;
  return { ...base, unlockedAll: true, buildings, nextId: maxId + 1 };
}

test('ATTACK 4a: dual carriageway (tier 4) crossing street (tier 1) → roundabout (tier 2)?', () => {
  let s = board([]);

  // Lay a street (tier 1)
  const street = [{ x: 10, y: 12 }];
  s = reducer(s, { type: 'placeRoadPath', spec: 'road', tiles: street });

  // Lay a dual carriageway (tier 4) crossing it
  const dual = [
    { x: 10, y: 11 },
    { x: 10, y: 12 }, // crosses street
    { x: 10, y: 13 },
  ];
  s = reducer(s, { type: 'placeRoadPath', spec: 'rd_dual', tiles: dual });

  // Find junction at (10,12)
  const junctions = s.buildings.filter((b) => b.spec === 'rd_roundabout' || b.spec === 'rd_junction');
  assert.equal(junctions.length, 1, 'exactly one junction created');

  const junction = junctions[0];
  const junctionSpec = SPECS[junction.spec];

  console.log(`Tier-rule result:`);
  console.log(`  new road: rd_dual (tier 4)`);
  console.log(`  existing: street (tier 1)`);
  console.log(`  max tier: ${Math.max(4, 1)} = 4`);
  console.log(`  junction spec selected: ${junction.spec} (tier ${junctionSpec.roadTier})`);
  console.log(`  expected per rule: tier >= 2 → roundabout`);

  assert.equal(junction.spec, 'rd_roundabout', 'junction should be roundabout (tier >= 2)');
  assert.equal(junctionSpec.roadTier, 2, 'roundabout has tier 2');

  console.log(`✓ ATTACK 4a: tier-rule correctly picks roundabout for tier 4 × 1 crossing`);
});

test('ATTACK 4b: avenue (tier 2) crossing street (tier 1) → roundabout (tier 2)', () => {
  let s = board([]);

  const street = [{ x: 10, y: 12 }];
  s = reducer(s, { type: 'placeRoadPath', spec: 'road', tiles: street });

  const avenue = [
    { x: 10, y: 11 },
    { x: 10, y: 12 }, // crosses
    { x: 10, y: 13 },
  ];
  s = reducer(s, { type: 'placeRoadPath', spec: 'rd_avenue', tiles: avenue });

  const junctions = s.buildings.filter((b) => b.spec === 'rd_roundabout' || b.spec === 'rd_junction');
  assert.equal(junctions.length, 1, 'exactly one junction');
  assert.equal(junctions[0].spec, 'rd_roundabout', 'should be roundabout');

  console.log(`✓ ATTACK 4b: avenue (tier 2) × street (tier 1) → roundabout`);
});

test('ATTACK 4c FIXED: street (tier 1) crossing street (tier 1) → deduped (no junction)', () => {
  let s = board([]);

  const street1 = [{ x: 10, y: 12 }];
  s = reducer(s, { type: 'placeRoadPath', spec: 'road', tiles: street1 });

  const street2 = [
    { x: 10, y: 11 },
    { x: 10, y: 12 },
    { x: 10, y: 13 },
  ];
  s = reducer(s, { type: 'placeRoadPath', spec: 'road', tiles: street2 });

  const junctions = s.buildings.filter((b) => b.spec === 'rd_junction');
  const roundabouts = s.buildings.filter((b) => b.spec === 'rd_roundabout');

  // FIXED: same-spec overlap (street over street) is deduped, no junction
  assert.equal(junctions.length, 0, 'no plain junctions (same-spec dedup)');
  assert.equal(roundabouts.length, 0, 'no roundabouts');

  // ONE-BUILDING-PER-TILE: crossing tile has exactly 1 building (the original street)
  const buildingsAtCrossing = s.buildings.filter((b) => b.x === 10 && b.y === 12);
  assert.equal(buildingsAtCrossing.length, 1, 'crossing tile has exactly 1 building');
  assert.equal(buildingsAtCrossing[0].spec, 'road', 'crossing tile is still a plain road (deduped)');

  console.log(`✓ ATTACK 4c FIXED: street × street deduped, no junction, one-building-per-tile preserved`);
});

test('ATTACK 4d: Check SPECS tier values match AC spec', () => {
  console.log(`\nSPECS tier values:`);
  console.log(`  road: tier ${SPECS.road.roadTier}`);
  console.log(`  rd_avenue: tier ${SPECS.rd_avenue.roadTier}`);
  console.log(`  rd_aroad: tier ${SPECS.rd_aroad.roadTier}`);
  console.log(`  rd_dual: tier ${SPECS.rd_dual.roadTier}`);
  console.log(`  rd_junction: tier ${SPECS.rd_junction.roadTier}`);
  console.log(`  rd_roundabout: tier ${SPECS.rd_roundabout.roadTier}`);

  assert.equal(SPECS.road.roadTier, 1, 'street tier = 1');
  assert.equal(SPECS.rd_avenue.roadTier, 2, 'avenue tier = 2');
  assert.equal(SPECS.rd_aroad.roadTier, 3, 'aroad tier = 3');
  assert.equal(SPECS.rd_dual.roadTier, 4, 'dual tier = 4');
  assert.equal(SPECS.rd_junction.roadTier, 1, 'junction tier = 1');
  assert.equal(SPECS.rd_roundabout.roadTier, 2, 'roundabout tier = 2');

  console.log(`✓ ATTACK 4d: all tier values confirmed`);
});
