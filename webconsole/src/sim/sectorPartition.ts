// sectorPartition.ts — FEAT-2326609764 inc1, SECTOR PARTITION SKELETON.
//
// Scope (Aaron's lead ruling on the acceptance doc
// docs/planning/acceptance/FEAT-2326609764.md, 2026-09-11): this is the
// FIRST increment of the "SPATIAL-PARTITION TICK" feature (§10 inc1 row).
// It lands the sector grid, one memoised cold-build index, and re-expresses
// exactly ONE derivation (totalJobs, data.ts) as an integer fold over that
// index — all behind `PARTITIONED_DERIVATIONS`, default OFF, so this file
// changes ZERO runtime behaviour today. Dirty tracking (inc2), the Layer
// S/G split (inc3), road-diff re-gating (inc4) and the remaining
// derivations (inc5) are NOT built here — see the doc's §10 table.
//
// LEAD RULING R1 (2026-09-11, overturning the doc's AC-1/AC-3 as written):
// `TILE_METRES` and a "consolidator section" constant already exist as
// REAL exports of consolidator.ts (TILE_METRES = 50,
// CONSOLIDATOR_SECTION_METRES = 800, with a runtime override via
// sectionMetresOf(s)) — NOT as data.ts exports the doc assumed. Consuming
// consolidator.ts's TILE_METRES here (rather than re-declaring a second
// `50`) is the GR#3 single-definition move. The doc's AC-3 "sectors nest
// exactly N x into sections" invariant is DROPPED outright: section size is
// now a per-state runtime slider (sectionMetresOf), so no compile-time
// nesting ratio can hold in general. SECTOR_METRES is therefore an
// INDEPENDENT constant with its own cost-model derivation (AC-2, below),
// unrelated to the consolidator's section size, and `sectorKeyOf` MUST
// NEVER read `sectionMetresOf` — enforced structurally by
// test/sectorPartition.test.mjs (a source-text grep, the astgate/units-lint
// precedent for "assert an import/read never happens" rather than trusting
// a comment).
//
// GOLDEN RULE #21 (determinism): every function below is a pure fold over
// SimState + the SPECS catalogue. No Date.now/performance.now/Math.random/
// localStorage. No `for (const x of someMap) { ...; break; }` — folds walk
// an explicitly sorted array (see foldCityJobs), never a bare Map iteration
// with early exit.
//
// GOLDEN RULE #3 (single source of truth): the per-building jobs quantity
// this file sums is computed by calling the EXACT SAME per-building
// function the whole-city path uses (`buildingJobsOf`, extracted from
// data.ts's totalJobs() body by this same commit) — two independent
// re-derivations of "how many jobs does this building have" is exactly the
// drift class GR#3 exists to prevent, and is also why AC-18's differential
// harness can prove byte-identity rather than "close enough".

import type { SimState } from './types.ts';
import { TILE_METRES } from './consolidator.ts';
import { MAP_W, MAP_H } from './grid.ts';
import { codedError } from './backend.ts';
// FEAT-2326609764 inc1: data.ts imports PARTITIONED_DERIVATIONS/
// sectorIndexOf/foldCityJobs from THIS file (totalJobs()'s flag branch),
// so this is a real two-way cycle — the SAME function-only (call-time)
// cyclic import pattern data.ts's own header already documents for
// specUnlocked/familyKeyOf (see data.ts's import of this file). Every
// binding pulled in below is either a hoisted `function` declaration
// (memoOnState, buildingJobsOf, isOnline — all safe regardless of module
// evaluation order) or, for SPECS, read only from inside a function BODY
// (buildSectorIndex, never at this module's own top-level), so the cycle
// resolves safely under ESM's live-binding semantics.
import { memoOnState, buildingJobsOf, isOnline, SPECS } from './data.ts';

// ---------------------------------------------------------------------------
// §1 Sector geometry — AC-1/AC-2/AC-4 (as amended by lead ruling R1 above)
// ---------------------------------------------------------------------------

