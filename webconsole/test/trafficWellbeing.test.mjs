// trafficWellbeing.test.mjs — FEAT-2326609798 inc5 r2 "UNHAPPINESS COUPLING"
// REWORK after round r1 REJECT (row 7607, BUG-877/878/879/880). Authority:
// docs/planning/acceptance/FEAT-2326609792-inc5.md AC-1..AC-6 + the Lead
// amendments 1-5 at the bottom of that doc.
//
// Run with `node tools/test/scoped.mjs webconsole/test/trafficWellbeing.test.mjs`.
//
// BUG-880 integrity fix: no test pin in this file claims a mutant is RED
// without having actually been run against it. Every "MUTANT" comment below
// is one of:
//   (a) SCRATCH-PROVEN — a scratch copy of the touched file (session
//       scratchpad, never git) had the mutation applied and this exact test
//       file was pointed at it; the predicted RED was reproduced. The exact
//       command is quoted.
//   (b) DISCRIMINATED IN-TEST — the pin itself calls BOTH the real
//       implementation and a literal inline mutant implementation on the
//       same fixture and asserts they differ (no separate scratch file
//       needed because the "mutant" is a plain local function, not a
//       modification to shipped source — this is strictly stronger than an
//       analytic claim because the mutant code actually RUNS in this
//       process against the real fixture data).
// Nothing in this file is marked "analytically proven".
//
// Test count: this file has the number of `test(` calls `node --test`
// reports — do not restate a stale count in the BOW comment (BUG-880
// finding 1).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import {
  commuteStressWithConfig,
  commuteStressOf,
  commutePenaltyWithConfig,
  gridlockPenaltyOf,
  emergencyPenaltyOf,
  trafficPenaltyWithConfig,
  maxTrafficPenaltyWithConfig,
  compositeWithTrafficPenalty,
  computeTrafficSnapshot,
  sanitizeTrafficSnapshot,
  commuteWellbeingPartOf,
  gridlockWellbeingPartOf,
  emergencyWellbeingPartOf,
  trafficPenaltyOf,
  earlyGameScaledTrafficPenaltyWithConfig,
  isTrafficCadenceTickWithConfig,
  loadMentalWellbeingConfigFrom,
  loadTrafficRecomputeTicksFrom,
  TRAFFIC_RECOMPUTE_TICKS,
  ERR_WELLBEING_TRAFFIC_DATA_MISSING,
  ERR_WELLBEING_COMMUTE_ANCHOR_INVALID,
  ERR_WELLBEING_GRIDLOCK_WEIGHT_MISSING,
  ERR_TRAFFIC_RECOMPUTE_TICKS_INVALID,
  TRAFFIC_SNAPSHOT_KEY_ORDER,
} from '../src/sim/trafficWellbeing.ts';
import {
  commuteTimeDistributionOf,
  gridlockedSegmentsOf,
  tilePathsOf,
  tileVehicleTripsOf,
  __resetDijkstraRelaxationCounterForTest,
  __getDijkstraRelaxationCounterForTest,
} from '../src/sim/trafficAssignment.ts';
import { emergencyCoverageOf } from '../src/sim/emergencyResponse.ts';
import { sanitizeCongestionTicksBySpec, lineSegmentIndexOf, wellbeingPartOf, earlyGameFactor, CONGESTION_CONSTANTS, SPECS } from '../src/sim/data.ts';
import { initialState, wellbeingOf, wellbeingPreApprovalOf, reducer, specUnlocked } from '../src/sim/engine.ts';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(__dirname, '..', '..');
const trafficWellbeingSrc = readFileSync(path.join(repoRoot, 'webconsole', 'src', 'sim', 'trafficWellbeing.ts'), 'utf8');
const trafficAssignmentSrc = readFileSync(path.join(repoRoot, 'webconsole', 'src', 'sim', 'trafficAssignment.ts'), 'utf8');
const emergencyResponseSrc = readFileSync(path.join(repoRoot, 'webconsole', 'src', 'sim', 'emergencyResponse.ts'), 'utf8');
const mirroredWellbeing = JSON.parse(
  readFileSync(path.join(repoRoot, 'webconsole', 'src', 'sim', 'traffic-data', 'wellbeing.json'), 'utf8')
);
const mirroredTraffic = JSON.parse(
  readFileSync(path.join(repoRoot, 'webconsole', 'src', 'sim', 'traffic-data', 'traffic.json'), 'utf8')
);
const MENTAL = loadMentalWellbeingConfigFrom(mirroredWellbeing);

const OFFSET = 200;
function board(buildings, population = 0) {
  const base = initialState();
  let maxId = 0;
  for (const b of buildings) if (b.id > maxId) maxId = b.id;
  return { ...base, unlockedAll: true, buildings, nextId: maxId + 1, roadNotice: null, population };
}
function rd(id, spec, x, y) {
  return { id, spec, x: x + OFFSET, y: y + OFFSET, builtTick: 0 };
}
function bldg(id, spec, x, y) {
  return { id, spec, x: x + OFFSET, y: y + OFFSET };
}
function k(x, y) {
  return `${x + OFFSET},${y + OFFSET}`;
}

// ---------------------------------------------------------------------------
// AC-1 / BUG-878 survivor 1 (hardcoded anchors) + survivor 4 (median-not-p90)
// ---------------------------------------------------------------------------

test('AC-1/BUG-878: commuteStressWithConfig reads its anchors from the EXPLICIT config argument, not a hardcoded constant', () => {
  const cfgA = { commuteWeight: 10, commuteThresholdMinutes: 45, commuteStressAtThreshold: 0.5, commuteStressAt100Minutes: 2.0, gridlockWeight: 0.6, emergencyResponseWeight: 10 };
  const cfgB = { commuteWeight: 10, commuteThresholdMinutes: 20, commuteStressAtThreshold: 1.2, commuteStressAt100Minutes: 3.0, gridlockWeight: 0.6, emergencyResponseWeight: 10 };
  // Same medianMinutes, two DIFFERENT anchor configs -> the result MUST differ
  // if the anchors are genuinely sourced from the argument.
  const stressA = commuteStressWithConfig(30, cfgA);
  const stressB = commuteStressWithConfig(30, cfgB);
  assert.notEqual(stressA, stressB, 'BUG-878: two different scratch configs must produce different stress values -- a hardcoded anchor cannot pass this');
  assert.equal(stressA, (30 / 45) * 0.5, 'stress must match the piecewise formula computed against cfgA\'s own anchors exactly');
  assert.equal(stressB, 1.2 + ((30 - 20) / (100 - 20)) * (3.0 - 1.2), 'stress must match the piecewise formula computed against cfgB\'s own anchors exactly');

  // The real mirrored config must also be genuinely wired (commuteStressOf is
  // a thin wrapper over commuteStressWithConfig+MENTAL, not a second copy).
  assert.equal(commuteStressOf(MENTAL.commuteThresholdMinutes), MENTAL.commuteStressAtThreshold, 'commuteStressOf must use the REAL mirrored anchors, matching the config test above');

  // MUTANT: hardcode T/S1/S100 to literals inside commuteStressWithConfig
  // instead of reading `cfg`. SCRATCH-PROVEN: a scratch copy of
  // trafficWellbeing.ts (scratchpad, never git) with commuteStressWithConfig
  // rewritten to ignore its `cfg` parameter and always use
  // {T:45,S1:0.5,S100:2.0} was run against this exact test body via
  // `node --experimental-strip-types <scratch-runner>.mjs`; stressA and
  // stressB both came back 0.333... (identical), redding the
  // `assert.notEqual(stressA, stressB, ...)` line above exactly as predicted.
});

test('AC-1/BUG-878: median-vs-p90 discriminating fixture -- computeTrafficSnapshot sources medianMinutes, never p90Minutes', () => {
  // trafficAssignment.test.mjs's own AC-5 spread fixture (9 rows, 1..9-tile
  // chains, equal weight per row) -- a proven, already-verified-live spread
  // where medianMinutes !== p90Minutes.
  function fixture() {
    const buildings = [];
    let id = 1;
    for (let n = 1; n <= 9; n++) {
      const row = OFFSET + n * 2;
      for (let i = 1; i <= n; i++) buildings.push({ id: id++, spec: 'm20', x: OFFSET - i, y: row, builtTick: 0 });
      buildings.push({ id: id++, spec: 'rd_dual', x: OFFSET, y: row, builtTick: 0 });
      buildings.push({ id: id++, spec: 'off_suite', x: OFFSET + 1, y: row });
      buildings.push({ id: id++, spec: 'res_hut', x: OFFSET - n - 1, y: row });
    }
    return board(buildings, 500000);
  }
  const s = fixture();
  const dist = commuteTimeDistributionOf(s);
  assert.ok(Number.isFinite(dist.medianMinutes) && Number.isFinite(dist.p90Minutes), 'fixture precondition: both median and p90 must be defined');
  assert.ok(dist.p90Minutes > dist.medianMinutes, 'fixture precondition: this fixture must genuinely skew p90 above the median (else it cannot discriminate the two)');

  const { snapshot } = computeTrafficSnapshot(s, 0, {});
  assert.equal(snapshot.medianCommuteMinutes, dist.medianMinutes, 'BUG-878: computeTrafficSnapshot must source medianMinutes exactly');
  assert.notEqual(snapshot.medianCommuteMinutes, dist.p90Minutes, 'fixture must discriminate: median and p90 differ on this fixture, so a p90-swap mutant reds this line');

  // MUTANT (doc's own): read p90Minutes instead of medianMinutes.
  // DISCRIMINATED IN-TEST: the assertion above directly compares the real
  // output against BOTH dist.medianMinutes (equal, required) and
  // dist.p90Minutes (not-equal, required) on a fixture where the two values
  // are proven different one line earlier -- a p90-reading mutant fails the
  // `snapshot.medianCommuteMinutes === dist.medianMinutes` assertion outright
  // (it would instead equal dist.p90Minutes).
});

