// bug-466-autoscale-cap.test.mjs — BUG-466: building auto-scale rate-limit + cooldown
//
// THE BUG: evaluateBuildingMonitors gated every monitored building's upgrade on the
// SAME city-wide utilization number (residents/residentsCapForPass or jobs/jobsCapForPass
// — identical for every building). Once the city crossed BUILDING_UTILIZATION_THRESHOLD,
// EVERY monitored non-maxed building upgraded in the SAME monthly pass — e.g. 2000
// buildings x ~£6,750 = £13.5M in one month — and it recurred as a treadmill every time
// population regrew into the ceiling.
//
// THE FIX: MAX_AUTO_SCALE_UPGRADES_PER_PASS caps how many buildings can be queued for
// upgrade in a single pass (selected in the existing deterministic strict-buildingId
// order), and AUTO_SCALE_COOLDOWN_TICKS + Building.lastAutoScaleTick stop the same
// building re-upgrading every cycle.
//
// Determinism (GR#21): the cap/cooldown use only tick + stable buildingId order + a
// per-building state field — no Date/Math.random. Replaying the same input reproduces
// an identical set of upgraded buildings and an identical charge.
//
// RED proof (scratch cp/mv, restore after): remove the
// `if (tierUpgradeById.size >= MAX_AUTO_SCALE_UPGRADES_PER_PASS) break;` line from
// evaluateBuildingMonitors in src/sim/engine.ts — the cap test below goes RED (all 40
// buildings upgrade in one pass instead of 25, and the charged cost lumps to
// 40 x 6750 = 270000 instead of 25 x 6750 = 168750).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SPECS } from '../src/sim/data.ts';
import {
  initialState,
  evaluateBuildingMonitors,
  MAX_AUTO_SCALE_UPGRADES_PER_PASS,
  AUTO_SCALE_COOLDOWN_TICKS,
  BUILDING_AUTO_SCALE_COST_FRACTION,
} from '../src/sim/engine.ts';

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
    // initialState() = advance(rawState()) already computes a (non-null)
    // roadConnectivity — explicitly clear it here. isOnline() skips the road
    // gate when roadConnectivity is absent (data.ts: "the road gates only apply
    // once s.roadConnectivity has been computed"), so these buildings are online
    // purely by builtTick/construction, isolating the cap/cooldown logic from the
    // (separately tested) road gates.
    roadConnectivity: undefined,
    ...over,
  };
}

const RES_ESTATE_COST = 45000;
const EXPECTED_UPGRADE_COST = Math.round(RES_ESTATE_COST * BUILDING_AUTO_SCALE_COST_FRACTION);
assert.equal(EXPECTED_UPGRADE_COST, 6750, 'precondition: pinned literal matches 45000 * 0.15');

// N well above the cap so the pre-fix behavior (all upgrade) and the fixed behavior
// (at most MAX per pass) are clearly distinguishable.
const N = 40;

function manyBuildings(n) {
  const buildings = [];
  const monitors = [];
  for (let i = 1; i <= n; i++) {
    buildings.push({ id: i, spec: 'res_estate', x: i, y: 0, builtTick: -1000 });
    monitors.push({ buildingId: i, until: 100000, type: 'residents' });
  }
  return { buildings, monitors };
}

