// road-replan-reroute.test.mjs — FEAT-1972079928 inc1 EXTENSION: fully autonomous
// demolish/reroute of EXISTING roads (Aaron ruling, BOW comment 2026-08-31 11:02,
// "PULL DEMOLISH-REROUTE INTO INC1 NOW").
//
// inc1 (dda11f7) only ever reused/upgraded existing road tiles or laid new ones on
// empty ground — it never removed/relocated an existing road segment. This file
// covers the extension: planRoadReplanCascade now also identifies existing
// SUBOPTIMAL "through" road tiles that this SAME atomic cascade proves redundant
// (bypassed by a surviving alternate path) and demolishes them, still inside the
// ONE compute-before-redraw transaction (no interleaving, no partial spend).
//
// Scenario shape used throughout: a small 4-tile "ring" of tier-1 local roads —
//   (60,50)-(61,50)
//     |         |
//   (60,51)-(61,51)
// walled off from the rest of the map by 8 `pylon` tiles (infrastructure, exempt
// from road-access — CONNECT_EXEMPT_KINDS — so they can never themselves block a
// demolition, and they are placed purely so replanSearch/Dijkstra cannot reach
// INTO the ring from the new placement — the ring's own redundancy is then
// completely decoupled from the connect+upgrade pass's own path selection,
// isolating exactly the new demolish/reroute mechanism this file tests).
// Removing any ONE tile from a 4-cycle still leaves the other 3 connected in an
// open path — that IS the "a better alignment already exists" proof: the ring is
// pure sprawl (a closed loop where a spanning path already connects everything).
//
// RED proof (scratch cp/mv, NEVER git — see report for the three performed):
//   1) Skip the whole demolition pass (early `continue`/return before check 1) →
//      the atomic-demolition test goes RED (ring stays at 4 tiles, no refund).
//   2) Remove the check-4 BFS redundancy requirement (treat every degree-2 tile
//      as demolishable unconditionally) → the no-fragmentation invariant breaks:
//      a SECOND ring tile also gets removed in the same pass even though it is
//      no longer safe, which the "exactly one demolition" assertion catches RED.
//   3) Remove the check-5 no-stranding building check → the no-stranding test
//      goes RED (the res_hut's sole road-adjacent tile gets demolished from
//      under it).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import { SPECS } from '../src/sim/data.ts';

function board(buildings) {
  const base = initialState();
  let maxId = 0;
  for (const b of buildings) if (b.id > maxId) maxId = b.id;
  return { ...base, unlockedAll: true, buildings, nextId: maxId + 1, placeNotice: null };
}

const tileAt = (s, x, y) => s.buildings.find((b) => b.x === x && b.y === y);
const roadAt = (x, y, id) => ({ id, spec: 'road', x, y, builtTick: -1000 });
const pylonAt = (x, y, id) => ({ id, spec: 'pylon', x, y, builtTick: -1000 });
const hutAt = (x, y, id) => ({ id, spec: 'res_hut', x, y, builtTick: -1000 });

// The 4-tile ring (ids 1-4) + the 8-tile wall (ids 5-12, pylon by default, or
// with a `res_hut` swapped in for one wall tile when `strandTest` is set).
function ringBuildings({ strandTest = false } = {}) {
  const ring = [roadAt(60, 50, 1), roadAt(61, 50, 2), roadAt(61, 51, 3), roadAt(60, 51, 4)];
  const wallSpots = [
    [59, 50], [60, 49], [62, 50], [61, 49],
    [62, 51], [61, 52], [59, 51], [60, 52],
  ];
  const wall = wallSpots.map(([x, y], i) => {
    const id = 5 + i;
    // (60,49) sits directly north of ring tile (60,50) — swap in a road-access-
    // requiring res_hut there for the no-stranding scenario.
    if (strandTest && x === 60 && y === 49) return hutAt(x, y, id);
    return pylonAt(x, y, id);
  });
  return [...ring, ...wall];
}

const NEW_TILE = { x: 63, y: 53 };
const RD_AVENUE_COST = SPECS.rd_avenue.cost; // 90
const ROAD_REFUND = Math.round(SPECS.road.cost * 0.25); // 10 (bulldoze convention)

// ════════════════════════════════════════════════════════════════════════════
// Core: a placement far enough away to trigger the cascade, but geometrically
// walled off from the ring, demolishes exactly ONE now-redundant ring tile.
// ════════════════════════════════════════════════════════════════════════════

