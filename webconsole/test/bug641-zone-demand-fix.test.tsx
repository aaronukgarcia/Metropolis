// bug641-zone-demand-fix.test.mjs — BUG-641 (P1, Aaron ×3): "'citizens want
// shops' no help — how much what type a clue would be nice is this one
// hypermarket or 50?"
//
// BUG-606 sized the 12 serviceCoverageOf()/wasteStatsOf() COVERAGE services
// via demandFixPlan(). The three ZONE demands (residential/commercial/
// industrial, engine.ts's demandOf()) still fell through to MapView.tsx's
// unsized legacy banners ("Citizens want shops — paint Commercial zones.").
// This suite exercises the new zone-demand seam in
// src/components/demandFixUi.ts: zoneDemandFixPlan/zoneDemandFix/
// zoneDemandMessage/worstAnyDemandFix. Pure logic only — no DOM mount, no
// MapView import (this agent's file surface is demandFixUi.ts + tests only;
// the MapView.tsx hookup is the lead's job at land time, see the module's
// own header comment on ZONE_DEMAND_THRESHOLD).
//
// Every count/cost assertion below is INDEPENDENTLY recomputed from
// exported primitives (capacityAtTier/placementCost/totalJobs/
// WORKING_AGE_FRACTION), never by trusting the function under test's own
// arithmetic back at itself (Vestige "verification standards": prove every
// regression test can fail).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState } from '../src/sim/engine.ts';
import { demandOf } from '../src/sim/engine.ts';
import {
  SPECS,
  capacityAtTier,
  placementCost,
  totalJobs,
  WORKING_AGE_FRACTION,
  AUTO_BUILD_DEMAND_FRACTION,
  AUTO_BUILD_DEMAND_PERCENT,
  type Spec,
} from '../src/sim/data.ts';
import {
  zoneDemandFixPlan,
  zoneDemandFix,
  zoneDemandMessage,
  worstAnyDemandFix,
  ZONE_DEMAND_THRESHOLD,
  ZONE_DEMAND_LABELS,
  type ZoneKey,
} from '../src/components/demandFixUi.ts';

/** A state with `population` citizens, no zone buildings, everything
 *  unlocked, ample funds — mirrors demand-fix.test.mjs's shortfallState() so
 *  the SAME fixture shape drives both the coverage-service and zone-demand
 *  suites. */
function shortfallState(population: number, fundsOverride = 1_000_000_000) {
  const base = initialState();
  return { ...base, population, unlockedAll: true, funds: fundsOverride, administrationState: null };
}

/** Independent per-zone unit-capacity recompute — mirrors demandFixUi.ts's
 *  own ZONE_PROVIDERS table (residents for housing, jobs for shops/industry,
 *  with the SAME 12/18 no-jobs-field fallback totalJobs() uses, data.ts
 *  ~2553) WITHOUT importing that private table, so this is a genuine
 *  cross-check rather than a tautology. */
function expectedUnitCapacity(zone: ZoneKey, sp: Spec): number {
  if (zone === 'residential') return capacityAtTier(sp, 0);
  if (sp.jobs) return capacityAtTier(sp, 0);
  return zone === 'commercial' ? 12 : 18;
}

// ---------------------------------------------------------------------------
// Sized shops (commercial) — Aaron's literal example.
// ---------------------------------------------------------------------------