test('BUG-466 (a): a saturated city with >MAX eligible buildings upgrades AT MOST MAX_AUTO_SCALE_UPGRADES_PER_PASS in one pass, charging exactly that many x cost (no lump)', () => {
  const { buildings, monitors } = manyBuildings(N);
  const sp = SPECS['res_estate'];
  const totalCap = N * sp.capacityTiers[0]; // 40 * 1500 = 60000
  const population = Math.ceil(totalCap * 0.9); // 90% utilization, well above 0.85 threshold

  const s = mk({ buildings, buildingMonitors: monitors, population, tick: 0 });
  const result = evaluateBuildingMonitors(s, 30);

  assert.equal(
    result.upgraded,
    MAX_AUTO_SCALE_UPGRADES_PER_PASS,
    `at most ${MAX_AUTO_SCALE_UPGRADES_PER_PASS} buildings upgrade in one pass, not all ${N}`
  );
  assert.equal(
    result.cost,
    MAX_AUTO_SCALE_UPGRADES_PER_PASS * EXPECTED_UPGRADE_COST,
    'charge is exactly cappedCount x cost — no lump for the un-applied upgrades'
  );

  // Determinism of SELECTION: the cap picks the buildings in strict buildingId
  // order (the pass's existing sort), so it is always the FIRST
  // MAX_AUTO_SCALE_UPGRADES_PER_PASS ids, 1..MAX, that scale — never a random subset.
  const scaledIds = result.buildings
    .filter((b) => b.capacityTier === 1)
    .map((b) => b.id)
    .sort((a, b) => a - b);
  const expectedIds = Array.from({ length: MAX_AUTO_SCALE_UPGRADES_PER_PASS }, (_, i) => i + 1);
  assert.deepEqual(scaledIds, expectedIds, 'the FIRST MAX buildings (by id) are the ones upgraded');

  // The remaining buildings (26..40) stayed at tier 0 — rolled over to a later pass,
  // not silently dropped and not charged.
  const untouched = result.buildings.filter((b) => b.id > MAX_AUTO_SCALE_UPGRADES_PER_PASS);
  assert.ok(
    untouched.every((b) => b.capacityTier === undefined),
    'buildings past the cap remain at tier 0 (undefined) this pass'
  );
});

test('BUG-466 (b): a building that auto-scaled does NOT scale again within AUTO_SCALE_COOLDOWN_TICKS, and CAN after', () => {
  const building = { id: 1, spec: 'res_estate', x: 0, y: 0, builtTick: -1000 };
  const sp = SPECS['res_estate'];
  const population = Math.ceil(sp.capacityTiers[0] * 0.95); // 95% util — well above threshold

  // First pass at tick 30: scales tier 0 -> 1, records lastAutoScaleTick = 30.
  let s = mk({
    buildings: [building],
    buildingMonitors: [{ buildingId: 1, until: 100000, type: 'residents' }],
    population,
    tick: 0,
  });
  let result = evaluateBuildingMonitors(s, 30);
  let b = result.buildings.find((x) => x.id === 1);
  assert.equal(b.capacityTier, 1, 'precondition: first pass scales the building');
  assert.equal(b.lastAutoScaleTick, 30, 'lastAutoScaleTick recorded at the scaling tick');
  assert.equal(result.upgraded, 1, 'precondition: exactly one upgrade counted');

  // Utilization is recomputed against the NEW (larger) capacity too — bump population
  // to keep utilization saturated at the new, higher tier-1 capacity so the ONLY
  // thing preventing a second upgrade is the cooldown, not utilization dropping.
  const tier1Cap = sp.capacityTiers[1];
  const highPopulation = Math.ceil(tier1Cap * 0.95);

  // Second pass, still inside the cooldown window (30 + 1 tick later, well under
  // AUTO_SCALE_COOLDOWN_TICKS): must NOT scale again.
  s = { ...s, buildings: result.buildings, population: highPopulation };
  const withinCooldownTick = 30 + 1;
  assert.ok(withinCooldownTick - b.lastAutoScaleTick < AUTO_SCALE_COOLDOWN_TICKS, 'test setup: still inside cooldown');
  result = evaluateBuildingMonitors(s, withinCooldownTick);
  b = result.buildings.find((x) => x.id === 1);
  assert.equal(b.capacityTier, 1, 'still tier 1 — cooldown blocks a second scale so soon');
  assert.equal(result.upgraded, 0, 'no upgrade counted while in cooldown');
  assert.equal(result.cost, 0, 'no charge while in cooldown');

  // Third pass, AFTER the cooldown has elapsed: eligible to scale again.
  s = { ...s, buildings: result.buildings, population: highPopulation };
  const afterCooldownTick = 30 + AUTO_SCALE_COOLDOWN_TICKS;
  result = evaluateBuildingMonitors(s, afterCooldownTick);
  b = result.buildings.find((x) => x.id === 1);
  assert.equal(b.capacityTier, 2, 'tier increments again once cooldown has elapsed');
  assert.equal(result.upgraded, 1, 'one upgrade counted after cooldown clears');
  assert.equal(b.lastAutoScaleTick, afterCooldownTick, 'lastAutoScaleTick refreshed to the new scaling tick');
});

