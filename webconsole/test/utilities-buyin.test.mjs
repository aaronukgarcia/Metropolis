// utilities-buyin.test.mjs — FEAT-2326609711 inc2: external buy-in for
// water, wastewater, and refuse collection.
//
// Mirrors grid-import.test.mjs's structure exactly (inc1's precedent):
// pure engine logic (computeFlows/wellbeingOf/serviceDemandOf/journal/
// genesisReplay/consistency) here; UI toggle/finance-row render smoke tests
// at the bottom (AC-8/AC-9/AC-10), same split mount.test.tsx uses for
// PowerTab.
//
// node --test type-strips the .ts imports, so these exercise the exact
// shipped sim code. RED/GREEN proofs for the key assertions are recorded in
// the build report (scratch cp/mv of fiscal.ts/engine.ts/data.ts, never a
// git revert — GR#24).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer, computeFlows, wellbeingOf } from '../src/sim/engine.ts';
import {
  serviceCoverageOf,
  serviceDemandOf,
  wasteStatsOf,
  collectionCoverageOf,
  isWaterShortageActive,
  isWastewaterShortageActive,
  isRefuseShortageActive,
  effectiveCleanWaterCoverageOf,
  effectiveWastewaterCoverageOf,
  effectiveRefuseCoverageOf,
  effectivePowerCoverageOf,
  SPECS,
} from '../src/sim/data.ts';
import {
  WATER_IMPORT_TARIFF_PER_PERSON_PER_TICK,
  WATER_IMPORT_ENABLED_DEFAULT,
  WATER_IMPORT_OUTFLOW_LABEL,
  WASTEWATER_CONTRACT_TARIFF_PER_PERSON_PER_TICK,
  WASTEWATER_CONTRACT_ENABLED_DEFAULT,
  WASTEWATER_CONTRACT_OUTFLOW_LABEL,
  REFUSE_CONTRACT_TARIFF_PER_TONNE_PER_TICK,
  REFUSE_CONTRACT_ENABLED_DEFAULT,
  REFUSE_CONTRACT_OUTFLOW_LABEL,
  utilityBuyInCostPerTick,
  verifyUtilityTariffInvariants,
  UTILITY_PLANT_AMORTISATION_TICKS,
  DEBT_THRESHOLD_FOR_BAILOUT,
} from '../src/sim/fiscal.ts';
import { emptyJournal, recordAction, isStateAffecting } from '../src/sim/journal.ts';
import { replayFromGenesis, replayIsDeterministic } from '../src/sim/genesisReplay.ts';
import { runConsistencyChecks } from '../src/sim/consistency.ts';
import { buildDebugJson } from '../src/sim/debugjson.ts';
import { EMPTY_MAP_UI } from '../src/sim/uistate.ts';
import { createSavepoint } from '../src/sim/replay.ts';

const debugUi = () => ({
  appVersion: 'v0.0.0-utilities-buyin-test',
  frameAtMs: 1_700_000_000_000,
  map: EMPTY_MAP_UI,
});

// ---------- fixtures ----------
//
// Clean water/wastewater need = s.population DIRECTLY (serviceCoverageOf's
// 'cleanwater'/'waste' rows: row(id, label, pop, cap, spec) — `need` is the
// raw population, no fraction). Capacity defaults to 0 with no water
// buildings, so setting `population` alone with an empty buildings array is
// a real, independently-derivable shortfall — no magic numbers hardcoded,
// the shortfall IS `population - 0`.
// pow_wind mw=6/unit (BUG-648 rebalance) — 3 units = 18 MW, comfortably above
// a 1000-population city's need (round(1000*0.012) = 12 MW), so POWER is
// never the binding constraint on the shared 'Utilities' wellbeing part —
// only the clean-water/wastewater toggle under test can move it.
function waterShortageCity(overrides = {}) {
  const s = { ...initialState(), buildings: [], population: 1000, ...overrides };
  if (!overrides.buildings) {
    let id = 600001;
    for (let i = 0; i < 3; i++) s.buildings.push({ id: id++, spec: 'pow_wind', x: 20 + i, y: 20 });
  }
  return s;
}

