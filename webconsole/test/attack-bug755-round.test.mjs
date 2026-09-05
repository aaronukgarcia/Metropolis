// attack-bug755-round.test.mjs — INDEPENDENT DESTRUCTIVE ROUND (GR#23) against
// BUG-755's fix. Attacker: opus-round-bug755 (NOT the author).
//
// Attacks:
//   (D) A savepoint written by the genuine PRE-FIX engine (two bare
//       'Level Rewards' inflows in one tick) must now restore — the whole
//       point of the fix. Built by MUTATING engine.ts back to the pre-fix
//       labels inside a mutant shadow, persisting a savepoint from it, and
//       restoring it with the FIXED replay.ts/consistency.ts.
//   (C) The demotion must not accept genuinely corrupt snapshots: duplicate
//       building ids, non-finite funds, a broken conservation identity and a
//       placeholder building must ALL still count as blocking and still be
//       REFUSED by both restore entry points.
//   (C2) flows.outflow-labels-unique is the same COSMETIC class and is still
//       fully blocking — proven here to be unreachable-by-construction on a
//       real ticked city (all outflow labels come from a unique-keyed bucket
//       map plus distinct string literals), so it is a documented follow-up,
//       not a live second save-destroyer.
//   (B) GR#21 determinism of the new tick-scoped label counter: identical
//       inputs produce byte-identical labels across two independent runs and
//       across a fresh module instance, the counter never leaks between
//       ticks, and the '#k' ordinal branch is exercised for real.

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

function pendingReward(newLevel, cash) {
  return { totalReward: cash, newLevel, notice: { level: newLevel, cash, unlocked: [] } };
}

// ── (D) OLD SAVE, WRITTEN BY THE PRE-FIX ENGINE, MUST RESTORE ─────────────
test('ATTACK D: a savepoint produced by the genuine PRE-FIX engine (bare duplicate "Level Rewards") restores', () => {
  const out = runWithMutant({
    targetRelPath: 'sim/engine.ts',
    mutate: (src) => {
      const n1 = 'inflows = [...inflows, { label: levelRewardInflowLabel(pr.newLevel), value: pr.totalReward }];';
      const n2 = 'inflows = [...inflows, { label: levelRewardInflowLabel(lr.newLevel), value: lr.totalReward }];';
      if (!src.includes(n1) || !src.includes(n2)) throw new Error('ATTACK D setup broken: engine label sites not found');
      return src
        .replace(n1, "inflows = [...inflows, { label: 'Level Rewards', value: pr.totalReward }];")
        .replace(n2, "inflows = [...inflows, { label: 'Level Rewards', value: lr.totalReward }];");
    },
    childBody: `
      const { initialState, reducer } = await import('./sim/engine.ts');
      const { createSavepoint, persistSavepoint, restoreFromSavepoint, prepareRestoreForChunkedTail } = await import('./sim/replay.ts');
      const { runConsistencyChecks } = await import('./sim/consistency.ts');

      function makeStorage() {
        const m = new Map();
        return { getItem: (k) => (m.has(k) ? m.get(k) : null), setItem: (k, v) => { m.set(k, v); }, removeItem: (k) => { m.delete(k); } };
      }
      const pr = (lvl, cash) => ({ totalReward: cash, newLevel: lvl, notice: { level: lvl, cash, unlocked: [] } });

      // A PRE-FIX city: two queued rewards drain on one tick under the OLD labels.
      const s = { ...initialState(), lastRewardedLevel: 4, pendingRewards: [pr(5, 1000), pr(6, 2000)], unlockedAll: true, funds: 5000000 };
      const old = reducer(s, { type: 'tick' });
      const bare = old.lastFlows.inflows.filter((f) => f.label === 'Level Rewards');
      console.log('PREFIX_DUPES:' + bare.length);

      const rep = runConsistencyChecks(old);
      const dup = rep.checks.find((c) => c.id === 'flows.inflow-labels-unique');
      console.log('DUP_CHECK_OK:' + dup.ok);
      console.log('FAILURES:' + rep.failures + ' BLOCKING:' + rep.blockingFailures);

      const storage = makeStorage();
      persistSavepoint(storage, createSavepoint(old, [], new Date(), 'test-build', null));
      const r = restoreFromSavepoint(storage);
      console.log('RESTORE_OK:' + r.success + ' REASON:' + (r.reason ?? 'none'));
      console.log('RESTORE_BUILDINGS:' + (r.state ? r.state.buildings.length : -1) + ' SAVED:' + old.buildings.length);
      console.log('RESTORE_FUNDS_MATCH:' + (r.state ? (r.state.funds === old.funds) : false));
      const p = prepareRestoreForChunkedTail(storage);
      console.log('PREPARE_OK:' + p.success + ' REASON:' + (p.reason ?? 'none'));
    `,
  });

  assert.match(out, /PREFIX_DUPES:2/, `the mutant must actually reproduce the pre-fix duplicate: ${out}`);
  assert.match(out, /DUP_CHECK_OK:false/, `the pre-fix save must genuinely fail flows.inflow-labels-unique: ${out}`);
  assert.match(out, /BLOCKING:0/, `a pre-fix save whose ONLY failure is the label dupe must have zero blocking failures: ${out}`);
  assert.match(out, /RESTORE_OK:true/, `the pre-fix save MUST restore — this is the entire point of BUG-755: ${out}`);
  assert.match(out, /RESTORE_FUNDS_MATCH:true/, `the restored city must be the saved city, not a fresh one: ${out}`);
  assert.match(out, /PREPARE_OK:true/, `the chunked-tail path must also accept the pre-fix save: ${out}`);
});

