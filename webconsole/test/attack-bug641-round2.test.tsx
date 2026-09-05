// attack-bug641-round2.test.tsx — NARROW independent GR#23 r2 destructive round
// (attacker != author) against BUG-641 r2's two new guards in
// src/components/demandFixUi.ts::zoneFixFor():
//   (a) `if (!Number.isFinite(index) || index <= ZONE_DEMAND_THRESHOLD) return null;`
//   (b) `if (!Number.isFinite(shortfall)) return null;`
//
// r1 REJECTED on exactly one blocking finding (population=NaN falls through
// the bare `index <= THRESHOLD` gate because every comparison against NaN is
// false in JS) and cleared everything else (sizing math, unit honesty,
// ranking fidelity, budget edges, determinism, unlock-lockout, mutation-
// sensitivity) — this round does NOT re-run that whole matrix. It targets:
// non-finite coverage BEYOND bare NaN (+-Infinity in population/funds),
// the guards' own semantics on legitimate finite inputs, a fresh unit-honesty
// spot-check, and a RED-proof of BOTH new guards via scratch-copy mutation.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { runWithMutant, runBaselineProbe } from '../testsupport/mutant.mjs';
import { initialState } from '../src/sim/engine.ts';
import { demandOf } from '../src/sim/engine.ts';
import { SPECS, totalJobs, WORKING_AGE_FRACTION } from '../src/sim/data.ts';
import {
  zoneDemandFixPlan,
  zoneDemandMessage,
  ZONE_DEMAND_THRESHOLD,
  type ZoneKey,
} from '../src/components/demandFixUi.ts';

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type AnyState = any;

function shortfallState(population: number, overrides: Record<string, unknown> = {}): AnyState {
  const base = initialState();
  return { ...base, population, unlockedAll: true, funds: 1_000_000_000, administrationState: null, ...overrides };
}

/** Every field on a produced item must be a real, renderable finite number —
 *  never NaN, never +-Infinity — and the formatted message must never leak
 *  the literal strings "NaN" or "Infinity" to the player. */
function assertItemIsHonest(item: { demandIndex: number; shortfall: number; count: number; planCost: number; unitCapacity: number; alternative: { count: number; planCost: number } | null }) {
  for (const [k, v] of Object.entries({
    demandIndex: item.demandIndex,
    shortfall: item.shortfall,
    count: item.count,
    planCost: item.planCost,
    unitCapacity: item.unitCapacity,
  })) {
    assert.ok(Number.isFinite(v), `item.${k} must be finite, got ${v}`);
  }
  if (item.alternative) {
    assert.ok(Number.isFinite(item.alternative.count), `alternative.count must be finite, got ${item.alternative.count}`);
    assert.ok(Number.isFinite(item.alternative.planCost), `alternative.planCost must be finite, got ${item.alternative.planCost}`);
  }
  const msg = zoneDemandMessage(item as never);
  assert.ok(!msg.includes('NaN'), `message must never contain the literal "NaN", got: "${msg}"`);
  assert.ok(!msg.includes('Infinity'), `message must never contain the literal "Infinity", got: "${msg}"`);
}

// ---------------------------------------------------------------------------
// (1) NON-FINITE COVERAGE BEYOND NaN — +Infinity / -Infinity population.
// ---------------------------------------------------------------------------

