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
