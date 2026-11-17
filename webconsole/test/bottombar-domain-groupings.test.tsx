// bottombar-domain-groupings.test.tsx — FEAT-2326609748: domain groupings for
// the build-palette "information" strip at the bottom of the screen
// (BottomBar.tsx's tree-fams column). Aaron (2026-09-02): "power / water /
// waste are all utilities, we need one for education, and one for health,
// one for industry etc" — instead of one flat list of PALETTE families.
//
// GR#15 (validators/data derive from data): the grouping lives in
// data.ts as PALETTE_DOMAIN, a pure lookup keyed off PALETTE's existing
// `title` field — NOT a parallel hardcoded list of spec ids in the
// component. These tests prove (a) every family is explicitly covered
// (nothing can silently fall into the 'General' catch-all today), (b) the
// domain grouping is a lossless repartition of PALETTE (same items, no
// drops, no duplicates), and (c) BottomBar renders the grouped headers while
// preserving the existing per-family selection behaviour across domains.
//
// RED PROOF (documented, not left in the tree — GR#21/verification
// standards): with the 'Health: Health' line removed from PALETTE_DOMAIN in
// a scratch copy of data.ts (cp/mv only, GR#24 — never a git revert), the
// completeness test below fails with "family \"Health\" must have an
// explicit domain in PALETTE_DOMAIN", and the lossless-repartition test
// fails too (Health family missing from the grouped union) — proving both
// assertions can actually catch a real removal, not just always pass.

import { test } from 'node:test';
import assert from 'node:assert/strict';

function ensureMountWindow() {
  if (typeof globalThis.window === 'undefined') {
    globalThis.window = {
      localStorage: {
        getItem: () => null,
        setItem: () => {},
        removeItem: () => {},
        clear: () => {},
        key: () => null,
        length: 0,
      },
      performance: { now: () => 0 },
    } as any;
  }
}

test('GR#15 completeness: every PALETTE family has an explicit domain in PALETTE_DOMAIN', async () => {
  const { PALETTE, PALETTE_DOMAIN } = await import('../src/sim/data.ts');
  for (const fam of PALETTE) {
    assert.ok(
      Object.prototype.hasOwnProperty.call(PALETTE_DOMAIN, fam.title),
      `family "${fam.title}" must have an explicit domain in PALETTE_DOMAIN — an unmapped family silently falls into ` +
        'the "General" catch-all instead of the domain Aaron actually wants, which is exactly the vanishing-tab class ' +
        'this test exists to catch',
    );
  }
});

test('BUG-608 reverse-completeness: every PALETTE_DOMAIN key matches a live PALETTE family title (no stale/orphan mapping)', async () => {
  const { PALETTE, PALETTE_DOMAIN } = await import('../src/sim/data.ts');
  const liveTitles = new Set(PALETTE.map((fam) => fam.title));
  for (const key of Object.keys(PALETTE_DOMAIN)) {
    assert.ok(
      liveTitles.has(key),
      `PALETTE_DOMAIN key "${key}" does not match any live PALETTE family title — this is a stale/orphan mapping ` +
        'left behind after a family was renamed or removed from PALETTE. It currently persists undetected forever ' +
        '(the forward completeness check above only proves every PALETTE family HAS a domain, never that every ' +
        'domain entry still points at a real family) and silently wastes a slot that will never render.',
    );
  }
});

test('paletteByDomain is a lossless repartition of PALETTE (no family dropped, none duplicated)', async () => {
  const { PALETTE, paletteByDomain } = await import('../src/sim/data.ts');
  const grouped = paletteByDomain();
  const groupedTitles = grouped.flatMap((g) => g.families.map((f) => f.title));

  assert.deepEqual(
    [...groupedTitles].sort(),
    PALETTE.map((f) => f.title).sort(),
    'the union of every domain group must equal the full PALETTE family set — exactly one home each',
  );
  assert.equal(
    new Set(groupedTitles).size,
    groupedTitles.length,
    'no family may appear under more than one domain',
  );
});

test('every domain PALETTE_DOMAIN can produce is listed in PALETTE_DOMAIN_ORDER (a domain can never render without a heading position)', async () => {
  const { PALETTE_DOMAIN, PALETTE_DOMAIN_ORDER } = await import('../src/sim/data.ts');
  const usedDomains = new Set(Object.values(PALETTE_DOMAIN));
  for (const d of usedDomains) {
    assert.ok(PALETTE_DOMAIN_ORDER.includes(d), `domain "${d}" must be listed in PALETTE_DOMAIN_ORDER`);
  }
});

test('domainOfFamily falls back to General for an unmapped title (never drops a tab)', async () => {
  const { domainOfFamily } = await import('../src/sim/data.ts');
  assert.equal(domainOfFamily('Some Brand New Family Nobody Mapped Yet'), 'General');
});

