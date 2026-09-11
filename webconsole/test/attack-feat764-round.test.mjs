// attack-feat764-round.test.mjs — FEAT-2326609764 inc1 independent destructive
// round (attacker opus-round-feat764-inc1, 2026-09-11; GR#23: attacker != author).
//
// These are the round's LASTING pins: every one of them was written because a
// mutant survived the author's own suite (test/sectorPartition.test.mjs), or
// because a claim made in source prose had no executable backing. Each pin
// names the mutant it kills so a future edit can re-derive why it exists.
//
// The round's REJECT finding (the AC-18 harness going vacuous when
// PARTITIONED_DERIVATIONS is flipped ON — test/partition-differential.mjs's
// `whole` row calls totalJobs(), which itself reads the flag, so at inc6 the
// harness compares the fold against ITSELF) is not directly pinnable from here
// until the whole-city walk is exported flag-independently. What IS pinned here
// is the durable substitute: an INDEPENDENT whole-city oracle re-implemented in
// this file, which stays a real comparison at every flag value and at every
// later increment.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

import {
  SPECS,
  totalJobs,
  totalJobsWholeCity,
  buildingJobsOf,
  isOnline,
  coerceBuildingJobsOverride,
  coerceSnapshotBuildings,
} from '../src/sim/data.ts';
import { MAP_W, MAP_H } from '../src/sim/grid.ts';
import {
  SECTOR_TILES,
  SECTORS_X,
  sectorKeyOf,
  sectorOriginOf,
  sectorIndexOf,
  foldCityJobs,
  totalJobsPartitioned,
  PARTITIONED_DERIVATIONS,
  __setPartitionedDerivationsForTest,
} from '../src/sim/sectorPartition.ts';
import { canonicalSerialize, compareAllDerivations } from './partition-differential.mjs';

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

const SPEC_POOL = ['off_suite', 'com_shop', 'farm_wheat'];

function seededCity(seed, count, { tick = 50 } = {}) {
  const rng = mulberry32(seed);
  const buildings = [];
  for (let i = 1; i <= count; i++) {
    const spec = SPEC_POOL[Math.floor(rng() * SPEC_POOL.length)];
    const x = Math.floor(rng() * MAP_W);
    const y = Math.floor(rng() * MAP_H);
    const online = rng() > 0.3;
    buildings.push({ id: i, spec, x, y, capacityTier: Math.floor(rng() * 3), builtTick: online ? null : tick });
  }
  return minimalState({ tick, nextId: count + 1, buildings });
}

/**
 * The INDEPENDENT oracle. Deliberately re-derived here rather than imported,
 * because the imported `totalJobs` is flag-switched: at inc6 it BECOMES the
 * fold, and every comparison against it silently turns into a tautology (the
 * round proved this — with the flag forced ON, a `- 1` per-building defect in
 * buildSectorIndex left all 200 corpus cities, the edge-case fixtures and the
 * 13k scale fixture GREEN). This walk reads only SPECS + isOnline +
 * buildingJobsOf, never the partition, so it stays a real second opinion.
 */
function wholeCityJobsOracle(s) {
  let jobs = 0;
  for (const b of s.buildings) {
    if (!isOnline(s, b)) continue;
    const sp = SPECS[b.spec];
    if (!sp) continue;
    jobs += buildingJobsOf(sp, b);
  }
  return jobs;
}

test('ATTACK/oracle: the sector fold equals a FLAG-INDEPENDENT whole-city oracle (kills the off-by-one mutant at every flag value)', () => {
  for (const seed of [11, 22, 33, 44]) {
    const s = seededCity(seed, 400);
    const oracle = wholeCityJobsOracle(s);
    assert.ok(oracle > 0, `seed ${seed}: fixture must have nonzero jobs or the pin is vacuous`);
    assert.equal(Object.is(foldCityJobs(sectorIndexOf(s)), oracle), true, `seed ${seed}: fold must equal the independent oracle`);
    assert.equal(totalJobs(s), oracle, `seed ${seed}: the shipped totalJobs must agree with the oracle on whichever branch it takes`);
  }
});

test('ATTACK/AC-16 integer domain: buildingJobsOf returns an INTEGER for every building, and the live catalogue carries no fractional jobs', () => {
  // The prose on buildingJobsOf claims "always an integer". The author suite
  // only asserted Number.isSafeInteger on AGGREGATES of generated cities, which
  // cannot fail while the generator draws from the integer catalogue. Assert
  // the per-building claim itself, and the catalogue property it rests on.
  for (const [id, sp] of Object.entries(SPECS)) {
    if (sp.jobs != null) assert.ok(Number.isInteger(sp.jobs), `${id}: sp.jobs ${sp.jobs} must be an integer`);
    if (Array.isArray(sp.capacityTiers)) {
      for (const v of sp.capacityTiers) assert.ok(Number.isInteger(v), `${id}: capacityTiers entry ${v} must be an integer`);
    }
  }
  const s = seededCity(99, 1200);
  let checked = 0;
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (!sp) continue;
    const j = buildingJobsOf(sp, b);
    assert.ok(Number.isInteger(j), `building ${b.id} (${b.spec}) jobs ${j} must be an integer`);
    checked++;
  }
  assert.ok(checked > 1000, 'pin must actually have examined buildings');
});

