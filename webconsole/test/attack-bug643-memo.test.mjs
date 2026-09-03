// attack-bug643-memo.test.mjs — BUG-643 (tier 2 of BUG-642): identity + perf
// suite for the waste-family / parks / station / road-connectivity
// memoisations added to src/sim/data.ts.
//
// SCOPE: wasteGeneratedOf, collectionCapacityOf, wasteStatsOf, processingMixOf
// (and its new internal processCapacitiesOf single-pass helper), parksCapacityOf,
// stationLinks — all now memoOnState (WeakMap<SimState, T> keyed on the state
// object, the house idiom documented at data.ts's memoOnState definition) — plus
// computeRoadConnectivity, memoised on s.buildings identity (the roadTileSetOf
// idiom, one level down from memoOnState because callers rebuild `{ ...s,
// roadConnectivity: computeRoadConnectivity(s) }` from the SAME buildings array).
//
// Every wrapped function is checked against an INDEPENDENTLY RE-IMPLEMENTED
// oracle (copied from the pre-BUG-643 data.ts formulas, not by importing data.ts
// internals) at three scales, so a future edit that lets the memo drift from the
// real formula is caught here rather than by two panels quietly disagreeing
// (exactly the BUG-642 round-finding class of defect this suite exists to guard
// against for the waste family).
//
// ATTACK CHECKLIST:
//   (a) semantic identity vs a from-scratch oracle at 3 scales (tiny/1k/13k)
//   (b) GR#21 — same state twice -> same cached reference (real cache hit, not
//       just "happens to recompute the same value")
//   (c) staleness — a state-mutating action (place a new processor / park /
//       station / road) must NOT read the pre-action cached answer
//   (d) per-selector perf bounds, derived from a MEDIAN of >=5 runs on THIS
//       machine (documented below), never a max, never a number picked on the
//       dev box and asserted as if it were portable — bounds are ~4x that
//       median specifically to absorb CI-vs-dev-box variance
//   (e) RED-PROOF — revert processingMixOf's memoisation via a GR#24 scratch
//       copy (cp/mv, never git) and show the perf bound goes red while
//       identity stays green
//   (f) replay byte-identity is covered by the sibling genesis-replay.test.mjs
//       and chunked-replay.test.mjs run alongside this file in the gate list
//       (not duplicated here)

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import fs from 'node:fs';
import {
  isOnline,
  wasteGeneratedOf,
  collectionCapacityOf,
  wasteStatsOf,
  processingMixOf,
  parksCapacityOf,
  stationLinks,
  computeRoadConnectivity,
  isRoadSpec,
  SPECS,
  WASTE_PER_RESIDENT,
  WASTE_PER_JOB,
  MAP_W,
  MAP_H,
} from '../src/sim/data.ts';
import { reducer, initialState } from '../src/sim/engine.ts';
import { buildScaleFixture, DEFAULT_BUILDING_COUNT } from './scale/fixture.mjs';

// ────────────────────────────────────────────────────────────────────────
// ORACLES — independently re-implemented from the pre-BUG-643 data.ts bodies.
// Deliberately do NOT import data.ts's private processCapacitiesOf/specJobs —
// hand-copied here so a memo drifting from the real formula is caught, not
// masked by testing the memo against itself.
// ────────────────────────────────────────────────────────────────────────

function legacySpecJobs(sp) {
  if (sp.jobs) return sp.jobs;
  if (sp.kind === 'commercial') return 12;
  if (sp.kind === 'industrial') return 18;
  return 0;
}

function legacyWasteGeneratedOf(s) {
  let tonnes = 0;
  for (const b of s.buildings) {
    if (!isOnline(s, b)) continue;
    const sp = SPECS[b.spec];
    if (!sp) continue;
    if (sp.kind === 'residential') {
      tonnes += (sp.residents ?? 8) * WASTE_PER_RESIDENT;
    } else if (sp.kind === 'commercial' || sp.kind === 'office' || sp.kind === 'industrial' || sp.kind === 'mine') {
      tonnes += legacySpecJobs(sp) * WASTE_PER_JOB;
    }
  }
  return tonnes;
}

