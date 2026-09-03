// attack-bug606-replay.test.mjs — PROMOTED from webconsole/attack/
// atk-replay.test.mjs (independent round r2, Aaron 2026-09-03: "promote the
// attacker's regressions into test/ ... so CI carries them"). Extension kept
// as .mjs — this file's imports (engine.ts/journal.ts/genesisReplay.ts) are
// all explicit-extension, no chain through demandFixUi.ts's extensionless
// internal imports, so plain `node --test` (tools/test/scoped.mjs's node
// group) resolves it fine, confirmed by direct invocation before promotion.
//
// ATTACK (BUG-606 independent round) — resolveDemandAll REPLAY DETERMINISM.
// The author's "journaled and replays identically" test never calls a replay
// function at all; this one does. Content through the marked line below is
// UNCHANGED from the original attack file (the independent round's own
// regressions, kept verbatim); the CAPPED-INVOCATION test after that line is
// NEW (this session, r2 follow-up item 1: "ADD a replay test with a capped
// invocation to prove [replay identity is preserved]").
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import { emptyJournal, recordAction, isStateAffecting } from '../src/sim/journal.ts';
import { replayFromGenesis, replayIsDeterministic, stableStringify, replayFromGenesisDefensive } from '../src/sim/genesisReplay.ts';
import { orderedDemandFixPlan, RESOLVE_DEMAND_ALL_MAX_UNITS } from '../src/sim/data.ts';

function driveAndRecord(actions) {
  let journal = emptyJournal();
  let state = initialState();
  for (const action of actions) {
    if (isStateAffecting(action)) journal = recordAction(journal, state.tick, action);
    state = reducer(state, action);
  }
  return { journal, liveState: state };
}

const ticks = (n) => Array.from({ length: n }, () => ({ type: 'tick' }));

// A REAL session reachable purely through journaled actions: grow a city, cap
// the treasury low so Fix All must partially place, then Fix All (twice).
function script({ funds, twice }) {
  return [
    { type: 'debugFunds', amount: 5_000_000 },
    { type: 'unlockAll' },
    { type: 'place', spec: 'res_hut', x: 5, y: 5 },
    { type: 'place', spec: 'res_hut', x: 7, y: 5 },
    { type: 'place', spec: 'res_hut', x: 9, y: 5 },
    ...ticks(40),
    { type: 'place', spec: 'res_hut', x: 11, y: 5 },
    ...ticks(40),
    // Drive the treasury down to a small, deterministic figure so Fix All is
    // forced into a PARTIAL placement.
    { type: 'debugFunds', amount: -5_000_000 },
    { type: 'debugFunds', amount: funds },
    { type: 'resolveDemandAll' },
    ...(twice ? [{ type: 'resolveDemandAll' }] : []),
    ...ticks(5),
  ];
}

for (const funds of [0, 1, 5_000, 60_000, 500_000, 50_000_000]) {
  test(`ATTACK replay: live vs genesis-replay byte-identical with resolveDemandAll at funds=${funds}`, () => {
    const { journal, liveState } = driveAndRecord(script({ funds, twice: false }));
    assert.ok(
      journal.entries.some((e) => e.action.type === 'resolveDemandAll'),
      'precondition: resolveDemandAll must be journaled'
    );
    const replayed = replayFromGenesis(journal);
    assert.equal(
      stableStringify({ ...replayed, roadConnectivity: null }),
      stableStringify({ ...liveState, roadConnectivity: null }),
      `live vs replay divergence at funds=${funds}`
    );
    assert.ok(replayIsDeterministic(journal), 'BUG-504 class: same journal replayed twice must be byte-identical');
    assert.ok(liveState.funds >= 0, 'funds must never go negative');
  });
}

test('ATTACK replay: resolveDemandAll twice in a row replays byte-identically', () => {
  const { journal, liveState } = driveAndRecord(script({ funds: 200_000, twice: true }));
  const seen = journal.entries.filter((e) => e.action.type === 'resolveDemandAll').length;
  assert.equal(seen, 2, 'precondition: two consecutive resolveDemandAll entries');
  const replayed = replayFromGenesis(journal);
  assert.equal(
    stableStringify({ ...replayed, roadConnectivity: null }),
    stableStringify({ ...liveState, roadConnectivity: null })
  );
  assert.ok(replayIsDeterministic(journal));
});

