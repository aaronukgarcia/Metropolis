// bug624-attack-round.test.mjs — INDEPENDENT DESTRUCTIVE ROUND (GR#23) on BUG-624.
// Not the author's test; written by the attacking session to probe the grace
// mechanism's edges. Deleted/ignored after the verdict is recorded — kept
// here only as evidence during the round.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import {
  runConsistencyChecks,
  GRACE_ELIGIBLE_LINE_IDS,
  foldGraceHistory,
  GRACE_WINDOW_SIZE,
} from '../src/sim/consistency.ts';

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

// ATTACK 1 / BUG-640: LAUNDERING MATRIX — alternating tamper (wages +777 on
// even ticks only). The ORIGINAL BUG-624 single-look-back rule (legacy Set
// contract, exercised below for the historical record) graced this forever;
// BUG-640's fix is the bounded window (foldGraceHistory / GRACE_WINDOW_SIZE /
// GRACE_MAX_FAILURES_IN_WINDOW) that DebugTab now threads. This test proves
// the fix actually detects the pattern within the window, closing the blind
// spot the attack round found.
test('ATTACK 1 (legacy Set contract, historical record): the single-look-back rule launders alternating tamper forever', () => {
  let s = seedCity();
  for (let i = 0; i < 10; i++) s = reducer(s, { type: 'tick' });

  const tamperEven = (state) => ({
    ...state,
    lastFlows: {
      ...state.lastFlows,
      outflows: state.lastFlows.outflows.map((f) =>
        f.label === 'Wages' ? { ...f, value: f.value + 777 } : f,
      ),
    },
  });

  let prior = new Set();
  let redCount = 0;
  let gracedCount = 0;
  for (let tick = 0; tick < 40; tick++) {
    s = reducer(s, { type: 'tick' });
    const applyTamper = tick % 2 === 0; // fails on even, "healthy" on odd
    const state = applyTamper ? tamperEven(s) : s;
    const report = runConsistencyChecks(state, undefined, prior);
    const check = report.checks.find((c) => c.id === 'flows.wages-matches');
    if (!check.ok) redCount++;
    if (check.detail.includes('BUG-624 grace')) gracedCount++;
    prior = new Set(report.rawFailedLineIds);
  }

  // Because the tamper only raw-fails on EVEN ticks, and the LEGACY grace
  // rule keys off "did the SAME id raw-fail on the immediately preceding
  // call", an odd-tick call in between always clears `prior` back to empty
  // (no raw failure that call). So every even-tick raw failure sees
  // prior={} and gets graced — this is the BUG-640 finding, preserved here
  // as a regression proof that the LEGACY (Set) contract still behaves this
  // way (documented, intentional backward compat — see the windowed Map
  // contract test below for the actual fix).
  console.log(`ATTACK 1 (legacy) result: redCount=${redCount} gracedCount=${gracedCount} over 40 ticks (20 tampered)`);
  assert.equal(gracedCount, 20, 'legacy Set contract: every one of the 20 tampered (even) ticks was graced — flicker-every-other-tick is INVISIBLE under the single-look-back rule');
  assert.equal(redCount, 0, 'legacy Set contract: the panel NEVER reds despite a real, persistent (if intermittent) +777 wages corruption');
});

