// hud-inc2-d1-d2-fixes.test.tsx — FEAT-2326609720 inc2, D1/D2 fixes from the
// independent round REJECT.
//
// D1 RED PROOF: this test was run against the pre-fix LeftDock.tsx (Debug
// gated via `...(isDev ? [{ id: DEBUG_GROUP_ID, ... }] : [])`), under this
// file's OWN runtime (tsx --test, never Vite, so `import.meta.env.DEV` is
// genuinely undefined here — verified by the PRECONDITION assertion inside
// the test, not "forced": assigning to `import.meta.env` from this module
// would be a per-module ESM no-op and would not reach LeftDock.tsx's own
// module scope) — the "Debug tab renders with DEV undefined" assertion went
// RED (the Debug tab id was simply absent from topTabs, so `>Debug<` never
// appeared). Restored via scratch cp/mv (GR#24), never git.
//
// D2 RED PROOF: run against the pre-fix App.tsx (still importing/mounting
// <RightDock/> inside <div className="col-wrap bottom-col">) and the pre-fix
// styles.css (`grid-template-rows: 48px 1fr 225px` + the "bottom" area row)
// — the "App tree contains no RightDock" and "styles.css declares no bottom
// grid row" assertions both went RED. Restored via scratch cp/mv, never git.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));

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

test('D1: the Debug tab is reachable (its entry renders) with DEV undefined/false — parity with the old RightDock', async () => {
  ensureMountWindow();
  // NIT FIX: assigning to `(import.meta as any).env` in THIS module is a
  // per-module ESM no-op — `import.meta` is a distinct object per module
  // record, so mutating it here has zero effect on what LeftDock.tsx's own
  // module scope reads (there is no shared/forceable global here, unlike
  // `globalThis.window`). The line below does NOT force anything; it is a
  // PRECONDITION assertion that this test's actual runtime genuinely has no
  // DEV flag — this file runs under `tsx --test` (never through Vite), so
  // `import.meta.env` is undefined here exactly as it would be in a real
  // production/dogfood `vite build` (DEV=false). If a future harness change
  // ever ran this file through Vite in dev mode, this assertion would fail
  // loudly instead of silently testing the wrong condition.
  assert.ok(!(import.meta as any).env?.DEV, 'PRECONDITION: this test run must genuinely have no DEV flag (tsx --test, not Vite dev) for the assertion below to mean anything');

  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { SimProvider } = await import('../src/sim/store.tsx');
  const { LeftDock } = await import('../src/components/left/LeftDock.tsx');

  const html = renderToString(
    React.default.createElement(SimProvider, { children: React.default.createElement(LeftDock) })
  );
  assert.ok(!html.includes('useSim must be used inside SimProvider'));
  assert.ok(html.includes('>Debug<'), 'the Debug tab entry must render even with DEV undefined (D1 parity fix)');
});

test('D1: DebugTab body itself still renders (debug.json viewer / commit / download / errors-captured) reachable via the unconditional tab', async () => {
  ensureMountWindow();
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { SimProvider } = await import('../src/sim/store.tsx');
  const { BusyProvider } = await import('../src/components/Busy.tsx');
  const { DebugTab } = await import('../src/components/left/tabs/debugTab.tsx');

  const html = renderToString(
    React.default.createElement(BusyProvider, {
      children: React.default.createElement(SimProvider, { children: React.default.createElement(DebugTab) }),
    })
  );
  assert.ok(!html.includes('useSim must be used inside SimProvider'));
  assert.ok(/Commit snapshot/.test(html), 'Commit snapshot button must be present (GR#1 dogfood pipeline)');
  assert.ok(/Download debug\.json/.test(html), 'Download debug.json button must be present');
  assert.ok(/Errors captured/.test(html), 'the errors-captured MET-code list section must be present');
});

function stripComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '');
}

test('D2: App.tsx no longer imports or mounts RightDock', async () => {
  const src = await fs.readFile(path.resolve(here, '../src/App.tsx'), 'utf-8');
  const code = stripComments(src);
  assert.ok(!/RightDock/.test(code), 'App.tsx code (comments aside) must not reference RightDock at all once the panel is fully retired');
  assert.ok(!/bottom-col/.test(code), 'App.tsx code (comments aside) must not render a "bottom-col" slot for the now-removed RightDock');
});

test('D2: styles.css no longer reserves a "bottom" grid row/area for the retired RightDock', async () => {
  const css = await fs.readFile(path.resolve(here, '../src/styles.css'), 'utf-8');
  const appRuleMatch = css.match(/\.app\s*\{([^}]*)\}/s);
  assert.ok(appRuleMatch, '.app grid rule must exist');
  const appRule = appRuleMatch![1];
  assert.ok(!/"left bottom right"/.test(appRule), '.app must not declare a "bottom" grid-template-area row');
  assert.ok(!/225px/.test(appRule), '.app must not reserve the old 225px bottom-row track');
  assert.ok(!/\.bottom-col\s*\{/.test(css), 'styles.css must not declare a .bottom-col rule any more');
});

test('D2: RightDock.tsx module itself is confirmed inert (returns null) and is no longer imported by App.tsx (belt-and-braces with the source check above)', async () => {
  // A full <App/> SSR mount is NOT used here — App.tsx pulls in MapView/
  // TopBar's dev-cheat buttons which need a real Vite `import.meta.env`
  // (undefined under plain tsx --test, unrelated to this lane's change) and
  // canvas-ish DOM APIs jsdom-less SSR doesn't provide. That gap is
  // pre-existing and out of this fix's scope. Two independent, reliable
  // proofs suffice instead: (a) the source-level absence check above, and
  // (b) RightDock itself still renders nothing when mounted directly.
  ensureMountWindow();
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { RightDock } = await import('../src/components/right/RightDock.tsx');
  const html = renderToString(React.default.createElement(RightDock));
  assert.equal(html, '', 'RightDock must still render nothing even if some other path tried to mount it');
});
