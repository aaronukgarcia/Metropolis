// feat-2326609777-store-integration.test.tsx — FEAT-2326609777 (2026-09-06):
// end-to-end proof that store.tsx's real worker tick/hydrate message path
// correctly drives the new delta-sync protocol (src/sim/simWorkerDelta.ts) —
// not just the pure diff/patch functions (see
// feat-2326609777-delta-sync.test.mjs for those), but the ACTUAL
// issueTickRequest/worker.onmessage wiring in store.tsx, mounted via a real
// SimProvider exactly like FEAT-2326609771's own end-to-end tests (same
// jsdom+createRoot idiom, same tick-driver-interval spy technique).
//
// The FakeWorker below is DELIBERATELY faithful to the real simWorker.ts's
// protocol (it imports the SAME runTick/diffSimState/applyStateDelta this
// feature's own worker entry uses, and sets `deltaCapable: true` on its
// first reply) — this is what makes it exercise store.tsx's delta branch at
// all. Every PRE-EXISTING FakeWorker mock in this suite (simworker-offload,
// feat-2326609771, attack-feat-webworker-defaulton-round) deliberately does
// NOT set that flag and is therefore NEVER switched into delta mode — see
// simWorkerDelta.ts's "BACKWARD COMPATIBILITY" section — so none of them
// needed to change for this feature to land.
//
// Proves:
//   (1) After the first tick, subsequent requests are 'runTickDelta', not
//       'runTick' — the optimisation actually activates.
//   (2) 60 ticks driven end-to-end through this real worker-message path
//       produce a state byte-identical to running the reducer directly on
//       the same starting state the same number of times (render fields are
//       not corrupted by the diff/patch round trip at real dogfood scale).
//   (3) A non-tick action dispatched while a delta-mode tick is in flight
//       still supersedes it correctly (BUG-618/787's lag chip contract,
//       simWorkerOffloadController.ts's invalidateInFlight/decideTickReply)
//       — AND the run continues to produce correct states afterwards,
//       proving a discarded delta reply does not leave workerKnownStateRef
//       out of sync with what the worker actually cached.
//   (4) GR#27: the live `state` exposed via useSim() after a delta-driven
//       tick is always a FULL, valid SimState (a real buildings array, not
//       a partial/delta object) — capture-before-wipe reads this state
//       directly and must never see anything else.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';
import { reducer } from '../src/sim/engine.ts';
import { runTick } from '../src/sim/simWorkerProtocol.ts';
import { diffSimState, applyStateDelta } from '../src/sim/simWorkerDelta.ts';

const TICK_LOOP_DELAY_MS = 900; // SPEED_MS[1] — engine.ts's default speed.
const ERROR_RING_STORAGE_KEY = 'metropolis.errorRing';
function readErrorRing(dom: JSDOM): any[] {
  const raw = dom.window.localStorage.getItem(ERROR_RING_STORAGE_KEY);
  return raw ? JSON.parse(raw) : [];
}

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

/** Captures the tick-driver's setInterval callback for MANUAL invocation,
 *  WITHOUT ever arming a real timer — this test drives 60 ticks per case,
 *  which (unlike the 1-2-tick end-to-end tests elsewhere in this suite)
 *  runs long enough under parallel/scoped test-runner load that a REAL
 *  background interval firing on its own schedule became an observed
 *  flake (an extra, unintended tick landing between two manual `tickCallback()`
 *  calls once wall-clock time under load exceeded TICK_LOOP_DELAY_MS).
 *  clearInterval on the fake id is a harmless no-op. */
function captureTickLoopCallback() {
  const g = (globalThis as any).window as any;
  const realSetInterval = g.setInterval.bind(g);
  const realClearInterval = g.clearInterval.bind(g);
  let captured: (() => void) | null = null;
  const FAKE_ID = -1;
  g.setInterval = (...args: any[]) => {
    if (args[1] === TICK_LOOP_DELAY_MS) {
      // Capture the callback for manual invocation only — deliberately
      // never arm a REAL timer for it (see this function's own doc comment
      // above for why: a 60-tick test run long enough under load for a real
      // background fire to land between two manual calls).
      captured = args[0];
      return FAKE_ID;
    }
    return realSetInterval(...args);
  };
  g.clearInterval = (id: any) => {
    if (id === FAKE_ID) return; // nothing real was ever armed for this id.
    realClearInterval(id);
  };
  return {
    get: () => captured,
    restore: () => {
      g.setInterval = realSetInterval;
      g.clearInterval = realClearInterval;
    },
  };
}

