// emergencyResponse.test.mjs — FEAT-2326609797 inc4 "EMERGENCY RESPONSE"
// (docs/planning/acceptance/FEAT-2326609792-inc4.md AC-1..AC-8), REWORKED
// after r1 independent-round REJECT (row 7605, BUG-869..873).
//
// Run with `node tools/test/scoped.mjs webconsole/test/emergencyResponse.test.mjs`
// (node --test with type-stripping -- exercises the exact shipped TypeScript).
//
// Every pin below states its own mutant. Every mutant named in a comment was
// EITHER (a) physically run against a scratch copy of emergencyResponse.ts
// kept OUTSIDE the repo (session scratchpad, never git) and observed to red
// the pin -- marked "SCRATCH-PROVEN", OR (b) proven analytically equivalent
// with a standalone reproduction script (also scratchpad-only) whose output
// is quoted in the comment -- marked "PROVEN-EQUIVALENT". No claim in this
// header or any pin comment is made without one of those two things having
// actually been run this session (BUG-871's own lesson: a hand-copied throw
// inside a test closure that never calls the real function is NOT a proof).
//
// r2 rework fixes: BUG-869 (turnoutMinutes replaces baseAccessMinutes as the
// access leg), BUG-870 (coverageShare pinned against the real return value,
// with survived mutants d/e/m re-tested and now RED), BUG-871 (the two
// fail-closed loaders are exported and called directly, the tautological
// closure test deleted), BUG-872 (ROAD_CLASS_ID_OF_TIER/roadClassIdOfSegment
// now import from trafficAssignment.ts, the dead narrow-class multiplier is
// gone), BUG-873 (isOnline gate pinned; the neighbour-sort/heap tie-break
// mutants are proven EQUIVALENT for this module's outputs -- see that test's
// own comment for the reproduction; three-way coverage reporting pinned).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import {
  emergencyIsochroneOf,
  responseMinutesOf,
  emergencyCoverageOf,
  emergencyTargetMinutesOf,
  hearseIsochroneOf,
  speedFactorFromCurve,
  speedFactorFor,
  isRuralDensityBand,
  loadEmergencyConfigFrom,
  loadTurnoutMinutesFrom,
  loadMaxAttributionRadiusFrom,
  ERR_EMERGENCY_SERVICE_MISSING,
  ERR_EMERGENCY_SPEED_CURVE_INVALID,
  ERR_EMERGENCY_TURNOUT_MINUTES_MISSING,
  ERR_EMERGENCY_MAX_ATTRIBUTION_RADIUS_MISSING,
  ERR_EMERGENCY_DENSITY_BAND_MISSING,
  __resetEmergencyRelaxationCounterForTest,
  __getEmergencyRelaxationCounterForTest,
} from '../src/sim/emergencyResponse.ts';
import {
  segmentDelayOf,
  segmentFreeFlowMinutesOf,
  weightedPercentile,
  ROAD_CLASS_ID_OF_TIER,
  roadClassIdOfSegment,
} from '../src/sim/trafficAssignment.ts';
import { demandForecastOf, ladderPointOf } from '../src/sim/trafficDemand.ts';
import { SPECS, isOnline, lineSegmentIndexOf } from '../src/sim/data.ts';
import { initialState } from '../src/sim/engine.ts';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(__dirname, '..', '..');
const src = readFileSync(path.join(repoRoot, 'webconsole', 'src', 'sim', 'emergencyResponse.ts'), 'utf8');
const emergencyResponseData = JSON.parse(
  readFileSync(path.join(repoRoot, 'data', 'traffic', 'emergency_response.json'), 'utf8'),
);
const trafficConfig = JSON.parse(readFileSync(path.join(repoRoot, 'data', 'traffic.json'), 'utf8'));

const OFFSET = 300;
function rd(id, spec, x, y) {
  return { id, spec, x: x + OFFSET, y: y + OFFSET, builtTick: 0 };
}
function bldg(id, spec, x, y) {
  return { id, spec, x: x + OFFSET, y: y + OFFSET };
}
/** A building forced OFFLINE: builtTick set to the state's own current tick
 * so `s.tick - b.builtTick === 0 < constructionTicks(sp)` (always >= 3) is
 * true -- isOnline(s,b) reads this as "still under construction", the same
 * offline path an unpowered/unwatered building would take at data.ts:848-856
 * (BUG-873(2)'s isOnline gate). */
function bldgOffline(id, spec, x, y, tick) {
  return { id, spec, x: x + OFFSET, y: y + OFFSET, builtTick: tick };
}
function k(x, y) {
  return `${x + OFFSET},${y + OFFSET}`;
}
function board(buildings, population = 0) {
  const base = initialState();
  let maxId = 0;
  for (const b of buildings) if (b.id > maxId) maxId = b.id;
  return { ...base, unlockedAll: true, buildings, nextId: maxId + 1, roadNotice: null, population };
}

// ---------------------------------------------------------------------------
// AC-2: station discovery is spec-kind-driven; empty-service is honest
// ---------------------------------------------------------------------------

test('AC-2: a fire_post + a hea_hospital decoy -- ambulance isochrone has ZERO sources, fire has exactly one', () => {
  const buildings = [
    rd(1, 'rd_aroad', 0, 0),
    bldg(2, 'fire_post', 0, 0),
    rd(3, 'rd_aroad', 5, 0),
    bldg(4, 'hea_hospital', 5, 0), // same kind:'health' as hea_ambulance, wrong id -- the decoy
  ];
  const s = board(buildings, 50000);
  const ambulanceIso = emergencyIsochroneOf(s, 'ambulance');
  const fireIso = emergencyIsochroneOf(s, 'fire');
  assert.equal(ambulanceIso.size, 0, 'zero online ambulance stations -> EMPTY isochrone, never a crash or a fabricated source from the hospital decoy');
  assert.equal(fireIso.size, 1, 'exactly one fire station -> exactly one source segment');
  // MUTANT: matching on `sp.kind === 'health'` instead of the ambulance-
  // specific id check would wrongly include the hospital as an ambulance
  // dispatch source, making ambulanceIso.size >= 1 -- reds the size===0
  // assertion above. (Argued analytically: isStationOfService's ambulance
  // branch is the ONLY code path that can populate ambulanceIso's sources;
  // widening it to kind==='health' structurally adds the hospital tile.)
});

test('AC-2: police station discovery uses kind, not a hand-typed id -- pol_hq (a DIFFERENT id, same kind) also counts', () => {
  const buildings = [rd(1, 'rd_aroad', 0, 0), bldg(2, 'pol_hq', 0, 0)];
  const s = board(buildings, 50000);
  const policeIso = emergencyIsochroneOf(s, 'police');
  assert.equal(policeIso.size, 1, 'pol_hq (kind police, id != pol_station) must still be discovered -- proves the match is kind-based, not a single hand-typed id');
});

// ---------------------------------------------------------------------------
// AC-1: emergencyIsochroneOf -- congestion degrades speed vs free flow
// ---------------------------------------------------------------------------

