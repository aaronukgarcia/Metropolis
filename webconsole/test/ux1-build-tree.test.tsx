// ux1-build-tree.test.tsx - FEAT-9995385591 (UX-1 of FEAT-2326609843): the
// tree-nodal build browser. One test group per acceptance criterion, named by
// AC id (docs/planning/acceptance/feat-ui-overhaul.md section 2, UX-1), plus
// BUG-1162 (clipping / duplicate Placing hint) and the BottomBar half of
// BUG-1185 (cap copy follows the spec's maxPerCity).
//
// Run: npx tsx --test test/ux1-build-tree.test.tsx
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

import { PALETTE, SPECS, paletteByDomain, isPlaceable } from '../src/sim/data.ts';
import { initialState, specUnlocked } from '../src/sim/engine.ts';
import {
  buildTree,
  flattenVisible,
  navigate,
  initialExpanded,
  pushRecent,
  RECENT_MAX,
  capChip,
  capAria,
  capTitle,
} from '../src/components/bottom/buildTree.ts';

const BOTTOMBAR_SRC = readFileSync(new URL('../src/components/bottom/BottomBar.tsx', import.meta.url), 'utf8');

// ------------------------------------------------------------------ DOM harness

async function installJsdom() {
  const { JSDOM } = await import('jsdom');
  const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', {
    url: 'http://localhost/',
    pretendToBeVisual: true,
  });
  const { window } = dom;
  const g = globalThis as any;
  g.window = window;
  g.document = window.document;
  Object.defineProperty(globalThis, 'navigator', { value: window.navigator, configurable: true, writable: true });
  g.HTMLElement = window.HTMLElement;
  g.requestAnimationFrame = window.requestAnimationFrame ? window.requestAnimationFrame.bind(window) : (cb: any) => setTimeout(cb, 0);
  g.cancelAnimationFrame = window.cancelAnimationFrame ? window.cancelAnimationFrame.bind(window) : clearTimeout;
  g.IS_REACT_ACT_ENVIRONMENT = true;
  if (typeof g.ResizeObserver === 'undefined') {
    g.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    };
  }
  return dom as any;
}

async function mountBar() {
  const dom = await installJsdom();
  const React = (await import('react')).default;
  const { createRoot } = await import('react-dom/client');
  const { act } = await import('react-dom/test-utils');
  const { SimProvider } = await import('../src/sim/store.tsx');
  const { useSim } = await import('../src/sim/simContext.ts');
  const { BottomBar } = await import('../src/components/bottom/BottomBar.tsx');
  let latest: any = null;
  const Probe = () => {
    latest = useSim();
    return null;
  };
  const container = dom.window.document.getElementById('root');
  const root = createRoot(container);
  await act(async () => {
    root.render(React.createElement(SimProvider, { children: React.createElement(React.Fragment, null, React.createElement(Probe), React.createElement(BottomBar)) }));
  });
  const q = (sel: string) => container.querySelector(sel) as any;
  const qa = (sel: string) => Array.from(container.querySelectorAll(sel)) as any[];
  const click = async (el: any) => {
    await act(async () => {
      el.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true, cancelable: true }));
    });
  };
  const key = async (el: any, k: string) => {
    await act(async () => {
      el.dispatchEvent(new dom.window.KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true }));
    });
  };
  const head = (label: string) => qa('.bt-head').find((el) => el.querySelector('.tree-title')?.textContent === label);
  return { dom, React, act, root, container, q, qa, click, key, head, sim: () => latest, unmount: async () => act(async () => root.unmount()) };
}

// ------------------------------------------------------------------ AC-1.1

test('AC-1.1: tree is domain > family > subtype; at most one domain auto-expanded at start', () => {
  const tree = buildTree(new Set(initialExpanded()), '', []);
  assert.deepEqual(tree.map((n) => n.label), paletteByDomain().map((d) => d.domain), 'domains in data.ts order');
  const openDomains = tree.filter((n) => n.kind === 'domain' && n.expanded);
  assert.equal(openDomains.length, 1, 'exactly one domain auto-expanded');
  assert.ok(openDomains[0].children.every((f) => f.kind === 'family'));
  const openFams = openDomains[0].children.filter((f) => f.expanded);
  assert.equal(openFams.length, 1);
  assert.equal(openFams[0].label, PALETTE[0].title);
  assert.ok(openFams[0].children.every((s) => s.kind === 'spec'));
});

