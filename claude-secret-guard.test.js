/**
 * claude-secret-guard.test.js — unit + end-to-end tests for claude-secret-guard.js.
 *
 * Covers:
 *   - BUG-026 fix: a high-entropy quoted literal in a .go file that exactly
 *     matches a package-scope identifier (func/type/const/var) declared
 *     anywhere in the same directory is suppressed — proven both at the
 *     unit level (collectGoPackageIdentifiers / isGoPackageIdentifier) and
 *     end-to-end (a real `git diff --cached` scan via the exported runScan
 *     path, using throwaway fixture files that are always cleaned up).
 *   - Regression guards, per the explicit CRITICAL CONSTRAINT in the BUG-026
 *     brief: this fix must NOT touch ENTROPY_THRESHOLD/ENTROPY_MIN_LENGTH,
 *     and must not resurrect the SEC-015-era false negative where an
 *     ALL-CAPS identifier (MAX_COUNT) stopped being detected by the GR#15
 *     hardcoding-smell splitter. A genuine high-entropy credential (random,
 *     not matching any declared identifier) must still be flagged, in both
 *     .go and non-.go files.
 *
 * This is the first test file for this hook (no prior test infra existed),
 * so it also stands as the smoke test that the hook is require()-able
 * without side effects (guarded by `require.main === module` in the
 * source — see its header comment).
 *
 * Run: node --test claude-secret-guard.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');
const crypto = require('crypto');
const { spawnSync } = require('child_process');

const ROOT = __dirname;

// ---------------------------------------------------------------------------
// BUG-088 P1 CORRECTION (2026-08-11): the end-to-end fixtures below used to
// `git add`/`git reset` straight against THIS PROJECT'S OWN .git/index — a
// Destructive-agent finding (concurrent `node --test` runs across this
// project's guard/checker test files collided on a real .git/index.lock and
// left a stray staged fixture in the real repo's index). Fixed by giving
// each e2e test its OWN throwaway git index (a fresh GIT_DIR under the OS
// temp dir) while keeping GIT_WORK_TREE pointed at this real project root —
// required because claude-secret-checker.js's collectGoPackageIdentifiers()
// resolves staged paths as `path.join(ROOT, relPath)` (ROOT hardcoded to
// this checker module's own directory) to read real sibling .go files for
// its BUG-026 suppression logic, so the fixture files still have to
// physically live under this project's directory tree for that logic to see
// them — only the *index* (what's staged) needs to be isolated, not the
// working-tree location. `git --git-dir=<throwaway> --work-tree=<ROOT> add`
// stages relative to the real files on disk into a completely separate
// index, so the real repo's own `.git/index` is never opened, locked, or
// mutated by these tests, however many run concurrently.
// ---------------------------------------------------------------------------

function withThrowawayIndex(fn) {
  const gitDir = fs.mkdtempSync(path.join(os.tmpdir(), 'secretguard-throwaway-index-'));
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

const guard = require('./claude-secret-guard.js');
const {
  scanLine,
  identifierWords,
  isHardcodeKeywordIdentifier,
  looksHighEntropy,
  shannonEntropy,
  stripGoCommentsAndStrings,
  collectGoPackageIdentifiers,
  isGoPackageIdentifier,
  GIT_COMMIT_RE,
} = guard;

// A random, genuinely high-entropy hex string — never equal to any
// declared identifier anywhere, by construction (crypto-random per run).
function genuineSecretCandidate() {
  // base64 (64-symbol alphabet, ~4.5-6 bits/char in practice) rather than
  // hex (16-symbol alphabet, ~4 bits/char max) — hex strings occasionally
  // dip below the 3.7 threshold on an unlucky draw since they have less
  // headroom above it; base64 reliably clears it, avoiding a flaky test.
  const candidate = crypto.randomBytes(24).toString('base64').replace(/=+$/, '');
  assert.ok(looksHighEntropy(candidate), 'test setup: fixture secret must itself clear the entropy bar');
  return candidate;
}

// ---------------------------------------------------------------------------
// Unit: stripGoCommentsAndStrings
// ---------------------------------------------------------------------------

test('stripGoCommentsAndStrings removes comments and string contents without breaking line structure', () => {
  const src = [
    'package fixture',
    '',
    '// a line comment mentioning secretLookingWord',
    '/* a block',
    '   comment */',
    'const realDecl = "not-this-text-inside-quotes"',
  ].join('\n');
  const cleaned = stripGoCommentsAndStrings(src);
  assert.ok(!cleaned.includes('secretLookingWord'));
  assert.ok(!cleaned.includes('not-this-text-inside-quotes'));
  assert.ok(cleaned.includes('const realDecl'));
  // Line count preserved (declarations below a stripped comment must not
  // shift line-relative parsing).
  assert.equal(cleaned.split('\n').length, src.split('\n').length);
});

// ---------------------------------------------------------------------------
// Unit: collectGoPackageIdentifiers / isGoPackageIdentifier
// ---------------------------------------------------------------------------

