/**
 * claude-version-guard.test.js — regression tests for
 * claude-version-guard.js's commit-intercept trigger (SEC-008's
 * boundary-anchored regex; BUG-088 P0 correction, 2026-08-11).
 *
 * BUG-088 P0 CORRECTION: a prior pass of this guard's refactor (extracting
 * the GR#2 payload into claude-version-checker.js) silently ported
 * claude-author-guard.js's buildQuoteMask()/isRealGitCommit() quote-tracking
 * machinery into THIS guard's trigger — machinery that never shipped here
 * (`git show HEAD:claude-version-guard.js` has only ever had a bare boundary
 * regex test inline) and that AC-C2 explicitly requires to stay unchanged.
 * That quote mask introduced a NEW false-negative: an unbalanced/odd-count
 * quote character earlier in the command string flips the mask's parity and
 * hides a real, later `git commit` from the trigger entirely — a live
 * Destructive-agent finding, reproduced below. Reverted to the bare regex.
 *
 * This is the first test file for this hook (no prior test infra existed).
 * Unit-level only: GIT_COMMIT_RE is a pure regex exported for testing, so
 * these tests exercise it directly via require('./claude-version-guard.js')
 * rather than spawning the process — the module only touches stdin/exits
 * when require.main === module (see the guard at the bottom of the source
 * file), so requiring it here is side-effect free (no git command, no
 * filesystem check runs).
 *
 * Run: node --test claude-version-guard.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawnSync } = require('child_process');

const { GIT_COMMIT_RE } = require('./claude-version-guard.js');

const ROOT = __dirname;

test('BUG-088 P0: the Destructive\'s exact fixture (unbalanced quote in a shell comment before a real commit) now correctly triggers', () => {
  // Exact fixture from the Destructive-agent finding: a single unescaped
  // apostrophe ("don't") ahead of a REAL, later `git commit` invocation.
  const command = '"# don\'t forget to review; git commit -m x"';

  assert.equal(
    GIT_COMMIT_RE.test(command),
    true,
    'a real, later git commit must still trigger the guard even after an unbalanced quote earlier in the command'
  );

  // Load-bearing proof: the buggy quote-masked implementation this
  // correction removed (reconstructed here verbatim from this guard's own
  // working tree immediately prior to the BUG-088 fix) gets this fixture
  // WRONG — the stray apostrophe flips buildQuoteMask's parity and hides the
  // real "git commit" that follows, so isRealGitCommit returns false where
  // it must return true.
  function preFixBuggyIsRealGitCommit(cmd) {
    const buggyRe = /(?:^|[;&|(\n])\s*git\s+(?:-C\s+\S+\s+)?commit\b/g;
    function buildQuoteMask(text) {
      const mask = new Array(text.length).fill(false);
      let quote = null;
      let i = 0;
      while (i < text.length) {
        const c = text[i];
        if (quote) {
          mask[i] = true;
          if (quote === '"' && c === '\\' && i + 1 < text.length) {
            mask[i + 1] = true;
            i += 2;
            continue;
          }
          if (c === quote) quote = null;
          i++;
          continue;
        }
        if (c === '\\' && i + 1 < text.length) {
          i += 2;
          continue;
        }
        if (c === '"' || c === "'") {
          quote = c;
          mask[i] = true;
          i++;
          continue;
        }
        i++;
      }
      return mask;
    }
    const mask = buildQuoteMask(cmd);
    buggyRe.lastIndex = 0;
    let m;
    while ((m = buggyRe.exec(cmd)) !== null) {
      const gitPos = m.index + m[0].toLowerCase().lastIndexOf('git');
      if (!mask[gitPos]) return true;
    }
    return false;
  }
  assert.equal(
    preFixBuggyIsRealGitCommit(command),
    false,
    'sanity: the reconstructed pre-fix (quote-masked) trigger must reproduce the bug (false negative) on this exact fixture — proving the regression test above is load-bearing'
  );
});

test('a REAL git commit immediately after an unquoted "(" still fires', () => {
  assert.equal(GIT_COMMIT_RE.test('(git commit --author="Fake <fake@evil.com>" -m x)'), true);
});

test('a REAL git commit after unquoted ";", "&&", "|", and newline still fires', () => {
  assert.equal(GIT_COMMIT_RE.test('true; git commit -m x'), true);
  assert.equal(GIT_COMMIT_RE.test('true && git commit -m x'), true);
  assert.equal(GIT_COMMIT_RE.test('true | git commit -m x'), true);
  assert.equal(GIT_COMMIT_RE.test('true\ngit commit -m x'), true);
});

test('a bare "git commit" at the very start of the command still fires', () => {
  assert.equal(GIT_COMMIT_RE.test('git commit -m x'), true);
});

test('a command with no "git commit" phrase at all does not fire', () => {
  assert.equal(GIT_COMMIT_RE.test('npm install'), false);
  assert.equal(GIT_COMMIT_RE.test('git status'), false);
});

// Accepted, HEAD-matching limitation (NOT a regression): the bare regex has
// no notion of "inside a string literal", so a BOW comment merely quoting
// "(git commit ...)" as prose still triggers this guard's GR#2 check (an
// over-trigger — costs an extra staged-diff inspection, never a bypass; this
// guard is also fail-OPEN on internal error, so the blast radius of an
// over-trigger here is smaller than the fail-closed siblings'). This is the
// pre-BUG-043 behaviour this guard has always had at HEAD and is exactly
// what AC-C2 requires be kept unchanged; asserted here so a future attempt
// to "fix" it by silently reintroducing quote-masking is caught by this test
// failing, not by it staying green.
test('accepted limitation (matches HEAD, not a regression): a quoted mention of "(git commit ...)" as prose still over-triggers', () => {
  const command =
    'node claude-bow.js comment FEAT-040 ' +
    '"... (git commit --author=... is the exact bypass ...)"';
  assert.equal(
    GIT_COMMIT_RE.test(command),
    true,
    'the bare trigger over-triggers on this quoted mention, exactly as it did at HEAD — an accepted false positive, not a bypass'
  );
});

// End-to-end: verifies via the real hook stdin contract that a genuinely
// unrelated command (no "git commit" phrase at all) allows silently. Run in
// this repo's own working tree with no staged changes required (the
// intercept must reject the command before any `git diff --cached
// --name-only` check runs).
test('end-to-end: guard ALLOWS (silent) a command with no "git commit" phrase at all', () => {
  const { spawnSync } = require('child_process');
  const path = require('path');
  const command = 'npm install';
  const result = spawnSync(process.execPath, [path.join(__dirname, 'claude-version-guard.js')], {
    input: JSON.stringify({ tool: 'Bash', tool_input: { command } }),
    encoding: 'utf8',
  });
  assert.equal(result.status, 0);
  assert.equal(result.stdout.trim(), '', 'ALLOW is silent (empty stdout) per this guard\'s hook contract');
});

// ---------------------------------------------------------------------------
// BUG-123 regression: `git -c key=value commit` and other global-option forms
// ---------------------------------------------------------------------------

test('BUG-123: "git -c key=value commit" and stacked/-C-combined forms now fire', () => {
  assert.equal(GIT_COMMIT_RE.test('git -c foo=bar commit -m test'), true);
  assert.equal(GIT_COMMIT_RE.test('git -c user.email=x commit'), true);
  assert.equal(GIT_COMMIT_RE.test('git -c commit.gpgsign=false commit'), true);
  assert.equal(GIT_COMMIT_RE.test('git -c a=b -c c=d commit -m x'), true);
  assert.equal(GIT_COMMIT_RE.test('git -c a=b -C /some/dir commit -m x'), true);
  assert.equal(GIT_COMMIT_RE.test('git -C /some/dir -c a=b commit -m x'), true);
});

test('BUG-123: sanity — the pre-fix regex genuinely misses "git -c foo=bar commit"', () => {
  const preFixRe = /(?:^|[;&|(\n])\s*git\s+(?:-C\s+\S+\s+)?commit\b/;
  assert.equal(preFixRe.test('git -c foo=bar commit -m test'), false);
});

// ---------------------------------------------------------------------------
// BUG-123 ROUND 2 regression: Tester B's backtracking false-positive finding.
// Round 1's single-alternation regex backtracked its argument-less catch-all
// into an already-claimed -c/-C token when the trailing verb check failed,
// leaving the option's OWN VALUE unconsumed right where the verb check ran
// next — so a -c/-C value that happened to start with "commit" was misread
// as the verb. Fixed by switching claude-git-commit-trigger.js to a
// tokenizer (option-run parsed one option at a time, verb compared by exact
// Set equality, never substring/alternation match). See that module's header.
// ---------------------------------------------------------------------------

test('BUG-123 round 2: "git -c commit.gpgsign=false status" / "-c commit.template=... diff" / "-C commit-repo status" are NOT commit triggers', () => {
  assert.equal(GIT_COMMIT_RE.test('git -c commit.gpgsign=false status'), false);
  assert.equal(GIT_COMMIT_RE.test('git -c commit.template=/dev/null diff'), false);
  assert.equal(GIT_COMMIT_RE.test('git -C commit-repo status'), false);
});

test('BUG-123 round 2: sanity — the round-1 regex genuinely mis-fires on these fixtures', () => {
  const round1OptSrc =
    '(?:' +
      '-c\\s+(?:"[^"]*"|\'[^\']*\'|\\S+)' +
      '|-C\\s+(?:"[^"]*"|\'[^\']*\'|\\S+)' +
      '|--git-dir(?:=\\S+|\\s+\\S+)' +
      '|--work-tree(?:=\\S+|\\s+\\S+)' +
      '|--namespace(?:=\\S+|\\s+\\S+)' +
      '|--[A-Za-z][A-Za-z-]*(?:=\\S+)?' +
      '|-[A-Za-z]' +
    ')';
  const round1OptsRunSrc = `(?:${round1OptSrc}\\s+)*`;
  const round1Re = new RegExp(`(?:^|[;&|(\\n])\\s*git\\s+${round1OptsRunSrc}(?:commit)\\b`);
  assert.equal(round1Re.test('git -c commit.gpgsign=false status'), true);
  assert.equal(round1Re.test('git -C commit-repo status'), true);
});

// ---------------------------------------------------------------------------
// BUG-123 ROUND 4 regression: attacker Vex's quote-open-mid-token finding.
// Round 3's value grammar only recognised a quote as the option's value when
// it was the first character right after the required whitespace; the
// equally common `-c key="value with space"` shape (quote opening mid-token,
// after `=`) fell through to the bare `\S+` catch-all and leaked the value's
// tail into the verb-word position. Fixed by consumeShellToken() in
// claude-git-commit-trigger.js — see that module's header.
// ---------------------------------------------------------------------------

test('BUG-123 round 4 (Vex): quoted values with the quote opening mid-token (after "=") are handled correctly', () => {
  assert.equal(GIT_COMMIT_RE.test('git -c user.name="John Q Commit" commit -m x'), true);
  assert.equal(GIT_COMMIT_RE.test("git -c user.name='John Q Commit' commit -m x"), true);
  assert.equal(GIT_COMMIT_RE.test('git -c msg="please commit later" status'), false);
});

test('BUG-123 round 4: sanity — the round-3 value grammar genuinely mis-handles these fixtures', () => {
  const round3OptSrc =
    '(?:' +
      '-c\\s+(?:"[^"]*"|\'[^\']*\'|\\S+)' +
      '|-C\\s+(?:"[^"]*"|\'[^\']*\'|\\S+)' +
      '|--git-dir(?:=\\S+|\\s+\\S+)' +
      '|--work-tree(?:=\\S+|\\s+\\S+)' +
      '|--namespace(?:=\\S+|\\s+\\S+)' +
      '|--[A-Za-z][A-Za-z-]*(?:=\\S+)?' +
      '|-[A-Za-z]' +
    ')';
  const round3OptsRunSrc = `(?:${round3OptSrc}\\s+)*`;
  const round3Re = new RegExp(`(?:^|[;&|(\\n])\\s*git\\s+${round3OptsRunSrc}(?:commit)\\b`);
  assert.equal(round3Re.test('git -c user.name="John Q Commit" commit -m x'), false);
  assert.equal(round3Re.test('git -c msg="please commit later" status'), true);
});

// End-to-end: a hand-maintained VERSION file staged, committed via
// `git -c ... commit`, must still be DENIED — proves the real checkVersion()
// payload actually runs (not just that the regex matches in isolation).
function withThrowawayIndex(fn) {
  const gitDir = fs.mkdtempSync(path.join(os.tmpdir(), 'versionguard-bug123-throwaway-index-'));
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

test('BUG-123 end-to-end: guard still DENIES a staged hand-maintained VERSION file when committed via "git -c ... commit"', { concurrency: false }, () => {
  const fixtureDir = path.join(ROOT, `__versionguard_bug123_fixture_${process.pid}__`);
  const fixturePath = path.join(fixtureDir, 'VERSION');
  withThrowawayIndex(() => {
    try {
      fs.mkdirSync(fixtureDir, { recursive: true });
      fs.writeFileSync(fixturePath, '1.2.3\n', 'utf8');
      const relPath = path.relative(ROOT, fixturePath).split(path.sep).join('/');
      const add = spawnSync('git', ['add', '--', relPath], { cwd: ROOT, encoding: 'utf8' });
      assert.equal(add.status, 0, add.stderr);

      const result = spawnSync(process.execPath, [path.join(ROOT, 'claude-version-guard.js')], {
        cwd: ROOT,
        env: process.env,
        encoding: 'utf8',
        input: JSON.stringify({
          tool: 'Bash',
          tool_input: { command: 'git -c user.email=x@example.com -c commit.gpgsign=false commit -m "test: bug-123 e2e"' },
        }),
      });
      assert.equal(result.status, 0);
      const parsed = JSON.parse(result.stdout);
      assert.equal(
        parsed?.hookSpecificOutput?.permissionDecision,
        'deny',
        `expected a deny for a hand-maintained VERSION-shaped file staged, via "git -c ... commit". raw stdout: ${result.stdout}`
      );
      assert.match(parsed.hookSpecificOutput.permissionDecisionReason, /GOLDEN RULE #2/);
    } finally {
      fs.rmSync(fixtureDir, { recursive: true, force: true });
    }
  });
});