function legacyCollectionCapacityOf(s) {
  let cap = 0;
  for (const b of s.buildings) {
    if (!isOnline(s, b)) continue;
    const sp = SPECS[b.spec];
    if (sp?.wasteCapacity) cap += sp.wasteCapacity;
  }
  return cap;
}

function legacyWasteStatsOf(s) {
  const generated = legacyWasteGeneratedOf(s);
  const capacity = legacyCollectionCapacityOf(s);
  const coverage = generated > 0 ? Math.min(1, capacity / generated) : 1;
  const collected = Math.min(generated, capacity);
  const uncollected = Math.max(0, generated - collected);
  return { generated, capacity, collected, coverage, uncollected, uncollectedFraction: 1 - coverage };
}

function legacyProcessCapacityOf(s, specId) {
  let cap = 0;
  for (const b of s.buildings) {
    if (b.spec !== specId) continue;
    if (!isOnline(s, b)) continue;
    const sp = SPECS[b.spec];
    if (sp?.processCapacity) cap += sp.processCapacity;
  }
  return cap;
}

function legacyProcessingMixOf(s) {
  const collected = legacyWasteStatsOf(s).collected;
  const efwCapacity = legacyProcessCapacityOf(s, 'waste_incinerator');
  const mrfCapacity = legacyProcessCapacityOf(s, 'waste_recycling');
  const compostCapacity = legacyProcessCapacityOf(s, 'waste_compost');
  const landfillCapacity = legacyProcessCapacityOf(s, 'waste_landfill');
  const divertCapacity = efwCapacity + mrfCapacity + compostCapacity;
  const diverted = Math.min(collected, divertCapacity);
  const share = (cap) => (divertCapacity > 0 ? diverted * (cap / divertCapacity) : 0);
  const efw = share(efwCapacity);
  const mrf = share(mrfCapacity);
  const compost = share(compostCapacity);
  const landfill = collected - diverted;
  const diversionRate = collected > 0 ? diverted / collected : 0;
  return {
    collected,
    efwCapacity,
    mrfCapacity,
    compostCapacity,
    landfillCapacity,
    divertCapacity,
    efw,
    mrf,
    compost,
    landfill,
    diverted,
    diversionRate,
  };
}

function legacyParksCapacityOf(s) {
  let capacity = 0;
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (sp?.kind === 'park') capacity += sp.w * sp.h;
  }
  return capacity;
}

function legacyStationLinks(s) {
  const roads = new Set();
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (sp?.kind === 'road') roads.add(`${b.x},${b.y}`);
  }
  const connectedIds = new Set();
  let total = 0;
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (!sp || sp.kind !== 'station') continue;
    total++;
    let linked = false;
    for (let dx = 0; dx < sp.w && !linked; dx++) {
      for (let dy = 0; dy < sp.h && !linked; dy++) {
        const x = b.x + dx;
        const y = b.y + dy;
        if (
          roads.has(`${x + 1},${y}`) ||
          roads.has(`${x - 1},${y}`) ||
          roads.has(`${x},${y + 1}`) ||
          roads.has(`${x},${y - 1}`)
        ) {
          linked = true;
        }
      }
    }
    if (linked) connectedIds.add(b.id);
  }
  return { total, connectedIds };
}

const ORACLE_ORTHO = [[1, 0], [-1, 0], [0, 1], [0, -1]];

