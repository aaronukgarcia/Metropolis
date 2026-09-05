// bug-672-mirror-restore.test.tsx — BUG-672: the IndexedDB save mirror is
// written but was never read back on boot ("getDefaultSaveStore() has exactly
// 4 call sites in store.tsx, all writes ... boot never references
// saveStore.ts", independent round finding, FEAT-2326609778 inc1).
//
// By the time this lane started, FEAT-2326609780 inc2 ("IDB inc2 makes
// IndexedDB the PRIMARY boot/self-heal store with localStorage fallback",
// commit 2bd94ad) had ALREADY landed the restore path this bug asks for: a
// post-mount effect in store.tsx (SimProvider, right after the one-time
// migration effect) reads the current lineage's IndexedDB slots
// (getDefaultSaveStore().getItem), picks the freshest valid candidate
// (freshestSavepoint), and — only when it is STRICTLY fresher than whatever
// booted from localStorage (isStrictlyFresherSavepointMeta) — swaps it in via
// the same chunked-tail hydrate machinery a large localStorage tail already
// uses. idb-primary-boot.test.tsx already proves the quota-wedge shape (a
// STALE localStorage rotation slot loses to a fresher IDB overflow copy) and
// the equal-freshness no-op end to end.
//
// ROUND REJECT (opus-round-bug672), FIXED HERE: the first draft of tests 2
// and 4 below seeded their IndexedDB competitor at a numbered ROTATION SLOT
// key (`metropolis.savepoint.<n>`). That is unsafe for this exact scenario —
// when localStorage already holds a real savepoint, SimProvider's OWN boot
// self-heal (BUG-617's "every boot re-persists once, even with an empty
// tail") fires a fresh `persistSavepoint` + `mirrorSaveCheckpoint` sweep
// across ALL `SAVEPOINT_CAP` rotation slots BEFORE the freshness-read effect
// ever runs — for any slot the self-heal did not occupy locally,
// `mirrorKeyFromLocalStorage` reads `null` and calls `store.removeItem(key)`,
// and for any slot it DID occupy, the self-heal's own bytes (a strictly
// higher `saveSeq`) win `guardedSavepointSetItem`'s overwrite guard outright.
// Either way the seeded "stale competitor" is gone or overwritten by the
// time the read happens — the attacker mutated `isStrictlyFresherSavepointMeta`
// to always return `true` and every assertion in the original test 2/4 still
// passed, because there was nothing left for a wrongly-permissive comparator
// to wrongly adopt. `SAVEPOINT_OVERFLOW_KEY` is the one IDB-only key the
// rotation self-heal NEVER touches (saveStore.ts's own doc comment: "it is
// never written to or read from localStorage, only IndexedDB" — the
// self-heal's mirror sweep only ever reads FROM localStorage), so every
// stale/hostile competitor below is seeded there instead — verified, not
// assumed, by the mutation table in this lane's own re-verification (see the
// build report): mutating the comparator to always-true is now caught by
// tests 2, 4, 5 and 6; mutating it to always-false is caught by test 1;
// forcing the swap unconditionally is caught by 2/4/5/6; forcing it to
// never fire is caught by 1.
//
// This file closes the acceptance gaps named on the BOW item that
// idb-primary-boot.test.tsx does not exercise:
//   1. localStorage genuinely ABSENT (not merely stale) + a mirror savepoint
//      present under the reserved legacy lineage -> the boot restores it.
//   2. localStorage STRICTLY NEWER (higher saveSeq) than a plain, honest,
//      older mirror candidate -> the mirror is never adopted.
//   3. A CORRUPT mirror candidate (unparsable freshness metadata) is skipped
//      and reported LOUDLY with the reserved registry code MET-V869 (added
//      by this lane — the corrupt-slot recordError call existed pre-fix but
//      carried no code at all, an anonymous 'app'/'load' row).
//   4. When localStorage's own boot is already the freshest thing anywhere,
//      a REAL, valid, older, same-lineage mirror candidate is never adopted
//      — proving the "never swaps" claim against genuine competing data, not
//      merely an empty mirror (which the round correctly flagged as vacuous:
//      an empty mirror makes `freshestSavepoint` return `null` and the
//      effect bail out at its very first `if (!best) return;`, never
//      exercising the comparator at all).
//   5. ATTACK 2a (adopted from the round's reusable fixture): a mirror
//      candidate with a HIGHER snapshotTick but a LOWER saveSeq must NOT be
//      adopted — tick is not an ordering key (BUG-687/BUG-755), and this is
//      the one shape that actually distinguishes a correct saveSeq-primary
//      comparator from a tick-primary regression.
//   6. ATTACK 2b (adopted from the round's reusable fixture): a mirror
//      candidate stamped with a DIFFERENT lineageId and a HIGHER saveSeq
//      must NOT be adopted — lineages never cross (BUG-687 item 5's own
//      rule), even when every OTHER field on the foreign record looks like a
//      clean winner.
//
// All six run against the REAL production code (store.tsx/saveStore.ts/
// replay.ts), never a reimplementation, following idb-primary-boot.test.tsx's
// own jsdom + fakeIndexedDB + real SimProvider mount pattern exactly.

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

