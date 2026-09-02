/**
 * claude-pre-push-check.test.js — closes ASM-735 (tool.prepushcheck): the
 * GR#19 Cloud-Functions-deploy-bundling PreToolUse hook had NO test file at
 * all. The ACs explicitly call for tests including a throwaway repo with a
 * fabricated functions/-touching commit to exercise the deny path — this file
 * provides both halves: pure unit tests of the argv-parsing/classification
 * logic, and a SPAWN suite driven against a real, disposable, LOCAL-ONLY git
 * fixture repo (no network, no shared state, torn down after each test).
 *
 * ASM-735 also required a small, low-risk export change to the tool file
 * itself (recorded in claude-pre-push-check.js's own header comment above
 * `function main()`): the stdin-consuming block used to run unconditionally
 * at require time, which would have attached a live stdin listener to the
 * TEST PROCESS the instant this file did `require('./claude-pre-push-check.js')`.
 * It is now wrapped in `main()`, gated by the standard
 * `if (require.main === module)` guard already used elsewhere in this repo
 * (claude-ping-check.js, claude-checkin-pipe-guard.js), with
 * `tokenize`/`isForcePush`/`parsePushTarget`/`GIT_PUSH_RE` exported — no
 * behavioural change; the real hook still runs exactly as before when
 * invoked directly by Claude Code.
 *
 * FIXTURE-REPO MECHANICS: `claude-pre-push-check.js` resolves its git working
 * directory as `const ROOT = __dirname` (hardcoded to the tool file's own
 * location, not overridable via env or cwd) — by design, since a PreToolUse
 * hook always runs from the checkout it's guarding. To point it at a
 * throwaway repo instead of this real checkout, the SPAWN tests below COPY
 * the (already-patched) tool file into the fixture repo directory and invoke
 * that copy directly, so its own `__dirname` — and therefore every git
 * command it runs — resolves inside the disposable fixture, never this repo.
 *
 * Determinism: no wall-clock assertions, no sleeps, no network (the "origin"
 * remote is a local bare repo on the filesystem, reachable only by path).
 *
 * Covers docs/planning/acceptance/tool.prepushcheck.md's ASM-735 test
 * mandate: argv/classification pure logic AND the deny-path fixture repo.
 *
 * Run: node --test claude-pre-push-check.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawnSync } = require('child_process');

const TOOL_SOURCE_PATH = path.join(__dirname, 'claude-pre-push-check.js');
const { tokenize, isForcePush, parsePushTarget, GIT_PUSH_RE } = require('./claude-pre-push-check.js');

// ── Pure unit tests: tokenize ────────────────────────────────────────────────

test('tokenize: splits whitespace-separated tokens and respects single/double quotes', () => {
  assert.deepEqual(tokenize('origin main'), ['origin', 'main']);
  assert.deepEqual(tokenize(`origin "my branch"`), ['origin', 'my branch']);
  assert.deepEqual(tokenize(`origin 'my branch'`), ['origin', 'my branch']);
  assert.deepEqual(tokenize('   '), [], 'whitespace-only input yields no tokens');
  assert.deepEqual(tokenize(''), []);
});

// ── Pure unit tests: isForcePush ─────────────────────────────────────────────

test('isForcePush: recognises every documented force-push spelling as a real token', () => {
  assert.equal(isForcePush('git push -f origin main'), true);
  assert.equal(isForcePush('git push --force origin main'), true);
  assert.equal(isForcePush('git push --force-with-lease origin main'), true);
  assert.equal(isForcePush('git push --force-with-lease=refs/heads/main:abc123 origin main'), true);
});

test('isForcePush: a bare trailing -f (no trailing space) is still exempted (the SEC-012 fix)', () => {
  assert.equal(isForcePush('git push origin main -f'), true);
});

test('isForcePush: a plain push, or a word merely containing "force", is NOT flagged', () => {
  assert.equal(isForcePush('git push origin main'), false);
  assert.equal(isForcePush('git push origin forceful-branch-name'), false,
    'a branch name containing "force" as a substring must not count as the --force flag');
});

// ── Pure unit tests: parsePushTarget ─────────────────────────────────────────

test('parsePushTarget: plain "git push origin main" resolves remote and branch', () => {
  assert.deepEqual(parsePushTarget('git push origin main'), { remote: 'origin', branch: 'main' });
});

test('parsePushTarget: a "local:remote" refspec resolves the branch to the REMOTE side', () => {
  assert.deepEqual(
    parsePushTarget('git push origin HEAD:refs/heads/feature-x'),
    { remote: 'origin', branch: 'refs/heads/feature-x' }
  );
});

test('parsePushTarget: a value-taking flag (-o/--push-option/--repo/--receive-pack) consumes its argument', () => {
  // If -o's value ("ci.skip") were mistaken for the remote, this would
  // resolve remote:"ci.skip" instead of "origin" — the real regression this
  // guards against.
  assert.deepEqual(parsePushTarget('git push -o ci.skip origin main'), { remote: 'origin', branch: 'main' });
  assert.deepEqual(parsePushTarget('git push --push-option=ignored origin main'), { remote: 'origin', branch: 'main' });
});

test('parsePushTarget: "git push" with no positional args resolves neither remote nor branch', () => {
  assert.deepEqual(parsePushTarget('git push'), { remote: null, branch: null });
});

test('parsePushTarget: a bare flag with no remote/branch still resolves nulls, not a mis-picked flag', () => {
  const { remote, branch } = parsePushTarget('git push --verbose');
  assert.equal(remote, null);
  assert.equal(branch, null);
});

// ── Pure unit tests: GIT_PUSH_RE (real invocation, not a bare mention) ───────

test('GIT_PUSH_RE: matches a real invocation at the start of the command or after a shell separator', () => {
  assert.match('git push origin main', GIT_PUSH_RE);
  assert.match('cd /repo && git push', GIT_PUSH_RE);
  assert.match('foo; git push -f', GIT_PUSH_RE);
  assert.match('git -C /some/repo push origin main', GIT_PUSH_RE, 'a -C <dir> prefix must still count as a real push');
});

test('GIT_PUSH_RE: does NOT match "push" appearing as a bare word with no preceding "git push" boundary', () => {
  assert.doesNotMatch('echo "please push the branch"', GIT_PUSH_RE);
  assert.doesNotMatch('git status', GIT_PUSH_RE);
});

test('GIT_PUSH_RE: any remote/branch name matches — not gated on the literal words "main"/"origin" (the SEC-012 fix)', () => {
  assert.match('git push upstream release', GIT_PUSH_RE);
  assert.match('git push backup master', GIT_PUSH_RE);
});

// ── SPAWN: real hook against a throwaway, local-only git fixture repo ───────

/** Run a git command in `cwd`, throwing on failure (fixture setup only — the
 *  hook under test never uses this helper). */
