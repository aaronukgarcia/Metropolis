// attack-bug1156-r4.test.tsx - BUG-1156 round r4 (opus-round-r4-bug1156), INDEPENDENT reachability +
// progress fuzzer. Written from scratch (not the builder's property test): 4 real lineages, ops
// {play N (+maybe save), reload, adopt via rendered File -> Cities, adopt via rendered banner,
// Start Over, corrupt current save, evict a random savepoint key, dismiss banner}.
// After EVERY step: (a) every lineage with a valid save is in listCities AND in the RENDERED
// File -> Cities rows, exactly once; (b) no lineage's newest valid tick ever decreases except
// through this fuzzer's own sabotage of THAT lineage; (c) a reload/adopt that boots an existing
// lineage boots at >= its newest valid save tick; (d) real cities never rank below scratch.

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
    Object.defineProperty(window, 'location', { value: { ...window.location, href: 'http://localhost/', reload() { (globalThis as any).__r4reloads = ((globalThis as any).__r4reloads ?? 0) + 1; } }, configurable: true });
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

const SEED_FROM = Number(process.env.R4_SEED_FROM ?? 1);
const SEEDS = Number(process.env.R4_SEEDS ?? 30);
const STEPS = Number(process.env.R4_STEPS ?? 8);

test(`R4 fuzzer: seeds ${SEED_FROM}..${SEED_FROM + SEEDS - 1} x ${STEPS} steps - reachability (API + rendered) and no progress loss`, async () => {
  const R = await import('../src/sim/replay.ts');
  const failures: string[] = [];
  let steps = 0;
  const opCounts: Record<string, number> = {};
  for (let seed = SEED_FROM; seed < SEED_FROM + SEEDS; seed++) {
    const rnd = mulberry32(seed * 104729 + 17);
    const pick = <T,>(xs: T[]): T => xs[Math.floor(rnd() * xs.length)];
    const dom = await freshEnv();
    const storage = dom.window.localStorage as unknown as Storage;
    let m: Mounted | null = null;
    const fail = (where: string, why: string) => failures.push(`seed ${seed} ${where}: ${why}`);
    // oracle: newest valid tick per lineage (max snapshotTick over valid saves)
    const oracle = (): Map<string, { tick: number; newestTick: number }> => {
      const out = new Map<string, { tick: number; newestTick: number }>();
      for (const id of R.scanAllSavepointLineages(storage as any)) {
        const valid = R.readAllSavepoints(storage as any, new Date(), id).filter(
          (sp: any) => sp && typeof sp === 'object' && typeof sp.snapshotTick === 'number' && Number.isFinite(sp.snapshotTick) && typeof sp.savedAt === 'string',
        );
        if (!valid.length) continue;
        const newest = R.mostRecentSavepoint(valid)!;
        out.set(id, { tick: Math.max(...valid.map((s: any) => s.snapshotTick)), newestTick: newest.snapshotTick });
      }
      return out;
    };
    let baseline = new Map<string, number>();
    const scratchSeen = new Set<string>();
    try {
      const reals = ['La' + seed, 'Lb' + seed, 'Lc' + seed, 'Ld' + seed];
      for (let i = 0; i < reals.length; i++) await seedCity(storage, reals[i], 100 + i * 40, new Date(Date.UTC(2026, 9, 31, 8 + i)));
      const ptrRoll = rnd();
      storage.setItem(R.CURRENT_LINEAGE_KEY, ptrRoll < 0.5 ? pick(reals) : ptrRoll < 0.75 ? 'Ldangle' + seed : reals[0]);
      m = await mount(dom, rnd() < 0.3);
      const bootCheck = (where: string, expectId?: string) => {
        const st = m!.seen.state;
        const lid = st.lineageId as string;
        const o = oracle().get(lid);
        if (o && st.tick < o.newestTick) fail(where, `booted ${lid} at tick ${st.tick} < its newest valid save tick ${o.newestTick}`);
        if (expectId && lid !== expectId && oracle().has(expectId)) fail(where, `adopted ${expectId} but booted ${lid}`);
        if (lid !== R.readCurrentLineageId(storage as any)) scratchSeen.add(lid);
      };
      const verify = async (where: string, sabotaged: string | null) => {
        steps++;
        const o = oracle();
        const listed = m!.seen.ctx.listCities() as any[];
        const ids = listed.map((c) => c.lineageId);
        if (new Set(ids).size !== ids.length) fail(where, `duplicate rows ${JSON.stringify(ids)}`);
        for (const id of o.keys()) if (!ids.includes(id)) fail(where, `valid lineage ${id} missing from listCities`);
        for (const id of ids) if (!o.has(id)) fail(where, `listed ${id} without a valid save`);
        const fs = listed.findIndex((c) => c.scratch);
        if (fs >= 0 && listed.slice(fs).some((c) => !c.scratch)) fail(where, 'real row ranked below a scratch row');
        for (const c of listed) {
          if (reals.includes(c.lineageId) && c.scratch) fail(where, `real seeded city ${c.lineageId} labelled scratch`);
          if (scratchSeen.has(c.lineageId) && !reals.includes(c.lineageId) && !c.scratch) fail(where, `scratch city ${c.lineageId} lost its stamp`);
        }
        const rows = await openCities(m!, dom);
        const rIds = rows.map((r) => r.getAttribute('data-city'));
        for (const id of o.keys()) if (!rIds.includes(id)) fail(where, `valid lineage ${id} missing from RENDERED File->Cities`);
        const cur = rows.filter((r) => /\(current\)/.test(r.textContent ?? ''));
        for (const r of cur) if (r.querySelector('button')) fail(where, 'current row is openable');
        await closeMenu(m!, dom);
        // progress: newest valid tick never decreases except for the lineage we just sabotaged
        for (const [id, t] of baseline) {
          const now = o.get(id)?.tick;
          if (id === sabotaged) continue;
          if (now === undefined) fail(where, `lineage ${id} LOST every valid save (had tick ${t})`);
          else if (now < t) fail(where, `lineage ${id} newest valid tick decreased ${t} -> ${now}`);
        }
        baseline = new Map([...o].map(([k, v]) => [k, v.tick]));
      };
      bootCheck('boot');
      await verify('boot', null);
      for (let s = 0; s < STEPS; s++) {
        const op = pick(['play', 'play', 'reload', 'adoptCities', 'adoptBanner', 'startOver', 'corrupt', 'evict', 'dismiss']);
        opCounts[op] = (opCounts[op] ?? 0) + 1;
        const where = `step${s}:${op}`;
        let sabotaged: string | null = null;
        try {
          if (op === 'play') {
            const n = 1 + Math.floor(rnd() * 20);
            await m.act(async () => m!.seen.ctx.dispatch({ type: 'speed', speed: 0 }));
            for (let i = 0; i < n; i++) await m.act(async () => m!.seen.ctx.dispatch({ type: 'tick' }));
            if (rnd() < 0.7) assert.ok(await m.seen.ctx.saveGame(), 'saveGame (autosave stand-in)');
          } else if (op === 'reload') {
            await unmount(m);
            m = await mount(dom, rnd() < 0.3);
            bootCheck(where);
          } else if (op === 'adoptCities') {
            const rows = (await openCities(m, dom)).filter((r) => r.querySelector('button'));
            if (rows.length) {
              const row = pick(rows);
              const id = row.getAttribute('data-city')!;
              await click(m, row.querySelector('button'), dom);
              if (R.readCurrentLineageId(storage as any) !== id) fail(where, `Open did not set the pointer to ${id}`);
              await unmount(m);
              m = await mount(dom, rnd() < 0.3);
              bootCheck(where, id);
            } else await closeMenu(m, dom);
          } else if (op === 'adoptBanner') {
            const items = Array.from(m.container.ownerDocument.querySelectorAll('.lineage-recovery-banner li[data-lineage]'));
            if (items.length) {
              const li = pick(items);
              const id = li.getAttribute('data-lineage')!;
              await click(m, li.querySelector('button'), dom);
              await unmount(m);
              m = await mount(dom, rnd() < 0.3);
              bootCheck(where, id);
            }
          } else if (op === 'startOver') {
            await m.act(async () => m!.seen.ctx.dispatch({ type: 'reset' }));
            await sleep(50);
          } else if (op === 'corrupt') {
            const cur = m.seen.state.lineageId as string;
            for (let slot = 0; slot < R.SAVEPOINT_CAP; slot++) {
              const k = R.savepointKey(slot, cur);
              if (storage.getItem(k) !== null) storage.setItem(k, '{corrupt');
            }
            sabotaged = cur;
            await unmount(m);
            m = await mount(dom, rnd() < 0.3);
            bootCheck(where);
          } else if (op === 'evict') {
            const keys: string[] = [];
            for (let i = 0; i < storage.length; i++) {
              const k = storage.key(i)!;
              if (k.startsWith(R.SAVEPOINT_KEY_PREFIX + '.') && /\.\d+$/.test(k)) keys.push(k);
            }
            if (keys.length) {
              const k = pick(keys);
              const segs = k.slice(R.SAVEPOINT_KEY_PREFIX.length + 1).split('.');
              sabotaged = segs.length === 1 ? R.LEGACY_LINEAGE_ID : segs.slice(0, -1).join('.');
              storage.removeItem(k);
            }
          } else {
            const btn = Array.from(m.container.ownerDocument.querySelectorAll('.lineage-recovery-banner button')).find((b) => b.textContent === 'Dismiss');
            if (btn) await click(m, btn, dom);
          }
        } catch (e) {
          fail(where, `threw ${(e as Error).message}`);
        }
        await verify(where, sabotaged);
      }
    } finally {
      await unmount(m);
      dom.window.close();
    }
  }
  console.log(`R4 FUZZ seeds=${SEED_FROM}..${SEED_FROM + SEEDS - 1} steps=${steps} ops=${JSON.stringify(opCounts)} failures=${failures.length}`);
  for (const f of failures.slice(0, 40)) console.log('R4 FAIL ' + f);
  assert.deepEqual(failures.slice(0, 15), [], 'no reachability / progress failures');
});

