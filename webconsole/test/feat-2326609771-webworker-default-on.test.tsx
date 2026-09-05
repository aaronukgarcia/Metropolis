// feat-2326609771-webworker-default-on.test.tsx — FEAT-2326609771
// (2026-09-04): the Web Worker tick offload flips from opt-in/default-OFF to
// a kill-switch that defaults ON (Aaron's Q100116 answer + the 2026-09-03
// interview, both "Default ON now"). A `.test.tsx` file (routed to `tsx
// --test` by tools/test/scoped.mjs's extension dispatch — see its header)
// so store.tsx/QueueDepthHud.tsx's JSX loads via a plain dynamic `import()`,
// mirroring test/bug605-queue-depth-hud-worker-line.test.tsx's proven
// jsdom + react-dom/client technique rather than simworker-offload.test.mjs's
// tsImport workaround (unnecessary once the file itself is tsx-loaded).
//
// This file proves the hardening default-ON demands:
//   (1) webWorkerFlag.ts's new unset/on/off/junk resolution matrix.
//   (2) simWorkerOffloadController.ts's deriveHandshakeTimeoutMs — the
//       DERIVED (not flat) first-tick-reply timeout: floor/slope/ceiling.
//   (3) webWorkerFallbackStatus.ts's pure tracker + label mapping.
//   (4) End-to-end: a throwing Worker constructor falls back silently-but-
//       visibly — a registry-sourced MET-V856 error, the fallback tracker
//       set, and NO tick lost (the sim clock keeps advancing on the
//       synchronous main-thread path).
//   (5) End-to-end: a Worker that constructs fine but never replies falls
//       back once the DERIVED handshake timeout elapses — same guarantees,
//       plus proof that an action dispatched while the doomed request is
//       still in flight is applied immediately, never lost/buffered.
//   (6) QueueDepthHud.tsx's worker line is honest in all THREE states:
//       worker live, worker failed (falling back), and explicitly off.
//   (7) The determinism parity test (worker-tick-path vs main-thread-only)
//       extended to run against test/scale/fixture.mjs's ~13k-building
//       dogfood-scale fixture, not just a fresh small city.
//   (8) Sanity: this very test environment forces sync mode under the new
//       default-ON flag whenever the Worker capability itself is absent —
//       proving the capability gate, not the flag spelling, is what keeps a
//       Worker-less environment on the synchronous path.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';
import { webWorkerOffloadEnabled } from '../src/sim/webWorkerFlag.ts';
import {
  deriveHandshakeTimeoutMs,
  HANDSHAKE_TIMEOUT_FLOOR_MS,
  HANDSHAKE_TIMEOUT_PER_BUILDING_MS,
  HANDSHAKE_TIMEOUT_CEILING_MS,
} from '../src/sim/simWorkerOffloadController.ts';
import {
  createWorkerFallbackTracker,
  getGlobalWorkerFallbackTracker,
  describeFallbackReason,
} from '../src/sim/webWorkerFallbackStatus.ts';
import { initialState } from '../src/sim/engine.ts';
import { getGlobalWorkerQueueTracker } from '../src/sim/workerQueueDepth.ts';

// ===========================================================================
// (1) webWorkerFlag.ts — unset/on/off/junk resolution matrix.
// ===========================================================================

function withFakeStorage<T>(getItemReturn: string | null, fn: () => T): T {
  const origWindow = (globalThis as any).window;
  (globalThis as any).window = { localStorage: { getItem: () => getItemReturn } };
  try {
    return fn();
  } finally {
    (globalThis as any).window = origWindow;
  }
}

