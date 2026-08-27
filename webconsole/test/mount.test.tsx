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
