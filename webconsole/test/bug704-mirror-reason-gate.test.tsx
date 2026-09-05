// bug704-mirror-reason-gate.test.tsx — BUG-704 round REJECT (P2, attacker
// opus-round-bug704): "mirrorAfterPersist(false) fires for BOTH
// stale-overwrite and storage-error; on the quota-wedge path (the direct
// mirror's whole purpose, FEAT-2326609780 inc2) localStorage made NO
// staleness judgement, yet the durable write is now gated against frozen
// slots, and stricter than gate A (newest-of-all vs rotation target) - a
// wedged city can stop saving anywhere silently."
//
// This file exercises the REAL production wiring (store.tsx's exported
// `mirrorAfterPersist`/`localSavepointBaselines`, never saveStore.ts's
// `mirrorSavepointDirect` directly) to prove:
//   1. `reason: 'storage-error'` (the genuine quota-wedge shape) does NOT
//      gate the durable write against localStorage's rotation-slot
//      baselines — it compares against the durable store's own prior
//      contents only, exactly as before BUG-704's fix, so a wedged city
//      keeps advancing the durable copy even while every localStorage slot
//      is frozen with older content.
//   2. `reason: 'stale-overwrite'` DOES gate against those baselines (the
//      BUG-704 fix itself, re-confirmed here through the reason-aware call).
//   3. Every durable refusal is RECORDED (GR#1/#17) via `recordError` —
//      never silent — while a successful (accepted) durable write records
//      nothing extra.

import { test, describe } from 'node:test';
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
  (globalThis as any).localStorage = window.localStorage;
  return dom;
}

async function waitFor(predicate: () => boolean | Promise<boolean>, timeoutMs = 3000, stepMs = 10): Promise<void> {
  const start = Date.now();
  for (;;) {
    if (await predicate()) return;
    if (Date.now() - start > timeoutMs) throw new Error('waitFor timed out');
    await new Promise((r) => setTimeout(r, stepMs));
  }
}

installJsdom();

const { persistSavepoint, persistSavepointWithReason, createSavepoint } = await import('../src/sim/replay.ts');
const { initialState } = await import('../src/sim/engine.ts');
const { mirrorAfterPersist } = await import('../src/sim/store.tsx');
const { getDefaultSaveStore, resetSaveStoreForTests } = await import('../src/sim/saveStore.ts');
const { recentErrors } = await import('../src/sim/backend.ts');

function overflowKeyFor(lineageId?: string): string {
  return !lineageId || lineageId === 'legacy' ? 'metropolis.savepoint.idbOnly' : `metropolis.savepoint.${lineageId}.idbOnly`;
}

/** Seed three fresher rotation-slot savepoints for `lineageId` directly into window.localStorage, then build a genuinely stale one that would lose a saveSeq comparison against them. */
function seedFresherSlotsAndBuildStale(lineageId: string): { stale: any } {
  const now = new Date(2026, 8, 5, 11);
  for (let seq = 5; seq <= 7; seq++) {
    const sp = createSavepoint({ ...initialState(), tick: 900 + seq, lineageId }, [], new Date(now.getTime() - (8 - seq) * 60_000), 'v1', null, seq);
    sp.lineageId = lineageId;
    const ok = persistSavepoint(window.localStorage, sp);
    assert.equal(ok, true, `seed persist saveSeq=${seq} must land`);
  }
  const stale = createSavepoint({ ...initialState(), tick: 100, lineageId }, [], now, 'v1', null, 3);
  stale.lineageId = lineageId;
  return { stale };
}

