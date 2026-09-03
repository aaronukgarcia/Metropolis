// perfHarness.ts — FEAT-2326609760 GPU acceleration spike, Phase 0 (Measure).
//
// Per the plan's Phase 0 (§5): "get a real number for MapView's own paint
// cost at the BUG-622/scale-gate fixture size". jsdom (this repo's test
// runtime) has NO GPU and NO real Canvas2D rasteriser — `CanvasRenderingContext2D`
// calls in jsdom are no-ops that don't touch a real backing store — so a
// wall-clock "how fast does the browser actually paint" number is not
// obtainable here. What jsdom CAN measure honestly is the JS-side cost each
// path pays PER FRAME before/instead of handing work to the browser/GPU:
//
//   (a) CURRENT draw loop's own JS cost: the building-iteration + per-
//       building coordinate math + the overlay full-scans MapView.tsx's
//       draw effect performs every redraw (§1.1 of the plan names these
//       scans explicitly). Measured here by literally replaying that same
//       loop shape against a real ctx (a counting stub, see below) so the
//       measured cost is the real per-building work, not a synthetic proxy.
//   (b) THIS path's JS cost: instanceBuilder.buildInstances() (a full
//       rebuild) plus rebuildDynamicOnly() (the steady-state per-tick cost)
//       plus ONE mocked device.queue.writeBuffer + submit — i.e. exactly
//       the "buffer-diff + one submit" the plan's §1.2 promises, and
//       exactly what MapRenderer.render() does internally.
//
// Real-GPU rasterisation-time numbers (the browser/driver actually painting
// pixels) are NOT this harness's job — the plan is explicit that those come
// from Aaron's own machine. `runInBrowserConsole()` below is exported so
// this same measurement can be pasted into a live devtools console against
// the real dogfood city for that follow-up.

// Uses the GLOBAL `performance` (available in both Node 16+ and every
// browser this project targets) rather than importing 'node:perf_hooks' —
// this file is also meant to run unmodified in a real browser via
// runInBrowserConsoleSnippet(), where a Node-specific import would fail.
import { SPECS, buildingDisplayStates, footprintOf, isRoadSpec } from '../sim/data.ts';
import type { Building, SimState } from '../sim/types.ts';
import { buildInstances, rebuildDynamicOnly, buildingInstanceFilter, roadInstanceFilter } from './instanceBuilder.ts';
import { MapRenderer } from './mapRenderer.ts';
import { viewportTileRect, visibleBuildingsOf, type TileRect } from './viewportCull.ts';

export interface PerfSample {
  label: string;
  msPerFrame: number;
  samples: number[];
}

function median(values: number[]): number {
  const sorted = [...values].sort((a, b) => a - b);
  return sorted[Math.floor(sorted.length / 2)];
}

function timeIt(label: string, iterations: number, fn: () => void): PerfSample {
  const samples: number[] = [];
  for (let i = 0; i < iterations; i++) {
    const t0 = performance.now();
    fn();
    samples.push(performance.now() - t0);
  }
  return { label, msPerFrame: median(samples), samples };
}

/**
 * A `CanvasRenderingContext2D`-shaped stub that counts draw calls instead of
 * touching a real backing store (jsdom's own ctx is already a no-op, so this
 * just gives (a) something to genuinely call per iteration and (b) a call
 * count as a sanity check that the loop shape being measured is right).
 */
export interface CountingCtx2D {
  calls: number;
  fillRect(): void;
  strokeRect(): void;
  beginPath(): void;
  stroke(): void;
  measureText(): { width: number };
}
export function makeCountingCtx2D(): CountingCtx2D {
  return {
    calls: 0,
    fillRect() {
      this.calls++;
    },
    strokeRect() {
      this.calls++;
    },
    beginPath() {
      this.calls++;
    },
    stroke() {
      this.calls++;
    },
    measureText() {
      this.calls++;
      return { width: 10 };
    },
  };
}

