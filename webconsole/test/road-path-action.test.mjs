// FEAT-1972079910 inc1: placeRoadPath action tests.
// Tests AC-3 (atomic journal action), AC-4 (all-or-nothing funds), via the reducer.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import { recordAction, emptyJournal, isStateAffecting } from '../src/sim/journal.ts';

// Build a clean board for testing.
function board(buildings) {
  const base = initialState();
  let maxId = 0;
  for (const b of buildings) if (b.id > maxId) maxId = b.id;
  return { ...base, unlockedAll: true, buildings, nextId: maxId + 1 };
}

// Test AC-3: one drag commits a single journal entry carrying the full path.
test('AC-3 RED proof: placeRoadPath is a single atomic journal entry', () => {
  const s = board([]);
  const tiles = [
    { x: 10, y: 10 },
    { x: 11, y: 10 },
    { x: 12, y: 10 },
  ];

  const action = { type: 'placeRoadPath', spec: 'road', tiles };

  // Verify it's state-affecting.
  assert.equal(isStateAffecting(action), true, 'placeRoadPath is state-affecting');

  // Record it in the journal.
  const j1 = recordAction(emptyJournal(), 0, action);
  assert.equal(j1.entries.length, 1, 'exactly one journal entry');
  assert.deepEqual(j1.entries[0].action, action, 'journal entry carries the full path');

  // Apply to the reducer.
  const after = reducer(s, action);

  // All tiles should be placed as buildings.
  assert.equal(
    after.buildings.filter((b) => b.spec === 'road').length,
    tiles.length,
    `all ${tiles.length} road tiles placed`
  );

  console.log('✓ AC-3: single placeRoadPath action, single journal entry');
});

// Test AC-4 RED proof: funds = total−1 → zero tiles placed, zero ledger movement.
test('AC-4 RED proof: insufficient funds blocks all placement', () => {
  const tiles = [
    { x: 10, y: 10 },
    { x: 11, y: 10 },
  ];

  // First discover the actual cost with a fresh board and ample funds.
  const ampleBoard = board([]);
  const ampleAction = { type: 'placeRoadPath', spec: 'road', tiles };
  const afterAmple = reducer({ ...ampleBoard, funds: 1000000 }, ampleAction);
  const actualCostPerTile = (1000000 - afterAmple.funds) / tiles.length;
  const totalCost = actualCostPerTile * tiles.length;

  // Now test with insufficient funds (total - 1).
  const testBoard = board([]);
  const testState = { ...testBoard, funds: totalCost - 1 };

  const action = { type: 'placeRoadPath', spec: 'road', tiles };
  const after = reducer(testState, action);

  // No tiles should be placed.
  const roadTiles = after.buildings.filter((b) => b.spec === 'road');
  assert.equal(roadTiles.length, 0, 'zero tiles placed when funds insufficient');

  // Funds should not change.
  assert.equal(after.funds, testState.funds, 'funds unchanged (no partial spend)');

  // A notice should be set.
  assert.ok(after.placeNotice, 'placeNotice set');
  assert.match(after.placeNotice, /Insufficient funds/i, 'notice mentions insufficient funds');

  console.log('✓ AC-4: insufficient funds blocks all placement, no ledger movement');
});

// Test AC-4 continued: funds exactly equal to total → all placed.
test('AC-4 edge case: funds exactly equal to total cost → place all', () => {
  const tiles = [
    { x: 10, y: 10 },
    { x: 11, y: 10 },
  ];

  // First discover the actual cost with a fresh board.
  const ampleBoard = board([]);
  const action = { type: 'placeRoadPath', spec: 'road', tiles };
  const afterAmple = reducer({ ...ampleBoard, funds: 1000000 }, action);
  const actualCostPerTile = (1000000 - afterAmple.funds) / tiles.length;
  const totalCost = actualCostPerTile * tiles.length;

  // Set funds to exactly the cost on a fresh board.
  const testBoard = board([]);
  const testState = { ...testBoard, funds: totalCost };
  const after = reducer(testState, action);

  // All tiles should be placed.
  const roadTiles = after.buildings.filter((b) => b.spec === 'road');
  assert.equal(roadTiles.length, tiles.length, 'all tiles placed at exact cost');

  // Funds should be zero.
  assert.equal(after.funds, 0, 'funds reduced to zero');

  console.log('✓ AC-4: exact cost funds allow placement');
});

