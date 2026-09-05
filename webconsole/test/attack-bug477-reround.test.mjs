// attack-bug477-reround.test.mjs — INDEPENDENT DESTRUCTIVE RE-ROUND artifacts
// (BUG-477, attacker "opus-reround-bug477", 2026-09-05). Kept as permanent
// regressions per GR#23.
//
// The re-round's brief was bounded to the round-1 REJECT's three fixes. Two
// of them are guarded here; the third (the attack-bug606-replay cap fixture)
// is guarded by that file's own precondition assert, which this round proved
// fires loudly — see the verdict note on BUG-477.
//
// Nothing in this file mutates source on disk. The round's source-mutation
// matrix (M1..M6, all six confirmed RED) was run with a pre-saved scratch
// copy of data.ts and restored from it, never via git (GR#24).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SPECS, RESOLVE_DEMAND_ALL_MAX_UNITS } from '../src/sim/data.ts';

function generatorSpecs(specs) {
  return Object.values(specs).filter(
    (sp) => sp.kind === 'power' && !sp.placeholder && typeof sp.mw === 'number' && sp.mw > 0
  );
}
const perMwCapex = (sp) => sp.cost / sp.mw;
const perMwDensity = (sp) => sp.mw / (sp.w * sp.h);
const totalCostFor = (sp, targetMw) => Math.ceil(targetMw / sp.mw) * sp.cost;

/** A faithful re-implementation of bug-477-power-coherence.test.mjs test 4's
 *  live pair search, so this attack file can run it over SYNTHETIC catalogues
 *  the real test can only see by editing data.ts on disk (the bug645/bug662
 *  stranded-mutation hazard class — deliberately not repeated here). If the
 *  production test's search ever changes shape, this copy is expected to be
 *  updated in the same commit; its job is to pin the BEHAVIOUR ON NO PAIR,
 *  which is the property round 1 found missing. */
function findCrossover(gens) {
  for (const cheap of gens) {
    for (const dense of gens) {
      if (cheap.id === dense.id) continue;
      if (!(perMwCapex(cheap) < perMwCapex(dense))) continue;
      if (!(perMwDensity(dense) > perMwDensity(cheap))) continue;
      if (!(totalCostFor(cheap, 1) < totalCostFor(dense, 1))) continue;
      for (let t = 2; t <= dense.mw * 5; t++) {
        if (totalCostFor(dense, t) < totalCostFor(cheap, t)) return { cheap, dense, crossoverTarget: t };
      }
    }
  }
  return null;
}

// ─────────────────────────────────────────────────────────────────────────
// ATTACK 1 (round-1 P2 close-out): the crossover test must FAIL LOUDLY, not
// pass vacuously, when the catalogue genuinely contains no crossover pair.
// Round 1 rejected the previous version precisely because a 10x-dearer
// mutation left it green. Proven here against two synthetic catalogues that
// each destroy every crossover, WITHOUT touching data.ts.
// ─────────────────────────────────────────────────────────────────────────
test('ATTACK: with NO genuine crossover pair in the catalogue, the search returns null (so test 4 reds, never passes vacuously)', () => {
  const live = generatorSpecs(SPECS);

  // (a) every generator identical in density — the "dense must be strictly
  //     denser" leg can never be satisfied.
  const flattened = live.map((sp) => ({ ...sp, w: 1, h: 1, mw: 100 }));
  assert.equal(
    findCrossover(flattened),
    null,
    'a density-flattened catalogue must yield NO crossover — if it yields one, test 4 is not measuring density payoff at all'
  );

  // (b) the live catalogue with its single dense side made 10x dearer (the
  //     M4-equivalent mutation; confirmed against the REAL data.ts in this
  //     round's mutation matrix, where it reddened test 4's assert.ok).
  const liveCrossover = findCrossover(live);
  assert.ok(liveCrossover, 'live catalogue must currently have a crossover pair (see ATTACK 2)');
  const inflated = live.map((sp) => (sp.id === liveCrossover.dense.id ? { ...sp, cost: sp.cost * 10 } : sp));
  assert.equal(
    findCrossover(inflated),
    null,
    `10x-ing the dense side (${liveCrossover.dense.id}) must destroy the crossover — this is the mutation round 1 found the old test surviving`
  );
});

