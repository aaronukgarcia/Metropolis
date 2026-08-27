/**
 * claude-author-guard.test.js — unit + end-to-end tests for
 * claude-author-guard.js (BUG-035, demoted to advisory by FEAT-045).
 *
 * FEAT-045 DEMOTION (2026-08-11): every fixture below that used to assert
 * `result.denied === true` (a block) now asserts `result.denied === false`
 * AND `result.status === 0` — the exact same fifteen bypass command strings
 * (BUG-044..052, plus the Round-4/BUG-077/078/ROUND4-3 findings), INVERTED
 * per AC-7's explicit requirement rather than deleted, so a future
 * regression that reintroduces a block on any of these already-catalogued
 * strings is caught by name. Detection-machinery unit tests (the internal
 * parsing/tokenizing functions) are UNCHANGED — demotion only changed what
 * happens with a positive detection, not the detection logic itself.
 *
 * All end-to-end cases run against THROWAWAY repos created under the OS
 * temp dir (never this repo — see the destructive-case rule in the BUG-035
 * brief) and always removed in a `finally`. Test identities are assembled
 * from fragments / built with template strings at runtime rather than
 * written as a single literal, and never appear in a commit message or a
 * staged file in THIS repo — the same recursion trap
 * claude-codename-guard.js's header describes: a fixture containing a fake
 * identity could trip claude-secret-guard.js's key-shape checks or become
 * exactly the kind of stray commit BUG-035 is about if it ever leaked into
 * this repo's own history. It never does — it only ever lands inside a temp
 * directory's own disposable git repo.
 *
 * Run: node --test claude-author-guard.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawnSync } = require('child_process');

const ROOT = __dirname;
const GUARD_PATH = path.join(ROOT, 'claude-author-guard.js');

const guard = require('./claude-author-guard.js');
const {
  findCommitInvocation,
  extractEnvOverrides,
  extractAuthorFlag,
  hasFlag,
} = guard;

// A "fabricated" identity built from fragments at runtime — never a single
// literal string in this source file (see header).
function fabricatedIdentity() {
  const user = ['te', 'st'].join('');
  const host = ['te', 'st', '.', 'com'].join('');
  return { name: user, email: `${user}@${host}` };
}

function withTempRepo(fn) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'author-guard-fixture-'));
  try {
    return fn(dir);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

function git(cwd, args) {
  const r = spawnSync('git', args, { cwd, encoding: 'utf8' });
  if (r.status !== 0) {
    throw new Error(`git ${args.join(' ')} failed: ${r.stderr}`);
  }
  return r.stdout.trim();
}

/** Runs `fn` with process.cwd() chdir'd into `dir` (the shared
 * claude-author-identity.js's git() calls use process.cwd()), restoring cwd
 * afterward even on throw. Mirrors claude-author-identity.test.js's withCwd. */
function withCwd(dir, fn) {
  const prev = process.cwd();
  process.chdir(dir);
  try {
    return fn();
  } finally {
    process.chdir(prev);
  }
}

/** Real, sanctioned-for-that-repo identity: whatever we configure locally. */
const SANCTIONED_NAME = 'Sanctioned Contributor';
const SANCTIONED_EMAIL = 'sanctioned@example.invalid';

function initRepoWithHistory(dir, commitCount = 3) {
  git(dir, ['init', '-b', 'main']);
  git(dir, ['config', 'user.name', SANCTIONED_NAME]);
  git(dir, ['config', 'user.email', SANCTIONED_EMAIL]);
  for (let i = 0; i < commitCount; i++) {
    fs.writeFileSync(path.join(dir, `file${i}.txt`), `content ${i}\n`, 'utf8');
    git(dir, ['add', '-A']);
    git(dir, ['commit', '-m', `commit ${i}`]);
  }
}

/** Invoke the guard exactly as the PreToolUse hook would: JSON on stdin,
 * decision conveyed via stdout. Post-demotion, `denied` is ALWAYS false
 * (AC-6/7) — kept as a named field rather than removed, specifically so a
 * future regression that starts setting permissionDecision to a blocking
 * value flips this field and fails every fixture below by name. `status`
 * is the observed subprocess exit code — AC-7 requires this to be 0 for
 * every fixture, with no exceptions. `advisory` is true when the guard
 * emitted a non-blocking allow+reason (AC-9's shape (a)); when the guard
 * instead used a silent allow (AC-8's shape, or AC-9's shape (b) if this
 * guard chose stderr-only — it does not, see claude-author-guard.js), both
 * `denied` and `advisory` are false and `reason` is null. */
function runGuard(cwd, command, env) {
  const payload = JSON.stringify({ tool: 'Bash', tool_input: { command } });
  const r = spawnSync(process.execPath, [GUARD_PATH], {
    cwd,
    input: payload,
    encoding: 'utf8',
    env: env || process.env,
  });
  let denied = false;
  let advisory = false;
  let reason = null;
  const stdout = (r.stdout || '').trim();
  if (stdout) {
    const parsed = JSON.parse(stdout);
    denied = parsed?.hookSpecificOutput?.permissionDecision === 'deny';
    advisory =
      parsed?.hookSpecificOutput?.permissionDecision === 'allow' &&
      !!parsed?.hookSpecificOutput?.permissionDecisionReason;
    reason = parsed?.hookSpecificOutput?.permissionDecisionReason || null;
  }
  return { denied, advisory, reason, stdout, stderr: r.stderr, status: r.status };
}

// ---------------------------------------------------------------------------
// Unit: findCommitInvocation (detection machinery — unchanged by demotion)
// ---------------------------------------------------------------------------

test('findCommitInvocation matches a plain git commit and ignores unrelated text', () => {
  assert.ok(findCommitInvocation('git commit -m "x"'));
  assert.ok(findCommitInvocation('cd repo && git commit -m "x"'));
  assert.equal(findCommitInvocation('git status'), null);
  assert.equal(findCommitInvocation('git rebase main'), null);
  assert.equal(findCommitInvocation('echo "please git commit later"'), null);
});

// ---------------------------------------------------------------------------
// Unit: extractEnvOverrides
// ---------------------------------------------------------------------------

test('extractEnvOverrides reads POSIX inline assignments', () => {
  const id = fabricatedIdentity();
  const prefix = `GIT_AUTHOR_NAME=${id.name} GIT_AUTHOR_EMAIL=${id.email} `;
  const out = extractEnvOverrides(prefix);
  assert.equal(out.GIT_AUTHOR_NAME, id.name);
  assert.equal(out.GIT_AUTHOR_EMAIL, id.email);
  assert.equal(out.GIT_COMMITTER_EMAIL, undefined);
});

test('extractEnvOverrides reads quoted export form', () => {
  const id = fabricatedIdentity();
  const prefix = `export GIT_COMMITTER_NAME="${id.name}"; export GIT_COMMITTER_EMAIL="${id.email}"; `;
  const out = extractEnvOverrides(prefix);
  assert.equal(out.GIT_COMMITTER_NAME, id.name);
  assert.equal(out.GIT_COMMITTER_EMAIL, id.email);
});

test('extractEnvOverrides reads PowerShell $env: form', () => {
  const id = fabricatedIdentity();
  const prefix = `$env:GIT_AUTHOR_EMAIL = '${id.email}'; `;
  const out = extractEnvOverrides(prefix);
  assert.equal(out.GIT_AUTHOR_EMAIL, id.email);
});

// ---------------------------------------------------------------------------
// Unit: extractAuthorFlag
// ---------------------------------------------------------------------------

test('extractAuthorFlag parses --author="Name <email>"', () => {
  const id = fabricatedIdentity();
  const r = extractAuthorFlag(` --author="${id.name} <${id.email}>" -m "x"`);
  assert.equal(r.email, id.email);
});

test('extractAuthorFlag parses --author with space-separated value', () => {
  const id = fabricatedIdentity();
  const r = extractAuthorFlag(` --author "${id.name} <${id.email}>" -m "x"`);
  assert.equal(r.email, id.email);
});

test('extractAuthorFlag returns null email when unparseable', () => {
  const r = extractAuthorFlag(` --author=NoEmailHere -m "x"`);
  assert.equal(r.email, null);
});

test('extractAuthorFlag returns null when the flag is absent', () => {
  assert.equal(extractAuthorFlag(' -m "x"'), null);
});

// ---------------------------------------------------------------------------
// Unit: hasFlag
// ---------------------------------------------------------------------------

test('hasFlag detects --amend and --reset-author as whole flags only', () => {
  assert.ok(hasFlag(' --amend --no-edit', '--amend'));
  assert.ok(!hasFlag(' --amend-something', '--amend'));
  assert.ok(hasFlag(' --author=x --reset-author', '--reset-author'));
});

// ---------------------------------------------------------------------------
// AC-6/AC-7: structural proof that no blocking decision exists anywhere in
// the file, and that the guard's own regression corpus (every fixture below)
// exits 0 unconditionally.
// ---------------------------------------------------------------------------

test('AC-6: claude-author-guard.js source contains ZERO occurrences of a blocking permissionDecision value (grep-equivalent, full file, not just reachable code)', () => {
  const src = fs.readFileSync(GUARD_PATH, 'utf8');
  assert.doesNotMatch(src, /permissionDecision.*['"]deny['"]/);
  assert.doesNotMatch(src, /permissionDecision.*['"]ask['"]/);
});

test('AC-6 false-pass guard: no uncaught-exception blocking path either — the outer catch fails OPEN (allow), not by throwing', () => {
  const src = fs.readFileSync(GUARD_PATH, 'utf8');
  const topLevelBlock = src.slice(src.indexOf('if (require.main === module)'));
  assert.match(topLevelBlock, /catch/);
  assert.doesNotMatch(topLevelBlock, /\bdeny\(/, 'no call to a deny-shaped function may remain in the top-level error handler');
});

// ---------------------------------------------------------------------------
// End-to-end: throwaway repos only
// ---------------------------------------------------------------------------

test('ALLOWS an ordinary commit whose identity matches configured git identity', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const result = runGuard(dir, 'git commit --allow-empty -m "ordinary commit"');
    assert.equal(result.denied, false, result.reason);
    assert.equal(result.status, 0);
  });
});

test('ADVISES (does not block, FEAT-045) on a commit whose author is overridden by --author to a fabricated identity', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `git commit --allow-empty --author="${id.name} <${id.email}>" -m "bad"`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false, 'AC-7: this fixture (former BLOCKS case) must now exit allow');
    assert.equal(result.status, 0, 'AC-7: exit code must be 0 unconditionally');
    assert.equal(result.advisory, true, 'AC-9: the guard must still say something (non-empty warning), not go silent on a positive detection');
    assert.match(result.reason, /--author/);
    assert.match(result.reason, new RegExp(id.email.replace('.', '\\.')));
  });
});

test('ADVISES (does not block, FEAT-045) on a commit whose identity comes from GIT_AUTHOR_* env vars inline in the command', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    // This is exactly BUG-035's shape: identity set via env vars, not --author.
    const cmd = `GIT_AUTHOR_NAME=${id.name} GIT_AUTHOR_EMAIL=${id.email} git commit --allow-empty -m "bad"`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false);
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true);
    assert.match(result.reason, /GIT_AUTHOR_EMAIL/);
  });
});

test('ADVISES (does not block, FEAT-045) on a commit whose committer (not author) comes from GIT_COMMITTER_* env vars', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `GIT_COMMITTER_NAME=${id.name} GIT_COMMITTER_EMAIL=${id.email} git commit --allow-empty -m "bad"`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false);
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true);
    assert.match(result.reason, /GIT_COMMITTER_EMAIL/);
  });
});

test('ALLOWS an ordinary --amend that keeps the existing sanctioned author', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    // Real amend, run for real (not just guard-checked), to prove the
    // pipeline: guard allows -> actual git commit --amend succeeds ->
    // author is unchanged, committer is refreshed to the same sanctioned
    // config identity.
    const guardResult = runGuard(dir, 'git commit --amend --no-edit');
    assert.equal(guardResult.denied, false, guardResult.reason);
    assert.equal(guardResult.status, 0);
    git(dir, ['commit', '--amend', '--no-edit']);
    const authorEmail = git(dir, ['log', '-1', '--format=%ae']);
    const committerEmail = git(dir, ['log', '-1', '--format=%ce']);
    assert.equal(authorEmail, SANCTIONED_EMAIL);
    assert.equal(committerEmail, SANCTIONED_EMAIL);
  });
});

test('ADVISES (does not block, FEAT-045) on an --amend that overrides the author to a fabricated identity', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `git commit --amend --no-edit --author="${id.name} <${id.email}>"`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false);
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true);
  });
});

test('ALLOWS non-commit git commands untouched, including git rebase', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 1);
    assert.equal(runGuard(dir, 'git status').denied, false);
    assert.equal(runGuard(dir, 'git rebase main').denied, false);
    assert.equal(runGuard(dir, 'git log --oneline').denied, false);
  });
});

test('CLAUDE_AUTHOR_GUARD_EXTRA_IDENTITIES sanctions a brand-new contributor immediately (and the fixture without it is now advisory, not blocking)', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    // First, without the extension var: FEAT-045 demoted this from a block
    // to an advisory allow (not yet known to history or config, but this
    // layer no longer refuses).
    const cmdAuthor = `git commit --allow-empty --author="${id.name} <${id.email}>" -m "x"`;
    const withoutExtension = runGuard(dir, cmdAuthor);
    assert.equal(withoutExtension.denied, false);
    assert.equal(withoutExtension.status, 0);
    assert.equal(withoutExtension.advisory, true);

    // With the operator-set extension env var: allowed AND silent (no
    // warning to give — the identity is genuinely sanctioned).
    const payload = JSON.stringify({
      tool: 'Bash',
      tool_input: { command: cmdAuthor },
    });
    const r = spawnSync(process.execPath, [GUARD_PATH], {
      cwd: dir,
      input: payload,
      encoding: 'utf8',
      env: {
        ...process.env,
        CLAUDE_AUTHOR_GUARD_EXTRA_IDENTITIES: id.email,
      },
    });
    const stdout = (r.stdout || '').trim();
    assert.equal(stdout, ''); // allow() writes nothing
    assert.equal(r.status, 0);
  });
});

test('deriveSanctioned never crashes on a repo with no commits yet (unborn HEAD)', () => {
  withTempRepo((dir) => {
    git(dir, ['init', '-b', 'main']);
    git(dir, ['config', 'user.name', SANCTIONED_NAME]);
    git(dir, ['config', 'user.email', SANCTIONED_EMAIL]);
    const result = runGuard(dir, 'git commit --allow-empty -m "first ever commit"');
    // Config identity alone is enough to sanction the very first commit.
    assert.equal(result.denied, false, result.reason);
    assert.equal(result.status, 0);
  });
});

test('AC-9: no sanctioned identities at all (no config, no history, no extension) is now ADVISORY, not a block — the real fail-closed control is githooks/commit-msg', () => {
  withTempRepo((dir) => {
    git(dir, ['init', '-b', 'main']);
    // Deliberately no user.name/user.email configured, local or global.
    //
    // SEC-052 ROUND 2 NOTE: this test used to force "zero derivable
    // identity" by pointing HOME/USERPROFILE/GIT_CONFIG_GLOBAL at an empty
    // directory. That is now EXACTLY the redirection attack SEC-052 round
    // 2 closed — configuredEmail() deliberately no longer honours those
    // env vars, so that trick would now just resolve this real machine's
    // real global identity instead of "nothing". Use the dedicated
    // test-only escape hatch instead (see claude-author-identity.js header
    // — safe, can only shrink the sanctioned set, never fabricate a match).
    try {
      const payload = JSON.stringify({
        tool: 'Bash',
        tool_input: { command: 'git commit --allow-empty -m "x"' },
      });
      const r = spawnSync(process.execPath, [GUARD_PATH], {
        cwd: dir,
        input: payload,
        encoding: 'utf8',
        env: {
          ...process.env,
          CLAUDE_AUTHOR_IDENTITY_TEST_FORCE_NO_CONFIGURED_EMAIL: '1',
        },
      });
      assert.equal(r.status, 0, 'AC-7: must exit 0 even with zero derivable sanctioned identities');
      const stdout = (r.stdout || '').trim();
      assert.ok(stdout, 'expected an advisory payload (AC-9: the guard must still say something)');
      const parsed = JSON.parse(stdout);
      assert.equal(parsed.hookSpecificOutput.permissionDecision, 'allow', 'AC-6/9: must be allow, never deny/ask');
      assert.match(parsed.hookSpecificOutput.permissionDecisionReason, /could not derive/);
    } finally {
      /* no temp dir to clean up now — see the note above */
    }
  });
});

test('AC-8: an internal error (shared module forced to throw) exits 0 SILENTLY — no hookSpecificOutput at all, distinct from the advisory case above', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const result = runGuard(dir, 'git commit --allow-empty -m "x"', {
      ...process.env,
      CLAUDE_AUTHOR_IDENTITY_FORCE_ERROR: '1',
    });
    assert.equal(result.status, 0, 'AC-8: internal errors fail OPEN, exit 0');
    assert.equal(result.stdout, '', 'AC-8: silence is an acceptable (and here, the actual) internal-error outcome — no fallback-derived opinion either');
  });
});

