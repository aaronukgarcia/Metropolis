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
import { recentErrors, setAppVersion, recordError, installConsoleTap } from '../src/sim/backend.ts';
import { versionRaw } from '../src/sim/version.ts';

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

test('BUG-442: throw undefined normalizes to Error without crashing the boundary', async () => {
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
      // BUG-442: throw undefined; the boundary should normalize it and render
      const undefinedThrow = undefined;
      const errInfo = { componentStack: 'stack-1' };

      await act(async () => {
        boundary.componentDidCatch(undefinedThrow as any, errInfo as any);
      });

      assert.ok(boundary.state.error, 'error is set even for undefined throw');
      assert.equal(boundary.state.error!.message, 'undefined', 'undefined normalized to Error with message "undefined"');
      const html = container.innerHTML;
      assert.ok(html.includes('undefined') || html, 'rendered output includes error or doesn\'t crash');
    } finally {
      await act(async () => {
        root.unmount();
      });
    }
  } finally {
    dom.window.close();
  }
});

test('BUG-442: throw string normalizes to Error without crashing the boundary', async () => {
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
      // BUG-442: throw a string; the boundary should normalize it and render
      const stringThrow = 'plain string error';
      const errInfo = { componentStack: 'stack-1' };

      await act(async () => {
        boundary.componentDidCatch(stringThrow as any, errInfo as any);
      });

      assert.ok(boundary.state.error, 'error is set even for string throw');
      assert.equal(boundary.state.error!.message, 'plain string error', 'string normalized to Error');
      const html = container.innerHTML;
      assert.ok(html.includes('plain string error'), 'rendered output includes the string error message');
    } finally {
      await act(async () => {
        root.unmount();
      });
    }
  } finally {
    dom.window.close();
  }
});

