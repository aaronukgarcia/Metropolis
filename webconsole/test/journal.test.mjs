// journal.test.mjs — FEAT-1972079854: deterministic input journal and replay testing.
//
// KEYSTONE TEST: record a scripted action sequence, replay onto a fresh initial state,
// assert deep-equal final SimState. Proves that the journal + reducer = determinism.
// RED/GREEN: mutating replay to skip one action makes deep-equal fail, proving coverage.
//
// Tests cover:
// 1. Action classification (isStateAffecting): UI-only vs state-affecting split
// 2. Journal recording: ring-buffer, capping, ordering
// 3. Replay determinism: same journal → same state
// 4. Savepoint round-trip: persist & restore through localStorage
// 5. Consistency checking: before/after replay
// 6. Storage failure handling: quota, private mode → graceful degradation

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import {
  isStateAffecting,
  recordAction,
  emptyJournal,
  journalSize,
  journalTail,
  persistJournal,
  loadJournal,
  JOURNAL_CAP,
  JOURNAL_PERSIST_MAX_CHARS,
  JOURNAL_KEY,
} from '../src/sim/journal.ts';
import {
  restoreFromSavepoint,
  persistSavepoint,
  readAllSavepoints,
  mostRecentSavepoint,
  createSavepoint,
  SAVEPOINT_CAP,
  SAVEPOINT_KEY_PREFIX,
} from '../src/sim/replay.ts';
import { initialState, reducer } from '../src/sim/engine.ts';
import { runConsistencyChecks } from '../src/sim/consistency.ts';
import { decode } from '../src/sim/saveCodec.ts';

// ===== ACTION CLASSIFICATION TESTS =====

describe('isStateAffecting: action classification', () => {
  test('state-affecting actions are journaled', () => {
    // Game tick
    assert.ok(isStateAffecting({ type: 'tick' }));
    // Placement
    assert.ok(isStateAffecting({ type: 'place', spec: 'res_hut', x: 5, y: 5 }));
    // Demolition
    assert.ok(isStateAffecting({ type: 'bulldoze', x: 5, y: 5 }));
    // Move (only the actual move, not pickup/cancel)
    assert.ok(isStateAffecting({ type: 'relocate', x: 10, y: 10 }));
    // Utilities
    assert.ok(isStateAffecting({ type: 'pipeUpgrade', id: 1 }));
    assert.ok(isStateAffecting({ type: 'tax', which: 'residential', rate: 12 }));
    assert.ok(isStateAffecting({ type: 'policy', id: 'recycling' }));
    assert.ok(isStateAffecting({ type: 'loan' }));
    assert.ok(isStateAffecting({ type: 'repay' }));
    // Debug
    assert.ok(isStateAffecting({ type: 'debugFunds', amount: 100 }));
    assert.ok(isStateAffecting({ type: 'debugXp', amount: 10 }));
    // System
    assert.ok(isStateAffecting({ type: 'reset' }));
  });

  test('UI-only actions are NOT journaled', () => {
    // Speed (UI control)
    assert.ok(!isStateAffecting({ type: 'speed', speed: 2 }));
    // Tool selection (UI state)
    assert.ok(!isStateAffecting({ type: 'tool', tool: { mode: 'build', spec: 'res_hut' } }));
    // Moving UI (pickup/cancel — not the actual move)
    assert.ok(!isStateAffecting({ type: 'pickup', id: 1 }));
    assert.ok(!isStateAffecting({ type: 'cancelMove' }));
    // Notice UI
    assert.ok(!isStateAffecting({ type: 'dismissNotice' }));
    assert.ok(!isStateAffecting({ type: 'hydrate', state: initialState() }));
  });
});

// ===== JOURNAL RECORDING TESTS =====

describe('journal: recording and ring-buffer', () => {
  test('empty journal has zero entries', () => {
    const j = emptyJournal();
    assert.equal(journalSize(j), 0);
  });

  test('recording a state-affecting action increases size', () => {
    let j = emptyJournal();
    j = recordAction(j, 0, { type: 'place', spec: 'res_hut', x: 5, y: 5 });
    assert.equal(journalSize(j), 1);
    assert.equal(j.entries[0].tick, 0);
    assert.equal(j.entries[0].action.type, 'place');
  });

  test('recording UI-only actions does not increase journal size', () => {
    let j = emptyJournal();
    j = recordAction(j, 0, { type: 'speed', speed: 2 });
    j = recordAction(j, 0, { type: 'tool', tool: { mode: 'build', spec: 'res_hut' } });
    assert.equal(journalSize(j), 0, 'UI actions are excluded');
  });

  test('mixed actions: only state-affecting are recorded', () => {
    let j = emptyJournal();
    j = recordAction(j, 0, { type: 'speed', speed: 2 }); // UI-only, skipped
    j = recordAction(j, 0, { type: 'place', spec: 'res_hut', x: 5, y: 5 }); // recorded
    j = recordAction(j, 1, { type: 'tool', tool: { mode: 'select' } }); // UI-only, skipped
    j = recordAction(j, 1, { type: 'tick' }); // recorded
    assert.equal(journalSize(j), 2);
    assert.equal(j.entries[0].action.type, 'place');
    assert.equal(j.entries[1].action.type, 'tick');
  });

  test('ring-buffer: cap enforced (oldest entries evicted)', () => {
    let j = emptyJournal();
    // Fill to capacity + 1
    for (let i = 0; i < JOURNAL_CAP + 1; i++) {
      j = recordAction(j, i, { type: 'debugFunds', amount: 1 });
    }
    assert.equal(journalSize(j), JOURNAL_CAP, `journal capped at ${JOURNAL_CAP}`);
    // The first entry should be gone (oldest evicted).
    assert.equal(j.entries[0].tick, 1, 'oldest entry (tick=0) was evicted');
    assert.equal(j.entries[JOURNAL_CAP - 1].tick, JOURNAL_CAP, 'newest entry is at the end');
  });

  test('journalTail: extract entries from a given index', () => {
    let j = emptyJournal();
    for (let i = 0; i < 5; i++) {
      j = recordAction(j, i, { type: 'debugFunds', amount: i });
    }
    const tail = journalTail(j, 2);
    assert.equal(tail.length, 3);
    assert.equal(tail[0].tick, 2);
    assert.equal(tail[2].tick, 4);
  });
});

