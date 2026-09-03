// bug604-busy-chip-delay.test.tsx — BUG-604 (Aaron Q100080 = A): the
// "Working…" chip must show ONLY when a wrapped action blocks the UI for
// longer than WORKING_CHIP_DELAY_MS (250ms), never for every action.
//
// Before the fix, Busy.tsx's run() called setBusy(true) immediately (before
// even the existing 30ms React-paint defer), so the chip flashed on for
// EVERY action regardless of how fast it settled — noise per Aaron's
// ruling. The fix gates the chip's visibility behind its own 250ms timer,
// cancelled if the action settles first.
//
// RED PROOF (documented, not re-run here — GR#24 forbids destructive git):
// scratch-copy Busy.tsx, drop the `chipTimer`/`settled` gate back to a bare
// `setBusy(true)` at the top of run() (the pre-fix shape), and the FAST
// test below goes red (busyValue flips true immediately instead of staying
// false through the whole run).
//
// Uses node:test's built-in fake timers (Node 20.4+, stable on this
// project's Node 25) to drive the 30ms defer / 250ms chip-delay / 60ms
// linger deterministically without real wall-clock waits.

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

// Flush pending microtasks (Promise resolutions) without touching macrotask
// (setTimeout) fake-timer state.
async function flushMicrotasks() {
  await Promise.resolve();
  await Promise.resolve();
  await Promise.resolve();
}

test('BUG-604: a fast action (settles before 250ms) never shows the Working… chip', async () => {
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

    mock.timers.enable({ apis: ['setTimeout'] });
    try {
      let resolveFn: () => void = () => {};
      const fastPromise = new Promise<void>((res) => {
        resolveFn = res;
      });

      await act(async () => {
        probe.run!(() => fastPromise);
      });

      // Advance past the existing 30ms React-paint defer — fn() is now running.
      await act(async () => {
        mock.timers.tick(30);
      });
      assert.equal(probe.busy, false, 'chip must not appear immediately when run() is called');

      // The action settles well inside the 250ms chip-delay window.
      await act(async () => {
        resolveFn();
        await flushMicrotasks();
      });
      assert.equal(probe.busy, false, 'chip must not appear once the fast action has settled');

      // Advance PAST the 250ms chip-delay threshold and the 60ms linger — the
      // chip must NEVER have appeared, because its timer was cancelled on
      // settle (this is the core BUG-604 assertion: the fast path is not a
      // "flash-then-hide", it never shows at all).
      await act(async () => {
        mock.timers.tick(250);
      });
      assert.equal(probe.busy, false, 'chip must still be absent after the chip-delay threshold elapses (timer was cancelled)');

      await act(async () => {
        mock.timers.tick(60);
      });
      assert.equal(probe.busy, false, 'chip must remain absent through where the linger would have been');

      const chip = container.querySelector('.busy-chip');
      assert.equal(chip, null, 'no .busy-chip element must ever have been rendered for the fast action');
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

test('BUG-604: a slow action (blocks past 250ms) shows the chip only after the delay, and lingers 60ms after completion', async () => {
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

    mock.timers.enable({ apis: ['setTimeout'] });
    try {
      let resolveFn: () => void = () => {};
      const slowPromise = new Promise<void>((res) => {
        resolveFn = res;
      });

      await act(async () => {
        probe.run!(() => slowPromise);
      });

      // Advance past the 30ms React-paint defer — fn() is now running, chip
      // timer has just been scheduled.
      await act(async () => {
        mock.timers.tick(30);
      });
      assert.equal(probe.busy, false, 'chip must not show immediately, even for a slow action');

      // Just short of the 250ms chip-delay threshold: still hidden.
      await act(async () => {
        mock.timers.tick(249);
      });
      assert.equal(probe.busy, false, 'chip must not show before the 250ms threshold elapses');

      // Cross the threshold: chip must now appear, action still running.
      await act(async () => {
        mock.timers.tick(1);
      });
      assert.equal(probe.busy, true, 'chip must appear once the action has blocked past 250ms');
      assert.ok(container.querySelector('.busy-chip'), '.busy-chip must be rendered once the chip is visible');

      // The action completes now — the chip must LINGER for the existing
      // 60ms, not vanish instantly.
      await act(async () => {
        resolveFn();
        await flushMicrotasks();
      });
      assert.equal(probe.busy, true, 'chip must still be visible immediately after the action completes (60ms linger)');

      await act(async () => {
        mock.timers.tick(59);
      });
      assert.equal(probe.busy, true, 'chip must still be visible just before the 60ms linger elapses');

      await act(async () => {
        mock.timers.tick(1);
      });
      assert.equal(probe.busy, false, 'chip must hide once the 60ms linger has elapsed');
      assert.equal(container.querySelector('.busy-chip'), null, '.busy-chip must be removed once the linger elapses');
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