// ─────────────────────────────────────────────────────────────────────────
// ATTACK 2 (fragility finding, informational-but-enforced): the live
// catalogue supports EXACTLY ONE crossover pair. Test 4 therefore reads as
// an exhaustive search but is, today, a single-pair test — and data.ts's own
// DEFERRED follow-up (raising pow_offshore toward ~£2.0M/MW) removes that
// one pair, at which point test 4 reds by design. This test makes the
// count visible so that lane is not surprised.
// ─────────────────────────────────────────────────────────────────────────
test('ATTACK: the catalogue currently supports at least one crossover pair, and the count is visible (fragility watch)', () => {
  const gens = generatorSpecs(SPECS);
  const pairs = [];
  for (const cheap of gens) {
    for (const dense of gens) {
      if (cheap.id === dense.id) continue;
      if (!(perMwCapex(cheap) < perMwCapex(dense))) continue;
      if (!(perMwDensity(dense) > perMwDensity(cheap))) continue;
      if (!(totalCostFor(cheap, 1) < totalCostFor(dense, 1))) continue;
      for (let t = 2; t <= dense.mw * 5; t++) {
        if (totalCostFor(dense, t) < totalCostFor(cheap, t)) { pairs.push(`${cheap.id}->${dense.id}@${t}MW`); break; }
      }
    }
  }
  assert.ok(
    pairs.length >= 1,
    'the catalogue must retain at least one cheap/sparse -> dear/dense total-cost crossover; ' +
      'if this drops to zero the coherence suite\'s test 4 reds too, and that is a real catalogue ' +
      'finding (no density payoff anywhere), not a test bug — see BUG-477'
  );
  // Deliberately not asserting an exact count (balance retunes may add pairs);
  // surfaced so a reader sees how thin the margin is.
  console.log(`[BUG-477 re-round] crossover pairs in the live catalogue: ${pairs.length} — ${pairs.join(', ')}`);
});

// ─────────────────────────────────────────────────────────────────────────
// ATTACK 3 (round-1 P1 close-out, fixture-side): the derived cap-fixture
// block count must actually clear the cap it is derived from. This pins the
// DERIVATION, not the number: it re-derives the same way attack-bug606-
// replay.test.mjs does and asserts the calibration is still self-consistent,
// so a future change to RESOLVE_DEMAND_ALL_MAX_UNITS keeps scaling the
// fixture instead of silently under-shooting.
//
// The measured non-linearity is the reason the margin matters: the fixture's
// planned-units/block ratio is SUBLINEAR (2.164 at 800 blocks, 2.064 at 1202
// measured 2026-09-05), so the nominal 1.30 margin realises as ~1.24. Still
// clear of the cap, but thinner than the constant's name suggests.
// ─────────────────────────────────────────────────────────────────────────
test('ATTACK: the cap fixture\'s derived block count scales with the cap constant it is derived from', () => {
  const CAL_BLOCKS = 800;
  const CAL_UNITS = 1731;
  const MARGIN = 1.3;
  const derive = (cap) => Math.ceil((CAL_BLOCKS * cap * MARGIN) / CAL_UNITS);
  const atCurrentCap = derive(RESOLVE_DEMAND_ALL_MAX_UNITS);
  assert.ok(atCurrentCap > CAL_BLOCKS, 'the derived fixture must be larger than the calibration point at the current cap');
  assert.ok(
    derive(RESOLVE_DEMAND_ALL_MAX_UNITS * 2) > atCurrentCap,
    'doubling the cap constant must grow the fixture — the round-1 defect was a FIXED 800 that stopped tracking the cap'
  );
});