test('BUG-641: sized shops — commercial demand over threshold names a real retail spec, sized and costed', () => {
  const s = shortfallState(6_000);
  const before = demandOf(s);
  assert.ok(before.commercial > ZONE_DEMAND_THRESHOLD, 'precondition: zero commercial buildings at pop 6,000 must trip the threshold');

  const plan = zoneDemandFixPlan(s);
  const item = plan.find((p) => p.zone === 'commercial');
  assert.ok(item, 'a commercial fix item must be present when demandOf().commercial is over threshold');
  assert.equal(item.demandIndex, before.commercial, 'demandIndex must be the SAME number demandOf() reports (SSOT, no re-derivation)');

  const sp = SPECS[item.specId];
  assert.ok(sp, 'specId must resolve to a real catalogue spec');
  assert.equal(sp.kind, 'commercial', 'the sized pick for a shops shortfall must be a retail (commercial-kind) spec, never housing/industry');

  // sizing: count must close AUTO_BUILD_DEMAND_FRACTION of the shortfall —
  // ceil(shortfall*FRACTION/unitCapacity), the SAME idiom BUG-606's
  // demandFixPlan()/rankedProviders() use for the coverage services.
  const expectedUnitCap = expectedUnitCapacity('commercial', sp);
  assert.equal(item.unitCapacity, expectedUnitCap, 'unitCapacity must match the independently-recomputed jobs capacity for the chosen spec');
  assert.ok(item.count > 0, 'a real shortfall must always yield a positive count');
  assert.ok(
    item.count * item.unitCapacity >= item.shortfall * AUTO_BUILD_DEMAND_FRACTION,
    `count x unitCapacity (${item.count * item.unitCapacity}) must cover AUTO_BUILD_DEMAND_FRACTION x shortfall (${item.shortfall * AUTO_BUILD_DEMAND_FRACTION})`,
  );
  assert.equal(item.planCost, item.count * placementCost(sp), 'planCost must be count * the SAME placementCost() the place/resolveDemand reducer would charge');

  // "cheapest picked": when an alternative exists it must never be strictly
  // cheaper than the chosen plan.
  if (item.alternative) {
    assert.ok(item.planCost <= item.alternative.planCost, 'the CHOSEN plan must never be pricier than its offered alternative');
  }

  const msg = zoneDemandMessage(item);
  assert.ok(msg.startsWith(`${ZONE_DEMAND_LABELS.commercial}:`), `message must open with the zone label, got: "${msg}"`);
  assert.ok(msg.includes(`${item.count} x ${sp.name}`), `message must name "${item.count} x ${sp.name}", got: "${msg}"`);
  assert.ok(msg.includes(`(£`), `message must include a real cost figure, got: "${msg}"`);
  assert.ok(msg.includes(`${AUTO_BUILD_DEMAND_PERCENT}%`), `message must cite AUTO_BUILD_DEMAND_PERCENT (GR#15, never a hardcoded "50%"/"150%"), got: "${msg}"`);
  if (item.alternative) {
    assert.ok(msg.includes('cheapest picked'), `message with a real alternative must include the "cheapest picked" clause, got: "${msg}"`);
  }
});

// ---------------------------------------------------------------------------
// Sized housing (residential).
// ---------------------------------------------------------------------------

test('BUG-641: sized housing — residential demand over threshold names a real housing spec, sized and costed', () => {
  // Residential demand rises when jobs outstrip workers (demandOf()'s `res`
  // term) — a handful of large office towers with a small population gives
  // jobs >> workers without needing any commercial/industrial buildings.
  const base = shortfallState(200);
  const s = {
    ...base,
    buildings: [
      { id: 1, spec: 'off_towers_downtown', x: 10, y: 10 },
      { id: 2, spec: 'off_towers_downtown', x: 20, y: 20 },
    ],
  };
  const before = demandOf(s);
  assert.ok(before.residential > ZONE_DEMAND_THRESHOLD, 'precondition: 4,000 vacant office jobs against 200 population must trip the residential threshold');

  const item = zoneDemandFixPlan(s).find((p) => p.zone === 'residential');
  assert.ok(item, 'a residential fix item must be present when demandOf().residential is over threshold');

  const sp = SPECS[item.specId];
  assert.equal(sp.kind, 'residential', 'the sized pick for a housing shortfall must be a residential spec');

  const workers = s.population * WORKING_AGE_FRACTION;
  const jobs = totalJobs(s);
  const expectedShortfall = Math.max(1, Math.round(jobs - workers));
  assert.equal(item.shortfall, expectedShortfall, 'residential shortfall must be the independently-recomputed (jobs - workers) gap, floored at 1');
  assert.equal(item.unitCapacity, expectedUnitCapacity('residential', sp));
  assert.ok(item.count * item.unitCapacity >= item.shortfall * AUTO_BUILD_DEMAND_FRACTION);
  assert.equal(item.planCost, item.count * placementCost(sp));

  const msg = zoneDemandMessage(item);
  assert.ok(msg.startsWith('Housing:'), `message must open with "Housing:", got: "${msg}"`);
  assert.ok(msg.includes(`${item.count} x ${sp.name}`));
});

