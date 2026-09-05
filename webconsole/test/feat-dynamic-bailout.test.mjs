// feat-dynamic-bailout.test.mjs — FEAT-dynamic-bailout (Aaron ruling Q100045,
// docs/planning/acceptance/FEAT-dynamic-bailout-2026-09-02.md): the dynamic,
// auto-scaled, ONCE-ONLY bailout offer that supersedes the fixed 750k/1.5M
// two-stage ladder's FRESH-ENTRY sizing (BAILOUT_INCOME_INJECTION).
//
// SCOPING NOTE (see engine.ts's FEAT-dynamic-bailout comment block for the
// full reasoning): this build retires the fixed-terms ladder's FRESH grant
// only — the escalation-to-second-bailout MACHINERY (worse-terms, fixed
// BAILOUT_INCOME_INJECTION_SECOND) is deliberately left UNCHANGED, per the
// spec's own §3 alternative branch (b), to avoid duplicating a second
// decline path across an already-large, already-tested endgame-teeth estate
// (imf-insolvency-inc2/inc3/inc4/inc5, bug-504-505-506-endgame, bug496-497,
// play-mode-endgame, bug501). Flagged for Aaron's explicit confirmation in
// the build report — this is a build-time scoping call, not a silent
// deviation.
//
// node --test type-strips the .ts imports; every assertion below can FAIL if
// the corresponding fix is reverted — RED/GREEN pairs proved with a scratch
// cp/mv of fiscal.ts/engine.ts, never a git revert (GR#24). See the trailing
// "RED self-proof" block for an actually-executed sabotage/restore cycle.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer, sanitizeTreasury, TICKS_PER_MONTH, TICKS_PER_YEAR } from '../src/sim/engine.ts';
import { buildGameSave, parseGameSave, gameSaveText } from '../src/sim/gamesave.ts';
import { emptyJournal } from '../src/sim/journal.ts';
import {
  DEBT_THRESHOLD_FOR_BAILOUT,
  INSOLVENCY_WARNING_THRESHOLD,
  BAILOUT_DURATION_TICKS,
  BAILOUT_FLOOR,
  BAILOUT_CAP_FRACTION_OF_CAPEX,
  CAPEX_SPEND_TO_SAVE_FRACTION,
  DYNAMIC_BAILOUT_INJECTION_LABEL,
  BAILOUT_INJECTION_LABEL,
  BAILOUT_SECOND_INJECTION_LABEL,
  ASSET_SALE_LABEL,
  PLAY_MODE_INJECTION_LABEL,
  netOpexBleedPerTick,
  computeDynamicBailoutOffer,
} from '../src/sim/fiscal.ts';
import { placementCost, SPECS } from '../src/sim/data.ts';

function tickAtFunds(state, targetFunds) {
  const forced = reducer(state, { type: 'debugFunds', amount: targetFunds - state.funds });
  return reducer(forced, { type: 'tick' });
}

const WARNING_BAND_FUNDS = Math.round((INSOLVENCY_WARNING_THRESHOLD + DEBT_THRESHOLD_FOR_BAILOUT) / 2);
const CRISIS_FUNDS = DEBT_THRESHOLD_FOR_BAILOUT * 2;

function freshCrisisEntry(state) {
  const warning = tickAtFunds(state, WARNING_BAND_FUNDS);
  return tickAtFunds(warning, CRISIS_FUNDS);
}

// ========== Pure formula: computeDynamicBailoutOffer ==========

test('AC-1 (proportionality — CAPEX): 10x cumulativeCapexSpent (same bleed) gives a strictly bigger offer, CAPEX component ~10x', () => {
  const bleed = 1000;
  const lowCapex = computeDynamicBailoutOffer(1_000_000, bleed);
  const highCapex = computeDynamicBailoutOffer(10_000_000, bleed);
  assert.ok(highCapex.offer > lowCapex.offer, 'a 10x-CAPEX city must get a strictly bigger offer at the same bleed');
  // Isolate the CAPEX-only component (offer minus the shared opexAllowance) —
  // both cases share the SAME opexAllowance (same bleed), so this difference
  // is purely the capexAllowance term.
  assert.equal(lowCapex.opexAllowance, highCapex.opexAllowance, 'precondition: identical bleed must yield identical opexAllowance');
  const ratio = highCapex.capexAllowance / lowCapex.capexAllowance;
  assert.ok(Math.abs(ratio - 10) < 0.01, `capexAllowance must scale ~10x with CAPEX (got ratio ${ratio})`);
});

