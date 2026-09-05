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

// BUG-721 (P2): the 500ms setInterval this file's tests mount (TopBar.tsx's
// EngineLagChip) used to bind the bare global `setInterval`, which under
// tsx/jsdom resolves to NODE's own timer, not the jsdom window's — so
// dom.window.close() had no power to stop a leaked interval, and a test that
// threw before root.unmount() (skipping the effect cleanup that would have
// cleared it) hung the runner forever (exit 124) instead of reporting a
// failure. Fixed at both ends:
//   (1) THE COMPONENT — src/components/TopBar.tsx's EngineLagChip now binds
//       window.setInterval/window.clearInterval (unref'd as a backstop), so
//       the timer belongs to the window this file's `installJsdom()` creates
//       and tears down. See the BUG-721 comments in TopBar.tsx and the other
//       webconsole/src files audited/fixed alongside it (consolidatorTab.tsx,
//       debugTab.tsx, QueueDepthHud.tsx, StaleBuildBanner.tsx,
//       sim/liveVersion.tsx, sim/store.tsx's autosave/consolidator-audit/
//       tick-driver intervals — the same bare-global-timer-in-a-mount-effect
//       shape, all reused across many jsdom-based tests in this suite).
//   (2) THE HARNESS — every test below now: (a) attempts root.unmount() a
//       SECOND time inside its outer `finally` (best-effort, errors
//       swallowed) so a throw from an assertion no longer skips the
//       component's cleanup effects entirely — previously only
//       dom.window.close() ran on a throw, never unmount(); and (b) carries a
//       hard per-test wall-clock deadline (node:test's own `timeout` option,
//       HARD_DEADLINE_MS below).
//
//       CORRECTED (opus-round-bug721 P2): `timeout` only makes node:test
//       CANCEL/report that one test as failed once the deadline elapses — it
//       does NOT free the process of a handle a leaked bare-global timer is
//       still holding. An attacker proved directly (reverting both the
//       component AND its test-side spy back to the pre-BUG-721 shape) that
//       `exit 124` still occurs under the deadline when the LEAK is real —
//       the deadline turns "hangs forever" into "hangs for
//       HARD_DEADLINE_MS then the outer runner's own `--test-force-exit`
//       (tools/test/scoped.mjs, BUG-599) has to reap it", not into "exits
//       cleanly on its own". THE COMPONENT FIX (1) is therefore the
//       load-bearing half of this bug: only a window-bound, `window.close()`-
//       stoppable timer actually prevents the leak from existing in the
//       first place. The per-test deadline is still worth keeping as
//       defense in depth (it bounds how long any single regression can stall
//       a run, and reports it as a clearly-attributed FAILURE rather than an
//       anonymous group timeout), but it is not what makes a leak-free
//       process happen.
//
// The RED-PROOF tests at the bottom of this file (search "BUG-721
// RED-PROOF") reproduce the OLD shape directly: the first proves a throw
// before unmount still reports FAILURE promptly (never silently hangs, given
// the component fix in place); the second proves, against the REAL
// production source via testsupport/mutant.mjs, that the FIXED component
// leaves zero live Timeout resources after a throw-before-unmount while a
// mutant reverting to the bare-global timer leaks exactly one.
const HARD_DEADLINE_MS = 15_000;

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

