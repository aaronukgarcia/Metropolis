// consolidator-layout-inc3-unit.test.mjs — FEAT-2326609779 (consolidator
// inc3, LAYOUT HIERARCHY). Pure unit tests of consolidatorLayout.ts's
// geometry/conflict/severance/free-space/seed primitives — NO engine/reducer
// dependency, so these are fast and exercise each checker directly against
// constructed scenarios, exactly as the acceptance doc's own "Check" clauses
// describe (AC-2/AC-3/AC-5/AC-6/AC-7/AC-8/AC-10).

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import {
  TIER_ORDER,
  TIER_MIN_INTERIOR_ANGLE_DEG,
  TIER_RANK,
  isHigherTier,
  mayPassThrough,
  isValidBendPath,
  firstInvalidBendIndex,
  isValidJunctionAngle,
  evaluateJunctionRules,
  layoutSeedOf,
  candidateTierPath,
  resolveTierConflicts,
  classifyFreeSpace,
  tileComponents,
  wouldSever,
  MIN_TIER_RUN_TILES,
} from '../src/sim/consolidatorLayout.ts';

// A point at angle `theta` degrees from b=(0,0), a=(1,0) — interiorAngleDeg(a,b,c)
// then evaluates to EXACTLY `theta` (up to float error) by construction.
function pointAtAngle(theta) {
  const rad = (theta * Math.PI) / 180;
  return { x: Math.cos(rad), y: Math.sin(rad) };
}

describe('FEAT-2326609779 AC-2/AC-5 — bend geometry', () => {
  test('a path shorter than 3 tiles is trivially valid for every tier', () => {
    for (const tier of TIER_ORDER) {
      assert.equal(isValidBendPath(tier, []), true);
      assert.equal(isValidBendPath(tier, [{ x: 0, y: 0 }]), true);
      assert.equal(isValidBendPath(tier, [{ x: 0, y: 0 }, { x: 1, y: 0 }]), true);
    }
  });

  test('a dead-straight path (180 degree interior angle) is valid for every tier', () => {
    const straight = [{ x: 0, y: 0 }, { x: 1, y: 0 }, { x: 2, y: 0 }, { x: 3, y: 0 }];
    for (const tier of TIER_ORDER) {
      assert.equal(isValidBendPath(tier, straight), true, tier);
    }
  });

  // Table-driven: for each tier, an angle clearly ABOVE its AC-2 minimum
  // passes, and an angle clearly BELOW it fails — proving isValidBendPath
  // matches TIER_MIN_INTERIOR_ANGLE_DEG exactly, tier by tier (AC-2's own
  // "at the limit and just inside/outside" check).
  const cases = [
    { tier: 'rail', above: 68, below: 67 },
    { tier: 'motorway', above: 91, below: 89 },
    { tier: 'dual', above: 113, below: 112 },
    { tier: 'aroad', above: 113, below: 112 },
    { tier: 'minor', above: 46, below: 44 },
  ];
  for (const { tier, above, below } of cases) {
    test(`${tier}: angle just ABOVE its ${TIER_MIN_INTERIOR_ANGLE_DEG[tier]}° minimum passes`, () => {
      const b = { x: 0, y: 0 };
      const a = { x: 1, y: 0 };
      const c = pointAtAngle(above);
      assert.equal(isValidBendPath(tier, [a, b, c]), true);
    });
    test(`${tier}: angle just BELOW its ${TIER_MIN_INTERIOR_ANGLE_DEG[tier]}° minimum fails`, () => {
      const b = { x: 0, y: 0 };
      const a = { x: 1, y: 0 };
      const c = pointAtAngle(below);
      assert.equal(isValidBendPath(tier, [a, b, c]), false);
      assert.equal(firstInvalidBendIndex(tier, [a, b, c]), 1);
    });
  }

  test('firstInvalidBendIndex finds the SINGLE out-of-spec bend in a 20-tile rail path (AC-5)', () => {
    // A straight rail path of 10 tiles (indices 0..9, all interior angles
    // 180 degrees — valid for every tier), then a near-total reversal at
    // point 9 (interior angle ~0 degrees, well under rail's 67.5 minimum),
    // then 9 more points continuing straight in the new (reversed)
    // direction — 20 points total, exactly ONE bend out of spec.
    const path = [];
    for (let i = 0; i < 10; i++) path.push({ x: i, y: 0 }); // indices 0..9
    for (let i = 1; i <= 9; i++) path.push({ x: 9 - i, y: 0.001 }); // indices 10..18: reverses back past the start
    assert.equal(path.length, 19);
    assert.equal(isValidBendPath('rail', path), false);
    assert.equal(firstInvalidBendIndex('rail', path), 9); // the reversal point itself
    // Every OTHER interior point (both straight runs) stays valid on its own.
    assert.equal(isValidBendPath('rail', path.slice(0, 10)), true);
    assert.equal(isValidBendPath('rail', path.slice(9)), true);
  });
});

