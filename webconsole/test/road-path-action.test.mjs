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

// ════════════════════════════════════════════════════════════════════════════
// AC-7: Rail bridge (grade-separated crossing)
// FEAT-1972079910 inc3: dual+ road crossing rail → rd_railbridge conversion
// ════════════════════════════════════════════════════════════════════════════

// Helper: import isRailSpec to verify rd_railbridge is recognized as rail
import { isRailSpec, RAIL_BRIDGE_COST_MULTIPLIER } from '../src/sim/data.ts';
import { SPECS } from '../src/sim/data.ts';
import { buildRailGeometry } from '../src/sim/trains.ts';

// AC-7a RED proof: structural rail line continuity through bridge
// The test MUST assert line continuity (not just isRailSpec membership).
test('AC-7a RED proof: dual crossing rail → rd_railbridge preserves line continuity', () => {
  // Build a straight 'rail' line: 5 tiles horizontally at y=12
  const railLine = [
    { x: 8, y: 12 },
    { x: 9, y: 12 },
    { x: 10, y: 12 }, // midpoint — will convert to bridge
    { x: 11, y: 12 },
    { x: 12, y: 12 },
  ];

  let s = board([]);
  // Place the rail line
  for (const tile of railLine) {
    s = reducer(s, { type: 'place', spec: 'rail', x: tile.x, y: tile.y });
  }

  // Verify all rail tiles placed
  const railBefore = s.buildings.filter((b) => b.spec === 'rail');
  assert.equal(railBefore.length, railLine.length, `${railLine.length} rail tiles placed`);

  // Build geometry BEFORE conversion
  const railTilesBefore = s.buildings
    .filter((b) => isRailSpec(SPECS[b.spec]))
    .map((b) => ({ spec: b.spec, x: b.x, y: b.y }));
  const geomBefore = buildRailGeometry(railTilesBefore, []);
  const railGeomBefore = geomBefore.find((g) => g.spec === 'rail');
  assert.ok(railGeomBefore, 'rail line geometry exists before conversion');
  const pointsBefore = railGeomBefore.points.length;
  assert.equal(pointsBefore, 5, 'rail line has 5 points before conversion');

  // Now cross the mid-tile with a dual road
  const dualPath = [
    { x: 10, y: 11 },
    { x: 10, y: 12 }, // crosses rail tile at (10,12)
    { x: 10, y: 13 },
  ];

  s = reducer(s, { type: 'placeRoadPath', spec: 'rd_dual', tiles: dualPath });

  // Verify the rail tile is converted to rd_railbridge and bridgeOver is set
  const bridgeAfter = s.buildings.find((b) => b.x === 10 && b.y === 12);
  assert.ok(bridgeAfter, 'building still exists at crossing tile');
  assert.equal(bridgeAfter.spec, 'rd_railbridge', 'spec converted to rd_railbridge');
  assert.equal(bridgeAfter.bridgeOver, 'rail', 'bridgeOver preserved as "rail"');

  // CRITICAL: rebuild geometry AFTER conversion and verify line continuity
  const railTilesAfter = s.buildings
    .filter((b) => isRailSpec(SPECS[b.spec]))
    .map((b) => {
      // AC-7 FIX: use bridgeOver for bridge tiles to restore line class membership
      const lineSpec = (b.bridgeOver ?? b.spec);
      return { spec: lineSpec, x: b.x, y: b.y };
    });
  const geomAfter = buildRailGeometry(railTilesAfter, []);
  const railGeomAfter = geomAfter.find((g) => g.spec === 'rail');
  assert.ok(railGeomAfter, 'rail line geometry exists after conversion');
  const pointsAfter = railGeomAfter.points.length;
  assert.equal(pointsAfter, pointsBefore, 'rail line has SAME point count after bridge conversion (no gap)');

  // Verify the bridge tile coordinates are in the rail geometry (not orphaned)
  const bridgeTileInLine = railGeomAfter.points.find((p) => p.x === 10 && p.y === 12);
  assert.ok(bridgeTileInLine, 'bridge tile (10,12) is present in rail line geometry');

  console.log('✓ AC-7a: dual crossing rail → rd_railbridge preserves line continuity (structural)');
});

