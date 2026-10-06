/**
 * claude-committhook-install-prepush.test.js — BUG-340/BUG-336 (deliverable
 * 3): tests for claude-committhook-install.js's generalisation to install
 * BOTH commit-msg and pre-push (installAll/verifyAll/summaryLines), added
 * alongside the pre-existing claude-committhook-install.test.js (which keeps
 * testing the single-hook `install(dir)`/`verify(dir)` calls unchanged).
 *
 * Run: node --test claude-committhook-install-prepush.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { execFileSync } = require('child_process');

const install = require('./claude-committhook-install.js');

function withTempRepo(fn) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'cth-pp-fix-'));
  try {
    return fn(dir);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

function git(cwd, args) {
  return execFileSync('git', args, { cwd, encoding: 'utf8' });
}

test('installAll(): installs BOTH commit-msg and pre-push, byte-for-byte matching their tracked sources', () => {
  withTempRepo((dir) => {
    const installed = install.installAll(dir);
    assert.ok(installed['commit-msg']);
    assert.ok(installed['pre-push']);

    for (const hookName of ['commit-msg', 'pre-push']) {
      const dest = install.installedPath(dir, hookName);
      assert.ok(fs.existsSync(dest), `expected ${hookName} to be installed`);
      const installedBytes = fs.readFileSync(dest);
      const canonicalBytes = fs.readFileSync(install.HOOKS[hookName].source);
      assert.ok(Buffer.compare(installedBytes, canonicalBytes) === 0, `${hookName} must match its tracked source byte-for-byte`);
    }
  });
});

test('verifyAll(): reports "absent" for both hooks when neither is installed', () => {
  withTempRepo((dir) => {
    const all = install.verifyAll(dir);
    assert.equal(all['commit-msg'].state, 'absent');
    assert.equal(all['pre-push'].state, 'absent');
  });
});

test('verifyAll(): reports "healthy" for both hooks immediately after installAll()', () => {
  withTempRepo((dir) => {
    install.installAll(dir);
    const all = install.verifyAll(dir);
    assert.equal(all['commit-msg'].state, 'healthy');
    assert.equal(all['pre-push'].state, 'healthy');
  });
});

test('verifyAll(): distinguishes per-hook state — pre-push stale while commit-msg stays healthy (PROVE-CAN-FAIL: never collapsed into one status)', () => {
  withTempRepo((dir) => {
    install.installAll(dir);
    fs.appendFileSync(install.installedPath(dir, 'pre-push'), '\n// tampered\n');
    const all = install.verifyAll(dir);
    assert.equal(all['commit-msg'].state, 'healthy', 'commit-msg must be unaffected by pre-push being tampered');
    assert.equal(all['pre-push'].state, 'stale', 'pre-push must independently report stale');
  });
});

test('summaryLines(): one line per tracked hook, each naming its own hook by label', () => {
  withTempRepo((dir) => {
    const lines = install.summaryLines(dir);
    assert.equal(lines.length, Object.keys(install.HOOKS).length);
    assert.ok(lines.some((l) => /COMMIT-MSG IDENTITY HOOK/.test(l)));
    assert.ok(lines.some((l) => /PRE-PUSH.*FLOOR GATE/.test(l)));
  });
});

test('summaryLine(dir, "pre-push"): ABSENT wording names pre-push specifically, not commit-msg\'s identity wording (PROVE-CAN-FAIL companion to the shared-wording bug this would have caused)', () => {
  withTempRepo((dir) => {
    const line = install.summaryLine(dir, 'pre-push');
    assert.match(line, /ABSENT/);
    assert.doesNotMatch(line, /identity-protected/i, 'pre-push wording must not reuse commit-msg\'s identity-specific phrase');
  });
});

test('CLI `install` installs both hooks and CLI `verify` exits 0 only when both are healthy', () => {
  const { spawnSync } = require('child_process');
  withTempRepo((dir) => {
    const installScript = path.join(__dirname, 'claude-committhook-install.js');
    const r1 = spawnSync(process.execPath, [installScript, 'install', dir], { encoding: 'utf8' });
    assert.equal(r1.status, 0, r1.stderr);
    assert.match(r1.stdout, /commit-msg hook to/);
    assert.match(r1.stdout, /pre-push hook to/);

    const r2 = spawnSync(process.execPath, [installScript, 'verify', dir], { encoding: 'utf8' });
    assert.equal(r2.status, 0, r2.stderr);

    // Tamper with pre-push only — CLI verify must now exit non-zero.
    fs.appendFileSync(install.installedPath(dir, 'pre-push'), '\n// tampered\n');
    const r3 = spawnSync(process.execPath, [installScript, 'verify', dir], { encoding: 'utf8' });
    assert.notEqual(r3.status, 0, 'expected non-zero exit when ANY tracked hook is not healthy');
  });
});

// ---------------------------------------------------------------------------
// BUG-340 r1 F7(a): never silently clobber an existing custom hook
// ---------------------------------------------------------------------------

test('F7(a): install() BACKS UP an existing hook file whose content differs, before overwriting it', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(install.hooksDir(dir), { recursive: true });
    const dest = install.installedPath(dir, 'pre-push');
    fs.writeFileSync(dest, '#!/bin/sh\necho a hand-written custom pre-push script\n');

    install.install(dir, 'pre-push');

    const backups = fs.readdirSync(install.hooksDir(dir)).filter((f) => f.startsWith('pre-push.bak-'));
    assert.equal(backups.length, 1, `expected exactly one pre-push.bak-* file, got: ${JSON.stringify(backups)}`);
    const backupContent = fs.readFileSync(path.join(install.hooksDir(dir), backups[0]), 'utf8');
    assert.match(backupContent, /hand-written custom pre-push script/, 'the backup must preserve the ORIGINAL custom content');

    // The live hook must now be the tracked canonical source, not the custom one.
    const nowInstalled = fs.readFileSync(dest);
    const canonical = fs.readFileSync(install.HOOKS['pre-push'].source);
    assert.equal(Buffer.compare(nowInstalled, canonical), 0, 'the live hook must be overwritten with the tracked source');
  });
});

test('F7(a) PROVE-CAN-FAIL: re-installing over an ALREADY-canonical hook creates NO backup (idempotent re-run, nothing lost)', () => {
  withTempRepo((dir) => {
    install.install(dir, 'pre-push'); // first install: canonical, no prior file
    install.install(dir, 'pre-push'); // second install: identical content already present

    const backups = fs.readdirSync(install.hooksDir(dir)).filter((f) => f.startsWith('pre-push.bak-'));
    assert.equal(backups.length, 0, 'a byte-identical re-install must not spam a backup file');
  });
});

// ---------------------------------------------------------------------------
// BUG-340 r1 F7(b): worktree-aware hooksDir / verify (no false ABSENT)
// ---------------------------------------------------------------------------

test('F7(b): hooksDir()/install()/verify() resolve correctly from a git WORKTREE, not just the main checkout', () => {
  withTempRepo((mainDir) => {
    git(mainDir, ['init', '-q', '-b', 'main']);
    git(mainDir, ['config', 'user.name', 'Fixture']);
    git(mainDir, ['config', 'user.email', 'fixture@example.invalid']);
    fs.writeFileSync(path.join(mainDir, 'README.md'), '# fixture\n');
    git(mainDir, ['add', 'README.md']);
    git(mainDir, ['commit', '-q', '-m', 'base']);

    const worktreeDir = fs.mkdtempSync(path.join(os.tmpdir(), 'committhook-worktree-'));
    fs.rmSync(worktreeDir, { recursive: true, force: true }); // git worktree add requires the path NOT exist
    try {
      git(mainDir, ['worktree', 'add', '-q', '-b', 'wt-branch', worktreeDir]);

      // Before any install: verify from the WORKTREE must read absent, not
      // silently succeed against a hooks dir that could never exist
      // (BUG-340 r1 F7(b) — the original `<repoRoot>/.git/hooks` guess
      // resolves under a FILE in a worktree, so this used to print false
      // ABSENT even when the MAIN checkout's hooks were actually healthy —
      // it could never find them from the worktree's own path either way).
      install.install(mainDir, 'commit-msg');
      const beforeFromMain = install.verify(mainDir, 'commit-msg');
      assert.equal(beforeFromMain.state, 'healthy', 'installing from the main checkout must read healthy from the main checkout');

      // The KEY assertion: verify() run with repoRoot = the WORKTREE must
      // see the SAME hooks dir as the main checkout (git shares one hooks
      // dir across all worktrees of the same repo) — so a hook installed
      // from main reads as healthy from the worktree too, and installing
      // from the worktree lands in the SAME shared hooks dir.
      const fromWorktree = install.verify(worktreeDir, 'commit-msg');
      assert.equal(
        fromWorktree.state, 'healthy',
        'a hook installed from the main checkout must be visible (not falsely ABSENT) when verified from a worktree'
      );
      // Compared via realpath+lowercase, not a raw string equal — Windows
      // can hand back EITHER an 8.3 short-form path segment or the long
      // form for the SAME directory depending on which API produced it
      // (the same normalisation gap documented in claude-bow.js's own
      // normalizeRecorderCwd, BUG-340 r1 F1); the point of this assertion
      // is "the SAME real directory", not "the identical string".
      const norm = (p) => {
        try { return fs.realpathSync.native(p).toLowerCase(); } catch { return p.toLowerCase(); }
      };
      assert.equal(
        norm(install.hooksDir(worktreeDir)), norm(install.hooksDir(mainDir)),
        'the worktree and the main checkout must resolve to the SAME hooks directory'
      );
    } finally {
      try { git(mainDir, ['worktree', 'remove', '--force', worktreeDir]); } catch { /* best-effort cleanup */ }
      fs.rmSync(worktreeDir, { recursive: true, force: true });
    }
  });
});

test('F7(b) PROVE-CAN-FAIL: a repoRoot that is NOT a git repo at all still falls back to the plain <repoRoot>/.git/hooks guess (no crash, no silent misresolution)', () => {
  withTempRepo((dir) => {
    // No `git init` here at all — hooksDir() must not throw, and must fall
    // back to the pre-F7(b) legacy path shape.
    const resolved = install.hooksDir(dir);
    assert.equal(resolved, path.join(dir, '.git', 'hooks'));
  });
});