describe('FEAT-2326609779 AC-3 — junction rules', () => {
  test('isValidJunctionAngle: >=90 degrees passes, <90 fails (no acute merges)', () => {
    assert.equal(isValidJunctionAngle(90), true);
    assert.equal(isValidJunctionAngle(180), true);
    assert.equal(isValidJunctionAngle(89.9), false);
    assert.equal(isValidJunctionAngle(45), false);
  });

  test('hierarchy rule: a higher tier may pass through a lower one; never the reverse', () => {
    assert.equal(mayPassThrough('rail', 'motorway'), true);
    assert.equal(mayPassThrough('motorway', 'rail'), false);
    assert.equal(mayPassThrough('motorway', 'minor'), true);
    assert.equal(mayPassThrough('minor', 'motorway'), false);
    assert.equal(mayPassThrough('rail', 'rail'), false); // same tier never "passes through" itself
  });

  test('TIER_RANK/isHigherTier agree with TIER_ORDER (rail first = highest)', () => {
    for (let i = 0; i < TIER_ORDER.length; i++) {
      assert.equal(TIER_RANK[TIER_ORDER[i]], i);
    }
    assert.equal(isHigherTier('rail', 'minor'), true);
    assert.equal(isHigherTier('minor', 'rail'), false);
  });

  // R3-B (round-3 finding, HIGH): evaluateJunctionRules had ZERO direct
  // coverage — every existing engine-level test only ever exercised it via
  // real cities, where it happened to never fire the way its own bug did
  // (or was masked by other gates). These two tests call the REAL function
  // directly: the false-positive regression case (must return true), and a
  // genuine acute-approach junction (must return false) — a stub `() =>
  // true` passes the first but goes RED on the second, proving this suite
  // actually exercises the evaluator's logic, not just its signature.
  test('R3-B REGRESSION: two tiers running in PARALLEL ADJACENT rows (never meeting) is NOT a junction', () => {
    const motorwayPath = Array.from({ length: 10 }, (_, x) => ({ x, y: 6 }));
    const railTiles = new Set(Array.from({ length: 10 }, (_, x) => `${x},5`)); // one row over — never touched.
    const placed = new Map([['rail', railTiles]]);
    assert.equal(
      evaluateJunctionRules('motorway', motorwayPath, placed),
      true,
      'R3-B: a parallel adjacent row must never be treated as a junction — the path never runs INTO the other tier',
    );
  });

  test('a genuine acute-angle approach INTO a higher tier is rejected (proves the evaluator is not a stub)', () => {
    // motorway: a straight horizontal line at y=0, x=5..7.
    const motorwayTiles = new Set(['5,0', '6,0', '7,0']);
    const placed = new Map([['motorway', motorwayTiles]]);
    // minor road approaching diagonally: (4,2) -> (5,1) — its OWN direction
    // of travel, extended one more tile, lands on (6,0): a real junction
    // point, at a 45-degree entry angle against motorway's own (5,0)-(6,0)-(7,0)
    // direction — well under MIN_JUNCTION_ENTRY_ANGLE_DEG (90).
    const minorPath = [{ x: 4, y: 2 }, { x: 5, y: 1 }];
    assert.equal(
      evaluateJunctionRules('minor', minorPath, placed),
      false,
      'a minor road running INTO a motorway at 45 degrees must be rejected — no acute merges (AC-3)',
    );
  });

  test('a genuine PERPENDICULAR (90-degree) approach INTO a higher tier is accepted', () => {
    const motorwayTiles = new Set(['5,0', '6,0', '7,0']);
    const placed = new Map([['motorway', motorwayTiles]]);
    // minor road running straight down onto (6,0) from directly above.
    const minorPath = [{ x: 6, y: 2 }, { x: 6, y: 1 }];
    assert.equal(evaluateJunctionRules('minor', minorPath, placed), true, 'a square (90 degree) T-junction is valid');
  });
});

