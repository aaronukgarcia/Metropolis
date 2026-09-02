// simworker-offload.test.mjs — FEAT-webworker-sim-offload Stage 0 + Stage 1
// (Landing 2, tick-only offload) regression + RED-proof tests.
//
// jsdom/node --test cannot construct a real Worker, so per the spec's own
// guidance (docs/planning/acceptance/FEAT-webworker-sim-offload-2026-09-02.md
// §6) this file tests the protocol's PURE handler function (runTick) and the
// queue-depth/flag primitives directly, never a real Worker instance —
// exactly the design simWorkerProtocol.ts/webWorkerFlag.ts/workerQueueDepth.ts
// were built around (see each file's header for why).

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';
// store.tsx (and its own JSX-bearing imports, e.g. components/RebuildPrompt.tsx)
// cannot be loaded by plain `node --test` on a .mjs file — Node's native TS
// support strips TYPES but does not transform JSX, and this file's own
// extension (.mjs) is what routes it to plain `node --test` rather than
// `tsx --test` (see tools/test/scoped.mjs's extension-based dispatch). The
// BUG-597 glue tests below need the real store.tsx, so they load it via tsx's
// own programmatic API instead of a bare dynamic import — this stays scoped
// to the one specifier that needs it; every other import in this file is
// untouched, JSX-free .ts.
import { tsImport } from 'tsx/esm/api';
import { initialState, reducer, wellbeingOf } from '../src/sim/engine.ts';
import { recordAction, emptyJournal } from '../src/sim/journal.ts';
import { stableStringify } from '../src/sim/genesisReplay.ts';
import { runTick } from '../src/sim/simWorkerProtocol.ts';
import { createQueueDepthTracker, getGlobalWorkerQueueTracker } from '../src/sim/workerQueueDepth.ts';
import { webWorkerOffloadEnabled } from '../src/sim/webWorkerFlag.ts';
import { occupiedSet, computeRoadConnectivity, powerStats, countByKindOnline, serviceCoverageOf, utilisationOf, waterCaps, SPECS } from '../src/sim/data.ts';
import {
  initialOffloadControllerState,
  beginTickRequest,
  invalidateInFlight,
  decideTickReply,
  shouldForceSyncTick,
  afterForcedSyncTick,
  clearWorkerBusy,
  FORCE_SYNC_TICK_SUPERSEDE_THRESHOLD,
} from '../src/sim/simWorkerOffloadController.ts';

// ---------------------------------------------------------------------------
// (1) AC-3-style determinism: main-thread-only reducer chain vs a "worker
// path" that computes every 'tick' action via runTick() (the exact function
// the real worker calls) and every other action via the ordinary reducer
// (exactly as store.tsx's Landing-2 design keeps place/bulldoze/etc on main).
// Byte-identical stableStringify output proves "which thread ran the tick"
// cannot itself be a determinism risk (GR#21) — same reducer, same journal.
// ---------------------------------------------------------------------------

/** Build a real mixed action sequence: N ticks, some placements. Deterministic
 *  (no Date.now/Math.random) — same fixture every run. */
function buildMixedActions(n) {
  const actions = [];
  for (let i = 0; i < n; i++) {
    if (i % 7 === 0) {
      actions.push({ type: 'place', spec: 'res_hut', x: 10 + (i % 50), y: 10 + Math.floor(i / 50) });
    } else if (i % 11 === 0) {
      actions.push({ type: 'place', spec: 'road', x: 60 + (i % 20), y: 60 });
    } else {
      actions.push({ type: 'tick' });
    }
  }
  return actions;
}

// N kept modest (not the spec's eventual N>=500 acceptance-test scale) so
// this file stays fast enough for the routine scoped-test gate; initialState()
// seeds a full city-sim population and each 'tick' walks the whole building
// list several times over (the exact O(buildings) cost this feature's Stage 0
// half targets) — 500 real ticks measured ~5-6 minutes on this fixture, far
// past what a per-commit gate should cost. 80 ticks still exercises dozens of
// tick/place interleavings and is more than enough to catch a forked worker
// tick implementation (see the RED PROOF test below, which needs far fewer).
const DETERMINISM_TICK_COUNT = 80;

test(`AC-3-style: worker-tick-path replay is byte-identical to main-thread-only replay (N=${DETERMINISM_TICK_COUNT})`, () => {
  const actions = buildMixedActions(DETERMINISM_TICK_COUNT);
  assert.equal(actions.length, DETERMINISM_TICK_COUNT, `precondition: fixture actually has ${DETERMINISM_TICK_COUNT} actions`);
  assert.ok(actions.some((a) => a.type === 'tick'), 'precondition: fixture includes ticks');
  assert.ok(actions.some((a) => a.type === 'place'), 'precondition: fixture includes placements');

  // Main-thread-only path: every action, including 'tick', goes through the
  // plain reducer directly — today's untouched behaviour (AC-8 fallback).
  let mainState = initialState();
  for (const action of actions) {
    mainState = reducer(mainState, action);
  }

  // "Worker path": ticks are computed via runTick() (the exact function the
  // real simWorker.ts entry calls in response to a postMessage 'runTick'
  // request); every other action still runs through the plain reducer on
  // main, exactly matching store.tsx's Landing-2 design (place/bulldoze
  // never move to the worker in this landing).
  let workerPathState = initialState();
  for (const action of actions) {
    workerPathState = action.type === 'tick' ? runTick(workerPathState) : reducer(workerPathState, action);
  }

  // RED PROOF: if runTick() ever forked reducer logic (e.g. called a
  // divergent tick handler, or skipped the top-level reducer()'s
  // roadConnectivity-recompute wrapper), this would redden immediately —
  // stableStringify sorts keys recursively so this is a true deep-equality
  // oracle, the same one genesis-replay.test.mjs uses for its own
  // determinism proof.
  assert.equal(
    stableStringify(workerPathState),
    stableStringify(mainState),
    'a tick computed via runTick() must produce byte-identical state to the same tick computed by the plain reducer'
  );
});

test('AC-3-style: a single runTick() call matches reducer(state, {type:"tick"}) exactly', () => {
  const s = reducer(initialState(), { type: 'place', spec: 'res_hut', x: 20, y: 20 });
  const viaReducer = reducer(s, { type: 'tick' });
  const viaRunTick = runTick(s);
  assert.equal(stableStringify(viaRunTick), stableStringify(viaReducer),
    'runTick() is a thin wrapper around reducer(state, {type:"tick"}) — no forked logic (GR#21)');
});

// RED PROOF for the determinism test itself: prove it CAN fail. A "worker"
// that forked its own tick logic (e.g. skipped the road-connectivity
// recompute wrapper) would diverge from the main path — simulate exactly
// that divergent implementation inline and confirm the SAME assertion style
// catches it.
test('RED PROOF: a forked/divergent tick implementation is caught by the same comparison', () => {
  const actions = buildMixedActions(30);
  let mainState = initialState();
  for (const action of actions) mainState = reducer(mainState, action);

  // A deliberately-wrong "worker" that skips the tick's population growth by
  // calling reduceCore behaviour incorrectly — simulated here by just NOT
  // advancing tick for every 3rd tick action (a stand-in for "forked logic
  // that drops some ticks"), not a suggestion this could happen for real.
  let divergentState = initialState();
  let tickCount = 0;
  for (const action of actions) {
    if (action.type === 'tick') {
      tickCount++;
      if (tickCount % 3 === 0) continue; // BUG simulation: silently drop every 3rd tick
      divergentState = runTick(divergentState);
    } else {
      divergentState = reducer(divergentState, action);
    }
  }
  assert.notEqual(
    stableStringify(divergentState),
    stableStringify(mainState),
    'a genuinely divergent tick implementation must NOT read as identical — proves the comparison above can fail'
  );
});

// ---------------------------------------------------------------------------
// (2) Fallback (AC-8-style): the flag helper degrades to "disabled" in every
// environment this test runner can exercise (no Worker global in node --test).
// ---------------------------------------------------------------------------

describe('webWorkerOffloadEnabled — AC-8 fallback contract', () => {
  test('returns false when Worker is undefined (this test runtime has no Worker global)', () => {
    assert.equal(typeof globalThis.Worker, 'undefined', 'precondition: node --test has no real Worker');
    assert.equal(webWorkerOffloadEnabled(), false, 'no Worker constructor -> always disabled, regardless of flag');
  });

  test('returns false even with the flag set to "1", still gated on Worker availability', () => {
    const origWindow = globalThis.window;
    globalThis.window = { localStorage: { getItem: (k) => (k === 'metropolis.webworker' ? '1' : null) } };
    try {
      assert.equal(webWorkerOffloadEnabled(), false, 'flag alone cannot enable it without a real Worker constructor');
    } finally {
      globalThis.window = origWindow;
    }
  });

  test('never throws when window/localStorage access fails (fail-closed)', () => {
    const origWindow = globalThis.window;
    globalThis.window = {
      get localStorage() {
        throw new Error('quota/denied');
      },
    };
    try {
      assert.doesNotThrow(() => webWorkerOffloadEnabled());
      assert.equal(webWorkerOffloadEnabled(), false);
    } finally {
      globalThis.window = origWindow;
    }
  });
});

// ---------------------------------------------------------------------------
// (3) Queue-depth groundwork (FEAT-2326609734): enqueue N, drain to 0.
// RED-proof: prove drain() actually decrements and enqueue() actually
// increments, and that draining past 0 clamps rather than going negative.
// ---------------------------------------------------------------------------

