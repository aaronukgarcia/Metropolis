// engineLag.test.mjs — BUG-618 (P1): unit tests for the ENGINE LAG GAUGE
// tracker (src/sim/engineLag.ts).
//
// Covers: backlog arithmetic (scheduled/completed/decay/clamp), ratio
// classification bands, stall recording + time-limited display window, the
// overall engineLagClassOf combinator, and subscribe/reset semantics. All
// timestamps are caller-supplied fabricated numbers (no real timers), per
// the module's own "testable with fabricated timestamps" design goal.
//
// RED-PROOF discipline (GR#24 — no git checkout/restore/reset used to
// verify): each assertion below was hand-verified to actually fail by
// temporarily mutating a scratch copy of engineLag.ts (e.g. flipping
// Math.max(0, ...) to a bare subtraction, or classifyRatio's `<=` to `<`)
// under `cp engineLag.ts engineLag.ts.scratch; ...edit scratch...; run test
// against scratch; mv back` and observing the expected assertion fail number
// change or a class flip at the boundary that this suite pins.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  EngineLagTracker,
  engineLagClassOf,
  RATIO_AMBER,
  RATIO_RED,
  BACKLOG_AMBER,
  BACKLOG_RED,
  STALL_THRESHOLD_MS,
  STALL_DISPLAY_MS,
} from '../src/sim/engineLag.ts';

// ============================================================================
// Backlog arithmetic
// ============================================================================

test('backlog is 0 before any scheduled/completed calls', () => {
  const t = new EngineLagTracker();
  const snap = t.snapshot(0);
  assert.equal(snap.ticksScheduled, 0);
  assert.equal(snap.ticksCompleted, 0);
  assert.equal(snap.backlog, 0);
  assert.equal(snap.backlogClass, 'green');
});

test('backlog = scheduled - completed while the engine falls behind', () => {
  const t = new EngineLagTracker();
  t.recordTickScheduled();
  t.recordTickScheduled();
  t.recordTickScheduled();
  t.recordTickCompleted();
  const snap = t.snapshot(0);
  assert.equal(snap.ticksScheduled, 3);
  assert.equal(snap.ticksCompleted, 1);
  assert.equal(snap.backlog, 2);
});

test('backlog decays back to 0 as completions catch up to schedule fires', () => {
  const t = new EngineLagTracker();
  for (let i = 0; i < 5; i++) t.recordTickScheduled();
  assert.equal(t.snapshot(0).backlog, 5, 'fully behind after 5 scheduled, 0 completed');
  for (let i = 0; i < 5; i++) t.recordTickCompleted();
  assert.equal(t.snapshot(0).backlog, 0, 'fully caught up once completed == scheduled');
});

test('backlog never goes negative — an extra completed (forced sync tick) clamps at 0', () => {
  const t = new EngineLagTracker();
  t.recordTickScheduled();
  t.recordTickCompleted();
  t.recordTickCompleted(); // e.g. guardedDispatch's K-supersede forced-sync escape
  assert.equal(t.snapshot(0).backlog, 0, 'completed > scheduled must clamp at 0, never read negative');
});

test('a fully caught-up engine reads backlog 0 (AC: "When the engine keeps up it reads 0")', () => {
  const t = new EngineLagTracker();
  for (let i = 0; i < 40; i++) {
    t.recordTickScheduled();
    t.recordTickCompleted();
  }
  assert.equal(t.snapshot(0).backlog, 0);
  assert.equal(t.snapshot(0).backlogClass, 'green');
});

// ============================================================================
// Backlog classification bands
// ============================================================================

test('backlogClass green at 0, amber at BACKLOG_AMBER, red above BACKLOG_RED-1', () => {
  const t = new EngineLagTracker();
  assert.equal(t.snapshot(0).backlogClass, 'green');

  t.recordTickScheduled();
  assert.equal(t.snapshot(0).backlog, BACKLOG_AMBER);
  assert.equal(t.snapshot(0).backlogClass, 'amber');

  const t2 = new EngineLagTracker();
  for (let i = 0; i < BACKLOG_RED + 1; i++) t2.recordTickScheduled();
  assert.equal(t2.snapshot(0).backlog, BACKLOG_RED + 1);
  assert.equal(t2.snapshot(0).backlogClass, 'red');
});

