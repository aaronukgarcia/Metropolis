// attack-consolidator-mutation-round.test.mjs
//
// INDEPENDENT DESTRUCTIVE ROUND (GR#23 — attacker is NOT the author) against
// the FEAT-2326609761 CONSOLIDATOR **mutation** estate: applyConsolidatorPass
// inside advance(), the consolidatorUndo reducer action, the pass log, and the
// money path.
//
// Blast radius is the reason this round exists: the estate demolishes
// buildings and spends money on Aaron's live city, automatically, with no
// confirmation. Aaron's own ruling on it is "the lead keeps an eye on its
// progress to make sure it does not go mad". Every test below is an attempt to
// make it go mad.
//
// Fixture discipline: everything is built from the REAL catalogue
// (fire_post -> fire_station, groupSize 5, the cheapest genuine rung in
// consolidationLadder()) and driven through the REAL `reducer`, never through
// an unexported helper — so a green test proves the live game path, not a
// mock. Where the estate's own behaviour is asserted, the assertion is
// RED-PROVED (a comment states the mutation that makes it fail, and the
// negative control is run where it is cheap to do so).
//
// FINDINGS (see the round report): F1 free connector roads, F2 successor built
// permanently offline with the stranding recheck skipped, F3 undo restores a
// building on top of the connector the same pass laid (map corruption),
// F4 undo is not single-level, F5 boundary-tick cost on Aaron's real 49k save.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  SPECS,
  placementCost,
  computeRoadConnectivity,
  isRoadAdjacent,
  isRoadConnected,
  CONSOLIDATOR_SCRAP_FRACTION,
} from '../src/sim/data.ts';
import { initialState, reducer, TICKS_PER_MONTH, CONSOLIDATOR_LOG_CAP } from '../src/sim/engine.ts';
import { decode } from '../src/sim/saveCodec.ts';
import { monthlyScopeOf } from '../src/sim/consolidator.ts';
import { INSOLVENCY_WARNING_THRESHOLD } from '../src/sim/fiscal.ts';

// ---------------------------------------------------------------------------
// Fixture kit
// ---------------------------------------------------------------------------

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
    ...over,
  };
}

/** A contiguous, map-edge-connected road row (x=0 is the map edge => seeded). */
function roadRow(y, maxX) {
  const r = [];
  for (let x = 0; x <= maxX; x++) r.push({ id: 5000 + y * 100 + x, spec: 'road', x, y, builtTick: -1000 });
  return r;
}

function withConnectivity(s) {
  return { ...s, roadConnectivity: computeRoadConnectivity(s) };
}

/**
 * Section 1 (tiles x=16..31, y=0..15) is in the twelfth that runs at tick 30,
 * so ONE tick from tick 29 fires a real pass — no 12-month warm-up needed.
 *
 * Layout, deliberately hostile but entirely ordinary:
 *   - the road row is at y=15 (the BOTTOM of the section),
 *   - the five fire_post sit at y=14, road-adjacent and online,
 *   - y=0..13 of the section is open ground.
 * The apply lane sites the successor at the lowest (y, x) that fits, i.e.
 * (16, 0) — fourteen tiles from the nearest road. That is the whole point:
 * it is how a real city looks (services along the road, empty land behind).
 */
function fireFixture(over = {}) {
  const posts = [];
  for (let i = 0; i < 5; i++) posts.push({ id: 100 + i, spec: 'fire_post', x: 16 + i, y: 14, builtTick: -1000 });
  // CEIL-3 headroom: fire capacity that lives OUTSIDE the group, so the
  // family-share ceiling is not the thing under test.
  const headroom = [
    { id: 900, spec: 'fire_station', x: 200, y: 200, builtTick: -1000 },
    { id: 901, spec: 'fire_station', x: 210, y: 200, builtTick: -1000 },
    { id: 902, spec: 'fire_station', x: 220, y: 200, builtTick: -1000 },
    { id: 903, spec: 'fire_station', x: 230, y: 200, builtTick: -1000 },
  ];
  return withConnectivity(
    mk({
      buildings: [...roadRow(15, 40), ...posts, ...headroom],
      tick: TICKS_PER_MONTH - 1,
      consolidatorEnabled: true,
      nextId: 9000,
      ...over,
    }),
  );
}

const NET_COST = placementCost(SPECS.fire_station) - 5 * Math.round(placementCost(SPECS.fire_post) * CONSOLIDATOR_SCRAP_FRACTION);

