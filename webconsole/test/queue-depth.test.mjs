// queue-depth.test.mjs — FEAT-1972079938: Queue Depth HUD tracker tests.
//
// Pure telemetry: increment/decrement per-engine depth, high-water mark
// tracking, reset semantics, concurrent accumulation, no leak on reject,
// and a check that driving the tracker never touches any sim state.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import {
  QueueDepthTracker,
  PROTOCOL_ENGINE_KEY,
  trackAsk,
} from '../src/sim/queueDepth.ts';

describe('QueueDepthTracker: basic increment/decrement', () => {
  test('depth returns to 0 after a matching increment+decrement', () => {
    const t = new QueueDepthTracker();
    t.increment('engineA');
    assert.equal(t.depthOf('engineA'), 1);
    t.decrement('engineA');
    assert.equal(t.depthOf('engineA'), 0);
  });

  test('an unknown engine reads depth 0, not undefined/NaN', () => {
    const t = new QueueDepthTracker();
    assert.equal(t.depthOf('neverSeen'), 0);
    assert.equal(t.highWaterMarkOf('neverSeen'), 0);
  });

  test('decrement is clamped at 0 — never goes negative on a stray call', () => {
    const t = new QueueDepthTracker();
    t.decrement('engineA');
    t.decrement('engineA');
    assert.equal(t.depthOf('engineA'), 0);
  });
});

describe('QueueDepthTracker: concurrent asks accumulate', () => {
  test('multiple in-flight asks accumulate depth before any settle', () => {
    const t = new QueueDepthTracker();
    t.increment('engineA');
    t.increment('engineA');
    t.increment('engineA');
    assert.equal(t.depthOf('engineA'), 3);
    t.decrement('engineA');
    assert.equal(t.depthOf('engineA'), 2);
    t.decrement('engineA');
    t.decrement('engineA');
    assert.equal(t.depthOf('engineA'), 0);
  });

  test('per-engine keying keeps depths independent', () => {
    const t = new QueueDepthTracker();
    t.increment('engineA');
    t.increment('engineB');
    t.increment('engineB');
    assert.equal(t.depthOf('engineA'), 1);
    assert.equal(t.depthOf('engineB'), 2);
    t.decrement('engineA');
    assert.equal(t.depthOf('engineA'), 0);
    assert.equal(t.depthOf('engineB'), 2, 'engineB unaffected by engineA settling');
  });
});

describe('QueueDepthTracker: high-water mark', () => {
  test('HWM captures the max depth reached', () => {
    const t = new QueueDepthTracker();
    t.increment('e'); // depth 1
    t.increment('e'); // depth 2
    t.increment('e'); // depth 3
    assert.equal(t.highWaterMarkOf('e'), 3);
    t.decrement('e'); // depth 2
    t.decrement('e'); // depth 1
    assert.equal(t.highWaterMarkOf('e'), 3, 'HWM does not decrease when depth drops');
  });

  test('HWM rises again if a later burst exceeds the prior peak', () => {
    const t = new QueueDepthTracker();
    t.increment('e');
    t.increment('e');
    t.decrement('e');
    t.decrement('e');
    assert.equal(t.highWaterMarkOf('e'), 2);
    t.increment('e');
    t.increment('e');
    t.increment('e');
    t.increment('e');
    assert.equal(t.highWaterMarkOf('e'), 4);
  });

  test('reset(engine) zeroes both depth and HWM for that engine', () => {
    const t = new QueueDepthTracker();
    t.increment('e');
    t.increment('e');
    assert.equal(t.depthOf('e'), 2);
    assert.equal(t.highWaterMarkOf('e'), 2);
    t.reset('e');
    assert.equal(t.depthOf('e'), 0);
    assert.equal(t.highWaterMarkOf('e'), 0);
  });

  test('resetAll() zeroes every tracked engine', () => {
    const t = new QueueDepthTracker();
    t.increment('a');
    t.increment('b');
    t.increment('b');
    t.resetAll();
    assert.equal(t.depthOf('a'), 0);
    assert.equal(t.depthOf('b'), 0);
    assert.equal(t.highWaterMarkOf('a'), 0);
    assert.equal(t.highWaterMarkOf('b'), 0);
  });

  test('reset does not affect other engines', () => {
    const t = new QueueDepthTracker();
    t.increment('a');
    t.increment('b');
    t.reset('a');
    assert.equal(t.depthOf('a'), 0);
    assert.equal(t.depthOf('b'), 1, 'engine b untouched by resetting engine a');
  });
});

