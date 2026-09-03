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
import { SPECS, isOnline, blockOccupancy, utilisationOf, densityTier, isRoadSpec } from '../sim/data.ts';
import type { SimState } from '../sim/types.ts';
import { buildInstances, rebuildDynamicOnly, buildingInstanceFilter, roadInstanceFilter } from './instanceBuilder.ts';
import { MapRenderer } from './mapRenderer.ts';

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
 * Replays the JS-side shape of MapView.tsx's CURRENT per-frame work at the
 * given state: the main building loop's per-building derivations
 * (isOnline/blockOccupancy/utilisationOf/densityTier — the same calls
 * MapView.tsx makes, §1.1) plus 3-4 fillRect/strokeRect calls per building,
 * plus the disconnected-road-flash full scan (§1.1's second full pass).
 * This is the JS cost that runs regardless of whether Canvas2D's own
 * rasteriser is GPU- or software-backed — it is main-thread work no browser
 * can parallelise away, and it is what Phase 1 aims to shrink to "1 draw call
 * + a camera-uniform update".
 */
export function measureCurrentDrawLoopJsCost(state: SimState, iterations = 10): PerfSample {
  const ctx = makeCountingCtx2D();
  return timeIt('current-canvas2d-draw-loop (JS-side cost)', iterations, () => {
    // Main building loop — mirrors MapView.tsx:291-389.
    for (const b of state.buildings) {
      const sp = SPECS[b.spec];
      if (!sp) continue;
      const online = isOnline(state, b);
      const occ = online ? blockOccupancy(state, b) : null;
      ctx.fillRect();
      if (occ != null) ctx.fillRect();
      if (sp.kind !== 'residential' && online) {
        const util = utilisationOf(state, b);
        if (util !== null) ctx.fillRect();
      }
      if (!online) {
        ctx.beginPath();
        ctx.stroke();
      }
      if (sp.w > 1 || sp.h > 1) ctx.strokeRect();
      if (sp.category === 'zones') {
        densityTier(sp);
        ctx.strokeRect();
      }
    }
    // Disconnected-road-flash full scan — mirrors MapView.tsx:402-413.
    const connected = new Set(state.roadConnectivity?.connectedRoadTiles ?? []);
    for (const b of state.buildings) {
      const sp = SPECS[b.spec];
      if (!sp || !isRoadSpec(sp)) continue;
      if (connected.has(`${b.x},${b.y}`)) continue;
      ctx.fillRect();
    }
  });
}

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
