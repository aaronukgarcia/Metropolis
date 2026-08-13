/**
 * claude-author-identity.test.js — tests for the shared sanctioned-identity
 * module (FEAT-045, AC-4/AC-5).
 *
 * All end-to-end fixtures run against THROWAWAY repos under the OS temp dir
 * (never this repo, never a repo with a tracked remote — same rule as
 * claude-author-guard.test.js) and are always removed in a `finally`.
 *
 * Run: node --test claude-author-identity.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawnSync } = require('child_process');

const identity = require('./claude-author-identity.js');
const guard = require('./claude-author-guard.js');
const hook = require('./githooks/commit-msg');
const install = require('./claude-committhook-install.js');

const SANCTIONED_NAME = 'Sanctioned Contributor';
const SANCTIONED_EMAIL = 'sanctioned@example.invalid';
const SECONDARY_EMAIL = 'secondary-contributor@example.invalid';

function withTempRepo(fn) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'author-identity-fixture-'));
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

/** BUG-190: githooks/commit-msg requires claude-codename-content-scan.js
 * (and its own dependencies, claude-codename-patterns.js and
 * claude-codename-diff.js) via the same two-candidate resolution shape as
 * claude-author-identity.js (see resolveCodenameScanModulePath()'s header
 * comment in githooks/commit-msg). Every fixture in this file that installs
 * the real hook must therefore copy all four sibling modules into the
 * throwaway repo root, not just claude-author-identity.js — mirroring
 * claude-committhook.test.js's initRepo(), which already does this
 * correctly. Centralised here so every call site stays in sync with the
 * hook's actual require graph. */
function copyHookSiblingModules(dir) {
  for (const name of [
    'claude-author-identity.js',
    'claude-codename-content-scan.js',
    'claude-codename-patterns.js',
    'claude-codename-diff.js',
  ]) {
    fs.copyFileSync(path.join(__dirname, name), path.join(dir, name));
  }
}

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

/** Runs `fn` with cwd chdir'd into `dir` (identity's git() calls use the
 * process cwd, matching how the real guard/hook invoke it), always
 * restoring cwd afterward even on throw. */
function withCwd(dir, fn) {
  const prev = process.cwd();
  process.chdir(dir);
  try {
    return fn();
  } finally {
    process.chdir(prev);
  }
}

// ---------------------------------------------------------------------------
// AC-4: one shared module, required identically by both consumers — proven
// structurally (same function reference, not a coincidentally-matching
// copy) and behaviorally (a runtime threshold change is observed the same
// way through both).
// ---------------------------------------------------------------------------

test('AC-4: claude-author-guard.js delegates to the shared module (same function references, not reimplementations)', () => {
  assert.equal(guard.configuredEmail, identity.configuredEmail);
  assert.equal(guard.trunkBranch, identity.trunkBranch);
  assert.equal(guard.historyEmails, identity.historyEmails);
  assert.equal(guard.extraIdentities, identity.extraIdentities);
  assert.equal(guard.deriveSanctioned, identity.deriveSanctioned);
  assert.equal(guard.THRESHOLDS, identity.THRESHOLDS);
});

test('AC-4: githooks/commit-msg delegates to the shared module (same function reference)', () => {
  assert.equal(hook.deriveSanctioned, identity.deriveSanctioned);
});

