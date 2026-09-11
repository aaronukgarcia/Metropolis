// attack-feat711-inc2-round.test.mjs — independent Destructive round pins for
// FEAT-2326609711 inc2 (external buy-in: water / waste-water / refuse).
// Attacker: opus-round-feat711-inc2 (GR#23, attacker != author).
//
// These are the ROUND's own pins, deliberately independent of the author's
// test/utilities-buyin.test.mjs: every expected value below is re-derived here
// from the live catalogue / the state under test, never copied from the
// implementation's constants where a constant is the thing under test.
//
// TWO pins are FINDINGS, not regressions — they encode the ruling the feature
// claims to implement and are RED against the build under review (see the
// verdict note): the attract half-wire (BUG-1026) and the refuse "left on the
// street" UI half-wire (BUG-1027). They must go GREEN on rework.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  initialState,
  reducer,
  computeFlows,
  wellbeingOf,
  attractivenessOf,
  approvalOf,
} from '../src/sim/engine.ts';
import {
  serviceCoverageOf,
  wasteStatsOf,
  collectionCoverageOf,
  effectiveCleanWaterCoverageOf,
  effectiveWastewaterCoverageOf,
  effectiveRefuseCoverageOf,
  effectivePowerCoverageOf,
  effectiveServiceCoverageOf,
  serviceDemandOf,
  waterBalanceOf,
  isWaterShortageActive,
  isWastewaterShortageActive,
  isRefuseShortageActive,
  SPECS,
} from '../src/sim/data.ts';
import {
  WATER_IMPORT_TARIFF_PER_PERSON_PER_TICK,
  WASTEWATER_CONTRACT_TARIFF_PER_PERSON_PER_TICK,
  REFUSE_CONTRACT_TARIFF_PER_TONNE_PER_TICK,
  WATER_IMPORT_OUTFLOW_LABEL,
  WASTEWATER_CONTRACT_OUTFLOW_LABEL,
  REFUSE_CONTRACT_OUTFLOW_LABEL,
  UTILITY_PLANT_AMORTISATION_TICKS,
  verifyUtilityTariffInvariants,
  utilityBuyInCostPerTick,
  gridImportCostPerTick,
} from '../src/sim/fiscal.ts';
import { isStateAffecting, emptyJournal, recordAction } from '../src/sim/journal.ts';
import { replayFromGenesis } from '../src/sim/genesisReplay.ts';
import { runConsistencyChecks } from '../src/sim/consistency.ts';
import { wasteDisplayModel } from '../src/components/right/wasteModel.ts';

const BUYIN_LABELS = [
  WATER_IMPORT_OUTFLOW_LABEL,
  WASTEWATER_CONTRACT_OUTFLOW_LABEL,
  REFUSE_CONTRACT_OUTFLOW_LABEL,
];

// A city with a REAL clean-water + waste-water shortfall (no water plants at
// all) and surplus power, so only the utility toggles under test can move a
// coverage-derived reader.
function waterShortCity(overrides = {}) {
  const s = { ...initialState(), buildings: [], population: 1000, ...overrides };
  if (!overrides.buildings) {
    let id = 910001;
    for (let i = 0; i < 3; i++) s.buildings.push({ id: id++, spec: 'pow_wind', x: 20 + i, y: 20 });
  }
  return s;
}

// A city that GENERATES refuse (residents) with no depot -> real tonnage shortfall.
function refuseShortCity(overrides = {}) {
  const s = { ...initialState(), buildings: [], population: 1000, ...overrides };
  if (!overrides.buildings) {
    let id = 920001;
    for (let i = 0; i < 100; i++) s.buildings.push({ id: id++, spec: 'res_hut', x: (id % 300) + 5, y: 5 });
  }
  return s;
}

const lineOf = (s, label) => computeFlows(s).outflows.find((f) => f.label === label);

// ───────────────────────────────────────────────────────────────────────────
// FINDING PINS (RED against the build under review — see the verdict note).
// ───────────────────────────────────────────────────────────────────────────

test('FINDING BUG-1026: a bought-in water/waste-water shortfall must not still dock ATTRACTIVENESS', () => {
  // attractivenessOf's avgCoverage term averages EVERY serviceCoverageOf row
  // RAW — including 'cleanwater' and 'waste'. Aaron's ruling for this feature
  // is price-premium-only ("the bought-in service fully substitutes"), and the
  // builder's own Lead Ruling R3 says a covered shortfall has NO consequence
  // beyond the outflow line. Holding wellbeing FIXED (so the already-wired
  // wellbeing path cannot mask the gap), a covered city must be strictly more
  // attractive than the identical uncovered one.
  const on = waterShortCity({ waterImportEnabled: true, wastewaterContractEnabled: true });
  const off = waterShortCity({ waterImportEnabled: false, wastewaterContractEnabled: false });
  const FIXED_WELLBEING = 60; // identical for both: isolates the coverage term
  const aOn = attractivenessOf(on, FIXED_WELLBEING);
  const aOff = attractivenessOf(off, FIXED_WELLBEING);
  assert.ok(
    aOn > aOff,
    `covered city must be strictly more attractive than the uncovered twin (got on=${aOn}, off=${aOff}) — ` +
      'attractivenessOf still reads the RAW cleanwater/waste coverage rows'
  );
});

