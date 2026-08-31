// road-replan-inc1.test.mjs — FEAT-1972079928 inc1: road re-planning on placement.
//
// Aaron's rulings (BOW comment, 2026-08-31) are AUTHORITATIVE over the BA draft:
//   (1) demolition/reroute of existing roads is fully autonomous, no confirmation.
//   (2) the WHOLE cascade over the affected region is computed to completion as
//       ONE atomic transaction BEFORE any tile is redrawn/committed — never
//       interleave compute and redraw (the load-bearing correctness AC).
//   (3) hierarchy is a STRONG preference: routing cost meaningfully favours
//       higher tiers even at extra distance (not a tie-breaker only).
//   (4) an in-cascade upgrade costs REPLAN_UPGRADE_COST_FRACTION (90%) of the
//       new tier's full build cost, through the ledger, atomic with placement.
//
// The whole cascade is folded into the SAME placeRoadPath reducer case (no new
// Action/journal type), so replay-byte-identical falls out of it being a pure
// function of state — the existing placeRoadPath replay tests already cover
// that mechanism; this file adds the re-plan-specific determinism/atomicity/
// hierarchy proofs on top.
//
// RED proof (scratch cp/mv, NEVER git — see report for the three performed):
//   1) Comment out the `replanPlan.totalCost <= placedState.funds` affordability
//      guard (force-apply even when unaffordable) → the atomicity test goes RED
//      (funds go negative / avenue tiles upgrade despite insufficient funds).
//   2) Flip the hierarchy discount formula's sign (make LOWER tiers cheaper to
//      reuse instead of higher) → the strong-hierarchy test goes RED (the
//      direct all-empty route is chosen instead of the longer avenue route).
//   3) Change REPLAN_RADIUS_TILES's use in replanBBox to ignore `radius` (fixed
//      window) → the AC-1 boundary test goes RED (the tile just past a radius=1
//      call is no longer excluded).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  initialState,
  reducer,
  replanSearch,
  replanBBox,
  REPLAN_RADIUS_TILES,
  REPLAN_UPGRADE_COST_FRACTION,
} from '../src/sim/engine.ts';
import { SPECS } from '../src/sim/data.ts';

// Build a clean board (no starter city) with an explicit building list.
function board(buildings) {
  const base = initialState();
  let maxId = 0;
  for (const b of buildings) if (b.id > maxId) maxId = b.id;
  return { ...base, unlockedAll: true, buildings, nextId: maxId + 1, placeNotice: null };
}

const tileAt = (s, x, y) => s.buildings.find((b) => b.x === x && b.y === y);
const laneAt = (x, y, id) => ({ id, spec: 'road', x, y, builtTick: -1000 });
const avenueAt = (x, y, id) => ({ id, spec: 'rd_avenue', x, y, builtTick: -1000 });

// ════════════════════════════════════════════════════════════════════════════
// AC-1: radius + bounding-box nearby-road detection.
// ════════════════════════════════════════════════════════════════════════════

test('AC-1: replanSearch finds roads within radius of the new tiles bbox, excludes outside/self', () => {
  const newTiles = [{ x: 50, y: 50 }];
  const radius = 3;

  const inside = laneAt(53, 50, 1); // exactly on the radius edge (50+3)
  const outside = laneAt(54, 50, 2); // one tile past the radius
  const selfTile = laneAt(50, 50, 3); // part of newTiles itself — must be excluded

  const s = board([inside, outside, selfTile]);
  const found = replanSearch(s, newTiles, radius);
  const foundKeys = found.map((r) => `${r.x},${r.y}`);

  assert.ok(foundKeys.includes('53,50'), 'tile exactly on the radius edge IS nearby');
  assert.ok(!foundKeys.includes('54,50'), 'tile one past the radius is NOT nearby');
  assert.ok(!foundKeys.includes('50,50'), 'a newTiles tile itself is excluded from its own search');
});

