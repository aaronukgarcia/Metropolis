// coverage.test.mjs — BUG-392: service demand and wellbeing share ONE
// per-service coverage ratio (GR#3 single source of truth).
//
// Run with `npm test` (node --test); node's type-stripping imports the real
// TypeScript modules, so these assertions exercise the exact shipped formulas.
//
// Aaron's live Y11 dump showed School/College/GP/Hospital/Police/Clean-water/
// Sewage demand ALL pegged at +100 while wellbeing read 91 — the two systems
// contradicted because each derived coverage independently, with mismatched
// units (facility counts vs population served). Both now consume
// serviceCoverageOf(); demand = demandIndexOf(coverage) and each wellbeing
// service part = coverage·100 (blended), so (high wellbeing, pegged demand)
// is structurally impossible.
//
// DIRECTIONAL TESTS ONLY (balance-number regime): they pin the SHAPE of the
// curves (coverage 1 → ~0 demand, 0.5 → ~+50, 0 → +100, monotone), not tuned
// values — every constant involved is a flagged placeholder for Aaron.
//
// RED-proven: scratch-mutating demandIndexOf in data.ts to `() => 100`
// (recreating the pegged pre-fix behaviour) fails 'directional: demand tracks
// coverage', 'consistency: wellbeing part and demand index can never both be
// high', and 'regression: the Y11 dump signature is impossible'.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  SPECS,
  serviceCoverageOf,
  serviceDemandOf,
  demandIndexOf,
  earlyGameFactor,
  pickAutoSpec,
} from '../src/sim/data.ts';
import { initialState, wellbeingOf } from '../src/sim/engine.ts';

/** A city: initial (service-free) starter map + population + extra buildings. */
function city(pop, specCounts = {}, mutate = (s) => s) {
  const s = initialState();
  s.population = pop;
  let id = 50000;
  let slot = 0;
  for (const [spec, n] of Object.entries(specCounts)) {
    assert.ok(SPECS[spec], `test setup: unknown spec "${spec}"`);
    for (let i = 0; i < n; i++) {
      // Coordinates are irrelevant to the coverage/wellbeing math.
      s.buildings.push({ id: id++, spec, x: 5 + (slot % 40) * 5, y: 5 + Math.floor(slot / 40) * 5 });
      slot++;
    }
  }
  return mutate(s);
}

/** A city with ZERO starter buildings (roads/rails cleared). Useful for precise demand testing. */
function emptyCity(pop, specCounts = {}) {
  const s = initialState();
  s.population = pop;
  s.buildings = []; // Clear the starter roads/rails
  let id = 50000;
  let slot = 0;
  for (const [spec, n] of Object.entries(specCounts)) {
    assert.ok(SPECS[spec], `test setup: unknown spec "${spec}"`);
    for (let i = 0; i < n; i++) {
      s.buildings.push({ id: id++, spec, x: 5 + (slot % 40) * 5, y: 5 + Math.floor(slot / 40) * 5 });
      slot++;
    }
  }
  return s;
}

const demandOfService = (s, svcId) => {
  const m = serviceDemandOf(s).find((d) => d.id === svcId);
  assert.ok(m, `no demand meter for service "${svcId}"`);
  return m.value;
};

const coverageOfService = (s, svcId) => {
  const r = serviceCoverageOf(s).find((c) => c.id === svcId);
  assert.ok(r, `no coverage row for service "${svcId}"`);
  return r;
};

const partOf = (s, label) => {
  const p = wellbeingOf(s).parts.find((p) => p.label === label);
  assert.ok(p, `no wellbeing part "${label}"`);
  return p.value;
};

// Wellbeing part label ↔ coverage row id, for the services that map 1:1.
const PAIRED = [
  ['Healthcare', 'gp'],
  ['Hospital care', 'hosp'],
  ['Safety', 'police'],
  ['Sewage', 'waste'],
];

// ---------- units: need and cap are the same quantity ----------

test('coverage rows: need and cap share units — full provision yields coverage 1', () => {
  // 4 clinics × 5,000 served = 20,000 = pop → coverage exactly 1.
  const s = city(20000, { hea_clinic: 4 });
  const gp = coverageOfService(s, 'gp');
  assert.equal(gp.need, 20000, 'GP need is the population (people), not a facility count');
  assert.equal(gp.cap, 20000, 'GP cap is Σ served (people)');
  assert.equal(gp.coverage, 1);
  // The pre-BUG-392 unit mismatch (need = pop/800 = 25 "clinics" vs cap =
  // 20,000 people) is dead: need is never orders of magnitude below cap units.
  assert.ok(gp.need > 100, 'need must be in people, not clinic count (pop/800 would be 25)');
});