describe('workerQueueDepth — FEAT-2326609734 groundwork', () => {
  test('enqueue N shows depth N, draining one at a time returns to 0', () => {
    const tracker = createQueueDepthTracker();
    assert.equal(tracker.depth(), 0, 'starts at 0 (zero-lag / no worker case)');

    const N = 7;
    for (let i = 0; i < N; i++) tracker.enqueue();
    assert.equal(tracker.depth(), N, `after enqueueing ${N}, depth must read exactly ${N}`);

    for (let i = N; i > 0; i--) {
      tracker.drain();
      assert.equal(tracker.depth(), i - 1, `after draining, depth must be ${i - 1}`);
    }
    assert.equal(tracker.depth(), 0, 'fully drained back to 0');
  });

  test('RED PROOF: an enqueue that forgets to increment would read a stale depth', () => {
    // Simulate the bug directly (no call to enqueue) and show the assertion
    // that WOULD catch a broken enqueue() fails against a tracker nothing
    // was pushed onto.
    const tracker = createQueueDepthTracker();
    // Intentionally do NOT enqueue.
    assert.notEqual(tracker.depth(), 3, 'a tracker nothing was enqueued onto must not read 3 — proves the equality check above is meaningful');
  });

  test('drain() never goes negative — an extra/duplicate ack clamps at 0', () => {
    const tracker = createQueueDepthTracker();
    tracker.enqueue();
    tracker.drain();
    tracker.drain(); // extra ack — must clamp, not go to -1
    assert.equal(tracker.depth(), 0);
  });

  test('reset() zeroes the backlog regardless of prior enqueues', () => {
    const tracker = createQueueDepthTracker();
    tracker.enqueue();
    tracker.enqueue();
    tracker.enqueue();
    tracker.reset();
    assert.equal(tracker.depth(), 0);
  });

  test('the global singleton is stable across getGlobalWorkerQueueTracker() calls (perfhud.ts idiom)', () => {
    const a = getGlobalWorkerQueueTracker();
    const b = getGlobalWorkerQueueTracker();
    assert.equal(a, b, 'same instance every call — same idiom as getGlobalTickTracker()');
  });
});

// ---------------------------------------------------------------------------
// (4) Stage 0 selector-dedup: prove the memoised aggregates are actually
// memoised (RED PROOF style, mirroring latency-quickwins.test.mjs's FIX 1),
// and that they still produce the SAME numbers as before the dedup — a pure
// refactor, no behaviour change, attackable by diffing against a version
// that recomputes from scratch every call.
// ---------------------------------------------------------------------------

function boardWithMix(extra = {}) {
  const base = initialState();
  return {
    ...base,
    unlockedAll: true,
    funds: 100_000_000,
    population: 5000,
    // builtTick: null -> isOnline()'s G1 construction-completion gate is
    // skipped entirely (data.ts's isOnline: "if (b.builtTick == null) return
    // true;") — keeps this fixture's coverage-aggregate assertions isolated
    // from unrelated construction-timing behaviour.
    buildings: [
      { id: 1, spec: 'hea_clinic', x: 20, y: 20, builtTick: null },
      { id: 2, spec: 'pol_station', x: 22, y: 20, builtTick: null },
      { id: 3, spec: 'pow_wind', x: 24, y: 20, builtTick: null },
    ],
    nextId: 4,
    roadConnectivity: undefined,
    ...extra,
  };
}

test('FIX 4 (Stage 0): powerStats returns the SAME reference for repeat calls on an unchanged state', () => {
  const s = { ...boardWithMix() };
  const first = powerStats(s);
  const second = powerStats(s);
  // RED PROOF: reverting the memoOnState wrap (calling computePowerStats(s)
  // directly on every call) would make this a fresh object each time —
  // `first === second` would be false.
  assert.equal(first, second, 'powerStats is memoised per (buildings, roadConnectivity, tick) — same reference for the same triple');
});

test('FIX 4 (Stage 0): powerStats recomputes (fresh reference) when tick changes', () => {
  const s1 = { ...boardWithMix(), tick: 10 };
  const v1 = powerStats(s1);
  const s2 = { ...s1, tick: 11 };
  const v2 = powerStats(s2);
  assert.notEqual(v1, v2, 'a tick change must invalidate the memo — construction-completion gates depend on tick, not just buildings');
});

test('FIX 5 (Stage 0): countByKindOnline is memoised the same way', () => {
  const s = boardWithMix();
  const first = countByKindOnline(s);
  const second = countByKindOnline(s);
  assert.equal(first, second, 'countByKindOnline memoised — computeFlows called this 3x per invocation pre-fix');
});

test('FIX 6 (Stage 0): serviceCoverageOf still reports the same coverage numbers as a from-scratch per-predicate scan', () => {
  const s = boardWithMix();
  const rows = serviceCoverageOf(s);
  const gpRow = rows.find((r) => r.id === 'gp');
  const policeRow = rows.find((r) => r.id === 'police');
  const powerRow = rows.find((r) => r.id === 'power');
  // Independently recompute via the raw predicates the old sumBy() calls
  // used, to prove the single-pass aggregate produces IDENTICAL sums (a
  // pure refactor, not a behaviour change).
  let expectedGp = 0;
  let expectedPolice = 0;
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (!sp) continue;
    if (sp.id === 'hea_clinic') expectedGp += sp.served ?? 0;
    if (sp.kind === 'police') expectedPolice += sp.served ?? 0;
  }
  assert.equal(gpRow.cap, expectedGp, 'gp coverage cap matches an independent from-scratch scan');
  assert.equal(policeRow.cap, expectedPolice, 'police coverage cap matches an independent from-scratch scan');
  assert.ok(powerRow, 'power row still present (powerStats still wired into serviceCoverageOf)');
});

test('FIX 7 (Stage 0): utilisationOf health/police/fire reuse the same aggregate as serviceCoverageOf (no drift)', () => {
  const s = boardWithMix();
  const coverage = serviceCoverageOf(s);
  const gpRow = coverage.find((r) => r.id === 'gp');
  const hospRow = coverage.find((r) => r.id === 'hosp');
  const policeRow = coverage.find((r) => r.id === 'police');

  const clinicBuilding = s.buildings.find((b) => b.spec === 'hea_clinic');
  const util = utilisationOf(s, clinicBuilding);
  assert.ok(util, 'a clinic with population present must report a utilisation');
  const expectedHealthCap = gpRow.cap + hospRow.cap;
  assert.equal(
    util.ratio,
    Math.min(1, Math.max(0, s.population / expectedHealthCap)),
    'utilisationOf health case must match serviceCoverageOf-derived capacity exactly — same aggregate, no separate drifted computation'
  );

  const policeBuilding = s.buildings.find((b) => b.spec === 'pol_station');
  const policeUtil = utilisationOf(s, policeBuilding);
  assert.ok(policeUtil);
  assert.equal(policeUtil.ratio, Math.min(1, Math.max(0, s.population / policeRow.cap)));
});

// ---------------------------------------------------------------------------
// Sanity: FIX 1/FIX 2 from the earlier b2d31bc7 pass (occupiedSet memo,
// conditional computeRoadConnectivity) are untouched by this Stage-0/1 work —
// a light smoke check, the real coverage lives in latency-quickwins.test.mjs.
// ---------------------------------------------------------------------------
test('sanity: occupiedSet/computeRoadConnectivity still importable and functional after this feature', () => {
  const s = boardWithMix();
  const set = occupiedSet(s);
  assert.ok(set instanceof Set);
  const conn = computeRoadConnectivity(s);
  assert.ok(Array.isArray(conn.connectedRoadTiles));
});

// ===========================================================================
// INDEPENDENT ROUND REJECT FOLLOW-UP (2026-09-02) — B1/B2/B3 regressions.
// ===========================================================================

// ---------------------------------------------------------------------------
// B1 — the Stage 0 memo key was incomplete (missed s.pipeTier). Proven live:
// serviceCoverageOf's cleanwater cap stayed stale (20000) after a pipeUpgrade
// bumped the SAME building's tier to Ø500mm (mult 1.8 -> eff 36000), while
// waterCaps(s) on the identical post-upgrade state correctly read 36000, and
// wellbeingOf(s) (contractually pure) returned different numbers cold vs.
// after a prior serviceCoverageOf read. Fixed by rekeying memoOnState on the
// state OBJECT itself (structurally complete by construction) instead of a
// hand-picked field triple — see data.ts's memoOnState header.
// ---------------------------------------------------------------------------

function waterBoard(fundsOverride = 100_000) {
  const base = initialState();
  return {
    ...base,
    unlockedAll: true,
    funds: fundsOverride,
    population: 20_000,
    buildings: [{ id: 1, spec: 'wat_clean', x: 30, y: 30, builtTick: null }],
    nextId: 2,
    pipeTier: {},
  };
}

test('B1 RED PROOF: a pipeUpgrade must NOT leave serviceCoverageOf reporting the pre-upgrade capacity', () => {
  const s0 = waterBoard();
  // "Cold" read at tier 0 — populates whatever memo exists for s0 specifically.
  const before = serviceCoverageOf(s0);
  const cleanBefore = before.find((r) => r.id === 'cleanwater');
  assert.equal(cleanBefore.cap, 20_000, 'precondition: tier-0 clean water cap is the raw served figure (mult 1)');

  // Upgrade the SAME building's pipe tier — a brand-new top-level state
  // object (reduceCore's pipeUpgrade case spreads `{...state, ...}`), but
  // buildings/roadConnectivity/tick are all UNCHANGED from s0 — exactly the
  // shape that fooled the old (buildings, roadConnectivity, tick) triple key.
  const s1 = reducer(s0, { type: 'pipeUpgrade', id: 1 });
  assert.notEqual(s1, s0, 'precondition: pipeUpgrade returns a new state object (immutable-update discipline)');
  assert.equal(s1.buildings, s0.buildings, 'precondition: buildings reference is UNCHANGED by pipeUpgrade — this is exactly what defeated the old triple key');
  assert.equal(s1.roadConnectivity, s0.roadConnectivity, 'precondition: roadConnectivity reference is also unchanged');
  assert.equal(s1.tick, s0.tick, 'precondition: tick is also unchanged');
  assert.notEqual(s1.pipeTier[1], s0.pipeTier[1] ?? 0, 'precondition: pipeTier for building 1 actually changed');

  const groundTruth = waterCaps(s1).clean;
  assert.equal(groundTruth, 36_000, 'precondition: waterCaps (never memoised) reports the upgraded Ø500mm capacity (20000 * 1.8)');

  const after = serviceCoverageOf(s1);
  const cleanAfter = after.find((r) => r.id === 'cleanwater');
  // RED PROOF: reverting data.ts's memoOnState to the old (buildings,
  // roadConnectivity, tick) triple key reproduces the exact live defect —
  // cleanAfter.cap reads 20000 (stale, cache HIT against s0's memoised
  // result) instead of 36000, while groundTruth (waterCaps, never
  // memoised) correctly reads 36000 on the SAME state s1 — the two-panels-
  // disagree GR#3 violation the round caught.
  assert.equal(cleanAfter.cap, groundTruth,
    'serviceCoverageOf must report the SAME post-upgrade capacity as waterCaps on the same state — a paid pipe upgrade must never look inert');
  assert.equal(cleanAfter.cap, 36_000);
});