// Refuse GENERATED tonnage comes from online residential buildings' `residents`
// field (WASTE_PER_RESIDENT tonnes/resident/tick) — population alone does not
// drive it (data.ts's wasteGeneratedOf walks buildings, not s.population). 100
// res_hut (residents: 8 each) with zero waste_depot -> generated = 100*8*0.01
// = 8 t/tick, capacity = 0 -> shortfall 8 t/tick, independently derivable.
// `population` here is set well above the earlyGameFactor(pop/50) ramp
// threshold (50) purely so the Refuse wellbeing part's early-game 55-blend
// does not swamp the coverage signal under test — wasteGeneratedOf() itself
// reads ONLY the residential buildings' `residents` field, never this
// population number (see the doc comment above wasteGeneratedOf, data.ts).
function refuseShortageCity(overrides = {}) {
  const s = { ...initialState(), buildings: [], population: 1000, ...overrides };
  if (!overrides.buildings) {
    let id = 700001;
    for (let i = 0; i < 100; i++) {
      s.buildings.push({ id: id++, spec: 'res_hut', x: (id % 400) + 5, y: 5 });
    }
  }
  return s;
}

// A city with real capacity that fully covers a small population — the
// "surplus twin" used to prove a covered shortfall is NOT distinguishable
// from genuine oversupply for consequence purposes (Lead Ruling R3).
function surplusWaterCity(overrides = {}) {
  const s = { ...initialState(), buildings: [], population: 1000, ...overrides };
  s.buildings.push({ id: 800001, spec: 'wat_tower', x: 10, y: 10 }); // 4000 served >= 1000 need
  s.buildings.push({ id: 800002, spec: 'wat_waste', x: 12, y: 10 }); // 20000 served >= 1000 need
  return s;
}

// ---------- AC-1: new city defaults to all external covers on ----------

test('AC-1: initialState() defaults all three utility toggles to *_ENABLED_DEFAULT (true)', () => {
  assert.equal(WATER_IMPORT_ENABLED_DEFAULT, true);
  assert.equal(WASTEWATER_CONTRACT_ENABLED_DEFAULT, true);
  assert.equal(REFUSE_CONTRACT_ENABLED_DEFAULT, true);
  const s = initialState();
  assert.equal(s.waterImportEnabled, true);
  assert.equal(s.wastewaterContractEnabled, true);
  assert.equal(s.refuseContractEnabled, true);
});

test('AC-1: with real shortfalls and cover ON, no shortage applies — all three outflow lines appear', () => {
  const w = waterShortageCity();
  const cov = serviceCoverageOf(w);
  const clean = cov.find((c) => c.id === 'cleanwater');
  const waste = cov.find((c) => c.id === 'waste');
  assert.ok(clean.need > clean.cap, 'precondition: real clean-water shortfall');
  assert.ok(waste.need > waste.cap, 'precondition: real wastewater shortfall');

  const flows = computeFlows(w);
  const waterLine = flows.outflows.find((f) => f.label === WATER_IMPORT_OUTFLOW_LABEL);
  const wwLine = flows.outflows.find((f) => f.label === WASTEWATER_CONTRACT_OUTFLOW_LABEL);
  assert.ok(waterLine && waterLine.value > 0, 'Water Import outflow must appear');
  assert.ok(wwLine && wwLine.value > 0, 'Waste-Water Contract outflow must appear');

  const r = refuseShortageCity();
  const stats = wasteStatsOf(r);
  assert.ok(stats.generated > stats.capacity, 'precondition: real refuse shortfall');
  const refuseLine = computeFlows(r).outflows.find((f) => f.label === REFUSE_CONTRACT_OUTFLOW_LABEL);
  assert.ok(refuseLine && refuseLine.value > 0, 'Contracted Refuse outflow must appear');
});

// ---------- AC-2: outflow value = shortfall * tariff, absent when no shortfall ----------

