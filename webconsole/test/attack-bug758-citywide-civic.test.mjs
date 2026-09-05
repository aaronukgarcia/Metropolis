// attack-bug758-citywide-civic.test.mjs — BUG-758 (P1, Aaron's exact
// sentence: "rather than say the 12 hospitals it should be one teaching
// hospital, its not 40 kindergartens it's a city kindergarten that does
// 1000 children"). The civic-tier consolidator (edu_nursery_city/
// hea_teaching, careTier) grouped candidates INSIDE ONE 16x16 section:
// edu_nursery -> edu_nursery_city needs 33 in a SINGLE section, so a
// dogfood-shaped city with kindergartens SCATTERED across the map (never 33
// in any one section) kept every one of them forever. This proves the fix:
// consolidator.ts's isCityWideFamily/findCityWideOpportunities now group
// health/school families CITY-WIDE, and engine.ts's findSuccessorSite rings
// outward from the group's anchor section for a real free site.
//
// Dogfood-shaped fixture (own build, per the brief — NOT imported from any
// sibling worktree's inc3 builder): ~2,342 buildings total — 448 road tiles
// (one full-width row), 90 edu_nursery scattered 15-per-section across 6
// sections, 12 hea_hospital scattered 2-per-section across 6 DIFFERENT
// sections (so NO section ever holds >= groupSize for EITHER family — the
// exact measured-zero-opportunities shape, not the lucky-clustering shape
// that let hospitals "happen to work" on Aaron's real city), plus 1,792
// filler road tiles (4 more full-width rows) spread across every section on
// the map.
//
// LEAD RULING (Aaron, 2026-09-05, same-day follow-up): CEIL-3's 50%
// family-share ceiling (engine.ts's CONSOLIDATOR_MAX_FAMILY_SHARE) is a
// PER-SECTION-fitting protection ("one XXL nuke for the whole city" — the
// single-point-of-failure risk of a residential/commercial density
// successor swallowing most of its family) and does NOT apply to
// isCityWideFamily (health/school) civic successors — Aaron's own words are
// "it's not 40 kindergartens it's a city kindergarten that does 1000
// children": consolidating ALL of a city's nurseries into City
// Kindergartens IS the intent, not a risk to cap. engine.ts's CEIL-3 check
// now reads `!isCityWideFamily(toSpec) && successorCapacity > ...`. Every
// OTHER capacity gate (BUG-736 capacity-loss, BUG-742 NaN fail-closed,
// protected-class, site, funds, one-per-city) still applies in full to
// civic families — only this per-section-fitting-specific ceiling is
// exempted. See describe block (2b) below for the exact-40 proof and (3b)
// for the RED-PROOF that restoring the ceiling for civic families reds it.
//
// The (2)/(1)/(0) blocks below still use 90 nurseries (not restored to 40)
// deliberately: they exercise the SCATTER/city-wide-grouping mechanism
// itself with a comfortable multi-group margin, independent of this
// ceiling-exemption ruling, which (2b) now covers on its own with the
// EXACT numbers from Aaron's sentence.
//
// Filler is road, not a residential/commercial spec, DELIBERATELY: road is a
// CONNECT_EXEMPT_KIND (capacityFieldOf returns null for it), so it can NEVER
// itself become a consolidation candidate. An earlier version of this
// fixture used res_hut filler, which turned out to be dense enough per
// section to form its OWN res_hut -> res_block opportunities (groupSize 7)
// that out-ranked (higher capacityGain) and ate the whole
// CONSOLIDATOR_MAX_TRANSACTIONS_PER_PASS (4) budget every pass, masking the
// civic fix under test — exactly the kind of fixture-contamination bug a
// read-only filler must not introduce.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { SPECS, computeRoadConnectivity } from '../src/sim/data.ts';
import {
  initialState,
  reducer,
  TICKS_PER_MONTH,
  CONSOLIDATOR_UNLOCK_LEVEL,
  xpForLevel,
  levelOf,
} from '../src/sim/engine.ts';
import { consolidationLadder, findOpportunities, sectionIndexOf, sectionKeyOf } from '../src/sim/consolidator.ts';
import { runWithMutant } from '../testsupport/mutant.mjs';

// ---------------------------------------------------------------------------
// Fixture kit (mirrors the estate's own established harness shape)
// ---------------------------------------------------------------------------

const EDU_COUNT = 90;
const EDU_SECTIONS = 6; // 15 per section
const HOSP_COUNT = 12;
const HOSP_SECTIONS = 6; // 2 per section
const ROAD_MAX_X = 447; // 28 sections x 16 tiles
const FILLER_ROAD_ROWS = 4;

function mk(over) {
  const base = initialState();
  return {
    ...base,
    unlockedAll: true,
    roadMonitors: [],
    buildingMonitors: [],
    buildings: [],
    population: 0,
    funds: 50_000_000_000,
    tick: 0,
    consolidatorEnabled: false,
    consolidatorLog: [],
    xp: xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL),
    lastRewardedLevel: levelOf(xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL)),
    consolidatorMode: 'monthly-twelfth',
    ...over,
  };
}
const withConnectivity = (s) => ({ ...s, roadConnectivity: computeRoadConnectivity(s) });
function advanceToNextBoundary(s) {
  let cur = s;
  do {
    cur = reducer(cur, { type: 'tick' });
  } while (cur.tick % TICKS_PER_MONTH !== 0);
  return cur;
}
const countSpec = (s, id) => s.buildings.filter((b) => b.spec === id).length;

/**
 * The dogfood-shaped fixture — see the file header for the exact shape and
 * why EDU_COUNT is 90, not Aaron's literal "40". SECTION_TILES is 16 —
 * offsets are chosen well inside a 16-tile section so nothing straddles a
 * section boundary, and the two civic clusters sit in disjoint section
 * ranges so neither family's placements can collide with the other's.
 */
