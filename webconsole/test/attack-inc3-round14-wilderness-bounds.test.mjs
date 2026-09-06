// attack-inc3-round14-wilderness-bounds.test.mjs — FEAT-2326609779
// (consolidator inc3, LAYOUT HIERARCHY), ROUND 13 REJECT (opus-round13-inc3,
// dated 2026-09-05), P1: "237 of 744 tier tiles sit at x >= 160 (up to
// x = 435) on a 160-wide fixture — paid infrastructure with permanent
// upkeep laid OUTSIDE THE MAP". The dogfood fixture's own built footprint is
// 160x96, but the game's real map (MAP_W=440/MAP_H=260) is far bigger, and
// the whole-map month (`monthlyScopeOf`'s `full` twelfth) offered every
// section in that whole grid to the layout stage — including sections
// containing no city at all. The fix (engine.ts's `applyConsolidatorPass`)
// computes the city's own occupied bounding box once per pass and drops any
// section outside it (expanded by LAYOUT_WILDERNESS_MARGIN_TILES).
//
// This is the round's own requested permanent regression test: every
// rail/motorway/dual/aroad/minor tile the layout stage ever places must (a)
// lie within the real map bounds (trivially true — sections are clipped)
// and (b) lie within LAYOUT_WILDERNESS_MARGIN_TILES tiles of the city's OWN
// pre-layout built footprint — never a leapfrog into untouched wilderness.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { computeRoadConnectivity, MAP_W, MAP_H } from '../src/sim/data.ts';
import { initialState, reducer, CONSOLIDATOR_UNLOCK_LEVEL, xpForLevel, levelOf } from '../src/sim/engine.ts';
import { TIER_SPEC_ID, LAYOUT_WILDERNESS_MARGIN_TILES } from '../src/sim/consolidatorLayout.ts';

function mk(over) {
  const base = initialState();
  return {
    ...base,
    unlockedAll: true,
    roadMonitors: [],
    buildingMonitors: [],
    buildings: [],
    population: 0,
    funds: 1_000_000_000,
    tick: 0,
    consolidatorEnabled: false,
    consolidatorLayoutEnabled: true,
    consolidatorLog: [],
    consolidatorMode: 'monthly-twelfth',
    xp: xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL),
    lastRewardedLevel: levelOf(xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL)),
    ...over,
  };
}

/** Same attacker-measured dogfood shape as attack-inc3-round11-dogfood.test.mjs (160x96 footprint, real MAP_W/MAP_H far bigger). */
function dogfoodFixture(over) {
  const W = 160;
  const H = 96;
  let id = 1;
  const buildings = [];
  for (let y = 0; y < H; y += 8) {
    for (let x = 0; x < W; x++) buildings.push({ id: id++, spec: 'road', x, y, builtTick: -1000 });
  }
  for (let x = 0; x < W; x += 8) {
    for (let y = 0; y < H; y++) buildings.push({ id: id++, spec: 'road', x, y, builtTick: -1000 });
  }
  let placed = 0;
  for (let by = 4; by < H && placed < 340; by += 8) {
    for (let bx = 4; bx < W && placed < 340; bx += 8) {
      const spec = placed % 2 === 0 ? 'res_terrace' : 'com_shop';
      buildings.push({ id: id++, spec, x: bx, y: by, builtTick: -1000 });
      placed++;
    }
  }
  for (let i = 0; i < 12; i++) {
    buildings.push({ id: id++, spec: 'hea_hospital', x: 4 + (i * 13) % (W - 8), y: 2, builtTick: -1000 });
  }
  for (let i = 0; i < 40; i++) {
    buildings.push({ id: id++, spec: 'edu_nursery', x: 4 + (i * 4) % (W - 8), y: H - 6, builtTick: -1000 });
  }
  const s = mk({ buildings, population: 200_000, ...over });
  return { ...s, roadConnectivity: computeRoadConnectivity(s), __seedFootprint: { minX: 0, minY: 0, maxX: W - 1, maxY: H - 1 } };
}

const TIER_SPECS = new Set(Object.values(TIER_SPEC_ID));

describe('R14 (round-13 P1): every layout-placed tile stays in the real map AND near the city, never in the wilderness', () => {
  test('over 900 ticks of the whole-map month, no rail/motorway/dual/aroad/minor tile is out of map bounds or past the wilderness margin from the city\'s own footprint', () => {
    const seed = dogfoodFixture({});
    const { minX, minY, maxX, maxY } = seed.__seedFootprint;
    let s = reducer(seed, { type: 'toggleConsolidator' });
    let violations = [];
    for (let i = 0; i < 900; i++) {
      s = reducer(s, { type: 'tick' });
    }
    for (const b of s.buildings) {
      if (!TIER_SPECS.has(b.spec) || (b.builtTick ?? 0) < 0) continue; // genesis/pre-existing tiles are not layout-placed.
      if (b.x < 0 || b.y < 0 || b.x >= MAP_W || b.y >= MAP_H) {
        violations.push({ ...b, reason: 'outside real map bounds' });
        continue;
      }
      const outsideMargin =
        b.x < minX - LAYOUT_WILDERNESS_MARGIN_TILES ||
        b.x > maxX + LAYOUT_WILDERNESS_MARGIN_TILES ||
        b.y < minY - LAYOUT_WILDERNESS_MARGIN_TILES ||
        b.y > maxY + LAYOUT_WILDERNESS_MARGIN_TILES;
      if (outsideMargin) violations.push({ ...b, reason: 'past wilderness margin from city footprint' });
    }
    // eslint-disable-next-line no-console
    console.log(
      `R14: ${violations.length} of ${s.buildings.filter((b) => TIER_SPECS.has(b.spec)).length} tier tiles violate map-bounds/wilderness-margin ` +
        `(first few: ${JSON.stringify(violations.slice(0, 5))})`,
    );
    assert.equal(
      violations.length,
      0,
      `R14 (round-13 P1): ${violations.length} layout-placed tier tiles are out of map bounds or more than ${LAYOUT_WILDERNESS_MARGIN_TILES} tiles from the city's own footprint (x:[${minX},${maxX}] y:[${minY},${maxY}]) — infrastructure must never leapfrog into empty wilderness.`,
    );
  });
});
