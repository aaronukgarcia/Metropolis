// buildings-tranche1-tourism.test.mjs — FEAT-9995385590 (AARON BALANCE PASS):
// the 50 tourism landmark placeholders (tour_*) are now REAL placeable specs.
// Names and footprints are pinned to the original placeholder lines; stats are
// placeholder-balance numbers scaled from the existing real landmarks.
//
// Run: node tools/test/scoped.mjs webconsole/test/buildings-tranche1-tourism.test.mjs
// (node's type-stripping imports the real data.ts and engine.ts.)

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SPECS, isPlaceable, canEnterSim } from '../src/sim/data.ts';
import { initialState, xpForLevel } from '../src/sim/engine.ts';

// [id, name, w, h] — copied from the placeholder lines this tranche converted.
const TOURISM_TRANCHE = [
  ['tour_greatwall', 'Great Wall', 5, 5],
  ['tour_colosseum', 'Colosseum', 3, 3],
  ['tour_tajmahal', 'Taj Mahal', 3, 3],
  ['tour_machupicchu', 'Machu Picchu', 4, 4],
  ['tour_petra', 'Petra', 4, 4],
  ['tour_giza', 'Great Pyramid', 4, 4],
  ['tour_eiffel', 'Eiffel Tower', 2, 2],
  ['tour_liberty', 'Statue of Liberty', 2, 2],
  ['tour_grandcanyon', 'Grand Canyon rim', 5, 5],
  ['tour_niagara', 'Niagara Falls', 4, 4],
  ['tour_angkor', 'Angkor Wat', 5, 5],
  ['tour_stonehenge', 'Stonehenge', 2, 2],
  ['tour_acropolis', 'Acropolis', 3, 3],
  ['tour_redeemer', 'Christ the Redeemer', 2, 2],
  ['tour_sagrada', 'Sagrada Família', 3, 3],
  ['tour_forbidden', 'Forbidden City', 5, 5],
  ['tour_stpeters', "St Peter's", 3, 3],
  ['tour_alhambra', 'Alhambra', 3, 3],
  ['tour_chichenitza', 'Chichén Itzá', 4, 4],
  ['tour_fuji', 'Mount Fuji station', 4, 4],
  ['tour_opera', 'Sydney Opera House', 3, 3],
  ['tour_goldengate', 'Golden Gate', 4, 4],
  ['tour_louvre', 'Louvre', 4, 4],
  ['tour_santorini', 'Caldera town', 3, 3],
  ['tour_venice', 'Canal quarter', 4, 4],
  ['tour_neuschwanstein', 'Neuschwanstein', 2, 2],
  ['tour_burj', 'Supertall observatory', 2, 2],
  ['tour_iguazu', 'Iguazú Falls', 4, 4],
  ['tour_banff', 'Alpine lake park', 5, 5],
  ['tour_aurora', 'Aurora lodge', 2, 2],
  ['tour_reef', 'Reef visitor centre', 3, 3],
  ['tour_yellowstone', 'Geyser park', 5, 5],
  ['tour_serengeti', 'Safari lodge', 4, 4],
  ['tour_fushimi', 'Fushimi Inari', 2, 2],
  ['tour_prague', 'Castle quarter', 3, 3],
  ['tour_dubrovnik', 'Walled city', 3, 3],
  ['tour_cappadocia', 'Cappadocia', 4, 4],
  ['tour_moai', 'Moai field', 3, 3],
  ['tour_uluru', 'Uluru', 4, 4],
  ['tour_tablemountain', 'Table Mountain', 4, 4],
  ['tour_hallstatt', 'Lakeside village', 2, 2],
  ['tour_antelope', 'Slot canyon', 2, 2],
  ['tour_halong', 'Ha Long Bay', 5, 5],
  ['tour_zhangjiajie', 'Pillar peaks', 4, 4],
  ['tour_matterhorn', 'Matterhorn', 4, 4],
  ['tour_towerlondon', 'Tower of London', 2, 2],
  ['tour_versailles', 'Versailles', 4, 4],
  ['tour_montstmichel', 'Mont-Saint-Michel', 2, 2],
  ['tour_giantscauseway', "Giant's Causeway", 2, 2],
  ['tour_edinburgh', 'Edinburgh Castle', 2, 2],
];

// Existing real landmark bounds (land_* with tourism): stats must stay inside them.
const REAL_LANDMARK_MIN_COST = 43200000;
const REAL_LANDMARK_MAX_COST = 1080000000;
const REAL_LANDMARK_MAX_TOURISM = 200;

const highLevel = { ...initialState(), xp: xpForLevel(30) };

test('tranche 1 pins exactly 50 tourism ids', () => {
  assert.equal(TOURISM_TRANCHE.length, 50);
  assert.equal(new Set(TOURISM_TRANCHE.map((t) => t[0])).size, 50);
});

for (const [id, name, w, h] of TOURISM_TRANCHE) {
  test(`${id}: real placeable tourism landmark`, () => {
    const sp = SPECS[id];
    assert.ok(sp, 'spec exists');
    assert.equal(sp.placeholder, undefined, 'no placeholder flag');
    assert.ok(canEnterSim(sp), 'may enter the sim');
    assert.equal(sp.kind, 'landmark');
    assert.equal(sp.name, name, 'name unchanged');
    assert.equal(sp.w, w, 'width unchanged');
    assert.equal(sp.h, h, 'height unchanged');
    assert.ok(isPlaceable(highLevel, sp), 'placeable at a high level');
    assert.ok(sp.tourism > 0, 'tourism > 0');
    assert.ok(sp.tourism <= REAL_LANDMARK_MAX_TOURISM, 'tourism within real landmark range');
    assert.equal(sp.maxPerCity, 1, 'unique building');
    assert.ok(sp.cost > 0, 'cost > 0');
    assert.ok(sp.cost >= REAL_LANDMARK_MIN_COST && sp.cost <= REAL_LANDMARK_MAX_COST, 'cost within real landmark range');
    assert.ok(sp.upkeep > 0, 'upkeep > 0');
  });
}