function buildDogfoodCity() {
  const buildings = [];
  let id = 1;

  // One full-width road row — 448 tiles (28 sections x 16).
  for (let x = 0; x <= ROAD_MAX_X; x++) {
    buildings.push({ id: id++, spec: 'road', x, y: 0, builtTick: -1000 });
  }

  // edu_nursery: EDU_COUNT / EDU_SECTIONS per section, sx 0..(EDU_SECTIONS-1).
  const perEduSection = EDU_COUNT / EDU_SECTIONS;
  for (let sx = 0; sx < EDU_SECTIONS; sx++) {
    for (let i = 0; i < perEduSection; i++) {
      buildings.push({ id: id++, spec: 'edu_nursery', x: sx * 16 + 1 + i, y: 1, builtTick: -1000 });
    }
  }

  // hea_hospital (2x2): HOSP_COUNT / HOSP_SECTIONS per section, sx 10..15 —
  // a DIFFERENT section range from the edu cluster above.
  const perHospSection = HOSP_COUNT / HOSP_SECTIONS;
  for (let sx = 10; sx < 10 + HOSP_SECTIONS; sx++) {
    for (let i = 0; i < perHospSection; i++) {
      buildings.push({ id: id++, spec: 'hea_hospital', x: sx * 16 + 1 + i * 3, y: 4, builtTick: -1000 });
    }
  }

  // Filler: 4 more full-width road rows (1,792 tiles) — see file header.
  for (let r = 0; r < FILLER_ROAD_ROWS; r++) {
    for (let x = 0; x <= ROAD_MAX_X; x++) {
      buildings.push({ id: id++, spec: 'road', x, y: 20 + r, builtTick: -1000 });
    }
  }

  return buildings;
}

const RUNGS = consolidationLadder();
const NURSERY_RUNG = RUNGS.find((r) => r.from === 'edu_nursery' && r.to === 'edu_nursery_city');
const HOSPITAL_RUNG = RUNGS.find((r) => r.from === 'hea_hospital' && r.to === 'hea_teaching');
const EXPECTED_CITY_KINDERGARTENS = Math.floor(EDU_COUNT / NURSERY_RUNG.groupSize);
const EXPECTED_TEACHING_HOSPITALS = Math.floor(HOSP_COUNT / HOSPITAL_RUNG.groupSize);

// ---------------------------------------------------------------------------
// (0) sanity: the fixture really does reproduce the measured-zero shape.
// ---------------------------------------------------------------------------

describe('BUG-758 (0): fixture sanity — no section ever holds a full civic group', () => {
  test('fixture totals are as designed, and no single section holds >= groupSize of either civic family', () => {
    const buildings = buildDogfoodCity();
    assert.equal(buildings.filter((b) => b.spec === 'edu_nursery').length, EDU_COUNT);
    assert.equal(buildings.filter((b) => b.spec === 'hea_hospital').length, HOSP_COUNT);
    assert.ok(buildings.length > 2000, 'dogfood-shaped: comfortably in the thousands of buildings');

    const s = withConnectivity(mk({ buildings }));
    const index = sectionIndexOf(s);
    let maxNurseryInSection = 0;
    let maxHospitalInSection = 0;
    for (const audit of index.values()) {
      maxNurseryInSection = Math.max(maxNurseryInSection, audit.countBySpec['edu_nursery'] ?? 0);
      maxHospitalInSection = Math.max(maxHospitalInSection, audit.countBySpec['hea_hospital'] ?? 0);
    }
    assert.equal(maxNurseryInSection, EDU_COUNT / EDU_SECTIONS);
    assert.equal(maxHospitalInSection, HOSP_COUNT / HOSP_SECTIONS);
    assert.ok(maxNurseryInSection < NURSERY_RUNG.groupSize, 'PRE-CONDITION: per-section grouping alone must be structurally unable to ever fire');
    assert.ok(maxHospitalInSection < HOSPITAL_RUNG.groupSize, 'PRE-CONDITION: per-section grouping alone must be structurally unable to ever fire');
  });
});

// ---------------------------------------------------------------------------
// (1) the fix: findOpportunities sees city-wide opportunities despite the
//     scatter, deterministically.
// ---------------------------------------------------------------------------

describe('BUG-758 (1): findOpportunities groups the scattered civic families CITY-WIDE', () => {
  test('at least one edu_nursery->edu_nursery_city and one hea_hospital->hea_teaching opportunity is found', () => {
    const buildings = buildDogfoodCity();
    const s = withConnectivity(mk({ buildings }));
    const opps = findOpportunities(s, Array.from(sectionIndexOf(s).keys()));
    const eduOpp = opps.find((o) => o.fromSpec === 'edu_nursery' && o.toSpec === 'edu_nursery_city');
    const hospOpp = opps.find((o) => o.fromSpec === 'hea_hospital' && o.toSpec === 'hea_teaching');
    assert.ok(eduOpp, 'BUG-758: a city-wide edu_nursery -> edu_nursery_city opportunity must exist despite the per-section scatter');
    assert.ok(hospOpp, 'BUG-758: a city-wide hea_hospital -> hea_teaching opportunity must exist despite the per-section scatter');
    assert.equal(eduOpp.groupCount, NURSERY_RUNG.groupSize);
    assert.equal(hospOpp.groupCount, HOSPITAL_RUNG.groupSize);
    assert.ok(eduOpp.capacityGain >= 0, 'consolidating must never lose real capacity');
    assert.ok(hospOpp.capacityGain >= 0, 'consolidating must never lose real capacity');
  });

  test('determinism: two independent findOpportunities calls on the same state are byte-identical, in both order and content', () => {
    const buildings = buildDogfoodCity();
    const s = withConnectivity(mk({ buildings }));
    const keys = Array.from(sectionIndexOf(s).keys());
    const a = JSON.stringify(findOpportunities(s, keys));
    const b = JSON.stringify(findOpportunities(s, keys));
    assert.equal(a, b, 'GR#21: no map-range/iteration-order nondeterminism in the city-wide path');
  });

  test('multiple city-wide opportunities for the same rung pick disjoint candidate building ids (no double-booking)', () => {
    const buildings = buildDogfoodCity();
    const s = withConnectivity(mk({ buildings }));
    const opps = findOpportunities(s, Array.from(sectionIndexOf(s).keys()));
    const eduOpps = opps.filter((o) => o.fromSpec === 'edu_nursery' && o.toSpec === 'edu_nursery_city');
    assert.ok(eduOpps.length >= 2, `expected >=2 city-wide edu_nursery opportunities from ${EDU_COUNT} nurseries at groupSize ${NURSERY_RUNG.groupSize}`);
    const seen = new Set();
    for (const o of eduOpps) {
      for (const bid of o.buildingIds) {
        assert.ok(!seen.has(bid), `building #${bid} claimed by more than one city-wide opportunity`);
        seen.add(bid);
      }
    }
  });
});

