// attack-p0-lineage-round2.test.mjs — INDEPENDENT DESTRUCTIVE ROUND 2 against
// the FIXES for round 1's F1..F4. Attacker is NOT the author.
//
// Round 1 REJECTED on four estate-introduced defects. The rework closes all
// four, and round 1's 19 attacks now pass UNMODIFIED. This file attacks the
// REWORK'S OWN new machinery, none of which existed when round 1 ran:
//
//   F2's fix introduced an AMBIENT DEFAULT inside persistSavepointWithReason:
//   a savepoint arriving with no lineageId is stamped from whatever
//   `metropolis.currentLineage` says at PERSIST time. That makes a persist's
//   destination depend on a mutable global pointer rather than on the state
//   that produced it — a new coupling with its own failure mode.
//
//   F1's fix REORDERED applyLoadedSave so the pointer write happens BEFORE
//   the persist. That is correct for the success path, but it means a FAILED
//   load has already moved the pointer.
//
//   F3's fix added a second ambient default (restampSavepointsBuildVersion's
//   `lineageId ?? readCurrentLineageId(storage)`).

import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  persistSavepoint,
  persistSavepointWithReason,
  createSavepoint,
  readAllSavepoints,
  mostRecentSavepoint,
  restoreFromSavepoint,
  restampSavepointsBuildVersion,
  mintLineageId,
  readCurrentLineageId,
  writeCurrentLineageId,
  LEGACY_LINEAGE_ID,
  CURRENT_LINEAGE_KEY,
  SAVEPOINT_KEY_PREFIX,
  SAVEPOINT_CAP,
} from '../src/sim/replay.ts';
import { initialState } from '../src/sim/engine.ts';

function memStorage() {
  const m = new Map();
  return {
    getItem: (k) => (m.has(k) ? m.get(k) : null),
    setItem: (k, v) => void m.set(k, String(v)),
    removeItem: (k) => void m.delete(k),
    _keys: () => Array.from(m.keys()).sort(),
  };
}

function keyOf(slot, lineageId) {
  if (!lineageId || lineageId === LEGACY_LINEAGE_ID) return `${SAVEPOINT_KEY_PREFIX}.${slot}`;
  return `${SAVEPOINT_KEY_PREFIX}.${lineageId}.${slot}`;
}

function pin(fn) {
  try {
    fn();
  } catch (e) {
    console.error('FINDING >>> ' + (e?.message ?? String(e)));
    throw e;
  }
}

// ---------------------------------------------------------------------------
// R2-A. THE AMBIENT STAMP'S BACK DOOR: the pointer moved between the moment
// the savepoint was CREATED and the moment it is PERSISTED.
//
// persistSavepointWithReason now resolves an absent lineageId from the LIVE
// pointer. The savepoint therefore carries no binding to the city it was made
// from — its destination is decided later, by a mutable global. Characterise
// exactly what that buys and what it costs.
// ---------------------------------------------------------------------------
test('R2-A1: a lineage-LESS savepoint is routed by the pointer as it stands at PERSIST time, not creation time — the contract the fix depends on', () => {
  const storage = memStorage();
  const lineageA = mintLineageId();
  const lineageB = mintLineageId();

  // Created while A was current (but the state carries no lineage — the
  // genesis-rebuild / legacy-snapshot shape F2 exists for).
  writeCurrentLineageId(storage, lineageA);
  const sp = createSavepoint({ ...initialState(), tick: 500 }, [], new Date(2026, 8, 4, 10), 'v', null, 5);
  assert.equal(sp.lineageId, undefined, 'test setup: the savepoint must arrive lineage-less');

  // The pointer moves to B before the persist happens.
  writeCurrentLineageId(storage, lineageB);
  assert.equal(persistSavepointWithReason(storage, sp, new Date(2026, 8, 4, 10)).ok, true);

  // Characterise: it lands in B, and the caller's object is MUTATED in place.
  const inB = readAllSavepoints(storage, new Date(2026, 8, 4, 11), lineageB);
  const inA = readAllSavepoints(storage, new Date(2026, 8, 4, 11), lineageA);
  assert.equal(inB.length, 1, 'the ambient default routes by the LIVE pointer');
  assert.equal(inA.length, 0);
  assert.equal(sp.lineageId, lineageB, 'the helper mutates the caller\'s savepoint object in place — callers must not reuse it as lineage-less');

  // THE COST, stated plainly: a lineage-less savepoint is NOT self-describing.
  // Every caller that persists one is therefore obliged to have the pointer
  // already correct. Round 1's F1 fix is exactly that obligation being met at
  // applyLoadedSave; onRebuild meets it by stamping result.state itself.
  // The invariant is asserted mechanically for the whole codebase below.
});

