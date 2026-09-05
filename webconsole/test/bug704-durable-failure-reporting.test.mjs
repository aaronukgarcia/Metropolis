// bug704-durable-failure-reporting.test.mjs — BUG-704 re-round 2 (P3 items
// 2 and 3, attacker opus-reround2-bug704):
//
// (2) "mirrorSavepointDirect returns false for ANY durable failure (quota,
//     degraded store, throw) and store.tsx now reports all of them as 'a
//     fresher save already exists' - return a reason (stale vs
//     storage-error) and record the accurate message for each (GR#1)."
// (3) "the new `void mirrorSavepointDirectToStore(...).then(...)` has no
//     .catch - add one that records."
//
// Item 2's saveStore.ts-level contract (mirrorSavepointDirect resolving
// `{ok, reason, error}` instead of a bare boolean) is exercised directly.
// The store.tsx-level "record the ACCURATE message" half, and item 3's
// `.catch`, both live inside store.tsx's private `mirrorSavepointDirect`
// wrapper — exercised here via testsupport/mutant.mjs, injecting a
// synthetic result (or a rejection) at that exact call site so the REAL
// `.then`/`.catch`/`recordDurableSavepointRefusal` chain runs against a
// controlled outcome, through the REAL production entry point
// (`mirrorAfterPersist`).

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { runWithMutant, runBaselineProbe } from '../testsupport/mutant.mjs';
import { createSaveStore, memoryKVStore, mirrorSavepointDirect } from '../src/sim/saveStore.ts';

// ---------------------------------------------------------------------------
// Item 2, saveStore.ts level: a genuine storage failure (quota/degraded/
// throw) must be classified 'storage-error', never 'stale'.
// ---------------------------------------------------------------------------

function failingSaveStore(errorMsg) {
  return {
    getItem: async () => null,
    setItem: async () => ({ ok: false, quota: true, degraded: false, error: errorMsg }),
    removeItem: async () => {},
    listKeys: async () => [],
    isFullyDegraded: () => false,
    degradedKeys: () => [],
  };
}

function throwingSaveStore() {
  return {
    getItem: async () => {
      throw new Error('injected getItem throw');
    },
    setItem: async () => ({ ok: true, quota: false, degraded: false }),
    removeItem: async () => {},
    listKeys: async () => [],
    isFullyDegraded: () => false,
    degradedKeys: () => [],
  };
}

describe('BUG-704 re-round 2 (P3 item 2): mirrorSavepointDirect distinguishes a real storage failure from a freshness refusal', () => {
  test('a genuine write failure (quota) is classified storage-error, not stale', async () => {
    const store = failingSaveStore('QuotaExceededError: the quota has been exceeded');
    const result = await mirrorSavepointDirect(store, JSON.stringify({ snapshotTick: 1, savedAt: new Date().toISOString() }), 'city-quota');
    assert.equal(result.ok, false);
    assert.equal(result.reason, 'storage-error', 'a real write failure must never be reported as a freshness refusal');
    assert.equal(result.error, 'QuotaExceededError: the quota has been exceeded');
  });

  test('an unexpected throw is classified storage-error', async () => {
    const store = throwingSaveStore();
    const result = await mirrorSavepointDirect(store, JSON.stringify({ snapshotTick: 1, savedAt: new Date().toISOString() }), 'city-throw');
    assert.equal(result.ok, false);
    assert.equal(result.reason, 'storage-error');
    assert.match(result.error, /injected getItem throw/);
  });

  test('a genuine freshness refusal is classified stale (contrast)', async () => {
    const store = createSaveStore(memoryKVStore());
    const now = new Date(2026, 8, 6, 10);
    const overflowKey = 'metropolis.savepoint.city-contrast.idbOnly';
    await store.setItem(overflowKey, JSON.stringify({ snapshotTick: 100, savedAt: now.toISOString(), saveSeq: 5 }));
    const stale = JSON.stringify({ snapshotTick: 1, savedAt: now.toISOString(), saveSeq: 1 });
    const result = await mirrorSavepointDirect(store, stale, 'city-contrast');
    assert.equal(result.ok, false);
    assert.equal(result.reason, 'stale');
  });
});

// ---------------------------------------------------------------------------
// Items 2 (accurate message at the store.tsx level) and 3 (the .catch),
// through the REAL production wiring, via a controlled mutation of the exact
// call site.
// ---------------------------------------------------------------------------

const CALL_SITE_NEEDLE = 'mirrorSavepointDirectToStore(getDefaultSaveStore(), encodedSavepoint, lineageId, extraExistingRaw)';

function mutateCallSite(src, replacement) {
  if (!src.includes(CALL_SITE_NEEDLE)) {
    throw new Error('RED-PROOF setup is broken: the mirrorSavepointDirectToStore call site was not found — has it moved?');
  }
  return src.replace(CALL_SITE_NEEDLE, replacement);
}

function stripCatch(src) {
  // Several functions in store.tsx have their own `.catch((e: unknown) => {`
  // clause — scope the search to the one INSIDE `mirrorSavepointDirect`
  // specifically, or an earlier, unrelated occurrence gets stripped instead
  // (reproduced: the naive `indexOf` from the top of the file hit a
  // different function's catch entirely, leaving THIS one untouched and the
  // RED-PROOF vacuous).
  const fnStart = src.indexOf('function mirrorSavepointDirect(');
  if (fnStart < 0) throw new Error('RED-PROOF setup is broken: mirrorSavepointDirect not found — has it moved/renamed?');
  const needle = `.catch((e: unknown) => {`;
  const start = src.indexOf(needle, fnStart);
  if (start < 0) throw new Error('RED-PROOF setup is broken: the .catch clause was not found — has it moved?');
  // Find the matching close of `.catch(...)` — depth-count from the `(` right after `.catch`.
  const parenStart = src.indexOf('(', start + '.catch'.length);
  let depth = 0;
  let i = parenStart;
  for (; i < src.length; i++) {
    if (src[i] === '(') depth++;
    else if (src[i] === ')') {
      depth--;
      if (depth === 0) break;
    }
  }
  if (depth !== 0) throw new Error('RED-PROOF setup is broken: could not find the end of the .catch clause');
  const before = src.slice(0, start);
  const after = src.slice(i + 2); // skip ')' and the trailing ';'
  return before + after;
}

