// mount.test.tsx — smoke test for React mount/provider correctness
//
// BUG-412: SimProvider boot-restore regression (null-context first render).
// The webconsole test suite is node --test on pure functions and never mounts React,
// so a provider/mount regression is invisible. This test catches the blank-screen
// class by rendering the provider tree and asserting:
//   (a) renderToString does NOT throw "useSim must be used inside SimProvider";
//   (b) the output is a non-empty string containing the consumer's output.
//
// This test ALWAYS EXECUTES its assertions — it never skips (an earlier version
// silently skipped when import.meta.env was absent under tsx, a false-green the
// BUG-412 round caught). store.tsx now reads import.meta.env?.DEV (optional
// chaining), so the real render path runs under a bare Node/tsx runtime.

import { test } from 'node:test';
import assert from 'node:assert/strict';

test('SimProvider mount: renderToString does not throw and produces consumer output', async () => {
  // SSR needs a minimal window (localStorage for boot-restore, performance for ticks).
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

  const React = await import('react');
  const { renderToString } = await import('react-dom/server');

  // No try/skip: if store.tsx cannot be imported or rendered, the test FAILS.
  // That is the whole point — a mount regression must turn this red.
  const { SimProvider, useSim } = await import('../src/sim/store.tsx');

  // Minimal consumer: if SimProvider is broken (null-context first render),
  // useSim throws "useSim must be used inside SimProvider" during render here.
  function MinimalConsumer() {
    const { state } = useSim();
    return React.default.createElement('div', null, `tick: ${state.tick}`);
  }

  const html = renderToString(
    React.default.createElement(SimProvider, {
      children: React.default.createElement(MinimalConsumer),
    })
  );

  assert.equal(typeof html, 'string', 'renderToString must return a string');
  assert.ok(html.length > 0, 'rendered HTML must be non-empty');
  assert.ok(html.includes('tick:'), 'rendered HTML must contain the consumer output');
});

// BUG-421: Aaron debug (5).json captured a 60ms burst of
//   "useSim must be used inside SimProvider" /
//   "render crash: useSim must be used inside SimProvider"
// (window.onerror + ErrorBoundary.componentDidCatch). BUG-412's test only
// mounts MinimalConsumer inside SimProvider, so a sibling of SimProvider that
// called useSim was invisible. App used to render VersionUpgradeToast outside
// SimProvider (same ErrorBoundary). This file always executes — no skip.

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

test('BUG-421 RED-prove: useSim in the VersionUpgradeToast slot (outside SimProvider) throws', async () => {
  ensureMountWindow();
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { SimProvider, useSim } = await import('../src/sim/store.tsx');
  const { ErrorBoundary } = await import('../src/components/ErrorBoundary.tsx');
  const { BusyProvider } = await import('../src/components/Busy.tsx');

  function OutsideToastStandIn() {
    useSim();
    return React.default.createElement('div', null, 'toast-slot');
  }

  // Mirrors pre-fix App.tsx: ErrorBoundary > BusyProvider > SimProvider, with
  // the toast as an ErrorBoundary child OUTSIDE SimProvider. SSR does not
  // swallow via ErrorBoundary — renderToString must throw. If this assertion
  // ever stops throwing, the detector is dead and BUG-421 is invisible again.
  assert.throws(
    () =>
      renderToString(
        React.default.createElement(
          ErrorBoundary,
          null,
          React.default.createElement(BusyProvider, {
            children: React.default.createElement(SimProvider, {
              children: React.default.createElement('div', null, 'inside'),
            }),
          }),
          React.default.createElement(OutsideToastStandIn)
        )
      ),
    (err: unknown) => {
      const msg = err instanceof Error ? err.message : String(err);
      assert.match(msg, /useSim must be used inside SimProvider/);
      return true;
    }
  );
});

test('BUG-421: App tree (ErrorBoundary + VersionUpgradeToast as App does) does not throw useSim', async () => {
  ensureMountWindow();
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { SimProvider, useSim } = await import('../src/sim/store.tsx');
  const { ErrorBoundary } = await import('../src/components/ErrorBoundary.tsx');
  const { BusyProvider } = await import('../src/components/Busy.tsx');
  const { VersionUpgradeToast } = await import('../src/sim/liveVersion.tsx');

  function MinimalConsumer() {
    const { state } = useSim();
    return React.default.createElement('div', null, `tick: ${state.tick}`);
  }

  // Same nesting as App.tsx after the fix: toast is inside SimProvider (so a
  // useSim call there cannot throw) and outside the inner ErrorBoundary (so a
  // sim-tree crash does not unmount it).
  const html = renderToString(
    React.default.createElement(
      ErrorBoundary,
      null,
      React.default.createElement(BusyProvider, {
        children: React.default.createElement(SimProvider, {
          children: [
            React.default.createElement(ErrorBoundary, {
              key: 'sim',
              children: React.default.createElement(MinimalConsumer),
            }),
            React.default.createElement(VersionUpgradeToast, { key: 'toast' }),
          ],
        }),
      })
    )
  );

  assert.equal(typeof html, 'string', 'renderToString must return a string');
  assert.ok(html.length > 0, 'rendered HTML must be non-empty');
  assert.ok(html.includes('tick:'), 'rendered HTML must contain the consumer output');
  assert.ok(
    !html.includes('useSim must be used inside SimProvider'),
    'App tree must not surface a useSim provider error'
  );
});
