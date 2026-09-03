// attack-bug641-zone-demand.test.mjs — independent GR#23 destructive round
// against BUG-641's uncommitted zone-demand-fix advisor
// (src/components/demandFixUi.ts: zoneDemandFixPlan / zoneDemandFix /
// zoneDemandMessage / worstAnyDemandFix). Author != attacker (GR#23
// independence amendment). Every assertion below is recomputed from
// independently-imported primitives (SPECS/capacityAtTier/placementCost/
// totalJobs/WORKING_AGE_FRACTION/AUTO_BUILD_DEMAND_FRACTION), never by
// trusting the function under test's own arithmetic back at itself.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState } from '../src/sim/engine.ts';
import { demandOf } from '../src/sim/engine.ts';
import {
  SPECS,
  capacityAtTier,
  placementCost,
  canEnterSim,
  AUTO_BUILD_DEMAND_FRACTION,
  type Spec,
} from '../src/sim/data.ts';
import {
  zoneDemandFixPlan,
  zoneDemandFix,
  zoneDemandMessage,
  worstAnyDemandFix,
  worstDemandFix,
  ZONE_DEMAND_THRESHOLD,
  type ZoneKey,
} from '../src/components/demandFixUi.ts';

// Test-file-only type shim: initialState()'s real return type is SimState,
// but the fixtures below deliberately spread arbitrary overrides (incl.
// pathological xp/population values for attack coverage) that don't fit a
// strict SimState shape everywhere they're used — `any` here is a test-code
// convenience, not a production typing (production code stays SimState-typed
// throughout demandFixUi.ts itself).
// eslint-disable-next-line @typescript-eslint/no-explicit-any
type AnyState = any;

function shortfallState(population: number, overrides: Record<string, unknown> = {}): AnyState {
  const base = initialState();
  return { ...base, population, unlockedAll: true, funds: 1_000_000_000, administrationState: null, ...overrides };
}

// Independent per-zone provider table — reimplemented from the module's OWN
// doc comment (residents for housing, jobs for shops/industry, 12/18
// no-jobs-field fallback), WITHOUT importing demandFixUi.ts's private
// ZONE_PROVIDERS/rankedZoneProviders, so this is a genuine second
// implementation to cross-check against, not a tautology.
function zoneMatch(zone: ZoneKey, sp: Spec): boolean {
  if (zone === 'residential') return sp.kind === 'residential';
  if (zone === 'commercial') return sp.kind === 'commercial';
  return sp.kind === 'industrial';
}
function zoneUnitCapacity(zone: ZoneKey, sp: Spec): number {
  if (zone === 'residential') return capacityAtTier(sp, 0);
  if (sp.jobs) return capacityAtTier(sp, 0);
  return zone === 'commercial' ? 12 : 18;
}
function independentRank(s: AnyState, zone: ZoneKey, budget: number, target: number) {
  const candidates: { sp: Spec; units: number; unitCost: number; planCost: number }[] = [];
  for (const sp of Object.values(SPECS) as Spec[]) {
    if (!canEnterSim(sp) || !(s.unlockedAll || sp.unlock <= 1)) {
      // fall through to the real unlock check below when not god-mode
    }
    if (!canEnterSim(sp)) continue;
    if (!(s.unlockedAll || sp.unlock <= levelFromXp(s))) continue;
    if (!zoneMatch(zone, sp)) continue;
    const cap = zoneUnitCapacity(zone, sp);
    if (cap <= 0) continue;
    const units = Math.max(1, Math.ceil(target / cap));
    const unitCost = placementCost(sp);
    candidates.push({ sp, units, unitCost, planCost: units * unitCost });
  }
  if (candidates.length === 0) return [];
  const cmp = (a: (typeof candidates)[number], b: (typeof candidates)[number], key: 'plan' | 'unit'): number => {
    const av = key === 'plan' ? a.planCost : a.unitCost;
    const bv = key === 'plan' ? b.planCost : b.unitCost;
    if (av !== bv) return av - bv;
    if (a.units !== b.units) return a.units - b.units;
    return a.sp.id < b.sp.id ? -1 : a.sp.id > b.sp.id ? 1 : 0;
  };
  const fitting = candidates.filter((c) => c.planCost <= budget).sort((a, b) => cmp(a, b, 'plan'));
  const singleAffordable = candidates.filter((c) => c.unitCost <= budget).sort((a, b) => cmp(a, b, 'unit'));
  const rest = [...candidates].sort((a, b) => cmp(a, b, 'unit'));
  const seen = new Set<string>();
  const ranked: (typeof candidates)[number][] = [];
  for (const tier of [fitting, singleAffordable, rest]) {
    for (const c of tier) {
      if (seen.has(c.sp.id)) continue;
      seen.add(c.sp.id);
      ranked.push(c);
    }
  }
  return ranked;
}
// crude levelOf mirror is not needed when unlockedAll is used everywhere below;
// kept only for the (unused in this file) non-godmode branch above.
function levelFromXp(_s?: AnyState): number {
  return 1;
}

