// attack-p0-lineage-round2.test.tsx — INDEPENDENT DESTRUCTIVE ROUND 2, mount
// level. Attacks F1's reorder consequence at a REAL boot: after the pointer
// has been moved but the persist failed, `metropolis.currentLineage` names a
// lineage that owns nothing. Boot must come up usable, must not destroy the
// de-referenced lineage's data, and must not wedge.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';

async function loadFakeIndexedDBFactory(): Promise<(backing?: Map<string, Map<string, string>>) => any> {
  const specifier = './helpers/fakeIndexedDB.mjs';
  const mod: any = await import(specifier);
  return mod.createFakeIndexedDBFactory;
}

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
  (globalThis as any).Blob = window.Blob;
  (globalThis as any).requestAnimationFrame = window.requestAnimationFrame.bind(window);
  (globalThis as any).cancelAnimationFrame = window.cancelAnimationFrame.bind(window);
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  window.HTMLAnchorElement.prototype.click = function () {};
  return dom;
}

function pin(fn: () => void) {
  try {
    fn();
  } catch (e) {
    console.error('FINDING >>> ' + ((e as Error).message || String(e)));
    throw e;
  }
}

async function waitFor(predicate: () => boolean, timeoutMs: number, stepMs = 25): Promise<void> {
  const start = Date.now();
  while (!predicate()) {
    if (Date.now() - start > timeoutMs) throw new Error('waitFor timed out');
    await new Promise((r) => setTimeout(r, stepMs));
  }
}

const openRoots: Array<{ root: any; act: any }> = [];
async function closeAllRoots() {
  while (openRoots.length) {
    const { root, act } = openRoots.pop()!;
    try {
      await act(async () => {
        root.unmount();
      });
    } catch {
      /* already gone */
    }
  }
}

async function mountProvider(dom: JSDOM) {
  const React = await import('react');
  const { createRoot } = await import('react-dom/client');
  const { act } = await import('react-dom/test-utils');
  const { SimProvider, useSim } = await import('../src/sim/store.tsx');
  const seen: { ctx: any; state: any } = { ctx: null, state: null };
  function Probe() {
    const ctx = useSim();
    seen.ctx = ctx;
    seen.state = ctx.state;
    return null;
  }
  const root = createRoot(dom.window.document.getElementById('root')!);
  await act(async () => {
    root.render(React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }));
  });
  await waitFor(() => !!seen.state, 8000);
  openRoots.push({ root, act });
  return { seen, act };
}

test('R2-B2 (mount): booting with a DANGLING currentLineage pointer (F1 wrote it, the persist then failed) comes up usable and leaves the de-referenced city fully intact on disk', async () => {
  const dom = installJsdom();
  try {
    (globalThis as any).indexedDB = (await loadFakeIndexedDBFactory())(new Map());
    const { resetSaveStoreForTests } = await import('../src/sim/saveStore.ts');
    resetSaveStoreForTests();

    const { initialState, reducer } = await import('../src/sim/engine.ts');
    const {
      createSavepoint,
      persistSavepoint,
      readAllSavepoints,
      mostRecentSavepoint,
      readCurrentLineageId,
      writeCurrentLineageId,
      restoreFromSavepoint,
      SAVEPOINT_CAP,
    } = await import('../src/sim/replay.ts');
    const { versionBadgeLabel } = await import('../src/sim/version.ts');

    const running = versionBadgeLabel();
    const storage = dom.window.localStorage as unknown as Storage;

    // A real, healthy namespaced city on disk.
    const live = 'Lreal000000live';
    let city: any = { ...initialState(), unlockedAll: true, funds: 5_000_000_000, lineageId: live };
    for (let i = 0; i < 20; i++) city = reducer(city, { type: 'place', spec: 'res_hut', x: 4 + (i % 10) * 3, y: 4 + Math.floor(i / 10) * 3 } as any);
    const LIVE_BUILDINGS = city.buildings.length;
    assert.ok(LIVE_BUILDINGS > 0);
    for (let s = 0; s < SAVEPOINT_CAP; s++) {
      const at = new Date(Date.now() - (SAVEPOINT_CAP - s) * 60_000);
      assert.equal(persistSavepoint(storage, createSavepoint({ ...city, tick: 500 + s }, [], at, running, null, 30 + s), at), true);
    }

    // The dangling pointer: F1 moved it to a lineage whose persist then failed.
    writeCurrentLineageId(storage, 'Ldangling00000nothing');
    assert.equal(readAllSavepoints(storage, new Date(), 'Ldangling00000nothing').length, 0, 'test setup: the pointer must own nothing');

    const { seen } = await mountProvider(dom);
    await new Promise((r) => setTimeout(r, 1500));

    // 1. The app is up and usable — never a blank/wedged boot.
    pin(() => assert.ok(seen.state && typeof seen.state.tick === 'number', 'BOOT BRICKED on a dangling currentLineage pointer'));
    pin(() => assert.ok(seen.ctx && typeof seen.ctx.saveGame === 'function', 'the context never became usable'));

    // 2. It booted a FRESH city (correct: the pointed-at lineage owns nothing)
    //    and minted+adopted a lineage of its own rather than silently
    //    adopting the dangling one.
    const afterPointer = readCurrentLineageId(storage);
    pin(() =>
      assert.notEqual(
        afterPointer,
        'Ldangling00000nothing',
        'boot left the pointer on a lineage that owns nothing — the very next autosave would write into a namespace the NEXT boot re-resolves the same way, ' +
          'which is fine, but the pointer must be re-minted so the fresh city has a real identity rather than inheriting a dead one.',
      ),
    );

    // 3. THE CRITICAL PROPERTY: the de-referenced real city is untouched and
    //    still fully restorable. A mis-pointed pointer must be recoverable.
    pin(() =>
      assert.equal(
        readAllSavepoints(storage, new Date(), live).length,
        SAVEPOINT_CAP,
        'A DANGLING-POINTER BOOT DESTROYED THE DE-REFERENCED CITY\'S SAVEPOINTS — the F1 reorder is only safe while a failed load costs the pointer, never the data.',
      ),
    );
    const back = restoreFromSavepoint(storage, live);
    assert.equal(back.success, true);
    assert.equal(back.state!.buildings.length, LIVE_BUILDINGS, 'the de-referenced city no longer restores to what it was');
    assert.equal(mostRecentSavepoint(readAllSavepoints(storage, new Date(), live))!.lineageId, live);

    // 4. And the fresh session's own saves do not leak into it.
    let ok = false;
    await (async () => {
      const { act } = openRoots[openRoots.length - 1];
      await act(async () => {
        ok = await seen.ctx.saveGame();
      });
    })();
    assert.equal(ok, true, 'the fresh session must be able to save');
    pin(() =>
      assert.equal(
        readAllSavepoints(storage, new Date(), live).length,
        SAVEPOINT_CAP,
        'the fresh session\'s save landed in the DE-REFERENCED city\'s namespace — cross-contamination after a dangling-pointer boot',
      ),
    );
    assert.equal(mostRecentSavepoint(readAllSavepoints(storage, new Date(), live))!.snapshot.buildings.length, LIVE_BUILDINGS);
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});
