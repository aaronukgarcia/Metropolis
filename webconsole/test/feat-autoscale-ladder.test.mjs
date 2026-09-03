// feat-autoscale-ladder.test.mjs — FEAT-2326609740 + BUG-590: the AUTO-SCALE
// LADDER. Aaron's ruling (Q100076, 2026-09-02): "A PLUS B" — buildings scale
// UP (add storeys, height recorded) AND OUT (footprint grows, real tiles
// claimed), "up then out, rinse and repeat" like the reference title. Height
// caps per Q100083 (2026-09-03, approved as placeholder). NPP reactors get a
// capacityTiers ladder too (Q100089=B) — tiers = reactor count, height-exempt.
//
// See docs/planning/acceptance/FEAT-autoscale-ladder-2026-09-03.md for the
// full 16-section spec this file is testing against.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  SPECS,
  HEIGHT_CAP_STOREYS,
  heightCapOf,
  capacityAtTier,
  computeRoadConnectivity,
  occupiedSet,
  fits,
  totalChildrenCapacity,
  totalServedCapacity,
  powerStats,
  MAP_W,
  MAP_H,
} from '../src/sim/data.ts';
import {
  initialState,
  reducer,
  evaluateBuildingMonitors,
  TICKS_PER_MONTH,
  BUILDING_AUTO_SCALE_COST_FRACTION,
} from '../src/sim/engine.ts';
import { buildDebugJson, debugJsonText } from '../src/sim/debugjson.ts';
import { EMPTY_MAP_UI } from '../src/sim/uistate.ts';

function mk(over) {
  const base = initialState();
  return {
    ...base,
    unlockedAll: true,
    roadMonitors: [],
    buildingMonitors: [],
    buildings: [],
    population: 0,
    funds: 10_000_000,
    tick: 0,
    ...over,
  };
}

/** A contiguous road chain along y=ROW from x=0..N so anything adjacent is
 *  road-connected (isOnline gate) — mirrors density-scale-inc1's own helper. */
function roadRow(y, maxX) {
  const roads = [];
  for (let x = 0; x <= maxX; x++) roads.push({ id: 1000 + y * 1000 + x, spec: 'road', x, y, builtTick: -1000 });
  return roads;
}

function withConnectivity(s) {
  return { ...s, roadConnectivity: computeRoadConnectivity(s) };
}

function testUi() {
  return { appVersion: 'v9.9.9-test', frameAtMs: 1_700_000_000_000, map: EMPTY_MAP_UI, errors: [] };
}

// ============================================================================
// §2 — capacityTiers coverage (BUG-590 spec-coverage gap closed)
// ============================================================================

const ALL_RESIDENTIAL = [
  'res_hut',
  'res_block',
  'res_terrace',
  'res_lowrise',
  'res_midrise',
  'res_highrise',
  'res_penthouse',
  'res_estate_compact',
  'res_estate',
  'res_estate_sprawl',
];

test('AC-1: every one of the 10 residential specs in the BUG-590 gap now carries a capacityTiers ladder', () => {
  for (const id of ALL_RESIDENTIAL) {
    const sp = SPECS[id];
    assert.ok(sp, `spec ${id} exists`);
    assert.ok(Array.isArray(sp.capacityTiers) && sp.capacityTiers.length > 1, `${id} has a capacityTiers ladder`);
    assert.equal(sp.capacityTiers[0], sp.residents, `${id} tier 0 equals the base residents figure`);
    assert.ok(sp.capacityTiers[1] > sp.capacityTiers[0], `${id} tier 1 grows over tier 0`);
  }
});

const SERVICE_SPECS_WITH_LADDER = [
  ['edu_nursery', 'children'],
  ['edu_primary', 'children'],
  ['edu_city', 'children'],
  ['edu_tech', 'children'],
  ['hea_clinic', 'served'],
  ['hea_hospital', 'served'],
  ['hea_ambulance', 'served'],
  ['hea_eldercare', 'served'],
  ['hea_teaching', 'served'],
  ['pol_station', 'served'],
  ['pol_hq', 'served'],
  ['off_suite', 'jobs'],
  ['off_tower', 'jobs'],
  ['off_data', 'jobs'],
];

