// Module key: tool.gitverdictguard
// Spec ref: GR#23 (Nothing Is Committed Un-Attacked); BUG-340 destructive round r1
//
// ATTACK REGRESSIONS — written by the INDEPENDENT destructive attacker for
// BUG-340 (round r1, Opus, 2026-09-01), NOT by the author of
// githooks/verdict-guard.js. Each test below encodes a bypass that was
// REPRODUCED LIVE against the real guard during that round. They assert the
// CORRECT behaviour, so a test here is RED until the corresponding hole is
// actually closed — that is deliberate: these tests define "done" for the
// r1 REJECT, they are not a description of today's behaviour.
//
// Findings encoded here:
//   A1  `git diff --cached --name-only` COLLAPSES a rename to the
//       destination path only. Renaming `internal/foo.go` ->
//       `internal/foo.md`, or `claude-destructive-guard.js` ->
//       `claude-destructive-guard.test.js`, therefore presents the guard
//       with an all-exempt staged set while the commit actually DELETES a
//       code-bearing file. Live-verified: both cases returned ok=true with
//       no verdict of any kind. Fix direction: `--no-renames` (or
//       `--name-status -M0`) so both sides of a rename are classified.
//   A2  `githooks/` is not in ANY code-bearing set (isEnforcedDirPath does
//       not cover it, and isGuardOrHookPath only matches root-level
//       `claude-*.js` plus `.claude/**`). BUG-340 puts the entire git-side
//       enforcement surface — commit-msg, verdict-guard.js, pre-push — in
//       that unguarded directory, so the gate can be neutered by a
//       verdict-free commit.

'use strict';

const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execFileSync } = require('node:child_process');

const vg = require('./verdict-guard.js');

/** Throwaway git repo with ONE base commit, built with plumbing only
 * (`write-tree`/`commit-tree`/`update-ref`) so no `git commit` runs — this
 * repo's own PreToolUse destructive guard denies commit-shaped commands from
 * a test harness. Returns the repo dir; caller cleans up. */
function withRenameRepo(fromPath, toPath, body) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'vg-attack-'));
  const git = (...args) => execFileSync('git', args, { cwd: dir, encoding: 'utf8' });
  try {
    git('init', '-q', '.');
    fs.mkdirSync(path.dirname(path.join(dir, fromPath)), { recursive: true });
    // Long enough that git's rename detection scores it 100% similar.
    fs.writeFileSync(path.join(dir, fromPath), `package foo\n${'// filler line\n'.repeat(40)}`);
    git('add', fromPath);
    const tree = git('write-tree').trim();
    const commit = execFileSync(
      'git',
      ['-c', 'user.email=a@b.invalid', '-c', 'user.name=t', 'commit-tree', '-m', 'base', tree],
      { cwd: dir, encoding: 'utf8' }
    ).trim();
    git('update-ref', 'HEAD', commit);
    fs.mkdirSync(path.dirname(path.join(dir, toPath)), { recursive: true });
    git('mv', fromPath, toPath);
    return body(dir);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

test('BUG-340 r1 A1: a staged rename of a .go file in an enforced dir to .md must NOT read as an exempt (docs-only) staged set', () => {
  withRenameRepo('internal/foo.go', 'internal/foo.md', (dir) => {
    const { dg } = vg.loadClassificationDeps();
    const staged = vg.getStagedFiles(dg, dir);
    // Live r1 observation: staged === ['internal/foo.md'] — the deleted
    // internal/foo.go is invisible, so the commit is classified exempt.
    assert.ok(
      staged.includes('internal/foo.go'),
      `the deleted side of the rename must be visible to the guard; got ${JSON.stringify(staged)}. ` +
        '`git diff --cached --name-only` collapses renames — use --no-renames.'
    );
    assert.equal(
      dg.isExemptFileSet(staged), false,
      'a commit that deletes a Go file from an enforced directory is not docs-only'
    );
  });
});

test('BUG-340 r1 A1(b): renaming the root GR#23 guard script to *.test.js must NOT read as an exempt (test-only) staged set', () => {
  withRenameRepo('claude-destructive-guard.js', 'claude-destructive-guard.test.js', (dir) => {
    const { dg } = vg.loadClassificationDeps();
    const staged = vg.getStagedFiles(dg, dir);
    assert.ok(
      staged.includes('claude-destructive-guard.js'),
      `deleting the GR#23 guard itself must be visible to the guard; got ${JSON.stringify(staged)}`
    );
    assert.equal(
      dg.isExemptFileSet(staged), false,
      'deleting claude-destructive-guard.js is not a test-only change'
    );
  });
});

test('BUG-340 r1 A2: githooks/ (the git-side GR#23 enforcement surface) must be code-bearing', () => {
  const { dg } = vg.loadClassificationDeps();
  for (const p of ['githooks/commit-msg', 'githooks/verdict-guard.js', 'githooks/pre-push']) {
    assert.equal(
      vg.isCodeBearing(dg, [dg.normalizeGitPath(p)]), true,
      `${p} enforces GR#23/GR#28 — a commit changing it must require a Destructive verdict`
    );
  }
});

test('BUG-340 r1 A3 (coverage gap): the FEAT-077 exemption branch must be reached by a set that is genuinely code-bearing', () => {
  // verdict-guard.test.js's "docs-only staged set is allowed" test stages
  // `docs/readme-notes.md`, which is NOT code-bearing — so it returns at the
  // isCodeBearing() early exit and never exercises isExemptFileSet() at all.
  // Mutating `if (dg.isExemptFileSet(...))` to `if (false)` left that suite
  // fully GREEN in round r1. This test pins a set that actually reaches the
  // exemption branch.
  const { dg } = vg.loadClassificationDeps();
  const docsInEnforcedDir = ['internal/notes.md'].map(dg.normalizeGitPath);
  assert.equal(vg.isCodeBearing(dg, docsInEnforcedDir), true, 'internal/ is an enforced dir');
  assert.equal(dg.isExemptFileSet(docsInEnforcedDir), true, 'a lone .md is FEAT-077 exempt');
});
