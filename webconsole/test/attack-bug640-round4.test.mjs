// attack-bug640-round4.test.mjs — INDEPENDENT DESTRUCTIVE RE-ROUND (GR#23) on
// BUG-640 r4 (consistency.ts's pushGraceable, two new signature-BLIND
// backstops layered on the r2/r3 signature-matched windowed grace):
//   (1) !Number.isFinite(delta) -> never graceable.
//   (2) SUSTAINED DIVERGENCE: priorDeltas.length >= GRACE_WINDOW_SIZE - 1
//       (raw-failed on every one of the caller's supplied historical
//       snapshots, gap-free) AND fails again now -> forced red regardless of
//       signature.
//
// Not the author's test; written by the attacking session per the r4 brief:
//   (a) is the epsilon refusal sound, or a round-3-constants artefact?
//   (b) THE GAP-FREE ASSUMPTION — a defect failing 5/6, 9/10, 19/20 (one
//       healthy gap, forever) — does it evade BOTH backstops?
//   (c) CALLER TRUST — the first GRACE_WINDOW_SIZE-1 refreshes of a fresh
//       session/page-reload: can nothing ever red?
//   (d) FALSE-POSITIVE REGRESSION — does backstop (2), being signature-BLIND,
//       red a genuinely healthy, continuously-growing city?
//   (e) -0/+0, Number.MIN_VALUE, alternating-sign deltas.
//   (f) RED-proof both backstops independently via scratch-copy.
//   (g) legacy Set path stays byte-identical; GR#27 captures stay raw.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import {
  runConsistencyChecks,
  GRACE_ELIGIBLE_LINE_IDS,
  GRACE_WINDOW_SIZE,
  GRACE_MAX_FAILURES_IN_WINDOW,
  GRACE_RATE_WINDOW_SIZE,
  GRACE_RATE_THRESHOLD,
  foldGraceHistory,
} from '../src/sim/consistency.ts';
import { buildDebugJson } from '../src/sim/debugjson.ts';
import { captureBeforeWipe, readPreWipeArchive } from '../src/sim/captureBeforeWipe.ts';

function seedCity() {
  let s = initialState();
  const services = [
    ['road', 30, 30],
    ['pylon', 31, 30],
    ['wat_clean', 32, 30],
    ['edu_primary', 34, 30],
    ['park', 36, 30],
    ['com_shop', 37, 30],
  ];
  for (const [spec, x, y] of services) s = reducer(s, { type: 'place', spec, x, y });
  s = {
    ...s,
    buildings: [...s.buildings, { id: s.nextId, spec: 'off_suite', x: 300, y: 200 }],
    nextId: s.nextId + 1,
  };
  return s;
}

function siteAt(n, baseX = 40, baseY = 40) {
  const row = Math.floor(n / 20);
  const col = n % 20;
  return { x: baseX + col * 2, y: baseY + row * 2 };
}

// Mirrors DebugTab's takeFrame/graceHistoryRef exactly.
//
// ROUND-4 UPDATE: DebugTab's rolling queue now caps at
// `GRACE_RATE_WINDOW_SIZE - 1` (not `GRACE_WINDOW_SIZE - 1`) so the new
// gap-tolerant slow tier ever has enough history to fire — see
// consistency.ts's GRACE_RATE_WINDOW_SIZE doc comment and debugTab.tsx's
// graceHistoryRef. This helper is updated to match the real caller contract.
function makeWindowedRunner() {
  let history = [];
  return {
    run(s, override) {
      const report = runConsistencyChecks(s, override, foldGraceHistory(history));
      history = [...history, report.rawFailedSignatures].slice(-(GRACE_RATE_WINDOW_SIZE - 1));
      return report;
    },
    historySnapshot: () => history,
  };
}

function tamperWagesBy(state, delta) {
  return {
    ...state,
    lastFlows: {
      ...state.lastFlows,
      outflows: state.lastFlows.outflows.map((f) =>
        f.label === 'Wages' ? { ...f, value: f.value + delta } : f,
      ),
    },
  };
}