describe('FEAT-2326609771: webWorkerOffloadEnabled default-resolution matrix (Worker capability present)', () => {
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

  test('unset (no key at all) -> ON (the new default)', () => {
    assert.equal(withFakeStorage(null, () => webWorkerOffloadEnabled()), true);
  });

  for (const onValue of ['on', 'On', ' ON ', '1', 'true', 'True', ' 1 ']) {
    test(`explicit ON spelling ${JSON.stringify(onValue)} -> ON`, () => {
      assert.equal(withFakeStorage(onValue, () => webWorkerOffloadEnabled()), true);
    });
  }

  for (const offValue of ['off', 'Off', ' OFF ', '0', 'false', 'False', ' 0 ']) {
    test(`explicit OFF spelling ${JSON.stringify(offValue)} -> OFF (the kill switch)`, () => {
      assert.equal(withFakeStorage(offValue, () => webWorkerOffloadEnabled()), false);
    });
  }

  for (const junkValue of ['', 'yes', 'nope', 'maybe', '2', 'undefined', 'null', 'ON please']) {
    test(`junk/unrecognized value ${JSON.stringify(junkValue)} -> ON (falls through to the new default, not the old fail-closed-to-off)`, () => {
      assert.equal(withFakeStorage(junkValue, () => webWorkerOffloadEnabled()), true);
    });
  }
});

test('FEAT-2326609771: no Worker capability -> always OFF regardless of stored value (capability gate wins over the default)', () => {
  const origWorker = (globalThis as any).Worker;
  delete (globalThis as any).Worker;
  try {
    assert.equal(withFakeStorage('on', () => webWorkerOffloadEnabled()), false, 'ON spelling cannot override a missing Worker constructor');
    assert.equal(withFakeStorage(null, () => webWorkerOffloadEnabled()), false, 'unset (default ON) also cannot override a missing Worker constructor');
  } finally {
    if (origWorker !== undefined) (globalThis as any).Worker = origWorker;
  }
});

// ===========================================================================
// (2) deriveHandshakeTimeoutMs — floor / per-building slope / ceiling clamp.
// ===========================================================================

describe('FEAT-2326609771: deriveHandshakeTimeoutMs — floor, slope, ceiling', () => {
  test('a fresh (0-building) city gets exactly the floor', () => {
    assert.equal(deriveHandshakeTimeoutMs(0), HANDSHAKE_TIMEOUT_FLOOR_MS);
  });

  test('timeout grows monotonically with building count, matching the documented per-building slope', () => {
    const at1000 = deriveHandshakeTimeoutMs(1000);
    const at2000 = deriveHandshakeTimeoutMs(2000);
    assert.equal(at1000, HANDSHAKE_TIMEOUT_FLOOR_MS + 1000 * HANDSHAKE_TIMEOUT_PER_BUILDING_MS);
    assert.ok(at2000 > at1000, 'a bigger city must never derive a SHORTER timeout than a smaller one');
  });

  test("the 49k-building dogfood scale does NOT trip the ceiling (no false positive at Aaron's real scale)", () => {
    const at49k = deriveHandshakeTimeoutMs(49_000);
    assert.ok(
      at49k < HANDSHAKE_TIMEOUT_CEILING_MS,
      `expected 49k buildings to derive a timeout below the ceiling (${HANDSHAKE_TIMEOUT_CEILING_MS}ms), got ${at49k}ms — a legitimately slow-but-alive worker at real dogfood scale must not be misdiagnosed as hung`
    );
    assert.ok(at49k > HANDSHAKE_TIMEOUT_FLOOR_MS, 'a large city must still derive MORE than the bare floor');
  });

  test('an astronomically large city is clamped at the ceiling — the timeout itself has BOTH a floor and a ceiling', () => {
    assert.equal(deriveHandshakeTimeoutMs(10_000_000), HANDSHAKE_TIMEOUT_CEILING_MS);
  });

  for (const hostile of [NaN, -5, -Infinity, Infinity]) {
    test(`hostile buildingCount input ${String(hostile)} falls back to the floor, never NaN/negative/Infinity`, () => {
      const result = deriveHandshakeTimeoutMs(hostile);
      assert.ok(Number.isFinite(result), `expected a finite timeout for hostile input ${hostile}, got ${result}`);
      assert.ok(result >= HANDSHAKE_TIMEOUT_FLOOR_MS, `expected at least the floor for hostile input ${hostile}, got ${result}`);
      assert.ok(result <= HANDSHAKE_TIMEOUT_CEILING_MS);
    });
  }
});