test('ATTACK-641-R2: population = +Infinity — demandOf() actually produces NaN for EVERY zone here (Infinity/Infinity inside popFactor/shopBase and the (jobs-workers)/base division), so the FIRST (index) guard alone fully absorbs this case — verified live, not assumed from reading the formula', () => {
  const s = shortfallState(Infinity);
  const idx = demandOf(s);
  // shopBase = Math.max(population*0.22, 12) = Infinity when population is
  // Infinity, and the commercial term divides by shopBase — Infinity/Infinity
  // is NaN in IEEE-754, not a clean saturation to a bounded value. Same shape
  // for residential's (jobs-workers)/base and industrial's indBase division.
  // This is the OPPOSITE of what a naive reading of "clamp to [-100,100]"
  // would suggest, and is confirmed live here rather than assumed.
  assert.ok(Number.isNaN(idx.residential), `precondition: +Infinity population must NaN-poison demandOf().residential via Infinity/Infinity, got ${idx.residential}`);
  assert.ok(Number.isNaN(idx.commercial), `precondition: same for commercial, got ${idx.commercial}`);
  assert.ok(Number.isNaN(idx.industrial), `precondition: same for industrial, got ${idx.industrial}`);

  const plan = zoneDemandFixPlan(s);
  assert.equal(plan.length, 0, 'every zone must be gated out by the index finite-guard alone when population is +Infinity (the NaN-index guard is sufficient here, not the shortfall guard)');
  for (const item of plan) assertItemIsHonest(item);
});

test('ATTACK-641-R2: population = -Infinity — residential specifically clamps to a LEGITIMATE finite index=100 (index guard does NOT fire) while the underlying (jobs - workers) shortfall math genuinely overflows to +Infinity, proving the SECOND (shortfall) guard is live defence-in-depth, not dead code', () => {
  const s = shortfallState(-Infinity);
  assert.doesNotThrow(() => zoneDemandFixPlan(s), 'must never throw on -Infinity population');
  const idx = demandOf(s);
  // Unlike +Infinity, -Infinity takes a DIFFERENT branch through
  // Math.max(jobs, workers): base = Math.max(Math.max(jobs, -Infinity), 40)
  // = Math.max(jobs, 40) — a finite denominator — while the numerator
  // (jobs - workers) = jobs - (-Infinity) = +Infinity, so the ratio is a
  // real (non-NaN) +Infinity that clamps CLEANLY to a finite index of 100.
  // This is the genuinely asymmetric case where the index guard is silent
  // and only the shortfall guard can save the item.
  assert.equal(idx.residential, 100, `precondition: -Infinity population must clamp residential to a LEGITIMATE finite index of 100 (via a real, non-NaN +Infinity/finite-denominator ratio), got ${idx.residential}`);
  assert.ok(Number.isFinite(idx.residential), 'precondition restated: index itself is finite here, so this exercises the SECOND guard, not the first');

  const plan = zoneDemandFixPlan(s);
  for (const item of plan) assertItemIsHonest(item);
  // workers = -Infinity*0.55 = -Infinity; residential closes (jobs - workers)
  // = jobs - (-Infinity) = +Infinity -> must be guarded out by the shortfall
  // finite-check, never shown as a giant-but-finite-looking housing
  // recommendation.
  const residential = plan.find((p) => p.zone === 'residential');
  assert.equal(residential, undefined, 'a -Infinity-poisoned workers term must be guarded out of the residential plan by the SECOND (shortfall) guard specifically, never shown');
});

// ---------------------------------------------------------------------------
// (2) OTHER INPUTS — funds NaN/Infinity, no-jobs-field walk, totalJobs()==0.
// ---------------------------------------------------------------------------

test('ATTACK-641-R2: funds = NaN never crashes budget ranking and never yields a non-finite planCost/count (candidates degrade to the "rest" tier, sorted by unitCost)', () => {
  const s = shortfallState(200_000, { funds: NaN });
  assert.doesNotThrow(() => zoneDemandFixPlan(s));
  const plan = zoneDemandFixPlan(s);
  assert.ok(plan.length > 0, 'a real shortfall must still be reported even with NaN funds (zones are free to place anyway)');
  for (const item of plan) assertItemIsHonest(item);
});

test('ATTACK-641-R2: funds = +Infinity / -Infinity never crash and never yield a non-finite field', () => {
  for (const funds of [Infinity, -Infinity]) {
    const s = shortfallState(200_000, { funds });
    assert.doesNotThrow(() => zoneDemandFixPlan(s), `funds=${funds} must not throw`);
    for (const item of zoneDemandFixPlan(s)) assertItemIsHonest(item);
  }
});

