// error-boundary.test.tsx — BUG-434 round-r1 BAR-3/BAR-4 acceptance.
//
// BAR-3: the FIRST error caught must be what render() shows; cascade errors (from
// failed cleanup/sibling components after the first crash) are COUNTED
// (state.cascadeCount / state.lastCascadeMessage) but never replace the displayed
// error. Before this fix, getDerivedStateFromError unconditionally returned
// { error }, so the LAST error caught was always the one shown — masking the root
// cause behind whatever cascade failure happened to fire last.
//
// BAR-4: drive two different errors through the boundary and assert the
// held/displayed error is the FIRST one, with cascadeCount reflecting the second.
//
// Per the brief's fallback option, this calls the lifecycle methods
// (componentDidCatch, static getDerivedStateFromError) directly rather than
// depending on a full jsdom double-throw sequence (React's real double-throw timing
// through the render cycle is not reliably sequenceable in a test). The instance is
// still a REAL, MOUNTED component (via react-dom/client in jsdom) so `this.setState`
// inside componentDidCatch is not a no-op — calling it on an unmounted bare instance
// warns and silently drops the update, which would make this test meaningless.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';

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

test('BAR-3/BAR-4: first error caught is held and displayed; second (cascade) error is counted, not displayed', async () => {
  const dom = installJsdom();
  try {
    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { ErrorBoundary } = await import('../src/components/ErrorBoundary.tsx');

    const ref = React.default.createRef<InstanceType<typeof ErrorBoundary>>();
    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);

    try {
      await act(async () => {
        root.render(
          React.default.createElement(ErrorBoundary, { ref, children: React.default.createElement('div', null, 'ok') }),
        );
      });

      const boundary = ref.current!;
      assert.ok(boundary, 'ErrorBoundary instance must be reachable via ref');

      const first = new Error('first crash: root cause');
      const second = new Error('second crash: cascade from cleanup');
      const third = new Error('third crash: another cascade');

      // getDerivedStateFromError itself makes NO state change (see ErrorBoundary.tsx —
      // it cannot see prior state, only the raw error); the class relies on
      // componentDidCatch's functional setState for the first-error-wins decision.
      const derived1 = (ErrorBoundary as any).getDerivedStateFromError(first);
      assert.equal(derived1, null, 'getDerivedStateFromError must not itself decide state (see BAR-3 comment)');

      await act(async () => {
        boundary.componentDidCatch(first, { componentStack: 'stack-1' } as any);
      });

      assert.equal(boundary.state.error, first, 'the FIRST error must be held in state.error');
      assert.equal(boundary.state.cascadeCount, 0, 'no cascade yet after only one error');

      await act(async () => {
        boundary.componentDidCatch(second, { componentStack: 'stack-2' } as any);
      });

      // CRITICAL: the displayed error must STILL be the first one.
      assert.equal(
        boundary.state.error,
        first,
        'BAR-3 CRITICAL: a second (cascade) error must NOT replace the displayed first error',
      );
      assert.equal(boundary.state.error!.message, 'first crash: root cause');
      assert.equal(boundary.state.cascadeCount, 1, 'the second error must be counted as one cascade');
      assert.equal(
        boundary.state.lastCascadeMessage,
        second.message,
        'the cascade message must be recorded for detail display',
      );

      // A third error keeps piling onto the cascade count without disturbing the display.
      await act(async () => {
        boundary.componentDidCatch(third, { componentStack: 'stack-3' } as any);
      });
      assert.equal(boundary.state.error, first, 'the displayed error must remain the first through multiple cascades');
      assert.equal(boundary.state.cascadeCount, 2, 'cascade count must keep incrementing');
      assert.equal(boundary.state.lastCascadeMessage, third.message);

      // Re-render check: the DOM must show the first error's message, the cascade
      // count, and NOT the raw second/third messages as the primary display.
      const html = container.innerHTML;
      assert.ok(html.includes('first crash: root cause'), 'rendered DOM must show the FIRST error message');
      assert.ok(html.includes('2 cascade errors'), 'rendered DOM must surface the cascade count');
      const preMatch = html.match(/<pre[^>]*>([\s\S]*?)<\/pre>/);
      assert.ok(preMatch, 'rendered output must contain a <pre> error block');
      assert.equal(
        preMatch![1],
        'first crash: root cause',
        'the <pre> block (primary display) must be EXACTLY the first error, never a cascade',
      );
    } finally {
      await act(async () => {
        root.unmount();
      });
    }
  } finally {
    dom.window.close();
  }
});
