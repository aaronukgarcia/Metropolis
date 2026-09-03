// Independent attacker variant of BUG-621: recordError throws EVERY time
// (not self-disarming), across 3 consecutive Busy.run() actions.
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
    return dom;
  });
}

async function mountBusyProbe() {
  const React = await import('react');
  const { createRoot } = await import('react-dom/client');
  const { BusyProvider, useBusy, BusyIndicator } = await import('../src/components/Busy.tsx');
  const probe: { run: ((fn: () => void | Promise<void>) => void) | null; busy: boolean } = { run: null, busy: false };
  function Probe() {
    const { run, busy } = useBusy();
    probe.run = run;
    probe.busy = busy;
    return null;
  }
  const container = (globalThis as any).document.getElementById('root');
  const root = createRoot(container);
  return { React, root, container, probe, Probe, BusyProvider, BusyIndicator };
}

async function flushMicrotasks() {
  await Promise.resolve();
  await Promise.resolve();
  await Promise.resolve();
}

async function withUnhandledRejectionWatch<T>(fn: () => Promise<T>): Promise<{ result: T; sawUnhandled: boolean }> {
  let sawUnhandled = false;
  const onUnhandled = () => { sawUnhandled = true; };
  process.on('unhandledRejection', onUnhandled);
  try {
    const result = await fn();
    await flushMicrotasks();
    return { result, sawUnhandled };
  } finally {
    process.off('unhandledRejection', onUnhandled);
  }
}

/** Permanently hostile unshift — throws EVERY call, never disarms. */
function armPermanentUnshiftTrip() {
  const original = Array.prototype.unshift;
  // eslint-disable-next-line no-extend-native
  Array.prototype.unshift = function () {
    throw new Error('ATTACK: permanently hostile Array.prototype.unshift');
  };
  return () => { Array.prototype.unshift = original; };
}

test('ATTACK BUG-621: recordError throws on EVERY call (not self-disarming) across 3 consecutive Busy.run actions', async () => {
  const dom: any = await installJsdom();
  try {
    const { React, root, container, probe, Probe, BusyProvider, BusyIndicator } = await mountBusyProbe();
    const { act } = await import('react-dom/test-utils');

    await act(async () => {
      root.render(
        React.default.createElement(BusyProvider, null,
          React.default.createElement(Probe),
          React.default.createElement(BusyIndicator)
        )
      );
    });

    const consoleErrorSpy = mock.method(console, 'error', () => {});
    const disarm = armPermanentUnshiftTrip();
    mock.timers.enable({ apis: ['setTimeout'] });
    try {
      for (let i = 0; i < 3; i++) {
        let ran = false;
        const { sawUnhandled } = await withUnhandledRejectionWatch(async () => {
          await act(async () => {
            probe.run!(() => {
              ran = true;
              throw new Error(`ATTACK probe iteration ${i}: fn() throws every time`);
            });
          });
          await act(async () => {
            mock.timers.tick(30);
            await flushMicrotasks();
          });
          await act(async () => {
            mock.timers.tick(60);
          });
        });
        assert.equal(ran, true, `iteration ${i}: fn must actually run`);
        assert.equal(sawUnhandled, false, `iteration ${i}: permanently-throwing recordError must never produce an unhandled rejection`);
        assert.equal(probe.busy, false, `iteration ${i}: chip lifecycle must resolve to hidden even with a permanently hostile recordError`);
        assert.equal(container.querySelector('.busy-chip'), null, `iteration ${i}: no leftover chip`);
      }
      assert.ok(consoleErrorSpy.mock.calls.length >= 3, 'each of the 3 swallowed recordError failures must be surfaced via console.error');
    } finally {
      mock.timers.reset();
      disarm();
      consoleErrorSpy.mock.restore();
    }

    await act(async () => { root.unmount(); });
  } finally {
    dom.window.close();
  }
});