// ---------------------------------------------------------------------------
// Regression tests for BUG-044..BUG-052 — each is the Destructive agent's own
// repro against the live v1 guard, confirmed red (bypassed / falsely denied)
// against v1 before the pre-demotion fix, confirmed green (blocked) against
// v2-v4 of this guard. FEAT-045 (2026-08-11) demoted every one of these
// fifteen "is BLOCKED" fixtures to "is ADVISED, not blocked" — the command
// strings are UNCHANGED (AC-7 requires this: inverted, not deleted), only
// the asserted outcome flipped from denied/true to denied/false + status 0.
// A future regression that reintroduces a block on any of these strings is
// caught by this same fixture, by name.
// ---------------------------------------------------------------------------

test('BUG-044: git -c user.email=<fake> ... commit is now ADVISED, not BLOCKED (was a total bypass pre-fix; demoted 2026-08-11)', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `git -c user.email=${id.email} -c user.name=${id.name} commit --allow-empty -m x`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false, 'AC-7: expected -c user.email override to still be DETECTED (advisory), never blocking');
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true, 'the detection itself must still fire — only the decision changed');
  });
});

// ---------------------------------------------------------------------------
// BUG-044 round 2 (attacker "Corvid", REJECT) — parseGitInvocation()'s own
// `-c key=value` scanning used a bare `\S+` regex that never went through
// BUG-123's quote-aware hardening. `git -c user.email="fake attacker
// <fake@evil.com>" commit ...` is a single shell token (the unquoted
// `user.email=` prefix runs straight into the quoted remainder with no
// gap), but `\S+` truncated at the first space INSIDE the open quote,
// corrupted the verb parse, and reached allow() with ZERO output — total
// silent non-detection, not merely a missed override. Fixed by delegating to
// the shared `consumeShellToken()`/`dequoteShellToken()` (claude-quote-
// mask.js), the same pattern BUG-123 round 6 used in
// claude-git-commit-trigger.js. These fixtures assert the ADVISORY posture
// (never a block — AC-6/7 still apply) but, unlike the round-1 fixture
// above, exercise the exact quoting shape that reached allow() with no
// advisory at all pre-fix.
// ---------------------------------------------------------------------------

test('BUG-044 round 2 (Corvid): git -c user.email="<fake name> <fake@evil.com>" commit is ADVISED, not silently allowed (embedded space inside the quoted value)', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const cmd = 'git -c user.email="fake attacker <fake@evil.com>" commit --allow-empty -m x';
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false, 'advisory only, never a block');
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true, 'must be a real advisory, not the pre-fix silent zero-output allow');
  });
});

test('BUG-044 round 2 (Corvid): two -c overrides in one invocation, one quoted with an embedded space, both DETECTED', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const cmd = 'git -c user.email="fake@evil.com" -c user.name="Fake Attacker" commit --allow-empty -m x';
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false);
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true);
  });
});

test('BUG-044 round 2: parseGitInvocation extracts the FULL quoted value (not truncated at the embedded space) into overrides', () => {
  const text = 'git -c user.email="fake attacker <fake@evil.com>" commit --allow-empty -m x';
  const inv = guard.parseGitInvocation(text, 'git'.length);
  assert.ok(inv, 'expected a parsed invocation');
  assert.equal(inv.overrides['user.email'], 'fake attacker <fake@evil.com>');
  assert.equal(inv.verbWord, 'commit');
});

test('BUG-044 round 2: parseGitInvocation extracts BOTH quoted -c values from a two-override invocation', () => {
  const text = 'git -c user.email="fake@evil.com" -c user.name="Fake Attacker" commit -m x';
  const inv = guard.parseGitInvocation(text, 'git'.length);
  assert.ok(inv);
  assert.equal(inv.overrides['user.email'], 'fake@evil.com');
  assert.equal(inv.overrides['user.name'], 'Fake Attacker');
  assert.equal(inv.verbWord, 'commit');
});

test('BUG-044 round 2 (BUG-123-shaped): -c value with an escaped embedded double quote (key="a\\"b c") is parsed as one token, dequoted correctly', () => {
  const text = String.raw`git -c user.email="a\"b c" commit -m x`;
  const inv = guard.parseGitInvocation(text, 'git'.length);
  assert.ok(inv, 'expected a parsed invocation (odd embedded-quote count must not mispair — BUG-123 round 6 Marrow regression)');
  assert.equal(inv.overrides['user.email'], 'a"b c');
  assert.equal(inv.verbWord, 'commit');
});

test('BUG-044 round 2 (BUG-123-shaped): -c value with THREE embedded escaped quotes (odd count, generalised) still parses as one token', () => {
  const text = String.raw`git -c user.email="a\"b\"c\"d e" commit -m x`;
  const inv = guard.parseGitInvocation(text, 'git'.length);
  assert.ok(inv);
  assert.equal(inv.overrides['user.email'], 'a"b"c"d e');
  assert.equal(inv.verbWord, 'commit');
});

test('BUG-044 round 2 (BUG-123-shaped): a bare unquoted -c value (no spaces) is unaffected by the rewrite', () => {
  const text = 'git -c user.email=fake@evil.com commit -m x';
  const inv = guard.parseGitInvocation(text, 'git'.length);
  assert.ok(inv);
  assert.equal(inv.overrides['user.email'], 'fake@evil.com');
  assert.equal(inv.verbWord, 'commit');
});

test('BUG-044 round 2 (BUG-123-shaped): a fully single-quoted -c value with an embedded space is parsed as one token (mixed quote style)', () => {
  const text = "git -c user.name='Fake Attacker' commit -m x";
  const inv = guard.parseGitInvocation(text, 'git'.length);
  assert.ok(inv);
  assert.equal(inv.overrides['user.name'], 'Fake Attacker');
  assert.equal(inv.verbWord, 'commit');
});

test('BUG-044 round 2 (BUG-123-shaped): an unterminated quote in a -c value fails the option parse cleanly (fail-closed on the token, not a crash or a corrupted verb)', () => {
  const text = 'git -c user.email="unterminated commit -m x';
  const inv = guard.parseGitInvocation(text, 'git'.length);
  // consumeShellToken swallows to EOF inside the open quote and returns -1;
  // parseGitInvocation stops the option loop and then finds no whitespace-
  // then-word verb shape left in the (fully-consumed) remainder, so the
  // whole invocation is correctly treated as unparseable rather than
  // guessing at a verb from inside the corrupted value.
  assert.equal(inv, null);
});

test('BUG-044 round 2 (BUG-123-shaped) end-to-end: -c value with an embedded escaped quote and a fabricated identity is ADVISED, not silently allowed', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const cmd = String.raw`git -c user.email="a\"b fake@evil.com" commit --allow-empty -m x`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false);
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true);
  });
});

test('BUG-045: git commit wrapped in bash -c \'...\' is now ADVISED, not BLOCKED', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `bash -c 'git commit --allow-empty --author="${id.name} <${id.email}>" -m x'`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false, 'expected the guard to still see through the bash -c wrapper (advisory)');
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true);
  });
});

test('BUG-045: git commit wrapped in sh -c \'...\' is now ADVISED, not BLOCKED (same class, different shell)', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `sh -c 'git commit --allow-empty --author="${id.name} <${id.email}>" -m x'`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false);
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true);
  });
});

test('BUG-045: git commit wrapped in powershell -Command "..." is now ADVISED, not BLOCKED', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `powershell -Command "git commit --allow-empty --author='${id.name} <${id.email}>' -m x"`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false);
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true);
  });
});

test('BUG-332 r6: git commit hidden inside an eval \'...\' body is now ADVISED, not BLOCKED (WRAPPER_PATTERNS gains eval)', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `eval "git commit --allow-empty --author='${id.name} <${id.email}>' -m x"`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false);
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true, 'an eval-wrapped commit must be scanned like any other wrapper body');
  });
});

test('BUG-332 r6: git commit hidden inside an iex \'...\' body is now ADVISED, not BLOCKED (PowerShell eval sibling)', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `iex "git commit --allow-empty --author='${id.name} <${id.email}>' -m x"`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false);
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true, 'an iex-wrapped commit must be scanned like any other wrapper body');
  });
});

// ---------------------------------------------------------------------------
// BUG-332 r7 (r6 REJECT, attacker a4eb859218dbd0b83) — the structural
// tokenizer. The r6 wrapper-recognition regexes were anchored
// `(?:^|[;&|(\n])`, so a wrapper word reached through a reserved word
// (`else`), the `!` negation keyword, or a prefix builtin (`builtin`) never
// matched — the r6 F10-F12 spellings. The lexer's command-position
// detection (scanShellWords) recognises the wrapper word after any of those
// and extracts the run-string. F13: GIT_TOKEN_RE's token class cannot see a
// quote-SPLIT git token (`g"it"`), which r6 showed silently bypassed the
// ENTIRE guard; the lexer concatenates the fragments.
// ---------------------------------------------------------------------------

test('BUG-332 r7 F10: `! eval "..."` — the reserved-word boundary the WRAPPER_PATTERNS anchor missed — is scanned (advisory)', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `! eval "git commit --allow-empty --author='${id.name} <${id.email}>' -m x"`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false);
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true, 'a `! eval "..."` wrapper must be scanned like the boundary-anchored spelling');
  });
});

test('BUG-332 r7 F11: `builtin eval "..."` — the prefix-builtin boundary the anchor missed — is scanned (advisory)', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `builtin eval "git commit --allow-empty --author='${id.name} <${id.email}>' -m x"`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false);
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true, 'a `builtin eval "..."` wrapper must be scanned like the boundary-anchored spelling');
  });
});

test('BUG-332 r7 F12: `else eval "..."` — the reserved-word boundary the anchor missed — is scanned (advisory)', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `if true; then :; else eval "git commit --allow-empty --author='${id.name} <${id.email}>' -m x"; fi`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false);
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true, 'a `else eval "..."` wrapper must be scanned like the boundary-anchored spelling');
  });
});

test('BUG-332 r7 F13 unit: findCommitInvocation detects a quote-split `g"it" commit`, ignores `g"it" status`', () => {
  assert.ok(findCommitInvocation('g"it" commit -m "x"'), 'a quote-split git token must be a real commit invocation');
  assert.equal(findCommitInvocation('g"it" status'), null, 'a quote-split git with no commit verb is not a commit invocation');
  assert.ok(findCommitInvocation('/usr/"bin"/git commit -m "x"'), 'a quote-split inside a full path is recognised too');
});

test('BUG-332 r7 F13 e2e: `g"it" commit` with a fabricated --author is ADVISED (was a total silent bypass)', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `g"it" commit --allow-empty --author='${id.name} <${id.email}>' -m x`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false);
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true, 'a quote-split git token must be recognised as a real git invocation');
  });
});

// ---------------------------------------------------------------------------
// BUG-332 r8 (r7 REJECT, attacker a70e40f847ad58b41) — the subprocess-prefix
// arg-run model. r7's CRITICAL finding: the prefix-wrapper family
// (sudo/doas/nice/stdbuf/setsid/xargs/timeout/env/nohup) was entirely absent
// from SHELL_PREFIX_WORDS, so `sudo bash -c "…git commit --author=…"` put the
// run-string's words at ARGUMENT position (commandStart=false) and
// wrapperBodiesFromWords skipped them — the forged-author commit inside the
// body was never scanned → silent ALLOW. r8 splits prefixes into IN-SHELL
// ({command,builtin,exec} — the next word is the real command, `builtin cd`
// still shifts cwd) vs SUBPROCESS ({sudo,doas,nice,stdbuf,setsid,xargs,
// timeout,env,nohup} — argument-run model: every word up to the next
// separator is the prefix's argument, and a shell wrapper word at the end
// still EXECUTES its run-string). F15a proves the body is extracted as a
// distinct scan text for every family member; F15b is the CONTROL proving a
// wrapper word as a plain argument to a NON-prefix command (`echo bash -c`)
// stays invisible (no over-block); F15c-f prove a forged --author inside the
// body is scanned like any other wrapper spelling.
// ---------------------------------------------------------------------------

test('BUG-332 r8 F15a unit: the run-string inside each subprocess-prefix wrapper is extracted as a distinct scan text', () => {
  const cases = [
    ['sudo', 'sudo bash -c "git commit -m x"'],
    ['sudo -n (flag)', 'sudo -n bash -c "git commit -m x"'],
    ['sudo -u root (flag-value)', 'sudo -u root bash -c "git commit -m x"'],
    ['doas', 'doas bash -c "git commit -m x"'],
    ['nice', 'nice bash -c "git commit -m x"'],
    ['nice -n 10 (flag-value)', 'nice -n 10 bash -c "git commit -m x"'],
    ['stdbuf', 'stdbuf bash -c "git commit -m x"'],
    ['setsid', 'setsid bash -c "git commit -m x"'],
    ['xargs', 'xargs -I{} bash -c "git commit -m x"'],
    ['timeout', 'timeout 10 bash -c "git commit -m x"'],
    ['env', 'env bash -c "git commit -m x"'],
    ['env -i (flag)', 'env -i bash -c "git commit -m x"'],
    ['nohup', 'nohup bash -c "git commit -m x"'],
    // r8 self-audit additions: a shell named by path, a combined short-flag
    // cluster, and a quote-split prefix word all still execute their run-string.
    ['sudo /bin/bash (path)', 'sudo /bin/bash -c "git commit -m x"'],
    ['sudo /usr/bin/bash (path)', 'sudo /usr/bin/bash -c "git commit -m x"'],
    ['sudo /bin/sh (path)', 'sudo /bin/sh -c "git commit -m x"'],
    ['sudo bash -lc (combined flag)', 'sudo bash -lc "git commit -m x"'],
    ['sudo bash -l -c (split flags)', 'sudo bash -l -c "git commit -m x"'],
    ['s"udo" bash (quote-split prefix)', 's"udo" bash -c "git commit -m x"'],
  ];
  for (const [name, cmd] of cases) {
    const texts = guard.gatherScanTexts(cmd, 0);
    assert.equal(texts.length, 2, `${name}: gatherScanTexts must yield the whole text PLUS the extracted run-string`);
    assert.equal(texts[1], 'git commit -m x', `${name}: the extracted body must be the run-string, scanned as its own text`);
  }
});

test('BUG-332 r8 F15b unit: a wrapper word as a plain argument to a NON-prefix command is NOT extracted (CONTROL)', () => {
  const texts = guard.gatherScanTexts('echo bash -c "git commit -m x"', 0);
  assert.equal(texts.length, 1, 'echo just prints its arguments — the quoted text must stay an argument, never a body');
});

test('BUG-332 r8 F15c: `sudo bash -c "git commit --author=…"` — the r7 attacker\'s proven-ALLOWED spelling — is scanned (advisory)', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `sudo bash -c "git commit --allow-empty --author='${id.name} <${id.email}>' -m x"`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false);
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true, 'the forged author inside the subprocess-prefix run-string must be detected');
  });
});

test('BUG-332 r8 F15d: `sudo -n bash -c "…"` — a flag between prefix and shell — is scanned (advisory)', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `sudo -n bash -c "git commit --allow-empty --author='${id.name} <${id.email}>' -m x"`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false);
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true, 'the -n flag must not hide the run-string (command position is the prefix\'s argument run)');
  });
});

test('BUG-332 r8 F15e: `env -i bash -c "…"` — env with the -i flag — is scanned (advisory)', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `env -i bash -c "git commit --allow-empty --author='${id.name} <${id.email}>' -m x"`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false);
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true, 'an env -i run-string must be scanned like any other wrapper body');
  });
});

test('BUG-332 r8 F15f CONTROL: `echo bash -c "git commit --author=…"` — the wrapper word is an echo ARGUMENT, never executed — stays invisible', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `echo bash -c "git commit --allow-empty --author='${id.name} <${id.email}>' -m x"`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false);
    assert.equal(result.status, 0);
    assert.equal(result.advisory, false, 'echo merely prints its arguments — a bash word there is not a wrapper and must not be scanned');
  });
});

// ---------------------------------------------------------------------------
// BUG-332 r9 (r8 REJECT, attacker aafe8e49df2df3cb9) — F1-F5. The r8 whole-run
// scan only fired for SHELL_EXECUTABLE_WORDS and only recognised a run flag
// whose cluster ENDED in `c`, so four total-bypass spellings survived: F1 an
// unlisted shell (`sudo ash/fish/bash4 -c "…"`), F2 a short-flag cluster
// CONTAINING `c` but not ending in it (`bash -ci/-icf/-lci "…"`), F3
// value-taking flags parked between the shell and the run flag (`bash -O
// extglob -c "…"`, `bash -o noclobber -c "…"`, `bash --rcfile x -c "…"`), and
// F4 a heredoc body fed to a shell (`sudo bash <<'EOF' … EOF`), which
// buildQuoteMask masks opaque so the git verbs inside were never scanned.
// F16a proves the whole-run scan extracts a body for EVERY one of those
// spellings; F16b the commandStart runStringBodyAt value-taking skip; F16c the
// shell-fed heredoc body extraction; F16d the CONTROLs (non-shell heredoc,
// quoted `<<`, plain-argument wrapper); F16e an end-to-end forged-author
// advisory through an unlisted shell.
// ---------------------------------------------------------------------------

