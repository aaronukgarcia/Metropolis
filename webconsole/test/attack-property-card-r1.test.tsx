// attack-property-card-r1.test.tsx — FEAT-2326609841 destructive round r1
// (opus-round-r1-property-card, independent attacker). Left in the lane on
// purpose: these pin behaviours the builder's suite does not reach.
//
//  R1-A  Brownout badge (AC-7) must be REACHABLE for a real power consumer and
//        must follow isBrownoutActive (the ONE brownout-consequence SSOT,
//        grid-import aware), not the raw toggle-blind brownoutOf.
//  R1-B  Power row (AC-6) for a sim power consumer (ind_factory draws 6 MW in
//        computePowerStats) must not read "—" (the card's own "not
//        applicable" glyph).
//  R1-C  Water apportionment under adversarial populations / orders.
//  R1-D  Ghost card: snapshot id no longer in state (consolidated / removed).
//  R1-E  Motif determinism for EVERY spec, and no <img> while LANDED_ART is empty.
//  R1-F  StrictMode rapid select -> reselect -> reselect interleaving.
//
// Run: node ../tools/test/scoped.mjs test/attack-property-card-r1.test.tsx
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState } from '../src/sim/engine.ts';
import { SPECS, computeRoadConnectivity, waterDemandOf, isBrownoutActive, brownoutOf, isOnline } from '../src/sim/data.ts';
import type { SimState } from '../src/sim/types.ts';

let _id = 2326609841900;
const B = (spec: string, x: number, y: number, extra: Record<string, unknown> = {}) => ({ id: _id++, spec, x, y, builtTick: 0, ...extra });

function city(buildings: unknown[], tick: number, population = 0, extra: Partial<SimState> = {}): SimState {
  const st = { ...initialState(), buildings: buildings as SimState['buildings'], tick, population, ...extra } as SimState;
  st.roadConnectivity = computeRoadConnectivity(st);
  return st;
}
const roads = (y = 10, n = 24) => Array.from({ length: n }, (_, i) => B('road', i, y));
const homeY = (spec: string) => 10 - SPECS[spec].h;

// ---------- R1-A / R1-B: power + brownout ----------
test('R1-A: Brownout badge is reachable for a real consumer (ind_factory) when the brownout bites (grid import OFF)', async () => {
  const { cardModelOf } = await import('../src/components/PropertyCard.tsx');
  const f = B('ind_factory', 2, 10 - SPECS['ind_factory'].h);
  const s = city([...roads(), f], 5000, 200000, { gridImportEnabled: false } as Partial<SimState>);
  assert.ok(isOnline(s, f), 'fixture sanity: factory online');
  assert.equal(isBrownoutActive(s), true, 'fixture sanity: the brownout bites');
  const m = cardModelOf(s, f)!;
  assert.ok(m.badges.includes('Brownout'), `factory card must show Brownout; badges=${JSON.stringify(m.badges)}`);
});

test('R1-A: with Grid Import ON (the default) a physical deficit is NOT a brownout - no card may show the badge', async () => {
  const { cardModelOf } = await import('../src/components/PropertyCard.tsx');
  const f = B('ind_factory', 2, 10 - SPECS['ind_factory'].h);
  const s = city([...roads(), f], 5000, 200000, { gridImportEnabled: true } as Partial<SimState>);
  assert.equal(brownoutOf(s).active, true, 'fixture sanity: physical deficit');
  assert.equal(isBrownoutActive(s), false, 'fixture sanity: grid import covers it');
  for (const b of s.buildings) assert.ok(!cardModelOf(s, b)!.badges.includes('Brownout'), b.spec);
});

test('R1-B: a sim power consumer (ind_factory, 6 MW in computePowerStats) does not show the "not applicable" dash', async () => {
  const { cardModelOf } = await import('../src/components/PropertyCard.tsx');
  const f = B('ind_factory', 2, 10 - SPECS['ind_factory'].h);
  const s = city([...roads(), f], 5000, 100);
  const row = cardModelOf(s, f)!.rows.find((r) => r.key === 'power')!;
  assert.notEqual(row.value, '—', 'factory draws power in the sim; "—" claims it draws none');
});

