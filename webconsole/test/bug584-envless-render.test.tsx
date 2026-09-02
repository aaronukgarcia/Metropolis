// bug584-envless-render.test.tsx — BUG-584: env-less render throws.
//
// TopBar.tsx (three call sites: StartOverButton's Unlock-All gate,
// DevFundsButton, DevFundsLargeButton) and PerfHud.tsx (its top-of-component
// dev-only early return) used the NON-optional `import.meta.env.DEV` idiom.
// That throws "Cannot read properties of undefined (reading 'DEV')" on any
// runtime where `import.meta.env` itself is absent (no Vite define plugin) —
// an SSR-style render, or this very tsx test runner. The rest of the
// codebase (debugTab.tsx, LeftDock.tsx, store.tsx — see mount.test.tsx's own
// BUG-412 comment) already uses the optional-chain idiom `import.meta.env?.DEV`
// for exactly this reason; TopBar/PerfHud were the two files that still had
// the unguarded form.
//
// This test file runs under `tsx --test` (never through Vite), so
// `import.meta.env` is genuinely undefined here — the same honest
// precondition pattern as hud-inc2-d1-d2-fixes.test.tsx's D1 proof. No
// forcing/mocking: this IS an env-less runtime, exactly like the class of
// runtime BUG-584 named (SSR-style render / the tsx test runner).
//
// RED-PROOF (recorded, not left in the tree — GR#24 scratch cp/mv, never
// git): reverting either `import.meta.env?.DEV` back to the non-optional
// `import.meta.env.DEV` in TopBar.tsx (any of the three sites) or in
// PerfHud.tsx reproduces the throw and turns the corresponding assertion
// below red ("Cannot read properties of undefined (reading 'DEV')" surfaces
// as an uncaught exception during renderToString, which node:test reports
// as a failed/uncaught test). Confirmed for all four sites individually,
// then restored.

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

test('PRECONDITION: this test runtime genuinely has no import.meta.env (tsx --test, not Vite)', () => {
  assert.ok(
    !(import.meta as any).env?.DEV,
    'PRECONDITION: this test run must genuinely lack a DEV flag for the render assertions below to mean anything'
  );
});

test('BUG-584: TopBar renders under an env-less runtime without throwing', async () => {
  ensureMountWindow();

  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { SimProvider } = await import('../src/sim/store.tsx');
  const { BusyProvider } = await import('../src/components/Busy.tsx');
  const { TopBar } = await import('../src/components/TopBar.tsx');

  const html = renderToString(
    React.default.createElement(BusyProvider, {
      children: React.default.createElement(SimProvider, { children: React.default.createElement(TopBar) }),
    })
  );

  assert.ok(!html.includes('useSim must be used inside SimProvider'));
  assert.ok(!html.includes('useBusy must be used inside BusyProvider'));
  assert.ok(html.length > 0, 'rendered HTML must be non-empty');
});

test('BUG-584: StartOverButton (Unlock-All gate + DevFundsButton + DevFundsLargeButton) renders under an env-less runtime without throwing, DEV-gated buttons absent', async () => {
  ensureMountWindow();

  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { SimProvider } = await import('../src/sim/store.tsx');
  const { BusyProvider } = await import('../src/components/Busy.tsx');
  const { StartOverButton } = await import('../src/components/TopBar.tsx');

  // The whole point: this must NOT throw. Before the fix, the non-optional
  // `import.meta.env.DEV` reads inside the Unlock-All gate, DevFundsButton,
  // and DevFundsLargeButton (all reached via StartOverButton, App.tsx's
  // actual mount site for this cluster) threw during render.
  const html = renderToString(
    React.default.createElement(BusyProvider, {
      children: React.default.createElement(SimProvider, { children: React.default.createElement(StartOverButton) }),
    })
  );

  assert.ok(!html.includes('useSim must be used inside SimProvider'));
  assert.ok(!html.includes('useBusy must be used inside BusyProvider'));
  assert.ok(html.length > 0, 'rendered HTML must be non-empty');
  // DEV-gated bits must be absent — DEV is undefined/falsy in this runtime.
  assert.ok(!html.includes('Unlock All'), 'Unlock-All god-mode button must not render with DEV falsy');
  assert.ok(!/\+£10m/.test(html), 'DevFundsButton must not render with DEV falsy');
  assert.ok(!/\+£1T/.test(html), 'DevFundsLargeButton must not render with DEV falsy');
});

test('BUG-584: PerfHud renders (as null) under an env-less runtime without throwing', async () => {
  ensureMountWindow();

  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { PerfHud } = await import('../src/components/PerfHud.tsx');

  // The whole point: this must NOT throw either.
  const html = renderToString(React.default.createElement(PerfHud));
  assert.equal(html, '', 'PerfHud must render nothing (dev-gated early return) with DEV falsy, not throw');
});