// ---------------------------------------------------------------------------
// Sized industry.
// ---------------------------------------------------------------------------

test('BUG-641: sized industry — industrial demand over threshold names a real industrial/farm spec, sized and costed', () => {
  const s = shortfallState(6_000);
  const before = demandOf(s);
  assert.ok(before.industrial > ZONE_DEMAND_THRESHOLD, 'precondition: zero industrial buildings at pop 6,000 must trip the threshold');

  const item = zoneDemandFixPlan(s).find((p) => p.zone === 'industrial');
  assert.ok(item, 'an industrial fix item must be present when demandOf().industrial is over threshold');

  const sp = SPECS[item.specId];
  assert.equal(sp.kind, 'industrial', 'the sized pick for an industry shortfall must be an industrial-kind (incl. farm) spec');
  assert.equal(item.unitCapacity, expectedUnitCapacity('industrial', sp));
  assert.ok(item.count * item.unitCapacity >= item.shortfall * AUTO_BUILD_DEMAND_FRACTION);
  assert.equal(item.planCost, item.count * placementCost(sp));

  const msg = zoneDemandMessage(item);
  assert.ok(msg.startsWith('Industry:'), `message must open with "Industry:", got: "${msg}"`);
});

// ---------------------------------------------------------------------------
// Threshold boundary — demand 40 -> null, 41 -> item (real demandOf() values,
// located by scanning population, never assumed from the formula shape).
// ---------------------------------------------------------------------------

test('BUG-641: threshold boundary — demandOf().commercial === 40 (exactly at threshold) yields no fix item, 41 yields one', () => {
  // A coarse integer-population scan can straddle the exact 40/41 pair
  // without ever landing on it (demandOf() can jump by more than 1 per
  // extra citizen). SimState.population is a plain number (not int-typed),
  // so a fine-grained BINARY SEARCH over a fractional population pins the
  // exact index===40 / index===41 pair (demandOf() rounds internally via
  // Math.round — this only requires finding the real-valued population
  // whose rounded index crosses 40, not reimplementing the formula).
  let lo = 1; // known index <= 40 in practice (near-zero population)
  let hi = 5_000; // known index > 40 in practice (see the other zone tests)
  assert.ok(demandOf(shortfallState(lo)).commercial <= ZONE_DEMAND_THRESHOLD, 'precondition: lo bound must be at/under the threshold');
  assert.ok(demandOf(shortfallState(hi)).commercial > ZONE_DEMAND_THRESHOLD, 'precondition: hi bound must be over the threshold');
  for (let i = 0; i < 60; i++) {
    const mid = (lo + hi) / 2;
    const index = demandOf(shortfallState(mid)).commercial;
    if (index <= ZONE_DEMAND_THRESHOLD) lo = mid;
    else hi = mid;
  }
  const belowIndex = demandOf(shortfallState(lo)).commercial;
  const aboveIndex = demandOf(shortfallState(hi)).commercial;
  assert.equal(belowIndex, 40, 'binary search must converge on the EXACT index-40 population (demandOf() is a step function of Math.round)');
  assert.equal(aboveIndex, 41, 'the immediately-adjacent population must read exactly index 41');

  assert.equal(
    zoneDemandFixPlan(shortfallState(lo)).find((p) => p.zone === 'commercial'),
    undefined,
    'demand EXACTLY 40 (the threshold value itself) must NOT yield a commercial fix item — the gate is strictly-greater-than',
  );
  const item = zoneDemandFixPlan(shortfallState(hi)).find((p) => p.zone === 'commercial');
  assert.ok(item, 'demand 41 (one over the threshold) must yield a commercial fix item');
});

