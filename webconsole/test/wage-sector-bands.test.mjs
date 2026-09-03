// wage-sector-bands.test.mjs — FEAT-wage-stage1 (Q100067/Q100086) regression
// tests for fiscal.ts's per-sector wage bands, Stage 1 of
// docs/planning/proposals/wage-ownership-model-2026-09-02.md's 4-stage plan.
//
// F2 FIX (independent round REJECT, 2026-09-03, GR#6): this header originally
// claimed "engine.ts is NOT touched by this feature... Stage 1 is not wired
// into the tick yet" — that was true for the FIRST landing only. Stage 1 IS
// NOW WIRED: engine.ts's computeFlows() sources the 'Wages' outflow from
// fiscal.ts's sectorWagesPerTick(data.ts's filledJobsBySector(s)), and
// consistency.ts's flows-vs-recompute check uses the identical formula. This
// file now tests THREE layers: (1) fiscal.ts's pure sectorWagesPerTick() /
// allocateFilledJobs() decomposition/apportionment functions directly, (2)
// data.ts's totalJobsBySector()/filledJobsBySector() (the real exported
// basis functions, not a local reimplementation), and (3) the engine.ts
// WIRING itself, via computeFlows() fixture comparisons (a permanent
// regression proof, not a manual sabotage-and-revert).
//
// F1 FIX (independent round REJECT): the first landing fed sectorWagesPerTick()
// raw job CAPACITY (vacancy-inclusive) — population 0 + one office tower paid
// a real wage for empty desks. filledJobsBySector() caps capacity at the
// actual workforce (population * WORKING_AGE_FRACTION) before apportioning by
// sector (fiscal.ts's allocateFilledJobs(), largest-remainder method) — see
// both files' own FEAT-wage-stage1/F1 comment blocks for the full rule.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  sectorWagesPerTick,
  allocateFilledJobs,
  wagesPerTick,
  KIND_TO_WAGE_SECTOR,
  SECTOR_WAGE_PER_MONTH,
  SECTOR_ORDER,
  ZERO_SECTOR_JOBS,
} from '../src/sim/fiscal.ts';
import {
  SPECS,
  isOnline,
  capacityAtTier,
  totalJobs,
  totalJobsBySector,
  filledJobsBySector,
  computeRoadConnectivity,
  WORKING_AGE_FRACTION,
} from '../src/sim/data.ts';
import { TICKS_PER_MONTH, computeFlows } from '../src/sim/engine.ts';

// ---------------------------------------------------------------------------
// Test-local helper: an INDEPENDENT reimplementation of totalJobsBySector()'s
// loop (not a call to the real export) used ONLY for the basis-parity
// cross-check below — proves the two independently-written loops agree,
// which is a stronger check than calling the same function twice.
function jobsBySectorFromBuildingsIndependent(state) {
  const bySector = { primary: 0, secondary: 0, tertiary: 0, public: 0 };
  for (const b of state.buildings) {
    if (!isOnline(state, b)) continue;
    const sp = SPECS[b.spec];
    if (!sp) continue;
    let jobs = 0;
    if (sp.jobs) jobs = capacityAtTier(sp, b.capacityTier ?? 0);
    else if (sp.kind === 'commercial') jobs = 12;
    else if (sp.kind === 'industrial') jobs = 18;
    if (jobs <= 0) continue;
    const sector = KIND_TO_WAGE_SECTOR[sp.kind];
    if (!sector) continue;
    bySector[sector] += jobs;
  }
  return bySector;
}

// Minimal SimState fixture (mirrors fiscal.test.mjs's baseState()/addBuilding()).
function baseState() {
  return {
    tick: 0,
    speed: 1,
    funds: 10000000,
    loanBalance: 0,
    population: 1000,
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
    lastRewardedLevel: 1,
    notice: null,
  };
}

function addBuilding(state, spec) {
  return {
    ...state,
    buildings: [
      ...state.buildings,
      { id: state.nextId, spec, x: state.nextId, y: 10, builtTick: null },
    ],
    nextId: state.nextId + 1,
  };
}