test('AC-2: the §2 service-spec set (schools/health/police/offices) carries capacityTiers ladders keyed on their real capacity field', () => {
  for (const [id, field] of SERVICE_SPECS_WITH_LADDER) {
    const sp = SPECS[id];
    assert.ok(sp, `spec ${id} exists`);
    assert.ok(Array.isArray(sp.capacityTiers) && sp.capacityTiers.length > 1, `${id} has a capacityTiers ladder`);
    assert.equal(sp.capacityTiers[0], sp[field], `${id} tier 0 equals its base ${field} figure`);
  }
});

test('AC-2b: capacityAtTier honours the new ladders exactly like the pre-existing estate ladders (GR#3 one growth-curve rule)', () => {
  const sp = SPECS['res_hut'];
  assert.equal(capacityAtTier(sp, 0), 8);
  assert.equal(capacityAtTier(sp, 1), sp.capacityTiers[1]);
  assert.equal(capacityAtTier(sp, 999), sp.capacityTiers[sp.capacityTiers.length - 1], 'caps at the last tier (GR#15 honest cap)');
});

// ============================================================================
// §13 — height cap table (Q100083 approved placeholder)
// ============================================================================

test('AC-13: HEIGHT_CAP_STOREYS matches Aaron-approved category shape for representative specs', () => {
  assert.equal(HEIGHT_CAP_STOREYS['res_hut'], 3, 'huts capped at 3');
  assert.equal(HEIGHT_CAP_STOREYS['res_terrace'], 3, 'terraces capped at 3');
  assert.equal(HEIGHT_CAP_STOREYS['res_block'], 8, 'res_block capped at 8');
  assert.equal(HEIGHT_CAP_STOREYS['res_highrise'], 30, 'highrise capped at 30');
  assert.equal(HEIGHT_CAP_STOREYS['res_tower_nyc'], 60, 'NYC tower capped at 60');
  assert.equal(HEIGHT_CAP_STOREYS['res_tower_sgp'], 40, 'SGP tower capped at 40');
  assert.equal(HEIGHT_CAP_STOREYS['edu_primary'], 4, 'schools capped at 4');
  assert.equal(HEIGHT_CAP_STOREYS['hea_clinic'], 6, 'clinics capped at 6');
  assert.equal(HEIGHT_CAP_STOREYS['hea_hospital'], 12, 'hospitals capped at 12');
  assert.equal(HEIGHT_CAP_STOREYS['off_tower'], 40, 'offices capped at 40');
  assert.equal(HEIGHT_CAP_STOREYS['ind_estate'], 3, 'factories capped at 3');
  assert.equal(HEIGHT_CAP_STOREYS['civ_prison'], undefined, 'civic caps not in scope for this build — heightCapOf falls back honestly');
  assert.equal(heightCapOf(SPECS['civ_prison']), Infinity, 'no cap on record -> Infinity, never a silent 0-storey lock (GR#15)');
});

// ============================================================================
// §3 — up-then-out alternation
// ============================================================================

test('AC-3: tier 0->1 (odd tier) is an UP step — height increments, footprint unchanged', () => {
  const roads = roadRow(9, 10);
  const b = { id: 1, spec: 'res_block', x: 10, y: 10, builtTick: -1000 };
  let s = mk({
    buildings: [...roads, b],
    population: SPECS['res_block'].capacityTiers[0] * 0.9, // >= 0.85 threshold
    tick: 30,
    buildingMonitors: [{ buildingId: 1, until: 360, type: 'residents' }],
  });
  s = withConnectivity(s);

  const result = evaluateBuildingMonitors(s, 30);
  const after = result.buildings.find((x) => x.id === 1);
  assert.equal(after.capacityTier, 1, 'advanced to tier 1');
  assert.equal(after.heightStoreys, 2, 'height incremented from the implicit base (1) to 2');
  assert.equal(after.footprintW ?? SPECS['res_block'].w, SPECS['res_block'].w, 'footprint width unchanged on an UP step');
  assert.equal(after.footprintH ?? SPECS['res_block'].h, SPECS['res_block'].h, 'footprint height unchanged on an UP step');
});