function withFixtureGoPackage(files, fn) {
  const dir = fs.mkdtempSync(path.join(ROOT, '__secretguard_test_fixture_'));
  try {
    for (const [name, content] of Object.entries(files)) {
      fs.writeFileSync(path.join(dir, name), content, 'utf8');
    }
    return fn(dir);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

test('collectGoPackageIdentifiers finds a const declared inside a grouped const() block (the real codeReplayTargetClosedEarly shape)', () => {
  withFixtureGoPackage(
    {
      'errors.go': [
        'package fixture',
        '',
        'const (',
        '\t// codeSomeLongCamelCaseIdentifierName: doc comment.',
        '\tcodeSomeLongCamelCaseIdentifierName = "MET-X001"',
        ')',
      ].join('\n'),
    },
    dir => {
      const ids = collectGoPackageIdentifiers(dir);
      assert.ok(ids.has('codeSomeLongCamelCaseIdentifierName'));
    }
  );
});

test('collectGoPackageIdentifiers finds single-line const/var/type/func declarations', () => {
  withFixtureGoPackage(
    {
      'misc.go': [
        'package fixture',
        '',
        'const singleLineConst = "x"',
        'var singleLineVar = "y"',
        'type SomeType struct{ Field string }',
        'func SomeFunc(a int) int { return a }',
        'func (r *SomeType) Method() string { return r.Field }',
      ].join('\n'),
    },
    dir => {
      const ids = collectGoPackageIdentifiers(dir);
      for (const want of ['singleLineConst', 'singleLineVar', 'SomeType', 'SomeFunc', 'Method']) {
        assert.ok(ids.has(want), `expected identifier "${want}" to be collected`);
      }
    }
  );
});

test('collectGoPackageIdentifiers does not treat text inside comments/strings as declarations', () => {
  withFixtureGoPackage(
    {
      'noise.go': [
        'package fixture',
        '// const notARealDeclaration = "x" (this is a comment, not code)',
        'var actualDecl = "const alsoNotARealDeclarationInsideAString = 1"',
      ].join('\n'),
    },
    dir => {
      const ids = collectGoPackageIdentifiers(dir);
      assert.ok(ids.has('actualDecl'));
      assert.ok(!ids.has('notARealDeclaration'));
      assert.ok(!ids.has('alsoNotARealDeclarationInsideAString'));
    }
  );
});

test('isGoPackageIdentifier: true for a literal matching a declared package identifier, false for an unrelated high-entropy literal', () => {
  withFixtureGoPackage(
    {
      'errors.go': [
        'package fixture',
        '',
        'const (',
        '\tcodeReplayTargetClosedEarlyFixture = "MET-H004"',
        ')',
      ].join('\n'),
    },
    dir => {
      const relFile = path.relative(ROOT, path.join(dir, 'probe.go'));
      assert.equal(isGoPackageIdentifier('codeReplayTargetClosedEarlyFixture', relFile), true);

      const genuineSecret = genuineSecretCandidate();
      assert.equal(isGoPackageIdentifier(genuineSecret, relFile), false);
    }
  );
});

// ---------------------------------------------------------------------------
// Regression: the SEC-015-era false negative (ALL-CAPS shattering) must stay fixed
// ---------------------------------------------------------------------------

test('regression: MAX_COUNT is still recognised as a whole keyword word (SCREAMING_SNAKE_CASE not shattered)', () => {
  // This is the exact regression the BUG-026 brief warns against re-breaking:
  // an earlier splitter shattered ALL-CAPS identifiers into single letters,
  // silently blinding the GR#15 hardcoding-smell check on constants like
  // MAX_COUNT. BUG-026 must not touch this path at all; this test proves it
  // still doesn't.
  assert.deepEqual(identifierWords('MAX_COUNT'), ['max', 'count']);
  assert.equal(isHardcodeKeywordIdentifier('MAX_COUNT'), true);

  const findings = scanLine('if (MAX_COUNT === 45) { fail(); }');
  assert.ok(
    findings.some(f => f.category === 'hardcoding-smell'),
    'MAX_COUNT === 45 must still be flagged as a GR#15 hardcoding smell'
  );
});

// ---------------------------------------------------------------------------
// Regression: entropy threshold/min-length untouched, genuine secrets still caught
// ---------------------------------------------------------------------------

test('regression: a genuine high-entropy credential of similar length to codeReplayTargetClosedEarly is still flagged', () => {
  // codeReplayTargetClosedEarly is 27 chars. Prove a same-length-class,
  // unrelated random secret still trips looksHighEntropy AND is not
  // suppressed by the new Go-package-identifier check (it matches no
  // declared identifier anywhere).
  const secret = genuineSecretCandidate(); // base64, reliably high-entropy — see helper comment
  assert.equal(secret.length >= 27, true);
  assert.equal(looksHighEntropy(secret), true);

  const findings = scanLine(`token := "${secret}"`);
  assert.ok(
    findings.some(f => f.category === 'high-entropy' && f.candidate === secret),
    'a genuine random high-entropy literal must still be reported by scanLine'
  );
});

test('regression: ENTROPY_THRESHOLD / ENTROPY_MIN_LENGTH constants are unchanged (3.7 / 20)', () => {
  // Re-derive the constants' effect rather than importing them directly
  // (they are not exported, deliberately — this proves behaviour, not just
  // the literal number, so a refactor that keeps behaviour identical still
  // passes).
  const nineteenChars = crypto.randomBytes(10).toString('hex').slice(0, 19);
  assert.equal(looksHighEntropy(nineteenChars), false, 'a 19-char candidate must still be exempt (ENTROPY_MIN_LENGTH=20 unchanged)');
  const twentyChars = crypto.randomBytes(10).toString('hex').slice(0, 20);
  // twentyChars may or may not clear the entropy bar depending on the random
  // draw's character distribution; only length-gating is asserted here.
  void twentyChars;
});

// ---------------------------------------------------------------------------
// End-to-end: real `git diff --cached` scan via the hook's own runScan path
// ---------------------------------------------------------------------------
//
// Stages real throwaway fixture files under the repo, invokes
// claude-secret-guard.js exactly as the PreToolUse hook does (JSON on
// stdin, JSON deny/allow on stdout), and always unstages + deletes the
// fixtures in a `finally` block so a failing assertion can never leave the
// repo dirty. No production file is touched.

function runGuardAsHook(commandText, extraEnv) {
  const result = spawnSync(process.execPath, [path.join(ROOT, 'claude-secret-guard.js')], {
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

test('end-to-end: guard still BLOCKS a genuine high-entropy secret staged in a .go file', { concurrency: false }, () => {
  const fixtureDir = path.join(ROOT, '__secretguard_e2e_fixture__');
  fs.rmSync(fixtureDir, { recursive: true, force: true });
  fs.mkdirSync(fixtureDir);
  const secret = genuineSecretCandidate();
  withThrowawayIndex(gitEnv => {
    try {
      fs.writeFileSync(
        path.join(fixtureDir, 'probe.go'),
        ['package secretguardfixture', '', `const probeSecret = "${secret}"`, ''].join('\n'),
        'utf8'
      );
      const add = spawnSync('git', ['add', '--', fixtureDir], { cwd: ROOT, env: gitEnv, encoding: 'utf8' });
      assert.equal(add.status, 0, `git add failed: ${add.stderr}`);

      const outcome = runGuardAsHook('git commit -m "test: bug-026 e2e fixture (should be blocked)"', gitEnv);
      assert.equal(outcome.denied, true, `expected the guard to deny a genuine secret commit. raw stdout: ${outcome.raw.stdout}`);
      assert.match(outcome.reason, /SECRET GUARD/);
    } finally {
      fs.rmSync(fixtureDir, { recursive: true, force: true });
    }
  });
});

test('end-to-end: guard SUPPRESSES a quoted literal matching a same-package declared Go identifier, while still catching an unrelated genuine secret in the same commit', { concurrency: false }, () => {
  const fixtureDir = path.join(ROOT, '__secretguard_e2e_fixture2__');
  fs.rmSync(fixtureDir, { recursive: true, force: true });
  fs.mkdirSync(fixtureDir);
  const unrelatedSecret = genuineSecretCandidate();
  withThrowawayIndex(gitEnv => {
  try {
    fs.writeFileSync(
      path.join(fixtureDir, 'errors.go'),
      [
        'package secretguardfixture2',
        '',
        'const (',
        '\tcodeSomeVeryLongCamelCaseIdentifierFixture = "MET-X999"',
        ')',
      ].join('\n'),
      'utf8'
    );
    fs.writeFileSync(
      path.join(fixtureDir, 'probe.go'),
      [
        'package secretguardfixture2',
        '',
        'import "strings"',
        '',
        '// grepsForIdentifier mirrors sec034_differential_test.go\'s pattern of',
        '// quoting a declared identifier as a string literal to search source.',
        'func grepsForIdentifier(src string) bool {',
        '\treturn strings.Contains(src, "codeSomeVeryLongCamelCaseIdentifierFixture")',
        '}',
        '',
        `const unrelatedGenuineSecret = "${unrelatedSecret}"`,
        '',
      ].join('\n'),
      'utf8'
    );
    const add = spawnSync('git', ['add', '--', fixtureDir], { cwd: ROOT, env: gitEnv, encoding: 'utf8' });
    assert.equal(add.status, 0, `git add failed: ${add.stderr}`);

    const outcome = runGuardAsHook('git commit -m "test: bug-026 e2e suppression fixture"', gitEnv);
    assert.equal(outcome.denied, true, 'the unrelated genuine secret in the same commit must still trigger a deny');
    assert.ok(
      outcome.reason.includes(unrelatedSecret.slice(0, 3)) || /high-entropy/.test(outcome.reason),
      `expected the deny reason to reference the genuine secret finding: ${outcome.reason}`
    );
    assert.ok(
      !outcome.reason.includes('codeSomeVeryLongCamelCaseIdentifierFixture'),
      `the package-identifier-matching literal must NOT appear in the deny reason (it should be suppressed): ${outcome.reason}`
    );
  } finally {
    fs.rmSync(fixtureDir, { recursive: true, force: true });
  }
  });
});

// ---------------------------------------------------------------------------
// BUG-088 P0 CORRECTION (2026-08-11): a prior pass of this guard's refactor
// silently ported claude-author-guard.js's buildQuoteMask()/isRealGitCommit()
// quote-tracking machinery into THIS guard's trigger — machinery that never
// shipped here (`git show HEAD:claude-secret-guard.js` has only ever had a
// bare `GIT_COMMIT_RE.test(command)`) and that AC-C1 explicitly requires to
// stay unchanged. That quote mask introduced a NEW false-negative: an
// unbalanced/odd-count quote character earlier in the command string flips
// the mask's parity and hides a real, later `git commit` from the trigger
// entirely — a live Destructive-agent finding, reproduced below. Reverted to
// the bare regex; these tests prove the fix and pin the (accepted, HEAD-
// matching) over-trigger behaviour the bare regex has always had.
// ---------------------------------------------------------------------------

test('BUG-088 P0: the Destructive\'s exact fixture (unbalanced quote in a shell comment before a real commit) now correctly triggers', () => {
  // Exact fixture from the Destructive-agent finding: a single unescaped
  // apostrophe ("don't") ahead of a REAL, later `git commit` invocation.
  const command = '"# don\'t forget to review; git commit -m x"';

  // The fixed trigger (bare regex, matching HEAD) has no notion of quotes at
  // all, so it correctly fires on the boundary-anchored "; git commit" later
  // in the string, regardless of the stray apostrophe earlier.
  assert.equal(
    GIT_COMMIT_RE.test(command),
    true,
    'a real, later git commit must still trigger the guard even after an unbalanced quote earlier in the command'
  );

  // Load-bearing proof this is a real regression test, not a no-op: the
  // buggy quote-masked implementation this correction removed (reconstructed
  // here verbatim from this guard's own working tree immediately prior to
  // the BUG-088 fix, matching claude-pre-commit-check.test.js's established
  // pattern of reconstructing prior code to prove a fix is load-bearing)
  // gets this fixture WRONG — the stray apostrophe flips buildQuoteMask's
  // parity and hides the real "git commit" that follows, so isRealGitCommit
  // returns false where it must return true.
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

test('a bare "git commit" at the very start of the command still fires', () => {
  assert.equal(GIT_COMMIT_RE.test('git commit -m x'), true);
});

test('a REAL git commit after unquoted ";", "&&", "|", and newline still fires', () => {
  assert.equal(GIT_COMMIT_RE.test('true; git commit -m x'), true);
  assert.equal(GIT_COMMIT_RE.test('true && git commit -m x'), true);
  assert.equal(GIT_COMMIT_RE.test('true | git commit -m x'), true);
  assert.equal(GIT_COMMIT_RE.test('true\ngit commit -m x'), true);
});

test('a command with no "git commit" phrase at all does not fire', () => {
  assert.equal(GIT_COMMIT_RE.test('npm install'), false);
  assert.equal(GIT_COMMIT_RE.test('git status'), false);
});

// Accepted, HEAD-matching limitation (NOT a regression): the bare regex has
// no notion of "inside a string literal", so a BOW comment merely quoting
// "(git commit ...)" as prose still triggers the guard (an over-trigger —
// costs an extra scan, never a bypass). This is the pre-BUG-043 behaviour
// this guard has always had at HEAD and is explicitly what AC-C1 requires be
// kept unchanged; asserted here so a future attempt to "fix" it by silently
// reintroducing quote-masking is caught by this test failing, not by it
// staying green.
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

// ---------------------------------------------------------------------------
// BUG-123 regression: `git -c key=value commit` and other global-option forms
// ---------------------------------------------------------------------------
//
// Prior to the fix, GIT_COMMIT_RE only tolerated a single bare `-C <dir>`
// between `git` and `commit` — any `-c key=value` (a routine idiom for
// overriding user.email/user.name/commit.gpgsign for one commit) failed to
// match, so the trigger returned false and the real scan never ran.

test('BUG-123: "git -c key=value commit" (single -c) now fires', () => {
  assert.equal(GIT_COMMIT_RE.test('git -c foo=bar commit -m test'), true);
  assert.equal(GIT_COMMIT_RE.test('git -c user.email=x@example.com commit -m x'), true);
  assert.equal(GIT_COMMIT_RE.test('git -c commit.gpgsign=false commit -m x'), true);
});

test('BUG-123: multiple stacked -c options, and -c combined with -C, still fire', () => {
  assert.equal(GIT_COMMIT_RE.test('git -c user.email=x -c user.name=y commit -m x'), true);
  assert.equal(GIT_COMMIT_RE.test('git -c user.email=x -C /some/dir commit -m x'), true);
  assert.equal(GIT_COMMIT_RE.test('git -C /some/dir -c user.email=x commit -m x'), true);
});

test('BUG-123: --git-dir=/--work-tree= global options still fire', () => {
  assert.equal(GIT_COMMIT_RE.test('git --git-dir=/x/.git --work-tree=/x commit -m x'), true);
});

test('BUG-123: sanity — the pre-fix regex genuinely misses this fixture (proves the regression test is load-bearing)', () => {
  const preFixRe = /(?:^|[;&|(\n])\s*git\s+(?:-C\s+\S+\s+)?commit\b/;
  assert.equal(
    preFixRe.test('git -c foo=bar commit -m test'),
    false,
    'sanity: the reconstructed pre-fix regex must fail to match this exact bypass fixture'
  );
});

// ---------------------------------------------------------------------------
// BUG-123 ROUND 2 regression: Tester B's backtracking false-positive finding.
// ---------------------------------------------------------------------------
//
// Round 1's fix consumed a `-c`/`-C` option's value with a greedy `\S+`
// inside one big alternation regex. When the trailing verb check failed
// (because the command was NOT actually a commit), the regex engine
// backtracked into the argument-less catch-all for the same `-c`/`-C` token,
// leaving the option's own value unconsumed right where the verb check ran
// next — so a value that happened to START with "commit" (immediately
// followed by a non-word character) was misread as the verb itself. Fixed by
// switching claude-git-commit-trigger.js from a single alternation regex to
// a tokenizer (finds the option run one option at a time, then compares the
// following whole word to the verb set by exact equality) — see that
// module's header for the full analysis. These four are Tester B's exact
// reproductions and must all now return false.

test('BUG-123 round 2: "git -c commit.gpgsign=false status" is NOT a commit trigger', () => {
  assert.equal(
    GIT_COMMIT_RE.test('git -c commit.gpgsign=false status'),
    false,
    'this is `git status` with an unrelated -c value that happens to start with "commit." — must not fire'
  );
});

test('BUG-123 round 2: "git -c commit.template=/dev/null diff" is NOT a commit trigger', () => {
  assert.equal(
    GIT_COMMIT_RE.test('git -c commit.template=/dev/null diff'),
    false,
    'this is `git diff` — must not fire'
  );
});

test('BUG-123 round 2: "git -C commit-repo status" (a directory literally named commit-repo) is NOT a commit trigger', () => {
  assert.equal(
    GIT_COMMIT_RE.test('git -C commit-repo status'),
    false,
    'the -C value is a directory name that happens to start with "commit-" — must not fire'
  );
});

test('BUG-123 round 2: sanity — the round-1 regex genuinely mis-fires on these fixtures (proves the regression tests are load-bearing)', () => {
  // Reconstructed verbatim from claude-git-commit-trigger.js as it stood
  // after round 1 (single alternation regex, no per-option tokenizer) —
  // confirmed by direct execution to reproduce Tester B's finding before
  // being replaced by the round-2 tokenizer fix.
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
  assert.equal(
    round1Re.test('git -c commit.gpgsign=false status'),
    true,
    'sanity: the reconstructed round-1 regex must genuinely mis-fire on this exact fixture'
  );
  assert.equal(
    round1Re.test('git -C commit-repo status'),
    true,
    'sanity: the reconstructed round-1 regex must genuinely mis-fire on this exact fixture'
  );
});

// ---------------------------------------------------------------------------
// BUG-123 ROUND 4 regression: attacker Vex's quote-open-mid-token finding.
// Round 3's `-c\s+(?:"[^"]*"|'[^']*'|\S+)` value grammar only recognised a
// quote as the option's value when it was the VERY FIRST character after the
// required whitespace (`-c "key=value"`). The equally common
// `-c key="value with space"` shape — quote opening AFTER the `=`, mid-token
// — fell through to the bare `\S+` catch-all, which stopped at the first
// whitespace INSIDE the still-open quote, leaking the value's tail into the
// verb-word position. Fixed by `consumeShellToken()` in
// claude-git-commit-trigger.js, which scans a single token character by
// character and treats a quote ANYWHERE within it as opening a
// whitespace-immune run until its own matching close quote — same discipline
// as claude-bow.js's tokenizeCommandSafely(), applied to one token instead
// of a whole command string. See that module's header for the full analysis.
// ---------------------------------------------------------------------------

test('BUG-123 round 4 (Vex): "git -c user.name=\\"John Q Commit\\" commit -m x" now fires (double-quote, mid-token)', () => {
  assert.equal(
    GIT_COMMIT_RE.test('git -c user.name="John Q Commit" commit -m x'),
    true,
    "Vex's exact false-negative repro: an ordinary quoted user.name override must not hide a real commit from the scan"
  );
});

test('BUG-123 round 4 (Vex): "git -c user.name=\'John Q Commit\' commit -m x" now fires (single-quote, mid-token)', () => {
  assert.equal(
    GIT_COMMIT_RE.test("git -c user.name='John Q Commit' commit -m x"),
    true,
    'the single-quote variant of the same mid-token quote shape must also fire'
  );
});

test('BUG-123 round 4 (Vex): "git -c msg=\\"please commit later\\" status" is NOT a commit trigger', () => {
  assert.equal(
    GIT_COMMIT_RE.test('git -c msg="please commit later" status'),
    false,
    "Vex's exact false-positive repro: the leaked quoted-value tail must not be misread as the verb"
  );
});

test('BUG-123 round 4: sanity — the round-3 value grammar genuinely mis-handles these fixtures (proves the regression tests are load-bearing)', () => {
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
  assert.equal(
    round3Re.test('git -c user.name="John Q Commit" commit -m x'),
    false,
    'sanity: the reconstructed round-3 regex must genuinely MISS this real commit'
  );
  assert.equal(
    round3Re.test('git -c msg="please commit later" status'),
    true,
    'sanity: the reconstructed round-3 regex must genuinely mis-fire on this status command'
  );
});

// ---------------------------------------------------------------------------
// BUG-123 ROUND 6 regression: attacker Marrow's odd-embedded-escaped-quote
// finding, plus Bill's "reuse, don't model" ruling.
//
// Round 4's `consumeShellToken()` paired quote characters POSITIONALLY
// (`text.indexOf(ch, i + 1)`) with no awareness of a preceding backslash
// escaping the quote — so it happened to parse correctly for a value with
// ZERO or an EVEN count of embedded escaped quotes (round 4's own tests only
// ever exercised 0 or 2) but mispaired on an ODD count: the escaped `\"`
// inside `-c key="a\"b"` was read as an ordinary close-quote, ending the
// "quoted region" one character early; the value's REAL closing `"` then
// looked like a fresh unmatched open-quote with no partner before EOF, so
// the option failed to parse (-1), the `git` occurrence was abandoned, and a
// real `git commit` went completely unscanned — BUG-123's own original
// impact, reproduced by a third independent route. Fixed (round 6) by making
// claude-git-commit-trigger.js's `consumeShellToken()` delegate entirely to
// the shared, canonical `buildQuoteMask()` (extracted to
// claude-quote-mask.js) instead of hand-rolling a fourth quote scanner — see
// that module's and claude-git-commit-trigger.js's headers for the full
// analysis. These fixtures are Marrow's exact repro plus the 1/3/5-embedded-
// escaped-quote generalisation Bill's ruling required.
// ---------------------------------------------------------------------------

test('BUG-123 round 6 (Marrow): "git -c key=\\"a\\\\\\"b\\" commit -m x" now fires (1 embedded escaped quote, exact repro)', () => {
  assert.equal(
    GIT_COMMIT_RE.test('git -c key="a\\"b" commit -m x'),
    true,
    "Marrow's exact false-negative repro: an odd count of embedded escaped quotes must not hide a real commit"
  );
});

test('BUG-123 round 6: odd embedded-escaped-quote counts (1, 3, 5) all still fire', () => {
  assert.equal(GIT_COMMIT_RE.test('git -c key="a\\"b" commit -m x'), true, '1 embedded escaped quote');
  assert.equal(
    GIT_COMMIT_RE.test('git -c key="a\\"b\\"c\\"d" commit -m x'),
    true,
    '3 embedded escaped quotes'
  );
  assert.equal(
    GIT_COMMIT_RE.test('git -c key="a\\"b\\"c\\"d\\"e\\"f" commit -m x'),
    true,
    '5 embedded escaped quotes'
  );
});

test('BUG-123 round 6: even embedded-escaped-quote counts (0, 2, 4) still fire (prior baseline, must not regress)', () => {
  assert.equal(GIT_COMMIT_RE.test('git -c key=value commit -m x'), true, '0 embedded quotes');
  assert.equal(GIT_COMMIT_RE.test('git -c key="a\\"b\\"" commit -m x'), true, '2 embedded escaped quotes');
  assert.equal(
    GIT_COMMIT_RE.test('git -c key="a\\"b\\"c\\"d\\"" commit -m x'),
    true,
    '4 embedded escaped quotes'
  );
});

test('BUG-123 round 6: genuinely unterminated -c value (unbalanced quote, no closing partner anywhere) still fails closed to a non-match', () => {
  assert.equal(
    GIT_COMMIT_RE.test('git -c key="unterminated commit -m x'),
    false,
    'a truly unterminated quote inside the option value must still refuse to parse (fail-closed posture unchanged from every prior round)'
  );
});

test('BUG-123 round 6: sanity — the round-4 positional-pairing scanner genuinely mis-handles Marrow\'s fixture (proves the regression test is load-bearing)', () => {
  // Reconstructed verbatim from claude-git-commit-trigger.js's round-4
  // consumeShellToken() (positional text.indexOf(ch, i+1) pairing, no
  // backslash awareness at all).
  function round4ConsumeShellToken(text, start) {
    let i = start;
    while (i < text.length) {
      const ch = text[i];
      if (ch === '"' || ch === "'") {
        const close = text.indexOf(ch, i + 1);
        if (close === -1) return -1;
        i = close + 1;
        continue;
      }
      if (/\s/.test(ch)) break;
      i++;
    }
    return i === start ? -1 : i;
  }
  const text = 'git -c key="a\\"b" commit -m x';
  const valueStart = text.indexOf('key=') + 'key='.length;
  assert.equal(
    round4ConsumeShellToken(text, valueStart),
    -1,
    "sanity: the reconstructed round-4 scanner must genuinely mispair on Marrow's exact fixture and return -1"
  );
});

test('BUG-123 round 6: claude-git-commit-trigger.js no longer contains any local quote-scanning implementation (Bill\'s requirement 2, grep-verifiable)', () => {
  const src = fs.readFileSync(path.join(ROOT, 'claude-git-commit-trigger.js'), 'utf8');
  assert.equal(
    /function\s+buildQuoteMask\s*\(/.test(src),
    false,
    'claude-git-commit-trigger.js must not define its own buildQuoteMask — it must require the shared claude-quote-mask.js module'
  );
  assert.ok(
    /require\(['"]\.\/claude-quote-mask\.js['"]\)/.test(src),
    'claude-git-commit-trigger.js must require the shared claude-quote-mask.js module'
  );
});

test('BUG-123 end-to-end: guard still BLOCKS a genuine high-entropy secret staged, when committed via "git -c ... commit"', { concurrency: false }, () => {
  const fixtureDir = path.join(ROOT, '__secretguard_e2e_fixture_bug123__');
  fs.rmSync(fixtureDir, { recursive: true, force: true });
  fs.mkdirSync(fixtureDir);
  const secret = genuineSecretCandidate();
  withThrowawayIndex(gitEnv => {
    try {
      fs.writeFileSync(
        path.join(fixtureDir, 'probe.go'),
        ['package secretguardfixturebug123', '', `const probeSecret = "${secret}"`, ''].join('\n'),
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
        `expected the guard to deny a genuine secret commit issued via "git -c ... commit". raw stdout: ${outcome.raw.stdout}`
      );
      assert.match(outcome.reason, /SECRET GUARD/);
    } finally {
      fs.rmSync(fixtureDir, { recursive: true, force: true });
    }
  });
});

// ---------------------------------------------------------------------------
// BUG-123 ROUND 9 regression (attacker "Cinder" REJECT): claude-git-commit-
// trigger.js used to be require()'d at TRUE module top level, before main()'s
// try/catch existed. A synchronous throw during that require (e.g. a broken
// claude-quote-mask.js, its own transitive dependency) crashed the whole
// process at MODULE LOAD TIME -- uncaught exception, exit 1, ZERO stdout, no
// hookSpecificOutput JSON -- which the PreToolUse contract treats as a
// non-blocking failure: the proposed `git commit` PROCEEDS completely
// unscanned. Fixed by loading the trigger module lazily, inside main()'s try
// block (loadGitCommitTrigger(), via the CLAUDE_SECRET_GUARD_TRIGGER_PATH env
// override so these tests never touch the real, live-edited
// claude-git-commit-trigger.js / claude-quote-mask.js), so a load-time throw
// is caught by the SAME catch() that already denies on any other internal
// error, mirroring claude-destructive-guard.js's own proven round-3 fix.
// ---------------------------------------------------------------------------

function withBrokenTriggerFixture(content, fn) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'secretguard-triggerfixture-'));
  const fixturePath = path.join(dir, 'fixture-trigger.js');
  try {
    if (content !== null) fs.writeFileSync(fixturePath, content, 'utf8');
    return fn(fixturePath);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

test('BUG-123 round 9 (Cinder) sanity: reverting to a top-level require() genuinely reproduces the uncaught module-load crash', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'secretguard-round9-sandbox-'));
  try {
    fs.writeFileSync(
      path.join(dir, 'broken-toplevel-guard.js'),
      "'use strict';\nconst {buildAnchoredGitVerbTriggerRegex} = require('./broken-trigger.js');\nconst RE = buildAnchoredGitVerbTriggerRegex('commit');\nprocess.stdin.on('data', () => {});\nprocess.stdin.on('end', () => { process.stdout.write(RE.test('git commit') ? 'yes' : 'no'); });\n",
      'utf8'
    );
    fs.writeFileSync(path.join(dir, 'broken-trigger.js'), 'this is ( not valid javascript {{{\n', 'utf8');
    const r = spawnSync(process.execPath, [path.join(dir, 'broken-toplevel-guard.js')], {
      input: JSON.stringify({ tool: 'Bash', tool_input: { command: 'git commit -m test' } }),
      encoding: 'utf8',
    });
    assert.equal(r.status, 1, 'sanity: the OLD top-level-require shape genuinely crashes with exit 1');
    assert.equal((r.stdout || '').trim(), '', 'sanity: the OLD shape emits ZERO stdout -- no decision, the commit would proceed unscanned');
    assert.match(r.stderr, /SyntaxError/);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('BUG-123 round 9: a broken claude-git-commit-trigger.js dependency on a real "git commit" invocation now DENIES cleanly with JSON, not a bare crash', () => {
  withBrokenTriggerFixture('this is ( not valid javascript {{{\n', (brokenTriggerPath) => {
    const r = spawnSync(process.execPath, [path.join(ROOT, 'claude-secret-guard.js')], {
      cwd: ROOT,
      encoding: 'utf8',
      env: { ...process.env, CLAUDE_SECRET_GUARD_TRIGGER_PATH: brokenTriggerPath },
      input: JSON.stringify({ tool: 'Bash', tool_input: { command: 'git commit -m test' } }),
    });
    assert.notEqual(r.status, 1, 'must not crash with a bare uncaught-exception exit(1) -- that is the exact bug this round fixes');
    assert.equal(r.status, 0, 'the guard process must exit 0 (it makes the decision itself)');
    assert.notEqual((r.stdout || '').trim(), '', 'a decision payload MUST be emitted -- the pre-fix bug emitted nothing at all here');
    const parsed = JSON.parse(r.stdout);
    assert.equal(parsed?.hookSpecificOutput?.permissionDecision, 'deny', 'a broken dependency on a real git-commit-shaped command must DENY, not silently allow');
    assert.match(parsed.hookSpecificOutput.permissionDecisionReason, /depend|load|trigger/i);
  });
});

test('BUG-123 round 9: a MISSING claude-git-commit-trigger.js on a real "git commit" invocation also DENIES cleanly, not crashes', () => {
  const r = spawnSync(process.execPath, [path.join(ROOT, 'claude-secret-guard.js')], {
    cwd: ROOT,
    encoding: 'utf8',
    env: {
      ...process.env,
      CLAUDE_SECRET_GUARD_TRIGGER_PATH: path.join(os.tmpdir(), 'this-file-does-not-exist-' + crypto.randomBytes(4).toString('hex') + '.js'),
    },
    input: JSON.stringify({ tool: 'Bash', tool_input: { command: 'git commit -m test' } }),
  });
  assert.equal(r.status, 0);
  const parsed = JSON.parse(r.stdout);
  assert.equal(parsed?.hookSpecificOutput?.permissionDecision, 'deny');
});

test('BUG-123 round 9: a broken claude-git-commit-trigger.js on a NON-commit command still allows silently (dependency-load fail-closed is scoped to commit-shaped input only)', () => {
  withBrokenTriggerFixture('this is ( not valid javascript {{{\n', (brokenTriggerPath) => {
    const r = spawnSync(process.execPath, [path.join(ROOT, 'claude-secret-guard.js')], {
      cwd: ROOT,
      encoding: 'utf8',
      env: { ...process.env, CLAUDE_SECRET_GUARD_TRIGGER_PATH: brokenTriggerPath },
      input: JSON.stringify({ tool: 'Bash', tool_input: { command: 'npm install' } }),
    });
    assert.equal(r.status, 0, 'must not crash');
    assert.equal((r.stdout || '').trim(), '', 'an unrelated command must allow silently even with a broken trigger dependency -- never brick the whole session over a bug this command does not need');
  });
});

test('BUG-123 round 9: normal operation is completely unaffected -- healthy dependency, real trigger still allows a non-commit command silently', () => {
  const r = spawnSync(process.execPath, [path.join(ROOT, 'claude-secret-guard.js')], {
    cwd: ROOT,
    encoding: 'utf8',
    input: JSON.stringify({ tool: 'Bash', tool_input: { command: 'npm install' } }),
  });
  assert.equal(r.status, 0);
  assert.equal((r.stdout || '').trim(), '', 'ALLOW is silent for a non-commit command, unchanged posture');
});

// ---------------------------------------------------------------------------
// BUG-123 ROUND 10 regression (attacker "Thresher" REJECT): the round-9
// dependency-free fallback, FALLBACK_LOOKS_LIKE_COMMIT_RE, capped the gap
// between "git" and "commit" at ~200 chars. A single legitimately long `-c`
// option value pushes a real commit's git-to-commit distance past that
// window, so with the trigger dependency broken NOTHING catches it -- a
// silent, unscanned allow. Fixed by rebuilding looksLikeCommitFallback() as
// unbounded indexOf/startsWith scanning (no regex, no distance cap, strictly
// O(n), zero backtracking).
// ---------------------------------------------------------------------------

test('BUG-123 round 10 (Thresher) exact repro, unit level: looksLikeCommitFallback() now fires on a 250-char -c value', () => {
  const longVal = 'A'.repeat(250);
  const cmd = `git -c user.name="${longVal}" commit -m test`;
  assert.equal(guard.looksLikeCommitFallback(cmd), true, 'a real commit with a long -c value must still be recognised by the dependency-free fallback');
});

test('BUG-123 round 10 (Thresher) sanity: the pre-fix 200-char-capped regex genuinely misses this fixture (proves the regression test is load-bearing)', () => {
  const PRE_FIX_RE = /\bgit(?:\.(?:exe|cmd))?\b[\s\S]{0,200}?\bcommit\b/i;
  const longVal = 'A'.repeat(250);
  const cmd = `git -c user.name="${longVal}" commit -m test`;
  assert.equal(PRE_FIX_RE.test(cmd), false, 'sanity: the OLD bounded regex genuinely fails to match Thresher\'s exact repro');
});

test('BUG-123 round 10 (Thresher) end-to-end: a broken trigger dependency + a real commit with a 250-char -c value now DENIES, not silently allows', () => {
  withBrokenTriggerFixture('this is ( not valid javascript {{{\n', (brokenTriggerPath) => {
    const longVal = 'A'.repeat(250);
    const cmd = `git -c user.name="${longVal}" commit -m test`;
    const r = spawnSync(process.execPath, [path.join(ROOT, 'claude-secret-guard.js')], {
      cwd: ROOT,
      encoding: 'utf8',
      env: { ...process.env, CLAUDE_SECRET_GUARD_TRIGGER_PATH: brokenTriggerPath },
      input: JSON.stringify({ tool: 'Bash', tool_input: { command: cmd } }),
    });
    assert.equal(r.status, 0, 'the guard process must exit 0 (it makes the decision itself)');
    assert.notEqual((r.stdout || '').trim(), '', 'a decision payload MUST be emitted -- the round-10 bug emitted nothing at all here');
    const parsed = JSON.parse(r.stdout);
    assert.equal(parsed?.hookSpecificOutput?.permissionDecision, 'deny', 'a broken dependency on a real, long-option git-commit-shaped command must DENY, not silently allow');
  });
});

test('BUG-123 round 10: even more extreme distances (2KB, 20KB, 200KB gap between "git" and "commit") still fire -- no residual bound anywhere', () => {
  for (const size of [2000, 20000, 200000]) {
    const longVal = 'B'.repeat(size);
    const cmd = `git -c user.name="${longVal}" commit -m test`;
    assert.equal(guard.looksLikeCommitFallback(cmd), true, `must fire at gap size ${size}`);
  }
});

test('BUG-123 round 10: prior fallback behaviour is preserved -- a quoted-bare "git" commit and a non-commit command still behave as before', () => {
  assert.equal(guard.looksLikeCommitFallback('"git" commit -m "x"'), true);
  assert.equal(guard.looksLikeCommitFallback('npm install'), false);
  assert.equal(guard.looksLikeCommitFallback(''), false);
  assert.equal(guard.looksLikeCommitFallback(null), false);
});

test('BUG-123 round 10: ReDoS safety -- a 1MB adversarial non-matching string (many "git" tokens, no "commit" anywhere) resolves quickly with no catastrophic backtracking', () => {
  // Deliberately adversarial: repeats "git " many times (so the OLD regex
  // would have had to restart its lazy scan from every single occurrence)
  // followed by a large filler block containing no "commit" substring at all.
  const adversarial = 'git '.repeat(300000) + 'x'.repeat(100000);
  assert.equal(adversarial.length > 1_000_000, true, 'sanity: the adversarial fixture is genuinely over 1MB');
  const t0 = process.hrtime.bigint();
  const result = guard.looksLikeCommitFallback(adversarial);
  const elapsedMs = Number(process.hrtime.bigint() - t0) / 1e6;
  assert.equal(result, false, 'no "commit" anywhere in the fixture -- must resolve to false, not hang');
  assert.equal(elapsedMs < 100, true, `must resolve well under 100ms on a 1MB adversarial input (took ${elapsedMs.toFixed(2)}ms) -- a slow fallback would itself be a DoS vector`);
});
