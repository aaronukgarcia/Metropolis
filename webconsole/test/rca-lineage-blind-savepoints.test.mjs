// rca-lineage-blind-savepoints.test.mjs — P0 RCA repro (Aaron, 2026-09-04):
// "I created a whole new map city 13 and never once placed any gorges dams and I
//  am getting reports of 22 excess dams — somehow the prior map is being reloaded
//  yet I saved and started a new map."
//
// PROVEN MECHANISM (see the RCA report for file:line evidence):
//   1. Savepoint slots are GLOBAL, lineage-blind keys: metropolis.savepoint.{0,1,2}
//      (replay.ts:74 SAVEPOINT_KEY_PREFIX, replay.ts:106 savepointKey).
//   2. NOTHING on the Start Over / new-game path clears them (store.tsx:711-730
//      dispatches 'reset' after the GR#27 capture; engine.ts's reset case is a
//      pure in-memory state replacement — no storage writes at all). The ONLY
//      code that ever clears a savepoint slot is the manual Config → "Clear
//      autosave slots" button (ConfigMenu.tsx:180-183).
//   3. persistSavepoint's BUG-469 overwrite protection (replay.ts:228-238)
//      compares snapshotTick (savedAt only as a tie-break). A brand-new city at
//      tick ~1,744 can NEVER beat an old city's tick ~164,000, so EVERY autosave
//      of the new city is REFUSED for the life of the browser profile.
//   4. Boot (store.tsx:370) reads mostRecentSavepoint(readAllSavepoints(...)) —
//      still the OLD city, because the new one never landed.
//   5. The restored OLD city hydrates through reducer 'hydrate' (source !== 'tick'),
//      which fires the FEAT-2326609781 surplus purge → "Removed 22 surplus Three
//      Gorges Dam ..." (engine.ts:6092-6162).
//
// RED PROOF (out-of-band, cp/mv — never git): neutering the BUG-469 tick
// comparison in replay.ts:231-232 to `const incomingIsNewer = true` flips
// tests 2/3/4 RED (the new city's autosave then lands and boot returns city 13).

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';

import {
  persistSavepoint,
  createSavepoint,
  readAllSavepoints,
  mostRecentSavepoint,
  SAVEPOINT_CAP,
  SAVEPOINT_KEY_PREFIX,
} from '../src/sim/replay.ts';
import { initialState, reducer } from '../src/sim/engine.ts';
import { SPECS } from '../src/sim/data.ts';

/** Minimal injectable Web Storage subset (same shape the other suites use). */
function memStorage() {
  const m = new Map();
  return {
    getItem: (k) => (m.has(k) ? m.get(k) : null),
    setItem: (k, v) => void m.set(k, String(v)),
    removeItem: (k) => void m.delete(k),
    /** test-only introspection */
    _keys: () => Array.from(m.keys()).sort(),
  };
}

const DAM = 'pow_hydro';
const DAM_COUNT = 23; // Aaron's real old city
const OLD_TICK = 164_000; // Y455-ish
const NEW_TICK = 1_744; // city 13 at Y4

/** The old, quota-wedged city: tick 164,000 and 23 Three Gorges Dams. */
function oldCityState() {
  const base = initialState();
  const dams = Array.from({ length: DAM_COUNT }, (_, i) => ({
    id: 900_000 + i,
    spec: DAM,
    x: 10 + i,
    y: 10,
    builtTick: 1000 + i,
  }));
  return {
    ...base,
    tick: OLD_TICK,
    buildings: [...base.buildings, ...dams],
    nextId: 1_000_000,
  };
}

/** City 13: brand new map, four years in, ZERO dams ever placed. */
function newCityState() {
  const base = initialState();
  return { ...base, tick: NEW_TICK };
}

/**
 * Fill every rotation slot with the old city's autosaves, exactly as hours of
 * play would have. savedAt is staggered so the "oldest occupied slot" target
 * selection in persistSavepoint is deterministic.
 */
function seedOldCitySlots(storage) {
  const old = oldCityState();
  for (let slot = 0; slot < SAVEPOINT_CAP; slot++) {
    const sp = createSavepoint(old, [], new Date(Date.UTC(2026, 8, 3, 20, slot)), 'v0.3.0.old', null);
    const ok = persistSavepoint(storage, sp, new Date(Date.UTC(2026, 8, 3, 20, slot)));
    assert.equal(ok, true, `old-city autosave should land in slot ${slot}`);
  }
  return old;
}