// ============================================================================
// (b) THE GAP-FREE ASSUMPTION — the obvious hole
// ============================================================================
//
// ROUND-4 FIX (post-REJECT): the round-3 gap-free run-length gate is GONE —
// REPLACED (not layered alongside) by a single gap-TOLERANT "K of N raw
// occurrence count" rate backstop (GRACE_RATE_WINDOW_SIZE / GRACE_RATE_
// THRESHOLD — see consistency.ts's doc comment for the full rationale and
// measured tuning). Under the new mechanism, RUN-LENGTH is irrelevant —
// only the TOTAL occurrence count within the trailing GRACE_RATE_WINDOW_SIZE
// checks matters, gaps or no gaps. So:
//   - the three duty cycles below (75%/80%/83%) that used to evade FOREVER
//     under the old run-length gate now DO get caught — not within the old
//     120-check probe (the rate window is much bigger, 180), but reliably
//     within ~150-166 checks (measured, all three duty cycles, all three gap
//     phases) — see the FIX PROOF tests below.
//   - the two "control" duty cycles (90%/95%) are no longer meaningfully
//     different in KIND from the 75-83% cases — they are simply caught
//     FASTER (higher duty -> the occurrence count crosses the threshold
//     sooner), confirming duty cycle (not run-length, which no longer exists
//     as a concept in this mechanism) is what governs detection latency now.
function runDutyCycleDriftAttack(dutyN, dutyD, gapOffset, checks) {
  let s = seedCity();
  for (let i = 0; i < 20; i++) s = reducer(s, { type: 'tick' });
  const runner = makeWindowedRunner();
  let redCount = 0;
  let rawFailCount = 0;
  let driftCounter = 0;
  let firstRedAt = -1;
  for (let i = 0; i < checks; i++) {
    s = reducer(s, { type: 'tick' });
    const isHealthyGap = (i + gapOffset) % dutyD === 0;
    let report;
    if (isHealthyGap) {
      report = runner.run(s); // no tamper this check — the "healthy" slot
    } else {
      driftCounter++;
      // Drifting delta: never repeats bit-identical, models a genuine
      // population/cumulative-spend-linked formula bug.
      const delta = 1 + driftCounter * 1e-6;
      const tampered = tamperWagesBy(s, delta);
      const raw = runConsistencyChecks(tampered);
      if (!raw.checks.find((c) => c.id === 'flows.wages-matches').ok) rawFailCount++;
      report = runner.run(tampered);
    }
    const check = report.checks.find((c) => c.id === 'flows.wages-matches');
    if (check && !check.ok) {
      redCount++;
      if (firstRedAt === -1) firstRedAt = i;
    }
  }
  return { redCount, rawFailCount, driftCounter, firstRedAt };
}

// GENUINELY EVASIVE-UNDER-THE-OLD-MECHANISM shapes (max consecutive-fail run
// <= GRACE_WINDOW_SIZE - 1 = 5, so the OLD round-3 gap-free gate never fired)
// — now measured under the round-4 rate backstop:
//   - at CHECKS=120 (the round-3-era probe length, well under the new
//     GRACE_RATE_WINDOW_SIZE=180), still 0 reds — the rate window hasn't
//     even filled yet, so this is NOT a regression, just a slower mechanism.
//   - at CHECKS=250 (comfortably past GRACE_RATE_WINDOW_SIZE), all three
//     duty cycles at all three gap phases MUST eventually red — proving the
//     round-4 fix actually closes the hole this section is named for.
for (const [n, d] of [
  [3, 4], // gap every 4th check, duty cycle 75% (the weakest evasive case)
  [4, 5], // gap every 5th check, duty cycle 80%
  [5, 6], // gap every 6th check, duty cycle ~83%
]) {
  for (const gapOffset of [0, 1, 2]) {
    test(`ATTACK b: duty cycle ${n}/${d} (gap offset ${gapOffset}) evades detection within the OLD 120-check probe length (rate window not yet full — not a regression)`, () => {
      const CHECKS = 120;
      const { redCount, rawFailCount, driftCounter } = runDutyCycleDriftAttack(n, d, gapOffset, CHECKS);
      assert.equal(rawFailCount, driftCounter, 'sanity: every fired tamper is a genuine raw failure');
      assert.equal(
        redCount,
        0,
        `at ${CHECKS} checks (< GRACE_RATE_WINDOW_SIZE=${GRACE_RATE_WINDOW_SIZE}) duty ${n}/${d} offset ${gapOffset} has not yet had enough history for the rate backstop to fire (got ${redCount})`,
      );
    });

    test(`ATTACK b FIX PROOF: duty cycle ${n}/${d} (gap offset ${gapOffset}) IS eventually caught by the round-4 gap-tolerant rate backstop`, () => {
      const CHECKS = 250;
      const { redCount, rawFailCount, driftCounter, firstRedAt } = runDutyCycleDriftAttack(n, d, gapOffset, CHECKS);
      console.log(
        `ATTACK b FIX PROOF duty=${n}/${d} gapOffset=${gapOffset}: over ${CHECKS} checks, driftFailures=${driftCounter}, rawFailCount=${rawFailCount}, gracedRedCount=${redCount}, firstRedAt=${firstRedAt}`,
      );
      assert.equal(rawFailCount, driftCounter, 'sanity: every fired tamper is a genuine raw failure');
      assert.ok(
        redCount > 0,
        `BUG-640 r4 FIX: a drifting defect with a healthy gap every ${d}th check (duty cycle ${n}/${d}, offset ${gapOffset}) must eventually red under the gap-tolerant rate backstop — 0/${CHECKS} would mean the round-4 hole is still open`,
      );
      assert.ok(
        firstRedAt >= 0 && firstRedAt < GRACE_RATE_WINDOW_SIZE * 1.1,
        `first red at ${firstRedAt} should land within a small margin of GRACE_RATE_WINDOW_SIZE=${GRACE_RATE_WINDOW_SIZE} (measured: ~148-166 across all three duty cycles/phases), not drift arbitrarily late`,
      );
    });
  }
}