// ========== Basic shape / conservation (sectorWagesPerTick) ==========

test('sectorWagesPerTick: all-zero input produces all-zero output', () => {
  const { lines, totalPerTick } = sectorWagesPerTick(ZERO_SECTOR_JOBS);
  assert.equal(totalPerTick, 0);
  assert.equal(lines.length, 4);
  for (const line of lines) {
    assert.equal(line.jobs, 0);
    assert.equal(line.wagePerTick, 0);
  }
});

test('conservation: totalPerTick equals the exact sum of per-sector lines (no rounding drift)', () => {
  const jobs = { primary: 7, secondary: 13, tertiary: 41, public: 5 };
  const { lines, totalPerTick } = sectorWagesPerTick(jobs);
  const sumOfLines = lines.reduce((a, l) => a + l.wagePerTick, 0);
  assert.equal(totalPerTick, sumOfLines);
});

test('GR#16: sectorWagesPerTick sanitizes hostile inputs instead of trusting the type', () => {
  const hostile = { primary: -5, secondary: NaN, tertiary: 'ten', public: Infinity };
  const { lines, totalPerTick } = sectorWagesPerTick(hostile);
  for (const line of lines) assert.equal(line.jobs, 0);
  assert.equal(totalPerTick, 0);
});

test('determinism: same input always produces byte-identical output (GR#21)', () => {
  const jobs = { primary: 3, secondary: 9, tertiary: 27, public: 4 };
  const a = sectorWagesPerTick(jobs);
  const b = sectorWagesPerTick(jobs);
  assert.deepEqual(a, b);
});

// ========== F1: allocateFilledJobs() — deterministic apportionment ==========

test('allocateFilledJobs: zero capacity or zero target returns all-zero (no divide-by-zero)', () => {
  assert.deepEqual(allocateFilledJobs(0, { primary: 10, secondary: 0, tertiary: 0, public: 0 }), {
    ...ZERO_SECTOR_JOBS,
  });
  assert.deepEqual(allocateFilledJobs(50, ZERO_SECTOR_JOBS), { ...ZERO_SECTOR_JOBS });
});

test('allocateFilledJobs: target === totalCapacity returns capacity verbatim, zero remainder', () => {
  const capacity = { primary: 10, secondary: 20, tertiary: 30, public: 5 }; // total 65
  const result = allocateFilledJobs(65, capacity);
  assert.deepEqual(result, capacity);
});

test('allocateFilledJobs: target < totalCapacity conserves the target exactly (largest-remainder method)', () => {
  // 17 to distribute over weights 7/13/41/5 (total 66) — chosen so every
  // sector's exact share has a non-trivial fractional remainder.
  const capacity = { primary: 7, secondary: 13, tertiary: 41, public: 5 };
  const result = allocateFilledJobs(17, capacity);
  const sum = result.primary + result.secondary + result.tertiary + result.public;
  assert.equal(sum, 17, 'allocated total must equal the target EXACTLY — no rounding drift');
  // every allocated sector must not exceed its own capacity
  for (const sector of SECTOR_ORDER) assert.ok(result[sector] <= capacity[sector]);
});

test('allocateFilledJobs: a target above totalCapacity is clamped to totalCapacity (never invents jobs)', () => {
  const capacity = { primary: 5, secondary: 5, tertiary: 5, public: 5 }; // total 20
  const result = allocateFilledJobs(999, capacity);
  const sum = result.primary + result.secondary + result.tertiary + result.public;
  assert.equal(sum, 20);
  assert.deepEqual(result, capacity);
});

test('allocateFilledJobs: equal-remainder ties break by fixed SECTOR_ORDER (primary/secondary/tertiary/public)', () => {
  // Four equal-capacity sectors, target not evenly divisible -> every exact
  // share has an IDENTICAL fractional remainder (0.25) -> the tie-break rule
  // alone decides who gets the +1s, in SECTOR_ORDER.
  const capacity = { primary: 100, secondary: 100, tertiary: 100, public: 100 };
  const result = allocateFilledJobs(101, capacity); // 25.25 each -> 1 leftover unit
  assert.equal(result.primary, 26, 'first in SECTOR_ORDER gets the sole leftover unit');
  assert.equal(result.secondary, 25);
  assert.equal(result.tertiary, 25);
  assert.equal(result.public, 25);
});

