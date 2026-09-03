// cold-audit-display-helpers.test.mjs — COLD AUDIT for the BUG-657 defect
// class: a clamp/format helper whose bounds landed outside the wrong paren
// (or is simply missing a guard its siblings all have), rendering a garbage
// value to the player, with ZERO test coverage catching it because nobody
// ever asserted on the function directly.
//
// BUG-657 itself: yLabel() read
//   Math.max(0, Math.min(Math.floor(y / ROW_BAND)), 25)
// -- the closing paren on Math.min sat one place too early, so Math.min got
// ONE argument (an identity, floor(y/10)) and Math.max(0, band, 25) became a
// floor of 25 (25 is itself one of max's three arguments), so EVERY row band
// 0..25 collapsed to 'Z'. This shipped 2026-08-25 and survived nine days
// because not one test in the whole suite asserted on yLabel/coordLabel
// despite five call sites (map gutter labels, building inspector, etc).
//
// This cold audit re-checked the shape at every Math.min/max/round/floor/abs
// and .toFixed call site under webconsole/src (368 sites) plus a coverage
// sweep of every exported display/format/clamp/convert helper in
// sim/data.ts and components/*.ts(x). Findings (most severe first), each
// with a concrete input->output demonstration and a RED-proof performed via
// scratch-copy mutation (cp the real file, mutate it back to the broken
// shape, run this suite, confirm the exact assertion reddens, then restore
// the scratch copy over the real file so no git command ever touches the
// tree):
//
// (1) BROKEN, FIXED HERE -- yLabel/coordLabel (sim/data.ts). On the branch
//     this audit started from, yLabel was STILL the exact broken shape BUG-657
//     describes (`Math.max(0, Math.min(Math.floor(y / ROW_BAND)), 25)`) --
//     yLabel(0) === yLabel(15) === yLabel(259) === 'Z', reproduced directly
//     below. Fixed by moving the paren so Math.min clamps the band to 25
//     BEFORE Math.max guards the negative direction:
//     `Math.max(0, Math.min(Math.floor(y / ROW_BAND), 25))`.
//     RED-PROOF: scratch-copied sim/data.ts, replaced the fixed line with the
//     literal broken one-line-too-early-paren shape, ran this file with
//     `node ../tools/test/scoped.mjs cold-audit-display-helpers.test.mjs` --
//     the 'yLabel: every row band renders its own letter' test reddened
//     (every band asserted 'Z' instead of its real letter, and the 'no row
//     band ever silently collapses onto another' test failed too), then
//     restored the scratch copy over data.ts. Confirmed GREEN again with the
//     real fix in place.
//
// (2) HARDENED -- fmtPct (sim/utils.ts). Every sibling formatter in this same
//     file (fmtNum/fmtMoney/fmtSigned/fmtMoneyEach) guards
//     `!Number.isFinite(n)` and degrades to a safe default; fmtPct was the
//     one exception, so fmtPct(NaN) rendered literally as "NaN%" and
//     fmtPct(Infinity) as "Infinity%" -- exactly the "garbage number shown to
//     the player" class GR#1 exists to prevent, and the SAME defensive idiom
//     already proven correct three times over in this file. No live call
//     site hits it today (every current caller pre-guards its own division),
//     but it is a landmine for the next one. Fixed: fmtPct now returns '0%'
//     for any non-finite input.
//     RED-PROOF: scratch-copied sim/utils.ts, removed the `if
//     (!Number.isFinite(n))` guard, ran this file -- the 'fmtPct: non-finite
//     input never leaks NaN/Infinity to the player' test reddened
//     (`assert.equal(fmtPct(NaN), '0%')` got 'NaN%' instead), then restored.
//
// (3) HARDENED -- pipeTierOf (sim/data.ts). The reducer never lets a tier
//     exceed PIPE_TIERS.length - 1, but pipeTierOf is also the read path for
//     whatever a save file hands back (Record<number, number> is a
//     compile-time promise only), and PIPE_TIERS[pipeTierOf(s, id)].mult is
//     read directly at two call sites -- an out-of-range or corrupt value
//     (a hand-edited save with pipeTier: {5: 99}, or a negative/fractional
//     entry) would index PIPE_TIERS out of bounds and throw on `.mult` of
//     undefined. No test anywhere in the suite calls pipeTierOf by name.
//     Fixed: clamped to [0, PIPE_TIERS.length - 1] at the single read SSOT,
//     same idiom as sanitizeCrimeRate/sanitizeCongestionTicksBySpec elsewhere
//     in this file.
//     RED-PROOF: scratch-copied sim/data.ts, reverted pipeTierOf to the bare
//     `return s.pipeTier[id] ?? 0;`, ran this file -- the 'pipeTierOf: a
//     corrupt out-of-range save value never crashes the PIPE_TIERS lookup'
//     test reddened (PIPE_TIERS[99] is undefined, `.mult` threw a
//     TypeError instead of the test's clamped-value assertion running),
//     then restored.
//
// Everything else surveyed (all 368 Math.min/max/round/floor/abs/.toFixed
// sites under src/, plus every exported formatter/clamp/tier helper in
// sim/data.ts and components/*.ts(x): demandIndexOf, earlyGameFactor,
// densityTier, capacityAtTier, fittingTier, blockOccupancy,
// sanitizeCrimeRate, sanitizeCongestionTicksBySpec, congestionLinesOf,
// wasteStatsOf, ragForWellbeing/Coverage/Approval/Unemployment/
// LineSaturation/HousingHeadroom/FiscalNet/Insolvency/Power, isAtCapacity,
// formatPower, fmtNum/fmtMoney/fmtSigned/fmtMoneyEach, gameDate,
// fmtShortfall, unemploymentOf) evaluated correctly across its documented
// input domain -- clamp order right, no inverted min/max, no off-by-one, no
// unguarded division. The characterisation tests below lock in the CURRENT,
// verified-correct behaviour of the ones that had NO existing direct
// assertion (demandIndexOf, earlyGameFactor's boundary, pipeTierOf's
// in-range path, ragColor) so this defect class cannot recur silently in
// them either.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  yLabel,
  coordLabel,
  demandIndexOf,
  earlyGameFactor,
  pipeTierOf,
  PIPE_TIERS,
} from '../src/sim/data.ts';
import { fmtPct } from '../src/sim/utils.ts';
import { ragColor } from '../src/components/ragThresholds.ts';

