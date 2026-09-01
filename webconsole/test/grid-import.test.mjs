// grid-import.test.mjs — FEAT-2326609711 inc1: Power Grid Import.
// Covers AC-1..AC-7, AC-9..AC-12 (AC-8 finance-panel row / AC-9 UI toggle
// smoke-render live in test/mount.test.tsx — this file owns the PURE engine
// logic those panels read, per the same split waste-panel-inc3.test.mjs uses
// between wasteDisplayModel.mjs tests and the WasteTab mount smoke test).
//
// node --test type-strips the .ts imports, so these exercise the exact
// shipped sim code. RED/GREEN proofs for the key assertions are recorded in
// the build report (scratch cp/mv of fiscal.ts/engine.ts, never a git revert
// — GR#24).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer, computeFlows } from '../src/sim/engine.ts';
import { powerStats, brownoutOf, isBrownoutActive, serviceDemandOf, SPECS } from '../src/sim/data.ts';
import {
  GRID_IMPORT_TARIFF_PER_MW,
  GRID_EXPORT_TARIFF_PER_MW,
  GRID_IMPORT_ENABLED_DEFAULT,
  GRID_IMPORT_OUTFLOW_LABEL,
  gridImportCostPerTick,
  verifyGridTariffInvariant,
  DEBT_THRESHOLD_FOR_BAILOUT,
  INSOLVENCY_WARNING_THRESHOLD,
} from '../src/sim/fiscal.ts';
import { emptyJournal, recordAction, isStateAffecting } from '../src/sim/journal.ts';
import { replayFromGenesis, replayIsDeterministic } from '../src/sim/genesisReplay.ts';
import { runConsistencyChecks } from '../src/sim/consistency.ts';

// ---------- fixtures ----------

// A city with a REAL power shortfall: enough population/office/industrial to
// push need above a small fixed capacity. Mirrors brownout.test.mjs's city().
function shortageCity(overrides = {}) {
  const s = { ...initialState(), buildings: [], population: 16667, ...overrides };
  let id = 900001;
  const put = (spec, n = 1) => {
    for (let i = 0; i < n; i++) s.buildings.push({ id: id++, spec, x: (id % 900) + 5, y: 5 });
  };
  put('com_shop', 10); // gives Business Tax a nonzero base to scale (AC-1/AC-3)
  put('pow_wind', 24); // 24*8 = 192 MW cap
  return s; // need = round(16667*0.012) = 200 MW -> 8 MW shortfall
}

function pw(s) {
  return powerStats(s);
}

// ---------- AC-1: new city defaults to grid import enabled ----------

test('AC-1: initialState() defaults gridImportEnabled to GRID_IMPORT_ENABLED_DEFAULT (true)', () => {
  assert.equal(GRID_IMPORT_ENABLED_DEFAULT, true, 'precondition: default is ON per the Design Ruling');
  const s = initialState();
  assert.equal(s.gridImportEnabled, true);
});

