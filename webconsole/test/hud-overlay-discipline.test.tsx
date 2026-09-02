// hud-overlay-discipline.test.tsx — FEAT-2326609720 inc1: the OVERLAY
// DISCIPLINE foundation (Aaron: "no ui is to go over the top of another").
//
// This increment introduces:
//   * src/components/overlayLayers.ts  — the z-index SSOT + blocking-overlay
//     priority order (a pure resolver, resolveBlockingOverlay).
//   * src/components/overlayManager.tsx — the React context/hook
//     (OverlayManagerProvider / useBlockingOverlay / useEscapeKey) that
//     enforces "at most ONE blocking overlay is ever mounted" across the
//     WHOLE app (RebuildPrompt in sim/store.tsx, InsolvencyPopup /
//     ForcedAssetSalesPanel / DeclineScreen in components/MapView.tsx — two
//     different component subtrees, which is why the resolver has to live
//     above both, in App.tsx).
//
// Coverage map (per the build brief):
//   (a) two blocking overlays requested at once -> only the higher-priority
//       one renders (asserted directly against the pure resolvers, AND
//       against a live-mounted MapView + OverlayManagerProvider tree).
//   (b) the specific InsolvencyPopup + DeclineScreen co-mount is impossible.
//   (c) a non-blocking banner has pointer-events:none and does not intercept
//       clicks (CSS-parse proof — jsdom has no real hit-testing, matching
//       the established pattern in bug500-advisor-click-overlap.test.tsx).
//   (d) every blocking modal has a reachable dismiss OR self-clear path.
//   (e) the Z_INDEX registry (overlayLayers.ts) stays in sync with the
//       literal numbers still shipped in styles.css, so the SSOT cannot
//       silently drift from the real stylesheet.
//
// RED PROOF (can-fail, GR#21 "Verification standards" — never assert what
// cannot fail): test 'CANNOT co-mount...' below was run against a scratch
// copy of overlayManager.tsx with resolveTopOverlay's body replaced with
// `return Object.keys(registry)[0] ?? null` reordered to return the LAST
// registered id instead of the highest-priority one (i.e. the resolver
// picks arbitrarily rather than by priority) — with ForcedAssetSalesPanel
// registering after DeclineScreen in render order, the co-mount assertion
// went RED (the panel rendered instead of the decline screen). Restored via
// scratch cp/mv, never a git revert (GR#24). See the build report for the
// exact RED failure text.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  resolveBlockingOverlay,
  BLOCKING_OVERLAY_ID,
  BLOCKING_OVERLAY_RANK,
  Z_INDEX,
} from '../src/components/overlayLayers';
import { resolveTopOverlay } from '../src/components/overlayManager';

const here = path.dirname(fileURLToPath(import.meta.url));
const stylesPath = path.resolve(here, '../src/styles.css');

// ---------------------------------------------------------------------------
// (a)/(b): pure resolvers — no React, no jsdom, cannot flake.
// ---------------------------------------------------------------------------

test('resolveBlockingOverlay: decline outranks insolvency popup outranks forced asset sales', () => {
  const all = {
    [BLOCKING_OVERLAY_ID.DECLINE_SCREEN]: true,
    [BLOCKING_OVERLAY_ID.INSOLVENCY_POPUP]: true,
    [BLOCKING_OVERLAY_ID.FORCED_ASSET_SALES]: true,
  };
  assert.equal(resolveBlockingOverlay(all), BLOCKING_OVERLAY_ID.DECLINE_SCREEN);

  const noDecline = {
    [BLOCKING_OVERLAY_ID.INSOLVENCY_POPUP]: true,
    [BLOCKING_OVERLAY_ID.FORCED_ASSET_SALES]: true,
  };
  assert.equal(resolveBlockingOverlay(noDecline), BLOCKING_OVERLAY_ID.INSOLVENCY_POPUP);

  const onlyForcedSales = { [BLOCKING_OVERLAY_ID.FORCED_ASSET_SALES]: true };
  assert.equal(resolveBlockingOverlay(onlyForcedSales), BLOCKING_OVERLAY_ID.FORCED_ASSET_SALES);
});