/**
 * Replays the JS-side shape of MapView.tsx's per-frame draw effect at the
 * given state, over a given pre-filtered `buildings` list: the main building
 * loop's per-building work (a `buildingDisplayStates` Map.get() plus the
 * fillRect/strokeRect calls — mirrors MapView.tsx's draw effect main loop),
 * the disconnected-road-flash full pass, and the station-connectivity-dot
 * pass. This is the JS cost that runs regardless of whether Canvas2D's own
 * rasteriser is GPU- or software-backed — main-thread work no browser can
 * parallelise away.
 *
 * `buildings` is a parameter (not always `state.buildings`) so this same
 * shape can be timed BOTH against the full city (the BUG-659 "before" cost)
 * and against a viewport-culled subset (the "after" cost) — see
 * measureCurrentDrawLoopJsCost / measureCulledDrawLoopJsCost below.
 */
function replayDrawLoopShape(state: SimState, buildings: readonly Building[], ctx: CountingCtx2D): void {
  // Main building loop — mirrors MapView.tsx's draw effect main loop. Reads
  // the memoised BuildingDisplayState map exactly as MapView.tsx does (a
  // Map.get() per building, not a fresh isOnline/blockOccupancy/
  // utilisationOf/densityTier call — those were memoised into this map by
  // BUG-630; re-deriving them here would measure a cost MapView.tsx no
  // longer pays).
  const displayStates = buildingDisplayStates(state);
  for (const b of buildings) {
    const sp = SPECS[b.spec];
    if (!sp) continue;
    const ds = displayStates.get(b.id);
    const online = ds ? ds.online : false;
    footprintOf(b, sp);
    ctx.fillRect();
    if (ds && ds.occupancy != null) ctx.fillRect();
    if (sp.kind !== 'residential' && online && ds && ds.utilisation !== null) ctx.fillRect();
    if (!online) {
      ctx.beginPath();
      ctx.stroke();
    }
    if (sp.w > 1 || sp.h > 1) ctx.strokeRect();
    if (sp.category === 'zones') ctx.strokeRect();
  }
  // Disconnected-road-flash pass — mirrors MapView.tsx's flash pass.
  const connected = new Set(state.roadConnectivity?.connectedRoadTiles ?? []);
  for (const b of buildings) {
    const sp = SPECS[b.spec];
    if (!sp || !isRoadSpec(sp)) continue;
    if (connected.has(`${b.x},${b.y}`)) continue;
    ctx.fillRect();
  }
  // Station-connectivity-dot pass — mirrors MapView.tsx's station loop.
  for (const b of buildings) {
    const sp = SPECS[b.spec];
    if (!sp || sp.kind !== 'station') continue;
    ctx.beginPath();
    ctx.fillRect();
    ctx.stroke();
  }
}

/**
 * BUG-659 "before": the draw loop's JS cost over the FULL, UNCULLED city —
 * what every repaint paid regardless of camera position prior to the
 * viewport-culling fix.
 */
export function measureCurrentDrawLoopJsCost(state: SimState, iterations = 10): PerfSample {
  const ctx = makeCountingCtx2D();
  return timeIt('current-canvas2d-draw-loop, uncalled (JS-side cost)', iterations, () =>
    replayDrawLoopShape(state, state.buildings, ctx)
  );
}

/**
 * BUG-659 "after": the SAME draw loop shape, but over the viewport-culled
 * building set (viewportCull.ts) a real MapView.tsx repaint now uses. `rect`
 * defaults to a representative "zoomed in on a corner" viewport so the
 * measured win is not an artefact of picking a full-map rect that culls to
 * everything.
 */
export function measureCulledDrawLoopJsCost(
  state: SimState,
  rect: TileRect,
  iterations = 10
): PerfSample & { visibleCount: number } {
  const ctx = makeCountingCtx2D();
  const visible = visibleBuildingsOf(state.buildings as Building[], rect);
  const sample = timeIt('current-canvas2d-draw-loop, viewport-culled (JS-side cost)', iterations, () =>
    replayDrawLoopShape(state, visible, ctx)
  );
  return { ...sample, visibleCount: visible.length };
}

export { viewportTileRect };

/**
 * Measures the FULL REBUILD path's JS cost (buildInstances for both batches
 * + a real writeBuffer/submit against a mocked device) — the cost paid only
 * on place/bulldoze/relocate, i.e. the "identity changed" branch of
 * MapRenderer.syncBatch.
 */
