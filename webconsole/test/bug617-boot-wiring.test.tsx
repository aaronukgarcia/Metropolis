// bug617-boot-wiring.test.tsx — BUG-617 end-to-end proof: a real client mount
// with a savepoint whose journalTail is past LARGE_TAIL_REPLAY_THRESHOLD
// boots on the pre-tail (small) state without blocking the first render, then
// converges to the fully replayed city via the chunked useEffect. Also proves
// the self-healing fresh-savepoint write (BUG-617's third fix component):
// after the chunked replay lands, the NEXT boot's tail is empty.
//
// RACE FIX (coordinator follow-up, 2026-09-03): the first version of this
// test used a 240-action tail and asserted the FIRST render's building count
// equalled the pre-tail count exactly. That assertion RACED the chunked
// replay: the effect's first chunk (replayTailChunked — up to
// TAIL_ACTIONS_PER_CHUNK=50 CHEAP 'place'/'road' actions, comfortably inside
// the 40ms time budget) runs SYNCHRONOUSLY inside the effect (React flushes
// effects before an enclosing `act()` resolves), so with only 240 actions
// total the first chunk alone could already advance the building count past
// the pre-tail snapshot before the assertion line ran — a flake, not a real
// signal.
//
// This version proves the same three claims WITHOUT racing a fixed action
// count against a fixed chunk size:
//   1. INSTANT BOOT, NEVER FULLY-REPLAYED SYNCHRONOUSLY: immediately after
//      mount, the building count is strictly LESS than the fully-replayed
//      total. With a tail of 2,400 actions (10x the original) and a
//      TAIL_ACTIONS_PER_CHUNK=50 ceiling, no single synchronous chunk can
//      ever reach the end — this holds by construction, not by timing luck.
//   2. PROGRESS IS VISIBLE: the SAME RebuildPrompt overlay a plain Load uses
//      (busyLabel="Loading your city…") is present in the DOM immediately
//      after mount — proving the chunked/progress wiring is actually engaged
//      for this large a tail, not merely "state converges eventually" by
//      some unrelated path.
//   3. CONVERGENCE + SELF-HEAL: driving real macrotask waits (jsdom's
//      requestAnimationFrame) to completion reaches the fully-replayed
//      building count, and the persisted savepoint afterwards carries an
//      EMPTY tail (BUG-617's self-healing fix) with the fully-replayed
//      snapshot baked in.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';

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

function buildGrowthTail(pairs: number) {
  const actions: Array<{ type: string; spec?: string; x?: number; y?: number }> = [];
  const cols = 100;
  for (let i = 0; i < pairs; i++) {
    const col = i % cols;
    const row = Math.floor(i / cols);
    const x = 2 + col * 3;
    const y = 2 + row * 3;
    actions.push({ type: 'place', spec: 'road', x, y: y + 1 });
    actions.push({ type: 'place', spec: 'res_hut', x, y });
  }
  return actions;
}

async function waitFor(predicate: () => boolean, timeoutMs: number, stepMs = 25): Promise<void> {
  const start = Date.now();
  while (!predicate()) {
    if (Date.now() - start > timeoutMs) throw new Error('waitFor timed out');
    await new Promise((r) => setTimeout(r, stepMs));
  }
}