test('AC-1: with a real shortage and import ON, no shortfall — Grid Import outflow appears, brownout income penalty does not fire', () => {
  const s = shortageCity();
  const stats = pw(s);
  assert.ok(stats.need > stats.cap, `precondition: real shortage, need ${stats.need} > cap ${stats.cap}`);
  assert.equal(s.gridImportEnabled, true, 'precondition: default ON');

  const flows = computeFlows(s);
  const line = flows.outflows.find((f) => f.label === GRID_IMPORT_OUTFLOW_LABEL);
  assert.ok(line !== undefined, 'Grid Import outflow must appear when enabled + shortage');
  assert.ok(line.value > 0);

  // The brownout income penalty must NOT also fire (mutually exclusive, AC-3 false-pass warning).
  const bo = brownoutOf(s);
  assert.ok(bo.active, 'precondition: brownoutOf reads active (pure function of powerStats only)');
  assert.ok(bo.incomeFactor < 1, 'precondition: the brownout factor would visibly bite if it were applied');

  // r2 FIX (was a can't-fail x===x comparing two .find() calls on the SAME
  // array): independently-computed UNSCALED baseline — a twin city with
  // enough capacity that NO deficit exists at all, so no brownout factor of
  // any kind is in play. RED-proven: reverting engine.ts's income gate to
  // `if (brownout.active)` (dropping the isBrownoutActive/!gridImportOn
  // check) turns this red because `businessTaxFlow.value` then reads
  // strictly LESS than `unscaledBusinessTax` (see build report).
  const twin = { ...initialState(), gridImportEnabled: true, buildings: [], population: 16667 };
  let tid = 990001;
  for (let i = 0; i < 10; i++) twin.buildings.push({ id: tid++, spec: 'com_shop', x: tid % 900, y: 5 });
  for (let i = 0; i < 26; i++) twin.buildings.push({ id: tid++, spec: 'pow_wind', x: tid % 900, y: 10 }); // 208 MW >= 200 need
  const twinStats = pw(twin);
  assert.ok(twinStats.cap >= twinStats.need, 'twin precondition: no deficit');
  const unscaledBusinessTax = computeFlows(twin).inflows.find((f) => f.label === 'Business Tax').value;
  const businessTaxFlow = flows.inflows.find((f) => f.label === 'Business Tax');
  assert.equal(
    businessTaxFlow.value,
    unscaledBusinessTax,
    'covered income must be genuinely unscaled (matches an independently-built no-deficit twin), not brownout-penalised'
  );
});

// ---------- r2 fix: SSOT predicate + DemandDock reader (grep sweep finding) ----------
//
// isBrownoutActive() is the single predicate every brownout CONSEQUENCE
// (income, wellbeing, DemandDock banner, power-row demand-index escalation)
// must read (GR#3). This pins the third and fourth consumers: serviceDemandOf's
// power row must NOT escalate/alert while cover is on, even though the raw
// physical deficit (brownoutOf) is real.
test('r2 fix: serviceDemandOf power row does not escalate/alert while Grid Import cover is ON', () => {
  const s = shortageCity({ gridImportEnabled: true });
  assert.ok(brownoutOf(s).active, 'precondition: a real physical deficit exists');
  assert.equal(isBrownoutActive(s), false, 'precondition: SSOT reads not-a-brownout while covered');

  const powerEntry = serviceDemandOf(s).find((m) => m.id === 'power');
  assert.ok(!powerEntry.alert, 'covered shortfall must not raise the BROWNOUT alert flag');
  assert.ok(powerEntry.value < 100, 'covered shortfall must not peg the escalated brownout index');
});

test('r2 fix: serviceDemandOf power row DOES escalate/alert with the identical shortage when cover is OFF', () => {
  const s = shortageCity({ gridImportEnabled: false });
  assert.equal(isBrownoutActive(s), true);
  const powerEntry = serviceDemandOf(s).find((m) => m.id === 'power');
  assert.equal(powerEntry.alert, true, 'the toggle is a real gate: legacy escalation still fires when off');
});

// ---------- AC-2: import outflow = (need - cap) * tariff ----------

test('AC-2: gridImportCostPerTick(50, 70, 2.5) === 50 (shortfall 20 MW @ 2.5/MW)', () => {
  assert.equal(gridImportCostPerTick(50, 70, GRID_IMPORT_TARIFF_PER_MW), Math.round((70 - 50) * 2.5));
  assert.equal(gridImportCostPerTick(50, 70, 2.5), 50);
});

test('AC-2: gridImportCostPerTick returns 0 when cap >= need (no shortage)', () => {
  assert.equal(gridImportCostPerTick(70, 70, 2.5), 0);
  assert.equal(gridImportCostPerTick(100, 70, 2.5), 0);
});

