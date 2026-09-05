// rebuild-eta-and-gen-guard.test.mjs — BAR-2 (live ETA) + BAR-3 (generation
// guard) pure-function coverage, round r1 REJECT follow-up on
// FEAT-1972079917 / BUG-435.
//
// Both behaviours are decided by pure helpers extracted into genesisReplay.ts
// specifically so they're testable without mounting the store's React state
// machine (estimateRemainingLabel, isStaleRebuildChain).

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { estimateRemainingLabel, isStaleRebuildChain } from '../src/sim/genesisReplay.ts';

describe('BAR-2: estimateRemainingLabel derives ETA from LIVE observed rate', () => {
  test('fewer than 2 samples: no estimate (never a canned animation)', () => {
    assert.equal(estimateRemainingLabel([], 1000), null);
    assert.equal(estimateRemainingLabel([{ actionsDone: 10, timestamp: 0 }], 1000), null);
  });

  test('under the minimum sample window: no estimate yet ("estimating...")', () => {
    // Only 200ms elapsed — below the 1000ms trust window.
    const samples = [
      { actionsDone: 0, timestamp: 0 },
      { actionsDone: 50, timestamp: 200 },
    ];
    assert.equal(estimateRemainingLabel(samples, 1000), null);
  });

  test('a real held rate over >=1s produces a sane remaining-time label', () => {
    // 100 actions/sec observed over 2 seconds -> 200 actions done, 800 remaining
    // at that rate = 8s remaining.
    const samples = [
      { actionsDone: 0, timestamp: 0 },
      { actionsDone: 200, timestamp: 2000 },
    ];
    const label = estimateRemainingLabel(samples, 1000);
    assert.ok(label, 'must produce a label once the sample window is long enough');
    assert.ok(label.includes('remaining'), 'label must say "remaining"');
    assert.ok(label.includes('8s'), `expected ~8s remaining, got: ${label}`);
  });

  test('estimate shrinks as the SAME rate holds and more actions complete', () => {
    const total = 1000;
    // First estimate: 200/2000ms done -> 800 remaining @ 0.1/ms = 8000ms.
    const early = estimateRemainingLabel(
      [
        { actionsDone: 0, timestamp: 0 },
        { actionsDone: 200, timestamp: 2000 },
      ],
      total
    );
    // Later estimate at the SAME rate: 600/6000ms done -> 400 remaining @ 0.1/ms = 4000ms.
    const later = estimateRemainingLabel(
      [
        { actionsDone: 0, timestamp: 0 },
        { actionsDone: 600, timestamp: 6000 },
      ],
      total
    );
    assert.ok(early && later, 'both estimates must be present');
    // Parse the numeric seconds out of "~Xs remaining" / "~Xm Ys remaining".
    const secondsOf = (s) => {
      const m = s.match(/(?:(\d+)m\s*)?(\d+)s/);
      return (Number(m[1] ?? 0) * 60) + Number(m[2]);
    };
    assert.ok(secondsOf(later) < secondsOf(early), `estimate must shrink as progress holds: ${early} -> ${later}`);
  });

  test('no forward progress between samples: no estimate (never divide toward garbage)', () => {
    const samples = [
      { actionsDone: 50, timestamp: 0 },
      { actionsDone: 50, timestamp: 2000 },
    ];
    assert.equal(estimateRemainingLabel(samples, 1000), null);
  });

  test('remaining already zero: reports 0s, not a crash', () => {
    const samples = [
      { actionsDone: 0, timestamp: 0 },
      { actionsDone: 1000, timestamp: 2000 },
    ];
    const label = estimateRemainingLabel(samples, 1000);
    assert.ok(label && label.includes('0s'), `expected a 0s label, got: ${label}`);
  });
});

