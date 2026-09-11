// FEAT-2326609804 "PER-TILE MODE SPLIT" — docs/planning/acceptance/FEAT-2326609804.md
// (AC-1..AC-9). New, additive module (not folded into trafficDemand.ts — that
// file is already 1700+ lines and this increment's surface (land-use density
// banding + walk-radius access discovery + a composite-key table lookup) is a
// distinct concern from inc2's per-tile trip generation; every symbol this
// module needs from trafficDemand.ts/data.ts is imported, never re-derived,
// per GR#3).
//
// WHY (BUG-938, inc9 r3 ruling): inc9's `modeShareBalance` reward was routed
// through `shannonModeBalanceOf(modeShareOf(ladderPointOf(s)))` — a pure
// function of POPULATION, never of what the player builds (every city at the
// same population scored identically, a hidden population bonus dressed up
// as a transport-integration reward). This module makes each demand tile's
// modal split depend on its OWN land-use density tier and its OWN local
// access to rail/bus/tram infrastructure (a direct player decision), then
// exposes a trip-weighted city-wide rollup (`realisedCityWideModeShareOf`)
// that genuinely reflects the aggregate infrastructure built — the
// structural prerequisite for a follow-up increment to re-wire inc9's
// coupling (AC-8: no consumer yet this increment).
//
// PURE + DETERMINISTIC (GR#21): every export is memoOnState over SimState
// only; no wall-clock read, no PRNG, no browser storage read; the
// walk-radius discovery reuses trafficDemand.ts's own nearestSourceForTiles
// (GR#3 — no second BFS flood), bounded by mode_split_local.json's own
// walkAccessRadiusMetres (GR#15, never a hand-typed tile count).
//
// Money (AC-9): this file never touches any fiscal SimState field or
// external-commuter worker basis — a pure spatial redistribution of MODE
// COMPOSITION, never an income change (see the acceptance doc's AC-9 for
// the exact excluded field list, checked mechanically by this module's own
// test suite).

import type { SimState } from './types.ts';
import { SPECS, memoOnState, lineSegmentIndexOf, stationLinks, SEGMENT_RAIL_CLASSES, capacityAtTier } from './data.ts';
import { demandForecastOf, modeShareOf, ladderPointOf, nearestSourceForTiles } from './trafficDemand.ts';
import { recordError } from './backend.ts';
import rawModeSplitLocal from './traffic-data/mode_split_local.json' with { type: 'json' };
import rawTrafficConfig from './traffic-data/traffic.json' with { type: 'json' };

// --- Registry error codes (GR#7) --------------------------------------------
// Minted via `node tools/plan/add-error.js add MET-V95x --mkey ui.webconsole
// ...` (data/errors.json), block V955-V959 reserved for FEAT-2326609804.
export const ERR_LOCAL_ACCESS_BAND_LOG = 'MET-V955'; // PerTileModeSplitLocalAccessBandMissing (one-shot log: composite key not in table, city-row fallback used)
export const ERR_TABLE_MALFORMED = 'MET-V956'; // PerTileModeSplitTableMalformed
export const ERR_LAND_USE_MAPPING_MISSING = 'MET-V957'; // PerTileModeSplitLandUseMappingMissing
export const ERR_WALK_RADIUS_MISSING = 'MET-V958'; // PerTileModeSplitWalkRadiusMissing
export const ERR_MODE_VECTOR_INVALID = 'MET-V959'; // PerTileModeSplitModeVectorInvalid

function registryError(code: string, message: string): Error {
  return new Error(`${code}: ${message}`);
}

// --- mode_split_local.json typed view + fail-closed load-time validation ---
// (AC-1/AC-7: every magnitude/band/id is sourced from this file, never a
// hand-typed literal in TS; a malformed table throws at module load, GR#7.)

interface DensityBandThreshold {
  band: string;
  minMagnitude: number;
}

interface ModeSplitLocalTable {
  modeIds: string[];
  walkAccessRadiusMetres: number;
  landUseDensityMapping: Record<string, string>;
  densityBandMagnitudeThresholds: DensityBandThreshold[];
  table: Record<string, Record<string, number>>;
}

