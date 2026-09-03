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


// ---------------------------------------------------------------------------
// BUG-614: overlapping run() calls must not flicker the chip. Each call's own
// 60ms linger used to call setBusy(false) unconditionally, so a call that
// settled EARLY (while a sibling call was still in flight and past its own
// 250ms threshold) would flip the chip off out from under the still-running
// sibling. The fix guards setBusy(false) behind an in-flight refcount
// (activeRef in Busy.tsx): only the LAST outstanding call's linger may hide
// the chip.
//
// Timer-nesting note: node:test's mock.timers.tick(N) only fires timers that
// already existed before the tick() call started — a setTimeout registered
// DURING a tick's callback (e.g. the 250ms chipTimer registered inside the
// 30ms defer's callback) will not fire within that same tick() call even if
// its target falls inside the ticked window. Each of these tests therefore
// ticks past the 30ms defer in its own call before ticking through the
// 250ms chip-delay, exactly mirroring the two pre-existing BUG-604 tests
// above (which use the same split for the same reason).
//
// RED PROOF (documented, not left in the tree — GR#24 forbids destructive
// git): scratch-copy Busy.tsx (`cp Busy.tsx Busy.tsx.bak`), replace the
// refcounted `setTimeout(() => { activeRef.current = ...; if (...) setBusy
// (false); }, 60)` back to the pre-fix unconditional `setTimeout(() =>
// setBusy(false), 60)` (dropping `activeRef` entirely), then re-run this
// file. The "overlapping slow+slow" test below goes red — probe.busy flips
// to false when the FIRST of the two overlapping calls finishes its linger,
// even though the second call is still well within its own working window
// — before restoring the fixed version from the backup and deleting it.
// ---------------------------------------------------------------------------