test('R4-UI: 50 lineages render as 50 rows; odd ids/labels render as text (escaped), current not openable, list re-reads on each open', async () => {
  const dom = await freshEnv();
  const R = await import('../src/sim/replay.ts');
  const storage = dom.window.localStorage as unknown as Storage;
  let m: Mounted | null = null;
  try {
    const odd = 'L<img src=x onerror=alert(1)>"&\'';
    await seedCity(storage, odd, 7, new Date(Date.UTC(2026, 9, 31, 7)));
    for (let i = 0; i < 49; i++) await seedCity(storage, `Lmany${String(i).padStart(2, '0')}`, 10 + i, new Date(Date.UTC(2026, 9, 31, 9, i)));
    storage.setItem(R.CURRENT_LINEAGE_KEY, 'Lmany00');
    m = await mount(dom, false);
    assert.equal(m.seen.state.lineageId, 'Lmany00');
    let rows = await openCities(m, dom);
    assert.equal(rows.length, 50, '50 rows');
    assert.equal(m.container.querySelectorAll('[data-cities-list] img').length, 0, 'no injected element');
    const oddRow = rows.find((r) => r.getAttribute('data-city') === odd);
    assert.ok(oddRow, 'odd id row present with the raw id as attribute');
    const cur = rows.filter((r) => /\(current\)/.test(r.textContent ?? ''));
    assert.equal(cur.length, 1);
    assert.equal(cur[0].getAttribute('data-city'), 'Lmany00');
    assert.equal(cur[0].querySelector('button'), null, 'current row not openable');
    assert.equal(rows.filter((r) => r.querySelector('button')).length, 49, 'every other row openable');
    await closeMenu(m, dom);
    await seedCity(storage, 'LlateArrival', 3, new Date(Date.UTC(2026, 9, 31, 11)));
    rows = await openCities(m, dom);
    assert.equal(rows.length, 51, 're-read on open');
    await click(m, oddRow ? rows.find((r) => r.getAttribute('data-city') === odd)!.querySelector('button') : null, dom);
    assert.equal(R.readCurrentLineageId(storage as any), odd, 'Open on an odd-id row adopts exactly that id');
  } finally {
    await unmount(m);
    dom.window.close();
  }
});

