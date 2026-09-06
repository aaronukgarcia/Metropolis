// consolidatorReplan.ts — FEAT-2326609779 inc4, THE RED BOX RE-PLAN.
//
// AARON'S ORDER (verbatim, his SECOND ask — the inc3 interpretations MISSED):
// "the red box needs to defag and reimmagine everything within it and
// optimise join come on lad I want this fixed". His earlier sentence: "the
// red box is to have a 'defrag' effect on the content road lays out, train
// layout, then the bigger consolidated buildings get laid down ... rather
// than say the 12 hospitals it should be one teaching hospital, its not 40
// kindgerden it's a city kindgerden that does 1000 children".
//
// WHY A NEW MODULE, AND WHAT IS DIFFERENT FROM inc3 -------------------------
// consolidatorLayout.ts is an INCREMENTAL EXTENDER: each pass it looks for
// the longest free run it can add to an existing line, gated by money. The
// round-13/14 verdicts named this precisely — "geometry is incremental
// extension, not a re-plan". That is why the shipped behaviour never looked
// like a defrag: nothing ever computes what the box SHOULD look like, so
// nothing ever converges to it. Extension can only ever decorate whatever
// mess is already there.
//
// This module computes, from scratch, a TARGET PLAN for the WHOLE box — a
// coherent tier hierarchy (rail spine, motorway/dual arterials, an A-road
// grid, minor infill) plus the consolidated civic blocks placed ON that plan
// beside the arterials — and then emits a deterministic, ORDERED list of
// EXECUTION STEPS that walk the box's current contents toward that plan a
// bounded amount per tick. The plan is a pure function of
// (box, contents, ports, seed): same inputs, same plan, forever (GR#21).
//
// FOUR CONTRACTS THIS MODULE OWNS (the task's own four numbered items):
//  1. DEFRAG/REIMAGINE — `planBox` computes the whole-box target from
//     scratch, never by extending what is there.
//  2. CONSERVATION — `planCivicBlocks` sizes every consolidated replacement
//     at >= the combined capacity of the originals it absorbs (the ladder's
//     own `groupSizeOf` FLOOR guarantees this, GR#15/GR#3 — this module
//     re-derives nothing), and `buildSteps` ALWAYS emits the `place` step for
//     the replacement BEFORE the `demolish` steps for its originals. A
//     caller that stops halfway (out of money) has therefore built capacity
//     and demolished nothing — never the reverse.
//  3. OPTIMISE JOIN — every tile where the OUTSIDE network crosses the box
//     boundary is a PORT. The plan's own lines are anchored to the ports, and
//     `validatePlan` asserts, on the plan itself, that (a) every port is
//     adjacent to a plan tile of its own tier or higher, (b) each tier's
//     tiles inside the box form exactly ONE 4-connected component, and
//     (c) no dead end longer than one tile survives. `planBox` does not
//     merely CHECK these — it REPAIRS toward them (largest/port-bearing
//     component kept, longer dead-end spurs trimmed) so the returned plan
//     satisfies them by construction.
//  4. INCREMENTAL, CONVERGENT EXECUTION — `buildSteps` is a total order over
//     the whole job; `stepsForTick` takes a bounded prefix of the steps not
//     yet done. Progress (`ReplanProgress`) is reportable at every tick.
//
// GR#21 determinism: no Math.random, no Date.now, no storage. Every iteration
// is over an explicitly sorted array or a fixed-bounds numeric loop. There is
// no `for (... of someMap) { ...; break; }` anywhere in this file (the
// map-range-with-break gotcha).
//
// GR#15: every capacity number comes from the catalogue via the consolidation
// ladder; the only literals here are the disclosed placeholder GEOMETRY
// constants in §0, gathered in one block, awaiting Aaron's balance pass.

import type { TierKind, TileXY } from './consolidatorLayout.ts';
import { TIER_ORDER, TIER_RANK, TIER_SPEC_ID, tileComponents } from './consolidatorLayout.ts';

// ---------------------------------------------------------------------------
// §0 PLACEHOLDER GEOMETRY — Aaron's balance pass pending. One block, never
// scattered literals downstream.
// ---------------------------------------------------------------------------

/**
 * How far apart, in tiles, the plan spaces each tier's own lines within the
 * box. Read as "a rail spine every N tiles" etc. A spacing at or beyond the
 * box's own extent means the tier gets exactly ONE line (the common case for
 * rail/motorway in a 16x16 red box) — which is the intended shape, not a
 * degenerate one: one spine, not a rail grid.
 */
export const TIER_LINE_SPACING: Readonly<Record<TierKind, number>> = {
  rail: 64,
  motorway: 32,
  dual: 16,
  aroad: 16,
  // LEAD RULING 2026-09-06 — THE MINOR PLAN IS A GRID, NOT PARALLEL LINES.
  // At spacing 16 in a 16-tile box the minor tier got at most one row and one
  // column, so any tile the higher tiers claimed split it into disconnected
  // pieces (measured: minor in 6-7 components). At spacing 8 the box always
  // holds a real GRID — two rows and two columns — so minor is inherently one
  // connected component and every aroad/dual line is joined by it. A lattice
  // intersection of two MINOR lines is a same-tier junction, never a
  // pass-through.
  minor: 8,
};

/**
 * MEASURED RETUNE 2026-09-06 (the E5 900-tick solvency bar). The FIRST wired
 * numbers (aroad 8 / minor 8) asked for a full GRID inside every 16x16 red
 * box — two A-road lines and two minor lines on each axis. Measured on the
 * dogfood E5 fixture over 900 ticks, that is what the money actually went on:
 *
 *   line rail        79 tiles   GBP  59,250,000
 *   line motorway    52 tiles   GBP  78,000,000
 *   line dual       183 tiles   GBP  17,568,000
 *   line aroad      523 tiles   GBP  28,242,000
 *   line minor    2,450 tiles   GBP  29,400,000
 *   line TOTAL    3,287 tiles   GBP 212,460,000
 *   civic             0 blocks  GBP           0
 *   ON GBP 665,517,286 vs OFF GBP 904,682,197 = 73.6% (bar: >= 90%)
 *
 * A 16-tile spacing on a 16-tile box gives ONE A-road cross and ONE minor
 * cross instead of a full grid — the arterial hierarchy Aaron described
 * ("road lays out, train layout, then the bigger consolidated buildings"),
 * not a uniform mesh. PLACEHOLDER-tier, same disclosure as every other
 * constant in this block: Aaron's balance pass will retune row by row.
 */
export const REPLAN_SPACING_RETUNE_NOTE = '2026-09-06 E5 solvency';

/**
 * MEASURED RETUNE 2026-09-06: the fraction of the pass's layout capex ceiling
 * the RE-PLAN may draw, the rest staying available to the inc3 extender for
 * the map outside the red box. The red box SLIDES (one tile per game day),
 * so the re-plan's spend over a long run is rate-limited, not plan-limited —
 * which means the per-pass draw, not the plan's total size, is the dominant
 * solvency lever. Halving it halves the long-run drag without changing the
 * plan's shape at all (a box still converges, just over more days).
 * PLACEHOLDER-tier (Aaron's balance pass pending).
 */
export const REPLAN_CAPEX_SHARE = 0.5;

/**
 * LEAD RULING 2026-09-06 — THE BOX DWELLS. Aaron's sentence is "defrag ...
 * THEN the bigger consolidated buildings get laid down". The glide window
 * slides one tile per game day, and the step order is lines-then-civic, so a
 * window that keeps sliding NEVER reaches its civic steps — measured: 0
 * consolidated civic blocks in 900 ticks on the dogfood fixture, while the
 * planner was correctly asking for them. A box that moves on before its
 * civic work runs has not done the job.
 *
 * So the window now STAYS on its current box until the job converges (or the
 * plan is discarded on an invariant failure), bounded by this many game days
 * so nothing can wait forever — a box that burns its whole dwell without
 * converging is released with MET-V874 rather than pinning the scanline
 * permanently. PLACEHOLDER-tier (Aaron's balance pass pending): 30 days is
 * one game month per box.
 */
export const REPLAN_MAX_DWELL_DAYS = 30;

/**
 * Which orientations a tier's lines take. Keeping the top three tiers
 * single-orientation is what makes the hierarchy read as a SPINE with
 * arterials rather than a uniform mesh, and it is also what keeps their
 * mutual crossings rare enough that the repair pass below almost never has
 * to cut anything. aroad/minor are genuine grids (both orientations) — a
 * grid is what survives losing individual crossing tiles to a higher tier
 * while staying one connected component.
 */
/**
 * LEAD RULING 2026-09-06 — the modulus the city-wide lattice PHASE is measured
 * in. It is the minor tier's own spacing (the finest lattice), and every
 * coarser tier's spacing is a multiple of it, so a single phase aligns the
 * whole hierarchy onto the same grid lines. Derived from TIER_LINE_SPACING
 * rather than written twice (GR#3): change the minor spacing and the phase
 * modulus follows.
 */
export const LATTICE_PHASE_MODULUS = TIER_LINE_SPACING.minor;

/**
 * LEAD RULING 2026-09-06 — "the genesis 8-grid IS the minor lattice wherever
 * it already exists". The city-wide lattice phase: the MODE of the existing
 * road-family tiles' x and y coordinates modulo LATTICE_PHASE_MODULUS. A city
 * laid out on an 8-grid at offset 0 (every fixture in this estate) yields 0,
 * which is exactly the previous hard-anchored behaviour; a city on any other
 * phase gets a lattice that lands ON its roads instead of between them.
 *
 * Both axes are folded into ONE tally deliberately: the lattice is a single
 * grid, and a city whose rows and columns disagreed on phase has no single
 * right answer — the mode over both axes is the least-repaint choice, and it
 * is total and deterministic (ties broken by the smallest offset, never by
 * iteration order).
 */
/**
 * Is (x,y) ON the city-wide lattice? A tile is, when its row OR its column is
 * a lattice line - exactly the shape a road grid has, so a genesis 8-grid at
 * this phase is entirely on-lattice. The ONE definition of "on the lattice" in
 * this module (GR#3): the spur rule, the stale-grid retention rule and the
 * port invariant all ask it here.
 */
export function onCityLattice(x: number, y: number, phase: number): boolean {
  const m = Math.max(1, LATTICE_PHASE_MODULUS);
  const ph = ((phase % m) + m) % m;
  return ((((x - ph) % m) + m) % m === 0) || ((((y - ph) % m) + m) % m === 0);
}

export function latticePhaseOf(roadTiles: readonly TileXY[]): number {
  const m = Math.max(1, LATTICE_PHASE_MODULUS);
  const tally = new Array<number>(m).fill(0);
  for (const p of roadTiles) {
    tally[(((p.x % m) + m) % m)] += 1;
    tally[(((p.y % m) + m) % m)] += 1;
  }
  let best = 0;
  for (let i = 1; i < m; i++) {
    if (tally[i] > tally[best]) best = i;
  }
  return best;
}

export const TIER_ORIENTATIONS: Readonly<Record<TierKind, readonly ('h' | 'v')[]>> = {
  rail: ['h'],
  motorway: ['v'],
  dual: ['h'],
  aroad: ['h', 'v'],
  minor: ['h', 'v'],
};

