// consolidatorGlide.ts — FEAT-2326609761 inc2, GLIDE MODE traversal.
//
// AARON'S RULING (2026-09-04, BOW FEAT-2326609761): "a consolidator
// traversal mode where the focus window starts top-left and moves ONE PIXEL
// (tile-column) RIGHT PER GAME DAY; at the end of a row it returns to the
// left edge one pixel LOWER, multi-passing slowly and continuously over the
// whole tile like a scanline... Complements (does not replace) the
// monthly-twelfth cadence — glide is the slow continuous walker, the
// month-12 whole-map pass still runs... GLIDE IS THE DEFAULT MODE."
//
// SCOPE (this increment, read-only half — see the FEAT-2326609761 inc2
// dispatch brief): this module is the PURE CURSOR only. It answers "which
// tile-rectangle is the focus window on for game-day D, at section-tile-size
// T" and nothing else — no SimState mutation, no building scan, no economics.
// It is deliberately independent of consolidator.ts's fixed-800m SectionAudit
// grid (sectionIndexOf/twelfths/monthlyScopeOf): those remain the AUDIT
// engine's own section partition (unchanged by this increment — the mutation
// lane that consumes findOpportunities/sectionIndexOf is landing separately
// and in parallel with this build; re-deriving that engine's grid from the
// player's adjustable section size is follow-up scope for whichever lane
// wires applyConsolidatorPass, see consolidator.ts's consolidatorSectionTilesOf
// doc comment). THIS module is a continuous SLIDING WINDOW over raw tile
// coordinates, sized by the player's chosen section size but advancing by
// ONE TILE (not one whole section) per day, which is what makes it a
// "glide" rather than a jump between the existing 476 fixed sections.
//
// GR#21 (determinism): every export here is a pure fold over (dayIndex,
// grid dims) — no Date.now, no localStorage, no stored cursor. "One pixel
// per game day" is Aaron's own words for why this is safe: the tick already
// IS the day counter (TICKS_PER_MONTH=30 mirrors the engine's day-tick
// convention), so `glideWindowOf(s.tick, ...)` is deterministic and
// replay-safe by construction — re-deriving from a persisted tick after a
// save/load reproduces the identical window, and a section-size change
// mid-glide simply changes which grid the NEXT call derives against (no
// interpolation, no stored "resume position" needed — see
// consolidatorSectionTilesOf in consolidator.ts for the one piece of state
// this reads).

// FEAT-2326609761 inc2 (import-cycle avoidance, added when the mutation lane
// landed): this module MUST stay a true leaf with ZERO imports from
// consolidator.ts or engine.ts. engine.ts's applyConsolidatorPass (the
// mutation lane) needs to call into this module directly to compute the
// day's glide window server-side, and consolidator.ts already imports
// TICKS_PER_MONTH/CONNECT_EXEMPT_KINDS FROM engine.ts — so an
// engine.ts -> consolidatorGlide.ts -> consolidator.ts -> engine.ts cycle
// would exist the moment this file imported MAP_W/MAP_H from consolidator.ts
// (which is exactly what it did in the inc2 read-only build, when only
// MapView.tsx — outside the sim module graph — consumed it). Mirrors
// consolidator.ts's OWN header note on why CONSOLIDATOR_ENABLED_DEFAULT
// lives in engine.ts rather than being imported, and its own MAP_W/MAP_H
// local mirror of data.ts's values for the identical reason.
/** Local mirror of consolidator.ts's MAP_W/MAP_H (itself a mirror of data.ts's) — VALUES must stay in sync (both are 440x260), see the cycle-avoidance note above for why this is a duplicated constant rather than an import. */
const MAP_W = 440;
const MAP_H = 260;

/**
 * The glide grid's shape for a given section-tile size: how many distinct
 * window positions exist along each axis. A window of width `sectionTiles`
 * sliding across `MAP_W` tiles one column at a time has
 * `MAP_W - sectionTiles + 1` valid left-edge (x0) positions (the last valid
 * x0 is `MAP_W - sectionTiles`, beyond which the window would spill off the
 * map) — same reasoning on the y axis with MAP_H/rows. `Math.max(1, ...)`
 * guards a pathological `sectionTiles >= MAP_W` (or `>= MAP_H`) config from
 * producing a zero/negative position count; CONSOLIDATOR_SECTION_TILES_MAX
 * (consolidator.ts) keeps that case out of reach in practice, this is
 * defence in depth only.
 */
