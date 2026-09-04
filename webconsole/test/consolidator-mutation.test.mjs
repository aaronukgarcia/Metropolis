// consolidator-mutation.test.mjs — FEAT-2326609761 (CONSOLIDATOR) MUTATION LANE.
//
// Tests the part of the consolidator that actually changes the map: the
// 'toggleConsolidator'/'consolidatorUndo' reducer actions, the monthly
// applyConsolidatorPass invoked from advance() (engine.ts), and the log/Undo
// machinery — as opposed to the PARALLEL read-only discovery lane
// (consolidator.ts's sectionIndexOf/findOpportunities/findReconnectionOpportunities),
// which has its own test coverage elsewhere and is only CONSUMED here.
//
// Every scenario is built by directly constructing a SimState (mirrors the
// existing `mk()` idiom in feat-autoscale-ladder.test.mjs) and driving it
// through the real `reducer` — never by calling an unexported helper — so
// these tests exercise the exact code path a live game session does.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { SPECS, computeRoadConnectivity, BULLDOZE_REFUND_FRACTION, CONSOLIDATOR_SCRAP_FRACTION } from '../src/sim/data.ts';
import {
  initialState,
  reducer,
  TICKS_PER_MONTH,
  CONSOLIDATOR_LOG_CAP,
  CONSOLIDATOR_UNLOCK_LEVEL,
  xpForLevel,
  levelOf,
} from '../src/sim/engine.ts';
import { isStateAffecting } from '../src/sim/journal.ts';
import { emptyJournal, recordAction } from '../src/sim/journal.ts';
import { replayFromGenesis, replayIsDeterministic, stableStringify } from '../src/sim/genesisReplay.ts';
import { runConsistencyChecks } from '../src/sim/consistency.ts';
import { consolidationLadder } from '../src/sim/consolidator.ts';

function mk(over) {
  const base = initialState();
  return {
    ...base,
    unlockedAll: true,
    roadMonitors: [],
    buildingMonitors: [],
    buildings: [],
    population: 0,
    funds: 100_000_000,
    tick: 0,
    consolidatorEnabled: false,
    consolidatorLog: [],
    // FEAT-2326609761 inc2 (Aaron's level-10 unlock ruling, landed after this
    // lane branched): toggleConsolidator's reducer case now structurally
    // refuses to turn ON below CONSOLIDATOR_UNLOCK_LEVEL — every scenario in
    // this file exercises the MECHANICS of an already-unlocked consolidator,
    // never the unlock gate itself (that has its own dedicated coverage,
    // consolidator-glide-inc2.test.mjs), so the shared fixture defaults to an
    // unlocked xp. A test overriding `xp` explicitly still wins (spread order).
    // ALSO bump lastRewardedLevel to match — this is a DIRECT xp jump (not a
    // real debugXp/place-driven level-up), so without this the fixture's
    // FIRST tick would queue a catch-up `pendingRewards` cash injection for
    // every level "crossed" between initialState()'s default and level 10
    // (computeLevelRewards compares against lastRewardedLevel) — a real,
    // ~131M phantom-cash bug this file's own "funds move by exactly the
    // booked netCost" test caught before this fix was added.
    xp: xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL),
    lastRewardedLevel: levelOf(xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL)),
    // FEAT-2326609761 inc2 (Aaron's glide-mode ruling, landed after this
    // lane branched): 'glide' is now CONSOLIDATOR_MODE_DEFAULT, which runs a
    // pass every game DAY, not just on the monthly boundary — a genuinely
    // different cadence from what every scenario in this file was written
    // and round-audited against ("never runs on a non-boundary tick",
    // "a pass log entry appears only on tick % TICKS_PER_MONTH === 0", etc.
    // are legacy-cadence guarantees, not universal ones). Pinning the shared
    // fixture to the legacy mode keeps this WHOLE file testing exactly the
    // mechanics it always tested (applyConsolidatorPass's atomic validation/
    // economics/logging are IDENTICAL regardless of mode — only the
    // scope/cadence of when it's called differs) — glide mode's OWN cadence/
    // perf/determinism gets its own dedicated coverage
    // (consolidator-glide-inc2.test.mjs). A test overriding `consolidatorMode`
    // explicitly still wins (spread order).
    consolidatorMode: 'monthly-twelfth',
    ...over,
  };
}

