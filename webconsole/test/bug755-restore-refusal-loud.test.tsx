// bug755-restore-refusal-loud.test.tsx — BUG-755 (P0, save loss), lead ruling
// part 3: a boot-time savepoint restore refusal on an EXISTING savepoint must
// never fall through to a fresh city SILENTLY. This mounts the REAL
// SimProvider (store.tsx) against a localStorage pre-seeded with a savepoint
// that fails a genuinely BLOCKING consistency check (duplicate building ids —
// buildings.ids-unique is NOT in consistency.ts's RESTORE_NONBLOCKING_CHECK_IDS),
// so BOTH prepareRestoreForChunkedTail and restoreFromSavepoint refuse it, and
// asserts:
//   (1) a registry-sourced error (MET-V868, GR#7) is recorded naming the
//       actual refusal reason,
//   (2) the booted state carries a visible trail (placeNotice — the same
//       field the FEAT-2326609784 NewsFeed already observes) rather than
//       looking exactly like a genuinely-fresh first-ever boot,
//   (3) a genuinely fresh boot (no savepoint at all) does NEITHER of the
//       above — the loud behaviour is conditioned on a savepoint having
//       existed and been refused, not fired unconditionally on every fresh
//       boot.
//
// RED-PROOF: runMutantSelfReinvoke reverts store.tsx's MET-V868
// recordError()/placeNotice block and re-runs this file's own positive test
// in a shadow copy — it must fail without that code.

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
  (globalThis as any).Blob = window.Blob;
  (globalThis as any).requestAnimationFrame = window.requestAnimationFrame.bind(window);
  (globalThis as any).cancelAnimationFrame = window.cancelAnimationFrame.bind(window);
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  window.HTMLAnchorElement.prototype.click = function () {};
  return dom;
}

async function loadFakeIndexedDBFactory(): Promise<(backing?: Map<string, Map<string, string>>) => any> {
  const specifier = './helpers/fakeIndexedDB.mjs';
  const mod: any = await import(specifier);
  return mod.createFakeIndexedDBFactory;
}

async function waitFor(predicate: () => boolean, timeoutMs: number, stepMs = 25): Promise<void> {
  const start = Date.now();
  while (!predicate()) {
    if (Date.now() - start > timeoutMs) throw new Error('waitFor timed out');
    await new Promise((r) => setTimeout(r, stepMs));
  }
}

const openRoots: Array<{ root: any; act: any }> = [];
async function closeAllRoots() {
  while (openRoots.length) {
    const { root, act } = openRoots.pop()!;
    try {
      await act(async () => {
        root.unmount();
      });
    } catch {
      /* already gone */
    }
  }
}

async function mountProvider(dom: JSDOM) {
  const React = await import('react');
  const { createRoot } = await import('react-dom/client');
  const { act } = await import('react-dom/test-utils');
  const { SimProvider, useSim } = await import('../src/sim/store.tsx');

  const seen: { ctx: any; state: any } = { ctx: null, state: null };
  function Probe() {
    const ctx = useSim();
    seen.ctx = ctx;
    seen.state = ctx.state;
    return null;
  }
  const container = dom.window.document.getElementById('root')!;
  const root = createRoot(container);
  await act(async () => {
    root.render(React.default.createElement(SimProvider, { children: React.default.createElement(Probe) }));
  });
  await waitFor(() => !!seen.state, 8000);
  openRoots.push({ root, act });
  return { seen, root, act };
}

/** A state that fails a REAL, blocking consistency check (duplicate building
 *  ids — never in RESTORE_NONBLOCKING_CHECK_IDS) while otherwise looking like
 *  a normal, freshly-initialized city (zero flows, so conservation itself
 *  never enters into it). */
async function buildBlockingCorruptState() {
  const { initialState } = await import('../src/sim/engine.ts');
  const base = initialState();
  return {
    ...base,
    buildings: [
      { id: 1, spec: 'res_hut', x: 5, y: 5 },
      { id: 1, spec: 'res_hut', x: 8, y: 8 }, // duplicate id -> buildings.ids-unique fails
    ],
  };
}