/** A worker mock that FAITHFULLY implements the real simWorker.ts protocol
 *  (full `runTick` bootstrap, then `runTickDelta`/`tickResultDelta` once its
 *  first reply's `deltaCapable: true` has been accepted) — synchronous
 *  (calls `this.onmessage` directly inside `postMessage`, no real thread),
 *  which is fine here since this test drives the tick-loop callback and
 *  `act()` boundaries explicitly rather than depending on real event-loop
 *  ordering. Records every message it ever received, for the assertions
 *  below to inspect. */
function makeDeltaCapableFakeWorker(messageLog: any[]) {
  return class FakeWorker {
    onmessage: ((ev: any) => void) | null = null;
    onerror: ((ev: any) => void) | null = null;
    cachedState: any = null;
    postMessage(msg: any) {
      messageLog.push(msg);
      if (msg.type === 'runTick') {
        const preTick = msg.state;
        const nextState = runTick(preTick);
        this.cachedState = nextState;
        this.onmessage?.({ data: { type: 'tickResult', state: nextState, requestId: msg.requestId, deltaCapable: true } });
        return;
      }
      if (msg.type === 'runTickDelta') {
        const preTick = applyStateDelta(this.cachedState, msg.delta);
        const nextState = runTick(preTick);
        const delta = diffSimState(preTick, nextState);
        this.cachedState = nextState;
        this.onmessage?.({ data: { type: 'tickResultDelta', requestId: msg.requestId, delta } });
      }
    }
    terminate() {}
  };
}

test('FEAT-2326609777: store.tsx switches to runTickDelta after the first reply, and 60 delta-driven ticks match a direct reducer chain byte-for-byte', async () => {
  const dom = installJsdom();
  const tickSpy = captureTickLoopCallback();
  const messageLog: any[] = [];
  try {
    dom.window.localStorage.setItem('metropolis.webworker', 'on');
    (globalThis as any).Worker = makeDeltaCapableFakeWorker(messageLog);

    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimProvider, useSim } = await import('../src/sim/store.tsx');

    let latestState: any = null;
    function Probe() {
      const { state } = useSim();
      latestState = state;
      return null;
    }

    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);
    await act(async () => {
      root.render(React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }));
    });

    // Capture the EXACT starting state (before any tick) to build an
    // independent reference chain below.
    const startingState = latestState;
    assert.ok(startingState, 'precondition: SimProvider mounted and exposed an initial state');

    const tickCallback = tickSpy.get();
    assert.ok(tickCallback, 'the tick-driver interval must have been registered on mount');

    const N = 60;
    for (let i = 0; i < N; i++) {
      await act(async () => {
        tickCallback!();
      });
    }

    // (1) Protocol switch: request #1 is 'runTick' (full — no cache yet);
    // every subsequent request must be 'runTickDelta'.
    const requestMessages = messageLog.filter((m) => m.type === 'runTick' || m.type === 'runTickDelta');
    assert.equal(requestMessages.length, N, `expected exactly ${N} tick requests to have been posted`);
    assert.equal(requestMessages[0].type, 'runTick', 'the very first request must be a full sync (no worker cache exists yet)');
    for (let i = 1; i < requestMessages.length; i++) {
      assert.equal(requestMessages[i].type, 'runTickDelta', `request #${i + 1} must be delta-mode once the worker has proven deltaCapable`);
    }

    // (2) Byte-identical render-field proof: an independent reducer chain
    // starting from the SAME captured starting state, run N times directly,
    // must match what store.tsx (driven entirely through the delta-sync
    // worker path) ended up with.
    let reference = startingState;
    for (let i = 0; i < N; i++) reference = reducer(reference, { type: 'tick' });
    assert.deepEqual(latestState, reference, `after ${N} delta-driven ticks, live store state must be byte-identical to a direct reducer chain over the same starting state`);

    // (4) GR#27: the live state is a full, valid SimState — a real
    // buildings array, not a partial/delta shape.
    assert.ok(Array.isArray(latestState.buildings), 'live state must expose a real buildings array (capture-before-wipe reads this directly)');
    assert.ok(latestState.buildings.length > 0, 'starterCity has buildings; they must survive the delta round trip');
    assert.equal(typeof latestState.tick, 'number');

    await act(async () => {
      root.unmount();
    });
  } finally {
    tickSpy.restore();
    delete (globalThis as any).Worker;
    dom.window.close();
  }
});