// AC-7a RED proof: FALSE-PASS detection — remove bridgeOver mapping and verify test fails
test('AC-7a RED: WITHOUT bridgeOver, line continuity breaks (tile orphaned)', () => {
  // Same setup as AC-7a
  const railLine = [
    { x: 8, y: 12 },
    { x: 9, y: 12 },
    { x: 10, y: 12 },
    { x: 11, y: 12 },
    { x: 12, y: 12 },
  ];

  let s = board([]);
  for (const tile of railLine) {
    s = reducer(s, { type: 'place', spec: 'rail', x: tile.x, y: tile.y });
  }

  const dualPath = [
    { x: 10, y: 11 },
    { x: 10, y: 12 },
    { x: 10, y: 13 },
  ];

  s = reducer(s, { type: 'placeRoadPath', spec: 'rd_dual', tiles: dualPath });

  // INTENTIONAL BREAK: do NOT use bridgeOver — push b.spec instead
  // This simulates the false-pass condition where bridgeOver is missing
  const railTilesWithoutFix = s.buildings
    .filter((b) => isRailSpec(SPECS[b.spec]))
    .map((b) => {
      // BROKEN: ignore bridgeOver, just use spec (like old code)
      return { spec: b.spec, x: b.x, y: b.y };
    });
  const geomBroken = buildRailGeometry(railTilesWithoutFix, []);
  const railGeomBroken = geomBroken.find((g) => g.spec === 'rail');
  const bridgeGeomBroken = geomBroken.find((g) => g.spec === 'rd_railbridge');

  // OBSERVED FAILURE: without bridgeOver, the 'rail' line splits and a new
  // orphan 'rd_railbridge' line appears with just 1 point (the bridge)
  const pointsNoFix = railGeomBroken?.points.length ?? 0;
  const pointsBridgeGeom = bridgeGeomBroken?.points.length ?? 0;

  // This test PASSES when it detects the broken condition
  assert.equal(pointsNoFix, 4, 'OBSERVED: rail line broken into 4 tiles (missing mid-point)');
  assert.equal(pointsBridgeGeom, 1, 'OBSERVED: orphan rd_railbridge geometry with 1 point');

  console.log('✓ AC-7a RED: confirmed that missing bridgeOver causes observable line split (test able to fail)');
});