// BUG-714 (Aaron, 2026-09-05, "rebuilding your city takes much longer than
// expected; use this to better forecast the eta which is way off"): a live
// audit (webconsole's own BUG-714 measurement script, run against a realistic
// growth-then-mature-tail journal) found the OLD cumulative-average-since-
// start rate under-estimated the remaining time by 76-100% through the back
// half of a real rebuild, because per-action cost climbs sharply as the city
// fills up (cheap early 'place' actions vs. expensive 'tick' actions on a
// mature, full-scale city — the O(buildings) cost BUG-617 measured directly).
// The fix (a TRAILING rate window, see RECENT_RATE_WINDOW_MS in
// genesisReplay.ts) tracks CURRENT throughput instead of the whole history,
// so the estimate re-calibrates within about a second of a slowdown rather
// than staying anchored to the replay's faster opening actions for its
// entire remaining run.
describe('BUG-714: estimateRemainingLabel converges on an accelerating-cost workload', () => {
  // A synthetic two-phase run: a FAST phase (800 of 1000 actions in the first
  // 1000ms) followed by a SLOW phase (the remaining 200 actions over 4000ms —
  // a 16x per-action slowdown), sampled every 500ms — the same shape a real
  // genesis replay takes when a cheap growth phase gives way to an expensive
  // mature-city tail. Piecewise-linear by construction so the TRUE remaining
  // time at any sample is exactly computable, not just directionally checked.
  // Timestamps scaled 10x vs. the raw shape (fast phase: 800/1000 actions in
  // the first 10s; slow phase: the remaining 200 over the next 40s) so the
  // remaining-time assertions land well clear of formatDurationShort's
  // nearest-second display rounding at small values.
  const total = 1000;
  const samples = [
    { actionsDone: 0, timestamp: 0 },
    { actionsDone: 800, timestamp: 10_000 }, // end of the fast phase
    { actionsDone: 825, timestamp: 15_000 },
    { actionsDone: 850, timestamp: 20_000 },
    { actionsDone: 875, timestamp: 25_000 },
    { actionsDone: 900, timestamp: 30_000 },
    { actionsDone: 925, timestamp: 35_000 },
    { actionsDone: 950, timestamp: 40_000 },
    { actionsDone: 975, timestamp: 45_000 }, // 97.5% done by ACTION COUNT
    { actionsDone: 1000, timestamp: 50_000 },
  ];
  const RUN_END_MS = 50_000;

  const secondsOf = (s) => {
    const m = s.match(/(?:(\d+)m\s*)?(\d+)s/);
    return Number(m[1] ?? 0) * 60 + Number(m[2]);
  };

  test('the OLD cumulative-since-start rate would badly under-estimate deep in the slow phase', () => {
    // Reproduce the PRE-FIX formula directly (first sample vs latest, no
    // window) to document exactly the defect this fix closes — this is NOT
    // testing production code, just pinning the historical bad-forecast shape
    // for contrast with the assertion below.
    const upToDeepSlow = samples.slice(0, 9); // through t=4500, actionsDone=975
    const first = upToDeepSlow[0];
    const last = upToDeepSlow[upToDeepSlow.length - 1];
    const oldRate = (last.actionsDone - first.actionsDone) / (last.timestamp - first.timestamp);
    const oldRemainingMs = (total - last.actionsDone) / oldRate;
    const actualRemainingMs = RUN_END_MS - last.timestamp; // 500ms
    const underEstimatePct = ((actualRemainingMs - oldRemainingMs) / actualRemainingMs) * 100;
    assert.ok(
      underEstimatePct > 50,
      `expected the old cumulative model to badly under-estimate (>50%) at 97.5% action-progress deep in the slow phase, got ${underEstimatePct.toFixed(0)}% (old predicted ${oldRemainingMs.toFixed(0)}ms vs actual ${actualRemainingMs}ms)`
    );
  });

  test('the NEW trailing-window rate converges tightly to the true remaining time', () => {
    // Same 97.5%-progress point as above, but through the real (fixed) helper.
    const upToDeepSlow = samples.slice(0, 9); // through t=4500, actionsDone=975
    const label = estimateRemainingLabel(upToDeepSlow, total);
    assert.ok(label, 'must produce an estimate once the slow phase has held for a full window');
    const actualRemainingMs = RUN_END_MS - upToDeepSlow[upToDeepSlow.length - 1].timestamp; // 500ms
    const predictedMs = secondsOf(label) * 1000;
    const errorPct = (Math.abs(predictedMs - actualRemainingMs) / actualRemainingMs) * 100;
    assert.ok(
      errorPct <= 25,
      `expected the windowed estimate to land within 25% of the true remaining time (500ms) once the slow phase has held for a full window, got "${label}" (${errorPct.toFixed(0)}% error)`
    );
  });

  test('the estimate keeps re-converging as the slow phase continues (not a one-off lucky sample)', () => {
    for (let cut = 8; cut <= samples.length; cut++) {
      const upTo = samples.slice(0, cut);
      const label = estimateRemainingLabel(upTo, total);
      if (!label) continue; // early cuts may still be within the min-window guard
      const actualRemainingMs = RUN_END_MS - upTo[upTo.length - 1].timestamp;
      if (actualRemainingMs <= 0) continue;
      const predictedMs = secondsOf(label) * 1000;
      const errorPct = (Math.abs(predictedMs - actualRemainingMs) / actualRemainingMs) * 100;
      assert.ok(
        errorPct <= 30,
        `estimate at cut=${cut} ("${label}") must stay within 30% of actual remaining (${actualRemainingMs}ms), got ${errorPct.toFixed(0)}% error`
      );
    }
  });
});

describe('BAR-3: isStaleRebuildChain generation guard', () => {
  test('same generation: chain is NOT stale', () => {
    assert.equal(isStaleRebuildChain(3, 3), false);
  });

  test('a bumped generation: the old chain IS stale', () => {
    // Chain captured gen 3 at start; a Retry/watchdog-stall bumped the live
    // counter to 4 while the old chain's rAF was still pending.
    assert.equal(isStaleRebuildChain(3, 4), true);
  });

  test('the guard is a strict compare, not a >= check', () => {
    // A chain that somehow captured a generation AHEAD of the live counter
    // (should never happen, but the guard must not silently treat that as
    // fresh either) is still flagged stale/mismatched.
    assert.equal(isStaleRebuildChain(5, 4), true);
  });
});