// ===== REPLAY DETERMINISM TESTS (KEYSTONE) =====

describe('replay: determinism keystone', () => {
  test('KEYSTONE: replay action sequence deterministically', () => {
    // Start with initial state.
    const s0 = initialState();

    // Record a sequence of state-affecting actions.
    const actions = [
      { type: 'place', spec: 'res_hut', x: 5, y: 5 },
      { type: 'tick' },
      { type: 'tick' },
      { type: 'tax', which: 'residential', rate: 10 },
      { type: 'place', spec: 'm20', x: 10, y: 10 },
      { type: 'tick' },
      { type: 'bulldoze', x: 5, y: 5 },
      { type: 'tick' },
    ];

    // Build journal by replaying actions on s0.
    let j = emptyJournal();
    let state1 = s0;
    for (let i = 0; i < actions.length; i++) {
      j = recordAction(j, state1.tick, actions[i]);
      state1 = reducer(state1, actions[i]);
    }

    // Now, on a FRESH initial state, replay the journal.
    const s0Fresh = initialState();
    let state2 = s0Fresh;
    for (const entry of j.entries) {
      state2 = reducer(state2, entry.action);
    }

    // Both paths should produce identical states (determinism proof).
    assert.deepEqual(state1, state2, 'replay onto fresh state matches original path');
  });

  test('RED: mutating replay (skip one action) breaks determinism', () => {
    // Same setup as keystone test.
    const s0 = initialState();
    const actions = [
      { type: 'place', spec: 'res_hut', x: 5, y: 5 },
      { type: 'tick' },
      { type: 'tick' },
    ];

    // Build journal.
    let j = emptyJournal();
    let state1 = s0;
    for (let i = 0; i < actions.length; i++) {
      j = recordAction(j, state1.tick, actions[i]);
      state1 = reducer(state1, actions[i]);
    }

    // Replay with one action SKIPPED (mutate to prove coverage).
    const s0Fresh = initialState();
    let state2 = s0Fresh;
    for (let i = 0; i < j.entries.length; i++) {
      if (i === 1) continue; // SKIP entry 1 (one of the ticks)
      state2 = reducer(state2, j.entries[i].action);
    }

    // This MUST NOT deep-equal (proves the skipped action mattered).
    assert.notDeepEqual(state1, state2, 'skipping one action breaks determinism');
  });

  test('replay journal to fresh state yields same end state', () => {
    const s0 = initialState();
    const actions = [
      { type: 'place', spec: 'res_hut', x: 5, y: 5 },
      { type: 'tick' },
      { type: 'tick' },
      { type: 'tick' },
    ];

    // Build journal on one path.
    let j = emptyJournal();
    let state1 = s0;
    for (let i = 0; i < actions.length; i++) {
      j = recordAction(j, state1.tick, actions[i]);
      state1 = reducer(state1, actions[i]);
    }

    // Replay on fresh state (determinism proof).
    const s0Fresh = initialState();
    let state2 = s0Fresh;
    for (const entry of j.entries) {
      state2 = reducer(state2, entry.action);
    }

    assert.deepEqual(state1, state2, 'replay yields identical end state');
  });

  test('long sequence: 100 ticks + placements (determinism)', () => {
    const s0 = initialState();

    // Scripted sequence: place, then tick 100 times.
    const actions = [{ type: 'place', spec: 'res_hut', x: 5, y: 5 }];
    for (let i = 0; i < 100; i++) {
      actions.push({ type: 'tick' });
    }

    // Build journal.
    let j = emptyJournal();
    let state1 = s0;
    for (let i = 0; i < actions.length; i++) {
      j = recordAction(j, state1.tick, actions[i]);
      state1 = reducer(state1, actions[i]);
    }

    // Replay on fresh state.
    const s0Fresh = initialState();
    let state2 = s0Fresh;
    for (const entry of j.entries) {
      state2 = reducer(state2, entry.action);
    }

    // Same state (determinism) - this is the keystone proof.
    assert.deepEqual(state1, state2, 'long replay sequence is deterministic');
  });
});