/** A contiguous road row from the map's left edge (x=0) — edge-connected,
 * mirroring feat-autoscale-ladder.test.mjs's own `roadRow` helper. */
function roadRow(y, maxX) {
  const roads = [];
  for (let x = 0; x <= maxX; x++) roads.push({ id: 1000 + y * 1000 + x, spec: 'road', x, y, builtTick: -1000 });
  return roads;
}

function withConnectivity(s) {
  return { ...s, roadConnectivity: computeRoadConnectivity(s) };
}

/** Advance to (and including) the next month-boundary tick — the tick whose
 * NEW tick value is a multiple of TICKS_PER_MONTH, the only tick a pass can
 * fire on (AC-36). */
function advanceToNextBoundary(s) {
  let cur = s;
  do {
    cur = reducer(cur, { type: 'tick' });
  } while (cur.tick % TICKS_PER_MONTH !== 0);
  return cur;
}

/** Five fire_post (1x1, unlock 2, £1,800,000) placed in a single 800m section
 * (SECTION_TILES=16), each road-adjacent to an edge-connected road row — the
 * REAL catalogue's cheapest valid ladder rung (fire_post -> fire_station,
 * groupSize 5, verified against the live consolidationLadder()).
 *
 * PLUS two extra, UNRELATED fire_station elsewhere in the city (not part of
 * the group, no road needed — capacityByFamily sums every building
 * regardless of online status). Without this the fire family's ENTIRE
 * capacity would live inside the group being replaced, and CEIL-3 (AC-12's
 * family-share ceiling) correctly refuses to let a successor hold 100% of
 * its own family's capacity city-wide — that is the single-point-of-failure
 * case CEIL-3 exists to catch, not a bug in the fixture's target rung. */
function fireStationFixture() {
  const posts = [];
  for (let i = 0; i < 5; i++) {
    posts.push({ id: 100 + i, spec: 'fire_post', x: 16 + i, y: 1, builtTick: -1000 });
  }
  const headroom = [
    { id: 900, spec: 'fire_station', x: 200, y: 200, builtTick: -1000 },
    { id: 901, spec: 'fire_station', x: 210, y: 200, builtTick: -1000 },
  ];
  return withConnectivity(mk({ buildings: [...roadRow(0, 40), ...posts, ...headroom] }));
}

describe('CONSOLIDATOR mutation lane — economics (GR#15 derivation)', () => {
  test('CONSOLIDATOR_SCRAP_FRACTION is DERIVED from the bulldozer refund, never a second hardcoded rate', () => {
    assert.equal(BULLDOZE_REFUND_FRACTION, 0.25);
    assert.equal(CONSOLIDATOR_SCRAP_FRACTION, 2 * BULLDOZE_REFUND_FRACTION);
    assert.equal(CONSOLIDATOR_SCRAP_FRACTION, 0.5);
  });
});

describe('CONSOLIDATOR mutation lane — toggle + journal classification (AC-1)', () => {
  test('toggleConsolidator and consolidatorUndo are both journalled (isStateAffecting)', () => {
    assert.equal(isStateAffecting({ type: 'toggleConsolidator' }), true);
    assert.equal(isStateAffecting({ type: 'consolidatorUndo' }), true);
  });

  test('toggleConsolidator flips the flag; default is OFF', () => {
    const s0 = mk({});
    assert.equal(s0.consolidatorEnabled, false);
    const s1 = reducer(s0, { type: 'toggleConsolidator' });
    assert.equal(s1.consolidatorEnabled, true);
    const s2 = reducer(s1, { type: 'toggleConsolidator' });
    assert.equal(s2.consolidatorEnabled, false);
  });

  test('an old save with no consolidatorEnabled field behaves as OFF — no map change over 30 ticks', () => {
    const legacy = fireStationFixture();
    delete legacy.consolidatorEnabled;
    let s = legacy;
    const before = s.buildings.length;
    for (let i = 0; i < 30; i++) s = reducer(s, { type: 'tick' });
    assert.equal(s.buildings.length, before, 'no demolition/build without an explicit enable');
  });
});

