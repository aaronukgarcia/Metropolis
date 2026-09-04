// bug669-cost-aware-boot.test.tsx — BUG-669 (P1, 2026-09-04) RED-PROOF.
//
// BUG-617's fix gated the instant-pre-tail-boot / chunked-replay path on
// `journalTail.length > LARGE_TAIL_REPLAY_THRESHOLD` (150 actions). That is
// an ACTION-COUNT-only cut line — it ignores the OTHER factor in replay
// cost, building count. Aaron's real 49,174-building save had a perfectly
// healthy 106-action tail (well under 150), so it took the OLD synchronous
// `restoreFromSavepoint` branch and blocked first paint for ~31 SECONDS.
//
// The fix (this session) deletes the threshold as a live gate in store.tsx:
// EVERY boot with an existing savepoint now takes the instant pre-tail boot
// + chunked replay path, regardless of tail length.
//
// This test proves it directly at the store.tsx boot layer with a tail of
// only 100 actions — comfortably BELOW the old 150-action cut line, so
// pre-fix code takes the synchronous branch and this test goes RED (first
// render already shows the fully-replayed count); post-fix code always
// chunks (TAIL_ACTIONS_PER_CHUNK=50 caps a single synchronous chunk), so
// this test goes GREEN (first render is provably short of the final count).

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

test('BUG-669: a HEALTHY tail well below the old action-count threshold still boots instant + chunked, never synchronous', async () => {
  const dom = installJsdom();
  try {
    const { initialState } = await import('../src/sim/engine.ts');
    const { emptyJournal, recordAction } = await import('../src/sim/journal.ts');
    const { createSavepoint, persistSavepoint } = await import('../src/sim/replay.ts');
    const { versionBadgeLabel } = await import('../src/sim/version.ts');

    const runningVersion = versionBadgeLabel();
    const start = { ...initialState(), unlockedAll: true, funds: 5_000_000_000 };
    const startBuildingCount = start.buildings.length;

    // 50 pairs = 100 actions — well under BUG-617's old 150-action cut line,
    // and above TAIL_ACTIONS_PER_CHUNK=50, so a single synchronous chunk can
    // never cover it under the new always-chunked path.
    const tailActions = buildGrowthTail(50);
    assert.ok(tailActions.length < 150, 'this tail must be a HEALTHY size, below the old threshold — that is the whole point of BUG-669');

    let journal = emptyJournal();
    for (const a of tailActions) journal = recordAction(journal, start.tick, a as never);

    const savepoint = createSavepoint(start, journal.entries, new Date(), runningVersion, null);
    assert.ok(persistSavepoint(dom.window.localStorage as unknown as Storage, savepoint), 'seed savepoint must persist');

    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimProvider, useSim } = await import('../src/sim/store.tsx');

    let latestBuildingCount = -1;
    function Probe() {
      const { state } = useSim();
      latestBuildingCount = state.buildings.length;
      return null;
    }

    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);

    act(() => {
      root.render(React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }));
    });

    const expectedFinalCount = startBuildingCount + tailActions.length;

    // Always unmount (clears the autosave setInterval / rAF chains store.tsx
    // starts) BEFORE asserting — an assertion throw must never leave real
    // timers dangling and hang the test process.
    await act(async () => {
      root.unmount();
    });

    assert.ok(
      latestBuildingCount < expectedFinalCount,
      `BUG-669 REGRESSION: a ${tailActions.length}-action tail (below the old 150-action threshold) was replayed ` +
        `SYNCHRONOUSLY inside the boot initializer (first render already shows ${latestBuildingCount} of ` +
        `${expectedFinalCount} buildings) — this is exactly the cost-blind branch that wedged Aaron's 49k-building save`,
    );
    assert.ok(latestBuildingCount >= startBuildingCount, 'first render must show at least the pre-tail city');
  } finally {
    dom.window.close();
  }
});
