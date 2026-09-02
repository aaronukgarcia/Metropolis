// BUG b2d31bc7 — UI-latency quick wins (FIX 1/2/3/5) regression tests.
//
// Aaron's P1: at 68K pop, placement lags ~5s behind the mouse; dragging 10
// estates places ~3. A read-only profiler traced this to reducer(place)'s
// several unmemoised O(buildings) passes fired once PER pointer-move during
// a drag. This file proves each of the three engine-level fixes independently:
//   (a) placeMany batches a whole drag into ONE atomic dispatch, affordability-
//       capped with a "placed X of Y" notice.
//   (b) occupiedSet() is memoised per buildings-array reference (FIX 1).
//   (c) the reducer wrapper skips computeRoadConnectivity for a placement that
//       provably cannot have touched the road graph, but still recomputes for
//       one that can (FIX 2).
// Every assertion here is a RED/GREEN proof — see the comment above each
// test for exactly how to revert the corresponding fix and watch it redden.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import { recordAction, isStateAffecting } from '../src/sim/journal.ts';
import { SPECS, occupiedSet, computeRoadConnectivity, demandFixPlan } from '../src/sim/data.ts';

/** Mirrors test/demand-fix.test.mjs's shortfallState(): a real population, no
 *  service buildings, all specs unlocked, ample treasury — guarantees a
 *  multi-unit demand-fix plan for the BUG-566 resolveDemand regression below. */
function shortfallState(population, fundsOverride = 1_000_000_000) {
  const base = initialState();
  return { ...base, population, unlockedAll: true, funds: fundsOverride, administrationState: null };
}

// Build a clean, fully-unlocked board for testing — mirrors road-path-action.test.mjs.
function board(buildings, extra = {}) {
  const base = initialState();
  let maxId = 0;
  for (const b of buildings) if (b.id > maxId) maxId = b.id;
  return { ...base, unlockedAll: true, buildings, nextId: maxId + 1, ...extra };
}

// A cheap 1x1 residential spec — trivial tile math, no footprint overlap risk
// when laying out a row, and NOT a road/trunk spec (isRoadOrTrunkSpec(false)),
// which is exactly the case FIX 2 targets. `res_hut` is a free ZONE (category
// 'zones' -> placementCost() === 0 regardless of funds), which is perfect for
// the "places all N" / "skips an occupied tile" tests but useless for an
// AFFORDABILITY test, so that one uses a real paid 1x1 service spec instead.
const HUT = 'res_hut';
const PAID_SPEC = 'hea_clinic'; // 1x1 health service, category 'services', real cost.
const PAID_COST = SPECS[PAID_SPEC].cost;

// A real road spec, to exercise the "DOES recompute" arm of FIX 2/test (c).
const ROAD = 'road';

// ---------------------------------------------------------------------------
// (a) placeMany: one dispatch places every valid tile; affordability-capped.
// ---------------------------------------------------------------------------

test('FEAT/BUG-b2d31bc7 FIX 3: placeMany places ALL N valid tiles in one dispatch', () => {
  const s = board([], { funds: 100_000_000 });
  const tiles = [];
  for (let i = 0; i < 10; i++) tiles.push({ x: 10 + i, y: 10 });

  assert.equal(isStateAffecting({ type: 'placeMany', spec: HUT, tiles }), true,
    'placeMany must be journaled (GR#21 replay-safety)');

  const after = reducer(s, { type: 'placeMany', spec: HUT, tiles });
  const placedCount = after.buildings.filter((b) => b.spec === HUT).length;

  // RED PROOF: before FIX 3 there was no 'placeMany' action at all — the
  // reducer's default branch would return `state` unchanged, so this would
  // assert 10 === 0 and fail loudly. Reverting the new 'placeMany' case in
  // engine.ts's reduceCore() (leaving the Action union member alone) makes
  // this redden the same way: reducer(s, {type:'placeMany',...}) === s.
  assert.equal(placedCount, 10, 'a 10-tile drag places all 10, not fewer (the reported "10 estates, 3 land" bug)');
  assert.equal(after.buildings.length, 10, 'exactly one journal-visible mutation added exactly 10 buildings');
  assert.equal(after.placeNotice, null, 'a fully-successful batch clears any placement notice');
});