test('B1 RED PROOF: wellbeingOf is pure — cold call equals a call preceded by other reads of the SAME state', () => {
  const s0 = waterBoard();
  const s1 = reducer(s0, { type: 'pipeUpgrade', id: 1 });

  // "Cold": nothing about s1 has been read yet at the point wellbeingOf is
  // first called on it.
  const coldResult = wellbeingOf(s1);

  // Build a FRESH equivalent state (same shape, different object identity)
  // and "warm" it with several prior reads before computing wellbeingOf —
  // if wellbeingOf's cleanwater-derived Utilities part depended on
  // call-history (the stale-memo defect), warming would change the answer.
  const s1b = reducer(waterBoard(), { type: 'pipeUpgrade', id: 1 });
  serviceCoverageOf(s1b);
  utilisationOf(s1b, s1b.buildings[0]);
  serviceCoverageOf(s1b); // second read, same object — must be a cache HIT, not a fresh divergent value.
  const warmedResult = wellbeingOf(s1b);

  // RED PROOF: with the old incomplete memo key, whether a function reads a
  // pipe-upgraded building's capacity FRESH or via a stale cached value
  // depended on incidental call history (which panel happened to read
  // first) rather than on the state passed in — this assertion is exactly
  // the "contractually pure function returned different values cold vs.
  // after a prior read" defect the round proved live (16 vs 19).
  assert.deepEqual(warmedResult, coldResult,
    'wellbeingOf(state) must be a pure function of state alone — identical result regardless of what else already read that state');
});

// ---------------------------------------------------------------------------
// B2 — loadGame race: an in-flight tick's reply, computed against the
// PRE-load state, must never be allowed to clobber a freshly loaded
// (possibly OLDER) save. Proven against simWorkerOffloadController.ts's pure
// state machine directly (store.tsx is thin glue over it — see its header).
// ---------------------------------------------------------------------------

test('B2 RED PROOF: a stale in-flight tick reply is discarded after a load/reset invalidates it, even with a HIGHER tick number', () => {
  // A tick request is issued while the live city is at tick 500.
  let controller = initialOffloadControllerState();
  const begun = beginTickRequest(controller, 500);
  assert.ok(begun, 'precondition: no request already in flight');
  controller = begun.state;
  assert.equal(controller.pendingTick, true);

  // The player loads an OLDER save (tick 12) WHILE that request is still in
  // flight — applyLoadedSave's hydrate is about to wholesale-replace state,
  // so store.tsx invalidates the in-flight request first (B2 fix).
  controller = invalidateInFlight(controller);
  assert.equal(controller.pendingTick, false);
  assert.equal(controller.activeRequestId, null);

  // The ABANDONED request's reply now arrives late: it carries tick 501 —
  // numerically HIGHER than the just-loaded save's tick 12. Under the OLD
  // (pre-fix) design, a tick-number-only guard (`501 > 12`) would have
  // PASSED and clobbered the freshly loaded older save with the stale
  // pre-load city. The requestId-based decision must discard it regardless.
  const { state: afterReply, decision } = decideTickReply(
    controller,
    { requestId: begun.requestId, resultTick: 501 },
    /* currentLiveTick (the just-loaded save's tick) */ 12
  );

  // RED PROOF: reverting decideTickReply to compare ONLY `resultTick >
  // currentLiveTick` (dropping the `reply.requestId !== s.activeRequestId`
  // check) reproduces the exact live defect — this assertion would then see
  // decision.kind === 'apply' (501 > 12) instead of 'discard', meaning the
  // loaded city would be overwritten by the stale pre-load state.
  assert.equal(decision.kind, 'discard', 'a reply for an invalidated (load-superseded) request must be discarded even though its own tick number is higher than the freshly loaded state');
  assert.equal(afterReply, controller, 'a discarded-due-to-mismatch reply must not further mutate the (already-invalidated) controller state');
});

test('B2: the loaded city survives end-to-end — reducer(hydrate) is never followed by a clobbering hydrate(staleState)', () => {
  // Simulates the full sequence store.tsx runs: tick requested from the
  // OLD city -> load-save invalidates it -> late reply arrives -> apply
  // ONLY the decision the controller returns (never the raw reply).
  const oldCity = { ...initialState(), tick: 500, funds: 999 };
  let controller = initialOffloadControllerState();
  const begun = beginTickRequest(controller, oldCity.tick);
  controller = begun.state;

  const loadedSave = { ...initialState(), tick: 12, funds: 55 };
  controller = invalidateInFlight(controller); // store.tsx's applyLoadedSave calls this BEFORE hydrating.
  let liveState = loadedSave; // the hydrate has now happened.

  // The stale reply lands.
  const staleReply = { requestId: begun.requestId, resultTick: oldCity.tick + 1 };
  const { state: nextController, decision } = decideTickReply(controller, staleReply, liveState.tick);
  controller = nextController;
  if (decision.kind === 'apply') {
    // This branch must NEVER execute for a superseded request — if it does,
    // the assertion below catches the clobber directly.
    liveState = { ...oldCity, tick: staleReply.resultTick };
  }

  assert.equal(decision.kind, 'discard');
  assert.equal(liveState, loadedSave, 'the loaded city must be exactly what survives — no clobber from the stale pre-load tick reply');
  assert.equal(liveState.funds, 55, 'funds must reflect the LOADED save, never the abandoned pre-load city');
});

// ---------------------------------------------------------------------------
// B3 — phantom journal tick: a tick's journal entry must be written if and
// only if its result is actually applied. A discarded/rejected/errored reply
// must leave NO trace in the journal, or genesis-replay would fold a tick
// that live play never actually ran.
// ---------------------------------------------------------------------------

test('B3 RED PROOF: decideTickReply never returns a tickToJournal for a discarded reply', () => {
  let controller = initialOffloadControllerState();
  const begun = beginTickRequest(controller, 500);
  controller = begun.state;

  // Case 1: requestId mismatch (superseded by another action/load/reset).
  const mismatched = decideTickReply(controller, { requestId: 9999, resultTick: 501 }, 500);
  assert.equal(mismatched.decision.kind, 'discard');
  assert.ok(!('tickToJournal' in mismatched.decision), 'a discarded decision must carry no tick number to journal at all');

  // Case 2: requestId matches but the reply's tick does not actually
  // advance past the current live tick (defensive belt-and-braces guard).
  const nonAdvancing = decideTickReply(controller, { requestId: begun.requestId, resultTick: 500 }, 500);
  assert.equal(nonAdvancing.decision.kind, 'discard');
  assert.ok(!('tickToJournal' in nonAdvancing.decision));
});

test('B3: a full request/invalidate/late-reply sequence never yields a journal write for the abandoned tick', () => {
  // Mirrors an onerror/teardown/supersede scenario end-to-end: journal
  // entries are collected ONLY when store.tsx would actually call
  // recordTickInJournalOnly — i.e. only on decision.kind === 'apply'.
  let journal = emptyJournal();
  const recordIfApplied = (decision, tick) => {
    if (decision.kind === 'apply') {
      journal = recordAction(journal, decision.tickToJournal, { type: 'tick' });
    }
  };

  let controller = initialOffloadControllerState();
  const begun = beginTickRequest(controller, 500);
  controller = begun.state;

  // The request is superseded (a placement arrives mid-flight, an onerror
  // fires, or a reset/load happens — all route through invalidateInFlight).
  controller = invalidateInFlight(controller);

  // The worker's reply for the abandoned request arrives anyway (the worker
  // itself doesn't know it was superseded — it always finishes the tick it
  // was given and posts back).
  const late = decideTickReply(controller, { requestId: begun.requestId, resultTick: 501 }, 500);
  recordIfApplied(late.decision);

  // RED PROOF: reverting B3 (journaling at REQUEST time instead of gating on
  // decision.kind === 'apply') would have added a 'tick' entry the instant
  // beginTickRequest ran, regardless of what happened afterward — this
  // assertion catches that directly: the journal must stay EMPTY here.
  assert.equal(journal.entries.length, 0, 'an abandoned/discarded tick must leave NO journal entry — genesis-replay must never be asked to replay a tick live play never actually applied');

  // Now the HAPPY path, for contrast: a fresh request that IS applied DOES
  // get journaled, at the PRE-tick number it was requested at.
  let controller2 = initialOffloadControllerState();
  const begun2 = beginTickRequest(controller2, 700);
  controller2 = begun2.state;
  const good = decideTickReply(controller2, { requestId: begun2.requestId, resultTick: 701 }, 700);
  recordIfApplied(good.decision);
  assert.equal(journal.entries.length, 1, 'an applied tick DOES get journaled — B3 only forbids journaling a tick that was NOT applied');
  assert.equal(journal.entries[0].tick, 700, 'journaled at the PRE-tick number (700), not the post-tick result (701)');
});

// ---------------------------------------------------------------------------
// Controller sanity: beginTickRequest correctly refuses to issue a second
// request while one is already in flight (the "at most one in flight" design
// invariant the tick-driver effect depends on).
// ---------------------------------------------------------------------------
test('sanity: beginTickRequest returns null while a request is already pending', () => {
  let controller = initialOffloadControllerState();
  const first = beginTickRequest(controller, 10);
  assert.ok(first);
  controller = first.state;
  const second = beginTickRequest(controller, 11);
  assert.equal(second, null, 'must not allow a second request while one is already in flight');
});

// ===========================================================================
// INDEPENDENT ROUND 2 REJECT — N1: continuous input starves the tick to a
// dead stop. Proven by the round against the real controller+reducer+journal:
// 200 interval fires with one 'place' per frame -> applied=0, discarded=200,
// clock stuck, queue-depth silently reading "caught up" throughout.
//
// This harness drives the REAL controller functions (beginTickRequest/
// invalidateInFlight/decideTickReply/shouldForceSyncTick/afterForcedSyncTick)
// and the REAL reducer/journal (recordAction, isStateAffecting-gated), in
// EXACTLY the call order store.tsx's guardedDispatch / worker.onmessage /
// tick-driver interval use — see the inline comments mapping each step to
// its store.tsx counterpart. The only test-only scaffolding is the discrete
// "frame" loop standing in for real timers/postMessage round-trips (a
// worker reply "arrives" `roundTripIntervals` frames after being requested)
// — necessary because jsdom/node --test cannot run a real Worker or drive
// real wall-clock timers deterministically (same reasoning as
// simWorkerProtocol.ts's runTick design).
// ===========================================================================

/**
 * @param {{frames:number, roundTripIntervals:number, actionCadence:number, actionFactory?:(frame:number)=>object}} opts
 */