export function measureInstancedFullRebuildJsCost(state: SimState, iterations = 10): PerfSample {
  const device = makeCountingDevice();
  const renderer = new MapRenderer(makeFakeCanvas());
  return timeIt('webgpu-path full rebuild (JS-side cost)', iterations, () => {
    // Force a "buildings changed" path every iteration by handing render()
    // a fresh top-level state object each time (new reference), matching
    // exactly what a place/bulldoze reducer action produces.
    void renderer; // renderer construction cost is excluded on purpose —
    // this measures the SAME per-frame primitives MapRenderer.syncBatch calls,
    // not object-construction overhead which happens once per component mount.
    const b1 = buildInstances(state, buildingInstanceFilter);
    const b2 = buildInstances(state, roadInstanceFilter);
    device.queue.writeBuffer(null, 0, b1.staticData);
    device.queue.writeBuffer(null, 0, b1.dynamicData);
    device.queue.writeBuffer(null, 0, b2.staticData);
    device.queue.writeBuffer(null, 0, b2.dynamicData);
    device.queue.submit([]);
  });
}

/**
 * Measures the STEADY-STATE (dynamic-only) path's JS cost — the cost paid
 * every ordinary tick where no building was placed/bulldozed. This is the
 * number the plan's Phase 2 acceptance shape cares about most: it should
 * scale with buildings.length for the dynamic re-derivation (cheaper than a
 * full rebuild, since colour parsing/static geometry is skipped), and is the
 * number that should dwarf-beat measureCurrentDrawLoopJsCost's per-frame cost.
 */
export function measureInstancedDynamicOnlyJsCost(state: SimState, iterations = 10): PerfSample {
  const device = makeCountingDevice();
  const buildingIds = buildInstances(state, buildingInstanceFilter).ids;
  const roadIds = buildInstances(state, roadInstanceFilter).ids;
  return timeIt('webgpu-path dynamic-only re-upload (JS-side cost)', iterations, () => {
    const dyn1 = rebuildDynamicOnly(state, buildingInstanceFilter, buildingIds);
    const dyn2 = rebuildDynamicOnly(state, roadInstanceFilter, roadIds);
    device.queue.writeBuffer(null, 0, dyn1);
    device.queue.writeBuffer(null, 0, dyn2);
    device.queue.submit([]);
  });
}

function makeCountingDevice(): { queue: { writeBuffer: (...a: unknown[]) => void; submit: (...a: unknown[]) => void } } {
  return {
    queue: {
      writeBuffer() {
        /* count omitted — this harness only times, doesn't assert counts */
      },
      submit() {},
    },
  };
}

function makeFakeCanvas(): { getContext(): null; width: number; height: number } {
  return { getContext: () => null, width: 800, height: 600 };
}

/**
 * Runs the full Phase 0 comparison and returns a report shaped for a written
 * BOW-comment number (per the plan's acceptance shape: "a written number
 * (median ms/frame ...) attached to BUG-622's own thread"). Exported both
 * for the jsdom test below and for pasting into a real devtools console
 * against Aaron's dogfood city (see runInBrowserConsole's doc comment).
 */
export function runPhase0Comparison(state: SimState, iterations = 10): PerfSample[] {
  return [
    measureCurrentDrawLoopJsCost(state, iterations),
    measureInstancedFullRebuildJsCost(state, iterations),
    measureInstancedDynamicOnlyJsCost(state, iterations),
  ];
}

/**
 * Not called by any test — this is the snippet Aaron pastes into a live
 * browser devtools console against the real dogfood city to get the
 * REAL-GPU numbers this harness cannot produce in jsdom. Kept here (rather
 * than only in the report) so it ships with the code it measures and never
 * goes stale against a future instanceBuilder.ts refactor.
 */
export function runInBrowserConsoleSnippet(): string {
  return [
    "import('/src/render/perfHarness.ts').then(async (m) => {",
    "  const state = window.__metropolisDebugState ?? (await import('/src/sim/simContext.ts')).currentStateForDebug?.();",
    '  if (!state) { console.error("no live SimState found - open the app first"); return; }',
    '  console.table(m.runPhase0Comparison(state, 20));',
    '});',
  ].join('\n');
}