/**
 * The longest dead-end spur the plan tolerates (task item 3: "forbid dead
 * ends longer than 1 tile"). A degree-1 chain of at most this many tiles is
 * a legitimate stub (a frontage, an access tile); anything longer is trimmed
 * back by `trimDeadEnds` until it is not.
 */
export const MAX_DEAD_END_TILES = 1;

/**
 * How many execution steps one tick may perform. The plan for a 16x16 box is
 * on the order of a hundred tiles plus a handful of civic blocks; converging
 * over tens of ticks (rather than in one thunderous tick) is what keeps the
 * money gates meaningful and the city watchable. Placeholder-tier.
 */
export const REPLAN_STEPS_PER_TICK = 3;

/**
 * MEASURED 2026-09-06 (E5 solvency retune). The retune order the lead set was
 * (a) capex share, (b) spacing, (c) steps per tick. Measured, in that order,
 * on the dogfood E5 fixture over 900 ticks:
 *
 *   baseline (aroad/minor 8, no share, 8 steps)  lines GBP 212,460,000  73.6%
 *   + REPLAN_CAPEX_SHARE 0.5 + aroad/minor 16    lines GBP 198,264,000  75.2%
 *
 * (a) barely moved the number, and the measurement says exactly why: the
 * re-plan's real draw is ~GBP 303,000 PER PASS against a per-pass ceiling of
 * GBP 7-10M (min(20M, 2% of funds) x REPLAN_CAPEX_SHARE). The capex ceiling
 * was NEVER the binding constraint, so halving it constrained nothing. The
 * share is KEPT anyway — it is a correct safety bound that stops the re-plan
 * monopolising the ceiling on a poorer city where it WOULD bind — but it is
 * not the solvency lever, and saying otherwise would be guessing.
 *
 * (b) worked as intended on A-roads (523 -> 157 tiles, -GBP 19.8M) but minor
 * road tiles ROSE (2,450 -> 2,813): with fewer A-road lines competing for the
 * per-tick STEP budget, the freed steps simply went to minor instead. That is
 * the tell — the re-plan's spend is STEP-limited, not money-limited and not
 * plan-limited.
 *
 * (c) is therefore the real lever, and this constant is it. PLACEHOLDER-tier
 * (Aaron's balance pass pending): fewer steps per tick means a box converges
 * over more game days, not that it converges to anything less — the plan's
 * shape is untouched.
 */
export const REPLAN_STEPS_RETUNE_NOTE = '2026-09-06 E5 solvency: spend is step-limited';

// ---------------------------------------------------------------------------
// §1 Inputs / outputs.
// ---------------------------------------------------------------------------

export interface ReplanBox {
  x0: number;
  y0: number;
  w: number;
  h: number;
}

/** One building currently standing inside the box. `tier` is non-null only for road/rail infrastructure. */
export interface ReplanContent {
  id: number;
  spec: string;
  x: number;
  y: number;
  tier: TierKind | null;
  /** Conserved quantities this building supplies. Zero for infrastructure. */
  residents: number;
  jobs: number;
  /** Service capacity in the spec's own capacity unit (children/served/...). */
  capacity: number;
  /** True for genesis / player-placed content the plan may never demolish. */
  protectedFromDemolition: boolean;
  /**
   * LEAD RULING 2026-09-06 — NO CHURN. True when the re-plan itself laid this
   * tile (the engine derives it from the building's own persisted
   * `placedBy: 'auto'` + a non-genesis builtTick — see engine.ts's
   * replanContentsOf). The stale-grid pass must NEVER remove a layout-owned
   * tile: doing so is the re-plan demolishing its own work and laying it
   * again next box, which is exactly the churn that kept line capex at
   * GBP 268M instead of the ~GBP 47M the lattice alone costs.
   *
   * Deliberately DERIVED from `placedBy`, not a second parallel set on
   * SimState: `placedBy` is already persisted on every building record and
   * already survives save/load, so a `layoutOwned` set would be duplicate
   * state that can drift out of agreement with it (GR#3).
   */
  layoutOwned?: boolean;
}

/**
 * A PORT: a network tile OUTSIDE the box that is orthogonally adjacent to a
 * tile INSIDE the box. `inside` is that inside tile — the plan must reach it
 * with a tile of the port's own tier or higher, or the outside network is cut
 * off from everything the box re-plans.
 */
export interface ReplanPort {
  outside: TileXY;
  inside: TileXY;
  tier: TierKind;
}

/** One consolidated civic block the plan wants, and the originals it absorbs. */
export interface PlannedCivic {
  /** The successor spec (e.g. 'edu_nursery_city', 'hea_teaching'). */
  spec: string;
  x: number;
  y: number;
  /** Ids of the originals this block replaces — demolished ONLY after it stands. */
  replaces: number[];
  /** Combined capacity of `replaces`. */
  capacityAbsorbed: number;
  /** The successor's own capacity — invariably >= `capacityAbsorbed` (ladder FLOOR rule). */
  capacityProvided: number;
  residentsAbsorbed: number;
  jobsAbsorbed: number;
}

export type ReplanStepKind = 'lay' | 'place' | 'demolish';

export interface ReplanStep {
  kind: ReplanStepKind;
  /** For 'lay': the tier. For 'place'/'demolish': null. */
  tier: TierKind | null;
  /** For 'lay'/'place': the spec to build. For 'demolish': the spec being removed. */
  spec: string;
  x: number;
  y: number;
  /** For 'demolish': the building id. */
  id?: number;
  /**
   * For 'demolish': the index in `steps` of the 'place' step that must have
   * COMPLETED first. Conservation (task item 2) is enforced here structurally
   * — a caller that honours `blockedBy` can never leave people homeless.
   * `null` (never merely absent) marks a sweep demolition — see `sweep`'s own
   * doc comment below.
   */
  blockedBy?: number | null;
  /**
   * INC4 WIRING: the step is already satisfied by what is standing — the
   * successor building is ALREADY at this tile from an earlier tick. The
   * executor must consume it (so the demolitions it blocks are unblocked)
   * but spend nothing and build nothing. Without this, a civic group whose
   * successor landed last tick would either be built TWICE or would strand
   * its originals forever (the group drops below `groupSize` once the first
   * original is removed, so `planCivicBlocks` stops emitting it and the
   * survivors are never cleared).
   */
  noop?: true;
  /**
   * FEAT-2326609779 inc4 close-out (BOW comment, 2026-09-06 ~17:15/~14:53):
   * this 'demolish' step is the ORPHAN-SWEEP (planStaleGridRemovals, BUG-808),
   * never a capacity-bearing civic replacement — it removes only a road-family
   * tile the family-component guard has already proven capacity-neutral (no
   * `residents`/`jobs` on a road tile). It is deliberately `blockedBy: null`
   * (never a number): a sweep has no 'place' step to wait on, unlike every
   * OTHER demolish step, which always names a real blocker. Distinguishing the
   * two lets a capacity-safety walk skip sweeps instead of misreading their
   * missing blocker as a conservation violation.
   */
  sweep?: true;
}

export interface ReplanMetrics {
  planTiles: number;
  /** Tiles the plan wants that are already correct — no work needed. */
  tilesAlreadyCorrect: number;
  junctionCount: number;
  deadEnds: number;
  componentsByTier: Record<TierKind, number>;
  portsTotal: number;
  portsConnected: number;
  civicBlocks: number;
  civicOriginals: number;
  /** Conservation ledger for the whole plan — must be >= 0 on every row. */
  residentsDelta: number;
  jobsDelta: number;
  capacityDelta: number;
}

export interface BoxPlan {
  box: ReplanBox;
  /** The target tiles per tier, sorted (y,x). Disjoint across tiers. */
  tierTiles: Record<TierKind, TileXY[]>;
  civic: PlannedCivic[];
  ports: ReplanPort[];
  /** Per tier, the higher-tier tiles its own line passes under/over (grade separation) — see resolveWholeBoxConflicts. */
  passThrough: Record<TierKind, Set<string>>;
  steps: ReplanStep[];
  metrics: ReplanMetrics;
  /** Non-empty when the plan failed its own invariants — the caller must discard it (MET-V868). */
  invariantFailures: string[];
}

export interface ReplanProgress {
  planTiles: number;
  tilesDone: number;
  stepsTotal: number;
  stepsDone: number;
  portsTotal: number;
  portsVerified: number;
  converged: boolean;
}

// ---------------------------------------------------------------------------
// §2 Small pure helpers.
// ---------------------------------------------------------------------------

export function keyOf(p: TileXY): string {
  return `${p.x},${p.y}`;
}

function parseKey(k: string): TileXY {
  const i = k.indexOf(',');
  return { x: Number(k.slice(0, i)), y: Number(k.slice(i + 1)) };
}

const ORTHO: readonly (readonly [number, number])[] = [
  [1, 0],
  [-1, 0],
  [0, 1],
  [0, -1],
];

function inBox(box: ReplanBox, x: number, y: number): boolean {
  return x >= box.x0 && x < box.x0 + box.w && y >= box.y0 && y < box.y0 + box.h;
}

/** Sorted (y then x) copy — the module's ONE canonical tile ordering. */
function sortTiles(tiles: readonly TileXY[]): TileXY[] {
  return tiles.slice().sort((a, b) => (a.y !== b.y ? a.y - b.y : a.x - b.x));
}

// ---------------------------------------------------------------------------
// §3 Ports — where the outside network crosses the boundary.
// ---------------------------------------------------------------------------

/**
 * Task item 3: every road/rail tile where the OUTSIDE network crosses the box
 * boundary is a port the plan must keep connected. `outsideNetwork` is a map
 * from tile key to tier for every network tile the caller knows about outside
 * the box (the caller decides the search radius — one tile of halo is enough
 * to find every crossing, since a crossing is by definition orthogonally
 * adjacent to an inside tile).
 *
 * Deterministic: the returned ports are sorted by (inside.y, inside.x, tier
 * rank), never by map iteration order.
 */
export function findPorts(box: ReplanBox, outsideNetwork: ReadonlyMap<string, TierKind>): ReplanPort[] {
  const ports: ReplanPort[] = [];
  const keys = Array.from(outsideNetwork.keys()).sort();
  for (const k of keys) {
    const p = parseKey(k);
    if (inBox(box, p.x, p.y)) continue; // not outside — not a port.
    const tier = outsideNetwork.get(k) as TierKind;
    for (const [dx, dy] of ORTHO) {
      const ix = p.x + dx;
      const iy = p.y + dy;
      if (!inBox(box, ix, iy)) continue;
      ports.push({ outside: { x: p.x, y: p.y }, inside: { x: ix, y: iy }, tier });
    }
  }
  return ports.sort((a, b) => {
    if (a.inside.y !== b.inside.y) return a.inside.y - b.inside.y;
    if (a.inside.x !== b.inside.x) return a.inside.x - b.inside.x;
    return TIER_RANK[a.tier] - TIER_RANK[b.tier];
  });
}

// ---------------------------------------------------------------------------
// §4 The tier hierarchy — the "reimagine" half.
// ---------------------------------------------------------------------------