test('BUG-332 r9 F16a unit: the whole-run scan extracts a body for unlisted shells, cluster-`c` flags, and value-taking-flag spellings (F1/F2/F3)', () => {
  const cases = [
    // F1: shell names the closed list did not enumerate.
    ['sudo ash', 'sudo ash -c "git commit -m x"'],
    ['sudo fish', 'sudo fish -c "git commit -m x"'],
    ['sudo tcsh', 'sudo tcsh -c "git commit -m x"'],
    ['sudo bash4 (version suffix)', 'sudo bash4 -c "git commit -m x"'],
    ['sudo mksh', 'sudo mksh -c "git commit -m x"'],
    ['sudo busybox (ash)', 'sudo busybox ash -c "git commit -m x"'],
    // F2: short-flag cluster CONTAINING c, not ending in it.
    ['sudo bash -ci', 'sudo bash -ci "git commit -m x"'],
    ['sudo bash -icf', 'sudo bash -icf "git commit -m x"'],
    ['sudo bash -lci', 'sudo bash -lci "git commit -m x"'],
    // F3: value-taking flags + values parked before the run flag.
    ['sudo bash -O extglob -c', 'sudo bash -O extglob -c "git commit -m x"'],
    ['sudo bash -o noclobber -c', 'sudo bash -o noclobber -c "git commit -m x"'],
    ['sudo bash --rcfile x -c', 'sudo bash --rcfile x -c "git commit -m x"'],
    ['sudo bash --init-file x -c', 'sudo bash --init-file x -c "git commit -m x"'],
  ];
  for (const [name, cmd] of cases) {
    const texts = guard.gatherScanTexts(cmd, 0);
    assert.equal(texts.length, 2, `${name}: gatherScanTexts must yield the whole text PLUS the extracted run-string`);
    assert.equal(texts[1], 'git commit -m x', `${name}: the extracted body must be the run-string, scanned as its own text`);
  }
});

test('BUG-332 r9 F16b unit: a COMMAND-position shell with value-taking flags still reaches the run flag (F3, commandStart path)', () => {
  const cases = [
    'bash -O extglob -c "git commit -m x"',
    'bash -o noclobber -c "git commit -m x"',
    'bash --rcfile x -c "git commit -m x"',
    'bash --init-file x -c "git commit -m x"',
    'bash -ci "git commit -m x"',
  ];
  for (const cmd of cases) {
    const texts = guard.gatherScanTexts(cmd, 0);
    assert.equal(texts.length, 2, `${cmd}: a command-position bash must have its run-string extracted`);
    assert.equal(texts[1], 'git commit -m x', `${cmd}: extracted body must be the run-string`);
  }
});

test('BUG-332 r9 F16c unit: a shell-fed heredoc body is extracted as its own scan text (F4)', () => {
  const cases = [
    ['sudo bash <<EOF', 'sudo bash <<EOF\ngit commit -m x\nEOF'],
    ['bash <<"EOF" (quoted delimiter)', 'bash <<"EOF"\ngit add evil.go\nEOF'],
    ["sudo bash <<'EOF' (single-quoted delimiter)", "sudo bash <<'EOF'\ngit commit -m x\nEOF"],
    ['xargs -I{} bash <<EOF', 'xargs -I{} bash <<EOF\ngit commit -m x\nEOF'],
    ['heredoc after a separator', 'echo hi; sudo bash <<EOF\ngit commit -m x\nEOF'],
  ];
  for (const [name, cmd] of cases) {
    const texts = guard.gatherScanTexts(cmd, 0);
    assert.equal(texts.length, 2, `${name}: gatherScanTexts must yield the whole text PLUS the heredoc body`);
    assert.match(texts[1] || '', /git (commit|add)/, `${name}: the extracted body must contain the git verb from inside the heredoc`);
  }
});

test('BUG-332 r9 F16d unit CONTROL: a non-shell heredoc, a quoted `<<`, and a plain-argument wrapper are NOT extracted', () => {
  const cases = [
    ['cat <<EOF (data, not commands)', 'cat <<EOF\ngit commit -m x\nEOF'],
    ['git add - <<EOF (data)', 'git add - <<EOF\nbinary\nEOF'],
    ['echo "cat <<EOF" (quoted <<)', 'echo "cat <<EOF"\ngit commit -m x\nEOF'],
    ['echo bash -c (plain argument)', 'echo bash -c "git commit -m x"'],
  ];
  for (const [name, cmd] of cases) {
    const texts = guard.gatherScanTexts(cmd, 0);
    assert.equal(texts.length, 1, `${name}: must NOT be extracted — the text stays a single scan text`);
  }
});

test('BUG-332 r9 F16e: a forged --author inside an unlisted-shell run-string is scanned (advisory, F1 end-to-end)', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `sudo ash -c "git commit --allow-empty --author='${id.name} <${id.email}>' -m x"`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false);
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true, 'the forged author inside an unlisted-shell run-string must be detected');
  });
});

// ---------------------------------------------------------------------------
// BUG-332 r10 (r9 attacker F1/F4/F5): the three CRITICAL closures the r9 round
// left open, fixed to the attacker's exact acceptance bar —
// F16f  command-position shells the r9 class omitted (`osh`/`posh`/`sash`/`nu`
//       /`rbash`) AND the applet-dispatcher spelling (`busybox ash -c`) — the
//       commandStart path now uses the same whole-run scan as inPrefixArgs.
// F16g  shell-fed heredocs through privilege wrappers (`sudo -s`/`sudo -i`,
//       `su`) and right-of-pipe shells (`cat <<EOF | bash`).
// F16h  pipe-fed shell text with no heredoc (`echo "..." | bash`) extracted,
//       with the verbatim-emitter CONTROLs (grep/cat are not extracted — their
//       output is statically unknowable, an honest documented limitation).
// ---------------------------------------------------------------------------

test('BUG-332 r10 F16f unit: COMMAND-position unlisted shells and the applet-dispatcher spelling have their run-strings extracted (F1)', () => {
  const cases = [
    // r9 attacker F1: shells the r9 class omitted, AT COMMAND position.
    ['osh -c', 'osh -c "git commit -m x"'],
    ['posh -c', 'posh -c "git commit -m x"'],
    ['sash -c', 'sash -c "git commit -m x"'],
    ['nu -c', 'nu -c "git commit -m x"'],
    ['rbash -c', 'rbash -c "git commit -m x"'],
    // applet dispatcher: `ash` is a non-flag word between the shell and -c —
    // the r9 commandStart runStringBodyAt stopped at it.
    ['busybox ash -c', 'busybox ash -c "git commit -m x"'],
  ];
  for (const [name, cmd] of cases) {
    const texts = guard.gatherScanTexts(cmd, 0);
    assert.equal(texts.length, 2, `${name}: gatherScanTexts must yield the whole text PLUS the extracted run-string`);
    assert.equal(texts[1], 'git commit -m x', `${name}: the extracted body must be the run-string`);
  }
});

test('BUG-332 r10 F16g unit: heredocs fed via `sudo -s`/`sudo -i`, `su`, or a right-of-pipe shell are extracted (F4a)', () => {
  const cases = [
    ['sudo -s <<EOF', 'sudo -s <<EOF\ngit commit -m x\nEOF'],
    ['sudo -i <<EOF', 'sudo -i <<EOF\ngit add evil.go\nEOF'],
    ['su root <<EOF', 'su root <<EOF\ngit commit -m x\nEOF'],
    ['cat <<EOF | bash (right-of-pipe shell)', 'cat <<EOF | bash\ngit commit -m x\nEOF'],
    ['cat <<EOF | sh (right-of-pipe unlisted shell)', 'cat <<EOF | sh\ngit commit -m x\nEOF'],
  ];
  for (const [name, cmd] of cases) {
    const texts = guard.gatherScanTexts(cmd, 0);
    assert.equal(texts.length, 2, `${name}: gatherScanTexts must yield the whole text PLUS the heredoc body`);
    assert.match(texts[1] || '', /git (commit|add)/, `${name}: the extracted body must contain the git verb from inside the heredoc`);
  }
});

test('BUG-332 r10 F16h unit: pipe-fed shell text `echo "..." | bash` is extracted; transforming emitters and non-shell targets are NOT (F4b)', () => {
  const extracted = [
    ['echo "git commit -m x" | bash', 'echo "git commit -m x" | bash', 'git commit -m x'],
    ['printf "git add evil.go" | sh', 'printf "git add evil.go" | sh', 'git add evil.go'],
  ];
  for (const [name, cmd, body] of extracted) {
    const texts = guard.gatherScanTexts(cmd, 0);
    assert.equal(texts.length, 2, `${name}: gatherScanTexts must yield the whole text PLUS the piped body`);
    assert.equal(texts[1], body, `${name}: the piped quoted argument IS the command text bash executes`);
  }
  const controls = [
    ['echo "git commit -m x" | grep foo (no shell target)', 'echo "git commit -m x" | grep foo'],
    ['grep "git commit" f | bash (transforming emitter — honest limitation)', 'grep "git commit" f | bash'],
    ['cat "git commit -m x" | bash (not a verbatim emitter)', 'cat "git commit -m x" | bash'],
  ];
  for (const [name, cmd] of controls) {
    const texts = guard.gatherScanTexts(cmd, 0);
    assert.equal(texts.length, 1, `${name}: must NOT be extracted — the text stays a single scan text`);
  }
});

// ---------------------------------------------------------------------------
// BUG-332 r11 (r10 attacker C1-C4 + MINOR-1):
// C1  `exec -a <argv0> <shell> -c "<body>"` — exec added to the subprocess
//     prefix set, so the argv0 word keeps `bash` in the inPrefixArgs run and
//     the whole-run scan finds the -c body (F17f).
// C2  subprocess-prefix pipe targets (`echo "x" | sudo bash`, `| sudo -s`) —
//     pipeFedShellBodies branch (b) + pipeEmitterToShell (F17a).
// C3  `xargs -I{} <shell> -c "{}"` placeholder substitution (F17b).
// C4  passthrough filters (`| cat |`, `| tee |`, `| sed '' |`) walked to the
//     verbatim emitter (F17c).
// MINOR-1 constant `$()`/backtick emitters in wrapper run-strings (F17e).
// Each mechanism RED-proven (see the verdict note on the BOW item).
// ---------------------------------------------------------------------------

function hasExtractedBody(cmd, pattern) {
  const texts = guard.gatherScanTexts(cmd, 0);
  return texts.length > 1 && texts.slice(1).some((t) => pattern.test(t));
}

test('BUG-332 r11 F17a unit: subprocess-prefix pipe targets (`sudo bash`, `doas bash`, `sudo -s`, `env bash`) are extracted (C2)', () => {
  const cases = [
    'echo "cd internal/engine && git add evil.go && git commit -m x" | sudo bash',
    'echo "cd internal/engine && git add evil.go && git commit -m x" | doas bash',
    'printf "%s" "git add evil.go" | sudo sh',
    'echo "git add evil.go" | sudo -s',
    'echo "git add evil.go" | sudo -i',
    'echo "git add evil.go" | env bash',
  ];
  for (const cmd of cases) {
    assert.equal(hasExtractedBody(cmd, /git add evil\.go/), true, `must extract the piped text: ${cmd}`);
  }
});

test('BUG-332 r11 F17b unit: `xargs -I<ph> <shell> -c "<ph>"` placeholder substitution is extracted (C3)', () => {
  const cases = [
    'echo "cd internal/engine && git add evil.go && git commit -m x" | xargs -I{} bash -c "{}"',
    'echo "git add evil.go" | xargs -I% sh -c "%"',
    'printf "%s" "git add evil.go" | xargs -I{} bash -c "{}"',
    'cat <<EOF | xargs -I{} bash -c "{}"\ngit add evil.go\nEOF',
  ];
  for (const cmd of cases) {
    assert.equal(hasExtractedBody(cmd, /git add evil\.go/), true, `must extract the piped text: ${cmd}`);
  }
});

test('BUG-332 r11 F17c unit: passthrough filters (`cat`, `tee`, `sed ""`) are walked to the verbatim emitter (C4)', () => {
  const cases = [
    'echo "git add evil.go" | cat | bash',
    'echo "git add evil.go" | tee /dev/null | bash',
    'echo "git add evil.go" | sed "" | bash',
    'printf "%s" "git add evil.go" | cat | sh',
    'cat <<EOF | cat | bash\ngit add evil.go\nEOF',
    'cat <<EOF | sed \'\' | bash\ngit add evil.go\nEOF',
  ];
  for (const cmd of cases) {
    assert.equal(hasExtractedBody(cmd, /git add evil\.go/), true, `must extract the piped text: ${cmd}`);
  }
});

test('BUG-332 r11 F17d unit: CONTROLs — xargs without -I, transforming emitters, and non-shell targets stay invisible (C2/C3/C4)', () => {
  const controls = [
    'echo "git add evil.go" | xargs bash', // input becomes ARGUMENTS, not text
    'cat <<EOF | xargs bash\ngit add evil.go\nEOF',
    'echo "git add evil.go" | grep foo | bash', // transforming emitter
    'echo "git add evil.go" | sed "s/x/y/" | bash',
    'cat "git commit -m x" | bash',
    'echo "git add evil.go" | grep git', // non-shell pipe target
    'echo "git add evil.go" | xargs rm',
  ];
  for (const cmd of controls) {
    const texts = guard.gatherScanTexts(cmd, 0);
    assert.equal(texts.length, 1, `must NOT be extracted: ${cmd}`);
  }
});

test('BUG-332 r11 F17e unit: constant `$()`/backtick emitters in wrapper run-strings are unwrapped; dynamic and data substitutions are NOT (MINOR-1)', () => {
  const extracted = [
    'bash -c "$(printf "git add evil.go && git commit -m x")"',
    'bash -c "$(echo "git add evil.go")"',
    'sh -c "$(printf \'git add evil.go\')"',
    'bash -c "`printf "git add evil.go"`"',
    'sudo sh -c "$(printf \'git add evil.go\')"',
    'eval "$(printf \'git add evil.go\')"',
    // BUG-332 r12 (r11 attacker NEW-4): a CONSTANT printf format is now
    // statically evaluated (`%s`/`%%` plus a fixed escape set), so a `%s\n`
    // format with constant args unwraps the exact string printf emits. These
    // were CONTROLs in r11 (printf format = "a transform") and are the round's
    // two reported payload spellings (double- and single-quoted inner value).
    'bash -c "$(printf \'%s\n\' "git add evil.go")"',
    'bash -c "$(printf \'%s\n\' \'git add evil.go\')"',
    // BUG-332 r13 (r12 attacker NEW-C): the evaluated subset now covers the
    // full `s`/`b` conversion — flags, width (literal or `*`), and precision.
    // A width/flag that does NOT shorten the payload emits it verbatim, so the
    // old "%10s is a boundary" control (which ENCODED the NEW-C bypass, real
    // commits 4370c48/df28d64) flips to an extract case.
    'bash -c "$(printf \'%10s\' "git add evil.go")"', // payload longer than width
    'bash -c "$(printf \'%-s\' "git add evil.go")"', // left-justify flag
    'bash -c "$(printf \'%*s\' "3" "git add evil.go")"', // width from constant value
    'bash -c "$(printf \'%b\n\' "git add evil.go")"', // %b, backslash-free payload
  ];
  for (const cmd of extracted) {
    assert.equal(hasExtractedBody(cmd, /git add evil\.go/), true, `must unwrap the constant emitter: ${cmd}`);
  }
  const controls = [
    'echo "$(printf \'git add evil.go\')"', // echo prints DATA, never executes
    'cat "$(printf \'git add evil.go\')"',
    'bash -c "$(printf \'git add $VAR\')"', // variable = dynamic
    'bash -c "$(printf "git add $VAR")"',
    // r14: the payload is SPLIT so the RAW wrapper body cannot contain `git
    // add evil.go` contiguously (the r14 mask fix stopped truncating the
    // `$()` body, which is the whole point of F20b) — a multi-command
    // substitution is still dynamic, so only a genuine unwrap could surface
    // it, and none does.
    'bash -c "$(echo "a"; echo "git add" "evil.go")"', // multi-command = dynamic
    'bash -c "$(git rev-parse --short HEAD)"', // non-emitter substitution
    // BUG-332 r12 (NEW-4) declared boundaries — printf formats the evaluator
    // cannot statically know stay unwrapped (GR#23 tripwire honesty):
    'bash -c "$(printf \'%d\' \'42\')"', // non-%s conversion
    // r13: precision TRUNCATES the payload. The payload is split across the
    // values (`"git add" "evil.go"`), so the RAW body never contains the full
    // literal either — the gate is purely the evaluator, which emits only
    // `git a evil.go` (`%.5s` of `git add`), never the full payload.
    'bash -c "$(printf \'%.5s %s\' "git add" "evil.go")"', // precision truncation
    'bash -c "$(printf \'%s\' \'a\' \'b\')"', // extra values → format repeats
    'echo "git add evil.go"',
  ];
  for (const cmd of controls) {
    // garbled lexer wrapper bodies may still be extracted (pre-existing); the
    // MINOR-1 gate is that the CONSTANT EMITTER'S text is never unwrapped.
    assert.equal(hasExtractedBody(cmd, /git add evil\.go/), false, `must NOT unwrap: ${cmd}`);
  }
});

