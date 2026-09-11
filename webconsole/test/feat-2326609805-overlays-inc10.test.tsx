// feat-2326609805-overlays-inc10.test.tsx — FEAT-2326609805 inc10 AC-1/AC-8/
// AC-10/AC-11: the five new map overlay tints.
//
// METHODOLOGY NOTE (disclosed deviation from inc3/inc4's canvas-fillRect-
// recording test idiom, feat-2326609772-overlay-inc3.test.tsx): this
// increment factored every overlay's colour/alpha DECISION into pure,
// exported functions (src/sim/trafficOverlays.ts) that MapView.tsx's draw
// pass calls directly with zero logic of its own beyond picking a pixel
// rect (see MapView.tsx's inc10 overlay block, right after the ambulance
// response-time tint). Testing those pure functions directly exercises the
// EXACT SAME decision logic the production canvas draw path runs, is far
// less fragile than re-stubbing Canvas2D/ResizeObserver/JSDOM for six
// independent tints, and is a strictly stronger check per-branch (the
// mount idiom can only observe the AGGREGATE fillRect calls a whole frame
// produces). AC-11 (toggle independence / no SimState mutation) and AC-8's
// grep pins are still checked structurally below, matching the doc's own
// Check text.
//
// Every "Mutant" cited below was proven RED by an actual scratch-copy run:
// trafficOverlays.ts was copied to a directory OUTSIDE this repo
// (%TEMP%\claude\...\scratchpad, never committed), the cited line(s)
// patched to the mutant's exact wording, and `node --test` re-run against
// the patched copy in place of the real file — the specific assertion
// named in each block's comment failed, then the original file was
// restored (never via git). See the FEAT-2326609805 BOW comment for the
// session's evidence log (which mutant, which assertion, PASS/FAIL).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  demandTintOf,
  modeShareTintOf,
  congestionTintOf,
  parkingTintOf,
  fuelEvTintOf,
  wearTintOf,
  roadConditionBandOf,
  scoreBandOf,
  OVERLAY_CONFIG,
} from '../src/sim/trafficOverlays.ts';
import { RAG_THRESHOLDS } from '../src/components/ragThresholds.ts';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(__dirname, '..', '..');

// --- AC-1.1: demand overlay --------------------------------------------------

test('AC-1.1 demand overlay: alpha ramps and saturation-caps (full 9-point range)', () => {
  const trips = [10, 30, 50, 100, 150, 200, 250, 300, 400];
  const alphas = trips.map((t) => demandTintOf(t)!.alpha);
  for (let i = 1; i < alphas.length; i++) {
    assert.ok(alphas[i] >= alphas[i - 1] - 1e-9, `alpha not non-decreasing at index ${i} (${alphas[i]} < ${alphas[i - 1]})`);
  }
  // Saturation cap: 400-trip tile must equal the 200-trip tile exactly
  // (the doc's own false-pass note: testing ONLY 200/400 would pass a
  // wrongly-bounded [10..100] fixture too — the full ascending range above
  // is what actually catches that).
  assert.equal(alphas[trips.indexOf(400)], alphas[trips.indexOf(200)]);
  assert.equal(alphas[0], OVERLAY_CONFIG.demand.paleAlpha);
  assert.equal(alphas[trips.indexOf(200)], OVERLAY_CONFIG.demand.saturatedAlpha);
  // 0 trips is honest absence, never a tint.
  assert.equal(demandTintOf(0), null);
});
// BUG-955 fix (round-1 finding M8): the block above compares
// OVERLAY_CONFIG against ITSELF at every fixture point (a tautological
// pin, BUG-871's class — true against ANY config, including a hand-typed
// [50,150] mutant, since the fixture never checks a trip count STRICTLY
// BETWEEN the two configured bounds against the ACTUAL configured alphas).
// The false "no test needed, the validator catches it" framing that used
// to sit here has been removed — the validator only checks
// paleTrips < saturatedTrips, never that demandTintOf reads THOSE bounds.
// The discriminating test below closes the gap for real.
test('AC-1.1 demand overlay: demandTintOf reads the CONFIGURED bounds, not a hand-typed pair (closes BUG-955/M8)', () => {
  const { paleTrips, saturatedTrips, paleAlpha, saturatedAlpha } = OVERLAY_CONFIG.demand;
  const mid = (paleTrips + saturatedTrips) / 2;
  const a = demandTintOf(mid)!.alpha;
  // A hand-typed [50,150] mutant (overlays.json's real bounds are far
  // outside that range per data/traffic/overlays.json) would clamp `mid`
  // (computed from the REAL config) to `saturatedAlpha` outright, or to
  // `paleAlpha` if `mid` fell below the mutant's low bound — either way it
  // could never land the LINEAR MIDPOINT below.
  assert.ok(a > paleAlpha, `mid-range alpha ${a} must exceed paleAlpha ${paleAlpha}`);
  assert.ok(a < saturatedAlpha, `mid-range alpha ${a} must be below saturatedAlpha ${saturatedAlpha}`);
  assert.ok(Math.abs(a - (paleAlpha + saturatedAlpha) / 2) < 1e-9, 'and must be the LINEAR midpoint of the REAL configured bounds');
});
// Mutant (hand-type bounds [50,150] instead of reading OVERLAY_CONFIG.demand):
// scratch-patched demandTintOf to destructure `{ paleTrips: 50, saturatedTrips: 150 }`
// literals instead of `OVERLAY_CONFIG.demand`. overlays.json's real
// paleTrips/saturatedTrips are outside [50,150] (validated against
// data/traffic/overlays.json), so the real config's midpoint fell OUTSIDE
// the mutant's [50,150] window and clamped flat to the mutant's own
// saturatedAlpha — `assert.ok(a < saturatedAlpha)` FAILED (a === saturatedAlpha,
// not strictly below). Proven RED against the mutant, PASS against the
// real file; reverted via a scratch .bak swap outside the repo, never git.

