// FEAT-1972079910 inc4: AC-5 bend legality tests.
// Covers legalisePath/isBendLegal/bendRadiiOf (roadTracker.ts), the
// minBendRadiusTilesForTier fail-closed loader (data.ts), and the
// placeRoadPath reducer's bendLegal enforcement (engine.ts).
//
// REWORK (post r1 REJECT, BUG-1014/BUG-1015, LEAD RULING v2): legalisePath no
// longer takes a raw path to smooth -- it PLANS a fresh staircase from
// (anchorX, anchorY) to (cursorX, cursorY) given minRun, returning
// { tiles, endX, endY, snapped }. These tests assert the new contract
// directly against an INDEPENDENT re-derivation of the rule (never against
// bendRadiiOf/isBendLegal alone, which would only catch a shared
// misunderstanding with the algorithm they were both built from).
//
// RED proofs: every assertion below is designed to fail if the corresponding
// mechanism is broken -- see the report/BOW comment for the mutation
// RED/GREEN log (scratch-copy mutations against roadTracker.ts, data.ts, and
// data/roads.json + validate-traffic-tables.mjs).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { computePath, legalisePath, isBendLegal, bendRadiiOf } from '../src/sim/roadTracker.ts';
import { minBendRadiusTilesForTier, ROAD_TIER_SPECS } from '../src/sim/data.ts';
import { initialState, reducer, ROAD_BEND_ILLEGAL_MESSAGE } from '../src/sim/engine.ts';

const ALL_TIERS = /** @type {const} */ ([1, 2, 3, 4, 5]);

function board(buildings) {
  const base = initialState();
  let maxId = 0;
  for (const b of buildings) if (b.id > maxId) maxId = b.id;
  return { ...base, unlockedAll: true, buildings, nextId: maxId + 1, funds: base.funds + 20_000_000 };
}