export interface GlideGrid {
  sectionTiles: number;
  /** Number of distinct x0 (left-edge column) positions the window can occupy — SKIP-EMPTY (Aaron, 2026-09-04) note: this is the number of positions the cursor actually VISITS, not `MAP_W - sectionTiles + 1` any more when a `nonEmptyColumns` filter was supplied to glideGridOf — see `columns` below. */
  xPositions: number;
  /** Number of distinct y0 (top-edge row) positions the window can occupy. Rows are NEVER skipped by the skip-empty feature (only columns are, per Aaron's literal wording, "marching ants and red squares only need to look at squares with content" scoped to the day->COLUMN mapping) — this stays the full `MAP_H - sectionTiles + 1` always. */
  yPositions: number;
  /** xPositions * yPositions — the number of game-days in one full multi-pass sweep. */
  positionsPerPass: number;
  /**
   * SKIP-EMPTY-LAND (Aaron, 2026-09-04, verbatim: "marchin ants and red
   * squares only need to look at squares with contnet so skip where nothing
   * is built"): `columns[i]` is the real tile x0 the cursor sits on for
   * x-position-index `i` (0..xPositions-1) — glideWindowOf indexes THIS
   * array instead of using `dayInPass % xPositions` directly as x0, which is
   * what turns "day N" into "the Nth NON-EMPTY column" rather than "the Nth
   * column, period". When glideGridOf's `nonEmptyColumns` argument is
   * omitted (every pre-skip-empty caller/test), `columns` is the plain
   * identity ramp `[0, 1, ..., xPositions-1]` — i.e. every valid column is
   * "non-empty" by assumption, reproducing the exact old dense raster
   * one-for-one (backward compatible, GR#24/no behaviour change for callers
   * that haven't opted in). Always sorted ascending and deduplicated, so the
   * mapping is a pure, order-independent function of the SET of occupied
   * columns (GR#21 — a shuffled buildings array that occupies the same
   * columns produces the identical `columns` array and hence the identical
   * glide sequence).
   */
  columns: number[];
}

/**
 * `sectionTiles` -> the glide grid shape. `nonEmptyColumns` (Aaron's
 * 2026-09-04 skip-empty-land ruling) is an OPTIONAL list of tile-column
 * indices (raw map x coordinates, 0..MAP_W-1) known to have at least one
 * building footprint intersecting them — typically `occupiedColumnsOf(s)`
 * from data.ts (this module stays a zero-import leaf per the cycle-avoidance
 * note at the top of the file, so it never computes that set itself; the
 * caller — engine.ts / MapView.tsx, both of which already import data.ts —
 * hands it in as plain data).
 *
 * Three cases:
 *  - `nonEmptyColumns` omitted (`undefined`): legacy dense behaviour, EVERY
 *    valid x0 position (0..MAP_W-sectionTiles) is visited, in order — this
 *    is the exact pre-skip-empty raster, so every existing caller/test that
 *    doesn't pass this argument is byte-identical to before.
 *  - `nonEmptyColumns` supplied and non-empty after clamping to the valid x0
 *    range: only those columns are visited, ascending, deduplicated — the
 *    skip-empty behaviour. A column outside the valid x0 range (e.g. a
 *    building sitting in the last `sectionTiles-1` tiles of the map, where
 *    starting a window there would spill off the edge) is silently dropped,
 *    exactly like the pre-existing `Math.min` edge clipping this module
 *    already does elsewhere.
 *  - `nonEmptyColumns` supplied but EMPTY, or every entry clamped away (a
 *    zero-buildings genesis city, or a hostile/degenerate input): falls back
 *    to the single column `[0]` — never a divide-by-zero, never an empty
 *    `columns` array, matching the "zero buildings -> column 0 / draw
 *    nothing" edge case Aaron's brief calls out explicitly.
 */
export function glideGridOf(sectionTiles: number, nonEmptyColumns?: readonly number[]): GlideGrid {
  const t = Math.max(1, Math.floor(sectionTiles));
  const maxX0 = Math.max(0, MAP_W - t);
  const yPositions = Math.max(1, MAP_H - t + 1);
  let columns: number[];
  if (nonEmptyColumns === undefined) {
    columns = Array.from({ length: maxX0 + 1 }, (_, i) => i);
  } else {
    const valid = new Set<number>();
    for (const c of nonEmptyColumns) {
      const x0 = Math.floor(c);
      if (Number.isFinite(x0) && x0 >= 0 && x0 <= maxX0) valid.add(x0);
    }
    columns = Array.from(valid).sort((a, b) => a - b);
    if (columns.length === 0) columns = [0];
  }
  const xPositions = columns.length;
  return { sectionTiles: t, xPositions, yPositions, positionsPerPass: xPositions * yPositions, columns };
}

