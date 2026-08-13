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
// the OLD (pre-demotion) guard genuinely used to deny the exact same
// fixture.
//
// BUG-199: this proof originally reconstructed the "old" guard from
// `git show HEAD:claude-pre-commit-check.js`, which only worked because at
// the time the demotion itself was still uncommitted. Once the demotion
// landed (b96559a wave), HEAD started holding the demoted (permissive)
// guard, and the proof self-falsified on every clean checkout. Fixed by
// walking `git log` for this file from HEAD backwards and testing each
// revision's content until one is found that actually denies the fixture —
// that revision is, by construction, the last pre-demotion revision,
// discovered dynamically so it never rots as more commits land. If no
// denying revision is ever found in the walked history, this fails loudly
// (never silently skips) per this project's verification standards.
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

// Runs the guard's source text (as a string) against the trailer fixture by
// materialising it to a scratch file first (the guard is a script, not a
// requirable pure function, once we're reconstructing arbitrary historical
// revisions). Returns { denies, result } — denies is true only when the
// guard produced a well-formed deny decision; any other outcome (allow,
// crash, malformed JSON) means denies is false.
function revisionDeniesFixture(sourceText) {
  const scratchDir = fs.mkdtempSync(path.join(__dirname, '__precommitcheck_old_'));
  const scratchFile = path.join(scratchDir, 'old-claude-pre-commit-check.js');
  try {
    fs.writeFileSync(scratchFile, sourceText, 'utf8');
    const result = runGuard(scratchFile, TRAILER_FIXTURE_COMMAND);
    if (result.status !== 0 || !result.stdout || !result.stdout.trim()) {
      return { denies: false, result };
    }
    let parsed;
    try {
      parsed = JSON.parse(result.stdout);
    } catch {
      return { denies: false, result };
    }
    const denies = parsed?.hookSpecificOutput?.permissionDecision === 'deny';
    return { denies, result, parsed };
  } finally {
    fs.rmSync(scratchDir, { recursive: true, force: true });
  }
}

// Walks `git log` for claude-pre-commit-check.js from HEAD backwards and
// returns the content (via `git show <sha>:<path>`) of the FIRST (i.e. most
// recent) revision whose guard denies the trailer fixture. Throws loudly if
// the walk exhausts history without finding one — a gate that can't
// evaluate must not report success (this project's verification standard).
function findLastPreDemotionRevision() {
  const REL_PATH = 'claude-pre-commit-check.js';
  const log = spawnSync('git', ['log', '--format=%H', '--', REL_PATH], {
    cwd: __dirname,
    encoding: 'utf8',
  });
  if (log.status !== 0) {
    throw new Error(
      `AC-C3 setup failed: \`git log -- ${REL_PATH}\` exited ${log.status}: ${log.stderr}`
    );
  }
  const shas = log.stdout.split('\n').map((s) => s.trim()).filter(Boolean);
  if (shas.length === 0) {
    throw new Error(
      `AC-C3 setup failed: git log returned no revisions for ${REL_PATH} — cannot search for a pre-demotion baseline.`
    );
  }
  for (const sha of shas) {
    const show = spawnSync('git', ['show', `${sha}:${REL_PATH}`], {
      cwd: __dirname,
      encoding: 'utf8',
    });
    if (show.status !== 0) {
      // File may not have existed yet at this revision (renamed/added
      // later) — skip rather than fail the whole walk.
      continue;
    }
    const { denies } = revisionDeniesFixture(show.stdout);
    if (denies) {
      return { sha, source: show.stdout };
    }
  }
  throw new Error(
    `AC-C3 load-bearing proof FAILED LOUDLY: walked ${shas.length} revision(s) of ${REL_PATH} ` +
      `in git history and NOT ONE denied the Co-Authored-By trailer fixture. Either the ` +
      `pre-demotion guard never existed in this history, or the demotion predates the oldest ` +
      `revision walked — this test cannot prove its intended historical claim and must not be ` +
      `treated as passing/skipping silently.`
  );
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

test('AC-C3 (load-bearing proof): the last pre-demotion revision (discovered by walking git log) DOES deny this exact fixture', () => {
  // Dynamically discover the last pre-demotion revision by walking git log
  // from HEAD backwards (BUG-199 fix — no hardcoded SHA, so this can't rot
  // as more commits land on top of the demotion). This is what makes the
  // assertion above load-bearing: if the demotion were reverted, THIS
  // test's own baseline shows what the old code did, and the assertion
  // above would then fail against a re-introduced blocking guard (proving
  // the new test actually exercises the demotion, not just an
  // already-passing no-op). If history never contained a denying revision,
  // findLastPreDemotionRevision() throws instead of letting this pass or
  // skip silently.
  const { sha, source } = findLastPreDemotionRevision();
  const { denies, result, parsed } = revisionDeniesFixture(source);
  assert.equal(result.status, 0, `guard process from revision ${sha} exited non-zero: ${result.stderr}`);
  assert.ok(
    denies,
    `revision ${sha} was found by the walk but re-verification did not deny — parsed output: ${JSON.stringify(parsed)}`
  );
  assert.equal(
    parsed.hookSpecificOutput.permissionDecision,
    'deny',
    'the last pre-demotion committed guard must deny this fixture — proving the demotion above is a real behavioural change, not a pre-existing no-op'
  );
});

test('AC-C3: grep confirms zero deny/ask permissionDecision literals remain in the current file', () => {
  const src = fs.readFileSync(path.join(__dirname, 'claude-pre-commit-check.js'), 'utf8');
  assert.ok(!/permissionDecision.*['"]deny['"]/.test(src));
  assert.ok(!/permissionDecision.*['"]ask['"]/.test(src));
});
