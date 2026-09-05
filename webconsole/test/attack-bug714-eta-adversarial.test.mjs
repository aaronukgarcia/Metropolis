// attack-bug714-eta-adversarial.test.mjs — INDEPENDENT DESTRUCTIVE ROUND
// (opus-round-bug714) against the trailing-window ETA rewrite in
// genesisReplay.ts estimateRemainingLabel.
//
// Attack surfaces (from the round brief):
//  1. Adversarial sample sequences: NaN/Infinity timestamps, out-of-order,
//     single-chunk sabotage, zero-elapsed divide-by-zero. Label must ALWAYS be
//     a sane string or null — never NaN/Infinity/negative/crash.
//  2. Fallback-to-full-history: can a noisy cadence STARVE the recent window so
//     it never estimates? Does it OSCILLATE wildly chunk-to-chunk?
//  3. Monotonic-acceleration honesty: worst-case end-loaded journal at 95%+.
//  5. No clock reads (purity): same input -> same output, order-independent of
//     wall clock.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { estimateRemainingLabel } from '../src/sim/genesisReplay.ts';

// A label is "sane" iff it is null OR a string with no NaN/Infinity token and
// no negative number, and it parses to a finite non-negative second count.
function isSaneLabel(label) {
  if (label === null) return true; // the sentinel is always acceptable
  if (typeof label !== 'string') return false;
  if (/nan|infinity|undefined/i.test(label)) return false;
  if (/-\d/.test(label)) return false;
  const m = label.match(/(?:(\d+)m\s*)?(\d+)s/);
  if (!m) return false;
  const secs = Number(m[1] ?? 0) * 60 + Number(m[2]);
  return Number.isFinite(secs) && secs >= 0;
}
function assertSaneLabel(label, ctx) {
  assert.ok(isSaneLabel(label), `${ctx}: label is not sane: "${label}"`);
}
// The estimate must NEVER throw, regardless of input — a rebuild progress bar
// crashing the store is unacceptable. Sanity of the *string* is asserted
// separately only for REACHABLE inputs (finite timestamps/counts).
function assertNoThrow(fn, ctx) {
  let out;
  assert.doesNotThrow(() => { out = fn(); }, `${ctx}: must not throw`);
  return out;
}

const secondsOf = (label) => {
  const m = label.match(/(?:(\d+)m\s*)?(\d+)s/);
  return Number(m[1] ?? 0) * 60 + Number(m[2]);
};

// ---------------------------------------------------------------------------
// ATTACK-1a: NON-FINITE inputs (NaN / ±Infinity timestamps, counts, totals).
//
// FINDING (opus-round-bug714): estimateRemainingLabel does NOT guard against
// non-finite numeric inputs. NaN/±Infinity in a timestamp, actionsDone, or
// actionsTotal propagate through the rate/remaining arithmetic and produce a
// GARBAGE label such as "~NaNs remaining" / "~Infinitym NaNs remaining".
//
// ADJUDICATION: this is an INHERITED, PRE-EXISTING robustness gap, NOT a
// BUG-714 regression — the pre-fix cumulative formula produces the identical
// garbage on the same inputs (proven in the round's analysis script). It is
// ALSO UNREACHABLE from the only caller (store.tsx:2628-2630 feeds
// performance.now() timestamps + integer progress counts + a finite journal
// length — none can ever be NaN/±Infinity). Recorded here as a permanent pin +
// a filed P3 follow-up (add a `Number.isFinite` guard returning the null
// sentinel). The HARD invariant the fix must hold — never THROW — is asserted;
// string-sanity is asserted only for reachable (finite) inputs below.
// ---------------------------------------------------------------------------
describe('ATTACK-1a: non-finite inputs never CRASH (inherited garbage-string gap documented)', () => {
  const nonFinite = {
    'NaN latest timestamp': [{ actionsDone: 0, timestamp: 0 }, { actionsDone: 100, timestamp: 2000 }, { actionsDone: 200, timestamp: NaN }],
    'NaN interior timestamp': [{ actionsDone: 0, timestamp: 0 }, { actionsDone: 100, timestamp: NaN }, { actionsDone: 200, timestamp: 3000 }],
    'NaN actionsDone': [{ actionsDone: 0, timestamp: 0 }, { actionsDone: NaN, timestamp: 2000 }, { actionsDone: 200, timestamp: 4000 }],
    '+Infinity timestamp': [{ actionsDone: 0, timestamp: 0 }, { actionsDone: 100, timestamp: 2000 }, { actionsDone: 200, timestamp: Infinity }],
    '-Infinity timestamp': [{ actionsDone: 0, timestamp: -Infinity }, { actionsDone: 100, timestamp: 2000 }, { actionsDone: 200, timestamp: 4000 }],
  };
  for (const [name, s] of Object.entries(nonFinite)) {
    test(`${name}: never throws (garbage string is inherited + unreachable)`, () => {
      assertNoThrow(() => estimateRemainingLabel(s, 1000), name);
    });
  }
  test('Infinity actionsTotal: never throws', () => {
    const s = [{ actionsDone: 0, timestamp: 0 }, { actionsDone: 100, timestamp: 2000 }, { actionsDone: 200, timestamp: 4000 }];
    assertNoThrow(() => estimateRemainingLabel(s, Infinity), 'Infinity actionsTotal');
  });
});

