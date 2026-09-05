// bug755-duplicate-inflow-labels.test.mjs — BUG-755 (P0, save loss).
//
// ROOT CAUSE (investigation lane): engine.ts's pending-reward drain loop
// (~2787) and the in-tick level-crossing loop (~3021) both pushed the SAME
// bare 'Level Rewards' inflow label. A tick draining 2+ queued rewards (or a
// queued reward plus an in-tick crossing) wrote DUPLICATE inflow labels into
// lastFlows. The autosave of that tick persists fine (saveGame returns
// true), but the NEXT boot's consistency.ts flows.inflow-labels-unique check
// rejects the snapshot, and store.tsx's boot initializer silently discarded
// the whole city for a fresh one.
//
// THIS FILE covers the lead's ruling parts 1 and 2:
//   (1) engine.ts: both sites now label each reward
//       `Level Rewards (Level ${level})`, with a stable ordinal suffix if two
//       entries somehow still share a level in one tick.
//   (2) consistency.ts: flows.inflow-labels-unique is a WARNING-class check
//       for the RESTORE path (consistency.ts's blockingFailures /
//       RESTORE_NONBLOCKING_CHECK_IDS) — a cosmetic label collision must
//       never, by itself, refuse a restore — while it stays a HARD failure
//       in the full debug/consistency report (`failures`).
//
// Part 3 (the boot-time loud-refusal registry error + placeNotice) is
// covered by bug755-restore-refusal-loud.test.tsx +
// bug755-restore-refusal-loud-redproof.test.mjs.
//
// RED-PROOFS (webconsole/testsupport/mutant.mjs, GR#24: real src is never
// touched — only a disposable shadow copy is mutated):
//   - reverting engine.ts's two sites to the bare 'Level Rewards' label
//     reproduces the duplicate-label defect directly.
//   - reverting consistency.ts's RESTORE_NONBLOCKING_CHECK_IDS to empty
//     makes a savepoint with ONLY a cosmetic label collision get refused on
//     restore again (BUG-755's actual save-loss mechanism).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { runWithMutant } from '../testsupport/mutant.mjs';

function makeStorage() {
  const m = new Map();
  return {
    getItem: (k) => (m.has(k) ? m.get(k) : null),
    setItem: (k, v) => { m.set(k, v); },
    removeItem: (k) => { m.delete(k); },
  };
}

/** A LevelRewardResult-shaped pending reward, as isValidLevelReward requires
 *  (engine.ts's BUG-600 sanitizer): totalReward, newLevel, notice{level,cash,unlocked[]}. */
function pendingReward(newLevel, cash) {
  return { totalReward: cash, newLevel, notice: { level: newLevel, cash, unlocked: [] } };
}

// ═══════════════════════════════════════════════════════════════════════
// PART 1 — engine.ts: unique per-level inflow labels.
// ═══════════════════════════════════════════════════════════════════════

test('BUG-755 part 1: two pending level rewards draining on ONE tick get UNIQUE inflow labels', async () => {
  const { initialState, reducer } = await import('../src/sim/engine.ts');
  const { runConsistencyChecks } = await import('../src/sim/consistency.ts');

  const s = { ...initialState(), lastRewardedLevel: 4, pendingRewards: [pendingReward(5, 1000), pendingReward(6, 2000)] };
  const out = reducer(s, { type: 'tick' });

  const levelInflows = out.lastFlows.inflows.filter((f) => f.label.startsWith('Level Rewards'));
  assert.equal(levelInflows.length, 2, 'both queued rewards must still pay out as separate inflow lines');
  const labels = levelInflows.map((f) => f.label);
  assert.equal(new Set(labels).size, 2, `the two inflow labels must be UNIQUE, got: ${JSON.stringify(labels)}`);
  assert.ok(labels.includes('Level Rewards (Level 5)'), `expected a 'Level Rewards (Level 5)' label, got: ${JSON.stringify(labels)}`);
  assert.ok(labels.includes('Level Rewards (Level 6)'), `expected a 'Level Rewards (Level 6)' label, got: ${JSON.stringify(labels)}`);

  const report = runConsistencyChecks(out);
  const dupCheck = report.checks.find((c) => c.id === 'flows.inflow-labels-unique');
  assert.equal(dupCheck.ok, true, `flows.inflow-labels-unique must PASS once labels are unique: ${dupCheck.detail}`);
});