// ---------------------------------------------------------------------------
// (2) end-to-end through the real reducer: convergence, conservation, undo.
// ---------------------------------------------------------------------------

describe('BUG-758 (2): end-to-end through the real reducer', () => {
  test('after enough monthly passes the scattered civic families converge to the minimum successor count; capacity and money conserve; successors sit on real free sites; Undo reverses', () => {
    const buildings = buildDogfoodCity();
    let s = withConnectivity(mk({ buildings }));
    s = reducer(s, { type: 'toggleConsolidator' });
    assert.equal(countSpec(s, 'edu_nursery'), EDU_COUNT);
    assert.equal(countSpec(s, 'hea_hospital'), HOSP_COUNT);

    const startFunds = s.funds;
    const startEduCapacity = EDU_COUNT * SPECS.edu_nursery.children;
    const startHealthCapacity = HOSP_COUNT * SPECS.hea_hospital.served;

    // Walk forward one boundary at a time until a pass's OWN transaction log
    // records a civic-family successor being built, for the Undo proof
    // below — never assume which pass it lands in. Verifying against the
    // pass's own recorded removed/added ids (not a raw building-COUNT
    // before/after diff) is deliberate: a whole game month also runs other
    // systemic mutations (road/building auto-scale, the biennial orphan-
    // connect sweep) that can change the total building count for reasons
    // entirely unrelated to this consolidator pass — Undo only reverses ITS
    // OWN pass, so a count-based comparison is the wrong tool here.
    let civicPass = null;
    let cur = s;
    for (let m = 0; m < 12 && !civicPass; m++) {
      cur = advanceToNextBoundary(cur);
      const log = cur.consolidatorLog?.[0];
      if (log && log.transactions.some((t) => t.added.some((a) => a.spec === 'edu_nursery_city' || a.spec === 'hea_teaching'))) {
        civicPass = { after: cur, log };
      }
    }
    assert.ok(civicPass, 'NON-VACUITY: at least one civic consolidation must occur within 12 monthly passes');

    const civicTxns = civicPass.log.transactions.filter((t) => t.added.some((a) => a.spec === 'edu_nursery_city' || a.spec === 'hea_teaching'));
    const addedCivicIds = new Set();
    const removedCivicRecords = [];
    for (const t of civicTxns) {
      for (const a of t.added) addedCivicIds.add(a.id);
      for (const r of t.removed) removedCivicRecords.push(r);
    }
    assert.ok(addedCivicIds.size > 0 && removedCivicRecords.length > 0, 'the civic transaction(s) must actually carry removed/added records');

    // Undo: reverses the WHOLE last pass (its own contract — AC-26), so
    // every one of ITS added ids must be gone and every one of ITS removed
    // records must be restored, civic or not.
    const undone = reducer(civicPass.after, { type: 'consolidatorUndo' });
    const undoneIds = new Set(undone.buildings.map((b) => b.id));
    for (const id of addedCivicIds) {
      assert.ok(!undoneIds.has(id), `Undo must remove the successor building #${id} the civic pass added`);
    }
    for (const r of removedCivicRecords) {
      const restored = undone.buildings.find((b) => b.id === r.id);
      assert.ok(restored, `Undo must restore removed building #${r.id} (${r.spec})`);
      assert.equal(restored.spec, r.spec, `Undo must restore #${r.id} as its original spec`);
      assert.equal(restored.x, r.x, `Undo must restore #${r.id} at its original x`);
      assert.equal(restored.y, r.y, `Undo must restore #${r.id} at its original y`);
    }
    // Every ADDED id across the WHOLE pass (not just the civic slice) must
    // be gone, and every REMOVED record across the whole pass restored —
    // the full AC-26 contract, not just the civic-relevant subset.
    const allAddedIds = new Set();
    const allRemovedRecords = [];
    for (const t of civicPass.log.transactions) {
      for (const a of t.added) allAddedIds.add(a.id);
      for (const r of t.removed) allRemovedRecords.push(r);
    }
    for (const id of allAddedIds) assert.ok(!undoneIds.has(id), `Undo must remove EVERY building #${id} the pass added, not just the civic ones`);
    for (const r of allRemovedRecords) assert.ok(undoneIds.has(r.id), `Undo must restore EVERY building #${r.id} the pass removed, not just the civic ones`);

    // Continue converging to the final, minimum-count state.
    for (let m = 0; m < 24; m++) {
      cur = advanceToNextBoundary(cur);
    }

    const finalNursery = countSpec(cur, 'edu_nursery');
    const finalCityKindergarten = countSpec(cur, 'edu_nursery_city');
    const finalHospital = countSpec(cur, 'hea_hospital');
    const finalTeaching = countSpec(cur, 'hea_teaching');
    // eslint-disable-next-line no-console
    console.log(
      `BUG-758 final: edu_nursery=${finalNursery} edu_nursery_city=${finalCityKindergarten} ` +
        `hea_hospital=${finalHospital} hea_teaching=${finalTeaching}`,
    );

    // Floor-based groupSize semantics (AC-8 rule 3, unchanged by this fix):
    // the MINIMUM number of successors this catalogue's ratios can produce
    // from this stock, with the below-groupSize remainder correctly left
    // alone (never destroyed, never force-merged into a partial group).
    assert.equal(finalCityKindergarten, EXPECTED_CITY_KINDERGARTENS, `exactly floor(${EDU_COUNT}/${NURSERY_RUNG.groupSize}) City Kindergartens — the minimum this catalogue supports`);
    assert.equal(finalNursery, EDU_COUNT - NURSERY_RUNG.groupSize * finalCityKindergarten, 'leftover nurseries below groupSize are correctly left alone, never destroyed');
    assert.equal(finalTeaching, EXPECTED_TEACHING_HOSPITALS, `exactly floor(${HOSP_COUNT}/${HOSPITAL_RUNG.groupSize}) Teaching Hospitals — the minimum this catalogue supports`);
    assert.equal(finalHospital, HOSP_COUNT - HOSPITAL_RUNG.groupSize * finalTeaching, 'leftover hospitals below groupSize are correctly left alone, never destroyed');

    // Conservation: capacity never falls, money is spent but never conjured.
    const finalEduCapacity = finalNursery * SPECS.edu_nursery.children + finalCityKindergarten * SPECS.edu_nursery_city.children;
    const finalHealthCapacity = finalHospital * SPECS.hea_hospital.served + finalTeaching * SPECS.hea_teaching.served;
    assert.ok(finalEduCapacity >= startEduCapacity, `nursery capacity must never fall: ${startEduCapacity} -> ${finalEduCapacity}`);
    assert.ok(finalHealthCapacity >= startHealthCapacity, `health capacity must never fall: ${startHealthCapacity} -> ${finalHealthCapacity}`);
    assert.ok(cur.funds >= 0, 'funds must never go negative');
    assert.ok(cur.funds <= startFunds, 'money is only ever spent by the consolidator, never created');

    // Successor placement: every successor sits on a REAL free site — no two
    // buildings in the final city overlap footprints.
    const occ = new Map();
    for (const b of cur.buildings) {
      const sp = SPECS[b.spec];
      if (!sp) continue;
      for (let dx = 0; dx < sp.w; dx++) {
        for (let dy = 0; dy < sp.h; dy++) {
          const key = `${b.x + dx},${b.y + dy}`;
          assert.ok(!occ.has(key), `tile ${key} double-occupied by #${occ.get(key)} and #${b.id} (${b.spec}) — successor NOT placed on a real free site`);
          occ.set(key, b.id);
        }
      }
    }
    const kindergartens = cur.buildings.filter((b) => b.spec === 'edu_nursery_city');
    const teachingHospitals = cur.buildings.filter((b) => b.spec === 'hea_teaching');
    assert.equal(kindergartens.length, finalCityKindergarten);
    assert.equal(teachingHospitals.length, finalTeaching);
    for (const b of [...kindergartens, ...teachingHospitals]) {
      assert.ok(sectionKeyOf(b.x, b.y) >= 0, 'successor must sit at a real, in-bounds tile');
    }
  });
});

