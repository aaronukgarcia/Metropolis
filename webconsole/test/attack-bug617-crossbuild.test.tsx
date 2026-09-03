// attack-bug617-crossbuild.test.tsx — INDEPENDENT DESTRUCTIVE ROUND, BUG-617.
//
// r1 REJECT — "THE SHIPPING PARADOX". store.tsx's large-tail deferral was
// gated `if (most && !crossBuild && ...)`. `crossBuild` is a bare string
// inequality of the savepoint's buildVersion against the running badge
// (needsRebuild, genesisReplay.ts), so it is TRUE on the first boot after ANY
// new bundle ships — including the very bundle carrying this fix. Aaron's
// wedged 1.4M city has a savepoint stamped with the PRE-fix build, so his
// first load took the cross-build branch straight into the UNCHANGED
// `restoreFromSavepoint`, whose tail loop is exactly the synchronous,
// unchunked, first-render-blocking loop this bug exists to eliminate. Measured
// in r1: first render already showed 4255 of 4255 buildings — the whole
// 2,400-action tail ground through before first paint. The rescue would only
// have become reachable on the SECOND boot, the one that no longer needed it.
//
// r2 — the `!crossBuild` guard is gone: ANY large tail gets the instant
// pre-tail boot + chunked replay, and a cross-build savepoint additionally
// remembers `crossBuildAfter` so the Rebuild-from-genesis prompt is offered
// AFTER the chunked replay lands, instead of being skipped or lost.
//
// This test pins the whole r2 cross-build sequence end to end: instant boot ->
// visible load progress -> convergence to the fully replayed city -> and only
// THEN the "New build detected" prompt. Not before (that would mean the
// instant boot was skipped), and not never (that would mean the player's
// pending rebuild decision was silently dropped).

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

test('ATTACK BUG-617 r2: a CROSS-BUILD savepoint with a large tail boots instantly, replays chunked, THEN prompts to rebuild', async () => {
  const dom = installJsdom();
  try {
    const { initialState } = await import('../src/sim/engine.ts');
    const { emptyJournal, recordAction } = await import('../src/sim/journal.ts');
    const { createSavepoint, persistSavepoint, LARGE_TAIL_REPLAY_THRESHOLD } = await import('../src/sim/replay.ts');
    const { versionBadgeLabel } = await import('../src/sim/version.ts');

    // A savepoint written by the PREVIOUS build — i.e. every savepoint in
    // existence at the instant this fix is deployed, Aaron's included.
    const staleVersion = 'v0.0.0.1-previous';
    assert.notEqual(staleVersion, versionBadgeLabel(), 'the fixture must be genuinely cross-build');

    const start = { ...initialState(), unlockedAll: true, funds: 5_000_000_000 };
    const startBuildingCount = start.buildings.length;
    const tailActions = buildGrowthTail(1200); // 2,400 actions — the pathological shape
    assert.ok(tailActions.length > LARGE_TAIL_REPLAY_THRESHOLD);

    let journal = emptyJournal();
    for (const a of tailActions) journal = recordAction(journal, start.tick, a as never);

    assert.ok(
      persistSavepoint(
        dom.window.localStorage as unknown as Storage,
        createSavepoint(start, journal.entries, new Date(), staleVersion, null),
      ),
      'seed savepoint must persist',
    );

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

    // Synchronous act() overload deliberately (see bug617-boot-wiring's own
    // determinism note): flushes render + effects, including the effect's
    // first synchronous chunk, but returns before the event loop can run the
    // scheduled rAF for chunk #2 — so the assertion below is provably at most
    // one chunk deep rather than racing an indeterminate number of cycles.
    act(() => {
      root.render(React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }));
    });

    const expectedFinalCount = startBuildingCount + tailActions.length;

    // CLAIM 1 (the r1 finding, inverted): the cross-build boot must NOT have
    // ground the whole tail through synchronously. 2,400 actions against a
    // TAIL_ACTIONS_PER_CHUNK=50 ceiling makes this hold by construction.
    assert.ok(
      latestBuildingCount < expectedFinalCount,
      `CROSS-BUILD boot replayed the entire ${tailActions.length}-action tail synchronously ` +
        `(first render already shows ${latestBuildingCount} of ${expectedFinalCount} buildings) — ` +
        `the r1 "shipping paradox" has returned`,
    );
    assert.ok(latestBuildingCount >= startBuildingCount, 'first render must show at least the pre-tail city');

    // CLAIM 2: the LOAD overlay is up — not the rebuild prompt. The rebuild
    // decision must not be offered while the tail is still replaying.
    assert.ok(
      container.textContent?.includes('Loading your city'),
      'the chunked-load progress overlay must be visible immediately after a large-tail cross-build boot',
    );
    assert.ok(
      !container.textContent?.includes('New build detected'),
      'the rebuild prompt must NOT be shown before the chunked tail replay has landed',
    );

    // CLAIM 3: convergence to the fully replayed old-engine city.
    // Widened 30s->90s / 10s->60s (independent round r2 stability finding,
    // 2026-09-03): a "does it eventually converge" check, not a per-chunk
    // timing gate — measured to occasionally exceed the old bounds when this
    // file runs alongside its bug617/attack siblings in one contended
    // node:test process. See tools/test/scoped.mjs's SLOW_TEST_CAPS_SEC entry
    // for this file. No loss of catching power: a genuinely hung/broken
    // chain still never satisfies the predicate.
    await waitFor(() => latestBuildingCount === expectedFinalCount, 90_000);
    assert.equal(latestBuildingCount, expectedFinalCount, 'state must converge to the fully replayed city');

    // CLAIM 4: and only THEN the cross-build prompt fires. Never dropped.
    await waitFor(() => !!container.textContent?.includes('New build detected'), 60_000);
    assert.ok(
      container.textContent?.includes('New build detected'),
      'after the chunked tail replay lands, the deferred cross-build rebuild prompt must be offered',
    );
    assert.ok(
      !container.textContent?.includes('Loading your city'),
      'the load overlay must be dismissed once the chunked replay completes',
    );

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});