test('FEAT-2326609777: a non-tick action superseding an in-flight delta-mode request is discarded correctly, and subsequent ticks stay correct afterwards', async () => {
  const dom = installJsdom();
  const tickSpy = captureTickLoopCallback();
  const messageLog: any[] = [];
  try {
    dom.window.localStorage.setItem('metropolis.webworker', 'on');
    class DeferredFakeWorker {
      onmessage: ((ev: any) => void) | null = null;
      onerror: ((ev: any) => void) | null = null;
      cachedState: any = null;
      pending: Array<() => void> = [];
      postMessage(msg: any) {
        messageLog.push(msg);
        // Defer the reply (queued, fired manually below) so a supersede can
        // be dispatched WHILE this request is still outstanding — mirrors
        // the real async postMessage round trip.
        this.pending.push(() => {
          if (msg.type === 'runTick') {
            const nextState = runTick(msg.state);
            this.cachedState = nextState;
            this.onmessage?.({ data: { type: 'tickResult', state: nextState, requestId: msg.requestId, deltaCapable: true } });
          } else if (msg.type === 'runTickDelta') {
            const preTick = applyStateDelta(this.cachedState, msg.delta);
            const nextState = runTick(preTick);
            const delta = diffSimState(preTick, nextState);
            this.cachedState = nextState;
            this.onmessage?.({ data: { type: 'tickResultDelta', requestId: msg.requestId, delta } });
          }
        });
      }
      flushOne() {
        const fn = this.pending.shift();
        fn?.();
      }
      terminate() {}
    }
    const worker = new DeferredFakeWorker();
    (globalThis as any).Worker = function () {
      return worker;
    } as any;

    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimProvider, useSim } = await import('../src/sim/store.tsx');

    let latestState: any = null;
    let dispatchRef: ((a: any) => void) | null = null;
    function Probe() {
      const { state, dispatch } = useSim();
      latestState = state;
      dispatchRef = dispatch;
      return null;
    }

    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);
    await act(async () => {
      root.render(React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }));
    });

    const tickCallback = tickSpy.get();
    const initialTick = latestState.tick;

    // Tick #1: full sync, flushed immediately — establishes the worker cache
    // and deltaCapable.
    await act(async () => {
      tickCallback!();
      worker.flushOne();
    });
    assert.equal(latestState.tick, initialTick + 1);

    // Tick #2: issued in delta mode, but DO NOT flush yet — dispatch a
    // non-tick action first, which must supersede it.
    const tickBeforeSupersede = latestState.tick;
    const fundsBeforeAction = latestState.funds;
    await act(async () => {
      tickCallback!();
    });
    assert.equal(messageLog[messageLog.length - 1].type, 'runTickDelta', 'precondition: tick #2 was issued in delta mode');
    await act(async () => {
      dispatchRef!({ type: 'debugFunds', amount: 12345 });
    });
    assert.equal(latestState.funds, fundsBeforeAction + 12345, 'the non-tick action must apply immediately, not wait on the in-flight delta request');
    assert.equal(latestState.tick, tickBeforeSupersede, 'the action itself must not advance the tick');

    // NOW flush the superseded reply — it must be discarded (tick must NOT
    // silently jump, and applying it must not corrupt funds).
    const fundsAfterAction = latestState.funds;
    await act(async () => {
      worker.flushOne();
    });
    assert.equal(latestState.tick, tickBeforeSupersede, 'a superseded delta reply must be discarded, not applied');
    assert.equal(latestState.funds, fundsAfterAction, 'discarding the superseded reply must not alter funds set by the action that superseded it');

    // The NEXT interval fire must issue a FRESH delta request (against the
    // worker's now-current cache, which itself IS in sync — the worker
    // genuinely computed and cached the superseded tick's result) and must
    // still produce a correct, further-advancing state — proving
    // workerKnownStateRef self-healed rather than staying stuck on stale
    // data after a discard.
    await act(async () => {
      tickCallback!();
      worker.flushOne();
    });
    assert.equal(latestState.tick, tickBeforeSupersede + 1, 'ticking must resume correctly after a supersede/discard cycle in delta mode');

    await act(async () => {
      root.unmount();
    });
  } finally {
    tickSpy.restore();
    delete (globalThis as any).Worker;
    dom.window.close();
  }
});