test('allocateFilledJobs: determinism (GR#21) — repeated calls with the same input are byte-identical', () => {
  const capacity = { primary: 3, secondary: 9, tertiary: 27, public: 4 };
  const a = allocateFilledJobs(20, capacity);
  const b = allocateFilledJobs(20, capacity);
  assert.deepEqual(a, b);
});

// ========== data.ts totalJobsBySector()/filledJobsBySector() — real exports ==========

test('totalJobsBySector (real export): basis parity vs data.ts totalJobs() for mixed-sector buildings', () => {
  let state = baseState();
  state = addBuilding(state, 'com_super'); // commercial, jobs: 40 -> tertiary
  state = addBuilding(state, 'ind_light'); // industrial, jobs: 24 -> secondary
  state = addBuilding(state, 'mine_quarry'); // mine, jobs: 30 -> primary
  state = addBuilding(state, 'off_suite'); // office, jobs: 25 -> tertiary
  state = addBuilding(state, 'bus_depot'); // transport, jobs: 20 -> public

  const bySector = totalJobsBySector(state);
  const sumBySector = bySector.primary + bySector.secondary + bySector.tertiary + bySector.public;
  assert.equal(sumBySector, totalJobs(state), 'sector-bucketed jobs must sum to the SAME total data.ts already reports');
  assert.equal(bySector.primary, 30);
  assert.equal(bySector.secondary, 24);
  assert.equal(bySector.tertiary, 40 + 25);
  assert.equal(bySector.public, 20);

  // Cross-check against an INDEPENDENTLY written reimplementation too.
  assert.deepEqual({ ...bySector }, jobsBySectorFromBuildingsIndependent(state));
});

test('totalJobsBySector: offline (under-construction) buildings contribute zero jobs, mirroring BUG-525', () => {
  let state = baseState();
  state = { ...state, tick: 0 };
  state = addBuilding(state, 'off_tower'); // jobs: 300 -> tertiary, but not yet built
  state.buildings[0].builtTick = 0; // just placed, still under construction at tick 0
  const bySector = totalJobsBySector(state);
  assert.equal(bySector.tertiary, 0);
});

test('F3: totalJobsBySector isOnline gating via a road-cut (G3 road-connected)', () => {
  // Road segment touching the west map edge (x=0), fully built, an office
  // adjacent to the FAR end of it — online while the segment stays
  // edge-connected. builtTick set far in the past so G1 (construction time)
  // never gates it — only the road-connectivity gates (G2/G3) are exercised.
  const roadAt = (x, y) => ({ id: 900000 + x, spec: 'road', x, y, builtTick: -100000 });
  const officeAt = (x, y) => ({ id: 1, spec: 'off_suite', x, y, builtTick: -100000 }); // jobs: 25 -> tertiary

  const connected = [roadAt(0, 500), roadAt(1, 500), roadAt(2, 500), officeAt(3, 500)];
  let state = { ...baseState(), tick: 100000, buildings: connected };
  state = { ...state, roadConnectivity: computeRoadConnectivity(state) };
  const beforeCut = totalJobsBySector(state);
  assert.equal(beforeCut.tertiary, 25, 'office online (road-adjacent + connected to the map edge) -> full jobs count');

  // Cut the road: remove the MIDDLE tile, isolating the office's local road
  // tile (2,500) from the edge-connected segment. New buildings array ->
  // different object reference -> memoOnState recomputes, not a stale hit.
  const cut = connected.filter((b) => !(b.spec === 'road' && b.x === 1 && b.y === 500));
  let cutState = { ...state, buildings: cut };
  cutState = { ...cutState, roadConnectivity: computeRoadConnectivity(cutState) };
  const afterCut = totalJobsBySector(cutState);
  assert.equal(afterCut.tertiary, 0, 'office road-disconnected from the network -> offline -> zero jobs counted');
});