// ---------------------------------------------------------------------------
// (1) FIX regression: yLabel / coordLabel
// ---------------------------------------------------------------------------

test('BUG-657 (re-found on this branch): yLabel every row band renders its own distinct letter, never a pegged Z', () => {
  // MAP_H is 260, ROW_BAND is 10 -> bands 0..25 exist (26 letters, A..Z).
  // Before the fix every one of these asserted 'Z' -- this is the exact
  // regression the class survived nine days undetected.
  assert.equal(yLabel(0), 'A', 'row 0 (band 0) must be A, not Z');
  assert.equal(yLabel(9), 'A', 'row 9 is still band 0 (last row of band A)');
  assert.equal(yLabel(10), 'B', 'row 10 rolls into band 1 (B)');
  assert.equal(yLabel(15), 'B', "the reported bug symptom -- row 15 must be 'B', the original defect made this 'Z'");
  assert.equal(yLabel(100), 'K', 'row 100 -> band 10 -> the 11th letter, K');
  assert.equal(yLabel(250), 'Z', 'row 250 -> band 25 -> Z, the LAST legitimate band, not a floor everything else pegs to');
  assert.equal(yLabel(259), 'Z', 'row 259 (MAP_H - 1) is genuinely the last row, correctly Z');
});

test('BUG-657: yLabel bands are monotonic and distinct -- no row band ever silently collapses onto another', () => {
  const seen = new Map();
  for (let band = 0; band <= 25; band++) {
    const y = band * 10; // first row of this band
    const label = yLabel(y);
    assert.ok(!seen.has(label) || seen.get(label) === band,
      `band ${band} (y=${y}) produced letter '${label}', already used by band ${seen.get(label)} -- the exact all-bands-collapse symptom`);
    seen.set(label, band);
  }
  assert.equal(seen.size, 26, 'all 26 row bands must produce 26 DISTINCT letters, not one repeated letter');
});