test('BUG-332 r11 F17f unit: `exec -a <argv0> <shell> -c "<body>"` keeps the shell in the prefix run and extracts its body (C1)', () => {
  const extracted = 'exec -a foo bash -c "cd internal/engine && git add evil.go && git commit -m x"';
  assert.equal(hasExtractedBody(extracted, /git add evil\.go/), true, 'the argv0 word must NOT hide the -c body');
  const controls = [
    'exec echo hi', // no shell — no run-string
    'exec git commit -m "docs: tidy"', // a real git invocation, NOT a wrapper
  ];
  for (const cmd of controls) {
    const texts = guard.gatherScanTexts(cmd, 0);
    assert.equal(texts.length, 1, `must NOT extract: ${cmd}`);
  }
});

// ---------------------------------------------------------------------------
// BUG-332 r12 (r11 attacker NEW-1..4): the four REJECT findings
// NEW-1  `bash <<< "git add … && git commit"` herestring total bypass
//        (F18a) — the lexer reads `<<<` as redirection and masks the operand
//        as ONE prose word, so neither verb is detected; the body must be
//        extracted like a shell-fed heredoc body.
// NEW-2  `xargs -I {} bash -c "{}"` — the placeholder SPACE-separated from
//        -I (F18b) — xargsPlaceholder stopped at the whitespace.
// NEW-3  GNU `xargs --replace[=STR]` long form (F18c) — only the `-I` prefix
//        was recognised. The SPACE-separated `--replace STR` is NOT a
//        substitution (GNU --replace is an OPTIONAL argument; empirically
//        `xargs --replace CMD bash -c "CMD"` executes `CMD`, never the piped
//        text), so it must stay a control, not a placeholder.
// NEW-4  constant `%s` printf emitters (F18d) — `printf '%s\n' 'git add
//        evil.go'` emits `git add evil.go\n`, which the wrapper shell
//        executes; a statically-evaluable format is no longer a "transform".
//        Also closes the GNU `-i[replstr]` shorthand (same class as -I).
// Each RED-proven (see the r12 verdict note on the BOW item).
// ---------------------------------------------------------------------------

test('BUG-332 r12 F18a unit: shell-fed herestring bodies are extracted (NEW-1)', () => {
  const extracted = [
    'bash <<< "cd internal/engine && git add evil.go && git commit -m x"',
    "sudo bash <<< 'git add evil.go'",
    'sudo -s <<< "git add evil.go"',
    'sudo -i <<< "git add evil.go"',
    'su <<< "git add evil.go"',
    "bash <<< $'git add evil.go'",
    "sh <<< 'git add evil.go'",
    'bash <<< git\\ add\\ evil.go',
    'xargs -I{} bash <<< "git add evil.go"',
  ];
  for (const cmd of extracted) {
    assert.equal(hasExtractedBody(cmd, /git add evil\.go/), true, `must extract the herestring operand: ${cmd}`);
  }
  const controls = [
    'cat <<< "git add evil.go"', // cat is not a shell — data, stays opaque
    'echo "git add evil.go"', // no herestring at all
    "bash <<< 'echo hi'", // shell-fed, but the operand holds no git add
    "bash 3<<< 'git add evil.go'", // `[N]<<<` feeds fd N, NOT stdin — not executed
  ];
  for (const cmd of controls) {
    assert.equal(hasExtractedBody(cmd, /git add evil\.go/), false, `must NOT extract git add: ${cmd}`);
  }
});

test('BUG-332 r12 F18b unit: `xargs -I {}` — placeholder SPACE-separated — is extracted (NEW-2)', () => {
  const extracted = [
    'echo "git add evil.go" | xargs -I {} bash -c "{}"',
    'echo "git add evil.go" | xargs -I {} sh -c "{}"',
  ];
  for (const cmd of extracted) {
    assert.equal(hasExtractedBody(cmd, /git add evil\.go/), true, `must extract the piped text: ${cmd}`);
  }
  const controls = [
    // -I with no shell run-string — input is substituted into grep's ARGUMENT.
    'echo "git add evil.go" | xargs -I {} grep {}',
    // -I {} still needs a shell to execute; the glued form stays a control in
    // F17d, here the spaced form must equally stay invisible without a shell.
    'echo "git add evil.go" | xargs -I {} rm {}',
  ];
  for (const cmd of controls) {
    assert.equal(hasExtractedBody(cmd, /git add evil\.go/), false, `must NOT extract: ${cmd}`);
  }
});

test('BUG-332 r12 F18c unit: GNU `xargs --replace[=STR]` long form is extracted (NEW-3)', () => {
  const extracted = [
    'echo "git add evil.go" | xargs --replace=C bash -c "C"',
    'echo "git add evil.go" | xargs --replace={} bash -c "{}"',
    'echo "git add evil.go" | xargs --replace bash -c "{}"',
  ];
  for (const cmd of extracted) {
    assert.equal(hasExtractedBody(cmd, /git add evil\.go/), true, `must extract the piped text: ${cmd}`);
  }
  const controls = [
    // EMPIRICAL (GNU findutils 4.10): --replace is an OPTIONAL argument, so a
    // SPACE-separated `--replace STR` leaves STR as the COMMAND's argv[0]
    // (`xargs --replace CMD bash -c "CMD"` runs `CMD`, never the piped text).
    'echo "git add evil.go" | xargs --replace CMD bash -c "CMD"',
    // --replace with no shell run-string — input becomes grep's ARGUMENT.
    'echo "git add evil.go" | xargs --replace={} grep {}',
  ];
  for (const cmd of controls) {
    assert.equal(hasExtractedBody(cmd, /git add evil\.go/), false, `must NOT extract: ${cmd}`);
  }
});

test('BUG-332 r12 F18d unit: constant `%s`/`%%` printf formats are evaluated (NEW-4)', () => {
  const extracted = [
    // DOUBLE-quoted inner values: an inner `"` truncates any garbled wrapper
    // body, so the literal `git add evil.go` can only reach the scan here if
    // the emitter was actually evaluated (the constant-printf unwrap itself).
    'bash -c "$(printf \'%s\n\' "git add evil.go")"',
    'sh -c "$(printf \'%s %s\' "git add" "evil.go")"',
    'bash -c "$(printf \'%s%%\' "git add evil.go")"',
    'eval "$(printf \'%s\n\' "git add evil.go")"',
    // BUG-332 r13 (NEW-C): width/flag forms that PRESERVE the payload now
    // evaluate (`%10s` of a payload longer than the width is verbatim, `%-s`,
    // `%*s` with a constant width — quoted or unquoted integer, `%b`).
    'bash -c "$(printf \'%10s\' "git add evil.go")"',
    'bash -c "$(printf \'%-s\' "git add evil.go")"',
    'bash -c "$(printf \'%*s\' "3" "git add evil.go")"',
    'bash -c "$(printf \'%*s\' 3 "git add evil.go")"',
    'bash -c "$(printf \'%b\n\' "git add evil.go")"',
  ];
  for (const cmd of extracted) {
    assert.equal(hasExtractedBody(cmd, /git add evil\.go/), true, `must unwrap the constant printf: ${cmd}`);
  }
  const controls = [
    'bash -c "$(printf \'%d\' \'42\')"', // non-%s conversion — declared boundary
    // r14: payload SPLIT across the values — the RAW body never holds the
    // contiguous literal, and the evaluator emits only `git a evil.go`
    // (`%.5s` truncates `git add`), never the full payload.
    'bash -c "$(printf \'%.5s %s\' "git add" "evil.go")"', // precision truncates → no payload
    'bash -c "$(printf \'%s\' \'a\' \'b\')"', // extra values — declared boundary
    'bash -c "$(printf \'%s\' $VAR)"', // dynamic arg — not constant
  ];
  for (const cmd of controls) {
    assert.equal(hasExtractedBody(cmd, /git add evil\.go/), false, `must NOT unwrap: ${cmd}`);
  }
});

// ---------------------------------------------------------------------------
// BUG-332 r13 (r12 attacker NEW-A..NEW-D): the four REJECT findings
// NEW-A  herestring operand flowing through a PASSTHROUGH pipe into a shell —
//        `cat <<< "git add evil.go" | bash` (real commit ef4ad5f). The r12
//        herestring gate only examined the pre-`<<<` command, so a herestring
//        fed to cat/tee/sed '' whose stdout pipes into a shell was ALLOWED.
//        F19a closes it with the SAME pipe-right check heredocs already use.
// NEW-B  xargs placeholder EMBEDDED in a larger run-string — `bash -c "{} &&
//        echo harmless"` (real commit 93d5334). pipeFedShellBodies required the
//        run-string body to EQUAL the placeholder exactly. F19b counts a
//        standalone placeholder word anywhere in the run-string.
// NEW-C  printf conversions emitting the payload VERBATIM outside the
//        `%s`/`%%` subset: `%b` (backslash-free payload), `%*s` (width from a
//        constant value), `%-s` (flag) — real commits 4370c48, df28d64. F19c
//        extends the evaluator; precision that TRUNCATES stays honest.
// NEW-D  a CONSTANT command substitution as the herestring OPERAND — `bash <<<
//        "$(printf '%s\n' 'git add evil.go')"` (real commit 2de10ff).
//        maybeConstantBody was gated to wrapper run-strings only. F19d unwraps
//        herestring operands too.
// Each RED-proven: F19 + the flipped F17e/F18d %10s cases fail before the r13
// source changes (see the r13 verdict note on the BOW item).
// ---------------------------------------------------------------------------

test('BUG-332 r13 F19a unit: herestring operands flowing through a passthrough pipe into a shell are extracted (NEW-A)', () => {
  const extracted = [
    'cat <<< "cd internal/engine && git add evil.go && git commit -m x" | bash',
    "tee <<< 'git add evil.go' | bash",
    "sed '' <<< 'git add evil.go' | sh",
    'cat <<< "git add evil.go" | sudo bash',
    'cat <<< "git add evil.go" | sudo -s',
    'cat <<< "git add evil.go" | xargs -I{} bash -c "{}"',
    'cat <<< "git add evil.go" | cat | bash',
  ];
  for (const cmd of extracted) {
    assert.equal(hasExtractedBody(cmd, /git add evil\.go/), true, `must extract the herestring operand through the pipe: ${cmd}`);
  }
  const controls = [
    'cat <<< "git add evil.go"', // no pipe — cat is DATA, never executed
    'cat <<< "git add evil.go" | grep foo', // pipe target is NOT a shell
    'echo x | bash && cat <<< "git add evil.go"', // earlier command's pipe is not THIS herestring's
  ];
  for (const cmd of controls) {
    assert.equal(hasExtractedBody(cmd, /git add evil\.go/), false, `must NOT extract: ${cmd}`);
  }
});

test('BUG-332 r13 F19b unit: xargs placeholder EMBEDDED in a larger run-string is extracted (NEW-B)', () => {
  const extracted = [
    'echo "cd internal/engine && git add evil.go && git commit -m x" | xargs -I{} bash -c "{} && echo harmless"',
    'echo "git add evil.go" | xargs -I{} sh -c "cd /tmp && {}"',
    'echo "git add evil.go" | xargs -I% bash -c "% && echo done"',
    'echo "git add evil.go" | xargs -I{} bash -c "{} | sh"',
  ];
  for (const cmd of extracted) {
    assert.equal(hasExtractedBody(cmd, /git add evil\.go/), true, `must extract the piped text: ${cmd}`);
  }
  const controls = [
    // placeholder glued inside a WORD is data, not a substitution site.
    'echo "git add evil.go" | xargs -I{} bash -c "echo not{}"',
    // non-shell xargs target — the substituted text is an ARGUMENT.
    'echo "git add evil.go" | xargs -I{} grep {}',
  ];
  for (const cmd of controls) {
    assert.equal(hasExtractedBody(cmd, /git add evil\.go/), false, `must NOT extract: ${cmd}`);
  }
});

test('BUG-332 r13 F19c unit: `%b`, `%*s`, `%-s` printf conversions preserving the payload are evaluated (NEW-C)', () => {
  const extracted = [
    'bash -c "$(printf \'%b\n\' "git add evil.go")"',
    'bash -c "$(printf \'%*s\' 3 "git add evil.go")"',
    'bash -c "$(printf \'%*s\' "3" "git add evil.go")"',
    'bash -c "$(printf \'%-s\' "git add evil.go")"',
    'bash -c "$(printf \'%10s\' "git add evil.go")"', // payload longer than width
  ];
  for (const cmd of extracted) {
    assert.equal(hasExtractedBody(cmd, /git add evil\.go/), true, `must unwrap the same-output printf: ${cmd}`);
  }
  const controls = [
    // r14: payload SPLIT across the values — the RAW body never holds the
    // contiguous literal, and the evaluator emits only `git a evil.go`
    // (`%.5s` truncates `git add`), never the full payload.
    'bash -c "$(printf \'%.5s %s\' "git add" "evil.go")"', // precision TRUNCATES → no payload
    'bash -c "$(printf \'%d\' \'42\')"', // non-string conversion — boundary
    'bash -c "$(printf \'%s\' \'a\' \'b\')"', // extra values → format repeats
  ];
  for (const cmd of controls) {
    assert.equal(hasExtractedBody(cmd, /git add evil\.go/), false, `must NOT unwrap: ${cmd}`);
  }
});

test('BUG-332 r13 F19d unit: constant command substitutions as herestring operands are unwrapped (NEW-D)', () => {
  // The payload is SPLIT across the format's values so the RAW operand body
  // (`$(printf '%s %s' 'git add' 'evil.go')`) never contains `git add
  // evil.go` contiguously — only the constant-substitution UNWRAP can produce
  // it. This isolates the NEW-D mechanism (a raw-operand substring match would
  // be a false pass, exactly the class this fix closes).
  const extracted = [
    'bash <<< "$(printf \'%s %s\' \'git add\' \'evil.go\')"',
    'sh <<< "$(printf \'%s %s\' \'git add\' \'evil.go\')"',
    'sudo bash <<< "$(printf \'%s %s\' \'git add\' \'evil.go\')"',
  ];
  for (const cmd of extracted) {
    assert.equal(hasExtractedBody(cmd, /git add evil\.go/), true, `must unwrap the constant operand: ${cmd}`);
  }
  const controls = [
    'cat <<< "$(printf \'%s\n\' \'git add evil.go\')"', // cat is DATA, not a shell
    'bash <<< "$(printf \'%d\' \'42\')"', // non-string conversion — boundary
  ];
  for (const cmd of controls) {
    assert.equal(hasExtractedBody(cmd, /git add evil\.go/), false, `must NOT unwrap: ${cmd}`);
  }
});

// ---------------------------------------------------------------------------
// BUG-332 r14 (r13 attacker F1/F2/F3): the three total-bypass classes the r13
// independent attacker REJECTed on.
// F20a  `|&` (stderr-merged) pipes — `isPipeTarget` and `pipeBefore` only
//       recognised a bare `|` as the character before the target word, so
//       `echo "…" |& bash` was never a pipe target.
// F20b  UNESCAPED nested double quotes inside `$(…)` in a wrapper run-string —
//       `bash -c "cat <<< '$(printf "%s\n" "…")' | bash"`. buildQuoteMask and
//       the WRAPPER_PATTERNS double-quote capture both stopped the outer quote
//       at the first inner `"`, truncating the body BEFORE the payload; and the
//       double-quote dequotes (`dequoteShellToken` / `unescapeDoubleQuoted`)
//       collapsed `\n`→`n`, mangling the `%s\n` format a constant-printf eval
//       needs. A real shell parses the quotes INSIDE `$()` in their own context
//       and keeps `\n` literal inside double quotes.
// F20c  hex escapes in `printf %b` — evalBString decoded `\a…\nnn` but not
//       `\xHH`, so a fully hex-encoded payload (`%b '\x67\x69\x74…'`) stayed
//       literal and the emitted command was never recognised.
// ---------------------------------------------------------------------------

test('BUG-332 r14 F20a unit: `|&` (stderr-merged) pipes into a shell are extracted; a non-shell `|&` target is not (F1)', () => {
  const extracted = [
    'echo "git add evil.go && git commit -m x" |& bash',
    "printf '%s\\n' \"git add evil.go\" |& bash",
    'echo "git add evil.go" |& xargs -I{} bash -c "{}"',
    'echo "git add evil.go" |& sudo bash',
  ];
  for (const cmd of extracted) {
    assert.equal(hasExtractedBody(cmd, /git add evil\.go/), true, `must extract the piped text through |&: ${cmd}`);
  }
  const controls = [
    'echo "git add evil.go" |& grep foo', // |& into a NON-shell target is data
    'echo "git add evil.go" & bash', // background & separator, not a pipe
  ];
  for (const cmd of controls) {
    assert.equal(hasExtractedBody(cmd, /git add evil\.go/), false, `must NOT extract: ${cmd}`);
  }
});

test('BUG-332 r14 F20b unit: wrapper bodies with UNESCAPED nested double quotes inside `$(…)` are extracted whole (F2)', () => {
  const extracted = [
    'bash -c "cat <<< \'$(printf "%s\\n" "git add evil.go && git commit -m x")\' | bash"',
    'eval "cat <<< \'$(printf "%s\\n" "git add evil.go && git commit -m x")\' | bash"',
    'bash -c "echo \'$(printf "%s\\n" "git add evil.go && git commit -m x")\' | bash"',
  ];
  for (const cmd of extracted) {
    assert.equal(hasExtractedBody(cmd, /git add evil\.go/), true, `must extract the full body past the nested quotes: ${cmd}`);
  }
  // The ESCAPED-inner-quote spelling — the r13 attacker's own control, which
  // must STAY denied — is the same body shape, just with `\"` instead of a bare
  // `"`, so it must also extract (the guard cannot regress it into an ALLOW).
  const escaped = 'bash -c "cat <<< \'$(printf \\"%s\\\\n\\" \\"git add evil.go && git commit -m x\\")\' | bash"';
  assert.equal(hasExtractedBody(escaped, /git add evil\.go/), true, 'the escaped-inner-quote control must stay extracted');
});