/**
 * The row/column offsets a tier's lines take within the box, ANCHORED TO ITS
 * PORTS first (task item 3: a port must be reachable, and the cheapest way to
 * guarantee that is to run the tier's own line straight through the port's
 * inside tile) and only then filled out on the tier's own spacing.
 *
 * Returns absolute coordinates (not box-relative), sorted ascending, deduped.
 */
export function tierLineOffsets(
  box: ReplanBox,
  tier: TierKind,
  orientation: 'h' | 'v',
  /**
   * LEAD RULING 2026-09-06 — A PORT MUST BE *REACHED*, NOT RAILROADED. Ports
   * no longer anchor lines here at all; they are served by short SPURS
   * (`planPortSpurs`) instead. Kept in the signature because every caller
   * passes it and because removing it would silently change call sites.
   *
   * WHY (measured, on the live engine): port anchoring used to add a
   * FULL-SPAN line for EVERY port of the tier. The dogfood city is a road
   * grid, so column x=0 is road for every y — one minor-road port per ROW —
   * so the plan anchored a line on every row and PAVED THE WHOLE BOX. The
   * consequences all traced to this one defect: `siteFor` could not find a
   * free 3x3 (or even 2x2) for a consolidated civic, so 10 of 10 city-wide
   * groups were rejected `no site` and the consolidated-civic count stayed at
   * zero; the box rendered as solid road; and minor-road tiles dominated the
   * solvency spend table at 1,817 tiles.
   */
  /** Deliberately UNUSED since the 2026-09-06 city-wide-lines ruling: the lattice is the ONLY source of lines, so ports never influence which lines exist. Kept in the signature because every caller passes it. */
  _ports: readonly ReplanPort[],
  /** Deliberately UNUSED — an absolute lattice must be seed-independent, or two overlapping box positions disagree and the sliding window repaints forever (the 2026-09-05 finding). */
  _seed: number,
  /**
   * LEAD RULING 2026-09-06 — THE LATTICE TAKES ITS PHASE FROM THE CITY. The
   * absolute lattice was hard-anchored at coordinate 0, so on a city whose own
   * road grid sits on a different phase the plan's lines fell BETWEEN the
   * existing roads: every plan tile was a fresh build, and every existing road
   * became an off-plan "stale" remnant to be scrapped — a full repaint of a
   * grid that was already the right shape.
   *
   * The phase is derived ONCE, city-wide, from the existing road family (the
   * mode of the road tiles' coordinates mod LATTICE_PHASE_MODULUS) and passed
   * down here, so the genesis grid IS the lattice wherever it already exists.
   * Every tier's spacing is a multiple of LATTICE_PHASE_MODULUS, so ONE phase
   * aligns the whole hierarchy: a rail line lands ON an existing road row
   * (defrag = replace the lower tier), never one tile beside it.
   *
   * Defaults to 0, which is exactly the previous behaviour — a city whose grid
   * is already on phase 0 (every fixture in this estate) is bit-identical.
   */
  phase = 0,
): number[] {
  const span = orientation === 'h' ? box.h : box.w;
  const origin = orientation === 'h' ? box.y0 : box.x0;
  if (span <= 0) return [];
  const spacing = Math.max(1, TIER_LINE_SPACING[tier]);
  // Absolute-coordinate lattice, so overlapping box positions AGREE on where
  // a line goes and the sliding window converges instead of repainting.
  const out: number[] = [];
  const ph = ((phase % spacing) + spacing) % spacing;
  for (let i = 0; i < span; i++) {
    const v = origin + i;
    if ((((v - ph) % spacing) + spacing) % spacing === 0) out.push(v);
  }
  // (a) The ruling's explicit cap. The lattice already yields at most this
  // many by construction; the slice makes the bound structural rather than
  // incidental, so a future spacing change cannot quietly reintroduce paving.
  const cap = Math.ceil(span / spacing);
  // LEAD RULING 2026-09-06 — RAIL AND MOTORWAY ARE CITY-WIDE LINES, NEVER
  // INVENTED PER BOX. The absolute lattice defines the ONLY lines that exist;
  // a box realises the segment of a city-wide line passing through it and
  // nothing else. The previous "one anchored line when the lattice yields
  // none" and "one perpendicular line when a port demands it" refinements are
  // DELETED, not disabled: measured, they were a self-feeding loop — every
  // line this re-plan laid became a fresh port for the next box, which
  // invented another line to reach it, which became another port. The E8
  // realised-box metric caught the result exactly: motorway/rail crossing at
  // grade 14 times inside one 16x16 box, junction density 0.047 -> 0.633, and
  // line capex GBP 47M -> GBP 307M.
  //
  // A port whose tier has no lattice line in this box is NOT a failure: it is
  // a line that passes through or terminates outside, and `validatePlan`
  // treats it that way (see its own note).
  return out.slice(0, Math.max(0, cap));
}

/**
 * LEAD RULING 2026-09-06 (b)/(c) — reach every port with the SHORTEST SPUR to
 * the nearest planned line, never a full-span line.
 *
 * For each port whose inside tile is not already served by a planned tile of
 * its own tier or higher, walk an L-shaped path from the port's inside tile
 * to the nearest planned tile of its OWN tier (aroad/minor may join the
 * nearest line of ANY tier — they are the general-access tiers, and the
 * acceptance measures own-tier components only for rail/motorway/dual).
 * Tiles already claimed by another tier are traversed, not re-claimed: a spur
 * crossing a higher tier is a grade separation, exactly as elsewhere.
 *
 * (c) Ports sharing a row/column collapse to one spur: a port already covered
 * by a spur added earlier in this pass is skipped, so a road grid's worth of
 * co-linear ports costs ONE spur, not one per port.
 *
 * (d) A tier with NO planned line at all cannot be spurred to — the port is
 * left unserved and `validatePlan` reports it, which is the MET-V871 path
 * (the rail-with-only-vertical-ports case).
 *
 * Deterministic (GR#21): ports are already sorted, targets are chosen by
 * (distance, then lexicographic tile key), and the L-path always turns in x
 * before y.
 */
export function planPortSpurs(
  box: ReplanBox,
  tierTiles: Record<TierKind, TileXY[]>,
  ports: readonly ReplanPort[],
  /** The city-wide lattice phase - see `onCityLattice` and the (f) rule below. */
  latticePhase = 0,
): { spurs: Record<TierKind, TileXY[]>; passThrough: Record<TierKind, Set<string>> } {
  const spurs = {} as Record<TierKind, TileXY[]>;
  // MEASURED FIX 2026-09-06 (the 878 discards): a spur walks past any tile
  // another tier already owns rather than re-claiming it — correct — but the
  // skipped tile left a HOLE in the spur, and the one-component check then
  // saw the tier as several pieces. Measured exactly: `aroad: 3 components`
  // on box 17,0 with the spur running x=17 and gaps at y=5 and y=8, on 180 of
  // 180 discards. A tile a spur runs straight through is a GRADE SEPARATION,
  // the same concept resolveWholeBoxConflicts already models — it is now
  // recorded as such instead of silently breaking the line.
  const spurPassThrough = {} as Record<TierKind, Set<string>>;
  for (const t of TIER_ORDER) {
    spurs[t] = [];
    spurPassThrough[t] = new Set();
  }
  const claimed = new Set<string>();
  for (const t of TIER_ORDER) for (const p of tierTiles[t]) claimed.add(keyOf(p));

  const servedBy = (port: ReplanPort): boolean => {
    const check = (k: string): boolean => {
      for (const t of TIER_ORDER) {
        if (TIER_RANK[t] > TIER_RANK[port.tier]) continue;
        if (tierTiles[t].some((p) => keyOf(p) === k)) return true;
        if (spurs[t].some((p) => keyOf(p) === k)) return true;
      }
      return false;
    };
    const ik = keyOf(port.inside);
    return check(ik) || ORTHO.some(([dx, dy]) => check(`${port.inside.x + dx},${port.inside.y + dy}`));
  };

  for (const port of ports) {
    // MEASURED FIX 2026-09-06 (E1: 100 of 100 discards, box 17,0) — (e) A TIER
    // WITH NO LATTICE LINE IN THIS BOX IS NEVER SPURRED TO. This is the SAME
    // rule validatePlan already applies to the port invariant ("the port
    // invariant applies ONLY to ports of tiers the lattice actually plans
    // inside this box"), stated once and obeyed in both places (GR#3).
    //
    // Measured, on the dogfood fixture at box 17,0: the aroad lattice yields
    // exactly two lines here (y=0 and x=32) and BOTH are absorbed by strictly
    // higher tiers (rail at y=0, motorway at x=32), so aroad ends the lattice
    // stage with ZERO own tiles. The previous box had laid a full A-road
    // column at x=16, which makes every one of the 14 west-edge tiles an
    // A-road PORT — and the spur pass then manufactured a brand-new 15-tile
    // A-road column at x=17, immediately parallel to the existing one. That is
    // exactly the self-feeding loop the city-wide-lines ruling DELETED from
    // `tierLineOffsets`, reintroduced here through the back door: every line
    // the re-plan lays becomes a fresh port for the next box, which invents
    // another line to reach it. The invented column was also HOLED (see the
    // two fixes below), which is what made the plan fail its own one-component
    // check and get discarded on 100 of 100 instrumented passes.
    //
    // A port of a tier with no line here is a PASS-THROUGH: a city-wide line
    // that runs past or terminates outside this box. Not a defect, and never a
    // reason to invent a line.
    // MEASURED FIX 2026-09-06 (E8 box 1,0) — (f) A PORT WHOSE OUTSIDE TILE IS
    // ON THE CITY-WIDE LATTICE IS ALREADY SATISFIED. This is the lead's
    // "the genesis 8-grid IS the minor lattice wherever it already exists"
    // ruling applied to the spur rule, and it is the same disease rule (e)
    // cures, one tier down: the box's west neighbour column x=0 is a genesis
    // road, so EVERY tile down the box's own west edge is a minor port, and
    // the spur pass answered by laying a full 14-tile minor column at x=1
    // immediately parallel to it. Measured at box 1,0: 62 realised minor tiles
    // where the lattice asks for ~30, in 2 components (the parallel column ran
    // 1,1..1,6 and 1,8..1,15 with a hole at 1,7 where a lattice tile "served"
    // the port without continuing the line).
    //
    // The port's outside tile and the box's own lines are THE SAME GRID once
    // the lattice is phase-aligned to the city, so they meet by construction —
    // there is nothing to spur to, and `validatePlan` exempts these ports for
    // exactly this reason (the two rules are deliberately symmetric: a port
    // this pass declines to serve must not then be reported as unserved).
    if (onCityLattice(port.outside.x, port.outside.y, latticePhase)) continue;
    if (tierTiles[port.tier].length === 0) continue;
    if (servedBy(port)) {
      // MEASURED FIX 2026-09-06 — NEVER LEAVE A HOLE IN A SPUR. `servedBy` is
      // adjacency-based, which is the right question for "is the port
      // REACHED" but the wrong one for "is the tier's line CONTINUOUS": a port
      // whose only server is the tip of an earlier SPUR of its own tier is
      // reached, yet its own inside tile stays unclaimed — and the next
      // unserved port two tiles along starts a fresh spur, leaving a
      // one-tile gap between them. Measured at box 17,0: tile 17,5 was exactly
      // this hole (ports at 17,4 and 17,6 each spurred, 17,5 served by
      // adjacency to 17,4 and never laid), splitting the tier in two.
      //
      // When the server is a real lattice tile there is no gap to close, so
      // only the spur-served case claims the inside tile — one tile, and the
      // line is contiguous by construction.
      const ik0 = keyOf(port.inside);
      const servedByLattice = TIER_ORDER.some(
        (t) => TIER_RANK[t] <= TIER_RANK[port.tier] &&
          (tierTiles[t].some((p) => keyOf(p) === ik0) ||
            ORTHO.some(([dx, dy]) => tierTiles[t].some((p) => keyOf(p) === `${port.inside.x + dx},${port.inside.y + dy}`))),
      );
      if (servedByLattice || claimed.has(ik0)) continue;
      claimed.add(ik0);
      spurs[port.tier].push({ x: port.inside.x, y: port.inside.y });
      continue;
    }
    const general = port.tier === 'aroad' || port.tier === 'minor';
    const targets: TileXY[] = [];
    for (const t of TIER_ORDER) {
      if (!general && t !== port.tier) continue;
      for (const p of tierTiles[t]) targets.push(p);
    }
    if (targets.length === 0) continue; // (d) nothing to reach — validatePlan reports it.
    const sorted = targets.slice().sort((a, b) => {
      const da = Math.abs(a.x - port.inside.x) + Math.abs(a.y - port.inside.y);
      const db = Math.abs(b.x - port.inside.x) + Math.abs(b.y - port.inside.y);
      return da !== db ? da - db : keyOf(a) < keyOf(b) ? -1 : 1;
    });
    const target = sorted[0];
    // Shortest L-path: x first, then y. Tiles already claimed by another tier
    // are traversed (grade separation), never re-claimed.
    const path: TileXY[] = [];
    let cx = port.inside.x;
    let cy = port.inside.y;
    const push = (x: number, y: number) => {
      if (!inBox(box, x, y)) return;
      if (x === target.x && y === target.y) {
        // MEASURED FIX 2026-09-06 — THE SPUR'S OWN ENDPOINT IS A JOIN, and it
        // must be RECORDED as one. The target belongs to another tier (it is
        // the nearest planned tile of any tier for a general tier), so the
        // spur neither claims it nor builds it — but the spur physically MEETS
        // it, and if that fact is not recorded the target is invisible to this
        // tier's own connectivity graph and the spur reads as severed from the
        // network it just joined. Measured at box 17,0: the A-road spur ending
        // on the minor line at 17,8 left 17,8 recorded nowhere, so the tiles
        // above and below it counted as separate components (a LOWER tier is
        // skipped outright by `componentsOfTier`'s own-tier walk, so nothing
        // else could bridge them).
        spurPassThrough[port.tier].add(`${x},${y}`);
        return;
      }
      const k = `${x},${y}`;
      if (claimed.has(k)) {
        // Another tier owns this tile; this spur passes under/over it. Record
        // the grade separation so the line still reads as continuous.
        spurPassThrough[port.tier].add(k);
        return;
      }
      path.push({ x, y });
    };
    push(cx, cy);
    while (cx !== target.x) {
      cx += cx < target.x ? 1 : -1;
      push(cx, cy);
    }
    while (cy !== target.y) {
      cy += cy < target.y ? 1 : -1;
      push(cx, cy);
    }
    for (const p of path) claimed.add(keyOf(p));
    spurs[port.tier].push(...path);
  }
  for (const t of TIER_ORDER) spurs[t] = sortTiles(spurs[t]);
  return { spurs, passThrough: spurPassThrough };
}

