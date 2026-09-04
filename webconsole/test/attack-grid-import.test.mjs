// attack-grid-import.test.mjs — FEAT-2326609711 inc1 independent destructive
// round r1 (Opus). These are the ATTACK regressions: every one of them was
// written because a mutation of the shipped production code survived the
// author's own suite, or because a headline assertion in it was a tautology
// that cannot fail.
//
// Surviving mutation that motivated this file (M9):
//   engine.ts computeFlows — change `if (brownout.active && !gridImportOn)`
//   to `if (brownout.active)` so the city pays the Grid Import tariff AND
//   takes the full brownout income penalty for the same shortage.
//   All 60 tests of grid-import/brownout/engine/consistency stayed GREEN.
//   The only assertion guarding that behaviour was
//   grid-import.test.mjs:72-73, which compares one `.find()` result against
//   a second `.find()` on the SAME array — i.e. `x === x`.
//
// Everything here has been RED/GREEN proven by scratch-copy mutation of the
// production files (cp/mv, never a git revert — GR#24).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, computeFlows, reducer, wellbeingOf } from '../src/sim/engine.ts';
import { powerStats, brownoutOf, isBrownoutActive } from '../src/sim/data.ts';
import {
  GRID_IMPORT_TARIFF_PER_MW,
  GRID_EXPORT_TARIFF_PER_MW,
  GRID_IMPORT_OUTFLOW_LABEL,
  gridImportCostPerTick,
  verifyGridTariffInvariant,
  POWER_PLANT_AMORTISATION_TICKS,
} from '../src/sim/fiscal.ts';

// ---------- fixtures ----------

/** A city with a REAL power shortfall (need 200 MW, cap 144 MW — BUG-648
 * rebased pow_wind to 6 MW/turbine, still a real shortfall either way). */
function shortageCity(overrides = {}) {
  const s = { ...initialState(), buildings: [], population: 16667, ...overrides };
  let id = 900001;
  const put = (spec, n = 1) => {
    for (let i = 0; i < n; i++) s.buildings.push({ id: id++, spec, x: (id % 900) + 5, y: 5 });
  };
  put('com_shop', 10);
  put('wat_clean', 1); // real clean-water coverage so the Utilities wellbeing part is non-zero
  put('pow_wind', 24);
  return s;
}

/** The same city with enough capacity that no shortfall (and no brownout) exists. */
function surplusTwin(overrides = {}) {
  const s = { ...initialState(), buildings: [], population: 16667, ...overrides };
  let id = 990001;
  const put = (spec, n = 1) => {
    for (let i = 0; i < n; i++) s.buildings.push({ id: id++, spec, x: (id % 900) + 5, y: 10 });
  };
  put('com_shop', 10);
  put('wat_clean', 1);
  // BUG-648 (2026-09-03): pow_wind's mw was rebalanced 8->6 (data.ts) — unit
  // count bumped 26->34 so the "no shortfall" precondition still holds.
  put('pow_wind', 34); // 204 MW >= 200 MW need
  return s;
}

const flowValue = (flows, side, label) => (flows[side].find((f) => f.label === label) || {}).value;

// ---------- ATTACK 1: the mutual-exclusion promise is actually enforced ----------
//
// engine.ts's own comment claims "the two penalty mechanisms are mutually
// exclusive so they can never double-charge the same shortage". The author's
// AC-1 test asserted that with `x === x`. This asserts it against an
// independently computed unscaled baseline, so removing the `!gridImportOn`
// gate turns it RED.