test('AC-1.1: click expands/collapses; hover never changes anything; expansion survives a Build/Move tab switch', async () => {
  const h = await mountBar();
  try {
    const health = h.head('Health');
    assert.ok(health, 'Health domain header rendered');
    assert.equal(health.getAttribute('aria-expanded'), 'false');
    const before = h.container.innerHTML;
    await h.act(async () => {
      health.dispatchEvent(new h.dom.window.MouseEvent('mouseover', { bubbles: true }));
      health.dispatchEvent(new h.dom.window.MouseEvent('mouseenter', { bubbles: false }));
    });
    assert.equal(h.container.innerHTML, before, 'hover is inert');
    await h.click(health);
    assert.equal(h.head('Health').getAttribute('aria-expanded'), 'true');
    // switch to Move then back
    const tabs = h.qa('[role=tab]');
    await h.click(tabs.find((t) => t.textContent === 'Move'));
    assert.equal(h.q('.bt-tree'), null, 'Build tree unmounted on Move tab');
    await h.click(h.qa('[role=tab]').find((t) => t.textContent === 'Build'));
    assert.equal(h.head('Health').getAttribute('aria-expanded'), 'true', 'expansion survived the tab switch');
    await h.click(h.head('Health'));
    assert.equal(h.head('Health').getAttribute('aria-expanded'), 'false', 'second click collapses');
  } finally {
    await h.unmount();
  }
});

// ------------------------------------------------------------------ AC-1.2

test('AC-1.2: subtypes render INLINE under their family (same tree, nested group), no second pane', async () => {
  const h = await mountBar();
  try {
    assert.equal(h.q('.tree-detail'), null, 'no separate detail pane');
    assert.equal(h.qa('.tree-wrap').length, 0);
    const net = h.head('Network');
    assert.ok(net);
    const group = net.parentElement.querySelector('[role=group]');
    assert.ok(group, 'subtypes sit in a group under the family header');
    const names = Array.from(group.querySelectorAll('.pal-name')).map((e: any) => e.textContent);
    assert.deepEqual(names, PALETTE[0].items.map((id) => SPECS[id].name));
    // sticky family header (CSS)
    const css = readFileSync(new URL('../src/styles.css', import.meta.url), 'utf8');
    assert.match(css, /\.bt-family\s*>\s*\.bt-head\s*\{[^}]*position:\s*sticky/);
  } finally {
    await h.unmount();
  }
});

// ------------------------------------------------------------------ AC-1.3

test('AC-1.3: subtype order is spec (PALETTE) order and does NOT change with availability', () => {
  for (const fam of PALETTE) {
    const open = new Set([...initialExpanded(), `f:${fam.title}`, ...paletteByDomain().map((d) => `d:${d.domain}`)]);
    const node = flattenVisible(buildTree(open, '', [])).find((n) => n.id === `f:${fam.title}`)!;
    assert.deepEqual(node.children.map((c) => c.specId), fam.items.filter((i) => SPECS[i]), fam.title);
  }
});

test('AC-1.3: locked chip + SpecCard-on-locked-click carry over; locked rows are not disabled', async () => {
  const h = await mountBar();
  try {
    const st = initialState();
    const lockedId = PALETTE[0].items.find((id) => !specUnlocked(st, SPECS[id]))!;
    assert.ok(lockedId, 'Network has a locked road at level 1');
    const row = h.qa('.bt-spec').find((el) => el.querySelector('.pal-name')?.textContent === SPECS[lockedId].name);
    assert.ok(row.className.includes('locked'));
    assert.match(row.getAttribute('aria-label'), /^Locked/);
    assert.equal(row.hasAttribute('disabled'), false);
    assert.match(row.querySelector('.pal-cost').textContent, /^Lv \d+/);
    await h.click(row);
    assert.equal(h.sim().state.tool.spec, undefined, 'locked click does not select a tool');
    assert.ok(h.container.ownerDocument.body.textContent!.includes(SPECS[lockedId].name), 'card open');
  } finally {
    await h.unmount();
  }
});

// ------------------------------------------------------------------ AC-1.4

