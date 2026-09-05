// bug704-corrupt-baseline-fail-closed.test.mjs — BUG-704 re-round 2 (P3
// item 1, attacker opus-reround2-bug704):
//
// "readSlot and parseSavepointFreshnessMeta have different acceptance
// domains - when every occupied rotation slot is semi-corrupt (valid JSON,
// recent savedAt, non-finite snapshotTick) readSlot treats them as occupied
// so localStorage refuses stale-overwrite, but the durable gate gets ZERO
// usable baselines and fails open (reproduced: the stale savepoint landed in
// the overflow slot). Make the two readers share one parse (single domain),
// and when baselines were gathered but none parsed, record it via the
// registry (GR#17) and fail CLOSED for the durable write."
//
// THE FIX, two parts:
//   1. `isUsableSavepointFreshness` (replay.ts) is now the ONE shared
//      acceptance predicate: `persistSavepointWithReason`'s overwrite-
//      protection branch applies it to an occupied local slot, and
//      saveStore.ts's `parseSavepointFreshnessMeta` applies it to a raw
//      durable-store candidate. A semi-corrupt occupied slot (well-formed
//      JSON, non-finite `snapshotTick`) can therefore no longer
//      authoritatively refuse a LOCAL write either — it is simply unusable
//      evidence on BOTH sides now, not "occupied" on one and "nothing" on
//      the other.
//   2. Defence in depth in `guardedSavepointSetItem`: if the caller supplied
//      baseline(s) (extraExistingRaw) and EVERY one of them fails to parse,
//      and the target key's own contents give no usable baseline either,
//      the write is REFUSED (fail CLOSED) and a registry error is recorded
//      (GR#17) — covers any other route corrupt bytes could reach the
//      durable gate through, independent of fix #1.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';

import { createSavepoint, persistSavepointWithReason } from '../src/sim/replay.ts';
import { initialState } from '../src/sim/engine.ts';
import { createSaveStore, memoryKVStore, mirrorSavepointDirect } from '../src/sim/saveStore.ts';
import { recentErrors } from '../src/sim/backend.ts';
import { runWithMutant, runBaselineProbe } from '../testsupport/mutant.mjs';

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

/** A savepoint-shaped raw JSON blob that is well-formed JSON with a RECENT `savedAt` but a non-finite `snapshotTick` — "valid JSON, freshness-unreadable". Never encode()-compressed (decode() is a no-op passthrough on plain JSON). */
function semiCorruptRaw(lineageId, savedAtIso) {
  return JSON.stringify({
    savedAt: savedAtIso,
    snapshotTick: null, // valid JSON; NOT a finite number -> fails isUsableSavepointFreshness
    snapshot: { tick: 1 },
    journalTail: [],
    lineageId,
  });
}

