// bug-509-tiered-population-ceiling.test.mjs — BUG-509: residential population
// ceiling ignores capacityTier.
//
// THE BUG: advance()'s `capacity` IIFE (src/sim/engine.ts, the population-growth
// ceiling just above the growth-model block) summed the FLAT per-spec base
// (`sp.residents ?? 8`) for every online residential building, never reading
// `b.capacityTier`. The canonical tiered capacity helpers — `residentsCapacity`
// and `onlineResidentsCapacity` (src/sim/data.ts) — correctly sum
// `capacityAtTier(sp, b.capacityTier ?? 0)`, and the jobs side (`totalJobs`)
// already does the tiered read. `evaluateBuildingMonitors` (engine.ts) bumps
// `building.capacityTier` AND charges the player (Building Auto-Scale outflow)
// based on utilization computed against the TIERED capacity — but the
// population ceiling the growth model converges toward never reflected that
// higher tier, so the auto-scale spend bought nothing: the city could never
// actually grow into the capacity it just paid to unlock.
//
// THE FIX: the ceiling now calls `onlineResidentsCapacity(s)` (residential-only,
// isOnline-gated, tier-aware) instead of the inline flat sum.
//
// RED proof (scratch cp/mv, restored immediately after — no git revert, per
// GR#24): the third test below copies src/sim/engine.ts to a .bak, textually
// swaps the fixed `const capacity = onlineResidentsCapacity(s);` line back to
// the original flat-sum IIFE, re-runs the deterministic ceiling test (second
// test below) as a child process against that reverted source, asserts it
// FAILS, then restores the original file from the backup in a try/finally.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { SPECS, onlineResidentsCapacity, residentsCapacity } from '../src/sim/data.ts';
import { initialState, reducer } from '../src/sim/engine.ts';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const ENGINE_PATH = path.join(__dirname, '..', 'src', 'sim', 'engine.ts');

// res_estate: flat base (tier 0) = 1500 residents; tier 2 (capacityTiers[2]) = 1815.
const SP = SPECS['res_estate'];
const FLAT_BASE = SP.residents;
const TIER = 2;
const TIERED_CAP = SP.capacityTiers[TIER];

/**
 * Places a real, road-connected res_estate building (via the ordinary 'place'
 * reducer action, so it goes through the SAME road-connectivity computation
 * advance() itself runs every tick — no synthetic `roadConnectivity: undefined`
 * bypass, which only survives until advance()'s own recompute at the top of
 * the tick), then fast-forwards its construction and bumps capacityTier
 * directly (simulating a completed Building Auto-Scale upgrade).
 */
function cityWithUpgradedResEstate(population) {
  let s = initialState();
  s = { ...s, unlockedAll: true, funds: 100_000_000 };
  s = reducer(s, { type: 'place', spec: 'res_estate', x: 5, y: 5 });
  const placed = s.buildings.find((b) => b.spec === 'res_estate');
  assert.ok(placed, 'precondition: res_estate placement succeeded');

  s = {
    ...s,
    population,
    // Strip the auto-registered building monitor so evaluateBuildingMonitors
    // cannot keep bumping capacityTier further during this test's ticks —
    // isolates the population-ceiling logic under test from the (separately
    // tested, BUG-466) auto-scale progression itself: this test wants a FIXED
    // tiered capacity to converge toward, not a moving target.
    buildingMonitors: [],
    buildings: s.buildings.map((b) =>
      b.id === placed.id
        ? { ...b, capacityTier: TIER, builtTick: s.tick - 10_000 } // long-finished construction
        : b
    ),
  };
  return { s, buildingId: placed.id };
}

test('BUG-509: residentsCapacity/onlineResidentsCapacity are tier-aware and strictly exceed the flat base at a non-zero tier', () => {
  assert.equal(FLAT_BASE, 1500, 'precondition: res_estate tier-0 base is 1500');
  assert.ok(TIERED_CAP > FLAT_BASE, 'precondition: tier 2 capacity (1815) exceeds the flat base (1500)');

  const { s } = cityWithUpgradedResEstate(0);

  assert.equal(onlineResidentsCapacity(s), TIERED_CAP, 'the online, tier-aware ceiling reads the upgraded tier');
  assert.equal(residentsCapacity(s), TIERED_CAP, 'the gross tier-aware capacity also reads the upgraded tier');
  assert.notEqual(
    onlineResidentsCapacity(s),
    FLAT_BASE,
    'tier-aware capacity must differ from (and exceed) the flat per-spec base once capacityTier > 0'
  );
});