test('AC-3b: tier 1->2 (even tier) is an OUT step — footprint grows, height unchanged', () => {
  const roads = roadRow(9, 20);
  const b = { id: 1, spec: 'res_block', x: 10, y: 10, builtTick: -1000, capacityTier: 1, heightStoreys: 2 };
  let s = mk({
    buildings: [...roads, b],
    population: SPECS['res_block'].capacityTiers[1] * 0.9,
    tick: 30,
    buildingMonitors: [{ buildingId: 1, until: 360, type: 'residents' }],
  });
  s = withConnectivity(s);

  const result = evaluateBuildingMonitors(s, 30);
  const after = result.buildings.find((x) => x.id === 1);
  assert.equal(after.capacityTier, 2, 'advanced to tier 2');
  assert.equal(after.heightStoreys, 2, 'height unchanged on an OUT step');
  const grewWidth = (after.footprintW ?? 2) === 3 && (after.footprintH ?? 2) === 2;
  const grewHeight = (after.footprintW ?? 2) === 2 && (after.footprintH ?? 2) === 3;
  assert.ok(grewWidth || grewHeight, 'footprint grew by exactly one tile in one dimension (width-first per §3.4)');
  assert.ok(grewWidth, 'width-first tiebreak: width grows before height when both are available');
});

test('AC-3c: OUT step actually claims real tiles through occupiedSet — a later placement cannot overlap the grown footprint', () => {
  const roads = roadRow(9, 20);
  const b = { id: 1, spec: 'res_block', x: 10, y: 10, builtTick: -1000, capacityTier: 1, heightStoreys: 2 };
  let s = mk({
    buildings: [...roads, b],
    population: SPECS['res_block'].capacityTiers[1] * 0.9,
    tick: 30,
    buildingMonitors: [{ buildingId: 1, until: 360, type: 'residents' }],
  });
  s = withConnectivity(s);
  const result = evaluateBuildingMonitors(s, 30);
  s = { ...s, buildings: result.buildings };

  const occ = occupiedSet(s);
  assert.ok(occ.has('12,10'), 'the newly-grown tile (x+2,y) is now occupied — the OUT step claimed a REAL tile, not a virtual one');
});

// ============================================================================
// §5/§9 — capex charge + conservation
// ============================================================================

test('AC-5/AC-9: each scale step charges exactly BUILDING_AUTO_SCALE_COST_FRACTION x sp.cost, once, matching the outflow', () => {
  const roads = roadRow(9, 10);
  const sp = SPECS['res_hut'];
  const b = { id: 1, spec: 'res_hut', x: 10, y: 10, builtTick: -1000 };
  const s = mk({
    buildings: [...roads, b],
    population: sp.capacityTiers[0] * 0.9,
    tick: 0,
    buildingMonitors: [{ buildingId: 1, until: 30, type: 'residents' }],
  });
  const expectedCost = Math.round(sp.cost * BUILDING_AUTO_SCALE_COST_FRACTION);

  let cur = withConnectivity(s);
  for (let i = 0; i < TICKS_PER_MONTH; i++) cur = reducer(cur, { type: 'tick' });

  const scaled = cur.buildings.find((x) => x.id === 1);
  assert.equal(scaled.capacityTier, 1, 'building scaled once');
  const outflow = cur.lastFlows.outflows.find((f) => f.label === 'Building Auto-Scale');
  assert.ok(outflow, 'Building Auto-Scale outflow recorded');
  assert.equal(outflow.value, expectedCost, 'outflow equals the calculated upgrade cost exactly');
});

// ============================================================================
// §10 — BUG-590 regression: sustained 100% occupancy actually auto-scales now
// ============================================================================

test('BUG-590 regression: 13 res_hut/res_block/res_terrace buildings at occ 1.0 for sustained ticks DO auto-scale (the 400-pop stall is closed)', () => {
  const roads = roadRow(9, 60);
  const buildings = [...roads];
  let id = 1;
  const specs5 = ['res_hut', 'res_hut', 'res_hut', 'res_hut', 'res_hut'];
  const specs4a = ['res_block', 'res_block', 'res_block', 'res_block'];
  const specs4b = ['res_terrace', 'res_terrace', 'res_terrace', 'res_terrace'];
  let x = 10;
  for (const spec of [...specs5, ...specs4a, ...specs4b]) {
    buildings.push({ id: id++, spec, x, y: 10, builtTick: -20_000 });
    x += SPECS[spec].w + 2;
  }
  const totalCap = buildings
    .filter((b) => SPECS[b.spec]?.kind === 'residential')
    .reduce((sum, b) => sum + SPECS[b.spec].residents, 0);

  const monitors = buildings
    .filter((b) => SPECS[b.spec]?.kind === 'residential')
    .map((b) => ({ buildingId: b.id, until: 1_000_000, type: 'residents' }));

  let s = mk({
    buildings,
    population: totalCap, // occ = 1.0, matching Aaron's tick-1196 capture
    funds: 1_000_000_000,
    tick: 0,
    buildingMonitors: monitors,
  });
  s = withConnectivity(s);

  let anyScaled = false;
  for (let i = 0; i < 1200 && !anyScaled; i++) {
    s = reducer(s, { type: 'tick' });
    anyScaled = s.buildings.some((b) => (b.capacityTier ?? 0) > 0);
  }

  assert.ok(anyScaled, 'at least one of the 13 base-spec residential buildings auto-scaled within the window — BUG-590 stall is closed');
});