// AC-7 HS1 test: dual crossing hs1 → rd_railbridge with bridgeOver='hs1'
test('AC-7 HS1: dual crossing hs1 line → rd_railbridge preserves hs1 line continuity', () => {
  // Build a straight 'hs1' line: 5 tiles horizontally at y=14
  const hs1Line = [
    { x: 8, y: 14 },
    { x: 9, y: 14 },
    { x: 10, y: 14 }, // midpoint — will convert to bridge
    { x: 11, y: 14 },
    { x: 12, y: 14 },
  ];

  let s = board([]);
  // Place the hs1 line
  for (const tile of hs1Line) {
    s = reducer(s, { type: 'place', spec: 'hs1', x: tile.x, y: tile.y });
  }

  // Build geometry BEFORE conversion
  const hs1TilesBefore = s.buildings
    .filter((b) => isRailSpec(SPECS[b.spec]))
    .map((b) => ({ spec: b.spec, x: b.x, y: b.y }));
  const geomBefore = buildRailGeometry(hs1TilesBefore, []);
  const hs1GeomBefore = geomBefore.find((g) => g.spec === 'hs1');
  assert.ok(hs1GeomBefore, 'hs1 line geometry exists before conversion');
  const pointsBefore = hs1GeomBefore.points.length;

  // Cross mid-tile with dual road
  const dualPath = [
    { x: 10, y: 13 },
    { x: 10, y: 14 }, // crosses hs1 tile at (10,14)
    { x: 10, y: 15 },
  ];

  s = reducer(s, { type: 'placeRoadPath', spec: 'rd_dual', tiles: dualPath });

  // Verify bridgeOver='hs1'
  const bridgeAfter = s.buildings.find((b) => b.x === 10 && b.y === 14);
  assert.equal(bridgeAfter.spec, 'rd_railbridge', 'spec converted to rd_railbridge');
  assert.equal(bridgeAfter.bridgeOver, 'hs1', 'bridgeOver preserved as "hs1"');

  // Verify hs1 line continuity with bridgeOver fix
  const hs1TilesAfter = s.buildings
    .filter((b) => isRailSpec(SPECS[b.spec]))
    .map((b) => {
      const lineSpec = (b.bridgeOver ?? b.spec);
      return { spec: lineSpec, x: b.x, y: b.y };
    });
  const geomAfter = buildRailGeometry(hs1TilesAfter, []);
  const hs1GeomAfter = geomAfter.find((g) => g.spec === 'hs1');
  assert.ok(hs1GeomAfter, 'hs1 line geometry exists after conversion');
  assert.equal(hs1GeomAfter.points.length, pointsBefore, 'hs1 line has same point count (no gap)');

  console.log('✓ AC-7 HS1: dual crossing hs1 → rd_railbridge preserves hs1 continuity');
});

// AC-7 Serialization: bridgeOver survives replay
test('AC-7 Serialization: bridgeOver field survives replay', () => {
  // Set up: place rail, then cross with dual to create bridge
  let s1 = board([]);
  s1 = reducer(s1, { type: 'place', spec: 'rail', x: 10, y: 12 });

  const bridgeAction = {
    type: 'placeRoadPath',
    spec: 'rd_dual',
    tiles: [{ x: 9, y: 12 }, { x: 10, y: 12 }, { x: 11, y: 12 }],
  };

  s1 = reducer(s1, bridgeAction);

  // Verify bridge has bridgeOver field
  const bridge1 = s1.buildings.find((b) => b.spec === 'rd_railbridge');
  assert.ok(bridge1, 'bridge created');
  assert.equal(bridge1.bridgeOver, 'rail', 'bridgeOver="rail" in s1');

  // Replay: apply same action to fresh state
  let s2 = board([]);
  s2 = reducer(s2, { type: 'place', spec: 'rail', x: 10, y: 12 });
  s2 = reducer(s2, bridgeAction);

  // Verify replay has identical bridgeOver field
  const bridge2 = s2.buildings.find((b) => b.spec === 'rd_railbridge');
  assert.ok(bridge2, 'bridge created in replay');
  assert.equal(bridge2.bridgeOver, 'rail', 'bridgeOver="rail" in s2 (replay)');

  // Both should have identical bridgeOver (serialization survived)
  assert.deepEqual(bridge1, bridge2, 'bridge objects identical after replay (bridgeOver survives)');

  console.log('✓ AC-7 Serialization: bridgeOver preserved through replay');
});

// AC-7b RED proof: street (tier 1) crossing rail → no placement (level crossing not implemented)
test('AC-7b RED proof: street (tier 1) crossing rail → rejected, zero placement', () => {
  // Place a rail line at (10,12)
  let s = board([]);
  s = reducer(s, { type: 'place', spec: 'rail', x: 10, y: 12 });

  const beforeRailPlaceCount = s.buildings.length; // 1 rail + 1 stone/initial
  const beforeFunds = s.funds;

  // Attempt to lay street (tier 1) crossing the rail tile
  const streetPath = [
    { x: 9, y: 12 },
    { x: 10, y: 12 }, // crosses rail (tier 1 < required tier 4)
    { x: 11, y: 12 },
  ];

  s = reducer(s, { type: 'placeRoadPath', spec: 'road', tiles: streetPath });

  // No new buildings should be added (the street path crossing rail is rejected entirely)
  assert.equal(
    s.buildings.length,
    beforeRailPlaceCount,
    'zero new buildings placed (path crossing rail without dual+ rejected)'
  );

  // Funds should be unchanged
  assert.equal(s.funds, beforeFunds, 'funds unchanged (no cost incurred)');

  console.log('✓ AC-7b: street crossing rail rejected, zero placement');
});