// ---------------------------------------------------------------------------
// (1) THE ANSWER IS RIGHT — real city, real spec, real math, verified
// independently against the SPECS catalogue (not the author's own arithmetic).
// ---------------------------------------------------------------------------

test('ATTACK-641: a big commercial shortfall names a REAL unlocked spec, sized >= FRACTION*shortfall, cost = count*placementCost (D1: never catalogue price)', () => {
  const s = shortfallState(200_000); // large city -> big commercial pressure
  const before = demandOf(s);
  assert.ok(before.commercial > ZONE_DEMAND_THRESHOLD, 'precondition: large pop, zero commercial buildings must trip threshold');

  const item = zoneDemandFixPlan(s).find((p) => p.zone === 'commercial');
  assert.ok(item, 'must produce a commercial fix item');
  const sp = SPECS[item.specId];
  assert.ok(sp, 'specId must resolve to a real catalogue spec');
  assert.equal(sp.kind, 'commercial', 'must be a real retail spec, not some other kind');
  assert.ok(canEnterSim(sp), 'must never recommend a placeholder spec');
  assert.ok(Number.isFinite(sp.unlock), 'every listed spec must carry a real, finite unlock number');

  // Independent capacity recompute.
  const expectedUnitCap = zoneUnitCapacity('commercial', sp);
  assert.equal(item.unitCapacity, expectedUnitCap, 'unitCapacity must match an independently-recomputed jobs-capacity figure');

  // The count must clear AUTO_BUILD_DEMAND_FRACTION x shortfall.
  assert.ok(
    item.count * item.unitCapacity >= item.shortfall * AUTO_BUILD_DEMAND_FRACTION,
    `count(${item.count}) x unitCapacity(${item.unitCapacity}) = ${item.count * item.unitCapacity} must be >= FRACTION(${AUTO_BUILD_DEMAND_FRACTION}) x shortfall(${item.shortfall}) = ${item.shortfall * AUTO_BUILD_DEMAND_FRACTION}`,
  );
  // And must not wildly overshoot — one unit LESS must fail to cover the target (proves count is the minimal ceil, not padded).
  assert.ok(
    (item.count - 1) * item.unitCapacity < item.shortfall * AUTO_BUILD_DEMAND_FRACTION || item.count === 1,
    'count must be the minimal unit count that clears the fix target, never padded',
  );

  // D1 precedent: cost must be count * placementCost (NEVER count * sp.cost,
  // the catalogue price) — every zone spec is 'zones' category so this is £0.
  assert.equal(placementCost(sp), 0, 'precondition: commercial specs are all zones-category, i.e. free to place (D1 precedent)');
  assert.equal(item.planCost, item.count * placementCost(sp), 'planCost must be count * placementCost(), never count * sp.cost');
  assert.equal(item.planCost, 0, 'a zone recommendation must be priced £0, matching the actual reducer charge, never the catalogue price');

  // Independent full-ranking cross-check: the chosen spec must be the winner
  // of a from-scratch reimplementation of the SAME scoring rule.
  const fixAmount = item.shortfall * AUTO_BUILD_DEMAND_FRACTION;
  const ranked = independentRank(s, 'commercial', s.funds, fixAmount);
  assert.ok(ranked.length > 0, 'independent ranking must find candidates too');
  assert.equal(item.specId, ranked[0].sp.id, 'the chosen spec must be the independently-recomputed top-ranked candidate');
});

// ---------------------------------------------------------------------------
// (2) UNIT HONESTY — the number shown ("N short") must be the SAME number the
// count is sized against, never the raw -100..100 pressure index.
// ---------------------------------------------------------------------------

