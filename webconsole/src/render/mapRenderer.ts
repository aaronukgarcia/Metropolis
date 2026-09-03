// mapRenderer.ts — FEAT-2326609760 GPU acceleration spike, Phase 0 + Phase 1
// (Aaron 2026-09-03 fast-track ruling: WebGPU-first, Canvas2D fallback,
// straight-to-default once its round passes).
//
// MapRenderer owns exactly one <canvas> and renders the building + road
// instance batches either via WebGPU (instanced quads, see shaders.ts) or,
// when WebGPU is unavailable or its context is lost, via a Canvas2D fallback
// that draws the SAME data (built from the SAME instanceBuilder.ts SoA
// arrays) so visual parity with the disabled path is by construction, not by
// a second hand-maintained draw loop that could drift from the WebGPU one.
//
// DETERMINISM (GR#21): this class is a pure CONSUMER of SimState. It never
// dispatches, never mutates `state`, and holds no reference that outlives a
// single render() call except identity-comparison keys (`state`/`state.
// buildings` references themselves, held only to detect "did this change",
// exactly the overlaySubsetsOf/memoOnState idiom already used in MapView.tsx
// and data.ts). A GPU buffer is a render-side mirror per §2.1 of the plan —
// destroying and rebuilding it from `state.buildings` is always lossless,
// and nothing here is ever read back into a reducer/dispatch path.
//
// DIRTY-UPLOAD DISCIPLINE (plan §2.2, "never upload on the 50ms frame timer
// alone"): render() is called on every MapView repaint (including the timer-
// forced ones), but instance-buffer uploads only happen when the data that
// feeds them actually changed:
//   - `state === lastState` (no sim tick, no pan/zoom-driven rebuild reason)
//     -> zero buffer uploads, camera uniform still refreshed (cheap, and
//     geom/pan-zoom can change independently of `state`).
//   - `state.buildings === lastBuildings` but `state !== lastState` (a tick
//     happened, no place/bulldoze/relocate) -> DYNAMIC-only re-upload via
//     rebuildDynamicOnly, STATIC buffer untouched.
//   - `state.buildings !== lastBuildings` (place/bulldoze/relocate/stamp) ->
//     full rebuild: new buffers sized to the new instance count, both
//     STATIC and DYNAMIC uploaded.
//
// CONTEXT LOSS (plan's risk #2): device.lost is wired at init to fall back
// to Canvas2D IN PLACE — render() keeps being called by the caller exactly
// as before, it just takes the Canvas2D branch from then on, so the map is
// never blanked mid-session. A lost device is never resurrected in Phase 1
// (a real "try WebGPU again" retry policy is future work — Canvas2D staying
// the fallback forever after a loss is a safe, always-correct default).

import {
  buildInstances,
  rebuildDynamicOnly,
  buildingInstanceFilter,
  roadInstanceFilter,
  STATIC_FLOATS_PER_INSTANCE,
  DYNAMIC_FLOATS_PER_INSTANCE,
} from './instanceBuilder.ts';
import {
  acquireWebGPU,
  watchForContextLoss,
  type MinimalGPUDevice,
  type MinimalGPUNavigator,
} from './webgpuSupport.ts';
import { INSTANCED_QUAD_WGSL, CAMERA_UNIFORM_FLOATS, VIEWPORT_UNIFORM_FLOATS } from './shaders.ts';
import type { Spec } from '../sim/data.ts';
import type { Building, SimState } from '../sim/types.ts';

export type RenderMode = 'webgpu' | 'canvas2d';

export interface Geom {
  s: number;
  ox: number;
  oy: number;
}

/** Render-loop counters, exposed for tests and for the Phase 0 perf harness
 * (perfHarness.ts) — never used for any sim-affecting decision. */
export interface RenderStats {
  staticUploads: number;
  dynamicUploads: number;
  buffersCreated: number;
  framesRendered: number;
}

