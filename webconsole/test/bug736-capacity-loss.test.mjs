// bug736-capacity-loss.test.mjs — BUG-736 (P1), FEAT-2326609761 CONSOLIDATOR
// mutation lane.
//
// Independent round finding (proven live through the real tick reducer,
// default sliders): 40 auto-scaled edu_nursery (capacityTier 9) in one
// section, consolidator ON — month 1 commits a transaction merging the
// ladder-derived group of nurseries into ONE edu_nursery_city, DESTROYING
// real capacity because two divergent, un-gated formulas both under-counted
// the group's true tiered capacity:
//
//   (1) engine.ts's applyConsolidatorPass never gated on capacityGain at
//       all — consolidator.ts's findOpportunities already computes the REAL
//       tiered capacityGain (and documents that a negative value must be
//       reported HONESTLY, never clamped, "so Aaron can see it"), but the
//       apply site never actually read it before committing.
//   (2) CEIL-3's family-share ceiling used `capacityOf(fromSpec) *
//       opp.groupCount` — flat TIER-0 capacity for every group member,
//       regardless of real auto-scale tier — a SECOND, DIVERGENT formula
//       from findOpportunities' own real tiered sum (a GR#3 SSOT split),
//       which is exactly why a tier-9 group could sail past the 0.5
//       family-share ceiling meant to catch a single-successor's capacity
//       dominance.
//
// Fix (engine.ts, applyConsolidatorPass): a `groupCapacityReal` (summed via
// consolidator.ts's exported `buildingCapacityOf` — the SAME tiered helper
// findOpportunities uses, GR#3, never a second formula) computed once per
// candidate, used BOTH to gate (`capacityGain < 0` => skip, reason
// 'capacity loss') and to feed CEIL-3's family-share check (replacing its
// former flat approximation).
//
// This file uses the REAL catalogue numbers (SPECS.edu_nursery /
// SPECS.edu_nursery_city / the live consolidationLadder()'s derived
// groupSize) throughout — GR#15: no hand-typed expected capacity constant,
// every expectation is derived from the data these tests exercise.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { SPECS, computeRoadConnectivity, capacityAtTier } from '../src/sim/data.ts';
import { initialState, reducer, TICKS_PER_MONTH, CONSOLIDATOR_UNLOCK_LEVEL, xpForLevel, levelOf } from '../src/sim/engine.ts';
import { consolidationLadder } from '../src/sim/consolidator.ts';
import { runConsistencyChecks } from '../src/sim/consistency.ts';
import { stableStringify } from '../src/sim/genesisReplay.ts';

// Mirrors consolidator-mutation.test.mjs's own `mk()`/`roadRow()`/
// `withConnectivity()`/`advanceToNextBoundary()` idiom exactly — this file
// deliberately does not invent a parallel fixture convention.
function mk(over) {
  const base = initialState();
  return {
    ...base,
    unlockedAll: true,
    roadMonitors: [],
    buildingMonitors: [],
    buildings: [],
    population: 0,
    funds: 500_000_000,
    tick: 0,
    consolidatorEnabled: false,
    consolidatorLog: [],
    xp: xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL),
    lastRewardedLevel: levelOf(xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL)),
    consolidatorMode: 'monthly-twelfth',
    ...over,
  };
}

function roadRow(y, maxX) {
  const roads = [];
  for (let x = 0; x <= maxX; x++) roads.push({ id: 1000 + y * 1000 + x, spec: 'road', x, y, builtTick: -1000 });
  return roads;
}

function withConnectivity(s) {
  return { ...s, roadConnectivity: computeRoadConnectivity(s) };
}

function advanceToNextBoundary(s) {
  let cur = s;
  do {
    cur = reducer(cur, { type: 'tick' });
  } while (cur.tick % TICKS_PER_MONTH !== 0);
  return cur;
}

/**
 * Runs `n` monthly boundaries. The fixtures below place everything in
 * section key 0 (x 0..15), which is only in the CURRENT month's rotation
 * scope (consolidator.ts's monthlyScopeOf, ruling 7) on twelfths whose
 * `key % 12` matches — running a FULL 12-month cycle (twelfth 11 = the
 * whole-map month, per ruling 7) guarantees section 0 is considered at
 * least once regardless of which twelfth the first boundary happens to
 * land on, mirroring consolidator-mutation.test.mjs's own CEIL-4 fixtures
 * ("for (let m = 0; m < 12; m++) s = advanceToNextBoundary(s);").
 */
function runMonths(s, n) {
  let cur = s;
  for (let m = 0; m < n; m++) cur = advanceToNextBoundary(cur);
  return cur;
}

const NURSERY = SPECS.edu_nursery;
const NURSERY_CITY = SPECS.edu_nursery_city;
const RUNG = consolidationLadder().find((e) => e.from === 'edu_nursery' && e.to === 'edu_nursery_city');

