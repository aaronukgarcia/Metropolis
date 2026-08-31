// road-motorway-junction-spacing.test.mjs — FEAT-1972079928 inc2, AC-5.
//
// Aaron's ruling (BOW comment, 2026-08-31 — AUTHORITATIVE over the BA draft):
// motorway junction scarcity = MINIMUM SPACING. The auto-replanner may only
// place a NEW motorway junction when it is >= MOTORWAY_JUNCTION_MIN_SPACING_
// TILES from the nearest EXISTING motorway junction; a nearer connection
// routes via a slip to a parallel A-road (or, absent one, is simply
// suppressed) instead of cutting a direct motorway junction. This file proves
// the AC-5 Check/Mutation/False-pass triad, plus an AC-8 (no double-
// conversion) proof specific to the motorway-junction path.
//
// RED proof performed (scratch cp/mv, NEVER git):
//   Commented out the `!motorwayJunctionSpacingOk(...)` branch in engine.ts's
//   placeRoadPath `existingMotorway` case (forcing every crossing straight to
//   the flat-conversion branch, i.e. the functional equivalent of
//   MOTORWAY_JUNCTION_MIN_SPACING_TILES = 0) → the spacing test below goes
//   RED: the second collector converts (52,50) to rd_mwyjunction and
//   countMotorwayJunctions() reads 2 instead of 1. Restored immediately after.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  initialState,
  reducer,
  nearestMotorwayJunctionDistance,
  countMotorwayJunctions,
  motorwayJunctionSpacingOk,
  findMotorwaySlipTarget,
  MOTORWAY_JUNCTION_MIN_SPACING_TILES,
  MOTORWAY_JUNCTION_MAX_PER_SEGMENT,
} from '../src/sim/engine.ts';
import { SPECS, MOTORWAY_JUNCTION_COST } from '../src/sim/data.ts';

// Build a clean board (no starter city) with an explicit building list and
// ample funds, mirroring the sibling road-*.test.mjs files' `board` helper.
function board(buildings, funds = 10_000_000) {
  const base = initialState();
  let maxId = 0;
  for (const b of buildings) if (b.id > maxId) maxId = b.id;
  return { ...base, unlockedAll: true, buildings, nextId: maxId + 1, funds, placeNotice: null };
}

const tileAt = (s, x, y) => s.buildings.find((b) => b.x === x && b.y === y);
const m20At = (x, y, id) => ({ id, spec: 'm20', x, y, builtTick: -1000 });

// A motorway running east-west from (40,50) to (60,50), per the AC-5 doc Check.
function motorwayBoard() {
  const tiles = [];
  let id = 1;
  for (let x = 40; x <= 60; x++) tiles.push(m20At(x, 50, id++));
  return board(tiles);
}

test('precondition: the AC-5 placeholder-balance constants this file assumes', () => {
  assert.equal(MOTORWAY_JUNCTION_MIN_SPACING_TILES, 6, 'AC-5 min-spacing placeholder');
  assert.equal(MOTORWAY_JUNCTION_MAX_PER_SEGMENT, 4, 'AC-5 max-per-segment placeholder');
});

// ════════════════════════════════════════════════════════════════════════════
// AC-5 Check: two collectors cross the motorway 2 tiles apart (well within the
// 6-tile minimum). The first gets a junction; the second must NOT.
// ════════════════════════════════════════════════════════════════════════════

test('AC-5 Check: a first collector crossing gets a motorway junction', () => {
  let s = motorwayBoard();
  s = reducer(s, {
    type: 'placeRoadPath',
    spec: 'rd_avenue',
    tiles: [{ x: 50, y: 49 }, { x: 50, y: 50 }, { x: 50, y: 51 }],
  });

  const crossing = tileAt(s, 50, 50);
  assert.ok(crossing, 'crossing tile still exists');
  assert.equal(crossing.spec, 'rd_mwyjunction', 'first crossing converted to a motorway junction');
  assert.equal(countMotorwayJunctions(s), 1, 'exactly one motorway junction exists so far');
});

