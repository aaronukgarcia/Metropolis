// utilities-buyin.test.tsx — FEAT-2326609711 inc2: UI render smoke tests
// (AC-8/AC-9/AC-10). Split from utilities-buyin.test.mjs because .tsx
// component imports require the `tsx --test` runner (package.json's "test"
// script runs .test.mjs under plain node --test and .test.tsx separately
// under tsx --test — mirrors mount.test.tsx's split exactly).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import { WATER_IMPORT_OUTFLOW_LABEL } from '../src/sim/fiscal.ts';
import { JSDOM } from 'jsdom';
import { JOURNAL_KEY } from '../src/sim/journal.ts';

function waterShortageCity(overrides: any = {}) {
  const s: any = { ...initialState(), buildings: [], population: 1000, ...overrides };
  if (!overrides.buildings) {
    let id = 600001;
    for (let i = 0; i < 3; i++) s.buildings.push({ id: id++, spec: 'pow_wind', x: 20 + i, y: 20 });
  }
  return s;
}

function surplusWaterCity(overrides: any = {}) {
  const s: any = { ...initialState(), buildings: [], population: 1000, ...overrides };
  s.buildings.push({ id: 800001, spec: 'wat_tower', x: 10, y: 10 });
  s.buildings.push({ id: 800002, spec: 'wat_waste', x: 12, y: 10 });
  return s;
}

// BUG-1050 (P3, mutant M13): the panel's displayed cost must be the EXACT
// figure fiscal.ts's SSOT booked, not a locally re-derived (and possibly
// drifted/halved) arithmetic. Expected is computed here via the SAME
// exported fiscal helper the component now calls — the round's own
// convention (see grid-import.test.mjs's AC-2 tests) — so this pins the
// wiring (component -> fiscal.ts), not a re-typed formula.
test('BUG-1050: PowerTab/WaterTab/WasteTab render the EXACT fiscal-derived cost figure, never a local re-derivation', async () => {
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { PowerTab, WaterTab, WasteTab } = await import('../src/components/left/tabs/servicesTabs.tsx');
  const { SimContext } = await import('../src/sim/simContext.ts');
  const { powerStats, waterBalanceOf, waterDemandOf } = await import('../src/sim/data.ts');
  const { wasteDisplayModel } = await import('../src/components/right/wasteModel.ts');
  const {
    gridImportCostPerTick,
    utilityBuyInCostPerTick,
    GRID_IMPORT_TARIFF_PER_MW,
    WATER_IMPORT_TARIFF_PER_PERSON_PER_TICK,
    WASTEWATER_CONTRACT_TARIFF_PER_PERSON_PER_TICK,
    REFUSE_CONTRACT_TARIFF_PER_TONNE_PER_TICK,
  } = await import('../src/sim/fiscal.ts');
  const { fmtMoney } = await import('../src/sim/utils.ts');

  const render = (Cmp: any, state: any) =>
    renderToString(
      React.default.createElement(
        SimContext.Provider,
        { value: { state, dispatch: () => {} } } as any,
        React.default.createElement(Cmp)
      )
    );

  const power = { ...initialState(), buildings: [{ id: 1, spec: 'pow_wind', x: 20, y: 20 }], population: 700, gridImportEnabled: true } as any;
  const pw = powerStats(power);
  const expectedPower = gridImportCostPerTick(pw.cap, pw.need, GRID_IMPORT_TARIFF_PER_MW);
  assert.ok(expectedPower > 0, 'precondition: a real, priced power shortfall');
  assert.ok(render(PowerTab, power).includes(fmtMoney(expectedPower)), `PowerTab must show ${fmtMoney(expectedPower)}`);

  const water = waterShortageCity({ waterImportEnabled: true, wastewaterContractEnabled: true, population: 337 });
  const bal = waterBalanceOf(water);
  const demand = waterDemandOf(water);
  const expectedWater = utilityBuyInCostPerTick(bal.clean, demand.clean, WATER_IMPORT_TARIFF_PER_PERSON_PER_TICK);
  const expectedWastewater = utilityBuyInCostPerTick(bal.waste, demand.waste, WASTEWATER_CONTRACT_TARIFF_PER_PERSON_PER_TICK);
  assert.ok(expectedWater > 0 && expectedWastewater > 0, 'precondition: real, priced water/wastewater shortfalls');
  const waterHtml = render(WaterTab, water);
  assert.ok(waterHtml.includes(fmtMoney(expectedWater)), `WaterTab must show ${fmtMoney(expectedWater)} for clean water`);
  assert.ok(waterHtml.includes(fmtMoney(expectedWastewater)), `WaterTab must show ${fmtMoney(expectedWastewater)} for wastewater`);

  let id = 720001;
  const waste = { ...initialState(), buildings: [], population: 337, refuseContractEnabled: true } as any;
  for (let i = 0; i < 34; i++) waste.buildings.push({ id: id++, spec: 'res_hut', x: (i % 200) + 5, y: 5 });
  const m = wasteDisplayModel(waste);
  const expectedRefuse = utilityBuyInCostPerTick(m.capacity, m.generated, REFUSE_CONTRACT_TARIFF_PER_TONNE_PER_TICK);
  assert.ok(expectedRefuse > 0, 'precondition: a real, priced refuse shortfall');
  assert.ok(render(WasteTab, waste).includes(fmtMoney(expectedRefuse)), `WasteTab must show ${fmtMoney(expectedRefuse)}`);
});