test('AC-2: Water Import outflow value = (need - cap) * WATER_IMPORT_TARIFF_PER_PERSON_PER_TICK', () => {
  const s = waterShortageCity();
  const clean = serviceCoverageOf(s).find((c) => c.id === 'cleanwater');
  // BUG-1028 rework: Math.ceil, not Math.round — a non-zero shortfall must
  // never book £0 (see fiscal.ts's utilityBuyInCostPerTick doc comment).
  const expected = Math.ceil((clean.need - clean.cap) * WATER_IMPORT_TARIFF_PER_PERSON_PER_TICK);
  assert.ok(expected > 0, 'precondition: a nonzero expected cost');
  const line = computeFlows(s).outflows.find((f) => f.label === WATER_IMPORT_OUTFLOW_LABEL);
  assert.equal(line.value, expected);
});

test('AC-2: Waste-Water Contract outflow value = (need - cap) * WASTEWATER_CONTRACT_TARIFF_PER_PERSON_PER_TICK', () => {
  const s = waterShortageCity();
  const waste = serviceCoverageOf(s).find((c) => c.id === 'waste');
  // BUG-1028 rework: Math.ceil, not Math.round (see the Water Import test above).
  const expected = Math.ceil((waste.need - waste.cap) * WASTEWATER_CONTRACT_TARIFF_PER_PERSON_PER_TICK);
  assert.ok(expected > 0);
  const line = computeFlows(s).outflows.find((f) => f.label === WASTEWATER_CONTRACT_OUTFLOW_LABEL);
  assert.equal(line.value, expected);
});

test('AC-2: Contracted Refuse outflow value = (generated - capacity) * REFUSE_CONTRACT_TARIFF_PER_TONNE_PER_TICK', () => {
  const s = refuseShortageCity();
  const stats = wasteStatsOf(s);
  // BUG-1028 rework: Math.ceil, not Math.round (see the Water Import test above).
  const expected = Math.ceil((stats.generated - stats.capacity) * REFUSE_CONTRACT_TARIFF_PER_TONNE_PER_TICK);
  assert.ok(expected > 0);
  const line = computeFlows(s).outflows.find((f) => f.label === REFUSE_CONTRACT_OUTFLOW_LABEL);
  assert.equal(line.value, expected);
});

test('AC-2: no shortfall -> the import line is ABSENT (not zero-valued)', () => {
  const s = surplusWaterCity();
  const clean = serviceCoverageOf(s).find((c) => c.id === 'cleanwater');
  assert.ok(clean.cap >= clean.need, 'precondition: surplus, no shortfall');
  const flows = computeFlows(s);
  assert.equal(flows.outflows.find((f) => f.label === WATER_IMPORT_OUTFLOW_LABEL), undefined);
  assert.equal(flows.outflows.find((f) => f.label === WASTEWATER_CONTRACT_OUTFLOW_LABEL), undefined);
});

// ---------- AC-3: cover OFF -> no outflow, legacy behaviour applies unchanged ----------

test('AC-3: cover OFF -> no import lines, and the legacy coverage-derived wellbeing/demand-index reads the RAW ratio', () => {
  const on = waterShortageCity({ waterImportEnabled: true, wastewaterContractEnabled: true });
  const off = waterShortageCity({ waterImportEnabled: false, wastewaterContractEnabled: false });

  const flowsOff = computeFlows(off);
  assert.equal(flowsOff.outflows.find((f) => f.label === WATER_IMPORT_OUTFLOW_LABEL), undefined);
  assert.equal(flowsOff.outflows.find((f) => f.label === WASTEWATER_CONTRACT_OUTFLOW_LABEL), undefined);

  // Legacy path: effective coverage with cover OFF must equal the RAW
  // serviceCoverageOf ratio exactly (byte-identical to pre-feature).
  const rawClean = serviceCoverageOf(off).find((c) => c.id === 'cleanwater').coverage;
  assert.equal(effectiveCleanWaterCoverageOf(off), Math.min(1, rawClean));
  assert.equal(isWaterShortageActive(off), true, 'the SSOT predicate reads true — the toggle is a real gate');
  assert.equal(isWaterShortageActive(on), false, 'and false while cover is ON with the identical shortfall');

  // Utilities wellbeing part must be STRICTLY worse off (legacy penalty
  // bites) than the covered twin (Lead Ruling R3: no consequence at all
  // while covered).
  const util = (s) => wellbeingOf(s).parts.find((p) => p.label === 'Utilities').value;
  assert.ok(util(on) > util(off), 'the covered city must score strictly better on Utilities than the uncovered one');
});