// ---------------------------------------------------------------------------
// (2b) LEAD RULING PROOF: Aaron's EXACT numbers — 40 nurseries, groupSize
//      33 — form the one full group and leave the honest 7-nursery
//      remainder (no partial successor), now that CEIL-3 is exempted for
//      city-wide families.
// ---------------------------------------------------------------------------

const EXACT_EDU_COUNT = 40;
const EXACT_EDU_SECTIONS = 8; // 5 per section — matches the ORIGINAL bug report shape

function buildExactNurseryCity() {
  const buildings = [];
  let id = 1;
  for (let x = 0; x <= ROAD_MAX_X; x++) buildings.push({ id: id++, spec: 'road', x, y: 0, builtTick: -1000 });
  const per = EXACT_EDU_COUNT / EXACT_EDU_SECTIONS;
  for (let sx = 0; sx < EXACT_EDU_SECTIONS; sx++) {
    for (let i = 0; i < per; i++) {
      buildings.push({ id: id++, spec: 'edu_nursery', x: sx * 16 + 1 + i, y: 1, builtTick: -1000 });
    }
  }
  return buildings;
}

describe('BUG-758 (2b): LEAD RULING — CEIL-3 exempted for civic families, exact-40 nurseries', () => {
  test('40 nurseries (groupSize 33): forms exactly ONE City Kindergarten, leaves the honest 7-nursery remainder, capacity conserved', () => {
    const buildings = buildExactNurseryCity();
    let s = withConnectivity(mk({ buildings }));
    s = reducer(s, { type: 'toggleConsolidator' });
    assert.equal(countSpec(s, 'edu_nursery'), EXACT_EDU_COUNT);

    const startCapacity = EXACT_EDU_COUNT * SPECS.edu_nursery.children;
    let cur = s;
    for (let m = 0; m < 12; m++) cur = advanceToNextBoundary(cur);

    const finalNursery = countSpec(cur, 'edu_nursery');
    const finalCity = countSpec(cur, 'edu_nursery_city');
    // eslint-disable-next-line no-console
    console.log(`BUG-758 (2b) exact-40 final: edu_nursery=${finalNursery} edu_nursery_city=${finalCity}`);

    assert.equal(finalCity, 1, 'LEAD RULING: exactly ONE City Kindergarten from the one full group of 33 — CEIL-3 no longer blocks this');
    assert.equal(finalNursery, EXACT_EDU_COUNT - NURSERY_RUNG.groupSize, 'the 7-nursery remainder (below groupSize) is left alone, never force-merged into a partial successor');

    const finalCapacity = finalNursery * SPECS.edu_nursery.children + finalCity * SPECS.edu_nursery_city.children;
    assert.ok(finalCapacity >= startCapacity, `capacity must never fall: ${startCapacity} -> ${finalCapacity}`);
  });
});

// ---------------------------------------------------------------------------
// (2c) ROUND F1 (opus-round-bug758, 2026-09-05, P1): findCityWideOpportunities
//      must sort candidates by (capacity ascending, id ascending), not id
//      alone — a plain id sort can slice an auto-scaled member into the
//      chosen group purely by luck of its id, pushing the group's REAL
//      combined capacity over the successor's and making BUG-736's
//      capacity-loss gate refuse the group FOREVER, deterministically, even
//      though enough cheaper members exist to form a valid group instead.
// ---------------------------------------------------------------------------

