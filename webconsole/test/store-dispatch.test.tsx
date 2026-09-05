// store-dispatch.test.tsx — BUG-434 round-r1 BAR-1/BAR-2 acceptance.
//
// BUG-434's fix made wrappedDispatch stable (useMemo deps [tickTracker], reading
// stateRefForDispatch.current instead of `state`). The r1 destructive round REJECTed
// because that stability claim had NO test proving it under a REAL client render with
// effects (SSR renderToString never re-renders, so it cannot see referential churn).
//
// This file mounts SimProvider with react-dom/client + jsdom, so effects actually run
// and re-renders actually happen, and asserts:
//   BAR-1: wrappedDispatch is the SAME function object across dispatches/renders.
//   BAR-2: the tick-loop interval is NOT recreated across a burst of dispatches (the
//          churn mechanism that froze the game at turbo speed in BUG-434).
//
// Both are mutation-proofed: temporarily reintroducing the bug (adding `state` to the
// wrappedDispatch useMemo deps and reading `state` instead of stateRefForDispatch.current
// inside recordAndDispatch) must turn these tests RED. See the scratch-copy procedure
// this test file's author ran (cp store.tsx store.tsx.mutant, edit, run, restore) —
// documented in the round evidence, not re-run automatically here (that would require
// mutating the live tree, which is banned during this bounce).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';

// Install jsdom globals BEFORE importing React/store — react-dom/client's createRoot
// and the effect scheduler probe `window`/`document` at module-eval and mount time.
function installJsdom() {
  const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', {
    url: 'http://localhost/',
    pretendToBeVisual: true, // requestAnimationFrame support
  });
  const { window } = dom;
  (globalThis as any).window = window;
  (globalThis as any).document = window.document;
  // Node 21+ defines a getter-only global `navigator` (its own NodeJS navigator);
  // plain assignment throws "Cannot set property navigator". Redefine it instead.
  Object.defineProperty(globalThis, 'navigator', {
    value: window.navigator,
    configurable: true,
    writable: true,
  });
  (globalThis as any).HTMLElement = window.HTMLElement;
  (globalThis as any).requestAnimationFrame = window.requestAnimationFrame.bind(window);
  (globalThis as any).cancelAnimationFrame = window.cancelAnimationFrame.bind(window);
  // Node already has a native global `performance` (used by the test runner itself);
  // do NOT replace it with jsdom's — the two implementations recurse into each other
  // when swapped in (RangeError: Maximum call stack size exceeded). SimProvider only
  // needs SOME performance.now(), and Node's own global already provides one.
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  // localStorage: jsdom provides one on `window`, but SimProvider reads
  // `window.localStorage` directly, so nothing extra to wire.
  return dom;
}

test('BAR-1: wrappedDispatch is referentially stable across renders and dispatches (real client mount)', async () => {
  const dom = installJsdom();
  try {
    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimProvider, useSim } = await import('../src/sim/store.tsx');

    const seen: unknown[] = [];
    let renderCount = 0;

    function Probe() {
      const { dispatch } = useSim();
      renderCount++;
      seen.push(dispatch);
      return null;
    }

    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);

    await act(async () => {
      root.render(React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }));
    });

    assert.ok(renderCount >= 1, 'Probe must have rendered at least once');
    const first = seen[0];
    assert.ok(typeof first === 'function', 'dispatch must be a function');

    // Dispatch several real actions through the captured dispatch reference and force
    // re-renders in between, capturing the dispatch reference the consumer sees EACH time.
    for (let i = 0; i < 5; i++) {
      await act(async () => {
        (first as (a: unknown) => void)({ type: 'debugFunds', amount: 1_000 });
      });
    }

    assert.ok(seen.length >= 2, 'Probe must have re-rendered after dispatches (state changed)');

    for (let i = 1; i < seen.length; i++) {
      assert.strictEqual(
        seen[i],
        seen[0],
        `wrappedDispatch changed identity at render #${i} — BUG-434 regression: the tick-loop effect ` +
          '(deps [wrappedDispatch]) will tear down/recreate its interval on every state change.',
      );
    }

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});