function mulberry32(a) {
  return function () {
    a |= 0; a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/**
 * Independent legality checker per the LEAD RULING'S bend definition: legal
 * iff every corner's run (the SHORTER of its two adjacent leg lengths, in
 * tile-steps) is >= minRun, and no corner is a 180-degree reversal. Written
 * fresh from the ruling text, not by calling bendRadiiOf/isBendLegal.
 */
function independentLegs(p) {
  const legs = [];
  for (let i = 1; i < p.length; i++) {
    const dx = p[i].x - p[i - 1].x;
    const dy = p[i].y - p[i - 1].y;
    if (Math.abs(dx) + Math.abs(dy) !== 1) return null; // not a 4-connected unit step
    const d = dx + ',' + dy;
    const last = legs[legs.length - 1];
    if (last && last.d === d) last.n++;
    else legs.push({ d, n: 1 });
  }
  return legs;
}

function independentCheck(p, minRun) {
  const legs = independentLegs(p);
  if (legs === null) return 'not 4-connected';
  for (let i = 1; i < legs.length; i++) {
    const [ax, ay] = legs[i - 1].d.split(',').map(Number);
    const [bx, by] = legs[i].d.split(',').map(Number);
    if (ax === -bx && ay === -by) return 'reversal at corner ' + i;
    if (Math.min(legs[i - 1].n, legs[i].n) < minRun) return 'corner ' + i + ' below minRun';
  }
  return null;
}

/** Independent re-derivation of the LEAD RULING's snap rule for one axis. */
function independentSnap(delta, minRun) {
  const abs = Math.abs(delta);
  if (abs === 0 || abs >= minRun) return delta;
  const dropChange = abs;
  const extendChange = minRun - abs;
  return dropChange <= extendChange ? 0 : Math.sign(delta) * minRun;
}

// A real Bresenham "hairpin": computePath's own near-diagonal crossover
// staircase (see roadTracker.ts's greedy adx>=ady tie-break) naturally
// produces several 1-tile zigzag bends -- a genuine hairpin mouse path, not
// a synthetic literal fixture. Kept as a definitional sanity fixture only
// (legalisePath no longer consumes an arbitrary path -- see fuzz below).
const HAIRPIN = computePath(0, 0, 30, 17);

test('sanity: the hairpin fixture actually contains tight (radius < 2) bends', () => {
  const radii = bendRadiiOf(HAIRPIN);
  assert.ok(radii.some((r) => r < 2), 'HAIRPIN must contain at least one bend tighter than radius 2 (else this fixture proves nothing)');
});

// --- AC-5 core + BUG-1014 fuzz: 2,000 seeded (anchor, cursor) pairs per tier -

test('AC-5: 2,000 seeded (anchor, cursor) pairs per tier -- legal, endpoint-correct, idempotent, no wandering', () => {
  const report = [];
  for (const tier of ALL_TIERS) {
    const minRun = minBendRadiusTilesForTier(tier);
    assert.ok(Number.isInteger(minRun) && minRun >= 1, 'minRun is a positive integer sourced from data/roads.json');

    let reached = 0, snappedCount = 0;
    for (let s = 0; s < 2000; s++) {
      const rnd = mulberry32(s * 7919 + tier * 104729);
      const anchorX = Math.floor(rnd() * 200) - 100;
      const anchorY = Math.floor(rnd() * 200) - 100;
      const dx = Math.floor(rnd() * (4 * minRun + 5)) - (2 * minRun + 2);
      const dy = Math.floor(rnd() * (4 * minRun + 5)) - (2 * minRun + 2);
      const cursorX = anchorX + dx;
      const cursorY = anchorY + dy;

      const out = legalisePath(anchorX, anchorY, cursorX, cursorY, minRun);

      // Legal by an INDEPENDENT checker.
      const bad = independentCheck(out.tiles, minRun);
      assert.equal(bad, null, `tier ${tier} seed ${s}: illegal output -- ${bad}`);

      // Anchor always preserved; tiles land exactly on the reported endpoint.
      assert.deepEqual(out.tiles[0], { x: anchorX, y: anchorY }, `tier ${tier} seed ${s}: anchor preserved`);
      assert.deepEqual(out.tiles[out.tiles.length - 1], { x: out.endX, y: out.endY }, `tier ${tier} seed ${s}: tiles land on reported endpoint`);

      // No wandering: step count == Manhattan distance of the planned endpoint.
      const manhattan = Math.abs(out.endX - anchorX) + Math.abs(out.endY - anchorY);
      assert.equal(out.tiles.length - 1, manhattan, `tier ${tier} seed ${s}: length must equal Manhattan distance of the planned endpoint (no wandering)`);

      // Reachability + snap rule, independently re-derived.
      if (dx === 0 || dy === 0 || (Math.abs(dx) >= minRun && Math.abs(dy) >= minRun)) {
        assert.deepEqual({ x: out.endX, y: out.endY }, { x: cursorX, y: cursorY }, `tier ${tier} seed ${s}: reachable cursor must be reached exactly`);
        assert.equal(out.snapped, false, `tier ${tier} seed ${s}: reachable cursor must not report snapped`);
        reached++;
      } else {
        assert.equal(out.snapped, true, `tier ${tier} seed ${s}: unreachable cursor must report snapped`);
        const sx = independentSnap(dx, minRun);
        const sy = independentSnap(dy, minRun);
        assert.deepEqual({ x: out.endX, y: out.endY }, { x: anchorX + sx, y: anchorY + sy }, `tier ${tier} seed ${s}: snap rule mismatch`);
        snappedCount++;
      }

      // Idempotence: re-planning the reported endpoint as the new cursor
      // reproduces the SAME path (LEAD RULING: "re-planning a planned
      // path's endpoints returns the same path").
      const again = legalisePath(anchorX, anchorY, out.endX, out.endY, minRun);
      assert.deepEqual(again.tiles, out.tiles, `tier ${tier} seed ${s}: idempotent (tiles)`);
      assert.deepEqual({ x: again.endX, y: again.endY }, { x: out.endX, y: out.endY }, `tier ${tier} seed ${s}: idempotent (endpoint)`);

      // No duplicate consecutive tiles (self-crossing would break 4-connectivity).
      for (let i = 1; i < out.tiles.length; i++) {
        assert.ok(out.tiles[i].x !== out.tiles[i - 1].x || out.tiles[i].y !== out.tiles[i - 1].y, `tier ${tier} seed ${s}: no duplicate consecutive tile`);
      }
    }
    report.push({ tier, spec: ROAD_TIER_SPECS[tier], minRun, reached, snapped: snappedCount });
  }
  console.log('BUG-1014 FUZZ (2,000 seeded (anchor,cursor) pairs per tier, 10,000 total):');
  for (const r of report) {
    console.log(`  tier ${r.tier} ${r.spec} minRun=${r.minRun}: endpoint reached ${r.reached}/2000, snapped ${r.snapped}/2000`);
  }
});

// --- Tier-1 identity: legalisePath(minRun=1) === computePath byte-for-byte -

test('AC-5 regression: minRun=1 reproduces computePath byte-for-byte over 500 seeded pairs', () => {
  const minRun1 = ALL_TIERS.find((t) => minBendRadiusTilesForTier(t) === 1);
  assert.ok(minRun1 !== undefined, 'fixture assumption: some real tier has minRun 1 (read from data, not assumed)');
  for (let s = 0; s < 500; s++) {
    const rnd = mulberry32(s * 12345);
    const ax = Math.floor(rnd() * 100) - 50;
    const ay = Math.floor(rnd() * 100) - 50;
    const cx = ax + Math.floor(rnd() * 80) - 40;
    const cy = ay + Math.floor(rnd() * 80) - 40;
    const out = legalisePath(ax, ay, cx, cy, 1);
    const raw = computePath(ax, ay, cx, cy);
    assert.deepEqual(out.tiles, raw, `seed ${s}: minRun=1 must reproduce computePath exactly for (${ax},${ay})->(${cx},${cy})`);
    assert.deepEqual({ x: out.endX, y: out.endY }, { x: cx, y: cy }, `seed ${s}: minRun=1 endpoint is always the cursor`);
    assert.equal(out.snapped, false, `seed ${s}: minRun=1 never snaps`);
  }
  console.log('✓ AC-5 regression: minRun=1 byte-identical to computePath over 500 seeded pairs');
});

// --- Exact leg-split shape: remainder goes to the EARLIEST legs (pinned) ---
// MUTATION SURVIVOR NOTE: a remainder-to-LAST-legs split is still legal,
// still lands on the endpoint, and is still idempotent -- none of that is
// observable through legality/endpoint/idempotence alone. This test pins
// the EXACT tile sequence for a k>1, remainder>0 case so a swapped
// distribution order is caught (determinism/replay-shape, GR#21).

test('AC-5: leg-split remainder is deterministically front-loaded onto the earliest legs (exact shape pinned)', () => {
  // dx=22, dy=22, minRun=5: k = min(floor(22/5), floor(22/5)) = 4.
  // splitEven(22, 4) = base 5, rem 2 -> [6, 6, 5, 5] (first two legs get +1).
  const minRun = 5;
  const out = legalisePath(0, 0, 22, 22, minRun);
  assert.equal(out.snapped, false, 'sanity: 22 is an exact multiple boundary case, fully reachable');
  assert.deepEqual({ x: out.endX, y: out.endY }, { x: 22, y: 22 });
  // Reconstruct the expected exact path from the documented leg lengths
  // (x-first since adx===ady ties to x): x6, y6, x6, y6, x5, y5, x5, y5.
  const expectedLegLens = [6, 6, 6, 6, 5, 5, 5, 5];
  let ex = 0, ey = 0;
  const expected = [{ x: 0, y: 0 }];
  for (let i = 0; i < expectedLegLens.length; i++) {
    const len = expectedLegLens[i];
    const isX = i % 2 === 0;
    for (let j = 0; j < len; j++) {
      if (isX) ex += 1; else ey += 1;
      expected.push({ x: ex, y: ey });
    }
  }
  assert.deepEqual(out.tiles, expected, 'the exact tile sequence must front-load the remainder onto the earliest x/y legs, not the last');
});

// --- Straight-line / no-bend cases are always legal, any minRun ------------

test('AC-5: a straight leg (dx===0 or dy===0) is always legal at every minRun and never snapped', () => {
  for (const tier of ALL_TIERS) {
    const minRun = minBendRadiusTilesForTier(tier);
    for (const [dx, dy] of [[0, 0], [1, 0], [0, 1], [-7, 0], [0, -7], [40, 0], [0, 40]]) {
      const out = legalisePath(10, 10, 10 + dx, 10 + dy, minRun);
      assert.equal(out.snapped, false, `tier ${tier} dx=${dx} dy=${dy}: straight leg never snaps`);
      assert.deepEqual({ x: out.endX, y: out.endY }, { x: 10 + dx, y: 10 + dy });
      assert.equal(independentCheck(out.tiles, minRun), null, `tier ${tier} dx=${dx} dy=${dy}: straight leg always legal`);
    }
  }
});

// --- Reducer refusal: illegal path -> zero placement, zero ledger movement -

test('AC-5: placeRoadPath reducer refuses an illegal (hand-crafted, unlegalised) bendLegal path', () => {
  const before = board([]);
  // A hand-built 1-step jog: illegal for every tier above 1.
  const tiles = [{ x: 5, y: 5 }, { x: 6, y: 5 }, { x: 6, y: 6 }, { x: 7, y: 6 }, { x: 8, y: 6 }];
  const minRun5 = minBendRadiusTilesForTier(5);
  assert.equal(isBendLegal(tiles, minRun5), false, 'sanity: the fixture IS illegal for the tightest tier');

  const action = { type: 'placeRoadPath', spec: 'm20', tiles, bendLegal: true };
  const after = reducer(before, action);

  assert.equal(after.buildings.length, before.buildings.length, 'zero tiles placed');
  assert.equal(after.funds, before.funds, 'funds unchanged (no partial spend)');
  assert.equal(after.placeNotice, ROAD_BEND_ILLEGAL_MESSAGE, 'placeNotice names the bend-legality refusal');
});

test('AC-5: placeRoadPath reducer places a legalised (planned) path normally (no false-positive refusal)', () => {
  const s = board([]);
  const minRun = minBendRadiusTilesForTier(2);
  const legal = legalisePath(0, 0, 40, 22, minRun);
  const action = { type: 'placeRoadPath', spec: ROAD_TIER_SPECS[2], tiles: legal.tiles, bendLegal: true };
  const after = reducer(s, action);
  assert.ok(after.buildings.length > 0, 'a planned legal path places tiles normally under bendLegal:true');
  assert.notEqual(after.placeNotice, ROAD_BEND_ILLEGAL_MESSAGE, 'no bend-legality refusal on a legal path');
});

test('AC-5 replay-compat (R4): a pre-inc4 action (no bendLegal field) skips the check entirely', () => {
  const s = board([]);
  // The raw hairpin, illegal for every tier, dispatched WITHOUT bendLegal --
  // must place exactly as inc1-inc3 always did (old journal entries replay
  // byte-identically; this rule postdates them).
  const action = { type: 'placeRoadPath', spec: 'road', tiles: HAIRPIN };
  const after = reducer(s, action);
  assert.ok(after.buildings.length > 0, 'a pre-inc4 action (bendLegal undefined) is never refused by the AC-5 check');
  assert.notEqual(after.placeNotice, ROAD_BEND_ILLEGAL_MESSAGE, 'no bend-legality notice without the marker');
});

// --- BUG-1016: a malformed (non-4-connected) bendLegal path is REFUSED, never a bare Error --

test('BUG-1016 fix: a bendLegal path whose dedup drops to non-4-connected is refused via MET-V961, not a thrown Error', () => {
  // Out and back along the same row, then a step off it: the return tiles are
  // all duplicates and get dropped by AC-6e's dedup, leaving (63,60) followed
  // by (61,59) -- two tiles apart, not 4-connected.
  const tiles = [];
  let x = 60;
  const y = 60;
  tiles.push({ x, y });
  for (let i = 0; i < 3; i++) { x += 1; tiles.push({ x, y }); } // -> (63,60)
  for (let i = 0; i < 2; i++) { x -= 1; tiles.push({ x, y }); } // -> (61,60), both duplicates
  tiles.push({ x: 61, y: 59 }); // a NEW tile off the row

  const before = board([]);
  let after;
  assert.doesNotThrow(() => {
    after = reducer(before, { type: 'placeRoadPath', spec: ROAD_TIER_SPECS[1], tiles, bendLegal: true });
  }, 'BUG-1016: a malformed bendLegal path must never throw a bare Error out of the reducer');
  assert.equal(after.buildings.length, before.buildings.length, 'BUG-1016: zero tiles placed on the malformed path');
  assert.equal(after.funds, before.funds, 'BUG-1016: zero funds movement');
  assert.equal(after.placeNotice, ROAD_BEND_ILLEGAL_MESSAGE, 'BUG-1016: refused via the registered MET-V961 notice');
});

test('AC-5: a non-road spec with bendLegal:true is never bend-checked (tier 0 must not reach the MET-V960 loader)', () => {
  const s = board([]);
  const tiles = [{ x: 40, y: 40 }, { x: 41, y: 40 }, { x: 41, y: 41 }];
  assert.doesNotThrow(() => reducer(s, { type: 'placeRoadPath', spec: 'park', tiles, bendLegal: true }));
});

// --- Loader fail-closed (GR#7/GR#15) ----------------------------------------

test('minBendRadiusTilesForTier fails closed (MET-V960) for a tier with no class mapping', () => {
  let caught;
  try {
    minBendRadiusTilesForTier(/** @type {any} */ (99));
  } catch (e) {
    caught = e;
  }
  assert.ok(caught, 'an unmapped tier must throw, never silently return a number');
  assert.equal(caught.code, 'MET-V960', 'the thrown error carries the registered fail-closed code (codedError.code), not a silent 0');
});

test('minBendRadiusTilesForTier returns a positive integer for every real tier', () => {
  for (const tier of ALL_TIERS) {
    const v = minBendRadiusTilesForTier(tier);
    assert.ok(Number.isInteger(v) && v > 0, `tier ${tier} minRun must be a positive integer, got ${v}`);
  }
});

test('minBendRadiusTilesForTier is monotone non-decreasing across tiers (GR#15 data, not restated here)', () => {
  let prev = 0;
  for (const tier of ALL_TIERS) {
    const v = minBendRadiusTilesForTier(tier);
    assert.ok(v >= prev, `tier ${tier} minRun ${v} must be >= previous tier's ${prev}`);
    prev = v;
  }
});
