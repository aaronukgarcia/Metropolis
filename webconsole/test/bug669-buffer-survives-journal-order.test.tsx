// bug669-buffer-survives-journal-order.test.tsx — BUG-669 REWORK PROOF.
//
// attack-bug669-round.test.tsx (the independent round's own file, left
// unmodified) proves the interleaved action SURVIVES in the live `state`
// after the chunked tail replay converges. This file proves the OTHER half
// of the required rework: the ON-DISK JOURNAL ends up in the same order the
// player actually experienced — tail actions, THEN the interleaved action —
// with no divergence between what `state` shows and what the journal would
// replay (GR#21). That is the guarantee `tailReplayBufferRef`'s drain (right
// before the tail-replay effect's own `dispatch({type:'hydrate', ...})`,
// store.tsx) exists to provide: the buffered action is replayed through the
// reducer ON TOP of the tail's own `finalState`, in the same relative order
// wrappedDispatch already gave it in the journal.
//
// Setup note: `metropolis.journal` (journal.ts's JOURNAL_KEY) is a SEPARATE
// localStorage key from the savepoint's own embedded `journalTail` blob —
// store.tsx's autosave effect always keeps them in lockstep in real play
// (a savepoint's tail is `journalTail(journal, lastSaveIndex)`, a slice of
// the SAME persisted journal), so this test seeds BOTH with the identical
// tail actions, exactly mirroring that real invariant, rather than relying
// on the boot path's `loadJournal` returning empty (which every OTHER
// bug617/bug669 test in this suite happens not to need, since none of them
// inspect the on-disk journal afterwards).

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
  Object.defineProperty(globalThis, 'navigator', { value: window.navigator, configurable: true, writable: true });
  (globalThis as any).HTMLElement = window.HTMLElement;
  (globalThis as any).requestAnimationFrame = window.requestAnimationFrame.bind(window);
  (globalThis as any).cancelAnimationFrame = window.cancelAnimationFrame.bind(window);
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  return dom;
}

function buildGrowthTail(pairs: number) {
  const actions: Array<{ type: string; spec?: string; x?: number; y?: number }> = [];
  for (let i = 0; i < pairs; i++) {
    const x = 2 + (i % 100) * 3;
    const y = 2 + Math.floor(i / 100) * 3;
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

const PROBE_X = 350;
const PROBE_Y = 200;

test('BUG-669 rework: an action dispatched mid chunked-tail-replay survives AND lands in the journal AFTER the tail, in order', async () => {
  const dom = installJsdom();
  try {
    const { initialState } = await import('../src/sim/engine.ts');
    const { emptyJournal, recordAction, persistJournal } = await import('../src/sim/journal.ts');
    const { createSavepoint, persistSavepoint } = await import('../src/sim/replay.ts');
    const { versionBadgeLabel } = await import('../src/sim/version.ts');

    const runningVersion = versionBadgeLabel();
    const start = { ...initialState(), unlockedAll: true, funds: 5_000_000_000 };
    const startBuildingCount = start.buildings.length;

    // 400 pairs = 800 actions — same shape as the round's own attack file:
    // well past LARGE_TAIL_REPLAY_THRESHOLD, so the chunked replay genuinely
    // spans multiple rAF turns, giving a real window to interleave a dispatch.
    const tailActions = buildGrowthTail(400);
    let journal = emptyJournal();
    for (const a of tailActions) journal = recordAction(journal, start.tick, a as never);

    // Seed BOTH the savepoint's embedded tail AND the separate on-disk
    // journal with the identical entries — the real-play invariant (see file
    // header) that lets this test's after-the-fact journal inspection mean
    // anything.
    const savepoint = createSavepoint(start, journal.entries, new Date(), runningVersion, null);
    assert.ok(persistSavepoint(dom.window.localStorage as unknown as Storage, savepoint), 'seed savepoint must persist');
    assert.ok(persistJournal(dom.window.localStorage as unknown as Storage, journal), 'seed journal must persist');

    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimProvider, useSim } = await import('../src/sim/store.tsx');

    let latestBuildingCount = -1;
    let latestBuildings: any[] = [];
    let latestDispatch: ((a: unknown) => void) | null = null;
    function Probe() {
      const { state, dispatch } = useSim();
      latestBuildingCount = state.buildings.length;
      latestBuildings = state.buildings;
      latestDispatch = dispatch as (a: unknown) => void;
      return null;
    }

    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);

    act(() => {
      root.render(React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }));
    });

    const expectedFinalCount = startBuildingCount + tailActions.length;
    assert.ok(latestBuildingCount < expectedFinalCount, 'precondition: must be mid-replay when the interleaved action fires');

    // Fire the interleaved player action while the chunked replay is still running.
    act(() => {
      latestDispatch!({ type: 'place', spec: 'res_hut', x: PROBE_X, y: PROBE_Y });
    });
    assert.ok(
      latestBuildings.some((b: any) => b.x === PROBE_X && b.y === PROBE_Y),
      'the interleaved action must be applied immediately, exactly as the round attack file already pins',
    );

    // Let the chunked tail replay run to completion.
    await waitFor(() => latestBuildingCount >= expectedFinalCount, 90_000);

    // SURVIVAL (state): the probe building is still there after convergence —
    // the buffer drain reconciled it onto the tail's own finalState instead
    // of the hydrate wiping it out.
    assert.ok(
      latestBuildings.some((b: any) => b.x === PROBE_X && b.y === PROBE_Y),
      'BUG-669 rework: the interleaved action must SURVIVE the replay hydrate, not be silently discarded',
    );

    // JOURNAL ORDER: flush the debounced journal write (the same
    // `beforeunload` path a real tab-close/reload uses — store.tsx's own
    // GR#27/BUG-458 handler) and read the on-disk journal back.
    act(() => {
      dom.window.dispatchEvent(new dom.window.Event('beforeunload'));
    });
    const { loadJournal } = await import('../src/sim/journal.ts');
    const persistedJournal = loadJournal(dom.window.localStorage as unknown as Storage);

    assert.ok(
      persistedJournal.entries.length >= tailActions.length + 1,
      `journal must carry at least the ${tailActions.length} tail entries plus the interleaved one, got ${persistedJournal.entries.length}`,
    );

    // The interleaved action must be the LAST entry (dispatched strictly
    // after every tail action, in real time) and every entry before it must
    // be one of the tail's own actions, in the SAME relative order they were
    // seeded in — i.e. the journal was never reordered or had the tail
    // spliced around the interleaved action.
    const last = persistedJournal.entries[persistedJournal.entries.length - 1];
    assert.equal((last.action as any).type, 'place');
    assert.equal((last.action as any).x, PROBE_X);
    assert.equal((last.action as any).y, PROBE_Y);

    const tailPortion = persistedJournal.entries.slice(0, tailActions.length);
    for (let i = 0; i < tailActions.length; i++) {
      assert.deepEqual(
        tailPortion[i].action,
        tailActions[i],
        `journal entry ${i} must match the seeded tail action in the SAME order (no reordering around the interleaved dispatch)`,
      );
    }

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});