describe('BUG-758 (2c): ROUND F1 — one auto-scaled nursery among 40 does not block the group', () => {
  test('40 nurseries, ONE at tier 1 (48 places) with the LOWEST id: one City Kindergarten still forms from the 33 SMALLEST, and the scaled one stays put', () => {
    const buildings = buildExactNurseryCity();
    // The FIRST nursery built (lowest id, section sx=0) is scaled to tier 1
    // (48 places) — under a plain id-ascending sort this would be the very
    // first of the "lowest-33-ids" slice, pushing that group's real capacity
    // to 32*30 + 48 = 1,008 > edu_nursery_city's 1,000, tripping BUG-736's
    // capacity-loss gate every single pass. The road tile is id 1 (see
    // ROAD_MAX_X's loop, always first) — the first edu_nursery pushed is id
    // ROAD_MAX_X+2 (1-indexed ids start at 1 for the road loop).
    const firstNurseryId = ROAD_MAX_X + 2;
    const scaled = buildings.find((b) => b.id === firstNurseryId);
    assert.equal(scaled?.spec, 'edu_nursery', 'RED-PROOF setup: expected id must actually be the first edu_nursery in the fixture');
    scaled.capacityTier = 1;

    let s = withConnectivity(mk({ buildings }));
    s = reducer(s, { type: 'toggleConsolidator' });
    assert.equal(countSpec(s, 'edu_nursery'), EXACT_EDU_COUNT);

    const startCapacity = (EXACT_EDU_COUNT - 1) * SPECS.edu_nursery.children + 1 * SPECS.edu_nursery.capacityTiers[1];
    let cur = s;
    for (let m = 0; m < 12; m++) cur = advanceToNextBoundary(cur);

    const finalNursery = countSpec(cur, 'edu_nursery');
    const finalCity = countSpec(cur, 'edu_nursery_city');
    // eslint-disable-next-line no-console
    console.log(`BUG-758 (2c) scaled-nursery final: edu_nursery=${finalNursery} edu_nursery_city=${finalCity}`);

    assert.equal(finalCity, 1, 'F1: one City Kindergarten must still form from the 33 SMALLEST-capacity nurseries, skipping the scaled one');
    assert.equal(finalNursery, EXACT_EDU_COUNT - NURSERY_RUNG.groupSize, 'exactly 7 nurseries remain (the tier-0 remainder PLUS the untouched scaled one, whichever the smallest-first sort left out)');

    // The scaled nursery itself (its ORIGINAL id) must still exist, untouched
    // — smallest-capacity-first means it is the LAST candidate ever
    // consumed, and with only one group formed here it is never reached.
    const stillThere = cur.buildings.find((b) => b.id === firstNurseryId);
    assert.ok(stillThere, 'F1: the scaled nursery must still exist — it must never be force-included in a group that then gets refused forever');
    assert.equal(stillThere.spec, 'edu_nursery');
    assert.equal(stillThere.capacityTier, 1, 'F1: the scaled nursery keeps its own tier — it was never touched');

    const finalCapacity =
      cur.buildings
        .filter((b) => b.spec === 'edu_nursery')
        .reduce((sum, b) => sum + SPECS.edu_nursery.capacityTiers[b.capacityTier ?? 0], 0) +
      finalCity * SPECS.edu_nursery_city.children;
    assert.ok(finalCapacity >= startCapacity, `capacity must never fall: ${startCapacity} -> ${finalCapacity}`);
  });

  test('RED-PROOF: a plain id-ascending sort (reverting F1) leaves the scaled group permanently refused — 0 City Kindergartens forever', () => {
    const out = runWithMutant({
      targetRelPath: 'sim/consolidator.ts',
      mutate: (original) => {
        const marker = 'ids.sort((a, b) => {\n      const ca = capacityOfId.get(a) ?? 0;\n      const cb = capacityOfId.get(b) ?? 0;\n      if (ca !== cb) return ca - cb;\n      return a - b;\n    });';
        assert.ok(original.includes(marker), 'RED-PROOF setup: expected F1 capacity-ascending sort not found — consolidator.ts shape changed');
        return original.replace(marker, 'ids.sort((a, b) => a - b); // BUG-758 F1 RED-PROOF mutant: reverts to plain id-ascending sort');
      },
      childBody: `
import { reducer, initialState, TICKS_PER_MONTH, CONSOLIDATOR_UNLOCK_LEVEL, xpForLevel, levelOf } from './sim/engine.ts';
import { computeRoadConnectivity, SPECS } from './sim/data.ts';

function mk(over) {
  const base = initialState();
  return {
    ...base, unlockedAll: true, roadMonitors: [], buildingMonitors: [], buildings: [],
    population: 0, funds: 50_000_000_000, tick: 0, consolidatorEnabled: false,
    consolidatorLog: [], xp: xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL),
    lastRewardedLevel: levelOf(xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL)),
    consolidatorMode: 'monthly-twelfth', ...over,
  };
}
const withConnectivity = (s) => ({ ...s, roadConnectivity: computeRoadConnectivity(s) });
function advanceToNextBoundary(s) {
  let cur = s;
  do { cur = reducer(cur, { type: 'tick' }); } while (cur.tick % TICKS_PER_MONTH !== 0);
  return cur;
}
const countSpec = (s, id) => s.buildings.filter((b) => b.spec === id).length;

const buildings = [];
let id = 1;
for (let x = 0; x <= ${ROAD_MAX_X}; x++) buildings.push({ id: id++, spec: 'road', x, y: 0, builtTick: -1000 });
for (let sx = 0; sx < ${EXACT_EDU_SECTIONS}; sx++) for (let i = 0; i < ${EXACT_EDU_COUNT / EXACT_EDU_SECTIONS}; i++) buildings.push({ id: id++, spec: 'edu_nursery', x: sx * 16 + 1 + i, y: 1, builtTick: -1000 });
buildings.find((b) => b.spec === 'edu_nursery').capacityTier = 1;

let s = withConnectivity(mk({ buildings }));
s = reducer(s, { type: 'toggleConsolidator' });
for (let m = 0; m < 12; m++) s = advanceToNextBoundary(s);

console.log(JSON.stringify({
  nursery: countSpec(s, 'edu_nursery'),
  cityKindergarten: countSpec(s, 'edu_nursery_city'),
}));
`,
    });
    const lastLine = out.trim().split('\n').pop();
    const result = JSON.parse(lastLine);
    // eslint-disable-next-line no-console
    console.log('[RED-PROOF F1] plain id-ascending sort result:', result);
    assert.equal(result.cityKindergarten, 0, 'RED-PROOF FAILED: a plain id sort should permanently refuse the group once a scaled member is sliced in');
    assert.equal(result.nursery, EXACT_EDU_COUNT, 'RED-PROOF: all 40 nurseries remain forever under a plain id-ascending sort');
  });
});