test('AC-1 mutation (radius=0 argument, functional RED-equivalent): no nearby roads found', () => {
  const newTiles = [{ x: 50, y: 50 }];
  const nearRoad = laneAt(51, 50, 1);
  const s = board([nearRoad]);

  // radius > 0 finds it...
  assert.equal(replanSearch(s, newTiles, 3).length, 1, 'precondition: found at radius 3');
  // ...radius 0 (equivalent to setting REPLAN_RADIUS_TILES = 0) finds nothing.
  assert.equal(replanSearch(s, newTiles, 0).length, 0, 'radius 0 finds no nearby roads (mutation goes RED without this)');
});

test('AC-1: replanBBox clamps to map bounds and expands by radius on every side', () => {
  const box = replanBBox([{ x: 5, y: 5 }, { x: 8, y: 5 }], 4);
  assert.deepEqual(box.lo, { x: 1, y: 1 }, 'lo expanded by radius (clamped at 0 not hit here)');
  assert.deepEqual(box.hi, { x: 12, y: 9 }, 'hi expanded by radius from the bbox max');
});

test('precondition: the placeholder-balance constants this file assumes', () => {
  assert.equal(REPLAN_RADIUS_TILES, 4, 'AC-1 radius placeholder');
  assert.equal(REPLAN_UPGRADE_COST_FRACTION, 0.9, "Aaron ruling (4): 90% upgrade cost");
});

// ════════════════════════════════════════════════════════════════════════════
// AC-3 (strong hierarchy, Aaron's ruling supersedes the BA's soft-preference
// draft): a longer route through existing higher-tier tiles is preferred over
// a SHORTER route through empty/local tiles. Proven observably: the chosen
// route's cells get upgraded to the new tier; the unchosen shorter route's
// cells are untouched (no new tiles placed there).
// ════════════════════════════════════════════════════════════════════════════

function hierarchyScenario(funds) {
  // Candidate stranded local road, 4 tiles west of where the new tile lands.
  const candidate = laneAt(46, 50, 1);
  // An existing AVENUE (tier 2) line one row north, connecting the candidate's
  // column all the way to the new tile's column — a LONGER route (5 hops) but
  // entirely reused higher-tier tiles vs the direct 3-hop all-empty route.
  const avenueLine = [
    avenueAt(46, 49, 2),
    avenueAt(47, 49, 3),
    avenueAt(48, 49, 4),
    avenueAt(49, 49, 5),
    avenueAt(50, 49, 6),
  ];
  const s = board([candidate, ...avenueLine]);
  return { ...s, funds };
}

test('AC-3 strong hierarchy: a 5-hop route via existing avenue tiles beats the shorter 3-hop empty route', () => {
  const s = hierarchyScenario(1_000_000);
  const after = reducer(s, { type: 'placeRoadPath', spec: 'rd_aroad', tiles: [{ x: 50, y: 50 }] });

  // The chosen (longer, avenue-reuse) route's tiles are all upgraded to rd_aroad (tier 3).
  for (const [x, y] of [[46, 49], [47, 49], [48, 49], [49, 49], [50, 49]]) {
    const t = tileAt(after, x, y);
    assert.ok(t, `avenue-route tile (${x},${y}) still exists`);
    assert.equal(t.spec, 'rd_aroad', `avenue-route tile (${x},${y}) upgraded to the new tier`);
  }

  // The shorter, unchosen direct route (empty cells) got NOTHING placed on it.
  for (const [x, y] of [[47, 50], [48, 50], [49, 50]]) {
    assert.equal(tileAt(after, x, y), undefined, `direct-route empty cell (${x},${y}) untouched — not the chosen route`);
  }

  // AC-7 non-destructive default: the candidate's OWN tile is never touched.
  const cand = tileAt(after, 46, 50);
  assert.equal(cand.id, 1, "candidate's building id preserved (not demolished/replaced)");
  assert.equal(cand.spec, 'road', "candidate's own tile stays tier 1 (re-plan only touches the connecting cells)");

  // AC-8: the avenue tiles' ids are preserved (upgrade in place, not new buildings).
  const upgraded49 = tileAt(after, 49, 49);
  assert.equal(upgraded49.id, 5, 'upgraded tile keeps its original building id');
});