describe('ATTACK-1: adversarial (but REACHABLE) sample sequences never yield a garbage label', () => {
  test('out-of-order timestamps (non-monotonic clock)', () => {
    const s = [
      { actionsDone: 0, timestamp: 5000 },
      { actionsDone: 100, timestamp: 1000 }, // clock went BACKWARDS
      { actionsDone: 200, timestamp: 8000 },
    ];
    assertSaneLabel(estimateRemainingLabel(s, 1000), 'out-of-order timestamps');
  });

  test('out-of-order actionsDone (progress went backwards)', () => {
    const s = [
      { actionsDone: 500, timestamp: 0 },
      { actionsDone: 100, timestamp: 2000 }, // fewer done than before
      { actionsDone: 600, timestamp: 4000 },
    ];
    assertSaneLabel(estimateRemainingLabel(s, 1000), 'backwards actionsDone');
  });

  test('zero-elapsed between all samples (identical timestamps)', () => {
    const s = [
      { actionsDone: 0, timestamp: 3000 },
      { actionsDone: 100, timestamp: 3000 },
      { actionsDone: 200, timestamp: 3000 },
    ];
    // totalElapsedMs = 0 < MIN_ETA_SAMPLE_WINDOW_MS -> must be null, no /0.
    const label = estimateRemainingLabel(s, 1000);
    assertSaneLabel(label, 'zero-elapsed identical timestamps');
    assert.equal(label, null, 'zero total elapsed must yield the sentinel, not a divide-by-zero');
  });

  test('single-chunk sabotage: all progress collapsed into the final sample', () => {
    // Long flat run then everything lands at once on the last sample.
    const s = [
      { actionsDone: 0, timestamp: 0 },
      { actionsDone: 0, timestamp: 2000 },
      { actionsDone: 0, timestamp: 4000 },
      { actionsDone: 999, timestamp: 4001 }, // 999 actions in 1ms
    ];
    assertSaneLabel(estimateRemainingLabel(s, 1000), 'single-chunk collapse');
  });

  test('final tiny recent step after a large gap -> window starves, fallback used', () => {
    const s = [
      { actionsDone: 0, timestamp: 0 },
      { actionsDone: 900, timestamp: 5000 },
      { actionsDone: 901, timestamp: 5050 }, // 1 action in 50ms (<200ms window)
    ];
    assertSaneLabel(estimateRemainingLabel(s, 1000), 'starved window fallback');
  });

  test('remaining exactly zero and beyond (overshoot) never negative', () => {
    const atTotal = [
      { actionsDone: 0, timestamp: 0 },
      { actionsDone: 1000, timestamp: 2000 },
    ];
    assert.equal(estimateRemainingLabel(atTotal, 1000), '~0s remaining');
    const over = [
      { actionsDone: 0, timestamp: 0 },
      { actionsDone: 1200, timestamp: 2000 }, // more done than total
    ];
    assertSaneLabel(estimateRemainingLabel(over, 1000), 'overshoot');
  });

  test('actionsTotal <= 0 and negative are handled', () => {
    const s = [
      { actionsDone: 0, timestamp: 0 },
      { actionsDone: 100, timestamp: 2000 },
    ];
    assert.equal(estimateRemainingLabel(s, 0), null);
    assert.equal(estimateRemainingLabel(s, -5), null);
  });

  test('a large fuzz sweep of chaotic sequences never crashes or emits garbage', () => {
    // Deterministic LCG so the sweep is reproducible (purity/determinism).
    let seed = 0x1234abcd;
    const rnd = () => {
      seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0;
      return seed / 0x100000000;
    };
    const chaos = [0, 1, -1, NaN, Infinity, -Infinity, 0.5, 1e12, -1e9];
    for (let iter = 0; iter < 4000; iter++) {
      const n = 2 + Math.floor(rnd() * 8);
      const s = [];
      for (let i = 0; i < n; i++) {
        const t = rnd() < 0.15 ? chaos[Math.floor(rnd() * chaos.length)] : Math.floor(rnd() * 60000);
        const a = rnd() < 0.15 ? chaos[Math.floor(rnd() * chaos.length)] : Math.floor(rnd() * 2000);
        s.push({ actionsDone: a, timestamp: t });
      }
      const total = rnd() < 0.1 ? chaos[Math.floor(rnd() * chaos.length)] : Math.floor(rnd() * 3000);
      let label;
      // HARD invariant on EVERY input, finite or not: never throw.
      assert.doesNotThrow(() => { label = estimateRemainingLabel(s, total); }, `fuzz iter ${iter} threw`);
      // Sanity of the STRING is only guaranteed for reachable (all-finite)
      // inputs — the non-finite garbage-string gap is documented in ATTACK-1a.
      const allFinite = Number.isFinite(total) && s.every((x) => Number.isFinite(x.actionsDone) && Number.isFinite(x.timestamp));
      if (allFinite) {
        assertSaneLabel(label, `fuzz iter ${iter} total=${total} samples=${JSON.stringify(s)}`);
      }
    }
  });
});