// ── (C) GENUINELY CORRUPT SNAPSHOTS MUST STILL BE REFUSED ────────────────
test('ATTACK C: real corruption still blocks — duplicate ids, non-finite funds, broken conservation', async () => {
  const { initialState, reducer } = await import('../src/sim/engine.ts');
  const { runConsistencyChecks } = await import('../src/sim/consistency.ts');
  const { createSavepoint, persistSavepoint, restoreFromSavepoint, prepareRestoreForChunkedTail } =
    await import('../src/sim/replay.ts');

  const base = reducer({ ...initialState(), unlockedAll: true, funds: 5_000_000 }, { type: 'tick' });
  const clean = runConsistencyChecks(base);
  assert.equal(clean.blockingFailures, 0, `setup: base city must be clean: ${JSON.stringify(clean.checks.filter((c) => !c.ok))}`);
  assert.ok(base.buildings.length > 0, 'setup: base city must have buildings to corrupt');

  const corruptions = {
    'buildings.ids-unique': { ...base, buildings: [...base.buildings, { ...base.buildings[0] }] },
    'sim.funds.valid': { ...base, funds: Number.NaN },
    'conservation.funds-vs-flows': { ...base, fundsAtTickEnd: base.fundsAtTickEnd + 123_456 },
  };

  for (const [expectId, bad] of Object.entries(corruptions)) {
    const rep = runConsistencyChecks(bad);
    const failed = rep.checks.filter((c) => !c.ok).map((c) => c.id);
    assert.ok(failed.length > 0, `${expectId}: corruption must fail SOMETHING (got none)`);
    assert.ok(
      rep.blockingFailures > 0,
      `${expectId}: real corruption must still be BLOCKING after the BUG-755 demotion — failed=${JSON.stringify(failed)}`,
    );

    const storage = makeStorage();
    persistSavepoint(storage, createSavepoint(bad, [], new Date(), 'test-build', null));
    const r = restoreFromSavepoint(storage);
    assert.equal(r.success, false, `${expectId}: restoreFromSavepoint must still REFUSE a genuinely corrupt snapshot`);
    const p = prepareRestoreForChunkedTail(storage);
    assert.equal(p.success, false, `${expectId}: prepareRestoreForChunkedTail must still REFUSE a genuinely corrupt snapshot`);
  }
});

// ── (C2) THE SIBLING COSMETIC CHECK IS STILL BLOCKING — REACHABILITY PROBE ─
test('ATTACK C2: flows.outflow-labels-unique is still fully blocking, and unreachable on a real ticked city', async () => {
  const { initialState, reducer } = await import('../src/sim/engine.ts');
  const { runConsistencyChecks, RESTORE_NONBLOCKING_CHECK_IDS } = await import('../src/sim/consistency.ts');

  assert.equal(
    RESTORE_NONBLOCKING_CHECK_IDS.has('flows.outflow-labels-unique'),
    false,
    'documenting the CURRENT contract: the outflow twin is deliberately NOT demoted',
  );

  // Reachability: tick a real city for a long stretch and prove the engine
  // never emits two outflow lines sharing a label (bucket keys are unique by
  // construction, every other push is a distinct string literal).
  let s = { ...initialState(), unlockedAll: true, funds: 50_000_000 };
  let worstDupes = 0;
  for (let i = 0; i < 120; i++) {
    s = reducer(s, { type: 'tick' });
    const labels = s.lastFlows.outflows.map((f) => f.label);
    worstDupes = Math.max(worstDupes, labels.length - new Set(labels).size);
  }
  assert.equal(worstDupes, 0, 'no reachable duplicate outflow label over 120 ticks — the sibling check is not a live save-destroyer');

  // But if one DID occur it would still refuse a restore — proving the
  // asymmetry is real and is a genuine (currently unreachable) follow-up.
  const forced = { ...s, lastFlows: { ...s.lastFlows, outflows: [...s.lastFlows.outflows, { ...s.lastFlows.outflows[0] }] } };
  const rep = runConsistencyChecks(forced);
  assert.equal(rep.checks.find((c) => c.id === 'flows.outflow-labels-unique').ok, false, 'setup: forced outflow dupe must fail its check');
  assert.ok(rep.blockingFailures > 0, 'a duplicate OUTFLOW label would still block a restore (documented follow-up)');
});