/**
 * AC-2 — the 1000 m sector size, derived from the per-tick cost model, NOT
 * picked. Reproduced from the acceptance doc so a future tuner can
 * re-derive rather than guess (the scale-gate.test.mjs BOUND DERIVATION
 * header is the precedent this follows).
 *
 * BUG-1022 FIX (round opus-round-feat764-inc1, P3): the previous version of
 * this comment dropped the c/f ratio from the minimisation and understated
 * the resulting optimum by ~39%. Corrected below — the algebra, not just
 * the number, was wrong.
 *
 * Per-tick cost under the sector-fold design is
 *
 *   C(S) = D * (N / S_occ) * c + S_occ * f
 *
 * where N = buildings, S_occ = occupied sectors, D = dirty sectors this
 * tick, c = 1.853 µs/building (measured, BUG-643 commit e5e323b, §1.1 of
 * the acceptance doc), f = per-sector fold cost (ASM-1488, ~0.5 µs,
 * UNMEASURED — inc1 was asked to measure it and did not (the R6 diagnostic
 * in test/sectorPartition.test.mjs times sectorIndexOf's cold BUILD, not
 * the fold's own per-sector cost); treat f, and therefore S* below, as
 * PROVISIONAL pending inc3's real measurement. Substituting S_occ = N/S
 * (S = buildings-per-sector) and minimising over S:
 *
 *   C(S) = D*N*c/S + S*f
 *   dC/dS = -D*N*c/S^2 + f = 0
 *   S* = sqrt(D * N * c / f)   -- NOT sqrt(D * N); the c/f ratio does not
 *                                 cancel, and a per-BUILDING cost (c) and a
 *                                 per-SECTOR cost (f) are not dimensionally
 *                                 interchangeable, so dropping the ratio
 *                                 was also dimensionally unsound, not just
 *                                 numerically wrong.
 *
 * At the ×50-map target scale (N ~= 1.5M buildings, D ~= 100 dirty sectors
 * under sustained building, ASM-1487) and c/f = 1.853/0.5 ~= 3.706:
 * S* = sqrt(100 * 1,500,000 * 3.706) ~= 23,578 buildings-per-occupied-sector
 * (not 12,247 — the uncorrected sqrt(D*N) figure). A 1000 m sector on the
 * ×50 grid (~3,111 x 1,838 tiles) yields ceil(3111/20) * ceil(1838/20) =
 * 156 * 92 = 14,352 sectors, i.e. mean ~104 buildings/occupied sector at
 * N=1.5M — that is ~61% of the (provisional) optimum S*, roughly 39% BELOW
 * it, not "within 17%" as the doc previously (and wrongly) claimed. The
 * cost curve is flat near its minimum and f is unmeasured, so 1000 m may
 * still turn out fine — but that has to be re-verified once inc3 actually
 * measures f, not asserted from the wrong formula. SECTOR_TILES is tied to
 * LAND, not building count, so mean buildings/sector stays roughly
 * constant across scale in the doc's table (§3 AC-2) — the property that
 * makes one fixed constant defensible at all, unlike the consolidator's
 * section size — but "defensible in shape" and "within 17% of the real
 * optimum" are two different claims, and only the first one currently
 * holds.
 */
export const SECTOR_METRES = 1000;

/** DERIVED, never a bare literal (GR#15). At today's constants this is exactly 1000 / 50 = 20. */
export const SECTOR_TILES = Math.round(SECTOR_METRES / TILE_METRES);

/** Sector grid dimensions, derived from the map size and SECTOR_TILES (never hand-computed). Edge sectors are partial (clipped to the map boundary), mirroring consolidator.ts's sectionOriginOf. */
export const SECTORS_X = Math.ceil(MAP_W / SECTOR_TILES);
export const SECTORS_Y = Math.ceil(MAP_H / SECTOR_TILES);
export const TOTAL_SECTORS = SECTORS_X * SECTORS_Y;

/**
 * Deterministic, integer sector key for a tile — raster order, ascending in
 * both x and y (mirrors consolidator.ts's sectionKeyOf exactly, at a
 * DIFFERENT and INDEPENDENT grid size — see the file header). A building
 * spanning a sector boundary is owned by the sector containing its ORIGIN
 * tile (b.x, b.y) — AC-4's adopted convention, the same rule ASM-1493 sets
 * for consolidator sections.
 *
 * STRUCTURAL INVARIANT (asserted by test/sectorPartition.test.mjs via
 * source-text inspection): this function reads ONLY SECTOR_TILES/SECTORS_X,
 * both compile-time constants — never `sectionMetresOf(s)` or any other
 * per-state value. The sector grid is fixed for the life of a build; only
 * the (unrelated) consolidator section grid is player-adjustable.
 *
 * BUG-1023 (round finding, P3): bounds-checked, fail-closed. Before this
 * fix `Math.floor` alone had no bounds check at all — an off-map origin
 * (`x >= MAP_W`, negative x/y) does not fail, it ALIASES into a
 * DIFFERENT, legitimate-looking sector's aggregate (measured on the live
 * grid: sectorKeyOf(MAP_W + 100, 0) returns 36, a real key one row down —
 * indistinguishable from an honest building placed there), and a
 * non-integer/NaN input mints a key outside [0, TOTAL_SECTORS) that no
 * structural gate can ever match (every NaN input coalesces to the SAME
 * Map key too). Harmless for inc1's grand-total fold (bucketing-blind by
 * construction), but a live hazard for inc2's dirty-sector side-band,
 * where an aliased key would dirty the WRONG sector. Every real building
 * origin is already guaranteed integer and in-bounds by the placement
 * reducer, so this throws MET-V965 rather than silently clamping — a
 * violation here means an upstream caller handed in bad data and the
 * caller needs fixing, not this function.
 */