test('R2-A2: THE BACK DOOR IS CLOSED BY CONSTRUCTION — every in-tree persist site either carries an explicit lineageId or writes the pointer first', async () => {
  const fs = await import('node:fs');
  const path = await import('node:path');
  const { fileURLToPath } = await import('node:url');
  const src = fs.readFileSync(
    path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'src', 'sim', 'store.tsx'),
    'utf8',
  );

  // The persist sites, and what makes each safe. If a NEW persist site appears
  // that satisfies neither, this test must be updated deliberately — that is
  // the point: the ambient default makes "which lineage" a whole-file property,
  // so it needs a whole-file check.
  const persistCalls = src.match(/persistSavepoint(WithReason)?\(window\.localStorage, [A-Za-z.]+\)/g) ?? [];
  assert.ok(persistCalls.length >= 4, `expected the known persist sites, found ${persistCalls.length}`);

  // applyLoadedSave: the pointer write MUST precede its persist (F1's fix).
  const pointerWrite = src.indexOf('writeCurrentLineageId(window.localStorage, normalizeLineageId(savepointToPersist.lineageId))');
  const loadPersist = src.indexOf('persistSavepoint(window.localStorage, savepointToPersist)');
  pin(() => assert.ok(pointerWrite > 0, 'applyLoadedSave must normalise + write the pointer (F1 fix missing)'));
  pin(() =>
    assert.ok(
      pointerWrite < loadPersist,
      'F1 REGRESSION: applyLoadedSave writes the current-lineage pointer AFTER its persist again. With the ambient default in ' +
        'persistSavepointWithReason, a legacy (lineage-less) loaded save would then be stamped into the PREVIOUS lineage\'s namespace — ' +
        'contaminating the abandoned city\'s own slots with the newly loaded one.',
    ),
  );

  // onRebuild: the rebuilt STATE must be stamped, not merely the savepoint —
  // otherwise the city's own identity is lost and every later export/import of
  // it is lineage-less again.
  pin(() =>
    assert.match(
      src,
      /result\.state = \{ \.\.\.result\.state, lineageId: rebuildLineageId \}/,
      'F2 REGRESSION: onRebuild must stamp the rebuilt SimState itself. Relying on the ambient default alone leaves savepoint.lineageId set but ' +
        'snapshot.lineageId undefined, so the restored city is identity-less again and an Export of it produces a lineage-less file.',
    ),
  );

  // The reset path must still mint outside the reducer and journal it.
  pin(() => assert.match(src, /recordAndDispatch\(\{ \.\.\.action, lineageId: freshLineageId \}\)/, 'the reset mint/journal wiring is gone'));
});

test('R2-A3: the ambient default never hijacks a save that DOES declare its lineage — an explicit id always wins over the pointer', () => {
  const storage = memStorage();
  const owner = mintLineageId();
  const other = mintLineageId();
  writeCurrentLineageId(storage, other); // the pointer names someone else entirely

  const sp = createSavepoint({ ...initialState(), tick: 77, lineageId: owner }, [], new Date(2026, 8, 4, 10), 'v', null, 3);
  assert.equal(persistSavepointWithReason(storage, sp, new Date(2026, 8, 4, 10)).ok, true);

  pin(() =>
    assert.equal(
      readAllSavepoints(storage, new Date(2026, 8, 4, 11), owner).length,
      1,
      'THE AMBIENT DEFAULT OVERRODE AN EXPLICIT LINEAGE: a savepoint that knows which city it belongs to must never be re-routed by the pointer.',
    ),
  );
  assert.equal(readAllSavepoints(storage, new Date(2026, 8, 4, 11), other).length, 0, 'it leaked into the pointer\'s lineage');
  assert.equal(sp.lineageId, owner, 'the explicit id was rewritten');
});

