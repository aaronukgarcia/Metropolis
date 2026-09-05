// attack-feat-webworker-defaulton-round.test.tsx — INDEPENDENT DESTRUCTIVE
// ROUND (GR#23, attacker != author) against FEAT-2326609771 (Web Worker tick
// offload flipped to DEFAULT-ON). This file targets the specific gaps this
// round found in the existing suite (test/feat-2326609771-webworker-
// default-on.test.tsx and test/simworker-offload.test.mjs), which is
// otherwise thorough (flag matrix, handshake-timeout derivation incl.
// hostile inputs, HUD honesty, N1/N2 rate-ceiling model). What it does NOT
// prove, and this file does:
//
//   (A) THE DOUBLE-APPLY RACE — the Web Worker saga's exact cautionary shape.
//       The existing "never replies" test proves the FORCED SYNC TICK fires
//       on handshake timeout, but its FakeWorker.postMessage "deliberately
//       NEVER calls onmessage" — it never exercises what happens when the
//       doomed request's reply arrives LATE, i.e. AFTER the timeout has
//       already torn the worker down and run the abandoned tick
//       synchronously. This is exactly the shape the round brief calls out:
//       a late reply racing a fallback transition must be discarded, never
//       applied, and the tick must land EXACTLY ONCE either way.
//   (B) Defense-in-depth: worker.onmessage/onerror are actually NULLED at
//       teardown, not just relying on the controller's requestId-mismatch
//       discard as the only line of defense.
//   (C) webWorkerOffloadEnabled() fail-closed when the Worker CAPABILITY IS
//       PRESENT but localStorage.getItem itself throws (quota/denied/SSR
//       shim) — the existing "never throws" test in simworker-offload.test.mjs
//       has no Worker global at all, so its assertion is vacuously true via
//       the earlier `typeof Worker === 'undefined'` short-circuit and never
//       actually exercises the try/catch around the storage read.
//   (D) A worker that fails AFTER a successful tick (mid-session
//       runtime-error, not a handshake failure) must not permanently lose
//       ticks — the clock must keep advancing at the ordinary cadence,
//       exactly once per interval fire, after the crash.
//
// ROUND FOLLOW-UP APPLIED (2026-09-04, Aaron-accepted pre-landing): (D)'s
// original finding was the ASYMMETRY between the two fallback paths — the
// handshake-timeout path ran the abandoned tick PROACTIVELY the instant it
// gave up on the worker, but onerror just cleared workerRef and waited for
// the next scheduled interval fire, silently costing up to one extra
// SPEED_MS interval of lag on top of whatever request was already lost.
// store.tsx's worker.onerror now runs the same proactive forced-sync-tick
// the instant it detects a request was actually in flight (never for a
// crash with nothing outstanding) — test (D) below asserts the tick
// advances IMMEDIATELY on the crash, not on the following interval fire.
//
// FOREGROUND from webconsole/ via tools/test/scoped.mjs's .test.tsx ->
// `tsx --test` dispatch (mirrors the sibling FEAT-2326609771 test file's own
// header note).
import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';
import { webWorkerOffloadEnabled } from '../src/sim/webWorkerFlag.ts';
import { deriveHandshakeTimeoutMs } from '../src/sim/simWorkerOffloadController.ts';
import { getGlobalWorkerFallbackTracker } from '../src/sim/webWorkerFallbackStatus.ts';
import { initialState } from '../src/sim/engine.ts';

// ---------------------------------------------------------------------------
// Shared jsdom + interval/timeout-capture harness, mirroring the sibling
// FEAT-2326609771 test file's own helpers (kept local/duplicated rather than
// imported so this file stands alone as an independent attacker artifact).
// ---------------------------------------------------------------------------

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
  if (typeof (globalThis as any).ResizeObserver === 'undefined') {
    (globalThis as any).ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    };
  }
  (globalThis as any).localStorage = window.localStorage;
  return dom;
}

