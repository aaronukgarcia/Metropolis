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
  engineLagChipClassOf,
  engineLagChipLabelOf,
  RATIO_AMBER,
  RATIO_RED,
  BACKLOG_AMBER,
  BACKLOG_RED,
  STALL_THRESHOLD_MS,
  STALL_DISPLAY_MS,
  RATE_WINDOW_TICKS,
  RATE_WINDOW_MS,
  MIN_RATE_SAMPLES,
  DEAD_WORKER_SCHEDULED_TICKS,
  RATE_GREEN_FRACTION,
  RATE_AMBER_FRACTION,
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
// BUG-787 — windowed achieved-vs-demanded RATE (replaces the one-way-ratchet
// backlog display). All timestamps below are fabricated (passed explicitly
// to recordTickCompleted), never real clock reads, per the module's
// testability discipline.
//
// RED-PROOF discipline (GR#24 — no destructive git): each assertion was
// hand-verified to actually fail by temporarily mutating a scratch copy of
// engineLag.ts (`cp engineLag.ts engineLag.ts.scratch; ...edit...; run
// against scratch; mv back`) — e.g. reverting classifyRate to the OLD
// classifyBacklog-based backlogClass (the exact pre-fix ratchet shape) makes
// the "burst then 30 on-budget ticks recovers to green" test fail (stays
// red forever, since a stale backlog-derived class never heals), and
// widening RATE_AMBER_FRACTION below the sustained-40%-rate fixture's value
// flips that test's expected class.
// ============================================================================

test('BUG-787: a single 3x-slow tick followed by 30 on-budget ticks returns to green (the OLD backlog-ratchet code stayed amber/red forever)', () => {
  const t = new EngineLagTracker();
  const intervalMs = 100;
  t.setIntervalMs(intervalMs); // demanded rate 10 t/s

  let now = 0;
  // The burst: one on-time tick, then one 3x-slow tick (300ms instead of 100ms).
  t.recordTickCompleted(now);
  now += intervalMs * 3;
  t.recordTickCompleted(now);

  // Immediately after the slow tick, the achieved rate over the 2-tick
  // window is still dragged down by the slow gap — sanity-check it is NOT
  // green yet (otherwise this test would prove nothing).
  const rightAfterSlowTick = t.snapshot(now);
  assert.notEqual(
    rightAfterSlowTick.rateClass,
    'green',
    'precondition: immediately after the slow tick the windowed rate must NOT read green yet'
  );

  // 30 more on-budget ticks (exactly RATE_WINDOW_TICKS) — enough to fully
  // evict the slow tick and its neighbour from the RATE_WINDOW_TICKS-capped
  // buffer.
  for (let i = 0; i < RATE_WINDOW_TICKS; i++) {
    now += intervalMs;
    t.recordTickCompleted(now);
  }

  const after = t.snapshot(now);
  assert.equal(
    after.rateClass,
    'green',
    'BUG-787: once the slow tick ages out of the window, the rate must recover to green — the old one-way-ratchet backlog display never recovered without an explicit settle()/reset()'
  );
});

test('BUG-787: a sustained ~40% achieved rate reads red with the right achieved/demanded numbers', () => {
  const t = new EngineLagTracker();
  const intervalMs = 100;
  t.setIntervalMs(intervalMs); // demanded rate 10 t/s
  // Ticks arriving every 250ms instead of every 100ms -> achieved 4 t/s,
  // 40% of demanded -> below RATE_AMBER_FRACTION (60%) -> red.
  let now = 0;
  for (let i = 0; i < 6; i++) {
    t.recordTickCompleted(now);
    now += 250;
  }
  const snap = t.snapshot(now);
  assert.equal(snap.demandedRate, 10, 'demandedRate = 1000/intervalMs');
  assert.ok(Math.abs(snap.achievedRate - 4) < 0.01, `achievedRate should be ~4 t/s, got ${snap.achievedRate}`);
  assert.equal(snap.rateClass, 'red', 'a sustained 40% achieved rate must read red');
});

test('BUG-787: a sustained ~67% achieved rate reads amber (between the 60% floor and the 95% green ceiling)', () => {
  const t = new EngineLagTracker();
  const intervalMs = 100;
  t.setIntervalMs(intervalMs); // demanded rate 10 t/s
  // Ticks arriving every 150ms instead of every 100ms -> achieved ~6.67 t/s,
  // ~67% of demanded -> amber (>=60%, <95%).
  let now = 0;
  for (let i = 0; i < 6; i++) {
    t.recordTickCompleted(now);
    now += 150;
  }
  const snap = t.snapshot(now);
  assert.ok(Math.abs(snap.achievedRate - 20 / 3) < 0.01, `achievedRate should be ~6.67 t/s, got ${snap.achievedRate}`);
  assert.equal(snap.rateClass, 'amber', 'a sustained ~67% achieved rate must read amber, not red or green');
});