test('BUG-755 part 1: a pending reward AND an in-tick crossing on the SAME tick also get unique labels', async () => {
  const { initialState, reducer, xpForLevel, computeLevelRewards } = await import('../src/sim/engine.ts');
  const { runConsistencyChecks } = await import('../src/sim/consistency.ts');

  // lastRewardedLevel=5 (level 5 was paid in an EARLIER tick), one xp short
  // of level 6's threshold, PLUS a leftover pending reward for level 3 still
  // sitting in the queue (a defensive shape — the sanitizer never assumes
  // ordering — proving the dedupe counter is shared across BOTH loops
  // regardless of which levels they carry). advance()'s own tempState = {
  // ...s, xp: s.xp + 1 } then crosses exactly level 6 in-tick.
  const xpJustBelowLevel6 = xpForLevel(6) - 1;
  const s = {
    ...initialState(),
    lastRewardedLevel: 5,
    xp: xpJustBelowLevel6,
    pendingRewards: [pendingReward(3, 500)],
  };
  // Setup sanity: exactly one in-tick crossing (level 6), matching advance()'s
  // own newXp = s.xp + 1 computation.
  const probeRewards = computeLevelRewards({ ...s, xp: s.xp + 1 });
  assert.equal(probeRewards.length, 1, 'test setup: xpForLevel(6)-1 plus 1 must cross exactly one level');
  assert.equal(probeRewards[0].newLevel, 6, 'test setup: the in-tick crossing must land on level 6');

  const out = reducer(s, { type: 'tick' });
  const levelInflows = out.lastFlows.inflows.filter((f) => f.label.startsWith('Level Rewards'));
  assert.equal(levelInflows.length, 2, `expected the pending level-3 reward AND the in-tick level-6 crossing to both pay out: ${JSON.stringify(out.lastFlows.inflows)}`);
  const labels = levelInflows.map((f) => f.label);
  assert.equal(new Set(labels).size, 2, `labels must be unique across pending+in-tick, got: ${JSON.stringify(labels)}`);

  const report = runConsistencyChecks(out);
  const dupCheck = report.checks.find((c) => c.id === 'flows.inflow-labels-unique');
  assert.equal(dupCheck.ok, true, `flows.inflow-labels-unique must PASS: ${dupCheck.detail}`);
});

test('BUG-755 part 1 RED-PROOF: reverting engine.ts to the bare "Level Rewards" label reproduces the duplicate', () => {
  const mutantOutput = runWithMutant({
    targetRelPath: 'sim/engine.ts',
    mutate: (src) => {
      const needle1 = "inflows = [...inflows, { label: levelRewardInflowLabel(pr.newLevel), value: pr.totalReward }];";
      const needle2 = "inflows = [...inflows, { label: levelRewardInflowLabel(lr.newLevel), value: lr.totalReward }];";
      if (!src.includes(needle1) || !src.includes(needle2)) {
        throw new Error('RED-PROOF setup is broken: the BUG-755 label call sites were not found — have they moved?');
      }
      return src
        .replace(needle1, "inflows = [...inflows, { label: 'Level Rewards', value: pr.totalReward }];")
        .replace(needle2, "inflows = [...inflows, { label: 'Level Rewards', value: lr.totalReward }];");
    },
    childBody: `
      const { initialState, reducer } = await import('./sim/engine.ts');
      const s = {
        ...initialState(),
        lastRewardedLevel: 4,
        pendingRewards: [
          { totalReward: 1000, newLevel: 5, notice: { level: 5, cash: 1000, unlocked: [] } },
          { totalReward: 2000, newLevel: 6, notice: { level: 6, cash: 2000, unlocked: [] } },
        ],
      };
      const out = reducer(s, { type: 'tick' });
      const levelInflows = out.lastFlows.inflows.filter((f) => f.label === 'Level Rewards');
      console.log('DUP_COUNT:' + levelInflows.length);
    `,
    timeoutMs: 60000,
  });
  assert.match(mutantOutput, /DUP_COUNT:2/, `mutant must reproduce exactly 2 entries sharing the bare 'Level Rewards' label: ${mutantOutput}`);
});

// ═══════════════════════════════════════════════════════════════════════
// PART 2 — consistency.ts: flows.inflow-labels-unique is WARNING-class for
// restore (never blocks), still HARD in the full report.
// ═══════════════════════════════════════════════════════════════════════

/** Manually reproduce the OLD-bug shape directly on lastFlows (bypassing
 *  engine.ts's fix entirely) so this test exercises consistency.ts/replay.ts
 *  in isolation: duplicate one inflow entry, keeping conservation intact by
 *  bumping funds/fundsAtTickEnd by the same amount. */
