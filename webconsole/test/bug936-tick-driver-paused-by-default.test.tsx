// bug936-tick-driver-paused-by-default.test.tsx — BUG-936 (P2, test-harness)
// structural pin.
//
// 33 webconsole/test/*.tsx files used to mount the real SimProvider against
// the real 900ms (SPEED_MS[1]) window.setInterval tick driver, because
// initialState() shipped speed: 1 and store.tsx installs the interval
// unconditionally whenever state.speed !== 0. Any render whose commit
// crossed the real 900ms boundary got an unrequested tick INSIDE act() — an
// extra draw and a mutated SimState landing mid-assertion
// (feat-2326609772-overlay-inc3.test.tsx's CI failures, fixed locally with
// speed: 0 before this fix generalised it).
//
// r1 of this fix put the seam in engine.ts's rawState() (initialState().speed
// defaulted to 0 under NODE_TEST_CONTEXT). The lead bounced that: it made the
// reducer's pure initial state environment-dependent (GR#21 smell) and
// silently changed initialState().speed for every .mjs test and emitted
// fixture (converge-fixture-emit, save-codec fidelity, replay determinism
// hashes) that never mounts anything.
//
// r2 (this version): initialState() stays PURE — speed is always 1, in every
// context. The seam moves to the DRIVER instead — store.tsx's tick-loop
// effect (the one that calls window.setInterval) skips arming altogether
// under NODE_TEST_CONTEXT unless a test has explicitly opted in via the
// documented test-only global `globalThis.__METRO_TEST_TICK_DRIVER__ = true`,
// set at any point before the mount that should get a real interval. Node's
// NODE_TEST_CONTEXT is set automatically for every file `node --test` runs
// and is never present in a production build (no `process` global in the
// browser), so production behaviour is completely unchanged.
//
// This test proves both halves: (1) initialState().speed is still exactly 1
// under the test harness (the r1 shape — speed 0 baked into the reducer —
// must never come back), and (2) despite that, a bare SimProvider mount with
// ZERO overrides and no opt-in installs NO tick-driver interval at any of the
// selectable delays (SPEED_MS[1..3] = 900/420/160ms — engine.ts's SPEED_MS
// table). Deliberately spy-based, no wall-clock wait: GR#28/verification-
// standards forbid depending on real time in a CI gate, and a spy proves the
// interval was never ARMED at all, which is strictly stronger than "no tick
// observed within some finite wait".
//
// RED PROOF (documented, not re-run here — GR#24 forbids destructive git):
// scratch-copy store.tsx's tick-loop effect back to unconditional arming
// (drop the NODE_TEST_CONTEXT/opt-in guard), and the first test below goes
// red — window.setInterval gets called once at delay 900 (SPEED_MS[1]) on a
// bare mount, exactly the regression this pin exists to catch.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';

const SPEED_MS_NONZERO = [900, 420, 160]; // engine.ts's SPEED_MS[1..3] — SPEED_MS[0] is 0 (paused).

function installJsdom() {
  const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', {
    url: 'http://localhost/',
    pretendToBeVisual: true,
  });
  const { window } = dom;
  (globalThis as any).window = window;
  (globalThis as any).document = window.document;
  Object.defineProperty(globalThis, 'navigator', { value: window.navigator, configurable: true, writable: true });
  (globalThis as any).HTMLElement = window.HTMLElement;
  (globalThis as any).requestAnimationFrame = window.requestAnimationFrame.bind(window);
  (globalThis as any).cancelAnimationFrame = window.cancelAnimationFrame.bind(window);
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  return dom;
}

test('BUG-936: initialState().speed stays 1 (pure, environment-independent) even under NODE_TEST_CONTEXT, and mounting SimProvider with the default (no overrides) test fixture still installs NO tick-driver interval', async () => {
  const dom = installJsdom();
  try {
    // Spy BEFORE import — store.tsx's tick-loop effect runs on mount, inside
    // the very first act() below, so the spy must already be in place.
    const g = dom.window as unknown as { setInterval: typeof setInterval };
    const realSetInterval = g.setInterval.bind(dom.window);
    const calls: number[] = [];
    (g as any).setInterval = ((...args: Parameters<typeof setInterval>) => {
      calls.push(args[1] as number);
      return realSetInterval(...(args as [any, any]));
    }) as typeof setInterval;

    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { initialState } = await import('../src/sim/engine.ts');
    const { SimProvider, useSim } = await import('../src/sim/store.tsx');

    // r2 pin: the reducer's pure initial state is NEVER environment-gated —
    // this must read 1 under NODE_TEST_CONTEXT exactly as it would in a
    // production build. If this assertion goes red, the r1 shape (speed 0
    // baked into rawState()) has come back.
    assert.equal(initialState().speed, 1, 'BUG-936 r2 pin: initialState().speed must be the pure production default (1), never environment-dependent');

    let latestSpeed: number | null = null;
    function Probe() {
      const { state } = useSim();
      latestSpeed = state.speed;
      return null;
    }

    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);
    await act(async () => {
      root.render(React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }));
    });

    assert.equal(latestSpeed, 1, 'BUG-936: the mounted store still carries the pure speed:1 default — the seam is NOT in initialState()');

    const tickDriverCalls = calls.filter((delay) => SPEED_MS_NONZERO.includes(delay));
    assert.equal(
      tickDriverCalls.length,
      0,
      `expected NO tick-driver interval at any of ${SPEED_MS_NONZERO.join('/')}ms on a default mount (speed 1, no opt-in), got ${tickDriverCalls.length} — ` +
        'the shared mount idiom is racing the real clock again (BUG-936 regression)',
    );

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});