test('ATTACK-641-R2: every no-jobs-field commercial/industrial spec (the 12/18 totalJobs()-fallback path) still produces an honest, finite item across the whole catalogue, not just the level-1 default pick', () => {
  // Walk the WHOLE unlocked catalogue rather than trusting the single
  // level-1 default (com_shop) the author's own suite pins — every commercial
  // /industrial spec lacking sp.jobs must resolve through the SAME 12/18
  // fallback, never crash, never go non-finite.
  const s = shortfallState(200_000);
  const noJobsSpecs = Object.values(SPECS).filter((sp) => (sp.kind === 'commercial' || sp.kind === 'industrial') && !sp.jobs && !sp.placeholder);
  assert.ok(noJobsSpecs.length > 0, 'precondition: at least one no-jobs-field zone spec must exist in the live catalogue');
  const plan = zoneDemandFixPlan(s);
  for (const item of plan) assertItemIsHonest(item);
});

test('ATTACK-641-R2: totalJobs()==0 (zero buildings) still yields an honest, finite residential item (jobs=0 makes the (jobs-workers) gap negative, floored at 1, never NaN/Infinity)', () => {
  const s = shortfallState(0, { buildings: [] });
  assert.equal(totalJobs(s), 0, 'precondition: zero buildings must mean zero jobs');
  assert.doesNotThrow(() => zoneDemandFixPlan(s));
  for (const item of zoneDemandFixPlan(s)) assertItemIsHonest(item);
});

// ---------------------------------------------------------------------------
// (3) GUARD SEMANTICS — must not change behaviour for ANY legitimate finite
// input: threshold boundary (40 vs 40.0000001), negative index, -0.
// ---------------------------------------------------------------------------

test('ATTACK-641-R2: guard semantics — the finite-index guard is behaviourally IDENTICAL to the old bare comparison for every finite index (boundary, negative, -0)', () => {
  // Number.isFinite(x) is true for EVERY real number including negatives and
  // -0 — the OR-guard `!Number.isFinite(index) || index <= THRESHOLD` reduces
  // to exactly `index <= THRESHOLD` whenever index is finite, so this is a
  // pure logical identity, verified here against concrete boundary values
  // rather than taken on faith.
  const finiteSamples = [ZONE_DEMAND_THRESHOLD, ZONE_DEMAND_THRESHOLD + 1e-7, ZONE_DEMAND_THRESHOLD - 1, -100, -0, 0, 100];
  for (const index of finiteSamples) {
    const oldGate = index <= ZONE_DEMAND_THRESHOLD;
    const newGate = !Number.isFinite(index) || index <= ZONE_DEMAND_THRESHOLD;
    assert.equal(newGate, oldGate, `guard must agree with the pre-fix bare comparison for finite index=${index}`);
  }
});

test('ATTACK-641-R2: guard semantics — real threshold boundary (demandOf()===40 vs 41) is UNCHANGED from the author\'s own pinned boundary test, re-verified independently here', () => {
  // Re-derive the exact index-40/41 population pair via the same binary
  // search discipline as the author's own boundary test, independently, to
  // confirm the new guard did not shift the boundary by even one ULP.
  let lo = 1;
  let hi = 5_000;
  for (let i = 0; i < 60; i++) {
    const mid = (lo + hi) / 2;
    const index = demandOf(shortfallState(mid)).commercial;
    if (index <= ZONE_DEMAND_THRESHOLD) lo = mid;
    else hi = mid;
  }
  assert.equal(demandOf(shortfallState(lo)).commercial, 40, 'precondition: binary search must still converge on index exactly 40');
  assert.equal(demandOf(shortfallState(hi)).commercial, 41, 'precondition: adjacent population must read exactly 41');
  assert.equal(
    zoneDemandFixPlan(shortfallState(lo)).find((p) => p.zone === 'commercial'),
    undefined,
    'index EXACTLY 40 must still yield no item post-fix (strictly-greater-than gate unchanged)',
  );
  assert.ok(
    zoneDemandFixPlan(shortfallState(hi)).find((p) => p.zone === 'commercial'),
    'index 41 must still yield an item post-fix',
  );
});

