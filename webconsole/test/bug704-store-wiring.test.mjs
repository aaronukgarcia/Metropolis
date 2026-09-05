// bug704-store-wiring.test.mjs — BUG-704 round REJECT (P1, attacker
// opus-round-bug704): the original BUG-704 fix was verified ONLY at the
// saveStore.ts layer (calling `mirrorSavepointDirect` directly with
// hand-built baseline arrays) — the REAL production wiring, store.tsx's
// `localSavepointBaselines` -> `mirrorAfterPersist` -> `mirrorSavepointDirect`
// chain, was never exercised. The attacker proved this by mutating
// `localSavepointBaselines` to `return [];` (making the whole fix inert) and
// showing every existing BUG-704 suite stayed green — the production call
// site was never under test at all.
//
// This file closes that gap: it calls the REAL exported `mirrorAfterPersist`
// (store.tsx) — never saveStore.ts's `mirrorSavepointDirect` directly — after
// seeding REAL `window.localStorage` rotation slots via `persistSavepoint`
// (replay.ts), for BOTH the legacy (unnamespaced) keyspace and a namespaced
// lineage's keyspace, and asserts the durable overflow slot is refused
// exactly as the F4 shape requires. The RED-PROOF (via testsupport/mutant.mjs)
// mutates `localSavepointBaselines` to `return [];` and proves this test then
// goes red — the wiring gap the round found, closed.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { runWithMutant, runBaselineProbe } from '../testsupport/mutant.mjs';

// Shared probe body: sets up a minimal DOM (jsdom) so `window.localStorage`
// is real, seeds BOTH keyspaces with fresher rotation-slot content via the
// REAL `persistSavepoint`, triggers a REAL stale-overwrite refusal via
// `persistSavepointWithReason`, then calls the REAL `mirrorAfterPersist`
// (store.tsx) — the exact production call chain every autosave/save/load
// site in store.tsx uses — and prints one marker per keyspace.
const PROBE_CHILD_BODY = `
import { JSDOM } from 'jsdom';

const dom = new JSDOM('<!doctype html><html><body></body></html>', { url: 'http://localhost/' });
globalThis.window = dom.window;
globalThis.localStorage = dom.window.localStorage;
globalThis.IS_REACT_ACT_ENVIRONMENT = true;

const { persistSavepoint, persistSavepointWithReason, createSavepoint } = await import('./sim/replay.ts');
const { initialState } = await import('./sim/engine.ts');
const { mirrorAfterPersist } = await import('./sim/store.tsx');
const { getDefaultSaveStore, resetSaveStoreForTests } = await import('./sim/saveStore.ts');

resetSaveStoreForTests();

async function settle() {
  // mirrorAfterPersist's durable write is a fire-and-forget promise chain
  // over an in-memory KV store — no real I/O, so a couple of microtask
  // turns is more than enough for it to have resolved.
  for (let i = 0; i < 20; i++) await new Promise((r) => setTimeout(r, 5));
}

async function runCase(label, lineageId) {
  const now = new Date(2026, 8, 5, 10);
  for (let seq = 5; seq <= 7; seq++) {
    const sp = createSavepoint({ ...initialState(), tick: 900 + seq, lineageId }, [], new Date(now.getTime() - (8 - seq) * 60_000), 'v1', null, seq);
    sp.lineageId = lineageId;
    const ok = persistSavepoint(window.localStorage, sp);
    if (!ok) {
      console.log(label + '-SETUP-BROKEN-SEED');
      return;
    }
  }

  const stale = createSavepoint({ ...initialState(), tick: 100, lineageId }, [], now, 'v1', null, 3);
  stale.lineageId = lineageId;
  const rejection = persistSavepointWithReason(window.localStorage, stale, now);
  if (rejection.reason !== 'stale-overwrite') {
    console.log(label + '-SETUP-BROKEN-REJECTION:' + JSON.stringify(rejection));
    return;
  }

  const overflowKey = !lineageId || lineageId === 'legacy' ? 'metropolis.savepoint.idbOnly' : \`metropolis.savepoint.\${lineageId}.idbOnly\`;
  const store = getDefaultSaveStore();
  const before = await store.getItem(overflowKey);
  if (before !== null) {
    console.log(label + '-SETUP-BROKEN-OVERFLOW-NOT-EMPTY');
    return;
  }

  // THE REAL PRODUCTION CALL — never mirrorSavepointDirect/guardedSavepointSetItem directly.
  mirrorAfterPersist(false, stale, rejection.reason);
  await settle();

  const after = await store.getItem(overflowKey);
  console.log(label + (after === null ? '-OK-REFUSED' : '-WRONGLY-ACCEPTED'));
}

await runCase('LEGACY', undefined);
await runCase('NAMESPACED', 'city-wire-704');
`;

describe('BUG-704 round REJECT (P1): the REAL store.tsx wiring (localSavepointBaselines -> mirrorAfterPersist -> mirrorSavepointDirect) must gate the durable write, legacy AND namespaced keyspace', () => {
  test('baseline (unmutated) sanity: both keyspaces refuse the stale write through the real wrapper', () => {
    const output = runBaselineProbe({
      targetRelPath: 'sim/store.tsx',
      childBody: PROBE_CHILD_BODY,
      extraArgs: ['--import', 'tsx/esm'],
      timeoutMs: 60000,
    });
    assert.doesNotMatch(output, /SETUP-BROKEN/, `probe setup must not be broken: ${output}`);
    assert.match(output, /LEGACY-OK-REFUSED/, `legacy keyspace must refuse through the real wrapper: ${output}`);
    assert.match(output, /NAMESPACED-OK-REFUSED/, `namespaced keyspace must refuse through the real wrapper: ${output}`);
  });

  test('RED-PROOF: mutating localSavepointBaselines to return [] (the fix fully inert) makes the durable store wrongly accept the stale write in BOTH keyspaces', () => {
    const mutantOutput = runWithMutant({
      targetRelPath: 'sim/store.tsx',
      mutate: (src) => {
        const needle = 'export function localSavepointBaselines(lineageId?: string): string[] {';
        if (!src.includes(needle)) {
          throw new Error('RED-PROOF setup is broken: localSavepointBaselines signature not found — has it moved or been renamed?');
        }
        const idx = src.indexOf(needle);
        const bodyStart = src.indexOf('{', idx);
        // Find the matching closing brace for this function by simple depth counting.
        let depth = 0;
        let i = bodyStart;
        for (; i < src.length; i++) {
          if (src[i] === '{') depth++;
          else if (src[i] === '}') {
            depth--;
            if (depth === 0) break;
          }
        }
        if (depth !== 0) {
          throw new Error('RED-PROOF setup is broken: could not find the end of localSavepointBaselines');
        }
        const before = src.slice(0, bodyStart + 1);
        const after = src.slice(i);
        return before + ' return []; ' + after;
      },
      childBody: PROBE_CHILD_BODY,
      extraArgs: ['--import', 'tsx/esm'],
      timeoutMs: 60000,
    });
    assert.doesNotMatch(mutantOutput, /SETUP-BROKEN/, `mutant probe setup must not be broken: ${mutantOutput}`);
    assert.match(mutantOutput, /LEGACY-WRONGLY-ACCEPTED/, `RED-PROOF: legacy keyspace must wrongly accept once the real baseline-gathering is inert: ${mutantOutput}`);
    assert.match(mutantOutput, /NAMESPACED-WRONGLY-ACCEPTED/, `RED-PROOF: namespaced keyspace must wrongly accept once the real baseline-gathering is inert: ${mutantOutput}`);
  });
});