test('BUG-332 r14 F20c unit: hex escapes in `printf %b` are decoded, exposing the emitted command (F3)', () => {
  const hex = '\\x67\\x69\\x74\\x20\\x61\\x64\\x64\\x20\\x65\\x76\\x69\\x6c\\x2e\\x67\\x6f';
  const extracted = [
    `bash -c "$(printf '%b' '${hex}')"`,
    `bash <<< "$(printf '%b' '${hex}')"`,
  ];
  for (const cmd of extracted) {
    assert.equal(hasExtractedBody(cmd, /git add evil\.go/), true, `must decode the hex payload: ${cmd}`);
  }
});

// ---------------------------------------------------------------------------
// BUG-332 r15 (r14 attacker): deterministic decoder pipes — the recognition
// layer must fail closed because the raw command has NO git verb (the decoded
// payload is the commit). Unit tests assert guard.hasDecoderFedShell() (the
// exported detector the destructive guard calls before isCommitInvocation).
// ---------------------------------------------------------------------------

test('BUG-332 r15 F21 unit: the six proven decoder-pipe spellings all flag hasDecoderFedShell (r14 attacker bar)', () => {
  const payload = 'cd internal/engine && git add evil.go && git commit -m x';
  const b64 = Buffer.from(payload, 'utf8').toString('base64');
  const hex = Buffer.from(payload, 'utf8').toString('hex');
  const flagged = [
    `echo '${b64}' | base64 -d | bash`,
    `base64 -d <<< '${b64}' | bash`,
    `printf '%s' '${b64}' | base64 -d | bash`,
    `echo '${b64}' | base64 --decode | bash`,
    `echo '${hex}' | xxd -r -p | bash`,
    `echo '${b64}' | openssl base64 -d | bash`,
  ];
  for (const cmd of flagged) {
    assert.equal(guard.hasDecoderFedShell(cmd), true, `must flag the decoder-fed shell: ${cmd}`);
  }
});

test('BUG-332 r15 F21 unit: sibling decoder routes flag; data/encode/no-shell controls stay clear', () => {
  const payload = 'cd internal/engine && git add evil.go && git commit -m x';
  const b64 = Buffer.from(payload, 'utf8').toString('base64');
  const flagged = [
    `echo '${b64}' | b64 -d | bash`,
    `echo '${b64}' | base32 -d | bash`,
    `cat <<EOF | base64 -d | bash\n${payload}\nEOF`,
    `echo '${b64}' | base64 -d | sudo bash`,
    `echo '${b64}' | base64 -d | xargs -I{} bash -c "{}"`,
    `echo '${b64}' | base64 -d |& bash`,
    `sudo base64 -d <<< '${b64}' | bash`,
    `bash -c "echo '${b64}' | base64 -d | bash"`,
    `echo '${b64}' | base64 -d | cat | bash`,
  ];
  for (const cmd of flagged) {
    assert.equal(guard.hasDecoderFedShell(cmd), true, `must flag the sibling decoder route: ${cmd}`);
  }
  const clear = [
    `echo '${b64}' | base64 -d | grep foo`,  // decoder into a NON-shell — data
    `echo '${payload}' | base64 | bash`,      // ENCODE mode — output is base64 text, not the payload
    `echo '${b64}' | base64 -d`,              // decoder, no shell after
    `base64 -d <<< '${b64}'`,                 // decoder, no pipe-right shell
    `cat <<EOF | grep foo\n${payload}\nEOF`,  // transforming pipe-right
    `echo '${payload}' | grep foo`,           // no shell target
    `git commit -m '${payload}'`,             // a plain commit, no decoder
  ];
  for (const cmd of clear) {
    assert.equal(guard.hasDecoderFedShell(cmd), false, `must stay clear of a decoder-fed shell: ${cmd}`);
  }
});

// ---------------------------------------------------------------------------
// BUG-332 r16 (r15 attacker F1–F8): the REJECT listed 8 NEW sibling
// total-bypass classes as the r16 acceptance bar. F22a–h each prove one
// class now flags hasDecoderFedShell; F22i holds the controls that must stay
// clear (encode mode, file operands, fixed xargs programs, echo-arg data,
// keyed ciphers). RED-proven: every flagged case is false on the r15 source
// (see probe-r16.js) and true after the F22 fixes; every clear case stays
// false. The classes mirror the attacker's list exactly:
//   F1 clustered short flags    `base64 -di` — GNU getopt clusters
//   F2 openssl `-a` short form  `openssl enc -a -d`
//   F3 env-prefix wrapper       `X=1 base64 -d`
//   F4 decompressors            `gzip -d`, `xz -d`, `gunzip`
//   F5 bare `xargs sh -c`       stdin line IS the -c program
//   F6 command substitution     `$(echo B64 | base64 -d)` at command position
//   F7 backslash-newline        real `\`+LF continuations (handled by
//                               normalizeContinuations; entangled-with-F5
//                               `| \<LF> xargs sh -c` is the F5 gap)
//   F8 PowerShell surface       `-EncodedCommand`, `FromBase64String | iex`
// ---------------------------------------------------------------------------

test('BUG-332 r16 F22a unit: clustered short decode flags flag hasDecoderFedShell (F1)', () => {
  const payload = 'cd internal/engine && git add evil.go && git commit -m x';
  const b64 = Buffer.from(payload, 'utf8').toString('base64');
  for (const cmd of [
    `echo '${b64}' | base64 -di | bash`,
    `echo '${b64}' | base32 -di | bash`,
    `echo '${b64}' | base64 -dix | bash`,
  ]) {
    assert.equal(guard.hasDecoderFedShell(cmd), true, `must flag the clustered flag: ${cmd}`);
  }
});

test('BUG-332 r16 F22b unit: openssl `enc -a -d` short-form base64 flags it (F2)', () => {
  const payload = 'cd internal/engine && git add evil.go && git commit -m x';
  const b64 = Buffer.from(payload, 'utf8').toString('base64');
  for (const cmd of [
    `echo '${b64}' | openssl enc -a -d | bash`,
    `echo '${b64}' | openssl enc -d -a | bash`,
  ]) {
    assert.equal(guard.hasDecoderFedShell(cmd), true, `must flag the openssl -a form: ${cmd}`);
  }
});

test('BUG-332 r16 F22c unit: env-prefix wrappers before the decoder flag it (F3)', () => {
  const payload = 'cd internal/engine && git add evil.go && git commit -m x';
  const b64 = Buffer.from(payload, 'utf8').toString('base64');
  for (const cmd of [
    `X=1 base64 -d <<< '${b64}' | bash`,
    `FOO=x echo '${b64}' | base64 -d | bash`,
    `sudo X=1 base64 -d <<< '${b64}' | bash`,
  ]) {
    assert.equal(guard.hasDecoderFedShell(cmd), true, `must flag the env-prefixed decoder: ${cmd}`);
  }
});

test('BUG-332 r16 F22d unit: decompressors as stdin→stdout stages flag it; a file operand stays clear (F4)', () => {
  const payload = 'cd internal/engine && git add evil.go && git commit -m x';
  const b64 = Buffer.from(payload, 'utf8').toString('base64');
  for (const cmd of [
    `echo '${b64}' | base64 -d | gzip -d | bash`,
    `echo '${b64}' | base64 -d | xz -d | bash`,
    `echo '${b64}' | base64 -d | gunzip | bash`,
    `echo '${b64}' | base64 -d | zcat | bash`,
    `gzip -d < file.gz | bash`, // stdin redirected — stdout still carries decoded bytes
  ]) {
    assert.equal(guard.hasDecoderFedShell(cmd), true, `must flag the stdin-fed decompressor: ${cmd}`);
  }
  assert.equal(guard.hasDecoderFedShell(`gzip -d file.gz | bash`), false,
    'file operand writes to a file — nothing piped to the shell');
});

test('BUG-332 r16 F22e unit: bare `xargs sh -c` — the piped line IS the program (F5)', () => {
  const payload = 'cd internal/engine && git add evil.go && git commit -m x';
  const b64 = Buffer.from(payload, 'utf8').toString('base64');
  // DECODER form: the xargs-piped line is the DECODED payload — the decoder
  // signal must fire.
  for (const cmd of [
    `echo '${b64}' | base64 -d | xargs sh -c`,
    `echo '${b64}' | base64 -d | xargs bash -c`,
  ]) {
    assert.equal(guard.hasDecoderFedShell(cmd), true, `must flag the decoder-fed bare xargs sh -c: ${cmd}`);
  }
  // PLAINTEXT form (no decoder): the piped line is the command string — it is
  // not the decoder signal, but gatherScanTexts must EXTRACT the payload as a
  // scan text so the commit verb is detected downstream.
  const plain = `echo '${payload}' | xargs sh -c`;
  assert.equal(guard.hasDecoderFedShell(plain), false,
    'a plaintext pipe has no decoder — the decoder signal must not fire');
  const texts = guard.gatherScanTexts(plain, 0);
  assert.equal(texts.some((t) => t === payload), true,
    'the plaintext xargs sh -c payload must be extracted as a scan text');
  // CONTROLS: a FIXED program after -c makes stdin the ARGUMENTS, never the
  // command string, so the piped text must NOT be extracted.
  const fixed = `echo '${payload}' | xargs sh -c 'echo hi'`;
  const fixedTexts = guard.gatherScanTexts(fixed, 0);
  assert.equal(fixedTexts.some((t) => t === payload), false,
    'a fixed -c program means stdin becomes arguments — the payload must NOT be extracted');
  assert.equal(guard.hasDecoderFedShell(`echo '${b64}' | xargs sh -c 'echo hi'`), false,
    'a FIXED program after -c makes stdin the ARGUMENTS, not command text');
});

test('BUG-332 r16 F22f unit: command-position command substitutions whose body ends in a decoder flag it (F6)', () => {
  const payload = 'cd internal/engine && git add evil.go && git commit -m x';
  const b64 = Buffer.from(payload, 'utf8').toString('base64');
  for (const cmd of [
    `bash -c "$(echo '${b64}' | base64 -d)"`,
    `$(echo '${b64}' | base64 -d) | bash`,
    `$(base64 -d <<< '${b64}') | bash`,
  ]) {
    assert.equal(guard.hasDecoderFedShell(cmd), true, `must flag the command-position substitution: ${cmd}`);
  }
  assert.equal(guard.hasDecoderFedShell(`echo "$(echo '${b64}' | base64 -d)"`), false,
    'a substitution in an ECHO ARGUMENT prints the decoded text as data — never executed');
});

test('BUG-332 r16 F22g unit: real backslash-newline continuations are still caught; the entangled `xargs sh -c` tail is the F5 fix (F7)', () => {
  const payload = 'cd internal/engine && git add evil.go && git commit -m x';
  const b64 = Buffer.from(payload, 'utf8').toString('base64');
  const cases = [
    `echo '${b64}' | base64 -d |\\\nbash`,
    `echo '${b64}' | base64 -d | \\\nbash`,
    `echo '${b64}' | base64 -d |\\\r\nbash`,
    `bash -c "echo '${b64}' | base64 -d |\\\nbash"`,
    `echo '${b64}' | base64 -d |\\\nxargs sh -c`, // entangled: the bs-LF then xargs ALLOW was F5
  ];
  for (const cmd of cases) {
    assert.equal(guard.hasDecoderFedShell(cmd), true, `must flag the continued decoder pipe: ${cmd}`);
  }
});

test('BUG-332 r16 F22h unit: the PowerShell base64 command surface flags it (F8)', () => {
  const payload = 'cd internal/engine && git add evil.go && git commit -m x';
  const b64 = Buffer.from(payload, 'utf8').toString('base64');
  for (const cmd of [
    `[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('${b64}')) | iex`,
    `powershell -EncodedCommand ${b64}`,
    `powershell -enc ${b64}`,
    `iex ([Convert]::FromBase64String('${b64}'))`,
  ]) {
    assert.equal(guard.hasDecoderFedShell(cmd), true, `must flag the PowerShell surface: ${cmd}`);
  }
  assert.equal(guard.hasDecoderFedShell(`powershell -Command "Write-Host 'hi'"`), false,
    'a plain -Command with no base64 stays clear');
});

test('BUG-332 r16 F22i unit: controls stay clear — encode mode, non-shell targets, no-shell decoders, keyed ciphers', () => {
  const payload = 'cd internal/engine && git add evil.go && git commit -m x';
  const b64 = Buffer.from(payload, 'utf8').toString('base64');
  const clear = [
    `echo '${payload}' | bash`,                 // plain pipe, no decoder
    `echo '${payload}' | grep foo`,             // transforming pipe-right
    `echo '${b64}' | base64 | bash`,            // ENCODE mode — output is base64 text
    `echo '${b64}' | base64 -d`,                // decoder, no shell after
    `echo '${b64}' | base64 -d | grep foo`,     // decoder into a NON-shell
    `git commit -m '${payload}'`,               // plain commit, no decoder
    `base64 -d <<< '${b64}'`,                   // decoder, no pipe-right shell
    `echo '${b64}' | openssl enc -d -aes-256-cbc | bash`, // keyed cipher — data-dependent
    `echo '${b64}' | gzip | bash`,              // COMPRESS mode
    `gzip -d file.gz | bash`,                   // file operand — writes a file
    `echo '${b64}' | xargs sh -c 'echo hi'`,    // fixed -c program — stdin becomes args
    `echo "$(echo '${b64}' | base64 -d)"`,      // substitution in an echo argument — data
  ];
  for (const cmd of clear) {
    assert.equal(guard.hasDecoderFedShell(cmd), false, `must stay clear: ${cmd}`);
  }
});

// ---------------------------------------------------------------------------
// BUG-332 r18 (r17 attacker REJECT F1-F3): three false-negative allow holes,
// each RED-proven end-to-end by the r17 attacker. F1 is the structural layer
// (classifyCommitShape); F2/F3 the decoder layer (hasDecoderFedShell). The
// regression tests below mirror the attacker's e2e proofs; controls pin the
// scope so the fix never over-reaches.
// ---------------------------------------------------------------------------

test('BUG-332 r18 (r17 F1): a commit executed through a HIDDEN git executable — no literal git token — classifies indirect and denies', () => {
  for (const cmd of [
    'GIT=git; $GIT commit -m "[FEAT-040] x"',
    '$(echo $GIT) commit -m "x"',
    'GIT=git $GIT commit -m "x"',
  ]) {
    const shape = guard.classifyCommitShape(cmd);
    assert.equal(shape.kind, 'indirect', `hidden git executable must classify indirect: ${cmd}`);
    assert.equal(shape.reason, 'hidden-commit', `reason must name hidden-commit: ${cmd}`);
  }
});

test('BUG-332 r18 (r17 F1 controls): commit words NOT preceded by a variable reference classify exactly as before', () => {
  assert.equal(guard.classifyCommitShape('git commit -m x').kind, 'plain');
  assert.equal(guard.classifyCommitShape('echo commit').kind, 'none');
  assert.equal(guard.classifyCommitShape('foo bar commit').kind, 'none');
  // `$foo bar commit` — the commit's command word is `bar`, not the variable.
  assert.equal(guard.classifyCommitShape('$foo bar commit').kind, 'none');
});

test('BUG-332 r18 (r17 F2): string-executor run-flag CLUSTERS (-ne/-pe/-ap, glued module + cluster) flag hasDecoderFedShell; -an stays clear', () => {
  const flagged = [
    "echo X | perl -MMIME::Base64 -ne 'print decode_base64($_)' | bash",
    "echo X | perl -pe 'print decode_base64($_)' | bash",
    "echo X | perl -ap 'print decode_base64($_)' | bash",
    "perl -ne 'system(\"git commit -m x\")'",
  ];
  for (const cmd of flagged) {
    assert.equal(guard.hasDecoderFedShell(cmd), true, `must flag the clustered run flag: ${cmd}`);
  }
  // `-an` has NO code-exec letter (autosplit + loop) — stays a file/loop run.
  assert.equal(guard.hasDecoderFedShell("echo X | perl -an 'print $_' | bash"), false,
    'a -an cluster (no c/e/r/p/m) is not code-exec — must stay clear');
});

test('BUG-332 r18 (r17 F3): a known data-text TRANSFORMER (sed/awk/tr with a program) feeding a shell flags hasDecoderFedShell', () => {
  const flagged = [
    "echo 'x' | sed 's/x/git commit -m \"no tag\"/' | bash",
    "echo 'x' | awk '{print \"git commit\"}' | bash",
    "echo 'x' | tr a-z A-Z | bash",
  ];
  for (const cmd of flagged) {
    assert.equal(guard.hasDecoderFedShell(cmd), true, `must flag the transformer feeding a shell: ${cmd}`);
  }
  // Scope pins: identity sed stays a passthrough walk (F17c); SELECTOR filters
  // (grep) and unknown stages stay clear (F17d / F21i) — only REWRITERS that
  // can inject text are the F3 class.
  const clear = [
    "echo 'x' | sed \"\" | bash",   // empty sed program = identity passthrough
    "echo 'x' | sort | bash",        // unknown non-transformer stage stays clear
    "echo 'x' | grep foo | bash",    // grep is a selector, out of the transformer class
  ];
  for (const cmd of clear) {
    assert.equal(guard.hasDecoderFedShell(cmd), false, `must stay clear: ${cmd}`);
  }
});

