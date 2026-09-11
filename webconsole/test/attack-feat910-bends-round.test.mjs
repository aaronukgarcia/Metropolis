/**
 * FEAT-1972079910 inc4 (AC-5) — INDEPENDENT DESTRUCTIVE ROUND (opus-round-feat910-bends)
 * REWORK PASS (r2, GR#23 attacker != author): r1 REJECTED this build over
 * BUG-1014 (legalisePath collapsed diagonal drags into a straight line at
 * tier 2+, farthest legal path instead of closest) and BUG-1015 (reversal
 * cancellation could shrink an already-emitted leg below minRun). The LEAD
 * RULING replaced the smooth-after-the-fact algorithm with a PLAN-from-
 * endpoints algorithm (legalisePath(anchorX, anchorY, cursorX, cursorY,
 * minRun) -> { tiles, endX, endY, snapped }), which structurally removes
 * both bug classes (there is no longer an arbitrary input path to smooth or
 * shrink). This file stays the independent regression suite for that fix:
 * the two bugs' pinned/skipped adversarial fuzzes are un-skipped (they now
 * pass because the new algorithm ignores the adversarial middle of any
 * input entirely), and the two "pin the current buggy behaviour" tests are
 * deleted (that behaviour no longer exists). BUG-1016's pin is likewise
 * replaced with a fix-verification test now that the reducer refuses a
 * malformed path via MET-V961 instead of throwing.
 *
 * Everything here is an INDEPENDENT re-derivation: the legality checker
 * below (`independentCheck`) is written from the LEAD RULING text, NOT by
 * calling the build's own bendRadiiOf/isBendLegal — the builder's tests
 * assert legality with the same function the algorithm is built around,
 * which cannot catch a shared misunderstanding. Minimum-run values are
 * always READ from data (GR#15).
 */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { computePath, legalisePath, isBendLegal, bendRadiiOf } from '../src/sim/roadTracker.ts';
import { minBendRadiusTilesForTier, ROAD_TIER_SPECS, ROAD_CLASS_ID_OF_TIER } from '../src/sim/data.ts';
import { ROAD_CLASS_ID_OF_TIER as REEXPORTED } from '../src/sim/trafficAssignment.ts';
import { initialState, reducer, ROAD_BEND_ILLEGAL_MESSAGE } from '../src/sim/engine.ts';

const TIERS = [1, 2, 3, 4, 5];

// ---------------------------------------------------------------- helpers --