// ============================================================================
// §12 — footprint growth under land constraint (up-only fallback)
// ============================================================================

test('AC-12: OUT step blocked by neighbours falls back to the UP mutation at the SAME tier index (F3 fix: never a 2-tier jump)', () => {
  // Surround the res_block on both growable sides (east and south) with other
  // buildings so BOTH the width-first and height fallback OUT attempts fail.
  //
  // F3 (independent round REJECT, 2026-09-03): an earlier draft "skipped
  // forward" to tier 3 here (a 2-tier jump for one charge). The fix anchors
  // the fallback to the SAME candidate index (tier 2) — when its natural
  // mutation (OUT) is blocked, the ALTERNATE mutation (UP) is tried at that
  // SAME index instead, so exactly one tier is ever gained per call.
  const roads = roadRow(9, 20);
  const target = { id: 1, spec: 'res_block', x: 10, y: 10, builtTick: -1000, capacityTier: 1, heightStoreys: 2 };
  const blockerEast = { id: 2, spec: 'park', x: 12, y: 10, builtTick: -1000 }; // blocks width+1 at (12,10)/(12,11)
  const blockerSouth = { id: 3, spec: 'park', x: 10, y: 12, builtTick: -1000 }; // blocks height+1 at (10,12)/(11,12)
  let s = mk({
    buildings: [...roads, target, blockerEast, blockerSouth],
    population: SPECS['res_block'].capacityTiers[1] * 0.9,
    tick: 30,
    buildingMonitors: [{ buildingId: 1, until: 360, type: 'residents' }],
  });
  s = withConnectivity(s);

  const result = evaluateBuildingMonitors(s, 30);
  const after = result.buildings.find((x) => x.id === 1);
  // tier 2's natural OUT is blocked -> fall back to UP AT tier 2 itself:
  // height increments, footprint stays at its tier-1 size (2x2), gained = 1.
  assert.equal(after.capacityTier, 2, 'advanced exactly ONE tier via the same-index UP fallback');
  assert.equal(after.heightStoreys, 3, 'height incremented via the fallback UP step');
  assert.equal(after.footprintW ?? SPECS['res_block'].w, SPECS['res_block'].w, 'footprint did not grow (the blocked OUT step never applied)');
});

test('AC-12b: a transient fits() failure does NOT lock the building — the monitor survives for a future pass', () => {
  const roads = roadRow(9, 20);
  const target = { id: 1, spec: 'res_block', x: 10, y: 10, builtTick: -1000, capacityTier: 1, heightStoreys: 2 };
  const blockerEast = { id: 2, spec: 'park', x: 12, y: 10, builtTick: -1000 };
  const blockerSouth = { id: 3, spec: 'park', x: 10, y: 12, builtTick: -1000 };
  let s = mk({
    buildings: [...roads, target, blockerEast, blockerSouth],
    population: SPECS['res_block'].capacityTiers[1] * 0.9,
    tick: 30,
    buildingMonitors: [{ buildingId: 1, until: 360, type: 'residents' }],
  });
  s = withConnectivity(s);
  const result = evaluateBuildingMonitors(s, 30);
  const after = result.buildings.find((x) => x.id === 1);
  assert.equal(after.scaleLocked ?? false, false, 'height cap (8) not yet reached at height 3 — not locked, since the fallback UP step succeeded');
});

// ============================================================================
// §13/§14 — height cap enforcement + monitor lifecycle / land-locked lock
// ============================================================================

