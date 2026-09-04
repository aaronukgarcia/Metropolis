// engine-lag-chip.test.tsx — BUG-618 (P1): component tests for the
// ALWAYS-VISIBLE TopBar engine-lag chip (src/components/TopBar.tsx's
// EngineLagChip).
//
// Covers: renders unconditionally in BOTH webworker-flag states (never gated
// by import.meta.env.DEV or the flag — the exact gap that made
// QueueDepthHud's worker line say "worker off" forever in Aaron's flag-off
// play sessions, see engineLag.ts's header), shows a live backlog reading
// fed from a REAL engineLagTracker mutation (GR#15 — never a hardcoded
// number), and expands a popover with the numeric detail on click.
//
// RED PROOF (documented, not re-run — GR#24 forbids destructive git):
// scratch-copy TopBar.tsx, wrap the <EngineLagChip /> mount site in
// `{false && <EngineLagChip />}` and the "must always render" assertions
// below go red (chip element does not exist); scratch-copy engineLag.ts and
// change `snap.backlog > 0` in the label ternary to a literal `false`
// (imagining a naive constant label) and the "reflects a real backlog"
// assertion goes red (label stays "Engine: OK" despite a fed backlog).

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

/** Minimal SimContext value — mirrors bug580-topbar-wellbeing-rag.test.tsx's
 *  ctx shape, the established "mount TopBar directly via SimContext.Provider,
 *  no full SimProvider machinery" idiom for this component. */
function makeCtx(state: any) {
  return {
    state,
    dispatch: () => {},
    cityName: 'Attackville',
    listSaves: () => [],
    listRecent: () => [],
    saveGame: async () => true,
    saveGameAs: async () => ({ ok: true }),
    loadGame: async () => {},
    loadNamed: async () => {},
    renameCity: () => ({ ok: true }),
    exportCity: async () => true,
    importCity: async () => true,
  };
}

async function mountTopBar(container: any, speedOverride?: number) {
  const React = await import('react');
  const { createRoot } = await import('react-dom/client');
  const { act } = await import('react-dom/test-utils');
  const { SimContext } = await import('../src/sim/simContext.ts');
  const { initialState } = await import('../src/sim/engine.ts');
  const { TopBar } = await import('../src/components/TopBar.tsx');

  const state = initialState();
  if (speedOverride !== undefined) state.speed = speedOverride as any;
  const ctx = makeCtx(state);
  const root = createRoot(container);
  await act(async () => {
    root.render(React.default.createElement(SimContext.Provider, { value: ctx }, React.default.createElement(TopBar)));
  });
  return { root, act };
}

test('BUG-618: engine lag chip renders with the webworker flag OFF (Aaron\'s default play mode)', async () => {
  const dom: any = await installJsdom();
  try {
    dom.window.localStorage.removeItem('metropolis.webworker');
    const { engineLagTracker } = await import('../src/sim/engineLag.ts');
    engineLagTracker.resetAll();

    const container = dom.window.document.getElementById('root');
    const { root, act } = await mountTopBar(container);

    const chip = container.querySelector('.engine-lag-chip');
    assert.ok(chip, 'the engine-lag chip must ALWAYS render, flag off or on (never dev-gated)');
    assert.match(chip!.textContent || '', /Engine:/, 'the chip must show an "Engine: ..." label');

    await act(async () => root.unmount());
    engineLagTracker.resetAll();
  } finally {
    dom.window.close();
  }
});

test('BUG-618: engine lag chip renders with the webworker flag ON', async () => {
  const dom: any = await installJsdom();
  try {
    dom.window.localStorage.setItem('metropolis.webworker', '1');
    if (typeof (globalThis as any).Worker === 'undefined') {
      (globalThis as any).Worker = class {
        postMessage() {}
        terminate() {}
        addEventListener() {}
        removeEventListener() {}
      };
    }
    const { engineLagTracker } = await import('../src/sim/engineLag.ts');
    const { webWorkerOffloadEnabled } = await import('../src/sim/webWorkerFlag.ts');
    assert.equal(webWorkerOffloadEnabled(), true, 'test precondition: flag must read enabled');
    engineLagTracker.resetAll();

    const container = dom.window.document.getElementById('root');
    const { root, act } = await mountTopBar(container);

    const chip = container.querySelector('.engine-lag-chip');
    assert.ok(chip, 'the engine-lag chip must render with the flag ON too — same component, no branch on flag state');

    await act(async () => root.unmount());
    engineLagTracker.resetAll();
  } finally {
    dom.window.close();
  }
});

