// bug624-flows-grace.test.mjs — BUG-624 regression.
//
// The flows-vs-recompute per-line checks (flows.wages-matches,
// flows.upkeep-total-matches) reconstruct their comparison from the CURRENT
// building set (countByKindOnline(s) / isOnline(s, b)), while the "actual"
// side is what advance() recorded via computeFlows(s) EARLIER in the same
// tick, evaluated against the PRE-increment s.tick. When a building's
// construction completes exactly on a tick boundary, isOnline flips
// false->true between the computeFlows() call and the final (tick-
// incremented) state, so the recompute counts one more online building than
// the actual flow charged for — a transient, self-healing divergence, not a
// real defect.
//
// consistency.ts's fix (see GRACE_ELIGIBLE_LINE_IDS / the new
// `priorFailedLineIds` argument + `rawFailedLineIds` result field): a
// caller that threads `rawFailedLineIds` from one tick's report into the
// next tick's `priorFailedLineIds` gets a two-consecutive-failures
// tolerance on JUST these two ids. `runConsistencyChecks(s)` with no third
// argument (every pre-existing call site, and the tests below that omit it)
// is BYTE-IDENTICAL to the pre-fix instant-fail behaviour.
//
// This file proves:
//   1. A growing city ticked 200 times, checked EVERY tick with the grace
//      thread wired through, shows ZERO panel reds — even though the
//      UNGRACED (instant-fail) report genuinely reds on some ticks (proving
//      the repro is real, not proving the check merely never fires).
//   1b. The SAME growing-city run through the REAL production path —
//      buildDebugJson (debugjson.ts), threading consistency.rawFailedLineIds
//      exactly as DebugTab's component ref does (src/components/left/tabs/
//      debugTab.tsx) — also shows ZERO panel reds end-to-end.
//   2. A PERSISTENT mutation (+777 to a flow, forever) still reds under the
//      grace thread — it fails on tick N (raw fail, no history -> graced)
//      AND tick N+1 (raw fail again, N's raw failure is now `prior` -> NOT
//      graced), so the grace never launders a genuine, repeating defect.
//   2b. The same persistent-defect scenario through buildDebugJson: refresh 1
//      is graced, refresh 2 (same tamper, `prior` threaded through the
//      component-ref pattern) reds for real.
//   3. The single-shot (no third argument) call used by every existing
//      consistency test — and by replay.ts's BUG-603 retry path, which
//      never passes the third argument either — is completely unaffected:
//      a corrupted flow still reds on the very first, only call.
//   4. captureBeforeWipe (GR#27) NEVER threads grace — a pre-wipe forensic
//      snapshot always shows the RAW, ungraced truth, even mid-online-flip.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import {
  runConsistencyChecks,
  GRACE_ELIGIBLE_LINE_IDS,
} from '../src/sim/consistency.ts';
import { buildDebugJson } from '../src/sim/debugjson.ts';
import { captureBeforeWipe, readPreWipeArchive, PREWIPE_ARCHIVE_KEY } from '../src/sim/captureBeforeWipe.ts';

const GRACE_IDS = ['flows.wages-matches', 'flows.upkeep-total-matches'];

// Seed a small road-connected service cluster (mirrors austerity-checks.test.mjs's
// buildOnlineCity fixture) then place a real job building (spliced directly,
// bypassing the road gate exactly like that fixture does, per its documented
// convention for `builtTick == null` buildings) so Wages is genuinely nonzero.
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

// Deterministic grid of candidate res_hut sites near the seeded road so
// autoConnect can reach every one of them.
function residentialSite(n) {
  const row = Math.floor(n / 20);
  const col = n % 20;
  return { x: 40 + col * 2, y: 40 + row * 2 };
}

/** Fixed, deterministic non-sim inputs for buildDebugJson (mirrors debugjson.test.mjs's testUi). */
function testUi(frameAtMs) {
  return {
    appVersion: 'v9.9.9-test',
    frameAtMs,
    map: { view: { zoom: 3.5, cx: 150, cy: 70 }, selectedBuildingId: null, showWater: true },
    errors: [],
  };
}