test('FINDING BUG-1027: with the refuse contract ON, the waste panel must not report refuse left on the street', () => {
  // The WasteTab RAG tile, its "(LEFT ON THE STREET)" tooltip and the
  // "refuse accumulates and drives the waste-health penalty" banner are all
  // keyed off wasteDisplayModel().hasUncollected, which is raw
  // wasteStatsOf().uncollected > 0 — untouched by this increment. With the
  // contract ON the waste-health penalty is in fact fully suppressed (the
  // Refuse wellbeing part reads 100), so the panel asserts a penalty that
  // does not exist. Same class as inc1's r1 BROWNOUT-banner REJECT.
  const on = refuseShortCity({ refuseContractEnabled: true });
  assert.ok(wasteStatsOf(on).uncollected > 0, 'precondition: a real tonnage shortfall');
  assert.equal(
    wellbeingOf(on).parts.find((p) => p.label === 'Refuse').value,
    100,
    'precondition: the contract really does suppress the waste-health penalty'
  );
  assert.equal(
    wasteDisplayModel(on).hasUncollected,
    false,
    'the panel must not flag uncollected refuse while the shortfall is contracted out'
  );
});

// ───────────────────────────────────────────────────────────────────────────
// REGRESSION ARMOUR (GREEN against the build under review).
// ───────────────────────────────────────────────────────────────────────────

test('tariff invariant re-derived INDEPENDENTLY from SPECS (cheapest local amortised, GR#15)', () => {
  const cheapest = (pred, unitOf) => {
    let best = Infinity;
    let id = null;
    for (const sp of Object.values(SPECS)) {
      if (sp.placeholder) continue;
      if (!pred(sp)) continue;
      const units = unitOf(sp);
      if (!(units > 0) || !(sp.cost > 0)) continue;
      const amortised = sp.cost / (units * UTILITY_PLANT_AMORTISATION_TICKS) + (sp.upkeep ?? 0) / units;
      if (amortised < best) {
        best = amortised;
        id = sp.id;
      }
    }
    return { best, id };
  };
  const water = cheapest((sp) => sp.kind === 'water' && sp.tag === 'clean', (sp) => sp.served);
  const ww = cheapest((sp) => sp.kind === 'water' && sp.tag === 'waste', (sp) => sp.served);
  const refuse = cheapest((sp) => sp.wasteCapacity > 0, (sp) => sp.wasteCapacity);

  assert.ok(water.id && ww.id && refuse.id, 'the live catalogue must contain one of each');
  assert.ok(
    WATER_IMPORT_TARIFF_PER_PERSON_PER_TICK > water.best,
    `water tariff ${WATER_IMPORT_TARIFF_PER_PERSON_PER_TICK} must exceed cheapest local ${water.best} (${water.id})`
  );
  assert.ok(
    WASTEWATER_CONTRACT_TARIFF_PER_PERSON_PER_TICK > ww.best,
    `wastewater tariff ${WASTEWATER_CONTRACT_TARIFF_PER_PERSON_PER_TICK} must exceed cheapest local ${ww.best} (${ww.id})`
  );
  assert.ok(
    REFUSE_CONTRACT_TARIFF_PER_TONNE_PER_TICK > refuse.best,
    `refuse tariff ${REFUSE_CONTRACT_TARIFF_PER_TONNE_PER_TICK} must exceed cheapest local ${refuse.best} (${refuse.id})`
  );

  // ... and the shipped helper must agree with this independent derivation,
  // plant ids included (a helper that picked the DEAREST local plant would
  // still report allHold=true, so the ids are the part that actually pins it).
  const shipped = verifyUtilityTariffInvariants(SPECS);
  assert.equal(shipped.cheapestWaterPlantId, water.id);
  assert.equal(shipped.cheapestWastewaterPlantId, ww.id);
  assert.equal(shipped.cheapestRefusePlantId, refuse.id);
  assert.equal(shipped.cheapestWaterAmortisedPerPersonTick, water.best);
  assert.equal(shipped.cheapestWastewaterAmortisedPerPersonTick, ww.best);
  assert.equal(shipped.cheapestRefuseAmortisedPerTonneTick, refuse.best);
  assert.equal(shipped.allHold, true);
});