// ===== SAVEPOINT PERSISTENCE TESTS =====

describe('savepoint: persistence and restoration', () => {
  // Mock localStorage for tests (fail-safe, never corrupts tests).
  class MockStorage {
    constructor() {
      this.data = {};
    }
    getItem(key) {
      return this.data[key] || null;
    }
    setItem(key, value) {
      this.data[key] = value;
    }
    removeItem(key) {
      delete this.data[key];
    }
  }

  test('create and persist savepoint with journalTail', () => {
    const storage = new MockStorage();
    const s = initialState();
    let j = emptyJournal();
    j = recordAction(j, s.tick, { type: 'place', spec: 'res_hut', x: 5, y: 5 });
    j = recordAction(j, s.tick, { type: 'tick' });
    const tail = j.entries; // All entries are the tail for this test

    const savepoint = createSavepoint(s, tail, new Date('2026-08-27T12:00:00Z'));
    const success = persistSavepoint(storage, savepoint);

    assert.ok(success, 'persist should succeed with valid storage');
    const key = `${SAVEPOINT_KEY_PREFIX}.0`;
    const raw = storage.getItem(key);
    assert.ok(raw, 'savepoint should be stored');
    // FEAT-1972079935: the stored value is now compressed (LZv1: prefix) —
    // decode() before parsing (a no-op on a legacy plain-JSON value).
    const loaded = JSON.parse(decode(raw));
    assert.equal(loaded.snapshotTick, s.tick);
    // journalTail should contain the actions we passed.
    assert.equal(loaded.journalTail.length, 2);
  });

  test('restore from savepoint: round-trip (ticks only)', () => {
    const storage = new MockStorage();

    // Build a savepoint with just ticks (no between-tick mutations).
    // journalTail is empty in this test (snapshot at end of tick is complete).
    const s0 = initialState();
    let s1 = s0;
    for (let i = 0; i < 5; i++) {
      s1 = reducer(s1, { type: 'tick' });
    }

    const savepoint = createSavepoint(s1, []); // Empty tail at this checkpoint
    persistSavepoint(storage, savepoint);

    // Restore from storage.
    const result = restoreFromSavepoint(storage);
    assert.ok(result.success, `restore should succeed, reason: ${result.reason}`);
    // journalTail is empty, so 0 actions replayed.
    assert.equal(result.replayed, 0, 'no actions replayed (tail is empty)');
    // Restored state should match the snapshot exactly.
    assert.deepEqual(result.state, s1, 'restored state matches original');
  });

  test('restore non-existent savepoint fails gracefully', () => {
    const storage = new MockStorage();
    const result = restoreFromSavepoint(storage);
    assert.ok(!result.success);
    assert.equal(result.reason, 'No savepoint found');
  });

  test('rotating savepoint slots (round-robin)', () => {
    const storage = new MockStorage();

    // Create and persist SAVEPOINT_CAP + 1 savepoints.
    // BUG-469: dates are deliberately kept close together (not the 1970
    // epoch) and readAllSavepoints() below is given an explicit `now` right
    // after the last write — the epoch timestamps this test used to use are
    // now (correctly) purged as stale by the BUG-469 retention window, which
    // would otherwise make this rotation-only test collide with retention.
    let last = new Date(2026, 0, 1, 0, 0);
    for (let i = 0; i < SAVEPOINT_CAP + 1; i++) {
      const s = initialState();
      last = new Date(2026, 0, 1, 0, i); // distinct, monotonically increasing minute
      const savepoint = createSavepoint(s, [], last);
      persistSavepoint(storage, savepoint, last);
    }

    // Read all savepoints (as of right after the last write).
    const savepoints = readAllSavepoints(storage, last);
    // Should have at most SAVEPOINT_CAP (oldest evicted).
    assert.ok(savepoints.length <= SAVEPOINT_CAP);

    // Most recent should be the last one created.
    const recent = mostRecentSavepoint(savepoints);
    assert.ok(recent?.savedAt.includes('2026-01-01')); // The latest timestamp.
  });

  test('corrupt JSON in savepoint slot is skipped', () => {
    const storage = new MockStorage();

    // Store valid savepoint in slot 0.
    const s = initialState();
    persistSavepoint(storage, createSavepoint(s, []));

    // Corrupt slot 1 with invalid JSON.
    storage.setItem(`${SAVEPOINT_KEY_PREFIX}.1`, 'not valid json{{{');

    // Read all savepoints.
    const savepoints = readAllSavepoints(storage);
    // Slot 1 should be skipped, but slot 0 should load.
    assert.equal(savepoints.length, 1);
    assert.ok(savepoints[0].snapshot);
  });

  test('storage quota error handled gracefully (persistSavepoint)', () => {
    class FailingStorage {
      getItem() {
        return null;
      }
      setItem() {
        throw new Error('QuotaExceededError');
      }
    }
    const storage = new FailingStorage();
    const s = initialState();
    const success = persistSavepoint(storage, createSavepoint(s, emptyJournal()));
    assert.ok(!success, 'persist should return false on error');
  });

  test('private mode (SecurityError) handled gracefully', () => {
    class PrivateModeStorage {
      getItem() {
        return null;
      }
      setItem() {
        throw new Error('SecurityError');
      }
    }
    const storage = new PrivateModeStorage();
    const s = initialState();
    const success = persistSavepoint(storage, createSavepoint(s, []));
    assert.ok(!success, 'private mode error should return false');
  });

  test('END-TO-END KEYSTONE: savepoint + journal tail → reload → restore → identical state', () => {
    // This is the critical proof that the recovery path works end-to-end.
    // FEAT-1972079854: Simulate a crash and recovery scenario.

    const storage = new MockStorage();

    // === PART 1: Live session (before crash) ===
    // Start with initial state and advance with ticks only (to avoid consistency check issues).
    let liveState = initialState();
    let liveJournal = emptyJournal();

    // Actions 0-4: advance state with ticks (consistency-safe)
    const script1 = [
      { type: 'tick' },
      { type: 'tick' },
      { type: 'tick' },
      { type: 'tick' },
      { type: 'tick' },
    ];

    for (const action of script1) {
      liveJournal = recordAction(liveJournal, liveState.tick, action);
      liveState = reducer(liveState, action);
    }

    // Autosave checkpoint #1 at this point (after 5 ticks).
    // sp1 captures: initial state + the 5 actions to replay to reach this point.
    const checkpointIndex = liveJournal.entries.length; // 5 entries
    const sp1State = liveState; // State after script1
    const savepointTail1 = liveJournal.entries.slice(0, checkpointIndex);
    const sp1 = createSavepoint(sp1State, savepointTail1, new Date('2026-08-27T10:00:00Z'));
    persistSavepoint(storage, sp1);

    // === PART 2: More actions AFTER the checkpoint (before crash) ===
    // These actions go into the NEW tail (for next autosave).
    // Use ticks only to keep snapshots consistent.
    const script2 = [
      { type: 'tick' },
      { type: 'tick' },
      { type: 'tick' },
    ];

    for (const action of script2) {
      liveJournal = recordAction(liveJournal, liveState.tick, action);
      liveState = reducer(liveState, action);
    }

    // Record the live state at "crash time".
    const crashState = liveState;
    const crashJournal = liveJournal;

    // === PART 3: Simulate crash + reload ===
    // New session starts with fresh module state (empty journal, fresh initialState).
    // The savepoint and journal tail are in localStorage.

    // Persist the crash-time journal to localStorage (simulating app persistence).
    persistJournal(storage, crashJournal);

    // Persist a SECOND savepoint with the post-checkpoint tail.
    // sp2.snapshot is the state from sp1 (end of checkpoint)
    // sp2.tail is the actions taken after sp1
    const savepointTail2 = crashJournal.entries.slice(checkpointIndex); // Actions 5-7 (post-checkpoint)
    const sp2 = createSavepoint(sp1State, savepointTail2, new Date('2026-08-27T10:00:30Z'));
    persistSavepoint(storage, sp2);

    // === PART 4: Recovery (what happens on boot after crash) ===
    // Restore from the most recent savepoint.
    const restoreResult = restoreFromSavepoint(storage);
    assert.ok(restoreResult.success, `restore should succeed, reason: ${restoreResult.reason}`);

    const recoveredState = restoreResult.state;
    const replayed = restoreResult.replayed;

    // === PART 5: Assertion — recovered state matches crash state ===
    // The recovered state should equal crashState because:
    // - sp2's snapshot is the state after script1 (checkpointIndex=5)
    // - sp2's tail contains the 3 actions from script2 (entries 5-7)
    // - Replay applies sp2.tail onto sp2.snapshot → recovers to crashState
    assert.equal(replayed, 3, '3 actions in sp2 tail were replayed');
    assert.deepEqual(recoveredState, crashState, 'recovered state matches crash state (KEYSTONE)');

    // === PART 6: RED test — mutation proving coverage ===
    // If we drop one tail action from sp2, the deep-equal should fail.
    const sp2Broken = createSavepoint(sp2.snapshot, savepointTail2.slice(1)); // Drop first tail action
    const storage2 = new MockStorage();
    persistSavepoint(storage2, sp2Broken);
    const restoreResult2 = restoreFromSavepoint(storage2);
    assert.ok(restoreResult2.success);

    // Replay only 2 actions instead of 3 → state differs.
    assert.notDeepEqual(
      restoreResult2.state,
      crashState,
      'RED: dropping one tail action breaks determinism (proves tail matters)'
    );
  });
});