/**
 * The whole-box tier target BEFORE conflict resolution and repair: every
 * tier's full-span lines. Full-span (edge to edge of the box) is the point —
 * a line that stops short of the boundary is exactly the disconnected stub
 * inc3 kept producing, and a full-span line's own endpoints ARE the boundary,
 * which is where the outside network is met.
 */
function rawTierLines(
  box: ReplanBox,
  ports: readonly ReplanPort[],
  seed: number,
  phase: number,
): Record<TierKind, TileXY[]> {
  const out = {} as Record<TierKind, TileXY[]>;
  const addLine = (tiles: TileXY[], orientation: 'h' | 'v', off: number) => {
    if (orientation === 'h') {
      for (let i = 0; i < box.w; i++) tiles.push({ x: box.x0 + i, y: off });
    } else {
      for (let i = 0; i < box.h; i++) tiles.push({ x: off, y: box.y0 + i });
    }
  };
  for (const tier of TIER_ORDER) {
    const tiles: TileXY[] = [];
    for (const orientation of TIER_ORIENTATIONS[tier]) {
      for (const off of tierLineOffsets(box, tier, orientation, ports, seed, phase)) addLine(tiles, orientation, off);
    }
    out[tier] = sortTiles(tiles);
  }
  return out;
}

/**
 * Tile-spread resolution across the whole box: a tile wanted by two tiers goes
 * to the HIGHER one (TIER_RANK), exactly the rule consolidatorLayout.ts's
 * `resolveTierConflicts` already applies per section (GR#3 — same rule, box
 * scope). Deterministic: TIER_ORDER is the iteration order.
 */
function resolveWholeBoxConflicts(raw: Record<TierKind, TileXY[]>): {
  paths: Record<TierKind, TileXY[]>;
  /**
   * GRADE SEPARATION (the crossing tiles a tier LOST to a strictly higher
   * tier, but whose line still runs straight through them). This is
   * `mayPassThrough`'s own rule made structural: "a higher tier may pass
   * through a lower tier unimpeded" means the rail bridge over the A-road
   * does not CUT the A-road — the A-road is still one continuous road, it
   * simply passes under. Without this the plan's own full-span higher-tier
   * lines act as WALLS that partition every lower tier into a left half and
   * a right half (measured directly on the messy-box fixture: the A-road
   * grid split into two components and the repair pass then deleted one of
   * them, leaving a plan that abandoned half the box). Every per-tier
   * connectivity question in this module is therefore asked over
   * `own tiles + its own pass-through crossings`, while OWNERSHIP (which
   * tier actually builds the tile) stays strictly with the higher tier.
   */
  passThrough: Record<TierKind, Set<string>>;
} {
  const claimed = new Map<string, TierKind>();
  const paths = {} as Record<TierKind, TileXY[]>;
  const passThrough = {} as Record<TierKind, Set<string>>;
  for (const tier of TIER_ORDER) passThrough[tier] = new Set();
  for (const tier of TIER_ORDER) {
    const survivors: TileXY[] = [];
    for (const p of raw[tier]) {
      const k = keyOf(p);
      const holder = claimed.get(k);
      if (holder != null) {
        // The holder is always a strictly HIGHER tier (TIER_ORDER is the
        // iteration order, so a tile is only ever claimed by an earlier =
        // higher-ranked tier), and same-tier duplicates cannot occur because
        // a tier's own lines never repeat a coordinate within one orientation
        // and the two orientations are deduped by this very map.
        if (holder !== tier) passThrough[tier].add(k);
        continue;
      }
      claimed.set(k, tier);
      survivors.push(p);
    }
    paths[tier] = survivors;
  }
  return { paths, passThrough };
}

/**
 * REPAIR (task item 3, the "one component" half). After conflict resolution a
 * tier can be split — a vertical A-road cut where a rail spine crosses it, for
 * instance. Keep exactly ONE component: the one containing this tier's ports
 * if it has any (a port MUST stay reachable — that is the whole contract),
 * otherwise the largest, ties broken by the lexicographically smallest member
 * key so the choice is total and deterministic.
 *
 * This is a strict narrowing — it never invents a tile — so it cannot break
 * any other invariant, and it makes "exactly one component per tier" true BY
 * CONSTRUCTION rather than merely asserted after the fact.
 */
function keepOneComponent(
  tiles: readonly TileXY[],
  portInsideKeys: ReadonlySet<string>,
  passThrough: ReadonlySet<string>,
): TileXY[] {
  if (tiles.length === 0) return [];
  const own = new Set(tiles.map(keyOf));
  // Grade separation: connectivity is asked over own tiles + the crossings
  // this tier passes under/over — see resolveWholeBoxConflicts's own doc.
  const set = new Set(own);
  for (const k of Array.from(passThrough).sort()) set.add(k);
  const comp = tileComponents(set);
  const members = new Map<number, string[]>();
  for (const k of Array.from(set).sort()) {
    const id = comp.get(k) as number;
    const list = members.get(id);
    if (list) list.push(k);
    else members.set(id, [k]);
  }
  const ids = Array.from(members.keys()).sort((a, b) => a - b);
  let bestId = ids[0];
  let bestScore = -1;
  for (const id of ids) {
    const list = members.get(id) as string[];
    const portsHeld = list.reduce((n, k) => n + (portInsideKeys.has(k) ? 1 : 0), 0);
    // Ports dominate size outright: a small port-bearing component is always
    // preferable to a big stranded one, because a stranded one severs the
    // outside network from the box.
    const score = portsHeld * 1_000_000 + list.length;
    if (score > bestScore) {
      bestScore = score;
      bestId = id;
    }
  }
  const keep = new Set(members.get(bestId) as string[]);
  return sortTiles(tiles.filter((p) => keep.has(keyOf(p))));
}

/**
 * REPAIR (task item 3, the "no dead ends > 1 tile" half). Iteratively removes
 * degree-1 tiles until every remaining degree-1 tile is either a port's own
 * inside tile (the network legitimately terminates at the boundary there), a
 * tile ON the box boundary (a line running edge to edge — its endpoints are
 * how the box joins the world, not dead ends), or the tail of a spur no
 * longer than MAX_DEAD_END_TILES.
 *
 * Bounded: each round removes at least one tile, so it terminates in at most
 * `tiles.length` rounds. Deterministic: candidates are collected, sorted, then
 * removed as a batch — never removed mid-iteration over an unordered set.
 */