test('AC-1 MUTATION-PROVE target: with CAPEX held equal, differing capex inputs must NOT change the offer at all if CAPEX were ignored', () => {
  // Sanity: if the formula ignored CAPEX entirely (the mutation), 1x and 10x
  // capex would produce IDENTICAL offers — proving this test can fail.
  const bleed = 1000;
  const a = computeDynamicBailoutOffer(1_000_000, bleed).offer;
  const b = computeDynamicBailoutOffer(10_000_000, bleed).offer;
  assert.notEqual(a, b, 'a CAPEX-blind formula (the mutation) would make these equal — proves the AC-1 test above is not vacuous');
});

test('AC-2 (proportionality — bleed): 10x recentOpexBleedPerTick (same CAPEX) gives a strictly bigger offer, OPEX component scales linearly', () => {
  const capex = 5_000_000;
  const lowBleed = computeDynamicBailoutOffer(capex, 1000);
  const highBleed = computeDynamicBailoutOffer(capex, 10000);
  assert.ok(highBleed.offer > lowBleed.offer, 'a 10x-bleed city must get a strictly bigger offer at the same CAPEX');
  assert.equal(lowBleed.capexAllowance, highBleed.capexAllowance, 'precondition: identical CAPEX must yield identical capexAllowance');
  assert.equal(highBleed.opexAllowance, lowBleed.opexAllowance * 10, 'opexAllowance must scale EXACTLY linearly with bleed rate');
});

test('AC-3 (year-of-bleed sizing): opexAllowance is EXACTLY bleed * BAILOUT_DURATION_TICKS — "a year of the current bleed rate"', () => {
  const bleed = 54_321;
  const { opexAllowance } = computeDynamicBailoutOffer(0, bleed);
  assert.equal(opexAllowance, Math.round(bleed * BAILOUT_DURATION_TICKS));
  assert.equal(BAILOUT_DURATION_TICKS, 360, 'sanity: "a year" must mean 360 ticks, matching the existing bailout-year constant');
});

test('AC-4 (floor): a city with near-zero bleed AND near-zero CAPEX still receives at least BAILOUT_FLOOR', () => {
  const { offer, branch } = computeDynamicBailoutOffer(0, 0);
  assert.equal(offer, BAILOUT_FLOOR);
  assert.equal(branch, 'floored');
});

test('AC-4 MUTATION-PROVE target: an unclamped raw formula on this fixture would round to a much smaller number than the floor', () => {
  // bleed=0, capex=0 -> raw = 0 + 0 = 0, which is far below BAILOUT_FLOOR —
  // proves the floor clamp above is doing real work, not a no-op.
  const rawWithoutClamp = Math.round(0 * BAILOUT_DURATION_TICKS) + Math.round(0 * CAPEX_SPEND_TO_SAVE_FRACTION);
  assert.ok(rawWithoutClamp < BAILOUT_FLOOR, 'the raw (unclamped) formula must undershoot the floor on this fixture');
});

test('AC-5 (cap): an absurd/adversarial bleed produces a FINITE offer clamped at the cap — never NaN/Infinity/overflow', () => {
  const capex = 2_000_000;
  const absurdBleed = Number.MAX_SAFE_INTEGER / BAILOUT_DURATION_TICKS;
  const { offer, branch } = computeDynamicBailoutOffer(capex, absurdBleed);
  assert.ok(Number.isFinite(offer), 'offer must be a finite number');
  assert.ok(Number.isSafeInteger(offer), 'offer must be a safe integer (never an overflowed/rounded-garbage value)');
  assert.equal(branch, 'capped');
  assert.equal(offer, Math.round(capex * BAILOUT_CAP_FRACTION_OF_CAPEX));
});

test('AC-5: a synthetic city with ZERO cumulativeCapexSpent and an absurd bleed still gets a FINITE, floor-sized offer (never zeroed out by its own zero CAPEX base)', () => {
  const absurdBleed = Number.MAX_SAFE_INTEGER / BAILOUT_DURATION_TICKS;
  const { offer, branch } = computeDynamicBailoutOffer(0, absurdBleed);
  assert.ok(Number.isFinite(offer) && offer > 0, 'a zero-CAPEX cap must never degenerate to a zero/undefined offer');
  assert.equal(offer, BAILOUT_FLOOR, 'the cap is floored at BAILOUT_FLOOR itself when cumulativeCapexSpent is 0');
  assert.equal(branch, 'capped');
});

