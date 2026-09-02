// bug593-findspot-recentre-expand.test.mjs — BUG-593 regression.
//
// Root cause (recorded diagnosis, BOW BUG-593): findSpot() (data.ts) searched
// ONLY a fixed 90x90 window (stride 2) centred on housingCentroid() and
// returned null the instant that window was exhausted, with no fallback to
// widen the search or look at the rest of the map. Aaron's real map had 13
// homes tucked in one corner of a 3,861-building motorway/rail city with
// 94.5% free land: the fixed window sat entirely inside the built-out mass
// around the tiny residential cluster and reported "no buildable site found"
// while acres of free land sat untouched elsewhere.
//
// FIRST DRAFT OF THE FIX (REJECTed by an independent destructive round): also
// replaced housingCentroid() with a bounding-box centre of ALL buildings
// ("builtUpCentroid"), reasoning that a residential-only centroid was itself
// the problem. The round's isolation proved this was wrong and actively
// harmful: initialState() ships ~1,855 map-spanning infrastructure buildings
// (motorway/rail/highspeed-rail), so an all-buildings bbox centroid sits at
// ~the map centre REGARDLESS of where the city is — on a fresh game at pop
// 10,000, resolveDemand('power') placed 16 pow_wind turbines in the map
// centre wilderness, 0/16 road-adjacent, vs HEAD's 16/16 road-adjacent using
// the real (small) road network. Isolating the two changes showed the OLD
// housingCentroid + ONLY the widen-before-giving-up loop is sufficient for
// Aaron's repro (his residential centroid was fine — the fixed WINDOW was the
// bug) and restores the fresh-game placement behaviour.
//
// THE ACCEPTED FIX (data.ts): (1) keep housingCentroid() exactly as it was;
// (2) when a pass finds nothing, DOUBLE the window and search again, up to
// the point the window already covers the whole map; (3) that final,
// whole-map-covering pass uses STRIDE 1, not stride 2 — the round's moderate
// finding #4: stride 2 samples only 1 of every 4 tiles, so a stride-2 "empty"
// result over the WHOLE map can be a false claim (a genuinely free tile at an
// odd offset would never be tested); (4) engine.ts's resolveDemand notice now
// calls noBuildableSiteReason(specId) instead of a bare "no buildable site
// found" string when the map-wide (stride-1-proven) search truly comes up
// empty.
//
// This file proves: (a) the Aaron-shape scenario now finds a site, with a
// RED-PROOF showing the OLD algorithm (fixed window, no expansion — same
// housingCentroid as today) genuinely fails on the exact same state, so the
// widen-before-giving-up loop is what's load-bearing here, not the centroid;
// (b) a genuinely-full map returns null AND the resolveDemand notice states
// why; (c) the stride-1 final pass finds a single free tile at an odd offset
// that a stride-2-only search would have missed (and wrongly called "full");
// (d) determinism — same state, same answer, twice; (e) a FRESH game at pop
// 10,000 still places power road-adjacent, matching HEAD (the regression the
// independent round's isolation caught in the rejected draft); (f) the
// pre-existing demand-fix/auto-build/latency-quickwins suites (findSpot's
// callers, incl. BUG-566) are unaffected — run separately via the scoped
// gate, not duplicated here.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import {
  findSpot,
  noBuildableSiteReason,
  occupiedSet,
  fits,
  roadTileSetOf,
  SPECS,
  MAP_W,
  MAP_H,
  demandFixPlan,
} from '../src/sim/data.ts';

/** Build a plain building record — the minimal shape findSpot()/occupiedSet() need. */
let nextId = 1;
function bld(spec, x, y) {
  return { id: nextId++, spec, x, y };
}

/**
 * The Aaron-shape state: a small residential cluster (13 homes, like the real
 * report) sitting INSIDE a solid built-out rectangle [2,150]x[2,150] (the
 * "motorway-heavy city" stand-in), with the rest of the 440x260 map
 * (anything with x>150 or y>150) completely free.
 *
 * housingCentroid() over the 13 homes at x=10..22,y=10 puts the search
 * window's initial (and, for the OLD algorithm, ONLY) 90x90 pass entirely
 * inside the solid built rectangle -> the un-expanded pass fails. Only
 * widening (win 90 -> 180) reaches past x=150/y=150 into free land. So this
 * state isolates the expansion loop specifically — the ONLY thing that
 * changed vs the pre-fix algorithm once builtUpCentroid was reverted.
 */
function aaronShapeState() {
  const base = initialState();
  const buildings = [];
  const BUILT_MAX = 150;
  const resCells = new Set();
  for (let i = 0; i < 13; i++) resCells.add(`${10 + i},10`);
  for (let x = 2; x <= BUILT_MAX; x++) {
    for (let y = 2; y <= BUILT_MAX; y++) {
      const key = `${x},${y}`;
      buildings.push(bld(resCells.has(key) ? 'res_hut' : 'com_shop', x, y));
    }
  }
  return { ...base, buildings, population: 1000, funds: 1_000_000_000 };
}

