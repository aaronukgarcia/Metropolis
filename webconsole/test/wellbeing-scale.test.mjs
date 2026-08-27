// wellbeing-scale.test.mjs — BUG-415: wellbeing tracks collapse to 0 at high population.
//
// Debug snapshots show Healthcare=0, Hospital care=0, Safety=0, Parks & leisure=0
// even though services exist and demand is 100. At earlier lower population,
// Healthcare was 14-16 and Hospital care was 100.
//
// Hypotheses:
// (a) coverage ratio → 0 because capacity doesn't scale with population and there's
//     an integer rounding floor that doesn't prevent 0
// (b) a divide-by-zero or NaN lurking in the coverage calc
// (c) the service lookup is disconnected at scale
// (d) 0 is CORRECT: services are genuinely under-provisioned at 300k population
//
// This test suite sweeps LOW vs HIGH population with PROPORTIONAL service provision
// and asserts wellbeing tracks DON'T discontinuously collapse to 0.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  SPECS,
  serviceCoverageOf,
  earlyGameFactor,
} from '../src/sim/data.ts';
import { initialState, wellbeingOf } from '../src/sim/engine.ts';

/** A city: initial state + population + extra buildings. */
function city(pop, specCounts = {}) {
  const s = initialState();
  s.population = pop;
  let id = 50000;
  let slot = 0;
  for (const [spec, n] of Object.entries(specCounts)) {
    assert.ok(SPECS[spec], `test setup: unknown spec "${spec}"`);
    for (let i = 0; i < n; i++) {
      // Coordinates don't affect coverage/wellbeing math.
      s.buildings.push({ id: id++, spec, x: 5 + (slot % 40) * 5, y: 5 + Math.floor(slot / 40) * 5 });
      slot++;
    }
  }
  return s;
}

test('BUG-415: wellbeing at LOW population with proportional services', () => {
  // Small city: 10k pop, 1 clinic (proportionally adequate)
  const s = city(10000, {
    hea_clinic: 1,     // 5000 served vs 10k pop → coverage 0.5
    hea_hospital: 1,   // 5000 served vs 10k pop → coverage 0.5
    pol_station: 1,    // 5000 served vs 10k pop → coverage 0.5
    park_town: 2,      // 80 per park vs 10k pop
    wat_clean: 1,
    wat_waste: 1,
  });

  const wb = wellbeingOf(s);
  console.log('LOW pop scenario (10k pop):', JSON.stringify(wb.parts, null, 2));

  // With early-game damping (pop/50 = 0.2 at pop 10k doesn't fully apply; f = 1 at 50k).
  // At pop 10k, f = min(1, 10000/50) = 1 (full damping).
  // coverage 0.5 → 50 before blend → after blend = 50 (no damping effect).
  // Even with damping, we should not see 0.

  const healthcare = wb.parts.find((p) => p.label === 'Healthcare');
  const hospital = wb.parts.find((p) => p.label === 'Hospital care');
  const safety = wb.parts.find((p) => p.label === 'Safety');

  assert.ok(healthcare.value > 0, `Healthcare (coverage 0.5) should be > 0, got ${healthcare.value}`);
  assert.ok(hospital.value > 0, `Hospital care (coverage 0.5) should be > 0, got ${hospital.value}`);
  assert.ok(safety.value > 0, `Safety (coverage 0.5) should be > 0, got ${safety.value}`);
});

test('BUG-415: wellbeing at HIGH population with PROPORTIONAL services (same ratio as low)', () => {
  // Large city: 300k pop, 60 clinics (proportional to the 1 clinic per 10k above).
  // So coverage should be IDENTICAL: 60 clinics × 5000 = 300k served vs 300k pop → coverage 1.
  // But the test case earlier had Healthcare=0 at high pop even though services existed.
  // So let's test with slightly sub-proportional: 50 clinics vs 300k → coverage ~0.83.
  const s = city(300000, {
    hea_clinic: 50,     // 250k served vs 300k pop → coverage 0.833
    hea_hospital: 10,   // 50k served vs 300k pop → coverage 0.167
    pol_station: 50,    // 250k served vs 300k pop → coverage 0.833
    park_town: 100,     // 4000 park capacity vs 300k pop
    wat_clean: 50,
    wat_waste: 50,
  });

  const wb = wellbeingOf(s);
  console.log('HIGH pop scenario (300k pop):', JSON.stringify(wb.parts, null, 2));

  // At pop 300k, f = min(1, 300000/50) = 1 (full damping).
  // coverage 0.833 → ~83 before blend (clampN(83.3, 0, 100) = 83) → after blend = 83.
  // coverage 0.167 → ~17 before blend → after blend = 17.
  // coverage 0.833 (police) → ~83.

  const healthcare = wb.parts.find((p) => p.label === 'Healthcare');
  const hospital = wb.parts.find((p) => p.label === 'Hospital care');
  const safety = wb.parts.find((p) => p.label === 'Safety');

  console.log('Healthcare value:', healthcare.value);
  console.log('Hospital care value:', hospital.value);
  console.log('Safety value:', safety.value);

  assert.ok(healthcare.value > 0, `Healthcare at high pop should be > 0, got ${healthcare.value}`);
  assert.ok(hospital.value > 0, `Hospital care at high pop should be > 0, got ${hospital.value}`);
  assert.ok(safety.value > 0, `Safety at high pop should be > 0, got ${safety.value}`);

  // More specific: coverage 0.833 should yield part >= 75 (clampN(83, 0, 100) = 83, blend(83) = 83 at f=1)
  assert.ok(
    healthcare.value >= 75,
    `Healthcare with coverage 0.833 should be >= 75, got ${healthcare.value}`
  );
  assert.ok(
    safety.value >= 75,
    `Safety with coverage 0.833 should be >= 75, got ${safety.value}`
  );
});