// Test AC-4 with affordability: ample funds → all placed and cost deducted.
test('AC-4 affordability: ample funds → all placed, cost deducted', () => {
  const s = board([]);
  const tiles = [
    { x: 10, y: 10 },
    { x: 11, y: 10 },
    { x: 12, y: 10 },
  ];

  const testState = { ...s, funds: 100000 }; // ample
  const action = { type: 'placeRoadPath', spec: 'road', tiles };
  const before = testState.funds;
  const after = reducer(testState, action);

  // All tiles should be placed.
  assert.equal(
    after.buildings.filter((b) => b.spec === 'road').length,
    tiles.length,
    'all tiles placed'
  );

  // Funds should be reduced by the cost.
  assert.ok(after.funds < before, 'funds deducted');

  // Ledger should have a "Laid X road tiles" entry.
  const lastEvent = after.ledger[after.ledger.length - 1];
  assert.match(lastEvent.label, /Laid.*road/i, 'ledger records road placement');
  assert.ok(lastEvent.amount < 0, 'ledger entry is negative (cost)');

  console.log('✓ AC-4: ample funds, all placed, cost deducted');
});

// Test AC-9 regression: single-tile "drag" (one-element path) still works.
test('AC-9 regression: single-tile placeRoadPath (one-element path)', () => {
  const s = board([]);
  const tiles = [{ x: 10, y: 10 }];
  const action = { type: 'placeRoadPath', spec: 'road', tiles };
  const after = reducer(s, action);

  const roads = after.buildings.filter((b) => b.spec === 'road');
  assert.equal(roads.length, 1, 'single tile placed');
  assert.equal(roads[0].x, 10);
  assert.equal(roads[0].y, 10);

  console.log('✓ AC-9: single-tile path places exactly one tile');
});

// Test XP grant: 4 per tile, matching place action.
test('XP grant: 4 per tile', () => {
  const s = board([]);
  const tiles = [
    { x: 10, y: 10 },
    { x: 11, y: 10 },
    { x: 12, y: 10 },
  ];

  const before = s.xp;
  const after = reducer(s, { type: 'placeRoadPath', spec: 'road', tiles });

  const xpGain = after.xp - before;
  assert.equal(xpGain, tiles.length * 4, `XP gained = ${tiles.length} tiles × 4`);

  console.log('✓ XP grant: correct (4 per tile)');
});

// Test replay: the same action replayed produces identical results.
test('AC-10 replay: same action replayed produces identical state', () => {
  const s1 = board([]);
  const s2 = board([]);
  const action = {
    type: 'placeRoadPath',
    spec: 'road',
    tiles: [
      { x: 10, y: 10 },
      { x: 11, y: 10 },
      { x: 12, y: 10 },
    ],
  };

  const after1 = reducer(s1, action);
  const after2 = reducer(s2, action);

  // Both should have the same number of buildings.
  assert.equal(after1.buildings.length, after2.buildings.length, 'same building count');

  // Both should have the same funds.
  assert.equal(after1.funds, after2.funds, 'same funds');

  // Both should have the same XP.
  assert.equal(after1.xp, after2.xp, 'same XP');

  console.log('✓ AC-10: replay determinism confirmed');
});

// Test out-of-bounds rejection: no tiles placed if any are out of bounds.
test('Bounds check: out-of-bounds tiles rejected (all-or-nothing)', () => {
  const s = board([]);
  const tiles = [
    { x: 10, y: 10 },
    { x: -1, y: 10 }, // out of bounds
  ];

  const before = s.funds;
  const after = reducer(s, { type: 'placeRoadPath', spec: 'road', tiles });

  // No tiles should be placed (all-or-nothing).
  assert.equal(after.buildings.filter((b) => b.spec === 'road').length, 0, 'no tiles placed');

  // Funds unchanged.
  assert.equal(after.funds, before, 'funds unchanged');

  console.log('✓ Bounds check: out-of-bounds rejects all tiles');
});

// ════════════════════════════════════════════════════════════════════════════
// AC-6: Auto-junctions where the new road crosses existing roads
// CORRECTED: convert existing road building in place, one-building-per-tile
// ════════════════════════════════════════════════════════════════════════════