test('WaterTab smoke: renders inside SimProvider, shows both toggles defaulted ON, no NaN', async () => {
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { WaterTab } = await import('../src/components/left/tabs/servicesTabs.tsx');
  const { SimProvider } = await import('../src/sim/store.tsx');

  if (typeof globalThis.window === 'undefined') {
    (globalThis as any).window = {
      localStorage: {
        getItem: () => null,
        setItem: () => {},
        removeItem: () => {},
        clear: () => {},
        key: () => null,
        length: 0,
      },
      performance: { now: () => 0 },
    };
  }

  const html = renderToString(
    React.default.createElement(SimProvider, { children: React.default.createElement(WaterTab) })
  );
  assert.ok(html.length > 0, 'WaterTab must render inside the provider without a context error');
  assert.ok(html.includes('external water cover'), 'the water toggle label must render');
  assert.ok(html.includes('external sewage cover'), 'the wastewater toggle label must render');
  assert.doesNotMatch(html, /NaN/, 'no NaN must leak into the rendered output');
});

test('WasteTab smoke: renders inside SimProvider, shows the refuse toggle defaulted ON, no NaN', async () => {
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { WasteTab } = await import('../src/components/left/tabs/servicesTabs.tsx');
  const { SimProvider } = await import('../src/sim/store.tsx');

  const html = renderToString(
    React.default.createElement(SimProvider, { children: React.default.createElement(WasteTab) })
  );
  assert.ok(html.length > 0, 'WasteTab must render inside the provider without a context error');
  assert.ok(html.includes('contracted refuse collection'), 'the refuse toggle label must render');
  assert.doesNotMatch(html, /NaN/, 'no NaN must leak into the rendered output');
});

// BUG-1050 (P3, mutants M8/M8b): WasteTab's three-way banner (no shortfall /
// shortfall covered / shortfall uncovered) was untested at the copy level —
// BUG-1027's fix pinned wasteDisplayModel.hasUncollected itself, but nothing
// asserted which of the three <p> banners actually renders. Each fixture
// below isolates exactly one branch.
async function renderWasteTabHtml(state: any) {
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { WasteTab } = await import('../src/components/left/tabs/servicesTabs.tsx');
  const { SimContext } = await import('../src/sim/simContext.ts');
  return renderToString(
    React.default.createElement(
      SimContext.Provider,
      { value: { state, dispatch: () => {} } } as any,
      React.default.createElement(WasteTab)
    )
  );
}

test('BUG-1050: WasteTab banner — no shortfall renders the neutral "all generated refuse is collected" line only', async () => {
  const { initialState } = await import('../src/sim/engine.ts');
  const s: any = { ...initialState(), buildings: [], population: 0 };
  const html = await renderWasteTabHtml(s);
  assert.ok(html.includes('All generated refuse is collected'), 'no-shortfall branch must render');
  assert.ok(!html.includes('left uncollected'), 'the uncovered-shortfall branch must NOT render');
  assert.ok(!html.includes('Contracted refuse collecting the shortfall'), 'the covered-shortfall branch must NOT render');
});

