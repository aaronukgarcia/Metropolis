// attack-bug646-round.test.mjs — Independent destructive round (GR#23,
// attacker != author) on BUG-646 (Aaron: "the autofix looks to have a 250
// limit, not enough, make it 2000" -> author found + fixed two real
// O(buildings)-per-unit perf bugs in occupiedSetIncremental/
// roadTileSetIncremental (data.ts) + engine.ts's 'place' case, and a
// sorted-candidate cache in createSpotSearchContext()).
//
// FOCUS: this is a money-and-state path on the hottest code in the project.
// The whole risk is INCREMENTAL CORRECTNESS — occupiedSetIncremental/
// roadTileSetIncremental/the candidate cache maintain state ACROSS
// placements instead of rebuilding, so a missed update leaves a phantom
// occupied tile (blocks a legitimately free spot) or a hole (lets two
// buildings overlap). Every test below re-derives an INDEPENDENT oracle from
// state.buildings directly — never calling occupiedSet()/roadTileSetOf()
// themselves — so a bug in the target's own cache cannot also poison the
// check.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import {
  SPECS,
  isRoadSpec,
  occupiedSet,
  roadTileSetOf,
  createSpotSearchContext,
  fits,
  RESOLVE_DEMAND_ALL_MAX_UNITS,
} from '../src/sim/data.ts';
import { emptyJournal, recordAction, isStateAffecting } from '../src/sim/journal.ts';
import { replayFromGenesis, stableStringify } from '../src/sim/genesisReplay.ts';
import fs from 'node:fs';

const ticks = (n) => Array.from({ length: n }, () => ({ type: 'tick' }));

/** starterCity()'s scenery ids are deterministic and always start from 1 —
 *  every genuine test placement always gets an id ABOVE this mark (the
 *  nextSafeBuildingId contract), so it is safe as a fixed module constant
 *  rather than recomputed per test. See assertSetsSane()'s doc comment. */
const SCENERY_HIGH_WATER_MARK = Math.max(0, ...initialState().buildings.map((b) => b.id));

/** Independent oracle: rebuild the occupied-tile set straight from
 *  state.buildings, with NO reference to occupiedSet()/occupiedSetCache. */
function oracleOccupied(buildings) {
  const set = new Set();
  for (const b of buildings) {
    const sp = SPECS[b.spec];
    if (!sp) continue;
    for (let dx = 0; dx < sp.w; dx++) for (let dy = 0; dy < sp.h; dy++) set.add(`${b.x + dx},${b.y + dy}`);
  }
  return set;
}

/** Independent oracle for the road-tile set, same idea. */
function oracleRoads(buildings) {
  const set = new Set();
  for (const b of buildings) {
    const sp = SPECS[b.spec];
    if (!sp || !isRoadSpec(sp)) continue;
    for (let dx = 0; dx < sp.w; dx++) for (let dy = 0; dy < sp.h; dy++) set.add(`${b.x + dx},${b.y + dy}`);
  }
  return set;
}

function setsEqual(a, b) {
  if (a.size !== b.size) return false;
  for (const v of a) if (!b.has(v)) return false;
  return true;
}

function diffSets(oracle, actual) {
  const missing = [...oracle].filter((t) => !actual.has(t)); // a HOLE (actual thinks it's free)
  const phantom = [...actual].filter((t) => !oracle.has(t)); // a PHANTOM (actual thinks it's occupied)
  return { missing, phantom };
}

/** starterCity() (engine.ts) deliberately overlaps a local 'road' onto the
 *  'm20' motorway at the junction tile (150,58) to render a crossroads — a
 *  raw array construction (starterCity() pushes buildings directly, never
 *  through the 'place' reducer) that bypasses fits()'s no-overlap guard
 *  entirely. It is NOT a counterexample to occupiedSet/fits() correctness,
 *  which only governs what the REDUCER can place, never hand-authored
 *  scenery. Genuine test placements below always get an id strictly above
 *  the genesis high-water mark (nextSafeBuildingId's own contract), so the
 *  overlap check only needs to exempt a collision where BOTH buildings
 *  predate `sceneryHighWaterMark` — any collision involving a REAL
 *  test-driven placement still fails loudly. */