test('ATTACK: import ON — powered income is NOT scaled by the brownout factor (no double charge)', () => {
  const s = shortageCity({ gridImportEnabled: true });
  const bo = brownoutOf(s);
  assert.ok(bo.active, 'precondition: brownoutOf still reads active (it is power-only, toggle-blind)');
  assert.ok(bo.incomeFactor < 1, 'precondition: the brownout factor would visibly bite if applied');

  const flows = computeFlows(s);
  assert.ok(
    flowValue(flows, 'outflows', GRID_IMPORT_OUTFLOW_LABEL) > 0,
    'precondition: the city is paying for external cover this tick'
  );

  // Independent unscaled baseline: an identical commercial/population base
  // with NO power deficit at all, so no brownout factor can be in play.
  const twin = computeFlows(surplusTwin({ gridImportEnabled: true }));
  assert.ok(brownoutOf(surplusTwin()).active === false, 'twin precondition: no deficit');

  for (const label of ['Business Tax', 'Freight Tax', 'Office Tax']) {
    const paid = flowValue(flows, 'inflows', label);
    const unscaled = flowValue(twin, 'inflows', label);
    if (paid === undefined && unscaled === undefined) continue;
    assert.equal(
      paid,
      unscaled,
      `${label} must be UNSCALED while Grid Import is covering the shortfall — ` +
        `paying the tariff AND taking the brownout income hit is a double charge`
    );
  }
});

test('ATTACK: import OFF — the same income lines ARE scaled (the gate is a real gate, not dead code)', () => {
  const off = computeFlows(shortageCity({ gridImportEnabled: false }));
  const twin = computeFlows(surplusTwin({ gridImportEnabled: false }));
  const bo = brownoutOf(shortageCity({ gridImportEnabled: false }));
  assert.equal(
    flowValue(off, 'inflows', 'Business Tax'),
    Math.round(flowValue(twin, 'inflows', 'Business Tax') * bo.incomeFactor),
    'with cover OFF the legacy brownout penalty must still bite'
  );
  assert.ok(
    flowValue(off, 'inflows', 'Business Tax') < flowValue(twin, 'inflows', 'Business Tax'),
    'the penalty must be directionally real, not a rounding no-op'
  );
});

// ---------- ATTACK 2: toggling changes NOTHING except the two documented things ----------
//
// AC-3's "byte-identical" test compared two identically-built fixtures to each
// other (`deepEqual(computeFlows(a), computeFlows(b))`), which only proves
// computeFlows is pure — it survives every mutation of the import path. This
// pins the actual claim: the ONLY differences between cover-on and cover-off
// are the Grid Import outflow line and the brownout income scaling.

test('ATTACK: the toggle moves exactly two things — the Grid Import line and powered income', () => {
  const on = computeFlows(shortageCity({ gridImportEnabled: true }));
  const off = computeFlows(shortageCity({ gridImportEnabled: false }));
  const poweredIncome = new Set(['Business Tax', 'Freight Tax', 'Office Tax']);

  const onOut = new Map(on.outflows.map((f) => [f.label, f.value]));
  const offOut = new Map(off.outflows.map((f) => [f.label, f.value]));
  for (const label of new Set([...onOut.keys(), ...offOut.keys()])) {
    if (label === GRID_IMPORT_OUTFLOW_LABEL) continue;
    assert.equal(onOut.get(label), offOut.get(label), `outflow "${label}" must not move with the toggle`);
  }
  assert.equal(offOut.has(GRID_IMPORT_OUTFLOW_LABEL), false, 'cover OFF must not book a Grid Import line');
  assert.ok(onOut.get(GRID_IMPORT_OUTFLOW_LABEL) > 0, 'cover ON must book a Grid Import line');

  const onIn = new Map(on.inflows.map((f) => [f.label, f.value]));
  const offIn = new Map(off.inflows.map((f) => [f.label, f.value]));
  for (const label of new Set([...onIn.keys(), ...offIn.keys()])) {
    if (poweredIncome.has(label)) continue;
    assert.equal(onIn.get(label), offIn.get(label), `inflow "${label}" must not move with the toggle`);
  }
});

