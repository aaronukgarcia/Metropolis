// bug1194-advisor-mix.test.tsx — BUG-1194: the MapView advisor prompt names the WHOLE
// Fix mix (formatMixText), never `count` (the mix TOTAL) glued to mix[0]'s name.
// RED-PROOF: reverting MapView's `buildingLabel` to formatBuildingCount(sp.name, fix.count)
// makes the multi-spec assertion fail (the prompt then claims "<total> x <first spec>").
import { test } from 'node:test';
import assert from 'node:assert/strict';

async function mountAdvisor(state: any) {
  const { JSDOM } = await import('jsdom');
  const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', { url: 'http://localhost/', pretendToBeVisual: true });
  const g: any = globalThis;
  g.window = dom.window;
  g.document = dom.window.document;
  try { g.navigator = dom.window.navigator; } catch { /* read-only on newer node */ }
  g.HTMLElement = dom.window.HTMLElement;
  g.Node = dom.window.Node;
  g.MouseEvent = dom.window.MouseEvent;
  g.Event = dom.window.Event;
  g.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  g.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  g.IS_REACT_ACT_ENVIRONMENT = true;
  if (typeof g.ResizeObserver === 'undefined') g.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };
  const React = await import('react');
  const { createRoot } = await import('react-dom/client');
  const { act } = await import('react-dom/test-utils');
  const { SimContext } = await import('../src/sim/simContext.ts');
  const { BusyProvider } = await import('../src/components/Busy.tsx');
  const { MapView } = await import('../src/components/MapView.tsx');
  const ctx: any = {
    state, dispatch: () => {}, cityName: 'X', listSaves: () => [], listRecent: () => [],
    saveGame: async () => true, saveGameAs: async () => {}, loadGame: async () => {}, loadNamed: async () => {}, renameCity: () => true,
  };
  const root = createRoot(document.getElementById('root')!);
  await act(async () => {
    root.render(React.default.createElement(SimContext.Provider, { value: ctx }, React.default.createElement(BusyProvider, { children: React.default.createElement(MapView) })));
  });
  const text = document.querySelector('.advisor')?.textContent ?? '';
  await act(async () => { root.unmount(); });
  dom.window.close();
  return text;
}

test('BUG-1194: a multi-spec Fix mix is named in full in the advisor prompt; a single-spec fix is unchanged', async () => {
  const { initialState } = await import('../src/sim/engine.ts');
  const D = await import('../src/sim/data.ts');
  const { worstDemandFix, formatMixText } = await import('../src/components/demandFixUi.ts');
  const base = { ...initialState(), unlockedAll: true, funds: 1_000_000_000, administrationState: null } as any;

  // Find populations whose WORST fix (what the advisor shows) is multi-spec / single-spec.
  let multi: { s: any; fix: any } | null = null;
  let single: { s: any; fix: any } | null = null;
  for (const pop of [1_000, 2_000, 5_000, 8_000, 13_000, 20_000, 30_000, 50_000, 80_000, 120_000]) {
    const s = { ...base, population: pop };
    const fix = worstDemandFix(s);
    if (!fix) continue;
    if (fix.mix && fix.mix.length > 1 && !multi) multi = { s, fix };
    if ((!fix.mix || fix.mix.length <= 1) && !single) single = { s, fix };
  }
  assert.ok(multi, 'fixture: some population has a multi-spec worst fix');
  const multiText = await mountAdvisor(multi!.s);
  assert.ok(multiText.includes(`place ${formatMixText(multi!.fix)}?`), `advisor must name the full mix, got: ${multiText}`);
  assert.ok(!multiText.includes(`place ${multi!.fix.count} x ${D.SPECS[multi!.fix.specId].name}?`), `must not claim mix total of mix[0]: ${multiText}`);

  assert.ok(single, 'fixture: some population has a single-spec worst fix');
  const singleText = await mountAdvisor(single!.s);
  assert.ok(singleText.includes(`place ${formatMixText(single!.fix)}?`), singleText);
  assert.ok(singleText.includes(`place ${single!.fix.count} x ${D.SPECS[single!.fix.specId].name}?`), singleText);
});
