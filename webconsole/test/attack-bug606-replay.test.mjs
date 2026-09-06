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
 *  — one 'placeMany' of high-capacity blocks (one journaled action) followed
 *  by enough ticks for population to actually fill the new capacity
 *  (residentsCapacity only grows the BUILT capacity; population fills it in
 *  gradually via the real growth simulation, not instantly).
 *  BUG-646 (cap 250 -> 2000, Aaron 2026-09-03): scaled from the original 50
 *  blocks/200 ticks (which planned 353 units, no longer enough to exceed the
 *  new 2000 cap) up to 800 blocks/250 ticks (measured 2,255 planned units at
 *  the time).
 *
 *  BUG-477 fixture-side follow-up (round rejection, 2026-09-05): the
 *  wind/windfarm repricing this estate lands (pow_wind/pow_windfarm raised
 *  toward the realistic capex anchor) shifted orderedDemandFixPlan's overall
 *  mix enough that the FIXED 800-block fixture only plans 1,731 units under
 *  the new prices — UNDER the 2000 cap, silently defeating this test's whole
 *  point (never asserted as flaky; it just stopped exercising the cap).
 *  BLOCK_COUNT is now DERIVED from RESOLVE_DEMAND_ALL_MAX_UNITS itself (GR#15
 *  — no bare re-guessed literal) via a calibration ratio measured once
 *  against the CURRENT catalogue (800 blocks / 250 ticks -> 1,731 planned
 *  units under the post-BUG-477 wind prices), scaled up by TARGET_MARGIN so
 *  the scenario stays comfortably over whatever the cap is even after
 *  ordinary future balance/price tuning, instead of sitting right at the
 *  edge the way the original fixed 800 did. If the catalogue moves far
 *  enough that this calibration itself goes stale, the precondition assert
 *  below will say so explicitly (not a silent pass).
 *
 *  BUG-394 follow-up (2026-09-06): 250 ticks stopped being long enough to
 *  calibrate from. BUG-394 made organic growth job-driven and rate-capped
 *  (grossInflow proportional to CURRENT population, not the vacant capacity
 *  on offer — the old model's growth was unbounded, ~800x/yr measured, so
 *  250 ticks from empty land used to blow well past any capacity ceiling).
 *  A near-empty city's growth is now dominated by the "progress guarantee"
 *  floor (>= 0.1% of capacity per tick while vacancy stays above 20%), which
 *  fills roughly the same ~80-90% of whatever capacity exists after a FIXED
 *  ~800-900 ticks regardless of how large that capacity is (the fill rate is
 *  itself proportional to capacity, so the two scale out) — so reaching the
 *  same population now genuinely requires ~900 ticks of real reducer work,
 *  not 250.
 *
 *  FIRST ATTEMPT (measured live, then abandoned — kept here as the record of
 *  why): keeping res_estate (1,500 residents/building) and raising
 *  CALIBRATION_TICKS to 900 does restore a working blocks->units
 *  relationship (1,600 res_estate blocks/900 ticks plans 2,180 units,
 *  population ~1.85M), but the reducer's own per-tick cost scales with
 *  BUILDING COUNT, and this whole fixture pays for that cost SIX-TO-SEVEN
 *  times over (the calibration probe, the doubling-loop's guess probe, this
 *  test's own precondition slice, driveAndRecord's live run, one
 *  replayFromGenesis, and replayIsDeterministic's own INTERNAL two more
 *  replayFromGenesis calls) — 1,600 buildings x 900 ticks measured ~90s per
 *  run standalone, so the full test exceeded the 15-minute AARON WATCHDOG
 *  ceiling (tools/test/scoped.mjs) outright; this is the finding this note
 *  exists to record, not a hang.
 *
 *  THE FIX: swap the fixture's building from res_estate (1,500
 *  residents/5x5) to res_tower_sgp ('Singapore-style Mega-Estate', 20,000
 *  residents/9x9 — src/sim/data.ts's biggest residential spec). Population
 *  growth is driven by total CAPACITY (residents-per-building x count), so
 *  the same target capacity is reachable with ~13x FEWER buildings, and
 *  since reducer per-tick cost scales with building count (not total
 *  capacity), this cuts the dominant cost by the same ~13x — measured live,
 *  160 res_tower_sgp blocks/900 ticks reaches population ~2.1M and plans
 *  2,764 units (38% clear of the 2,000 cap) in ~6.5s standalone, against
 *  1,600 res_estate blocks/900 ticks' ~90s for a WORSE (2,180) result. The
 *  full test file (all 6-7 replays of this fixture plus the six funds-sweep
 *  cases) now completes in well under a minute. GR#21: res_tower_sgp is
 *  still placed via the same real journaled 'placeMany' action and grown by
 *  the same real reducer/tick loop — nothing about determinism or the
 *  replay path changes, only which catalogue spec supplies the capacity. */
const CAP_FIXTURE_CALIBRATION_BLOCKS = 160;
const CAP_FIXTURE_CALIBRATION_TICKS = 900;
const CAP_FIXTURE_SPEC = 'res_tower_sgp';
const CAP_FIXTURE_TILE_STEP = 12; // res_tower_sgp is a 9x9 footprint; 12 leaves clearance
const CAP_FIXTURE_ROW_WRAP_X = 600; // MAP_W is 624 (src/sim/grid.ts) — stay clear of the edge
const CAP_FIXTURE_TARGET_MARGIN = 1.3; // aim ~30% clear of the cap, not right at its edge
const CAP_FIXTURE_MAX_DOUBLINGS = 4;

/* The planned-unit yield per residential block is a property of the live
 * catalogue and the demand planner (a pinned 1731 went stale the moment the
 * civic-tier family split changed what largest-first chooses), so it is
 * MEASURED here at the calibration size and scaled, then re-measured and
 * doubled (bounded) until the plan genuinely clears the cap. GR#15: the
 * fixture derives from the runtime, never from a remembered number. */
function plannedUnitsFor(blockCount) {
  const genesis = initialState();
  const pre = capTriggerScriptFor(blockCount)
    .slice(0, -1 - 5)
    .reduce((s, a) => reducer(s, a), genesis);
  return orderedDemandFixPlan(pre).reduce((sum, item) => sum + item.count, 0);
}

let capFixtureBlockCountMemo = null;
function capFixtureBlockCount() {
  if (capFixtureBlockCountMemo !== null) return capFixtureBlockCountMemo;
  const calibrationUnits = plannedUnitsFor(CAP_FIXTURE_CALIBRATION_BLOCKS);
  assert.ok(calibrationUnits > 0, `calibration run planned ${calibrationUnits} units — the demand planner produced nothing to scale from`);
  let blocks = Math.ceil(
    (CAP_FIXTURE_CALIBRATION_BLOCKS * RESOLVE_DEMAND_ALL_MAX_UNITS * CAP_FIXTURE_TARGET_MARGIN) / calibrationUnits
  );
  for (let i = 0; i < CAP_FIXTURE_MAX_DOUBLINGS; i++) {
    if (plannedUnitsFor(blocks) > RESOLVE_DEMAND_ALL_MAX_UNITS) break;
    blocks *= 2;
  }
  capFixtureBlockCountMemo = blocks;
  return blocks;
}

function capTriggerScript() {
  return capTriggerScriptFor(capFixtureBlockCount());
}

function capTriggerScriptFor(blockCount) {
  const tiles = [];
  let x = 5;
  let y = 5;
  for (let i = 0; i < blockCount; i++) {
    tiles.push({ x, y });
    x += CAP_FIXTURE_TILE_STEP;
    if (x > CAP_FIXTURE_ROW_WRAP_X) {
      x = 5;
      y += CAP_FIXTURE_TILE_STEP;
    }
  }
  return [
    { type: 'debugFunds', amount: 100_000_000_000 },
    { type: 'unlockAll' },
    { type: 'placeMany', spec: CAP_FIXTURE_SPEC, tiles },
    ...ticks(CAP_FIXTURE_CALIBRATION_TICKS),
    { type: 'debugFunds', amount: -100_000_000_000 },
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