/** Every tile covered more than once — the invariant `fits`/occupiedSet rely on. */
function overlappingTiles(state) {
  const seen = new Map();
  const bad = [];
  for (const b of state.buildings) {
    const sp = SPECS[b.spec];
    if (!sp) continue;
    for (let dx = 0; dx < sp.w; dx++)
      for (let dy = 0; dy < sp.h; dy++) {
        const k = `${b.x + dx},${b.y + dy}`;
        if (seen.has(k)) bad.push(`${k}: ${seen.get(k)} vs ${b.id}:${b.spec}`);
        else seen.set(k, `${b.id}:${b.spec}`);
      }
  }
  return bad;
}

function lastPass(s) {
  return (s.consolidatorLog ?? [])[0] ?? null;
}

// ---------------------------------------------------------------------------
// F1 — MONEY: the connector road the pass lays for its own successor is FREE
// ---------------------------------------------------------------------------

describe('ATTACK 1 — money: is every pound the pass spends actually charged?', () => {
  test('a consolidate transaction charges placementCost(successor) ONLY — the connector road it lays is never billed', () => {
    const s0 = fireFixture({ funds: 100_000_000 });
    const s1 = reducer(s0, { type: 'tick' });

    const pass = lastPass(s1);
    assert.ok(pass && pass.transactions.length === 1, 'setup: exactly one consolidate transaction applied');
    const txn = pass.transactions[0];
    assert.equal(txn.kind, 'consolidate');

    // What the pass actually built, beyond the successor: connector road tiles.
    const created = s1.buildings.filter((b) => b.id >= s0.nextId);
    const connectors = created.filter((b) => b.id !== txn.added[0].id);
    assert.ok(connectors.length > 0, 'setup: the successor needed a connector (it is 14 tiles from the road)');

    const connectorValue = connectors.reduce((sum, b) => sum + placementCost(SPECS[b.spec]), 0);
    assert.ok(connectorValue > 0, 'setup: those connector tiles are not free specs');

    // r3 fix (this round): autoConnect can also upgrade an EXISTING road tile
    // IN PLACE at a junction (same id, spec bumped to the connector's tier) —
    // that tile never shows up in `created` (id < s0.nextId) because it was
    // never newly appended, only rewritten. A reconstruction that only sums
    // `created` tiles is structurally blind to this spend, which is exactly
    // the £15,000 gap this fixture exposes at tile 6516 (road -> rd_avenue).
    // Summing the upgrade delta closes the gap without weakening the gate:
    // this is still a full independent reconstruction from the building diff,
    // not a copy of the production formula under test.
    const beforeById = new Map(s0.buildings.map((b) => [b.id, b]));
    const upgraded = s1.buildings.filter((b) => {
      const before = beforeById.get(b.id);
      return before && before.spec !== b.spec;
    });
    const upgradeValue = upgraded.reduce(
      (sum, b) => sum + Math.max(0, placementCost(SPECS[b.spec]) - placementCost(SPECS[beforeById.get(b.id).spec])),
      0,
    );

    // RED-PROOF: this assertion fails the moment `buildCost` stops covering the
    // connector or an in-place junction upgrade — flip either component out of
    // the sum and it goes red, so this exact-equality form is the real gate.
    assert.equal(
      txn.buildCost,
      placementCost(SPECS.fire_station) + connectorValue + upgradeValue,
      `F1: the pass laid ${connectors.length} ${connectors[0].spec} tiles worth ${connectorValue} plus ` +
        `${upgraded.length} in-place upgrade(s) worth ${upgradeValue}, and billed ${txn.buildCost} — ` +
        `advance() reverts funds to preFunds and re-books ONLY sum(txn.buildCost) as the 'Consolidation' outflow, ` +
        `so autoConnect's own spend inside the pass must be fully covered or the city gets free roads every pass.`,
    );
  });

  test('funds move by exactly the booked netCost — which is how the free-road leak stays invisible to conservation', () => {
    const s0 = fireFixture({ funds: 100_000_000 });
    const s1 = reducer(s0, { type: 'tick' });
    const pass = lastPass(s1);
    const bookedNet = pass.transactions.reduce((n, t) => n + t.netCost, 0);
    // Ordinary tick income/upkeep is tiny in this fixture but non-zero, so the
    // comparison is bounded rather than exact — the point is that the connector
    // (27,000 x 14 = 378,000) is nowhere in the money path.
    const delta = s0.funds - s1.funds;
    assert.ok(
      Math.abs(delta - bookedNet) < 10_000,
      `funds moved by ${delta}, the pass booked ${bookedNet} — conservation.funds-vs-flows holds, ` +
        `which is precisely why F1 is silent: the asset appears, the money never left.`,
    );
    const row = (s1.ledger ?? []).find((e) => String(e.label).startsWith('Consolidation'));
    assert.ok(row, 'AC-24: exactly one aggregate ledger row per pass');
    assert.equal(row.amount, -bookedNet);
    assert.equal((s1.ledger ?? []).filter((e) => String(e.label).startsWith('Connector ')).length, 0, 'AC-24: no per-connector rows leak into the ledger');
  });

  test('the 50% scrap rate is charged per demolished unit, and a zero-cost zone refunds nothing', () => {
    const s1 = reducer(fireFixture(), { type: 'tick' });
    const txn = lastPass(s1).transactions[0];
    assert.equal(txn.scrapRecovered, 5 * Math.round(placementCost(SPECS.fire_post) * CONSOLIDATOR_SCRAP_FRACTION));
    assert.equal(txn.netCost, txn.buildCost - txn.scrapRecovered);
    // No money printer: scrap can never exceed what was paid for the group.
    assert.ok(txn.scrapRecovered <= 5 * placementCost(SPECS.fire_post));
  });
});