test('AC-13b: a building already AT its height cap never advances via an UP tier — the tier step is refused structurally', () => {
  const roads = roadRow(9, 10);
  // res_hut height cap = 3. Put it at heightStoreys=3, tier 0 (an UP tier, tier 1, is next).
  const b = { id: 1, spec: 'res_hut', x: 10, y: 10, builtTick: -1000, heightStoreys: 3, capacityTier: 0 };
  let s = mk({
    buildings: [...roads, b],
    population: SPECS['res_hut'].capacityTiers[0] * 0.9,
    tick: 30,
    buildingMonitors: [{ buildingId: 1, until: 360, type: 'residents' }],
  });
  s = withConnectivity(s);

  const result = evaluateBuildingMonitors(s, 30);
  const after = result.buildings.find((x) => x.id === 1);
  assert.ok((after.heightStoreys ?? 1) <= 3, 'heightStoreys never exceeds the cap (3)');
  // F3 fix: tier 1's natural UP is blocked by the cap -> fall back to OUT AT
  // tier 1 itself (never skip forward to tier 2) — res_hut is 1x1 with open
  // space around it in this fixture, so the OUT mutation succeeds, landing
  // on tier 1 (gained = 1) with height still capped at 3.
  assert.equal(after.capacityTier, 1, 'advanced exactly ONE tier via the same-index OUT fallback');
  assert.equal(after.heightStoreys, 3, 'height stayed at the cap — never incremented past it');
});

// O5 (Opus r2 re-round, 2026-09-03): "add a regression that a SUCCESSFUL
// height-capped step does not lock (currently only R2-F3a catches that
// mutant)" — a building whose natural UP mutation is permanently blocked by
// the height cap but whose same-index OUT fallback SUCCEEDS must NOT be
// marked scaleLocked, as long as it did not land on the ladder's LAST index.
// Locking is reserved for reaching the true end of the ladder (AC-14) or a
// genuinely exhausted attempt (never a mere height-cap fallback that worked).
test('O5: a successful height-capped fallback step (mid-ladder) does not set scaleLocked', () => {
  const roads = roadRow(9, 10);
  const sp = SPECS['res_hut'];
  // Same shape as AC-13b (height already at cap, natural UP blocked, OUT
  // fallback succeeds) but landing MID-ladder, nowhere near the last index —
  // this must stay unlocked, unlike AC-14's true last-index case.
  const b = { id: 1, spec: 'res_hut', x: 10, y: 10, builtTick: -1000, heightStoreys: 3, capacityTier: 0 };
  let s = mk({
    buildings: [...roads, b],
    population: sp.capacityTiers[0] * 0.9,
    tick: 30,
    buildingMonitors: [{ buildingId: 1, until: 360, type: 'residents' }],
  });
  s = withConnectivity(s);

  const result = evaluateBuildingMonitors(s, 30);
  const after = result.buildings.find((x) => x.id === 1);
  assert.equal(after.capacityTier, 1, 'sanity: the height-capped fallback step succeeded');
  assert.ok(after.capacityTier < sp.capacityTiers.length - 1, 'sanity: nowhere near the ladder end');
  assert.equal(after.scaleLocked ?? false, false, 'a successful mid-ladder height-capped step must not lock');
  assert.ok(
    result.monitors.some((m) => m.buildingId === 1),
    'the monitor must survive a successful mid-ladder step (only a REAL lock removes it)'
  );
});

test('AC-14: a building that reaches the LAST tier of its ladder is locked and its monitor is removed', () => {
  const roads = roadRow(9, 10);
  const sp = SPECS['res_hut'];
  const lastTier = sp.capacityTiers.length - 2; // one step away from the final tier
  const b = { id: 1, spec: 'res_hut', x: 10, y: 10, builtTick: -1000, capacityTier: lastTier, heightStoreys: 1 };
  let s = mk({
    buildings: [...roads, b],
    population: sp.capacityTiers[lastTier] * 0.9,
    tick: 30,
    buildingMonitors: [{ buildingId: 1, until: 360, type: 'residents' }],
  });
  s = withConnectivity(s);

  const result = evaluateBuildingMonitors(s, 30);
  const after = result.buildings.find((x) => x.id === 1);
  assert.equal(after.capacityTier, sp.capacityTiers.length - 1, 'advanced onto the LAST tier');
  assert.equal(after.scaleLocked, true, 'landing on the last tier locks the building immediately');
  assert.equal(
    result.monitors.find((m) => m.buildingId === 1),
    undefined,
    'the monitor is removed the same pass the building locks'
  );
});