test('each predicate is wired to its OWN toggle (a crossed flag must be visible)', () => {
  const base = waterShortCity();
  const r = refuseShortCity();
  // water reads ONLY waterImportEnabled
  assert.equal(isWaterShortageActive({ ...base, waterImportEnabled: false }), true);
  assert.equal(isWaterShortageActive({ ...base, waterImportEnabled: true, refuseContractEnabled: false, wastewaterContractEnabled: false }), false);
  // wastewater reads ONLY wastewaterContractEnabled
  assert.equal(isWastewaterShortageActive({ ...base, wastewaterContractEnabled: false }), true);
  assert.equal(isWastewaterShortageActive({ ...base, wastewaterContractEnabled: true, waterImportEnabled: false, refuseContractEnabled: false }), false);
  // refuse reads ONLY refuseContractEnabled
  assert.equal(isRefuseShortageActive({ ...r, refuseContractEnabled: false }), true);
  assert.equal(isRefuseShortageActive({ ...r, refuseContractEnabled: true, waterImportEnabled: false, wastewaterContractEnabled: false }), false);
  // and the effective-coverage twins likewise
  assert.equal(effectiveCleanWaterCoverageOf({ ...base, waterImportEnabled: false, refuseContractEnabled: true }), 0);
  assert.equal(effectiveWastewaterCoverageOf({ ...base, wastewaterContractEnabled: false, waterImportEnabled: true }), 0);
  assert.equal(effectiveRefuseCoverageOf({ ...r, refuseContractEnabled: false, waterImportEnabled: true }), 0);
});

test('effective coverage is NOT an unconditional 1: cover OFF returns the raw ratio exactly', () => {
  const off = waterShortCity({ waterImportEnabled: false, wastewaterContractEnabled: false });
  const rOff = refuseShortCity({ refuseContractEnabled: false });
  const rawClean = serviceCoverageOf(off).find((c) => c.id === 'cleanwater').coverage;
  const rawWaste = serviceCoverageOf(off).find((c) => c.id === 'waste').coverage;
  assert.equal(effectiveCleanWaterCoverageOf(off), Math.min(1, rawClean));
  assert.equal(effectiveWastewaterCoverageOf(off), Math.min(1, rawWaste));
  assert.equal(effectiveRefuseCoverageOf(rOff), Math.min(1, collectionCoverageOf(rOff)));
  assert.ok(rawClean < 1 && rawWaste < 1, 'precondition: the raw ratios really are short');
  // ... and a PARTIALLY covered city with cover OFF keeps its fractional ratio
  // (a mutant returning 1 whenever raw < 1 would be caught by the equalities
  // above; a mutant returning 0 would be caught here).
  const partial = waterShortCity({
    waterImportEnabled: false,
    population: 8000,
    buildings: [
      { id: 1, spec: 'pow_wind', x: 20, y: 20 },
      { id: 2, spec: 'pow_wind', x: 21, y: 20 },
      { id: 3, spec: 'wat_tower', x: 10, y: 10 },
    ],
  });
  const rawPartial = serviceCoverageOf(partial).find((c) => c.id === 'cleanwater').coverage;
  assert.ok(rawPartial > 0 && rawPartial < 1, `precondition: a genuinely PARTIAL coverage ratio, got ${rawPartial}`);
  assert.equal(effectiveCleanWaterCoverageOf(partial), rawPartial, 'cover OFF must pass the fractional ratio through untouched');
});

test('AC-6 exactly-once + integer + strictly-positive, and never pushed at zero shortfall', () => {
  const s = waterShortCity();
  const r = refuseShortCity();
  for (const [state, label] of [
    [s, WATER_IMPORT_OUTFLOW_LABEL],
    [s, WASTEWATER_CONTRACT_OUTFLOW_LABEL],
    [r, REFUSE_CONTRACT_OUTFLOW_LABEL],
  ]) {
    const hits = computeFlows(state).outflows.filter((f) => f.label === label);
    assert.equal(hits.length, 1, `${label} must be booked exactly once`);
    assert.ok(Number.isInteger(hits[0].value), `${label} must be integer GBP, got ${hits[0].value}`);
    assert.ok(hits[0].value > 0, `${label} must be strictly positive when present`);
  }
  // A city with genuine surplus must carry NONE of the three lines (AC-2's
  // "absent, not zero-valued" idiom) — and a shortfall of exactly zero is a
  // surplus, so the `> 0` gate is what this pins.
  const surplus = { ...waterShortCity(), buildings: [...waterShortCity().buildings,
    { id: 930001, spec: 'wat_tower', x: 10, y: 10 }, { id: 930002, spec: 'wat_waste', x: 12, y: 10 }] };
  const cw = serviceCoverageOf(surplus).find((c) => c.id === 'cleanwater');
  assert.ok(cw.cap >= cw.need, 'precondition: genuine surplus');
  for (const label of BUYIN_LABELS) {
    assert.equal(computeFlows(surplus).outflows.some((f) => f.label === label), false, `${label} must be ABSENT`);
  }
});

test('the exact three labels, and no other label collides with them', () => {
  assert.equal(WATER_IMPORT_OUTFLOW_LABEL, 'Water Import');
  assert.equal(WASTEWATER_CONTRACT_OUTFLOW_LABEL, 'Waste-Water Contract');
  assert.equal(REFUSE_CONTRACT_OUTFLOW_LABEL, 'Contracted Refuse');
  const s = reducer(waterShortCity(), { type: 'tick' });
  const labels = s.lastFlows.outflows.map((f) => f.label);
  assert.equal(new Set(labels).size, labels.length, 'no duplicate outflow labels in a live tick');
});