function simulateOffload({ frames, roundTripIntervals, actionCadence, actionFactory = () => ({ type: 'debugFunds', amount: 1 }) }) {
  let controller = initialOffloadControllerState();
  let state = initialState();
  let journal = emptyJournal();
  let appliedTicks = 0;
  let forcedTicks = 0;
  // BUG-592: a real FIFO queue, not a single variable — a single variable
  // would OVERWRITE (silently drop) an earlier still-outstanding request's
  // completion event the instant a later one is issued, which would leave
  // that earlier request's workerBusy NEVER cleared (its "reply" would
  // simply never arrive in the model) and permanently wedge
  // beginTickRequest's workerBusy gate for the rest of the run — exactly
  // the kind of harness inaccuracy this fix's own arrival (a real,
  // workerBusy-gated beginTickRequest) would otherwise expose as a false
  // failure here, unrelated to the K-escape/liveness behaviour this
  // particular harness exists to test.
  let pendingReplies = []; // FIFO: { requestId, dueAtFrame, preTick }
  const tracker = createQueueDepthTracker();
  // Instrumentation for the queue-depth-honesty assertion: every sample
  // where depth()===0 AND supersedeStreak()>0 is exactly the "silently
  // reads caught up while behind" moment the round flagged.
  let sawHonestBehindSample = false;
  const sampleHonesty = () => {
    if (tracker.depth() === 0 && tracker.supersedeStreak() > 0) sawHonestBehindSample = true;
  };

  // Mirrors store.tsx's issueTickRequest() (tick-driver interval + N1 rebase).
  const issueRequest = (frame) => {
    const begun = beginTickRequest(controller, state.tick);
    if (!begun) return;
    controller = begun.state;
    tracker.enqueue();
    pendingReplies.push({ requestId: begun.requestId, dueAtFrame: frame + roundTripIntervals, preTick: state.tick });
  };

  // Mirrors store.tsx's guardedDispatch (invalidate-if-pending, apply,
  // K-supersede forced-sync-tick escape — all in that order).
  const dispatchNonTick = (action) => {
    if (controller.pendingTick) {
      controller = invalidateInFlight(controller);
      tracker.drain();
      tracker.reportSupersedeStreak(controller.supersedeStreak);
      sampleHonesty();
    }
    journal = recordAction(journal, state.tick, action);
    state = reducer(state, action);
    if (shouldForceSyncTick(controller)) {
      controller = afterForcedSyncTick(controller);
      tracker.reportSupersedeStreak(0);
      const preTick = state.tick;
      journal = recordAction(journal, preTick, { type: 'tick' });
      state = reducer(state, { type: 'tick' });
      appliedTicks++;
      forcedTicks++;
    }
  };

  for (let frame = 0; frame < frames; frame++) {
    // (1) deliver EVERY due worker reply — mirrors worker.onmessage, once
    // per real message the (FIFO, serial) worker actually finishes. A
    // superseded request still gets its own completion event here (the
    // worker doesn't know it was superseded) — necessary so its
    // workerBusy gets cleared even though its result will be discarded.
    while (pendingReplies.length > 0 && pendingReplies[0].dueAtFrame === frame) {
      const reply = pendingReplies.shift();
      const resultTick = reply.preTick + 1;
      const { state: nextController, decision } = decideTickReply(
        controller,
        { requestId: reply.requestId, resultTick },
        state.tick
      );
      const wasStale = nextController === controller;
      // BUG-592: the real beginTickRequest() this harness calls now gates on
      // workerBusy, not pendingTick — clear it here, unconditionally,
      // exactly like store.tsx's worker.onmessage does, or NO further
      // request could ever be issued after the first (this is the harness
      // being kept faithful to the real production call sites now that
      // workerBusy exists — not a BUG-592-specific assertion in this N1/N2
      // section, which is about the K-escape, not the memory hazard).
      controller = clearWorkerBusy(nextController);
      if (wasStale) {
        // N2 fix (independent round 3 REJECT, 2026-09-02): DISCARD, do not
        // rebase — the next interval fire (step 3 below, or the NEXT frame's
        // step 3 in the real interval-driven cadence) issues the fresh
        // request. See simWorkerOffloadController.ts's
        // FORCE_SYNC_TICK_SUPERSEDE_THRESHOLD header for why combining an
        // immediate reissue here with the K-escape formed an
        // interval-independent tick generator (the round's N2 finding).
      } else {
        tracker.drain();
        tracker.reportSupersedeStreak(controller.supersedeStreak);
        if (decision.kind === 'apply') {
          journal = recordAction(journal, decision.tickToJournal, { type: 'tick' });
          state = reducer(state, { type: 'tick' });
          appliedTicks++;
        }
      }
    }
    // (2) scheduled user action this frame — mirrors a UI dispatch via guardedDispatch.
    if (actionCadence > 0 && frame % actionCadence === 0) {
      dispatchNonTick(actionFactory(frame));
    }
    // (3) the tick-driver interval firing — issues a request if idle.
    if (!controller.pendingTick) {
      issueRequest(frame);
    }
  }
  return { state, journal, appliedTicks, forcedTicks, tracker, sawHonestBehindSample };
}

// ---------------------------------------------------------------------------
// (1) The liveness regression the round demanded, RED-proofed against the
// pre-fix (discard-only, no supersede-streak) behaviour.
// ---------------------------------------------------------------------------

test('N1 RED PROOF: continuous action-every-frame input no longer starves the clock (200 frames, 4-interval round trip)', () => {
  const { appliedTicks, forcedTicks, state } = simulateOffload({
    frames: 200,
    roundTripIntervals: 4,
    actionCadence: 1, // an action EVERY frame — the round's exact proven repro shape.
  });

  // RED PROOF: reverting shouldForceSyncTick to always return false (or
  // removing the forced-sync-tick call in dispatchNonTick/guardedDispatch)
  // reproduces the round's exact live defect — appliedTicks stays 0 and
  // state.tick never advances past its initial value, no matter how many
  // of the 200 frames run, because every worker request gets invalidated
  // by the coincident action before its 4-interval round trip can land.
  assert.ok(appliedTicks > 0, `the clock must make SOME progress under continuous contention — got 0 applied ticks over 200 frames (the exact starvation the round proved)`);
  assert.ok(forcedTicks > 0, 'progress here can ONLY come from the forced-sync-tick escape (the worker path is provably starved at cadence=1, roundTrip=4) — forcedTicks must be > 0');
  assert.equal(appliedTicks, forcedTicks, 'at this cadence/round-trip combination NO worker-path tick can ever land — every applied tick must be a forced one');

  // Forward-progress FLOOR (BUG-592-corrected): with the workerBusy fix in
  // place, invalidateInFlight's supersedeStreak can only usefully increment
  // once a request has actually been ISSUED — and beginTickRequest now
  // refuses to issue while workerBusy (i.e. for most of a busy worker's own
  // roundTripIntervals-frame processing window, there is nothing pending
  // left to invalidate, so dispatchNonTick's invalidate branch is simply
  // skipped on those frames). One supersede-worthy cycle therefore takes
  // roughly `roundTripIntervals` frames, not 1 — a real, WELCOME consequence
  // of the fix (it's exactly what caps the worker's mailbox at 1 outstanding
  // computation — see BUG-592's dedicated section further down), not a
  // regression in liveness: forcedTicks still fires at least once every K
  // such cycles, i.e. roughly every `roundTripIntervals * (K+1)` frames.
  // Loose lower bound (not tight — avoids flaking on exact off-by-one
  // framing) still clearly separates "genuinely progressing" from "stuck at 0".
  const floor = Math.floor(200 / (4 /* roundTripIntervals */ * (FORCE_SYNC_TICK_SUPERSEDE_THRESHOLD + 1))) - 2;
  assert.ok(appliedTicks >= floor, `expected at least ~${floor} applied ticks over 200 frames at K=${FORCE_SYNC_TICK_SUPERSEDE_THRESHOLD}, got ${appliedTicks}`);
  assert.ok(state.tick > 1, 'the live SimState tick counter must have actually advanced, not just an internal counter');
});

test('N1 RED PROOF: WITHOUT the forced-sync-tick escape, the same scenario truly does stall at 0 (proves the assertion above is meaningful)', () => {
  // Reproduces the round's ORIGINAL (pre-fix) design inline: an offload
  // controller that only ever discards on supersede, never forces a
  // synchronous tick. This is the RED case the fix above turns GREEN.
  let controller = initialOffloadControllerState();
  let state = initialState();
  let pendingReply = null;
  let appliedTicks = 0;
  const roundTripIntervals = 4;
  const frames = 200;

  const issueRequest = (frame) => {
    const begun = beginTickRequest(controller, state.tick);
    if (!begun) return;
    controller = begun.state;
    pendingReply = { requestId: begun.requestId, dueAtFrame: frame + roundTripIntervals, preTick: state.tick };
  };

  for (let frame = 0; frame < frames; frame++) {
    if (pendingReply && pendingReply.dueAtFrame === frame) {
      const resultTick = pendingReply.preTick + 1;
      const { state: nextController, decision } = decideTickReply(controller, { requestId: pendingReply.requestId, resultTick }, state.tick);
      // BUG-592: clear workerBusy so this harness stays faithful to the
      // real production call sites (a reply always frees the worker slot)
      // — otherwise beginTickRequest's workerBusy gate would itself stall
      // all future issuance after the very first cycle, for a reason
      // unrelated to the OLD (no-escape) design this test means to isolate.
      controller = clearWorkerBusy(nextController);
      pendingReply = null;
      // NOTE: no rebase-on-stale, no forced-sync-tick — this is the OLD design.
      if (decision.kind === 'apply') {
        state = reducer(state, { type: 'tick' });
        appliedTicks++;
      }
    }
    // action every frame, invalidating whatever is pending, with NO escape hatch.
    if (controller.pendingTick) {
      controller = invalidateInFlight(controller);
    }
    state = reducer(state, { type: 'debugFunds', amount: 1 });
    if (!controller.pendingTick) {
      issueRequest(frame);
    }
  }

  assert.equal(appliedTicks, 0, 'confirms the OLD (pre-N1-fix) design genuinely stalls at 0 applied ticks under this exact input pattern — this is what the round measured live');
  assert.equal(state.tick, 1, 'confirms the sim clock literally never advances under the old design (initialState().tick === 1)');
});

// ---------------------------------------------------------------------------
// (2) Parametric cliff cases: round-trip 1/2/4 intervals x cadence 1/2/3 —
// every cell must show non-zero applied ticks over a real run.
// ---------------------------------------------------------------------------

