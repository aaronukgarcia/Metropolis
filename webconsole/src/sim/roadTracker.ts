/**
 * FEAT-1972079910 inc1/inc4: anchored road placement tracker.
 *
 * Pure logic for computing contiguous paths from an anchor to a cursor tile
 * using Bresenham-style 4-connected interpolation. No Date/Math.random (GR#21).
 * Exported functions are testable from node via tsx/node --test.
 *
 * inc4 (AC-5, bend legality) adds legalisePath/isBendLegal/bendRadiiOf. This
 * module stays a zero-import LEAF (data.ts's minBendRadiusTilesForTier owns
 * reading the tier's minimum radius from data/roads.json — GR#15 — and the
 * caller, e.g. MapView.tsx's tracker preview or engine.ts's placeRoadPath
 * reducer, passes that number in as `minRun`; this file never itself decides
 * WHAT the minimum is, only whether/how a path satisfies a given one).
 *
 * BEND DEFINITION (LEAD RULING R1, since the tile grid has no arcs): on a
 * 4-connected path, a bend is a heading change between consecutive steps.
 * The "run length" on one side of a bend is the number of tile-to-tile STEPS
 * (edges, not tile count — so a straight run of 3 tiles is a run of length
 * 2) in that direction before the heading changes again. The bend's radius
 * is the SHORTER of its two adjacent run lengths. A path is bend-legal for a
 * given `minRun` when every bend's radius is >= minRun AND no bend is a full
 * 180-degree reversal (a reversal is illegal regardless of run length).
 * bendRadiiOf/isBendLegal implement exactly this definition and are the
 * SHARED checker used both by the engine.ts reducer's enforcement and by
 * every test in this codebase — they are unaffected by the inc4 REWORK
 * below and still accept an arbitrary 4-connected Tile[] path.
 *
 * ALGORITHM v2 (legalisePath, LEAD RULING after r1 REJECT/BUG-1014/BUG-1015):
 * the inc4-r1 build SMOOTHED an arbitrary input path leg-by-leg after the
 * fact, which (a) collapsed every sub-minRun leg of a Bresenham staircase
 * into the FIRST heading — the "closest legal path" claim was false, the
 * shipped answer was the FARTHEST legal path of that tile count (BUG-1014)
 * — and (b) let a reversal-cancellation shrink an already-emitted leg below
 * minRun without re-validating the corner below it (BUG-1015). Both bugs are
 * structural to "smooth what's there"; the fix is to stop smoothing and
 * PLAN instead: legalisePath now takes only the two ENDPOINTS (anchor,
 * cursor) plus minRun and constructs a fresh legal staircase between them
 * from scratch — the raw mouse path's intermediate tiles are never
 * consulted, so there is nothing left to smooth and nothing left to
 * shrink-without-revalidating.
 *
 * Let dx = cursorX - anchorX, dy = cursorY - anchorY.
 *   - If dx === 0 or dy === 0, the path is a single straight leg (or the
 *     anchor tile alone) — always legal for any minRun, endpoint untouched.
 *   - Otherwise the cursor is reachable exactly when |dx| >= minRun AND
 *     |dy| >= minRun. When one delta's absolute value is below minRun, it is
 *     SNAPPED: extended to minRun or dropped to 0, whichever changes that
 *     axis's endpoint LESS (a tie prefers drop). `snapped` is reported so
 *     the caller can show the player where the road will really end.
 *   - With both (possibly snapped) deltas nonzero and >= minRun, the path is
 *     built as k = min(floor(|dx|/minRun), floor(|dy|/minRun)) alternating
 *     x/y leg PAIRS (k >= 1, since both axes are >= minRun by construction),
 *     starting on the longer axis (a tie starts on x). Each axis's total
 *     tile-steps are split across its k legs as evenly as floor division
 *     allows, with the REMAINDER distributed to the EARLIEST legs of that
 *     axis (deterministic, no Date/Math.random). Every leg's length is then
 *     >= minRun by construction (floor(total/k) >= minRun follows directly
 *     from k <= floor(total/minRun)), no two adjacent legs are ever the same
 *     axis (so never a reversal — only two distinct headings are ever used,
 *     always perpendicular), and the path always lands exactly on the
 *     (possibly snapped) cursor. The result is bend-legal BY CONSTRUCTION
 *     (nothing to smooth after the fact) and IDEMPOTENT because it is a pure
 *     function of (anchor, cursor, minRun) alone — replanning the same
 *     endpoints with the same minRun reproduces the identical leg split
 *     every time, independent of any prior output.
 *   - minRun <= 1 is special-cased to delegate straight to computePath: at
 *     minRun 1 every real leg (length >= 1) already satisfies the minimum
 *     trivially, so there is no legality constraint to plan around, and the
 *     REQUIRED byte-for-byte match with computePath's own greedy Bresenham
 *     tie-break (prefer horizontal on a tie) is guaranteed by construction
 *     rather than by trying to re-derive Bresenham's uneven step clustering
 *     from the even-split leg-planner above (the two algorithms cluster
 *     steps differently in general — see the tier-1 identity test).
 */