test('R4-3: stamp lifecycle - scratch adopted then Start Over; real adopted then reload; never mislabelled', async () => {
  const dom = await freshEnv();
  const R = await import('../src/sim/replay.ts');
  const storage = dom.window.localStorage as unknown as Storage;
  let m: Mounted | null = null;
  try {
    await seedCity(storage, 'Lreal1', 200, new Date(Date.UTC(2026, 9, 31, 7)));
    storage.setItem(R.CURRENT_LINEAGE_KEY, 'LdeadPtr');
    m = await mount(dom, true); // StrictMode withheld boot -> scratch
    const scratch = m.seen.state.lineageId as string;
    assert.notEqual(scratch, 'Lreal1');
    assert.notEqual(scratch, 'LdeadPtr');
    for (let i = 0; i < 5; i++) await m.act(async () => m!.seen.ctx.dispatch({ type: 'tick' }));
    assert.ok(await m.seen.ctx.saveGame());
    const row = () => (m!.seen.ctx.listCities() as any[]).find((c) => c.lineageId === scratch);
    assert.equal(row()?.scratch, true, 'scratch stamped after first save');
    // adopt scratch, reload, keep stamp after another save
    assert.equal(R.adoptLineage(storage as any, scratch), true);
    await unmount(m);
    m = await mount(dom, true);
    assert.equal(m.seen.state.lineageId, scratch);
    for (let i = 0; i < 3; i++) await m.act(async () => m!.seen.ctx.dispatch({ type: 'tick' }));
    assert.ok(await m.seen.ctx.saveGame());
    assert.equal(row()?.scratch, true, 'adopted scratch keeps stamp');
    // Start Over from the adopted scratch: new city is real
    await m.act(async () => m!.seen.ctx.dispatch({ type: 'reset' }));
    await sleep(50);
    const fresh = m.seen.state.lineageId as string;
    assert.notEqual(fresh, scratch);
    for (let i = 0; i < 3; i++) await m.act(async () => m!.seen.ctx.dispatch({ type: 'tick' }));
    assert.ok(await m.seen.ctx.saveGame());
    const list = m.seen.ctx.listCities() as any[];
    assert.equal(list.find((c) => c.lineageId === fresh)?.scratch, false, 'Start Over city is real');
    assert.equal(list.find((c) => c.lineageId === scratch)?.scratch, true, 'old scratch keeps stamp and is reachable');
    assert.equal(list.find((c) => c.lineageId === 'Lreal1')?.scratch, false);
    // adopt real, reload, save: real stays unstamped
    assert.equal(R.adoptLineage(storage as any, 'Lreal1'), true);
    await unmount(m);
    m = await mount(dom, true);
    assert.equal(m.seen.state.lineageId, 'Lreal1');
    assert.ok(await m.seen.ctx.saveGame());
    assert.equal((m.seen.ctx.listCities() as any[]).find((c) => c.lineageId === 'Lreal1')?.scratch, false, 'real stays real');
    const order = (m.seen.ctx.listCities() as any[]).map((c) => c.scratch);
    assert.deepEqual(order, [...order].sort((a, b) => Number(a) - Number(b)), 'real first');
  } finally {
    await unmount(m);
    dom.window.close();
  }
});