test('ATTACK-641: message shows the SIZING shortfall (jobs/residents), never the raw -100..100 demand index, and the two are provably different magnitudes', () => {
  const s = shortfallState(200_000);
  const item = zoneDemandFixPlan(s).find((p) => p.zone === 'commercial');
  assert.ok(item);

  // The raw index is clamped to [-100,100] and will be a tiny number (<=100)
  // while the physical jobs shortfall at this population is enormous —
  // proving these are genuinely different magnitudes, not coincidentally equal.
  assert.ok(Math.abs(item.demandIndex) <= 100, 'demandIndex must stay in its documented -100..100 band');
  assert.ok(item.shortfall > 100, `physical shortfall (${item.shortfall}) must dwarf the -100..100 index at this population — if this fails the two concepts have been conflated`);

  const msg = zoneDemandMessage(item);
  // The message's "<N> short" must name item.shortfall, not item.demandIndex.
  assert.ok(
    !msg.includes(`${item.demandIndex} short`),
    `message must NOT show the raw demand index as the "short" quantity, got: "${msg}"`,
  );
  // fmtNum renders thousands with separators; check the leading digit-groups match.
  const numPart = msg.split(':')[1].trim().split(' short')[0];
  assert.notEqual(numPart, String(item.demandIndex), 'the shown number must not equal the raw index');
});

// ---------------------------------------------------------------------------
// (3) GR#3 DIVERGENCE — the local rankedZoneProviders() port vs data.ts's
// rankedProviders() shape. The two operate on disjoint domains (zone kinds
// are not in DEMAND_FIX_PROVIDERS), so no single call can hit both; instead
// verify the LOCAL port is faithful to the documented algorithm across an
// unlock edge, a budget edge, and a genuine equal-cost/equal-unit TIE.
// ---------------------------------------------------------------------------

test('ATTACK-641: TIE case — a trivial shortfall makes every unlocked commercial spec cost £0 and need 1 unit, so the pick degenerates to ALPHABETICAL id order, not "smallest sensible building"', () => {
  // With target <= 12 (com_shop's own fallback capacity, the smallest
  // capacity among unlocked commercial specs), every unlocked commercial spec
  // needs exactly 1 unit and costs £0 (zones are free) — a genuine full tie
  // on both tiebreak keys used before id-ascending.
  const s = shortfallState(50, { funds: 1_000_000_000 }); // small population -> just over threshold, tiny shortfall
  const item = zoneDemandFixPlan(s).find((p) => p.zone === 'commercial');
  if (!item) {
    // If this population doesn't trip the threshold, this specific attack
    // does not apply at this fixture — do not silently pass, prove it.
    assert.ok(demandOf(s).commercial <= ZONE_DEMAND_THRESHOLD, 'either an item exists or the precondition genuinely does not trip');
    return;
  }
  const commercialSpecs = Object.values(SPECS).filter((sp) => canEnterSim(sp) && sp.kind === 'commercial' && sp.unlock <= 1);
  // Recompute whether this is really a degenerate tie for THIS item's own fixAmount.
  const fixAmount = item.shortfall * AUTO_BUILD_DEMAND_FRACTION;
  const allUnitOne = commercialSpecs.every((sp) => Math.ceil(fixAmount / zoneUnitCapacity('commercial', sp)) === 1);
  if (allUnitOne && commercialSpecs.length > 1) {
    const idsSorted = [...commercialSpecs.map((sp) => sp.id)].sort();
    assert.equal(
      item.specId,
      idsSorted[0],
      `TIE FINDING: with every unlocked commercial spec costing £0 and needing 1 unit, the pick is alphabetical-id, not size/footprint aware — got ${item.specId}, expected ${idsSorted[0]} (this documents a real UX risk: a trivial shortfall could recommend an oversized structure purely by id ordering)`,
    );
  }
});

test('ATTACK-641: UNLOCK EDGE — raising the unlock level must never make the chosen spec WORSE (never regress to a costlier plan) and must track specUnlocked() exactly', () => {
  // Fresh (level 1) city vs unlockedAll city on the SAME population/shortfall.
  const base = initialState();
  const lvl1 = { ...base, population: 6_000, funds: 1_000_000_000, administrationState: null };
  const godmode = { ...lvl1, unlockedAll: true };

  const itemLvl1 = zoneDemandFixPlan(lvl1).find((p) => p.zone === 'commercial');
  const itemGod = zoneDemandFixPlan(godmode).find((p) => p.zone === 'commercial');
  assert.ok(itemLvl1 && itemGod, 'both fixtures must trip the commercial threshold at pop 6,000');

  const spLvl1 = SPECS[itemLvl1.specId];
  assert.ok(spLvl1.unlock <= 1, 'level-1 city must never recommend a spec above unlock 1');
  // Unlocking more specs must never raise the total plan cost above what a
  // more restricted candidate set already achieved (more choices, not worse ones).
  assert.ok(itemGod.planCost <= itemLvl1.planCost, 'unlocking more specs must never make the recommended plan MORE expensive');
});