describe('ATTACK-2: ETA STABILITY across consecutive chunks (no wild oscillation)', () => {
  // At the REAL chunk cadence (CHUNK_TIME_BUDGET_MS=40, rendered per rAF), a
  // 1000ms trailing window averages ~18-25 chunks, so heavy per-chunk cost
  // noise is damped. Build an 800-chunk run at ~40ms/chunk with falling
  // throughput + brutal per-chunk noise and assert the chunk-to-chunk ETA
  // never swings more than 3x (measured worst ~2x, dominated by nearest-second
  // display rounding at small remaining values, not by rate instability).
  test('windowed ETA is steady at the real ~40ms cadence despite cost noise', () => {
    let seed = 0xBEEF >>> 0;
    const rnd = () => { seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0; return seed / 0x100000000; };
    const samples = [{ actionsDone: 0, timestamp: 0 }];
    let t = 0, done = 0;
    const N = 800;
    for (let i = 0; i < N; i++) {
      const frac = i / N;
      const baseActs = 30 * (1 - 0.9 * frac);
      const acts = Math.max(1, Math.round(baseActs * (0.3 + 1.4 * rnd())));
      const dt = Math.round(40 * (0.75 + 0.5 * rnd()));
      t += dt; done += acts;
      samples.push({ actionsDone: done, timestamp: t });
    }
    const total = samples[samples.length - 1].actionsDone;
    let prev = null, worst = 1;
    for (let cut = 2; cut < samples.length; cut++) {
      const label = estimateRemainingLabel(samples.slice(0, cut), total);
      assertSaneLabel(label, `stability cut=${cut}`);
      const secs = label ? secondsOf(label) : null;
      if (prev != null && secs != null && prev > 0 && secs > 0) {
        worst = Math.max(worst, Math.max(secs / prev, prev / secs));
      }
      if (secs != null) prev = secs;
    }
    assert.ok(worst <= 3, `windowed ETA swung ${worst.toFixed(2)}x chunk-to-chunk at real cadence (expected <=3x)`);
  });
});

describe('ATTACK-3: monotonic-acceleration honesty on an end-loaded journal', () => {
  // The fix cannot predict an UNSEEN future slowdown (no causal estimator can).
  // At a hard 95%-fast / 5%-catastrophically-slow cliff, at the 95% point the
  // estimate necessarily reads ~0s while 45s actually remain — this is bounded
  // and honest (never negative/garbage), and matches the pre-fix behaviour.
  // The value the fix ADDS is that ONCE the slow tail is being sampled the
  // windowed estimate re-converges within ~a window; assert that convergence.
  const total = 1000;
  const s = [{ actionsDone: 0, timestamp: 0 }, { actionsDone: 950, timestamp: 5000 }];
  for (let i = 1; i <= 45; i++) {
    s.push({ actionsDone: Math.min(1000, 950 + Math.round((50 * i) / 45)), timestamp: 5000 + i * 1000 });
  }
  const END = s[s.length - 1].timestamp;

  test('pre-burst under-estimate is bounded & sane (never garbage/negative)', () => {
    const label = estimateRemainingLabel(s.slice(0, 2), total);
    assertSaneLabel(label, 'pre-burst');
    assert.equal(label, '~0s remaining', 'a fast opening legitimately predicts ~0s before the cliff is observed');
  });

  test('once the slow tail is sampled, the windowed estimate re-converges (vast majority within 35%)', () => {
    // FINDING (opus-round-bug714): with a THIN window (this harness samples the
    // tail only every 1000ms, so the 1000ms window holds ~1 sample) the
    // integer-quantized trickle makes doneDelta beat between 1 and 2 per
    // window, producing periodic ~2x ETA swings (spikes to ~45% error every
    // ~9 samples). This is BOUNDED and UNREACHABLE at production cadence:
    // CHUNK_TIME_BUDGET_MS=40 yields a progress sample every ~40ms, so the
    // window holds ~25 samples and ±1 integer quantization is <5%. Assert the
    // OVERWHELMING MAJORITY of tail cuts land within 35% (proving genuine
    // recalibration — the pre-fix cumulative rate would stay pinned near ~0s
    // for the ENTIRE tail), tolerating the documented periodic thin-window
    // quantization spikes.
    let within = 0, count = 0;
    for (let cut = 3; cut <= s.length; cut++) {
      const upTo = s.slice(0, cut);
      const trueRem = END - upTo[upTo.length - 1].timestamp;
      if (trueRem <= 1000) break; // sub-second true remaining: display rounding dominates
      const label = estimateRemainingLabel(upTo, total);
      assertSaneLabel(label, `tail cut=${cut}`);
      const err = Math.abs(secondsOf(label) * 1000 - trueRem) / trueRem * 100;
      count++;
      if (err <= 35) within++;
    }
    const frac = within / count;
    assert.ok(frac >= 0.75, `only ${(frac * 100).toFixed(0)}% of tail cuts converged within 35% (expected >=75%; thin-window quantization spikes are the known bounded exception)`);
  });
});