test('money: 200 ticks of a live city conserve exactly, with buy-in lines active throughout', () => {
  let s = { ...initialState(), buildings: [], population: 4000 };
  let id = 940001;
  for (let i = 0; i < 6; i++) s.buildings.push({ id: id++, spec: 'pow_wind', x: 20 + i, y: 20 });
  for (let i = 0; i < 120; i++) s.buildings.push({ id: id++, spec: 'res_block', x: (i % 50) + 5, y: Math.floor(i / 50) + 8 });
  let ticksWithLine = 0;
  for (let t = 0; t < 200; t++) {
    if (t === 80) s = reducer(s, { type: 'toggleWaterImport' });
    if (t === 140) s = reducer(s, { type: 'toggleWaterImport' });
    s = reducer(s, { type: 'tick' });
    const inSum = s.lastFlows.inflows.reduce((a, f) => a + f.value, 0);
    const outSum = s.lastFlows.outflows.reduce((a, f) => a + f.value, 0);
    assert.equal(
      s.fundsAtTickEnd - s.fundsAtTickStart,
      inSum - outSum,
      `conservation broke at tick ${t}`
    );
    if (s.lastFlows.outflows.some((f) => BUYIN_LABELS.includes(f.label))) ticksWithLine++;
  }
  assert.ok(ticksWithLine > 100, `expected sustained buy-in activity, saw ${ticksWithLine} ticks`);
  const report = runConsistencyChecks(s);
  const failed = report.checks.filter((c) => !c.ok).map((c) => `${c.id}: ${c.detail}`);
  assert.deepEqual(failed, [], 'consistency checks must stay green over a live run with buy-in active');
});

test('all three toggles journal, and a replay of a toggle script reproduces them', () => {
  for (const type of ['toggleWaterImport', 'toggleWastewaterContract', 'toggleRefuseContract']) {
    assert.equal(isStateAffecting({ type }), true, `${type} must be journaled`);
  }
  const script = [
    { type: 'toggleWaterImport' },
    { type: 'tick' },
    { type: 'toggleRefuseContract' },
    { type: 'tick' },
  ];
  let journal = emptyJournal();
  let live = initialState();
  for (const a of script) {
    journal = recordAction(journal, live.tick, a);
    live = reducer(live, a);
  }
  const replayed = replayFromGenesis(journal);
  assert.equal(replayed.waterImportEnabled, live.waterImportEnabled);
  assert.equal(replayed.wastewaterContractEnabled, live.wastewaterContractEnabled);
  assert.equal(replayed.refuseContractEnabled, live.refuseContractEnabled);
  assert.equal(live.waterImportEnabled, false, 'precondition: the script really flipped water off');
  assert.equal(live.refuseContractEnabled, false);
});

test('consistency upkeep exclusion is label-EXACT and does not swallow the Water & Waste upkeep bucket', () => {
  // A city with real water-plant upkeep AND an active buy-in line: if the
  // exclusion were widened (e.g. a startsWith/includes match), the plant's
  // 'Water & Waste' upkeep would vanish from actualUpkeep and the
  // reconciliation would diverge.
  let s = { ...initialState(), buildings: [], population: 20000 };
  let id = 950001;
  for (let i = 0; i < 40; i++) s.buildings.push({ id: id++, spec: 'pow_wind', x: (i % 20) + 20, y: 20 + Math.floor(i / 20) });
  s.buildings.push({ id: id++, spec: 'wat_tower', x: 10, y: 10 }); // 4000 of 20000 served -> real shortfall AND real upkeep
  for (let i = 0; i < 60; i++) s.buildings.push({ id: id++, spec: 'res_hut', x: (i % 40) + 5, y: 12 });
  s = reducer(s, { type: 'tick' });
  const hasUpkeepBucket = s.lastFlows.outflows.some((f) => f.label === 'Water & Waste');
  assert.ok(hasUpkeepBucket, 'precondition: a real Water & Waste upkeep bucket line exists');
  assert.ok(
    s.lastFlows.outflows.some((f) => BUYIN_LABELS.includes(f.label)),
    'precondition: at least one buy-in line is active in the same tick'
  );
  const check = runConsistencyChecks(s).checks.find((c) => c.id === 'flows.upkeep-total-matches');
  assert.ok(check);
  assert.equal(check.ok, true, `upkeep reconciliation must stay exact: ${check.detail}`);
});