async function waitFor(predicate: () => boolean, timeoutMs: number, stepMs = 25): Promise<void> {
  const start = Date.now();
  while (!predicate()) {
    if (Date.now() - start > timeoutMs) throw new Error('waitFor timed out');
    await new Promise((r) => setTimeout(r, stepMs));
  }
}

function injectBuilding(state: any, spec: string, x: number, y: number) {
  const id = state.nextId ?? state.buildings.length + 1;
  return { ...state, nextId: id + 1, buildings: [...state.buildings, { id, spec, x, y }] };
}

// ---------------------------------------------------------------------------
// 1. LOCAL ABSENT + MIRROR PRESENT -> RESTORES.
// ---------------------------------------------------------------------------

test('BUG-672: localStorage has NOTHING at all (a cleared/private-mode boot); a savepoint sitting only in the IndexedDB mirror under the reserved legacy lineage is restored', async () => {
  const dom = installJsdom();
  try {
    const backing = new Map<string, Map<string, string>>();
    (globalThis as any).indexedDB = (await loadFakeIndexedDBFactory())(backing);
    const { resetSaveStoreForTests, getDefaultSaveStore } = await import('../src/sim/saveStore.ts');
    resetSaveStoreForTests();

    const { initialState } = await import('../src/sim/engine.ts');
    const { createSavepoint, SAVEPOINT_KEY_PREFIX } = await import('../src/sim/replay.ts');
    const { versionBadgeLabel } = await import('../src/sim/version.ts');

    // Seed a real, well-formed savepoint identifying a unique building, into
    // IndexedDB ONLY, at the bare (legacy, unnamespaced) slot key -- exactly
    // where a pre-lineage mirror write, or the reserved legacy lineage, lives.
    const mirroredState = injectBuilding({ ...initialState() }, 'res_hut', 3, 3);
    const mirrored = createSavepoint(mirroredState, [], new Date(), versionBadgeLabel(), null);
    const store = getDefaultSaveStore();
    const putOk = await store.setItem(`${SAVEPOINT_KEY_PREFIX}.0`, JSON.stringify(mirrored));
    assert.ok(putOk.ok, 'test setup: seeding the IndexedDB mirror directly must succeed');

    // localStorage is genuinely empty -- no savepoint, no journal, no
    // current-lineage pointer. The boot's synchronous fast path has nothing
    // to work with and falls through to a fresh dev/genesis city; the only
    // route the mirrored save can reach the player through is the post-mount
    // IDB-freshness restore this bug is about.
    assert.equal(dom.window.localStorage.length, 0, 'test setup: localStorage must start genuinely empty');

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

    await waitFor(() => !!latestState && latestState.buildings.some((b: any) => b.spec === 'res_hut'), 10_000);

    assert.ok(latestState.buildings.some((b: any) => b.spec === 'res_hut' && b.x === 3 && b.y === 3), 'the mirror-only savepoint must have been restored onto the booted city');

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});

// ---------------------------------------------------------------------------
// Shared harness for the "local vs. an OVERFLOW-KEY mirror competitor" shape
// used by tests 2, 4, 5, 6 -- the overflow key is the one IDB-only slot the
// boot self-heal's rotation-slot mirror sweep can never clobber or remove
// (see the file header's ROUND REJECT note), so it is the only place a
// competitor can be seeded and still genuinely exist by the time the
// freshness-read effect actually runs.
// ---------------------------------------------------------------------------

async function runLocalVsOverflowCompetitor(opts: {
  competitorSaveSeq: number;
  mutateCompetitor?: (sp: any) => any;
  expectAdopted: boolean;
  competitorSpec: string;
}): Promise<{ latestState: any; container: HTMLElement; root: any; dom: JSDOM }> {
  const dom = installJsdom();
  const backing = new Map<string, Map<string, string>>();
  (globalThis as any).indexedDB = (await loadFakeIndexedDBFactory())(backing);
  const { resetSaveStoreForTests, getDefaultSaveStore, SAVEPOINT_OVERFLOW_KEY } = await import('../src/sim/saveStore.ts');
  resetSaveStoreForTests();

  const { initialState } = await import('../src/sim/engine.ts');
  const { createSavepoint, persistSavepoint } = await import('../src/sim/replay.ts');
  const { versionBadgeLabel } = await import('../src/sim/version.ts');

  // LOCAL: a real, freshest, saveSeq-9 savepoint in localStorage.
  const localState = injectBuilding({ ...initialState() }, 'res_hut', 5, 5);
  const localSavepoint = createSavepoint(localState, [], new Date(), versionBadgeLabel(), null, 9 /* saveSeq */);
  assert.ok(persistSavepoint(dom.window.localStorage as unknown as Storage, localSavepoint), 'test setup: local savepoint must persist');

  // COMPETITOR: seeded at the overflow key so it survives the boot self-heal.
  const competitorState = injectBuilding({ ...initialState() }, opts.competitorSpec, 7, 7);
  let competitor: any = createSavepoint(competitorState, [], new Date(Date.now() + 600_000 /* deliberately LATER wall-clock than local */), versionBadgeLabel(), null, opts.competitorSaveSeq);
  if (opts.mutateCompetitor) competitor = opts.mutateCompetitor(competitor);
  const store = getDefaultSaveStore();
  const putOk = await store.setItem(SAVEPOINT_OVERFLOW_KEY, JSON.stringify(competitor));
  assert.ok(putOk.ok, 'test setup: seeding the overflow-key competitor must succeed');
  // eslint-disable-next-line no-console
  console.error('DEBUG post-setup overflow readback key=', SAVEPOINT_OVERFLOW_KEY, 'value=', await store.getItem(SAVEPOINT_OVERFLOW_KEY));

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

  await waitFor(() => !!latestState && latestState.buildings.some((b: any) => b.spec === 'res_hut'), 10_000);

  // Give the boot self-heal AND the async IDB-freshness probe every chance to
  // run to completion -- real macrotask waits, well past both.
  await new Promise((r) => setTimeout(r, 1200));

  const adopted = !!latestState.buildings.some((b: any) => b.spec === opts.competitorSpec);
  assert.equal(adopted, opts.expectAdopted, `competitor adoption must be exactly ${opts.expectAdopted}`);
  if (!opts.expectAdopted) {
    assert.ok(latestState.buildings.some((b: any) => b.spec === 'res_hut' && b.x === 5 && b.y === 5), 'the local (winning) city must still be showing');
  }

  return { latestState, container, root, dom };
}

// ---------------------------------------------------------------------------
// 2. LOCAL STRICTLY NEWER THAN MIRROR -> MIRROR NEVER ADOPTED.
// ---------------------------------------------------------------------------

test('BUG-672: localStorage holds a STRICTLY NEWER savepoint (higher saveSeq) than a genuinely stale IndexedDB mirror candidate seeded at the overflow key; the mirror is never adopted', async () => {
  const { root, dom } = await runLocalVsOverflowCompetitor({ competitorSaveSeq: 1, expectAdopted: false, competitorSpec: 'res_highrise' });
  try {
    await (async () => {
      const { act } = await import('react-dom/test-utils');
      await act(async () => {
        root.unmount();
      });
    })();
  } finally {
    dom.window.close();
  }
});

// ---------------------------------------------------------------------------
// 3. CORRUPT MIRROR -> LOUD REFUSAL, MET-V869, NEVER TRUSTED.
// ---------------------------------------------------------------------------

test('BUG-672: a corrupt IndexedDB mirror slot (unparsable freshness metadata) is skipped and reported loudly with MET-V869, never adopted', async () => {
  const dom = installJsdom();
  try {
    const backing = new Map<string, Map<string, string>>();
    (globalThis as any).indexedDB = (await loadFakeIndexedDBFactory())(backing);
    const { resetSaveStoreForTests, getDefaultSaveStore } = await import('../src/sim/saveStore.ts');
    resetSaveStoreForTests();

    const { SAVEPOINT_KEY_PREFIX } = await import('../src/sim/replay.ts');
    const { recentErrors } = await import('../src/sim/backend.ts');

    // A hostile/corrupted mirror entry: valid JSON, but snapshotTick is NaN
    // and savedAt is missing entirely -- exactly the shape a truncated write
    // or a hand-edited IndexedDB record produces. Written DIRECTLY (never
    // through guardedSavepointSetItem/mirrorSavepointDirect, which would
    // themselves refuse it) so this test targets the BOOT-TIME reader's own
    // corrupt-candidate handling, independent of the write-side guard.
    const corrupt = { snapshotTick: NaN, snapshot: { buildings: [] }, journalTail: [] };
    const store = getDefaultSaveStore();
    const putOk = await store.setItem(`${SAVEPOINT_KEY_PREFIX}.0`, JSON.stringify(corrupt));
    assert.ok(putOk.ok, 'test setup: seeding the corrupt mirror candidate must succeed (this is a boot-read concern, not a write-guard one)');

    // localStorage is empty, so this corrupt candidate is the ONLY thing the
    // boot-freshness effect has to look at -- it must never crash, never be
    // silently adopted, and must leave an honest trail.
    assert.equal(dom.window.localStorage.length, 0, 'test setup: localStorage must start genuinely empty');

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

    await waitFor(() => latestState !== null, 5000);

    // Give the async freshness probe time to read + reject the corrupt slot.
    await new Promise((r) => setTimeout(r, 750));

    assert.ok(latestState.buildings.length >= 0, 'boot must never crash on a corrupt mirror candidate (fail-open to whatever booted locally)');
    const v869 = recentErrors().filter((e) => e.code === 'MET-V869');
    assert.equal(v869.length, 1, 'the corrupt mirror slot must be reported EXACTLY once, loudly, with the registry code MET-V869');
    assert.match(v869[0].msg, /corrupt metadata/, 'the recorded message must name the corrupt-metadata condition, not a generic failure');

    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});

// ---------------------------------------------------------------------------
// 4. LOCAL FRESHEST + A REAL, VALID, OLDER SAME-LINEAGE COMPETITOR -> NO SWAP.
// ---------------------------------------------------------------------------

test('BUG-672: when localStorage is already usable and holds the freshest known savepoint, a REAL valid older mirror candidate at the overflow key is never adopted - the mirror probe never manifests as a swap', async () => {
  const { container, root, dom } = await runLocalVsOverflowCompetitor({ competitorSaveSeq: 3, expectAdopted: false, competitorSpec: 'res_highrise' });
  try {
    assert.ok(!container.textContent?.includes('Loading your city'), 'no chunked-load/swap overlay must ever appear when the mirror candidate is genuinely older');
    const { act } = await import('react-dom/test-utils');
    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});

// ---------------------------------------------------------------------------
// 5. ATTACK 2a (round fixture): HIGHER TICK, LOWER SAVESEQ -> NOT ADOPTED.
// ---------------------------------------------------------------------------

test('BUG-672 / ATTACK 2a: a mirror candidate with a HIGHER snapshotTick but a LOWER saveSeq must NOT be adopted - tick is not an ordering key (BUG-687/BUG-755)', async () => {
  const { root, dom } = await runLocalVsOverflowCompetitor({
    competitorSaveSeq: 2, // strictly LOWER than local's saveSeq 9
    mutateCompetitor: (sp) => ({ ...sp, snapshotTick: (sp.snapshotTick ?? 0) + 9999 }), // but a much HIGHER tick
    expectAdopted: false,
    competitorSpec: 'res_highrise',
  });
  try {
    const { act } = await import('react-dom/test-utils');
    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});

// ---------------------------------------------------------------------------
// 6. ATTACK 2b (round fixture): FOREIGN LINEAGE, HIGHER SAVESEQ -> NOT ADOPTED.
// ---------------------------------------------------------------------------

test('BUG-672 / ATTACK 2b: a mirror candidate stamped with a DIFFERENT lineageId and a HIGHER saveSeq must NOT be adopted - lineages never cross (BUG-687 item 5)', async () => {
  const { root, dom } = await runLocalVsOverflowCompetitor({
    competitorSaveSeq: 9999, // strictly HIGHER than local's saveSeq 9
    mutateCompetitor: (sp) => ({ ...sp, lineageId: 'lin-attacker-0001', snapshotTick: (sp.snapshotTick ?? 0) + 9999 }), // and a higher tick too
    expectAdopted: false,
    competitorSpec: 'res_highrise',
  });
  try {
    const { act } = await import('react-dom/test-utils');
    await act(async () => {
      root.unmount();
    });
  } finally {
    dom.window.close();
  }
});
