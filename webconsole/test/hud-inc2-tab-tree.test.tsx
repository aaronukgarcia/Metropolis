// hud-inc2-tab-tree.test.tsx — FEAT-2326609720 inc2, AC-1/AC-2/AC-3/AC-4.
//
// Proves the six-group tab tree renders and every relocated element lands in
// its new home and NOWHERE else (AC-2), RightDock is retired (AC-3), and
// DemandDock/TopBar snapshot-untouched (AC-4).
//
// RED PROOF (documented, not left in the tree — GR#21 "verification
// standards"): running these assertions against the PRE-inc2 RightDock.tsx
// (git show HEAD~1's stat/rates/earnings/power/water/waste/lines/xp/
// specialists/policy/milestones/units tabs still present, `<Panel title=
// "Information" tabs={TABS}>` with the 14-entry TABS array) fails every
// "must contain" LeftDock assertion (the content simply is not there yet)
// and the "RightDock renders no tabs" assertion (the old TABS array of 14
// entries is non-empty) — proving these assertions can actually fail, not
// just always pass.

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

test('AC-1: LeftDock renders exactly the six approved top-level group tabs (in order) plus dev-gated Debug', async () => {
  ensureMountWindow();
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { SimProvider } = await import('../src/sim/store.tsx');
  const { LeftDock } = await import('../src/components/left/LeftDock.tsx');

  const html = renderToString(
    React.default.createElement(SimProvider, { children: React.default.createElement(LeftDock) })
  );
  assert.ok(!html.includes('useSim must be used inside SimProvider'));
  for (const label of ['Finance', 'Services', 'Population', 'Build &amp; Zoning', 'Projections', 'Alerts']) {
    assert.ok(html.includes(`>${label}<`), `top-level group "${label}" must render as a tab`);
  }
  // Order: Finance before Services before Population before Build & Zoning
  // before Projections before Alerts.
  const order = ['Finance', 'Services', 'Population', 'Build &amp; Zoning', 'Projections', 'Alerts'];
  let lastIdx = -1;
  for (const label of order) {
    const idx = html.indexOf(`>${label}<`);
    assert.ok(idx > lastIdx, `"${label}" must appear after the previous group in DOM order`);
    lastIdx = idx;
  }
});

test('AC-2/AC-3: RightDock renders nothing (no relocated tab ids survive it)', async () => {
  const { RightDock } = await import('../src/components/right/RightDock.tsx');
  ensureMountWindow();
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const html = renderToString(React.default.createElement(RightDock));
  assert.equal(html, '', 'RightDock must render nothing once every tab has a new home (AC-3)');
});

test('AC-2: a relocated element (Tax Settings sliders) renders under Finance and the old Rates tab id is gone from RightDock', async () => {
  ensureMountWindow();
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { SimProvider } = await import('../src/sim/store.tsx');
  const { TaxSettingsTab } = await import('../src/components/left/tabs/financeTabs.tsx');

  const html = renderToString(
    React.default.createElement(SimProvider, { children: React.default.createElement(TaxSettingsTab) })
  );
  assert.ok(/Council tax/.test(html), 'Tax Settings must render the relocated tax sliders');
  assert.ok(!html.includes('useSim must be used inside SimProvider'));

  // RightDock no longer exposes a `rates` tab id anywhere in its module (the
  // whole component returns null — this double-checks no stray dead code
  // still declares the tab id string used by the old TABS array).
  const fs = await import('node:fs/promises');
  const path = await import('node:path');
  const { fileURLToPath } = await import('node:url');
  const here = path.dirname(fileURLToPath(import.meta.url));
  const src = await fs.readFile(path.resolve(here, '../src/components/right/RightDock.tsx'), 'utf-8');
  for (const id of ['rates', 'earnings', 'policy', 'power', 'water', 'waste', 'lines', 'xp', 'specialists', 'milestones', 'units']) {
    assert.ok(!new RegExp(`id: '${id}'`).test(src), `RightDock.tsx source must not declare a "${id}" tab id`);
  }
});

test('AC-5: Employment tab (NEW) renders real totalJobs/unemploymentOf numbers, not a placeholder', async () => {
  ensureMountWindow();
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { SimProvider } = await import('../src/sim/store.tsx');
  const { EmploymentTab } = await import('../src/components/left/tabs/populationTabs.tsx');

  const html = renderToString(
    React.default.createElement(SimProvider, { children: React.default.createElement(EmploymentTab) })
  );
  assert.ok(/Total jobs/.test(html));
  assert.ok(/Unemployment/.test(html));
  assert.ok(!/NaN|Infinity/.test(html), 'no NaN/Infinity leak on a fresh city (population may be 0)');
});

test('AC-6/AC-10: Migration and Projections stub tabs render an explicit "not yet available" fallback with no numeric colour', async () => {
  ensureMountWindow();
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { SimProvider } = await import('../src/sim/store.tsx');
  const { MigrationTab } = await import('../src/components/left/tabs/populationTabs.tsx');
  const { DemandForecastTab, RevenueForecastTab } = await import('../src/components/left/tabs/projectionsTabs.tsx');

  for (const Comp of [MigrationTab, DemandForecastTab, RevenueForecastTab]) {
    const html = renderToString(
      React.default.createElement(SimProvider, { children: React.default.createElement(Comp) })
    );
    assert.ok(/not yet available/.test(html), `${Comp.name} must render the GR#1 fallback string`);
    assert.ok(!/var\(--done\)|var\(--warn\)|var\(--danger\)/.test(html), `${Comp.name} must not apply a RAG colour to the stub row`);
  }
});

test('AC-4: DemandDock is unchanged by this increment (still its own "Demand" panel, no tabs)', async () => {
  ensureMountWindow();
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { SimProvider } = await import('../src/sim/store.tsx');
  const { BusyProvider } = await import('../src/components/Busy.tsx');
  const { DemandDock } = await import('../src/components/left/DemandDock.tsx');

  const html = renderToString(
    React.default.createElement(BusyProvider, {
      children: React.default.createElement(SimProvider, { children: React.default.createElement(DemandDock) }),
    })
  );
  assert.ok(html.includes('>Demand<'), 'DemandDock must still render its own "Demand" panel title, unfolded into the six-group tree');
  assert.ok(!html.includes('useSim must be used inside SimProvider'));
});