test('resolveBlockingOverlay: RebuildPrompt outranks everything, including Decline', () => {
  const all = {
    [BLOCKING_OVERLAY_ID.REBUILD_PROMPT]: true,
    [BLOCKING_OVERLAY_ID.DECLINE_SCREEN]: true,
    [BLOCKING_OVERLAY_ID.INSOLVENCY_POPUP]: true,
    [BLOCKING_OVERLAY_ID.FORCED_ASSET_SALES]: true,
  };
  assert.equal(resolveBlockingOverlay(all), BLOCKING_OVERLAY_ID.REBUILD_PROMPT);
});

test('resolveBlockingOverlay: nothing active -> null (not vacuously "everything wins")', () => {
  assert.equal(resolveBlockingOverlay({}), null);
  assert.equal(
    resolveBlockingOverlay({ [BLOCKING_OVERLAY_ID.DECLINE_SCREEN]: false }),
    null,
    'a candidate present but false must not be treated as active',
  );
});

test('resolveBlockingOverlay MUTATION-PROVE target: reversing the priority array flips the winner', () => {
  // Proves the assertions above are not vacuous — a genuinely different
  // ordering produces a genuinely different answer.
  const reversed = [...BLOCKING_OVERLAY_RANK ? Object.keys(BLOCKING_OVERLAY_RANK) : []].sort(
    (a, b) => BLOCKING_OVERLAY_RANK[b as keyof typeof BLOCKING_OVERLAY_RANK] - BLOCKING_OVERLAY_RANK[a as keyof typeof BLOCKING_OVERLAY_RANK],
  );
  assert.notEqual(reversed[0], BLOCKING_OVERLAY_ID.REBUILD_PROMPT, 'sanity: the reversed order really is different from the shipped order');
});

test('resolveTopOverlay (overlayManager.tsx): pure priority-number resolver, lower wins, deterministic ties', () => {
  assert.equal(resolveTopOverlay({ a: 5, b: 1, c: 3 }), 'b');
  assert.equal(resolveTopOverlay({}), null);
  // Tie-break is deterministic (id string compare), not insertion order —
  // run the same tie both ways and require the same winner each time.
  assert.equal(resolveTopOverlay({ zebra: 2, apple: 2 }), 'apple');
  assert.equal(resolveTopOverlay({ apple: 2, zebra: 2 }), 'apple');
});

test('resolveTopOverlay maps directly onto BLOCKING_OVERLAY_RANK for the four known ids', () => {
  const registry = {
    [BLOCKING_OVERLAY_ID.FORCED_ASSET_SALES]: BLOCKING_OVERLAY_RANK.forcedAssetSales,
    [BLOCKING_OVERLAY_ID.INSOLVENCY_POPUP]: BLOCKING_OVERLAY_RANK.insolvencyPopup,
    [BLOCKING_OVERLAY_ID.DECLINE_SCREEN]: BLOCKING_OVERLAY_RANK.declineScreen,
  };
  assert.equal(resolveTopOverlay(registry), BLOCKING_OVERLAY_ID.DECLINE_SCREEN);
});

// ---------------------------------------------------------------------------
// (a)/(b) live-mount: the REAL MapView + OverlayManagerProvider, with a
// hand-built state that forces InsolvencyPopup, ForcedAssetSalesPanel AND
// DeclineScreen to all WANT to render simultaneously (bypassing the engine
// reducer entirely — this proves the UI-layer invariant holds even if some
// future code path, or a bug, ever left more than one of these flags true at
// once; engine.ts's own force-clear, proved by bug496-497-insolvency-decline
// .test.mjs, is a SEPARATE, already-closed defence at the state layer).
// ---------------------------------------------------------------------------

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