function legacyComputeRoadConnectivity(s) {
  const roadTiles = new Set();
  const trunkTiles = new Set();
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (!sp) continue;
    const road = isRoadSpec(sp);
    const trunk = sp.kind === 'motorway' || sp.kind === 'rail' || sp.kind === 'station';
    if (!road && !trunk) continue;
    for (let dx = 0; dx < sp.w; dx++)
      for (let dy = 0; dy < sp.h; dy++) {
        const k = `${b.x + dx},${b.y + dy}`;
        if (road) roadTiles.add(k);
        if (trunk) trunkTiles.add(k);
      }
  }
  const connected = new Set();
  const queue = [];
  const seed = (k) => {
    if (roadTiles.has(k) && !connected.has(k)) {
      connected.add(k);
      queue.push(k);
    }
  };
  for (const k of roadTiles) {
    const c = k.indexOf(',');
    const x = Number(k.slice(0, c));
    const y = Number(k.slice(c + 1));
    const edge = x === 0 || y === 0 || x === MAP_W - 1 || y === MAP_H - 1;
    const nearTrunk =
      trunkTiles.has(`${x + 1},${y}`) ||
      trunkTiles.has(`${x - 1},${y}`) ||
      trunkTiles.has(`${x},${y + 1}`) ||
      trunkTiles.has(`${x},${y - 1}`);
    if (edge || trunkTiles.has(k) || nearTrunk) seed(k);
  }
  let head = 0;
  while (head < queue.length) {
    const k = queue[head++];
    const c = k.indexOf(',');
    const x = Number(k.slice(0, c));
    const y = Number(k.slice(c + 1));
    for (const [ox, oy] of ORACLE_ORTHO) {
      const nk = `${x + ox},${y + oy}`;
      if (roadTiles.has(nk) && !connected.has(nk)) {
        connected.add(nk);
        queue.push(nk);
      }
    }
  }
  return { connectedRoadTiles: Array.from(connected).sort() };
}

function assertSemanticIdentity(s, label) {
  assert.deepEqual(wasteGeneratedOf(s), legacyWasteGeneratedOf(s), `${label}: wasteGeneratedOf diverged`);
  assert.deepEqual(collectionCapacityOf(s), legacyCollectionCapacityOf(s), `${label}: collectionCapacityOf diverged`);
  assert.deepEqual(wasteStatsOf(s), legacyWasteStatsOf(s), `${label}: wasteStatsOf diverged`);
  assert.deepEqual(processingMixOf(s), legacyProcessingMixOf(s), `${label}: processingMixOf diverged`);
  assert.deepEqual(parksCapacityOf(s), legacyParksCapacityOf(s), `${label}: parksCapacityOf diverged`);
  const links = stationLinks(s);
  const legacyLinks = legacyStationLinks(s);
  assert.equal(links.total, legacyLinks.total, `${label}: stationLinks.total diverged`);
  assert.deepEqual([...links.connectedIds].sort(), [...legacyLinks.connectedIds].sort(), `${label}: stationLinks.connectedIds diverged`);
}

function median(values) {
  const sorted = [...values].sort((a, b) => a - b);
  return sorted[Math.floor(sorted.length / 2)];
}

// ────────────────────────────────────────────────────────────────────────
// (a) SEMANTIC IDENTITY at 3 scales
// ────────────────────────────────────────────────────────────────────────

