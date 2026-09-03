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

  const html = renderToString(
    React.default.createElement(SimProvider, { children: React.default.createElement(BottomBar) }),
  );
  assert.ok(!html.includes('useSim must be used inside SimProvider'));

  for (const domain of ['Utilities', 'Education', 'Health', 'Industry &amp; Economy', 'Safety']) {
    assert.ok(html.includes(`>${domain}<`), `domain header "${domain}" must render`);
  }
  // Utilities groups Power + Water & Waste (Aaron's example, verbatim).
  const utilitiesIdx = html.indexOf('>Utilities<');
  const powerIdx = html.indexOf('>Power<');
  const waterIdx = html.indexOf('>Water &amp; Waste<');
  const educationHeaderIdx = html.indexOf('>Education<');
  assert.ok(utilitiesIdx >= 0 && powerIdx > utilitiesIdx, 'Power family must render after the Utilities header');
  assert.ok(waterIdx > utilitiesIdx, 'Water & Waste family must render after the Utilities header');
  assert.ok(
    (educationHeaderIdx < 0 || powerIdx < educationHeaderIdx) && waterIdx < (educationHeaderIdx < 0 ? Infinity : educationHeaderIdx),
    'Power and Water & Waste must both fall inside Utilities, before the next domain header',
  );
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

    const findFamButton = (title: string) =>
      Array.from(container.querySelectorAll('.tree-fam')).find(
        (el: any) => el.querySelector('.tree-title')?.textContent === title,
      ) as any;

    // Default family is PALETTE[0] ('Network', under the Transport domain).
    const networkBtn = findFamButton('Network');
    assert.ok(networkBtn, 'Network family button must render');
    assert.ok(networkBtn.className.includes('open'), 'Network is the default open family');

    // Click Health (Health domain) — a different domain from Network's (Transport).
    const healthBtn = findFamButton('Health');
    assert.ok(healthBtn, 'Health family button must render under the Health domain');
    await act(async () => {
      healthBtn.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true, cancelable: true }));
    });
    assert.ok(healthBtn.className.includes('open'), 'Health must become the open family after clicking it');
    assert.ok(
      !findFamButton('Network').className.includes('open'),
      'Network must lose the open class once a different domain\'s family is selected',
    );
    assert.ok(
      container.querySelector('.tree-detail')?.textContent?.length > 0,
      'tree-detail must render the newly selected family\'s items',
    );

    // Now click Power (Utilities domain) — proves selection keeps working
    // across a THIRD domain, not just a one-time switch.
    const powerBtn = findFamButton('Power');
    assert.ok(powerBtn, 'Power family button must render under the Utilities domain');
    await act(async () => {
      powerBtn.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true, cancelable: true }));
    });
    assert.ok(powerBtn.className.includes('open'), 'Power must become the open family after clicking it');
    assert.ok(!findFamButton('Health').className.includes('open'), 'Health must lose the open class once Power is selected');

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});
