// genesis-divergence-bug631.test.mjs — BUG-631 regression: the genesisReplay
// stale-premise sibling of the BUG-617 tail-path divergence.
//
// BUG-460 FIX A wrapped replayFromGenesis / replayFromGenesisDefensive /
// replayFromGenesisDefensiveChunked in setReplayMode(true) on the premise
// that "nothing reads state.roadConnectivity BETWEEN actions during a
// replay". BUG-606 falsified that premise for the SAVEPOINT-TAIL path
// (replay.ts's replayTailChunked, fixed by the BUG-617 independent round's
// A2 finding) by adding the resolveDemand / resolveDemandAll reducer cases,
// which derive their build plan through demandFixPlan -> serviceCoverageOf
// -> isOnline -> the G2/G3 road gates, reading state.roadConnectivity
// DIRECTLY, INSIDE the reducer, between actions. genesisReplay.ts's THREE
// genesis-replay entry points shared the exact same stale premise
// (BUG-631) — this file proves the divergence and pins the fix (dropping
// setReplayMode entirely from genesisReplay.ts, mirroring replay.ts's F1).
//
// A [place road][resolveDemand gp] journal is the ordinary "finish the
// road, then click Fix" player sequence: it must replay byte-identically
// whether run through a plain action-by-action reducer loop or through
// genesisReplay's chunked generator.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import { emptyJournal, recordAction } from '../src/sim/journal.ts';
import { computeRoadConnectivity, MAP_H, isOnline, serviceCoverageOf } from '../src/sim/data.ts';
import {
  replayFromGenesis,
  replayFromGenesisDefensiveChunked,
  stableStringify,
} from '../src/sim/genesisReplay.ts';

const COL = 200;
const UNLOCK_COST_HEADROOM = 900_000_000_000;

/**
 * A genesis-reachable action script that ends with a health clinic sitting
 * beside an isolated road stub one tile short of the map edge — road
 * ADJACENT but not road CONNECTED (mirrors attack-bug617-round.test.mjs's
 * craftDisconnectedClinicCity, but built entirely from journalled actions
 * rather than a hand-crafted snapshot, since genesis replay always starts
 * at initialState()). A generous 250-tick run after placement clears
 * construction (hea_clinic's cost is far below the threshold that would
 * need more than a handful of ticks) so isOnline's G1 gate is satisfied by
 * the time the road-completion action fires.
 */
function buildDisconnectedClinicScript() {
  const actions = [
    { type: 'debugFunds', amount: UNLOCK_COST_HEADROOM },
    { type: 'unlockAll' },
    { type: 'place', spec: 'road', x: COL, y: MAP_H - 2 },
    { type: 'place', spec: 'hea_clinic', x: COL, y: MAP_H - 3 },
    // A real GP shortfall is required for `resolveDemand` to attempt a
    // build at all (demandFixPlan skips any row with need <= 0) — a single
    // res_tower_sgp (≈20,000 residents at capacity) run through enough
    // ticks to fill gives a large, real GP demand deterministically, far
    // faster than growing population organically from res_hut zoning.
    { type: 'place', spec: 'road', x: 10, y: 10 },
    { type: 'place', spec: 'res_tower_sgp', x: 12, y: 10 },
  ];
  for (let i = 0; i < 600; i++) actions.push({ type: 'tick' });
  return actions;
}

/** The player's real, journalled two-action sequence: finish the road to the
 * map edge (which brings the clinic online), then click Fix (GP) — the
 * scenario the BUG-617 round's A2 pinned on the tail path. */
const CONNECT_THEN_FIX = [
  { type: 'place', spec: 'road', x: COL, y: MAP_H - 1 },
  { type: 'resolveDemand', serviceKey: 'gp' },
];

/** Record a Journal exactly as the store does: recordAction against the
 * live state's CURRENT tick, then advance the live state through the pure
 * reducer — so the journal's `tick` stamps match a real play session. */
