// bug1156-lineage-pointer-withheld.test.tsx — BUG-1156 (P1, save loss).
// An IMPLICIT boot refusal (restore refused / pointer drifted while savepoints
// exist under other lineages) must NOT overwrite metropolis.currentLineage.
// Mounts the REAL SimProvider against pre-seeded localStorage.

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
  (globalThis as any).Blob = window.Blob;
  (globalThis as any).requestAnimationFrame = window.requestAnimationFrame.bind(window);
  (globalThis as any).cancelAnimationFrame = window.cancelAnimationFrame.bind(window);
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  window.HTMLAnchorElement.prototype.click = function () {};
  return dom;
}

async function freshEnv() {
  const dom = installJsdom();
  const specifier = './helpers/fakeIndexedDB.mjs';
  const mod: any = await import(specifier);
  (globalThis as any).indexedDB = mod.createFakeIndexedDBFactory(new Map());
  const { resetSaveStoreForTests } = await import('../src/sim/saveStore.ts');
  resetSaveStoreForTests();
  const { resetBootRefusalGuardForTests } = await import('../src/sim/store.tsx');
  resetBootRefusalGuardForTests();
  return dom;
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
      /* gone */
    }
  }
}

async function mountProvider(dom: JSDOM, strict = false) {
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
  const container = dom.window.document.getElementById('root')!;
  const root = createRoot(container);
  const tree = React.default.createElement(SimProvider, { children: React.default.createElement(Probe) });
  await act(async () => {
    root.render(strict ? React.default.createElement(React.default.StrictMode, null, tree) : tree);
  });
  await waitFor(() => !!seen.state, 8000);
  openRoots.push({ root, act });
  await new Promise((r) => setTimeout(r, 200));
  return { seen, root, act, container };
}

async function seedCity(storage: Storage, lineageId: string, funds: number) {
  const { createSavepoint, persistSavepoint } = await import('../src/sim/replay.ts');
  const { initialState } = await import('../src/sim/engine.ts');
  const city = { ...initialState(), lineageId, funds };
  assert.ok(persistSavepoint(storage as any, createSavepoint(city as any, [], new Date(), 'test-build', null)));
  return city;
}

const v868Count = async (needle: string) => {
  const { recentErrors } = await import('../src/sim/backend.ts');
  // backend.ts collapses identical messages into one record with a `count`; sum counts so a double raise is visible.
  return recentErrors()
    .filter((e: any) => e.code === 'MET-V868' && String(e.msg ?? '').includes(needle))
    .reduce((n: number, e: any) => n + (e.count ?? 1), 0);
};