// ===========================================================================
// (3) webWorkerFallbackStatus.ts — pure tracker + label mapping.
// ===========================================================================

describe('FEAT-2326609771: webWorkerFallbackStatus tracker', () => {
  test('starts with reason() null, report() is sticky, reset() clears it', () => {
    const t = createWorkerFallbackTracker();
    assert.equal(t.reason(), null);
    t.report('construct-failed');
    assert.equal(t.reason(), 'construct-failed');
    t.report('handshake-timeout'); // a later report overwrites, no queueing.
    assert.equal(t.reason(), 'handshake-timeout');
    t.reset();
    assert.equal(t.reason(), null);
  });

  test('the global singleton is stable across getGlobalWorkerFallbackTracker() calls', () => {
    const a = getGlobalWorkerFallbackTracker();
    const b = getGlobalWorkerFallbackTracker();
    assert.equal(a, b);
    a.reset(); // leave no residue for other tests sharing this process.
  });

  test('describeFallbackReason has a distinct, non-empty label for every reason', () => {
    const reasons = ['construct-failed', 'handshake-error', 'handshake-timeout', 'runtime-error'] as const;
    const labels = reasons.map(describeFallbackReason);
    assert.equal(
      new Set(labels).size,
      reasons.length,
      'every reason must have a DISTINCT label — a collapsed label would make the HUD/error text ambiguous about which failure mode actually happened'
    );
    for (const label of labels) {
      assert.ok(typeof label === 'string' && label.length > 0);
    }
  });
});

// ===========================================================================
// (4)/(5)/(6) End-to-end: a real SimProvider mount, a fake global Worker.
// Mirrors test/bug605-queue-depth-hud-worker-line.test.tsx's jsdom+createRoot
// idiom and test/simworker-offload.test.mjs's BUG-597 captureTickLoopCallback
// spy (both proven working patterns in this suite) rather than inventing a
// third mounting technique.
// ===========================================================================

const TICK_LOOP_DELAY_MS = 900; // SPEED_MS[1] — engine.ts's default speed.

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
  // backend.ts's queueStorage() reads the BARE `localStorage` global — see
  // simworker-offload.test.mjs's BUG-597 section for why this matters under
  // a Node version with its own experimental built-in localStorage global.
  (globalThis as any).localStorage = window.localStorage;
  return dom;
}

/** Spy on the tick-driver's setInterval, capturing its own callback by its
 *  distinctive delay (TICK_LOOP_DELAY_MS) — same isolation trick as
 *  simworker-offload.test.mjs's BUG-597 section.
 *
 *  BUG-721: store.tsx's tick-driver interval now binds `window.setInterval`
 *  (not the bare global) — see store.tsx's own BUG-721 comments — so this
 *  spy targets `globalThis.window.setInterval` (installJsdom() above assigns
 *  `globalThis.window = <jsdom window>`, exactly what `window` resolves to
 *  inside store.tsx). */
function captureTickLoopCallback() {
  const g = (globalThis as any).window as any;
  const real = g.setInterval.bind(g);
  let captured: (() => void) | null = null;
  g.setInterval = (...args: any[]) => {
    const id = real(...args);
    if (args[1] === TICK_LOOP_DELAY_MS) captured = args[0];
    return id;
  };
  return {
    get: () => captured,
    restore: () => {
      g.setInterval = real;
    },
  };
}

/** Spy on the global setTimeout, capturing the MOST RECENT call whose delay
 *  equals `delayMs` — used to fire the handshake watchdog deterministically
 *  (no real wall-clock wait) without disturbing every OTHER unrelated
 *  setTimeout in store.tsx (journal debounce, upgrade-toast, the rebuild
 *  watchdog at WATCHDOG_MS=10000, ...), which use different delays. */
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

const ERROR_RING_STORAGE_KEY = 'metropolis.errorRing';
function readErrorRing(dom: JSDOM): any[] {
  const raw = dom.window.localStorage.getItem(ERROR_RING_STORAGE_KEY);
  return raw ? JSON.parse(raw) : [];
}

