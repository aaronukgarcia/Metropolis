// idb-primary-boot.test.tsx — FEAT-2326609780 inc2: IndexedDB-PRIMARY boot.
//
// CONTEXT: inc1 (FEAT-2326609778, landed) made every save WRITE mirror into
// IndexedDB, but boot still read localStorage exclusively, and BUG-617's
// self-heal savepoint write skipped the mirror entirely (P2 follow-up filed
// on BUG-669's own landing). attack-indexeddb-round.test.mjs's FINDING 1
// proved the resulting gap directly: "the mirror is genuinely write-only...
// clearing localStorage loses the save even though the IndexedDB mirror
// still has it". Aaron's real 49k-building city hit exactly this shape:
// localStorage's 5MB quota rejects every savepoint persist, the savepoint
// never advances, and every reload re-replays a huge journal tail against a
// permanently stale snapshot.
//
// THIS BUILD closes that gap two ways:
//   (1) SAVE: every `persistSavepoint` call site now mirrors UNCONDITIONALLY
//       (`mirrorAfterPersist` in store.tsx) — on a failed localStorage write,
//       the savepoint that could not be written lands DIRECTLY in IndexedDB's
//       new overflow slot (`SAVEPOINT_OVERFLOW_KEY`, saveStore.ts), bypassing
//       the "read localStorage first" step every inc1 mirror helper used
//       (which would otherwise just re-copy the stale bytes and never let
//       the durable store get ahead). The BUG-617 self-heal write gets the
//       same treatment (previously mirrored NOTHING at all).
//   (2) BOOT: a post-mount effect (store.tsx, right after the existing
//       one-time-migration effect) compares the freshest thing IndexedDB
//       holds against what localStorage's SYNCHRONOUS boot initializer just
//       loaded (`isStrictlyFresherSavepointMeta`, mirroring
//       `persistSavepoint`'s own tick/savedAt overwrite-protection rule). A
//       strictly fresher IDB savepoint is hydrated through the EXACT SAME
//       `prepareRestoreForChunkedTail`/`replayTailChunked` machinery a large
//       localStorage tail already uses (fed a tiny in-memory shim storage
//       exposing the IDB bytes at slot 0) — so mid-swap dispatches survive
//       via the existing BUG-669 buffer-and-drain, and the swap is a real
//       'load'-kind hydrate (AC-31 semantics unchanged, BUG-677 honoured).
//
// This file proves, end to end, against the REAL production code (never a
// reimplementation): the quota-wedge repro (1), grandfathering surviving an
// IDB-only source (2, the merge-lane seam finding this increment was
// explicitly asked for), and the equal-freshness no-op (3).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';

// The .mjs test helper has no .d.ts (it is plain JS, consumed elsewhere only
// from .mjs test files). tsc --noEmit only attempts to resolve a dynamic
// import()'s module declaration when its specifier is a LITERAL string —
// routing it through a variable keeps this a plain `Promise<any>` (no
// implicit-any error) while the runtime behaviour (a relative dynamic
// import, resolved by tsx's loader exactly like every other import in this
// file) is unaffected.
async function loadFakeIndexedDBFactory(): Promise<(backing?: Map<string, Map<string, string>>) => any> {
  const specifier = './helpers/fakeIndexedDB.mjs';
  const mod: any = await import(specifier);
  return mod.createFakeIndexedDBFactory;
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
  return dom;
}

async function waitFor(predicate: () => boolean, timeoutMs: number, stepMs = 25): Promise<void> {
  const start = Date.now();
  while (!predicate()) {
    if (Date.now() - start > timeoutMs) throw new Error('waitFor timed out');
    await new Promise((r) => setTimeout(r, stepMs));
  }
}

async function waitForAsync(predicate: () => Promise<boolean>, timeoutMs: number, stepMs = 25): Promise<void> {
  const start = Date.now();
  for (;;) {
    if (await predicate()) return;
    if (Date.now() - start > timeoutMs) throw new Error('waitForAsync timed out');
    await new Promise((r) => setTimeout(r, stepMs));
  }
}

// ---------------------------------------------------------------------------
// 0. Pure-function proof: the freshness comparator itself.
// ---------------------------------------------------------------------------

