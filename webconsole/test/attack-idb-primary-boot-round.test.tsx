// attack-idb-primary-boot-round.test.tsx — INDEPENDENT DESTRUCTIVE ROUND,
// FEAT-2326609780 inc2 (IDB-PRIMARY BOOT).
//
// The attacker is NOT the author (GR#23 independence amendment). Everything
// here targets the estate's NEW machinery: mirrorSavepointDirect /
// SAVEPOINT_OVERFLOW_KEY (saveStore.ts), and store.tsx's
// mirrorAfterPersist / bootSavepointMeta / isStrictlyFresherSavepointMeta /
// freshestSavepoint / the post-mount IDB-freshness swap effect /
// pendingTailReplay.swapBaseState.
//
// This is SAVE-INTEGRITY machinery for a player whose real city is
// quota-wedged TODAY, so the attack priorities are save-eaters first:
//   A. THE SWAP RACE — a boot that has BOTH a localStorage tail replay
//      pending AND a strictly-fresher IDB savepoint. Who wins, and is the
//      outcome deterministic?
//   B. JOURNAL COHERENCE after a swap — the swap replaces STATE but not the
//      journal lineage the running app carries.
//   C. THE OVERFLOW SLOT IS UNGUARDED — mirrorSavepointDirect overwrites the
//      durable rescue copy with no freshness check at all.
//   D. Comparator edge cases vs persistSavepoint's own BUG-469 rule.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';

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

function pin(fn: () => void) {
  try {
    fn();
  } catch (e) {
    console.error('FINDING >>> ' + ((e as Error).message || String(e)));
    throw e;
  }
}