test('AC-4: mutating THRESHOLDS.HISTORY_THRESHOLD is observed identically by both consumers (real repo, real git log scan)', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 1); // SANCTIONED_EMAIL: 1 commit as author+committer -> 2 history hits
    // Add a SECOND identity that appears exactly twice in history (author +
    // committer of one commit) via GIT_AUTHOR_*/GIT_COMMITTER_* env vars for
    // a single commit — below the DEFAULT threshold (3), above a lowered
    // threshold (2).
    fs.writeFileSync(path.join(dir, 'extra.txt'), 'x', 'utf8');
    git(dir, ['add', '-A']);
    const env = {
      ...process.env,
      GIT_AUTHOR_NAME: 'Secondary',
      GIT_AUTHOR_EMAIL: SECONDARY_EMAIL,
      GIT_COMMITTER_NAME: 'Secondary',
      GIT_COMMITTER_EMAIL: SECONDARY_EMAIL,
    };
    const r = spawnSync('git', ['commit', '-m', 'secondary commit'], { cwd: dir, env, encoding: 'utf8' });
    assert.equal(r.status, 0, r.stderr);

    withCwd(dir, () => {
      // At the default threshold (3), SECONDARY_EMAIL (2 hits) is NOT
      // picked up by history via EITHER entry point.
      assert.equal(identity.THRESHOLDS.HISTORY_THRESHOLD, 3, 'precondition: default threshold');
      assert.ok(!identity.historyEmails().has(SECONDARY_EMAIL));
      assert.ok(!guard.historyEmails().has(SECONDARY_EMAIL));

      const original = identity.THRESHOLDS.HISTORY_THRESHOLD;
      try {
        identity.THRESHOLDS.HISTORY_THRESHOLD = 2;
        // Observed via claude-author-identity.js directly...
        assert.ok(identity.historyEmails().has(SECONDARY_EMAIL), 'identity module did not observe the lowered threshold');
        // ...and via claude-author-guard.js's re-exported entry point...
        assert.ok(guard.historyEmails().has(SECONDARY_EMAIL), 'guard did not observe the lowered threshold');
        // ...and via the shared module as required by githooks/commit-msg.
        assert.ok(hook.deriveSanctioned().has(SECONDARY_EMAIL), 'hook did not observe the lowered threshold via deriveSanctioned()');
      } finally {
        identity.THRESHOLDS.HISTORY_THRESHOLD = original;
      }
    });
  });
});

// ---------------------------------------------------------------------------
// AC-4 "lazy implementation" trap: no consumer may fall back to an embedded
// list when the shared module is broken.
// ---------------------------------------------------------------------------

test('AC-4: a broken shared module (deriveSanctioned throwing) is NOT swallowed into a fallback list by claude-author-guard.js — it fails open (allow, no output), never a silent embedded-list allow', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const GUARD_PATH = path.join(__dirname, 'claude-author-guard.js');
    const cmd = 'git commit --allow-empty -m "x"';
    const payload = JSON.stringify({ tool: 'Bash', tool_input: { command: cmd } });
    const r = spawnSync(process.execPath, [GUARD_PATH], {
      cwd: dir,
      input: payload,
      encoding: 'utf8',
      env: { ...process.env, CLAUDE_AUTHOR_IDENTITY_FORCE_ERROR: '1' },
    });
    assert.equal(r.status, 0, 'demoted guard must still exit 0 even when the shared module throws');
    assert.equal((r.stdout || '').trim(), '', 'expected silent allow (no hookSpecificOutput) on internal error, not a fallback-list-derived decision');
  });
});

test('AC-4: a broken shared module (deriveSanctioned throwing) makes githooks/commit-msg fail CLOSED — the opposite of the guard, never a fallback list', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    const HOOK_PATH = path.join(__dirname, 'githooks', 'commit-msg');
    const r = spawnSync(process.execPath, [HOOK_PATH, path.join(dir, 'msgfile')], {
      cwd: dir,
      encoding: 'utf8',
      env: { ...process.env, CLAUDE_AUTHOR_IDENTITY_FORCE_ERROR: '1' },
    });
    assert.notEqual(r.status, 0, 'expected the commit-msg hook to fail closed (non-zero exit) when the shared module throws');
  });
});

// ---------------------------------------------------------------------------
// AC-5: the "history alone would sanction the wrong address" claim is
// present in the module header (grep floor; prose completeness is a human
// review, per the AC's own words).
// ---------------------------------------------------------------------------

test('AC-5: the module header states the specific BUG-036 claim (grep floor)', () => {
  const src = fs.readFileSync(path.join(__dirname, 'claude-author-identity.js'), 'utf8');
  assert.match(src, /history alone would (have )?sanction(ed)? the wrong address/i);
  assert.match(src, /BUG-036/);
});

// ---------------------------------------------------------------------------
// Unit coverage for the derivation itself (moved/adapted from
// claude-author-guard.test.js's pre-demotion suite — same behavior, now
// exercised directly against the shared module).
// ---------------------------------------------------------------------------

test('deriveSanctioned includes the configured git identity', () => {
  withTempRepo((dir) => {
    git(dir, ['init', '-b', 'main']);
    git(dir, ['config', 'user.name', SANCTIONED_NAME]);
    git(dir, ['config', 'user.email', SANCTIONED_EMAIL]);
    withCwd(dir, () => {
      assert.ok(identity.deriveSanctioned().has(SANCTIONED_EMAIL));
    });
  });
});

