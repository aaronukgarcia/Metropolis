// sectorPartition.test.mjs — FEAT-2326609764 inc1 (SPATIAL-PARTITION TICK).
//
// Covers the acceptance doc's inc1 row (§10):
//   AC-1/AC-2/AC-4 (as amended by the lead's R1 ruling, 2026-09-11) — sector
//     geometry derived from consolidator.ts's real TILE_METRES, independent
//     of the player-adjustable consolidator section size.
//   AC-4/AC-5 — sectorIndexOf is one memoised walk, occupied sectors only,
//     origin-tile ownership for boundary-spanning footprints.
//   AC-16/AC-17 — integer domain + the published NOT_PARTITIONED residue.
//   AC-18 — the differential harness corpus (seeded random cities across
//     three orders of magnitude, boundary/edge fixtures, the scale fixture).
//   AC-20 — order independence, asserted directly.
//   AC-26 — GR#21 determinism discipline (no Date.now/Math.random/localStorage
//     in this file or sectorPartition.ts; verified by inspection + this
//     suite's own determinism re-run test below).
//
// AC-19 (the RED proof) is NOT a permanent test here — the acceptance doc
// asks for three defects reintroduced via a SCRATCH COPY (cp f f.bak; edit;
// run; restore), proven to redden this suite, then reverted (GR#24: never a
// git command to undo). That proof was run once during this build and is
// logged in the BOW comment / build report, not committed as code.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

import { SPECS, totalJobs, totalJobsWholeCity, computeRoadConnectivity, buildingJobsOf, isOnline } from '../src/sim/data.ts';
import { MAP_W, MAP_H } from '../src/sim/grid.ts';
import { TILE_METRES } from '../src/sim/consolidator.ts';
import {
  SECTOR_METRES,
  SECTOR_TILES,
  SECTORS_X,
  SECTORS_Y,
  TOTAL_SECTORS,
  sectorKeyOf,
  sectorOriginOf,
  sectorIndexOf,
  foldCityJobs,
  totalJobsPartitioned,
  PARTITIONED_DERIVATIONS,
  __setPartitionedDerivationsForTest,
  NOT_PARTITIONED,
} from '../src/sim/sectorPartition.ts';
import { compareAllDerivations, canonicalSerialize } from './partition-differential.mjs';
import { buildScaleFixture } from './scale/fixture.mjs';

const __dirname = dirname(fileURLToPath(import.meta.url));
const SECTOR_PARTITION_SOURCE = readFileSync(join(__dirname, '../src/sim/sectorPartition.ts'), 'utf8');