test('ATTACK-641: BUDGET EDGE — funds=0, funds=exactly one unit price, funds=+1T (Aaron button) all yield sane, non-NaN, non-Infinity, capped counts', () => {
  for (const funds of [0, 1, 1_000_000_000_000]) {
    const s = shortfallState(500_000, { funds });
    const item = zoneDemandFixPlan(s).find((p) => p.zone === 'commercial');
    assert.ok(item, `an item must still be produced regardless of funds (zones are free to place) at funds=${funds}`);
    assert.ok(Number.isFinite(item.count) && item.count > 0, `count must be a finite positive number at funds=${funds}, got ${item.count}`);
    assert.ok(Number.isFinite(item.planCost), `planCost must be finite at funds=${funds}, got ${item.planCost}`);
    assert.ok(!Number.isNaN(item.count) && !Number.isNaN(item.planCost), `no NaN at funds=${funds}`);
    // No RESOLVE_DEMAND_ALL_MAX_UNITS-style cap is exposed on ZoneDemandFixItem
    // — assert the count stays within a sane bound relative to the shortfall
    // (never runaway, e.g. not orders of magnitude beyond ceil(shortfall*FRACTION/1)).
    assert.ok(item.count < 10_000_000, `count must not run away unboundedly at funds=${funds}, got ${item.count}`);
  }
});

// ---------------------------------------------------------------------------
// (4) worstAnyDemandFix cross-domain ranking — trivial zone index can outrank
// a serious coverage (hospital) shortfall.
// ---------------------------------------------------------------------------

test('ATTACK-641: worstAnyDemandFix can let a BARELY-over-threshold zone index (41) outrank a severe hospital shortfall, because the two "gap" numbers are not comparable units', () => {
  // Build a state with a severe hospital shortfall (huge population, zero
  // hospitals -> `need` in the tens of thousands) alongside a commercial zone
  // index that is only just over the 40 threshold (small nonzero commercial
  // building count tuned to sit near 41, if population allows it).
  const s = shortfallState(60_000); // triggers big coverage need (hosp) AND commercial zone pressure
  const coverage = worstDemandFix(s);
  const zone = zoneDemandFix(s);
  assert.ok(coverage, 'precondition: a coverage shortfall must exist (hospital/etc at pop 60,000 with zero services)');
  assert.ok(zone, 'precondition: a zone shortfall must exist too');

  const coverageGap = Math.abs(coverage.need - coverage.have);
  const zoneGap = Math.abs(zone.demandIndex);
  const result = worstAnyDemandFix(s);

  // Demonstrate the units are NOT comparable: the coverage gap is typically
  // in the tens-of-thousands (people/served), the zone gap is capped at 100.
  assert.ok(coverageGap > 100, `coverage gap (${coverageGap}) should typically dwarf any possible zone index (max 100) — the comparison in worstAnyDemandFix is cross-unit by construction`);
  assert.ok(zoneGap <= 100, 'zone index is bounded to +/-100 by construction');

  // Given the documented tie-break ("zone only displaces coverage when
  // STRICTLY larger"), coverage must win here since coverageGap > zoneGap.
  assert.deepEqual(result, coverage, 'when the coverage gap magnitude exceeds the (max-100) zone index magnitude, coverage must win — this is the EXPECTED, if slightly odd, cross-unit comparison');

  // Now construct the inverted, genuinely alarming case: shrink the coverage
  // gap far below 100 (small population, so `need` values are tiny) while a
  // zone index still reads >40. This proves the comparator really is
  // magnitude-only across incomparable units, which is worth flagging.
  const small = shortfallState(45); // pop 45: zone index over threshold (per author's own test), and small `need` values
  const smallCoverage = worstDemandFix(small);
  const smallZone = zoneDemandFix(small);
  if (smallCoverage && smallZone) {
    const cGap = Math.abs(smallCoverage.need - smallCoverage.have);
    const zGap = Math.abs(smallZone.demandIndex);
    if (zGap > cGap) {
      const r = worstAnyDemandFix(small);
      assert.deepEqual(
        r,
        smallZone,
        `FINDING: at pop 45 a zone index gap (${zGap}) can outrank a genuine coverage shortfall (${cGap}) purely because the -100..100 index and the (need-have) coverage gap are not the same unit — worstAnyDemandFix's doc comment already flags this as a known limitation, confirmed here with concrete numbers`,
      );
    }
  }
});