test('deriveSanctioned never crashes on a repo with no commits yet (unborn HEAD)', () => {
  withTempRepo((dir) => {
    git(dir, ['init', '-b', 'main']);
    git(dir, ['config', 'user.name', SANCTIONED_NAME]);
    git(dir, ['config', 'user.email', SANCTIONED_EMAIL]);
    withCwd(dir, () => {
      assert.doesNotThrow(() => identity.deriveSanctioned());
    });
  });
});

test('CLAUDE_AUTHOR_GUARD_EXTRA_IDENTITIES is picked up by extraIdentities()', () => {
  const prev = process.env.CLAUDE_AUTHOR_GUARD_EXTRA_IDENTITIES;
  process.env.CLAUDE_AUTHOR_GUARD_EXTRA_IDENTITIES = 'Extra Person <extra@example.invalid>';
  try {
    assert.ok(identity.extraIdentities().has('extra@example.invalid'));
  } finally {
    if (prev === undefined) delete process.env.CLAUDE_AUTHOR_GUARD_EXTRA_IDENTITIES;
    else process.env.CLAUDE_AUTHOR_GUARD_EXTRA_IDENTITIES = prev;
  }
});

// ---------------------------------------------------------------------------
// BUG-052 regression (moved from claude-author-guard.test.js — the bounded
// history scan now lives in THIS file's source, not the guard's).
// ---------------------------------------------------------------------------

test('BUG-052: historyEmails() bounds its git log scan (source carries a --max-count cap, not unbounded)', () => {
  const src = fs.readFileSync(path.join(__dirname, 'claude-author-identity.js'), 'utf8');
  assert.match(src, /--max-count=\$\{THRESHOLDS\.HISTORY_SCAN_LIMIT\}/);
  assert.equal(typeof identity.THRESHOLDS.HISTORY_SCAN_LIMIT, 'number');
  assert.ok(identity.THRESHOLDS.HISTORY_SCAN_LIMIT > 0 && Number.isFinite(identity.THRESHOLDS.HISTORY_SCAN_LIMIT));
});

// ---------------------------------------------------------------------------
// SEC-052: configuredEmail() must not resolve THROUGH a `-c`/env-var config
// override belonging to the very `git commit`/`git merge` invocation whose
// child process this code runs inside of. Source: source-carries-the-fix
// checks (fast, no subprocess) PLUS live subprocess reproductions with the
// actual poisoned environment shape git hands to a hook's own child process
// (GIT_CONFIG_PARAMETERS for `-c`, GIT_CONFIG_COUNT/KEY_n/VALUE_n for the
// explicit env-var form), PLUS a full real-hook end-to-end reproduction
// against `git -c user.email=... commit` mirroring the original live
// finding.
// ---------------------------------------------------------------------------

test('SEC-052: configuredEmail() reads local via --local, never the unscoped form (source check)', () => {
  const src = fs.readFileSync(path.join(__dirname, 'claude-author-identity.js'), 'utf8');
  // The fixed function body must never call `git(['config', 'user.email'])`
  // (the unscoped form that resolves through a command-scoped override).
  assert.doesNotMatch(src, /git\(\['config',\s*'user\.email'\]\)/);
  assert.match(src, /'--local'/);
});

test('SEC-052 ROUND 2: global-scope fallback no longer calls `git config --global` at all (source check) — it reads globalConfigPaths() via --file, resolved from os.userInfo().homedir', () => {
  const src = fs.readFileSync(path.join(__dirname, 'claude-author-identity.js'), 'utf8');
  // `--global` resolution is itself redirectable via GIT_CONFIG_GLOBAL/HOME
  // (this round's live finding) — the fixed source must not invoke it for
  // the fallback at all.
  assert.doesNotMatch(src, /git\(\['config',\s*'--global'/);
  assert.match(src, /os\.userInfo\(\)\.homedir/, 'must resolve the global config location via os.userInfo().homedir, not os.homedir() or an env var');
  // Note: os.homedir() may legitimately appear in explanatory PROSE (it is
  // discussed and rejected in the header comment) — the actual constraint
  // is that it must never be CALLED in the code.
  assert.doesNotMatch(src, /=\s*os\.homedir\(\)/, 'os.homedir() reads USERPROFILE/HOME directly and is just as poisonable as git --global — must use os.userInfo().homedir instead');
  assert.match(src, /'--file'/, 'global candidates must be read via git config --file <path>, an explicit target immune to -c/env overrides');
});

test('SEC-052(a): `-c user.email=X` (GIT_CONFIG_PARAMETERS form) no longer poisons configuredEmail()', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 1);
    withCwd(dir, () => {
      // Reproduce exactly what git hands to a commit-msg hook's own child
      // process when the invoking command was `git -c user.email=X ...`:
      // GIT_CONFIG_PARAMETERS carrying the override, inherited via env,
      // NOT passed as a CLI flag to configuredEmail()'s own `git config`
      // call (there is no CLI flag here to poison — this is the actual
      // bypass shape).
      const prev = process.env.GIT_CONFIG_PARAMETERS;
      process.env.GIT_CONFIG_PARAMETERS = "'user.email=fabricated@example.invalid'";
      try {
        const e = identity.configuredEmail();
        assert.equal(e, SANCTIONED_EMAIL, 'configuredEmail() must return the real persisted identity, not the -c override');
        assert.notEqual(e, 'fabricated@example.invalid');
      } finally {
        if (prev === undefined) delete process.env.GIT_CONFIG_PARAMETERS;
        else process.env.GIT_CONFIG_PARAMETERS = prev;
      }
    });
  });
});