test('FEAT-2326609771: a throwing Worker constructor falls back silently-but-visibly — no tick lost', async () => {
  const dom = installJsdom();
  const spy = captureTickLoopCallback();
  try {
    dom.window.localStorage.setItem('metropolis.webworker', 'on');
    class FakeWorker {
      constructor() {
        throw new Error('boom-construct');
      }
    }
    (globalThis as any).Worker = FakeWorker;

    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimProvider, useSim } = await import('../src/sim/store.tsx');

    getGlobalWorkerFallbackTracker().reset();
    const errorCountBefore = readErrorRing(dom).length;

    // Plain `number` (no `| null`), sentinelled at -1: Probe() always runs
    // synchronously as part of the `act(async () => { root.render(...) })`
    // commit below, so by the time this is read the real value is already
    // set — a `number | null` annotation here would just invite TS's
    // closure-capture narrowing to (incorrectly, but harmlessly) pin the
    // type to literal `null` at every read site, forcing `as number` casts
    // everywhere for no actual safety benefit.
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

    assert.equal(
      getGlobalWorkerFallbackTracker().reason(),
      'construct-failed',
      'a throwing constructor must report construct-failed, not silently vanish'
    );

    const errorsAfter = readErrorRing(dom);
    assert.equal(errorsAfter.length, errorCountBefore + 1, 'exactly one registry-sourced error must be recorded for a throwing Worker constructor (GR#1/GR#7)');
    assert.equal(errorsAfter[0].code, 'MET-V856');

    const tickCallback = spy.get();
    assert.ok(tickCallback, 'the tick-driver interval must have been registered on mount');
    const tickBefore = latestTick;
    await act(async () => {
      tickCallback!();
    });
    assert.equal(
      latestTick,
      tickBefore + 1,
      'the sim clock must still advance via the synchronous fallback reducer after a construction failure — the failure must never lose a tick'
    );

    await act(async () => {
      root.unmount();
    });
    getGlobalWorkerFallbackTracker().reset();
  } finally {
    spy.restore();
    delete (globalThis as any).Worker;
    dom.window.close();
  }
});