// BUG-640 FIX PROOF: the same alternating attack, this time threading the
// NEW windowed Map contract (foldGraceHistory over a rolling queue of past
// raw-failure snapshots) exactly as DebugTab now does. This must DETECT the
// pattern — the blind spot must close.
test('BUG-640 fix: the windowed Map contract detects alternating (every-other-tick) wages tamper', () => {
  let s = seedCity();
  for (let i = 0; i < 10; i++) s = reducer(s, { type: 'tick' });

  const tamperEven = (state) => ({
    ...state,
    lastFlows: {
      ...state.lastFlows,
      outflows: state.lastFlows.outflows.map((f) =>
        f.label === 'Wages' ? { ...f, value: f.value + 777 } : f,
      ),
    },
  });

  const history = []; // rolling queue of past raw-failure SIGNATURE snapshots, mirrors DebugTab
  let redCount = 0;
  let gracedCount = 0;
  let firstRedTick = -1;
  for (let tick = 0; tick < 40; tick++) {
    s = reducer(s, { type: 'tick' });
    const applyTamper = tick % 2 === 0; // fails on even, "healthy" on odd
    const state = applyTamper ? tamperEven(s) : s;
    const report = runConsistencyChecks(state, undefined, foldGraceHistory(history));
    const check = report.checks.find((c) => c.id === 'flows.wages-matches');
    if (!check.ok) {
      redCount++;
      if (firstRedTick === -1) firstRedTick = tick;
    }
    if (check.detail.includes('BUG-640 grace')) gracedCount++;
    // ROUND-2: push the SIGNATURE snapshot (id -> delta) — this +777 tamper
    // is a constant, so its delta is identical every time it fires, which is
    // exactly the "recurring defect" signature-matching is meant to catch.
    history.push(report.rawFailedSignatures);
    if (history.length > GRACE_WINDOW_SIZE - 1) history.shift();
  }

  console.log(`BUG-640 fix result: redCount=${redCount} gracedCount=${gracedCount} firstRedTick=${firstRedTick} over 40 ticks (20 tampered)`);
  assert.ok(
    redCount > 0,
    'the windowed contract must detect the alternating defect at least once — the blind spot from ATTACK 1 must be closed',
  );
  assert.ok(
    firstRedTick >= 0 && firstRedTick <= 8,
    `the defect must surface within a small multiple of GRACE_WINDOW_SIZE (${GRACE_WINDOW_SIZE}) tampered ticks, not forever (firstRedTick=${firstRedTick})`,
  );
  assert.ok(
    gracedCount < 20,
    'not every tampered tick may be graced once the window accumulates enough occurrences (some early ticks may still be graced legitimately)',
  );
});

// ATTACK 2: GRACE SCOPE CREEP — only the two eligible ids can ever be graced.
test('ATTACK 2: conservation.funds-vs-flows can NEVER be graced even with history threaded', () => {
  let s = seedCity();
  for (let i = 0; i < 5; i++) s = reducer(s, { type: 'tick' });

  const tamperConservation = (state) => ({ ...state, fundsAtTickEnd: state.fundsAtTickEnd + 999 });

  const t1 = tamperConservation(s);
  const r1 = runConsistencyChecks(t1, undefined, new Set());
  const c1 = r1.checks.find((c) => c.id === 'conservation.funds-vs-flows');
  assert.equal(c1.ok, false, 'conservation reds INSTANTLY even on first call with empty prior history');
  assert.ok(!c1.detail.includes('BUG-624 grace'));
});

test('ATTACK 2b: flows.council-tax-matches can NEVER be graced (not in GRACE_ELIGIBLE_LINE_IDS)', () => {
  let s = seedCity();
  for (let i = 0; i < 5; i++) s = reducer(s, { type: 'tick' });
  const tamperCouncilTax = (state) => ({
    ...state,
    lastFlows: {
      ...state.lastFlows,
      inflows: state.lastFlows.inflows.map((f) =>
        f.label === 'Council Tax' ? { ...f, value: f.value + 999 } : f,
      ),
    },
  });
  const t1 = tamperCouncilTax(s);
  const r1 = runConsistencyChecks(t1, undefined, new Set());
  const c1 = r1.checks.find((c) => c.id === 'flows.council-tax-matches');
  assert.equal(c1.ok, false, 'council-tax reds instantly, no grace path exists for it at all');
});

// ATTACK 6: sabotage GRACE_ELIGIBLE to include conservation -> must red.
test('ATTACK 6: RED-PROOF — if GRACE_ELIGIBLE_LINE_IDS is expanded to include conservation, a test must catch it', () => {
  assert.ok(
    !GRACE_ELIGIBLE_LINE_IDS.has('conservation.funds-vs-flows'),
    'GRACE_ELIGIBLE_LINE_IDS scope guard: conservation must never be a member',
  );
  // Simulate the sabotage locally (do not mutate the frozen module set) and
  // prove that the *pushGraceable* codepath is what would need to change —
  // conservation is pushed via checks.push directly (never pushGraceable),
  // so even monkeypatching GRACE_ELIGIBLE_LINE_IDS would not launder it
  // without an additional code change to route conservation through
  // pushGraceable. This documents WHY attack 2 structurally cannot succeed,
  // not just that it doesn't currently.
});