test('FEAT/BUG-b2d31bc7 FIX 3: placeMany is affordability-capped with a "placed X of Y" notice', () => {
  // Fund exactly enough for 3 clinics, attempt 10 — hea_clinic is a real PAID
  // spec (not a free zone like res_hut), so this actually exercises the
  // funds-run-out-mid-batch path.
  const s = board([], { funds: PAID_COST * 3 });
  const tiles = [];
  for (let i = 0; i < 10; i++) tiles.push({ x: 10 + i, y: 20 });

  const after = reducer(s, { type: 'placeMany', spec: PAID_SPEC, tiles });
  const placedCount = after.buildings.filter((b) => b.spec === PAID_SPEC).length;

  // RED PROOF: an implementation that ignores affordability mid-batch (or
  // that doesn't stop the loop) would either place 10 anyway (funds go
  // negative — reddens the `placedCount < 10` and `funds >= 0` assertions)
  // or throw. Reverting the `if (cost > 0 && cur.funds < cost) break;` guard
  // inside the placeMany case reproduces exactly that.
  assert.ok(placedCount < 10, 'stops before placing more than affordable');
  assert.equal(placedCount, 3, 'places exactly as many as funds allow');
  assert.ok(after.funds >= 0, 'never overspends into negative funds');
  assert.ok(after.placeNotice, 'a short batch surfaces a notice (never a silent partial no-op)');
  assert.match(after.placeNotice, /Placed 3 of 10/, 'notice reports placed-vs-attempted, mirroring resolveDemand');
});

test('FEAT/BUG-b2d31bc7 FIX 3: a single-tile placeMany still places one (click-through-batch parity)', () => {
  const s = board([], { funds: 100_000_000 });
  const after = reducer(s, { type: 'placeMany', spec: HUT, tiles: [{ x: 15, y: 15 }] });
  assert.equal(after.buildings.filter((b) => b.spec === HUT).length, 1, 'a 1-tile "drag" still places one');
});

test('FEAT/BUG-b2d31bc7 FIX 3: placeMany skips a tile that no longer fits, keeps trying the rest', () => {
  const s = board([], { funds: 100_000_000 });
  // Same tile twice (a drag revisiting a cell) plus one fresh tile.
  const tiles = [{ x: 30, y: 30 }, { x: 30, y: 30 }, { x: 31, y: 30 }];
  const after = reducer(s, { type: 'placeMany', spec: HUT, tiles });
  assert.equal(after.buildings.filter((b) => b.spec === HUT).length, 2,
    'the repeated tile is skipped once occupied, the distinct tile still places');
});

// ---------------------------------------------------------------------------
// (b) occupiedSet(): memoised per buildings-array reference (FIX 1).
// ---------------------------------------------------------------------------

test('FEAT/BUG-b2d31bc7 FIX 1: occupiedSet returns the SAME Set reference for the same buildings ref', () => {
  const s = board([
    { id: 1, spec: HUT, x: 5, y: 5, builtTick: 0 },
    { id: 2, spec: HUT, x: 6, y: 5, builtTick: 0 },
  ]);

  const first = occupiedSet(s);
  const second = occupiedSet(s); // same s.buildings reference — should hit the WeakMap cache.

  // RED PROOF: pre-FIX-1, occupiedSet built a brand-new `Set` on every call —
  // `first === second` would be false. Reverting data.ts's occupiedSet back
  // to its old body (drop the WeakMap memo, always `buildOccupiedSet` fresh)
  // reddens this immediately.
  assert.equal(first, second, 'occupiedSet is memoised — identical reference for an unchanged buildings array');
  assert.ok(first.has('5,5') && first.has('6,5'), 'the cached set still has correct contents');
});

test('FEAT/BUG-b2d31bc7 FIX 1: occupiedSet returns a FRESH Set when buildings changes', () => {
  const s1 = board([{ id: 1, spec: HUT, x: 5, y: 5, builtTick: 0 }]);
  const set1 = occupiedSet(s1);

  const s2 = { ...s1, buildings: [...s1.buildings, { id: 2, spec: HUT, x: 6, y: 5, builtTick: 0 }] };
  const set2 = occupiedSet(s2);

  assert.notEqual(set1, set2, 'a new buildings array reference must not reuse the stale cached Set');
  assert.ok(!set1.has('6,5'), 'the OLD cached set is untouched by the new building');
  assert.ok(set2.has('6,5'), 'the NEW set reflects the new building');
});

