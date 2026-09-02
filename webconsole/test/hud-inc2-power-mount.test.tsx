// hud-inc2-power-mount.test.tsx — FEAT-2326609720 inc2, AC-9 live-component
// proof. Split out from hud-inc2-determinism-and-power.test.mjs because a
// .tsx module (store.tsx) cannot be loaded under plain `node --test` — this
// file runs under `tsx --test` (mirrors mount.test.tsx's PowerTab smoke test).

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

test('AC-9: PowerTab (live component, relocated to Services/Utilities) renders without throwing and shows the toggle, no NaN', async () => {
  ensureMountWindow();
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { SimProvider } = await import('../src/sim/store.tsx');
  const { PowerTab } = await import('../src/components/left/tabs/servicesTabs.tsx');

  const html = renderToString(
    React.default.createElement(SimProvider, { children: React.default.createElement(PowerTab) })
  );
  assert.ok(html.length > 0, 'rendered HTML must be non-empty');
  assert.ok(
    !html.includes('useSim must be used inside SimProvider'),
    'PowerTab must render inside the provider without a context error'
  );
  assert.ok(/Use external power cover/.test(html), 'the toggle control must be present');
  assert.ok(/Imported MW/.test(html), 'the imported-MW readout must be present');
  assert.ok(/>On</.test(html), 'a new city must render the toggle defaulted ON');
  assert.ok(!/NaN|Infinity/.test(html), 'no NaN/Infinity in the rendered panel');
});

test('AC-1/AC-2: UtilitiesTab hosts Power/Water/Waste as sibling sub-tabs under Services', async () => {
  ensureMountWindow();
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { SimProvider } = await import('../src/sim/store.tsx');
  const { UtilitiesTab } = await import('../src/components/left/tabs/servicesTabs.tsx');

  const html = renderToString(
    React.default.createElement(SimProvider, { children: React.default.createElement(UtilitiesTab) })
  );
  assert.ok(!html.includes('useSim must be used inside SimProvider'));
  assert.ok(html.includes('>Power<'));
  assert.ok(html.includes('>Water<'));
  assert.ok(html.includes('>Waste &amp; Recycling<'));
  // Default sub-tab (Power) content is visible on first render.
  assert.ok(/Use external power cover/.test(html));
});

test('AC-5: Education/Health/Safety domain tabs render coverage grids from serviceCoverageOf', async () => {
  ensureMountWindow();
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { SimProvider } = await import('../src/sim/store.tsx');
  const { EducationTab, HealthTab, SafetyTab } = await import('../src/components/left/tabs/servicesTabs.tsx');

  for (const [Comp, expectLabel] of [
    [EducationTab, 'Nursery'],
    [HealthTab, 'GP clinics'],
    [SafetyTab, 'Fire cover'],
  ] as const) {
    const html = renderToString(
      React.default.createElement(SimProvider, { children: React.default.createElement(Comp) })
    );
    assert.ok(!html.includes('useSim must be used inside SimProvider'));
    assert.ok(html.includes(expectLabel), `${Comp.name} must render the "${expectLabel}" coverage row`);
    assert.ok(!/NaN|Infinity/.test(html));
  }
});
