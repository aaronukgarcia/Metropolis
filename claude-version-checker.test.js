/**
 * claude-version-checker.test.js — BUG-088 extraction proof for
 * claude-version-checker.js.
 *
 * Proves:
 *   1. AC-B4: no boundary-regex/quote-mask/engage-decision trigger machinery.
 *   2. AC-B2: header names the ASM-386 verb-coverage gap.
 *   3. AC-D3: checkVersion() reproduces claude-version-guard.js's own
 *      hand-maintained-file / hardcoded-semver detection against real
 *      staged fixtures.
 *   4. AC-E1/AC-F1/Section-C: three-state contract; internal error is its
 *      own state (never silently "clean") — and the header explicitly
 *      documents that a CALLER should treat this checker's internal-error
 *      as fail-open, the one deliberate divergence from the other three.
 *
 * Run: node --test claude-version-checker.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawnSync } = require('child_process');

const ROOT = __dirname;
const checker = require('./claude-version-checker.js');

// ---------------------------------------------------------------------------
// BUG-088 P1 CORRECTION (2026-08-11): AC-D3 below used to `git add`/
// `git reset` straight against THIS PROJECT'S OWN .git/index — a
// Destructive-agent finding (concurrent `node --test` runs across this
// project's guard/checker test files collided on a real .git/index.lock and
// left a stray staged fixture in the real repo's index). checker.checkVersion()
// hardcodes `cwd: ROOT` for its git invocations, by design (BUG-088 keeps
// its payload identical to the original guard) — so the fixture still has to
// live under this project's real directory tree, but the .git INDEX it gets
// staged into does not: `git --git-dir=<throwaway> --work-tree=<ROOT> add`
// stages relative to the real file on disk into a completely separate
// throwaway index, never touching the real repo's own `.git/index`.
// checker.checkVersion()'s git calls pass no explicit `env`, so they inherit
// `process.env` — set GIT_DIR/GIT_WORK_TREE there for the duration of the
// check and restore immediately after ({ concurrency: false } below so no
// other test in this process observes the temporarily-redirected env).
// ---------------------------------------------------------------------------

function withThrowawayIndex(fn) {
  const gitDir = fs.mkdtempSync(path.join(os.tmpdir(), 'versionchecker-throwaway-index-'));
  const savedGitDir = process.env.GIT_DIR;
  const savedWorkTree = process.env.GIT_WORK_TREE;
  try {
    process.env.GIT_DIR = path.join(gitDir, '.git');
    process.env.GIT_WORK_TREE = ROOT;
    const init = spawnSync('git', ['init', '-q'], { cwd: ROOT, encoding: 'utf8' });
    assert.equal(init.status, 0, `throwaway git init failed: ${init.stderr}`);
    spawnSync('git', ['config', 'user.email', 'test@example.com'], { cwd: ROOT, encoding: 'utf8' });
    spawnSync('git', ['config', 'user.name', 'Test'], { cwd: ROOT, encoding: 'utf8' });
    return fn();
  } finally {
    if (savedGitDir === undefined) delete process.env.GIT_DIR; else process.env.GIT_DIR = savedGitDir;
    if (savedWorkTree === undefined) delete process.env.GIT_WORK_TREE; else process.env.GIT_WORK_TREE = savedWorkTree;
    fs.rmSync(gitDir, { recursive: true, force: true });
  }
}

test('AC-B4: claude-version-checker.js contains no boundary-regex/quote-mask trigger machinery', () => {
  const src = fs.readFileSync(path.join(ROOT, 'claude-version-checker.js'), 'utf8');
  assert.ok(!/buildQuoteMask|GIT_COMMIT_RE|isRealGitCommit/.test(src));
});

test('AC-B2: header names cherry-pick/revert/am + ASM-386', () => {
  const src = fs.readFileSync(path.join(ROOT, 'claude-version-checker.js'), 'utf8');
  assert.ok(/ASM-386/.test(src));
  assert.ok(/cherry-pick/.test(src));
  assert.ok(/revert/.test(src));
  assert.ok(/\bam\b/.test(src));
});

test('header documents this checker\'s deliberate fail-open-on-error posture as a caller-side decision', () => {
  const src = fs.readFileSync(path.join(ROOT, 'claude-version-checker.js'), 'utf8');
  assert.ok(/fail-OPEN|fail-open/i.test(src));
  assert.ok(/CALLER/.test(src), 'header should state this is a caller-side decision, not a fourth state');
});

// ---------------------------------------------------------------------------
// Unit: isHandMaintainedVersionFile / VERSION_GO_RE parity with the original
// guard's detection rules.
// ---------------------------------------------------------------------------

test('isHandMaintainedVersionFile recognises the retired two-file paths and a bare VERSION file', () => {
  assert.equal(checker.isHandMaintainedVersionFile('app/package.json'), true);
  assert.equal(checker.isHandMaintainedVersionFile('app/src/lib/version.ts'), true);
  assert.equal(checker.isHandMaintainedVersionFile('VERSION'), true);
  assert.equal(checker.isHandMaintainedVersionFile('some/dir/VERSION'), true);
  assert.equal(checker.isHandMaintainedVersionFile('internal/foundation/buildinfo/buildinfo.go'), false);
  assert.equal(checker.isHandMaintainedVersionFile('README.md'), false);
});

test('VERSION_GO_RE matches version.go paths but not the buildinfo.go exemption target', () => {
  assert.equal(checker.BUILDINFO_EXEMPT_PATH, 'internal/foundation/buildinfo/buildinfo.go');
  assert.ok(checker.VERSION_GO_RE.test('internal/foundation/buildinfo/version.go'));
  assert.ok(!checker.VERSION_GO_RE.test(checker.BUILDINFO_EXEMPT_PATH));
});

// ---------------------------------------------------------------------------
// AC-D3: fixture-parity end-to-end — a docs-only staged commit is exempt
// (clean), matching the original guard's EXEMPT_PATTERNS behaviour.
// ---------------------------------------------------------------------------

test('AC-D3: checkVersion() reports clean for a docs-only staged commit (EXEMPT_PATTERNS)', { concurrency: false }, () => {
  const fixtureName = `__versionchecker_docs_${process.pid}_${Date.now()}.md`;
  const fixturePath = path.join(ROOT, 'docs', fixtureName);
  withThrowawayIndex(() => {
    try {
      fs.writeFileSync(fixturePath, '# scratch fixture\n', 'utf8');
      const add = spawnSync('git', ['add', `docs/${fixtureName}`], { cwd: ROOT, encoding: 'utf8' });
      assert.equal(add.status, 0, add.stderr);
      const result = checker.checkVersion();
      assert.equal(result.status, 'clean');
    } finally {
      fs.rmSync(fixturePath, { force: true });
    }
  });
});

// ---------------------------------------------------------------------------
// AC-E1 / AC-F1: three-state contract, internal error never silently "clean".
// ---------------------------------------------------------------------------

test('AC-F1: checkVersion() returns {status:"internal-error"} (never "clean") when git diff --cached fails', () => {
  // Run the checker with GIT_DIR pointed somewhere that is not a repo, so
  // `git diff --cached --name-only` itself fails — a genuine internal
  // error distinct from "nothing staged". Run via a subprocess so the
  // env override is scoped to this one call, never touching this test
  // process's own environment or any other concurrent agent's.
  const script = `
    const checker = require(${JSON.stringify(path.join(ROOT, 'claude-version-checker.js'))});
    const result = checker.checkVersion();
    process.stdout.write(JSON.stringify({ status: result.status, hasError: result.error instanceof Error }));
  `;
  const result = spawnSync(process.execPath, ['-e', script], {
    cwd: ROOT,
    encoding: 'utf8',
    env: { ...process.env, GIT_DIR: path.join(ROOT, '__does_not_exist__.git') },
  });
  assert.equal(result.status, 0, result.stderr);
  const parsed = JSON.parse(result.stdout);
  assert.equal(parsed.status, 'internal-error');
  assert.equal(parsed.hasError, true);
});