test('FEAT-2326609771: a Worker that never replies falls back once the DERIVED handshake timeout elapses — no tick or action lost', async () => {
  const dom = installJsdom();
  const tickSpy = captureTickLoopCallback();
  // starterCity() (engine.ts) seeds a non-trivial road/motorway grid even on
  // a brand-new city, so the handshake watchdog's derived timeout is NOT the
  // bare floor here — compute the REAL expected value from the same
  // initialState() a clean-localStorage boot resolves to, rather than
  // assuming 0 buildings.
  const expectedHandshakeTimeoutMs = deriveHandshakeTimeoutMs(initialState().buildings.length);
  const timeoutSpy = captureNamedSetTimeout(expectedHandshakeTimeoutMs);
  try {
    dom.window.localStorage.setItem('metropolis.webworker', 'on');
    let constructedCount = 0;
    class FakeWorker {
      constructor() {
        constructedCount++;
      }
      postMessage() {
        /* deliberately NEVER calls onmessage — the hostile behaviour under test. */
      }
      terminate() {}
    }
    (globalThis as any).Worker = FakeWorker;

    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimProvider, useSim } = await import('../src/sim/store.tsx');

    getGlobalWorkerFallbackTracker().reset();

    // Plain `number` (no `| null`) for the same reason as the previous
    // test's `latestTick` — see its comment.
    let latestTick = -1;
    let latestBuildings = -1;
    let dispatchRef: ((a: any) => void) | null = null;
    function Probe() {
      const { state, dispatch } = useSim();
      latestTick = state.tick;
      latestBuildings = state.buildings.length;
      dispatchRef = dispatch;
      return null;
    }

    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);
    await act(async () => {
      root.render(React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }));
    });

    const tickCallback = tickSpy.get();
    assert.ok(tickCallback, 'the tick-driver interval must have been registered on mount');

    const tickBefore = latestTick;
    const buildingsBefore = latestBuildings;

    // First interval fire: posts the tick request and arms the handshake
    // watchdog (deriveHandshakeTimeoutMs(0 buildings) === the floor, a fresh
    // city has no buildings yet).
    await act(async () => {
      tickCallback!();
    });
    assert.equal(constructedCount, 1, 'precondition: the worker was actually constructed (this is a handshake failure, not a construction failure)');
    assert.equal(getGlobalWorkerFallbackTracker().reason(), null, 'still within the handshake window — no fallback yet, just a pending request');
    const handshakeTimeoutCallback = timeoutSpy.get();
    assert.ok(handshakeTimeoutCallback, 'the handshake watchdog setTimeout must have been armed the instant the first request was posted');
    // ROUND FOLLOW-UP ("HUD honesty during the handshake window"): the
    // real store.tsx wiring must have reported a handshake start time to
    // the global tracker the instant the first request was posted — this
    // is what lets QueueDepthHud distinguish "still proving alive" from an
    // ordinary steady-state "N pending" backlog (see the dedicated
    // QueueDepthHud tests below for the rendered-text proof).
    assert.equal(typeof getGlobalWorkerQueueTracker().handshakeStartAt(), 'number', 'issuing the first-ever tick request must report a handshake start timestamp to the global tracker');

    // While the doomed request is still outstanding, dispatch a real action —
    // proves a non-tick action is NEVER buffered/lost behind a pending
    // worker round trip, handshake or otherwise.
    // (151, 57) sits directly against starterCity()'s existing road spine
    // (engine.ts places a road run at x=150, y=57..63) — placing here adds
    // EXACTLY one building. A coordinate far from any existing road (e.g.
    // (5,5)) triggers the reducer's own auto-road-connection cascade,
    // silently adding dozens of road tiles to reach the new building — real
    // engine behaviour, but noise this assertion doesn't want to depend on.
    await act(async () => {
      dispatchRef!({ type: 'place', spec: 'res_hut', x: 151, y: 57 });
    });
    assert.equal(
      latestBuildings,
      buildingsBefore + 1,
      'an action dispatched while a tick request is in flight must apply IMMEDIATELY, never lost or delayed behind the pending (and here, doomed) worker round trip'
    );

    // Fire the handshake watchdog directly (no real wall-clock wait).
    await act(async () => {
      handshakeTimeoutCallback!();
    });

    assert.equal(
      getGlobalWorkerFallbackTracker().reason(),
      'handshake-timeout',
      'a worker that never replies within the derived window must be reported as a handshake-timeout fallback'
    );
    const matching = readErrorRing(dom).filter((e) => e.code === 'MET-V856');
    assert.ok(matching.length > 0, 'a registry-sourced MET-V856 error must be recorded for a handshake timeout (GR#1/GR#7)');
    assert.ok(
      latestTick > tickBefore,
      "the abandoned request's tick must still have been run via the forced synchronous fallback the instant the timeout fired — never silently skipped"
    );
    assert.equal(getGlobalWorkerQueueTracker().handshakeStartAt(), null, 'the handshake-timeout teardown must clear the reported start time — a torn-down worker has no "starting" window left to report');

    // Confirm the fallback is durable: a FURTHER interval fire must still
    // advance the clock via the ordinary sync path (the worker is gone for
    // the rest of the session, not retried).
    const tickAfterTimeout = latestTick;
    await act(async () => {
      tickCallback!();
    });
    assert.equal(latestTick, tickAfterTimeout + 1, 'ticks must keep advancing on the sync fallback for the rest of the session after a handshake timeout');

    await act(async () => {
      root.unmount();
    });
    getGlobalWorkerFallbackTracker().reset();
  } finally {
    tickSpy.restore();
    timeoutSpy.restore();
    delete (globalThis as any).Worker;
    dom.window.close();
  }
});

// ===========================================================================
// (6) QueueDepthHud.tsx — three honest states.
// ===========================================================================