const localSplit = rawModeSplitLocal as unknown as ModeSplitLocalTable;

if (!Array.isArray(localSplit.modeIds) || localSplit.modeIds.length === 0) {
  throw registryError(ERR_TABLE_MALFORMED, 'mode_split_local.json modeIds must be a non-empty array');
}
const MODE_IDS: readonly string[] = localSplit.modeIds;

if (
  typeof localSplit.walkAccessRadiusMetres !== 'number' ||
  !Number.isFinite(localSplit.walkAccessRadiusMetres) ||
  localSplit.walkAccessRadiusMetres <= 0
) {
  throw registryError(
    ERR_WALK_RADIUS_MISSING,
    `mode_split_local.json walkAccessRadiusMetres must be a positive finite number, got ${JSON.stringify(localSplit.walkAccessRadiusMetres)}`,
  );
}
const WALK_ACCESS_RADIUS_METRES = localSplit.walkAccessRadiusMetres;

if (!localSplit.landUseDensityMapping || typeof localSplit.landUseDensityMapping !== 'object') {
  throw registryError(ERR_TABLE_MALFORMED, 'mode_split_local.json landUseDensityMapping is missing or not an object');
}
// AC-1: every SPECS id carrying residents or jobs must have a mapping entry —
// checked once, at MODULE LOAD, over the whole static catalogue (SPECS never
// changes at runtime), so a new job/residential spec added without a mapping
// entry fails loudly the instant the module loads rather than degrading
// silently the first time a building of that spec goes online.
for (const [specId, sp] of Object.entries(SPECS)) {
  if (sp.residents == null && sp.jobs == null) continue;
  if (!(specId in localSplit.landUseDensityMapping)) {
    throw registryError(
      ERR_LAND_USE_MAPPING_MISSING,
      `mode_split_local.json landUseDensityMapping is missing spec id "${specId}" (kind "${sp.kind}"), which carries residents/jobs`,
    );
  }
}

if (!localSplit.table || typeof localSplit.table !== 'object') {
  throw registryError(ERR_TABLE_MALFORMED, 'mode_split_local.json table is missing or not an object');
}
{
  const modeIdSet = new Set(MODE_IDS);
  for (const [key, vector] of Object.entries(localSplit.table)) {
    const keys = Object.keys(vector);
    if (keys.length !== modeIdSet.size || !keys.every((k) => modeIdSet.has(k))) {
      throw registryError(ERR_MODE_VECTOR_INVALID, `mode_split_local.json table['${key}'] keys do not exactly match modeIds`);
    }
    let sum = 0;
    for (const v of Object.values(vector)) sum += v;
    // BUG-987 (round 2): tightened 1e-6 -> 1e-9, matching
    // validate-traffic-tables.mjs's own tightened check and
    // trafficModeSplit.test.mjs's AC-4 assertion tolerance. The r1 finding
    // was that a row summing to 1.000001 (the OLD bound's own limit) would
    // still load successfully while making AC-4's renormalisation step a
    // REAL, non-equivalent correction rather than a floating-point no-op —
    // this data file's rows already sum to 1 within 1e-12 (independently
    // verified this round), so 1e-9 changes nothing today and closes the
    // hole for a future hand-edited row.
    if (!Number.isFinite(sum) || Math.abs(sum - 1) > 1e-9) {
      throw registryError(ERR_MODE_VECTOR_INVALID, `mode_split_local.json table['${key}'] mode-share vector sums to ${sum}, expected 1.0`);
    }
  }
}