// ---------- R1-C: apportionment adversarial ----------
function sum(_s: SimState, shares: any) {
  let c = 0, w = 0;
  for (const v of shares.byId.values()) {
    assert.ok(Number.isInteger(v.clean) && Number.isInteger(v.waste) && v.clean >= 0 && v.waste >= 0, 'integer non-negative shares');
    c += v.clean; w += v.waste;
  }
  return { c, w };
}
function prng(seed: number) {
  return () => ((seed = (seed * 1103515245 + 12345) >>> 0) / 2 ** 32);
}

test('R1-C: single 1-capacity hut carries a huge population exactly; pop 0 / negative / fractional never leak into shares', async () => {
  const { utilitySharesOf } = await import('../src/sim/buildingUtility.ts');
  for (const pop of [0, -5, 1234.5, 1000003, 2 ** 40 + 3]) {
    const hut = B('res_hut', 1, homeY('res_hut'));
    const s = city([...roads(), hut], 5000, pop);
    const d = waterDemandOf(s);
    const sh = utilitySharesOf(s);
    const { c, w } = sum(s, sh);
    assert.equal(c + sh.unallocated.clean, d.clean, `pop ${pop} clean identity`);
    assert.equal(w + sh.unallocated.waste, d.waste, `pop ${pop} waste identity`);
    if (Number.isInteger(pop) && pop > 0) assert.equal(c, d.clean, `pop ${pop}: all on the one hut`);
    if (pop <= 0) assert.equal(c, 0, `pop ${pop}: nothing invented`);
  }
});

test('R1-C: all-under-construction city -> every share unallocated, byId empty', async () => {
  const { utilitySharesOf } = await import('../src/sim/buildingUtility.ts');
  const homes = ['res_hut', 'res_block', 'res_lowrise'].map((sp, i) => B(sp, 1 + i * 4, homeY(sp), { builtTick: 4999 }));
  const s = city([...roads(), ...homes], 5000, 777);
  const sh = utilitySharesOf(s);
  assert.equal(sh.byId.size, 0);
  assert.equal(sh.unallocated.clean, waterDemandOf(s).clean);
});

test('R1-C: shares are identical under 25 seeded shuffles of building order (GR#21)', async () => {
  const { utilitySharesOf } = await import('../src/sim/buildingUtility.ts');
  _id = 2326609841950;
  const specs = ['res_hut', 'res_block', 'res_lowrise', 'res_block', 'res_hut', 'res_terrace', 'res_hut'];
  const base = [...roads(), ...specs.map((sp, i) => B(sp, 1 + i * 3, homeY(sp)))];
  const canon = (s: SimState) => JSON.stringify([...utilitySharesOf(s).byId.entries()].sort((a, b) => a[0] - b[0]));
  const ref = canon(city(base, 5000, 99991));
  const r = prng(841);
  for (let k = 0; k < 25; k++) {
    const arr = [...base];
    for (let i = arr.length - 1; i > 0; i--) { const j = Math.floor(r() * (i + 1)); [arr[i], arr[j]] = [arr[j], arr[i]]; }
    assert.equal(canon(city(arr, 5000, 99991)), ref, `shuffle ${k}`);
  }
});

test('R1-C2: exact remainder TIE between identical homes resolves by id, not array order (3 identical homes, pop 3k+1)', async () => {
  const { utilitySharesOf } = await import('../src/sim/buildingUtility.ts');
  const homes = [0, 1, 2].map((i) => B('res_block', 1 + i * 4, homeY('res_block')));
  const fwd = utilitySharesOf(city([...roads(), ...homes], 5000, 3001));
  const rev = utilitySharesOf(city([...roads(), ...[...homes].reverse()], 5000, 3001));
  for (const h of homes) assert.deepEqual(rev.byId.get(h.id), fwd.byId.get(h.id), `home ${h.id}`);
  assert.equal(fwd.byId.get(homes[0].id)!.clean, 1001, 'lowest id takes the odd unit');
});