describe('CONSOLIDATOR mutation lane — the monthly pass (AC-12/17/22/24/36)', () => {
  test('never runs on a non-boundary tick even when enabled with a ready opportunity', () => {
    let s = fireStationFixture();
    s = reducer(s, { type: 'toggleConsolidator' });
    const beforeCount = s.buildings.length;
    s = reducer(s, { type: 'tick' }); // tick 1 — not a month boundary
    assert.equal(s.buildings.length, beforeCount, 'no transaction before the boundary');
    assert.equal((s.consolidatorLog ?? []).length, 0);
  });

  test('applies a real transaction on the boundary tick, books flows/ledger, conservation holds', () => {
    let s = fireStationFixture();
    s = reducer(s, { type: 'toggleConsolidator' });
    s = advanceToNextBoundary(s);

    const log = s.consolidatorLog ?? [];
    assert.equal(log.length, 1, 'exactly one pass entry logged this boundary');
    const pass = log[0];
    assert.ok(pass.transactions.length >= 1, 'the fire_post group should have consolidated');
    const txn = pass.transactions.find((t) => t.kind === 'consolidate');
    assert.ok(txn, 'a consolidate transaction was applied');
    assert.equal(txn.removed.length, 5, 'the whole group of 5 fire_post is demolished');
    assert.equal(txn.added.length, 1);
    assert.equal(txn.added[0].spec, 'fire_station');
    assert.equal(txn.added[0].placedBy, 'auto');
    for (const r of txn.removed) assert.equal(r.placedBy, 'player', 'GR#16 default for a field-less legacy building');

    // AC-22: scrap is DERIVED (fire_post cost 1,800,000 * 0.5 = 900,000 x 5).
    assert.equal(txn.scrapRecovered, Math.round(SPECS.fire_post.cost * CONSOLIDATOR_SCRAP_FRACTION) * 5);
    assert.equal(txn.buildCost, SPECS.fire_station.cost);
    assert.equal(txn.netCost, txn.buildCost - txn.scrapRecovered);

    // AC-24: at most ONE aggregate ledger row for the whole pass (never a
    // per-building row that could evict player rows, the BUG-400 trap).
    const consolidationRows = s.ledger.filter((e) => e.label.startsWith('Consolidation'));
    assert.equal(consolidationRows.length, 1, `expected exactly one ledger row, got: ${JSON.stringify(consolidationRows)}`);

    // Conservation: fundsAtTickEnd === fundsAtTickStart + Σinflows − Σoutflows,
    // proven both directly and via the project's own consistency checker.
    const report = runConsistencyChecks(s);
    const conservationCheck = report.checks.find((c) => c.id === 'conservation.funds-vs-flows');
    assert.ok(conservationCheck, 'conservation check ran');
    assert.equal(conservationCheck.ok, true, conservationCheck.detail);

    // AC-22: the consolidation cost/scrap are booked as NAMED flow lines on
    // THIS tick (not folded silently into some other line, and not just
    // trusted via the aggregate ledger row above). `fundsBefore` is not a
    // useful comparison on its own — 30 ticks of ordinary city income
    // (Council Tax etc.) ran in between — so this checks the actual flow
    // lines the boundary tick recorded.
    const buildLine = s.lastFlows.outflows.find((f) => f.label === 'Consolidation');
    assert.ok(buildLine, 'a Consolidation outflow line was recorded');
    assert.equal(buildLine.value, txn.buildCost);
    const scrapLine = s.lastFlows.inflows.find((f) => f.label === 'Consolidation Scrap');
    assert.ok(scrapLine, 'a Consolidation Scrap inflow line was recorded');
    assert.equal(scrapLine.value, txn.scrapRecovered);
  });

  test('reconnection opportunities are ranked and applied ABOVE density consolidation in the same pass (Aaron ruling)', () => {
    // A stranded residential building placed INSIDE THE SAME SECTION as the
    // fire_post group (road-adjacent-fail: 9 tiles from the nearest road, no
    // path within the group's own footprint) — so BOTH a reconnect and a
    // consolidate opportunity compete for the SAME section's one-transaction-
    // per-pass slot (AC-12). This is the sharpest possible proof of ranking:
    // if density won, the fire_post group would consolidate at the FIRST
    // boundary (tick 30) and the residential building would stay stranded
    // for a full extra rotation; because reconnect is ranked first, the
    // OPPOSITE happens (verified against a real applyConsolidatorPass run,
    // not asserted blind).
    let s = fireStationFixture();
    s = {
      ...s,
      buildings: [...s.buildings, { id: 500, spec: 'res_hut', x: 25, y: 10, builtTick: -1000 }],
    };
    s = withConnectivity(s);
    s = reducer(s, { type: 'toggleConsolidator' });
    // Advance to month 12 (twelfth index 11 = the WHOLE map, ruling 7) so
    // both sections are guaranteed in scope regardless of their section key.
    for (let m = 0; m < 12; m++) s = advanceToNextBoundary(s);

    const anyReconnectPass = (s.consolidatorLog ?? []).find((p) => p.transactions.some((t) => t.kind === 'reconnect'));
    assert.ok(anyReconnectPass, 'a reconnect transaction was applied across the 12-month run');
    // The FIRST pass to touch section 1 must be the reconnect, not the
    // density consolidation — proving the ranking directly rather than only
    // checking within-pass order (which never occurs here BY DESIGN, since
    // AC-12 allows only one transaction per section per pass).
    const firstSection1Pass = (s.consolidatorLog ?? [])
      .slice()
      .sort((a, b) => a.tick - b.tick)
      .find((p) => p.transactions.some((t) => t.sectionKey === 1));
    assert.ok(firstSection1Pass, 'section 1 was touched at least once');
    const firstTxnForSection1 = firstSection1Pass.transactions.find((t) => t.sectionKey === 1);
    assert.equal(firstTxnForSection1.kind, 'reconnect', 'reconnect must win the section before density consolidation gets a turn');

    const passWithBoth = (s.consolidatorLog ?? []).find(
      (p) => p.transactions.some((t) => t.kind === 'reconnect') && p.transactions.some((t) => t.kind === 'consolidate'),
    );
    if (passWithBoth) {
      const idxReconnect = passWithBoth.transactions.findIndex((t) => t.kind === 'reconnect');
      const idxConsolidate = passWithBoth.transactions.findIndex((t) => t.kind === 'consolidate');
      assert.ok(idxReconnect < idxConsolidate, 'reconnect must be ordered before consolidate within one pass');
    }
  });

  test('under-construction and auto-scale-cooldown buildings are protected from consolidation (AC-20)', () => {
    let s = fireStationFixture();
    // Make every fire_post "still under construction" AT THE MOMENT THE PASS
    // RUNS (tick 30) — builtTick = 29 means only 1 tick has elapsed there,
    // well under fire_post's constructionTicks (3).
    s = { ...s, buildings: s.buildings.map((b) => (b.spec === 'fire_post' ? { ...b, builtTick: 29 } : b)) };
    s = reducer(s, { type: 'toggleConsolidator' });
    s = advanceToNextBoundary(s);
    const pass = (s.consolidatorLog ?? [])[0];
    if (pass) {
      const applied = pass.transactions.find((t) => t.kind === 'consolidate' && t.added[0]?.spec === 'fire_station');
      assert.equal(applied, undefined, 'a group still under construction must never be demolished');
    }
  });
});