export function sectorKeyOf(x: number, y: number): number {
  if (!Number.isInteger(x) || !Number.isInteger(y) || x < 0 || y < 0 || x >= MAP_W || y >= MAP_H) {
    throw codedError('MET-V965', `sectorKeyOf received an out-of-bounds or non-integer tile (${x}, ${y})`);
  }
  const sx = Math.floor(x / SECTOR_TILES);
  const sy = Math.floor(y / SECTOR_TILES);
  return sy * SECTORS_X + sx;
}

/** The origin tile of a sector, from its key. Inverse of sectorKeyOf's bucketing. */
export function sectorOriginOf(key: number): { x0: number; y0: number } {
  const sx = key % SECTORS_X;
  const sy = Math.floor(key / SECTORS_X);
  return { x0: sx * SECTOR_TILES, y0: sy * SECTOR_TILES };
}

// ---------------------------------------------------------------------------
// §2 SectorAggregate — AC-5 (inc1-scoped: jobs + buildingCount only)
// ---------------------------------------------------------------------------

/**
 * AC-5's explicit, inspectable, integer-domain record — INC1-SCOPED.
 * Every numeric field here is an integer (AC-16); `jobs` is gated by
 * isOnline() exactly like data.ts's totalJobs(), because that is the one
 * derivation this increment re-expresses.
 *
 * Later increments (see docs/planning/acceptance/FEAT-2326609764.md §10)
 * add, in order: tilesUsed, countByKind, residentsCapacity, jobsBySector
 * (per-wage-sector, not just the grand total), childrenCapacity,
 * servedCapacity, serviceCapacity (nursery/primary/tertiary/gp/hosp/
 * police/fire/clean/waste), parksCapacity, powerCap/powerNeed,
 * waterCleanCap/waterWasteCap, wasteGenerated, collectionCapacity,
 * processCapacities (inc3's Layer S) — then the IDENTICAL shape restricted
 * to isOnline() buildings as Layer G (AC-9), and builtAtGlobalVersions
 * (AC-11, inc3). None of that lands in inc1; adding a field without
 * updating this comment is a doc-drift smell, not a build break.
 */
export interface SectorAggregate {
  key: number;
  x0: number;
  y0: number;
  buildingCount: number;
  /** Sum of buildingJobsOf(sp, b) over this sector's buildings, gated by isOnline() — the same gate totalJobs() applies (BUG-525). */
  jobs: number;
}

// ---------------------------------------------------------------------------
// §3 The sector index — AC-4: one memoised walk, occupied sectors only
// ---------------------------------------------------------------------------

function buildSectorIndex(s: SimState): ReadonlyMap<number, SectorAggregate> {
  // AC-4: ONE walk over s.buildings, bucketing by the ORIGIN tile's sector
  // key. Order-independent accumulation (GR#21) — insertion order into the
  // Map does not affect any field's value, only iteration order, and every
  // read of this index (foldCityJobs) re-sorts by key before folding.
  const bySector = new Map<number, SectorAggregate>();
  for (const b of s.buildings) {
    const key = sectorKeyOf(b.x, b.y);
    let agg = bySector.get(key);
    if (!agg) {
      const { x0, y0 } = sectorOriginOf(key);
      agg = { key, x0, y0, buildingCount: 0, jobs: 0 };
      bySector.set(key, agg);
    }
    agg.buildingCount += 1;
    if (isOnline(s, b)) {
      const sp = SPECS[b.spec];
      if (sp) agg.jobs += buildingJobsOf(sp, b);
    }
  }
  return bySector;
}

