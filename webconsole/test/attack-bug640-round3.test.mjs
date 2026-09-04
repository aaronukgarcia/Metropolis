// attack-bug640-round3.test.mjs — INDEPENDENT DESTRUCTIVE RE-ROUND (GR#23) on
// BUG-640 r2 (consistency.ts's signature-matched windowed grace:
// foldGraceHistory / GRACE_WINDOW_SIZE / GRACE_MAX_FAILURES_IN_WINDOW /
// pushGraceable's instanceof Map/Set dispatch). Not the author's test;
// written by the attacking session per the r3 brief:
//   (a) THE RESIDUAL — a real dogfood auto-build batch cadence (identical
//       spec, many-at-once, repeated) vs the "accepted residual".
//   (b) DEFECT MASKING — a genuine defect whose delta drifts every check
//       (population-linked) is never caught: the mirror failure.
//   (c) floating-point equality edges on the delta signature.
//   (d) exact window/threshold boundary arithmetic with alternating deltas.
//   (e) type-versioning: a mixed array of [Set, Record] history entries.
//   (f) GR#27 capture-before-wipe stays RAW even under a masked-forever defect.
//   (g) two independent RED-proofs of the author's own assertions.
//   (h) junk deltas: NaN / Infinity / undefined in a signature record.

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
// `GRACE_RATE_WINDOW_SIZE - 1` (not `GRACE_WINDOW_SIZE - 1`) so the round-4
// gap-tolerant rate backstop ever has enough history to fire — see
// consistency.ts's GRACE_RATE_WINDOW_SIZE doc comment and debugTab.tsx's
// graceHistoryRef. Widening this cap does NOT change any test below that
// exercises the round-2 signature-match tolerance (ATTACK a/d, h2) — that
// tolerance is now scoped internally to just the trailing
// GRACE_WINDOW_SIZE - 1 snapshots regardless of how much history the caller
// supplies (see foldGraceHistory's side channel) — it only enables ATTACK
// b's drifting-defect case to ever reach the (much larger) rate backstop.
function makeWindowedRunner() {
  let history = [];
  return {
    run(s) {
      const report = runConsistencyChecks(s, undefined, foldGraceHistory(history));
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

// ===== (a) THE RESIDUAL: real dogfood auto-build BATCH cadence =====
//
// Aaron's dogfood cities auto-build ~150% of demand in BATCHES of the SAME
// cheapest spec (resolveDemandAll), not one building placed every few ticks
// (the author's ATTACK 1c fixture). A batch of N identical-spec buildings
// placed on the SAME tick all complete construction on the SAME tick too
// (identical builtTick + identical constructionTicks(sp)), so the online-flip
// fires as ONE event whose magnitude is N x the per-building upkeep delta —
// and if auto-build repeats the SAME-SIZED batch of the SAME spec (a
// perfectly plausible "keep building parks to meet demand" pattern), that
// magnitude repeats EXACTLY across batches. Under the round-2 rule (grace
// only while matchingCount+1 < GRACE_MAX_FAILURES_IN_WINDOW=2, i.e. the
// FIRST occurrence of a delta is graced and the SECOND is NOT), the second
// same-sized batch within one GRACE_WINDOW_SIZE of the first REDS — for a
// perfectly healthy, continuously-growing city doing nothing but what
// Aaron's own dogfood auto-builder does.
test('ATTACK a: real dogfood BATCH auto-build (identical spec, many-at-once, repeated) reds on the 2nd batch', () => {
  let s = seedCity();
  const BATCH_SIZE = 8;
  const BATCH_PERIOD = 5; // ticks between batches — well under GRACE_WINDOW_SIZE=6
  const runner = makeWindowedRunner();
  let placedTotal = 0;
  let batchesPlaced = 0;
  let redAtBatch = [];
  for (let tick = 0; tick < 60; tick++) {
    if (tick % BATCH_PERIOD === 0 && batchesPlaced < 6) {
      // One whole batch of IDENTICAL spec placed on the SAME tick — this is
      // resolveDemandAll's actual shape: "build enough parks to cover the
      // shortfall right now", not one building trickled in.
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
    if (c && !c.ok) redAtBatch.push({ tick, batchesPlaced });
  }
  console.log(
    `ATTACK a: BATCH_SIZE=${BATCH_SIZE} BATCH_PERIOD=${BATCH_PERIOD} batchesPlaced=${batchesPlaced} redEvents=${JSON.stringify(redAtBatch)}`,
  );
  // FINDING: if identical-sized repeated batches produce a matching delta,
  // the SECOND (and every subsequent) same-sized batch within the window
  // reds — a perfectly healthy, continuously-growing dogfood city triggers
  // the consistency panel exactly the way BUG-640 r1 was rejected for. This
  // assertion documents whichever way the fix actually behaves; a failure
  // here means the residual claimed "9.8% homogeneous-cadence only" under-
  // states the real blast radius once REAL auto-build batching (not a
  // single trickled building) is modelled.
  assert.ok(
    redAtBatch.length > 0,
    'BUG-640 r3 FINDING: repeated same-sized same-spec auto-build BATCHES (the real resolveDemandAll shape, not a trickled single building) reproduce an identical delta and RED the panel on a perfectly healthy, continuously-growing dogfood city — the "9.8% homogeneous residual" undersells the real-world blast radius, because real auto-build is batchy, not one-at-a-time',
  );
});

// ===== (b) DEFECT MASKING — the mirror failure =====
//
// A defect whose divergence DRIFTS every single check (e.g. wrong-by-a-
// population-term, and population keeps growing tick over tick) never
// repeats an IDENTICAL delta -> under the round-2 rule it was graced
// FOREVER, even though it fires on literally every consecutive check (which
// the ORIGINAL pre-BUG-624 code, and even the plain BUG-624 two-consecutive
// rule, would have caught instantly). This was more dangerous than the
// false-positive residual: a silent, permanent false NEGATIVE.
//
// ROUND-3 FIX (post-REJECT, 2026-09-03): consistency.ts's pushGraceable now
// layers a SIGNATURE-BLIND sustained-divergence backstop on top of the
// signature match — if a grace-eligible id raw-fails on literally EVERY ONE
// of the last GRACE_WINDOW_SIZE consecutive checks (regardless of whether
// the deltas match each other), it reds unconditionally. This drifting
// defect fires on every single check, so it saturates the window and reds
// starting once the window fills (empirically: 35/40, i.e. from check 6
// onward — the first `GRACE_WINDOW_SIZE - 1` checks are still legitimately
// building up the history). Assertion FLIPPED below from "must never red"
// to "must eventually red, and keep redding."
//
// ROUND-4 UPDATE (post-REJECT, same day — "the gap-free hole"): the small
// gap-free-only backstop this test originally proved is GONE, REPLACED by a
// much larger gap-tolerant rate backstop (GRACE_RATE_WINDOW_SIZE=180 /
// GRACE_RATE_THRESHOLD=125 — see consistency.ts's doc comment). This
// 100%-duty (gap-free) drifting defect is exactly the shape BOTH mechanisms
// were built to catch, so it still gets caught here — just later
// (empirically once total raw fails cross ~125, not 6), because the round-4
// redesign intentionally trades speed for closing a real false-positive
// class a small window could not avoid. CHECKS raised from 40 to 150
// accordingly.
test('ATTACK b FIX PROOF: a persistent defect whose delta drifts with population is caught by the round-4 gap-tolerant rate backstop', () => {
  let s = seedCity();
  // Let the city run and grow population organically for a while first.
  for (let i = 0; i < 20; i++) s = reducer(s, { type: 'tick' });
  const runner = makeWindowedRunner();
  let redCount = 0;
  let rawFailCount = 0;
  const CHECKS = 150;
  for (let i = 0; i < CHECKS; i++) {
    s = reducer(s, { type: 'tick' });
    // Simulate a genuine wrong-by-a-drifting-basis defect: the tampered
    // "actual" Wages outflow is off by a tiny amount keyed to `s.tick`
    // (which STRICTLY increases every single tick, unlike population, which
    // in this fixture's small city can plateau for several ticks in a row —
    // an earlier draft of this attack used population and accidentally
    // produced REPEATING deltas whenever population happened to plateau,
    // which defeated the very scenario it meant to construct). `s.tick` is
    // a stand-in for any real per-tick-varying basis a genuine formula bug
    // could be indexed on (population, cumulative spend, elapsed ticks...).
    const tampered = tamperWagesBy(s, 1 + s.tick * 1e-6);
    const rawReport = runConsistencyChecks(tampered);
    const rawCheck = rawReport.checks.find((c) => c.id === 'flows.wages-matches');
    if (rawCheck && !rawCheck.ok) rawFailCount++;
    const graced = runner.run(tampered);
    const check = graced.checks.find((c) => c.id === 'flows.wages-matches');
    if (check && !check.ok) redCount++;
  }
  console.log(
    `ATTACK b FIX PROOF: over ${CHECKS} consecutive checks, rawFailCount=${rawFailCount} (should be ${CHECKS}, this IS a genuine every-check defect), sustained-divergence-backstopped redCount=${redCount}`,
  );
  assert.equal(rawFailCount, CHECKS, 'sanity: the tamper is a genuine defect firing on every single raw check');
  // FIX PROOF (round-4 mechanism): the gap-tolerant rate backstop is
  // signature- AND gap-BLIND — it fires purely on "raw-failed at least
  // GRACE_RATE_THRESHOLD times within the last GRACE_RATE_WINDOW_SIZE
  // checks", so a drifting delta can no longer hide behind never-repeating
  // signatures. It cannot fire before enough history has accumulated (the
  // startup window — see attack-bug640-round4.test.mjs's ATTACK c), but once
  // the running count of this always-failing defect crosses the threshold,
  // it reds every remaining check (the count only ever grows for a defect
  // that never stops firing).
  assert.ok(
    redCount > 0,
    'BUG-640 r4 FIX: a genuine, every-check-persistent defect whose delta drifts with population must eventually red via the gap-tolerant rate backstop, even though signature matching alone would mask it forever',
  );
  assert.ok(
    redCount >= CHECKS - GRACE_RATE_THRESHOLD,
    `BUG-640 r4 FIX: once the rate threshold is crossed, EVERY subsequent check of this always-failing defect must red (got redCount=${redCount} of ${CHECKS}, threshold=${GRACE_RATE_THRESHOLD}) — the backstop must not merely fire once and then go back to sleep`,
  );
});

// ===== (c) FLOATING POINT EQUALITY EDGES =====

test('ATTACK c1: two genuine transients differing at 1e-12 are (correctly) treated as different signatures — no false grace-denial', () => {
  const priorMap = foldGraceHistory([{ 'flows.wages-matches': 5.000000000001 }]);
  let s = seedCity();
  for (let i = 0; i < 10; i++) s = reducer(s, { type: 'tick' });
  const targetDelta = 5.000000000002;
  const forced = tamperWagesBy(s, targetDelta);
  const report = runConsistencyChecks(forced, undefined, priorMap);
  const check = report.checks.find((c) => c.id === 'flows.wages-matches');
  assert.equal(
    check.ok,
    true,
    'two floats differing at the 1e-12 place are NOT === in JS, so this is correctly treated as a fresh (1st) occurrence of ITS OWN signature and stays graced — exact equality does not falsely deny grace to genuinely-distinct transients',
  );
});

test('ATTACK c2: two occurrences of what SHOULD be "the same" defect, differing only by float rounding noise, are never recognised as a repeat (never reds)', () => {
  // A real recompute chain (division, multiplication, accumulation order)
  // can produce a "logically identical" delta that differs in its last bit
  // between two calls even with NO population/state drift at all — pure
  // floating-point non-associativity. Model this directly: two deltas that
  // are equal to 15 decimal places but not bit-identical.
  const a = 0.1 + 0.2; // 0.30000000000000004
  const b = 0.3; // exactly 0.3 in IEEE-754 double
  assert.notEqual(a, b, 'sanity: this is the classic float non-associativity gap');
  const priorMap = foldGraceHistory([{ 'flows.wages-matches': a }]);
  let s = seedCity();
  for (let i = 0; i < 10; i++) s = reducer(s, { type: 'tick' });
  const forced = tamperWagesBy(s, b);
  const report = runConsistencyChecks(forced, undefined, priorMap);
  const check = report.checks.find((c) => c.id === 'flows.wages-matches');
  // FINDING (documented, mirrors b): a defect that recomputes to "the same"
  // value up to float noise is NEVER counted as a repeat by exact `===`,
  // so it is graced again rather than red — the SAME masking mechanism as
  // ATTACK b, but triggered by arithmetic noise alone, with zero state
  // drift required.
  assert.equal(
    check.ok,
    true,
    'BUG-640 r3 FINDING: floating-point rounding noise alone (no population/state drift needed) is enough to defeat signature matching — "the same" recurring defect can present a different float each time purely from computation order and never accumulate toward the threshold',
  );
});

// ===== (d) EXACT WINDOW/THRESHOLD BOUNDARY — alternating A,B,A,B =====

test('ATTACK d: 3 distinct deltas across 6 refreshes never accumulate (3 separate 1st-occurrence graces)', () => {
  const snapshots = [
    { 'flows.wages-matches': 111 },
    { 'flows.wages-matches': 222 },
    { 'flows.wages-matches': 333 },
  ];
  const folded = foldGraceHistory(snapshots);
  const deltas = folded.get('flows.wages-matches');
  assert.deepEqual(deltas, [111, 222, 333]);
  // A 4th occurrence of ANY of these three deltas would be a 2nd MATCHING
  // occurrence (red); a 4th occurrence of a NEW delta stays a 1st (graced).
  for (const d of [111, 222, 333]) {
    const matching = deltas.filter((x) => x === d).length;
    assert.equal(matching, 1, `delta ${d} appears exactly once — its next occurrence would be the 2nd (reds)`);
  }
});

test('ATTACK d: alternating A,B,A,B,A,B reds starting at the 3rd refresh (the 2nd occurrence of A)', () => {
  const A = 444;
  const B = 555;
  let s = seedCity();
  for (let i = 0; i < 10; i++) s = reducer(s, { type: 'tick' });
  const runner = makeWindowedRunner();
  const sequenceDeltas = [A, B, A, B, A, B];
  const results = [];
  for (const delta of sequenceDeltas) {
    s = reducer(s, { type: 'tick' });
    const tampered = tamperWagesBy(s, delta);
    const report = runner.run(tampered);
    const check = report.checks.find((c) => c.id === 'flows.wages-matches');
    results.push(check.ok);
  }
  console.log(`ATTACK d alternating A,B,A,B,A,B graced-sequence: ${JSON.stringify(results)}`);
  // refresh1 = A (1st ever) -> graced=true
  // refresh2 = B (1st ever) -> graced=true
  // refresh3 = A (2nd occurrence of A, within window) -> NOT graced (red)
  // refresh4 = B (2nd occurrence of B, within window) -> NOT graced (red)
  // refresh5 = A (3rd occurrence, still >= threshold) -> NOT graced (red)
  // refresh6 = B (3rd occurrence) -> NOT graced (red)
  assert.deepEqual(
    results,
    [true, true, false, false, false, false],
    'BUG-640 r3: exact boundary proof — an alternating A,B,A,B,A,B defect (period 2, well inside GRACE_WINDOW_SIZE=6) is graced for exactly the first occurrence of each value and REDS from the 2nd occurrence of each onward, since GRACE_MAX_FAILURES_IN_WINDOW=2 tolerates only ONE prior matching delta',
  );
});

// ===== (e) TYPE-VERSIONING: mixed array of [Set, Record] history entries =====
//
// If a hot-reload or an older DebugTab bundle briefly coexists with the new
// one (or a stale closure holds an old-shape ref across an HMR boundary),
// graceHistoryRef.current could plausibly contain a MIX of legacy Set
// entries (the pre-round-2 BUG-624 shape) and new Record<string,number>
// snapshots. foldGraceHistory must not crash on that mix.
test('ATTACK e: foldGraceHistory on a MIXED array of [Set, Record, Record] does not crash and only folds the Record entries', () => {
  const legacySetEntry = new Set(['flows.wages-matches']); // wrong shape, pre-round-2
  const recordEntry1 = { 'flows.wages-matches': 42 };
  const recordEntry2 = { 'flows.upkeep-total-matches': -7 };
  let folded;
  assert.doesNotThrow(() => {
    folded = foldGraceHistory([legacySetEntry, recordEntry1, recordEntry2]);
  }, 'a mixed-shape history array must never crash the debug panel');
  // Object.keys(Set-instance) is [] (a Set has no own enumerable string
  // keys), so the legacy entry silently contributes nothing — degrade
  // safely, not a crash, but ALSO not an error surfaced anywhere: a stray
  // Set in the history is invisibly dropped rather than logged.
  assert.deepEqual(folded.get('flows.wages-matches'), [42], 'the Set entry contributes nothing; only the Record entry is folded');
  assert.deepEqual(folded.get('flows.upkeep-total-matches'), [-7]);
});

test('ATTACK e2: foldGraceHistory on an array containing a plain array (not Set/Record) as one snapshot does not crash', () => {
  let folded;
  assert.doesNotThrow(() => {
    folded = foldGraceHistory([['flows.wages-matches'], { 'flows.wages-matches': 9 }]);
  });
  // Object.keys(['flows.wages-matches']) === ['0'] — a numeric index, not a
  // GRACE_ELIGIBLE_LINE_IDS member, so it is filtered out by the `.has(id)`
  // guard, not because the shape was rejected outright. Confirm this stays
  // inert rather than accidentally polluting the map under key "0".
  assert.equal(folded.has('0'), false, 'a plain-array snapshot must not leak a numeric-index entry into the fold');
  assert.deepEqual(folded.get('flows.wages-matches'), [9]);
});

// ===== (f) GR#27 — capture-before-wipe stays RAW even under the masked-forever defect =====

test('ATTACK f: captureBeforeWipe still reports the RAW failure for a defect that the live panel (attack b) would mask forever', () => {
  let s = seedCity();
  for (let i = 0; i < 20; i++) s = reducer(s, { type: 'tick' });
  const tampered = tamperWagesBy(s, 1 + s.tick * 1e-6);
  const map = new Map();
  const storage = {
    getItem: (k) => (map.has(k) ? map.get(k) : null),
    setItem: (k, v) => map.set(k, String(v)),
  };
  captureBeforeWipe(tampered, 'v9.9.9-test', storage, 1_700_000_000_000);
  const archive = readPreWipeArchive(storage);
  const captured = archive[0].debug.consistency;
  assert.ok(
    captured.rawFailedLineIds.includes('flows.wages-matches'),
    'GR#27: the forensic capture must show the RAW failure unconditionally — the capture path never threads grace at all (no priorFailedLineIds argument), so it is unaffected either way by whether the live panel eventually catches this defect via the sustained-divergence backstop (ATTACK b) or not',
  );
  assert.ok(captured.failures > 0);
});

// ===== (g) RED-PROOFS of the author's own assertions =====

test('RED-PROOF g0: disabling the sustained-divergence backstop (simulated locally) reproduces the ATTACK-b masking exactly, proving the backstop — not incidental test structure — is what closes it', () => {
  // Re-derive ATTACK b's own drifting-delta scenario, but fold the history
  // through a LOCALLY reimplemented rule that has ONLY the round-2
  // signature-matched tolerance (no sustained-divergence check, no
  // non-finite guard) — i.e. exactly the pre-round-3 algorithm. If this
  // reproduces "0 reds over 40 checks" (the original round-3 finding),
  // that proves the real module's round-3 backstop — not some other change
  // — is what makes the real ATTACK b test now red.
  let s = seedCity();
  for (let i = 0; i < 20; i++) s = reducer(s, { type: 'tick' });
  let history = []; // rolling queue of {id: delta} snapshots
  let redCountWithoutBackstop = 0;
  for (let i = 0; i < 40; i++) {
    s = reducer(s, { type: 'tick' });
    const delta = 1 + s.tick * 1e-6;
    const tampered = tamperWagesBy(s, delta);
    const raw = runConsistencyChecks(tampered);
    const rawDelta = raw.rawFailedSignatures['flows.wages-matches'];
    const priorDeltas = history.filter((h) => h.id === 'flows.wages-matches').map((h) => h.delta);
    const matchingCount = priorDeltas.filter((d) => d === rawDelta).length;
    // round-2-ONLY rule: no sustained-divergence backstop, no non-finite guard.
    const graced = matchingCount + 1 < GRACE_MAX_FAILURES_IN_WINDOW;
    if (!graced) redCountWithoutBackstop++;
    history = [...history, { id: 'flows.wages-matches', delta: rawDelta }].slice(-(GRACE_WINDOW_SIZE - 1));
  }
  console.log(`RED-PROOF g0: round-2-only (no backstop) reimplementation redCount=${redCountWithoutBackstop}/40 (real code now reds via the round-3 backstop — see ATTACK b FIX PROOF)`);
  assert.equal(
    redCountWithoutBackstop,
    0,
    'RED-PROOF: with ONLY the round-2 signature-matched rule (no sustained-divergence backstop), the drifting defect is masked forever — exactly the ATTACK-b finding — confirming the round-3 backstop is what closes it, not incidental test structure',
  );
});

test('RED-PROOF g1: quantizing the delta signature (instead of exact ===) WOULD catch the ATTACK-b masked defect, proving exact equality is the root cause of the masking', () => {
  // Re-derive the exact same population-scaled tamper as ATTACK b, but this
  // time fold the history through a LOCALLY reimplemented rule that matches
  // on a QUANTIZED delta (rounded to, say, 2 significant scaled units)
  // rather than bit-exact equality — never touching the real module. If
  // quantizing turns the masked-forever defect into a caught one, that
  // proves the CURRENT code's exact-`===` choice (not some other factor) is
  // what causes the ATTACK-b masking.
  let s = seedCity();
  for (let i = 0; i < 20; i++) s = reducer(s, { type: 'tick' });
  let history = []; // rolling queue of {id: delta} snapshots, quantized rule
  const quantize = (d) => Math.round(d); // coarse bucket — collapses the tiny per-tick drift
  let redCountQuantized = 0;
  for (let i = 0; i < 40; i++) {
    s = reducer(s, { type: 'tick' });
    const delta = 1 + s.tick * 1e-6;
    const tampered = tamperWagesBy(s, delta);
    const raw = runConsistencyChecks(tampered);
    const rawDelta = raw.rawFailedSignatures['flows.wages-matches'];
    assert.equal(typeof rawDelta, 'number');
    const bucket = quantize(rawDelta);
    const priorMatching = history.filter((h) => h === bucket).length;
    const graced = priorMatching + 1 < GRACE_MAX_FAILURES_IN_WINDOW;
    if (!graced) redCountQuantized++;
    history = [...history, bucket].slice(-(GRACE_WINDOW_SIZE - 1));
  }
  console.log(`RED-PROOF g1: quantized-delta reimplementation redCount=${redCountQuantized}/40 (real code reds 0/40 for the identical tamper — see ATTACK b)`);
  assert.ok(
    redCountQuantized > 0,
    'RED-PROOF: a quantized-delta comparison catches the same defect ATTACK b proved is masked forever by exact ===, confirming exact float equality (not some unrelated factor) is the specific root cause of the masking finding',
  );
});

test('RED-PROOF g2: the author\'s ATTACK-2a-style boundary claim ("2nd matching occurrence reds") is exactly GRACE_MAX_FAILURES_IN_WINDOW-1 matching priors, not GRACE_WINDOW_SIZE-1', () => {
  // The author's doc comment ties the tolerance to GRACE_MAX_FAILURES_IN_WINDOW
  // (currently 2), independent of GRACE_WINDOW_SIZE (currently 6). Prove the
  // two constants are NOT accidentally conflated: construct a history with
  // GRACE_MAX_FAILURES_IN_WINDOW - 1 matching priors (currently: exactly 1)
  // sitting inside a window far shorter than GRACE_WINDOW_SIZE, and confirm
  // it still reds on the next matching occurrence — the threshold check is
  // purely a matching-count comparison, not a window-size comparison in
  // disguise.
  const priorDeltas = Array(GRACE_MAX_FAILURES_IN_WINDOW - 1).fill(999);
  const priorMap = foldGraceHistory([
    Object.fromEntries(priorDeltas.map((d, i) => [i === 0 ? 'flows.wages-matches' : `unused-${i}`, d])),
  ]);
  let s = seedCity();
  for (let i = 0; i < 10; i++) s = reducer(s, { type: 'tick' });
  const forced = tamperWagesBy(s, 999);
  const report = runConsistencyChecks(forced, undefined, priorMap);
  const check = report.checks.find((c) => c.id === 'flows.wages-matches');
  assert.equal(
    check.ok,
    false,
    'RED-PROOF: exactly GRACE_MAX_FAILURES_IN_WINDOW-1 (=1) prior matching occurrences plus this one (=2) must red — this is a pure matching-COUNT threshold, confirming the author\'s comment is accurate and the two tuning constants are not conflated in the implementation',
  );
});

// ===== (h) JUNK DELTAS: NaN / Infinity / undefined in a signature record =====

// ROUND-3 FIX: pushGraceable now treats a non-finite delta (NaN or Infinity)
// as NEVER graceable, independent of history — see consistency.ts's
// `!Number.isFinite(delta)` branch. Assertion FLIPPED below.
test('ATTACK h1 FIX PROOF: a NaN delta (e.g. a divide-by-zero defect) reds on EVERY occurrence, never masked', () => {
  let s = seedCity();
  for (let i = 0; i < 10; i++) s = reducer(s, { type: 'tick' });
  const runner = makeWindowedRunner();
  let redCount = 0;
  let rawFailCount = 0;
  for (let i = 0; i < 20; i++) {
    s = reducer(s, { type: 'tick' });
    // Force the ACTUAL Wages outflow to NaN directly (models a corrupted
    // upstream computation, e.g. 0/0 in a rate formula) so delta = NaN - recomputed = NaN.
    const tampered = {
      ...s,
      lastFlows: {
        ...s.lastFlows,
        outflows: s.lastFlows.outflows.map((f) => (f.label === 'Wages' ? { ...f, value: NaN } : f)),
      },
    };
    const raw = runConsistencyChecks(tampered);
    const rawDelta = raw.rawFailedSignatures['flows.wages-matches'];
    assert.ok(Number.isNaN(rawDelta), 'sanity: the tamper produces a NaN delta');
    if (!raw.checks.find((c) => c.id === 'flows.wages-matches').ok) rawFailCount++;
    const graced = runner.run(tampered);
    if (!graced.checks.find((c) => c.id === 'flows.wages-matches').ok) redCount++;
  }
  assert.equal(rawFailCount, 20, 'sanity: the NaN corruption is a genuine every-check defect');
  // FIX PROOF: non-finite deltas are never graceable at all, regardless of
  // window/history state, so this reds from the very FIRST occurrence —
  // strictly stronger than merely "eventually" catching it via the
  // sustained-divergence backstop (which would also eventually catch it,
  // but only after the window filled).
  assert.equal(
    redCount,
    20,
    'BUG-640 r3 FIX: a NaN-producing defect (a corrupted upstream NaN propagating into Wages, e.g. a divide-by-zero) must red on EVERY occurrence, starting immediately — treating a non-finite delta as never-graceable means it can never hide behind `NaN === NaN` being false, unlike ordinary signature matching',
  );
});

test('ATTACK h2: an Infinity delta DOES correctly accumulate and reds (Infinity === Infinity is true)', () => {
  let s = seedCity();
  for (let i = 0; i < 10; i++) s = reducer(s, { type: 'tick' });
  const runner = makeWindowedRunner();
  let redCount = 0;
  for (let i = 0; i < 10; i++) {
    s = reducer(s, { type: 'tick' });
    const tampered = {
      ...s,
      lastFlows: {
        ...s.lastFlows,
        outflows: s.lastFlows.outflows.map((f) => (f.label === 'Wages' ? { ...f, value: Infinity } : f)),
      },
    };
    const graced = runner.run(tampered);
    if (!graced.checks.find((c) => c.id === 'flows.wages-matches').ok) redCount++;
  }
  // Unlike NaN, Infinity === Infinity is true in JS, so this DOES accumulate
  // matching occurrences and reds from the 2nd check onward.
  assert.ok(redCount > 0, 'an Infinity-producing defect (unlike a NaN one) is correctly caught, since Infinity self-equals');
});

test('ATTACK h3: an undefined delta entry in a history snapshot self-matches (undefined === undefined) rather than crashing', () => {
  const priorMap = foldGraceHistory([{ 'flows.wages-matches': undefined }]);
  assert.deepEqual(priorMap.get('flows.wages-matches'), [undefined]);
  const matching = priorMap.get('flows.wages-matches').filter((d) => d === undefined).length;
  assert.equal(matching, 1, 'undefined self-matches under === , so a repeated-undefined-delta defect would still accumulate toward the threshold, unlike NaN');
});