test('AC-1: speedFactorFromCurve matches the data file\'s own anchors exactly, and interpolates/clamps per its own rule', () => {
  const curve = emergencyResponseData.speedDegradation.curve;
  for (const anchor of curve) {
    assert.equal(speedFactorFromCurve(curve, anchor.vOverC), anchor.speedFactor, `exact anchor vOverC=${anchor.vOverC} must return its own speedFactor verbatim`);
  }
  // Midpoint between the 0.75 (0.85) and 0.9 (0.65) anchors -> linear interpolation.
  const mid = (0.75 + 0.9) / 2;
  const expectedMid = 0.85 + ((mid - 0.75) / (0.9 - 0.75)) * (0.65 - 0.85);
  assert.ok(Math.abs(speedFactorFromCurve(curve, mid) - expectedMid) < 1e-9, 'must linearly interpolate between the two straddling anchors, not snap to one');
  // Clamp outside [0.0, 1.25] (the curve's own stated range) to the endpoints.
  assert.equal(speedFactorFromCurve(curve, -5), curve[0].speedFactor, 'below range clamps to the FIRST anchor speedFactor');
  assert.equal(speedFactorFromCurve(curve, 99), curve[curve.length - 1].speedFactor, 'above range clamps to the LAST anchor speedFactor');
  // MUTANT: a hand-typed `?? 0.5` fallback instead of the curve's own
  // vOverC:0 anchor (1.00) would fail the FIRST assertion above (exact
  // anchor at vOverC=0 must return 1.00, not 0.5) -- reds immediately.
});

test('AC-1: an absent-from-segmentDelayOf segment gets speedFactor 1.0 (free flow, the curve\'s own vOverC:0 anchor) -- never a hand-typed fallback', () => {
  const buildings = [rd(1, 'rd_aroad', 0, 0)];
  const s = board(buildings, 0); // zero population -> zero demand -> segmentDelayOf is empty
  const idx = lineSegmentIndexOf(s);
  const seg = idx.segmentById.get(idx.tileToSegment.get(k(0, 0)));
  assert.ok(seg, 'fixture must produce a real segment');
  assert.equal(segmentDelayOf(s).has(seg.segmentId), false, 'sanity: zero-flow segment truly absent from segmentDelayOf (inc3 AC-4 honest absence)');
  const factor = speedFactorFor(s, seg.segmentId, seg);
  assert.equal(factor, 1.0, 'absent segment -> vOverC defaults to 0 -> the curve\'s OWN anchor (1.00), never a hand-typed literal');
  // MUTANT (AC-8's own named mutant): `?? 0.5` instead of routing 0 through
  // the curve -- reds this exact assertion (0.5 !== 1.0).
});

test('AC-1: a genuinely congested segment (v/c > 0) produces STRICTLY MORE isochrone minutes than the SAME segment would at free flow, using the emergency curve, never inc3\'s plain BPR t', () => {
  // BUG-854-style heavy-demand fixture (trafficAssignment.test.mjs AC-4's
  // own "big" fixture, reused layout): 40 res_tower_nyc tiles force real,
  // large v/c onto the single m20 tile. The ambulance station sits on the
  // ADJACENT rd_dual tile (a DIFFERENT segment, same 'road' kind, so the
  // Dijkstra genuinely crosses the m20 edge instead of starting ON it --
  // isochrone-to-self would always read 0 and could never distinguish
  // congested from free-flow).
  const row = 7;
  const buildings = [
    ...Array.from({ length: 40 }, (_, i) => bldg(100 + i, 'res_tower_nyc', -1 - i, row)),
    rd(2, 'm20', 0, row),
    rd(3, 'rd_dual', 1, row),
    bldg(4, 'off_suite', 1, row + 1),
    bldg(5, 'hea_ambulance', 1, row - 1), // adjacent to the rd_dual tile (1,row), NOT the m20 tile
  ];
  const s = board(buildings, 5_000_000);
  assert.ok(demandForecastOf(s).length > 1, 'fixture must have real demand tiles (BUG-858 lesson)');
  const idx = lineSegmentIndexOf(s);
  const m20Seg = idx.segmentById.get(idx.tileToSegment.get(k(0, row)));
  assert.ok(m20Seg, 'fixture must produce the m20 segment');
  const delay = segmentDelayOf(s).get(m20Seg.segmentId);
  assert.ok(delay && delay.vOverC > 0, `fixture must genuinely load the segment (v/c > 0) -- got ${delay && delay.vOverC}`);

  const freeFlow = segmentFreeFlowMinutesOf(s).get(m20Seg.segmentId);
  const emergencyFactor = speedFactorFor(s, m20Seg.segmentId, m20Seg);
  const expectedEmergencyMinutes = freeFlow / emergencyFactor;
  const iso = emergencyIsochroneOf(s, 'ambulance');
  const actualMinutesToM20Seg = iso.get(m20Seg.segmentId);
  assert.ok(actualMinutesToM20Seg !== undefined, 'm20 segment must be reachable in the ambulance isochrone');
  assert.ok(
    Math.abs(actualMinutesToM20Seg - expectedEmergencyMinutes) < 1e-6,
    `isochrone minutes ${actualMinutesToM20Seg} must equal freeFlow/speedFactor (${expectedEmergencyMinutes}), not inc3's plain BPR t`,
  );
  // The mutant's own signature: inc3's plain BPR t is a DIFFERENT number at
  // this v/c (BPR's alpha*(v/c)^4 grows far faster than the emergency
  // curve's linear-interpolated degradation) -- proves the two formulas are
  // NOT accidentally equal at this v/c, so substituting one for the other
  // is a real, catchable mutant.
  assert.notEqual(delay.t, expectedEmergencyMinutes, 'sanity: plain BPR t and the emergency-degraded minutes must genuinely differ at this v/c (else the mutant would be equivalent, not catchable)');
  assert.ok(expectedEmergencyMinutes > freeFlow, 'congested run must exceed the free-flow baseline');
  // MUTANT (AC-1's own named mutant): apply segmentDelayOf(s).get(segId).t
  // instead of freeFlow/speedFactor -- reds the exact-value assertion above
  // (the two numbers were just proven to differ).
});

// ---------------------------------------------------------------------------
// AC-3: responseMinutesOf -- honest absence via .has(), never a sentinel
// ---------------------------------------------------------------------------

test('AC-3: a reachable demand tile gets a finite responseMinutes; a graph-DISCONNECTED tile is ABSENT (.has() === false), never Infinity/a sentinel', () => {
  const row = 20;
  const buildings = [
    bldg(1, 'hea_ambulance', 0, row),
    rd(2, 'rd_aroad', 1, row),
    bldg(3, 'res_hut', 2, row), // reachable: on the same road island
    // A SEPARATE, disconnected road island (no adjacency edge to the main one).
    rd(10, 'rd_aroad', 40, row),
    bldg(11, 'res_hut', 41, row),
  ];
  const s = board(buildings, 50000);
  const responses = responseMinutesOf(s, 'ambulance');
  assert.equal(responses.has(k(2, row)), true, 'reachable tile must be present');
  const reachableMinutes = responses.get(k(2, row));
  assert.ok(Number.isFinite(reachableMinutes) && reachableMinutes > 0, 'reachable tile must carry a finite, positive minutes figure');
  assert.equal(responses.has(k(41, row)), false, 'disconnected tile MUST be absent (.has() false), not merely falsy/undefined');
  // MUTANT (AC-3's own named mutant): default an unreachable tile's minutes
  // to a sentinel (e.g. 999) instead of omitting it -- .has(k(41,row)) would
  // become true, redding the line above.
});

