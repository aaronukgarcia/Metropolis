// demand-fix-ui.test.tsx — FEAT-2326609728 inc2: the one-click demand-fix UI.
//
// The engine core (demandFixPlan()/'resolveDemand', inc1, ac7291e) is landed +
// rounded and untouched here. This increment is UI-only: the MapView advisor
// prompt is rewired to quantify the fix ("place N <building>s"), and the
// DemandDock service rows each grow a "Fix (N)" button — both reading
// demandFixPlan(state)/worstDemandFix(state) (src/components/demandFixUi.ts,
// UI-side SSOT so the two surfaces never diverge) and dispatching the SAME
// {type:'resolveDemand', serviceKey} inc1 action, never a second placement
// path.
//
// Live-mount pattern follows test/bug500-advisor-click-overlap.test.tsx
// (createRoot + jsdom + SimContext.Provider fixture, dispatch spy) — the
// established way this suite proves a real component's click wiring without
// re-deriving hit-testing jsdom cannot do.
//
// RED-PROOF: each assertion below is shown to be able to fail —
//  (a) is checked against an EXPECTED count computed independently in this
//      file (recomputed from demandFixPlan()'s own need/have/gap fields, not
//      by importing worstDemandFix) — if MapView's wiring drops the +5%
//      headroom, picks the wrong service, or hardcodes a stale N, the
//      expected/actual strings diverge and the assertion reddens;
//  (b) is checked with the SAME independent recompute against a specific
//      DemandDock row plan, and separately proven capable of going red by
//      first asserting the row-lookup Map actually contains the key (a typo'd
//      serviceKey lookup would silently render zero buttons and this would
//      still show a false pass without that precondition check);
//  (c) is checked against a state that has an early-return "Welcome" advisor
//      branch disabled (one residential building placed, o population) but
//      zero real service demand — proving "no button" is a real gate, not
//      just an artifact of the welcome-screen short-circuit.

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

/** Mirrors test/demand-fix.test.mjs's shortfallState(): a real population,
 *  no service buildings, everything unlocked, ample funds — guarantees every
 *  population-scaled service is genuinely in shortfall. */
function shortfallState(initialState: any, population: number, fundsOverride = 1_000_000_000) {
  return { ...initialState(), population, unlockedAll: true, funds: fundsOverride, administrationState: null };
}

/** Independent recompute of "the most-pressing demandFixPlan() entry" —
 *  deliberately NOT calling worstDemandFix() from demandFixUi.ts, so this is a
 *  real cross-check of that function's gap/tie-break contract, not a tautology. */
function expectedWorstFix(plan: any[]) {
  const ranked = plan
    .map((item) => ({ item, gap: item.need * 1.05 - item.have }))
    .sort((a, b) => b.gap - a.gap || a.item.serviceKey.localeCompare(b.item.serviceKey));
  return ranked[0]?.item ?? null;
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
// (a) MapView advisor: quantified "place N <building>s" prompt + click wiring.
// ---------------------------------------------------------------------------

test('FEAT-2326609728 inc2 (a): advisor shows "place N <building>s" with the correct N and clicking dispatches resolveDemand', async () => {
  const dom: any = await installJsdom();
  try {
    const { initialState } = await import('../src/sim/engine.ts');
    const { demandFixPlan, SPECS } = await import('../src/sim/data.ts');
    const { MapView } = await import('../src/components/MapView.tsx');

    // population 8,000: every population-scaled service (gp/hosp/police/fire/
    // cleanwater/waste) ties on gap (need*1.05 = 8,400), and 'cleanwater' wins
    // the alphabetical tie-break. FEAT-demanddock-overhaul: optimalProvider()'s
    // "1 dam not 20 towers" clears-in-one branch now picks Water Works
    // (wat_clean, served 20,000, best cost-per-capacity of the units that can
    // singlehandedly clear 8,400) over Water Tower — a single unit, count 1.
    const state = shortfallState(initialState, 8000);
    const plan = demandFixPlan(state);
    const top = expectedWorstFix(plan);
    assert.ok(top, 'precondition: population 8,000 with zero service buildings must yield a real shortfall');
    assert.equal(top.serviceKey, 'cleanwater', 'precondition: cleanwater must win the deterministic tie-break at this population');
    assert.equal(top.count, 1, 'precondition: one Water Works (served 20,000) clears the 8,400 shortfall in a single unit');
    const spName = SPECS[top.specId].name;
    assert.equal(spName, 'Water Works');

    const { ctx, calls } = makeCtx(state);
    const { container, root, act } = await mountReal(MapView, ctx);

    const advisor = container.querySelector('.advisor');
    assert.ok(advisor, 'precondition: the advisor element must render');
    const text = advisor!.textContent ?? '';
    // BUG-587: "N x <Name>" (formatBuildingCount), not the old English
    // pluraliser's bare "N <Name+s/es>" — Water Works is itself the hazard
    // case (already ends in -s) that the old rule mangled into "Water
    // Workses"; assert BOTH the correct shape and the absence of the bug.
    assert.ok(
      text.includes(`place ${top.count} x Water Works`),
      `advisor text must quantify the fix as "place ${top.count} x Water Works", got: "${text}"`,
    );
    assert.ok(!/Water Workses/.test(text), `advisor text must not double-pluralise "Water Works": "${text}"`);
    assert.ok(text.includes('clean water'), 'advisor text must name the service the fix clears');
    assert.ok(container.querySelector('.advisor.clickable'), 'the advisor must be clickable when a fix is offered');

    await act(async () => {
      advisor!.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true, cancelable: true }));
      // advisorContent.go routes through useBusy().run() (real setTimeout) — see
      // the established bug500 pattern.
      await new Promise((resolve) => setTimeout(resolve, 80));
    });

    assert.equal(calls.length, 1, 'clicking the advisor must dispatch exactly one action');
    assert.deepEqual(
      calls[0],
      { type: 'resolveDemand', serviceKey: 'cleanwater' },
      'the dispatched action must be resolveDemand for the SAME serviceKey the prompt quantified',
    );

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});