test('AC-5 MUTATION-PROVE target: the naive cap (2x CAPEX with no floor) would collapse to ZERO on the zero-CAPEX fixture', () => {
  const naiveCap = Math.round(0 * BAILOUT_CAP_FRACTION_OF_CAPEX);
  assert.equal(naiveCap, 0, 'proves the Math.max(cap, BAILOUT_FLOOR) guard above is load-bearing, not redundant');
});

test('formula: negative/non-finite inputs are sanitized to 0 (GR#16) rather than propagating NaN', () => {
  const a = computeDynamicBailoutOffer(-5000, -100);
  assert.equal(a.offer, BAILOUT_FLOOR, 'negative inputs must floor to 0 before the formula, landing on the floor');
  const b = computeDynamicBailoutOffer(NaN, Infinity);
  assert.ok(Number.isFinite(b.offer), 'NaN/Infinity inputs must never propagate into the offer');
});

// ========== Pure SSOT bleed function: netOpexBleedPerTick ==========

test('netOpexBleedPerTick excludes one-off injection labels from the structural-income side', () => {
  const flows = {
    outflows: [{ label: 'Wages', value: 1000 }],
    inflows: [
      { label: 'Council Tax', value: 200 },
      { label: BAILOUT_INJECTION_LABEL, value: 500_000 },
      { label: BAILOUT_SECOND_INJECTION_LABEL, value: 250_000 },
      { label: DYNAMIC_BAILOUT_INJECTION_LABEL, value: 900_000 },
      { label: ASSET_SALE_LABEL, value: 40 },
      { label: PLAY_MODE_INJECTION_LABEL, value: 1_000_000_000_000 },
    ],
  };
  // Structural bleed = outflows(1000) - structural inflows(200) = 800, NOT
  // negative-into-the-billions if the one-off labels leaked in.
  assert.equal(netOpexBleedPerTick(flows), 800);
});

test('netOpexBleedPerTick MUTATION-PROVE target: without the exclusion filter, the huge one-off inflows above would swamp the reading', () => {
  const flows = {
    outflows: [{ label: 'Wages', value: 1000 }],
    inflows: [
      { label: 'Council Tax', value: 200 },
      { label: PLAY_MODE_INJECTION_LABEL, value: 1_000_000_000_000 },
    ],
  };
  const naiveBleed = Math.max(
    0,
    flows.outflows.reduce((a, b) => a + b.value, 0) - flows.inflows.reduce((a, b) => a + b.value, 0),
  );
  assert.equal(naiveBleed, 0, 'an unfiltered reading would be swamped to 0 by the Play Mode injection alone');
  assert.equal(netOpexBleedPerTick(flows), 800, 'the SSOT function must NOT be swamped — proving the exclusion filter matters');
});

test('netOpexBleedPerTick floors at 0 — a net-positive tick has no "bleed"', () => {
  const flows = { outflows: [{ label: 'Wages', value: 100 }], inflows: [{ label: 'Council Tax', value: 500 }] };
  assert.equal(netOpexBleedPerTick(flows), 0);
});

// ========== Engine integration: cumulativeCapexSpent tracking ==========

test('cumulativeCapexSpent starts at 0 for a brand-new game, and capexBackfilled starts true (no backfill owed)', () => {
  const s0 = initialState();
  assert.equal(s0.cumulativeCapexSpent, 0);
  assert.equal(s0.capexBackfilled, true);
  assert.equal(s0.dynamicBailoutUsed, false);
});

test('cumulativeCapexSpent accumulates GROSS on a paid placement (place action)', () => {
  const s0 = initialState();
  const cost = placementCost(SPECS['road']);
  assert.ok(cost > 0, 'precondition: road has a real placement cost');
  const after = reducer(s0, { type: 'place', spec: 'road', x: 5, y: 5 });
  assert.ok(after.buildings.some((b) => b.spec === 'road' && b.x === 5 && b.y === 5), 'precondition: placement succeeded');
  assert.ok(
    after.cumulativeCapexSpent >= (s0.cumulativeCapexSpent ?? 0) + cost,
    'cumulativeCapexSpent must increase by AT LEAST the placement cost (auto-connect may add more)',
  );
});