test('AC-1.4: search filters across all levels on name + blurb; Esc clears; search never changes the tool', async () => {
  const q = 'hvdc';
  const tree = buildTree(new Set(), q, []);
  const rows = flattenVisible(tree);
  const specs = rows.filter((r) => r.kind === 'spec').map((r) => r.specId);
  assert.ok(specs.includes('pow_hvdc'), 'name match');
  assert.ok(rows.some((r) => r.kind === 'domain') && rows.some((r) => r.kind === 'family'), 'ancestors kept and expanded');
  assert.ok(specs.every((id) => (SPECS[id!].name + SPECS[id!].blurb).toLowerCase().includes(q)), 'only matches');
  // blurb-only match
  const blurbWord = 'subsea';
  const blurbHit = flattenVisible(buildTree(new Set(), blurbWord, [])).filter((r) => r.kind === 'spec');
  assert.ok(blurbHit.some((r) => r.specId === 'pow_hvdc'), 'blurb match');
  assert.equal(flattenVisible(buildTree(new Set(), 'zzzqqq', [])).length, 0);

  const h = await mountBar();
  try {
    const input = h.q('.bt-input');
    const toolBefore = JSON.stringify(h.sim().state.tool);
    await h.act(async () => {
      const setter = Object.getOwnPropertyDescriptor(h.dom.window.HTMLInputElement.prototype, 'value')!.set!;
      setter.call(input, 'hvdc');
      input.dispatchEvent(new h.dom.window.Event('input', { bubbles: true }));
    });
    assert.ok(h.qa('.bt-spec').some((e) => e.querySelector('.pal-name')?.textContent === SPECS.pow_hvdc.name));
    assert.ok(h.qa('.bt-spec').length < 10, 'filtered live');
    assert.equal(JSON.stringify(h.sim().state.tool), toolBefore, 'search alone dispatches nothing');
    // A REAL window keydown listener (as MapView/keyhandler installs): Esc with a query
    // must clear the filter and must NOT leak to it (else the active tool is cancelled).
    let leaked = 0;
    const onWin = (e: any) => { if (e.key === 'Escape') leaked++; };
    h.dom.window.addEventListener('keydown', onWin);
    await h.key(h.q('.bt-input'), 'Escape');
    assert.equal(h.q('.bt-input').value, '', 'Esc clears');
    assert.equal(leaked, 0, 'Esc with a query must not reach the window keydown handler');
    await h.key(h.q('.bt-input'), 'Escape');
    assert.equal(leaked, 1, 'Esc with an empty query still falls through to the global cancel');
    h.dom.window.removeEventListener('keydown', onWin);
  } finally {
    await h.unmount();
  }
});

// ------------------------------------------------------------------ AC-1.5

test('AC-1.5: Recent pseudo-family tops the tree, holds the last N picks (deduped, capped, newest first)', async () => {
  let r: string[] = [];
  for (const id of ['road', 'rd_avenue', 'road', 'res_hut', 'com_shop', 'park', 'pow_wind', 'hea_clinic']) r = pushRecent(r, id);
  assert.equal(r.length, RECENT_MAX);
  assert.equal(r[0], 'hea_clinic');
  assert.equal(new Set(r).size, r.length, 'no duplicates');
  // dedupe must hold inside the cap window too (a re-pick moves to the front, never doubles)
  assert.deepEqual(pushRecent(pushRecent(pushRecent([], 'road'), 'res_hut'), 'road'), ['road', 'res_hut']);
  const tree = buildTree(new Set(initialExpanded()), '', ['road']);
  assert.equal(tree[0].kind, 'recent');
  assert.equal(tree[0].label, 'Recent');
  assert.equal(buildTree(new Set(initialExpanded()), '', [])[0].kind, 'domain', 'hidden while empty');

  const h = await mountBar();
  try {
    assert.equal(h.q('.bt-recent'), null);
    const road = h.qa('.bt-spec').find((e) => e.querySelector('.pal-name')?.textContent === SPECS.road.name);
    await h.click(road);
    assert.equal(h.sim().state.tool.spec, 'road');
    assert.ok(!h.q('.bt-recent'), 'a pick alone is not a placement: Recent stays empty');
    await h.act(async () => h.sim().dispatch({ type: 'tool', tool: { mode: 'select' } }));
    assert.ok(!h.q('.bt-recent'), 'a cancelled pick never enters Recent');
    // re-pick and actually place (find a tile the reducer accepts)
    await h.click(h.qa('.bt-spec').find((e) => e.querySelector('.pal-name')?.textContent === SPECS.road.name));
    const n0 = h.sim().state.buildings.length;
    for (let y = 40; y < 60 && h.sim().state.buildings.length === n0; y++) {
      for (let x = 40; x < 60 && h.sim().state.buildings.length === n0; x++) {
        await h.act(async () => h.sim().dispatch({ type: 'place', spec: 'road', x, y }));
      }
    }
    assert.ok(h.sim().state.buildings.length > n0, 'test setup: a road was placed');
    assert.ok(h.q('.bt-recent'), 'Recent appears after a PLACEMENT');
    assert.equal(h.container.querySelector('.bt-tree').firstElementChild.className.includes('bt-recent'), true, 'at the top');
    assert.ok(h.q('.bt-recent .bt-spec'));
  } finally {
    await h.unmount();
  }
  // keys 1-9 quick-pick untouched
  const kb = readFileSync(new URL('../src/sim/keybindings.ts', import.meta.url), 'utf8');
  assert.match(kb, /PALETTE_FLAT\.slice\(0, 9\)/);
});

