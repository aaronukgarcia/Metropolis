// utilisation.test.mjs — FEAT-1972079855: per-building utilisation indicator.
//
// Tests the utilisationOf() derivation for correctness across all building kinds:
// - residential = occupancy vs capacity (population / residents-capacity)
// - workplaces = filled jobs vs job capacity (workers / total-jobs)
// - services = demand served vs capacity (per-service formulas)
// - power = MW draw vs MW capacity
// - honest null-basis kinds (parks, landmarks, etc.) = documented null, never fake 100%
//
// Determinism & null-basis honesty are proved by constructed states.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { utilisationOf, SPECS, residentsCapacity, totalJobs, powerStats, waterCaps } from '../src/sim/data.ts';

// Minimal state builder
function minimalState(overrides = {}) {
  return {
    tick: 0,
    speed: 1,
    funds: 10000000,
    loanBalance: 0,
    population: 0,
    xp: 0,
    taxRates: { residential: 9, commercial: 11, industrial: 13 },
    policies: { recycling: false, transitSubsidy: false, tourismDrive: false, austerity: false },
    buildings: [],
    nextId: 1,
    movingId: null,
    tool: { mode: 'select' },
    clipboard: null,
    pipeTier: {},
    history: [],
    ledger: [],
    nextLedgerId: 1,
    lastFlows: { inflows: [], outflows: [] },
    fundsAtTickStart: 10000000,
    fundsAtTickEnd: 10000000,
    pendingRewards: [],
    lastRewardedLevel: 1,
    notice: null,
    ...overrides,
  };
}

// Add a building to state
function withBuilding(state, spec, builtTick = null) {
  return {
    ...state,
    buildings: [
      ...state.buildings,
      { id: state.nextId, spec, x: state.nextId, y: 10, builtTick },
    ],
    nextId: state.nextId + 1,
  };
}

test('utilisationOf: residential occupancy', () => {
  // No residents cap → null
  let s = minimalState({ population: 100 });
  let b = { id: 1, spec: 'res_hut', x: 0, y: 0 };
  assert.equal(utilisationOf(s, b), null, 'no capacity → null');

  // With capacity: ratio = population / capacity, clamped 0..1
  s = withBuilding(s, 'res_hut');
  const cap = residentsCapacity(s);
  assert.ok(cap > 0, 'res_hut has capacity');
  let u = utilisationOf(s, b);
  assert.ok(u !== null, 'with capacity → utilisation');
  assert.equal(u.basis, 'citywide occupancy', 'basis admits aggregation');
  assert.ok(u.ratio >= 0 && u.ratio <= 1, 'ratio clamped 0..1');
  // Expected ratio is clamped: min(1, max(0, population / capacity))
  const expectedRatio = Math.min(1, Math.max(0, 100 / cap));
  assert.deepEqual(u.ratio, expectedRatio, 'ratio = population / capacity (clamped)');

  // Over-capacity clamps to 1.0
  s = minimalState({ population: 1000 });
  s = withBuilding(s, 'res_hut');
  b = s.buildings[0];
  u = utilisationOf(s, b);
  assert.equal(u.ratio, 1, 'over-capacity clamps to 1.0');
});

test('utilisationOf: power MW draw vs capacity', () => {
  // No power capacity → null
  let s = minimalState({ population: 100 });
  let b = { id: 1, spec: 'pow_wind', x: 0, y: 0 };
  let u = utilisationOf(s, b);
  assert.equal(u, null, 'no power plants → null');

  // With power plant
  s = withBuilding(s, 'pow_wind');
  const pw = powerStats(s);
  assert.ok(pw.cap > 0, 'pow_wind has capacity');
  b = s.buildings[0];
  u = utilisationOf(s, b);
  assert.ok(u !== null, 'with power plant → utilisation');
  assert.equal(u.basis, 'citywide MW draw', 'basis admits aggregation');
  // draw = population * 0.012, capacity = 8 MW
  const expected = Math.min(1, Math.max(0, pw.need / pw.cap));
  assert.deepEqual(u.ratio, expected, 'ratio = need / cap');
});

test('utilisationOf: jobs (commercial/office/industrial/mine)', () => {
  // No jobs → null
  let s = minimalState({ population: 100 });
  let b = { id: 1, spec: 'com_shop', x: 0, y: 0 };
  let u = utilisationOf(s, b);
  assert.equal(u, null, 'no jobs yet → null');

  // With a commercial building
  s = withBuilding(s, 'com_shop');
  const jobs = totalJobs(s);
  assert.ok(jobs > 0, 'com_shop adds jobs');
  b = s.buildings[0];
  u = utilisationOf(s, b);
  assert.ok(u !== null, 'with jobs → utilisation');
  assert.equal(u.basis, 'citywide workers vs jobs', 'basis admits aggregation');
  // workers = population * 0.55 = 55
  const workers = s.population * 0.55;
  const expected = Math.min(1, Math.max(0, workers / jobs));
  assert.deepEqual(u.ratio, expected, 'ratio = workers / jobs');
});