// ===========================================================================
// FEAT-2326609777 round follow-up (opus-round-feat777, 2026-09-06):
// baseTick integrity check + periodic full resync. Proves:
//   (5) a wrong-basis delta (same ids, values differ) is detected and the
//       NEXT round trip is a full sync;
//   (6) a resync occurs after RESYNC_EVERY_TICKS delta requests, and the
//       payload that request carries is the full clone (not a delta);
//   (7) the registry error (MET-V891) is recorded exactly once per
//       detection, not once per subsequent tick.
// ===========================================================================

test('FEAT-2326609777 round follow-up: a wrong-basis delta reply is detected and the NEXT round trip is a full sync', async () => {
  const dom = installJsdom();
  const tickSpy = captureTickLoopCallback();
  const messageLog: any[] = [];
  try {
    dom.window.localStorage.setItem('metropolis.webworker', 'on');
    // A worker that behaves faithfully EXCEPT: on its second reply (the
    // first delta-mode one), it lies about `delta.baseTick` — one less than
    // the tick it actually diffed from. Same ids/length/order as a genuine
    // reply (diffSimState computed the delta correctly); ONLY the integrity
    // stamp is wrong — exactly the round's attack shape.
    let replyCount = 0;
    class LyingWorker {
      onmessage: ((ev: any) => void) | null = null;
      onerror: ((ev: any) => void) | null = null;
      cachedState: any = null;
      postMessage(msg: any) {
        messageLog.push(msg);
        if (msg.type === 'runTick') {
          const nextState = runTick(msg.state);
          this.cachedState = nextState;
          replyCount++;
          this.onmessage?.({ data: { type: 'tickResult', state: nextState, requestId: msg.requestId, deltaCapable: true } });
          return;
        }
        if (msg.type === 'runTickDelta') {
          replyCount++;
          if (replyCount === 2) {
            // Lie: report a baseTick one less than the real basis. The
            // worker itself still computes correctly (it's OUR reply's
            // stamp that's wrong, modeling main-side detection of a
            // reply whose delta doesn't match the basis main is holding).
            const preTick = applyStateDelta(this.cachedState, msg.delta);
            const nextState = runTick(preTick);
            const realDelta = diffSimState(preTick, nextState);
            this.cachedState = nextState;
            this.onmessage?.({
              data: { type: 'tickResultDelta', requestId: msg.requestId, delta: { ...realDelta, baseTick: realDelta.baseTick - 1 } },
            });
            return;
          }
          const preTick = applyStateDelta(this.cachedState, msg.delta);
          const nextState = runTick(preTick);
          const delta = diffSimState(preTick, nextState);
          this.cachedState = nextState;
          this.onmessage?.({ data: { type: 'tickResultDelta', requestId: msg.requestId, delta } });
        }
      }
      terminate() {}
    }
    (globalThis as any).Worker = LyingWorker;

    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimProvider, useSim } = await import('../src/sim/store.tsx');

    let latestState: any = null;
    function Probe() {
      const { state } = useSim();
      latestState = state;
      return null;
    }
    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);
    await act(async () => {
      root.render(React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }));
    });
    const tickCallback = tickSpy.get();
    const tickBefore = latestState.tick;

    // Request #1: full bootstrap. Request #2: delta, but the reply LIES
    // about baseTick — must be detected and discarded (tick does not
    // advance this round, and a registry error is recorded).
    await act(async () => {
      tickCallback!();
    });
    await act(async () => {
      tickCallback!();
    });
    assert.equal(messageLog[1].type, 'runTickDelta', 'precondition: request #2 was issued in delta mode');
    assert.equal(latestState.tick, tickBefore + 1, 'the mismatched reply must be discarded, not applied — tick must not have advanced a second time');
    const mismatchErrors = readErrorRing(dom).filter((e) => e.code === 'MET-V891');
    assert.equal(mismatchErrors.length, 1, 'exactly one MET-V891 registry error must be recorded for this one detection');
    assert.equal(mismatchErrors[0].count, 1);

    // Request #3 (the NEXT round trip after the detection) must be a full
    // resync, not another delta — workerKnownStateRef was reset to null.
    await act(async () => {
      tickCallback!();
    });
    assert.equal(messageLog.length, 3);
    assert.equal(messageLog[2].type, 'runTick', 'the round trip immediately after a detected mismatch must be a full sync');
    assert.equal(latestState.tick, tickBefore + 2, 'ticking must resume correctly via the full-resync round trip');

    await act(async () => {
      root.unmount();
    });
  } finally {
    tickSpy.restore();
    delete (globalThis as any).Worker;
    dom.window.close();
  }
});

