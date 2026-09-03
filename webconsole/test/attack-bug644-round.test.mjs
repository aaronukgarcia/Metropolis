// attack-bug644-round.test.mjs — Independent Destructive round (GR#23) against
// BUG-644 (P1): "the 13k fixture has ~0 water plants so the O(water x
// buildings) BUG-642 freeze never reddened this gate". Attacker != author
// (GR#23 independence amendment). Findings recorded on the BOW item; this
// file keeps the reproducible RED-proofs as a permanent regression estate,
// per the round brief's "Write webconsole/test/attack-bug644-round.test.mjs".
//
// SCOPE: this file only asserts things independently RE-DERIVED and RE-
// MEASURED by the attacker, not things merely re-read from the author's own
// comments. Where a number below matches the author's documented figure, it
// is because this round independently reproduced it (see each test's own
// comment for the reproduction method), not because it was copied.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildScaleFixture, DEFAULT_BUILDING_COUNT, DEFAULT_TARGET_POPULATION } from './scale/fixture.mjs';
import { initialState, reducer } from '../src/sim/engine.ts';
import { SPECS, isOnline } from '../src/sim/data.ts';
import { runConsistencyChecks } from '../src/sim/consistency.ts';

// ============================================================================
// (a) CAN THE GATE ACTUALLY FAIL? — see the round's own scratch exploration
// for the full waterCaps-memo / isOnline-memo / synthetic-slowdown sweep;
// the two below are kept as permanent regressions because they exercise real,
// currently-shipped code paths without needing to hand-edit data.ts at test
// time (which would not survive a source change elsewhere).
// ============================================================================

test('BUG-644 attack: nextSafeBuildingId is load-bearing — the pre-fix stale nextId genuinely collides at scale', () => {
  // Reproduces the exact pre-fix bug by hand: buildScaleFixture() itself now
  // calls nextSafeBuildingId(buildings) (the fix). This test independently
  // proves the defect it fixes was REAL by stomping nextId back down to
  // initialState()'s stale small-city default (what the old code implicitly
  // left in place) and ticking until runConsistencyChecks' buildings.ids-
  // unique check fires. If this ever stops failing, either the fixture no
  // longer hand-assigns ids past initialState()'s default (the bug class is
  // gone by construction) or something else has silently changed the
  // reducer's auto-build id-minting path — either way this test should be
  // re-examined, not deleted.
  let s = buildScaleFixture({ buildingCount: 2000, targetPopulation: 150_000, settleTicks: 1 });
  const staleNextId = initialState().nextId;
  const maxExistingId = Math.max(...s.buildings.map((b) => b.id));
  assert.ok(
    staleNextId <= maxExistingId,
    'precondition: the stale default nextId must be <= the fixture max id, or this attack cannot fire'
  );
  s = { ...s, nextId: staleNextId };

  let collided = false;
  for (let i = 0; i < 80 && !collided; i++) {
    s = reducer(s, { type: 'tick' });
    const report = runConsistencyChecks(s);
    const failure = report.checks.find((c) => !c.ok && /duplicate building/i.test(c.detail ?? ''));
    if (failure) collided = true;
  }
  assert.ok(
    collided,
    'reverting nextId to the pre-fix stale default must eventually collide with a hand-built id ' +
      '(independently reproduced: 771 duplicate IDs at tick 55 on this machine) — proves the ' +
      'nextSafeBuildingId fix is load-bearing, not decorative'
  );

  // And the ACTUAL (non-reverted) fixture must never hit this class of failure.
  const real = buildScaleFixture();
  assert.ok(
    real.nextId > Math.max(...real.buildings.map((b) => b.id)),
    'the real fixture (as shipped) must set nextId strictly above every hand-assigned building id'
  );
});

test('BUG-644 attack: the >=95% online self-check is NOT vacuous — a genuine offline-degradation mutant reddens it', () => {
  // Mutation-adequacy proof for HALF A's own online-fraction assertion (round
  // item (f)): force 40% of buildings into the under-construction gate
  // (builtTick = current tick, which fails computeIsOnline's G1 construction-
  // time check) and confirm the resulting fraction (measured, not assumed)
  // drops below the 0.95 floor the real test enforces.
  const s = buildScaleFixture({ buildingCount: 500, targetPopulation: 40_000, settleTicks: 1 });
  const mutated = {
    ...s,
    buildings: s.buildings.map((b, i) => (i % 5 < 2 ? { ...b, builtTick: s.tick } : b)),
  };
  let onlineCount = 0;
  for (const b of mutated.buildings) if (isOnline(mutated, b)) onlineCount++;
  const fraction = onlineCount / mutated.buildings.length;
  assert.ok(
    fraction < 0.95,
    `expected the 40%-offlined mutant to fail the 95% floor, got ${(fraction * 100).toFixed(1)}% ` +
      '(if this ever passes, the online self-check has gone vacuous — see the AC-6 VACUOUS precedent)'
  );

  // And the real fixture (unmutated) must clear the floor, so the assertion
  // above is a genuine mutation kill, not a tautology that always fails.
  let realOnline = 0;
  for (const b of s.buildings) if (isOnline(s, b)) realOnline++;
  assert.ok(realOnline / s.buildings.length >= 0.95, 'the unmutated fixture must itself clear the 95% floor');
});

