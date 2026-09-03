// fixture.mjs — FEAT-2326609759 (BUG-617 RCA): deterministic ~13k-building /
// 1M+-population SimState fixture for the webconsole SCALE GATE.
//
// WHY THIS EXISTS: Aaron's live dogfood city (1.4M population, ~13k
// buildings) wedged the main thread for minutes on load. No test in this
// repo had EVER run the webconsole reducer/derivations at anything close to
// that scale — every existing perf-flavoured test (wellbeing-scale.test.mjs,
// density-scale-inc1.test.mjs, bug-460-alloc.test.mjs) tops out at a few
// hundred buildings / a few hundred thousand population. The Go engine has
// had a real 1M-citizen CI gate since BUG-034 (internal/harness/synth +
// cmd/perfci, wired as the perf-1m-probe CI job) — the webconsole never had
// an equivalent. This fixture is the webconsole half of that bar.
//
// FAST-PATH CONSTRUCTION (documented, see internal/harness/synth/generator.go
// for the Go-side precedent of the same idea): rather than replaying ~13,000
// individual 'place' reducer actions — which would themselves take the
// wedge-scale time this gate exists to bound, defeating a <60s fixture-build
// budget — this builds the `buildings[]` array and `population` field
// DIRECTLY from SPECS + capacityTiers (the same technique
// wellbeing-scale.test.mjs's `city()` helper uses at a much smaller scale),
// then runs a small, fixed number of REAL reducer ticks (SETTLE_TICKS) to
// derive everything the direct construction does not set by hand: lastFlows,
// history, demographicHistory, roadConnectivity, insolvency state, etc.
//
// ONLINE-GATING SHORTCUT: buildings are constructed WITHOUT a `builtTick`
// field. `isOnline()` (data.ts) treats an absent `builtTick` as "pre-dates
// the road-activation-gate feature — treat as always online" (its own
// documented BACKWARD TOLERANCE for "a bespoke/legacy state that never went
// through advance()/reducer" — see isOnline's doc comment). That is exactly
// what this fixture is: a bespoke state, not one built by replaying 'place'
// actions through a real connected road network. This means residential
// capacity counts immediately without needing a hand-built road mesh capable
// of flood-filling 13,000 buildings' worth of adjacency — a second, orthogonal
// scale problem this gate is NOT trying to solve. A modest number of real
// 'road'-kind buildings are still included (ROAD_FRACTION below) purely so
// computeRoadConnectivity() has a non-trivial tile set to flood-fill each
// tick, matching the real cost shape even though no building's online-ness
// depends on the result.
//
// DETERMINISM (GR#21): no Date.now()/Math.random() anywhere in this file.
// Every building's spec/position/tier is a pure function of its index and
// the (already-deterministic, data-file-sourced) SPECS catalogue. Two calls
// to buildScaleFixture() with the same params produce byte-identical output.
//
// COMPOSITION PROVENANCE (BUG-644, 2026-09-03): the fractions above were a
// hand-guessed "generic city" mix (heavy office/industrial/commercial, a
// flat 3% water) with NO separate motorway/rail/station/transport/mine/
// civic/pylon/landmark categories at all — 'road' silently absorbed every
// one of those kinds as plain road tiles. That drift is exactly why the
// BUG-642 O(water-plants x buildings) freeze (488 water-kind buildings x
// 29,831 buildings = 5.4s on Aaron's real city) never reddened this gate:
// the mix being ticked bore no resemblance to the mix that froze. The table
// below (CATEGORY_FRACTIONS) is instead DERIVED from a real 29,831-building
// dogfood capture (Aaron's live savepoint, decoded via saveCodec.ts +
// tallied by SPECS[...].kind, captured 2026-09-03 — see BUG-644 on the BOW
// for the exact repro script). Measured kind counts on that capture:
//   road 14139 (47.40%)  motorway 4663 (15.63%)  school 3276 (10.98%)
//   park 2819 (9.45%)    power 2040 (6.84%)      rail 1109 (3.72%)
//   water 488 (1.64%)    health 350 (1.17%)      residential 348 (1.17%)
//   fire 198 (0.66%)     industrial 108 (0.36%)  commercial 98 (0.33%)
//   police 80 (0.27%)    office 55 (0.18%)       transport 33 (0.11%)
//   mine 14 (0.05%)      civic 7 (0.02%)         station 3 (0.01%)
//   pylon 2 (0.01%)      landmark 1 (0.00%)      -- sums to 29,831.
// Every non-residential, non-'road' fraction below is that kind's share of
// the REMAINING (post-residential) 29,483 buildings (kind-count / 29483),
// so it composes with the pre-existing "'road' absorbs the remainder" rule
// unchanged. residential itself is NOT in this table — see fillResidential's
// doc comment for why capacity-driven placement (not a fixed fraction) is
// still correct, and how it now also lands close to the real 1.17% share.