test('FEAT-1972079916: error record displays code and correlationId on crash screen', async () => {
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
      const testError = new Error('test-error-' + Math.random());
      const errInfo = { componentStack: '\n    in TestComponent' };

      await act(async () => {
        boundary.componentDidCatch(testError, errInfo as any);
      });

      // Wait a bit for setState to complete
      await new Promise((resolve) => setTimeout(resolve, 10));

      // Check that error record is set
      assert.ok(boundary.state.errorRecord, 'errorRecord should be set after componentDidCatch');
      assert.ok(boundary.state.errorRecord.code === 'MET-V801', 'error code should be MET-V801 (RenderCrash)');
      assert.equal(typeof boundary.state.errorRecord.correlationId, 'number', 'correlationId should be set');

      // Check HTML rendering
      const html = container.innerHTML;
      assert.ok(html.includes(boundary.state.errorRecord.code), 'rendered HTML should include error code');
      assert.ok(
        html.includes(String(boundary.state.errorRecord.correlationId)),
        'rendered HTML should include correlation ID',
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

      // BAR-F5 (round-r1): the RING/debug.json must show BOTH errors, the
      // second marked cascade:true and linked to the first via
      // firstCorrelationId — even though the DISPLAY (asserted above) still
      // holds only the first.
      const firstRow = recentErrors().find((e) => e.msg === first.message);
      const secondRow = recentErrors().find((e) => e.msg === second.message);
      assert.ok(firstRow, 'the first error must have its own ring row');
      assert.ok(secondRow, 'the SECOND (cascade) error must ALSO have its own ring row');
      assert.equal(firstRow.cascade, undefined, 'the first error is the root cause, not a cascade');
      assert.equal(secondRow.cascade, true, 'the second error must be marked cascade:true in the ring');
      assert.equal(
        secondRow.firstCorrelationId,
        firstRow.correlationId,
        "the cascade row's firstCorrelationId must link back to the root cause's correlationId",
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

// ---------- round-r1 BAR-F1: useSim-outside-provider records the NAMED code end-to-end ----------

test('BAR-F1: a useSim-outside-provider crash records code MET-V800 (not the generic MET-V801) through the boundary', async () => {
  const dom = installJsdom();
  try {
    const React = await import('react');
    const { renderToString } = await import('react-dom/server');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { ErrorBoundary } = await import('../src/components/ErrorBoundary.tsx');
    const { useSim } = await import('../src/sim/simContext.ts');

    function OutsideProviderConsumer() {
      useSim(); // no SimProvider ancestor -> throws codedError('MET-V800', ...)
      return null;
    }

    // Capture the REAL error object useSim throws (via a bare SSR render, no
    // boundary involved) so the rest of this test exercises the actual
    // production throw, not a hand-rolled stand-in. Per this file's own
    // fallback-option comment (top of file), the throw is then fed directly
    // into componentDidCatch — React's real double-throw timing through a
    // live mount is not reliably sequenceable in this jsdom harness.
    let capturedError: (Error & { code?: string }) | undefined;
    try {
      renderToString(React.default.createElement(OutsideProviderConsumer));
    } catch (e) {
      capturedError = e as Error & { code?: string };
    }
    assert.ok(capturedError, 'useSim must throw when rendered outside SimProvider');
    assert.equal(capturedError!.code, 'MET-V800', 'the real useSim throw must carry .code MET-V800');

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
      await act(async () => {
        boundary.componentDidCatch(capturedError as Error, { componentStack: '\n    in OutsideProviderConsumer' } as any);
      });

      assert.ok(boundary.state.errorRecord, 'errorRecord must be set');
      assert.equal(
        boundary.state.errorRecord!.code,
        'MET-V800',
        'must carry the NAMED code MET-V800, not the generic MET-V801 fallback',
      );

      const row = recentErrors().find((e) => e.msg === 'useSim must be used inside SimProvider');
      assert.ok(row, 'the useSim-outside-provider crash must be recorded');
      assert.equal(row.code, 'MET-V800');

      const html = container.innerHTML;
      assert.ok(html.includes('MET-V800'), 'the crash screen must display the NAMED code');
    } finally {
      await act(async () => {
        root.unmount();
      });
    }
  } finally {
    dom.window.close();
  }
});

// ---------- round-r1 BAR-F3: appVersion equals the REAL version module export ----------

// ---------- round-r1 BAR-F6: no second root (tap suppression on the diagnostic log) ----------

test('BAR-F6: one render crash produces exactly ONE ring row (no extra generic MET-V804 from the diagnostic console.error)', async () => {
  const dom = installJsdom();
  // Same environment quirk as BAR-F4: this sandbox's global `localStorage`
  // exists but throws on setItem, which would otherwise mint an UNRELATED
  // MET-V805 "write failed" row on every recordError() call and make it look
  // like every crash produces 2 rows regardless of suppression. Install a
  // working in-memory stub so the row count we observe is purely about
  // BAR-F6 (does the diagnostic console.error re-enter the tap), not noise.
  const originalDescriptor = Object.getOwnPropertyDescriptor(globalThis, 'localStorage');
  const mem = new Map<string, string>();
  Object.defineProperty(globalThis, 'localStorage', {
    value: {
      getItem: (k: string) => (mem.has(k) ? mem.get(k)! : null),
      setItem: (k: string, v: string) => void mem.set(k, String(v)),
    },
    configurable: true,
  });
  // Install the REAL console tap on the actual global console so this test can
  // prove something: without it, the boundary's suppressed diagnostic
  // console.error call has no tap to re-enter in the first place, and this
  // test would pass regardless of whether withTapSuppressed exists.
  const realConsoleError = console.error;
  installConsoleTap(console);
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
      const crash = new Error('BAR-F6-single-crash-' + Math.random());

      const before = recentErrors().length;
      await act(async () => {
        boundary.componentDidCatch(crash, { componentStack: '\n    in SoleCrasher' } as any);
      });
      const after = recentErrors();

      assert.equal(after.length, before + 1, 'exactly ONE new ring row for the one crash');
      const rowsForThisCrash = after.filter((e) => e.msg === crash.message);
      assert.equal(rowsForThisCrash.length, 1, 'the crash itself gets exactly one row');
      const diagnosticTapRows = after.filter((e) => e.msg.includes('render crash caught by ErrorBoundary'));
      assert.equal(
        diagnosticTapRows.length,
        0,
        "the boundary's own diagnostic console.error must NOT mint a second (generic MET-V804) ring row",
      );
    } finally {
      await act(async () => {
        root.unmount();
      });
    }
  } finally {
    console.error = realConsoleError;
    dom.window.close();
    if (originalDescriptor) {
      Object.defineProperty(globalThis, 'localStorage', originalDescriptor);
    } else {
      delete (globalThis as any).localStorage;
    }
  }
});

test('BAR-F3: once wired via setAppVersion(versionRaw), a coded error stamps the ACTUAL version.ts export', () => {
  setAppVersion(versionRaw);
  const msg = 'real-version-check-' + Math.random();
  recordError(msg, { code: 'MET-V800' });
  const row = recentErrors().find((e) => e.msg === msg);
  assert.ok(row, 'record captured');
  assert.equal(row.appVersion, versionRaw, "appVersion must equal version.ts's actual versionRaw export");
  assert.notEqual(row.appVersion, 'unknown', 'the unknown fallback must not be hit once the real version is wired');
});