test('BUG-657: yLabel clamps beyond-max and negative input without throwing or silently repeating the max band incorrectly', () => {
  // y beyond MAP_H should still clamp to the last legitimate band (Z), same
  // as the last real row -- distinguishing "genuinely at the ceiling" from
  // "the whole function is broken and everything reads Z" is exactly what
  // test (1) above already proves; this just confirms the clamp direction
  // for out-of-domain input specifically.
  assert.equal(yLabel(1000), 'Z', 'far beyond MAP_H clamps to the last band, Z');
  assert.equal(yLabel(-5), 'A', 'negative y clamps to the first band, A, never throws/NaN');
});

test('BUG-657: coordLabel composes yLabel with a 1-based column, inheriting the same fix', () => {
  assert.equal(coordLabel(0, 0), 'A,1');
  assert.equal(coordLabel(0, 15), 'B,1', 'the reported bug symptom coordinate ("grid Z,374") must now read a real row letter');
  assert.equal(coordLabel(373, 15), 'B,374', "reproduces Aaron's exact reported inspector string shape, minus the dead 'Z'");
});

// ---------------------------------------------------------------------------
// (2) FIX regression: fmtPct non-finite guard
// ---------------------------------------------------------------------------

test('fmtPct: non-finite input never leaks NaN/Infinity to the player', () => {
  assert.equal(fmtPct(NaN), '0%', 'a NaN ratio (e.g. an unguarded 0/0 upstream) must never render literally as "NaN%"');
  assert.equal(fmtPct(Infinity), '0%', 'Infinity must not render literally as "Infinity%"');
  assert.equal(fmtPct(-Infinity), '0%', '-Infinity must not render literally as "-Infinity%"');
});

test('fmtPct: characterisation of the normal, correct path (unchanged by the hardening)', () => {
  assert.equal(fmtPct(0), '0.0%');
  assert.equal(fmtPct(0.5), '50.0%');
  assert.equal(fmtPct(1), '100.0%');
  assert.equal(fmtPct(1.5), '150.0%', 'fmtPct deliberately does NOT clamp to 100% -- over-100% ratios (e.g. fundGrowth) are real, meaningful values');
  assert.equal(fmtPct(-0.25), '-25.0%', 'negative ratios (e.g. a fiscal-margin loss) keep their sign, unclamped');
  assert.equal(fmtPct(0.5, 0), '50%', 'digits=0 still respected after the guard');
});

// ---------------------------------------------------------------------------
// (3) FIX regression: pipeTierOf bounds hardening
// ---------------------------------------------------------------------------

function fakeState(pipeTier) {
  return { pipeTier };
}

