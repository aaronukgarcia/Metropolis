// bug1156-idb-reachability.test.tsx - BUG-1156 r5 (F2): a city that lives ONLY in IndexedDB (the
// quota-overflow `metropolis.savepoint.<lineage>.idbOnly` key, or a mirrored slot whose localStorage
// copy is gone) is listed in File -> Cities and RESTORABLE through the IDB-primary boot path.
// Part 1 is the end-to-end proof; part 2 is a 120-seed two-tier reachability fuzzer with an
// "evict to IDB-only" operation.

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
  // jsdom has no navigation: make reload a recorded no-op (the fuzzer remounts itself).
  try {
    Object.defineProperty(window, 'location', { value: { ...window.location, href: 'http://localhost/', reload() { (globalThis as any).__r5reloads = ((globalThis as any).__r5reloads ?? 0) + 1; } }, configurable: true });
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

function mulberry32(a: number) {
  return () => {
    a |= 0; a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

type Mounted = { seen: { ctx: any; state: any }; root: any; act: any; container: HTMLElement };

async function mount(dom: JSDOM, strict: boolean): Promise<Mounted> {
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
  await sleep(150);
  return { seen, root, act, container };
}

async function unmount(m: Mounted | null) {
  if (!m) return;
  try {
    await m.act(async () => m.root.unmount());
  } catch { /* gone */ }
}

async function click(m: Mounted, el: Element | null | undefined, dom: JSDOM) {
  assert.ok(el, 'click target exists');
  await m.act(async () => {
    el!.dispatchEvent(new (dom.window as any).MouseEvent('click', { bubbles: true }));
  });
}

/** Open File -> Cities in the RENDERED menu and return the rows; leaves the menu open. */
async function openCities(m: Mounted, dom: JSDOM): Promise<Element[]> {
  const fileBtn = Array.from(m.container.querySelectorAll('button')).find((b) => (b.textContent ?? '').trim() === 'File');
  await click(m, fileBtn, dom);
  const citiesBtn = Array.from(m.container.querySelectorAll('button')).find((b) => /Cities/.test(b.textContent ?? ''));
  await click(m, citiesBtn, dom);
  return Array.from(m.container.querySelectorAll('[data-city]'));
}
async function closeMenu(m: Mounted, dom: JSDOM) {
  const fileBtn = Array.from(m.container.querySelectorAll('button')).find((b) => (b.textContent ?? '').trim() === 'File');
  if (fileBtn && /active/.test(fileBtn.className)) await click(m, fileBtn, dom);
}

async function seedCity(storage: Storage, lineageId: string, tick: number, savedAt: Date) {
  const { createSavepoint, persistSavepoint } = await import('../src/sim/replay.ts');
  const { initialState } = await import('../src/sim/engine.ts');
  const base: any = initialState();
  const city: any = { ...base, tick, lineageId, buildings: [...base.buildings, { id: 9001, spec: 'res_hut', x: 7, y: 7 }] };
  assert.ok(persistSavepoint(storage as any, createSavepoint(city, [], savedAt, 'test-build', null)), 'seed persist');
}


const IDB_TICK = 777;

async function encodedSave(lineageId: string, tick: number, savedAt: Date): Promise<string> {
  const { createSavepoint } = await import('../src/sim/replay.ts');
  const { initialState } = await import('../src/sim/engine.ts');
  const base: any = initialState();
  const city: any = { ...base, tick, lineageId, buildings: [...base.buildings, { id: 9001, spec: 'res_hut', x: 7, y: 7 }] };
  return JSON.stringify(createSavepoint(city, [], savedAt, 'test-build', null));
}

/** Put a city ONLY in IndexedDB under its .idbOnly overflow key (what a quota-overflowing big city does). */
async function seedIdbOnly(lineageId: string, tick: number, savedAt: Date) {
  const S = await import('../src/sim/saveStore.ts');
  const key = `metropolis.savepoint.${lineageId}.idbOnly`;
  const r = await S.getDefaultSaveStore().setItem(key, await encodedSave(lineageId, tick, savedAt));
  assert.ok(r.ok, 'idb seed');
}

test('R5-F2 end-to-end: a city seeded ONLY in IndexedDB is listed, Open restores it at its saved tick', async () => {
  const dom = await freshEnv();
  const R = await import('../src/sim/replay.ts');
  const storage = dom.window.localStorage as unknown as Storage;
  let m: Mounted | null = null;
  try {
    await seedCity(storage, 'LlocalReal', 100, new Date(Date.UTC(2026, 9, 31, 8)));
    await seedIdbOnly('LbigIdb', IDB_TICK, new Date(Date.UTC(2026, 9, 31, 9)));
    storage.setItem(R.CURRENT_LINEAGE_KEY, 'LlocalReal');
    m = await mount(dom, false);
    assert.equal(m.seen.state.lineageId, 'LlocalReal');
    const rows = await openCities(m, dom);
    await waitFor(() => !!m!.container.querySelector('[data-city="LbigIdb"]') && !m!.container.querySelector('[data-cities-loading]'), 4000);
    const big = m.container.querySelector('[data-city="LbigIdb"]');
    assert.ok(big, 'IDB-only city is in the rendered list (rows before union: ' + rows.length + ')');
    assert.ok(/t777/.test(big!.textContent ?? ''), 'row shows its tick: ' + big!.textContent);
    await click(m, big!.querySelector('button'), dom);
    assert.equal(R.readCurrentLineageId(storage as any), 'LbigIdb', 'pointer adopted');
    await unmount(m);
    m = await mount(dom, false);
    await waitFor(() => m!.seen.state.lineageId === 'LbigIdb' && m!.seen.state.tick === IDB_TICK, 8000);
    assert.equal(R.readCurrentLineageId(storage as any), 'LbigIdb', 'pointer still the opened city');
    assert.equal(storage.getItem(R.IDB_ADOPT_KEY), null, 'marker consumed');
  } finally {
    await unmount(m);
    dom.window.close();
  }
});

test('R5-F2 two-tier de-dup: newest valid save across tiers wins, one row per lineage, idbOnly only when no local save', async () => {
  const dom = await freshEnv();
  const S = await import('../src/sim/saveStore.ts');
  const storage = dom.window.localStorage as unknown as Storage;
  try {
    await seedCity(storage, 'Lboth', 100, new Date(Date.UTC(2026, 9, 31, 8)));
    const store = S.getDefaultSaveStore();
    await store.setItem('metropolis.savepoint.Lboth.idbOnly', await encodedSave('Lboth', 500, new Date(Date.UTC(2026, 9, 31, 10))));
    await store.setItem('metropolis.savepoint.Lbad.idbOnly', JSON.stringify({ snapshotTick: 'x', savedAt: 5 }));
    const rows = await S.listCitiesAll(storage as any, store, null, new Date(Date.UTC(2026, 9, 31, 11)));
    assert.deepEqual(rows.map((r) => r.lineageId), ['Lboth'], 'invalid IDB entry skipped, one row');
    assert.equal(rows[0].snapshotTick, 500, 'newest across tiers');
    assert.equal(rows[0].idbOnly, false, 'has a local save');
  } finally {
    dom.window.close();
  }
});

const SEEDS = Number(process.env.R5_SEEDS ?? 120);

test(`R5 fuzzer: ${SEEDS} seeds - every city with a valid save in EITHER tier is listed once (API + rendered), evict-to-IDB-only keeps it reachable, Open restores it`, async () => {
  const R = await import('../src/sim/replay.ts');
  const S = await import('../src/sim/saveStore.ts');
  const failures: string[] = [];
  let checks = 0;
  let evictions = 0;
  let opens = 0;
  for (let seed = 1; seed <= SEEDS; seed++) {
    const rnd = mulberry32(seed * 7919 + 3);
    const dom = await freshEnv();
    const storage = dom.window.localStorage as unknown as Storage;
    const store = S.getDefaultSaveStore();
    let m: Mounted | null = null;
    const fail = (w: string, why: string) => failures.push(`seed ${seed} ${w}: ${why}`);
    const truth = new Map<string, { tick: number; where: 'local' | 'idb' | 'both' }>();
    try {
      const ids = ['La' + seed, 'Lb' + seed, 'Lc' + seed, 'Ld' + seed];
      for (let i = 0; i < ids.length; i++) {
        const when = new Date(Date.UTC(2026, 9, 31, 8 + i));
        const tick = 100 + i * 50;
        const roll = i === 0 ? 0 : rnd(); // city 0 is always local: a pointer target exists
        if (roll < 0.34) {
          await seedCity(storage, ids[i], tick, when);
          truth.set(ids[i], { tick, where: 'local' });
        } else if (roll < 0.67) {
          await seedIdbOnly(ids[i], tick, when);
          truth.set(ids[i], { tick, where: 'idb' });
        } else {
          await seedCity(storage, ids[i], tick, when);
          // IDB copy OLDER than the local one: the newest-across-tiers winner is the local save (a NEWER IDB copy is
          // covered at API level by the de-dup test; the pre-existing boot swap owns that case, not the list).
          await seedIdbOnly(ids[i], tick - 25, new Date(when.getTime() - 60_000));
          truth.set(ids[i], { tick, where: 'both' });
        }
      }
      storage.setItem(R.CURRENT_LINEAGE_KEY, ids[0]);
      m = await mount(dom, rnd() < 0.3);
      const verify = async (w: string) => {
        checks++;
        const rows = await S.listCitiesAll(storage as any, store, m!.seen.state.lineageId ?? null);
        const got = rows.map((r) => r.lineageId);
        if (new Set(got).size !== got.length) fail(w, 'duplicate rows ' + got.join(','));
        for (const [id, t] of truth) {
          const row = rows.find((r) => r.lineageId === id);
          if (!row) fail(w, `city ${id} (${t.where}) missing from listCitiesAll`);
          else {
            // the running city may have autosaved/advanced; only check ticks for cities that are not running
            if (id !== m!.seen.state.lineageId && row.snapshotTick !== t.tick) fail(w, `city ${id} row tick ${row.snapshotTick} != newest across tiers ${t.tick}`);
            if (id !== m!.seen.state.lineageId && row.idbOnly !== (t.where === 'idb')) fail(w, `city ${id} idbOnly=${row.idbOnly} but where=${t.where}`);
          }
        }
        const fs = rows.findIndex((r) => r.scratch);
        if (fs >= 0 && rows.slice(fs).some((r) => !r.scratch)) fail(w, 'real row below scratch row');
        await openCities(m!, dom);
        await waitFor(() => !m!.container.querySelector('[data-cities-loading]'), 4000);
        const rIds = Array.from(m!.container.querySelectorAll('[data-city]')).map((r) => r.getAttribute('data-city'));
        for (const id of truth.keys()) if (!rIds.includes(id)) fail(w, `city ${id} missing from RENDERED File->Cities`);
        await closeMenu(m!, dom);
      };
      await verify('boot');
      for (let s = 0; s < 4; s++) {
        const op = rnd();
        if (op < 0.4) {
          // EVICT TO IDB-ONLY: copy a city's savepoint slots into IDB, then drop every localStorage slot
          const pickable = [...truth].filter(([id, t]) => t.where !== 'idb' && id !== m!.seen.state.lineageId);
          if (pickable.length) {
            const [id, t] = pickable[Math.floor(rnd() * pickable.length)];
            for (let slot = 0; slot < 3; slot++) {
              const k = R.savepointKey(slot, id);
              const raw = storage.getItem(k);
              if (raw) {
                await store.setItem(k, raw);
                storage.removeItem(k);
              }
            }
            truth.set(id, { tick: t.tick, where: 'idb' });
            evictions++;
          }
        } else if (op < 0.75) {
          await unmount(m);
          m = await mount(dom, rnd() < 0.3);
        } else {
          const openable = [...truth].filter(([id]) => id !== m!.seen.state.lineageId);
          if (openable.length) {
            const [id, t] = openable[Math.floor(rnd() * openable.length)];
            await openCities(m!, dom);
            await waitFor(() => !m!.container.querySelector('[data-cities-loading]'), 4000);
            const row = m!.container.querySelector(`[data-city="${id}"]`);
            if (!row || !row.querySelector('button')) fail('open', `no Open button for ${id}`);
            else {
              await click(m!, row.querySelector('button'), dom);
              await unmount(m);
              m = await mount(dom, rnd() < 0.3);
              opens++;
              try {
                await waitFor(() => m!.seen.state.lineageId === id && m!.seen.state.tick >= t.tick, 6000);
              } catch {
                fail('open', `opened ${id} (${t.where}, tick ${t.tick}) but booted ${m!.seen.state.lineageId} at tick ${m!.seen.state.tick}`);
              }
              // a restored IDB-only city is legitimately re-materialised locally by the boot's self-heal
              if (t.where === 'idb') truth.set(id, { tick: t.tick, where: 'both' });
            }
          }
        }
        await verify('step' + s);
      }
    } catch (e) {
      fail('exception', e instanceof Error ? (e.stack ?? e.message) : String(e));
    } finally {
      await unmount(m);
      dom.window.close();
    }
  }
  console.log(`R5 fuzzer: seeds=${SEEDS} checks=${checks} evictions=${evictions} opens=${opens} failures=${failures.length}`);
  if (failures.length) console.log(failures.slice(0, 15).join('\n'));
  assert.equal(failures.length, 0, failures.slice(0, 8).join(' | '));
  assert.ok(evictions > 20 && opens > 20, 'fuzzer exercised evictions and opens');
});
