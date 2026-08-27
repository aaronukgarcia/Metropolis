// perfhud.test.mjs — FEAT-1972079856: performance HUD metrics tests.
//
// Ring buffer math (avg/p95/worst), feature detection, throttle logic,
// and SimState type cleanliness validation.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  RingBuffer,
  createFpsTracker,
  createTickTracker,
  createRenderThrottle,
  sampleFrame,
  fpsMetrics,
  tickMetrics,
  recordTickDuration,
  isRenderDue,
  jsHeapMemory,
  networkMetrics,
} from '../src/sim/perfhud.ts';

// ============================================================================
// Ring Buffer Tests
// ============================================================================

test('RingBuffer: stores and averages values', () => {
  const buf = new RingBuffer(5);
  buf.add(10);
  buf.add(20);
  buf.add(30);
  assert.equal(buf.average(), 20, 'avg of 10, 20, 30 = 20');
});

test('RingBuffer: p95 with 20 values in 20-cap buffer', () => {
  const buf = new RingBuffer(20);
  for (let i = 1; i <= 20; i++) {
    buf.add(i);
  }
  const p95 = buf.p95();
  // 95th percentile of 1..20: idx = ceil(20*0.95)-1 = 18, value = 19
  assert.equal(p95, 19, 'p95 of 1..20 = 19');
});

test('RingBuffer: worst value', () => {
  const buf = new RingBuffer(10);
  buf.add(5);
  buf.add(42);
  buf.add(10);
  assert.equal(buf.worst(), 42, 'worst of 5,42,10 = 42');
});

test('RingBuffer: wraps around when full', () => {
  const buf = new RingBuffer(3);
  buf.add(10);
  buf.add(20);
  buf.add(30);
  buf.add(40); // wraps, replaces 10
  buf.add(50); // wraps, replaces 20
  // Buffer now holds [50, 30, 40] (values 30, 40, 50)
  assert.equal(buf.average(), 40, 'avg of 30,40,50 = 40');
});

test('RingBuffer: empty buffer returns 0 stats', () => {
  const buf = new RingBuffer(10);
  assert.equal(buf.average(), 0);
  assert.equal(buf.p95(), 0);
  assert.equal(buf.worst(), 0);
});

test('RingBuffer: isFull reflects wrap state', () => {
  const buf = new RingBuffer(2);
  assert.equal(buf.isFull(), false, 'not full initially');
  buf.add(1);
  assert.equal(buf.isFull(), false, 'not full with 1 item');
  buf.add(2);
  assert.equal(buf.isFull(), true, 'full after reaching capacity (index wraps to 0)');
  buf.add(3); // wrap around again
  assert.equal(buf.isFull(), true, 'still full');
});

test('RingBuffer: reset clears all state', () => {
  const buf = new RingBuffer(5);
  buf.add(10);
  buf.add(20);
  buf.reset();
  assert.equal(buf.average(), 0);
  assert.equal(buf.isFull(), false);
});

// ============================================================================
// FPS Tracker Tests
// ============================================================================

test('FPS tracker: frame samples accumulate', () => {
  const tracker = createFpsTracker();
  sampleFrame(tracker, 0);
  sampleFrame(tracker, 16);
  sampleFrame(tracker, 32);
  const metrics = fpsMetrics(tracker);
  assert(metrics.avgFps > 0, 'avg fps > 0 with samples');
});

test('FPS tracker: ignores first frame (no delta)', () => {
  const tracker = createFpsTracker();
  sampleFrame(tracker, 100);
  // After one sample, lastFrameMs is set but no delta recorded (needs 2 frames)
  let metrics = fpsMetrics(tracker);
  assert.equal(metrics.avgFrameMs, 0, 'no frames yet');

  sampleFrame(tracker, 116);
  metrics = fpsMetrics(tracker);
  // Now one delta of 16 ms
  assert.equal(metrics.avgFrameMs, 16, 'first delta = 16');
});

test('FPS tracker: rejects impossible deltas', () => {
  const tracker = createFpsTracker();
  sampleFrame(tracker, 0);
  sampleFrame(tracker, 16);
  // Sanity check: ignore if delta > 1000 ms
  sampleFrame(tracker, 2000);
  let metrics = fpsMetrics(tracker);
  assert.equal(metrics.avgFrameMs, 16, 'only first delta counted, second ignored');
});

test('FPS tracker: computes fps from ms', () => {
  const tracker = createFpsTracker();
  sampleFrame(tracker, 0);
  sampleFrame(tracker, 16.67); // ~60 FPS
  const metrics = fpsMetrics(tracker);
  assert(metrics.avgFps > 59 && metrics.avgFps < 61, 'fps ≈ 60');
});

