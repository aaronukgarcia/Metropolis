/**
 * claude-author-guard.test.js — unit + end-to-end tests for
 * claude-author-guard.js (BUG-035).
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
 * decision conveyed via stdout (deny() writes JSON + exit 0; allow() writes
 * nothing + exit 0 — the exit code alone never distinguishes them). */
function runGuard(cwd, command) {
  const payload = JSON.stringify({ tool: 'Bash', tool_input: { command } });
  const r = spawnSync(process.execPath, [GUARD_PATH], {
    cwd,
    input: payload,
    encoding: 'utf8',
  });
  let denied = false;
  let reason = null;
  const stdout = (r.stdout || '').trim();
  if (stdout) {
    const parsed = JSON.parse(stdout);
    denied = parsed?.hookSpecificOutput?.permissionDecision === 'deny';
    reason = parsed?.hookSpecificOutput?.permissionDecisionReason || null;
  }
  return { denied, reason, stdout, stderr: r.stderr, status: r.status };
}

// ---------------------------------------------------------------------------
// Unit: findCommitInvocation
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
// End-to-end: throwaway repos only
// ---------------------------------------------------------------------------

test('ALLOWS an ordinary commit whose identity matches configured git identity', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const result = runGuard(dir, 'git commit --allow-empty -m "ordinary commit"');
    assert.equal(result.denied, false, result.reason);
  });
});

test('BLOCKS a commit whose author is overridden by --author to a fabricated identity', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `git commit --allow-empty --author="${id.name} <${id.email}>" -m "bad"`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, true);
    assert.match(result.reason, /--author/);
    assert.match(result.reason, new RegExp(id.email.replace('.', '\\.')));
  });
});

test('BLOCKS a commit whose identity comes from GIT_AUTHOR_* env vars inline in the command', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    // This is exactly BUG-035's shape: identity set via env vars, not --author.
    const cmd = `GIT_AUTHOR_NAME=${id.name} GIT_AUTHOR_EMAIL=${id.email} git commit --allow-empty -m "bad"`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, true);
    assert.match(result.reason, /GIT_AUTHOR_EMAIL/);
  });
});

test('BLOCKS a commit whose committer (not author) comes from GIT_COMMITTER_* env vars', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `GIT_COMMITTER_NAME=${id.name} GIT_COMMITTER_EMAIL=${id.email} git commit --allow-empty -m "bad"`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, true);
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
    git(dir, ['commit', '--amend', '--no-edit']);
    const authorEmail = git(dir, ['log', '-1', '--format=%ae']);
    const committerEmail = git(dir, ['log', '-1', '--format=%ce']);
    assert.equal(authorEmail, SANCTIONED_EMAIL);
    assert.equal(committerEmail, SANCTIONED_EMAIL);
  });
});

test('BLOCKS an --amend that overrides the author to a fabricated identity', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    const cmd = `git commit --amend --no-edit --author="${id.name} <${id.email}>"`;
    const result = runGuard(dir, cmd);
    assert.equal(result.denied, true);
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

test('CLAUDE_AUTHOR_GUARD_EXTRA_IDENTITIES sanctions a brand-new contributor immediately', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const id = fabricatedIdentity();
    // First, without the extension var: blocked (not yet known to history
    // or config).
    const cmdAuthor = `git commit --allow-empty --author="${id.name} <${id.email}>" -m "x"`;
    assert.equal(runGuard(dir, cmdAuthor).denied, true);

    // With the operator-set extension env var: allowed, on the FIRST commit
    // — no history accumulation needed.
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
  });
});

test('fails closed with no sanctioned identities at all (no config, no history, no extension)', () => {
  withTempRepo((dir) => {
    git(dir, ['init', '-b', 'main']);
    // Deliberately no user.name/user.email configured, local or global — use
    // -c to force git config lookups in this process to see nothing, by
    // pointing HOME/XDG at an empty dir the guard subprocess also inherits.
    const emptyHome = fs.mkdtempSync(path.join(os.tmpdir(), 'author-guard-empty-home-'));
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
          HOME: emptyHome,
          USERPROFILE: emptyHome,
          GIT_CONFIG_NOSYSTEM: '1',
          GIT_CONFIG_GLOBAL: path.join(emptyHome, 'nonexistent-gitconfig'),
        },
      });
      const stdout = (r.stdout || '').trim();
      assert.ok(stdout, 'expected a deny payload');
      const parsed = JSON.parse(stdout);
      assert.equal(parsed.hookSpecificOutput.permissionDecision, 'deny');
      assert.match(parsed.hookSpecificOutput.permissionDecisionReason, /could not derive/);
    } finally {
      fs.rmSync(emptyHome, { recursive: true, force: true });
    }
  });
});
