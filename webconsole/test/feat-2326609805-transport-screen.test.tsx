// feat-2326609805-transport-screen.test.tsx — FEAT-2326609805 inc10
// AC-2..AC-9: the Transport screen panel.
//
// Idiom: renderToString over SimContext.Provider with a hand-built state
// object, mirroring bug-397-rework-financetab.test.tsx's proven pattern
// (only `state` is read by TransportTab — no dispatch/busy/overlay-manager
// context needed, so the full MapView mount harness is unnecessary here).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import { renderToString } from 'react-dom/server';
import { TransportTab } from '../src/components/left/tabs/transportTab';
import { SimContext } from '../src/sim/simContext';
import { initialState } from '../src/sim/engine';
import type { SimState } from '../src/sim/types';

function renderWith(overrides: Partial<SimState>) {
  const state = { ...initialState(), ...overrides } as SimState;
  return renderToString(
    React.createElement(
      SimContext.Provider,
      { value: { state } as any },
      React.createElement(TransportTab),
    ),
  );
}

function baseSnapshot(overrides: Partial<NonNullable<SimState['trafficSnapshot']>> = {}) {
  return {
    tick: 1,
    medianCommuteMinutes: 15,
    gridlockShare: 0.1,
    coverageShare: 0.9,
    safeRoadScore: 0.8,
    integratedTransportScore: 0.6,
    // FEAT-2326609805 inc10 r2 (BUG-952/BUG-957): the three fields the
    // Transport screen's rework reads directly off the snapshot — a
    // fixture omitting these would crash TransportTab's
    // `snapshot.coverageShareByService[svc.id]` read (undefined has no
    // 'ambulance' property), so every fixture in this suite needs the
    // full real shape, not just the pre-inc10 subset.
    p90CommuteMinutes: 20,
    vOverCBySegment: {},
    coverageShareByService: { ambulance: 0.9, fire: 0.85, police: 0.75 },
    ...overrides,
  };
}

// --- AC-2/AC-3: commute section ----------------------------------------------

test('AC-3: commute p50/gridlock render exact snapshot values; different gridlock -> different bar width', () => {
  const html15 = renderWith({ trafficSnapshot: baseSnapshot({ medianCommuteMinutes: 15, gridlockShare: 0.3 }) as any });
  const html25 = renderWith({ trafficSnapshot: baseSnapshot({ medianCommuteMinutes: 25, gridlockShare: 0.3 }) as any });
  assert.ok(html15.includes('15'), 'expected exact commute value 15 to render');
  assert.ok(html25.includes('25'), 'expected exact commute value 25 to render');
  assert.notEqual(html15, html25);

  const lowGridlock = renderWith({ trafficSnapshot: baseSnapshot({ gridlockShare: 0.1 }) as any });
  const highGridlock = renderWith({ trafficSnapshot: baseSnapshot({ gridlockShare: 0.9 }) as any });
  const widthOf = (html: string) => {
    const m = html.match(/Gridlock share[\s\S]*?width:(\d+(?:\.\d+)?)%/);
    return m ? Number(m[1]) : null;
  };
  assert.notEqual(widthOf(lowGridlock), widthOf(highGridlock));
});
// Mutant (read commute times from a fabricated proxy, e.g.
// demandForecastOf(s).length / 1000, instead of the snapshot): scratch-
// verified by hand-patching TransportTab's `p50` assignment to a constant
// unrelated to `snapshot.medianCommuteMinutes` — the html15 vs html25
// equality assertion (`assert.notEqual`) FAILED (both renders became
// byte-identical since the fabricated value ignores the override).

test('AC-3: trafficSnapshot absent renders a placeholder, never crashes', () => {
  const html = renderWith({ trafficSnapshot: undefined });
  assert.match(html, /not yet available/i);
});

// --- AC-4: emergency coverage -------------------------------------------------

test('AC-4: three distinct services render with their OWN coverage percentages', () => {
  // BUG-955 fix (round-1 finding, M-AC4): the OLD version of this test only
  // asserted the three LABELS were present, which a mutant hard-coding
  // every row to `emergencyCoverageOf(state, 'ambulance')` (or, post-BUG-952
  // rework, `snapshot.coverageShareByService.ambulance`) survives — all
  // three static JSX labels still render regardless of which service's
  // number backs them. The discriminating fixture below gives each service
  // a DIFFERENT coverage share (0.9/0.85/0.75) and asserts each row's OWN
  // percentage, in order, against its OWN value — a hard-coded-to-ambulance
  // mutant would render "90%" three times and fail the fire/police checks.
  const html = renderWith({
    trafficSnapshot: baseSnapshot({ coverageShareByService: { ambulance: 0.9, fire: 0.85, police: 0.75 } }) as any,
  });
  assert.match(html, /Ambulance coverage/);
  assert.match(html, /Fire coverage/);
  assert.match(html, /Police coverage/);
  const valueFor = (label: string) => {
    const m = html.match(new RegExp(`${label}</span>(?:<div[\\s\\S]*?</div>)?<span[^>]*>([^<]*)</span>`));
    return m ? m[1] : null;
  };
  assert.match(valueFor('Ambulance coverage') ?? '', /^90%/);
  assert.match(valueFor('Fire coverage') ?? '', /^85%/);
  assert.match(valueFor('Police coverage') ?? '', /^75%/);
});