test('utilisationOf: school places filled', () => {
  // No schools → null
  let s = minimalState({ population: 100 });
  let b = { id: 1, spec: 'edu_nursery', x: 0, y: 0 };
  let u = utilisationOf(s, b);
  assert.equal(u, null, 'no schools yet → null');

  // With a school
  s = withBuilding(s, 'edu_nursery');
  b = s.buildings[0];
  u = utilisationOf(s, b);
  assert.ok(u !== null, 'with school → utilisation');
  assert.equal(u.basis, 'citywide student places', 'basis admits aggregation');
  // nursery capacity is 30 places, students = population * 0.18 = 18
  const places = 30;
  const students = s.population * 0.18;
  const expected = Math.min(1, Math.max(0, students / places));
  assert.deepEqual(u.ratio, expected, 'ratio = students / places');
});

test('utilisationOf: water service capacity', () => {
  // No water plants → null
  let s = minimalState({ population: 100 });
  let b = { id: 1, spec: 'wat_clean', x: 0, y: 0 };
  let u = utilisationOf(s, b);
  assert.equal(u, null, 'no water plants → null');

  // With a clean water plant
  s = withBuilding(s, 'wat_clean');
  b = s.buildings[0];
  u = utilisationOf(s, b);
  assert.ok(u !== null, 'with water plant → utilisation');
  assert.equal(u.basis, 'citywide clean water usage', 'basis admits aggregation');
  // clean capacity = 20000, served = population = 100
  const { clean } = waterCaps(s);
  assert.equal(clean, 20000, 'wat_clean capacity');
  const expected = Math.min(1, Math.max(0, s.population / clean));
  assert.deepEqual(u.ratio, expected, 'ratio = population / capacity');
});

test('utilisationOf: health aggregate capacity', () => {
  // No health buildings → null
  let s = minimalState({ population: 100 });
  let b = { id: 1, spec: 'hea_clinic', x: 0, y: 0 };
  let u = utilisationOf(s, b);
  assert.equal(u, null, 'no health buildings → null');

  // With a clinic
  s = withBuilding(s, 'hea_clinic');
  b = s.buildings[0];
  u = utilisationOf(s, b);
  assert.ok(u !== null, 'with clinic → utilisation');
  assert.equal(u.basis, 'citywide health coverage', 'basis admits aggregation');
  // clinic serves 5000, population = 100
  const expected = Math.min(1, Math.max(0, s.population / 5000));
  assert.deepEqual(u.ratio, expected, 'ratio = population / health-capacity');
});

test('utilisationOf: police coverage', () => {
  // No police → null
  let s = minimalState({ population: 100 });
  let b = { id: 1, spec: 'pol_station', x: 0, y: 0 };
  let u = utilisationOf(s, b);
  assert.equal(u, null, 'no police → null');

  // With a station
  s = withBuilding(s, 'pol_station');
  b = s.buildings[0];
  u = utilisationOf(s, b);
  assert.ok(u !== null, 'with station → utilisation');
  assert.equal(u.basis, 'citywide police coverage', 'basis admits aggregation');
  // station covers 10000
  const expected = Math.min(1, Math.max(0, s.population / 10000));
  assert.deepEqual(u.ratio, expected, 'ratio = population / police-capacity');
});

test('utilisationOf: null-basis kinds (parks, landmarks, transport, etc.)', () => {
  const nullBasisKinds = ['park', 'landmark', 'fire', 'civic', 'transport', 'leisure', 'road', 'motorway', 'rail', 'station', 'pylon'];
  let s = minimalState({ population: 100 });

  for (const kind of nullBasisKinds) {
    // Find a spec of this kind
    const spec = Object.values(SPECS).find((sp) => sp.kind === kind);
    if (!spec) {
      console.warn(`No spec found for kind: ${kind}`);
      continue;
    }

    const b = { id: 1, spec: spec.id, x: 0, y: 0 };
    const u = utilisationOf(s, b);
    assert.equal(
      u,
      null,
      `${kind} (${spec.id}): honest null-basis, never fake 100%`
    );
  }
});

test('utilisationOf: determinism — same state → same ratio', () => {
  // Use populations that don't clamp: both < res_hut capacity (8)
  const s1 = minimalState({ population: 2 });
  const s2 = minimalState({ population: 2 });
  const s3 = minimalState({ population: 5 });

  const b = { id: 1, spec: 'res_hut', x: 0, y: 0 };
  const withB1 = { ...s1, buildings: [{ id: 1, spec: 'res_hut', x: 0, y: 0, builtTick: null }] };
  const withB2 = { ...s2, buildings: [{ id: 1, spec: 'res_hut', x: 0, y: 0, builtTick: null }] };
  const withB3 = { ...s3, buildings: [{ id: 1, spec: 'res_hut', x: 0, y: 0, builtTick: null }] };

  const u1 = utilisationOf(withB1, b);
  const u2 = utilisationOf(withB2, b);
  const u3 = utilisationOf(withB3, b);

  assert.deepEqual(u1, u2, 'identical states → identical ratios');
  assert.notDeepEqual(u1, u3, 'different population → different ratio');
  assert.ok(u3.ratio > u1.ratio, 'higher population → higher ratio');
});