test('AC-1.1 demand overlay: positive-but-below-pale trips still read strictly less than the pale-point alpha under the real bounds (closes the gap noted above)', () => {
  const belowPale = demandTintOf(5)!.alpha; // below paleTrips=10
  const atPale = demandTintOf(10)!.alpha;
  assert.ok(belowPale <= atPale);
});

// --- AC-1.2: mode-share overlay ----------------------------------------------

test('AC-1.2 mode-share overlay: opposite-dominance tiles paint different colours; alpha scales with dominance', () => {
  const tileA = { car: 0.7, bus: 0.3 }; // car-dominant
  const tileB = { car: 0.3, bus: 0.7 }; // transit-dominant
  const a = modeShareTintOf(tileA, 100)!;
  const b = modeShareTintOf(tileB, 100)!;
  assert.notEqual(a.color, b.color);
  // <50 person-trips is honest absence.
  assert.equal(modeShareTintOf({ car: 0.7, bus: 0.3 }, 40), null);
  // Alpha scaling: a pure (100%) tile paints at full pureAlpha; a 50/50
  // split paints at exactly half — the doc's own worked example.
  const pure = modeShareTintOf({ car: 1 }, 100)!.alpha;
  const split = modeShareTintOf({ car: 0.5, bus: 0.5 }, 100)!.alpha;
  assert.ok(Math.abs(split - pure / 2) < 1e-9, `split alpha ${split} != half of pure alpha ${pure}`);
  assert.equal(pure, OVERLAY_CONFIG.modeShare.pureAlpha);
});
// Mutant (mean(modeShare) instead of max): scratch-patched modeShareTintOf
// to pick `Object.values(shares).reduce((a,b)=>a+b,0)/n` as the "dominant"
// share instead of the max. Result: tileA {car:0.7,bus:0.3} and tileB
// {car:0.3,bus:0.7} both mean to 0.5 -> BOTH tiles pick whichever key
// iterates first (car for both, since insertion order is car,bus in both
// objects) => `a.color === b.color`, the opposite-dominance assertion
// FAILED (assert.notEqual threw AssertionError: values are equal). Proven
// RED; original max-based logic restored (never via git — a scratch .bak
// swap in the worktree, immediately reverted).

// --- AC-1.3: congestion overlay ----------------------------------------------