test('R4-4: reload inside the first-autosave window of a brand-new scratch city loses only unsaved ticks (house norm)', async () => {
  const dom = await freshEnv();
  const R = await import('../src/sim/replay.ts');
  const storage = dom.window.localStorage as unknown as Storage;
  let m: Mounted | null = null;
  try {
    await seedCity(storage, 'Lreal2', 300, new Date(Date.UTC(2026, 9, 31, 7)));
    storage.setItem(R.CURRENT_LINEAGE_KEY, 'LdeadPtr2');
    m = await mount(dom, false);
    const scratch = m.seen.state.lineageId as string;
    for (let i = 0; i < 20; i++) await m.act(async () => m!.seen.ctx.dispatch({ type: 'tick' }));
    // no save: reload inside the window
    await unmount(m);
    m = await mount(dom, false);
    assert.equal(m.seen.state.lineageId, scratch, 'same scratch id resumed (no pile-up)');
    assert.ok(m.seen.state.tick < 20, 'unsaved ticks are lost (same as any city before its first autosave)');
    assert.ok((m.seen.ctx.listCities() as any[]).some((c) => c.lineageId === 'Lreal2'), 'real city still reachable');
    assert.equal(R.AUTOSAVE_INTERVAL_MS, 30000);
  } finally {
    await unmount(m);
    dom.window.close();
  }
});

