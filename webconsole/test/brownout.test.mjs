// brownout.test.mjs — BUG-393: power deficit must escalate the demand index
// sharply AND carry a real consequence (brownout mechanic).
//
// Run with `npm test` (node --test type-strips the .ts imports, so these
// exercise the exact shipped sim code).
//
// Background (Aaron's live dump): power need 10,592 MW vs capacity 10,185 —
// a 4% DEFICIT — yet the demand index read a ±4 wiggle and nothing anywhere
// consumed the deficit. These tests pin the fix:
//   INDEX      any deficit floors the power demand index at
//              BROWNOUT_INDEX_FLOOR (+50) and climbs steeply with deficitRatio.
//   INCOME     commercial/industrial/office inflows scale by incomeFactor.
//   WELLBEING  the Utilities part collapses with deficitRatio.
//   UI STATE   the power service entry carries alert:true (drives the
//              DemandDock banner + row highlight).
// All weights are PLACEHOLDER (balance-number regime) — tests assert
// DIRECTION and derive exact expectations from the same runtime helpers,
// never from hardcoded balance numbers (GR#15).
//
// MERGED with BUG-392 (lane/bug392): the NO-deficit power mapping now rides
// the shared serviceCoverageOf/demandIndexOf curve (surplus reads negative);
// the deficit branch escalates from the same coverage row (deficitRatio =
// 1 - coverage = 1 - cap/need), so these directional expectations are
// unchanged: any deficit >= +50, 50% deficit pegs +100, monotone.
//
// RED proof (2026-08-26, scratch-copy method per GR#24a): with the brownout
// income block scratch-removed from computeFlows, "4% deficit: powered
// business income takes a nonzero reduction" fails with
// `AssertionError: 44 < 44` — so the consequence tests genuinely detect the
// mechanic's absence, not merely their own arithmetic.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, computeFlows, wellbeingOf, utilitiesWellbeingUnpenalized } from '../src/sim/engine.ts';
import {
  SPECS,
  powerStats,
  brownoutOf,
  serviceDemandOf,
  BROWNOUT_INDEX_FLOOR,
  BROWNOUT_WELLBEING_K,
} from '../src/sim/data.ts';
import { businessTaxPerTick } from '../src/sim/fiscal.ts';

/**
 * Deterministic test city: 10 shops (income), 1 water works (so the
 * Utilities wellbeing part is nonzero), and a chosen power fleet.
 * Population 16,667 -> powerStats.need = round(16667 * 0.012) = 200 MW
 * (no industrial/office/mine, so nothing else feeds need).
 */
function city(power = {}) {
  const buildings = [];
  let id = 1;
  const put = (spec) => buildings.push({ id: id++, spec, x: id * 3, y: 0 });
  for (let i = 0; i < 10; i++) put('com_shop');
  put('wat_clean');
  for (const [spec, n] of Object.entries(power)) for (let i = 0; i < n; i++) put(spec);
  // FEAT-2326609711 inc1 (AC-12 regression pin): initialState() now defaults
  // gridImportEnabled to TRUE (Design Ruling — new cities start on external
  // cover), which would otherwise SKIP the legacy brownout income penalty
  // these fixtures exist to prove (engine.ts computeFlows gates it on
  // `!gridImportEnabled`). Explicitly disable the toggle here so this
  // pre-existing suite keeps testing the LEGACY shortage path unchanged
  // (byte-identical results) — exactly per AC-12's own text: "The test
  // fixture explicitly sets gridImportEnabled = false ... before verifying
  // brownout." RED proof: with this line removed, every income/wellbeing
  // deficit assertion below turns red because Grid Import silently covers
  // the shortfall instead of browning out.
  return { ...initialState(), population: 16667, buildings, gridImportEnabled: false };
}

// Fleet capacities derive from the spec catalogue at runtime, never inline.
const SURPLUS = () => city({ pow_coal: 3 }); //  3*80 = 240 MW >= 200: no deficit
const DEFICIT_4PC = () => city({ pow_wind: 24 }); // 24*8 = 192 MW: 4% deficit
const DEFICIT_50PC = () => city({ pow_solar: 4 }); //  4*25 = 100 MW: 50% deficit
const RESTORED = () => city({ pow_wind: 24, pow_coal: 1 }); // 272 MW: recovered

/** The UN-scaled Business Tax the flows formula produces, derived from the
 * same state the sim reads (GR#15) — via the SAME SSOT function computeFlows
 * uses (fiscal.ts's businessTaxPerTick), never a duplicated/hardcoded
 * coefficient (BUG-452 inc1 rebased businessTaxFraction — a locally-inlined
 * 0.4 here would have silently drifted, exactly the GR#3 class of bug). */
function unscaledBusinessTax(s) {
  const shops = s.buildings.filter((b) => SPECS[b.spec].kind === 'commercial').length;
  return businessTaxPerTick(shops, s.taxRates.commercial);
}

const businessTax = (s) =>
  computeFlows(s).inflows.find((f) => f.label === 'Business Tax')?.value;
const powerEntry = (s) => serviceDemandOf(s).find((m) => m.id === 'power');
const utilitiesPart = (s) =>
  wellbeingOf(s).parts.find((p) => p.label === 'Utilities')?.value;

// ---------- preconditions: the fixture cities are what they claim ----------