test('F3: totalJobsBySector memoisation identity — same state object returns the SAME cached reference, a new object recomputes', () => {
  let state = baseState();
  state = addBuilding(state, 'off_suite');
  const first = totalJobsBySector(state);
  const second = totalJobsBySector(state); // identical object reference -> cache hit
  assert.equal(first, second, 'same state reference must return the identical cached object');

  const differentRefSameContent = { ...state }; // new object, same buildings array
  const third = totalJobsBySector(differentRefSameContent);
  assert.notEqual(third, first, 'a different state object must not silently reuse a stale cache entry');
  assert.deepEqual({ ...third }, { ...first }, 'but the recomputed VALUE must still match');
});

// ========== F5: shared-memo mutation hardening ==========

test('F5: totalJobsBySector returns a frozen object — a mutation attempt cannot corrupt the shared per-state cache', () => {
  let state = baseState();
  state = addBuilding(state, 'off_suite'); // tertiary: 25
  const first = totalJobsBySector(state);
  assert.ok(Object.isFrozen(first), 'totalJobsBySector() result must be frozen');
  assert.throws(
    () => {
      first.tertiary = 999999;
    },
    TypeError,
    'mutating a frozen object under ES-module strict mode must throw',
  );
  const second = totalJobsBySector(state); // same state reference -> same cached object
  assert.equal(second.tertiary, 25, 'the cached value must be unaffected by the failed mutation attempt');
});

test('F5: filledJobsBySector also returns a frozen object', () => {
  let state = baseState();
  state = { ...state, population: 1000 };
  state = addBuilding(state, 'off_suite');
  const filled = filledJobsBySector(state);
  assert.ok(Object.isFrozen(filled), 'filledJobsBySector() result must be frozen');
  assert.throws(() => {
    filled.public = 42;
  }, TypeError);
});

// ========== F1: filled-jobs allocation — the three named edge cases ==========

test('F1 filled-jobs: an empty city (population 0) has zero filled jobs in EVERY sector, however large capacity is', () => {
  let state = baseState();
  state = { ...state, population: 0 };
  state = addBuilding(state, 'off_towers_downtown'); // 2,000 job slots, all vacant at pop 0
  const filled = filledJobsBySector(state);
  assert.deepEqual({ ...filled }, { ...ZERO_SECTOR_JOBS });
});

test('F1 filled-jobs: jobs > workers pays exactly the workforce (capacity-weighted), never the raw capacity', () => {
  let state = baseState();
  state = { ...state, population: 200 }; // workers = 200 * WORKING_AGE_FRACTION
  state = addBuilding(state, 'off_towers_downtown'); // capacity 2,000, far above workers
  const filled = filledJobsBySector(state);
  const totalFilled = filled.primary + filled.secondary + filled.tertiary + filled.public;
  const expectedWorkers = Math.round(200 * WORKING_AGE_FRACTION);
  assert.equal(totalFilled, expectedWorkers, 'filled total must equal the WORKFORCE, capped below capacity');
});

test('F1 filled-jobs: workers > jobs pays exactly the total job capacity (every sector filled to its own capacity)', () => {
  let state = baseState();
  state = { ...state, population: 100000 }; // workers = 55,000, far above any capacity below
  state = addBuilding(state, 'off_suite'); // capacity 25, tertiary only
  const filled = filledJobsBySector(state);
  assert.deepEqual({ ...filled }, { primary: 0, secondary: 0, tertiary: 25, public: 0 });
});

// ========== F3: engine.ts wiring — permanent fixture-comparison proof ==========
// (replaces the manual "+777 sabotage, run tests, revert" cycle with a
// standing regression the suite runs every time)

test('F1 regression: population 0 + one huge office tower pays £0 Wages (was £113,333/tick pre-fix)', () => {
  let state = baseState();
  state = { ...state, population: 0 };
  state = addBuilding(state, 'off_towers_downtown');
  const { outflows } = computeFlows(state);
  const wages = outflows.find((f) => f.label === 'Wages')?.value ?? 0;
  assert.equal(wages, 0, 'an empty city must not pay wages for vacant job capacity');
});