// ---------------------------------------------------------------------------
// (b) DemandDock per-row "Fix (N)" button.
// ---------------------------------------------------------------------------

test('FEAT-2326609728 inc2 (b): a DEMAND-panel row in shortfall shows a "Fix (N)" button that dispatches resolveDemand', async () => {
  const dom: any = await installJsdom();
  try {
    const { initialState } = await import('../src/sim/engine.ts');
    const { demandFixPlan, SPECS } = await import('../src/sim/data.ts');
    const { DemandDock } = await import('../src/components/left/DemandDock.tsx');

    const state = shortfallState(initialState, 8000);
    const plan = demandFixPlan(state);
    const water = plan.find((p: any) => p.serviceKey === 'cleanwater');
    assert.ok(water, 'precondition: cleanwater must be in the plan (row-lookup key must actually exist — a typo would silently show zero buttons)');
    // FEAT-demanddock-overhaul: at pop 8,000 optimalProvider() picks Water
    // Works (1 unit clears the 8,400 shortfall) over Water Tower — see (a).
    assert.equal(water.count, 1);
    assert.equal(SPECS[water.specId].name, 'Water Works');

    const { ctx, calls } = makeCtx(state);
    const { container, root, act } = await mountReal(DemandDock, ctx);

    const buttons = Array.from(container.querySelectorAll('.demand-fix-btn')) as any[];
    assert.ok(buttons.length > 0, 'at least one Fix button must render for a city in shortfall');
    // FEAT-demanddock-overhaul: at pop 8,000 several rows independently show
    // "Fix (1)" (cleanwater AND hosp both clear in one unit) — disambiguate by
    // the button's title, which names the specific spec (fixTitle embeds
    // SPECS[fix.specId].name), not just the count.
    const waterBtn = buttons.find((b) => /Fix \(1\)/.test(b.textContent ?? '') && /Water Works/.test(b.title ?? ''));
    assert.ok(
      waterBtn,
      `expected a "Fix (1)" Water Works button for the clean-water row among: ${buttons.map((b) => `${b.textContent}/${b.title}`).join(', ')}`
    );
    // BUG-587: the title must read "1 x Water Works" (formatBuildingCount),
    // never the old English pluraliser's mangled "Water Workses".
    assert.ok(/1 x Water Works/.test(waterBtn!.title ?? ''), `Fix button title must read "1 x Water Works", got: "${waterBtn!.title}"`);
    assert.ok(!/Water Workses/.test(waterBtn!.title ?? ''), `Fix button title must not double-pluralise "Water Works": "${waterBtn!.title}"`);

    await act(async () => {
      waterBtn!.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true, cancelable: true }));
      await new Promise((resolve) => setTimeout(resolve, 80));
    });

    assert.equal(calls.length, 1, 'clicking Fix must dispatch exactly one action');
    assert.deepEqual(calls[0], { type: 'resolveDemand', serviceKey: 'cleanwater' });

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});

// ---------------------------------------------------------------------------
// (c) No shortfall -> no button, no advisor fix-prompt.
// ---------------------------------------------------------------------------