test('SEC-052(b): GIT_CONFIG_COUNT/GIT_CONFIG_KEY_0/GIT_CONFIG_VALUE_0 form no longer poisons configuredEmail()', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 1);
    withCwd(dir, () => {
      const savedKeys = ['GIT_CONFIG_COUNT', 'GIT_CONFIG_KEY_0', 'GIT_CONFIG_VALUE_0'];
      const prev = {};
      for (const k of savedKeys) prev[k] = process.env[k];
      process.env.GIT_CONFIG_COUNT = '1';
      process.env.GIT_CONFIG_KEY_0 = 'user.email';
      process.env.GIT_CONFIG_VALUE_0 = 'fabricated-env@example.invalid';
      try {
        const e = identity.configuredEmail();
        assert.equal(e, SANCTIONED_EMAIL, 'configuredEmail() must return the real persisted identity, not the env-var override');
        assert.notEqual(e, 'fabricated-env@example.invalid');
      } finally {
        for (const k of savedKeys) {
          if (prev[k] === undefined) delete process.env[k];
          else process.env[k] = prev[k];
        }
      }
    });
  });
});

test('SEC-052(c): legitimate --author=/GIT_AUTHOR_IDENT identity detection is unchanged — a real fabricated --author is still rejected by the installed hook', () => {
  withTempRepo((dir) => {
    // Full real-hook end-to-end reproduction, mirroring how the original
    // bug was found: install the real tracked hook, then attempt a commit
    // whose AUTHOR identity is fabricated via `--author=` (a path this fix
    // deliberately does not touch — the hook resolves author/committer via
    // `git var GIT_AUTHOR_IDENT`/`GIT_COMMITTER_IDENT`, not configuredEmail()).
    git(dir, ['init', '-b', 'main']);
    git(dir, ['config', 'user.name', SANCTIONED_NAME]);
    git(dir, ['config', 'user.email', SANCTIONED_EMAIL]);
    copyHookSiblingModules(dir);
    install.install(dir);
    fs.writeFileSync(path.join(dir, 'a.txt'), '1', 'utf8');
    git(dir, ['add', '-A']);

    const r = spawnSync(
      'git',
      ['commit', '--author=Fabricated Author <fabricated@example.invalid>', '-m', 'x'],
      { cwd: dir, encoding: 'utf8' }
    );
    assert.notEqual(r.status, 0, 'a fabricated --author identity must still be rejected by the real installed hook');
  });
});

test('SEC-052: a real `git -c user.email=X commit` bypass attempt is rejected end-to-end by the fixed, installed hook', () => {
  withTempRepo((dir) => {
    git(dir, ['init', '-b', 'main']);
    git(dir, ['config', 'user.name', SANCTIONED_NAME]);
    git(dir, ['config', 'user.email', SANCTIONED_EMAIL]);
    copyHookSiblingModules(dir);
    install.install(dir);
    fs.writeFileSync(path.join(dir, 'a.txt'), '1', 'utf8');
    git(dir, ['add', '-A']);

    // The exact original finding: `-c user.email=X` poisons BOTH the
    // identity under test AND (pre-fix) the sanctioned set it is checked
    // against, in one invocation.
    const r = spawnSync(
      'git',
      ['-c', 'user.email=fabricated-bypass@example.invalid', 'commit', '-m', 'x'],
      { cwd: dir, encoding: 'utf8' }
    );
    assert.notEqual(r.status, 0, 'the `-c user.email=` bypass must be rejected now that configuredEmail() is scope-pinned');

    // Control: a legitimate commit with the real configured identity must
    // still succeed — the fix must not become a false-positive machine.
    fs.writeFileSync(path.join(dir, 'b.txt'), '2', 'utf8');
    git(dir, ['add', '-A']);
    const ok = spawnSync('git', ['commit', '-m', 'legit'], { cwd: dir, encoding: 'utf8' });
    assert.equal(ok.status, 0, `legitimate commit with the real configured identity must succeed: ${ok.stderr}`);
  });
});