describe('FEAT-2326609779 AC-3/AC-6 — tile-spread conflict resolution', () => {
  test('two tiers wanting the SAME tile: the higher tier wins, the loser is recorded skipped+conflicted', () => {
    const shared = { x: 5, y: 5 };
    const raw = {
      rail: [{ x: 4, y: 5 }, shared, { x: 6, y: 5 }],
      motorway: [{ x: 5, y: 4 }, shared, { x: 5, y: 6 }],
      dual: [],
      aroad: [],
      minor: [],
    };
    const resolved = resolveTierConflicts(raw);
    // rail (higher) keeps the shared tile.
    assert.ok(resolved.paths.rail.some((p) => p.x === shared.x && p.y === shared.y));
    // motorway (lower) loses it.
    assert.ok(!resolved.paths.motorway.some((p) => p.x === shared.x && p.y === shared.y));
    assert.equal(resolved.conflictsDetected.length, 1);
    assert.equal(resolved.skippedTiles.length, 1);
    assert.equal(resolved.skippedTiles[0].tier, 'motorway');
    assert.equal(resolved.skippedTiles[0].x, shared.x);
    assert.equal(resolved.skippedTiles[0].y, shared.y);
  });

  test('overlapping tier GRIDS (many shared tiles): every conflict is recorded, non-conflicting tiles survive for both', () => {
    // rail wants a horizontal line, minor wants a crossing vertical line —
    // they intersect at exactly one tile.
    const raw = {
      rail: [{ x: 0, y: 3 }, { x: 1, y: 3 }, { x: 2, y: 3 }, { x: 3, y: 3 }],
      motorway: [],
      dual: [],
      aroad: [],
      minor: [{ x: 2, y: 0 }, { x: 2, y: 1 }, { x: 2, y: 2 }, { x: 2, y: 3 }],
    };
    const resolved = resolveTierConflicts(raw);
    assert.equal(resolved.paths.rail.length, 4); // rail (higher) untouched
    assert.equal(resolved.paths.minor.length, 3); // minor lost exactly the shared tile
    assert.ok(!resolved.paths.minor.some((p) => p.x === 2 && p.y === 3));
    assert.equal(resolved.conflictsDetected.length, 1);
  });

  test('no conflicts at all when tiers claim disjoint tiles', () => {
    const raw = {
      rail: [{ x: 0, y: 0 }, { x: 1, y: 0 }, { x: 2, y: 0 }],
      motorway: [{ x: 0, y: 5 }, { x: 1, y: 5 }, { x: 2, y: 5 }],
      dual: [],
      aroad: [],
      minor: [],
    };
    const resolved = resolveTierConflicts(raw);
    assert.equal(resolved.conflictsDetected.length, 0);
    assert.equal(resolved.skippedTiles.length, 0);
    assert.equal(resolved.paths.rail.length, 3);
    assert.equal(resolved.paths.motorway.length, 3);
  });
});

describe('FEAT-2326609779 AC-4 — severance checker (tileComponents/wouldSever)', () => {
  test('tileComponents: a straight line of tiles is ONE connected component', () => {
    const tiles = new Set(['0,0', '1,0', '2,0', '3,0', '4,0']);
    const comp = tileComponents(tiles);
    const ids = new Set(Array.from(tiles).map((t) => comp.get(t)));
    assert.equal(ids.size, 1);
  });

  test('tileComponents: two disjoint segments are TWO components', () => {
    const tiles = new Set(['0,0', '1,0', '5,0', '6,0']);
    const comp = tileComponents(tiles);
    assert.equal(comp.get('0,0'), comp.get('1,0'));
    assert.equal(comp.get('5,0'), comp.get('6,0'));
    assert.notEqual(comp.get('0,0'), comp.get('5,0'));
  });

  test('pure ADDITION never severs — structurally impossible (AC-4, wouldSever doc)', () => {
    const existing = new Set(['0,0', '1,0', '2,0']);
    const added = new Set(['3,0', '3,1', '3,2']); // a whole new branch, no removal
    assert.equal(wouldSever(existing, added), false);
  });

  test('DEMOLISHING the sole connector tile between two rail halves IS a severance (the AC-4 scenario)', () => {
    // A rail line 0..4 on row y=0; tile (2,0) is the ONLY connector between
    // the (0,0)-(1,0) half and the (3,0)-(4,0) half.
    const existing = new Set(['0,0', '1,0', '2,0', '3,0', '4,0']);
    const removed = new Set(['2,0']);
    const added = new Set(); // nothing rebuilt in its place
    assert.equal(wouldSever(existing, added, removed), true);
  });

  test('demolishing a connector tile and IMMEDIATELY replacing it with an equivalent bridge tile does NOT sever', () => {
    const existing = new Set(['0,0', '1,0', '2,0', '3,0', '4,0']);
    const removed = new Set(['2,0']);
    const added = new Set(['2,0']); // same tile, effectively unchanged
    assert.equal(wouldSever(existing, added, removed), false);
  });

  test('demolishing a tile that was ALREADY isolated (not connecting anything) is not a severance', () => {
    const existing = new Set(['0,0', '1,0', '9,9']); // 9,9 has no neighbours
    const removed = new Set(['9,9']);
    assert.equal(wouldSever(existing, new Set(), removed), false);
  });
});