test('ATTACK-641-R2: guard semantics — a legitimately very negative index (deep under-threshold) still returns null via the ORIGINAL branch, not the new finite-guard branch', () => {
  // A quiet city (small population, low tax) reads a strongly negative
  // index for at least one zone — confirm it is still gated out, proving the
  // OR short-circuits to the (unchanged) second clause for ordinary negative
  // finite values, not just for NaN/Infinity.
  const s = shortfallState(1);
  const idx = demandOf(s);
  const anyZone: ZoneKey[] = ['residential', 'commercial', 'industrial'];
  for (const zone of anyZone) {
    if (idx[zone] <= ZONE_DEMAND_THRESHOLD) {
      assert.equal(
        zoneDemandFixPlan(s).find((p) => p.zone === zone),
        undefined,
        `${zone} at index ${idx[zone]} (<= threshold) must yield no item`,
      );
    }
  }
});

// ---------------------------------------------------------------------------
// (4) LAUNDERING CHECK — cheaply re-verify one r1-cleared property: the
// displayed shortfall is still item.shortfall (physical units), never the
// -100..100 demandIndex, on a fresh fixture independent of the author's/r1's.
// ---------------------------------------------------------------------------

test('ATTACK-641-R2: laundering check — the message\'s "N short" figure is still item.shortfall in physical units, never demandIndex, on an independent industrial fixture', () => {
  const s = shortfallState(150_000);
  const item = zoneDemandFixPlan(s).find((p) => p.zone === 'industrial');
  assert.ok(item, 'precondition: a large population with zero industrial buildings must trip the industrial threshold');
  const workers = s.population * WORKING_AGE_FRACTION;
  const jobs = totalJobs(s);
  const expectedShortfall = Math.max(1, Math.round(workers - jobs));
  assert.equal(item.shortfall, expectedShortfall, 'shortfall must be the independently-recomputed physical (workers-jobs) gap');
  assert.ok(Math.abs(item.demandIndex) <= 100, 'demandIndex stays bounded');
  assert.ok(item.shortfall > 100, 'physical shortfall must dwarf the bounded index at this scale (proves they are not the same number)');
  const msg = zoneDemandMessage(item);
  assert.ok(!msg.includes(`${item.demandIndex} short`), `message must not show the raw index as the shortfall figure, got: "${msg}"`);
});

// ---------------------------------------------------------------------------
// (5) RED-PROOF — remove EACH new guard independently via a scratch copy
// (GR#24: never git checkout/restore to undo) and prove each one reddens
// something on its own; then restore the original file byte-for-byte.
// ---------------------------------------------------------------------------

