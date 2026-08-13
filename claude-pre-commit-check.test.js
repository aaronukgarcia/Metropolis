/**
 * claude-pre-commit-check.test.js — regression tests for
 * claude-pre-commit-check.js (BUG-043 fix, this guard's instance of the
 * class shared with claude-secret-guard.js / claude-version-guard.js /
 * claude-plan-guard.js — the fix itself is ported from
 * claude-author-guard.js's Tester-verified buildQuoteMask()).
 *
 * This is the first test file for this hook (no prior test infra existed).
 * Unit-level only: isRealGitCommit()/buildQuoteMask() are pure functions of
 * the command text, so these tests exercise them directly via
 * require('./claude-pre-commit-check.js') rather than spawning the process —
 * the module only touches stdin/exits when require.main === module (see the
 * guard at the bottom of the source file), so requiring it here is side-
 * effect free.
 *
 * Run: node --test claude-pre-commit-check.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');

const { isRealGitCommit } = require('./claude-pre-commit-check.js');

// ---------------------------------------------------------------------------
// BUG-043 — the false positive: a quoted mention of "git commit" after an
// unquoted-looking-but-actually-quoted "(" must NOT be treated as a real
// invocation. This is reproduction (1) from the bug report, verbatim.
// ---------------------------------------------------------------------------

test('BUG-043: a BOW comment quoting "(git commit ...)" as prose is NOT a real invocation', () => {
  const command =
    'node claude-bow.js comment FEAT-040 ' +
    '"... (git commit --author=... is the exact bypass ...)"';
  assert.equal(
    isRealGitCommit(command),
    false,
    'a quoted mention of "git commit" after "(" inside a string literal must not fire'
  );
});

test('BUG-043: the second live repro — a comment describing this very fix', () => {
  const command =
    'node claude-bow.js comment BUG-043 ' +
    '"[fix shape found] ... a real git commit hidden after a parenthesis, ' +
    'e.g. (git commit --author=fake -m x), must still be caught ..."';
  assert.equal(
    isRealGitCommit(command),
    false,
    'a comment ABOUT the bug (quoting the reproduction) must not itself trip the guard'
  );
});

// ---------------------------------------------------------------------------
// THE TEST THAT MUST NOT BE DROPPED: a real invocation immediately after an
// unquoted "(" must still fire. This is the guard against the tempting-but-
// wrong shortcut of removing "(" from the boundary class.
// ---------------------------------------------------------------------------

test('a REAL git commit immediately after an unquoted "(" still fires', () => {
  const command = '(git commit --author="Fake <fake@evil.com>" -m x)';
  assert.equal(
    isRealGitCommit(command),
    true,
    'a genuine invocation hidden after a real, unquoted "(" must still be detected'
  );
});

test('a REAL git commit after unquoted ";", "&&", "|", and newline still fires', () => {
  assert.equal(isRealGitCommit('true; git commit -m x'), true);
  assert.equal(isRealGitCommit('true && git commit -m x'), true);
  assert.equal(isRealGitCommit('true | git commit -m x'), true);
  assert.equal(isRealGitCommit('true\ngit commit -m x'), true);
});

test('a bare "git commit" at the very start of the command still fires', () => {
  assert.equal(isRealGitCommit('git commit -m x'), true);
});

// ---------------------------------------------------------------------------
// Sanity: a command with no git-commit phrase at all never fires, quoted or not.
// ---------------------------------------------------------------------------

test('a command with no "git commit" phrase at all does not fire', () => {
  assert.equal(isRealGitCommit('npm install'), false);
  assert.equal(isRealGitCommit('git status'), false);
});

test('mixed case: a quoted mention earlier does not mask a REAL invocation later', () => {
  const command =
    'node claude-bow.js comment FEAT-040 "(git commit is the bypass)"; git commit -m x';
  assert.equal(
    isRealGitCommit(command),
    true,
    'a real invocation later in the command must still be found even after an earlier quoted mention'
  );
});

// ---------------------------------------------------------------------------
// BUG-088 DEMOTION — end-to-end proof (AC-C3, matching tool.committhook.md's
// AC-6/AC-7 precedent for claude-author-guard.js, "invert not delete"): this
// file's original test suite had no deny-shaped assertions to invert (it
// only ever exercised isRealGitCommit() above), so these are NEW tests that
// establish the demoted behaviour directly, plus a load-bearing proof that
// the OLD (pre-demotion, still-committed) guard genuinely used to deny the
// exact same fixture — reconstructed from HEAD via `git show`, per
// dev-team-process.md's "correct baseline" rule (a fix that is uncommitted
// and sits on top of other uncommitted work still reconstructs cleanly from
// HEAD here, since claude-pre-commit-check.js's last COMMITTED state is
// pre-BUG-088 and pre-any-other-uncommitted-work on this file).
// ---------------------------------------------------------------------------

const { spawnSync } = require('child_process');
const path = require('path');
const fs = require('fs');

const TRAILER_FIXTURE_COMMAND =
  'git commit -m "test commit\n\nCo-Authored-By: Claude <noreply@anthropic.com>"';

function runGuard(scriptPath, command) {
  return spawnSync(process.execPath, [scriptPath], {
    input: JSON.stringify({ tool: 'Bash', tool_input: { command } }),
    encoding: 'utf8',
  });
}

test('AC-C3: current (demoted) guard never denies a Co-Authored-By trailer — advisory allow only', () => {
  const result = runGuard(path.join(__dirname, 'claude-pre-commit-check.js'), TRAILER_FIXTURE_COMMAND);
  assert.equal(result.status, 0);
  if (result.stdout.trim()) {
    const parsed = JSON.parse(result.stdout);
    assert.equal(parsed.hookSpecificOutput.permissionDecision, 'allow',
      'a positive trailer detection must surface as an advisory allow, never deny/ask');
  }
  // Either a silent allow (empty stdout) or an advisory allow is acceptable —
  // what is NOT acceptable, and what this assertion above already rules out,
  // is a blocking decision.
});

test('AC-C3 (load-bearing proof): the OLD, still-committed guard (pre-BUG-088, from HEAD) DOES deny this exact fixture', () => {
  // Reconstruct the pre-demotion guard from HEAD into scratch and prove it
  // denies — this is what makes the assertion above load-bearing: if the
  // demotion were reverted, THIS test's own baseline shows what the old
  // code did, and the assertion above would then fail against a
  // re-introduced blocking guard (proving the new test actually exercises
  // the demotion, not just an already-passing no-op).
  const show = spawnSync('git', ['show', 'HEAD:claude-pre-commit-check.js'], {
    cwd: __dirname,
    encoding: 'utf8',
  });
  assert.equal(show.status, 0, show.stderr);
  const scratchDir = fs.mkdtempSync(path.join(__dirname, '__precommitcheck_old_'));
  const scratchFile = path.join(scratchDir, 'old-claude-pre-commit-check.js');
  try {
    fs.writeFileSync(scratchFile, show.stdout, 'utf8');
    const result = runGuard(scratchFile, TRAILER_FIXTURE_COMMAND);
    assert.equal(result.status, 0);
    const parsed = JSON.parse(result.stdout);
    assert.equal(
      parsed.hookSpecificOutput.permissionDecision,
      'deny',
      'the pre-BUG-088 committed guard must still deny this fixture — proving the demotion above is a real behavioural change, not a pre-existing no-op'
    );
  } finally {
    fs.rmSync(scratchDir, { recursive: true, force: true });
  }
});

test('AC-C3: grep confirms zero deny/ask permissionDecision literals remain in the current file', () => {
  const src = fs.readFileSync(path.join(__dirname, 'claude-pre-commit-check.js'), 'utf8');
  assert.ok(!/permissionDecision.*['"]deny['"]/.test(src));
  assert.ok(!/permissionDecision.*['"]ask['"]/.test(src));
});