test('BUG-614: two overlapping slow actions keep the chip shown continuously (no flicker) until the LAST one finishes', async () => {
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
      let resolveFirst: () => void = () => {};
      let resolveSecond: () => void = () => {};
      const firstPromise = new Promise<void>((res) => {
        resolveFirst = res;
      });
      const secondPromise = new Promise<void>((res) => {
        resolveSecond = res;
      });

      // t=0: first slow action starts.
      await act(async () => {
        probe.run!(() => firstPromise);
      });
      // t=0->30: first's 30ms defer fires; its chipTimer (target t=280) is
      // now registered.
      await act(async () => {
        mock.timers.tick(30);
      });

      // t=30: second slow action starts, well overlapping the first.
      await act(async () => {
        probe.run!(() => secondPromise);
      });
      // t=30->60: second's 30ms defer fires; its chipTimer (target t=310) is
      // now registered.
      await act(async () => {
        mock.timers.tick(30);
      });

      // t=60->280: crosses the first call's 250ms chip-delay threshold.
      await act(async () => {
        mock.timers.tick(220);
      });
      assert.equal(probe.busy, true, 'chip must be visible once the first call has blocked past its own 250ms threshold');

      // First call settles at t=280 (logical). Its 60ms linger is now
      // registered (target t=340) — the second call is still running.
      await act(async () => {
        resolveFirst();
        await flushMicrotasks();
      });
      assert.equal(probe.busy, true, 'chip must remain visible immediately after the first call settles — the second call is still in flight');

      // t=280->340: fires the second call's chipTimer (target t=310, already
      // registered, harmless no-op since busy is already true) and then the
      // first call's linger (target t=340) — the decrement must NOT zero the
      // in-flight count because the second call is still outstanding.
      await act(async () => {
        mock.timers.tick(60);
      });
      assert.equal(probe.busy, true, 'BUG-614 regression: the chip must NOT flicker off when the first of two overlapping calls finishes its linger while the second is still running');
      assert.ok(container.querySelector('.busy-chip'), '.busy-chip must still be rendered — no flicker');

      // Second call settles at t=340 (logical). Its own 60ms linger is now
      // registered (target t=400).
      await act(async () => {
        resolveSecond();
        await flushMicrotasks();
      });
      assert.equal(probe.busy, true, 'chip must still be visible immediately after the second (last outstanding) call settles');

      await act(async () => {
        mock.timers.tick(59);
      });
      assert.equal(probe.busy, true, "chip must still be visible just before the second call's 60ms linger elapses");

      await act(async () => {
        mock.timers.tick(1);
      });
      assert.equal(probe.busy, false, "chip must hide only once the LAST outstanding call's linger has elapsed");
      assert.equal(container.querySelector('.busy-chip'), null, '.busy-chip must be removed once every overlapping call has finished');
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

test('BUG-614: a fast action started mid-flight of a slow action does not hide the chip early', async () => {
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
      let resolveSlow: () => void = () => {};
      const slowPromise = new Promise<void>((res) => {
        resolveSlow = res;
      });

      // t=0: slow action starts.
      await act(async () => {
        probe.run!(() => slowPromise);
      });
      // t=0->30: defer fires, chipTimer registered (target t=280).
      await act(async () => {
        mock.timers.tick(30);
      });
      // t=30->280: crosses the 250ms threshold.
      await act(async () => {
        mock.timers.tick(250);
      });
      assert.equal(probe.busy, true, 'slow action chip must be visible');

      // t=280: a fast action starts while the slow one's chip is showing.
      let resolveFast: () => void = () => {};
      const fastPromise = new Promise<void>((res) => {
        resolveFast = res;
      });
      await act(async () => {
        probe.run!(() => fastPromise);
      });
      // t=280->310: fast call's own 30ms defer fires; its chipTimer
      // (target t=560) is registered but irrelevant — it settles long before.
      await act(async () => {
        mock.timers.tick(30);
      });

      // Fast action settles almost immediately, well inside its own 250ms
      // window. Its 60ms linger is now registered (target t=370).
      await act(async () => {
        resolveFast();
        await flushMicrotasks();
      });
      assert.equal(probe.busy, true, 'chip must remain visible — the slow action is still running');

      // t=310->370: fires the fast call's linger. Its decrement must not
      // zero out the in-flight count while the slow action is still running.
      await act(async () => {
        mock.timers.tick(60);
      });
      assert.equal(probe.busy, true, "BUG-614 regression: a fast overlapping call finishing its linger must not hide the chip while the original slow call is still in flight");
      assert.ok(container.querySelector('.busy-chip'), '.busy-chip must still be rendered');

      // The slow action itself settles now and its linger runs out — only
      // then should the chip hide.
      await act(async () => {
        resolveSlow();
        await flushMicrotasks();
      });
      await act(async () => {
        mock.timers.tick(60);
      });
      assert.equal(probe.busy, false, 'chip must hide once the slow action (the true last outstanding call) has finished its linger');
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

test("BUG-614: a fast action started during a slow action's post-completion linger keeps the chip shown continuously", async () => {
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
      let resolveSlow: () => void = () => {};
      const slowPromise = new Promise<void>((res) => {
        resolveSlow = res;
      });

      // t=0: slow action starts.
      await act(async () => {
        probe.run!(() => slowPromise);
      });
      // t=0->30: defer fires, chipTimer registered (target t=280).
      await act(async () => {
        mock.timers.tick(30);
      });
      // t=30->280: crosses the 250ms threshold.
      await act(async () => {
        mock.timers.tick(250);
      });
      assert.equal(probe.busy, true);

      // Slow action settles at t=280 — enters its 60ms linger window
      // (target t=340).
      await act(async () => {
        resolveSlow();
        await flushMicrotasks();
      });
      assert.equal(probe.busy, true, 'chip must still be visible at the start of the linger');

      // t=280: a NEW fast action starts partway through that linger.
      let resolveFast: () => void = () => {};
      const fastPromise = new Promise<void>((res) => {
        resolveFast = res;
      });
      await act(async () => {
        probe.run!(() => fastPromise);
      });

      // t=280->320: the fast call's 30ms defer fires (target t=310, well
      // before the slow call's linger elapses at t=340) — it increments the
      // in-flight count before the slow call's decrement can land.
      await act(async () => {
        mock.timers.tick(40);
      });
      assert.equal(probe.busy, true, 'chip must remain visible while the new fast call is in flight');

      // t=320->340: fires the slow call's linger. Its decrement must NOT
      // zero the in-flight count because the new fast call is now counted.
      await act(async () => {
        mock.timers.tick(20);
      });
      assert.equal(probe.busy, true, "BUG-614 regression: the chip must stay shown continuously — a new call started during the outgoing call's linger must keep it up");
      assert.ok(container.querySelector('.busy-chip'), '.busy-chip must still be rendered — continuous, no flicker');

      // The fast call settles and its own 60ms linger runs to completion —
      // only then, as the true last outstanding call, does the chip hide.
      await act(async () => {
        resolveFast();
        await flushMicrotasks();
      });
      await act(async () => {
        mock.timers.tick(60);
      });
      assert.equal(probe.busy, false, 'chip must hide once the fast call (now the last outstanding call) finishes its own linger');
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
