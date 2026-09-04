// attack-bug625-duplicate-key.test.tsx — BUG-625: "Warning: Encountered two
// children with the same key" fired 8x in 9 minutes at Aaron's 1.44M-pop /
// 29,654-building dogfood city, NEVER at 550k, stack pointing at React's
// client reconciler (reconcileChildrenArray).
//
// ROOT CAUSE (confirmed by direct repro against the real engine reducer, see
// the standalone repro script this test formalises): `state.nextId` is the
// SimState's SOLE building-id-minting counter (engine.ts's `nextId++`
// call-sites). Whenever `state.buildings` is populated by a path that does
// NOT resync `nextId` to `nextSafeBuildingId(buildings)` afterwards (the
// BUG-413 class — engine.ts's `replay.ts` already guards its own hard-reset/
// journal-rebuild path, but that guard is a per-call-site discipline, not a
// structural invariant on SimState itself), any SUBSEQUENT building placed
// mints an id that can already exist further up `buildings[]`. Two entirely
// DIFFERENT buildings then share one `id` — and every list keyed `key={b.id}`
// (or a derivative that copies `id`/`x`/`y` straight off a building, like
// ForcedSaleAsset) renders a duplicate React key. Three live sites found by
// enumeration + an independent round's REJECT finding on the first pass:
// ConstructionQueue.tsx's queue rows, servicesTabs.tsx's WaterTab plant rows,
// and MapView.tsx's Forced Asset Sales overlay (`forcedSaleAssets()`,
// engine.ts — copies id/x/y off `state.buildings` exactly like the other two,
// and renders during insolvency, exactly where a long-lived, heavily-built
// city is most likely to be found). This is scale-triggered because it only
// bites once a real dogfood city both (a) has gone through a path that
// under-syncs nextId AND (b) has enough buildings for the resulting id
// collision to land inside one of these RENDERED lists (a fresh, low-id-count
// city stays lucky).
//
// This test proves the defect directly against the REAL rendered components
// (createRoot + jsdom, the established live-mount pattern from
// bug606-demanddock-ui.test.tsx / bug498-forced-sales-dismiss.test.tsx) by
// handing them a SimState with two deliberately id-colliding buildings/assets
// — exactly the shape the corrupted-nextId path produces — and trapping
// console.error for React's own duplicate-key warning text. No engine.ts/
// data.ts/store.tsx edits: this suite only touches the RENDER surface
// (ConstructionQueue.tsx, servicesTabs.tsx, MapView.tsx's
// ForcedAssetSalesPanel — a component-local list-key fix, not a rework of
// MapView's map-canvas rendering itself).
//
// RED SELF-PROOF (documented, not left sabotaged in the tree — GR#21
// verification standards, mirroring construction-queue.test.tsx's own
// RED-proof note): with `key={r.id}` / `key={b.id}` / `key={a.id}` restored
// (the pre-fix code) in each of the three files, the corresponding assertion
// below fails — the console.error trap catches "Warning: Encountered two
// children with the same key" for ConstructionQueueTab, WaterTab, and the
// Forced Asset Sales overlay in turn. Verified via `cp <file> <file>.bak`,
// reverting the key expression, re-running (RED), then `mv <file>.bak <file>`
// (never a git command — GR#24) to restore the fix and re-run GREEN.
//
// FOLLOW-UP (not blocking this estate, worth a BOW line): `nextLedgerId`
// (engine.ts, financeTabs.tsx's ledger `key={e.id}`) has no
// `nextSafeLedgerId`-equivalent resync anywhere — `nextSafeBuildingId()` is
// only ever called for buildings. The ledger is capped (`LEDGER_CAP`,
// `.slice(0, LEDGER_CAP)`) and every mint site reads its `nextLedgerId` from
// the SAME incoming `s`/`state`, so no reachable path was found that
// under-syncs it the way `nextId` can be under-synced — but the absence of an
// analogous resync helper is a structural gap worth closing defensively if a
// ledger-populating path like the buildings one is ever added.

import { test } from 'node:test';
import assert from 'node:assert/strict';

function installJsdom() {
  return import('jsdom').then(({ JSDOM }) => {
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
    if (typeof (globalThis as any).ResizeObserver === 'undefined') {
      (globalThis as any).ResizeObserver = class {
        observe() {}
        unobserve() {}
        disconnect() {}
      };
    }
    return dom;
  });
}