describe('P0 RCA: lineage-blind savepoints resurrect the previous city', () => {
  test('1. savepoint slots carry NO city/lineage identity — the keys are global', () => {
    const storage = memStorage();
    seedOldCitySlots(storage);
    const keys = storage._keys();
    assert.deepEqual(keys, [
      `${SAVEPOINT_KEY_PREFIX}.0`,
      `${SAVEPOINT_KEY_PREFIX}.1`,
      `${SAVEPOINT_KEY_PREFIX}.2`,
    ]);
    // Nothing in the persisted key namespace distinguishes one city from another:
    // a second city writes to (and is compared against) these identical keys.
  });

  test('2. Start Over does NOT clear the slots — reset is a pure in-memory action', () => {
    const storage = memStorage();
    seedOldCitySlots(storage);
    const before = storage._keys();

    // The whole storage-visible effect of "Start Over" is store.tsx's
    // GR#27 capture + `dispatch({type:'reset'})`. The reducer itself:
    const afterReset = reducer(oldCityState(), { type: 'reset' });
    assert.equal(afterReset.tick <= 1, true, 'reset returns a fresh city in memory');
    assert.equal(afterReset.buildings.filter((b) => b.spec === DAM).length, 0, 'fresh city has no dams');

    // ...but every old-city savepoint is still on disk, untouched:
    assert.deepEqual(storage._keys(), before);
    for (let slot = 0; slot < SAVEPOINT_CAP; slot++) {
      assert.notEqual(storage.getItem(`${SAVEPOINT_KEY_PREFIX}.${slot}`), null);
    }
  });

  test('3. THE DEFECT: every autosave of the new city is REFUSED by the BUG-469 tick gate', () => {
    const storage = memStorage();
    seedOldCitySlots(storage);

    const city13 = newCityState();
    // Simulate a long session of autosaves at 30s intervals as city 13 grows.
    let anyLanded = false;
    for (let i = 0; i < 40; i++) {
      const tick = NEW_TICK + i * 10; // still nowhere near 164,000
      const sp = createSavepoint({ ...city13, tick }, [], new Date(Date.UTC(2026, 8, 4, 10, i)), 'v0.3.0.new', null);
      const ok = persistSavepoint(storage, sp, new Date(Date.UTC(2026, 8, 4, 10, i)));
      anyLanded = anyLanded || ok;
    }
    assert.equal(anyLanded, false, 'NOT ONE autosave of the new city was allowed to persist');

    // ...even though every one of them is strictly NEWER in wall-clock time.
    const persisted = readAllSavepoints(storage, new Date(Date.UTC(2026, 8, 4, 12)));
    assert.equal(persisted.length, SAVEPOINT_CAP);
    for (const sp of persisted) {
      assert.equal(sp.snapshotTick, OLD_TICK, 'the slots still hold the OLD city');
    }
  });

  test('4. Boot after a reload restores the OLD city, not city 13', () => {
    const storage = memStorage();
    seedOldCitySlots(storage);
    for (let i = 0; i < 40; i++) {
      const sp = createSavepoint(
        { ...newCityState(), tick: NEW_TICK + i * 10 },
        [],
        new Date(Date.UTC(2026, 8, 4, 10, i)),
        'v0.3.0.new',
        null,
      );
      persistSavepoint(storage, sp, new Date(Date.UTC(2026, 8, 4, 10, i)));
    }

    // This is exactly store.tsx:370's boot expression.
    const most = mostRecentSavepoint(readAllSavepoints(storage, new Date(Date.UTC(2026, 8, 4, 12))));
    assert.notEqual(most, null);
    assert.equal(most.snapshotTick, OLD_TICK, 'boot picks the OLD city');
    assert.equal(
      most.snapshot.buildings.filter((b) => b.spec === DAM).length,
      DAM_COUNT,
      'the restored city carries all 23 Three Gorges Dams',
    );
  });

  test('5. Hydrating that restored state produces Aaron\'s exact "22 surplus" notice', () => {
    const storage = memStorage();
    seedOldCitySlots(storage);
    const most = mostRecentSavepoint(readAllSavepoints(storage, new Date(Date.UTC(2026, 8, 4, 12))));

    // Boot's tail-replay / applyLoadedSave both finish with a non-tick hydrate.
    const hydrated = reducer(newCityState(), { type: 'hydrate', state: most.snapshot });

    const cap = SPECS[DAM].maxPerCity;
    assert.equal(cap, 1);
    assert.equal(hydrated.buildings.filter((b) => b.spec === DAM).length, cap);
    assert.match(
      String(hydrated.placeNotice ?? ''),
      new RegExp(`Removed ${DAM_COUNT - cap} surplus ${SPECS[DAM].name}`),
    );
  });

  test('6. Control: had the slots been lineage-namespaced (or cleared), city 13 survives', () => {
    // Same sequence, but with the old city's slots cleared at new-game time —
    // i.e. what the fix must guarantee.
    const storage = memStorage();
    seedOldCitySlots(storage);
    for (let slot = 0; slot < SAVEPOINT_CAP; slot++) {
      storage.removeItem(`${SAVEPOINT_KEY_PREFIX}.${slot}`);
    }
    const sp = createSavepoint(newCityState(), [], new Date(Date.UTC(2026, 8, 4, 10)), 'v0.3.0.new', null);
    assert.equal(persistSavepoint(storage, sp, new Date(Date.UTC(2026, 8, 4, 10))), true);

    const most = mostRecentSavepoint(readAllSavepoints(storage, new Date(Date.UTC(2026, 8, 4, 12))));
    assert.equal(most.snapshotTick, NEW_TICK, 'city 13 is what boot restores');
    assert.equal(most.snapshot.buildings.filter((b) => b.spec === DAM).length, 0, 'no dams, no purge notice');
  });
});
