// mapRenderer.test.mjs — FEAT-2326609760 GPU acceleration spike, Phase 1.
//
// Exercises the three load-bearing behaviours the brief asks for directly:
//   1. dirty-upload only on identity change — a tick with unchanged buildings
//      must cost ZERO instance-buffer uploads, not "cheap" uploads.
//   2. feature-detect fallback path (covered lightly here; webgpuSupport.test.mjs
//      owns acquisition itself — this file proves MapRenderer.init() actually
//      WIRES that result into `mode`/`fallbackReason`).
//   3. context-loss handling — device.lost must flip the renderer to Canvas2D
//      IN PLACE (never blanking the map — a render() call right after loss
//      must still draw something).
//
// All three tests use a minimal MOCK GPUDevice (writeBuffer/createBuffer
// counters) — this is jsdom, there is no real WebGPU here; the plan is
// explicit that real-GPU numbers come from Aaron's own machine later.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildScaleFixture } from '../scale/fixture.mjs';
import { reducer } from '../../src/sim/engine.ts';
import { MapRenderer } from '../../src/render/mapRenderer.ts';

function makeFakePipelineParts() {
  const pass = {
    setPipeline() {},
    setBindGroup() {},
    setVertexBuffer() {},
    draw() {},
    end() {},
  };
  const encoder = {
    beginRenderPass: () => pass,
    finish: () => ({}),
  };
  return { pass, encoder };
}

/** A mock GPUDevice that counts every buffer creation and every writeBuffer
 * call, split by which logical buffer it targeted (identified by the
 * `label` passed to createBuffer, tracked by returning a tagged object). */
function makeMockDevice() {
  const counts = { buffersCreated: 0, staticWrites: 0, dynamicWrites: 0, otherWrites: 0, submits: 0 };
  const { encoder } = makeFakePipelineParts();
  const device = {
    lost: new Promise(() => {}), // never resolves in this mock unless replaced per-test
    createBuffer(desc) {
      counts.buffersCreated++;
      return { __label: desc.label ?? 'unlabelled' };
    },
    createShaderModule() {
      return {};
    },
    createRenderPipeline() {
      return { getBindGroupLayout: () => ({}) };
    },
    createBindGroup() {
      return {};
    },
    createCommandEncoder: () => encoder,
    queue: {
      writeBuffer(buffer, _offset, _data) {
        const label = buffer && typeof buffer === 'object' ? buffer.__label : 'unknown';
        if (label === 'metropolis-instance-static') counts.staticWrites++;
        else if (label === 'metropolis-instance-dynamic') counts.dynamicWrites++;
        else counts.otherWrites++;
      },
      submit() {
        counts.submits++;
      },
    },
    destroy() {},
  };
  return { device, counts };
}

function fakeCanvas() {
  return { getContext: () => null, width: 800, height: 600 };
}

test('MapRenderer.init(): supported device -> mode webgpu, no fallback reason', async () => {
  const { device } = makeMockDevice();
  const renderer = new MapRenderer(fakeCanvas());
  await renderer.init({ device, format: 'bgra8unorm' });
  assert.equal(renderer.mode, 'webgpu');
  assert.equal(renderer.fallbackReason, null);
});

test('MapRenderer.init(): no navigator.gpu injected and no device -> falls back to canvas2d with a reason', async () => {
  const renderer = new MapRenderer(fakeCanvas());
  await renderer.init({ gpu: undefined });
  assert.equal(renderer.mode, 'canvas2d');
  assert.match(renderer.fallbackReason ?? '', /navigator\.gpu is undefined/);
});

test('dirty-upload: first render of a new state does exactly ONE static + ONE dynamic upload per batch', async () => {
  const { device, counts } = makeMockDevice();
  const renderer = new MapRenderer(fakeCanvas());
  await renderer.init({ device });
  // init() itself allocates the camera + viewport uniform buffers (2) — capture
  // that baseline before render() so this test isolates the RENDER-time cost.
  const buffersAfterInit = counts.buffersCreated;
  const state = buildScaleFixture({ buildingCount: 200, targetPopulation: 5000 });

  renderer.render(state, { s: 1, ox: 0, oy: 0 });

  // Two batches (building + road) -> 2 static uploads, 2 dynamic uploads.
  assert.equal(counts.staticWrites, 2, 'first render must upload STATIC data for both batches');
  assert.equal(counts.dynamicWrites, 2, 'first render must upload DYNAMIC data for both batches');
  assert.equal(
    counts.buffersCreated - buffersAfterInit,
    4,
    'first render must allocate static+dynamic instance buffers for both batches (building + road)'
  );
});

