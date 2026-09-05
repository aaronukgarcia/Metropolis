// bug704-savepoint-dual-store-gate.test.mjs — BUG-704 (P2, adjacent to the P0
// lineage-blind-saves estate BUG-687).
//
// THE DEFECT (found by the independent round against FEAT-2326609780, see
// attack-bug-436-round.test.mjs's F4 finding): saveStore.ts's
// `guardedSavepointSetItem` only ever compared an incoming write against the
// SAME IndexedDB key's own prior contents. `mirrorSavepointDirect` writes a
// localStorage-refused (stale) savepoint STRAIGHT into the durable store's
// per-lineage overflow slot — a key that has often never seen a competitor —
// so the write landed unopposed even though `persistSavepointWithReason`
// (replay.ts) had JUST refused the exact same savepoint as older than what
// localStorage's own rotation slots already hold for this lineage. The two
// "durable-ish" stores (localStorage, IndexedDB) could then disagree about
// which savepoint of a lineage is newest, and the stale one could resurrect
// an old city on a later boot.
//
// THE FIX (saveStore.ts): `guardedSavepointSetItem` now also accepts
// `extraExistingRaw` — other current copies of the SAME lineage's savepoint —
// and refuses the write unless the incoming savepoint is newer-or-equal to
// the NEWEST of {the target key's own contents, every extra baseline}.
// `mirrorSavepointDirect` (exported) threads this through as an optional 4th
// argument; store.tsx's wrapper (`localSavepointBaselines`) feeds in the
// CURRENT localStorage rotation-slot bytes for the savepoint's lineage — the
// same bytes `persistSavepointWithReason` just compared against — so both
// stores now share ONE ordering gate instead of two that can silently
// disagree. Ordering itself is unchanged: saveSeq primary, tick+savedAt
// fallback, exactly BUG-687's (lineageId, saveSeq) rule.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';

import { createSavepoint, persistSavepoint, persistSavepointWithReason } from '../src/sim/replay.ts';
import { initialState } from '../src/sim/engine.ts';
import { createSaveStore, memoryKVStore, mirrorSavepointDirect } from '../src/sim/saveStore.ts';
import { runWithMutant, runBaselineProbe } from '../testsupport/mutant.mjs';

/** A minimal synchronous localStorage-shaped mock, matching StorageLike. */
function memStorage() {
  const m = new Map();
  return {
    getItem: (k) => (m.has(k) ? m.get(k) : null),
    setItem: (k, v) => void m.set(k, String(v)),
    removeItem: (k) => void m.delete(k),
    _keys: () => Array.from(m.keys()).sort(),
    _map: m,
  };
}

/** All raw (still-encoded) values currently sitting in `storage`'s savepoint-shaped keys — exactly what store.tsx's `localSavepointBaselines` gathers. */
function savepointBaselines(storage) {
  return storage._keys()
    .filter((k) => k.startsWith('metropolis.savepoint.'))
    .map((k) => storage.getItem(k));
}