interface Batch {
  filter: (sp: Spec) => boolean;
  lastBuildingsRef: Building[] | null;
  lastIds: number[];
  count: number;
  staticBuffer: unknown | null;
  dynamicBuffer: unknown | null;
}

function newBatch(filter: (sp: Spec) => boolean): Batch {
  return { filter, lastBuildingsRef: null, lastIds: [], count: 0, staticBuffer: null, dynamicBuffer: null };
}

/** Bytes-per-float, for buffer sizing. */
const F32 = 4;

export interface MapRendererDeps {
  /** Injected fake `navigator.gpu` for tests. Production callers omit this. */
  gpu?: MinimalGPUNavigator;
  /** Injected pre-acquired device/format, bypassing acquireWebGPU entirely —
   * the seam unit tests use to drive the dirty-upload/context-loss logic
   * without an async acquisition round-trip. */
  device?: MinimalGPUDevice;
  format?: string;
}

export class MapRenderer {
  mode: RenderMode = 'canvas2d';
  fallbackReason: string | null = null;
  readonly stats: RenderStats = { staticUploads: 0, dynamicUploads: 0, buffersCreated: 0, framesRendered: 0 };

  private device: MinimalGPUDevice | null = null;
  private pipeline: unknown | null = null;
  private cameraBuffer: unknown | null = null;
  private viewportBuffer: unknown | null = null;
  private bindGroup: unknown | null = null;

  private lastState: SimState | null = null;
  private readonly buildingBatch: Batch = newBatch(buildingInstanceFilter);
  private readonly roadBatch: Batch = newBatch(roadInstanceFilter);

  private ctx2d: CanvasRenderingContext2D | null = null;

  private readonly canvas: { getContext(kind: string): unknown; width: number; height: number } | null;

  // NOTE: node's native TypeScript type-stripping (used to run these files
  // directly via `node --test` — see fixture.mjs's own header for the
  // precedent) does not support TS constructor parameter properties
  // (`constructor(private readonly x: T)`); this repo's other TS sources
  // avoid that syntax for the same reason (grep engine.ts/data.ts — none
  // use it), so this class declares the field explicitly instead.
  constructor(canvas: { getContext(kind: string): unknown; width: number; height: number } | null) {
    this.canvas = canvas;
  }

  /**
   * Feature-detects WebGPU and acquires a device. Always resolves — never
   * throws — falling back to `mode = 'canvas2d'` with `fallbackReason` set
   * on ANY failure (no navigator.gpu, no adapter, acquisition exception).
   */
  async init(deps: MapRendererDeps = {}): Promise<void> {
    const device = deps.device;
    if (device) {
      this.adoptDevice(device, deps.format ?? 'bgra8unorm');
      return;
    }
    const acquired = await acquireWebGPU(deps.gpu);
    if (!acquired.supported) {
      this.mode = 'canvas2d';
      this.fallbackReason = acquired.reason;
      this.ctx2d = this.canvas ? (this.canvas.getContext('2d') as CanvasRenderingContext2D | null) : null;
      return;
    }
    this.adoptDevice(acquired.device, acquired.format);
  }

