// attack-property-card-r2.test.tsx — FEAT-2326609841 destructive round r2
// (opus-round-r2-property-card): delta checks on the r1 fixes.
//  R2-A  shared draw table moves BOTH powerStats and the card (runtime mutation)
//  R2-B  Brownout badge oracle == isBrownoutActive on every card, grid import ON and OFF
//  R2-C  F6 gone-state under StrictMode rapid reselect after removal, and a
//        loaded state where the selected id is gone
// Run: node ../tools/test/scoped.mjs test/attack-property-card-r2.test.tsx
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState } from '../src/sim/engine.ts';
import { SPECS, computeRoadConnectivity, powerStats, isBrownoutActive, brownoutOf, POWER_DRAW_MW_BY_KIND } from '../src/sim/data.ts';
import { formatPower } from '../src/sim/utils.ts';
import type { SimState } from '../src/sim/types.ts';

let _id = 2326609842900;
const B = (spec: string, x: number, y: number, extra: Record<string, unknown> = {}) => ({ id: _id++, spec, x, y, builtTick: 0, ...extra });
const Y = (spec: string) => 10 - SPECS[spec].h;
function city(buildings: unknown[], tick: number, population = 0, extra: Partial<SimState> = {}): SimState {
  const st = { ...initialState(), buildings: buildings as SimState['buildings'], tick, population, ...extra } as SimState;
  st.roadConnectivity = computeRoadConnectivity(st);
  return st;
}
const roads = () => Array.from({ length: 30 }, (_, i) => B('road', i, 10));

test('R2-A: mutating the shared draw table moves powerStats AND the card row together (one source)', async () => {
  const { cardModelOf } = await import('../src/components/PropertyCard.tsx');
  const f = B('ind_factory', 2, Y('ind_factory'));
  const mk = () => city([...roads(), f], 5000, 1000);
  const orig = POWER_DRAW_MW_BY_KIND.industrial;
  const need0 = powerStats(mk()).need;
  assert.equal(cardModelOf(mk(), f)!.rows.find((r) => r.key === 'power')!.value, formatPower(orig));
  try {
    (POWER_DRAW_MW_BY_KIND as any).industrial = 40;
    assert.equal(powerStats(mk()).need, need0 + (40 - orig), 'sim moved');
    assert.equal(cardModelOf(mk(), f)!.rows.find((r) => r.key === 'power')!.value, formatPower(40), 'card moved');
  } finally {
    (POWER_DRAW_MW_BY_KIND as any).industrial = orig;
  }
  assert.equal(powerStats(mk()).need, need0);
});

test('R2-B: Brownout badge on every card equals isBrownoutActive (grid import ON is silent, OFF shows on online consumers)', async () => {
  const { cardModelOf } = await import('../src/components/PropertyCard.tsx');
  const bs = [...roads(), B('ind_factory', 2, Y('ind_factory')), B('off_suite', 8, Y('off_suite')), B('mine_quarry', 14, Y('mine_quarry')), B('res_block', 20, Y('res_block')), B('ind_factory', 24, Y('ind_factory'), { builtTick: 4999 })];
  for (const grid of [true, false]) {
    const s = city(bs, 5000, 300000, { gridImportEnabled: grid } as Partial<SimState>);
    assert.equal(brownoutOf(s).active, true, 'genuine physical shortfall');
    assert.equal(isBrownoutActive(s), !grid);
    let shown = 0;
    for (const b of s.buildings) {
      const has = cardModelOf(s, b)!.badges.includes('Brownout');
      if (has) shown++;
      if (grid) assert.ok(!has, `grid ON: ${b.spec} must be silent`);
    }
    if (!grid) assert.equal(shown, 3, 'three online consumers badge; the pending factory and the home do not');
  }
});

async function installJsdom() {
  const { JSDOM } = await import('jsdom');
  const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', { url: 'http://localhost/', pretendToBeVisual: true });
  const { window } = dom;
  (globalThis as any).window = window;
  (globalThis as any).document = window.document;
  Object.defineProperty(globalThis, 'navigator', { value: window.navigator, configurable: true, writable: true });
  (globalThis as any).HTMLElement = window.HTMLElement;
  (globalThis as any).requestAnimationFrame = window.requestAnimationFrame.bind(window);
  (globalThis as any).cancelAnimationFrame = window.cancelAnimationFrame.bind(window);
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
}
const ctxFor = (state: SimState) => ({ state, dispatch: () => {}, cityName: 'R2', listSaves: () => [], listRecent: () => [], saveGame: async () => true, saveGameAs: async () => ({ ok: true }), loadGame: async () => {}, loadNamed: async () => {}, renameCity: () => ({ ok: true }), exportCity: async () => true, importCity: async () => true });

test('R2-C: StrictMode reselect live -> removed -> other -> removed again, and a loaded state lacking the id: gone state, never stale, never a crash', async () => {
  await installJsdom();
  const React: any = (await import('react')).default;
  const { act } = await import('react');
  const { createRoot } = await import('react-dom/client');
  const { SimContext } = await import('../src/sim/simContext.ts');
  const { BuildingCard } = await import('../src/components/MapView.tsx');
  const container = (globalThis as any).document.getElementById('root');
  const root = createRoot(container);
  const render = async (st: SimState, building: any) =>
    act(async () => root.render(React.createElement(React.StrictMode, null, React.createElement(SimContext.Provider, { value: ctxFor(st) }, React.createElement(BuildingCard, { building, connected: true, showRefs: false, onClose: () => {} })))));
  const a = B('res_block', 2, Y('res_block'));
  const b = B('pow_wind', 9, 8);
  const r = roads();
  const withA = city([...r, a, b], 5000, 50);
  const noA = city([...r, b], 5001, 50);
  const state = () => container.querySelector('[data-testid="pc-maturity"]')?.getAttribute('data-state');
  await render(withA, a);
  assert.equal(state(), 'operational');
  await render(noA, a);
  assert.equal(state(), 'gone');
  await render(noA, b);
  assert.equal(state(), 'operational');
  assert.equal(container.querySelector('[data-testid="property-card"]')!.getAttribute('data-spec'), 'pow_wind');
  await render(noA, a);
  assert.equal(state(), 'gone');
  await render(withA, a); // e.g. undo / reload of a save that has it again
  assert.equal(state(), 'operational');
  // Loaded save (fresh state object, JSON round-trip) without the id.
  const loaded = JSON.parse(JSON.stringify(noA)) as SimState;
  loaded.roadConnectivity = computeRoadConnectivity(loaded);
  await render(loaded, a);
  assert.equal(state(), 'gone');
  assert.equal(container.querySelectorAll('[data-testid="property-card"]').length, 1);
  await act(async () => root.unmount());
});