function gitFixture(cwd, args) {
  const r = spawnSync('git', args, { cwd, encoding: 'utf8' });
  if (r.status !== 0) {
    throw new Error(`fixture git ${args.join(' ')} failed in ${cwd}: ${r.stderr || r.stdout}`);
  }
  return r.stdout;
}

/** Build a throwaway origin (bare) + work (checked-out) repo pair, with the
 *  work repo's main branch already tracking origin/main via one clean initial
 *  commit (no functions/ files) — so later commits made ONLY in the work repo
 *  are exactly the "pending, not yet pushed" commits the hook inspects. The
 *  patched claude-pre-push-check.js is copied into the WORK repo so its
 *  hardcoded `__dirname`-based ROOT resolves there. Returns { workDir, hookPath, cleanup }. */
function makeFixtureRepo() {
  const base = fs.mkdtempSync(path.join(os.tmpdir(), 'prepush-fixture-'));
  const originDir = path.join(base, 'origin.git');
  const workDir = path.join(base, 'work');
  fs.mkdirSync(originDir);
  fs.mkdirSync(workDir);

  gitFixture(originDir, ['init', '--bare', '-q']);

  gitFixture(workDir, ['init', '-q']);
  gitFixture(workDir, ['config', 'user.email', 'fixture@example.invalid']);
  gitFixture(workDir, ['config', 'user.name', 'Fixture Bot']);
  // Deterministic default branch name regardless of the operator's global config.
  gitFixture(workDir, ['checkout', '-q', '-b', 'main']);
  fs.writeFileSync(path.join(workDir, 'README.md'), 'fixture repo\n', 'utf8');
  gitFixture(workDir, ['add', 'README.md']);
  gitFixture(workDir, ['commit', '-q', '-m', 'initial commit']);
  gitFixture(workDir, ['remote', 'add', 'origin', originDir]);
  gitFixture(workDir, ['push', '-q', '-u', 'origin', 'main']);

  const hookPath = path.join(workDir, 'claude-pre-push-check.js');
  fs.copyFileSync(TOOL_SOURCE_PATH, hookPath);

  return {
    workDir,
    hookPath,
    /** Commit a file under functions/ (so the hook's touchesFunctions check
     *  fires) with the given commit message, WITHOUT pushing. */
    commitFunctionsChange(message, fileContent = 'exports.foo = () => {};\n') {
      const fnDir = path.join(workDir, 'functions');
      fs.mkdirSync(fnDir, { recursive: true });
      const file = path.join(fnDir, `f-${Date.now()}-${Math.random().toString(36).slice(2)}.js`);
      fs.writeFileSync(file, fileContent, 'utf8');
      gitFixture(workDir, ['add', '.']);
      gitFixture(workDir, ['commit', '-q', '-m', message]);
    },
    /** Commit a change OUTSIDE functions/, without pushing. */
    commitNonFunctionsChange(message) {
      const file = path.join(workDir, `note-${Date.now()}.md`);
      fs.writeFileSync(file, 'not a function\n', 'utf8');
      gitFixture(workDir, ['add', '.']);
      gitFixture(workDir, ['commit', '-q', '-m', message]);
    },
    cleanup() {
      fs.rmSync(base, { recursive: true, force: true });
    },
  };
}

