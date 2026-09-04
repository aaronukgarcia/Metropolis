// attack-bug669-round.test.tsx — INDEPENDENT DESTRUCTIVE ROUND, BUG-669.
//
// BUG-669 deleted the LARGE_TAIL_REPLAY_THRESHOLD gate in store.tsx's boot
// initializer: EVERY boot with an existing savepoint now takes the instant
// pre-tail-boot + post-mount chunked-tail-replay path, not just the rare
// large-tail (>150 actions) case BUG-617 introduced the mechanism for.
//
// ATTACK: the chunked-replay effect's generator (`replayTailChunked`) is
// created ONCE from a snapshot of `stateRefForDispatch.current` taken at
// effect-fire time (store.tsx ~line 1166), and on completion the effect
// calls `dispatch({ type: 'hydrate', state: finalState })` — a full state
// REPLACEMENT — where `finalState` was computed purely from that snapshot
// plus the tail, with NO knowledge of anything dispatched to the live
// `state` in between. But `guardedDispatch` (the ONLY dispatch exposed to
// the app via useSim(), context value at the bottom of SimProvider) never
// checks `rebuildInProgress` / `pendingTailReplay` at all — a player action
// dispatched WHILE the chunked replay is running is applied immediately to
// the CURRENT (pre-tail) `state` via the ordinary reducer path, and is then
// silently DISCARDED the instant the replay's own hydrate lands, because
// hydrate is a full replacement, not a merge.
//
// This test proves the action loss directly: place a building at a
// coordinate the tail never touches, immediately after mount (while the
// chunked tail replay for an unrelated 800-action tail is still running),
// then wait for the tail replay to fully converge, and check whether the
// interleaved building survived.

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

// Mirrors the fixture idiom shared by bug617/bug669's own tests: roads/huts
// packed into x in [2,299], y in [2,~14] — the interleaved probe action below
// (x=350, y=200) is deliberately far outside this footprint so any building
// found there can ONLY be the interleaved action, never tail growth.
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

test('ATTACK BUG-669: an action dispatched mid chunked-tail-replay is silently reverted by the replay hydrate', async () => {
  const dom = installJsdom();
  try {
    const { initialState } = await import('../src/sim/engine.ts');
    const { emptyJournal, recordAction } = await import('../src/sim/journal.ts');
    const { createSavepoint, persistSavepoint } = await import('../src/sim/replay.ts');
    const { versionBadgeLabel } = await import('../src/sim/version.ts');

    const runningVersion = versionBadgeLabel();
    const start = { ...initialState(), unlockedAll: true, funds: 5_000_000_000 };
    const startBuildingCount = start.buildings.length;

    // 400 pairs = 800 actions — well past LARGE_TAIL_REPLAY_THRESHOLD, so the
    // replay genuinely takes multiple chunks/rAF turns (real elapsed time),
    // giving a real (not theoretical) window to interleave a dispatch.
    const tailActions = buildGrowthTail(400);
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
    let sawProbeBeforeConvergence = false;
    function Probe() {
      const { state, dispatch } = useSim();
      latestBuildingCount = state.buildings.length;
      latestBuildings = state.buildings;
      latestDispatch = dispatch as (a: unknown) => void;
      if (
        state.buildings.length < startBuildingCount + tailActions.length &&
        state.buildings.some((b: any) => b.x === PROBE_X && b.y === PROBE_Y)
      ) {
        sawProbeBeforeConvergence = true;
      }
      return null;
    }

    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);

    act(() => {
      root.render(React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }));
    });

    const expectedFinalCount = startBuildingCount + tailActions.length;

    // Confirm we really are mid-replay (pre-tail state, well short of the
    // fully-replayed count) before firing the interleaved action — this is
    // the assertion that proves the attack window is real, not assumed.
    assert.ok(
      latestBuildingCount < expectedFinalCount,
      'precondition: must be mid-replay (short of the final count) when the interleaved action fires',
    );
    assert.ok(typeof latestDispatch === 'function', 'dispatch must be available from useSim() immediately after mount');
    assert.ok(
      !latestBuildings.some((b: any) => b.x === PROBE_X && b.y === PROBE_Y),
      'the probe coordinate must be free before the interleaved dispatch',
    );

    // Fire the interleaved player action while the chunked replay is still running.
    act(() => {
      latestDispatch!({ type: 'place', spec: 'res_hut', x: PROBE_X, y: PROBE_Y });
    });

    assert.ok(
      latestBuildings.some((b: any) => b.x === PROBE_X && b.y === PROBE_Y),
      'the interleaved action must be applied immediately (guardedDispatch has no rebuildInProgress gate) — ' +
        'if this fails, guardedDispatch has started buffering/rejecting actions during replay instead of dropping them later',
    );

    // Let the chunked tail replay run to completion (real rAF turns).
    await waitFor(() => latestBuildingCount >= expectedFinalCount, 90_000);

    const probeSurvivedConvergence = latestBuildings.some((b: any) => b.x === PROBE_X && b.y === PROBE_Y);

    await act(async () => {
      root.unmount();
    });

    assert.ok(
      sawProbeBeforeConvergence,
      'internal check: the probe building must have been observed live before the replay converged',
    );
    assert.ok(
      probeSurvivedConvergence,
      'BUG-669 FINDING: a player action dispatched during the post-mount chunked tail replay was applied ' +
        'immediately, but was then silently DISCARDED when the replay\'s own ' +
        "dispatch({type:'hydrate', state: finalState}) landed — finalState was computed from a stale " +
        'pre-interleave snapshot taken when the effect started. The player sees their action vanish with no error, ' +
        'and only recovers it (if at all) via the journal on a LATER reload.',
    );
  } finally {
    dom.window.close();
  }
});