// BUG-989 (round 2 fix): densityBandMagnitudeThresholds — the TIER-AWARE
// step function densityBandOf() evaluates at runtime, replacing a bare
// specId->band table read (which is invariant to a building's own
// capacityTier and therefore build-insensitive, exactly what BUG-989 found).
// Sourced from mode_split_local.json (GR#15 — never a hand-typed magnitude
// in this file), MECHANICALLY derived from landUseDensityMapping's own
// existing per-spec assignment (see the JSON's own
// densityBandMagnitudeThresholdsNote for the exact derivation and the
// 0-mismatch verification against every mapped spec at tier 0) — not a
// second, possibly-divergent density model (GR#3).
if (!Array.isArray(localSplit.densityBandMagnitudeThresholds) || localSplit.densityBandMagnitudeThresholds.length === 0) {
  throw registryError(ERR_TABLE_MALFORMED, 'mode_split_local.json densityBandMagnitudeThresholds must be a non-empty array');
}
{
  const knownDensityBandsFromMapping = new Set(Object.values(localSplit.landUseDensityMapping));
  let prevMag = -Infinity;
  const seenBands = new Set<string>();
  for (const t of localSplit.densityBandMagnitudeThresholds) {
    if (!t || typeof t.band !== 'string' || t.band.length === 0) {
      throw registryError(ERR_TABLE_MALFORMED, `mode_split_local.json densityBandMagnitudeThresholds has an entry with an invalid band: ${JSON.stringify(t)}`);
    }
    if (seenBands.has(t.band)) {
      throw registryError(ERR_TABLE_MALFORMED, `mode_split_local.json densityBandMagnitudeThresholds repeats band "${t.band}"`);
    }
    seenBands.add(t.band);
    if (typeof t.minMagnitude !== 'number' || !Number.isFinite(t.minMagnitude) || t.minMagnitude < 0) {
      throw registryError(ERR_TABLE_MALFORMED, `mode_split_local.json densityBandMagnitudeThresholds["${t.band}"] minMagnitude must be a non-negative finite number, got ${JSON.stringify(t.minMagnitude)}`);
    }
    if (t.minMagnitude <= prevMag) {
      throw registryError(ERR_TABLE_MALFORMED, `mode_split_local.json densityBandMagnitudeThresholds is not strictly ascending at band "${t.band}" (minMagnitude ${t.minMagnitude} <= previous ${prevMag})`);
    }
    prevMag = t.minMagnitude;
  }
  // Every band actually USED by landUseDensityMapping must be reachable via
  // the thresholds — otherwise a spec's tier-0 band (from the static
  // mapping) could disagree with its own runtime (threshold-based) band,
  // silently reintroducing a second density model.
  for (const band of knownDensityBandsFromMapping) {
    if (!seenBands.has(band)) {
      throw registryError(ERR_TABLE_MALFORMED, `mode_split_local.json densityBandMagnitudeThresholds is missing band "${band}", used by landUseDensityMapping`);
    }
  }
}
const DENSITY_BAND_THRESHOLDS: readonly DensityBandThreshold[] = localSplit.densityBandMagnitudeThresholds;

// webconsoleMetresPerTile (data/traffic.json, GR#15 — no hand-typed 50)
// mirrors trafficAssignment.ts's own minimal reader of the same field.
interface TrafficConfigView {
  webconsoleMetresPerTile: number;
}
const trafficConfigView = rawTrafficConfig as unknown as TrafficConfigView;
if (
  typeof trafficConfigView.webconsoleMetresPerTile !== 'number' ||
  !Number.isFinite(trafficConfigView.webconsoleMetresPerTile) ||
  trafficConfigView.webconsoleMetresPerTile <= 0
) {
  throw registryError(ERR_TABLE_MALFORMED, 'data/traffic.json is missing a numeric webconsoleMetresPerTile field');
}
const WALK_ACCESS_RADIUS_TILES = Math.max(0, Math.ceil(WALK_ACCESS_RADIUS_METRES / trafficConfigView.webconsoleMetresPerTile));

// --- AC-1: land-use density band -------------------------------------------

/** Data-sourced tile key (GR#21 canonical form, matches trafficDemand.ts's own "x,y" convention). */
function tileKeyOf(x: number, y: number): string {
  return `${x},${y}`;
}

/**
 * densityBandForMagnitude (BUG-989 fix) — the step function that turns a
 * (residents+jobs) MAGNITUDE into a density band, using
 * mode_split_local.json's `densityBandMagnitudeThresholds` (ascending by
 * `minMagnitude`, validated at module load above). Returns the band of the
 * HIGHEST threshold whose `minMagnitude <= magnitude`; a magnitude below
 * every threshold falls back to the lowest (first) band — defensive only,
 * since every threshold list's first entry has `minMagnitude` at or below
 * the smallest real magnitude in the catalogue (verified: 0 mismatches
 * against the SPECS catalogue this round).
 */