// AC-6a RED proof: avenue crossing street → junction conversion (different tiers).
test('AC-6a RED proof: avenue crossing street → junction conversion, count=1 at tile', () => {
  // Lay street A (tier 1) from (10,10) to (10,15) vertically.
  const streetA = [
    { x: 10, y: 10 },
    { x: 10, y: 11 },
    { x: 10, y: 12 },
    { x: 10, y: 13 },
    { x: 10, y: 14 },
    { x: 10, y: 15 },
  ];

  let s = board([]);
  s = reducer(s, { type: 'placeRoadPath', spec: 'road', tiles: streetA });

  // Verify street A is placed.
  assert.equal(s.buildings.filter((b) => b.spec === 'road').length, streetA.length, 'Street A placed');

  // Find the building at (10,12) for later verification
  const originalRoadAt10_12 = s.buildings.find((b) => b.x === 10 && b.y === 12 && b.spec === 'road');
  const originalId = originalRoadAt10_12.id;

  // Lay avenue B (tier 2) from (8,12) to (12,12) horizontally.
  // This crosses street A at (10,12) with DIFFERENT tier → conversion to rd_roundabout.
  const avenueB = [
    { x: 8, y: 12 },
    { x: 9, y: 12 },
    { x: 10, y: 12 }, // crosses street A here; tier rule: max(1,2)=2 → roundabout
    { x: 11, y: 12 },
    { x: 12, y: 12 },
  ];

  s = reducer(s, { type: 'placeRoadPath', spec: 'rd_avenue', tiles: avenueB });

  // ONE-BUILDING-PER-TILE INVARIANT:
  // Verify exactly ONE building at the crossing tile (10,12).
  const buildingsAtCrossing = s.buildings.filter((b) => b.x === 10 && b.y === 12);
  assert.equal(buildingsAtCrossing.length, 1, 'exactly one building at crossing tile');

  // Verify it's the ORIGINAL building (conversion, not new placement).
  assert.equal(buildingsAtCrossing[0].id, originalId, 'crossing building is original road (converted)');
  assert.equal(buildingsAtCrossing[0].spec, 'rd_roundabout', 'converted to rd_roundabout (tier 2)');

  // Verify no duplicate junctions.
  const allRoundabouts = s.buildings.filter((b) => b.spec === 'rd_roundabout');
  assert.equal(allRoundabouts.length, 1, 'exactly one roundabout');

  console.log('✓ AC-6a: avenue crossing street → roundabout conversion, one building per tile');
});

// AC-6b RED proof: avenue or above crossing → roundabout spec via tier rule.
test('AC-6b RED proof: avenue crossing street → roundabout conversion at tier 2', () => {
  // Lay a street (tier 1) from (10,10) to (10,15).
  const street = [
    { x: 10, y: 10 },
    { x: 10, y: 11 },
    { x: 10, y: 12 },
    { x: 10, y: 13 },
    { x: 10, y: 14 },
    { x: 10, y: 15 },
  ];

  let s = board([]);
  s = reducer(s, { type: 'placeRoadPath', spec: 'road', tiles: street });

  // Find original building at (10,12)
  const originalRoad = s.buildings.find((b) => b.x === 10 && b.y === 12 && b.spec === 'road');
  const originalId = originalRoad.id;

  // Lay an avenue (tier 2) from (8,12) to (12,12).
  // This crosses the street at (10,12); tier rule: max(2,1)=2 → roundabout.
  const avenue = [
    { x: 8, y: 12 },
    { x: 9, y: 12 },
    { x: 10, y: 12 }, // crosses street here; max(street=1, avenue=2) = tier 2 → roundabout
    { x: 11, y: 12 },
    { x: 12, y: 12 },
  ];

  s = reducer(s, { type: 'placeRoadPath', spec: 'rd_avenue', tiles: avenue });

  // Verify exactly one building at crossing tile (converted, not new).
  const buildingsAtCrossing = s.buildings.filter((b) => b.x === 10 && b.y === 12);
  assert.equal(buildingsAtCrossing.length, 1, 'exactly one building at crossing tile');
  assert.equal(buildingsAtCrossing[0].id, originalId, 'building is original (converted)');
  assert.equal(buildingsAtCrossing[0].spec, 'rd_roundabout', 'converted to rd_roundabout');

  // Verify no plain junctions.
  const junctions = s.buildings.filter((b) => b.spec === 'rd_junction');
  assert.equal(junctions.length, 0, 'no plain junctions when avenue is involved');

  // Verify roundabout exists (not junction).
  const roundabouts = s.buildings.filter((b) => b.spec === 'rd_roundabout');
  assert.equal(roundabouts.length, 1, 'exactly one roundabout');

  console.log('✓ AC-6b: avenue crossing street → rd_roundabout via tier rule');
});

