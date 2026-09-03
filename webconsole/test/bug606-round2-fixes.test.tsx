// bug606-round2-fixes.test.tsx — BUG-606 independent round REJECT follow-ups
// (Aaron, 2026-09-03): M1 (cur-vs-state re-derivation regression guard), M6
// (administration-mode Fix All: paid services blocked, free zones proceed),
// M7 (message price agrees with the REAL, placementCost()-derived planCost —
// the D1 fix). Also folds in Aaron's SAME-DAY superseding ruling on BUG-601
// (AUTO_BUILD_DEMAND_FRACTION 0.5 -> 1.5): every fixture below is derived
// against the LIVE constant/functions, never a hand-typed number assuming
// either fraction, so a future re-tuning cannot silently make these vacuous.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import { demandFixPlan, orderedDemandFixPlan, SPECS, placementCost, AUTO_BUILD_DEMAND_FRACTION, AUTO_BUILD_DEMAND_PERCENT, RESOLVE_DEMAND_ALL_MAX_UNITS, type Spec, type DemandFixPlanItem } from '../src/sim/data.ts';
import { demandFixMessage } from '../src/components/demandFixUi.ts';
import type { SimState } from '../src/sim/types.ts';

function shortfallState(population: number, fundsOverride = 1_000_000_000): SimState {
  const base = initialState();
  return { ...base, population, unlockedAll: true, funds: fundsOverride, administrationState: null };
}

function specCounts(s: SimState): Map<string, number> {
  const m = new Map<string, number>();
  for (const b of s.buildings) m.set(b.spec, (m.get(b.spec) ?? 0) + 1);
  return m;
}

// ---------------------------------------------------------------------------
// M1 — resolveDemandAll must re-derive demandFixPlan against the
// ALREADY-UPDATED state (cur) on every iteration, never the frozen state
// captured once at the top of the batch. The attacker's divergence class:
// a regression that swapped `demandFixPlan(cur)` for `demandFixPlan(state)`
// inside the loop would ship the STALE, ample-funds pick for a mid/low
// priority service even after earlier services have genuinely depleted the
// treasury — e.g. recommending fire_hq (needs its FULL plan cost available)
// when the REAL remaining budget can only ever afford a fire_post batch.
// ---------------------------------------------------------------------------

test('M1: resolveDemandAll re-derives demandFixPlan against the depleted treasury — fire must resolve to fire_post, never the STALE fire_hq an upfront (frozen-state) plan recommends', () => {
  // Aaron's own repro figures (2026-09-03): pop 400,000. Funds re-derived
  // for the NEW AUTO_BUILD_DEMAND_FRACTION=1.5 superseding ruling (the
  // original £400M repro no longer reaches fire in the priority order at
  // all once every service's target tripled in size — this run needs bigger
  // funds so fire is genuinely REACHED, with just enough spent by
  // higher-priority services beforehand that the fire choice is forced to
  // re-derive against a smaller real budget than the STALE £1.2B figure
  // demandFixPlan(state) would have used throughout).
  const s = shortfallState(400_000, 1_200_000_000);
  const order = orderedDemandFixPlan(s);
  assert.ok(order.some((p) => p.serviceKey === 'fire'), 'precondition: a fire shortfall must exist in the batch');

  // The STALE, upfront plan (computed once against the untouched ample
  // treasury) — this is what a `demandFixPlan(state)` regression would ship
  // for EVERY iteration, ignoring how much earlier services actually spent.
  const staleFire = demandFixPlan(s).find((p) => p.serviceKey === 'fire');
  assert.ok(staleFire);
  assert.equal(staleFire.specId, 'fire_hq', 'precondition: the STALE upfront plan must pick fire_hq (ample-funds "1 dam" scoring) — proves this scenario has real state-sensitivity');

  const before = specCounts(s);
  const result = reducer(s, { type: 'resolveDemandAll' });
  const after = specCounts(result);

  const postDelta = (after.get('fire_post') ?? 0) - (before.get('fire_post') ?? 0);
  const hqDelta = (after.get('fire_hq') ?? 0) - (before.get('fire_hq') ?? 0);
  const stationDelta = (after.get('fire_station') ?? 0) - (before.get('fire_station') ?? 0);

  assert.ok(postDelta > 0, `fire_post (the cur-based, funds-depleted pick) must actually be placed — got 0`);
  assert.equal(hqDelta, 0, 'fire_hq (the STALE, ample-funds pick) must NOT be placed — proves resolveDemandAll re-derives against cur, not the frozen state');
  assert.equal(stationDelta, 0, 'fire_station must not be placed either — the real, depleted-treasury winner is fire_post');
  assert.ok(result.funds >= 0, 'funds must never go negative');
});

