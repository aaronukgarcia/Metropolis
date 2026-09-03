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

import {
  SPECS,
  MAP_W,
  MAP_H,
  capacityAtTier,
  onlineResidentsCapacity,
} from '../../src/sim/data.ts';
import { initialState, reducer } from '../../src/sim/engine.ts';

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
 * buildingCount (never approximate — a CI gate must be exactly reproducible). */
const CATEGORY_FRACTIONS = [
  ['office', 0.14],
  ['industrial', 0.12],
  ['commercial', 0.10],
  ['park', 0.08],
  ['health', 0.05],
  ['police', 0.03],
  ['fire', 0.03],
  ['school', 0.05],
  ['civic', 0.03],
  ['power', 0.02],
  ['water', 0.03],
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

/**
 * Builds residential buildings until online capacity reaches targetPopulation
 * (or RESIDENTIAL_MAX_COUNT buildings is hit — see that constant's doc).
 * Cycles through every residential spec AND every capacityTier so both the
 * base-capacity and auto-scaled-tier code paths get real coverage at scale.
 */
function fillResidential(targetPopulation, coord) {
  const specs = specsOfKind('residential');
  const out = [];
  let capacity = 0;
  let i = 0;
  while (capacity < targetPopulation && out.length < RESIDENTIAL_MAX_COUNT) {
    const sp = specs[i % specs.length];
    const tier = sp.capacityTiers ? Math.floor(i / specs.length) % sp.capacityTiers.length : 0;
    const cap = capacityAtTier(sp, tier);
    const { x, y } = coord(sp);
    const b = { spec: sp.id, x, y };
    if (sp.capacityTiers) b.capacityTier = tier;
    out.push(b);
    capacity += cap;
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