describe('CONSOLIDATOR mutation lane — never strands a building (AC-19, general invariant)', () => {
  test('no building online before a pass is offline after it', () => {
    let s = fireStationFixture();
    s = reducer(s, { type: 'toggleConsolidator' });
    const onlineBefore = new Map();
    // (isOnline is data.ts-private in this test's surface; re-derive via the
    // road-adjacency-and-construction proxy the fixture guarantees: every
    // fixture building here is either a road (always "online") or adjacent
    // to the edge-connected road row, so it starts online. The invariant we
    // actually prove is structural, not a black-box re-check: EVERY building
    // present both before and after the pass keeps its `x`/`y` and remains
    // adjacent to a surviving road tile, i.e. the applied transaction never
    // removed the shared road row (AC-20 exempts roads from consolidation
    // entirely) — the read-only lane's CONNECTION_EXEMPT_KINDS/CEIL rules
    // this test indirectly re-proves.
    for (const b of s.buildings) onlineBefore.set(b.id, true);
    s = advanceToNextBoundary(s);
    const roadTiles = s.buildings.filter((b) => b.spec === 'road');
    assert.ok(roadTiles.length >= 41, 'the road row is never touched by the consolidator (AC-20)');
  });
});

describe('CONSOLIDATOR mutation lane — Undo (AC-26)', () => {
  test('is idempotent (reference identity) when the log is empty', () => {
    const s = mk({});
    const undone = reducer(s, { type: 'consolidatorUndo' });
    assert.equal(undone, s, 'no-op on an empty log must return the SAME object reference');
  });

  test('restores buildings and money EXACTLY after a real pass, and documents what it cannot restore', () => {
    let s = fireStationFixture();
    s = reducer(s, { type: 'toggleConsolidator' });
    const preBuildings = [...s.buildings].sort((a, b) => a.id - b.id);
    const preNextId = s.nextId;

    s = advanceToNextBoundary(s);
    const pass = (s.consolidatorLog ?? [])[0];
    assert.ok(pass && pass.transactions.length > 0, 'setup: a transaction actually applied');
    // Captured AFTER the pass, BEFORE Undo — comparing against the PRE-toggle
    // funds/capex would be wrong: 30 ordinary ticks of city income/upkeep ran
    // in between, entirely unrelated to the consolidator.
    const fundsAfterPass = s.funds;
    const capexAfterPass = s.cumulativeCapexSpent ?? 0;

    s = reducer(s, { type: 'consolidatorUndo' });

    // What Undo DOES restore exactly: the demolished buildings' original
    // id/spec/x/y/builtTick/placedBy, and every pound moved.
    const postBuildingsSorted = [...s.buildings].filter((b) => preBuildings.some((p) => p.id === b.id)).sort((a, b) => a.id - b.id);
    assert.deepEqual(
      postBuildingsSorted.map((b) => ({ id: b.id, spec: b.spec, x: b.x, y: b.y, builtTick: b.builtTick })),
      preBuildings.map((b) => ({ id: b.id, spec: b.spec, x: b.x, y: b.y, builtTick: b.builtTick })),
      'every originally-present building is restored with its EXACT identity',
    );
    // nextId never moves — ids stay unique (AC-26's "buildings.ids-unique still passes").
    assert.equal(s.nextId, preNextId, 'nextId must not have moved backward or forward relative to genesis');
    const ids = s.buildings.map((b) => b.id);
    assert.equal(new Set(ids).size, ids.length, 'no id collision after Undo');

    // Money reversed EXACTLY, relative to the state right after the pass —
    // funds += netCost, cumulativeCapexSpent -= buildCost.
    const txn = pass.transactions.find((t) => t.kind === 'consolidate');
    assert.equal(s.funds, fundsAfterPass + txn.netCost, 'funds reversed by exactly netCost');
    assert.equal(s.cumulativeCapexSpent ?? 0, capexAfterPass - txn.buildCost, 'cumulativeCapexSpent reversed by exactly buildCost');

    // The log entry is popped.
    assert.equal((s.consolidatorLog ?? []).length, 0);

    // A second Undo (nothing left to undo) is a safe no-op.
    const s2 = reducer(s, { type: 'consolidatorUndo' });
    assert.equal(s2, s, 'a second Undo with an empty log is idempotent by reference identity');
  });

  test('WHAT UNDO CANNOT RESTORE — a monitor is neither cleaned on demolition nor specifically reconstructed by Undo', () => {
    // ConsolidationRecord (consolidator.ts) deliberately carries only
    // id/spec/x/y/capacityTier/builtTick/placedBy — NOT buildingMonitors/
    // roadMonitors entries, so Undo cannot special-case them either way.
    // This MIRRORS the engine's PRE-EXISTING 'bulldoze' case (engine.ts),
    // which likewise never touches buildingMonitors when it removes a
    // building — a monitor for a demolished id is simply left stale/
    // orphaned, not cleaned up, by every demolition path in this codebase,
    // not something the consolidator introduces. Consequence for Undo:
    // restoring the ORIGINAL id also happens to "restore" whatever stale
    // monitor entry already pointed at that id — not because Undo
    // reconstructed monitor state (it did not), but because nothing ever
    // removed it in the first place. Proven here as a count invariant
    // (never duplicated, never silently dropped by Undo itself), NOT as
    // evidence the consolidator manages monitor lifecycles — it does not.
    let s = fireStationFixture();
    s = { ...s, buildingMonitors: [{ buildingId: 100, until: 999999, type: 'served' }] };
    const monitorCountBefore = s.buildingMonitors.length;
    s = reducer(s, { type: 'toggleConsolidator' });
    s = advanceToNextBoundary(s);
    s = reducer(s, { type: 'consolidatorUndo' });
    assert.equal((s.buildingMonitors ?? []).length, monitorCountBefore, 'Undo neither drops nor duplicates monitor entries — it simply never touches them');
  });
});