test('ATTACK/AC-16: byte-identity is CONDITIONAL on that integer domain — a fractional jobs value makes the two paths disagree', () => {
  // Documents the round's P2 finding as an executable fact rather than prose:
  // `b.jobsOverride` is a persisted field with no integer coercion at any save
  // boundary, and the moment a non-integer reaches buildingJobsOf the fold and
  // the whole-city walk stop agreeing (same values, different summation ORDER).
  const rng = mulberry32(7);
  const buildings = [];
  for (let i = 1; i <= 4000; i++) {
    buildings.push({ id: i, spec: 'off_suite', x: Math.floor(rng() * MAP_W), y: Math.floor(rng() * MAP_H), builtTick: null, jobsOverride: 0.1 });
  }
  const s = minimalState({ buildings });
  const oracle = wholeCityJobsOracle(s);
  const fold = foldCityJobs(sectorIndexOf(s));
  assert.equal(Object.is(oracle, fold), false, 'if this ever becomes true the integer dependency has been removed or the fixture no longer spans sectors — re-derive this pin');
  assert.ok(Math.abs(oracle - fold) < 1e-6, 'the disagreement is float-accumulation drift, not a logic error');
});

test('ATTACK/AC-5: buildingCount counts OFFLINE buildings too (kills the "index skips offline buildings" mutant)', () => {
  const tick = 10;
  const s = minimalState({
    tick,
    buildings: [
      { id: 1, spec: 'off_suite', x: 0, y: 0, builtTick: null },
      { id: 2, spec: 'off_suite', x: 1, y: 1, builtTick: tick },
      { id: 3, spec: 'off_suite', x: 2, y: 2, builtTick: tick },
    ],
  });
  const agg = sectorIndexOf(s).get(sectorKeyOf(0, 0));
  assert.ok(agg, 'sector 0 must exist');
  assert.equal(agg.buildingCount, 3, 'buildingCount is a STRUCTURAL count (every building), not an online-gated one');
  assert.equal(agg.jobs, buildingJobsOf(SPECS['off_suite'], s.buildings[0]), 'only the online building contributes jobs');
  assert.ok(agg.jobs > 0, 'sanity: the online building really does carry jobs, or this pin is vacuous');
});

test('ATTACK/AC-5: x0/y0 are the sector ORIGIN tile (kills the "sectorOriginOf returns 0,0" mutant)', () => {
  const x = SECTOR_TILES * 3 + 5;
  const y = SECTOR_TILES * 2 + 7;
  const s = minimalState({ buildings: [{ id: 1, spec: 'off_suite', x, y, builtTick: null }] });
  const key = sectorKeyOf(x, y);
  const agg = sectorIndexOf(s).get(key);
  assert.ok(agg);
  assert.equal(agg.key, key);
  assert.equal(agg.x0, SECTOR_TILES * 3, 'x0 must be the sector origin, not 0');
  assert.equal(agg.y0, SECTOR_TILES * 2, 'y0 must be the sector origin, not 0');
  assert.deepEqual(sectorOriginOf(key), { x0: SECTOR_TILES * 3, y0: SECTOR_TILES * 2 });
});