// F2 fix (independent round REJECT, 2026-09-03): classifyBacklog previously
// used BACKLOG_AMBER (1) as its amber/red boundary instead of BACKLOG_RED
// (3), so a backlog of 2 wrongly read RED — undetected by the test above
// because it only pinned 1 (amber) and BACKLOG_RED+1=4 (red), skipping right
// over the exact value the bug misclassified. These three pin the boundary
// explicitly: backlog 2 and 3 must both be amber (<=BACKLOG_RED), 4 must be
// the first red (>BACKLOG_RED).
test('backlogClass: exactly 2 is amber (the value the pre-fix code wrongly read as red)', () => {
  const t = new EngineLagTracker();
  t.recordTickScheduled();
  t.recordTickScheduled();
  assert.equal(t.snapshot(0).backlog, 2);
  assert.equal(t.snapshot(0).backlogClass, 'amber');
});

test('backlogClass: exactly BACKLOG_RED (3) is still amber, the inclusive ceiling', () => {
  const t = new EngineLagTracker();
  for (let i = 0; i < BACKLOG_RED; i++) t.recordTickScheduled();
  assert.equal(t.snapshot(0).backlog, BACKLOG_RED);
  assert.equal(t.snapshot(0).backlogClass, 'amber', 'backlog exactly BACKLOG_RED must still be amber, not red');
});

test('backlogClass: exactly BACKLOG_RED + 1 (4) is the first red value', () => {
  const t = new EngineLagTracker();
  for (let i = 0; i < BACKLOG_RED + 1; i++) t.recordTickScheduled();
  assert.equal(t.snapshot(0).backlog, 4);
  assert.equal(t.snapshot(0).backlogClass, 'red');
});

// ============================================================================
// Tick-cost ratio classification (AC: <=1 green, 1-3 amber, >3 red)
// ============================================================================

test('ratio is null (and ratioClass green, not a false alarm) with no duration/interval data yet', () => {
  const t = new EngineLagTracker();
  const snap = t.snapshot(0);
  assert.equal(snap.ratio, null);
  assert.equal(snap.ratioClass, 'green');
});

test('ratioClass green at exactly the AMBER boundary (<=1 green per the brief)', () => {
  const t = new EngineLagTracker();
  t.setIntervalMs(100);
  t.recordTickDuration(100 * RATIO_AMBER); // ratio == RATIO_AMBER == 1
  const snap = t.snapshot(0);
  assert.equal(snap.ratio, RATIO_AMBER);
  assert.equal(snap.ratioClass, 'green');
});

test('ratioClass amber just above the green boundary and at the RED boundary', () => {
  const t = new EngineLagTracker();
  t.setIntervalMs(100);
  t.recordTickDuration(101); // ratio 1.01 > 1
  assert.equal(t.snapshot(0).ratioClass, 'amber');

  const t2 = new EngineLagTracker();
  t2.setIntervalMs(100);
  t2.recordTickDuration(100 * RATIO_RED); // ratio == 3, still amber (<=3)
  assert.equal(t2.snapshot(0).ratioClass, 'amber', 'ratio exactly RATIO_RED must still be amber, not red');
});

test('ratioClass red above the RED boundary', () => {
  const t = new EngineLagTracker();
  t.setIntervalMs(100);
  t.recordTickDuration(301); // ratio 3.01 > 3
  assert.equal(t.snapshot(0).ratioClass, 'red');
});

test('lastTickMs / intervalMs report the most recent values, not a running average', () => {
  const t = new EngineLagTracker();
  t.setIntervalMs(1000);
  t.recordTickDuration(50);
  t.recordTickDuration(9000); // a real stall-class tick
  const snap = t.snapshot(0);
  assert.equal(snap.lastTickMs, 9000, 'must report the LAST duration, not e.g. an average of 50 and 9000');
  assert.equal(snap.ratio, 9);
  assert.equal(snap.ratioClass, 'red');
});

test('recordTickDuration rejects negative and absurd values (sanity filter)', () => {
  const t = new EngineLagTracker();
  t.setIntervalMs(100);
  t.recordTickDuration(-5);
  assert.equal(t.snapshot(0).lastTickMs, null, 'a negative duration must never be recorded');
  t.recordTickDuration(999_999);
  assert.equal(t.snapshot(0).lastTickMs, null, 'an absurd (>=60s) duration must never be recorded');
  t.recordTickDuration(42);
  assert.equal(t.snapshot(0).lastTickMs, 42, 'a sane value is recorded normally');
});

