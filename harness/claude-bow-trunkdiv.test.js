/**
 * claude-bow-trunkdiv.test.js — regression tests for BUG-348.
 *
 * The METROPOLIS STARTUP SUMMARY (claude-bow.js printGitCheck) reported the
 * working tree's uncommitted-vs-HEAD state and ahead/behind of
 * origin/<branch>, but NEVER how far HEAD had drifted BEHIND trunk
 * (origin/main). A branch 128 commits behind trunk therefore read as a
 * healthy session (the GR#26 128-behind disaster). This suite asserts the new
 * trunk-divergence report against the pure, exported helpers:
 *
 *   - trunkDivergence(git, trunkRef) computes REAL behind/ahead counts against
 *     a throwaway repo — the check that can FAIL if the computation is wrong
 *     or removed (BUG-348's explicit test requirement: construct a branch N
 *     behind and assert the summary reports N).
 *   - formatTrunkDivergence(div) renders the GR#26 thresholds (>20 P1,
 *     >50 stop-the-line), the 0/0 healthy line, and the missing-origin/main
 *     "divergence unknown" degradation — no repo needed.
 *
 * All fixtures run against THROWAWAY repos under the OS temp dir, removed in a
 * `finally`. No DB, no network — trunkDivergence never fetches; it reads the
 * ref it is handed.
 *
 * Run: node --test claude-bow-trunkdiv.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawnSync } = require('child_process');

const bow = require('./claude-bow.js');

// ---------------------------------------------------------------------------
// Throwaway-repo scaffolding
// ---------------------------------------------------------------------------

function withTempRepo(fn) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'bug348-trunkdiv-'));
  try {
    return fn(dir);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

/** A `git(args)` runner with the SAME contract printGitCheck's inner git has:
 * returns trimmed stdout, THROWS on non-zero exit. Bound to one repo dir. */
function gitRunnerFor(dir) {
  return (args) => {
    const r = spawnSync('git', args, { cwd: dir, encoding: 'utf8' });
    if (r.status !== 0) {
      throw new Error((r.stderr || r.stdout || `git ${args.join(' ')} failed`).trim());
    }
    return (r.stdout || '').trim();
  };
}

function commit(git, dir, name) {
  fs.writeFileSync(path.join(dir, name), `${name}\n`, 'utf8');
  git(['add', '-A']);
  git(['commit', '-m', name]);
}

/** Builds a repo whose local branch `trunk` diverges from HEAD (on `main`) by
 * a known count: `behind` commits on trunk-not-HEAD, `ahead` commits on
 * HEAD-not-trunk, sharing one common base. Returns the git runner. */
function buildDivergentRepo(dir, behind, ahead) {
  const git = gitRunnerFor(dir);
  git(['init', '-b', 'main']);
  git(['config', 'user.name', 'Fixture']);
  git(['config', 'user.email', 'fixture@example.invalid']);
  commit(git, dir, 'base.txt');            // common base
  git(['checkout', '-b', 'trunk']);
  for (let i = 0; i < behind; i++) commit(git, dir, `t${i}.txt`);
  git(['checkout', 'main']);
  for (let i = 0; i < ahead; i++) commit(git, dir, `h${i}.txt`);
  return git;
}

// ---------------------------------------------------------------------------
// trunkDivergence — REAL count computation (the check that can fail)
// ---------------------------------------------------------------------------

test('trunkDivergence reports the exact behind/ahead counts (2 behind, 3 ahead)', () => {
  withTempRepo((dir) => {
    const git = buildDivergentRepo(dir, 2, 3);
    const div = bow.trunkDivergence(git, 'trunk');
    assert.equal(div.available, true);
    assert.equal(div.behind, 2, 'behind count must equal the commits added on trunk');
    assert.equal(div.ahead, 3, 'ahead count must equal the commits added on HEAD');
    assert.equal(div.ref, 'trunk');
  });
});

test('trunkDivergence reports a large behind count exactly (the 128-behind shape)', () => {
  withTempRepo((dir) => {
    // Small but distinct from ahead so a swapped left/right would be caught.
    const git = buildDivergentRepo(dir, 7, 1);
    const div = bow.trunkDivergence(git, 'trunk');
    assert.equal(div.behind, 7);
    assert.equal(div.ahead, 1);
  });
});