function densityBandForMagnitude(magnitude: number): string {
  let band = DENSITY_BAND_THRESHOLDS[0].band;
  for (const t of DENSITY_BAND_THRESHOLDS) {
    if (t.minMagnitude <= magnitude) band = t.band;
    else break; // ascending order (validated at load) — no higher threshold can still qualify.
  }
  return band;
}

/**
 * AC-1 — a demand tile's density band. BUG-989 fix (round 2): keyed by the
 * building's ACTUAL scaled capacity at its CURRENT `capacityTier`
 * (`capacityAtTier(SPECS[spec], capacityTier)`, data.ts — the same scaling
 * the wage bill / demand forecast already apply), bucketed via
 * `densityBandForMagnitude` — NOT a fixed per-spec-id table lookup that
 * ignores upgrades entirely (the r1 defect: a res_hut at capacityTier 0 and
 * capacityTier 9 returned byte-identical bands because the lookup never
 * consulted the tier at all). `capacityTier` defaults to 0 (a freshly placed
 * building, matching demandForecastOf's own `b.capacityTier ?? 0` idiom) so
 * every existing caller that never had a tier to pass keeps its EXACT prior
 * answer — landUseDensityMapping's per-spec value is the tier-0 point on
 * this same step function BY CONSTRUCTION (0 mismatches verified this round,
 * see the JSON's own densityBandMagnitudeThresholdsNote), so this is not a
 * behaviour change at tier 0, only at tier > 0.
 */
export function landUseDensityBandFor(spec: string, capacityTier = 0): string {
  return densityBandOf(spec, capacityTier);
}

function densityBandOf(spec: string, capacityTier = 0): string {
  // The completeness check at module load already guarantees every
  // residents/jobs-bearing spec has a landUseDensityMapping entry — reused
  // here as the SAME fail-closed guard (a spec absent from that mapping is
  // not a demand-generating spec, or the catalogue drifted since load,
  // either way not safe to bucket by magnitude silently).
  if (!(spec in localSplit.landUseDensityMapping)) {
    throw registryError(ERR_LAND_USE_MAPPING_MISSING, `mode_split_local.json landUseDensityMapping has no entry for spec "${spec}"`);
  }
  const sp = SPECS[spec];
  const magnitude = demandMagnitudeAtTier(sp, capacityTier);
  return densityBandForMagnitude(magnitude);
}

/**
 * demandMagnitudeAtTier (BUG-989 rework) — the SAME (residents+jobs)
 * magnitude basis `mode_split_local.json`'s `landUseDensityMapping` /
 * `densityBandMagnitudeThresholds` were generated from, evaluated at an
 * ARBITRARY tier. Mirrors data.ts's own `jobsAtTier` BUG-652 rule (not
 * reusable directly — it is module-private): a spec whose `jobs` field
 * shares the catalogue entry with ANOTHER capacity field (residents/
 * children/served) keeps `jobs` FLAT regardless of tier, because that
 * spec's `capacityTiers` ladder is sized for the OTHER field, not jobs
 * (`hea_teaching`'s ladder is sized for its 200,000 `served` figure, not its
 * 1,450 `jobs` figure — blindly calling `capacityAtTier` for it would read
 * the served-ladder's value as a JOB count, off by two orders of magnitude,
 * exactly the mismatch this round's own reconstruction check caught before
 * this fix landed). A residents-only or jobs-only spec (no second capacity
 * field) scales normally via `capacityAtTier`, matching
 * `demandForecastOf`'s own basis for THAT case.
 */
function demandMagnitudeAtTier(sp: (typeof SPECS)[string] | undefined, tier: number): number {
  if (!sp) return 0;
  if (sp.jobs != null) {
    const jobsSharesSpecWithOtherCapacity = sp.residents != null || sp.children != null || sp.served != null;
    return jobsSharesSpecWithOtherCapacity ? sp.jobs : capacityAtTier(sp, tier);
  }
  if (sp.residents != null) return capacityAtTier(sp, tier);
  return 0;
}

