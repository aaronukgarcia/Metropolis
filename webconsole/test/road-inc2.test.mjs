// road-inc2.test.mjs — FEAT-1972079907 inc2: one-year traffic monitoring + auto-scale.
//
// Determinism is the crux (GR#21): the monthly monitor pass is a PURE function of
// (state, tick) — no Date/Math.random, monitors processed in strict (x,y) order —
// so the same board + same ticks yield byte-identical tier outcomes and ledger.
//
// RED proof (scratch cp/mv, NEVER git): flip the saturation comparison in
// evaluateRoadMonitors (`load >= threshold*cap` → `load > threshold*cap*10`) and the
// threshold/both-scale/conservation tests go RED; restore engine.ts → GREEN.
// Iterating `active` in map order without the (x,y) sort leaves outputs identical
// for distinct tiles but the "order-independent" test pins that guarantee.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  initialState,
  reducer,
  evaluateRoadMonitors,
  TICKS_PER_MONTH,
  TICKS_PER_YEAR,
  ROAD_SATURATION_THRESHOLD,
} from '../src/sim/engine.ts';
import {
  SPECS,
  roadTierOf,
  placementCost,
  ROAD_TIER_CAPACITY,
} from '../src/sim/data.ts';
import { runConsistencyChecks } from '../src/sim/consistency.ts';

// A clean state (no starter city) with an explicit building list + monitors.
function mk(over) {
  const base = initialState();
  return {
    ...base,
    unlockedAll: true,
    roadNotice: null,
    roadMonitors: [],
    buildings: [],
    population: 0,
    funds: 10_000_000,
    ...over,
  };
}

const tileAt = (s, x, y) => s.buildings.find((b) => b.x === x && b.y === y);

// A tier-1 lane at (10,10) fed by a heavy-industry source at (20,20). ind_heavy
// carries jobs:110 → traffic weight = jobs + freight = 220. At full activity that is
// well over 0.85 × tier-1 capacity (100), so the segment saturates.
const SOURCE = { id: 2, spec: 'ind_heavy', x: 20, y: 20, builtTick: -1000 };
const laneAt = (x, y) => ({ id: 100 + x * 1000 + y, spec: 'road', x, y, builtTick: -1000 });

// Sanity: the placeholder-balance numbers this file reasons about.
test('preconditions: the placeholder-balance constants are what the assertions assume', () => {
  assert.equal(TICKS_PER_YEAR, TICKS_PER_MONTH * 12, 'a year is 12 months of ticks');
  assert.equal(TICKS_PER_YEAR, 360, '360-tick year (matches utils.gameDate calendar)');
  assert.equal(ROAD_SATURATION_THRESHOLD, 0.85, 'saturation threshold placeholder');
  assert.equal(ROAD_TIER_CAPACITY[1], 100, 'tier-1 capacity placeholder');
});

// ===================== (1) load → upgrade threshold =====================

test('threshold: under saturation does NOT upgrade; pushed over DOES upgrade exactly one tier', () => {
  const buildings = [laneAt(10, 10), { ...SOURCE }];
  const monitors = [{ x: 10, y: 10, source: 2, until: TICKS_PER_YEAR }];

  // UNDER: low population → low activity (0.2) → load ≈ 44 < 0.85×100=85 → no upgrade.
  const under = reducer(
    mk({ buildings, roadMonitors: monitors, population: 100, tick: TICKS_PER_MONTH - 1 }),
    { type: 'tick' }
  );
  assert.equal(under.tick % TICKS_PER_MONTH, 0, 'landed on a monthly boundary');
  assert.equal(tileAt(under, 10, 10).spec, 'road', 'below threshold: lane stays tier 1');

  // OVER: full population → activity 1 → load 220 ≥ 85 → upgrade one tier (1→2).
  const over = reducer(
    mk({ buildings, roadMonitors: monitors, population: 500, tick: TICKS_PER_MONTH - 1 }),
    { type: 'tick' }
  );
  const t = tileAt(over, 10, 10);
  assert.equal(t.spec, 'rd_avenue', 'over threshold: lane auto-scaled to the avenue');
  assert.equal(roadTierOf(SPECS[t.spec]), 2, 'exactly ONE tier up (1 → 2), not a jump');
});

// ===================== (2) both-scale =====================

test('both-scale: connector AND joined road both scale when both saturate', () => {
  // (10,10) models the connector tile, (11,10) the joined road — same feeding source.
  const buildings = [laneAt(10, 10), laneAt(11, 10), { ...SOURCE }];
  const monitors = [
    { x: 10, y: 10, source: 2, until: TICKS_PER_YEAR },
    { x: 11, y: 10, source: 2, until: TICKS_PER_YEAR },
  ];
  const after = reducer(
    mk({ buildings, roadMonitors: monitors, population: 500, tick: TICKS_PER_MONTH - 1 }),
    { type: 'tick' }
  );
  assert.equal(tileAt(after, 10, 10).spec, 'rd_avenue', 'connector scaled');
  assert.equal(tileAt(after, 11, 10).spec, 'rd_avenue', 'joined road scaled');
});