test('R4-F2 probe: a city whose only save lives in IndexedDB (localStorage slot evicted / over quota) - is it in the Cities list?', async () => {
  const dom = await freshEnv();
  const R = await import('../src/sim/replay.ts');
  const S = await import('../src/sim/saveStore.ts');
  const storage = dom.window.localStorage as unknown as Storage;
  let m: Mounted | null = null;
  try {
    await seedCity(storage, 'LidbOnly', 900, new Date(Date.UTC(2026, 9, 31, 9)));
    await seedCity(storage, 'LlsReal', 100, new Date(Date.UTC(2026, 9, 31, 8)));
    const store = S.getDefaultSaveStore();
    const key = R.savepointKey(0, 'LidbOnly');
    assert.ok(await S.mirrorKeyFromLocalStorage(store, storage as any, key), 'mirrored into IDB');
    assert.ok((await store.getItem(key)) !== null);
    storage.removeItem(key); // localStorage eviction; the durable IDB copy survives
    storage.setItem(R.CURRENT_LINEAGE_KEY, 'LdanglingF2');
    m = await mount(dom, false);
    // BUG-1156 r5 FLIP: the two-tier union (localStorage + IndexedDB) lists the IDB-only city. The sync
    // localStorage-only listCities (the r4 build's only list) still cannot see it - that is the r4 defect.
    const syncListed = (m.seen.ctx.listCities() as any[]).map((c) => c.lineageId);
    assert.ok(!syncListed.includes('LidbOnly'), 'premise: the localStorage-only list cannot see it (this is what made r4 red)');
    const all = (await m.seen.ctx.listCitiesAll()) as any[];
    const listed = all.map((c) => c.lineageId);
    console.log(`R4-F2 idb-only city listed? ${listed.includes('LidbOnly')} listed=${JSON.stringify(listed)}`);
    assert.ok(listed.includes('LidbOnly'), 'BUG-1156 r5 FLIP: an IDB-only city is reachable from File -> Cities');
    assert.equal(listed.filter((x) => x === 'LidbOnly').length, 1, 'listed exactly once');
    assert.equal(all.find((c) => c.lineageId === 'LidbOnly').idbOnly, true);
  } finally {
    await unmount(m);
    dom.window.close();
  }
});

test('R4-F1 (FINDING, low): Open on a listed-but-unrestorable city forgets the scratch resume id - the played scratch city is NOT resumed, a fresh scratch is minted (still reachable via Cities)', async () => {
  const dom = await freshEnv();
  const R = await import('../src/sim/replay.ts');
  const storage = dom.window.localStorage as unknown as Storage;
  let m: Mounted | null = null;
  try {
    // a shape-valid save (finite tick, string savedAt) that restore refuses: grid larger than this build
    const { initialState } = await import('../src/sim/engine.ts');
    const sp: any = R.createSavepoint({ ...(initialState() as any), tick: 400, lineageId: 'LbigGrid' }, [], new Date(Date.UTC(2026, 9, 31, 7)), 'b', null);
    sp.gridW = 100000;
    sp.gridH = 100000;
    assert.ok(R.persistSavepoint(storage as any, sp));
    storage.setItem(R.CURRENT_LINEAGE_KEY, 'LbigGrid');
    m = await mount(dom, false);
    const s1 = m.seen.state.lineageId as string;
    assert.notEqual(s1, 'LbigGrid', 'refused -> scratch');
    await m.act(async () => m!.seen.ctx.dispatch({ type: 'speed', speed: 0 }));
    for (let i = 0; i < 30; i++) await m.act(async () => m!.seen.ctx.dispatch({ type: 'tick' }));
    assert.ok(await m.seen.ctx.saveGame());
    const rows = await openCities(m, dom);
    const big = rows.find((r) => r.getAttribute('data-city') === 'LbigGrid');
    assert.ok(big && big.querySelector('button'), 'the unrestorable city is listed AND openable');
    await click(m, big!.querySelector('button'), dom);
    await unmount(m);
    m = await mount(dom, false);
    const s2 = m.seen.state.lineageId as string;
    const listed = (m.seen.ctx.listCities() as any[]).map((c) => c.lineageId);
    console.log(`R4-F1 s1=${s1} s2=${s2} bootTick=${m.seen.state.tick} listed=${JSON.stringify(listed)}`);
    assert.ok(listed.includes(s1), 'played scratch city still reachable via Cities (principle holds)');
    // BUG-1156 r5 FLIP: a failed adopt keeps the scratch resume id, so the played scratch city is resumed
    // (not a fresh one), and the refusal reason is shown as a notice.
    assert.equal(s2, s1, 'BUG-1156 r5 FLIP: Open(unrestorable) -> reload RESUMES the played scratch city');
    assert.ok(m.seen.state.tick >= 30, 'BUG-1156 r5 FLIP: the screen shows the played 30-tick scratch city');
    assert.ok(/could not be restored|refused|grid/i.test(String(m.seen.state.placeNotice ?? '')), 'refusal reason shown as a notice: ' + String(m.seen.state.placeNotice));
  } finally {
    await unmount(m);
    dom.window.close();
  }
});