test('AC-3 upgrade cost: 5 upgraded tiles charged at 90% of the new tier full cost, through the ledger', () => {
  const s = hierarchyScenario(1_000_000);
  const before = s.funds;
  const after = reducer(s, { type: 'placeRoadPath', spec: 'rd_aroad', tiles: [{ x: 50, y: 50 }] });

  const aroadCost = SPECS.rd_aroad.cost; // 180
  const perTileUpgrade = Math.round(aroadCost * REPLAN_UPGRADE_COST_FRACTION); // 162
  const replanCost = perTileUpgrade * 5; // 810
  const placementCost = aroadCost; // the single new tile at (50,50), full cost
  const totalCost = placementCost + replanCost;

  assert.equal(before - after.funds, totalCost, `total spend = placement (${placementCost}) + 5×90% upgrades (${replanCost})`);

  const replanEntry = after.ledger.find((e) => e.label.startsWith('Re-planned'));
  assert.ok(replanEntry, 'a "Re-planned roads …" ledger entry was recorded');
  assert.equal(replanEntry.amount, -replanCost, 'ledger books exactly the 90%-fraction upgrade cost, not full price');
});

// ════════════════════════════════════════════════════════════════════════════
// AC-6: atomic, all-or-nothing funds for the re-plan cascade (separate from —
// and on top of — the original placement's own all-or-nothing check).
// ════════════════════════════════════════════════════════════════════════════

test('AC-6 atomicity RED proof: unaffordable re-plan changes NOTHING (funds unchanged, no upgrades)', () => {
  const aroadCost = SPECS.rd_aroad.cost;
  const perTileUpgrade = Math.round(aroadCost * REPLAN_UPGRADE_COST_FRACTION);
  const replanCost = perTileUpgrade * 5;
  const totalNeeded = aroadCost + replanCost;

  // Funds cover the ORIGINAL placement but fall exactly £1 short of the re-plan.
  const s = hierarchyScenario(totalNeeded - 1);
  const after = reducer(s, { type: 'placeRoadPath', spec: 'rd_aroad', tiles: [{ x: 50, y: 50 }] });

  // The original placement still succeeded (it was affordable on its own).
  assert.equal(tileAt(after, 50, 50).spec, 'rd_aroad', 'the original tile still placed (independent all-or-nothing gate)');

  // But NONE of the re-plan's upgrades applied — the whole cascade rolled back.
  for (const [x, y] of [[46, 49], [47, 49], [48, 49], [49, 49], [50, 49]]) {
    assert.equal(tileAt(after, x, y).spec, 'rd_avenue', `(${x},${y}) NOT upgraded — re-plan is all-or-nothing`);
  }
  assert.equal(after.funds, s.funds - aroadCost, 'funds reduced ONLY by the original placement, not a partial re-plan spend');
  assert.ok(!after.ledger.some((e) => e.label.startsWith('Re-planned')), 'no ghost "Re-planned" ledger entry when unaffordable');
});

test('AC-6 edge case: funds exactly equal to placement+replan total → both apply, funds hit zero', () => {
  const aroadCost = SPECS.rd_aroad.cost;
  const perTileUpgrade = Math.round(aroadCost * REPLAN_UPGRADE_COST_FRACTION);
  const replanCost = perTileUpgrade * 5;
  const totalNeeded = aroadCost + replanCost;

  const s = hierarchyScenario(totalNeeded);
  const after = reducer(s, { type: 'placeRoadPath', spec: 'rd_aroad', tiles: [{ x: 50, y: 50 }] });

  assert.equal(after.funds, 0, 'funds reduced to exactly zero');
  assert.equal(tileAt(after, 49, 49).spec, 'rd_aroad', 'the re-plan applied at exact affordability');
});

// ════════════════════════════════════════════════════════════════════════════
// AC-2 / AC-6 determinism: two independent runs of the identical scenario
// produce byte-identical buildings/funds/ledger (the re-plan is a pure function
// of state — no Date/Math.random anywhere in the cascade).
// ════════════════════════════════════════════════════════════════════════════