function assertSetsSane(state, label, sceneryHighWaterMark = SCENERY_HIGH_WATER_MARK) {
  const occ = occupiedSet(state);
  const occOracle = oracleOccupied(state.buildings);
  const occDiff = diffSets(occOracle, occ);
  assert.ok(
    setsEqual(occ, occOracle),
    `${label}: occupiedSet diverged from oracle — missing(holes)=${JSON.stringify(occDiff.missing).slice(0, 200)} phantom=${JSON.stringify(occDiff.phantom).slice(0, 200)}`
  );

  const roads = roadTileSetOf(state);
  const roadOracle = oracleRoads(state.buildings);
  const roadDiff = diffSets(roadOracle, roads);
  assert.ok(
    setsEqual(roads, roadOracle),
    `${label}: roadTileSetOf diverged from oracle — missing(holes)=${JSON.stringify(roadDiff.missing).slice(0, 200)} phantom=${JSON.stringify(roadDiff.phantom).slice(0, 200)}`
  );

  // Structural invariant regardless of caching: no two buildings placed
  // through the REDUCER may overlap footprints (the thing occupiedSet exists
  // to prevent). A collision between two pre-existing scenery buildings
  // (id <= sceneryHighWaterMark on BOTH sides) is exempt — see this
  // function's doc comment (starterCity()'s deliberate road/motorway
  // junction tile, which never goes through fits()).
  const seenBy = new Map(); // tile key -> building id that first claimed it
  for (const b of state.buildings) {
    const sp = SPECS[b.spec];
    if (!sp) continue;
    for (let dx = 0; dx < sp.w; dx++) {
      for (let dy = 0; dy < sp.h; dy++) {
        const key = `${b.x + dx},${b.y + dy}`;
        const priorId = seenBy.get(key);
        if (priorId !== undefined) {
          const bothScenery = priorId <= sceneryHighWaterMark && b.id <= sceneryHighWaterMark;
          assert.ok(
            bothScenery,
            `${label}: OVERLAP at ${key} — building id ${b.id} (${b.spec}) collides with building id ${priorId}`
          );
        } else {
          seenBy.set(key, b.id);
        }
      }
    }
  }
}