test('FEAT-2326609777 round follow-up: a full resync is forced after RESYNC_EVERY_TICKS delta requests, with a full-clone payload that request', async () => {
  const dom = installJsdom();
  const tickSpy = captureTickLoopCallback();
  const messageLog: any[] = [];
  try {
    dom.window.localStorage.setItem('metropolis.webworker', 'on');
    (globalThis as any).Worker = makeDeltaCapableFakeWorker(messageLog);

    const { RESYNC_EVERY_TICKS } = await import('../src/sim/simWorkerDelta.ts');
    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimProvider, useSim } = await import('../src/sim/store.tsx');

    let latestState: any = null;
    function Probe() {
      const { state } = useSim();
      latestState = state;
      return null;
    }
    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);
    await act(async () => {
      root.render(React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }));
    });
    const tickCallback = tickSpy.get();
    const initialTick = latestState.tick;

    // Request #1 is the bootstrap full sync; requests #2..#(1+RESYNC_EVERY_TICKS)
    // are delta-mode; request #(2+RESYNC_EVERY_TICKS) is the forced resync.
    const totalRequests = 2 + RESYNC_EVERY_TICKS;
    for (let i = 0; i < totalRequests; i++) {
      await act(async () => {
        tickCallback!();
      });
    }
    assert.equal(messageLog.length, totalRequests);
    assert.equal(messageLog[0].type, 'runTick', 'request #1 is the bootstrap full sync');
    for (let i = 1; i < 1 + RESYNC_EVERY_TICKS; i++) {
      assert.equal(messageLog[i].type, 'runTickDelta', `request #${i + 1} must be delta-mode (within the RESYNC_EVERY_TICKS window)`);
    }
    const resyncMsg = messageLog[1 + RESYNC_EVERY_TICKS];
    assert.equal(resyncMsg.type, 'runTick', `request #${2 + RESYNC_EVERY_TICKS} must be the forced periodic full resync`);
    assert.ok('state' in resyncMsg && !('delta' in resyncMsg), 'the forced resync request must carry the FULL state, not a delta payload');

    // And the request immediately after the forced resync goes back to
    // delta mode (the counter reset, it did not stay pinned to "always full").
    await act(async () => {
      tickCallback!();
    });
    assert.equal(messageLog[messageLog.length - 1].type, 'runTickDelta', 'delta mode must resume after the periodic resync, not stay forced full');
    assert.equal(latestState.tick, initialTick + totalRequests + 1, 'the clock must have kept advancing through the forced resync, never stalling');

    await act(async () => {
      root.unmount();
    });
  } finally {
    tickSpy.restore();
    delete (globalThis as any).Worker;
    dom.window.close();
  }
});
