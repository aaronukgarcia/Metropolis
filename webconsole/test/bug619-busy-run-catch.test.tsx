// bug619-busy-run-catch.test.tsx — BUG-619 (found in the BUG-614 round):
// Busy.run()'s `void (async () => { try { await fn(); } finally {...} })()`
// had no catch — a throwing/rejecting fn() escaped the IIFE as a genuine
// unhandled promise rejection, which node:test (and browsers) treat as a
// process-level failure event.
//
// The fix adds a `catch` between the `try` and `finally`: the error is
// normalized (normalizeThrowable, same helper backend.ts's own
// window/unhandledrejection handlers use) and reported via recordError
// (GR#1/GR#7 registry-sourced trapping — a NAMED code on the thrown value
// wins via codedError, otherwise none is forced). The `finally` block —
// and therefore the BUG-604 chip-timer-cancel + BUG-614 refcount/linger
// semantics — is untouched and must still run exactly as before whether
// fn() succeeds, throws synchronously, or rejects.
//
// RED PROOF (documented, not re-run here — GR#24 forbids destructive git):
// scratch-copy Busy.tsx, delete the added `catch (e) { ... }` block (leaving
// bare `try { await fn(); } finally { ... }`), and the two "no unhandled
// rejection" tests below go red via node's `process.on('unhandledRejection')`
// listener firing.
//
// Uses node:test's built-in fake timers to drive the 30ms defer
// deterministically, and a temporary process-level unhandledRejection
// listener (removed in `finally`, per the harness's own pattern for trapping
// this class of event) to prove none escapes.

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

// Captures whether an unhandledRejection fired during the guarded section.
async function withUnhandledRejectionWatch<T>(fn: () => Promise<T>): Promise<{ result: T; sawUnhandled: boolean }> {
  let sawUnhandled = false;
  const onUnhandled = () => {
    sawUnhandled = true;
  };
  process.on('unhandledRejection', onUnhandled);
  try {
    const result = await fn();
    // Give any already-queued rejection notification a chance to fire before
    // we stop watching (Node schedules the event on a later microtask/tick).
    await flushMicrotasks();
    return { result, sawUnhandled };
  } finally {
    process.off('unhandledRejection', onUnhandled);
  }
}

test('BUG-619: fn() rejecting async produces no unhandled promise rejection, and the chip lifecycle still runs (finally)', async () => {
  const dom: any = await installJsdom();
  try {
    const { React, root, container, probe, Probe, BusyProvider, BusyIndicator } = await mountBusyProbe();
    const { act } = await import('react-dom/test-utils');
    const { recentErrors } = await import('../src/sim/backend.ts');

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

    const errorsBefore = recentErrors().length;

    mock.timers.enable({ apis: ['setTimeout'] });
    try {
      const { sawUnhandled } = await withUnhandledRejectionWatch(async () => {
        let rejectFn: (e: unknown) => void = () => {};
        const rejectingPromise = new Promise<void>((_res, rej) => {
          rejectFn = rej;
        });
        // Swallow the promise's own default unhandled-rejection classification
        // isn't needed here — Busy.run consumes it via `await fn()`, so this
        // handle itself is never "unhandled"; only Busy.run's OWN internal
        // await-then-throw path is under test.

        await act(async () => {
          probe.run!(() => rejectingPromise);
        });

        // Cross the 30ms defer — fn() (the rejecting promise) is now awaited
        // inside Busy.run's IIFE.
        await act(async () => {
          mock.timers.tick(30);
        });

        await act(async () => {
          rejectFn(new Error('BUG-619 probe: async rejection'));
          await flushMicrotasks();
        });

        // Chip lifecycle: BUG-604/614 finally-based linger still runs past
        // the rejection — advance past the 60ms linger and confirm the chip
        // resolves back to hidden exactly as a normal fast-settling action
        // would (it never got a chance to show since it settled well inside
        // 250ms).
        await act(async () => {
          mock.timers.tick(60);
        });
      });

      assert.equal(sawUnhandled, false, 'BUG-619: an async-rejecting fn() must not produce a process-level unhandledRejection');
      assert.equal(probe.busy, false, 'the chip must resolve to hidden — the finally-based linger/refcount logic is unaffected by the catch');
      assert.equal(container.querySelector('.busy-chip'), null, 'no .busy-chip left behind after a rejecting action');

      const errorsAfter = recentErrors().length;
      assert.ok(errorsAfter > errorsBefore, 'the rejection must be reported via the registry error trap (recordError), not silently dropped');
    } finally {
      mock.timers.reset();
    }

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});

test('BUG-619: fn() throwing synchronously produces no unhandled promise rejection, and the chip lifecycle still runs (finally)', async () => {
  const dom: any = await installJsdom();
  try {
    const { React, root, container, probe, Probe, BusyProvider, BusyIndicator } = await mountBusyProbe();
    const { act } = await import('react-dom/test-utils');
    const { recentErrors } = await import('../src/sim/backend.ts');

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

    const errorsBefore = recentErrors().length;

    mock.timers.enable({ apis: ['setTimeout'] });
    try {
      const { sawUnhandled } = await withUnhandledRejectionWatch(async () => {
        await act(async () => {
          probe.run!(() => {
            throw new Error('BUG-619 probe: sync throw');
          });
        });

        // Cross the 30ms defer — fn() throws synchronously inside the
        // `await fn()` expression (an `await`-ed throw behaves like a
        // rejection from Busy.run's IIFE's point of view).
        await act(async () => {
          mock.timers.tick(30);
          await flushMicrotasks();
        });

        await act(async () => {
          mock.timers.tick(60);
        });
      });

      assert.equal(sawUnhandled, false, 'BUG-619: a synchronously-throwing fn() must not produce a process-level unhandledRejection');
      assert.equal(probe.busy, false, 'the chip must resolve to hidden after a sync-throwing action');
      assert.equal(container.querySelector('.busy-chip'), null, 'no .busy-chip left behind after a throwing action');

      const errorsAfter = recentErrors().length;
      assert.ok(errorsAfter > errorsBefore, 'the sync throw must be reported via the registry error trap (recordError), not silently dropped');
    } finally {
      mock.timers.reset();
    }

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});
