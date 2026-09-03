// attack-autoscale-ladder.test.mjs — INDEPENDENT DESTRUCTIVE ROUND (GR#23)
// against FEAT-2326609740 + BUG-590 (the auto-scale ladder). Attacker is NOT
// the author. Adversarial probes, not acceptance restatements.
//
// Findings this file pins (see the round report):
//   F1  same-pass footprint OVERLAP — two buildings claim the same tile
//   F2  relocate() moves a grown building using the SPEC footprint -> overlap
//   F3  2-hop skip-forward grants TWO tiers of capacity for ONE charge
//   F4  a transient fits() block at the height cap locks the building FOREVER
//   F5  the grown footprint is invisible to every renderer/hit-tester

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  SPECS,
  heightCapOf,
  capacityAtTier,
  computeRoadConnectivity,
  occupiedSet,
  fits,
  powerStats,
  MAP_W,
  MAP_H,
} from '../src/sim/data.ts';
import {
  initialState,
  reducer,
  evaluateBuildingMonitors,
  TICKS_PER_MONTH,
  BUILDING_AUTO_SCALE_COST_FRACTION,
} from '../src/sim/engine.ts';
import { buildDebugJson, debugJsonText } from '../src/sim/debugjson.ts';
import { EMPTY_MAP_UI } from '../src/sim/uistate.ts';

function mk(over) {
  const base = initialState();
  return {
    ...base,
    unlockedAll: true,
    roadMonitors: [],
    buildingMonitors: [],
    buildings: [],
    population: 0,
    funds: 100_000_000_000,
    tick: 0,
    ...over,
  };
}

function roadRow(y, maxX) {
  const roads = [];
  for (let x = 0; x <= maxX; x++) roads.push({ id: 700000 + y * 1000 + x, spec: 'road', x, y, builtTick: -1000 });
  return roads;
}

function withConnectivity(s) {
  return { ...s, roadConnectivity: computeRoadConnectivity(s) };
}

function testUi() {
  return { appVersion: 'v9.9.9-test', frameAtMs: 1_700_000_000_000, map: EMPTY_MAP_UI, errors: [] };
}

/** Every tile a building really occupies, per its OWN footprint. */
function tilesOf(b) {
  const sp = SPECS[b.spec];
  const w = b.footprintW ?? sp.w;
  const h = b.footprintH ?? sp.h;
  const out = [];
  for (let dx = 0; dx < w; dx++) for (let dy = 0; dy < h; dy++) out.push(`${b.x + dx},${b.y + dy}`);
  return out;
}

/** Every pair of buildings sharing a tile. */
function overlaps(buildings) {
  const owner = new Map();
  const clashes = [];
  for (const b of buildings) {
    if (!SPECS[b.spec]) continue;
    for (const t of tilesOf(b)) {
      if (owner.has(t)) clashes.push({ tile: t, a: owner.get(t), b: b.id });
      else owner.set(t, b.id);
    }
  }
  return clashes;
}

// ===========================================================================
// F1 — ATTACK 1: same-pass footprint collision.
// evaluateBuildingMonitors steps up to MAX_AUTO_SCALE_UPGRADES_PER_PASS (25)
// buildings in ONE pass, and attemptScaleStep() computes occupiedSet() from
// the PRE-pass `s` every time. No building can see any other building's claim
// from the same pass, so two of them take the same tile.
//   A(10,5) grows RIGHT into (11,5).
//   B(11,4) right-blocked by the blocker at (12,4), grows DOWN into (11,5).
// ===========================================================================
function collisionFixture() {
  const roads = [];
  let rid = 900000;
  for (let x = 0; x <= 20; x++) roads.push({ id: rid++, spec: 'road', x, y: 6, builtTick: -1000 });
  for (let y = 3; y <= 5; y++) roads.push({ id: rid++, spec: 'road', x: 14, y, builtTick: -1000 });
  for (let x = 11; x <= 13; x++) roads.push({ id: rid++, spec: 'road', x, y: 3, builtTick: -1000 });

  const a = { id: 1, spec: 'res_hut', x: 10, y: 5, builtTick: -1000, capacityTier: 1, heightStoreys: 2 };
  const b = { id: 2, spec: 'res_hut', x: 11, y: 4, builtTick: -1000, capacityTier: 1, heightStoreys: 2 };
  const blocker = { id: 3, spec: 'res_hut', x: 12, y: 4, builtTick: -1000 };
  return withConnectivity(
    mk({
      buildings: [...roads, a, b, blocker],
      population: SPECS['res_hut'].capacityTiers[1] * 1000,
      tick: 30,
      buildingMonitors: [
        { buildingId: 1, until: 360, type: 'residents' },
        { buildingId: 2, until: 360, type: 'residents' },
      ],
    })
  );
}