// AC-6c RED proof: cost = new-road tiles + conversion-cost (GR#15).
test('AC-6c RED proof: conversion-cost charged once per tile, matches SPECS', () => {
  // Lay a street at (10,12).
  const street = [{ x: 10, y: 12 }];
  let s = board([]);
  s = reducer(s, { type: 'placeRoadPath', spec: 'road', tiles: street });

  const beforeFunds = s.funds;

  // Lay an avenue crossing it at (10,12) (tier 2 > tier 1 → conversion to roundabout).
  const avenueB = [
    { x: 10, y: 11 },
    { x: 10, y: 12 }, // crosses existing street → conversion to roundabout
    { x: 10, y: 13 },
  ];

  const costPerRoad = 40; // 'road' spec cost (for streets)
  const costPerAvenue = 90; // 'rd_avenue' spec cost
  const costPerRoundabout = 50; // 'rd_roundabout' conversion cost

  // Cost = 2 NEW avenue tiles (11, 13) × 90 + 1 CONVERSION (12) × roundabout-cost
  // Total = 180 + 50 = 230
  const expectedCost = 2 * costPerAvenue + 1 * costPerRoundabout;

  s = reducer(s, { type: 'placeRoadPath', spec: 'rd_avenue', tiles: avenueB });

  const actualCost = beforeFunds - s.funds;
  assert.equal(actualCost, expectedCost, `cost = 2 avenues × 90 + 1 conversion × 50 = 230`);

  // Verify the ledger records the conversion.
  const lastEvent = s.ledger[0];
  assert.equal(lastEvent.amount, -expectedCost, `ledger records exact cost`);
  assert.match(lastEvent.label, /converted.*junction/i, 'ledger mentions conversion');

  console.log('✓ AC-6c: conversion cost charged via SPECS, one charge per tile');
});

// AC-6d RED proof: insufficient funds for conversions → all-or-nothing blocks.
test('AC-6d RED proof: insufficient funds for conversion cost → all placement blocked', () => {
  // Lay a street at (10,12).
  const street = [{ x: 10, y: 12 }];
  let s = board([]);
  s = reducer(s, { type: 'placeRoadPath', spec: 'road', tiles: street });

  // Crossing path: avenue crossing street
  // Cost = 2 new avenues (180) + 1 conversion (50) = 230
  const avenueB = [
    { x: 10, y: 11 },
    { x: 10, y: 12 }, // crosses → conversion cost
    { x: 10, y: 13 },
  ];

  const costPerAvenue = 90;
  const costPerRoundabout = 50;
  const totalCost = 2 * costPerAvenue + 1 * costPerRoundabout; // 230

  // Set funds to less than total.
  s = { ...s, funds: totalCost - 1 };

  const beforeFunds = s.funds;
  const beforeBuildingCount = s.buildings.length;

  s = reducer(s, { type: 'placeRoadPath', spec: 'rd_avenue', tiles: avenueB });

  // No new tiles should be placed.
  assert.equal(s.buildings.length, beforeBuildingCount, 'no new tiles placed (all-or-nothing)');

  // Existing building at (10,12) should still be 'road' (not converted).
  const stillRoad = s.buildings.find((b) => b.x === 10 && b.y === 12);
  assert.equal(stillRoad.spec, 'road', 'crossing tile not converted when funds insufficient');

  // Funds unchanged.
  assert.equal(s.funds, beforeFunds, 'funds unchanged');

  // placeNotice should be set.
  assert.ok(s.placeNotice, 'placeNotice set');
  assert.match(s.placeNotice, /Insufficient funds/i, 'notice mentions insufficient funds');

  console.log('✓ AC-6d: insufficient funds blocks all placement (all-or-nothing)');
});