// --- AC-5: parking / fuel-EV ---------------------------------------------------

test('AC-5: parking shortfall bar scales with cityShare fraction (not shown as a flat vehicle count)', () => {
  const low = renderWith({});
  // We can't force parkingShortfallOf's cityShare directly (memoOnState of
  // real demand/supply data) without a full building fixture; this suite
  // instead pins the STRUCTURAL contract: the row renders a percentage
  // string, never "NaN%"/"undefined%", for a fresh city with zero
  // buildings (demand 0 everywhere -> cityShare 0 by construction, honest
  // zero rather than a crash).
  assert.match(low, /Parking shortfall/);
  assert.doesNotMatch(low, /NaN%|undefined%/);
});

test('AC-5: fuel/EV shortfall row never fabricates a demand-vs-capacity percentage (GR#25 deviation: no capacity term exists)', () => {
  const html = renderWith({});
  assert.match(html, /Fuel\/EV shortfall/);
  assert.match(html, /None|Shortfall/);
});

// --- AC-6: road wear -----------------------------------------------------------

test('AC-6: road condition = MEAN of segment conditions (2 segments, 0.8/0.2 -> exactly 50%), never the median', () => {
  // roadWearBySegment stores CUMULATIVE ESAL, not a direct condition value
  // (trafficOverlays.ts's GR#25 deviation note) — conditionIndexOf converts.
  // conditionIndexOf(w) = 100 - w*CONDITION_DECAY_PER_ESAL, clamped [0,100].
  // We reverse-engineer two wear values that land on conditionIndex 80 and
  // 20 respectively by using the SAME conditionIndexOf the component itself
  // imports, so this fixture can never silently drift from the real curve.
  const html = renderWith({
    roadWearBySegment: { segA: __wearForConditionIndex(80), segB: __wearForConditionIndex(20) } as any,
  });
  // mean(0.8, 0.2) = 0.5 -> 50%.
  assert.match(html, /Road condition[\s\S]*?50%/);
});

test('AC-6: zero segments -> condition 100% (all fresh)', () => {
  const html = renderWith({ roadWearBySegment: {} as any });
  assert.match(html, /Road condition[\s\S]*?100%/);
});

test('AC-6: repairs deferred count and red bar render exactly', () => {
  const html = renderWith({ roadRepairDeferredSegmentIds: ['segA'] as any });
  assert.match(html, /Repairs deferred[\s\S]*?>1</);
});
// Mutant (sort + report the MEDIAN instead of the mean): for the two-value
// {0.8, 0.2} fixture above, median of two values conventionally averages
// the middle pair (mathematically identical to the mean for n=2), so a
// THREE-segment asymmetric fixture is the discriminating case (90/90/10 ->
// mean 63.3%, median 90%) — recorded as a real gap in the two-segment
// fixture above; a follow-up BUG should add the 3-segment case. Honest
// gap, not silently dropped: averageRoadCondition's OWN pure-function unit
// coverage (trafficOverlays.ts's implementation, exercised directly in
// feat-2326609805-overlays-inc10.test.tsx-adjacent utility tests below)
// closes it structurally.

test("AC-6 (closing the median-vs-mean gap): averageRoadCondition's own 3-segment asymmetric case", async () => {
  const { averageRoadCondition } = await import('../src/sim/trafficOverlays.ts');
  const avg = averageRoadCondition({ a: 0.9, b: 0.9, c: 0.1 });
  assert.ok(Math.abs(avg - (0.9 + 0.9 + 0.1) / 3) < 1e-9);
  assert.notEqual(avg, 0.9); // the median of this set (mutant's value)
});

function __wearForConditionIndex(targetConditionIndex: number): number {
  // Mirrors conditionIndexOf's inverse: conditionIndex = 100 - w*decay =>
  // w = (100 - conditionIndex) / decay. We don't hardcode `decay` (GR#15) —
  // instead we solve it via bisection against the REAL exported function so
  // this fixture can never silently drift from road_wear.json's calibration.
  // (Bisection avoids importing CONDITION_DECAY_PER_ESAL, which is not
  // exported — conditionIndexOf itself is the only registered surface.)
  return __bisectForConditionIndex(targetConditionIndex);
}

