// attack-bug1156-r1.test.tsx — BUG-1156 independent destructive round r1
// (attacker: opus-round-r1-bug1156). The data-loss matrix against the
// lineage-pointer withhold + LineageRecoveryBanner + adoptLineage build.
// Mounts the REAL SimProvider against pre-seeded jsdom localStorage.
// Tests named PROBE-* document a pre-existing hazard (they assert the
// hazard's CURRENT shape so a future fix flips them deliberately).

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

/** Seed a city with a real building + non-zero tick under `lineageId`. */
async function seedCity(storage: Storage, lineageId: string | undefined, funds: number, tick: number, savedAt: Date) {
  const { createSavepoint, persistSavepoint } = await import('../src/sim/replay.ts');
  const { initialState } = await import('../src/sim/engine.ts');
  const base: any = initialState();
  const city: any = { ...base, funds, tick, buildings: [...base.buildings, { id: 9001, spec: 'res_hut', x: 7, y: 7 }] };
  if (lineageId) city.lineageId = lineageId;
  else delete city.lineageId;
  assert.ok(persistSavepoint(storage as any, createSavepoint(city, [], savedAt, 'test-build', null)), 'seed persist');
  return city;
}

function snapshotKeys(storage: Storage, prefix: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (let i = 0; i < storage.length; i++) {
    const k = storage.key(i)!;
    if (k.startsWith(prefix)) out[k] = storage.getItem(k)!;
  }
  return out;
}

async function clickButton(m: any, dom: JSDOM, el: Element | null) {
  assert.ok(el, 'button exists');
  await m.act(async () => {
    el!.dispatchEvent(new (dom.window as any).MouseEvent('click', { bubbles: true }));
  });
}