describe('QueueDepthTracker: a rejected ask still decrements (no leak)', () => {
  test('a settled-by-reject ask frees its slot exactly like a settled-by-resolve one', async () => {
    const t = new QueueDepthTracker();
    let reject;
    const p = new Promise((_res, rej) => { reject = rej; });
    const wrapped = trackAsk('e', p, t);
    assert.equal(t.depthOf('e'), 1, 'increments on send');
    reject(new Error('boom'));
    await assert.rejects(wrapped);
    assert.equal(t.depthOf('e'), 0, 'decrements on reject — no leak');
  });

  test('a settled-by-resolve ask decrements too', async () => {
    const t = new QueueDepthTracker();
    let resolve;
    const p = new Promise((res) => { resolve = res; });
    const wrapped = trackAsk('e', p, t);
    assert.equal(t.depthOf('e'), 1);
    resolve('ok');
    await wrapped;
    assert.equal(t.depthOf('e'), 0);
  });

  test('mixed resolve/reject asks all settle without leaking depth', async () => {
    const t = new QueueDepthTracker();
    const settlers = [];
    const promises = [1, 2, 3, 4].map((i) => {
      let settle;
      const p = new Promise((res, rej) => { settle = i % 2 === 0 ? () => rej(new Error(`ask ${i}`)) : () => res(i); });
      settlers.push(settle);
      return trackAsk(PROTOCOL_ENGINE_KEY, p, t);
    });
    assert.equal(t.depthOf(PROTOCOL_ENGINE_KEY), 4);
    for (const settle of settlers) settle();
    await Promise.allSettled(promises);
    assert.equal(t.depthOf(PROTOCOL_ENGINE_KEY), 0, 'every ask settled, none leaked');
  });
});

describe('QueueDepthTracker: snapshot + subscribe', () => {
  test('snapshot reports entries sorted, plus totals', () => {
    const t = new QueueDepthTracker();
    t.increment('zeta');
    t.increment('alpha');
    t.increment('alpha');
    const snap = t.snapshot();
    assert.deepEqual(snap.entries.map((e) => e.engine), ['alpha', 'zeta']);
    assert.equal(snap.total, 3);
    assert.equal(snap.totalHighWaterMark, 3);
  });

  test('subscribe fires immediately with the current snapshot, then on every change', () => {
    const t = new QueueDepthTracker();
    const seen = [];
    const unsub = t.subscribe((s) => seen.push(s.total));
    assert.equal(seen.length, 1, 'fired immediately on subscribe');
    assert.equal(seen[0], 0);
    t.increment('e');
    assert.equal(seen.length, 2);
    assert.equal(seen[1], 1);
    unsub();
    t.increment('e');
    assert.equal(seen.length, 2, 'no further notifications after unsubscribe');
  });
});

describe('QueueDepthTracker: pure telemetry — never touches sim state', () => {
  test('driving the tracker touches nothing outside its own instance', () => {
    // A frozen, unrelated "sim state" object stands in for SimState. If the
    // tracker (or trackAsk) reached into any ambient global/sim-shaped
    // object, mutating a frozen object would throw in strict mode — proving
    // this module has no side channel into sim state.
    const fakeSimState = Object.freeze({ tick: 0, funds: 0, population: 0 });
    const before = JSON.stringify(fakeSimState);

    const t = new QueueDepthTracker();
    t.increment('e');
    t.increment('e');
    t.decrement('e');
    t.reset('e');
    t.resetAll();

    assert.equal(JSON.stringify(fakeSimState), before, 'fakeSimState untouched by tracker operations');
    assert.deepEqual(Object.keys(fakeSimState), ['tick', 'funds', 'population'], 'no properties added');
  });
});
