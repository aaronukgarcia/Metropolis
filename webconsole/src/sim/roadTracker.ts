/**
 * FEAT-1972079910 inc1: anchored road placement tracker.
 *
 * Pure logic for computing contiguous paths from an anchor to a cursor tile
 * using Bresenham-style 4-connected interpolation. No Date/Math.random (GR#21).
 * Exported functions are testable from node via tsx/node --test.
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