export interface GlideWindow {
  x0: number;
  y0: number;
  w: number;
  h: number;
  /** The day index this window was derived from, reduced into the current pass (0..positionsPerPass-1). */
  dayInPass: number;
  /** Which full multi-pass sweep of the whole map this day falls in (0-based, monotonic with dayIndex). */
  passIndex: number;
}

/**
 * THE GLIDE CURSOR. Pure function of (dayIndex, grid) — GR#21. `dayIndex` is
 * expected to be `s.tick` (one engine tick == one game day, matching
 * TICKS_PER_MONTH's 30-day month); this function itself is tick-unit
 * agnostic and just treats its input as "day N", so callers stay free to
 * pass any day-granular counter without this module caring.
 *
 * Raster order: x0 advances 0, 1, 2, ... up to xPositions-1 (one tile-column
 * right per day), then wraps to x0=0 with y0 advanced by one tile-row; once
 * y0 reaches yPositions-1 and wraps, the whole sweep repeats (passIndex
 * increments) — "multi-passing... forever" per the ruling. `((n % m) + m) %
 * m` handles a negative dayIndex defensively (should never occur — ticks
 * only increase — but a raw `%` on a negative JS number returns negative,
 * which would otherwise index outside the valid range).
 *
 * w/h are always exactly `grid.sectionTiles` by construction (x0 never
 * exceeds `MAP_W - sectionTiles`, so the window never needs edge-clipping,
 * unlike consolidator.ts's fixed-section sectionOriginOf) — the Math.min
 * calls are defence-in-depth only, matching that file's own clipping idiom
 * for a reader's familiarity, not because clipping is ever expected to bite.
 *
 * SKIP-EMPTY-LAND (Aaron, 2026-09-04): `x0` is now read from
 * `grid.columns[xIndex]` rather than being `xIndex` itself — this is the
 * WHOLE mechanism by which "day N" becomes "the Nth NON-EMPTY column" (see
 * GlideGrid.columns' own doc comment). `dayIndex`/`dayInPass`/`passIndex`
 * arithmetic and the y0 row advance are completely unchanged — only which
 * columns actually appear moves. DESIGN NOTE (Aaron-accepted, carried over
 * from the brief verbatim): the cursor is a pure function of
 * `(occupiedColumns, tick)` now rather than of `tick` alone — still
 * deterministic and save-safe (nothing about it reads Date/localStorage/
 * random, and re-deriving `occupiedColumns` from the same `buildings` array
 * after a save/load reproduces byte-identical output), but a building placed
 * or removed mid-sweep can shift which real map column a given `dayInPass`
 * index lands on the NEXT time this is called (exactly like a section-size
 * change mid-glide already could, per the doc comment above) — there is no
 * "resume position" to desync because none is ever persisted.
 */
export function glideWindowOf(dayIndex: number, grid: GlideGrid): GlideWindow {
  const total = grid.positionsPerPass;
  const dayInPass = ((dayIndex % total) + total) % total;
  const passIndex = Math.floor((dayIndex - dayInPass) / total);
  const y0 = Math.floor(dayInPass / grid.xPositions);
  const xIndex = dayInPass % grid.xPositions;
  const x0 = grid.columns[xIndex];
  const w = Math.min(grid.sectionTiles, MAP_W - x0);
  const h = Math.min(grid.sectionTiles, MAP_H - y0);
  return { x0, y0, w, h, dayInPass, passIndex };
}

/**
 * Convenience one-shot: derive the glide window directly from a day index
 * and section-tile size, without the caller needing to build a GlideGrid
 * first. Cheap (glideGridOf is O(1) — the caller-supplied `nonEmptyColumns`
 * array itself is assumed already-computed/memoised by the caller, e.g.
 * data.ts's `occupiedColumnsOf`, never recomputed here) — safe to call per
 * render frame or per tick without memoisation. `nonEmptyColumns` is
 * optional and forwarded verbatim to glideGridOf — see its doc comment for
 * the three-case (omitted / non-empty / empty-after-clamping) contract.
 */
export function glideWindowForDay(dayIndex: number, sectionTiles: number, nonEmptyColumns?: readonly number[]): GlideWindow {
  return glideWindowOf(dayIndex, glideGridOf(sectionTiles, nonEmptyColumns));
}
