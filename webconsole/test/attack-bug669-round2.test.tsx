// attack-bug669-round2.test.tsx — INDEPENDENT DESTRUCTIVE ROUND 2, BUG-669 REWORK.
//
// Round 1 (attack-bug669-round.test.tsx, unmodified) proved the ORIGINAL defect:
// a mid-chunked-tail-replay action was silently discarded by the replay's own
// `dispatch({type:'hydrate', ...})`. That estate is now REJECTED and reworked:
// `tailReplayActiveRef` + `tailReplayBufferRef` in store.tsx buffer every
// non-tick action dispatched during replay and drain it through the reducer
// onto `finalState` right before hydrate lands, producing `reconciledState`.
//
// This is a FRESH, independent round-2 attacker (not the author of either the
// original bug or the rework). Attacks below target the REWORK's own new
// machinery per the dispatch brief:
//   A. journal-vs-state fidelity: does the live boot's `reconciledState`
//      (chunked-tail + buffer-drain, WITH the extra nextSafeBuildingId/
//      sanitizeTreasury/consistency-check calls the rework adds) agree with a
//      plain sequential reducer fold over the identical action sequence — i.e.
//      "what would have happened if the player's action had simply landed
//      right after the tail, with no chunking/buffering machinery at all"?
//   B. precondition-conflict drain: a buffered placement whose target tile the
//      tail ALSO built on (legal pre-tail, illegal post-tail) must be dropped
//      cleanly (no overlapping placement, no duplicate building) rather than
//      corrupting the map.
//   C. 'reset' rejection must be genuinely user-visible (recordError, not a
//      silent return) and must NOT touch state or the buffer.
//   D. StrictMode double-invoke of the new `useLayoutEffect` (main.tsx wraps
//      the real app in React.StrictMode) must not double-drain the buffer or
//      double-apply the hydrate.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';

function installJsdom() {
  const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', {
    url: 'http://localhost/',
    pretendToBeVisual: true,
  });
  const { window } = dom;
  (globalThis as any).window = window;
  (globalThis as any).document = window.document;
  Object.defineProperty(globalThis, 'navigator', { value: window.navigator, configurable: true, writable: true });
  (globalThis as any).HTMLElement = window.HTMLElement;
  (globalThis as any).requestAnimationFrame = window.requestAnimationFrame.bind(window);
  (globalThis as any).cancelAnimationFrame = window.cancelAnimationFrame.bind(window);
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  return dom;
}

function buildGrowthTail(pairs: number) {
  const actions: Array<{ type: string; spec?: string; x?: number; y?: number }> = [];
  for (let i = 0; i < pairs; i++) {
    const x = 2 + (i % 100) * 3;
    const y = 2 + Math.floor(i / 100) * 3;
    actions.push({ type: 'place', spec: 'road', x, y: y + 1 });
    actions.push({ type: 'place', spec: 'res_hut', x, y });
  }
  return actions;
}

async function waitFor(predicate: () => boolean, timeoutMs: number, stepMs = 25): Promise<void> {
  const start = Date.now();
  while (!predicate()) {
    if (Date.now() - start > timeoutMs) throw new Error('waitFor timed out');
    await new Promise((r) => setTimeout(r, stepMs));
  }
}

const PROBE_X = 350;
const PROBE_Y = 200;