// AC-6e RED proof: same-tile dedup (self-intersection) has zero cost.
test('AC-6e RED proof: self-intersection (same tile twice in path) deduped, zero cost', () => {
  // A path that revisits the same tile (dedup should keep only one copy).
  const pathWithDup = [
    { x: 10, y: 10 },
    { x: 11, y: 10 },
    { x: 10, y: 10 }, // duplicate, should be deduped
    { x: 10, y: 11 },
  ];

  let s = board([]);
  const beforeFunds = s.funds;
  s = reducer(s, { type: 'placeRoadPath', spec: 'road', tiles: pathWithDup });

  // Verify only 3 unique tiles placed (the duplicate is deduped).
  const roads = s.buildings.filter((b) => b.spec === 'road');
  assert.equal(roads.length, 3, 'dedup reduces duplicate tile to one placement');

  // Verify cost is 3 roads × 40 (no extra charge for duplicate).
  const costPerRoad = 40;
  const expectedCost = 3 * costPerRoad;
  const actualCost = beforeFunds - s.funds;
  assert.equal(actualCost, expectedCost, 'dedup does not charge for duplicate');

  // Verify no junction was created (no existing roads to cross, just dedup).
  const junctions = s.buildings.filter((b) => b.spec === 'rd_junction' || b.spec === 'rd_roundabout');
  assert.equal(junctions.length, 0, 'no junction from self-intersection dedup');

  console.log('✓ AC-6e: self-intersection deduped, zero cost for duplicate');
});

// Repeat-drag test: identical path twice → second commit costs zero (full dedup).
test('Repeat-drag: identical path twice → second commit deduped, zero cost', () => {
  const path = [
    { x: 10, y: 10 },
    { x: 10, y: 11 },
    { x: 10, y: 12 },
  ];

  let s = board([]);

  // First drag: place path
  const beforeFirst = s.funds;
  s = reducer(s, { type: 'placeRoadPath', spec: 'road', tiles: path });
  const firstCost = beforeFirst - s.funds;
  assert.equal(firstCost, 120, 'first drag costs 3 roads × 40');

  const beforeSecond = s.funds;
  const beforeBuildingCount = s.buildings.length;

  // Second identical drag: should dedup all tiles
  s = reducer(s, { type: 'placeRoadPath', spec: 'road', tiles: path });
  const secondCost = beforeSecond - s.funds;

  // All tiles are same-spec overlaps → zero cost
  assert.equal(secondCost, 0, 'second drag deduped, zero cost');

  // No new buildings added
  assert.equal(s.buildings.length, beforeBuildingCount, 'no new buildings on repeat');

  console.log('✓ Repeat-drag: identical path twice, second costs zero');
});

// Demolish test: destroying roundabout building removes it fully.
test('Demolish: removing roundabout building removes it fully (no orphans)', () => {
  // Lay street A
  const streetA = [{ x: 10, y: 12 }];
  let s = board([]);
  s = reducer(s, { type: 'placeRoadPath', spec: 'road', tiles: streetA });

  // Lay avenue B crossing at (10,12) → converts to roundabout (tier 2 > tier 1)
  const avenueB = [
    { x: 10, y: 11 },
    { x: 10, y: 12 }, // conversion to roundabout
    { x: 10, y: 13 },
  ];

  s = reducer(s, { type: 'placeRoadPath', spec: 'rd_avenue', tiles: avenueB });

  // Verify roundabout exists at (10,12)
  const buildingsAtTile = s.buildings.filter((b) => b.x === 10 && b.y === 12);
  assert.equal(buildingsAtTile.length, 1, 'one building at crossing before demolish');
  assert.equal(buildingsAtTile[0].spec, 'rd_roundabout', 'building is roundabout');

  // Bulldoze the roundabout
  const before = s.buildings.length;
  s = reducer(s, { type: 'bulldoze', x: 10, y: 12 });

  // Verify roundabout is gone (no orphans left behind)
  const afterDemolish = s.buildings.filter((b) => b.x === 10 && b.y === 12);
  assert.equal(afterDemolish.length, 0, 'roundabout removed, no orphans');

  // Verify total building count decreased by 1
  assert.equal(s.buildings.length, before - 1, 'one building removed');

  console.log('✓ Demolish: roundabout removed fully, no orphans');
});