test('FEAT-2326609728 inc2 (c): no shortfall -> the advisor offers no fix-prompt and DemandDock shows no Fix buttons', async () => {
  const dom: any = await installJsdom();
  try {
    const { initialState } = await import('../src/sim/engine.ts');
    const { demandFixPlan } = await import('../src/sim/data.ts');
    const { MapView } = await import('../src/components/MapView.tsx');
    const { DemandDock } = await import('../src/components/left/DemandDock.tsx');

    // One residential building placed (so c.residential > 0 and the advisor's
    // very first "Welcome to the Hythe turning" early-return does NOT fire —
    // that branch would trivially show no fix-prompt for an unrelated reason
    // and make this test vacuous) but population 0, so every population-scaled
    // service's `need` is 0 and demandFixPlan() is genuinely empty.
    // A lone res_hut generates a small fixed refuse tonnage regardless of
    // actual population (wasteGeneratedOf sums sp.residents, not live
    // occupancy) — so a waste_depot (50 t/tick, comfortably above one hut's
    // 8-resident tonnage) is placed alongside it to keep 'refuse' out of the
    // plan too, isolating this test to the population-scaled services.
    const base = shortfallState(initialState, 0);
    const state = {
      ...base,
      buildings: [
        { id: 1, spec: 'res_hut', x: 10, y: 10 },
        { id: 2, spec: 'waste_depot', x: 20, y: 20 },
      ],
    };
    const plan = demandFixPlan(state);
    assert.equal(plan.length, 0, 'precondition: zero population means zero real service demand');

    const { ctx: mapCtx } = makeCtx(state);
    const map = await mountReal(MapView, mapCtx);
    const advisorText = map.container.querySelector('.advisor')?.textContent ?? '';
    assert.ok(
      !/Do you want to place \d+ /.test(advisorText),
      `advisor must not show a quantified fix-prompt with no real shortfall, got: "${advisorText}"`,
    );
    await map.act(async () => {
      map.root.unmount();
    });

    const { ctx: dockCtx } = makeCtx(state);
    const dock = await mountReal(DemandDock, dockCtx);
    const fixButtons = dock.container.querySelectorAll('.demand-fix-btn');
    assert.equal(fixButtons.length, 0, 'DemandDock must render zero Fix buttons when nothing is in shortfall');
    await dock.act(async () => {
      dock.root.unmount();
    });
  } finally {
    dom.window.close();
  }
});

// ---------------------------------------------------------------------------
// (d) BUG-587: a hazard-class catalogue name (Water Works, count > 1) renders
// correctly ("N x Name") on BOTH surfaces — the old pluralizeBuildingName()
// English -s/-x/-z/-ch/-sh -> -es rule mangled this exact name into
// "Water Workses" whenever count !== 1 (count === 1 returned the name
// unchanged, so the bug was invisible at count 1 — this test deliberately
// picks a population where cleanwater's fix needs 2 units).
//
// RED-PROOF: reverting demandFixUi.ts's formatBuildingCount() to the old
// `pluralizeBuildingName` body (count===1 ? name : name+'es' for this
// -s-ending case) makes both /Water Workses/ assertions below fail — hand-
// verified during development and re-confirmed by temporarily restoring the
// old implementation and re-running this test.
// ---------------------------------------------------------------------------

test('BUG-587 (d): "Water Works" x2 renders as "2 x Water Works" (never "Water Workses") on both the MapView advisor and the DemandDock Fix button', async () => {
  const dom: any = await installJsdom();
  try {
    const { initialState } = await import('../src/sim/engine.ts');
    const { demandFixPlan, SPECS } = await import('../src/sim/data.ts');
    const { MapView } = await import('../src/components/MapView.tsx');
    const { DemandDock } = await import('../src/components/left/DemandDock.tsx');

    // Population 20,000: cleanwater's shortfall needs 2x Water Works (served
    // 20,000/unit) and independently wins the worst-gap ranking — verified by
    // direct exploration of demandFixPlan() at this population during
    // development (see PR notes); re-verified here as a precondition so a
    // future SPECS/optimalProvider() rebalance that changes the winning
    // service or count fails LOUD rather than silently making this test
    // vacuous.
    const state = shortfallState(initialState, 20000);
    const plan = demandFixPlan(state);
    const top = expectedWorstFix(plan);
    assert.ok(top, 'precondition: population 20,000 must yield a real shortfall');
    assert.equal(top.serviceKey, 'cleanwater', 'precondition: cleanwater must win the worst-gap ranking at this population');
    assert.equal(top.count, 2, 'precondition: 2x Water Works must be needed to clear the 20,000-population shortfall');
    assert.equal(SPECS[top.specId].name, 'Water Works');

    const { ctx: mapCtx } = makeCtx(state);
    const map = await mountReal(MapView, mapCtx);
    const advisorText = map.container.querySelector('.advisor')?.textContent ?? '';
    assert.ok(
      advisorText.includes('place 2 x Water Works'),
      `advisor text must read "place 2 x Water Works", got: "${advisorText}"`,
    );
    assert.ok(!/Water Workses/.test(advisorText), `advisor text must not double-pluralise "Water Works": "${advisorText}"`);
    await map.act(async () => {
      map.root.unmount();
    });

    const { ctx: dockCtx } = makeCtx(state);
    const dock = await mountReal(DemandDock, dockCtx);
    const buttons = Array.from(dock.container.querySelectorAll('.demand-fix-btn')) as any[];
    const waterBtn = buttons.find((b) => /Fix \(2\)/.test(b.textContent ?? '') && /Water Works/.test(b.title ?? ''));
    assert.ok(
      waterBtn,
      `expected a "Fix (2)" Water Works button among: ${buttons.map((b) => `${b.textContent}/${b.title}`).join(', ')}`,
    );
    assert.ok(/2 x Water Works/.test(waterBtn!.title ?? ''), `Fix button title must read "2 x Water Works", got: "${waterBtn!.title}"`);
    assert.ok(!/Water Workses/.test(waterBtn!.title ?? ''), `Fix button title must not double-pluralise "Water Works": "${waterBtn!.title}"`);
    await dock.act(async () => {
      dock.root.unmount();
    });
  } finally {
    dom.window.close();
  }
});