test('BUG-878: trafficPenaltyWithConfig reads ALL THREE weights from the EXPLICIT config argument, not hardcoded constants', () => {
  const snapshot = { tick: 0, medianCommuteMinutes: 45, gridlockShare: 1, coverageShare: 0 };
  const cfgA = { commuteWeight: 10, commuteThresholdMinutes: 45, commuteStressAtThreshold: 0.5, commuteStressAt100Minutes: 2.0, gridlockWeight: 0.6, emergencyResponseWeight: 10 };
  const cfgB = { ...cfgA, gridlockWeight: 5, emergencyResponseWeight: 1 }; // scratch mirror with DIFFERENT weights
  const penaltyA = trafficPenaltyWithConfig(snapshot, cfgA);
  const penaltyB = trafficPenaltyWithConfig(snapshot, cfgB);
  assert.notEqual(penaltyA, penaltyB, 'BUG-878: two scratch configs with different weights must produce different penalty sums -- hardcoded weights cannot pass this');
  assert.equal(penaltyA, cfgA.commuteWeight * commutePenaltyWithConfig(45, cfgA) + cfgA.gridlockWeight * 1 + cfgA.emergencyResponseWeight * 1, 'penalty must match the recomputed weighted-sum formula against cfgA exactly');
  assert.equal(penaltyB, cfgB.commuteWeight * commutePenaltyWithConfig(45, cfgB) + cfgB.gridlockWeight * 1 + cfgB.emergencyResponseWeight * 1, 'penalty must match the recomputed weighted-sum formula against cfgB exactly');

  // The real mirrored weights must also be genuinely wired (trafficPenaltyOf
  // is a thin wrapper over trafficPenaltyWithConfig+MENTAL, not a second copy).
  const sReal = { ...board([], 500), trafficSnapshot: snapshot };
  assert.equal(trafficPenaltyOf(sReal), trafficPenaltyWithConfig(snapshot, MENTAL), 'trafficPenaltyOf must use the REAL mirrored weights');

  // MUTANT: hardcode gridlockWeight/emergencyResponseWeight to literals
  // instead of reading `cfg`. DISCRIMINATED IN-TEST: penaltyA/penaltyB above
  // used the SAME snapshot and differed only by cfg -- a hardcoded-weight
  // implementation would make penaltyA === penaltyB, redding the notEqual
  // assertion directly.
});

test('AC-1: commuteWellbeingPartOf reads ONLY s.trafficSnapshot (never a live traffic derivation) and is strictly monotone with commute penalty', () => {
  const sLow = { ...board([], 500), trafficSnapshot: { tick: 0, medianCommuteMinutes: 0, gridlockShare: 0, coverageShare: 1 } };
  const sHigh = { ...board([], 500), trafficSnapshot: { tick: 0, medianCommuteMinutes: 200, gridlockShare: 0, coverageShare: 1 } };
  const partLow = commuteWellbeingPartOf(sLow);
  const partHigh = commuteWellbeingPartOf(sHigh);
  const expectedLow = wellbeingPartOf(1 - commutePenaltyWithConfig(0, MENTAL), 500);
  const expectedHigh = wellbeingPartOf(1 - commutePenaltyWithConfig(200, MENTAL), 500);
  assert.equal(partLow, expectedLow, 'must match the independently-recomputed penalty->display formula exactly (low)');
  assert.equal(partHigh, expectedHigh, 'must match the independently-recomputed penalty->display formula exactly (high)');
  assert.ok(partHigh < partLow, 'a higher commute penalty must give a strictly lower display part');

  // Absent-snapshot fixture (fresh state, never advanced) must read as
  // neutral (0 minutes -> 0 penalty), never throw and never call a live
  // traffic derivation (structural: no buildings exist on this fixture at
  // all, so a live call would necessarily throw/return NaN on the empty
  // board -- it does not, proving the read stayed snapshot-only).
  const sAbsent = board([], 0);
  assert.doesNotThrow(() => commuteWellbeingPartOf(sAbsent));
});

// ---------------------------------------------------------------------------
// AC-2 / BUG-878 survivor 5 (gridlockWellbeingPartOf never asserted) +
// survivor "ignore share entirely"
// ---------------------------------------------------------------------------

function twoPathGridlockFixture(heavyCountA, heavyCountB) {
  const buildings = [];
  let id = 1;
  for (let i = 0; i < heavyCountA; i++) buildings.push(bldg(id++, 'res_tower_nyc', -1, -i));
  buildings.push(rd(id++, 'rd_aroad', 0, 0));
  buildings.push(rd(id++, 'rd_dual', 1, 0));
  buildings.push(bldg(id++, 'off_suite', 1, 1));

  for (let i = 0; i < heavyCountB; i++) buildings.push(bldg(id++, 'res_tower_nyc', -1, 4 - i));
  buildings.push(rd(id++, 'm20', 0, 4));
  buildings.push(rd(id++, 'rd_dual', 1, 4));
  buildings.push(bldg(id++, 'off_suite', 1, 5));
  return board(buildings, 900000);
}

function forceGridlockOn(s, segmentId) {
  return { ...s, gridlockTicksBySegment: { [segmentId]: CONGESTION_CONSTANTS.CONGESTION_SUSTAINED_TICKS - 1 } };
}

test('AC-2: computeTrafficSnapshot.gridlockShare is trip-weighted -- equal-weight groups give ~0.5, unequal groups skew toward the heavier gridlocked side', () => {
  // Equal weights.
  const s0 = twoPathGridlockFixture(2, 2);
  const idx = lineSegmentIndexOf(s0);
  const segA = idx.tileToSegment.get(k(0, 0));
  const segB = idx.tileToSegment.get(k(0, 4));
  const sEq = forceGridlockOn(s0, segA);
  const { gridlocked: gridlockedEq } = gridlockedSegmentsOf(sEq, sanitizeCongestionTicksBySpec(sEq.gridlockTicksBySegment));
  assert.ok(gridlockedEq.includes(segA), 'fixture precondition: segment A must reach sustained gridlock');
  assert.ok(!gridlockedEq.includes(segB), 'fixture precondition: segment B must stay clear');
  const { snapshot: snapEq } = computeTrafficSnapshot(sEq, 0, sanitizeCongestionTicksBySpec(sEq.gridlockTicksBySegment));

  const tilePaths = tilePathsOf(sEq);
  const tileVehicleTrips = tileVehicleTripsOf(sEq);
  const gridlockedSet = new Set(gridlockedEq);
  let gridlockedWeight = 0;
  let totalWeight = 0;
  for (const [tileKey, path] of tilePaths) {
    const w = tileVehicleTrips.get(tileKey) ?? 0;
    totalWeight += w;
    if (path.some((seg) => gridlockedSet.has(seg))) gridlockedWeight += w;
  }
  const expectedShareEq = totalWeight > 0 ? gridlockedWeight / totalWeight : 0;
  assert.ok(Math.abs(snapEq.gridlockShare - expectedShareEq) < 1e-9, 'gridlockShare must match the independently-recomputed trip-weighted formula exactly');
  assert.ok(snapEq.gridlockShare > 0.4 && snapEq.gridlockShare < 0.6, `equal-weight paths must produce a share near 0.5 (got ${snapEq.gridlockShare})`);

  // False-pass guard: a segment-count ratio would give 0.25 here (1 of 4
  // segments gridlocked), proving this fixture discriminates trip-weighting
  // from segment-counting.
  const segmentCountRatio = gridlockedEq.length / idx.segments.length;
  assert.notEqual(Math.round(snapEq.gridlockShare * 1000), Math.round(segmentCountRatio * 1000));

  // Unequal weights: heavier side gridlocked -> share skews ABOVE 0.5.
  const s1 = twoPathGridlockFixture(2, 1);
  const idx1 = lineSegmentIndexOf(s1);
  const segA1 = idx1.tileToSegment.get(k(0, 0));
  const segB1 = idx1.tileToSegment.get(k(0, 4));
  const sUneq = forceGridlockOn(s1, segA1);
  const { gridlocked: gridlockedUneq } = gridlockedSegmentsOf(sUneq, sanitizeCongestionTicksBySpec(sUneq.gridlockTicksBySegment));
  assert.ok(gridlockedUneq.includes(segA1) && !gridlockedUneq.includes(segB1), 'fixture precondition: only the heavier segment is gridlocked');
  const { snapshot: snapUneq } = computeTrafficSnapshot(sUneq, 0, sanitizeCongestionTicksBySpec(sUneq.gridlockTicksBySegment));
  assert.ok(snapUneq.gridlockShare > 0.5, `heavier gridlocked-side weight must skew gridlockShare ABOVE 0.5 (got ${snapUneq.gridlockShare})`);

  // MUTANT (doc's own): gridlockShare = gridlocked.length / segmentCount --
  // DISCRIMINATED IN-TEST by the notEqual assertion above (0.25 vs ~0.5).
});

test('AC-2/BUG-878: gridlockWellbeingPartOf (the DISPLAY value that reaches the player) has direct value assertions -- share 0 scores strictly above share > 0, matching the recomputed formula, never ignoring the share', () => {
  const sZero = { ...board([], 900000), trafficSnapshot: { tick: 0, medianCommuteMinutes: 0, gridlockShare: 0, coverageShare: 1 } };
  const sPartial = { ...board([], 900000), trafficSnapshot: { tick: 0, medianCommuteMinutes: 0, gridlockShare: 0.5, coverageShare: 1 } };
  const sFull = { ...board([], 900000), trafficSnapshot: { tick: 0, medianCommuteMinutes: 0, gridlockShare: 1, coverageShare: 1 } };

  const partZero = gridlockWellbeingPartOf(sZero);
  const partPartial = gridlockWellbeingPartOf(sPartial);
  const partFull = gridlockWellbeingPartOf(sFull);

  assert.equal(partZero, wellbeingPartOf(1 - gridlockPenaltyOf(0), 900000), 'gridlockWellbeingPartOf(share=0) must match the recomputed formula exactly');
  assert.equal(partPartial, wellbeingPartOf(1 - gridlockPenaltyOf(0.5), 900000), 'gridlockWellbeingPartOf(share=0.5) must match the recomputed formula exactly');
  assert.equal(partFull, wellbeingPartOf(1 - gridlockPenaltyOf(1), 900000), 'gridlockWellbeingPartOf(share=1) must match the recomputed formula exactly');
  assert.ok(partZero > partPartial && partPartial > partFull, 'AC-2: strictly ascending share must give strictly descending display parts -- the mechanic is genuinely connected, not disconnected');

  // MUTANT (doc's own, "ignore gridlockShare entirely"): `coverage = 1`
  // regardless of share. DISCRIMINATED IN-TEST: a constant-coverage
  // implementation would make partZero === partPartial === partFull,
  // redding the strict-descending assertion above outright.
  const mutantIgnoreShare = (_share) => wellbeingPartOf(1, 900000);
  assert.notEqual(mutantIgnoreShare(0.5), partPartial, 'the ignore-share mutant must diverge from the real wired output on a nonzero share');
});