import {
  SPECS,
  MAP_W,
  MAP_H,
  capacityAtTier,
  onlineResidentsCapacity,
} from '../../src/sim/data.ts';
import { initialState, reducer, nextSafeBuildingId } from '../../src/sim/engine.ts';

/** Default total building count — Aaron's reported live-city order of magnitude. */
export const DEFAULT_BUILDING_COUNT = 13000;

/** Default population-capacity floor — Aaron's reported live-city figure. */
export const DEFAULT_TARGET_POPULATION = 1_400_000;

/** Cap on how many buildings the residential capacity-driven loop may place,
 * so a catalogue change (e.g. every residential spec's capacity dropping)
 * can't silently blow the residential share past the whole building budget. */
const RESIDENTIAL_MAX_COUNT = 6000;

/** Non-residential category fractions of the REMAINING (post-residential)
 * building budget. 'road' is deliberately absent — it absorbs whatever
 * rounding remainder is left so the total always lands exactly on
 * buildingCount (never approximate — a CI gate must be exactly reproducible).
 * See the file-header COMPOSITION PROVENANCE note above for where every
 * number comes from — this is no longer a hand-guessed mix. */
const CATEGORY_FRACTIONS = [
  ['motorway', 4663 / 29483],
  ['school', 3276 / 29483],
  ['park', 2819 / 29483],
  ['power', 2040 / 29483],
  ['rail', 1109 / 29483],
  ['water', 488 / 29483],
  ['health', 350 / 29483],
  ['fire', 198 / 29483],
  ['industrial', 108 / 29483],
  ['commercial', 98 / 29483],
  ['police', 80 / 29483],
  ['office', 55 / 29483],
  ['transport', 33 / 29483],
  ['mine', 14 / 29483],
  ['civic', 7 / 29483],
  ['station', 3 / 29483],
  ['pylon', 2 / 29483],
  ['landmark', 1 / 29483],
];

function specsOfKind(kind) {
  return Object.values(SPECS)
    .filter((sp) => sp.kind === kind && !sp.placeholder)
    .sort((a, b) => a.id.localeCompare(b.id));
}

/**
 * Places `count` buildings of the given kind, cycling deterministically
 * through every unlocked spec of that kind (and, where a spec has
 * capacityTiers, through every tier) so the fixture exercises the full
 * catalogue breadth rather than one repeated spec.
 */
function fillKind(kind, count, coord) {
  const specs = specsOfKind(kind);
  const out = [];
  if (specs.length === 0 || count <= 0) return out;
  for (let i = 0; i < count; i++) {
    const sp = specs[i % specs.length];
    const tier = sp.capacityTiers ? Math.floor(i / specs.length) % sp.capacityTiers.length : undefined;
    const { x, y } = coord(sp);
    const b = { spec: sp.id, x, y };
    if (tier !== undefined) b.capacityTier = tier;
    out.push(b);
  }
  return out;
}

/** Every (spec, tier) combination of kind 'residential', one entry per
 * combination (a spec with no capacityTiers contributes exactly one entry),
 * sorted by capacity DESCENDING. Used by fillResidential below — see that
 * function's doc comment for why descending order (not the catalogue's
 * natural id order) is the fix for BUG-644's composition drift. */
function allResidentialCombosByCapacityDesc() {
  const specs = specsOfKind('residential');
  const combos = [];
  for (const sp of specs) {
    if (sp.capacityTiers) {
      for (let tier = 0; tier < sp.capacityTiers.length; tier++) {
        combos.push({ sp, tier, cap: capacityAtTier(sp, tier) });
      }
    } else {
      combos.push({ sp, tier: undefined, cap: capacityAtTier(sp, undefined) });
    }
  }
  combos.sort((a, b) => b.cap - a.cap);
  return combos;
}

/**
 * Builds residential buildings until online capacity reaches targetPopulation
 * (or RESIDENTIAL_MAX_COUNT buildings is hit — see that constant's doc).
 *
 * BUG-644 COMPOSITION FIX: the previous version round-robinned uniformly
 * through spec+tier combos in catalogue-id order — which, because the
 * lowest-capacity combos (res_hut cap 8, res_terrace cap 30, ...) sort before
 * the towers alphabetically, spent many early iterations on tiny-capacity
 * buildings and needed 300+ buildings total to reach a 1.4M-population
 * target. Aaron's real city reaches 1.44M population from just 348
 * residential buildings (1.17% of 29,831) because a handful of the
 * highest-capacity tower tiers (res_tower_sgp/res_tower_nyc/
 * res_estate_sprawl at their top tiers, tens of thousands of capacity each)
 * do almost all of the work. Cycling the SAME combo list but sorted by
 * capacity DESCENDING reproduces that shape directly — still visits every
 * combo (so every base-capacity and auto-scaled-tier code path keeps getting
 * exercised on each full cycle) but the biggest contributors land first,
 * landing on ~1% residential share at the default 1.4M target instead of the
 * previous ~2.4%. Deliberately still a plain bounded while-loop (not a
 * separate "place every combo once" pass) — a fixed pass would place every
 * one of the ~57 combos regardless of targetPopulation, which BREAKS the
 * several OTHER tests in this repo that build small fixtures via this same
 * function (buildingCount as low as 50, e.g. render/mapRenderer.test.mjs) by
 * making residential alone exceed the whole building budget.
 */