// ---------------------------------------------------------------------------
// A. FIDELITY: reconciledState must agree with a plain sequential reducer
//    fold over [tail..., bufferedAction] — no divergence introduced by the
//    chunking/buffering machinery itself.
// ---------------------------------------------------------------------------
test('BUG-669 round2 (A): reconciledState after buffer-drain matches a plain sequential reducer fold', async () => {
  const dom = installJsdom();
  try {
    const { initialState, reducer } = await import('../src/sim/engine.ts');
    const { emptyJournal, recordAction } = await import('../src/sim/journal.ts');
    const { createSavepoint, persistSavepoint } = await import('../src/sim/replay.ts');
    const { versionBadgeLabel } = await import('../src/sim/version.ts');

    const runningVersion = versionBadgeLabel();
    const start = { ...initialState(), unlockedAll: true, funds: 5_000_000_000 };

    const tailActions = buildGrowthTail(400);
    const bufferedAction = { type: 'place', spec: 'res_hut', x: PROBE_X, y: PROBE_Y };

    let journal = emptyJournal();
    for (const a of tailActions) journal = recordAction(journal, start.tick, a as never);
    const savepoint = createSavepoint(start, journal.entries, new Date(), runningVersion, null);
    assert.ok(persistSavepoint(dom.window.localStorage as unknown as Storage, savepoint), 'seed savepoint must persist');

    // Independent oracle: fold the SAME action sequence through the SAME pure
    // reducer, with NO chunking, NO buffering, NO nextSafeBuildingId/
    // sanitizeTreasury/consistency-check side calls — this is "what should
    // have happened" if BUG-669's whole mechanism did not exist.
    let expected = start;
    for (const a of tailActions) expected = reducer(expected, a as never);
    expected = reducer(expected, bufferedAction as never);

    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimProvider, useSim } = await import('../src/sim/store.tsx');

    let latestBuildingCount = -1;
    let latestState: any = null;
    let latestDispatch: ((a: unknown) => void) | null = null;
    function Probe() {
      const { state, dispatch } = useSim();
      latestBuildingCount = state.buildings.length;
      latestState = state;
      latestDispatch = dispatch as (a: unknown) => void;
      return null;
    }

    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);
    act(() => {
      root.render(React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }));
    });

    const expectedFinalCount = start.buildings.length + tailActions.length;
    assert.ok(latestBuildingCount < expectedFinalCount, 'precondition: must be mid-replay when the buffered action fires');

    act(() => {
      latestDispatch!(bufferedAction);
    });

    await waitFor(() => latestBuildingCount >= expectedFinalCount, 90_000);

    // Compare the load-bearing fields (buildings by (x,y,spec) set, funds,
    // population is derived so not compared here) rather than a byte-exact
    // stableStringify — the live path's extra nextSafeBuildingId call may
    // legitimately renumber `nextId`/building `id` fields without changing
    // what city actually exists, and that renumbering is NOT itself a bug.
    const actualTiles = new Set(latestState.buildings.map((b: any) => `${b.x},${b.y},${b.spec}`));
    const expectedTiles = new Set(expected.buildings.map((b: any) => `${b.x},${b.y},${b.spec}`));
    assert.deepEqual(
      [...actualTiles].sort(),
      [...expectedTiles].sort(),
      'BUG-669 round2 FINDING (A): the live chunked+buffered boot must produce the SAME set of buildings as a plain ' +
        'sequential reducer fold over the identical action sequence — a mismatch means the buffer-drain machinery ' +
        '(nextSafeBuildingId/sanitizeTreasury/consistency-recheck) is silently altering the city, not just its bookkeeping.',
    );
    assert.equal(
      latestState.funds,
      expected.funds,
      'BUG-669 round2 FINDING (A): funds must match the plain sequential-fold oracle exactly — no phantom cost/refund ' +
        'introduced by the drain path',
    );

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});