// ---------------------------------------------------------------------------
// r2 follow-on probe: the self-heal savepoint written at the END of a
// CROSS-BUILD chunked load is stamped with the RUNNING build version, while
// the prompt asking the player to rebuild is still on screen and unresolved.
// BUG-468 deliberately re-stamps only on RESOLUTION (onKeep), precisely so an
// unresolved prompt recurs on the next boot instead of being silently lost.
// This test asks whether an unresolved cross-build decision survives a reload.
//
// VERDICT: originally it did NOT — verified failing against r2, the healed
// savepoint carried buildVersion v0.3.0.386 (the running badge) wrapped
// around the pre-rebuild OLD-ENGINE state, so a reload at that moment
// computed needsRebuild=false and the prompt never returned. Filed as
// BUG-635 (P2, not data loss: the city is intact, the window is a reload
// during an unresolved prompt, and a rebuild can still be forced later).
// BUG-635's remedy landed (store.tsx's chunked-tail self-heal now stamps the
// healed savepoint with `crossBuildAfter.savedVersion` instead of `running`
// while the cross-build decision is pending, re-stamping to `running` only
// on resolution via onKeep/onResume) — un-skipped as the regression gate.
test('ATTACK BUG-617 r2 / BUG-635: does an UNRESOLVED cross-build decision survive a reload after the self-heal write?', async () => {
  const dom = installJsdom();
  try {
    const { initialState } = await import('../src/sim/engine.ts');
    const { emptyJournal, recordAction } = await import('../src/sim/journal.ts');
    const { createSavepoint, persistSavepoint, readAllSavepoints, mostRecentSavepoint } = await import('../src/sim/replay.ts');
    const { versionBadgeLabel } = await import('../src/sim/version.ts');
    const { needsRebuild } = await import('../src/sim/genesisReplay.ts');

    const staleVersion = 'v0.0.0.1-previous';
    const start = { ...initialState(), unlockedAll: true, funds: 5_000_000_000 };
    const tailActions = buildGrowthTail(400); // 800 actions
    let journal = emptyJournal();
    for (const a of tailActions) journal = recordAction(journal, start.tick, a as never);
    persistSavepoint(
      dom.window.localStorage as unknown as Storage,
      createSavepoint(start, journal.entries, new Date(), staleVersion, null),
    );

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

    const expectedFinalCount = start.buildings.length + tailActions.length;
    await waitFor(() => latestBuildingCount === expectedFinalCount, 30_000);
    await waitFor(() => !!container.textContent?.includes('New build detected'), 10_000);

    // The prompt is UP and the player has decided NOTHING. What is on disk?
    const healed = mostRecentSavepoint(readAllSavepoints(dom.window.localStorage as unknown as Storage));
    assert.ok(healed, 'a self-healed savepoint must exist');
    assert.ok(
      needsRebuild(healed!.buildVersion, versionBadgeLabel()),
      `the self-heal write stamped the OLD-ENGINE state with the RUNNING build version ` +
        `(${healed!.buildVersion}), so a reload while the prompt is still unresolved boots with ` +
        `needsRebuild=false and the "New build detected" prompt NEVER returns — the player ` +
        `silently keeps an old-engine city believing it is current. BUG-468 re-stamps only on ` +
        `RESOLUTION (onKeep) for exactly this reason.`,
    );

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});
