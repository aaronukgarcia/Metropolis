/**
 * tools/plan/generate.test.js — regression test for BUG-023.
 *
 * generate.js used to merge security-scan ledger entries with
 * `securityScans[m.key] || null`: a ledger key matching no code.json module
 * (a typo, e.g. "tool.scretguard" instead of "tool.secretguard") vanished
 * silently — no error, no warning, nothing written anywhere. This test
 * proves the fix: generate.js now emits a `[MET-T031]` warning on stderr
 * naming the offending key, non-fatally (exit code stays 0, code.json is
 * still written).
 *
 * This is necessarily an integration-style test (spawns the real script as
 * a child process against the real repo files) rather than a unit test,
 * because generate.js is a top-level script with `process.exit()` calls,
 * not a module of exported functions — refactoring that shape is out of
 * scope for this fix. The test mutates data/security-scans.json with an
 * extra bogus key, runs the generator, and restores the original file
 * (byte-for-byte) in a `finally` block regardless of outcome, so a failing
 * assertion can never leave the repo's real ledger file mutated.
 *
 * Run: node tools/plan/generate.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');
const crypto = require('crypto');
const { spawnSync } = require('child_process');

const ROOT = path.resolve(__dirname, '..', '..');
const SCANS_PATH = path.join(ROOT, 'data', 'security-scans.json');
const GENERATE_PATH = path.join(__dirname, 'generate.js');

const sha256 = (buf) => crypto.createHash('sha256').update(buf).digest('hex');

test('generate.js warns loudly (non-fatal) on a security-scan ledger key matching no code.json module', () => {
  const originalRaw = fs.readFileSync(SCANS_PATH, 'utf8');
  try {
    const scans = JSON.parse(originalRaw);
    const BOGUS_KEY = 'tool.this-key-does-not-exist-typo';
    scans.scans[BOGUS_KEY] = {
      scannedAt: '2026-08-10T00:00:00Z',
      scanVersion: 'v1',
      by: 'test-harness',
      findings: [],
      verdict: 'clean',
    };
    fs.writeFileSync(SCANS_PATH, JSON.stringify(scans, null, 2) + '\n', 'utf8');

    const result = spawnSync(process.execPath, [GENERATE_PATH], {
      cwd: ROOT,
      encoding: 'utf8',
    });

    assert.equal(result.status, 0, `generate.js should exit 0 (non-fatal warning), got ${result.status}. stderr:\n${result.stderr}`);
    assert.match(result.stderr, /\[MET-T031\]/, 'expected a MET-T031 warning on stderr');
    assert.ok(
      result.stderr.includes(BOGUS_KEY),
      `expected the warning to name the offending key "${BOGUS_KEY}"; stderr was:\n${result.stderr}`
    );

    // The bogus key must not have leaked into code.json — it is dropped,
    // just loudly instead of silently.
    const codeJson = JSON.parse(fs.readFileSync(path.join(ROOT, 'code.json'), 'utf8'));
    assert.ok(
      !codeJson.modules.some(m => m.key === BOGUS_KEY),
      'bogus ledger key must not appear as a code.json module'
    );
  } finally {
    // Restore the real ledger byte-for-byte and regenerate code.json /
    // bow-import.json back to their pre-test state, so this test leaves no
    // residue no matter how it exits.
    fs.writeFileSync(SCANS_PATH, originalRaw, 'utf8');
    spawnSync(process.execPath, [GENERATE_PATH], { cwd: ROOT, encoding: 'utf8' });
  }
});

test('generate.js gives the documented-exemption pointer for a key listed in knownOrphanedKeys', () => {
  const result = spawnSync(process.execPath, [GENERATE_PATH], { cwd: ROOT, encoding: 'utf8' });
  assert.equal(result.status, 0);
  assert.match(
    result.stderr,
    /\[MET-T031\].*legacy\.versionguard.*documented exemption/,
    `expected a documented-exemption warning for legacy.versionguard; stderr was:\n${result.stderr}`
  );
});

// The REAL, live, single-source-of-truth master plan. Read-only in this
// file from here on — every test below that needs to mutate a plan
// operates on an isolated temp COPY (BUG-069) and redirects generate.js at
// it via the MET_PLAN_PATH env var, never touching this path.
const PLAN_PATH = path.join(ROOT, 'docs', 'planning', 'master-plan-v2.1.json');

// BUG-069 crash-safety net: hash the live plan before/after the whole
// suite runs (registered once, at module load, so it wraps every test in
// this file including future ones) and assert byte-identical. This is the
// test-of-the-test: even if a future test forgets the temp-copy pattern
// and mutates PLAN_PATH directly, this catches it — and unlike a
// try/finally restore, it has no window where the assertion itself could
// be skipped by a crash (a crash simply means this after-hook never runs,
// and the live file is left exactly as the crash left it either way; the
// point is no NORMAL test run — pass or fail — ever depends on a
// mutate-then-restore of the SSOT to stay clean).
let livePlanHashBefore;
test.before(() => {
  livePlanHashBefore = sha256(fs.readFileSync(PLAN_PATH));
});
test.after(() => {
  const livePlanHashAfter = sha256(fs.readFileSync(PLAN_PATH));
  assert.equal(
    livePlanHashAfter,
    livePlanHashBefore,
    'BUG-069 REGRESSION: docs/planning/master-plan-v2.1.json (the live SSOT) was modified during the test run'
  );
});

/**
 * Creates a temp copy of the live master plan in an isolated tmpdir and
 * returns its path plus a cleanup function. Callers mutate/write the
 * COPY and point generate.js at it via MET_PLAN_PATH — the live SSOT at
 * PLAN_PATH is never opened for writing anywhere in this file.
 */
