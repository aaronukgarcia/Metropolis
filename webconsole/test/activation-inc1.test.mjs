// activation-inc1.test.mjs — FEAT-1972079891 inc1: BUILDING ACTIVATION, road gates.
//
// Scope: the ROAD-connectivity gates only (G1 construction [existing], G2 road-adjacent,
// G3 road-connected). The water/power/worker gates (G4/G5/G6) are DEFERRED (per Aaron —
// the city-wide-coverage-as-a-per-building-gate cliff), so this file never asserts them.
//
// Determinism is the crux (GR#21, AC-8): connectivity + gates are PURE functions of
// SimState — no Date/Math.random — so the same state yields the same online set and the
// same connectedRoadTiles, and a genesis replay reproduces the online set byte-identically.
//
// RED proof (scratch cp/mv, NEVER git): break computeRoadConnectivity's seeding (drop the
// map-edge seed) and the flood-fill test goes RED; break the adjacency loop (skip the
// ortho neighbours) and the adjacency/gate tests go RED. Restore data.ts → GREEN.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  SPECS,
  isOnline,
  isRoadAdjacent,
  isRoadConnected,
  computeRoadConnectivity,
  computeFailedGates,
  onlineResidentsCapacity,
} from '../src/sim/data.ts';
import { initialState, reducer, computeFlows } from '../src/sim/engine.ts';
import { replayFromGenesis, stableStringify } from '../src/sim/genesisReplay.ts';
import { emptyJournal, recordAction } from '../src/sim/journal.ts';

const key = (x, y) => `${x},${y}`;
const roadAt = (x, y) => ({ id: 1_000_000 + x * 1000 + y, spec: 'road', x, y, builtTick: -1000 });

// Clean board (no starter city), with roadConnectivity computed for the given buildings.
function mk(over) {
  const base = initialState();
  const s = {
    ...base,
    unlockedAll: true,
    roadNotice: null,
    roadMonitors: [],
    buildings: [],
    population: 0,
    funds: 10_000_000,
    tick: 0,
    ...over,
  };
  return { ...s, roadConnectivity: computeRoadConnectivity(s) };
}

// Recompute the connectivity graph after mutating buildings/tick.
const withConn = (s) => ({ ...s, roadConnectivity: computeRoadConnectivity(s) });

// ===================== (1) AC-1 flood-fill =====================

test('AC-1 flood-fill: only the edge-connected road segment is in connectedRoadTiles', () => {
  const s = mk({
    buildings: [
      // Segment A — touches the west map edge (x=0) → CONNECTED.
      roadAt(0, 5),
      roadAt(1, 5),
      roadAt(2, 5),
      // Segment B — isolated island → NOT connected.
      roadAt(100, 100),
      roadAt(101, 100),
      roadAt(102, 100),
      // Segment C — a second isolated island → NOT connected.
      roadAt(200, 200),
      roadAt(201, 200),
    ],
  });
  const conn = computeRoadConnectivity(s);
  assert.deepEqual(
    conn.connectedRoadTiles,
    [key(0, 5), key(1, 5), key(2, 5)],
    'exactly the three edge-connected tiles — no isolated segment leaks in'
  );
});

// ===================== (2) AC-2 adjacency =====================

test('AC-2 adjacency: orthogonal road → true; two away → false; diagonal → false', () => {
  const s = mk({ buildings: [roadAt(5, 5)] });
  // res_hut is 1×1. Orthogonally adjacent (directly below the road).
  const adj = { id: 1, spec: 'res_hut', x: 5, y: 4, builtTick: -1000 };
  const far = { id: 2, spec: 'res_hut', x: 5, y: 7, builtTick: -1000 }; // 2 tiles clear
  const diag = { id: 3, spec: 'res_hut', x: 6, y: 6, builtTick: -1000 }; // diagonal only
  assert.equal(isRoadAdjacent(s, adj), true, 'orthogonal neighbour of a road → road-side');
  assert.equal(isRoadAdjacent(s, far), false, 'two tiles from the road → not road-side');
  assert.equal(isRoadAdjacent(s, diag), false, 'diagonal-only touch does NOT count');
});

// ===================== (3) AC-3 connectivity gate =====================