test('BUG-787: a sustained on-budget rate reads green', () => {
  const t = new EngineLagTracker();
  const intervalMs = 100;
  t.setIntervalMs(intervalMs);
  let now = 0;
  for (let i = 0; i < 6; i++) {
    t.recordTickCompleted(now);
    now += intervalMs;
  }
  const snap = t.snapshot(now);
  assert.ok(Math.abs(snap.achievedRate - 10) < 0.01);
  assert.equal(snap.rateClass, 'green');
});

test('BUG-787: achievedRate is null before at least 2 completions are recorded (no false alarm on startup)', () => {
  const t = new EngineLagTracker();
  t.setIntervalMs(100);
  assert.equal(t.snapshot(0).achievedRate, null, 'no completions yet');
  t.recordTickCompleted(0);
  assert.equal(t.snapshot(0).achievedRate, null, 'a single completion cannot derive a rate');
  t.recordTickCompleted(100);
  assert.ok(t.snapshot(0).achievedRate !== null, 'two completions are enough to derive a rate');
});

test('BUG-787: the rate window empties on reset() — achievedRate reverts to null', () => {
  const t = new EngineLagTracker();
  t.setIntervalMs(100);
  t.recordTickCompleted(0);
  t.recordTickCompleted(100);
  t.recordTickCompleted(200);
  assert.ok(t.snapshot(0).achievedRate !== null, 'precondition: a rate exists before reset()');
  t.reset();
  assert.equal(t.snapshot(0).achievedRate, null, 'reset() must empty the windowed-rate buffer');
  assert.equal(t.snapshot(0).rateClass, 'green', 'empty rate data must read green, never a false alarm');
});

test('BUG-787: the rate window empties on settle() (pause honesty extends to the rate signal too)', () => {
  const t = new EngineLagTracker();
  t.setIntervalMs(100);
  t.recordTickCompleted(0);
  t.recordTickCompleted(250); // a genuinely slow pair, red if it survived
  assert.notEqual(t.snapshot(0).rateClass, 'green', 'precondition: a bad rate exists before settle()');
  t.settle();
  assert.equal(t.snapshot(0).achievedRate, null, 'settle() must empty the windowed-rate buffer');
  assert.equal(t.snapshot(0).rateClass, 'green', 'a paused engine has no current rate to report — must read green, not carry forward a stale red');
});

test('BUG-787: completedTimes buffer is capped at RATE_WINDOW_TICKS entries — an old completion outside RATE_WINDOW_MS of the newest is excluded from the rate', () => {
  const t = new EngineLagTracker();
  t.setIntervalMs(100);
  // Two completions close together, then a long real-world gap (e.g. the
  // engine was legitimately idle/slow for a while) bigger than RATE_WINDOW_MS
  // before the next completion — the old pair must not still be averaged in.
  t.recordTickCompleted(0);
  t.recordTickCompleted(100);
  const farFuture = RATE_WINDOW_MS + 100_000;
  t.recordTickCompleted(farFuture);
  t.recordTickCompleted(farFuture + 100);
  const snap = t.snapshot(farFuture + 100);
  // Only the last two (spaced 100ms apart, on-budget) should be in the
  // window relative to the newest timestamp — achieved rate should read as
  // caught up (green), not dragged down by the ancient pair.
  assert.equal(snap.rateClass, 'green', 'ancient completions outside RATE_WINDOW_MS of the newest must not pollute the current rate');
});

// ============================================================================
// opus-round-bug787 (2026-09-05) — round REJECT fixes F1/F2/F3/F4/F5
// ============================================================================

test('opus-round-bug787: RATE_WINDOW_TICKS and MIN_RATE_SAMPLES are pinned to specific values (F5 — these were previously unpinned, so 30->3 or 12->1 would silently pass)', () => {
  assert.equal(RATE_WINDOW_TICKS, 30, 'RATE_WINDOW_TICKS must stay pinned at 30 — a silent shrink defeats the F5 sample floor');
  assert.equal(MIN_RATE_SAMPLES, 12, 'MIN_RATE_SAMPLES must stay pinned at 12');
  assert.ok(MIN_RATE_SAMPLES <= RATE_WINDOW_TICKS, 'the floor can never exceed the storage cap it draws from');
});

// F1: dead-worker detection. Both scenarios below are mechanically IDENTICAL
// (scheduledSinceLastCompletion is a tick-count threshold, not a time one —
// see DEAD_WORKER_SCHEDULED_TICKS's own doc comment) but are written at
// realistic tick counts for a 160ms Turbo interval to mirror the two
// concrete durations named in the round: ~1 minute (60000/160 ~= 375 ticks)
// and ~30 minutes (1800000/160 ~= 11250 ticks) of a dead worker.

