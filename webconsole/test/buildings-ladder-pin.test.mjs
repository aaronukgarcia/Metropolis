// buildings-ladder-pin.test.mjs — FEAT-9995385590: the consolidation ladder around the 87
// buildings that were roadmap placeholders until 2026-11-01. The ladder is DERIVED from the
// catalogue (consolidationLadder), so a catalogue edit can silently add nonsense rungs; this
// file pins (1) the exact set of rungs that touch any of the 87, (2) the farm isolation
// invariant (a farm is never a rung endpoint with a non-farm), (3) the two specs that
// deliberately consolidate with nothing. Changing the set is allowed, but only deliberately:
// edit EXPECTED_RUNGS in the same commit.
//
// RED proof (BUG-1183): drop the isStaySpec or transportModeOf segment from familyKeyOf and the stay/transport tests fail.
// RED proof: drop isFarmSpec from familyKeyOf (farm/industry rungs reappear) or remove
// harbour_fishing / pow_hvdc from CONSOLIDATION_EXEMPT_SPEC_IDS and tests 2 and 3 fail.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { consolidationLadder, familyKeyOf, isFarmSpec, isStaySpec, transportModeOf } from '../src/sim/consolidator.ts';
import { SPECS } from '../src/sim/data.ts';

const FORMER_PLACEHOLDERS = new Set([
  'rail_branch',
  'trans_parkride',
  'trans_interchange',
  'rail_freightyard',
  'ev_charging_hub',
  'com_discounter',
  'com_darkstore',
  'ind_chemworks',
  'ind_refinery',
  'ind_fulfilment',
  'ind_parcelhub',
  'farm_dairy',
  'farm_abattoir',
  'harbour_fishing',
  'mine_chalk',
  'mine_clay',
  'mine_coal',
  'lei_gym',
  'lei_sportsground',
  'lei_stables',
  'pow_hvdc',
  'pow_reprocess',
  'death_cemetery',
  'death_crematorium',
  'air_heliport',
  'air_fire_helibase',
  'air_police_helibase',
  'land_containerport',
  'land_ferryterminal',
  'land_cern',
  'land_gigafactory',
  'land_semifab',
  'tour_greatwall',
  'tour_colosseum',
  'tour_tajmahal',
  'tour_machupicchu',
  'tour_petra',
  'tour_giza',
  'tour_eiffel',
  'tour_liberty',
  'tour_grandcanyon',
  'tour_niagara',
  'tour_angkor',
  'tour_stonehenge',
  'tour_acropolis',
  'tour_redeemer',
  'tour_sagrada',
  'tour_forbidden',
  'tour_stpeters',
  'tour_alhambra',
  'tour_chichenitza',
  'tour_fuji',
  'tour_opera',
  'tour_goldengate',
  'tour_louvre',
  'tour_santorini',
  'tour_venice',
  'tour_neuschwanstein',
  'tour_burj',
  'tour_iguazu',
  'tour_banff',
  'tour_aurora',
  'tour_reef',
  'tour_yellowstone',
  'tour_serengeti',
  'tour_fushimi',
  'tour_prague',
  'tour_dubrovnik',
  'tour_cappadocia',
  'tour_moai',
  'tour_uluru',
  'tour_tablemountain',
  'tour_hallstatt',
  'tour_antelope',
  'tour_halong',
  'tour_zhangjiajie',
  'tour_matterhorn',
  'tour_towerlondon',
  'tour_versailles',
  'tour_montstmichel',
  'tour_giantscauseway',
  'tour_edinburgh',
  'stay_bb',
  'stay_hotel',
  'stay_luxury',
  'stay_resort',
  'stay_caravan',
]);