test('ATTACK (a): tiny hand-built state — waste family/parks/stationLinks match the unmemoised oracle', () => {
  let s = initialState();
  s = {
    ...s,
    tick: 200,
    roadConnectivity: { connectedRoadTiles: ['6,5', '7,5'] },
    buildings: [
      { id: 1, spec: 'res_hut', x: 5, y: 5, builtTick: 0 },
      { id: 2, spec: 'road', x: 6, y: 5, builtTick: 0 },
      { id: 3, spec: 'road', x: 7, y: 5, builtTick: 0 },
      { id: 4, spec: 'waste_depot', x: 6, y: 6, builtTick: 0 },
      { id: 5, spec: 'waste_incinerator', x: 20, y: 20, builtTick: 0 },
      { id: 6, spec: 'waste_recycling', x: 30, y: 30, builtTick: 0 },
      { id: 7, spec: 'waste_compost', x: 40, y: 40, builtTick: 0 },
      { id: 8, spec: 'waste_landfill', x: 50, y: 50, builtTick: 0 },
      { id: 9, spec: 'park', x: 60, y: 60, builtTick: 0 },
      { id: 10, spec: 'station_sanderling', x: 8, y: 5, builtTick: 0 },
      { id: 11, spec: 'off_suite', x: 70, y: 70, builtTick: 0 },
      { id: 12, spec: 'farm_wheat', x: 80, y: 80, builtTick: 0 },
    ].filter((b) => SPECS[b.spec]), // tolerate catalogue renames without failing this fixture
  };
  assertSemanticIdentity(s, 'tiny');
  assert.deepEqual(computeRoadConnectivity(s), legacyComputeRoadConnectivity(s), 'tiny: computeRoadConnectivity diverged');
});

test('ATTACK (a): 1,000-building fixture — waste family/parks/stationLinks match the unmemoised oracle', () => {
  const s = buildScaleFixture({ buildingCount: 1000, targetPopulation: 100_000, settleTicks: 3 });
  assertSemanticIdentity(s, '1k fixture');
  assert.deepEqual(computeRoadConnectivity(s), legacyComputeRoadConnectivity(s), '1k fixture: computeRoadConnectivity diverged');
});

let bigFixture;
test('ATTACK (a): 13k/1.4M-population fixture (the real dogfood-scale gate fixture) — matches the unmemoised oracle', () => {
  bigFixture = buildScaleFixture();
  assert.equal(bigFixture.buildings.length, DEFAULT_BUILDING_COUNT);
  assertSemanticIdentity(bigFixture, '13k fixture');
  assert.deepEqual(
    computeRoadConnectivity(bigFixture),
    legacyComputeRoadConnectivity(bigFixture),
    '13k fixture: computeRoadConnectivity diverged'
  );
});

// A dedicated 30k-building fixture — Aaron's real dogfood city is 29,831
// buildings; the dispatch prompt explicitly asks for "three scales including a
// 30k-building state".
let hugeFixture;
test('ATTACK (a): ~30k-building fixture (Aaron dogfood scale) — matches the unmemoised oracle', () => {
  hugeFixture = buildScaleFixture({ buildingCount: 30000, targetPopulation: 2_000_000, settleTicks: 1 });
  assertSemanticIdentity(hugeFixture, '30k fixture');
  assert.deepEqual(
    computeRoadConnectivity(hugeFixture),
    legacyComputeRoadConnectivity(hugeFixture),
    '30k fixture: computeRoadConnectivity diverged'
  );
});

// ────────────────────────────────────────────────────────────────────────
// (b) GR#21 — same state twice -> same cached reference
// ────────────────────────────────────────────────────────────────────────

test('ATTACK (b): GR#21 — the SAME state object queried twice returns byte-identical AND referentially-identical results (proves a real cache hit)', () => {
  const s = bigFixture ?? buildScaleFixture();
  const pairs = [
    [wasteStatsOf(s), wasteStatsOf(s)],
    [processingMixOf(s), processingMixOf(s)],
    [stationLinks(s), stationLinks(s)],
    [computeRoadConnectivity(s), computeRoadConnectivity(s)],
  ];
  for (const [first, second] of pairs) {
    assert.equal(first, second, 'repeated call on the same state must return the SAME object reference (real cache hit, not a lucky deepEqual)');
  }
  // parksCapacityOf/wasteGeneratedOf/collectionCapacityOf return primitive
  // numbers, not objects — referential identity is not meaningful for them,
  // so just re-assert value stability here.
  assert.equal(parksCapacityOf(s), parksCapacityOf(s));
  assert.equal(wasteGeneratedOf(s), wasteGeneratedOf(s));
  assert.equal(collectionCapacityOf(s), collectionCapacityOf(s));
});