describe('FEAT-2326609779 AC-7/AC-8 — free-space disposition', () => {
  test('classifyFreeSpace: near-amenity tiles become parks, everything else becomes reserve, sorted (y,x)', () => {
    const free = [{ x: 5, y: 1 }, { x: 0, y: 0 }, { x: 2, y: 0 }, { x: 1, y: 0 }];
    const nearAmenity = (p) => p.x < 3;
    const alloc = classifyFreeSpace(free, nearAmenity);
    assert.equal(alloc.parkCount, 3);
    assert.equal(alloc.reserveCount, 1);
    assert.deepEqual(alloc.tilesByKind.parks, [{ x: 0, y: 0 }, { x: 1, y: 0 }, { x: 2, y: 0 }]);
    assert.deepEqual(alloc.tilesByKind.reserve, [{ x: 5, y: 1 }]);
  });

  test('a 4x4 section with buildings only in the corners: the centre 8 free tiles are all classified (park or reserve)', () => {
    // Occupy the 4 corners of a 4x4 box; the remaining 12 interior tiles are
    // free. (The acceptance doc's own worked example names "the centre 8" —
    // this proves EVERY free tile in the box is accounted for exactly once,
    // regardless of the exact corner/centre split chosen.)
    const occupied = new Set(['0,0', '3,0', '0,3', '3,3']);
    const free = [];
    for (let x = 0; x < 4; x++) {
      for (let y = 0; y < 4; y++) {
        if (!occupied.has(`${x},${y}`)) free.push({ x, y });
      }
    }
    assert.equal(free.length, 12);
    const alloc = classifyFreeSpace(free, () => true);
    assert.equal(alloc.parkCount + alloc.reserveCount, 12);
  });

  test('AC-8: a growth-reserve tile is reusable — classifyFreeSpace makes no cost decision itself (that is the caller/engine.ts job), it only classifies', () => {
    const alloc = classifyFreeSpace([{ x: 1, y: 1 }], () => false);
    assert.equal(alloc.reserveCount, 1);
    assert.equal(alloc.parkCount, 0);
  });
});

