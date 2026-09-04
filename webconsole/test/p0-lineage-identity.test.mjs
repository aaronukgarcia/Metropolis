// p0-lineage-identity.test.mjs — P0 RCA fix (Aaron, 2026-09-04): additional
// coverage beyond the copied RCA repro (rca-lineage-blind-savepoints.test.mjs,
// kept as the acceptance bar, unmodified). Low-level replay.ts API only — no
// SimProvider mount — to exercise the namespaced-slot/scoped-gate/migration
// mechanism directly and cheaply.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  persistSavepoint,
  persistSavepointWithReason,
  createSavepoint,
  readAllSavepoints,
  mostRecentSavepoint,
  restampSavepointsBuildVersion,
  migrateLegacySavepointsInPlace,
  mintLineageId,
  readCurrentLineageId,
  writeCurrentLineageId,
  LEGACY_LINEAGE_ID,
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

test('P0 lineage fix: two lineages autosaving interleaved never cross-refuse', () => {
  const storage = memStorage();
  const lineageA = mintLineageId();
  const lineageB = mintLineageId();
  assert.notEqual(lineageA, lineageB, 'test setup: two distinct minted lineages');

  // City A: high tick, grows normally.
  let landedA = 0;
  for (let i = 0; i < 5; i++) {
    const state = { ...initialState(), tick: 100_000 + i * 10, lineageId: lineageA };
    const sp = createSavepoint(state, [], new Date(2026, 8, 4, 10, i), 'v-test', null, i + 1);
    if (persistSavepoint(storage, sp, new Date(2026, 8, 4, 10, i))) landedA++;
  }
  // City B: brand-new, LOW tick, interleaved with A's autosaves.
  let landedB = 0;
  for (let i = 0; i < 5; i++) {
    const state = { ...initialState(), tick: 10 + i, lineageId: lineageB };
    const sp = createSavepoint(state, [], new Date(2026, 8, 4, 10, i), 'v-test', null, i + 1);
    if (persistSavepoint(storage, sp, new Date(2026, 8, 4, 10, i))) landedB++;
  }

  // THE CORE CLAIM: city B's low-tick autosaves are NEVER refused because of
  // city A's high-tick ones occupying "the same slot" — they physically
  // cannot, because the slots are namespaced per lineage now.
  assert.ok(landedA > 0, 'lineage A must land its own autosaves');
  assert.ok(landedB > 0, 'lineage B (low tick) must land its own autosaves DESPITE lineage A holding a much higher tick');

  const mostA = mostRecentSavepoint(readAllSavepoints(storage, new Date(2026, 8, 4, 12), lineageA));
  const mostB = mostRecentSavepoint(readAllSavepoints(storage, new Date(2026, 8, 4, 12), lineageB));
  assert.ok(mostA && mostA.snapshot.lineageId === lineageA, 'lineage A reads back its OWN city');
  assert.ok(mostB && mostB.snapshot.lineageId === lineageB, 'lineage B reads back its OWN city, untouched by A');
  assert.notEqual(mostA.snapshotTick, mostB.snapshotTick);
});

test('P0 lineage fix: boot after a new game restores the NEW lineage while the OLD one stays intact under its own namespace', () => {
  const storage = memStorage();
  const oldLineage = mintLineageId();
  for (let slot = 0; slot < SAVEPOINT_CAP; slot++) {
    const state = { ...initialState(), tick: 500_000, lineageId: oldLineage };
    const sp = createSavepoint(state, [], new Date(2026, 8, 3, 20, slot), 'v-old', null, slot + 1);
    assert.ok(persistSavepoint(storage, sp, new Date(2026, 8, 3, 20, slot)));
  }
  writeCurrentLineageId(storage, oldLineage);

  // "Start Over": a fresh lineage becomes current; the old city's slots are
  // NEVER touched (mirrors engine.ts's 'reset' case + the Start Over dispatch
  // site — no storage write happens to the old lineage's own keys at all).
  const newLineage = mintLineageId();
  writeCurrentLineageId(storage, newLineage);
  const newState = { ...initialState(), tick: 3, lineageId: newLineage };
  assert.ok(persistSavepoint(storage, createSavepoint(newState, [], new Date(2026, 8, 4, 9), 'v-new', null, 1)));

  // Boot reads ONLY the current (new) lineage.
  const current = readCurrentLineageId(storage);
  assert.equal(current, newLineage);
  const bootMost = mostRecentSavepoint(readAllSavepoints(storage, new Date(2026, 8, 4, 12), current));
  assert.ok(bootMost, 'boot must find the new lineage');
  assert.equal(bootMost.snapshotTick, 3, 'boot restores the NEW city, not the old high-tick one');

  // The OLD lineage is still fully intact, reachable under its own namespace.
  const oldStill = mostRecentSavepoint(readAllSavepoints(storage, new Date(2026, 8, 4, 12), oldLineage));
  assert.ok(oldStill, 'the old lineage must still be readable — nothing deleted it');
  assert.equal(oldStill.snapshotTick, 500_000);
});

