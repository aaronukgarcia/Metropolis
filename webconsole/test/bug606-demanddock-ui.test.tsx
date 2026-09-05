// bug606-demanddock-ui.test.tsx — BUG-606 UI half: the visible per-row sizing
// message (Aaron: "'citizens want shops' no help ... a clue would be nice")
// and the DemandDock header's "Fix All" button (Aaron: "next to the word
// demand for the right tab I want a fix-all button").
//
// Live-mount pattern copied from test/demand-fix-ui.test.tsx (createRoot +
// jsdom + SimContext.Provider fixture, dispatch spy) — this suite's
// established way to prove real component click-wiring.

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

function shortfallState(initialState: any, population: number, fundsOverride = 1_000_000_000) {
  return { ...initialState(), population, unlockedAll: true, funds: fundsOverride, administrationState: null };
}

async function mountReal(Component: any, ctx: any) {
  const React = await import('react');
  const { createRoot } = await import('react-dom/client');
  const { act } = await import('react-dom/test-utils');
  const { SimContext } = await import('../src/sim/simContext.ts');
  const { BusyProvider } = await import('../src/components/Busy.tsx');

  const container = (globalThis as any).document.getElementById('root');
  const root = createRoot(container);
  await act(async () => {
    root.render(
      React.default.createElement(
        SimContext.Provider,
        { value: ctx },
        React.default.createElement(BusyProvider, { children: React.default.createElement(Component) }),
      ),
    );
  });
  return { container, root, act };
}

function makeCtx(state: any) {
  const calls: { type?: string; serviceKey?: string }[] = [];
  const ctx: any = {
    state,
    dispatch: (action: any) => calls.push(action),
    cityName: 'Attackville',
    listSaves: () => [],
    listRecent: () => [],
    saveGame: async () => true,
    saveGameAs: async () => {},
    loadGame: async () => {},
    loadNamed: async () => {},
    renameCity: () => true,
  };
  return { ctx, calls };
}

// ---------------------------------------------------------------------------
// (a) Visible per-row sizing message (BUG-606).
// ---------------------------------------------------------------------------

