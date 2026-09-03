// attack-bug617-lifecycle.test.tsx — INDEPENDENT DESTRUCTIVE ROUND, BUG-617 r2.
//
// Covers the two r1 findings whose fixes live in store.tsx's chunked-tail
// effect rather than in replay.ts:
//
//   F3 (GR#1) — `persistSavepoint` NEVER throws on quota; it returns `false`.
//     r1's self-heal wrapped the call in a bare try/catch and discarded the
//     boolean, so a quota failure (the EXACT BUG-607 condition that created
//     BUG-617) was swallowed with zero signal and the player re-paid the
//     large-tail load on every boot with no idea why. r2 checks the boolean
//     and routes it through recordError + the `⚠ save` indicator.
//
//   F4 — r1's effect abandoned its generator on cleanup with a bare
//     `cancelled = true`: no gen.return(), no generation guard, no
//     rebuildInProgress reset. React.StrictMode (main.tsx) double-invokes
//     effects, so every dev boot abandoned one generator, and a genuine
//     unmount left the module-scoped rebuildInProgress flag stuck ON — which
//     is the flag the tick driver checks (`if (rebuildInProgress) return;`),
//     i.e. a stuck flag silently kills the engine.

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

/** Seed a same-build savepoint with a large tail, returning the fixture facts. */
async function seedLargeTail(dom: JSDOM, pairs = 1200) {
  const { initialState } = await import('../src/sim/engine.ts');
  const { emptyJournal, recordAction } = await import('../src/sim/journal.ts');
  const { createSavepoint, persistSavepoint, LARGE_TAIL_REPLAY_THRESHOLD } = await import('../src/sim/replay.ts');
  const { versionBadgeLabel } = await import('../src/sim/version.ts');

  const start = { ...initialState(), unlockedAll: true, funds: 5_000_000_000 };
  const tailActions = buildGrowthTail(pairs);
  assert.ok(tailActions.length > LARGE_TAIL_REPLAY_THRESHOLD);
  let journal = emptyJournal();
  for (const a of tailActions) journal = recordAction(journal, start.tick, a as never);
  const sp = createSavepoint(start, journal.entries, new Date(), versionBadgeLabel(), null);
  assert.ok(persistSavepoint(dom.window.localStorage as unknown as Storage, sp), 'seed savepoint must persist');
  return {
    startBuildingCount: start.buildings.length,
    tailLength: tailActions.length,
    expectedFinalCount: start.buildings.length + tailActions.length,
  };
}