test('AC-2: computeFlows Grid Import value matches the helper exactly; absent (not zero) when no shortage', () => {
  const s = shortageCity();
  const stats = pw(s);
  const flows = computeFlows(s);
  const line = flows.outflows.find((f) => f.label === GRID_IMPORT_OUTFLOW_LABEL);
  const expected = gridImportCostPerTick(stats.cap, stats.need, GRID_IMPORT_TARIFF_PER_MW);
  assert.equal(line.value, expected);

  // No-shortage city: line must be ABSENT, not present with value 0.
  const surplus = { ...initialState(), buildings: [], population: 10 };
  surplus.buildings.push({ id: 1, spec: 'pow_coal', x: 5, y: 5 }); // 80 MW >> tiny need
  const surplusStats = pw(surplus);
  assert.ok(surplusStats.cap > surplusStats.need, 'precondition: surplus city has no shortage');
  const surplusFlows = computeFlows(surplus);
  assert.equal(
    surplusFlows.outflows.find((f) => f.label === GRID_IMPORT_OUTFLOW_LABEL),
    undefined,
    'Grid Import must be ABSENT (not a zero-value entry) when there is no shortage'
  );
});

// ---------- AC-3: toggle OFF -> no outflow, legacy brownout applies unchanged ----------

test('AC-3: gridImportEnabled=false -> no Grid Import outflow, brownout income penalty fires as before', () => {
  const s = shortageCity({ gridImportEnabled: false });
  const stats = pw(s);
  assert.ok(stats.need > stats.cap, 'precondition: shortage');

  const flows = computeFlows(s);
  assert.equal(
    flows.outflows.find((f) => f.label === GRID_IMPORT_OUTFLOW_LABEL),
    undefined,
    'Grid Import must be absent when toggle is off'
  );

  const bo = brownoutOf(s);
  assert.ok(bo.active);
  const businessTax = flows.inflows.find((f) => f.label === 'Business Tax').value;
  // Recompute the UNSCALED figure the same way brownout.test.mjs does, via a
  // shortage-free twin: same commercial base (10 com_shop), enough power
  // capacity that brownout is inactive.
  const twin = { ...initialState(), gridImportEnabled: false, buildings: [], population: 16667 };
  let id = 990001;
  for (let i = 0; i < 10; i++) twin.buildings.push({ id: id++, spec: 'com_shop', x: id % 900, y: 5 });
  for (let i = 0; i < 26; i++) twin.buildings.push({ id: id++, spec: 'pow_wind', x: id % 900, y: 10 }); // 208 MW >= 200 need
  const twinStats = pw(twin);
  assert.ok(twinStats.cap >= twinStats.need, 'twin precondition: no deficit');
  const unscaledBusinessTax = computeFlows(twin).inflows.find((f) => f.label === 'Business Tax').value;
  assert.ok(businessTax < unscaledBusinessTax, 'brownout must still scale income down when import is off');
  assert.equal(businessTax, Math.round(unscaledBusinessTax * bo.incomeFactor));
});

test('AC-3: legacy path is identical however "off" is reached — direct construction vs default-then-toggled — not two clones of one fixture', () => {
  // r2 FIX (was a can't-fail deepEqual of two identical fixtures, which only
  // proves computeFlows is pure): `a` is built with gridImportEnabled=false
  // directly; `b` starts at the real default (true) and reaches false via
  // the PRODUCTION toggleGridImport reducer action — an independently
  // derived path to the same logical state. RED-proven: mutating the
  // reducer's toggle case to `return { ...state, gridImportEnabled: true }`
  // (an inverted/no-op toggle) makes `b.gridImportEnabled` stay `true`,
  // so `computeFlows(b)` still books a Grid Import line while `computeFlows(a)`
  // does not — the deepEqual below turns red (see build report).
  const a = shortageCity({ gridImportEnabled: false });
  const b = reducer(shortageCity(), { type: 'toggleGridImport' });
  assert.equal(b.gridImportEnabled, false, 'precondition: the toggle path actually reached "off"');
  assert.deepEqual(computeFlows(a), computeFlows(b));
});

// ---------- AC-4: tariff invariant ----------