// ---------------------------------------------------------------------------
// Deterministic PRNG (mulberry32) — no Math.random (GR#21), mirrors the
// established idiom in test/attack-bug659-round.test.mjs /
// test/attack-bug935-round.test.mjs.
// ---------------------------------------------------------------------------
function mulberry32(seed) {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// ---------------------------------------------------------------------------
// Minimal state builder (mirrors test/utilisation.test.mjs's minimalState) —
// AC-7's "a state built by a test, no reducer involved" case, deliberately.
// ---------------------------------------------------------------------------
function minimalState(overrides = {}) {
  return {
    tick: 0,
    speed: 1,
    funds: 10_000_000,
    loanBalance: 0,
    population: 0,
    xp: 0,
    taxRates: { residential: 9, commercial: 11, industrial: 13 },
    policies: { recycling: false, transitSubsidy: false, tourismDrive: false, austerity: false },
    buildings: [],
    nextId: 1,
    movingId: null,
    tool: { mode: 'select' },
    clipboard: null,
    pipeTier: {},
    history: [],
    ledger: [],
    nextLedgerId: 1,
    lastFlows: { inflows: [], outflows: [] },
    fundsAtTickStart: 10_000_000,
    fundsAtTickEnd: 10_000_000,
    pendingRewards: [],
    lastRewardedLevel: 1,
    notice: null,
    ...overrides,
  };
}

// A small, deliberately varied catalogue of specs used by the generator
// below: some carry `jobs` directly (with and without capacityTiers), some
// fall back to the kind-based defaults (commercial=12, industrial=18), and
// some (kind 'park'/'road') contribute zero jobs — exercising every branch
// of buildingJobsOf.
const JOB_SPEC = 'off_suite'; // office, sp.jobs truthy
const COMMERCIAL_NO_JOBS_SPEC = 'com_shop'; // commercial, sp.jobs falsy -> fallback 12
const INDUSTRIAL_NO_JOBS_SPEC = 'farm_wheat'; // industrial, sp.jobs falsy -> fallback 18
const ZERO_JOBS_SPEC = Object.values(SPECS).find((sp) => sp.kind === 'park')?.id;
const ROAD_SPEC = Object.values(SPECS).find((sp) => sp.kind === 'road' && sp.w === 1 && sp.h === 1)?.id;

test('sanity: fixture spec ids exist in the live catalogue', () => {
  assert.ok(SPECS[JOB_SPEC], `expected ${JOB_SPEC} in SPECS`);
  assert.ok(SPECS[COMMERCIAL_NO_JOBS_SPEC], `expected ${COMMERCIAL_NO_JOBS_SPEC} in SPECS`);
  assert.ok(SPECS[INDUSTRIAL_NO_JOBS_SPEC], `expected ${INDUSTRIAL_NO_JOBS_SPEC} in SPECS`);
  assert.ok(ZERO_JOBS_SPEC, 'expected at least one park-kind spec');
  assert.ok(ROAD_SPEC, 'expected at least one 1x1 road-kind spec');
  assert.ok(SPECS[JOB_SPEC].jobs, `${JOB_SPEC} must carry sp.jobs for the fallback branches to be exercised elsewhere`);
  assert.ok(!SPECS[COMMERCIAL_NO_JOBS_SPEC].jobs, `${COMMERCIAL_NO_JOBS_SPEC} must NOT carry sp.jobs (exercises the commercial fallback)`);
  assert.ok(!SPECS[INDUSTRIAL_NO_JOBS_SPEC].jobs, `${INDUSTRIAL_NO_JOBS_SPEC} must NOT carry sp.jobs (exercises the industrial fallback)`);
});

const SPEC_POOL = [JOB_SPEC, COMMERCIAL_NO_JOBS_SPEC, INDUSTRIAL_NO_JOBS_SPEC, ZERO_JOBS_SPEC, ROAD_SPEC];

/**
 * Deterministic seeded random city generator (GR#21: no Math.random). Every
 * building is placed with a real (x, y) inside the map, a spec drawn from
 * SPEC_POOL, and a mix of online (builtTick: null — pre-dates the
 * road-activation-gate feature, "always online" per isOnline's own
 * documented backward tolerance) and offline/mid-construction
 * (builtTick: s.tick, so `s.tick - b.builtTick < constructionTicks(sp)`
 * holds and isOnline() gates it off) buildings.
 */
function seededRandomCity(seed, buildingCount, { tick = 50 } = {}) {
  const rng = mulberry32(seed);
  const buildings = [];
  for (let i = 1; i <= buildingCount; i++) {
    const spec = SPEC_POOL[Math.floor(rng() * SPEC_POOL.length)];
    const x = Math.floor(rng() * MAP_W);
    const y = Math.floor(rng() * MAP_H);
    const online = rng() > 0.15; // ~15% offline/mid-construction, matching AC-18's "mixed online/offline" corpus requirement
    const capacityTier = Math.floor(rng() * 3);
    const b = { id: i, spec, x, y, capacityTier };
    if (!online) b.builtTick = tick; // fresh placement THIS tick -> under construction -> isOnline() false
    else b.builtTick = null; // pre-dates the gate -> always online
    buildings.push(b);
  }
  return minimalState({ tick, nextId: buildingCount + 1, buildings });
}

// ---------------------------------------------------------------------------
// AC-1/AC-2 (as amended by lead ruling R1) — derived constants, never bare
// literals; sector geometry is independent of the consolidator's section
// size.
// ---------------------------------------------------------------------------
test('AC-1/AC-2 (amended): sector constants are derived, not literals', () => {
  assert.equal(SECTOR_METRES, 1000);
  assert.equal(TILE_METRES, 50, 'sanity: consolidator.ts TILE_METRES unchanged');
  assert.equal(SECTOR_TILES, Math.round(SECTOR_METRES / TILE_METRES));
  assert.equal(SECTOR_TILES, 20, 'at todays constants SECTOR_TILES must be exactly 20');
  assert.equal(SECTORS_X, Math.ceil(MAP_W / SECTOR_TILES));
  assert.equal(SECTORS_Y, Math.ceil(MAP_H / SECTOR_TILES));
  assert.equal(TOTAL_SECTORS, SECTORS_X * SECTORS_Y);
});

// ---------------------------------------------------------------------------
// R1's structural invariant: sectorKeyOf must NEVER read the consolidator's
// runtime-adjustable section size. Verified by source inspection (the
// astgate/units-lint precedent for "assert a read never happens") rather
// than trusting the doc comment alone.
// ---------------------------------------------------------------------------
test('structural: sectorKeyOf never reads sectionMetresOf (source inspection)', () => {
  const start = SECTOR_PARTITION_SOURCE.indexOf('export function sectorKeyOf');
  assert.ok(start >= 0, 'sectorKeyOf must exist as a named export');
  const end = SECTOR_PARTITION_SOURCE.indexOf('\n}', start);
  const body = SECTOR_PARTITION_SOURCE.slice(start, end);
  assert.ok(!body.includes('sectionMetresOf'), 'sectorKeyOf must not reference the player-adjustable consolidator section size');
  assert.ok(!body.includes('CONSOLIDATOR_SECTION_METRES'), 'sectorKeyOf must not reference the consolidator section constant either');
});

test('GR#21: no Date.now/Math.random/localStorage anywhere in sectorPartition.ts', () => {
  assert.ok(!/Date\.now\s*\(/.test(SECTOR_PARTITION_SOURCE));
  assert.ok(!/Math\.random\s*\(/.test(SECTOR_PARTITION_SOURCE));
  assert.ok(!/localStorage\.\w|localStorage\[/.test(SECTOR_PARTITION_SOURCE), 'no actual localStorage READ/WRITE (a doc-comment mention of the word, even sentence-terminated, is fine)');
  // No bare Map/Set iteration with an early break — every walk here is
  // either an accumulation over the full collection or an explicit sort
  // first (foldCityJobs sorts keys before iterating).
  assert.ok(!/for\s*\([^)]*\)\s*\{[^}]*break/s.test(SECTOR_PARTITION_SOURCE.replace(/\/\/.*$/gm, '')));
});

// ---------------------------------------------------------------------------
// AC-4/AC-5 — cold build, occupied sectors only, origin-tile ownership.
// ---------------------------------------------------------------------------
test('AC-4: sectorIndexOf holds only occupied sectors, keyed by ORIGIN tile', () => {
  const s = minimalState({
    buildings: [
      { id: 1, spec: JOB_SPEC, x: 0, y: 0, builtTick: null },
      { id: 2, spec: JOB_SPEC, x: SECTOR_TILES - 1, y: SECTOR_TILES - 1, builtTick: null }, // same sector as id 1 (still sector 0)
      { id: 3, spec: JOB_SPEC, x: SECTOR_TILES, y: 0, builtTick: null }, // next sector along x
    ],
  });
  const index = sectorIndexOf(s);
  assert.equal(index.size, 2, 'only 2 distinct sectors are occupied');
  const sector0 = index.get(sectorKeyOf(0, 0));
  assert.ok(sector0);
  assert.equal(sector0.buildingCount, 2, 'ids 1 and 2 share sector 0');
  const sector1 = index.get(sectorKeyOf(SECTOR_TILES, 0));
  assert.ok(sector1);
  assert.equal(sector1.buildingCount, 1);
});

test('AC-4: a footprint spanning a sector boundary is owned ENTIRELY by its origin sector', () => {
  // A building placed one tile before a sector boundary with a footprint
  // large enough to spill into the next sector must still be counted only
  // in the ORIGIN sector's aggregate — footprintW/H are irrelevant to
  // sectorKeyOf, only (b.x, b.y) matters (AC-4).
  const originX = SECTOR_TILES - 1;
  const s = minimalState({
    buildings: [{ id: 1, spec: JOB_SPEC, x: originX, y: 0, footprintW: 10, footprintH: 10, builtTick: null }],
  });
  const index = sectorIndexOf(s);
  assert.equal(index.size, 1, 'a spanning footprint still occupies exactly one sector: its origin');
  const origin = index.get(sectorKeyOf(originX, 0));
  assert.ok(origin);
  assert.equal(origin.buildingCount, 1);
});

test('AC-7 (fail-safe posture, sanity): a hand-built SimState (no reducer) still cold-builds correctly', () => {
  // Sanity companion to AC-7's real fail-safe test, which lives in
  // data.ts's totalJobs() itself: a state constructed entirely by hand
  // (this test, never via the reducer) must still produce a correct
  // sectorIndexOf/foldCityJobs pair equal to the whole-city walk.
  const s = seededRandomCity(7, 250);
  assert.deepEqual(compareAllDerivations(s), []);
});

// ---------------------------------------------------------------------------
// AC-16 — every SectorAggregate field is a safe integer.
// ---------------------------------------------------------------------------
test('AC-16: every SectorAggregate.jobs value is Number.isSafeInteger', () => {
  const s = seededRandomCity(16, 3000);
  const index = sectorIndexOf(s);
  assert.ok(index.size > 0);
  for (const agg of index.values()) {
    assert.ok(Number.isSafeInteger(agg.jobs), `sector ${agg.key} jobs=${agg.jobs} must be a safe integer`);
    assert.ok(Number.isSafeInteger(agg.buildingCount));
  }
});

// ---------------------------------------------------------------------------
// AC-17 — the published NOT_PARTITIONED residue list, plus a DIAGNOSTIC
// (never asserted) measurement of each entry's real cost.
// ---------------------------------------------------------------------------
test('AC-17: NOT_PARTITIONED is published and non-empty', () => {
  assert.ok(Array.isArray(NOT_PARTITIONED));
  assert.ok(NOT_PARTITIONED.length > 0);
  for (const entry of NOT_PARTITIONED) {
    assert.equal(typeof entry.name, 'string');
    assert.ok(entry.name.length > 0);
    assert.equal(typeof entry.reason, 'string');
    assert.ok(entry.reason.length > 20, `${entry.name}'s reason should be a real explanation, not a stub`);
  }
});

test('AC-17: PARTITIONED_DERIVATIONS default is OFF', () => {
  assert.equal(PARTITIONED_DERIVATIONS, false);
});

// ---------------------------------------------------------------------------
// BUG-1019 fix verification: the test seam actually flips the SHIPPED
// dispatch (totalJobs), and the AC-18 harness stays a real comparison
// (never a tautology) at EITHER flag value. Always resets the flag in a
// finally — this module-level `let` is shared across every test in this
// worker.
// ---------------------------------------------------------------------------
test('BUG-1019: the test seam forces totalJobs() to dispatch through totalJobsPartitioned, and the flag-independent harness still agrees', () => {
  const s = seededRandomCity(1019, 2000);
  assert.equal(PARTITIONED_DERIVATIONS, false, 'sanity: starts OFF');
  assert.equal(totalJobs(s), totalJobsWholeCity(s), 'flag OFF: totalJobs must take the whole-city branch');
  __setPartitionedDerivationsForTest(true);
  try {
    assert.equal(PARTITIONED_DERIVATIONS, true, 'the seam must actually flip the live binding data.ts reads');
    assert.equal(totalJobs(s), totalJobsPartitioned(s), 'flag ON: totalJobs must dispatch to totalJobsPartitioned');
    assert.equal(totalJobs(s), totalJobsWholeCity(s), 'sanity: on a clean fixture the two implementations still agree');
    // The BUG-1019 proof itself: the differential harness's own two rows
    // (totalJobsWholeCity / totalJobsPartitioned, called directly) must be
    // unaffected by the flag's value — unlike the OLD harness, which called
    // totalJobs(s) for `whole` and would have gone tautological right here.
    assert.deepEqual(compareAllDerivations(s), [], 'the harness must still be a real two-implementation comparison with the flag forced ON');
  } finally {
    __setPartitionedDerivationsForTest(false);
  }
  assert.equal(PARTITIONED_DERIVATIONS, false, 'the flag must be back to its default after this test');
});

test('BUG-1019: __setPartitionedDerivationsForTest is a test-only seam (guarded by NODE_TEST_CONTEXT)', () => {
  // Sanity that the seam's own guard checks the SAME env var node --test
  // sets for every file it runs (store.tsx's established idiom) — a
  // negative/removed value must throw rather than silently no-op.
  assert.ok(process.env.NODE_TEST_CONTEXT, 'sanity: this suite is actually running under node --test');
  const saved = process.env.NODE_TEST_CONTEXT;
  delete process.env.NODE_TEST_CONTEXT;
  try {
    assert.throws(() => __setPartitionedDerivationsForTest(true), /NODE_TEST_CONTEXT/);
  } finally {
    process.env.NODE_TEST_CONTEXT = saved;
  }
  assert.equal(PARTITIONED_DERIVATIONS, false, 'the guarded call above must not have changed anything');
});

// ---------------------------------------------------------------------------
// AC-20 — order independence, asserted directly (not merely reasoned about).
// ---------------------------------------------------------------------------
test('AC-20: folding the SAME sector index ascending / descending / shuffled produces byte-identical totals', (t) => {
  const s = seededRandomCity(20, 5000);
  const index = sectorIndexOf(s);
  const ascending = Array.from(index.keys()).sort((a, b) => a - b);
  const descending = [...ascending].reverse();
  const rng = mulberry32(2020);
  const shuffled = [...ascending];
  for (let i = shuffled.length - 1; i > 0; i--) {
    const j = Math.floor(rng() * (i + 1));
    [shuffled[i], shuffled[j]] = [shuffled[j], shuffled[i]];
  }

  function foldInOrder(keys) {
    let total = 0;
    for (const k of keys) total += index.get(k).jobs;
    return total;
  }

  const totalAscending = foldInOrder(ascending);
  const totalDescending = foldInOrder(descending);
  const totalShuffled = foldInOrder(shuffled);
  const totalViaFoldCityJobs = foldCityJobs(index);

  assert.ok(Number.isSafeInteger(totalAscending) && totalAscending > 0, 'sanity: a 5000-building city has nonzero jobs');
  assert.equal(Object.is(totalAscending, totalDescending), true, 'ascending vs descending must be byte-identical (Object.is)');
  assert.equal(Object.is(totalAscending, totalShuffled), true, 'ascending vs shuffled must be byte-identical (Object.is)');
  assert.equal(Object.is(totalAscending, totalViaFoldCityJobs), true, 'foldCityJobs must agree with a hand-rolled ascending fold');
  t.diagnostic(`AC-20: ${index.size} sectors, jobs total ${totalAscending}, 3 orderings byte-identical`);
});

// ---------------------------------------------------------------------------
// BUG-1021 — three mutants survived the author suite in round
// opus-round-feat764-inc1: buildingCount silently online-gated, x0/y0
// unasserted, and foldCityJobs's ascending sort deletable without any test
// noticing. Pinned here (not only in the round's own attack file) so
// inc2/inc3 do not re-open the class.
// ---------------------------------------------------------------------------
test('BUG-1021: buildingCount is a STRUCTURAL count, independent of isOnline (kills the "skip offline buildings" mutant)', () => {
  const tick = 10;
  const s = minimalState({
    tick,
    buildings: [
      { id: 1, spec: JOB_SPEC, x: 0, y: 0, builtTick: null }, // online (pre-dates the gate)
      { id: 2, spec: JOB_SPEC, x: 1, y: 1, builtTick: tick }, // offline: mid-construction this tick
      { id: 3, spec: JOB_SPEC, x: 2, y: 2, builtTick: tick }, // offline: mid-construction this tick
    ],
  });
  const agg = sectorIndexOf(s).get(sectorKeyOf(0, 0));
  assert.ok(agg, 'sector 0 must exist');
  assert.equal(agg.buildingCount, 3, 'buildingCount counts every building in the sector, online or not');
  assert.equal(agg.jobs, buildingJobsOf(SPECS[JOB_SPEC], s.buildings[0]), 'only the online building contributes to jobs');
  assert.ok(agg.jobs > 0, 'sanity: the online building really does carry jobs, or this pin is vacuous');
});

test('BUG-1021: SectorAggregate.x0/y0 are the sector ORIGIN tile, not {0,0} (kills the "sectorOriginOf always returns 0,0" mutant)', () => {
  const x = SECTOR_TILES * 4 + 3;
  const y = SECTOR_TILES * 2 + 9;
  const s = minimalState({ buildings: [{ id: 1, spec: JOB_SPEC, x, y, builtTick: null }] });
  const key = sectorKeyOf(x, y);
  const agg = sectorIndexOf(s).get(key);
  assert.ok(agg);
  assert.equal(agg.x0, SECTOR_TILES * 4, 'x0 must be the sector origin x, never 0 unless the building really is in sector column 0');
  assert.equal(agg.y0, SECTOR_TILES * 2, 'y0 must be the sector origin y, never 0 unless the building really is in sector row 0');
  assert.notEqual(agg.x0, 0, 'sanity the fixture actually exercises a non-zero-origin sector');
  assert.deepEqual(sectorOriginOf(key), { x0: SECTOR_TILES * 4, y0: SECTOR_TILES * 2 });
});

test('BUG-1021: foldCityJobs visits keys in ASCENDING order, observed via an order-recording SPY (not a float sentinel; kills the "delete the sort" mutant)', () => {
  // A float-accumulation sentinel cannot distinguish orderings while the
  // domain is integral (the round's own finding) — this observes the
  // ACTUAL iteration order foldCityJobs takes, via a getter that records
  // every key it is asked to read, regardless of the numeric values summed.
  const visited = [];
  const fake = new Map();
  for (const key of [77, 4, 55, 1, 30]) {
    fake.set(key, {
      key,
      x0: 0,
      y0: 0,
      buildingCount: 1,
      get jobs() {
        visited.push(key);
        return key;
      },
    });
  }
  const total = foldCityJobs(fake);
  assert.equal(total, 77 + 4 + 55 + 1 + 30);
  assert.deepEqual(visited, [1, 4, 30, 55, 77], 'foldCityJobs must sort keys ascending before folding (GR#21) — insertion order here is deliberately NOT ascending');
});

// ---------------------------------------------------------------------------
// AC-18 — the differential harness corpus.
// ---------------------------------------------------------------------------
test('AC-18 corpus: seeded random cities across 3 orders of magnitude (>=200 cities)', (t) => {
  const sizeBuckets = [
    { count: 150, buildings: 100 },
    { count: 40, buildings: 3000 },
    { count: 10, buildings: 30_000 },
  ];
  let citiesRun = 0;
  for (const { count, buildings } of sizeBuckets) {
    for (let i = 0; i < count; i++) {
      const seed = buildings * 1000 + i;
      const s = seededRandomCity(seed, buildings, { tick: 20 + (i % 30) });
      const mismatches = compareAllDerivations(s);
      assert.deepEqual(mismatches, [], `seed ${seed} (${buildings} buildings) must agree: ${JSON.stringify(mismatches)}`);
      citiesRun++;
    }
  }
  assert.ok(citiesRun >= 200, `expected >=200 cities, ran ${citiesRun}`);
  t.diagnostic(`AC-18: ${citiesRun} seeded random cities, all agree`);
});

test('AC-18 corpus: edge cases (zero buildings, single sector, every boundary, all-offline, mid-construction)', () => {
  // Zero buildings.
  assert.deepEqual(compareAllDerivations(minimalState({ buildings: [] })), []);

  // Single sector: every building inside sector (0,0).
  {
    const rng = mulberry32(1);
    const buildings = [];
    for (let i = 1; i <= 30; i++) {
      buildings.push({
        id: i,
        spec: SPEC_POOL[Math.floor(rng() * SPEC_POOL.length)],
        x: Math.floor(rng() * SECTOR_TILES),
        y: Math.floor(rng() * SECTOR_TILES),
        builtTick: null,
      });
    }
    const s = minimalState({ buildings });
    assert.equal(sectorIndexOf(s).size, 1, 'every building must land in exactly one sector');
    assert.deepEqual(compareAllDerivations(s), []);
  }

  // Every boundary: the four map corners, plus every axis at x=0/MAP_W-1 and
  // y=0/MAP_H-1, plus tiles exactly ON a sector boundary (multiples of
  // SECTOR_TILES) and one tile before it.
  {
    const boundaryCoords = [
      [0, 0],
      [MAP_W - 1, 0],
      [0, MAP_H - 1],
      [MAP_W - 1, MAP_H - 1],
      [SECTOR_TILES - 1, SECTOR_TILES - 1],
      [SECTOR_TILES, SECTOR_TILES],
      [SECTOR_TILES * 2 - 1, 0],
      [SECTOR_TILES * 2, 0],
    ];
    const buildings = boundaryCoords.map(([x, y], i) => ({ id: i + 1, spec: JOB_SPEC, x, y, builtTick: null }));
    const s = minimalState({ buildings });
    assert.deepEqual(compareAllDerivations(s), []);
    // Every listed coordinate must land in the sector its own formula predicts.
    const index = sectorIndexOf(s);
    for (const [x, y] of boundaryCoords) {
      assert.ok(index.has(sectorKeyOf(x, y)), `sector for (${x},${y}) must be present`);
    }
  }

  // All-offline (every building mid-construction this tick).
  {
    const rng = mulberry32(2);
    const tick = 5;
    const buildings = [];
    for (let i = 1; i <= 200; i++) {
      buildings.push({
        id: i,
        spec: SPEC_POOL[Math.floor(rng() * SPEC_POOL.length)],
        x: Math.floor(rng() * MAP_W),
        y: Math.floor(rng() * MAP_H),
        builtTick: tick, // placed THIS tick -> under construction -> offline
      });
    }
    const s = minimalState({ tick, buildings });
    // Sanity: totalJobs must be 0 (every building gated offline) — proves
    // the corpus actually exercises the offline branch, not just claims to.
    assert.equal(totalJobs(s), 0, 'an all-mid-construction city must have zero online jobs');
    assert.deepEqual(compareAllDerivations(s), []);
  }

  // Mixed mid-construction: half online, half freshly placed this tick.
  {
    const rng = mulberry32(3);
    const tick = 40;
    const buildings = [];
    for (let i = 1; i <= 400; i++) {
      buildings.push({
        id: i,
        spec: SPEC_POOL[Math.floor(rng() * SPEC_POOL.length)],
        x: Math.floor(rng() * MAP_W),
        y: Math.floor(rng() * MAP_H),
        builtTick: i % 2 === 0 ? tick : null,
      });
    }
    const s = minimalState({ tick, buildings });
    assert.deepEqual(compareAllDerivations(s), []);
  }
});

// ---------------------------------------------------------------------------
// BUG-1024 — the pre-existing corpus's "mixed online/offline" claim was, in
// every fixture, offline for exactly ONE reason: mid-construction
// (builtTick === s.tick). isOnline's OTHER limb — road connectivity (the
// FEAT-1972079891 G2/G3 gates, which is what SectorAggregate.jobs's own
// doc comment claims to mirror) was never taken anywhere, including the
// 13k scale fixture (measured: 13,000/13,000 online there). isOnline's road
// gates only fire when construction is ALREADY complete (`s.tick -
// b.builtTick >= constructionTicks(sp)`) AND `s.roadConnectivity` is set —
// a hand-built state's buildings default to `builtTick: undefined`, which
// isOnline treats as "pre-dates the gate, always online" and skips the
// road check ENTIRELY (see data.ts's isOnline doc comment, "BACKWARD
// TOLERANCE"). This corpus city therefore deliberately gives every
// building a real, long-past `builtTick` and a real `s.roadConnectivity`
// (computed by data.ts's own computeRoadConnectivity, never hand-faked) so
// the connectivity limb is actually exercised, not merely claimed.
// ---------------------------------------------------------------------------
test('AC-18/BUG-1024 corpus: a disconnected-road city exercises isOnline\'s road-CONNECTIVITY limb, not just construction-time', () => {
  const LONG_PAST_TICK = 0;
  const NOW = 100_000; // far past any spec's constructionTicks — construction-time gate always passes here
  const buildings = [];
  let nextId = 1;

  // Component A: a road strip touching the map edge (x=0) -> CONNECTED.
  for (let x = 0; x <= 8; x++) buildings.push({ id: nextId++, spec: ROAD_SPEC, x, y: 0, builtTick: LONG_PAST_TICK });
  const connectedOffices = [];
  for (let x = 1; x <= 5; x++) {
    const id = nextId++;
    connectedOffices.push(id);
    buildings.push({ id, spec: JOB_SPEC, x, y: 1, builtTick: LONG_PAST_TICK }); // road-adjacent to (x,0)
  }

  // Component B: a road ISLAND far from any map edge/trunk tile -> road-adjacent, but NOT connected.
  const ISLAND_X0 = 200;
  const ISLAND_Y = 200;
  for (let dx = 0; dx <= 4; dx++) buildings.push({ id: nextId++, spec: ROAD_SPEC, x: ISLAND_X0 + dx, y: ISLAND_Y, builtTick: LONG_PAST_TICK });
  const islandOffices = [];
  for (let dx = 0; dx <= 4; dx++) {
    const id = nextId++;
    islandOffices.push(id);
    buildings.push({ id, spec: JOB_SPEC, x: ISLAND_X0 + dx, y: ISLAND_Y + 1, builtTick: LONG_PAST_TICK }); // road-adjacent to the island, island is unreachable from any seed
  }

  // A building with NO adjacent road at all -> offline via the road-ADJACENT gate (not even reached the connectivity check).
  const isolatedId = nextId++;
  buildings.push({ id: isolatedId, spec: JOB_SPEC, x: 300, y: 300, builtTick: LONG_PAST_TICK });

  let s = minimalState({ tick: NOW, nextId, buildings });
  // Real computation, never hand-faked — the SAME function isOnline's own
  // gate relies on (data.ts's computeRoadConnectivity, AC-1's flood-fill).
  s = { ...s, roadConnectivity: computeRoadConnectivity(s) };

  const byId = new Map(s.buildings.map((b) => [b.id, b]));
  for (const id of connectedOffices) {
    const b = byId.get(id);
    assert.equal(isOnline(s, b), true, `connected-component office ${id} must be ONLINE`);
    assert.ok(buildingJobsOf(SPECS[JOB_SPEC], b) > 0, 'sanity: the office spec really does carry jobs');
  }
  let offlineViaConnectivity = 0;
  for (const id of islandOffices) {
    const b = byId.get(id);
    // Directly probe the gate this corpus exists to exercise: road-adjacent (true) but NOT road-connected (false).
    assert.equal(isOnline(s, b), false, `island office ${id} must be OFFLINE via the road-CONNECTIVITY limb, not the adjacency limb`);
    offlineViaConnectivity++;
  }
  assert.ok(offlineViaConnectivity >= 5, `expected >=5 buildings offline through the connectivity limb, got ${offlineViaConnectivity}`);
  assert.equal(isOnline(s, byId.get(isolatedId)), false, 'the fully isolated building must also be offline (via road-adjacency, the OTHER gate)');

  // The actual AC-18 claim: fold and walk still agree in a city whose
  // isOnline gating runs through BOTH road gates, not just construction-time.
  assert.deepEqual(compareAllDerivations(s), []);
});

// ---------------------------------------------------------------------------
// BUG-1020 — the corpus never included a VALID (integer, non-negative)
// jobsOverride city, so the fold-vs-walk comparison never actually
// exercised effectiveJobsOf's jobsOverride branch of buildingJobsOf at all.
// (The FRACTIONAL/divergent case is deliberately NOT "fixed" here — it is
// the round's own documented, still-open fact, pinned permanently in
// attack-feat764-round.test.mjs's "byte-identity is CONDITIONAL" test.)
// ---------------------------------------------------------------------------
test('AC-18/BUG-1020 corpus: a city with valid (integer, non-negative) jobsOverride buildings still agrees', () => {
  const rng = mulberry32(1020);
  const buildings = [];
  for (let i = 1; i <= 600; i++) {
    const x = Math.floor(rng() * MAP_W);
    const y = Math.floor(rng() * MAP_H);
    const jobsOverride = Math.floor(rng() * 50); // integer, >= 0 — the contract coerceBuildingJobsOverride now enforces at the storage boundary
    buildings.push({ id: i, spec: JOB_SPEC, x, y, builtTick: null, jobsOverride });
  }
  const s = minimalState({ nextId: 601, buildings });
  assert.ok(totalJobsWholeCity(s) > 0, 'sanity: jobsOverride buildings must actually contribute jobs, or this pin is vacuous');
  assert.deepEqual(compareAllDerivations(s), []);
});

test('AC-18 corpus: the scale fixture (test/scale/fixture.mjs, ~13k buildings / 1.4M population)', (t) => {
  const s = buildScaleFixture();
  const mismatches = compareAllDerivations(s);
  assert.deepEqual(mismatches, []);
  t.diagnostic(`scale fixture: ${s.buildings.length} buildings, totalJobs=${totalJobs(s)}`);
});

// ---------------------------------------------------------------------------
// AC-18 item 3: Aaron's private savepoint, env-gated (METRO_AARON_SAVEPOINT),
// skipped when unset — this file must never read or commit that path itself.
// ---------------------------------------------------------------------------
test('AC-18 corpus: Aaron\'s real savepoint (env-gated, skipped when unset)', async (t) => {
  const savepointPath = process.env.METRO_AARON_SAVEPOINT;
  if (!savepointPath) {
    t.skip('METRO_AARON_SAVEPOINT not set — this corpus entry only runs locally against Aaron\'s own machine');
    return;
  }
  const { readFile } = await import('node:fs/promises');
  const { decode } = await import('../src/sim/saveCodec.ts');
  const raw = await readFile(savepointPath, 'utf8');
  const s = decode(raw);
  const mismatches = compareAllDerivations(s);
  assert.deepEqual(mismatches, []);
  t.diagnostic(`Aaron's savepoint: ${s.buildings.length} buildings, totalJobs=${totalJobs(s)}`);
});

// ---------------------------------------------------------------------------
// R6 — a diagnostic-only timing of the cold sectorIndexOf build. NEVER an
// assertion bound (perf bounds live in the scale-gate, not here).
// ---------------------------------------------------------------------------
test('R6 diagnostic: cold sectorIndexOf build time on the scale fixture (not asserted)', (t) => {
  const s = buildScaleFixture();
  const t0 = performance.now();
  const index = sectorIndexOf(s);
  const elapsedMs = performance.now() - t0;
  t.diagnostic(`cold sectorIndexOf build: ${elapsedMs.toFixed(2)} ms over ${s.buildings.length} buildings, ${index.size} occupied sectors`);
  assert.ok(index.size > 0, 'sanity only — the timing above is diagnostic, not a bound');
});

// ---------------------------------------------------------------------------
// Diagnostic (not asserted): measured cost of each NOT_PARTITIONED entry
// against the scale fixture, so AC-17's "residue is visible" claim is
// backed by a live number rather than a stale hardcoded one (GR#15).
// ---------------------------------------------------------------------------
test('R6/AC-17 diagnostic: measured cost of each NOT_PARTITIONED entry (not asserted)', async (t) => {
  const s = buildScaleFixture();
  const dataModule = await import('../src/sim/data.ts');
  const engineModule = await import('../src/sim/engine.ts');
  const callableByName = {
    crimeRateOf: dataModule.crimeRateOf,
    demandFixPlan: dataModule.demandFixPlan,
    stationLinks: dataModule.stationLinks,
    lineUsageOf: dataModule.lineUsageOf,
    congestionLinesOf: dataModule.congestionLinesOf,
    computeRoadConnectivity: dataModule.computeRoadConnectivity,
    buildingDisplayStates: dataModule.buildingDisplayStates,
    computeFlows: engineModule.computeFlows,
    wellbeingOf: engineModule.wellbeingOf,
  };
  for (const entry of NOT_PARTITIONED) {
    const fn = callableByName[entry.name];
    if (typeof fn !== 'function') {
      t.diagnostic(`${entry.name}: not directly callable with (s) alone in this harness — cost not measured this pass`);
      continue;
    }
    const t0 = performance.now();
    try {
      fn(s);
    } catch (err) {
      t.diagnostic(`${entry.name}: threw during measurement (${err.message}) — cost not usable`);
      continue;
    }
    const elapsedMs = performance.now() - t0;
    t.diagnostic(`${entry.name}: ${elapsedMs.toFixed(3)} ms on the ${s.buildings.length}-building scale fixture`);
  }
});

// ---------------------------------------------------------------------------
// BUG-1025 — partition-differential.mjs's canonicalSerialize had NO test of
// its own before the round (nothing imported it except compareAllDerivations
// internally). Round pins in attack-feat764-round.test.mjs cover the -0/0/
// NaN/plain-object-key-order properties AC-18 names; this suite adds the
// nested Map/Set + array cases the round flagged as untested (harmless for
// inc1's numbers-only table, but load-bearing once inc3/inc5 grow it to
// object-shaped rows).
// ---------------------------------------------------------------------------
test('BUG-1025: canonicalSerialize distinguishes -0/0/NaN and normalises plain-object key order', () => {
  assert.equal(canonicalSerialize(-0), '-0');
  assert.equal(canonicalSerialize(0), '0');
  assert.notEqual(canonicalSerialize(-0), canonicalSerialize(0));
  assert.equal(canonicalSerialize(NaN), 'NaN');
  assert.notEqual(canonicalSerialize(NaN), canonicalSerialize(0));
  assert.equal(canonicalSerialize({ z: 1, a: 2, m: 3 }), canonicalSerialize({ a: 2, m: 3, z: 1 }), 'plain-object key order must not affect the serialisation');
});

test('BUG-1025: canonicalSerialize preserves Map/Set INSERTION order and recurses into NESTED structures', () => {
  const mapA = new Map([[1, 'x'], [2, 'y']]);
  const mapB = new Map([[2, 'y'], [1, 'x']]); // same entries, different insertion order
  assert.notEqual(canonicalSerialize(mapA), canonicalSerialize(mapB), 'Map insertion order is deliberately significant here (AC-20 covers fold ORDER independence separately)');
  assert.equal(canonicalSerialize(mapA), canonicalSerialize(new Map([[1, 'x'], [2, 'y']])), 'identical insertion order must serialise identically');

  const setA = new Set([1, 2, 3]);
  const setB = new Set([3, 2, 1]);
  assert.notEqual(canonicalSerialize(setA), canonicalSerialize(setB));

  // Nested: a Map whose values are arrays of objects, and an array of Maps —
  // both directions of nesting the eventual inc3/inc5 DERIVATIONS rows will need.
  const nested1 = new Map([['a', [{ x: 1, y: 2 }, { y: -0 }]]]);
  const nested2 = new Map([['a', [{ y: 2, x: 1 }, { y: -0 }]]]); // inner object key order shuffled, values identical
  assert.equal(canonicalSerialize(nested1), canonicalSerialize(nested2), 'nested plain-object key order still must not matter');

  const arrayOfMaps1 = [new Map([[1, 'a']]), new Map([[2, 'b']])];
  const arrayOfMaps2 = [new Map([[1, 'a']]), new Map([[2, 'b']])];
  assert.equal(canonicalSerialize(arrayOfMaps1), canonicalSerialize(arrayOfMaps2));
  const arrayOfMapsReordered = [new Map([[2, 'b']]), new Map([[1, 'a']])];
  assert.notEqual(canonicalSerialize(arrayOfMaps1), canonicalSerialize(arrayOfMapsReordered), 'array ELEMENT order is significant (arrays are not sorted, unlike plain-object keys)');
});

// ---------------------------------------------------------------------------
// BUG-1023 — sectorKeyOf must fail-closed (MET-V965) rather than silently
// ALIAS an off-map/negative/non-integer origin into a legitimate-looking
// neighbouring sector key.
// ---------------------------------------------------------------------------
test('BUG-1023: sectorKeyOf rejects out-of-bounds, negative and non-integer tiles (MET-V965) instead of aliasing', () => {
  const isMetV962 = (err) => err instanceof Error && err.code === 'MET-V965';
  assert.throws(() => sectorKeyOf(MAP_W, 0), isMetV962, 'an off-map x must be rejected, not aliased into row 1');
  assert.throws(() => sectorKeyOf(MAP_W + 100, 0), isMetV962, 'the round\'s exact repro: this used to silently return a legitimate-looking key 36');
  assert.throws(() => sectorKeyOf(-1, 0), isMetV962, 'a negative x must be rejected');
  assert.throws(() => sectorKeyOf(0, -1), isMetV962, 'a negative y must be rejected');
  assert.throws(() => sectorKeyOf(NaN, 0), isMetV962, 'NaN must be rejected, not coalesced into one shared Map key');
  assert.throws(() => sectorKeyOf(1.5, 0), isMetV962, 'a non-integer tile must be rejected');
  assert.throws(() => sectorKeyOf(0, MAP_H), isMetV962, 'an off-map y must be rejected');
  // Sanity: the legitimate boundary values immediately inside the map still work.
  assert.equal(sectorKeyOf(MAP_W - 1, 0), sectorKeyOf(MAP_W - 1, 0));
  assert.doesNotThrow(() => sectorKeyOf(MAP_W - 1, MAP_H - 1));
});