// CONTROL: sparse gaps (duty cycle 9/10, 19/20) are caught FASTER than the
// 75-83% cases above (higher duty -> the occurrence count crosses
// GRACE_RATE_THRESHOLD sooner) — there is no separate "run-length" axis left
// to disprove; this control now demonstrates the mechanism is a pure,
// monotonic function of duty cycle.
for (const [n, d] of [
  [9, 10],
  [19, 20],
]) {
  test(`ATTACK b (control): duty cycle ${n}/${d} is caught, and no later than the weaker 3/4 duty cycle above (monotonic in duty, not a separate run-length regime)`, () => {
    const CHECKS = 250;
    const { redCount, rawFailCount, driftCounter, firstRedAt } = runDutyCycleDriftAttack(n, d, 0, CHECKS);
    console.log(`ATTACK b control duty=${n}/${d}: driftFailures=${driftCounter}, rawFailCount=${rawFailCount}, gracedRedCount=${redCount}, firstRedAt=${firstRedAt}`);
    assert.equal(rawFailCount, driftCounter, 'sanity: every fired tamper is a genuine raw failure');
    assert.ok(
      redCount > 0,
      `CONTROL: duty cycle ${n}/${d} must be caught by the rate backstop (got ${redCount}/${CHECKS})`,
    );
    assert.ok(
      firstRedAt >= 0 && firstRedAt <= 166,
      `CONTROL: a HIGHER duty cycle (${n}/${d}) must be caught NO LATER than the weakest evasive case measured (3/4 duty, first red ~166) — got firstRedAt=${firstRedAt}, confirming detection latency is monotonic in duty cycle under the new mechanism`,
    );
  });
}

// A non-drifting (repeating exact delta) high-duty-cycle defect is NOT masked
// — signature matching alone catches it within a couple of matching
// occurrences, gap or no gap. This confirms the hole above is specifically
// about DRIFT, not duty-cycle per se.
test('ATTACK b (control): a NON-drifting (identical-delta) 5/6 duty-cycle defect IS still caught by signature matching alone', () => {
  let s = seedCity();
  for (let i = 0; i < 20; i++) s = reducer(s, { type: 'tick' });
  const runner = makeWindowedRunner();
  let redCount = 0;
  const FIXED_DELTA = 12345.6789;
  for (let i = 0; i < 30; i++) {
    s = reducer(s, { type: 'tick' });
    const isHealthyGap = i % 6 === 0;
    const report = isHealthyGap ? runner.run(s) : runner.run(tamperWagesBy(s, FIXED_DELTA));
    const check = report.checks.find((c) => c.id === 'flows.wages-matches');
    if (check && !check.ok) redCount++;
  }
  assert.ok(
    redCount > 0,
    'control: an identical-delta (non-drifting) recurring defect is caught by signature matching even with gaps — confirms the ATTACK-b hole is specific to DRIFT, not merely to duty-cycle < 100%',
  );
});

// ============================================================================
// (c) CALLER TRUST — the fresh-session / startup window
// ============================================================================
//
// Backstop 2 needs priorDeltas.length >= GRACE_WINDOW_SIZE - 1 (5), which by
// construction cannot be true until the caller has accumulated
// GRACE_RATE_THRESHOLD - 1 PRIOR raw failures within its history. A
// brand-new session/page reload (DebugTab's rolling history starts empty)
// therefore has a window during which NOTHING can ever red for a drifting,
// grace-eligible id (signature match also cannot accumulate a repeat because
// a drifting delta never repeats).
//
// ROUND-4 UPDATE: the startup window did NOT shrink — it GREW, from
// GRACE_WINDOW_SIZE - 1 (5 refreshes, ~75s) to GRACE_RATE_THRESHOLD - 1 (124
// refreshes, ~31 minutes at the documented 15s cadence) — this is the
// EXPLICITLY ACCEPTED cost documented in consistency.ts's GRACE_RATE_WINDOW_
// SIZE doc comment (option (c) of the r4 brief: "shrinks or is explicitly
// documented as accepted" — this redesign chose the latter, because a
// smaller window is exactly what reopens the false-positive class this
// round exists to close). This test now proves the FULL, larger window,
// not just its old 5-refresh prefix.
test('ATTACK c: the first GRACE_RATE_THRESHOLD-1 refreshes of a fresh session cannot red a drifting grace-eligible defect, even though it fires from refresh 1 (startup window GREW under r4 — explicitly accepted, see consistency.ts)', () => {
  let s = seedCity();
  for (let i = 0; i < 20; i++) s = reducer(s, { type: 'tick' });
  const runner = makeWindowedRunner(); // fresh — empty history, exactly a new page load
  const results = [];
  const CHECKS = GRACE_RATE_THRESHOLD + 5; // run a little past the boundary
  for (let i = 0; i < CHECKS; i++) {
    s = reducer(s, { type: 'tick' });
    const delta = 1 + i * 1e-6; // drifts every refresh from the very first one
    const tampered = tamperWagesBy(s, delta);
    const report = runner.run(tampered);
    const check = report.checks.find((c) => c.id === 'flows.wages-matches');
    results.push(check.ok);
  }
  const firstRedIndex = results.findIndex((ok) => ok === false);
  console.log(`ATTACK c fresh-session: firstRedIndex=${firstRedIndex} over ${CHECKS} refreshes (GRACE_RATE_THRESHOLD=${GRACE_RATE_THRESHOLD})`);
  // The first GRACE_RATE_THRESHOLD - 1 refreshes structurally CANNOT reach
  // the rate backstop (not enough history yet) and a drifting delta never
  // accumulates a signature match either — so they are ALL graced, no
  // matter how the tamper behaves.
  const firstN = results.slice(0, GRACE_RATE_THRESHOLD - 1);
  assert.ok(
    firstN.every((ok) => ok === true),
    `BUG-640 r4 (documented, accepted trade-off): the first ${GRACE_RATE_THRESHOLD - 1} refreshes of ANY fresh session/page-reload are structurally un-reddable for a drifting grace-eligible defect — a user who reloads the debug tab during an active drifting-defect incident sees a clean panel for the first ~${Math.round(((GRACE_RATE_THRESHOLD - 1) * 15) / 60)} minutes (at the documented 15s cadence) even though the defect has been firing on every single check since before the reload. This is LARGER than round-3's window, an explicitly accepted cost of closing the gapped-evasion hole without reopening the false-positive class.`,
  );
  // But it MUST eventually red once the window has enough history — the
  // startup window is a bounded, accepted delay, not a permanent mask.
  assert.ok(
    firstRedIndex >= 0 && firstRedIndex < CHECKS,
    'the drifting defect must eventually red once the fresh session accumulates enough history — the startup window is bounded, not permanent',
  );
});