// BUG-1183: the ladder family now carries a stay-vs-leisure segment (isStaySpec) and a
// transport-mode segment (transportModeOf), so stay_* never ladders with lei_* and the
// road / tram / rail-freight / EV depots never cross modes. Rungs REMOVED by that fix
// (previously pinned here):
//   bus_depot -> rail_freightyard x7, ev_charging_hub -> rail_freightyard x18,
//   ev_charging_hub -> tram_depot x4, tram_depot -> rail_freightyard x4,
//   lei_cinema -> stay_luxury x4, lei_cinema -> stay_resort x11, lei_museum -> stay_resort x4,
//   lei_stables -> stay_luxury x7, lei_stables -> stay_resort x18, lei_theatre -> stay_resort x6,
//   stay_bb -> lei_arena x11, stay_bb -> lei_museum x4, stay_bb -> lei_themepark x26,
//   stay_caravan -> lei_arena x5, stay_caravan -> lei_themepark x11, stay_hotel -> lei_themepark x8.
const EXPECTED_RUNGS = [
  'com_darkstore -> com_hypermarket x44',
  'com_darkstore -> com_mall x12',
  'com_discounter -> com_hypermarket x26',
  'com_discounter -> com_mall x7',
  'farm_abattoir -> farm_estate x75',
  'farm_dairy -> farm_estate x125',
  'ind_cement -> ind_refinery x7',
  'ind_chemworks -> ind_estate x14',
  'ind_refinery -> ind_estate x5',
  'ind_warehouse -> ind_fulfilment x4',
  'lei_gym -> lei_leisure x4',
  'lei_stables -> lei_arena x11',
  'lei_stables -> lei_museum x4',
  'lei_stables -> lei_themepark x26',
  'mine_chalk -> mine_deep x4',
  'stay_bb -> stay_luxury x7',
  'stay_bb -> stay_resort x18',
  'stay_caravan -> stay_resort x7',
  'stay_hotel -> stay_resort x5',
  'trans_parkride -> grand_terminus x10',
  'trans_parkride -> trans_interchange x5',
];

const rungLabel = (e) => `${e.from} -> ${e.to} x${e.groupSize}`;

test('the ladder rungs touching the 87 former placeholders are exactly the pinned set', () => {
  const actual = consolidationLadder()
    .filter((e) => FORMER_PLACEHOLDERS.has(e.from) || FORMER_PLACEHOLDERS.has(e.to))
    .map(rungLabel)
    .sort();
  assert.deepEqual(actual, [...EXPECTED_RUNGS].sort());
});

test('farm isolation: no rung joins a farm spec with a non-farm spec', () => {
  for (const e of consolidationLadder()) {
    assert.equal(isFarmSpec(SPECS[e.from]), isFarmSpec(SPECS[e.to]), rungLabel(e));
  }
  // The family key itself separates them (Parcel Hub shares kind, capacity field and
  // tag with Farm Estate, so only the farm segment keeps them apart).
  assert.notEqual(familyKeyOf(SPECS.ind_parcelhub), familyKeyOf(SPECS.farm_estate));
  assert.notEqual(familyKeyOf(SPECS.farm_dairy), familyKeyOf(SPECS.ind_fulfilment));
  assert.equal(familyKeyOf(SPECS.farm_dairy), familyKeyOf(SPECS.farm_estate), 'farms still share a family with each other');
});

test('harbour_fishing, pow_hvdc and the two helibases consolidate with nothing (neither feeder nor target)', () => {
  for (const e of consolidationLadder()) {
    for (const id of ['harbour_fishing', 'pow_hvdc', 'air_fire_helibase', 'air_police_helibase']) {
      assert.notEqual(e.from, id, rungLabel(e));
      assert.notEqual(e.to, id, rungLabel(e));
    }
  }
});

test('the pre-existing ind_light / ind_warehouse / ind_logistics -> farm_estate rungs are gone', () => {
  const labels = new Set(consolidationLadder().map((e) => `${e.from}>${e.to}`));
  for (const f of ['ind_light', 'ind_warehouse', 'ind_logistics']) assert.equal(labels.has(`${f}>farm_estate`), false, f);
});

test('BUG-1183: no stay<->leisure rung exists', () => {
  for (const e of consolidationLadder()) {
    assert.equal(isStaySpec(SPECS[e.from]), isStaySpec(SPECS[e.to]), rungLabel(e));
  }
  assert.notEqual(familyKeyOf(SPECS.stay_bb), familyKeyOf(SPECS.lei_themepark));
  assert.equal(familyKeyOf(SPECS.stay_bb), familyKeyOf(SPECS.stay_resort), 'stays still share a family with each other');
});

test('BUG-1183: no cross-mode transport rung exists', () => {
  const modes = new Set(['bus_depot', 'tram_depot', 'rail_freightyard', 'ev_charging_hub']);
  for (const e of consolidationLadder()) {
    assert.equal(transportModeOf(SPECS[e.from]), transportModeOf(SPECS[e.to]), rungLabel(e));
    assert.equal(modes.has(e.from) || modes.has(e.to), false, `${rungLabel(e)}: depots have no rung left`);
  }
  const keys = [...modes].map((id) => familyKeyOf(SPECS[id]));
  assert.equal(new Set(keys).size, 4, 'four depot modes, four families');
});