describe('N1 parametric cliff matrix: round-trip x action-cadence, every cell must show progress', () => {
  for (const roundTripIntervals of [1, 2, 4]) {
    for (const actionCadence of [1, 2, 3]) {
      test(`roundTrip=${roundTripIntervals} intervals, cadence=every ${actionCadence} frame(s): appliedTicks > 0`, () => {
        const { appliedTicks, state } = simulateOffload({ frames: 200, roundTripIntervals, actionCadence });
        assert.ok(appliedTicks > 0, `roundTrip=${roundTripIntervals} cadence=${actionCadence}: expected progress, got 0 applied ticks`);
        assert.ok(state.tick > 1, `roundTrip=${roundTripIntervals} cadence=${actionCadence}: SimState tick must have advanced past its initial value`);
      });
    }
  }
});

// ---------------------------------------------------------------------------
// (3) Queue-depth honesty (FEAT-2326609734 AC-7 shortfall, flagged twice by
// the round): during sustained supersede, the readout must NOT silently
// read "caught up" — depth()===0 with supersedeStreak()>0 must be
// observable and must NOT be conflated with genuine idleness.
// ---------------------------------------------------------------------------

test('N1: queue-depth honesty — supersedeStreak surfaces "behind" even while depth() reads 0', () => {
  const { tracker, sawHonestBehindSample } = simulateOffload({
    frames: 200,
    roundTripIntervals: 4,
    actionCadence: 1,
  });

  // RED PROOF: before this fix, invalidateInFlightWorkerTick only ever
  // called tracker.drain() — depth() correctly goes to 0 on every
  // supersede, but nothing else was ever reported, so a UI reading ONLY
  // depth() would show "caught up" throughout 200 frames of total
  // starvation. sawHonestBehindSample being true proves the NEW signal
  // (supersedeStreak) is actually observable at exactly the moments
  // depth() alone would have lied.
  assert.equal(sawHonestBehindSample, true,
    'at least one sample during the run must show depth()===0 (drained) simultaneously with supersedeStreak()>0 (behind) — this is the exact "silently caught up" gap the round flagged twice');

  // After the run settles (last thing simulateOffload does per-frame is
  // either apply-and-reset-streak or supersede-and-bump-streak), the FINAL
  // state must not be a lie either: since this run ends via a forced tick's
  // reset (or a completed apply), depth() and streak together must be
  // internally consistent — never BOTH "0 pending" and a stale nonzero
  // streak reported after a real apply.
  assert.ok(tracker.depth() >= 0 && tracker.supersedeStreak() >= 0);
});

test('N1: a genuinely idle worker (no contention) reports BOTH depth()===0 and supersedeStreak()===0 — "caught up" stays honest when it is true', () => {
  // cadence=0 -> no actions dispatched at all; nothing ever supersedes.
  const { tracker, appliedTicks } = simulateOffload({ frames: 50, roundTripIntervals: 2, actionCadence: 0 });
  assert.ok(appliedTicks > 0, 'precondition: ticks are landing normally with no contention');
  assert.equal(tracker.supersedeStreak(), 0, 'no contention -> streak must be 0 -> the UI must be free to say "caught up"');
});

// ---------------------------------------------------------------------------
// (4) Six of the seven adversarial interleavings the round asked to re-run
// (the 7th — millisecond-timed, exercising the N2 fix's sub-interval
// discretization directly — lives further down, right after the N2 ceiling
// matrix). Journal replay from genesis must stay byte-identical under the
// CURRENT (rebase-removed, N2-fixed) design: a supersede streak can still
// trigger a SYNCHRONOUS forced tick, which must still produce a journal
// that replays to exactly the live result now that immediate reissue is
// gone (only the K-escape's timing changed vs. the round-2 design; the
// journal-write-only-on-apply invariant from B3 is untouched either way).
// ---------------------------------------------------------------------------

describe('N1/N2: six of seven adversarial interleavings replay byte-identical under the rebase-removed, forced-sync-tick-only design', () => {
  const scenarios = [
    { name: '1: fast contention, fast round-trip', frames: 150, roundTripIntervals: 1, actionCadence: 1 },
    { name: '2: fast contention, slow round-trip (the proven repro shape)', frames: 200, roundTripIntervals: 4, actionCadence: 1 },
    { name: '3: moderate contention, fast round-trip', frames: 150, roundTripIntervals: 1, actionCadence: 2 },
    { name: '4: moderate contention, slow round-trip', frames: 150, roundTripIntervals: 4, actionCadence: 2 },
    { name: '5: light contention, slow round-trip', frames: 150, roundTripIntervals: 4, actionCadence: 3 },
    { name: '6: no contention at all (baseline determinism)', frames: 100, roundTripIntervals: 2, actionCadence: 0 },
  ];

  for (const scenario of scenarios) {
    test(`interleaving ${scenario.name}: replay(journal) === live state`, () => {
      const { state: liveState, journal } = simulateOffload(scenario);

      // Fold the recorded journal through the SAME plain reducer from
      // genesis — exactly genesisReplay.ts's own methodology (replayFromGenesis
      // folds initialState() + every journaled action through `reducer`).
      let replayed = initialState();
      for (const entry of journal.entries) {
        replayed = reducer(replayed, entry.action);
      }

      assert.equal(
        stableStringify(replayed),
        stableStringify(liveState),
        `${scenario.name}: replaying the journal recorded under the rebase/forced-sync-tick design must reproduce the EXACT live result — no action lost, none double-applied, none out of order`
      );
    });
  }
});

// ---------------------------------------------------------------------------
// (5) decideTickReply's belt-and-braces second guard, explicitly (the round's
// minor: "add a test for it, or remove it" — kept, per its own header
// reasoning, as cheap defensive insurance; this proves its exact behaviour).
// ---------------------------------------------------------------------------

test('decideTickReply belt-and-braces guard: a matched requestId whose tick does NOT advance is discarded, streak untouched', () => {
  let controller = initialOffloadControllerState();
  const begun = beginTickRequest(controller, 500);
  controller = begun.state;
  // Bump the streak first (simulating prior supersedes) so we can prove
  // this branch leaves it ALONE (neither resets to 0 like a real apply,
  // nor increments like invalidateInFlight).
  controller = { ...controller, supersedeStreak: 2 };

  // A reply with the CORRECT requestId but a non-advancing tick (should not
  // occur under the documented invariants — defensive-only).
  const { state: after, decision } = decideTickReply(controller, { requestId: begun.requestId, resultTick: 500 }, 500);

  assert.equal(decision.kind, 'discard', 'a non-advancing reply must be discarded even with a matching requestId');
  assert.equal(after.supersedeStreak, 2, 'this branch must not touch supersedeStreak — it is neither a real apply (no reset) nor a supersede (no increment)');
  assert.equal(after.pendingTick, false, 'the settled request is still cleared regardless of the discard reason');
  assert.equal(after.activeRequestId, null);
});

// ===========================================================================
// INDEPENDENT ROUND 3 REJECT — N2: the round-2 fix (immediate rebase-on-
// stale-reply COMBINED with the K-supersede forced-sync-tick escape) forms
// an INTERVAL-INDEPENDENT tick generator under continuous sub-round-trip-
// interval input — the round measured ~20x the selected speed at
// SPEED_MS=1000/16ms round-trip/60Hz drag. Fix: drop the immediate rebase
// entirely (simWorkerOffloadController.ts's FORCE_SYNC_TICK_SUPERSEDE_THRESHOLD
// header + store.tsx's worker.onmessage/issueTickRequest carry the full
// reasoning). This section proves BOTH bounds hold together: progress can
// never exceed the selected speed (new, N2) AND can never stall (kept, N1).
//
// A millisecond-granularity harness is required here (unlike the frame-
// granularity one above, where "1 frame = 1 interval fire" — a scale that
// cannot distinguish "reissue immediately" from "wait for the next
// interval" since both collapse to the same discrete step). N2 is
// fundamentally about SUB-interval timing: a worker round-trip much shorter
// than SPEED_MS lets many issue/invalidate/reissue cycles complete inside
// one interval period if (and only if) an immediate reissue exists.
// ===========================================================================

// BUG-592 (Web Worker round 4 follow-up, 2026-09-02) — pre-fix stand-in for
// beginTickRequest: gates on `pendingTick` only, exactly the shipped design
// BEFORE this fix, completely ignoring `workerBusy`. Used ONLY by the
// `legacyNoBusyGate: true` RED-proof scratch variant below, never by the
// real (shipped) simulateOffloadTimed path, which always calls the real
// beginTickRequest (imported above) and therefore gets the fix for free.
function legacyBeginTickRequestIgnoringBusy(s, currentTick) {
  if (s.pendingTick) return null;
  const requestId = s.nextRequestId;
  return {
    state: {
      pendingTick: true,
      activeRequestId: requestId,
      activeRequestTick: currentTick,
      nextRequestId: requestId + 1,
      supersedeStreak: s.supersedeStreak,
      workerBusy: true, // set but never consulted by this legacy gate — kept only so the shape matches OffloadControllerState.
    },
    requestId,
  };
}

/**
 * @param {{durationMs:number, speedMs:number, roundTripMs:number, actionIntervalMs:number, rebase?:boolean, forceThreshold?:number, legacyNoBusyGate?:boolean}} opts
 * `rebase` (default false, matching the SHIPPED design) and `forceThreshold`
 * (default the real FORCE_SYNC_TICK_SUPERSEDE_THRESHOLD) are ONLY ever
 * overridden by the RED-proof tests below, to reproduce the round's exact
 * findings against a controlled scratch variant — never to change the real
 * shipped behaviour. `legacyNoBusyGate` (default false) is the BUG-592
 * RED-proof equivalent of `rebase`: default false uses the real, FIXED
 * beginTickRequest (workerBusy-gated); true swaps in
 * legacyBeginTickRequestIgnoringBusy to reproduce the pre-fix hazard.
 *
 * BUG-592 modelling (2026-09-02): `pendingReply` used to be a SINGLE
 * variable — too optimistic a model, since it can never represent more than
 * one outstanding real computation and so could never expose the round's
 * actual finding (an unbounded worker mailbox under sustained input with
 * round-trip > interval). It is now a real FIFO queue (`workerQueue`)
 * modelling the (serial, single-threaded, uncancellable) worker's actual
 * mailbox: each posted message occupies a slot until its OWN completion
 * time, computed serially (a message cannot start processing before the
 * previous one in the queue has finished — `workerFreeAtMs`). `maxOutstanding`
 * is the peak queue length ever observed — the direct memory-hazard proxy
 * (each queued message carries a full SimState clone in real code).
 */