test('trunkDivergence reports 0/0 when HEAD is level with the trunk ref', () => {
  withTempRepo((dir) => {
    const git = buildDivergentRepo(dir, 0, 0); // HEAD == trunk == base
    const div = bow.trunkDivergence(git, 'trunk');
    assert.equal(div.available, true);
    assert.equal(div.behind, 0);
    assert.equal(div.ahead, 0);
  });
});

test('trunkDivergence returns {available:false} when origin/main is missing (fresh clone / offline)', () => {
  withTempRepo((dir) => {
    const git = buildDivergentRepo(dir, 1, 1);
    // No `origin` remote exists in the fixture — the default ref is absent.
    const div = bow.trunkDivergence(git); // default 'origin/main'
    assert.equal(div.available, false);
  });
});

test('trunkDivergence works on a detached HEAD', () => {
  withTempRepo((dir) => {
    const git = buildDivergentRepo(dir, 2, 2);
    const head = git(['rev-parse', 'HEAD']);
    git(['checkout', head]); // detach
    const div = bow.trunkDivergence(git, 'trunk');
    assert.equal(div.available, true);
    assert.equal(div.behind, 2);
    assert.equal(div.ahead, 2);
  });
});

// ---------------------------------------------------------------------------
// formatTrunkDivergence — GR#26 thresholds + degradation
// ---------------------------------------------------------------------------

test('formatTrunkDivergence: 0/0 renders a healthy "level" line', () => {
  const line = bow.formatTrunkDivergence({ available: true, behind: 0, ahead: 3, ref: 'origin/main' });
  assert.match(line, /Trunk \(origin\/main\): level \(0 behind \/ 3 ahead\)/);
  assert.doesNotMatch(line, /BEHIND/);
});

test('formatTrunkDivergence: behind>0 under threshold reports the count, no escalation', () => {
  const line = bow.formatTrunkDivergence({ available: true, behind: 5, ahead: 2, ref: 'origin/main' });
  assert.match(line, /5 BEHIND \/ 2 ahead/);
  assert.doesNotMatch(line, /P1|STOP THE LINE/);
});

test('formatTrunkDivergence: >20 behind escalates to a P1', () => {
  const line = bow.formatTrunkDivergence({ available: true, behind: 28, ahead: 35, ref: 'origin/main' });
  assert.match(line, /28 BEHIND \/ 35 ahead/);
  assert.match(line, /P1: rebase\/merge trunk before new work \(GR#26\)/);
  assert.doesNotMatch(line, /STOP THE LINE/);
});

test('formatTrunkDivergence: exactly 20 behind is NOT yet a P1 (boundary)', () => {
  const line = bow.formatTrunkDivergence({ available: true, behind: 20, ahead: 0, ref: 'origin/main' });
  assert.doesNotMatch(line, /P1|STOP THE LINE/);
});

test('formatTrunkDivergence: >50 behind is stop-the-line (the 128 case)', () => {
  const line = bow.formatTrunkDivergence({ available: true, behind: 128, ahead: 35, ref: 'origin/main' });
  assert.match(line, /128 BEHIND \/ 35 ahead/);
  assert.match(line, /STOP THE LINE: reconcile with trunk before ANY work \(GR#26\)/);
});

test('formatTrunkDivergence: exactly 50 behind is P1, not yet stop-the-line (boundary)', () => {
  const line = bow.formatTrunkDivergence({ available: true, behind: 50, ahead: 0, ref: 'origin/main' });
  assert.match(line, /P1/);
  assert.doesNotMatch(line, /STOP THE LINE/);
});

test('formatTrunkDivergence: unavailable renders "divergence unknown", never a silent zero', () => {
  const line = bow.formatTrunkDivergence({ available: false });
  assert.match(line, /divergence unknown/);
  assert.match(line, /git fetch origin/);
  assert.doesNotMatch(line, /0 behind/);
});

// ---------------------------------------------------------------------------
// End-to-end: real repo -> divergence object -> rendered line
// ---------------------------------------------------------------------------

test('trunkDivergence + formatTrunkDivergence: a 25-behind fixture renders a P1 line reporting 25', () => {
  withTempRepo((dir) => {
    const git = buildDivergentRepo(dir, 25, 1);
    const line = bow.formatTrunkDivergence(bow.trunkDivergence(git, 'trunk'));
    assert.match(line, /25 BEHIND \/ 1 ahead/);
    assert.match(line, /P1/);
  });
});