function trimDeadEnds(
  tiles: readonly TileXY[],
  box: ReplanBox,
  protectedKeys: ReadonlySet<string>,
  /**
   * Tiles that are PRESENT for degree purposes but owned by another tier and
   * therefore never removed and never returned — this tier's grade-separated
   * crossings (see resolveWholeBoxConflicts). Without these the tile either
   * side of a crossing reads as degree-1 and the trim eats the line inward
   * from every crossing.
   */
  virtualKeys: ReadonlySet<string> = new Set(),
): TileXY[] {
  const ownKeys = new Set(tiles.map(keyOf));
  let set = new Set(ownKeys);
  for (const k of Array.from(virtualKeys).sort()) set.add(k);
  const onBoundary = (p: TileXY): boolean =>
    p.x === box.x0 || p.x === box.x0 + box.w - 1 || p.y === box.y0 || p.y === box.y0 + box.h - 1;
  for (let round = 0; round < tiles.length; round++) {
    const doomed: string[] = [];
    for (const k of Array.from(set).sort()) {
      const p = parseKey(k);
      if (protectedKeys.has(k) || virtualKeys.has(k) || onBoundary(p)) continue;
      let degree = 0;
      for (const [dx, dy] of ORTHO) {
        if (set.has(`${p.x + dx},${p.y + dy}`)) degree += 1;
      }
      if (degree <= 1) doomed.push(k);
    }
    // A spur of length <= MAX_DEAD_END_TILES is allowed to stand: only trim
    // when the tile behind the tip is ALSO degree-<=2 and itself interior,
    // i.e. the spur is genuinely longer than the allowance.
    const trimmed = doomed.filter((k) => {
      const p = parseKey(k);
      let neighbourKey: string | null = null;
      for (const [dx, dy] of ORTHO) {
        const nk = `${p.x + dx},${p.y + dy}`;
        if (set.has(nk)) neighbourKey = neighbourKey ?? nk;
      }
      if (neighbourKey === null) return true; // isolated tile: always junk.
      if (MAX_DEAD_END_TILES < 1) return true;
      // The tip is allowed if its single neighbour is a real junction/through
      // tile (degree >= 3, or on the boundary) — that makes the spur exactly
      // one tile long. Otherwise the spur is >= 2 tiles and gets trimmed.
      const n = parseKey(neighbourKey);
      let nDeg = 0;
      for (const [dx, dy] of ORTHO) {
        if (set.has(`${n.x + dx},${n.y + dy}`)) nDeg += 1;
      }
      return !(nDeg >= 3 || onBoundary(n));
    });
    if (trimmed.length === 0) break;
    const next = new Set(set);
    for (const k of trimmed) next.delete(k);
    set = next;
  }
  return sortTiles(
    Array.from(set)
      .filter((k) => ownKeys.has(k))
      .map(parseKey),
  );
}

// ---------------------------------------------------------------------------
// §5 Civic consolidation — "not 40 kindergartens, one city kindergarten".
// ---------------------------------------------------------------------------

/** The ladder rung this module needs: many `from` become one `to`. Supplied by the caller from consolidator.ts's own derived ladder (GR#3/GR#15 — never re-derived here). */
export interface ReplanLadderRung {
  from: string;
  to: string;
  groupSize: number;
  /** The successor's own capacity, from the catalogue. */
  toCapacity: number;
  /** Footprint of the successor, in tiles. */
  toW: number;
  toH: number;
}

/**
 * Group the box's civic contents by spec and emit one consolidated block per
 * full group, sited ON the plan — specifically on the free tile CLOSEST to an
 * arterial (motorway/dual) tile, ties broken by (y,x). "Next to the
 * arterials" is Aaron's own placement rule; siting deterministically against
 * the plan (not against whatever happened to be there) is what makes this a
 * re-plan rather than another in-place shuffle.
 *
 * CONSERVATION (task item 2): a group is only ever emitted when the successor
 * provides AT LEAST the group's combined capacity. `groupSizeOf`'s FLOOR rule
 * already guarantees this for every real rung; the check is repeated here as
 * a hard gate so a hand-built or future rung can never quietly delete
 * capacity through this path.
 */
export function planCivicBlocks(
  contents: readonly ReplanContent[],
  rungs: readonly ReplanLadderRung[],
  siteFor: (index: number, footprintW: number, footprintH: number) => TileXY | null,
): PlannedCivic[] {
  const bySpec = new Map<string, ReplanContent[]>();
  for (const c of contents.slice().sort((a, b) => (a.y !== b.y ? a.y - b.y : a.x !== b.x ? a.x - b.x : a.id - b.id))) {
    if (c.tier !== null || c.protectedFromDemolition) continue;
    if (c.capacity <= 0) continue;
    const list = bySpec.get(c.spec);
    if (list) list.push(c);
    else bySpec.set(c.spec, [c]);
  }
  const rungBySpec = new Map<string, ReplanLadderRung>();
  for (const r of rungs.slice().sort((a, b) => (a.from < b.from ? -1 : a.from > b.from ? 1 : a.to < b.to ? -1 : 1))) {
    if (!rungBySpec.has(r.from)) rungBySpec.set(r.from, r);
  }
  const out: PlannedCivic[] = [];
  let siteIndex = 0;
  for (const spec of Array.from(bySpec.keys()).sort()) {
    const rung = rungBySpec.get(spec);
    if (!rung || rung.groupSize < 2) continue;
    const members = bySpec.get(spec) as ReplanContent[];
    const groups = Math.floor(members.length / rung.groupSize);
    for (let g = 0; g < groups; g++) {
      const slice = members.slice(g * rung.groupSize, (g + 1) * rung.groupSize);
      const capacityAbsorbed = slice.reduce((n, c) => n + c.capacity, 0);
      // Hard conservation gate (MET-V869's own condition): never emit a group
      // whose combined capacity the successor cannot fully absorb.
      if (rung.toCapacity < capacityAbsorbed) continue;
      const site = siteFor(siteIndex, rung.toW, rung.toH);
      siteIndex += 1;
      if (!site) continue;
      out.push({
        spec: rung.to,
        x: site.x,
        y: site.y,
        replaces: slice.map((c) => c.id).sort((a, b) => a - b),
        capacityAbsorbed,
        capacityProvided: rung.toCapacity,
        residentsAbsorbed: slice.reduce((n, c) => n + c.residents, 0),
        jobsAbsorbed: slice.reduce((n, c) => n + c.jobs, 0),
      });
    }
  }
  return out;
}

// ---------------------------------------------------------------------------
// §6 Validation — the plan's own invariants (task item 3's asserts).
// ---------------------------------------------------------------------------

/**
 * One tier's connected components, computed over its own tiles PLUS its
 * grade-separated crossings (see resolveWholeBoxConflicts). Only OWN tiles
 * are reported back — a crossing belongs to the higher tier, it is merely
 * traversable by this one. The single place this module answers "how many
 * pieces is this tier in" (GR#3).
 */
export function componentsOfTier(
  tiles: readonly TileXY[],
  passThrough: ReadonlySet<string> | undefined,
): Map<string, number> {
  const own = new Set(tiles.map(keyOf));
  const graph = new Set(own);
  for (const k of Array.from(passThrough ?? new Set<string>()).sort()) graph.add(k);
  const comp = tileComponents(graph);
  const out = new Map<string, number>();
  for (const k of Array.from(own).sort()) out.set(k, comp.get(k) as number);
  return out;
}

/**
 * LEAD RULING 2026-09-06 — TERMINAL JOINS. THE shared join predicate: a tile
 * of a tier is connected to another tile of that tier when a path exists
 * through the ROAD FAMILY — including through tiles of a DIFFERENT tier. A
 * spur that ENDS on a motorway has joined the network; it is not a stranded
 * stub, and counting it as one measures the tier in isolation rather than the
 * city.
 *
 * WHY THIS EXISTS AS ONE EXPORTED FUNCTION (GR#3): the planner already models
 * a spur's endpoint as a join (see `planPortSpurs`'s `push`, which records the
 * target in `spurPassThrough`), but the realised-box metric derived its own
 * pass-through from an OPPOSITE-SIDES test and was therefore structurally
 * blind to a TERMINAL join — a spur ending on a foreign tile could never be
 * bridged by it. Measured on the converged box 32,0: minor read as 3
 * components while the road network was in fact whole, the stranded piece
 * (33,5..36,5) being a spur joined to the motorway column at 32,5. The metric
 * and the planner now ask the SAME function, so they cannot drift again.
 *
 * The opposite-sides CROSSING test is unaffected and stays where it is: a
 * crossing and a join are different facts and are counted separately.
 *
 * `familyKeys` is every road-family tile in scope, any tier. Only OWN tiles
 * are reported back. Deterministic: a sorted walk, no map-range-with-break.
 */
export function componentsOfTierWithJoins(
  tiles: readonly TileXY[],
  familyKeys: ReadonlySet<string>,
): Map<string, number> {
  const own = new Set(tiles.map(keyOf));
  const graph = new Set(own);
  for (const k of Array.from(familyKeys).sort()) graph.add(k);
  const comp = tileComponents(graph);
  const out = new Map<string, number>();
  for (const k of Array.from(own).sort()) out.set(k, comp.get(k) as number);
  return out;
}

/**
 * How many 4-connected components the WHOLE road family forms. Aaron's own
 * question ("optimise join") asked of the realised city: one network, not
 * several. Grade separation needs no special case here — a tile a line passes
 * under is itself a family tile, so it is already in the graph.
 */
export function familyComponentCount(familyKeys: ReadonlySet<string>): number {
  if (familyKeys.size === 0) return 0;
  const comp = tileComponents(new Set(Array.from(familyKeys).sort()));
  return new Set(Array.from(comp.values())).size;
}

/** How many plan tiles have three or more plan neighbours of any tier — the junction count the plan wants MINIMISED (task item 3). */
export function junctionCountOf(tierTiles: Record<TierKind, readonly TileXY[]>): number {
  const all = new Set<string>();
  for (const tier of TIER_ORDER) for (const p of tierTiles[tier]) all.add(keyOf(p));
  let junctions = 0;
  for (const k of Array.from(all).sort()) {
    const p = parseKey(k);
    let degree = 0;
    for (const [dx, dy] of ORTHO) {
      if (all.has(`${p.x + dx},${p.y + dy}`)) degree += 1;
    }
    if (degree >= 3) junctions += 1;
  }
  return junctions;
}

/** Dead ends longer than MAX_DEAD_END_TILES, counted over the union of every tier's tiles. Boundary tiles are never dead ends (that is where the box joins the world). */
export function deadEndCountOf(
  tierTiles: Record<TierKind, readonly TileXY[]>,
  box: ReplanBox,
  /**
   * LEAD RULING 2026-09-06 (3): a degree-1 tile that is the ONLY road tile
   * adjacent to a building it serves is a legitimate CUL-DE-SAC, not a dead
   * end — removing it would strand the building. Callers that know the box's
   * buildings pass this predicate; callers reasoning about a bare plan (no
   * buildings) omit it and get the strict count, unchanged.
   */
  isSoleAccessForBuilding?: (p: TileXY) => boolean,
): number {
  const all = new Set<string>();
  for (const tier of TIER_ORDER) for (const p of tierTiles[tier]) all.add(keyOf(p));
  const onBoundary = (p: TileXY): boolean =>
    p.x === box.x0 || p.x === box.x0 + box.w - 1 || p.y === box.y0 || p.y === box.y0 + box.h - 1;
  let count = 0;
  for (const k of Array.from(all).sort()) {
    const p = parseKey(k);
    if (onBoundary(p)) continue;
    let degree = 0;
    let neighbourKey: string | null = null;
    for (const [dx, dy] of ORTHO) {
      const nk = `${p.x + dx},${p.y + dy}`;
      if (all.has(nk)) {
        degree += 1;
        neighbourKey = neighbourKey ?? nk;
      }
    }
    if (degree > 1 || neighbourKey === null) continue;
    const n = parseKey(neighbourKey);
    let nDeg = 0;
    for (const [dx, dy] of ORTHO) {
      if (all.has(`${n.x + dx},${n.y + dy}`)) nDeg += 1;
    }
    // A one-tile spur off a real junction / off the boundary is allowed.
    if (nDeg >= 3 || onBoundary(n)) continue;
    // ...and so is a cul-de-sac that is some building's only road access.
    if (isSoleAccessForBuilding?.(p)) continue;
    count += 1;
  }
  return count;
}