// ===================== (3) cap at ladder max =====================

test('cap: a tier-5 saturated segment does not upgrade past tier 5', () => {
  const buildings = [{ id: 1, spec: 'm20', x: 10, y: 10, builtTick: -1000 }, { ...SOURCE }];
  const monitors = [{ x: 10, y: 10, source: 2, until: TICKS_PER_YEAR }];
  const after = reducer(
    mk({ buildings, roadMonitors: monitors, population: 500, tick: TICKS_PER_MONTH - 1 }),
    { type: 'tick' }
  );
  const t = tileAt(after, 10, 10);
  assert.equal(t.spec, 'm20', 'motorway (tier 5) is NOT upgraded past the ladder max');
  assert.equal(roadTierOf(SPECS[t.spec]), 5, 'still tier 5');
});

// ===================== (4) deterministic over the window =====================

test('deterministic: the same monitored scenario twice → byte-identical tiers + ledger', () => {
  // A sustained city: res_highrise keeps population near capacity, so the ind_heavy
  // monitor genuinely saturates and scales across the window (not a no-op run).
  // DD4=C (Aaron, 2026-08-28): no grace period — the building must be connected to
  // the road network to stay online. Road at (10,10) is connected to the map edge
  // via (0,10) so the entire segment is part of the connected network.
  //
  // FEAT-1972079919: the bi-monthly orphan-connect sweep fires at tick 60 and upgrades
  // junction roads to match the connector tier. To isolate the monitor's scaling behavior
  // from the sweep's junction upgrades, SOURCE is placed adjacent to the network (8,7)
  // so that the sweep finds it already connected and does not try to auto-connect it.
  // This preserves the test's focus on monitor determinism without adding extraneous
  // road tiles or hiding the sweep's effect.
  const scenario = () =>
    mk({
      buildings: [
        // Connect the monitored road to the network edge via map-edge road at (0,10).
        laneAt(0, 10),
        laneAt(1, 10),
        laneAt(2, 10),
        laneAt(3, 10),
        laneAt(4, 10),
        laneAt(5, 10),
        laneAt(6, 10),
        laneAt(7, 10),
        laneAt(8, 10),
        laneAt(9, 10),
        laneAt(10, 10),
        // SOURCE placed adjacent to network roads (at 8,7 is adjacent to roads at 8,10)
        // so that the tick-60 orphan-connect sweep finds it already connected.
        { id: 2, spec: 'ind_heavy', x: 8, y: 7, builtTick: -1000 },
        // res_highrise positioned at (10,11), adjacent to the connected road at (10,10),
        // so it stays online and maintains the population for this traffic-scaling test.
        { id: 3, spec: 'res_highrise', x: 10, y: 11, builtTick: -1000 },
      ],
      roadMonitors: [{ x: 10, y: 10, source: 2, until: TICKS_PER_YEAR }],
      population: 500,
      tick: 0,
    });

  let a = scenario();
  let b = scenario();
  for (let i = 0; i < 2 * TICKS_PER_MONTH; i++) {
    a = reducer(a, { type: 'tick' });
    b = reducer(b, { type: 'tick' });
  }
  // Something actually scaled — otherwise this would pass vacuously. Two monthly
  // evals in the window (ticks 30 and 60) drive the saturated lane 1 → 2 → 3.
  assert.equal(tileAt(a, 10, 10).spec, 'rd_aroad', 'the monitored lane scaled twice during the window');

  const fingerprint = (s) =>
    JSON.stringify({ buildings: s.buildings, ledger: s.ledger, funds: s.funds, monitors: s.roadMonitors });
  assert.equal(fingerprint(a), fingerprint(b), 'two independent runs are byte-identical');
});

test('order-independent: monitors in reversed input order upgrade the identical tile set (GR#21)', () => {
  const s = mk({
    buildings: [laneAt(10, 10), laneAt(11, 10), laneAt(12, 10), { ...SOURCE }],
    roadMonitors: [],
    population: 500,
  });
  const forward = [
    { x: 10, y: 10, source: 2, until: TICKS_PER_YEAR },
    { x: 11, y: 10, source: 2, until: TICKS_PER_YEAR },
    { x: 12, y: 10, source: 2, until: TICKS_PER_YEAR },
  ];
  const reversed = [...forward].reverse();
  const r1 = evaluateRoadMonitors({ ...s, roadMonitors: forward }, TICKS_PER_MONTH);
  const r2 = evaluateRoadMonitors({ ...s, roadMonitors: reversed }, TICKS_PER_MONTH);
  assert.deepEqual(
    r1.buildings.map((b) => [b.x, b.y, b.spec]).sort(),
    r2.buildings.map((b) => [b.x, b.y, b.spec]).sort(),
    'input monitor order must not change which tiles scale'
  );
  assert.equal(r1.cost, r2.cost, 'identical spend regardless of monitor order');
  assert.equal(r1.upgraded, 3, 'all three saturated lanes scaled');
});

// ===================== (5) monitoring expiry =====================