// ===== BUG-603: stale lastFlows must not reject a savepoint on restore =====
//
// Repro (integration soak, deterministic): a save whose journal tail — or
// whose baked-in snapshot — ends on a NON-TICK discretionary action (a
// policy toggle, a tax-rate change, a build) is REJECTED on load, because
// runConsistencyChecks' flows-vs-recompute layer (flows.upkeep-total-matches
// / flows.business-tax-matches / flows.wages-matches) compares LIVE-
// recomputed flows (reflecting the post-action state) against the STORED
// lastFlows (which only refreshes on 'tick'). Aaron ruling Q100079=A: on
// restore, RECOMPUTE before the consistency gate — never weaken the check,
// make it true by recomputing.
//
// TIGHTENED after an independent REJECT round: the first version of this fix
// back-derived fundsAtTickStart from the recomputed flow sums to keep the
// funds-vs-flows conservation check "true", which made conservation a
// tautology — a hand-tampered fundsAtTickEnd was silently laundered through
// the retry. The corrected split (replay.ts's
// checkConsistencyRecoveringStaleFlows / recomputeFlowsOverride,
// consistency.ts's runConsistencyChecks actualFlowsOverride param):
// conservation ALWAYS reads the real, stored lastFlows + funds triplet
// (never legitimately goes stale from a post-tick action, so it never needs
// recovering); ONLY the four per-line policy-sensitive checks (Council Tax /
// Business Tax / Wages / Upkeep) retry against a fresh recompute. Nothing is
// written back into the restored state — the stored lastFlows stays the
// historical record it always was.