// ---------------------------------------------------------------------------
// Unlock-awareness — a locked spec is never recommended.
// ---------------------------------------------------------------------------

test('BUG-641: unlock-awareness — a fresh (level-1, unlockedAll:false) city never recommends a locked spec', () => {
  const base = initialState(); // xp: 30 -> levelOf 1, unlockedAll: false (engine.ts defaults)
  const s = { ...base, population: 6_000, funds: 1_000_000_000, administrationState: null };
  assert.equal(s.unlockedAll, false, 'precondition: this fixture must exercise real unlock gating, not the unlockedAll bypass');

  const before = demandOf(s);
  assert.ok(before.commercial > ZONE_DEMAND_THRESHOLD && before.industrial > ZONE_DEMAND_THRESHOLD, 'precondition: pop 6,000 with zero zone buildings trips both thresholds');

  const plan = zoneDemandFixPlan(s);
  for (const item of plan) {
    const sp = SPECS[item.specId];
    assert.ok(sp.unlock <= 1, `${item.zone}'s chosen spec ${item.specId} (unlock ${sp.unlock}) must be unlocked at level 1, never a locked spec`);
    if (item.alternative) {
      const altSp = SPECS[item.alternative.specId];
      assert.ok(altSp.unlock <= 1, `${item.zone}'s alternative spec ${item.alternative.specId} (unlock ${altSp.unlock}) must also be unlocked at level 1`);
    }
  }
  // Level-1 commercial unlock is exactly com_shop (com_retail unlocks at 2) —
  // pin the concrete pick so a future catalogue change that silently drops
  // unlock gating (e.g. accidentally matching com_retail) fails LOUD.
  const commercial = plan.find((p) => p.zone === 'commercial');
  assert.ok(commercial, 'precondition: a commercial fix item must be present at level 1');
  assert.equal(commercial.specId, 'com_shop', 'the only level-1 commercial spec is Corner Shop (com_shop)');
  // com_shop carries NO `jobs` field in the live catalogue — this is
  // precisely the totalJobs()-fallback case (data.ts ~2553); pin the exact
  // unitCapacity so a drift in that mirrored fallback constant fails LOUD
  // rather than silently under/over-sizing every commercial recommendation
  // that resolves to a jobs-less spec.
  const commercialSp = SPECS[commercial.specId];
  assert.equal(commercialSp.jobs, undefined, 'precondition: com_shop must be a jobs-less spec to exercise the fallback');
  assert.equal(commercial.unitCapacity, expectedUnitCapacity('commercial', commercialSp), 'unitCapacity for a jobs-less commercial spec must match the independently-recomputed totalJobs()-fallback figure');
});

// ---------------------------------------------------------------------------
// GR#21 purity — two identical states give identical items; no mutation.
// ---------------------------------------------------------------------------

test('BUG-641: purity — zoneDemandFixPlan/zoneDemandFix/worstAnyDemandFix are deterministic and never mutate state', () => {
  const s = shortfallState(7_500);
  const before = JSON.stringify(s);

  const planA = zoneDemandFixPlan(s);
  const planB = zoneDemandFixPlan(s);
  assert.deepEqual(planA, planB, 'zoneDemandFixPlan must return identical output for identical input (GR#21: no Date/Math.random)');

  const fixA = zoneDemandFix(s);
  const fixB = zoneDemandFix(s);
  assert.deepEqual(fixA, fixB, 'zoneDemandFix must be deterministic');

  const worstA = worstAnyDemandFix(s);
  const worstB = worstAnyDemandFix(s);
  assert.deepEqual(worstA, worstB, 'worstAnyDemandFix must be deterministic');

  assert.equal(JSON.stringify(s), before, 'none of zoneDemandFixPlan/zoneDemandFix/worstAnyDemandFix may mutate their input state');
});