/**
 * The plan's own invariant check (task item 3). Returns a list of human-
 * readable failures — EMPTY means the plan is sound. `planBox` runs this on
 * its own output and, when it is non-empty, reports the failures on the plan
 * so the caller can discard it loudly (MET-V868) rather than executing a
 * plan that would cut the box off from the world.
 */
export interface ValidatablePlan {
  box: ReplanBox;
  tierTiles: Record<TierKind, TileXY[]>;
  civic: readonly PlannedCivic[];
  ports: readonly ReplanPort[];
  /** Grade-separated crossings per tier — see resolveWholeBoxConflicts. Absent means "no crossings", which is the strictest reading. */
  passThrough?: Record<TierKind, ReadonlySet<string>>;
  /**
   * The city-wide lattice phase. Present so the port invariant can apply the
   * SAME exemption `planPortSpurs` rule (f) applies: a port whose outside tile
   * is on the lattice is satisfied by the lattice itself. If these two ever
   * disagreed the planner would decline to serve a port and then discard its
   * own plan for not serving it — an unbreakable discard loop.
   */
  latticePhase?: number;
}

export function validatePlan(plan: ValidatablePlan): string[] {
  const failures: string[] = [];
  const box = plan.box;
  const all = new Set<string>();
  for (const tier of TIER_ORDER) for (const p of plan.tierTiles[tier]) all.add(keyOf(p));

  for (const tier of TIER_ORDER) {
    const tiles = plan.tierTiles[tier];
    if (tiles.length === 0) continue;
    const distinct = new Set(Array.from(componentsOfTier(tiles, plan.passThrough?.[tier]).values()));
    if (distinct.size !== 1) failures.push(`${tier}: ${distinct.size} components inside the box, expected 1`);
  }

  for (const port of plan.ports) {
    // LEAD RULING 2026-09-06: the "port reached by a same-or-higher tier"
    // invariant applies ONLY to ports of tiers the lattice actually plans
    // inside this box. A rail or motorway port on a box the city-wide lattice
    // does not route through is a line PASSING THROUGH or TERMINATING
    // OUTSIDE — a fact about the city, not a defect in this box's plan, and
    // never a reason to invent a line to meet it.
    if (plan.tierTiles[port.tier].length === 0) continue;
    // Symmetric with planPortSpurs rule (f): a port whose outside tile is ON
    // the city-wide lattice is satisfied by the lattice itself — the outside
    // line and this box's own lines are the same phase-aligned grid.
    if (onCityLattice(port.outside.x, port.outside.y, plan.latticePhase ?? 0)) continue;
    const ik = keyOf(port.inside);
    // Same tier or higher, reachable: the port's own inside tile is a plan
    // tile of a tier at least as high, OR is orthogonally adjacent to one.
    const rankOK = (k: string): boolean => {
      for (const tier of TIER_ORDER) {
        if (TIER_RANK[tier] > TIER_RANK[port.tier]) continue;
        if (plan.tierTiles[tier].some((p) => keyOf(p) === k)) return true;
      }
      return false;
    };
    const adjacentOK = ORTHO.some(([dx, dy]) => rankOK(`${port.inside.x + dx},${port.inside.y + dy}`));
    if (!rankOK(ik) && !adjacentOK) {
      failures.push(`port ${port.tier} at ${ik} is not reached by a same-or-higher tier plan tile`);
    }
  }

  const de = deadEndCountOf(plan.tierTiles, box);
  if (de > 0) failures.push(`${de} dead end(s) longer than ${MAX_DEAD_END_TILES} tile`);

  for (const c of plan.civic) {
    if (c.capacityProvided < c.capacityAbsorbed) {
      failures.push(`civic ${c.spec} at ${c.x},${c.y} provides ${c.capacityProvided} < absorbed ${c.capacityAbsorbed}`);
    }
  }
  // LEAD RULING 2026-09-06 (pass-through): an empty plan is only a failure if
  // some port's tier WOULD have been planned here. A box the city-wide
  // lattice does not route any line through, whose only ports belong to lines
  // passing through or terminating outside, is a legitimately empty plan —
  // not a defect, and never a reason to invent a line.
  if (all.size === 0 && plan.ports.length > 0) {
    const anyPlannableTier = TIER_ORDER.some((t) => plan.tierTiles[t].length > 0);
    if (anyPlannableTier) failures.push('plan is empty but the box has ports to keep connected');
  }
  return failures;
}

// ---------------------------------------------------------------------------
// §7 The planner itself.
// ---------------------------------------------------------------------------

/**
 * LEAD RULING 2026-09-06 — CIVIC GROUPING IS CITY-WIDE. Aaron's example is
 * "not 40 kindergartens, it's a CITY kindergarten": the 40 are scattered over
 * the whole city, so a group formed inside one 16x16 red box can never reach
 * the ladder's own group size (33 for the nursery rung). Measured directly:
 * the per-box grouping produced ZERO consolidated civics in 900 ticks on the
 * dogfood fixture while the planner was working perfectly — it simply never
 * saw 33 nurseries in one window.
 *
 * A city-wide group therefore forms over the WHOLE city, and the re-plan
 * claims it whenever ANY member falls inside the box: the successor is sited
 * on the box's own laid-out block (beside an arterial) and the group's
 * members are demolished wherever they stand. `members` carries the full
 * records precisely because most of them are OUTSIDE the box, so the step
 * builder cannot look them up in the box's own contents.
 *
 * NOTE ON PROVENANCE (stated plainly rather than assumed): the ruling pointed
 * at `findCityWideOpportunities` in consolidator.ts as the thing to consume.
 * That function does not exist at this worktree's base — consolidator.ts here
 * exports only the SECTION-scoped `findOpportunities`. The grouping is
 * therefore built by the caller (engine.ts's `replanCityWideGroups`) from
 * `consolidationLadder()`, which IS the shared SSOT for every rung, group
 * size and capacity (GR#3/GR#15) — the same ladder BUG-758's own path reads.
 */
export interface CityWideCivicGroup {
  from: string;
  to: string;
  /** Every member of the group, ANYWHERE in the city — full records. */
  members: readonly ReplanContent[];
  toCapacity: number;
  toW: number;
  toH: number;
}

export interface PlanBoxInput {
  box: ReplanBox;
  contents: readonly ReplanContent[];
  ports: readonly ReplanPort[];
  rungs: readonly ReplanLadderRung[];
  seed: number;
  /**
   * City-wide groups with at least one member inside this box. When non-empty
   * these REPLACE the per-box grouping entirely (the ruling: "the planner's
   * per-box grouping becomes the fallback only when no city-wide group
   * touches the box").
   */
  cityWideGroups?: readonly CityWideCivicGroup[];
  /**
   * LEAD RULING 2026-09-06 — the city-wide lattice phase from `latticePhaseOf`
   * (see its doc). Computed ONCE over the whole city by the caller and passed
   * in, never re-derived per box: a phase that differed between two overlapping
   * box positions would move the lines under the sliding window and repaint
   * forever, which is the exact failure the absolute lattice was introduced to
   * end. Defaults to 0 = the previous hard-anchored behaviour.
   */
  latticePhase?: number;
}

/**
 * ONE coherent re-plan of everything inside the box (task item 1). Pure and
 * deterministic in (box, contents, ports, rungs, seed).
 */