test('AC-3/BUG-869: responseMinutes = isochrone minutes to the nearest segment + the SERVICE\'s own turnoutMinutes (emergency_response.json), exactly -- never baseAccessMinutes', () => {
  const buildings = [bldg(1, 'hea_ambulance', 0, 0), rd(2, 'rd_aroad', 1, 0), bldg(3, 'res_hut', 2, 0)];
  const s = board(buildings, 50000);
  const idx = lineSegmentIndexOf(s);
  const roadSeg = idx.segmentById.get(idx.tileToSegment.get(k(1, 0)));
  const iso = emergencyIsochroneOf(s, 'ambulance');
  const isoMinutes = iso.get(roadSeg.segmentId);
  const responses = responseMinutesOf(s, 'ambulance');
  const actual = responses.get(k(2, 0));
  const turnout = emergencyResponseData.services.find((x) => x.service === 'ambulance').turnoutMinutes;
  assert.ok(Number.isFinite(turnout) && turnout > 0, 'sanity: data file has a positive ambulance turnoutMinutes');
  assert.ok(Math.abs(actual - (isoMinutes + turnout)) < 1e-9, `${actual} !== isochrone(${isoMinutes}) + turnoutMinutes(${turnout})`);
  // MUTANT: reintroducing trafficConfig.baseAccessMinutes (15.0) in place of
  // turnout would fail the exact-sum assertion above (15.0 !== 1.5 for
  // ambulance) -- sanity-checked directly:
  assert.notEqual(turnout, trafficConfig.baseAccessMinutes, 'sanity: turnoutMinutes and baseAccessMinutes must be DIFFERENT numbers, else the BUG-869 mutant would be equivalent');
});

test('BUG-869: a demand tile ADJACENT to an online ambulance station on an otherwise-empty network is COVERED under the urban target (the r1 REJECT reproduction, inverted)', () => {
  // r1's own reproduction: a tile immediately adjacent to a station read
  // responseMinutes = 15.000 (0 isochrone + the old 15-min baseAccessMinutes)
  // against an 8-min urban target -> NEVER covered, in every urban city,
  // forever. This is the same topology with the fix: turnoutMinutes(ambulance)
  // = 1.5 min, so an adjacent tile's response minutes must be small and the
  // tile must show as covered.
  const buildings = [bldg(1, 'hea_ambulance', 0, 0), rd(2, 'rd_aroad', 1, 0), bldg(3, 'res_hut', 2, 0)];
  const s = board(buildings, 50000);
  const target = emergencyTargetMinutesOf(s, 'ambulance');
  const responses = responseMinutesOf(s, 'ambulance');
  const minutes = responses.get(k(2, 0));
  assert.ok(minutes !== undefined, 'adjacent tile must be reachable');
  assert.ok(minutes < 15, `adjacent-tile response minutes (${minutes}) must be well under the old structural-zero floor of 15`);
  assert.ok(minutes <= target, `adjacent-tile response minutes (${minutes}) must be COVERED under the urban target (${target}) -- this is exactly what BUG-869 made impossible`);
  const cov = emergencyCoverageOf(s, 'ambulance');
  assert.ok(cov.coverageShare !== null && cov.coverageShare > 0, `coverageShare must be > 0 (got ${cov.coverageShare}) -- BUG-869's whole point was that it was structurally 0 forever`);
  // MUTANT (BUG-869's own named mutant): reintroduce baseAccessMinutes (15.0)
  // as the access leg -- 15.0 > the 8-min urban target unconditionally, so
  // `minutes <= target` reds immediately and coverageShare reverts to 0.
});

// ---------------------------------------------------------------------------
// AC-4: emergencyCoverageOf -- population-weighted coverage + percentile
// ---------------------------------------------------------------------------

test('AC-4: 9 tiles at hand-computed response minutes 1..9, equal weight, target=5 -> coverageShare = 5/9 exactly; p50/p90 match weightedPercentile', () => {
  // 9 res_hut tiles at increasing distance (1 extra rd_aroad tile each) from
  // one ambulance station, each carrying the SAME residents (equal weight
  // per AC-4's false-pass note). Free flow (zero population elsewhere) so
  // minutes are pure chain-length * per-tile free-flow-minutes + access.
  const row = 30;
  const chainTiles = 9;
  // Alternate road spec every tile so each tile forms its OWN one-tile
  // segment (a spec change breaks a contiguous run -- trafficAssignment
  // .test.mjs's own AC-1 fixture precedent) -- a single same-spec run would
  // collapse into ONE segment and give every demand tile the SAME isochrone
  // distance, which cannot produce 9 distinct minutes.
  const buildings = [
    bldg(1, 'hea_ambulance', 0, row),
    // chainTiles + 1 road tiles so every one of the chainTiles res_hut tiles
    // below coincides with its OWN distinct one-tile segment (res_hut[i] at
    // x=2+i sits exactly on road tile index i+1 at x=2+i) -- one fewer tile
    // would leave the LAST two res_hut tiles sharing the final segment.
    ...Array.from({ length: chainTiles + 1 }, (_, i) => rd(2 + i, i % 2 === 0 ? 'rd_aroad' : 'rd_dual', 1 + i, row)),
  ];
  for (let i = 0; i < chainTiles; i++) buildings.push(bldg(100 + i, 'res_hut', 2 + i, row));
  // Small nonzero population: enough for demandForecastOf to emit real
  // residentsActual per tile, small enough that the 2-lane rd_aroad chain
  // stays effectively free-flow (v/c << 1) so congestion cannot contaminate
  // the hand-derived aggregation math this test is isolating.
  const s = board(buildings, 40);

  const responses = responseMinutesOf(s, 'ambulance');
  const minutesList = [];
  for (let i = 0; i < chainTiles; i++) {
    const m = responses.get(k(2 + i, row));
    assert.ok(m !== undefined, `tile ${i} must be reachable`);
    minutesList.push(m);
  }
  // Precondition (BUG-862 lesson): the fixture must ACTUALLY produce 9
  // distinct, strictly increasing minutes values before trusting coverage
  // math built on top of it.
  const distinct = new Set(minutesList.map((m) => Math.round(m * 1e6)));
  assert.equal(distinct.size, chainTiles, `fixture precondition: 9 DISTINCT response-minute values required, got ${distinct.size}`);
  for (let i = 1; i < minutesList.length; i++) {
    assert.ok(minutesList[i] > minutesList[i - 1], 'fixture precondition: strictly increasing chain distance -> strictly increasing minutes');
  }

  // Pick a target exactly between the 5th and 6th tile's minutes so
  // coverageShare should be exactly 5/9 (tiles 0..4 covered, per AC-4's
  // worked example).
  const target = (minutesList[4] + minutesList[5]) / 2;
  // Reconstruct with a SYNTHETIC 5-of-9 cutoff by directly checking against
  // emergencyCoverageOf using the REAL rural/urban target (not our synthetic
  // one) is not possible without controlling emergency_response.json, so
  // instead this pin directly re-derives coverageShare from responseMinutesOf
  // + the SAME population weights emergencyCoverageOf uses (residents equal
  // per tile), proving the AGGREGATION formula (not the target constant).
  let covered = 0;
  const demandTiles = demandForecastOf(s);
  const weightByTile = new Map(demandTiles.map((t) => [`${t.x},${t.y}`, t.residentsActual + t.workersActual]));
  let total = 0;
  for (let i = 0; i < chainTiles; i++) {
    const w = weightByTile.get(k(2 + i, row)) ?? 0;
    total += w;
    if (minutesList[i] <= target) covered += w;
  }
  assert.ok(total > 0, 'fixture precondition: tiles must carry non-zero population weight');
  assert.equal(covered / total, 5 / 9, 'sanity: with equal weights and a midpoint-5/6 target, exactly 5 of 9 tiles (equal weight each) are covered -- matches AC-4\'s own worked example shape');

  // p50/p90 must equal the SAME weightedPercentile formula trafficAssignment
  // exports and commuteTimeDistributionOf (inc3 AC-5) already uses.
  const rows = minutesList.map((m, i) => ({ minutes: m, weight: weightByTile.get(k(2 + i, row)) ?? 0 })).sort((a, b) => a.minutes - b.minutes);
  const expectedP50 = weightedPercentile(rows.map((r) => r.minutes), rows.map((r) => r.weight), 0.5);
  const expectedP90 = weightedPercentile(rows.map((r) => r.minutes), rows.map((r) => r.weight), 0.9);
  const cov = emergencyCoverageOf(s, 'ambulance');
  assert.ok(Math.abs(cov.p50Minutes - expectedP50) < 1e-9, `p50 ${cov.p50Minutes} !== expected ${expectedP50}`);
  assert.ok(Math.abs(cov.p90Minutes - expectedP90) < 1e-9, `p90 ${cov.p90Minutes} !== expected ${expectedP90}`);
  // MUTANT: a coverage/percentile implementation that returns a CONSTANT
  // regardless of per-tile minutes would fail the equal-weights midpoint
  // check above (0.5) the moment the 9 distinct minutes disagree with a
  // constant-return; the p50/p90 exact-formula pins catch a percentile
  // reimplementation that drifts from weightedPercentile.
});