test('P0 lineage fix: a savepoint with no lineageId reads as the reserved legacy lineage and is rewritten in place (migration)', () => {
  const storage = memStorage();
  for (let slot = 0; slot < SAVEPOINT_CAP; slot++) {
    const sp = createSavepoint({ ...initialState(), tick: 42 + slot }, [], new Date(2026, 8, 1, 0, slot), 'v-legacy', null);
    assert.equal(sp.lineageId, undefined, 'test setup: a genuinely pre-fix savepoint carries no lineageId');
    assert.ok(persistSavepoint(storage, sp, new Date(2026, 8, 1, 0, slot)));
  }
  // Boot default: no pointer written yet -> resolves to LEGACY_LINEAGE_ID.
  assert.equal(readCurrentLineageId(storage), LEGACY_LINEAGE_ID);

  // Migration rewrites each legacy slot IN PLACE (same physical key).
  const before = storage._keys();
  const wrote = migrateLegacySavepointsInPlace(storage);
  assert.ok(wrote, 'migration must actually rewrite at least one slot');
  assert.deepEqual(storage._keys(), before, 'migration must be IN PLACE — no new keys, nothing removed');

  const migrated = readAllSavepoints(storage, new Date(2026, 8, 4, 12), LEGACY_LINEAGE_ID);
  assert.equal(migrated.length, SAVEPOINT_CAP);
  for (const sp of migrated) assert.equal(sp.lineageId, LEGACY_LINEAGE_ID);

  // Idempotent — a second pass is a no-op.
  assert.equal(migrateLegacySavepointsInPlace(storage), false);

  // restampSavepointsBuildVersion also stays lineage-aware (defaults to legacy).
  assert.ok(restampSavepointsBuildVersion(storage, 'v-current-build'));
  const restamped = mostRecentSavepoint(readAllSavepoints(storage, new Date(2026, 8, 4, 12), LEGACY_LINEAGE_ID));
  assert.equal(restamped.buildVersion, 'v-current-build');
});

test('P0 lineage fix: persistSavepointWithReason returns an explicit RejectReason and never cross-lineage-refuses', () => {
  const storage = memStorage();
  const lineageA = mintLineageId();
  const lineageB = mintLineageId();

  // Fill ALL of lineage A's rotation slots so the NEXT write must go through
  // the overwrite-protection comparison rather than simply landing in an
  // empty slot.
  for (let slot = 0; slot < SAVEPOINT_CAP; slot++) {
    const highTickA = { ...initialState(), tick: 900_000 + slot, lineageId: lineageA };
    assert.ok(persistSavepoint(storage, createSavepoint(highTickA, [], new Date(2026, 8, 4, 10, slot), 'v', null, 1)));
  }

  // Lineage B's low-tick, low-seq savepoint must land — it is never even
  // compared against lineage A's, because the slots are physically separate.
  const lowTickB = { ...initialState(), tick: 5, lineageId: lineageB };
  const resultB = persistSavepointWithReason(storage, createSavepoint(lowTickB, [], new Date(2026, 8, 4, 10), 'v', null, 1));
  assert.equal(resultB.ok, true, 'a brand-new, low-tick lineage must never be refused because of an unrelated lineage occupying its own slots');
  assert.equal(resultB.reason, undefined);

  // A genuinely STALE re-write WITHIN the same lineage is still refused, with a reason.
  const staleA = { ...initialState(), tick: 1, lineageId: lineageA };
  const staleResult = persistSavepointWithReason(storage, createSavepoint(staleA, [], new Date(2020, 0, 1), 'v', null, 1));
  assert.equal(staleResult.ok, false);
  assert.equal(staleResult.reason, 'stale-overwrite');
});