function memStorage() {
  const map = new Map();
  return {
    getItem(k) {
      return map.has(k) ? map.get(k) : null;
    },
    setItem(k, v) {
      map.set(k, String(v));
    },
  };
}

test('BUG-624: growing city, 200 ticks, zero panel reds WITH the grace thread wired through', () => {
  let s = seedCity();
  let prior; // undefined on tick 1: "no history yet" -> opt-in with an empty history
  let ungrazedRedTicks = 0;
  let gracedRedTicks = 0;
  let sawGraceNote = false;

  for (let i = 0; i < 200; i++) {
    // Grow the city continuously — place a new residential building most
    // ticks so construction completions (and therefore online-flips) land
    // on many different, non-aligned tick numbers, exactly like the "ticks
    // 4/6/16/31/58 of a growing city" repro in the BOW item.
    if (i < 120 && i % 2 === 0) {
      const { x, y } = residentialSite(i / 2);
      s = reducer(s, { type: 'place', spec: 'res_hut', x, y });
    }
    s = reducer(s, { type: 'tick' });

    // Ungraced (instant-fail) view — proves the repro is real.
    const ungraced = runConsistencyChecks(s);
    for (const id of GRACE_IDS) {
      const c = ungraced.checks.find((ch) => ch.id === id);
      if (c && !c.ok) ungrazedRedTicks++;
    }

    // Graced view — what a panel threading rawFailedLineIds would show.
    const graced = runConsistencyChecks(s, undefined, prior ?? new Set());
    for (const id of GRACE_IDS) {
      const c = graced.checks.find((ch) => ch.id === id);
      assert.ok(c, `${id} check exists at tick ${s.tick}`);
      if (!c.ok) gracedRedTicks++;
      if (c.detail.includes('BUG-624 grace')) sawGraceNote = true;
    }
    prior = new Set(graced.rawFailedLineIds);
  }

  assert.ok(
    ungrazedRedTicks > 0,
    'sanity: the ungraced instant-fail view must show at least one real red over 200 ticks of a growing city, or this test is not exercising the bug',
  );
  assert.ok(sawGraceNote, 'at least one tick was actually graced (detail carries the BUG-624 marker)');
  assert.equal(
    gracedRedTicks,
    0,
    'the graced panel view must show ZERO reds across the full 200-tick growing-city run',
  );
});

test('BUG-624: a PERSISTENT divergence (+777 forever) still reds under the grace thread by the 2nd consecutive check', () => {
  let s = seedCity();
  for (let i = 0; i < 10; i++) s = reducer(s, { type: 'tick' });

  const tamper = (state) => ({
    ...state,
    lastFlows: {
      ...state.lastFlows,
      outflows: state.lastFlows.outflows.map((f) =>
        f.label === 'Wages' ? { ...f, value: f.value + 777 } : f,
      ),
    },
  });

  const tampered1 = tamper(s);
  const report1 = runConsistencyChecks(tampered1, undefined, new Set());
  const check1 = report1.checks.find((c) => c.id === 'flows.wages-matches');
  assert.equal(check1.ok, true, 'tick 1 of a persistent defect is graced (indistinguishable from a real online-flip on its own)');
  assert.ok(report1.rawFailedLineIds.includes('flows.wages-matches'), 'the RAW failure is still recorded for tick 2 to see');

  // Advance one real tick (re-tampering every tick, exactly like a
  // permanently-broken formula would) and check again with tick 1's raw
  // failures fed in as history.
  const s2 = reducer(s, { type: 'tick' });
  const tampered2 = tamper(s2);
  const report2 = runConsistencyChecks(tampered2, undefined, new Set(report1.rawFailedLineIds));
  const check2 = report2.checks.find((c) => c.id === 'flows.wages-matches');
  assert.equal(check2.ok, false, 'the SAME check failing on the 2nd consecutive evaluation is a real defect, not graced');
  assert.ok(!check2.detail.includes('BUG-624 grace'), 'no grace marker on a persisted failure');
});

