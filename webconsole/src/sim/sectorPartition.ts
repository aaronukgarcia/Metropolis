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
// the ONE TILE_METRES definition here (rather than re-declaring a second
// `50`) is the GR#3 single-definition move; since BUG-1051 that definition
// lives in the dependency-free leaf grid.ts (consolidator.ts re-exports it)
// and must be imported from the leaf, never across the consolidator cycle. The doc's AC-3 "sectors nest
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
// BUG-1051 FIX (lead, port-time): TILE_METRES now lives in the LEAF
// webconsole/src/sim/grid.ts (consolidator.ts re-exports it for its own
// existing callers) — reading it via consolidator.ts here put a top-level
// binding read across the consolidator -> data -> sectorPartition import
// cycle, which is entry-order-sensitive (red on main after inc1 landed,
// depending on which module a given process happens to import FIRST).
// Reading it from the leaf module instead is entry-order-safe by
// construction — grid.ts imports nothing from this cycle.
import { MAP_W, MAP_H, TILE_METRES } from './grid.ts';
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
import { memoOnState, buildingJobsOf, isOnline, SPECS, constructionTicks } from './data.ts';

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
 * O(TOTAL_SECTORS). This is inc1's ORIGINAL binding, kept verbatim and
 * renamed (`sectorIndexOf` below is now the public entry point, a
 * dispatcher — see R2's doc comment) — this is the flag-OFF path AND the
 * base-index builder the flag-ON incremental path (§9) falls back to.
 */
const coldSectorIndexOf: (s: SimState) => ReadonlyMap<number, SectorAggregate> = memoOnState(buildSectorIndex);

// `sectorIndexOf`, the public entry point (flag OFF: coldSectorIndexOf;
// flag ON: incremental rebuild against the per-state side-band) is defined
// in §9 below, AFTER the dirty-tracking/schedule primitives it depends on —
// see that section's header for the R2/BUG-1064..1070 rework.

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

/**
 * R1's own allowlist requirement: "any reducer case not routed through
 * [markSectorsDirty/markAllSectorsDirty] yields null (=> full recompute) ...
 * or is listed in an explicit NOT_TRACKED allowlist with a reason." Every
 * engine.ts reducer case/branch that mutates `s.buildings` (or, for
 * pipeUpgrade, a field AC-8's table names) is either (a) wired to a
 * markSectorsDirty/markAllSectorsDirty call at its own call site — grep
 * engine.ts for "FEAT-2326609764 inc2" to find every one — or (b) listed
 * here. Every entry here commits `null` for its transition (AC-7's
 * fail-safe: full recompute, never a stale answer), which is SAFE by
 * construction; the entries exist so a reviewer can tell "deliberately
 * untracked, here is why" from "forgotten" at a glance, per R1.
 */
export interface NotTrackedEntry {
  /** The reducer case/action type, or internal function, that is not wired. */
  site: string;
  reason: string;
}

export const NOT_TRACKED: readonly NotTrackedEntry[] = Object.freeze([
  {
    site: "case 'sellAsset'",
    reason:
      'removes exactly one building, structurally identical to bulldoze — NOT wired because AC-8\'s table does not name it as a trigger row and the doc is the authority; falls back to null (full recompute), which is correct, just not optimised. Flagged as an open question for the lead: should sellAsset get the SAME precise-mark treatment bulldoze does?',
  },
  {
    site: "case 'enterAdministration'",
    reason: 'never touches s.buildings — only administrationState/bailoutState/insolvencyState fields. Nothing to mark.',
  },
  {
    site: "case 'consolidatorUndo' (undoLastConsolidatorPass)",
    reason:
      'restores a whole consolidator pass\'s worth of demolitions/builds, citywide, by construction — same shape as the consolidator pass itself (which IS wired via markAllSectorsDirty in advance()), but undo is player-invoked and rare (not a monthly tick-driven pass), so this was left on the null fallback rather than duplicating the pass-level wiring for an already-safe path.',
  },
  {
    site: "case 'hydrate' with action.source === 'tick' (the Web Worker tick-delivery path)",
    reason:
      'the ACTUAL tick computation runs inside a separate JS execution context (simWorker.ts) with its OWN independent module-level dirty side-band/completion schedule (WeakMaps and mutable module state do not cross a Web Worker boundary via postMessage) — the main thread receiving this hydrate has no way to know what the worker\'s reducer() dispatch actually marked dirty. Falls back to null (full recompute) on the main thread, which is SAFE but not fast. OPEN QUESTION for the lead: wiring this properly needs the worker to serialise its own dirty-key list as a plain array field on the hydrate action payload (WeakMaps/Sets cannot cross postMessage) — out of THIS increment\'s scope (the acceptance doc\'s authority list never names simWorker.ts), flagged rather than silently left for a later session to rediscover.',
  },
  {
    site: 'stampRegion / bulldoze / relocate / pipeUpgrade / place / placeMany / resolveDemand / resolveDemandAll / placeRoadPath / evaluateBuildingMonitors / evaluateRoadMonitors / sweepOrphanConnects / applyConsolidatorPass (via advance())',
    reason: 'WIRED — see the "FEAT-2326609764 inc2" comment at each call site in engine.ts. Listed here only so this file is the single place a reviewer can see the WHOLE picture (wired + deliberately not) in one read.',
  },
]);

// ---------------------------------------------------------------------------
// §7/§8/§9 — dirty tracking + completion schedule + incremental rebuild.
//
// REWORKED (2026-09-11) after inc2 r1 REJECT (BUG-1064..1070, round
// opus-round-feat764-inc2) and the lead's post-REJECT ruling. The r1 design
// had TWO module-global structures (`baseStateOf`, `completionSchedule`)
// that were not keyed by state at all, or keyed only by a raw future tick
// number — so a second live timeline in the SAME process (a genesis replay
// running alongside the live state, a savepoint loaded into a second slot,
// undo/redo branches, or simply two test fixtures) silently corrupted the
// first one's bookkeeping (BUG-1065), and `baseStateOf.set(next, prev)`
// created a WeakMap VALUE chain that retained every historical SimState for
// the life of the session (BUG-1066) — precisely the memory budget this
// feature exists to protect (see the BOW's "honest ceiling" comment).
//
// THE FIX: everything this feature needs to remember about a transition —
// the dirty set, the base index to patch, AND the completion schedule — now
// lives in ONE side-band record, in ONE WeakMap keyed by the STATE OBJECT
// itself (never by a raw tick number, never a value that points back to a
// PREVIOUS state):
//
//   sideBandOf: WeakMap<SimState, { dirty, baseIndex, schedule }>
//
// `commitPendingDirtySectors(next, prev)` reads `prev`'s OWN entry (never
// `prev` itself — see baseIndex below), derives `next`'s entry, and writes
// it against `next`. Two timelines that share a common ancestor but never
// share a SimState object again literally cannot see each other's writes —
// there is no third structure for them to collide in.
//
//   - `dirty`: `ReadonlySet<number> | 'FULL' | null`, exactly as before
//     (AC-6/AC-7). `'FULL'` — a case proved (or R3 conservatively assumed)
//     every sector is dirty; STICKY for the dispatch. `null` — nothing
//     narrowed it (AC-7's own fail-safe: full recompute).
//   - `baseIndex`: the PATCHABLE `sectorIndexOf(prev)` result — an actual
//     `ReadonlyMap<number, SectorAggregate>` of small POJOs, holding no
//     reference to any SimState — computed and stored ONLY when `dirty` is
//     a real Set (BUG-1066 fix: this is "prev's INDEX", never "prev
//     itself", so the chain that used to retain whole SimStates now retains
//     nothing but numbers).
//   - `schedule`: `ReadonlyMap<number, ReadonlySet<number>>`, copy-on-write
//     forward from `prev`'s own schedule on the TOUCHED tick keys only
//     (BUG-1065 fix: per-state, not module-global) — see §8 below.
//
// SINGLE CHOKE POINT (unchanged from r1): every reducer path that can leave
// this side-band stale funnels through `resetPendingDirtySectors(state)`
// (ONCE, top of `reducer()`, now takes the incoming state so the schedule's
// read baseline can be seeded from ITS side-band entry), the mark/schedule
// functions below (called by each AC-8 trigger's own case), and
// `commitPendingDirtySectors()` (ONCE, bottom of `reducer()`).
//
// FLAG-OFF COST (BUG-1069, second finding): every function below opens with
// `if (!PARTITIONED_DERIVATIONS) return` — a boolean check and NOTHING else.
// With the flag off, `sideBandOf` is never read or written, no Set/Map is
// ever allocated, and `coldSectorIndexOf` (the ONLY consumer with the flag
// off) never looks at any of this — matching AC-21's "flag off = zero
// runtime change" as a claim about COST too, not just behaviour.
// ---------------------------------------------------------------------------

export interface SectorSideBand {
  dirty: ReadonlySet<number> | 'FULL' | null;
  baseIndex: ReadonlyMap<number, SectorAggregate> | null;
  schedule: ReadonlyMap<number, ReadonlySet<number>>;
}

const EMPTY_SCHEDULE: ReadonlyMap<number, ReadonlySet<number>> = new Map();

/** The ONE per-state side-band record (BUG-1065/BUG-1066 fix). Keys weakly on the state object; values hold no reference back to any SimState. */
const sideBandOf = new WeakMap<SimState, SectorSideBand>();

// ---- transient, in-flight-dispatch accumulator ----------------------------
// Module-level `let`s are safe here (never a leak, never a cross-timeline
// hazard): JS is single-threaded and `reducer()` dispatches are never
// concurrent or re-entrant, so exactly one dispatch's worth of scratch state
// can ever be live at a time. Everything durable derived from this scratch
// state is folded into `sideBandOf` at `commitPendingDirtySectors` and
// nothing here is read again after that commit until the NEXT dispatch's
// `resetPendingDirtySectors` re-seeds it.

type PendingDirty = Set<number> | 'FULL' | null;
let pendingDirty: PendingDirty = null;
/** This dispatch's read baseline for `sectorsCompletingAtTick` — `prev`'s own committed schedule, seeded by `resetPendingDirtySectors`. */
let pendingScheduleBase: ReadonlyMap<number, ReadonlySet<number>> = EMPTY_SCHEDULE;
/** Set by `rebuildCompletionScheduleFromScratch` — a WHOLESALE replacement (AC-7: reset/hydrate void the prior city's schedule outright, never merge with it) that supersedes `pendingScheduleBase` for the rest of this dispatch. */
let pendingScheduleReplaceFull: Map<number, Set<number>> | null = null;
let pendingScheduleAdds: Map<number, Set<number>> | null = null;
let pendingScheduleRemovals: Map<number, Set<number>> | null = null;
let pendingScheduleConsumedTicks: Set<number> | null = null;

/** Called ONCE, at the top of engine.ts's `reducer()`, before `reduceCore` runs — mirrors the existing `roadTopologyMayHaveChanged = true` reset immediately above it. Takes the INCOMING state so this dispatch's schedule reads (`sectorsCompletingAtTick`) see `state`'s own committed schedule, not some other timeline's. */
export function resetPendingDirtySectors(state: SimState): void {
  if (!PARTITIONED_DERIVATIONS) return;
  pendingDirty = null;
  // BUG-1091: "no side-band entry" means the baseline is UNKNOWN (a boot-restored
  // savepoint enters the reducer with no 'hydrate' action), never "empty
  // schedule". Derive it from the state's own buildings, once, O(N).
  pendingScheduleBase = sideBandOf.get(state)?.schedule ?? completionScheduleOf(state);
  pendingScheduleReplaceFull = null;
  pendingScheduleAdds = null;
  pendingScheduleRemovals = null;
  pendingScheduleConsumedTicks = null;
}

/**
 * AC-6/AC-7 — the reducer-produced dirty set for the transition that
 * produced `next`, or `null` if this exact transition never went through
 * `commitPendingDirtySectors` (a hand-built SimState, a savepoint load, a
 * genesis replay's `isReplaying` fast path, or any reducer case this
 * increment does not instrument). `null` MUST be read by every consumer as
 * "rebuild every sector from scratch" — never as "nothing changed".
 */
export function dirtySectorsSince(next: SimState): ReadonlySet<number> | null {
  const band = sideBandOf.get(next);
  if (!band) return null;
  return band.dirty === 'FULL' ? null : band.dirty;
}

/**
 * AC-8 — a reducer case calls this with the EXACT sector keys its own
 * mutation touched (never a diff — see the file header). A no-op if this
 * dispatch has already been escalated to `'FULL'` this same dispatch (the
 * stickiness a later `rebuildCompletionScheduleFromScratch` call — see
 * §8's BUG-1067 fix below — relies on: once escalated, this function can
 * never downgrade the dispatch back to a narrow set).
 *
 * DELIBERATELY still promotes `null` to a real (possibly empty) `Set` on an
 * EMPTY `keys` argument — this is what lets `advance()`'s own
 * `markSectorsDirty(sectorsCompletingAtTick(tick))` call produce a genuine
 * `.size === 0` result on an ordinary quiet tick (AC-10's own "100 ticks
 * with no completions marks zero sectors dirty" requirement, asserted
 * directly by sectorDirty.test.mjs's AC-24.5 test) rather than falling back
 * to the (correct, but strictly more expensive) `null` full-recompute path
 * on every single quiet tick. BUG-1067's actual defect was NOT this
 * promotion itself — it was that `reset`/`hydrate`'s own WHOLESALE
 * buildings replacement had nothing else to escalate the dispatch with, so
 * `advance()`'s harmless empty-completions mark was left standing in as a
 * false "precisely nothing changed" signal for a transition that changed
 * everything. See `rebuildCompletionScheduleFromScratch`'s own fix below —
 * the escalation belongs at the SOURCE of the wholesale replacement, not by
 * neutering this function for every caller including the genuinely-quiet
 * ones.
 *
 * BUG-1069 fix: mutates ONE `Set` in place across every call THIS dispatch
 * — no reallocation per call, so a `placeMany`/`stampRegion` batch of N
 * tiles is O(N) total, not O(N^2).
 */
export function markSectorsDirty(keys: Iterable<number>): void {
  if (!PARTITIONED_DERIVATIONS) return;
  if (pendingDirty === 'FULL') return;
  if (pendingDirty === null) pendingDirty = new Set<number>();
  for (const k of keys) pendingDirty.add(k);
}

/** R3 — the naive road rule: escalate this dispatch to "every sector dirty" (implemented as AC-7's own `null` full-recompute signal — see the file header's rationale). Also covers the consolidator's citywide demolish/build pass, which is not itself a road change but is equally citywide. */
export function markAllSectorsDirty(): void {
  if (!PARTITIONED_DERIVATIONS) return;
  pendingDirty = 'FULL';
}

/**
 * Called ONCE, at the bottom of `reducer()`, against the state object it is
 * about to hand back to its caller. Deliberately takes no action if
 * `next === prev` (a case's ordinary no-op `return state`) — committing in
 * that case would OVERWRITE whatever side-band entry `prev` already carries
 * from being an EARLIER dispatch's `next`.
 *
 * `baseIndex` (BUG-1066 fix) is `prev`'s own computed INDEX — obtained by
 * calling `sectorIndexOf(prev)` right here, where `prev` is a live call
 * parameter — never a pointer to `prev` itself. Only computed when `dirty`
 * is a real Set (no point building a base index a `null`/`'FULL'` result
 * will never consult).
 *
 * `schedule` (BUG-1065 fix) is derived from `prev`'s own side-band entry —
 * never a module-global — with copy-on-write on only the TOUCHED tick keys
 * (§8): every untouched tick's `Set` is carried forward BY REFERENCE, never
 * cloned, so this stays cheap even with many outstanding constructions.
 */
export function commitPendingDirtySectors(next: SimState, prev: SimState): void {
  if (!PARTITIONED_DERIVATIONS) return;
  if (next === prev) return;
  const prevBand = sideBandOf.get(prev);
  const baseSchedule: ReadonlyMap<number, ReadonlySet<number>> = pendingScheduleReplaceFull ?? prevBand?.schedule ?? pendingScheduleBase;
  const hasRemovals = !!pendingScheduleRemovals && pendingScheduleRemovals.size > 0;
  const hasConsumed = !!pendingScheduleConsumedTicks && pendingScheduleConsumedTicks.size > 0;
  const hasAdds = !!pendingScheduleAdds && pendingScheduleAdds.size > 0;
  let schedule: ReadonlyMap<number, ReadonlySet<number>> = baseSchedule;
  if (pendingScheduleReplaceFull || hasRemovals || hasConsumed || hasAdds) {
    // Shallow copy: shares every UNTOUCHED tick's Set by reference — the
    // copy-on-write only actually clones a Set below, for a tick this
    // dispatch actually touched.
    const merged = new Map<number, ReadonlySet<number>>(baseSchedule);
    if (pendingScheduleConsumedTicks) {
      for (const t of pendingScheduleConsumedTicks) merged.delete(t);
    }
    if (pendingScheduleRemovals) {
      for (const [t, removeKeys] of pendingScheduleRemovals) {
        const existing = merged.get(t);
        if (existing) {
          const filtered = new Set(existing);
          for (const k of removeKeys) filtered.delete(k);
          if (filtered.size > 0) merged.set(t, filtered);
          else merged.delete(t);
        }
      }
    }
    if (pendingScheduleAdds) {
      for (const [t, addKeys] of pendingScheduleAdds) {
        const existing = merged.get(t);
        const unioned = existing ? new Set(existing) : new Set<number>();
        for (const k of addKeys) unioned.add(k);
        merged.set(t, unioned);
      }
    }
    schedule = merged;
  }
  // BUG-1137 (r3 finding, r4 fix): an un-banded prev (legacy / hand-built / connectedRoadTiles:[]
  // shape, or a stale/stripped road graph) has NO trustworthy premise: advance() recomputes
  // roadConnectivity from buildings on the transition, flipping isOnline for gated buildings the
  // precise dirty set never names, and no base index (prev's own, or computeRoadConnectivity(prev))
  // is sound for every stale-graph shape (A1a/A1e/A1f). So ANY transition out of an un-banded prev
  // commits FULL. One-off cost on first entry only; the banded path and flag OFF never reach here.
  let dirty: ReadonlySet<number> | 'FULL' | null = pendingDirty;
  if (!prevBand && dirty !== null) dirty = 'FULL';
  let baseIndex: ReadonlyMap<number, SectorAggregate> | null = null;
  if (dirty !== null && dirty !== 'FULL') baseIndex = sectorIndexOf(prev);
  sideBandOf.set(next, { dirty, baseIndex, schedule });
}

/** TEST SEAM — mirrors `__setPartitionedDerivationsForTest`'s NODE_TEST_CONTEXT guard. Resets the in-flight accumulator WITHOUT going through a full `reducer()` dispatch. Does NOT touch any already-committed `sideBandOf` entry (there is no module-global schedule left to clear — BUG-1065 fix). */
export function __resetDirtyTrackingForTest(): void {
  if (typeof process === 'undefined' || !process.env?.NODE_TEST_CONTEXT) {
    throw new Error('__resetDirtyTrackingForTest may only be called under NODE_TEST_CONTEXT (node --test)');
  }
  pendingDirty = null;
  pendingScheduleBase = EMPTY_SCHEDULE;
  pendingScheduleReplaceFull = null;
  pendingScheduleAdds = null;
  pendingScheduleRemovals = null;
  pendingScheduleConsumedTicks = null;
}

/** TEST SEAM — read-only peek at the current dispatch's pending accumulator (before it is committed), for tests that want to assert mid-case behaviour without a full dispatch round trip. */
export function __peekPendingDirtySectorsForTest(): PendingDirty {
  if (typeof process === 'undefined' || !process.env?.NODE_TEST_CONTEXT) {
    throw new Error('__peekPendingDirtySectorsForTest may only be called under NODE_TEST_CONTEXT (node --test)');
  }
  return pendingDirty;
}

// ---------------------------------------------------------------------------
// §8 Construction completion schedule — AC-10, now PER-STATE (BUG-1065 fix)
//
// A building placed today completes at a KNOWN future tick
// (`b.builtTick + constructionTicks(sp)`, the exact threshold
// `isOnline`/`data.ts:972` already tests). Registering that tick's sector at
// PLACEMENT time and consuming it at ADVANCE time means tick advance NEVER
// scans `s.buildings` to ask "is anyone finishing" — the forbidden O(N) shape
// AC-10 names explicitly.
//
// Unlike r1's design, the schedule is no longer a bare module-level Map
// keyed by a raw tick number (BUG-1065: two timelines racing to consume the
// SAME tick number corrupted each other permanently). It now lives inside
// each state's OWN `sideBandOf` entry (§7), threaded forward
// copy-on-write. Within a single dispatch, additions/removals/consumptions
// accumulate in the transient `pendingSchedule*` variables above and are
// folded into the NEXT state's entry by `commitPendingDirtySectors`.
//
// `rebuildCompletionScheduleFromScratch` (O(buildings), once) handles the
// ONE case that cannot rely on incremental bookkeeping: a freshly hydrated
// (loaded) state whose under-construction buildings were placed in a PRIOR
// session this process's schedule never saw. It WHOLESALE REPLACES the
// schedule for this dispatch (AC-7: reset/hydrate void the prior city's
// bookkeeping outright — see BUG-1067's escalation table below), rather
// than merging with whatever `pendingScheduleBase` happened to hold. A
// genesis replay does NOT need this rebuild: it replays every historical
// 'place'-family action through the real reducer, so the schedule is
// populated incrementally exactly as it would be during live play.
//
// ESCALATION TABLE (BUG-1067, ruling point 3 — every wholesale
// `s.buildings` replacement, explicit, not inferred). Each row's own
// `rebuildCompletionScheduleFromScratch` call is what commits `'FULL'`
// (=> `null` to every consumer) for that transition — see that function's
// own BUG-1067 doc comment for why the escalation lives THERE and not in
// `markSectorsDirty`:
//   reset      -> rebuildCompletionScheduleFromScratch(fresh city), which
//                 escalates pendingDirty to 'FULL', THEN advance(); nothing
//                 advance() does afterward (including its own
//                 markSectorsDirty(sectorsCompletingAtTick(0)) call against
//                 the fresh empty schedule) can downgrade it back — 'FULL'
//                 is sticky.
//   newGame    -> same path as reset (both route through the same case).
//   hydrate    -> rebuildCompletionScheduleFromScratch(loaded/purged state)
//                 escalates the SAME way; this case never itself calls any
//                 OTHER marking function, so the escalation is the only
//                 (and sufficient) source of `null` here.
//   genesis replay (isReplaying) -> reducer() returns before
//                 commitPendingDirtySectors ever runs (see reducer()'s own
//                 `if (isReplaying) return next` early exit) — no entry is
//                 written at all, so dirtySectorsSince() reads `null` for
//                 every replayed state, which is the correct "unknown,
//                 recompute" answer for a state this feature never saw
//                 constructed incrementally.
// ---------------------------------------------------------------------------

/** AC-10 — called once per placed building (from every reducer path that can introduce one), registering the sector that must be marked dirty the tick this building's construction crosses `constructionTicks(sp)`. */
export function scheduleSectorCompletion(completionTick: number, sectorKey: number): void {
  if (!PARTITIONED_DERIVATIONS) return;
  if (!pendingScheduleAdds) pendingScheduleAdds = new Map<number, Set<number>>();
  let set = pendingScheduleAdds.get(completionTick);
  if (!set) {
    set = new Set<number>();
    pendingScheduleAdds.set(completionTick, set);
  }
  set.add(sectorKey);
}

/**
 * BUG-1064 fix — a mid-construction building's completion is registered
 * against the sector it was PLACED in; relocating it (builtTick is
 * preserved by relocate, so the completion TICK does not change) must move
 * that registration to its NEW sector, or the old sector goes on being
 * marked dirty at completion while the sector the building actually lives
 * in now never learns it came online. A no-op when the two keys are equal
 * (same-sector relocate — nothing to move).
 */
export function rescheduleSectorCompletion(completionTick: number, oldSectorKey: number, newSectorKey: number): void {
  if (!PARTITIONED_DERIVATIONS) return;
  if (oldSectorKey === newSectorKey) return;
  if (!pendingScheduleRemovals) pendingScheduleRemovals = new Map<number, Set<number>>();
  let removeSet = pendingScheduleRemovals.get(completionTick);
  if (!removeSet) {
    removeSet = new Set<number>();
    pendingScheduleRemovals.set(completionTick, removeSet);
  }
  removeSet.add(oldSectorKey);
  scheduleSectorCompletion(completionTick, newSectorKey);
}

/**
 * AC-10 — consumed exactly once per tick, from `advance()`, BEFORE any other
 * mutation this tick so the returned keys can be folded into the same
 * dispatch's `markSectorsDirty` call. Reads THIS dispatch's baseline
 * (`pendingScheduleReplaceFull` if a wholesale rebuild ran earlier this same
 * dispatch, else `pendingScheduleBase` — `prev`'s own committed schedule),
 * unioned with anything added/removed for this exact tick so far this
 * dispatch, then marks the tick consumed (idempotent — a second call this
 * dispatch for the SAME tick returns `[]`, never double-delivers). Returns
 * `[]`, never `null` — AC-10's own "100 ticks with no completions marks
 * zero sectors dirty" test needs an actual empty result to assert
 * `.size === 0` against, not the "unknown" `null`.
 */
export function sectorsCompletingAtTick(tick: number): number[] {
  if (!PARTITIONED_DERIVATIONS) return [];
  if (pendingScheduleConsumedTicks?.has(tick)) return [];
  const base = pendingScheduleReplaceFull ?? pendingScheduleBase;
  const baseSet = base.get(tick);
  const addSet = pendingScheduleAdds?.get(tick);
  const removeSet = pendingScheduleRemovals?.get(tick);
  if (!pendingScheduleConsumedTicks) pendingScheduleConsumedTicks = new Set<number>();
  pendingScheduleConsumedTicks.add(tick);
  if (!baseSet && !addSet) return [];
  const result = new Set<number>();
  if (baseSet) for (const k of baseSet) result.add(k);
  if (removeSet) for (const k of removeSet) result.delete(k);
  if (addSet) for (const k of addSet) result.add(k);
  return Array.from(result);
}

/**
 * AC-10/AC-7/BUG-1067 — a full, one-time rebuild of the schedule from
 * `s.buildings`, for hydrate/reset/newGame: a state whose under-construction
 * buildings were never placed through THIS session's `scheduleSectorCompletion`
 * calls. Sets `pendingScheduleReplaceFull` — a WHOLESALE replacement for the
 * rest of this dispatch (never merged with `pendingScheduleBase`, and any
 * adds/removals/consumed-ticks accumulated so far this dispatch against the
 * OLD baseline are discarded — they describe a schedule this rebuild has
 * just superseded outright).
 *
 * BUG-1067 FIX (the actual repro's root cause): this function is ONLY ever
 * called for a WHOLESALE `s.buildings` replacement (reset/newGame/hydrate —
 * the three rows of §8's escalation table above), so it ALSO escalates
 * `pendingDirty` straight to `'FULL'` here, at the single source of the
 * replacement — never by leaving it to whatever `advance()` happens to mark
 * afterward. Before this fix, `reset` (which calls this, then `advance(s)`)
 * had NOTHING escalating its own transition: `advance()`'s
 * `markSectorsDirty(sectorsCompletingAtTick(tick))` call against a fresh
 * city's empty schedule produced a real-but-EMPTY Set, which `sectorIndexOf`
 * correctly read as "precisely zero sectors changed" and so reused the
 * PREVIOUS (pre-reset) city's base index untouched — reporting the old
 * city's aggregates for the new one. Escalating HERE means every wholesale
 * replacement is unconditionally safe regardless of what advance() (or
 * anything else) does afterward — 'FULL' is sticky (`markSectorsDirty`
 * no-ops once escalated), so this can only ever WIDEN the recompute, never
 * accidentally narrow it back down.
 */
export function rebuildCompletionScheduleFromScratch(s: SimState): void {
  if (!PARTITIONED_DERIVATIONS) return;
  const schedule = completionScheduleOf(s);
  pendingScheduleReplaceFull = schedule;
  pendingDirty = 'FULL';
  pendingScheduleAdds = null;
  pendingScheduleRemovals = null;
  pendingScheduleConsumedTicks = null;
}

/** Derives the completion schedule (tick -> sector keys) from `s.buildings` alone. Pure; touches no side-band. Shared by the wholesale rebuild and by BUG-1091's unknown-baseline recovery. */
function completionScheduleOf(s: SimState): Map<number, Set<number>> {
  const schedule = new Map<number, Set<number>>();
  for (const b of s.buildings) {
    if (b.builtTick == null) continue; // matches isOnline's own null-builtTick == "always online" convention (data.ts:966)
    const sp = SPECS[b.spec];
    if (!sp) continue;
    const ticksToComplete = constructionTicks(sp);
    if (s.tick - b.builtTick < ticksToComplete) {
      const tick = b.builtTick + ticksToComplete;
      let set = schedule.get(tick);
      if (!set) {
        set = new Set<number>();
        schedule.set(tick, set);
      }
      set.add(sectorKeyOf(b.x, b.y));
    }
  }
  return schedule;
}

// ---------------------------------------------------------------------------
// §9 Incremental sector-index rebuild — inc2 (R2 lead ruling, reworked with §7)
//
// `sectorIndexOf(next)`, flag ON: if `next`'s side-band gives a non-null
// dirty set AND a base index (§7's `baseIndex`, already `prev`'s computed
// index — never `prev` itself), copy every CLEAN sector's aggregate from
// the base UNCHANGED (by reference — no reallocation) and recompute ONLY
// the dirty ones in a single combined pass. Flag OFF: byte-identical to
// inc1 — `coldSectorIndexOf` is untouched, no side-band read at all.
//
// `indexResultCache` memoises the RESULT of this function per state, keyed
// weakly on the state itself and holding only small `SectorAggregate` POJOs
// as values — no SimState reference anywhere in it, so it cannot become a
// second BUG-1066-shaped leak.
//
// HONEST LIMITATION (unchanged from r1): `SectorAggregate` carries no
// per-sector BUILDING-ID list (AC-5, inc1-scoped), so locating which of
// `s.buildings` belong to a DIRTY sector still means one pass over the
// whole array. What this DOES save: for every building in a CLEAN sector,
// `isOnline`/`buildingJobsOf` are never called — those two calls are AC-23's
// own measured dominant cost, not the O(1) bucketing check. A true O(D)
// rebuild needs a maintained reverse index, tracked as an inc3 follow-up.
// ---------------------------------------------------------------------------

/**
 * R2 — copies every CLEAN sector's aggregate from `baseIndex` by
 * reference (no reallocation, no re-read of that sector's buildings) and
 * recomputes every DIRTY sector from `s.buildings` with the EXACT SAME
 * arithmetic `buildSectorIndex` uses (GR#3 — one accumulation rule, not
 * two), in a single combined pass. A sector that lost its last building
 * this transition is correctly ABSENT from the result (never `.set()`
 * here) — R2's "delete sectors that became empty".
 */
function rebuildDirtySectors(
  s: SimState,
  baseIndex: ReadonlyMap<number, SectorAggregate>,
  dirty: ReadonlySet<number>,
): ReadonlyMap<number, SectorAggregate> {
  const next = new Map<number, SectorAggregate>();
  for (const [key, agg] of baseIndex) {
    if (!dirty.has(key)) next.set(key, agg);
  }
  for (const b of s.buildings) {
    const key = sectorKeyOf(b.x, b.y);
    if (!dirty.has(key)) continue;
    let agg = next.get(key);
    if (!agg) {
      const { x0, y0 } = sectorOriginOf(key);
      agg = { key, x0, y0, buildingCount: 0, jobs: 0 };
      next.set(key, agg);
    }
    agg.buildingCount += 1;
    if (isOnline(s, b)) {
      const sp = SPECS[b.spec];
      if (sp) agg.jobs += buildingJobsOf(sp, b);
    }
  }
  return next;
}

const indexResultCache = new WeakMap<SimState, ReadonlyMap<number, SectorAggregate>>();

/**
 * AC-4/R2 — the public entry point every consumer (totalJobsPartitioned,
 * the differential harness, tests) calls. Flag OFF: delegates straight to
 * `coldSectorIndexOf`, inc1's untouched binding. Flag ON: incremental when
 * possible, a full cold build otherwise (AC-7's fail-safe — a `null` dirty
 * set, or no cached base index, both fall through to `buildSectorIndex`
 * directly here rather than through `coldSectorIndexOf`'s own separate
 * WeakMap, so the two caches never fight over the same state's entry).
 */
export function sectorIndexOf(s: SimState): ReadonlyMap<number, SectorAggregate> {
  if (!PARTITIONED_DERIVATIONS) return coldSectorIndexOf(s);
  const cached = indexResultCache.get(s);
  if (cached) return cached;
  const band = sideBandOf.get(s);
  const dirty = band ? (band.dirty === 'FULL' ? null : band.dirty) : null;
  const result = dirty !== null && band?.baseIndex ? rebuildDirtySectors(s, band.baseIndex, dirty) : buildSectorIndex(s);
  indexResultCache.set(s, result);
  return result;
}
