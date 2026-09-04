// attack-idb-primary-boot-round2.test.tsx — INDEPENDENT DESTRUCTIVE ROUND 2,
// FEAT-2326609780 inc2 (IDB-PRIMARY BOOT) REWORK.
//
// Round 1 (attack-idb-primary-boot-round.test.tsx, kept UNMODIFIED as the bar)
// returned REJECT on six findings. The rework closes all six — all six round-1
// attacks now pass against it. This file attacks the REWORK'S OWN new
// machinery, which is substantial and none of it existed when round 1 ran:
//
//   G. THE NEW BUILDING-COUNT SAFETY NET (store.tsx, the IDB-freshness
//      effect): an empty-tail candidate with FEWER buildings than the live
//      city is refused. Building count is not a monotonic measure of
//      progress — a city legitimately SHRINKS (bulldoze, forced asset sale,
//      the consolidator's own scrap-and-rebuild passes, a dam purge). Does
//      the net refuse a LEGITIMATE rescue?
//
//   H. THE NEW JOURNAL RE-BASE (F2's fix): the swap now overwrites the
//      persisted journal with a SINGLE synthetic
//      `{type:'hydrate', state: reconciledState}` entry, flushed through
//      `journalPersisterRef.current.flush()`. That one entry embeds the WHOLE
//      SimState. What happens when that write does not fit — i.e. exactly the
//      quota-wedged browser this entire increment exists for?

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
  (globalThis as any).requestAnimationFrame = window.requestAnimationFrame.bind(window);
  (globalThis as any).cancelAnimationFrame = window.cancelAnimationFrame.bind(window);
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
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

// ---------------------------------------------------------------------------
// G. THE BUILDING-COUNT HEURISTIC — DOES IT REFUSE A LEGITIMATE RESCUE?
//
//    The wedge shape, with ONE ordinary difference: between the stale
//    localStorage savepoint and the fresher IndexedDB one, the player
//    DEMOLISHED. The IndexedDB copy is unambiguously the newer, correct city
//    (later savedAt, same lineage, its own tail empty) — it simply has fewer
//    buildings, because the player bulldozed some. `bulldoze` is a core game
//    verb (journal.ts classifies it state-affecting), forced asset sales
//    remove buildings (FEAT-1972079923 inc2), and the consolidator's
//    scrap-and-rebuild passes net-remove them too (FEAT-2326609761, glide is
//    the DEFAULT). This is not an exotic state.
// ---------------------------------------------------------------------------
test('ATTACK G: the new empty-tail building-count safety net REFUSES a legitimate rescue when the player demolished — a city that shrank is still the newer city', async () => {
  const dom = installJsdom();
  try {
    const backing = new Map<string, Map<string, string>>();
    (globalThis as any).indexedDB = (await loadFakeIndexedDBFactory())(backing);
    const { resetSaveStoreForTests, getDefaultSaveStore, SAVEPOINT_OVERFLOW_KEY } = await import('../src/sim/saveStore.ts');
    resetSaveStoreForTests();

    const { initialState, reducer } = await import('../src/sim/engine.ts');
    const { createSavepoint, persistSavepoint } = await import('../src/sim/replay.ts');
    const { versionBadgeLabel } = await import('../src/sim/version.ts');

    const running = versionBadgeLabel();
    let cityBefore = { ...initialState(), unlockedAll: true, funds: 5_000_000_000 };
    for (const a of buildGrowthTail(40)) cityBefore = reducer(cityBefore, a as never);

    // localStorage is STUCK on the pre-demolition city (the wedge: every
    // savepoint persist since has been rejected).
    assert.ok(
      persistSavepoint(dom.window.localStorage as unknown as Storage, createSavepoint(cityBefore, [], new Date(), running, null)),
      'seed the stale (pre-demolition) localStorage savepoint',
    );

    // The player then DEMOLISHED 30 buildings. Ordinary play. The durable
    // IndexedDB overflow slot — the only place a savepoint could still land —
    // holds this newer, SMALLER city, with a later savedAt.
    let cityAfter = cityBefore;
    let demolished = 0;
    for (const b of cityBefore.buildings.slice(-40)) {
      const next = reducer(cityAfter, { type: 'bulldoze', x: b.x, y: b.y } as never);
      if (next.buildings.length < cityAfter.buildings.length) {
        demolished += cityAfter.buildings.length - next.buildings.length;
        cityAfter = next;
      }
      if (demolished >= 30) break;
    }
    assert.ok(demolished >= 20, `test setup: the demolition must actually shrink the city (removed ${demolished})`);
    assert.ok(cityAfter.buildings.length < cityBefore.buildings.length, 'test setup: the newer city must be SMALLER');

    const store = getDefaultSaveStore();
    assert.ok(
      (await store.setItem(SAVEPOINT_OVERFLOW_KEY, JSON.stringify(createSavepoint(cityAfter, [], new Date(Date.now() + 60_000), running, null)))).ok,
      'seed the durable rescue copy (newer, smaller, empty tail)',
    );

    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimProvider, useSim } = await import('../src/sim/store.tsx');

    let latestState: any = null;
    function Probe() {
      const { state } = useSim();
      latestState = state;
      return null;
    }
    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);
    act(() => {
      root.render(React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }));
    });
    await waitFor(() => !!latestState, 5000);
    await new Promise((r) => setTimeout(r, 3000));

    console.error(
      `[G] stale localStorage city=${cityBefore.buildings.length}; durable rescue (newer, ${demolished} demolished)=${cityAfter.buildings.length}; app settled on ${latestState.buildings.length}`,
    );
    pin(() =>
      assert.equal(
        latestState.buildings.length,
        cityAfter.buildings.length,
        `THE HEURISTIC IS UNSAFE: the durable IndexedDB copy is unambiguously the NEWER city (later savedAt, same lineage, empty tail) but has ` +
          `${cityAfter.buildings.length} buildings vs the stale localStorage boot's ${cityBefore.buildings.length}, because the player DEMOLISHED ${demolished}. ` +
          `The new empty-tail building-count net refuses the swap, and the app stayed on ${latestState.buildings.length} — the stale city. ` +
          'The wedge this increment exists to close stays open for any player who bulldozed, took a forced asset sale (FEAT-1972079923 inc2), ' +
          'or let the DEFAULT-ON consolidator (FEAT-2326609761 glide) scrap-and-rebuild. Building count is not a monotonic progress measure; a ' +
          'per-lineage monotonic counter (journal length / save index stamped into the savepoint) is.',
      ),
    );

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});