// ---------------------------------------------------------------------------
// F2 — THE CITY-BREAKER: the successor's own online status is never checked,
//      and the skip-recheck optimisation guarantees nothing catches it.
// ---------------------------------------------------------------------------

describe('ATTACK 2 — can it destroy something it should not? (AC-19 + the skip-recheck predicate)', () => {
  test('a pass will demolish five ROAD-CONNECTED buildings and leave a successor that can never come online', () => {
    // Funds chosen so the transaction itself is affordable but the connector is
    // not: netCost < funds < netCost + connectorCost. autoConnect's
    // `if (s.funds < totalCost)` branch then returns the state UNCHANGED — no
    // tiles appended — so `roadTopologyMayHaveChanged` is false, the AC-19
    // recheck is SKIPPED, and the transaction commits.
    const funds = NET_COST + 60_000; // one connector tile costs 27,000; the route needs 14.
    const s0 = fireFixture({ funds });

    const onlineBefore = s0.buildings.filter((b) => b.spec === 'fire_post' && isRoadAdjacent(s0, b) && isRoadConnected(s0, b));
    assert.equal(onlineBefore.length, 5, 'setup: all five fire_post start road-adjacent AND road-connected');

    const s1 = reducer(s0, { type: 'tick' });
    const pass = lastPass(s1);
    assert.ok(pass && pass.transactions.length === 1, 'setup: the transaction committed (it was affordable)');
    assert.equal(s1.buildings.filter((b) => b.spec === 'fire_post').length, 0, 'setup: all five were demolished');

    const successor = s1.buildings.find((b) => b.id === pass.transactions[0].added[0].id);
    assert.ok(successor, 'setup: the successor exists');

    assert.ok(
      isRoadAdjacent(s1, successor) && isRoadConnected(s1, successor),
      `F2: the pass demolished 5 online fire_post and built ${successor.spec} at (${successor.x},${successor.y}) ` +
        `which is road-adjacent=${isRoadAdjacent(s1, successor)} road-connected=${isRoadConnected(s1, successor)}. ` +
        `Fire cover for that district is now permanently ZERO and the pass log records no skip. ` +
        `Two independent holes: (a) neighbourhoodIds is built from the PRE-transaction audit so the successor ` +
        `is never in onlineBefore and is never rechecked on EITHER branch; (b) the connector was unaffordable so ` +
        `autoConnect returned the state unchanged, buildings.length did not move, and the skip-recheck predicate ` +
        `suppressed the AC-19 check entirely.`,
    );
  });

  test('the same transaction is SAFE when the connector is affordable — proving the fixture, not the harness, is what differs (RED-proof control)', () => {
    const s1 = reducer(fireFixture({ funds: 100_000_000 }), { type: 'tick' });
    const pass = lastPass(s1);
    const successor = s1.buildings.find((b) => b.id === pass.transactions[0].added[0].id);
    assert.ok(isRoadAdjacent(s1, successor) && isRoadConnected(s1, successor), 'control: with money, autoConnect connects it');
  });

  test('roads are never demolished by a pass (AC-20 exempt kinds) — the one thing that keeps the skip-recheck predicate sound for SURVIVORS', () => {
    const s0 = fireFixture({ funds: 100_000_000 });
    const roadsBefore = s0.buildings.filter((b) => SPECS[b.spec] && SPECS[b.spec].kind === 'road').map((b) => b.id).sort((a, b) => a - b);
    const s1 = reducer(s0, { type: 'tick' });
    const roadsAfter = new Set(s1.buildings.map((b) => b.id));
    for (const id of roadsBefore) assert.ok(roadsAfter.has(id), `road tile ${id} survived the pass`);
  });
});

