// attack-feat802-round.test.mjs — FEAT-2326609802 inc9 "REWARDS", independent
// destructive round 1 (attacker opus-round-feat802-inc9, verdict REJECT).
//
// These are the BEHAVIOURAL pins that round 1 proved were missing. The
// builder's own trafficRewards.test.mjs proved AC-1/AC-2/AC-3/AC-5 with real
// mutant-killing assertions (verified: 4/4 of those doc mutants go red), but
// AC-4's entropy check and AC-6's attract check were written as
// SOURCE-STRING greps plus locally-retyped copies of the formula, never as a
// comparison against the shipped export's own OUTPUT. Two mutants therefore
// survived the full suite:
//
//   MUTANT A — replace the Shannon kernel `-share*Math.log(share)` with
//     `share*(1-share)` (a completely different evenness measure). The suite
//     stayed GREEN, because the only assertion touching the real
//     modeShareBalanceOf was `real >= 0 && real <= 1` — a tautology against
//     an implementation whose last statement is clampN(x, 0, 1).
//   MUTANT B — delete `* integrationMultiplier` from attractivenessOf's
//     return expression (leaving the const declared). The suite stayed
//     GREEN, because AC-6 grepped for the formula text and then evaluated a
//     retyped copy of it rather than calling attractivenessOf at all.
//
// Both are pinned below against the real exports. No timing assertions.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { modeShareBalanceOf, integratedTransportScoreOf, interchangeAdjacencyOf } from '../src/sim/trafficRewards.ts';
import { ladderPointOf, modeShareOf } from '../src/sim/trafficDemand.ts';
import { sanitizeTrafficSnapshot } from '../src/sim/trafficWellbeing.ts';
import { initialState, attractivenessOf, wellbeingOf } from '../src/sim/engine.ts';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(__dirname, '..', '..');
const engineSrc = readFileSync(path.join(repoRoot, 'webconsole', 'src', 'sim', 'engine.ts'), 'utf8');
// Strips block/line comments so a "must never appear" structural pin cannot
// false-fail on the file's OWN doc-comment prose (which legitimately
// discusses a removed symbol by name while explaining its removal) — same
// idiom trafficRewards.test.mjs's own stripComments uses.
function stripComments(text) {
  return text.replace(/\/\*[\s\S]*?\*\//g, '').replace(/\/\/.*$/gm, '');
}
const engineCodeOnly = stripComments(engineSrc);

const OFFSET = 300;
function board(buildings, population) {
  const base = initialState();
  let maxId = 0;
  for (const b of buildings) if (b.id > maxId) maxId = b.id;
  return { ...base, unlockedAll: true, buildings, nextId: maxId + 1, roadNotice: null, population };
}
function rd(id, spec, x, y) {
  return { id, spec, x: x + OFFSET, y: y + OFFSET, builtTick: 0 };
}

// ---------------------------------------------------------------------------
// AC-4 — the entropy KERNEL, pinned against the export's own output
// ---------------------------------------------------------------------------

test('AC-4 (round-1 pin, kills MUTANT A): modeShareBalanceOf output equals the ln(N)-normalised SHANNON entropy of the real ladder vector, recomputed independently', () => {
  for (const population of [100, 100000, 2000000]) {
    const s = board([], population);
    const shares = modeShareOf(ladderPointOf(s));
    const ids = Object.keys(shares);
    assert.ok(ids.length >= 2, `sanity: rung at pop ${population} must expose a multi-mode vector`);

    // Independent recomputation of -Σ s·ln(s) / ln(N). This is the assertion
    // the builder's suite recomputed but never compared to the export.
    let entropy = 0;
    for (const id of ids) {
      const share = shares[id];
      if (share > 0) entropy += -share * Math.log(share);
    }
    const expected = entropy / Math.log(ids.length);
    const actual = modeShareBalanceOf(s);
    assert.ok(
      Math.abs(actual - expected) < 1e-12,
      `pop ${population}: modeShareBalanceOf ${actual} must equal the Shannon/ln(N) value ${expected}; ` +
        'a different evenness kernel (e.g. Simpson share*(1-share)) must not survive',
    );
    // A non-degenerate rung must land strictly inside (0,1) — otherwise the
    // check above could be satisfied by a clamp saturating at a bound.
    assert.ok(actual > 0 && actual < 1, `pop ${population}: balance ${actual} must be strictly inside (0,1), not a saturated clamp`);
  }
});

test('AC-4 (round-1 pin): the two shares of integratedTransportScoreOf reconcile exactly with its own components', () => {
  const s = board([], 100000);
  const expected = 0.5 * interchangeAdjacencyOf(s) + 0.5 * modeShareBalanceOf(s);
  assert.ok(
    Math.abs(integratedTransportScoreOf(s) - expected) < 1e-12,
    `integratedTransportScoreOf must be the rewards.json 0.5/0.5 blend of its own two components (got ${integratedTransportScoreOf(s)}, expected ${expected})`,
  );
  assert.ok(integratedTransportScoreOf(s) >= 0 && integratedTransportScoreOf(s) <= 1, 'score must stay in [0,1]');
});

// ---------------------------------------------------------------------------
// AC-6 (SUPERSEDED by the r3 lead ruling, BUG-938) — attract NO LONGER reads
// integratedTransportScore at all. The round-1/round-2 evidence above is
// what forced the ruling: the multiplier this test used to pin was found
// (BUG-938) to reward pure population, not anything built, so the lead
// removed it rather than accept a hidden population bonus dressed up as a
// transport reward. This replacement proves the REMOVAL, the same rigour
// the original pin used against the addition: attractivenessOf's real
// OUTPUT is pinned invariant under every integratedTransportScore value
// (not merely "no crash"), and the symbol itself is grep-confirmed absent —
// attractivenessOf is byte-identical to its pre-inc9 form at commit c9c0072
// (`git show c9c0072:webconsole/src/sim/engine.ts` — no integrationMultiplier
// anywhere in that revision either; verified by hand at rework time).
// ---------------------------------------------------------------------------

test('AC-6 (r3 ruling, BUG-938): attractivenessOf output is INVARIANT to integratedTransportScore -- no attract coupling survives (kills a re-introduced multiplier of any span)', () => {
  const base = board([rd(1, 'rd_aroad', 0, 0)], 100000);
  const withScore = (integratedTransportScore) => ({
    ...base,
    trafficSnapshot: {
      tick: 0,
      medianCommuteMinutes: 0,
      gridlockShare: 0,
      coverageShare: null,
      safeRoadScore: 1,
      integratedTransportScore,
    },
  });

  const wbOverall = 50;
  const at0 = attractivenessOf(withScore(0), wbOverall);
  const at1 = attractivenessOf(withScore(1), wbOverall);
  const atHalf = attractivenessOf(withScore(0.5), wbOverall);
  const atHuge = attractivenessOf(withScore(1e9), wbOverall);
  const atNeg = attractivenessOf(withScore(-5), wbOverall);
  const atNaN = attractivenessOf(withScore(Number.NaN), wbOverall);

  assert.ok(at0 > 0, `attractivenessOf must be positive and non-degenerate (got ${at0})`);
  assert.equal(at0, at1, 'a re-introduced multiplier off integratedTransportScore must not survive: score 0 vs 1 must give the IDENTICAL output');
  assert.equal(at0, atHalf, 'score 0 vs 0.5 must give the identical output');
  assert.equal(at0, atHuge, 'score 0 vs an out-of-range huge value must give the identical output');
  assert.equal(at0, atNeg, 'score 0 vs a negative value must give the identical output');
  assert.equal(at0, atNaN, 'score 0 vs NaN must give the identical output (BUG-939: NaN must never even be READ here any more)');

  // Structural pin: the symbol itself is gone (a re-added multiplier under a
  // different local name would still be caught by the invariance checks
  // above, but this catches the exact regression directly and cheaply).
  assert.doesNotMatch(engineCodeOnly, /integrationMultiplier/, 'integrationMultiplier must not exist anywhere in engine.ts CODE (BUG-938 removal; comments may still discuss the removal by name)');
  assert.doesNotMatch(engineCodeOnly, /integratedTransportScoreFromSnapshotOf/, 'attractivenessOf/engine.ts must never read integratedTransportScoreFromSnapshotOf in CODE any more (BUG-938 removal; comments may still discuss the removal by name)');
});

// ---------------------------------------------------------------------------
// Snapshot extension — old saves and corrupt values (round brief item 1)
// ---------------------------------------------------------------------------

test('round-1 pin: sanitizeTrafficSnapshot clamps BOTH new inc9 score fields into the unit interval and never yields NaN', () => {
  const legacy = { tick: 3, medianCommuteMinutes: 20, gridlockShare: 0.1, coverageShare: null };

  // A pre-inc9 save carries neither field — both must take their documented
  // neutral defaults, never undefined/NaN.
  const fromLegacy = sanitizeTrafficSnapshot(legacy);
  assert.equal(fromLegacy.safeRoadScore, 1, 'an absent safeRoadScore must default to the documented neutral 1.0');
  assert.equal(fromLegacy.integratedTransportScore, 0, 'an absent integratedTransportScore must default to the documented neutral 0');

  for (const [raw, safeExpected, integExpected] of [
    [1e9, 1, 1],
    [-1, 0, 0],
    [Number.NaN, 1, 0],
    [Number.POSITIVE_INFINITY, 1, 0],
    ['0.5', 1, 0],
    [null, 1, 0],
  ]) {
    const out = sanitizeTrafficSnapshot({ ...legacy, safeRoadScore: raw, integratedTransportScore: raw });
    assert.equal(out.safeRoadScore, safeExpected, `safeRoadScore for raw ${String(raw)} must be ${safeExpected}, got ${out.safeRoadScore}`);
    assert.equal(out.integratedTransportScore, integExpected, `integratedTransportScore for raw ${String(raw)} must be ${integExpected}, got ${out.integratedTransportScore}`);
    assert.ok(Number.isFinite(out.safeRoadScore) && Number.isFinite(out.integratedTransportScore), 'neither field may ever sanitize to NaN/Infinity');
    assert.ok(out.safeRoadScore >= 0 && out.safeRoadScore <= 1, 'safeRoadScore must land in [0,1]');
    assert.ok(out.integratedTransportScore >= 0 && out.integratedTransportScore <= 1, 'integratedTransportScore must land in [0,1]');
  }
});

// ---------------------------------------------------------------------------
// Determinism of the two exports (no timing assertions)
// ---------------------------------------------------------------------------

test('round-1 pin: modeShareBalanceOf/integratedTransportScoreOf are byte-identical across fresh-object re-derivations', () => {
  const mk = () => board([rd(1, 'rd_aroad', 0, 0), rd(2, 'rd_aroad', 1, 0)], 250000);
  const runs = [];
  for (let i = 0; i < 3; i++) {
    const s = mk();
    runs.push(JSON.stringify([modeShareBalanceOf(s), interchangeAdjacencyOf(s), integratedTransportScoreOf(s)]));
  }
  assert.equal(runs[0], runs[1], 'run 1 and 2 must be byte-identical');
  assert.equal(runs[1], runs[2], 'run 2 and 3 must be byte-identical');
});

// ===========================================================================
// ROUND 2 (attacker opus-reround-feat802-inc9, verdict REJECT) -> LEAD RULING
// r3 (BUG-938).
//
// Round 2 found that BUG-927's per-tile-occupancy fix (r2) was direction-
// and magnitude-blind (MUTANT C: drop trip weighting to a plain per-tile
// average; MUTANT D: invert the density band) AND, independently, measured
// (BUG-938) that the fix's real-world consequence was a POPULATION PENALTY
// dressed as a build reward: every built city scored LOWER than a bare map,
// 13 of 18 ladder rungs structurally unreachable at real per-tile occupancy
// scale. The lead's r3 ruling (recorded on FEAT-2326609802) rejected the
// whole per-tile approach rather than patch its direction/magnitude bugs:
// the webconsole has no per-tile mode-split model, so ANY function built
// from per-tile occupancy is approximating something that does not exist.
// modeShareBalanceOf now returns the honest, CITY-WIDE, population-keyed
// value directly (shannonModeBalanceOf(modeShareOf(ladderPointOf(s)))) —
// mathematically what the trip-weighted-over-identical-tiles basis reduces
// to, per the ruling. BUG-937 requires the author suite to pin this basis
// to 1e-12 against an independent recomputation; the pins below do that
// (superseding the MUTANT C/D pins, which pinned a basis that no longer
// exists) and replace the round-2 "denser city scores higher" DIRECTION
// pin — meaningless now that there is no per-tile signal to direct — with
// the pin BUG-937's own resolution note specifies: the score is
// POPULATION-KEYED, so equal-population/different-land-use must be EQUAL,
// and different populations must differ.
// ===========================================================================

import { shannonModeBalanceOf } from '../src/sim/trafficRewards.ts';

function bl(id, spec, x, y) {
  return { id, spec, x: x + OFFSET, y: y + OFFSET };
}

/** Independent re-derivation of AC-4/BUG-938's documented basis: the
 * city-wide ladder row's own mode split, through the Shannon kernel.
 * Deliberately not a copy of the implementation's control flow — it reads
 * the same public inputs (ladderPointOf/modeShareOf) and calls the exported
 * kernel separately, rather than calling modeShareBalanceOf and comparing
 * it to itself. */
function independentCityLevelBalance(s) {
  const shares = modeShareOf(ladderPointOf(s));
  return shannonModeBalanceOf(shares);
}

test('AC-4/BUG-937/BUG-938 (r3 ruling pin): modeShareBalanceOf equals the city-wide population-keyed Shannon value, recomputed independently to 1e-12, for BOTH a bare and a heavily-built city', () => {
  const fixtures = [
    ['bare', board([], 500000)],
    ['40x res_hut', board(Array.from({ length: 40 }, (_, i) => bl(i + 1, 'res_hut', i, 0)), 500000)],
    [
      'mixed res_tower_nyc + off_tower',
      board([bl(1, 'res_tower_nyc', 0, 0), bl(2, 'res_tower_nyc', 1, 0), bl(3, 'off_tower', 2, 0), bl(4, 'off_tower', 3, 0)], 500000),
    ],
  ];
  for (const [label, s] of fixtures) {
    const expected = independentCityLevelBalance(s);
    const actual = modeShareBalanceOf(s);
    assert.ok(
      Math.abs(actual - expected) < 1e-12,
      `${label}: modeShareBalanceOf ${actual} must equal the independently recomputed city-wide Shannon value ${expected} ` +
        '(a re-introduced per-tile mechanism of any shape — MUTANT-C/D-class — must diverge from this pin the moment it depends on WHAT is built)',
    );
  }
});

test('AC-4/BUG-937/BUG-938 (r3 ruling pin, replaces the round-2 DIRECTION pin): modeShareBalanceOf is POPULATION-KEYED, not build-sensitive — equal population + different land use gives the SAME value, different populations give DIFFERENT values', () => {
  // The round-2 "a dense city must score strictly higher" direction pin is
  // MEANINGLESS after the r3 ruling (there is no per-tile signal left to
  // direct) — replaced per the lead's own resolution note with a pin that
  // the score is honestly population-keyed: it must NOT distinguish two
  // same-population cities by land use (that would mean a per-tile
  // mechanism crept back in, unpinned by the test above), and it MUST
  // distinguish two different-population cities (the population dependency
  // itself must still be real, not a frozen constant).
  const population = 500000;
  const huts = Array.from({ length: 40 }, (_, i) => bl(i + 1, 'res_hut', i, 0));
  const hutCity = board(huts, population);
  const denseCity = board(
    [bl(1, 'res_tower_nyc', 0, 0), bl(2, 'res_tower_nyc', 1, 0), bl(3, 'off_tower', 2, 0), bl(4, 'off_tower', 3, 0)],
    population,
  );
  const bareSamePop = board([], population);

  const hut = modeShareBalanceOf(hutCity);
  const dense = modeShareBalanceOf(denseCity);
  const bare = modeShareBalanceOf(bareSamePop);
  assert.equal(
    ladderPointOf(hutCity).population,
    ladderPointOf(denseCity).population,
    'sanity: both fixtures share the same city-wide ladder point',
  );
  assert.equal(hut, dense, `same-population, different land-use mixes MUST collapse to the SAME score today (hut ${hut} vs dense ${dense}) — a difference here means a per-tile mechanism crept back in, unpinned`);
  assert.equal(hut, bare, `a built city and a bare city at the SAME population must also be identical (hut ${hut} vs bare ${bare})`);

  const bigCity = board([], 20_000_000);
  const big = modeShareBalanceOf(bigCity);
  assert.notEqual(bare, big, `two DIFFERENT populations must give different scores (pop ${population} -> ${bare}, pop 20,000,000 -> ${big}) — the population dependency itself must remain real`);
});

test('GR#16/BUG-939: a non-finite integratedTransportScore in the trafficSnapshot must never poison attractivenessOf or wellbeingOf to NaN', () => {
  // BUG-939 fix: safeRoadScoreFromSnapshotOf/integratedTransportScoreFromSnapshotOf
  // (trafficWellbeing.ts) now guard with Number.isFinite instead of a plain
  // `typeof === 'number'` check (NaN passes typeof but not Number.isFinite),
  // defaulting to the documented neutral. attractivenessOf no longer reads
  // integratedTransportScore at all (BUG-938 removal, proven above), so it
  // was ALREADY immune to this specific field; this pin now also covers
  // wellbeingOf's 'Safe roads' part, which DOES still read a score off the
  // snapshot (safeRoadScoreFromSnapshotOf) and could have the same class of
  // bug if a bad value reached it directly.
  const base = board([], 100000);
  const withScores = (safeRoadScore, integratedTransportScore) => ({
    ...base,
    trafficSnapshot: { tick: 0, medianCommuteMinutes: 0, gridlockShare: 0, coverageShare: null, safeRoadScore, integratedTransportScore },
  });
  for (const bad of [Number.NaN, Number.POSITIVE_INFINITY, Number.NEGATIVE_INFINITY, 'x', null, undefined]) {
    const s = withScores(bad, bad);
    const a = attractivenessOf(s, 50);
    assert.ok(Number.isFinite(a), `attractivenessOf must stay finite with safeRoadScore/integratedTransportScore=${String(bad)}, got ${a}`);
    const { parts } = wellbeingOf(s);
    for (const p of parts) {
      assert.ok(Number.isFinite(p.value), `wellbeingOf part '${p.label}' must stay finite with a bad snapshot score=${String(bad)}, got ${p.value}`);
    }
  }
});

// ===========================================================================
// ROUND 3 (attacker opus-round3-feat802-inc9).
//
// Scope enforcement and the r3 blocker fixes (BUG-937/938/939/940) all
// verified — see the BOW evidence comment. The one NEW hole round 3 found
// is below.
//
// FINDING (round 3): safeRoadScoreOf's FOUR-weight branch — the branch that
// exists solely to honour AC-1/AC-2's junction awareness, and that feeds
// this increment's ONLY live coupling ('Safe roads') — has no exact-value
// pin anywhere. The author suite pins the THREE-weight (no-junction) branch
// exactly (AC-1) and pins the junction branch only by ORDER (AC-2:
// grade_separated > roundabout > simple_priority). Two mutants therefore
// survived the ENTIRE 9-file gate group (trafficRewards +
// attack-feat802-round + trafficWellbeing + attack-feat798-round3/round4 +
// trafficAssignment + traffic-data-mirror + bug-519 + crime-mechanic, all
// together, exit 0 PASS):
//
//   MUTANT E — junction branch only: `wSpeed * (1 - designSpeedPenalty)` ->
//     `wSpeed * 1` (the whole design-speed axis of "reward safe roads",
//     0.20 of 1.00 weight, silently deleted). Measured on the author suite's
//     own corridor fixture: roundabout 0.7362297346323037 ->
//     0.8049769346323037 (+0.0687, +9.3%), simple_priority
//     0.6537297346323037 -> 0.7224769346323037, grade_separated
//     0.7712297346323036 -> 0.8399769346323036. The AC-2 ORDER is preserved
//     by the mutant, which is exactly why order-only pins cannot see it.
//   MUTANT F — junction branch only: `wCongestion * congestion` ->
//     `wCongestion * 1.0` (the congestion-safety interaction deleted).
//     Measured 0.7362297346323037 -> 0.7362527999999999; order preserved,
//     gate green.
//
// The SHIPPED implementation is CORRECT — the four-weight value was
// re-derived by hand from rewards.json/roads.json/traffic.json and matches
// HEAD to the last bit (simple_priority 0.6537297346323037, roundabout
// 0.7362297346323037, grade_separated 0.7712297346323036). So this is a
// test-adequacy gap, not a behaviour defect, and the pin below closes it.
// ===========================================================================

import { safeRoadScoreOf, citySafeRoadScoreOf } from '../src/sim/trafficRewards.ts';
import { lineSegmentIndexOf } from '../src/sim/data.ts';
import { segmentDelayOf, roadClassIdOfSegment } from '../src/sim/trafficAssignment.ts';

const rewardsJson = JSON.parse(readFileSync(path.join(repoRoot, 'data', 'traffic', 'rewards.json'), 'utf8'));
const roadsJson = JSON.parse(readFileSync(path.join(repoRoot, 'data', 'roads.json'), 'utf8'));
const trafficJson = JSON.parse(readFileSync(path.join(repoRoot, 'data', 'traffic.json'), 'utf8'));

const kR3 = (x, y) => `${x + OFFSET},${y + OFFSET}`;
const bldgR3 = (id, spec, x, y) => ({ id, spec, x: x + OFFSET, y: y + OFFSET });

/** Same 8-tile rd_aroad corridor shape the author suite's own AC-1/AC-2
 * fixtures use, with an optional junction building adjacent to it. */
function corridorR3(row, population, junctionSpec) {
  const buildings = [
    bldgR3(1, 'res_hut', -9, row),
    ...Array.from({ length: 8 }, (_, i) => rd(2 + i, 'rd_aroad', -8 + i, row)),
    bldgR3(20, 'off_suite', 0, row),
  ];
  if (junctionSpec) buildings.push(bldgR3(50, junctionSpec, -1, row + 1));
  const s = board(buildings, population);
  return { s, segId: lineSegmentIndexOf(s).tileToSegment.get(kR3(-8, row)) };
}

/** Independent re-derivation of the FULL four-weight formula straight from
 * the three data files — no import of the implementation's own constants,
 * no reuse of its control flow. */
function expectedJunctionScore(s, segId, junctionKey) {
  const comp = (id) => rewardsJson.safeRoadScore.components.find((c) => c.id === id);
  const seg = lineSegmentIndexOf(s).segmentById.get(segId);
  const roadClassId = roadClassIdOfSegment(seg);
  const wBase = comp('roadClassBaseSafety').weight;
  const wJunction = comp('junctionTypeSafety').weight;
  const wSpeed = comp('designSpeedPenalty').weight;
  const wCongestion = comp('congestionSafetyInteraction').weight;
  const base = comp('roadClassBaseSafety').byRoadClassId[roadClassId];
  const junctionSafety = comp('junctionTypeSafety').byJunctionType[junctionKey];
  const speedKmh = (roadsJson.classes.find((c) => c.id === roadClassId).speedLimit * trafficJson.metresPerMile) / 1000;
  const anchor = comp('designSpeedPenalty').speedAnchorKmh;
  const span = comp('designSpeedPenalty').speedSpanKmh;
  const penalty = Math.min(1, Math.max(0, (speedKmh - anchor) / span));
  const curve = [...comp('congestionSafetyInteraction').curve].sort((a, b) => a.vOverC - b.vOverC);
  const vc = segmentDelayOf(s).get(segId).vOverC;
  let congestion;
  if (vc <= curve[0].vOverC) congestion = curve[0].safetyContribution;
  else if (vc >= curve[curve.length - 1].vOverC) congestion = curve[curve.length - 1].safetyContribution;
  else {
    for (let i = 0; i < curve.length - 1; i++) {
      const a = curve[i];
      const b = curve[i + 1];
      if (vc >= a.vOverC && vc <= b.vOverC) {
        congestion = a.safetyContribution + ((vc - a.vOverC) / (b.vOverC - a.vOverC)) * (b.safetyContribution - a.safetyContribution);
        break;
      }
    }
  }
  return { expected: wBase * base + wJunction * junctionSafety + wSpeed * (1 - penalty) + wCongestion * congestion, penalty, congestion };
}

test('AC-1/AC-2 (round-3 pin, kills MUTANT E + MUTANT F): the FOUR-weight junction branch matches the exact hand-derived value for every junction spec, not merely the right ORDER', () => {
  const cases = [
    ['rd_junction', 'simple_priority', 21],
    ['rd_roundabout', 'roundabout', 5],
    ['rd_mwyjunction', 'grade_separated', 37],
  ];
  for (const [spec, junctionKey, row] of cases) {
    const fix = corridorR3(row, 50000, spec);
    const actual = safeRoadScoreOf(fix.s).get(fix.segId);
    assert.ok(actual !== undefined, `${spec}: fixture must produce a scored segment`);
    const { expected, penalty, congestion } = expectedJunctionScore(fix.s, fix.segId, junctionKey);
    // Sanity: the two terms MUTANT E/F delete must be non-degenerate in this
    // fixture, otherwise the pin below would be vacuous against them.
    assert.ok(penalty > 1e-6, `${spec}: designSpeedPenalty ${penalty} must be non-zero here or the speed-term pin is vacuous`);
    assert.ok(congestion < 1, `${spec}: congestion contribution ${congestion} must be < 1 here or the congestion-term pin is vacuous`);
    assert.ok(
      Math.abs(actual - expected) < 1e-9,
      `${spec}: junction-branch score ${actual} must equal the hand-derived four-weight value ${expected} ` +
        '(order-only pins let a deleted designSpeedPenalty or congestionSafetyInteraction term through — round 3 MUTANT E/F)',
    );
    assert.ok(actual >= 0 && actual <= 1, `${spec}: score ${actual} must stay in [0,1]`);
  }
});

test("AC-5 (round-3 pin): the 'Safe roads' wellbeing part is bounded [0,100], finite at both score extremes and on an empty city, strictly ascending, and 'Integrated transport' is absent", () => {
  const bare = board([], 100000);
  // Empty city: no flow anywhere -> citySafeRoadScoreOf's documented neutral
  // 1.0, and the part must be its best, never NaN.
  assert.equal(citySafeRoadScoreOf(bare), 1.0, 'a city with no routed flow must read the neutral 1.0, never NaN');
  const withScore = (safeRoadScore) => ({
    ...bare,
    trafficSnapshot: { tick: 0, medianCommuteMinutes: 0, gridlockShare: 0, coverageShare: null, safeRoadScore, integratedTransportScore: 0 },
  });
  const partAt = (v) => wellbeingOf(withScore(v)).parts.find((p) => p.label === 'Safe roads').value;
  const lo = partAt(0);
  const mid = partAt(0.5);
  const hi = partAt(1);
  for (const [label, v] of [['0', lo], ['0.5', mid], ['1', hi]]) {
    assert.ok(Number.isFinite(v) && v >= 0 && v <= 100, `'Safe roads' at score ${label} must be finite in [0,100], got ${v}`);
  }
  assert.ok(lo < mid && mid < hi, `'Safe roads' must be POSITIVE-signed and strictly ascending (0 -> ${lo}, 0.5 -> ${mid}, 1 -> ${hi}); an inverted part would descend`);
  const labels = wellbeingOf(withScore(1)).parts.map((p) => p.label);
  assert.equal(labels.filter((l) => l === 'Safe roads').length, 1, "exactly ONE 'Safe roads' part may exist");
  assert.ok(!labels.includes('Integrated transport'), "'Integrated transport' must NOT be a wellbeing part (BUG-938 r3 ruling)");
});