test('ATTACK/GR#21: foldCityJobs itself visits keys in ASCENDING order (kills the "drop the sort" mutant)', () => {
  // The author's AC-20 test folds hand-built orderings of its own; it never
  // observes foldCityJobs's OWN iteration order, so deleting the sort survives.
  // Observe the order directly through a Map whose insertion order is scrambled.
  const visited = [];
  const fake = new Map();
  for (const key of [40, 3, 17, 9]) {
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
  assert.equal(total, 40 + 3 + 17 + 9);
  assert.deepEqual(visited, [3, 9, 17, 40], 'foldCityJobs must sort keys ascending before folding (GR#21)');
});

test('ATTACK/AC-4: sector keys are FLOOR-bucketed by the ORIGIN tile, checked against hand-computed keys (kills ceil / far-corner mutants without using sectorKeyOf as its own oracle)', () => {
  const cases = [
    [0, 0, 0],
    [SECTOR_TILES - 1, SECTOR_TILES - 1, 0],
    [SECTOR_TILES, 0, 1],
    [0, SECTOR_TILES, SECTORS_X],
    [SECTOR_TILES * 2 + 3, SECTOR_TILES * 3 + 4, 3 * SECTORS_X + 2],
  ];
  for (const [x, y, expected] of cases) {
    assert.equal(sectorKeyOf(x, y), expected, `sectorKeyOf(${x},${y})`);
  }
  const s = minimalState({
    buildings: [{ id: 1, spec: 'off_suite', x: SECTOR_TILES - 1, y: 0, footprintW: 10, footprintH: 10, builtTick: null }],
  });
  const index = sectorIndexOf(s);
  assert.equal(index.size, 1);
  assert.ok(index.has(0), 'the origin sector is 0, not the far-corner sector 1');
});

test('ATTACK/AC-18: canonicalSerialize really does separate -0, 0 and NaN (the harness claim, never previously asserted)', () => {
  assert.notEqual(canonicalSerialize(-0), canonicalSerialize(0));
  assert.equal(canonicalSerialize(-0), '-0');
  assert.equal(canonicalSerialize(NaN), 'NaN');
  assert.notEqual(canonicalSerialize(NaN), canonicalSerialize(0));
  assert.equal(canonicalSerialize({ b: 1, a: 2 }), canonicalSerialize({ a: 2, b: 1 }), 'plain-object key order must not matter');
  assert.notEqual(canonicalSerialize(new Map([[1, 2]])), canonicalSerialize(new Map([[2, 1]])));
});

test('ATTACK/AC-18: the seeded generator is reproducible from its seed ALONE', () => {
  const a = seededCity(4242, 300);
  const b = seededCity(4242, 300);
  assert.equal(canonicalSerialize(a.buildings), canonicalSerialize(b.buildings));
  const c = seededCity(4243, 300);
  assert.notEqual(canonicalSerialize(a.buildings), canonicalSerialize(c.buildings), 'different seeds must give different cities or the generator is seed-blind');
});

test('ATTACK/AC-21: nothing new is persisted — SimState gains no sector field', async () => {
  const types = await readFile(new URL('../src/sim/types.ts', import.meta.url), 'utf8');
  assert.ok(!/sector(Index|Aggregate|Key)/i.test(types), 'the partition must stay a derived side structure, never a SimState field');
});

// ===========================================================================
// ROUND 2 - opus-reround-feat764-inc1 (2026-09-11), after the r1 REJECT
// (BUG-1019) was reworked. The r1 pins above stay untouched; everything below
// was written because an r2 attack found something the reworked author suite
// does not cover.
// ===========================================================================

// ---------------------------------------------------------------------------
// BUG-1043 (r2 P2) - the BUG-1020 storage-boundary coercion is the ONLY thing
// standing between a persisted `jobsOverride` and buildingJobsOf's
// integer-domain contract, and NO test anywhere asserted what it actually
// does. This is that table, measured. Two holes are pinned as FACTS, not as
// aspirations: (a) `Math.trunc` does not clamp to a SAFE integer, so
// 2**53 / 1e21 sail through the coercion AND through totalJobsPartitioned's
// MET-V964 guard (which tests Number.isInteger, not Number.isSafeInteger)
// even though the author suite's own AC-16 pin asserts isSafeInteger; and
// (b) a WRONG-TYPE jobsOverride is rejected only on gamesave.ts's file/named-
// save path - the savepoint/replay/store hydrate path routes through
// coerceSnapshotBuildings, which passes a non-number through untouched.
// ---------------------------------------------------------------------------
test('ATTACK/BUG-1043: the jobsOverride storage-boundary coercion table (measured, including its two holes)', () => {
  const coerce = (v) => coerceBuildingJobsOverride({ id: 1, spec: 'off_suite', x: 0, y: 0, jobsOverride: v }, 0).jobsOverride;
  assert.equal(coerce(0.1), 0);
  assert.equal(coerce(12.9), 12);
  assert.equal(coerce(-1), 0);
  // MEASURED SUB-FINDING (BUG-1043): Math.trunc(-0.5) is -0, and the `< 0`
  // floor does not catch it (-0 < 0 is false), so the coercion's output for a
  // small negative fraction is NEGATIVE ZERO, not 0 - harmless for the fold
  // (integer addition of -0 is an identity, and JSON.stringify(-0) is "0" so
  // it never survives a save round-trip) but not what the helper's own doc
  // comment ("floored at 0") says it does.
  assert.equal(Object.is(coerce(-0.5), -0), true, 'BUG-1043: the negative-fraction case yields -0, not 0');
  assert.equal(coerce(NaN), 0);
  assert.equal(coerce(Infinity), 0);
  assert.equal(coerce(-Infinity), 0);
  const clean = { id: 1, spec: 'off_suite', x: 0, y: 0, jobsOverride: 7 };
  assert.equal(coerceBuildingJobsOverride(clean, 0), clean, 'a clean building must come back by identity (no churn)');
  assert.equal(coerce(0), 0);
  assert.equal(coerce(2 ** 53), 2 ** 53, 'BUG-1043: Math.trunc does not clamp to Number.MAX_SAFE_INTEGER');
  assert.equal(coerce(1e21), 1e21, 'BUG-1043: 1e21 is Number.isInteger-true and survives the coercion');
  const out = coerceSnapshotBuildings([{ id: 1, spec: 'off_suite', x: 0, y: 0, jobsOverride: '12' }]);
  assert.equal(out[0].jobsOverride, '12', 'BUG-1043: coerceSnapshotBuildings leaves a wrong-typed jobsOverride alone');
});

// ---------------------------------------------------------------------------
// BUG-1043 (r2) - the consequence, and a NON-VACUOUS dispatch pin in one.
// A city whose jobsOverride values are unsafe integers (the hole above) makes
// the fold and the whole-city walk genuinely DISAGREE, because integer
// addition stops being associative past 2**53. That gives the only fixture in
// this estate where totalJobs() returning the wrong branch is OBSERVABLE - so
// it doubles as the dispatch proof the author suite cannot make (its own
// seam test compares two values that agree on every clean fixture, and would
// stay green against a dispatch that ignored the flag entirely).
// ---------------------------------------------------------------------------
test('ATTACK/BUG-1043: an unsafe-integer city diverges, the harness REPORTS it, and totalJobs dispatches to the fold when the flag is ON', () => {
  const mk = (id, x, y, jobsOverride) => ({ id, spec: 'off_suite', x, y, builtTick: null, jobsOverride });
  const cityBuildings = () => [mk(1, 0, 0, 1), mk(2, SECTOR_TILES, 0, 2 ** 53), mk(3, 1, 1, 1)];
  const s = minimalState({ tick: 100, buildings: cityBuildings() });
  const walk = totalJobsWholeCity(s);
  const fold = totalJobsPartitioned(s);
  assert.equal(walk, 9007199254740992, 'the whole-city walk loses the 1 it was holding when 2**53 lands');
  assert.equal(fold, 9007199254740994, 'the fold sums each sector exactly, then folds');
  assert.equal(Object.is(walk, fold), false, 'BUG-1043: byte-identity is broken by an UNSAFE integer, not only by a fractional one');
  const mismatches = compareAllDerivations(minimalState({ tick: 100, buildings: cityBuildings() }));
  assert.equal(mismatches.length, 1, 'the AC-18 harness must report the divergence');
  assert.equal(mismatches[0].field, 'totalJobs');
  assert.equal(mismatches[0].whole, walk);
  assert.equal(mismatches[0].partitioned, fold);
  assert.ok(Number.isInteger(fold) && !Number.isSafeInteger(fold), 'the fold guard tests isInteger, so an unsafe integer passes it');
  assert.equal(PARTITIONED_DERIVATIONS, false, 'sanity: starts OFF');
  assert.equal(totalJobs(minimalState({ tick: 100, buildings: cityBuildings() })), walk, 'flag OFF: totalJobs must return the WHOLE-CITY value');
  __setPartitionedDerivationsForTest(true);
  try {
    assert.equal(totalJobs(minimalState({ tick: 100, buildings: cityBuildings() })), fold, 'flag ON: totalJobs must return the FOLD value (kills a dispatch that ignores the flag)');
  } finally {
    __setPartitionedDerivationsForTest(false);
  }
  assert.equal(PARTITIONED_DERIVATIONS, false, 'the flag must be back to its default');
});

// ---------------------------------------------------------------------------
// r2 - the seam cannot be reached from a shipped build, proven from BOTH
// sides: the guarded setter throws without NODE_TEST_CONTEXT (the author
// suite pins that), and the exported `let` itself is NOT writable by an
// importer, so there is no second route to flipping it.
// ---------------------------------------------------------------------------
test('ATTACK/BUG-1019: the flag has exactly ONE mutation route and it is the guarded seam', async () => {
  const mod = await import('../src/sim/sectorPartition.ts');
  assert.throws(() => { mod.PARTITIONED_DERIVATIONS = true; }, TypeError, 'an ESM live binding must be read-only to importers');
  assert.equal(mod.PARTITIONED_DERIVATIONS, false);
  const src = await readFile(new URL('../src/sim/sectorPartition.ts', import.meta.url), 'utf8');
  const assignments = src.match(/PARTITIONED_DERIVATIONS\s*=/g) || [];
  assert.equal(assignments.length, 2, 'exactly two assignments: the `export let` initialiser and the guarded seam');
  assert.ok(/NODE_TEST_CONTEXT/.test(src), 'the seam must stay env-guarded');
});