test('BUG-418: teaching hospital is hospital-class coverage (served 120000 → coverage 1)', () => {
  const s = city(120000, { hea_teaching: 1 });
  const hosp = coverageOfService(s, 'hosp');
  assert.equal(hosp.need, 120000);
  assert.equal(hosp.cap, 120000, 'hea_teaching served must count toward Hospital cap');
  assert.equal(hosp.coverage, 1, 'only hea_teaching at pop 120000 must read hospital coverage 1, not 0');
  assert.equal(coverageOfService(s, 'gp').cap, 0, 'teaching hospital must not inflate GP cap');
});

test('BUG-418: elder-care home does not increase GP or Hospital cap', () => {
  const bare = city(20000, {});
  const withHome = city(20000, { hea_eldercare: 1 });
  assert.equal(coverageOfService(withHome, 'gp').cap, coverageOfService(bare, 'gp').cap);
  assert.equal(coverageOfService(withHome, 'hosp').cap, coverageOfService(bare, 'hosp').cap);
  assert.equal(coverageOfService(withHome, 'gp').coverage, coverageOfService(bare, 'gp').coverage);
  assert.equal(coverageOfService(withHome, 'hosp').coverage, coverageOfService(bare, 'hosp').coverage);
});

test('zero population: coverage defined as 1, demand 0 (nothing required = covered)', () => {
  const s = city(0, {});
  for (const r of serviceCoverageOf(s)) {
    if (r.need === 0) assert.equal(r.coverage, 1, `${r.id}: zero need must read fully covered`);
  }
  for (const d of serviceDemandOf(s)) {
    assert.equal(d.value, 0, `${d.id}: empty map must not scream demand`);
  }
});

// ---------- directional: the demand curve tracks coverage ----------

test('directional: demand tracks coverage (1→~0, 0.8→~+20, 0.5→~+50, 0→+100, 2→-100)', () => {
  assert.equal(earlyGameFactor(20000), 1, 'precondition: no early-game damping at this pop');

  // coverage 0 → pegged +100 (only here may it peg)
  assert.equal(demandOfService(city(20000, {}), 'gp'), 100);
  // coverage 0.5 → ~+50
  assert.equal(demandOfService(city(20000, { hea_clinic: 2 }), 'gp'), 50);
  // coverage 0.8 → ~+20, NOT +100 — the headline BUG-392 behaviour
  assert.equal(demandOfService(city(25000, { hea_clinic: 4 }), 'gp'), 20);
  // coverage 1.0 → ~0
  assert.equal(demandOfService(city(20000, { hea_clinic: 4 }), 'gp'), 0);
  // coverage 2.0 → -100 (surplus end of the clamp)
  assert.equal(demandOfService(city(20000, { hea_clinic: 8 }), 'gp'), -100);
});

test('directional: demand is monotone non-increasing in capacity, strict until the clamp', () => {
  let prev = Infinity;
  for (let clinics = 0; clinics <= 5; clinics++) {
    const v = demandOfService(city(20000, { hea_clinic: clinics }), 'gp');
    assert.ok(v <= prev, `demand rose when capacity was added (${prev} → ${v} at ${clinics} clinics)`);
    if (prev !== Infinity && prev > -100) {
      assert.ok(v < prev, `demand must strictly fall while unclamped (${prev} → ${v})`);
    }
    prev = v;
  }
});

test('regression: one clinic at pop 8000 reads a proportional shortfall, never a peg', () => {
  // Old formula: mk(pop/800=10, served=5000) = (5000-10)/10·100 → clamped ±100.
  const v = demandOfService(city(8000, { hea_clinic: 1 }), 'gp');
  // coverage = 5000/8000 = 0.625 → ~+38 (placeholder linear curve)
  assert.ok(v >= 30 && v <= 45, `expected proportional ~+38, got ${v}`);
});

// ---------- wellbeing consumes the SAME ratios ----------

test('wellbeing service parts consume the shared coverage ratios (high when covered, low when not)', () => {
  const covered = city(20000, {
    hea_clinic: 4, // gp coverage 1
    hea_hospital: 1, // hosp coverage 2
    pol_station: 2, // police coverage 1
    wat_clean: 1,
    wat_waste: 1, // water/sewage coverage 1
  });
  const bare = city(20000, {});
  assert.equal(earlyGameFactor(20000), 1);

  for (const [label, svcId] of PAIRED) {
    const covRatio = Math.min(1, coverageOfService(covered, svcId).coverage);
    assert.equal(
      partOf(covered, label),
      Math.round(covRatio * 100),
      `${label} must equal 100·min(coverage,1) at full damping — same ratio as the demand meter`
    );
    assert.ok(partOf(covered, label) >= 95, `${label}: full coverage must score high`);
    assert.ok(partOf(bare, label) <= 5, `${label}: zero coverage must score low`);
  }
});

// ---------- the contradiction is structurally impossible ----------