/**
 * Places `tiers.length` edu_nursery buildings, one per entry (tiers[i] is
 * that building's capacityTier), row-major within a single 16x16 section
 * (section 0: x 0..15, y 0..15 — SECTION_TILES, consolidator.ts), with a
 * road row along y=0 so the successor (sited on the freed footprint) is
 * always road-adjacent. `headroomCount` extra, untouched edu_nursery_city
 * buildings are placed far away purely to give CEIL-3's family-share ceiling
 * enough city-wide headroom to ever pass (mirrors consolidator-mutation.
 * test.mjs's own fireStationFixture headroom idiom) — irrelevant to the
 * capacity-loss cases, which are gated before CEIL-3 is ever reached.
 */
function nurseryFixture(tiers, headroomCount = 0) {
  const buildings = [...roadRow(0, 15)];
  tiers.forEach((tier, i) => {
    const x = i % 16;
    const y = 1 + Math.floor(i / 16);
    buildings.push({ id: 100 + i, spec: 'edu_nursery', x, y, builtTick: -1000, capacityTier: tier });
  });
  for (let h = 0; h < headroomCount; h++) {
    buildings.push({ id: 9000 + h, spec: 'edu_nursery_city', x: 300 + h * 10, y: 300, builtTick: -1000 });
  }
  return withConnectivity(mk({ buildings }));
}

/** Total edu_nursery capacity currently on the board, tier-aware — the SSOT figure every "capacity held" assertion below checks against. */
function totalNurseryCapacity(s) {
  return s.buildings
    .filter((b) => b.spec === 'edu_nursery')
    .reduce((sum, b) => sum + capacityAtTier(NURSERY, b.capacityTier ?? 0), 0);
}

describe('BUG-736 setup — the real ladder rung this whole file exercises', () => {
  test('edu_nursery -> edu_nursery_city exists with a derivable, non-trivial groupSize', () => {
    assert.ok(RUNG, 'the edu_nursery -> edu_nursery_city rung must exist in the live catalogue-derived ladder');
    assert.ok(RUNG.groupSize >= 4, 'CONSOLIDATOR_MIN_GROUP floor');
    // Sanity precondition every scenario below relies on: a TIER-0 group at
    // exactly the derived groupSize must not itself already be a capacity
    // loss (AC-8 rule 3 guarantees this structurally) — if this ever failed
    // the whole ladder rung would be broken independent of BUG-736.
    const flatGroupCapacity = RUNG.groupSize * capacityAtTier(NURSERY, 0);
    assert.ok(flatGroupCapacity <= capacityAtTier(NURSERY_CITY, 0), 'tier-0 group capacity must not exceed the successor (AC-8 rule 4)');
  });
});

describe('BUG-736 — tier-9 auto-scaled nurseries: capacity must NEVER be lost', () => {
  test('40 tier-9 nurseries: the ladder-derived group is SKIPPED (capacity loss), never consolidated, for 24 months straight', () => {
    const tiers = Array.from({ length: 40 }, () => 9);
    let s = nurseryFixture(tiers);
    s = reducer(s, { type: 'toggleConsolidator' });

    const before = totalNurseryCapacity(s);
    // Precondition: this fixture really DOES reproduce the defect's shape —
    // the real tiered group capacity must exceed the successor's, or this
    // test would prove nothing.
    const realGroupCapacity = RUNG.groupSize * capacityAtTier(NURSERY, 9);
    assert.ok(realGroupCapacity > capacityAtTier(NURSERY_CITY, 0), 'setup: the tier-9 group must be a genuine capacity-loss case');

    let sawCapacityLossSkip = false;
    for (let m = 0; m < 24; m++) {
      s = advanceToNextBoundary(s);
      const pass = (s.consolidatorLog ?? [])[0];
      if (pass && pass.skipped.some((sk) => sk.reason === 'capacity loss')) sawCapacityLossSkip = true;
      // Never demolished: all 40 tier-9 nurseries survive every single month.
      assert.equal(
        s.buildings.filter((b) => b.spec === 'edu_nursery').length,
        40,
        `month ${m + 1}: no tier-9 nursery may ever be demolished into a capacity-losing successor`,
      );
      assert.equal(s.buildings.filter((b) => b.spec === 'edu_nursery_city').length, 0, `month ${m + 1}: no successor was ever built from this group`);
    }
    assert.ok(sawCapacityLossSkip, "GR#17 visibility: the skip must be recorded with reason 'capacity loss', not silently dropped");
    assert.equal(totalNurseryCapacity(s), before, 'total nursery capacity held EXACTLY steady across 24 months (AC-8 rule 4)');
    assert.ok(totalNurseryCapacity(s) >= realGroupCapacity, 'capacity never fell below the pre-existing real (tiered) total');
  });
});