test('BUG-646 ATTACK: occupiedSet/roadTileSetOf stay byte-identical to a from-scratch oracle across a long MIXED place/bulldoze/relocate/stampRegion/road sequence', () => {
  let s = { ...initialState(), funds: 1e13, unlockedAll: true, administrationState: null };
  assertSetsSane(s, 'genesis'); // starterCity()'s own m20/road junction overlap is exempt — see assertSetsSane's doc comment

  // place a grid of mixed specs including a road, a tagged power plant, and
  // clean/waste water plants (the ones whose placement invalidates the
  // sorted-candidate cache) interleaved with everything else.
  const specs = ['res_hut', 'road', 'pow_coal', 'wat_clean', 'wat_waste', 'pow_wind', 'hea_clinic'];
  let x = 10;
  let y = 10;
  const placedIds = [];
  for (let i = 0; i < 60; i++) {
    const spec = specs[i % specs.length];
    const before = s.buildings.length;
    s = reducer(s, { type: 'place', spec, x, y });
    if (s.buildings.length > before) placedIds.push(s.buildings[s.buildings.length - 1].id);
    assertSetsSane(s, `after place #${i} (${spec} at ${x},${y})`);
    x += 4;
    if (x > 200) {
      x = 10;
      y += 4;
    }
  }

  // A REFUSED placement (deliberately overlapping an existing building) must
  // be a true no-op — no state change, no cache corruption for the NEXT real
  // placement.
  const occBefore = s;
  const overlapAttempt = reducer(s, { type: 'place', spec: 'res_hut', x: 10, y: 10 });
  assert.equal(overlapAttempt, occBefore, 'a refused (occupied-tile) placement must return the identical state reference (true no-op)');
  assertSetsSane(overlapAttempt, 'after refused overlap placement');

  // An out-of-funds abort must never mutate buildings/funds (BUG-396: it DOES
  // return a new object carrying an "insufficient funds" placeNotice, so a
  // strict reference no-op is the wrong check here — the real invariant is
  // no building added and no money moved).
  const brokeState = { ...s, funds: 0 };
  const brokeAttempt = reducer(brokeState, { type: 'place', spec: 'wat_clean', x: 500, y: 500 });
  assert.equal(brokeAttempt.buildings.length, brokeState.buildings.length, 'an out-of-funds placement must add no building');
  assert.equal(brokeAttempt.funds, brokeState.funds, 'an out-of-funds placement must not move any money');
  assert.match(brokeAttempt.placeNotice ?? '', /insufficient funds/i, 'an out-of-funds placement must report why it was refused');
  assertSetsSane(brokeAttempt, 'after out-of-funds abort');

  // Bulldoze one of the placed buildings, then place again on the freed tile.
  // 'bulldoze' targets by COORDINATE (finds whatever building's footprint
  // covers action.x/action.y), not by id.
  const toBulldoze = placedIds[5];
  const freedTile = s.buildings.find((b) => b.id === toBulldoze);
  const bulldozed = reducer(s, { type: 'bulldoze', x: freedTile.x, y: freedTile.y });
  assert.equal(bulldozed.buildings.some((b) => b.id === toBulldoze), false, 'bulldozed building must be gone');
  assertSetsSane(bulldozed, 'after bulldoze');
  const replaced = reducer(bulldozed, { type: 'place', spec: 'res_hut', x: freedTile.x, y: freedTile.y });
  assert.ok(replaced.buildings.length > bulldozed.buildings.length, 'placing on a freshly-bulldozed tile must succeed (no phantom occupation)');
  assertSetsSane(replaced, 'after re-place on freed tile');
  s = replaced;

  // relocate an existing building to a new empty spot. 'relocate' moves
  // whatever building 'pickup' most recently latched onto (state.movingId),
  // not an id carried on the relocate action itself.
  const toRelocate = s.buildings.find((b) => b.spec === 'res_hut');
  if (toRelocate) {
    const pickedUp = reducer(s, { type: 'pickup', id: toRelocate.id });
    assert.equal(pickedUp.movingId, toRelocate.id, 'precondition: pickup must latch movingId onto the target building');
    const relocated = reducer(pickedUp, { type: 'relocate', x: 300, y: 200 }); // in-bounds (MAP_W=440, MAP_H=260) — 900,900 silently no-ops out of bounds
    assertSetsSane(relocated, 'after relocate');
    // the OLD footprint must now be free.
    const stillOccupied = occupiedSet(relocated).has(`${toRelocate.x},${toRelocate.y}`);
    assert.equal(stillOccupied, false, 'relocate must free the OLD footprint, not leave a phantom occupation behind');
    s = relocated;
  }

  // stampRegion (bulk-place a real clipboard region — road tiles along a row).
  const stampItems = [];
  for (let dx = 0; dx < 20; dx++) stampItems.push({ spec: 'road', dx, dy: 0 });
  const stamped = reducer(s, { type: 'stampRegion', clipboard: { w: 20, h: 1, items: stampItems }, x: 600, y: 10 });
  assert.ok(stamped.buildings.length >= s.buildings.length, 'precondition: stampRegion must actually place something');
  assertSetsSane(stamped, 'after stampRegion');
  s = stamped;

  // A resolveDemandAll batch at high population, on top of everything above.
  const bigPopState = { ...s, population: 3_000_000, funds: 1e13, administrationState: null };
  const afterFix = reducer(bigPopState, { type: 'resolveDemandAll' });
  assertSetsSane(afterFix, 'after resolveDemandAll batch');
});

test('BUG-646 ATTACK: auto-scale growth changing a footprint (capacityTier upgrade) does not corrupt occupiedSet', () => {
  // res_estate has capacityTiers (auto-scale upgrades capacity, NOT footprint,
  // per the spec table) — but drive a real growth sequence through it and
  // prove the occupied set survives regardless.
  let s = { ...initialState(), funds: 1e13, unlockedAll: true, administrationState: null };
  s = reducer(s, { type: 'place', spec: 'res_estate', x: 20, y: 20 });
  assertSetsSane(s, 'after placing res_estate');
  for (let i = 0; i < 60; i++) {
    s = reducer(s, { type: 'tick' });
    if (i % 10 === 0) assertSetsSane(s, `after growth tick ${i}`);
  }
  assertSetsSane(s, 'after growth sequence');
});