/**
 * AC-4 — memoOnState-wrapped so a repeated call against the SAME SimState
 * object identity is O(1) after the first (identical idiom to
 * data.ts's totalJobs/residentsCapacity/etc). Sectors with zero buildings
 * are absent from the returned map — O(occupied sectors), never
 * O(TOTAL_SECTORS).
 */
export const sectorIndexOf: (s: SimState) => ReadonlyMap<number, SectorAggregate> = memoOnState(buildSectorIndex);

// ---------------------------------------------------------------------------
// §4 The fold — AC-14 (inc1-scoped: totalJobs only)
// ---------------------------------------------------------------------------

/**
 * AC-14/AC-16 — folds `jobs` over the sector index in EXPLICIT ascending
 * key order (never a bare Map iteration, GR#21). Because every
 * SectorAggregate.jobs value is an integer (AC-16 — IEEE-754 doubles are
 * exact for every integer up to 2^53, and integer addition within that
 * range is exactly associative and commutative), this fold's result is
 * provably independent of iteration order — proven directly by
 * test/sectorPartition.test.mjs's order-independence test (AC-20), not
 * merely reasoned about here.
 */
export function foldCityJobs(index: ReadonlyMap<number, SectorAggregate>): number {
  const keys = Array.from(index.keys()).sort((a, b) => a - b);
  let total = 0;
  for (const k of keys) {
    const agg = index.get(k);
    if (agg) total += agg.jobs;
  }
  return total;
}

/**
 * BUG-1019 FIX (round opus-round-feat764-inc1 REJECT, lead ruling
 * 2026-09-11): the fold's OWN entry point, named and exported to mirror
 * data.ts's totalJobsWholeCity exactly — the pair the AC-18 differential
 * harness now compares DIRECTLY, instead of going through totalJobs()'s
 * flag dispatch (which is what made the harness compare the fold against
 * itself once the flag flipped ON; see the round's finding).
 *
 * BUG-1020 (P2, same round): the integer-domain guard buildingJobsOf's own
 * doc comment claims ("always an integer", AC-16/AC-17) rests on but never
 * enforced. Checked HERE, once per sector aggregate rather than once per
 * building inside buildSectorIndex, so a non-integer can never silently
 * ship through the fold as a slightly-wrong number — it throws the
 * registered, fail-closed MET-V964 instead. This guard applies ONLY to
 * this entry point (the one totalJobs() actually dispatches to when the
 * flag is on) — sectorIndexOf/foldCityJobs stay raw, ungated composable
 * primitives so the differential harness and the round's own attack suite
 * (which deliberately documents the flag-off, non-integer-jobsOverride
 * divergence as a KNOWN, unfixed fact — attack-feat764-round.test.mjs's
 * "byte-identity is CONDITIONAL" pin) keep working unchanged.
 */
export function totalJobsPartitioned(s: SimState): number {
  const index = sectorIndexOf(s);
  for (const agg of index.values()) {
    if (!Number.isInteger(agg.jobs)) {
      throw codedError('MET-V964', `Sector ${agg.key} produced a non-integer jobs aggregate (${agg.jobs}) while folding sectorIndexOf`);
    }
  }
  return foldCityJobs(index);
}

// ---------------------------------------------------------------------------
// §5 The flag — inc1 ships this default OFF (§10 of the acceptance doc)
// ---------------------------------------------------------------------------

const DEFAULT_PARTITIONED_DERIVATIONS = false;

/**
 * Default OFF for the whole of inc1: flipping this to `true` makes
 * data.ts's totalJobs() return totalJobsPartitioned(s) instead of
 * totalJobsWholeCity(s). Both paths are exercised by
 * test/partition-differential.mjs regardless of this flag's value — the
 * harness always runs both named implementations directly and compares
 * them (BUG-1019 fix); only the SHIPPED runtime behaviour is gated by this
 * constant. inc6 flips the default to ON (see the acceptance doc's §10
 * increment table); this file is not the place that flip happens — this
 * module-level `let` (mutable ONLY via the test seam immediately below)
 * remains the single source of truth for what ships, read live by data.ts's
 * totalJobs() on every call via the ES-module live-binding semantics
 * documented on this file's own header and on data.ts's import of it.
 */
export let PARTITIONED_DERIVATIONS: boolean = DEFAULT_PARTITIONED_DERIVATIONS;

