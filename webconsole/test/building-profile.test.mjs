// building-profile.test.mjs — FEAT-1972079866: per-object economic profile.
//
// Exercises the pure buildingProfile(spec, taxRates?) helper the selected-
// building card renders from. Node's type-stripping imports the real .ts helper
// so the test covers the exact code the UI ships (no reimplementation, no drift).
//
// Coverage:
//   - residential: capex=cost, opex=upkeep, PRODUCES residents + council-tax cash
//   - power plant: PRODUCES power (mw), REQUIRES nothing, capex/opex correct
//   - commercial: PRODUCES business-tax cash; office: PRODUCES jobs
//   - a consumer carrying mw/water draw: shows them under REQUIRES
//   - revenue with no clean fiscal helper -> capability (value === null), never invented
//   - determinism: same inputs -> identical profile

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  buildingProfile,
  specClass,
  specClassLabel,
  buildingCopyPayload,
} from '../src/sim/profile.ts';
import { SPECS } from '../src/sim/data.ts';
import { councilTaxPerTick, businessTaxPerTick } from '../src/sim/fiscal.ts';

const TAX = { residential: 100, commercial: 100, industrial: 100 };

const find = (lines, key) => lines.find((l) => l.key === key);

test('residential: capex=cost, opex=upkeep, PRODUCES residents + council tax', () => {
  const sp = SPECS.res_hut;
  const p = buildingProfile(sp, TAX);
  assert.equal(p.capex, sp.cost);
  assert.equal(p.opex, sp.upkeep);

  const residents = find(p.produces, 'residents');
  assert.ok(residents, 'residents line present under PRODUCES');
  assert.equal(residents.value, sp.residents);

  const council = find(p.produces, 'councilTax');
  assert.ok(council, 'council tax line present under PRODUCES');
  // Derived via the EXISTING fiscal helper, not invented.
  assert.equal(council.value, councilTaxPerTick(sp.residents, TAX.residential));
  assert.equal(council.unit, 'moneyPerTick');

  // No inputs for a plain dwelling.
  assert.equal(p.requires.length, 0);
});

test('residential without taxRates: council tax degrades to a capability (null, not invented)', () => {
  const p = buildingProfile(SPECS.res_hut);
  const council = find(p.produces, 'councilTax');
  assert.ok(council);
  assert.equal(council.value, null);
});

test('power plant: PRODUCES power (mw), REQUIRES nothing, capex/opex correct', () => {
  for (const id of ['pow_wind', 'pow_nuke']) {
    const sp = SPECS[id];
    const p = buildingProfile(sp, TAX);
    assert.equal(p.capex, sp.cost);
    assert.equal(p.opex, sp.upkeep);
    const power = find(p.produces, 'power');
    assert.ok(power, `${id} produces power`);
    assert.equal(power.value, sp.mw);
    assert.equal(power.unit, 'power');
    // A plant draws no modelled inputs.
    assert.equal(p.requires.length, 0, `${id} requires nothing`);
  }
});

test('commercial: PRODUCES business tax via the fiscal helper', () => {
  const sp = SPECS.com_shop;
  const p = buildingProfile(sp, TAX);
  assert.equal(p.capex, sp.cost);
  assert.equal(p.opex, sp.upkeep);
  const biz = find(p.produces, 'businessTax');
  assert.ok(biz, 'business tax line present');
  assert.equal(biz.value, businessTaxPerTick(1, TAX.commercial));
});

test('office: PRODUCES jobs; office tax shown as capability (no invented number)', () => {
  const sp = SPECS.off_suite;
  const p = buildingProfile(sp, TAX);
  const jobs = find(p.produces, 'jobs');
  assert.ok(jobs, 'jobs line present under PRODUCES');
  assert.equal(jobs.value, sp.jobs);
  // Workers to staff the jobs appear under REQUIRES.
  const workers = find(p.requires, 'workers');
  assert.ok(workers, 'workers line present under REQUIRES');
  assert.equal(workers.value, sp.jobs);
  // Office tax has no clean per-building fiscal helper -> capability only.
  const officeTax = find(p.produces, 'officeTax');
  assert.ok(officeTax);
  assert.equal(officeTax.value, null);
});

test('a consumer with power + water draw shows them under REQUIRES', () => {
  // Synthetic non-plant spec that carries an mw draw and a water field. The
  // helper is pure over any Spec, so this proves the REQUIRES routing without
  // editing data.ts (no real placeholder spec carries these inputs today).
  const consumer = { ...SPECS.hea_clinic, mw: 5, water: 120 };
  const p = buildingProfile(consumer, TAX);
  const power = find(p.requires, 'power');
  assert.ok(power, 'power draw under REQUIRES');
  assert.equal(power.value, 5);
  const water = find(p.requires, 'water');
  assert.ok(water, 'water draw under REQUIRES');
  assert.equal(water.value, 120);
  // Non-plant mw must NOT appear as produced power.
  assert.equal(find(p.produces, 'power'), undefined);
});

test('determinism: same spec + rates -> identical profile', () => {
  const a = buildingProfile(SPECS.res_block, TAX);
  const b = buildingProfile(SPECS.res_block, TAX);
  assert.deepEqual(a, b);
});

// ── CLASS-TYPE NUMBER (NNNN.L) ───────────────────────────────────────────────

test('specClassLabel: stable + deterministic, zero-padded NNNN, .L == spec level', () => {
  const sp = SPECS.res_hut;
  const first = specClassLabel(sp);
  // Stable/deterministic: identical over many calls.
  for (let i = 0; i < 100; i++) {
    assert.equal(specClassLabel(sp), first, 'label is stable across calls');
  }
  // Format NNNN.L
  const m = /^(\d{4})\.(\d+)$/.exec(first);
  assert.ok(m, `label ${first} matches NNNN.L`);
  // NNNN is the class number zero-padded to 4 digits.
  assert.equal(m[1], String(specClass(sp)).padStart(4, '0'));
  // .L equals the spec's level (the `unlock` field).
  assert.equal(Number(m[2]), sp.unlock);
});

test('specClass: two different specs -> different class numbers', () => {
  assert.notEqual(specClass(SPECS.res_hut), specClass(SPECS.res_block));
  assert.notEqual(specClassLabel(SPECS.pow_wind), specClassLabel(SPECS.pow_nuke));
  // Every spec id in the catalogue maps to a distinct class number.
  const ids = Object.keys(SPECS);
  const nums = ids.map((id) => specClass(SPECS[id]));
  assert.equal(new Set(nums).size, ids.length, 'class numbers are unique per type');
});

// ── COPY-AS-JSON PAYLOAD ─────────────────────────────────────────────────────

test('buildingCopyPayload: includes id, spec, class, level, and profile', () => {
  const sp = SPECS.res_block;
  const building = { id: 44, spec: 'res_block' };
  const payload = buildingCopyPayload(building, sp, TAX);
  assert.equal(payload.id, 44);
  assert.equal(payload.spec, 'res_block');
  assert.equal(payload.class, specClassLabel(sp));
  assert.equal(payload.level, sp.unlock);
  assert.deepEqual(payload.profile, buildingProfile(sp, TAX));
  // Round-trips through JSON (what the Copy button actually writes).
  const round = JSON.parse(JSON.stringify(payload));
  assert.deepEqual(round, payload);
});