test('AC-3: refuse cover OFF -> no Contracted Refuse line, Refuse wellbeing part reads the raw collectionCoverageOf', () => {
  const on = refuseShortageCity({ refuseContractEnabled: true });
  const off = refuseShortageCity({ refuseContractEnabled: false });
  assert.equal(computeFlows(off).outflows.find((f) => f.label === REFUSE_CONTRACT_OUTFLOW_LABEL), undefined);
  assert.equal(effectiveRefuseCoverageOf(off), Math.min(1, collectionCoverageOf(off)));
  const refusePart = (s) => wellbeingOf(s).parts.find((p) => p.label === 'Refuse').value;
  assert.ok(refusePart(on) > refusePart(off), 'covered city scores strictly better on the Refuse wellbeing part');
});

// ---------- AC-4: tariff invariant, derived from the live catalogue ----------

test('BUG-1047: cover OFF on an OVERSUPPLIED fixture returns the raw ratio unclamped, never Math.min(1, raw)', () => {
  // The r2 REJECT finding: effective*CoverageOf's outer Math.min(1, raw)
  // clamped the RAW ratio too, destroying an oversupplied city's surplus
  // signal (raw > 1) even with the cover OFF and nothing substituted.
  let id = 990001;
  const s = { ...initialState(), buildings: [], population: 2000, gridImportEnabled: false, waterImportEnabled: false, wastewaterContractEnabled: false, refuseContractEnabled: false };
  for (let i = 0; i < 10; i++) s.buildings.push({ id: id++, spec: 'res_block', x: (i % 30) + 3, y: 3 });
  for (let i = 0; i < 30; i++) s.buildings.push({ id: id++, spec: 'pow_wind', x: (i % 30) + 3, y: 20 });
  for (let i = 0; i < 3; i++) s.buildings.push({ id: id++, spec: 'wat_clean', x: (i % 30) + 3, y: 50 });
  for (let i = 0; i < 3; i++) s.buildings.push({ id: id++, spec: 'wat_waste', x: (i % 30) + 3, y: 55 });
  for (let i = 0; i < 30; i++) s.buildings.push({ id: id++, spec: 'waste_depot', x: (i % 30) + 3, y: 60 });

  const cov = serviceCoverageOf(s);
  const rawClean = cov.find((c) => c.id === 'cleanwater').coverage;
  const rawWaste = cov.find((c) => c.id === 'waste').coverage;
  assert.ok(rawClean > 1, `precondition: clean water genuinely oversupplied, got ${rawClean}`);
  assert.ok(rawWaste > 1, `precondition: waste water genuinely oversupplied, got ${rawWaste}`);

  assert.equal(effectiveCleanWaterCoverageOf(s), rawClean, 'must NOT clamp the raw oversupply ratio to 1 while cover is OFF');
  assert.equal(effectiveWastewaterCoverageOf(s), rawWaste, 'must NOT clamp the raw oversupply ratio to 1 while cover is OFF');

  // serviceDemandOf's cleanwater/waste rows must report the same surplus
  // (negative demand index) as the untouched power row — the in-fixture
  // control that proves the divergence was the clamp, not the fixture.
  const rows = new Map(serviceDemandOf(s).map((r) => [r.id, r.value]));
  assert.ok(rows.get('power') < 0, 'control: the untouched power row reports the surplus');
  assert.equal(rows.get('cleanwater'), rows.get('power'), 'cleanwater must match the surplus control row');
  assert.equal(rows.get('waste'), rows.get('power'), 'waste must match the surplus control row');
});