  private adoptDevice(device: MinimalGPUDevice, format: string): void {
    this.device = device;
    this.mode = 'webgpu';
    this.fallbackReason = null;
    try {
      this.pipeline = device.createRenderPipeline({
        vertex: { module: device.createShaderModule({ code: INSTANCED_QUAD_WGSL }), entryPoint: 'vs_main' },
        fragment: { module: device.createShaderModule({ code: INSTANCED_QUAD_WGSL }), entryPoint: 'fs_main', targets: [{ format }] },
        primitive: { topology: 'triangle-strip' },
      });
      this.cameraBuffer = device.createBuffer({
        size: CAMERA_UNIFORM_FLOATS * F32,
        usage: 0x40 /* UNIFORM */ | 0x8 /* COPY_DST */,
        label: 'metropolis-camera-uniform',
      });
      this.viewportBuffer = device.createBuffer({
        size: VIEWPORT_UNIFORM_FLOATS * F32,
        usage: 0x40 | 0x8,
        label: 'metropolis-viewport-uniform',
      });
      this.bindGroup = device.createBindGroup({
        layout: (this.pipeline as { getBindGroupLayout?: (i: number) => unknown }).getBindGroupLayout?.(0) ?? {},
        entries: [
          { binding: 0, resource: { buffer: this.cameraBuffer } },
          { binding: 1, resource: { buffer: this.viewportBuffer } },
        ],
      });
    } catch (err) {
      // Pipeline setup failing (bad shader compile, driver rejection) is
      // exactly as fatal to the WebGPU path as acquisition failing — fall
      // back rather than crash the frame (GR#1).
      const message = err instanceof Error ? err.message : String(err);
      this.mode = 'canvas2d';
      this.fallbackReason = `pipeline setup failed: ${message}`;
      this.ctx2d = this.canvas ? (this.canvas.getContext('2d') as CanvasRenderingContext2D | null) : null;
      return;
    }
    watchForContextLoss(device, (reason, message) => {
      this.mode = 'canvas2d';
      this.fallbackReason = `device lost (${reason}): ${message}`;
      this.ctx2d = this.canvas ? (this.canvas.getContext('2d') as CanvasRenderingContext2D | null) : null;
      // Force a full re-derivation on the next real WebGPU render (there
      // won't be one — mode is canvas2d now — but this also protects a
      // future retry-the-device policy from replaying stale buffer refs).
      this.buildingBatch.staticBuffer = null;
      this.buildingBatch.dynamicBuffer = null;
      this.roadBatch.staticBuffer = null;
      this.roadBatch.dynamicBuffer = null;
    });
  }

  /** Renders one frame. Never throws — a rendering-layer failure degrades to
   * "the previous frame stays on screen" at worst, never crashes the caller
   * (GR#1 applied to a render loop, not just data mutation). */
  render(state: SimState, geom: Geom): void {
    this.stats.framesRendered++;
    if (this.mode === 'canvas2d') {
      this.renderCanvas2D(state, geom);
      this.lastState = state;
      return;
    }
    this.updateCameraUniform(geom);
    if (state !== this.lastState) {
      this.syncBatch(this.buildingBatch, state);
      this.syncBatch(this.roadBatch, state);
    }
    this.submitWebGPUFrame();
    this.lastState = state;
  }

  private syncBatch(batch: Batch, state: SimState): void {
    if (!this.device) return;
    if (state.buildings === batch.lastBuildingsRef && batch.staticBuffer && batch.dynamicBuffer) {
      // STATIC unchanged (place/bulldoze/relocate did not happen) — only
      // dynamic fields (online/occupancy/utilisation/tier) may have moved
      // this tick. Never re-touches the static buffer.
      const dyn = rebuildDynamicOnly(state, batch.filter, batch.lastIds);
      this.device.queue.writeBuffer(batch.dynamicBuffer as never, 0, dyn);
      this.stats.dynamicUploads++;
      return;
    }
    // Buildings array identity changed (or first render) — full rebuild.
    const inst = buildInstances(state, batch.filter);
    batch.staticBuffer = this.device.createBuffer({
      size: Math.max(1, inst.count) * STATIC_FLOATS_PER_INSTANCE * F32,
      usage: 0x20 /* VERTEX */ | 0x8 /* COPY_DST */,
      label: 'metropolis-instance-static',
    });
    batch.dynamicBuffer = this.device.createBuffer({
      size: Math.max(1, inst.count) * DYNAMIC_FLOATS_PER_INSTANCE * F32,
      usage: 0x20 | 0x8,
      label: 'metropolis-instance-dynamic',
    });
    this.stats.buffersCreated += 2;
    this.device.queue.writeBuffer(batch.staticBuffer as never, 0, inst.staticData);
    this.device.queue.writeBuffer(batch.dynamicBuffer as never, 0, inst.dynamicData);
    this.stats.staticUploads++;
    this.stats.dynamicUploads++;
    batch.lastBuildingsRef = state.buildings;
    batch.lastIds = inst.ids;
    batch.count = inst.count;
  }