test('dirty-upload: re-rendering the SAME state object costs ZERO instance-buffer uploads', async () => {
  const { device, counts } = makeMockDevice();
  const renderer = new MapRenderer(fakeCanvas());
  await renderer.init({ device });
  const state = buildScaleFixture({ buildingCount: 200, targetPopulation: 5000 });

  renderer.render(state, { s: 1, ox: 0, oy: 0 });
  const afterFirst = { ...counts };
  renderer.render(state, { s: 1.5, ox: 10, oy: 20 }); // pan/zoom changed, state did not

  assert.equal(counts.staticWrites, afterFirst.staticWrites, 'unchanged state must not re-upload STATIC data');
  assert.equal(counts.dynamicWrites, afterFirst.dynamicWrites, 'unchanged state must not re-upload DYNAMIC data');
  // The camera/viewport uniforms ARE expected to update every frame (cheap,
  // and pan/zoom is independent of `state`) — those are the "otherWrites".
  assert.ok(counts.otherWrites > 0, 'camera uniform should still refresh even when state is unchanged');
});

test('dirty-upload: a tick with UNCHANGED buildings re-uploads DYNAMIC only, never STATIC', async () => {
  const { device, counts } = makeMockDevice();
  const renderer = new MapRenderer(fakeCanvas());
  await renderer.init({ device });
  const state0 = buildScaleFixture({ buildingCount: 200, targetPopulation: 5000 });

  renderer.render(state0, { s: 1, ox: 0, oy: 0 });
  const staticAfterFirst = counts.staticWrites;
  const dynamicAfterFirst = counts.dynamicWrites;

  const state1 = reducer(state0, { type: 'tick' });
  assert.equal(state1.buildings, state0.buildings, 'a plain tick must not replace the buildings array reference');
  assert.notEqual(state1, state0, 'a plain tick must still produce a new top-level state object');

  renderer.render(state1, { s: 1, ox: 0, oy: 0 });

  assert.equal(counts.staticWrites, staticAfterFirst, 'a tick with unchanged buildings must NOT re-upload STATIC');
  assert.ok(counts.dynamicWrites > dynamicAfterFirst, 'a tick must re-upload DYNAMIC (occupancy/utilisation may have moved)');
});

test('dirty-upload: placing a new building (buildings identity changes) triggers a full re-upload', async () => {
  const { device, counts } = makeMockDevice();
  const renderer = new MapRenderer(fakeCanvas());
  await renderer.init({ device });
  const state0 = buildScaleFixture({ buildingCount: 200, targetPopulation: 5000 });

  renderer.render(state0, { s: 1, ox: 0, oy: 0 });
  const staticAfterFirst = counts.staticWrites;

  const state1 = { ...state0, buildings: [...state0.buildings] }; // new array identity, same content
  assert.notEqual(state1.buildings, state0.buildings);

  renderer.render(state1, { s: 1, ox: 0, oy: 0 });

  assert.ok(counts.staticWrites > staticAfterFirst, 'a buildings-identity change must trigger a STATIC re-upload');
});

test('context-loss: device.lost resolving flips the renderer to canvas2d WITHOUT throwing, and render() still draws', async () => {
  let resolveLost;
  const lostPromise = new Promise((resolve) => {
    resolveLost = resolve;
  });
  const { device } = makeMockDevice();
  device.lost = lostPromise;

  const calls = { fillRect: 0, clearRect: 0 };
  const ctx2d = {
    clearRect: () => calls.clearRect++,
    fillRect: () => calls.fillRect++,
    set globalAlpha(_v) {},
    set fillStyle(_v) {},
  };
  const canvas = { getContext: (kind) => (kind === '2d' ? ctx2d : null), width: 800, height: 600 };

  const renderer = new MapRenderer(canvas);
  await renderer.init({ device });
  assert.equal(renderer.mode, 'webgpu');

  const state = buildScaleFixture({ buildingCount: 50, targetPopulation: 1000 });
  renderer.render(state, { s: 1, ox: 0, oy: 0 });

  resolveLost({ reason: 'destroyed', message: 'simulated driver crash' });
  await new Promise((resolve) => setTimeout(resolve, 0)); // let the .then() callback run

  assert.equal(renderer.mode, 'canvas2d', 'device loss must flip mode to canvas2d');
  assert.match(renderer.fallbackReason ?? '', /device lost/);

  // The map must NOT be blanked: the very next render() call must still
  // produce real draw calls via the Canvas2D fallback (GR#27's spirit
  // applied to a rendering path: a lost context is not a silent black map).
  renderer.render(state, { s: 1, ox: 0, oy: 0 });
  assert.ok(calls.clearRect > 0, 'fallback render must still clear+repaint the canvas');
  assert.ok(calls.fillRect > 0, 'fallback render must still draw buildings after context loss');
});