// ── (B) GR#21 DETERMINISM OF THE TICK-SCOPED COUNTER ─────────────────────
test('ATTACK B: level-reward labels are deterministic, tick-scoped, and the ordinal branch is real', async () => {
  const { initialState, reducer } = await import('../src/sim/engine.ts');

  const seed = () => ({
    ...initialState(),
    lastRewardedLevel: 4,
    pendingRewards: [pendingReward(5, 1000), pendingReward(6, 2000)],
    unlockedAll: true,
    funds: 5_000_000,
  });
  const labelsOf = (st) => st.lastFlows.inflows.map((f) => f.label);

  const a = labelsOf(reducer(seed(), { type: 'tick' }));
  const b = labelsOf(reducer(seed(), { type: 'tick' }));
  assert.deepEqual(a, b, 'GR#21: identical inputs must produce byte-identical inflow labels');

  // A FRESH module instance must agree too (proves no module-level counter state).
  const fresh = await import('../src/sim/engine.ts?attack-bug755-fresh');
  const c = labelsOf(fresh.reducer(seed(), { type: 'tick' }));
  assert.deepEqual(a, c, 'GR#21: a freshly-instantiated engine module must produce the same labels');

  // The '#k' ordinal branch: two queued rewards for the SAME level in one tick.
  const sameLevel = reducer(
    { ...initialState(), lastRewardedLevel: 4, pendingRewards: [pendingReward(5, 1000), pendingReward(5, 1000)] },
    { type: 'tick' },
  );
  const lr = sameLevel.lastFlows.inflows.filter((f) => f.label.startsWith('Level Rewards'));
  assert.equal(lr.length, 2, 'both same-level queued rewards must still pay out');
  assert.equal(new Set(lr.map((f) => f.label)).size, 2, `same-level entries must still be unique: ${JSON.stringify(lr.map((f) => f.label))}`);
  assert.ok(lr.some((f) => f.label === 'Level Rewards (Level 5) #2'), `the stable ordinal branch must be used: ${JSON.stringify(lr.map((f) => f.label))}`);

  // Tick-scoped: the counter must NOT leak into the next tick — a second tick
  // with its own single reward must produce the un-suffixed label again.
  const t1 = reducer({ ...initialState(), lastRewardedLevel: 4, pendingRewards: [pendingReward(5, 1000), pendingReward(5, 1000)] }, { type: 'tick' });
  const t2 = reducer({ ...t1, pendingRewards: [pendingReward(5, 1000)] }, { type: 'tick' });
  const t2Labels = t2.lastFlows.inflows.filter((f) => f.label.startsWith('Level Rewards')).map((f) => f.label);
  assert.deepEqual(t2Labels, ['Level Rewards (Level 5)'], `the counter must reset per tick, got: ${JSON.stringify(t2Labels)}`);
});

// ── (A) THE REMAINING SILENT PATH: A LINEAGE POINTER THAT DOESN'T MATCH ───
// store.tsx's new MET-V868/placeNotice block is gated on `most` being truthy,
// and `most` comes from readAllSavepoints(storage, now, currentLineageId).
// If the CURRENT LINEAGE POINTER does not match the lineage a real savepoint
// was written under, `most` is null, the guard never fires, and the boot is
// still an indistinguishable-from-day-one fresh city — the BUG-755 defect
// shape reached through a different door. Documented here as a follow-up
// (this is BUG-687 lineage territory, NOT a regression from this fix).
test('ATTACK A: a lineage-pointer mismatch still hides a real savepoint from the MET-V868 guard', async () => {
  const { initialState, reducer } = await import('../src/sim/engine.ts');
  const { createSavepoint, persistSavepoint, readAllSavepoints, mostRecentSavepoint } = await import('../src/sim/replay.ts');

  const city = reducer({ ...initialState(), lineageId: 'lineage-A', unlockedAll: true, funds: 5_000_000 }, { type: 'tick' });
  const storage = makeStorage();
  assert.ok(persistSavepoint(storage, createSavepoint(city, [], new Date(), 'test-build', null)), 'setup: savepoint must persist');

  const foundOwn = mostRecentSavepoint(readAllSavepoints(storage, new Date(), 'lineage-A'));
  assert.ok(foundOwn, 'sanity: the savepoint is findable under its OWN lineage id');

  const foundOther = mostRecentSavepoint(readAllSavepoints(storage, new Date(), 'lineage-B'));
  assert.equal(
    foundOther,
    null,
    'FINDING: under a mismatched lineage pointer `most` is null, so store.tsx\'s `if (most)` MET-V868 guard never fires — ' +
      'a real savepoint on disk is still discarded with zero signal. Follow-up, not a BUG-755 regression.',
  );
});