// NOTE (independent round r2, 2026-09-03): before the RESOLVE_DEMAND_ALL_MAX_
// UNITS perf cap, Fix All == N sequential resolveDemand dispatches EXACTLY —
// that equivalence is now INTENTIONALLY BROKEN by design at this repro's
// scale: 'resolveDemandAll' shares ONE global 250-unit budget across the
// WHOLE priority order, while N independent resolveDemand dispatches each
// get their OWN 250-unit budget (a player clicking every Fix (N) button by
// hand places far MORE total units than one Fix-All click at this
// population). The cross-check below asserts the NEW real invariant instead:
// Fix All never exceeds the global cap, and its capped notice is honest.
test('M1 cross-check: resolveDemandAll never exceeds RESOLVE_DEMAND_ALL_MAX_UNITS in total, even when the real plan needs vastly more (perf cap, independent round r2)', () => {
  const s = shortfallState(400_000, 1_200_000_000);
  const order = orderedDemandFixPlan(s);
  const totalPlanned = order.reduce((sum, item) => sum + item.count, 0);
  assert.ok(
    totalPlanned > RESOLVE_DEMAND_ALL_MAX_UNITS,
    `precondition: this repro must genuinely need MORE than the cap (${totalPlanned} planned vs cap ${RESOLVE_DEMAND_ALL_MAX_UNITS}) for the cap to mean anything`
  );

  const result = reducer(s, { type: 'resolveDemandAll' });
  assert.ok(result.funds >= 0, 'funds must never go negative');

  // Parse the reducer's OWN "built X of Y planned" accounting from the
  // notice — NOT a specCounts() diff. A specCounts() diff would over-count:
  // findSpot()/'place' may legitimately auto-append PAID road/rail connector
  // tiles (autoConnect/autoBranchRail) to reach an unreached site, an
  // existing, unrelated side effect of the shared 'place' path — those
  // connector tiles are real buildings but were never one of the "units"
  // demandFixPlan()/the cap are counting.
  const m = /^Fix All: built (\d+) of (\d+) planned — click Fix All again for the rest$/.exec(result.placeNotice ?? '');
  assert.ok(m, `expected the capped notice format, got: ${result.placeNotice}`);
  const totalPlaced = Number(m![1]);
  const noticeTotalPlanned = Number(m![2]);
  assert.equal(noticeTotalPlanned, totalPlanned, 'notice must report the SAME totalPlanned the priority order itself computed');
  assert.ok(totalPlaced <= RESOLVE_DEMAND_ALL_MAX_UNITS, `Fix All placed ${totalPlaced} units — must never exceed the ${RESOLVE_DEMAND_ALL_MAX_UNITS}-unit per-click cap`);
  assert.ok(totalPlaced > 0, 'the cap must still allow SOME real progress, not stall at 0');
});

test('M1 cross-check (RED-PROOF live): with the SAME repro state, N sequential resolveDemand dispatches place MORE real plan units than one Fix All click — proves the global cap is real, not a no-op', () => {
  // This is the reverse of the pre-cap equivalence: sequential dispatches are
  // each independently capped per-SERVICE (RESOLVE_DEMAND_ALL_MAX_UNITS per
  // click), so N of them place roughly N times the single Fix-All budget.
  const s = shortfallState(400_000, 1_200_000_000);
  const order = orderedDemandFixPlan(s);
  const all = reducer(s, { type: 'resolveDemandAll' });
  const allMatch = /^Fix All: built (\d+) of \d+ planned/.exec(all.placeNotice ?? '');
  assert.ok(allMatch, `expected a capped Fix All notice, got: ${all.placeNotice}`);
  const allTotal = Number(allMatch![1]);

  // Sum only each dispatch's OWN plan spec delta (never a raw specCounts()
  // diff — see the sibling test's comment on why that over-counts via
  // auto-appended road connector tiles).
  let seq: SimState = s;
  let seqTotal = 0;
  for (const item of order) {
    const plan = demandFixPlan(seq).find((p) => p.serviceKey === item.serviceKey);
    const before = plan ? specCounts(seq).get(plan.specId) ?? 0 : 0;
    seq = reducer(seq, { type: 'resolveDemand', serviceKey: item.serviceKey });
    const after = plan ? specCounts(seq).get(plan.specId) ?? 0 : 0;
    seqTotal += after - before;
  }

  assert.ok(allTotal <= RESOLVE_DEMAND_ALL_MAX_UNITS, `Fix All (${allTotal}) must respect the global cap`);
  assert.ok(seqTotal > allTotal, `N sequential Fix clicks (${seqTotal} real plan units) must place MORE than one Fix All click (${allTotal} units) once the global cap binds`);
});