test('BUG-618: chip shows "Engine: OK" (green) when the tracker is caught up', async () => {
  const dom: any = await installJsdom();
  try {
    dom.window.localStorage.removeItem('metropolis.webworker');
    const { engineLagTracker } = await import('../src/sim/engineLag.ts');
    engineLagTracker.resetAll();

    const container = dom.window.document.getElementById('root');
    const { root, act } = await mountTopBar(container);

    const chip = container.querySelector('.engine-lag-chip');
    assert.match(chip!.textContent || '', /Engine: OK/, 'a fresh, never-fed tracker must read as caught up, never a false alarm');
    assert.ok(chip!.classList.contains('green'), 'must carry the green status class');

    await act(async () => root.unmount());
    engineLagTracker.resetAll();
  } finally {
    dom.window.close();
  }
});

test('BUG-618: chip reflects a REAL backlog fed into engineLagTracker (GR#15 — never a hardcoded number)', async () => {
  const dom: any = await installJsdom();
  try {
    dom.window.localStorage.removeItem('metropolis.webworker');
    const { engineLagTracker } = await import('../src/sim/engineLag.ts');
    engineLagTracker.resetAll();
    // Feed a real backlog of 4 (scheduled 4, completed 0) BEFORE mount so the
    // very first render already reflects tracker state, then mount.
    engineLagTracker.recordTickScheduled();
    engineLagTracker.recordTickScheduled();
    engineLagTracker.recordTickScheduled();
    engineLagTracker.recordTickScheduled();

    const container = dom.window.document.getElementById('root');
    const { root, act } = await mountTopBar(container);

    const chip = container.querySelector('.engine-lag-chip');
    assert.match(chip!.textContent || '', /Engine: 4 behind/, 'the label must show the REAL fed backlog count, 4');
    assert.ok(!chip!.classList.contains('green'), 'a nonzero backlog must not read green');

    await act(async () => root.unmount());
    engineLagTracker.resetAll();
  } finally {
    dom.window.close();
  }
});

test('BUG-618: clicking the chip expands a popover with backlog/tick/interval/stall detail', async () => {
  const dom: any = await installJsdom();
  try {
    dom.window.localStorage.removeItem('metropolis.webworker');
    const { engineLagTracker } = await import('../src/sim/engineLag.ts');
    engineLagTracker.resetAll();
    engineLagTracker.setIntervalMs(1000);
    engineLagTracker.recordTickDuration(250);

    const container = dom.window.document.getElementById('root');
    const { root, act } = await mountTopBar(container);

    assert.equal(container.querySelector('.engine-lag-popover'), null, 'the popover must be closed by default');

    const chip = container.querySelector('.engine-lag-chip') as any;
    await act(async () => {
      chip.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true }));
    });

    const popover = container.querySelector('.engine-lag-popover');
    assert.ok(popover, 'clicking the chip must open the detail popover');
    assert.match(popover!.textContent || '', /Backlog/i);
    assert.match(popover!.textContent || '', /Last tick/i);
    assert.match(popover!.textContent || '', /250\.0 ms/, 'must show the real fed last-tick duration');
    assert.match(popover!.textContent || '', /Interval/i);
    assert.match(popover!.textContent || '', /1000 ms/, 'must show the real fed interval length');
    assert.match(popover!.textContent || '', /Worst stall/i);

    // Click again to collapse.
    await act(async () => {
      chip.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true }));
    });
    assert.equal(container.querySelector('.engine-lag-popover'), null, 'a second click must collapse the popover');

    await act(async () => root.unmount());
    engineLagTracker.resetAll();
  } finally {
    dom.window.close();
  }
});

test('BUG-618: a stalled tracker shows "Engine: stalled X.Xs" and turns red', async () => {
  const dom: any = await installJsdom();
  try {
    dom.window.localStorage.removeItem('metropolis.webworker');
    const { engineLagTracker } = await import('../src/sim/engineLag.ts');
    engineLagTracker.resetAll();
    // Fed at the REAL current performance.now() — the chip's own snapshot()
    // calls use the real clock (nowMs() in TopBar.tsx), so a stall recorded
    // against a fabricated t=0 would read as ancient (past STALL_DISPLAY_MS)
    // the instant the component snapshots at mount time.
    engineLagTracker.recordFrameGap(4200, performance.now());

    const container = dom.window.document.getElementById('root');
    const { root, act } = await mountTopBar(container);

    const chip = container.querySelector('.engine-lag-chip');
    assert.match(chip!.textContent || '', /Engine: stalled 4\.2s/, 'a recently recorded stall must render as "stalled X.Xs"');
    assert.ok(chip!.classList.contains('red'), 'a stall must render red regardless of backlog/ratio state');

    await act(async () => root.unmount());
    engineLagTracker.resetAll();
  } finally {
    dom.window.close();
  }
});

