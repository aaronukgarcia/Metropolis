// roadConnect.ts — FEAT-1972079907 inc1: deterministic grid router for road auto-connect.
//
// PURE + DETERMINISTIC (GR#21): given a board (occupied cells + road cells) and a
// building footprint, plan the connector road that bridges the building to the
// NEAREST existing road, routing AROUND occupied tiles. NO Date.now / Math.random
// anywhere — the same board + footprint always yields the identical plan.
//
// Kept free of SimState/SPECS so it can be unit-tested with plain sets. engine.ts
// builds the sets from state and turns a plan into journaled reducer mutations.

export interface Pt {
  x: number;
  y: number;
}

/**
 * Result of planning a connector for a placed building.
 * - `connected`  — the building already touches a road; nothing to lay.
 * - `path`       — connector cells to lay as new road tiles, ordered from the
 *                  building side to the road side. Empty when connected/blocked.
 * - `junctions`  — existing road cells orthogonally adjacent to the road-side end
 *                  of `path`; upgrade-on-connect targets these.
 * - `blocked`    — no route to any road within the cost budget.
 */
export interface ConnectPlan {
  connected: boolean;
  path: Pt[];
  junctions: Pt[];
  blocked: boolean;
}

/**
 * Neighbour scan order — the ONLY source of direction-ordinal tie-breaking.
 * Fixed: up, left, right, down. Deterministic (GR#21).
 */
const DIRS: Pt[] = [
  { x: 0, y: -1 }, // up
  { x: -1, y: 0 }, // left
  { x: 1, y: 0 }, // right
  { x: 0, y: 1 }, // down
];

/**
 * Default exploration budget (max cells settled) before a connect is declared
 * blocked. PLACEHOLDER-balance (flagged for Aaron): bounds pathfinding cost at
 * scale while comfortably covering any reasonable connector distance.
 */
export const CONNECT_BUDGET = 6000;

const key = (x: number, y: number) => `${x},${y}`;

/** Sort points strictly by (x, y) — the primary deterministic tie-break. */
function byXY(a: Pt, b: Pt): number {
  return a.x - b.x || a.y - b.y;
}

/** True if cell (x,y) is orthogonally adjacent to any road tile. */
function touchesRoad(x: number, y: number, roads: Set<string>): boolean {
  return (
    roads.has(key(x, y - 1)) ||
    roads.has(key(x - 1, y)) ||
    roads.has(key(x + 1, y)) ||
    roads.has(key(x, y + 1))
  );
}

export interface PlanParams {
  /** All impassable cells (every building footprint, roads included). */
  occupied: Set<string>;
  /** Drivable road cells (subset of `occupied`). */
  roads: Set<string>;
  /** Building footprint rectangle. */
  bx: number;
  by: number;
  bw: number;
  bh: number;
  /** Map bounds. */
  mapW: number;
  mapH: number;
  /** Max cells to settle before declaring blocked. */
  budget?: number;
}

/**
 * Plan a connector from a building footprint to the nearest existing road.
 *
 * Determinism: a level-synchronous BFS (all steps cost 1). Each frontier level is
 * processed in strict (x, y) order, so (a) the FIRST goal found is the nearest one
 * tie-broken by lowest (x, y), and (b) a cell's parent is always its lowest-(x, y)
 * predecessor at the minimal distance. Direction ordinal (DIRS) only orders the
 * within-cell neighbour scan. No randomness, no wall-clock.
 */
export function planConnector(params: PlanParams): ConnectPlan {
  const { occupied, roads, bx, by, bw, bh, mapW, mapH } = params;
  const budget = params.budget ?? CONNECT_BUDGET;

  // (1) Already connected? Any footprint cell orthogonally adjacent to a road.
  for (let dx = 0; dx < bw; dx++) {
    for (let dy = 0; dy < bh; dy++) {
      if (touchesRoad(bx + dx, by + dy, roads)) {
        return { connected: true, path: [], junctions: [], blocked: false };
      }
    }
  }

  const inBounds = (x: number, y: number) => x >= 0 && y >= 0 && x < mapW && y < mapH;
  const passable = (x: number, y: number) => inBounds(x, y) && !occupied.has(key(x, y));

  // Seed frontier: passable cells orthogonally adjacent to the building footprint.
  const parent = new Map<string, string | null>();
  const visited = new Set<string>();
  const seed: Pt[] = [];
  const pushSeed = (x: number, y: number) => {
    if (!passable(x, y)) return;
    const k = key(x, y);
    if (visited.has(k)) return;
    visited.add(k);
    parent.set(k, null);
    seed.push({ x, y });
  };
  for (let dx = 0; dx < bw; dx++) {
    pushSeed(bx + dx, by - 1);
    pushSeed(bx + dx, by + bh);
  }
  for (let dy = 0; dy < bh; dy++) {
    pushSeed(bx - 1, by + dy);
    pushSeed(bx + bw, by + dy);
  }

  let frontier = seed.sort(byXY);
  let settled = 0;

  while (frontier.length > 0 && settled < budget) {
    // Goal check FIRST, in (x, y) order → nearest, lowest-(x,y) goal wins.
    for (const c of frontier) {
      if (touchesRoad(c.x, c.y, roads)) {
        return reconstruct(c, parent, roads);
      }
    }
    const next: Pt[] = [];
    for (const c of frontier) {
      settled++;
      for (const d of DIRS) {
        const nx = c.x + d.x;
        const ny = c.y + d.y;
        if (!passable(nx, ny)) continue;
        const nk = key(nx, ny);
        if (visited.has(nk)) continue;
        visited.add(nk);
        parent.set(nk, key(c.x, c.y));
        next.push({ x: nx, y: ny });
      }
    }
    frontier = next.sort(byXY);
  }

  // No road reachable within budget.
  return { connected: false, path: [], junctions: [], blocked: true };
}

/** Walk parent pointers from the road-side goal back to the building-side seed. */
function reconstruct(goal: Pt, parent: Map<string, string | null>, roads: Set<string>): ConnectPlan {
  const path: Pt[] = [];
  let cur: string | null = key(goal.x, goal.y);
  while (cur != null) {
    const [x, y] = cur.split(',').map(Number);
    path.push({ x, y });
    cur = parent.get(cur) ?? null;
  }
  path.reverse(); // building side → road side

  // Junction road tiles: existing roads orthogonally adjacent to the road-side end.
  const junctions: Pt[] = [];
  for (const d of DIRS) {
    const jx = goal.x + d.x;
    const jy = goal.y + d.y;
    if (roads.has(key(jx, jy))) junctions.push({ x: jx, y: jy });
  }
  junctions.sort(byXY);

  return { connected: false, path, junctions, blocked: false };
}