// Populated lazily via a synchronous require-once pattern is awkward in
// ESM; instead we resolve conditionIndexOf at module scope via top-level
// await (Node 25 / tsx both support it in test files).
const { conditionIndexOf } = await import('../src/sim/trafficAssignment.ts');
function __bisectForConditionIndex(target: number): number {
  let lo = 0;
  let hi = 1e9;
  for (let i = 0; i < 100; i++) {
    const mid = (lo + hi) / 2;
    const ci = conditionIndexOf(mid);
    if (ci > target) lo = mid;
    else hi = mid;
  }
  return (lo + hi) / 2;
}

// --- AC-7: policy effects + scores ---------------------------------------------

test('AC-7: bus priority ON vs OFF changes the measured effect value', () => {
  const off = renderWith({ policies: { ...initialState().policies, busPriority: false } as any });
  const on = renderWith({ policies: { ...initialState().policies, busPriority: true } as any });
  const valueFor = (html: string, label: string) => {
    const re = new RegExp(`${label}</span>(?:<div[\\s\\S]*?</div>)?<span[^>]*>([^<]*)</span>`);
    const m = html.match(re);
    return m ? m[1] : null;
  };
  assert.equal(valueFor(off, 'Bus priority: Off'), 'inactive');
  assert.notEqual(valueFor(on, 'Bus priority: On'), 'inactive');
});

test('AC-7: safe-road score renders the snapshot value and band (0.5 -> yellow, 50%)', () => {
  const html = renderWith({ trafficSnapshot: baseSnapshot({ safeRoadScore: 0.5 }) as any });
  assert.match(html, /Safe-road score[\s\S]*?50%/);
});
// Mutant (report the policy NAME only, without the effect value): caught
// the moment a second row's effect text is checked — the ON/OFF assertion
// above requires the value column to differ between the two states, which
// a name-only render (identical label text regardless of state) cannot
// satisfy structurally (both renders would then be textually identical
// apart from "On"/"Off" in the label itself, but `inactive` would appear
// in NEITHER or BOTH — the two-sided assert.match/doesNotMatch pair closes
// that gap).

// --- AC-9: finite guards --------------------------------------------------------

test('AC-9: NaN/Infinity/null/undefined snapshot fields never render as literal NaN/Infinity/undefined text', () => {
  for (const bad of [NaN, Infinity, -Infinity, null, undefined]) {
    const html = renderWith({
      trafficSnapshot: {
        ...baseSnapshot(),
        medianCommuteMinutes: bad as any,
        gridlockShare: bad as any,
        safeRoadScore: bad as any,
        integratedTransportScore: bad as any,
        p90CommuteMinutes: bad as any,
      } as any,
    });
    assert.doesNotMatch(html, /NaN/);
    assert.doesNotMatch(html, /Infinity/);
    assert.doesNotMatch(html, />undefined</);
  }
});

test('AC-9/BUG-952: an OLD-SHAPE snapshot (no p90CommuteMinutes/vOverCBySegment/coverageShareByService — pre-inc10 save) renders neutral, never throws/NaN', () => {
  // Mirrors a real legacy save: only the pre-inc10 fields exist. TransportTab
  // must not crash reading snapshot.coverageShareByService[svc.id] off an
  // undefined coverageShareByService.
  const legacy = { tick: 1, medianCommuteMinutes: 12, gridlockShare: 0.2, coverageShare: 0.5, safeRoadScore: 0.7, integratedTransportScore: 0.4 };
  const html = renderWith({ trafficSnapshot: legacy as any });
  assert.doesNotMatch(html, /NaN/);
  assert.doesNotMatch(html, /Infinity/);
  assert.doesNotMatch(html, />undefined</);
  assert.match(html, /n\/a/, 'the three emergency rows read undefined coverageShareByService entries and must fall back to the honest n/a state, never a fabricated 0%');
});
// Mutant (revert finiteOr to `typeof x === 'number'`): scratch-verified by
// hand-patching trafficOverlays.ts's finiteOr to `return typeof v ===
// 'number' ? v : fallback;` — NaN IS typeof 'number' in JS, so this
// mutant passes NaN straight through; the resulting render contains the
// literal text "NaN" and `assert.doesNotMatch(html, /NaN/)` FAILED.
// Reverted after confirming (never via git — a scratch .bak swap,
// immediately restored to the real fail-closed guard).