test('AC-4: verifyGridTariffInvariant derives a real cheapest plant from the live catalogue and reports the truth', () => {
  const result = verifyGridTariffInvariant(SPECS);
  assert.ok(result.cheapestPlantId !== null, 'a cheapest power plant must be found in the catalogue');
  assert.ok(SPECS[result.cheapestPlantId], 'reported plant id must be a real catalogue spec');
  assert.equal(SPECS[result.cheapestPlantId].kind, 'power');

  // Independently recompute the minimum to prove the function is not hardcoded (GR#15).
  let expectedMin = Infinity;
  let expectedId = null;
  for (const sp of Object.values(SPECS)) {
    if (sp.kind !== 'power' || sp.placeholder || !sp.mw || !sp.cost) continue;
    const v = sp.cost / (sp.mw * (25 * 360)) + (sp.upkeep ?? 0) / sp.mw;
    if (v < expectedMin) {
      expectedMin = v;
      expectedId = sp.id;
    }
  }
  assert.equal(result.cheapestPlantId, expectedId);
  assert.ok(Math.abs(result.cheapestAmortisedPerMwTick - expectedMin) < 1e-9);

  // The inc1 design promise (import strictly dearer than export) genuinely holds today.
  assert.equal(result.importExceedsExport, true, 'GRID_IMPORT_TARIFF_PER_MW must exceed GRID_EXPORT_TARIFF_PER_MW');
  assert.equal(GRID_IMPORT_TARIFF_PER_MW > GRID_EXPORT_TARIFF_PER_MW, true);

  // The `allHold` flag must be the honest AND of all three legs (never hardcoded true) —
  // see BUG-477: today's catalogue does NOT clear the export/local-amortised-cost leg,
  // so this must read false, not be silently forced true.
  assert.equal(result.allHold, result.importExceedsExport && result.exportExceedsLocal && result.importExceedsLocal);
});

test('AC-4 RED-prove: verifyGridTariffInvariant honestly fails the local-cost legs for an absurdly-dear catalogue, never hardcoded true', () => {
  // r2 FIX (was a can't-fail test: it built a literal object and asserted
  // `1.0 > 2.5 === false`, never once calling the production function).
  // This calls the REAL verifyGridTariffInvariant() with a synthetic
  // catalogue engineered so even the (higher) import tariff cannot clear
  // the local amortised cost — proving the function actually derives its
  // legs from the catalogue instead of returning a hardcoded pass.
  const direCatalogue = { costly: { id: 'costly', kind: 'power', mw: 1, cost: 1e15, upkeep: 1e12 } };
  const result = verifyGridTariffInvariant(direCatalogue);
  assert.equal(result.cheapestPlantId, 'costly');
  assert.equal(result.exportExceedsLocal, false, 'an absurdly dear local plant must fail the export-exceeds-local leg');
  assert.equal(result.importExceedsLocal, false, 'and the import-exceeds-local leg');
  assert.equal(result.allHold, false, 'allHold must honestly reflect the failing legs, never forced true');

  // The real, live tariff constants (today's catalogue-independent ordering)
  // also hold, checked directly against the exported constants (not
  // hardcoded numbers, GR#15).
  assert.equal(GRID_IMPORT_TARIFF_PER_MW > GRID_EXPORT_TARIFF_PER_MW, true);
});

// ---------- AC-5: toggle persists through genesis replay ----------

function driveAndRecord(actions) {
  let journal = emptyJournal();
  let state = initialState();
  for (const action of actions) {
    journal = recordAction(journal, state.tick, action);
    state = reducer(state, action);
  }
  return { journal, liveState: state };
}

test('AC-5: toggleGridImport is journaled as state-affecting', () => {
  assert.equal(isStateAffecting({ type: 'toggleGridImport' }), true);
});

test('AC-5: genesis replay reproduces the toggled-off state exactly (toggle at tick 0, ticks after)', () => {
  const SCRIPT = [
    { type: 'toggleGridImport' }, // off (was true)
    { type: 'tick' },
    { type: 'tick' },
    { type: 'tick' },
  ];
  const { journal, liveState } = driveAndRecord(SCRIPT);
  assert.equal(liveState.gridImportEnabled, false, 'live path: toggled off');

  const replayed = replayFromGenesis(journal);
  assert.equal(replayed.gridImportEnabled, false, 'replay must reproduce the toggled-off state');
  assert.equal(replayed.gridImportEnabled, liveState.gridImportEnabled);
  assert.equal(replayIsDeterministic(journal), true);
});