test('BUG-617: large-tail boot mounts on pre-tail state with visible progress, then converges via chunked replay, then self-heals', async () => {
  const dom = installJsdom();
  try {
    const { initialState } = await import('../src/sim/engine.ts');
    const { emptyJournal, recordAction } = await import('../src/sim/journal.ts');
    const { createSavepoint, persistSavepoint, readAllSavepoints, mostRecentSavepoint, LARGE_TAIL_REPLAY_THRESHOLD } =
      await import('../src/sim/replay.ts');
    const { versionBadgeLabel } = await import('../src/sim/version.ts');

    // BUG-617 test fix: the savepoint's buildVersion MUST match the running
    // build's badge (store.tsx's currentBuildVersion() reads the SAME
    // versionBadgeLabel()) — a mismatched version makes boot's `needsRebuild`
    // check true, routing through the CROSS-BUILD genesis-rebuild-prompt path
    // instead of BUG-617's large-tail chunked SAVEPOINT-restore path this test
    // exists to prove. The original version of this test hard-coded
    // 'v0.0.0.1', which (almost) never equals the real running badge, so it
    // was silently exercising the WRONG code path — the entire tail was
    // replayed synchronously by the pre-existing `restoreFromSavepoint`
    // (correct for a small tail, but not what this large-tail test intends to
    // exercise), which is what actually made the original assertion flake.
    const runningVersion = versionBadgeLabel();

    const start = { ...initialState(), unlockedAll: true, funds: 5_000_000_000 };
    const startBuildingCount = start.buildings.length;
    // 10x the original repro's tail — with TAIL_ACTIONS_PER_CHUNK=50, a
    // single synchronous chunk can cover AT MOST 50 of these 2,400 actions,
    // so "not yet fully replayed at first render" holds by construction, not
    // by timing luck.
    const tailActions = buildGrowthTail(1200); // 2,400 actions
    assert.ok(tailActions.length > LARGE_TAIL_REPLAY_THRESHOLD, 'test tail must exceed the large-tail threshold');

    let journal = emptyJournal();
    for (const a of tailActions) journal = recordAction(journal, start.tick, a as never);

    const savepoint = createSavepoint(start, journal.entries, new Date(), runningVersion, null);
    const ok = persistSavepoint(dom.window.localStorage as unknown as Storage, savepoint);
    assert.ok(ok, 'seed savepoint must persist');

    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimProvider, useSim } = await import('../src/sim/store.tsx');

    let latestBuildingCount = -1;
    function Probe() {
      const { state } = useSim();
      latestBuildingCount = state.buildings.length;
      return null;
    }

    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);

    // DETERMINISM NOTE: this mount uses the SYNCHRONOUS `act()` overload
    // (not `await act(async () => ...)`) DELIBERATELY. An async act callback
    // introduces an `await` point, which yields control back to the Node
    // event loop — and jsdom's requestAnimationFrame (pretendToBeVisual)
    // schedules its callback on a real (if short) timer, so an awaited act
    // call can let SEVERAL chunked-replay rAF cycles fire before this test's
    // own continuation resumes (the original flaky version's exact failure
    // mode, observed at up to 2,400 actions). The SYNCHRONOUS overload flushes
    // React's render + effects (including this effect's own FIRST,
    // synchronous `processChunk()` call, which applies at most
    // TAIL_ACTIONS_PER_CHUNK=50 actions) but returns to this test's code
    // BEFORE the event loop gets a turn to run the scheduled
    // `requestAnimationFrame(processChunk)` for chunk #2 — so the assertion
    // immediately below is checking state that is PROVABLY only one chunk
    // deep, not racing an indeterminate number of rAF cycles.
    act(() => {
      root.render(React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }));
    });

    const expectedFinalCount = startBuildingCount + tailActions.length;

    // CLAIM 1: instant boot, never fully-replayed synchronously. By
    // construction (2,400 actions, 50-per-chunk ceiling) the first render
    // cannot already show the fully-replayed city.
    assert.ok(
      latestBuildingCount < expectedFinalCount,
      `first render must NOT already show the fully-replayed city ` +
        `(got ${latestBuildingCount}, final is ${expectedFinalCount}) — the large tail must not ` +
        `have been replayed synchronously inside the boot initializer`,
    );
    assert.ok(latestBuildingCount >= startBuildingCount, 'first render must show at least the pre-tail city, never fewer buildings');

    // CLAIM 2: progress is visible via the SAME RebuildPrompt overlay a plain
    // Load uses (busyLabel keys off rebuildDecision.kind === 'load').
    assert.ok(
      container.textContent?.includes('Loading your city'),
      'the chunked-load progress overlay must be visible immediately after mount for a large tail',
    );

    // CLAIM 3: convergence + self-heal. Real macrotask waits let jsdom's
    // requestAnimationFrame (pretendToBeVisual) actually fire between React's
    // effect and the next chunk.
    //
    // DELIBERATELY NOT wrapped in `act(async () => ...)`: React's act()
    // batches/defers commits from updates that happen INSIDE its scope until
    // the scope itself resolves. Since the chunked replay's own progress
    // commits are exactly what `waitFor`'s predicate is polling for, wrapping
    // the poll in `act()` creates a deadlock — act() will not resolve until
    // the predicate is satisfied, but the predicate can only become satisfied
    // by a commit act() is deferring, so it self-stalls until the timeout
    // (confirmed directly: the same wait converges promptly OUTSIDE act(),
    // and never converges — despite the generator provably advancing the
    // whole way to completion internally — INSIDE it). React 18's createRoot
    // still applies real-timer-driven updates to the DOM outside act() (only
    // printing a "not wrapped in act(...)" warning), so polling the plain
    // `latestBuildingCount` closure variable here is safe and deterministic.
    // Widened 20s->90s (independent round r2 stability finding, 2026-09-03):
    // this is a "does it eventually converge" check, not a per-chunk timing
    // gate — the chunked chain's real wall-clock rate is CPU-contention
    // sensitive (multiple heavy jsdom/React chunked-replay tests running
    // concurrently in the same node:test process starve each other's
    // requestAnimationFrame cadence), measured to occasionally exceed 20s
    // when this file runs alongside its bug617/attack siblings. Widening
    // costs nothing on catching power: a genuinely hung/broken chain still
    // never satisfies the predicate and still fails, just after waiting
    // longer — see tools/test/scoped.mjs's SLOW_TEST_CAPS_SEC entry for this
    // file, which raises the group's own hard cap to match.
    await waitFor(() => latestBuildingCount === expectedFinalCount, 90_000);

    assert.equal(latestBuildingCount, expectedFinalCount, 'state must converge to the fully replayed city');

    // The progress overlay must be gone once the load has completed.
    assert.ok(
      !container.textContent?.includes('Loading your city'),
      'the progress overlay must be dismissed once the chunked replay completes',
    );

    // Self-healing: the persisted savepoint now on disk must have an EMPTY
    // tail (BUG-617's third fix component) — the NEXT boot has nothing large
    // left to replay.
    const healed = mostRecentSavepoint(readAllSavepoints(dom.window.localStorage as unknown as Storage));
    assert.ok(healed, 'a self-healed savepoint must be persisted after the chunked replay completes');
    assert.equal(healed!.journalTail.length, 0, 'the self-healed savepoint must carry an EMPTY tail');
    assert.equal(healed!.snapshot.buildings.length, expectedFinalCount, 'the self-healed snapshot must be the fully replayed city');

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});