// ---------------------------------------------------------------------------
// M6 — administration-mode Fix All: paid services must not place; free zones
// (parks — placementCost() £0, data.ts) proceed exactly as resolveDemand's
// existing per-service semantics already allow (the discretionary-spend
// freeze only ever gates cost>0 placements — placePlanItem()/'place' case).
// ---------------------------------------------------------------------------

test('M6: Fix All under administration places ZERO paid buildings but still places free park zones, and the notice names the true cause', () => {
  const s: SimState = { ...shortfallState(50_000, 1_000_000_000), administrationState: { enteredAt: 0 } };
  const order = orderedDemandFixPlan(s);
  assert.ok(order.length > 0, 'precondition: a real multi-service plan exists under administration');
  const paidServices = order.filter((p) => placementCost(SPECS[p.specId]) > 0);
  const freeServices = order.filter((p) => placementCost(SPECS[p.specId]) <= 0);
  assert.ok(paidServices.length > 0, 'precondition: at least one PAID service must be in the plan');
  assert.ok(freeServices.length > 0, 'precondition: at least one FREE (zone) service (parks) must be in the plan');

  const before = specCounts(s);
  const result = reducer(s, { type: 'resolveDemandAll' });
  const after = specCounts(result);

  for (const p of paidServices) {
    const delta = (after.get(p.specId) ?? 0) - (before.get(p.specId) ?? 0);
    assert.equal(delta, 0, `PAID service ${p.serviceKey} (${p.specId}) must place NOTHING under administration`);
  }
  for (const p of freeServices) {
    const delta = (after.get(p.specId) ?? 0) - (before.get(p.specId) ?? 0);
    assert.ok(delta > 0, `FREE zone service ${p.serviceKey} (${p.specId}) must still place under administration — a £0 placement is not discretionary spend`);
  }
  // D2 FIX cross-check: every PAID service was BLOCKED, not fund-starved —
  // the notice must say so, never the generic "insufficient funds" a pre-D2
  // regression would have shown. The treasury MAY still move slightly (park
  // placements are £0 themselves, but findSpot()'s autoConnect/autoBranchRail
  // may lay a real, PAID road connector tile to reach an unreached site — an
  // existing, unrelated side effect of the shared 'place' path, not a
  // violation of the administration freeze), so this only bounds it, never
  // asserts exact equality.
  assert.ok(result.funds <= s.funds, 'funds must never INCREASE');
  assert.ok(result.funds >= 0, 'funds must never go negative');
  assert.ok(
    /administration/i.test(result.placeNotice ?? ''),
    `notice must name Administration Mode as the true cause, got: ${result.placeNotice}`
  );
  assert.ok(!/insufficient funds/i.test(result.placeNotice ?? ''), `notice must NOT blame funds when the treasury never moved: ${result.placeNotice}`);
});

test('M6 cross-check: Fix All under administration matches N sequential resolveDemand dispatches exactly (same result as clicking every Fix button by hand)', () => {
  const s: SimState = { ...shortfallState(50_000, 1_000_000_000), administrationState: { enteredAt: 0 } };
  const order = orderedDemandFixPlan(s);
  const all = reducer(s, { type: 'resolveDemandAll' });
  let seq = s;
  for (const item of order) seq = reducer(seq, { type: 'resolveDemand', serviceKey: item.serviceKey });
  assert.equal(all.funds, seq.funds);
  const a = specCounts(all), b = specCounts(seq);
  const keys = new Set([...a.keys(), ...b.keys()]);
  for (const k of keys) assert.equal(a.get(k) ?? 0, b.get(k) ?? 0, `spec ${k} differs`);
});

// ---------------------------------------------------------------------------
// M7 — D1 fix: the message price must equal the REAL, placementCost()-
// derived planCost, for both paid AND free-zone services. A free zone must
// render an honest "£0", never the catalogue price.
// ---------------------------------------------------------------------------