export interface Tile {
  x: number;
  y: number;
}

/**
 * Compute a 4-connected orthogonal path from (x0, y0) to (x1, y1).
 * Bresenham-style: at each step, move horizontally or vertically (never diagonal),
 * choosing the direction that reduces the Manhattan distance most.
 * Pure function: no side effects, no Date/Math.random.
 *
 * Returns the FULL path including BOTH start and end tiles. Single-click (same start/end)
 * produces a one-tile path [start].
 *
 * AC-1 (contiguity): every consecutive pair is orthogonally adjacent.
 * AC-2 (frame-rate independence): identical (start, end) → identical path.
 */
export function computePath(x0: number, y0: number, x1: number, y1: number): Tile[] {
  const path: Tile[] = [{ x: x0, y: y0 }];
  let x = x0;
  let y = y0;

  // Main loop: while not at the end, pick the next step.
  while (x !== x1 || y !== y1) {
    const dx = x1 - x;
    const dy = y1 - y;
    const adx = Math.abs(dx);
    const ady = Math.abs(dy);

    // Greedy: move in whichever direction reduces Manhattan distance.
    // On a tie, prefer horizontal (x before y).
    if (adx >= ady) {
      x += dx > 0 ? 1 : -1;
    } else {
      y += dy > 0 ? 1 : -1;
    }
    path.push({ x, y });
  }

  return path;
}

/**
 * Assembly: given a sequence of cursor tile positions, assemble a contiguous path
 * by chaining computePath() from the anchor through each cursor position.
 * Deduplicates the end of each segment with the start of the next (avoids double-counting).
 *
 * AC-1: result is a single 4-connected component.
 * AC-2: same cursor sequence → same path (independent of sampling density).
 */
export function assemblePath(anchorX: number, anchorY: number, cursorPath: Tile[]): Tile[] {
  const assembled: Tile[] = [];
  let currentX = anchorX;
  let currentY = anchorY;

  for (const cursor of cursorPath) {
    const segment = computePath(currentX, currentY, cursor.x, cursor.y);
    // Add the segment, but skip the first tile if we already added it (from a prior segment).
    const startIdx = assembled.length > 0 ? 1 : 0;
    for (let i = startIdx; i < segment.length; i++) {
      assembled.push(segment[i]);
    }
    currentX = cursor.x;
    currentY = cursor.y;
  }

  return assembled;
}

/** One of the 4 orthogonal headings a tile-to-tile step can take. */
type Dir = 'N' | 'S' | 'E' | 'W';

const OPPOSITE: Record<Dir, Dir> = { N: 'S', S: 'N', E: 'W', W: 'E' };

/** A compressed straight run: `len` consecutive unit steps in `dir`. */
interface Leg {
  dir: Dir;
  len: number;
}

function stepDir(a: Tile, b: Tile): Dir {
  const dx = b.x - a.x;
  const dy = b.y - a.y;
  if (dx === 1 && dy === 0) return 'E';
  if (dx === -1 && dy === 0) return 'W';
  if (dx === 0 && dy === 1) return 'S';
  if (dx === 0 && dy === -1) return 'N';
  throw new Error(`roadTracker.legalisePath: tiles (${a.x},${a.y}) -> (${b.x},${b.y}) are not 4-connected unit steps`);
}

/** Compress a 4-connected tile path into direction legs (adjacent legs always differ in dir). */
function toLegs(path: Tile[]): Leg[] {
  const legs: Leg[] = [];
  for (let i = 1; i < path.length; i++) {
    const d = stepDir(path[i - 1], path[i]);
    const last = legs[legs.length - 1];
    if (last && last.dir === d) {
      last.len += 1;
    } else {
      legs.push({ dir: d, len: 1 });
    }
  }
  return legs;
}

const UNIT: Record<Dir, { dx: number; dy: number }> = {
  N: { dx: 0, dy: -1 },
  S: { dx: 0, dy: 1 },
  E: { dx: 1, dy: 0 },
  W: { dx: -1, dy: 0 },
};

/** Rebuild a tile path by walking `legs` from `start`, one leg at a time. */
function fromLegs(start: Tile, legs: Leg[]): Tile[] {
  const out: Tile[] = [{ x: start.x, y: start.y }];
  let x = start.x;
  let y = start.y;
  for (const leg of legs) {
    const u = UNIT[leg.dir];
    for (let i = 0; i < leg.len; i++) {
      x += u.dx;
      y += u.dy;
      out.push({ x, y });
    }
  }
  return out;
}

/** Result of {@link legalisePath}: a freshly PLANNED, always bend-legal staircase. */
export interface LegalPathResult {
  tiles: Tile[];
  /** Where the plan actually ends — equals (cursorX, cursorY) unless `snapped`. */
  endX: number;
  endY: number;
  /** True when the requested cursor was not exactly reachable and an axis was snapped. */
  snapped: boolean;
}

/** Split `total` tile-steps across `count` legs as evenly as integer division
 * allows, giving the REMAINDER to the EARLIEST legs (deterministic, GR#21). */