test('isStrictlyFresherSavepointMeta / freshestSavepoint: tick-then-savedAt rule, matching persistSavepoint\'s own overwrite protection', async () => {
  const dom = installJsdom();
  try {
    const { isStrictlyFresherSavepointMeta, freshestSavepoint } = await import('../src/sim/store.tsx');

    // A null booted savepoint (fresh dev-city boot) loses to ANY candidate.
    assert.equal(isStrictlyFresherSavepointMeta({ snapshotTick: 0, savedAt: 'x' }, null), true);
    // A null candidate never beats anything.
    assert.equal(isStrictlyFresherSavepointMeta(null, { snapshotTick: 0, savedAt: 'x' }), false);
    assert.equal(isStrictlyFresherSavepointMeta(null, null), false);

    // Higher tick wins outright, even with an EARLIER savedAt string (tick is primary).
    assert.equal(
      isStrictlyFresherSavepointMeta({ snapshotTick: 10, savedAt: '2020-01-01T00:00:00.000Z' }, { snapshotTick: 5, savedAt: '2030-01-01T00:00:00.000Z' }),
      true,
    );
    // Equal tick: savedAt breaks the tie.
    assert.equal(isStrictlyFresherSavepointMeta({ snapshotTick: 5, savedAt: '2020-01-02T00:00:00.000Z' }, { snapshotTick: 5, savedAt: '2020-01-01T00:00:00.000Z' }), true);
    // EQUAL on both fields — the "boot effect must not hydrate a second time" case — is NOT strictly fresher.
    assert.equal(isStrictlyFresherSavepointMeta({ snapshotTick: 5, savedAt: '2020-01-01T00:00:00.000Z' }, { snapshotTick: 5, savedAt: '2020-01-01T00:00:00.000Z' }), false);
    // Older loses outright.
    assert.equal(isStrictlyFresherSavepointMeta({ snapshotTick: 4, savedAt: '2020-01-01T00:00:00.000Z' }, { snapshotTick: 5, savedAt: '2020-01-01T00:00:00.000Z' }), false);

    const sp = (tick: number, savedAt: string) => ({ snapshotTick: tick, savedAt, snapshot: {}, journalTail: [] }) as any;
    assert.deepEqual(freshestSavepoint([null, sp(1, 'a'), null]), sp(1, 'a'));
    const best = freshestSavepoint([sp(1, '2020-01-01T00:00:00.000Z'), sp(9, '2020-01-01T00:00:00.000Z'), sp(3, '2029-01-01T00:00:00.000Z')]);
    assert.equal(best!.snapshotTick, 9, 'the highest tick must win among several candidates (no saveSeq on either side — tick+savedAt fallback)');
    assert.equal(freshestSavepoint([null, null]), null);

    // FEAT-2326609780 round 3 (the structural fix): saveSeq is now PRIMARY,
    // overriding tick/savedAt entirely — this is what closes ATTACK G
    // (building count shrank) and R2-F1 (a later wall-clock stamp at equal
    // tick used to win regardless of which side had the real history).
    assert.equal(
      isStrictlyFresherSavepointMeta(
        { snapshotTick: 5, savedAt: '2020-01-01T00:00:00.000Z', saveSeq: 9 },
        { snapshotTick: 5, savedAt: '2020-01-02T00:00:00.000Z', saveSeq: 3 }, // LATER savedAt, but fewer persists
      ),
      true,
      'a higher saveSeq must win even against an EARLIER tick+savedAt tie-break value — ATTACK G/R2-F1',
    );
    assert.equal(
      isStrictlyFresherSavepointMeta(
        { snapshotTick: 100, savedAt: '2030-01-01T00:00:00.000Z', saveSeq: 2 }, // higher tick+savedAt, but fewer persists
        { snapshotTick: 5, savedAt: '2020-01-01T00:00:00.000Z', saveSeq: 7 },
      ),
      false,
      'a LOWER saveSeq must lose even against a higher tick+savedAt — saveSeq is primary, not a tie-break',
    );
    // A saveSeq of 0 on the candidate is a REAL, valid value — not "absent".
    assert.equal(
      isStrictlyFresherSavepointMeta({ snapshotTick: 1, savedAt: 'a', saveSeq: 1 }, { snapshotTick: 1, savedAt: 'a', saveSeq: 0 }),
      true,
      'saveSeq 1 must beat saveSeq 0',
    );
    // Pre-round-3 savepoints (saveSeq undefined on BOTH sides) fall back to
    // the documented tick+savedAt rule — never silently treated as "equal,
    // no swap" just because neither carries the new field.
    assert.equal(
      isStrictlyFresherSavepointMeta({ snapshotTick: 6, savedAt: 'a' }, { snapshotTick: 5, savedAt: 'a' }),
      true,
      'both sides pre-round-3 (no saveSeq) must fall back to tick+savedAt, not silently tie',
    );

    const seqSp = (seq: number, tick: number, savedAt: string) => ({ snapshotTick: tick, savedAt, saveSeq: seq, snapshot: {}, journalTail: [] }) as any;
    const bestBySeq = freshestSavepoint([seqSp(2, 99, '2099-01-01T00:00:00.000Z'), seqSp(9, 1, '2000-01-01T00:00:00.000Z'), seqSp(5, 50, '2050-01-01T00:00:00.000Z')]);
    assert.equal(bestBySeq!.saveSeq, 9, 'freshestSavepoint must pick the highest saveSeq, not the highest tick or the latest savedAt');
  } finally {
    dom.window.close();
  }
});

