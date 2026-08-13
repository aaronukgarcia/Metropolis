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