test('R1-A (live shape): pointer X/no saves, city under B — N=3 reloads with scratch saves: pointer stays X, B bytes untouched, nothing under X, adopt(B) byte-stable', async () => {
  const dom = await freshEnv();
  try {
    const { CURRENT_LINEAGE_KEY, readCurrentLineageId, adoptLineage, readAllSavepoints, listRecoverableLineages } = await import('../src/sim/replay.ts');
    const storage = dom.window.localStorage as unknown as Storage;
    const cityB = await seedCity(storage, 'LbR1A', 4_242_424, 777, new Date('2026-10-31T23:00:00Z'));
    storage.setItem(CURRENT_LINEAGE_KEY, 'LxR1A');
    const bBefore = snapshotKeys(storage, 'metropolis.savepoint.LbR1A.');
    assert.ok(Object.keys(bBefore).length > 0);
    const scratchIds = new Set<string>();
    for (let n = 0; n < 3; n++) {
      const m = await mountProvider(dom);
      assert.equal(readCurrentLineageId(storage as any), 'LxR1A', `reload ${n}: pointer must stay X`);
      const sid = m.seen.state.lineageId;
      assert.ok(sid && sid !== 'LbR1A' && sid !== 'LxR1A', `reload ${n}: scratch lineage`);
      scratchIds.add(sid);
      assert.ok(m.container.querySelector('[data-lineage="LbR1A"]'), `reload ${n}: B listed`);
      const ok = await m.seen.ctx.saveGame();
      assert.ok(ok, 'scratch save succeeded');
      assert.equal(readCurrentLineageId(storage as any), 'LxR1A', 'a scratch save must not move the pointer');
      await closeAllRoots();
    }
    assert.equal(scratchIds.size, 1, 'BUG-1156 F1 fix: ONE scratch lineage reused across all reloads');
    for (const sid of scratchIds) assert.ok(readAllSavepoints(storage as any, new Date(), sid).length > 0, 'scratch saves land under the scratch id');
    assert.equal(Object.keys(snapshotKeys(storage, 'metropolis.savepoint.LxR1A.')).length, 0, 'nothing written under X');
    assert.deepEqual(snapshotKeys(storage, 'metropolis.savepoint.LbR1A.'), bBefore, 'B bytes untouched across 3 scratch boots+saves');
    const listed = listRecoverableLineages(storage as any, 'LxR1A').map((l) => l.lineageId);
    assert.equal(listed.length, 2, 'B + exactly ONE scratch lineage recoverable (no pile-up)');

    adoptLineage(storage as any, 'LbR1A');
    const m2 = await mountProvider(dom);
    assert.equal(m2.seen.state.lineageId, 'LbR1A');
    assert.equal(m2.seen.state.tick, cityB.tick);
    assert.equal(m2.seen.state.funds, cityB.funds);
    assert.equal(m2.seen.state.buildings.length, cityB.buildings.length);
    // BUG-1156 r4 CHANGE: was `no banner`. That pin CONTRADICTS the r3 attacker's own R3-3/R3-4 durability
    // assertions (after a clean real-city restore the played scratch city must stay reachable); both cannot
    // hold. Now: no banner NOTE and the only row is the single scratch city (nothing about the real city).
    const rowsAfter = Array.from(m2.container.querySelectorAll('[data-lineage]')).map((e) => e.getAttribute('data-lineage'));
    assert.ok(rowsAfter.length <= 1 && !rowsAfter.includes('LbR1A'), 'clean restore: at most the one scratch nudge row');
    assert.ok(!m2.container.querySelector('[data-lineage-note]'), 'clean restore: no refusal note');
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});

test('R1-B: play 50 ticks in the scratch city + save, THEN adopt B via the banner — B restores clean AND the scratch lineage stays recoverable', async () => {
  const dom = await freshEnv();
  try {
    const { CURRENT_LINEAGE_KEY, readCurrentLineageId, readAllSavepoints, mostRecentSavepoint, listRecoverableLineages } = await import('../src/sim/replay.ts');
    const storage = dom.window.localStorage as unknown as Storage;
    const cityB = await seedCity(storage, 'LbR1B', 3_000_000, 500, new Date('2026-10-31T23:00:00Z'));
    storage.setItem(CURRENT_LINEAGE_KEY, 'LxR1B');
    const m = await mountProvider(dom);
    const sid = m.seen.state.lineageId;
    await m.act(async () => {
      m.seen.ctx.dispatch({ type: 'speed', speed: 0 });
    });
    for (let i = 0; i < 50; i++) {
      await m.act(async () => {
        m.seen.ctx.dispatch({ type: 'tick' });
      });
    }
    const scratchTick = m.seen.state.tick;
    assert.ok(scratchTick >= 50);
    assert.ok(await m.seen.ctx.saveGame());
    await clickButton(m, dom, m.container.querySelector('[data-lineage="LbR1B"] button'));
    assert.equal(readCurrentLineageId(storage as any), 'LbR1B');
    await closeAllRoots();
    const m2 = await mountProvider(dom);
    assert.equal(m2.seen.state.lineageId, 'LbR1B');
    assert.equal(m2.seen.state.tick, cityB.tick);
    assert.equal(m2.seen.state.funds, cityB.funds);
    const sp = mostRecentSavepoint(readAllSavepoints(storage as any, new Date(), sid));
    assert.ok(sp, 'scratch lineage still on disk');
    assert.equal(sp!.snapshotTick, scratchTick, 'scratch progress intact');
    assert.ok(listRecoverableLineages(storage as any, 'LbR1B').some((l) => l.lineageId === sid), 'scratch lineage is listable (API)');
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});

test('R1-C: adopt B -> Start Over -> reload: pointer moves only on explicit reset; B stays reachable from the banner', async () => {
  const dom = await freshEnv();
  try {
    const { CURRENT_LINEAGE_KEY, readCurrentLineageId, adoptLineage } = await import('../src/sim/replay.ts');
    const storage = dom.window.localStorage as unknown as Storage;
    await seedCity(storage, 'LbR1C', 2_000_000, 300, new Date('2026-10-31T23:00:00Z'));
    storage.setItem(CURRENT_LINEAGE_KEY, 'LxR1C');
    await mountProvider(dom);
    await closeAllRoots();
    adoptLineage(storage as any, 'LbR1C');
    const m = await mountProvider(dom);
    assert.equal(m.seen.state.lineageId, 'LbR1C');
    await m.act(async () => {
      m.seen.ctx.dispatch({ type: 'reset' });
    });
    await new Promise((r) => setTimeout(r, 300));
    const afterReset = readCurrentLineageId(storage as any);
    assert.notEqual(afterReset, 'LbR1C', 'Start Over is explicit intent: pointer moves to the new city');
    assert.equal(m.seen.state.lineageId, afterReset, 'pointer and running city agree after Start Over');
    await closeAllRoots();
    const m2 = await mountProvider(dom);
    // the reset city may or may not have autosaved; either way B must remain reachable or be the restored city
    const restoredNew = m2.seen.state.lineageId === afterReset;
    if (!restoredNew) {
      assert.equal(readCurrentLineageId(storage as any), afterReset, 'pointer still withheld on the reset id');
    }
    const bannerB = m2.container.querySelector('[data-lineage="LbR1C"]');
    // DOCUMENTED: once a boot RESTORES successfully, the banner never lists other lineages.
    if (restoredNew) assert.equal(bannerB, null);
    else assert.ok(bannerB, 'B reachable after Start Over + reload');
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});

test('R1-D: corrupt save under CURRENT lineage + B elsewhere: pointer held, banner lists B (and not the corrupt current)', async () => {
  const dom = await freshEnv();
  try {
    const { createSavepoint, persistSavepoint, CURRENT_LINEAGE_KEY, readCurrentLineageId } = await import('../src/sim/replay.ts');
    const { initialState } = await import('../src/sim/engine.ts');
    const storage = dom.window.localStorage as unknown as Storage;
    await seedCity(storage, 'LbR1D', 1_500_000, 200, new Date('2026-10-31T22:00:00Z'));
    const bad: any = {
      ...initialState(),
      lineageId: 'LcurR1D',
      buildings: [
        { id: 1, spec: 'res_hut', x: 5, y: 5 },
        { id: 1, spec: 'res_hut', x: 8, y: 8 },
      ],
    };
    assert.ok(persistSavepoint(storage as any, createSavepoint(bad, [], new Date('2026-10-31T23:00:00Z'), 'test-build', null)));
    storage.setItem(CURRENT_LINEAGE_KEY, 'LcurR1D');
    const curBefore = snapshotKeys(storage, 'metropolis.savepoint.LcurR1D.');
    const m = await mountProvider(dom);
    assert.equal(readCurrentLineageId(storage as any), 'LcurR1D');
    assert.ok(m.container.querySelector('[data-lineage="LbR1D"]'), 'B offered');
    assert.equal(m.container.querySelector('[data-lineage="LcurR1D"]'), null, 'corrupt current not offered for adoption (it is what a reload retries)');
    assert.deepEqual(snapshotKeys(storage, 'metropolis.savepoint.LcurR1D.'), curBefore, 'corrupt save left on disk for forensics');
    assert.match(String(m.seen.state.placeNotice), /could not be restored/);
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});

test('R1-E: Dismiss hides the banner for this boot only — a reload re-offers recovery, pointer untouched by Dismiss', async () => {
  const dom = await freshEnv();
  try {
    const { CURRENT_LINEAGE_KEY, readCurrentLineageId } = await import('../src/sim/replay.ts');
    const storage = dom.window.localStorage as unknown as Storage;
    await seedCity(storage, 'LbR1E', 1_000_000, 100, new Date('2026-10-31T23:00:00Z'));
    storage.setItem(CURRENT_LINEAGE_KEY, 'LxR1E');
    const m = await mountProvider(dom);
    const banner = m.container.querySelector('.lineage-recovery-banner')!;
    const dismiss = Array.from(banner.querySelectorAll('button')).find((b) => b.textContent === 'Dismiss')!;
    await clickButton(m, dom, dismiss);
    assert.equal(m.container.querySelector('.lineage-recovery-banner'), null);
    assert.equal(readCurrentLineageId(storage as any), 'LxR1E');
    await closeAllRoots();
    const m2 = await mountProvider(dom);
    assert.ok(m2.container.querySelector('[data-lineage="LbR1E"]'), 'reload re-offers');
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});

test('R1-F: three recoverable lineages incl. legacy — newest-first deterministic, legacy adoptable', async () => {
  const dom = await freshEnv();
  try {
    const { CURRENT_LINEAGE_KEY, adoptLineage, listRecoverableLineages } = await import('../src/sim/replay.ts');
    const storage = dom.window.localStorage as unknown as Storage;
    const legacy = await seedCity(storage, undefined, 11_111, 111, new Date('2026-10-31T21:00:00Z'));
    await seedCity(storage, 'Lone', 22_222, 222, new Date('2026-10-31T23:00:00Z'));
    await seedCity(storage, 'Ltwo', 33_333, 333, new Date('2026-10-31T22:00:00Z'));
    storage.setItem(CURRENT_LINEAGE_KEY, 'LxR1F');
    const ids = listRecoverableLineages(storage as any, 'LxR1F').map((l) => l.lineageId);
    assert.deepEqual(ids, ['Lone', 'Ltwo', 'legacy']);
    const m = await mountProvider(dom);
    const rendered = Array.from(m.container.querySelectorAll('[data-lineage]')).map((e) => e.getAttribute('data-lineage'));
    assert.deepEqual(rendered, ['Lone', 'Ltwo', 'legacy']);
    await closeAllRoots();
    adoptLineage(storage as any, 'legacy');
    const m2 = await mountProvider(dom);
    assert.equal(m2.seen.state.tick, legacy.tick);
    assert.equal(m2.seen.state.funds, legacy.funds);
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});

test('R1-G: hostile key space — quote/unicode/dotted lineage ids, truncated JSON, non-object JSON: listing fails closed per entry, boot never throws', async () => {
  const dom = await freshEnv();
  try {
    const { CURRENT_LINEAGE_KEY, listRecoverableLineages, readCurrentLineageId } = await import('../src/sim/replay.ts');
    const storage = dom.window.localStorage as unknown as Storage;
    const weird = 'L"q\'<b>é😀.dot';
    await seedCity(storage, weird, 5_000, 50, new Date('2026-10-31T23:00:00Z'));
    storage.setItem('metropolis.savepoint.Ltrunc.0', '{"snapshotTick":5,"savedAt":"2026-');
    storage.setItem('metropolis.savepoint.Lnull.0', 'null');
    storage.setItem('metropolis.savepoint.Lstr.0', '"just a string"');
    storage.setItem('metropolis.savepoint.Larr.0', '[]');
    storage.setItem(CURRENT_LINEAGE_KEY, 'LxR1G');
    let listed: any[] = [];
    assert.doesNotThrow(() => {
      listed = listRecoverableLineages(storage as any, 'LxR1G');
    });
    const ids = listed.map((l) => l.lineageId);
    assert.ok(ids.includes(weird), 'hostile-but-valid lineage listed');
    assert.ok(!ids.includes('Ltrunc'), 'truncated JSON omitted');
    // record what non-object JSON does (honesty: must not surface an un-adoptable row with undefined tick)
    const junk = listed.filter((l) => typeof l.snapshotTick !== 'number' || typeof l.savedAt !== 'string');
    const m = await mountProvider(dom);
    assert.equal(readCurrentLineageId(storage as any), 'LxR1G');
    const row = Array.from(m.container.querySelectorAll('[data-lineage]')).find((e) => e.getAttribute('data-lineage') === weird);
    assert.ok(row, 'hostile id rendered as text (escaped by React)');
    assert.equal(m.container.querySelector('b'), null, 'no markup injection');
    assert.deepEqual(junk.map((l) => l.lineageId), [], `non-object savepoint JSON surfaced as adoptable rows: ${JSON.stringify(junk)}`);
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});

test('R1-H: adoption write throws — pointer is old or new, never a third state; failure is not silent', async () => {
  const dom = await freshEnv();
  try {
    const { CURRENT_LINEAGE_KEY, readCurrentLineageId, adoptLineage } = await import('../src/sim/replay.ts');
    const storage = dom.window.localStorage as unknown as Storage;
    await seedCity(storage, 'LbR1H', 1_000_000, 100, new Date('2026-10-31T23:00:00Z'));
    storage.setItem(CURRENT_LINEAGE_KEY, 'LxR1H');
    const throwing: any = {
      getItem: (k: string) => storage.getItem(k),
      setItem: () => {
        throw new Error('QuotaExceededError');
      },
      removeItem: (k: string) => storage.removeItem(k),
      key: (i: number) => storage.key(i),
      get length() {
        return storage.length;
      },
    };
    assert.doesNotThrow(() => adoptLineage(throwing, 'LbR1H'));
    assert.equal(readCurrentLineageId(storage as any), 'LxR1H', 'old pointer kept');
    // F3 fixed (flipped deliberately): adoptLineage now reports the failure so the UI can refuse to reload.
    assert.equal(adoptLineage(throwing, 'LbR1H'), false);
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});

test('R1-6: StrictMode double boot records MET-V868 once AND the banner renders; fresh start + clean restore show no banner', async () => {
  const dom = await freshEnv();
  try {
    const { CURRENT_LINEAGE_KEY } = await import('../src/sim/replay.ts');
    const { recentErrors } = await import('../src/sim/backend.ts');
    const storage = dom.window.localStorage as unknown as Storage;
    await seedCity(storage, 'LbR16strict', 1_000_000, 100, new Date('2026-10-31T23:00:00Z'));
    storage.setItem(CURRENT_LINEAGE_KEY, 'LxR16strict');
    const m = await mountProvider(dom, true);
    const n = recentErrors()
      .filter((e: any) => e.code === 'MET-V868' && String(e.msg ?? '').includes('LbR16strict'))
      .reduce((s: number, e: any) => s + (e.count ?? 1), 0);
    assert.equal(n, 1);
    assert.ok(m.container.querySelector('[data-lineage="LbR16strict"]'));
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});

test('PROBE-5: shared journal key — scratch city play overwrites B\'s journal; adopt(B) then the cross-build Rebuild replays the SCRATCH journal over B', async () => {
  const dom = await freshEnv();
  try {
    const { CURRENT_LINEAGE_KEY, readAllSavepoints, mostRecentSavepoint } = await import('../src/sim/replay.ts');
    const { JOURNAL_KEY } = await import('../src/sim/journal.ts');
    const storage = dom.window.localStorage as unknown as Storage;
    const cityB = await seedCity(storage, 'LbP5', 9_999_999, 900, new Date('2026-10-31T23:00:00Z'));
    const bJournal = JSON.stringify({ entries: [{ tick: 0, action: { type: 'debugFunds', amount: 1 } }] });
    storage.setItem(JOURNAL_KEY, bJournal);
    storage.setItem(CURRENT_LINEAGE_KEY, 'LxP5');
    const m = await mountProvider(dom);
    await m.act(async () => {
      m.seen.ctx.dispatch({ type: 'speed', speed: 0 });
      m.seen.ctx.dispatch({ type: 'debugFunds', amount: 123 });
    });
    await new Promise((r) => setTimeout(r, 1500));
    await closeAllRoots();
    const journalAfterScratch = storage.getItem(JOURNAL_KEY);
    const clobbered = journalAfterScratch !== bJournal;
    console.log(`PROBE-5 journal clobbered by scratch city: ${clobbered} (len ${journalAfterScratch?.length ?? 'null'})`);

    const { adoptLineage } = await import('../src/sim/replay.ts');
    adoptLineage(storage as any, 'LbP5');
    const m2 = await mountProvider(dom);
    assert.equal(m2.seen.state.funds, cityB.funds, 'adopt restores B snapshot');
    await new Promise((r) => setTimeout(r, 500));
    const rebuildBtn = Array.from(m2.container.ownerDocument.querySelectorAll('button')).find((b) => /^Rebuild on/.test(b.textContent ?? ''));
    console.log(`PROBE-5 cross-build Rebuild prompt offered after adopt: ${!!rebuildBtn}`);
    if (rebuildBtn) {
      await clickButton(m2, dom, rebuildBtn);
      await new Promise((r) => setTimeout(r, 2000));
      const spB = mostRecentSavepoint(readAllSavepoints(storage as any, new Date(), 'LbP5'));
      console.log(`PROBE-5 B after Rebuild: tick=${spB?.snapshotTick} funds=${(spB?.snapshot as any)?.funds} buildings=${(spB?.snapshot as any)?.buildings?.length} (was tick=${cityB.tick} funds=${cityB.funds} buildings=${cityB.buildings.length})`);
    }
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});

test('PROBE-5-CONTROL: same Rebuild on B with B\'s OWN pointer and NO scratch city (no adoption involved) — isolates whether the loss is adoption-specific', async () => {
  const dom = await freshEnv();
  try {
    const { CURRENT_LINEAGE_KEY, readAllSavepoints, mostRecentSavepoint } = await import('../src/sim/replay.ts');
    const { JOURNAL_KEY } = await import('../src/sim/journal.ts');
    const storage = dom.window.localStorage as unknown as Storage;
    const cityB = await seedCity(storage, 'LbP5c', 9_999_999, 900, new Date('2026-10-31T23:00:00Z'));
    storage.setItem(JOURNAL_KEY, JSON.stringify({ entries: [] }));
    storage.setItem(CURRENT_LINEAGE_KEY, 'LbP5c');
    const m2 = await mountProvider(dom);
    assert.equal(m2.seen.state.funds, cityB.funds);
    await new Promise((r) => setTimeout(r, 500));
    const rebuildBtn = Array.from(m2.container.ownerDocument.querySelectorAll('button')).find((b) => /^Rebuild on/.test(b.textContent ?? ''));
    console.log(`PROBE-5-CONTROL Rebuild prompt offered: ${!!rebuildBtn}`);
    if (rebuildBtn) {
      await clickButton(m2, dom, rebuildBtn);
      await new Promise((r) => setTimeout(r, 2000));
      const spB = mostRecentSavepoint(readAllSavepoints(storage as any, new Date(), 'LbP5c'));
      console.log(`PROBE-5-CONTROL B after Rebuild: tick=${spB?.snapshotTick} funds=${(spB?.snapshot as any)?.funds} (was tick=${cityB.tick} funds=${cityB.funds})`);
    }
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});