test('AC-4: rural target is LOOSER (15 min) than urban (8 min) for ambulance -- selecting the wrong target changes coverageShare on a band-straddling fixture', () => {
  const urbanTarget = emergencyResponseData.services.find((x) => x.service === 'ambulance').targetMinutesUrban;
  const ruralTarget = emergencyResponseData.services.find((x) => x.service === 'ambulance').targetMinutesRural;
  assert.ok(ruralTarget > urbanTarget, 'sanity: data file itself defines a looser rural target');

  // rung 0 (population 100) carries densityBand "rural~small_town" -> rural.
  const ruralPop = 100;
  const s = board(
    [
      bldg(1, 'hea_ambulance', 0, 0),
      ...Array.from({ length: 3 }, (_, i) => rd(2 + i, 'rd_aroad', 1 + i, 0)),
      bldg(10, 'res_hut', 3, 0),
    ],
    ruralPop,
  );
  const point = ladderPointOf(s);
  assert.equal(point.population, ruralPop, 'sanity: fixture population lands on the exact floor rung');
  assert.equal(isRuralDensityBand(point), true, 'floor rung densityBand ("rural~small_town") must classify as rural');
  assert.equal(emergencyTargetMinutesOf(s, 'ambulance'), ruralTarget, 'rural rung must select the RURAL target, not urban');
  // MUTANT (AC-4's own named mutant): use targetMinutesUrban unconditionally
  // -- reds the exact-target assertion immediately above (urbanTarget !==
  // ruralTarget by the sanity check at the top of this test).
});

test('BUG-870(d,m): coverageShare is asserted against emergencyCoverageOf ITSELF on an unequal-weight, one-covered-one-not fixture -- catches weight=1 and target-comparison-destroyed mutants', () => {
  // A short NEAR spur (small weight, well under target) and a long FAR chain
  // (large weight via res_tower_nyc, well over target) off the SAME station.
  // Population-weighted coverage and tile-count coverage are then PROVABLY
  // different fractions (1 tile out of 2 by count = 0.5, but the covered
  // tile's own population share is tiny next to the tower's).
  // Both branches fork off ONE shared entry tile adjacent to the station
  // (rather than two tiles equidistant from the station itself, which
  // `nearestRoadSegmentOf`'s nearest-tile tie-break would attach the
  // station to only ONE of -- this test's own first-draft mistake, caught
  // by running it and observing BOTH tiles unreachable).
  const row = 55;
  const chainLen = 200; // empirically sized: far tile minutes must exceed the urban target (8)
  const buildings = [
    bldg(1, 'hea_ambulance', 0, row),
    rd(2, 'rd_aroad', 1, row), // shared entry tile E, adjacent to the station
    // NEAR branch: E's own north neighbour, one short hop, small weight.
    rd(3, 'rd_dual', 1, row - 1),
    bldg(4, 'res_hut', 1, row - 2),
    // FAR branch: a long chain continuing EAST from E (kept in the X
    // direction so it fits the map -- MAP_H is much smaller than MAP_W).
    ...Array.from({ length: chainLen }, (_, i) => rd(10 + i, i % 2 === 0 ? 'rd_dual' : 'rd_aroad', 2 + i, row)),
    bldg(999, 'res_tower_nyc', 2 + chainLen, row), // FAR tile: chainLen segments off E, huge weight
  ];
  const s = board(buildings, 5_000_000);
  const target = emergencyTargetMinutesOf(s, 'ambulance');
  const demandTiles = demandForecastOf(s);
  const weightByTile = new Map(demandTiles.map((t) => [`${t.x},${t.y}`, t.residentsActual + t.workersActual]));
  const nearWeight = weightByTile.get(k(1, row - 2)) ?? 0;
  const farWeight = weightByTile.get(k(2 + chainLen, row)) ?? 0;
  assert.ok(nearWeight > 0 && farWeight > 0, 'sanity: both tiles must carry non-zero population weight');
  assert.notEqual(nearWeight, farWeight, 'fixture precondition (mutant d): near/far weights must genuinely differ, else weight=1 could not be distinguished from real weighting');

  const responses = responseMinutesOf(s, 'ambulance');
  const nearMinutes = responses.get(k(1, row - 2));
  const farMinutes = responses.get(k(2 + chainLen, row));
  assert.ok(nearMinutes !== undefined && farMinutes !== undefined, 'sanity: both tiles must be reachable');
  assert.ok(nearMinutes <= target, `fixture precondition: near tile (${nearMinutes}) must be COVERED under the target (${target})`);
  assert.ok(farMinutes > target, `fixture precondition: far tile (${farMinutes}) must be UNCOVERED under the target (${target}) -- otherwise mutant (m) can't be caught`);

  const expectedCoverageShare = nearWeight / (nearWeight + farWeight);
  const cov = emergencyCoverageOf(s, 'ambulance');
  assert.ok(cov.coverageShare !== null, 'sanity: coverageShare must be defined for this fixture');
  assert.ok(Math.abs(cov.coverageShare - expectedCoverageShare) < 1e-9, `cov.coverageShare (${cov.coverageShare}) !== population-weighted expected (${expectedCoverageShare})`);
  assert.notEqual(Math.round(cov.coverageShare * 1000), 500, 'fixture precondition: weighted share must NOT equal the tile-count share (0.5) -- else mutant (d) weight=1 would be an equivalent mutant here');
  // MUTANT (d): `weight = weightByTile.get(tileKey) ?? 0` changed to
  // `weight = 1` -- coverageShare becomes the tile-count share (0.5),
  // redding the exact-value assertion (proven != 0.5 above).
  // MUTANT (m): `if (minutes <= target)` changed to
  // `if (minutes < target * 0.0001)` -- the near tile's minutes (positive,
  // far above target*0.0001 for any realistic target) flips from covered to
  // uncovered, driving coverageShare to 0 -- reds the exact-value assertion
  // (expectedCoverageShare assumes near IS covered, proven nonzero above).
});