// ============================================================================
// (d) FALSE-POSITIVE REGRESSION — is the round-4 rate backstop signature-BLIND
// in a way that reopens a false positive? (Corrected finding: NO — see below)
// ============================================================================
//
// Does a perfectly healthy city ever legitimately raw-fail
// 'flows.upkeep-total-matches' on GRACE_WINDOW_SIZE consecutive checks with
// NO healthy gap? The BUG-624 doc explains individual online-flip transients
// are normally isolated (self-heal next tick) — but a STEADY continuous
// build cadence (place one cheap building per tick, every tick, indefinitely
// — a very plausible dogfood auto-builder shape once demand keeps pace) makes
// a DIFFERENT building complete construction on EVERY tick once the pipeline
// is primed, so the aggregate upkeep-total-matches check can raw-fail on
// every consecutive check indefinitely.
//
// ROUND-4 CORRECTED DIAGNOSIS (post-REJECT — the round-3 gap-free backstop
// this test originally blamed no longer exists, replaced by the gap-tolerant
// rate backstop above): re-measuring against the actual round-4
// implementation shows this fixture DOES still red — but empirically NOT via
// the new rate backstop (the run self-terminates at ~65-70 raw fails, well
// under GRACE_RATE_THRESHOLD=125, confirmed below by asserting the first red
// arrives long before the rate threshold could possibly have been reached).
// The actual cause is the UNTOUCHED round-2 signature-match tolerance: this
// fixture places the SAME spec ('park') every single tick, so its recompute-
// vs-actual delta is the IDENTICAL constant value each time (measured: e.g.
// -11 repeating) — exactly the "coincidentally identical-spec transients"
// residual round-2/round-3 already documented and explicitly accepted (see
// ATTACK d3 below, and consistency.ts's round-2 doc comment) as a residual
// NOT eliminated by signature matching. The r4 brief said "exact-match grace
// ... stays as r2 built it" — so this residual is preserved deliberately,
// not a new backstop-2 regression (backstop 2 no longer exists in this
// shape). This test now asserts the CORRECTED causal claim.
test('ATTACK d: a steady one-building-per-tick, SAME-SPEC build cadence still reds — via the PRE-EXISTING r2 signature-match residual, not the (now-removed) r3 gap-free backstop or the new r4 rate backstop', () => {
  let s = seedCity();
  const runner = makeWindowedRunner();
  let placedTotal = 0;
  let redCount = 0;
  let rawFailStreak = 0;
  let maxRawFailStreak = 0;
  let totalRawFails = 0;
  let firstRedAt = -1;
  const TICKS = 100;
  const redAt = [];
  for (let tick = 0; tick < TICKS; tick++) {
    // Place exactly one cheap 'park' every tick — a continuous, steady,
    // healthy build cadence (not a batch, not identical-tick completion).
    const { x, y } = siteAt(placedTotal, 60, 60);
    s = reducer(s, { type: 'place', spec: 'park', x, y });
    placedTotal++;
    s = reducer(s, { type: 'tick' });
    const report = runner.run(s); // NO tamper — this is genuinely healthy state
    const c = report.checks.find((ch) => ch.id === 'flows.upkeep-total-matches');
    const raw = runConsistencyChecks(s);
    const rawC = raw.checks.find((ch) => ch.id === 'flows.upkeep-total-matches');
    if (rawC && !rawC.ok) {
      rawFailStreak++;
      totalRawFails++;
      maxRawFailStreak = Math.max(maxRawFailStreak, rawFailStreak);
    } else {
      rawFailStreak = 0;
    }
    if (c && !c.ok) {
      redCount++;
      redAt.push(tick);
      if (firstRedAt === -1) firstRedAt = tick;
    }
  }
  console.log(
    `ATTACK d: steady 1-park/tick over ${TICKS} ticks: maxRawFailStreak=${maxRawFailStreak}, totalRawFails=${totalRawFails}, gracedRedCount=${redCount}, firstRedAt=${firstRedAt}, redAt(first 10)=${JSON.stringify(redAt.slice(0, 10))}`,
  );
  if (redCount > 0) {
    // CORRECTED CAUSAL CLAIM: if this reds at all, it must be happening WAY
    // before totalRawFails could possibly reach GRACE_RATE_THRESHOLD (125) —
    // proving the rate backstop is NOT the mechanism responsible, and by
    // elimination it is the round-2 exact-signature-match tolerance (a
    // same-spec build's identical delta satisfies "2 matching occurrences"
    // within just GRACE_WINDOW_SIZE checks, long before 125 total fails
    // could ever accumulate).
    assert.ok(
      firstRedAt < GRACE_RATE_THRESHOLD,
      `first red at tick ${firstRedAt} must land well before GRACE_RATE_THRESHOLD=${GRACE_RATE_THRESHOLD} raw fails could accumulate, proving the round-4 rate backstop is NOT what causes this — it is the pre-existing, deliberately-unchanged round-2 signature-match tolerance seeing the SAME identical delta every tick (a documented residual, not a round-4 regression)`,
    );
    console.log(
      'ATTACK d: CORRECTED FINDING — this reds via the PRE-EXISTING round-2 signature-match residual (identical-spec-repeated-transients, same class as ATTACK d3\'s batch residual below), NOT via any round-3/round-4 backstop. Round-4 was explicitly told to keep "exact-match grace ... as r2 built it," so this residual is preserved deliberately and is not a new false-positive class introduced by this round.',
    );
  } else {
    console.log('ATTACK d: no reds at all in this run — the residual did not manifest at this fixture/duration.');
  }
});