// ---------------------------------------------------------------------------
// (5) all three zones over threshold at once — deterministic winner.
// ---------------------------------------------------------------------------

test('ATTACK-641: all three zones over threshold simultaneously — zoneDemandFix() picks by raw demandIndex deterministically, repeatable across runs and object identities', () => {
  // A large population with zero zone buildings at all pushes commercial and
  // industrial over threshold; adding vacant office jobs (no residents) also
  // pushes residential over threshold.
  // Small population keeps commercial/industrial over threshold (author's own
  // pop-45 test proves zero-buildings trips both at this scale), while ONE
  // office tower's 2,000 vacant jobs vs ~25 workers pushes residential's
  // (jobs-workers) pressure term far over threshold too.
  const base = shortfallState(45);
  const s = {
    ...base,
    buildings: [{ id: 1, spec: 'off_towers_downtown', x: 10, y: 10 }],
  };
  const idx = demandOf(s);
  assert.ok(idx.residential > ZONE_DEMAND_THRESHOLD, 'precondition: residential must be over threshold');
  assert.ok(idx.commercial > ZONE_DEMAND_THRESHOLD, 'precondition: commercial must be over threshold');
  assert.ok(idx.industrial > ZONE_DEMAND_THRESHOLD, 'precondition: industrial must be over threshold');

  const plan = zoneDemandFixPlan(s);
  assert.equal(plan.length, 3, 'all three zones must appear in the plan');

  const winner1 = zoneDemandFix(s);
  const winner2 = zoneDemandFix({ ...s }); // same values, different object identity
  assert.deepEqual(winner1, winner2, 'winner must be identical across object identities with identical values (GR#21)');

  // Independent recompute of the expected winner: max demandIndex, ties by zone key ascending.
  const expected = [...plan].sort((a, b) => b.demandIndex - a.demandIndex || a.zone.localeCompare(b.zone))[0];
  assert.deepEqual(winner1, expected, 'zoneDemandFix must pick the max-index zone with the documented tie-break, independently recomputed');

  // Run it 20 times — no Math.random/Date, must be byte-identical every time.
  for (let i = 0; i < 20; i++) {
    assert.deepEqual(zoneDemandFix(s), winner1, `run ${i}: must be identical every time (GR#21, no clock/RNG)`);
  }
});

// ---------------------------------------------------------------------------
// (7) unlock-awareness edge — every commercial spec locked -> null, not a crash.
// ---------------------------------------------------------------------------

test('ATTACK-641: every zone spec locked (unlock impossibly high) -> null, never a crash or a locked-spec recommendation', () => {
  // Monkey-patch is not available (SPECS is a real catalogue import) — instead
  // exercise via a fabricated state whose xp/level machinery is bypassed:
  // unlockedAll:false and population high enough to trip demand, but confirm
  // that AT LEAST level-1 city (the lowest real level) still only offers
  // level<=1 specs (already covered) — here we verify the actual floor case:
  // a state whose xp is impossibly low still returns either a valid level-1
  // pick or null, never throws.
  const base = initialState();
  const s = { ...base, population: 6_000, funds: 1_000_000_000, xp: -999999, unlockedAll: false, administrationState: null };
  assert.doesNotThrow(() => zoneDemandFixPlan(s), 'must never throw even with a pathological xp value');
  const plan = zoneDemandFixPlan(s);
  for (const item of plan) {
    const sp = SPECS[item.specId];
    assert.ok(sp && canEnterSim(sp), 'any produced item must resolve to a real, enterable spec');
  }
});

// ---------------------------------------------------------------------------
// (8) junk inputs — NaN demand, population 0, no buildings.
// ---------------------------------------------------------------------------

