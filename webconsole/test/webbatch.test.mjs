// webbatch.test.mjs — FEAT-1972079882 / -883 / -884.
//
// Run with `npm test` (node --test). Node's type-stripping imports the real
// TypeScript engine/data directly, so these exercise the exact shipped logic.
//
// Each assertion can go RED (proven by scratch-mutating the engine, e.g. making
// placementCost return sp.cost for zones, or dropping the lastRewardedLevel
// guard in grantLevelRewards — see the lane report).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { reducer, initialState, levelOf, xpForLevel, LEVEL_REWARD_RATE } from '../src/sim/engine.ts';
import { placementCost, isFreeZone, densityTier, blockOccupancy, unlockedAtLevel, SPECS } from '../src/sim/data.ts';

// A tile far from the starter city (roads at y56/58/…, rail y84) — empty for a 1×1.
const EMPTY = { x: 5, y: 5 };

// ---------- FEAT-1972079882: zoning is free ----------

test('placementCost: zone category costs £0, non-zone keeps catalogue cost', () => {
  assert.equal(placementCost(SPECS.res_hut), 0); // residential zone
  assert.equal(placementCost(SPECS.com_shop), 0); // commercial zone
  assert.equal(placementCost(SPECS.park), 0); // park zone
  assert.equal(placementCost(SPECS.off_suite), 0); // office zone
  assert.equal(placementCost(SPECS.road), SPECS.road.cost); // network keeps cost
  assert.equal(placementCost(SPECS.pow_wind), SPECS.pow_wind.cost); // service keeps cost
  assert.ok(isFreeZone(SPECS.res_hut) && !isFreeZone(SPECS.road));
});

test('placing a zone deducts NO money; a non-zone still charges', () => {
  const s0 = initialState();
  const fundsBefore = s0.funds;

  // Zone: funds unchanged, building added, build time still applies (builtTick set).
  const s1 = reducer(s0, { type: 'place', spec: 'res_hut', x: EMPTY.x, y: EMPTY.y });
  assert.equal(s1.funds, fundsBefore, 'zoning must be free — funds unchanged');
  const placed = s1.buildings.find((b) => b.spec === 'res_hut' && b.x === EMPTY.x && b.y === EMPTY.y);
  assert.ok(placed, 'zone building was added');
  assert.equal(placed.builtTick, s0.tick, 'build TIME still applies (builtTick recorded)');

  // Non-zone (road) still charges its cost.
  const s2 = reducer(s1, { type: 'place', spec: 'road', x: 7, y: 5 });
  assert.equal(s2.funds, s1.funds - SPECS.road.cost, 'network structure still costs money');
});

test('bulldozing a free zone refunds nothing (no money printer)', () => {
  const s0 = initialState();
  const s1 = reducer(s0, { type: 'place', spec: 'res_hut', x: EMPTY.x, y: EMPTY.y });
  const s2 = reducer(s1, { type: 'bulldoze', x: EMPTY.x, y: EMPTY.y });
  assert.equal(s2.funds, s1.funds, 'a free zone refunds £0 when demolished');
});

// ---------- FEAT-1972079882: density tier + occupancy fill ----------

test('densityTier is deterministic and ordered by size/capacity', () => {
  assert.equal(densityTier(SPECS.res_hut), 1); // 1×1, 8 residents -> low
  assert.equal(densityTier(SPECS.res_block), 2); // 2×2, 60 residents -> medium
  // A large high-capacity block reaches the top tier.
  assert.equal(densityTier(SPECS.off_tower), 3); // 2×3 + 300 jobs -> high
});

test('blockOccupancy: residents vs capacity fraction, clamped 0..1', () => {
  const half = { buildings: [{ id: 1, spec: 'res_hut', x: 0, y: 0 }], population: 4 };
  assert.equal(blockOccupancy(half, half.buildings[0]), 0.5); // 4 / 8 capacity
  const over = { buildings: [{ id: 1, spec: 'res_hut', x: 0, y: 0 }], population: 999 };
  assert.equal(blockOccupancy(over, over.buildings[0]), 1); // clamped to full
  // Services/parks have no occupancy encoding (render full).
  assert.equal(blockOccupancy({ buildings: [{ id: 1, spec: 'park', x: 0, y: 0 }], population: 5 }, { id: 1, spec: 'park', x: 0, y: 0 }), null);
});

// ---------- FEAT-1972079883: dev +10M ----------

test('debugFunds grants EXACTLY 10,000,000 — no more, no less', () => {
  const s0 = initialState();
  const s1 = reducer(s0, { type: 'debugFunds', amount: 10_000_000 });
  assert.equal(s1.funds - s0.funds, 10_000_000, 'grant is exactly ten million');
  // Idempotent per press: two presses add exactly twenty million.
  const s2 = reducer(s1, { type: 'debugFunds', amount: 10_000_000 });
  assert.equal(s2.funds - s0.funds, 20_000_000);
});

// ---------- FEAT-1972079884: milestone reward once per level ----------

test('level-up grants ~10% cash + notice, EXACTLY ONCE per crossing', () => {
  const s0 = initialState();
  assert.equal(levelOf(s0.xp), 1, 'start at level 1');
  assert.equal(s0.notice, null, 'no notice at start');
  assert.equal(s0.lastRewardedLevel, 1, 'seed level counts as already rewarded');

  // Cross into level 2 (xpForLevel(2) = 50; start xp ~31, so +25 crosses it).
  const need = xpForLevel(2) - s0.xp + 1;
  const s1 = reducer(s0, { type: 'debugXp', amount: need });
  assert.equal(levelOf(s1.xp), 2, 'now level 2');
  const expectCash = Math.round(s0.funds * LEVEL_REWARD_RATE);
  assert.equal(s1.funds - s0.funds, expectCash, 'reward is ~10% of prior funds');
  assert.ok(s1.notice && s1.notice.level === 2, 'notice announces level 2');
  assert.equal(s1.lastRewardedLevel, 2);
  // The notice lists what unlocked at level 2 (data-derived, non-empty here).
  assert.deepEqual(s1.notice.unlocked, unlockedAtLevel(2));

  // A further xp bump that STAYS in level 2 must NOT reward again.
  const s2 = reducer(s1, { type: 'debugXp', amount: 1 });
  assert.equal(s2.funds, s1.funds, 'no second reward within the same level');
  assert.equal(s2.lastRewardedLevel, 2);

  // Dismiss, then bump again inside level 2 — notice stays cleared (fires once).
  const s3 = reducer(s2, { type: 'dismissNotice' });
  assert.equal(s3.notice, null);
  const s4 = reducer(s3, { type: 'debugXp', amount: 1 });
  assert.equal(s4.notice, null, 'no fresh notice while still level 2');
  assert.equal(s4.funds, s3.funds, 'still no extra cash');
});

test('crossing multiple levels at once rewards each level exactly once', () => {
  const s0 = initialState();
  // Jump straight to level 4.
  const s1 = reducer(s0, { type: 'debugXp', amount: xpForLevel(4) });
  assert.equal(levelOf(s1.xp), 4);
  assert.equal(s1.lastRewardedLevel, 4, 'lastRewardedLevel catches up to the new level');
  // Compounded three 10% injections (levels 2,3,4) — funds strictly grew, once each.
  let expected = s0.funds;
  for (let L = 2; L <= 4; L++) expected += Math.round(expected * LEVEL_REWARD_RATE);
  assert.equal(s1.funds, expected, 'each crossed level granted its reward once, compounding');
  assert.ok(s1.notice && s1.notice.level === 4, 'the live notice shows the latest level');
});
