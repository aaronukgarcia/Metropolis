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

test('BUG-052: historyEmails() bounds its git log scan — the cap now lives in claude-author-identity.js, re-exported unchanged through guard.THRESHOLDS', () => {
  // A repo with thousands of commits is impractical to build inside a fast
  // unit test; the meaningful regression evidence here is that the bound is
  // actually wired into the git invocation, not left as a dangling constant.
  // The scan itself moved to claude-author-identity.js (AC-4) — see that
  // file's own BUG-052 test for the --max-count grep; this test asserts the
  // guard's re-exported view of the SAME bound (not a second copy).
  assert.equal(typeof guard.HISTORY_SCAN_LIMIT, 'number');
  assert.ok(guard.HISTORY_SCAN_LIMIT > 0 && Number.isFinite(guard.HISTORY_SCAN_LIMIT));
  assert.equal(guard.THRESHOLDS.HISTORY_SCAN_LIMIT, guard.HISTORY_SCAN_LIMIT);
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