/** Capture EVERY setInterval registration (regardless of delay) — used to
 *  find the tick-driver's interval without needing to know SPEED_MS's value
 *  up front (store.tsx registers exactly one interval on mount at
 *  state.speed's resolved ms).
 *
 *  BUG-721: store.tsx's tick-driver interval now binds `window.setInterval`
 *  (not the bare global) — see store.tsx's own BUG-721 comments and
 *  TopBar.tsx's EngineLagChip for the full rationale — so this spy targets
 *  `globalThis.window.setInterval` (installJsdom() above assigns
 *  `globalThis.window = <jsdom window>`, which is exactly what `window`
 *  resolves to inside store.tsx). */
function captureAnyInterval() {
  const g = (globalThis as any).window as any;
  const real = g.setInterval.bind(g);
  let captured: (() => void) | null = null;
  g.setInterval = (...args: any[]) => {
    const id = real(...args);
    captured = args[0];
    return id;
  };
  return {
    get: () => captured,
    restore: () => {
      g.setInterval = real;
    },
  };
}

/** Capture the MOST RECENT setTimeout whose delay equals `delayMs` — same
 *  isolation technique the sibling test file uses to fire the handshake
 *  watchdog deterministically. */
function captureNamedSetTimeout(delayMs: number) {
  const g = globalThis as any;
  const real = g.setTimeout.bind(globalThis);
  let captured: (() => void) | null = null;
  g.setTimeout = (...args: any[]) => {
    const id = real(...args);
    if (args[1] === delayMs) captured = args[0];
    return id;
  };
  return {
    get: () => captured,
    restore: () => {
      g.setTimeout = real;
    },
  };
}

// ===========================================================================
// (A) THE DOUBLE-APPLY RACE — a late reply arriving AFTER the handshake
// watchdog has already torn the worker down and forced the abandoned tick
// through the synchronous fallback.
// ===========================================================================