test('ATTACK replay: defensive replayer never SKIPS a resolveDemandAll entry', () => {
  const { journal } = driveAndRecord(script({ funds: 100_000, twice: true }));
  const res = replayFromGenesisDefensive(journal);
  assert.equal(res.crashed, false);
  assert.deepEqual(res.skipped, [], `defensive replay skipped actions: ${JSON.stringify(res.skipped)}`);
});

test('ATTACK: isStateAffecting classifies resolveDemandAll as journaled', () => {
  assert.equal(isStateAffecting({ type: 'resolveDemandAll' }), true);
});

// ---------------------------------------------------------------------------
// NEW (this session, r2 follow-up item 1) — a CAPPED resolveDemandAll must
// still replay byte-identically. RESOLVE_DEMAND_ALL_MAX_UNITS is a fixed
// constant (no clock/RNG), so a capped batch is exactly as deterministic as
// an uncapped one — this proves it with a REAL journal big enough to force
// the cap to bind (unlike the small `script()` scenarios above, which never
// plan more than a few units and so never exercise the cap at all).
// ---------------------------------------------------------------------------

/** Grows a real, journal-reachable city big enough that a SINGLE
 *  resolveDemandAll batch plans well over RESOLVE_DEMAND_ALL_MAX_UNITS units
 *  — one 'placeMany' of 50 high-capacity res_estate blocks (one journaled
 *  action) followed by enough ticks for population to actually fill the new
 *  capacity (residentsCapacity only grows the BUILT capacity; population
 *  fills it in gradually via the real growth simulation, not instantly). */
function capTriggerScript() {
  const tiles = [];
  let x = 5;
  let y = 5;
  for (let i = 0; i < 50; i++) {
    tiles.push({ x, y });
    x += 3;
    if (x > 300) {
      x = 5;
      y += 3;
    }
  }
  return [
    { type: 'debugFunds', amount: 5_000_000_000 },
    { type: 'unlockAll' },
    { type: 'placeMany', spec: 'res_estate', tiles },
    ...ticks(200),
    { type: 'debugFunds', amount: -5_000_000_000 },
    { type: 'debugFunds', amount: 1_000_000_000_000 },
    { type: 'resolveDemandAll' },
    ...ticks(5),
  ];
}

test('ATTACK replay (NEW, r2 cap): a CAPPED resolveDemandAll (more units planned than RESOLVE_DEMAND_ALL_MAX_UNITS) still replays byte-identically', () => {
  const genesis = initialState();
  const preState = capTriggerScript()
    .slice(0, -1 - 5) // drop the trailing resolveDemandAll + its 5 ticks
    .reduce((s, a) => reducer(s, a), genesis);
  const order = orderedDemandFixPlan(preState);
  const totalPlanned = order.reduce((sum, item) => sum + item.count, 0);
  assert.ok(
    totalPlanned > RESOLVE_DEMAND_ALL_MAX_UNITS,
    `precondition: this scenario must genuinely need MORE than the cap (${totalPlanned} planned vs cap ${RESOLVE_DEMAND_ALL_MAX_UNITS}) or the cap is never exercised`
  );

  const { journal, liveState } = driveAndRecord(capTriggerScript());
  assert.ok(
    journal.entries.some((e) => e.action.type === 'resolveDemandAll'),
    'precondition: resolveDemandAll must be journaled'
  );
  assert.ok(
    /click Fix All again for the rest/.test(liveState.placeNotice ?? ''),
    `precondition: this run must actually HIT the cap (capped notice), got: ${liveState.placeNotice}`
  );

  const replayed = replayFromGenesis(journal);
  assert.equal(
    stableStringify({ ...replayed, roadConnectivity: null }),
    stableStringify({ ...liveState, roadConnectivity: null }),
    'a CAPPED resolveDemandAll must replay byte-identically from genesis — the cap is a fixed constant, never a clock/elapsed-time bound (GR#21)'
  );
  assert.ok(replayIsDeterministic(journal), 'same journal replayed twice must ALSO be byte-identical when the cap binds');
  assert.ok(liveState.funds >= 0, 'funds must never go negative');
});