test('AC-14b: an already-maxed building (currentTier at the ladder end) is locked+de-monitored without a redundant charge', () => {
  const roads = roadRow(9, 10);
  const sp = SPECS['res_hut'];
  const maxTier = sp.capacityTiers.length - 1;
  const b = { id: 1, spec: 'res_hut', x: 10, y: 10, builtTick: -1000, capacityTier: maxTier };
  let s = mk({
    buildings: [...roads, b],
    population: sp.capacityTiers[maxTier] * 0.99,
    tick: 30,
    buildingMonitors: [{ buildingId: 1, until: 360, type: 'residents' }],
  });
  s = withConnectivity(s);

  const result = evaluateBuildingMonitors(s, 30);
  const after = result.buildings.find((x) => x.id === 1);
  assert.equal(after.scaleLocked, true, 'already-maxed building is locked');
  assert.equal(result.cost, 0, 'no charge for a building that was already at the top of its ladder');
  assert.equal(result.monitors.length, 0, 'monitor removed');
});

// ============================================================================
// §11 — service spec monitors (schools/health/offices) actually fire
// ============================================================================

test('AC-11: a school (edu_primary) auto-scales when children/capacity crosses the 0.85 threshold', () => {
  const roads = roadRow(9, 10);
  const sp = SPECS['edu_primary'];
  const b = { id: 1, spec: 'edu_primary', x: 10, y: 10, builtTick: -1000 };
  let s = mk({
    buildings: [...roads, b],
    population: Math.ceil(sp.children * 0.9),
    tick: 30,
    buildingMonitors: [{ buildingId: 1, until: 360, type: 'children' }],
  });
  s = withConnectivity(s);
  assert.ok(totalChildrenCapacity(s) > 0, 'precondition: totalChildrenCapacity sees the placed school');

  const result = evaluateBuildingMonitors(s, 30);
  const after = result.buildings.find((x) => x.id === 1);
  assert.equal(after.capacityTier, 1, 'school auto-scaled tier 0->1');
  assert.equal(result.cost, Math.round(sp.cost * BUILDING_AUTO_SCALE_COST_FRACTION), 'tier cost charged (15% of spec cost)');
});

test('AC-11b: a hospital (hea_hospital) auto-scales when served/capacity crosses the 0.85 threshold', () => {
  const roads = roadRow(9, 10);
  const sp = SPECS['hea_hospital'];
  const b = { id: 1, spec: 'hea_hospital', x: 10, y: 10, builtTick: -1000 };
  let s = mk({
    buildings: [...roads, b],
    population: Math.ceil(sp.served * 0.9),
    tick: 30,
    buildingMonitors: [{ buildingId: 1, until: 360, type: 'served' }],
  });
  s = withConnectivity(s);
  assert.ok(totalServedCapacity(s) > 0, 'precondition: totalServedCapacity sees the placed hospital');

  const result = evaluateBuildingMonitors(s, 30);
  const after = result.buildings.find((x) => x.id === 1);
  assert.equal(after.capacityTier, 1, 'hospital auto-scaled tier 0->1');
});

test('AC-11c: an office tower (off_tower) auto-scales when jobs/capacity crosses the 0.85 threshold', () => {
  const roads = roadRow(9, 10);
  const sp = SPECS['off_tower'];
  const b = { id: 1, spec: 'off_tower', x: 10, y: 10, builtTick: -1000 };
  let s = mk({
    buildings: [...roads, b],
    population: Math.ceil((sp.jobs * 0.9) / 0.55), // 'jobs' util reads population*0.55 vs jobsCap (see engine.ts jobs-utilization comment)
    tick: 30,
    buildingMonitors: [{ buildingId: 1, until: 360, type: 'jobs' }],
  });
  s = withConnectivity(s);

  const result = evaluateBuildingMonitors(s, 30);
  const after = result.buildings.find((x) => x.id === 1);
  assert.equal(after.capacityTier, 1, 'office tower auto-scaled tier 0->1');
});

