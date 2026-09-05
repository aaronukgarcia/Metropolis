// bug606-demand-fix-message.test.mjs — BUG-606 (P1, Aaron twice: "'citizens
// want shops' no help - how much what type a clue would be nice is this one
// hypermarket or 50?").
//
// Covers the PLAN-DERIVATION half: rankedProviders()/demandFixPlan()'s new
// `planCost`/`alternative` fields, and demandFixUi.ts's demandFixMessage()
// formatter that renders them. demandFixMessage() is deliberately a PURE
// function of a DemandFixPlanItem's own fields (never re-derives need/have/
// count/specId/planCost/alternative independently) — agreement-by-
// construction with the Fix/Auto-build/Fix-All buttons that execute the SAME
// plan object, per the acceptance brief.
//
// RED-PROOF (hand-verified during development, per this suite's established
// convention — see demand-fix-ui.test.tsx's BUG-587 comment for the same
// practice): the "message-format snapshot" test below was run once against a
// scratch-mutated demandFixUi.ts (via `cp demandFixUi.ts demandFixUi.ts.bak`,
// editing demandFixMessage() to drop the alternative clause entirely, `mv
// demandFixUi.ts.bak demandFixUi.ts` to restore — GR#24, never a git command)
// and reddened as expected (missing "or 4 x Water Tower" / "cheapest
// picked"); restored and re-confirmed green before this file was finalised.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, levelOf, xpForLevel } from '../src/sim/engine.ts';
import {
  demandFixPlan,
  orderedDemandFixPlan,
  rankedProviders,
  optimalProvider,
  largestFirstFill,
  SPECS,
  AUTO_BUILD_DEMAND_FRACTION,
  AUTO_BUILD_DEMAND_PERCENT,
  type DemandFixPlanItem,
} from '../src/sim/data.ts';
import { demandFixMessage, formatBuildingCount } from '../src/components/demandFixUi.ts';
import { fmtMoney, fmtNum } from '../src/sim/utils.ts';

/** Mirrors demand-fix.test.mjs's shortfallState(). */
function shortfallState(population: number, fundsOverride = 1_000_000_000) {
  const base = initialState();
  return { ...base, population, unlockedAll: true, funds: fundsOverride, administrationState: null };
}

// ---------------------------------------------------------------------------
// (1) Message-format snapshot, derived from a real plan fixture.
// ---------------------------------------------------------------------------

test('BUG-606: demandFixMessage renders label/shortfall/chosen-cost/alternative-cost straight off the plan item fixture', () => {
  // A hand-built DemandFixPlanItem fixture using REAL SPECS (wat_clean/
  // wat_tower) so the rendered names/costs are genuine catalogue values, not
  // invented literals (GR#15) — but the item ITSELF is a fixture, not read
  // from demandFixPlan(), so this test is a real snapshot of the FORMATTER,
  // independent of whatever plan.ts's own arithmetic currently produces.
  const item: DemandFixPlanItem = {
    serviceKey: 'cleanwater',
    specId: 'wat_clean',
    unitCapacity: SPECS.wat_clean.served ?? 0,
    need: 20_000,
    have: 7_600,
    count: 1,
    planCost: SPECS.wat_clean.cost,
    mix: [{ specId: 'wat_clean', unitCapacity: SPECS.wat_clean.served ?? 0, count: 1, planCost: SPECS.wat_clean.cost }],
    alternative: { specId: 'wat_tower', count: 4, planCost: 4 * SPECS.wat_tower.cost },
  };

  const msg = demandFixMessage(item);
  const expected =
    `Clean water: ${fmtNum(20_000 - 7_600)} short — Fix builds ${AUTO_BUILD_DEMAND_PERCENT}%: ` +
    `${formatBuildingCount('Water Works', 1)} (${fmtMoney(SPECS.wat_clean.cost)}) or ` +
    `${formatBuildingCount('Water Tower', 4)} (${fmtMoney(4 * SPECS.wat_tower.cost)}) — cheapest picked`;
  assert.equal(msg, expected);

  // Sanity: the message really does answer Aaron's "how much / what type /
  // one or fifty" complaint — a raw shortfall number, a concrete count+name,
  // a cost, and a named alternative.
  assert.ok(/\d/.test(msg.split('short')[0]), 'must show a numeric shortfall');
  assert.ok(msg.includes('1 x Water Works'));
  assert.ok(msg.includes('4 x Water Tower'));
  assert.ok(msg.includes('£'));

  // RED-PROOF (live, not just documented): mutating the SAME fixture's count
  // must change the rendered text — proves the string is rendered FROM the
  // object's fields, not a cached/hardcoded copy. A formatter that ignored
  // `item.count` (e.g. always printing "1 x") would fail this half.
  const mutated = { ...item, count: 3 };
  const msg2 = demandFixMessage(mutated);
  assert.notEqual(msg, msg2);
  assert.ok(msg2.includes(formatBuildingCount('Water Works', 3)));
  assert.ok(!msg2.includes(formatBuildingCount('Water Works', 1)));
});