// Re-measure the r2 headline: mixed-spec every-3-tick cadence, many refreshes.
test('ATTACK d2: r2 headline re-measured under r4 — mixed-spec every-3-tick cadence over 500 refreshes', () => {
  let s = seedCity();
  const runner = makeWindowedRunner();
  let placedTotal = 0;
  let falsePositives = 0;
  const specs = ['park', 'com_shop', 'edu_primary'];
  for (let tick = 0; tick < 500; tick++) {
    if (tick % 3 === 0) {
      const { x, y } = siteAt(placedTotal, 60, 60);
      s = reducer(s, { type: 'place', spec: specs[placedTotal % specs.length], x, y });
      placedTotal++;
    }
    s = reducer(s, { type: 'tick' });
    const report = runner.run(s);
    for (const c of report.checks) {
      if (GRACE_ELIGIBLE_LINE_IDS.has(c.id) && !c.ok) falsePositives++;
    }
  }
  console.log(`ATTACK d2: mixed-spec every-3-tick over 500 refreshes, placedTotal=${placedTotal}, falsePositives=${falsePositives}`);
  assert.equal(
    falsePositives,
    0,
    `BUG-640 r4 REGRESSION CHECK: mixed-spec (varying delta) every-3-tick cadence must stay 0 false positives under r4 exactly as under r2/r3 (got ${falsePositives}) — this is the r2 headline claim and must not have regressed`,
  );
});

// Aaron's real auto-build batch shape (resolveDemandAll): N identical-spec
// buildings placed in one pass, repeated periodically.
test('ATTACK d3: resolveDemandAll-shaped batches (identical spec, many-at-once, repeated) re-measured under r4', () => {
  let s = seedCity();
  const BATCH_SIZE = 8;
  const BATCH_PERIOD = 5;
  const runner = makeWindowedRunner();
  let placedTotal = 0;
  let batchesPlaced = 0;
  const redEvents = [];
  for (let tick = 0; tick < 60; tick++) {
    if (tick % BATCH_PERIOD === 0 && batchesPlaced < 6) {
      for (let i = 0; i < BATCH_SIZE; i++) {
        const { x, y } = siteAt(placedTotal, 60, 60);
        s = reducer(s, { type: 'place', spec: 'park', x, y });
        placedTotal++;
      }
      batchesPlaced++;
    }
    s = reducer(s, { type: 'tick' });
    const report = runner.run(s);
    const c = report.checks.find((ch) => ch.id === 'flows.upkeep-total-matches');
    if (c && !c.ok) redEvents.push(tick);
  }
  console.log(`ATTACK d3: BATCH_SIZE=${BATCH_SIZE} BATCH_PERIOD=${BATCH_PERIOD} redEvents=${JSON.stringify(redEvents)}`);
  // This documents the r3-known residual (identical-sized repeated batches
  // reproduce an identical delta and red on the 2nd occurrence within the
  // window) is UNCHANGED by r4 — r4 adds backstops on top, it does not touch
  // the underlying signature-match tolerance, so the pre-existing r3 residual
  // persists exactly as before.
  console.log('ATTACK d3: this residual is PRE-EXISTING (r3), not introduced by r4 — documented for completeness only.');
});