test('ROUND-ADDED (M4 survivor) REWORKED (BUG-1028): the cost is CEIL-rounded to integer GBP, never zero for a non-zero shortfall', () => {
  // Original M4 finding: the author suite's fixtures all produce an
  // exactly-integer product (1000 persons x 0.08 = 80), so a Math.round ->
  // Math.floor mutation survived it. LEAD RULING point 3 (BUG-1028) then
  // replaced Math.round with Math.ceil outright — a non-zero shortfall must
  // NEVER book £0, which Math.round did for any shortfall below its own
  // round-up threshold (see the DOCUMENTS test immediately below, now
  // reworked into a regression pin instead of a documented hole). Pinned
  // here at the unit level AND through computeFlows; a reintroduced
  // Math.floor OR a reintroduced Math.round both turn this red (floor(0.56)
  // = 0 and floor(0.24) = 0 either way; round(0.24) = 0 too — only ceil
  // gets both cases right).
  assert.equal(utilityBuyInCostPerTick(0, 7, 0.08), 1, 'ceil(0.56) = 1');
  assert.equal(utilityBuyInCostPerTick(0, 3, 0.08), 1, 'ceil(0.24) = 1 — BUG-1028: never £0 for a real shortfall');
  const tiny = waterShortCity({ population: 7, buildings: [{ id: 1, spec: 'pow_wind', x: 20, y: 20 }] });
  const cw = serviceCoverageOf(tiny).find((c) => c.id === 'cleanwater');
  assert.equal(cw.need - cw.cap, 7, 'precondition: a 7-person shortfall, a fractional product');
  const line = computeFlows(tiny).outflows.find((f) => f.label === WATER_IMPORT_OUTFLOW_LABEL);
  assert.ok(line, 'a fractional cost must still be booked (rounded up), never truncated away');
  assert.equal(line.value, 1);
});

test('REWORKED BUG-1028: a sub-rounding-threshold shortfall now costs at least £1, never FREE', () => {
  // Was: "DOCUMENTS BUG-1028: a sub-rounding-threshold shortfall is covered
  // entirely FREE" — a documented balance hole, not a regression pin, per
  // the original round verdict. LEAD RULING point 3 closed the hole
  // (Math.ceil replaces Math.round in utilityBuyInCostPerTick/
  // gridImportCostPerTick, fiscal.ts): this is now a genuine regression pin.
  // A hamlet (the exact early-game case this feature exists for) with a
  // clean-water shortfall below the OLD round-up threshold (< 6.25 persons
  // at 0.08) still reports fully covered for CONSEQUENCE purposes
  // (effectiveCleanWaterCoverageOf stays 1 — the price premium, not a
  // quality penalty, is the point) but now books a real, non-zero cost.
  const hamlet = waterShortCity({ population: 6, buildings: [{ id: 1, spec: 'pow_wind', x: 20, y: 20 }] });
  const cw = serviceCoverageOf(hamlet).find((c) => c.id === 'cleanwater');
  assert.ok(cw.need > cw.cap, 'precondition: a real shortfall');
  assert.equal(effectiveCleanWaterCoverageOf(hamlet), 1, 'it is reported fully covered for consequence purposes ...');
  const line = computeFlows(hamlet).outflows.find((f) => f.label === WATER_IMPORT_OUTFLOW_LABEL);
  assert.ok(line, '... but BUG-1028: the buy-in line must be present, not absent');
  assert.ok(line.value >= 1, `... and cost at least £1, got ${line?.value}`);
});

test('ROUND-ADDED (M8 survivor, BUG-1029): each panel toggle really dispatches its journaled action', () => {
  // AC-9's own declared false-pass ("toggle present but clicking does not
  // dispatch") was untested: replacing the WaterTab button's onClick with a
  // no-op left every author test GREEN (round mutant M8). The .tsx suite only
  // renders to a string, so no click path is exercised anywhere. Until a real
  // click test exists, this pins the wiring structurally against the shipped
  // source — a no-op handler, a wrong action type, or a direct state write
  // all fail it.
  const src = readFileSync(new URL('../src/components/left/tabs/servicesTabs.tsx', import.meta.url), 'utf8');
  for (const type of ['toggleWaterImport', 'toggleWastewaterContract', 'toggleRefuseContract']) {
    assert.ok(
      src.includes(`onClick={() => dispatch({ type: '${type}' })}`),
      `servicesTabs.tsx must dispatch ${type} from a toggle's onClick`
    );
    assert.equal(isStateAffecting({ type }), true, `${type} must also be journaled`);
  }
});

// ===========================================================================
// ROUND 2 (opus-reround-feat711-inc2) - pins added against the REWORKED build.
// Two are FINDINGS (RED by design, BUG-1047 / BUG-1048) and must go GREEN on
// the next rework; the rest are regression armour that kills mutants which
// survived every author + round suite in r2 (BUG-1049 / BUG-1050).
// ===========================================================================

let r2id = 990000;
const r2add = (s, spec, n, x0, y0) => {
  for (let i = 0; i < n; i++) s.buildings.push({ id: r2id++, spec, x: ((x0 + i) % 240) + 3, y: y0 + Math.floor((x0 + i) / 240) });
  return s;
};
/** An OVERSUPPLIED city: every utility has far more capacity than the
 *  population needs, so each raw coverage ratio is well above 1. */