describe('BUG-704: guardedSavepointSetItem is a single ordering gate shared by localStorage and the durable IndexedDB mirror', () => {
  test('AC1 (the exact F4 shape): localStorage already holds a newer (lineage, saveSeq); the stale write is refused there AND the durable overflow slot must ALSO refuse it and keep whatever it already had', async () => {
    const now = new Date(2026, 8, 5, 9);
    const storage = memStorage();
    const lineage = 'city-A';

    // Occupy all three rotation slots for this lineage with saveSeq 5/6/7 —
    // the "already newer" content localStorage holds.
    for (let seq = 5; seq <= 7; seq++) {
      const sp = createSavepoint({ ...initialState(), tick: 900 + seq, lineageId: lineage }, [], new Date(now.getTime() - (8 - seq) * 60_000), 'v1', null, seq);
      sp.lineageId = lineage;
      assert.equal(persistSavepoint(storage, sp), true, `seed persist saveSeq=${seq} must land`);
    }

    // A stale write for the SAME lineage at saveSeq 3 (older than all three occupied slots).
    const stale = createSavepoint({ ...initialState(), tick: 100, lineageId: lineage }, [], now, 'v1', null, 3);
    stale.lineageId = lineage;
    const rejection = persistSavepointWithReason(storage, stale, now);
    assert.equal(rejection.reason, 'stale-overwrite', 'precondition: localStorage refuses the stale write');

    // Seed the durable overflow slot with something the guard would ALSO see
    // as older than the stale write itself, so an unguarded write would land
    // (isolating the fix to the cross-store comparison, not a same-key one).
    const store = createSaveStore(memoryKVStore());
    const overflowKey = `metropolis.savepoint.${lineage}.idbOnly`;
    await store.setItem(overflowKey, JSON.stringify({ ...stale, saveSeq: 1, snapshotTick: 1 }));

    const baselines = savepointBaselines(storage);
    assert.ok(baselines.length >= 3, 'precondition: baselines carry the fresher localStorage content');

    const mirrored = await mirrorSavepointDirect(store, JSON.stringify(stale), lineage, baselines);
    assert.equal(mirrored.ok, false, 'the durable store must refuse the same stale write localStorage just refused');
    assert.equal(mirrored.reason, 'stale', 'a freshness refusal must be reported as \'stale\'');

    const stillThere = JSON.parse(await store.getItem(overflowKey));
    assert.equal(stillThere.saveSeq, 1, 'the durable overflow slot must keep its own prior (older-but-uncontested) content untouched — never clobbered by the refused write');
  });

  test('AC2: a genuinely newer savepoint for the same lineage lands in both stores', async () => {
    const now = new Date(2026, 8, 5, 9);
    const storage = memStorage();
    const lineage = 'city-B';

    const older = createSavepoint({ ...initialState(), tick: 10, lineageId: lineage }, [], new Date(now.getTime() - 60_000), 'v1', null, 1);
    older.lineageId = lineage;
    assert.equal(persistSavepoint(storage, older), true);

    const store = createSaveStore(memoryKVStore());
    const overflowKey = `metropolis.savepoint.${lineage}.idbOnly`;

    const newer = createSavepoint({ ...initialState(), tick: 500, lineageId: lineage }, [], now, 'v1', null, 2);
    newer.lineageId = lineage;
    const persisted = persistSavepoint(storage, newer, now);
    assert.equal(persisted, true, 'localStorage accepts a genuinely newer write');

    const baselines = savepointBaselines(storage); // now includes `newer` itself, at slot alongside `older`
    const mirrored = await mirrorSavepointDirect(store, JSON.stringify(newer), lineage, baselines);
    assert.equal(mirrored.ok, true, 'the durable store accepts the same newer write (it is at least as new as every baseline, including its own bytes)');

    const landed = JSON.parse(await store.getItem(overflowKey));
    assert.equal(landed.saveSeq, 2, 'the newer savepoint lands in the durable store');
  });

  test('AC3 (BUG-687\'s rule): a DIFFERENT lineage\'s stale-looking write is never compared against another lineage\'s baselines', async () => {
    const now = new Date(2026, 8, 5, 9);
    const storageA = memStorage();
    const lineageA = 'city-old';
    const lineageB = 'city-new';

    // Lineage A: a long-lived, high-tick, high-saveSeq city.
    const oldCity = createSavepoint({ ...initialState(), tick: 50000, lineageId: lineageA }, [], now, 'v1', null, 40);
    oldCity.lineageId = lineageA;
    assert.equal(persistSavepoint(storageA, oldCity), true);
    const baselinesA = savepointBaselines(storageA);

    // Lineage B: a brand-new city, low tick, low saveSeq — per BUG-687, this
    // must NEVER be treated as "stale" merely because a DIFFERENT lineage
    // (A) has a higher tick/saveSeq.
    const newCity = createSavepoint({ ...initialState(), tick: 1, lineageId: lineageB }, [], now, 'v1', null, 1);
    newCity.lineageId = lineageB;

    const store = createSaveStore(memoryKVStore());
    // BUG-704 round REJECT (P2/P3): feed lineage A's REAL baselines
    // (tick=50000, saveSeq=40) into lineage B's write directly — not an
    // empty array standing in for "the real call site would never do this".
    // The real call site (store.tsx's `localSavepointBaselines`) only ever
    // gathers the SAME lineage's own keys in practice, but the GUARD ITSELF
    // must be the thing that ignores a foreign-lineage candidate (BUG-687
    // item 5's rule, same as `isStrictlyFresherSavepointMeta`) — a defence
    // that only ever holds because callers happen to behave is not a
    // defence at all. Without the lineage check inside
    // `guardedSavepointSetItem`, lineage A's tick=50000/seq=40 baseline would
    // wrongly refuse lineage B's tick=1/seq=1 write as "stale".
    assert.ok(baselinesA.length >= 1, 'precondition: lineage A has a real baseline to feed in');
    const mirroredB = await mirrorSavepointDirect(store, JSON.stringify(newCity), lineageB, baselinesA);
    assert.equal(mirroredB.ok, true, 'a brand-new lineage is never refused because an UNRELATED lineage\'s baseline happens to be further along');

    const overflowKeyB = `metropolis.savepoint.${lineageB}.idbOnly`;
    const landedB = JSON.parse(await store.getItem(overflowKeyB));
    assert.equal(landedB.lineageId, lineageB);

    const overflowKeyA = `metropolis.savepoint.${lineageA}.idbOnly`;
    assert.equal(await store.getItem(overflowKeyA), null, 'lineage A\'s own overflow key is untouched by lineage B\'s write');
  });

  test('RED-PROOF: with the shared gate bypassed (extraExistingRaw ignored), the durable store accepts the stale write', () => {
    const baseline = runBaselineProbe({
      targetRelPath: 'sim/saveStore.ts',
      childBody: PROBE_CHILD_BODY,
    });
    assert.match(baseline, /PASSED-ACCEPT-CHECK/, 'baseline (unmutated) sanity: the probe itself must reach its marker against the real source');
    assert.doesNotMatch(baseline, /WRONGLY-ACCEPTED/, 'baseline (unmutated) sanity: the fixed code must refuse the stale write');

    const mutantOutput = runWithMutant({
      targetRelPath: 'sim/saveStore.ts',
      mutate: (src) => {
        const needle = 'for (const raw of providedExtras) {';
        if (!src.includes(needle)) {
          throw new Error('RED-PROOF setup is broken: guardedSavepointSetItem\'s extra-baseline loop was not found — has the fix moved?');
        }
        // Bypass the shared gate: never even look at the extra baselines,
        // exactly the pre-BUG-704 defect (comparing only against the target
        // key's own prior contents).
        return src.replace(needle, 'for (const raw of []) {');
      },
      childBody: PROBE_CHILD_BODY,
    });
    assert.match(
      mutantOutput,
      /WRONGLY-ACCEPTED/,
      'RED-PROOF: bypassing the shared gate must let the stale write land in the durable store unopposed (the exact BUG-704 defect)',
    );
  });
});