// ------------------------------------------------------------------ AC-1.6

test('AC-1.6: every placeable spec reachable in <=3 clicks from a cold open; picking dispatches the same tool action', async () => {
  const cold = new Set(initialExpanded());
  const domOf = new Map<string, string>();
  for (const d of paletteByDomain()) for (const f of d.families) domOf.set(f.title, d.domain);
  let reachable = 0;
  for (const fam of PALETTE) {
    for (const id of fam.items) {
      if (!SPECS[id]) continue;
      // clicks needed = collapsed ancestors + the pick itself
      let clicks = 1;
      if (!cold.has(`f:${fam.title}`)) clicks++;
      if (!cold.has(`d:${domOf.get(fam.title)}`)) clicks++;
      assert.ok(clicks <= 3, `${id} needs ${clicks} clicks`);
      reachable++;
    }
  }
  assert.equal(reachable, PALETTE.flatMap((f) => f.items).filter((i) => SPECS[i]).length);
  // and the tree actually offers every spec when everything is open
  const all = new Set<string>(['r:recent', ...paletteByDomain().flatMap((d) => [`d:${d.domain}`, ...d.families.map((f) => `f:${f.title}`)])]);
  const shown = flattenVisible(buildTree(all, '', [])).filter((r) => r.kind === 'spec').map((r) => r.specId);
  assert.deepEqual([...shown].sort(), PALETTE.flatMap((f) => f.items).filter((i) => SPECS[i]).sort(), 'every placeable spec appears exactly once');

  const h = await mountBar();
  try {
    const st = initialState();
    const id = PALETTE.flatMap((f) => f.items).find((i) => isPlaceable(st, SPECS[i]) && st.funds >= SPECS[i].cost && PALETTE[0].items.includes(i))!;
    const row = h.qa('.bt-spec').find((e) => e.querySelector('.pal-name')?.textContent === SPECS[id].name);
    await h.click(row);
    assert.deepEqual(h.sim().state.tool, { mode: 'build', spec: id });
  } finally {
    await h.unmount();
  }
});

// ------------------------------------------------------------------ accessibility

test('A11Y: ARIA tree roles, levels, aria-expanded, roving tabindex', async () => {
  const h = await mountBar();
  try {
    const tree = h.q('[role=tree]');
    assert.ok(tree);
    assert.ok(tree.getAttribute('aria-label'));
    const items = h.qa('[role=treeitem]');
    assert.ok(items.length > 10);
    for (const it of items) assert.ok(Number(it.getAttribute('aria-level')) >= 1);
    for (const it of items.filter((e) => e.classList.contains('bt-head'))) assert.ok(['true', 'false'].includes(it.getAttribute('aria-expanded')));
    assert.ok(h.qa('[role=group]').length >= 1);
    assert.equal(items.filter((e) => e.tabIndex === 0).length, 1, 'exactly one tabbable row');
  } finally {
    await h.unmount();
  }
});

test('A11Y: arrow keys expand/collapse/move; Enter picks', async () => {
  // pure model
  const rowsOf = (open: string[]) => flattenVisible(buildTree(new Set(open), '', []));
  const cold = initialExpanded();
  let rows = rowsOf(cold);
  const util = rows.find((r) => r.id === 'd:Utilities')!;
  assert.deepEqual(navigate(rows, util.id, 'ArrowRight'), { focus: util.id, expand: util.id });
  rows = rowsOf([...cold, util.id]);
  assert.equal(navigate(rows, util.id, 'ArrowRight').focus, rows[rows.findIndex((r) => r.id === util.id) + 1].id, 'Right on open node enters first child');
  assert.deepEqual(navigate(rows, util.id, 'ArrowLeft'), { focus: util.id, collapse: util.id });
  const fam = rows.find((r) => r.kind === 'family' && r.parentId === util.id)!;
  assert.equal(navigate(rows, fam.id, 'ArrowLeft').focus, util.id, 'Left on closed child goes to parent');
  assert.equal(navigate(rows, util.id, 'ArrowDown').focus, rows[rows.findIndex((r) => r.id === util.id) + 1].id);
  assert.equal(navigate(rows, rows[0].id, 'ArrowUp').focus, rows[0].id, 'clamped at top');
  assert.equal(navigate(rows, util.id, 'End').focus, rows[rows.length - 1].id);

  // DOM
  const h = await mountBar();
  try {
    const util = h.head('Utilities');
    util.focus();
    await h.key(util, 'ArrowRight');
    assert.equal(h.head('Utilities').getAttribute('aria-expanded'), 'true');
    await h.key(h.head('Utilities'), 'ArrowLeft');
    assert.equal(h.head('Utilities').getAttribute('aria-expanded'), 'false');
    // Enter on a spec row picks it
    const roadRow = h.qa('.bt-spec').find((e) => e.querySelector('.pal-name')?.textContent === SPECS.road.name);
    roadRow.focus();
    await h.key(roadRow, 'Enter');
    assert.equal(h.sim().state.tool.spec, 'road');
    // Enter on a family toggles it
    const net = h.head('Network');
    net.focus();
    await h.key(net, 'Enter');
    assert.equal(h.head('Network').getAttribute('aria-expanded'), 'false');
  } finally {
    await h.unmount();
  }
});

