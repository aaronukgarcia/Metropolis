// bug605-queue-depth-hud-worker-line.test.tsx — BUG-605 second half (Aaron
// Q100081 = C, both queues visible): the always-mounted QueueDepthHud panel
// must show a MEANINGFUL tick-queue line regardless of the
// metropolis.webworker flag state, never a dim/misleading 0/0.
//
// Before the fix, the panel showed only the protocol-asks section (which
// legitimately reads 0/0 whenever no protocol traffic exists — the
// mock/offline default) and said NOTHING about the worker/tick path at all,
// so a viewer had no way to tell "no worker traffic" apart from "nothing is
// wired up".
//
// RED PROOF (documented, not re-run — GR#24 forbids destructive git):
// scratch-copy QueueDepthHud.tsx, delete the `.qd-worker-line` paragraph and
// its workerLine computation, and every assertion below that queries
// `[data-testid="qd-worker-line"]` goes red (element does not exist).

import { test } from 'node:test';
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
    return dom;
  });
}

test('BUG-605: flag OFF renders the honest "worker off" line, never a dim 0/0 for the worker section', async () => {
  const dom: any = await installJsdom();
  try {
    // Flag explicitly off (default): no metropolis.webworker key set.
    dom.window.localStorage.removeItem('metropolis.webworker');

    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { QueueDepthHud } = await import('../src/components/right/QueueDepthHud.tsx');
    const { queueDepthTracker } = await import('../src/sim/queueDepth.ts');

    queueDepthTracker.resetAll();

    const container = dom.window.document.getElementById('root');
    const root = createRoot(container);
    await act(async () => {
      root.render(React.default.createElement(QueueDepthHud));
    });

    const hud = container.querySelector('.queue-depth-hud');
    assert.ok(hud, 'the HUD root element must render');

    const workerLine = hud!.querySelector('[data-testid="qd-worker-line"]');
    assert.ok(workerLine, 'the worker/tick line must always be present, flag on or off');
    assert.match(
      workerLine!.textContent || '',
      /worker off/i,
      'flag OFF (no DEV tick data available under this test\'s import.meta.env) must render the honest "worker off" line, not a numeric placeholder',
    );
    assert.doesNotMatch(
      workerLine!.textContent || '',
      /^0\/0$|^0 \/ 0$/,
      'the worker line must never be a bare dim "0/0"',
    );

    await act(async () => {
      root.unmount();
    });
    queueDepthTracker.resetAll();
  } finally {
    dom.window.close();
  }
});

test('BUG-605: flag ON renders the REAL worker backlog depth from workerQueueDepth.ts, driven by real tracker state (GR#15)', async () => {
  const dom: any = await installJsdom();
  try {
    dom.window.localStorage.setItem('metropolis.webworker', '1');
    // jsdom does provide a global Worker in newer versions; webWorkerOffloadEnabled()
    // also requires `typeof Worker !== 'undefined'`. Stub one if absent so the
    // flag actually reads as enabled under this test's jsdom version.
    if (typeof (globalThis as any).Worker === 'undefined') {
      (globalThis as any).Worker = class {
        postMessage() {}
        terminate() {}
        addEventListener() {}
        removeEventListener() {}
      };
    }

    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { QueueDepthHud } = await import('../src/components/right/QueueDepthHud.tsx');
    const { queueDepthTracker } = await import('../src/sim/queueDepth.ts');
    const { getGlobalWorkerQueueTracker } = await import('../src/sim/workerQueueDepth.ts');
    const { webWorkerOffloadEnabled } = await import('../src/sim/webWorkerFlag.ts');

    assert.equal(webWorkerOffloadEnabled(), true, 'test precondition: the flag must read enabled for this case to be meaningful');

    queueDepthTracker.resetAll();
    const workerTracker = getGlobalWorkerQueueTracker();
    workerTracker.reset();
    workerTracker.enqueue();
    workerTracker.enqueue();
    workerTracker.enqueue();

    const container = dom.window.document.getElementById('root');
    const root = createRoot(container);
    await act(async () => {
      root.render(React.default.createElement(QueueDepthHud));
    });

    const hud = container.querySelector('.queue-depth-hud');
    const workerLine = hud!.querySelector('[data-testid="qd-worker-line"]');
    assert.ok(workerLine, 'the worker/tick line must be present with the flag on');
    assert.match(
      workerLine!.textContent || '',
      /3 pending/,
      'flag ON must reflect the REAL workerQueueDepth.ts tracker depth (3), not a hardcoded or stale value (GR#15)',
    );

    // Drain to 0 and re-render (force a poll) — the line must update.
    workerTracker.drain();
    workerTracker.drain();
    workerTracker.drain();
    await act(async () => {
      // Re-render forces the effect's readOnce() to run again via the poll
      // interval firing at least once; simplest deterministic path here is
      // to unmount/remount so the initial readOnce() picks up fresh state.
      root.unmount();
    });
    const root2 = createRoot(container);
    await act(async () => {
      root2.render(React.default.createElement(QueueDepthHud));
    });
    const workerLine2 = container.querySelector('[data-testid="qd-worker-line"]');
    assert.match(workerLine2!.textContent || '', /0 pending/, 'depth must reflect a real drain back to 0, not stick at the earlier value');

    await act(async () => {
      root2.unmount();
    });
    queueDepthTracker.resetAll();
    workerTracker.reset();
  } finally {
    dom.window.close();
  }
});