describe('BUG-603: restore recovers from a stale-lastFlows false-positive rejection', () => {
  class MockStorage {
    constructor() {
      this.data = {};
    }
    getItem(key) {
      return this.data[key] || null;
    }
    setItem(key, value) {
      this.data[key] = value;
    }
    removeItem(key) {
      delete this.data[key];
    }
  }

  /**
   * A state with real population/upkeep/tax so toggling a policy or tax rate
   * actually changes the recomputed flows (an all-zero city would toggle
   * austerity against 0-value flows and the check would never diverge —
   * proving nothing). Roads + a residential + a commercial building, ticked
   * forward until population/taxes are flowing.
   */
  function buildPopulatedState() {
    let s = initialState();
    // Deterministic debug funds + full catalogue unlock (same idiom as
    // gamesave-roundtrip-fidelity.test.mjs's buildRichState) so the specs
    // below are affordable/placeable regardless of the fresh city's starting
    // level/treasury — not the gameplay path under test, just a way to get a
    // populated, flow-bearing city.
    s = reducer(s, { type: 'debugFunds', amount: 500_000_000 });
    s = reducer(s, { type: 'unlockAll' });
    const roadTiles = [];
    for (let x = 0; x <= 20; x++) roadTiles.push({ x, y: 10 });
    roadTiles.push({ x: 10, y: 11 });
    roadTiles.push({ x: 15, y: 11 });
    s = reducer(s, { type: 'placeRoadPath', spec: 'road', tiles: roadTiles });
    s = reducer(s, { type: 'place', spec: 'res_estate', x: 10, y: 12 });
    s = reducer(s, { type: 'place', spec: 'com_shop', x: 15, y: 12 });
    for (let i = 0; i < 100; i++) s = reducer(s, { type: 'tick' });
    // Sanity: prove the fixture actually exercises real flows before relying
    // on it to reproduce the bug.
    assert.ok(s.population > 0, 'expected population to have grown');
    assert.ok(s.lastFlows.outflows.some((f) => f.value > 0), 'expected non-zero outflows');
    return s;
  }

  test('1. soak repro: tick -> policy toggle (austerity) in the TAIL -> savepoint -> restore SUCCEEDS', () => {
    const storage = new MockStorage();
    const snapshotState = buildPopulatedState();

    // Tail: one more tick, then a discretionary policy toggle — no tick
    // after it, so lastFlows is stale relative to the toggled policies by
    // the time the savepoint is taken.
    let tailState = reducer(snapshotState, { type: 'tick' });
    tailState = reducer(tailState, { type: 'policy', id: 'austerity' });
    assert.equal(tailState.policies.austerity, true, 'expected austerity turned on');

    const tail = [
      { tick: snapshotState.tick, action: { type: 'tick' } },
      { tick: tailState.tick, action: { type: 'policy', id: 'austerity' } },
    ];

    // Prove this WOULD have failed pre-fix: the post-replay state's live
    // recompute (austerity ON) diverges from its own stored lastFlows
    // (austerity OFF, from the 'tick' before the toggle).
    const rawReport = runConsistencyChecks(tailState);
    assert.ok(rawReport.failures > 0, 'expected the raw (unrecovered) state to fail consistency');

    const savepoint = createSavepoint(snapshotState, tail, new Date('2026-09-03T00:00:00Z'));
    persistSavepoint(storage, savepoint);

    const result = restoreFromSavepoint(storage);
    assert.ok(result.success, `restore should succeed, reason: ${result.reason}`);
    assert.equal(result.replayed, 2);
    assert.equal(result.state.policies.austerity, true);
    // NOTE: nothing is written back into result.state (see the ruling above),
    // so a bare runConsistencyChecks(result.state) call — with no override —
    // is EXPECTED to still report the same per-line staleness until the next
    // real tick refreshes lastFlows; that is not a regression, it is the
    // documented trade-off. The only thing that must hold is that
    // restoreFromSavepoint itself (which runs the split retry) succeeds,
    // asserted above.
  });

  test('2. autosave-after-action shape: the toggle is BAKED INTO the snapshot itself (empty tail) -> restore SUCCEEDS', () => {
    const storage = new MockStorage();
    let s = buildPopulatedState();
    // The toggle happens, then autosave fires immediately — no further tick,
    // no journal tail at all. The SNAPSHOT itself carries stale lastFlows.
    s = reducer(s, { type: 'policy', id: 'austerity' });

    const rawReport = runConsistencyChecks(s);
    assert.ok(rawReport.failures > 0, 'expected the raw baked-in-snapshot state to fail consistency');

    const savepoint = createSavepoint(s, [], new Date('2026-09-03T00:01:00Z'));
    persistSavepoint(storage, savepoint);

    const result = restoreFromSavepoint(storage);
    assert.ok(result.success, `restore should succeed, reason: ${result.reason}`);
    assert.equal(result.replayed, 0);
    assert.equal(result.state.policies.austerity, true);
  });

  test('3. tax + place as the tail action -> restore SUCCEEDS (same class as the policy toggle)', () => {
    const storage = new MockStorage();
    const snapshotState = buildPopulatedState();

    let tailState = reducer(snapshotState, { type: 'tick' });
    tailState = reducer(tailState, { type: 'tax', which: 'commercial', rate: 25 });
    tailState = reducer(tailState, { type: 'place', spec: 'res_estate', x: 20, y: 12 });

    const tail = [
      { tick: snapshotState.tick, action: { type: 'tick' } },
      { tick: tailState.tick, action: { type: 'tax', which: 'commercial', rate: 25 } },
      { tick: tailState.tick, action: { type: 'place', spec: 'res_estate', x: 20, y: 12 } },
    ];

    const rawReport = runConsistencyChecks(tailState);
    assert.ok(rawReport.failures > 0, 'expected the raw (unrecovered) state to fail consistency');

    const savepoint = createSavepoint(snapshotState, tail, new Date('2026-09-03T00:02:00Z'));
    persistSavepoint(storage, savepoint);

    const result = restoreFromSavepoint(storage);
    assert.ok(result.success, `restore should succeed, reason: ${result.reason}`);
    assert.equal(result.replayed, 3);
    assert.equal(result.state.taxRates.commercial, 25);
  });

  test('4. RED-PROOF: a GENUINELY corrupt save (beyond policy staleness) is STILL rejected', () => {
    const storage = new MockStorage();
    const s = buildPopulatedState();

    // Corrupt something the flows recompute cannot ever paper over: an
    // out-of-range tax rate (shape-validation layer, taxRates.commercial,
    // consistency.ts) — independent of the flows-vs-recompute layer this fix
    // touches, so recomputing lastFlows must not make this pass.
    const corrupted = { ...s, taxRates: { ...s.taxRates, commercial: 250 } };
    assert.ok(
      runConsistencyChecks(corrupted).failures > 0,
      'sanity: the hand-broken state must fail consistency raw'
    );

    const savepoint = createSavepoint(corrupted, [], new Date('2026-09-03T00:03:00Z'));
    persistSavepoint(storage, savepoint);

    const result = restoreFromSavepoint(storage);
    assert.ok(!result.success, 'a genuinely corrupt save must still be rejected');
    assert.match(result.reason, /Snapshot failed consistency/);
  });

  test('4b. RED-PROOF: duplicate building ids (beyond policy staleness) is STILL rejected', () => {
    const storage = new MockStorage();
    const s = buildPopulatedState();
    assert.ok(s.buildings.length >= 2, 'need at least 2 buildings to duplicate an id');

    // Corrupt a second building's id to collide with the first — the
    // buildings.ids-unique shape check, wholly unrelated to lastFlows.
    const corruptedBuildings = s.buildings.map((b, i) => (i === 1 ? { ...b, id: s.buildings[0].id } : b));
    const corrupted = { ...s, buildings: corruptedBuildings };
    assert.ok(
      runConsistencyChecks(corrupted).failures > 0,
      'sanity: the hand-broken state must fail consistency raw'
    );

    const savepoint = createSavepoint(corrupted, [], new Date('2026-09-03T00:04:00Z'));
    persistSavepoint(storage, savepoint);

    const result = restoreFromSavepoint(storage);
    assert.ok(!result.success, 'a genuinely corrupt save must still be rejected');
  });

  // ===== TAMPER REGRESSIONS (independent REJECT round finding) =====
  //
  // The FIRST version of this fix back-derived fundsAtTickStart from the
  // recomputed flow sums so the conservation check would always come out
  // "true" — which made it a tautology: a hand-tampered fundsAtTickEnd was
  // silently ACCEPTED through the exact same retry path that legitimately
  // recovers a stale-policy save. These two cases are the attacker's
  // reproduction (+1,000,000 and -500,000) and must PERMANENTLY reject,
  // specifically while the retry path is genuinely engaged (a real,
  // legitimate policy-staleness condition is present too) — proving
  // conservation is checked against the REAL stored funds triplet on every
  // attempt, retry included, never against a value derived from the
  // recomputed override.

  function tamperFundsRejectionCase(delta) {
    const storage = new MockStorage();
    let s = buildPopulatedState();
    // Legitimate staleness (same shape as test 2) — this alone WOULD restore
    // successfully (proven by test 2 above), so the retry path is genuinely
    // engaged here, not skipped because the raw check already passed.
    s = reducer(s, { type: 'policy', id: 'austerity' });

    const tampered = { ...s, fundsAtTickEnd: s.fundsAtTickEnd + delta };
    const savepoint = createSavepoint(tampered, [], new Date('2026-09-03T00:09:00Z'));
    persistSavepoint(storage, savepoint);

    const result = restoreFromSavepoint(storage);
    assert.ok(
      !result.success,
      `a fundsAtTickEnd tampered by ${delta} must be rejected even under a legitimate stale-flows retry`
    );
    assert.match(result.reason, /Snapshot failed consistency/);
  }

  test('6. TAMPER REGRESSION: fundsAtTickEnd +1,000,000 is REJECTED through the retry path', () => {
    tamperFundsRejectionCase(1_000_000);
  });

  test('7. TAMPER REGRESSION: fundsAtTickEnd -500,000 is REJECTED through the retry path', () => {
    tamperFundsRejectionCase(-500_000);
  });

  test('5. determinism: restore -> save -> restore is byte-stable', () => {
    const storage = new MockStorage();
    const snapshotState = buildPopulatedState();
    let tailState = reducer(snapshotState, { type: 'tick' });
    tailState = reducer(tailState, { type: 'policy', id: 'austerity' });
    const tail = [
      { tick: snapshotState.tick, action: { type: 'tick' } },
      { tick: tailState.tick, action: { type: 'policy', id: 'austerity' } },
    ];
    persistSavepoint(storage, createSavepoint(snapshotState, tail, new Date('2026-09-03T00:05:00Z')));

    const first = restoreFromSavepoint(storage);
    assert.ok(first.success, `first restore should succeed, reason: ${first.reason}`);

    // Re-save the restored state (empty tail — it is already fully replayed)
    // and restore again; the two restored states must be identical.
    const storage2 = new MockStorage();
    persistSavepoint(storage2, createSavepoint(first.state, [], new Date('2026-09-03T00:06:00Z')));
    const second = restoreFromSavepoint(storage2);
    assert.ok(second.success, `second restore should succeed, reason: ${second.reason}`);

    assert.deepEqual(second.state, first.state, 'restore -> save -> restore must be byte-stable');
  });

  test('unaffected path: a savepoint whose lastFlows is already fresh restores byte-identical to the snapshot (no regression)', () => {
    const storage = new MockStorage();
    const s1 = buildPopulatedState();
    const savepoint = createSavepoint(s1, [], new Date('2026-09-03T00:07:00Z'));
    persistSavepoint(storage, savepoint);

    const result = restoreFromSavepoint(storage);
    assert.ok(result.success, `restore should succeed, reason: ${result.reason}`);
    // Normalise both sides through the same JSON round-trip the savepoint
    // storage itself uses (encode(JSON.stringify(...)) / JSON.parse(decode(...)))
    // before comparing — matches the precedent in
    // gamesave-roundtrip-fidelity.test.mjs: JSON turns a stray `-0` ledger
    // amount (e.g. a free-zone placement) into `0` on BOTH sides equally, a
    // pre-existing storage-format quirk unrelated to BUG-603, so normalising
    // identically on both sides still catches a genuine field drop/mistype
    // without masking one.
    assert.deepEqual(
      JSON.parse(JSON.stringify(result.state)),
      JSON.parse(JSON.stringify(s1)),
      'a consistent snapshot must restore untouched, byte-identical'
    );
  });
});