function childBody() {
  return `
process.on('unhandledRejection', (e) => {
  console.log('UNHANDLED-REJECTION:' + (e && e.message));
});

const { mirrorAfterPersist } = await import('./sim/store.tsx');
const { recentErrors } = await import('./sim/backend.ts');

const before = recentErrors().length;
mirrorAfterPersist(false, { snapshotTick: 1, savedAt: new Date().toISOString(), lineageId: 'city-injected' }, 'storage-error');
for (let i = 0; i < 20; i++) await new Promise((r) => setTimeout(r, 5));
const added = recentErrors().slice(0, recentErrors().length - before);
console.log('RECORDED:' + JSON.stringify(added.slice(0, 3).map((e) => e.msg)));
console.log('DONE');
`;
}

describe('BUG-704 re-round 2 (P3 items 2 + 3): store.tsx records an ACCURATE message and never lets a durable-mirror rejection go unhandled', () => {
  test('a storage-error result is recorded as a FAILURE, never as "a fresher save already exists" (baseline, unmutated real path also sane)', () => {
    const baseline = runBaselineProbe({
      targetRelPath: 'sim/store.tsx',
      childBody: childBody(),
      extraArgs: ['--import', 'tsx/esm'],
      timeoutMs: 60000,
    });
    assert.match(baseline, /DONE/, `probe must complete: ${baseline}`);
    assert.doesNotMatch(baseline, /UNHANDLED-REJECTION/, `the real (unmutated) path must never produce an unhandled rejection: ${baseline}`);
  });

  test('injected storage-error result -> accurate FAILED message, no "fresher save" wording', () => {
    const output = runWithMutant({
      targetRelPath: 'sim/store.tsx',
      mutate: (src) => mutateCallSite(src, `Promise.resolve({ ok: false, reason: 'storage-error', error: 'INJECTED-QUOTA-EXCEEDED' })`),
      childBody: childBody(),
      extraArgs: ['--import', 'tsx/esm'],
      timeoutMs: 60000,
    });
    assert.match(output, /DONE/, `probe must complete: ${output}`);
    assert.doesNotMatch(output, /UNHANDLED-REJECTION/);
    assert.match(output, /INJECTED-QUOTA-EXCEEDED/, `the accurate underlying detail must reach the recorded message: ${output}`);
    assert.match(output, /FAILED/, `a genuine storage failure must be reported as a FAILURE: ${output}`);
    assert.doesNotMatch(output, /fresher save already exists/, `a genuine storage failure must NEVER be reported as a freshness refusal (GR#1): ${output}`);
  });

  test('injected stale (freshness-refusal) result -> "fresher save" wording, contrast', () => {
    const output = runWithMutant({
      targetRelPath: 'sim/store.tsx',
      mutate: (src) => mutateCallSite(src, `Promise.resolve({ ok: false, reason: 'stale', error: 'refused: INJECTED-STALE-REFUSAL' })`),
      childBody: childBody(),
      extraArgs: ['--import', 'tsx/esm'],
      timeoutMs: 60000,
    });
    assert.match(output, /DONE/, `probe must complete: ${output}`);
    assert.doesNotMatch(output, /UNHANDLED-REJECTION/);
    assert.match(output, /INJECTED-STALE-REFUSAL/, `the accurate underlying detail must reach the recorded message: ${output}`);
    assert.doesNotMatch(output, /FAILED/, `a freshness refusal must not be worded as a storage FAILURE: ${output}`);
  });

  test('an unexpected rejection from the durable mirror is caught and recorded, never an unhandled rejection', () => {
    const output = runWithMutant({
      targetRelPath: 'sim/store.tsx',
      mutate: (src) => mutateCallSite(src, `Promise.reject(new Error('INJECTED-REJECTION'))`),
      childBody: childBody(),
      extraArgs: ['--import', 'tsx/esm'],
      timeoutMs: 60000,
    });
    assert.match(output, /DONE/, `probe must complete: ${output}`);
    assert.doesNotMatch(output, /UNHANDLED-REJECTION/, `the .catch must prevent an unhandled rejection: ${output}`);
    assert.match(output, /INJECTED-REJECTION/, `the rejection's own message must be recorded: ${output}`);
  });

  test('RED-PROOF: removing the .catch lets the same injected rejection become an UNHANDLED REJECTION', () => {
    const output = runWithMutant({
      targetRelPath: 'sim/store.tsx',
      mutate: (src) => {
        const withInjectedRejection = mutateCallSite(src, `Promise.reject(new Error('INJECTED-REJECTION'))`);
        return stripCatch(withInjectedRejection);
      },
      childBody: childBody(),
      extraArgs: ['--import', 'tsx/esm'],
      timeoutMs: 60000,
    });
    assert.match(
      output,
      /UNHANDLED-REJECTION:INJECTED-REJECTION/,
      `RED-PROOF: without the .catch, the same injected rejection must surface as an unhandled rejection: ${output}`,
    );
  });
});