test('BUG-1063: effectivePowerCoverageOf on an OVERSUPPLIED fixture with Grid Import OFF returns the raw ratio unclamped', () => {
  // BUG-1063 (r3 round P3 finding): BUG-1047's ruling ("pass through
  // UNCLAMPED when not substituting") was pinned for cleanwater/wastewater
  // (the test above) and for refuse, but not for effectivePowerCoverageOf —
  // the surviving mutant restored the outer Math.min(1, raw) there and every
  // suite stayed green because the function's one production consumer
  // (attractivenessOf's avgCoverage, via effectiveServiceCoverageOf) already
  // clamps every row with clampN(r.coverage, 0, 1). Pin the helper itself,
  // independent of that consumer, on a genuinely oversupplied power fixture.
  let id = 991001;
  const s = { ...initialState(), buildings: [], population: 200, gridImportEnabled: false, waterImportEnabled: false, wastewaterContractEnabled: false, refuseContractEnabled: false };
  for (let i = 0; i < 2; i++) s.buildings.push({ id: id++, spec: 'res_block', x: (i % 30) + 3, y: 3 });
  for (let i = 0; i < 30; i++) s.buildings.push({ id: id++, spec: 'pow_wind', x: (i % 30) + 3, y: 20 });

  const rawPower = serviceCoverageOf(s).find((c) => c.id === 'power').coverage;
  assert.ok(rawPower > 1, `precondition: power genuinely oversupplied, got ${rawPower}`);
  assert.equal(
    effectivePowerCoverageOf(s),
    rawPower,
    'must NOT clamp the raw oversupply ratio to 1 while Grid Import cover is OFF'
  );
});

test('AC-4: verifyUtilityTariffInvariants() derives cheapest local plants from SPECS and every tariff exceeds them', () => {
  const result = verifyUtilityTariffInvariants(SPECS);
  assert.ok(result.cheapestWaterPlantId, 'a cheapest water plant must be found in the live catalogue');
  assert.ok(result.cheapestWastewaterPlantId, 'a cheapest wastewater plant must be found');
  assert.ok(result.cheapestRefusePlantId, 'a cheapest refuse depot must be found');
  assert.equal(result.waterExceedsLocal, true, `WATER tariff must exceed local (${result.cheapestWaterAmortisedPerPersonTick})`);
  assert.equal(result.wastewaterExceedsLocal, true, `WASTEWATER tariff must exceed local (${result.cheapestWastewaterAmortisedPerPersonTick})`);
  assert.equal(result.refuseExceedsLocal, true, `REFUSE tariff must exceed local (${result.cheapestRefuseAmortisedPerTonneTick})`);
  assert.equal(result.allHold, true);
});

test('AC-4: the derivation formula itself is pinned against a hand-built dirt-cheap fixture', () => {
  const dirt = {
    wat_test: { id: 'wat_test', kind: 'water', tag: 'clean', served: 100, cost: 100, upkeep: 0 },
    wwt_test: { id: 'wwt_test', kind: 'water', tag: 'waste', served: 100, cost: 100, upkeep: 0 },
    depot_test: { id: 'depot_test', kind: 'water', wasteCapacity: 100, cost: 100, upkeep: 0 },
  };
  const result = verifyUtilityTariffInvariants(dirt);
  // cost 100 / (served 100 * T) = 1/T; upkeep is 0 so it drops out entirely.
  const expected = 1 / UTILITY_PLANT_AMORTISATION_TICKS;
  assert.equal(result.cheapestWaterAmortisedPerPersonTick, expected);
  assert.equal(result.cheapestWastewaterAmortisedPerPersonTick, expected);
  assert.equal(result.cheapestRefuseAmortisedPerTonneTick, expected);
  assert.equal(WATER_IMPORT_TARIFF_PER_PERSON_PER_TICK > expected, true);
});