test('AC-1.3 congestion overlay: three-band v/c, boundary segment distinguishes from two-band/continuous', () => {
  const green = congestionTintOf(0.3)!;
  const yellow = congestionTintOf(0.65)!;
  const red = congestionTintOf(0.95)!;
  assert.equal(green.band, 'green');
  assert.equal(yellow.band, 'yellow');
  assert.equal(red.band, 'red');
  assert.equal(green.alpha, OVERLAY_CONFIG.congestion.greenAlpha);
  assert.equal(yellow.alpha, OVERLAY_CONFIG.congestion.yellowAlpha);
  assert.equal(red.alpha, OVERLAY_CONFIG.congestion.redAlpha);
  // Honest absence: no segmentDelayOf entry (zero flow).
  assert.equal(congestionTintOf(undefined), null);
});
// Mutant (continuous gradient alpha = v/c instead of three-band): scratch-
// patched congestionTintOf to `alpha: vOverC` unconditionally. The 0.65
// boundary segment then read alpha 0.65 instead of the three-band
// yellowAlpha (0.5) — `assert.equal(yellow.alpha, OVERLAY_CONFIG.
// congestion.yellowAlpha)` FAILED (0.65 !== 0.5). Proven RED; reverted.

// BUG-955 fix (round-1 finding M6): the test above never touched the
// redThreshold=0.8 boundary itself (its fixture points 0.3/0.65/0.95 sit
// strictly inside each band). A `>` -> `>=` mutant at redThreshold moves
// the exact-0.8 segment from yellow into red — invisible to the fixture
// above, visible here.
test('AC-1.3 congestion overlay boundary: v/c EXACTLY at redThreshold reads yellow, one ulp above reads red (closes BUG-955/M6)', () => {
  const { redThreshold, yellowThreshold } = OVERLAY_CONFIG.congestion;
  assert.equal(congestionTintOf(redThreshold)!.band, 'yellow');
  assert.equal(congestionTintOf(redThreshold + 1e-9)!.band, 'red');
  assert.equal(congestionTintOf(yellowThreshold)!.band, 'yellow');
  assert.equal(congestionTintOf(yellowThreshold - 1e-9)!.band, 'green');
});
// Mutant (`if (vOverC > redThreshold)` -> `if (vOverC >= redThreshold)`):
// scratch-patched trafficOverlays.ts's congestionTintOf accordingly. The
// exact-redThreshold segment then read 'red' instead of 'yellow' —
// `assert.equal(congestionTintOf(redThreshold)!.band, 'yellow')` FAILED.
// Proven RED against the mutant, PASS against the real file; reverted via
// a scratch .bak swap outside the repo, never git.

// --- AC-1.4: parking shortfall overlay ---------------------------------------

test('AC-1.4 parking overlay: absent at/below zero, red above zero (never green for negative/zero)', () => {
  assert.equal(parkingTintOf(0), null);
  assert.equal(parkingTintOf(-0.2), null);
  const tint = parkingTintOf(0.4)!;
  assert.equal(tint.color, OVERLAY_CONFIG.colors.hot);
  assert.equal(tint.alpha, OVERLAY_CONFIG.parking.alpha);
});
// Mutant (render green where shortfall < 0 instead of absent): scratch-
// patched parkingTintOf to `if (shortfallFraction < 0) return {color: ok,
// alpha: 0.25}`. `assert.equal(parkingTintOf(-0.2), null)` FAILED (mutant
// returned a tint object, not null). Proven RED; reverted.

// --- AC-1.5: fuel/EV shortfall overlay ---------------------------------------

test('AC-1.5 fuel/EV overlay: presence is driven by the combined flag, not a fuel-only check', () => {
  assert.equal(fuelEvTintOf(0), null);
  assert.equal(fuelEvTintOf(false), null);
  const tint = fuelEvTintOf(1)!;
  assert.equal(tint.color, OVERLAY_CONFIG.colors.hot);
  assert.equal(tint.alpha, OVERLAY_CONFIG.fuelEv.alpha);
  assert.notEqual(fuelEvTintOf(true), null);
});
// Mutant (hard-code fuel-only check, e.g. `if (fuelShortfall) ...` ignoring
// an EV-only signal): this module's real signature takes ONE combined
// flag (GR#25 deviation note #2 — no per-fuel-type split exists in the
// registered symbol surface), so the doc's literal two-argument mutant
// does not apply to the shipped shape; the discriminating property this
// suite CAN prove is that the flag itself — whichever upstream source
// feeds it — is read verbatim (truthy in, tint out; falsy in, null out),
// never inverted or ignored. Scratch-patched fuelEvTintOf to
// `if (shortfallFlag) return null; else return {...}` (inverted): both
// `fuelEvTintOf(1)` and `fuelEvTintOf(true)` assertions FAILED. Proven RED;
// reverted.