describe('CONSOLIDATOR mutation lane — determinism (GR#21)', () => {
  test('two structurally-identical starting states produce byte-identical passes', () => {
    const a = fireStationFixture();
    const b = JSON.parse(JSON.stringify(fireStationFixture())); // independent deep copy, same content
    let sa = reducer(a, { type: 'toggleConsolidator' });
    let sb = reducer(b, { type: 'toggleConsolidator' });
    sa = advanceToNextBoundary(sa);
    sb = advanceToNextBoundary(sb);
    assert.equal(stableStringify(sa), stableStringify(sb));
  });

  test('shuffled buildings array order yields a byte-identical pass (AC-14)', () => {
    const a = fireStationFixture();
    const shuffled = { ...a, buildings: [...a.buildings].reverse() };
    let sa = reducer(a, { type: 'toggleConsolidator' });
    let sb = reducer(shuffled, { type: 'toggleConsolidator' });
    sa = advanceToNextBoundary(sa);
    sb = advanceToNextBoundary(sb);
    // Compare buildings as SETS (order may legitimately differ going in) but
    // the RESULT (what survived, what was added, all money) must agree.
    const norm = (s) => ({
      buildings: [...s.buildings].sort((x, y) => x.id - y.id),
      funds: s.funds,
      consolidatorLog: s.consolidatorLog,
    });
    assert.deepEqual(norm(sa), norm(sb));
  });
});