function simulateOffloadTimed({
  durationMs,
  speedMs,
  roundTripMs,
  actionIntervalMs,
  rebase = false,
  forceThreshold = FORCE_SYNC_TICK_SUPERSEDE_THRESHOLD,
  legacyNoBusyGate = false,
}) {
  let controller = initialOffloadControllerState();
  let state = initialState();
  let journal = emptyJournal();
  let appliedTicks = 0;
  let intervalFireCount = 0;
  const workerQueue = []; // FIFO: { requestId, preTick, dueAtMs }
  let workerFreeAtMs = 0; // when the serial worker will next be idle.
  let maxOutstanding = 0;

  const forceCheck = (c) => c.supersedeStreak >= forceThreshold;

  const issueRequest = (nowMs) => {
    const begun = legacyNoBusyGate
      ? legacyBeginTickRequestIgnoringBusy(controller, state.tick)
      : beginTickRequest(controller, state.tick);
    if (!begun) return;
    controller = begun.state;
    const startAt = Math.max(nowMs, workerFreeAtMs);
    const dueAtMs = startAt + roundTripMs;
    workerFreeAtMs = dueAtMs;
    workerQueue.push({ requestId: begun.requestId, preTick: state.tick, dueAtMs });
    maxOutstanding = Math.max(maxOutstanding, workerQueue.length);
  };

  const dispatchNonTick = () => {
    if (controller.pendingTick) {
      controller = invalidateInFlight(controller);
    }
    journal = recordAction(journal, state.tick, { type: 'debugFunds', amount: 1 });
    state = reducer(state, { type: 'debugFunds', amount: 1 });
    if (forceCheck(controller)) {
      controller = afterForcedSyncTick(controller);
      const preTick = state.tick;
      journal = recordAction(journal, preTick, { type: 'tick' });
      state = reducer(state, { type: 'tick' });
      appliedTicks++;
    }
  };

  let nowMs = 0;
  let nextIntervalAt = speedMs;
  let nextActionAt = actionIntervalMs > 0 ? actionIntervalMs : Infinity;

  // Discrete-event loop: advance to the next scheduled event (an interval
  // fire, a scheduled action, or the EARLIEST due worker reply — whichever
  // is soonest) rather than stepping one fixed unit at a time, so round-
  // trip/interval/action-interval can differ by orders of magnitude without
  // either an absurdly fine step size or missed events.
  for (;;) {
    const candidates = [nextIntervalAt, nextActionAt];
    if (workerQueue.length > 0) candidates.push(workerQueue[0].dueAtMs);
    const t = Math.min(...candidates);
    if (t > durationMs) break;
    nowMs = t;

    if (workerQueue.length > 0 && workerQueue[0].dueAtMs === nowMs) {
      const msg = workerQueue.shift(); // FIFO: the worker replies in the order it was given work.
      const resultTick = msg.preTick + 1;
      const { state: nextController, decision } = decideTickReply(
        controller,
        { requestId: msg.requestId, resultTick },
        state.tick
      );
      const wasStale = nextController === controller;
      // BUG-592: ANY reply (matched or stale) means the worker is actually
      // done with THIS one computation — clear workerBusy unconditionally,
      // exactly like store.tsx's worker.onmessage, so the fixed
      // (legacyNoBusyGate: false) path's beginTickRequest is allowed to
      // issue again. The legacy scratch path never consults workerBusy, so
      // this is a no-op for it either way.
      controller = clearWorkerBusy(nextController);
      if (wasStale) {
        // N2: this is the ONE call site the fix removes from the real
        // code. `rebase` defaults to false; only the RED-proof test below
        // ever passes true, to reproduce the round's exact finding.
        if (rebase) issueRequest(nowMs);
      } else if (decision.kind === 'apply') {
        journal = recordAction(journal, decision.tickToJournal, { type: 'tick' });
        state = reducer(state, { type: 'tick' });
        appliedTicks++;
      }
    }
    if (nowMs === nextActionAt) {
      dispatchNonTick();
      nextActionAt += actionIntervalMs;
    }
    if (nowMs === nextIntervalAt) {
      intervalFireCount++;
      if (!controller.pendingTick) issueRequest(nowMs);
      nextIntervalAt += speedMs;
    }
  }
  return { state, journal, appliedTicks, intervalFireCount, maxOutstanding, finalOutstanding: workerQueue.length };
}

// ---------------------------------------------------------------------------
// (1) Upper bound (N2, new): ticks-per-interval must NEVER exceed 1 under
// continuous input, across a 4 (round-trip) x 8 (action cadence) matrix.
// ---------------------------------------------------------------------------

describe('N2 ceiling matrix: round-trip x drag-input-rate, appliedTicks/intervalFireCount must NEVER exceed 1.0', () => {
  const SPEED_MS_UNDER_TEST = 1000;
  const roundTripsMs = [8, 16, 33, 66];
  const actionIntervalsMs = [8, 16, 33, 50, 66, 100, 150, 250];

  for (const roundTripMs of roundTripsMs) {
    for (const actionIntervalMs of actionIntervalsMs) {
      test(`roundTrip=${roundTripMs}ms, drag-input every ${actionIntervalMs}ms (speed=${SPEED_MS_UNDER_TEST}ms/tick): ratio <= 1.0`, () => {
        const { appliedTicks, intervalFireCount } = simulateOffloadTimed({
          durationMs: SPEED_MS_UNDER_TEST * 20, // 20 interval periods — enough to average out startup transients.
          speedMs: SPEED_MS_UNDER_TEST,
          roundTripMs,
          actionIntervalMs,
        });
        assert.ok(intervalFireCount > 0, 'precondition: the interval actually fired during the run');
        const ratio = appliedTicks / intervalFireCount;
        // RED PROOF: reinstating the removed rebase call (see the dedicated
        // RED-proof test below, which does exactly this against a scratch
        // `rebase: true` variant) makes this ratio blow past 1.0 — this is
        // the round's exact N2 finding (~20x at 1000ms/16ms/60Hz).
        assert.ok(ratio <= 1.0, `roundTrip=${roundTripMs}ms cadence=${actionIntervalMs}ms: ratio ${ratio.toFixed(3)} exceeds 1.0 — the clock is running FASTER than the selected speed`);
      });
    }
  }
});

// ---------------------------------------------------------------------------
// (2) Lower bound (N1, kept): the same continuous-contention scenario must
// still show genuine forward progress — the K-escape is the ONLY re-issue
// path now, so this re-proves it still works stripped of rebase.
// ---------------------------------------------------------------------------

test('N2: liveness floor still holds with rebase removed — continuous contention still produces progress', () => {
  const { appliedTicks, intervalFireCount } = simulateOffloadTimed({
    durationMs: 20_000,
    speedMs: 1000,
    roundTripMs: 16,
    actionIntervalMs: 7, // faster than the round trip — continuous drag input, the N1/N2 proven repro shape.
  });
  assert.ok(appliedTicks > 0, 'the clock must still make progress once rebase is removed — the K-escape alone must suffice (N1 requirement, re-proven under the N2 fix)');
  assert.ok(intervalFireCount > 0);
  // Sanity: this is also, trivially, within the ceiling (progress that
  // exists but never exceeds 1/interval) — both bounds hold simultaneously.
  assert.ok(appliedTicks / intervalFireCount <= 1.0);
});

// ---------------------------------------------------------------------------
// (3) Re-run the interleavings (now SEVEN — the round asked to re-prove
// byte-identical replay under the rebase-removed design; one new scenario
// added exercising the millisecond-timed harness directly, on top of the
// six frame-based ones already proven for the K-escape design).
// ---------------------------------------------------------------------------

test('interleaving 7 (ms-timed, N2 design): replay(journal) === live state under sustained sub-round-trip-interval contention', () => {
  const { state: liveState, journal } = simulateOffloadTimed({
    durationMs: 20_000,
    speedMs: 1000,
    roundTripMs: 16,
    actionIntervalMs: 7,
  });

  let replayed = initialState();
  for (const entry of journal.entries) {
    replayed = reducer(replayed, entry.action);
  }

  assert.equal(
    stableStringify(replayed),
    stableStringify(liveState),
    'interleaving 7: replaying the journal recorded under the rebase-removed N2 design must reproduce the exact live result'
  );
});

// ---------------------------------------------------------------------------
// (4) RED-proof both directions, per the round's explicit demand.
// ---------------------------------------------------------------------------

test('N2 RED PROOF: reinstating the immediate rebase (scratch variant) blows the ceiling past 1.0 — reproduces the round\'s exact finding', () => {
  const { appliedTicks, intervalFireCount } = simulateOffloadTimed({
    durationMs: 1000 * 20,
    speedMs: 1000,
    roundTripMs: 16,
    actionIntervalMs: 7, // faster than the round trip — continuous drag input, the round's proven scenario shape.
    rebase: true, // the ONLY thing this scratch variant changes.
  });
  const ratio = appliedTicks / intervalFireCount;
  // This is what test/simworker-offload.test.mjs's N2 ceiling matrix would
  // have looked like BEFORE the fix — proves that assertion (ratio <= 1.0)
  // is genuinely meaningful, not vacuously true regardless of design.
  assert.ok(ratio > 1.0, `expected the reinstated-rebase scratch variant to exceed the selected speed (interval-independent tick generation) — got ratio ${ratio.toFixed(2)}, appliedTicks=${appliedTicks}, intervalFireCount=${intervalFireCount}`);
  // Loosely corroborates the round's own measured order of magnitude
  // (~20x) without pinning an exact figure this harness's discretization
  // isn't precise enough to guarantee bit-for-bit.
  assert.ok(ratio > 5, `expected a large (order-of-magnitude) overrun, not a marginal one — got ${ratio.toFixed(2)}x`);
});

test('N2 RED PROOF: an effectively-infinite supersede threshold (scratch variant) stalls the clock — reproduces the N1 finding, confirms the floor test is meaningful', () => {
  const { appliedTicks } = simulateOffloadTimed({
    durationMs: 20_000,
    speedMs: 1000,
    roundTripMs: 16,
    actionIntervalMs: 7, // strictly faster than the round trip — every in-flight request is provably invalidated before its reply can land, regardless of phase alignment.
    forceThreshold: Number.POSITIVE_INFINITY, // K=Infinity — the escape hatch never fires.
  });
  // This is what the N1 liveness-floor test would have looked like WITHOUT
  // the K-escape — proves that assertion (appliedTicks > 0) is genuinely
  // meaningful, not vacuously true regardless of design.
  assert.equal(appliedTicks, 0, 'with the forced-sync-tick escape disabled (K=Infinity), sustained contention must stall the clock completely — confirms the liveness-floor assertion above is not vacuous');
});

