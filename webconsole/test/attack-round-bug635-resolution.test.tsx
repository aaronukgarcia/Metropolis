// attack-round-bug635-resolution.test.tsx — INDEPENDENT DESTRUCTIVE ROUND,
// BUG-635 (GR#23 attacker != author).
//
// The author's regression test (attack-bug617-crossbuild.test.tsx) proves the
// UNRESOLVED window survives a reload. This attack exercises the RESOLUTION
// paths the fix's own comment claims: "onKeep/onResume re-stamp to `running`
// once the player actually resolves the prompt (either choice)." Does
// clicking "Keep my city" (onKeep) actually flip the healed savepoint's
// buildVersion back to running? Does onResume?

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

async function bootToCrossBuildPrompt(dom: JSDOM) {
  const { initialState } = await import('../src/sim/engine.ts');
  const { emptyJournal, recordAction } = await import('../src/sim/journal.ts');
  const { createSavepoint, persistSavepoint } = await import('../src/sim/replay.ts');

  const staleVersion = 'v0.0.0.1-previous';
  const start = { ...initialState(), unlockedAll: true, funds: 5_000_000_000 };
  const tailActions = buildGrowthTail(400); // 800 actions — large-tail threshold
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

  return { root, container, act, staleVersion, expectedFinalCount, getCount: () => latestBuildingCount };
}

function clickButtonByText(container: Element, dom: JSDOM, text: string) {
  const buttons = Array.from(container.querySelectorAll('button'));
  const btn = buttons.find((b) => b.textContent?.includes(text));
  if (!btn) {
    throw new Error(
      `no button matching "${text}" found; available: ${buttons.map((b) => JSON.stringify(b.textContent)).join(', ')}`,
    );
  }
  const evt = new dom.window.MouseEvent('click', { bubbles: true, cancelable: true });
  btn.dispatchEvent(evt);
}

test('ATTACK BUG-635 (independent round): onKeep re-stamps the healed cross-build savepoint to `running` exactly once', async () => {
  const dom = installJsdom();
  try {
    const { readAllSavepoints } = await import('../src/sim/replay.ts');
    const { needsRebuild } = await import('../src/sim/genesisReplay.ts');
    const { versionBadgeLabel } = await import('../src/sim/version.ts');

    const { root, container, act, staleVersion, getCount } = await bootToCrossBuildPrompt(dom);

    // Pre-resolution: the healed savepoint must still carry the OLD version
    // (BUG-635's fix) — the same assertion the author's regression test makes.
    // The healed write lands in a DIFFERENT slot than the original seed
    // savepoint (target-slot choice prefers an EMPTY slot — replay.ts), so
    // TWO slots are occupied pre-resolution: the original pre-tail seed AND
    // the post-tail healed one. This is itself an attack surface (a): boot
    // must pick the NEWER (healed) one by snapshotTick/savedAt, not clobber
    // it — verified separately below by requiring ALL slots be clean after
    // resolution, not just "the" slot.
    const beforeSaves = readAllSavepoints(dom.window.localStorage as unknown as Storage);
    assert.ok(beforeSaves.length >= 1, 'at least one savepoint slot should be occupied before resolution');
    const healedBefore = beforeSaves.reduce((best, sp) => (sp.snapshotTick > best.snapshotTick ? sp : best));
    assert.equal(healedBefore.buildVersion, staleVersion, 'pre-resolution healed (newest) savepoint must carry the OLD version');

    // Resolve via "Keep my city" / "Keep old snapshot".
    await act(async () => {
      clickButtonByText(container, dom, 'Keep');
    });

    const afterSaves = readAllSavepoints(dom.window.localStorage as unknown as Storage);
    assert.equal(afterSaves.length, beforeSaves.length, 'onKeep must not create/duplicate a savepoint slot');
    const anyStaleAfterKeep = afterSaves.some((sp) => needsRebuild(sp.buildVersion, versionBadgeLabel()));
    assert.equal(
      anyStaleAfterKeep,
      false,
      `after onKeep, EVERY persisted savepoint must be re-stamped to the running build ` +
        `(got versions: ${afterSaves.map((s) => s.buildVersion).join(', ')}) so a subsequent reload does not re-prompt`,
    );
    // The city DATA itself (buildings/funds/etc, not just the version stamp)
    // must be byte-identical across the restamp — restampSavepointsBuildVersion
    // only mutates `buildVersion`, nothing else in the snapshot.
    const healedBeforeOther = { ...healedBefore.snapshot, } as Record<string, unknown>;
    const healedAfter = afterSaves.reduce((best, sp) => (sp.snapshotTick > best.snapshotTick ? sp : best));
    assert.deepEqual(
      healedAfter.snapshot,
      healedBeforeOther,
      'onKeep must not alter city data, only the version stamp',
    );
    void getCount;

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});

test('ATTACK BUG-635 (independent round): onResume re-stamps the healed cross-build savepoint to `running`', async () => {
  const dom = installJsdom();
  try {
    const { readAllSavepoints } = await import('../src/sim/replay.ts');
    const { needsRebuild } = await import('../src/sim/genesisReplay.ts');
    const { versionBadgeLabel } = await import('../src/sim/version.ts');

    const { root, container, act, staleVersion } = await bootToCrossBuildPrompt(dom);

    const beforeSaves = readAllSavepoints(dom.window.localStorage as unknown as Storage);
    assert.equal(beforeSaves[0].buildVersion, staleVersion, 'pre-resolution healed savepoint must carry the OLD version');

    // onResume triggers window.location.reload() — jsdom's reload is a no-op
    // stub by default (navigation is not implemented), so the re-stamp write
    // that happens BEFORE the reload call is still directly observable.
    await act(async () => {
      clickButtonByText(container, dom, 'Rebuild on');
    });
    // The rebuild path needs to run to completion (genesis replay) before
    // reaching the 'report' phase where onResume is offered.
    await waitFor(() => !!container.textContent?.match(/Resume on/), 30_000);
    await act(async () => {
      clickButtonByText(container, dom, 'Resume on');
    });

    const afterSaves = readAllSavepoints(dom.window.localStorage as unknown as Storage);
    assert.ok(afterSaves.length >= 1, 'a savepoint must exist after resume');
    const anyStale = afterSaves.some((sp) => needsRebuild(sp.buildVersion, versionBadgeLabel()));
    assert.equal(anyStale, false, 'after onResume, no persisted savepoint may still carry a stale buildVersion');

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});
