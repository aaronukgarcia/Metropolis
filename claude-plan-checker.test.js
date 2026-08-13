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
const os = require('os');
const path = require('path');

const ROOT = __dirname;
const checker = require('./claude-plan-checker.js');

// ---------------------------------------------------------------------------
// BUG-112 FIX (2026-08-13): the BUG-088 P1 correction above (superseded)
// still renamed the REAL, SHARED tools/plan/generate.js away and back to
// simulate "generate.js is missing" — even with a unique per-process backup
// name and a bounded restore retry, the SOURCE path being renamed away was
// still this project's one real generate.js. Any other concurrent process
// (another `node --test` run, or this session's own parallel-agent pattern)
// calling checker.checkPlan() during that window observed a spurious
// internal-error, because fs.existsSync(GENERATE_PATH) genuinely returned
// false for everyone, not just the test's own process. Confirmed via BUG-112
// (Destructive round 3 on the BUG-088 fix): reproduced at 4-way and 16-way
// concurrency.
//
// Fix: never touch the real file at all. checkPlan() resolves GENERATE_PATH
// relative to this module's own __dirname at require() time, so copying
// claude-plan-checker.js into an throwaway scratch directory (deliberately
// WITHOUT a tools/plan/generate.js alongside it) and requiring that copy
// fresh gives a checker instance whose GENERATE_PATH points at a path that
// has never existed and is never shared with any other process — no rename,
// no shared mutable state, no cross-process race, same assertion.
function loadScratchCheckerMissingGenerate() {
  const scratchDir = fs.mkdtempSync(path.join(os.tmpdir(), 'planchecker_missing_generate_'));
  const scratchModulePath = path.join(scratchDir, 'claude-plan-checker.js');
  fs.copyFileSync(path.join(ROOT, 'claude-plan-checker.js'), scratchModulePath);
  // Deliberately do NOT create scratchDir/tools/plan/generate.js — that's
  // the whole point of this fixture.
  const scratchChecker = require(scratchModulePath);
  return { scratchChecker, scratchDir };
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
  assert.ok(fs.existsSync(checker.GENERATE_PATH), 'sanity: this repo\'s real tools/plan/generate.js must exist (untouched by this test)');
  const { scratchChecker, scratchDir } = loadScratchCheckerMissingGenerate();
  try {
    const result = scratchChecker.checkPlan();
    assert.equal(result.status, 'internal-error');
    assert.ok(result.error instanceof Error);
  } finally {
    fs.rmSync(scratchDir, { recursive: true, force: true });
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