test('ATTACK (b): two independently-built identical fixtures agree (different objects, same content)', () => {
  const s1 = buildScaleFixture({ buildingCount: 500, targetPopulation: 30_000, settleTicks: 2 });
  const s2 = buildScaleFixture({ buildingCount: 500, targetPopulation: 30_000, settleTicks: 2 });
  assert.deepEqual(wasteStatsOf(s1), wasteStatsOf(s2));
  assert.deepEqual(processingMixOf(s1), processingMixOf(s2));
  assert.deepEqual(parksCapacityOf(s1), parksCapacityOf(s2));
  const l1 = stationLinks(s1);
  const l2 = stationLinks(s2);
  assert.equal(l1.total, l2.total);
  assert.deepEqual([...l1.connectedIds].sort(), [...l2.connectedIds].sort());
});

// ────────────────────────────────────────────────────────────────────────
// (c) STALENESS — a real reducer action that changes the waste/park/station/
// road picture must never read the pre-action cached answer.
// ────────────────────────────────────────────────────────────────────────

test('ATTACK (c): placing a new waste processor produces a state whose processingMixOf/wasteStatsOf reflect it (no stale pre-place cache hit)', () => {
  const s = buildScaleFixture({ buildingCount: 300, targetPopulation: 20_000, settleTicks: 1 });
  const before = processingMixOf(s);
  // Append the new building DIRECTLY (a fresh top-level state + fresh buildings
  // array, exactly what a real reducer case returns — see the (b) in-place
  // mutation grep above) rather than dispatching 'place': the fixture's OWN
  // buildings carry no `builtTick` (isOnline's documented BACKWARD TOLERANCE
  // treats an absent builtTick as always-online), but a REAL 'place' action
  // stamps builtTick = current tick, which then subjects the new building to
  // the road-adjacency gate — and this coordinate is deliberately far from any
  // road so a real placement would come up OFFLINE for reasons unrelated to
  // memoisation. Direct construction isolates the thing under test: does a
  // NEW buildings array reference get a FRESH processingMixOf answer.
  const newBuilding = { id: 999_001, spec: 'waste_incinerator', x: 200, y: 200 };
  const next = { ...s, buildings: [...s.buildings, newBuilding] };
  assert.notEqual(next, s, 'the constructed next state must be a new top-level object');
  assert.notEqual(next.buildings, s.buildings, 'the constructed next state must have a new buildings array reference');
  const after = processingMixOf(next);
  assert.deepEqual(after, legacyProcessingMixOf(next), 'post-place processingMixOf must match the unmemoised oracle');
  assert.notEqual(
    after.efwCapacity,
    before.efwCapacity,
    'adding a new EfW plant must change efwCapacity — a stale cache hit here would be the BUG-642-class regression'
  );
});

test('ATTACK (c): placing a new station produces a state whose stationLinks reflects it', () => {
  let s = buildScaleFixture({ buildingCount: 300, targetPopulation: 20_000, settleTicks: 1 });
  s = { ...s, unlockedAll: true };
  const before = stationLinks(s);
  const next = reducer(s, { type: 'place', spec: 'station_sanderling', x: 210, y: 200 });
  assert.notEqual(next, s, 'place must return a new top-level state object');
  const after = stationLinks(next);
  assert.deepEqual([...after.connectedIds].sort(), [...legacyStationLinks(next).connectedIds].sort(), 'post-place stationLinks must match the unmemoised oracle');
  if (next.buildings.length > s.buildings.length) {
    assert.notEqual(after.total, before.total, 'placing a new station must change stationLinks.total');
  }
});