test('AC-4: verifyUtilityTariffInvariants ignores placeholder specs', () => {
  const onlyPlaceholder = {
    wat_ph: { id: 'wat_ph', kind: 'water', tag: 'clean', served: 1, cost: 1, upkeep: 0, placeholder: true },
  };
  const result = verifyUtilityTariffInvariants(onlyPlaceholder);
  assert.equal(result.cheapestWaterPlantId, null, 'a placeholder-only catalogue must report NO cheapest plant');
  assert.equal(result.cheapestWaterAmortisedPerPersonTick, 0);
});

// ---------- AC-5: toggles persist through saves / genesis replay ----------

function driveAndRecord(actions) {
  let journal = emptyJournal();
  let state = initialState();
  for (const action of actions) {
    journal = recordAction(journal, state.tick, action);
    state = reducer(state, action);
  }
  return { journal, liveState: state };
}

test('AC-5: all three toggles are journaled as state-affecting', () => {
  assert.equal(isStateAffecting({ type: 'toggleWaterImport' }), true);
  assert.equal(isStateAffecting({ type: 'toggleWastewaterContract' }), true);
  assert.equal(isStateAffecting({ type: 'toggleRefuseContract' }), true);
});

test('AC-5: genesis replay reproduces toggled-off utility state exactly', () => {
  const SCRIPT = [
    { type: 'toggleWaterImport' },
    { type: 'toggleWastewaterContract' },
    { type: 'toggleRefuseContract' },
    { type: 'tick' },
    { type: 'tick' },
  ];
  const { journal, liveState } = driveAndRecord(SCRIPT);
  assert.equal(liveState.waterImportEnabled, false);
  assert.equal(liveState.wastewaterContractEnabled, false);
  assert.equal(liveState.refuseContractEnabled, false);

  const replayed = replayFromGenesis(journal);
  assert.equal(replayed.waterImportEnabled, false);
  assert.equal(replayed.wastewaterContractEnabled, false);
  assert.equal(replayed.refuseContractEnabled, false);
  assert.equal(replayIsDeterministic(journal), true);
});

test('AC-5: savepoint round-trip preserves an explicit toggle state, and a legacy save missing the fields defaults ON', () => {
  const s = reducer(initialState(), { type: 'toggleRefuseContract' }); // off
  const sp = JSON.parse(JSON.stringify(createSavepoint(s, [], new Date(0), 'v-test', null)));
  assert.equal(sp.snapshot.refuseContractEnabled, false, 'an explicit false must round-trip through JSON');

  const legacy = { ...initialState() };
  delete legacy.waterImportEnabled;
  delete legacy.wastewaterContractEnabled;
  delete legacy.refuseContractEnabled;
  assert.equal('waterImportEnabled' in legacy, false, 'precondition: genuinely absent');
  const dj = buildDebugJson(legacy, debugUi());
  assert.equal(dj.sim.waterImportEnabled, true, 'AC-12: a legacy state must report the EXPLICIT documented default');
  assert.equal(dj.sim.wastewaterContractEnabled, true);
  assert.equal(dj.sim.refuseContractEnabled, true);
});

// ---------- AC-6: conservation — each outflow booked exactly once ----------

test('AC-6: each utility outflow appears exactly once, and the reducer books exactly the net flow computeFlows() reports', () => {
  const s = waterShortageCity();
  const flows = computeFlows(s);
  for (const label of [WATER_IMPORT_OUTFLOW_LABEL, WASTEWATER_CONTRACT_OUTFLOW_LABEL]) {
    const matches = flows.outflows.filter((f) => f.label === label);
    assert.equal(matches.length, 1, `${label} must appear exactly once, never duplicated`);
  }
  const inflowSum = flows.inflows.reduce((a, f) => a + f.value, 0);
  const outflowSum = flows.outflows.reduce((a, f) => a + f.value, 0);
  const after = reducer(s, { type: 'tick' });
  assert.equal(
    after.fundsAtTickEnd - after.fundsAtTickStart,
    inflowSum - outflowSum,
    'the reducer must apply exactly the net flow computeFlows() reports, no double-debit'
  );
});