test('BUG-1050: WasteTab banner — a covered shortfall renders the neutral "contracted refuse collecting" line, never the penalty warning', async () => {
  const { initialState } = await import('../src/sim/engine.ts');
  let id = 700001;
  const s: any = { ...initialState(), buildings: [], population: 1000, refuseContractEnabled: true };
  for (let i = 0; i < 100; i++) s.buildings.push({ id: id++, spec: 'res_hut', x: (i % 200) + 5, y: 5 });
  const html = await renderWasteTabHtml(s);
  assert.ok(html.includes('Contracted refuse collecting the shortfall'), 'covered-shortfall branch must render');
  assert.ok(!html.includes('drives the waste-health penalty'), 'the uncovered/penalty banner must NOT render while contracted');
  assert.ok(!html.includes('All generated refuse is collected'), 'the no-shortfall branch must NOT render');
});

test('BUG-1050: WasteTab banner — an uncovered shortfall renders the "left uncollected" penalty warning', async () => {
  const { initialState } = await import('../src/sim/engine.ts');
  let id = 710001;
  const s: any = { ...initialState(), buildings: [], population: 1000, refuseContractEnabled: false };
  for (let i = 0; i < 100; i++) s.buildings.push({ id: id++, spec: 'res_hut', x: (i % 200) + 5, y: 5 });
  const html = await renderWasteTabHtml(s);
  assert.ok(html.includes('left uncollected') && html.includes('drives the waste-health penalty'), 'uncovered-shortfall penalty branch must render');
  assert.ok(!html.includes('Contracted refuse collecting the shortfall'), 'the covered-shortfall branch must NOT render');
  assert.ok(!html.includes('All generated refuse is collected'), 'the no-shortfall branch must NOT render');
});

test('EarningsTab smoke: renders a utility buy-in row only when the flow is actually present this tick', async () => {
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { EarningsTab } = await import('../src/components/left/tabs/financeTabs.tsx');
  const { SimContext } = await import('../src/sim/simContext.ts');

  const shortage = reducer(waterShortageCity(), { type: 'tick' } as any);
  assert.ok(
    shortage.lastFlows.outflows.some((f: any) => f.label === WATER_IMPORT_OUTFLOW_LABEL),
    'precondition'
  );

  const htmlWithFlow = renderToString(
    React.default.createElement(
      SimContext.Provider,
      { value: { state: shortage, dispatch: () => {} } } as any,
      React.default.createElement(EarningsTab)
    )
  );
  assert.ok(htmlWithFlow.includes('Water Import'), 'Water Import row must render when the flow is present');

  const surplus = reducer(surplusWaterCity(), { type: 'tick' } as any);
  assert.equal(
    surplus.lastFlows.outflows.find((f: any) => f.label === WATER_IMPORT_OUTFLOW_LABEL),
    undefined,
    'precondition: no flow this tick'
  );
  const htmlNoFlow = renderToString(
    React.default.createElement(
      SimContext.Provider,
      { value: { state: surplus, dispatch: () => {} } } as any,
      React.default.createElement(EarningsTab)
    )
  );
  assert.ok(
    !htmlNoFlow.includes('Water Import'),
    'Water Import row must be ABSENT when no flow occurred, not a stale value'
  );
});

// ---------------------------------------------------------------------------
// BUG-1029 (AC-9 real interaction, mutation M8 fix): clicking a toggle button
// really dispatches its journaled action. Before this, AC-9 was covered only
// by renderToString() label greps (never clicks anything) plus a round-added
// source-grep pin (attack-feat711-inc2-round.test.mjs, "each panel toggle
// really dispatches its journaled action") — a structural check, not a real
// interaction test. This mounts SimProvider with a REAL client root (jsdom,
// same idiom as store-dispatch.test.tsx's BAR-1/BAR-2), finds the actual
// rendered <button> for each of the three inc2 toggles, and dispatches a
// REAL native 'click' DOM event at it (not calling dispatch() directly) —
// exactly the path a no-op `onClick={() => {}}` mutant (round mutant M8)
// breaks: state.<flag>Enabled must flip AND the journal (persisted to
// localStorage by store.tsx's debounced JournalPersister) must record the
// exact action type.
// ---------------------------------------------------------------------------

function installJsdomForClickTest() {
  const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', {
    url: 'http://localhost/',
    pretendToBeVisual: true,
  });
  const { window } = dom;
  (globalThis as any).window = window;
  (globalThis as any).document = window.document;
  Object.defineProperty(globalThis, 'navigator', {
    value: window.navigator,
    configurable: true,
    writable: true,
  });
  (globalThis as any).HTMLElement = window.HTMLElement;
  (globalThis as any).requestAnimationFrame = window.requestAnimationFrame.bind(window);
  (globalThis as any).cancelAnimationFrame = window.cancelAnimationFrame.bind(window);
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  return dom;
}