async function renderHudLine(dom: JSDOM): Promise<string> {
  const React = await import('react');
  const { createRoot } = await import('react-dom/client');
  const { act } = await import('react-dom/test-utils');
  const { QueueDepthHud } = await import('../src/components/right/QueueDepthHud.tsx');
  const container = dom.window.document.getElementById('root')!;
  const root = createRoot(container);
  await act(async () => {
    root.render(React.default.createElement(QueueDepthHud));
  });
  const line = container.querySelector('[data-testid="qd-worker-line"]');
  const text = line ? line.textContent || '' : '';
  await act(async () => {
    root.unmount();
  });
  return text;
}

test('FEAT-2326609771: QueueDepthHud worker line — worker live (flag on, no fallback reported)', async () => {
  const dom = installJsdom();
  try {
    dom.window.localStorage.setItem('metropolis.webworker', 'on');
    if (typeof (globalThis as any).Worker === 'undefined') {
      (globalThis as any).Worker = class {
        postMessage() {}
        terminate() {}
      };
    }
    getGlobalWorkerFallbackTracker().reset();
    getGlobalWorkerQueueTracker().reset();

    const text = await renderHudLine(dom);
    assert.match(text, /pending|superseded/, `expected the LIVE worker line shape, got: ${JSON.stringify(text)}`);
    assert.doesNotMatch(text, /failed|off/, 'a live worker must never render a failed/off line');
  } finally {
    delete (globalThis as any).Worker;
    dom.window.close();
  }
});

// ---------------------------------------------------------------------------
// ROUND FOLLOW-UP (2026-09-04, Aaron-accepted pre-landing) — "HUD honesty
// during the handshake window": while a worker instance's very FIRST tick
// request is still outstanding (the derived-timeout window), the sim clock
// is FROZEN (no reply has ever landed from this worker) — a plain
// "worker: 1 pending" reading is indistinguishable from an ordinary healthy
// worker one tick behind, when in fact nothing is moving yet. QueueDepthHud
// must show a distinct "worker starting (Xs)" line for this window instead.
// ---------------------------------------------------------------------------

test('FEAT-2326609771: QueueDepthHud worker line — "worker starting (Xs)" during the handshake window, not "N pending"', async () => {
  const dom = installJsdom();
  try {
    dom.window.localStorage.setItem('metropolis.webworker', 'on');
    if (typeof (globalThis as any).Worker === 'undefined') {
      (globalThis as any).Worker = class {
        postMessage() {}
        terminate() {}
      };
    }
    getGlobalWorkerFallbackTracker().reset();
    const tracker = getGlobalWorkerQueueTracker();
    tracker.reset();
    tracker.enqueue(); // depth()===1, exactly the shape that used to render "worker: 1 pending".
    const startedAt = Date.now() - 2500; // 2.5s into the handshake window.
    tracker.reportHandshakeStartAt(startedAt);

    const text = await renderHudLine(dom);
    assert.match(text, /worker starting/i, `expected the handshake-window line, got: ${JSON.stringify(text)}`);
    assert.doesNotMatch(text, /\bpending\b/, 'the handshake window must NOT render as ordinary "N pending" backlog — the round called that reading dishonest');
    assert.match(text, /\(2\.\ds\)/, `expected an elapsed-seconds readout around 2.5s, got: ${JSON.stringify(text)}`);
  } finally {
    getGlobalWorkerQueueTracker().reset();
    delete (globalThis as any).Worker;
    dom.window.close();
  }
});

test('FEAT-2326609771: QueueDepthHud reverts to the ordinary "N pending" reading once the handshake settles', async () => {
  const dom = installJsdom();
  try {
    dom.window.localStorage.setItem('metropolis.webworker', 'on');
    if (typeof (globalThis as any).Worker === 'undefined') {
      (globalThis as any).Worker = class {
        postMessage() {}
        terminate() {}
      };
    }
    getGlobalWorkerFallbackTracker().reset();
    const tracker = getGlobalWorkerQueueTracker();
    tracker.reset();
    tracker.enqueue();
    // handshakeStartAt left at its default (null) — the handshake already
    // settled (a reply landed, or none was ever attempted this render).

    const text = await renderHudLine(dom);
    assert.match(text, /\d+ pending/, `expected the ordinary steady-state reading once handshakeStartAt is null, got: ${JSON.stringify(text)}`);
    assert.doesNotMatch(text, /starting/i);
  } finally {
    getGlobalWorkerQueueTracker().reset();
    delete (globalThis as any).Worker;
    dom.window.close();
  }
});