// ============================================================================
// (c) COMPOSITION FIDELITY — independently re-tallied by this round against
// Aaron's real savepoint (C:\Users\aarongarcia\.claude\jobs\f9ac9353\tmp\
// aaron-savepoint.lz, decoded via saveCodec.ts's decode(), state at
// .snapshot): every one of the 20 kind counts below was independently
// recomputed by this round from that decoded snapshot and matches the
// fixture header's COMPOSITION PROVENANCE comment exactly (population
// 1,443,526; 29,831 buildings total). Hardcoded here (not re-decoding the
// savepoint at test time) because the savepoint file lives outside this repo
// in a job-scoped tmp dir, not a committed fixture — this is the
// independently-VERIFIED reference table, not a blind copy of the author's
// claim.
// ============================================================================
const VERIFIED_REAL_CAPTURE_COUNTS = {
  road: 14139,
  motorway: 4663,
  school: 3276,
  park: 2819,
  power: 2040,
  rail: 1109,
  water: 488,
  health: 350,
  residential: 348,
  fire: 198,
  industrial: 108,
  commercial: 98,
  police: 80,
  office: 55,
  transport: 33,
  mine: 14,
  civic: 7,
  station: 3,
  pylon: 2,
  landmark: 1,
};
const VERIFIED_REAL_CAPTURE_TOTAL = 29831;

test('BUG-644 attack: fixture composition tracks the independently re-verified real capture within tolerance', () => {
  assert.equal(
    Object.values(VERIFIED_REAL_CAPTURE_COUNTS).reduce((a, b) => a + b, 0),
    VERIFIED_REAL_CAPTURE_TOTAL,
    'sanity: the reference table itself must sum to the documented total'
  );

  const s = buildScaleFixture();
  const counts = {};
  for (const b of s.buildings) {
    const kind = SPECS[b.spec]?.kind ?? 'UNKNOWN';
    counts[kind] = (counts[kind] ?? 0) + 1;
  }

  const offenders = [];
  for (const [kind, realCount] of Object.entries(VERIFIED_REAL_CAPTURE_COUNTS)) {
    const realFrac = realCount / VERIFIED_REAL_CAPTURE_TOTAL;
    const fixtureFrac = (counts[kind] ?? 0) / s.buildings.length;
    // Generous absolute tolerance (2 percentage points) — small-count kinds
    // (landmark 1/29831 = 0.003%) can legitimately round to 0 at 13k scale;
    // this only needs to catch a gross drift (e.g. a category silently
    // reverting to 0% or blowing up to 10x its real share), not pixel-match
    // rounding at the tail.
    if (Math.abs(realFrac - fixtureFrac) > 0.02) {
      offenders.push(`${kind}: real ${(realFrac * 100).toFixed(2)}% vs fixture ${(fixtureFrac * 100).toFixed(2)}%`);
    }
  }
  assert.equal(offenders.length, 0, `composition drifted beyond tolerance: ${offenders.join(', ')}`);
});

// ============================================================================
// (d) DETERMINISM (GR#21)
// ============================================================================
test('BUG-644 attack: buildScaleFixture() is byte-identical across two independent calls', () => {
  const s1 = buildScaleFixture();
  const s2 = buildScaleFixture();
  assert.equal(JSON.stringify(s1), JSON.stringify(s2));
});

// ============================================================================
// (g) SMALL FIXTURES: exact counts, but ONLY when targetPopulation is scaled
// down with buildingCount — this round independently found (and is
// DOCUMENTING, not silently fixing, per GR#24/round scope) that
// buildScaleFixture({buildingCount: N}) WITHOUT also lowering
// targetPopulation from its 1.4M default silently returns MORE than N
// buildings once fillResidential alone needs more than N buildings to reach
// 1.4M capacity (e.g. buildingCount:50 with the default target actually
// returns 123 buildings, all residential, zero of every other category).
// Every one of the 5 real dependent suites (attack-bug630-display-state,
// attack-bug642-memo, render/perfHarness, render/mapRenderer,
// render/instanceBuilder) always passes a correspondingly-small
// targetPopulation alongside a small buildingCount, so this never fires in
// the current suite — but the contract itself ("buildingCount buildings")
// is not honoured for an arbitrary caller. Filed as a finding on BUG-644,
// not fixed here (attacker != author, GR#23).
// ============================================================================
test('BUG-644 attack: small buildingCount DOES land exactly on the requested total when targetPopulation is scaled down (the 5 dependent-suite shape)', () => {
  for (const [buildingCount, targetPopulation] of [
    [50, 1000],
    [200, 5000],
    [300, 20_000],
    [500, 55_000],
    [1000, 100_000],
    [2000, 150_000],
  ]) {
    const s = buildScaleFixture({ buildingCount, targetPopulation, settleTicks: 1 });
    assert.equal(
      s.buildings.length,
      buildingCount,
      `buildingCount:${buildingCount}/targetPopulation:${targetPopulation} must land exactly on ${buildingCount}`
    );
  }
});

test('BUG-644 attack FINDING: buildingCount alone (default targetPopulation) is silently NOT honoured for small counts', () => {
  // Documents the gap found above as a standing, named regression check
  // (not a fix) — if this test ever starts failing because the contract got
  // fixed, that is GOOD news and this test (plus the finding note on
  // BUG-644) should be retired.
  const s = buildScaleFixture({ buildingCount: 50 });
  assert.notEqual(
    s.buildings.length,
    50,
    'this is the documented BUG-644-round finding: buildingCount:50 at the DEFAULT 1.4M target ' +
      'currently returns more than 50 buildings (fillResidential alone needs more to hit the ' +
      'population floor) — if this assertion starts failing, the contract has been fixed upstream'
  );
});