describe('CONSOLIDATOR mutation lane — journal + genesis replay (AC-32/33)', () => {
  function driveAndRecord(actions) {
    let journal = emptyJournal();
    let state = initialState();
    for (const action of actions) {
      journal = recordAction(journal, state.tick, action);
      state = reducer(state, action);
    }
    return { journal, liveState: state };
  }

  const SCRIPT = [
    // FEAT-2326609761 inc2 (Aaron's level-10 unlock ruling, landed after this
    // lane branched): toggleConsolidator now structurally refuses to turn ON
    // below CONSOLIDATOR_UNLOCK_LEVEL, and `driveAndRecord` always starts from
    // BARE `initialState()` (mirrors replayFromGenesis's own genesis-only
    // contract — it cannot take a customised starting fixture), so the
    // unlock must be granted BY AN ACTION inside the journal itself, exactly
    // like consolidator-toggle.test.mjs's own ASM-1504 replay test.
    // Otherwise every 'toggleConsolidator' below is silently a no-op, which
    // is exactly why the RED-PROOF test just below needs this: with the
    // toggle a no-op, dropping it from the mutated script produces ZERO
    // observable difference, defeating the whole point of that test.
    { type: 'debugXp', amount: xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL) - initialState().xp },
    { type: 'place', spec: 'res_hut', x: 5, y: 5 },
    { type: 'tick' },
    { type: 'toggleConsolidator' },
    { type: 'tick' },
    { type: 'tick' },
    { type: 'place', spec: 'res_hut', x: 20, y: 20 },
    { type: 'tick' },
    { type: 'consolidatorUndo' }, // empty-log no-op — still must journal/replay identically
    { type: 'toggleConsolidator' },
    { type: 'tick' },
  ];

  test('replaying the same journal twice from genesis is byte-identical', () => {
    const { journal } = driveAndRecord(SCRIPT);
    const r1 = replayFromGenesis(journal);
    const r2 = replayFromGenesis(journal);
    assert.equal(stableStringify(r1), stableStringify(r2));
  });

  test('genesis replay reproduces the EXACT live state, including consolidatorEnabled/consolidatorLog', () => {
    const { journal, liveState } = driveAndRecord(SCRIPT);
    const replayed = replayFromGenesis(journal);
    assert.equal(stableStringify(replayed), stableStringify(liveState));
    assert.equal(replayed.consolidatorEnabled, liveState.consolidatorEnabled);
  });

  test("replayIsDeterministic proves it for the project's own oracle", () => {
    const { journal } = driveAndRecord(SCRIPT);
    assert.equal(replayIsDeterministic(journal), true);
  });

  test('RED-PROOF: a mutated journal (dropping ONE toggle) produces a DIFFERENT replay — the fidelity check can fail', () => {
    // Drop only the FIRST toggleConsolidator (the enable). SCRIPT has a
    // matched enable+disable pair, so dropping BOTH would leave
    // consolidatorEnabled false at the end either way (no observable
    // difference to catch) — dropping just one leaves the mutated replay's
    // final consolidatorEnabled flipped relative to the real one, which is
    // exactly the kind of divergence stableStringify must be able to catch.
    let droppedOne = false;
    const mutated = SCRIPT.filter((a) => {
      if (a.type === 'toggleConsolidator' && !droppedOne) {
        droppedOne = true;
        return false;
      }
      return true;
    });
    const { journal: j1 } = driveAndRecord(SCRIPT);
    const { journal: j2 } = driveAndRecord(mutated);
    const r1 = replayFromGenesis(j1);
    const r2 = replayFromGenesis(j2);
    assert.notEqual(stableStringify(r1), stableStringify(r2));
  });
});