test('BUG-624: omitting the grace argument (every pre-existing call site) is byte-identical to the old instant-fail behaviour', () => {
  let s = seedCity();
  for (let i = 0; i < 10; i++) s = reducer(s, { type: 'tick' });

  const tampered = {
    ...s,
    lastFlows: {
      ...s.lastFlows,
      outflows: s.lastFlows.outflows.map((f) =>
        f.label === 'Wages' ? { ...f, value: f.value + 777 } : f,
      ),
    },
  };

  // No third argument -> no grace, ever, on the very first and only call.
  // This is exactly how replay.ts's checkConsistencyRecoveringStaleFlows,
  // debugjson.ts's buildDebugJson, and every existing consistency.test.mjs
  // assertion call runConsistencyChecks — none of them are touched by
  // BUG-624's fix.
  const report = runConsistencyChecks(tampered);
  const check = report.checks.find((c) => c.id === 'flows.wages-matches');
  assert.equal(check.ok, false, 'a genuinely tampered flow still reds instantly with no grace argument');
  assert.ok(report.rawFailedLineIds.includes('flows.wages-matches'), 'rawFailedLineIds is still populated for a caller that wants to opt in later');
});

test('BUG-624: GRACE_ELIGIBLE_LINE_IDS is exactly the two buildings-basis lines, never conservation or shape checks', () => {
  assert.deepEqual(
    [...GRACE_ELIGIBLE_LINE_IDS].sort(),
    ['flows.upkeep-total-matches', 'flows.wages-matches'],
  );
  assert.ok(!GRACE_ELIGIBLE_LINE_IDS.has('conservation.funds-vs-flows'), 'conservation is never grace-eligible');
});

// ===== END-TO-END: the REAL production path (buildDebugJson / DebugTab) =====

test('BUG-624 END-TO-END: growing city, 200 refreshes through buildDebugJson, zero panel reds (mirrors DebugTab\'s component-ref thread)', () => {
  let s = seedCity();
  // Mirrors debugTab.tsx's `priorFailedLineIdsRef` — a plain local variable
  // here stands in for the component ref; the point under test is that
  // buildDebugJson stays a pure function taking this in as its 3rd argument.
  let priorFailedLineIdsRef = new Set();
  let ungrazedRedTicks = 0;
  let panelRedRefreshes = 0;
  let sawGraceNote = false;

  for (let i = 0; i < 200; i++) {
    if (i < 120 && i % 2 === 0) {
      const { x, y } = residentialSite(i / 2);
      s = reducer(s, { type: 'place', spec: 'res_hut', x, y });
    }
    s = reducer(s, { type: 'tick' });

    // What the panel would show WITHOUT the fix (no history threaded) —
    // proves the repro still reproduces through the real serializer, not
    // just the lower-level runConsistencyChecks API.
    const ungracedDj = buildDebugJson(s, testUi(1_700_000_000_000 + i * 1000));
    for (const id of GRACE_IDS) {
      const c = ungracedDj.consistency.checks.find((ch) => ch.id === id);
      if (c && !c.ok) ungrazedRedTicks++;
    }

    // What DebugTab's actual takeFrame() does: pass the ref, then update it.
    const dj = buildDebugJson(s, testUi(1_700_000_000_000 + i * 1000), priorFailedLineIdsRef);
    for (const id of GRACE_IDS) {
      const c = dj.consistency.checks.find((ch) => ch.id === id);
      assert.ok(c, `${id} present in debug.json consistency.checks at tick ${s.tick}`);
      if (!c.ok) panelRedRefreshes++;
      if (c.detail.includes('BUG-624 grace')) sawGraceNote = true;
    }
    priorFailedLineIdsRef = new Set(dj.consistency.rawFailedLineIds);
  }

  assert.ok(
    ungrazedRedTicks > 0,
    'sanity: buildDebugJson without history threading must still reproduce real reds over 200 ticks of a growing city',
  );
  assert.ok(sawGraceNote, 'at least one refresh through buildDebugJson was actually graced');
  assert.equal(
    panelRedRefreshes,
    0,
    'the DebugTab panel path (buildDebugJson + threaded rawFailedLineIds) must show ZERO reds across the full 200-tick growing-city run',
  );
});

