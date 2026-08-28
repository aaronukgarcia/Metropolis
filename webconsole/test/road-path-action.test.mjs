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