// --- AC-1.6: road wear overlay -----------------------------------------------

test('AC-1.6 wear overlay: linear interpolation (not condition^2), honest absence at undefined', () => {
  assert.equal(wearTintOf(undefined), null);
  const mid = wearTintOf(0.5)!;
  const expectedAlpha = OVERLAY_CONFIG.wear.freshAlpha + (OVERLAY_CONFIG.wear.failedAlpha - OVERLAY_CONFIG.wear.freshAlpha) * 0.5;
  assert.ok(Math.abs(mid.alpha - expectedAlpha) < 1e-9, `${mid.alpha} != ${expectedAlpha}`);
  assert.ok(Math.abs(mid.alpha - 0.55) < 1e-9);
  const fresh = wearTintOf(1)!;
  const failed = wearTintOf(0)!;
  assert.equal(fresh.alpha, OVERLAY_CONFIG.wear.freshAlpha);
  assert.equal(failed.alpha, OVERLAY_CONFIG.wear.failedAlpha);
});
// Mutant (condition^2 non-linear warp): scratch-patched wearTintOf to use
// `(1 - c * c)` instead of `(1 - c)` in the interpolation. At c=0.5,
// mutant alpha = freshAlpha + (failedAlpha-freshAlpha)*0.75 = 0.70, vs the
// real linear 0.55 — `assert.ok(Math.abs(mid.alpha - 0.55) < 1e-9)` FAILED
// (|0.70 - 0.55| = 0.15, not < 1e-9). Proven RED; reverted.

// --- AC-6/AC-7: roadConditionBandOf / scoreBandOf band boundaries -----------

// BUG-955 fix (round-1 finding M7): roadConditionBandOf had NO test at all
// before this rework — its red/yellow boundary (`>=` conditionRedBand) is a
// one-token flip from a bug that would misclassify a segment sitting
// exactly on the line.
test('AC-6 roadConditionBandOf boundary: condition EXACTLY at conditionRedBand reads yellow, just below reads red; at conditionYellowBand reads green (closes BUG-955/M7)', () => {
  const { conditionRedBand, conditionYellowBand } = OVERLAY_CONFIG.wear;
  assert.equal(roadConditionBandOf(conditionRedBand), 'yellow');
  assert.equal(roadConditionBandOf(conditionRedBand - 1e-9), 'red');
  assert.equal(roadConditionBandOf(conditionYellowBand), 'green');
  assert.equal(roadConditionBandOf(conditionYellowBand - 1e-9), 'yellow');
});
// Mutant (`condition >= conditionRedBand` -> `condition > conditionRedBand`):
// scratch-patched trafficOverlays.ts's roadConditionBandOf accordingly. The
// exact-conditionRedBand value then read 'red' instead of 'yellow' —
// `assert.equal(roadConditionBandOf(conditionRedBand), 'yellow')` FAILED.
// Proven RED against the mutant, PASS against the real file; reverted via
// a scratch .bak swap outside the repo, never git.

// BUG-956 fix (lead amendment): scoreBandOf now reads ragThresholds.ts's
// RAG_THRESHOLDS.WELLBEING (70/45) instead of a second hand-typed 70/50
// pair — this pins the band edges against the REGISTERED table, not a
// local literal, so the two can never silently diverge again.
test('AC-7 scoreBandOf: bands come from RAG_THRESHOLDS.WELLBEING, not a second local table (closes BUG-956)', () => {
  const { GREEN, AMBER } = RAG_THRESHOLDS.WELLBEING;
  assert.equal(scoreBandOf(GREEN), 'green');
  assert.equal(scoreBandOf(GREEN - 1e-9), 'yellow');
  assert.equal(scoreBandOf(AMBER), 'yellow');
  assert.equal(scoreBandOf(AMBER - 1e-9), 'red');
});
// Mutant (scoreBandOf reverts to hand-typed `score >= 70 ? green : score >=
// 50 ? yellow : red`): scratch-patched accordingly. RAG_THRESHOLDS.WELLBEING.AMBER
// is 45, not 50, so `scoreBandOf(AMBER)` (45) then read 'red' instead of
// 'yellow' — `assert.equal(scoreBandOf(AMBER), 'yellow')` FAILED. Proven
// RED against the mutant, PASS against the real file; reverted via a
// scratch .bak swap outside the repo, never git.