test('BUG-606 (a): a shortfall row renders a VISIBLE sizing+recommendation line (not just a hover title)', async () => {
  const dom: any = await installJsdom();
  try {
    const { initialState } = await import('../src/sim/engine.ts');
    const { demandFixPlan } = await import('../src/sim/data.ts');
    const { DemandDock } = await import('../src/components/left/DemandDock.tsx');
    const { demandFixMessage } = await import('../src/components/demandFixUi.ts');

    const state = shortfallState(initialState, 16_800);
    const plan = demandFixPlan(state);
    const water = plan.find((p: any) => p.serviceKey === 'cleanwater');
    assert.ok(water, 'precondition: cleanwater must be in shortfall at this population');

    const { ctx } = makeCtx(state);
    const { container, root, act } = await mountReal(DemandDock, ctx);

    const hints = Array.from(container.querySelectorAll('.fix-hint')) as any[];
    assert.ok(hints.length > 0, 'at least one visible fix-hint line must render for a city in shortfall');
    const expected = demandFixMessage(water);
    const waterHint = hints.find((h) => (h.textContent ?? '') === expected);
    assert.ok(
      waterHint,
      `expected a visible hint reading "${expected}" among: ${hints.map((h) => h.textContent).join(' | ')}`
    );

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});

test('BUG-606 (a): no shortfall -> no fix-hint lines render', async () => {
  const dom: any = await installJsdom();
  try {
    const { initialState } = await import('../src/sim/engine.ts');
    const { DemandDock } = await import('../src/components/left/DemandDock.tsx');
    const state = shortfallState(initialState, 0);
    const { ctx } = makeCtx(state);
    const { container, root, act } = await mountReal(DemandDock, ctx);
    assert.equal(container.querySelectorAll('.fix-hint').length, 0);
    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});

// ---------------------------------------------------------------------------
// (b) Fix All button — header placement, disabled state, dispatch wiring.
// ---------------------------------------------------------------------------

test('BUG-606 (b): Fix All renders in the Demand panel header and dispatches resolveDemandAll when clicked', async () => {
  const dom: any = await installJsdom();
  try {
    const { initialState } = await import('../src/sim/engine.ts');
    const { orderedDemandFixPlan } = await import('../src/sim/data.ts');
    const { DemandDock } = await import('../src/components/left/DemandDock.tsx');

    const state = shortfallState(initialState, 16_800);
    assert.ok(orderedDemandFixPlan(state).length > 0, 'precondition: a real multi-service shortfall must exist');

    const { ctx, calls } = makeCtx(state);
    const { container, root, act } = await mountReal(DemandDock, ctx);

    const header = container.querySelector('.panel-h');
    assert.ok(header, 'precondition: the Demand panel header must render');
    const fixAllBtn = header!.querySelector('.fix-all-btn') as any;
    assert.ok(fixAllBtn, 'a Fix All button must render in the Demand panel header');
    assert.ok(!fixAllBtn.disabled, 'Fix All must be enabled when a real shortfall exists');

    await act(async () => {
      fixAllBtn.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true, cancelable: true }));
      await new Promise((resolve) => setTimeout(resolve, 80));
    });

    // RETUNE (this session, post-BUG-685 largest-first landing): this
    // fixture's Fix All batch now genuinely expands to a real multi-service,
    // multi-spec mix (data.ts largestFirstFill()) whose aggregate marginal
    // wage bill trips placementGate.ts's own pre-existing (BUG-652 round r4)
    // recurring-cost confirm — a correct consequence of the mix now being
    // fully/honestly expanded (DemandDock.tsx's own BUG-685 comment: "expand
    // every service's REAL mix ... not a monoculture"), not a regression in
    // this fix. Drive the SAME real flow a player would: confirm through the
    // "Big commitment" dialog before asserting the dispatch landed.
    let dialog = container.querySelector('[role="dialog"]');
    if (dialog) {
      assert.equal(calls.length, 0, 'the gate must not dispatch before the player confirms');
      const buildAnywayBtn = Array.from(dialog.querySelectorAll('button')).find(
        (b: any) => /build anyway/i.test(b.textContent ?? '')
      ) as any;
      assert.ok(buildAnywayBtn, 'a real recurring-cost confirm must offer a "Build anyway" button');
      await act(async () => {
        buildAnywayBtn.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true, cancelable: true }));
        await new Promise((resolve) => setTimeout(resolve, 80));
      });
    }

    assert.equal(calls.length, 1, 'clicking Fix All (confirming the recurring-cost gate if one appears) must dispatch exactly one action');
    assert.deepEqual(calls[0], { type: 'resolveDemandAll' }, 'Fix All must dispatch the single batched resolveDemandAll action');

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});

test('BUG-606 (b): Fix All is disabled (with a tooltip) when nothing is in deficit', async () => {
  const dom: any = await installJsdom();
  try {
    const { initialState } = await import('../src/sim/engine.ts');
    const { orderedDemandFixPlan } = await import('../src/sim/data.ts');
    const { DemandDock } = await import('../src/components/left/DemandDock.tsx');

    const state = shortfallState(initialState, 0);
    assert.equal(orderedDemandFixPlan(state).length, 0, 'precondition: zero population means zero real shortfall');

    const { ctx, calls } = makeCtx(state);
    const { container, root, act } = await mountReal(DemandDock, ctx);

    const fixAllBtn = container.querySelector('.fix-all-btn') as any;
    assert.ok(fixAllBtn, 'the Fix All button must still render (disabled, not absent)');
    assert.ok(fixAllBtn.disabled, 'Fix All must be disabled when nothing is in deficit');
    assert.ok(fixAllBtn.title && fixAllBtn.title.length > 0, 'a disabled Fix All must still carry an explanatory tooltip');

    // Clicking a disabled button must never dispatch (jsdom honours the
    // disabled attribute for click dispatch on a real <button>).
    await act(async () => {
      fixAllBtn.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true, cancelable: true }));
      await new Promise((resolve) => setTimeout(resolve, 80));
    });
    assert.equal(calls.length, 0, 'a disabled Fix All must never dispatch');

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});