test('R2-A4: a genuine LEGACY install is byte-identically unaffected by the ambient default (the guard\'s stated purpose)', () => {
  const storage = memStorage();
  // No pointer at all — the pre-fix player.
  const before = createSavepoint({ ...initialState(), tick: 4000 }, [], new Date(2026, 8, 4, 10), 'v', null, 1);
  assert.equal(persistSavepointWithReason(storage, before, new Date(2026, 8, 4, 10)).ok, true);
  assert.ok(storage.getItem(keyOf(0)) !== null, 'a legacy save must still land at the UNNAMESPACED key');
  assert.deepEqual(
    storage._keys().filter((k) => k.startsWith(SAVEPOINT_KEY_PREFIX)),
    [keyOf(0)],
    'a legacy persist must create no namespaced keys at all',
  );
  assert.equal(restoreFromSavepoint(storage, readCurrentLineageId(storage)).state.tick, 4000);

  // Explicit 'legacy' pointer: identical.
  const s2 = memStorage();
  writeCurrentLineageId(s2, LEGACY_LINEAGE_ID);
  const sp2 = createSavepoint({ ...initialState(), tick: 4000 }, [], new Date(2026, 8, 4, 10), 'v', null, 1);
  assert.equal(persistSavepointWithReason(s2, sp2, new Date(2026, 8, 4, 10)).ok, true);
  assert.ok(s2.getItem(keyOf(0)) !== null);
});

test('R2-A5: a CORRUPT pointer cannot become a savepoint destination that boot can never find again', () => {
  // The ambient default reads the pointer verbatim. Whatever garbage it holds
  // becomes a key segment — so the ONE thing that must hold is that boot
  // resolves the pointer the SAME way the persist did, for every value.
  for (const junk of ['  ', 'L/../..', '{"a":1}', 'L'.repeat(5000), 'legacyy', ' x']) {
    const storage = memStorage();
    storage.setItem(CURRENT_LINEAGE_KEY, junk);
    const sp = createSavepoint({ ...initialState(), tick: 123 }, [], new Date(2026, 8, 4, 10), 'v', null, 1);
    assert.equal(persistSavepointWithReason(storage, sp, new Date(2026, 8, 4, 10)).ok, true, `persist threw/failed on pointer ${JSON.stringify(junk)}`);
    const restored = restoreFromSavepoint(storage, readCurrentLineageId(storage));
    pin(() =>
      assert.equal(
        restored.success,
        true,
        `A SAVE WRITTEN UNDER POINTER ${JSON.stringify(junk)} IS UNREADABLE AT BOOT: the persist and the boot read resolved the pointer differently, ` +
          'so the city is written to a key nothing ever looks at — silently never saved, the P0 shape.',
      ),
    );
    assert.equal(restored.state.tick, 123);
  }
});

