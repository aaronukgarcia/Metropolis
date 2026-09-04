// attack-aaron-rulings-round.test.mjs — independent DESTRUCTIVE round (GR#23)
// against the 2026-09-04 "Aaron rulings" estate: Three Gorges Dam load-time
// purge + 50% scrap credit, Channel Tunnel 6x4 footprint grandfather,
// MapView monthly-twelfth-only scope grid, residential fittingTier<=3 clamp.
//
// This attacker is independent of the estate's author (GR#23 amendment).
// Money-printer / save-eater priorities per the round brief: idempotency,
// conservation, genesis-replay divergence, epoch fail-safety, footprint
// overlap truth, mutation survival.
//
// VERDICT SUMMARY (see BOW comment for the recorded verdict + full evidence):
// ACCEPT. No money-duplication exploit found: the purge is provably
// idempotent (repeated hydrate of an already-purged state is a true no-op,
// including across 5 repeated loads), conservation holds over 400
// subsequent ticks, and 4/4 attempted mutations (tick-hydrate re-credit,
// tie-break inversion, epoch-guard removal, residential-clamp removal) are
// each caught by the estate's own test suite. One real, but non-blocking,
// gap found and documented below: hard-reset-replay (genesis replay) never
// re-applies the purge (hydrate is deliberately un-journaled), so a
// hard-reset of a save that had legacy over-cap buildings produces a
// DIFFERENT — not wrong, just different and untested — economy than the
// live-purged save. This is the SAME disclosed-divergence class the
// codebase already documents for economyEpoch/BUG-652, just not yet written
// down for this new interaction. Filed as a follow-up, not a blocker: the
// only caller of genesis replay is the explicit, opt-in hard-reset-replay
// flow, which already ships its own before/after report — the divergence is
// surfaced to the player, not silent.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { SPECS, countOfSpec, surplusInstancesOf, CONSOLIDATOR_SCRAP_FRACTION, placementCost } from '../src/sim/data.ts';
import { initialState, reducer } from '../src/sim/engine.ts';
import { emptyJournal, recordAction } from '../src/sim/journal.ts';
import { replayFromGenesis, replayIsDeterministic } from '../src/sim/genesisReplay.ts';
import { runConsistencyChecks } from '../src/sim/consistency.ts';

const DAM = 'pow_hydro';
const DAM_SPEC = SPECS[DAM];

function twentyThreeDamState() {
  const genesis = initialState();
  const dams = [];
  for (let i = 0; i < 23; i++) {
    dams.push({ id: 500000 + i, spec: DAM, x: 10 + (i % 20) * 4, y: 10 + Math.floor(i / 20) * 4, builtTick: i });
  }
  return { ...genesis, unlockedAll: true, buildings: [...genesis.buildings, ...dams], nextId: genesis.nextId + 23 };
}

// ---------------------------------------------------------------------------
// MONEY PRINTER: idempotency under repeated load, and conservation over time.
// ---------------------------------------------------------------------------

describe('ATTACK: no re-credit on repeated load of an already-purged state', () => {
  test('5 successive hydrates of the same purged state produce byte-identical funds/buildings each time', () => {
    let s = reducer(initialState(), { type: 'hydrate', state: twentyThreeDamState() });
    const fundsAfterFirst = s.funds;
    const buildingsAfterFirst = s.buildings;
    for (let i = 0; i < 5; i++) {
      s = reducer(initialState(), { type: 'hydrate', state: { ...s, placeNotice: null } });
      assert.equal(s.funds, fundsAfterFirst, `hydrate #${i + 2} must not move funds again`);
      assert.equal(countOfSpec(s, DAM), 1);
    }
    assert.deepEqual(s.buildings, buildingsAfterFirst);
  });

  test('conservation (funds-vs-flows) holds for 400 ticks following the purge', () => {
    let s = reducer(initialState(), { type: 'hydrate', state: twentyThreeDamState() });
    let failures = 0;
    for (let i = 0; i < 400; i++) {
      s = reducer(s, { type: 'tick' });
      const report = runConsistencyChecks(s);
      if (report.failures !== 0) failures++;
    }
    assert.equal(failures, 0, 'no consistency-check failures across 400 post-purge ticks');
  });

  test('the scrap credit line appears EXACTLY ONCE in lastFlows.inflows and EXACTLY ONCE in the ledger, never duplicated', () => {
    const s = reducer(initialState(), { type: 'hydrate', state: twentyThreeDamState() });
    const label = `Surplus ${DAM_SPEC.name} decommission scrap`;
    const inflowMatches = s.lastFlows.inflows.filter((f) => f.label === label);
    const ledgerMatches = s.ledger.filter((e) => e.label === label);
    assert.equal(inflowMatches.length, 1);
    assert.equal(ledgerMatches.length, 1);
  });
});

// ---------------------------------------------------------------------------
// GENESIS-REPLAY DIVERGENCE (documented finding, not a rejection).
// ---------------------------------------------------------------------------