async function mountReal(Component: any, ctx: any) {
  const React = await import('react');
  const { createRoot } = await import('react-dom/client');
  const { act } = await import('react-dom/test-utils');
  const { SimContext } = await import('../src/sim/simContext.ts');

  const container = (globalThis as any).document.getElementById('root');
  const root = createRoot(container);
  await act(async () => {
    root.render(
      React.default.createElement(SimContext.Provider, { value: ctx }, React.default.createElement(Component)),
    );
  });
  return { container, root, act };
}

function makeCtx(state: any) {
  return {
    state,
    dispatch: () => {},
    cityName: 'Attackville',
    listSaves: () => [],
    listRecent: () => [],
    saveGame: async () => true,
    saveGameAs: async () => ({ ok: true }),
    loadGame: async () => {},
    loadNamed: async () => {},
    renameCity: () => ({ ok: true }),
    exportCity: async () => true,
    importCity: async () => true,
  };
}

/** Installs a console.error spy that records every call, restorable via the returned fn. */
function trapConsoleError() {
  const original = console.error;
  const calls: unknown[][] = [];
  console.error = (...args: unknown[]) => {
    calls.push(args);
  };
  return {
    calls,
    restore: () => {
      console.error = original;
    },
    duplicateKeyWarnings: () =>
      calls.filter((args) => args.some((a) => typeof a === 'string' && a.includes('two children with the same key'))),
  };
}

// ---------------------------------------------------------------------------
// (1) ConstructionQueueTab — two DIFFERENT under-construction buildings
// sharing one `id` (the corrupted-nextId shape).
// ---------------------------------------------------------------------------

test('BUG-625: ConstructionQueueTab does not emit a duplicate-key warning when two buildings share an id (corrupted-nextId shape)', async () => {
  const dom: any = await installJsdom();
  const trap = trapConsoleError();
  try {
    const { initialState } = await import('../src/sim/engine.ts');
    const { ConstructionQueueTab } = await import('../src/components/left/ConstructionQueue.tsx');

    const base = initialState();
    // Two DIFFERENT buildings, deliberately given the SAME id and DIFFERENT
    // positions — exactly what a desynced nextId hands out (see file header).
    // Both must be genuinely "still under construction" (builtTick === tick,
    // fresh) so constructionQueueOf() keeps both rows.
    const colliding = [
      { id: 999, spec: 'res_hut', x: 10, y: 10, builtTick: 0 },
      { id: 999, spec: 'res_hut', x: 40, y: 40, builtTick: 0 },
    ];
    const state = {
      ...base,
      buildings: colliding,
      tick: 0,
      unlockedAll: true,
      funds: 1_000_000_000,
    };

    const { root, act } = await mountReal(ConstructionQueueTab, makeCtx(state));

    assert.deepEqual(
      trap.duplicateKeyWarnings(),
      [],
      `expected zero duplicate-key warnings, got: ${JSON.stringify(trap.duplicateKeyWarnings())}`,
    );

    await act(async () => {
      root.unmount();
    });
  } finally {
    trap.restore();
    dom.window.close();
  }
});

// ---------------------------------------------------------------------------
// (2) WaterTab — two DIFFERENT water plants sharing one `id`.
// ---------------------------------------------------------------------------

test('BUG-625: WaterTab does not emit a duplicate-key warning when two water plants share an id (corrupted-nextId shape)', async () => {
  const dom: any = await installJsdom();
  const trap = trapConsoleError();
  try {
    const { initialState } = await import('../src/sim/engine.ts');
    const { WaterTab } = await import('../src/components/left/tabs/servicesTabs.tsx');
    const { SPECS } = await import('../src/sim/data.ts');

    const waterSpecId = Object.values(SPECS).find((sp: any) => sp.kind === 'water')?.id;
    assert.ok(waterSpecId, 'precondition: at least one water-kind spec must exist in the catalogue');

    const base = initialState();
    const colliding = [
      { id: 777, spec: waterSpecId, x: 12, y: 12, builtTick: 0 },
      { id: 777, spec: waterSpecId, x: 88, y: 88, builtTick: 0 },
    ];
    const state = {
      ...base,
      buildings: colliding,
      tick: 0,
      unlockedAll: true,
      funds: 1_000_000_000,
      pipeTier: {},
    };

    const { root, act } = await mountReal(WaterTab, makeCtx(state));

    assert.deepEqual(
      trap.duplicateKeyWarnings(),
      [],
      `expected zero duplicate-key warnings, got: ${JSON.stringify(trap.duplicateKeyWarnings())}`,
    );

    await act(async () => {
      root.unmount();
    });
  } finally {
    trap.restore();
    dom.window.close();
  }
});