test('fixture sanity: fleets produce the intended deficit ratios', () => {
  const pw = powerStats(DEFICIT_4PC());
  assert.ok(pw.need > pw.cap, 'DEFICIT_4PC must actually be in deficit');
  const r4 = brownoutOf(DEFICIT_4PC()).deficitRatio;
  assert.ok(Math.abs(r4 - 0.04) < 1e-9, `expected exactly a 4% deficit, got ${r4}`);
  const r50 = brownoutOf(DEFICIT_50PC()).deficitRatio;
  assert.ok(Math.abs(r50 - 0.5) < 1e-9, `expected exactly a 50% deficit, got ${r50}`);
  // deficitRatio is consistent with powerStats (SSOT, GR#3)
  assert.equal(r4, 1 - pw.cap / pw.need);
});

// ---------- no deficit -> no effect ----------

test('no deficit: brownout inactive, income unscaled, no alert', () => {
  const s = SURPLUS();
  const bo = brownoutOf(s);
  assert.deepEqual(bo, { active: false, deficitRatio: 0, incomeFactor: 1 });
  assert.equal(businessTax(s), unscaledBusinessTax(s), 'income must be untouched');
  const p = powerEntry(s);
  assert.ok(!p.alert, 'no deficit must not raise the brownout alert');
  assert.ok(p.value <= 100, 'surplus stays on the ordinary (BUG-392 seam) scale');
});

// ---------- INDEX: deficit escalates sharply ----------

test('4% deficit: demand index floors at +50 (not a +/-4 wiggle)', () => {
  const p = powerEntry(DEFICIT_4PC());
  assert.ok(
    p.value >= BROWNOUT_INDEX_FLOOR,
    `a real deficit must read >= +${BROWNOUT_INDEX_FLOOR}, got ${p.value}`
  );
  assert.equal(p.alert, true, 'deficit must raise the alert flag for the UI');
});

test('50% deficit: index pegs at +100 and exceeds the 4% reading (monotonic)', () => {
  const small = powerEntry(DEFICIT_4PC()).value;
  const big = powerEntry(DEFICIT_50PC()).value;
  assert.equal(big, 100, 'a severe deficit must peg the index');
  assert.ok(big >= small, `escalation must be monotonic: ${big} vs ${small}`);
});

// ---------- CONSEQUENCE: income ----------

test('4% deficit: powered business income takes a nonzero reduction', () => {
  const s = DEFICIT_4PC();
  const base = unscaledBusinessTax(s);
  const got = businessTax(s);
  assert.ok(got < base, `Business Tax must drop under brownout: ${got} < ${base}`);
  assert.equal(
    got,
    Math.round(base * brownoutOf(s).incomeFactor),
    'reduction must equal the SSOT incomeFactor'
  );
});

test('50% deficit: strong income effect, stronger than the 4% case', () => {
  const strong = businessTax(DEFICIT_50PC());
  const mild = businessTax(DEFICIT_4PC());
  const base = unscaledBusinessTax(DEFICIT_50PC());
  assert.ok(strong < mild, `50% deficit must cost more than 4%: ${strong} < ${mild}`);
  assert.equal(strong, Math.round(base * brownoutOf(DEFICIT_50PC()).incomeFactor));
});

// ---------- CONSEQUENCE: wellbeing ----------

test('wellbeing Utilities part drops with deficit and drops harder at 50%', () => {
  const healthy = utilitiesPart(RESTORED());
  const mild = utilitiesPart(DEFICIT_4PC());
  const severe = utilitiesPart(DEFICIT_50PC());
  assert.ok(mild < healthy, `4% deficit must dent Utilities: ${mild} < ${healthy}`);
  assert.ok(severe < mild, `50% deficit must be far worse: ${severe} < ${mild}`);
});

test('BUG-393 F2: Utilities wellbeing is strictly less than unpenalized value when brownout active', () => {
  // Create a deficit state (4% power shortfall)
  const s = DEFICIT_4PC();
  const bo = brownoutOf(s);
  assert.ok(bo.active, 'precondition: DEFICIT_4PC must be in brownout');

  // Calculate both the actual (penalized) and unpenalized Utilities wellbeing values
  const unpenalized = utilitiesWellbeingUnpenalized(s);
  const actual = utilitiesPart(s);

  // The brownout penalty must reduce the Utilities part: actual < unpenalized
  assert.ok(
    actual < unpenalized,
    `with brownout active, Utilities must be penalized below unpenalized: ${actual} < ${unpenalized}`
  );

  // Verify the reduction follows the formula: unpenalized * (1 - deficitRatio * K)
  const expectedFactor = 1 - bo.deficitRatio * BROWNOUT_WELLBEING_K;
  const expectedActual = Math.max(0, Math.round(unpenalized * expectedFactor));
  assert.equal(
    actual,
    expectedActual,
    `actual must equal the formula-derived value: ${actual} should be ${expectedActual}`
  );

  // No-deficit state must NOT apply the penalty
  const healthy = RESTORED();
  assert.equal(
    utilitiesPart(healthy),
    utilitiesWellbeingUnpenalized(healthy),
    'without deficit, actual must equal unpenalized'
  );
});

// ---------- RECOVERY: effects vanish when capacity is restored ----------

test('capacity restored: brownout inactive, income back to unscaled, no alert', () => {
  const s = RESTORED();
  assert.equal(brownoutOf(s).active, false);
  assert.equal(businessTax(s), unscaledBusinessTax(s));
  assert.ok(!powerEntry(s).alert);
});

// ---------- determinism ----------

test('brownout state and demand entries are deterministic (no randomness)', () => {
  const s = DEFICIT_4PC();
  assert.deepEqual(brownoutOf(s), brownoutOf(s));
  assert.deepEqual(serviceDemandOf(s), serviceDemandOf(s));
  assert.deepEqual(computeFlows(s), computeFlows(s));
});