describe('FINDING: genesis replay never re-applies the purge (hydrate is un-journaled)', () => {
  test("'hydrate' is NOT a journaled action type — genesis replay of a real journal can never re-trigger the purge/credit", () => {
    // journal.ts's isStateAffecting(hydrate) === false is the load-bearing
    // fact here; assert it via the observable behaviour (recordAction is a
    // no-op for it) rather than reaching into journal.ts internals.
    let j = emptyJournal();
    j = recordAction(j, 0, { type: 'hydrate', state: twentyThreeDamState() });
    assert.deepEqual(j.entries, [], 'hydrate must never be recorded to the journal');
  });

  test('a legacy over-cap journal replayed from genesis is SELF-deterministic (replayIsDeterministic holds) even though it never purges', () => {
    // Build a journal of 23 'place' actions the way an OLD pre-cap build
    // would have recorded them. Under TODAY'S reducer (cap always
    // enforced), only the first succeeds on replay — replayIsDeterministic
    // is a self-consistency proof (same journal, same function, twice), so
    // it holds regardless; it says nothing about matching the live-purged
    // economy, which is exactly the gap this test documents.
    let journal = emptyJournal();
    for (let i = 0; i < 23; i++) {
      const x = 10 + (i % 20) * 4, y = 10 + Math.floor(i / 20) * 4;
      journal = recordAction(journal, i, { type: 'place', spec: DAM, x, y });
    }
    assert.equal(replayIsDeterministic(journal), true);
    const replayed = replayFromGenesis(journal);
    // Under current rules, remainingAllowance blocks every placement past
    // the first — genesis replay never spends on (or purges/credits for)
    // dams 2-23, unlike a live load of the same eventual save file, which
    // paid for all 23 historically then got 50% scrap back for 22 at load.
    assert.ok(countOfSpec(replayed, DAM) <= 1, 'replay never exceeds the cap either (consistent with current rules)');
  });

  test('DOCUMENTED GAP: a live-purged save and a genesis-replay of its own journal do NOT produce the same funds total — no test anywhere asserts they should, and none should be added without an explicit Aaron ruling on which is "correct"', () => {
    // This test intentionally asserts the CURRENT (divergent) behaviour so a
    // future change to either path shows up as an intentional decision, not
    // a silent drift. It is not a correctness assertion about which number
    // is "right" — hard-reset-replay is documented elsewhere (genesisReplay.ts)
    // as deliberately NOT byte-identical to a live-continued save.
    const raw = twentyThreeDamState();
    const livePurged = reducer(initialState(), { type: 'hydrate', state: raw });

    let journal = emptyJournal();
    for (let i = 0; i < 23; i++) {
      const x = 10 + (i % 20) * 4, y = 10 + Math.floor(i / 20) * 4;
      journal = recordAction(journal, i, { type: 'place', spec: DAM, x, y });
    }
    const replayed = replayFromGenesis(journal);

    // The two paths are NOT required to match (and per this codebase's own
    // documented hard-reset-replay philosophy, are not expected to) — this
    // assertion exists to make the divergence visible rather than silent.
    assert.notEqual(livePurged.funds, replayed.funds, 'the two paths currently diverge on funds — see FINDING above; a future fix collapsing them should update this test deliberately');
  });
});

// ---------------------------------------------------------------------------
// EPOCH FAIL-SAFETY: hostile tunnelFootprintEpoch values never corrupt state.
// ---------------------------------------------------------------------------

describe('ATTACK: hostile tunnelFootprintEpoch values are handled fail-safe', () => {
  test('NaN epoch does not crash and does not re-shrink an already-overridden tunnel', async () => {
    const { stampTunnelFootprintGrandfather, LAND_TUNNEL_LEGACY_FOOTPRINT } = await import('../src/sim/data.ts');
    const genesis = initialState();
    const s = {
      ...genesis,
      tunnelFootprintEpoch: NaN,
      buildings: [
        ...genesis.buildings,
        { id: 620001, spec: 'land_tunnel', x: 10, y: 10, builtTick: 0, footprintW: 6, footprintH: 4 }, // already a real new tunnel
      ],
    };
    const stamped = stampTunnelFootprintGrandfather(s);
    const tunnel = stamped.buildings.find((b) => b.id === 620001);
    assert.equal(tunnel.footprintW, 6, 'a tunnel that already carries an explicit override must never be touched, even under a NaN epoch');
    assert.equal(tunnel.footprintH, 4);
  });

  test('a WRONG (higher-than-current) epoch is treated as already-migrated: no re-stamp, no crash', async () => {
    const { stampTunnelFootprintGrandfather } = await import('../src/sim/data.ts');
    const genesis = initialState();
    const s = {
      ...genesis,
      tunnelFootprintEpoch: 999,
      buildings: [...genesis.buildings, { id: 630001, spec: 'land_tunnel', x: 10, y: 10, builtTick: 0 }],
    };
    const stamped = stampTunnelFootprintGrandfather(s);
    const tunnel = stamped.buildings.find((b) => b.id === 630001);
    assert.equal(tunnel.footprintW, undefined, 'epoch already "ahead" of current -> treated as migrated, tunnel reads the current (bigger) spec dims, not stamped legacy');
    assert.equal(stamped.tunnelFootprintEpoch, 999, 'a higher epoch is left alone, not clobbered down to current');
  });
});