test('BUG-618: engine lag chip renders with the webworker flag OFF (Aaron\'s default play mode)', { timeout: HARD_DEADLINE_MS }, async () => {
  const dom: any = await installJsdom();
  let root: any = null;
  let act: any = null;
  let engineLagTracker: any = null;
  try {
    dom.window.localStorage.removeItem('metropolis.webworker');
    ({ engineLagTracker } = await import('../src/sim/engineLag.ts'));
    engineLagTracker.resetAll();

    const container = dom.window.document.getElementById('root');
    ({ root, act } = await mountTopBar(container));

    const chip = container.querySelector('.engine-lag-chip');
    assert.ok(chip, 'the engine-lag chip must ALWAYS render, flag off or on (never dev-gated)');
    assert.match(chip!.textContent || '', /Engine:/, 'the chip must show an "Engine: ..." label');

    await act(async () => root.unmount());
    engineLagTracker.resetAll();
  } finally {
    // BUG-721 harness fix: unmount is now ALSO attempted here (best-effort,
    // errors swallowed so they never mask a real assertion failure above) —
    // a throw from an assertion earlier in the try block used to skip
    // root.unmount() entirely, so the mounted component's cleanup effects
    // (clearInterval) never ran; only dom.window.close() below fired. Under
    // the OLD (pre-BUG-721) component shape, whose interval bound to the
    // wrong (Node, not jsdom-window) global, window.close() had no power to
    // stop that leaked timer at all — this is the exact "throws before
    // unmount hangs the runner forever" shape BUG-721 fixes at both ends.
    try {
      await act(async () => root.unmount());
    } catch {
      /* best-effort re-attempt only; the original failure above still reports */
    }
    try {
      engineLagTracker.resetAll();
    } catch {
      /* module may not have imported yet on an early-throwing path */
    }
    dom.window.close();
  }
});

test('BUG-618: engine lag chip renders with the webworker flag ON', { timeout: HARD_DEADLINE_MS }, async () => {
  const dom: any = await installJsdom();
  let root: any = null;
  let act: any = null;
  let engineLagTracker: any = null;
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
    ({ engineLagTracker } = await import('../src/sim/engineLag.ts'));
    const { webWorkerOffloadEnabled } = await import('../src/sim/webWorkerFlag.ts');
    assert.equal(webWorkerOffloadEnabled(), true, 'test precondition: flag must read enabled');
    engineLagTracker.resetAll();

    const container = dom.window.document.getElementById('root');
    ({ root, act } = await mountTopBar(container));

    const chip = container.querySelector('.engine-lag-chip');
    assert.ok(chip, 'the engine-lag chip must render with the flag ON too — same component, no branch on flag state');

    await act(async () => root.unmount());
    engineLagTracker.resetAll();
  } finally {
    // BUG-721 harness fix: unmount is now ALSO attempted here (best-effort,
    // errors swallowed so they never mask a real assertion failure above) —
    // a throw from an assertion earlier in the try block used to skip
    // root.unmount() entirely, so the mounted component's cleanup effects
    // (clearInterval) never ran; only dom.window.close() below fired. Under
    // the OLD (pre-BUG-721) component shape, whose interval bound to the
    // wrong (Node, not jsdom-window) global, window.close() had no power to
    // stop that leaked timer at all — this is the exact "throws before
    // unmount hangs the runner forever" shape BUG-721 fixes at both ends.
    try {
      await act(async () => root.unmount());
    } catch {
      /* best-effort re-attempt only; the original failure above still reports */
    }
    try {
      engineLagTracker.resetAll();
    } catch {
      /* module may not have imported yet on an early-throwing path */
    }
    dom.window.close();
  }
});

test('BUG-618: chip shows "Engine: OK" (green) when the tracker is caught up', { timeout: HARD_DEADLINE_MS }, async () => {
  const dom: any = await installJsdom();
  let root: any = null;
  let act: any = null;
  let engineLagTracker: any = null;
  try {
    dom.window.localStorage.removeItem('metropolis.webworker');
    ({ engineLagTracker } = await import('../src/sim/engineLag.ts'));
    engineLagTracker.resetAll();

    const container = dom.window.document.getElementById('root');
    ({ root, act } = await mountTopBar(container));

    const chip = container.querySelector('.engine-lag-chip');
    assert.match(chip!.textContent || '', /Engine: OK/, 'a fresh, never-fed tracker must read as caught up, never a false alarm');
    assert.ok(chip!.classList.contains('green'), 'must carry the green status class');

    await act(async () => root.unmount());
    engineLagTracker.resetAll();
  } finally {
    // BUG-721 harness fix: unmount is now ALSO attempted here (best-effort,
    // errors swallowed so they never mask a real assertion failure above) —
    // a throw from an assertion earlier in the try block used to skip
    // root.unmount() entirely, so the mounted component's cleanup effects
    // (clearInterval) never ran; only dom.window.close() below fired. Under
    // the OLD (pre-BUG-721) component shape, whose interval bound to the
    // wrong (Node, not jsdom-window) global, window.close() had no power to
    // stop that leaked timer at all — this is the exact "throws before
    // unmount hangs the runner forever" shape BUG-721 fixes at both ends.
    try {
      await act(async () => root.unmount());
    } catch {
      /* best-effort re-attempt only; the original failure above still reports */
    }
    try {
      engineLagTracker.resetAll();
    } catch {
      /* module may not have imported yet on an early-throwing path */
    }
    dom.window.close();
  }
});