test('F1 ATTACK-1: two buildings scaling OUT in the same monitor pass must not claim the same tile', () => {
  const s = collisionFixture();
  const r = evaluateBuildingMonitors(s, 30);
  const A = r.buildings.find((x) => x.id === 1);
  const B = r.buildings.find((x) => x.id === 2);
  const clashes = overlaps(r.buildings);
  assert.deepEqual(
    clashes,
    [],
    `FOOTPRINT OVERLAP after ONE monitor pass: ${JSON.stringify(clashes)}\n` +
      `  A=${JSON.stringify({ id: A.id, x: A.x, y: A.y, w: A.footprintW, h: A.footprintH, tier: A.capacityTier })}\n` +
      `  B=${JSON.stringify({ id: B.id, x: B.x, y: B.y, w: B.footprintW, h: B.footprintH, tier: B.capacityTier })}`
  );
});

test('F1b ATTACK-1b: after the overlap, occupiedSet under-counts — demolishing one building frees a tile the other still stands on', () => {
  const s0 = collisionFixture();
  const r = evaluateBuildingMonitors(s0, 30);
  // Demolish A. B still occupies (11,5) — the contested tile.
  const s1 = { ...s0, buildings: r.buildings.filter((b) => b.id !== 1) };
  const B = s1.buildings.find((b) => b.id === 2);
  const occ = occupiedSet(s1);
  const stillStandingOn = tilesOf(B);
  for (const t of stillStandingOn) {
    assert.ok(occ.has(t), `tile ${t} is under live building 2 but is NOT in occupiedSet — a later placement will land on top of it`);
  }
  // and the hard consequence: a 1x1 placement is now permitted on top of B.
  const contested = '11,5';
  assert.ok(!fits(occ, 1, 1, 11, 5) || !stillStandingOn.includes(contested),
    `fits() says a new 1x1 building may be placed at ${contested}, which building 2 still occupies`);
});

// ===========================================================================
// F2 — ATTACK 2: `relocate` (the move tool) validates against SPECS[spec].w/h,
// not the building's grown footprint. Move a grown 2x1 into a 1x1 hole.
// ===========================================================================
test('F2 ATTACK-2: relocating a building that has scaled OUT must validate its REAL footprint', () => {
  const roads = roadRow(6, 30);
  // grown: 2 tiles wide (footprintW=2) though res_hut's spec is 1x1
  const grown = { id: 1, spec: 'res_hut', x: 10, y: 5, builtTick: -1000, capacityTier: 2, heightStoreys: 2, footprintW: 2, footprintH: 1 };
  // A 1-tile-wide hole at (20,5): neighbours at (19,5) and (21,5).
  const l = { id: 2, spec: 'res_hut', x: 19, y: 5, builtTick: -1000 };
  const rr = { id: 3, spec: 'res_hut', x: 21, y: 5, builtTick: -1000 };
  let s = withConnectivity(mk({ buildings: [...roads, grown, l, rr], tick: 0, movingId: 1 }));

  const after = reducer(s, { type: 'relocate', x: 20, y: 5 });
  const moved = after.buildings.find((b) => b.id === 1);
  const clashes = overlaps(after.buildings);
  assert.deepEqual(clashes, [],
    `relocate accepted a move into a hole too small for the REAL footprint: moved to (${moved.x},${moved.y}) ` +
    `w=${moved.footprintW} h=${moved.footprintH}; overlaps=${JSON.stringify(clashes)}`);
});

test('F2b ATTACK-2b: relocating a grown building must not push it off the map edge', () => {
  const roads = roadRow(MAP_H - 3, MAP_W - 1);
  const grown = { id: 1, spec: 'res_hut', x: 10, y: 5, builtTick: -1000, capacityTier: 2, footprintW: 3, footprintH: 1 };
  let s = withConnectivity(mk({ buildings: [...roads, grown], tick: 0, movingId: 1 }));
  const after = reducer(s, { type: 'relocate', x: MAP_W - 1, y: MAP_H - 5 });
  const moved = after.buildings.find((b) => b.id === 1);
  assert.ok(moved.x + (moved.footprintW ?? 1) <= MAP_W,
    `relocate put a grown building off the map: x=${moved.x} w=${moved.footprintW} MAP_W=${MAP_W}`);
});