function oversuppliedCity(flags = {}) {
  r2id = 990000;
  const s = { ...initialState(), buildings: [], population: 2000, ...flags };
  r2add(s, 'res_block', 10, 5, 5);
  r2add(s, 'pow_wind', 30, 5, 15);
  r2add(s, 'wat_clean', 3, 5, 50);
  r2add(s, 'wat_waste', 3, 5, 55);
  r2add(s, 'waste_depot', 30, 5, 60);
  return s;
}
const ALL_COVERS_OFF = {
  gridImportEnabled: false,
  waterImportEnabled: false,
  wastewaterContractEnabled: false,
  refuseContractEnabled: false,
};

test('FINDING BUG-1047: with every cover OFF the OVERSUPPLY signal must survive - cleanwater/waste demand rows stay negative, exactly as before this feature', () => {
  // effective*CoverageOf ends in Math.min(1, raw < 1 && on ? 1 : raw): the
  // OUTER clamp is applied to the RAW ratio too, so the helper is not a
  // pass-through when the contract is OFF. serviceDemandOf's new cleanwater/
  // waste branches consume it, and that branch never clamped before - so an
  // oversupplied city's surplus reading (a NEGATIVE demand index) is
  // destroyed on the legacy path, contradicting AC-3/AC-13 and the helpers'
  // own "byte-identical to the pre-feature legacy path" doc comments.
  // Measured against a full scratch swap of the 10 modified src files to
  // HEAD: HEAD reads cleanwater -100 / waste -100 / power -100, the build
  // under review reads 0 / 0 / -100. The POWER row is the in-fixture control
  // (inc2 does not touch serviceDemandOf's power branch) and proves the
  // divergence is the new clamp, not the fixture.
  const s = oversuppliedCity(ALL_COVERS_OFF);
  const cov = serviceCoverageOf(s);
  assert.ok(cov.find((c) => c.id === 'cleanwater').coverage > 1, 'precondition: clean water is oversupplied');
  assert.ok(cov.find((c) => c.id === 'waste').coverage > 1, 'precondition: waste water is oversupplied');
  const rows = new Map(serviceDemandOf(s).map((r) => [r.id, r.value]));
  assert.ok(rows.get('power') < 0, 'control: the untouched power row still reports the surplus');
  assert.equal(rows.get('cleanwater'), rows.get('power'), 'cleanwater must report the same surplus as the control row');
  assert.equal(rows.get('waste'), rows.get('power'), 'waste must report the same surplus as the control row');
});

test('FINDING BUG-1048: a fully CONTRACTED waste-water shortfall must not still dock approval (or migration) through the leak penalty', () => {
  // approvalOf (engine.ts ~11328) docks 5 approval on waterBalanceOf(s).leak,
  // i.e. discharge capacity below 80% of clean capacity - a consequence of
  // exactly the shortfall the Waste-Water Contract buys in, and ungated by
  // wastewaterContractEnabled. Measured: approval 26 contracted vs 31 on the
  // twin with the plant built, and attractivenessOf 0.18534090909090906 vs
  // 0.18659090909090909 - BUG-1026's half-wire class surviving through a
  // reader the serviceCoverageOf grep could not see (leak reads waterCaps).
  const build = (withWaste) => {
    r2id = 970000;
    const s = { ...initialState(), buildings: [], population: 20000, wastewaterContractEnabled: true };
    r2add(s, 'res_block', 40, 5, 5);
    r2add(s, 'pow_wind', 40, 5, 40);
    r2add(s, 'wat_clean', 2, 5, 60);
    if (withWaste) r2add(s, 'wat_waste', 2, 5, 70);
    r2add(s, 'waste_depot', 20, 5, 80);
    return s;
  };
  const contracted = build(false);
  const twin = build(true);
  const ww = serviceCoverageOf(contracted).find((c) => c.id === 'waste');
  assert.ok(ww.need > ww.cap, 'precondition: a real waste-water shortfall');
  assert.equal(effectiveWastewaterCoverageOf(contracted), 1, 'precondition: the contract reports it fully covered');
  assert.equal(
    approvalOf(contracted),
    approvalOf(twin),
    'a contracted waste-water shortfall must not dock approval - the leak penalty still fires'
  );
  assert.equal(
    attractivenessOf(contracted, 60),
    attractivenessOf(twin, 60),
    'and must not dock migration either'
  );
});