// ------------------------------------------------------------------ BUG-1162

test('BUG-1162: palette text never forced mid-word, no family column to collide with, Placing hint not duplicated', async () => {
  const css = readFileSync(new URL('../src/styles.css', import.meta.url), 'utf8');
  const cap = css.match(/\.bt-spec \.pal-cap\s*\{[^}]*\}/)![0];
  assert.doesNotMatch(cap, /overflow-wrap:\s*anywhere|word-break:\s*break-all/);
  assert.match(cap, /white-space:\s*normal/);
  assert.match(css, /\.bt-spec \.pal-cost\s*\{[^}]*flex:\s*0 0 auto/, 'cost column cannot shrink into the text');
  const h = await mountBar();
  try {
    assert.equal(h.q('.tree-fams'), null, 'no separate family column');
    const road = h.qa('.bt-spec').find((e) => e.querySelector('.pal-name')?.textContent === SPECS.road.name);
    await h.click(road);
    assert.equal(h.sim().state.tool.spec, 'road');
    assert.ok(!/Placing/.test(h.container.textContent), 'BuildTab no longer renders its own Placing hint (the map tool chip owns it)');
  } finally {
    await h.unmount();
  }
});

// ------------------------------------------------------------------ BUG-1185 (BottomBar half)

test('BUG-1185: cap copy uses the spec maxPerCity (pow_hvdc has 2), not a hardcoded "one per city"', () => {
  assert.equal(SPECS.pow_hvdc.maxPerCity, 2);
  assert.equal(capChip(SPECS.pow_hvdc), '2 per city');
  assert.match(capAria(SPECS.pow_hvdc), /2 per city/);
  assert.doesNotMatch(capAria(SPECS.pow_hvdc), /^One per city/);
  assert.match(capTitle(SPECS.pow_hvdc), /limited to 2 per city/);
  assert.equal(capChip(SPECS.pow_hydro), 'One per city');
  assert.match(capAria(SPECS.pow_hydro), /One per city — Three Gorges Dam already built/);
  assert.match(capTitle(SPECS.pow_hydro), /one per city, and this city already has one/);
  assert.doesNotMatch(BOTTOMBAR_SRC, /One per city|one per city/, 'BottomBar.tsx has no hardcoded cap literal');
});

// ------------------------------------------------------------------ key propagation (r1 blocker)

test('keys the tree consumes never reach the window keydown handler; keys it does not consume still do', async () => {
  const h = await mountBar();
  try {
    const seen: string[] = [];
    const onWin = (e: any) => seen.push(e.key);
    h.dom.window.addEventListener('keydown', onWin);
    const first = h.q('.bt-tree [tabindex="0"]');
    await h.act(async () => first.focus());
    for (const k of ['ArrowDown', 'ArrowDown', 'ArrowUp', 'ArrowRight', 'ArrowLeft', 'End', 'Home', 'Enter']) {
      await h.key(h.dom.window.document.activeElement, k);
    }
    assert.deepEqual(seen, [], 'tree navigation/Enter leaked to the window handler: ' + seen.join(','));
    for (const k of ['p', 'Escape', ' ', '1']) await h.key(h.dom.window.document.activeElement, k);
    assert.deepEqual(seen, ['p', 'Escape', ' ', '1'], 'unconsumed keys must still reach global shortcuts');
    h.dom.window.removeEventListener('keydown', onWin);
  } finally {
    await h.unmount();
  }
});