// ---------- ATTACK 3: real conservation, not `x === x` ----------
//
// The author's AC-6 headline assertion was
//   `const e = start + in - out; assert.equal(e, start + in - out);`
// which cannot fail. This drives a real tick and proves (a) the ledger
// balances and (b) the Grid Import amount is genuinely inside the debit —
// remove the outflow push from engine.ts and (b) turns RED.

test('ATTACK: a real tick conserves funds AND the Grid Import amount is genuinely in the debit', () => {
  const after = reducer(shortageCity(), { type: 'tick' });
  const inSum = after.lastFlows.inflows.reduce((a, f) => a + f.value, 0);
  const outSum = after.lastFlows.outflows.reduce((a, f) => a + f.value, 0);
  assert.equal(after.fundsAtTickEnd, after.fundsAtTickStart + inSum - outSum, 'ledger must balance');

  const imp = after.lastFlows.outflows.filter((f) => f.label === GRID_IMPORT_OUTFLOW_LABEL);
  assert.equal(imp.length, 1, 'exactly one Grid Import line');
  assert.ok(imp[0].value > 0);

  // The counterfactual: with cover OFF the same city keeps exactly that much
  // more money, minus whatever the brownout income penalty costs it.
  const offAfter = reducer(shortageCity({ gridImportEnabled: false }), { type: 'tick' });
  const offOutSum = offAfter.lastFlows.outflows.reduce((a, f) => a + f.value, 0);
  assert.equal(outSum - offOutSum, imp[0].value, 'the whole outflow delta must be the import line, no more, no less');
});

test('ATTACK: Grid Export and Grid Import can never both fire on the same tick', () => {
  // Mutually exclusive by construction (cap > need vs need > cap), asserted
  // over a sweep so a future refactor that computes them from different
  // sources cannot quietly make both fire.
  for (let plants = 0; plants <= 30; plants++) {
    const s = { ...initialState(), buildings: [], population: 16667 };
    let id = 800001;
    for (let i = 0; i < 10; i++) s.buildings.push({ id: id++, spec: 'com_shop', x: (id % 900) + 5, y: 5 });
    for (let i = 0; i < plants; i++) s.buildings.push({ id: id++, spec: 'pow_wind', x: (id % 900) + 5, y: 9 });
    const f = computeFlows(s);
    const exp = f.inflows.some((x) => x.label === 'Grid Export');
    const imp = f.outflows.some((x) => x.label === GRID_IMPORT_OUTFLOW_LABEL);
    assert.ok(!(exp && imp), `both Grid Export and Grid Import fired at ${plants} plants`);
  }
});

// ---------- ATTACK 4: numeric hygiene ----------

test('ATTACK: gridImportCostPerTick is finite, non-negative and integral for hostile inputs', () => {
  const cases = [
    [0, 0], [0, 1e9], [1e9, 0], [7.5, 9.25], [0.1, 0.2],
    [100, 100], [-5, 5], [Number.MAX_SAFE_INTEGER, 0],
  ];
  for (const [cap, need] of cases) {
    const v = gridImportCostPerTick(cap, need, GRID_IMPORT_TARIFF_PER_MW);
    assert.ok(Number.isFinite(v), `non-finite cost for cap=${cap} need=${need}: ${v}`);
    assert.ok(Number.isInteger(v), `non-integer pounds for cap=${cap} need=${need}: ${v}`);
    assert.ok(v >= 0, `NEGATIVE import cost (a refund!) for cap=${cap} need=${need}: ${v}`);
  }
});

test('ATTACK: a city with zero power buildings imports its whole demand, cleanly', () => {
  const s = { ...initialState(), buildings: [], population: 16667 };
  let id = 700001;
  for (let i = 0; i < 10; i++) s.buildings.push({ id: id++, spec: 'com_shop', x: (id % 900) + 5, y: 5 });
  const pw = powerStats(s);
  assert.equal(pw.cap, 0, 'precondition: no generation at all');
  const f = computeFlows(s);
  const v = flowValue(f, 'outflows', GRID_IMPORT_OUTFLOW_LABEL);
  assert.equal(v, Math.round(pw.need * GRID_IMPORT_TARIFF_PER_MW));
  assert.ok(Number.isInteger(v) && v > 0);
  const after = reducer(s, { type: 'tick' });
  assert.ok(Number.isFinite(after.funds) && Number.isInteger(after.funds), 'funds must survive as an integer');
});