test('expiry: after the 1-year window a later saturation does NOT trigger an upgrade', () => {
  // FEAT-1972079919: the bi-monthly orphan-connect sweep fires at tick 60, same as the
  // monitor evaluation. To isolate the monitor's expiry behavior from the sweep's effects,
  // SOURCE is placed adjacent to the network at (8, 7), so that its footprint [8,11)×[7,10)
  // includes cell (10,9) which is orthogonally adjacent to the road at (10,10). The sweep
  // then finds SOURCE already connected and skips the autoConnect step, avoiding its
  // junction-upgrade side effects.
  const buildings = [laneAt(10, 10), { id: 2, spec: 'ind_heavy', x: 8, y: 7, builtTick: -1000 }];

  // CONTROL — window still open at the eval tick (until 60, eval at tick 60): upgrades.
  const openTick = 2 * TICKS_PER_MONTH; // 60
  const ctrl = reducer(
    mk({
      buildings,
      roadMonitors: [{ x: 10, y: 10, source: 2, until: openTick }],
      population: 500,
      tick: openTick - 1,
    }),
    { type: 'tick' }
  );
  assert.equal(tileAt(ctrl, 10, 10).spec, 'rd_avenue', 'in-window saturated segment upgrades (control)');

  // EXPIRED — window closed one month before the eval tick: no upgrade, monitor dropped.
  const expired = reducer(
    mk({
      buildings,
      roadMonitors: [{ x: 10, y: 10, source: 2, until: TICKS_PER_MONTH }], // until 30, eval at 60
      population: 500,
      tick: openTick - 1,
    }),
    { type: 'tick' }
  );
  assert.equal(tileAt(expired, 10, 10).spec, 'road', 'expired monitor does NOT upgrade the saturated lane');
  // BUG-440 regression: on a tick that is both monthly (monitor eval) AND sweep (tick % 60),
  // the sweep branch used to re-read s.roadMonitors and clobber the eval's filtered list,
  // resurrecting expired monitors. The monthly rebind must carry the filtered monitors so
  // the sweep branch reads the post-eval list.
  assert.equal(expired.roadMonitors.length, 0, 'the expired monitor is dropped from state (BUG-440)');
});

// ===================== (6) conservation across an auto-scale event =====================

test('conservation: an auto-scale event books the tier-delta through the ledger and conserves money', () => {
  const buildings = [laneAt(10, 10), { ...SOURCE }];
  const monitors = [{ x: 10, y: 10, source: 2, until: TICKS_PER_YEAR }];
  const before = mk({ buildings, roadMonitors: monitors, population: 500, tick: TICKS_PER_MONTH - 1 });
  const after = reducer(before, { type: 'tick' });

  // The upgrade happened.
  assert.equal(tileAt(after, 10, 10).spec, 'rd_avenue', 'lane auto-scaled 1 → 2');

  // The cost booked is exactly the placement-cost DELTA to the higher tier.
  const expectedCost = placementCost(SPECS.rd_avenue) - placementCost(SPECS.road); // 90 - 40 = 50
  assert.ok(expectedCost > 0, 'precondition: the upgrade has a positive cost');

  const ledgerEntry = after.ledger.find((e) => e.label.startsWith('Auto-scaled'));
  assert.ok(ledgerEntry, 'an "Auto-scaled …" ledger entry was recorded');
  assert.equal(ledgerEntry.amount, -expectedCost, 'the ledger books the negative tier-delta cost');

  const outflow = after.lastFlows.outflows.find((f) => f.label === 'Road Auto-Scale');
  assert.ok(outflow, 'the spend is recorded as a "Road Auto-Scale" outflow (counts for conservation)');
  assert.equal(outflow.value, expectedCost, 'outflow value == the booked upgrade cost');

  // The tick-boundary conservation invariant holds across the event.
  const report = runConsistencyChecks(after);
  const conservation = report.checks.find((c) => c.id === 'conservation.funds-vs-flows');
  assert.equal(conservation.ok, true, `conservation holds: ${conservation.detail}`);

  // And upkeep reconciliation still agrees despite the mid-tick tier swap (the whole
  // tick was computed on the post-upgrade roads, so recorded == recomputed).
  const upkeep = report.checks.find((c) => c.id === 'flows.upkeep-total-matches');
  assert.equal(upkeep.ok, true, `upkeep reconciles after the tier swap: ${upkeep.detail}`);
});

// ===================== registration through the real place path =====================

test('registration: auto-connect registers monitors for the connector + joined road for one year', () => {
  // Real starter city: placing near the M20 lays a connector, which registers monitors.
  const s0 = initialState();
  const s1 = reducer(s0, { type: 'place', spec: 'res_hut', x: 50, y: 50 });
  assert.equal(s1.roadNotice, null, 'connected to the network');
  assert.ok(s1.roadMonitors.length > 0, 'monitors registered for the laid connector + junction');
  const placed = s1.buildings.find((b) => b.spec === 'res_hut' && b.x === 50 && b.y === 50);
  for (const m of s1.roadMonitors) {
    assert.equal(m.until, s0.tick + TICKS_PER_YEAR, 'each monitor watches for exactly one year');
    assert.equal(m.source, placed.id, 'the placed building is the feeding source');
  }
});