test('SEC-052: a real `git -c user.email=X merge --no-ff` bypass attempt is rejected end-to-end by the fixed, installed hook (the other verb this hook exists to protect, per the original finding)', () => {
  withTempRepo((dir) => {
    git(dir, ['init', '-b', 'main']);
    git(dir, ['config', 'user.name', SANCTIONED_NAME]);
    git(dir, ['config', 'user.email', SANCTIONED_EMAIL]);
    copyHookSiblingModules(dir);
    install.install(dir);

    fs.writeFileSync(path.join(dir, 'a.txt'), '1', 'utf8');
    git(dir, ['add', '-A']);
    git(dir, ['commit', '-m', 'base']);
    git(dir, ['checkout', '-b', 'topic']);
    fs.writeFileSync(path.join(dir, 'b.txt'), '2', 'utf8');
    git(dir, ['add', '-A']);
    git(dir, ['commit', '-m', 'topic commit']);
    git(dir, ['checkout', 'main']);

    // The exact original finding's second verb: `-c user.email=X ... merge
    // --no-ff` poisons BOTH the identity under test AND (pre-fix) the
    // sanctioned set it is checked against, in the same invocation.
    const bad = spawnSync(
      'git',
      ['-c', 'user.email=merge-bypass@example.invalid', '-c', 'user.name=Merge Bypass',
       'merge', '--no-ff', 'topic', '-m', 'merge with fabricated identity'],
      { cwd: dir, encoding: 'utf8' }
    );
    assert.notEqual(bad.status, 0, 'the `-c user.email=` merge bypass must be rejected now that configuredEmail() is scope-pinned');
    // No merge commit object should have landed with the fabricated identity.
    git(dir, ['merge', '--abort']);

    // Control: a legitimate no-ff merge with the real configured identity
    // must still succeed — the fix must not become a false-positive machine
    // on the verb it exists to protect.
    const ok = spawnSync('git', ['merge', '--no-ff', 'topic', '-m', 'legit merge'], { cwd: dir, encoding: 'utf8' });
    assert.equal(ok.status, 0, `legitimate merge --no-ff with the real configured identity must succeed: ${ok.stderr}`);
  });
});

// ---------------------------------------------------------------------------
// SEC-052 ROUND 2: the global-scope fallback (`--global`) is itself
// redirectable via GIT_CONFIG_GLOBAL (git 2.32+) or HOME/USERPROFIL, neither
// of which is filtered before this hook's own child process reads it.
// Reproduces Magpie's exact two live attacks, PLUS the two legitimate
// controls Magpie's fix direction explicitly required not to break:
// local-only (round 1) and global-only/no-local (the fallback's whole
// reason to exist).
//
// These tests use a fresh repo with NO local user.name/user.email set at
// all (so configuredEmail() is forced down the global fallback path), and
// therefore rely on THIS MACHINE'S real, unpoisoned global git identity for
// the "must still succeed" controls — never mutated, only read.
// ---------------------------------------------------------------------------

function realMachineGlobalEmail() {
  // The baseline: what git's OWN --global resolution returns in a clean,
  // unpoisoned environment. Used only as an oracle for the "legitimate
  // global-only" controls below — never written to.
  const r = spawnSync('git', ['config', '--global', 'user.email'], { encoding: 'utf8' });
  return r.status === 0 ? r.stdout.trim() : null;
}

function initRepoNoLocalIdentity(dir) {
  git(dir, ['init', '-b', 'main']);
  // Deliberately do NOT set local user.name/user.email — forces
  // configuredEmail() (and git's own identity resolution) down to the
  // global scope, which is exactly the case this round's bug lives in.
}