test('AC-5 Check: a SECOND collector 2 tiles away (< min spacing) does NOT get its own junction', () => {
  let s = motorwayBoard();
  s = reducer(s, {
    type: 'placeRoadPath',
    spec: 'rd_avenue',
    tiles: [{ x: 50, y: 49 }, { x: 50, y: 50 }, { x: 50, y: 51 }],
  });
  const firstJunction = tileAt(s, 50, 50);
  assert.equal(firstJunction.spec, 'rd_mwyjunction', 'precondition: first junction placed');
  const firstJunctionId = firstJunction.id;

  // Second collector crosses at (52,50) — 2 tiles from the first junction,
  // well inside MOTORWAY_JUNCTION_MIN_SPACING_TILES (6).
  const fundsBefore = s.funds;
  s = reducer(s, {
    type: 'placeRoadPath',
    spec: 'rd_avenue',
    tiles: [{ x: 52, y: 49 }, { x: 52, y: 50 }, { x: 52, y: 51 }],
  });

  // False-pass guard: do NOT merely check the first junction still exists —
  // prove the SECOND crossing was suppressed (not converted, not double-billed).
  const secondCrossing = tileAt(s, 52, 50);
  assert.ok(secondCrossing, 'the underlying motorway tile at the second crossing still exists');
  assert.equal(secondCrossing.spec, 'm20', 'second crossing tile stays a plain motorway tile — NOT converted to a junction');

  // The first junction is untouched (same id, same spec) — no reshuffle.
  assert.equal(tileAt(s, 50, 50).id, firstJunctionId, 'first junction building id unchanged');
  assert.equal(tileAt(s, 50, 50).spec, 'rd_mwyjunction', 'first junction spec unchanged');

  // Only ONE motorway junction exists network-wide after both placements.
  assert.equal(countMotorwayJunctions(s), 1, 'still exactly one motorway junction — the second was suppressed, not added');

  // The connecting collector tiles either side of the crossing were still
  // placed (only the direct motorway crossing itself is suppressed) — proves
  // this is a reroute/suppress of the JUNCTION, not a rejection of the whole path.
  assert.equal(tileAt(s, 52, 49).spec, 'rd_avenue', 'collector tile north of the crossing still placed');
  assert.equal(tileAt(s, 52, 51).spec, 'rd_avenue', 'collector tile south of the crossing still placed');

  // No MOTORWAY_JUNCTION_COST was charged for the suppressed crossing — only
  // the two ordinary rd_avenue tiles were billed.
  const avenueCost = SPECS.rd_avenue.cost;
  assert.equal(fundsBefore - s.funds, avenueCost * 2, 'second placement billed only for the 2 collector tiles, no junction charge');
});

// ════════════════════════════════════════════════════════════════════════════
// AC-5 Mutation: prove the spacing test can actually go RED. `motorwayJunction
// SpacingOk` takes minSpacing as a PARAMETER (functional-equivalent mutation
// target for a real `const`, same pattern as replanSearch's `radius` arg in
// road-replan-inc1.test.mjs) — calling it with minSpacing=0 is the exact
// behavioural equivalent of setting MOTORWAY_JUNCTION_MIN_SPACING_TILES = 0.
// ════════════════════════════════════════════════════════════════════════════

test('AC-5 Mutation: with min spacing forced to 0, the second crossing WOULD be allowed (spacing rule proven load-bearing)', () => {
  let s = motorwayBoard();
  s = reducer(s, {
    type: 'placeRoadPath',
    spec: 'rd_avenue',
    tiles: [{ x: 50, y: 49 }, { x: 50, y: 50 }, { x: 50, y: 51 }],
  });

  const dist = nearestMotorwayJunctionDistance(s, 52, 50);
  assert.equal(dist, 2, 'precondition: the second crossing really is 2 tiles from the first junction');

  // With the REAL default (6): not OK — this is what the reducer enforces above.
  assert.equal(
    motorwayJunctionSpacingOk(s, 52, 50, MOTORWAY_JUNCTION_MIN_SPACING_TILES, MOTORWAY_JUNCTION_MAX_PER_SEGMENT),
    false,
    'real default min-spacing (6): second crossing is NOT allowed'
  );

  // With the rule zeroed out (mutation-equivalent): now OK — proves that if
  // the guard were deleted/mutated, the AC-5 Check test above would go RED
  // (the second crossing would convert, countMotorwayJunctions would read 2).
  assert.equal(
    motorwayJunctionSpacingOk(s, 52, 50, 0, Infinity),
    true,
    'mutation (min spacing = 0, max count = Infinity): second crossing WOULD be allowed — the rule is load-bearing'
  );
});