function makePlanFixture() {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'generate-plan-fixture-'));
  const fixturePath = path.join(dir, 'master-plan-v2.1.json');
  fs.copyFileSync(PLAN_PATH, fixturePath);
  const cleanup = () => {
    fs.rmSync(dir, { recursive: true, force: true });
  };
  return { fixturePath, cleanup };
}

test('generate.js FAILS validation when a declared collaboration has no matching call edge (BUG-058 part 2 drift check)', () => {
  const { fixturePath, cleanup } = makePlanFixture();
  try {
    const plan = JSON.parse(fs.readFileSync(fixturePath, 'utf8'));
    // engine.core is depended on by nearly everything but is not in
    // engine.season's calls[] — a safe, real pair to assert a fake
    // requirement between, proving the check actually inspects calls[]
    // rather than passing unconditionally.
    const season = plan.items.find(it => it.key === 'engine.season');
    assert.ok(season, 'fixture assumption broke: engine.season no longer exists in the master plan');
    assert.ok(!(season.calls || []).includes('engine.core'), 'fixture assumption broke: engine.season already calls engine.core');
    season.collaborations = { consumesFrom: ['engine.core'] };
    fs.writeFileSync(fixturePath, JSON.stringify(plan, null, 2) + '\n', 'utf8');

    const result = spawnSync(process.execPath, [GENERATE_PATH, '--check'], {
      cwd: ROOT,
      encoding: 'utf8',
      env: { ...process.env, MET_PLAN_PATH: fixturePath },
    });

    assert.notEqual(result.status, 0, `generate.js --check should fail on an unregistered declared collaboration, got exit ${result.status}. stderr:\n${result.stderr}`);
    assert.match(result.stderr, /MET-T025/, `expected a MET-T025 collaborations-drift error; stderr was:\n${result.stderr}`);
    assert.ok(
      result.stderr.includes('engine.season') && result.stderr.includes('engine.core'),
      `expected the error to name both engine.season and engine.core; stderr was:\n${result.stderr}`
    );
  } finally {
    cleanup();
  }
});