test('ROUND2 (BUG-1049, mutants M3/M4): attract responds to Grid Import in BOTH directions, and effectivePowerCoverageOf is not a constant', () => {
  // LEAD RULING point 1 put POWER into the attract substitution
  // (effectivePowerCoverageOf + its row in effectiveServiceCoverageOf,
  // closing BUG-1030). Nothing pinned it: deleting the power row from
  // effectiveServiceCoverageOf (M3) and making effectivePowerCoverageOf
  // return 1 unconditionally (M4) BOTH passed every author and round suite.
  const powerShort = (flag) => {
    r2id = 960000;
    const s = { ...initialState(), buildings: [], population: 20000, gridImportEnabled: flag };
    r2add(s, 'res_block', 40, 5, 5);
    r2add(s, 'pow_wind', 1, 5, 40);
    r2add(s, 'wat_clean', 2, 5, 60);
    r2add(s, 'wat_waste', 2, 5, 70);
    r2add(s, 'waste_depot', 20, 5, 80);
    return s;
  };
  const on = powerShort(true);
  const off = powerShort(false);
  const rawOff = serviceCoverageOf(off).find((c) => c.id === 'power').coverage;
  assert.ok(rawOff < 1, 'precondition: a real power deficit');
  // M4 killer: OFF must report the RAW ratio, never a constant 1.
  assert.equal(effectivePowerCoverageOf(off), rawOff, 'cover OFF returns the raw power ratio unchanged');
  assert.equal(effectivePowerCoverageOf(on), 1, 'cover ON substitutes the deficit as fully covered');
  // M3 killer: the substitution must reach effectiveServiceCoverageOf's row
  // and therefore attractivenessOf's coverage average.
  assert.equal(
    effectiveServiceCoverageOf(on).find((r) => r.id === 'power').coverage,
    1,
    'effectiveServiceCoverageOf must substitute the power row, not just cleanwater/waste'
  );
  assert.ok(
    attractivenessOf(on, 60) > attractivenessOf(off, 60),
    'a bought-in power deficit must make the city strictly more attractive than the uncovered twin'
  );
});

test('ROUND2 (BUG-1050, mutant M6): inc1 grid import is CEIL-rounded - a fractional shortfall is never free', () => {
  // The rework applied Math.ceil to gridImportCostPerTick too, but the test
  // edits only rewrote the EXPECTED expression (Math.round(...) ->
  // Math.ceil(...)) on exact-integer fixtures, which is tautological: a
  // ceil -> round revert survived grid-import.test.mjs AND
  // attack-grid-import.test.mjs. This is the inc1 twin of the inc2 pin above.
  assert.equal(gridImportCostPerTick(0, 0.1, 2.5), 1, 'ceil(0.25) = 1 - a 0.1 MW deficit costs at least GBP 1');
  assert.equal(gridImportCostPerTick(0, 0.3, 2.5), 1, 'ceil(0.75) = 1, not the round()/floor() 1/0 split');
  assert.equal(gridImportCostPerTick(5, 5, 2.5), 0, 'no deficit, no charge');
});

// ===========================================================================
// ROUND 3 (opus-round3-feat711-inc2) — FINDING PIN. RED against the build
// under review; must go GREEN on rework.
// ===========================================================================

test('FINDING BUG-1061: with EVERY cover OFF a sewage leak must still dock approval exactly as it did before this feature', () => {
  // The r2 rework gated approvalOf's leak penalty on isWastewaterShortageActive
  // (LEAD RULING point 2). That predicate is false whenever there is no
  // population-level waste-water SHORTAGE — regardless of the contract toggle —
  // but waterBalanceOf().leak is a CAPACITY-RATIO fact (waste/clean < 0.8) that
  // fires happily on a city whose waste capacity fully covers its need and is
  // merely small next to an over-built clean network. On those cities the -5
  // now vanishes EVEN WITH EVERY COVER EXPLICITLY OFF, i.e. the legacy path is
  // no longer byte-identical to main (measured: approvalOf 31 here vs 26 on
  // HEAD, and the 300-tick canonical digest diverges). BUG-1048's own text
  // predicted exactly this and asked for a ruling rather than a blanket gate.
  //
  // The expected delta is re-derived from the no-leak twin, never a literal:
  // the two cities differ ONLY in clean capacity, so every other approval term
  // is identical and the gap IS the leak penalty.
  const ALL_OFF = {
    gridImportEnabled: false,
    waterImportEnabled: false,
    wastewaterContractEnabled: false,
    refuseContractEnabled: false,
  };
  const leakCity = (cleanPlants) => {
    r2id = 930000;
    const s = { ...initialState(), buildings: [], population: 2000, ...ALL_OFF };
    r2add(s, 'res_block', 40, 5, 5);
    r2add(s, 'pow_wind', 30, 5, 20);
    r2add(s, 'wat_clean', cleanPlants, 5, 50);
    r2add(s, 'wat_waste', 1, 5, 60);
    r2add(s, 'waste_depot', 30, 5, 70);
    return s;
  };
  const leaky = leakCity(5); // clean 100,000 vs waste 20,000 -> ratio 0.2 < 0.8
  const balanced = leakCity(1); // clean 20,000 vs waste 20,000 -> ratio 1.0
  // Preconditions: the leak is a real physical fact, and there is NO
  // waste-water shortage at all (so the contract is buying nothing).
  assert.equal(waterBalanceOf(leaky).leak, true, 'precondition: the over-built-clean city leaks');
  assert.equal(waterBalanceOf(balanced).leak, false, 'precondition: the twin does not leak');
  assert.equal(
    isWastewaterShortageActive(leaky),
    false,
    'precondition: no waste-water SHORTAGE — capacity already covers the population'
  );
  const wasteRow = serviceCoverageOf(leaky).find((c) => c.id === 'waste');
  assert.ok(wasteRow.coverage >= 1, 'precondition: the waste row is fully covered');
  assert.equal(
    approvalOf(balanced) - approvalOf(leaky),
    5,
    'a leak with every cover OFF must still cost exactly the legacy 5 approval points ' +
      `(got balanced=${approvalOf(balanced)}, leaky=${approvalOf(leaky)}) — the BUG-1048 gate ` +
      'keys off shortage-existence instead of the contract, so it suppresses the penalty on the legacy path'
  );
});

