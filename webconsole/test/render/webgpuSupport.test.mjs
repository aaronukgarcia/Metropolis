// webgpuSupport.test.mjs — FEAT-2326609760 GPU acceleration spike, Phase 1.
//
// Feature-detect fallback path: every way WebGPU acquisition can fail to
// give the caller a real device must resolve (never throw) to
// `{ supported: false, reason }` — that reason is the "recorded reason" the
// plan's Phase 1 goal names explicitly ("navigator.gpu feature-detect ->
// Canvas2D fallback with a recorded reason").

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { acquireWebGPU, watchForContextLoss } from '../../src/render/webgpuSupport.ts';

test('acquireWebGPU: no gpu object at all -> unsupported with a named reason', async () => {
  const result = await acquireWebGPU(undefined);
  assert.equal(result.supported, false);
  assert.match(result.reason, /navigator\.gpu is undefined/);
});

test('acquireWebGPU: gpu.requestAdapter() resolves null -> unsupported with a named reason', async () => {
  const fakeGpu = { requestAdapter: async () => null };
  const result = await acquireWebGPU(fakeGpu);
  assert.equal(result.supported, false);
  assert.match(result.reason, /requestAdapter/);
});

test('acquireWebGPU: adapter.requestDevice() resolves null -> unsupported with a named reason', async () => {
  const fakeGpu = { requestAdapter: async () => ({ requestDevice: async () => null }) };
  const result = await acquireWebGPU(fakeGpu);
  assert.equal(result.supported, false);
  assert.match(result.reason, /requestDevice/);
});

test('acquireWebGPU: requestAdapter() throwing -> caught and reported, never propagates', async () => {
  const fakeGpu = {
    requestAdapter: async () => {
      throw new Error('driver exploded');
    },
  };
  const result = await acquireWebGPU(fakeGpu);
  assert.equal(result.supported, false);
  assert.match(result.reason, /driver exploded/);
});

test('acquireWebGPU: full happy path returns supported:true with device/adapter/format', async () => {
  const fakeDevice = { lost: new Promise(() => {}) };
  const fakeAdapter = { requestDevice: async () => fakeDevice };
  const fakeGpu = { requestAdapter: async () => fakeAdapter, getPreferredCanvasFormat: () => 'rgba8unorm' };
  const result = await acquireWebGPU(fakeGpu);
  assert.equal(result.supported, true);
  if (result.supported) {
    assert.equal(result.device, fakeDevice);
    assert.equal(result.format, 'rgba8unorm');
  }
});

test('acquireWebGPU: missing getPreferredCanvasFormat falls back to a sane default format string', async () => {
  const fakeDevice = { lost: new Promise(() => {}) };
  const fakeGpu = { requestAdapter: async () => ({ requestDevice: async () => fakeDevice }) };
  const result = await acquireWebGPU(fakeGpu);
  assert.equal(result.supported, true);
  if (result.supported) assert.equal(typeof result.format, 'string');
});

test('watchForContextLoss: device.lost resolving calls onLost with reason + message', async () => {
  let captured = null;
  const device = { lost: Promise.resolve({ reason: 'destroyed', message: 'app called destroy()' }) };
  watchForContextLoss(device, (reason, message) => {
    captured = { reason, message };
  });
  // Let the microtask queue drain.
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.deepEqual(captured, { reason: 'destroyed', message: 'app called destroy()' });
});

test('watchForContextLoss: a throwing onLost handler is swallowed, never an unhandled rejection', async () => {
  const device = { lost: Promise.resolve({ reason: 'destroyed', message: 'x' }) };
  assert.doesNotThrow(() => {
    watchForContextLoss(device, () => {
      throw new Error('handler bug');
    });
  });
  await new Promise((resolve) => setTimeout(resolve, 0));
});