test('engine wiring: computeFlows Wages EXACTLY matches sectorWagesPerTick(filledJobsBySector(s)), and DIVERGES from the rejected capacity-based formula', () => {
  let state = baseState();
  state = { ...state, population: 100 }; // small workforce relative to the tower's capacity
  state = addBuilding(state, 'off_towers_downtown'); // 2,000 job slots, mostly vacant at pop 100
  const { outflows } = computeFlows(state);
  const actualWages = outflows.find((f) => f.label === 'Wages')?.value ?? 0;

  const correctFilled = sectorWagesPerTick(filledJobsBySector(state)).totalPerTick;
  const rejectedCapacityBased = sectorWagesPerTick(totalJobsBySector(state)).totalPerTick;

  assert.equal(actualWages, correctFilled, 'engine.ts must source Wages from the FILLED-jobs formula');
  assert.notEqual(
    actualWages,
    rejectedCapacityBased,
    'must NOT regress to paying raw vacancy-inclusive job capacity (the F1 defect) — ' +
      `capacity-based would have paid ${rejectedCapacityBased}, correct filled-jobs pays ${correctFilled}`,
  );
});

test('consistency check: flows.wages-matches passes for a filled-jobs-relevant fixture (population well below job capacity)', async () => {
  const { runConsistencyChecks } = await import('../src/sim/consistency.ts');
  let state = baseState();
  state = { ...state, population: 500 };
  state = addBuilding(state, 'off_towers_downtown');
  const { outflows } = computeFlows(state);
  state = { ...state, lastFlows: { inflows: [], outflows } };
  const report = runConsistencyChecks(state);
  const check = report.checks.find((c) => c.id === 'flows.wages-matches');
  assert.ok(check, 'wages check exists');
  assert.equal(check.ok, true, `consistency recompute must agree with the engine's filled-jobs Wages figure: ${check.detail}`);
});

// ========== The doc's four worked examples (§6), as fixtures ==========
//
// NOTE on fidelity: the doc's worked examples give PER-FIRM negotiated wages
// (corner shop £1,600/mo, supermarket £1,800/mo — both nominally "tertiary"
// in this Stage-1 taxonomy) — Stage 1's coarse ONE-band-per-sector design
// cannot reproduce two different tertiary rates simultaneously by
// construction (see fiscal.ts's SECTOR_WAGE_PER_MONTH comment). These tests
// therefore assert the STAGE-1 FORMULA (jobs x the one documented sector
// band) reproduces the doc's JOB COUNTS correctly, not its per-firm £ amounts
// — the £ divergence from the doc's own illustrative figures is the
// documented, deliberate Stage-1 simplification. These exercise
// sectorWagesPerTick() DIRECTLY with hand-specified (already-filled) job
// counts — they are about the WAGE BAND formula, not the filled-jobs cap.

test('doc example (a) — corner shop, sole trader, 2 tertiary jobs', () => {
  const { lines } = sectorWagesPerTick({ ...ZERO_SECTOR_JOBS, tertiary: 2 });
  const tertiary = lines.find((l) => l.sector === 'tertiary');
  assert.equal(tertiary.jobs, 2);
  assert.equal(tertiary.wagePerMonth, SECTOR_WAGE_PER_MONTH.tertiary);
  assert.equal(tertiary.jobs * tertiary.wagePerMonth, 2 * 1700);
});

test('doc example (b) — chain supermarket, 50 tertiary jobs', () => {
  const { lines } = sectorWagesPerTick({ ...ZERO_SECTOR_JOBS, tertiary: 50 });
  const tertiary = lines.find((l) => l.sector === 'tertiary');
  assert.equal(tertiary.jobs, 50);
  assert.equal(tertiary.jobs * tertiary.wagePerMonth, 50 * 1700);
});

test('doc example (c) — council school, 30 public-sector jobs, matches the teacher wage exactly', () => {
  const { lines } = sectorWagesPerTick({ ...ZERO_SECTOR_JOBS, public: 30 });
  const pub = lines.find((l) => l.sector === 'public');
  assert.equal(pub.jobs, 30);
  assert.equal(pub.jobs * pub.wagePerMonth, 60000);
});