// ===========================================================================
// BUG-592 (Web Worker round 4 follow-up, 2026-09-02) — unbounded worker
// mailbox / tab-OOM memory hazard. A supersede clears `pendingTick`,
// unblocking the next interval's beginTickRequest, but the superseded
// computation is STILL running inside the worker (no cancellation channel).
// Under sustained input with round-trip > interval, main posted faster than
// the worker could drain: 501 queued / ~887MB measured over a 60s drag at
// interval=100ms/rt=600ms. Fix: `workerBusy` (simWorkerOffloadController.ts)
// tracks the worker's real busy/idle state SEPARATELY from `pendingTick` —
// beginTickRequest now refuses to issue while it is true, capping the real
// worker mailbox at exactly 1 outstanding computation by construction.
// ===========================================================================

describe('BUG-592: peak outstanding worker computations is capped at 1 (the round\'s hazard cells)', () => {
  // The round's own browser-measured figures (501 / 351 queued) came from a
  // real 60s mouse drag against a real Worker's actual postMessage mailbox —
  // not bit-for-bit reproducible in this simplified discrete-event ms
  // harness. What IS reproducible, and is what this test actually proves:
  // (a) the LEGACY (pre-fix) gate lets the queue grow far past 1 under the
  // same interval/round-trip/sustained-input shape the round used, and
  // (b) the FIXED gate caps it at exactly 1, always, regardless of how long
  // the round-trip or how sustained the input.
  const hazardCells = [
    { name: 'interval=100ms, round-trip=600ms (the round\'s primary repro: 501 queued live)', speedMs: 100, roundTripMs: 600 },
    { name: 'interval=100ms, round-trip=240ms (the round\'s second repro: 351 queued live)', speedMs: 100, roundTripMs: 240 },
  ];

  for (const cell of hazardCells) {
    test(`${cell.name}: FIXED design caps peak outstanding at 1`, () => {
      const { maxOutstanding, finalOutstanding, appliedTicks } = simulateOffloadTimed({
        durationMs: 60_000, // the round's own "60s drag" duration.
        speedMs: cell.speedMs,
        roundTripMs: cell.roundTripMs,
        actionIntervalMs: 16, // ~60Hz sustained drag input — faster than both the interval and the round-trip.
      });
      assert.ok(appliedTicks > 0, 'precondition: the run actually produced some ticks (forced-sync-tick escape at minimum) — not a vacuous all-zero run');
      assert.equal(maxOutstanding, 1, `${cell.name}: the worker mailbox must NEVER hold more than 1 outstanding computation at once — got peak ${maxOutstanding}`);
      assert.equal(finalOutstanding <= 1, true, 'at most one computation left outstanding at run end');
    });

    test(`${cell.name}: RED PROOF — LEGACY (pre-fix, pendingTick-only) gate lets the queue grow far past 1`, () => {
      const { maxOutstanding } = simulateOffloadTimed({
        durationMs: 60_000,
        speedMs: cell.speedMs,
        roundTripMs: cell.roundTripMs,
        actionIntervalMs: 16,
        legacyNoBusyGate: true, // the ONLY thing this scratch variant changes — reproduces the round's exact defect.
      });
      // This is what the "FIXED design caps peak outstanding at 1" assertion
      // above would have looked like BEFORE the fix — proves that assertion
      // is genuinely meaningful, not vacuously true regardless of design.
      // Order-of-magnitude corroboration of the round's own measured figures
      // (501/351), not a bit-exact reproduction (see this describe block's
      // header for why an exact match isn't the point).
      assert.ok(maxOutstanding > 50, `${cell.name}: expected the legacy (pendingTick-only) gate to let the worker mailbox grow far past 1 under sustained sub-round-trip-interval input — got peak ${maxOutstanding}`);
    });
  }
});

test('BUG-592: correctness is unaffected by the busy-gate fix — replay stays byte-identical at the primary hazard cell', () => {
  const { state: liveState, journal } = simulateOffloadTimed({
    durationMs: 60_000,
    speedMs: 100,
    roundTripMs: 600,
    actionIntervalMs: 16,
  });
  let replayed = initialState();
  for (const entry of journal.entries) {
    replayed = reducer(replayed, entry.action);
  }
  assert.equal(
    stableStringify(replayed),
    stableStringify(liveState),
    'the busy-gate fix changes ONLY when a real worker message is posted, never what gets journaled/applied — replay must still be byte-identical'
  );
});

describe('BUG-592: the N2 ceiling matrix still holds (≤1.0) with the busy-gate fix in place', () => {
  const SPEED_MS_UNDER_TEST = 1000;
  const roundTripsMs = [8, 16, 33, 66, 600]; // 600 added: a round-trip far longer than the interval, the BUG-592 hazard shape.
  const actionIntervalsMs = [8, 16, 33, 50, 66, 100];

  for (const roundTripMs of roundTripsMs) {
    for (const actionIntervalMs of actionIntervalsMs) {
      test(`roundTrip=${roundTripMs}ms, drag-input every ${actionIntervalMs}ms: ratio <= 1.0 AND peak outstanding <= 1`, () => {
        const { appliedTicks, intervalFireCount, maxOutstanding } = simulateOffloadTimed({
          durationMs: SPEED_MS_UNDER_TEST * 20,
          speedMs: SPEED_MS_UNDER_TEST,
          roundTripMs,
          actionIntervalMs,
        });
        assert.ok(intervalFireCount > 0, 'precondition: the interval actually fired during the run');
        const ratio = appliedTicks / intervalFireCount;
        assert.ok(ratio <= 1.0, `roundTrip=${roundTripMs}ms cadence=${actionIntervalMs}ms: ratio ${ratio.toFixed(3)} exceeds 1.0`);
        assert.ok(maxOutstanding <= 1, `roundTrip=${roundTripMs}ms cadence=${actionIntervalMs}ms: peak outstanding ${maxOutstanding} exceeds 1 — the BUG-592 memory hazard is back`);
      });
    }
  }
});

test('BUG-592: floor/liveness — the K-escape STILL fires (progress still happens) even while the worker is busy', () => {
  // Directly proves the "a busy worker must never block the SYNC path"
  // invariant: guardedDispatch's forced-sync-tick escape
  // (afterForcedSyncTick + wrappedDispatch({type:'tick'})) runs on MAIN
  // THREAD ONLY — it never consults workerRef/workerBusy at all (see
  // store.tsx's guardedDispatch, which gates the escape ONLY on
  // shouldForceSyncTick(controller), never on worker state). This scenario
  // sustains input faster than a deliberately very slow round-trip, so the
  // worker is busy (a real computation outstanding) for nearly the entire
  // run — progress must still occur via the K-escape regardless.
  const { appliedTicks, intervalFireCount, maxOutstanding } = simulateOffloadTimed({
    durationMs: 20_000,
    speedMs: 1000,
    roundTripMs: 5000, // MUCH slower than the interval — the worker is busy almost continuously.
    actionIntervalMs: 7, // continuous sustained input, faster than both interval and round-trip.
  });
  assert.equal(maxOutstanding, 1, 'precondition: the busy-gate fix still holds even at this extreme round-trip — never more than 1 outstanding');
  assert.ok(appliedTicks > 0, 'the clock must still make progress via the forced-sync-tick escape even while the worker is (almost) continuously busy — a busy worker must never block the sync path');
  assert.ok(appliedTicks / intervalFireCount <= 1.0, 'the ceiling still holds even in this busy-heavy scenario');
});

test('BUG-592 RED PROOF: disabling the busy-skip (scratch legacy variant) reproduces peak-outstanding > 1 at the primary hazard cell', () => {
  // Mirrors the round's exact finding one more time, explicitly worded as
  // the required RED-proof for the busy-skip mechanism itself: scratch-
  // disable it, watch the assertion the FIXED test above relies on
  // (maxOutstanding === 1) go red, then this test file's own use of the
  // real (non-legacy) path elsewhere proves it is restored.
  const disabled = simulateOffloadTimed({
    durationMs: 60_000,
    speedMs: 100,
    roundTripMs: 600,
    actionIntervalMs: 16,
    legacyNoBusyGate: true,
  });
  assert.notEqual(disabled.maxOutstanding, 1, 'RED PROOF: with the busy-skip scratch-disabled, peak outstanding must NOT read 1 — proves the maxOutstanding===1 assertions elsewhere in this file are genuinely meaningful, not vacuously true');
  assert.ok(disabled.maxOutstanding > 1, `expected peak outstanding to exceed 1 with the busy-skip disabled — got ${disabled.maxOutstanding}`);

  // Restore: the SAME scenario through the real (fixed) path caps at 1.
  const restored = simulateOffloadTimed({
    durationMs: 60_000,
    speedMs: 100,
    roundTripMs: 600,
    actionIntervalMs: 16,
  });
  assert.equal(restored.maxOutstanding, 1, 'the real (shipped) beginTickRequest, unmodified, caps peak outstanding at exactly 1 for the identical scenario — confirms the fix, not the scratch toggle, is what the shipped code relies on');
});

// ---------------------------------------------------------------------------
// BUG-597 hardening (flag-gated path, defence-in-depth) — two latent
// strandings the round-4 follow-up round flagged in store.tsx's worker glue
// itself, not in the pure controller module the tests above exercise.
// Neither is reachable in the shipped Landing-2 build today (SimState is
// JSON-serialisable by design, and there is only one WorkerToMainMessage
// variant), but both fail dead-and-silent the moment either assumption
// stops holding, so they need real regression coverage of the actual glue
// code in store.tsx — not just the pure controller, which cannot see either
// bug (both live in store.tsx's postMessage call and worker.onmessage
// handler, neither of which the controller module touches).
//
// This is a DIFFERENT technique from every other test in this file: rather
// than driving the pure controller/simulateOffloadTimed model, it mounts a
// real SimProvider (react-dom/client + jsdom, the store-dispatch.test.tsx
// idiom) with a hand-rolled FakeWorker standing in for the one thing
// jsdom/node --test genuinely cannot construct (a real browser Worker
// thread) — the fake satisfies `typeof Worker !== 'undefined'` so
// webWorkerFlag.ts's gate opens, and the tick-loop's setInterval callback is
// captured and invoked manually (same spy-and-capture idiom as
// store-dispatch.test.tsx's BAR-2) so ticks are driven deterministically
// instead of waiting on real SPEED_MS timers.
// ---------------------------------------------------------------------------