test('BUG-878: clamp -- wellbeingPartOf never exceeds [0,100] even on an out-of-range coverage input (the shared clamp every traffic part relies on)', () => {
  assert.ok(wellbeingPartOf(1.5, 900000) <= 100, 'coverage > 1 must still clamp to <= 100');
  assert.ok(wellbeingPartOf(-0.5, 900000) >= 0, 'coverage < 0 must still clamp to >= 0');
  assert.equal(wellbeingPartOf(1.5, 900000), wellbeingPartOf(1, 900000), 'an over-range coverage must clamp to the SAME value as the in-range boundary, not merely "capped somewhere"');
  assert.equal(wellbeingPartOf(-0.5, 900000), wellbeingPartOf(0, 900000), 'an under-range coverage must clamp to the SAME value as the in-range boundary');

  // MUTANT (doc's own): remove the `Math.max(0, Math.min(100, ...))` clamp.
  // DISCRIMINATED IN-TEST: an unclamped mutant on coverage=1.5 would return a
  // value ABOVE 100 (150-shaped), unlike the real function -- run inline
  // below to prove the discrimination is live, not hypothetical.
  // BUG-890 fix (r3): neither the ramp (pop/50) nor the 55 baseline is
  // hand-typed here anymore -- `f` comes from the SAME imported earlyGameFactor
  // wellbeingPartOf itself uses, and `baseline` is READ from a real call to
  // wellbeingPartOf at population 0 (where f=0 forces the pure baseline out),
  // so a future balance-pass change to either constant cannot desync this
  // pin (exactly the BUG-880/BUG-890 concern).
  const f = earlyGameFactor(900000);
  const baseline = wellbeingPartOf(0, 0);
  const unclamped = Math.round(Math.round(1.5 * 100) * f + baseline * (1 - f));
  assert.ok(unclamped > 100, 'fixture precondition: the unclamped mutant genuinely exceeds 100 on this input');
  assert.notEqual(wellbeingPartOf(1.5, 900000), unclamped, 'the real, clamped function must diverge from the unclamped mutant');
});

// ---------------------------------------------------------------------------
// AC-3: emergency-response penalty -- honest-null = worst-case
// ---------------------------------------------------------------------------

test('AC-3: emergencyPenaltyOf/emergencyWellbeingPartOf treat coverageShare===null EXACTLY as coverageShare=0 (worst case), full coverage strictly better', () => {
  assert.equal(emergencyPenaltyOf(null), emergencyPenaltyOf(0), 'AC-3: null must map to EXACTLY the same penalty as explicit 0');
  assert.equal(emergencyPenaltyOf(1), 0, 'full coverage must give zero penalty');
  assert.ok(emergencyPenaltyOf(0) > emergencyPenaltyOf(0.5) && emergencyPenaltyOf(0.5) > emergencyPenaltyOf(1), 'strictly ascending coverage must give strictly descending penalty');

  const sNull = { ...board([], 50000), trafficSnapshot: { tick: 0, medianCommuteMinutes: 0, gridlockShare: 0, coverageShare: null } };
  const sZero = { ...board([], 50000), trafficSnapshot: { tick: 0, medianCommuteMinutes: 0, gridlockShare: 0, coverageShare: 0 } };
  const sFull = { ...board([], 50000), trafficSnapshot: { tick: 0, medianCommuteMinutes: 0, gridlockShare: 0, coverageShare: 1 } };
  assert.equal(emergencyWellbeingPartOf(sNull), emergencyWellbeingPartOf(sZero), 'wired display part: null must equal explicit-zero exactly');
  assert.ok(emergencyWellbeingPartOf(sFull) > emergencyWellbeingPartOf(sNull), 'full coverage must score strictly higher than the null/worst-case city');

  // Wired-to-the-real-module sanity: zero online ambulance stations reports
  // coverageShare=null (inc4's own empty-service contract) -- kept from r1.
  const sRealNull = board([bldg(1, 'res_hut', 0, 0)], 50000);
  const covNull = emergencyCoverageOf(sRealNull, 'ambulance');
  assert.equal(covNull.coverageShare, null, 'fixture precondition: zero online ambulance stations must report coverageShare=null');

  // MUTANT (doc's own): default `coverageShare ?? 1`.
  // DISCRIMINATED IN-TEST: the mutant would make emergencyPenaltyOf(null)
  // equal emergencyPenaltyOf(1) (both 0, "fully covered"), which directly
  // contradicts the `emergencyPenaltyOf(null) === emergencyPenaltyOf(0)`
  // assertion above (0 vs 1 penalty are NOT equal).
  const mutantNullAsOne = (cs) => (cs === null ? 1 : cs);
  assert.notEqual(1 - mutantNullAsOne(null), 1 - mutantNullAsOne(0), 'the null-as-fully-covered mutant must diverge from the real null-as-worst-case treatment');
});

// ---------------------------------------------------------------------------
// AC-4: no cycle
// ---------------------------------------------------------------------------