test('M7: demandFixMessage renders £0 for a free-zone (parks) plan, never the catalogue price', () => {
  const s = shortfallState(300_000); // parksNeed = pop*0.002 = 600, no parks built — real parks shortfall.
  const parks = demandFixPlan(s).find((p) => p.serviceKey === 'parks');
  assert.ok(parks, 'precondition: a real parks shortfall must exist');
  assert.equal(placementCost(SPECS[parks.specId]), 0, 'precondition: the chosen park spec must be free to place');
  // D1: planCost must be the REAL charge (£0), not count * SPECS[specId].cost
  // (the catalogue price, which is nonzero for every park spec).
  assert.equal(parks.planCost, 0, 'parks.planCost must be £0 — placementCost()-derived, not the catalogue cost');
  assert.notEqual(SPECS[parks.specId].cost, 0, 'precondition: the catalogue price itself is genuinely nonzero (proves this is a real fix, not a spec with £0 cost anyway)');

  const msg = demandFixMessage(parks);
  assert.ok(msg.includes('£0'), `message must show the HONEST £0 price for a free zone, got: ${msg}`);
  assert.ok(
    !msg.includes(`£${SPECS[parks.specId].cost.toLocaleString('en-GB')}`),
    `message must NOT show the catalogue price ${SPECS[parks.specId].cost} the player is never actually charged: ${msg}`
  );
});

test('M7: demandFixMessage price agrees with plan.planCost for every service in a real mixed (paid + free) plan', () => {
  const s = shortfallState(300_000, 1_000_000_000);
  const plan = demandFixPlan(s);
  assert.ok(plan.some((p) => placementCost(SPECS[p.specId]) === 0), 'precondition: at least one free-zone entry (parks) in this plan');
  assert.ok(plan.some((p) => placementCost(SPECS[p.specId]) > 0), 'precondition: at least one paid entry in this plan');
  for (const item of plan) {
    const realCost = item.count * placementCost(SPECS[item.specId]);
    assert.equal(item.planCost, realCost, `${item.serviceKey}: planCost=${item.planCost} must equal count*placementCost=${realCost}`);
    const msg = demandFixMessage(item);
    const expectedFragment = realCost === 0 ? '£0' : `£${realCost.toLocaleString('en-GB')}`;
    assert.ok(msg.includes(expectedFragment), `${item.serviceKey}: message must show the real charge ${expectedFragment}, got: ${msg}`);
    if (item.alternative) {
      const altReal = item.alternative.count * placementCost(SPECS[item.alternative.specId]);
      assert.equal(item.alternative.planCost, altReal, `${item.serviceKey} ALT: planCost=${item.alternative.planCost} must equal count*placementCost=${altReal}`);
    }
  }
});

// ---------------------------------------------------------------------------
// D1 design guard — free-zone tie-break prefers FEWEST UNITS (biggest park
// spec), so Fix All places a few big parks instead of carpeting the map with
// many small ones (Aaron, 2026-09-03). ⚠ BALANCE-NUMBER NOTE for Aaron's
// pass: recorded here as a locked-in behaviour for his row-by-row review.
// ---------------------------------------------------------------------------

test('D1 design guard: among all-free (£0) park candidates, the chosen spec needs the FEWEST units (biggest capacity), not the most', () => {
  const s = shortfallState(300_000, 1_000_000_000);
  const parks = demandFixPlan(s).find((p) => p.serviceKey === 'parks');
  assert.ok(parks);
  // Every unlocked park spec is free — rankedProviders' cost-based tiers all
  // tie at £0, so the units-ascending tie-break must have picked the
  // candidate needing the FEWEST units among every unlocked park spec sized
  // against this SAME shortfall.
  const parkSpecs = Object.values(SPECS).filter((sp) => sp.kind === 'park');
  const unitsFor = (sp: Spec) => Math.max(1, Math.ceil((parks.need - parks.have) * AUTO_BUILD_DEMAND_FRACTION / (sp.w * sp.h)));
  const chosenUnits = parks.count;
  for (const sp of parkSpecs) {
    if (sp.id === parks.specId) continue;
    const otherUnits = unitsFor(sp);
    assert.ok(chosenUnits <= otherUnits, `chosen ${parks.specId} (${chosenUnits} units) must need no MORE units than ${sp.id} (${otherUnits} units) among free options`);
  }
});

// ---------------------------------------------------------------------------
// D3 r2 — fmtShortfall (demandFixUi.ts, not exported) must never render a
// real sub-1 gap as a misleading "0"/"0.0". Tested indirectly through
// demandFixMessage(), which is the public surface that actually renders it.
// RED-PROOF: this test is EXACTLY what would catch a revert to the r1 fix
// (`gap.toFixed(1)`, which rendered 0.046 as "0.0 short" — the independent
// round's own r2 attack finding) — hand-verified live via a scratch cp/mv
// sabotage (fmtShortfall reverted to `gap.toFixed(1)`) during development;
// reddened on the 0.046 case exactly as expected, restored and reconfirmed
// green before this file was finalised.
// ---------------------------------------------------------------------------

