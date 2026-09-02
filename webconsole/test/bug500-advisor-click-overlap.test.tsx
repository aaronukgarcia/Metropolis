// bug500-advisor-click-overlap.test.tsx — BUG-500 (P1): the info/overlay
// captures clicks over the auto-place advisor control.
//
// The advisor "click to place it now at the best site" control
// (.advisor.clickable, top:8px left:8px) and the Forced Asset Sales panel
// (.forced-asset-sales-panel, top:8px left:8px, z-index:17) used to occupy
// the SAME top-left corner. Since the panel declares an explicit z-index
// (17) and the advisor does not (z-index:auto, which always loses to any
// element with an explicit stacking-context z-index regardless of DOM
// order), the panel painted OVER the advisor and captured its clicks for the
// entire bailout — auto-place was unreachable.
//
// jsdom does not implement real layout/hit-testing (document.elementFromPoint
// is not a function under jsdom — verified directly), so this suite proves
// the fix the same way the codebase already proves stacking bugs elsewhere
// (see mount.test.tsx's "BUG-497 (3): decline-screen-overlay is pinned above
// every other known overlay z-index in styles.css" test): by parsing the
// actual shipped styles.css and asserting the two rules no longer claim the
// same anchor point. This is a real, deterministic, CI-safe proof of the
// exact regression (the SAME anchor is the whole bug), and it is paired with
// a live-mount test proving the advisor's click handler still fires
// correctly on the real DOM node post-fix.
//
// RED PROOF: reverting the styles.css position for .forced-asset-sales-panel
// to `top: 8px; left: 8px;` (the pre-fix value, restored via a scratch
// cp/mv — GR#24, never a git revert) turns the first test in this file red,
// because the two rules then declare an identical (position, top, left)
// anchor again.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const stylesPath = path.resolve(here, '../src/styles.css');

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

test('BUG-500: .forced-asset-sales-panel must declare a position (top/left) rule', async () => {
  const css = await fs.readFile(stylesPath, 'utf-8');
  const panelBody = ruleBody(css, '.forced-asset-sales-panel');
  assert.equal(decl(panelBody, 'position'), 'absolute');
  assert.ok(decl(panelBody, 'left') !== null, '.forced-asset-sales-panel must declare a left anchor');
});

test('BUG-500: .advisor and .forced-asset-sales-panel must NOT share the same absolute anchor', async () => {
  const css = await fs.readFile(stylesPath, 'utf-8');
  const advisorBody = ruleBody(css, '.advisor');
  const panelBody = ruleBody(css, '.forced-asset-sales-panel');

  const advisorPos = { position: decl(advisorBody, 'position'), top: decl(advisorBody, 'top'), left: decl(advisorBody, 'left') };
  const panelPos = { position: decl(panelBody, 'position'), top: decl(panelBody, 'top'), left: decl(panelBody, 'left') };

  const sameAnchor =
    advisorPos.position === panelPos.position &&
    advisorPos.top === panelPos.top &&
    advisorPos.left === panelPos.left;

  assert.ok(
    !sameAnchor,
    `.advisor and .forced-asset-sales-panel must not claim the same corner — both currently declare ` +
      `position:${panelPos.position} top:${panelPos.top} left:${panelPos.left}, which is exactly the ` +
      'BUG-500 overlap (the higher-z-index panel overdraws and click-blocks the advisor for the whole bailout)',
  );
});

test('BUG-500: the .forced-asset-sales-panel z-index (17) beats .advisor (z-index:auto) — proving the corner MUST differ, not the z-order', async () => {
  const css = await fs.readFile(stylesPath, 'utf-8');
  const advisorBody = ruleBody(css, '.advisor');
  const panelBody = ruleBody(css, '.forced-asset-sales-panel');
  assert.equal(decl(advisorBody, 'z-index'), null, 'the advisor intentionally has no z-index (stacks at auto)');
  assert.equal(decl(panelBody, 'z-index'), '17', 'the panel keeps its z-index — raising the advisor above it is NOT the sanctioned fix (task brief: do not just raise z-index blindly)');
});

// ---------------------------------------------------------------------------
// Live-mount companion: prove the advisor's click handler still fires
// correctly against the real component tree with BOTH the panel and a
// clickable advisor mounted simultaneously (the exact BUG-500 scenario).
// jsdom cannot hit-test real paint order, so this does not re-prove the
// overlap (the CSS tests above do that) — it proves the fix did not break
// the advisor's own click wiring while moving the panel.

function installJsdom() {
  // Lazy require to keep this file loadable even if jsdom's ESM export shape
  // ever changes; the store-dispatch.test.tsx pattern does the same.
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

test('BUG-500 companion: with the Forced Asset Sales panel AND a clickable advisor both mounted, clicking the advisor node fires its handler', async () => {
  const dom: any = await installJsdom();
  try {
    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimContext } = await import('../src/sim/simContext.ts');
    const { BusyProvider } = await import('../src/components/Busy.tsx');
    const { MapView } = await import('../src/components/MapView.tsx');
    const { initialState } = await import('../src/sim/engine.ts');

    // unlockedAll + ample funds + a real population with zero service buildings
    // reliably drives pickAutoSpec() to offer an affordable auto-build (verified
    // directly against src/sim/data.ts's pickAutoSpec), so advisorContent.go is a
    // real function and the advisor renders with the .clickable modifier — the
    // SAME condition the bug report describes ("auto-place unreachable"). An
    // active bailoutState mounts the Forced Asset Sales panel at the same time.
    const state: any = {
      ...initialState(),
      population: 2000,
      unlockedAll: true,
      funds: 50_000_000,
      bailoutState: { enteredAt: 0 },
    };

    let dispatchCalls = 0;
    const ctx: any = {
      state,
      dispatch: () => {
        dispatchCalls++;
      },
      cityName: 'Attackville',
      listSaves: () => [],
      listRecent: () => [],
      saveGame: async () => true,
      saveGameAs: async () => {},
      loadGame: async () => {},
      loadNamed: async () => {},
      renameCity: () => true,
    };

    const container = dom.window.document.getElementById('root');
    const root = createRoot(container);
    await act(async () => {
      root.render(
        React.default.createElement(
          SimContext.Provider,
          { value: ctx },
          React.default.createElement(BusyProvider, { children: React.default.createElement(MapView) }),
        ),
      );
    });

    const panel = container.querySelector('.forced-asset-sales-panel');
    assert.ok(panel, 'precondition: the Forced Asset Sales panel must be mounted (bailoutState active)');
    const advisor = container.querySelector('.advisor.clickable');
    assert.ok(advisor, 'precondition: the advisor must be rendering with the clickable modifier (auto-build offered)');

    await act(async () => {
      advisor!.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true, cancelable: true }));
      // advisorContent.go routes through useBusy().run(), which defers the actual
      // dispatch behind a real setTimeout (BusyProvider, src/components/Busy.tsx) —
      // wait it out with real timers so the assertion below observes the dispatch.
      await new Promise((resolve) => setTimeout(resolve, 80));
    });

    assert.ok(dispatchCalls > 0, 'clicking the advisor node must fire its onClick handler (the auto-place action)');

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});
