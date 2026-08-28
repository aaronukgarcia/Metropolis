// density-inc1.test.mjs — FEAT-1972079900 inc1: ESTATE-SCALE placeable specs
// (object density / LOD inc1, the PLACEABLE-tier reading of the brief).
//
// Scope inc1 = a single large object standing for a whole estate, carrying the
// AGGREGATE jobs / residents / upkeep / footprint of the ~N constituent buildings
// it represents. Render-coarsening (auto-merge at zoom) and up-density variants
// are inc2 and are NOT exercised here.
//
// Run with `npm test` (node --test); node type-strips the imported .ts so these
// assertions exercise the exact shipped catalogue + economy + activation + waste
// paths — no copy, no drift. Every test pins REAL values and is written to be
// able to FAIL: change an estate's footprint/jobs/residents, or drop the online
// gate, and an assertion below goes red.
//
// RED proof (scratch cp/mv, NEVER git): revert any estate line in data.ts back to
// its PH(...) placeholder form and tests (1) well-formed + (2) aggregate economy
// go RED (a placeholder carries zero stats and is not placeable). Restore → GREEN.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  SPECS,
  isPlaceable,
  isOnline,
  totalJobs,
  residentsCapacity,
  wasteGeneratedOf,
  computeRoadConnectivity,
  constructionTicks,
  densityTier,
  WASTE_PER_JOB,
  WASTE_PER_RESIDENT,
  PALETTE,
} from '../src/sim/data.ts';
import { initialState, reducer, computeFlows } from '../src/sim/engine.ts';

const EPS = 1e-9;

// The four estate-scale specs graduated in inc1, with the aggregate they stand
// for. These are PLACEHOLDER-balance numbers (Aaron's row-by-row pass pending).
const ESTATES = [
  { id: 'ind_estate',      kind: 'industrial',  jobs: 2000, residents: 0,    w: 6, h: 6, tag: 'pollution' },
  { id: 'com_hypermarket', kind: 'commercial',  jobs: 800,  residents: 0,    w: 5, h: 5, tag: undefined },
  { id: 'off_businesspark', kind: 'office',     jobs: 1200, residents: 0,    w: 5, h: 5, tag: undefined },
  { id: 'res_estate',      kind: 'residential', jobs: 0,    residents: 1500, w: 5, h: 5, tag: undefined },
];

/** A city with the starter map cleared, so only the buildings we add matter. */
function bareCity(pop = 0) {
  const s = initialState();
  s.buildings = [];
  s.population = pop;
  return s;
}

let _id = 800000;
function add(s, spec, opts = {}) {
  assert.ok(SPECS[spec], `test setup: unknown spec "${spec}"`);
  const b = { id: _id++, spec, x: 5, y: 5, ...opts };
  s.buildings.push(b);
  return b;
}

// ───────────────────────── 1. SPECS WELL-FORMED ─────────────────────────

test('each estate is a REAL, placeable spec with the expected kind/category/footprint/stats', () => {
  const god = { ...initialState(), unlockedAll: true };
  for (const e of ESTATES) {
    const sp = SPECS[e.id];
    assert.ok(sp, `estate "${e.id}" missing from SPECS`);
    // Real, not a "coming soon" placeholder.
    assert.notEqual(sp.placeholder, true, `${e.id}: must NOT be a placeholder`);
    assert.equal(isPlaceable(god, sp), true, `${e.id}: must be placeable under unlockedAll`);
    // Correct classification so existing systems treat it as its zone type.
    assert.equal(sp.kind, e.kind, `${e.id}: kind`);
    assert.equal(sp.category, 'zones', `${e.id}: estates are zone-category`);
    // LARGE footprint (4×4..6×6 per the brief).
    assert.equal(sp.w, e.w, `${e.id}: width`);
    assert.equal(sp.h, e.h, `${e.id}: height`);
    assert.ok(sp.w >= 4 && sp.h >= 4, `${e.id}: an estate must be a LARGE footprint (≥4×4)`);
    // Non-zero AGGREGATE stat, as appropriate to the kind.
    if (e.residents > 0) {
      assert.equal(sp.residents, e.residents, `${e.id}: residents`);
      assert.equal(sp.jobs, undefined, `${e.id}: a housing estate carries no jobs`);
    } else {
      assert.equal(sp.jobs, e.jobs, `${e.id}: jobs`);
      assert.equal(sp.residents, undefined, `${e.id}: a workplace estate carries no residents`);
    }
    assert.equal(sp.tag, e.tag, `${e.id}: pollution tag`);
    // CROSS-SYSTEM LEAK GUARDS (the waste_depot lesson): an estate must not smuggle
    // stats into systems it doesn't belong to.
    assert.equal(sp.mw, undefined, `${e.id}: must NOT set mw (would be read as grid GENERATION)`);
    assert.equal(sp.served, undefined, `${e.id}: must NOT set served (no service/water capacity)`);
    assert.equal(sp.children, undefined, `${e.id}: must NOT set children (not a school)`);
    // A large, high-capacity object is a high density tier.
    assert.equal(densityTier(sp), 3, `${e.id}: an estate is a high (tier-3) density block`);
    // Real cost/upkeep (it is a real building the economy charges upkeep for).
    assert.ok(sp.cost > 0, `${e.id}: real cost`);
    assert.ok(sp.upkeep > 0, `${e.id}: real upkeep`);
    assert.ok(sp.blurb.trim().length > 0, `${e.id}: names the scale in its blurb`);
  }
});