// Shared-in-shadow-tree probe body for the RED-PROOF: imports saveStore.ts
// from the shadow copy's own root (mutated or not, depending on which runner
// invoked it), builds the exact F4 shape, and prints a marker Node's parent
// process can grep for. Runs twice (once via runBaselineProbe against the
// REAL unmutated source, once via runWithMutant against the mutated one) so
// the assertions above can tell "the probe itself is broken" apart from "the
// mutation was/was not detected".
const PROBE_CHILD_BODY = `
import { createSavepoint, persistSavepoint, persistSavepointWithReason } from './sim/replay.ts';
import { initialState } from './sim/engine.ts';
import { createSaveStore, memoryKVStore, mirrorSavepointDirect } from './sim/saveStore.ts';

function memStorage() {
  const m = new Map();
  return {
    getItem: (k) => (m.has(k) ? m.get(k) : null),
    setItem: (k, v) => void m.set(k, String(v)),
    removeItem: (k) => void m.delete(k),
    _keys: () => Array.from(m.keys()).sort(),
  };
}

const now = new Date(2026, 8, 5, 9);
const storage = memStorage();
const lineage = 'city-A';

for (let seq = 5; seq <= 7; seq++) {
  const sp = createSavepoint({ ...initialState(), tick: 900 + seq, lineageId: lineage }, [], new Date(now.getTime() - (8 - seq) * 60_000), 'v1', null, seq);
  sp.lineageId = lineage;
  persistSavepoint(storage, sp);
}

const stale = createSavepoint({ ...initialState(), tick: 100, lineageId: lineage }, [], now, 'v1', null, 3);
stale.lineageId = lineage;
const rejection = persistSavepointWithReason(storage, stale, now);
if (rejection.reason !== 'stale-overwrite') {
  console.log('PROBE-SETUP-BROKEN: expected stale-overwrite, got ' + JSON.stringify(rejection));
  process.exit(1);
}

const store = createSaveStore(memoryKVStore());
const baselines = storage._keys().filter((k) => k.startsWith('metropolis.savepoint.')).map((k) => storage.getItem(k));

const mirrored = await mirrorSavepointDirect(store, JSON.stringify(stale), lineage, baselines);
if (mirrored.ok) {
  console.log('WRONGLY-ACCEPTED');
} else {
  console.log('PASSED-ACCEPT-CHECK');
}
`;