// ---------------------------------------------------------------------------
// (3) RED-PROOF: reverting city-wide grouping (isCityWideFamily -> false)
//     reproduces the exact reported bug — zero opportunities from the same
//     fixture that (2) proves converges under the real fix.
// ---------------------------------------------------------------------------

describe('BUG-758 (3): RED-PROOF — restoring per-section-only grouping reproduces the bug', () => {
  test('mutant (isCityWideFamily forced false) sees ZERO edu_nursery_city / hea_teaching opportunities on the identical dogfood fixture', () => {
    const out = runWithMutant({
      targetRelPath: 'sim/consolidator.ts',
      mutate: (original) => {
        const marker = 'export function isCityWideFamily(sp: Spec): boolean {\n  return CITY_WIDE_CONSOLIDATION_KINDS.has(sp.kind);\n}';
        assert.ok(original.includes(marker), 'RED-PROOF setup: expected isCityWideFamily body not found — consolidator.ts shape changed');
        return original.replace(marker, 'export function isCityWideFamily(sp: Spec): boolean {\n  return false; // BUG-758 RED-PROOF mutant: restores per-section-only grouping\n}');
      },
      childBody: `
import { reducer, initialState, TICKS_PER_MONTH, CONSOLIDATOR_UNLOCK_LEVEL, xpForLevel, levelOf } from './sim/engine.ts';
import { computeRoadConnectivity, SPECS } from './sim/data.ts';

function mk(over) {
  const base = initialState();
  return {
    ...base, unlockedAll: true, roadMonitors: [], buildingMonitors: [], buildings: [],
    population: 0, funds: 50_000_000_000, tick: 0, consolidatorEnabled: false,
    consolidatorLog: [], xp: xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL),
    lastRewardedLevel: levelOf(xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL)),
    consolidatorMode: 'monthly-twelfth', ...over,
  };
}
const withConnectivity = (s) => ({ ...s, roadConnectivity: computeRoadConnectivity(s) });
function advanceToNextBoundary(s) {
  let cur = s;
  do { cur = reducer(cur, { type: 'tick' }); } while (cur.tick % TICKS_PER_MONTH !== 0);
  return cur;
}
const countSpec = (s, id) => s.buildings.filter((b) => b.spec === id).length;

const buildings = [];
let id = 1;
for (let x = 0; x <= ${ROAD_MAX_X}; x++) buildings.push({ id: id++, spec: 'road', x, y: 0, builtTick: -1000 });
for (let sx = 0; sx < ${EDU_SECTIONS}; sx++) for (let i = 0; i < ${EDU_COUNT / EDU_SECTIONS}; i++) buildings.push({ id: id++, spec: 'edu_nursery', x: sx * 16 + 1 + i, y: 1, builtTick: -1000 });
for (let sx = 10; sx < 10 + ${HOSP_SECTIONS}; sx++) for (let i = 0; i < ${HOSP_COUNT / HOSP_SECTIONS}; i++) buildings.push({ id: id++, spec: 'hea_hospital', x: sx * 16 + 1 + i * 3, y: 4, builtTick: -1000 });
for (let r = 0; r < ${FILLER_ROAD_ROWS}; r++) for (let x = 0; x <= ${ROAD_MAX_X}; x++) buildings.push({ id: id++, spec: 'road', x, y: 20 + r, builtTick: -1000 });

let s = withConnectivity(mk({ buildings }));
s = reducer(s, { type: 'toggleConsolidator' });
for (let m = 0; m < 12; m++) s = advanceToNextBoundary(s);

console.log(JSON.stringify({
  nursery: countSpec(s, 'edu_nursery'),
  cityKindergarten: countSpec(s, 'edu_nursery_city'),
  hospital: countSpec(s, 'hea_hospital'),
  teaching: countSpec(s, 'hea_teaching'),
}));
`,
    });
    const lastLine = out.trim().split('\n').pop();
    const result = JSON.parse(lastLine);
    // eslint-disable-next-line no-console
    console.log('[RED-PROOF] mutant (per-section-only) result:', result);
    assert.equal(result.cityKindergarten, 0, 'RED-PROOF FAILED: per-section-only grouping should NEVER produce a City Kindergarten from this scattered fixture');
    assert.equal(result.teaching, 0, 'RED-PROOF FAILED: per-section-only grouping should NEVER produce a Teaching Hospital from this scattered fixture');
    assert.equal(result.nursery, EDU_COUNT, `RED-PROOF: all ${EDU_COUNT} kindergartens remain forever under per-section-only grouping`);
    assert.equal(result.hospital, HOSP_COUNT, `RED-PROOF: all ${HOSP_COUNT} hospitals remain forever under per-section-only grouping`);
  });
});

// ---------------------------------------------------------------------------
// (3b) RED-PROOF: restoring the CEIL-3 family-share ceiling for civic
//      families reproduces the LEAD RULING's exact complaint on the exact-40
//      fixture — the one full group is blocked and edu_nursery_city stays 0.
// ---------------------------------------------------------------------------