test('ATTACK: an empty city (no buildings, no population) books no Grid Import line', () => {
  const s = { ...initialState(), buildings: [], population: 0 };
  const f = computeFlows(s);
  assert.equal(flowValue(f, 'outflows', GRID_IMPORT_OUTFLOW_LABEL), undefined);
  assert.ok(Number.isFinite(reducer(s, { type: 'tick' }).funds));
});

// ---------- ATTACK 5: verifyGridTariffInvariant actually derives from the catalogue ----------
//
// The author's "AC-4 RED-prove" test built a literal object and asserted
// `1.0 > 2.5 === false` — it never calls the production function at all.
// This calls the real function with synthetic catalogues and proves each leg
// responds to the data (GR#15), including the `allHold` AND.

test('ATTACK: verifyGridTariffInvariant responds to the catalogue, and allHold is never hardcoded', () => {
  const dirt = { id: 'dirt', kind: 'power', mw: 100, cost: 1, upkeep: 0 };
  const gold = { id: 'gold', kind: 'power', mw: 1, cost: 1e12, upkeep: 1e9 };

  const cheap = verifyGridTariffInvariant({ dirt });
  assert.equal(cheap.cheapestPlantId, 'dirt');
  assert.ok(cheap.exportExceedsLocal && cheap.importExceedsLocal, 'a near-free plant must clear both local legs');
  assert.equal(cheap.allHold, true, 'all three legs hold for a near-free catalogue');

  const dear = verifyGridTariffInvariant({ gold });
  assert.equal(dear.cheapestPlantId, 'gold');
  assert.equal(dear.exportExceedsLocal, false, 'an absurdly dear plant must fail the local legs');
  assert.equal(dear.importExceedsLocal, false);
  assert.equal(dear.allHold, false, 'allHold must be the honest AND, never forced true');

  // The minimum is a real minimum across a mixed catalogue.
  assert.equal(verifyGridTariffInvariant({ dirt, gold }).cheapestPlantId, 'dirt');

  // Placeholder / zero-MW / zero-cost specs are excluded, and an empty
  // catalogue degrades to 0 rather than Infinity leaking into a comparison.
  const none = verifyGridTariffInvariant({
    ph: { id: 'ph', kind: 'power', placeholder: true, mw: 10, cost: 10 },
    z: { id: 'z', kind: 'power', mw: 0, cost: 10 },
    notpower: { id: 'notpower', kind: 'commercial', mw: 10, cost: 1 },
  });
  assert.equal(none.cheapestPlantId, null);
  assert.equal(none.cheapestAmortisedPerMwTick, 0);
  assert.ok(Number.isFinite(none.cheapestAmortisedPerMwTick));

  // The derivation formula itself is pinned to the exported horizon constant.
  assert.equal(
    verifyGridTariffInvariant({ dirt }).cheapestAmortisedPerMwTick,
    1 / (100 * POWER_PLANT_AMORTISATION_TICKS)
  );
  assert.equal(GRID_IMPORT_TARIFF_PER_MW > GRID_EXPORT_TARIFF_PER_MW, true);
});