// ---------------------------------------------------------------------------
// 1. THE QUOTA WEDGE, END TO END.
// ---------------------------------------------------------------------------

test('FEAT-2326609780 inc2: quota wedge — localStorage rejects every savepoint persist, the IndexedDB mirror keeps advancing, and a fresh boot picks up the FRESHER IDB savepoint with a SHORT (empty) tail instead of staying stuck on the stale localStorage one', async () => {
  const dom = installJsdom();
  try {
    const backing = new Map<string, Map<string, string>>();
    (globalThis as any).indexedDB = (await loadFakeIndexedDBFactory())(backing);
    const { resetSaveStoreForTests, getDefaultSaveStore } = await import('../src/sim/saveStore.ts');
    resetSaveStoreForTests();

    const { occupiedSet, fits, MAP_W, MAP_H } = await import('../src/sim/data.ts');
    const { SAVEPOINT_KEY_PREFIX, readAllSavepoints, mostRecentSavepoint, readCurrentLineageId, LEGACY_LINEAGE_ID } = await import('../src/sim/replay.ts');
    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimProvider, useSim } = await import('../src/sim/store.tsx');

    function findClearSpot(state: any, w: number, h: number) {
      const occ = occupiedSet(state);
      for (let y = 0; y <= MAP_H - h; y++) {
        for (let x = 0; x <= MAP_W - w; x++) {
          if (fits(occ, w, h, x, y)) return { x, y };
        }
      }
      throw new Error('no clear spot found');
    }
    function injectBuilding(state: any, spec: string, x: number, y: number) {
      const id = state.nextId ?? state.buildings.length + 1;
      return { ...state, nextId: id + 1, buildings: [...state.buildings, { id, spec, x, y }] };
    }

    let latestState: any = null;
    let saveGameRef: (() => Promise<boolean>) | null = null;
    let dispatchRef: ((a: any) => void) | null = null;
    function Probe() {
      const { state, saveGame, dispatch } = useSim();
      latestState = state;
      saveGameRef = saveGame;
      dispatchRef = dispatch;
      return null;
    }

    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);
    act(() => {
      root.render(React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }));
    });
    await waitFor(() => latestState !== null, 5000);

    const baselineCount = latestState.buildings.length;

    // Baseline: one successful save while localStorage is fully healthy —
    // lands on BOTH localStorage and (async, best-effort) IndexedDB.
    const spot1 = findClearSpot(latestState, 1, 1);
    act(() => {
      dispatchRef!({ type: 'hydrate', state: injectBuilding(latestState, 'res_hut', spot1.x, spot1.y) });
    });
    assert.equal(latestState.buildings.length, baselineCount + 1);
    let baselineSaveOk = false;
    await act(async () => {
      baselineSaveOk = await saveGameRef!();
    });
    assert.ok(baselineSaveOk, 'baseline save must succeed while localStorage is healthy');

    // FEAT-2326609780 P0 lineage fix (test premise updated per the
    // coordinator's own note): a real boot now mints its OWN lineage id, so
    // its mirrored IDB keys are namespaced under it, not the bare
    // `metropolis.savepoint.*` keys a pre-lineage save used. Read the
    // MINTED lineage back off the pointer the boot itself just wrote.
    const bootLineageId = readCurrentLineageId(dom.window.localStorage);
    assert.notEqual(bootLineageId, LEGACY_LINEAGE_ID, 'a fresh boot must mint its OWN lineage, never the reserved legacy one');
    const slotKey = (slot: number) => `${SAVEPOINT_KEY_PREFIX}.${bootLineageId}.${slot}`;
    const overflowKey = `${SAVEPOINT_KEY_PREFIX}.${bootLineageId}.idbOnly`;

    const store = getDefaultSaveStore();
    await waitForAsync(async () => (await store.getItem(slotKey(0))) !== null, 5000);

    // Now wedge localStorage: every WRITE to a savepoint slot throws
    // QuotaExceededError (the real over-quota shape on a 49k-building city);
    // reads/removals still work (mirrors attack-bug617-lifecycle.test.tsx's
    // quota harness). Journal/other keys are left alone — this test only
    // needs the SAVEPOINT write path to be wedged.
    const realStorage = dom.window.localStorage;
    let rejectedWrites = 0;
    const quotaStorage = {
      get length() {
        return realStorage.length;
      },
      key: (i: number) => realStorage.key(i),
      getItem: (k: string) => realStorage.getItem(k),
      removeItem: (k: string) => realStorage.removeItem(k),
      setItem: (k: string, v: string) => {
        if (k.startsWith(SAVEPOINT_KEY_PREFIX)) {
          rejectedWrites++;
          const e: any = new Error('QuotaExceededError: persistent storage is full');
          e.name = 'QuotaExceededError';
          e.code = 22;
          throw e;
        }
        realStorage.setItem(k, v);
      },
      clear: () => realStorage.clear(),
    };
    Object.defineProperty(dom.window, 'localStorage', { value: quotaStorage, configurable: true });

    // The city advances over SEVERAL wedged persist attempts — the realistic
    // shape (many failed autosave intervals over a session), not just one.
    // FEAT-2326609780 round 3 (test premise fix, per the coordinator's own
    // note): with the saveSeq redesign, a SINGLE wedged save's seq can tie
    // with the boot-time self-heal's own seq bump on the next reload (the
    // self-heal is itself exactly one persist attempt past the stale local
    // baseline) — an exact numeric coincidence this minimal a repro can hit,
    // resolved by the tick+savedAt tie-break, which is not guaranteed to
    // favour the rescue. Several wedged attempts (matching a real multi-
    // interval wedge) put the rescue's saveSeq unambiguously ahead of
    // anything a single local self-heal bump could ever reach.
    const WEDGED_SAVE_ATTEMPTS = 5;
    let wedgedSaveOk = true;
    for (let i = 0; i < WEDGED_SAVE_ATTEMPTS; i++) {
      const spot = findClearSpot(latestState, 1, 1);
      act(() => {
        dispatchRef!({ type: 'hydrate', state: injectBuilding(latestState, 'res_hut', spot.x, spot.y) });
      });
      await act(async () => {
        wedgedSaveOk = await saveGameRef!();
      });
    }
    assert.equal(latestState.buildings.length, baselineCount + 1 + WEDGED_SAVE_ATTEMPTS);
    assert.equal(wedgedSaveOk, false, 'every wedged save must FAIL — localStorage is wedged');
    assert.ok(rejectedWrites > 0, 'the quota shim must actually have rejected a savepoint write');

    // THE CORE CLAIM (1a): IndexedDB's overflow slot must hold the ADVANCED
    // city directly, bypassing the (still-stale) localStorage read entirely.
    await waitForAsync(async () => (await store.getItem(overflowKey)) !== null, 5000);
    const overflowRaw = await store.getItem(overflowKey);
    const overflowSavepoint = JSON.parse(overflowRaw!);
    assert.equal(overflowSavepoint.snapshot.buildings.length, baselineCount + 1 + WEDGED_SAVE_ATTEMPTS, 'IndexedDB overflow must hold the ADVANCED (post-wedge) city');
    assert.equal(overflowSavepoint.journalTail.length, 0, 'a manual save always carries an EMPTY tail — this is the SHORT tail the fresh boot will use');

    // THE CORE CLAIM (1b): localStorage's own slot must be UNCHANGED — genuinely stuck.
    const staleLocal = mostRecentSavepoint(readAllSavepoints(realStorage as unknown as Storage, new Date(), bootLineageId));
    assert.ok(staleLocal, 'the baseline savepoint must still be readable from localStorage');
    assert.equal(staleLocal!.snapshot.buildings.length, baselineCount + 1, 'localStorage must be STUCK on the pre-wedge (stale) city');

    await act(async () => {
      root.unmount();
    });

    // "Reload": a FRESH SimProvider mount, with localStorage restored to a
    // fully working (but still STALE-content) store, and the SAME IndexedDB
    // backing map (a real reload keeps the same on-disk IndexedDB database).
    Object.defineProperty(dom.window, 'localStorage', { value: realStorage, configurable: true });

    let latestState2: any = null;
    function Probe2() {
      const { state } = useSim();
      latestState2 = state;
      return null;
    }
    const root2 = createRoot(container);
    act(() => {
      root2.render(React.default.createElement(SimProvider, { children: React.default.createElement(Probe2) }));
    });

    // Instant boot must show the STALE localStorage city first (byte-for-byte
    // unchanged synchronous fast path — zero regression, per the design).
    assert.equal(latestState2.buildings.length, baselineCount + 1, 'the synchronous boot-time fast path must be UNCHANGED — instant, localStorage-sourced first paint');

    // THE PAYOFF: the post-mount IDB-freshness effect swaps in the fresher
    // IndexedDB savepoint, converging to the ADVANCED city — with no giant
    // tail replay wait, because the IDB savepoint's own tail was empty.
    await waitFor(() => latestState2 && latestState2.buildings.length === baselineCount + 1 + WEDGED_SAVE_ATTEMPTS, 10_000);
    assert.equal(latestState2.buildings.length, baselineCount + 1 + WEDGED_SAVE_ATTEMPTS, 'the fresh boot must converge to the city IndexedDB held, not the one localStorage was stuck on');

    await act(async () => {
      root2.unmount();
    });
  } finally {
    dom.window.close();
  }
});