describe('BUG-758 (3b): RED-PROOF — restoring the family-share ceiling for civic families reds the exact-40 case', () => {
  test('mutant (CEIL-3 NOT exempted for isCityWideFamily) blocks the exact-40 group forever', () => {
    const out = runWithMutant({
      targetRelPath: 'sim/engine.ts',
      mutate: (original) => {
        const marker = 'if (!isCityWideFamily(toSpec) && successorCapacity > CONSOLIDATOR_MAX_FAMILY_SHARE * familyTotalAfter) {';
        assert.ok(original.includes(marker), 'RED-PROOF setup: expected CEIL-3 exemption condition not found — engine.ts shape changed');
        return original.replace(marker, 'if (successorCapacity > CONSOLIDATOR_MAX_FAMILY_SHARE * familyTotalAfter) { // BUG-758 RED-PROOF mutant: restores the ceiling for civic families');
      },
      childBody: `
import { reducer, initialState, TICKS_PER_MONTH, CONSOLIDATOR_UNLOCK_LEVEL, xpForLevel, levelOf } from './sim/engine.ts';
import { computeRoadConnectivity, SPECS } from './sim/data.ts';

function mk(over) {
  const base = initialState();
  return {
    ...base, unlockedAll: true, roadMonitors: [], buildingMonitors: [], buildings: [],
    population: 0, funds: 50_000_000_000, tick: 0, consolidatorEnabled: false,
    consolidatorLog: [], xp: xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL),
    lastRewardedLevel: levelOf(xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL)),
    consolidatorMode: 'monthly-twelfth', ...over,
  };
}
const withConnectivity = (s) => ({ ...s, roadConnectivity: computeRoadConnectivity(s) });
function advanceToNextBoundary(s) {
  let cur = s;
  do { cur = reducer(cur, { type: 'tick' }); } while (cur.tick % TICKS_PER_MONTH !== 0);
  return cur;
}
const countSpec = (s, id) => s.buildings.filter((b) => b.spec === id).length;

const buildings = [];
let id = 1;
for (let x = 0; x <= ${ROAD_MAX_X}; x++) buildings.push({ id: id++, spec: 'road', x, y: 0, builtTick: -1000 });
for (let sx = 0; sx < ${EXACT_EDU_SECTIONS}; sx++) for (let i = 0; i < ${EXACT_EDU_COUNT / EXACT_EDU_SECTIONS}; i++) buildings.push({ id: id++, spec: 'edu_nursery', x: sx * 16 + 1 + i, y: 1, builtTick: -1000 });

let s = withConnectivity(mk({ buildings }));
s = reducer(s, { type: 'toggleConsolidator' });
for (let m = 0; m < 12; m++) s = advanceToNextBoundary(s);

console.log(JSON.stringify({
  nursery: countSpec(s, 'edu_nursery'),
  cityKindergarten: countSpec(s, 'edu_nursery_city'),
}));
`,
    });
    const lastLine = out.trim().split('\n').pop();
    const result = JSON.parse(lastLine);
    // eslint-disable-next-line no-console
    console.log('[RED-PROOF 3b] mutant (ceiling restored for civic) result:', result);
    assert.equal(result.cityKindergarten, 0, 'RED-PROOF FAILED: restoring the ceiling for civic families should block the exact-40 group exactly like the lead ruling complained');
    assert.equal(result.nursery, EXACT_EDU_COUNT, 'RED-PROOF: all 40 nurseries remain forever if the ceiling is not exempted for civic families');
  });
});

// ---------------------------------------------------------------------------
// (4) ROUND F3 (opus-round-bug758, 2026-09-05, P2): findSuccessorSite's
//     radius>=1 branch had ZERO coverage — setting `maxRadius = 0` (i.e.
//     disabling the whole ring-search fallback and leaving only the
//     radius-0 single-section scan) reproduced NOTHING red. This fixture
//     packs the anchor section (0,0) completely full except 33 ISOLATED
//     (non-adjacent, checkerboard) edu_nursery cells, so after the group is
//     demolished the freed tiles are 33 scattered single-tile gaps — no
//     contiguous 3x3 block exists anywhere in section (0,0) for
//     edu_nursery_city to land on. The successor can therefore ONLY be
//     placed by ringing outward into an adjacent, empty section.
// ---------------------------------------------------------------------------

const PACKED_SECTION_ROAD_MAX_X = 31; // 2 sections wide (sx 0 and 1)

/**
 * 33 edu_nursery on a checkerboard ((x+y) even) inside section (0,0)'s rows
 * y=1..15 (y=0 reserved for the shared road row), every OTHER cell in that
 * same range filled with road — so no two freed (post-demolish) tiles are
 * ever adjacent, which rules out even a 2x1 gap, let alone the 3x3
 * edu_nursery_city needs. Section (1,0) (x=16..31) is left otherwise empty.
 */
function buildPackedAnchorFixture() {
  const buildings = [];
  let id = 1;
  for (let x = 0; x <= PACKED_SECTION_ROAD_MAX_X; x++) {
    buildings.push({ id: id++, spec: 'road', x, y: 0, builtTick: -1000 });
  }
  const nurseryCells = [];
  for (let y = 1; y <= 15 && nurseryCells.length < 33; y++) {
    for (let x = 0; x <= 15 && nurseryCells.length < 33; x++) {
      if ((x + y) % 2 === 0) nurseryCells.push({ x, y });
    }
  }
  assert.equal(nurseryCells.length, 33, 'fixture setup: must find exactly 33 checkerboard cells');
  const nurserySet = new Set(nurseryCells.map((c) => `${c.x},${c.y}`));
  for (const c of nurseryCells) {
    buildings.push({ id: id++, spec: 'edu_nursery', x: c.x, y: c.y, builtTick: -1000 });
  }
  for (let y = 1; y <= 15; y++) {
    for (let x = 0; x <= 15; x++) {
      if (nurserySet.has(`${x},${y}`)) continue;
      buildings.push({ id: id++, spec: 'road', x, y, builtTick: -1000 });
    }
  }
  return buildings;
}