test('BAR-2: the tick-loop setInterval is not recreated across a burst of dispatches (turbo speed)', async () => {
  const dom = installJsdom();
  try {
    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimProvider, useSim } = await import('../src/sim/store.tsx');

    // Spy on setInterval/clearInterval on the jsdom WINDOW: BUG-721 moved
    // store.tsx's mount-effect intervals (including the tick loop) from the
    // bare global identifiers to `window.setInterval`/`window.clearInterval`
    // (a leaked bare-global timer bound to Node's own timer table instead of
    // the jsdom window, which `dom.window.close()` cannot stop — see
    // TopBar.tsx's EngineLagChip and store.tsx's own BUG-721 comments), so
    // the calls this test needs to observe now land on `dom.window`, not
    // `globalThis`. (Historical note, now inverted: before BUG-721 this spy
    // had to target `globalThis` — spying on `dom.window` back then silently
    // counted zero calls.)
    //
    // Two DIFFERENT effects in store.tsx call setInterval: the tick loop (deps
    // [state.speed, wrappedDispatch], delay = SPEED_MS[state.speed]) and the autosave
    // timer (deps [state, journal, lastSaveIndex] — INTENTIONALLY recreated every
    // dispatch, delay = AUTOSAVE_INTERVAL_MS = 30000). Only the tick loop's churn is the
    // BUG-434 freeze mechanism, so we isolate it by its distinctive delay (900ms at the
    // default speed=1, per SPEED_MS in engine.ts) rather than counting all setInterval
    // calls (which falsely flagged the ALWAYS-churning-by-design autosave timer too).
    const TICK_LOOP_DELAY_MS = 900;
    const g = dom.window as unknown as { setInterval: typeof setInterval; clearInterval: typeof clearInterval };
    let setIntervalCalls = 0;
    let clearIntervalCalls = 0;
    const tickIntervalIds = new Set<unknown>();
    const realSetInterval = g.setInterval.bind(dom.window);
    const realClearInterval = g.clearInterval.bind(dom.window);
    g.setInterval = ((...args: Parameters<typeof setInterval>) => {
      const id = realSetInterval(...(args as [any, any?]));
      if (args[1] === TICK_LOOP_DELAY_MS) {
        setIntervalCalls++;
        tickIntervalIds.add(id);
      }
      return id;
    }) as typeof setInterval;
    g.clearInterval = ((...args: Parameters<typeof clearInterval>) => {
      const id = args[0];
      if (tickIntervalIds.has(id)) {
        clearIntervalCalls++;
        tickIntervalIds.delete(id);
      }
      return realClearInterval(...(args as [any]));
    }) as typeof clearInterval;

    function Probe() {
      const { dispatch } = useSim();
      (Probe as any)._lastDispatch = dispatch;
      return null;
    }

    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);

    try {
      await act(async () => {
        root.render(
          React.default.createElement(SimProvider, {
            children: React.default.createElement(Probe),
          }),
        );
      });

      // Speed starts at 1 (see engine.ts initialState) so the tick-loop effect mounts one
      // interval on initial render. Any ADDITIONAL setInterval call after this point (while
      // speed/wrappedDispatch stay put) is the churn bug reappearing.
      const setIntervalAfterMount = setIntervalCalls;
      assert.ok(setIntervalAfterMount >= 1, 'tick-loop effect must have registered one interval on mount');

      const dispatch = (Probe as any)._lastDispatch as (a: unknown) => void;
      for (let i = 0; i < 50; i++) {
        await act(async () => {
          dispatch({ type: 'debugFunds', amount: 10_000 });
        });
      }

      assert.strictEqual(
        setIntervalCalls,
        setIntervalAfterMount,
        `setInterval was called ${setIntervalCalls - setIntervalAfterMount} extra time(s) during the 50-dispatch burst — ` +
          'the tick-loop effect is re-running (its deps include wrappedDispatch), which is the BUG-434 freeze mechanism.',
      );
      assert.strictEqual(
        clearIntervalCalls,
        0,
        `clearInterval was called ${clearIntervalCalls} time(s) during the burst — the tick-loop interval was torn down, ` +
          'which is exactly the BUG-434 freeze mechanism (constant clear+recreate starves the tick callback).',
      );

      await act(async () => {
        root.unmount();
      });
    } finally {
      g.setInterval = realSetInterval;
      g.clearInterval = realClearInterval;
    }
  } finally {
    dom.window.close();
  }
});
