/**
 * claude-plan-checker.test.js — BUG-088 extraction proof for
 * claude-plan-checker.js.
 *
 * Proves:
 *   1. AC-B4: no boundary-regex/quote-mask/engage-decision trigger machinery
 *      lives in this module.
 *   2. AC-B2: header names the ASM-386 verb-coverage gap.
 *   3. AC-D4: checkPlan() reproduces claude-plan-guard.js's own drift
 *      detection — a clean working tree (already regenerated) reports
 *      "clean"; documents the commit-msg-timing divergence in its header.
 *   4. AC-E1/AC-F1: three-state contract, internal error never silently
 *      downgraded to "clean" (missing generate.js).
 *
 * Run: node --test claude-plan-checker.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const path = require('path');

const ROOT = __dirname;
const checker = require('./claude-plan-checker.js');

// ---------------------------------------------------------------------------
// BUG-088 P1 CORRECTION (2026-08-11): restoring a renamed-away REAL
// production file must not itself be a single point of failure. Under an
// 8-way concurrent stress run against this project's real, shared
// tools/plan/generate.js, a transient Windows ENOENT was observed on the
// restore rename even though the backup path (unique per pid+timestamp) was
// never touched by any other process — consistent with NTFS/AV-scanner
// metadata lag under heavy concurrent I/O on the same directory rather than
// a genuine naming collision. A short bounded retry absorbs that transient
// flake without masking a real failure (it still throws after exhausting
// retries, so a genuinely stranded file is never silently swallowed).
// ---------------------------------------------------------------------------
function restoreWithRetry(from, to, attempts = 5, delayMs = 20) {
  for (let i = 1; i <= attempts; i++) {
    try {
      fs.renameSync(from, to);
      return;
    } catch (err) {
      if (i === attempts) throw err;
      const until = Date.now() + delayMs;
      while (Date.now() < until) { /* brief busy-wait; no sleep primitive in sync context */ }
    }
  }
}

test('AC-B4: claude-plan-checker.js contains no boundary-regex/quote-mask trigger machinery', () => {
  const src = fs.readFileSync(path.join(ROOT, 'claude-plan-checker.js'), 'utf8');
  assert.ok(!/buildQuoteMask|GIT_COMMIT_RE|isRealGitCommit/.test(src));
});

test('AC-B2: header names cherry-pick/revert/am + ASM-386', () => {
  const src = fs.readFileSync(path.join(ROOT, 'claude-plan-checker.js'), 'utf8');
  assert.ok(/ASM-386/.test(src));
  assert.ok(/cherry-pick/.test(src));
  assert.ok(/revert/.test(src));
  assert.ok(/\bam\b/.test(src));
});

test('AC-D4: header documents the commit-msg-timing divergence for the regeneration side effect', () => {
  const src = fs.readFileSync(path.join(ROOT, 'claude-plan-checker.js'), 'utf8');
  assert.ok(/write-tree/.test(src), 'header should reference the git write-tree timing distinction');
  assert.ok(/pre-commit/.test(src) && /commit-msg/.test(src));
});

// ---------------------------------------------------------------------------
// AC-D4 fixture parity: checkPlan() against this repo's ACTUAL working tree
// (already regenerated as of every commit, per the plan-guard's own
// enforcement) should report clean, exactly like claude-plan-guard.js's own
// hash-compare would.
// ---------------------------------------------------------------------------

test('AC-D4: checkPlan() reports {status:"clean"} against an already-regenerated working tree', () => {
  const result = checker.checkPlan();
  // If this genuinely fails, code.json/bow-import.json are ALREADY drifted
  // in this working tree independent of this test — not this test's fault,
  // but worth surfacing via the assertion message rather than a bare diff.
  assert.equal(
    result.status,
    'clean',
    `expected a clean plan-drift check against the current working tree; got ${result.status}: ${JSON.stringify(result.findings || result.error)}`
  );
});

// ---------------------------------------------------------------------------
// AC-E1 / AC-F1: three-state contract.
// ---------------------------------------------------------------------------

test('AC-F1: checkPlan() returns {status:"internal-error"} (never silently "clean") when generate.js is missing', () => {
  const real = checker.GENERATE_PATH;
  // Unique per test-run (pid+timestamp, matching the convention already
  // established in claude-secret-checker.test.js's fixture naming) so
  // concurrent `node --test` runs across this project's guard/checker test
  // files never collide on the same backup path (BUG-088 P1 correction).
  const backup = `${real}.bug088_test_backup_${process.pid}_${Date.now()}`;
  assert.ok(fs.existsSync(real), 'test setup: tools/plan/generate.js must exist');
  let renamed = false;
  try {
    fs.renameSync(real, backup);
    renamed = true;
    const result = checker.checkPlan();
    assert.equal(result.status, 'internal-error');
    assert.ok(result.error instanceof Error);
  } finally {
    // Only restore if the rename-away actually succeeded — guarantees the
    // real file is never left stranded on a thrown JS exception between the
    // rename and here (try/finally executes on a throw), while never
    // attempting a restore that would itself throw when there was nothing
    // to restore. NOT guaranteed on a hard process kill (SIGKILL/OOM-kill/
    // power-loss) between the successful rename-away and this restore step
    // — try/finally cannot run in that case, so the real file would stay
    // stranded under the backup name. This is a known, accepted residual
    // gap, not a bug.
    if (renamed) restoreWithRetry(backup, real);
  }
});

test('hashFiles() is deterministic and sensitive to content changes', () => {
  const dir = fs.mkdtempSync(path.join(ROOT, '__planchecker_test_'));
  try {
    const p = path.join(dir, 'a.json');
    fs.writeFileSync(p, '{"a":1}', 'utf8');
    const h1 = checker.hashFiles([p]);
    const h2 = checker.hashFiles([p]);
    assert.equal(h1, h2, 'same content must hash identically (determinism)');
    fs.writeFileSync(p, '{"a":2}', 'utf8');
    const h3 = checker.hashFiles([p]);
    assert.notEqual(h1, h3, 'a content change must change the hash (the whole point of the drift check)');
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});