// ---------- TRIPWIRE (r2 FIXED): the wellbeing half of the brownout seam is now covered ----------
//
// r1 BUG (fixed by r2): BUG-393's brownout has TWO limbs: the income penalty
// (engine.ts computeFlows) and the wellbeing penalty (engine.ts wellbeingOf,
// Utilities part × (1 − deficitRatio·BROWNOUT_WELLBEING_K)). inc1 as shipped
// by r1 gated only the FIRST on the toggle, so a city that had bought in its
// entire shortfall still collapsed its Utilities wellbeing part and still
// raised DemandDock's `brownout-banner` — it paid the tariff AND browned out
// anyway.
//
// r2 FIX: introduced data.ts's isBrownoutActive(s) as the ONE single source
// of truth combining the physical deficit (brownoutOf) with the Grid Import
// toggle, and routed income (engine.ts computeFlows), wellbeing (engine.ts
// wellbeingOf), the DemandDock banner, AND the power-row demand-index
// escalation through it (GR#3 — no consumer recomputes the toggle check
// locally any more). brownoutOf() itself stays deliberately toggle-BLIND
// (the ATTACK 1 test above relies on that), so `brownoutOf(on).active` is
// STILL true — that is not the defect; isBrownoutActive(on) reading false is
// the fix. Updated per this file's own note above: "update it to assert the
// covered city's Utilities part matches the surplus twin's."
//
// RED-proven (scratch cp/mv, GR#24): reverting wellbeingOf's gate from
// `isBrownoutActive(s)` back to `brownout.active` makes `util(on)` collapse
// to `util(off)` again and turns this test red.
test('FIXED (was TRIPWIRE): buying in the shortfall now spares the brownout wellbeing penalty', () => {
  const on = shortageCity({ gridImportEnabled: true });
  const off = shortageCity({ gridImportEnabled: false });
  const util = (s) => wellbeingOf(s).parts.find((p) => p.label === 'Utilities').value;

  assert.ok(
    flowValue(computeFlows(on), 'outflows', GRID_IMPORT_OUTFLOW_LABEL) > 0,
    'precondition: the covered city is paying for cover'
  );
  assert.equal(brownoutOf(on).active, true, 'brownoutOf stays toggle-blind: the physical deficit is still real');
  assert.equal(isBrownoutActive(on), false, 'FIXED: the SSOT predicate reads false while cover buys the shortfall in');
  assert.equal(isBrownoutActive(off), true, 'and stays true with cover off — the toggle is a real gate, not dead code');
  // FIXED: the covered city no longer takes the BROWNOUT_WELLBEING_K
  // escalation multiplier — it scores strictly BETTER than the uncovered
  // city (96 vs 90 for this fixture), though not identically to a
  // genuinely-oversupplied twin (100): the base BUG-392 coverage-derived
  // part still reflects local capacity (0.96 of need) — that quality signal
  // is explicitly OUT OF SCOPE for inc1 (Aaron's ruling: price premium
  // only; capacity/quality/satisfaction effects are LATER increments). The
  // wellbeing PENALTY multiplier — the thing this tripwire pinned — is what
  // must (and now does) disappear.
  assert.ok(
    util(on) > util(off),
    'FIXED: the covered city strictly beats the uncovered one, which still browns out'
  );
  assert.ok(
    util(surplusTwin()) > util(on),
    'a genuinely-oversupplied city still scores better than an import-covered one (local coverage, not a brownout penalty)'
  );
});

// ===========================================================================
// DESTRUCTIVE ROUND r2 (Opus, independent) — NEW attack regressions.
// Every one below was written because a mutation of the shipped production
// code SURVIVED the r2 suite, or because an attack-bar item had no guard at
// all. All RED/GREEN proven by scratch cp/mv mutation (never a git revert —
// GR#24). r1's four tautology sites and mutation M9 were re-run first and are
// all genuinely closed; these cover what r2 left unguarded.
// ===========================================================================

import { buildDebugJson, debugJsonText } from '../src/sim/debugjson.ts';
import { EMPTY_MAP_UI } from '../src/sim/uistate.ts';
import { createSavepoint } from '../src/sim/replay.ts';
import { GRID_IMPORT_ENABLED_DEFAULT } from '../src/sim/fiscal.ts';