// ---------------------------------------------------------------------------
// H. THE JOURNAL RE-BASE UNDER QUOTA.
//
//    F2's fix builds `rebasedJournal = { entries: [{ tick, action:
//    {type:'hydrate', state: reconciledState} }] }` and flushes it. That ONE
//    entry embeds the ENTIRE SimState. `persistJournal` (journal.ts) only
//    truncates when `entries.length > 200`; with a single entry it falls
//    straight through to `safeSetItem`, and on failure takes the
//    `entries.length <= 200` branch — which calls
//    `storage.removeItem(JOURNAL_KEY)` and returns false. The flush's return
//    value is not checked at the call site.
//
//    So on the quota-wedged browser this increment exists for, the swap
//    DELETES the player's persisted journal outright, silently.
// ---------------------------------------------------------------------------
test('ATTACK H: on a quota-wedged browser the swap DELETES the persisted journal — the re-based single-hydrate entry cannot fit, and persistJournal removes the key on failure', async () => {
  const dom = installJsdom();
  try {
    const backing = new Map<string, Map<string, string>>();
    (globalThis as any).indexedDB = (await loadFakeIndexedDBFactory())(backing);
    const { resetSaveStoreForTests, getDefaultSaveStore, SAVEPOINT_OVERFLOW_KEY } = await import('../src/sim/saveStore.ts');
    resetSaveStoreForTests();

    const { initialState, reducer } = await import('../src/sim/engine.ts');
    const { emptyJournal, recordAction, persistJournal, loadJournal, JOURNAL_KEY } = await import('../src/sim/journal.ts');
    const { createSavepoint, persistSavepoint } = await import('../src/sim/replay.ts');
    const { versionBadgeLabel } = await import('../src/sim/version.ts');

    const running = versionBadgeLabel();
    const start = { ...initialState(), unlockedAll: true, funds: 5_000_000_000 };

    // The player's real recorded history — the thing a Rebuild from Genesis /
    // hard-reset-replay (GR#27, FEAT-1972079897) depends on existing.
    const lsActions = buildGrowthTail(10);
    let lsState = start;
    let journal = emptyJournal();
    for (const a of lsActions) {
      lsState = reducer(lsState, a as never);
      journal = recordAction(journal, lsState.tick, a as never);
    }
    assert.ok(
      persistSavepoint(dom.window.localStorage as unknown as Storage, createSavepoint(lsState, [], new Date(), running, null)),
      'seed the localStorage savepoint',
    );
    assert.ok(persistJournal(dom.window.localStorage as unknown as Storage, journal), 'seed the persisted journal');
    const seededEntries = loadJournal(dom.window.localStorage as unknown as Storage).entries.length;
    assert.ok(seededEntries > 0, 'test setup: the persisted journal must have entries');

    // The durable rescue copy: newer AND bigger, so the round-2 building-count
    // net cannot be what refuses this swap.
    let idbState = lsState;
    for (const a of buildGrowthTail(20)) idbState = reducer(idbState, a as never);
    const store = getDefaultSaveStore();
    assert.ok(
      (await store.setItem(SAVEPOINT_OVERFLOW_KEY, JSON.stringify(createSavepoint(idbState, [], new Date(Date.now() + 60_000), running, null)))).ok,
      'seed the durable rescue copy',
    );

    // WEDGE the journal key only: a write of the re-based single-hydrate
    // entry (which embeds the whole SimState) does not fit. Everything else
    // — including the savepoint slots — keeps working, so this test isolates
    // the journal re-base and nothing else.
    const realStorage = dom.window.localStorage;
    let journalWriteRejections = 0;
    const wedged = {
      get length() {
        return realStorage.length;
      },
      key: (i: number) => realStorage.key(i),
      getItem: (k: string) => realStorage.getItem(k),
      removeItem: (k: string) => realStorage.removeItem(k),
      clear: () => realStorage.clear(),
      setItem: (k: string, v: string) => {
        if (k === JOURNAL_KEY && v.length > 50_000) {
          journalWriteRejections++;
          const e: any = new Error('QuotaExceededError: persistent storage is full');
          e.name = 'QuotaExceededError';
          e.code = 22;
          throw e;
        }
        realStorage.setItem(k, v);
      },
    };
    Object.defineProperty(dom.window, 'localStorage', { value: wedged, configurable: true });

    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimProvider, useSim } = await import('../src/sim/store.tsx');

    let latestState: any = null;
    function Probe() {
      const { state } = useSim();
      latestState = state;
      return null;
    }
    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);
    act(() => {
      root.render(React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }));
    });
    await waitFor(() => !!latestState, 5000);
    // Wait for the swap.
    try {
      await waitFor(() => latestState.buildings.length === idbState.buildings.length, 8000);
    } catch {
      console.error('[H] the swap did not fire at all');
    }
    await new Promise((r) => setTimeout(r, 500));

    const after = loadJournal(realStorage as unknown as Storage);
    console.error(
      `[H] swap landed=${latestState.buildings.length === idbState.buildings.length}; journal entries before=${seededEntries} after=${after.entries.length}; oversize journal writes rejected=${journalWriteRejections}`,
    );
    pin(() =>
      assert.ok(
        after.entries.length > 0,
        `SAVE-EATER: the persisted journal went from ${seededEntries} entries to ${after.entries.length} — it was DELETED. The F2 re-base flushes a single ` +
          'synthetic hydrate entry embedding the whole SimState; persistJournal only truncates when entries.length > 200, so with ONE entry it falls through ' +
          'to the failure branch and calls removeItem(JOURNAL_KEY). The flush return value is not checked at the call site, so this is silent. On the ' +
          'quota-wedged browser this whole increment exists for, the swap therefore destroys the player\'s action history outright — and Rebuild from ' +
          'Genesis / hard-reset-replay (GR#27, FEAT-1972079897) then rebuilds a bare city. The pre-rework behaviour left a stale-but-real journal; this ' +
          'leaves none.',
      ),
    );

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});