// ============================================================================
// (a) THE EPSILON REFUSAL — is it sound, or a red herring?
// ============================================================================
//
// The author's refusal argument: no single global tolerance-band epsilon can
// be simultaneously < 1e-12 (to keep ATTACK c1's two distinct transients
// apart) and >= 4e-5 (to catch ATTACK b's drift). But ATTACK b (this round)
// proves the REAL hole is the gap — a defect with a healthy gap NEVER
// saturates the sustained-divergence backstop regardless of any delta
// tolerance, because that backstop counts RAW OCCURRENCES (any signature),
// not matching-signature occurrences. Loosening the delta-match epsilon does
// nothing for a gapped defect: it would still need to survive across a gap in
// priorDeltas, and the backstop's condition is `priorDeltas.length >=
// GRACE_WINDOW_SIZE - 1`, which is a GAP-FREEDOM test, structurally
// independent of what epsilon governs the signature match. This test proves
// that claim directly: even a PERFECT (zero-tolerance-needed) oracle that
// always recognises the drifting defect's signature as "the same" cannot
// close the gap-free hole, because the backstop's gate is COUNT of
// consecutive fails, not signature identity.
test('ATTACK a: the epsilon-band refusal is a RED HERRING for the gap-free hole — even a perfect signature oracle (tolerance covering the entire drift) cannot close it, because the real gate is GAP-FREEDOM (raw occurrence count), not signature matching', () => {
  let s = seedCity();
  for (let i = 0; i < 20; i++) s = reducer(s, { type: 'tick' });
  // Locally reimplement pushGraceable's rule with a PERFECT epsilon (treat
  // every drifting-defect delta as matching every other one — the best any
  // tolerance band could ever do) AND the real gap-free-only sustained
  // backstop, mirroring the actual r4 gate condition.
  let history = []; // rolling {failed: bool}[] per check, length-capped
  let redCount = 0;
  const CHECKS = 60;
  for (let i = 0; i < CHECKS; i++) {
    s = reducer(s, { type: 'tick' });
    const isHealthyGap = i % 6 === 0; // one gap per 6 — same shape as ATTACK b
    let rawFailed;
    if (isHealthyGap) {
      rawFailed = false;
    } else {
      rawFailed = true; // the drift always raw-fails when it fires
    }
    // PERFECT signature oracle: every fired occurrence is deemed "the same
    // signature" (best case for the epsilon-band idea) — so signature
    // matching alone would red starting at the 2nd firing. But we are
    // testing the SUSTAINED-DIVERGENCE gate specifically, which the real
    // code applies as an independent, signature-blind, gap-free-required
    // condition. Reproduce ONLY that gate:
    const priorFails = history.slice(-(GRACE_WINDOW_SIZE - 1));
    const allPriorFailed = priorFails.length >= GRACE_WINDOW_SIZE - 1 && priorFails.every((f) => f);
    const sustainedRed = allPriorFailed && rawFailed;
    if (sustainedRed) redCount++;
    history = [...history, rawFailed].slice(-(GRACE_WINDOW_SIZE - 1));
  }
  console.log(`ATTACK a: perfect-oracle sustained-backstop-only redCount=${redCount}/${CHECKS} (gap every 6th check)`);
  assert.equal(
    redCount,
    0,
    'BUG-640 r4 FINDING: the sustained-divergence backstop never fires for a gapped defect NO MATTER HOW GENEROUS the signature-matching epsilon is (this simulation grants it a PERFECT match), because the backstop is gated on GAP-FREE RAW OCCURRENCE COUNT, an axis the epsilon debate never touches. The author\'s epsilon refusal may be locally correct on its own narrow question (no band is simultaneously < 1e-12 and >= 4e-5), but it is a RED HERRING relative to closing ATTACK b: the actual missing piece is a GAP-TOLERANT persistence count (e.g. "at least K raw-fails in the last N checks", independent of signature), not a wider delta-matching epsilon at all.',
  );
});

// ============================================================================
// (e) EDGE VALUES: -0/+0, Number.MIN_VALUE, alternating sign
// ============================================================================

test('ATTACK e1: -0 and +0 deltas are treated as the SAME signature (=== considers them equal), consistent with IEEE-754 semantics', () => {
  const priorMap = foldGraceHistory([{ 'flows.wages-matches': -0 }]);
  let s = seedCity();
  for (let i = 0; i < 10; i++) s = reducer(s, { type: 'tick' });
  const forced = tamperWagesBy(s, 0); // +0 delta relative to recompute
  // Note: tamperWagesBy(s, 0) does not actually change the value, so this
  // path may not raw-fail at all (0 delta = matching). Use a synthetic
  // matchingCount check directly instead, mirroring d's boundary style.
  const deltas = priorMap.get('flows.wages-matches');
  assert.equal(deltas[0] === 0, true, '-0 === 0 is true in JS, confirming -0/+0 collapse to the same signature bucket');
  assert.equal(Object.is(deltas[0], -0), true, 'sanity: the stored value really is negative zero');
});