function fillResidential(targetPopulation, coord) {
  const combos = allResidentialCombosByCapacityDesc();
  const out = [];
  let capacity = 0;
  let i = 0;
  while (capacity < targetPopulation && out.length < RESIDENTIAL_MAX_COUNT) {
    const combo = combos[i % combos.length];
    const { x, y } = coord(combo.sp);
    const b = { spec: combo.sp.id, x, y };
    if (combo.tier !== undefined) b.capacityTier = combo.tier;
    out.push(b);
    capacity += combo.cap;
    i++;
  }
  return { buildings: out, capacity };
}

/** Deterministic raster coordinate assignment. Overlap between buildings is
 * EXPECTED and harmless here (see file header — this is direct construction,
 * not the collision-checked 'place' reducer path); the raster only keeps
 * every (x, y) inside the map bounds so no derivation ever indexes outside
 * [0, MAP_W) x [0, MAP_H). */
function makeCoordAllocator() {
  const CELL = 10;
  const cols = Math.max(1, Math.floor(MAP_W / CELL));
  const rows = Math.max(1, Math.floor(MAP_H / CELL));
  let idx = 0;
  return (sp) => {
    const gx = idx % cols;
    const gy = Math.floor(idx / cols) % rows;
    idx++;
    const x = Math.max(0, Math.min(MAP_W - sp.w, gx * CELL));
    const y = Math.max(0, Math.min(MAP_H - sp.h, gy * CELL));
    return { x, y };
  };
}

/**
 * Builds the deterministic scale fixture: buildingCount buildings (default
 * DEFAULT_BUILDING_COUNT), residential capacity reaching at least
 * targetPopulation (default DEFAULT_TARGET_POPULATION), settled through
 * `settleTicks` REAL reducer 'tick' actions.
 *
 * Funds are seeded very high (FIXTURE_FUNDS) so ~13k buildings' worth of
 * upkeep never trips insolvency/bailout/administration state machinery
 * mid-settle — this gate measures TICK COST at scale, not the (separately
 * tested) insolvency state machine.
 */
export function buildScaleFixture({
  buildingCount = DEFAULT_BUILDING_COUNT,
  targetPopulation = DEFAULT_TARGET_POPULATION,
  settleTicks = 3,
} = {}) {
  const coord = makeCoordAllocator();

  const { buildings: residential, capacity } = fillResidential(targetPopulation, coord);

  const remaining = Math.max(0, buildingCount - residential.length);
  const nonResidential = [];
  let used = 0;
  for (const [kind, frac] of CATEGORY_FRACTIONS) {
    const n = Math.round(remaining * frac);
    nonResidential.push(...fillKind(kind, n, coord));
    used += n;
  }
  // 'road' absorbs the exact rounding remainder — the total ALWAYS equals
  // buildingCount exactly, never "approximately".
  const roadCount = Math.max(0, remaining - used);
  nonResidential.push(...fillKind('road', roadCount, coord));

  const allPlaced = [...residential, ...nonResidential];

  let nextId = 1;
  const buildings = allPlaced.map((b) => ({ id: nextId++, ...b }));

  let s = initialState();
  s = {
    ...s,
    buildings,
    population: Math.floor(capacity * 0.98),
    // Seeded high: see doc comment above. Arbitrary round figure, far above
    // any plausible per-tick expense at this scale.
    funds: 1_000_000_000_000,
    fundsAtTickStart: 1_000_000_000_000,
    fundsAtTickEnd: 1_000_000_000_000,
    // BUG-644: this fixture direct-constructs `buildings` with hand-assigned
    // sequential ids (1..buildingCount above) but never touched `nextId`,
    // which stayed at initialState()'s small starter-city default. Any
    // reducer path that auto-places a new building (residential/commercial
    // demand response, road auto-extension, ...) mints its next id from that
    // stale low counter and collides with an existing hand-built building —
    // caught by runConsistencyChecks' buildings.ids-unique check once enough
    // ticks run for an auto-build to fire (reproduced here: BUG-644's own
    // longer-running per-selector sampling loop hit a "771 duplicate
    // building IDs" failure at tick ~25 past settle before this fix).
    // nextSafeBuildingId (engine.ts) is the same helper the reducer's own
    // rehydration path uses for exactly this class of bug (see its doc
    // comment) — reuse it here rather than re-deriving max+1 by hand.
    nextId: nextSafeBuildingId(buildings),
  };

  for (let i = 0; i < settleTicks; i++) {
    s = reducer(s, { type: 'tick' });
  }

  return s;
}

/** Convenience: population capacity of the residential mix chosen by
 * fillResidential, exposed for the test's own sanity assertions. */
export function residentialCapacityOf(s) {
  return onlineResidentsCapacity(s);
}