// ===========================================================================
// F3 — ATTACK 3: the 2-hop skip-forward grants TWO tiers of capacity for ONE
// charge. When the UP step is refused (height at cap) the loop CONSUMES that
// tier and advances on the next one; `cost`/`upgraded` are incremented once.
// ===========================================================================
test('F3 ATTACK-3: a skip-forward step must not deliver two tiers of capacity for one charge', () => {
  const roads = roadRow(9, 30);
  const spId = 'res_hut';
  const cap = heightCapOf(SPECS[spId]);
  // Even current tier -> candidate is odd (UP), refused at cap; next (OUT) succeeds.
  const b = { id: 1, spec: spId, x: 10, y: 10, builtTick: -1000, capacityTier: 2, heightStoreys: cap };
  const s = withConnectivity(
    mk({
      buildings: [...roads, b],
      population: SPECS[spId].capacityTiers[2] * 1000,
      tick: 30,
      buildingMonitors: [{ buildingId: 1, until: 360, type: 'residents' }],
    })
  );

  const r = evaluateBuildingMonitors(s, 30);
  const after = r.buildings.find((x) => x.id === 1);
  const gained = (after.capacityTier ?? 0) - 2;
  const oneStep = Math.round(SPECS[spId].cost * BUILDING_AUTO_SCALE_COST_FRACTION);
  assert.ok(gained <= 1,
    `tier jumped by ${gained} (2 -> ${after.capacityTier}) for a SINGLE charge of ${r.cost} ` +
    `(one step = ${oneStep}); capacity ${capacityAtTier(SPECS[spId], 2)} -> ${capacityAtTier(SPECS[spId], after.capacityTier)}, upgraded=${r.upgraded}`);
});

// ===========================================================================
// F4 — ATTACK 4: at the height cap, a TRANSIENT fits() failure locks the
// building PERMANENTLY (scaleLocked + monitor removed), so growth can never
// resume after a neighbouring demolition frees the space.
// ===========================================================================
function boxedInFixture() {
  const spId = 'res_hut';
  const cap = heightCapOf(SPECS[spId]);
  const roads = roadRow(9, 30);
  const b = { id: 1, spec: spId, x: 10, y: 10, builtTick: -1000, capacityTier: 1, heightStoreys: cap };
  const right = { id: 2, spec: spId, x: 11, y: 10, builtTick: -1000 };
  const down = { id: 3, spec: spId, x: 10, y: 11, builtTick: -1000 };
  return withConnectivity(
    mk({
      buildings: [...roads, b, right, down],
      population: SPECS[spId].capacityTiers[1] * 1000,
      tick: 30,
      buildingMonitors: [{ buildingId: 1, until: 3600, type: 'residents' }],
    })
  );
}

test('F4 ATTACK-4: a demolishable block at the height cap must not permanently lock the building', () => {
  const s = boxedInFixture();
  const r = evaluateBuildingMonitors(s, 30);
  const after = r.buildings.find((x) => x.id === 1);
  assert.equal(after.scaleLocked ?? false, false,
    'a building blocked only by DEMOLISHABLE neighbours was marked permanently scaleLocked');
  assert.ok(r.monitors.some((m) => m.buildingId === 1),
    'its monitor was removed, so growth can never resume after the space is freed');
});

test('F4b ATTACK-4b: constructive — after the blockers are demolished growth must resume', () => {
  let s = boxedInFixture();
  const blocked = evaluateBuildingMonitors(s, 30);
  s = { ...s, buildings: blocked.buildings.filter((x) => x.id !== 2 && x.id !== 3), buildingMonitors: blocked.monitors };
  const freed = evaluateBuildingMonitors(s, 30 + 10 * TICKS_PER_MONTH);
  const after = freed.buildings.find((x) => x.id === 1);
  assert.ok((after.capacityTier ?? 0) > 1,
    `growth did not resume after space was freed (tier=${after.capacityTier}, scaleLocked=${after.scaleLocked})`);
});