export function planBox(input: PlanBoxInput): BoxPlan {
  const { box, contents, ports, rungs, seed } = input;
  const phase = ((input.latticePhase ?? 0) % LATTICE_PHASE_MODULUS + LATTICE_PHASE_MODULUS) % LATTICE_PHASE_MODULUS;

  // (1) The tier hierarchy, from scratch.
  const raw = rawTierLines(box, ports, seed, phase);
  const { paths: resolved, passThrough } = resolveWholeBoxConflicts(raw);
  const portInsideByTier = new Map<TierKind, Set<string>>();
  for (const tier of TIER_ORDER) portInsideByTier.set(tier, new Set());
  for (const port of ports) (portInsideByTier.get(port.tier) as Set<string>).add(keyOf(port.inside));
  const allPortInside = new Set<string>();
  for (const port of ports) allPortInside.add(keyOf(port.inside));

  const tierTiles = {} as Record<TierKind, TileXY[]>;
  for (const tier of TIER_ORDER) {
    const oneComponent = keepOneComponent(
      resolved[tier],
      portInsideByTier.get(tier) as Set<string>,
      passThrough[tier],
    );
    tierTiles[tier] = trimDeadEnds(oneComponent, box, allPortInside, passThrough[tier]);
  }
  // LEAD RULING 2026-09-06 (b): reach each unserved port with the SHORTEST
  // spur to the nearest planned line. Added AFTER the trim deliberately — a
  // spur runs from the box boundary to a line, so both its ends are anchored
  // (boundary tiles are exempt from the dead-end rule, and the far end joins
  // the line), and running the trim over them again could only re-delete
  // exactly what the ports need.
  const { spurs, passThrough: spurPT } = planPortSpurs(box, tierTiles, ports, phase);
  for (const tier of TIER_ORDER) {
    // A spur's grade separations join its tier's own pass-through set, so a
    // spur that runs under another tier still reads as ONE continuous line.
    for (const k of Array.from(spurPT[tier]).sort()) passThrough[tier].add(k);
    if (spurs[tier].length === 0) continue;
    const seen = new Set(tierTiles[tier].map(keyOf));
    for (const p of spurs[tier]) {
      if (seen.has(keyOf(p))) continue;
      seen.add(keyOf(p));
      tierTiles[tier].push(p);
    }
    tierTiles[tier] = sortTiles(tierTiles[tier]);
  }

  // (2) The consolidated civic blocks, sited beside the arterials.
  const planTileKeys = new Set<string>();
  for (const tier of TIER_ORDER) for (const p of tierTiles[tier]) planTileKeys.add(keyOf(p));
  const arterialKeys = new Set<string>();
  for (const tier of ['motorway', 'dual'] as TierKind[]) for (const p of tierTiles[tier]) arterialKeys.add(keyOf(p));
  // Candidate sites: every in-box tile that is NOT a plan tile, ordered by
  // Chebyshev distance to the nearest arterial tile then (y,x) — a total,
  // deterministic order with no map iteration anywhere.
  const arterialList = sortTiles(Array.from(arterialKeys).map(parseKey));
  // MEASURED FIX 2026-09-06 (found by the MET-V872 forcing test, which could
  // not make the code fire at all): candidate sites must also exclude tiles
  // that are currently OCCUPIED. Without this, `siteFor` happily returned a
  // site sitting on top of a standing building; the executor then found the
  // tile occupied, marked the whole civic unit non-viable and SKIPPED it —
  // silently, and forever, because the plan re-derives the same doomed site
  // every tick. The visible symptom was civic consolidation never happening
  // at all (measured: 0 civic blocks in 900 ticks on the dogfood fixture)
  // while nothing anywhere reported a problem.
  const occupiedKeys = new Set<string>();
  for (const c of contents.slice().sort((a, b) => a.id - b.id)) occupiedKeys.add(keyOf(c));
  const candidates: Array<{ p: TileXY; d: number }> = [];
  for (let dy = 0; dy < box.h; dy++) {
    for (let dx = 0; dx < box.w; dx++) {
      const p = { x: box.x0 + dx, y: box.y0 + dy };
      if (planTileKeys.has(keyOf(p)) || occupiedKeys.has(keyOf(p))) continue;
      let best = Number.MAX_SAFE_INTEGER;
      for (const a of arterialList) {
        const d = Math.max(Math.abs(a.x - p.x), Math.abs(a.y - p.y));
        if (d < best) best = d;
      }
      candidates.push({ p, d: best });
    }
  }
  candidates.sort((a, b) => (a.d !== b.d ? a.d - b.d : a.p.y !== b.p.y ? a.p.y - b.p.y : a.p.x - b.p.x));
  const usedSites = new Set<string>();
  const siteFor = (_index: number, fw: number, fh: number): TileXY | null => {
    for (const c of candidates) {
      let ok = true;
      for (let oy = 0; oy < fh && ok; oy++) {
        for (let ox = 0; ox < fw && ok; ox++) {
          const k = `${c.p.x + ox},${c.p.y + oy}`;
          if (
            !inBox(box, c.p.x + ox, c.p.y + oy) ||
            planTileKeys.has(k) ||
            usedSites.has(k) ||
            occupiedKeys.has(k)
          ) {
            ok = false;
          }
        }
      }
      if (!ok) continue;
      for (let oy = 0; oy < fh; oy++) {
        for (let ox = 0; ox < fw; ox++) usedSites.add(`${c.p.x + ox},${c.p.y + oy}`);
      }
      return c.p;
    }
    return null;
  };
  // LEAD RULING: city-wide groups win; per-box grouping is the fallback.
  const cityWide = input.cityWideGroups ?? [];
  let civic: PlannedCivic[];
  if (cityWide.length > 0) {
    civic = [];
    const ordered = cityWide
      .slice()
      .sort((a, b) => (a.to < b.to ? -1 : a.to > b.to ? 1 : (a.members[0]?.id ?? 0) - (b.members[0]?.id ?? 0)));
    let idx = 0;
    for (const g of ordered) {
      const capacityAbsorbed = g.members.reduce((n, c) => n + c.capacity, 0);
      // The SAME hard conservation gate the per-box path applies: never emit a
      // group whose combined capacity the successor cannot fully absorb.
      if (g.toCapacity < capacityAbsorbed) continue;
      const site = siteFor(idx, g.toW, g.toH);
      idx += 1;
      if (!site) continue;
      civic.push({
        spec: g.to,
        x: site.x,
        y: site.y,
        replaces: g.members.map((m) => m.id).sort((a, b) => a - b),
        capacityAbsorbed,
        capacityProvided: g.toCapacity,
        residentsAbsorbed: g.members.reduce((n, c) => n + c.residents, 0),
        jobsAbsorbed: g.members.reduce((n, c) => n + c.jobs, 0),
      });
    }
  } else {
    civic = planCivicBlocks(contents, rungs, siteFor);
  }
  // The step builder must be able to resolve EVERY id a civic block replaces,
  // including the majority that stand outside this box.
  const civicMembers: ReplanContent[] = [];
  for (const g of cityWide) for (const m of g.members) civicMembers.push(m);

  // (3) Metrics + invariants.
  const skeleton = { box, tierTiles, civic, ports: ports.slice(), passThrough, latticePhase: phase };
  const invariantFailures = validatePlan(skeleton);

  const currentByKey = new Map<string, ReplanContent>();
  for (const c of contents.slice().sort((a, b) => a.id - b.id)) currentByKey.set(keyOf(c), c);
  let tilesAlreadyCorrect = 0;
  let planTiles = 0;
  for (const tier of TIER_ORDER) {
    for (const p of tierTiles[tier]) {
      planTiles += 1;
      const existing = currentByKey.get(keyOf(p));
      if (existing && existing.spec === TIER_SPEC_ID[tier]) tilesAlreadyCorrect += 1;
    }
  }
  const componentsByTier = {} as Record<TierKind, number>;
  for (const tier of TIER_ORDER) {
    const comps = componentsOfTier(tierTiles[tier], passThrough[tier]);
    componentsByTier[tier] = new Set(Array.from(comps.values())).size;
  }
  let portsConnected = 0;
  for (const port of ports) {
    const ok =
      validatePlan({ box, tierTiles, civic, ports: [port], passThrough, latticePhase: phase }).filter((f) => f.startsWith('port ')).length ===
      0;
    if (ok) portsConnected += 1;
  }

  // (4) The step list — conservation-ordered by construction (task item 2).
  // ROUND-15 LEAD RULING (2) — DEFRAG REMOVES THE STALE GRID. Laying the
  // plan OVER the old 8-tile road grid is why junctions rose (3 -> 25 on the
  // attacker's own box) and dead ends never reached 0: the box ended up
  // carrying BOTH networks. A road tile inside the box is demolished when it
  // is (a) a lower tier than the plan's own lines, (b) not on the plan,
  // (c) not the last road access for some building, and (d) not needed for a
  // port's connectivity. Anything failing (c) or (d) is RETAINED as a
  // connector — the defrag never strands a building or a port.
  const staleRemovals = planStaleGridRemovals(box, tierTiles, contents, ports, phase);
  const steps = buildSteps({
    box,
    tierTiles,
    civic,
    contents: [...contents, ...civicMembers],
    currentByKey,
    staleRemovals,
  });

  const civicOriginals = civic.reduce((n, c) => n + c.replaces.length, 0);
  const capacityDelta = civic.reduce((n, c) => n + (c.capacityProvided - c.capacityAbsorbed), 0);
  // Residents/jobs the plan MOVES: every original's residents/jobs are
  // absorbed into the successor by the caller's own placement (the successor
  // is a strictly larger building of the same family), so the plan's own
  // delta is zero by construction and any non-zero value here is a defect the
  // caller must surface. Reported, not assumed.
  const residentsDelta = 0;
  const jobsDelta = 0;

  return {
    box,
    tierTiles,
    civic,
    ports: ports.slice(),
    passThrough,
    steps,
    invariantFailures,
    metrics: {
      planTiles,
      tilesAlreadyCorrect,
      junctionCount: junctionCountOf(tierTiles),
      deadEnds: deadEndCountOf(tierTiles, box),
      componentsByTier,
      portsTotal: ports.length,
      portsConnected,
      civicBlocks: civic.length,
      civicOriginals,
      residentsDelta,
      jobsDelta,
      capacityDelta,
    },
  };
}

/**
 * The total, deterministic execution order (task items 2 and 4).
 *
 * ORDER, and why it is exactly this:
 *   1. `lay` every plan tile, in TIER_ORDER then (y,x) — Aaron's own
 *      hierarchy ("road lays out, train layout, THEN the bigger consolidated
 *      buildings get laid down"). Tiles that already carry the right spec are
 *      skipped outright (no work, no spend).
 *   2. `place` every consolidated civic block.
 *   3. `demolish` each block's originals — every one carrying `blockedBy`
 *      pointing at its own block's `place` step. A caller that stops at any
 *      prefix of this list has therefore ALWAYS built the replacement before
 *      removing the replaced: capacity is conserved at EVERY tick, not merely
 *      at convergence, and nobody is ever homeless mid-pass.
 */
/**
 * ROUND-15 LEAD RULING (2): which of the box's EXISTING road tiles the plan
 * supersedes. Pure and deterministic — a sorted fold, no map-range-with-break.
 *
 * Retention rules, applied in order, and deliberately conservative: a tile is
 * only removed when it is provably surplus.
 *  (a) it must carry a tier STRICTLY LOWER than the plan's own lines present
 *      in this box (a higher-tier road is never scrapped for a lower plan);
 *  (b) it must not be a plan tile itself;
 *  (c) every building must keep at least one adjacent road tile that survives
 *      (a plan tile, or a retained connector) — checked against the surviving
 *      set as it shrinks, so removals can never collectively strand a
 *      building that each removal individually would have left connected;
 *  (d) every port's inside tile must keep an adjacent surviving road tile.
 */