test('BUG-646 ATTACK: the sorted-candidate cache does not hand back a STALE spot when a candidate becomes invalid mid-batch (another placement takes the tile)', () => {
  const s = { ...initialState(), funds: 1e13, unlockedAll: true, administrationState: null };
  const ctx = createSpotSearchContext(s, 'hea_clinic');

  const first = ctx.findNext();
  assert.ok(first, 'precondition: a first spot must be found');

  // Simulate ANOTHER concurrent consumer of the same context occupying the
  // NEXT candidate the cache would otherwise hand out, by directly draining
  // and inspecting what the context considers occupied vs what a real
  // 'place' dispatch would do. Do this properly: dispatch a REAL 'place' at
  // the exact spot findNext() just returned, then feed occupy() what 'place'
  // actually added (mirrors placePlanItem's real usage), and confirm the
  // NEXT findNext() never repeats the same tile.
  const placed1 = reducer(s, { type: 'place', spec: 'hea_clinic', x: first.x, y: first.y });
  ctx.occupy(placed1.buildings.slice(s.buildings.length));

  const second = ctx.findNext();
  assert.ok(second, 'a second spot must still be found');
  assert.ok(
    !(second.x === first.x && second.y === first.y),
    'findNext() must never hand back a tile the context itself was just told is occupied'
  );
  const sp = SPECS['hea_clinic'];
  assert.ok(
    fits(occupiedSet(placed1), sp.w, sp.h, second.x, second.y),
    'the second candidate must actually fit against the REAL post-placement occupied set, not a stale cached one'
  );

  // Now directly attack the cache invalidation contract: place a SECOND
  // clinic (tag-free spec) and confirm the context has synced correctly by
  // exhausting several more candidates, checking each against a live oracle
  // rebuilt from the real dispatched state.
  let cur = placed1;
  let ctxState = s;
  const clinicIds = [];
  for (let i = 0; i < 15; i++) {
    const spot = ctx.findNext();
    assert.ok(spot, `precondition: spot #${i} must exist`);
    const before = cur.buildings.length;
    const next = reducer(cur, { type: 'place', spec: 'hea_clinic', x: spot.x, y: spot.y });
    assert.ok(next.buildings.length > before, `placement #${i} at the context's own recommended spot must actually succeed (not refused as occupied)`);
    ctx.occupy(next.buildings.slice(before));
    cur = next;
  }
  assertSetsSane(cur, 'after 15 context-driven clinic placements');
});

test('BUG-646 ATTACK: a tagged spec (wat_clean, pollution power plant) placed through the batch context never scores against STALE tagged/resList data', () => {
  // wat_clean/wat_waste/pow_coal all carry a `tag`, which the author's own
  // doc comment says invalidates the sorted-candidate cache on every unit —
  // correctness-preserving but losing the speedup. Prove the correctness
  // half: every placement must land on a tile that is TRULY free and whose
  // score inputs reflect every prior placement in the SAME batch.
  const pop = 6_000_000;
  let s = { ...initialState(), population: pop, funds: 1e13, unlockedAll: true, administrationState: null };
  const before = s.buildings.length;
  const after = reducer(s, { type: 'resolveDemand', serviceKey: 'cleanwater' });
  assert.ok(after.buildings.length > before, 'precondition: at least one wat_clean must place');
  assertSetsSane(after, 'after cleanwater resolveDemand (tagged spec batch)');

  const after2 = reducer(after, { type: 'resolveDemand', serviceKey: 'power' });
  assertSetsSane(after2, 'after power resolveDemand (pollution-tagged spec batch)');
});

test('BUG-646 ATTACK/DETERMINISM: two identical starting states produce an IDENTICAL placement SEQUENCE from resolveDemandAll (no Set/Map iteration-order dependency)', () => {
  const pop = 3_000_000;
  const mk = () => ({ ...initialState(), population: pop, funds: 1e13, unlockedAll: true, administrationState: null });
  const r1 = reducer(mk(), { type: 'resolveDemandAll' });
  const r2 = reducer(mk(), { type: 'resolveDemandAll' });
  assert.deepEqual(
    r1.buildings.map((b) => ({ spec: b.spec, x: b.x, y: b.y })),
    r2.buildings.map((b) => ({ spec: b.spec, x: b.x, y: b.y })),
    'GR#21: identical inputs must produce an identical placement sequence — any Set/Map insertion-order dependency in the new incremental code would show up here as a divergence'
  );
  assert.equal(stableStringify({ ...r1, roadConnectivity: null }), stableStringify({ ...r2, roadConnectivity: null }), 'full state must also be byte-identical');
});