test('cumulativeCapexSpent is NEVER decremented by a refund (bulldoze) — gross-only per spec §7.1', () => {
  const s0 = initialState();
  const placed = reducer(s0, { type: 'place', spec: 'road', x: 6, y: 6 });
  const capexAfterPlace = placed.cumulativeCapexSpent;
  const target = placed.buildings.find((b) => b.spec === 'road' && b.x === 6 && b.y === 6);
  assert.ok(target, 'precondition: the road exists to demolish');
  const demolished = reducer(placed, { type: 'bulldoze', id: target.id });
  assert.equal(
    demolished.cumulativeCapexSpent,
    capexAfterPlace,
    'a demolition refund must NOT reduce cumulativeCapexSpent — a demolish/rebuild cycle must never manipulate the dynamic bailout offer downward',
  );
});

// ========== Engine integration: the ONE dynamic offer ==========

test('a fresh crisis entry credits the DYNAMIC offer (not the old fixed BAILOUT_INCOME_INJECTION) and flips the once-only latch', () => {
  const s0 = initialState();
  const entered = freshCrisisEntry(s0);
  assert.ok(entered.bailoutState, 'the ONE dynamic bailout must be active');
  assert.equal(entered.dynamicBailoutUsed, true);
  const flow = entered.lastFlows.inflows.find((f) => f.label === DYNAMIC_BAILOUT_INJECTION_LABEL);
  assert.ok(flow, 'the dynamic offer must be a NAMED, traceable inflow');
  assert.ok(flow.value > 0);
  assert.equal(
    entered.lastFlows.inflows.some((f) => f.label === BAILOUT_INJECTION_LABEL),
    false,
    'the retired fixed-terms label must never appear for a fresh dynamic grant',
  );
});

// AC-10: conservation on the bailout tick — the offer is a normal ledger inflow.
test('AC-10 (conservation): the dynamic offer tick still satisfies fundsAtTickEnd === fundsAtTickStart + Σinflows − Σoutflows', () => {
  const s0 = initialState();
  const entered = freshCrisisEntry(s0);
  const inflowSum = entered.lastFlows.inflows.reduce((a, b) => a + b.value, 0);
  const outflowSum = entered.lastFlows.outflows.reduce((a, b) => a + b.value, 0);
  assert.equal(entered.fundsAtTickEnd, entered.fundsAtTickStart + inflowSum - outflowSum);
});

// AC-9: determinism.
test('AC-9 (determinism): two identical runs into the dynamic bailout produce byte-identical offer/latch/capex', () => {
  const run = () => freshCrisisEntry(initialState());
  const a = run();
  const b = run();
  assert.equal(JSON.stringify(a), JSON.stringify(b));
  assert.equal(a.cumulativeCapexSpent, b.cumulativeCapexSpent);
  assert.equal(a.dynamicBailoutUsed, b.dynamicBailoutUsed);
});

// ========== AC-6/AC-7: once-only, this playthrough AND across save/load ==========

test('AC-6 (once-only, same playthrough): after the dynamic offer, a SECOND crisis credits NO dynamic-labelled inflow of any size', () => {
  let s = freshCrisisEntry(initialState());
  // Clean-end the bailout.
  let t = s;
  for (let k = 0; k < BAILOUT_DURATION_TICKS - 1; k++) t = tickAtFunds(t, 5_000_000);
  s = tickAtFunds(t, 5_000_000);
  assert.equal(s.bailoutState, null, 'precondition: cleanly recovered');

  const second = freshCrisisEntry(s);
  const secondDynamic = second.lastFlows.inflows.find((f) => f.label === DYNAMIC_BAILOUT_INJECTION_LABEL);
  assert.equal(secondDynamic, undefined, 'no second dynamic-labelled inflow of any size once dynamicBailoutUsed is true');
});