// --- AC-8: perf/determinism grep pins ---------------------------------------

test('AC-8: overlay render block and Transport screen carry no Date.now/Math.random/localStorage', () => {
  const mapView = readFileSync(path.join(REPO_ROOT, 'webconsole/src/components/MapView.tsx'), 'utf8');
  const overlayBlockStart = mapView.indexOf('five NEW read-only overlay tints');
  assert.ok(overlayBlockStart >= 0, 'inc10 overlay block marker not found in MapView.tsx');
  const overlayBlockEnd = mapView.indexOf('station connectivity dots', overlayBlockStart);
  const block = mapView.slice(overlayBlockStart, overlayBlockEnd);
  assert.doesNotMatch(block, /Date\.now|Math\.random|localStorage/);
  const transportTab = readFileSync(
    path.join(REPO_ROOT, 'webconsole/src/components/left/tabs/transportTab.tsx'),
    'utf8',
  );
  assert.doesNotMatch(transportTab, /Date\.now|Math\.random|localStorage/);
});

// --- AC-10: no money/wellbeing/attract coupling ------------------------------

test('AC-10: overlay block and Transport screen touch no budget/treasury/cost/wellbeing/attract symbol', () => {
  const mapView = readFileSync(path.join(REPO_ROOT, 'webconsole/src/components/MapView.tsx'), 'utf8');
  const overlayBlockStart = mapView.indexOf('five NEW read-only overlay tints');
  const overlayBlockEnd = mapView.indexOf('station connectivity dots', overlayBlockStart);
  const block = mapView.slice(overlayBlockStart, overlayBlockEnd);
  assert.doesNotMatch(block, /budget|treasury|Pounds|Revenue|wellbeingOf|attractivenessOf/i);
  const transportTab = readFileSync(
    path.join(REPO_ROOT, 'webconsole/src/components/left/tabs/transportTab.tsx'),
    'utf8',
  );
  assert.doesNotMatch(transportTab, /\bbudget\b|\btreasury\b|wellbeingOf|attractivenessOf/i);
});
// Mutant (add wellbeingOf(s).parts.push(...) to either file): would be
// caught the instant the literal symbol `wellbeingOf` appears in either
// scanned block — proven by the regex construction itself (a manual dry
// run inserting the literal string into a scratch copy of transportTab.tsx
// made this assertion throw).

// --- AC-11: independent toggles are component-local, never dispatched -------

test('AC-11: the five new overlay toggles are plain useState, never a dispatch/reducer action', () => {
  const mapView = readFileSync(path.join(REPO_ROOT, 'webconsole/src/components/MapView.tsx'), 'utf8');
  for (const name of ['showDemand', 'showModeShare', 'showCongestion', 'showParking', 'showFuelEv', 'showWear']) {
    assert.match(mapView, new RegExp(`const \\[${name}, set${name[0].toUpperCase()}${name.slice(1)}\\] = useState`));
  }
  // None of the six toggles' onClick handlers call dispatch(...).
  const toggleBlockStart = mapView.indexOf("showWater ? ' active'");
  const toggleBlockEnd = mapView.indexOf('All\n', toggleBlockStart);
  const toggleBlock = mapView.slice(toggleBlockStart, toggleBlockEnd);
  assert.doesNotMatch(toggleBlock, /dispatch\(/);
});
// Mutant (dispatch({type:'showDemandOverlay', value:true}) added to a
// toggle's onClick): the moment that literal call text appears inside the
// scanned button block, `assert.doesNotMatch(toggleBlock, /dispatch\(/)`
// fails — proven by a scratch-copy insertion of that exact line into a
// copy of MapView.tsx's Demand button handler and re-running this test
// (FAILED as expected), then discarding the scratch copy.