test('opus-round-bug787 F1: dead-from-boot — ~1 minute of scheduled fires with ZERO completions ever reads red, never a frozen/false "Engine: OK"', () => {
  const t = new EngineLagTracker();
  t.setIntervalMs(160);
  for (let i = 0; i < 375; i++) t.recordTickScheduled();
  const snap = t.snapshot(60_000);
  assert.equal(snap.achievedRate, null, 'no completions ever recorded, so there genuinely is no rate data');
  assert.equal(snap.deadWorker, true, 'F1: >=DEAD_WORKER_SCHEDULED_TICKS scheduled fires with zero completions must be flagged dead');
  assert.equal(snap.scheduledSinceLastCompletion, 375);
  assert.equal(engineLagClassOf(snap), 'red', 'a dead-from-boot worker must read red, not green off the null-achievedRate convention');
});

test('opus-round-bug787 F1: dead-from-boot — ~30 minutes of scheduled fires with ZERO completions still reads red', () => {
  const t = new EngineLagTracker();
  t.setIntervalMs(160);
  for (let i = 0; i < 11_250; i++) t.recordTickScheduled();
  const snap = t.snapshot(1_800_000);
  assert.equal(snap.deadWorker, true);
  assert.equal(engineLagClassOf(snap), 'red', 'a 30-minute-dead worker must read red — this is the EXACT reported defect (45,000 scheduled / 0 completed reading green)');
});

test('opus-round-bug787 F1: dead-after-healthy — a worker that was fine, then stopped completing for ~1 minute of schedule fires, reads red (not the frozen last-healthy rate)', () => {
  const t = new EngineLagTracker();
  t.setIntervalMs(160); // demanded rate 6.25 t/s
  // A healthy period first — real completions at the correct cadence.
  let now = 0;
  for (let i = 0; i < 20; i++) {
    t.recordTickCompleted(now);
    t.recordTickScheduled();
    now += 160;
  }
  const healthySnap = t.snapshot(now);
  assert.equal(healthySnap.rateClass, 'green', 'precondition: the tracker was genuinely healthy before dying');
  assert.equal(healthySnap.deadWorker, false);

  // The worker dies: the tick-driver keeps firing (recordTickScheduled) but
  // nothing ever completes again, for ~1 minute at this interval (~375 fires).
  for (let i = 0; i < 375; i++) t.recordTickScheduled();
  now += 375 * 160;
  const deadSnap = t.snapshot(now);
  assert.equal(deadSnap.deadWorker, true, 'F1: scheduled fires piling up with no NEW completions must flag dead, regardless of stale healthy history');
  assert.equal(
    engineLagClassOf(deadSnap),
    'red',
    'F1 RED-PROOF: the old self-referential rate window would have frozen the pre-death achievedRate/rateClass forever here — this must NOT read green'
  );
});

test('opus-round-bug787 F1: dead-after-healthy — ~30 minutes of schedule fires with no new completions reads red', () => {
  const t = new EngineLagTracker();
  t.setIntervalMs(160);
  let now = 0;
  for (let i = 0; i < 20; i++) {
    t.recordTickCompleted(now);
    t.recordTickScheduled();
    now += 160;
  }
  for (let i = 0; i < 11_250; i++) t.recordTickScheduled();
  now += 11_250 * 160;
  const snap = t.snapshot(now);
  assert.equal(snap.deadWorker, true);
  assert.equal(engineLagClassOf(snap), 'red');
});

test('opus-round-bug787 F1: settle() (pause) clears the dead-worker signal — a paused engine is never "dead"', () => {
  const t = new EngineLagTracker();
  t.setIntervalMs(160);
  for (let i = 0; i < 10; i++) t.recordTickScheduled(); // no completions -> would be dead
  assert.equal(t.snapshot(0).deadWorker, true, 'precondition: dead before settle()');
  t.settle();
  assert.equal(t.snapshot(0).deadWorker, false, 'settle() must clear deadWorker — pause is not death');
  assert.equal(t.snapshot(0).scheduledSinceLastCompletion, 0);
});

// F3: setIntervalMs must clear the rate window ONLY on a genuine change, and
// that change must never itself produce a non-green reading (the false
// "4-9s red on every speed-up" the round reported).