test('AC-3 gate: building by a connected road is online; by an isolated road is offline', () => {
  const connectedHouse = { id: 10, spec: 'res_hut', x: 1, y: 49, builtTick: -1000 };
  const isolatedHouse = { id: 11, spec: 'res_hut', x: 200, y: 49, builtTick: -1000 };
  const s = mk({
    buildings: [
      // Connected road reaching the west edge at (0,50).
      roadAt(0, 50),
      roadAt(1, 50),
      // Isolated road segment.
      roadAt(200, 50),
      roadAt(201, 50),
      connectedHouse,
      isolatedHouse,
    ],
  });
  // Both are road-adjacent...
  assert.equal(isRoadAdjacent(s, connectedHouse), true);
  assert.equal(isRoadAdjacent(s, isolatedHouse), true);
  // ...but only the one beside the connected road is road-connected + online.
  assert.equal(isRoadConnected(s, connectedHouse), true, 'adjacent road is in the network');
  assert.equal(isRoadConnected(s, isolatedHouse), false, 'adjacent road is an island');
  assert.equal(isOnline(s, connectedHouse), true, 'connected building is ONLINE');
  assert.equal(isOnline(s, isolatedHouse), false, 'disconnected building is OFFLINE');
});

// ===================== (4) AC-4 road gates: cut & reconnect =====================

test('AC-4 road gates: all met → online; cut the connecting road → offline; reconnect → online', () => {
  const house = { id: 10, spec: 'res_hut', x: 1, y: 49, builtTick: -1000 };
  const roads = [roadAt(0, 50), roadAt(1, 50)];
  let s = mk({ buildings: [...roads, house] });
  assert.equal(isOnline(s, house), true, 'prereqs met → online');

  // Cut: demolish the road tile the house sits beside (1,50).
  s = withConn({ ...s, buildings: s.buildings.filter((b) => !(b.x === 1 && b.y === 50)) });
  assert.equal(isOnline(s, house), false, 'road cut → offline in the same evaluation');

  // Reconnect: lay the tile back.
  s = withConn({ ...s, buildings: [...s.buildings, roadAt(1, 50)] });
  assert.equal(isOnline(s, house), true, 'road restored → online again');
});

// ===================== (5) AC-12 instant re-evaluation (same tick) =====================

test('AC-12: placing the connecting road re-evaluates in the SAME action (no delay tick)', () => {
  // House beside an ISOLATED road island at (1,100) → offline.
  const house = { id: 10, spec: 'res_hut', x: 1, y: 99, builtTick: -1000 };
  let s = mk({ buildings: [roadAt(1, 100), house] });
  assert.equal(isOnline(s, house), false, 'starts offline — the island road is not connected');

  // Place a single road tile at the map edge (0,100), bridging the island to the
  // network. No `tick` — the reducer refreshes connectivity for the place action.
  const tickBefore = s.tick;
  s = reducer(s, { type: 'place', spec: 'road', x: 0, y: 100 });
  assert.equal(s.tick, tickBefore, 'no tick elapsed (place is a between-tick action)');
  assert.equal(isOnline(s, house), true, 'house comes online in the same tick the road is placed');
});

// ===================== (6) AC-8 determinism + genesis replay =====================

test('AC-8 determinism: identical state → identical online set + connectedRoadTiles', () => {
  const build = () =>
    mk({
      buildings: [
        roadAt(0, 50),
        roadAt(1, 50),
        { id: 10, spec: 'res_hut', x: 1, y: 49, builtTick: -1000 },
        { id: 11, spec: 'res_hut', x: 200, y: 49, builtTick: -1000 },
      ],
    });
  const a = build();
  const b = build();
  assert.deepEqual(
    computeRoadConnectivity(a).connectedRoadTiles,
    computeRoadConnectivity(b).connectedRoadTiles,
    'connectedRoadTiles is deterministic'
  );
  const onlineSet = (s) => s.buildings.filter((x) => isOnline(s, x)).map((x) => x.id).sort();
  assert.deepEqual(onlineSet(a), onlineSet(b), 'online set is deterministic');
});

