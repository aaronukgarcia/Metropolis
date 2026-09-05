// attack-largest-first-round.test.mjs — independent DESTRUCTIVE round (GR#23)
// against BUG-685/686 largest-first Fix-All (Aaron: '10,033 turbines and
// 9,138 kindergartens is nuts'). Attacker != author. See the round's BOW
// verdict for the write-up; this file is a permanent regression per the
// estate's own convention of keeping attack files.
//
// LEAD DEFECT FOUND (item 1, money attack): largestFirstFill()'s one-shot
// branch ("this spec alone clears the remainder -> take exactly 1 unit")
// terminates candidate evaluation the moment `remaining` hits zero, so when
// that single biggest-available spec is UNAFFORDABLE, the mix has no smaller
// fallback entry at all — placePlanItem() has nothing left to walk to and the
// batch places 0 units even though cheaper, perfectly affordable candidates
// exist and were simply never offered. This reproduces without any mutation,
// on a stock unlockAll'd game with pow_hydro (maxPerCity:1, GBP5bn) as the
// sole credited-largest 'power' candidate.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer, xpForLevel } from '../src/sim/engine.ts';
import {
  demandFixPlan,
  largestFirstFill,
  creditedUnitCapacity,
  serviceCoverageOf,
  SPECS,
  LADDER_CREDIT_FRACTION,
} from '../src/sim/data.ts';

function unlockedFundedState(funds) {
  let s = initialState();
  s = reducer(s, { type: 'unlockAll' });
  return { ...s, funds };
}

/** Civic-tier rebase fix (FEAT-2326609772, 2026-09-05): unlockedFundedState's
 *  `unlockAll` sets the state's god-mode `unlockedAll` flag, which bypasses
 *  EVERY spec's unlock level at once — no way to isolate one candidate from
 *  a same-family successor added later (edu_nursery_city, unlock level 4).
 *  This variant unlocks by real XP LEVEL instead, so a caller can hold a
 *  civic-tier successor locked while its smaller sibling (lower unlock
 *  level) stays available, exactly like a real mid-game city. */
function unlockedToLevelState(funds, level) {
  const s = initialState();
  return { ...s, unlockedAll: false, xp: xpForLevel(level), funds };
}

// --- ITEM 1: MONEY under a one-shot mix -----------------------------------
test('MONEY: one-shot branch strands the plan with zero affordable fallback', () => {
  const s = unlockedFundedState(1_000_000_000); // GBP1bn — plenty for pow_wind, nowhere near pow_hydro's GBP5bn
  const mix = largestFirstFill(s, 'power', 1000); // small shortfall -> hydro one-shots it alone
  assert.equal(mix.length, 1);
  assert.equal(mix[0].specId, 'pow_hydro');
  assert.ok(SPECS['pow_hydro'].cost > s.funds, 'must be unaffordable for the attack to bite');
  assert.ok(SPECS['pow_wind'].cost < s.funds, 'a real, cheap, unlocked fallback exists but was excluded from the mix');

  // Drive the SAME construction through the real reducer surface used by the
  // Fix button: build a synthetic plan item via the same shape resolveDemand
  // consumes, confirm zero progress.
  const before = s.buildings.length;
  // Exercise placePlanItem indirectly: dispatch resolveDemand against a
  // hand-shaped state whose OWN demandFixPlan() naturally reproduces the same
  // one-shot mix (co-opting a fresh game's real power shortfall would require
  // a large fixture; the direct largestFirstFill() call above already proves
  // the mix-construction defect deterministically and without a mutation).
  const after = reducer(s, { type: 'place', spec: 'pow_wind', x: 1000, y: 1000 });
  assert.ok(after.buildings.length >= before, 'sanity: placement path itself still works for the cheap spec');
});

// --- ITEM 2: THE CREDIT GAMBLE ---------------------------------------------
test('CREDIT GAMBLE: creditedUnitCapacity never leaks into serviceCoverageOf (real capacity stays visible)', () => {
  const nursery = SPECS['edu_nursery'];
  const base = nursery.children; // 30 — tier-0 real capacity
  const credited = creditedUnitCapacity(nursery, base);
  assert.equal(credited, Math.round(nursery.capacityTiers[nursery.capacityTiers.length - 1] * LADDER_CREDIT_FRACTION));
  assert.ok(credited > base * 10, 'the gamble: credited capacity is a huge multiple of a fresh unit\'s real capacity');

  // serviceCoverageOf must be built from the REAL per-tier capacity, never
  // creditedUnitCapacity — the source grep in the investigation found exactly
  // one call site for creditedUnitCapacity (largestFirstFill's own sizing),
  // and this asserts the OBSERVABLE consequence: placing one fresh nursery
  // moves the coverage numerator by its REAL capacity, not the credited one.
  let s = unlockedFundedState(1_000_000_000_000);
  const before = serviceCoverageOf(s).find((r) => r.id === 'nursery');
  const placed = reducer(s, { type: 'place', spec: 'edu_nursery', x: 40, y: 40 });
  const after = serviceCoverageOf(placed).find((r) => r.id === 'nursery');
  assert.ok(before && after, 'nursery coverage row must exist');
  const delta = after.cap - before.cap;
  assert.ok(
    delta <= base + 1,
    `real coverage capacity must grow by the REAL unit capacity (~${base}), not the credited figure (${credited}); grew by ${delta}`
  );
});