// ---------------------------------------------------------------------------
// R2-B. F1's REORDER: THE POINTER IS WRITTEN BEFORE THE PERSIST.
//
// On the success path that is exactly right. But a persist can FAIL (quota —
// the wedge this whole increment exists for). The pointer has then already
// moved to a lineage that owns nothing, and the city the player was actually
// playing has been de-referenced. Is that recoverable at boot?
// ---------------------------------------------------------------------------
test('R2-B1: pointer written, persist then FAILS — boot must not brick, and must not silently destroy the de-referenced lineage', () => {
  const storage = memStorage();
  const live = mintLineageId();
  // The player's real, healthy city.
  for (let s = 0; s < SAVEPOINT_CAP; s++) {
    const at = new Date(2026, 8, 4, 9, s);
    assert.equal(persistSavepoint(storage, createSavepoint({ ...initialState(), tick: 9000 + s, lineageId: live }, [], at, 'v', null, 10 + s), at), true);
  }
  writeCurrentLineageId(storage, live);

  // A Load of a legacy save: F1 writes the pointer FIRST...
  writeCurrentLineageId(storage, LEGACY_LINEAGE_ID);
  // ...and the persist then fails (quota).
  const refusing = {
    getItem: storage.getItem,
    removeItem: storage.removeItem,
    setItem: (k, v) => {
      if (k.startsWith(SAVEPOINT_KEY_PREFIX)) throw new Error('QuotaExceededError');
      storage.setItem(k, v);
    },
  };
  const loadedSp = createSavepoint({ ...initialState(), tick: 12_000 }, [], new Date(2026, 8, 4, 10), 'v', null, 20);
  assert.equal(persistSavepointWithReason(refusing, loadedSp, new Date(2026, 8, 4, 10)).ok, false, 'test setup: the persist must fail');

  // BOOT. The pointer says legacy; legacy owns nothing.
  const pointer = readCurrentLineageId(storage);
  assert.equal(pointer, LEGACY_LINEAGE_ID);
  const r = restoreFromSavepoint(storage, pointer);
  assert.equal(r.success, false, 'nothing to restore for the de-referenced pointer — must fail cleanly, not throw');

  // THE THING THAT MUST HOLD: the player's real city is still ON DISK and
  // fully readable. A mis-pointed pointer is recoverable; a destroyed
  // keyspace is not.
  pin(() =>
    assert.equal(
      readAllSavepoints(storage, new Date(2026, 8, 4, 11), live).length,
      SAVEPOINT_CAP,
      'A FAILED LOAD DESTROYED THE LIVE CITY\'S SAVEPOINTS: the pointer reorder is only safe while the de-referenced lineage\'s data survives intact.',
    ),
  );
  assert.equal(restoreFromSavepoint(storage, live).state.tick, 9002, 'the live city must still restore when pointed at');
});

// ---------------------------------------------------------------------------
// R2-C. F3's SECOND AMBIENT DEFAULT.
// ---------------------------------------------------------------------------
test('R2-C1: restampSavepointsBuildVersion defaults from the pointer, honours an explicit override, and is a no-op for a legacy player', () => {
  // (i) omitted arg -> the pointer's lineage.
  const s1 = memStorage();
  const live = mintLineageId();
  writeCurrentLineageId(s1, live);
  assert.equal(persistSavepoint(s1, createSavepoint({ ...initialState(), tick: 10, lineageId: live }, [], new Date(2026, 8, 4, 10), 'v-OLD', null, 1), new Date(2026, 8, 4, 10)), true);
  assert.equal(restampSavepointsBuildVersion(s1, 'v-NEW'), true);
  assert.equal(mostRecentSavepoint(readAllSavepoints(s1, new Date(2026, 8, 4, 11), live)).buildVersion, 'v-NEW');

  // (ii) an EXPLICIT lineage must still win over the pointer — otherwise a
  // caller that legitimately knows better (a cross-lineage tool) is overridden.
  const s2 = memStorage();
  const a = mintLineageId();
  const b = mintLineageId();
  writeCurrentLineageId(s2, b);
  assert.equal(persistSavepoint(s2, createSavepoint({ ...initialState(), tick: 10, lineageId: a }, [], new Date(2026, 8, 4, 10), 'v-OLD', null, 1), new Date(2026, 8, 4, 10)), true);
  assert.equal(restampSavepointsBuildVersion(s2, 'v-NEW', a), true, 'an explicit lineage argument must be honoured, not shadowed by the pointer');
  assert.equal(mostRecentSavepoint(readAllSavepoints(s2, new Date(2026, 8, 4, 11), a)).buildVersion, 'v-NEW');

  // (iii) legacy player: unchanged behaviour.
  const s3 = memStorage();
  assert.equal(persistSavepoint(s3, createSavepoint({ ...initialState(), tick: 10 }, [], new Date(2026, 8, 4, 10), 'v-OLD', null, 1), new Date(2026, 8, 4, 10)), true);
  assert.equal(restampSavepointsBuildVersion(s3, 'v-NEW'), true);
  assert.equal(mostRecentSavepoint(readAllSavepoints(s3, new Date(2026, 8, 4, 11), LEGACY_LINEAGE_ID)).buildVersion, 'v-NEW');
});