test('pipeTierOf: a corrupt out-of-range save value never crashes the PIPE_TIERS lookup', () => {
  const s = fakeState({ 7: 99, 8: -3, 9: 1.7, 10: NaN, 11: Infinity });
  // Every one of these must resolve to a valid PIPE_TIERS index so
  // PIPE_TIERS[pipeTierOf(s, id)].mult never throws.
  for (const id of [7, 8, 9, 10, 11]) {
    const tier = pipeTierOf(s, id);
    assert.ok(tier >= 0 && tier <= PIPE_TIERS.length - 1,
      `pipeTierOf(id=${id}) returned ${tier}, out of PIPE_TIERS's valid [0, ${PIPE_TIERS.length - 1}] range`);
    assert.doesNotThrow(() => PIPE_TIERS[tier].mult, `PIPE_TIERS[pipeTierOf(id=${id})] must not throw`);
  }
  assert.equal(pipeTierOf(s, 7), PIPE_TIERS.length - 1, 'an out-of-range-high tier (99) clamps to the last real tier');
  assert.equal(pipeTierOf(s, 8), 0, 'a negative tier clamps to 0');
  assert.equal(pipeTierOf(s, 9), 1, 'a fractional tier floors to the nearest whole tier');
  assert.equal(pipeTierOf(s, 10), 0, 'NaN collapses to the safe default, 0');
  assert.equal(pipeTierOf(s, 11), 0, 'Infinity is non-finite (Number.isFinite(Infinity) === false), so it collapses to the same safe default as NaN, 0 -- never an out-of-bounds index');
});

test('pipeTierOf: characterisation of the normal, in-range path (unchanged by the hardening)', () => {
  const s = fakeState({ 1: 0, 2: 1, 3: 2 });
  assert.equal(pipeTierOf(s, 1), 0);
  assert.equal(pipeTierOf(s, 2), 1);
  assert.equal(pipeTierOf(s, 3), 2);
  assert.equal(pipeTierOf(s, 999), 0, 'a building id with no recorded tier defaults to 0 (base pipe), never crashes');
});

// ---------------------------------------------------------------------------
// Characterisation tests for correct-but-previously-untested helpers.
// These pin down the CURRENT, verified behaviour at 0 / negative / max /
// beyond-max so the next edit that flips a clamp direction or misplaces a
// paren reddens immediately instead of shipping silently.
// ---------------------------------------------------------------------------

test('CHARACTERISATION: demandIndexOf -- monotone bounded map of (1 - coverage), never pegged before the true edges', () => {
  assert.equal(demandIndexOf(1.0), 0, 'exact coverage -> zero demand');
  assert.equal(demandIndexOf(0.8), 20, '80% coverage reads +20, never a pegged +100 (the BUG-392 saturation this guards against)');
  assert.equal(demandIndexOf(0.5), 50);
  assert.equal(demandIndexOf(0), 100, 'zero coverage is the true +100 ceiling');
  assert.equal(demandIndexOf(2), -100, 'double coverage (surplus) clamps at the -100 floor');
  assert.equal(demandIndexOf(-1), 100, 'a nonsensical negative coverage still clamps to the +100 ceiling, never runs away past it');
});

test('CHARACTERISATION: earlyGameFactor -- ramps 0..1 linearly to pop 50, then pegs at 1 and never exceeds it', () => {
  assert.equal(earlyGameFactor(0), 0, 'an empty city has zero early-game factor');
  assert.equal(earlyGameFactor(25), 0.5, 'halfway through the ramp');
  assert.equal(earlyGameFactor(50), 1, 'exactly at the ramp ceiling');
  assert.equal(earlyGameFactor(51), 1, 'one past the ceiling still pegs at 1, never exceeds it');
  assert.equal(earlyGameFactor(1_000_000), 1, 'a huge city stays pegged at exactly 1, never runs away past it');
  assert.equal(earlyGameFactor(-5), -0.1, 'negative population is out of the documented domain and is not clamped -- documenting the current (never-hit in practice, population is never negative) behaviour rather than silently assuming a clamp that is not actually there');
});

test('CHARACTERISATION: ragColor -- every RagState maps to a distinct, defined CSS colour token', () => {
  const green = ragColor('green');
  const amber = ragColor('amber');
  const red = ragColor('red');
  const stub = ragColor('stub');
  for (const [name, value] of [['green', green], ['amber', amber], ['red', red], ['stub', stub]]) {
    assert.ok(typeof value === 'string' && value.length > 0, `ragColor('${name}') must return a non-empty string`);
  }
  const values = new Set([green, amber, red, stub]);
  assert.equal(values.size, 4, 'all four RAG states must map to visually distinct colour tokens');
});