describe('BUG-704 re-round 2 (P3 item 1): readSlot and the durable gate share ONE acceptance domain for savepoint freshness', () => {
  test('a semi-corrupt occupied LOCAL rotation slot no longer authoritatively refuses a write it cannot evaluate', () => {
    const storage = memStorage();
    const lineage = 'city-corrupt-local';
    const now = new Date(2026, 8, 6, 9);
    for (let slot = 0; slot < 3; slot++) {
      storage.setItem(`metropolis.savepoint.${lineage}.${slot}`, semiCorruptRaw(lineage, new Date(now.getTime() - slot * 1000).toISOString()));
    }

    // Even a savepoint with a LOW tick (would lose an ordinary tick
    // comparison) must be accepted here — the occupied slots cannot prove
    // they are fresher, so they must not block the write (matches this
    // module's own fail-open-toward-availability posture for corrupt
    // EXISTING values everywhere else).
    const incoming = createSavepoint({ ...initialState(), tick: 1, lineageId: lineage }, [], now, 'v1', null, 1);
    incoming.lineageId = lineage;
    const result = persistSavepointWithReason(storage, incoming, now);
    assert.equal(result.ok, true, 'a corrupt-but-occupied slot must not authoritatively block a write it cannot evaluate');
  });

  test('the durable gate FAILS CLOSED (not open) when every supplied baseline is corrupt, and records a registry error (GR#17)', async () => {
    const lineage = 'city-corrupt-durable';
    const now = new Date(2026, 8, 6, 9);
    const corruptBaselines = [0, 1, 2].map((slot) => semiCorruptRaw(lineage, new Date(now.getTime() - slot * 1000).toISOString()));

    const store = createSaveStore(memoryKVStore());
    const overflowKey = `metropolis.savepoint.${lineage}.idbOnly`;
    assert.equal(await store.getItem(overflowKey), null, 'precondition: overflow slot starts empty');

    const stale = createSavepoint({ ...initialState(), tick: 1, lineageId: lineage }, [], now, 'v1', null, 1);
    stale.lineageId = lineage;

    const before = recentErrors().filter((e) => e.msg.includes('none of which parsed as a usable savepoint')).length;
    const mirrored = await mirrorSavepointDirect(store, JSON.stringify(stale), lineage, corruptBaselines);

    assert.equal(mirrored.ok, false, 'FAIL CLOSED: every baseline was corrupt, so the write must be refused, not silently accepted');
    assert.equal(mirrored.reason, 'stale', 'a fail-closed refusal is reported in the same bucket as an ordinary freshness refusal');
    assert.equal(await store.getItem(overflowKey), null, 'the stale write must NOT land in the overflow slot unopposed (the exact re-round-2 repro)');

    const after = recentErrors().filter((e) => e.msg.includes('none of which parsed as a usable savepoint'));
    assert.ok(after.length > before, 'GR#17: the corrupt-baseline condition itself must be recorded, never silent');
  });

  test('RED-PROOF: reverting the fail-closed guard reproduces the exact repro — the stale savepoint lands in the overflow slot unopposed', () => {
    const baseline = runBaselineProbe({ targetRelPath: 'sim/saveStore.ts', childBody: PROBE_CHILD_BODY });
    assert.doesNotMatch(baseline, /SETUP-BROKEN/, `probe setup must not be broken: ${baseline}`);
    assert.match(baseline, /PASSED-FAIL-CLOSED/, `baseline (unmutated) sanity: the fixed code must fail closed: ${baseline}`);

    const mutantOutput = runWithMutant({
      targetRelPath: 'sim/saveStore.ts',
      mutate: (src) => {
        const needle = 'if (providedExtras.length > 0 && unparsableExtras === providedExtras.length && newestExisting === null) {';
        if (!src.includes(needle)) {
          throw new Error('RED-PROOF setup is broken: the fail-closed corrupt-baseline guard was not found — has the fix moved?');
        }
        // Revert to the pre-fix behaviour: corrupt baselines are silently
        // treated as "nothing to compare against" -> fail OPEN.
        return src.replace(needle, 'if (false) {');
      },
      childBody: PROBE_CHILD_BODY,
    });
    assert.doesNotMatch(mutantOutput, /SETUP-BROKEN/, `mutant probe setup must not be broken: ${mutantOutput}`);
    assert.match(
      mutantOutput,
      /WRONGLY-ACCEPTED/,
      `RED-PROOF: reverting the fail-closed guard must let the stale write land in the overflow slot unopposed again: ${mutantOutput}`,
    );
  });
});

const PROBE_CHILD_BODY = `
import { createSavepoint } from './sim/replay.ts';
import { initialState } from './sim/engine.ts';
import { createSaveStore, memoryKVStore, mirrorSavepointDirect } from './sim/saveStore.ts';

function semiCorruptRaw(lineageId, savedAtIso) {
  return JSON.stringify({
    savedAt: savedAtIso,
    snapshotTick: null,
    snapshot: { tick: 1 },
    journalTail: [],
    lineageId,
  });
}

const lineage = 'city-corrupt-probe';
const now = new Date(2026, 8, 6, 9);
const corruptBaselines = [0, 1, 2].map((slot) => semiCorruptRaw(lineage, new Date(now.getTime() - slot * 1000).toISOString()));

const store = createSaveStore(memoryKVStore());
const overflowKey = \`metropolis.savepoint.\${lineage}.idbOnly\`;
const before = await store.getItem(overflowKey);
if (before !== null) {
  console.log('SETUP-BROKEN-OVERFLOW-NOT-EMPTY');
} else {
  const stale = createSavepoint({ ...initialState(), tick: 1, lineageId: lineage }, [], now, 'v1', null, 1);
  stale.lineageId = lineage;
  const mirrored = await mirrorSavepointDirect(store, JSON.stringify(stale), lineage, corruptBaselines);
  const landed = await store.getItem(overflowKey);
  if (mirrored.ok || landed !== null) {
    console.log('WRONGLY-ACCEPTED');
  } else {
    console.log('PASSED-FAIL-CLOSED');
  }
}
`;