// --- AC-2: per-tile local-access band ---------------------------------------

const LOCAL_ACCESS_NONE = 'local_access_none';
const LOCAL_ACCESS_LOW = 'local_access_low';
const LOCAL_ACCESS_MEDIUM = 'local_access_medium';
const LOCAL_ACCESS_HIGH = 'local_access_high';

/**
 * tileLocalAccessBandOf (AC-2) — for every demand-generating tile
 * (demandForecastOf(s)), the local-access band discovered by a bounded walk
 * (WALK_ACCESS_RADIUS_TILES, sourced from mode_split_local.json) via
 * trafficDemand.ts's own `nearestSourceForTiles` primitive — reused rather
 * than a second BFS flood (GR#3, BUG-935 lesson: this IS the cheap
 * per-query pattern that primitive was built for).
 *
 * ASM-1533/BUG-990 (honest correction, round 2): `SEGMENT_LINE_CLASSES`
 * (data.ts) has NO bus/tram/metro segment class AT ALL today — only
 * 'rail'/'hs1' heavy-rail specs and three road tiers ('rd_aroad'/'rd_dual'/
 * 'm20') are decomposed into segments. That is a BROADER gap than the doc's
 * "bus_lane_variant/tram_track_variant only" framing. `hasBusAccess` is
 * therefore structurally always false with the live catalogue, and this is
 * NOT "no future change needed" (the r1 comment's false claim, corrected
 * below): the day a bus/tram segment CLASS is added to data.ts, THIS FILE
 * must be edited to populate a `busSourceKeys` loop from `segIndex`
 * (mirroring the `railSourceKeys` loop above) — there is no data-driven way
 * to make that automatic, because the class does not exist yet to iterate
 * over. AC-2's `rail XOR bus` rule therefore collapses to exactly
 * `hasRailAccess` for `local_access_medium`, and `local_access_high` (rail
 * AND bus) is unreachable until that future edit lands.
 */
export const tileLocalAccessBandOf: (s: SimState) => Map<string, string> = memoOnState((s) => {
  const demandTiles = demandForecastOf(s);
  const queryKeys = demandTiles.map((t) => tileKeyOf(t.x, t.y));
  // GR#21: sorted, deterministic — no map-range-with-break.
  const sortedQueryKeys = [...queryKeys].sort();

  const segIndex = lineSegmentIndexOf(s);
  const railSourceKeys: string[] = [];
  for (const [tileKey, segmentId] of segIndex.tileToSegment) {
    const seg = segIndex.segmentById.get(segmentId);
    if (seg && SEGMENT_RAIL_CLASSES.has(seg.spec)) railSourceKeys.push(tileKey);
  }
  railSourceKeys.sort();

  // stationLinks(s)'s binary road-connected adjacency (AC-2/ASM-1532) — the
  // fallback signal for local_access_low. Station tile = the building's own
  // (x,y), matching stationLinks' own definition of a "linked" station.
  const stationSourceKeys: string[] = [];
  const connected = stationLinks(s).connectedIds;
  if (connected.size > 0) {
    for (const b of s.buildings) {
      if (connected.has(b.id)) stationSourceKeys.push(tileKeyOf(b.x, b.y));
    }
  }
  stationSourceKeys.sort();

  const railNearest = nearestSourceForTiles(sortedQueryKeys, railSourceKeys, WALK_ACCESS_RADIUS_TILES);
  // BUG-990 fix: no bus/tram segment class exists in the live catalogue
  // (see the doc comment above), so a `busSourceKeys` array is ALWAYS
  // empty — calling `nearestSourceForTiles` over a guaranteed-empty source
  // set wastes a walk-radius pass every single invocation for no possible
  // result (`nearestSourceForTiles` itself returns an empty Map immediately
  // when `sourceTileKeys.length === 0`, so this Map literal is equivalent
  // AND cheaper). `hasBus` stays `false` until that future data.ts edit
  // lands, at which point THIS line (not just a data table) needs to change
  // to a real `nearestSourceForTiles(sortedQueryKeys, busSourceKeys, ...)`
  // call fed by a real `busSourceKeys` loop over `segIndex`.
  const busNearest = new Map<string, string>();
  const stationNearest = nearestSourceForTiles(sortedQueryKeys, stationSourceKeys, WALK_ACCESS_RADIUS_TILES);

  const out = new Map<string, string>();
  for (const key of sortedQueryKeys) {
    if (out.has(key)) continue; // GR#21: a tile can repeat in queryKeys if two specs share (x,y) — never re-derive.
    const hasRail = railNearest.has(key);
    const hasBus = busNearest.has(key);
    let band: string;
    if (hasRail && hasBus) band = LOCAL_ACCESS_HIGH;
    else if (hasRail || hasBus) band = LOCAL_ACCESS_MEDIUM;
    else if (stationNearest.has(key)) band = LOCAL_ACCESS_LOW;
    else band = LOCAL_ACCESS_NONE;
    out.set(key, band);
  }
  return out;
});