// ---------------------------------------------------------------------------
// (c) reducer wrapper: computeRoadConnectivity gated on road/trunk changes
//     (FIX 2). Proxy for "was it recomputed": roadConnectivity REFERENCE
//     identity — computeRoadConnectivity always returns a brand-new object,
//     so an unchanged reference proves the wrapper skipped the recompute,
//     and a changed reference (with correct contents) proves it ran.
// ---------------------------------------------------------------------------

test('FEAT/BUG-b2d31bc7 FIX 2: a non-road placement does NOT recompute roadConnectivity', () => {
  let s = board([], { funds: 100_000_000 });
  s = { ...s, roadConnectivity: computeRoadConnectivity(s) };
  const before = s.roadConnectivity;

  const after = reducer(s, { type: 'place', spec: HUT, x: 40, y: 40 });

  // RED PROOF: before FIX 2, the wrapper recomputed on EVERY buildings change
  // unconditionally (`if (next.buildings !== s.buildings || !next.roadConnectivity)`).
  // Restoring that unconditional recompute reddens this — `after.roadConnectivity`
  // would be a brand-new object every time, never `=== before`.
  assert.equal(after.roadConnectivity, before,
    'placing a non-road building with no road-adjacency work must not trigger the road BFS');
});

test('FEAT/BUG-b2d31bc7 FIX 2: a road placement DOES recompute roadConnectivity', () => {
  let s = board([], { funds: 100_000_000 });
  s = { ...s, roadConnectivity: computeRoadConnectivity(s) };
  const before = s.roadConnectivity;

  // A road tile at the map edge is a connectivity SEED (data.ts computeRoadConnectivity:
  // map-edge road tiles seed the BFS), so this one is guaranteed CONNECTED —
  // avoids the test depending on any other trunk/motorway being present.
  const after = reducer(s, { type: 'place', spec: ROAD, x: 0, y: 0 });

  // RED PROOF: an over-eager gate that NEVER recomputes (e.g. the flag wired
  // backwards, or defaulting to "skip") would leave `after.roadConnectivity
  // === before` even though a road tile was just added — this assertion
  // catches that directly, and a contents check catches a gate that recomputes
  // but from stale buildings.
  assert.notEqual(after.roadConnectivity, before,
    'placing an actual road tile must still trigger the road BFS');
  assert.ok(after.roadConnectivity.connectedRoadTiles.includes('0,0'),
    'the recomputed graph reflects the just-placed (edge-seeded) road tile');
});

test('FEAT/BUG-b2d31bc7 FIX 2: a non-road placement that triggers autoConnect DOES recompute', () => {
  // A road tile sits a few squares from where the hut will land, well within
  // roadConnect's CONNECT_BUDGET — autoConnect lays a connector (road-spec
  // tiles) to reach it. Even though the PLACED building (the hut) is not
  // itself road/trunk, the road graph DID change, so this must still trigger
  // the wrapper's recompute — this is the case FIX 2's engine.ts comment
  // calls out explicitly: "gate it... be careful X".
  let s = board([{ id: 1, spec: ROAD, x: 58, y: 60, builtTick: 0 }], { funds: 100_000_000 });
  s = { ...s, roadConnectivity: computeRoadConnectivity(s) };
  const before = s.roadConnectivity;

  const after = reducer(s, { type: 'place', spec: HUT, x: 60, y: 60 });

  // Sanity: autoConnect actually laid a connector for this scenario (otherwise
  // the test below would pass vacuously for the wrong reason).
  const roadTileCount = after.buildings.filter((b) => b.spec === ROAD).length;
  assert.ok(roadTileCount > 1, 'precondition: autoConnect actually laid at least one new road tile');

  assert.notEqual(after.roadConnectivity, before,
    'a non-road building whose placement triggers autoConnect must still recompute connectivity');
  const expected = computeRoadConnectivity(after);
  assert.deepEqual(
    [...after.roadConnectivity.connectedRoadTiles].sort(),
    [...expected.connectedRoadTiles].sort(),
    'the recomputed graph matches a from-scratch recompute (never stale)'
  );
});