const debugUi = () => ({
  appVersion: 'v0.0.0-attack',
  frameAtMs: 1_700_000_000_000,
  map: EMPTY_MAP_UI,
  errors: [],
});

// ---------- ATTACK 6: the debug JSON tells the TRUTH about the toggle ----------
//
// GAP: debugjson.ts's `gridImportEnabled: s.gridImportEnabled ?? DEFAULT` had
// no test. Mutation `gridImportEnabled: true` (hardcoded) survived the whole
// r2 suite GREEN. A dogfood debug capture that always claims cover is ON is
// worse than no field — every economy diagnosis read from it would be wrong.

test('r2 ATTACK: debug JSON sim.gridImportEnabled reflects the REAL state, both ways (never hardcoded)', () => {
  const on = buildDebugJson({ ...initialState(), gridImportEnabled: true }, debugUi());
  const off = buildDebugJson({ ...initialState(), gridImportEnabled: false }, debugUi());
  assert.equal(on.sim.gridImportEnabled, true);
  assert.equal(off.sim.gridImportEnabled, false, 'a hardcoded `true` must turn this red');
  assert.notEqual(on.sim.gridImportEnabled, off.sim.gridImportEnabled, 'the field must actually track state');

  // And it must survive JSON serialization (an `undefined` would be dropped).
  const parsed = JSON.parse(debugJsonText(off));
  assert.equal(parsed.sim.gridImportEnabled, false, 'must survive into the emitted text, not be dropped');
});

test('r2 ATTACK: debug JSON reports the documented default for a LEGACY state predating the field', () => {
  const legacy = { ...initialState() };
  delete legacy.gridImportEnabled;
  assert.equal('gridImportEnabled' in legacy, false, 'precondition: the field is genuinely absent');
  const dj = buildDebugJson(legacy, debugUi());
  assert.equal(
    dj.sim.gridImportEnabled,
    GRID_IMPORT_ENABLED_DEFAULT,
    'a legacy state must report the EXPLICIT documented default, never `undefined` or a silent false'
  );
  assert.equal(typeof dj.sim.gridImportEnabled, 'boolean');
});

test('r2 ATTACK: the Grid Import tariff line is visible in the debug capture flows when cover is paying', () => {
  const after = reducer(shortageCity(), { type: 'tick' });
  const dj = buildDebugJson(after, debugUi());
  const line = dj.flows.outflows.find((f) => f.label === GRID_IMPORT_OUTFLOW_LABEL);
  assert.ok(line, 'a dogfood capture must show the Grid Import outflow the city actually paid');
  assert.equal(line.value, after.lastFlows.outflows.find((f) => f.label === GRID_IMPORT_OUTFLOW_LABEL).value);
  assert.equal(dj.sim.gridImportEnabled, true, 'and must agree that cover is ON');
});

// ---------- ATTACK 7: savepoint persistence, both directions + legacy ----------

test('r2 ATTACK: the toggle survives a savepoint JSON round-trip in BOTH states', () => {
  for (const value of [true, false]) {
    const s = shortageCity({ gridImportEnabled: value });
    const sp = JSON.parse(JSON.stringify(createSavepoint(s, [], new Date(0), 'v-attack', null)));
    assert.equal(sp.snapshot.gridImportEnabled, value, `savepoint must preserve gridImportEnabled=${value}`);
    // And the restored snapshot must drive the SAME economy as the live state.
    const live = computeFlows(s).outflows.find((f) => f.label === GRID_IMPORT_OUTFLOW_LABEL);
    const restored = computeFlows(sp.snapshot).outflows.find((f) => f.label === GRID_IMPORT_OUTFLOW_LABEL);
    assert.deepEqual(restored, live, 'a restored savepoint must produce an identical Grid Import position');
  }
});

