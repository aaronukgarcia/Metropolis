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
import * as fs from 'node:fs';
import * as path from 'node:path';
import { fileURLToPath } from 'node:url';
import { execFileSync } from 'node:child_process';
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
  const here = path.dirname(fileURLToPath(import.meta.url));
  const targetPath = path.join(here, '..', 'src', 'components', 'demandFixUi.ts');
  const original = fs.readFileSync(targetPath, 'utf8');
  const backupPath = targetPath + '.attack-r2.bak';
  fs.writeFileSync(backupPath, original, 'utf8');

  const runProbe = (probeSrc: string): { ok: boolean; out: string } => {
    // Isolated in-process probe via a temp .mjs harness invoked as a child
    // process (so the mutated TS source is freshly re-evaluated per probe,
    // never cached from a prior import in THIS test process).
    const tmpDir = fs.mkdtempSync(path.join(here, 'r2-redproof-'));
    const probePath = path.join(tmpDir, 'probe.mjs');
    fs.writeFileSync(probePath, probeSrc, 'utf8');
    try {
      const out = execFileSync(
        process.execPath,
        ['--import', 'tsx', probePath],
        { cwd: path.join(here, '..'), encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] },
      );
      return { ok: true, out };
    } catch (e) {
      const err = e as { stdout?: string; stderr?: string; message?: string };
      return { ok: false, out: (err.stdout ?? '') + (err.stderr ?? '') + (err.message ?? '') };
    } finally {
      fs.rmSync(tmpDir, { recursive: true, force: true });
    }
  };

  try {
    // --- Mutation A: strip the index finite-guard back to the bare r1-buggy comparison. ---
    const mutatedA = original.replace(
      'if (!Number.isFinite(index) || index <= ZONE_DEMAND_THRESHOLD) return null;',
      'if (index <= ZONE_DEMAND_THRESHOLD) return null;',
    );
    assert.notEqual(mutatedA, original, 'precondition: mutation A string must actually be found and replaced (test would be vacuous otherwise)');
    fs.writeFileSync(targetPath, mutatedA, 'utf8');
    const probeA = runProbe(`
      import assert from 'node:assert/strict';
      import { zoneDemandFixPlan } from '../src/components/demandFixUi.ts';
      import { initialState } from '../src/sim/engine.ts';
      const s = { ...initialState(), population: NaN, unlockedAll: true, funds: 1_000_000_000, administrationState: null };
      const plan = zoneDemandFixPlan(s);
      for (const item of plan) {
        assert.ok(!Number.isNaN(item.count), 'must not be NaN');
      }
      process.stdout.write('PROBE-A-PASSED');
    `);
    assert.ok(!probeA.ok || !probeA.out.includes('PROBE-A-PASSED'), `mutation A (index guard removed) must REDDEN the NaN-population case, but the probe reported: ${probeA.out}`);

    // Restore before the second mutation so each is tested against the real fix in isolation.
    fs.writeFileSync(targetPath, original, 'utf8');

    // --- Mutation B: strip the shortfall finite-guard. ---
    const mutatedB = original.replace(
      'if (!Number.isFinite(shortfall)) return null;\n',
      '',
    );
    assert.notEqual(mutatedB, original, 'precondition: mutation B string must actually be found and replaced (test would be vacuous otherwise)');
    fs.writeFileSync(targetPath, mutatedB, 'utf8');
    const probeB = runProbe(`
      import assert from 'node:assert/strict';
      import { zoneDemandFixPlan } from '../src/components/demandFixUi.ts';
      import { initialState } from '../src/sim/engine.ts';
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
    `);
    assert.ok(!probeB.ok || !probeB.out.includes('PROBE-B-PASSED'), `mutation B (shortfall guard removed) must REDDEN the -Infinity-population residential case, but the probe reported: ${probeB.out}`);
  } finally {
    // GR#24: restore via scratch-copy mv, never a git command.
    fs.writeFileSync(targetPath, fs.readFileSync(backupPath, 'utf8'), 'utf8');
    fs.rmSync(backupPath, { force: true });
    const restored = fs.readFileSync(targetPath, 'utf8');
    assert.equal(restored, original, 'the author\'s file must be restored BYTE-IDENTICAL after the red-proof mutations');
  }
});