test('setIntervalMs ignores non-positive values (never divides by zero)', () => {
  const t = new EngineLagTracker();
  t.recordTickDuration(10);
  t.setIntervalMs(0);
  assert.equal(t.snapshot(0).intervalMs, null, 'a 0 interval must be rejected, not stored as a divide-by-zero trap');
  t.setIntervalMs(-100);
  assert.equal(t.snapshot(0).intervalMs, null);
  t.setIntervalMs(200);
  assert.equal(t.snapshot(0).intervalMs, 200);
});

// ============================================================================
// Stall detector
// ============================================================================

test('a frame gap at or below STALL_THRESHOLD_MS is ordinary jitter, not a stall', () => {
  const t = new EngineLagTracker();
  t.recordFrameGap(STALL_THRESHOLD_MS, 1000);
  const snap = t.snapshot(1000);
  assert.equal(snap.recentStallMs, null, 'a gap AT the threshold must not itself count as a stall');
  assert.equal(snap.worstStallMs, 0);
});

test('a frame gap above STALL_THRESHOLD_MS records a stall and the session worst', () => {
  const t = new EngineLagTracker();
  t.recordFrameGap(STALL_THRESHOLD_MS + 1, 1000);
  const snap = t.snapshot(1000);
  assert.equal(snap.recentStallMs, STALL_THRESHOLD_MS + 1);
  assert.equal(snap.worstStallMs, STALL_THRESHOLD_MS + 1);
});

test('recentStallMs reverts to null after STALL_DISPLAY_MS has elapsed ("for a few seconds")', () => {
  const t = new EngineLagTracker();
  t.recordFrameGap(4200, 10_000); // a 4.2s stall recorded at t=10000
  assert.equal(t.snapshot(10_000).recentStallMs, 4200, 'immediately after: still shown');
  assert.equal(t.snapshot(10_000 + STALL_DISPLAY_MS).recentStallMs, 4200, 'exactly at the window edge: still shown');
  assert.equal(
    t.snapshot(10_000 + STALL_DISPLAY_MS + 1).recentStallMs,
    null,
    'past the display window: must revert to null, not stay stuck showing a stale stall forever'
  );
});

test('worstStallMs is a session high-water mark: a smaller LATER stall does not lower it', () => {
  const t = new EngineLagTracker();
  t.recordFrameGap(4200, 1000);
  t.recordFrameGap(600, 20_000); // a smaller, later stall
  const snap = t.snapshot(20_000);
  assert.equal(snap.recentStallMs, 600, 'the RECENT report is the latest stall');
  assert.equal(snap.worstStallMs, 4200, 'the WORST report never regresses to a smaller later value');
});

test('reset() clears backlog/duration/interval/recent-stall but preserves worstStallMs; resetAll() clears everything', () => {
  const t = new EngineLagTracker();
  t.recordTickScheduled();
  t.setIntervalMs(500);
  t.recordTickDuration(50);
  t.recordFrameGap(1000, 5000);

  t.reset();
  const afterReset = t.snapshot(5000);
  assert.equal(afterReset.ticksScheduled, 0);
  assert.equal(afterReset.ticksCompleted, 0);
  assert.equal(afterReset.lastTickMs, null);
  assert.equal(afterReset.intervalMs, null);
  assert.equal(afterReset.recentStallMs, null, 'reset() clears the recent-stall timestamp too');
  assert.equal(afterReset.worstStallMs, 1000, 'reset() preserves the session HWM (queueDepth.ts HWM convention)');

  t.resetAll();
  assert.equal(t.snapshot(5000).worstStallMs, 0, 'resetAll() clears the HWM too (test-isolation-only escape hatch)');
});

// ============================================================================
// F1 fix (independent round REJECT, 2026-09-03) — settle() / pause honesty
// ============================================================================

