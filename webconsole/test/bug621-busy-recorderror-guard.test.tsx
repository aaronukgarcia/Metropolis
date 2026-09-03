// bug621-busy-recorderror-guard.test.tsx — BUG-621 (found in the independent
// BUG-619 destructive round, worktree agent-a748aa0b2820e81d9): Busy.tsx's
// BUG-619 catch block called recordError(normalized.message, {...}) with no
// try/catch of its own. normalizeThrowable() is explicitly hardened to
// "never throw, even when the thrown value fights back" (BAR-F2) —
// recordError() carried no equivalent contract: its own array bookkeeping
// (errorLog.find/unshift/pop) was unguarded. Because JS async-function
// machinery converts a same-tick synchronous throw during the `try` block
// into a promise rejection, a throwing recordError() call re-escaped past
// the `finally` boundary as an unhandled promise rejection — exactly the
// BUG-619 class, recreated one level deeper.
//
// The BUG-621 filing's own PoC (reverted, scratch-only per GR#24) armed a
// SELF-DISARMING trip on Array.prototype.unshift so recordError()'s
// errorLog.unshift(record) call throws exactly once. This test reproduces
// that exact trigger (not reachable through any normal Busy.run() usage —
// filed as defense-in-depth, not a reproduced production defect) and proves
// the fix: Busy.tsx now wraps the recordError(...) call in its own
// try/catch (swallow-with-console.error), matching normalizeThrowable's
// "never throw back to the caller" discipline.
//
// RED PROOF (documented, not re-run here — GR#24 forbids destructive git):
// scratch-copy Busy.tsx, remove the inner try/catch this test targets
// (leaving the bare `recordError(normalized.message, {...})` call), and this
// test's "no unhandled rejection" assertion goes red via node's
// `process.on('unhandledRejection')` listener firing — reproducing the exact
// BUG-621 filing.

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

  const probe: { run: ((fn: () => void | Promise<void>) => void) | null; busy: boolean } = {
    run: null,
    busy: false,
  };

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
  const onUnhandled = () => {
    sawUnhandled = true;
  };
  process.on('unhandledRejection', onUnhandled);
  try {
    const result = await fn();
    await flushMicrotasks();
    return { result, sawUnhandled };
  } finally {
    process.off('unhandledRejection', onUnhandled);
  }
}

/**
 * Reproduces the BUG-621 filing's exact PoC trigger: a SELF-DISARMING trip
 * on Array.prototype.unshift that throws exactly once (the first call),
 * then permanently restores normal behaviour — so recordError()'s
 * `errorLog.unshift(record)` throws on its next invocation only, and every
 * other unshift call anywhere else (React internals included) is unaffected
 * beyond that single trip. Always restored via the returned disarm() in a
 * `finally`, even if the trip never fires.
 */
function armSelfDisarmingUnshiftTrip(): () => void {
  const original = Array.prototype.unshift;
  let armed = true;
  // eslint-disable-next-line no-extend-native
  Array.prototype.unshift = function (this: unknown[], ...items: unknown[]) {
    if (armed) {
      armed = false;
      Array.prototype.unshift = original; // disarm BEFORE throwing — exactly one trip, ever
      throw new Error('BUG-621 probe: hostile Array.prototype.unshift (prototype pollution)');
    }
    return original.apply(this, items);
  };
  return () => {
    Array.prototype.unshift = original;
  };
}

test('BUG-621: recordError() throwing inside Busy.run\'s catch produces no unhandled promise rejection, and the chip lifecycle still runs (finally)', async () => {
  const dom: any = await installJsdom();
  try {
    const { React, root, container, probe, Probe, BusyProvider, BusyIndicator } = await mountBusyProbe();
    const { act } = await import('react-dom/test-utils');

    await act(async () => {
      root.render(
        React.default.createElement(
          BusyProvider,
          null,
          React.default.createElement(Probe),
          React.default.createElement(BusyIndicator)
        )
      );
    });

    const consoleErrorSpy = mock.method(console, 'error', () => {});
    const disarm = armSelfDisarmingUnshiftTrip();

    mock.timers.enable({ apis: ['setTimeout'] });
    try {
      const { sawUnhandled } = await withUnhandledRejectionWatch(async () => {
        await act(async () => {
          probe.run!(() => {
            throw new Error('BUG-621 probe: fn() throws, driving Busy.run into its catch -> recordError()');
          });
        });

        // Cross the 30ms defer — fn() throws synchronously inside the
        // `await fn()` expression, landing in Busy.run's catch block, which
        // calls recordError() — the hostile unshift trip fires exactly there.
        await act(async () => {
          mock.timers.tick(30);
          await flushMicrotasks();
        });

        // Chip lifecycle: the finally-based linger must still run even though
        // the catch block's OWN recordError() call itself threw.
        await act(async () => {
          mock.timers.tick(60);
        });
      });

      assert.equal(
        sawUnhandled,
        false,
        'BUG-621: a throwing recordError() inside Busy.run\'s catch must not produce a process-level unhandledRejection'
      );
      assert.equal(probe.busy, false, 'the chip must resolve to hidden — the finally-based linger/refcount logic is unaffected');
      assert.equal(container.querySelector('.busy-chip'), null, 'no .busy-chip left behind after the hostile recordError()');
      assert.ok(
        consoleErrorSpy.mock.calls.length >= 1,
        'the swallowed recordError() failure must still be surfaced via console.error, not silently dropped'
      );
    } finally {
      mock.timers.reset();
      disarm();
      consoleErrorSpy.mock.restore();
    }

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});

test('BUG-621: a second Busy.run() action after the hostile recordError() trip still completes normally (no lingering corruption)', async () => {
  const dom: any = await installJsdom();
  try {
    const { React, root, container, probe, Probe, BusyProvider, BusyIndicator } = await mountBusyProbe();
    const { act } = await import('react-dom/test-utils');

    await act(async () => {
      root.render(
        React.default.createElement(
          BusyProvider,
          null,
          React.default.createElement(Probe),
          React.default.createElement(BusyIndicator)
        )
      );
    });

    const consoleErrorSpy = mock.method(console, 'error', () => {});
    const disarm = armSelfDisarmingUnshiftTrip();

    mock.timers.enable({ apis: ['setTimeout'] });
    try {
      await withUnhandledRejectionWatch(async () => {
        await act(async () => {
          probe.run!(() => {
            throw new Error('BUG-621 probe: first action, trips the hostile unshift once');
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

      // The trip is self-disarming (armed=false after firing once), so
      // Array.prototype.unshift is back to normal by now — a follow-up
      // action's own recordError() call must succeed and the chip lifecycle
      // must be completely unaffected by the earlier swallowed failure.
      let ranSecond = false;
      const { sawUnhandled: sawUnhandledSecond } = await withUnhandledRejectionWatch(async () => {
        await act(async () => {
          probe.run!(() => {
            ranSecond = true;
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

      assert.equal(ranSecond, true, 'the second action must actually run');
      assert.equal(sawUnhandledSecond, false, 'the second (clean) action must not produce an unhandled rejection either');
      assert.equal(probe.busy, false, 'the chip must resolve to hidden after the second action');
      assert.equal(container.querySelector('.busy-chip'), null, 'no .busy-chip left behind after the second action');
    } finally {
      mock.timers.reset();
      disarm();
      consoleErrorSpy.mock.restore();
    }

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});