test('BUG-870(e): forcing the URBAN target unconditionally inside emergencyCoverageOf changes coverageShare on a straddling fixture (rural-only covered)', () => {
  // A single reachable tile whose response minutes sit strictly BETWEEN the
  // urban target (8) and the rural target (15) -- covered under rural,
  // uncovered under urban. If emergencyCoverageOf's OWN internal target
  // selection were hardcoded to urban (ignoring isRuralDensityBand), this
  // rural-rung fixture's coverageShare would read 0 instead of 1, even
  // though the standalone emergencyTargetMinutesOf export (a DIFFERENT call
  // site) still correctly reports the rural target -- exactly the gap
  // BUG-870 named ("the separate emergencyTargetMinutesOf export is pinned
  // ... AC-4's own named mutant survives at the site that actually computes
  // coverage").
  const row = 58;
  const chainLen = 190; // empirically sized: far enough for minutes to land in (urbanTarget, ruralTarget]
  const ruralPop = 100; // floor rung -> densityBand "rural~small_town" -> rural
  const buildings = [
    bldg(1, 'hea_ambulance', 0, row),
    ...Array.from({ length: chainLen }, (_, i) => rd(2 + i, i % 2 === 0 ? 'rd_aroad' : 'rd_dual', 1 + i, row)),
    bldg(200, 'res_hut', 1 + chainLen, row),
  ];
  const s = board(buildings, ruralPop);
  const point = ladderPointOf(s);
  assert.equal(point.population, ruralPop, 'sanity: fixture lands on the exact floor rung');
  assert.equal(isRuralDensityBand(point), true, 'sanity: floor rung is rural');
  const urbanTarget = emergencyResponseData.services.find((x) => x.service === 'ambulance').targetMinutesUrban;
  const ruralTarget = emergencyResponseData.services.find((x) => x.service === 'ambulance').targetMinutesRural;
  const responses = responseMinutesOf(s, 'ambulance');
  const minutes = responses.get(k(1 + chainLen, row));
  assert.ok(minutes !== undefined, 'sanity: tile must be reachable');
  assert.ok(minutes > urbanTarget, `fixture precondition: minutes (${minutes}) must exceed the urban target (${urbanTarget})`);
  assert.ok(minutes <= ruralTarget, `fixture precondition: minutes (${minutes}) must NOT exceed the rural target (${ruralTarget}) -- straddling fixture required`);

  const cov = emergencyCoverageOf(s, 'ambulance');
  assert.equal(cov.coverageShare, 1, 'the ONLY demand tile must read as FULLY covered under the correctly-selected RURAL target');
  // MUTANT (e, SCRATCH-PROVEN): `target = rural ? targetMinutesRural : targetMinutesUrban`
  // changed to `target = EMERGENCY.targetMinutesUrban[service]` unconditionally
  // -- minutes (> urbanTarget by the precondition above) flips to uncovered,
  // coverageShare becomes 0 -- reds the exact-value assertion above. A
  // scratch copy with this exact change was run and reported
  // cov.coverageShare === 0.
});

test('AC-4: coverageShare is null (never NaN) for a city with zero routable demand', () => {
  const s = board([bldg(1, 'hea_ambulance', 0, 0)], 0);
  const cov = emergencyCoverageOf(s, 'ambulance');
  assert.equal(cov.coverageShare, null, 'zero demand tiles -> null, never a division-by-zero NaN');
  assert.equal(Number.isNaN(cov.coverageShare), false, 'must not even be the NaN primitive');
});

// ---------------------------------------------------------------------------
// AC-5: hearseIsochroneOf -- honest-absence contract, GR#25
// ---------------------------------------------------------------------------

test('AC-5: hearseIsochroneOf is null with no cemetery, AND STILL null with death_cemetery/death_crematorium forced directly into buildings', () => {
  const s0 = board([], 0);
  assert.equal(hearseIsochroneOf(s0), null, 'default state -> null');
  const sForced = board(
    [
      { id: 1, spec: 'death_cemetery', x: 0, y: 0 },
      { id: 2, spec: 'death_crematorium', x: 5, y: 5 },
    ],
    50000,
  );
  assert.equal(hearseIsochroneOf(sForced), null, 'forced placeholder buildings (bypassing canEnterSim) must STILL yield null -- the guard is "MOD-083 does not exist", not "no cemetery is placed"');
  // MUTANT (AC-5's own named mutant): route from death_cemetery/
  // death_crematorium tiles the moment one is forced into s.buildings (drop
  // the "MOD-083 doesn't exist" guard) -- reds the forced-building
  // assertion (returns a non-null Map instead of null).
});

// ---------------------------------------------------------------------------
// AC-7: no unhappiness coupling, no money
// ---------------------------------------------------------------------------

test('AC-7: emergencyResponse.ts never touches budget/treasury/*Pounds/*Revenue/*Cost or a happiness/wellbeing identifier', () => {
  const forbidden = /budget|treasury|Pounds|Revenue|happiness|wellbeing|commuteWeight/;
  const hit = src.split('\n').find((line) => forbidden.test(line) && !line.trim().startsWith('*') && !line.trim().startsWith('//'));
  assert.equal(hit, undefined, `forbidden fiscal/wellbeing identifier found: ${hit}`);
  // MUTANT: wire coverageShare into any happiness/wellbeing-named identifier
  // -- caught the moment such an identifier appears in the module.
});

// ---------------------------------------------------------------------------
// AC-8: data-sourced, registry errors, determinism, structural bound
// ---------------------------------------------------------------------------