async function mountMapViewWithOverlayManager(state: any) {
  const React = await import('react');
  const { createRoot } = await import('react-dom/client');
  const { act } = await import('react-dom/test-utils');
  const { SimContext } = await import('../src/sim/simContext.ts');
  const { BusyProvider } = await import('../src/components/Busy.tsx');
  const { MapView } = await import('../src/components/MapView.tsx');
  const { OverlayManagerProvider } = await import('../src/components/overlayManager.tsx');

  const ctx: any = {
    state,
    dispatch: () => {},
    cityName: 'Attackville',
    listSaves: () => [],
    listRecent: () => [],
    saveGame: async () => true,
    saveGameAs: async () => {},
    loadGame: async () => {},
    loadNamed: async () => {},
    renameCity: () => true,
  };

  const container = (globalThis as any).document.getElementById('root');
  const root = createRoot(container);
  await act(async () => {
    root.render(
      React.default.createElement(
        OverlayManagerProvider,
        null,
        React.default.createElement(
          SimContext.Provider,
          { value: ctx },
          React.default.createElement(BusyProvider, { children: React.default.createElement(MapView) }),
        ),
      ),
    );
  });
  return { container, root, act };
}

test('CANNOT co-mount: DeclineScreen + InsolvencyPopup + ForcedAssetSalesPanel all "wanting" to show -> only DeclineScreen renders', async () => {
  const dom: any = await installJsdom();
  try {
    const { initialState } = await import('../src/sim/engine.ts');
    const state: any = {
      ...initialState(),
      // Hand-forced co-active state — the engine would never naturally
      // produce this (it force-clears insolvencyPopup on decline), which is
      // exactly why it is the right stress case for a UI-layer invariant.
      insolvencyPopup: { state: 'crisis', enteredAt: 10 },
      bailoutState: { enteredAt: 10 },
      declineState: {
        enteredAt: 20,
        peakPopulation: 500,
        finalPopulation: 10,
        minFundsEver: -1_000_000,
        totalSpending: 5_000_000,
      },
    };

    const { container, root, act } = await mountMapViewWithOverlayManager(state);

    assert.ok(container.querySelector('.decline-screen-overlay'), 'DeclineScreen must render — it is the highest-priority active candidate');
    assert.equal(
      container.querySelector('.insolvency-popup-overlay'),
      null,
      'InsolvencyPopup must NOT be mounted while DeclineScreen is showing, even though state.insolvencyPopup is non-null',
    );
    assert.equal(
      container.querySelector('.forced-asset-sales-panel'),
      null,
      'ForcedAssetSalesPanel must NOT be mounted while DeclineScreen is showing, even though state.bailoutState is non-null',
    );

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});

test('CANNOT co-mount: InsolvencyPopup + ForcedAssetSalesPanel both wanting to show (no decline) -> only InsolvencyPopup renders', async () => {
  const dom: any = await installJsdom();
  try {
    const { initialState } = await import('../src/sim/engine.ts');
    const state: any = {
      ...initialState(),
      insolvencyPopup: { state: 'crisis', enteredAt: 10 },
      bailoutState: { enteredAt: 10 },
    };

    const { container, root, act } = await mountMapViewWithOverlayManager(state);

    assert.ok(container.querySelector('.insolvency-popup-overlay'), 'InsolvencyPopup must render');
    assert.equal(
      container.querySelector('.forced-asset-sales-panel'),
      null,
      'ForcedAssetSalesPanel must be suppressed while the higher-priority InsolvencyPopup is up',
    );

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});

test('once the higher-priority overlay clears, the lower one takes over (no permanent suppression)', async () => {
  const dom: any = await installJsdom();
  try {
    const { initialState } = await import('../src/sim/engine.ts');
    const stateBoth: any = {
      ...initialState(),
      insolvencyPopup: { state: 'crisis', enteredAt: 10 },
      bailoutState: { enteredAt: 10 },
    };
    const { container, root, act } = await mountMapViewWithOverlayManager(stateBoth);
    assert.ok(container.querySelector('.insolvency-popup-overlay'), 'precondition: popup showing');
    assert.equal(container.querySelector('.forced-asset-sales-panel'), null, 'precondition: panel suppressed');

    const { SimContext } = await import('../src/sim/simContext.ts');
    const { BusyProvider } = await import('../src/components/Busy.tsx');
    const { MapView } = await import('../src/components/MapView.tsx');
    const { OverlayManagerProvider } = await import('../src/components/overlayManager.tsx');
    const React = await import('react');

    const stateDismissed: any = { ...stateBoth, insolvencyPopup: null };
    const ctx: any = {
      state: stateDismissed,
      dispatch: () => {},
      cityName: 'Attackville',
      listSaves: () => [],
      listRecent: () => [],
      saveGame: async () => true,
      saveGameAs: async () => {},
      loadGame: async () => {},
      loadNamed: async () => {},
      renameCity: () => true,
    };
    await act(async () => {
      root.render(
        React.default.createElement(
          OverlayManagerProvider,
          null,
          React.default.createElement(
            SimContext.Provider,
            { value: ctx },
            React.default.createElement(BusyProvider, { children: React.default.createElement(MapView) }),
          ),
        ),
      );
    });

    assert.equal(container.querySelector('.insolvency-popup-overlay'), null, 'popup gone once dismissed');
    assert.ok(container.querySelector('.forced-asset-sales-panel'), 'the forced-asset-sales panel must now take over — suppression is not permanent');

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});

// ---------------------------------------------------------------------------
// (c) non-blocking chrome never intercepts a click meant for the auto-place
// control (.advisor). CSS-parse proof, matching the established pattern in
// bug500-advisor-click-overlap.test.tsx (jsdom has no elementFromPoint).
// ---------------------------------------------------------------------------

function ruleBody(css: string, selector: string): string {
  const re = new RegExp(selector.replace(/[.]/g, '\\.') + '\\s*\\{([^}]*)\\}', 's');
  const m = css.match(re);
  assert.ok(m, `${selector} CSS rule must exist`);
  return m![1];
}

function decl(body: string, prop: string): string | null {
  const re = new RegExp(prop + '\\s*:\\s*([^;]+);');
  const m = body.match(re);
  return m ? m[1].trim() : null;
}

test('non-blocking full-width banners never capture clicks: pointer-events:none on the wrapper', async () => {
  const css = await fs.readFile(stylesPath, 'utf-8');
  for (const selector of ['.playmode-banner', '.stale-build-banner', '.insolvency-banner']) {
    const body = ruleBody(css, selector);
    assert.equal(
      decl(body, 'pointer-events'),
      'none',
      `${selector} must declare pointer-events:none on its wrapper — it is chrome, not a control surface (I-3)`,
    );
  }
});

test('the stale-build-banner Reload/dismiss buttons re-enable pointer-events on themselves only (I-3 exception for interactive elements)', async () => {
  const css = await fs.readFile(stylesPath, 'utf-8');
  // These two selectors share one rule body (comma-separated), unlike the
  // single-selector rules ruleBody() targets elsewhere in this file — match
  // the rule block directly rather than forcing it through ruleBody().
  const m = css.match(/\.stale-build-banner-reload,\s*\.stale-build-banner-dismiss\s*\{([^}]*)\}/s);
  assert.ok(m, '.stale-build-banner-reload, .stale-build-banner-dismiss rule must exist');
  assert.equal(
    decl(m![1], 'pointer-events'),
    'auto',
    'the Reload/dismiss buttons must re-enable pointer-events:auto — they are the interactive controls the wrapper\'s pointer-events:none deliberately excludes',
  );
});

// ---------------------------------------------------------------------------
// (d) every blocking modal has a reachable dismiss OR self-clear path.
// ---------------------------------------------------------------------------

test('DeclineScreen has an explicit × close button (previously had NONE — audit finding)', async () => {
  const dom: any = await installJsdom();
  try {
    const { initialState } = await import('../src/sim/engine.ts');
    const state: any = {
      ...initialState(),
      declineState: {
        enteredAt: 20,
        peakPopulation: 500,
        finalPopulation: 10,
        minFundsEver: -1_000_000,
        totalSpending: 5_000_000,
      },
    };
    const { container, root, act } = await mountMapViewWithOverlayManager(state);
    const closeBtn = container.querySelector('.decline-screen-close');
    assert.ok(closeBtn, 'DeclineScreen must render an explicit close button (AC-6/I-4)');

    await act(async () => {
      closeBtn.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true, cancelable: true }));
    });
    assert.equal(container.querySelector('.decline-screen-overlay'), null, 'clicking close must hide the overlay');
    assert.ok(
      container.querySelector('.decline-reopen-chip'),
      'closing must leave a reachable reopen affordance — a hard-stop modal may never strand the player with no way back to Start Over/Load Save/Play Mode (I-4)',
    );

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});

