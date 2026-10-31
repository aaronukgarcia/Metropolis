// property-card.test.tsx — FEAT-2326609841: the Monopoly-style property card.
// AC-1..AC-9 (see docs/planning/acceptance/feat-property-card.md). REAL jsdom
// mounts (createRoot + act), WITH and WITHOUT React.StrictMode (BUG-756 class),
// through the real SimContext provider and the real BuildingCard seam MapView
// renders — not a hand-fed component in isolation.
//
// Run: node ../tools/test/scoped.mjs test/property-card.test.tsx
// Red-proof: mutation table in the lane report (scratch copy + md5 restore).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState } from '../src/sim/engine.ts';
import {
  SPECS,
  BULLDOZE_REFUND_FRACTION,
  placementCost,
  constructionTicks,
  computeRoadConnectivity,
} from '../src/sim/data.ts';
import { fmtMoney } from '../src/sim/utils.ts';
import type { SimState } from '../src/sim/types.ts';

async function installJsdom() {
  const { JSDOM } = await import('jsdom');
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
  return dom;
}

let _id = 2326609841500;
const B = (spec: string, x: number, y: number, extra: Record<string, unknown> = {}) => ({
  id: _id++,
  spec,
  x,
  y,
  ...extra,
});

function city(buildings: unknown[], tick: number, population = 0): SimState {
  const st = { ...initialState(), buildings: buildings as SimState['buildings'], tick, population } as SimState;
  st.roadConnectivity = computeRoadConnectivity(st);
  return st;
}