// ───────────────────────── 2. AGGREGATE ECONOMY ─────────────────────────

test('aggregate economy: an estate contributes its aggregate jobs to city totals (flows like N buildings)', () => {
  const base = bareCity(0);
  const before = totalJobs(base);
  const s = bareCity(0);
  add(s, 'ind_estate');
  const after = totalJobs(s);
  assert.equal(after - before, 2000, 'ind_estate adds its full aggregate 2,000 industrial jobs');
});

test('aggregate economy: a housing estate contributes its aggregate residents to capacity', () => {
  const base = bareCity(0);
  const before = residentsCapacity(base);
  const s = bareCity(0);
  add(s, 'res_estate');
  const after = residentsCapacity(s);
  assert.equal(after - before, 1500, 'res_estate adds its full aggregate 1,500 residential capacity');
});

test('aggregate economy: an estate contributes its aggregate upkeep to the outflows (delta vs not-placed)', () => {
  const bucketTotal = (s) =>
    computeFlows(s).outflows.filter((f) => f.label === 'Commerce & Industry').reduce((a, f) => a + f.value, 0);
  const base = bareCity(500);
  const before = bucketTotal(base);
  const s = bareCity(500);
  add(s, 'off_businesspark'); // builtTick null ⇒ online (matches the economy-flow convention)
  const after = bucketTotal(s);
  assert.equal(after - before, SPECS.off_businesspark.upkeep, 'office estate upkeep lands in the Commerce & Industry bucket');
  assert.equal(after - before, 420, 'off_businesspark upkeep is its placeholder 420/tick');
});

// ───────────────────────── 3. INHERITS ACTIVATION (road gate) ─────────────────────────

const roadAt = (x, y) => ({ id: 1_000_000 + x * 1000 + y, spec: 'road', x, y, builtTick: -1000 });
const withConn = (s) => ({ ...s, roadConnectivity: computeRoadConnectivity(s) });

test('inherits activation: a road-DISCONNECTED estate is offline (contributes zero) — no new logic', () => {
  // ind_estate occupies x5..10,y5..10. An adjacent road ISLAND at (4,5) is road-side
  // but not reachable from the map edge / trunk → the estate fails the road-connected gate.
  const est = { id: 5, spec: 'ind_estate', x: 5, y: 5, builtTick: -1000 };
  const island = withConn({ ...bareCity(0), buildings: [est, roadAt(4, 5)] });
  assert.equal(isOnline(island, est), false, 'estate beside an isolated (unconnected) road is OFFLINE');
  // Offline ⇒ no aggregate reaches the online-gated systems (waste).
  assert.equal(wasteGeneratedOf(island), 0, 'an offline estate generates no waste');

  // Connected: a road chain from the west map edge (x=0) to the estate → online.
  const chain = [];
  for (let x = 0; x <= 4; x++) chain.push(roadAt(x, 5));
  const connected = withConn({ ...bareCity(0), buildings: [est, ...chain] });
  assert.equal(isOnline(connected, est), true, 'estate beside an edge-connected road is ONLINE');
});

// ───────────────────────── 4. INHERITS WASTE ─────────────────────────

test('inherits waste: an online estate generates refuse proportional to its aggregate jobs/residents', () => {
  // A workplace estate: refuse ∝ jobs. ind_estate online (built long ago, no road
  // gate because bareCity has no roadConnectivity → backward-tolerance online).
  const work = bareCity(0);
  add(work, 'ind_estate'); // builtTick null ⇒ online
  assert.ok(
    Math.abs(wasteGeneratedOf(work) - 2000 * WASTE_PER_JOB) < EPS,
    'ind_estate refuse = 2,000 jobs × per-job rate'
  );

  // A housing estate: refuse ∝ residents.
  const home = bareCity(0);
  add(home, 'res_estate');
  assert.ok(
    Math.abs(wasteGeneratedOf(home) - 1500 * WASTE_PER_RESIDENT) < EPS,
    'res_estate refuse = 1,500 residents × per-resident rate'
  );

  // OFFLINE (under construction) ⇒ zero, exactly like any building.
  const s = bareCity(0);
  const b = add(s, 'ind_estate', { builtTick: s.tick });
  assert.ok(constructionTicks(SPECS.ind_estate) > 0);
  assert.equal(isOnline(s, b), false, 'freshly-placed estate is under construction');
  assert.equal(wasteGeneratedOf(s), 0, 'an under-construction estate generates no waste');
});

// ───────────────────────── 5. CATALOGUE INTEGRITY ─────────────────────────

test('catalogue integrity: each estate appears in exactly ONE palette family', () => {
  for (const e of ESTATES) {
    const fams = PALETTE.filter((f) => f.items.includes(e.id)).map((f) => f.title);
    assert.equal(fams.length, 1, `${e.id}: must appear in exactly one family, found in [${fams}]`);
  }
});

test('catalogue integrity: the estate ids are placeable end-to-end via the reducer (real, enter the sim)', () => {
  const s = { ...initialState(), unlockedAll: true, funds: 10_000_000 };
  s.buildings = [];
  const before = s.buildings.length;
  const after = reducer(s, { type: 'place', spec: 'res_estate', x: 200, y: 120 });
  assert.equal(after.buildings.length, before + 1, 'a real estate spec places into the running sim');
  assert.equal(after.buildings[after.buildings.length - 1].spec, 'res_estate');
});