/** Run the fixture's copy of the hook, simulating the given `git push`
 *  command string arriving on stdin exactly as Claude Code's PreToolUse
 *  payload does. */
function runHookOnFixture(fixture, command, envOverrides = {}) {
  return spawnSync(process.execPath, [fixture.hookPath], {
    input: JSON.stringify({ tool: 'Bash', tool_input: { command } }),
    encoding: 'utf8',
    env: { ...process.env, ...envOverrides },
    timeout: 10000,
  });
}

function parseDenyReason(stdout) {
  if (!stdout || !stdout.trim()) return null;
  const parsed = JSON.parse(stdout);
  return parsed?.hookSpecificOutput?.permissionDecisionReason ?? null;
}

test('SPAWN deny path: a functions/-touching commit with NO bundled deploy line is BLOCKED', () => {
  const fixture = makeFixtureRepo();
  try {
    fixture.commitFunctionsChange('add a new cloud function');
    const r = runHookOnFixture(fixture, 'git push origin main');

    assert.equal(r.status, 0, 'a PreToolUse deny is signalled via JSON output, not a nonzero exit');
    const reason = parseDenyReason(r.stdout);
    assert.ok(reason, `expected a deny JSON payload, got stdout=${JSON.stringify(r.stdout)} stderr=${JSON.stringify(r.stderr)}`);
    assert.match(reason, /GOLDEN RULE #19/);
    assert.match(reason, /firebase deploy --only functions/);
  } finally {
    fixture.cleanup();
  }
});

test('SPAWN allow path: a functions/-touching commit WITH a bundled deploy line is ALLOWED', () => {
  const fixture = makeFixtureRepo();
  try {
    fixture.commitFunctionsChange(
      'add a new cloud function\n\nfirebase deploy --only functions:foo'
    );
    const r = runHookOnFixture(fixture, 'git push origin main');
    assert.equal(r.status, 0);
    assert.equal(r.stdout.trim(), '', 'a correctly-bundled commit must produce no deny output at all');
  } finally {
    fixture.cleanup();
  }
});

test('SPAWN allow path: a commit that never touches functions/ is ALLOWED regardless of message', () => {
  const fixture = makeFixtureRepo();
  try {
    fixture.commitNonFunctionsChange('just a docs tweak');
    const r = runHookOnFixture(fixture, 'git push origin main');
    assert.equal(r.status, 0);
    assert.equal(r.stdout.trim(), '');
  } finally {
    fixture.cleanup();
  }
});

test('SPAWN escape hatch: CLAUDE_DISABLE_PUSH_CHECK=1 allows an otherwise-offending push', () => {
  const fixture = makeFixtureRepo();
  try {
    fixture.commitFunctionsChange('add a new cloud function, no deploy line');
    const r = runHookOnFixture(fixture, 'git push origin main', { CLAUDE_DISABLE_PUSH_CHECK: '1' });
    assert.equal(r.status, 0);
    assert.equal(r.stdout.trim(), '', 'the disable flag must silence the check entirely');
  } finally {
    fixture.cleanup();
  }
});

test('SPAWN force-push exemption: an offending commit is ALLOWED when the push is a force-push', () => {
  const fixture = makeFixtureRepo();
  try {
    fixture.commitFunctionsChange('add a new cloud function, no deploy line');
    const r = runHookOnFixture(fixture, 'git push -f origin main');
    assert.equal(r.status, 0);
    assert.equal(r.stdout.trim(), '', 'force-push is exempted regardless of what the pending commits touch');
  } finally {
    fixture.cleanup();
  }
});

test('SPAWN non-push command: an unrelated Bash command is never intercepted', () => {
  const fixture = makeFixtureRepo();
  try {
    fixture.commitFunctionsChange('add a new cloud function, no deploy line');
    const r = runHookOnFixture(fixture, 'git status');
    assert.equal(r.status, 0);
    assert.equal(r.stdout.trim(), '');
  } finally {
    fixture.cleanup();
  }
});

test('SPAWN destination resolution: an omitted remote/branch falls back to the tracked upstream and still denies', () => {
  const fixture = makeFixtureRepo();
  try {
    fixture.commitFunctionsChange('add a new cloud function, no deploy line');
    // "git push" alone, relying on the tracked upstream (origin/main) set up
    // by makeFixtureRepo's initial `push -u`.
    const r = runHookOnFixture(fixture, 'git push');
    assert.equal(r.status, 0);
    const reason = parseDenyReason(r.stdout);
    assert.ok(reason, 'the tracked-upstream fallback must still find the pending offending commit');
    assert.match(reason, /origin\/main/);
  } finally {
    fixture.cleanup();
  }
});