function driveAndRecord(actions) {
  let journal = emptyJournal();
  let state = initialState();
  for (const action of actions) {
    journal = recordAction(journal, state.tick, action);
    state = reducer(state, action);
  }
  return { journal, liveState: state };
}

function chunkedGenesis(journal) {
  const gen = replayFromGenesisDefensiveChunked(journal);
  let n;
  do {
    n = gen.next();
  } while (!n.done);
  return n.value;
}

describe('BUG-631: genesis replay vs the road-connectivity stale-premise divergence', () => {
  test('setup sanity: the scripted clinic really is gated off by road connectivity before the connecting placement', () => {
    const { liveState } = driveAndRecord(buildDisconnectedClinicScript());
    const clinic = liveState.buildings.find((b) => b.spec === 'hea_clinic');
    assert.ok(clinic, 'setup must have placed the clinic');
    assert.equal(isOnline(liveState, clinic), false, 'clinic must start OFFLINE (road stub not connected to the edge)');
    assert.equal(
      serviceCoverageOf(liveState).find((c) => c.id === 'gp')?.cap ?? 0,
      0,
      'offline clinic must contribute no GP capacity'
    );

    const connected = reducer(liveState, CONNECT_THEN_FIX[0]);
    const clinic2 = connected.buildings.find((b) => b.id === clinic.id);
    assert.equal(isOnline(connected, clinic2), true, 'clinic must be ONLINE once the road reaches the edge');
  });

  test('a genesis journal containing [place road][resolveDemand] replays byte-identical to a fresh action-by-action run (chunked)', () => {
    const fullScript = [...buildDisconnectedClinicScript(), ...CONNECT_THEN_FIX];
    const { journal } = driveAndRecord(fullScript);

    // Fresh action-by-action run: apply the SAME script through the pure
    // reducer directly, with no genesis-replay machinery at all — the
    // ground truth "what would a live session actually converge to".
    let freshActionByAction = initialState();
    for (const action of fullScript) freshActionByAction = reducer(freshActionByAction, action);
    freshActionByAction = {
      ...freshActionByAction,
      roadConnectivity: computeRoadConnectivity(freshActionByAction),
    };

    // Unchunked genesis replay (replayFromGenesis).
    const unchunkedGenesis = replayFromGenesis(journal);
    assert.equal(
      stableStringify(unchunkedGenesis),
      stableStringify(freshActionByAction),
      'unchunked replayFromGenesis must be byte-identical to a fresh action-by-action run'
    );

    // Chunked genesis replay (replayFromGenesisDefensiveChunked) — the path
    // BUG-631 flagged as sharing the tail path's stale setReplayMode premise.
    const chunkedResult = chunkedGenesis(journal);
    assert.equal(chunkedResult.skipped.length, 0, 'no action in this script is invalid under current rules');
    assert.equal(
      stableStringify(chunkedResult.state),
      stableStringify(freshActionByAction),
      `chunked genesis replay must be BYTE-IDENTICAL to a fresh action-by-action run.\n` +
        `  fresh   : ${freshActionByAction.buildings.length} buildings, funds ${freshActionByAction.funds}\n` +
        `  chunked : ${chunkedResult.state.buildings.length} buildings, funds ${chunkedResult.state.funds}\n` +
        `  (the setReplayMode-wrapped premise would let 'resolveDemand' see a STALE\n` +
        `  pre-road roadConnectivity graph, believe the now-connected clinic was still\n` +
        `  offline, and build a second clinic a real session never built — BUG-631.)`
    );
  });

  test('chunked-vs-unchunked genesis replay stay byte-identical on the same journal (chunking-invariance)', () => {
    const fullScript = [...buildDisconnectedClinicScript(), ...CONNECT_THEN_FIX];
    const { journal } = driveAndRecord(fullScript);

    const unchunkedGenesis = replayFromGenesis(journal);
    const chunkedResult = chunkedGenesis(journal);

    assert.equal(
      stableStringify(chunkedResult.state),
      stableStringify(unchunkedGenesis),
      'chunked genesis replay must be byte-identical to unchunked genesis replay on the SAME journal'
    );
  });
});