// ---------------------------------------------------------------------------
// (3) Forced Asset Sales overlay (MapView.tsx ~line 1485) — an independent
// round's REJECT finding on the original estate: forcedSaleAssets() (engine.ts)
// copies id/x/y straight off `state.buildings`, exposing this overlay to the
// SAME collision class, and it renders during insolvency — exactly where a
// long-lived, heavily-built (and so more likely to have hit a desynced-nextId
// path somewhere) city is most likely to be found. Mounts the REAL MapView
// (mountFull below) — the established pattern from
// bug498-forced-sales-dismiss.test.tsx, since ForcedAssetSalesPanel is not
// separately exported.
// ---------------------------------------------------------------------------

async function mountFull(state: any) {
  const React = await import('react');
  const { createRoot } = await import('react-dom/client');
  const { act } = await import('react-dom/test-utils');
  const { SimContext } = await import('../src/sim/simContext.ts');
  const { BusyProvider } = await import('../src/components/Busy.tsx');
  const { MapView } = await import('../src/components/MapView.tsx');

  const container = (globalThis as any).document.getElementById('root');
  const root = createRoot(container);
  await act(async () => {
    root.render(
      React.default.createElement(
        SimContext.Provider,
        { value: makeCtx(state) },
        React.default.createElement(BusyProvider, { children: React.default.createElement(MapView) }),
      ),
    );
  });
  return { container, root, act };
}

test('BUG-625: the Forced Asset Sales overlay does not emit a duplicate-key warning when two sellable assets share an id (corrupted-nextId shape)', async () => {
  const dom: any = await installJsdom();
  const trap = trapConsoleError();
  try {
    const { initialState } = await import('../src/sim/engine.ts');

    const base = initialState();
    // Two DIFFERENT, sellable (non-zero placementCost) buildings, deliberately
    // given the SAME id and DIFFERENT tiles — the corrupted-nextId shape (see
    // file header). res_hut is a FREE-ZONE spec (placementCost 0), which
    // forcedSaleAssets() deliberately excludes ("nothing to force-sell for
    // £0") — edu_nursery is paid (same spec construction-queue.test.tsx uses
    // for its own non-zero-cost assertion), so both rows survive the
    // `capitalValue <= 0` filter.
    const colliding = [
      { id: 888, spec: 'edu_nursery', x: 15, y: 15, builtTick: 0 },
      { id: 888, spec: 'edu_nursery', x: 60, y: 60, builtTick: 0 },
    ];
    const state: any = {
      ...base,
      buildings: colliding,
      tick: 0,
      unlockedAll: true,
      funds: 1_000_000_000,
      bailoutState: { enteredAt: 0 },
    };

    const { container, root, act } = await mountFull(state);

    const panel = container.querySelector('.forced-asset-sales-panel');
    assert.ok(panel, 'precondition: the Forced Asset Sales panel must be mounted while bailoutState is active');
    const rows = container.querySelectorAll('.forced-asset-sales-list li');
    assert.equal(rows.length, 2, 'precondition: both colliding-id assets must render as rows');

    assert.deepEqual(
      trap.duplicateKeyWarnings(),
      [],
      `expected zero duplicate-key warnings, got: ${JSON.stringify(trap.duplicateKeyWarnings())}`,
    );

    await act(async () => {
      root.unmount();
    });
  } finally {
    trap.restore();
    dom.window.close();
  }
});

// ---------------------------------------------------------------------------
// (4) Positive control — the trap itself actually catches React's duplicate-
// key warning when a list IS genuinely keyed by the colliding field alone.
// Proves (1)/(2)/(3) above are not silently vacuous (e.g. because jsdom or
// the react-dom build in use suppresses the warning outright).
// ---------------------------------------------------------------------------

test('BUG-625 control: the console.error trap catches a genuine key={id}-only collision', async () => {
  const dom: any = await installJsdom();
  const trap = trapConsoleError();
  try {
    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');

    function BadList() {
      const rows = [
        { id: 1, label: 'a' },
        { id: 1, label: 'b' },
      ];
      return React.default.createElement(
        'ul',
        null,
        rows.map((r) => React.default.createElement('li', { key: r.id }, r.label)),
      );
    }

    const container = (globalThis as any).document.getElementById('root');
    const root = createRoot(container);
    await act(async () => {
      root.render(React.default.createElement(BadList));
    });

    assert.ok(
      trap.duplicateKeyWarnings().length > 0,
      'the trap must catch a deliberately-reproduced key={id}-only collision — otherwise (1)/(2) above prove nothing',
    );

    await act(async () => {
      root.unmount();
    });
  } finally {
    trap.restore();
    dom.window.close();
  }
});