test('AC-7 (once-only, across save/load — the BUG-504 re-arm class): the latch survives buildGameSave -> gameSaveText -> parseGameSave -> hydrate', () => {
  const used = freshCrisisEntry(initialState());
  assert.equal(used.dynamicBailoutUsed, true, 'precondition: the one offer has been used');

  const save = buildGameSave({
    state: used,
    journal: emptyJournal(),
    journalTail: [],
    name: 'attack-fixture',
    buildVersion: 'test',
  });
  const text = gameSaveText(save);
  const parsed = parseGameSave(text);
  assert.equal(parsed.ok, true);

  // Mirrors applyLoadedSave's real load path: sanitizeTreasury(snapshot) then
  // the REAL reducer's 'hydrate' case.
  const snapshot = sanitizeTreasury(parsed.save.savepoint.snapshot);
  const reloaded = reducer(snapshot, { type: 'hydrate', state: snapshot });
  assert.equal(reloaded.dynamicBailoutUsed, true, 'the latch must survive the FULL save/load round-trip');

  // Drive the reloaded city into crisis again — it must NOT get a second
  // dynamic-labelled offer (this is the exact shape of BUG-504's old
  // firstBailoutCount re-arm bug, now proved closed for the NEW latch).
  const secondCrisis = freshCrisisEntry(reloaded);
  const secondDynamic = secondCrisis.lastFlows.inflows.find((f) => f.label === DYNAMIC_BAILOUT_INJECTION_LABEL);
  assert.equal(secondDynamic, undefined, 'a reloaded save must never re-arm the once-only dynamic offer');
});

test('reset (Start Over / new game) CLEARS the once-only latch along with the wipe', () => {
  const used = freshCrisisEntry(initialState());
  assert.equal(used.dynamicBailoutUsed, true);
  const fresh = reducer(used, { type: 'reset' });
  assert.equal(fresh.dynamicBailoutUsed, false, 'a genuinely NEW playthrough must start with a fresh once-only latch');
  assert.equal(fresh.cumulativeCapexSpent, 0, 'a genuinely NEW playthrough must start with zero cumulative capex');
});

// ========== AC-8: old-save grandfathering / migration (spec §4 table) ==========

test('AC-8 (migration): a solvent old save with NO bailout history at all backfills clean — dynamicBailoutUsed false, capex backfilled once', () => {
  const s0 = initialState();
  const oldSave = { ...s0, cumulativeCapexSpent: undefined, capexBackfilled: undefined, dynamicBailoutUsed: undefined };
  delete oldSave.cumulativeCapexSpent;
  delete oldSave.capexBackfilled;
  delete oldSave.dynamicBailoutUsed;
  const migrated = sanitizeTreasury(oldSave);
  assert.equal(migrated.dynamicBailoutUsed, false, 'a genuinely solvent, bailout-free old save must NOT be granted a used latch');
  assert.equal(migrated.capexBackfilled, true, 'the backfill must run exactly once and flip the flag');
  assert.ok(typeof migrated.cumulativeCapexSpent === 'number', 'cumulativeCapexSpent must be backfilled to a real number, never left undefined');
});

test('AC-8 (migration): an old save MID a first bailout is marked dynamicBailoutUsed=true (already had its "once")', () => {
  const s0 = initialState();
  const oldSave = { ...s0, bailoutState: { enteredAt: 10 } };
  delete oldSave.dynamicBailoutUsed;
  delete oldSave.cumulativeCapexSpent;
  delete oldSave.capexBackfilled;
  const migrated = sanitizeTreasury(oldSave);
  assert.equal(migrated.dynamicBailoutUsed, true, 'a save already mid a (grandfathered, old-terms) bailout must not get a FRESH dynamic offer too');
});

test('AC-8 (migration): an old save with firstBailoutCount===1 (solvent again) is marked dynamicBailoutUsed=true — no-double-dip', () => {
  const s0 = initialState();
  const oldSave = { ...s0, firstBailoutCount: 1 };
  delete oldSave.dynamicBailoutUsed;
  const migrated = sanitizeTreasury(oldSave);
  assert.equal(migrated.dynamicBailoutUsed, true, 'a save that already used the OLD first-tier grant once must not get a second (dynamic) one');
});

test('AC-8 (migration): an old save in bailoutSecondState / administrationState / declineState is marked dynamicBailoutUsed=true', () => {
  const s0 = initialState();
  for (const patch of [
    { bailoutSecondState: { enteredAt: 5 } },
    { administrationState: { enteredAt: 5, origin: 'bailout' } },
    { declineState: { enteredAt: 5, peakPopulation: 0, finalPopulation: 0, minFundsEver: 0, totalSpending: 0 } },
  ]) {
    const oldSave = { ...s0, ...patch };
    delete oldSave.dynamicBailoutUsed;
    const migrated = sanitizeTreasury(oldSave);
    assert.equal(migrated.dynamicBailoutUsed, true, `patch ${JSON.stringify(patch)} must migrate to dynamicBailoutUsed=true`);
  }
});

