// bug498-forced-sales-dismiss.test.tsx — BUG-498 (P1): Forced Asset Sales
// window has no working close.
//
// The panel (ForcedAssetSalesPanel, MapView.tsx) used to render only "Enter
// Administration" + per-asset "Sell" — no explicit dismiss. Its visibility is
// year-pinned BY DESIGN (bailoutState/bailoutSecondState clears at year-end
// or on enterAdministration, per engine.ts) — not a hard trap — but the
// player had no way to get it off the screen mid-year.
//
// The fix adds a UI-ONLY "dismiss" affordance (component-local React state,
// never touching SimState/dispatch — GR#3: no second source of truth for
// money) that hides the panel for the current bailout without altering
// bailoutState/bailoutSecondState/funds in any way.
//
// This suite mounts the REAL MapView with react-dom/client + jsdom (so real
// click handlers and real state hooks run — renderToString cannot exercise
// onClick), drives an actual click on the dismiss button, and asserts:
//   (a) the panel disappears from the DOM after the click;
//   (b) dismissing NEVER dispatches any action (funds/bailout state
//       untouched — the SimState the panel reads never changes because of
//       this button).
//
// RED PROOF: before the MapView.tsx fix (no dismiss button rendered at all),
// `container.querySelector('.forced-asset-sales-close')` is null, so the
// click-and-assert-gone flow in the first test below cannot even find a
// button to click — verified directly against a scratch copy of MapView.tsx
// with the close button JSX removed (GR#24 — no git revert used).

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

async function mountPanel(bailoutState: any) {
  const React = await import('react');
  const { createRoot } = await import('react-dom/client');
  const { act } = await import('react-dom/test-utils');
  const { SimContext } = await import('../src/sim/simContext.ts');
  const { BusyProvider } = await import('../src/components/Busy.tsx');
  const { MapView } = await import('../src/components/MapView.tsx');
  const { initialState } = await import('../src/sim/engine.ts');

  const state: any = { ...initialState(), bailoutState };
  let dispatchCalls: any[] = [];
  const ctx: any = {
    state,
    dispatch: (action: any) => {
      dispatchCalls.push(action);
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

  const container = (globalThis as any).document.getElementById('root');
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

  return { container, root, act, dispatchCalls, state };
}

test('BUG-498: the Forced Asset Sales panel renders an explicit dismiss/close control', async () => {
  const dom: any = await installJsdom();
  try {
    const { container, root, act } = await mountPanel({ enteredAt: 0 });
    const panel = container.querySelector('.forced-asset-sales-panel');
    assert.ok(panel, 'precondition: the panel must be mounted while bailoutState is active');
    const closeBtn = container.querySelector('.forced-asset-sales-close');
    assert.ok(closeBtn, 'BUG-498: the panel must render an explicit dismiss/close button');
    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});

test('BUG-498: clicking dismiss hides the panel', async () => {
  const dom: any = await installJsdom();
  try {
    const { container, root, act } = await mountPanel({ enteredAt: 0 });
    const closeBtn = container.querySelector('.forced-asset-sales-close');
    assert.ok(closeBtn, 'precondition: dismiss button must exist to click it');

    await act(async () => {
      closeBtn.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true, cancelable: true }));
    });

    assert.equal(
      container.querySelector('.forced-asset-sales-panel'),
      null,
      'the panel must no longer be rendered after the player clicks dismiss',
    );

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});

test('BUG-498: dismissing the panel NEVER dispatches an action (UI-only — bailout/funds untouched, GR#3)', async () => {
  const dom: any = await installJsdom();
  try {
    const { container, root, act, dispatchCalls, state } = await mountPanel({ enteredAt: 0 });
    const closeBtn = container.querySelector('.forced-asset-sales-close');
    assert.ok(closeBtn, 'precondition: dismiss button must exist to click it');

    const fundsBefore = state.funds;
    const bailoutBefore = state.bailoutState;

    await act(async () => {
      closeBtn.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true, cancelable: true }));
    });

    assert.equal(dispatchCalls.length, 0, `dismiss must not dispatch any action, but dispatched: ${JSON.stringify(dispatchCalls)}`);
    assert.equal(state.funds, fundsBefore, 'funds must be untouched by a dismiss (no second source of truth for money)');
    assert.equal(state.bailoutState, bailoutBefore, 'bailoutState must be untouched by a dismiss (the bailout is NOT cancelled)');

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});

test('BUG-498 MUTATION-PROVE target: a state with no active bailout renders no panel and no close button at all', async () => {
  const dom: any = await installJsdom();
  try {
    const { container, root, act } = await mountPanel(null);
    assert.equal(container.querySelector('.forced-asset-sales-panel'), null, 'no bailout active — the panel must not render');
    assert.equal(container.querySelector('.forced-asset-sales-close'), null, 'no bailout active — no close button either');
    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});