test('opus-round-bug787 F3: a genuine interval CHANGE clears the achieved-rate window (old cadence must not be judged against the new demanded rate)', () => {
  const t = new EngineLagTracker();
  t.setIntervalMs(200); // old cadence: 200ms -> demanded 5 t/s
  let now = 0;
  for (let i = 0; i < 15; i++) {
    t.recordTickCompleted(now);
    now += 200;
  }
  assert.equal(t.snapshot(now).rateClass, 'green', 'precondition: healthy at the OLD interval');

  // Speed up: interval actually changes to 100ms (demanded doubles to 10 t/s).
  t.setIntervalMs(100);
  const rightAfterSpeedUp = t.snapshot(now);
  assert.equal(
    rightAfterSpeedUp.achievedRate,
    null,
    'F3: the window must be CLEARED on a genuine interval change, not carry the old 200ms-cadence samples forward'
  );
  assert.equal(
    rightAfterSpeedUp.rateClass,
    'green',
    'F3: immediately after a speed-up, with no rate data yet, must read green (no data), never a false red/amber off stale-cadence samples'
  );
});

test('opus-round-bug787 F3: a same-value setIntervalMs call (ordinary re-fire, no actual speed change) must NOT clear the window', () => {
  const t = new EngineLagTracker();
  t.setIntervalMs(160);
  let now = 0;
  for (let i = 0; i < 15; i++) {
    t.recordTickCompleted(now);
    now += 160;
  }
  assert.ok(t.snapshot(now).achievedRate !== null, 'precondition: a rate exists');
  t.setIntervalMs(160); // same value — store.tsx's effect can re-fire without a real change
  assert.ok(
    t.snapshot(now).achievedRate !== null,
    'F3: a same-value setIntervalMs call must be a no-op on the rate window, never clear real history'
  );
});

test('opus-round-bug787 F3: a full speed-up sequence — clear, then re-accumulate at the NEW cadence — never produces a non-green reading along the way', () => {
  const t = new EngineLagTracker();
  t.setIntervalMs(200);
  let now = 0;
  for (let i = 0; i < 15; i++) {
    t.recordTickCompleted(now);
    now += 200;
  }
  t.setIntervalMs(100); // speed up
  const readings = [];
  readings.push(engineLagClassOf(t.snapshot(now)));
  // Re-accumulate at the NEW, matching 100ms cadence.
  for (let i = 0; i < 15; i++) {
    now += 100;
    t.recordTickCompleted(now);
    readings.push(engineLagClassOf(t.snapshot(now)));
  }
  assert.ok(
    readings.every((r) => r === 'green'),
    `F3: every reading through a speed-up + healthy re-accumulation must be green — got [${readings.join(', ')}]`
  );
});

// F2: dual-statistic (mean + median, worse-of) rate classification — Aaron's
// exact measured scenario must still read green (a minority of ticks
// spiking moderately), while a genuinely-sustained-slow OR a majority-
// healthy-but-severely-degraded-minority scenario must read amber/red.

// opus-reround-bug787 (round REJECT #2): re-verified at a >=90% floor (down
// from the round-1 test's >=95%) — introducing the mean-over-span statistic
// (needed to close the "median absorbs 49% of ticks at any severity"
// exploit) pulls Aaron's real scenario's worst-case window down to ~88-94%
// depending on window alignment (see RATE_GREEN_FRACTION's own comment for
// the exact arithmetic: 1 spike in a 29-gap window = 93.5%, 2 spikes = 87.9%
// — the mean CANNOT ignore either occurrence, unlike the old median-only
// statistic), so a small number of transient non-green readings during
// worst-case window alignment is expected and does not represent user-
// visible flapping (RATE_GREEN_FRACTION governs it, currently 0.85).
test('opus-reround-bug787 F2: Aaron\'s measured scenario (112ms median tick, one 480ms spike in ~20 completions, 160ms Turbo interval) reads green on >=90% of readings', () => {
  const t = new EngineLagTracker();
  const intervalMs = 160;
  t.setIntervalMs(intervalMs); // demanded rate 6.25 t/s
  let now = 0;
  const readings = [];
  const TOTAL_TICKS = 200;
  for (let i = 0; i < TOTAL_TICKS; i++) {
    // One spike in every 20 completions (5%), matching the round's "one
    // 480ms in 20" framing; every other gap is the healthy interval.
    const gap = (i + 1) % 20 === 0 ? 480 : intervalMs;
    now += gap;
    t.recordTickCompleted(now);
    readings.push(t.snapshot(now).rateClass);
  }
  const greenCount = readings.filter((r) => r === 'green').length;
  const greenFraction = greenCount / readings.length;
  assert.ok(
    greenFraction >= 0.9,
    `F2: Aaron's scenario must read green on >=90% of readings — got ${(greenFraction * 100).toFixed(1)}% (${greenCount}/${readings.length})`
  );
});