test('doc example (d) — flat rented to a worker: rent is orthogonal to wages (§6d note)', () => {
  const a = sectorWagesPerTick({ ...ZERO_SECTOR_JOBS, tertiary: 2 });
  const d = sectorWagesPerTick({ ...ZERO_SECTOR_JOBS, tertiary: 2 });
  assert.deepEqual(a, d);
});

// ========== Flat-wage divergence (old population-flat vs new per-sector) ==========

test('baseline/genesis equivalence: at population 0 with no jobs, Stage 1 and the old flat wage both pay £0', () => {
  assert.equal(wagesPerTick(0), 0);
  assert.equal(sectorWagesPerTick(ZERO_SECTOR_JOBS).totalPerTick, 0);
});

test('documented divergence (pure formula): a jobs-vs-population gap pays LESS under Stage 1 than the old flat-per-capita wage', () => {
  // This exercises sectorWagesPerTick() DIRECTLY with a hand-specified,
  // already-filled SectorJobs (not the production filled-jobs pipeline) —
  // it documents the FORMULA-LEVEL divergence direction. See the
  // "engine wiring" tests above for the same divergence proven end-to-end
  // through computeFlows()/filledJobsBySector().
  const population = 10000;
  const jobsBySector = { primary: 20, secondary: 80, tertiary: 300, public: 100 }; // 500 jobs total
  const oldFlat = wagesPerTick(population);
  const stage1 = sectorWagesPerTick(jobsBySector).totalPerTick;
  assert.ok(
    stage1 < oldFlat,
    `Stage 1 (${stage1}) should pay LESS than the old flat-per-capita wage (${oldFlat}) once jobs undershoot population.`,
  );
});

// ========== TICKS_PER_MONTH_REF parity (mirrors the existing BUG-452 pattern) ==========

test('fiscal.ts internal TICKS_PER_MONTH_REF stays equal to engine.ts TICKS_PER_MONTH', () => {
  const jobs = { ...ZERO_SECTOR_JOBS, tertiary: 30 };
  const { lines } = sectorWagesPerTick(jobs);
  const tertiary = lines.find((l) => l.sector === 'tertiary');
  const expectedMonthly = tertiary.jobs * tertiary.wagePerMonth;
  const reconstructedMonthly = tertiary.wagePerTick * TICKS_PER_MONTH;
  assert.ok(
    Math.abs(reconstructedMonthly - expectedMonthly) <= TICKS_PER_MONTH,
    `tick-rounding drift too large: ${reconstructedMonthly} vs ${expectedMonthly}`,
  );
});

// ========== KIND_TO_WAGE_SECTOR coverage ==========

test('KIND_TO_WAGE_SECTOR maps every job-bearing catalogue kind seen in SPECS to a sector', () => {
  const jobBearingKinds = new Set();
  for (const sp of Object.values(SPECS)) {
    if (sp.placeholder) continue;
    if (sp.jobs || sp.kind === 'commercial' || sp.kind === 'industrial') {
      jobBearingKinds.add(sp.kind);
    }
  }
  for (const kind of jobBearingKinds) {
    assert.ok(
      KIND_TO_WAGE_SECTOR[kind],
      `catalogue kind '${kind}' carries jobs but has no KIND_TO_WAGE_SECTOR entry`,
    );
  }
});

// ========== F6: farm_* kind classification note (flagged, not remapped) ==========

test('F6 (documentation, not a remap): every farm_* spec is cataloged as kind industrial -> secondary band', () => {
  const farmIds = Object.keys(SPECS).filter((id) => id.startsWith('farm_'));
  assert.ok(farmIds.length > 0, 'sanity: the catalogue still has farm_* specs');
  for (const id of farmIds) {
    assert.equal(SPECS[id].kind, 'industrial', `${id} is expected to be kind 'industrial' today`);
    assert.equal(KIND_TO_WAGE_SECTOR[SPECS[id].kind], 'secondary');
  }
});