describe('BUG-736 — the intended arc still works: tier-0 groups DO consolidate', () => {
  test('a tier-0 group of 40 nurseries consolidates into a City Kindergarten (real capacity GAIN, never a loss)', () => {
    const tiers = Array.from({ length: 40 }, () => 0);
    // 2 headroom edu_nursery_city elsewhere so CEIL-3's family-share ceiling
    // has enough city-wide capacity to ever let a successor through — see
    // nurseryFixture's own doc comment.
    let s = nurseryFixture(tiers, 2);
    s = reducer(s, { type: 'toggleConsolidator' });
    s = runMonths(s, 12);

    const log = s.consolidatorLog ?? [];
    assert.ok(log.length > 0, 'at least one pass ran across the 12-month rotation');
    const txn = log
      .flatMap((p) => p.transactions)
      .find((t) => t.added[0]?.spec === 'edu_nursery_city' && t.removed.every((r) => r.spec === 'edu_nursery'));
    assert.ok(txn, 'the tier-0 group DID consolidate into a City Kindergarten — the intended arc must keep working');
    assert.equal(txn.removed.length, RUNG.groupSize, 'exactly the ladder-derived group size was consolidated');

    const groupCapacityReal = RUNG.groupSize * capacityAtTier(NURSERY, 0);
    const successorCapacity = capacityAtTier(NURSERY_CITY, 0);
    assert.ok(successorCapacity >= groupCapacityReal, 'setup: this rung is a genuine capacity GAIN, the positive control for BUG-736s gate');

    // No skip for "capacity loss" was ever recorded for this fixture's own
    // 40-nursery group — the gate must not false-positive on a legitimate
    // gain (some OTHER unrelated skip reason, e.g. 'action budget', is fine).
    const anyCapacityLossSkip = log.some((p) => p.skipped.some((sk) => sk.reason === 'capacity loss'));
    assert.equal(anyCapacityLossSkip, false, 'a genuine capacity-gain rung must never be skipped as a capacity loss');
  });
});

describe('BUG-736 — mixed-tier groups: the gate sums REAL per-building tiers, not a flat approximation', () => {
  // BUG-685/686 (0eb5f31, landed after this file was authored): edu_nursery's
  // capacityTiers moved from tierLadder(30) (topped at 71) to
  // campusLadder(30, 2000) (data.ts) — a much steeper curve. Under the OLD
  // ladder, nHigh=3 tier-1 members (delta +3 each, +9 total) fit comfortably
  // inside the group's +10 slack to the successor (33*30=990, successor
  // 1000), making a genuine MIXED (>=1 upgraded member) under-successor
  // group constructible. Under the LIVE campusLadder, tier-1 is +18 — MORE
  // than the group's entire +10 slack on its own, so upgrading even ONE
  // member already overshoots (990+18=1008 > 1000). Proven exhaustively
  // (not assumed): a breadth-first search over every reachable sum of up to
  // groupSize-1 real tier-upgrade deltas never lands in (0, 10] once at
  // least one upgrade is used — the smallest reachable positive delta is 18.
  // The ONLY real "at-or-under-successor" edu_nursery group left is the
  // trivial ALL-tier-0 one, already covered by the sibling "the intended
  // arc still works" describe block above — a genuinely MIXED under-
  // successor group is UNCONSTRUCTIBLE for this rung on the live catalogue.
  // Skipped here (GR#15: never fake a fixture number) rather than weakened;
  // attack-bug736-round.test.mjs proves the same underlying gate semantic
  // ("a real, non-trivial tiered sum at/under the successor still
  // consolidates") on a rung where it IS constructible.
  test('mixed tiers summing UNDER the successor still consolidate', (t) => {
    const T0 = capacityAtTier(NURSERY, 0);
    const slack = capacityAtTier(NURSERY_CITY, 0) - RUNG.groupSize * T0; // +10 on the live catalogue
    const deltas = (NURSERY.capacityTiers ?? []).map((cap) => cap - T0).filter((d) => d > 0);
    const minUpgradeDelta = deltas.length ? Math.min(...deltas) : Infinity; // +18 on the live catalogue
    const mixedUnderConstructible = minUpgradeDelta <= slack;
    if (!mixedUnderConstructible) {
      t.skip(
        `UNCONSTRUCTIBLE on the live campusLadder(30,2000): the smallest real edu_nursery tier upgrade is +${minUpgradeDelta}, which exceeds the group's entire +${slack} slack below the successor, so no group with >=1 upgraded member can stay at or under it (see comment above)`,
      );
      return;
    }
    // Kept for if/when a future catalogue change reopens this case.
    const nHigh = 3;
    const nFlat = RUNG.groupSize - nHigh;
    const tiers = [...Array.from({ length: nFlat }, () => 0), ...Array.from({ length: nHigh }, () => 1)];
    const groupCapacityReal = nFlat * capacityAtTier(NURSERY, 0) + nHigh * capacityAtTier(NURSERY, 1);
    const successorCapacity = capacityAtTier(NURSERY_CITY, 0);
    assert.ok(groupCapacityReal <= successorCapacity, 'setup: this mix must be a real (or break-even) capacity gain');

    let s = nurseryFixture(tiers, 2);
    s = reducer(s, { type: 'toggleConsolidator' });
    s = runMonths(s, 12);

    const log = s.consolidatorLog ?? [];
    const txn = log.flatMap((p) => p.transactions).find((t) => t.added[0]?.spec === 'edu_nursery_city');
    assert.ok(txn, 'a mixed group whose REAL summed capacity does not exceed the successor must still consolidate');
  });

  test('mixed tiers summing OVER the successor are skipped (capacity loss), exactly like the pure-tier-9 case', () => {
    const nHigh = 3;
    const nFlat = RUNG.groupSize - nHigh;
    const tiers = [...Array.from({ length: nFlat }, () => 0), ...Array.from({ length: nHigh }, () => 9)];
    const groupCapacityReal = nFlat * capacityAtTier(NURSERY, 0) + nHigh * capacityAtTier(NURSERY, 9);
    const successorCapacity = capacityAtTier(NURSERY_CITY, 0);
    assert.ok(groupCapacityReal > successorCapacity, 'setup: this mix must be a real capacity loss — the case a flat-tier-0 approximation would miss');

    let s = nurseryFixture(tiers);
    s = reducer(s, { type: 'toggleConsolidator' });
    s = runMonths(s, 12);

    const log = s.consolidatorLog ?? [];
    assert.ok(log.length > 0, 'at least one pass ran across the 12-month rotation');
    const anyCapacityLossSkip = log.some((p) => p.skipped.some((sk) => sk.reason === 'capacity loss'));
    assert.ok(anyCapacityLossSkip, 'a mixed-tier group whose REAL summed capacity exceeds the successor must be skipped, never approximated as safe');
    assert.equal(s.buildings.filter((b) => b.spec === 'edu_nursery_city').length, 0, 'no successor was built from this loss-making mix');
    assert.equal(s.buildings.filter((b) => b.spec === 'edu_nursery').length, tiers.length, 'every nursery in the mix survives untouched');
  });
});