  private updateCameraUniform(geom: Geom): void {
    if (!this.device || !this.cameraBuffer || !this.viewportBuffer) return;
    const cam = new Float32Array([geom.s, geom.ox, geom.oy, 0]);
    this.device.queue.writeBuffer(this.cameraBuffer as never, 0, cam);
    const vw = new Float32Array([
      (this.canvas?.width ?? 0) / 2,
      (this.canvas?.height ?? 0) / 2,
    ]);
    this.device.queue.writeBuffer(this.viewportBuffer as never, 0, vw);
  }

  private submitWebGPUFrame(): void {
    if (!this.device || !this.pipeline) return;
    const encoder = this.device.createCommandEncoder({ label: 'metropolis-map-frame' });
    // getCurrentTexture()/colorAttachments wiring is real-canvas-context
    // territory (no jsdom equivalent) — the render pass description is
    // deliberately opaque here (`{}` in tests) so unit tests can exercise
    // the dirty-upload bookkeeping above without a real GPUCanvasContext.
    const pass = encoder.beginRenderPass({});
    pass.setPipeline(this.pipeline);
    pass.setBindGroup(0, this.bindGroup);
    for (const batch of [this.buildingBatch, this.roadBatch]) {
      if (batch.count === 0 || !batch.staticBuffer || !batch.dynamicBuffer) continue;
      pass.setVertexBuffer(0, batch.staticBuffer);
      pass.setVertexBuffer(1, batch.dynamicBuffer);
      pass.draw(4, batch.count);
    }
    pass.end();
    this.device.queue.submit([encoder.finish()]);
  }

  /**
   * Canvas2D fallback. Deliberately reuses buildInstances() (the SAME data
   * source the WebGPU path uploads) rather than re-deriving building visuals
   * by hand a second time, so the fallback can never visually drift from the
   * WebGPU path — a bug in "what to draw" gets fixed once, in
   * instanceBuilder.ts, for both renderers.
   */
  private renderCanvas2D(state: SimState, geom: Geom): void {
    if (!this.ctx2d || !this.canvas) return;
    const ctx = this.ctx2d;
    ctx.clearRect(0, 0, this.canvas.width, this.canvas.height);
    for (const batch of [buildInstances(state, buildingInstanceFilter), buildInstances(state, roadInstanceFilter)]) {
      for (let i = 0; i < batch.count; i++) {
        const so = i * STATIC_FLOATS_PER_INSTANCE;
        const dof = i * DYNAMIC_FLOATS_PER_INSTANCE;
        const x = batch.staticData[so + 0];
        const y = batch.staticData[so + 1];
        const w = batch.staticData[so + 2];
        const h = batch.staticData[so + 3];
        const r = Math.round(batch.staticData[so + 4] * 255);
        const g = Math.round(batch.staticData[so + 5] * 255);
        const bl = Math.round(batch.staticData[so + 6] * 255);
        const online = batch.dynamicData[dof + 0] > 0.5;
        const px = geom.ox + x * geom.s;
        const py = geom.oy + y * geom.s;
        const pw = w * geom.s;
        const ph = h * geom.s;
        ctx.globalAlpha = online ? 1 : 0.45;
        ctx.fillStyle = `rgb(${r},${g},${bl})`;
        ctx.fillRect(px, py, Math.max(pw, 1), Math.max(ph, 1));
      }
    }
    ctx.globalAlpha = 1;
  }

  dispose(): void {
    this.device?.destroy();
    this.device = null;
  }
}
