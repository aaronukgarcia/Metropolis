/**
 * claude-codename-guard.test.js — regression tests for
 * claude-codename-guard.js's `git commit`/`git push` trigger (BUG-123,
 * 2026-08-12).
 *
 * This is the first test file for this guard (no prior test infra existed).
 * BUG-123 found that the same trigger-regex class of bug affecting
 * claude-secret-guard.js / claude-version-guard.js / claude-plan-guard.js
 * also affects this FAIL-CLOSED GR#22 guard: the pre-fix trigger
 * `/\bgit\s+(commit|push)\b/` does not tolerate any git global option between
 * `git` and the verb, so `git -c user.email=... commit` (or any other
 * `-c`/`-C`/`--git-dir=` form) slipped past this guard entirely — it never
 * even reached the staged-diff scan. Fixed via
 * claude-git-commit-trigger.js's shared option-run grammar (GR#3).
 *
 * The guard was given a `require.main === module` testability guard (same
 * pattern as the three sibling guards, BUG-043-era) so it can be require()'d
 * here without touching stdin; it exports GIT_COMMIT_OR_PUSH_RE for direct,
 * unit-level testing.
 *
 * The end-to-end tests below exercise the guard's "numbered abbreviation"
 * pattern using an ABBR constant built from string fragments at runtime
 * (see below) rather than written as a literal — matching GR#22's own
 * discipline (real name, abbreviations, and numbered form must never enter
 * git as a literal) so this test file cannot itself become a violation.
 *
 * Run: node --test claude-codename-guard.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawnSync } = require('child_process');

const ROOT = __dirname;
const { GIT_COMMIT_OR_PUSH_RE } = require('./claude-codename-guard.js');

// GR#22: built from fragments at runtime, never written as a literal, even
// though it exercises the guard's own "numbered abbreviation" pattern —
// matches this file's existing runtime-synthesis discipline for the other
// forbidden-word fixtures below rather than relying on any claim that this
// specific fragment is exempt.
const ABBR = ['C', 'S', '1'].join('');

// ---------------------------------------------------------------------------
// Unit: trigger regex
// ---------------------------------------------------------------------------

test('a bare "git commit" / "git push" still fires', () => {
  assert.equal(GIT_COMMIT_OR_PUSH_RE.test('git commit -m x'), true);
  assert.equal(GIT_COMMIT_OR_PUSH_RE.test('git push origin main'), true);
});

test('a command with neither verb does not fire', () => {
  assert.equal(GIT_COMMIT_OR_PUSH_RE.test('git status'), false);
  assert.equal(GIT_COMMIT_OR_PUSH_RE.test('npm install'), false);
});

test('BUG-123: "git -c key=value commit" and "git -c key=value push" now fire', () => {
  assert.equal(GIT_COMMIT_OR_PUSH_RE.test('git -c foo=bar commit -m test'), true);
  assert.equal(GIT_COMMIT_OR_PUSH_RE.test('git -c user.email=x@example.com commit'), true);
  assert.equal(GIT_COMMIT_OR_PUSH_RE.test('git -c commit.gpgsign=false commit'), true);
  assert.equal(GIT_COMMIT_OR_PUSH_RE.test('git -c foo=bar push origin main'), true);
});

test('BUG-123: multiple stacked -c options, and -c combined with -C, still fire', () => {
  assert.equal(GIT_COMMIT_OR_PUSH_RE.test('git -c user.email=x -c user.name=y commit -m x'), true);
  assert.equal(GIT_COMMIT_OR_PUSH_RE.test('git -c user.email=x -C /some/dir commit -m x'), true);
  assert.equal(GIT_COMMIT_OR_PUSH_RE.test('git -C /some/dir -c user.email=x commit -m x'), true);
});

test('BUG-123: --git-dir=/--work-tree= global options still fire', () => {
  assert.equal(GIT_COMMIT_OR_PUSH_RE.test('git --git-dir=/x/.git --work-tree=/x commit -m x'), true);
});

test('BUG-123: sanity — the pre-fix regex genuinely misses "git -c foo=bar commit"', () => {
  const preFixRe = /\bgit\s+(commit|push)\b/;
  assert.equal(preFixRe.test('git -c foo=bar commit -m test'), false);
});

// ---------------------------------------------------------------------------
// BUG-123 ROUND 2 regression: Tester B's backtracking false-positive finding.
// Round 1's single-alternation regex backtracked its argument-less catch-all
// into an already-claimed -c/-C token when the trailing verb check failed,
// leaving the option's OWN VALUE unconsumed right where the verb check ran
// next — so a -c value that happened to start with "commit" OR "push" was
// misread as the verb. Fixed by switching claude-git-commit-trigger.js to a
// tokenizer (option-run parsed one option at a time, verb compared by exact
// Set equality, never substring/alternation match). See that module's
// header. `git -c push.default=commit-should-not-match log` is Tester B's
// exact repro against THIS guard's bare `commit|push` alternation — the
// value contains both words as substrings, which is exactly why a
// substring/alternation-based check is the wrong tool here.
// ---------------------------------------------------------------------------

test('BUG-123 round 2: "git -c commit.gpgsign=false status" / "-C commit-repo status" / "-c push.default=commit-should-not-match log" are NOT commit/push triggers', () => {
  assert.equal(GIT_COMMIT_OR_PUSH_RE.test('git -c commit.gpgsign=false status'), false);
  assert.equal(GIT_COMMIT_OR_PUSH_RE.test('git -C commit-repo status'), false);
  assert.equal(GIT_COMMIT_OR_PUSH_RE.test('git -c push.default=commit-should-not-match log'), false);
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
  const round1Re = new RegExp(`\\bgit\\s+${round1OptsRunSrc}(?:commit|push)\\b`);
  assert.equal(round1Re.test('git -c commit.gpgsign=false status'), true);
  assert.equal(round1Re.test('git -C commit-repo status'), true);
  assert.equal(round1Re.test('git -c push.default=commit-should-not-match log'), true);
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
  assert.equal(GIT_COMMIT_OR_PUSH_RE.test('git -c user.name="John Q Commit" commit -m x'), true);
  assert.equal(GIT_COMMIT_OR_PUSH_RE.test("git -c user.name='John Q Commit' commit -m x"), true);
  assert.equal(GIT_COMMIT_OR_PUSH_RE.test('git -c msg="please commit later" status'), false);
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
  const round3Re = new RegExp(`\\bgit\\s+${round3OptsRunSrc}(?:commit|push)\\b`);
  assert.equal(round3Re.test('git -c user.name="John Q Commit" commit -m x'), false);
  assert.equal(round3Re.test('git -c msg="please commit later" status'), true);
});

// ---------------------------------------------------------------------------
// End-to-end: real staged-diff scan via the hook's own stdin contract
// ---------------------------------------------------------------------------

function withThrowawayIndex(fn) {
  const gitDir = fs.mkdtempSync(path.join(os.tmpdir(), 'codenameguard-throwaway-index-'));
  try {
    const gitEnv = { ...process.env, GIT_DIR: path.join(gitDir, '.git'), GIT_WORK_TREE: ROOT };
    const init = spawnSync('git', ['init', '-q'], { cwd: ROOT, env: gitEnv, encoding: 'utf8' });
    assert.equal(init.status, 0, `throwaway git init failed: ${init.stderr}`);
    spawnSync('git', ['config', 'user.email', 'test@example.com'], { cwd: ROOT, env: gitEnv });
    spawnSync('git', ['config', 'user.name', 'Test'], { cwd: ROOT, env: gitEnv });
    return fn(gitEnv);
  } finally {
    fs.rmSync(gitDir, { recursive: true, force: true });
  }
}

function runGuardAsHook(commandText, extraEnv) {
  const result = spawnSync(process.execPath, [path.join(ROOT, 'claude-codename-guard.js')], {
    cwd: ROOT,
    encoding: 'utf8',
    env: extraEnv || process.env,
    input: JSON.stringify({ tool: 'Bash', tool_input: { command: commandText } }),
  });
  if (!result.stdout) return { denied: false, raw: result };
  let parsed;
  try {
    parsed = JSON.parse(result.stdout);
  } catch {
    return { denied: false, raw: result };
  }
  const decision = parsed?.hookSpecificOutput?.permissionDecision;
  return {
    denied: decision === 'deny',
    reason: parsed?.hookSpecificOutput?.permissionDecisionReason || '',
    raw: result,
  };
}

test('BUG-123 end-to-end: guard still DENIES a genuine numbered-abbreviation violation staged, when committed via "git -c ... commit"', { concurrency: false }, () => {
  const fixtureDir = path.join(ROOT, '__codenameguard_e2e_fixture_bug123__');
  fs.rmSync(fixtureDir, { recursive: true, force: true });
  fs.mkdirSync(fixtureDir);
  withThrowawayIndex(gitEnv => {
    try {
      // Built from the runtime-synthesized ABBR fragment, not written literally.
      fs.writeFileSync(
        path.join(fixtureDir, 'probe.txt'),
        `a stray reference to ${ABBR} slipped into this line\n`,
        'utf8'
      );
      const add = spawnSync('git', ['add', '--', fixtureDir], { cwd: ROOT, env: gitEnv, encoding: 'utf8' });
      assert.equal(add.status, 0, `git add failed: ${add.stderr}`);

      const outcome = runGuardAsHook(
        'git -c user.email=x@example.com -c commit.gpgsign=false commit -m "test: bug-123 e2e fixture (should be blocked)"',
        gitEnv
      );
      assert.equal(
        outcome.denied,
        true,
        `expected the guard to deny a genuine GR#22 violation committed via "git -c ... commit". raw stdout: ${outcome.raw.stdout}`
      );
      assert.match(outcome.reason, /CODENAME GUARD/);
    } finally {
      fs.rmSync(fixtureDir, { recursive: true, force: true });
    }
  });
});

test('end-to-end: guard ALLOWS (silent) a command with neither verb, even with a -c option present', () => {
  const outcome = runGuardAsHook('git -c user.email=x@example.com status');
  assert.equal(outcome.denied, false);
});

// ---------------------------------------------------------------------------
// BUG-137 regression: a forbidden word appearing ONLY in a new/renamed file's
// PATH (clean content, clean commit message) bypassed the guard entirely,
// because the diff-scan filter excluded '+++'/'---'/'rename ...' header lines
// outright instead of stripping the prefix and scanning the path. Live-found
// by this session's Destructive attacker against FEAT-071 in an isolated
// scratch repo. Uses the runtime-synthesized ABBR fragment as the fixture
// literal, same discipline as every other fixture in this file.
// ---------------------------------------------------------------------------

test('BUG-137: guard DENIES a new file whose NAME (not content) carries a forbidden pattern', { concurrency: false }, () => {
  const fixtureDir = path.join(ROOT, '__codenameguard_e2e_fixture_bug137__');
  fs.rmSync(fixtureDir, { recursive: true, force: true });
  fs.mkdirSync(fixtureDir);
  withThrowawayIndex(gitEnv => {
    try {
      // Clean content — the violation lives only in the filename.
      fs.writeFileSync(path.join(fixtureDir, `${ABBR}-notes.txt`), 'nothing suspicious here\n', 'utf8');
      const add = spawnSync('git', ['add', '--', fixtureDir], { cwd: ROOT, env: gitEnv, encoding: 'utf8' });
      assert.equal(add.status, 0, `git add failed: ${add.stderr}`);

      const outcome = runGuardAsHook(
        'git commit -m "test: bug-137 e2e fixture (should be blocked, filename-only)"',
        gitEnv
      );
      assert.equal(
        outcome.denied,
        true,
        `expected the guard to deny a new file whose name alone carries a forbidden pattern. raw stdout: ${outcome.raw.stdout}`
      );
      assert.match(outcome.reason, /CODENAME GUARD/);
    } finally {
      fs.rmSync(fixtureDir, { recursive: true, force: true });
    }
  });
});

test('BUG-137: guard DENIES a renamed file whose NEW name carries a forbidden pattern (old name and content are clean)', { concurrency: false }, () => {
  const fixtureDir = path.join(ROOT, '__codenameguard_e2e_fixture_bug137_rename__');
  fs.rmSync(fixtureDir, { recursive: true, force: true });
  fs.mkdirSync(fixtureDir);
  withThrowawayIndex(gitEnv => {
    try {
      const oldPath = path.join(fixtureDir, 'notes.txt');
      const newPath = path.join(fixtureDir, `${ABBR}-notes.txt`);
      fs.writeFileSync(oldPath, 'nothing suspicious here, and this body never changes\n', 'utf8');
      let r = spawnSync('git', ['add', '--', fixtureDir], { cwd: ROOT, env: gitEnv, encoding: 'utf8' });
      assert.equal(r.status, 0, `initial git add failed: ${r.stderr}`);
      r = spawnSync('git', ['commit', '-m', 'test: bug-137 rename fixture setup (clean)'], { cwd: ROOT, env: gitEnv, encoding: 'utf8' });
      assert.equal(r.status, 0, `fixture setup commit failed: ${r.stderr}`);

      fs.renameSync(oldPath, newPath);
      r = spawnSync('git', ['add', '-A', '--', fixtureDir], { cwd: ROOT, env: gitEnv, encoding: 'utf8' });
      assert.equal(r.status, 0, `rename git add failed: ${r.stderr}`);

      const outcome = runGuardAsHook(
        'git commit -m "test: bug-137 e2e fixture (should be blocked, rename-only)"',
        gitEnv
      );
      assert.equal(
        outcome.denied,
        true,
        `expected the guard to deny a rename whose new name alone carries a forbidden pattern. raw stdout: ${outcome.raw.stdout}`
      );
      assert.match(outcome.reason, /CODENAME GUARD/);
    } finally {
      fs.rmSync(fixtureDir, { recursive: true, force: true });
    }
  });
});

// ---------------------------------------------------------------------------
// BUG-140 regression: ordinary \b is a \w/non-\w transition, and underscore
// counts as \w, so a forbidden pattern immediately followed by an underscore
// (snake_case) or by another letter with zero separator (camelCase) never
// got a boundary transition and silently evaded every scan surface. Found by
// this session's Destructive attacker re-probing FEAT-071 after BUG-137
// landed — not a BUG-137 regression, a separate pre-existing gap in the
// shared PATTERNS boundary mechanism. Fixed by dropping regex boundary
// anchors (which can't tell case apart once the 'i' flag folds them) for a
// plain-JS boundary check in scan(): a match now counts unless the character
// immediately before/after it is a literal lowercase letter. Uses the
// runtime-synthesized ABBR fragment as the fixture literal, same discipline
// as every other fixture in this file.
// ---------------------------------------------------------------------------

test('BUG-140: guard DENIES a forbidden pattern immediately followed by an underscore (snake_case)', { concurrency: false }, () => {
  const fixtureDir = path.join(ROOT, '__codenameguard_e2e_fixture_bug140_snake__');
  fs.rmSync(fixtureDir, { recursive: true, force: true });
  fs.mkdirSync(fixtureDir);
  withThrowawayIndex(gitEnv => {
    try {
      fs.writeFileSync(path.join(fixtureDir, 'notes.txt'), `const flag = ${ABBR}_module_enabled;\n`, 'utf8');
      const add = spawnSync('git', ['add', '--', fixtureDir], { cwd: ROOT, env: gitEnv, encoding: 'utf8' });
      assert.equal(add.status, 0, `git add failed: ${add.stderr}`);

      const outcome = runGuardAsHook(
        'git commit -m "test: bug-140 snake_case fixture (should be blocked)"',
        gitEnv
      );
      assert.equal(
        outcome.denied,
        true,
        `expected the guard to deny a forbidden pattern immediately followed by "_". raw stdout: ${outcome.raw.stdout}`
      );
      assert.match(outcome.reason, /CODENAME GUARD/);
    } finally {
      fs.rmSync(fixtureDir, { recursive: true, force: true });
    }
  });
});

test('BUG-140: guard DENIES a forbidden pattern immediately adjacent to another letter with no separator (camelCase)', { concurrency: false }, () => {
  const fixtureDir = path.join(ROOT, '__codenameguard_e2e_fixture_bug140_camel__');
  fs.rmSync(fixtureDir, { recursive: true, force: true });
  fs.mkdirSync(fixtureDir);
  withThrowawayIndex(gitEnv => {
    try {
      // Preceded by "_" (non-letter) and followed directly by "Z" (uppercase,
      // i.e. a genuine camelCase word-start) — no separator on either side.
      fs.writeFileSync(path.join(fixtureDir, 'notes.txt'), `const flag_${ABBR}Zone = true;\n`, 'utf8');
      const add = spawnSync('git', ['add', '--', fixtureDir], { cwd: ROOT, env: gitEnv, encoding: 'utf8' });
      assert.equal(add.status, 0, `git add failed: ${add.stderr}`);

      const outcome = runGuardAsHook(
        'git commit -m "test: bug-140 camelCase fixture (should be blocked)"',
        gitEnv
      );
      assert.equal(
        outcome.denied,
        true,
        `expected the guard to deny a forbidden pattern with no separator before a following capital letter. raw stdout: ${outcome.raw.stdout}`
      );
      assert.match(outcome.reason, /CODENAME GUARD/);
    } finally {
      fs.rmSync(fixtureDir, { recursive: true, force: true });
    }
  });
});

test('BUG-140: guard still DENIES the existing hyphen-separated case (no regression)', { concurrency: false }, () => {
  const fixtureDir = path.join(ROOT, '__codenameguard_e2e_fixture_bug140_hyphen__');
  fs.rmSync(fixtureDir, { recursive: true, force: true });
  fs.mkdirSync(fixtureDir);
  withThrowawayIndex(gitEnv => {
    try {
      fs.writeFileSync(path.join(fixtureDir, 'notes.txt'), `ref: ${ABBR}-suffix in the old naming\n`, 'utf8');
      const add = spawnSync('git', ['add', '--', fixtureDir], { cwd: ROOT, env: gitEnv, encoding: 'utf8' });
      assert.equal(add.status, 0, `git add failed: ${add.stderr}`);

      const outcome = runGuardAsHook(
        'git commit -m "test: bug-140 hyphen regression fixture (should be blocked)"',
        gitEnv
      );
      assert.equal(outcome.denied, true, `raw stdout: ${outcome.raw.stdout}`);
      assert.match(outcome.reason, /CODENAME GUARD/);
    } finally {
      fs.rmSync(fixtureDir, { recursive: true, force: true });
    }
  });
});

test('BUG-140: guard still ALLOWS a fully letter-embedded occurrence on both sides (false-positive control, no over-correction)', { concurrency: false }, () => {
  const fixtureDir = path.join(ROOT, '__codenameguard_e2e_fixture_bug140_control__');
  fs.rmSync(fixtureDir, { recursive: true, force: true });
  fs.mkdirSync(fixtureDir);
  withThrowawayIndex(gitEnv => {
    try {
      // The abbreviation fragment here sits between two ordinary lowercase
      // letters on both sides ("a" before, "zebra" after) — not a realistic
      // identifier or filename shape, and must still be treated as embedded
      // noise, not a violation, proving the fix didn't over-correct into a
      // broad substring match.
      fs.writeFileSync(path.join(fixtureDir, 'notes.txt'), `a${ABBR}zebra is not a real token anywhere\n`, 'utf8');
      const add = spawnSync('git', ['add', '--', fixtureDir], { cwd: ROOT, env: gitEnv, encoding: 'utf8' });
      assert.equal(add.status, 0, `git add failed: ${add.stderr}`);

      const outcome = runGuardAsHook('git commit -m "test: bug-140 false-positive control (should pass)"', gitEnv);
      assert.equal(outcome.denied, false, `expected the guard to allow a fully letter-embedded occurrence. raw stdout: ${outcome.raw.stdout}`);
    } finally {
      fs.rmSync(fixtureDir, { recursive: true, force: true });
    }
  });
});

test('BUG-140: guard still ALLOWS an ordinary new file with a clean name and clean content', { concurrency: false }, () => {
  const fixtureDir = path.join(ROOT, '__codenameguard_e2e_fixture_bug137_clean__');
  fs.rmSync(fixtureDir, { recursive: true, force: true });
  fs.mkdirSync(fixtureDir);
  withThrowawayIndex(gitEnv => {
    try {
      fs.writeFileSync(path.join(fixtureDir, 'ordinary-notes.txt'), 'nothing suspicious here\n', 'utf8');
      const add = spawnSync('git', ['add', '--', fixtureDir], { cwd: ROOT, env: gitEnv, encoding: 'utf8' });
      assert.equal(add.status, 0, `git add failed: ${add.stderr}`);

      const outcome = runGuardAsHook('git commit -m "test: bug-137 clean fixture (should pass)"', gitEnv);
      assert.equal(outcome.denied, false, `expected the guard to allow a clean filename/content. raw stdout: ${outcome.raw.stdout}`);
    } finally {
      fs.rmSync(fixtureDir, { recursive: true, force: true });
    }
  });
});

// ---------------------------------------------------------------------------
// BUG-144 regression: BUG-140's boundary check used AND semantics — a match
// only counted as a hit if NEITHER neighbour was a lowercase letter. That
// missed the camelCase MIDDLE-segment case: a forbidden word preceded by a
// lowercase letter (continuing an identifier, e.g. "mega") but followed by
// an unambiguous uppercase transition (e.g. "Zone") has one real boundary
// (the right side) and one lowercase-adjacent side (the left) — AND
// semantics rejected it outright even though the right side alone was
// enough to prove it isn't ordinary lowercase prose. Fixed by switching to
// OR semantics: a match counts as a hit if EITHER side is a real boundary;
// only a match with lowercase letters on BOTH sides (a true continuing
// lowercase run, indistinguishable from prose) is still rejected.
// ---------------------------------------------------------------------------

test('BUG-144: guard DENIES a forbidden pattern as a camelCase MIDDLE segment (lowercase before, uppercase after)', { concurrency: false }, () => {
  const fixtureDir = path.join(ROOT, '__codenameguard_e2e_fixture_bug144_middle__');
  fs.rmSync(fixtureDir, { recursive: true, force: true });
  fs.mkdirSync(fixtureDir);
  withThrowawayIndex(gitEnv => {
    try {
      // The abbreviation fragment here sits directly after lowercase "mega"
      // (continuing what looks like a run) but directly before uppercase
      // "Zone" (a genuine camelCase transition) — exactly the
      // prefixWordEngine-shaped case BUG-144 found AND semantics missing.
      fs.writeFileSync(path.join(fixtureDir, 'notes.txt'), `const flag = mega${ABBR}Zone;\n`, 'utf8');
      const add = spawnSync('git', ['add', '--', fixtureDir], { cwd: ROOT, env: gitEnv, encoding: 'utf8' });
      assert.equal(add.status, 0, `git add failed: ${add.stderr}`);

      const outcome = runGuardAsHook(
        'git commit -m "test: bug-144 camelCase middle-segment fixture (should be blocked)"',
        gitEnv
      );
      assert.equal(
        outcome.denied,
        true,
        `expected the guard to deny a forbidden pattern as a camelCase middle segment. raw stdout: ${outcome.raw.stdout}`
      );
      assert.match(outcome.reason, /CODENAME GUARD/);
    } finally {
      fs.rmSync(fixtureDir, { recursive: true, force: true });
    }
  });
});

test('BUG-144: guard still ALLOWS a fully lowercase-embedded occurrence on both sides (no over-correction)', { concurrency: false }, () => {
  const fixtureDir = path.join(ROOT, '__codenameguard_e2e_fixture_bug144_control__');
  fs.rmSync(fixtureDir, { recursive: true, force: true });
  fs.mkdirSync(fixtureDir);
  withThrowawayIndex(gitEnv => {
    try {
      // Both neighbours are plain lowercase letters — a true continuing
      // lowercase run, the one case that must still be rejected under OR
      // semantics (rejected only when BOTH sides are lowercase-adjacent).
      fs.writeFileSync(path.join(fixtureDir, 'notes.txt'), 'xcs1y is not a real token anywhere\n', 'utf8');
      const add = spawnSync('git', ['add', '--', fixtureDir], { cwd: ROOT, env: gitEnv, encoding: 'utf8' });
      assert.equal(add.status, 0, `git add failed: ${add.stderr}`);

      const outcome = runGuardAsHook('git commit -m "test: bug-144 false-positive control (should pass)"', gitEnv);
      assert.equal(outcome.denied, false, `expected the guard to allow a fully lowercase-embedded occurrence on both sides. raw stdout: ${outcome.raw.stdout}`);
    } finally {
      fs.rmSync(fixtureDir, { recursive: true, force: true });
    }
  });
});