test('BUG-1029: clicking the Water Import / Wastewater Contract / Refuse Contract toggles really dispatches — state flips AND the journal records the action', async () => {
  const dom = installJsdomForClickTest();
  try {
    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimProvider, useSim } = await import('../src/sim/store.tsx');
    const { WaterTab, WasteTab } = await import('../src/components/left/tabs/servicesTabs.tsx');

    let lastState: any = null;
    function Probe({ children }: { children: React.ReactNode }) {
      const { state } = useSim();
      lastState = state;
      return children as any;
    }

    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);

    await act(async () => {
      root.render(
        React.default.createElement(SimProvider, {
          children: React.default.createElement(Probe, {
            children: [
              React.default.createElement(WaterTab, { key: 'water' }),
              React.default.createElement(WasteTab, { key: 'waste' }),
            ],
          }),
        }),
      );
    });

    assert.ok(lastState, 'Probe must have captured the initial state');
    // Preconditions: all three contracts default ON (GRID/WATER/WASTEWATER/
    // REFUSE_*_ENABLED_DEFAULT — Aaron's "a hamlet starts on external
    // contracts for everything" ruling, carried into every field's `?? DEFAULT`
    // fallback so an undefined field reads as ON too).
    assert.notEqual(lastState.waterImportEnabled, false, 'precondition: water import defaults ON');
    assert.notEqual(lastState.wastewaterContractEnabled, false, 'precondition: wastewater contract defaults ON');
    assert.notEqual(lastState.refuseContractEnabled, false, 'precondition: refuse contract defaults ON');

    const toggleButtons = Array.from(container.querySelectorAll('button.toggle')) as HTMLButtonElement[];
    // WaterTab renders 2 toggles (water, wastewater) then WasteTab renders 1
    // (refuse) — same DOM order as their JSX (servicesTabs.tsx).
    assert.equal(toggleButtons.length, 3, `expected 3 toggle buttons, found ${toggleButtons.length}`);
    const [waterBtn, wastewaterBtn, refuseBtn] = toggleButtons;

    const clickReal = async (btn: HTMLButtonElement) => {
      await act(async () => {
        btn.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true, cancelable: true }));
      });
    };

    await clickReal(waterBtn);
    assert.equal(lastState.waterImportEnabled, false, 'a real click on the Water Import toggle must flip the flag');

    await clickReal(wastewaterBtn);
    assert.equal(lastState.wastewaterContractEnabled, false, 'a real click on the Wastewater Contract toggle must flip the flag');

    await clickReal(refuseBtn);
    assert.equal(lastState.refuseContractEnabled, false, 'a real click on the Refuse Contract toggle must flip the flag');

    // The three toggles are all `isStateAffecting` (journal.ts) — store.tsx's
    // wrappedDispatch/guardedDispatch schedules a debounced localStorage
    // write (JournalPersister, JOURNAL_PERSIST_DEBOUNCE_MS = 1000ms) for
    // every state-affecting action. Wait past the debounce (real timers —
    // journal.ts's createJournalPersister uses the bare `setTimeout`, so
    // this is a genuine wall-clock wait, not a fake-timer advance) and read
    // the ACTUAL persisted journal back out of jsdom's localStorage — proving
    // the click's action reached the journal, not just React state.
    await new Promise((resolve) => setTimeout(resolve, 1200));

    const raw = dom.window.localStorage.getItem(JOURNAL_KEY);
    assert.ok(raw, 'the journal must have been persisted to localStorage after the debounce window');
    const persisted = JSON.parse(raw!) as { entries: { action: { type: string } }[] };
    const types = persisted.entries.map((e) => e.action.type);
    assert.ok(types.includes('toggleWaterImport'), `journal must record toggleWaterImport, got types: ${JSON.stringify(types)}`);
    assert.ok(types.includes('toggleWastewaterContract'), `journal must record toggleWastewaterContract, got types: ${JSON.stringify(types)}`);
    assert.ok(types.includes('toggleRefuseContract'), `journal must record toggleRefuseContract, got types: ${JSON.stringify(types)}`);

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});