// opus-reround-bug787 F2 (the actual exploit this round fixes): 45% of ticks
// running at 480ms (a MAJORITY-healthy but heavily-degraded-minority
// distribution) must NOT read green — the median alone would report the
// healthy majority (100%), but the mean-over-span correctly reports the
// true averaged throughput (~53%, matching the round's own arithmetic).
test('opus-reround-bug787 F2: 45% of ticks at 480ms (majority healthy, but a large minority badly degraded) does not read green — mean throughput ~53%', () => {
  const t = new EngineLagTracker();
  const intervalMs = 160;
  t.setIntervalMs(intervalMs); // demanded rate 6.25 t/s
  let now = 0;
  // A long, evenly-interleaved run so the windowed sample always reflects
  // close to the true 45%/55% mix (not just a lucky/unlucky alignment).
  const TOTAL_TICKS = 100;
  let slowCount = 0;
  for (let i = 0; i < TOTAL_TICKS; i++) {
    const wantSlow = slowCount / (i + 1) < 0.45;
    const gap = wantSlow ? 480 : intervalMs;
    if (wantSlow) slowCount++;
    now += gap;
    t.recordTickCompleted(now);
  }
  const snap = t.snapshot(now);
  const meanFraction = snap.achievedRate / snap.demandedRate;
  assert.ok(
    Math.abs(meanFraction - 0.53) < 0.03,
    `sanity: mean throughput should be close to the round's own ~53% figure — got ${(meanFraction * 100).toFixed(1)}%`
  );
  assert.notEqual(snap.rateClass, 'green', 'F2: 45% of ticks at 480ms must NOT read green — the median-only exploit reported this as healthy');
});

// opus-reround-bug787 F2 (the EXACT reported exploit): 45% of ticks at
// 30,000ms — true throughput ~1% of demanded — must read red, not the false
// "Engine: OK" the median-only statistic produced.
test('opus-reround-bug787 F2: 45% of ticks at 30,000ms (true throughput ~1%) reads red, not the false "Engine: OK" the median-only exploit produced', () => {
  const t = new EngineLagTracker();
  const intervalMs = 160;
  t.setIntervalMs(intervalMs); // demanded rate 6.25 t/s
  let now = 0;
  const TOTAL_TICKS = 40;
  let slowCount = 0;
  for (let i = 0; i < TOTAL_TICKS; i++) {
    const wantSlow = slowCount / (i + 1) < 0.45;
    const gap = wantSlow ? 30_000 : intervalMs;
    if (wantSlow) slowCount++;
    now += gap;
    t.recordTickCompleted(now);
  }
  const snap = t.snapshot(now);
  const meanFraction = snap.achievedRate / snap.demandedRate;
  assert.ok(
    meanFraction < 0.05,
    `sanity: true mean throughput must be close to the reported ~1% — got ${(meanFraction * 100).toFixed(1)}%`
  );
  // The median-only exploit this round fixes: the healthy 55% majority
  // still makes the MEDIAN read ~100% (green) — proving the median alone is
  // not enough, and it is the WORSE-of-two combination that saves this case.
  assert.notEqual(snap.achievedRateMedian, null);
  assert.ok(
    snap.achievedRateMedian / snap.demandedRate > 0.9,
    'sanity: the median-only statistic would still misreport this as near-100% healthy — proving why mean-over-span had to be added, not just a threshold tweak'
  );
  assert.equal(
    snap.rateClass,
    'red',
    'F2 RED-PROOF: 45% of ticks at 30,000ms must read red — this is the exact reported exploit (45,000/30,000ms scenario reading "Engine: OK")'
  );
});

// F5/general: RATE_WINDOW_MS pinning — prove it is NOT an inert knob: an old
// completion outside the window must be excluded from the rate sample when
// there is NOT enough total history for the MIN_RATE_SAMPLES floor to kick
// in (if the floor always rescued it regardless of RATE_WINDOW_MS, this
// constant would be dead and should be deleted rather than kept as an inert
// knob per the round's own instruction).
test('opus-reround-bug787: RATE_WINDOW_MS is a live constraint, not a dead knob — an old completion outside it (with too little total history for the F5 floor) is excluded from the rate', () => {
  const t = new EngineLagTracker();
  t.setIntervalMs(1000);
  // Only 2 completions ever — well under MIN_RATE_SAMPLES, so the F5 floor
  // (which needs >= MIN_RATE_SAMPLES of TOTAL history) cannot rescue this.
  t.recordTickCompleted(0);
  const farFuture = RATE_WINDOW_MS + 5000;
  t.recordTickCompleted(farFuture);
  const snap = t.snapshot(farFuture);
  // If RATE_WINDOW_MS were inert (e.g. treated as Infinity), the sample
  // would be [0, farFuture] and the achieved rate would be a real (very
  // low) number. With RATE_WINDOW_MS enforced, the t=0 entry ages out,
  // leaving only 1 timestamp in the window — not enough to derive a rate.
  assert.equal(snap.achievedRate, null, 'RATE_WINDOW_MS must exclude the ancient completion, leaving too few in-window samples for a rate');
  assert.equal(snap.achievedRateMedian, null);
  assert.equal(snap.rateClass, 'green', 'no rate data (correctly excluded ancient sample) reads green, not a stale/fabricated figure');
});

