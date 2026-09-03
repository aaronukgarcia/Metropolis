// bug615-queue-depth-hud-flag-flip.test.tsx — BUG-615: the worker line's 1s
// poll used to capture `workerOn` at effect-creation (a value read once from
// `webWorkerOffloadEnabled()` during render, then baked into the `useEffect`
// dependency array), so toggling the metropolis.webworker localStorage flag
// while QueueDepthHud stayed mounted never changed the rendered line until an
// UNRELATED re-render recreated the effect with a fresh value. A genuinely
// idle session (no protocol traffic) would show the stale line indefinitely
// after a flag flip.
//
// The fix reads webWorkerOffloadEnabled() fresh INSIDE every poll tick
// (readOnce()) instead of once at effect-creation, and the effect's
// dependency array is now `[]` (it never needs to be recreated to see a new
// flag value).
//
// RED PROOF (documented, not re-run here — GR#24 forbids destructive git):
// scratch-copy QueueDepthHud.tsx, revert the `const workerOn = ...` line back
// to the top-level `const workerOn = webWorkerOffloadEnabled();` (evaluated
// once per render) with the effect depending on `[workerOn]`, and the test
// below goes red: flipping the flag between two 1000ms fake-timer ticks with
// no other re-render never changes the worker line's text.
//
// Uses node:test's built-in fake timers (Node 20.4+) to drive the 1Hz poll
// deterministically without real wall-clock waits.

import { test, mock } from 'node:test';
import assert from 'node:assert/strict';

function installJsdom() {
  return import('jsdom').then(({ JSDOM }) => {
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
    if (typeof (globalThis as any).ResizeObserver === 'undefined') {
      (globalThis as any).ResizeObserver = class {
        observe() {}
        unobserve() {}
        disconnect() {}
      };
    }
    // Stub a Worker constructor so webWorkerOffloadEnabled() can read
    // "enabled" under jsdom (mirrors bug605's test setup).
    if (typeof (globalThis as any).Worker === 'undefined') {
      (globalThis as any).Worker = class {
        postMessage() {}
        terminate() {}
        addEventListener() {}
        removeEventListener() {}
      };
    }
    return dom;
  });
}

test('BUG-615: flipping the webworker flag mid-session updates the worker line within one poll tick, with no other re-render', async () => {
  const dom: any = await installJsdom();
  try {
    // Start with the flag OFF (default).
    dom.window.localStorage.removeItem('metropolis.webworker');

    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { QueueDepthHud } = await import('../src/components/right/QueueDepthHud.tsx');
    const { queueDepthTracker } = await import('../src/sim/queueDepth.ts');
    const { getGlobalWorkerQueueTracker } = await import('../src/sim/workerQueueDepth.ts');

    queueDepthTracker.resetAll();
    const workerTracker = getGlobalWorkerQueueTracker();
    workerTracker.reset();
    workerTracker.enqueue();
    workerTracker.enqueue();

    mock.timers.enable({ apis: ['setInterval', 'setTimeout'] });
    try {
      const container = dom.window.document.getElementById('root');
      const root = createRoot(container);
      await act(async () => {
        root.render(React.default.createElement(QueueDepthHud));
      });

      const hud = container.querySelector('.queue-depth-hud');
      const workerLine = () => hud!.querySelector('[data-testid="qd-worker-line"]')!.textContent || '';

      // Initial mount reads the flag synchronously (readOnce() runs once
      // immediately) — flag is OFF, so the honest "worker off" line shows.
      assert.match(workerLine(), /worker off/i, 'flag OFF at mount must render the "worker off" line');

      // Flip the flag ON while the component stays mounted — NO re-render,
      // NO unmount/remount, just the localStorage write.
      dom.window.localStorage.setItem('metropolis.webworker', '1');

      // Advance exactly one 1s poll tick — no other stimulus.
      await act(async () => {
        mock.timers.tick(1000);
      });

      assert.match(
        workerLine(),
        /2 pending/,
        'BUG-615: the very next 1Hz poll tick after the flag flip must read the flag fresh and switch to the real worker backlog line, with no unrelated re-render required',
      );

      // Flip back OFF and confirm the reverse direction also updates within
      // one tick.
      dom.window.localStorage.removeItem('metropolis.webworker');
      await act(async () => {
        mock.timers.tick(1000);
      });
      assert.match(
        workerLine(),
        /worker off/i,
        'flipping the flag back OFF must also be picked up on the very next poll tick',
      );

      await act(async () => {
        root.unmount();
      });
    } finally {
      mock.timers.reset();
    }
    queueDepthTracker.resetAll();
    workerTracker.reset();
  } finally {
    dom.window.close();
  }
});