// BUG-1062 (r3 round P2 test gap): the BUG-1048/BUG-1061 leak-consequence
// gate (WaterTab's leakConsequenceActive) had NOTHING pinning it — the round
// found a surviving mutant reverting the whole gate back to raw `bal.leak`
// that kept every existing suite green. The fixture below is the round's own
// (over-built clean network vs a merely-adequate waste plant, so
// waterBalanceOf().leak is a real physical fact with NO population-level
// waste-water shortage at all) run through all three branches the CONTRACT
// TOGGLE (post-BUG-1061) now controls.
function leakFixture(cleanPlants: number, wastewaterContractEnabled: boolean) {
  let id = 940001;
  const s: any = {
    ...initialState(),
    buildings: [],
    population: 2000,
    gridImportEnabled: false,
    waterImportEnabled: false,
    wastewaterContractEnabled,
    refuseContractEnabled: false,
  };
  for (let i = 0; i < 40; i++) s.buildings.push({ id: id++, spec: 'res_block', x: (i % 30) + 3, y: 5 });
  for (let i = 0; i < 30; i++) s.buildings.push({ id: id++, spec: 'pow_wind', x: (i % 30) + 3, y: 20 });
  for (let i = 0; i < cleanPlants; i++) s.buildings.push({ id: id++, spec: 'wat_clean', x: (i % 30) + 3, y: 50 });
  s.buildings.push({ id: id++, spec: 'wat_waste', x: 5, y: 60 });
  for (let i = 0; i < 30; i++) s.buildings.push({ id: id++, spec: 'waste_depot', x: (i % 30) + 3, y: 70 });
  return s;
}

async function renderWaterTabHtml(state: any) {
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { WaterTab } = await import('../src/components/left/tabs/servicesTabs.tsx');
  const { SimContext } = await import('../src/sim/simContext.ts');
  return renderToString(
    React.default.createElement(
      SimContext.Provider,
      { value: { state, dispatch: () => {} } } as any,
      React.default.createElement(WaterTab)
    )
  );
}

test('BUG-1062: WaterTab leak branch — Waste-Water Contract ON over a real leak renders the neutral "contract covering it" line, no -5 warning, no red discharge tile', async () => {
  const { waterBalanceOf } = await import('../src/sim/data.ts');
  const s = leakFixture(5, true);
  assert.equal(waterBalanceOf(s).leak, true, 'precondition: a genuine physical leak');
  const html = await renderWaterTabHtml(s);
  assert.ok(html.includes('but the Waste-Water Contract is covering the'), 'the neutral covered-leak line must render');
  assert.ok(!html.includes('sewage backs up (-5 approval)'), 'the -5 approval warning must NOT render while the contract is ON');
  assert.doesNotMatch(html, /class="tile neg">[\s\S]{0,60}Discharge capacity/, 'the discharge tile must NOT paint red while the contract is ON');
});

test('BUG-1062: WaterTab leak branch — Waste-Water Contract OFF over the same leak renders the legacy -5 warning and the red discharge tile', async () => {
  const { waterBalanceOf } = await import('../src/sim/data.ts');
  const s = leakFixture(5, false);
  assert.equal(waterBalanceOf(s).leak, true, 'precondition: the identical physical leak as the ON case');
  const html = await renderWaterTabHtml(s);
  assert.ok(html.includes('sewage backs up (-5 approval)'), 'the legacy -5 approval warning must render while the contract is OFF');
  assert.ok(!html.includes('but the Waste-Water Contract is covering the'), 'the covered-leak neutral line must NOT render while the contract is OFF');
  assert.match(html, /class="tile neg">[\s\S]{0,60}Discharge capacity/, 'the discharge tile must paint red while the contract is OFF');
});

test('BUG-1062: WaterTab leak branch — no leak at all renders the "Network balanced" line regardless of the toggle', async () => {
  const { waterBalanceOf } = await import('../src/sim/data.ts');
  const balanced = leakFixture(1, false);
  assert.equal(waterBalanceOf(balanced).leak, false, 'precondition: the balanced twin does not leak');
  const html = await renderWaterTabHtml(balanced);
  assert.ok(html.includes('Network balanced'), 'the no-leak branch must render');
  assert.ok(!html.includes('sewage backs up (-5 approval)'), 'the -5 warning must NOT render when there is no leak');
  assert.ok(!html.includes('but the Waste-Water Contract is covering the'), 'the covered-leak neutral line must NOT render when there is no leak');
});