test('AC-8 replay: replayFromGenesis reproduces the online set byte-identically', () => {
  // Drive a real journal from genesis (mirrors the store: record @ pre-dispatch tick).
  const actions = [
    // Place a house next to the starter-city road grid (x=150 column) — auto-connect
    // wires it to the connected network, so it activates.
    { type: 'place', spec: 'res_hut', x: 151, y: 60 },
    { type: 'tick' },
    { type: 'tick' },
    { type: 'tick' },
    { type: 'tick' },
  ];
  let journal = emptyJournal();
  let live = initialState();
  for (const action of actions) {
    journal = recordAction(journal, live.tick, action);
    live = reducer(live, action);
  }
  const replayed = replayFromGenesis(journal);

  const onlineSet = (s) => s.buildings.filter((x) => isOnline(s, x)).map((x) => x.id).sort();
  assert.deepEqual(onlineSet(replayed), onlineSet(live), 'replay reproduces the exact online set');
  assert.deepEqual(
    replayed.roadConnectivity.connectedRoadTiles,
    live.roadConnectivity.connectedRoadTiles,
    'replay reproduces the exact connectivity graph'
  );
  // Whole-state byte-identity (the AC-8 oracle) — covers online status + economy.
  assert.equal(stableStringify(replayed), stableStringify(live), 'replay is byte-identical to live');
  // The placed house is actually ONLINE (otherwise this passes vacuously).
  const house = live.buildings.find((b) => b.spec === 'res_hut');
  assert.ok(house && isOnline(live, house), 'the auto-connected house is online (non-vacuous)');
});

// ===================== (7) AC-14 offline → zero economy =====================

test('AC-14: an offline (road-disconnected) building contributes ZERO upkeep + capacity', () => {
  // res_block: 60 residents, upkeep 6, category zones. Population 0 so wages don't
  // confound the upkeep comparison. Both states hold EXACTLY ONE road (same road
  // upkeep) — the only difference is whether that road is connected, so the whole
  // economy delta is attributable to the block going offline.
  const block = (x, y) => ({ id: 10, spec: 'res_block', x, y, builtTick: -1000 });

  // ONLINE: road at the west edge (connected); block sits beside it.
  const online = mk({ buildings: [roadAt(0, 50), block(0, 48)] });
  // OFFLINE: an isolated road island (block is road-adjacent but not connected).
  const offline = mk({ buildings: [roadAt(5, 50), block(5, 48)] });

  const onlineBlock = online.buildings.find((b) => b.spec === 'res_block');
  const offlineBlock = offline.buildings.find((b) => b.spec === 'res_block');
  assert.equal(isOnline(online, onlineBlock), true, 'sanity: online building is online');
  assert.equal(isOnline(offline, offlineBlock), false, 'sanity: offline building is offline');

  // Capacity (residents) — offline contributes ZERO (income/growth basis is gated too).
  assert.equal(onlineResidentsCapacity(online), 60, 'online block houses 60');
  assert.equal(onlineResidentsCapacity(offline), 0, 'offline block houses NOBODY');

  // Upkeep (outflows): both states pay the single road's upkeep; ONLY the online
  // state additionally pays the block's upkeep. The delta is exactly the block upkeep.
  const upkeepTotal = (s) => computeFlows(s).outflows.reduce((a, f) => a + f.value, 0);
  const onlineUp = upkeepTotal(online);
  const offlineUp = upkeepTotal(offline);
  assert.equal(offlineUp, SPECS.road.upkeep, 'offline: only the road upkeep, NOT the block');
  assert.equal(
    onlineUp - offlineUp,
    SPECS.res_block.upkeep,
    'online adds exactly the block upkeep — the offline block draws ZERO'
  );
});

// ===================== (8) DD4 immediate re-evaluation (Aaron, 2026-08-28, Option C) =====================

test('DD4=C: loaded state re-evaluates disconnected buildings immediately (no grace)', () => {
  // No roads at all → the house can never be road-connected. On load, it is
  // evaluated immediately against the gates, not given a grace period.
  const house = { id: 10, spec: 'res_hut', x: 50, y: 50, builtTick: -1000 };

  // After loading a state with this disconnected house, it should be OFFLINE immediately.
  const loaded = mk({ buildings: [house], tick: 0 });
  assert.equal(isOnline(loaded, loaded.buildings[0]), false, 'loaded disconnected building is OFFLINE immediately');

  // The WHY tooltip explains why it failed.
  const reasons = computeFailedGates(loaded, loaded.buildings[0]).map((g) => g.gate);
  assert.deepEqual(reasons, ['road-adjacent'], 'reason is the road gate');
});