test('BUG-415: wellbeing does NOT discontinuously collapse when scaling from 10k to 300k with same ratios', () => {
  // Low pop: 10k, 2 clinics → coverage 1.0 (10k served / 10k pop)
  const sLow = city(10000, {
    hea_clinic: 2,
    hea_hospital: 1,
    pol_station: 2,
    park_town: 2,
    wat_clean: 2,
    wat_waste: 2,
  });

  // High pop: 300k, 60 clinics → coverage 1.0 (300k served / 300k pop = same ratio as low)
  const sHigh = city(300000, {
    hea_clinic: 60,
    hea_hospital: 30,
    pol_station: 60,
    park_town: 60,
    wat_clean: 60,
    wat_waste: 60,
  });

  const wbLow = wellbeingOf(sLow);
  const wbHigh = wellbeingOf(sHigh);

  const getLabel = (parts, label) => parts.find((p) => p.label === label).value;

  const healthcare_low = getLabel(wbLow.parts, 'Healthcare');
  const healthcare_high = getLabel(wbHigh.parts, 'Healthcare');
  const hospital_low = getLabel(wbLow.parts, 'Hospital care');
  const hospital_high = getLabel(wbHigh.parts, 'Hospital care');
  const safety_low = getLabel(wbLow.parts, 'Safety');
  const safety_high = getLabel(wbHigh.parts, 'Safety');

  console.log(`Healthcare: low=${healthcare_low} high=${healthcare_high}`);
  console.log(`Hospital care: low=${hospital_low} high=${hospital_high}`);
  console.log(`Safety: low=${safety_low} high=${safety_high}`);

  // At SAME proportional coverage, wellbeing should NOT collapse to 0.
  assert.ok(
    healthcare_high > 0,
    `Healthcare collapses to 0 at high pop (was ${healthcare_low} at low pop)`
  );
  assert.ok(
    hospital_high > 0,
    `Hospital care collapses to 0 at high pop (was ${hospital_low} at low pop)`
  );
  assert.ok(
    safety_high > 0,
    `Safety collapses to 0 at high pop (was ${safety_low} at low pop)`
  );

  // Rough bounds: at same coverage ratio, parts should be within ±10% of each other.
  // (small drift due to earlyGameFactor blend, but not a discontinuous collapse)
  assert.ok(
    Math.abs(healthcare_high - healthcare_low) < 15,
    `Healthcare drift too large: ${healthcare_low} → ${healthcare_high}`
  );
  assert.ok(
    Math.abs(hospital_high - hospital_low) < 15,
    `Hospital care drift too large: ${hospital_low} → ${hospital_high}`
  );
});

test('BUG-415: genuinely unprovisioned service DOES read low at any population', () => {
  // NO clinics at any population.
  const sLow = city(10000, {
    hea_clinic: 0,
    hea_hospital: 0,
  });
  const sHigh = city(300000, {
    hea_clinic: 0,
    hea_hospital: 0,
  });

  const wbLow = wellbeingOf(sLow);
  const wbHigh = wellbeingOf(sHigh);

  const getLabel = (parts, label) => parts.find((p) => p.label === label)?.value ?? null;
  const healthcare_low = getLabel(wbLow.parts, 'Healthcare');
  const hospital_low = getLabel(wbLow.parts, 'Hospital care');
  const healthcare_high = getLabel(wbHigh.parts, 'Healthcare');
  const hospital_high = getLabel(wbHigh.parts, 'Hospital care');

  console.log(`No clinics: low=${healthcare_low}/${hospital_low} high=${healthcare_high}/${hospital_high}`);

  // With ZERO coverage, parts should indeed be low (not high).
  // At coverage 0, part = blend(0) = 0·f + 55·(1-f). At high pop, f=1 → blend(0) = 0.
  assert.ok(healthcare_low <= 5, `Healthcare with no clinics should be ~0, got ${healthcare_low}`);
  assert.ok(healthcare_high <= 5, `Healthcare with no clinics should be ~0, got ${healthcare_high}`);
});

test('BUG-415: coverage ratio calculation does not produce NaN or Infinity', () => {
  // Test various population and provision levels to ensure coverage is always finite.
  const populations = [1, 10, 100, 1000, 10000, 100000, 300000];
  const clinicCounts = [0, 1, 2, 5, 10, 50, 100];

  for (const pop of populations) {
    for (const clinics of clinicCounts) {
      const s = city(pop, { hea_clinic: clinics });
      const coverage = serviceCoverageOf(s);
      const gpRow = coverage.find((r) => r.id === 'gp');

      assert.ok(
        Number.isFinite(gpRow.coverage),
        `GP coverage at pop=${pop}, clinics=${clinics} is not finite: ${gpRow.coverage}`
      );
      assert.ok(
        !Number.isNaN(gpRow.coverage),
        `GP coverage at pop=${pop}, clinics=${clinics} is NaN`
      );
      assert.ok(
        gpRow.coverage >= 0,
        `GP coverage at pop=${pop}, clinics=${clinics} is negative: ${gpRow.coverage}`
      );

      // Now check wellbeing doesn't produce NaN either.
      const wb = wellbeingOf(s);
      for (const part of wb.parts) {
        assert.ok(
          Number.isFinite(part.value),
          `Wellbeing part '${part.label}' at pop=${pop}, clinics=${clinics} is not finite: ${part.value}`
        );
        assert.ok(
          part.value >= 0 && part.value <= 100,
          `Wellbeing part '${part.label}' at pop=${pop}, clinics=${clinics} is out of [0,100]: ${part.value}`
        );
      }
    }
  }
});