// ============================================================================
// F1 fix (independent round REJECT, 2026-09-03) — "the killer, pause
// honesty": a nonzero backlog left over from the instant before Pause (e.g.
// a drag-supersede burst right before the player hit Pause) must NEVER
// display "Engine: N behind" forever while paused — a paused engine cannot
// be "behind", nothing is being asked of it. These tests mount with
// state.speed === 0 directly (never calling engineLagTracker.settle()
// themselves) to prove the CHIP ITSELF is honest about pause, independent of
// whether store.tsx's settle() call has already landed (it fires from a
// useEffect, which commits after paint — see the component's own header
// comment on why the UI-level override exists at all).
// ============================================================================

test('BUG-618 F1: paused (speed 0) with a real leftover backlog shows "Engine: paused", never "N behind"', async () => {
  const dom: any = await installJsdom();
  try {
    dom.window.localStorage.removeItem('metropolis.webworker');
    const { engineLagTracker } = await import('../src/sim/engineLag.ts');
    engineLagTracker.resetAll();
    // Simulate the drag-supersede burst leaving a real backlog right before
    // pause — deliberately NOT calling settle() here, so this test proves
    // the CHIP's own pause-honesty override, not store.tsx's settle() wiring
    // (that is covered separately by the settle() unit tests and the F3
    // integration test).
    engineLagTracker.recordTickScheduled();
    engineLagTracker.recordTickScheduled();
    engineLagTracker.recordTickScheduled();
    engineLagTracker.recordTickScheduled();
    assert.equal(engineLagTracker.snapshot(performance.now()).backlog, 4, 'precondition: a real backlog exists');

    const container = dom.window.document.getElementById('root');
    const { root, act } = await mountTopBar(container, 0); // speed 0 = paused

    const chip = container.querySelector('.engine-lag-chip');
    assert.match(
      chip!.textContent || '',
      /Engine: paused/,
      'paused with a leftover backlog must read "Engine: paused" IMMEDIATELY, never "Engine: 4 behind"'
    );
    assert.doesNotMatch(chip!.textContent || '', /behind/, 'the word "behind" must never appear while paused');
    assert.ok(chip!.classList.contains('green'), 'paused must render green, not amber/red from the leftover backlog');

    await act(async () => root.unmount());
    engineLagTracker.resetAll();
  } finally {
    dom.window.close();
  }
});

test('BUG-618 F1: a genuine stall detected WHILE paused still overrides to "stalled", not suppressed by pause', async () => {
  const dom: any = await installJsdom();
  try {
    dom.window.localStorage.removeItem('metropolis.webworker');
    const { engineLagTracker } = await import('../src/sim/engineLag.ts');
    engineLagTracker.resetAll();
    engineLagTracker.recordFrameGap(4200, performance.now());

    const container = dom.window.document.getElementById('root');
    const { root, act } = await mountTopBar(container, 0); // speed 0 = paused

    const chip = container.querySelector('.engine-lag-chip');
    assert.match(
      chip!.textContent || '',
      /Engine: stalled 4\.2s/,
      'a real thread-block stall must still be reported while paused — it is a fact about the main thread, not the sim clock'
    );
    assert.ok(chip!.classList.contains('red'));

    await act(async () => root.unmount());
    engineLagTracker.resetAll();
  } finally {
    dom.window.close();
  }
});

test('BUG-618 F1: resuming from pause (speed back to nonzero) reads OK once the tracker has settled', async () => {
  const dom: any = await installJsdom();
  try {
    dom.window.localStorage.removeItem('metropolis.webworker');
    const { engineLagTracker } = await import('../src/sim/engineLag.ts');
    engineLagTracker.resetAll();
    // Leave a leftover backlog, as if paused mid-burst...
    engineLagTracker.recordTickScheduled();
    engineLagTracker.recordTickScheduled();
    engineLagTracker.recordTickScheduled();
    // ...then settle (what store.tsx's tick-driver effect does on entering
    // pause) BEFORE mounting at a resumed, nonzero speed — proving that once
    // settled, resuming reads a clean OK rather than carrying the pre-pause
    // deficit forward.
    engineLagTracker.settle();

    const container = dom.window.document.getElementById('root');
    const { root, act } = await mountTopBar(container, 1); // resumed

    const chip = container.querySelector('.engine-lag-chip');
    assert.match(chip!.textContent || '', /Engine: OK/, 'post-settle + resume must read OK, not carry forward the pre-pause backlog');
    assert.ok(chip!.classList.contains('green'));

    await act(async () => root.unmount());
    engineLagTracker.resetAll();
  } finally {
    dom.window.close();
  }
});