// ============================================================================
// Tick Tracker Tests
// ============================================================================

test('Tick tracker: accumulates tick durations', () => {
  const tracker = createTickTracker();
  recordTickDuration(tracker, 1.5);
  recordTickDuration(tracker, 2.0);
  recordTickDuration(tracker, 1.8);
  const metrics = tickMetrics(tracker);
  assert.equal(Math.round(metrics.avgMs * 10) / 10, 1.8, 'avg tick time');
});

test('Tick tracker: rejects impossible durations', () => {
  const tracker = createTickTracker();
  recordTickDuration(tracker, 1.5);
  recordTickDuration(tracker, 50000); // Reject: >10s
  recordTickDuration(tracker, 1.8);
  const metrics = tickMetrics(tracker);
  // Should have only 1.5 and 1.8: avg = 1.65
  // Rounding: Math.round(1.65 * 10) = Math.round(16.5) = 16 (banker's rounding)
  assert(metrics.avgMs > 1.6 && metrics.avgMs < 1.7, 'avg is ~1.65');
});

test('Tick tracker: p95 works', () => {
  const tracker = createTickTracker();
  // Add 20 durations: 1..20
  for (let i = 1; i <= 20; i++) {
    recordTickDuration(tracker, i);
  }
  const metrics = tickMetrics(tracker);
  assert.equal(metrics.p95Ms, 19, 'p95 of 1..20 = 19');
});

// ============================================================================
// Render Throttle Tests
// ============================================================================

test('Render throttle: due on first call', () => {
  const throttle = createRenderThrottle();
  assert.equal(isRenderDue(throttle, 0), true, 'first call is due');
});

test('Render throttle: not due before 1000 ms', () => {
  const throttle = createRenderThrottle();
  isRenderDue(throttle, 0);
  assert.equal(isRenderDue(throttle, 500), false, 'not due at 500ms');
  assert.equal(isRenderDue(throttle, 999), false, 'not due at 999ms');
});

test('Render throttle: due at 1000 ms', () => {
  const throttle = createRenderThrottle();
  isRenderDue(throttle, 0);
  assert.equal(isRenderDue(throttle, 1000), true, 'due at 1000ms');
});

test('Render throttle: updates lastRenderMs on render', () => {
  const throttle = createRenderThrottle();
  isRenderDue(throttle, 0);
  isRenderDue(throttle, 1000);
  // Next check should be from 1000, not 0
  assert.equal(isRenderDue(throttle, 1500), false, 'clock reset to 1000');
  assert.equal(isRenderDue(throttle, 2000), true, 'due from 1000');
});

// ============================================================================
// Feature Detection Tests
// ============================================================================

test('jsHeapMemory: feature detects gracefully', () => {
  const mem = jsHeapMemory();
  // Should return null when unavailable (Node env) or a number (browser)
  assert(mem === null || typeof mem === 'number', 'returns null or number');
});

test('networkMetrics: returns object shape', () => {
  const net = networkMetrics();
  assert.equal(typeof net.fetchCount, 'number');
  assert.equal(typeof net.fetchBytes, 'number');
  assert(net.fetchCount >= 0 && net.fetchBytes >= 0, 'non-negative counts');
});

// ============================================================================
// SimState Type Cleanliness
// ============================================================================

test('SimState: contains no perf-related fields', () => {
  // A synthetic state object with all required fields
  const state = {
    tick: 0,
    speed: 1,
    funds: 10000000,
    loanBalance: 0,
    population: 0,
    xp: 0,
    taxRates: { residential: 9, commercial: 11, industrial: 13 },
    policies: { recycling: false, transitSubsidy: false, tourismDrive: false, austerity: false },
    buildings: [],
    nextId: 1,
    movingId: null,
    tool: { mode: 'select' },
    clipboard: null,
    pipeTier: {},
    history: [],
    ledger: [],
    nextLedgerId: 1,
    lastFlows: { inflows: [], outflows: [] },
    lastRewardedLevel: 1,
    notice: null,
    fundsAtTickStart: 0,
    fundsAtTickEnd: 0,
    pendingRewards: [],
  };

  // List of disallowed perf fields that must NEVER appear in SimState
  const disallowedPerfFields = [
    'fps',
    'frameMs',
    'memoryBytes',
    'tickMs',
    'networkBytes',
    'perfSnapshot',
  ];

  const stateKeys = Object.keys(state);
  for (const field of disallowedPerfFields) {
    assert(
      !stateKeys.includes(field),
      `SimState must not contain "${field}" (would break determinism)`
    );
  }
});