test('BUG-618: chip reflects a REAL backlog fed into engineLagTracker (GR#15 — never a hardcoded number)', { timeout: HARD_DEADLINE_MS }, async () => {
  const dom: any = await installJsdom();
  let root: any = null;
  let act: any = null;
  let engineLagTracker: any = null;
  try {
    dom.window.localStorage.removeItem('metropolis.webworker');
    ({ engineLagTracker } = await import('../src/sim/engineLag.ts'));
    engineLagTracker.resetAll();
    // Feed a real backlog of 4 (scheduled 4, completed 0) BEFORE mount so the
    // very first render already reflects tracker state, then mount.
    engineLagTracker.recordTickScheduled();
    engineLagTracker.recordTickScheduled();
    engineLagTracker.recordTickScheduled();
    engineLagTracker.recordTickScheduled();

    const container = dom.window.document.getElementById('root');
    ({ root, act } = await mountTopBar(container));

    const chip = container.querySelector('.engine-lag-chip');
    assert.match(chip!.textContent || '', /Engine: 4 behind/, 'the label must show the REAL fed backlog count, 4');
    assert.ok(!chip!.classList.contains('green'), 'a nonzero backlog must not read green');

    await act(async () => root.unmount());
    engineLagTracker.resetAll();
  } finally {
    // BUG-721 harness fix: unmount is now ALSO attempted here (best-effort,
    // errors swallowed so they never mask a real assertion failure above) —
    // a throw from an assertion earlier in the try block used to skip
    // root.unmount() entirely, so the mounted component's cleanup effects
    // (clearInterval) never ran; only dom.window.close() below fired. Under
    // the OLD (pre-BUG-721) component shape, whose interval bound to the
    // wrong (Node, not jsdom-window) global, window.close() had no power to
    // stop that leaked timer at all — this is the exact "throws before
    // unmount hangs the runner forever" shape BUG-721 fixes at both ends.
    try {
      await act(async () => root.unmount());
    } catch {
      /* best-effort re-attempt only; the original failure above still reports */
    }
    try {
      engineLagTracker.resetAll();
    } catch {
      /* module may not have imported yet on an early-throwing path */
    }
    dom.window.close();
  }
});

test('BUG-618: clicking the chip expands a popover with backlog/tick/interval/stall detail', { timeout: HARD_DEADLINE_MS }, async () => {
  const dom: any = await installJsdom();
  let root: any = null;
  let act: any = null;
  let engineLagTracker: any = null;
  try {
    dom.window.localStorage.removeItem('metropolis.webworker');
    ({ engineLagTracker } = await import('../src/sim/engineLag.ts'));
    engineLagTracker.resetAll();
    engineLagTracker.setIntervalMs(1000);
    engineLagTracker.recordTickDuration(250);

    const container = dom.window.document.getElementById('root');
    ({ root, act } = await mountTopBar(container));

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
    // BUG-721 harness fix: unmount is now ALSO attempted here (best-effort,
    // errors swallowed so they never mask a real assertion failure above) —
    // a throw from an assertion earlier in the try block used to skip
    // root.unmount() entirely, so the mounted component's cleanup effects
    // (clearInterval) never ran; only dom.window.close() below fired. Under
    // the OLD (pre-BUG-721) component shape, whose interval bound to the
    // wrong (Node, not jsdom-window) global, window.close() had no power to
    // stop that leaked timer at all — this is the exact "throws before
    // unmount hangs the runner forever" shape BUG-721 fixes at both ends.
    try {
      await act(async () => root.unmount());
    } catch {
      /* best-effort re-attempt only; the original failure above still reports */
    }
    try {
      engineLagTracker.resetAll();
    } catch {
      /* module may not have imported yet on an early-throwing path */
    }
    dom.window.close();
  }
});

