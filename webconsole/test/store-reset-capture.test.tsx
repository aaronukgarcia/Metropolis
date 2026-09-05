// store-reset-capture.test.tsx — BUG-437 round-r1 BAR-1(b) integration coverage.
//
// r1 REJECTed: attemptWipe (webconsole/src/sim/captureBeforeWipe.ts) was covered
// only at the unit level (captureBeforeWipe.ts's own module), never through the
// ACTUAL store dispatch path wired at store.tsx:319 (wrappedDispatch's `reset`
// branch). This mounts SimProvider with react-dom/client + jsdom (the pattern
// proven out in store-dispatch.test.tsx — SSR renderToString never re-renders,
// so effects/state updates must run under a real client root) and drives a full
// reset dispatch through the real component tree with a rigged localStorage.
//
// Covers:
//   1. localStorage rigged to throw ONLY on the pre-wipe archive key → reset is
//      aborted: city state unchanged, no archive entry written, and the
//      role="alert" "Start Over aborted" banner (store.tsx:857-878) renders.
//   2. Un-rigging storage and dispatching reset again → the wipe proceeds (state
//      resets to a fresh city) and the archive entry now exists.
//
// MUTATION-PROOF: re-wrapping attemptWipe's captureBeforeWipe call in a swallowing
// try/catch (so applyWipe always runs) turns BAR-1's direct unit tests in
// capture-before-wipe.test.mjs RED — verified via scratch-copy (cp/mv), not
// re-run here. This file adds the store-level integration leg the round asked for.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';

// Install jsdom globals BEFORE importing React/store — react-dom/client's createRoot
// and the effect scheduler probe `window`/`document` at module-eval and mount time.
// (Same recipe as store-dispatch.test.tsx.)
function installJsdom() {
  const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', {
    url: 'http://localhost/',
    pretendToBeVisual: true,
  });
  const { window } = dom;
  (globalThis as any).window = window;
  (globalThis as any).document = window.document;
  Object.defineProperty(globalThis, 'navigator', {
    value: window.navigator,
    configurable: true,
    writable: true,
  });
  (globalThis as any).HTMLElement = window.HTMLElement;
  (globalThis as any).requestAnimationFrame = window.requestAnimationFrame.bind(window);
  (globalThis as any).cancelAnimationFrame = window.cancelAnimationFrame.bind(window);
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  return dom;
}