async function waitFor(predicate: () => boolean, timeoutMs: number, stepMs = 25): Promise<void> {
  const start = Date.now();
  while (!predicate()) {
    if (Date.now() - start > timeoutMs) throw new Error('waitFor timed out');
    await new Promise((r) => setTimeout(r, stepMs));
  }
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

// ---------------------------------------------------------------------------
// A. THE SWAP RACE: a large localStorage tail replay pending AT THE SAME TIME
//    as a strictly-fresher IndexedDB savepoint. This is Aaron's EXACT shape.
//
//    The author's claim is that the swap fires "only if no other boot flow
//    already owns the surface" (`hasPendingBootWorkRef`). That check is read
//    AFTER the async IDB read resolves — so the outcome is decided by a race
//    between an IndexedDB round-trip and a requestAnimationFrame-paced
//    chunked replay. This test pins WHICH outcome actually happens and
//    whether ANY of the player's data is lost either way.
// ---------------------------------------------------------------------------
test('ATTACK A: boot with BOTH a large localStorage tail AND a strictly-fresher IndexedDB savepoint — the player must never end up with a city smaller than either source', async () => {
  const dom = installJsdom();
  try {
    const backing = new Map<string, Map<string, string>>();
    (globalThis as any).indexedDB = (await loadFakeIndexedDBFactory())(backing);
    const { resetSaveStoreForTests, getDefaultSaveStore } = await import('../src/sim/saveStore.ts');
    resetSaveStoreForTests();

    const { initialState, reducer } = await import('../src/sim/engine.ts');
    const { emptyJournal, recordAction } = await import('../src/sim/journal.ts');
    const { createSavepoint, persistSavepoint, SAVEPOINT_KEY_PREFIX } = await import('../src/sim/replay.ts');
    const { versionBadgeLabel } = await import('../src/sim/version.ts');

    const running = versionBadgeLabel();
    const start = { ...initialState(), unlockedAll: true, funds: 5_000_000_000 };

    // localStorage: a snapshot at tick T with a LARGE tail carrying the city
    // 800 actions forward — exactly the BUG-617/BUG-669 boot shape.
    const tailActions = buildGrowthTail(120);
    let journal = emptyJournal();
    for (const a of tailActions) journal = recordAction(journal, start.tick, a as never);
    const lsSavepoint = createSavepoint(start, journal.entries, new Date(), running, null);
    assert.ok(persistSavepoint(dom.window.localStorage as unknown as Storage, lsSavepoint), 'seed localStorage savepoint');

    // What the localStorage lineage is genuinely worth once its tail replays:
    let lsTruth = start;
    for (const a of tailActions) lsTruth = reducer(lsTruth, a as never);
    const lsTruthBuildings = lsTruth.buildings.length;
    assert.ok(lsTruthBuildings > start.buildings.length + 200, 'sanity: the tail must be a real, large growth tail');

    // IndexedDB: a savepoint that is STRICTLY FRESHER by the estate's own
    // rule (snapshotTick + 1) but whose snapshot is the SMALL pre-tail city.
    // This is reachable in the wedge: the overflow slot is written from a
    // savepoint whose own tick advanced (a tick or two of play) while the
    // localStorage rotation stayed stuck on an older SNAPSHOT that still
    // carries the player's whole tail.
    const idbState = { ...start, tick: start.tick + 1 };
    const idbSavepoint = createSavepoint(idbState, [], new Date(Date.now() + 1000), running, null);
    const store = getDefaultSaveStore();
    assert.ok((await store.setItem(`${SAVEPOINT_KEY_PREFIX}.0`, JSON.stringify(idbSavepoint))).ok, 'seed IDB slot 0');

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

    // Let BOTH flows settle completely.
    await waitFor(() => !!latestState, 5000);
    await new Promise((r) => setTimeout(r, 3000));

    const finalBuildings = latestState.buildings.length;
    // The player owns the union of what both sources prove they built. The
    // ONLY acceptable outcomes are "the tail replay landed" (>= lsTruth) —
    // the IDB snapshot contains nothing the tail does not. Landing on the
    // small IDB snapshot means the player's 800 recorded actions were thrown
    // away by the swap.
    assert.ok(
      finalBuildings >= lsTruthBuildings,
      `SAVE-EATER: the boot converged to ${finalBuildings} buildings, but the localStorage lineage (snapshot + its own journal tail) is worth ${lsTruthBuildings}. ` +
        'The IDB-freshness swap discarded the pending large-tail replay.',
    );

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});

// ---------------------------------------------------------------------------
// A2. The other half of the same race: when the tail replay DOES win, the
//     freshness check is never re-evaluated (idbFreshnessCheckedRef is
//     already latched), so the wedge the increment exists to close persists
//     for exactly the player who needs it — AND the swap can still fire
//     afterwards against a STALE bootSavepointMeta.
//
//     This test makes the "tail replay owns the surface" branch certain by
//     giving the tail 800 actions and asserting the documented, deterministic
//     end state: the app must end on the LATEST lineage, and the durable
//     store must not be left holding something the app silently ignored.
// ---------------------------------------------------------------------------
test('ATTACK A2: after a boot-time tail replay completes, a strictly-fresher IndexedDB savepoint must not be silently abandoned forever', async () => {
  const dom = installJsdom();
  try {
    const backing = new Map<string, Map<string, string>>();
    (globalThis as any).indexedDB = (await loadFakeIndexedDBFactory())(backing);
    const { resetSaveStoreForTests, getDefaultSaveStore } = await import('../src/sim/saveStore.ts');
    resetSaveStoreForTests();

    const { initialState, reducer } = await import('../src/sim/engine.ts');
    const { emptyJournal, recordAction } = await import('../src/sim/journal.ts');
    const { createSavepoint, persistSavepoint, SAVEPOINT_KEY_PREFIX } = await import('../src/sim/replay.ts');
    const { versionBadgeLabel } = await import('../src/sim/version.ts');
    const { SAVEPOINT_OVERFLOW_KEY } = await import('../src/sim/saveStore.ts');

    const running = versionBadgeLabel();
    const start = { ...initialState(), unlockedAll: true, funds: 5_000_000_000 };

    // localStorage: small snapshot + a large tail (slow, rAF-paced replay).
    const tailActions = buildGrowthTail(120);
    let journal = emptyJournal();
    for (const a of tailActions) journal = recordAction(journal, start.tick, a as never);
    assert.ok(
      persistSavepoint(dom.window.localStorage as unknown as Storage, createSavepoint(start, journal.entries, new Date(), running, null)),
      'seed localStorage savepoint with a large tail',
    );

    // IndexedDB overflow: the REAL rescue copy — strictly fresher AND
    // genuinely bigger than anything localStorage can reconstruct.
    let idbTruth = start;
    for (const a of tailActions) idbTruth = reducer(idbTruth, a as never);
    // Two extra buildings only the IDB copy knows about.
    idbTruth = reducer(idbTruth, { type: 'place', spec: 'road', x: 350, y: 201 } as never);
    idbTruth = reducer(idbTruth, { type: 'place', spec: 'res_hut', x: 350, y: 200 } as never);
    idbTruth = { ...idbTruth, tick: start.tick + 5 };
    const idbBuildings = idbTruth.buildings.length;
    const store = getDefaultSaveStore();
    assert.ok(
      (await store.setItem(SAVEPOINT_OVERFLOW_KEY, JSON.stringify(createSavepoint(idbTruth, [], new Date(Date.now() + 5000), running, null)))).ok,
      'seed IDB overflow slot',
    );

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
    await waitFor(() => !!latestState, 5000);
    await new Promise((r) => setTimeout(r, 4000));
    console.error(`[A2] settled=${latestState.buildings.length} idbRescue=${idbBuildings} lsPreTail=${start.buildings.length}`);

    pin(() => assert.equal(
      latestState.buildings.length,
      idbBuildings,
      `SAVE-EATER (the wedge is NOT closed for the player who needs it): the durable IndexedDB copy holds ${idbBuildings} buildings but the app settled on ` +
        `${latestState.buildings.length}. A pending boot-time tail replay makes the freshness check bail out permanently (idbFreshnessCheckedRef is already latched), ` +
        'so the fresher durable savepoint is never adopted — on EVERY subsequent boot too, because the localStorage tail is re-created by the self-heal each time.',
    ));
    // Also unused var guard
    void SAVEPOINT_KEY_PREFIX;

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});

// ---------------------------------------------------------------------------
// B. JOURNAL COHERENCE AFTER A SWAP.
//
//    The swap replaces STATE from the IndexedDB lineage but leaves `journal`
//    (and `lastSaveIndex`) exactly as localStorage's boot produced them.
//    Metropolis's headline recovery feature is genesis replay / hard-reset-
//    replay (FEAT-1972079897, GR#27): replaying the journal MUST reproduce
//    the city the player is looking at. This test folds the post-swap journal
//    from genesis through the real reducer and compares.
// ---------------------------------------------------------------------------
test('ATTACK B: after an IDB-freshness swap, a genesis replay of the live journal must reproduce the city the player is now looking at', async () => {
  const dom = installJsdom();
  try {
   try {
    const backing = new Map<string, Map<string, string>>();
    (globalThis as any).indexedDB = (await loadFakeIndexedDBFactory())(backing);
    const { resetSaveStoreForTests, getDefaultSaveStore, SAVEPOINT_OVERFLOW_KEY } = await import('../src/sim/saveStore.ts');
    resetSaveStoreForTests();

    const { initialState, reducer } = await import('../src/sim/engine.ts');
    const { emptyJournal, recordAction, persistJournal, loadJournal } = await import('../src/sim/journal.ts');
    const { createSavepoint, persistSavepoint, SAVEPOINT_KEY_PREFIX } = await import('../src/sim/replay.ts');
    const { versionBadgeLabel } = await import('../src/sim/version.ts');

    const running = versionBadgeLabel();
    const start = { ...initialState(), unlockedAll: true, funds: 5_000_000_000 };

    // localStorage lineage: three placements, journalled AND folded into the
    // snapshot (the ordinary healthy shape — snapshot is current, tail empty).
    const lsActions = [
      { type: 'place', spec: 'road', x: 10, y: 11 },
      { type: 'place', spec: 'res_hut', x: 10, y: 10 },
      { type: 'place', spec: 'res_hut', x: 13, y: 10 },
    ];
    let lsState = start;
    let journal = emptyJournal();
    for (const a of lsActions) {
      lsState = reducer(lsState, a as never);
      journal = recordAction(journal, lsState.tick, a as never);
    }
    assert.ok(
      persistSavepoint(dom.window.localStorage as unknown as Storage, createSavepoint(lsState, [], new Date(), running, null)),
      'seed localStorage savepoint',
    );
    assert.ok(persistJournal(dom.window.localStorage as unknown as Storage, journal), 'seed the localStorage journal');

    // IndexedDB lineage: a DIFFERENT, strictly fresher city (tick +10) whose
    // buildings were never in the localStorage journal.
    let idbState = start;
    const idbActions = [
      { type: 'place', spec: 'road', x: 40, y: 41 },
      { type: 'place', spec: 'res_hut', x: 40, y: 40 },
      { type: 'place', spec: 'res_hut', x: 43, y: 40 },
      { type: 'place', spec: 'res_hut', x: 46, y: 40 },
      { type: 'place', spec: 'res_hut', x: 49, y: 40 },
    ];
    for (const a of idbActions) idbState = reducer(idbState, a as never);
    // NOTE: deliberately NOT hand-editing `tick` (that would risk tripping the
    // consistency gate and confusing the finding). Freshness is expressed the
    // OTHER legal way the estate's own comparator supports: EQUAL tick, LATER
    // savedAt — precisely the tie-break rule it documents.
    const store = getDefaultSaveStore();
    assert.ok(
      (await store.setItem(SAVEPOINT_OVERFLOW_KEY, JSON.stringify(createSavepoint(idbState, [], new Date(Date.now() + 9000), running, null)))).ok,
      'seed the IDB OVERFLOW slot with the fresher, DIFFERENT lineage (the wedge shape — and the only IDB slot the inc1 one-time migration does not overwrite with localStorage bytes, see FINDING E)',
    );
    {
      // Rule out a test artefact: prove the seeded IDB savepoint restores
      // cleanly through the EXACT machinery the swap uses.
      const { prepareRestoreForChunkedTail } = await import('../src/sim/replay.ts');
      const raw = JSON.stringify(createSavepoint(idbState, [], new Date(Date.now() + 9000), running, null));
      const shim = { getItem: (k: string) => (k === `${SAVEPOINT_KEY_PREFIX}.0` ? raw : null), setItem: () => {}, removeItem: () => {} };
      const probe = prepareRestoreForChunkedTail(shim as never);
      console.error(`[B] shim-restore probe: success=${probe.success} reason=${probe.reason ?? '-'} buildings=${probe.state?.buildings.length ?? -1}`);
      assert.ok(probe.success, `test setup: the seeded IDB savepoint must restore cleanly (${probe.reason})`);
    }

    const React = await import('react');
    const { createRoot } = await import('react-dom/client');
    const { act } = await import('react-dom/test-utils');
    const { SimProvider, useSim } = await import('../src/sim/store.tsx');

    let latestState: any = null;
    function Probe() {
      const sim = useSim() as any;
      latestState = sim.state;
      return null;
    }
    const container = dom.window.document.getElementById('root')!;
    const root = createRoot(container);
    act(() => {
      root.render(React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }));
    });
    await waitFor(() => !!latestState, 5000);
    console.error(`[B] booted=${latestState.buildings.length} lsLineage=${lsState.buildings.length} idbLineage=${idbState.buildings.length}`);
    // Wait for the swap to land (the IDB lineage has 5 placements vs 3).
    try {
      await waitFor(() => latestState.buildings.length === idbState.buildings.length, 8000);
    } catch {
      console.error(`FINDING >>> [B] the IDB-freshness swap NEVER FIRED: still on ${latestState.buildings.length} buildings (localStorage lineage) after 8s, IDB holds ${idbState.buildings.length} at a strictly fresher tick (equal tick ${idbState.tick}, later savedAt).`);
    }
    // The journal a Rebuild-from-Genesis / hard-reset-replay actually reads
    // back is the PERSISTED one — the swap never rewrites it.
    const liveJournal = loadJournal(dom.window.localStorage as unknown as Storage);
    console.error(`[B] after settle: state=${latestState.buildings.length} persistedJournalEntries=${liveJournal.entries.length}`);

    // Now: does replaying the LIVE journal from genesis reproduce the LIVE city?
    // This is exactly what Config -> "Rebuild from genesis" / hard-reset-replay does.
    let replayed = { ...initialState(), unlockedAll: true, funds: 5_000_000_000 };
    for (const e of liveJournal.entries) replayed = reducer(replayed, e.action as never);

    pin(() => assert.equal(
      replayed.buildings.length,
      latestState.buildings.length,
      `SAVE-EATER: after the swap the live city has ${latestState.buildings.length} buildings but a genesis replay of the live journal produces ` +
        `${replayed.buildings.length}. The swap replaced STATE from the IndexedDB lineage without swapping/reconciling the JOURNAL, so a Rebuild from ` +
        'Genesis (or the hard-reset-replay feature, GR#27/FEAT-1972079897) silently reverts the player to the stale localStorage lineage.',
    ));

    await act(async () => {
      root.unmount();
    });
   } catch (e) {
     console.error('FINDING >>> [B] threw: ' + ((e as Error).stack || String(e)));
     throw e;
   }
  } finally {
    dom.window.close();
  }
});

// ---------------------------------------------------------------------------
// C. THE OVERFLOW SLOT IS COMPLETELY UNGUARDED.
//
//    In the wedge, the overflow slot is the ONLY copy of the advanced city
//    (localStorage's rotation is stuck by definition). `mirrorSavepointDirect`
//    does a bare `setItem` with NO freshness comparison — unlike
//    `persistSavepoint`, which refuses a stale overwrite outright (BUG-469).
//    So ANY later failed persist of an OLDER savepoint destroys the rescue
//    copy. Loading an older named save while wedged does exactly that.
// ---------------------------------------------------------------------------
test('ATTACK C: a later FAILED persist of an OLDER savepoint overwrites the IndexedDB overflow rescue copy — mirrorSavepointDirect has no BUG-469-style overwrite protection', async () => {
  const dom = installJsdom();
  try {
    const backing = new Map<string, Map<string, string>>();
    (globalThis as any).indexedDB = (await loadFakeIndexedDBFactory())(backing);
    const { resetSaveStoreForTests, getDefaultSaveStore, mirrorSavepointDirect, SAVEPOINT_OVERFLOW_KEY } = await import('../src/sim/saveStore.ts');
    resetSaveStoreForTests();

    const { initialState, reducer } = await import('../src/sim/engine.ts');
    const { createSavepoint } = await import('../src/sim/replay.ts');
    const { versionBadgeLabel } = await import('../src/sim/version.ts');

    const running = versionBadgeLabel();
    const start = { ...initialState(), unlockedAll: true, funds: 5_000_000_000 };

    // The rescue copy: the advanced city that ONLY IndexedDB holds.
    let advanced = start;
    for (const a of buildGrowthTail(50)) advanced = reducer(advanced, a as never);
    advanced = { ...advanced, tick: 900 };
    const rescue = createSavepoint(advanced, [], new Date(), running, null);

    const store = getDefaultSaveStore();
    assert.ok(await mirrorSavepointDirect(store, JSON.stringify(rescue)), 'the rescue copy must land in the overflow slot');
    assert.equal(JSON.parse((await store.getItem(SAVEPOINT_OVERFLOW_KEY))!).snapshotTick, 900);

    // The player now loads an OLDER named save (or any older-lineage
    // savepoint) while still wedged: persistSavepoint fails, so
    // mirrorAfterPersist routes it straight to mirrorSavepointDirect.
    const older = createSavepoint({ ...start, tick: 100 }, [], new Date(), running, null);
    await mirrorSavepointDirect(store, JSON.stringify(older));

    const nowInOverflow = JSON.parse((await store.getItem(SAVEPOINT_OVERFLOW_KEY))!);
    pin(() => assert.equal(
      nowInOverflow.snapshotTick,
      900,
      `SAVE-EATER: the overflow slot now holds tick ${nowInOverflow.snapshotTick} (${nowInOverflow.snapshot.buildings.length} buildings) — the tick-900 rescue ` +
        `copy (${advanced.buildings.length} buildings) was silently destroyed by an OLDER failed persist. persistSavepoint refuses exactly this write ` +
        '(BUG-469 overwrite protection); mirrorSavepointDirect does not, and in the wedge the overflow slot is the ONLY surviving copy.',
    ));
  } finally {
    dom.window.close();
  }
});

// ---------------------------------------------------------------------------
// D. COMPARATOR EDGE CASES — hostile / hand-edited / pre-inc1 metadata.
//    The estate's own test only feeds well-formed values.
// ---------------------------------------------------------------------------
test('ATTACK D: isStrictlyFresherSavepointMeta vs hostile metadata (missing/NaN savedAt, NaN tick) must never claim a garbage candidate is fresher', async () => {
  const dom = installJsdom();
  try {
    const { isStrictlyFresherSavepointMeta, freshestSavepoint } = await import('../src/sim/store.tsx');
    const booted = { snapshotTick: 500, savedAt: '2026-09-04T00:00:00.000Z' };

    // A hand-edited / pre-inc1 record with no savedAt at all.
    const noSavedAt = { snapshotTick: 500, savedAt: undefined as unknown as string };
    pin(() => assert.equal(
      isStrictlyFresherSavepointMeta(noSavedAt, booted),
      false,
      'a candidate with no savedAt must never win an equal-tick comparison',
    ));

    // NaN tick — `NaN > 500` and `NaN === 500` are both false, so this is safe
    // by accident rather than by design; pin it so it stays safe.
    pin(() => assert.equal(isStrictlyFresherSavepointMeta({ snapshotTick: NaN, savedAt: 'z' }, booted), false, 'NaN-tick candidate must not win'));
    // ...but a NaN on the BOOTED side lets ANY candidate through:
    pin(() => assert.equal(
      isStrictlyFresherSavepointMeta({ snapshotTick: 1, savedAt: 'a' }, { snapshotTick: NaN, savedAt: 'z' }),
      false,
      'a corrupt (NaN-tick) booted savepoint must not let an ancient candidate win — NaN comparisons are all false, which is the safe direction here',
    ));

    // freshestSavepoint over a corrupt entry must not throw and must not
    // prefer the corrupt one.
    const sp = (tick: any, savedAt: any) => ({ snapshotTick: tick, savedAt, snapshot: {}, journalTail: [] }) as any;
    const best = freshestSavepoint([sp(NaN, 'z'), sp(7, '2026-01-01T00:00:00.000Z')]);
    pin(() => assert.equal(best?.snapshotTick, 7, 'a NaN-tick candidate must never be chosen over a well-formed one'));

    // DIVERGENCE FROM persistSavepoint (BUG-469): persistSavepoint accepts an
    // equal-tick write when savedAt is >= (see replay.ts `incomingIsNewer`),
    // while this comparator requires strictly >. The estate's header claims
    // the two "mirror ... exactly". They do not. Strict is the SAFE direction
    // for the swap (equal => no second hydrate), so this is a documentation
    // defect, not a behavioural one — pinned so a future "alignment" change
    // cannot silently make the swap fire on an equal savepoint.
    pin(() => assert.equal(
      isStrictlyFresherSavepointMeta({ snapshotTick: 5, savedAt: 'same' }, { snapshotTick: 5, savedAt: 'same' }),
      false,
      'equal tick AND equal savedAt must NOT be strictly fresher (persistSavepoint would ACCEPT this write — the two rules are deliberately different)',
    ));
  } finally {
    dom.window.close();
  }
});

// ---------------------------------------------------------------------------
// E. THE MIRROR HAS NO FRESHNESS GUARD EITHER — inc1's one-time migration
//    (and every mirrorSavepointCheckpoint) copies localStorage's bytes over
//    the IndexedDB ROTATION SLOTS unconditionally. If IndexedDB held a
//    FRESHER savepoint in one of those slots, the stale localStorage copy
//    destroys it — and it does so during mount, i.e. potentially BEFORE the
//    freshness effect gets to read them. Only the overflow key survives.
// ---------------------------------------------------------------------------
test('ATTACK E: mounting with a STALE localStorage savepoint destroys a FRESHER savepoint sitting in an IndexedDB rotation slot (mirror writes carry no BUG-469-style freshness guard)', async () => {
  const dom = installJsdom();
  try {
    const backing = new Map<string, Map<string, string>>();
    (globalThis as any).indexedDB = (await loadFakeIndexedDBFactory())(backing);
    const { resetSaveStoreForTests, getDefaultSaveStore } = await import('../src/sim/saveStore.ts');
    resetSaveStoreForTests();

    const { initialState, reducer } = await import('../src/sim/engine.ts');
    const { createSavepoint, persistSavepoint, SAVEPOINT_KEY_PREFIX } = await import('../src/sim/replay.ts');
    const { versionBadgeLabel } = await import('../src/sim/version.ts');

    const running = versionBadgeLabel();
    const start = { ...initialState(), unlockedAll: true, funds: 5_000_000_000 };

    // localStorage: the STALE city.
    assert.ok(
      persistSavepoint(dom.window.localStorage as unknown as Storage, createSavepoint(start, [], new Date(), running, null)),
      'seed the stale localStorage savepoint',
    );

    // IndexedDB rotation slot 0: the FRESHER city (later savedAt, more buildings).
    let fresher = start;
    for (const a of buildGrowthTail(20)) fresher = reducer(fresher, a as never);
    const fresherBuildings = fresher.buildings.length;
    const store = getDefaultSaveStore();
    assert.ok(
      (await store.setItem(`${SAVEPOINT_KEY_PREFIX}.0`, JSON.stringify(createSavepoint(fresher, [], new Date(Date.now() + 60_000), running, null)))).ok,
      'seed the FRESHER savepoint into an IndexedDB rotation slot',
    );

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
    await waitFor(() => !!latestState, 5000);
    await new Promise((r) => setTimeout(r, 2500));

    // Read it back the way the estate's own decodeSavepointRaw does (the
    // mirror re-encodes with saveCodec, so a raw JSON.parse would throw).
    const { decode } = await import('../src/sim/saveCodec.ts');
    const slot0 = JSON.parse(decode((await store.getItem(`${SAVEPOINT_KEY_PREFIX}.0`))!));
    console.error(`[E] after mount: IDB slot0 buildings=${slot0.snapshot.buildings.length} (seeded fresher=${fresherBuildings}, stale=${start.buildings.length}); live state=${latestState.buildings.length}`);
    pin(() =>
      assert.equal(
        slot0.snapshot.buildings.length,
        fresherBuildings,
        `SAVE-EATER: IndexedDB rotation slot 0 held the FRESHER city (${fresherBuildings} buildings) and now holds ${slot0.snapshot.buildings.length}. ` +
          'A mount with a stale localStorage savepoint mirrors the stale bytes over the durable slots unconditionally — mirrorSaveCheckpoint / ' +
          'migrateFromLocalStorage carry no freshness comparison, and the IDB-freshness effect races them with no ordering guarantee.',
      ),
    );
    pin(() =>
      assert.equal(
        latestState.buildings.length,
        fresherBuildings,
        `and the player was left on the stale city (${latestState.buildings.length} buildings) instead of the fresher durable one (${fresherBuildings}).`,
      ),
    );

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});