test('AC-2 determinism: two independent runs of the identical re-plan scenario are byte-identical', () => {
  const action = { type: 'placeRoadPath', spec: 'rd_aroad', tiles: [{ x: 50, y: 50 }] };
  const a = reducer(hierarchyScenario(1_000_000), action);
  const b = reducer(hierarchyScenario(1_000_000), action);

  const fingerprint = (s) =>
    JSON.stringify({
      buildings: [...s.buildings].sort((x, y) => x.id - y.id),
      funds: s.funds,
      ledger: s.ledger,
    });

  assert.equal(fingerprint(a), fingerprint(b), 'two independent runs produce byte-identical state');

  // Not a vacuous pass — something actually changed vs. the pre-action board.
  assert.notEqual(fingerprint(a), fingerprint(hierarchyScenario(1_000_000)), 'the re-plan genuinely mutated state');
});

test('replay: re-applying the same placeRoadPath action to a fresh identical board reproduces the re-plan byte-identically', () => {
  // Mirrors road-path-action.test.mjs's replay-determinism pattern: the whole
  // cascade lives inside ONE journaled action, so replaying that action from
  // genesis on a fresh board is the replay proof (no separate 'replanRoads'
  // journal entry to keep in lockstep — nothing to desync).
  const action = { type: 'placeRoadPath', spec: 'rd_aroad', tiles: [{ x: 50, y: 50 }] };
  const s1 = reducer(hierarchyScenario(1_000_000), action);
  const s2 = reducer(hierarchyScenario(1_000_000), action);

  assert.equal(s1.buildings.length, s2.buildings.length, 'same building count on replay');
  assert.equal(s1.funds, s2.funds, 'same funds on replay');
  assert.deepEqual(
    s1.buildings.map((b) => [b.id, b.spec, b.x, b.y]).sort(),
    s2.buildings.map((b) => [b.id, b.spec, b.x, b.y]).sort(),
    'identical building set (ids, specs, positions) on replay'
  );
});

// ════════════════════════════════════════════════════════════════════════════
// AC-8: no double-conversion / no conflict with the landed auto-junction
// convert-in-place logic (FEAT-1972079910 inc2/3).
// ════════════════════════════════════════════════════════════════════════════

test('AC-8: re-plan never re-converts an already-auto-junction tile', () => {
  // A roundabout (auto-placed junction spec) sits on the candidate's only route
  // to the new path. The re-plan must treat it as a normal (if non-upgradable)
  // passable tile and must NOT re-convert it or touch its id.
  const candidate = laneAt(46, 50, 1);
  const roundabout = { id: 2, spec: 'rd_roundabout', x: 48, y: 50, builtTick: -1000 };
  let s = board([candidate, roundabout]);
  s = { ...s, funds: 1_000_000 };

  const after = reducer(s, { type: 'placeRoadPath', spec: 'rd_aroad', tiles: [{ x: 50, y: 50 }] });

  const rb = tileAt(after, 48, 50);
  assert.ok(rb, 'roundabout tile still present');
  assert.equal(rb.id, 2, 'roundabout id unchanged (never re-converted)');
  assert.equal(rb.spec, 'rd_roundabout', 'roundabout spec unchanged — re-plan does not touch auto-junction specs');
});

// ════════════════════════════════════════════════════════════════════════════
// No nearby roads → no cascade at all (existing placeRoadPath behaviour is
// completely unaffected when there is nothing around to re-plan).
// ════════════════════════════════════════════════════════════════════════════

test('no-op: an isolated placement with no nearby roads triggers no re-plan cascade', () => {
  const s = board([]);
  const after = reducer({ ...s, funds: 100_000 }, { type: 'placeRoadPath', spec: 'road', tiles: [{ x: 50, y: 50 }] });
  assert.equal(after.buildings.length, 1, 'only the placed tile exists');
  assert.ok(!after.ledger.some((e) => e.label.startsWith('Re-planned')), 'no re-plan ledger entry when nothing nearby');
});