test('BUG-1156: pointer=X with no saves, city saved under B -> pointer NOT moved, B listed, adopt(B) restores B', async () => {
  const dom = await freshEnv();
  try {
    const { CURRENT_LINEAGE_KEY, readCurrentLineageId, adoptLineage } = await import('../src/sim/replay.ts');
    const storage = dom.window.localStorage as unknown as Storage;
    const cityB = await seedCity(storage, 'Lbbbb1156', 7_654_321);
    storage.setItem(CURRENT_LINEAGE_KEY, 'Lxxxx1156');

    const m = await mountProvider(dom);

    assert.equal(readCurrentLineageId(storage as any), 'Lxxxx1156', 'the pointer must NOT be overwritten by a freshly minted lineage');
    assert.notEqual(m.seen.state.lineageId, 'Lbbbb1156');
    assert.notEqual(m.seen.state.lineageId, 'Lxxxx1156', 'the fresh city runs under its own scratch lineage');
    assert.equal(await v868Count('Lbbbb1156'), 1, 'MET-V868 still raised');
    assert.match(String(m.seen.state.placeNotice), /Adopt/);
    const banner = m.container.querySelector('.lineage-recovery-banner');
    assert.ok(banner, 'recovery banner is visible');
    assert.ok(banner!.querySelector('[data-lineage="Lbbbb1156"]'), 'B is listed');
    assert.match(banner!.textContent ?? '', /tick \d+/);

    await closeAllRoots();
    adoptLineage(storage as any, 'Lbbbb1156');
    const m2 = await mountProvider(dom);
    assert.equal(m2.seen.state.lineageId, 'Lbbbb1156', 'adopted lineage restores');
    assert.equal(m2.seen.state.funds, cityB.funds, 'adopted city round-trips its data');
    assert.ok(!m2.container.querySelector('.lineage-recovery-banner'));
    assert.equal(m2.seen.state.tick, (cityB as any).tick);
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});

test('BUG-1156: clicking Adopt in the banner sets the pointer to that lineage', async () => {
  const dom = await freshEnv();
  try {
    const { CURRENT_LINEAGE_KEY, readCurrentLineageId } = await import('../src/sim/replay.ts');
    const storage = dom.window.localStorage as unknown as Storage;
    await seedCity(storage, 'Lbbbb1156', 1_000_000);
    storage.setItem(CURRENT_LINEAGE_KEY, 'Lxxxx1156');
    const m = await mountProvider(dom);
    const btn = m.container.querySelector('[data-lineage="Lbbbb1156"] button') as HTMLButtonElement | null;
    assert.ok(btn, 'Adopt button present');
    await m.act(async () => {
      btn!.dispatchEvent(new (dom.window as any).MouseEvent('click', { bubbles: true }));
    });
    assert.equal(readCurrentLineageId(storage as any), 'Lbbbb1156');
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});

test('BUG-1156: genuinely fresh start (no savepoints anywhere) still writes the pointer and shows no banner/V868', async () => {
  const dom = await freshEnv();
  try {
    const { readCurrentLineageId, CURRENT_LINEAGE_KEY } = await import('../src/sim/replay.ts');
    const storage = dom.window.localStorage as unknown as Storage;
    const { recentErrors } = await import('../src/sim/backend.ts');
    const beforeIds = new Set(recentErrors().map((e: any) => e.correlationId));
    const m = await mountProvider(dom);
    assert.equal(storage.getItem(CURRENT_LINEAGE_KEY), m.seen.state.lineageId);
    assert.equal(readCurrentLineageId(storage as any), m.seen.state.lineageId);
    assert.ok(!m.container.querySelector('.lineage-recovery-banner'));
    assert.equal(recentErrors().filter((e: any) => !beforeIds.has(e.correlationId) && e.code === 'MET-V868').length, 0);
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});

test('BUG-1156: StrictMode double boot records MET-V868 once', async () => {
  const dom = await freshEnv();
  try {
    const { CURRENT_LINEAGE_KEY } = await import('../src/sim/replay.ts');
    const storage = dom.window.localStorage as unknown as Storage;
    // unique ids: backend.ts accumulates `count` on identical messages across tests in this process
    await seedCity(storage, 'Lstrict1156', 1_000_000);
    storage.setItem(CURRENT_LINEAGE_KEY, 'Lxstrict1156');
    await mountProvider(dom, true);
    assert.equal(await v868Count('Lstrict1156'), 1);
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});

test('BUG-1156: restore refused for the CURRENT lineage (corrupt save) leaves the pointer on that lineage', async () => {
  const dom = await freshEnv();
  try {
    const { createSavepoint, persistSavepoint, CURRENT_LINEAGE_KEY, readCurrentLineageId } = await import('../src/sim/replay.ts');
    const { initialState } = await import('../src/sim/engine.ts');
    const storage = dom.window.localStorage as unknown as Storage;
    const bad = {
      ...initialState(),
      lineageId: 'Lcurr1156',
      buildings: [
        { id: 1, spec: 'res_hut', x: 5, y: 5 },
        { id: 1, spec: 'res_hut', x: 8, y: 8 },
      ],
    };
    assert.ok(persistSavepoint(storage as any, createSavepoint(bad as any, [], new Date(), 'test-build', null)));
    storage.setItem(CURRENT_LINEAGE_KEY, 'Lcurr1156');
    const m = await mountProvider(dom);
    assert.equal(readCurrentLineageId(storage as any), 'Lcurr1156', 'refusal must not abandon the lineage that still holds the save');
    assert.notEqual(m.seen.state.lineageId, 'Lcurr1156');
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});


test('BUG-1156 F1: reloading N times while the pointer is withheld leaves exactly ONE scratch lineage; scratch ranks last and is labelled', async () => {
  const dom = await freshEnv();
  try {
    const { CURRENT_LINEAGE_KEY, scanAllSavepointLineages, SCRATCH_LINEAGE_KEY } = await import('../src/sim/replay.ts');
    const storage = dom.window.localStorage as unknown as Storage;
    await seedCity(storage, 'Lreal1156f1', 1_000_000);
    storage.setItem(CURRENT_LINEAGE_KEY, 'Lx1156f1');
    const ids = new Set<string>();
    for (let n = 0; n < 5; n++) {
      const m = await mountProvider(dom);
      ids.add(m.seen.state.lineageId);
      assert.ok(await m.seen.ctx.saveGame());
      await closeAllRoots();
    }
    assert.equal(ids.size, 1, 'the same scratch lineage across 5 reloads');
    assert.equal(storage.getItem(SCRATCH_LINEAGE_KEY), Array.from(ids)[0]);
    assert.equal(scanAllSavepointLineages(storage as any).length, 2, 'real + exactly one scratch on disk');
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});

test('BUG-1156 F1: adopting the SCRATCH row keeps the real city discoverable; adopting the real city clears the scratch id', async () => {
  const dom = await freshEnv();
  try {
    const { CURRENT_LINEAGE_KEY, SCRATCH_LINEAGE_KEY, adoptLineage, readScratchLineageId } = await import('../src/sim/replay.ts');
    const storage = dom.window.localStorage as unknown as Storage;
    await seedCity(storage, 'Lreal1156b', 1_000_000);
    storage.setItem(CURRENT_LINEAGE_KEY, 'Lx1156b');
    const m = await mountProvider(dom);
    const sid = m.seen.state.lineageId;
    assert.ok(await m.seen.ctx.saveGame());
    await closeAllRoots();
    // reload: scratch row (the active one) is not offered to itself; adopt it explicitly
    assert.equal(adoptLineage(storage as any, sid), true);
    assert.equal(readScratchLineageId(storage as any), sid, 'adopting scratch keeps the scratch marker');
    const m2 = await mountProvider(dom);
    assert.equal(m2.seen.state.lineageId, sid);
    const real = m2.container.querySelector('[data-lineage="Lreal1156b"]');
    assert.ok(real, 'real city still offered after adopting the scratch city');
    assert.ok(!m2.container.querySelector(`[data-lineage="${sid}"]`), 'the running city is not listed');
    assert.equal(adoptLineage(storage as any, 'Lreal1156b'), true);
    // BUG-1156 r5 FLIP: adoption no longer forgets the scratch resume id (F1: a refused adopt must resume the played scratch
    // city, not mint a new one); the CLEAN restore of the real city at boot forgets it instead.
    assert.equal(storage.getItem(SCRATCH_LINEAGE_KEY), sid, 'BUG-1156 r5 FLIP: adopting keeps the scratch resume id until the real city restores cleanly');
    await closeAllRoots();
    const m3 = await mountProvider(dom);
    assert.equal(m3.seen.state.lineageId, 'Lreal1156b');
    assert.equal(storage.getItem(SCRATCH_LINEAGE_KEY), null, 'a clean restore of the real city forgets the scratch resume id');
    // r4: the scratch city (saved) is still nudged about after the real city restores; it is never orphaned.
    assert.ok(m3.container.querySelector(`[data-lineage="${sid}"]`), 'scratch city still offered');
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});

test('BUG-1156 F3: adoptLineage reports failure when the pointer write is refused', async () => {
  const { adoptLineage } = await import('../src/sim/replay.ts');
  const broken = { getItem: () => null, setItem: () => { throw new Error('quota'); }, removeItem: () => {} };
  assert.equal(adoptLineage(broken as any, 'Lany'), false);
});

test('BUG-1156 r3 F4: banner wording does not claim a temporary city when the running city is an adopted scratch', async () => {
  const React = await import('react');
  const { renderToStaticMarkup } = await import('react-dom/server');
  const { LineageRecoveryBanner } = await import('../src/components/LineageRecoveryBanner.tsx');
  const rows = [{ lineageId: 'Lreal', snapshotTick: 5, savedAt: 'x', scratch: false, label: "City Lreal" }];
  const adopted = renderToStaticMarkup(React.default.createElement(LineageRecoveryBanner, { lineages: rows, onAdopt: () => {}, onDismiss: () => {}, kind: 'adopted' }));
  const temp = renderToStaticMarkup(React.default.createElement(LineageRecoveryBanner, { lineages: rows, onAdopt: () => {}, onDismiss: () => {} }));
  assert.ok(!/recovered session city/.test(adopted), 'adopted wording');
  assert.ok(/recovered session city/.test(temp) && !/temporary/i.test(temp), 'default wording: durable, never temporary');
  const noteOnly = renderToStaticMarkup(React.default.createElement(LineageRecoveryBanner, { lineages: [], note: 'kept on disk', onAdopt: () => {}, onDismiss: () => {} }));
  assert.ok(/kept on disk/.test(noteOnly), 'note shown with zero rows');
});