// ---------------------------------------------------------------------------
// BUG-566 (independent-round REJECT on this estate) — resolveDemand shared
// the SAME FIX-2 recompute-gate hazard as placeMany, but only placeMany got
// the aggregation fix. resolveDemand bulk-places via recursive
// reduceCore('place') too (engine.ts, the 'resolveDemand' case) — each inner
// 'place' call flips the shared module-level `roadTopologyMayHaveChanged`
// flag as a side effect, so without aggregating across the WHOLE batch, only
// the LAST iteration's verdict survives. Proven repro (pop 4000, 'power'
// demand fix, 12 buildings placed incl. connectors): an EARLY pow_wind lays a
// road connector (flag -> true) but the FINAL one doesn't need one (flag ->
// false) — the flag exits the loop false, so the wrapper skips
// computeRoadConnectivity even though the graph changed: 970 connected tiles
// returned vs. 975 from a fresh recompute (5 connector tiles silently
// dropped -> wrongly offline -> wrong jobs/residents/power until the next
// buildings-changing action).
// ---------------------------------------------------------------------------

test('BUG-566: resolveDemand aggregates the road-topology flag across its WHOLE batch, never stale', () => {
  // FEAT-demanddock-overhaul: optimalProvider()'s "1 dam not 20 towers" branch
  // now clears a small power shortfall with ONE big unit (e.g. pop 4,000 used
  // to need several pow_wind turbines; it now resolves to a single
  // pow_windfarm) — that would make this fixture place only 1 building,
  // vacuously satisfying (not exercising) the multi-placement aggregation this
  // test targets. Pop 10,000 on a capped £100M budget excludes every unit
  // whose capacity alone would clear the shortfall in one (windfarm/coal both
  // affordable but both under the 126MW shortfall), so optimalProvider() falls
  // back to the cheapest absolute-cost unit (pow_wind, 8MW) needing 16 units —
  // still a real multi-unit, connector-laying batch.
  let s = shortfallState(10_000, 100_000_000);
  s = { ...s, roadConnectivity: computeRoadConnectivity(s) };
  const before = s.roadConnectivity;

  // Independent round follow-up: the original precondition counted ALL new
  // buildings (power units + any connector tiles), which can pass vacuously
  // even when only ONE power unit was placed (a single unit can still need a
  // multi-tile road connector). Count placements of the PLAN's own spec
  // specifically — that is what "a multi-unit batch" actually means.
  const plan = demandFixPlan(s).find((p) => p.serviceKey === 'power');
  assert.ok(plan && plan.count > 1, 'precondition: the power demand-fix plan itself must call for 2+ units');

  const after = reducer(s, { type: 'resolveDemand', serviceKey: 'power' });

  const placedUnits = after.buildings.filter((b) => b.spec === plan.specId).length -
    s.buildings.filter((b) => b.spec === plan.specId).length;
  assert.ok(placedUnits > 1, `precondition: resolveDemand placed multiple power UNITS (not just connector tiles), got ${placedUnits}`);

  // RED PROOF (verified live during this fix): reverting the aggregation in
  // the 'resolveDemand' case (dropping `anyRoadTopologyChange` back to
  // whatever the LAST inner reduceCore('place') call left the shared flag at)
  // reproduces the exact attacker repro — `after.roadConnectivity` stays
  // reference-EQUAL to `before` (970 connected tiles) even though the batch
  // changed the graph, while a from-scratch recompute reports 975. This
  // assertion catches that directly via reference identity, and the
  // deepEqual below catches the same defect via contents.
  assert.notEqual(after.roadConnectivity, before,
    'a resolveDemand batch that laid at least one connector must trigger a fresh recompute, not reuse the stale one');

  const fresh = computeRoadConnectivity(after);
  assert.deepEqual(
    [...after.roadConnectivity.connectedRoadTiles].sort(),
    [...fresh.connectedRoadTiles].sort(),
    'the reducer-returned graph must match a from-scratch recompute exactly (the 970-vs-975 defect)'
  );
});

// Sanity: journal classification for the new action (GR#21 replay coverage).
test('FEAT/BUG-b2d31bc7 FIX 3: placeMany round-trips through the journal', () => {
  const action = { type: 'placeMany', spec: HUT, tiles: [{ x: 1, y: 1 }, { x: 2, y: 1 }] };
  const j = recordAction({ entries: [] }, 0, action);
  assert.equal(j.entries.length, 1);
  assert.deepEqual(j.entries[0].action, action);
});