// Amber-dot-with-OK impossibility (opus-reround-bug787, round REJECT #2):
// property-style fuzz over many synthetic snapshots proving
// engineLagChipLabelOf can NEVER return 'Engine: OK' when
// engineLagChipClassOf disagrees (non-green), across paused/unpaused and a
// wide spread of rate/ratio/stall/deadWorker combinations.
test('opus-reround-bug787: amber-dot-with-OK is impossible — label is never "Engine: OK" whenever the chip class is non-green (fuzzed)', () => {
  const scenarios = [];

  // Directly synthesize snapshots (bypassing the tracker) to cover the full
  // combinatorial space cheaply and deterministically.
  const lagClasses = ['green', 'amber', 'red'];
  function makeSnap(overrides) {
    return {
      ticksScheduled: 0,
      ticksCompleted: 0,
      backlog: 0,
      slippedSinceLoad: 0,
      lastTickMs: 250,
      intervalMs: 160,
      ratio: 1.5,
      ratioClass: 'green',
      backlogClass: 'green',
      achievedRate: 6.0,
      achievedRateMedian: 6.0,
      demandedRate: 6.25,
      rateClass: 'green',
      scheduledSinceLastCompletion: 0,
      deadWorker: false,
      worstStallMs: 0,
      recentStallMs: null,
      ...overrides,
    };
  }

  for (const ratioClass of lagClasses) {
    for (const rateClass of lagClasses) {
      for (const deadWorker of [false, true]) {
        for (const stalled of [false, true]) {
          for (const paused of [false, true]) {
            scenarios.push({
              paused,
              snap: makeSnap({
                ratioClass,
                rateClass,
                deadWorker,
                recentStallMs: stalled ? 1200 : null,
                // Keep achievedRate/demandedRate consistent with rateClass
                // being non-green so `behind` can legitimately trigger too.
                achievedRate: rateClass === 'green' ? 6.0 : rateClass === 'amber' ? 5.0 : 1.0,
              }),
            });
          }
        }
      }
    }
  }

  let checked = 0;
  for (const { snap, paused } of scenarios) {
    const cls = engineLagChipClassOf(snap, paused);
    const label = engineLagChipLabelOf(snap, paused);
    if (cls !== 'green') {
      assert.notEqual(
        label,
        'Engine: OK',
        `amber-dot-with-OK: cls=${cls} paused=${paused} ratioClass=${snap.ratioClass} rateClass=${snap.rateClass} deadWorker=${snap.deadWorker} stalled=${snap.recentStallMs !== null} produced the label "Engine: OK"`
      );
    }
    checked++;
  }
  assert.ok(checked > 50, 'sanity: the fuzz must actually cover a meaningful number of scenarios');
});

test('opus-round-bug787 F2: 20 CONSECUTIVE 480ms ticks (no healthy majority to fall back on) reads red, then recovers to green once healthy ticks return', () => {
  const t = new EngineLagTracker();
  const intervalMs = 160;
  t.setIntervalMs(intervalMs); // demanded rate 6.25 t/s
  let now = 0;
  const duringReadings = [];
  for (let i = 0; i < 20; i++) {
    now += 480;
    t.recordTickCompleted(now);
    duringReadings.push(t.snapshot(now).rateClass);
  }
  // The tail of the sustained-slow run (once enough slow samples have
  // accumulated to dominate the window/floor) must read red.
  assert.equal(duringReadings[duringReadings.length - 1], 'red', 'F2: 20 consecutive 480ms ticks must read red at the end of the run');

  // Recovery: RATE_WINDOW_TICKS more healthy ticks at the real interval.
  for (let i = 0; i < RATE_WINDOW_TICKS; i++) {
    now += intervalMs;
    t.recordTickCompleted(now);
  }
  assert.equal(t.snapshot(now).rateClass, 'green', 'F2/BUG-787: must recover to green once the slow run ages out of the window');
});

// F5: the sample-count floor — a slow ("Slow" speed) tick-driver interval
// naturally produces very few completions inside a plain RATE_WINDOW_MS
// window even while healthy; one nearby hiccup must not dominate a too-thin
// sample. This is the concrete "Slow speed has 6 samples so one hiccup reads
// red" scenario named in the round.