test('demolish/reroute: a provably-redundant ring tile is torn down, the rest of the ring survives', () => {
  const s = { ...board(ringBuildings()), funds: 1_000_000 };
  const after = reducer(s, { type: 'placeRoadPath', spec: 'rd_avenue', tiles: [NEW_TILE] });

  const ringCoords = [[60, 50], [61, 50], [61, 51], [60, 51]];
  const survivors = ringCoords.filter(([x, y]) => tileAt(after, x, y));
  assert.equal(survivors.length, 3, 'exactly ONE ring tile was demolished, three survive');

  // The surviving 3 ring tiles must still all be present with their ORIGINAL
  // ids/spec (never re-created, never upgraded — a plain demolition+refund,
  // not a demolish-then-rebuild).
  for (const [x, y] of survivors) {
    const t = tileAt(after, x, y);
    assert.equal(t.spec, 'road', `surviving ring tile (${x},${y}) is untouched tier-1 road`);
  }

  const replanEntry = after.ledger.find((e) => e.label.startsWith('Re-planned'));
  assert.ok(replanEntry, 'a "Re-planned roads …" ledger entry was recorded');
  assert.match(replanEntry.label, /1 demolished/, 'ledger label reports exactly 1 demolition');

  // Net cost = original placement (full price) MINUS the demolition refund —
  // no new tiles/upgrades were needed for this scenario, so the re-plan entry
  // itself books ONLY the refund (a negative cost = a credit).
  assert.equal(replanEntry.amount, ROAD_REFUND, 're-plan ledger entry books the refund as a credit');
  assert.equal(1_000_000 - after.funds, RD_AVENUE_COST - ROAD_REFUND, 'net spend = placement cost minus demolition refund');
});

test('demolish/reroute: no non-road demolition — all 8 pylon walls survive untouched', () => {
  const s = { ...board(ringBuildings()), funds: 1_000_000 };
  const after = reducer(s, { type: 'placeRoadPath', spec: 'rd_avenue', tiles: [NEW_TILE] });

  const pylons = after.buildings.filter((b) => b.spec === 'pylon');
  assert.equal(pylons.length, 8, 'all 8 pylon walls survive — the cascade never demolishes a non-road building');
  for (let i = 0; i < 8; i++) {
    assert.ok(pylons.some((p) => p.id === 5 + i), `pylon id ${5 + i} preserved`);
  }
});

// ════════════════════════════════════════════════════════════════════════════
// No-stranding: a building whose ONLY road-adjacent tile is the redundant ring
// candidate blocks its demolition — a DIFFERENT ring tile is demolished instead.
// ════════════════════════════════════════════════════════════════════════════

test('no-stranding: a building relying on the candidate tile as its ONLY road access blocks its demolition', () => {
  const s = { ...board(ringBuildings({ strandTest: true })), funds: 1_000_000 };
  const after = reducer(s, { type: 'placeRoadPath', spec: 'rd_avenue', tiles: [NEW_TILE] });

  // (60,50) is now protected — the res_hut at (60,49) depends on it exclusively.
  const protectedTile = tileAt(after, 60, 50);
  assert.ok(protectedTile, '(60,50) was NOT demolished — a building depends on it');
  assert.equal(protectedTile.id, 1, "protected candidate's id unchanged");
  assert.equal(protectedTile.spec, 'road', "protected candidate's spec unchanged");

  // The res_hut itself is untouched (never demolished — only roads are ever candidates).
  const hut = after.buildings.find((b) => b.spec === 'res_hut');
  assert.ok(hut, 'the res_hut survives');
  assert.equal(hut.x, 60);
  assert.equal(hut.y, 49);

  // A ring tile WAS still demolished overall (some other, safe tile) — the
  // no-stranding rule blocks ONE specific candidate, it does not disable the
  // whole pass.
  const ringCoords = [[60, 50], [61, 50], [61, 51], [60, 51]];
  const survivors = ringCoords.filter(([x, y]) => tileAt(after, x, y));
  assert.equal(survivors.length, 3, 'exactly one (different) ring tile was demolished');
});

// ════════════════════════════════════════════════════════════════════════════
// Atomicity: demolitions are inside the SAME all-or-nothing gate as the
// connect+upgrade additions. An unaffordable NET cost rolls back BOTH.
// ════════════════════════════════════════════════════════════════════════════

function atomicityScenario(funds) {
  const stranded = roadAt(63, 55, 13); // 2 tiles south of the new tile: forces one new £90 connector tile
  const s = board([...ringBuildings(), stranded]);
  return { ...s, funds };
}