// --- AC-3: per-tile mode-share vector lookup + fallback ---------------------

// D2 (BUG-851 model): log-once per distinct missing composite key, per page
// load — the first encounter silently demotes to the city-row fallback
// (cheap, no user impact), the second+ logs a registry error so the gap is
// visible in the session's logs without spamming per-tile-per-tick.
const loggedMissingCompositeKeys = new Set<string>();
function logMissingCompositeKeyOnce(key: string): void {
  if (loggedMissingCompositeKeys.has(key)) {
    recordError(
      `mode_split_local.json table is missing composite key "${key}" — falling back to the city-level split`,
      { type: 'app', code: ERR_LOCAL_ACCESS_BAND_LOG, action: 'trafficModeSplit.perTileLocalModeShareOf' },
    );
  } else {
    loggedMissingCompositeKeys.add(key);
  }
}

/**
 * buildingCapacityTierByTileOf (BUG-989 support) — `x,y` -> that building's
 * `capacityTier` (defaulting to 0, matching demandForecastOf's own
 * `b.capacityTier ?? 0` idiom, data.ts's `coerceCapacityTier` storage-
 * boundary safeX handles the malformed-input case upstream of this file —
 * GR#16, no second coercion here). One pass over `s.buildings`, memoised, so
 * `densityBandOf` never needs an O(buildings) scan per tile.
 */
const buildingCapacityTierByTileOf: (s: SimState) => Map<string, number> = memoOnState((s) => {
  const out = new Map<string, number>();
  for (const b of s.buildings) {
    out.set(tileKeyOf(b.x, b.y), b.capacityTier ?? 0);
  }
  return out;
});

/**
 * perTileLocalModeShareOf (AC-3) — each demand tile's LOCAL mode-share
 * vector, keyed by the composite `${densityBand}_${accessBand}` string
 * against mode_split_local.json's table (AC-1's `densityBandOf` +
 * `tileLocalAccessBandOf`'s per-tile access band, above). A composite key
 * absent from the table falls back to the CITY-WIDE row
 * (`modeShareOf(ladderPointOf(s))`, the SAME accessor inc2 already uses —
 * never a second density model, GR#3) — never invented, never read from the
 * old inc0 mode_share_by_density.json per-tile (that table stays a source
 * NOTE for this one's provenance, never a second runtime lookup path).
 *
 * BUG-988 fix (round 2): every returned vector is a FROZEN CLONE, never the
 * live `mode_split_local.json` module object (`localSplit.table[...]`) and
 * never a shared reference to `cityRow` handed to more than one tile. r1
 * proved in-process that mutating one state's returned vector corrupted a
 * DIFFERENT state's read of the same table row (every memoOnState cache
 * shares the same underlying JSON object) — cloning at the point of return
 * is the fix, `Object.freeze` makes a future mutation attempt throw in
 * strict mode rather than silently corrupt, per the lead's own suggested
 * remedy.
 */