// opus-reround-bug787 (2026-09-05, dated retune note): this test originally
// asserted 'green' here, reasoning purely from the MEDIAN statistic (10 of
// 11 gaps at 3000ms outvote the one 9000ms hiccup, median stays healthy).
// That reasoning is exactly the round-2 exploit ("the median absorbs up to
// 49% of ticks at ANY severity") — it ignored that the hiccup, though a
// minority, is a REAL 3x-slow tick that genuinely cost 6000ms of extra time,
// and the mean-over-span statistic (now also consulted, worse-of-two)
// correctly reflects that: 11 gaps summing to 39000ms for 12 completions =
// 0.282 t/s achieved vs 0.333 t/s demanded = 84.6%, just under the 85%
// green floor -> amber. This is the CORRECT, more honest outcome: a single
// recent 3x-slow tick out of 12 (a much higher hiccup RATE than Aaron's 1-
// in-20 scenario, which stays green) should show as a mild, real amber
// degradation, not be swept under the rug as a false "OK". The floor's job
// was only ever to stop it reading a false FULL RED off a too-thin 2-sample
// window (proven below) — not to manufacture a false green.
test('opus-round-bug787 F5: a Slow-speed interval with one recent hiccup reads amber (not a false RED off a too-thin window, but not a false GREEN either)', () => {
  const t = new EngineLagTracker();
  const intervalMs = 3000; // "Slow" speed — few ticks fit in a plain 10s window
  t.setIntervalMs(intervalMs); // demanded rate 0.333 t/s
  let now = 0;
  // 12 clean historical ticks at the correct 3000ms cadence...
  for (let i = 0; i < 12; i++) {
    t.recordTickCompleted(now);
    if (i < 11) now += 3000;
  }
  // ...then ONE recent hiccup (3x slow) as the very next (13th) completion.
  now += 9000;
  t.recordTickCompleted(now);

  const naiveWindowOnly = (() => {
    // What a plain RATE_WINDOW_MS-only cut (no floor) would see: only
    // entries within 10s of `now` — just the last 2 timestamps here, i.e.
    // the hiccup gap itself with nothing to dilute it against.
    const withinWindow = [now - 9000, now]; // the last clean tick + the hiccup
    return (withinWindow[1] - withinWindow[0]) / 1; // the raw gap, for documentation only
  })();
  assert.equal(naiveWindowOnly, 9000, 'sanity: without the floor, the only available gap IS the 9000ms hiccup itself');
  // Without the floor, BOTH statistics would read off that lone 9000ms gap:
  // rate = 1000/9000 = 0.111 t/s = 33% of demanded -> RED. The floor's job
  // is to soften that false full-red into an honest amber, not erase it.
  assert.ok(1000 / naiveWindowOnly / (1000 / intervalMs) < RATE_AMBER_FRACTION, 'sanity: the naive window-only figure would be red');

  const snap = t.snapshot(now);
  assert.ok(Math.abs(snap.achievedRateMedian - 1000 / 3000) < 0.001, 'the median stays at the healthy 3000ms cadence (10 of 11 gaps)');
  assert.ok(
    snap.achievedRate < snap.achievedRateMedian,
    'the MEAN must read lower than the median here — it is the honest statistic that cannot ignore the real 6000ms of extra time the hiccup cost'
  );
  assert.equal(
    snap.rateClass,
    'amber',
    'F5: the floor must turn a false full-red (too-thin window) into a real, modest amber (the mean statistic) — never all the way to a false green (what median-only did pre-reround)'
  );
});

test('opus-round-bug787 F5: without enough total history to floor with, a thin/early sample is used as-is (the floor never fabricates data)', () => {
  const t = new EngineLagTracker();
  t.setIntervalMs(3000);
  // Only 2 completions ever recorded — well under MIN_RATE_SAMPLES. The
  // floor's "use the total history" fallback must not kick in (there is
  // nothing extra to fall back to) — this must behave exactly as a thin
  // 2-sample read (the pre-F5 behaviour), not throw or fabricate.
  t.recordTickCompleted(0);
  t.recordTickCompleted(9000); // a lone slow gap
  const snap = t.snapshot(9000);
  assert.equal(snap.achievedRate, 1000 / 9000);
  assert.equal(snap.rateClass, 'red', 'with only 2 total samples ever, a slow lone gap correctly reads red — nothing to dilute it with yet');
});

// F4: slippedSinceLoad must survive settle() as a non-settling accumulator.