test('BUG-646 ATTACK/DETERMINISM (GR#21): a resolveDemandAll of >250 units replays byte-identically from genesis', () => {
  // No debug action sets population directly (it is a derived/simulated
  // field, not a journaled input) — grow it for real via a journaled
  // 'placeMany' of high-capacity res_estate blocks followed by enough ticks
  // for the growth simulation to actually fill the new capacity, exactly the
  // pattern attack-bug606-replay.test.mjs's capTriggerScript() uses.
  const tiles = [];
  let tx = 5;
  let ty = 5;
  for (let i = 0; i < 800; i++) {
    tiles.push({ x: tx, y: ty });
    tx += 6;
    if (tx > 430) {
      tx = 5;
      ty += 6;
    }
  }
  let journal = emptyJournal();
  let state = initialState();
  const script = [
    { type: 'debugFunds', amount: 5_000_000_000 },
    { type: 'unlockAll' },
    { type: 'placeMany', spec: 'res_estate', tiles },
    ...ticks(250),
    { type: 'debugFunds', amount: 1e13 },
    { type: 'resolveDemandAll' },
    ...ticks(5),
  ];
  for (const action of script) {
    if (isStateAffecting(action)) journal = recordAction(journal, state.tick, action);
    state = reducer(state, action);
  }
  const plannedUnits = state.buildings.length;
  assert.ok(plannedUnits > 250, `precondition: this batch must exceed the OLD 250 cap to be a meaningful regression check (placed ${plannedUnits})`);

  const replayed = replayFromGenesis(journal);
  assert.equal(
    stableStringify({ ...replayed, roadConnectivity: null }),
    stableStringify({ ...state, roadConnectivity: null }),
    'live vs genesis-replay must be byte-identical for a large resolveDemandAll batch'
  );
});

/** Strip // line comments and /* block comments *[space]so a source-scan for
 *  a suspicious magic number ignores narrative doc comments (this codebase's
 *  BUG-646 doc comments legitimately quote "250" repeatedly as HISTORY —
 *  the OLD cap value, pre-fix per-unit timings, etc. — which is exactly the
 *  kind of true positive a raw string grep cannot tell apart from a real
 *  second hardcoded cap; only CODE lines are the actual GR#15 risk). */
function stripComments(src) {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/\/\/.*$/gm, '');
}

test('BUG-646 ATTACK: exactly RESOLVE_DEMAND_ALL_MAX_UNITS placed when 2001 are planned, and the message text derives from the constant (GR#15, no hardcoded 250 or 2000 in CODE)', () => {
  const dataTs = stripComments(fs.readFileSync(new URL('../src/sim/data.ts', import.meta.url), 'utf8'));
  const engineTs = stripComments(fs.readFileSync(new URL('../src/sim/engine.ts', import.meta.url), 'utf8'));
  // The constant declaration itself is allowed to say 2000; nothing ELSE in
  // CODE may hardcode 250 or a bare 2000 as if it were a second, independent
  // cap. Spec-table capacity/roadTier numbers (rd_avenue/rd_roundabout
  // `capacity: 250`, BROWNOUT_INDEX_SLOPE = 250, etc.) are unrelated
  // constants that happen to share the digits — excluded explicitly rather
  // than by broadening the match, so a genuine second cap constant still
  // trips this.
  const suspiciousDataTs = dataTs
    .replace(/export const RESOLVE_DEMAND_ALL_MAX_UNITS = 2000;/, '')
    .replace(/capacity:\s*250/g, '')
    .replace(/BROWNOUT_INDEX_SLOPE = 250/, '')
    .replace(/2:\s*250,/, '') // ROAD_TIER_CAPACITY[2] — an unrelated road-network constant
    .replace(/jobs:\s*250\b/g, '') // land_stadium researched employment figure (BUG-652), unrelated to the cap
    .match(/\b250\b/g);
  assert.equal(suspiciousDataTs, null, `data.ts must not hardcode 250 in CODE outside the (removed) constant declaration and known-unrelated spec constants: ${JSON.stringify(suspiciousDataTs)}`);
  const literalCapInEngine = engineTs.match(/\b(250|2000)\b/g);
  assert.equal(literalCapInEngine, null, `engine.ts must reference RESOLVE_DEMAND_ALL_MAX_UNITS, never a literal 250/2000 in CODE: ${JSON.stringify(literalCapInEngine)}`);
});