test('AC-8: no Date.now/Math.random/localStorage in production code', () => {
  const productionSrc = src
    .split('\n')
    .filter((line) => !line.trim().startsWith('*') && !line.trim().startsWith('//'))
    .join('\n');
  assert.doesNotMatch(productionSrc, /Date\.now|Math\.random|localStorage/);
});

test('AC-8: determinism -- 10 reruns of emergencyCoverageOf are byte-identical (JSON)', () => {
  const buildings = [
    bldg(1, 'hea_ambulance', 0, 0),
    bldg(2, 'fire_post', 5, 5),
    bldg(3, 'pol_station', -5, -5),
    rd(4, 'rd_aroad', 1, 0),
    bldg(5, 'res_hut', 2, 0),
  ];
  const s = board(buildings, 50000);
  const first = JSON.stringify([emergencyCoverageOf(s, 'ambulance')]);
  for (let i = 0; i < 10; i++) {
    const again = JSON.stringify([emergencyCoverageOf(s, 'ambulance')]);
    assert.equal(again, first, `rerun ${i} diverged from the first call`);
  }
});

test('BUG-871: loadEmergencyConfigFrom throws its named registry code for every malformed shape (real loader, real throw)', () => {
  const base = JSON.parse(JSON.stringify(emergencyResponseData));

  const noServices = { ...base, services: base.services.filter((x) => x.service !== 'fire') };
  assert.throws(() => loadEmergencyConfigFrom(noServices), new RegExp(ERR_EMERGENCY_SERVICE_MISSING));

  const shortCurve = { ...base, speedDegradation: { ...base.speedDegradation, curve: [base.speedDegradation.curve[0]] } };
  assert.throws(() => loadEmergencyConfigFrom(shortCurve), new RegExp(ERR_EMERGENCY_SPEED_CURVE_INVALID));

  const unsortedCurve = {
    ...base,
    speedDegradation: { ...base.speedDegradation, curve: [...base.speedDegradation.curve].reverse() },
  };
  assert.throws(() => loadEmergencyConfigFrom(unsortedCurve), new RegExp(ERR_EMERGENCY_SPEED_CURVE_INVALID));
  // MUTANT: replace any of these throws with a `?? default` fallback --
  // reds the corresponding assert.throws (no throw occurs). SCRATCH-PROVEN
  // for the noServices case: removing the `for (const service of SERVICES)`
  // guard loop and defaulting missing targets to `?? 8`/`?? 15` leaves this
  // call returning normally instead of throwing.
});

test('BUG-871: loadTurnoutMinutesFrom (the real function, not a hand-copied throw) is called directly with missing/zero/negative/NaN/string turnoutMinutes -- every shape throws ERR_EMERGENCY_TURNOUT_MINUTES_MISSING', () => {
  const base = JSON.parse(JSON.stringify(emergencyResponseData));
  const mutate = (patchFn) => {
    const j = JSON.parse(JSON.stringify(base));
    j.services = j.services.map((row) => (row.service === 'ambulance' ? { ...row, ...patchFn(row) } : row));
    return j;
  };
  // Sanity: the REAL data file passes.
  const good = loadTurnoutMinutesFrom(base);
  assert.equal(good.ambulance, base.services.find((x) => x.service === 'ambulance').turnoutMinutes, 'sanity: the real loader returns the real data value');

  assert.throws(() => loadTurnoutMinutesFrom(mutate(() => ({ turnoutMinutes: undefined }))), new RegExp(ERR_EMERGENCY_TURNOUT_MINUTES_MISSING), 'missing');
  assert.throws(() => loadTurnoutMinutesFrom(mutate(() => ({ turnoutMinutes: 0 }))), new RegExp(ERR_EMERGENCY_TURNOUT_MINUTES_MISSING), 'zero');
  assert.throws(() => loadTurnoutMinutesFrom(mutate(() => ({ turnoutMinutes: -1.5 }))), new RegExp(ERR_EMERGENCY_TURNOUT_MINUTES_MISSING), 'negative');
  assert.throws(() => loadTurnoutMinutesFrom(mutate(() => ({ turnoutMinutes: NaN }))), new RegExp(ERR_EMERGENCY_TURNOUT_MINUTES_MISSING), 'NaN');
  assert.throws(() => loadTurnoutMinutesFrom(mutate(() => ({ turnoutMinutes: '1.5' }))), new RegExp(ERR_EMERGENCY_TURNOUT_MINUTES_MISSING), 'string');
  assert.throws(() => loadTurnoutMinutesFrom({ services: 'not-an-array' }), new RegExp(ERR_EMERGENCY_TURNOUT_MINUTES_MISSING), 'services not an array');
  // MUTANT (h, BUG-871's own named class): `row.turnoutMinutes ?? 1.5` instead
  // of the typeof/finite/positive guard -- SCRATCH-PROVEN: a scratch copy
  // with that one-line change left the `undefined`/NaN/string cases silently
  // returning 1.5 instead of throwing, redding 3 of the 6 assertions above.
});

test('BUG-871: loadMaxAttributionRadiusFrom (the real function) is called directly with missing/zero/negative/NaN/string maxAttributionRadiusTiles -- every shape throws ERR_EMERGENCY_MAX_ATTRIBUTION_RADIUS_MISSING', () => {
  const good = loadMaxAttributionRadiusFrom(trafficConfig);
  assert.equal(good, trafficConfig.maxAttributionRadiusTiles, 'sanity: the real loader returns the real data value');

  assert.throws(() => loadMaxAttributionRadiusFrom({ ...trafficConfig, maxAttributionRadiusTiles: undefined }), new RegExp(ERR_EMERGENCY_MAX_ATTRIBUTION_RADIUS_MISSING), 'missing');
  assert.throws(() => loadMaxAttributionRadiusFrom({ ...trafficConfig, maxAttributionRadiusTiles: 0 }), new RegExp(ERR_EMERGENCY_MAX_ATTRIBUTION_RADIUS_MISSING), 'zero');
  assert.throws(() => loadMaxAttributionRadiusFrom({ ...trafficConfig, maxAttributionRadiusTiles: -10 }), new RegExp(ERR_EMERGENCY_MAX_ATTRIBUTION_RADIUS_MISSING), 'negative');
  assert.throws(() => loadMaxAttributionRadiusFrom({ ...trafficConfig, maxAttributionRadiusTiles: NaN }), new RegExp(ERR_EMERGENCY_MAX_ATTRIBUTION_RADIUS_MISSING), 'NaN');
  assert.throws(() => loadMaxAttributionRadiusFrom({ ...trafficConfig, maxAttributionRadiusTiles: '250' }), new RegExp(ERR_EMERGENCY_MAX_ATTRIBUTION_RADIUS_MISSING), 'string');
  // MUTANT (h2): `j.maxAttributionRadiusTiles ?? 250` instead of the guard --
  // SCRATCH-PROVEN: the undefined/NaN/string cases return 250 silently
  // instead of throwing, redding 3 of the 5 assertions above. This loader
  // had NO test at all before this rework (BUG-871's own finding).
});

