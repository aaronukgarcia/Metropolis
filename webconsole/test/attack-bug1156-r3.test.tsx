// attack-bug1156-r3.test.tsx — BUG-1156 independent destructive round r3
// (attacker: opus-round-r3-bug1156). Durability sweep across every lineage kind,
// corrupt-then-fixed, cross-build scratch restore, history eviction, StrictMode.

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

async function clickButton(m: any, dom: JSDOM, el: Element | null) {
  assert.ok(el, 'button exists');
  await m.act(async () => {
    el!.dispatchEvent(new (dom.window as any).MouseEvent('click', { bubbles: true }));
  });
}



async function playTicks(m: any, n: number) {
  await m.act(async () => {
    m.seen.ctx.dispatch({ type: 'speed', speed: 0 });
  });
  for (let i = 0; i < n; i++) {
    await m.act(async () => {
      m.seen.ctx.dispatch({ type: 'tick' });
    });
  }
}
function rowsOf(m: any): string[] {
  return Array.from(m.container.querySelectorAll('[data-lineage]')).map((e: any) => e.getAttribute('data-lineage'));
}
async function adoptViaBanner(m: any, dom: JSDOM, id: string) {
  await clickButton(m, dom, m.container.querySelector(`[data-lineage="${id}"] button`));
  await closeAllRoots();
}
function v868For(recentErrors: () => any[], needle: string): number {
  return recentErrors()
    .filter((e: any) => e.code === 'MET-V868' && String(e.msg ?? '').includes(needle))
    .reduce((s: number, e: any) => s + (e.count ?? 1), 0);
}