test('BUG-070: generate.js reports live collaborations-gate coverage, and the count moves when the fixture is mutated', () => {
  const { fixturePath, cleanup } = makePlanFixture();
  try {
    const plan = JSON.parse(fs.readFileSync(fixturePath, 'utf8'));
    const total = plan.items.length;
    const before = plan.items.filter(it => it.collaborations).length;

    const baseline = spawnSync(process.execPath, [GENERATE_PATH, '--check'], {
      cwd: ROOT,
      encoding: 'utf8',
      env: { ...process.env, MET_PLAN_PATH: fixturePath },
    });
    assert.equal(baseline.status, 0, `baseline run should validate clean. stderr:\n${baseline.stderr}`);
    const baselineRe = new RegExp(`collaborations declared for ${before}/${total} modules \\(${total - before} uncovered by this gate\\)`);
    assert.match(
      baseline.stdout,
      baselineRe,
      `expected the coverage line to report ${before}/${total}; stdout was:\n${baseline.stdout}`
    );

    // Now REMOVE a collaborations field from a module that has one, and
    // prove the reported count decreases accordingly (not hardcoded).
    const withCollab = plan.items.find(it => it.collaborations);
    assert.ok(withCollab, 'fixture assumption broke: no master-plan item currently carries a collaborations field');
    delete withCollab.collaborations;
    fs.writeFileSync(fixturePath, JSON.stringify(plan, null, 2) + '\n', 'utf8');

    const afterRemove = spawnSync(process.execPath, [GENERATE_PATH, '--check'], {
      cwd: ROOT,
      encoding: 'utf8',
      env: { ...process.env, MET_PLAN_PATH: fixturePath },
    });
    assert.equal(afterRemove.status, 0, `run after removing a collaborations field should still validate clean. stderr:\n${afterRemove.stderr}`);
    const afterRemoveRe = new RegExp(`collaborations declared for ${before - 1}/${total} modules \\(${total - before + 1} uncovered by this gate\\)`);
    assert.match(
      afterRemove.stdout,
      afterRemoveRe,
      `expected the coverage line to drop to ${before - 1}/${total} after removing a collaborations field; stdout was:\n${afterRemove.stdout}`
    );

    // And ADD a collaborations field to a module that has none, and prove
    // the reported count increases accordingly. Use an item whose deps[]
    // already give it a safe, real target to declare a (satisfied)
    // collaboration against, so this doesn't also trip the drift check.
    const withoutCollab = plan.items.find(it => !it.collaborations && (it.calls || []).length > 0);
    assert.ok(withoutCollab, 'fixture assumption broke: no master-plan item without collaborations has any calls[] to declare against');
    const target = withoutCollab.calls[0];
    withoutCollab.collaborations = { consumesFrom: [target] };
    fs.writeFileSync(fixturePath, JSON.stringify(plan, null, 2) + '\n', 'utf8');

    const afterAdd = spawnSync(process.execPath, [GENERATE_PATH, '--check'], {
      cwd: ROOT,
      encoding: 'utf8',
      env: { ...process.env, MET_PLAN_PATH: fixturePath },
    });
    assert.equal(afterAdd.status, 0, `run after adding a satisfied collaborations field should validate clean. stderr:\n${afterAdd.stderr}`);
    const afterAddRe = new RegExp(`collaborations declared for ${before}/${total} modules \\(${total - before} uncovered by this gate\\)`);
    assert.match(
      afterAdd.stdout,
      afterAddRe,
      `expected the coverage line to return to ${before}/${total} after adding a collaborations field back; stdout was:\n${afterAdd.stdout}`
    );
  } finally {
    cleanup();
  }
});

test('BUG-111: concurrent generate.js invocations never leave code.json/bow-import.json torn (atomic write)', () => {
  // Real regression test for the reported failure mode: multiple concurrent
  // invocations racing on the SAME output paths used to interleave their
  // fs.writeFileSync calls, producing "Unterminated string in JSON". This
  // spawns several real generate.js runs concurrently against the live
  // repo's actual output paths (code.json, tools/plan/bow-import.json) and
  // asserts both are always fully parseable once every run has finished —
  // never truncated/interleaved. Content is deterministic (same plan, same
  // carried-over GUIDs from the code.json already on disk), so re-running
  // this repeatedly is safe and idempotent; no restore is needed, but we
  // still run once more at the end to leave the repo in its normal
  // freshly-regenerated state.
  const CONCURRENCY = 8;
  const ROUNDS = 3;
  const codeJsonPath = path.join(ROOT, 'code.json');
  const bowImportPath = path.join(ROOT, 'tools', 'plan', 'bow-import.json');

  for (let round = 0; round < ROUNDS; round++) {
    const runs = Array.from({ length: CONCURRENCY }, () =>
      spawnSync(process.execPath, [GENERATE_PATH], { cwd: ROOT, encoding: 'utf8' })
    );
    // Every concurrent run should itself succeed (atomic write means no
    // process ever observes/produces a torn file, so none should error).
    for (const [i, r] of runs.entries()) {
      assert.equal(r.status, 0, `round ${round}, run ${i} should exit 0. stderr:\n${r.stderr}`);
    }

    // The winner's output on disk, whichever process wrote last, must be
    // fully valid, parseable JSON — never a torn/interleaved fragment.
    const codeJsonRaw = fs.readFileSync(codeJsonPath, 'utf8');
    const bowImportRaw = fs.readFileSync(bowImportPath, 'utf8');
    assert.doesNotThrow(() => JSON.parse(codeJsonRaw), `round ${round}: code.json is not valid JSON after concurrent writes`);
    assert.doesNotThrow(() => JSON.parse(bowImportRaw), `round ${round}: bow-import.json is not valid JSON after concurrent writes`);
  }
});