test('BUG-624 END-TO-END: a persistent tamper still reds in the panel by the 2nd refresh through buildDebugJson', () => {
  let s = seedCity();
  for (let i = 0; i < 10; i++) s = reducer(s, { type: 'tick' });

  const tamper = (state) => ({
    ...state,
    lastFlows: {
      ...state.lastFlows,
      outflows: state.lastFlows.outflows.map((f) =>
        f.label === 'Wages' ? { ...f, value: f.value + 777 } : f,
      ),
    },
  });

  // Refresh 1: empty history -> the panel grace tolerates it (mirrors
  // debugTab.tsx's `priorFailedLineIdsRef` starting as `new Set()`, not
  // `undefined` — see that component's BUG-624 comment).
  const dj1 = buildDebugJson(tamper(s), testUi(1_700_000_000_000), new Set());
  const check1 = dj1.consistency.checks.find((c) => c.id === 'flows.wages-matches');
  assert.equal(check1.ok, true, 'refresh 1 of a persistent defect is graced through the panel path');

  // Refresh 2 (a real tick later, tampered again — a permanently-broken
  // formula would do this every tick), threading refresh 1's rawFailedLineIds
  // exactly as DebugTab's ref update does.
  const s2 = reducer(s, { type: 'tick' });
  const dj2 = buildDebugJson(tamper(s2), testUi(1_700_000_001_000), new Set(dj1.consistency.rawFailedLineIds));
  const check2 = dj2.consistency.checks.find((c) => c.id === 'flows.wages-matches');
  assert.equal(check2.ok, false, 'refresh 2 of the SAME persistent defect reds for real in the panel path');
  assert.ok(!check2.detail.includes('BUG-624 grace'), 'no grace marker once the defect has persisted into a 2nd refresh');
});

// ===== GR#27 capture-before-wipe: must NEVER thread grace =====

test('BUG-624: captureBeforeWipe never threads grace — a mid-online-flip wipe capture shows the RAW divergence', () => {
  // Build a state whose NEXT tick will land exactly on an online-flip (a
  // freshly-placed res_hut a few ticks from construction-complete), then
  // capture-before-wipe right at that instant.
  let s = seedCity();
  s = reducer(s, { type: 'place', spec: 'res_hut', x: 40, y: 40 });
  let foundDivergingTick = false;
  for (let i = 0; i < 30 && !foundDivergingTick; i++) {
    s = reducer(s, { type: 'tick' });
    const raw = runConsistencyChecks(s);
    if (GRACE_IDS.some((id) => raw.checks.find((c) => c.id === id)?.ok === false)) {
      foundDivergingTick = true;
    }
  }
  assert.ok(foundDivergingTick, 'sanity: found a tick where the raw (ungraced) view genuinely reds');

  const storage = memStorage();
  captureBeforeWipe(s, 'v9.9.9-test', storage, 1_700_000_000_000);
  const archive = readPreWipeArchive(storage);
  assert.equal(archive.length, 1, 'one capture written');
  const captured = archive[0].debug.consistency;
  // compactDebugForArchive trims `checks` to only the failing ones (or [] on
  // full success) — assert the RAW divergence survived into rawFailedLineIds
  // exactly as the ungraced runConsistencyChecks(s) call would show, proving
  // the capture path took no grace shortcut.
  const expectedRaw = runConsistencyChecks(s).rawFailedLineIds;
  assert.deepEqual(
    [...captured.rawFailedLineIds].sort(),
    [...expectedRaw].sort(),
    'captureBeforeWipe records the RAW ungraced failures, never a grace-filtered view',
  );
  if (expectedRaw.length > 0) {
    assert.ok(captured.failures > 0, 'a genuinely-diverging capture is NOT laundered to failures:0');
  }
});