describe('ATTACK-4: the full-history FALLBACK is exercised (author tests never starve the window)', () => {
  // The 3 author BUG-714 tests space samples 5000ms apart with a 1000ms window,
  // so recentWindow always spans a full inter-sample gap and NEVER hits the
  // MIN_RECENT_WINDOW_MS fallback. Breaking the fallback therefore escapes
  // their coverage (proven by the round's mutation run: `if (false)` on the
  // fallback guard left all 3 green). These cases drive a STARVED recent
  // window (last two samples share a timestamp, or are <200ms apart) so the
  // fallback path is load-bearing — without it a zero-width recent window
  // divides by zero and falsely reports "~0s".
  test('zero-width recent window falls back to full history (not a false ~0s)', () => {
    // Full history spans 2000ms with real progress, but the last two samples
    // share a timestamp -> recent window elapsed = 0 -> must fall back.
    const s = [
      { actionsDone: 0, timestamp: 0 },
      { actionsDone: 500, timestamp: 2000 },
      { actionsDone: 600, timestamp: 2000 }, // same ts as prev
    ];
    const label = estimateRemainingLabel(s, 1000);
    assertSaneLabel(label, 'zero-width recent window');
    assert.notEqual(label, '~0s remaining', 'a zero-width recent window must fall back, not report done');
    // Full-history rate: 600 actions / 2000ms = 0.3/ms; remaining 400 => ~1333ms => "~1s".
    assert.equal(label, '~1s remaining', 'must use the full-history rate when the recent window is starved');
  });

  test('sub-200ms recent window falls back rather than trusting a noisy micro-sample', () => {
    const s = [
      { actionsDone: 0, timestamp: 0 },
      { actionsDone: 900, timestamp: 5000 },
      { actionsDone: 901, timestamp: 5050 }, // 1 action in 50ms (<200ms)
    ];
    const label = estimateRemainingLabel(s, 1000);
    assertSaneLabel(label, 'sub-200ms window');
    // Trusting the 50ms micro-sample (1 action/50ms = 0.02/ms) would predict
    // remaining 99 / 0.02 = ~5s; the full-history fallback (901/5050=0.178/ms)
    // predicts 99/0.178 = ~0.5s => "~1s"/"~0s". Assert it is NOT the wildly
    // inflated micro-sample estimate.
    const secs = label ? secondsOf(label) : 0;
    assert.ok(secs <= 2, `expected the full-history fallback (<=2s), got "${label}" — the sub-200ms micro-sample was trusted`);
  });
});

describe('ATTACK-5: purity / no clock reads', () => {
  test('same input -> same output across repeated calls and time', () => {
    const s = [
      { actionsDone: 0, timestamp: 0 },
      { actionsDone: 500, timestamp: 3000 },
      { actionsDone: 600, timestamp: 6000 },
    ];
    const a = estimateRemainingLabel(s, 1000);
    const b = estimateRemainingLabel(s, 1000);
    assert.equal(a, b, 'estimate must be a pure function of its arguments');
  });

  test('does not mutate the input samples array', () => {
    const s = [
      { actionsDone: 0, timestamp: 0 },
      { actionsDone: 500, timestamp: 3000 },
      { actionsDone: 600, timestamp: 6000 },
    ];
    const snapshot = JSON.stringify(s);
    estimateRemainingLabel(s, 1000);
    assert.equal(JSON.stringify(s), snapshot, 'input must not be mutated');
  });
});