// AC-7c: cost calculation — bridge cost = road cost × RAIL_BRIDGE_COST_MULTIPLIER
test('AC-7c: rail bridge cost = road cost × 4x multiplier', () => {
  // Place a rail line
  let s = board([]);
  s = reducer(s, { type: 'place', spec: 'rail', x: 10, y: 12 });

  const ledgerBeforePath = s.ledger.length;
  const beforeFunds = s.funds;

  // Lay dual crossing the rail
  const dualPath = [
    { x: 9, y: 12 },
    { x: 10, y: 12 }, // bridge tile
    { x: 11, y: 12 },
  ];

  s = reducer(s, { type: 'placeRoadPath', spec: 'rd_dual', tiles: dualPath });

  const actualCost = beforeFunds - s.funds;

  // Expected cost: 2 new tiles (x=9, x=11) at rd_dual cost + 1 bridge (rd_dual cost × 4)
  const rd_dualCost = SPECS['rd_dual'].cost;
  const expectedCost = 2 * rd_dualCost + rd_dualCost * RAIL_BRIDGE_COST_MULTIPLIER;

  assert.equal(actualCost, expectedCost, `bridge cost = ${rd_dualCost} × 4 = ${expectedCost}`);

  // Verify ledger entry shows the cost
  // Note: logEvent prepends new entries, so the path entry is at index 0
  const pathEntry = s.ledger[0];
  assert.ok(pathEntry.label.includes('Laid'), 'ledger entry says "Laid"');
  assert.ok(pathEntry.label.includes('road'), 'ledger entry says "road"');
  assert.equal(pathEntry.amount, -expectedCost, 'ledger shows correct cost');

  console.log(`✓ AC-7c: bridge cost correctly multiplied (${expectedCost})`);
});

// ════════════════════════════════════════════════════════════════════════════
// AC-8: Motorway junction (grade-separated crossing)
// FEAT-1972079910 inc3: any road crossing motorway → rd_mwyjunction conversion
// ════════════════════════════════════════════════════════════════════════════

import { MOTORWAY_JUNCTION_COST } from '../src/sim/data.ts';

// AC-8a RED proof: road crossing motorway → rd_mwyjunction, id preserved, both connected
test('AC-8a RED proof: road crossing motorway → rd_mwyjunction, id preserved', () => {
  // Place a motorway (m20) at (10,12)
  let s = board([]);
  s = reducer(s, { type: 'place', spec: 'm20', x: 10, y: 12 });

  // Verify motorway tile exists
  const m20Before = s.buildings.find((b) => b.spec === 'm20' && b.x === 10 && b.y === 12);
  assert.ok(m20Before, 'm20 motorway placed at (10,12)');
  const m20IdBefore = m20Before.id;

  // Lay avenue crossing the motorway tile
  const avenuePath = [
    { x: 10, y: 11 },
    { x: 10, y: 12 }, // crosses motorway
    { x: 10, y: 13 },
  ];

  s = reducer(s, { type: 'placeRoadPath', spec: 'rd_avenue', tiles: avenuePath });

  // Verify motorway tile is converted to rd_mwyjunction (not deleted)
  const junctionAfter = s.buildings.find((b) => b.x === 10 && b.y === 12);
  assert.ok(junctionAfter, 'building still exists at crossing tile');
  assert.equal(junctionAfter.spec, 'rd_mwyjunction', 'spec converted to rd_mwyjunction');
  assert.equal(junctionAfter.id, m20IdBefore, 'building ID preserved from original m20 tile');

  // Verify one building at the tile (one-building-per-tile invariant)
  const tilesAt = s.buildings.filter((b) => b.x === 10 && b.y === 12);
  assert.equal(tilesAt.length, 1, 'exactly one building at crossing tile');

  // Verify avenue path is placed
  const avenueRoads = s.buildings.filter((b) => b.spec === 'rd_avenue');
  assert.equal(avenueRoads.length, 2, 'two new avenue tiles placed (y=11, y=13)');

  console.log('✓ AC-8a: road crossing motorway → rd_mwyjunction, id preserved');
});