// ---------------------------------------------------------------------------
// R2-D. MUTATIONS ON THE TWO NEW DEFAULTING PATHS. Each is modelled as the
// exact code deletion, run against the real primitives.
// ---------------------------------------------------------------------------
test('MUTATION 5: delete the ambient default in persistSavepointWithReason — the rebuild lands in the LEGACY keyspace again and it is CAUGHT', () => {
  const storage = memStorage();
  const live = mintLineageId();
  // The legacy keyspace holds a real pre-fix city that must not be clobbered.
  // Seeded BEFORE the pointer exists — otherwise the ambient default would
  // (correctly) route it into `live` and there would be no legacy city at all.
  assert.equal(persistSavepoint(storage, createSavepoint({ ...initialState(), tick: 88_000 }, [], new Date(2026, 8, 4, 8), 'v', null, 500), new Date(2026, 8, 4, 8)), true);
  writeCurrentLineageId(storage, live);

  // MUTATED: the savepoint keeps its absent lineageId all the way to the key.
  const rebuilt = createSavepoint({ ...initialState(), tick: 40 }, [], new Date(2026, 8, 4, 10), 'v', null, 501);
  const mutatedKey = keyOf(0, undefined); // what savepointKey would produce with no stamp
  assert.equal(mutatedKey, keyOf(0, LEGACY_LINEAGE_ID));
  storage.setItem(mutatedKey, JSON.stringify(rebuilt)); // the mutation's effect

  assert.equal(
    readAllSavepoints(storage, new Date(2026, 8, 4, 11), live).length,
    0,
    'MUTATION NOT CAUGHT: without the ambient default the rebuild leaves the live lineage with NO savepoint — invisible at the next boot',
  );
  assert.equal(
    mostRecentSavepoint(readAllSavepoints(storage, new Date(2026, 8, 4, 11), LEGACY_LINEAGE_ID)).snapshotTick,
    40,
    'MUTATION NOT CAUGHT: and it clobbers the legacy city the migration exists to protect',
  );

  // UNMUTATED control on a clean store: the default routes it correctly.
  const clean = memStorage();
  writeCurrentLineageId(clean, live);
  const sp = createSavepoint({ ...initialState(), tick: 40 }, [], new Date(2026, 8, 4, 10), 'v', null, 1);
  assert.equal(persistSavepointWithReason(clean, sp, new Date(2026, 8, 4, 10)).ok, true);
  assert.equal(readAllSavepoints(clean, new Date(2026, 8, 4, 11), live).length, 1);
  assert.equal(clean.getItem(keyOf(0)), null, 'and nothing is written to the legacy keyspace');
});

test('MUTATION 6: delete restampSavepointsBuildVersion\'s pointer default AND the call-site arguments — BUG-468\'s loop returns and is CAUGHT', () => {
  const storage = memStorage();
  const live = mintLineageId();
  writeCurrentLineageId(storage, live);
  assert.equal(persistSavepoint(storage, createSavepoint({ ...initialState(), tick: 10, lineageId: live }, [], new Date(2026, 8, 4, 10), 'v-OLD', null, 1), new Date(2026, 8, 4, 10)), true);

  // MUTATED: restamp the LEGACY keyspace (what an undefaulted, unargumented
  // call resolves to) — the namespaced city's stamp is untouched.
  restampSavepointsBuildVersion(storage, 'v-NEW', LEGACY_LINEAGE_ID);
  assert.equal(
    mostRecentSavepoint(readAllSavepoints(storage, new Date(2026, 8, 4, 11), live)).buildVersion,
    'v-OLD',
    'MUTATION NOT CAUGHT: the stale buildVersion must survive, which is what re-triggers the "New build detected" prompt on every boot',
  );
  // UNMUTATED: the default fixes it.
  assert.equal(restampSavepointsBuildVersion(storage, 'v-NEW'), true);
  assert.equal(mostRecentSavepoint(readAllSavepoints(storage, new Date(2026, 8, 4, 11), live)).buildVersion, 'v-NEW');
});