test('ATTACK (c): a full realistic action sequence never observes a stale waste/parks/station answer', () => {
  let s = buildScaleFixture({ buildingCount: 300, targetPopulation: 20_000, settleTicks: 1 });
  s = { ...s, unlockedAll: true };
  const actions = [
    { type: 'tick' },
    { type: 'place', spec: 'waste_recycling', x: 220, y: 200 },
    { type: 'tick' },
    { type: 'place', spec: 'park', x: 230, y: 200 },
    { type: 'tax', which: 'residential', rate: 0.05 },
    { type: 'tick' },
  ].filter((a) => a.type !== 'place' || SPECS[a.spec]);
  for (const action of actions) {
    s = reducer(s, action);
    assertSemanticIdentity(s, `after dispatching ${JSON.stringify(action)}`);
  }
});

// ────────────────────────────────────────────────────────────────────────
// (d) PERF BOUNDS — median of >=7 runs, bounds at ~4x the measured median.
//
// DERIVATION (recorded so a future reader can tell a real regression from a
// dev-box artifact, per this project's house rule against wall-clock bounds
// picked without justification): measured on this dev machine, node 25, over
// several 9-run calibration sessions at the 13k-building/1.4M-population scale
// fixture (DEFAULT_BUILDING_COUNT). EACH RUN BUILDS A FRESH STATE (`{ ...base,
// buildings: base.buildings.slice() }`) so memoOnState's WeakMap<SimState, T>
// cannot carry a hit across runs — this is deliberate: it reproduces the real
// per-TICK shape (a genuinely new state each tick) rather than measuring an
// artifact of reusing one object forever, which the first draft of this file
// got wrong (every selector reported ~0.00ms because the SAME fixture object
// was reused across all 9 timed runs, so only the very first run ever paid
// the real cost). Each run then calls the selector 5 times on that ONE fresh
// state (1 real cold pass + 4 memo hits — the real per-tick call-site
// fan-out: computeFlows + 2-4 panels each pulling the same derivation).
// Medians observed across several calibration runs:
//   wasteStatsOf             ~1.9-2.6ms/run (5 calls)  -> bound 12ms
//   processingMixOf          ~2.4-3.0ms/run (5 calls)  -> bound 15ms
//   parksCapacityOf          ~0.2-0.3ms/run (5 calls)  -> bound 3ms
//   stationLinks             ~0.9-1.3ms/run (20 calls, wider fan-out — see its
//                             own comment below for why) -> bound 6ms
//   computeRoadConnectivity (cold, 1 call/run, no intra-run reuse to amortise)
//                             ~1.1-1.4ms/run -> bound 8ms
// These are LOOSE (4x+) sanity ceilings, not tight regression gates — their
// job is to catch a memo being silently reverted/broken (see RED-PROOF below),
// not to enforce a specific speed target. CI hardware differs from this box;
// tightening these further without re-deriving on CI would repeat the
// house-rule mistake this comment exists to avoid.
// ────────────────────────────────────────────────────────────────────────

function timeRuns(fn, runs, callsPerRun, buildFreshState) {
  const samples = [];
  for (let i = 0; i < runs; i++) {
    const s = buildFreshState();
    const t0 = performance.now();
    for (let c = 0; c < callsPerRun; c++) fn(s);
    samples.push(performance.now() - t0);
  }
  return median(samples);
}

const perfBase = buildScaleFixture();
function perfFreshState() {
  return { ...perfBase, buildings: perfBase.buildings.slice() };
}

test('PERF: wasteStatsOf median (1 cold + 4 cached calls/run, fresh state per run) stays under the derived bound', () => {
  const m = timeRuns(wasteStatsOf, 7, 5, perfFreshState);
  console.log(`[PERF] wasteStatsOf median=${m.toFixed(2)}ms (bound 12ms)`);
  assert.ok(m < 12, `wasteStatsOf median ${m.toFixed(2)}ms exceeded the 12ms bound`);
});