// ===== BUG-437 BAR-2: persistJournal quota coverage =====
//
// journal.ts:166-190's real contract: pre-shrink BEFORE any setItem is attempted
// once the serialized payload exceeds JOURNAL_PERSIST_MAX_CHARS, then on a setItem
// throw, halve-and-retry down to a floor of 200 entries, and if even that throws,
// removeItem(JOURNAL_KEY) and return false. Retention always keeps the TAIL (newest
// entries — `entries.slice(-N)`), never the head.

describe('persistJournal: quota / shrink-and-retry contract', () => {
  function bigJournal(count) {
    let j = emptyJournal();
    for (let i = 0; i < count; i++) {
      j = recordAction(j, i, { type: 'debugFunds', amount: i });
    }
    return j;
  }

  test('payload over JOURNAL_PERSIST_MAX_CHARS is pre-shrunk BEFORE any setItem is attempted', () => {
    // 20,000 debugFunds entries serialize well past the 400k char cap.
    const j = bigJournal(20_000);
    const rawSize = JSON.stringify({ entries: j.entries }).length;
    assert.ok(rawSize > JOURNAL_PERSIST_MAX_CHARS, 'fixture must actually exceed the cap to test pre-shrink');

    const attempts = [];
    const storage = {
      setItem(k, v) {
        attempts.push(v);
      },
      removeItem() {},
    };

    const ok = persistJournal(storage, j);
    assert.ok(ok, 'persist should succeed once shrunk under the cap');
    assert.ok(attempts.length >= 1, 'setItem must have been attempted');
    assert.ok(
      attempts[0].length <= JOURNAL_PERSIST_MAX_CHARS,
      `first setItem attempt (${attempts[0].length} chars) must already be pre-shrunk under the ${JOURNAL_PERSIST_MAX_CHARS} cap`,
    );
  });

  test('setItem throwing QuotaExceeded on the first N attempts → halves and retries down to success, keeping the NEWEST entries (tail retention)', () => {
    const ENTRY_COUNT = 800;
    const j = bigJournal(ENTRY_COUNT);
    const THROW_ATTEMPTS = 2;

    let attemptCount = 0;
    const attempts = [];
    const storage = {
      setItem(k, v) {
        attemptCount += 1;
        attempts.push(v);
        if (attemptCount <= THROW_ATTEMPTS) {
          throw new Error('QuotaExceededError: simulated');
        }
      },
      removeItem() {},
    };

    const ok = persistJournal(storage, j);
    assert.ok(ok, 'persist should eventually succeed');
    assert.equal(attemptCount, THROW_ATTEMPTS + 1, 'exactly one more attempt than the number of throws');

    const persisted = JSON.parse(attempts[attempts.length - 1]).entries;
    // 800 → halve → 400 → halve → 200 (floor), matching Math.floor(n/2) each retry.
    assert.equal(persisted.length, 200);

    // TAIL retention: the persisted entries must be the newest (highest tick),
    // not the oldest (a head-slice mutation would fail this).
    const expectedTail = j.entries.slice(-persisted.length);
    assert.equal(persisted[0].action.amount, expectedTail[0].action.amount);
    assert.equal(persisted[persisted.length - 1].action.amount, expectedTail[expectedTail.length - 1].action.amount);
    assert.equal(persisted[persisted.length - 1].action.amount, ENTRY_COUNT - 1, 'the very newest entry must be retained');
    assert.notEqual(persisted[0].action.amount, 0, 'the oldest entries must have been dropped, not kept');
  });

  test('setItem that ALWAYS throws → persistJournal removes the key and returns false', () => {
    // Small journal (<=200 entries): the shrink loop's floor check short-circuits
    // immediately (entries.length <= 200), so this proves the terminal give-up path,
    // not just another halving round.
    const j = bigJournal(10);
    let removedKey = null;
    let setItemCalls = 0;
    const storage = {
      setItem() {
        setItemCalls += 1;
        throw new Error('QuotaExceededError: always');
      },
      removeItem(k) {
        removedKey = k;
      },
    };

    const ok = persistJournal(storage, j);
    assert.equal(ok, false, 'persistJournal must report failure when storage can never accept the write');
    assert.ok(setItemCalls >= 1, 'setItem must have been attempted at least once');
    assert.equal(removedKey, JOURNAL_KEY, 'the stale/partial journal key must be removed on terminal failure');
  });
});