function duplicateOneInflowLabel(state) {
  const inflows = state.lastFlows.inflows;
  if (inflows.length === 0) throw new Error('test setup: state has no inflow to duplicate');
  const dupe = { ...inflows[0] };
  return {
    ...state,
    funds: state.funds + dupe.value,
    fundsAtTickEnd: state.fundsAtTickEnd + dupe.value,
    lastFlows: { ...state.lastFlows, inflows: [...inflows, dupe] },
  };
}

test('BUG-755 part 2: a state with ONLY a duplicate-inflow-label failure is NOT counted in blockingFailures', async () => {
  const { initialState, reducer } = await import('../src/sim/engine.ts');
  const { runConsistencyChecks, RESTORE_NONBLOCKING_CHECK_IDS } = await import('../src/sim/consistency.ts');

  assert.ok(RESTORE_NONBLOCKING_CHECK_IDS.has('flows.inflow-labels-unique'), 'flows.inflow-labels-unique must be registered as restore-non-blocking');

  const s = { ...initialState(), lastRewardedLevel: 4, pendingRewards: [pendingReward(5, 1000)] };
  const clean = reducer(s, { type: 'tick' });
  const cleanReport = runConsistencyChecks(clean);
  assert.equal(cleanReport.blockingFailures, 0, `setup: the clean, pre-duplication state must have zero blocking failures: ${JSON.stringify(cleanReport.checks.filter((c) => !c.ok))}`);

  const corrupted = duplicateOneInflowLabel(clean);
  const corruptedReport = runConsistencyChecks(corrupted);
  const dupCheck = corruptedReport.checks.find((c) => c.id === 'flows.inflow-labels-unique');
  assert.equal(dupCheck.ok, false, 'test setup: the duplicated state must actually fail flows.inflow-labels-unique');
  // Still hard-flagged in the FULL report...
  assert.ok(corruptedReport.failures > cleanReport.failures, 'the cosmetic failure must still count toward the full `failures` total (debug/consistency report)');
  // ...but NEVER counted as a BLOCKING failure.
  assert.equal(corruptedReport.blockingFailures, cleanReport.blockingFailures, 'a duplicate-label-ONLY failure must add ZERO to blockingFailures');
});

test('BUG-755 part 2: a savepoint with ONLY the duplicate-label failure still RESTORES successfully (the save is not lost)', async () => {
  const { initialState, reducer } = await import('../src/sim/engine.ts');
  const { createSavepoint, persistSavepoint, restoreFromSavepoint, prepareRestoreForChunkedTail } = await import('../src/sim/replay.ts');

  const s = { ...initialState(), lastRewardedLevel: 4, pendingRewards: [pendingReward(5, 1000)], unlockedAll: true, funds: 5_000_000 };
  const ticked = reducer(s, { type: 'tick' });
  const savedCity = duplicateOneInflowLabel(ticked);
  const savedBuildingCount = savedCity.buildings.length;
  const savedFunds = savedCity.funds;

  const storage = makeStorage();
  assert.ok(persistSavepoint(storage, createSavepoint(savedCity, [], new Date(), 'test-build', null)), 'test setup: persisting the savepoint must succeed');

  const restored = restoreFromSavepoint(storage);
  assert.equal(restored.success, true, `restoreFromSavepoint must SUCCEED despite the cosmetic label duplication: ${restored.reason}`);
  assert.equal(restored.state.buildings.length, savedBuildingCount, 'the restored city must be the SAME city that was saved (buildings), not a discarded/fresh one');
  assert.equal(restored.state.funds, savedFunds, 'the restored city must be the SAME city that was saved (funds)');

  const prepared = prepareRestoreForChunkedTail(storage);
  assert.equal(prepared.success, true, `prepareRestoreForChunkedTail must SUCCEED despite the cosmetic label duplication: ${prepared.reason}`);
  assert.equal(prepared.state.buildings.length, savedBuildingCount, 'the chunked-tail-prepared city must be the SAME saved city');
});