test('BUG-046: git cherry-pick with a -c user.email override is now ADVISED, not BLOCKED (was zero coverage)', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `git -c user.email=${id.email} cherry-pick HEAD`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false, 'expected cherry-pick to still be a checked verb (advisory)');
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true);
  });
});

test('BUG-046: git revert with a -c user.email override is now ADVISED, not BLOCKED (was zero coverage)', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `git -c user.email=${id.email} revert --no-edit HEAD`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false, 'expected revert to still be a checked verb (advisory)');
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true);
  });
});

test('BUG-046: git am with a -c user.email override is now ADVISED, not BLOCKED (was zero coverage)', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `git -c user.email=${id.email} am some.patch`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false, 'expected am to still be a checked verb (advisory)');
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true);
  });
});

test('BUG-046: ordinary git rebase remains untouched (deliberately out of scope, unchanged)', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    assert.equal(runGuard(dir, 'git rebase main').denied, false);
  });
});

test('BUG-047: a git alias resolving to commit is now ADVISED, not BLOCKED, when the aliased invocation carries a fabricated identity', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    git(dir, ['config', 'alias.ci', 'commit']);
    const id = fabricatedIdentity();
    const cmd = `git ci --allow-empty --author="${id.name} <${id.email}>" -m x`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false, 'expected alias.ci to still resolve to commit and be checked (advisory)');
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true);
  });
});

test('BUG-047: an alias resolving to a non-commit verb (e.g. status) is left untouched', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    git(dir, ['config', 'alias.st', 'status']);
    assert.equal(runGuard(dir, 'git st').denied, false);
  });
});

test('BUG-048: backslash-newline continuation between "git" and "commit" (bash) is now ADVISED, not BLOCKED', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `git \\\ncommit --allow-empty --author="${id.name} <${id.email}>" -m x`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false, 'expected backslash-newline to still be normalized to whitespace (advisory)');
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true);
  });
});

test('BUG-048: backtick-newline continuation between "git" and "commit" (PowerShell) is now ADVISED, not BLOCKED', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `git \`\ncommit --allow-empty --author="${id.name} <${id.email}>" -m x`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false, 'expected backtick-newline to still be normalized to whitespace (advisory)');
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true);
  });
});

test('BUG-049: git.exe commit with a fabricated author is now ADVISED, not BLOCKED (was a bypass on Windows)', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `git.exe commit --allow-empty --author="${id.name} <${id.email}>" -m x`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false, 'expected the executable basename to still be matched (advisory)');
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true);
  });
});

test('BUG-050: an ordinary commit whose -m message merely CONTAINS "--author=<email>" is ALLOWED (was, and remains, a false positive fix — unchanged by demotion)', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const cmd = 'git commit --allow-empty -m "docs: explain the --author=<email> flag in the guard header"';
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false, result.reason);
    assert.equal(result.status, 0);
  });
});

test('BUG-050: an ordinary commit with --message (long form) containing "--author=" text is ALLOWED (unchanged by demotion)', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const cmd = 'git commit --allow-empty --message "note: --author=<x> is documented here"';
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false, result.reason);
    assert.equal(result.status, 0);
  });
});

test('BUG-050: a REAL fabricated --author flag (not inside a message) is now ADVISED, not BLOCKED — the fix still does not open a hole', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `git commit --allow-empty --author="${id.name} <${id.email}>" -m "-m used earlier so this is a real regression check"`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false);
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true);
  });
});

test('BUG-051: an advisory reason never contains any sanctioned email address, but still names the field and count', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `git commit --allow-empty --author="${id.name} <${id.email}>" -m x`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false);
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true);
    assert.ok(
      !result.reason.toLowerCase().includes(SANCTIONED_EMAIL.toLowerCase()),
      'sanctioned identity address leaked into the advisory reason'
    );
    assert.match(result.reason, /sanctioned identit/i);
    assert.match(result.reason, /--author/);
  });
});

test('BUG-052/ASM-226: HISTORY_SCAN_LIMIT is the CEILING, and the per-run cap is derived — both re-exported unchanged from claude-author-identity.js, not reimplemented', () => {
  // A repo with thousands of commits is impractical to build inside a fast
  // unit test; the meaningful regression evidence here is that the bound is
  // actually wired into the git invocation, not left as a dangling constant.
  // The scan itself moved to claude-author-identity.js (AC-4) — see that
  // file's own BUG-052 test for the --max-count grep; this test asserts the
  // guard's re-exported view of the SAME bound (not a second copy).
  assert.equal(typeof guard.HISTORY_SCAN_LIMIT, 'number');
  assert.ok(guard.HISTORY_SCAN_LIMIT > 0 && Number.isFinite(guard.HISTORY_SCAN_LIMIT));
  assert.equal(guard.THRESHOLDS.HISTORY_SCAN_LIMIT, guard.HISTORY_SCAN_LIMIT);
  // ASM-226: the derivation is exposed (and shared, not a copy), so the
  // cap tests below can exercise it directly through the guard.
  assert.equal(guard.deriveScanLimit, require('./claude-author-identity.js').deriveScanLimit);
});

test('ASM-226 (GR#15): deriveScanLimit() derives the cap from the repo commit count, not the hardcoded 2000', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 5);
    withCwd(dir, () => {
      // 5 commits exist; the ceiling (2000) is far above that. A DERIVED cap
      // must be 5. A regression back to a hardcoded 2000 (or a broken
      // derivation that ignores the repo) would return 2000 and fail here.
      assert.equal(guard.deriveScanLimit(), 5);
    });
  });
});

test('ASM-226: the operator-set ceiling (CLAUDE_AUTHOR_GUARD_HISTORY_LIMIT) still bounds the derived cap — BUG-052 preserved', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 5);
    const prev = process.env.CLAUDE_AUTHOR_GUARD_HISTORY_LIMIT;
    process.env.CLAUDE_AUTHOR_GUARD_HISTORY_LIMIT = '2';
    try {
      withCwd(dir, () => {
        assert.equal(guard.deriveScanLimit(), 2, 'a ceiling below the commit count must cap the derived limit to 2');
      });
    } finally {
      if (prev === undefined) delete process.env.CLAUDE_AUTHOR_GUARD_HISTORY_LIMIT;
      else process.env.CLAUDE_AUTHOR_GUARD_HISTORY_LIMIT = prev;
    }
  });
});

test('ASM-226: the derived cap is ACTUALLY APPLIED — historyEmails() hands git --max-count=<derived>, not the hardcoded 2000', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 5);
    // Intercept at the child_process.execFileSync boundary (the same seam
    // claude-author-identity.test.js uses for its BUG-052 test): the shared
    // module destructures execFileSync ONCE at load, so we patch it on the
    // cached module object and re-require BOTH modules fresh so the spy is
    // picked up by the module's own top-level destructuring.
    const cp = require('child_process');
    const real = cp.execFileSync;
    const captured = [];
    cp.execFileSync = function (file, args, options) {
      if (file === 'git') captured.push({ args: [...args] });
      return real.apply(this, arguments);
    };
    const idPath = require.resolve('./claude-author-identity.js');
    const guardPath = require.resolve('./claude-author-guard.js');
    delete require.cache[idPath];
    delete require.cache[guardPath];
    try {
      const freshGuard = require('./claude-author-guard.js');
      withCwd(dir, () => {
        freshGuard.historyEmails();
      });
    } finally {
      cp.execFileSync = real;
      delete require.cache[idPath];
      delete require.cache[guardPath];
      require('./claude-author-identity.js'); // restore a clean cached instance
      require('./claude-author-guard.js');
    }
    const logCall = captured.find((c) => c.args[0] === 'log');
    assert.ok(logCall, `expected a git log call; captured: ${JSON.stringify(captured)}`);
    assert.ok(
      logCall.args.includes('--max-count=5'),
      `expected --max-count=5 (derived from the repo's actual 5 commits), got: ${JSON.stringify(logCall.args)}`
    );
  });
});

// ---------------------------------------------------------------------------
// Unit tests for the new class-level machinery (wrapper recursion, alias
// resolution) directly, beyond the end-to-end BUG regressions above.
// ---------------------------------------------------------------------------

test('gatherScanTexts recurses into a bash -c body and finds text inside it', () => {
  const texts = guard.gatherScanTexts("bash -c 'git commit -m x'", 0);
  assert.ok(texts.some((t) => t.includes('git commit -m x')));
});

test('gatherScanTexts does not recurse into an unrecognised command wrapper', () => {
  const texts = guard.gatherScanTexts('mytool --run "git commit -m x"', 0);
  // Only the outer (unmodified) text should be present — "mytool" is not a
  // recognised wrapper, so its quoted argument is not treated as a nested
  // command to scan; this is the stated, deliberate limitation, not a bug.
  assert.equal(texts.length, 1);
});

test('findCommitInvocation still ignores a quoted mention with no real shell separator before "git"', () => {
  assert.equal(guard.findCommitInvocation('echo "please git commit later"'), null);
});

test('resolveAlias follows a one-level alias to a known commit verb', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 1);
    git(dir, ['config', 'alias.ci', 'commit']);
    const cwd = process.cwd();
    process.chdir(dir);
    try {
      assert.equal(guard.resolveAlias('ci', 0, new Set()), 'commit');
    } finally {
      process.chdir(cwd);
    }
  });
});

test('resolveAlias leaves an unknown, non-aliased word unresolved', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 1);
    const cwd = process.cwd();
    process.chdir(dir);
    try {
      assert.equal(guard.resolveAlias('status', 0, new Set()), 'status');
    } finally {
      process.chdir(cwd);
    }
  });
});

// ---------------------------------------------------------------------------
// Tester FAIL regressions (2026-08-11) — v2's own Tester round. FEAT-045
// demoted the end-to-end assertion here too (same inversion rule as above);
// the unit-level assertions (findCommitInvocation returning null for prose)
// are unaffected by demotion and unchanged.
//
// Failure 1: BUG-043 (this guard's instance) — a quoted, non-git BOW comment
// containing the phrase "(git commit --author=..." was DENIED because
// GIT_TOKEN_RE's boundary class (`;&|(\n`) matched the "(" with no check for
// quote state. Fixed via buildQuoteMask() + the gitPos check in
// findCommitInvocation(). Both reproductions below are the Tester's own,
// used verbatim (the BOW-comment shape, and the exact end-to-end command it
// used against the live hook).
//
// Failure 2: ASM-229 claims wrapper recursion is bounded at
// MAX_WRAPPER_DEPTH = 4, but WRAPPER_PATTERNS captured a double-quoted
// body's raw escaped text (`\"`) without unescaping it, so a real second
// level of nesting never matched the wrapper regex again — recursion broke
// after one level. Fixed via unescapeDoubleQuoted() applied to the
// double-quoted capture before recursing. The chosen route is "make it work
// to the declared depth" (not "correct the doc down"), so ASM-229 remains
// accurate as originally stated.
// ---------------------------------------------------------------------------

test('BUG-043 (author-guard instance): a quoted BOW-comment mention of "(git commit --author=..." is not treated as a real invocation', () => {
  // Unit level: this is not a git command at all — findCommitInvocation must
  // return null once the quoted "(git commit ...)" text is recognised as
  // being inside a quoted argument, not after a real shell separator.
  const proseCmd =
    'node claude-bow.js comment FEAT-040 "see the guard doc (git commit --author=nottrusted@example.com is the exact bypass BUG-035 fixed)"';
  assert.equal(guard.findCommitInvocation(proseCmd), null);
});

test('BUG-043 (author-guard instance): the exact end-to-end BOW-comment command the Tester ran is ALLOWED, not denied', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const proseCmd =
      'node claude-bow.js comment FEAT-040 "see the guard doc (git commit --author=nottrusted@example.com is the exact bypass BUG-035 fixed)"';
    const result = runGuard(dir, proseCmd);
    assert.equal(result.denied, false, result.reason);
    assert.equal(result.advisory, false, 'a genuinely non-git-commit command must produce no advisory noise either');
  });
});

test('BUG-043 (author-guard instance): a REAL git commit hidden after "(" with a fabricated author is now ADVISED, not BLOCKED — the fix did not weaken real detection', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    // Unquoted "(" immediately before "git" — a genuine shell subshell/group
    // open, not prose inside a string literal. Must still be DETECTED.
    const cmd = `(git commit --allow-empty --author="${id.name} <${id.email}>" -m x)`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false, 'expected a real invocation after an unquoted "(" to still be caught (advisory)');
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true);
  });
});

test('buildQuoteMask marks quoted regions true and unquoted regions false, honoring double-quote backslash escapes', () => {
  const text = 'a "b\\"c" d \'e\' f';
  const mask = guard.buildQuoteMask(text);
  assert.equal(mask[0], false); // 'a'
  assert.equal(mask[2], true); // opening "
  assert.equal(mask[3], true); // b
  assert.equal(mask[9], false); // space after closing "
  assert.equal(mask[11], true); // e (inside single quotes)
  assert.equal(mask[14], false); // f
});

test('ASM-229: nested wrappers escaped two levels deep (bash -c "bash -c \\"...\\"") recurse to depth 2 and the fabricated identity is now ADVISED, not BLOCKED', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();

    // Build a correctly-escaped two-level-nested wrapper the way a real
    // shell would produce one: escape once per level of quoting.
    function escOnce(s) {
      return s.split('\\').join('\\\\').split('"').join('\\"');
    }
    const innerCmd = `git commit --allow-empty --author="${id.name} <${id.email}>" -m x`;
    const middleCmd = `bash -c "${escOnce(innerCmd)}"`;
    const outerCmd = `bash -c "${escOnce(middleCmd)}"`;

    // Unit level: gatherScanTexts must actually surface the innermost git
    // invocation as one of its candidate texts once unescaped.
    const texts = guard.gatherScanTexts(outerCmd, 0);
    assert.ok(
      texts.some((t) => t.includes('git commit') && t.includes(id.email)),
      'expected the twice-nested body to be recovered after unescaping'
    );

    // End-to-end: the live guard must still DETECT it (advisory only).
    const result = runGuard(dir, outerCmd);
    assert.equal(result.denied, false, 'expected the guard to still see through two levels of escaped nesting (advisory)');
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true);
  });
});

test('unescapeDoubleQuoted reverses backslash-escaped quotes and backslashes without touching plain text', () => {
  assert.equal(guard.unescapeDoubleQuoted('a\\"b'), 'a"b');
  assert.equal(guard.unescapeDoubleQuoted('a\\\\b'), 'a\\b');
  assert.equal(guard.unescapeDoubleQuoted('plain text'), 'plain text');
});

// ---------------------------------------------------------------------------
// Round 4: two live bypasses in buildQuoteMask found by the Destructive
// agent (filed as BUG-077 and BUG-078 — bounced straight to this junior
// without codes at the time of the finding, then filed by Ben afterward),
// each VERIFIED to actually execute in a real bash shell (the Tester
// substituted `echo` for `git commit` and read back the parsed arguments —
// these are not theoretical mask-flips). FEAT-045 inverted the end-to-end
// assertions below the same way as every other fixture in this file.
// ---------------------------------------------------------------------------

test('ROUND4-1 (BUG-077): buildQuoteMask does not open a phantom quote on a backslash-escaped quote OUTSIDE any quoted region', () => {
  // `\"` outside quotes is a literal `"` character to a real shell — it does
  // not start a quoted region. v1-v3 only honoured backslash escapes INSIDE
  // double quotes; outside, `\"` was read as a bare `"`, opening a phantom
  // quoted region that swallowed the real `git commit` as "prose".
  const text = 'echo \\" && git commit --author="x <x@x.com>" -m y';
  const mask = guard.buildQuoteMask(text);
  const gitPos = text.indexOf('git commit');
  assert.equal(mask[gitPos], false, 'the real git invocation must not be masked as inside a quote');
});

test('ROUND4-1 (BUG-077) end-to-end: echo \\" && git commit --author=<fake> is now ADVISED, not BLOCKED (was a total bypass)', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `echo \\" && git commit --allow-empty --author="${id.name} <${id.email}>" -m x`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false, 'expected the escaped-quote-outside-quotes case to still be DETECTED (advisory)');
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true);
  });
});

test('ROUND4-2 (BUG-078): buildQuoteMask does not let a stray quote inside a heredoc body flip parity across the terminator', () => {
  // A real shell treats heredoc bodies as opaque — no quote parsing happens
  // inside them. buildQuoteMask had no concept of heredocs, so a stray `"`
  // in ordinary heredoc prose flipped quote parity and stayed flipped
  // through the terminator and across the real `git commit` line that
  // followed it.
  const text = [
    "cat <<'EOF'",
    'some text with an unmatched " quote inside a heredoc',
    'EOF',
    'git commit --author="x <x@x.com>" -m y',
  ].join('\n');
  const mask = guard.buildQuoteMask(text);
  const gitPos = text.indexOf('git commit');
  assert.equal(mask[gitPos], false, 'the real git invocation after the heredoc must not be masked as inside a quote');
});