// ---------------------------------------------------------------------------
// 2. GRANDFATHERING SURVIVES AN IDB-ONLY SOURCE (the merge-lane seam finding).
// ---------------------------------------------------------------------------

test('FEAT-2326609780 inc2: an OLD savepoint (pre-BUG-652 economyEpoch) sourced ONLY from IndexedDB (nothing in localStorage) still grandfathers a six-spec building exactly like a localStorage-sourced restore', async () => {
  const dom = installJsdom();
  try {
    const backing = new Map<string, Map<string, string>>();
    (globalThis as any).indexedDB = (await loadFakeIndexedDBFactory())(backing);
    const { resetSaveStoreForTests, getDefaultSaveStore } = await import('../src/sim/saveStore.ts');
    resetSaveStoreForTests();

    const { initialState } = await import('../src/sim/engine.ts');
    const { totalJobs, JOBS_GRANDFATHER_ECONOMY_EPOCH } = await import('../src/sim/data.ts');
    const { createSavepoint, SAVEPOINT_KEY_PREFIX } = await import('../src/sim/replay.ts');
    const { versionBadgeLabel } = await import('../src/sim/version.ts');

    function injectBuilding(state: any, spec: string, x: number, y: number) {
      const id = state.nextId ?? state.buildings.length + 1;
      return { ...state, nextId: id + 1, buildings: [...state.buildings, { id, spec, x, y }] };
    }

    // An OLD save (economyEpoch predates the BUG-652 migration) containing a
    // land_airport — mirrors bug652-jobs-grandfathering.test.mjs's own
    // fixture exactly, except this bytes-identical savepoint is seeded ONLY
    // into IndexedDB, never localStorage.
    let oldState: any = { ...initialState(), economyEpoch: 0 };
    oldState = injectBuilding(oldState, 'land_airport', 1, 1);
    const oldSavepoint = createSavepoint(oldState, [], new Date(), versionBadgeLabel(), null);

    const store = getDefaultSaveStore();
    const putOk = await store.setItem(`${SAVEPOINT_KEY_PREFIX}.0`, JSON.stringify(oldSavepoint));
    assert.ok(putOk.ok, 'test setup: seeding IndexedDB directly must succeed');

    // localStorage stays completely empty — boot's synchronous fast path
    // will fall through to a fresh dev/genesis city; the IDB-freshness
    // effect is the ONLY route the old save can reach the player through.
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
    act(() => {
      root.render(React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }));
    });

    await waitFor(() => !!latestState && latestState.buildings.some((b: any) => b.spec === 'land_airport'), 10_000);

    assert.equal(latestState.economyEpoch, JOBS_GRANDFATHER_ECONOMY_EPOCH, 'the IDB-sourced restore must bump economyEpoch to current, exactly like a localStorage-sourced one');
    assert.equal(totalJobs(latestState), 0, 'the grandfathered airport sourced ONLY from IndexedDB must contribute ZERO jobs — the same protection a localStorage-sourced restore already has');

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});