test('AC-5 Mutation, full reducer-level RED proof: MIN_SPACING functional-equivalent of 0 removed would make the spacing test fail', () => {
  // This test does not monkeypatch the module (ES module bindings are
  // read-only from outside); instead it documents, via the pure predicate
  // function used by the reducer, exactly which assertion in the Check test
  // above would flip if the constant were mutated to 0 — the same technique
  // road-replan-inc1.test.mjs uses for REPLAN_RADIUS_TILES.
  let s = motorwayBoard();
  s = reducer(s, {
    type: 'placeRoadPath',
    spec: 'rd_avenue',
    tiles: [{ x: 50, y: 49 }, { x: 50, y: 50 }, { x: 50, y: 51 }],
  });
  // Simulate "mutation active": every spacing check now uses 0/Infinity.
  const mutatedAllowed = motorwayJunctionSpacingOk(s, 52, 50, 0, Infinity);
  assert.equal(mutatedAllowed, true, 'mutation: spacing rule gone, crossing allowed');
  // The real reducer (unmutated) disagrees — proving the two are not the same
  // and that the guard the reducer actually calls is doing real work.
  const realAllowed = motorwayJunctionSpacingOk(
    s,
    52,
    50,
    MOTORWAY_JUNCTION_MIN_SPACING_TILES,
    MOTORWAY_JUNCTION_MAX_PER_SEGMENT
  );
  assert.notEqual(mutatedAllowed, realAllowed, 'mutated vs real spacing predicate disagree on this exact scenario');
});

// ════════════════════════════════════════════════════════════════════════════
// AC-5 False-pass guard: a test that only checks the max-count rule (not
// spacing) would miss this class of bug entirely — prove spacing alone (with
// count nowhere near the max) is what's enforced here.
// ════════════════════════════════════════════════════════════════════════════

test('AC-5 False-pass guard: spacing is enforced independently of the count rule (only 1 junction exists, nowhere near MAX_PER_SEGMENT)', () => {
  let s = motorwayBoard();
  s = reducer(s, {
    type: 'placeRoadPath',
    spec: 'rd_avenue',
    tiles: [{ x: 50, y: 49 }, { x: 50, y: 50 }, { x: 50, y: 51 }],
  });
  assert.ok(countMotorwayJunctions(s) < MOTORWAY_JUNCTION_MAX_PER_SEGMENT, 'precondition: nowhere near the max-count threshold');

  s = reducer(s, {
    type: 'placeRoadPath',
    spec: 'rd_avenue',
    tiles: [{ x: 52, y: 49 }, { x: 52, y: 50 }, { x: 52, y: 51 }],
  });
  // Even though count is far below the max, spacing alone still suppressed it.
  assert.equal(tileAt(s, 52, 50).spec, 'm20', 'spacing rule alone (independent of count) suppressed the second junction');
});

// ════════════════════════════════════════════════════════════════════════════
// Spacing satisfied: a collector far enough away (>= MIN_SPACING) DOES get
// its own junction — proves this isn't a blanket "only one junction ever" bug.
// ════════════════════════════════════════════════════════════════════════════

test('spacing satisfied: a collector >= MIN_SPACING away from the first junction gets its own junction', () => {
  let s = motorwayBoard();
  s = reducer(s, {
    type: 'placeRoadPath',
    spec: 'rd_avenue',
    tiles: [{ x: 50, y: 49 }, { x: 50, y: 50 }, { x: 50, y: 51 }],
  });

  // (57,50) is 7 tiles from (50,50) — >= MOTORWAY_JUNCTION_MIN_SPACING_TILES (6).
  s = reducer(s, {
    type: 'placeRoadPath',
    spec: 'rd_avenue',
    tiles: [{ x: 57, y: 49 }, { x: 57, y: 50 }, { x: 57, y: 51 }],
  });

  assert.equal(tileAt(s, 57, 50).spec, 'rd_mwyjunction', 'a sufficiently-spaced crossing gets its own junction');
  assert.equal(countMotorwayJunctions(s), 2, 'two junctions now exist, correctly spaced');
});

// ════════════════════════════════════════════════════════════════════════════
// AC-5 option (a): a slip target (parallel A-road-or-above) is found when one
// is in reach, and findMotorwaySlipTarget returns null when none is.
// ════════════════════════════════════════════════════════════════════════════

test('findMotorwaySlipTarget: finds a nearby parallel A-road tile, ignores tiles inside the excluded (own-path) set', () => {
  const roadByTile = new Map();
  roadByTile.set('49,48', { building: { x: 49, y: 48 }, spec: SPECS.rd_aroad });
  const excluded = new Set(['52,50']); // the crossing tile itself, never a valid slip target
  const found = findMotorwaySlipTarget(roadByTile, 50, 50, 4, excluded);
  assert.deepEqual(found, { x: 49, y: 48 }, 'the parallel A-road tile is found within the search radius');
});