// ---------------------------------------------------------------------------
// F3/F4 — UNDO
// ---------------------------------------------------------------------------

describe('ATTACK 3 — Undo: the safety net Aaron asked for', () => {
  test('is a no-op by reference identity on an empty log (AC-26)', () => {
    const s = mk({});
    assert.equal(reducer(s, { type: 'consolidatorUndo' }), s);
  });

  test('ONE pass then ONE undo leaves two buildings stacked on the same tile', () => {
    const s0 = fireFixture({ funds: 100_000_000 });
    assert.deepEqual(overlappingTiles(s0), [], 'setup: the fixture starts clean');
    const s1 = reducer(s0, { type: 'tick' });
    assert.deepEqual(overlappingTiles(s1), [], 'the pass ITSELF leaves the map consistent');

    const u = reducer(s1, { type: 'consolidatorUndo' });
    assert.deepEqual(
      overlappingTiles(u),
      [],
      'F3: Undo restores the demolished buildings by coordinate, but a consolidate transaction records ONLY the ' +
        'successor in `added` — the connector road tiles autoConnect laid for it are never recorded and never ' +
        'removed. The connector was routed across the tiles the demolition freed, so restoring the group stacks a ' +
        'fire_post on top of an rd_avenue. `fits`/occupiedSet/bulldoze all assume this cannot happen.',
    );
  });

  test('money and ids are reversed exactly on the parts Undo does handle', () => {
    const s1 = reducer(fireFixture({ funds: 100_000_000 }), { type: 'tick' });
    const pass = lastPass(s1);
    const fundsAfterPass = s1.funds;
    const capexAfterPass = s1.cumulativeCapexSpent ?? 0;
    const u = reducer(s1, { type: 'consolidatorUndo' });

    const net = pass.transactions.reduce((n, t) => n + t.netCost, 0);
    const gross = pass.transactions.reduce((n, t) => n + t.buildCost, 0);
    assert.equal(u.funds, fundsAfterPass + net, 'funds reversed exactly');
    assert.equal(u.cumulativeCapexSpent ?? 0, capexAfterPass - gross, 'capex reversed exactly');
    const ids = u.buildings.map((b) => b.id);
    assert.equal(new Set(ids).size, ids.length, 'no id collision after Undo');
    assert.ok(u.nextId > Math.max(...ids), 'nextId still above every surviving id');
    assert.equal((u.consolidatorLog ?? []).length, 0, 'the entry is popped');
  });

  test('Undo is NOT single-level: it walks back through the whole 20-entry ring, one pass per press', () => {
    // Ten fire_post in section 1: one transaction per section per pass, so a
    // second pass lands the next time that section is in scope (the month-12
    // whole-map pass at tick 330).
    const posts = [];
    for (let i = 0; i < 10; i++) posts.push({ id: 100 + i, spec: 'fire_post', x: 16 + i, y: 14, builtTick: -1000 });
    const headroom = [200, 210, 220, 230].map((x, i) => ({ id: 900 + i, spec: 'fire_station', x, y: 200, builtTick: -1000 }));
    let s = withConnectivity(
      mk({ buildings: [...roadRow(15, 40), ...posts, ...headroom], tick: TICKS_PER_MONTH - 1, consolidatorEnabled: true, nextId: 9000 }),
    );
    for (let i = 0; i < 340; i++) s = reducer(s, { type: 'tick' });
    assert.equal((s.consolidatorLog ?? []).length, 2, 'setup: two passes ran');
    assert.equal(s.buildings.filter((b) => b.spec === 'fire_post').length, 0, 'setup: both groups consolidated');

    const afterFirstUndo = reducer(s, { type: 'consolidatorUndo' });
    assert.equal(afterFirstUndo.buildings.filter((b) => b.spec === 'fire_post').length, 5, 'the newest pass is reversed');

    const afterSecondUndo = reducer(afterFirstUndo, { type: 'consolidatorUndo' });
    assert.equal(
      afterSecondUndo.buildings.filter((b) => b.spec === 'fire_post').length,
      5,
      'F4: AC-26/ASM-1502 say Undo is SINGLE-LEVEL — "once a later pass runs, the earlier one is no longer ' +
        'undoable". The implementation pops one entry per press off a 20-deep ring, so a second press reverses ' +
        'a pass from ELEVEN GAME MONTHS ago against a map that has moved on. Here that restores all ten posts ' +
        'and produces overlapping tiles.',
    );
  });
});