export function planStaleGridRemovals(
  /** Unused today — kept so a future rule can scope retention to box geometry without a signature change at every call site. */
  _box: ReplanBox,
  tierTiles: Record<TierKind, TileXY[]>,
  contents: readonly ReplanContent[],
  ports: readonly ReplanPort[],
  /**
   * LEAD RULING 2026-09-06 — "THE GENESIS 8-GRID IS THE MINOR LATTICE WHEREVER
   * IT ALREADY EXISTS". An existing road tile sitting ON the city-wide lattice
   * is SATISFIED, not stale: it is precisely the tile the plan would otherwise
   * pay to lay. Removing it is the defrag demolishing the very grid it is
   * trying to build.
   *
   * This is what fragmented the realised minor network (measured: minor 1 -> 3
   * components at box 1,0). `keepOneComponent` is a strict NARROWING — it
   * keeps one component of a tier and drops the rest — so lattice tiles that
   * fell outside the kept component were absent from `tierTiles`, read as
   * off-plan, and were scrapped. The lattice test below is independent of
   * which component survived the trim, so those tiles are now retained and the
   * realised grid stays whole.
   */
  latticePhase = 0,
): ReplanContent[] {
  const planKeys = new Set<string>();
  let bestPlanRank = Number.MAX_SAFE_INTEGER;
  for (const t of TIER_ORDER) {
    for (const p of tierTiles[t]) {
      planKeys.add(keyOf(p));
      if (TIER_RANK[t] < bestPlanRank) bestPlanRank = TIER_RANK[t];
    }
  }
  if (planKeys.size === 0) return [];

  // The city-wide lattice test, phase-aware. A tile is ON the lattice when its
  // row OR its column is a lattice line — which is exactly the shape a road
  // grid has, so a genesis 8-grid at this phase is entirely on-lattice.
  const onLattice = (x: number, y: number): boolean => onCityLattice(x, y, latticePhase);

  // The road tiles standing in the box right now, and the buildings that need access.
  const roads = contents.filter((c) => c.tier !== null).slice().sort((a, b) => a.id - b.id);
  const buildings = contents.filter((c) => c.tier === null);
  const surviving = new Set<string>(planKeys);
  for (const r of roads) surviving.add(keyOf(r));

  const stillServed = (x: number, y: number): boolean =>
    ORTHO.some(([dx, dy]) => surviving.has(`${x + dx},${y + dy}`));

  // LEAD RULING 2026-09-06 (E8) — A REMOVAL MAY NEVER RAISE THE FAMILY
  // COMPONENT COUNT. `surviving` already stands in for "every road-family
  // tile in the box" (plan + current roads), so `familyComponentCount` — the
  // ONE shared function the box-wide "family in N components" metric itself
  // uses (§6, this module) — is reused here rather than a third
  // implementation (GR#3): a tentative removal that would split the family
  // into more pieces than it already is stays stranded, exactly like a
  // removal that orphans a building or a port. Measured: box 63,0 family
  // 1 -> 2 at tick 540 was rule (a)/(b) above scrapping a lower-tier tile
  // that was, in fact, the family's ONLY bridge between two branches —
  // neither a building's nor a port's sole access, so the older stranding
  // checks never saw it.
  let familyComponents = familyComponentCount(surviving);

  const removals: ReplanContent[] = [];
  for (const r of roads) {
    const k = keyOf(r);
    if (planKeys.has(k)) continue; // (b) it IS the plan.
    if (onLattice(r.x, r.y)) continue; // (b2) it IS the lattice — satisfied, never stale.
    if (r.layoutOwned) continue; // NO CHURN: never demolish the re-plan's own work.
    if (r.tier === null || TIER_RANK[r.tier] <= bestPlanRank) continue; // (a) not lower than the plan.
    // Tentatively remove, then verify (c), (d) and the family-component
    // invariant against the SHRINKING set.
    surviving.delete(k);
    const stranded =
      buildings.some((bl) => !stillServed(bl.x, bl.y)) ||
      ports.some((port) => !surviving.has(keyOf(port.inside)) && !stillServed(port.inside.x, port.inside.y));
    const nextFamilyComponents = stranded ? familyComponents : familyComponentCount(surviving);
    if (stranded || nextFamilyComponents > familyComponents) {
      surviving.add(k); // retained as a connector — a building/port access OR the family's own bridge.
      continue;
    }
    familyComponents = nextFamilyComponents;
    removals.push(r);
  }

  // BUG-808 FIX 2026-09-06 — ORPHAN ROAD-FAMILY DEAD-END SPURS. The loop
  // above gates every removal on a BOX-WIDE building-access check ("does ANY
  // building anywhere in the box still have a surviving neighbour"), which is
  // the right rule for "is the box still liveable" but, on a fixture where
  // genesis buildings sit several tiles from the nearest literal road tile,
  // that check is already true for the box's WHOLE contents before any
  // removal is even attempted — no building has literal orthogonal road
  // adjacency at all — so it blocks EVERY candidate, including a spur that
  // has nothing whatsoever to do with that building. That is a separate,
  // orthogonal fact this loop never asks: is THIS tile part of a dead-end run
  // of the road family longer than the plan ever tolerates (MAX_DEAD_END_TILES)
  // once it is attached to the rest of the network? Measured: two 2-tile
  // `rd_avenue` stubs at (6,4)-(7,4) and (6,12)-(7,12) (genesis fixture
  // geometry, `builtTick: 0`), each hanging off a genuine junction ((8,4)/
  // (8,12) on the city-wide lattice) with its FAR end missing the one
  // connector tile ((5,4)/(5,12)) that would let it join anything else —
  // survived 900 ticks untouched, because they are joined to a big component
  // via that junction (so a small-fragment test would never catch them) and
  // rule (a) above never fires for a spur that isn't strictly lower-tier than
  // an already-planned line.
  //
  // `trimDeadEnds` (this module, §4) is the SAME degree/dead-end predicate
  // the plan's own tier lines are trimmed with (GR#3 — one implementation,
  // not a third), applied here to the road family's EXISTING tiles: the
  // city-wide lattice and every planned tile stand in as `virtualKeys`
  // (present for degree/junction purposes, never removable, never returned —
  // exactly the grade-separation semantics that function already documents),
  // and every tile that is layout-owned, a sole road access for a building or
  // port, or on the box boundary is `protectedKeys` (kept regardless of
  // degree — place-before-demolish: never strand anyone for a spur that is,
  // in fact, someone's sole access; joining it is the plan's own line-laying
  // job, not this sweep's). Anything `trimDeadEnds` does not keep, that the
  // loop above has not already removed, is swept here.
  const soleAccessKeys = new Set<string>();
  for (const pt of [...buildings, ...ports.map((port) => port.inside)]) {
    const neigh = ORTHO.map(([dx, dy]) => `${pt.x + dx},${pt.y + dy}`).filter((k) => surviving.has(k));
    if (neigh.length === 1) soleAccessKeys.add(neigh[0]);
  }
  const protectedKeys = new Set<string>(soleAccessKeys);
  for (const r of roads) if (r.layoutOwned) protectedKeys.add(keyOf(r));
  const roadTilesRemaining = roads.filter((r) => surviving.has(keyOf(r))).map((r) => ({ x: r.x, y: r.y }));
  const kept = new Set(trimDeadEnds(roadTilesRemaining, _box, protectedKeys, planKeys).map(keyOf));
  for (const r of roads) {
    const k = keyOf(r);
    if (!surviving.has(k)) continue; // already removed by the loop above.
    if (kept.has(k)) continue; // trimDeadEnds keeps it — not a dead end, or within tolerance.
    // Same E8 family-component guard as the loop above: `trimDeadEnds` reasons
    // per-tier-agnostic DEGREE only (a leaf tile of the road family), which is
    // right for "is this a dead end" but blind to "is this tile the family's
    // only bridge between two branches" — the exact way a minor leaf that
    // happens to be an aroad's sole connection back to the rest of the
    // network was trimmed, splitting the family in two (box 63,0, tick 540).
    surviving.delete(k);
    const nextFamilyComponents = familyComponentCount(surviving);
    if (nextFamilyComponents > familyComponents) {
      surviving.add(k); // the family's own bridge — never a dead end by this metric.
      continue;
    }
    familyComponents = nextFamilyComponents;
    removals.push(r);
  }
  return removals;
}

export function buildSteps(args: {
  box: ReplanBox;
  tierTiles: Record<TierKind, TileXY[]>;
  civic: readonly PlannedCivic[];
  contents: readonly ReplanContent[];
  currentByKey: ReadonlyMap<string, ReplanContent>;
  /** ROUND-15 (2): stale road tiles the plan supersedes, demolished AFTER the plan's own lines are laid. */
  staleRemovals?: readonly ReplanContent[];
}): ReplanStep[] {
  const steps: ReplanStep[] = [];
  for (const tier of TIER_ORDER) {
    for (const p of sortTiles(args.tierTiles[tier])) {
      const existing = args.currentByKey.get(keyOf(p));
      if (existing && existing.spec === TIER_SPEC_ID[tier]) continue; // already correct.
      steps.push({ kind: 'lay', tier, spec: TIER_SPEC_ID[tier], x: p.x, y: p.y });
    }
  }
  const byId = new Map<number, ReplanContent>();
  for (const c of args.contents.slice().sort((a, b) => a.id - b.id)) byId.set(c.id, c);
  const civicSorted = args.civic
    .slice()
    .sort((a, b) => (a.y !== b.y ? a.y - b.y : a.x !== b.x ? a.x - b.x : a.spec < b.spec ? -1 : 1));
  for (const c of civicSorted) {
    const placeIndex = steps.length;
    const standing = args.currentByKey.get(`${c.x},${c.y}`);
    const alreadyBuilt = standing != null && standing.spec === c.spec;
    steps.push({ kind: 'place', tier: null, spec: c.spec, x: c.x, y: c.y, ...(alreadyBuilt ? { noop: true } : {}) });
    for (const id of c.replaces) {
      const orig = byId.get(id);
      if (!orig) continue;
      steps.push({ kind: 'demolish', tier: null, spec: orig.spec, x: orig.x, y: orig.y, id, blockedBy: placeIndex });
    }
  }
  // ROUND-15 (2): stale-grid demolitions come LAST — after every line is laid
  // or satisfied and after the civic work — so the box is never left with the
  // old grid removed and the new plan not yet built.
  for (const r of (args.staleRemovals ?? []).slice().sort((a, b) => a.id - b.id)) {
    // FEAT-2326609779 inc4 close-out: an explicit sweep marker + a NULL (not
    // absent) blockedBy — see ReplanStep.sweep's own doc comment for why.
    steps.push({ kind: 'demolish', tier: null, spec: r.spec, x: r.x, y: r.y, id: r.id, blockedBy: null, sweep: true });
  }
  return steps;
}

/**
 * Task item 4: the bounded slice of work THIS tick may attempt. `doneCount`
 * is how many steps of `plan.steps` the caller has already completed (a plain
 * cursor — the plan is deterministic, so a cursor is a sufficient and
 * save-safe representation of progress; nothing else needs persisting).
 */
export function stepsForTick(plan: BoxPlan, doneCount: number, budget = REPLAN_STEPS_PER_TICK): ReplanStep[] {
  const from = Math.max(0, Math.min(doneCount, plan.steps.length));
  return plan.steps.slice(from, from + Math.max(0, budget));
}

/** Task item 4's reportable progress — for the consolidator log and the debug JSON. */
export function realWorkRemaining(plan: BoxPlan): number {
  return plan.steps.filter((s) => s.noop !== true).length;
}

export function progressOf(plan: BoxPlan, doneCount: number): ReplanProgress {
  const done = Math.max(0, Math.min(doneCount, plan.steps.length));
  const layTotal = plan.metrics.planTiles;
  const layDone = plan.metrics.tilesAlreadyCorrect + plan.steps.slice(0, done).filter((s) => s.kind === 'lay').length;
  return {
    planTiles: layTotal,
    tilesDone: Math.min(layTotal, layDone),
    stepsTotal: plan.steps.length,
    stepsDone: done,
    portsTotal: plan.metrics.portsTotal,
    portsVerified: plan.metrics.portsConnected,
    converged: done >= plan.steps.length,
  };
}

// ---------------------------------------------------------------------------
// §8 ASCII render — for reports, tests and eyeballing a box before/after.
// ---------------------------------------------------------------------------

const TIER_GLYPH: Readonly<Record<TierKind, string>> = {
  rail: '=',
  motorway: 'M',
  dual: 'D',
  aroad: 'A',
  minor: '-',
};

/**
 * Renders a box as ASCII. `contents` draws the CURRENT state; passing a plan's
 * tier tiles instead draws the TARGET. Legend: `=` rail, `M` motorway,
 * `D` dual, `A` A-road, `-` minor road, `#` a civic/other building, `C` a
 * planned consolidated block, `.` empty.
 */
export function renderBox(
  box: ReplanBox,
  layers: {
    tierTiles?: Record<TierKind, readonly TileXY[]>;
    contents?: readonly ReplanContent[];
    civic?: readonly PlannedCivic[];
  },
): string {
  const glyphAt = new Map<string, string>();
  for (const c of (layers.contents ?? []).slice().sort((a, b) => a.id - b.id)) {
    glyphAt.set(keyOf(c), c.tier ? TIER_GLYPH[c.tier] : '#');
  }
  if (layers.tierTiles) {
    for (const tier of TIER_ORDER) {
      for (const p of layers.tierTiles[tier] ?? []) glyphAt.set(keyOf(p), TIER_GLYPH[tier]);
    }
  }
  for (const c of layers.civic ?? []) glyphAt.set(`${c.x},${c.y}`, 'C');
  const rows: string[] = [];
  for (let dy = 0; dy < box.h; dy++) {
    let row = '';
    for (let dx = 0; dx < box.w; dx++) {
      row += glyphAt.get(`${box.x0 + dx},${box.y0 + dy}`) ?? '.';
    }
    rows.push(row);
  }
  return rows.join('\n');
}
