// engine-lag-store-integration.test.tsx — BUG-618 (P1), F3 fix (independent
// round REJECT, 2026-09-03): "wiring untested — the built-but-not-wired
// class". The unit tests in engineLag.test.mjs exercise the tracker in
// total isolation — they would stay green even if store.tsx's calls into
// engineLagTracker were silently deleted (e.g. a future refactor drops
// `recordTickCompleted()` from wrappedDispatch's 'tick' branch). This file
// closes that gap by driving the REAL store.tsx tick-driver end to end —
// a real SimProvider mount, a real setInterval-driven tick loop, real
// dispatched ticks through the real reducer — and asserting the tracker
// actually advanced. If any of the store.tsx instrumentation call sites
// (recordTickScheduled in the interval, recordTickCompleted in
// wrappedDispatch's 'tick' branch) were ever deleted, this test goes red
// where the pure unit tests could not.
//
// Mount idiom mirrors store-dispatch.test.tsx (BAR-1/BAR-2): install jsdom
// globals BEFORE importing React/store, mount SimProvider with
// react-dom/client + act() so effects (the tick-driver interval) actually
// run. No fake-timer library exists in this project's toolchain (plain
// node:test, no sinon/jest) — this uses REAL wall-clock waits against the
// fastest selectable speed (SPEED_MS[3] = 160ms) to keep the test's real
// runtime small while still observing several genuine interval fires.
//
// RED PROOF (documented, not re-run — GR#24 forbids destructive git):
// scratch-copy store.tsx, delete the `engineLagTracker.recordTickCompleted();`
// line from wrappedDispatch's 'tick' branch, and the "completed count
// advanced" assertion below goes red (ticksCompleted stays 0 while
// ticksScheduled keeps climbing — backlog also stops reading 0, a second
// independent signal of the same regression).

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
  // Node's own global `performance` is kept (see store-dispatch.test.tsx's
  // comment on why swapping in jsdom's recurses infinitely).
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  return dom;
}

test('BUG-618 F3: real store dispatch path advances engineLagTracker — completed count and backlog=0 after real interval fires', async () => {
  const dom = installJsdom();
  try {
    // No metropolis.webworker flag set — this exercises the main-thread
    // fallback path (the one Aaron actually plays with, per the brief), the
    // same lockstep scheduled->completed path the F1 pause fix reasons
    // about.
    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimProvider, useSim } = await import('../src/sim/store.tsx');
    const { engineLagTracker } = await import('../src/sim/engineLag.ts');

    engineLagTracker.resetAll();

    function Probe() {
      const { dispatch } = useSim();
      (Probe as any)._lastDispatch = dispatch;
      return null;
    }

    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);

    await act(async () => {
      root.render(React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }));
    });

    const before = engineLagTracker.snapshot(performance.now());
    assert.equal(before.ticksScheduled, 0, 'precondition: a freshly reset tracker starts at 0');
    assert.equal(before.ticksCompleted, 0);

    // Switch to the fastest speed (SPEED_MS[3] = 160ms) so a real wall-clock
    // wait observes several genuine tick-driver interval fires quickly.
    const dispatch = (Probe as any)._lastDispatch as (a: unknown) => void;
    await act(async () => {
      dispatch({ type: 'speed', speed: 3 });
    });

    // Real wall-clock wait for ~5 interval periods. act() flushes the React
    // effects/state updates that each real setInterval fire triggers.
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 900));
    });

    const after = engineLagTracker.snapshot(performance.now());
    assert.ok(
      after.ticksScheduled >= 3,
      `expected several real tick-driver interval fires (>=3) in 900ms at a 160ms interval, got ticksScheduled=${after.ticksScheduled} — the tick-driver's engineLagTracker.recordTickScheduled() call site may be missing`
    );
    assert.ok(
      after.ticksCompleted >= 3,
      `expected the completed count to advance alongside scheduled (>=3), got ticksCompleted=${after.ticksCompleted} — wrappedDispatch's engineLagTracker.recordTickCompleted() call site may be missing (the exact F3 regression class)`
    );
    assert.equal(
      after.ticksCompleted,
      after.ticksScheduled,
      'the main-thread fallback path schedules and completes a tick SYNCHRONOUSLY within the same interval fire — completed must always exactly match scheduled here, never lag behind'
    );
    assert.equal(
      after.backlog,
      0,
      'backlog must have returned to (and stayed at) 0 — a real completed tick immediately following every real scheduled fire is the definition of "caught up"'
    );

    await act(async () => {
      root.unmount();
    });
    engineLagTracker.resetAll();
  } finally {
    dom.window.close();
  }
});

test('BUG-618 F3: dispatching real tick actions through the store advances ticksCompleted by exactly N (deterministic, no real-timer flakiness)', async () => {
  const dom = installJsdom();
  try {
    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimProvider, useSim } = await import('../src/sim/store.tsx');
    const { engineLagTracker } = await import('../src/sim/engineLag.ts');

    engineLagTracker.resetAll();

    function Probe() {
      const { dispatch } = useSim();
      (Probe as any)._lastDispatch = dispatch;
      return null;
    }

    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);

    await act(async () => {
      root.render(React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }));
    });

    const dispatch = (Probe as any)._lastDispatch as (a: unknown) => void;
    const before = engineLagTracker.snapshot(performance.now()).ticksCompleted;

    const N = 5;
    for (let i = 0; i < N; i++) {
      await act(async () => {
        dispatch({ type: 'tick' });
      });
    }

    const after = engineLagTracker.snapshot(performance.now());
    assert.equal(
      after.ticksCompleted - before,
      N,
      `dispatching ${N} real {type:'tick'} actions through the store must advance ticksCompleted by exactly ${N} — a smaller delta means wrappedDispatch's recordTickCompleted() call site is missing or firing conditionally`
    );

    await act(async () => {
      root.unmount();
    });
    engineLagTracker.resetAll();
  } finally {
    dom.window.close();
  }
});