test('R3-1 (real lineage, genuinely fresh): play, save, reload - progress kept on the same lineage', async () => {
  const dom = await freshEnv();
  try {
    const { readCurrentLineageId } = await import('../src/sim/replay.ts');
    const storage = dom.window.localStorage as unknown as Storage;
    const m = await mountProvider(dom);
    const lid = m.seen.state.lineageId;
    await playTicks(m, 50);
    const played = m.seen.state.tick;
    assert.ok(await m.seen.ctx.saveGame());
    await closeAllRoots();
    const m2 = await mountProvider(dom);
    console.log('R3-1 real', lid, 'played', played, 'boot', m2.seen.state.tick, 'pointer', readCurrentLineageId(storage as any));
    assert.equal(m2.seen.state.lineageId, lid);
    assert.ok(m2.seen.state.tick >= played);
    assert.deepEqual(rowsOf(m2), []);
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});

test('R3-2 (legacy lineage): legacy save restores, play, save, reload - progress kept', async () => {
  const dom = await freshEnv();
  try {
    const storage = dom.window.localStorage as unknown as Storage;
    await seedCity(storage, undefined, 4_000_000, 700, new Date('2026-10-31T23:00:00Z'));
    const m = await mountProvider(dom);
    const t0 = m.seen.state.tick;
    await playTicks(m, 20);
    const played = m.seen.state.tick;
    assert.ok(await m.seen.ctx.saveGame());
    await closeAllRoots();
    const m2 = await mountProvider(dom);
    console.log('R3-2 legacy boot0', t0, 'played', played, 'boot1', m2.seen.state.tick, 'lineage', m2.seen.state.lineageId);
    assert.ok(t0 >= 700);
    assert.ok(m2.seen.state.tick >= played);
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});

test('R3-3 (full sequence): scratch play, reload, play more, adopt real via banner, reload, re-adopt scratch from the banner', async () => {
  const dom = await freshEnv();
  try {
    const { CURRENT_LINEAGE_KEY, readCurrentLineageId, listRecoverableLineages } = await import('../src/sim/replay.ts');
    const storage = dom.window.localStorage as unknown as Storage;
    await seedCity(storage, 'LbR33', 4_000_000, 700, new Date('2026-10-31T23:00:00Z'));
    storage.setItem(CURRENT_LINEAGE_KEY, 'LxR33');
    const log: string[] = [];
    const m = await mountProvider(dom);
    const sid = m.seen.state.lineageId;
    await playTicks(m, 30);
    assert.ok(await m.seen.ctx.saveGame());
    await closeAllRoots();
    const m2 = await mountProvider(dom);
    log.push(`reload1 lineage=${m2.seen.state.lineageId} tick=${m2.seen.state.tick} rows=${rowsOf(m2)}`);
    assert.equal(m2.seen.state.lineageId, sid);
    assert.ok(m2.seen.state.tick >= 30, 'step 2: scratch resumes');
    await playTicks(m2, 30);
    const p2 = m2.seen.state.tick;
    assert.ok(await m2.seen.ctx.saveGame());
    await adoptViaBanner(m2, dom, 'LbR33');
    log.push(`after adopt real pointer=${readCurrentLineageId(storage as any)}`);
    const m3 = await mountProvider(dom);
    const rows3 = rowsOf(m3);
    log.push(`reload2 lineage=${m3.seen.state.lineageId} tick=${m3.seen.state.tick} rows=${JSON.stringify(rows3)}`);
    const onDisk = listRecoverableLineages(storage as any, 'LbR33').find((l) => l.lineageId === sid);
    log.push(`scratch still on disk tick=${onDisk?.snapshotTick} (played ${p2})`);
    console.log('R3-3', log.join(' | '));
    assert.equal(m3.seen.state.lineageId, 'LbR33');
    assert.ok(m3.seen.state.tick >= 700, 'real city restored');
    assert.ok((onDisk?.snapshotTick ?? 0) >= p2, 'scratch progress on disk');
    assert.ok(rows3.includes(sid), 'DURABILITY: the played scratch city must stay reachable from the UI after adopting the real city');
    await adoptViaBanner(m3, dom, sid);
    const m4 = await mountProvider(dom);
    assert.equal(m4.seen.state.lineageId, sid);
    assert.ok(m4.seen.state.tick >= p2, 're-adopted scratch resumes at the played tick');
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});

test('R3-4 (corrupt-only, then the corruption is fixed): first boot notes, later boots resume, then the real city comes back and the scratch stays listed', async () => {
  const dom = await freshEnv();
  try {
    const { createSavepoint, persistSavepoint, CURRENT_LINEAGE_KEY, listRecoverableLineages } = await import('../src/sim/replay.ts');
    const { initialState } = await import('../src/sim/engine.ts');
    const storage = dom.window.localStorage as unknown as Storage;
    const bad: any = { ...initialState(), lineageId: 'LcR34', buildings: [{ id: 1, spec: 'res_hut', x: 5, y: 5 }, { id: 1, spec: 'res_hut', x: 8, y: 8 }] };
    assert.ok(persistSavepoint(storage as any, createSavepoint(bad, [], new Date('2026-10-31T23:00:00Z'), 'test-build', null)));
    storage.setItem(CURRENT_LINEAGE_KEY, 'LcR34');
    const m = await mountProvider(dom);
    const sid = m.seen.state.lineageId;
    const note1 = m.container.querySelector('.lineage-recovery-banner')?.textContent ?? '';
    await playTicks(m, 40);
    assert.ok(await m.seen.ctx.saveGame());
    await closeAllRoots();
    const m2 = await mountProvider(dom);
    const t2 = m2.seen.state.tick;
    await playTicks(m2, 10);
    const p2 = m2.seen.state.tick;
    assert.ok(await m2.seen.ctx.saveGame());
    await closeAllRoots();
    // "Fix the corruption": a valid, newer save appears under the current lineage.
    const base: any = initialState();
    const good: any = { ...base, lineageId: 'LcR34', funds: 5_000_000, tick: 500, buildings: [...base.buildings, { id: 9002, spec: 'res_hut', x: 9, y: 9 }] };
    assert.ok(persistSavepoint(storage as any, createSavepoint(good, [], new Date(Date.now() + 60_000), 'test-build', null)), 'fixed save persists');
    const m3 = await mountProvider(dom);
    const rows3 = rowsOf(m3);
    const onDisk = listRecoverableLineages(storage as any, 'LcR34').find((l) => l.lineageId === sid);
    console.log('R3-4 note1', JSON.stringify(note1.slice(0, 160)), 'boot2', t2, 'played', p2, 'boot3', m3.seen.state.lineageId, m3.seen.state.tick, 'rows3', JSON.stringify(rows3), 'scratch on disk', onDisk?.snapshotTick);
    assert.ok(/unrecoverable/.test(note1) && /KEPT/.test(note1));
    assert.ok(t2 >= 40, 'second boot resumes');
    assert.equal(m3.seen.state.lineageId, 'LcR34', 'real city back');
    assert.ok(m3.seen.state.tick >= 500);
    assert.ok((onDisk?.snapshotTick ?? 0) >= p2, 'scratch progress kept on disk');
    assert.ok(rows3.includes(sid), 'DURABILITY: the scratch city stays listed after the real city comes back');
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});

test('R3-5 (scratch restored across a BUILD change): what the player is shown', async () => {
  const dom = await freshEnv();
  try {
    const { CURRENT_LINEAGE_KEY, readCurrentLineageId } = await import('../src/sim/replay.ts');
    const storage = dom.window.localStorage as unknown as Storage;
    await seedCity(storage, 'LbR35', 4_000_000, 700, new Date('2026-10-31T23:00:00Z'));
    storage.setItem(CURRENT_LINEAGE_KEY, 'LxR35');
    const m = await mountProvider(dom);
    const sid = m.seen.state.lineageId;
    await playTicks(m, 25);
    assert.ok(await m.seen.ctx.saveGame());
    await closeAllRoots();
    const { restampSavepointsBuildVersion, readAllSavepoints } = await import('../src/sim/replay.ts');
    restampSavepointsBuildVersion(storage as any, 'old-build-r35', sid);
    const restamped = readAllSavepoints(storage as any, new Date(), sid).filter((sp: any) => sp.buildVersion === 'old-build-r35').length;
    const m2 = await mountProvider(dom);
    const body = m2.container.ownerDocument.body.textContent ?? '';
    const hasPrompt = /new build|Rebuild/i.test(body);
    await closeAllRoots();
    const after = readAllSavepoints(storage as any, new Date(), sid).map((sp: any) => sp.buildVersion);
    console.log('R3-5 restamped', restamped, 'boot', m2.seen.state.lineageId, m2.seen.state.tick, 'rebuild prompt shown', hasPrompt, 'pointer', readCurrentLineageId(storage as any), 'stamps after boot', JSON.stringify(after));
    assert.ok(restamped > 0, 'premise');
    assert.equal(m2.seen.state.lineageId, sid);
    assert.ok(m2.seen.state.tick >= 25, 'scratch progress kept across the build change');
    assert.equal(readCurrentLineageId(storage as any), 'LxR35', 'pointer untouched');
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});

// BUG-1156 r4 REWRITE (lead structural ruling): the capped metropolis.scratchHistory list
// was DELETED, so R3-6's original premise (history cap + malformed history) no longer exists.
// Replaced by the equivalent hostile-input contract for the durable scratch stamp: a
// malformed `scratch` value on a savepoint reads as false (fail closed), a boolean true reads
// as scratch, and the label can never depend on any capped side list.
test('R3-6 (r4 rewrite: durable scratch stamp): malformed stamps read false; true labels scratch; no capped history exists', async () => {
  const dom = await freshEnv();
  try {
    const R: any = await import('../src/sim/replay.ts');
    const storage = dom.window.localStorage as unknown as Storage;
    assert.equal(R.readScratchHistory, undefined, 'the capped scratch history is gone');
    const { initialState } = await import('../src/sim/engine.ts');
    const cases: Array<[string, unknown, boolean]> = [['Lr36a', true, true], ['Lr36b', 'true', false], ['Lr36c', 1, false], ['Lr36d', undefined, false], ['Lr36e', null, false]];
    for (const [id, stamp, expected] of cases) {
      const sp: any = R.createSavepoint({ ...initialState(), lineageId: id, tick: 5 }, [], new Date('2026-10-31T20:00:00Z'), 'b', null);
      delete sp.scratch;
      if (stamp !== undefined) sp.scratch = stamp;
      assert.ok(R.persistSavepoint(storage as any, sp));
      const row = R.listCities(storage as any, null).find((c: any) => c.lineageId === id);
      assert.ok(row, id + ' listed');
      assert.equal(row.scratch, expected, `stamp ${JSON.stringify(stamp)}`);
    }
  } finally {
    dom.window.close();
  }
});

test('R3-7 (10 withhold episodes): evicted ex-scratch rows lose their [scratch] label - does the real city still rank first?', async () => {
  const dom = await freshEnv();
  try {
    const { CURRENT_LINEAGE_KEY, adoptLineage, listRecoverableLineages } = await import('../src/sim/replay.ts');
    const storage = dom.window.localStorage as unknown as Storage;
    await seedCity(storage, 'LbR37', 4_000_000, 700, new Date('2026-10-31T20:00:00Z'));
    const ids: string[] = [];
    for (let ep = 0; ep < 10; ep++) {
      storage.setItem(CURRENT_LINEAGE_KEY, 'LxR37ep' + ep);
      const m = await mountProvider(dom);
      ids.push(m.seen.state.lineageId);
      assert.ok(await m.seen.ctx.saveGame());
      await closeAllRoots();
      assert.equal(adoptLineage(storage as any, 'LbR37'), true);
    }
    storage.setItem(CURRENT_LINEAGE_KEY, 'LxR37final');
    const m = await mountProvider(dom);
    const listed = listRecoverableLineages(storage as any, 'LxR37final', new Date(), m.seen.state.lineageId);
    const unlabelled = listed.filter((l) => !l.scratch).map((l) => l.lineageId);
    console.log('R3-7 listed', listed.length, 'unlabelled', JSON.stringify(unlabelled), 'order', JSON.stringify(listed.map((l) => `${l.lineageId}:${l.scratch ? 's' : 'r'}:${l.savedAt}`)));
    assert.equal(listed[0].lineageId, 'LbR37', 'real city ranks first');
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});

test('R3-8 (StrictMode scratch RESTORE boot): one restore, MET-V868 once, no boot-time save churn, pointer untouched', async () => {
  const dom = await freshEnv();
  try {
    const { CURRENT_LINEAGE_KEY, readCurrentLineageId, readAllSavepoints } = await import('../src/sim/replay.ts');
    const { recentErrors } = await import('../src/sim/backend.ts');
    const storage = dom.window.localStorage as unknown as Storage;
    await seedCity(storage, 'LbR38', 4_000_000, 700, new Date('2026-10-31T23:00:00Z'));
    storage.setItem(CURRENT_LINEAGE_KEY, 'LxR38');
    const m = await mountProvider(dom);
    const sid = m.seen.state.lineageId;
    await playTicks(m, 20);
    assert.ok(await m.seen.ctx.saveGame());
    await closeAllRoots();
    const before = readAllSavepoints(storage as any, new Date(), sid).map((s: any) => `${s.snapshotTick}|${s.saveSeq}`);
    const { resetBootRefusalGuardForTests } = await import('../src/sim/store.tsx');
    resetBootRefusalGuardForTests();
    const v0 = v868For(recentErrors, 'LbR38');
    const m2 = await mountProvider(dom, true);
    const v1 = v868For(recentErrors, 'LbR38');
    const after = readAllSavepoints(storage as any, new Date(), sid).map((s: any) => `${s.snapshotTick}|${s.saveSeq}`);
    console.log('R3-8 strict boot', m2.seen.state.lineageId, m2.seen.state.tick, 'V868 delta', v1 - v0, 'saves before', JSON.stringify(before), 'after', JSON.stringify(after));
    assert.equal(m2.seen.state.lineageId, sid);
    assert.ok(m2.seen.state.tick >= 20);
    assert.equal(v1 - v0, 1, 'MET-V868 once');
    assert.equal(readCurrentLineageId(storage as any), 'LxR38');
    // Boot-time save churn is judged against the CONTROLS in R3-8c (normal-lineage restore, strict and not).
    (globalThis as any).__r38ScratchStrictChurn = after.length - before.length;
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});

test('R3-8c (controls): boot-time save churn for scratch non-strict, real strict, real non-strict - scratch must not exceed the real-city baseline', async () => {
  const results: Record<string, number> = {};
  for (const [label, scratch, strict] of [['scratch-nonstrict', true, false], ['real-strict', false, true], ['real-nonstrict', false, false]] as const) {
    const dom = await freshEnv();
    try {
      const { CURRENT_LINEAGE_KEY, readAllSavepoints } = await import('../src/sim/replay.ts');
      const storage = dom.window.localStorage as unknown as Storage;
      if (scratch) {
        await seedCity(storage, 'LbR38c', 4_000_000, 700, new Date('2026-10-31T23:00:00Z'));
        storage.setItem(CURRENT_LINEAGE_KEY, 'LxR38c');
      }
      const m = await mountProvider(dom);
      const lid = m.seen.state.lineageId;
      await playTicks(m, 20);
      assert.ok(await m.seen.ctx.saveGame());
      await closeAllRoots();
      const before = readAllSavepoints(storage as any, new Date(), lid).length;
      const m2 = await mountProvider(dom, strict);
      assert.equal(m2.seen.state.lineageId, lid, `${label}: restored`);
      assert.ok(m2.seen.state.tick >= 20, `${label}: progress`);
      results[label] = readAllSavepoints(storage as any, new Date(), lid).length - before;
    } finally {
      await closeAllRoots();
      dom.window.close();
    }
  }
  const scratchStrict = (globalThis as any).__r38ScratchStrictChurn;
  console.log('R3-8c churn', JSON.stringify({ ...results, 'scratch-strict': scratchStrict }));
  if (typeof scratchStrict === 'number') assert.ok(scratchStrict <= results['real-strict'], 'scratch strict churn <= real strict churn');
  assert.ok(results['scratch-nonstrict'] <= results['real-nonstrict'], 'scratch non-strict churn <= real non-strict churn');
});