test('InsolvencyPopup has an explicit × close button in addition to "I understand"', async () => {
  const dom: any = await installJsdom();
  try {
    const { initialState } = await import('../src/sim/engine.ts');
    const state: any = { ...initialState(), insolvencyPopup: { state: 'crisis', enteredAt: 10 } };
    const { container, root, act } = await mountMapViewWithOverlayManager(state);
    assert.ok(container.querySelector('.insolvency-popup-close'), 'InsolvencyPopup must render an explicit × close button');
    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});

test('ForcedAssetSalesPanel keeps its BUG-498 close button (regression guard for this increment\'s changes)', async () => {
  const dom: any = await installJsdom();
  try {
    const { initialState } = await import('../src/sim/engine.ts');
    const state: any = { ...initialState(), bailoutState: { enteredAt: 0 } };
    const { container, root, act } = await mountMapViewWithOverlayManager(state);
    assert.ok(container.querySelector('.forced-asset-sales-close'), 'the panel must still expose its close button');
    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});

// ---------------------------------------------------------------------------
// (e) the Z_INDEX registry is the SSOT — prove it stays in sync with the
// literal numbers actually shipped in styles.css, so the "no magic numbers"
// discipline is mechanically enforced, not just a comment.
// ---------------------------------------------------------------------------

function zIndexOf(css: string, selector: string): number {
  const body = ruleBody(css, selector);
  const z = decl(body, 'z-index');
  assert.ok(z !== null, `${selector} must declare a z-index`);
  return Number(z);
}

test('Z_INDEX registry stays in sync with styles.css (SSOT parity — a drift here means the "no magic numbers" rule silently broke)', async () => {
  const css = await fs.readFile(stylesPath, 'utf-8');
  const parity: Array<[string, keyof typeof Z_INDEX]> = [
    ['.forced-asset-sales-panel', 'FORCED_ASSET_SALES_PANEL'],
    ['.insolvency-banner', 'INSOLVENCY_BANNER'],
    ['.place-notice-banner', 'PLACE_NOTICE_BANNER'],
    ['.levelup-banner', 'LEVELUP_BANNER'],
    ['.busy-chip', 'BUSY_CHIP'],
    ['.insolvency-popup-overlay', 'INSOLVENCY_POPUP'],
    ['.perf-hud', 'PERF_HUD'],
    ['.decline-screen-overlay', 'DECLINE_SCREEN'],
    ['.decline-reopen-chip', 'DECLINE_REOPEN_CHIP'],
    ['.playmode-banner', 'PLAYMODE_BANNER'],
    ['.stale-build-banner', 'STALE_BUILD_BANNER'],
  ];
  for (const [selector, key] of parity) {
    assert.equal(
      zIndexOf(css, selector),
      Z_INDEX[key],
      `Z_INDEX.${key} (${Z_INDEX[key]}) must equal ${selector}'s literal z-index in styles.css — update overlayLayers.ts AND styles.css together`,
    );
  }
});

test('Z_INDEX.REBUILD_PROMPT beats every other registered value (the boot-time engine gate must win over everything)', () => {
  const values = Object.entries(Z_INDEX).filter(([k]) => k !== 'REBUILD_PROMPT');
  for (const [k, v] of values) {
    assert.ok(Z_INDEX.REBUILD_PROMPT > (v as number), `REBUILD_PROMPT must beat ${k} (${v})`);
  }
});

test('the priority order and the z-index order agree for the three MapView-hosted blocking overlays (no inversion, I-1)', async () => {
  const css = await fs.readFile(stylesPath, 'utf-8');
  const declineZ = zIndexOf(css, '.decline-screen-overlay');
  const popupZ = zIndexOf(css, '.insolvency-popup-overlay');
  const panelZ = zIndexOf(css, '.forced-asset-sales-panel');
  assert.ok(declineZ > popupZ, 'DeclineScreen (highest resolver priority) must also have the highest z-index');
  assert.ok(popupZ > panelZ, 'InsolvencyPopup (mid resolver priority) must also beat ForcedAssetSalesPanel in z-index');
});