/** Faithful reimplementation of the PRE-FIX findSpot() algorithm: fixed WIN=90
 *  window, stride 2, centred on housingCentroid(), no widening on an empty
 *  pass. Used ONLY as a red-proof control, never as production logic. */
function oldFixedWindowFindSpot(s, specId) {
  const sp = SPECS[specId];
  if (!sp) return null;
  const occ = occupiedSet(s);
  let hx = 0;
  let hy = 0;
  let n = 0;
  for (const b of s.buildings) {
    if (SPECS[b.spec]?.kind !== 'residential') continue;
    hx += b.x;
    hy += b.y;
    n++;
  }
  const hc = n === 0 ? { x: 150, y: 78 } : { x: hx / n, y: hy / n };

  const WIN = 90;
  const xa = Math.max(2, Math.floor(hc.x - WIN / 2));
  const ya = Math.max(2, Math.floor(hc.y - WIN / 2));
  const xb = Math.min(MAP_W - sp.w - 2, xa + WIN);
  const yb = Math.min(MAP_H - sp.h - 2, ya + WIN);
  for (let y = ya; y <= yb; y += 2) {
    for (let x = xa; x <= xb; x += 2) {
      if (fits(occ, sp.w, sp.h, x, y)) return { x, y };
    }
  }
  return null;
}

test('BUG-593 RED-PROOF: the pre-fix fixed-window algorithm fails on the Aaron-shape state', () => {
  const s = aaronShapeState();
  const residentialCount = s.buildings.filter((b) => SPECS[b.spec].kind === 'residential').length;
  assert.equal(residentialCount, 13, 'sanity: 13-home cluster, matching the real report');

  const oldResult = oldFixedWindowFindSpot(s, 'park');
  assert.equal(oldResult, null, 'the OLD algorithm must fail here — this is the reported bug reproduced');
});

test('BUG-593 FIX: findSpot() finds a site on the Aaron-shape state (widen-before-giving-up)', () => {
  const s = aaronShapeState();
  const spot = findSpot(s, 'park');
  assert.ok(spot, 'findSpot() must find a site once free land exists beyond the initial window');

  const sp = SPECS.park;
  const occ = occupiedSet(s);
  assert.ok(fits(occ, sp.w, sp.h, spot.x, spot.y), 'the returned spot must actually be free');

  // It must be OUTSIDE the solid built rectangle — i.e. the search really did
  // widen past the small residential corner into the free 94.5%-of-the-map
  // land, not merely find a coincidental gap inside the built mass (there is
  // none: the built rectangle [2,150]x[2,150] is filled solid by construction).
  assert.ok(
    spot.x > 150 || spot.y > 150,
    `expected a spot outside the solid built rectangle, got (${spot.x},${spot.y})`
  );
});

test('BUG-593: findSpot() is deterministic — same state, same answer, twice', () => {
  const s = aaronShapeState();
  const first = findSpot(s, 'park');
  const second = findSpot(s, 'park');
  assert.deepEqual(first, second, 'findSpot() must be a pure function of state — no hidden randomness/order dependence');
  assert.ok(first, 'both calls should find the same real site (not both null)');
});

/** Fill every reachable cell of the map (the window math clamps candidates to
 *  [2, MAP_W-w-2] x [2, MAP_H-h-2]) with a 1x1 non-residential spec, so a 1x1
 *  target genuinely has nowhere to go anywhere on the map. */
function fullMapState(skip) {
  const base = initialState();
  const buildings = [];
  for (let x = 2; x <= MAP_W - 3; x++) {
    for (let y = 2; y <= MAP_H - 3; y++) {
      if (skip && skip.x === x && skip.y === y) continue;
      buildings.push(bld('com_shop', x, y));
    }
  }
  return { ...base, buildings, population: 60_000, funds: 1_000_000_000, unlockedAll: true, administrationState: null };
}

test('BUG-593: a genuinely-full map returns null from findSpot()', () => {
  const s = fullMapState();
  assert.equal(findSpot(s, 'park'), null, 'a fully-tiled map has nowhere left for a 1x1 building');
});

test('BUG-593 (round moderate #4): the stride-1 final pass finds a single free tile at an ODD offset that stride-2 would miss', () => {
  // The final, whole-map-covering pass always starts its scan at xa=ya=2
  // (clamped there by construction whenever the window truly covers the
  // whole map — see findSpot()'s coversWholeMap check). A stride-2 scan from
  // an even origin only ever visits EVEN (x,y). (301,201) is odd/odd, so a
  // stride-2-only search would walk right past it and wrongly report "no
  // free area" even though the map is 99.999...% full, not 100%.
  const FREE = { x: 301, y: 201 };
  const s = fullMapState(FREE);

  const spot = findSpot(s, 'park');
  assert.deepEqual(
    spot,
    FREE,
    'the stride-1 final pass must find the one genuinely free odd-offset tile'
  );

  // Control: prove the claim about stride-2 actually holds for this exact
  // state (not just asserted in a comment) — a stride-2-only scan of the
  // same fully-clamped final window never lands on (301,201).
  let stride2WouldFindIt = false;
  for (let y = 2; y <= MAP_H - 3; y += 2) {
    for (let x = 2; x <= MAP_W - 3; x += 2) {
      if (x === FREE.x && y === FREE.y) stride2WouldFindIt = true;
    }
  }
  assert.equal(stride2WouldFindIt, false, 'sanity: (301,201) is unreachable at stride 2 from an even origin');
});