describe('BUG-736 — determinism (GR#21) and conservation of funds', () => {
  test('two structurally-identical tier-9 states produce a byte-identical pass (the skip is deterministic)', () => {
    const tiers = Array.from({ length: 40 }, () => 9);
    const a = nurseryFixture(tiers);
    const b = JSON.parse(JSON.stringify(nurseryFixture(tiers)));
    let sa = reducer(a, { type: 'toggleConsolidator' });
    let sb = reducer(b, { type: 'toggleConsolidator' });
    sa = runMonths(sa, 12);
    sb = runMonths(sb, 12);
    assert.ok((sa.consolidatorLog ?? []).some((p) => p.skipped.some((sk) => sk.reason === 'capacity loss')), 'setup: the skip actually fired, so this proves determinism of the real gated path, not an inert no-op');
    assert.equal(stableStringify(sa), stableStringify(sb));
  });

  test('conservation of funds holds when a capacity-loss group is skipped (no spend for a transaction that never happened)', () => {
    const tiers = Array.from({ length: 40 }, () => 9);
    let s = nurseryFixture(tiers);
    s = reducer(s, { type: 'toggleConsolidator' });
    s = runMonths(s, 12);
    assert.ok((s.consolidatorLog ?? []).some((p) => p.skipped.some((sk) => sk.reason === 'capacity loss')), 'setup: the skip actually fired');
    const report = runConsistencyChecks(s);
    const conservationCheck = report.checks.find((c) => c.id === 'conservation.funds-vs-flows');
    assert.ok(conservationCheck, 'conservation check ran');
    assert.equal(conservationCheck.ok, true, conservationCheck.detail);
  });

  test('conservation of funds holds when a legitimate tier-0 consolidation IS applied', () => {
    const tiers = Array.from({ length: 40 }, () => 0);
    let s = nurseryFixture(tiers, 2);
    s = reducer(s, { type: 'toggleConsolidator' });
    s = runMonths(s, 12);
    assert.ok((s.consolidatorLog ?? []).some((p) => p.transactions.some((t) => t.added[0]?.spec === 'edu_nursery_city')), 'setup: a real transaction actually applied');
    const report = runConsistencyChecks(s);
    const conservationCheck = report.checks.find((c) => c.id === 'conservation.funds-vs-flows');
    assert.ok(conservationCheck, 'conservation check ran');
    assert.equal(conservationCheck.ok, true, conservationCheck.detail);
  });
});
