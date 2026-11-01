// attack-bug1156-r5.test.tsx - BUG-1156 round r5 (independent attacker opus-round-r5-bug1156).
// Q-B: a NEWER IndexedDB copy of a city that also has an OLDER localStorage save - what does the boot land on,
// what does File -> Cities advertise, where does Open land. M1-M5: the metropolis.idbAdopt marker edges.
// S1: the FileMenu stale-result counter. FZ: two-tier fuzzer with "newer copy in IDB" + "corrupt IDB record" ops.
// Tests named DEFECT pin observed broken behaviour; they must flip deliberately when fixed.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';

function installJsdom() {
  const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', { url: 'http://localhost/', pretendToBeVisual: true });
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
  try {
    Object.defineProperty(window, 'location', { value: { ...window.location, href: 'http://localhost/', reload() {} }, configurable: true });
  } catch { /* keep jsdom's */ }
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

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
async function waitFor(p: () => boolean, ms: number) {
  const s = Date.now();
  while (!p()) {
    if (Date.now() - s > ms) throw new Error('waitFor timed out');
    await sleep(20);
  }
}
async function settle(p: () => boolean, ms: number): Promise<boolean> {
  try { await waitFor(p, ms); return true; } catch { return false; }
}

function mulberry32(a: number) {
  return () => {
    a |= 0; a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

type Mounted = { seen: { ctx: any; state: any }; root: any; act: any; container: HTMLElement };

async function mount(dom: JSDOM, strict: boolean, settleMs = 150): Promise<Mounted> {
  const React = (await import('react')).default;
  const { createRoot } = await import('react-dom/client');
  const { act } = await import('react-dom/test-utils');
  const { SimProvider, useSim } = await import('../src/sim/store.tsx');
  const { FileMenu } = await import('../src/components/FileMenu.tsx');
  const seen: { ctx: any; state: any } = { ctx: null, state: null };
  function Probe() {
    const ctx = useSim();
    seen.ctx = ctx;
    seen.state = ctx.state;
    return null;
  }
  const container = dom.window.document.getElementById('root')! as unknown as HTMLElement;
  const root = createRoot(container);
  const tree = React.createElement(SimProvider, { children: [React.createElement(Probe, { key: 'p' }), React.createElement(FileMenu, { key: 'f' })] });
  await act(async () => {
    root.render(strict ? React.createElement(React.StrictMode, null, tree) : tree);
  });
  await waitFor(() => !!seen.state, 8000);
  if (settleMs) await sleep(settleMs);
  return { seen, root, act, container };
}
async function unmount(m: Mounted | null) {
  if (!m) return;
  try { await m.act(async () => m.root.unmount()); } catch { /* gone */ }
}
async function click(m: Mounted, el: Element | null | undefined, dom: JSDOM) {
  assert.ok(el, 'click target exists');
  await m.act(async () => { el!.dispatchEvent(new (dom.window as any).MouseEvent('click', { bubbles: true })); });
}
async function openCities(m: Mounted, dom: JSDOM) {
  const fileBtn = Array.from(m.container.querySelectorAll('button')).find((b) => (b.textContent ?? '').trim() === 'File');
  await click(m, fileBtn, dom);
  const citiesBtn = Array.from(m.container.querySelectorAll('button')).find((b) => /Cities/.test(b.textContent ?? ''));
  await click(m, citiesBtn, dom);
  await waitFor(() => !m.container.querySelector('[data-cities-loading]'), 5000);
}
async function closeMenu(m: Mounted, dom: JSDOM) {
  const fileBtn = Array.from(m.container.querySelectorAll('button')).find((b) => (b.textContent ?? '').trim() === 'File');
  if (fileBtn && /active/.test(fileBtn.className)) await click(m, fileBtn, dom);
}
function rowTick(m: Mounted, id: string): number | null {
  const row = m.container.querySelector(`[data-city="${id}"]`);
  if (!row) return null;
  const mm = /t([\d,]+)/.exec(row.textContent ?? '');
  return mm ? Number(mm[1].replace(/,/g, '')) : null;
}

async function encodedSave(lineageId: string, tick: number, savedAt: Date, saveSeq?: number, extra = 0): Promise<string> {
  const { createSavepoint } = await import('../src/sim/replay.ts');
  const { initialState } = await import('../src/sim/engine.ts');
  const base: any = initialState();
  const added = [{ id: 9001, spec: 'res_hut', x: 7, y: 7 }];
  for (let i = 0; i < extra; i++) added.push({ id: 9100 + i, spec: 'res_hut', x: 20 + i, y: 9 });
  const city: any = { ...base, tick, lineageId, buildings: [...base.buildings, ...added] };
  return JSON.stringify(createSavepoint(city, [], savedAt, (await import('../src/sim/version.ts')).versionBadgeLabel(), null, saveSeq));
}
async function seedLocal(storage: Storage, lineageId: string, tick: number, savedAt: Date, saveSeq?: number) {
  const { persistSavepoint } = await import('../src/sim/replay.ts');
  assert.ok(persistSavepoint(storage as any, JSON.parse(await encodedSave(lineageId, tick, savedAt, saveSeq))), 'seed local');
}
async function seedIdb(key: string, lineageId: string, tick: number, savedAt: Date, saveSeq?: number, extra = 0) {
  const S = await import('../src/sim/saveStore.ts');
  const r = await S.getDefaultSaveStore().setItem(key, await encodedSave(lineageId, tick, savedAt, saveSeq, extra));
  assert.ok(r.ok, 'idb seed');
}
const T = (h: number) => new Date(Date.UTC(2026, 9, 31, h));

// ---------------------------------------------------------------- Q-B
for (const variant of [
  { name: 'idbOnly overflow key, saveSeq on both', key: (id: string) => `metropolis.savepoint.${id}.idbOnly`, seqLocal: 1 as number | undefined, seqIdb: 9 as number | undefined },
  { name: 'mirrored slot .1, saveSeq on both', key: (id: string) => `metropolis.savepoint.${id}.1`, seqLocal: 1, seqIdb: 9 },
  { name: 'idbOnly key, no saveSeq (tick+savedAt fallback)', key: (id: string) => `metropolis.savepoint.${id}.idbOnly`, seqLocal: undefined, seqIdb: undefined },
]) {
  test(`Q-B current city: newer IDB copy vs older local save (${variant.name}) - boot lands on the newer one and the list agrees`, async () => {
    const dom = await freshEnv();
    const R = await import('../src/sim/replay.ts');
    const storage = dom.window.localStorage as unknown as Storage;
    let m: Mounted | null = null;
    try {
      await seedLocal(storage, 'Lx', 100, T(8), variant.seqLocal);
      await seedIdb(variant.key('Lx'), 'Lx', 500, T(10), variant.seqIdb);
      storage.setItem(R.CURRENT_LINEAGE_KEY, 'Lx');
      m = await mount(dom, false);
      const landed = await settle(() => m!.seen.state.tick >= 500, 6000);
      await openCities(m, dom);
      const listed = rowTick(m, 'Lx');
      console.error(`[Q-B ${variant.name}] boot tick=${m.seen.state.tick} lineage=${m.seen.state.lineageId} listed=${listed}`);
      assert.equal(m.seen.state.lineageId, 'Lx');
      assert.ok(landed, `boot landed at tick ${m.seen.state.tick}, newer IDB copy is tick 500 (listed ${listed})`);
    } finally {
      await unmount(m);
      dom.window.close();
    }
  });
}

test('Q-B Open: a NON-current city whose newest save is in IDB (older local) - Open lands on the tick the list advertised', async () => {
  const dom = await freshEnv();
  const R = await import('../src/sim/replay.ts');
  const storage = dom.window.localStorage as unknown as Storage;
  let m: Mounted | null = null;
  try {
    await seedLocal(storage, 'Lhome', 50, T(7), 1);
    await seedLocal(storage, 'Ly', 100, T(8), 1);
    await seedIdb('metropolis.savepoint.Ly.idbOnly', 'Ly', 500, T(10), 9);
    storage.setItem(R.CURRENT_LINEAGE_KEY, 'Lhome');
    m = await mount(dom, false);
    await openCities(m, dom);
    const listed = rowTick(m, 'Ly');
    assert.equal(listed, 500, 'list advertises the newest tick across tiers');
    await click(m, m.container.querySelector('[data-city="Ly"] button'), dom);
    await unmount(m);
    m = await mount(dom, false);
    const landed = await settle(() => m!.seen.state.lineageId === 'Ly' && m!.seen.state.tick >= 500, 6000);
    console.error(`[Q-B open] listed=${listed} booted=${m.seen.state.lineageId}@${m.seen.state.tick}`);
    assert.ok(landed, `Open advertised t${listed} but landed ${m.seen.state.lineageId}@${m.seen.state.tick}`);
  } finally {
    await unmount(m);
    dom.window.close();
  }
});

// ---------------------------------------------------------------- markers
test('M1 stale marker: tab closed before the swap ran - the marker survives and the next boot opens the city', async () => {
  const dom = await freshEnv();
  const R = await import('../src/sim/replay.ts');
  const storage = dom.window.localStorage as unknown as Storage;
  let m: Mounted | null = null;
  try {
    await seedLocal(storage, 'Lhome', 50, T(7), 1);
    await seedIdb('metropolis.savepoint.Lbig.idbOnly', 'Lbig', 777, T(9), 3);
    storage.setItem(R.CURRENT_LINEAGE_KEY, 'Lhome');
    assert.ok(R.adoptLineage(storage as any, 'Lbig', true));
    m = await mount(dom, false, 0);
    await unmount(m); // closed immediately
    m = null;
    const markerAfterClose = storage.getItem(R.IDB_ADOPT_KEY);
    m = await mount(dom, false);
    const landed = await settle(() => m!.seen.state.lineageId === 'Lbig' && m!.seen.state.tick === 777, 6000);
    console.error(`[M1] markerAfterClose=${markerAfterClose} booted=${m.seen.state.lineageId}@${m.seen.state.tick} marker=${storage.getItem(R.IDB_ADOPT_KEY)}`);
    assert.ok(landed, `after an interrupted open the next boot is ${m.seen.state.lineageId}@${m.seen.state.tick} (marker after close: ${markerAfterClose})`);
    assert.equal(storage.getItem(R.IDB_ADOPT_KEY), null, 'marker consumed');
  } finally {
    await unmount(m);
    dom.window.close();
  }
});

test('M2 marker at a lineage that no longer exists: boot not wedged, other cities listed, a reload does not loop', async () => {
  const dom = await freshEnv();
  const R = await import('../src/sim/replay.ts');
  const storage = dom.window.localStorage as unknown as Storage;
  let m: Mounted | null = null;
  try {
    await seedLocal(storage, 'Lhome', 50, T(7), 1);
    storage.setItem(R.CURRENT_LINEAGE_KEY, 'Lgone');
    storage.setItem(R.IDB_ADOPT_KEY, 'Lgone');
    m = await mount(dom, false);
    await sleep(800);
    assert.notEqual(m.seen.state.lineageId, 'Lhome', 'sanity: the boot did not silently take Lhome');
    assert.equal(storage.getItem(R.IDB_ADOPT_KEY), null, 'marker consumed after the failed read');
    await openCities(m, dom);
    assert.ok(m.container.querySelector('[data-city="Lhome"] button'), 'Lhome openable');
    await click(m, m.container.querySelector('[data-city="Lhome"] button'), dom);
    await unmount(m);
    m = await mount(dom, false);
    assert.equal(m.seen.state.lineageId, 'Lhome', 'the real city opens');
    assert.equal(m.seen.state.tick, 50);
  } finally {
    await unmount(m);
    dom.window.close();
  }
});

test('M3 marker + Start Over before the swap lands: the Start Over city must not be overwritten by the late IDB swap', async () => {
  const dom = await freshEnv();
  const R = await import('../src/sim/replay.ts');
  const storage = dom.window.localStorage as unknown as Storage;
  let m: Mounted | null = null;
  try {
    await seedLocal(storage, 'Lhome', 50, T(7), 1);
    await seedIdb('metropolis.savepoint.Lbig.idbOnly', 'Lbig', 777, T(9), 3);
    storage.setItem(R.CURRENT_LINEAGE_KEY, 'Lhome');
    assert.ok(R.adoptLineage(storage as any, 'Lbig', true));
    m = await mount(dom, false, 0);
    const before = m.seen.state.lineageId;
    await m.act(async () => m!.seen.ctx.dispatch({ type: 'reset' }));
    const pointerAfterReset = R.readCurrentLineageId(storage as any);
    await sleep(2500);
    console.error(`[M3] bootLineage=${before} pointerAfterReset=${pointerAfterReset} final=${m.seen.state.lineageId}@${m.seen.state.tick} pointer=${R.readCurrentLineageId(storage as any)}`);
    assert.equal(m.seen.state.lineageId, R.readCurrentLineageId(storage as any), 'running city and pointer agree');
    assert.ok(m.seen.state.tick < 777, `the Start Over city was replaced by the IDB city (tick ${m.seen.state.tick})`);
  } finally {
    await unmount(m);
    dom.window.close();
  }
});

test('M4 marker under StrictMode double boot: opens once, marker consumed, MET-V868 not recorded', async () => {
  const dom = await freshEnv();
  const R = await import('../src/sim/replay.ts');
  const storage = dom.window.localStorage as unknown as Storage;
  let m: Mounted | null = null;
  try {
    await seedLocal(storage, 'Lhome', 50, T(7), 1);
    await seedIdb('metropolis.savepoint.Lbig.idbOnly', 'Lbig', 777, T(9), 3);
    storage.setItem(R.CURRENT_LINEAGE_KEY, 'Lhome');
    assert.ok(R.adoptLineage(storage as any, 'Lbig', true));
    m = await mount(dom, true);
    const landed = await settle(() => m!.seen.state.lineageId === 'Lbig' && m!.seen.state.tick === 777, 6000);
    assert.ok(landed, `StrictMode boot: ${m.seen.state.lineageId}@${m.seen.state.tick}`);
    assert.equal(storage.getItem(R.IDB_ADOPT_KEY), null);
    assert.equal(R.readCurrentLineageId(storage as any), 'Lbig');
  } finally {
    await unmount(m);
    dom.window.close();
  }
});

test('M5 two Opens in a row (IDB-only A then IDB-only B before reload): the LAST Open wins, nothing lost', async () => {
  const dom = await freshEnv();
  const R = await import('../src/sim/replay.ts');
  const storage = dom.window.localStorage as unknown as Storage;
  let m: Mounted | null = null;
  try {
    await seedLocal(storage, 'Lhome', 50, T(7), 1);
    await seedIdb('metropolis.savepoint.La.idbOnly', 'La', 300, T(8), 3);
    await seedIdb('metropolis.savepoint.Lb.idbOnly', 'Lb', 400, T(9), 3);
    storage.setItem(R.CURRENT_LINEAGE_KEY, 'Lhome');
    m = await mount(dom, false);
    await openCities(m, dom);
    await click(m, m.container.querySelector('[data-city="La"] button'), dom);
    await closeMenu(m, dom);
    await openCities(m, dom);
    await click(m, m.container.querySelector('[data-city="Lb"] button'), dom);
    await unmount(m);
    m = await mount(dom, false);
    const landed = await settle(() => m!.seen.state.lineageId === 'Lb' && m!.seen.state.tick === 400, 6000);
    assert.ok(landed, `two opens: ${m.seen.state.lineageId}@${m.seen.state.tick}`);
    await openCities(m, dom);
    for (const id of ['Lhome', 'La']) assert.ok(m.container.querySelector(`[data-city="${id}"] button`), `${id} still openable`);
    // and a local-backed Open after an IDB-only Open clears the marker
    await click(m, m.container.querySelector('[data-city="Lhome"] button'), dom);
    assert.equal(storage.getItem(R.IDB_ADOPT_KEY), null, 'local Open clears marker');
    await unmount(m);
    m = await mount(dom, false);
    assert.equal(m.seen.state.lineageId, 'Lhome');
  } finally {
    await unmount(m);
    dom.window.close();
  }
});

// ---------------------------------------------------------------- S1 stale-result counter
test('S1 FileMenu: a late listCitiesAll resolve from an earlier open never overwrites the newer open', async () => {
  const dom = installJsdom();
  try {
    const React = (await import('react')).default;
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimContext } = await import('../src/sim/simContext.ts');
    const { FileMenu } = await import('../src/components/FileMenu.tsx');
    const row = (id: string, tick: number) => ({ lineageId: id, snapshotTick: tick, savedAt: '2026-10-31T00:00:00.000Z', scratch: false, label: 'City ' + id });
    const resolvers: Array<(v: any) => void> = [];
    let syncReads = 0;
    const ctx: any = {
      state: { tick: 1 }, dispatch() {}, cityName: 'x', listSaves: () => [], listRecent: () => [], saveGame: () => true, saveGameAs: () => true,
      loadGame: () => true, loadNamed: () => true, renameCity: () => true, exportCity: async () => true, importCity: async () => true,
      listCities: () => { syncReads++; return [row('Lsync', 1)]; },
      listCitiesAll: () => new Promise((r) => resolvers.push(r)),
      adoptCity: () => {},
    };
    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);
    await act(async () => root.render(React.createElement(SimContext.Provider, { value: ctx }, React.createElement(FileMenu))));
    const renderReadsBefore = syncReads;
    const btn = (re: RegExp) => Array.from(container.querySelectorAll('button')).find((b) => re.test((b.textContent ?? '').trim()));
    const clickEl = async (el: any) => act(async () => el.dispatchEvent(new (dom.window as any).MouseEvent('click', { bubbles: true })));
    await clickEl(btn(/^File$/));
    await clickEl(btn(/Cities/));
    await clickEl(btn(/^File$/)); // close
    await clickEl(btn(/^File$/)); // reopen
    await clickEl(btn(/Cities/));
    assert.equal(resolvers.length, 2);
    await act(async () => resolvers[1]([row('Lnew', 2)]));
    await act(async () => resolvers[0]([row('Lstale', 3)]));
    const ids = Array.from(container.querySelectorAll('[data-city]')).map((e) => e.getAttribute('data-city'));
    assert.deepEqual(ids, ['Lnew'], 'stale resolve dropped');
    assert.equal(container.querySelector('[data-cities-loading]'), null, 'loading cleared by the live request');
    // re-render without opening: no storage read
    const reads = syncReads;
    await act(async () => root.render(React.createElement(SimContext.Provider, { value: { ...ctx } }, React.createElement(FileMenu))));
    assert.equal(syncReads, reads, 'no listCities call on render');
    assert.equal(renderReadsBefore, 0, 'no listCities call on first render');
    await act(async () => root.unmount());
  } finally {
    dom.window.close();
  }
});

// ---------------------------------------------------------------- FZ fuzzer
const SEEDS = Number(process.env.R5A_SEEDS ?? 60);
const STEPS = Number(process.env.R5A_STEPS ?? 6);

test(`FZ r5 attacker fuzzer: ${SEEDS} seeds x ${STEPS} steps with newer-IDB-copy + corrupt-IDB ops`, async () => {
  const R = await import('../src/sim/replay.ts');
  const S = await import('../src/sim/saveStore.ts');
  const failures: string[] = [];
  const counts = { checks: 0, newer: 0, corrupt: 0, opens: 0, reloads: 0, evict: 0 };
  for (let seed = 1; seed <= SEEDS; seed++) {
    const rnd = mulberry32(seed * 104729 + 17);
    const dom = await freshEnv();
    const storage = dom.window.localStorage as unknown as Storage;
    const store = S.getDefaultSaveStore();
    let m: Mounted | null = null;
    let hour = 8;
    // truth: every save copy per city, keyed by storage key, with tick (savedAt grows with tick)
    const local = new Map<string, number>(); // lineage -> local newest tick
    const idb = new Map<string, Map<string, number>>(); // lineage -> key -> tick
    const fail = (w: string, why: string) => failures.push(`seed ${seed} ${w}: ${why}`);
    const newest = (id: string): number | null => {
      const ts = [...(idb.get(id)?.values() ?? [])];
      if (local.has(id)) ts.push(local.get(id)!);
      return ts.length ? Math.max(...ts) : null;
    };
    const putIdb = async (id: string, key: string, tick: number) => {
      // realistic clock: a newer copy is written LATER than anything already on disk
      await seedIdb(key, id, tick, new Date(Date.now() + 1000 * hour++), tick);
      if (!idb.has(id)) idb.set(id, new Map());
      idb.get(id)!.set(key, tick);
    };
    try {
      const ids = ['La' + seed, 'Lb' + seed, 'Lc' + seed, 'Ld' + seed];
      for (let i = 0; i < ids.length; i++) {
        const tick = 100 + i * 50;
        const roll = i === 0 ? 0 : rnd();
        if (roll < 0.3) { await seedLocal(storage, ids[i], tick, new Date(Date.now() - 3600_000 + 1000 * hour++), tick); local.set(ids[i], tick); }
        else if (roll < 0.6) await putIdb(ids[i], `metropolis.savepoint.${ids[i]}.idbOnly`, tick);
        else { await seedLocal(storage, ids[i], tick, new Date(Date.now() - 3600_000 + 1000 * hour++), tick); local.set(ids[i], tick); await putIdb(ids[i], `metropolis.savepoint.${ids[i]}.idbOnly`, tick + 100); }
      }
      storage.setItem(R.CURRENT_LINEAGE_KEY, ids[0]);
      m = await mount(dom, rnd() < 0.3);
      const running = () => m!.seen.state.lineageId as string;
      const afterBoot = async (w: string, expectId: string | null) => {
        counts.checks++;
        const target = expectId ?? R.readCurrentLineageId(storage as any);
        const want = target ? newest(target) : null;
        if (target && want !== null) {
          const ok = await settle(() => running() === target && m!.seen.state.tick >= want, 6000);
          if (!ok) fail(w, `boot/open of ${target} (newest valid copy t${want}) landed ${running()}@${m!.seen.state.tick}`);
        }
        // the running city re-materialises locally (self-heal) - model it
        if (ids.includes(running())) local.set(running(), Math.max(local.get(running()) ?? 0, m!.seen.state.tick));
      };
      const verifyList = async (w: string) => {
        counts.checks++;
        const rows = await S.listCitiesAll(storage as any, store, running());
        const got = rows.map((r) => r.lineageId);
        if (new Set(got).size !== got.length) fail(w, 'duplicate rows');
        for (const id of ids) {
          const n = newest(id);
          const row = rows.find((r) => r.lineageId === id);
          if (n === null) continue; // model gap: the boot mirrors a running city's slots into IDB, untracked here
          if (!row) { fail(w, `${id} (newest t${n}) missing`); continue; }
          if (id !== running() && row.snapshotTick !== n) fail(w, `${id} row t${row.snapshotTick} != newest t${n}`);
        }
      };
      await afterBoot('boot', null);
      await verifyList('boot');
      for (let s = 0; s < STEPS; s++) {
        const op = rnd();
        const others = ids.filter((id) => id !== running() && newest(id) !== null);
        if (op < 0.2 && others.length) {
          // NEWER COPY IN IDB than local (a mirror that kept advancing while localStorage stuck)
          const id = others[Math.floor(rnd() * others.length)];
          await putIdb(id, rnd() < 0.5 ? `metropolis.savepoint.${id}.idbOnly` : R.savepointKey(Math.floor(rnd() * 3), id), (newest(id) ?? 0) + 200);
          counts.newer++;
        } else if (op < 0.35) {
          // CORRUPT an IDB record
          const all = [...idb].flatMap(([id, mk]) => [...mk.keys()].map((k) => [id, k] as const)).filter(([id]) => id !== running());
          if (all.length) {
            const [id, k] = all[Math.floor(rnd() * all.length)];
            await store.setItem(k, rnd() < 0.5 ? 'garbage{{not-a-save' : JSON.stringify({ snapshotTick: 'x' }));
            idb.get(id)!.delete(k);
            counts.corrupt++;
          }
        } else if (op < 0.45 && others.length) {
          // EVICT local slots of a city to IDB-only
          const cand = others.filter((id) => local.has(id));
          if (cand.length) {
            const id = cand[Math.floor(rnd() * cand.length)];
            for (let slot = 0; slot < 3; slot++) {
              const k = R.savepointKey(slot, id);
              const raw = storage.getItem(k);
              if (raw) {
                await store.setItem(k, raw);
                try { const t = R.decodeSavepointBytes(raw).snapshotTick; if (!idb.has(id)) idb.set(id, new Map()); idb.get(id)!.set(k, t); } catch { /* compressed */ }
                storage.removeItem(k);
              }
            }
            local.delete(id);
            counts.evict++;
          }
        } else if (op < 0.7) {
          await unmount(m);
          m = await mount(dom, rnd() < 0.3);
          counts.reloads++;
          await afterBoot('reload' + s, null);
        } else if (others.length) {
          const id = others[Math.floor(rnd() * others.length)];
          await openCities(m!, dom);
          const btn = m!.container.querySelector(`[data-city="${id}"] button`);
          const advertised = rowTick(m!, id);
          if (!btn) { fail('open' + s, `no Open for ${id}`); await closeMenu(m!, dom); continue; }
          if (advertised !== newest(id)) fail('open' + s, `${id} advertised t${advertised} newest t${newest(id)}`);
          await click(m!, btn, dom);
          await unmount(m);
          m = await mount(dom, rnd() < 0.3);
          counts.opens++;
          await afterBoot('open' + s, id);
        }
        await verifyList('step' + s);
      }
    } catch (e) {
      fail('exception', e instanceof Error ? (e.stack ?? e.message).slice(0, 400) : String(e));
    } finally {
      await unmount(m);
      dom.window.close();
    }
  }
  console.error(`[FZ] seeds=${SEEDS} steps=${STEPS} ${JSON.stringify(counts)} failures=${failures.length}`);
  if (failures.length) console.error(failures.slice(0, 25).join('\n'));
  assert.equal(failures.length, 0, failures.slice(0, 6).join(' | '));
});