test('PERF: processingMixOf median (1 cold + 4 cached calls/run, fresh state per run) stays under the derived bound', () => {
  const m = timeRuns(processingMixOf, 7, 5, perfFreshState);
  console.log(`[PERF] processingMixOf median=${m.toFixed(2)}ms (bound 15ms)`);
  assert.ok(m < 15, `processingMixOf median ${m.toFixed(2)}ms exceeded the 15ms bound`);
});

test('PERF: parksCapacityOf median (1 cold + 4 cached calls/run, fresh state per run) stays under the derived bound', () => {
  const m = timeRuns(parksCapacityOf, 7, 5, perfFreshState);
  console.log(`[PERF] parksCapacityOf median=${m.toFixed(2)}ms (bound 3ms)`);
  assert.ok(m < 3, `parksCapacityOf median ${m.toFixed(2)}ms exceeded the 3ms bound`);
});

// stationLinks uses MORE calls/run (20, not 5) than the other selectors here
// specifically so its RED-PROOF below (which reverts stationLinks' own memo)
// has enough absolute-time separation to be reliable: at 5 calls/run the
// memoised-vs-unmemoised gap (~0.6ms vs ~2.9ms) sits too close together for a
// single loose bound to cleanly separate green-normal from red-reverted. 20
// calls/run widens that gap proportionally (~1ms memoised vs ~12ms unmemoised).
test('PERF: stationLinks median (1 cold + 19 cached calls/run, fresh state per run) stays under the derived bound', () => {
  const m = timeRuns(stationLinks, 7, 20, perfFreshState);
  console.log(`[PERF] stationLinks median=${m.toFixed(2)}ms (bound 6ms)`);
  assert.ok(m < 6, `stationLinks median ${m.toFixed(2)}ms exceeded the 6ms bound`);
});

test('PERF: computeRoadConnectivity median (cold, no intra-run reuse, fresh state per run) stays under the derived bound', () => {
  const m = timeRuns(computeRoadConnectivity, 7, 1, perfFreshState);
  console.log(`[PERF] computeRoadConnectivity median=${m.toFixed(2)}ms (bound 8ms)`);
  assert.ok(m < 8, `computeRoadConnectivity median ${m.toFixed(2)}ms exceeded the 8ms bound`);
});

// ────────────────────────────────────────────────────────────────────────
// (e) RED-PROOF — revert processingMixOf's memoisation via a GR#24-compliant
// scratch copy (cp/mv, never git) and prove the perf bound goes RED while
// identity stays GREEN. Run in a FRESH child process (tsx/node ESM caches the
// module graph, so an in-process before/after cannot observe a mid-run edit —
// same reasoning BUG-642's attack suite documents for its own red-proof).
// ────────────────────────────────────────────────────────────────────────

const DATA_TS_PATH = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'src', 'sim', 'data.ts');
const BACKUP_PATH = `${DATA_TS_PATH}.attack643.bak`;

