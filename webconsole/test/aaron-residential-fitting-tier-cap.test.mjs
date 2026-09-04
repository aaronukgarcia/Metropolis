// aaron-residential-fitting-tier-cap.test.mjs — Aaron bug report 2026-09-04
// (verbatim, filed P1): "when i lay housing down why is an auto motorway
// laid down to support it?"
//
// fittingTier() (data.ts) feeds a dense building's `residents` count into the
// SAME `cap` throughput proxy a factory's `jobs`/freight uses, so a 400+
// resident tower crossed the score>=24 tier-5 (m20 motorway) threshold on
// housing density alone — residential traffic does not actually behave like
// industrial freight. Fix (PLACEHOLDER-balance, directional only): clamp the
// returned tier for `sp.kind === 'residential'` to at most 3 (A-road) AFTER
// the existing score ladder runs, so every other kind is untouched.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SPECS, fittingTier } from '../src/sim/data.ts';

test('every residential spec in SPECS fits at most tier 3 (A-road), derived from SPECS not a literal id', () => {
  const residential = Object.values(SPECS).filter((sp) => sp.kind === 'residential' && !sp.placeholder);
  assert.ok(residential.length > 0, 'precondition: at least one real residential spec exists');
  // The densest residential spec (by residents) is the one most likely to
  // have crossed the old tier-5 threshold — assert it directly, plus every
  // other residential spec for good measure.
  const densest = [...residential].sort((a, b) => (b.residents ?? 0) - (a.residents ?? 0))[0];
  assert.ok((densest.residents ?? 0) > 0, 'precondition: the densest residential spec actually carries residents');
  assert.ok(fittingTier(densest) <= 3, `${densest.id} (${densest.residents} residents) must fit at most tier 3, got ${fittingTier(densest)}`);
  for (const sp of residential) {
    assert.ok(fittingTier(sp) <= 3, `${sp.id} must fit at most tier 3 (residential never needs more than an A-road)`);
  }
});

test('control: a heavy industrial spec still reaches tier 4/5 — the residential clamp is not over-broad', () => {
  const industrial = Object.values(SPECS).filter((sp) => (sp.kind === 'industrial' || sp.kind === 'mine') && !sp.placeholder);
  assert.ok(industrial.some((sp) => fittingTier(sp) >= 4), 'at least one heavy industrial/mine spec must still reach tier 4+ (freight genuinely needs heavier roads)');
});

test('control: a landmark spec still reaches tier 4/5 — the clamp only touches residential', () => {
  const landmarks = Object.values(SPECS).filter((sp) => sp.kind === 'landmark' && !sp.placeholder);
  assert.ok(landmarks.some((sp) => fittingTier(sp) >= 4), 'at least one landmark must still reach tier 4+ (city arterials)');
});

test('RED-PROOF sanity: without the clamp, the densest residential spec WOULD have reached tier 5 on residents alone (score = area + residents*0.05 >= 24)', () => {
  const densest = Object.values(SPECS)
    .filter((sp) => sp.kind === 'residential' && !sp.placeholder)
    .sort((a, b) => (b.residents ?? 0) - (a.residents ?? 0))[0];
  const naiveScore = densest.w * densest.h + (densest.residents ?? 0) * 0.05;
  assert.ok(naiveScore >= 24, `precondition: ${densest.id}'s naive (unclamped) score (${naiveScore}) really did cross the old tier-5 threshold — proving the clamp is load-bearing, not a strawman`);
  // And the ACTUAL (clamped) result differs from the naive one.
  assert.notEqual(fittingTier(densest), 5, 'the clamp must actually change the outcome for this spec');
});
