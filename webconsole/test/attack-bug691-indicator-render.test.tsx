/**
 * INDEPENDENT DESTRUCTIVE ROUND — BUG-691, UI half.
 *
 * Attacker: opus-round-bug691 (NOT the author).
 *
 * The estate's two new tests live in debugtab.test.mjs and only exercise
 * backend.ts. The actual player-visible artefact of BUG-691 — the "⚠ debug
 * sink down" chip in components/left/tabs/debugTab.tsx — had ZERO coverage:
 * deleting the whole JSX block reddened nothing (mutation-proved by this
 * round). These tests close that hole: the chip must appear when the module
 * status says the sink is down, must be ABSENT when it is not, and must not
 * be faked by the ephemeral `status` string.
 */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';

const MOUNTED_TEST_TIMEOUT_MS = 20_000;

function installJsdom() {
  const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', {
    url: 'http://localhost/',
    pretendToBeVisual: true,
  });
  const { window } = dom;
  (globalThis as any).window = window;
  (globalThis as any).document = window.document;
  Object.defineProperty(globalThis, 'navigator', {
    value: window.navigator,
    configurable: true,
    writable: true,
  });
  (globalThis as any).HTMLElement = window.HTMLElement;
  (globalThis as any).requestAnimationFrame = window.requestAnimationFrame.bind(window);
  (globalThis as any).cancelAnimationFrame = window.cancelAnimationFrame.bind(window);
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  return dom;
}

/** Render the LEFT-dock DebugTab (the file BUG-691 actually changed) to a
 * string inside the same provider nesting App.tsx uses. */
async function renderDebugTab() {
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { SimProvider } = await import('../src/sim/store.tsx');
  const { BusyProvider } = await import('../src/components/Busy.tsx');
  const { DebugTab } = await import('../src/components/left/tabs/debugTab.tsx');
  return renderToString(
    React.default.createElement(BusyProvider, {
      children: React.default.createElement(SimProvider, {
        children: React.default.createElement(DebugTab),
      }),
    }),
  );
}

/** Install a fetch that always fails so commitDebug takes the sink-down path. */
function installDeadSink() {
  const original = Object.getOwnPropertyDescriptor(globalThis, 'fetch');
  Object.defineProperty(globalThis, 'fetch', {
    value: async () => ({ ok: false, status: 503 }),
    configurable: true,
    writable: true,
  });
  return () => {
    if (original) Object.defineProperty(globalThis, 'fetch', original);
    else delete (globalThis as any).fetch;
  };
}

test(
  'ATTACK BUG-691: a cold DebugTab (no commit attempted yet) shows NO sink-down chip — the indicator must not cry wolf',
  { timeout: MOUNTED_TEST_TIMEOUT_MS },
  async () => {
    const dom = installJsdom();
    try {
      const { debugSinkStatus } = await import('../src/sim/backend.ts');
      assert.equal(debugSinkStatus().unreachable, false, 'precondition: module-init state is reachable');
      const html = await renderDebugTab();
      assert.ok(html.length > 0, 'the tab must render');
      assert.ok(!html.includes('useSim must be used inside SimProvider'), 'must render inside the providers');
      assert.equal(/debug sink down/.test(html), false, 'no chip before any commit attempt');
    } finally {
      dom.window.close();
    }
  },
);

test(
  'ATTACK BUG-691: after a failed commit, a FRESHLY MOUNTED DebugTab renders the persistent "debug sink down" chip (survives remount)',
  { timeout: MOUNTED_TEST_TIMEOUT_MS },
  async () => {
    const dom = installJsdom();
    const restoreFetch = installDeadSink();
    try {
      const { commitDebug, debugSinkStatus } = await import('../src/sim/backend.ts');
      await commitDebug({ attackBug691: 'indicator-render' });
      assert.equal(debugSinkStatus().unreachable, true, 'precondition: the sink is recorded as down');

      // A brand-new mount — no component state carried over from the commit.
      // This is the exact scenario the fix claims to cover ("a tab that was
      // closed while the sink went down still shows the true status").
      const html = await renderDebugTab();
      assert.ok(
        /debug sink down/.test(html),
        'the persistent sink-down chip must render on a fresh mount seeded from debugSinkStatus()',
      );
      // And it must be the WARNING chip, not merely the word appearing in the
      // error-ring list below (that row says "Debug sink unreachable").
      assert.ok(
        /⚠\s*(<!--\s*-->)?\s*debug sink down/.test(html) || /debug sink down/.test(html.replace(/<!--\s*-->/g, '')),
        'the chip carries its warning glyph',
      );
      assert.ok(
        /title="[^"]*did not respond[^"]*"/.test(html),
        'the chip carries an explanatory title attribute pointing at the queued-locally behaviour',
      );
    } finally {
      restoreFetch();
      dom.window.close();
    }
  },
);
