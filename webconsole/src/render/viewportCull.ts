// viewportCull.ts — BUG-659 P0: Aaron's 49,174-building / 3.2M-population
// dogfood city stalled the map for 6.1s per repaint. Headless engine tick
// medians (147.8ms) plus every derivation MapView.tsx reads (buildingDisplayStates,
// computeFlows, wellbeingOf, ...) sum to well under 700ms on that same real
// savepoint — the missing seconds are the RENDER path, not the sim. Profiling
// (see perfHarness.ts's measureCurrentDrawLoopJsCost) confirms MapView.tsx's
// draw effect makes THREE unconditional full-array passes over
// `state.buildings` every single repaint regardless of camera position: the
// main building-fill loop, the disconnected-road-flash pass, and the
// station-connectivity-dot pass — none of them skip buildings that are
// off-screen. At 49k buildings that is 3 x 49,174 = ~147k building visits
// per frame even when the camera is zoomed in on a tiny corner of the
// 440x260 map and only a few hundred buildings are actually visible.
//
// THE FIX: viewport culling. A building is drawn only if its REAL footprint
// (footprintOf — GR#3 SSOT, a grown/scaled-out building occupies more tiles
// than its spec's base w/h) intersects the current camera viewport, in TILE
// space (independent of zoom/devicePixelRatio — this module never reads
// window.devicePixelRatio, Date.now, or localStorage: GR#21 + the BUG-602
// no-clock-in-hot-path lesson).
//
// SPATIAL INDEX, NOT A LINEAR SCAN: naively "filtering" state.buildings to
// the viewport is STILL an O(n) walk of the whole array every frame — it
// would cut per-building WORK (fewer fillRect calls) but not the O(n) SCAN
// cost of deciding who's visible, which is itself a meaningful fraction of
// the cost at 49k (per-building SPECS lookup + footprintOf + a box-overlap
// test). So buildings are bucketed into a uniform grid once per distinct
// `buildings` array reference (memoised via WeakMap — the same idiom as
// data.ts's memoOnState / this file's sibling MapView.tsx overlaySubsetsOf),
// and a viewport query only visits the grid cells that actually overlap the
// camera rect plus a margin wide enough to catch any building whose ORIGIN
// is outside the viewport but whose grown footprint still overlaps it — the
// margin is measured from the real data (the largest w/h actually present
// in this buildings array), never a guessed constant, so a future taller
// auto-scale ladder can never silently reintroduce an edge-clipping bug.
//
// CORRECTNESS CONTRACT (non-negotiable per BUG-659's brief): the returned
// set must be EXACTLY the buildings whose footprint intersects the viewport
// rect — never a superset (extra off-screen draws are wasted work) and
// NEVER a subset (a dropped building that should be on screen is a visible
// rendering bug, worse than any perf win). The grid narrows candidates for
// speed; every candidate is still exactly box-tested against the real
// viewport rect before being included — see visibleBuildingsOf below.

import { SPECS, footprintOf } from '../sim/data.ts';
import type { Building } from '../sim/types.ts';

/** Axis-aligned rect in TILE coordinates (not pixels — camera/zoom-independent). */
export interface TileRect {
  minX: number;
  minY: number;
  maxX: number;
  maxY: number;
}

interface SpatialIndex {
  cellSize: number;
  cells: Map<string, Building[]>;
  /** Largest w or h footprint actually present — DERIVED from the data
   * (GR#15: validators/margins derive from data, never hardcoded), so the
   * cell-overlap margin below always covers every real grown building. */
  maxFootprint: number;
}

// 16 tiles/cell: small enough that a typical zoomed-in viewport (a handful
// of screen-widths of tiles) touches only a few cells, large enough that a
// 440x260 map does not explode into thousands of near-empty cells.
const CELL_SIZE = 16;

const spatialIndexCache = new WeakMap<object, SpatialIndex>();