test('settle() zeroes backlog (scheduled/completed) but preserves worstStallMs and last-tick/interval stats', () => {
  const t = new EngineLagTracker();
  // Simulate a drag-supersede burst right before the player hits Pause:
  // several scheduled fires with only some completed, leaving a real
  // nonzero backlog at the instant of pause.
  t.recordTickScheduled();
  t.recordTickScheduled();
  t.recordTickScheduled();
  t.recordTickCompleted();
  t.setIntervalMs(900);
  t.recordTickDuration(120);
  t.recordFrameGap(2000, 5000); // a real stall earlier in the session

  assert.equal(t.snapshot(5000).backlog, 2, 'precondition: a real backlog exists before settle()');

  t.settle();
  const snap = t.snapshot(5000);
  assert.equal(snap.ticksScheduled, 0, 'settle() must zero ticksScheduled');
  assert.equal(snap.ticksCompleted, 0, 'settle() must zero ticksCompleted');
  assert.equal(snap.backlog, 0, 'a paused engine cannot be "behind" — backlog must read 0 immediately after settle()');
  assert.equal(snap.backlogClass, 'green');
  assert.equal(snap.lastTickMs, 120, 'settle() must PRESERVE the last real tick duration, not wipe it');
  assert.equal(snap.intervalMs, 900, 'settle() must PRESERVE the interval length');
  assert.equal(snap.worstStallMs, 2000, 'settle() must PRESERVE the session worst-stall high-water mark');
});

test('settle() is idempotent — calling it again while already settled is a harmless no-op', () => {
  const t = new EngineLagTracker();
  t.settle();
  assert.equal(t.snapshot(0).backlog, 0);
  t.settle();
  assert.equal(t.snapshot(0).backlog, 0);
  assert.equal(t.snapshot(0).ticksScheduled, 0);
  assert.equal(t.snapshot(0).ticksCompleted, 0);
});

test('after settle(), resuming (fresh scheduled/completed calls) counts up cleanly from a matched baseline', () => {
  const t = new EngineLagTracker();
  t.recordTickScheduled();
  t.recordTickScheduled();
  t.recordTickScheduled(); // backlog 3 at pause time
  t.settle();
  assert.equal(t.snapshot(0).backlog, 0);

  // Resume: the tick-driver starts firing again from a clean baseline.
  t.recordTickScheduled();
  t.recordTickCompleted();
  assert.equal(
    t.snapshot(0).backlog,
    0,
    'post-resume, a scheduled+completed pair must read caught-up, not carry forward the pre-pause deficit'
  );
});

// ============================================================================
// engineLagClassOf combinator
// ============================================================================

test('engineLagClassOf is green when both backlog and ratio are green', () => {
  const t = new EngineLagTracker();
  assert.equal(engineLagClassOf(t.snapshot(0)), 'green');
});

test('engineLagClassOf takes the WORSE of backlogClass/ratioClass', () => {
  const t = new EngineLagTracker();
  t.setIntervalMs(100);
  t.recordTickDuration(50); // ratio 0.5 -> green
  for (let i = 0; i < BACKLOG_RED + 1; i++) t.recordTickScheduled(); // backlog -> red
  assert.equal(engineLagClassOf(t.snapshot(0)), 'red', 'a red backlog must win over a green ratio');
});

test('engineLagClassOf is red whenever a stall is currently being reported, regardless of backlog/ratio', () => {
  const t = new EngineLagTracker();
  // Everything else green...
  t.setIntervalMs(100);
  t.recordTickDuration(10);
  t.recordTickScheduled();
  t.recordTickCompleted();
  assert.equal(engineLagClassOf(t.snapshot(0)), 'green', 'sanity: green before any stall');
  // ...but an active stall must override to red — "stalled" is the worst signal.
  t.recordFrameGap(4200, 0);
  assert.equal(engineLagClassOf(t.snapshot(0)), 'red');
});

// ============================================================================
// subscribe()
// ============================================================================

test('subscribe fires immediately with the current snapshot, then again on every mutation', () => {
  const t = new EngineLagTracker();
  const seen = [];
  const unsub = t.subscribe((s) => seen.push(s.backlog), 0);
  assert.equal(seen.length, 1, 'subscribe must fire immediately');
  assert.equal(seen[0], 0);

  t.recordTickScheduled();
  assert.equal(seen.length, 2, 'a mutation must notify the listener');
  assert.equal(seen[1], 1);

  unsub();
  t.recordTickScheduled();
  assert.equal(seen.length, 2, 'after unsubscribe, no further notifications');
});