describe('BUG-758 (4): ROUND F3 — findSuccessorSite rings outward when the anchor section is packed full', () => {
  test('anchor section (0,0) has no free 3x3 anywhere: the successor lands in the ADJACENT section, all 33 members demolished, no strand', () => {
    const buildings = buildPackedAnchorFixture();
    // Sanity: no contiguous 3x3 free block exists in section (0,0) even
    // BEFORE any demolition (the 33 nursery cells themselves are the only
    // gaps in the road fill, and they are pairwise non-adjacent).
    const s0 = withConnectivity(mk({ buildings }));
    const occ0 = new Set(s0.buildings.map((b) => `${b.x},${b.y}`));
    let anyFree3x3 = false;
    for (let y = 0; y <= 13 && !anyFree3x3; y++) {
      for (let x = 0; x <= 13 && !anyFree3x3; x++) {
        let free = true;
        for (let dy = 0; dy < 3 && free; dy++) for (let dx = 0; dx < 3 && free; dx++) if (occ0.has(`${x + dx},${y + dy}`)) free = false;
        if (free) anyFree3x3 = true;
      }
    }
    assert.ok(!anyFree3x3, 'fixture setup: section (0,0) must have NO free 3x3 block anywhere, pre-demolition');

    let s = reducer(s0, { type: 'toggleConsolidator' });
    assert.equal(countSpec(s, 'edu_nursery'), 33);
    let cur = s;
    let civicPass = null;
    for (let m = 0; m < 6 && !civicPass; m++) {
      cur = advanceToNextBoundary(cur);
      const log = cur.consolidatorLog?.[0];
      if (log && log.transactions.some((t) => t.added.some((a) => a.spec === 'edu_nursery_city'))) civicPass = log;
    }
    assert.ok(civicPass, 'NON-VACUITY: the group must actually consolidate despite the packed anchor section');

    const skipReasons = (civicPass.skipped ?? []).map((x) => x.reason);
    assert.ok(!skipReasons.includes('no site'), `must not skip for 'no site' — ring search must have found the adjacent section instead (skipped: ${JSON.stringify(skipReasons)})`);
    assert.ok(!skipReasons.includes('would strand'), `must not skip for 'would strand' (skipped: ${JSON.stringify(skipReasons)})`);

    assert.equal(countSpec(cur, 'edu_nursery'), 0, 'all 33 members must be demolished — a single exact-groupSize group, no remainder');
    const kindergarten = cur.buildings.find((b) => b.spec === 'edu_nursery_city');
    assert.ok(kindergarten, 'the successor must have been placed');
    const anchorSx = Math.floor(0 / 16);
    const successorSx = Math.floor(kindergarten.x / 16);
    assert.notEqual(successorSx, anchorSx, `F3: the successor must land OUTSIDE the packed anchor section (0,0) — got sx=${successorSx} at (${kindergarten.x},${kindergarten.y})`);
    assert.equal(successorSx, 1, 'F3: expected the successor to ring out into the immediately adjacent section (sx=1)');

    // Capacity conserved.
    const finalCapacity = countSpec(cur, 'edu_nursery') * SPECS.edu_nursery.children + countSpec(cur, 'edu_nursery_city') * SPECS.edu_nursery_city.children;
    assert.ok(finalCapacity >= 33 * SPECS.edu_nursery.children, 'capacity must never fall');
  });
});

describe('BUG-758 (4b): RED-PROOF — disabling the ring-search fallback (maxRadius=0) reds the packed-anchor fixture', () => {
  test('mutant (findSuccessorSite maxRadius forced to 0) leaves the group permanently unplaced on the packed-anchor fixture', () => {
    const out = runWithMutant({
      targetRelPath: 'sim/engine.ts',
      mutate: (original) => {
        const marker = 'const maxRadius = Math.max(SECTIONS_X, SECTIONS_Y);';
        assert.ok(original.includes(marker), 'RED-PROOF setup: expected findSuccessorSite maxRadius line not found — engine.ts shape changed');
        return original.replace(marker, 'const maxRadius = 0; // BUG-758 F3 RED-PROOF mutant: disables the ring-search fallback entirely');
      },
      childBody: `
import { reducer, initialState, TICKS_PER_MONTH, CONSOLIDATOR_UNLOCK_LEVEL, xpForLevel, levelOf } from './sim/engine.ts';
import { computeRoadConnectivity, SPECS } from './sim/data.ts';

function mk(over) {
  const base = initialState();
  return {
    ...base, unlockedAll: true, roadMonitors: [], buildingMonitors: [], buildings: [],
    population: 0, funds: 50_000_000_000, tick: 0, consolidatorEnabled: false,
    consolidatorLog: [], xp: xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL),
    lastRewardedLevel: levelOf(xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL)),
    consolidatorMode: 'monthly-twelfth', ...over,
  };
}
const withConnectivity = (s) => ({ ...s, roadConnectivity: computeRoadConnectivity(s) });
function advanceToNextBoundary(s) {
  let cur = s;
  do { cur = reducer(cur, { type: 'tick' }); } while (cur.tick % TICKS_PER_MONTH !== 0);
  return cur;
}
const countSpec = (s, id) => s.buildings.filter((b) => b.spec === id).length;

const buildings = [];
let id = 1;
for (let x = 0; x <= ${PACKED_SECTION_ROAD_MAX_X}; x++) buildings.push({ id: id++, spec: 'road', x, y: 0, builtTick: -1000 });
const nurseryCells = [];
for (let y = 1; y <= 15 && nurseryCells.length < 33; y++) {
  for (let x = 0; x <= 15 && nurseryCells.length < 33; x++) {
    if ((x + y) % 2 === 0) nurseryCells.push({ x, y });
  }
}
const nurserySet = new Set(nurseryCells.map((c) => \`\${c.x},\${c.y}\`));
for (const c of nurseryCells) buildings.push({ id: id++, spec: 'edu_nursery', x: c.x, y: c.y, builtTick: -1000 });
for (let y = 1; y <= 15; y++) {
  for (let x = 0; x <= 15; x++) {
    if (nurserySet.has(\`\${x},\${y}\`)) continue;
    buildings.push({ id: id++, spec: 'road', x, y, builtTick: -1000 });
  }
}

let s = withConnectivity(mk({ buildings }));
s = reducer(s, { type: 'toggleConsolidator' });
for (let m = 0; m < 6; m++) s = advanceToNextBoundary(s);

console.log(JSON.stringify({
  nursery: countSpec(s, 'edu_nursery'),
  cityKindergarten: countSpec(s, 'edu_nursery_city'),
}));
`,
    });
    const lastLine = out.trim().split('\n').pop();
    const result = JSON.parse(lastLine);
    // eslint-disable-next-line no-console
    console.log('[RED-PROOF F3] maxRadius=0 result:', result);
    assert.equal(result.cityKindergarten, 0, 'RED-PROOF FAILED: with the ring-search fallback disabled, the packed-anchor group must NEVER find a site');
    assert.equal(result.nursery, 33, 'RED-PROOF: all 33 nurseries remain forever with the ring-search fallback disabled');
  });
});