test('atomicity: unaffordable net re-plan (addition minus refund) changes NOTHING — no partial demolish, no partial build', () => {
  const totalNeeded = RD_AVENUE_COST /* original placement */ + (RD_AVENUE_COST - ROAD_REFUND) /* net re-plan */;
  const s = atomicityScenario(totalNeeded - 1); // exactly £1 short of the re-plan's NET cost
  const after = reducer(s, { type: 'placeRoadPath', spec: 'rd_avenue', tiles: [NEW_TILE] });

  // Original placement stood (affordable on its own, independent gate).
  assert.equal(tileAt(after, 63, 53).spec, 'rd_avenue', 'the original tile still placed');

  // Re-plan rolled back COMPLETELY: no new connector tile, ring fully intact.
  assert.equal(tileAt(after, 63, 54), undefined, 'the £90 connector tile was NOT placed — re-plan is all-or-nothing');
  const ringCoords = [[60, 50], [61, 50], [61, 51], [60, 51]];
  for (const [x, y] of ringCoords) {
    assert.ok(tileAt(after, x, y), `ring tile (${x},${y}) NOT demolished — the whole net-cost cascade rolled back`);
  }
  assert.equal(after.funds, s.funds - RD_AVENUE_COST, 'funds reduced ONLY by the original placement');
  assert.ok(!after.ledger.some((e) => e.label.startsWith('Re-planned')), 'no ghost "Re-planned" ledger entry when the net cost is unaffordable');
});

test('atomicity edge case: funds exactly cover placement + net re-plan cost — both the new tile AND the demolition apply', () => {
  const totalNeeded = RD_AVENUE_COST + (RD_AVENUE_COST - ROAD_REFUND);
  const s = atomicityScenario(totalNeeded);
  const after = reducer(s, { type: 'placeRoadPath', spec: 'rd_avenue', tiles: [NEW_TILE] });

  assert.equal(after.funds, 0, 'funds reduced to exactly zero');
  assert.equal(tileAt(after, 63, 54).spec, 'rd_avenue', 'the £90 connector tile WAS placed');
  const ringCoords = [[60, 50], [61, 50], [61, 51], [60, 51]];
  const survivors = ringCoords.filter(([x, y]) => tileAt(after, x, y));
  assert.equal(survivors.length, 3, 'exactly one ring tile demolished alongside the new connector');
});

// ════════════════════════════════════════════════════════════════════════════
// Determinism: two independent runs of the identical demolish scenario are
// byte-identical (pure function of state — no Date/Math.random anywhere).
// ════════════════════════════════════════════════════════════════════════════

test('determinism: two independent runs of the identical demolish scenario are byte-identical', () => {
  const action = { type: 'placeRoadPath', spec: 'rd_avenue', tiles: [NEW_TILE] };
  const scenario = () => ({ ...board(ringBuildings()), funds: 1_000_000 });
  const a = reducer(scenario(), action);
  const b = reducer(scenario(), action);

  const fingerprint = (s) =>
    JSON.stringify({
      buildings: [...s.buildings].sort((x, y) => x.id - y.id),
      funds: s.funds,
      ledger: s.ledger,
    });

  assert.equal(fingerprint(a), fingerprint(b), 'two independent runs produce byte-identical state');
  assert.notEqual(fingerprint(a), fingerprint(scenario()), 'the re-plan genuinely demolished something (not a vacuous pass)');
});

test('replay: re-applying the same placeRoadPath action to a fresh identical board reproduces the demolition byte-identically', () => {
  const action = { type: 'placeRoadPath', spec: 'rd_avenue', tiles: [NEW_TILE] };
  const scenario = () => ({ ...board(ringBuildings()), funds: 1_000_000 });
  const s1 = reducer(scenario(), action);
  const s2 = reducer(scenario(), action);

  assert.equal(s1.buildings.length, s2.buildings.length, 'same building count on replay');
  assert.equal(s1.funds, s2.funds, 'same funds on replay');
  assert.deepEqual(
    s1.buildings.map((b) => [b.id, b.spec, b.x, b.y]).sort(),
    s2.buildings.map((b) => [b.id, b.spec, b.x, b.y]).sort(),
    'identical building set (ids, specs, positions) on replay'
  );
});

// ════════════════════════════════════════════════════════════════════════════
// No infinite loop / no thrash: a single placement produces exactly ONE
// re-plan pass (a bounded linear scan, never a fixed-point/re-triggering loop).
// ════════════════════════════════════════════════════════════════════════════

test('no thrash: a single placement never produces more than one "Re-planned" ledger entry', () => {
  const s = { ...board(ringBuildings()), funds: 1_000_000 };
  const after = reducer(s, { type: 'placeRoadPath', spec: 'rd_avenue', tiles: [NEW_TILE] });
  const replanEntries = after.ledger.filter((e) => e.label.startsWith('Re-planned'));
  assert.equal(replanEntries.length, 1, 'exactly one re-plan ledger entry — no repeated/cascading re-triggering');
});