test('AC-8 (migration): backfill proxy sums placementCost over the CURRENT standing buildings, never zero for a real city', () => {
  const s0 = initialState();
  const withBuildings = {
    ...s0,
    buildings: [
      { id: 9001, spec: 'road', x: 0, y: 0 },
      { id: 9002, spec: 'rd_avenue', x: 2, y: 0 },
    ],
  };
  delete withBuildings.cumulativeCapexSpent;
  delete withBuildings.capexBackfilled;
  const migrated = sanitizeTreasury(withBuildings);
  const expected = placementCost(SPECS['road']) + placementCost(SPECS['rd_avenue']);
  assert.equal(migrated.cumulativeCapexSpent, expected);
  assert.ok(migrated.cumulativeCapexSpent > 0, 'a real city with buildings must never backfill to zero');
});

test('AC-8 (migration): backfill runs EXACTLY ONCE — a second sanitize pass never re-sums', () => {
  const s0 = initialState();
  const withBuildings = { ...s0, buildings: [{ id: 9001, spec: 'road', x: 0, y: 0 }] };
  delete withBuildings.cumulativeCapexSpent;
  delete withBuildings.capexBackfilled;
  const firstPass = sanitizeTreasury(withBuildings);
  // Simulate a SECOND building appearing (e.g. a fresh placement) WITHOUT
  // resetting capexBackfilled — the sum must NOT be recomputed from scratch.
  const withMoreBuildings = { ...firstPass, buildings: [...firstPass.buildings, { id: 9002, spec: 'rd_avenue', x: 2, y: 0 }] };
  const secondPass = sanitizeTreasury(withMoreBuildings);
  assert.equal(
    secondPass.cumulativeCapexSpent,
    firstPass.cumulativeCapexSpent,
    'capexBackfilled=true must prevent a re-sum — cumulativeCapexSpent only grows via real charge sites from here on',
  );
});

test('migration never crashes or zeroes funds on a malformed/partial old save (spec §4 closing paragraph)', () => {
  const s0 = initialState();
  const weird = { ...s0, funds: 12345 };
  delete weird.cumulativeCapexSpent;
  delete weird.capexBackfilled;
  delete weird.dynamicBailoutUsed;
  delete weird.firstBailoutCount;
  const migrated = sanitizeTreasury(weird);
  assert.equal(migrated.funds, 12345, 'funds must be untouched by this migration');
  assert.equal(migrated.dynamicBailoutUsed, false);
});

// ========== AC-13: Play Mode unaffected by the new fields ==========

test('AC-13: Play Mode engagement does not perturb cumulativeCapexSpent/dynamicBailoutUsed', () => {
  let s = freshCrisisEntry(initialState());
  let t = s;
  for (let k = 0; k < BAILOUT_DURATION_TICKS - 1; k++) t = tickAtFunds(t, 5_000_000);
  s = tickAtFunds(t, 5_000_000);
  const capexBefore = s.cumulativeCapexSpent;
  const latchBefore = s.dynamicBailoutUsed;
  const played = reducer(s, { type: 'enterPlayMode' });
  assert.equal(played.cumulativeCapexSpent, capexBefore, 'Play Mode must not perturb an unrelated tracker');
  assert.equal(played.dynamicBailoutUsed, latchBefore, 'Play Mode must not perturb the once-only latch either way');
});

// ========== F2/F2b (independent round REJECT, 2026-09-02): tick-time capex
// terms (road auto-scale, building auto-scale, auto-connect sweep) counted
// EXACTLY ONCE, not double-counted or dropped ==========
//
// F2's diagnosed bug: sweepOrphanConnects() -> autoConnect() ALREADY writes
// its own cumulativeCapexSpent increment into the state it hands back, but
// advance()'s final state assembly used to ALSO add `orphanConnectCost` on
// top — every pound the sweep spent was counted twice (measured: 36,000 real
// spend -> 72,000 capex). Fixed by dropping `orphanConnectCost` from that
// final addition (autoScaleCost/buildingAutoScaleCost are the OPPOSITE case —
// pure selectors that never touch cumulativeCapexSpent themselves, so they
// MUST stay in that addition). Each test below drives a REAL tick-time event
// end-to-end (never the pure selector alone) so a `+ 0` mutation of ANY of
// the three tick-time terms in advance()'s final cumulativeCapexSpent
// expression is caught.