function splitEven(total: number, count: number): number[] {
  const base = Math.floor(total / count);
  const rem = total - base * count;
  const legs: number[] = [];
  for (let i = 0; i < count; i++) legs.push(base + (i < rem ? 1 : 0));
  return legs;
}

/**
 * AC-5 (LEAD RULING v2, post r1-REJECT): PLAN a fresh 4-connected staircase
 * from (anchorX, anchorY) to (cursorX, cursorY) whose every leg is >= minRun
 * tile-steps, with no reversal — see the header comment for the full
 * algorithm. Deterministic, no Date/Math.random (GR#21). Idempotent: calling
 * legalisePath again with the returned (endX, endY) as the new cursor and
 * the same minRun reproduces the identical result, since the output depends
 * only on (anchor, cursor, minRun), never on a prior call's path.
 *
 * `minRun` must be a positive integer (the tier's minimum run length, in
 * steps — read via data.ts's minBendRadiusTilesForTier, GR#15: this function
 * never reads the road spec itself, only accepts the number).
 */
export function legalisePath(
  anchorX: number,
  anchorY: number,
  cursorX: number,
  cursorY: number,
  minRun: number
): LegalPathResult {
  // minRun <= 1: no legality constraint exists — delegate straight to
  // computePath for a guaranteed byte-for-byte match with its own Bresenham
  // tie-break (see header comment; the even-split planner below clusters
  // steps differently from Bresenham in general, so it cannot substitute).
  if (!(minRun > 1)) {
    return {
      tiles: computePath(anchorX, anchorY, cursorX, cursorY),
      endX: cursorX,
      endY: cursorY,
      snapped: false,
    };
  }

  const dx = cursorX - anchorX;
  const dy = cursorY - anchorY;

  // A straight leg (or a single-tile no-op) never bends, so it is legal at
  // any minRun and the endpoint is never touched.
  if (dx === 0 || dy === 0) {
    return {
      tiles: computePath(anchorX, anchorY, cursorX, cursorY),
      endX: cursorX,
      endY: cursorY,
      snapped: false,
    };
  }

  let snapped = false;
  const snapAxis = (delta: number): number => {
    const abs = Math.abs(delta);
    if (abs >= minRun) return delta;
    snapped = true;
    const dropChange = abs; // change if the axis is dropped to 0
    const extendChange = minRun - abs; // change if the axis is extended to minRun
    return dropChange <= extendChange ? 0 : Math.sign(delta) * minRun;
  };

  const sx = snapAxis(dx);
  const sy = snapAxis(dy);
  const endX = anchorX + sx;
  const endY = anchorY + sy;

  if (sx === 0 || sy === 0) {
    // A snap dropped one axis entirely: the plan is a straight leg (or the
    // anchor tile alone) to the snapped endpoint — always legal.
    return { tiles: computePath(anchorX, anchorY, endX, endY), endX, endY, snapped };
  }

  const adx = Math.abs(sx);
  const ady = Math.abs(sy);
  const k = Math.min(Math.floor(adx / minRun), Math.floor(ady / minRun));
  // k >= 1: both adx and ady are either the original delta (already
  // >= minRun) or snapped exactly to minRun, so floor(./minRun) >= 1 always.

  const xLegLens = splitEven(adx, k);
  const yLegLens = splitEven(ady, k);
  const xDir: Dir = sx > 0 ? 'E' : 'W';
  const yDir: Dir = sy > 0 ? 'S' : 'N';
  const xFirst = adx >= ady; // longer axis leads; a tie starts on x.

  const legs: Leg[] = [];
  for (let i = 0; i < k; i++) {
    const xLeg: Leg = { dir: xDir, len: xLegLens[i] };
    const yLeg: Leg = { dir: yDir, len: yLegLens[i] };
    if (xFirst) {
      legs.push(xLeg, yLeg);
    } else {
      legs.push(yLeg, xLeg);
    }
  }

  return { tiles: fromLegs({ x: anchorX, y: anchorY }, legs), endX, endY, snapped };
}

/**
 * The bend radius (shorter adjacent run, in steps) at every interior corner
 * of `path`, in order. Exported so tests assert legality against the SAME
 * definition legalisePath uses, rather than a hand-rolled reimplementation.
 * A reversal is reported as radius 0 (always illegal, regardless of minRun).
 */
export function bendRadiiOf(path: Tile[]): number[] {
  if (path.length <= 2) return [];
  const legs = toLegs(path);
  const radii: number[] = [];
  for (let i = 1; i < legs.length; i++) {
    if (OPPOSITE[legs[i].dir] === legs[i - 1].dir) {
      radii.push(0);
    } else {
      radii.push(Math.min(legs[i - 1].len, legs[i].len));
    }
  }
  return radii;
}

/** True when every bend in `path` has radius >= minRun and none is a reversal. */
export function isBendLegal(path: Tile[], minRun: number): boolean {
  return bendRadiiOf(path).every((r) => r >= minRun);
}
