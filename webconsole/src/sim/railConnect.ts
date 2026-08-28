// railConnect.ts — FEAT-1972079902 inc3: deterministic grid router for rail
// auto-branch-lining on gateway placement.
//
// PURE + DETERMINISTIC (GR#21): given a board (occupied cells) and a gateway
// footprint, plan a branch line that bridges the gateway to the NEAREST tile of a
// target rail line (a set of line tiles — e.g. every 'rail' tile, or every 'hs1'
// tile), routing AROUND occupied tiles. NO Date.now / Math.random anywhere — the
// same board + footprint + target set always yields the identical plan.
//
// This is the SAME router idiom as roadConnect.planConnector (level-synchronous
// BFS, strict (x,y) tie-break, fixed direction ordinal, cost budget). It is kept a
// SIBLING rather than a modification of planConnector so road inc1/inc2 stay
// byte-for-byte untouched. The one behavioural difference: the goal is "orthogonally
// adjacent to ANY tile in an arbitrary target set" (the line to branch to), whereas
// planConnector hard-codes the road set and additionally computes upgrade junctions
// (rail lines are never upgraded, so no junction bookkeeping here).
//
// Kept free of SimState/SPECS so it can be unit-tested with plain sets. engine.ts
// builds the sets from state and turns a plan into journaled reducer mutations.

export interface Pt {
  x: number;
  y: number;
}

/**
 * Result of planning a rail branch from a gateway footprint to a target line.
 * - `connected` — the gateway already touches the target line; nothing to lay.
 * - `path`      — branch cells to lay as new line tiles, ordered from the gateway
 *                 side to the line side. Empty when connected/blocked.
 * - `blocked`   — no route to the target line within the cost budget.
 */
export interface BranchPlan {
  connected: boolean;
  path: Pt[];
  blocked: boolean;
}

/**
 * Neighbour scan order — the ONLY source of direction-ordinal tie-breaking.
 * Fixed: up, left, right, down. Deterministic (GR#21). Identical ordinal to
 * roadConnect.DIRS so the two routers tie-break the same way.
 */
const DIRS: Pt[] = [
  { x: 0, y: -1 }, // up
  { x: -1, y: 0 }, // left
  { x: 1, y: 0 }, // right
  { x: 0, y: 1 }, // down
];

/**
 * Default exploration budget (max cells settled) before a branch is declared
 * blocked. PLACEHOLDER-balance (flagged for Aaron): a gateway can sit far from the
 * nearest line (a corner airport reaching a mid-map HS1 line), so this is sized
 * ABOVE the whole board's cell count (MAP_W·MAP_H = 440·260 = 114,400) — a branch is
 * therefore only ever "blocked" when the line is genuinely walled off, never merely
 * distant. Runs only on the rare gateway placement, so the sweep cost is a non-issue.
 */
export const RAIL_BRANCH_BUDGET = 200000;

const key = (x: number, y: number) => `${x},${y}`;

/** Sort points strictly by (x, y) — the primary deterministic tie-break. */
function byXY(a: Pt, b: Pt): number {
  return a.x - b.x || a.y - b.y;
}

/** True if cell (x,y) is orthogonally adjacent to any tile in the target line set. */
function touchesTarget(x: number, y: number, targets: Set<string>): boolean {
  return (
    targets.has(key(x, y - 1)) ||
    targets.has(key(x - 1, y)) ||
    targets.has(key(x + 1, y)) ||
    targets.has(key(x, y + 1))
  );
}

export interface BranchParams {
  /** All impassable cells (every building footprint, target line tiles included). */
  occupied: Set<string>;
  /** The target line's tiles — the branch connects to the nearest of these. */
  targets: Set<string>;
  /** Gateway footprint rectangle. */
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
 * Plan a branch from a gateway footprint to the nearest tile of the target line.
 *
 * Determinism: a level-synchronous BFS (all steps cost 1). Each frontier level is
 * processed in strict (x, y) order, so (a) the FIRST goal found is the nearest one
 * tie-broken by lowest (x, y), and (b) a cell's parent is always its lowest-(x, y)
 * predecessor at the minimal distance. Direction ordinal (DIRS) only orders the
 * within-cell neighbour scan. No randomness, no wall-clock — identical discipline
 * to roadConnect.planConnector.
 */
export function planRailBranch(params: BranchParams): BranchPlan {
  const { occupied, targets, bx, by, bw, bh, mapW, mapH } = params;
  const budget = params.budget ?? RAIL_BRANCH_BUDGET;

  // (1) Already connected? Any footprint cell orthogonally adjacent to the line.
  for (let dx = 0; dx < bw; dx++) {
    for (let dy = 0; dy < bh; dy++) {
      if (touchesTarget(bx + dx, by + dy, targets)) {
        return { connected: true, path: [], blocked: false };
      }
    }
  }

  const inBounds = (x: number, y: number) => x >= 0 && y >= 0 && x < mapW && y < mapH;
  const passable = (x: number, y: number) => inBounds(x, y) && !occupied.has(key(x, y));

  // Seed frontier: passable cells orthogonally adjacent to the gateway footprint.
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
      if (touchesTarget(c.x, c.y, targets)) {
        return reconstruct(c, parent);
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

  // No target line reachable within budget.
  return { connected: false, path: [], blocked: true };
}

/** Walk parent pointers from the line-side goal back to the gateway-side seed. */
function reconstruct(goal: Pt, parent: Map<string, string | null>): BranchPlan {
  const path: Pt[] = [];
  let cur: string | null = key(goal.x, goal.y);
  while (cur != null) {
    const [x, y] = cur.split(',').map(Number);
    path.push({ x, y });
    cur = parent.get(cur) ?? null;
  }
  path.reverse(); // gateway side → line side
  return { connected: false, path, blocked: false };
}