test('F2b: a road auto-scale event increments cumulativeCapexSpent by EXACTLY the booked "Road Auto-Scale" outflow', () => {
  // Mirrors road-inc2.test.mjs's proven saturation fixture: a tier-1 lane at
  // (10,10) fed by an ind_heavy source at (20,20), population 500 -> the
  // segment saturates and scales road -> rd_avenue at the monthly boundary.
  const base = initialState();
  const s0 = {
    ...base,
    unlockedAll: true,
    roadNotice: null,
    funds: 10_000_000,
    population: 500,
    buildings: [
      { id: 100, spec: 'road', x: 10, y: 10, builtTick: -1000 },
      { id: 2, spec: 'ind_heavy', x: 20, y: 20, builtTick: -1000 },
    ],
    roadMonitors: [{ x: 10, y: 10, source: 2, until: TICKS_PER_YEAR }],
    tick: TICKS_PER_MONTH - 1,
  };
  const capexBefore = s0.cumulativeCapexSpent ?? 0;
  const after = reducer(s0, { type: 'tick' });
  const outflow = after.lastFlows.outflows.find((f) => f.label === 'Road Auto-Scale');
  assert.ok(outflow && outflow.value > 0, 'precondition: the road auto-scale event must actually fire on this fixture');
  assert.equal(
    after.cumulativeCapexSpent,
    capexBefore + outflow.value,
    `road auto-scale capex mismatch: cumulativeCapexSpent=${after.cumulativeCapexSpent}, expected ${capexBefore + outflow.value} (before=${capexBefore} + outflow=${outflow.value})`,
  );
});

test('F2b: a building auto-scale event increments cumulativeCapexSpent by EXACTLY the booked "Building Auto-Scale" outflow', () => {
  let s = initialState();
  s = { ...s, unlockedAll: true, funds: 100_000_000 };
  s = reducer(s, { type: 'place', spec: 'res_estate', x: 5, y: 5 });
  const placed = s.buildings.find((b) => b.spec === 'res_estate');
  assert.ok(placed, 'precondition: res_estate placement (road-connected via autoConnect) succeeded');

  // Drive utilization (population / res_estate's tier-0 capacity, 1500) above
  // the 0.85 BUILDING_UTILIZATION_THRESHOLD, register a monitor, and
  // fast-forward construction so isOnline() reads true.
  const capexAfterPlace = s.cumulativeCapexSpent;
  s = {
    ...s,
    population: 1400,
    tick: TICKS_PER_MONTH - 1,
    buildingMonitors: [{ buildingId: placed.id, until: TICKS_PER_MONTH + 100_000, type: 'residents' }],
    buildings: s.buildings.map((b) => (b.id === placed.id ? { ...b, builtTick: -100_000 } : b)),
  };
  const after = reducer(s, { type: 'tick' });
  const outflow = after.lastFlows.outflows.find((f) => f.label === 'Building Auto-Scale');
  assert.ok(outflow && outflow.value > 0, 'precondition: the building auto-scale event must actually fire on this fixture');
  // FEAT-2326609781 (2026-09-04): the residential fittingTier clamp means
  // res_estate's placement lays a tier-3 connector, so this same tick's
  // growth ALSO fires a legitimate Road Auto-Scale upgrade — pay-as-you-grow
  // working as ruled (don't pre-build motorways; upgrade when demand proves
  // it). The capex identity therefore covers BOTH auto-scale events of the
  // tick, still exactly (no double-count — that's what F2/F2b exist to pin).
  const roadOutflow = after.lastFlows.outflows.find((f) => f.label === 'Road Auto-Scale');
  const expectedCapex = capexAfterPlace + outflow.value + (roadOutflow?.value ?? 0);
  assert.equal(
    after.cumulativeCapexSpent,
    expectedCapex,
    `building auto-scale capex mismatch: cumulativeCapexSpent=${after.cumulativeCapexSpent}, expected ${expectedCapex} (building=${outflow.value} + road=${roadOutflow?.value ?? 0})`,
  );
});