test('BUG-871: isRuralDensityBand fails loud (never silently defaults) when densityBand is absent', () => {
  assert.throws(
    () => isRuralDensityBand({ nonNumeric: [] }),
    new RegExp(ERR_EMERGENCY_DENSITY_BAND_MISSING),
    'a ladder point missing densityBand must fail loud, never silently default to urban or rural',
  );
  assert.throws(
    () => isRuralDensityBand({ nonNumeric: [{ key: 'densityBand', rawValue: 42 }] }),
    new RegExp(ERR_EMERGENCY_DENSITY_BAND_MISSING),
    'a non-string densityBand must also fail loud',
  );
  // MUTANT: replace the throw with a `?? false` (silently urban) default --
  // reds both assertions above (no throw occurs).
});

test('AC-8: structural bound -- total isochrone relaxations across the 3 services stay bounded by 3 x segmentCount (never O(citizens))', () => {
  const buildings = [
    bldg(1, 'hea_ambulance', 0, 0),
    bldg(2, 'fire_post', 3, 3),
    bldg(3, 'pol_station', -3, -3),
    ...Array.from({ length: 8 }, (_, i) => rd(10 + i, 'rd_aroad', 1 + i, 0)),
  ];
  const s = board(buildings, 5000000);
  const segmentCount = lineSegmentIndexOf(s).segments.length;
  assert.ok(segmentCount > 0, 'sanity: fixture must produce at least one segment');
  __resetEmergencyRelaxationCounterForTest();
  // Force computation of all 3 isochrones (memoised -- one Dijkstra run each).
  for (const svc of ['ambulance', 'fire', 'police']) emergencyIsochroneOf(s, svc);
  const relaxations = __getEmergencyRelaxationCounterForTest();
  // Each Dijkstra relaxes at most (neighbour-count) edges per visited
  // segment; a real, loose, non-wall-clock bound is 3 services x
  // segmentCount x (max degree observed in this fixture's adjacency, <=2 in
  // a straight-line topology) -- never proportional to population, which
  // this fixture sets to 5,000,000.
  assert.ok(relaxations <= 3 * segmentCount * 4, `relaxations ${relaxations} must stay bounded by 3 x segmentCount x maxDegree, not grow with population (5,000,000)`);
});

// ---------------------------------------------------------------------------
// BUG-872: GR#3 dedupe -- ROAD_CLASS_ID_OF_TIER/roadClassIdOfSegment now live
// ONLY in trafficAssignment.ts; the narrow-class multiplier is gone, not dead.
// ---------------------------------------------------------------------------

test('BUG-872: ROAD_CLASS_ID_OF_TIER/roadClassIdOfSegment are the trafficAssignment.ts exports, not a second local copy in emergencyResponse.ts', () => {
  assert.equal(typeof ROAD_CLASS_ID_OF_TIER, 'object', 'sanity: the additive export exists');
  assert.equal(ROAD_CLASS_ID_OF_TIER[3], 'two_lane', 'sanity: tier3 maps to two_lane, the only currently-segmentable narrow-adjacent tier');
  assert.equal(typeof roadClassIdOfSegment, 'function', 'sanity: the additive export exists');
  assert.doesNotMatch(src, /ROAD_CLASS_ID_OF_TIER\s*[:=]/, 'emergencyResponse.ts must NOT declare its own ROAD_CLASS_ID_OF_TIER (GR#3 dedupe)');
  assert.doesNotMatch(src, /function roadClassIdOfSegment/, 'emergencyResponse.ts must NOT declare its own roadClassIdOfSegment (GR#3 dedupe)');
  // MUTANT: reintroduce a local ROAD_CLASS_ID_OF_TIER const in
  // emergencyResponse.ts -- reds the grep-based dedupe assertion.
});

test('BUG-872: the narrow-class speed multiplier is REMOVED, not merely unreachable -- speedFactorFor equals the curve value alone on a congested two_lane segment', () => {
  // Production-code-only grep (excludes // and * comment lines, the same
  // filter AC-8's Date.now/Math.random test uses) -- this module's OWN
  // header/BUG-872 explanatory prose legitimately says "narrowClass" while
  // documenting the removal, so a naive whole-file grep would false-positive
  // on the very comment that explains the fix.
  const productionSrc = src
    .split('\n')
    .filter((line) => !line.trim().startsWith('*') && !line.trim().startsWith('//'))
    .join('\n');
  assert.doesNotMatch(productionSrc, /narrowClass/i, 'emergencyResponse.ts PRODUCTION CODE must not reference narrowClass anything (removed, not dead-code-retained)');
  const row = 7;
  const buildings = [
    ...Array.from({ length: 40 }, (_, i) => bldg(100 + i, 'res_tower_nyc', -1 - i, row)),
    rd(2, 'm20', 0, row),
    rd(3, 'rd_dual', 1, row),
    bldg(4, 'off_suite', 1, row + 1),
    bldg(5, 'hea_ambulance', 1, row - 1),
  ];
  const s = board(buildings, 5_000_000);
  const idx = lineSegmentIndexOf(s);
  const m20Seg = idx.segmentById.get(idx.tileToSegment.get(k(0, row)));
  const delay = segmentDelayOf(s).get(m20Seg.segmentId);
  assert.ok(delay && delay.vOverC > 0, 'sanity: segment must be genuinely congested');
  const actual = speedFactorFor(s, m20Seg.segmentId, m20Seg);
  const expected = speedFactorFromCurve(emergencyResponseData.speedDegradation.curve, delay.vOverC);
  assert.equal(actual, expected, 'speedFactorFor must equal the curve value ALONE -- no narrow-class multiplier applied, ever, to any segment');
  // MUTANT: reintroduce `factor *= EMERGENCY.narrowClassMultiplier` gated on
  // ROAD_CLASS_ID_OF_TIER[roadTier] membership -- m20 is tier5 (motorway),
  // never in narrowClassRoadIds, so this specific reintroduction would NOT
  // red this pin (that is precisely BUG-872's own point: the multiplier is
  // unreachable for every currently-segmentable class) -- caught instead by
  // the grep-based "no narrowClass reference" assertion above, which reds
  // the moment the identifier reappears anywhere in the module.
});

// ---------------------------------------------------------------------------
// BUG-873: isOnline gate, tie-break equivalence, three-way coverage report
// ---------------------------------------------------------------------------