test('SEC-052 ROUND 2 precondition: this machine has a real global user.email configured (required for the controls below to be meaningful)', () => {
  const email = realMachineGlobalEmail();
  assert.ok(email, 'expected a real global git user.email on this dev machine — controls below cannot prove anything without one');
});

test('SEC-052 ROUND 2(a): GIT_CONFIG_GLOBAL=<evil>.gitconfig no longer poisons configuredEmail() when no local identity is set', () => {
  withTempRepo((repoDir) => {
    initRepoNoLocalIdentity(repoDir);
    withTempRepo((evilDir) => {
      const evilFile = path.join(evilDir, 'evil.gitconfig');
      fs.writeFileSync(
        evilFile,
        '[user]\n\temail = fabricated-global-config@example.invalid\n\tname = Fabricated Global\n',
        'utf8'
      );
      withCwd(repoDir, () => {
        const prev = process.env.GIT_CONFIG_GLOBAL;
        process.env.GIT_CONFIG_GLOBAL = evilFile;
        try {
          const e = identity.configuredEmail();
          assert.notEqual(e, 'fabricated-global-config@example.invalid', 'GIT_CONFIG_GLOBAL must not be able to redirect the global-scope read');
        } finally {
          if (prev === undefined) delete process.env.GIT_CONFIG_GLOBAL;
          else process.env.GIT_CONFIG_GLOBAL = prev;
        }
      });
    });
  });
});

test('SEC-052 ROUND 2(b): HOME=<evil-home> (with a real .gitconfig planted there) no longer poisons configuredEmail() when no local identity is set', () => {
  withTempRepo((repoDir) => {
    initRepoNoLocalIdentity(repoDir);
    withTempRepo((evilHomeDir) => {
      fs.writeFileSync(
        path.join(evilHomeDir, '.gitconfig'),
        '[user]\n\temail = fabricated-home-redirect@example.invalid\n\tname = Fabricated Home\n',
        'utf8'
      );
      withCwd(repoDir, () => {
        const prevHome = process.env.HOME;
        const prevUserProfile = process.env.USERPROFILE;
        process.env.HOME = evilHomeDir;
        process.env.USERPROFILE = evilHomeDir; // Windows equivalent, same attack shape
        try {
          const e = identity.configuredEmail();
          assert.notEqual(e, 'fabricated-home-redirect@example.invalid', 'HOME/USERPROFILE must not be able to redirect the global-scope read');
        } finally {
          if (prevHome === undefined) delete process.env.HOME; else process.env.HOME = prevHome;
          if (prevUserProfile === undefined) delete process.env.USERPROFILE; else process.env.USERPROFILE = prevUserProfile;
        }
      });
    });
  });
});

test('SEC-052 ROUND 2(c): a real installed-hook `GIT_CONFIG_GLOBAL=<evil> git commit` bypass attempt is rejected end-to-end (no local identity set)', () => {
  withTempRepo((repoDir) => {
    initRepoNoLocalIdentity(repoDir);
    copyHookSiblingModules(repoDir);
    install.install(repoDir);
    fs.writeFileSync(path.join(repoDir, 'a.txt'), '1', 'utf8');
    git(repoDir, ['add', '-A']);

    withTempRepo((evilDir) => {
      const evilFile = path.join(evilDir, 'evil.gitconfig');
      fs.writeFileSync(
        evilFile,
        '[user]\n\temail = fabricated-global-config@example.invalid\n\tname = Fabricated Global\n',
        'utf8'
      );
      const r = spawnSync('git', ['commit', '-m', 'x'], {
        cwd: repoDir,
        encoding: 'utf8',
        env: { ...process.env, GIT_CONFIG_GLOBAL: evilFile },
      });
      assert.notEqual(r.status, 0, 'the GIT_CONFIG_GLOBAL redirect bypass must be rejected by the fixed, installed hook');
    });
  });
});

test('SEC-052 ROUND 2(d): a real installed-hook `HOME=<evil-home> git commit` bypass attempt is rejected end-to-end (no local identity set)', () => {
  withTempRepo((repoDir) => {
    initRepoNoLocalIdentity(repoDir);
    copyHookSiblingModules(repoDir);
    install.install(repoDir);
    fs.writeFileSync(path.join(repoDir, 'a.txt'), '1', 'utf8');
    git(repoDir, ['add', '-A']);

    withTempRepo((evilHomeDir) => {
      fs.writeFileSync(
        path.join(evilHomeDir, '.gitconfig'),
        '[user]\n\temail = fabricated-home-redirect@example.invalid\n\tname = Fabricated Home\n',
        'utf8'
      );
      const r = spawnSync('git', ['commit', '-m', 'x'], {
        cwd: repoDir,
        encoding: 'utf8',
        env: { ...process.env, HOME: evilHomeDir, USERPROFILE: evilHomeDir },
      });
      assert.notEqual(r.status, 0, 'the HOME/USERPROFILE redirect bypass must be rejected by the fixed, installed hook');
    });
  });
});