test('ATTACK: a late worker reply arriving AFTER the handshake-timeout fallback must be DISCARDED, never applied — the tick lands exactly once', async () => {
  const dom = installJsdom();
  const intervalSpy = captureAnyInterval();
  const expectedHandshakeTimeoutMs = deriveHandshakeTimeoutMs(initialState().buildings.length);
  const timeoutSpy = captureNamedSetTimeout(expectedHandshakeTimeoutMs);
  try {
    dom.window.localStorage.setItem('metropolis.webworker', 'on');

    // A hostile FakeWorker that captures its own onmessage handler reference
    // so this test can invoke it DIRECTLY after the real worker object has
    // already been torn down (worker.onmessage set to null, worker.terminate()
    // called) — simulating a message TASK that was already queued by the
    // browser's event loop before termination, which per the DOM spec would
    // find no handler attached by the time it runs (store.tsx nulls
    // worker.onmessage BEFORE calling terminate()). Calling the captured
    // function reference directly is the maximally hostile version of this
    // race: even a browser bug that let an in-flight message task fire
    // AFTER a handler is nulled (invoking whatever closure happened to be
    // captured) must still be rendered harmless by the controller's own
    // requestId-based discard — defense in depth, not reliance on a single
    // mechanism.
    let capturedOnMessage: ((ev: any) => void) | null = null;
    let postMessageCallCount = 0;
    class LateReplyWorker {
      set onmessage(fn: ((ev: any) => void) | null) {
        capturedOnMessage = fn;
      }
      get onmessage() {
        return capturedOnMessage;
      }
      onerror: any = null;
      postMessage(msg: any) {
        postMessageCallCount++;
        // Deliberately never schedules a reply on its own — the test fires
        // the captured handler manually, AFTER the handshake timeout, to
        // control the exact ordering under attack.
        (this as any)._lastMsg = msg;
      }
      terminate() {}
    }
    (globalThis as any).Worker = LateReplyWorker;

    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimProvider, useSim } = await import('../src/sim/store.tsx');

    getGlobalWorkerFallbackTracker().reset();

    let latestTick = -1;
    function Probe() {
      const { state } = useSim();
      latestTick = state.tick;
      return null;
    }

    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);
    await act(async () => {
      root.render(React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }));
    });

    const tickCallback = intervalSpy.get();
    assert.ok(tickCallback, 'the tick-driver interval must have been registered on mount');

    const tickBefore = latestTick;

    // First interval fire: posts the doomed request and arms the handshake
    // watchdog.
    await act(async () => {
      tickCallback!();
    });
    assert.equal(postMessageCallCount, 1, 'precondition: exactly one request was posted to the worker');
    const handshakeTimeoutCallback = timeoutSpy.get();
    assert.ok(handshakeTimeoutCallback, 'the handshake watchdog must have been armed');
    // Explicit annotation deliberately avoids TS's closure-capture narrowing
    // (capturedOnMessage is only ever reassigned inside the class setter
    // closure above, which control-flow analysis does not see through,
    // narrowing an unannotated `const` copy to literal `null`) — same
    // sentinel-vs-narrowing gotcha the sibling FEAT-2326609771 test file's
    // own `latestTick` comment documents, applied here to a function ref.
    const capturedHandlerBeforeTeardown: ((ev: any) => void) | null = capturedOnMessage;
    assert.ok(capturedHandlerBeforeTeardown, 'precondition: the worker had an onmessage handler attached before teardown');

    // Fire the handshake watchdog — this tears the worker down (nulls
    // worker.onmessage per store.tsx, terminates it) and forces the
    // abandoned tick through the synchronous fallback RIGHT NOW.
    await act(async () => {
      handshakeTimeoutCallback!();
    });
    assert.equal(getGlobalWorkerFallbackTracker().reason(), 'handshake-timeout');
    const tickAfterFallback = latestTick;
    assert.ok(tickAfterFallback > tickBefore, 'the forced synchronous fallback must have advanced the clock exactly once');

    // Defense-in-depth check (B, folded in here since it shares the setup):
    // the real Worker instance's onmessage must have been nulled by the
    // teardown, even though this test's LateReplyWorker getter still
    // reflects whatever was last SET — read the CURRENT value, which must
    // be null after teardown.
    assert.equal(capturedOnMessage, null, 'store.tsx must null out worker.onmessage during teardown (defense-in-depth: a real queued message task would then find no handler at all)');

    // THE ATTACK: invoke the CAPTURED (pre-teardown) handler reference
    // directly, simulating a late reply for the now-abandoned request
    // finally "arriving". Build a plausible tickResult reply claiming a
    // HIGHER tick number than what the fallback already applied, to
    // maximise the chance of a naive implementation clobbering forward
    // state with stale data.
    const forgedReply = {
      data: {
        type: 'tickResult',
        requestId: 0, // the first (and only) request issued this session.
        state: { ...(await import('../src/sim/engine.ts')).initialState(), tick: tickAfterFallback + 100 },
      },
    };
    let threw = false;
    const invokeLateHandler = capturedHandlerBeforeTeardown as unknown as (ev: unknown) => void;
    try {
      invokeLateHandler(forgedReply);
    } catch {
      threw = true;
    }

    // The forged/late reply must be a no-op: no crash, and — the load-bearing
    // assertion — the live tick must NOT have jumped to the forged value.
    assert.equal(threw, false, 'a late/forged reply must never crash the app');
    assert.equal(
      latestTick,
      tickAfterFallback,
      `a late reply arriving after the handshake-timeout fallback must be DISCARDED, never applied — got tick ${latestTick}, expected it to stay at ${tickAfterFallback} (forged reply claimed tick ${tickAfterFallback + 100})`
    );

    // And the ordinary fallback cadence must still be intact afterwards —
    // one more interval fire advances the clock by exactly one, not zero
    // (frozen) and not two (a hidden double-apply finally landing).
    const tickBeforeNextFire = latestTick;
    await act(async () => {
      tickCallback!();
    });
    assert.equal(latestTick, tickBeforeNextFire + 1, 'the clock must keep advancing by exactly one tick per interval fire after the attack, no drift either direction');

    await act(async () => {
      root.unmount();
    });
    getGlobalWorkerFallbackTracker().reset();
  } finally {
    intervalSpy.restore();
    timeoutSpy.restore();
    delete (globalThis as any).Worker;
    dom.window.close();
  }
});