test('BUG-936: the documented test-only opt-in global (set BEFORE mount) DOES install the tick-driver interval (the escape hatch still works)', async () => {
  const dom = installJsdom();
  try {
    const g = dom.window as unknown as { setInterval: typeof setInterval };
    const realSetInterval = g.setInterval.bind(dom.window);
    const calls: number[] = [];
    (g as any).setInterval = ((...args: Parameters<typeof setInterval>) => {
      calls.push(args[1] as number);
      return realSetInterval(...(args as [any, any]));
    }) as typeof setInterval;

    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimProvider, useSim } = await import('../src/sim/store.tsx');

    let latestSpeed: number | null = null;
    function Probe() {
      const { state } = useSim();
      latestSpeed = state.speed;
      return null;
    }

    // The opt-in is a global read AT ARM TIME (inside store.tsx's tick-loop
    // effect), so setting it before the initial render is sufficient — no
    // post-mount dispatch is needed, unlike r1's per-test workaround.
    (globalThis as any).__METRO_TEST_TICK_DRIVER__ = true;

    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);
    await act(async () => {
      root.render(React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }));
    });

    assert.equal(latestSpeed, 1, 'precondition: the store still boots at the pure speed:1 default');

    assert.equal(
      calls.filter((d) => d === 900).length,
      1,
      'a test that opts in via the documented global before mount must get a real SPEED_MS[1]=900ms interval on that very first mount',
    );

    await act(async () => {
      root.unmount();
    });
  } finally {
    delete (globalThis as any).__METRO_TEST_TICK_DRIVER__;
    dom.window.close();
  }
});

// ─────────────────────────────────────────────────────────────────────────────
// Independent destructive round (opus-round-bug936, 2026-09-11) — added pins.
//
// R1. ORDERING CONTRACT. The opt-in global is read AT ARM TIME, i.e. inside
// the tick-loop effect, and React runs effects after commit — so "before the
// mount" is not a stylistic preference, it is the contract. Setting the flag
// after `await act(() => root.render(...))` is TOO LATE for that mount: the
// effect has already run and taken the skip branch, and nothing re-reads the
// global until the effect's dependencies (state.speed / wrappedDispatch)
// change. All twelve opt-in sites in this repo set it before render; this pin
// makes the failure mode of getting that order wrong explicit and permanent,
// so a future author who copies the idiom into the wrong place gets a failing
// assertion with a reason rather than a silently un-armed driver.
//
// R2. PRODUCTION INERTNESS. Verified against the real build during the round,
// not asserted here: `npm run build` (exit 0), then the guard in
// dist/assets/index-*.js minifies to
//   `typeof process<"u"&&!!(Hc!=null&&Hc.NODE_TEST_CONTEXT)`
// where `Hc={}` is the empty object literal Vite substitutes for `process.env`
// in a browser bundle. `Hc.NODE_TEST_CONTEXT` is therefore statically
// `undefined`, so the skip branch is unreachable in the browser even if a page
// polyfilled a `process` global. The arm path after the guard is byte-identical
// to HEAD's. This comment is the record; it is not a runtime assertion because
// a unit test cannot see the Vite bundle.
// ─────────────────────────────────────────────────────────────────────────────

test('BUG-936 round pin: the opt-in global is read AT ARM TIME — setting it AFTER the mount act() is too late and arms nothing (the documented contract is "before mount")', async () => {
  const dom = installJsdom();
  try {
    const g = dom.window as unknown as { setInterval: typeof setInterval };
    const realSetInterval = g.setInterval.bind(dom.window);
    const calls: number[] = [];
    (g as any).setInterval = ((...args: Parameters<typeof setInterval>) => {
      calls.push(args[1] as number);
      return realSetInterval(...(args as [any, any]));
    }) as typeof setInterval;

    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimProvider, useSim } = await import('../src/sim/store.tsx');

    function Probe() {
      useSim();
      return null;
    }

    // Deliberately NOT set before the render — this is the wrong order.
    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);
    await act(async () => {
      root.render(React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }));
    });

    // Too late: React ran the tick-loop effect during the act() above, it took
    // the skip branch, and nothing re-reads this global until the effect's own
    // dependencies change.
    (globalThis as any).__METRO_TEST_TICK_DRIVER__ = true;
    await act(async () => {});

    assert.equal(
      calls.filter((d) => SPEED_MS_NONZERO.includes(d)).length,
      0,
      'setting the opt-in global after the mount act() must NOT retroactively arm the tick driver — the contract is to set it BEFORE render',
    );

    await act(async () => {
      root.unmount();
    });
  } finally {
    delete (globalThis as any).__METRO_TEST_TICK_DRIVER__;
    dom.window.close();
  }
});