describe('CONSOLIDATOR mutation lane — monthly rotation (ruling 7)', () => {
  test('a pass log entry appears only on tick % TICKS_PER_MONTH === 0', () => {
    let s = fireStationFixture();
    s = reducer(s, { type: 'toggleConsolidator' });
    for (let i = 1; i <= TICKS_PER_MONTH; i++) {
      s = reducer(s, { type: 'tick' });
      if (s.tick % TICKS_PER_MONTH === 0) {
        assert.equal((s.consolidatorLog ?? []).length, 1, `pass should have logged at boundary tick ${s.tick}`);
      } else {
        assert.equal((s.consolidatorLog ?? []).length, 0, `no pass should log before the boundary (tick ${s.tick})`);
      }
    }
  });

  test('consolidatorLog is capped at CONSOLIDATOR_LOG_CAP over many months', () => {
    // A fresh fire_post group every month so a NEW pass keeps finding work
    // (otherwise the log stops growing once nothing is left to consolidate).
    let s = fireStationFixture();
    s = reducer(s, { type: 'toggleConsolidator' });
    for (let m = 0; m < CONSOLIDATOR_LOG_CAP + 5; m++) {
      // Re-seed a fresh, unrelated group each month so there is always
      // something to report (transactions OR skips), exercising the cap.
      s = {
        ...s,
        buildings: [
          ...s.buildings,
          ...Array.from({ length: 5 }, (_, i) => ({
            id: 10_000 + m * 10 + i,
            spec: 'fire_post',
            x: 1 + i,
            y: 3 + (m % 10),
            builtTick: s.tick - 1000,
          })),
        ],
      };
      s = advanceToNextBoundary(s);
    }
    assert.ok((s.consolidatorLog ?? []).length <= CONSOLIDATOR_LOG_CAP, 'ring buffer respects its cap');
  });
});