test('opus-round-bug787 F4: slippedSinceLoad accumulates ACROSS settle() calls instead of zeroing on every pause', () => {
  const t = new EngineLagTracker();
  // First pause cycle: schedule 3, complete 1 -> backlog 2 lost.
  t.recordTickScheduled();
  t.recordTickScheduled();
  t.recordTickScheduled();
  t.recordTickCompleted();
  assert.equal(t.snapshot(0).slippedSinceLoad, 2, 'precondition: 2 slipped before the first settle()');
  t.settle();
  assert.equal(
    t.snapshot(0).slippedSinceLoad,
    2,
    'F4: settle() must NOT zero slippedSinceLoad — the old bug would report 0 here, hiding the 2 real slipped ticks'
  );

  // Second pause cycle after resuming: schedule 4, complete 1 -> backlog 3 lost.
  t.recordTickScheduled();
  t.recordTickScheduled();
  t.recordTickScheduled();
  t.recordTickScheduled();
  t.recordTickCompleted();
  assert.equal(t.snapshot(0).slippedSinceLoad, 5, 'precondition: 2 (accumulated) + 3 (live backlog) = 5 before the second settle()');
  t.settle();
  assert.equal(t.snapshot(0).slippedSinceLoad, 5, 'F4: the SECOND settle() must ADD to the accumulator (2+3=5), never reset it to just the latest backlog');
});

test('opus-round-bug787 F4: only reset()/resetAll() clear slippedSinceLoad — a fresh load, not a pause', () => {
  const t = new EngineLagTracker();
  t.recordTickScheduled();
  t.recordTickScheduled();
  t.recordTickCompleted();
  t.settle();
  assert.equal(t.snapshot(0).slippedSinceLoad, 1);
  t.reset();
  assert.equal(t.snapshot(0).slippedSinceLoad, 0, 'reset() (a fresh mount/load) must clear slippedSinceLoad');
});

// ============================================================================
// engineLagClassOf combinator
// ============================================================================

test('engineLagClassOf is green when both backlog and ratio are green', () => {
  const t = new EngineLagTracker();
  assert.equal(engineLagClassOf(t.snapshot(0)), 'green');
});

// BUG-787 (2026-09-05, dated retune note): this test used to prove
// engineLagClassOf took the WORSE of backlogClass/ratioClass by forcing
// backlogClass red via pure scheduled fires with ZERO completions — that was
// exactly the one-way-ratchet display bug (a real production city could sit
// "red" forever off stale scheduled-vs-completed counters, e.g. the exact
// 966-behind/112ms-median-tick contradiction Aaron hit). engineLagClassOf no
// longer consults backlogClass at all (see the function's own BUG-787
// comment) — it combines rateClass (the windowed achieved-vs-demanded
// signal) with ratioClass instead. This retuned test proves the equivalent
// "WORSE of the two live signals" property using rateClass: a sustained slow
// completion rate forces rateClass red while ratio stays green.
test('engineLagClassOf takes the WORSE of rateClass/ratioClass (BUG-787: no longer backlogClass)', () => {
  const t = new EngineLagTracker();
  t.setIntervalMs(100); // demanded rate 10 t/s
  t.recordTickDuration(50); // ratio 0.5 -> green
  // Completions spaced 1000ms apart -> achieved rate 1 t/s, 10% of demanded -> red.
  t.recordTickCompleted(0);
  t.recordTickCompleted(1000);
  t.recordTickCompleted(2000);
  assert.equal(engineLagClassOf(t.snapshot(0)), 'red', 'a red rateClass must win over a green ratioClass');
});

// Old backlogClass-only test retained (2026-09-05 sanity check) to prove
// backlogClass on its own — still computed, still tested directly — does
// NOT by itself drive engineLagClassOf any more: a backlog-red-but-rate-
// unknown snapshot (zero completions ever recorded) must read green overall,
// since there is no achieved-rate data yet (same "null reads green" ratio
// convention). This is the direct RED-PROOF that backlogClass was actually
// disconnected from the combinator, not merely renamed.
test('BUG-787: engineLagClassOf reads green when backlogClass is red but no completions exist yet (no rate data = no false alarm)', () => {
  const t = new EngineLagTracker();
  t.setIntervalMs(100);
  t.recordTickDuration(50); // ratio green
  for (let i = 0; i < BACKLOG_RED + 1; i++) t.recordTickScheduled(); // backlogClass -> red, but zero completions
  const snap = t.snapshot(0);
  assert.equal(snap.backlogClass, 'red', 'precondition: backlogClass is still computed and still red here');
  assert.equal(snap.achievedRate, null, 'precondition: no completions ever recorded, so achievedRate is null');
  assert.equal(snap.rateClass, 'green', 'no rate data yet must read green, matching the ratioClass null convention');
  assert.equal(
    engineLagClassOf(snap),
    'green',
    'engineLagClassOf must NOT be dragged red by a stale backlogClass alone — that is the exact one-way-ratchet class BUG-787 fixes'
  );
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