test('BUG-466 (c): determinism — same input evaluated twice (incl. via a deep-cloned state) produces byte-identical upgraded sets and cost', () => {
  const { buildings, monitors } = manyBuildings(N);
  const sp = SPECS['res_estate'];
  const totalCap = N * sp.capacityTiers[0];
  const population = Math.ceil(totalCap * 0.9);

  const s1 = mk({ buildings, buildingMonitors: monitors, population, tick: 0 });
  const s2 = JSON.parse(JSON.stringify(s1)); // simulates a save/reload round trip

  const r1 = evaluateBuildingMonitors(s1, 30);
  const r2 = evaluateBuildingMonitors(s2, 30);

  assert.equal(r1.upgraded, r2.upgraded, 'same upgrade count');
  assert.equal(r1.cost, r2.cost, 'same charge');
  assert.deepEqual(
    JSON.stringify(r1.buildings.map((b) => ({ id: b.id, tier: b.capacityTier, last: b.lastAutoScaleTick }))),
    JSON.stringify(r2.buildings.map((b) => ({ id: b.id, tier: b.capacityTier, last: b.lastAutoScaleTick }))),
    'byte-identical per-building outcome (same buildings picked, same tiers, same lastAutoScaleTick)'
  );

  // Running it a third time with the SAME r1 output re-evaluated at the same tick
  // again is also stable (idempotent within a pass — evaluateBuildingMonitors is pure).
  const r1Again = evaluateBuildingMonitors(s1, 30);
  assert.deepEqual(r1Again.buildings, r1.buildings, 'pure function — identical input always yields identical output');
});

test('BUG-466 (d): conservation — funds charged equal exactly (buildings actually upgraded) x upgradeCost, never more', () => {
  const { buildings, monitors } = manyBuildings(N);
  const sp = SPECS['res_estate'];
  const totalCap = N * sp.capacityTiers[0];
  const population = Math.ceil(totalCap * 0.9);

  const s = mk({ buildings, buildingMonitors: monitors, population, tick: 0 });
  const result = evaluateBuildingMonitors(s, 30);

  assert.equal(result.cost, result.upgraded * EXPECTED_UPGRADE_COST, 'cost === upgraded x per-upgrade cost exactly');
  assert.ok(result.upgraded < N, 'precondition: the cap actually bit (fewer upgrades than eligible buildings)');

  // Double-check by independently counting tier bumps in the returned buildings —
  // the charged cost must equal the ACTUAL number of buildings whose tier changed,
  // not the number of monitors that were merely eligible.
  const actuallyUpgraded = result.buildings.filter((b) => (b.capacityTier ?? 0) > 0).length;
  assert.equal(actuallyUpgraded, result.upgraded, 'upgraded count matches buildings whose capacityTier actually changed');
  assert.equal(result.cost, actuallyUpgraded * EXPECTED_UPGRADE_COST, 'no charge for un-applied (capped) upgrades');
});

test('BUG-466 (e): backward compatibility — a building/save with no lastAutoScaleTick field loads and is eligible to auto-scale (never in cooldown)', () => {
  // A building shaped exactly like a pre-BUG-466 save: no lastAutoScaleTick key at all
  // (not even `undefined` explicitly — the property is simply absent, as JSON.parse
  // of an old save would produce).
  const legacyBuilding = { id: 1, spec: 'res_estate', x: 0, y: 0, builtTick: -1000, capacityTier: 0 };
  assert.ok(!('lastAutoScaleTick' in legacyBuilding), 'precondition: field is genuinely absent, old-save shaped');

  const sp = SPECS['res_estate'];
  const population = Math.ceil(sp.capacityTiers[0] * 0.95);

  const s = mk({
    buildings: [legacyBuilding],
    buildingMonitors: [{ buildingId: 1, until: 100000, type: 'residents' }],
    population,
    tick: 0,
  });

  const result = evaluateBuildingMonitors(s, 30);
  const b = result.buildings.find((x) => x.id === 1);
  assert.equal(b.capacityTier, 1, 'legacy building (no lastAutoScaleTick) is eligible and scales normally');
  assert.equal(result.upgraded, 1, 'one upgrade counted');
  assert.equal(b.lastAutoScaleTick, 30, 'field is now populated going forward');
});