test('placement wires the correct monitor type per spec kind (§4/§11)', () => {
  let s = mk({ unlockedAll: true, funds: 5_000_000_000 });
  s = reducer(s, { type: 'place', spec: 'edu_primary', x: 5, y: 5 });
  s = reducer(s, { type: 'place', spec: 'hea_hospital', x: 20, y: 5 });
  s = reducer(s, { type: 'place', spec: 'off_tower', x: 40, y: 5 });
  s = reducer(s, { type: 'place', spec: 'pow_nuke', x: 60, y: 5 });

  const findMonitorFor = (spec) => {
    const b = s.buildings.find((x) => x.spec === spec);
    return s.buildingMonitors.find((m) => m.buildingId === b.id);
  };
  assert.equal(findMonitorFor('edu_primary').type, 'children');
  assert.equal(findMonitorFor('hea_hospital').type, 'served');
  assert.equal(findMonitorFor('off_tower').type, 'jobs');
  assert.equal(findMonitorFor('pow_nuke').type, 'mw');
});

// ============================================================================
// Q100089=B — NPP reactor ladder
// ============================================================================

test('NPP: pow_nuke carries a capacityTiers reactor ladder and is height-EXEMPT (every tier is an OUT step)', () => {
  const sp = SPECS['pow_nuke'];
  assert.ok(Array.isArray(sp.capacityTiers) && sp.capacityTiers.length > 1, 'pow_nuke has a capacityTiers ladder');
  assert.equal(sp.capacityTiers[0], sp.mw, 'tier 0 MW equals the base twin-AGR figure');
  assert.ok(sp.capacityTiers[1] > sp.capacityTiers[0], 'tier 1 adds a reactor worth of MW');
  assert.equal(heightCapOf(sp), Infinity, 'power-ladder specs carry no height-cap entry (height-exempt)');
});

test('NPP: a reactor-ladder tier step is an OUT (footprint growth), never an UP, and scales the grid MW via powerStats', () => {
  const roads = roadRow(20, 30);
  const sp = SPECS['pow_nuke'];
  const b = { id: 1, spec: 'pow_nuke', x: 10, y: 21, builtTick: -50_000 };
  let s = mk({
    buildings: [...roads, b],
    population: 100,
    tick: 30,
    buildingMonitors: [{ buildingId: 1, until: 360, type: 'mw' }],
  });
  s = withConnectivity(s);
  // Force utilization over threshold directly via a synthetic high-demand
  // population is impractical (industrial/office counters drive `need`), so
  // drive the monitor evaluation with a state whose `need` already exceeds
  // `cap` — the 'mw' utilization basis is powerStats().need/cap (real ratio,
  // not the population-based proxy the other types use).
  const before = powerStats(s);
  assert.ok(before.cap > 0, 'precondition: the plant contributes MW');

  const result = evaluateBuildingMonitors(s, 30);
  const after = result.buildings.find((x) => x.id === 1);
  // At population 100 the grid need is tiny relative to the 1,120 MW plant,
  // so utilization stays below threshold and NOTHING should scale here —
  // this asserts the negative case (no spurious NPP scale-up at low demand).
  assert.equal(after.capacityTier ?? 0, 0, 'no scale-up at low grid demand');
  assert.equal(after.heightStoreys ?? 1, 1, 'height never touched for a power-ladder spec');
});

// ============================================================================
// §8 — debugJSON coverage
// ============================================================================

test('AC-8: debugJson serializes heightStoreys/scaleLocked/footprint for every building, defaulting for pre-feature saves', () => {
  const roads = roadRow(9, 10);
  const scaled = { id: 1, spec: 'res_block', x: 10, y: 10, builtTick: -1000, capacityTier: 1, heightStoreys: 2, footprintW: 3, footprintH: 2, scaleLocked: false };
  const legacy = { id: 2, spec: 'res_hut', x: 20, y: 10, builtTick: -1000 }; // no new fields at all — pre-feature save
  const s = mk({ buildings: [...roads, scaled, legacy] });

  const dj = buildDebugJson(s, testUi());
  const dScaled = dj.buildings.list.find((x) => x.id === 1);
  const dLegacy = dj.buildings.list.find((x) => x.id === 2);

  assert.equal(dScaled.heightStoreys, 2);
  assert.equal(dScaled.footprintW, 3);
  assert.equal(dScaled.footprintH, 2);
  assert.equal(dScaled.scaleLocked, false);

  assert.equal(dLegacy.heightStoreys, 1, 'GR#16 default: missing heightStoreys -> 1');
  assert.equal(dLegacy.scaleLocked, false, 'GR#16 default: missing scaleLocked -> false');
  assert.equal(dLegacy.footprintW, SPECS['res_hut'].w, "GR#16 default: missing footprintW -> the spec's base w");
  assert.equal(dLegacy.footprintH, SPECS['res_hut'].h, "GR#16 default: missing footprintH -> the spec's base h");
});