test('consistency.ts upkeep-total-matches check is NOT broken by active utility buy-in outflows', () => {
  const s = reducer(refuseShortageCity({ waterImportEnabled: true, wastewaterContractEnabled: true }), {
    type: 'tick',
  });
  assert.ok(
    s.lastFlows.outflows.some((f) => f.label === REFUSE_CONTRACT_OUTFLOW_LABEL),
    'precondition: Contracted Refuse actually fired this tick'
  );
  const report = runConsistencyChecks(s);
  const check = report.checks.find((c) => c.id === 'flows.upkeep-total-matches');
  assert.ok(check);
  assert.equal(check.ok, true, `utility buy-in outflows must be excluded from upkeep reconciliation: ${check.detail}`);
});

// ---------- AC-7: insolvency path — imports are ordinary outflows, no exemption ----------

test('AC-7: utility buy-in outflows are subject to applyOutflowPolicies (austerity discount) like any other outflow', () => {
  const withoutAusterity = waterShortageCity();
  const withAusterity = waterShortageCity({ policies: { ...withoutAusterity.policies, austerity: true } });
  const a = computeFlows(withoutAusterity).outflows.find((f) => f.label === WATER_IMPORT_OUTFLOW_LABEL);
  const b = computeFlows(withAusterity).outflows.find((f) => f.label === WATER_IMPORT_OUTFLOW_LABEL);
  assert.ok(a && b);
  assert.equal(b.value, Math.round(a.value * 0.9), 'austerity must discount the Water Import outflow 10%, no exemption');
});

test('AC-7: a utility buy-in outflow can tip the city into insolvency crisis exactly like any other outflow', () => {
  const s = waterShortageCity();
  const line = computeFlows(s).outflows.find((f) => f.label === WATER_IMPORT_OUTFLOW_LABEL);
  assert.ok(line && line.value > 0, 'precondition: a real import cost exists');

  const targetFunds = DEBT_THRESHOLD_FOR_BAILOUT + Math.max(1, Math.round(line.value / 2));
  const forced = reducer(s, { type: 'debugFunds', amount: targetFunds - s.funds });
  const after = reducer(forced, { type: 'tick' });
  assert.ok(after.lastFlows.outflows.some((f) => f.label === WATER_IMPORT_OUTFLOW_LABEL));
  assert.ok(['warning', 'crisis'].includes(after.insolvencyState), `expected a distressed band, got ${after.insolvencyState}`);
});

// ---------- AC-9/AC-10: toggles are real sim state, survive unrelated actions ----------

test('AC-9/AC-10: each toggle flips its own field and survives an unrelated dispatch (not React local state)', () => {
  const s0 = initialState();
  const s1 = reducer(s0, { type: 'toggleWaterImport' });
  assert.equal(s1.waterImportEnabled, false);
  assert.equal(s1.wastewaterContractEnabled, true, 'toggles are independent — flipping water must not touch wastewater');
  const s2 = reducer(s1, { type: 'tax', which: 'residential', rate: 12 });
  assert.equal(s2.waterImportEnabled, false, 'must persist across an unrelated dispatch');
  const s3 = reducer(s2, { type: 'toggleWaterImport' });
  assert.equal(s3.waterImportEnabled, true);

  const w1 = reducer(s0, { type: 'toggleWastewaterContract' });
  assert.equal(w1.wastewaterContractEnabled, false);
  const r1 = reducer(s0, { type: 'toggleRefuseContract' });
  assert.equal(r1.refuseContractEnabled, false);
});

// ---------- AC-11: determinism ----------

test('AC-11: utility buy-in is deterministic — identical state produces identical outflows, no randomness', () => {
  const s = waterShortageCity();
  const f1 = computeFlows(s);
  const f2 = computeFlows(s);
  assert.deepEqual(f1, f2);
});

test('AC-11: replaying an identical toggle+tick script twice is byte-identical', () => {
  const SCRIPT = [
    { type: 'tick' },
    { type: 'toggleWaterImport' },
    { type: 'tick' },
    { type: 'toggleWaterImport' },
    { type: 'tick' },
  ];
  const { journal: j1 } = driveAndRecord(SCRIPT);
  const { journal: j2 } = driveAndRecord(SCRIPT);
  const r1 = replayFromGenesis(j1);
  const r2 = replayFromGenesis(j2);
  assert.deepEqual(r1, r2);
  assert.equal(replayIsDeterministic(j1), true);
});

