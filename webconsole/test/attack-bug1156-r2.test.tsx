// attack-bug1156-r2.test.tsx — BUG-1156 independent destructive round r2
// (attacker: opus-round-r2-bug1156). Probes against the r2 rework: single
// reused scratch id, recoverableAfterRestore, per-entry validation, F3.
// Tests named R2-*-DEFECT assert the CURRENT (defective) shape where a
// finding was made, so a fix flips them deliberately.

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

// BUG-1156 r3 FLIP (R2-1, deliberate): this test pinned the r2 defect (scratch progress lost on reload). The r3 fix restores the stored
// scratch city from its newest valid save, so every DEFECT assertion below is inverted to pin the FIX strictly: the reload resumes at the
// played tick, and the newest scratch save is not older than the played one. All three red on the r2 build (fresh boot under reused id).
test('R2-1 FIXED (live shape, scratch play across a reload): 60 ticks in scratch + save, reload, save again - the played scratch city RESUMES', async () => {
  const dom = await freshEnv();
  try {
    const { CURRENT_LINEAGE_KEY, readAllSavepoints, mostRecentSavepoint, listRecoverableLineages, SCRATCH_LINEAGE_KEY } = await import('../src/sim/replay.ts');
    const storage = dom.window.localStorage as unknown as Storage;
    await seedCity(storage, 'LbR21', 4_000_000, 700, new Date('2026-10-31T23:00:00Z'));
    storage.setItem(CURRENT_LINEAGE_KEY, 'LxR21');
    const m = await mountProvider(dom);
    const sid = m.seen.state.lineageId;
    await playTicks(m, 60);
    const playedTick = m.seen.state.tick;
    assert.ok(await m.seen.ctx.saveGame());
    await closeAllRoots();
    const m2 = await mountProvider(dom);
    assert.equal(m2.seen.state.lineageId, sid, 'same scratch id reused');
    const bootTick = m2.seen.state.tick;
    const rendered = Array.from(m2.container.querySelectorAll('[data-lineage]')).map((e) => e.getAttribute('data-lineage'));
    console.log('R2-1 after reload: running tick', bootTick, 'played', playedTick, 'banner rows', JSON.stringify(rendered));
    assert.ok(await m2.seen.ctx.saveGame());
    const newest = mostRecentSavepoint(readAllSavepoints(storage as any, new Date(), sid));
    console.log('R2-1 newest scratch save tick', newest?.snapshotTick, 'scratch key', storage.getItem(SCRATCH_LINEAGE_KEY));
    assert.ok(playedTick >= 60, 'premise: played 60 ticks');
    assert.ok(bootTick >= playedTick, `FIX: reload restores the scratch city (boot ${bootTick} vs played ${playedTick})`);
    assert.ok(!rendered.includes(sid), 'the running scratch city is never self-listed');
    assert.ok(rendered.includes('LbR21'), 'the real city is still offered');
    assert.ok(newest!.snapshotTick >= playedTick, 'FIX: newest save under the scratch id is the played city, not a fresh one');
    const listedTick = listRecoverableLineages(storage as any, 'LbR21').find((l) => l.lineageId === sid)?.snapshotTick;
    assert.ok((listedTick ?? 0) >= playedTick, 'FIX: after adopting B the scratch row shows the played tick');
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});

// BUG-1156 r3 FLIP (R2-2, deliberate): pinned the amnesia loop (every reload restarts at tick ~1). With scratch restore the city resumes
// after its first autosave, so boot ticks must GROW (>=40, >=80) - red on the r2 build. The banner must also state the situation
// (corrupt save listed as kept) even though zero rows are adoptable.
test('R2-2 FIXED (corrupt current, NO other lineage): the scratch city resumes on every reload; banner explains the kept corrupt save', async () => {
  const dom = await freshEnv();
  try {
    const { createSavepoint, persistSavepoint, CURRENT_LINEAGE_KEY, readCurrentLineageId } = await import('../src/sim/replay.ts');
    const { initialState } = await import('../src/sim/engine.ts');
    const storage = dom.window.localStorage as unknown as Storage;
    const bad: any = { ...initialState(), lineageId: 'LcurR22', buildings: [{ id: 1, spec: 'res_hut', x: 5, y: 5 }, { id: 1, spec: 'res_hut', x: 8, y: 8 }] };
    assert.ok(persistSavepoint(storage as any, createSavepoint(bad, [], new Date('2026-10-31T23:00:00Z'), 'test-build', null)));
    storage.setItem(CURRENT_LINEAGE_KEY, 'LcurR22');
    const ticks: number[] = [];
    let banner = 0;
    const notes: string[] = [];
    for (let n = 0; n < 3; n++) {
      const m = await mountProvider(dom);
      ticks.push(m.seen.state.tick);
      banner += m.container.querySelectorAll('[data-lineage]').length;
      notes.push(m.container.querySelector('.lineage-recovery-banner')?.textContent ?? '');
      await playTicks(m, 40);
      assert.ok(await m.seen.ctx.saveGame());
      await closeAllRoots();
    }
    console.log('R2-2 boot ticks', JSON.stringify(ticks), 'banner rows total', banner, 'pointer', readCurrentLineageId(storage as any));
    assert.equal(readCurrentLineageId(storage as any), 'LcurR22');
    assert.ok(ticks[0] < 40, 'first boot is a fresh scratch city');
    assert.ok(ticks[1] >= 40, `FIX: second boot resumes the 40 played ticks (got ${ticks[1]})`);
    assert.ok(ticks[2] >= 80, `FIX: third boot resumes 80 played ticks (got ${ticks[2]})`);
    assert.equal(banner, 0, 'no adoptable row exists (the corrupt save is not adoptable; the running scratch is never self-listed)');
    for (const [i, n] of notes.entries()) {
      assert.ok(/unrecoverable/.test(n) && /KEPT/.test(n), `boot ${i}: banner states the corrupt save is kept (got: ${n.slice(0, 120)})`);
    }
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});

test('R2-3 (adopt scratch, then N reloads): real city offered on EVERY later boot', async () => {
  const dom = await freshEnv();
  try {
    const { CURRENT_LINEAGE_KEY, readCurrentLineageId, SCRATCH_LINEAGE_KEY, adoptLineage } = await import('../src/sim/replay.ts');
    const storage = dom.window.localStorage as unknown as Storage;
    await seedCity(storage, 'LbR23', 4_000_000, 700, new Date('2026-10-31T23:00:00Z'));
    storage.setItem(CURRENT_LINEAGE_KEY, 'LxR23');
    const m = await mountProvider(dom);
    const sid = m.seen.state.lineageId;
    await playTicks(m, 30);
    assert.ok(await m.seen.ctx.saveGame());
    await closeAllRoots();
    assert.equal(adoptLineage(storage as any, sid), true);
    for (let n = 0; n < 4; n++) {
      const mm = await mountProvider(dom);
      assert.equal(mm.seen.state.lineageId, sid, `boot ${n}: scratch restored`);
      assert.ok(mm.seen.state.tick >= 30, `boot ${n}: scratch progress restored`);
      assert.ok(mm.container.querySelector('[data-lineage="LbR23"]'), `boot ${n}: real city still offered`);
      assert.equal(storage.getItem(SCRATCH_LINEAGE_KEY), sid);
      if (n === 1) {
        await playTicks(mm, 5);
        assert.ok(await mm.seen.ctx.saveGame());
      }
      await closeAllRoots();
    }
    assert.equal(readCurrentLineageId(storage as any), sid);
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});

// BUG-1156 r3 FLIP (R2-4, deliberate): pinned unlabelled stale ex-scratch rows. The fix keeps a bounded scratch-id history and labels every
// historic scratch row [scratch], ranked after real cities; the last assertion inverts from "no row is scratch" to "exactly the three
// stale rows are". Red when the history labelling is removed (mutation b).
test('R2-4 FIXED (stale ex-scratch rows across withhold episodes are labelled [scratch] and ranked last)', async () => {
  const dom = await freshEnv();
  try {
    const { CURRENT_LINEAGE_KEY, adoptLineage, listRecoverableLineages } = await import('../src/sim/replay.ts');
    const storage = dom.window.localStorage as unknown as Storage;
    await seedCity(storage, 'LbR24', 4_000_000, 700, new Date('2026-10-31T20:00:00Z'));
    const scratchIds: string[] = [];
    for (let ep = 0; ep < 3; ep++) {
      storage.setItem(CURRENT_LINEAGE_KEY, 'LxR24ep' + ep);
      const m = await mountProvider(dom);
      scratchIds.push(m.seen.state.lineageId);
      assert.ok(await m.seen.ctx.saveGame());
      await closeAllRoots();
      assert.equal(adoptLineage(storage as any, 'LbR24'), true);
      const mb = await mountProvider(dom);
      assert.equal(mb.seen.state.lineageId, 'LbR24');
      await closeAllRoots();
    }
    storage.setItem(CURRENT_LINEAGE_KEY, 'LxR24final');
    const m = await mountProvider(dom);
    const rows = Array.from(m.container.querySelectorAll('[data-lineage]')).map((e) => e.textContent);
    const listed = listRecoverableLineages(storage as any, 'LxR24final', new Date(), m.seen.state.lineageId);
    console.log('R2-4 distinct scratch ids', new Set(scratchIds).size, 'rows', JSON.stringify(rows));
    assert.equal(new Set(scratchIds).size, 3, 'one new scratch lineage per withhold episode');
    assert.equal(listed.length, 4, '3 stale ex-scratch rows + B');
    // Ranking holds in this flow: the clean restore of B re-persists B, so B is newer than every ex-scratch row.
    assert.equal(listed[0].lineageId, 'LbR24', 'real city ranks first when it was the last city run');
    assert.deepEqual(listed.map((l) => l.scratch), [false, true, true, true], 'FIX: the three stale rows are labelled scratch and sorted after the real city');
    assert.deepEqual(new Set(listed.filter((l) => l.scratch).map((l) => l.lineageId)), new Set(scratchIds), 'the labelled rows are exactly the minted scratch ids');
    const rendered = rows.filter((t) => (t ?? '').startsWith('[scratch] '));
    assert.equal(rendered.length, 3, 'FIX: the banner renders the [scratch] label on all three stale rows');
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});

test('R2-5 (two tabs while withheld): both tabs run fresh cities under ONE scratch lineage id', async () => {
  const dom = await freshEnv();
  try {
    const { CURRENT_LINEAGE_KEY } = await import('../src/sim/replay.ts');
    const storage = dom.window.localStorage as unknown as Storage;
    await seedCity(storage, 'LbR25', 4_000_000, 700, new Date('2026-10-31T23:00:00Z'));
    storage.setItem(CURRENT_LINEAGE_KEY, 'LxR25');
    const a = await mountProvider(dom);
    const sidA = a.seen.state.lineageId;
    const div = dom.window.document.createElement('div');
    div.id = 'root2';
    dom.window.document.body.appendChild(div);
    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimProvider, useSim } = await import('../src/sim/store.tsx');
    const seenB: any = {};
    function ProbeB() {
      const c = useSim();
      seenB.state = c.state;
      return null;
    }
    const rootB = createRoot(div);
    await act(async () => {
      rootB.render(React.default.createElement(SimProvider, { children: React.default.createElement(ProbeB) }));
    });
    openRoots.push({ root: rootB, act });
    console.log('R2-5 tabA', sidA, 'tabB', seenB.state?.lineageId);
    assert.equal(seenB.state.lineageId, sidA, 'two independent fresh cities share one lineage namespace (BUG-687 two-writer shape)');
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});

test('R2-6 (StrictMode withheld boot): exactly one scratch id, MET-V868 once', async () => {
  const dom = await freshEnv();
  try {
    const { CURRENT_LINEAGE_KEY, SCRATCH_LINEAGE_KEY, readCurrentLineageId } = await import('../src/sim/replay.ts');
    const { recentErrors } = await import('../src/sim/backend.ts');
    const storage = dom.window.localStorage as unknown as Storage;
    await seedCity(storage, 'LbR26s', 4_000_000, 700, new Date('2026-10-31T23:00:00Z'));
    storage.setItem(CURRENT_LINEAGE_KEY, 'LxR26s');
    const m = await mountProvider(dom, true);
    const n = recentErrors()
      .filter((e: any) => e.code === 'MET-V868' && String(e.msg ?? '').includes('LbR26s'))
      .reduce((s: number, e: any) => s + (e.count ?? 1), 0);
    const ids = new Set<string>();
    for (let i = 0; i < storage.length; i++) {
      const k = storage.key(i)!;
      const mm = /^metropolis\.savepoint\.([^.]+)\./.exec(k);
      if (mm) ids.add(mm[1]);
    }
    console.log('R2-6 running', m.seen.state.lineageId, 'key', storage.getItem(SCRATCH_LINEAGE_KEY), 'V868', n);
    assert.equal(m.seen.state.lineageId, storage.getItem(SCRATCH_LINEAGE_KEY));
    assert.equal(n, 1);
    assert.equal(readCurrentLineageId(storage as any), 'LxR26s');
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});

test('R2-7 (F3 UI path): Adopt click with a refusing pointer write records the failure, keeps the banner, does not reload', async () => {
  const dom = await freshEnv();
  try {
    const { CURRENT_LINEAGE_KEY, readCurrentLineageId } = await import('../src/sim/replay.ts');
    const { recentErrors } = await import('../src/sim/backend.ts');
    const storage = dom.window.localStorage as unknown as Storage;
    await seedCity(storage, 'LbR27', 4_000_000, 700, new Date('2026-10-31T23:00:00Z'));
    storage.setItem(CURRENT_LINEAGE_KEY, 'LxR27');
    const m = await mountProvider(dom);
    const proto = Object.getPrototypeOf(storage);
    const origSet = proto.setItem;
    proto.setItem = function (k: string, v: string) {
      if (k === CURRENT_LINEAGE_KEY) throw new Error('QuotaExceededError');
      return origSet.call(this, k, v);
    };
    try {
      await clickButton(m, dom, m.container.querySelector('[data-lineage="LbR27"] button'));
    } finally {
      proto.setItem = origSet;
    }
    const adoptErr = recentErrors().filter((e: any) => e.code === 'MET-V868' && String(e.msg ?? '').includes('Could not adopt lineage LbR27'));
    console.log('R2-7 adopt errors', adoptErr.length);
    assert.equal(readCurrentLineageId(storage as any), 'LxR27');
    assert.ok(adoptErr.length >= 1, 'failure recorded');
    assert.ok(m.container.querySelector('[data-lineage="LbR27"]'), 'banner stays so the player can retry');
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});