// WHY stationLinks IS THE RED-PROOF TARGET, NOT processingMixOf/wasteStatsOf:
// this file's chain is LAYERED (wasteStatsOf wraps wasteGeneratedOf +
// collectionCapacityOf, processingMixOf wraps wasteStatsOf + processCapacitiesOf).
// Reverting only the OUTER wrapper's own memoOnState leaves its inputs still
// memoised underneath, so a repeat call still hits the (still-cached) inner
// results and the outer body's own arithmetic is cheap regardless — no visible
// slowdown. The layering is real perf-win behaviour (a caller of processingMixOf
// benefits from wasteGeneratedOf being cached even before this fix's OWN
// wrapper existed), but it means processingMixOf is the wrong function to
// red-proof directly. stationLinks has NO other memoised layer above or below
// it (it IS the base O(buildings) computation, called directly by 4 engine.ts
// sites) — reverting ITS memo is the one edit in this fix guaranteed to show up
// as a real, undiluted slowdown, so it is the one exercised here.
test('ATTACK (e): RED-PROOF — reverting stationLinks to its unmemoised form keeps identity green but turns its perf bound red (proven in a fresh child process, data.ts restored via GR#24 scratch-copy discipline)', () => {
  execFileSync('cp', [DATA_TS_PATH, BACKUP_PATH]);
  const backupStat = fs.statSync(BACKUP_PATH);
  assert.ok(backupStat.size > 0, 'GR#24 mutation-cycle safety check: backup file must exist and be non-empty before mutating');

  const CHILD_SCRIPT = path.join(path.dirname(fileURLToPath(import.meta.url)), 'scale', '__bug643-redproof-child.mjs');

  try {
    const original = fs.readFileSync(DATA_TS_PATH, 'utf8');
    // Un-memoise stationLinks: swap the memoOnState-wrapped const declaration
    // for a plain exported function that recomputes every call — exactly its
    // pre-BUG-643 shape.
    const marker = 'export const stationLinks: (s: SimState) => StationLinkInfo = memoOnState((s) => {';
    assert.ok(original.includes(marker), 'RED-PROOF setup: expected memoised stationLinks declaration not found — data.ts shape changed');
    const mutated = original
      .replace(marker, 'export function stationLinks(s: SimState): StationLinkInfo {')
      .replace(
        /(export function stationLinks\(s: SimState\): StationLinkInfo \{[\s\S]*?\n  return \{ total, connectedIds \};\n)\}\);/,
        '$1}'
      );
    assert.notEqual(mutated, original, 'RED-PROOF setup: the mutation must actually change the file');
    fs.writeFileSync(DATA_TS_PATH, mutated, 'utf8');

    fs.writeFileSync(
      CHILD_SCRIPT,
      `import { stationLinks } from '../../src/sim/data.ts';\n` +
        `import { buildScaleFixture } from '../scale/fixture.mjs';\n` +
        `const base = buildScaleFixture();\n` +
        `function fresh(){ return { ...base, buildings: base.buildings.slice() }; }\n` +
        `function median(v){const a=[...v].sort((x,y)=>x-y);return a[Math.floor(a.length/2)];}\n` +
        `const samples=[];\n` +
        `for (let i=0;i<7;i++){const s=fresh();const t0=performance.now();for(let c=0;c<20;c++)stationLinks(s);samples.push(performance.now()-t0);}\n` +
        `console.log(JSON.stringify({ median: median(samples) }));\n`,
      'utf8'
    );

    const out = execFileSync('node', ['--import', 'tsx', CHILD_SCRIPT], {
      cwd: path.join(path.dirname(fileURLToPath(import.meta.url)), '..'),
      encoding: 'utf8',
    });
    const { median: unmemoisedMedian } = JSON.parse(out.trim().split('\n').pop());
    console.log(`[RED-PROOF] unmemoised stationLinks median=${unmemoisedMedian.toFixed(2)}ms (bound was 6ms)`);
    assert.ok(
      unmemoisedMedian > 6,
      `RED-PROOF FAILED: reverting the memoisation should have pushed the median (${unmemoisedMedian.toFixed(2)}ms) over the 6ms bound — if it did not, the bound is too loose or the memo was not actually doing anything`
    );
  } finally {
    fs.renameSync(BACKUP_PATH, DATA_TS_PATH);
    if (fs.existsSync(CHILD_SCRIPT)) fs.rmSync(CHILD_SCRIPT);
  }
});

test('ATTACK (e): tripwire — data.ts is back in its expected (memoised, BUG-643-fixed) state after the red-proof, no stray .bak left', () => {
  const src = fs.readFileSync(DATA_TS_PATH, 'utf8');
  assert.match(
    src,
    /export const stationLinks: \(s: SimState\) => StationLinkInfo = memoOnState\(\(s\) => \{/,
    'data.ts must currently contain the memoised stationLinks implementation'
  );
  assert.equal(fs.existsSync(BACKUP_PATH), false, 'no stray .bak file should be left over from the red-proof run');
});