// ---------------------------------------------------------------------------
// B. PRECONDITION CONFLICT: the tail ALSO builds on the probe tile. The
//    player's interleaved placement is legal pre-tail (tile free) but illegal
//    post-tail (tile now occupied by the tail's own building). The drain must
//    reject cleanly — no overlapping/duplicate buildings.
// ---------------------------------------------------------------------------
test('BUG-669 round2 (B): a buffered placement whose tile the tail also claims is dropped cleanly, no overlap', async () => {
  const dom = installJsdom();
  try {
    const { initialState } = await import('../src/sim/engine.ts');
    const { emptyJournal, recordAction } = await import('../src/sim/journal.ts');
    const { createSavepoint, persistSavepoint } = await import('../src/sim/replay.ts');
    const { versionBadgeLabel } = await import('../src/sim/version.ts');

    const runningVersion = versionBadgeLabel();
    const start = { ...initialState(), unlockedAll: true, funds: 5_000_000_000 };

    // A long tail so there's real elapsed time to interleave, ending with a
    // placement AT THE PROBE TILE ITSELF — the conflict.
    const tailActions = buildGrowthTail(400);
    tailActions.push({ type: 'place', spec: 'res_hut', x: PROBE_X, y: PROBE_Y });

    let journal = emptyJournal();
    for (const a of tailActions) journal = recordAction(journal, start.tick, a as never);
    const savepoint = createSavepoint(start, journal.entries, new Date(), runningVersion, null);
    assert.ok(persistSavepoint(dom.window.localStorage as unknown as Storage, savepoint), 'seed savepoint must persist');

    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimProvider, useSim } = await import('../src/sim/store.tsx');

    let latestBuildingCount = -1;
    let latestBuildings: any[] = [];
    let latestDispatch: ((a: unknown) => void) | null = null;
    function Probe() {
      const { state, dispatch } = useSim();
      latestBuildingCount = state.buildings.length;
      latestBuildings = state.buildings;
      latestDispatch = dispatch as (a: unknown) => void;
      return null;
    }

    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);
    act(() => {
      root.render(React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }));
    });

    const expectedFinalCount = start.buildings.length + tailActions.length;
    assert.ok(latestBuildingCount < expectedFinalCount, 'precondition: must be mid-replay');
    assert.ok(
      !latestBuildings.some((b: any) => b.x === PROBE_X && b.y === PROBE_Y),
      'precondition: probe tile must be free pre-tail (this is what makes the immediate apply legal)',
    );

    // Legal on the CURRENT pre-tail state — applies immediately.
    act(() => {
      latestDispatch!({ type: 'place', spec: 'res_block', x: PROBE_X, y: PROBE_Y });
    });
    assert.ok(
      latestBuildings.some((b: any) => b.x === PROBE_X && b.y === PROBE_Y && b.spec === 'res_block'),
      'the interleaved placement must apply immediately to live state (unchanged UX contract)',
    );

    await waitFor(() => latestBuildingCount >= expectedFinalCount, 90_000);

    const atProbe = latestBuildings.filter((b: any) => b.x === PROBE_X && b.y === PROBE_Y);
    assert.equal(
      atProbe.length,
      1,
      'BUG-669 round2 FINDING (B): exactly ONE building may occupy the probe tile after convergence — ' +
        `found ${atProbe.length}: ${JSON.stringify(atProbe)}. More than one means the drain corrupted the map with an ` +
        'overlapping placement instead of rejecting the buffered action cleanly.',
    );
    assert.equal(
      atProbe[0].spec,
      'res_hut',
      "the tail's own claim on the tile must win (it lands in finalState BEFORE the buffered action is drained on top, " +
        'so the buffered res_shack placement sees an occupied tile and must no-op)',
    );

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});

// ---------------------------------------------------------------------------
// C. RESET REJECTION VISIBILITY: a 'reset' dispatched during replay must be
//    rejected with a user-visible recorded error, must not mutate state, and
//    must not enter the buffer.
// ---------------------------------------------------------------------------
test('BUG-669 round2 (C): reset during replay is rejected loudly, state and buffer untouched', async () => {
  const dom = installJsdom();
  try {
    const { initialState } = await import('../src/sim/engine.ts');
    const { emptyJournal, recordAction } = await import('../src/sim/journal.ts');
    const { createSavepoint, persistSavepoint } = await import('../src/sim/replay.ts');
    const { versionBadgeLabel } = await import('../src/sim/version.ts');
    const { recentErrors } = await import('../src/sim/backend.ts');

    const runningVersion = versionBadgeLabel();
    const start = { ...initialState(), unlockedAll: true, funds: 5_000_000_000 };
    const startBuildingCount = start.buildings.length;

    const tailActions = buildGrowthTail(400);
    let journal = emptyJournal();
    for (const a of tailActions) journal = recordAction(journal, start.tick, a as never);
    const savepoint = createSavepoint(start, journal.entries, new Date(), runningVersion, null);
    assert.ok(persistSavepoint(dom.window.localStorage as unknown as Storage, savepoint), 'seed savepoint must persist');

    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimProvider, useSim } = await import('../src/sim/store.tsx');

    const errorCountBefore = recentErrors().length;

    let latestBuildingCount = -1;
    let latestDispatch: ((a: unknown) => void) | null = null;
    function Probe() {
      const { state, dispatch } = useSim();
      latestBuildingCount = state.buildings.length;
      latestDispatch = dispatch as (a: unknown) => void;
      return null;
    }

    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);
    act(() => {
      root.render(React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }));
    });

    const expectedFinalCount = startBuildingCount + tailActions.length;
    assert.ok(latestBuildingCount < expectedFinalCount, 'precondition: must be mid-replay');
    const countBeforeReset = latestBuildingCount;

    act(() => {
      latestDispatch!({ type: 'reset' });
    });

    // State must be completely unaffected by the rejected reset.
    assert.equal(
      latestBuildingCount,
      countBeforeReset,
      "BUG-669 round2 FINDING (C): a 'reset' during replay must be REJECTED outright, not applied — building count " +
        'must not change (a real reset would wipe to a fresh city)',
    );

    const errorsAfter = recentErrors();
    assert.ok(
      errorsAfter.length > errorCountBefore,
      "BUG-669 round2 FINDING (C): a 'reset' rejected during replay must record a user-visible error via recordError " +
        '— found no new entry in recentErrors()',
    );
    const resetError = errorsAfter.find((e: any) => e.action === 'reset-during-replay');
    assert.ok(
      resetError,
      "BUG-669 round2 FINDING (C): the rejected reset's recordError call must be findable/taggable " +
        `(action: 'reset-during-replay') — recentErrors() = ${JSON.stringify(errorsAfter.slice(0, 3))}`,
    );

    // Let the replay converge normally afterward — the rejected reset must
    // not have wedged the buffer/replay machinery.
    await waitFor(() => latestBuildingCount >= expectedFinalCount, 90_000);

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});