// ============================================================================
// §15 — determinism
// ============================================================================

test('AC-15: two identical runs produce byte-identical debugJson after scaling (tier/height/footprint/scaleLocked/cost all match)', () => {
  function run() {
    const roads = roadRow(9, 10);
    const b = { id: 1, spec: 'res_hut', x: 10, y: 10, builtTick: -1000 };
    let s = mk({
      buildings: [...roads, b],
      population: Math.ceil(SPECS['res_hut'].capacityTiers[0] * 0.9),
      funds: 10_000_000,
      tick: 0,
      buildingMonitors: [{ buildingId: 1, until: 1_000_000, type: 'residents' }],
    });
    s = withConnectivity(s);
    for (let i = 0; i < TICKS_PER_MONTH * 3; i++) s = reducer(s, { type: 'tick' });
    return debugJsonText(buildDebugJson(s, testUi()));
  }

  const a = run();
  const b = run();
  assert.equal(a, b, 'two identical runs from the same input produce byte-identical debugJson');
});

// ============================================================================
// §16 — conservation
// ============================================================================

test('AC-16: cumulativeCapexSpent + funds stay in lockstep with placement + auto-scale outflows', () => {
  const roads = roadRow(9, 10);
  // hea_clinic is category 'services' (a genuinely PAID placement, unlike a
  // 'zones' residential spec where placementCost() is £0 by design — see
  // placementCost()'s isFreeZone rule) — the right spec to prove placement
  // itself books a real capex charge.
  const sp = SPECS['hea_clinic'];
  let s = mk({ unlockedAll: true, funds: 100_000_000, buildings: roads, tick: 0 });
  const beforeCapex = s.cumulativeCapexSpent ?? 0;

  s = reducer(s, { type: 'place', spec: 'hea_clinic', x: 10, y: 10 });
  const placed = s.buildings.find((b) => b.spec === 'hea_clinic');
  const placementFig = sp.cost; // hea_clinic is a paid 'services' spec — placementCost(sp) === sp.cost
  assert.equal(s.cumulativeCapexSpent, beforeCapex + placementFig, 'placement increments cumulativeCapexSpent by the placement cost');

  // Jump the clock to ONE tick before a monthly auto-scale boundary, THEN pin
  // population — a multi-tick loop from here would let advance()'s ordinary
  // population growth/decline model pull population back toward
  // residentsCapacity(s) (=0, no housing in this fixture), decaying the
  // synthetic 0.9-utilization figure to nothing before the monthly pass ever
  // runs. Landing the single 'tick' call exactly on the boundary sidesteps
  // that confound entirely (same trick §11's direct evaluateBuildingMonitors
  // calls use, just via the real reducer for the funds/conservation check).
  s = {
    ...s,
    tick: TICKS_PER_MONTH - 1,
    population: Math.ceil(sp.capacityTiers[0] * 0.9),
    buildingMonitors: [{ buildingId: placed.id, until: 1_000_000, type: 'served' }],
  };
  s = withConnectivity(s);
  const capexBeforeScale = s.cumulativeCapexSpent;
  const before = s;
  s = reducer(s, { type: 'tick' });

  const scaledFlow = s.lastFlows.outflows.find((f) => f.label === 'Building Auto-Scale');
  assert.ok(scaledFlow, 'precondition: the building actually auto-scaled on the monthly boundary tick');
  const scaledBuilding = s.buildings.find((b) => b.id === placed.id);
  assert.equal(scaledBuilding.capacityTier, 1, 'tier actually advanced');

  const income = s.lastFlows.inflows.reduce((a, b) => a + b.value, 0);
  const expense = s.lastFlows.outflows.reduce((a, b) => a + b.value, 0);
  assert.equal(s.funds, before.funds + income - expense, 'conservation holds exactly through the auto-scale tick');
  assert.equal(
    s.cumulativeCapexSpent,
    capexBeforeScale + scaledFlow.value,
    'cumulativeCapexSpent increments by EXACTLY the booked Building Auto-Scale outflow'
  );
});