// ===== CONSISTENCY CHECKING IN REPLAY =====

describe('consistency: before and after replay', () => {
  test('savepoint snapshot is consistent before replay', () => {
    const s = initialState();
    const report = runConsistencyChecks(s);
    assert.equal(report.failures, 0, 'initial state should be consistent');
  });

  test('replay maintains consistency across ticks', () => {
    const s0 = initialState();
    let state = s0;
    for (let i = 0; i < 10; i++) {
      state = reducer(state, { type: 'tick' });
    }
    const report = runConsistencyChecks(state);
    assert.equal(report.failures, 0, 'after 10 ticks, state should be consistent');
  });

  test('complex sequence is deterministic (place + tax + ticks)', () => {
    const s0 = initialState();
    const actions = [
      { type: 'place', spec: 'res_hut', x: 5, y: 5 },
      { type: 'tick' },
      { type: 'place', spec: 'm20', x: 10, y: 10 },
      { type: 'tick' },
      { type: 'tick' },
      { type: 'tax', which: 'residential', rate: 12 },
      { type: 'tick' },
    ];

    // Build journal on one path.
    let j = emptyJournal();
    let state1 = s0;
    for (const action of actions) {
      j = recordAction(j, state1.tick, action);
      state1 = reducer(state1, action);
    }

    // Replay on fresh state.
    const s0Fresh = initialState();
    let state2 = s0Fresh;
    for (const entry of j.entries) {
      state2 = reducer(state2, entry.action);
    }

    // Determinism proof.
    assert.deepEqual(state1, state2, 'complex action sequence is deterministic');
  });
});