test('ATTACK BUG-617 r2 / F3: a quota-failing self-heal write surfaces a VISIBLE error and corrupts nothing', async () => {
  const dom = installJsdom();
  try {
    const fixture = await seedLargeTail(dom);
    const { readAllSavepoints, mostRecentSavepoint } = await import('../src/sim/replay.ts');
    const { recentErrors } = await import('../src/sim/backend.ts');

    // Snapshot what is genuinely on disk before the run, so "no corruption"
    // is checked against real bytes rather than against a belief.
    const realStorage = dom.window.localStorage;
    const diskBefore = new Map<string, string>();
    for (let i = 0; i < realStorage.length; i++) {
      const k = realStorage.key(i)!;
      diskBefore.set(k, realStorage.getItem(k)!);
    }
    assert.ok(diskBefore.size > 0, 'the fixture must actually be on disk');

    // Now make EVERY write fail the way an over-quota browser does — the
    // BUG-607 condition that produced this bug in the first place. Reads and
    // removals still work, so the restore path itself is unaffected.
    let writesAttempted = 0;
    const quotaStorage = {
      get length() {
        return realStorage.length;
      },
      key: (i: number) => realStorage.key(i),
      getItem: (k: string) => realStorage.getItem(k),
      removeItem: (_k: string) => {
        /* deliberately a no-op: a removal must not be able to destroy the
           original savepoint while writes are failing */
      },
      setItem: (_k: string, _v: string) => {
        writesAttempted++;
        const e: any = new Error('QuotaExceededError: persistent storage is full');
        e.name = 'QuotaExceededError';
        e.code = 22;
        throw e;
      },
      clear: () => {
        /* no-op */
      },
    };
    Object.defineProperty(dom.window, 'localStorage', { value: quotaStorage, configurable: true });

    const errorsBefore = recentErrors().length;

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

    // The load itself must still succeed — a failed self-heal is not a failed load.
    // Widened 30s->90s / 10s->60s (independent round r2 stability finding,
    // 2026-09-03): "does it eventually converge/record/render" checks, not
    // per-chunk timing gates — measured to occasionally exceed the old bounds
    // when this file runs alongside its bug617/attack siblings in one
    // contended node:test process. See tools/test/scoped.mjs's
    // SLOW_TEST_CAPS_SEC entry for this file. No loss of catching power: a
    // genuinely broken/silent path still never satisfies the predicate.
    await waitFor(() => latestBuildingCount === fixture.expectedFinalCount, 90_000);
    assert.equal(latestBuildingCount, fixture.expectedFinalCount, 'the chunked load must still converge despite a full disk');

    // GR#1: the failure must be VISIBLE, not swallowed.
    assert.ok(writesAttempted > 0, 'the self-heal must actually have attempted a write');
    await waitFor(() => recentErrors().length > errorsBefore, 60_000);
    const allErrors = recentErrors();
    assert.ok(
      allErrors.some((e) => /self-heal save failed/i.test(e.msg ?? '')),
      `a quota-failed self-heal must record a registry error (GR#1). Errors seen: ` +
        JSON.stringify(allErrors.map((e) => e.msg)),
    );
    await waitFor(() => !!container.textContent?.includes('⚠ save'), 60_000);
    assert.ok(container.textContent?.includes('⚠ save'), 'the visible save-failure indicator must be shown');

    // NO CORRUPTION: the original savepoint + journal on disk must be exactly
    // as they were, so the next boot can retry from the same intact inputs.
    for (const [k, v] of diskBefore) {
      assert.equal(realStorage.getItem(k), v, `on-disk key ${k} must be byte-unchanged after a failed self-heal`);
    }
    Object.defineProperty(dom.window, 'localStorage', { value: realStorage, configurable: true });
    const survivor = mostRecentSavepoint(readAllSavepoints(realStorage as unknown as Storage));
    assert.ok(survivor, 'the original savepoint must still be readable');
    assert.equal(survivor!.journalTail.length, fixture.tailLength, 'the original tail must be intact for the next boot');

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});

test('ATTACK BUG-617 r2 / F4: StrictMode double-mount replays exactly once and converges correctly', async () => {
  const dom = installJsdom();
  try {
    const fixture = await seedLargeTail(dom, 400); // 800 actions — enough to chunk, quick to converge
    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimProvider, useSim } = await import('../src/sim/store.tsx');
    const genesisReplay = await import('../src/sim/genesisReplay.ts');

    let latestBuildingCount = -1;
    function Probe() {
      const { state } = useSim();
      latestBuildingCount = state.buildings.length;
      return null;
    }
    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);

    // StrictMode is what main.tsx actually renders under — mount/cleanup/mount,
    // i.e. the effect fires twice and the FIRST generator is abandoned.
    act(() => {
      root.render(
        React.default.createElement(
          React.default.StrictMode,
          null,
          React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }),
        ),
      );
    });

    // Widened 30s->90s / 10s->60s — see this file's earlier F3 test comment
    // (independent round r2 stability finding, 2026-09-03).
    await waitFor(() => latestBuildingCount === fixture.expectedFinalCount, 90_000);
    assert.equal(
      latestBuildingCount,
      fixture.expectedFinalCount,
      'under StrictMode the tail must be replayed exactly once onto the pre-tail state — ' +
        'a double-applied tail would overshoot, a dead chain would undershoot',
    );

    // The tick driver's global suspend flag must be released once the load is done,
    // otherwise `if (rebuildInProgress) return;` silently kills the engine forever.
    await waitFor(() => genesisReplay.rebuildInProgress === false, 60_000);
    assert.equal(genesisReplay.rebuildInProgress, false, 'rebuildInProgress must be cleared after the chunked load completes');

    await act(async () => {
      root.unmount();
    });
    assert.equal(genesisReplay.rebuildInProgress, false, 'rebuildInProgress must stay cleared after unmount');
  } finally {
    dom.window.close();
  }
});

test('ATTACK BUG-617 r2 / F4: unmounting MID-replay leaves no orphan chain and clears rebuildInProgress', async () => {
  const dom = installJsdom();
  try {
    const fixture = await seedLargeTail(dom, 1200); // 2,400 actions — long enough to unmount mid-flight
    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimProvider, useSim } = await import('../src/sim/store.tsx');
    const genesisReplay = await import('../src/sim/genesisReplay.ts');

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

    // Provably mid-flight: one synchronous chunk at most (50 of 2,400).
    assert.ok(latestBuildingCount < fixture.expectedFinalCount, 'must still be mid-replay before unmounting');
    const countAtUnmount = latestBuildingCount;

    await act(async () => {
      root.unmount();
    });

    // The suspend flag must be released — a stuck `true` means the tick driver
    // (`if (rebuildInProgress) return;`) never runs another tick.
    assert.equal(
      genesisReplay.rebuildInProgress,
      false,
      'unmounting mid-replay must clear rebuildInProgress — a stuck flag silently kills the engine',
    );

    // No orphan chain: an abandoned generator must not keep advancing state
    // behind the unmounted tree.
    await new Promise((r) => setTimeout(r, 400));
    assert.equal(latestBuildingCount, countAtUnmount, 'no orphan chunked chain may keep replaying after unmount');

    // And the persisted inputs are untouched, so the next boot starts clean.
    const { readAllSavepoints, mostRecentSavepoint } = await import('../src/sim/replay.ts');
    const survivor = mostRecentSavepoint(readAllSavepoints(dom.window.localStorage as unknown as Storage));
    assert.ok(survivor, 'the savepoint must survive an interrupted chunked replay');
    assert.equal(
      survivor!.journalTail.length,
      fixture.tailLength,
      'an interrupted chunked replay must not truncate or rewrite the persisted tail',
    );
  } finally {
    dom.window.close();
  }
});