// ---------------------------------------------------------------------------
// Gates that HOLD — recorded so the report can say what survived
// ---------------------------------------------------------------------------

describe('ATTACK 4 — gates that hold', () => {
  test('a pass can never spend money the city does not have, and the refusal is on the record (AC-23)', () => {
    const s0 = fireFixture({ funds: NET_COST - 1 });
    const s1 = reducer(s0, { type: 'tick' });
    assert.equal(lastPass(s1).transactions.length, 0, 'refused one pound short');
    assert.ok(lastPass(s1).skipped.some((k) => k.reason === 'insufficient funds'), 'and says why');
    assert.ok(s1.funds > INSOLVENCY_WARNING_THRESHOLD, 'nowhere near the floor');
    // RED-proof: exactly netCost and it commits — the boundary is where it claims.
    const s2 = reducer(fireFixture({ funds: NET_COST }), { type: 'tick' });
    assert.equal(lastPass(s2).transactions.length, 1, 'RED-proof: one pound more and the same fixture acts');

    // OBSERVATION (not a defect, recorded for the report): because
    // INSOLVENCY_WARNING_THRESHOLD is NEGATIVE (-750,000) and the density path
    // gates on `cur.funds < netCost` FIRST, the second gate
    // `cur.funds - netCost < INSOLVENCY_WARNING_THRESHOLD` is unreachable on
    // that path. The consolidator is therefore STRICTER than ASM-1501 asks —
    // it will not use the overdraft at all. The floor gate is live only on the
    // reconnect path, where autoConnect can spend after the check.
    assert.ok(INSOLVENCY_WARNING_THRESHOLD < 0, 'the floor is an overdraft, so the affordability gate dominates it');
  });

  test('nothing happens on a non-boundary tick, and nothing happens while disabled', () => {
    const off = { ...fireFixture(), consolidatorEnabled: false };
    let s = off;
    for (let i = 0; i < 3 * TICKS_PER_MONTH; i++) s = reducer(s, { type: 'tick' });
    assert.equal(s.buildings.filter((b) => b.spec === 'fire_post').length, 5, 'disabled: not one building touched');
    assert.equal((s.consolidatorLog ?? []).length, 0);

    const mid = reducer({ ...fireFixture(), tick: 5 }, { type: 'tick' });
    assert.equal((mid.consolidatorLog ?? []).length, 0, 'no pass off the monthly boundary');
  });

  test('a mid-month disable stops the next boundary dead', () => {
    let s = fireFixture();
    s = reducer(s, { type: 'toggleConsolidator' }); // -> off
    s = reducer(s, { type: 'tick' }); // the boundary
    assert.equal((s.consolidatorLog ?? []).length, 0);
    assert.equal(s.buildings.filter((b) => b.spec === 'fire_post').length, 5);
  });

  test('the pass is a pure function of state: identical states and a SHUFFLED buildings array give identical transactions', () => {
    const a = fireFixture({ funds: 100_000_000 });
    const b = fireFixture({ funds: 100_000_000 });
    const shuffled = withConnectivity({ ...a, buildings: [...a.buildings].reverse() });

    const ra = lastPass(reducer(a, { type: 'tick' }));
    const rb = lastPass(reducer(b, { type: 'tick' }));
    const rs = lastPass(reducer(shuffled, { type: 'tick' }));
    assert.deepEqual(rb, ra, 'two identical states -> identical pass');
    assert.deepEqual(rs, ra, 'shuffled buildings order -> identical pass (GR#21)');
  });

  test('GR#21: the mutation lane contains no clock, no randomness, no storage, and no map-range-with-break', () => {
    const here = path.dirname(fileURLToPath(import.meta.url));
    const src = fs.readFileSync(path.join(here, '..', 'src', 'sim', 'engine.ts'), 'utf8');
    const start = src.indexOf('function applyConsolidatorPass');
    const end = src.indexOf('function advance(s: SimState)');
    assert.ok(start > 0 && end > start, 'located the mutation lane');
    const lane = src.slice(start, end);
    for (const banned of ['Date.now', 'new Date', 'Math.random', 'localStorage', 'performance.now']) {
      assert.ok(!lane.includes(banned), `${banned} must never appear in the tick path`);
    }
    // The only Map iteration in the lane is cityFamilyCapacity's order-independent sum.
    const mapRanges = lane.split('\n').filter((l) => l.includes('.values()') || l.includes('.entries()'));
    assert.equal(mapRanges.length, 1, 'exactly one Map iteration, and it is a fold');
    assert.ok(!lane.slice(lane.indexOf(mapRanges[0])).slice(0, 200).includes('break'), 'no early break out of a Map range');
  });

  test('the per-pass transaction cap is honoured, and skipped candidates are reported honestly (AC-38)', () => {
    // 5 sections x 5 posts: 5 candidate transactions, cap is 4.
    const posts = [];
    let id = 100;
    // twelfth-1 sections that share the tick-30 scope: use the whole-map month
    // (tick 330) so every section is in scope at once.
    for (let s = 0; s < 5; s++) for (let i = 0; i < 5; i++) posts.push({ id: id++, spec: 'fire_post', x: 16 * (s + 1) + i, y: 14, builtTick: -1000 });
    const headroom = [];
    for (let i = 0; i < 10; i++) headroom.push({ id: 900 + i, spec: 'fire_station', x: 200 + i * 3, y: 200, builtTick: -1000 });
    const s0 = withConnectivity(
      mk({ buildings: [...roadRow(15, 120), ...posts, ...headroom], tick: 11 * TICKS_PER_MONTH - 1, consolidatorEnabled: true, nextId: 9000, funds: 1_000_000_000 }),
    );
    assert.equal(monthlyScopeOf(11 * TICKS_PER_MONTH).full, true, 'setup: the month-12 whole-map pass');
    const s1 = reducer(s0, { type: 'tick' });
    const pass = lastPass(s1);
    assert.ok(pass.transactions.length <= 4, `cap honoured (${pass.transactions.length})`);
    assert.ok(pass.skipped.some((k) => k.reason === 'action budget'), 'the 5th is reported, not silently dropped');
  });

  test('the pass log is a ring of 20 and the 21st pass evicts honestly (AC-25)', () => {
    // Drive 22 boundaries with a state that always has SOMETHING to report and
    // never mutates the map: the successor spec is NOT unlocked, so every pass
    // logs a `skipped: 'not unlocked'` row. (Funds stay huge, so the city
    // cannot slide into decline and stop ticking part-way through the run —
    // which is exactly what a funds-starved variant of this fixture did.)
    let s = { ...fireFixture({ funds: 1_000_000_000 }), unlockedAll: false, level: 1 };
    const seen = [];
    for (let m = 0; m < 22; m++) {
      // section 1 is in scope at tick 30 and at the whole-map month; simply
      // re-arm the same boundary each time by rewinding the tick.
      s = { ...s, tick: TICKS_PER_MONTH - 1 };
      s = reducer(s, { type: 'tick' });
      const p = lastPass(s);
      if (p) seen.push(p.id);
    }
    const log = s.consolidatorLog ?? [];
    assert.ok(log.length <= CONSOLIDATOR_LOG_CAP, `ring capped at ${CONSOLIDATOR_LOG_CAP}, saw ${log.length}`);
    assert.equal(log.length, CONSOLIDATOR_LOG_CAP, 'the ring is full');
    assert.equal(log[0].id, seen[seen.length - 1], 'newest-first');
    assert.equal(log[0].id, 22, 'ids are monotonic across the whole run, never reused');
    assert.ok(log[log.length - 1].id > seen[0], 'the two oldest entries were evicted, not overwritten in place');
    assert.ok(log.every((p) => p.skipped.length > 0 && p.transactions.length === 0), 'the unlock gate held on every pass (AC-8 rule 6)');
  });

  test('provenance: consolidator-created buildings are tagged auto, restored player buildings keep player (AC-21)', () => {
    const s1 = reducer(fireFixture({ funds: 100_000_000 }), { type: 'tick' });
    const pass = lastPass(s1);
    const successor = s1.buildings.find((b) => b.id === pass.transactions[0].added[0].id);
    assert.equal(successor.placedBy, 'auto');
    for (const r of pass.transactions[0].removed) assert.equal(r.placedBy, 'player', 'an absent placedBy reads as player (GR#16)');
    const u = reducer(s1, { type: 'consolidatorUndo' });
    for (const b of u.buildings.filter((b) => b.spec === 'fire_post')) assert.equal(b.placedBy, 'player');
  });
});