// ===========================================================================
// (C) webWorkerOffloadEnabled — Worker capability PRESENT but
// localStorage.getItem itself throws. The existing "never throws" coverage
// in test/simworker-offload.test.mjs has no Worker global, so it short-
// circuits on the CAPABILITY check before ever reaching the storage read —
// this test forces the try/catch's OTHER guard to actually matter.
// ===========================================================================

describe('ATTACK: webWorkerOffloadEnabled fail-closed when Worker IS present but localStorage throws', () => {
  const origWorker = (globalThis as any).Worker;
  class FakeWorker {
    postMessage() {}
    terminate() {}
  }
  test.beforeEach(() => {
    (globalThis as any).Worker = FakeWorker;
  });
  test.afterEach(() => {
    if (origWorker === undefined) delete (globalThis as any).Worker;
    else (globalThis as any).Worker = origWorker;
  });

  test('window.localStorage getter throws (quota/denied) -> resolves false, never throws, despite Worker being available', () => {
    const origWindow = (globalThis as any).window;
    (globalThis as any).window = {
      get localStorage() {
        throw new Error('SecurityError: localStorage access denied');
      },
    };
    try {
      let result: boolean | undefined;
      assert.doesNotThrow(() => {
        result = webWorkerOffloadEnabled();
      }, 'a throwing localStorage getter must never escape webWorkerOffloadEnabled, even with a real Worker constructor available');
      assert.equal(result, false, 'fail-closed to disabled — the default-ON behaviour must NOT be assumed when the storage read itself is unreliable');
    } finally {
      (globalThis as any).window = origWindow;
    }
  });

  test('localStorage.getItem itself throws (not just the property access) -> resolves false, never throws', () => {
    const origWindow = (globalThis as any).window;
    (globalThis as any).window = {
      localStorage: {
        getItem() {
          throw new Error('QuotaExceededError');
        },
      },
    };
    try {
      let result: boolean | undefined;
      assert.doesNotThrow(() => {
        result = webWorkerOffloadEnabled();
      });
      assert.equal(result, false);
    } finally {
      (globalThis as any).window = origWindow;
    }
  });
});

// ===========================================================================
// (D) A mid-session RUNTIME error (after at least one successful reply, so
// it is classified runtime-error, not handshake-error) must not permanently
// lose ticks — the ordinary fallback cadence must resume, exactly one tick
// per interval fire, with no compensating double-tick and no permanent stall.
// ===========================================================================