test('BUG-509: the population growth ceiling inside advance()/tick honours the tiered capacity, not the flat base', () => {
  // Deliberately drive population ABOVE BOTH the flat base (1500) and the
  // tiered capacity (1815), landing in advance()'s deterministic
  // "over-capacity" branch:
  //   population = Math.max(capacity, popBefore - Math.ceil((popBefore - capacity) * 0.1));
  // This branch is a PURE function of `capacity` and `popBefore` only — no
  // jobs/attractiveness/migration confounds — so it isolates exactly which
  // capacity value the ceiling used. Starting at 2000:
  //   flat-sum bug (capacity=1500):   2000 - ceil(500*0.1) = 2000-50 = 1950, then
  //                                   converges DOWN to a floor of 1500.
  //   tier-aware fix (capacity=1815): 2000 - ceil(185*0.1) = 2000-19 = 1981, then
  //                                   converges DOWN to a (higher) floor of 1815.
  const population = 2000;
  assert.ok(population > FLAT_BASE && population > TIERED_CAP, 'precondition: population starts above both candidate ceilings');

  const { s: s0 } = cityWithUpgradedResEstate(population);
  let s = reducer(s0, { type: 'tick' });

  const expectedAfterOneTick = population - Math.ceil((population - TIERED_CAP) * 0.1);
  assert.equal(
    s.population,
    expectedAfterOneTick,
    `after one tick the over-capacity churn must shrink toward the TIERED capacity (${TIERED_CAP}), ` +
      `not the flat base (${FLAT_BASE}) — got ${s.population}`
  );

  // Run enough further ticks for the churn to fully converge, and confirm it
  // settles EXACTLY at the tiered capacity, never overshooting down to the
  // flat base.
  for (let i = 0; i < 200 && s.population > TIERED_CAP; i++) {
    s = reducer(s, { type: 'tick' });
  }
  assert.equal(s.population, TIERED_CAP, 'population converges to the TIERED capacity ceiling, not the flat base');
  assert.ok(TIERED_CAP > FLAT_BASE, 'sanity: the ceiling this test converged to is strictly above the flat base');
});

test('BUG-509: RED proof — reverting the fix (flat sum, no capacityTier read) reproduces the pre-fix over-capacity shrink', () => {
  const original = fs.readFileSync(ENGINE_PATH, 'utf8');
  const fixedLine = '  const capacity = onlineResidentsCapacity(s);';
  assert.ok(original.includes(fixedLine), 'precondition: the fixed one-line ceiling is present in engine.ts');

  const buggyIIFE = [
    '  const capacity = (() => {',
    '    let cap = 0;',
    '    for (const b of s.buildings) {',
    '      if (!isOnline(s, b)) continue;',
    '      const sp = SPECS[b.spec];',
    "      if (sp?.kind === 'residential') cap += sp.residents ?? 8;",
    '    }',
    '    return cap;',
    '  })();',
  ].join('\n');

  const reverted = original.replace(fixedLine, buggyIIFE);
  assert.notEqual(reverted, original, 'precondition: the textual swap actually changed the file');

  const backupPath = ENGINE_PATH + '.bug509-red-proof.bak';
  fs.copyFileSync(ENGINE_PATH, backupPath); // scratch copy, per GR#24 — never git for revert
  try {
    fs.writeFileSync(ENGINE_PATH, reverted, 'utf8');

    // Run this same test file's SECOND test (the ceiling behaviour test) as a
    // fresh, INDEPENDENT child process against the reverted source, and confirm
    // it fails. NODE_TEST_CONTEXT must be stripped from the child's env: this
    // test itself normally runs under an outer `node --test`, which sets that
    // var, and Node's test runner then treats an inherited child as a
    // subtest reporting over IPC to the (unrelated) outer runner instead of
    // exiting non-zero on failure — silently making this whole RED proof a
    // false pass. Confirmed empirically: without stripping it, the exact same
    // revert+run below exits 0 (looks green) purely because of the inherited
    // env var, even though the reverted code visibly fails when run directly.
    const nodeExe = process.execPath;
    const childEnv = { ...process.env };
    delete childEnv.NODE_TEST_CONTEXT;
    let failed = false;
    let output = '';
    try {
      output = execFileSync(
        nodeExe,
        ['--test', '--test-name-pattern=BUG-509: the population growth ceiling', fileURLToPath(import.meta.url)],
        { cwd: path.join(__dirname, '..'), encoding: 'utf8', stdio: 'pipe', env: childEnv }
      );
    } catch (err) {
      failed = true;
      output = (err.stdout || '') + (err.stderr || '');
    }

    assert.ok(failed, 'the ceiling test must FAIL against the reverted (flat-sum) engine.ts — proves the test can fail');
    assert.match(output, /not ok|fail/i, 'child test run output reports a failure against the flat-sum revert');
  } finally {
    // Restore the fixed file — scratch mv/copy only, never git (GR#24).
    fs.copyFileSync(backupPath, ENGINE_PATH);
    fs.unlinkSync(backupPath);
  }

  // Sanity: after restore, the fix is back in place.
  const restored = fs.readFileSync(ENGINE_PATH, 'utf8');
  assert.equal(restored, original, 'engine.ts is restored byte-identical to the fixed version after the RED proof');
});

test('BUG-509: the ceiling change does not disturb funds/conservation (auto-scale charging is independent of the ceiling)', () => {
  // The Building Auto-Scale charge is applied inside evaluateBuildingMonitors,
  // which runs and charges regardless of the population ceiling computed later
  // in advance() — this fix only changes WHERE the ceiling reads capacity from,
  // never anything about money flow. No monitors are registered in this state,
  // so no auto-scale charge should appear purely from the ceiling fix.
  const { s: s0 } = cityWithUpgradedResEstate(1600);
  const fundsBefore = s0.funds;
  const s = reducer(s0, { type: 'tick' });

  assert.ok(Number.isFinite(s.funds) && Number.isFinite(fundsBefore), 'funds remain finite numbers across the tick');
  const autoScaleEntries = s.ledger.filter((l) => /Auto-scaled \d+ building/.test(l.label));
  assert.equal(
    autoScaleEntries.length,
    0,
    'no building auto-scale charge fires from the ceiling fix alone (no monitors registered on this state)'
  );
});