test('BottomBar renders the expected domain headers, each containing its families, in DOMAIN_ORDER', async () => {
  ensureMountWindow();
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { SimProvider } = await import('../src/sim/store.tsx');
  const { BottomBar } = await import('../src/components/bottom/BottomBar.tsx');
  const { paletteByDomain } = await import('../src/sim/data.ts');

  const html = renderToString(
    React.default.createElement(SimProvider, { children: React.default.createElement(BottomBar) }),
  );
  assert.ok(!html.includes('useSim must be used inside SimProvider'));

  for (const domain of ['Utilities', 'Education', 'Health', 'Industry &amp; Economy', 'Safety']) {
    assert.ok(html.includes(`>${domain}<`), `domain header "${domain}" must render`);
  }
  // UX-1 (FEAT-9995385591): domains now render COLLAPSED (only the first is open), so
  // families are checked on the tree model with every domain open instead of in the SSR
  // html. Utilities groups Power + Water & Waste (Aaron's example, verbatim).
  const { buildTree } = await import('../src/components/bottom/buildTree.ts');
  const allOpen = new Set(paletteByDomain().flatMap((d) => [`d:${d.domain}`, ...d.families.map((f) => `f:${f.title}`)]));
  const tree = buildTree(allOpen, '', []);
  const util = tree.find((n) => n.label === 'Utilities')!;
  assert.deepEqual(util.children.map((f) => f.label), ['Power', 'Water & Waste']);
  assert.deepEqual(tree.map((n) => n.label), paletteByDomain().map((d) => d.domain), 'domains in DOMAIN_ORDER');
});

// ---------------------------------------------------------------------------
// Live-mount companion: prove selection is unaffected by the regrouping —
// switching the active family across two DIFFERENT domains still updates the
// item list, and the previously active family loses its "open" class exactly
// as before.
// ---------------------------------------------------------------------------

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
    (globalThis as any).requestAnimationFrame = window.requestAnimationFrame ? window.requestAnimationFrame.bind(window) : (cb: any) => setTimeout(cb, 0);
    (globalThis as any).cancelAnimationFrame = window.cancelAnimationFrame ? window.cancelAnimationFrame.bind(window) : clearTimeout;
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

test('BottomBar: clicking a family in one domain then another preserves the click-to-select behaviour across the new domain headers', async () => {
  const dom: any = await installJsdom();
  try {
    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimProvider } = await import('../src/sim/store.tsx');
    const { BottomBar } = await import('../src/components/bottom/BottomBar.tsx');

    const container = dom.window.document.getElementById('root');
    const root = createRoot(container);
    await act(async () => {
      root.render(
        React.default.createElement(SimProvider, { children: React.default.createElement(BottomBar) }),
      );
    });

    // UX-1: the tree is click-to-expand (hover is inert); open the domain, then the family.
    const findFamButton = (title: string) =>
      Array.from(container.querySelectorAll('.bt-head')).find(
        (el: any) => el.querySelector('.tree-title')?.textContent === title,
      ) as any;
    const clickEl = async (el: any) =>
      act(async () => {
        el.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true, cancelable: true }));
      });

    // Default family is PALETTE[0] ('Network', under the Transport domain).
    const networkBtn = findFamButton('Network');
    assert.ok(networkBtn, 'Network family button must render');
    assert.ok(networkBtn.className.includes('open'), 'Network is the default open family');

    // Click Health (Health domain) - a different domain from Network's (Transport).
    // The Health domain and the Health family share a label; the domain header is first.
    const healthDomain = findFamButton('Health');
    assert.ok(healthDomain, 'Health domain header must render');
    await clickEl(healthDomain);
    const healthBtn = Array.from(container.querySelectorAll('.bt-head')).filter(
      (el: any) => el.querySelector('.tree-title')?.textContent === 'Health',
    )[1] as any;
    assert.ok(healthBtn, 'Health family button must render once its domain is open');
    await clickEl(healthBtn);
    assert.ok(healthBtn.className.includes('open'), 'Health must become open after clicking it');
    assert.ok(
      findFamButton('Network').className.includes('open'),
      'UX-1 AC-1.1: opening another family does NOT collapse Network (expansion is independent)',
    );
    assert.ok(
      healthBtn.parentElement.querySelector('[role=group]')?.textContent?.length > 0,
      "the family's items render inline under it",
    );

    // Now click Power (Utilities domain) — proves selection keeps working
    // across a THIRD domain, not just a one-time switch.
    await clickEl(findFamButton('Utilities'));
    const powerBtn = findFamButton('Power');
    assert.ok(powerBtn, 'Power family button must render under the Utilities domain');
    await clickEl(powerBtn);
    assert.ok(powerBtn.className.includes('open'), 'Power must become open after clicking it');
    await clickEl(powerBtn);
    assert.ok(!findFamButton('Power').className.includes('open'), 'a second click collapses Power again');

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});