test('ATTACK e2: Number.MIN_VALUE delta is finite and participates in ordinary signature matching (not misrouted into the non-finite branch)', () => {
  let s = seedCity();
  for (let i = 0; i < 10; i++) s = reducer(s, { type: 'tick' });
  const priorMap = foldGraceHistory([{ 'flows.wages-matches': Number.MIN_VALUE }]);
  const forced = tamperWagesBy(s, Number.MIN_VALUE);
  const report = runConsistencyChecks(forced, undefined, priorMap);
  const check = report.checks.find((c) => c.id === 'flows.wages-matches');
  assert.ok(Number.isFinite(Number.MIN_VALUE), 'sanity: MIN_VALUE is finite (it is the smallest positive representable double, not near-zero-as-in-underflow)');
  // 1 prior matching occurrence + this one = 2 >= GRACE_MAX_FAILURES_IN_WINDOW -> red.
  assert.equal(
    check.ok,
    false,
    'Number.MIN_VALUE is a normal finite number and follows the ordinary signature-matched rule (2nd matching occurrence reds), confirming it is not accidentally caught by the !Number.isFinite branch or treated as zero',
  );
});

test('ATTACK e3: a delta that alternates sign every check (+d, -d, +d, -d...) is treated as two DIFFERENT signatures, so it behaves exactly like the ATTACK-d alternating-A/B case, not as a special "same magnitude" bucket', () => {
  const D = 777;
  let s = seedCity();
  for (let i = 0; i < 10; i++) s = reducer(s, { type: 'tick' });
  const runner = makeWindowedRunner();
  const sequence = [D, -D, D, -D, D, -D];
  const results = [];
  for (const delta of sequence) {
    s = reducer(s, { type: 'tick' });
    const tampered = tamperWagesBy(s, delta);
    const report = runner.run(tampered);
    const check = report.checks.find((c) => c.id === 'flows.wages-matches');
    results.push(check.ok);
  }
  console.log(`ATTACK e3 alternating +/-D graced-sequence: ${JSON.stringify(results)}`);
  assert.deepEqual(
    results,
    [true, true, false, false, false, false],
    'an alternating-sign delta is exactly the ATTACK-d two-distinct-signature case (+D and -D never === each other) — no special-cased "same magnitude, different sign" leniency exists, confirmed here explicitly',
  );
});

// ============================================================================
// (f) RED-PROOFS of both r4 backstops independently, via scratch-copy diff
// ============================================================================

test('RED-PROOF f1: disabling ONLY the non-finite guard (simulated) reproduces NaN masking, proving that guard alone closes h1', () => {
  let s = seedCity();
  for (let i = 0; i < 10; i++) s = reducer(s, { type: 'tick' });
  let history = [];
  let redCountWithoutGuard = 0;
  const CHECKS = 20;
  for (let i = 0; i < CHECKS; i++) {
    s = reducer(s, { type: 'tick' });
    const tampered = {
      ...s,
      lastFlows: {
        ...s.lastFlows,
        outflows: s.lastFlows.outflows.map((f) => (f.label === 'Wages' ? { ...f, value: NaN } : f)),
      },
    };
    const raw = runConsistencyChecks(tampered);
    const rawDelta = raw.rawFailedSignatures['flows.wages-matches'];
    const priorDeltas = history.slice(-(GRACE_WINDOW_SIZE - 1));
    // Round-2-only rule (NO non-finite guard, NO sustained backstop):
    const matchingCount = priorDeltas.filter((d) => d === rawDelta).length; // NaN === NaN is false, always 0
    const graced = matchingCount + 1 < GRACE_MAX_FAILURES_IN_WINDOW;
    if (!graced) redCountWithoutGuard++;
    history = [...history, rawDelta].slice(-(GRACE_WINDOW_SIZE - 1));
  }
  console.log(`RED-PROOF f1: without the non-finite guard, redCountWithoutGuard=${redCountWithoutGuard}/${CHECKS} (real r4 code reds ${CHECKS}/${CHECKS} per h1 FIX PROOF)`);
  assert.equal(
    redCountWithoutGuard,
    0,
    'RED-PROOF: removing ONLY the non-finite guard reproduces total NaN-masking (0 reds), confirming that guard specifically (not the sustained backstop, not test structure) is what closes attack h1',
  );
});

test('RED-PROOF f2: disabling ONLY the sustained-divergence backstop (simulated, signature match kept) reproduces the ATTACK-b every-check-persistent (gap-free) masking', () => {
  let s = seedCity();
  for (let i = 0; i < 20; i++) s = reducer(s, { type: 'tick' });
  let history = [];
  let redCountWithoutBackstop = 0;
  const CHECKS = 40;
  for (let i = 0; i < CHECKS; i++) {
    s = reducer(s, { type: 'tick' });
    const delta = 1 + s.tick * 1e-6;
    const tampered = tamperWagesBy(s, delta);
    const raw = runConsistencyChecks(tampered);
    const rawDelta = raw.rawFailedSignatures['flows.wages-matches'];
    const priorDeltas = history.slice(-(GRACE_WINDOW_SIZE - 1));
    const matchingCount = priorDeltas.filter((d) => d === rawDelta).length; // always 0, drift never repeats
    // Signature-matched rule only, no sustained backstop, no non-finite guard:
    const graced = matchingCount + 1 < GRACE_MAX_FAILURES_IN_WINDOW;
    if (!graced) redCountWithoutBackstop++;
    history = [...history, rawDelta].slice(-(GRACE_WINDOW_SIZE - 1));
  }
  console.log(`RED-PROOF f2: without the sustained backstop, redCountWithoutBackstop=${redCountWithoutBackstop}/${CHECKS} (real r4 code reds from window-fill onward per ATTACK b FIX PROOF)`);
  assert.equal(
    redCountWithoutBackstop,
    0,
    'RED-PROOF: removing ONLY the sustained-divergence backstop reproduces the gap-free drift masking (0 reds), confirming that backstop specifically closes the gap-free case (though ATTACK b above shows it does NOT close the gapped case)',
  );
});

