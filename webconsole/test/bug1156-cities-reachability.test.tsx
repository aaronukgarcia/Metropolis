// bug1156-cities-reachability.test.tsx - BUG-1156 r4 universal-reachability property test.
// Seeded random sequences of {play, reload, adopt real, adopt scratch, Start Over, corrupt current}
// over 3+ lineages; after EVERY step each lineage with a valid save must be in the Cities list
// (the same listCities the File menu reads), exactly once, with the right scratch label.

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

function mulberry32(a: number) {
  return () => {
    a |= 0; a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const SEEDS = Number(process.env.CITIES_SEEDS ?? 100);
const STEPS = 6;

test(`CITIES property: ${SEEDS} seeds x ${STEPS} steps - every valid-save lineage listed once with the right scratch label after EVERY step`, async () => {
  const R = await import('../src/sim/replay.ts');
  let checks = 0;
  const failures: string[] = [];
  for (let seed = 1; seed <= SEEDS; seed++) {
    const rnd = mulberry32(seed * 7919);
    const pick = <T,>(xs: T[]): T => xs[Math.floor(rnd() * xs.length)];
    const dom = await freshEnv();
    try {
      const storage = dom.window.localStorage as unknown as Storage;
      const realIds = ['LaP' + seed, 'LbP' + seed, 'LcP' + seed];
      const mustList = new Set<string>(); // lineages we saved and never corrupted
      const scratchKnown = new Set<string>();
      const realKnown = new Set<string>();
      for (let i = 0; i < realIds.length; i++) {
        await seedCity(storage, realIds[i], 1_000_000 + i, 100 + i * 50, new Date(Date.UTC(2026, 9, 30, 10 + i)));
        mustList.add(realIds[i]);
        realKnown.add(realIds[i]);
      }
      // pointer: one real city, or a dangling pointer (withheld boot -> scratch)
      storage.setItem(R.CURRENT_LINEAGE_KEY, rnd() < 0.5 ? pick(realIds) : 'LxP' + seed);
      let m: any = await mountProvider(dom, rnd() < 0.3);
      const verify = (step: string) => {
        const listed = m.seen.ctx.listCities() as any[];
        const ids = listed.map((c) => c.lineageId);
        checks++;
        const fail = (why: string) => failures.push(`seed ${seed} ${step}: ${why} (listed ${JSON.stringify(ids)})`);
        if (new Set(ids).size !== ids.length) fail('duplicate rows');
        for (const id of mustList) if (!ids.includes(id)) fail(`missing ${id}`);
        // independent oracle: every lineage key family in storage with a valid save must be listed
        const oracle = new Set<string>();
        for (let i = 0; i < storage.length; i++) {
          const k = storage.key(i)!;
          if (!k.startsWith('metropolis.savepoint.')) continue;
          const segs = k.slice('metropolis.savepoint.'.length).split('.');
          if (!/^\d+$/.test(segs[segs.length - 1])) continue;
          const id = segs.length === 1 ? R.LEGACY_LINEAGE_ID : segs.slice(0, -1).join('.');
          if (R.readAllSavepoints(storage as any, new Date(), id).some((sp: any) => typeof sp?.snapshotTick === 'number' && typeof sp?.savedAt === 'string')) oracle.add(id);
        }
        for (const id of oracle) if (!ids.includes(id)) fail(`valid-save lineage ${id} not listed`);
        for (const id of ids) if (!oracle.has(id)) fail(`listed ${id} has no valid save`);
        for (const c of listed) {
          if (scratchKnown.has(c.lineageId) && !c.scratch) fail(`${c.lineageId} should be labelled scratch`);
          if (realKnown.has(c.lineageId) && c.scratch) fail(`${c.lineageId} wrongly labelled scratch`);
          if (c.scratch !== /Recovered session city/.test(c.label)) fail(`${c.lineageId} label/stamp disagree`);
        }
        const firstScratch = listed.findIndex((c) => c.scratch);
        if (firstScratch >= 0 && listed.slice(firstScratch).some((c) => !c.scratch)) fail('scratch row ahead of a real row');
        if (listed.filter((c) => c.current).length > 1) fail('more than one current row');
      };
      const noteRun = () => {
        const lid = m.seen.state.lineageId as string;
        // scratch-ness is durable: an adopted scratch city (pointer == its id) is STILL scratch.
        if (scratchKnown.has(lid) || lid !== R.readCurrentLineageId(storage as any)) scratchKnown.add(lid);
        else realKnown.add(lid);
      };
      const remount = async () => {
        await closeAllRoots();
        m = await mountProvider(dom, rnd() < 0.3);
      };
      noteRun();
      verify('boot');
      for (let step = 0; step < STEPS; step++) {
        const kind = pick(['play', 'reload', 'adoptReal', 'adoptScratch', 'startOver', 'corrupt']);
        const label = `step${step}:${kind}`;
        try {
          if (kind === 'play') {
            await playTicks(m, 3);
            const lid = m.seen.state.lineageId as string;
            assert.ok(await m.seen.ctx.saveGame());
            noteRun();
            mustList.add(lid);
          } else if (kind === 'reload') {
            await remount();
            noteRun();
          } else if (kind === 'adoptReal' || kind === 'adoptScratch') {
            const rows = (m.seen.ctx.listCities() as any[]).filter((c) => !c.current && (kind === 'adoptScratch' ? c.scratch : !c.scratch));
            if (rows.length) {
              assert.equal(R.adoptLineage(storage as any, pick(rows).lineageId), true);
              await remount();
              noteRun();
            }
          } else if (kind === 'startOver') {
            await m.act(async () => {
              m.seen.ctx.dispatch({ type: 'reset' });
            });
            noteRun();
          } else {
            const cur = m.seen.state.lineageId as string;
            for (let slot = 0; slot < R.SAVEPOINT_CAP; slot++) {
              const key = R.savepointKey(slot, cur);
              if (storage.getItem(key) !== null) storage.setItem(key, '{corrupt');
            }
            mustList.delete(cur);
            await remount();
            noteRun();
          }
        } catch (e) {
          failures.push(`seed ${seed} ${label}: threw ${(e as Error).message}`);
        }
        verify(label);
      }
    } finally {
      await closeAllRoots();
      dom.window.close();
    }
  }
  console.log(`CITIES property: seeds=${SEEDS} steps/seed=${STEPS} (+boot) checks=${checks} failures=${failures.length}`);
  assert.deepEqual(failures.slice(0, 10), [], 'no reachability failures');
});

test('CITIES FileMenu: the Cities view lists every city, marks current, Open adopts', async () => {
  const dom = await freshEnv();
  try {
    const R = await import('../src/sim/replay.ts');
    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimProvider } = await import('../src/sim/store.tsx');
    const { FileMenu } = await import('../src/components/FileMenu.tsx');
    const storage = dom.window.localStorage as unknown as Storage;
    await seedCity(storage, 'LfmReal', 2_000_000, 300, new Date('2026-10-30T10:00:00Z'));
    storage.setItem(R.CURRENT_LINEAGE_KEY, 'LfmDangling');
    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);
    await act(async () => {
      root.render(React.default.createElement(SimProvider, { children: React.default.createElement(FileMenu) }));
    });
    openRoots.push({ root, act });
    await new Promise((r) => setTimeout(r, 300));
    const click = async (el: Element | null | undefined) => {
      assert.ok(el);
      await act(async () => {
        el!.dispatchEvent(new (dom.window as any).MouseEvent('click', { bubbles: true }));
      });
    };
    await click(container.querySelector('button.btn.tiny'));
    const citiesBtn = Array.from(container.querySelectorAll('button')).find((b) => /Cities/.test(b.textContent ?? ''));
    await click(citiesBtn);
    const rows = Array.from(container.querySelectorAll('[data-city]'));
    assert.ok(rows.some((r) => r.getAttribute('data-city') === 'LfmReal' && r.getAttribute('data-scratch') === 'false'), 'real city listed');
    // (the running scratch city has no save yet, so it is correctly not listed; the current marker is exercised by the property test)
    const realRow = rows.find((r) => r.getAttribute('data-city') === 'LfmReal')!;
    await click(realRow.querySelector('button'));
    assert.equal(R.readCurrentLineageId(storage as any), 'LfmReal', 'Open adopts');
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});