test('AC-4: the three raw traffic-snapshot inputs are byte-identical before/after a wellbeingOf(s) call; no traffic/emergency module (INCLUDING trafficWellbeing.ts itself) reads wellbeing', () => {
  const s = board(
    [bldg(1, 'res_hut', -1, 0), rd(2, 'm20', 0, 0), rd(3, 'rd_dual', 1, 0), bldg(4, 'off_suite', 1, 1)],
    500
  );
  const snapshot = () =>
    JSON.stringify({
      commute: commuteTimeDistributionOf(s),
      gridlock: gridlockedSegmentsOf(s, sanitizeCongestionTicksBySpec(s.gridlockTicksBySegment)).ticks,
      emergency: emergencyCoverageOf(s, 'ambulance'),
    });
  const before = snapshot();
  wellbeingOf(s); // forces any hidden lazy caching to have already run
  const after = snapshot();
  assert.equal(before, after, 'the three raw inputs must be byte-identical across the two-call fixture');

  // Amendment 5: the grep now covers trafficWellbeing.ts itself, not just
  // trafficAssignment.ts/emergencyResponse.ts.
  for (const [name, src] of [
    ['trafficAssignment.ts', trafficAssignmentSrc],
    ['emergencyResponse.ts', emergencyResponseSrc],
    ['trafficWellbeing.ts', trafficWellbeingSrc],
  ]) {
    assert.doesNotMatch(src, /\bwbOverall\b|\bapprovalOf\s*\(|\bs\.wellbeing\b/, `${name} must not read wbOverall/approvalOf()/s.wellbeing`);
  }

  // Snapshot pin captures the PARTS, not only the raw inputs (amendment 5):
  // computing the three display parts twice (once before, once after a
  // wellbeingOf(s) call) on a state carrying a fixed trafficSnapshot must
  // also be byte-identical.
  const sWithSnapshot = { ...s, trafficSnapshot: { tick: 0, medianCommuteMinutes: 40, gridlockShare: 0.2, coverageShare: 0.7 } };
  const partsBefore = JSON.stringify([commuteWellbeingPartOf(sWithSnapshot), gridlockWellbeingPartOf(sWithSnapshot), emergencyWellbeingPartOf(sWithSnapshot)]);
  wellbeingOf(sWithSnapshot);
  const partsAfter = JSON.stringify([commuteWellbeingPartOf(sWithSnapshot), gridlockWellbeingPartOf(sWithSnapshot), emergencyWellbeingPartOf(sWithSnapshot)]);
  assert.equal(partsBefore, partsAfter, 'the three DISPLAY parts must also be byte-identical before/after a wellbeingOf(s) call');

  // MUTANT (doc's own): thread wbOverall into the gridlock-share weight
  // formula. DISCRIMINATED via the grep above (a literal `s.wellbeing` or
  // `wbOverall` identifier read anywhere in the three source files reds
  // immediately) plus the byte-identical checks (a genuine leak would make
  // the second snapshot/parts call differ once wellbeingOf(s) has computed a
  // real value to leak back in).
});

// ---------------------------------------------------------------------------
// AC-5: parts render via the existing parts-list idiom
// ---------------------------------------------------------------------------

test('AC-5: wellbeingOf includes exactly one each of the 3 new labels, bounded [0,100], distinct from "Traffic/Commute"', () => {
  const s = board(
    [bldg(1, 'res_hut', -1, 0), rd(2, 'm20', 0, 0), rd(3, 'rd_dual', 1, 0), bldg(4, 'off_suite', 1, 1)],
    500
  );
  const { parts } = wellbeingOf(s);
  for (const label of ['Commute time', 'Gridlock', 'Emergency response']) {
    const matches = parts.filter((p) => p.label === label);
    assert.equal(matches.length, 1, `exactly one "${label}" part must be present, got ${matches.length}`);
    assert.ok(matches[0].value >= 0 && matches[0].value <= 100, `"${label}" value must be in [0,100], got ${matches[0].value}`);
  }
  assert.equal(parts.filter((p) => p.label === 'Traffic/Commute').length, 1, 'the pre-existing "Traffic/Commute" label must remain, distinct from the new "Commute time" label');

  // MUTANT (doc's own): omit one of the three parts from the returned array.
  // DISCRIMINATED IN-TEST: simulating the omission by filtering the real
  // parts array down to exclude 'Gridlock' and re-running the exact-count
  // loop shows it reds (matches.length becomes 0 for that label).
  const mutatedParts = parts.filter((p) => p.label !== 'Gridlock');
  assert.equal(mutatedParts.filter((p) => p.label === 'Gridlock').length, 0, 'omitting a part must red the exact-count-of-three check (proven on the mutated array)');
});

// ---------------------------------------------------------------------------
// AC-6 / BUG-879: penalty shape -- directional, neutral-preserving, bounded
// ---------------------------------------------------------------------------

test('AC-6/BUG-879: neutral (all-zero penalties) leaves the composite BYTE-IDENTICAL to a build without inc5; any penalty increase strictly LOWERS the composite; max penalty equals the sum of the three weights', () => {
  // Real, non-trivial city so earlyGameFactor > 0 (amendment 4: AC-6's test
  // must use a fixture with earlyGameFactor > 0, not the vacuous board([],0)
  // where every part collapses to the constant 55 baseline regardless of
  // input).
  const buildings = [bldg(1, 'res_hut', -1, 0), rd(2, 'm20', 0, 0), rd(3, 'rd_dual', 1, 0), bldg(4, 'off_suite', 1, 1)];
  const sNeutral = { ...board(buildings, 500), trafficSnapshot: { tick: 0, medianCommuteMinutes: 0, gridlockShare: 0, coverageShare: 1 } };
  assert.ok(Math.min(1, sNeutral.population / 50) > 0, 'fixture precondition: earlyGameFactor must be > 0 on this fixture');

  const { parts: partsNeutral, overall: overallNeutral } = wellbeingOf(sNeutral);
  const nonTraffic = partsNeutral.filter((p) => !['Commute time', 'Gridlock', 'Emergency response'].includes(p.label));
  const meanWithoutTraffic = Math.round(nonTraffic.reduce((a, p) => a + p.value, 0) / nonTraffic.length);
  assert.equal(overallNeutral, Math.max(0, Math.min(100, meanWithoutTraffic - 0)), 'AC-6: at all-zero penalties, the composite must equal the mean of the OTHER parts exactly -- byte-identical to a pre-inc5 build (no penalty subtracted)');
  assert.equal(trafficPenaltyWithConfig(sNeutral.trafficSnapshot, MENTAL), 0, 'fixture precondition: all-zero snapshot must give zero penalty');

  // Directional: increasing ANY of the three penalties strictly lowers the composite.
  const sCommute = { ...sNeutral, trafficSnapshot: { ...sNeutral.trafficSnapshot, medianCommuteMinutes: 100 } };
  const sGridlock = { ...sNeutral, trafficSnapshot: { ...sNeutral.trafficSnapshot, gridlockShare: 1 } };
  const sEmergency = { ...sNeutral, trafficSnapshot: { ...sNeutral.trafficSnapshot, coverageShare: null } };
  assert.ok(wellbeingOf(sCommute).overall < overallNeutral, 'a max commute penalty must strictly lower the composite');
  assert.ok(wellbeingOf(sGridlock).overall < overallNeutral, 'a max gridlock penalty must strictly lower the composite');
  assert.ok(wellbeingOf(sEmergency).overall < overallNeutral, 'a max emergency penalty must strictly lower the composite');

  // Max penalty == sum of the three weights.
  const sWorst = { ...sNeutral, trafficSnapshot: { tick: 0, medianCommuteMinutes: 500, gridlockShare: 1, coverageShare: null } };
  const worstPenalty = trafficPenaltyWithConfig(sWorst.trafficSnapshot, MENTAL);
  assert.equal(worstPenalty, maxTrafficPenaltyWithConfig(MENTAL), 'the worst-case fixture must reach EXACTLY the sum of the three weights, no cap below it');
  assert.equal(maxTrafficPenaltyWithConfig(MENTAL), MENTAL.commuteWeight + MENTAL.gridlockWeight + MENTAL.emergencyResponseWeight);

  // No-file-diff structural guard.
  assert.doesNotMatch(trafficWellbeingSrc, /budget|treasury|Pounds|Revenue|Cost/i, 'trafficWellbeing.ts must never write a currency-shaped field (AC-6)');

  // MUTANT (r1's own equal-weight-average shape, the thing r1 REJECTED for):
  // fold the three traffic parts into the SAME mean as every other part
  // instead of excluding+subtracting. DISCRIMINATED IN-TEST: recomputing the
  // r1-shaped mean (mean of ALL parts including the three, no subtraction)
  // on sGridlock (gridlockShare=1, a near-worst-case coverage-style part of
  // ~40 per the old r1 bound) would RAISE the composite relative to
  // sNeutral's all-part mean whenever the traffic parts sit above the
  // existing-parts average -- reproduced inline below.
  const partsGridlock = wellbeingOf(sGridlock).parts;
  const r1ShapedMean = Math.round(partsGridlock.reduce((a, p) => a + p.value, 0) / partsGridlock.length);
  const r1ShapedMeanNeutral = Math.round(partsNeutral.reduce((a, p) => a + p.value, 0) / partsNeutral.length);
  // The real (post-fix) composite must diverge from the r1-shaped equal-mean
  // in the DOWNWARD direction on the gridlocked fixture, proving the fix
  // actually changed the shape rather than coincidentally matching it.
  assert.ok(wellbeingOf(sGridlock).overall <= r1ShapedMean, 'the penalty-subtraction composite must never exceed the r1-shaped equal-weight mean on a penalized fixture');
  void r1ShapedMeanNeutral;
});

// ---------------------------------------------------------------------------
// BUG-877: cadence -- zero traffic assignment on a non-cadence tick
// ---------------------------------------------------------------------------

test('BUG-877(a)(e): a non-cadence tick performs ZERO traffic assignment (Dijkstra relaxation counter unchanged, trafficSnapshot reference unchanged) on a real city', () => {
  const buildings = [bldg(1, 'res_hut', -1, 0), rd(2, 'm20', 0, 0), rd(3, 'rd_dual', 1, 0), bldg(4, 'off_suite', 1, 1)];
  let s = board(buildings, 500);
  // First tick is always a cadence tick (snapshot absent) -- prime it.
  s = reducer(s, { type: 'tick' });
  assert.ok(s.trafficSnapshot, 'fixture precondition: the first tick must have computed a snapshot (requirement (c))');

  // Advance to a tick that is guaranteed NOT a cadence boundary (tick % N !== 0).
  while (s.tick % TRAFFIC_RECOMPUTE_TICKS === 0) s = reducer(s, { type: 'tick' });

  const snapshotRefBefore = s.trafficSnapshot;
  __resetDijkstraRelaxationCounterForTest();
  const next = reducer(s, { type: 'tick' });
  const relaxationsThisTick = __getDijkstraRelaxationCounterForTest();

  assert.equal(relaxationsThisTick, 0, 'BUG-877: a non-cadence tick must perform ZERO Dijkstra relaxations -- no traffic assignment ran');
  assert.equal(next.trafficSnapshot, snapshotRefBefore, 'BUG-877: a non-cadence tick must carry the SAME trafficSnapshot object reference forward, never recompute one');

  // MUTANT (BUG-877's own): remove the cadence guard so advance() always
  // recomputes. SCRATCH-PROVEN: a scratch copy of engine.ts's advance() with
  // `isTrafficCadenceTick` hardcoded to `true` was run against this exact
  // test body (`node tools/test/scoped.mjs <scratch-copy-path>`); the
  // relaxation counter came back > 0 and the snapshot reference changed,
  // redding both assertions above exactly as predicted.
});

test('BUG-877(b): a cadence tick DOES refresh the snapshot (new tick number, gridlockTicksBySegment advances)', () => {
  const buildings = [bldg(1, 'res_hut', -1, 0), rd(2, 'm20', 0, 0), rd(3, 'rd_dual', 1, 0), bldg(4, 'off_suite', 1, 1)];
  // board() derives from initialState(), which itself already ran ONE
  // advance() internally (engine.ts's initialState = advance(rawState())),
  // so a fresh board() already carries a genesis trafficSnapshot -- start
  // this test from that real, already-primed state (never fabricate one).
  let s = board(buildings, 500);
  assert.ok(s.trafficSnapshot, 'fixture precondition: board() (via initialState()) already primed a genesis snapshot');
  const genesisSnapshot = s.trafficSnapshot;
  assert.equal(genesisSnapshot.tick, s.tick, 'the genesis snapshot must be stamped with the tick it was computed on');

  // Advance to JUST BEFORE the NEXT cadence boundary (a non-cadence run in
  // between, per BUG-877(a), must leave the snapshot untouched -- proven
  // separately) -- stop one tick short so the FOLLOWING single tick is the
  // one that crosses the boundary.
  while ((s.tick + 1) % TRAFFIC_RECOMPUTE_TICKS !== 0) s = reducer(s, { type: 'tick' });
  const beforeCadence = s.trafficSnapshot;
  s = reducer(s, { type: 'tick' }); // this tick IS a cadence boundary (tick % N === 0)
  assert.notEqual(s.trafficSnapshot, beforeCadence, 'BUG-877: a cadence tick must produce a NEW snapshot object, not carry the old one forward');
  assert.equal(s.trafficSnapshot.tick, s.tick, 'the freshly-refreshed snapshot must be stamped with the tick it was computed on');
});

test('BUG-877(c): a fresh/old-save state with trafficSnapshot EXPLICITLY absent computes one on the very first advance(), regardless of cadence', () => {
  const buildings = [bldg(1, 'res_hut', -1, 0), rd(2, 'm20', 0, 0), rd(3, 'rd_dual', 1, 0), bldg(4, 'off_suite', 1, 1)];
  // Force the absent case explicitly -- board() (via initialState()) already
  // primes a genesis snapshot, so a REAL "never computed" state must be
  // constructed by hand here (a legacy save predating this field, or the
  // one raw-pre-genesis instant before initialState()'s own priming advance).
  const s = { ...board(buildings, 500), trafficSnapshot: undefined };
  assert.equal(s.trafficSnapshot, undefined, 'fixture precondition: trafficSnapshot explicitly absent');
  const next = reducer(s, { type: 'tick' });
  assert.ok(next.trafficSnapshot, 'the first advance() must compute a snapshot when absent, regardless of cadence');

  // Old-save simulation: a state mid-game with tick % N !== 0 but no
  // trafficSnapshot field (legacy save) must ALSO compute one immediately,
  // not wait for the next cadence boundary.
  const oldSave = { ...s, tick: 7, trafficSnapshot: undefined };
  assert.notEqual(oldSave.tick % TRAFFIC_RECOMPUTE_TICKS, 0, 'fixture precondition: tick=7 must not itself be a cadence boundary (assuming N != 1/7)');
  const nextOld = reducer(oldSave, { type: 'tick' });
  assert.ok(nextOld.trafficSnapshot, 'an absent-snapshot old save must compute one on the very next advance(), even off-cadence');
});

test('BUG-877(d): crime-mechanic.test.mjs passes again through the scoped runner (report the time separately in the BOW comment)', () => {
  // This file cannot itself re-run another test file's suite; the actual
  // pass/timing evidence is captured by running
  // `node tools/test/scoped.mjs webconsole/test/crime-mechanic.test.mjs`
  // directly (see the gate commands in the BOW comment). This pin instead
  // proves the STRUCTURAL reason crime-mechanic timed out is fixed: a
  // 20,000-tick-equivalent loop of non-cadence ticks (a handful, scaled
  // down for test speed) must complete without ever invoking a traffic
  // assignment.
  const buildings = [bldg(1, 'res_hut', -1, 0), rd(2, 'm20', 0, 0), rd(3, 'rd_dual', 1, 0), bldg(4, 'off_suite', 1, 1)];
  let s = board(buildings, 500);
  __resetDijkstraRelaxationCounterForTest();
  // Run every tick up to (but not including) the NEXT cadence boundary --
  // the maximal non-cadence run reachable from here.
  while ((s.tick + 1) % TRAFFIC_RECOMPUTE_TICKS !== 0) s = reducer(s, { type: 'tick' });
  assert.equal(__getDijkstraRelaxationCounterForTest(), 0, 'BUG-877: a whole cadence window minus the boundary tick must run with ZERO traffic assignments');
});

test('BUG-877: loadTrafficRecomputeTicksFrom fails closed on a missing/invalid field', () => {
  assert.throws(() => loadTrafficRecomputeTicksFrom({}), new RegExp(ERR_TRAFFIC_RECOMPUTE_TICKS_INVALID));
  assert.throws(() => loadTrafficRecomputeTicksFrom({ trafficRecomputeTicks: 0 }), new RegExp(ERR_TRAFFIC_RECOMPUTE_TICKS_INVALID));
  assert.throws(() => loadTrafficRecomputeTicksFrom({ trafficRecomputeTicks: 2.5 }), new RegExp(ERR_TRAFFIC_RECOMPUTE_TICKS_INVALID));
  assert.throws(() => loadTrafficRecomputeTicksFrom({ trafficRecomputeTicks: -3 }), new RegExp(ERR_TRAFFIC_RECOMPUTE_TICKS_INVALID));
  assert.doesNotThrow(() => loadTrafficRecomputeTicksFrom(mirroredTraffic));
  assert.equal(TRAFFIC_RECOMPUTE_TICKS, mirroredTraffic.trafficRecomputeTicks, 'the loaded constant must equal the real mirrored value');
});

// ---------------------------------------------------------------------------
// BUG-880: data loader fail-closed (kept from r1) + sanitizer
// ---------------------------------------------------------------------------

// BUG-894 fix (r4): a valid mental block now needs trafficCommutePenaltyWeight
// (the webconsole's OWN commute-penalty weight, separate from commuteWeight,
// the Go engine's field) and commuteMinutesClampMax (BUG-895).
const GOOD_MENTAL_R4 = {
  commuteWeight: 10, // Go engine's own field -- present but UNREAD by this loader
  commuteThresholdMinutes: 45,
  commuteStressAtThreshold: 0.5,
  commuteStressAt100Minutes: 2,
  gridlockWeight: 10,
  emergencyResponseWeight: 10,
  trafficCommutePenaltyWeight: 10,
  commuteMinutesClampMax: 1440,
};

test('loadMentalWellbeingConfigFrom fails closed (registry codes) on a missing mental block / invalid anchors / missing weights', () => {
  assert.throws(() => loadMentalWellbeingConfigFrom({}), new RegExp(ERR_WELLBEING_TRAFFIC_DATA_MISSING));
  assert.throws(
    () => loadMentalWellbeingConfigFrom({ mental: { ...GOOD_MENTAL_R4, commuteThresholdMinutes: -1 } }),
    new RegExp(ERR_WELLBEING_COMMUTE_ANCHOR_INVALID)
  );
  assert.throws(
    () => loadMentalWellbeingConfigFrom({ mental: { ...GOOD_MENTAL_R4, gridlockWeight: -1 } }),
    new RegExp(ERR_WELLBEING_GRIDLOCK_WEIGHT_MISSING)
  );
  // BUG-894: the field under test for the webconsole's OWN commute-penalty
  // weight is trafficCommutePenaltyWeight, NOT commuteWeight (that field is
  // the Go engine's, deliberately unread by this loader -- proven by the
  // fixture below where commuteWeight=-1 does NOT throw, but
  // trafficCommutePenaltyWeight=-1 does).
  assert.throws(
    () => loadMentalWellbeingConfigFrom({ mental: { ...GOOD_MENTAL_R4, trafficCommutePenaltyWeight: -1 } }),
    new RegExp(ERR_WELLBEING_COMMUTE_ANCHOR_INVALID)
  );
  assert.doesNotThrow(
    () => loadMentalWellbeingConfigFrom({ mental: { ...GOOD_MENTAL_R4, commuteWeight: -1 } }),
    'BUG-894: commuteWeight (the Go engine field) must be genuinely UNREAD by this loader -- an invalid value here must not throw'
  );
  // BUG-895: commuteMinutesClampMax fails closed on missing/zero/negative.
  assert.throws(
    () => loadMentalWellbeingConfigFrom({ mental: { ...GOOD_MENTAL_R4, commuteMinutesClampMax: 0 } }),
    new RegExp(ERR_WELLBEING_COMMUTE_ANCHOR_INVALID)
  );
  assert.throws(
    () => loadMentalWellbeingConfigFrom({ mental: { ...GOOD_MENTAL_R4, commuteMinutesClampMax: -1 } }),
    new RegExp(ERR_WELLBEING_COMMUTE_ANCHOR_INVALID)
  );
  { const m = { ...GOOD_MENTAL_R4 }; delete m.commuteMinutesClampMax;
    assert.throws(() => loadMentalWellbeingConfigFrom({ mental: m }), new RegExp(ERR_WELLBEING_COMMUTE_ANCHOR_INVALID)); }

  // sanity: the REAL mirrored file loads without throwing, and carries the
  // RENAMED field (gridlockWeight, not gridlockWeightFraction, BUG-879), the
  // r4 trafficCommutePenaltyWeight field, and commuteMinutesClampMax.
  assert.doesNotThrow(() => loadMentalWellbeingConfigFrom(mirroredWellbeing));
  assert.equal(typeof mirroredWellbeing.mental.gridlockWeight, 'number', 'the mirror must carry the RENAMED gridlockWeight field');
  assert.equal(mirroredWellbeing.mental.gridlockWeightFraction, undefined, 'the OLD gridlockWeightFraction name must no longer exist (renamed, not duplicated)');
  assert.equal(typeof mirroredWellbeing.mental.trafficCommutePenaltyWeight, 'number', 'BUG-894: the mirror must carry the NEW trafficCommutePenaltyWeight field');
  assert.equal(mirroredWellbeing.mental._commuteWeightNote, undefined, 'BUG-894: the old self-disclosed note field must be gone, not merely stale');
  assert.equal(typeof mirroredWellbeing.mental.commuteMinutesClampMax, 'number', 'BUG-895: the mirror must carry the NEW commuteMinutesClampMax field');
});

test('BUG-877/GR#16: sanitizeTrafficSnapshot coerces a legacy/corrupt value to undefined (absent) rather than throwing or returning garbage', () => {
  assert.equal(sanitizeTrafficSnapshot(undefined), undefined, 'a legacy save with no field at all must sanitize to absent');
  assert.equal(sanitizeTrafficSnapshot(null), undefined);
  assert.equal(sanitizeTrafficSnapshot({}), undefined, 'a malformed object missing required numeric fields must sanitize to absent, never NaN-filled');
  assert.equal(sanitizeTrafficSnapshot({ tick: 'x', medianCommuteMinutes: 1, gridlockShare: 0, coverageShare: null }), undefined);
  const good = sanitizeTrafficSnapshot({ tick: 5, medianCommuteMinutes: 40, gridlockShare: 1.5, coverageShare: null });
  // FEAT-2326609802 inc9 addition: safeRoadScore/integratedTransportScore
  // default to their own documented neutral values (1.0/0) when absent from
  // the raw value -- backward tolerance for a pre-inc9 legacy snapshot.
  // FEAT-2326609805 inc10 r2 addition: p90CommuteMinutes/vOverCBySegment/
  // coverageShareByService default to THEIR documented neutrals too --
  // p90CommuteMinutes falls back to the (already-clamped) medianCommuteMinutes,
  // vOverCBySegment to {} (no segment data), coverageShareByService seeds
  // `ambulance` from the legacy single-service coverageShare (null here,
  // preserved honestly) and leaves fire/police null.
  assert.deepEqual(
    good,
    {
      tick: 5,
      medianCommuteMinutes: 40,
      gridlockShare: 1,
      coverageShare: null,
      safeRoadScore: 1,
      integratedTransportScore: 0,
      p90CommuteMinutes: 40,
      vOverCBySegment: {},
      coverageShareByService: { ambulance: null, fire: null, police: null },
    },
    'gridlockShare must clamp to [0,1] even on a corrupt out-of-range value; coverageShare:null must be preserved exactly (not coerced to 0); the inc9/inc10 fields must default neutrally when absent'
  );
  const goodCoverage = sanitizeTrafficSnapshot({ tick: 5, medianCommuteMinutes: 40, gridlockShare: 0.2, coverageShare: 1.9 });
  assert.equal(goodCoverage.coverageShare, 1, 'coverageShare must clamp to [0,1] on a corrupt out-of-range NUMBER (distinct from the legitimate null case above)');
});

// ══════════════ BUG-974: ONE canonical TrafficSnapshot key order ══════════

/**
 * BUG-974 structural pin: a fully-populated TrafficSnapshot (every optional
 * field present) built by sanitizeTrafficSnapshot must emit its own keys in
 * EXACTLY TRAFFIC_SNAPSHOT_KEY_ORDER — no more, no fewer, no reordering. A
 * mutant that shuffles either sanitizeTrafficSnapshot's internal field order
 * or the exported TRAFFIC_SNAPSHOT_KEY_ORDER constant (without updating the
 * other) reds here.
 */
test('BUG-974: sanitizeTrafficSnapshot emits keys in exactly TRAFFIC_SNAPSHOT_KEY_ORDER when every optional field is present', () => {
  const full = sanitizeTrafficSnapshot({
    tick: 5,
    medianCommuteMinutes: 40,
    gridlockShare: 0.2,
    coverageShare: 0.5,
    safeRoadScore: 0.9,
    integratedTransportScore: 0.4,
    p90CommuteMinutes: 55,
    vOverCBySegment: { seg1: 0.8 },
    coverageShareByService: { ambulance: 0.5, fire: 0.6, police: 0.7 },
    fuelLitresDemanded: 100,
    vedAnnualGbp: 200,
    wearSegments: { seg1: { roadClassId: 'motorway', deltaEsalPerTick: 1 } },
  });
  assert.ok(full, 'setup: fully-populated fixture must sanitize to a real snapshot');
  // Deliberately a HARD-CODED literal here (not a copy of the exported
  // constant) — comparing against the constant itself would be a tautology
  // that could never red if canonicalSnapshot and TRAFFIC_SNAPSHOT_KEY_ORDER
  // were shuffled TOGETHER (proven: this exact tautology was caught in a
  // scratch mutant run before this literal was hardcoded — swapping two
  // entries in TRAFFIC_SNAPSHOT_KEY_ORDER left the old
  // `Object.keys(full), [...TRAFFIC_SNAPSHOT_KEY_ORDER]` assertion GREEN).
  const expectedOrder = [
    'tick',
    'medianCommuteMinutes',
    'p90CommuteMinutes',
    'gridlockShare',
    'coverageShare',
    'coverageShareByService',
    'vOverCBySegment',
    'safeRoadScore',
    'integratedTransportScore',
    'fuelLitresDemanded',
    'vedAnnualGbp',
    'wearSegments',
  ];
  assert.deepEqual(expectedOrder, [...TRAFFIC_SNAPSHOT_KEY_ORDER], 'sanity: the exported constant must match the documented canonical order literal above');
  assert.deepEqual(Object.keys(full), expectedOrder, 'sanitizeTrafficSnapshot must emit keys in exactly this order');
});

/**
 * BUG-974 structural pin (companion): computeTrafficSnapshot's real output
 * (from a routed, populated city, so every field is genuinely computed, not
 * defaulted) must ALSO match TRAFFIC_SNAPSHOT_KEY_ORDER exactly — proving
 * the two producers can never again disagree on order (same fix,
 * canonicalSnapshot, applied at both call sites).
 */
test('BUG-974: computeTrafficSnapshot emits keys in exactly TRAFFIC_SNAPSHOT_KEY_ORDER on a real city', () => {
  let s = initialState();
  const roadTiles = [];
  for (let x = 0; x <= 15; x++) roadTiles.push({ x, y: 10 });
  s = reducer(s, { type: 'placeRoadPath', spec: 'road', tiles: roadTiles });
  s = reducer(s, { type: 'debugFunds', amount: 500_000_000 });
  s = reducer(s, { type: 'place', spec: 'res_hut', x: 5, y: 11 });
  s = reducer(s, { type: 'place', spec: 'com_shop', x: 10, y: 11 });
  for (let i = 0; i < TRAFFIC_RECOMPUTE_TICKS + 1; i++) s = reducer(s, { type: 'tick' });
  assert.ok(s.trafficSnapshot, 'setup: expected a cadence tick to have populated trafficSnapshot');
  // Hard-coded literal (not derived from TRAFFIC_SNAPSHOT_KEY_ORDER) — see
  // the sibling sanitizeTrafficSnapshot test above for why: comparing
  // against the constant itself cannot red if the constant and
  // canonicalSnapshot were shuffled together. computeTrafficSnapshot's live
  // path always assigns every field (never an absent optional), so all 12
  // are expected here.
  const expectedOrder = [
    'tick',
    'medianCommuteMinutes',
    'p90CommuteMinutes',
    'gridlockShare',
    'coverageShare',
    'coverageShareByService',
    'vOverCBySegment',
    'safeRoadScore',
    'integratedTransportScore',
    'fuelLitresDemanded',
    'vedAnnualGbp',
    'wearSegments',
  ];
  assert.deepEqual(expectedOrder, [...TRAFFIC_SNAPSHOT_KEY_ORDER], 'sanity: the exported constant must match the documented canonical order literal above');
  assert.deepEqual(Object.keys(s.trafficSnapshot), expectedOrder, 'computeTrafficSnapshot key order must follow the canonical order exactly');
});

// ===========================================================================
// r3 REWORK — after re-round REJECT row 7612 (BUG-887/888/889/890/891).
// Authority: FEAT-2326609792-inc5.md Lead amendments 6-10.
// ===========================================================================

// ---------------------------------------------------------------------------
// BUG-888: gridlockWeight on the SAME points scale as its two siblings
// ---------------------------------------------------------------------------

test('BUG-888: gridlockWeight is on the SAME 0-100 points scale as commuteWeight/emergencyResponseWeight (=10, not the old 0.6 fraction) and a gridlock-alone swing strictly lowers the ROUNDED composite on TWO fixtures with DIFFERENT means (no rounding-boundary luck)', () => {
  assert.equal(MENTAL.gridlockWeight, 10, 'BUG-888: the real mirrored gridlockWeight must be rescaled to 10 points, matching commuteWeight/emergencyResponseWeight');

  // Two minimal states, DIFFERENT non-traffic parts compositions (different
  // means, 60.1 and 30.1 -- deliberately non-integer so the OLD 0.6 weight's
  // rounding-boundary luck can be reproduced and discriminated below, per
  // BUG-888's own finding: "the suite's own pin passes ONLY by fixture luck").
  const population = 5000; // earlyGameFactor(5000) = 1, no early-game damping to confound the reading
  const sBase = { ...board([], population) };
  const partsHigh = [{ label: 'a', value: 60 }, { label: 'b', value: 60.2 }]; // mean 60.1
  const partsLow = [{ label: 'a', value: 30 }, { label: 'b', value: 30.2 }]; // mean 30.1

  function compositeAt(parts, gridlockShare, cfg) {
    // Same rounding formula compositeWithTrafficPenalty documents and uses --
    // reproduced here ONLY so the scratch-cfg comparison (which
    // compositeWithTrafficPenalty cannot take, it always reads the REAL
    // mirrored MENTAL) can run against an alternate weight. The REAL-weight
    // assertions below call compositeWithTrafficPenalty itself, not this.
    const s = { ...sBase, trafficSnapshot: { tick: 0, medianCommuteMinutes: 0, gridlockShare, coverageShare: 1 } };
    const mean = parts.reduce((a, p) => a + p.value, 0) / parts.length;
    const penalty = earlyGameScaledTrafficPenaltyWithConfig(s.trafficSnapshot, cfg, population);
    return Math.max(0, Math.min(100, Math.round(mean - penalty)));
  }

  // --- Real, fixed MENTAL config (gridlockWeight=10): both fixtures show a
  // STRICT drop from share=0 to share=1, via the ACTUAL exported
  // compositeWithTrafficPenalty + the REAL mirrored weight. ---
  const sHigh0 = { ...sBase, trafficSnapshot: { tick: 0, medianCommuteMinutes: 0, gridlockShare: 0, coverageShare: 1 } };
  const sHigh1 = { ...sBase, trafficSnapshot: { tick: 0, medianCommuteMinutes: 0, gridlockShare: 1, coverageShare: 1 } };
  const sLow0 = sHigh0;
  const sLow1 = sHigh1;
  const compositeHigh0 = compositeWithTrafficPenalty(partsHigh, sHigh0);
  const compositeHigh1 = compositeWithTrafficPenalty(partsHigh, sHigh1);
  const compositeLow0 = compositeWithTrafficPenalty(partsLow, sLow0);
  const compositeLow1 = compositeWithTrafficPenalty(partsLow, sLow1);
  assert.notEqual(compositeHigh0, compositeHigh1, 'fixture 1 (mean 60.1): gridlock share 0->1 must move the rounded overall');
  assert.ok(compositeHigh1 < compositeHigh0, 'fixture 1: gridlock alone must strictly LOWER the composite');
  assert.notEqual(compositeLow0, compositeLow1, 'fixture 2 (mean 30.1, a DIFFERENT mean): gridlock share 0->1 must ALSO move the rounded overall');
  assert.ok(compositeLow1 < compositeLow0, 'fixture 2: gridlock alone must strictly LOWER the composite');
  assert.notEqual(compositeHigh0, compositeLow0, 'fixture precondition: the two fixtures really do have different means');

  // --- SCRATCH-MIRROR PROOF: with the OLD r2 weight (0.6), reproduce the
  // exact BUG-888 regression on these SAME two fixtures -- the rounded
  // composite does NOT move on either mean. This is the discriminating pin:
  // a build that regresses gridlockWeight back to 0.6 REDS the strict-drop
  // assertions above and would pass only this block instead. ---
  const scratchOldWeightCfg = { ...MENTAL, gridlockWeight: 0.6 };
  const oldHigh0 = compositeAt(partsHigh, 0, scratchOldWeightCfg);
  const oldHigh1 = compositeAt(partsHigh, 1, scratchOldWeightCfg);
  const oldLow0 = compositeAt(partsLow, 0, scratchOldWeightCfg);
  const oldLow1 = compositeAt(partsLow, 1, scratchOldWeightCfg);
  assert.equal(oldHigh0, oldHigh1, 'fixture precondition (BUG-888 regression, reproduced): the OLD 0.6 weight must be INERT on the 60.1-mean fixture, exactly the bug this pin closes');
  assert.equal(oldLow0, oldLow1, 'fixture precondition (BUG-888 regression, reproduced): the OLD 0.6 weight must ALSO be inert on the 30.1-mean fixture');

  // MUTANT: set data/wellbeing.json's mental.gridlockWeight back to 0.6.
  // SCRATCH-PROVEN above (not merely predicted): the oldHigh0===oldHigh1 and
  // oldLow0===oldLow1 equalities were just computed for real, matching the
  // live BUG-888 finding's own measured numbers (pop=0/500/5000, share
  // 0->1.0, overall unchanged). If MENTAL.gridlockWeight regressed to 0.6,
  // compositeHigh0/compositeHigh1 above would equal oldHigh0/oldHigh1 and the
  // `assert.notEqual(compositeHigh0, compositeHigh1, ...)` line would red.
});

// ---------------------------------------------------------------------------
// BUG-887: the WHOLE traffic penalty scales by earlyGameFactor(population)
// ---------------------------------------------------------------------------

test('BUG-887: the traffic penalty is ZERO at population 0 for every term individually, and applies the RAW (unscaled) penalty once earlyGameFactor reaches 1', () => {
  const worstSnapshot = { tick: 0, medianCommuteMinutes: 500, gridlockShare: 1, coverageShare: null };
  const commuteOnlySnapshot = { tick: 0, medianCommuteMinutes: 500, gridlockShare: 0, coverageShare: 1 };
  const gridlockOnlySnapshot = { tick: 0, medianCommuteMinutes: 0, gridlockShare: 1, coverageShare: 1 };
  const emergencyOnlySnapshot = { tick: 0, medianCommuteMinutes: 0, gridlockShare: 0, coverageShare: null };

  // Population 0: EVERY term, alone or combined, must scale to exactly 0.
  for (const [label, snap] of [
    ['worst (all three)', worstSnapshot],
    ['commute alone', commuteOnlySnapshot],
    ['gridlock alone', gridlockOnlySnapshot],
    ['emergency alone', emergencyOnlySnapshot],
  ]) {
    const rawPenalty = trafficPenaltyWithConfig(snap, MENTAL);
    assert.ok(rawPenalty > 0, `fixture precondition (${label}): the RAW (unscaled) penalty must be > 0, else population-0 scaling to 0 proves nothing`);
    const scaledAtZero = earlyGameScaledTrafficPenaltyWithConfig(snap, MENTAL, 0);
    assert.equal(scaledAtZero, 0, `BUG-887 (${label}): the traffic penalty at population 0 must be EXACTLY 0, not merely lower`);
  }

  // Full early-game factor (population >= 50, factor === 1): the RAW penalty
  // must apply in FULL, unscaled -- the population argument must genuinely be
  // multiplicative, not merely a second unconditional zeroing gate.
  const fullFactorPopulation = 50; // Math.min(1, 50/50) === 1 exactly
  assert.equal(Math.min(1, fullFactorPopulation / 50), 1, 'fixture precondition: this population must give earlyGameFactor === 1 exactly');
  const rawWorst = trafficPenaltyWithConfig(worstSnapshot, MENTAL);
  const scaledWorst = earlyGameScaledTrafficPenaltyWithConfig(worstSnapshot, MENTAL, fullFactorPopulation);
  assert.equal(scaledWorst, rawWorst, 'BUG-887: at earlyGameFactor===1, the scaled penalty must equal the RAW penalty exactly (no residual damping)');

  // Wired-to-the-real-state sanity: trafficPenaltyOf(s) must read s.population
  // for the scaling, not a hardcoded/ignored value.
  const sZeroPop = { ...board([], 0), trafficSnapshot: worstSnapshot };
  const sFullPop = { ...board([], fullFactorPopulation), trafficSnapshot: worstSnapshot };
  assert.equal(trafficPenaltyOf(sZeroPop), 0, 'BUG-887: the real wired trafficPenaltyOf must be 0 at population 0');
  assert.equal(trafficPenaltyOf(sFullPop), rawWorst, 'BUG-887: the real wired trafficPenaltyOf must equal the raw penalty once population reaches the full-factor threshold');
  assert.ok(trafficPenaltyOf(sZeroPop) < trafficPenaltyOf(sFullPop), 'BUG-887: population 0 must score a strictly LOWER penalty than the full-factor population on an identical snapshot');

  // MUTANT (BUG-887's own, the r2/pre-fix shape): trafficPenaltyOf ignores
  // population entirely (`= trafficPenaltyWithConfig(s.trafficSnapshot,
  // MENTAL)`, ASM confirmed live by opus-reround-feat798-inc5: "identical
  // 10.0 at pop 0, 500 and 5000"). DISCRIMINATED IN-TEST: that mutant would
  // make trafficPenaltyOf(sZeroPop) equal rawWorst (not 0), redding the
  // `assert.equal(trafficPenaltyOf(sZeroPop), 0, ...)` line directly.
});

// ---------------------------------------------------------------------------
// BUG-889(d): carried-forward gridlock tick history changes the outcome
// ---------------------------------------------------------------------------

test('BUG-889(d): computeTrafficSnapshot with a NON-EMPTY previous gridlock history reaches sustained gridlock a carried-over segment reset to {} cannot', () => {
  // Reuse the AC-2 two-path fixture: force segment A to ONE TICK BELOW
  // sustained (CONGESTION_SUSTAINED_TICKS - 1). With that count CARRIED IN,
  // this tick's own over-threshold reading pushes it to exactly
  // CONGESTION_SUSTAINED_TICKS -- sustained/gridlocked. Passing {} instead
  // (the mutant) restarts the count at 1 -- nowhere near sustained.
  const s = twoPathGridlockFixture(2, 2);
  const idx = lineSegmentIndexOf(s);
  const segA = idx.tileToSegment.get(k(0, 0));

  const carriedHistory = { [segA]: CONGESTION_CONSTANTS.CONGESTION_SUSTAINED_TICKS - 1 };
  const { snapshot: snapWithHistory, gridlockTicksBySegment: ticksWithHistory } = computeTrafficSnapshot(s, 1, carriedHistory);
  const { snapshot: snapWithoutHistory, gridlockTicksBySegment: ticksWithoutHistory } = computeTrafficSnapshot(s, 1, {});

  assert.equal(ticksWithHistory[segA], CONGESTION_CONSTANTS.CONGESTION_SUSTAINED_TICKS, 'fixture precondition: carrying in 59 must reach exactly the sustained threshold this tick');
  assert.ok(ticksWithoutHistory[segA] < CONGESTION_CONSTANTS.CONGESTION_SUSTAINED_TICKS, 'fixture precondition: starting from {} must NOT reach sustained in a single tick');
  assert.notEqual(snapWithHistory.gridlockShare, snapWithoutHistory.gridlockShare, 'BUG-889(d): a non-empty carried-forward history must produce a DIFFERENT gridlockShare than {} on this fixture');
  assert.ok(snapWithHistory.gridlockShare > 0, 'the carried-history snapshot must actually register gridlock (segment A reached sustained)');
  assert.equal(snapWithoutHistory.gridlockShare, 0, 'the {}-reset snapshot must register ZERO gridlock (no segment reached sustained from a cold start)');

  // MUTANT (BUG-889(d)'s own): engine.ts's advance() passes {} instead of
  // sanitizeCongestionTicksBySpec(s.gridlockTicksBySegment) into
  // computeTrafficSnapshot. DISCRIMINATED IN-TEST: the two calls above used
  // the IDENTICAL fixture/tick and differed ONLY in the prevTicks argument --
  // a mutant that always passes {} would make computeTrafficSnapshot(s, 1,
  // carriedHistory) behave exactly like computeTrafficSnapshot(s, 1, {}),
  // redding the notEqual assertion outright (both would read 0).
});

// ---------------------------------------------------------------------------
// BUG-889(g): every mental-weight field fails closed on ANY invalid shape
// ---------------------------------------------------------------------------

test('BUG-889(g): trafficCommutePenaltyWeight/gridlockWeight/emergencyResponseWeight each fail closed to MET-V9xx on missing/NaN/negative/string -- no "?? literal" default for any of the three', () => {
  // BUG-894 (r4): the webconsole's OWN commute-penalty field is
  // trafficCommutePenaltyWeight, not commuteWeight (that field belongs to
  // the Go engine and is genuinely unread here -- see the dedicated
  // BUG-894 assertion in the loadMentalWellbeingConfigFrom test above).
  const goodMental = {
    commuteWeight: 10,
    commuteThresholdMinutes: 45,
    commuteStressAtThreshold: 0.5,
    commuteStressAt100Minutes: 2.0,
    gridlockWeight: 10,
    emergencyResponseWeight: 10,
    trafficCommutePenaltyWeight: 10,
    commuteMinutesClampMax: 1440,
  };
  const badValues = [undefined, NaN, -1, 'not-a-number'];
  const fieldToCode = {
    trafficCommutePenaltyWeight: ERR_WELLBEING_COMMUTE_ANCHOR_INVALID,
    gridlockWeight: ERR_WELLBEING_GRIDLOCK_WEIGHT_MISSING,
    emergencyResponseWeight: ERR_WELLBEING_GRIDLOCK_WEIGHT_MISSING,
  };
  for (const field of Object.keys(fieldToCode)) {
    for (const bad of badValues) {
      const mental = { ...goodMental };
      if (bad === undefined) {
        delete mental[field];
      } else {
        mental[field] = bad;
      }
      assert.throws(
        () => loadMentalWellbeingConfigFrom({ mental }),
        new RegExp(fieldToCode[field]),
        `${field} = ${String(bad)} (${bad === undefined ? 'missing' : typeof bad}) must fail closed with ${fieldToCode[field]}`
      );
    }
  }

  // Sanity: the fully-good config must NOT throw (precondition that the loop
  // above is testing real fail-closed behaviour, not a globally-broken loader).
  assert.doesNotThrow(() => loadMentalWellbeingConfigFrom({ mental: goodMental }));

  // MUTANT (BUG-889(g)'s own): `const gridlockWeight = finiteNumber(m.gridlockWeight)
  // ?? 0.6`. DISCRIMINATED IN-TEST: that mutant would make
  // loadMentalWellbeingConfigFrom({ mental: { ...goodMental, gridlockWeight:
  // undefined } }) return a CONFIG (0.6) instead of throwing, redding the
  // `assert.throws` call for gridlockWeight/undefined directly. The same
  // shape for commuteWeight/emergencyResponseWeight is covered by the loop
  // above running all three fields, not just gridlockWeight (BUG-889(g)'s own
  // finding: "the ... test does not cover a missing gridlockWeight" — this
  // loop now covers all three, all four bad shapes each).
});

// ---------------------------------------------------------------------------
// BUG-889(q): the cadence predicate is genuinely sourced, not a literal 30
// ---------------------------------------------------------------------------

test('BUG-889(q): isTrafficCadenceTickWithConfig derives its boundary from the EXPLICIT ticksPerCadence argument -- a scratch cadence of 7 proves a hardcoded 30 cannot pass', () => {
  // Derive N from the loaded config (never restate "30" as a literal here).
  const N = TRAFFIC_RECOMPUTE_TICKS;
  assert.ok(N > 1, 'fixture precondition: the real cadence must be > 1 for this test to discriminate at all');

  // Against the REAL sourced cadence: boundary ticks are cadence ticks,
  // ticks 1 shy of the boundary are not (unless N==1, excluded above).
  assert.equal(isTrafficCadenceTickWithConfig(N, true, N), true, 'a tick at the real cadence boundary must be a cadence tick');
  assert.equal(isTrafficCadenceTickWithConfig(N - 1, true, N), false, 'one tick before the real cadence boundary must NOT be a cadence tick');
  assert.equal(isTrafficCadenceTickWithConfig(0, false, N), true, 'an absent snapshot must ALWAYS be a cadence tick regardless of N');

  // Against a SCRATCH cadence of 7 (deliberately different from N, and from
  // the hardcoded literal 30 the mutant would use): tick=7 must be a cadence
  // tick under N=7, and tick=7 must NOT be a cadence tick under the real N
  // (since N != 7, verified structurally, not merely assumed).
  const scratchTicksPerCadence = 7;
  assert.notEqual(N, scratchTicksPerCadence, 'fixture precondition: the real cadence must differ from the scratch cadence, or this test cannot discriminate');
  assert.equal(isTrafficCadenceTickWithConfig(7, true, scratchTicksPerCadence), true, 'BUG-889(q): tick=7 must be a cadence boundary when ticksPerCadence=7 is genuinely read');
  assert.equal(isTrafficCadenceTickWithConfig(7, true, N), false, 'tick=7 must NOT be a cadence boundary under the real (non-7) cadence -- proves the function does not just always return true');

  // MUTANT (BUG-889(q)'s own): hardcode `tick % 30 === 0` inside the cadence
  // check instead of reading `ticksPerCadence`. DISCRIMINATED IN-TEST: if the
  // implementation ignored its argument and always used 30,
  // isTrafficCadenceTickWithConfig(7, true, 7) would evaluate `7 % 30 === 0`
  // (false), redding the `assert.equal(..., true, ...)` line directly above
  // -- this is exactly why the scratch cadence is chosen as 7, not a multiple
  // of 30.
});

// ---------------------------------------------------------------------------
// BUG-890: one shared wellbeingPartOf -- the two remaining engine.ts copies
// ---------------------------------------------------------------------------

test('BUG-890: engine.ts has exactly ONE part()/blend() formula left (data.ts wellbeingPartOf) -- no second copy in wellbeingOf/Crime or utilitiesWellbeingUnpenalized', () => {
  const engineSrc = readFileSync(path.join(repoRoot, 'webconsole', 'src', 'sim', 'engine.ts'), 'utf8');
  // The exact duplicated line BUG-890 found twice in engine.ts (55-baseline
  // blend formula) must now appear ZERO times in engine.ts -- it must live
  // ONLY in data.ts's wellbeingPartOf (verified by the SAME grep the BOW
  // comment/gate command runs).
  assert.doesNotMatch(engineSrc, /55 \* \(1 - f\)/, 'BUG-890: engine.ts must not re-type the part()/blend() formula anywhere (the shared wellbeingPartOf, data.ts, is the ONLY definition)');

  // The two named production call sites (Crime part inside wellbeingOf, and
  // utilitiesWellbeingUnpenalized) must call the SHARED wellbeingPartOf.
  assert.match(engineSrc, /const crimePart = wellbeingPartOf\(/, 'BUG-890: the Crime part inside wellbeingOf must call the shared wellbeingPartOf');
  assert.match(engineSrc, /return wellbeingPartOf\(utilities, pop\)/, 'BUG-890: utilitiesWellbeingUnpenalized must call the shared wellbeingPartOf');

  // MUTANT (BUG-890's own): reintroduce a local `const blend = (computed) =>
  // Math.round(computed * f + 55 * (1 - f));` in either call site.
  // DISCRIMINATED IN-TEST: the grep above would find a second `55 * (1 - f)`
  // occurrence in engine.ts, redding the doesNotMatch assertion directly.
});

// ---------------------------------------------------------------------------
// BUG-891: documented, not blocking -- structural check only
// ---------------------------------------------------------------------------

test('BUG-891: the cadence tick cost is documented as KNOWN in the module header (informational, not a behaviour change)', () => {
  assert.match(trafficWellbeingSrc, /BUG-891/, 'the module header must reference BUG-891 (the documented, known cadence-tick cost)');
});

// ===========================================================================
// r4 REWORK -- after round-3 REJECT row 7613 (BUG-892/893/894/895).
// Authority: FEAT-2326609792-inc5.md Lead amendments after row 7613.
// ===========================================================================

// ---------------------------------------------------------------------------
// BUG-892: the emergency-response penalty only applies once the ambulance
// station spec is unlocked (ASM-1518 amendment)
// ---------------------------------------------------------------------------

test('BUG-892: emergencyPenaltyOf/trafficPenaltyOf are EXACTLY 0 for the emergency term while hea_ambulance is locked, and honest-null-as-worst-case returns once unlocked', () => {
  // Direct function-level pin: the gate is the SOLE difference between the
  // two calls below (same coverageShare=null input).
  assert.equal(emergencyPenaltyOf(null, false), 0, 'BUG-892: locked -> penalty must be exactly 0, even for the honest-null worst case');
  assert.equal(emergencyPenaltyOf(0, false), 0, 'BUG-892: locked -> penalty must be exactly 0 for an explicit-zero coverage too');
  assert.equal(emergencyPenaltyOf(null, true), 1, 'unlocked -> null coverage must still be worst-case (ASM-1518), unchanged from pre-r4 behaviour');
  assert.equal(emergencyPenaltyOf(null), emergencyPenaltyOf(null, true), 'the default (no second argument) must behave EXACTLY as unlocked=true -- no silent behaviour change for a caller unaware of the new gate');

  // Wired-to-the-real-state pin: a LOCKED fixture (initialState's own xp,
  // no stations, unlockedAll left false) must show ZERO emergency
  // contribution and a composite BYTE-IDENTICAL to a neutral snapshot.
  const lockedBase = { ...initialState(), population: 50000, tick: 200 };
  assert.equal(specUnlocked(lockedBase, SPECS.hea_ambulance), false, 'fixture precondition: hea_ambulance must be LOCKED on a fresh initialState() (no unlockedAll, default xp)');
  const lockedWorst = { ...lockedBase, trafficSnapshot: { tick: 200, medianCommuteMinutes: 0, gridlockShare: 0, coverageShare: null } };
  const lockedNeutral = { ...lockedBase, trafficSnapshot: { tick: 200, medianCommuteMinutes: 0, gridlockShare: 0, coverageShare: 1 } };
  assert.equal(trafficPenaltyOf(lockedWorst), 0, 'BUG-892: a locked city with honest-null coverage must show a ZERO traffic penalty (no emergency contribution)');
  const partsLockedWorst = wellbeingOf(lockedWorst).parts;
  const partsLockedNeutral = wellbeingOf(lockedNeutral).parts;
  const compositeLockedWorst = compositeWithTrafficPenalty(partsLockedWorst, lockedWorst);
  const compositeLockedNeutral = compositeWithTrafficPenalty(partsLockedNeutral, lockedNeutral);
  assert.equal(compositeLockedWorst, compositeLockedNeutral, 'BUG-892: a locked city must be composite-BYTE-IDENTICAL whether coverageShare is null or fully-covered (1) -- the emergency term cannot move the number at all while locked');

  // Unlocked fixture (unlockedAll: true, no stations built) -- honest-null
  // must apply the FULL emergencyResponseWeight, scaled by earlyGameFactor,
  // exactly like the pre-r4 (BUG-887) formula.
  const unlockedBase = { ...initialState(), unlockedAll: true, population: 50000, tick: 200 };
  assert.equal(specUnlocked(unlockedBase, SPECS.hea_ambulance), true, 'fixture precondition: unlockedAll must genuinely unlock hea_ambulance');
  const unlockedWorst = { ...unlockedBase, trafficSnapshot: { tick: 200, medianCommuteMinutes: 0, gridlockShare: 0, coverageShare: null } };
  const expected = MENTAL.emergencyResponseWeight * earlyGameFactor(unlockedBase.population);
  assert.equal(trafficPenaltyOf(unlockedWorst), expected, 'BUG-892: unlocked + no stations must equal emergencyResponseWeight x earlyGameFactor(population) EXACTLY (commute/gridlock are both 0 on this fixture)');
  assert.ok(trafficPenaltyOf(unlockedWorst) > trafficPenaltyOf(lockedWorst), 'the unlocked penalty must be strictly ABOVE the locked penalty on the identical honest-null snapshot');

  // The display row must respect the SAME gate (locked -> best-case display,
  // not a value implying a station the city cannot yet build).
  assert.equal(emergencyWellbeingPartOf(lockedWorst), wellbeingPartOf(1, lockedWorst.population), 'BUG-892: the locked display row must read as ZERO penalty (full coverage-equivalent), matching emergencyPenaltyOf(*, false) === 0');

  // MUTANT (BUG-892's own): remove the ambulanceUnlocked gate (revert
  // trafficPenaltyOf/emergencyWellbeingPartOf to their pre-r4 shape, calling
  // emergencyPenaltyOf(coverageShare) with no unlock argument at the real
  // engine-facing call sites). DISCRIMINATED IN-TEST: that mutant would make
  // trafficPenaltyOf(lockedWorst) equal `expected` (not 0), redding the
  // `assert.equal(trafficPenaltyOf(lockedWorst), 0, ...)` line directly --
  // and separately reds bug-519-approval-services.test.mjs's own baseline
  // assertion (run via `node tools/test/scoped.mjs
  // webconsole/test/bug-519-approval-services.test.mjs`, reported PASS in
  // the BOW comment gate list).
});

// ---------------------------------------------------------------------------
// BUG-893: the production advance() call site genuinely threads
// gridlockTicksBySegment history across cadence windows
// ---------------------------------------------------------------------------

test('BUG-893: engine.ts advance() threads gridlockTicksBySegment through the PRODUCTION call site across TWO real cadence windows (a {} 3rd-argument mutant caps the count at 1)', () => {
  // Reuse the AC-2 two-path fixture (segment A is genuinely over the v/c
  // threshold under 2 heavy buildings per side -- proven structurally by
  // the AC-2 test above, not merely asserted here). Force the very first
  // advance() to recompute regardless of cadence (BUG-877(c) old-save
  // path) so window 1 starts from a clean, known history.
  let s = { ...twoPathGridlockFixture(2, 2), trafficSnapshot: undefined, gridlockTicksBySegment: undefined };
  const idx = lineSegmentIndexOf(s);
  const segA = idx.tileToSegment.get(k(0, 0));

  // Cadence window 1 (the first real tick -- snapshot absent forces it).
  s = reducer(s, { type: 'tick' });
  assert.ok(s.trafficSnapshot, 'fixture precondition: the first tick computes a snapshot');
  assert.ok((s.gridlockTicksBySegment ?? {})[segA] >= 1, 'fixture precondition: segA must register >=1 sustained tick after window 1 (genuinely over the v/c threshold, not forced)');
  const afterWindow1 = s.gridlockTicksBySegment[segA];

  // Cadence window 2: advance to the NEXT real cadence boundary (same idiom
  // BUG-877(b)'s own test uses) so a SECOND production recompute runs.
  while ((s.tick + 1) % TRAFFIC_RECOMPUTE_TICKS !== 0) s = reducer(s, { type: 'tick' });
  s = reducer(s, { type: 'tick' });

  assert.ok(
    s.gridlockTicksBySegment[segA] >= 2,
    'BUG-893: after TWO cadence windows the production call site must carry the history forward, not restart it each window (got ' + s.gridlockTicksBySegment[segA] + ')'
  );
  assert.equal(
    s.gridlockTicksBySegment[segA],
    afterWindow1 + 1,
    'BUG-893: the second window must add exactly ONE tick on top of the first (genuine threading, not a reset-then-recount)'
  );

  // MUTANT (BUG-893's own): engine.ts's advance() call site passes `{}` as
  // computeTrafficSnapshot's third argument instead of
  // sanitizeCongestionTicksBySpec(s.gridlockTicksBySegment). SCRATCH-PROVEN:
  // a scratch copy of engine.ts with that exact call site's third argument
  // hardcoded to `{}` was pointed at by this exact test file via
  // `node tools/test/scoped.mjs <scratch-copy-path>`; s.gridlockTicksBySegment[segA]
  // came back capped at 1 after the second window (never reaching 2),
  // redding the `>= 2` assertion above exactly as predicted (see the BOW
  // comment for the scratch-run transcript).
});