test('BUG-593: noBuildableSiteReason() states WHY, not a bare map-full-sounding string', () => {
  const reason = noBuildableSiteReason('park');
  const sp = SPECS.park;
  assert.match(reason, /no free \d+x\d+ area on the map for/, 'must name the footprint size, not just "no site"');
  assert.ok(reason.includes(`${sp.w}x${sp.h}`), 'must cite the actual spec dimensions');
  assert.ok(reason.includes(sp.name), 'must cite the actual spec name');
  assert.notEqual(reason, 'no buildable site found', 'must NOT be the old bare string that read as map-full');

  // Unknown spec id degrades to the old bare message rather than throwing —
  // defensive fallback, exercised because resolveDemand's specId always comes
  // from a validated plan but this helper is a small standalone utility.
  assert.equal(noBuildableSiteReason('not-a-real-spec'), 'no buildable site found');
});

test('BUG-593: resolveDemand threads the informative reason through placeNotice on a full map', () => {
  const base = initialState();
  const shortfallOnly = { ...base, population: 60_000, unlockedAll: true, funds: 1_000_000_000, administrationState: null };
  const plan = demandFixPlan(shortfallOnly).find((p) => p.serviceKey === 'cleanwater');
  assert.ok(plan, 'sanity: a cleanwater shortfall plan exists at population 60,000 with no buildings');

  // Now saturate the map so the SAME plan's placements have nowhere to go.
  const buildings = [];
  for (let x = 2; x <= MAP_W - 3; x++) {
    for (let y = 2; y <= MAP_H - 3; y++) {
      buildings.push(bld('com_shop', x, y));
    }
  }
  const full = { ...shortfallOnly, buildings };

  const result = reducer(full, { type: 'resolveDemand', serviceKey: 'cleanwater' });
  assert.equal(result.placeNotice.includes('no buildable site found'), false, 'must not be the bare old string');

  const sp = SPECS[plan.specId];
  assert.ok(
    result.placeNotice.includes(`${sp.w}x${sp.h}`) && result.placeNotice.includes(sp.name),
    `placeNotice should cite the spec's footprint/name; got: ${result.placeNotice}`
  );
});

/** True if any cell of a w×h footprint at (x,y) is orthogonally adjacent to a
 *  road tile — mirrors roadConnect.ts's touchesRoad(), reimplemented here
 *  (not imported: it's module-private) purely to VERIFY the regression, never
 *  as production logic. */
function touchesRoad(roads, x, y, w, h) {
  for (let dx = 0; dx < w; dx++) {
    for (let dy = 0; dy < h; dy++) {
      const bx = x + dx;
      const by = y + dy;
      if (
        roads.has(`${bx},${by - 1}`) ||
        roads.has(`${bx - 1},${by}`) ||
        roads.has(`${bx + 1},${by}`) ||
        roads.has(`${bx},${by + 1}`)
      ) {
        return true;
      }
    }
  }
  return false;
}

test('BUG-593 REGRESSION (independent-round isolation): resolveDemand on a FRESH game places power road-adjacent, like HEAD', () => {
  // No override of `buildings` — this is exactly initialState()'s real
  // starter road/rail network, zero residential buildings (housingCentroid()
  // falls back to its hardcoded (150,78), which sits near that real network —
  // this is the behaviour the rejected builtUpCentroid draft broke, because an
  // ALL-buildings bbox centroid is dragged to ~the map centre by the ~1,855
  // map-spanning motorway/rail/HS1 tiles regardless of where the real roads
  // actually cluster).
  const base = initialState();
  const s = { ...base, population: 10_000, unlockedAll: true, funds: 1_000_000_000, administrationState: null };

  const plan = demandFixPlan(s).find((p) => p.serviceKey === 'power');
  assert.ok(plan, 'sanity: a power shortfall plan exists at population 10,000 on a fresh game');
  assert.ok(plan.count > 0, 'sanity: the plan actually calls for placements');

  const beforeIds = new Set(s.buildings.map((b) => b.id));
  const result = reducer(s, { type: 'resolveDemand', serviceKey: 'power' });

  const placed = result.buildings.filter((b) => !beforeIds.has(b.id) && b.spec === plan.specId);
  assert.equal(placed.length, plan.count, 'the full batch must place — a fresh game has plenty of free land near the real roads');

  const roads = roadTileSetOf(result);
  const sp = SPECS[plan.specId];
  const adjacentCount = placed.filter((b) => touchesRoad(roads, b.x, b.y, sp.w, sp.h)).length;
  assert.equal(
    adjacentCount,
    placed.length,
    `every placement must be road-adjacent on a fresh game (matches HEAD); got ${adjacentCount}/${placed.length}`
  );
});
