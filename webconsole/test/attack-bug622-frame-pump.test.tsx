// attack-bug622-frame-pump.test.tsx — BUG-622 (P1) regression: MapView used to
// carry a 20fps (`setInterval(..., 50)`) `frame` repaint-pump whose sole
// purpose (per its own removed comment) was forcing the draw effect's
// dependency array to change every 50ms, unconditionally re-running the ENTIRE
// draw effect body — every building, every overlay full-array scan — even
// when nothing on screen had changed (paused game, no camera move, no tick).
//
// Investigation (BUG-622 profiling) proved `frame`'s numeric value drove ZERO
// pixels: every genuinely animated element (the disconnected-road flash, live
// train positions) is a pure function of `state.tick`, never of `frame`. The
// pump was pure waste, and at scale (13k buildings) it multiplied an already-
// expensive per-building draw loop by 20x/sec regardless of sim activity —
// see tmp-profile/drawloop-bench.mjs for the ~13.9s/frame measurement at
// Aaron's reported scale (a SEPARATE data.ts-side O(n^2) finding, reported to
// the wage lane for a residentsCapacity()/totalJobs() memoisation fix; this
// test only proves the 20fps FORCING mechanism itself is gone).
//
// This test proves the canvas draw effect no longer re-runs on a wall-clock
// heartbeat when no real dependency changed: it counts Canvas2D calls via a
// stub context, waits several real 50ms periods with the sim paused and the
// camera untouched, and asserts the count does NOT grow.
//
// RED PROOF (documented, not re-run — GR#24 forbids destructive git): a
// scratch copy of MapView.tsx with the `setInterval(() => setFrame((f) => f +
// 1), 50)` effect and the `frame` dependency restored turns this test red —
// the ctx-call count keeps climbing every ~50ms with zero state change, which
// is exactly the BUG-622 regression this test exists to catch.

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
  Object.defineProperty(globalThis, 'navigator', { value: window.navigator, configurable: true, writable: true });
  (globalThis as any).HTMLElement = window.HTMLElement;
  (globalThis as any).requestAnimationFrame = window.requestAnimationFrame.bind(window);
  (globalThis as any).cancelAnimationFrame = window.cancelAnimationFrame.bind(window);
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  return dom;
}

test('BUG-622: MapView draw effect does NOT re-run on a wall-clock heartbeat when no real dependency changed', async () => {
  const dom = installJsdom();
  try {
    let ctxCallCount = 0;
    const noop = () => {
      ctxCallCount++;
    };
    const stubCtx = {
      setTransform: noop,
      clearRect: noop,
      beginPath: noop,
      moveTo: noop,
      lineTo: noop,
      stroke: noop,
      fillRect: noop,
      strokeRect: noop,
      fillText: noop,
      fill: noop,
      arc: noop,
      rect: noop,
      clip: noop,
      setLineDash: noop,
      measureText: (s: string) => {
        ctxCallCount++;
        return { width: s.length * 6 };
      },
      save: noop,
      restore: noop,
      translate: noop,
      scale: noop,
      rotate: noop,
      closePath: noop,
      strokeStyle: '',
      fillStyle: '',
      lineWidth: 1,
      globalAlpha: 1,
      font: '',
      textAlign: 'start',
      textBaseline: 'alphabetic',
    };
    (dom.window as any).HTMLCanvasElement.prototype.getContext = function () {
      return stubCtx;
    };

    class StubResizeObserver {
      cb: (entries: unknown[]) => void;
      constructor(cb: (entries: unknown[]) => void) {
        this.cb = cb;
      }
      observe(el: unknown) {
        this.cb([{ target: el, contentRect: { width: 1200, height: 800 } }]);
      }
      unobserve() {}
      disconnect() {}
    }
    (globalThis as any).ResizeObserver = StubResizeObserver;
    (dom.window as any).ResizeObserver = StubResizeObserver;

    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimProvider } = await import('../src/sim/store.tsx');
    const { BusyProvider } = await import('../src/components/Busy.tsx');
    const { OverlayManagerProvider } = await import('../src/components/overlayManager.tsx');
    const { MapView } = await import('../src/components/MapView.tsx');

    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);

    await act(async () => {
      root.render(
        React.default.createElement(
          OverlayManagerProvider,
          null,
          React.default.createElement(
            BusyProvider,
            null,
            React.default.createElement(SimProvider, { children: React.default.createElement(MapView) })
          )
        )
      );
    });

    // First mount does a real draw — record where the count lands.
    const afterMount = ctxCallCount;
    assert.ok(afterMount > 0, 'precondition: MapView must actually draw at least once on mount');

    // Wait several real 50ms periods (the OLD pump's exact interval) with NO
    // dispatch, NO camera move, NO tick — game paused, player idle. If the
    // 20fps repaint pump were still present, the draw effect would re-run
    // repeatedly here and ctxCallCount would keep climbing.
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 350));
    });

    const afterIdleWait = ctxCallCount;
    assert.equal(
      afterIdleWait,
      afterMount,
      `ctx call count grew from ${afterMount} to ${afterIdleWait} during a 350ms idle wait with no state change — ` +
        `the 20fps frame repaint-pump regression (BUG-622) is back: the draw effect is re-running on a wall-clock ` +
        `heartbeat instead of only on real dependency changes`
    );

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});