// ---------------------------------------------------------------------------
// D. STRICTMODE DOUBLE-INVOKE: main.tsx wraps the real app in
//    React.StrictMode, which double-invokes effects (mount, cleanup, mount
//    again) in development. The new useLayoutEffect must not double-drain the
//    buffer or double-hydrate.
// ---------------------------------------------------------------------------
test('BUG-669 round2 (D): StrictMode double-invoke of the tail-replay layout effect does not double-apply', async () => {
  const dom = installJsdom();
  try {
    const { initialState } = await import('../src/sim/engine.ts');
    const { emptyJournal, recordAction } = await import('../src/sim/journal.ts');
    const { createSavepoint, persistSavepoint } = await import('../src/sim/replay.ts');
    const { versionBadgeLabel } = await import('../src/sim/version.ts');

    const runningVersion = versionBadgeLabel();
    const start = { ...initialState(), unlockedAll: true, funds: 5_000_000_000 };
    const startBuildingCount = start.buildings.length;

    const tailActions = buildGrowthTail(150);
    let journal = emptyJournal();
    for (const a of tailActions) journal = recordAction(journal, start.tick, a as never);
    const savepoint = createSavepoint(start, journal.entries, new Date(), runningVersion, null);
    assert.ok(persistSavepoint(dom.window.localStorage as unknown as Storage, savepoint), 'seed savepoint must persist');

    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimProvider, useSim } = await import('../src/sim/store.tsx');

    let latestBuildingCount = -1;
    let latestDispatch: ((a: unknown) => void) | null = null;
    let latestBuildings: any[] = [];
    function Probe() {
      const { state, dispatch } = useSim();
      latestBuildingCount = state.buildings.length;
      latestBuildings = state.buildings;
      latestDispatch = dispatch as (a: unknown) => void;
      return null;
    }

    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);
    act(() => {
      root.render(
        React.default.createElement(
          React.default.StrictMode,
          null,
          React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }),
        ),
      );
    });

    const tailOnlyFinalCount = startBuildingCount + tailActions.length;

    // Interleave a buffered action during the (possibly double-mounted) replay.
    let probeInterleaved = false;
    if (latestBuildingCount < tailOnlyFinalCount) {
      act(() => {
        latestDispatch!({ type: 'place', spec: 'res_hut', x: PROBE_X, y: PROBE_Y });
      });
      probeInterleaved = true;
    }
    const expectedFinalCount = tailOnlyFinalCount + (probeInterleaved ? 1 : 0);

    await waitFor(() => latestBuildingCount >= expectedFinalCount, 90_000);
    // Give any stray superseded-generation callback a chance to (wrongly) fire.
    await new Promise((r) => setTimeout(r, 200));

    assert.equal(
      latestBuildingCount,
      expectedFinalCount,
      'BUG-669 round2 FINDING (D): StrictMode double-invoking the layout effect must converge to EXACTLY the tail count ' +
        `(+ the buffered probe, if it was interleaved) — got ${latestBuildingCount}, expected ${expectedFinalCount}; a ` +
        'mismatch means the tail was replayed twice or the buffer was drained twice.',
    );
    const probeCount = latestBuildings.filter((b: any) => b.x === PROBE_X && b.y === PROBE_Y).length;
    assert.ok(
      probeCount <= 1,
      `BUG-669 round2 FINDING (D): the probe building must appear at most once — found ${probeCount} (double-drain of the buffer)`,
    );

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});