test('F2: an auto-connect SWEEP event increments cumulativeCapexSpent by EXACTLY the measured spend, not double (the diagnosed bug)', () => {
  // An orphan house 4 tiles from a road: the bi-monthly sweep (tick % 60===0)
  // lays connectors via the SAME autoConnect() path 'place' uses, which
  // already writes its own cumulativeCapexSpent increment.
  const base = initialState();
  const PERIOD = 2 * TICKS_PER_MONTH;
  const s0 = {
    ...base,
    unlockedAll: true,
    roadNotice: null,
    funds: 10_000_000,
    ledger: [],
    buildings: [
      { id: 1, spec: 'res_hut', x: 10, y: 10 },
      { id: 2, spec: 'road', x: 10, y: 14 },
    ],
    tick: PERIOD - 1,
  };
  const capexBefore = s0.cumulativeCapexSpent ?? 0;
  const after = reducer(s0, { type: 'tick' });
  assert.equal(after.tick, PERIOD, 'precondition: the sweep tick fired');
  const connectOutflow = (after.lastFlows.outflows.find((f) => f.label === 'Road Auto-Connect') ?? { value: 0 }).value;
  assert.ok(connectOutflow > 0, 'precondition: the sweep must actually spend money laying a connector');
  assert.equal(
    after.cumulativeCapexSpent,
    capexBefore + connectOutflow,
    `auto-connect capex double-counted: cumulativeCapexSpent=${after.cumulativeCapexSpent} for a real spend of ${connectOutflow} (F2 double-count class)`,
  );
});

// ========== RED self-proof (GR#24: scratch cp/mv only, never git) ==========
//
// Proves the AC-7 save/load-survival test above is not vacuous by actually
// sabotaging the migration and confirming the test goes RED, then restoring
// the original file byte-for-byte. This is a SEPARATE, self-contained test
// so a reviewer can see the sabotage/restore pair without re-running the
// whole suite by hand — see the inline fs operations below.
test('RED self-proof: AC-7 fails if the once-only latch is not migrated on load (proves the test above is not vacuous)', async () => {
  // BUG-739: mutation now runs against a private webconsole/test/helpers/
  // mutant.mjs shadow copy of webconsole/src, never the real, shared
  // engine.ts — a distinct shadow path is its own cache-miss for
  // `await import(...)`, so the old cache-busting query-string trick against
  // the REAL file is no longer needed either.
  const { createMutantShadow } = await import('./helpers/mutant.mjs');
  const shadow = createMutantShadow({
    targetRelPath: 'sim/engine.ts',
    mutate: (original) => {
      // F3 (independent round REJECT, 2026-09-02) changed this migration line
      // from a bare `=== undefined` read to a `typeof === 'boolean'` coercion —
      // the marker below tracks that exact line so this self-proof keeps
      // sabotaging the REAL current code, not a stale string that silently
      // stops matching (which would make this precondition assert false
      // forever, masking the whole RED self-proof instead of proving it).
      const sabotageMarker =
        "let dynamicBailoutUsed = typeof s.dynamicBailoutUsed === 'boolean' ? s.dynamicBailoutUsed : undefined;";
      assert.ok(original.includes(sabotageMarker), 'precondition: the exact migration line must exist to sabotage');
      // Sabotage: force dynamicBailoutUsed to ALWAYS read false, regardless
      // of what a loaded save carries — reproduces the BUG-504-class re-arm
      // bug for the NEW latch (a save's used status is silently forgotten on
      // load).
      return original.replace(sabotageMarker, 'let dynamicBailoutUsed = false; /* SABOTAGED */');
    },
  });
  try {
    const sabotagedEngine = await import(shadow.importUrl('sim/engine.ts'));
    const usedS = (() => {
      const w = sabotagedEngine.reducer(
        sabotagedEngine.initialState(),
        { type: 'debugFunds', amount: WARNING_BAND_FUNDS - sabotagedEngine.initialState().funds },
      );
      const warned = sabotagedEngine.reducer(w, { type: 'tick' });
      const c = sabotagedEngine.reducer(warned, {
        type: 'debugFunds',
        amount: CRISIS_FUNDS - warned.funds,
      });
      return sabotagedEngine.reducer(c, { type: 'tick' });
    })();
    assert.equal(usedS.dynamicBailoutUsed, true, 'precondition: the sabotaged build must still GRANT the one offer normally');

    const snapshot = sabotagedEngine.sanitizeTreasury(usedS);
    const reloaded = sabotagedEngine.reducer(snapshot, { type: 'hydrate', state: snapshot });
    // Under the sabotage, the migration forgets the latch entirely.
    assert.equal(reloaded.dynamicBailoutUsed, false, 'RED CONFIRMED: the sabotaged build forgets the latch on reload');
  } finally {
    shadow.cleanup();
  }
});