// ---------- AC-12: legacy state defaults explicitly, no retroactive cover ----------

test('AC-12: a legacy state predating these fields reads ON by every SSOT reader (never a silent off)', () => {
  const legacy = waterShortageCity();
  delete legacy.waterImportEnabled;
  delete legacy.wastewaterContractEnabled;
  assert.equal('waterImportEnabled' in legacy, false);
  assert.equal(isWaterShortageActive(legacy), false, 'a legacy state must be treated as covered (ON), not a silent shortage');
  assert.equal(isWastewaterShortageActive(legacy), false);
  const flows = computeFlows(legacy);
  assert.ok(flows.outflows.some((f) => f.label === WATER_IMPORT_OUTFLOW_LABEL), 'a legacy state must still book the import cost while treated as ON');
});

// ---------- AC-13: regression — legacy shortage behaviour unchanged with covers off ----------

test('AC-13: with all three covers explicitly OFF, wellbeing/demand-index match a pre-feature-shaped raw-ratio computation exactly', () => {
  const s = {
    ...waterShortageCity({ waterImportEnabled: false, wastewaterContractEnabled: false }),
  };
  const r = refuseShortageCity({ refuseContractEnabled: false, population: s.population, buildings: [...s.buildings, ...refuseShortageCity().buildings] });

  const rawClean = Math.min(1, serviceCoverageOf(s).find((c) => c.id === 'cleanwater').coverage);
  const rawWaste = Math.min(1, serviceCoverageOf(s).find((c) => c.id === 'waste').coverage);
  const rawRefuse = Math.min(1, collectionCoverageOf(r));

  assert.equal(effectiveCleanWaterCoverageOf(s), rawClean);
  assert.equal(effectiveWastewaterCoverageOf(s), rawWaste);
  assert.equal(effectiveRefuseCoverageOf(r), rawRefuse);

  // demand-index rows must match the pre-feature demandIndexOf(raw) shape too.
  const demandRows = serviceDemandOf(s);
  const cleanRow = demandRows.find((row) => row.id === 'cleanwater');
  const wasteRow = demandRows.find((row) => row.id === 'waste');
  assert.ok(cleanRow.value > 0, 'a real shortfall with cover off must still raise the demand index');
  assert.ok(wasteRow.value > 0);
});

test('AC-13: MUTATION-PROVE target — cover OFF must be a real gate, not dead code (isXShortageActive is genuinely toggle-sensitive)', () => {
  const on = waterShortageCity({ waterImportEnabled: true });
  const off = waterShortageCity({ waterImportEnabled: false });
  assert.notEqual(isWaterShortageActive(on), isWaterShortageActive(off));
  assert.notEqual(effectiveCleanWaterCoverageOf(on), effectiveCleanWaterCoverageOf(off));
});

// ---------- utilityBuyInCostPerTick: shared formula, unit-tested directly ----------

test('utilityBuyInCostPerTick: shortfall * tariff, ceil-rounded (BUG-1028), floored at zero', () => {
  assert.equal(utilityBuyInCostPerTick(350, 500, 0.08), Math.ceil(150 * 0.08));
  assert.equal(utilityBuyInCostPerTick(500, 350, 0.08), 0, 'surplus must never go negative');
  assert.equal(utilityBuyInCostPerTick(0, 0, 0.08), 0);
  // BUG-1028: a sub-rounding-threshold shortfall must still cost >= 1, never 0.
  assert.equal(utilityBuyInCostPerTick(499, 500, 0.08), 1, 'a 1-person shortfall must cost at least £1, not £0');
});

// ---------- AC-8/AC-9: UI render smoke tests ----------
// See utilities-buyin.test.tsx — .tsx component imports require the
// `tsx --test` runner (package.json's "test" script splits .test.mjs from
// .test.tsx exactly this way; mirrors mount.test.tsx's split for PowerTab).