test('D3 r2: a sub-0.05 gap renders "<1 short", never a misleading "0"/"0.0" (the attacker\'s 0.046 case)', () => {
  const item: DemandFixPlanItem = {
    serviceKey: 'cleanwater',
    specId: 'wat_tower',
    unitCapacity: SPECS.wat_tower.served ?? 0,
    need: 1000.046,
    have: 1000, // gap = 0.046 — the exact attacker repro figure
    count: 1,
    planCost: SPECS.wat_tower.cost,
    alternative: null,
  };
  const msg = demandFixMessage(item);
  assert.ok(msg.includes('<1 short'), `expected "<1 short" for a 0.046 gap, got: ${msg}`);
  assert.ok(!/\b0(\.0)? short\b/.test(msg), `must never render the misleading "0"/"0.0 short" for a real 0.046 gap, got: ${msg}`);
});

test('D3 r2: a 0.05<=gap<1 gap renders 2 significant figures, never a bare truncated "0"', () => {
  const item: DemandFixPlanItem = {
    serviceKey: 'cleanwater',
    specId: 'wat_tower',
    unitCapacity: SPECS.wat_tower.served ?? 0,
    need: 1000.46,
    have: 1000, // gap = 0.46
    count: 1,
    planCost: SPECS.wat_tower.cost,
    alternative: null,
  };
  const msg = demandFixMessage(item);
  assert.ok(msg.includes('0.46 short'), `expected 2-sig-fig "0.46 short", got: ${msg}`);
  assert.ok(!/\b0 short\b/.test(msg), `must never truncate a real 0.46 gap to "0 short": ${msg}`);
});

test('D3 r2: gaps >= 1 are unaffected (still the normal thousands-separated integer)', () => {
  const item: DemandFixPlanItem = {
    serviceKey: 'cleanwater',
    specId: 'wat_tower',
    unitCapacity: SPECS.wat_tower.served ?? 0,
    need: 12_400,
    have: 0,
    count: 4,
    planCost: 4 * SPECS.wat_tower.cost,
    alternative: null,
  };
  const msg = demandFixMessage(item);
  assert.ok(msg.includes('12,400 short'), `expected the normal integer format for a >=1 gap, got: ${msg}`);
});

// ---------------------------------------------------------------------------
// GR#15 binding (independent round r2, Aaron 2026-09-03: "the attacker
// hardcoded 50 with 1.5 live and nothing failed — the GR#15 drift its own
// comment claims is impossible"). AUTO_BUILD_DEMAND_PERCENT's own doc
// comment in data.ts claims it is DERIVED from AUTO_BUILD_DEMAND_FRACTION and
// can never drift — this test is the mechanical proof of that claim, so a
// future edit that hardcodes one without updating the other reddens HERE
// instead of only in a human's head.
// ---------------------------------------------------------------------------

test('GR#15 binding: AUTO_BUILD_DEMAND_PERCENT === AUTO_BUILD_DEMAND_FRACTION * 100, mechanically — never two independently-edited numbers', () => {
  assert.equal(
    AUTO_BUILD_DEMAND_PERCENT,
    AUTO_BUILD_DEMAND_FRACTION * 100,
    `AUTO_BUILD_DEMAND_PERCENT (${AUTO_BUILD_DEMAND_PERCENT}) has drifted from AUTO_BUILD_DEMAND_FRACTION * 100 (${AUTO_BUILD_DEMAND_FRACTION * 100}) — these must be the SAME number by construction (data.ts), never two hand-maintained literals`
  );
  // RED-PROOF (documented, hand-verified live during development via a
  // scratch cp/mv sabotage of data.ts's AUTO_BUILD_DEMAND_PERCENT export —
  // hardcoding `export const AUTO_BUILD_DEMAND_PERCENT = 50;` alongside the
  // live AUTO_BUILD_DEMAND_FRACTION = 1.5 reddened this exact assertion
  // (50 !== 150); restored and reconfirmed green before this file was
  // finalised. This is precisely the drift class the attacker's own
  // "hardcoded 50 with 1.5 live and nothing failed" finding describes.
  assert.equal(typeof AUTO_BUILD_DEMAND_PERCENT, 'number');
  assert.ok(Number.isFinite(AUTO_BUILD_DEMAND_PERCENT));
});