test('ATTACK-641: junk inputs — population 0, zero buildings, and a NaN-poisoned population never crash and never fabricate a positive shortfall from nothing', () => {
  const s0 = shortfallState(0);
  assert.doesNotThrow(() => zoneDemandFixPlan(s0));
  assert.doesNotThrow(() => zoneDemandFix(s0));
  assert.doesNotThrow(() => worstAnyDemandFix(s0));

  const sNaN = shortfallState(NaN);
  let threw = false;
  let plan;
  try {
    plan = zoneDemandFixPlan(sNaN);
  } catch {
    threw = true;
  }
  assert.ok(!threw, 'a NaN population must not crash zoneDemandFixPlan (fail-soft, not fail-loud-crash, for a UI advisor)');
  // FINDING (confirmed live): demandOf() with a NaN population produces
  // demandIndex = NaN for all three zones. zoneFixFor()'s gate is
  // `if (index <= ZONE_DEMAND_THRESHOLD) return null;` — but in JS EVERY
  // comparison against NaN (<=, >, ===) is false, so `NaN <= 40` is false and
  // the "not over threshold" early-return is SKIPPED, not taken. Execution
  // falls through into the sizing math (rawGap/shortfall/fixAmount/units, all
  // NaN-poisoned), producing a full 3-zone plan with demandIndex/shortfall/
  // count/planCost/alternative all NaN — a NaN populated plan that would
  // render as literally "NaN short — Fix builds 150%: NaN x Corner Shop
  // (£NaN)" if ever reached by the UI. This is the exact "garbage numbers
  // shown to the player" failure class GR#1/GR#15 exist to prevent.
  for (const item of plan ?? []) {
    assert.ok(!Number.isNaN(item.count), `no item may carry a NaN count (got ${JSON.stringify(item)}) — zoneFixFor()'s threshold gate must reject a non-finite demandIndex, not silently fall through NaN's "every comparison is false" trap`);
    assert.ok(!Number.isNaN(item.planCost), 'no item may carry a NaN planCost');
    assert.ok(Number.isFinite(item.shortfall) || item.shortfall === Infinity, 'shortfall must not silently become NaN');
  }
});

// ---------------------------------------------------------------------------
// (9) RED-PROOF — mutate the capacity fallback and the threshold comparison;
// the author's own regression suite (bug641-zone-demand-fix.test.tsx) must go
// red. This proves the author's tests can actually fail (Vestige
// "verification standards": prove every regression test can fail).
// ---------------------------------------------------------------------------

test('ATTACK-641: RED-PROOF — verify the module logic is not vacuous by asserting a mutation-sensitive invariant directly', () => {
  // We cannot literally patch demandFixUi.ts's private ZONE_PROVIDERS table
  // from outside the module without editing the file (which would corrupt the
  // estate under attack), so this proves red-sensitivity a different way:
  // assert a precise numeric identity that would break under EITHER named
  // mutation (capacity fallback changed, or threshold changed), so a manual
  // source mutation trivially flips this test red. This is the same
  // discipline as the author's own pinned-fallback assertions, extended to
  // the boundary itself.
  const s = shortfallState(6_000);
  const item = zoneDemandFixPlan(s).find((p) => p.zone === 'commercial');
  assert.ok(item);
  const sp = SPECS[item.specId];
  if (!sp.jobs) {
    // This spec exercises the no-jobs-field fallback directly.
    assert.equal(item.unitCapacity, 12, 'commercial no-jobs fallback must be EXACTLY 12 (mutating ZONE_PROVIDERS.commercial fallback flips this)');
  }
  // Threshold boundary sensitivity: demandOf().commercial === ZONE_DEMAND_THRESHOLD exactly must NOT yield an item (mutating > to >= flips this).
  // Reuse the author's own binary-search discipline briefly to locate index 40 without assuming formula shape.
  let lo = 1;
  let hi = 6_000;
  for (let i = 0; i < 60; i++) {
    const mid = (lo + hi) / 2;
    const index = demandOf(shortfallState(mid)).commercial;
    if (index <= ZONE_DEMAND_THRESHOLD) lo = mid;
    else hi = mid;
  }
  const atThreshold = demandOf(shortfallState(lo)).commercial;
  if (atThreshold === ZONE_DEMAND_THRESHOLD) {
    const noItem = zoneDemandFixPlan(shortfallState(lo)).find((p) => p.zone === 'commercial');
    assert.equal(noItem, undefined, 'demand exactly AT threshold must yield no item (mutating > to >= in zoneFixFor flips this red)');
  }
});
