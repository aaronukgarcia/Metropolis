// bug394-offline-housing.test.mjs — BUG-394 legibility: split the OFFLINE
// residential capacity by ROOT CAUSE so the pop-freeze is legible.
//
// underConstructionResidents(s) = gross − online = the whole "+N" of residential
// capacity that is offline. That N mixes TWO causes: dwellings still being built
// (G1 construction — self-resolves) and dwellings built but stranded off the road
// network (G2/G3 — needs the player to connect roads). offlineResidentsByReason
// attributes each offline dwelling's residents to `construction` or `disconnected`
// using the COMMITTED activation gate (computeFailedGates) — it does not
// reimplement or change the gate.
//
// Pure state -> value (GR#21). Each test can FAIL: swap the road-gate test for a
// constant and #1/#3 misclassify; drop the isOnline guard and #2 breaks; break
// the partition and the "sum == underConstructionResidents" assertion breaks.
//
// RED proof (scratch cp/mv, NEVER git): in offlineResidentsByReason, flip the
// `road ? disconnected : construction` branch → #1/#3 go RED. Restore → GREEN.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  SPECS,
  isOnline,
  computeRoadConnectivity,
  offlineResidentsByReason,
  underConstructionResidents,
} from '../src/sim/data.ts';
import { initialState } from '../src/sim/engine.ts';

const key = (x, y) => `${x},${y}`;
const HUT = SPECS.res_hut.residents ?? 8;
const roadAt = (x, y) => ({ id: 1_000_000 + x * 1000 + y, spec: 'road', x, y, builtTick: -1000 });

// Clean board (no starter city) with roadConnectivity computed for its buildings.
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

// A res_hut costs 220 → constructionTicks = max(3, round(220/1500)) = 3. Stamping
// builtTick == s.tick means 0 ticks elapsed (< 3) → still building.
const stillBuilding = (id, x, y, tick) => ({ id, spec: 'res_hut', x, y, builtTick: tick });
// builtTick far in the past → construction long finished; online status is then
// decided purely by the road gates.
const built = (id, x, y) => ({ id, spec: 'res_hut', x, y, builtTick: -1000 });

// ============================================================================
// #1 — mixed scene: construction vs disconnected split exactly; sum == total offline
// ============================================================================
test('BUG-394 #1: offlineResidentsByReason splits construction vs disconnected exactly, and the two sum to the total offline capacity', () => {
  const tick = 5;
  const s = mk({
    tick,
    buildings: [
      // Connected road reaching the west map edge at (0,50).
      roadAt(0, 50),
      roadAt(1, 50),
      // Isolated road island (never reaches an edge) far away.
      roadAt(200, 50),
      roadAt(201, 50),

      // ONLINE: built long ago, beside the connected road.
      built(10, 1, 49),

      // UNDER CONSTRUCTION: beside the CONNECTED road, but stamped this tick so
      // still building (G1). Attributed to `construction`.
      stillBuilding(20, 1, 51, tick),
      stillBuilding(21, 0, 49, tick),

      // DISCONNECTED: built, road-adjacent to the ISOLATED island → fails G3.
      // Attributed to `disconnected`.
      built(30, 200, 49),
      built(31, 201, 49),
      built(32, 200, 51),
    ],
  });

  // Sanity: the online one is online; the rest are offline.
  assert.equal(isOnline(s, s.buildings.find((b) => b.id === 10)), true, 'built+connected → online');
  for (const id of [20, 21, 30, 31, 32]) {
    assert.equal(isOnline(s, s.buildings.find((b) => b.id === id)), false, `id ${id} is offline`);
  }

  const { construction, disconnected } = offlineResidentsByReason(s);
  assert.equal(construction, 2 * HUT, 'exactly the 2 still-building dwellings');
  assert.equal(disconnected, 3 * HUT, 'exactly the 3 built-but-off-network dwellings');

  // Full attribution: no offline resident is lost or double-counted.
  assert.equal(
    construction + disconnected,
    underConstructionResidents(s),
    'construction + disconnected == total offline residential capacity (gross − online)'
  );
});

// ============================================================================
// #2 — all dwellings online → both buckets 0
// ============================================================================
test('BUG-394 #2: when every residential building is online, both reasons are 0', () => {
  const s = mk({
    tick: 100,
    buildings: [
      roadAt(0, 50),
      roadAt(1, 50),
      roadAt(2, 50),
      built(10, 1, 49),
      built(11, 2, 49),
    ],
  });
  for (const id of [10, 11]) {
    assert.equal(isOnline(s, s.buildings.find((b) => b.id === id)), true, `id ${id} online`);
  }
  const { construction, disconnected } = offlineResidentsByReason(s);
  assert.equal(construction, 0, 'nothing under construction');
  assert.equal(disconnected, 0, 'nothing disconnected');
  assert.equal(underConstructionResidents(s), 0, 'total offline is 0');
});

// ============================================================================
// #3 — a road-disconnected dwelling is `disconnected`, NOT `construction`; and a
//      still-building road-connected dwelling is `construction`, NOT `disconnected`
// ============================================================================
test('BUG-394 #3: disconnected-vs-construction attribution is by the actual failing gate', () => {
  const tick = 5;

  // Case A: built, road-adjacent to an ISOLATED road → disconnected.
  const a = mk({
    tick,
    buildings: [roadAt(200, 50), roadAt(201, 50), built(30, 200, 49)],
  });
  assert.equal(isOnline(a, a.buildings.find((b) => b.id === 30)), false, 'disconnected house offline');
  const ra = offlineResidentsByReason(a);
  assert.equal(ra.disconnected, HUT, 'road-disconnected dwelling → disconnected');
  assert.equal(ra.construction, 0, 'and NOT counted as construction');

  // Case B: still building, beside a CONNECTED road → construction.
  const b = mk({
    tick,
    buildings: [roadAt(0, 50), roadAt(1, 50), stillBuilding(20, 1, 49, tick)],
  });
  assert.equal(isOnline(b, b.buildings.find((x) => x.id === 20)), false, 'still-building house offline');
  const rb = offlineResidentsByReason(b);
  assert.equal(rb.construction, HUT, 'still-building road-connected dwelling → construction');
  assert.equal(rb.disconnected, 0, 'and NOT counted as disconnected');
});

// ============================================================================
// #4 — determinism (GR#21): same state → identical result across calls
// ============================================================================
test('BUG-394 #4: offlineResidentsByReason is deterministic', () => {
  const tick = 5;
  const s = mk({
    tick,
    buildings: [
      roadAt(0, 50),
      roadAt(1, 50),
      roadAt(200, 50),
      stillBuilding(20, 1, 49, tick),
      built(30, 200, 49),
    ],
  });
  const first = offlineResidentsByReason(s);
  for (let i = 0; i < 5; i++) {
    const r = offlineResidentsByReason(s);
    assert.deepEqual(r, first, 'identical result every call');
  }
  assert.equal(first.construction, HUT, 'stable construction figure');
  assert.equal(first.disconnected, HUT, 'stable disconnected figure');
});