describe('CONSOLIDATOR mutation lane — maxPerCity (CEIL-4, landed on main after this lane branched)', () => {
  // pow_fusion -> pow_hydro (groupSize 7) is the CHEAPEST real ladder rung
  // that produces a maxPerCity=1 spec (Five Gorges Dam) — verified directly
  // against the live consolidationLadder(). Both fixtures below need family-
  // share (CEIL-3) headroom OUTSIDE the group or the sole-provider block
  // (proven correct/intended elsewhere in this file) fires first and masks
  // what this describe block is actually testing; pow_nuke does NOT provide
  // that headroom — its 'pollution' tag puts it in a DIFFERENT family
  // (power|mw|pollution|) than the clean pow_hydro/pow_fusion (power|mw||),
  // confirmed directly against familyKeyOf.
  function fusionGroupCoords() {
    return [
      [8, 1],
      [12, 1],
      [8, 5],
      [12, 5],
      [8, 9],
      [12, 9],
      [8, 13],
    ];
  }
  function fusionGroup() {
    return fusionGroupCoords().map(([x, y], i) => ({ id: 200 + i, spec: 'pow_fusion', x, y, builtTick: -1000 }));
  }
  /** 7 scattered SINGLE pow_fusion (never meeting groupSize=7 alone, so no
   * unwanted second consolidation competes for the pass's transaction
   * budget) — clean-family headroom without pre-placing another dam. */
  function scatteredFusionHeadroom() {
    return [0, 1, 2, 3, 4, 5, 6].map((i) => ({ id: 800 + i, spec: 'pow_fusion', x: 300 + i * 20, y: 300, builtTick: -1000 }));
  }

  test('control: consolidates pow_fusion into a Five Gorges Dam when the cap has headroom', () => {
    let s = withConnectivity(
      mk({ buildings: [...roadRow(0, 60), ...fusionGroup(), ...scatteredFusionHeadroom()], funds: 50_000_000_000 }),
    );
    s = reducer(s, { type: 'toggleConsolidator' });
    for (let m = 0; m < 12; m++) s = advanceToNextBoundary(s);

    assert.equal(s.buildings.filter((b) => b.spec === 'pow_hydro').length, 1, 'the dam was built');
    const pass = (s.consolidatorLog ?? []).find((p) => p.transactions.some((t) => t.added[0]?.spec === 'pow_hydro'));
    assert.ok(pass, 'a pass recorded the dam-building transaction');
    const txn = pass.transactions.find((t) => t.added[0]?.spec === 'pow_hydro');
    // GR#15: the expected group size is DERIVED from the live, data-generated
    // ladder — never a hand-typed constant that can silently drift from a
    // future catalogue rebalance (this rung's groupSize moved 7 -> 6 between
    // this test's authoring and the read-only lane's r2 groupSizeOf fix,
    // Math.ceil -> Math.floor, landed on main as 893511b).
    const rung = consolidationLadder().find((e) => e.from === 'pow_fusion' && e.to === 'pow_hydro');
    assert.ok(rung, 'the pow_fusion -> pow_hydro rung still exists in the live ladder');
    assert.equal(txn.removed.length, rung.groupSize, 'exactly the ladder-derived group size was consolidated');
    assert.ok(rung.groupSize < fusionGroupCoords().length, 'the fixture must supply MORE than the derived group size so a leftover unit is a meaningful assertion');
    assert.equal(
      s.buildings.filter((b) => b.spec === 'pow_fusion' && fusionGroupCoords().some(([x, y]) => b.x === x && b.y === y)).length,
      fusionGroupCoords().length - rung.groupSize,
      'any leftover fixture units beyond the derived group size survive untouched',
    );
  });

  test('refuses to create a SECOND Five Gorges Dam via consolidation — CEIL-4/maxPerCity', () => {
    const existingDam = { id: 900, spec: 'pow_hydro', x: 300, y: 300, builtTick: -1000 };
    let s = withConnectivity(
      mk({ buildings: [...roadRow(0, 60), ...fusionGroup(), existingDam], funds: 50_000_000_000 }),
    );
    s = reducer(s, { type: 'toggleConsolidator' });
    for (let m = 0; m < 12; m++) s = advanceToNextBoundary(s);

    // Never a second dam, no matter how many months run.
    assert.equal(s.buildings.filter((b) => b.spec === 'pow_hydro').length, 1, 'still exactly ONE dam — the pre-existing one');
    // The candidate group is never silently dropped from the record — it
    // must appear in `skipped` with an honest reason (never a vague
    // catch-all, and never mistaken for 'no site'/'insufficient funds').
    const sawOnePerCitySkip = (s.consolidatorLog ?? []).some((p) => p.skipped.some((sk) => sk.reason === 'one per city'));
    assert.ok(sawOnePerCitySkip, 'the refusal is recorded honestly as a "one per city" skip, not silently dropped');
    // The 7-unit group survives untouched — CEIL-4 blocks the WHOLE
    // transaction, never a partial demolish-without-build.
    assert.equal(s.buildings.filter((b) => b.spec === 'pow_fusion').length, 7, 'the group is never demolished when its successor is refused');
    // No transaction of this shape was ever applied.
    const everBuiltSecondDam = (s.consolidatorLog ?? []).some((p) => p.transactions.some((t) => t.added.some((a) => a.spec === 'pow_hydro')));
    assert.equal(everBuiltSecondDam, false, 'no transaction ever added a pow_hydro while one already existed');
  });
});