describe('FEAT-2326609779 AC-2/AC-5 honesty fix — bent paths genuinely reachable, at a genuinely failable angle', () => {
  test('a fully-empty real-sized section produces a BENT candidate (not just decoratively reachable)', () => {
    const box = { x0: 0, y0: 0, w: 16, h: 16 };
    const avail = new Set();
    for (let x = 0; x < 16; x++) for (let y = 0; y < 16; y++) avail.add(`${x},${y}`);
    let bent = 0;
    for (let trial = 0; trial < 60; trial++) {
      const path = candidateTierPath(avail, box, layoutSeedOf(trial, trial * 7));
      if (path.length === 0) continue;
      const p0 = path[0];
      if (!(path.every((q) => q.x === p0.x) || path.every((q) => q.y === p0.y))) bent++;
    }
    assert.ok(bent > 0, 'HONESTY FIX: at least some real-section candidates must actually bend, not just be theoretically capable of it');
  });

  test('across many seeds, the right-angle (90-degree) bend actually occurs and genuinely FAILS isValidBendPath for dual/A-road while PASSING for rail/motorway/minor', () => {
    const box = { x0: 0, y0: 0, w: 16, h: 16 };
    const avail = new Set();
    for (let x = 0; x < 16; x++) for (let y = 0; y < 16; y++) avail.add(`${x},${y}`);
    let sawRightAngleRejection = false;
    for (let trial = 0; trial < 200 && !sawRightAngleRejection; trial++) {
      const seed = layoutSeedOf(trial, trial * 13);
      const path = candidateTierPath(avail, box, seed);
      if (path.length < 3) continue;
      // A right-angle bend has an exact 90-degree interior angle somewhere;
      // a chamfer's is 135 — distinguish directly rather than trust internals.
      const has90 = path.some((_, i) => {
        if (i === 0 || i === path.length - 1) return false;
        const a = path[i - 1], b = path[i], c = path[i + 1];
        const v1x = a.x - b.x, v1y = a.y - b.y, v2x = c.x - b.x, v2y = c.y - b.y;
        const dot = v1x * v2x + v1y * v2y;
        return Math.abs(dot) < 1e-9; // perpendicular == 90 degrees exactly
      });
      if (!has90) continue;
      // Found a genuine 90-degree placement — prove the gate has real teeth.
      if (!isValidBendPath('dual', path) && !isValidBendPath('aroad', path)) {
        assert.equal(isValidBendPath('rail', path), true, 'rail (67.5 min) must still accept a 90-degree bend');
        assert.equal(isValidBendPath('motorway', path), true, 'motorway (90 min) must still accept a 90-degree bend');
        assert.equal(isValidBendPath('minor', path), true, 'minor (45 min) must still accept a 90-degree bend');
        sawRightAngleRejection = true;
      }
    }
    assert.ok(
      sawRightAngleRejection,
      'HONESTY FIX: across many real-section seeds, a right-angle bend must actually occur AND actually fail the AC-2 gate for dual/A-road — not decorative',
    );
  });
});

describe('FEAT-2326609779 AC-10 — deterministic layout seed and candidate search', () => {
  test('layoutSeedOf is a pure function of (sectionKey, tick) — repeated calls are byte-identical', () => {
    const a = layoutSeedOf(42, 900);
    const b = layoutSeedOf(42, 900);
    assert.equal(a, b);
    assert.equal(typeof a, 'number');
    assert.ok(Number.isFinite(a));
  });

  test('layoutSeedOf never reads Math.random/Date.now — same section, different tick, still deterministic per call', () => {
    const t1 = layoutSeedOf(1, 100);
    const t2 = layoutSeedOf(1, 100);
    const t3 = layoutSeedOf(1, 200);
    assert.equal(t1, t2);
    // Not asserting t1 !== t3 (a hash COULD collide) — only that repeated
    // calls with the SAME inputs are identical, which is the actual GR#21 contract.
    assert.equal(typeof t3, 'number');
  });

  test('candidateTierPath finds the longest free run and respects MIN_TIER_RUN_TILES', () => {
    const box = { x0: 0, y0: 0, w: 8, h: 1 };
    // A free run of exactly MIN_TIER_RUN_TILES-1 tiles must be rejected...
    const shortFree = new Set(['0,0', '1,0']); // length 2 < MIN_TIER_RUN_TILES (3)
    assert.equal(candidateTierPath(shortFree, box, 0).length, 0);
    // ...while a run of exactly MIN_TIER_RUN_TILES is accepted.
    const justEnough = new Set(['0,0', '1,0', '2,0']);
    assert.equal(candidateTierPath(justEnough, box, 0).length, MIN_TIER_RUN_TILES);
  });

  test('candidateTierPath picks the LONGEST run when multiple runs exist, deterministically', () => {
    const box = { x0: 0, y0: 0, w: 10, h: 1 };
    const free = new Set(['0,0', '1,0', '5,0', '6,0', '7,0', '8,0']); // runs of 2 and 4
    const path = candidateTierPath(free, box, 0);
    assert.equal(path.length, 4);
    assert.deepEqual(path, [{ x: 5, y: 0 }, { x: 6, y: 0 }, { x: 7, y: 0 }, { x: 8, y: 0 }]);
  });

  test('candidateTierPath is order-independent: shuffling the Set\'s insertion order never changes the result (AC-10)', () => {
    const box = { x0: 0, y0: 0, w: 10, h: 1 };
    const keysInOrder = ['5,0', '6,0', '7,0', '8,0', '0,0', '1,0'];
    const shuffled = ['1,0', '0,0', '8,0', '7,0', '6,0', '5,0'];
    const a = candidateTierPath(new Set(keysInOrder), box, 0);
    const b = candidateTierPath(new Set(shuffled), box, 0);
    assert.deepEqual(a, b);
  });
});