// ---------- mount helpers ----------
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
function ctxFor(state: SimState) {
  return { state, dispatch: () => {}, cityName: 'R1', listSaves: () => [], listRecent: () => [], saveGame: async () => true, saveGameAs: async () => ({ ok: true }), loadGame: async () => {}, loadNamed: async () => {}, renameCity: () => ({ ok: true }), exportCity: async () => true, importCity: async () => true };
}
async function mount(strict: boolean) {
  await installJsdom();
  const React: any = (await import('react')).default;
  const { act } = await import('react');
  const { createRoot } = await import('react-dom/client');
  const { SimContext } = await import('../src/sim/simContext.ts');
  const { BuildingCard } = await import('../src/components/MapView.tsx');
  const container = (globalThis as any).document.getElementById('root');
  const root = createRoot(container);
  const render = async (st: SimState, building: any) => {
    const inner = React.createElement(SimContext.Provider, { value: ctxFor(st) }, React.createElement(BuildingCard, { building, connected: true, showRefs: false, onClose: () => {} }));
    await act(async () => root.render(strict ? React.createElement(React.StrictMode, null, inner) : inner));
  };
  return { container, render, unmount: async () => act(async () => root.unmount()) };
}

test('R1-D: snapshot whose id has left state (consolidated/removed) - card does not crash (ghost probe)', async () => {
  const gone = B('res_block', 2, homeY('res_block'));
  const s = city([...roads()], 5000, 50);
  const m = await mount(false);
  await m.render(s, gone);
  const card = m.container.querySelector('[data-testid="property-card"]');
  assert.ok(card, 'renders (snapshot fallback) without throwing');
  // eslint-disable-next-line no-console
  console.log('R1-D ghost maturity:', m.container.querySelector('[data-testid="pc-maturity"]')?.textContent, 'badges:', m.container.querySelector('[data-testid="pc-badges"]')?.textContent ?? '(none)');
  await m.unmount();
});

test('R1-F: StrictMode rapid select A -> B -> A -> B renders exactly one card for the latest building', async () => {
  const a = B('res_block', 2, homeY('res_block'));
  const b = B('pow_wind', 8, 8);
  const s = city([...roads(), a, b], 5000, 50);
  const m = await mount(true);
  for (const x of [a, b, a, b, a, b]) await m.render(s, x);
  const cards = m.container.querySelectorAll('[data-testid="property-card"]');
  assert.equal(cards.length, 1);
  assert.equal((cards[0] as HTMLElement).dataset.spec, 'pow_wind');
  await m.unmount();
});

test('R1-G: a mousedown inside the card still reaches document listeners (FileMenu outside-click close); outside the card is untouched', async () => {
  const a = B('res_block', 2, homeY('res_block'));
  const s = city([...roads(), a], 5000, 50);
  const m = await mount(false);
  await m.render(s, a);
  const doc = (globalThis as any).document;
  const W = (globalThis as any).window;
  let hits = 0;
  const onDoc = () => hits++;
  doc.addEventListener('mousedown', onDoc);
  const outside = doc.createElement('div');
  doc.body.appendChild(outside);
  outside.dispatchEvent(new W.MouseEvent('mousedown', { bubbles: true }));
  assert.equal(hits, 1, 'outside-card mousedown reaches document');
  (m.container.querySelector('[data-testid="pc-name"]') as HTMLElement).dispatchEvent(new W.MouseEvent('mousedown', { bubbles: true }));
  doc.removeEventListener('mousedown', onDoc);
  await m.unmount();
  assert.equal(hits, 2, 'inside-card mousedown swallowed: an open File menu would not close on a click into the card');
});

test('R1-H: Q100092 on the CARD row - an under-construction plant shows no generation (the builder pin greps "1120" but the row renders "1.1 GW")', async () => {
  const { cardModelOf } = await import('../src/components/PropertyCard.tsx');
  const nuke = B('pow_nuke', 2, 8, { builtTick: 0 });
  const s = city([...roads(), nuke], 1, 100);
  const row = cardModelOf(s, nuke)!.rows.find((r) => r.key === 'power')!;
  assert.equal(row.value, '—');
});

test('R1-E: every spec motif byte-identical twice; no <img> rendered while LANDED_ART is empty', async () => {
  const React: any = (await import('react')).default;
  const { renderToStaticMarkup } = await import('react-dom/server');
  const { motifSvgOf, BuildingPicture, LANDED_ART } = await import('../src/components/buildingMotifs.tsx');
  assert.equal(LANDED_ART.size, 0);
  for (const sp of Object.values(SPECS)) {
    assert.equal(motifSvgOf(sp), motifSvgOf(sp), sp.id);
    const html = renderToStaticMarkup(React.createElement(BuildingPicture, { spec: sp }));
    assert.ok(!/<img/i.test(html), `${sp.id}: no <img>`);
    assert.ok(/<svg/i.test(html) || html.includes('svg'), `${sp.id}: plate has a motif`);
  }
});