// ===========================================================================
// F5 — ATTACK 5: the grown footprint is real for occupancy but invisible to
// hit-testing/rendering. The engine's own occupiedSet disagrees with the
// spec-footprint predicate MapView uses to decide which building a tile is.
// ===========================================================================
// RE-ROUND (2026-09-03) REWRITE: the r1 version of this test inlined a COPY of
// the pre-fix predicate (`bs.w`/`bs.h`) and could therefore never observe the
// fix, whichever way the code went. It now drives the REAL code path a click
// on a grown tile actually takes — MapView dispatches `{type:'bulldoze', x, y}`
// and the REDUCER re-does its own hit test — so it observes the shipped
// behaviour rather than a frozen snapshot of the old one.
test('F5 ATTACK-5: a grown tile must be attributable to its building by the REAL bulldoze hit test', () => {
  const roads = roadRow(9, 30);
  const grown = { id: 1, spec: 'res_hut', x: 10, y: 10, builtTick: -1000, capacityTier: 2, footprintW: 2, footprintH: 1 };
  const s = withConnectivity(mk({ buildings: [...roads, grown], funds: 1e9 }));
  const occ = occupiedSet(s);
  assert.ok(occ.has('11,10'), 'sanity: the grown tile is occupied by the engine');
  const after = reducer(s, { type: 'bulldoze', x: 11, y: 10 });
  assert.ok(
    !after.buildings.some((b) => b.id === 1),
    'tile (11,10) is occupied but the reducer bulldoze attributes it to NO building — an un-selectable, un-bulldozable dead tile'
  );
});

// ===========================================================================
// MONEY — every scale step charges once; capex tracks the booked outflow.
// ===========================================================================
test('MONEY: auto-scale spend is booked exactly once per tick over a 400-tick heavy-scaling run', () => {
  const roads = roadRow(9, 40);
  const bs = [];
  for (let i = 0; i < 12; i++) bs.push({ id: 100 + i, spec: 'res_hut', x: 2 * i, y: 10, builtTick: -1000 });
  let s = withConnectivity(
    mk({
      buildings: [...roads, ...bs],
      population: 100000,
      tick: 0,
      buildingMonitors: bs.map((b) => ({ buildingId: b.id, until: 1_000_000, type: 'residents' })),
    })
  );

  let prevCapex = s.cumulativeCapexSpent ?? 0;
  let scaledTicks = 0;
  for (let i = 0; i < 400; i++) {
    const next = reducer(s, { type: 'tick' });
    const flow = (next.lastFlows?.outflows ?? []).find((f) => f.label === 'Building Auto-Scale');
    const booked = flow ? flow.value : 0;
    if (booked > 0) {
      assert.equal((next.cumulativeCapexSpent ?? 0) - prevCapex, booked,
        `tick ${next.tick}: capex delta != booked auto-scale outflow ${booked} (double-count or miss)`);
      scaledTicks++;
    }
    prevCapex = next.cumulativeCapexSpent ?? 0;
    s = next;
  }
  assert.ok(scaledTicks > 0, 'the run must actually have scaled (otherwise the probe proves nothing)');
});

// ===========================================================================
// MAP EDGE — an OUT step must never grow off-map.
// ===========================================================================
test('EDGE: an OUT step at the map edge never grows off-map', () => {
  const roads = roadRow(MAP_H - 3, MAP_W - 1);
  const b = { id: 1, spec: 'res_hut', x: MAP_W - 1, y: MAP_H - 1, builtTick: -1000, capacityTier: 1, heightStoreys: 3 };
  const s = withConnectivity(
    mk({ buildings: [...roads, b], population: 1_000_000, tick: 30, buildingMonitors: [{ buildingId: 1, until: 100000, type: 'residents' }] })
  );
  const r = evaluateBuildingMonitors(s, 30);
  const after = r.buildings.find((x) => x.id === 1);
  assert.ok(after.x + (after.footprintW ?? 1) <= MAP_W, `grew off the right edge: x=${after.x} w=${after.footprintW}`);
  assert.ok(after.y + (after.footprintH ?? 1) <= MAP_H, `grew off the bottom edge: y=${after.y} h=${after.footprintH}`);
});

// ===========================================================================
// OLD SAVES — a pre-feature building occupies exactly its spec footprint.
// ===========================================================================
test('OLD SAVES: a pre-feature building (no footprint fields) occupies exactly its spec footprint', () => {
  const b = { id: 1, spec: 'res_block', x: 10, y: 10, builtTick: -1000 };
  const s = mk({ buildings: [b] });
  const sp = SPECS['res_block'];
  assert.equal(occupiedSet(s).size, sp.w * sp.h);
});

// ===========================================================================
// NPP — the reactor ladder must be height-exempt and scale grid MW.
// ===========================================================================
test('NPP: pow_nuke is height-exempt and every reactor tier raises grid MW', () => {
  const sp = SPECS['pow_nuke'];
  assert.ok(Array.isArray(sp.capacityTiers), 'pow_nuke carries a reactor ladder');
  assert.equal(heightCapOf(sp), Infinity, 'pow_nuke carries no height cap (height-exempt)');
  const base = { id: 1, spec: 'pow_nuke', x: 40, y: 40, builtTick: -2000 };
  const s0 = withConnectivity(mk({ buildings: [...roadRow(39, 60), base] }));
  const s1 = withConnectivity({ ...s0, buildings: s0.buildings.map((b) => (b.id === 1 ? { ...b, capacityTier: 3 } : b)) });
  assert.ok(powerStats(s0).cap > 0, 'sanity: the reactor is online in the fixture');
  assert.ok(powerStats(s1).cap > powerStats(s0).cap,
    'a higher reactor tier must raise the grid MW cap through computePowerStats');
});