// ───────────────────────────────────────────────────────────────────────────
// ROUND 4 ADDITIONS (opus-round4-feat711-inc2) — BUG-1074 survivors.
//
// Two of the three serviceDemandOf gates AC-3 relies on had NOTHING pinning
// them: mutating the 'cleanwater' branch back to `c.coverage` (M18) and the
// refuse row back to `waste.coverage` (M23) each kept 9 suites GREEN, while
// the identical revert on the 'waste' row (M22, the control) was RED. Both
// mutants are non-equivalent: serviceDemandOf is what DemandDock renders AND
// what pickAutoSpec() sorts on with a >25 auto-build trigger, so a regression
// would put a red 100 demand meter on a contracted service and start
// auto-building plants the contract already covers — the exact r1/r2
// half-wire class (BUG-1026/BUG-1027), silently restorable.
//
// Every expectation below is re-derived from the UNCOVERED twin of the same
// fixture, never written as a literal.
// ───────────────────────────────────────────────────────────────────────────

test('ROUND4 (BUG-1074, mutants M18/M23): the cleanwater AND refuse demand-index rows are gated by their own contract, not just the waste row', () => {
  // A city with a real shortfall in all three utilities: residents + surplus
  // power, NO water plants at all, NO refuse depots. Grid Import is held OFF
  // throughout so only the three inc2 toggles can move anything.
  let id = 970001;
  const city = (overrides) => {
    const s = {
      ...initialState(),
      buildings: [],
      population: 2000,
      gridImportEnabled: false,
      ...overrides,
    };
    for (let i = 0; i < 40; i++) s.buildings.push({ id: id++, spec: 'res_block', x: (i % 50) + 3, y: 5 });
    for (let i = 0; i < 40; i++) s.buildings.push({ id: id++, spec: 'pow_wind', x: (i % 50) + 3, y: 20 });
    return s;
  };
  const off = city({ waterImportEnabled: false, wastewaterContractEnabled: false, refuseContractEnabled: false });
  const on = city({ waterImportEnabled: true, wastewaterContractEnabled: true, refuseContractEnabled: true });

  const rowsOf = (s) => new Map(serviceDemandOf(s).map((r) => [r.id, r.value]));
  const offRows = rowsOf(off);
  const onRows = rowsOf(on);

  // Preconditions, derived not assumed: every one of the three services is in
  // a genuine shortfall on this fixture, so each row has something to escalate.
  const covOff = new Map(serviceCoverageOf(off).map((r) => [r.id, r.coverage]));
  assert.ok(covOff.get('cleanwater') < 1, 'precondition: a real clean-water shortfall');
  assert.ok(covOff.get('waste') < 1, 'precondition: a real waste-water shortfall');
  assert.ok(collectionCoverageOf(off) < 1, 'precondition: a real refuse shortfall');
  for (const id of ['cleanwater', 'waste', 'refuse']) {
    assert.ok(
      offRows.get(id) > 0,
      `precondition: with cover OFF the ${id} demand row escalates (got ${offRows.get(id)})`
    );
  }

  // The pin itself: with each contract ON the same shortfall must NOT raise
  // the meter at all. The expected value is NOT a literal — it is taken from
  // the ONE row of these three whose gate is already independently pinned
  // (the 'waste' row: mutating it back to the raw coverage is RED against the
  // author's suite, the round's control mutant M22), so cleanwater and refuse
  // are asserted to behave exactly like the gate that is known to work.
  const coveredReference = onRows.get('waste');
  assert.ok(
    coveredReference < offRows.get('waste'),
    'precondition: the independently-pinned waste gate really does suppress its own row ' +
      `(on=${coveredReference}, off=${offRows.get('waste')})`
  );
  for (const svc of ['cleanwater', 'refuse']) {
    assert.equal(
      onRows.get(svc),
      coveredReference,
      `the ${svc} demand-index row must read the SAME suppressed value as the independently-pinned ` +
        `waste row while its own contract is ON (got ${onRows.get(svc)}, waste reference ${coveredReference}, ` +
        `uncovered twin ${offRows.get(svc)}) — the row is still reading the RAW coverage`
    );
    assert.ok(
      offRows.get(svc) > onRows.get(svc),
      `sanity: the uncovered twin's ${svc} row must be strictly higher than the covered one`
    );
  }
});