test('BUG-618: a stalled tracker shows "Engine: stalled X.Xs" and turns red', { timeout: HARD_DEADLINE_MS }, async () => {
  const dom: any = await installJsdom();
  let root: any = null;
  let act: any = null;
  let engineLagTracker: any = null;
  try {
    dom.window.localStorage.removeItem('metropolis.webworker');
    ({ engineLagTracker } = await import('../src/sim/engineLag.ts'));
    engineLagTracker.resetAll();
    // Fed at the REAL current performance.now() — the chip's own snapshot()
    // calls use the real clock (nowMs() in TopBar.tsx), so a stall recorded
    // against a fabricated t=0 would read as ancient (past STALL_DISPLAY_MS)
    // the instant the component snapshots at mount time.
    engineLagTracker.recordFrameGap(4200, performance.now());

    const container = dom.window.document.getElementById('root');
    ({ root, act } = await mountTopBar(container));

    const chip = container.querySelector('.engine-lag-chip');
    assert.match(chip!.textContent || '', /Engine: stalled 4\.2s/, 'a recently recorded stall must render as "stalled X.Xs"');
    assert.ok(chip!.classList.contains('red'), 'a stall must render red regardless of backlog/ratio state');

    await act(async () => root.unmount());
    engineLagTracker.resetAll();
  } finally {
    // BUG-721 harness fix: unmount is now ALSO attempted here (best-effort,
    // errors swallowed so they never mask a real assertion failure above) —
    // a throw from an assertion earlier in the try block used to skip
    // root.unmount() entirely, so the mounted component's cleanup effects
    // (clearInterval) never ran; only dom.window.close() below fired. Under
    // the OLD (pre-BUG-721) component shape, whose interval bound to the
    // wrong (Node, not jsdom-window) global, window.close() had no power to
    // stop that leaked timer at all — this is the exact "throws before
    // unmount hangs the runner forever" shape BUG-721 fixes at both ends.
    try {
      await act(async () => root.unmount());
    } catch {
      /* best-effort re-attempt only; the original failure above still reports */
    }
    try {
      engineLagTracker.resetAll();
    } catch {
      /* module may not have imported yet on an early-throwing path */
    }
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

test('BUG-618 F1: paused (speed 0) with a real leftover backlog shows "Engine: paused", never "N behind"', { timeout: HARD_DEADLINE_MS }, async () => {
  const dom: any = await installJsdom();
  let root: any = null;
  let act: any = null;
  let engineLagTracker: any = null;
  try {
    dom.window.localStorage.removeItem('metropolis.webworker');
    ({ engineLagTracker } = await import('../src/sim/engineLag.ts'));
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
    ({ root, act } = await mountTopBar(container, 0)); // speed 0 = paused

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
    // BUG-721 harness fix: unmount is now ALSO attempted here (best-effort,
    // errors swallowed so they never mask a real assertion failure above) —
    // a throw from an assertion earlier in the try block used to skip
    // root.unmount() entirely, so the mounted component's cleanup effects
    // (clearInterval) never ran; only dom.window.close() below fired. Under
    // the OLD (pre-BUG-721) component shape, whose interval bound to the
    // wrong (Node, not jsdom-window) global, window.close() had no power to
    // stop that leaked timer at all — this is the exact "throws before
    // unmount hangs the runner forever" shape BUG-721 fixes at both ends.
    try {
      await act(async () => root.unmount());
    } catch {
      /* best-effort re-attempt only; the original failure above still reports */
    }
    try {
      engineLagTracker.resetAll();
    } catch {
      /* module may not have imported yet on an early-throwing path */
    }
    dom.window.close();
  }
});

test('BUG-618 F1: a genuine stall detected WHILE paused still overrides to "stalled", not suppressed by pause', { timeout: HARD_DEADLINE_MS }, async () => {
  const dom: any = await installJsdom();
  let root: any = null;
  let act: any = null;
  let engineLagTracker: any = null;
  try {
    dom.window.localStorage.removeItem('metropolis.webworker');
    ({ engineLagTracker } = await import('../src/sim/engineLag.ts'));
    engineLagTracker.resetAll();
    engineLagTracker.recordFrameGap(4200, performance.now());

    const container = dom.window.document.getElementById('root');
    ({ root, act } = await mountTopBar(container, 0)); // speed 0 = paused

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
    // BUG-721 harness fix: unmount is now ALSO attempted here (best-effort,
    // errors swallowed so they never mask a real assertion failure above) —
    // a throw from an assertion earlier in the try block used to skip
    // root.unmount() entirely, so the mounted component's cleanup effects
    // (clearInterval) never ran; only dom.window.close() below fired. Under
    // the OLD (pre-BUG-721) component shape, whose interval bound to the
    // wrong (Node, not jsdom-window) global, window.close() had no power to
    // stop that leaked timer at all — this is the exact "throws before
    // unmount hangs the runner forever" shape BUG-721 fixes at both ends.
    try {
      await act(async () => root.unmount());
    } catch {
      /* best-effort re-attempt only; the original failure above still reports */
    }
    try {
      engineLagTracker.resetAll();
    } catch {
      /* module may not have imported yet on an early-throwing path */
    }
    dom.window.close();
  }
});

test('BUG-618 F1: resuming from pause (speed back to nonzero) reads OK once the tracker has settled', { timeout: HARD_DEADLINE_MS }, async () => {
  const dom: any = await installJsdom();
  let root: any = null;
  let act: any = null;
  let engineLagTracker: any = null;
  try {
    dom.window.localStorage.removeItem('metropolis.webworker');
    ({ engineLagTracker } = await import('../src/sim/engineLag.ts'));
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
    ({ root, act } = await mountTopBar(container, 1)); // resumed

    const chip = container.querySelector('.engine-lag-chip');
    assert.match(chip!.textContent || '', /Engine: OK/, 'post-settle + resume must read OK, not carry forward the pre-pause backlog');
    assert.ok(chip!.classList.contains('green'));

    await act(async () => root.unmount());
    engineLagTracker.resetAll();
  } finally {
    // BUG-721 harness fix: unmount is now ALSO attempted here (best-effort,
    // errors swallowed so they never mask a real assertion failure above) —
    // a throw from an assertion earlier in the try block used to skip
    // root.unmount() entirely, so the mounted component's cleanup effects
    // (clearInterval) never ran; only dom.window.close() below fired. Under
    // the OLD (pre-BUG-721) component shape, whose interval bound to the
    // wrong (Node, not jsdom-window) global, window.close() had no power to
    // stop that leaked timer at all — this is the exact "throws before
    // unmount hangs the runner forever" shape BUG-721 fixes at both ends.
    try {
      await act(async () => root.unmount());
    } catch {
      /* best-effort re-attempt only; the original failure above still reports */
    }
    try {
      engineLagTracker.resetAll();
    } catch {
      /* module may not have imported yet on an early-throwing path */
    }
    dom.window.close();
  }
});

// ============================================================================
// BUG-721 RED-PROOF — reproduces the OLD component shape directly (a
// mount-effect interval bound to the BARE global setInterval, never
// window.setInterval) and proves the fix at both ends: the harness reports a
// FAILURE within the deadline instead of hanging, and the FIXED component
// leaves no live timer handle behind after a normal unmount.
// ============================================================================

let bug721LeakedOldShapeId: ReturnType<typeof setInterval> | null = null;

/** Simulates the OLD (pre-BUG-721) EngineLagChip shape: a mount-effect
 *  interval bound to the BARE global setInterval/clearInterval (never
 *  window.setInterval — exactly the defect BUG-721 fixes). Mounts it, then
 *  deliberately throws BEFORE calling root.unmount() — the precise "a test
 *  throws before unmount" shape that used to hang the runner forever,
 *  because the leaked interval belongs to Node's own global timer table,
 *  not the jsdom window this function's own dom.window.close() (in its
 *  finally) tears down. */
async function throwBeforeUnmountUnderOldShape(): Promise<void> {
  const dom: any = await installJsdom();
  try {
    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');

    function OldShapeChip() {
      const [, setTick] = React.default.useState(0);
      React.default.useEffect(() => {
        // THE OLD BUG SHAPE: the bare global, not window-bound — under
        // tsx/jsdom this resolves to Node's own timer.
        const id = setInterval(() => setTick((n: number) => n + 1), 20);
        bug721LeakedOldShapeId = id;
        return () => clearInterval(id);
      }, []);
      return null;
    }

    const container = dom.window.document.getElementById('root');
    const root = createRoot(container);
    await act(async () => {
      root.render(React.default.createElement(OldShapeChip));
    });

    // Deliberately throw BEFORE root.unmount() — the exact shape that used
    // to skip the component's cleanup effect entirely (only this function's
    // own dom.window.close() below would run).
    throw new Error('BUG-721 RED-PROOF: simulated assertion failure before unmount');
  } finally {
    dom.window.close();
  }
}

test(
  'BUG-721 RED-PROOF: a throw before unmount under the OLD bare-global-timer shape still reports FAILURE within the deadline, not a hang',
  { timeout: HARD_DEADLINE_MS },
  async () => {
    const startedAt = Date.now();
    await assert.rejects(
      throwBeforeUnmountUnderOldShape(),
      /simulated assertion failure before unmount/,
      'the harness must propagate the original failure, never swallow it'
    );
    const elapsedMs = Date.now() - startedAt;
    assert.ok(
      elapsedMs < HARD_DEADLINE_MS,
      `must resolve well within the ${HARD_DEADLINE_MS}ms hard deadline — took ${elapsedMs}ms. This proves the ` +
        'promise itself settles promptly (the throw propagates, the finally runs) — it does NOT by itself prove ' +
        'the PROCESS can exit: node:test\'s `timeout` option only cancels/reports one test, it does not free a ' +
        'handle a leaked bare-global timer still holds (see the header comment above and the mutant-based ' +
        'second RED-PROOF below, which is what actually proves the component fix closes that hole).'
    );

    // Confirm the OLD shape genuinely DID leak — a bare-global timer that
    // dom.window.close() (already run, above, inside the throwing helper's
    // own finally) could not stop — proving why the COMPONENT fix (not just
    // this harness fix) is load-bearing. Then sweep it directly (real
    // clearInterval, mirroring exactly how a genuinely leaked pre-fix timer
    // would have to be cleaned up) so this RED-PROOF never itself leaves a
    // stray interval running for the rest of the suite/process.
    assert.ok(
      bug721LeakedOldShapeId !== null,
      'precondition: the old-shape effect must have armed its interval before the throw'
    );
    clearInterval(bug721LeakedOldShapeId!);
    bug721LeakedOldShapeId = null;
  }
);

// opus-round-bug721 P2 (1): the original version of this second RED-PROOF
// was VACUOUS — it preferred `process._getActiveHandles()`, which on this
// Node version (25.3) reports ZERO Timeout handles even while a real
// interval is live (`process.getActiveResourcesInfo()` correctly reports
// one), so `before` and `after` were always both 0 and the `after <= before`
// assertion could never fail. It also only ever exercised the CLEAN-unmount
// path, which the OLD (pre-BUG-721) component already handled correctly —
// clearInterval on a normal unmount clears a bare-global timer exactly as
// well as a window-bound one. The defect BUG-721 fixes only shows up on the
// LEAK path: a throw BEFORE unmount, which skips the cleanup effect
// entirely and leaves whichever kind of timer was armed to survive
// `dom.window.close()` on its own.
//
// CORRECTED PROOF: `process.getActiveResourcesInfo()` only (documented,
// stable since Node 19 — no `_getActiveHandles()` fallback, which is what
// made the vacuity possible), and the LEAK path against the REAL production
// source via testsupport/mutant.mjs (GR#24 — mutation only through that
// helper, real webconsole/src is never touched): mount the real
// `EngineLagChip` from a disposable shadow copy of webconsole/src, throw
// BEFORE calling root.unmount() (skipping its cleanup effect entirely, the
// precise "a test throws before unmount" shape), close the window in a
// `finally`, then count live `Timeout` resources.
//   - Against the UNMUTATED (current, FIXED) source: 0 Timeout resources —
//     the interval is `window`-bound, so `window.close()` (already run,
//     inside the probe's own finally) tears it down even though the
//     cleanup effect that would normally clearInterval it never ran.
//   - Against a MUTANT that reverts EngineLagChip's interval back to the
//     bare global `setInterval`/`clearInterval` (the OLD, pre-BUG-721
//     shape): 1 Timeout resource survives — `window.close()` has no power
//     over a timer bound to Node's own global timer table.
//
// Mounting real React inside a mutant.mjs shadow child needs care: the
// shadow lives under `os.tmpdir()`, so a bare ESM `import 'react'` inside
// the shadow copy of TopBar.tsx cannot resolve (no ancestor `node_modules`)
// — confirmed by direct experiment before writing this test. The fix is
// `createRequire(import.meta.url)` inside the child (CJS `require`, unlike
// ESM `import`, DOES fall back to `NODE_PATH`) with `NODE_PATH` pointed at
// the real `webconsole/node_modules` (set on `process.env` immediately
// before the mutant.mjs call, restored in a `finally` — `execFileSync`
// inherits the parent's env by default) — verified directly (react,
// react-dom/client, react-dom/test-utils, jsdom, and a shadow-relative
// `require('./components/TopBar.tsx')` all resolve this way) before this
// test was written to depend on it.
import { runWithMutant, runBaselineProbe } from '../testsupport/mutant.mjs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const REAL_NODE_MODULES = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', 'node_modules');
// Resolved from THIS file's own location (webconsole/test/…), which has
// webconsole/node_modules as an ancestor — matches the existing
// attack-bug641-round2.test.tsx idiom for handing a tsx loader to a
// mutant.mjs child.
const TSX_LOADER_URL = import.meta.resolve('tsx');

/** Child-process probe body (plain JS, CJS `require` via createRequire —
 *  see the comment above): mounts the real `EngineLagChip` (speed=1, so the
 *  tick-cost/backlog paths are live), throws BEFORE `root.unmount()` (the
 *  exact "a test throws before unmount" shape), closes the jsdom window in
 *  a `finally` exactly like this file's own harness fix does, then reports
 *  the count of live `Timeout` resources as `RESULT:<json>` on stdout so the
 *  parent test can parse it out of execFileSync's captured output. Always
 *  calls `process.exit(0)` at the end so a genuinely leaked (mutant) timer
 *  can never itself keep the child process alive waiting to be reaped. */
const ENGINE_LAG_LEAK_PROBE_BODY = `
import { createRequire } from 'node:module';
const require = createRequire(import.meta.url);
const { JSDOM } = require('jsdom');

const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', {
  url: 'http://localhost/',
  pretendToBeVisual: true,
});
const { window } = dom;
globalThis.window = window;
globalThis.document = window.document;
Object.defineProperty(globalThis, 'navigator', { value: window.navigator, configurable: true, writable: true });
globalThis.HTMLElement = window.HTMLElement;
globalThis.IS_REACT_ACT_ENVIRONMENT = true;

const React = require('react');
// tsx has no tsconfig.json to read from this shadow's cwd, so it falls back
// to the CLASSIC JSX transform (expects a global \`React\` in scope) rather
// than the project's real "jsx": "react-jsx" automatic runtime — TopBar.tsx
// only imports named hooks from 'react' (never the default), so without this
// its own JSX in EngineLagChip's render throws "ReferenceError: React is not
// defined" (caught directly before this line was added).
globalThis.React = React;
const { createRoot } = require('react-dom/client');
const { act } = require('react-dom/test-utils');
const { EngineLagChip } = require('./components/TopBar.tsx');

async function main() {
  const container = window.document.getElementById('root');
  const root = createRoot(container);
  try {
    await act(async () => {
      root.render(React.createElement(EngineLagChip, { speed: 1 }));
    });
    // Deliberately throw BEFORE root.unmount() — skips the cleanup effect
    // (clearInterval) entirely, exactly the shape that used to hang a real
    // test runner forever under the OLD bare-global-timer component.
    throw new Error('BUG-721 LEAK PROBE: simulated throw before unmount');
  } catch {
    // Swallow — this probe measures leaked handles, not the thrown error
    // (that propagation path is already covered by the first RED-PROOF
    // above, against the real jsdom/node:test harness).
  } finally {
    dom.window.close();
  }

  // One macrotask turn so anything already queued settles before counting.
  await new Promise((resolve) => setTimeout(resolve, 0));
  const timeoutCount = process.getActiveResourcesInfo().filter((r) => r === 'Timeout').length;
  process.stdout.write('RESULT:' + JSON.stringify({ timeoutCount }) + '\\n');
  process.exit(0);
}
main();
`;

function parseLeakProbeOutput(out: string): number {
  const match = /RESULT:(\{.*\})/.exec(out);
  assert.ok(match, `leak probe must print its RESULT line — got:\n${out}`);
  const parsed = JSON.parse(match![1]);
  assert.equal(typeof parsed.timeoutCount, 'number', `leak probe RESULT must carry a numeric timeoutCount — got:\n${out}`);
  return parsed.timeoutCount;
}

test(
  'BUG-721 RED-PROOF: a throw-before-unmount leaves zero Timeout resources on the FIXED source, but one on a mutant reverting the bare-global timer',
  { timeout: HARD_DEADLINE_MS },
  async () => {
    const originalNodePath = process.env.NODE_PATH;
    process.env.NODE_PATH = REAL_NODE_MODULES;
    try {
      // (a) Baseline: the CURRENT (fixed) source, unmutated — window-bound,
      // so window.close() (run inside the probe's own finally, BEFORE
      // unmount ever gets a chance to run) tears the interval down anyway.
      const fixedOut = runBaselineProbe({
        targetRelPath: 'components/TopBar.tsx',
        childBody: ENGINE_LAG_LEAK_PROBE_BODY,
        extraArgs: ['--import', TSX_LOADER_URL],
        timeoutMs: 30_000,
      });
      const fixedTimeoutCount = parseLeakProbeOutput(fixedOut);
      assert.equal(
        fixedTimeoutCount,
        0,
        `the FIXED (window-bound) EngineLagChip must leave ZERO live Timeout resources after a throw-before-unmount — got ${fixedTimeoutCount}. ` +
          'window.close() must be able to stop this interval even though the cleanup effect never ran.'
      );

      // (b) Mutant: revert the interval back to the bare global
      // setInterval/clearInterval (the OLD, pre-BUG-721 shape) — real
      // production file, mutated only inside a disposable shadow copy via
      // testsupport/mutant.mjs, never the real tree.
      const mutantOut = runWithMutant({
        targetRelPath: 'components/TopBar.tsx',
        mutate: (original: string) => {
          const fixedShape =
            "  useEffect(() => {\n" +
            "    const id = window.setInterval(() => setSnapshot(engineLagTracker.snapshot(nowMs())), 500);\n" +
            "    (id as unknown as { unref?: () => void })?.unref?.();\n" +
            "    return () => window.clearInterval(id);\n" +
            "  }, []);";
          const oldShape =
            "  useEffect(() => {\n" +
            "    const id = setInterval(() => setSnapshot(engineLagTracker.snapshot(nowMs())), 500);\n" +
            "    return () => clearInterval(id);\n" +
            "  }, []);";
          assert.ok(
            original.includes(fixedShape),
            'precondition: the BUG-721-fixed EngineLagChip interval effect must be found verbatim in TopBar.tsx — RED-PROOF setup is broken if this fails (e.g. the fix was refactored and this test was not updated to match)'
          );
          return original.replace(fixedShape, oldShape);
        },
        childBody: ENGINE_LAG_LEAK_PROBE_BODY,
        extraArgs: ['--import', TSX_LOADER_URL],
        timeoutMs: 30_000,
      });
      const mutantTimeoutCount = parseLeakProbeOutput(mutantOut);
      assert.equal(
        mutantTimeoutCount,
        1,
        `the MUTANT (bare-global-timer, pre-BUG-721 shape) must leak exactly ONE live Timeout resource after the same throw-before-unmount — got ${mutantTimeoutCount}. ` +
          'window.close() has no power over a timer bound to Node\'s own global timer table, which is the entire BUG-721 defect this test proves the fix for.'
      );
    } finally {
      if (originalNodePath === undefined) delete process.env.NODE_PATH;
      else process.env.NODE_PATH = originalNodePath;
    }
  }
);