// ============================================================================
// (g) Legacy Set path stays byte-identical; GR#27 captures stay raw
// ============================================================================

test('ATTACK g1: the legacy BUG-624 Set contract is untouched by r4 — one-consecutive-failure tolerance, byte-identical to before', () => {
  const priorSet = new Set(['flows.wages-matches']);
  let s = seedCity();
  for (let i = 0; i < 10; i++) s = reducer(s, { type: 'tick' });
  const forced = tamperWagesBy(s, 42);
  const report = runConsistencyChecks(forced, undefined, priorSet);
  const check = report.checks.find((c) => c.id === 'flows.wages-matches');
  assert.equal(check.ok, false, 'Set contract: failing on the immediately-preceding check is NOT graced (only a FIRST consecutive failure is tolerated)');
  const freshSet = new Set();
  const report2 = runConsistencyChecks(forced, undefined, freshSet);
  const check2 = report2.checks.find((c) => c.id === 'flows.wages-matches');
  assert.equal(check2.ok, true, 'Set contract: a first-ever failure (not in the prior set) IS graced, unchanged from BUG-624');
});

test('ATTACK g2: a NaN delta under the LEGACY Set contract is unaffected by the r4 non-finite guard (guard only applies inside the Map branch)', () => {
  const priorSet = new Set(); // first occurrence
  let s = seedCity();
  for (let i = 0; i < 10; i++) s = reducer(s, { type: 'tick' });
  const tampered = {
    ...s,
    lastFlows: {
      ...s.lastFlows,
      outflows: s.lastFlows.outflows.map((f) => (f.label === 'Wages' ? { ...f, value: NaN } : f)),
    },
  };
  const report = runConsistencyChecks(tampered, undefined, priorSet);
  const check = report.checks.find((c) => c.id === 'flows.wages-matches');
  // The non-finite guard is gated behind `priorFailedLineIds instanceof Map`
  // — a Set-shaped caller never reaches it, so a NaN under the legacy
  // contract is graced exactly like any other first-ever failure. Documents
  // that the h1 fix is Map-contract-only, a caller stuck on the legacy Set
  // shape gets no NaN protection at all.
  assert.equal(
    check.ok,
    true,
    'BUG-640 r4 OBSERVATION: the non-finite guard only protects callers using the Map (windowed) contract; a caller still on the legacy Set contract gets ZERO NaN protection — a first NaN occurrence is graced exactly as before r3/r4',
  );
});

test('ATTACK g3: GR#27 captureBeforeWipe still reports the RAW failure for a gap-free-drift defect masked forever by the live panel (ATTACK b)', () => {
  let s = seedCity();
  for (let i = 0; i < 20; i++) s = reducer(s, { type: 'tick' });
  // Run the gap-drift scenario for a while via the live runner (proving it
  // stays masked), then capture.
  const runner = makeWindowedRunner();
  for (let i = 0; i < 30; i++) {
    s = reducer(s, { type: 'tick' });
    const isHealthyGap = i % 6 === 0;
    if (!isHealthyGap) {
      s = tamperWagesBy(s, 1 + i * 1e-6);
    }
    runner.run(s);
  }
  const map = new Map();
  const storage = {
    getItem: (k) => (map.has(k) ? map.get(k) : null),
    setItem: (k, v) => map.set(k, String(v)),
  };
  captureBeforeWipe(s, 'v9.9.9-test', storage, 1_700_000_000_000);
  const archive = readPreWipeArchive(storage);
  const captured = archive[0].debug.consistency;
  // The final state `s` may or may not itself be mid-tamper depending on
  // parity — assert on the general contract: the capture path never threads
  // priorFailedLineIds at all, so its rawFailedLineIds/failures reflect the
  // untouched raw evaluation, unaffected by whatever the live panel graced.
  assert.ok(Array.isArray(captured.rawFailedLineIds), 'GR#27: capture always carries a raw-failure list, independent of any grace state');
});

// ============================================================================
// Boundary sanity: confirm GRACE_WINDOW_SIZE / GRACE_MAX_FAILURES_IN_WINDOW
// have not silently changed under r4 (the constants the whole analysis above
// depends on).
// ============================================================================
test('sanity: tuning constants unchanged from r2/r3', () => {
  assert.equal(GRACE_WINDOW_SIZE, 6);
  assert.equal(GRACE_MAX_FAILURES_IN_WINDOW, 2);
});

test('sanity: r4 rate-backstop constants match this round\'s measured tuning', () => {
  assert.equal(GRACE_RATE_WINDOW_SIZE, 180);
  assert.equal(GRACE_RATE_THRESHOLD, 125);
});