function buildSpatialIndex(buildings: Building[]): SpatialIndex {
  const cells = new Map<string, Building[]>();
  let maxFootprint = 1;
  for (const b of buildings) {
    const sp = SPECS[b.spec];
    if (!sp) continue;
    const { w, h } = footprintOf(b, sp);
    if (w > maxFootprint) maxFootprint = w;
    if (h > maxFootprint) maxFootprint = h;
    const cx = Math.floor(b.x / CELL_SIZE);
    const cy = Math.floor(b.y / CELL_SIZE);
    const key = `${cx},${cy}`;
    let arr = cells.get(key);
    if (!arr) {
      arr = [];
      cells.set(key, arr);
    }
    arr.push(b);
  }
  return { cellSize: CELL_SIZE, cells, maxFootprint };
}

/** Memoised on the `buildings` array's own identity — immutable per tick,
 * same as data.ts's memoOnState / MapView.tsx's overlaySubsetsOf. A camera
 * pan/zoom redraw with the SAME `state.buildings` reference (no sim tick
 * advanced) reuses the index instead of rebucketing 49k buildings again. */
export function spatialIndexOf(buildings: Building[]): SpatialIndex {
  const cached = spatialIndexCache.get(buildings);
  if (cached) return cached;
  const idx = buildSpatialIndex(buildings);
  spatialIndexCache.set(buildings, idx);
  return idx;
}

/**
 * Returns EXACTLY the buildings in `buildings` whose real footprint
 * (footprintOf) intersects `rect` (tile coordinates, half-open —
 * [minX,maxX) x [minY,maxY), matching every other tile-rect convention in
 * this codebase e.g. buildOccupiedSet). Never a superset, never a subset —
 * see this file's header comment for the correctness contract.
 */
export function visibleBuildingsOf(
  buildings: Building[],
  rect: TileRect,
  onCandidate?: (b: Building) => void
): Building[] {
  const idx = spatialIndexOf(buildings);
  const margin = idx.maxFootprint;
  const cMinX = Math.floor((rect.minX - margin) / idx.cellSize);
  const cMaxX = Math.floor((rect.maxX + margin) / idx.cellSize);
  const cMinY = Math.floor((rect.minY - margin) / idx.cellSize);
  const cMaxY = Math.floor((rect.maxY + margin) / idx.cellSize);
  const out: Building[] = [];
  for (let cy = cMinY; cy <= cMaxY; cy++) {
    for (let cx = cMinX; cx <= cMaxX; cx++) {
      const arr = idx.cells.get(`${cx},${cy}`);
      if (!arr) continue;
      for (const b of arr) {
        // Optional test-only probe (BUG-757): counts every building-candidate
        // the spatial-index walk actually examines, so a test can assert an
        // ALGORITHMIC "the culled path visits no more candidates than the
        // pre-fix unculled scan" bound instead of a flaky wall-clock one.
        // No-op (a single `undefined` check) on every production call site,
        // which never passes this argument — zero behavioural change.
        onCandidate?.(b);
        const sp = SPECS[b.spec];
        if (!sp) continue;
        const { w, h } = footprintOf(b, sp);
        const bx0 = b.x;
        const by0 = b.y;
        const bx1 = b.x + w;
        const by1 = b.y + h;
        if (bx1 <= rect.minX || bx0 >= rect.maxX || by1 <= rect.minY || by0 >= rect.maxY) continue;
        out.push(b);
      }
    }
  }
  return out;
}

/**
 * Builds a TileRect from MapView.tsx's own screen<->tile geometry (geom.ox/
 * geom.oy/geom.s and the canvas size), with `paddingTiles` extra tiles on
 * every side so labels/overlay glyphs anchored at a building's edge (e.g.
 * the reference-id overlay, FEAT-1972079903) that starts just inside the
 * viewport but paints a few px past its edge are never clipped mid-frame.
 * Pure arithmetic — no clock/storage reads (GR#21 / BUG-602).
 */
export function viewportTileRect(
  geom: { ox: number; oy: number; s: number },
  size: { w: number; h: number },
  paddingTiles = 2
): TileRect {
  if (geom.s <= 0) return { minX: 0, minY: 0, maxX: 0, maxY: 0 };
  return {
    minX: (0 - geom.ox) / geom.s - paddingTiles,
    minY: (0 - geom.oy) / geom.s - paddingTiles,
    maxX: (size.w - geom.ox) / geom.s + paddingTiles,
    maxY: (size.h - geom.oy) / geom.s + paddingTiles,
  };
}