test('BUG-111: the atomic write-then-rename pattern itself never produces a torn file under a true concurrent race', () => {
  // Isolates the mechanism (temp-file-in-same-dir + fs.renameSync) from
  // generate.js's own logic, using a large payload and many concurrent
  // writers hammering literally the same target path in a tight loop —
  // a much tighter race window than spawning full generate.js processes,
  // so this is the test most likely to catch a regression back to a naive
  // writeFileSync if the atomic helper is ever "simplified" away.
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'generate-atomic-write-'));
  const targetPath = path.join(dir, 'race-target.json');
  try {
    const workerScript = path.join(dir, 'worker.js');
    fs.writeFileSync(
      workerScript,
      `
      const fs = require('fs');
      const path = require('path');
      const crypto = require('crypto');
      const targetPath = process.argv[2];
      const marker = process.argv[3];
      const dir = path.dirname(targetPath);
      // Large-ish distinct payload per worker so a torn write (part of one
      // worker's bytes + part of another's) is very likely to break JSON
      // parsing rather than coincidentally still parsing.
      const payload = { marker, blob: marker.repeat(50000) };
      for (let i = 0; i < 20; i++) {
        const tmpPath = path.join(dir, \`.race-target.tmp-\${process.pid}-\${crypto.randomBytes(6).toString('hex')}\`);
        fs.writeFileSync(tmpPath, JSON.stringify(payload), 'utf8');
        fs.renameSync(tmpPath, targetPath);
      }
      `,
      'utf8'
    );

    const WORKERS = 6;
    const runs = Array.from({ length: WORKERS }, (_, i) =>
      spawnSync(process.execPath, [workerScript, targetPath, `w${i}-`], { encoding: 'utf8' })
    );
    for (const [i, r] of runs.entries()) {
      assert.equal(r.status, 0, `worker ${i} crashed. stderr:\n${r.stderr}`);
    }

    // After all workers finish, the target must exist and be fully valid
    // JSON belonging to exactly one worker's payload — never a mix.
    const finalRaw = fs.readFileSync(targetPath, 'utf8');
    let parsed;
    assert.doesNotThrow(() => { parsed = JSON.parse(finalRaw); }, `target file is not valid JSON after the concurrent race:\n${finalRaw.slice(0, 200)}...`);
    assert.ok(/^w\d-$/.test(parsed.marker), `unexpected marker shape: ${parsed.marker}`);
    assert.equal(parsed.blob, parsed.marker.repeat(50000), 'blob content does not match its own marker — payload was mixed with another writer\'s');
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('generate.js passes validation when a declared collaboration DOES have a matching call edge', () => {
  const { fixturePath, cleanup } = makePlanFixture();
  try {
    const plan = JSON.parse(fs.readFileSync(fixturePath, 'utf8'));
    const build = plan.items.find(it => it.key === 'engine.build');
    assert.ok(build, 'fixture assumption broke: engine.build no longer exists in the master plan');
    assert.ok((build.calls || []).includes('engine.season'), 'fixture assumption broke: engine.build no longer calls engine.season — has BUG-058\'s edge regressed?');
    build.collaborations = { consumesFrom: ['engine.season'] };
    fs.writeFileSync(fixturePath, JSON.stringify(plan, null, 2) + '\n', 'utf8');

    const result = spawnSync(process.execPath, [GENERATE_PATH, '--check'], {
      cwd: ROOT,
      encoding: 'utf8',
      env: { ...process.env, MET_PLAN_PATH: fixturePath },
    });

    assert.equal(result.status, 0, `generate.js --check should pass when the declared collaboration is already realized as a call edge. stderr:\n${result.stderr}`);
  } finally {
    cleanup();
  }
});

// ── same-path self-calls (BUG-325): MET-T022 extended to catch same-path calls ──
test('BUG-325: generate.js FAILS when a feature calls its parent module at the same path', () => {
  const { fixturePath, cleanup } = makePlanFixture();
  try {
    const plan = JSON.parse(fs.readFileSync(fixturePath, 'utf8'));
    // Find an existing module to use as a base (engine.mining has features that call it).
    const mining = plan.items.find(it => it.key === 'engine.mining');
    assert.ok(mining, 'fixture assumption broke: engine.mining no longer exists in the master plan');
    assert.ok(mining.path, 'fixture assumption broke: engine.mining has no path');

    // Create a test feature at the SAME path but with a different key, calling the parent.
    const testFeature = {
      key: 'feat.testsamepath',
      type: 'feature',
      seq: 9999,
      priority: 'P2',
      milestone: 'M4',
      layer: 'engine',
      title: 'Test feature at same path as parent — should be rejected',
      desc: 'This feature is at the same path as its parent module and calls it, a validation error.',
      specRef: 'internal test',
      path: mining.path, // SAME path as engine.mining
      deps: ['engine.mining'],
      calls: ['engine.mining'], // Calls the parent — MET-T022 should catch this
    };
    plan.items.push(testFeature);
    fs.writeFileSync(fixturePath, JSON.stringify(plan, null, 2) + '\n', 'utf8');

    const result = spawnSync(process.execPath, [GENERATE_PATH, '--check'], {
      cwd: ROOT,
      encoding: 'utf8',
      env: { ...process.env, MET_PLAN_PATH: fixturePath },
    });

    assert.notEqual(result.status, 0, `generate.js --check should fail on same-path self-call, got exit ${result.status}. stderr:\n${result.stderr}`);
    assert.match(result.stderr, /MET-T022/, `expected a MET-T022 error; stderr was:\n${result.stderr}`);
    assert.ok(
      result.stderr.includes('feat.testsamepath') && result.stderr.includes('engine.mining'),
      `expected the error to name both feat.testsamepath and engine.mining; stderr was:\n${result.stderr}`
    );
    assert.ok(
      result.stderr.includes('same path'),
      `expected the error to mention "same path"; stderr was:\n${result.stderr}`
    );
  } finally {
    cleanup();
  }
});

test('BUG-325: generate.js passes validation when features call different modules (no false positive)', () => {
  const { fixturePath, cleanup } = makePlanFixture();
  try {
    const plan = JSON.parse(fs.readFileSync(fixturePath, 'utf8'));
    const mining = plan.items.find(it => it.key === 'engine.mining');
    const world = plan.items.find(it => it.key === 'engine.world');
    assert.ok(mining && world, 'fixture assumption broke: engine.mining or engine.world missing');
    assert.notEqual(mining.path, world.path, 'fixture assumption broke: engine.mining and engine.world have the same path');

    // BUG-325 cleanup: the 11 latent same-path calls are now caught by MET-T022.
    // Remove them from the fixture so this test can verify the no-false-positive case.
    const samePathPairs = [
      ['feat.resourcesurvey', 'ui.screen.map'],
      ['feat.extraction', 'engine.mining'],
      ['feat.commoditymarket', 'engine.chemicals'],
      ['feat.facilitypermits', 'engine.build'],
      ['feat.decommission', 'engine.finance'],
      ['feat.containerport', 'engine.freight'],
      ['feat.channeltunnel', 'engine.tunnels'],
      ['feat.pharmacampus', 'engine.fdi'],
      ['feat.refinery', 'engine.chemicals'],
      ['feat.minetypes', 'engine.mining'],
      ['feat.farmtypes', 'engine.farming'],
    ];
    for (const [caller, target] of samePathPairs) {
      const item = plan.items.find(it => it.key === caller);
      if (item && item.calls) {
        item.calls = item.calls.filter(c => c !== target);
      }
    }

    // Create a feature that calls engine.world but is at mining's path — different targets, no error.
    const testFeature = {
      key: 'feat.testdifferentpath',
      type: 'feature',
      seq: 9998,
      priority: 'P2',
      milestone: 'M4',
      layer: 'engine',
      title: 'Test feature calling a different path',
      desc: 'This feature calls engine.world, a different path — should be allowed.',
      specRef: 'internal test',
      path: mining.path,
      deps: ['engine.mining'],
      calls: ['engine.world'], // Calls a different module at a different path — OK
    };
    plan.items.push(testFeature);
    fs.writeFileSync(fixturePath, JSON.stringify(plan, null, 2) + '\n', 'utf8');

    const result = spawnSync(process.execPath, [GENERATE_PATH, '--check'], {
      cwd: ROOT,
      encoding: 'utf8',
      env: { ...process.env, MET_PLAN_PATH: fixturePath },
    });

    assert.equal(result.status, 0, `generate.js --check should pass when feature calls a different path. stderr:\n${result.stderr}`);
  } finally {
    cleanup();
  }
});

// ── units registry validation (MET-T032..T038) ─────────────────────────────
// The F4 reject finding: the units validation shipped with no regression test,
// so nothing pinned the fail-closed behaviour against drift. Each case mutates
// a COPY of the plan (never the live SSOT) and asserts generate.js exits 1 with
// the matching MET-T0xx code AND writes nothing.

function runUnitsCheck(plan, mutate) {
  const { fixturePath, cleanup } = makePlanFixture();
  try {
    const p = JSON.parse(fs.readFileSync(fixturePath, 'utf8'));
    mutate(p);
    fs.writeFileSync(fixturePath, JSON.stringify(p, null, 2) + '\n', 'utf8');
    const result = spawnSync(process.execPath, [GENERATE_PATH, '--check'], {
      cwd: ROOT,
      encoding: 'utf8',
      env: { ...process.env, MET_PLAN_PATH: fixturePath },
    });
    return { result, cleanup };
  } catch (e) {
    cleanup();
    throw e;
  }
}

test('generate.js FAILS units validation on a duplicate unit key (MET-T034)', () => {
  const { result, cleanup } = runUnitsCheck(null, p => {
    p.units = (p.units || []).concat([{ ...p.units[0] }]); // duplicate key
  });
  try {
    assert.notEqual(result.status, 0, `expected exit 1 on duplicate unit key, got ${result.status}`);
    assert.match(result.stderr, /MET-T034/, `expected MET-T034; stderr:\n${result.stderr}`);
  } finally {
    cleanup();
  }
});

test('generate.js FAILS units validation on an invalid dimension (MET-T037)', () => {
  const { result, cleanup } = runUnitsCheck(null, p => {
    p.units = (p.units || []).concat([{ key: 'test.bogus', name: 'Bogus', symbol: 'X', dimension: 'not-a-dimension' }]);
  });
  try {
    assert.notEqual(result.status, 0, `expected exit 1 on invalid dimension, got ${result.status}`);
    assert.match(result.stderr, /MET-T037/, `expected MET-T037; stderr:\n${result.stderr}`);
  } finally {
    cleanup();
  }
});

test('generate.js FAILS units validation on a non-positive-integer scale (MET-T038)', () => {
  const { result, cleanup } = runUnitsCheck(null, p => {
    p.units = (p.units || []).concat([{ key: 'test.badscale', name: 'BadScale', symbol: 'X', dimension: 'money', scale: 0 }]);
  });
  try {
    assert.notEqual(result.status, 0, `expected exit 1 on scale 0, got ${result.status}`);
    assert.match(result.stderr, /MET-T038/, `expected MET-T038; stderr:\n${result.stderr}`);
  } finally {
    cleanup();
  }
});

test('generate.js FAILS units validation on a missing name/symbol (MET-T035/T036)', () => {
  const { result, cleanup } = runUnitsCheck(null, p => {
    p.units = (p.units || []).concat([{ key: 'test.noname', symbol: 'X', dimension: 'money' }]);
  });
  try {
    assert.notEqual(result.status, 0, `expected exit 1 on missing name, got ${result.status}`);
    assert.match(result.stderr, /MET-T035/, `expected MET-T035; stderr:\n${result.stderr}`);
  } finally {
    cleanup();
  }
});