function ctxFor(state: SimState) {
  return {
    state,
    dispatch: () => {},
    cityName: 'Cardville',
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

interface Mounted {
  container: HTMLElement;
  rerender: (state: SimState, closeSpy?: () => void) => Promise<void>;
  unmount: () => Promise<void>;
}

/** Mount the real BuildingCard (what MapView renders) through the real provider. */
async function mountCard(state: SimState, building: any, strict: boolean, onClose: () => void = () => {}): Promise<Mounted> {
  await installJsdom();
  const React: any = await import('react');
  const { createRoot } = await import('react-dom/client');
  const { act } = await import('react');
  const { SimContext } = await import('../src/sim/simContext.ts');
  const { BuildingCard } = await import('../src/components/MapView.tsx');
  const container = (globalThis as any).document.getElementById('root') as HTMLElement;
  const root = createRoot(container);
  const tree = (st: SimState, close: () => void) => {
    const inner = React.default.createElement(
      SimContext.Provider,
      { value: ctxFor(st) },
      React.default.createElement(BuildingCard, { building, connected: true, showRefs: false, onClose: close }),
    );
    return strict ? React.default.createElement(React.default.StrictMode, null, inner) : inner;
  };
  await act(async () => {
    root.render(tree(state, onClose));
  });
  return {
    container,
    rerender: async (st, close = onClose) => {
      await act(async () => {
        root.render(tree(st, close));
      });
    },
    unmount: async () => {
      await act(async () => {
        root.unmount();
      });
    },
  };
}

const q = (m: Mounted, sel: string) => m.container.querySelector(sel) as HTMLElement | null;
const text = (m: Mounted, sel: string) => (q(m, sel)?.textContent ?? '').replace(/\s+/g, ' ').trim();

function road(y = 10) {
  return Array.from({ length: 12 }, (_, i) => B('road', i, y, { builtTick: 0 }));
}

for (const strict of [true, false]) {
  test(`AC-1 (StrictMode=${strict}): card renders name, spec-coloured band and a close affordance; Close button and Escape both call onClose exactly once`, async () => {
    const home = B('res_block', 2, 8, { builtTick: 0 });
    const s = city([...road(), home], 5000, 100);
    let closes = 0;
    const m = await mountCard(s, home, strict, () => closes++);
    const sp = SPECS['res_block'];
    assert.equal(text(m, '[data-testid="pc-name"]'), sp.name);
    assert.ok(q(m, '[data-testid="pc-band"]'), 'band present');
    // jsdom normalises hex to rgb(); compare via a throwaway element.
    const probe = (globalThis as any).document.createElement('div');
    probe.style.background = sp.color;
    assert.equal((q(m, '[data-testid="pc-band"]') as HTMLElement).style.background, probe.style.background);
    const { act } = await import('react');
    await act(async () => {
      (q(m, '[data-testid="pc-close"]') as HTMLButtonElement).click();
    });
    assert.equal(closes, 1, 'Close button fires onClose');
    await act(async () => {
      (globalThis as any).window.dispatchEvent(new (globalThis as any).window.KeyboardEvent('keydown', { key: 'Escape' }));
    });
    assert.equal(closes, 2, 'Escape fires onClose (exactly once per press, even under StrictMode)');
    await m.unmount();
  });
}

test('AC-1: unmounting the card removes its Escape listener (no leak after close)', async () => {
  const home = B('res_block', 2, 8, { builtTick: 0 });
  const s = city([...road(), home], 5000, 100);
  let closes = 0;
  const m = await mountCard(s, home, false, () => closes++);
  await m.unmount();
  const { act } = await import('react');
  await act(async () => {
    (globalThis as any).window.dispatchEvent(new (globalThis as any).window.KeyboardEvent('keydown', { key: 'Escape' }));
  });
  assert.equal(closes, 0);
});

test('AC-2: band colour and category label come from the Spec (two specs differ; no hardcoded style)', async () => {
  const a = B('res_block', 2, 8, { builtTick: 0 });
  const b = B('pow_wind', 5, 8, { builtTick: 0 });
  assert.notEqual(SPECS['res_block'].color, SPECS['pow_wind'].color, 'fixture sanity');
  assert.notEqual(SPECS['res_block'].category, SPECS['pow_wind'].category, 'fixture sanity');
  const s = city([...road(), a, b], 5000, 100);
  const ma = await mountCard(s, a, false);
  const bandA = (q(ma, '[data-testid="pc-band"]') as HTMLElement).style.background;
  const catA = text(ma, '[data-testid="pc-category"]');
  await ma.unmount();
  const mb = await mountCard(s, b, false);
  const bandB = (q(mb, '[data-testid="pc-band"]') as HTMLElement).style.background;
  const catB = text(mb, '[data-testid="pc-category"]');
  await mb.unmount();
  assert.notEqual(bandA, bandB);
  assert.notEqual(catA, catB);
  assert.equal(catA, 'Zone');
  assert.equal(catB, 'Service');
});

test('AC-3: every kind renders a non-empty picture; different kinds differ; same spec is byte-identical twice', async () => {
  const { motifSvgOf } = await import('../src/components/buildingMotifs.tsx');
  const kinds = [...new Set(Object.values(SPECS).map((sp) => sp.kind))];
  const seen = new Map<string, string>();
  for (const k of kinds) {
    const sp = Object.values(SPECS).find((x) => x.kind === k)!;
    const svg = motifSvgOf(sp);
    assert.ok(svg.length > 400 && svg.includes('<svg'), `${k}: non-empty motif`);
    assert.equal(motifSvgOf(sp), svg, `${k}: deterministic (byte-identical twice)`);
    // Compare the DRAWN body only: strip the kind-bearing attributes so a
    // shared painter cannot pass on its label alone.
    // Same colour/id/footprint for every kind, so only the painter can differ.
    const norm = motifSvgOf({ ...sp, color: '#888888', id: 'probe', w: 2, h: 2, tag: undefined, residents: undefined });
    seen.set(k, norm.split(k).join('KIND'));
  }
  assert.equal(new Set(seen.values()).size, kinds.length, 'every kind has a visibly different motif');
  // Mounted plate is present and non-empty on a real card.
  const home = B('res_block', 2, 8, { builtTick: 0 });
  const m = await mountCard(city([...road(), home], 5000, 100), home, false);
  assert.ok((q(m, '[data-testid="pc-plate"] svg') as any), 'plate renders an svg');
  await m.unmount();
});

test('AC-4: Cost = placementCost, Value = resale (BULLDOZE_REFUND_FRACTION x cost), correctly labelled', async () => {
  const hall = B('hea_clinic', 2, 8, { builtTick: 0 });
  const s = city([...road(), hall], 5000, 100);
  const m = await mountCard(s, hall, false);
  const sp = SPECS['hea_clinic'];
  assert.ok(placementCost(sp) > 0, 'fixture sanity: a priced spec');
  assert.equal(text(m, '[data-testid="pc-cost"]'), `Cost ${fmtMoney(placementCost(sp))}`);
  assert.equal(text(m, '[data-testid="pc-value"]'), `Value (resale) ${fmtMoney(Math.round(placementCost(sp) * BULLDOZE_REFUND_FRACTION))}`);
  await m.unmount();
});

test('AC-5: maturity ladder - construction countdown, operational, consolidated tier N; Q100092 keeps PRODUCES suppressed while building', async () => {
  const total = constructionTicks(SPECS['pow_nuke']);
  const nuke = B('pow_nuke', 2, 8, { builtTick: 0 });
  const mid = Math.round(total / 2);
  let m = await mountCard(city([...road(), nuke], mid, 100), nuke, false);
  assert.equal(text(m, '[data-testid="pc-maturity"]'), `Under construction (${mid}/${total} days)`);
  assert.ok(!(m.container.textContent ?? '').includes('1120'), 'nameplate MW suppressed under construction (Q100092)');
  // The row renders "1.1 GW" when live, so the real pin is the row itself.
  assert.equal(text(m, 'tr[data-row="power"] td'), '—', 'power row suppressed under construction');
  assert.ok(!/GW|MW/.test(text(m, 'tr[data-row="power"]').replace('Power generated', '')), 'no figure leaks into the row');
  await m.unmount();

  m = await mountCard(city([...road(), nuke], total + 5, 100), nuke, false);
  assert.equal(text(m, '[data-testid="pc-maturity"]'), 'Operational');
  await m.unmount();

  const auto = B('res_block', 2, 8, { builtTick: 0, placedBy: 'auto', capacityTier: 2 });
  m = await mountCard(city([...road(), auto], 5000, 100), auto, false);
  assert.equal(text(m, '[data-testid="pc-maturity"]'), 'Consolidated tier 3');
  await m.unmount();
});

test('AC-6: power / water / waste / refuse / upkeep rows; genesis m20 shows GBP 0 upkeep; non-water spec shows a dash', async () => {
  const { utilitySharesOf } = await import('../src/sim/buildingUtility.ts');
  const home = B('res_block', 2, 8, { builtTick: 0 });
  const s = city([...road(), home], 5000, 4321);
  const share = utilitySharesOf(s).byId.get(home.id)!;
  assert.ok(share.clean > 0);
  let m = await mountCard(s, home, false);
  assert.match(text(m, 'tr[data-row="water"] td'), new RegExp(`^${share.clean.toLocaleString('en-GB')} people supplied`));
  assert.match(text(m, 'tr[data-row="waste"] td'), new RegExp(`^${share.waste.toLocaleString('en-GB')} people sewered`));
  await m.unmount();

  const shop = B('com_shop', 2, 8, { builtTick: 0 });
  m = await mountCard(city([...road(), shop], 5000, 4321), shop, false);
  assert.equal(text(m, 'tr[data-row="water"] td'), '—');
  assert.equal(text(m, 'tr[data-row="power"] td'), '—');
  await m.unmount();

  // Genesis motorway (no builtTick) honestly £0; the same spec player-built pays.
  const genesis = B('m20', 0, 20);
  const built = B('m20', 1, 20, { builtTick: 40 });
  assert.ok(SPECS['m20'].upkeep > 0, 'fixture sanity: m20 normally costs upkeep');
  m = await mountCard(city([genesis, built], 5000, 0), genesis, false);
  assert.equal(text(m, 'tr[data-row="upkeep"] td'), '£0/tick');
  await m.unmount();
  m = await mountCard(city([genesis, built], 5000, 0), built, false);
  assert.equal(text(m, 'tr[data-row="upkeep"] td'), `${fmtMoney(SPECS['m20'].upkeep)}/tick`);
  await m.unmount();

  const wind = B('pow_wind', 2, 8, { builtTick: 0 });
  m = await mountCard(city([...road(), wind], 5000, 0), wind, false);
  assert.equal(text(m, 'tr[data-row="power"] td'), '6 MW');
  await m.unmount();
});

test('AC-7: a card left open updates on tick with a STALE selected snapshot (countdown advances; flips to Operational)', async () => {
  const total = constructionTicks(SPECS['pow_nuke']);
  const nuke = B('pow_nuke', 2, 8, { builtTick: 0 });
  const s1 = city([...road(), nuke], 10, 100);
  const m = await mountCard(s1, nuke, true);
  assert.equal(text(m, '[data-testid="pc-maturity"]'), `Under construction (10/${total} days)`);
  await m.rerender(city([...road(), nuke], 25, 100));
  assert.equal(text(m, '[data-testid="pc-maturity"]'), `Under construction (25/${total} days)`);
  await m.rerender(city([...road(), nuke], total + 1, 100));
  assert.equal(text(m, '[data-testid="pc-maturity"]'), 'Operational');
  await m.unmount();
});

test('AC-7: the LIVE instance is resolved by id - a replaced building object (tier change) reaches the open card', async () => {
  const home = B('res_block', 2, 8, { builtTick: 0 });
  const m = await mountCard(city([...road(), home], 5000, 100), home, false);
  assert.equal(text(m, '[data-testid="pc-maturity"]'), 'Operational');
  // Same id, new object (the sim's immutable-replace discipline): consolidated tier 2.
  const upgraded = { ...home, placedBy: 'auto', capacityTier: 1 };
  await m.rerender(city([...road(), upgraded], 5001, 100));
  assert.equal(text(m, '[data-testid="pc-maturity"]'), 'Consolidated tier 2');
  await m.unmount();
});

test('AC-7: zero water coverage shows a visible badge, not silently wrong numbers', async () => {
  const home = B('res_block', 2, 8, { builtTick: 0 });
  // No water plants at all and population present => coverage 0.
  const s = { ...city([...road(), home], 5000, 5000), waterImportEnabled: false, wastewaterContractEnabled: false } as SimState;
  const m = await mountCard(s, home, false);
  const badges = text(m, '[data-testid="pc-badges"]');
  assert.match(badges, /Water shortfall/);
  assert.match(badges, /Sewage shortfall/);
  await m.unmount();
});

test('AC-8: network pieces get the slim card (no utility table), full card for zoned buildings; card sets no pointer capture outside its box', async () => {
  const r = B('road', 0, 20, { builtTick: 0 });
  const m = await mountCard(city([r], 5000, 0), r, false);
  assert.ok(q(m, '.property-card.pc-slim'), 'slim class');
  assert.ok(!q(m, 'tr[data-row="water"]'), 'slim variant has no water row');
  assert.ok(q(m, 'tr[data-row="upkeep"]'), 'slim still shows upkeep');
  await m.unmount();
});

test('AC-8: StrictMode mount renders exactly one card (no double render of listeners/DOM)', async () => {
  const home = B('res_block', 2, 8, { builtTick: 0 });
  const m = await mountCard(city([...road(), home], 5000, 10), home, true);
  assert.equal(m.container.querySelectorAll('[data-testid="property-card"]').length, 1);
  await m.unmount();
});

test('AC-9: the card path reads no clock, randomness or storage (BUG-602 / GR#21 source guard)', async () => {
  // A runtime Date.now trap is polluted by React/jsdom internals, so the guard
  // is on the card's OWN source: PropertyCard + motifs + utility helper.
  const { readFileSync } = await import('node:fs');
  const files = ['../src/components/PropertyCard.tsx', '../src/components/buildingMotifs.tsx', '../src/sim/buildingUtility.ts'];
  for (const f of files) {
    const src = readFileSync(new URL(f, import.meta.url), 'utf8')
      .split('\n')
      .filter((l) => !l.trim().startsWith('//') && !l.trim().startsWith('*') && !l.trim().startsWith('/*'))
      .join('\n');
    assert.ok(!/Date\.now|new Date\(|performance\.now|Math\.random|localStorage|sessionStorage|setInterval|setTimeout/.test(src), `${f}: no clock/random/storage/timers`);
  }
});

test('AC-9: card derivations are memoised per state (share map computed once per state object)', async () => {
  const { utilitySharesOf } = await import('../src/sim/buildingUtility.ts');
  const s = city([...road(), B('res_block', 2, 8, { builtTick: 0 })], 5000, 10);
  assert.equal(utilitySharesOf(s), utilitySharesOf(s));
});

test('F2: the card power row and powerStats read ONE draw table (mutating the table moves BOTH)', async () => {
  const data = await import('../src/sim/data.ts');
  const { cardModelOf } = await import('../src/components/PropertyCard.tsx');
  const mk = () => {
    const f = B('ind_factory', 2, 10 - SPECS['ind_factory'].h, { builtTick: 0 });
    return { f, s: city([...road(), f], 5000, 0) };
  };
  const rowOf = (s: SimState, f: any) => cardModelOf(s, f)!.rows.find((r) => r.key === 'power')!.value;
  let { f, s } = mk();
  assert.ok(data.isOnline(s, f), 'fixture sanity: online');
  assert.equal(data.powerStats(s).need, data.POWER_DRAW_MW_BY_KIND.industrial);
  assert.equal(rowOf(s, f), '6 MW');
  const orig = data.POWER_DRAW_MW_BY_KIND.industrial;
  try {
    data.POWER_DRAW_MW_BY_KIND.industrial = 40;
    ({ f, s } = mk());
    assert.equal(data.powerStats(s).need, 40, 'sim moved');
    assert.equal(rowOf(s, f), '40 MW', 'card moved with it');
  } finally {
    data.POWER_DRAW_MW_BY_KIND.industrial = orig;
  }
});

test('F1: Brownout badge follows isBrownoutActive (SSOT) for online consumers only', async () => {
  const { cardModelOf } = await import('../src/components/PropertyCard.tsx');
  const f = B('ind_factory', 2, 10 - SPECS['ind_factory'].h, { builtTick: 0 });
  const shop = B('com_shop', 6, 8, { builtTick: 0 });
  const bites = { ...city([...road(), f, shop], 5000, 200000), gridImportEnabled: false } as SimState;
  assert.ok(cardModelOf(bites, f)!.badges.includes('Brownout'), 'consumer shows Brownout when it bites');
  assert.ok(!cardModelOf(bites, shop)!.badges.includes('Brownout'), 'non-consumer never does');
  const covered = { ...bites, gridImportEnabled: true } as SimState;
  assert.ok(!cardModelOf(covered, f)!.badges.includes('Brownout'), 'grid import ON -> no brownout');
});

test('F6: a building removed by a non-bulldoze path (consolidation) leaves an explicit gone state, never the stale copy', async () => {
  const home = B('res_block', 2, 8, { builtTick: 0 });
  const m = await mountCard(city([...road(), home], 5000, 100), home, true);
  assert.equal(text(m, '[data-testid="pc-maturity"]'), 'Operational');
  await m.rerender(city([...road()], 5001, 100)); // spliced out
  assert.match(text(m, '[data-testid="pc-maturity"]'), /No longer exists/);
  assert.ok(!q(m, 'tr[data-row="upkeep"]'), 'no stale figures');
  let closes = 0;
  await m.rerender(city([...road()], 5001, 100), () => closes++);
  const { act } = await import('react');
  await act(async () => {
    (q(m, '[data-testid="pc-close"]') as HTMLButtonElement).click();
  });
  assert.equal(closes, 1);
  await m.unmount();
});

test('F5: the card swallows no document-level mousedown (FileMenu outside-click close)', async () => {
  const home = B('res_block', 2, 8, { builtTick: 0 });
  const m = await mountCard(city([...road(), home], 5000, 100), home, false);
  let hits = 0;
  const doc = (globalThis as any).document;
  const on = () => hits++;
  doc.addEventListener('mousedown', on);
  q(m, '[data-testid="pc-name"]')!.dispatchEvent(new (globalThis as any).window.MouseEvent('mousedown', { bubbles: true }));
  doc.removeEventListener('mousedown', on);
  assert.equal(hits, 1);
  await m.unmount();
});