function mulberry32(a) {
  return function () {
    a |= 0; a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
const DIRS = [[1, 0], [-1, 0], [0, 1], [0, -1]];

/** Independent leg decomposition. Returns null when the path is not 4-connected. */
function independentLegs(p) {
  const legs = [];
  for (let i = 1; i < p.length; i++) {
    const dx = p[i].x - p[i - 1].x;
    const dy = p[i].y - p[i - 1].y;
    if (Math.abs(dx) + Math.abs(dy) !== 1) return null;
    const d = dx + ',' + dy;
    const last = legs[legs.length - 1];
    if (last && last.d === d) last.n++;
    else legs.push({ d, n: 1 });
  }
  return legs;
}

/**
 * Independent legality verdict per the LEAD RULING: "legal = every corner run
 * >= the tier minimum AND no 180-degree reversal anywhere", the corner's run
 * being the SHORTER of the two straight runs either side of it.
 * Returns null when legal, else a reason string.
 */
function independentCheck(p, minRun) {
  const legs = independentLegs(p);
  if (legs === null) return 'not 4-connected (or a repeated consecutive tile)';
  for (let i = 1; i < legs.length; i++) {
    const [ax, ay] = legs[i - 1].d.split(',').map(Number);
    const [bx, by] = legs[i].d.split(',').map(Number);
    if (ax === -bx && ay === -by) return '180-degree reversal at corner ' + i;
    if (Math.min(legs[i - 1].n, legs[i].n) < minRun) {
      return 'corner ' + i + ' run min(' + legs[i - 1].n + ',' + legs[i].n + ') < minRun ' + minRun;
    }
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

/** Seeded generator over the shapes the brief names. Only the FIRST and LAST
 * tile matter to the new plan-from-endpoints contract; the shape of the
 * middle is exactly what BUG-1015's domain used to exploit and now cannot,
 * because legalisePath never looks at it. */
function generate(mode, rnd) {
  const p = [];
  let x = 0, y = 0;
  if (mode === 'cursor') return computePath(0, 0, Math.floor(rnd() * 60) - 30, Math.floor(rnd() * 60) - 30);
  if (mode === 'offmap') return computePath(-5000, -5000, -5000 + Math.floor(rnd() * 40) - 20, -5000 + Math.floor(rnd() * 40) - 20);
  if (mode === 'single') return [{ x: Math.floor(rnd() * 10), y: Math.floor(rnd() * 10) }];
  p.push({ x, y });
  if (mode === 'walk') {
    const n = 1 + Math.floor(rnd() * 30);
    for (let i = 0; i < n; i++) { const [dx, dy] = DIRS[Math.floor(rnd() * 4)]; x += dx; y += dy; p.push({ x, y }); }
  } else if (mode === 'legs') {
    const nl = 1 + Math.floor(rnd() * 8);
    for (let i = 0; i < nl; i++) {
      const [dx, dy] = DIRS[Math.floor(rnd() * 4)];
      const len = 1 + Math.floor(rnd() * 7);
      for (let j = 0; j < len; j++) { x += dx; y += dy; p.push({ x, y }); }
    }
  } else if (mode === 'zigzag') {
    const n = 1 + Math.floor(rnd() * 20);
    for (let i = 0; i < n; i++) { if (i % 2) y += 1; else x += 1; p.push({ x, y }); }
  } else if (mode === 'spiral') {
    let len = 1, di = 0;
    for (let k = 0; k < 8; k++) {
      const [dx, dy] = DIRS[di % 4];
      for (let j = 0; j < len; j++) { x += dx; y += dy; p.push({ x, y }); }
      di++; if (k % 2) len++;
    }
  } else if (mode === 'hairpin') {
    const n = 3 + Math.floor(rnd() * 10);
    for (let i = 0; i < n; i++) { y += 1; p.push({ x, y }); }
    for (let i = 0; i < n; i++) { x += 1; p.push({ x, y }); }
    for (let i = 0; i < n; i++) { y -= 1; p.push({ x, y }); }
  } else if (mode === 'straight') {
    const n = 1 + Math.floor(rnd() * 40);
    for (let i = 0; i < n; i++) { x += 1; p.push({ x, y }); }
  } else if (mode === 'L') {
    const a = 1 + Math.floor(rnd() * 20), b = 1 + Math.floor(rnd() * 20);
    for (let i = 0; i < a; i++) { x += 1; p.push({ x, y }); }
    for (let i = 0; i < b; i++) { y += 1; p.push({ x, y }); }
  }
  return p;
}

function plan(input, minRun) {
  const a = input[0];
  const b = input[input.length - 1];
  return legalisePath(a.x, a.y, b.x, b.y, minRun);
}

function board(buildings = []) {
  const base = initialState();
  let maxId = 0;
  for (const b of buildings) if (b.id > maxId) maxId = b.id;
  return { ...base, unlockedAll: true, buildings, nextId: maxId + 1, funds: base.funds + 50000000 };
}

// ------------------------------------------------- provenance / SSOT moves --

test('ATTACK provenance: ROAD_CLASS_ID_OF_TIER relocation is byte-identical and re-exported', () => {
  // The build MOVED this table data.ts <- trafficAssignment.ts. The re-export must be
  // the SAME object, not a second literal that can silently drift (GR#3).
  assert.equal(REEXPORTED, ROAD_CLASS_ID_OF_TIER, 're-export must be the identical object, not a copy');
  assert.deepEqual({ ...ROAD_CLASS_ID_OF_TIER }, {
    1: 'residential_street', 2: 'avenue_2_plus_2', 3: 'two_lane', 4: 'dual_carriageway', 5: 'motorway',
  }, 'mapping unchanged from the pre-move trafficAssignment.ts literal');
  assert.ok(Object.isFrozen(ROAD_CLASS_ID_OF_TIER), 'still frozen');
  for (const t of TIERS) assert.ok(ROAD_TIER_SPECS[t], 'tier ' + t + ' has a spec id');
});

test('ATTACK data (GR#15): every real road spec resolves to a positive-integer minRun, monotone, no literals here', () => {
  const seen = [];
  for (const t of TIERS) {
    const v = minBendRadiusTilesForTier(t);
    assert.ok(Number.isInteger(v) && v > 0, 'tier ' + t + ' minRun must be a positive integer');
    seen.push(v);
  }
  for (let i = 1; i < seen.length; i++) assert.ok(seen[i] >= seen[i - 1], 'non-decreasing across tiers');
  assert.ok(seen[seen.length - 1] > seen[0], 'the table must actually vary by tier (a flat table hides tier mix-ups)');
});

test('ATTACK loader fail-closed: unmapped tier throws MET-V960, never a silent 0', () => {
  for (const bogus of [0, 6, 99, -1, 1.5, NaN]) {
    let caught = null;
    try { minBendRadiusTilesForTier(bogus); } catch (e) { caught = e; }
    assert.ok(caught, 'tier ' + bogus + ' must throw');
    assert.equal(caught.code, 'MET-V960', 'tier ' + bogus + ' must fail closed with the registered code');
  }
});

// ------------------------------------------------ semantics: the definition --

test('ATTACK definition: bendRadiiOf/isBendLegal agree with an INDEPENDENT checker on hand-built corners', () => {
  const p = [{ x: 0, y: 0 }];
  let x = 0, y = 0;
  for (let i = 0; i < 4; i++) { x += 1; p.push({ x, y }); }
  for (let i = 0; i < 2; i++) { y += 1; p.push({ x, y }); }
  assert.deepEqual(bendRadiiOf(p), [2], 'radius is the SHORTER adjacent run');
  assert.equal(isBendLegal(p, 2), true);
  assert.equal(isBendLegal(p, 3), false, 'shorter side (2) must veto minRun 3 even though the leading leg is 4');
  assert.equal(independentCheck(p, 3), 'corner 1 run min(4,2) < minRun 3');

  for (const [a, b] of [[3, 3], [3, 4], [4, 3]]) {
    const q = [{ x: 0, y: 0 }];
    let qx = 0, qy = 0;
    for (let i = 0; i < a; i++) { qx += 1; q.push({ x: qx, y: qy }); }
    for (let i = 0; i < b; i++) { qy += 1; q.push({ x: qx, y: qy }); }
    assert.equal(isBendLegal(q, 3), true, 'runs ' + a + '/' + b + ' legal at minRun 3 (boundary is inclusive)');
    assert.equal(isBendLegal(q, 4), Math.min(a, b) >= 4, 'runs ' + a + '/' + b + ' at minRun 4');
  }
});

test('ATTACK reversal: the SHARED checker flags a 180 at EVERY position (first corner, middle, last corner)', () => {
  // bendRadiiOf/isBendLegal (unchanged by the r2 rework) must still catch a
  // reversal wherever it sits in a hand-built path -- this is the checker
  // engine.ts's reducer enforces against the COMMITTED tile list with.
  const mk = (legs) => {
    const p = [{ x: 0, y: 0 }];
    let x = 0, y = 0;
    for (const [d, n] of legs) for (let i = 0; i < n; i++) {
      if (d === 'E') x += 1; else if (d === 'W') x -= 1; else if (d === 'S') y += 1; else y -= 1;
      p.push({ x, y });
    }
    return p;
  };
  const first = mk([['E', 3], ['W', 3], ['S', 3], ['E', 3]]);
  const mid = mk([['E', 3], ['S', 3], ['N', 3], ['E', 3]]);
  const last = mk([['E', 3], ['S', 3], ['E', 3], ['W', 3]]);
  for (const [name, p] of [['first', first], ['middle', mid], ['last', last]]) {
    assert.ok(bendRadiiOf(p).includes(0), name + '-leg reversal must report radius 0');
    assert.equal(isBendLegal(p, 1), false, name + '-leg reversal illegal even at minRun 1');
    assert.match(independentCheck(p, 1), /reversal/, name + ': independent checker agrees');
  }
});

// --------------------------------------------- fuzz over the REACHABLE domain --

test('ATTACK fuzz: 2,000 seeded cursor paths per tier — plan(anchor,cursor) over the domain MapView can actually produce', () => {
  // MapView now calls legalisePath(anchorX, anchorY, cursorX, cursorY, minRun)
  // directly -- the reachable domain is every (anchor, cursor) pair a mouse
  // drag can produce, including off-map coords.
  const report = [];
  for (const tier of TIERS) {
    const minRun = minBendRadiusTilesForTier(tier);
    let endReached = 0, endSnapped = 0, selfCross = 0;
    for (let s = 0; s < 2000; s++) {
      const rnd = mulberry32(s * 7919 + tier * 104729);
      const mode = ['cursor', 'offmap', 'single', 'straight', 'L'][s % 5];
      const input = generate(mode, rnd);
      const out = plan(input, minRun);

      const bad = independentCheck(out.tiles, minRun);
      assert.equal(bad, null, 'tier ' + tier + ' seed ' + s + ' (' + mode + '): illegal output — ' + bad);
      assert.deepEqual(out.tiles[0], { x: input[0].x, y: input[0].y }, 'tier ' + tier + ' seed ' + s + ': anchor preserved');
      const again = plan([out.tiles[0], { x: out.endX, y: out.endY }], minRun);
      assert.deepEqual(again.tiles, out.tiles, 'tier ' + tier + ' seed ' + s + ': idempotent');

      const seen = new Set();
      for (const t of out.tiles) { const k = t.x + ',' + t.y; if (seen.has(k)) { selfCross++; break; } seen.add(k); }

      const cursor = input[input.length - 1];
      if (out.endX === cursor.x && out.endY === cursor.y) endReached++; else endSnapped++;
    }
    assert.equal(selfCross, 0, 'tier ' + tier + ': a planned path must never cross itself');
    report.push({ tier: tier, spec: ROAD_TIER_SPECS[tier], minRun: minRun, endReached: endReached, endSnapped: endSnapped });
  }
  console.log('BUG-1014 FIX VERIFIED (2,000 seeded cursor paths per tier, 10,000 total):');
  for (const r of report) {
    console.log('  tier ' + r.tier + ' ' + r.spec + ' minRun=' + r.minRun + ': endpoint reached ' + r.endReached + '/2000, snapped ' + r.endSnapped + '/2000');
  }
});

test('ATTACK fuzz: 2,000 seeded ADVERSARIAL paths per tier (reversals, revisits, spirals) — BUG-1015 domain, now UN-SKIPPED', () => {
  // BUG-1015 was structural to smoothing an arbitrary input path. The r2
  // rework PLANS from (anchor, cursor) alone and never looks at the middle,
  // so an adversarial middle (reversals, revisits, spirals) can no longer
  // corrupt anything -- this fuzz now passes unconditionally.
  for (const tier of TIERS) {
    const minRun = minBendRadiusTilesForTier(tier);
    for (let s = 0; s < 2000; s++) {
      const rnd = mulberry32(s * 7919 + tier * 104729);
      const mode = ['walk', 'legs', 'zigzag', 'spiral', 'hairpin'][s % 5];
      const input = generate(mode, rnd);
      const out = plan(input, minRun);
      assert.equal(independentCheck(out.tiles, minRun), null, 'tier ' + tier + ' seed ' + s + ' (' + mode + ')');
      const again = plan([out.tiles[0], { x: out.endX, y: out.endY }], minRun);
      assert.deepEqual(again.tiles, out.tiles, 'tier ' + tier + ' seed ' + s + ' (' + mode + '): idempotent');
    }
  }
});

// -------------------------------------------------------- reducer attacks ---

test('ATTACK reducer (e): a FORGED bendLegal:true on genuinely illegal tiles is refused, zero funds moved', () => {
  const before = board([]);
  const tiles = [{ x: 20, y: 20 }, { x: 21, y: 20 }, { x: 21, y: 21 }, { x: 22, y: 21 }, { x: 23, y: 21 }];
  for (const tier of [2, 3, 4, 5]) {
    const spec = ROAD_TIER_SPECS[tier];
    const minRun = minBendRadiusTilesForTier(tier);
    assert.ok(independentCheck(tiles, minRun), 'sanity: fixture is illegal at tier ' + tier);
    const after = reducer(before, { type: 'placeRoadPath', spec: spec, tiles: tiles, bendLegal: true });
    assert.equal(after.buildings.length, before.buildings.length, 'tier ' + tier + ': zero tiles placed');
    assert.equal(after.funds, before.funds, 'tier ' + tier + ': zero funds movement');
    assert.equal(after.placeNotice, ROAD_BEND_ILLEGAL_MESSAGE, 'tier ' + tier + ': MET-V961 refusal notice');
  }
});

test('ATTACK reducer: the refusal is DERIVED, not a blanket reject — the same tiles place at a tier whose minRun allows them', () => {
  const before = board([]);
  const tiles = [{ x: 20, y: 20 }, { x: 21, y: 20 }, { x: 21, y: 21 }, { x: 22, y: 21 }, { x: 23, y: 21 }];
  const minRun1 = minBendRadiusTilesForTier(1);
  assert.equal(independentCheck(tiles, minRun1), null, 'fixture is legal at tier 1');
  const after = reducer(before, { type: 'placeRoadPath', spec: ROAD_TIER_SPECS[1], tiles: tiles, bendLegal: true });
  assert.equal(after.buildings.length, tiles.length, 'tier 1 places all 5 tiles');
  assert.notEqual(after.placeNotice, ROAD_BEND_ILLEGAL_MESSAGE);
  assert.ok(after.funds < before.funds, 'and charges for them');
});

test('ATTACK replay-compat (R4/R6): the SAME illegal action without the marker replays as pre-inc4, deterministically', () => {
  const before = board([]);
  const hairpin = [{ x: 30, y: 30 }, { x: 31, y: 30 }, { x: 31, y: 31 }, { x: 32, y: 31 }, { x: 33, y: 31 }];
  const legacy = { type: 'placeRoadPath', spec: ROAD_TIER_SPECS[5], tiles: hairpin };
  assert.ok(independentCheck(hairpin, minBendRadiusTilesForTier(5)), 'sanity: illegal for a motorway');

  const runs = [];
  for (let i = 0; i < 3; i++) {
    const after = reducer(board([]), { type: 'placeRoadPath', spec: ROAD_TIER_SPECS[5], tiles: hairpin.map((t) => ({ x: t.x, y: t.y })) });
    runs.push(JSON.stringify({
      n: after.buildings.length,
      funds: after.funds,
      notice: after.placeNotice ?? null,
      tiles: after.buildings.map((b) => b.x + ',' + b.y + ',' + b.spec).sort(),
    }));
  }
  assert.equal(runs[0], runs[1], 'determinism run 1 vs 2');
  assert.equal(runs[1], runs[2], 'determinism run 2 vs 3');
  const after = reducer(before, legacy);
  assert.equal(after.buildings.length, hairpin.length, 'pre-inc4 action still places every tile');
  assert.notEqual(after.placeNotice, ROAD_BEND_ILLEGAL_MESSAGE, 'no retroactive AC-5 rejection of an old journal entry');

  const marked = reducer(before, { type: 'placeRoadPath', spec: ROAD_TIER_SPECS[5], tiles: hairpin, bendLegal: true });
  assert.equal(marked.buildings.length, 0, 'marker flips the same action to refused');
  assert.equal(marked.placeNotice, ROAD_BEND_ILLEGAL_MESSAGE);
});

test('ATTACK reducer: a non-road spec with bendLegal:true is never bend-checked (tier 0 must not reach the MET-V960 loader)', () => {
  const s = board([]);
  const tiles = [{ x: 40, y: 40 }, { x: 41, y: 40 }, { x: 41, y: 41 }];
  assert.doesNotThrow(() => reducer(s, { type: 'placeRoadPath', spec: 'park', tiles: tiles, bendLegal: true }));
});

test('ATTACK BUG-1016 FIX VERIFIED: a self-crossing bendLegal path is refused via MET-V961, never a bare Error', () => {
  // Same repro BUG-1016 was pinned against: the reducer deduplicates
  // repeated tiles (AC-6e) BEFORE the bend check, so a path revisiting an
  // INTERIOR tile can dedup to a non-4-connected remainder. Previously this
  // threw a plain Error out of the reducer; the fix wraps the bend check and
  // treats that case as an ordinary refusal.
  const tiles = [];
  let x = 60;
  const y = 60;
  tiles.push({ x: x, y: y });
  for (let i = 0; i < 3; i++) { x += 1; tiles.push({ x: x, y: y }); }  // -> (63,60)
  for (let i = 0; i < 2; i++) { x -= 1; tiles.push({ x: x, y: y }); }  // -> (61,60), both duplicates
  tiles.push({ x: 61, y: 59 });                                       // a NEW tile off the row
  const before = board([]);
  let after;
  assert.doesNotThrow(() => {
    after = reducer(before, { type: 'placeRoadPath', spec: ROAD_TIER_SPECS[1], tiles: tiles, bendLegal: true });
  }, 'BUG-1016: must never throw a bare Error out of the reducer');
  assert.equal(after.buildings.length, before.buildings.length, 'BUG-1016: zero tiles placed');
  assert.equal(after.funds, before.funds, 'BUG-1016: zero funds movement');
  assert.equal(after.placeNotice, ROAD_BEND_ILLEGAL_MESSAGE, 'BUG-1016: refused via the registered MET-V961 notice');
});

// ---------------------------------------------------------- marker coverage --

test('ATTACK (f): the MapView dispatch site always carries the marker, plans from the anchor/cursor, and the cost uses the LEGALISED tile count', async () => {
  const fs = await import('node:fs/promises');
  const src = await fs.readFile(new URL('../src/components/MapView.tsx', import.meta.url), 'utf8');
  const lines = src.split(/\n/);
  const dispatchLines = lines.map((l, i) => [i + 1, l]).filter((e) => /type:\s*'placeRoadPath'/.test(e[1]));
  assert.equal(dispatchLines.length, 1, 'exactly one placeRoadPath dispatch site (a second, unmarked one would bypass AC-5)');
  const idx = dispatchLines[0][0];
  // Strip // comments first — the dispatch site's own doc comment MENTIONS
  // "bendLegal:true", so an uncommented match would survive deleting the field.
  const window = lines.slice(idx - 1, idx + 14).map((l) => l.replace(/\/\/.*$/, '')).join('\n');
  assert.match(window, /bendLegal:\s*true\s*,/, 'the single dispatch site sets the marker (in code, not only in a comment)');

  // BUG-1014 fix: there is no more "compute a raw path, then smooth it" --
  // legalisePath is called directly from the anchor and the live cursor.
  assert.ok(!/\brawPath\b/.test(src), 'BUG-1014 fix: no leftover rawPath symbol — nothing left to smooth after the fact');
  assert.match(src, /legalisePath\(\s*\n?\s*roadTracker\.anchorX,\s*\n?\s*roadTracker\.anchorY,\s*\n?\s*t\.x,\s*\n?\s*t\.y,/,
    'legalisePath is called PLAN-style directly from roadTracker.anchorX/anchorY and the live cursor tile');
  // AC-3: the previewed cost must be computed from the LEGALISED path, not a raw one.
  assert.match(src, /const cost = sp \? placementCost\(sp\) \* path\.length/, 'cost multiplies the (legalised) path length');
});

// ===========================================================================
// R2 ROUND EXTENSIONS (opus-reround-feat910-bends) — independent re-attack of
// the LEAD RULING v2 plan-from-endpoints algorithm. Everything below was
// written from the ruling text and run against the r2 build; the mutation
// RED/GREEN log is on the BOW item.
// ===========================================================================

/** R2: my own violation list for a planned path — independent of the build's
 * bendRadiiOf/isBendLegal AND of the r1 helpers above. */
function r2Violations(p, minRun, anchor, end) {
  const v = [];
  if (p.length === 0) { v.push('empty'); return v; }
  if (p[0].x !== anchor.x || p[0].y !== anchor.y) v.push('does not start at anchor');
  const last = p[p.length - 1];
  if (last.x !== end.x || last.y !== end.y) v.push('does not end at the reported endpoint');
  const legs = [];
  for (let i = 1; i < p.length; i++) {
    const dx = p[i].x - p[i - 1].x, dy = p[i].y - p[i - 1].y;
    if (Math.abs(dx) + Math.abs(dy) !== 1) { v.push('step ' + i + ' is not a 4-connected unit step'); return v; }
    const k = dx + ':' + dy, l = legs[legs.length - 1];
    if (l && l.k === k) l.n++; else legs.push({ k, n: 1, dx, dy });
  }
  for (let i = 1; i < legs.length; i++) {
    if (legs[i].dx === -legs[i - 1].dx && legs[i].dy === -legs[i - 1].dy) v.push('reversal at corner ' + i);
    else if (Math.min(legs[i - 1].n, legs[i].n) < minRun) v.push('corner ' + i + ' run min(' + legs[i - 1].n + ',' + legs[i].n + ') < ' + minRun);
  }
  const man = Math.abs(end.x - anchor.x) + Math.abs(end.y - anchor.y);
  if (p.length - 1 !== man) v.push('length ' + (p.length - 1) + ' != Manhattan ' + man);
  const seen = new Set();
  for (const t of p) { const kk = t.x + ',' + t.y; if (seen.has(kk)) v.push('self-crossing at ' + kk); seen.add(kk); }
  return v;
}

test('R2: EXHAUSTIVE boundary grid per tier (0/1/minRun+-1/2xminRun+-1, both signs, map edges/corners, off-map anchors)', () => {
  // The brief boundary set, crossed with itself on both axes and both signs,
  // from six anchors including the four map corners and an off-map anchor.
  // This is the case class the r1 build failed (BUG-1014: a reachable cursor
  // was not reached) and the one the snap rule lives in.
  const MAPW = 256, MAPH = 256;
  const anchors = [{ x: 0, y: 0 }, { x: MAPW - 1, y: MAPH - 1 }, { x: 0, y: MAPH - 1 }, { x: MAPW - 1, y: 0 }, { x: 128, y: 128 }, { x: -3, y: -3 }];
  const rows = [];
  for (const tier of TIERS) {
    const minRun = minBendRadiusTilesForTier(tier);
    const deltas = [0, 1, minRun - 1, minRun, minRun + 1, 2 * minRun - 1, 2 * minRun, 3 * minRun + 1];
    let cases = 0, reached = 0, snapped = 0;
    for (const a of anchors) for (const bx of deltas) for (const by of deltas) for (const sx of [1, -1]) for (const sy of [1, -1]) {
      const cx = a.x + sx * bx, cy = a.y + sy * by;
      const out = legalisePath(a.x, a.y, cx, cy, minRun);
      cases++;
      const vs = r2Violations(out.tiles, minRun, a, { x: out.endX, y: out.endY });
      const dx = cx - a.x, dy = cy - a.y;
      const reachable = dx === 0 || dy === 0 || (Math.abs(dx) >= minRun && Math.abs(dy) >= minRun);
      if (reachable) {
        if (out.endX !== cx || out.endY !== cy) vs.push('REACHABLE cursor not reached (BUG-1014 class)');
        if (out.snapped) vs.push('reachable but snapped=true');
        reached++;
      } else {
        if (!out.snapped) vs.push('unreachable but snapped=false');
        const ex = a.x + independentSnap(dx, minRun), ey = a.y + independentSnap(dy, minRun);
        if (out.endX !== ex || out.endY !== ey) vs.push('snap mismatch: got (' + out.endX + ',' + out.endY + ') want (' + ex + ',' + ey + ')');
        snapped++;
      }
      const again = legalisePath(a.x, a.y, out.endX, out.endY, minRun);
      if (JSON.stringify(again.tiles) !== JSON.stringify(out.tiles)) vs.push('NOT idempotent');
      if (again.snapped) vs.push('re-plan of a planned endpoint reports snapped');
      assert.equal(vs.join(' | '), '', 'tier ' + tier + ' minRun ' + minRun + ' (' + a.x + ',' + a.y + ')->(' + cx + ',' + cy + ')');
    }
    rows.push('  tier ' + tier + ' minRun=' + minRun + ': ' + cases + ' cases, reached ' + reached + ', snapped ' + snapped);
  }
  console.log('R2 EXHAUSTIVE BOUNDARY GRID (all clean):');
  for (const r of rows) console.log(r);
});

test('R2: 6,000 seeded (anchor,cursor) pairs per tier over the whole map + off-map cursors', () => {
  const MAPW = 256, MAPH = 256;
  for (const tier of TIERS) {
    const minRun = minBendRadiusTilesForTier(tier);
    for (let s = 0; s < 6000; s++) {
      const rnd = mulberry32(s * 2654435761 + tier * 97);
      const ax = Math.floor(rnd() * MAPW), ay = Math.floor(rnd() * MAPH);
      const cx = Math.floor(rnd() * (MAPW + 80)) - 40, cy = Math.floor(rnd() * (MAPH + 80)) - 40;
      const out = legalisePath(ax, ay, cx, cy, minRun);
      const vs = r2Violations(out.tiles, minRun, { x: ax, y: ay }, { x: out.endX, y: out.endY });
      const dx = cx - ax, dy = cy - ay;
      if (dx === 0 || dy === 0 || (Math.abs(dx) >= minRun && Math.abs(dy) >= minRun)) {
        if (out.endX !== cx || out.endY !== cy) vs.push('REACHABLE cursor not reached');
        if (out.snapped) vs.push('reachable but snapped');
      } else {
        if (!out.snapped) vs.push('unreachable but snapped=false');
        const ex = ax + independentSnap(dx, minRun), ey = ay + independentSnap(dy, minRun);
        if (out.endX !== ex || out.endY !== ey) vs.push('snap mismatch');
      }
      const again = legalisePath(ax, ay, out.endX, out.endY, minRun);
      if (JSON.stringify(again.tiles) !== JSON.stringify(out.tiles)) vs.push('NOT idempotent');
      assert.equal(vs.join(' | '), '', 'tier ' + tier + ' seed ' + s + ' (' + ax + ',' + ay + ')->(' + cx + ',' + cy + ')');
    }
  }
});

test('R2: both axes short at once — the snap rule applies per axis and the result is still legal', () => {
  // The ruling snaps EACH short axis independently (drop vs extend, tie ->
  // drop). Exhaustively for every tier, every |dx|,|dy| strictly below
  // minRun, both signs.
  for (const tier of TIERS) {
    const minRun = minBendRadiusTilesForTier(tier);
    for (let ax = 0; ax < minRun; ax++) for (let ay = 0; ay < minRun; ay++) for (const sx of [1, -1]) for (const sy of [1, -1]) {
      const cx = 100 + sx * ax, cy = 100 + sy * ay;
      const out = legalisePath(100, 100, cx, cy, minRun);
      if (ax === 0 || ay === 0) {
        // A zero delta is never "short": the straight-leg branch keeps it.
        assert.equal(out.snapped, false, 'tier ' + tier + ': a straight drag never snaps');
        assert.deepEqual({ x: out.endX, y: out.endY }, { x: cx, y: cy });
      } else {
        const wantX = 100 + independentSnap(sx * ax, minRun);
        const wantY = 100 + independentSnap(sy * ay, minRun);
        assert.equal(out.snapped, true, 'tier ' + tier + ': both axes short must report snapped');
        assert.deepEqual({ x: out.endX, y: out.endY }, { x: wantX, y: wantY }, 'tier ' + tier + ' (' + ax + ',' + ay + '): per-axis snap');
      }
      assert.equal(r2Violations(out.tiles, minRun, { x: 100, y: 100 }, { x: out.endX, y: out.endY }).join(' | '), '');
    }
  }
});

test('R2: minRun=1 is byte-identical to computePath over 3,000 seeded pairs (the ruling requires delegation)', () => {
  for (let s = 0; s < 3000; s++) {
    const rnd = mulberry32(s * 40503 + 7);
    const ax = Math.floor(rnd() * 200) - 100, ay = Math.floor(rnd() * 200) - 100;
    const cx = ax + Math.floor(rnd() * 160) - 80, cy = ay + Math.floor(rnd() * 160) - 80;
    assert.deepEqual(legalisePath(ax, ay, cx, cy, 1).tiles, computePath(ax, ay, cx, cy), 'seed ' + s);
  }
});

// ------------------------------------------------------ reducer extensions --

test('R2 reducer: a DUPLICATE-TILE bendLegal list is refused via MET-V961 with zero tiles and zero funds', () => {
  const before = board([]);
  const dup = [{ x: 50, y: 50 }, { x: 50, y: 50 }, { x: 51, y: 50 }, { x: 52, y: 50 }, { x: 52, y: 51 }];
  const after = reducer(before, { type: 'placeRoadPath', spec: ROAD_TIER_SPECS[4], tiles: dup, bendLegal: true });
  assert.equal(after.buildings.length, before.buildings.length, 'zero tiles placed');
  assert.equal(after.funds, before.funds, 'zero funds movement');
  assert.equal(after.placeNotice, ROAD_BEND_ILLEGAL_MESSAGE, 'refused via the MET-V961 notice, not a throw');
});

test('R2 reducer: a non-road spec with bendLegal:true does not even reach the bend branch (no MET-V961 notice)', () => {
  // The author test only asserted doesNotThrow, which a mutant removing the
  // `newRoadTier > 0` guard SURVIVES (the tier-0 loader throw is then
  // swallowed by the BUG-1016 catch and becomes a bogus bend refusal - BUG-1041).
  // Pin the observable: no bend notice at all.
  const s = board([]);
  const tiles = [{ x: 40, y: 40 }, { x: 41, y: 40 }, { x: 41, y: 41 }];
  const after = reducer(s, { type: 'placeRoadPath', spec: 'park', tiles: tiles, bendLegal: true });
  assert.notEqual(after.placeNotice, ROAD_BEND_ILLEGAL_MESSAGE, 'a non-road spec must never produce a bend-legality refusal');
});

test('R2 AC-6: a PLANNED staircase crossing an existing avenue still converts the shared tiles (crossing logic intact)', () => {
  // AC-6 (inc2) must still hold for a path produced by the inc4 planner.
  let s = board([]);
  const avenue = [];
  for (let y = 16; y <= 30; y++) avenue.push({ x: 10, y: y });
  s = reducer(s, { type: 'placeRoadPath', spec: ROAD_TIER_SPECS[2], tiles: avenue });
  assert.equal(s.buildings.filter((b) => b.spec === ROAD_TIER_SPECS[2]).length, avenue.length, 'avenue laid');
  const idsBefore = new Map(s.buildings.map((b) => [b.x + ',' + b.y, b.id]));

  const minRun3 = minBendRadiusTilesForTier(3);
  const planned = legalisePath(5, 20, 25, 32, minRun3);
  assert.equal(planned.snapped, false, 'fixture: the drag is reachable at tier 3');
  assert.equal(r2Violations(planned.tiles, minRun3, { x: 5, y: 20 }, { x: 25, y: 32 }).join(' | '), '', 'fixture: the planned path is legal');
  const shared = planned.tiles.filter((t) => idsBefore.has(t.x + ',' + t.y));
  assert.ok(shared.length > 0, 'fixture: the planned staircase actually crosses the avenue');

  const after = reducer(s, { type: 'placeRoadPath', spec: ROAD_TIER_SPECS[3], tiles: planned.tiles, bendLegal: true });
  assert.notEqual(after.placeNotice, ROAD_BEND_ILLEGAL_MESSAGE, 'a legal planned path is not refused');
  for (const t of shared) {
    const at = after.buildings.filter((b) => b.x === t.x && b.y === t.y);
    assert.equal(at.length, 1, 'exactly one building at crossing tile ' + t.x + ',' + t.y);
    assert.equal(at[0].id, idsBefore.get(t.x + ',' + t.y), 'crossing tile ' + t.x + ',' + t.y + ' is the ORIGINAL building, converted in place');
    assert.equal(at[0].spec, 'rd_roundabout', 'avenue+ crossing converts to a roundabout (AC-6 tier rule), tile ' + t.x + ',' + t.y);
  }
});

// ------------------------------------------------ MapView wiring (static) ---

test('R2 (f/g): the MapView pointermove wiring has exactly one raw computePath fallback and feeds snapped into the tracker state', async () => {
  // MUTANT-DRIVEN (three survivors of the author suite, filed as BUG-1039 /
  // BUG-1040): re-assigning `path` from computePath AFTER legalisePath, or
  // hardcoding `snapped: false` in the setRoadTracker update, both leave
  // every pre-existing grep satisfied. These pins close the textual half; a
  // real behavioural pointer-drag test is BUG-1039.
  const fs = await import('node:fs/promises');
  const src = await fs.readFile(new URL('../src/components/MapView.tsx', import.meta.url), 'utf8');
  const code = src.split(/\n/).map((l) => l.replace(/\/\/.*$/, '')).join('\n');
  const computePathCalls = (code.match(/computePath\(/g) || []).length;
  assert.equal(computePathCalls, 1, 'exactly ONE computePath( call may remain (the non-road-spec fallback) - a second means the legalised path is being overwritten');
  const pathAssigns = (code.match(/(^|[^.\w])path = /g) || []).length;
  assert.equal(pathAssigns, 2, 'exactly TWO assignments to `path`: the legalised tiles and the non-road fallback');
  assert.match(code, /snapped = legal\.snapped;/, 'the legalisePath result snapped flag is captured');
  assert.match(code, /totalCost: cost,\s*\n\s*snapped,/, 'and flows into the tracker state as the shorthand `snapped` (a hardcoded false would silence the amber snap outline)');
  assert.match(code, /if \(roadTracker\.snapped && roadTracker\.currentPath\.length > 0\)/, 'the ghost preview reads the tracker snap flag to draw the endpoint outline');
});