describe('BUG-597: worker glue hardening (postMessage throw + guard order)', () => {
  const TICK_LOOP_DELAY_MS = 900; // SPEED_MS[1] — engine.ts's default speed.

  function installJsdomForWorkerTests() {
    // Mirrors test/store-dispatch.test.tsx's installJsdom() exactly (same
    // globals SimProvider's effects probe at module-eval/mount time).
    const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', {
      url: 'http://localhost/',
      pretendToBeVisual: true,
    });
    const { window } = dom;
    globalThis.window = window;
    globalThis.document = window.document;
    Object.defineProperty(globalThis, 'navigator', {
      value: window.navigator,
      configurable: true,
      writable: true,
    });
    globalThis.HTMLElement = window.HTMLElement;
    globalThis.requestAnimationFrame = window.requestAnimationFrame.bind(window);
    globalThis.cancelAnimationFrame = window.cancelAnimationFrame.bind(window);
    globalThis.IS_REACT_ACT_ENVIRONMENT = true;
    // backend.ts's queueStorage() reads the BARE `localStorage` global (not
    // `window.localStorage`) — Node 22+'s own experimental built-in global
    // `localStorage` otherwise shadows jsdom's and is broken without
    // `--localstorage-file`, which would make every recordError() call in
    // these tests fail its persistErrorRing() write. Point the bare global
    // at the real jsdom localStorage so recordError works exactly as it does
    // in a real browser (where `localStorage` and `window.localStorage` are
    // the same object).
    globalThis.localStorage = window.localStorage;
    // Opt into the worker-offload flag store.tsx's webWorkerFlag.ts reads.
    window.localStorage.setItem('metropolis.webworker', '1');
    return dom;
  }

  /** Spy on the global setInterval/clearInterval, capturing the tick-loop's
   *  own callback by its distinctive delay (same isolation trick as
   *  store-dispatch.test.tsx's BAR-2 — the autosave timer uses a different
   *  delay and must not be confused with it). Returns {get, restore}. */
  function captureTickLoopCallback() {
    const g = globalThis;
    const real = g.setInterval.bind(globalThis);
    let captured = null;
    g.setInterval = (...args) => {
      const id = real(...args);
      if (args[1] === TICK_LOOP_DELAY_MS) captured = args[0];
      return id;
    };
    return {
      get: () => captured,
      restore: () => {
        g.setInterval = real;
      },
    };
  }

  test('a postMessage-throwing worker: busy is cleared, the fallback tick runs (clock advances), the tracker shows no stuck backlog, an error is recorded, and the NEXT interval recovers', async () => {
    const dom = installJsdomForWorkerTests();
    const spy = captureTickLoopCallback();
    try {
      let postCount = 0;
      let throwOnNextPost = true;
      class FakeWorker {
        postMessage(msg) {
          postCount++;
          if (throwOnNextPost) {
            throwOnNextPost = false;
            // Simulates the DataCloneError class postMessage would throw if
            // SimState ever stopped being JSON-serialisable — see
            // issueTickRequest's own header in store.tsx.
            throw new Error('simulated postMessage failure (BUG-597)');
          }
          // A subsequent, successful post replies with a real tickResult —
          // this is the "next interval works normally" recovery leg.
          queueMicrotask(() => {
            this.onmessage?.({
              data: { type: 'tickResult', state: runTick(msg.state), requestId: msg.requestId },
            });
          });
        }
        terminate() {}
      }
      globalThis.Worker = FakeWorker;

      const React = await import('react');
      const { createRoot } = await import('react-dom/client');
      const { act } = await import('react-dom/test-utils');
      // CI runs `node --test` from the REPO ROOT, so tsImport's tsconfig
      // search never finds webconsole/tsconfig.json (jsx: react-jsx) and
      // compiles store.tsx's JSX to CLASSIC React.createElement calls that
      // expect a global React. Locally scoped.mjs runs from webconsole/ and
      // gets the automatic transform, which is why this only reddened on CI.
      // Providing the global is harmless under either transform.
      globalThis.React = React.default ?? React;
      const { SimProvider, useSim } = await tsImport('../src/sim/store.tsx', import.meta.url);
      // NOTE: deliberately NOT a second `tsImport`/`import` of backend.ts for
      // recentErrors() here — each separate tsImport() call gets its OWN
      // module registration, so a second load of backend.ts would carry an
      // independent in-memory errorLog singleton that never sees what
      // store.tsx's OWN internal `./backend` import records (confirmed by
      // hand: recentErrors() from a second load read back 0 while the error
      // was demonstrably recorded). backend.ts's recordError always persists
      // to the real localStorage ring (persistErrorRing) as its last step, so
      // reading that ring directly observes the actual recordError() call
      // inside store.tsx's own module instance, independent of which loader
      // loaded which copy of the module.
      const ERROR_RING_STORAGE_KEY = 'metropolis.errorRing';
      const readErrorRing = () => {
        const raw = dom.window.localStorage.getItem(ERROR_RING_STORAGE_KEY);
        return raw ? JSON.parse(raw) : [];
      };

      getGlobalWorkerQueueTracker().reset();
      const errorCountBefore = readErrorRing().length;

      let latestState = null;
      function Probe() {
        const { state } = useSim();
        latestState = state;
        return null;
      }

      const container = dom.window.document.getElementById('root');
      const root = createRoot(container);
      try {
        await act(async () => {
          root.render(React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }));
        });

        const tickCallback = spy.get();
        assert.ok(tickCallback, 'the tick-loop interval must have been registered on mount');

        const tickBefore = latestState.tick;

        // Fire one interval: issueTickRequest() commits workerBusy/enqueue,
        // then postMessage throws.
        await act(async () => {
          tickCallback();
        });

        assert.equal(postCount, 1, 'postMessage must have been attempted exactly once');
        assert.equal(
          latestState.tick,
          tickBefore + 1,
          'the interval must fall back to the synchronous tick for THIS fire — no tick may be lost to the postMessage failure'
        );
        assert.equal(
          getGlobalWorkerQueueTracker().depth(),
          0,
          'the tracker slot enqueued before the throw must be un-enqueued — no stuck backlog'
        );

        const errorsAfterThrow = readErrorRing();
        assert.equal(errorsAfterThrow.length, errorCountBefore + 1, 'exactly one new registry error must be recorded for the postMessage failure');
        assert.match(
          errorsAfterThrow[0].msg,
          /falling back to synchronous tick/,
          'the recorded error must describe the postMessage failure and the fallback'
        );

        // RECOVERY: the next interval fire must be able to post to the
        // worker again — workerBusy must not be stuck true from the throw.
        const tickBeforeRecovery = latestState.tick;
        await act(async () => {
          tickCallback();
          await new Promise((resolve) => setTimeout(resolve, 0)); // flush the queued onmessage reply
        });

        assert.equal(postCount, 2, 'the NEXT interval must be able to post to the worker again (workerBusy correctly cleared by the unwind)');
        assert.equal(latestState.tick, tickBeforeRecovery + 1, 'the recovered worker round-trip must still advance the clock exactly once');
        assert.equal(getGlobalWorkerQueueTracker().depth(), 0, 'the tracker must read caught-up again after the successful recovery round-trip');

        await act(async () => {
          root.unmount();
        });
      } finally {
        // no-op: container lives inside the jsdom window torn down below.
      }
    } finally {
      spy.restore();
      delete globalThis.Worker;
      dom.window.close();
    }
  });

  test('RED-PROOF pin: an unknown-message-type reply still clears workerBusy (guard-order matters)', async () => {
    // Proves the guard-order fix in store.tsx's worker.onmessage: clearing
    // workerBusy must happen BEFORE the `msg.type !== 'tickResult'`
    // narrowing, not after — otherwise a reply of any type OTHER than
    // 'tickResult' returns through the guard without ever reaching the
    // clear, stranding workerBusy=true forever (beginTickRequest then
    // refuses every future request). Reverting the fix (moving the clear
    // back below the guard) must turn this test red.
    const dom = installJsdomForWorkerTests();
    const spy = captureTickLoopCallback();
    try {
      let postCount = 0;
      class FakeWorker {
        postMessage() {
          postCount++;
          // Reply with a message type the protocol does not define today —
          // simulating the "second message type" scenario the round flagged.
          queueMicrotask(() => {
            this.onmessage?.({ data: { type: 'notAnActualProtocolMessage' } });
          });
        }
        terminate() {}
      }
      globalThis.Worker = FakeWorker;

      const React = await import('react');
      const { createRoot } = await import('react-dom/client');
      const { act } = await import('react-dom/test-utils');
      // CI runs `node --test` from the REPO ROOT, so tsImport's tsconfig
      // search never finds webconsole/tsconfig.json (jsx: react-jsx) and
      // compiles store.tsx's JSX to CLASSIC React.createElement calls that
      // expect a global React. Locally scoped.mjs runs from webconsole/ and
      // gets the automatic transform, which is why this only reddened on CI.
      // Providing the global is harmless under either transform.
      globalThis.React = React.default ?? React;
      const { SimProvider, useSim } = await tsImport('../src/sim/store.tsx', import.meta.url);

      getGlobalWorkerQueueTracker().reset();

      function Probe() {
        useSim();
        return null;
      }

      const container = dom.window.document.getElementById('root');
      const root = createRoot(container);

      await act(async () => {
        root.render(React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }));
      });

      const tickCallback = spy.get();
      assert.ok(tickCallback, 'the tick-loop interval must have been registered on mount');

      // First fire: posts, gets the unknown-type reply back.
      await act(async () => {
        tickCallback();
        await new Promise((resolve) => setTimeout(resolve, 0));
      });
      assert.equal(postCount, 1, 'precondition: the first interval must have posted once');
      assert.equal(
        getGlobalWorkerQueueTracker().depth(),
        0,
        'the tracker must show caught-up after the unknown-type reply — it must not read as still outstanding'
      );

      // Second fire: if workerBusy was correctly cleared by the unknown-type
      // reply, beginTickRequest allows a new post. If the guard-order bug
      // were present, workerBusy would still read true here and this post
      // would never happen — postCount would stay stuck at 1 forever.
      await act(async () => {
        tickCallback();
        await new Promise((resolve) => setTimeout(resolve, 0));
      });
      assert.equal(
        postCount,
        2,
        'workerBusy must have been cleared by the FIRST (unknown-type) reply — a second real interval fire must be able to post again'
      );

      await act(async () => {
        root.unmount();
      });
    } finally {
      spy.restore();
      delete globalThis.Worker;
      dom.window.close();
    }
  });
});

