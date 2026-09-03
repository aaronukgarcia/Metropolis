// bug645-redproof-child.mjs — child process helper for the BUG-645 RED-PROOF
// in bug645-population-visibility.test.mjs. Run in a FRESH process (never
// inline in the parent test file — the parent's own tsx module cache already
// holds the memoised residentialConstructionSummary, so an in-process
// before/after comparison cannot observe a disk-level mutation; see
// attack-bug642-memo.test.mjs's identical documented reasoning).
//
// WHY THE WARM METRIC, NOT THE COLD ONE: the FIRST call on a fresh 29,831-
// building state costs the same full O(buildings) scan whether or not the
// function is memoOnState-wrapped (memoisation cannot avoid that first
// scan) — measured directly: an unmemoised residentialConstructionSummary's
// first call is ~1-3ms, indistinguishable in order of magnitude from the
// memoised version's own cold-call cost. The property memoOnState actually
// buys is repeated calls on the SAME state object (TopBar + DemographicsTab
// both reading it every render, on a state that has not changed) collapsing
// to a WeakMap hit. This script therefore reproduces the real WARM-call
// perf test (bug645-population-visibility.test.mjs's own WARM assertion):
// call the function 20 times on ONE state object and take the median.
// Memoised: ~0.0004ms (WeakMap hit). Un-memoised: every call re-runs the
// full scan, so the median stays at the ~1-3ms full-scan cost — a >1000x
// gap that reliably reddens the WARM bound below.
//
// GUARD (mirrors tools/test/scoped.mjs's own documented guard, BUG-543
// class): this file lives under a `test/` directory, so CI's repo-root bare
// `node --test` (no tsx loader) auto-discovers and imports it directly. A
// static top-level `import ... from '../../src/sim/data.ts'` would fail to
// resolve under that loader-less `node --test` (no .ts extension handling),
// reddening the unrelated root job. `node --test` sets NODE_TEST_CONTEXT for
// every file it discovers; exit BEFORE any import is attempted whenever that
// is set — this file is a CLI helper invoked explicitly (with `--import tsx`)
// by the real test, never a test suite on its own.
if (process.env.NODE_TEST_CONTEXT) {
  process.exit(0);
}

const { residentialConstructionSummary } = await import('../../src/sim/data.ts');
const { buildScaleFixture } = await import('./fixture.mjs');

const WARM_CALL_MEDIAN_BOUND_MS = 0.05;
const SCALE_BUILDING_COUNT = 29831;
const SCALE_TARGET_POPULATION = 1_900_000;
const REPEATS = 20;

function median(values) {
  const sorted = [...values].sort((a, b) => a - b);
  return sorted[Math.floor(sorted.length / 2)];
}

const s = buildScaleFixture({ buildingCount: SCALE_BUILDING_COUNT, targetPopulation: SCALE_TARGET_POPULATION, settleTicks: 1 });
residentialConstructionSummary(s); // warm the memo (a no-op when un-memoised)
const times = [];
for (let i = 0; i < REPEATS; i++) {
  const t0 = performance.now();
  residentialConstructionSummary(s);
  times.push(performance.now() - t0);
}
const med = median(times);
console.log(`[bug645-redproof-child] median WARM (same-state, repeated) call time at ${SCALE_BUILDING_COUNT} buildings: ${med.toFixed(4)}ms (bound ${WARM_CALL_MEDIAN_BOUND_MS}ms)`);
if (med >= WARM_CALL_MEDIAN_BOUND_MS) {
  console.log('RED-PROOF-FAIL: median warm-call time exceeds the memoised bound — the un-memoised mutation was correctly detected.');
  process.exit(1);
}
console.log('unexpectedly stayed under bound even unmemoised — RED-PROOF DID NOT FIRE');
process.exit(0);