// ---------------------------------------------------------------------------
// F5 — PERF on Aaron's real city (local-only input; skipped elsewhere)
// ---------------------------------------------------------------------------

const REAL_SAVE = String.raw`C:\Users\aarongarcia\.claude\jobs\f9ac9353\tmp\aaron-49k.lz`;
// The estate's own claim, from the build report: "~400-900ms/pass on the real
// 49k save (down from 1.3s)". This bound is that claim's UPPER end, applied to
// the ADDED cost (enabled minus disabled) so ordinary tick cost is not counted
// against it. ASM-1510's own budget is far tighter still (40ms).
const CLAIMED_ADDED_MS = 900;

describe('ATTACK 5 — perf on the real 49,174-building city', () => {
  test('the added boundary-tick cost of an enabled pass', (t) => {
    if (!fs.existsSync(REAL_SAVE)) {
      t.skip(`${REAL_SAVE} is a local job-workspace file, not a CI input`);
      return;
    }
    const parsed = JSON.parse(decode(fs.readFileSync(REAL_SAVE, 'utf8')));
    const real = parsed.snapshot ?? parsed;
    // tick 5369 -> 5370: a month boundary AND the month-12 whole-map pass for
    // this save's own tick line, so builtTick-based gates read true values.
    const prep = (enabled) => {
      const s = { ...real, tick: 5369, consolidatorEnabled: enabled, consolidatorLog: [] };
      return { ...s, roadConnectivity: computeRoadConnectivity(s) };
    };
    const median = (enabled) => {
      const ts = [];
      for (let i = 0; i < 7; i++) {
        const b = prep(enabled);
        const t0 = performance.now();
        reducer(b, { type: 'tick' });
        ts.push(performance.now() - t0);
      }
      ts.sort((a, b) => a - b);
      return ts[3];
    };
    const off = median(false);
    const on = median(true);
    const added = on - off;
    const sample = reducer(prep(true), { type: 'tick' });
    const pass = lastPass(sample);
    t.diagnostic(
      `real save: ${real.buildings.length} buildings; boundary tick disabled ${off.toFixed(0)}ms, ` +
        `enabled ${on.toFixed(0)}ms, added ${added.toFixed(0)}ms; ${pass.transactions.length} transactions, ` +
        `${pass.skipped.length} skipped`,
    );
    // LANDING DECISION (lead, 2026-09-04, per the r3 round's own disclosure):
    // the ~1.5-2.1s monthly-boundary stall is a KNOWN, DISCLOSED residual of
    // the monthly-twelfth cadence, accepted for landing because Aaron ruled
    // the consolidator watchable now and GLIDE MODE (inc2, in build) is the
    // structural supersession — one section per game DAY spreads the same
    // work ~30x thinner. This assertion therefore bounds a REGRESSION (4x
    // the measured 1,854ms reality), not the aspiration: if a future change
    // makes the monthly pass materially worse it still reds, while the known
    // stall is measured and logged loudly above on every run. ASM-1510's
    // 40ms budget transfers to the glide-windowed pass in inc2.
    const REGRESSION_CEILING_MS = 7500; // ~4x the measured 1,854ms median
    assert.ok(
      added <= REGRESSION_CEILING_MS,
      `F5 REGRESSION: the pass adds ${added.toFixed(0)}ms to one boundary tick — past the ${REGRESSION_CEILING_MS}ms ` +
        `ceiling (4x the 1,854ms measured at landing). The monthly-cadence stall is known and glide-inc2-gated; ` +
        `exceeding THIS bound means something got worse.`,
    );
  });
});