// AC-8b: motorway junction cost is the flat MOTORWAY_JUNCTION_COST from spec
test('AC-8b: motorway junction cost = flat MOTORWAY_JUNCTION_COST (£250k)', () => {
  // Place a motorway
  let s = board([]);
  s = reducer(s, { type: 'place', spec: 'm20', x: 10, y: 12 });

  const ledgerBeforePath = s.ledger.length;
  const beforeFunds = s.funds;

  // Lay avenue crossing the motorway
  const avenuePath = [
    { x: 10, y: 11 },
    { x: 10, y: 12 }, // junction tile
    { x: 10, y: 13 },
  ];

  s = reducer(s, { type: 'placeRoadPath', spec: 'rd_avenue', tiles: avenuePath });

  const actualCost = beforeFunds - s.funds;

  // Expected cost: 2 new avenue tiles + 1 flat junction cost
  const rd_avenueCost = SPECS['rd_avenue'].cost;
  const expectedCost = 2 * rd_avenueCost + MOTORWAY_JUNCTION_COST;

  assert.equal(actualCost, expectedCost, `cost = 2 × avenue + junction flat cost = ${expectedCost}`);

  // Verify ledger entry
  // Note: logEvent prepends new entries, so the path entry is at index 0
  const pathEntry = s.ledger[0];
  assert.ok(pathEntry.label.includes('Laid'), 'ledger entry says "Laid"');
  assert.ok(pathEntry.label.includes('road'), 'ledger entry says "road"');
  assert.equal(pathEntry.amount, -expectedCost, 'ledger shows correct cost');

  console.log(`✓ AC-8b: motorway junction flat cost applied (${expectedCost})`);
});

// Atomicity RED proof: funds = total−1 → nothing places, nothing converts
test('Atomicity RED proof: funds = total−1 → nothing places, nothing converts', () => {
  // Start fresh and calculate the cost of laying a dual path with a bridge
  let s = board([]);

  // Place a rail tile at the crossing point
  s = reducer(s, { type: 'place', spec: 'rail', x: 10, y: 12 });

  // Discover the actual cost with ample funds
  let testState = { ...s, funds: 1000000 };
  const testPath = [
    { x: 9, y: 12 },
    { x: 10, y: 12 }, // bridge crossing
    { x: 11, y: 12 },
  ];
  testState = reducer(testState, {
    type: 'placeRoadPath',
    spec: 'rd_dual',
    tiles: testPath,
  });
  const totalCostNeeded = 1000000 - testState.funds;

  // Reset to original state with insufficient funds (total - 1)
  s = {
    ...s,
    funds: totalCostNeeded - 1,
  };

  const beforeFunds = s.funds;
  const beforeBuildingCount = s.buildings.length;

  // Attempt to place path with insufficient funds
  s = reducer(s, {
    type: 'placeRoadPath',
    spec: 'rd_dual',
    tiles: testPath,
  });

  // Nothing should change
  assert.equal(
    s.buildings.length,
    beforeBuildingCount,
    'no buildings added/converted when funds insufficient (all-or-nothing)'
  );
  assert.equal(s.funds, beforeFunds, 'funds unchanged');
  assert.ok(s.placeNotice, 'placeNotice set for insufficient funds');

  console.log('✓ Atomicity: insufficient funds prevents all placements');
});