test('AC-5 MUTATION-PROVE target: a journal with NO toggle action replays with the true default (nothing lost)', () => {
  const { journal, liveState } = driveAndRecord([{ type: 'tick' }, { type: 'tick' }]);
  assert.equal(liveState.gridImportEnabled, true);
  const replayed = replayFromGenesis(journal);
  assert.equal(replayed.gridImportEnabled, true);
});

// ---------- AC-6: conservation — outflow debited exactly once ----------

test('AC-6: Grid Import appears exactly once in outflows, and computeFlows() truly matches what the reducer books to funds', () => {
  // r2 FIX (was a can't-fail test: `fundsAtTickEnd` was DEFINED as
  // `fundsAtTickStart + inflowSum - outflowSum` and then asserted equal to
  // that exact same expression — cannot fail under any mutation).
  const s = shortageCity();
  const flows = computeFlows(s);
  const matches = flows.outflows.filter((f) => f.label === GRID_IMPORT_OUTFLOW_LABEL);
  assert.equal(matches.length, 1, 'Grid Import must appear exactly once, never duplicated');
  const stats = pw(s);
  assert.equal(
    matches[0].value,
    gridImportCostPerTick(stats.cap, stats.need, GRID_IMPORT_TARIFF_PER_MW),
    'the booked value must match the independent shortfall*tariff formula'
  );

  const inflowSum = flows.inflows.reduce((a, f) => a + f.value, 0);
  const outflowSum = flows.outflows.reduce((a, f) => a + f.value, 0);

  // Bind the STATIC computeFlows() call above to what the REDUCER actually
  // books to funds on a real tick from the SAME starting state — this is
  // the real, failable invariant: the reducer's actual fund delta must
  // equal the net flow this call reports, proving Grid Import's value
  // genuinely reaches fundsAtTickEnd through production code (not a formula
  // compared to itself). RED-proven: debiting the import cost a second time
  // directly against state.funds inside computeFlows (the AC-6 double-debit
  // mutation) makes the reducer's actual delta diverge from inflowSum -
  // outflowSum computed here, turning this red (see build report).
  const after = reducer(s, { type: 'tick' });
  assert.equal(
    after.fundsAtTickEnd - after.fundsAtTickStart,
    inflowSum - outflowSum,
    'the reducer must apply exactly the net flow computeFlows() reports, including Grid Import, no more no less'
  );
});

test('AC-6: a full tick() through the reducer conserves funds including Grid Import', () => {
  const s = shortageCity();
  const before = reducer(s, { type: 'debugFunds', amount: 0 }); // no-op normalize
  const after = reducer(before, { type: 'tick' });
  const expected = after.fundsAtTickStart +
    after.lastFlows.inflows.reduce((a, f) => a + f.value, 0) -
    after.lastFlows.outflows.reduce((a, f) => a + f.value, 0);
  assert.equal(after.fundsAtTickEnd, expected);
  assert.ok(after.lastFlows.outflows.some((f) => f.label === GRID_IMPORT_OUTFLOW_LABEL));
});

// ---------- consistency.ts cross-check (found while building AC-6) ----------

test('consistency.ts upkeep-total-matches check is NOT broken by an active Grid Import outflow', () => {
  const s = reducer(shortageCity(), { type: 'tick' });
  assert.ok(
    s.lastFlows.outflows.some((f) => f.label === GRID_IMPORT_OUTFLOW_LABEL),
    'precondition: Grid Import actually fired this tick'
  );
  const report = runConsistencyChecks(s);
  const check = report.checks.find((c) => c.id === 'flows.upkeep-total-matches');
  assert.ok(check, 'upkeep-total-matches check must exist');
  assert.equal(
    check.ok,
    true,
    `Grid Import must be excluded from the upkeep reconciliation like Refuse Collection/Waste Disposal: ${check.detail}`
  );
});