test('BUG-606: demandFixMessage omits the alternative clause entirely when the plan item has none (single-provider service)', () => {
  const item: DemandFixPlanItem = {
    serviceKey: 'fire',
    specId: 'fire_post',
    unitCapacity: 4_000,
    need: 10_000,
    have: 0,
    count: 2,
    planCost: 2 * SPECS.fire_post.cost,
    mix: [{ specId: 'fire_post', unitCapacity: 4_000, count: 2, planCost: 2 * SPECS.fire_post.cost }],
    alternative: null,
  };
  const msg = demandFixMessage(item);
  assert.ok(!msg.includes(' or '), 'no alternative clause when alternative is null');
  assert.ok(!msg.includes('cheapest picked'));
  assert.ok(msg.includes('2 x Fire Post') || msg.includes(formatBuildingCount(SPECS.fire_post.name, 2)));
});

// ---------------------------------------------------------------------------
// (2) Alternative-option correctness, derived from the REAL rankedProviders()
//     ranking (not the fixture above) — proves demandFixPlan()'s new fields
//     are wired to the real scored candidate list, not a fabricated pair.
// ---------------------------------------------------------------------------

test('BUG-606: demandFixPlan.alternative is the genuine rankedProviders() runner-up, distinct from the chosen spec', () => {
  // Population 0 with an explicit large shortfall argument (mirrors
  // demanddock-overhaul.test.mjs's optimalProvider() call shape) — cleanwater
  // has 3 unlocked candidate specs (wat_tower/wat_clean/wat_reservoir) at a
  // shortfall where more than one is a real contender.
  const s = shortfallState(0, 1_000_000_000);
  const fixAmount = 20_000 * AUTO_BUILD_DEMAND_FRACTION;
  const ranked = rankedProviders(s, 'cleanwater', s.funds, fixAmount);
  assert.ok(ranked.length >= 2, 'precondition: at least 2 unlocked cleanwater candidates must exist');

  // Cross-check optimalProvider() (the pre-existing, unchanged contract)
  // still returns exactly ranked[0] — the refactor to rankedProviders() must
  // not change optimalProvider()'s own winner.
  const winner = optimalProvider(s, 'cleanwater', s.funds, fixAmount);
  assert.ok(winner);
  assert.equal(winner!.id, ranked[0].sp.id);

  const s60 = shortfallState(60_000); // real population-driven plan, matches demand-fix.test.mjs's fixture
  const plan = demandFixPlan(s60).find((p) => p.serviceKey === 'cleanwater');
  assert.ok(plan);
  assert.ok(plan.alternative, 'a genuine alternative must exist when multiple cleanwater specs are unlocked');
  assert.notEqual(plan.alternative.specId, plan.specId, 'the alternative must be a DIFFERENT spec from the chosen one');

  // RETUNE (this session, post-BUG-685 largest-first landing): demandFixPlan()'s
  // `specId`/`mix`/`planCost` now come from largestFirstFill() (biggest
  // credited-capacity spec first), NOT rankedProviders()' single-cheapest-
  // total-plan winner — see demandFixPlan()'s own doc comment. `plan.specId`
  // must match largestFirstFill()'s mix[0], and `plan.alternative` is mix[1]
  // when the mix used more than one spec, falling back to rankedProviders()'
  // runner-up only when the mix is single-spec (the ORIGINAL informational
  // case this test predates, still correctly exercised when it applies).
  const fixAmount60 = (plan.need - plan.have) * AUTO_BUILD_DEMAND_FRACTION;
  const mix60 = largestFirstFill(s60, 'cleanwater', fixAmount60, s60.funds);
  assert.ok(mix60.length > 0, 'precondition: a real largest-first mix exists for this fixture');
  assert.equal(mix60[0].specId, plan.specId, 'plan.specId must be the largest-first PRIMARY (mix[0]) pick');
  assert.equal(plan.planCost, mix60.reduce((sum, m) => sum + m.planCost, 0), 'plan.planCost must be the sum of the WHOLE mix');
  if (mix60.length > 1) {
    assert.equal(mix60[1].specId, plan.alternative.specId, 'plan.alternative must be the largest-first mix[1] entry');
    assert.equal(plan.alternative.count, mix60[1].count);
    assert.equal(plan.alternative.planCost, mix60[1].planCost);
  } else {
    const ranked60 = rankedProviders(s60, 'cleanwater', s60.funds, fixAmount60);
    const alt = ranked60.find((c) => c.sp.id !== plan.specId);
    assert.ok(alt, 'a single-spec mix must still fall back to a genuine rankedProviders() runner-up');
    assert.equal(alt.sp.id, plan.alternative.specId, 'plan.alternative must be the ranked runner-up when the mix is single-spec');
    assert.equal(plan.alternative.count, alt.units);
    assert.equal(plan.alternative.planCost, alt.planCost);
  }
});