test('BUG-755 part 3: a savepoint that EXISTS but fails a BLOCKING consistency check records MET-V868 and leaves a visible trail, never a mute fresh boot', async () => {
  const dom = installJsdom();
  try {
    (globalThis as any).indexedDB = (await loadFakeIndexedDBFactory())(new Map());
    const { resetSaveStoreForTests } = await import('../src/sim/saveStore.ts');
    resetSaveStoreForTests();

    const { createSavepoint, persistSavepoint } = await import('../src/sim/replay.ts');
    const { recentErrors } = await import('../src/sim/backend.ts');
    const { runConsistencyChecks } = await import('../src/sim/consistency.ts');

    const storage = dom.window.localStorage as unknown as Storage;
    const corrupt = await buildBlockingCorruptState();

    // Test setup sanity: this really is a BLOCKING failure (not the BUG-755
    // cosmetic inflow-label one), so the restore SHOULD be refused.
    const preCheck = runConsistencyChecks(corrupt as any);
    assert.ok(preCheck.blockingFailures > 0, 'test setup: the corrupt state must fail a BLOCKING check');
    const idCheck = preCheck.checks.find((c) => c.id === 'buildings.ids-unique');
    assert.equal(idCheck?.ok, false, 'test setup: buildings.ids-unique must be the failing check');

    const before = recentErrors().length;
    assert.ok(persistSavepoint(storage, createSavepoint(corrupt as any, [], new Date(), 'test-build', null)));

    const m = await mountProvider(dom);
    await new Promise((r) => setTimeout(r, 300));

    // (1) LOUD registry error, naming the real reason.
    const errs = recentErrors();
    assert.ok(errs.length > before, 'a NEW error must have been recorded across this boot');
    const v868 = errs.find((e: any) => e.code === 'MET-V868');
    assert.ok(v868, 'MET-V868 (restore refused for an existing savepoint) must be recorded — the refusal must never be silent');
    assert.match(String((v868 as any).msg ?? ''), /consistency|blocking|failures/i, 'the recorded error must name the actual refusal reason, not a generic message');

    // (2) a visible trail on the booted state itself — never indistinguishable
    // from a genuinely-fresh, first-ever boot.
    assert.ok(
      typeof m.seen.state.placeNotice === 'string' && m.seen.state.placeNotice.length > 0,
      'the booted state must carry a visible notice (surfaced via the NewsFeed) when an existing savepoint was refused',
    );
    assert.match(m.seen.state.placeNotice, /could not be restored|restore/i);

    // The corrupt savepoint was NOT destroyed — it is still sitting in
    // storage, untouched, for future recovery.
    const stillThere = storage.getItem('metropolis.savepoint.0') ?? storage.getItem('metropolis.savepoint.legacy.0');
    assert.ok(stillThere, 'the refused savepoint must be left on disk, never deleted by the fallback');
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});

test('BUG-755 part 3 (negative control): a GENUINELY fresh boot (no savepoint at all) does NOT record MET-V868 or set a restore-refusal placeNotice', async () => {
  const dom = installJsdom();
  try {
    (globalThis as any).indexedDB = (await loadFakeIndexedDBFactory())(new Map());
    const { resetSaveStoreForTests } = await import('../src/sim/saveStore.ts');
    resetSaveStoreForTests();
    const { recentErrors } = await import('../src/sim/backend.ts');
    const { scanAllSavepointLineages } = await import('../src/sim/replay.ts');

    // HERMETIC PRECONDITION (CI red on push 15, run 33985969744, ubuntu/Node
    // 22 — this test passed 3/3 on Windows/Node 25 but failed on CI): the
    // ROOT CAUSE was NOT a platform difference in localStorage/jsdom — it was
    // this test's OWN diff logic. `backend.ts`'s `errorLog` is a MODULE-LEVEL
    // array, never reset between tests in this file (or any file); a prior
    // test in this SAME file (the part-3 positive test above) legitimately
    // records a real MET-V868 entry via `unshift` (newest-first). The old
    // check computed `newCount = errs.length - before` and then sliced
    // `errs.slice(0, newCount + 1)` — that trailing `+1` means when NO new
    // error was recorded (newCount === 0) it still grabbed ONE element:
    // index 0, which is the MOST RECENT entry from a PRIOR test, not this
    // one. Whether that stale index-0 entry happens to BE the prior test's
    // V868 (making this assertion falsely red) depends on exactly which
    // async effects fire in between — timing/scheduling that differs across
    // Node versions/OSes, which is why it passed on Windows/Node 25 and
    // failed on Linux/Node 22 despite being the SAME underlying bug, not a
    // product defect. FIX: diff by IDENTITY (`correlationId`), never by
    // array position/count — a stable set membership check is immune to
    // dedup/ordering/prior-test pollution regardless of platform.
    //
    // The OTHER direction was checked too (a genuine `scanAllSavepointLineages`
    // product defect misreading a non-savepoint key as a savepoint on Linux
    // — e.g. path separators, key ordering, a jsdom Storage quirk): keys are
    // plain ASCII strings (`metropolis.savepoint.<slot>`) with no OS path
    // separators involved, jsdom's Storage.key() enumerates a plain
    // insertion-ordered Map (not OS/filesystem dependent), and this test's
    // OWN precondition assertion below (asserted zero savepoint keys via the
    // SAME `scanAllSavepointLineages` the production code uses) is the direct
    // proof: it holds on a freshly constructed JSDOM/localStorage with
    // nothing written to it, on every platform — ruling out a scan-side
    // defect for this scenario.
    const storage = dom.window.localStorage as unknown as Storage;
    const preScan = scanAllSavepointLineages(storage as any);
    assert.deepEqual(preScan, [], `hermetic precondition failed: fresh storage must have ZERO savepoint keys before boot, found lineages: ${JSON.stringify(preScan)}`);

    const beforeIds = new Set(recentErrors().map((e: any) => e.correlationId));
    const m = await mountProvider(dom);
    await new Promise((r) => setTimeout(r, 300));

    const newErrors = recentErrors().filter((e: any) => !beforeIds.has(e.correlationId));
    const v868 = newErrors.find((e: any) => e.code === 'MET-V868');
    assert.ok(
      !v868,
      `a genuinely fresh install (no savepoint ever existed) must NOT report a restore refusal. ` +
        `NEW errors this boot: ${JSON.stringify(newErrors.map((e: any) => ({ code: e.code, msg: e.msg })))}. ` +
        `post-boot savepoint scan: ${JSON.stringify(scanAllSavepointLineages(storage as any))}`,
    );
    assert.ok(
      !(typeof m.seen.state.placeNotice === 'string' && /could not be restored/i.test(m.seen.state.placeNotice)),
      `a genuinely fresh boot must not carry a restore-refusal placeNotice, got: ${JSON.stringify(m.seen.state.placeNotice)}`,
    );
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});

test('BUG-755 P1 (independent round ATTACK A shape): a savepoint under a DIFFERENT lineage than the current pointer still records MET-V868 and sets the placeNotice', async () => {
  const dom = installJsdom();
  try {
    (globalThis as any).indexedDB = (await loadFakeIndexedDBFactory())(new Map());
    const { resetSaveStoreForTests } = await import('../src/sim/saveStore.ts');
    resetSaveStoreForTests();

    const { createSavepoint, persistSavepoint, readCurrentLineageId, LEGACY_LINEAGE_ID } = await import('../src/sim/replay.ts');
    const { recentErrors } = await import('../src/sim/backend.ts');
    const { initialState } = await import('../src/sim/engine.ts');

    const storage = dom.window.localStorage as unknown as Storage;

    // The ATTACK A shape exactly: a REAL, perfectly valid savepoint written
    // under lineage 'lineage-A', while the current-lineage POINTER is never
    // written (defaults to LEGACY_LINEAGE_ID) — a real mismatch, not a
    // corrupt save. `most` (readAllSavepoints under the CURRENT lineage) is
    // null, so the ORIGINAL `if (most)` guard alone would never fire.
    const city = { ...initialState(), lineageId: 'lineage-A', unlockedAll: true, funds: 5_000_000 };
    // createSavepoint copies `state.lineageId` onto the Savepoint automatically
    // (replay.ts), and persistSavepoint derives the storage KEY from the
    // savepoint's own lineageId — so this lands at
    // 'metropolis.savepoint.lineage-A.0', never the legacy keys.
    assert.ok(persistSavepoint(storage, createSavepoint(city, [], new Date(), 'test-build', null)), 'test setup: savepoint must persist');
    assert.ok(storage.getItem('metropolis.savepoint.lineage-A.0'), 'test setup sanity: the savepoint must be namespaced under lineage-A, not legacy');

    assert.equal(readCurrentLineageId(storage), LEGACY_LINEAGE_ID, 'test setup: the current-lineage pointer is untouched (defaults to legacy) — the mismatch');

    const before = recentErrors().length;
    const m = await mountProvider(dom);
    await new Promise((r) => setTimeout(r, 300));

    const errs = recentErrors();
    assert.ok(errs.length > before, 'a NEW error must have been recorded across this boot');
    const v868 = errs.find((e: any) => e.code === 'MET-V868');
    assert.ok(v868, 'MET-V868 must be recorded even when the mismatch is a LINEAGE POINTER drift, not a blocking consistency failure');
    assert.match(String((v868 as any).msg ?? ''), /lineage-A/, `the recorded error must name the OTHER lineage id it found the savepoint under: ${JSON.stringify(v868)}`);

    assert.ok(
      typeof m.seen.state.placeNotice === 'string' && /lineage mismatch/i.test(m.seen.state.placeNotice),
      `the booted state must carry a lineage-mismatch notice: ${m.seen.state.placeNotice}`,
    );

    // The real savepoint under lineage-A is untouched.
    assert.ok(storage.getItem('metropolis.savepoint.lineage-A.0'), 'the lineage-A savepoint must be left on disk, never deleted');
  } finally {
    await closeAllRoots();
    dom.window.close();
  }
});

// RED-PROOF for this file's positive test lives in
// bug755-restore-refusal-loud-redproof.test.mjs — a .tsx test file cannot be
// re-invoked as a fresh tsx-loaded child by testsupport/mutant.mjs's
// runMutantSelfReinvoke (it re-spawns a bare `node --test`, and Node's
// native module loader does not know how to load `.tsx`), so the RED-PROOF
// uses the SAME plain-child-process pattern as bug704-store-wiring.test.mjs:
// runWithMutant + `extraArgs: ['--import', 'tsx/esm']` around a childBody
// script that mounts SimProvider directly (no node:test involved in the
// child at all).