test('BUG-437 BAR-1(b): reset with a rigged pre-wipe archive write aborts the wipe; un-rigging lets it proceed', async () => {
  const dom = installJsdom();
  try {
    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimProvider, useSim } = await import('../src/sim/store.tsx');
    const { PREWIPE_ARCHIVE_KEY, readPreWipeArchive } = await import('../src/sim/captureBeforeWipe.ts');
    const { initialState } = await import('../src/sim/engine.ts');

    // `any` on purpose: this is a test probe reaching into whatever useSim() returns
    // at render time. A plain function-return getter (rather than a `let x | null`
    // + non-null-assertion pattern) sidesteps TS control-flow narrowing edge cases
    // around a value reassigned from inside a nested component closure.
    function Probe() {
      const sim = useSim();
      (Probe as any)._latest = sim;
      return null;
    }
    function latest(): { state: any; dispatch: (a: unknown) => void } {
      const v = (Probe as any)._latest;
      if (!v) throw new Error('Probe has not rendered yet');
      return v;
    }

    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);

    await act(async () => {
      root.render(React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }));
    });
    assert.ok((Probe as any)._latest, 'Probe must have rendered');

    // BUG-694 tick-gate: pause the store's own auto tick-driver (store.tsx's
    // setInterval effect, keyed on state.speed, firing every SPEED_MS[speed]ms
    // via engine.ts's `speed` action — SPEED_MS[1] default is 900ms) BEFORE
    // driving any explicit ticks below. Under load this test's own act()
    // round-trips can take long enough in wall-clock time for that background
    // interval to also fire, injecting an extra real tick between the funds
    // snapshot (dirtyFunds, below) and the post-abort assert — moving funds by
    // exactly one tick's net flows (observed 964638 vs 964729, pass/fail/pass).
    // This is the liveness-needs-both-bounds class (house memory: a progress
    // floor without a rate ceiling let a clock runaway ship) — the fix is
    // TICK-GATING the driver itself (speed: 0), not widening the funds
    // tolerance, which would also hide a real GR#27 funds-leak-on-abort bug.
    await act(async () => { latest().dispatch({ type: 'speed', speed: 0 }); });

    // Dirty the city (same proven sequence used in capture-before-wipe.test.mjs's
    // dirtyCity() fixture) so "unchanged after aborted wipe" is actually observable.
    await act(async () => { latest().dispatch({ type: 'debugFunds', amount: 50_000 }); });
    await act(async () => { latest().dispatch({ type: 'place', spec: 'res_hut', x: 5, y: 5 }); });
    await act(async () => { latest().dispatch({ type: 'tick' }); });
    await act(async () => { latest().dispatch({ type: 'tick' }); });

    const dirtyFunds = latest().state.funds;
    const dirtyTick = latest().state.tick;
    const dirtyBuildings = latest().state.buildings.length;
    assert.ok(dirtyBuildings > 0, 'fixture must actually place a building to detect a wipe');

    // Rig localStorage: throw ONLY on the pre-wipe archive key, pass everything
    // else through to the real jsdom localStorage (autosave/savepoint writes must
    // keep working so the app doesn't crash for unrelated reasons).
    //
    // captureBeforeWipe (captureBeforeWipe.ts:87-112) has THREE fallback layers
    // before it truly gives up: (1) write the capped slice, (2) write just this
    // one entry, (3) trigger a browser file download via an <a download> click.
    // Rigging only setItem lets layer (3) "succeed" silently under jsdom (a.click()
    // on an anchor is a no-op there, not a real download), which would make the
    // capture falsely appear to succeed. Block the anchor click too so ALL THREE
    // layers genuinely fail — the real "user's disk is also full" scenario.
    const realAnchorClick = dom.window.HTMLAnchorElement.prototype.click;
    dom.window.HTMLAnchorElement.prototype.click = function () {
      throw new Error('anchor click blocked for test (simulating total capture failure)');
    };

    const realStorage = dom.window.localStorage;
    const rigged = {
      getItem: (k: string) => realStorage.getItem(k),
      setItem: (k: string, v: string) => {
        if (k === PREWIPE_ARCHIVE_KEY) {
          throw new Error('QuotaExceededError: rigged for test');
        }
        return realStorage.setItem(k, v);
      },
      removeItem: (k: string) => realStorage.removeItem(k),
      key: (i: number) => realStorage.key(i),
      get length() {
        return realStorage.length;
      },
    };
    Object.defineProperty(dom.window, 'localStorage', { value: rigged, configurable: true });

    await act(async () => { latest().dispatch({ type: 'reset' }); });

    // --- City state must be UNCHANGED: the wipe was aborted. ---
    assert.equal(latest().state.funds, dirtyFunds, 'funds unchanged after aborted wipe');
    assert.equal(latest().state.tick, dirtyTick, 'tick unchanged after aborted wipe');
    assert.equal(latest().state.buildings.length, dirtyBuildings, 'buildings unchanged after aborted wipe');

    // --- No archive entry was written (the rigged setItem failed). ---
    const archiveAfterFail = readPreWipeArchive(realStorage as any);
    assert.equal(archiveAfterFail.length, 0, 'no archive entry from the failed capture');

    // --- The captureError banner (store.tsx:857-878) must render. ---
    const alertEl = container.querySelector('[role="alert"]');
    assert.ok(alertEl, 'captureError banner (role="alert") must be rendered after an aborted wipe');
    assert.match(alertEl!.textContent ?? '', /Start Over aborted/);

    // --- Un-rig storage AND the anchor click: the wipe should now proceed. ---
    dom.window.HTMLAnchorElement.prototype.click = realAnchorClick;
    Object.defineProperty(dom.window, 'localStorage', { value: realStorage, configurable: true });

    await act(async () => { latest().dispatch({ type: 'reset' }); });

    const fresh = initialState();
    assert.equal(latest().state.tick, fresh.tick, 'wipe proceeds once the capture succeeds');
    assert.equal(latest().state.funds, fresh.funds, 'wipe proceeds once the capture succeeds (funds reset)');
    assert.equal(latest().state.buildings.length, fresh.buildings.length, 'wipe proceeds once the capture succeeds (buildings reset)');

    const archiveAfterSuccess = readPreWipeArchive(realStorage as any);
    assert.equal(archiveAfterSuccess.length, 1, 'archive entry exists after the successful wipe capture');
    assert.equal(archiveAfterSuccess[0].tick, dirtyTick, 'archived entry captured the pre-wipe tick');

    await act(async () => { root.unmount(); });
  } finally {
    dom.window.close();
  }
});