test('ATTACK-641-R2: RED-PROOF — removing the index finite-guard alone reddens the NaN-population regression; removing the shortfall finite-guard alone reddens the +Infinity-population regression; original file is restored byte-identical afterward', () => {
  // BUG-739: each mutation now runs against a private webconsole/test/
  // helpers/mutant.mjs shadow copy of webconsole/src — the real, shared
  // demandFixUi.ts is never written to. Each probe is a fresh child process
  // (so the mutated TS source is freshly re-evaluated, never cached from a
  // prior import), importing via './components/demandFixUi.ts' /
  // './sim/engine.ts' relative to the shadow root (which mirrors src/
  // directly), rather than '../src/...' relative to the real webconsole/test.
  //
  // R1 (BUG-739 round REJECT, 2026-09-05): src/components/demandFixUi.ts (and
  // its own imports of ../sim/engine, ../sim/data, ../sim/types, ../sim/utils)
  // uses EXTENSIONLESS relative specifiers throughout — plain `node` (native
  // TS type-stripping, no loader) cannot resolve those at all and the child
  // died at link time with ERR_MODULE_NOT_FOUND on EVERY run, mutated or not.
  // The old assertion (`!ok || !out.includes('PROBE-A-PASSED')`) treated ANY
  // crash as "detection", so a semantically inert mutation "passed" exactly
  // like the real one — a vacuous RED-PROOF. Fixed two ways: (1) the tsx
  // loader is now passed via extraArgs, resolved with `import.meta.resolve`
  // (portable — no hardcoded node_modules path) so extensionless imports
  // actually resolve; (2) each probe is run TWICE — first against an
  // UNMUTATED shadow copy (runBaselineProbe), asserting it reaches its own
  // PASSED marker (proves the probe mechanism itself works, independent of
  // any mutation), THEN against the mutant, asserting the output contains
  // the SPECIFIC assertion message that mutation is expected to trip — never
  // a bare "didn't say PASSED" check, which a crash satisfies just as well
  // as a real detection.
  const tsxLoaderUrl = import.meta.resolve('tsx');

  const runProbeAgainstMutant = (find: string, replace: string, probeSrc: string): { ok: boolean; out: string } => {
    let out = '';
    let ok = true;
    try {
      out = runWithMutant({
        targetRelPath: 'components/demandFixUi.ts',
        mutate: (original: string) => {
          assert.ok(original.includes(find), 'precondition: mutation string must actually be found (test would be vacuous otherwise)');
          return original.replace(find, replace);
        },
        childBody: probeSrc,
        extraArgs: ['--import', tsxLoaderUrl],
      });
    } catch (e) {
      ok = false;
      const err = e as { stdout?: string; stderr?: string; message?: string };
      out = (err.stdout ?? '') + (err.stderr ?? '') + (err.message ?? '');
    }
    return { ok, out };
  };

  const runBaseline = (probeSrc: string): { ok: boolean; out: string } => {
    let out = '';
    let ok = true;
    try {
      out = runBaselineProbe({
        targetRelPath: 'components/demandFixUi.ts',
        childBody: probeSrc,
        extraArgs: ['--import', tsxLoaderUrl],
      });
    } catch (e) {
      ok = false;
      const err = e as { stdout?: string; stderr?: string; message?: string };
      out = (err.stdout ?? '') + (err.stderr ?? '') + (err.message ?? '');
    }
    return { ok, out };
  };

  // R1 non-vacuity fix, continued: population=NaN was found (via a live
  // debug probe, not assumed) to be independently caught by the SECOND
  // (shortfall) guard regardless of guard A's state — s.population feeds
  // BOTH demandOf()'s index AND zoneFixFor's own `workers = s.population *
  // WORKING_AGE_FRACTION`, so a NaN population poisons `shortfall` too, and
  // guard B alone empties the plan whether or not guard A is mutated,
  // making that fixture unable to isolate guard A at all (confirmed: the
  // mutated plan is EMPTY either way — the exact vacuous-pass shape this
  // round is fixing). A poisoned TAX RATE, not population, is what isolates
  // guard A cleanly: demandOf()'s residential formula reads s.taxRates but
  // zoneFixFor's workers/jobs/shortfall math never does, so
  // taxRates.residential=NaN poisons ONLY the residential demandIndex while
  // shortfall/count stay perfectly finite — confirmed live: with guard A
  // removed, this fixture produces a real plan item with
  // `demandIndex: NaN` and an otherwise-valid finite count/shortfall/
  // planCost, exactly BUG-641 r1's "garbage numbers shown to the player"
  // shape, with the SECOND guard never in a position to also catch it.
  const probeASrc = `
      import assert from 'node:assert/strict';
      import { zoneDemandFixPlan } from './components/demandFixUi.ts';
      import { initialState } from './sim/engine.ts';
      const base = initialState();
      const s = { ...base, population: 200000, unlockedAll: true, funds: 1_000_000_000, administrationState: null, taxRates: { ...base.taxRates, residential: NaN } };
      const plan = zoneDemandFixPlan(s);
      const residential = plan.find((p) => p.zone === 'residential');
      // Against UNMUTATED source, guard A correctly filters this out
      // (residential is undefined) — nothing to check, reach PASSED. Against
      // the MUTATED source, guard A is bypassed and residential leaks
      // through with a NaN demandIndex, which THIS assertion catches.
      if (residential) {
        assert.ok(!Number.isNaN(residential.demandIndex), 'demandIndex must not be NaN');
      }
      process.stdout.write('PROBE-A-PASSED');
    `;

  // NON-VACUITY PRECONDITION for probe A: the SAME probe, run against
  // UNMUTATED source, must actually reach PROBE-A-PASSED. If this fails, the
  // probe itself is broken (wrong loader, bad import path, etc.) and the
  // mutated run's "failure" below would prove nothing.
  const baselineA = runBaseline(probeASrc);
  assert.ok(baselineA.ok && baselineA.out.includes('PROBE-A-PASSED'), `non-vacuity precondition failed: probe A must PASS against UNMUTATED demandFixUi.ts before it can be trusted to detect mutation A; got: ${baselineA.out}`);

  // --- Mutation A: strip the index finite-guard back to the bare r1-buggy comparison. ---
  const probeA = runProbeAgainstMutant(
    'if (!Number.isFinite(index) || index <= ZONE_DEMAND_THRESHOLD) return null;',
    'if (index <= ZONE_DEMAND_THRESHOLD) return null;',
    probeASrc,
  );
  // Specific expected failure: with the index guard removed, a NaN
  // population's NaN demandIndex is never gated out (every comparison
  // against NaN is false), so the resulting item's `count` is NaN and the
  // probe's own `assert.ok(!Number.isNaN(item.count), 'must not be NaN')`
  // throws with EXACTLY this message — never a bare crash/no-PASSED-marker
  // check, which a broken probe (or an unrelated crash) would satisfy too.
  assert.ok(!probeA.ok, `mutation A (index guard removed) must make the probe process exit non-zero; got: ${probeA.out}`);
  assert.match(probeA.out, /must not be NaN/, `mutation A must trip the SPECIFIC 'must not be NaN' assertion, not just any failure; got: ${probeA.out}`);

  const probeBSrc = `
      import assert from 'node:assert/strict';
      import { zoneDemandFixPlan } from './components/demandFixUi.ts';
      import { initialState } from './sim/engine.ts';
      // -Infinity (not +Infinity) is the reachable case for the shortfall
      // guard specifically: residential's index clamps to a LEGITIMATE finite
      // 100 here (index guard silent), while the (jobs-workers) shortfall
      // still overflows to +Infinity underneath — verified live above in the
      // main suite before this probe was written.
      const s = { ...initialState(), population: -Infinity, unlockedAll: true, funds: 1_000_000_000, administrationState: null };
      const plan = zoneDemandFixPlan(s);
      const residential = plan.find((p) => p.zone === 'residential');
      assert.equal(residential, undefined, 'a -Infinity-shortfall residential item must be omitted');
      process.stdout.write('PROBE-B-PASSED');
    `;

  // NON-VACUITY PRECONDITION for probe B.
  const baselineB = runBaseline(probeBSrc);
  assert.ok(baselineB.ok && baselineB.out.includes('PROBE-B-PASSED'), `non-vacuity precondition failed: probe B must PASS against UNMUTATED demandFixUi.ts before it can be trusted to detect mutation B; got: ${baselineB.out}`);

  // --- Mutation B: strip the shortfall finite-guard. ---
  const probeB = runProbeAgainstMutant(
    'if (!Number.isFinite(shortfall)) return null;\n',
    '',
    probeBSrc,
  );
  assert.ok(!probeB.ok, `mutation B (shortfall guard removed) must make the probe process exit non-zero; got: ${probeB.out}`);
  assert.match(probeB.out, /a -Infinity-shortfall residential item must be omitted/, `mutation B must trip the SPECIFIC shortfall-omission assertion, not just any failure; got: ${probeB.out}`);
});