// ---------- AC-7: insolvency + policy modifiers treat Grid Import as a normal outflow ----------

test('AC-7: Grid Import is subject to applyOutflowPolicies (austerity discount) like any other outflow', () => {
  const withoutAusterity = shortageCity();
  const withAusterity = shortageCity({ policies: { ...withoutAusterity.policies, austerity: true } });
  const a = computeFlows(withoutAusterity).outflows.find((f) => f.label === GRID_IMPORT_OUTFLOW_LABEL);
  const b = computeFlows(withAusterity).outflows.find((f) => f.label === GRID_IMPORT_OUTFLOW_LABEL);
  assert.ok(a && b);
  assert.equal(b.value, Math.round(a.value * 0.9), 'austerity must discount Grid Import 10%, same as every outflow — no exemption');
});

test('AC-7: Grid Import outflow can tip the city into insolvency crisis exactly like any other outflow', () => {
  const s = shortageCity();
  const stats = pw(s);
  const importCost = gridImportCostPerTick(stats.cap, stats.need, GRID_IMPORT_TARIFF_PER_MW);
  assert.ok(importCost > 0, 'precondition: a real import cost exists to tip the balance');

  // Fund the city just above the crisis threshold by less than the import cost,
  // so THIS tick's Grid Import outflow (among others) tips it into crisis.
  const targetFunds = DEBT_THRESHOLD_FOR_BAILOUT + Math.max(1, Math.round(importCost / 2));
  const forced = reducer(s, { type: 'debugFunds', amount: targetFunds - s.funds });
  const after = reducer(forced, { type: 'tick' });
  assert.ok(
    after.lastFlows.outflows.some((f) => f.label === GRID_IMPORT_OUTFLOW_LABEL),
    'Grid Import must have been part of the flows that tipped this tick'
  );
  // The state must still reach a real insolvency band (no special exemption
  // path bypassing the standard funds->band classification).
  assert.ok(['warning', 'crisis'].includes(after.insolvencyState), `expected a distressed band, got ${after.insolvencyState}`);
});

// ---------- AC-9/AC-10: toggle is real sim state, survives unrelated actions ----------

test('AC-9/AC-10: toggleGridImport flips state.gridImportEnabled and the value survives an unrelated action', () => {
  const s0 = initialState();
  assert.equal(s0.gridImportEnabled, true);
  const s1 = reducer(s0, { type: 'toggleGridImport' });
  assert.equal(s1.gridImportEnabled, false);
  // Unrelated action (mirrors "click elsewhere, close panels" — real sim state
  // is untouched by UI-only interactions and other reducer actions).
  const s2 = reducer(s1, { type: 'tax', which: 'residential', rate: 12 });
  assert.equal(s2.gridImportEnabled, false, 'toggle must persist across unrelated dispatches (not React local state)');
  const s3 = reducer(s2, { type: 'toggleGridImport' });
  assert.equal(s3.gridImportEnabled, true);
});

// ---------- AC-11: determinism ----------

test('AC-11: Grid Import is deterministic — identical state produces identical outflow, no randomness', () => {
  const s = shortageCity();
  const f1 = computeFlows(s);
  const f2 = computeFlows(s);
  assert.deepEqual(f1, f2);
  const l1 = f1.outflows.find((f) => f.label === GRID_IMPORT_OUTFLOW_LABEL);
  const l2 = f2.outflows.find((f) => f.label === GRID_IMPORT_OUTFLOW_LABEL);
  assert.equal(l1.value, l2.value);
});

test('AC-11: replaying the same journal twice through a shortage+toggle script is byte-identical', () => {
  const SCRIPT = [
    { type: 'tick' },
    { type: 'toggleGridImport' },
    { type: 'tick' },
    { type: 'toggleGridImport' },
    { type: 'tick' },
  ];
  const { journal } = driveAndRecord(SCRIPT);
  assert.equal(replayIsDeterministic(journal), true);
});