test('ATTACK: a worker that crashes mid-session (after a successful tick) does not lose ticks or double-apply on recovery', async () => {
  const dom = installJsdom();
  const intervalSpy = captureAnyInterval();
  try {
    dom.window.localStorage.setItem('metropolis.webworker', 'on');

    let onmessageHandler: ((ev: any) => void) | null = null;
    let onerrorHandler: ((ev: any) => void) | null = null;
    let postCount = 0;
    let nextShouldCrash = false;
    class FlakyWorker {
      set onmessage(fn: any) {
        onmessageHandler = fn;
      }
      get onmessage() {
        return onmessageHandler;
      }
      set onerror(fn: any) {
        onerrorHandler = fn;
      }
      get onerror() {
        return onerrorHandler;
      }
      postMessage(msg: any) {
        postCount++;
        if (nextShouldCrash) {
          // Simulate the worker dying mid-computation: no reply, onerror
          // fires asynchronously (as it genuinely would) — modelled here as
          // a synchronous callback invocation the test triggers explicitly
          // below, mirroring how the sibling suite drives onerror.
          return;
        }
        // Healthy reply: echo back a state advancing tick by 1.
        queueMicrotask(() => {
          onmessageHandler?.({ data: { type: 'tickResult', requestId: msg.requestId, state: { ...msg.state, tick: msg.state.tick + 1 } } });
        });
      }
      terminate() {}
    }
    (globalThis as any).Worker = FlakyWorker;

    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimProvider, useSim } = await import('../src/sim/store.tsx');

    getGlobalWorkerFallbackTracker().reset();

    let latestTick = -1;
    function Probe() {
      const { state } = useSim();
      latestTick = state.tick;
      return null;
    }

    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);
    await act(async () => {
      root.render(React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }));
    });

    const tickCallback = intervalSpy.get();
    assert.ok(tickCallback, 'the tick-driver interval must have been registered on mount');

    // First fire: healthy reply, proves the worker (handshake-complete now).
    const tickBefore = latestTick;
    await act(async () => {
      tickCallback!();
      await new Promise((r) => setTimeout(r, 0)); // let the queued microtask settle inside act().
    });
    assert.ok(latestTick > tickBefore, 'the first (healthy) tick must have applied');
    assert.equal(getGlobalWorkerFallbackTracker().reason(), null, 'no fallback yet — the worker is genuinely alive');

    // Second fire: this one "crashes" (no reply is ever sent).
    nextShouldCrash = true;
    const tickBeforeCrash = latestTick;
    await act(async () => {
      tickCallback!();
    });
    assert.equal(postCount, 2, 'precondition: the second request was actually posted');
    assert.equal(latestTick, tickBeforeCrash, 'the crashed request must not have applied anything yet — no reply has arrived');

    // Fire onerror directly (as store.tsx's worker.onerror would be invoked
    // by a real crashing Worker) — this is a RUNTIME error since one reply
    // already landed successfully above (workerHandshakeCompleteRef is true).
    await act(async () => {
      onerrorHandler?.({ message: 'simulated runtime crash' } as any);
    });
    assert.equal(
      getGlobalWorkerFallbackTracker().reason(),
      'runtime-error',
      'a crash AFTER at least one successful reply must be classified runtime-error, not handshake-error'
    );

    // FEAT-2326609771 round follow-up ("make the onerror recovery path
    // proactive like the handshake-timeout path"): the CRASHED request's own
    // reply is still never applied (B3 holds — no reply was ever observed
    // for it, so it is never journaled/applied as such), but onerror now
    // runs a FRESH synchronous tick immediately, right here, the same way
    // the handshake-timeout path already did — rather than silently waiting
    // for the next scheduled interval fire and reading a stale in-flight
    // request as outstanding in the meantime. This is not "the crashed
    // reply materialising"; it is a brand-new tick computed from CURRENT
    // state via the ordinary reducer, exactly as if the tick-driver's own
    // interval had happened to fire at this instant instead.
    assert.equal(
      latestTick,
      tickBeforeCrash + 1,
      'onerror must run the abandoned tick PROACTIVELY (a fresh synchronous tick from current state), not wait for the next interval fire — the round flagged this exact asymmetry against the handshake-timeout path'
    );

    // ...and the VERY NEXT interval fire (now routed through the ordinary
    // main-thread fallback, since workerRef is cleared) must advance the
    // clock by EXACTLY one MORE tick — proving the crash costs at most one
    // tick's worth of delay, never a permanent stall and never a
    // compensating double-tick trying to "catch up".
    const tickBeforeRecoveryFire = latestTick;
    await act(async () => {
      tickCallback!();
    });
    assert.equal(
      latestTick,
      tickBeforeRecoveryFire + 1,
      'the tick-driver must keep advancing by exactly one tick per interval fire after the proactive recovery — no permanent loss, no double-catch-up'
    );

    await act(async () => {
      root.unmount();
    });
    getGlobalWorkerFallbackTracker().reset();
  } finally {
    intervalSpy.restore();
    delete (globalThis as any).Worker;
    dom.window.close();
  }
});