/**
 * TEST SEAM (BUG-1019 lead ruling, point 1: "provide a test seam so a test
 * can force the flag ON"). Lets a test exercise totalJobs()'s ACTUAL
 * dispatch to totalJobsPartitioned without ever letting a real build ship
 * with the flag flipped — guarded fail-closed by NODE_TEST_CONTEXT, the
 * SAME env var `node --test` sets automatically for every file it runs
 * (established idiom, see store.tsx's own NODE_TEST_CONTEXT checks) so this
 * can never be called from a shipped (non-test) process. Every test that
 * calls this MUST reset the flag back to DEFAULT_PARTITIONED_DERIVATIONS
 * (e.g. in a try/finally) before returning — this module-level `let` is
 * shared across every test file `node --test` runs in the same worker.
 */
export function __setPartitionedDerivationsForTest(value: boolean): void {
  if (typeof process === 'undefined' || !process.env?.NODE_TEST_CONTEXT) {
    throw new Error('__setPartitionedDerivationsForTest may only be called under NODE_TEST_CONTEXT (node --test)');
  }
  PARTITIONED_DERIVATIONS = value;
}

// ---------------------------------------------------------------------------
// §6 NOT_PARTITIONED — AC-17's published residue list
// ---------------------------------------------------------------------------

/**
 * AC-17 — derivations from the acceptance doc's §1.3 inventory that are NOT
 * simple integer sector-folds, published here so the residue is visible
 * rather than discovered late. This is the doc's own §10 inc5 "residue"
 * list (per-building maps and non-additive per-entity structures) plus the
 * two functions AC-12/AC-9 explicitly keep off the fold path:
 *
 *   - computeRoadConnectivity stays a flood-fill over road tiles, UNCHANGED
 *     by this feature (AC-12: "the flood-fill remains O(road tiles) and is
 *     unchanged by this item").
 *   - computeFlows reads folded totals but is itself a per-tick multi-line
 *     money computation, not a single scalar sum — it consumes fold
 *     OUTPUTS (totalJobs et al.) rather than being one itself.
 *
 * Each entry's ACTUAL cost is measured at test time (never hardcoded here —
 * GR#15: a stale baked-in number would silently drift from the real
 * catalogue/fixture) by
 * test/sectorPartition.test.mjs's `measureNotPartitionedCosts` diagnostic,
 * which times every entry against test/scale/fixture.mjs and logs the
 * result (t.diagnostic — never an assertion bound, per R6/AC-15's own
 * "diagnostic-only" posture).
 */
export interface NotPartitionedEntry {
  name: string;
  reason: string;
}

export const NOT_PARTITIONED: readonly NotPartitionedEntry[] = Object.freeze([
  {
    name: 'buildingDisplayStates',
    reason:
      'a per-BUILDING Map (id -> display state), not a citywide scalar sum — no fold target exists. Doc §10 inc5: made lazy/viewport-scoped instead of sector-folded.',
  },
  {
    name: 'crimeRateOf',
    reason:
      'a spatial RATE derived from crime-generating and crime-suppressing influences over a radius, not an additive per-building quantity. Doc §10 inc5 residue.',
  },
  {
    name: 'wellbeingOf',
    reason:
      'a composite weighted blend of many non-integer factors (engine.ts:4970), not a sum of per-building integers. Doc §10 inc5 residue.',
  },
  {
    name: 'demandFixPlan',
    reason:
      'an ORDERED remediation plan (a list of actions), not a scalar — folding has no meaning for a plan. Doc §10 inc5 residue.',
  },
  {
    name: 'stationLinks',
    reason:
      'a graph/topology structure over station buildings, not a per-sector additive quantity. Doc §10 inc5 residue.',
  },
  {
    name: 'lineUsageOf',
    reason:
      'a per-transit-LINE breakdown (keyed by line, not by sector) — the partition axis does not match sector geometry. Doc §10 inc5 residue.',
  },
  {
    name: 'congestionLinesOf',
    reason:
      'a per-line/per-segment structure, not a citywide scalar sum. Doc §10 inc5 residue.',
  },
  {
    name: 'computeRoadConnectivity',
    reason:
      'AC-12: the flood-fill over road tiles is explicitly kept UNCHANGED by this feature — road-topology dirtying re-GATES sectors, it does not replace the flood-fill itself.',
  },
  {
    name: 'computeFlows',
    reason:
      'a per-tick multi-line money computation that CONSUMES fold outputs (totalJobs, etc.) rather than being one — nothing to re-express as a sector fold on its own.',
  },
]);