test('SEC-052 ROUND 2 control(a): legitimate LOCAL-only identity (round 1 case) still succeeds end-to-end', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 1); // sets LOCAL user.name/email via initRepoWithHistory
    copyHookSiblingModules(dir);
    install.install(dir);
    fs.writeFileSync(path.join(dir, 'b.txt'), '2', 'utf8');
    git(dir, ['add', '-A']);
    const r = spawnSync('git', ['commit', '-m', 'legit-local'], { cwd: dir, encoding: 'utf8' });
    assert.equal(r.status, 0, `legitimate LOCAL-only commit must still succeed: ${r.stderr}`);
  });
});

test('SEC-052 ROUND 2 control(b): legitimate GLOBAL-only identity, no local set, still succeeds end-to-end — the exact case the fallback exists for', () => {
  const expected = realMachineGlobalEmail();
  withTempRepo((dir) => {
    initRepoNoLocalIdentity(dir);
    copyHookSiblingModules(dir);
    install.install(dir);

    withCwd(dir, () => {
      assert.equal(identity.configuredEmail(), expected, 'configuredEmail() must resolve the real global email when no local value is set');
    });

    fs.writeFileSync(path.join(dir, 'c.txt'), '3', 'utf8');
    git(dir, ['add', '-A']);
    // Unpoisoned environment: git's own identity resolution AND
    // configuredEmail()'s fallback both land on this machine's real global
    // identity, so the commit must succeed.
    const r = spawnSync('git', ['commit', '-m', 'legit-global-only'], { cwd: dir, encoding: 'utf8' });
    assert.equal(r.status, 0, `legitimate GLOBAL-only commit (no local config) must still succeed: ${r.stderr}`);
  });
});

// ---------------------------------------------------------------------------
// BUG-136: GIT_DIR (and GIT_WORK_TREE/GIT_COMMON_DIR) redirect git's own
// repo-discovery to an attacker-fabricated repo, defeating --local the same
// way SEC-052 round 2 found GIT_CONFIG_GLOBAL/HOME defeating --global.
// Reproduces the live attack, plus a control proving legitimate local-scope
// resolution (round 1's whole reason to exist) still works unpoisoned.
// ---------------------------------------------------------------------------

test('BUG-136: GIT_DIR=<evil-repo>/.git no longer redirects configuredEmail()\'s local-scope read to a fabricated repo', () => {
  withTempRepo((repoDir) => {
    initRepoWithHistory(repoDir, 1); // real LOCAL identity: SANCTIONED_EMAIL
    withTempRepo((evilDir) => {
      git(evilDir, ['init', '-b', 'main']);
      git(evilDir, ['config', 'user.name', 'Fabricated Local']);
      git(evilDir, ['config', 'user.email', 'fabricated-local-config@example.invalid']);
      const evilGitDir = path.join(evilDir, '.git');
      withCwd(repoDir, () => {
        const prev = process.env.GIT_DIR;
        process.env.GIT_DIR = evilGitDir;
        try {
          const e = identity.configuredEmail();
          assert.notEqual(e, 'fabricated-local-config@example.invalid', 'GIT_DIR must not be able to redirect the local-scope read to a different repo');
          assert.equal(e, SANCTIONED_EMAIL, 'with GIT_DIR stripped, configuredEmail() must fall back to normal discovery and resolve the real repo\'s own local identity');
        } finally {
          if (prev === undefined) delete process.env.GIT_DIR;
          else process.env.GIT_DIR = prev;
        }
      });
    });
  });
});

test('BUG-136 control: legitimate local-scope resolution (round 1 case) still works with an unpoisoned environment after the GIT_DIR fix', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 1);
    withCwd(dir, () => {
      assert.equal(identity.configuredEmail(), SANCTIONED_EMAIL, 'stripping GIT_DIR/GIT_WORK_TREE/GIT_COMMON_DIR must not break normal, unpoisoned local-scope resolution');
    });
  });
});