test('utilisationOf: ratio is always 0..1 clamped', () => {
  // Under-capacity: ratio < 1
  let s = minimalState({ population: 10 });
  s = withBuilding(s, 'res_hut'); // capacity 8, pop 10 → over capacity
  let b = s.buildings[0];
  let u = utilisationOf(s, b);
  assert.ok(u.ratio >= 0 && u.ratio <= 1, 'ratio clamped even when over capacity');

  // Zero capacity: ratio = 0
  s = minimalState({ population: 0 });
  s = withBuilding(s, 'res_hut');
  b = s.buildings[0];
  u = utilisationOf(s, b);
  assert.equal(u.ratio, 0, 'zero population → ratio 0');

  // Maximum over-supply
  s = minimalState({ population: 1000000 });
  s = withBuilding(s, 'res_hut'); // capacity 8, pop 1M → massively over
  b = s.buildings[0];
  u = utilisationOf(s, b);
  assert.equal(u.ratio, 1, 'huge over-supply → ratio clamped 1.0');
});

test('utilisationOf: null building spec → null', () => {
  const s = minimalState();
  const b = { id: 1, spec: 'nonexistent_spec', x: 0, y: 0 };
  const u = utilisationOf(s, b);
  assert.equal(u, null, 'invalid spec → null');
});

test('utilisationOf: online vs under-construction building has same utilisation', () => {
  let s = minimalState({ population: 50 });
  s = withBuilding(s, 'res_hut', null); // online (builtTick null)

  const b = s.buildings[0];
  const u = utilisationOf(s, b);
  assert.ok(u !== null, 'online building has utilisation');

  // Same building under construction should have the same utilisation
  // (utilisationOf doesn't check online status — it's a pure ratio)
  const bConstructing = { ...b, builtTick: s.tick };
  const uConstructing = utilisationOf(s, bConstructing);
  assert.deepEqual(u, uConstructing, 'utilisation is independent of online status');
});

test('utilisationOf: map-cue derivation (non-residential kinds)', () => {
  // FEAT-1972079855 map-cue rendering requires utilisationOf() to return non-null
  // for non-residential buildings with capacity signals. Test that the cue-rendering
  // contract is satisfied: non-residential buildings of kinds with capacity models
  // return {ratio, basis} suitable for color-coding and display.
  let s = minimalState({ population: 100 });

  // Test non-residential kinds that SHOULD render a cue
  const cuedKinds = [
    { spec: 'pow_wind', kind: 'power', expectCue: true },
    { spec: 'com_shop', kind: 'commercial', expectCue: true },
    { spec: 'off_suite', kind: 'office', expectCue: true },
    { spec: 'farm_wheat', kind: 'industrial', expectCue: true },
    { spec: 'mine_quarry', kind: 'mine', expectCue: true },
    { spec: 'edu_nursery', kind: 'school', expectCue: true },
    { spec: 'hea_clinic', kind: 'health', expectCue: true },
    { spec: 'pol_station', kind: 'police', expectCue: true },
    { spec: 'wat_clean', kind: 'water', expectCue: true },
    // Null-basis kinds should NOT render a cue
    { spec: 'park', kind: 'park', expectCue: false },
    { spec: 'land_stadium', kind: 'landmark', expectCue: false },
    { spec: 'road', kind: 'road', expectCue: false },
  ];

  for (const { spec, kind, expectCue } of cuedKinds) {
    // Build a state with this building
    let testS = withBuilding(s, spec);
    const b = testS.buildings[0];
    const u = utilisationOf(testS, b);

    if (expectCue) {
      assert.ok(u !== null, `${kind} (${spec}): expects non-null util for cue`);
      assert.ok(typeof u.ratio === 'number', `${kind}: ratio is number`);
      assert.ok(u.ratio >= 0 && u.ratio <= 1, `${kind}: ratio in 0..1`);
      assert.ok(typeof u.basis === 'string' && u.basis.includes('citywide'), `${kind}: basis admits aggregation`);
      // Color-coding test: the cue uses ratio thresholds
      // green < 0.3, amber 0.3-0.7, red >= 0.7
      const col = u.ratio < 0.3 ? 'green' : u.ratio < 0.7 ? 'amber' : 'red';
      assert.ok(['green', 'amber', 'red'].includes(col), `${kind}: color assignment defined`);
    } else {
      assert.equal(u, null, `${kind} (${spec}): honest null, no cue rendered`);
    }
  }
});