export const perTileLocalModeShareOf: (s: SimState) => Map<string, Record<string, number>> = memoOnState((s) => {
  const demandTiles = demandForecastOf(s);
  const accessBands = tileLocalAccessBandOf(s);
  const tierByTile = buildingCapacityTierByTileOf(s);
  const cityRow = modeShareOf(ladderPointOf(s));

  const out = new Map<string, Record<string, number>>();
  for (const t of demandTiles) {
    const key = tileKeyOf(t.x, t.y);
    if (out.has(key)) continue; // GR#21: never re-derive a tile already resolved this call.
    const tier = tierByTile.get(key) ?? 0;
    const densityBand = densityBandOf(t.spec, tier);
    const accessBand = accessBands.get(key) ?? LOCAL_ACCESS_NONE;
    const compositeKey = `${densityBand}_${accessBand}`;
    const row = localSplit.table[compositeKey];
    if (!row) logMissingCompositeKeyOnce(compositeKey);
    // BUG-988: clone (never alias) EVERY entry, whether sourced from the
    // table or the city-row fallback — a fresh object per TILE, not merely
    // per call, because `cityRow` itself is shared across every fallback
    // tile within this one call.
    out.set(key, Object.freeze({ ...(row ?? cityRow) }));
  }
  return out;
});

/**
 * densityAndAccessBandOf (test/debug helper, AC-3's false-pass guard) —
 * exposes WHICH composite key a tile resolved to (and whether the row came
 * from the table vs the city-row fallback), so a test can assert the
 * SOURCE, not merely that the numbers happen to match (AC-3's false-pass
 * note: a coincidental value match must not pass).
 */
export function tileModeShareSourceOf(s: SimState, x: number, y: number, spec: string): { compositeKey: string; fromTable: boolean } {
  const accessBands = tileLocalAccessBandOf(s);
  const tier = buildingCapacityTierByTileOf(s).get(tileKeyOf(x, y)) ?? 0;
  const densityBand = densityBandOf(spec, tier);
  const accessBand = accessBands.get(tileKeyOf(x, y)) ?? LOCAL_ACCESS_NONE;
  const compositeKey = `${densityBand}_${accessBand}`;
  return { compositeKey, fromTable: compositeKey in localSplit.table };
}

// --- AC-4: realised city-wide mode share (trip-weighted) --------------------

/**
 * realisedCityWideModeShareOf (AC-4, AC-8) — trip-weighted sum of every
 * per-tile vector (perTileLocalModeShareOf), renormalised to sum to exactly
 * 1.0 (floating-point drift correction only). This is the intended SOURCE
 * for a re-enabled inc9 `modeShareBalance`
 * (`shannonModeBalanceOf(realisedCityWideModeShareOf(s))`) — see
 * trafficRewards.ts's doc note near `integratedTransportScoreOf` — but has
 * NO consumer yet this increment (AC-8: the re-enablement is a follow-up).
 */
export const realisedCityWideModeShareOf: (s: SimState) => Record<string, number> = memoOnState((s) => {
  const demandTiles = demandForecastOf(s);
  const perTile = perTileLocalModeShareOf(s);

  const totals: Record<string, number> = {};
  for (const id of MODE_IDS) totals[id] = 0;
  let totalWeight = 0;

  for (const t of demandTiles) {
    const key = tileKeyOf(t.x, t.y);
    const vector = perTile.get(key);
    if (!vector) continue; // unreachable in practice (perTile is built from the same demandTiles), defensive only.
    const weight = t.personTrips;
    if (weight <= 0) continue;
    totalWeight += weight;
    for (const id of MODE_IDS) totals[id] += (vector[id] ?? 0) * weight;
  }

  if (totalWeight <= 0) {
    // No demand at all — fall back to the city-level split rather than a
    // divide-by-zero NaN vector (honest degenerate case, mirrors AC-3's own
    // city-row fallback).
    return modeShareOf(ladderPointOf(s));
  }

  let sum = 0;
  for (const id of MODE_IDS) sum += totals[id];
  const out: Record<string, number> = {};
  for (const id of MODE_IDS) out[id] = sum > 0 ? totals[id] / sum : 0;
  return out;
});