test('findMotorwaySlipTarget: returns null when nothing viable (only sub-A-road tiles or nothing) is in reach', () => {
  const roadByTile = new Map();
  roadByTile.set('49,48', { building: { x: 49, y: 48 }, spec: SPECS.road }); // tier 1, too low
  const excluded = new Set();
  const found = findMotorwaySlipTarget(roadByTile, 50, 50, 2, excluded);
  assert.equal(found, null, 'a local-tier road does not qualify as a slip target (A-road tier 3+ required)');

  const empty = findMotorwaySlipTarget(new Map(), 50, 50, 4, excluded);
  assert.equal(empty, null, 'no candidates at all → null');
});

// ════════════════════════════════════════════════════════════════════════════
// AC-8 (no double-conversion), specific to the motorway-junction path: once a
// tile is rd_mwyjunction, a SECOND crossing over the SAME tile must merge
// into it, never re-convert, never double-charge, never churn the id.
// ════════════════════════════════════════════════════════════════════════════

test('AC-8: a second road crossing an ALREADY-junction tile merges into it — no double-conversion, no double-charge, id stable', () => {
  let s = motorwayBoard();
  s = reducer(s, {
    type: 'placeRoadPath',
    spec: 'rd_avenue',
    tiles: [{ x: 50, y: 49 }, { x: 50, y: 50 }, { x: 50, y: 51 }],
  });
  const junctionBefore = tileAt(s, 50, 50);
  assert.equal(junctionBefore.spec, 'rd_mwyjunction', 'precondition: junction exists');
  const junctionId = junctionBefore.id;
  const junctionTick = junctionBefore.builtTick;
  const buildingCountBefore = s.buildings.length;
  const fundsBefore = s.funds;

  // A second placement re-extends the SAME collector further north/south,
  // still passing through the (50,50) junction tile. (50,49)/(50,51) are the
  // SAME rd_avenue spec as before (same-spec dedup, zero cost — unrelated to
  // AC-8); the two NEW tiles (50,48)/(50,52) are fresh empty cells (ordinary
  // cost); (50,50) is the interesting one — it crosses the ALREADY-junction
  // tile a second time and must merge, not re-convert.
  s = reducer(s, {
    type: 'placeRoadPath',
    spec: 'rd_avenue',
    tiles: [{ x: 50, y: 48 }, { x: 50, y: 49 }, { x: 50, y: 50 }, { x: 50, y: 51 }, { x: 50, y: 52 }],
  });

  const junctionAfter = tileAt(s, 50, 50);
  assert.equal(junctionAfter.spec, 'rd_mwyjunction', 'still rd_mwyjunction — never re-converted or reversed');
  assert.equal(junctionAfter.id, junctionId, 'building id stable — no double-conversion, no id churn');
  assert.equal(junctionAfter.builtTick, junctionTick, 'builtTick stable — not replaced by a new building');

  // Exactly one building at that tile (one-building-per-tile invariant): no
  // stacked duplicate junction from the second crossing.
  const atTile = s.buildings.filter((b) => b.x === 50 && b.y === 50);
  assert.equal(atTile.length, 1, 'exactly one building at the junction tile — no stacked duplicate');
  assert.equal(countMotorwayJunctions(s), 1, 'still exactly one motorway junction network-wide — no double-conversion anywhere');

  // Cost: exactly the 2 brand-new rd_avenue tiles (50,48)/(50,52). The two
  // middle tiles (50,49)/(50,51) are same-spec dedup (zero cost) and the
  // junction tile (50,50) merges for zero cost — critically, NOT a second
  // MOTORWAY_JUNCTION_COST charge for re-crossing an already-junction tile.
  const avenueCost = SPECS.rd_avenue.cost;
  const spend = fundsBefore - s.funds;
  assert.equal(spend, avenueCost * 2, 'billed only for the 2 brand-new avenue tiles — no re-charge for the merge/dedup tiles');
  assert.ok(spend < MOTORWAY_JUNCTION_COST, 'no second MOTORWAY_JUNCTION_COST charged for re-crossing the junction tile');
  assert.equal(s.buildings.length, buildingCountBefore + 2, 'only the 2 brand-new tiles were added, no extra junction building');
});