// ---------------------------------------------------------------------------
// workerQueueDepth.ts's new handshakeStartAt API, tested in isolation (pure,
// no DOM) — the HUD tests above prove it end-to-end; these RED-proof the
// primitive itself.
// ---------------------------------------------------------------------------

test('FEAT-2326609771: workerQueueDepth handshakeStartAt — starts null, is settable, reset() clears it', async () => {
  const { createQueueDepthTracker } = await import('../src/sim/workerQueueDepth.ts');
  const t = createQueueDepthTracker();
  assert.equal(t.handshakeStartAt(), null, 'starts null — no handshake in progress');
  t.reportHandshakeStartAt(12345);
  assert.equal(t.handshakeStartAt(), 12345);
  t.reset();
  assert.equal(t.handshakeStartAt(), null, 'reset() must clear handshakeStartAt alongside depth/streak — every teardown path (onerror, handshake-timeout, effect cleanup) relies on this single call clearing everything');
});

test('FEAT-2326609771: QueueDepthHud worker line — worker failed, falling back (flag on, fallback reported)', async () => {
  const dom = installJsdom();
  try {
    dom.window.localStorage.setItem('metropolis.webworker', 'on');
    if (typeof (globalThis as any).Worker === 'undefined') {
      (globalThis as any).Worker = class {
        postMessage() {}
        terminate() {}
      };
    }
    getGlobalWorkerFallbackTracker().reset();
    getGlobalWorkerFallbackTracker().report('handshake-timeout');

    const text = await renderHudLine(dom);
    assert.match(text, /failed/i, `expected an honest "failed" line when a fallback was reported, got: ${JSON.stringify(text)}`);
    assert.match(text, /handshake timed out/i, 'the specific failure reason must be visible, not a generic "something went wrong"');
  } finally {
    getGlobalWorkerFallbackTracker().reset();
    delete (globalThis as any).Worker;
    dom.window.close();
  }
});

test('FEAT-2326609771: QueueDepthHud worker line — explicitly off', async () => {
  const dom = installJsdom();
  try {
    dom.window.localStorage.setItem('metropolis.webworker', 'off');
    getGlobalWorkerFallbackTracker().reset();

    const text = await renderHudLine(dom);
    assert.match(text, /worker off/i, `expected the explicit-off line, got: ${JSON.stringify(text)}`);
  } finally {
    dom.window.close();
  }
});

// (7) Determinism parity at the ~13k-building dogfood scale lives in
// test/simworker-offload.test.mjs (extending its existing AC-3-style parity
// test) rather than here — it needs test/scale/fixture.mjs (a plain .mjs
// helper with no type declarations), and importing a .mjs from a .tsx file
// under this project's strict tsconfig produces spurious noImplicitAny
// errors with no actual type-safety benefit. Keeping the parity check
// alongside the ORIGINAL small-city version it extends also makes the size
// comparison between the two easier to read in one file.

// ===========================================================================
// (8) Sanity: the capability gate (not the flag spelling) is what keeps a
// Worker-less environment on the synchronous path under the new default.
// ===========================================================================

test('FEAT-2326609771: default-ON still resolves to OFF the instant the Worker capability is absent', () => {
  const origWorker = (globalThis as any).Worker;
  delete (globalThis as any).Worker;
  try {
    assert.equal(typeof (globalThis as any).Worker, 'undefined', 'precondition: Worker capability removed for this assertion');
    const result = withFakeStorage(null, () => webWorkerOffloadEnabled());
    assert.equal(
      result,
      false,
      'default-ON must still resolve to OFF the instant the Worker capability itself is absent — the capability gate, not the flag value, is what keeps a Worker-less runtime on the synchronous path'
    );
  } finally {
    if (origWorker !== undefined) (globalThis as any).Worker = origWorker;
  }
});
