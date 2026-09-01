// Module key: tool.destructiveguard (attack tests — BUG-340 independent round r2)
// Spec ref: GR#23; BUG-340 r1 finding F3 (`--no-renames`).
//
// COVERAGE GAP FOUND IN r2: the F3 fix added `--no-renames` to THREE
// `git diff --cached` / `git diff` invocations inside claude-destructive-guard.js
// (getStagedFilesFromDir, main()'s staged read, main()'s --all working-tree
// read), but NOTHING in the suite could fail if they were removed again.
// Measured by mutation in r2: deleting `--no-renames` from every one of those
// three sites left `claude-destructive-guard.test.js` +
// `githooks/verdict-guard-attack.test.js` fully GREEN (the only rename test in
// the estate covers githooks/verdict-guard.js's own copy, not this file's).
// "A test that cannot fail is a finding" — this file closes that.
//
// The bypass being guarded: `git diff --cached --name-only` COLLAPSES a
// rename to the destination path only, so renaming a code-bearing file in an
// enforced directory to a docs/test-exempt name makes the whole staged set
// read as exempt, and the commit needs no Destructive verdict at all.

'use strict';

const test = require('node:test');
const assert = require('node:assert');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { execFileSync } = require('child_process');

const guard = require('./claude-destructive-guard.js');

function git(dir, args) {
  return execFileSync('git', args, { cwd: dir, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] });
}

/** Throwaway repo with a committed `internal/foo.go` renamed to
 * `docs/foo.md` and staged. Git scores this a 100%-similarity rename, which
 * is exactly the case `--name-only` collapses. */
function withRenameRepo(fn) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'dg-rename-r2-'));
  try {
    git(dir, ['init', '-q', '.']);
    git(dir, ['config', 'user.name', 'Test']);
    git(dir, ['config', 'user.email', 'test@example.invalid']);
    fs.mkdirSync(path.join(dir, 'internal'), { recursive: true });
    // Long enough that git's rename detection scores it as a pure rename.
    const body = 'package foo\n\n' + Array.from({ length: 40 }, (_, i) => `// line ${i}`).join('\n') + '\n';
    fs.writeFileSync(path.join(dir, 'internal', 'foo.go'), body);
    git(dir, ['add', 'internal/foo.go']);
    git(dir, ['-c', 'core.hooksPath=/dev/null', 'commit', '-q', '-m', 'seed']);
    fs.mkdirSync(path.join(dir, 'docs'), { recursive: true });
    git(dir, ['mv', 'internal/foo.go', 'docs/foo.md']);
    return fn(dir);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

test('BUG-340 r2/F3: getStagedFilesFromDir() reports BOTH sides of a rename (PROVE-CAN-FAIL: drop --no-renames and this goes red)', () => {
  withRenameRepo((dir) => {
    // Sanity: the DEFAULT git spelling really does collapse the rename, so
    // this test is asserting a behaviour that a real bug would break — not
    // one git gives us for free.
    const collapsed = execFileSync('git', ['diff', '--cached', '--name-only'], { cwd: dir, encoding: 'utf8' })
      .split('\n').map((s) => s.trim()).filter(Boolean);
    assert.deepEqual(collapsed, ['docs/foo.md'],
      'precondition: plain --name-only must collapse the rename, otherwise this test proves nothing');

    const staged = guard.getStagedFilesFromDir(dir);
    assert.ok(staged.includes('internal/foo.go'),
      `the DELETED (code-bearing) side of a rename must be visible to the guard; got ${JSON.stringify(staged)}. ` +
        'claude-destructive-guard.js must pass --no-renames to every git diff it classifies from.');
    assert.ok(staged.includes('docs/foo.md'), 'the added side must also be visible');
  });
});

test('BUG-340 r2/F3: a rename of a code-bearing file to a .md name is NOT an exempt staged set', () => {
  withRenameRepo((dir) => {
    const staged = guard.getStagedFilesFromDir(dir);
    assert.equal(guard.isExemptFileSet(staged), false,
      'a rename whose destination is docs-only must not classify the commit as FEAT-077 exempt');
    assert.ok(staged.some(guard.isEnforcedDirPath),
      'the enforced-dir source path must still drive code-bearing classification');
  });
});

test('BUG-340 r2/F3: a rename of a root guard script to a .test.js name is NOT an exempt staged set', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'dg-rename2-r2-'));
  try {
    git(dir, ['init', '-q', '.']);
    git(dir, ['config', 'user.name', 'Test']);
    git(dir, ['config', 'user.email', 'test@example.invalid']);
    const body = '// guard\n' + Array.from({ length: 40 }, (_, i) => `// line ${i}`).join('\n') + '\n';
    fs.writeFileSync(path.join(dir, 'claude-some-guard.js'), body);
    git(dir, ['add', 'claude-some-guard.js']);
    git(dir, ['-c', 'core.hooksPath=/dev/null', 'commit', '-q', '-m', 'seed']);
    git(dir, ['mv', 'claude-some-guard.js', 'claude-some-guard.test.js']);

    const staged = guard.getStagedFilesFromDir(dir);
    assert.ok(staged.includes('claude-some-guard.js'),
      `the deleted guard-script side must be visible; got ${JSON.stringify(staged)}`);
    assert.equal(guard.isExemptFileSet(staged), false,
      'renaming a guard script to a .test.js name must not buy the FEAT-077 test-only exemption');
    assert.ok(staged.some(guard.isGuardOrHookPath),
      'the guard-script path must still drive code-bearing classification');
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});