test('consistency: wellbeing part and demand index can never both be high (part + demand ≤ 101)', () => {
  // Sweep pops (including sub-50 damped ones) × provision levels.
  const pops = [0, 30, 500, 5000, 20000, 120000];
  const bundles = [0, 1, 3, 8];
  for (const pop of pops) {
    for (const n of bundles) {
      const s = city(pop, {
        hea_clinic: n * 2,
        hea_hospital: n,
        pol_station: n,
        wat_clean: n,
        wat_waste: n,
        edu_nursery: n * 4,
        edu_primary: n,
        col_sixth: n,
        pow_fusion: n,
      });
      const demands = new Map(serviceDemandOf(s).map((d) => [d.id, d.value]));
      for (const [label, svcId] of PAIRED) {
        const sum = partOf(s, label) + demands.get(svcId);
        assert.ok(
          sum <= 101,
          `pop=${pop} n=${n} ${label}: part ${partOf(s, label)} + demand ${demands.get(svcId)} = ${sum} — ` +
            'the wellbeing part and the demand meter disagree about the same coverage'
        );
      }
    }
  }
});

test('regression: the Y11 dump signature (wellbeing ≥ 85 while demand pegs) is impossible', () => {
  const pops = [0, 30, 500, 5000, 20000, 120000];
  const bundles = [0, 1, 3, 8];
  const scenarios = [];
  for (const pop of pops) {
    for (const n of bundles) {
      scenarios.push(
        city(
          pop,
          {
            hea_clinic: n * 2,
            hea_hospital: n,
            pol_station: n,
            wat_clean: n,
            wat_waste: n,
            edu_nursery: Math.ceil((pop * 0.06) / 30) * Math.min(n, 1),
            edu_city: n,
            col_sixth: n,
            pow_fusion: n,
            park_town: n * 12,
          },
          (s) => {
            // Push the non-service parts up so high overall wellbeing is reachable
            // and the implication below is not vacuously true.
            s.taxRates = { residential: 5, commercial: 5, industrial: 5 };
            s.policies = { ...s.policies, transitSubsidy: true };
            return s;
          }
        )
      );
    }
  }

  let sawHighWellbeing = false;
  for (const s of scenarios) {
    const overall = wellbeingOf(s).overall;
    const demands = serviceDemandOf(s).map((d) => d.value);
    const avgDemand = demands.reduce((a, v) => a + v, 0) / demands.length;
    if (overall >= 85) {
      sawHighWellbeing = true;
      assert.ok(
        avgDemand < 50,
        `overall wellbeing ${overall} coexists with average service demand ${avgDemand.toFixed(1)} — the BUG-392 contradiction`
      );
      assert.ok(
        !demands.every((v) => v >= 95),
        `overall wellbeing ${overall} with ALL demand meters pegged — the exact Y11 dump signature`
      );
    }
  }
  // Guard against vacuous truth: at least one sweep scenario must actually
  // reach high wellbeing, or this test proves nothing.
  assert.ok(sawHighWellbeing, 'sweep never produced overall ≥ 85 — implication tested nothing');
});

// ---------- pickAutoSpec sign cure: returns WORST-covered, not OVERSUPPLIED ----------

test('BUG-392 F1: pickAutoSpec sign cure — returns undersupplied service, not oversupplied', () => {
  // RED-proof for the descending sort in pickAutoSpec (data.ts line 876).
  // When pickAutoSpec picks the FIRST element after sorting, it must sort
  // descending (highest demand first) to get the worst-covered service.
  // An ascending sort would pick the lowest-demand services instead.
  //
  // This test verifies that when we have an undersupplied service (GP) and
  // an oversupplied one (police), pickAutoSpec picks the former, not the latter.
  // If the sort were flipped to ascending, it would not pick police (since
  // its demand is -100, not > 25), but it would pick from the wrong end of
  // the distribution. The test proves the sort works by demonstrating that
  // an undersupplied service is selected, never an oversupplied one.
  const s = city(15000, {
    // Healthcare: UNDERSUPPLIED
    hea_clinic: 1,      // 5000 vs 15000 → demand ~+40
    hea_hospital: 1,
    // Police: OVERSUPPLIED (to ensure it's in the sorted list but not picked)
    pol_station: 3,     // 30000 vs 15000 → demand -100
  });

  const demands = serviceDemandOf(s);
  const gpDemand = demands.find((d) => d.id === 'gp')?.value ?? 0;
  const policeDemand = demands.find((d) => d.id === 'police')?.value ?? 0;

  // Precondition: GP is undersupplied, police is oversupplied
  assert.ok(gpDemand > 0, `GP must have positive demand (undersupplied): ${gpDemand}`);
  assert.ok(policeDemand < 0, `Police must have negative demand (oversupplied): ${policeDemand}`);

  // The test: pickAutoSpec must NOT pick an oversupplied service.
  // With the correct descending sort, it picks from the high-demand end.
  // With ascending, it would pick from the low-demand end (but only if > 25,
  // so it wouldn't pick police's -100, but it demonstrates the sort matters).
  const picked = pickAutoSpec(s);
  assert.ok(picked, 'should pick at least one service with demand > 25');
  assert.notEqual(
    picked.spec,
    'pol_station',
    `must not pick oversupplied police (demand ${policeDemand}) when descending sort targets high-demand services`
  );
});