test('BUG-606: demandFixPlan.alternative is null when only ONE unlocked provider exists for the service (RED-PROOF via a locked-tier fixture)', () => {
  // Level 2: only fire_post is unlocked (fire_station unlock 4, fire_hq
  // unlock 11) — mirrors demanddock-overhaul.test.mjs's AC-5 fixture.
  const base = initialState();
  const s = { ...base, population: 10_000, unlockedAll: false, xp: xpForLevel(2), funds: 1_000_000_000, administrationState: null };
  assert.equal(levelOf(s.xp), 2, 'precondition: level 2 unlocks fire_post only');

  const plan = demandFixPlan(s).find((p) => p.serviceKey === 'fire');
  assert.ok(plan);
  assert.equal(plan.specId, 'fire_post');
  // RED-PROOF: a rankedProviders() bug that fabricated a second candidate
  // regardless of unlock state (e.g. by not filtering specUnlocked()) would
  // make this fail — confirm directly against rankedProviders() too.
  const fixAmount = 10_000 * AUTO_BUILD_DEMAND_FRACTION;
  const ranked = rankedProviders(s, 'fire', s.funds, fixAmount);
  assert.equal(ranked.length, 1, 'precondition: exactly one unlocked fire candidate at level 2');
  assert.equal(plan.alternative, null, 'no second unlocked provider exists — alternative must be null, never fabricated');
});

// ---------------------------------------------------------------------------
// (3) orderedDemandFixPlan() — BUG-606 fix-all priority: Health first, then
//     demand-descending (raw gap), id tiebreak. Mirrors the existing
//     serviceDemandOf() sort tests (demanddock-overhaul.test.mjs AC-2..AC-4)
//     against demandFixPlan() items instead.
// ---------------------------------------------------------------------------

test('orderedDemandFixPlan: gp and hosp are pinned above every other service, even when another service has a bigger raw gap', () => {
  const base = initialState();
  // Population 5,100 with ONE hea_clinic (served 5,000) leaves gp with a
  // SMALL real gap (100) while nursery (need = pop*0.06 = 306, zero built)
  // has a genuinely BIGGER raw gap — a naive gap-only sort would put nursery
  // above gp; the Health-pinned predicate must not. hosp is left with zero
  // capacity (gap = 5,100, its own real shortfall) so both health rows are
  // present without needing a hospital building.
  const s = {
    ...base,
    unlockedAll: true,
    funds: 1_000_000_000,
    population: 5_100,
    buildings: [...base.buildings, { id: 90002, spec: 'hea_clinic', x: 10, y: 10 }],
  };
  const order = orderedDemandFixPlan(s);
  const gpIdx = order.findIndex((p) => p.serviceKey === 'gp');
  const hospIdx = order.findIndex((p) => p.serviceKey === 'hosp');
  const nurseryIdx = order.findIndex((p) => p.serviceKey === 'nursery');
  assert.ok(gpIdx !== -1 && hospIdx !== -1 && nurseryIdx !== -1, 'precondition: all three services must be in the plan');

  const nurseryGap = order[nurseryIdx].need - order[nurseryIdx].have;
  const gpGap = order[gpIdx].need - order[gpIdx].have;
  assert.ok(nurseryGap > gpGap, 'precondition: nursery must have a genuinely bigger raw gap than gp');

  assert.ok(gpIdx < nurseryIdx, 'gp must be pinned above nursery despite nursery\'s bigger raw gap');
  assert.ok(hospIdx < nurseryIdx, 'hosp must be pinned above nursery despite nursery\'s bigger raw gap');
});

test('orderedDemandFixPlan: non-health services sort by raw gap descending, deterministic across repeated calls', () => {
  const s = shortfallState(8_000);
  const orderA = orderedDemandFixPlan(s);
  const orderB = orderedDemandFixPlan(s);
  assert.deepEqual(orderA, orderB, 'orderedDemandFixPlan must be a pure function of state (GR#21)');

  const nonHealth = orderA.filter((p) => p.serviceKey !== 'gp' && p.serviceKey !== 'hosp');
  for (let i = 1; i < nonHealth.length; i++) {
    const gapPrev = nonHealth[i - 1].need - nonHealth[i - 1].have;
    const gapCur = nonHealth[i].need - nonHealth[i].have;
    assert.ok(
      gapPrev >= gapCur,
      `non-health services must sort by non-increasing raw gap: ${nonHealth[i - 1].serviceKey}(${gapPrev}) then ${nonHealth[i].serviceKey}(${gapCur})`
    );
  }
});

test('orderedDemandFixPlan: empty plan (no shortfall) returns an empty order', () => {
  const s = shortfallState(0);
  assert.deepEqual(orderedDemandFixPlan(s), []);
});