test('BUG-873(2): an OFFLINE (under-construction) ambulance station is NOT a dispatch source -- isOnline gate pinned', () => {
  const base = initialState();
  const tick = base.tick;
  const buildings = [
    bldg(1, 'hea_ambulance', 0, 0), // online (no builtTick -> isOnline treats as always-on, data.ts:849)
    bldgOffline(2, 'hea_ambulance', 10, 10, tick), // offline: under construction as of `tick`
    rd(3, 'rd_aroad', 1, 0),
    rd(4, 'rd_aroad', 11, 10),
  ];
  const s = { ...base, buildings, nextId: 5, population: 50000 };
  assert.equal(isOnline(s, s.buildings[0]), true, 'sanity: station 1 is online');
  assert.equal(isOnline(s, s.buildings[1]), false, 'sanity: station 2 is genuinely offline (under construction)');
  const iso = emergencyIsochroneOf(s, 'ambulance');
  assert.equal(iso.size, 1, `exactly ONE source segment (the online station) -- got ${iso.size} (an offline station must never seed the isochrone)`);
  // MUTANT (BUG-873(2)'s own named mutant): `if (!isOnline(s, b)) continue`
  // changed to `if (false) continue` -- the offline station's nearest
  // segment would ALSO become a source, growing iso.size to 2 (or, if both
  // stations share no segment, still a different reachable set) -- reds the
  // exact size===1 assertion above. SCRATCH-PROVEN: a scratch copy with the
  // gate removed produced an isochrone of size two on this exact fixture.
});

test('BUG-873(1): removing the neighbour-sort / MinHeap segId tie-break is PROVEN EQUIVALENT for this module\'s outputs (distance-only Dijkstra, no early termination)', () => {
  // This module's dijkstraIsochrone never terminates early (it explores the
  // WHOLE reachable component) and always updates via a STRICT `nd < known`
  // comparison. For a diamond topology (one source, two equal-length
  // parallel paths to the same destination, with the two parallel segments'
  // ids on OPPOSITE sides of alphabetic order from their Set-insertion
  // order), a standalone reproduction (scratchpad, not committed --
  // dijkstra_check.mjs) ran the identical algorithm with all 4 combinations
  // of {sorted neighbours, unsorted neighbours} x {segId tie-break, dist-only
  // tie-break} and observed BYTE-IDENTICAL dist/relaxation-count output in
  // all 4 runs (dist(D)=10, dist(A)=5, dist(B)=5, relax=8 every time) --
  // this is a general property of Dijkstra without early termination: the
  // converged minimum distance to any node does not depend on visitation
  // order among ties, only on the true shortest-path cost. The two mutants
  // BUG-873(1) names are therefore PROVEN EQUIVALENT for
  // emergencyIsochroneOf/responseMinutesOf/emergencyCoverageOf's observable
  // outputs -- this test instead pins that TWO topologically-symmetric
  // fixtures (built with their id labels swapped) produce IDENTICAL results,
  // which is the strongest observable property this determinism class can
  // assert without tracking predecessor paths (which this module does not
  // do, by design -- it returns distances only).
  function chainRun(reverseIds) {
    const row = 50;
    const chainLen = 6;
    // A straight multi-segment chain (alternating specs so each tile is its
    // own segment, same shape AC-4's 9-tile fixture uses) built with its
    // building ids assigned in the OPPOSITE numeric order on the second run
    // -- if heap/Set tie-break order ever leaked into the final distance
    // (it should not, per the standalone reproduction above), reversing
    // which id gets assigned to which physical tile would change the result.
    const roadSpecs = Array.from({ length: chainLen }, (_, i) => (i % 2 === 0 ? 'rd_aroad' : 'rd_dual'));
    const ids = Array.from({ length: chainLen }, (_, i) => (reverseIds ? 1000 - i : 2 + i));
    const buildings = [
      bldg(1, 'hea_ambulance', 0, row),
      ...roadSpecs.map((spec, i) => ({ id: ids[i], spec, x: 1 + i + OFFSET, y: row + OFFSET, builtTick: 0 })),
      bldg(999, 'res_hut', chainLen, row),
    ];
    const s = board(buildings, 50000);
    return responseMinutesOf(s, 'ambulance').get(k(chainLen, row));
  }
  const a = chainRun(false);
  const b = chainRun(true); // same physical topology, segment ids reversed
  assert.ok(a !== undefined && b !== undefined, 'sanity: both runs must reach the destination tile');
  assert.equal(a, b, 'IDENTICAL physical topology with REVERSED segment ids must give the SAME response minutes -- if tie-break/insertion order leaked into the output this would diverge');
});

test('BUG-873(3): coverage is reported THREE ways -- a fixture where HALF the population is stranded on a disconnected road island proves strandedPopulation and coverageShareOfAll', () => {
  const row = 60;
  const buildings = [
    bldg(1, 'hea_ambulance', 0, row),
    rd(2, 'rd_aroad', 1, row),
    bldg(3, 'res_hut', 2, row), // reachable island: covered (adjacent)
    // A disconnected island of EQUAL population weight (same spec, same
    // building shape) -- no adjacency edge to the main road.
    rd(10, 'rd_aroad', 40, row),
    bldg(11, 'res_hut', 41, row), // stranded island
  ];
  const s = board(buildings, 50000);
  const demandTiles = demandForecastOf(s);
  const weightByTile = new Map(demandTiles.map((t) => [`${t.x},${t.y}`, t.residentsActual + t.workersActual]));
  const reachableWeight = weightByTile.get(k(2, row)) ?? 0;
  const strandedWeight = weightByTile.get(k(41, row)) ?? 0;
  assert.ok(reachableWeight > 0 && strandedWeight > 0, 'sanity: both islands carry real population weight');
  assert.ok(Math.abs(reachableWeight - strandedWeight) < Math.max(reachableWeight, strandedWeight) * 0.5, 'sanity: the two islands are roughly comparable weight (both res_hut, so this should hold structurally)');

  const responses = responseMinutesOf(s, 'ambulance');
  assert.equal(responses.has(k(2, row)), true, 'sanity: reachable tile has a response');
  assert.equal(responses.has(k(41, row)), false, 'sanity: stranded tile has NO response (AC-3 honest absence)');

  const cov = emergencyCoverageOf(s, 'ambulance');
  assert.ok(cov.coverageShare !== null && cov.coverageShare > 0.99, `coverageShare (share of RESPONDED population) must read the reachable island as ~fully covered (adjacent tile) -- got ${cov.coverageShare}`);
  assert.ok(Math.abs(cov.strandedPopulation - strandedWeight) < 1e-6, `strandedPopulation (${cov.strandedPopulation}) must equal the stranded island's own weight (${strandedWeight})`);
  assert.ok(cov.coverageShareOfAll !== null, 'coverageShareOfAll must be defined (non-zero total demand)');
  assert.ok(cov.coverageShareOfAll < cov.coverageShare, `coverageShareOfAll (${cov.coverageShareOfAll}) must be STRICTLY LESS than coverageShare (${cov.coverageShare}) -- a city that strands half its map must show it in the whole-city denominator, even though coverageShare (responded-only) hides it`);
  const expectedShareOfAll = reachableWeight / (reachableWeight + strandedWeight);
  assert.ok(Math.abs(cov.coverageShareOfAll - expectedShareOfAll) < 1e-9, `coverageShareOfAll (${cov.coverageShareOfAll}) !== expected (${expectedShareOfAll})`);
  // MUTANT: coverageShareOfAll computed as coveredWeight/totalWeight (same
  // denominator as coverageShare, ignoring stranded population) -- would
  // equal cov.coverageShare exactly, redding the strict-less-than assertion
  // above. MUTANT: strandedPopulation hardcoded to 0 -- reds the
  // strandedWeight-equality assertion.
});