describe('BUG-704 round REJECT (P2): mirrorAfterPersist gates the durable write differently by reason', () => {
  test('storage-error: the durable write is NOT gated against frozen localStorage rotation slots (a quota-wedged city keeps advancing IndexedDB)', async () => {
    resetSaveStoreForTests();
    const lineageId = 'city-wedge-704';
    const { stale } = seedFresherSlotsAndBuildStale(lineageId);
    const store = getDefaultSaveStore();
    const overflowKey = overflowKeyFor(lineageId);
    assert.equal(await store.getItem(overflowKey), null, 'precondition: overflow slot starts empty');

    // Simulate the genuine quota-wedge shape directly: localStorage's write
    // failed with reason 'storage-error' (never a staleness judgement) even
    // though localStorage's OWN rotation slots (seeded above) happen to hold
    // fresher content than `stale`. Per the fix, this must NOT be gated
    // against those slots.
    mirrorAfterPersist(false, stale, 'storage-error');
    await waitFor(async () => (await store.getItem(overflowKey)) !== null);

    const landed = JSON.parse((await store.getItem(overflowKey)) as string);
    assert.equal(landed.saveSeq, 3, 'the quota-wedged savepoint reaches the durable overflow slot despite frozen, fresher localStorage slots');
  });

  test('stale-overwrite: the durable write IS gated against localStorage rotation-slot baselines', async () => {
    resetSaveStoreForTests();
    const lineageId = 'city-stale-704';
    const { stale } = seedFresherSlotsAndBuildStale(lineageId);
    // Make it a REAL stale-overwrite rejection (not just a simulated reason).
    const rejection = persistSavepointWithReason(window.localStorage, stale, new Date(2026, 8, 5, 11));
    assert.equal(rejection.reason, 'stale-overwrite', 'precondition: localStorage genuinely refuses this as stale');

    const store = getDefaultSaveStore();
    const overflowKey = overflowKeyFor(lineageId);
    assert.equal(await store.getItem(overflowKey), null, 'precondition: overflow slot starts empty');

    mirrorAfterPersist(false, stale, rejection.reason);
    // Give the fire-and-forget chain a chance to run; it must NOT write.
    for (let i = 0; i < 20; i++) await new Promise((r) => setTimeout(r, 5));

    assert.equal(await store.getItem(overflowKey), null, 'the durable overflow slot must stay refused — gated against the fresher localStorage baselines');
  });

  test('every durable refusal is recorded (GR#1/#17) — never silent', async () => {
    resetSaveStoreForTests();
    const lineageId = 'city-recorded-704';
    const { stale } = seedFresherSlotsAndBuildStale(lineageId);
    const rejection = persistSavepointWithReason(window.localStorage, stale, new Date(2026, 8, 5, 11));
    assert.equal(rejection.reason, 'stale-overwrite');

    const before = recentErrors().filter((e) => /Durable \(IndexedDB\) save refused/.test(e.msg)).length;
    mirrorAfterPersist(false, stale, rejection.reason);
    await waitFor(() => recentErrors().filter((e) => /Durable \(IndexedDB\) save refused/.test(e.msg)).length > before);
    const after = recentErrors().filter((e) => /Durable \(IndexedDB\) save refused/.test(e.msg));
    assert.ok(after.length > 0, 'a durable refusal must record a registry error, never fail silently');
  });

  test('an ACCEPTED durable write (storage-error path succeeding) records no durable-refusal error', async () => {
    resetSaveStoreForTests();
    const lineageId = 'city-accepted-704';
    const { stale } = seedFresherSlotsAndBuildStale(lineageId);
    const store = getDefaultSaveStore();
    const overflowKey = overflowKeyFor(lineageId);

    const before = recentErrors().filter((e) => /Durable \(IndexedDB\) save refused/.test(e.msg) && e.msg.includes(lineageId)).length;
    mirrorAfterPersist(false, stale, 'storage-error');
    await waitFor(async () => (await store.getItem(overflowKey)) !== null);
    const after = recentErrors().filter((e) => /Durable \(IndexedDB\) save refused/.test(e.msg) && e.msg.includes(lineageId)).length;
    assert.equal(after, before, 'an accepted durable write must not also record a refusal error');
  });
});