test('MUTATION 7: drop normalizeLineageId from F1\'s pointer write (back to `if (lineageId)`) — a legacy load leaves the pointer stale and it is CAUGHT', () => {
  const storage = memStorage();
  const previous = mintLineageId();
  writeCurrentLineageId(storage, previous);

  // A legacy save being loaded: lineageId absent.
  const loaded = createSavepoint({ ...initialState(), tick: 3000 }, [], new Date(2026, 8, 4, 10), 'v', null, 7);

  // MUTATED pointer write: `if (loaded.lineageId) write(...)` — skipped.
  const mutatedPointer = loaded.lineageId ? loaded.lineageId : readCurrentLineageId(storage);
  assert.equal(mutatedPointer, previous, 'MUTATION NOT CAUGHT: the pointer must remain on the PREVIOUS lineage under the mutation');

  // ...and with the ambient default now in place, the loaded LEGACY city is
  // stamped into the PREVIOUS lineage — strictly worse than round 1, because
  // it now actively contaminates the abandoned city's own slots.
  assert.equal(persistSavepointWithReason(storage, loaded, new Date(2026, 8, 4, 10)).ok, true);
  assert.equal(
    mostRecentSavepoint(readAllSavepoints(storage, new Date(2026, 8, 4, 11), previous)).snapshotTick,
    3000,
    'MUTATION NOT CAUGHT: under the mutation the loaded legacy city lands in the ABANDONED lineage\'s namespace — the F1+F2 interaction',
  );

  // UNMUTATED: normalize -> 'legacy', pointer moves, the city stays legacy.
  const clean = memStorage();
  writeCurrentLineageId(clean, previous);
  const sp = createSavepoint({ ...initialState(), tick: 3000 }, [], new Date(2026, 8, 4, 10), 'v', null, 7);
  writeCurrentLineageId(clean, sp.lineageId && sp.lineageId !== LEGACY_LINEAGE_ID ? sp.lineageId : LEGACY_LINEAGE_ID);
  assert.equal(persistSavepointWithReason(clean, sp, new Date(2026, 8, 4, 10)).ok, true);
  assert.equal(readAllSavepoints(clean, new Date(2026, 8, 4, 11), previous).length, 0, 'the abandoned lineage must stay untouched');
  assert.equal(mostRecentSavepoint(readAllSavepoints(clean, new Date(2026, 8, 4, 11), LEGACY_LINEAGE_ID)).snapshotTick, 3000);
});

test('MUTATION 8 (EQUIVALENCE CHECK): removing the `ambient !== legacy` guard is an EQUIVALENT mutant — savepointKey maps \'legacy\' and undefined to the same key', () => {
  // Documented so a future round does not spend time hunting a mutant that
  // cannot be killed: the guard is defensive clarity, not behaviour. Its ONLY
  // observable effect is the literal field value on the stored record.
  assert.equal(keyOf(0, LEGACY_LINEAGE_ID), keyOf(0, undefined));
  const withGuard = memStorage();
  const withoutGuard = memStorage();
  for (const s of [withGuard, withoutGuard]) writeCurrentLineageId(s, LEGACY_LINEAGE_ID);

  const a = createSavepoint({ ...initialState(), tick: 55 }, [], new Date(2026, 8, 4, 10), 'v', null, 1);
  assert.equal(persistSavepointWithReason(withGuard, a, new Date(2026, 8, 4, 10)).ok, true);
  // The mutation's effect: stamp 'legacy' explicitly, then persist.
  const b = createSavepoint({ ...initialState(), tick: 55 }, [], new Date(2026, 8, 4, 10), 'v', null, 1);
  b.lineageId = LEGACY_LINEAGE_ID;
  assert.equal(persistSavepointWithReason(withoutGuard, b, new Date(2026, 8, 4, 10)).ok, true);

  assert.deepEqual(withGuard._keys(), withoutGuard._keys(), 'the two variants must occupy the SAME keys — that is what makes the mutant equivalent');
  assert.equal(restoreFromSavepoint(withGuard, LEGACY_LINEAGE_ID).state.tick, restoreFromSavepoint(withoutGuard, LEGACY_LINEAGE_ID).state.tick);
});