// ---------------------------------------------------------------------------
// 3. EQUAL FRESHNESS — NO SECOND HYDRATE, NO DOUBLE-LOAD FLICKER.
// ---------------------------------------------------------------------------

test('FEAT-2326609780 inc2: when IndexedDB and localStorage agree (the healthy inc1-mirror case), the boot effect does NOT hydrate a second time', async () => {
  const dom = installJsdom();
  try {
    const backing = new Map<string, Map<string, string>>();
    (globalThis as any).indexedDB = (await loadFakeIndexedDBFactory())(backing);
    const { resetSaveStoreForTests, getDefaultSaveStore } = await import('../src/sim/saveStore.ts');
    resetSaveStoreForTests();

    const { initialState } = await import('../src/sim/engine.ts');
    const { createSavepoint, persistSavepoint, SAVEPOINT_KEY_PREFIX } = await import('../src/sim/replay.ts');
    const { versionBadgeLabel } = await import('../src/sim/version.ts');

    const start = { ...initialState() };
    const savepoint = createSavepoint(start, [], new Date(), versionBadgeLabel(), null);
    assert.ok(persistSavepoint(dom.window.localStorage as unknown as Storage, savepoint), 'seed savepoint must persist to localStorage');

    // Simulate a fully healthy inc1 mirror: the EXACT SAME bytes localStorage
    // holds are already sitting in IndexedDB too — equal freshness, not stale.
    const store = getDefaultSaveStore();
    const raw = dom.window.localStorage.getItem(`${SAVEPOINT_KEY_PREFIX}.0`)!;
    await store.setItem(`${SAVEPOINT_KEY_PREFIX}.0`, raw);

    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimProvider, useSim } = await import('../src/sim/store.tsx');

    let latestState: any = null;
    let renderCount = 0;
    function Probe() {
      const { state } = useSim();
      latestState = state;
      renderCount++;
      return null;
    }
    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);
    act(() => {
      root.render(React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }));
    });

    const bootBuildingCount = latestState.buildings.length;
    const bootTick = latestState.tick;

    // Give the async IDB-freshness check every chance to (wrongly) fire a
    // second hydrate — real macrotask waits, well past any microtask chain
    // the freshness check's Promise.all could need.
    await new Promise((r) => setTimeout(r, 500));

    assert.equal(latestState.buildings.length, bootBuildingCount, 'equal freshness must never change the booted building count');
    assert.equal(latestState.tick, bootTick, 'equal freshness must never change the booted tick — no double-load flicker');
    assert.ok(
      !container.textContent?.includes('Loading your city'),
      'no chunked-load overlay must ever appear when IndexedDB is merely EQUAL (not fresher) to what already booted',
    );

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});