test('BUG-755 part 2 RED-PROOF: without RESTORE_NONBLOCKING_CHECK_IDS classifying the check, the save IS lost on restore', () => {
  const mutantOutput = runWithMutant({
    targetRelPath: 'sim/consistency.ts',
    mutate: (src) => {
      const needle = "export const RESTORE_NONBLOCKING_CHECK_IDS: ReadonlySet<string> = new Set([\n  'flows.inflow-labels-unique',\n]);";
      if (!src.includes(needle)) {
        throw new Error('RED-PROOF setup is broken: RESTORE_NONBLOCKING_CHECK_IDS definition not found — has it moved?');
      }
      return src.replace(needle, "export const RESTORE_NONBLOCKING_CHECK_IDS: ReadonlySet<string> = new Set([]);");
    },
    childBody: `
      const { initialState, reducer } = await import('./sim/engine.ts');
      const { createSavepoint, persistSavepoint, restoreFromSavepoint } = await import('./sim/replay.ts');

      function makeStorage() {
        const m = new Map();
        return {
          getItem: (k) => (m.has(k) ? m.get(k) : null),
          setItem: (k, v) => { m.set(k, v); },
          removeItem: (k) => { m.delete(k); },
        };
      }

      const s = {
        ...initialState(),
        lastRewardedLevel: 4,
        pendingRewards: [{ totalReward: 1000, newLevel: 5, notice: { level: 5, cash: 1000, unlocked: [] } }],
        unlockedAll: true,
        funds: 5_000_000,
      };
      const ticked = reducer(s, { type: 'tick' });
      const dupe = { ...ticked.lastFlows.inflows[0] };
      const savedCity = {
        ...ticked,
        funds: ticked.funds + dupe.value,
        fundsAtTickEnd: ticked.fundsAtTickEnd + dupe.value,
        lastFlows: { ...ticked.lastFlows, inflows: [...ticked.lastFlows.inflows, dupe] },
      };

      const storage = makeStorage();
      persistSavepoint(storage, createSavepoint(savedCity, [], new Date(), 'test-build', null));
      const restored = restoreFromSavepoint(storage);
      console.log('RESTORE_SUCCESS:' + restored.success);
    `,
    timeoutMs: 60000,
  });
  assert.match(mutantOutput, /RESTORE_SUCCESS:false/, `RED-PROOF: without the warning-class classification, a cosmetic label duplication must WRONGLY refuse the restore (BUG-755's actual save-loss mechanism): ${mutantOutput}`);
});

// ═══════════════════════════════════════════════════════════════════════
// END-TO-END REGRESSION: two queued rewards drain on one tick, save,
// restore — the restored city must be the SAVED city (no save loss).
// ═══════════════════════════════════════════════════════════════════════

test('BUG-755 regression: two queued level rewards draining on one tick -> save -> restore yields the SAVED city, not a fresh one', async () => {
  const { initialState, reducer } = await import('../src/sim/engine.ts');
  const { createSavepoint, persistSavepoint, restoreFromSavepoint, prepareRestoreForChunkedTail } = await import('../src/sim/replay.ts');
  const { runConsistencyChecks } = await import('../src/sim/consistency.ts');

  let s = { ...initialState(), unlockedAll: true, funds: 5_000_000 };
  for (let i = 0; i < 6; i++) {
    s = reducer(s, { type: 'place', spec: 'road', x: 10 + i, y: 10 });
  }
  // Queue TWO pending rewards to drain on the SAME upcoming tick — the exact
  // BUG-755 repro shape.
  s = { ...s, lastRewardedLevel: 4, pendingRewards: [pendingReward(5, 1000), pendingReward(6, 2000)] };

  const drained = reducer(s, { type: 'tick' });
  const report = runConsistencyChecks(drained);
  assert.equal(report.blockingFailures, 0, `the drained-tick city must have zero BLOCKING consistency failures: ${JSON.stringify(report.checks.filter((c) => !c.ok))}`);

  const savedBuildingCount = drained.buildings.length;
  const savedFunds = drained.funds;
  const savedTick = drained.tick;

  const storage = makeStorage();
  assert.ok(persistSavepoint(storage, createSavepoint(drained, [], new Date(), 'test-build', null)), 'the drained-tick city must save successfully (mirrors "the autosave of that tick persists fine")');

  const restored = restoreFromSavepoint(storage);
  assert.equal(restored.success, true, `the NEXT boot must restore the saved city, not refuse it: ${restored.reason}`);
  assert.equal(restored.state.buildings.length, savedBuildingCount, 'THE RESTORED CITY MUST BE THE SAVED CITY (buildings) — this is the save-loss BUG-755 exists to fix');
  assert.equal(restored.state.funds, savedFunds, 'THE RESTORED CITY MUST BE THE SAVED CITY (funds)');
  assert.equal(restored.state.tick, savedTick, 'THE RESTORED CITY MUST BE THE SAVED CITY (tick)');

  const prepared = prepareRestoreForChunkedTail(storage);
  assert.equal(prepared.success, true, `prepareRestoreForChunkedTail (the REAL boot path, BUG-617) must also restore the saved city: ${prepared.reason}`);
  assert.equal(prepared.state.buildings.length, savedBuildingCount, 'the chunked-tail-prepared city must be the SAVED city (buildings)');
});