test('ROUND4-2 (BUG-078) end-to-end: a stray quote inside a heredoc body no longer hides a following git commit --author=<fake> — now ADVISED, not BLOCKED', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = [
      "cat <<'EOF'",
      'some text with an unmatched " quote inside a heredoc',
      'EOF',
      `git commit --allow-empty --author="${id.name} <${id.email}>" -m x`,
    ].join('\n');
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false, 'expected the heredoc-stray-quote case to still be DETECTED (advisory)');
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true);
  });
});

test('ROUND4: a stray-quote shape that a real shell rejects as a syntax error (echo "a"b" && git commit ...) still ALLOWS here — recorded as a known, NON-exploitable mask-flip, not a live bypass (unchanged by demotion — this was already an allow)', () => {
  // Verified with `bash -c 'echo "a"b" && echo REACHED'` -> "unexpected EOF
  // while looking for matching `"`", exit 2, REACHED never printed. Because
  // this shape can never reach a real shell, the guard's mask-flip here
  // (which makes it treat the rest of the line as quoted prose and ALLOW)
  // costs nothing in practice. Deliberately NOT "fixed" in this round —
  // fixing it would mean detecting shell-level syntax validity, which is a
  // different and much larger problem than the two live bypasses this round
  // targets. This test pins the current (unchanged, pre-existing) behavior
  // so a future change to buildQuoteMask notices if it moves, without
  // claiming that movement is required.
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `echo "a"b" && git commit --allow-empty --author="${id.name} <${id.email}>" -m x`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false, 'non-executable shape, pinned as an intentionally-unaddressed theoretical mask-flip');
  });
});

// ---------------------------------------------------------------------------
// Round 4, item 3: full-path git.exe invocation (sibling-guard finding,
// relayed by Ben). GIT_TOKEN_RE only ever matched the literal word "git" at
// a boundary — a path prefix before it meant no match at all, so the guard
// never engaged and every check behind it (including --author) was skipped.
// This is the same OUTCOME as BUG-044/045/049 (guard doesn't fire) reached
// by a fourth, distinct route: a plain path prefix, with no wrapper, alias,
// or continuation trick involved. FEAT-045 inverted the end-to-end
// assertions below the same way as every other fixture in this file.
// ---------------------------------------------------------------------------

test('ROUND4-3: findCommitInvocation matches git.exe reached via a full Windows path, unquoted', () => {
  const cmd = 'C:\\Program Files\\Git\\bin\\git.exe commit --author="Fake <fake@evil.com>" -m x';
  assert.ok(guard.findCommitInvocation(cmd), 'expected the full-path git.exe invocation to be recognised');
});

test('ROUND4-3: findCommitInvocation matches git.exe reached via a full Windows path, double-quoted (the path itself contains a space)', () => {
  const cmd = '"C:\\Program Files\\Git\\bin\\git.exe" commit --author="Fake <fake@evil.com>" -m x';
  assert.ok(guard.findCommitInvocation(cmd), 'expected the quoted full-path git.exe invocation to be recognised');
});

test('ROUND4-3: findCommitInvocation matches a POSIX full path (/usr/bin/git) unchanged from before', () => {
  assert.ok(guard.findCommitInvocation('/usr/bin/git commit --author="Fake <fake@evil.com>" -m x'));
});

test('ROUND4-3 end-to-end: unquoted "C:\\Program Files\\Git\\bin\\git.exe commit --author=<fake>" is now ADVISED, not BLOCKED (was a total bypass — Git for Windows\' actual default install path)', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `C:\\Program Files\\Git\\bin\\git.exe commit --allow-empty --author="${id.name} <${id.email}>" -m x`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false, 'expected the unquoted full-path case to still be DETECTED (advisory)');
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true);
  });
});

test('ROUND4-3 end-to-end: quoted "C:\\Program Files\\Git\\bin\\git.exe" commit --author=<fake> is now ADVISED, not BLOCKED', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `"C:\\Program Files\\Git\\bin\\git.exe" commit --allow-empty --author="${id.name} <${id.email}>" -m x`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false, 'expected the quoted full-path case to still be DETECTED (advisory)');
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true);
  });
});

test('ROUND4-3: widening to full-path tokens does not weaken BUG-043 — a quoted prose mention of a full git.exe path is still ALLOWED, not treated as a real invocation', () => {
  const cmd = 'echo "please C:\\Program Files\\Git\\bin\\git.exe commit later"';
  assert.equal(guard.findCommitInvocation(cmd), null, 'a quoted prose mention of a full path must not be treated as a real invocation');
});

test('ROUND4-3: widening to full-path tokens does not weaken the nine originals — an ordinary quoted BOW-comment mention with "(" is still ALLOWED', () => {
  const cmd =
    'node claude-bow.js comment FEAT-040 "see the guard doc (git commit --author=nottrusted@example.com is the exact bypass BUG-035 fixed)"';
  assert.equal(guard.findCommitInvocation(cmd), null);
});

test('ROUND4-3: findCommitInvocation still ignores plain git.exe with no path when it is not a real invocation shape (git status)', () => {
  assert.equal(guard.findCommitInvocation('git.exe status'), null);
});

// ---------------------------------------------------------------------------
// BUG-079 (P0): GIT_TOKEN_RE's left-hand boundary class was `^` / a shell
// separator ONLY — no alternative for "preceded by an ordinary leading word
// and a space." `sudo git commit ...`, `env git commit ...`, and any other
// single leading wrapper word this guard cannot enumerate put a plain space
// (not a boundary character) immediately before the scan region, so the
// WHOLE match failed and findCommitInvocation() returned null: total
// silent non-detection on completely ordinary command shapes, live-verified
// end-to-end (real bash executed the fabricated-author commit while the
// guard's stdin contract returned empty stdout / ALLOW). Fix: the boundary
// class now also accepts plain whitespace, so the git-token scan can start
// right after any leading word, not just after `^`/`;`/`&`/`|`/`(`/`\n`. The
// "is this real shell syntax or just prose" distinction remains entirely
// buildQuoteMask()'s job (BUG-043), unchanged by this fix — see the updated
// header comment above GIT_TOKEN_RE.
// ---------------------------------------------------------------------------

for (const wrapper of ['sudo', 'env', 'time', 'nice', 'command']) {
  test(`BUG-079: '${wrapper} git commit --author=<fake>' is now DETECTED (advisory) — was a total silent bypass`, () => {
    withTempRepo((dir) => {
      initRepoWithHistory(dir, 3);
      const id = fabricatedIdentity();
      const cmd = `${wrapper} git commit --allow-empty --author="${id.name} <${id.email}>" -m x`;
      const result = runGuard(dir, cmd);
      assert.equal(result.denied, false);
      assert.equal(result.status, 0);
      assert.equal(result.advisory, true, `expected '${wrapper} git commit ...' to be advised, not silently allowed`);
    });
  });
}

test("BUG-079: 'xargs -I{} git commit --author=<fake> {}'-shaped invocation is now DETECTED (advisory)", () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `echo x | xargs -I{} git commit --allow-empty --author="${id.name} <${id.email}>" -m x`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false);
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true);
  });
});

test('BUG-079: an ARBITRARY, unenumerated leading wrapper word (a made-up shell function name) is also DETECTED — proves this is not just enumerating known wrappers', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `myTotallyMadeUpWrapperFn123 git commit --allow-empty --author="${id.name} <${id.email}>" -m x`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false);
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true, 'an unenumerated wrapper word must not defeat detection either');
  });
});

test('BUG-079: findCommitInvocation finds the invocation directly for sudo/env-prefixed commands (unit-level, no subprocess)', () => {
  assert.notEqual(guard.findCommitInvocation('sudo git commit --author="x <x@x.com>" -m y'), null);
  assert.notEqual(guard.findCommitInvocation('env git commit --author="x <x@x.com>" -m y'), null);
  assert.notEqual(guard.findCommitInvocation('time git commit --author="x <x@x.com>" -m y'), null);
  assert.notEqual(guard.findCommitInvocation('nice git commit --author="x <x@x.com>" -m y'), null);
});

test('BUG-079 regression guard: plain "git commit" with NO wrapper prefix still detected (no regression from the boundary widening)', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `git commit --allow-empty --author="${id.name} <${id.email}>" -m x`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false);
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true);
  });
});

test('BUG-079 regression guard: the BUG-043/BUG-078 quote-mask fix still holds after widening the boundary class — quoted prose mentioning "git" (now reachable via a whitespace boundary too) is still ALLOWED, not treated as a real invocation', () => {
  assert.equal(guard.findCommitInvocation('echo "please git commit later"'), null);
  assert.equal(
    guard.findCommitInvocation(
      'node claude-bow.js comment FEAT-040 "see the guard doc (git commit --author=nottrusted@example.com is the exact bypass BUG-035 fixed)"'
    ),
    null
  );
});

test('BUG-079 regression guard: a wrapper-prefixed command with the fabricated author hidden in an ordinary QUOTED commit message is still ALLOWED (BUG-050 shape, now reachable via a leading wrapper word too)', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const cmd = 'sudo git commit --allow-empty -m "docs: explain the --author=<email> flag in the guard header"';
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false, result.reason);
    assert.equal(result.status, 0);
  });
});

test('BUG-079 regression guard: word-boundary widening does not turn "mygit"/"digit"/"gitlint" into false invocation matches', () => {
  assert.equal(guard.findCommitInvocation('run mygit status'), null);
  assert.equal(guard.findCommitInvocation('echo digit status'), null);
  assert.equal(guard.findCommitInvocation('gitlint check'), null);
});

// ---------------------------------------------------------------------------
// BUG-082: heredoc-body "--author=" prose is no longer scanned as a real flag
// ---------------------------------------------------------------------------

test('BUG-082: git commit -F - <<EOF whose piped-in message merely MENTIONS "--author=<email>" as prose is ALLOWED, not falsely flagged', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    // Exact fixture shape from the bug report: an ordinary commit message,
    // fed via a heredoc into `-F -`, that documents this very guard by
    // mentioning "--author=<email>" as prose — no real --author flag
    // anywhere on the command line.
    const cmd =
      'git commit --allow-empty -F - <<EOF\n' +
      'Docs: explain that --author=<email> on the command line is what BUG-035 guards against.\n' +
      'EOF';
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false, result.reason);
    assert.equal(result.status, 0);
    assert.equal(
      result.advisory,
      false,
      `expected no advisory at all for pure heredoc-body prose, got: ${result.reason}`
    );
  });
});

test('BUG-082 unit: extractAuthorFlag ignores "--author=" text that only appears inside a heredoc body', () => {
  const suffix = '--allow-empty -F - <<EOF\nDocs mention --author=<fake@fake.com> here.\nEOF';
  assert.equal(guard.extractAuthorFlag(suffix), null);
});

test('BUG-082 regression: a REAL --author flag used as an actual command-line argument (no heredoc involved) still triggers the advisory — no loss of detection', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `git commit --allow-empty --author="${id.name} <${id.email}>" -m "plain commit, no heredoc"`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false);
    assert.equal(result.status, 0);
    assert.equal(result.advisory, true, 'a real --author flag must still be detected and advised on');
    assert.match(result.reason, /--author/);
  });
});

test('BUG-082 regression: a fabricated --author hidden inside a heredoc body is STILL CAUGHT when a SECOND, real, non-heredoc --author flag is also present on the same command line — the heredoc exemption is not a blanket bypass for the rest of the line', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    // The real --author flag sits in ordinary argument position, BEFORE the
    // heredoc redirection (an unambiguous shell shape — a real flag placed
    // on the same logical command line after a heredoc's own delimiter word
    // but before its terminator would itself be ambiguous shell grammar, not
    // a meaningful bypass repro, so it is deliberately not used here). The
    // heredoc body separately mentions "--author=" as prose, exactly like
    // the BUG-082 fixture, to prove the body text is still ignored even when
    // a real flag is present elsewhere on the line.
    const cmd =
      `git commit --allow-empty --author="${id.name} <${id.email}>" -F - <<EOF\n` +
      'Docs: --author=<not-a-real-flag@example.invalid> is just prose here too.\n' +
      'EOF';
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false);
    assert.equal(result.status, 0);
    assert.equal(
      result.advisory,
      true,
      'the real --author flag elsewhere on the line must still be caught, heredoc or not'
    );
    assert.match(result.reason, /--author/);
  });
});

// ---------------------------------------------------------------------------
// BUG-163: bash allows trailing argv AFTER the heredoc delimiter word but
// still on its OWN header line (`cmd <<EOF --author=...`) — the body always
// starts on the NEXT physical line regardless. tokenize()'s heredoc branch
// used to jump straight from the delimiter word to skipping the body,
// silently dropping that same-line remainder (a real forged --author flag)
// without ever tokenizing it. Fixed by tokenizing header.afterHeader through
// the header line's own newline as ordinary text before skipping the body.
// ---------------------------------------------------------------------------

test('BUG-163: a forged --author flag placed AFTER the heredoc delimiter word, on the header line itself, is now DETECTED (was a total silent bypass)', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    // Exact fixture shape from the bug report: the forged --author flag
    // sits on the SAME line as the `<<EOF` redirect, after the delimiter
    // word — real argv to git, per bash's own grammar, even though the
    // heredoc BODY (the lines that follow) is unrelated message text.
    const cmd =
      `git commit -F - <<EOF --author="${id.name} <${id.email}>"\n` +
      'some message body\n' +
      'EOF';
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false, result.reason);
    assert.equal(result.status, 0);
    assert.equal(
      result.advisory,
      true,
      'a --author flag on the heredoc header line itself must now be caught'
    );
    assert.match(result.reason, /--author/);
  });
});

test('BUG-163 unit: extractAuthorFlag finds a --author flag placed on the heredoc header line, after the delimiter word', () => {
  const suffix = '-F - <<EOF --author="Fake <fake@evil.com>"\nsome message body\nEOF';
  const flag = extractAuthorFlag(suffix);
  assert.notEqual(flag, null, 'expected the header-line --author flag to be found, not silently dropped');
  assert.equal(flag.email, 'fake@evil.com');
});

test('BUG-163 non-regression: BUG-082s exact fixture (prose "--author=" INSIDE the heredoc BODY, not on the header line) still produces NO false advisory', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    // Identical to the BUG-082 fixture above: no trailing args on the
    // header line at all, so the BUG-163 fix (which only tokenizes the
    // header line's own remainder) must not reach into the body and must
    // not reintroduce BUG-082's false positive.
    const cmd =
      'git commit --allow-empty -F - <<EOF\n' +
      'Docs: explain that --author=<email> on the command line is what BUG-035 guards against.\n' +
      'EOF';
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false, result.reason);
    assert.equal(result.status, 0);
    assert.equal(
      result.advisory,
      false,
      `expected no advisory for pure heredoc-body prose (BUG-082 must stay fixed), got: ${result.reason}`
    );
  });
});

test('BUG-163 unit: an ordinary heredoc with nothing but the delimiter word on its header line still tokenizes exactly as before (no trailing-args regression)', () => {
  const suffix = '-F - <<EOF\nsome message body mentioning --author=<fake@fake.com>\nEOF';
  assert.equal(extractAuthorFlag(suffix), null, 'body-only prose must still be ignored');
  // The delimiter-word token itself is still present, unaffected by the fix.
  assert.deepEqual(guard.tokenize(suffix), ['-F', '-', '<<EOF']);
});

test('BUG-163 unit: two heredocs on the same command line — a forged --author on the FIRST heredocs header line is caught, and the SECOND heredocs body prose is still ignored', () => {
  const suffix =
    '-F - <<EOF --author="Fake <fake@evil.com>"\n' +
    'first body\n' +
    'EOF' +
    ' -F - <<EOF2\n' +
    'second body mentioning --author=<other@fake.com> as prose\n' +
    'EOF2';
  const flag = extractAuthorFlag(suffix);
  assert.notEqual(flag, null, 'the forged flag on the first heredoc header line must still be found');
  assert.equal(flag.email, 'fake@evil.com');
});

test('BUG-163 unit: a CRLF-terminated heredoc (BUG-081s known-open gap: terminator line not matched, heredoc swallows to EOF) does not crash tokenize() and does not fabricate a false --author match from header-line-remainder handling', () => {
  const suffix = '-F - <<EOF --allow-empty\r\nsome message\r\nEOF\r\n';
  // BUG-081 is a separate, already-logged, still-open gap (CRLF terminator
  // lines are not recognised, so the heredoc swallows to end-of-string) —
  // this test only asserts the BUG-163 fix does not regress THAT behaviour
  // or throw, and does not turn an ordinary flag on the header line into a
  // false author match.
  assert.doesNotThrow(() => guard.tokenize(suffix));
  const flag = extractAuthorFlag(suffix);
  assert.equal(flag, null, 'no --author flag on this fixture — none should be found');
});

// ---------------------------------------------------------------------------
// BUG-165: BUG-163's fix made tokenize()'s heredoc branch recursively call
// tokenize(remainder) on the header line's own trailing text, once per
// `<<word`-shaped marker found there — recursion depth was unbounded and
// proportional to marker COUNT on that one line. ~7000+ markers threw
// RangeError: Maximum call stack size exceeded. Fixed by replacing the
// recursive call with a single non-recursive scan (scanTokens(text, false))
// for the header-line remainder, capping total call depth at 2 regardless of
// marker count. Also: extractAuthorFlag() in main() had no try/catch (unlike
// identity.deriveSanctioned() nearby), so the RangeError crashed the hook
// with an uncaught native exception instead of the guard's documented
// fail-OPEN contract (AC-8) — fixed with a matching try/catch at the call
// site.
// ---------------------------------------------------------------------------