// ===========================================================================
// DETERMINISM — two identical runs must produce byte-identical debug JSON.
// ===========================================================================
test('DETERMINISM: two identical heavy-scaling runs produce byte-identical debug JSON', () => {
  const build = () => {
    const roads = roadRow(9, 40);
    const bs = [];
    for (let i = 0; i < 10; i++) bs.push({ id: 300 + i, spec: 'res_hut', x: 2 * i, y: 10, builtTick: -1000 });
    let s = withConnectivity(
      mk({ buildings: [...roads, ...bs], population: 50000, tick: 0, buildingMonitors: bs.map((b) => ({ buildingId: b.id, until: 1_000_000, type: 'residents' })) })
    );
    for (let i = 0; i < 200; i++) s = reducer(s, { type: 'tick' });
    return debugJsonText(buildDebugJson(s, testUi()));
  };
  assert.equal(build(), build());
});

// ===========================================================================
// BUG-590 — the base-spec stall. Capacity must climb; note the ladder ceiling.
// ===========================================================================
test('BUG-590: a base-spec-only city climbs off the stall (and record the ladder ceiling)', () => {
  const roads = roadRow(9, 60);
  const bs = [];
  for (let i = 0; i < 13; i++) bs.push({ id: 200 + i, spec: 'res_hut', x: 2 * i, y: 10, builtTick: -1000 });
  let s = withConnectivity(
    mk({ buildings: [...roads, ...bs], population: 200, tick: 0, buildingMonitors: bs.map((b) => ({ buildingId: b.id, until: 1_000_000, type: 'residents' })) })
  );
  const baseCap = bs.length * SPECS['res_hut'].residents;
  for (let i = 0; i < 400; i++) {
    s = reducer(s, { type: 'tick' });
    s = { ...s, population: 1_000_000 };
  }
  let cap = 0;
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (sp?.kind === 'residential') cap += capacityAtTier(sp, b.capacityTier ?? 0);
  }
  assert.ok(cap > baseCap, `residential capacity did not climb: ${cap} vs base ${baseCap} — the BUG-590 stall is NOT closed`);
  // Ceiling note (not a failure): tierLadder(base,10) tops out at 1.1^9 ~ 2.36x,
  // so 13 res_hut can never exceed 13 * 19 = 247 residents on the ladder alone.
});

// ===========================================================================
// F1c — the decisive one: F1 is NOT a contrived fixture. A plain sparse city
// of 848 buildings, ticked normally through the reducer, self-overlaps.
// ===========================================================================
test('F1c ATTACK-1c: a realistic sparse city ticked normally must never self-overlap', () => {
  const buildings = [];
  let id = 1;
  for (let y = 0; y < 30; y += 3) for (let x = 0; x <= 40; x++) buildings.push({ id: id++, spec: 'road', x, y, builtTick: -3000 });
  // Deterministic sparse layout (LCG) — irregular vacancies are the ingredient
  // a uniform grid lacks: some buildings are right-blocked and grow DOWN into a
  // tile a neighbour is growing RIGHT into, in the SAME monitor pass.
  const monitors = [];
  let seed = 12345;
  const rnd = () => ((seed = (seed * 1103515245 + 12345) % 2147483648) / 2147483648);
  for (let y = 1; y < 29; y += 1) {
    if (y % 3 === 0) continue;
    for (let x = 0; x <= 40; x += 1) {
      if (rnd() < 0.45) continue;
      const b = { id: id++, spec: 'res_hut', x, y, builtTick: -3000 };
      buildings.push(b);
      monitors.push({ buildingId: b.id, until: 1e7, type: 'residents' });
    }
  }
  let s = withConnectivity(mk({ buildings, buildingMonitors: monitors, population: 5_000_000, tick: 0 }));
  for (let i = 0; i < 120; i++) {
    s = reducer(s, { type: 'tick' });
    s = { ...s, population: 5_000_000 };
    const clashes = overlaps(s.buildings);
    assert.deepEqual(clashes, [], `city self-overlapped at tick ${s.tick}: ${JSON.stringify(clashes.slice(0, 3))}`);
  }
});