test('BUG-641: purity — an identical SECOND state (deep-equal, different object identity) yields an identical plan', () => {
  const s1 = shortfallState(7_500);
  const s2 = shortfallState(7_500);
  assert.notEqual(s1, s2, 'precondition: the two states must be distinct objects');
  assert.deepEqual(zoneDemandFixPlan(s1), zoneDemandFixPlan(s2), 'two independently-constructed but value-identical states must yield an identical plan (no hidden clock/RNG/identity dependence)');
});

// ---------------------------------------------------------------------------
// worstAnyDemandFix — combines coverage and zone rankings.
// ---------------------------------------------------------------------------

test('BUG-641: worstAnyDemandFix returns null only when NEITHER a coverage nor a zone fix exists', () => {
  // Population 0, one residential building online (isOnline's "no builtTick"
  // escape hatch — `b.builtTick == null` covers `undefined`, so simply
  // omitting the field is the same always-online fixture as demand-fix-ui's
  // forceOnline()) so no service has real need and demandOf()'s popFactor
  // term is 0 for commercial/industrial; residential's jobs-vs-workers gap
  // is also 0 with zero jobs and zero workers. A lone res_hut generates a
  // small FIXED refuse tonnage regardless of live occupancy (wasteGeneratedOf
  // sums sp.residents, not population) — a waste_depot (50 t/tick, far above
  // one hut's 8-resident tonnage) is placed alongside it to keep 'refuse' out
  // of the coverage plan too (same fixture shape as demand-fix-ui.test.tsx's
  // (c) test), isolating this to a genuinely all-zero plan.
  const base = shortfallState(0);
  const s = {
    ...base,
    buildings: [
      { id: 1, spec: 'res_hut', x: 5, y: 5 },
      { id: 2, spec: 'waste_depot', x: 15, y: 15 },
    ],
  };
  const result = worstAnyDemandFix(s);
  assert.equal(result, null, 'a genuinely quiet city (no coverage shortfall, no zone pressure) must report no fix at all');
});

test('BUG-641: worstAnyDemandFix returns a real item when only zone demand (no coverage shortfall) is present', () => {
  // Population 0 means every serviceCoverageOf()/wasteStatsOf() `need` is 0
  // (demandFixPlan() empty), but a large office estate with 0 population
  // still drives commercial/industrial popFactor to 0 too (popFactor scales
  // with population) — so instead exercise the case with a small nonzero
  // population and zero zone buildings, which trips commercial/industrial
  // demand while genuinely deriving zero coverage need (nursery/primary/gp/
  // etc. all key off population too, but at a small population the ZONE
  // index crosses 40 well before the coverage `need` figures do — this is
  // asserted directly rather than assumed).
  const s = shortfallState(45);
  const covered = zoneDemandFixPlan(s).length > 0;
  assert.ok(covered, 'precondition: pop 45 with zero zone buildings must trip at least one zone threshold');
  const result = worstAnyDemandFix(s);
  assert.ok(result, 'worstAnyDemandFix must return a real item when a zone fix exists');
});

// ---------------------------------------------------------------------------
// Message format — mirrors demandFixMessage()'s BUG-606 shape exactly.
// ---------------------------------------------------------------------------

test('BUG-641: zoneDemandMessage omits the "cheapest picked" clause when only one provider is unlocked', () => {
  const base = initialState(); // level 1, unlockedAll: false
  const s = { ...base, population: 6_000, funds: 1_000_000_000, administrationState: null };
  const commercial = zoneDemandFixPlan(s).find((p) => p.zone === 'commercial');
  assert.ok(commercial);
  // com_shop is the ONLY commercial spec unlocked at level 1 (com_retail
  // unlocks at 2) — a single-provider case, same contract as
  // demandFixMessage()'s alternative:null branch.
  assert.equal(commercial.alternative, null, 'precondition: exactly one commercial provider is unlocked at level 1');
  const msg = zoneDemandMessage(commercial);
  assert.ok(!msg.includes('cheapest picked'), `single-provider message must not claim a "cheapest picked" alternative, got: "${msg}"`);
});