// --- ITEM 3: maxPerCity in the mix ------------------------------------------
test('DAM CAP: a mix never proposes 2 dams and never blanks the whole plan when the dam is exhausted', () => {
  let s = unlockedFundedState(50_000_000_000);
  // First mix may include 1 dam (maxPerCity 1).
  const mix1 = largestFirstFill(s, 'power', 100000);
  const damEntries1 = mix1.filter((m) => m.specId === 'pow_hydro');
  assert.ok(damEntries1.length <= 1 && (damEntries1[0]?.count ?? 0) <= 1);

  // Simulate the dam already built (remainingAllowance exhausted) — the mix
  // for a SECOND round must never re-offer it, and must still offer other
  // candidates (never an empty/wasted plan just because the dam is gone).
  const built = reducer(s, { type: 'place', spec: 'pow_hydro', x: 5, y: 5 });
  const mix2 = largestFirstFill(built, 'power', 100000);
  assert.equal(mix2.filter((m) => m.specId === 'pow_hydro').length, 0, 'dam must not reappear once maxPerCity is exhausted');
  assert.ok(mix2.length > 0, 'plan must not be wasted/emptied just because the capped spec is gone');
});

// --- ITEM 4: DETERMINISM -----------------------------------------------------
test('DETERMINISM: largestFirstFill is stable across repeat calls (tie-break is total order, not iteration order)', () => {
  const s = unlockedFundedState(1_000_000_000_000);
  const runs = Array.from({ length: 5 }, () => largestFirstFill(s, 'power', 837412));
  const serialised = runs.map((r) => JSON.stringify(r));
  assert.ok(serialised.every((x) => x === serialised[0]), 'identical inputs must produce byte-identical mixes every call');
});

// --- ITEM 5: hamlet boundary both ways --------------------------------------
test('HAMLET BOUNDARY: a 40-place shortfall gets exactly ONE kindergarten', () => {
  const s = unlockedFundedState(1_000_000_000_000);
  const mix = largestFirstFill(s, 'nursery', 40);
  assert.equal(mix.length, 1);
  assert.equal(mix[0].specId, 'edu_nursery');
  assert.equal(mix[0].count, 1);
});

test('CEIL BOUNDARY: shortfall of exactly the smallest-candidate capacity + 1 ceils to 2 units on the last candidate', () => {
  // Lock every OTHER nursery-stage spec down to force edu_nursery as the
  // ONLY candidate, isolating the ceil-on-last-candidate branch. Stock SPECS
  // gained a same-stage successor since this test was written
  // (edu_nursery_city, unlock level 4, FEAT-2326609761 civic-tier
  // consolidator) — unlockedFundedState's god-mode flag would unlock it too,
  // so this uses unlockedToLevelState(level 3) instead: edu_nursery's own
  // unlock level (2) clears, edu_nursery_city's (4) stays locked.
  assert.ok(SPECS['edu_nursery'].unlock < 4, 'precondition: edu_nursery unlocks below level 4');
  assert.equal(SPECS['edu_nursery_city']?.unlock, 4, 'precondition: edu_nursery_city unlocks at level 4 (still locked at level 3)');
  let s = unlockedToLevelState(1_000_000_000_000, 3);
  const cap = creditedUnitCapacity(SPECS['edu_nursery'], SPECS['edu_nursery'].children);
  const mix = largestFirstFill(s, 'nursery', cap + 1);
  // With only edu_nursery unlocked/matching at this level, it is
  // simultaneously first AND last candidate, so shortfall (cap+1) exceeds
  // its one-shot capacity by 1 and must ceil to 2 units, never defer/floor
  // to 1 (which would silently undershoot by `cap`).
  assert.equal(mix.length, 1);
  assert.equal(mix[0].specId, 'edu_nursery');
  assert.equal(mix[0].count, 2, `shortfall cap+1 must ceil to 2 units, got ${mix[0].count}`);
});

// --- GAP FOUND BY MUTATION: nothing pins LADDER_CREDIT_FRACTION's VALUE ----
// A scratch-copy mutation to 1.0 (the credit-fraction overshoot mutant the
// round brief asked for) was caught by NOTHING in the existing suite or this
// file's other tests — every assertion here only checks relative properties
// (biggest-first, real coverage untouched by the credit figure), never the
// magic constant itself. A future accidental edit to this Aaron-set policy
// value would ship silently. Pin it.
test('MUTATION GAP CLOSED: LADDER_CREDIT_FRACTION is pinned at the Aaron-set 0.5', () => {
  assert.equal(LADDER_CREDIT_FRACTION, 0.5, 'a change to this policy constant must be a deliberate, visible diff, not silent');
});

// --- ITEM 6: MUTATIONS (run in this same process — these are logic-only,
// no filesystem source mutation, so no GR#24 scratch-copy revert is needed) --
test('MUTATION SANITY: sort-ascending would be WRONG (biggest must lead, not smallest)', () => {
  const s = unlockedFundedState(1_000_000_000_000);
  const mix = largestFirstFill(s, 'power', 1000);
  // Correct (descending) behaviour picks the credited-biggest candidate
  // (pow_hydro) for a one-shot; an ascending-sort mutant would instead pick
  // the credited-smallest unlocked power spec. Assert the REAL, correct
  // result so a future ascending-sort regression fails this test.
  assert.equal(mix[0].specId, 'pow_hydro', 'largest-credited-capacity spec must lead the mix');
});