test('BUG-165: a header line with thousands of stacked <<word markers no longer stack-overflows tokenize()', () => {
  // Exact fixture shape from the bug report: one heredoc header line
  // carrying ~7000 additional `<<word`-shaped markers, followed by an
  // ordinary body/terminator for the FIRST (real) heredoc.
  const markerCount = 7000;
  const markers = Array.from({ length: markerCount }, (_, i) => `<<X${i}`).join(' ');
  const suffix = `-F - <<EOF ${markers}\nsome message body\nEOF`;
  const start = Date.now();
  let tokens;
  assert.doesNotThrow(() => {
    tokens = guard.tokenize(suffix);
  }, 'BUG-165: thousands of heredoc-shaped markers on one header line must not throw');
  const elapsedMs = Date.now() - start;
  assert.ok(Array.isArray(tokens) && tokens.length > markerCount, 'expected a real token array, not a truncated/partial result');
  assert.ok(elapsedMs < 5000, `expected the scan to complete quickly (iterative, not recursive); took ${elapsedMs}ms`);
});

test('BUG-165: the same thousands-of-markers fixture run end-to-end through the guard process exits 0 with no uncaught-exception stack trace on stderr', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const markerCount = 7000;
    const markers = Array.from({ length: markerCount }, (_, i) => `<<X${i}`).join(' ');
    const cmd = `git commit --allow-empty -F - <<EOF ${markers}\nsome message body\nEOF`;
    const result = runGuard(dir, cmd);
    assert.equal(result.status, 0, `BUG-165: guard must exit 0, not crash; stderr: ${result.stderr}`);
    assert.equal(
      /RangeError|Maximum call stack/.test(result.stderr || ''),
      false,
      `BUG-165: no uncaught RangeError/stack-overflow trace expected on stderr; got: ${result.stderr}`
    );
  });
});

test('BUG-165 non-regression: BUG-163s exact fixture (forged --author flag on a normal heredoc header line) still works correctly', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd =
      `git commit -F - <<EOF --author="${id.name} <${id.email}>"\n` +
      'some message body\n' +
      'EOF';
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false, result.reason);
    assert.equal(result.status, 0);
    assert.equal(
      result.advisory,
      true,
      'BUG-165 must not regress BUG-163: a --author flag on the heredoc header line itself must still be caught'
    );
    assert.match(result.reason, /--author/);
  });
});

test('BUG-165 non-regression: BUG-082s exact fixture (heredoc-body-only prose mentioning "--author=") still produces no false advisory', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const cmd =
      'git commit --allow-empty -F - <<EOF\n' +
      'Docs: explain that --author=<email> on the command line is what BUG-035 guards against.\n' +
      'EOF';
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false, result.reason);
    assert.equal(result.status, 0);
    assert.equal(
      result.advisory,
      false,
      `BUG-165 must not regress BUG-082: expected no advisory for pure heredoc-body prose, got: ${result.reason}`
    );
  });
});

test('BUG-165: extractAuthorFlag forced to throw synthetically (test-only escape hatch) is caught by main()s try/catch and fails OPEN, not an uncaught crash', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const result = runGuard(dir, 'git commit --allow-empty -m "x"', {
      ...process.env,
      CLAUDE_AUTHOR_GUARD_FORCE_TOKENIZE_ERROR: '1',
    });
    assert.equal(result.status, 0, 'BUG-165: a forced tokenize()/extractAuthorFlag() throw must still fail OPEN, exit 0');
    assert.equal(
      /RangeError|at extractAuthorFlag|at tokenize/.test(result.stderr || ''),
      false,
      `BUG-165: no uncaught-exception stack trace expected on stderr; got: ${result.stderr}`
    );
  });
});

// ---------------------------------------------------------------------------
// BUG-169: BUG-165's fix capped scanTokens()'s RECURSION depth at 2, but the
// same RangeError: Maximum call stack size exceeded still reproduced at a
// HIGHER marker count via a DIFFERENT unbounded construct on the same code
// path: `tokens.push(...remainderTokens)` spread the heredoc header-line
// remainder's tokens as individual call arguments, and V8 caps argument
// count per call (independent of recursion depth) at roughly 125,000-135,000
// on the build the bug was filed against. Fixed by replacing both spread-call
// sites in scanTokens()'s heredoc-remainder merge with plain loops
// (`for (const t of remainderTokens) tokens.push(t)`), which has no
// call-arity ceiling at all. Regression fixture below is well above the
// empirically-found threshold (500,000, vs. the ~125-135k ceiling) to prove
// the fix removes the ceiling entirely rather than merely raising it.
// ---------------------------------------------------------------------------

test('BUG-169: a header line with 500,000 stacked <<word markers (far above the ~125-135k V8 argument-count ceiling that broke the spread-call merge) does not stack-overflow tokenize()', () => {
  const markerCount = 500000;
  const markers = Array.from({ length: markerCount }, (_, i) => `<<X${i}`).join(' ');
  const suffix = `-F - <<EOF ${markers}\nsome message body\nEOF`;
  const start = Date.now();
  let tokens;
  assert.doesNotThrow(() => {
    tokens = guard.tokenize(suffix);
  }, 'BUG-169: half a million heredoc-shaped markers on one header line must not throw');
  const elapsedMs = Date.now() - start;
  assert.ok(
    Array.isArray(tokens) && tokens.length > markerCount,
    'expected a real token array, not a truncated/partial result'
  );
  assert.ok(
    elapsedMs < 30000,
    `expected the scan to complete within a generous bounded timeout (loop, not spread-call); took ${elapsedMs}ms`
  );
});

test('BUG-169 non-regression: BUG-165s exact fixture (7000 markers) still completes with no throw', () => {
  const markerCount = 7000;
  const markers = Array.from({ length: markerCount }, (_, i) => `<<X${i}`).join(' ');
  const suffix = `-F - <<EOF ${markers}\nsome message body\nEOF`;
  let tokens;
  assert.doesNotThrow(() => {
    tokens = guard.tokenize(suffix);
  }, 'BUG-169 must not regress BUG-165: 7000 markers must still not throw');
  assert.ok(Array.isArray(tokens) && tokens.length > markerCount);
});

test('BUG-169 non-regression: BUG-163s exact fixture (forged --author flag on the heredoc header line) still detected', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd =
      `git commit -F - <<EOF --author="${id.name} <${id.email}>"\n` +
      'some message body\n' +
      'EOF';
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false, result.reason);
    assert.equal(result.status, 0);
    assert.equal(
      result.advisory,
      true,
      'BUG-169 must not regress BUG-163: a --author flag on the heredoc header line itself must still be caught'
    );
    assert.match(result.reason, /--author/);
  });
});

test('BUG-169 non-regression: BUG-082s exact fixture (heredoc-body-only prose mentioning "--author=") still produces no false advisory', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const cmd =
      'git commit --allow-empty -F - <<EOF\n' +
      'Docs: explain that --author=<email> on the command line is what BUG-035 guards against.\n' +
      'EOF';
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, false, result.reason);
    assert.equal(result.status, 0);
    assert.equal(
      result.advisory,
      false,
      `BUG-169 must not regress BUG-082: expected no advisory for pure heredoc-body prose, got: ${result.reason}`
    );
  });
});

// ---------------------------------------------------------------------------
// BUG-224 round-4 residual / BUG-232: recognition semantics.
//
// The three round-4 REJECT bypasses all reduced to "findCommitInvocation()
// returned null for a command that really does commit" — either an
// unrecognised global option before the verb, or an alias whose body the
// leading-word heuristic launders. These tests pin the two author-guard-side
// halves of the fix: (a) benign valueless global options are consumed so the
// verb resolves (bypass #3 closed OUTRIGHT — the commit is recognised and
// gated); (b) scanGitInvocations() reports parse-failure and shell-escape
// aliases honestly so a fail-closed consumer (claude-destructive-guard.js's
// BUG-232 sweep) can deny instead of allowing silently.
// ---------------------------------------------------------------------------

test('BUG-224 bypass #3 closed: benign global options before the verb no longer defeat recognition', () => {
  for (const cmd of [
    'git -p commit -a -m "no bow tag"',
    'git -P commit -m "x"',
    'git --no-pager commit -m "x"',
    'git --paginate commit -m "x"',
    'git --no-optional-locks --no-pager commit -m "x"',
    'git -p -c user.email=x@y.z commit -m "x"', // benign opt mixed with recognised -c
  ]) {
    const inv = findCommitInvocation(cmd);
    assert.ok(inv, `expected recognition for: ${cmd}`);
    assert.equal(inv.verb, 'commit', cmd);
    assert.equal(inv.verbWord, 'commit', cmd);
  }
});

test('BUG-224: a benign-option PREFIX of a longer word does not match (--no-pagerx is not --no-pager)', () => {
  // "--no-pagerx" is not a real git option; the option run must stop there,
  // the verb-word regex must fail on its leading '-', and recognition must
  // report null (which fail-closed consumers treat as could-not-parse).
  assert.equal(findCommitInvocation('git --no-pagerx commit -m "x"'), null);
});

test('BUG-232: scanGitInvocations reports parse-failure (parsed:false) for unrecognised global options, never silence', () => {
  const { scanGitInvocations } = guard;
  for (const cmd of [
    'git --config-env=alias.ca=EV ca -m "no bow tag"', // round-4 bypass #2
    'git --exec-path=/evil/path commit -m "x"',
    'git --no-pagerx commit -m "x"',
  ]) {
    const entries = scanGitInvocations(cmd);
    assert.equal(entries.length, 1, `expected exactly one entry for: ${cmd}`);
    assert.equal(entries[0].parsed, false, `expected parsed:false for: ${cmd}`);
    assert.ok(entries[0].tail.length > 0, `tail must carry the unparsed segment for: ${cmd}`);
  }
});

test('BUG-232: scanGitInvocations resolves ordinary verbs and excludes quoted prose', () => {
  const { scanGitInvocations } = guard;
  const entries = scanGitInvocations('git status && git commit -m "run git add first"');
  // Two real invocations; the "git add" inside the -m string is prose.
  assert.equal(entries.length, 2);
  assert.equal(entries[0].parsed, true);
  assert.equal(entries[0].resolved, 'status');
  assert.equal(entries[1].parsed, true);
  assert.equal(entries[1].resolved, 'commit');
  assert.equal(entries[1].shellEscapeAlias, false);
  assert.equal(scanGitInvocations('echo "please git commit later"').length, 0);
});

test('BUG-232: scanGitInvocations bounds each invocation tail at its own unquoted shell boundary', () => {
  const { scanGitInvocations } = guard;
  const entries = scanGitInvocations('git add tools/x.js && git commit -m "a && b"');
  assert.equal(entries.length, 2);
  assert.equal(entries[0].resolved, 'add');
  // The add's tail must stop at the unquoted && — never swallow the commit.
  assert.equal(entries[0].tail.trim(), 'tools/x.js');
  // The commit's tail keeps its own quoted && intact.
  assert.ok(entries[1].tail.includes('"a && b"'));
});

test('BUG-224 bypass #1: a shell-escape alias is reported as shellEscapeAlias:true, not laundered into its leading word', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 1);
    git(dir, ['config', 'alias.ca', '!git commit -a']);
    git(dir, ['config', 'alias.sneak', '!status; git commit -a']);
    git(dir, ['config', 'alias.st', 'status']);
    withCwd(dir, () => {
      const { scanGitInvocations, resolveAliasDetailed } = guard;

      // The round-4 repro: leading word of the '!' body is 'git', which the
      // legacy resolver returns — but shellEscapeAlias must flag it.
      const ca = scanGitInvocations('git ca -m "no bow tag"');
      assert.equal(ca.length, 1);
      assert.equal(ca[0].parsed, true);
      assert.equal(ca[0].shellEscapeAlias, true, 'ca (!git commit -a) must be flagged shell-escape');

      // The laundering case: leading word resolves to the innocuous
      // 'status', yet the body commits. The flag is the only honest signal.
      const sneak = resolveAliasDetailed('sneak', 0, new Set(), {});
      assert.equal(sneak.verb, 'status');
      assert.equal(sneak.shellEscape, true, 'a "!status; git commit" body must be flagged shell-escape');

      // An ordinary (non-!) alias stays unflagged and resolves as before.
      const st = resolveAliasDetailed('st', 0, new Set(), {});
      assert.equal(st.verb, 'status');
      assert.equal(st.shellEscape, false);

      // resolveAlias (the legacy wrapper) is byte-identical to before.
      assert.equal(guard.resolveAlias('ca', 0, new Set(), {}), 'git');
      assert.equal(guard.resolveAlias('st', 0, new Set(), {}), 'status');
    });
  });
});

test('BUG-231 + shell-escape: an inline -c alias with a shell-escape body is flagged through the override path too', () => {
  const { scanGitInvocations } = guard;
  const entries = scanGitInvocations('git -c alias.zz=\'!git commit -a\' zz');
  assert.equal(entries.length, 1);
  assert.equal(entries[0].parsed, true);
  assert.equal(entries[0].shellEscapeAlias, true);
});

test('BUG-232 refactor non-regression: findCommitInvocation corpus is unchanged on top of scanGitInvocations', () => {
  // Positive corpus — every shape the pre-refactor scanner recognised.
  for (const cmd of [
    'git commit -m "x"',
    'git.exe commit -m "x"',
    '"C:\\Program Files\\Git\\bin\\git.exe" commit -m "x"',
    "'git' commit -m \"x\"",
    'cd repo && git commit -m "x"',
    'git -c user.email=a@b.c commit -m "x"',
    'bash -c \'git commit -m "x"\'',
    'git status; git commit -m "x"', // red-herring earlier token, keep scanning
  ]) {
    const inv = findCommitInvocation(cmd);
    assert.ok(inv, `expected recognition for: ${cmd}`);
    assert.equal(inv.verb, 'commit', cmd);
  }
  // Negative corpus — shapes that must stay unrecognised.
  for (const cmd of [
    'git status',
    'git rebase main',
    'echo "please git commit later"',
    'npm install',
  ]) {
    assert.equal(findCommitInvocation(cmd), null, `expected null for: ${cmd}`);
  }
});

test('BUG-224 round-5 REJECT fix: recognition cost is early-exit + memoized, never one subprocess per git token', () => {
  // The Destructive round-5 attacker showed the first scanGitInvocations()
  // shipped as an EAGER array builder: findCommitInvocation() on a command
  // whose FIRST token is the commit still alias-resolved every one of 3000
  // trailing `git status` tokens (one synchronous `git config` subprocess
  // each, ~43s inside a PreToolUse hook that fires on every Bash call).
  // Wall-clock assertions are banned in this repo's CI (verification
  // standards), so this asserts the BEHAVIOUR that made it slow instead:
  // count actual execFileSync('git', ['config', ...]) spawns by patching
  // child_process BEFORE the guard module is required (it destructures
  // execFileSync at load time), in a child node process.
  const script = `
    const cp = require('child_process');
    const orig = cp.execFileSync;
    let configCalls = 0;
    cp.execFileSync = function (file, args) {
      if (file === 'git' && Array.isArray(args) && args[0] === 'config') configCalls++;
      return orig.apply(this, arguments);
    };
    const g = require(process.argv[1]);
    let cmd = 'git commit -m x; ';
    for (let i = 0; i < 200; i++) cmd += 'git status; ';
    const inv = g.findCommitInvocation(cmd);
    if (!inv || inv.verb !== 'commit') { console.log(JSON.stringify({ err: 'recognition broke' })); process.exit(0); }
    const early = configCalls;
    configCalls = 0;
    const entries = g.scanGitInvocations(cmd);
    console.log(JSON.stringify({ early, scanned: configCalls, entryCount: entries.length }));
  `;
  const r = spawnSync(process.execPath, ['-e', script, GUARD_PATH], { encoding: 'utf8', cwd: ROOT });
  assert.equal(r.status, 0, r.stderr);
  const out = JSON.parse(r.stdout.trim());
  assert.equal(out.err, undefined, out.err);
  // commit is the FIRST token and is in KNOWN_COMMIT_VERBS (no alias lookup
  // needed): the lazy first-match path must spawn ZERO config subprocesses.
  assert.equal(out.early, 0, `findCommitInvocation spawned ${out.early} config lookups — early exit lost`);
  // The full scan classifies all 200 `git status` tokens but they share one
  // memoized resolution: at most ONE config subprocess for the whole scan.
  assert.ok(out.scanned <= 1, `scanGitInvocations spawned ${out.scanned} config lookups for 200 identical tokens — memoization lost`);
  assert.equal(out.entryCount, 201);
});

test('BUG-224 round-5: the memo is per-scan and keyed on alias overrides — different -c alias bodies do not share a result', () => {
  const { scanGitInvocations } = guard;
  // Same verb word 'zz', two different inline alias bodies in one command:
  // the second must NOT inherit the first's resolution.
  const entries = scanGitInvocations(
    "git -c alias.zz='commit -a' zz; git -c alias.zz='status' zz"
  );
  assert.equal(entries.length, 2);
  assert.equal(entries[0].resolved, 'commit');
  assert.equal(entries[1].resolved, 'status');
});