test('r2 ATTACK: an OLD savepoint with no gridImportEnabled field loads on the EXPLICIT documented default', () => {
  const legacy = shortageCity();
  delete legacy.gridImportEnabled;
  const sp = JSON.parse(JSON.stringify(createSavepoint(legacy, [], new Date(0), 'v-old', null)));
  assert.equal('gridImportEnabled' in sp.snapshot, false, 'precondition: an old save genuinely lacks the field');

  const flows = computeFlows(sp.snapshot);
  const line = flows.outflows.find((f) => f.label === GRID_IMPORT_OUTFLOW_LABEL);
  // The documented default is ON, so an old city buys its shortfall in rather
  // than silently browning out. Pinned against the CONSTANT (GR#15), so if the
  // default is ever retuned this test states the new expectation, and a silent
  // `undefined -> falsy -> off` read (a real economy change) turns it red.
  if (GRID_IMPORT_ENABLED_DEFAULT) {
    assert.ok(line && line.value > 0, 'default ON: an old save must buy the shortfall in, not brown out');
    assert.equal(isBrownoutActive(sp.snapshot), false, 'and must take no brownout consequence');
  } else {
    assert.equal(line, undefined, 'default OFF: an old save must take the legacy brownout path');
    assert.equal(isBrownoutActive(sp.snapshot), true);
  }
  // Either way it must match a state that sets the default EXPLICITLY — no
  // third, silently-different behaviour for the absent-field case.
  const explicit = shortageCity({ gridImportEnabled: GRID_IMPORT_ENABLED_DEFAULT });
  assert.deepEqual(computeFlows(sp.snapshot), computeFlows(explicit));
});

// ---------- ATTACK 8: long-run money integrity across mid-month toggle flips ----------

test('r2 ATTACK: 600 ticks with 4 mid-run toggle flips — exact conservation, integer pounds, never double-charged', () => {
  let s = shortageCity();
  let importTicks = 0;
  let flips = 0;
  for (let t = 0; t < 600; t++) {
    // Keep the city solvent so advance() never short-circuits on decline, and
    // pin population so a real shortfall persists for the whole run.
    s = reducer(s, { type: 'debugFunds', amount: 5_000_000 - s.funds });
    s = { ...s, population: 16667 };
    if (t % 137 === 136) {
      s = reducer(s, { type: 'toggleGridImport' });
      flips++;
    }
    s = reducer(s, { type: 'tick' });

    const inSum = s.lastFlows.inflows.reduce((a, f) => a + f.value, 0);
    const outSum = s.lastFlows.outflows.reduce((a, f) => a + f.value, 0);
    assert.equal(s.fundsAtTickEnd, s.fundsAtTickStart + inSum - outSum, `conservation broke at tick ${t}`);
    assert.ok(Number.isInteger(s.funds), `funds went non-integer at tick ${t}: ${s.funds}`);
    for (const f of [...s.lastFlows.inflows, ...s.lastFlows.outflows]) {
      assert.ok(Number.isInteger(f.value), `float drift in "${f.label}" at tick ${t}: ${f.value}`);
    }

    const gi = s.lastFlows.outflows.filter((f) => f.label === GRID_IMPORT_OUTFLOW_LABEL);
    assert.ok(gi.length <= 1, `Grid Import double-debited at tick ${t}`);
    if (gi.length === 1) {
      importTicks++;
      assert.ok(gi[0].value > 0, `non-positive import charge at tick ${t}`);
      assert.equal(s.gridImportEnabled, true, `import charged while the toggle was OFF at tick ${t}`);
    } else {
      // No import line: either no shortfall, or cover is off.
      assert.ok(
        s.gridImportEnabled === false || powerStats(s).cap >= powerStats(s).need,
        `cover was ON with a real shortfall but nothing was charged at tick ${t}`
      );
    }
  }
  assert.ok(flips >= 4, `the run must actually flip the toggle mid-stream, got ${flips}`);
  assert.ok(importTicks > 100, `the run must actually exercise importing, got ${importTicks} import ticks`);
});
