/**
 * claude-scratch.test.js — regression tests for claude-scratch.js (FEAT-058).
 *
 * These are filesystem-output tests, not guard-input tests: each one builds
 * a real throwaway git repository under the OS temp directory (never inside
 * E:\git\Metropolis — the tool must never be exercised against the project's
 * own working tree from a test), puts it into a known tracked-modified /
 * untracked / gitignored state, runs the tool's exported functions against
 * it, and asserts exactly what landed on disk. The gitignored-file exclusion
 * is asserted as explicitly as the included-file inclusion, per the brief:
 * a version that copies everything (ignoring .gitignore) would still pass a
 * suite that only checked "included files are present".
 *
 * Run: node --test claude-scratch.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { execFileSync } = require('child_process');

const {
  ScratchError,
  getRepoRoot,
  getChangedPaths,
  formatTimestamp,
  pickDestDir,
  createDestDir,
  pathCrossesReparsePoint,
  copyPaths,
  runSnapshot,
} = require('./claude-scratch.js');

const GIT_ENV = {
  ...process.env,
  GIT_AUTHOR_NAME: 'FEAT-058 Test Fixture',
  GIT_AUTHOR_EMAIL: 'feat-058-fixture@example.invalid',
  GIT_COMMITTER_NAME: 'FEAT-058 Test Fixture',
  GIT_COMMITTER_EMAIL: 'feat-058-fixture@example.invalid',
};

/** Build a throwaway git repo with tracked-modified + untracked + gitignored fixtures. */
function makeFixtureRepo() {
  const dir = fs.mkdtempSync(
    path.join(os.tmpdir(), 'claude-scratch-test-')
  );
  execFileSync('git', ['init', '-q'], { cwd: dir, env: GIT_ENV });

  fs.writeFileSync(
    path.join(dir, '.gitignore'),
    'ignored.txt\nignored_dir/\n'
  );
  fs.writeFileSync(path.join(dir, 'tracked.txt'), 'original\n');
  execFileSync('git', ['add', '.gitignore', 'tracked.txt'], {
    cwd: dir,
    env: GIT_ENV,
  });
  execFileSync('git', ['commit', '-q', '-m', 'initial'], {
    cwd: dir,
    env: GIT_ENV,
  });

  // Tracked-but-modified (uncommitted).
  fs.writeFileSync(path.join(dir, 'tracked.txt'), 'modified\n');

  // Untracked.
  fs.writeFileSync(path.join(dir, 'untracked.txt'), 'untracked content\n');

  // Gitignored file + gitignored directory (must NEVER be copied).
  fs.writeFileSync(path.join(dir, 'ignored.txt'), 'must not appear\n');
  fs.mkdirSync(path.join(dir, 'ignored_dir'));
  fs.writeFileSync(
    path.join(dir, 'ignored_dir', 'inner.txt'),
    'must not appear either\n'
  );

  return dir;
}

function rm(dir) {
  fs.rmSync(dir, { recursive: true, force: true });
}

test('getChangedPaths: returns exactly the tracked-modified + untracked paths, excluding gitignored ones', () => {
  const repo = makeFixtureRepo();
  try {
    const repoRoot = getRepoRoot(repo);
    const paths = getChangedPaths(repoRoot).sort();
    assert.deepEqual(paths, ['tracked.txt', 'untracked.txt']);
  } finally {
    rm(repo);
  }
});

test('getChangedPaths: excludes .scratch/ defensively even when .gitignore does not list it', () => {
  const repo = makeFixtureRepo();
  try {
    fs.mkdirSync(path.join(repo, '.scratch'));
    fs.writeFileSync(
      path.join(repo, '.scratch', 'previous-snapshot.txt'),
      'stale\n'
    );
    const repoRoot = getRepoRoot(repo);
    const paths = getChangedPaths(repoRoot);
    assert.ok(
      !paths.some((p) => p.startsWith('.scratch/') || p === '.scratch'),
      '.scratch/ must never be treated as copyable input, gitignored or not'
    );
  } finally {
    rm(repo);
  }
});

test('runSnapshot: destination folder contains exactly the modified+untracked files, current content, and none of the gitignored ones', () => {
  const repo = makeFixtureRepo();
  try {
    const fixedNow = new Date(2026, 7, 11, 16, 12, 0); // 2026-08-11 16:12:00 local
    const dest = runSnapshot({ cwd: repo, now: fixedNow });

    assert.equal(path.basename(dest), '2026-08-11T161200');
    assert.equal(path.dirname(dest), path.join(getRepoRoot(repo), '.scratch'));

    // Positive case: included files present with CURRENT (uncommitted) content.
    assert.equal(
      fs.readFileSync(path.join(dest, 'tracked.txt'), 'utf8'),
      'modified\n'
    );
    assert.equal(
      fs.readFileSync(path.join(dest, 'untracked.txt'), 'utf8'),
      'untracked content\n'
    );

    // Negative case, asserted as explicitly as the positive one: gitignored
    // file and gitignored directory must be absent from the destination.
    assert.equal(fs.existsSync(path.join(dest, 'ignored.txt')), false);
    assert.equal(fs.existsSync(path.join(dest, 'ignored_dir')), false);

    // And nothing extra: the destination must contain exactly the two
    // expected entries (plus the .gitignore/tracked.txt tracked-clean file
    // is NOT modified so is correctly absent too — only uncommitted state
    // is copied).
    const entries = fs.readdirSync(dest).sort();
    assert.deepEqual(entries, ['tracked.txt', 'untracked.txt']);
  } finally {
    rm(repo);
  }
});

test('runSnapshot: two runs in the same second get disambiguated destination folders', () => {
  const repo = makeFixtureRepo();
  try {
    const fixedNow = new Date(2026, 7, 11, 16, 12, 0);
    const dest1 = runSnapshot({ cwd: repo, now: fixedNow });
    const dest2 = runSnapshot({ cwd: repo, now: fixedNow });
    assert.notEqual(dest1, dest2);
    assert.equal(path.basename(dest2), '2026-08-11T161200-2');
  } finally {
    rm(repo);
  }
});

test('getRepoRoot: throws ScratchError (not an uncaught exception) outside a git repository', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'claude-scratch-nogit-'));
  try {
    assert.throws(() => getRepoRoot(dir), ScratchError);
  } finally {
    rm(dir);
  }
});

test('copyPaths: fails loudly on the first unreadable path and does not silently skip it', () => {
  const repo = makeFixtureRepo();
  try {
    const repoRoot = getRepoRoot(repo);
    const destDir = path.join(repoRoot, '.scratch', 'copy-test');
    fs.mkdirSync(destDir, { recursive: true });

    assert.throws(
      () => copyPaths(repoRoot, ['tracked.txt', 'does-not-exist.txt'], destDir),
      (err) =>
        err instanceof ScratchError &&
        /does-not-exist\.txt/.test(err.message)
    );

    // The file before the failing one was copied (proves it stops AT the
    // failure rather than never starting, and doesn't clean up itself —
    // that responsibility belongs to the caller, runSnapshot).
    assert.equal(
      fs.readFileSync(path.join(destDir, 'tracked.txt'), 'utf8'),
      'modified\n'
    );
    assert.equal(fs.existsSync(path.join(destDir, 'does-not-exist.txt')), false);
  } finally {
    rm(repo);
  }
});

test('runSnapshot: fails loudly (does not throw an uncaught exception, returns via ScratchError) when the destination cannot be created', () => {
  const repo = makeFixtureRepo();
  try {
    const repoRoot = getRepoRoot(repo);
    // Pre-create a plain FILE at the ".scratch" path itself, so the
    // recursive mkdir needed to create ".scratch/<timestamp>/" cannot
    // succeed (it needs ".scratch" to be a directory, and it is a file) —
    // simulates "destination cannot be created" (GR#1: disk/permission
    // failures must fail loudly, never half-copy silently).
    fs.writeFileSync(
      path.join(repoRoot, '.scratch'),
      'blocking file, not a directory\n'
    );
    const fixedNow = new Date(2026, 7, 11, 16, 12, 0);

    assert.throws(
      () => runSnapshot({ cwd: repo, now: fixedNow }),
      ScratchError
    );
  } finally {
    rm(repo);
  }
});

test('formatTimestamp: zero-pads to the documented "YYYY-MM-DDTHHMMSS" shape', () => {
  assert.equal(formatTimestamp(new Date(2026, 0, 5, 3, 4, 6)), '2026-01-05T030406');
});

test('pickDestDir: does not collide with an existing folder for the same second', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'claude-scratch-dest-'));
  try {
    const now = new Date(2026, 7, 11, 16, 12, 0);
    const first = pickDestDir(dir, now);
    fs.mkdirSync(first, { recursive: true });
    const second = pickDestDir(dir, now);
    assert.notEqual(first, second);
    assert.equal(path.basename(second), '2026-08-11T161200-2');
  } finally {
    rm(dir);
  }
});

// ---------------------------------------------------------------------------
// P0 #1 regression: git status stderr warnings must be fatal, not silently
// treated as "nothing changed" just because stdout is empty and exit is 0.
// ---------------------------------------------------------------------------

test('getChangedPaths: throws ScratchError when git status exits 0 with EMPTY stdout but a stderr warning (partial enumeration) — the exact silent-data-loss shape', () => {
  // A hand-built stand-in for what `git status` does on this platform when
  // it hits an untracked path buried too deep to open (~850+ chars): exit
  // code 0 (a warning, not a git failure), empty stdout (nothing enumerated
  // for that subtree), non-empty stderr carrying the warning. Injected via
  // getChangedPaths' spawnSyncFn parameter rather than a real 850-char path
  // fixture, which would be unwieldy to construct/clean up portably in a
  // unit test — the injection point exercises the exact same decision logic
  // the live repro hit.
  const fakeSpawnSync = () => ({
    stdout: '',
    stderr:
      "warning: could not open directory 'some/deeply/nested/path': Filename too long\n",
    status: 0,
    error: null,
  });

  assert.throws(
    () => getChangedPaths('C:\\fake\\repo-root', { spawnSyncFn: fakeSpawnSync }),
    ScratchError
  );
});

test('getChangedPaths: also treats a generic "error:"-prefixed stderr line as fatal, not just the literal "Filename too long" wording (GR#15)', () => {
  const fakeSpawnSync = () => ({
    stdout: '',
    stderr: 'error: something else git considers wrong while enumerating\n',
    status: 0,
    error: null,
  });

  assert.throws(
    () => getChangedPaths('C:\\fake\\repo-root', { spawnSyncFn: fakeSpawnSync }),
    ScratchError
  );
});

test('getChangedPaths: does NOT throw on ordinary stderr-free output (no false positive from the new check)', () => {
  const fakeSpawnSync = () => ({
    stdout: '?? untracked.txt\0',
    stderr: '',
    status: 0,
    error: null,
  });

  const paths = getChangedPaths('C:\\fake\\repo-root', { spawnSyncFn: fakeSpawnSync });
  assert.deepEqual(paths, ['untracked.txt']);
});

// ---------------------------------------------------------------------------
// P0 #2 regression: destination-directory creation must be atomic — no
// check-then-create TOCTOU window two concurrent runs can both slip through.
// ---------------------------------------------------------------------------

test('createDestDir: two calls for the identical scratchRoot+date resolve to two DIFFERENT, both-real destination folders', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'claude-scratch-atomic-'));
  try {
    const now = new Date(2026, 7, 11, 16, 12, 0);
    const first = createDestDir(dir, now);
    const second = createDestDir(dir, now);

    assert.notEqual(first, second);
    assert.equal(path.basename(first), '2026-08-11T161200');
    assert.equal(path.basename(second), '2026-08-11T161200-2');
    // Both must actually exist as real directories on disk — proves this
    // isn't just two distinct strings, but two distinct, both-created folders.
    assert.ok(fs.statSync(first).isDirectory());
    assert.ok(fs.statSync(second).isDirectory());
  } finally {
    rm(dir);
  }
});

test('createDestDir: simulated race — a directory created between name-selection and create attempt is detected via EEXIST and skipped, not silently reused', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'claude-scratch-race-'));
  try {
    const now = new Date(2026, 7, 11, 16, 12, 0);
    const base = formatTimestamp(now);
    // Simulate "someone else" winning the race for the base name by
    // creating it before createDestDir ever runs.
    fs.mkdirSync(path.join(dir, base), { recursive: true });

    const winner = createDestDir(dir, now);
    assert.equal(path.basename(winner), `${base}-2`);
    // The original, "someone else's", folder is untouched/still empty —
    // proves createDestDir did not write into it.
    assert.deepEqual(fs.readdirSync(path.join(dir, base)), []);
  } finally {
    rm(dir);
  }
});

test('runSnapshot: concurrent real child processes never collide on the same destination folder (strongest proof — mirrors the Destructive repro)', async () => {
  const repo = makeFixtureRepo();
  try {
    const runnerScript = `
      const scratch = require(${JSON.stringify(path.resolve(__dirname, 'claude-scratch.js'))});
      const now = new Date(2026, 7, 11, 16, 12, 0);
      try {
        const dest = scratch.runSnapshot({ cwd: ${JSON.stringify(repo)}, now });
        process.stdout.write(dest + '\\n');
      } catch (err) {
        process.stdout.write('ERROR:' + err.message + '\\n');
      }
    `;

    const { spawn } = require('child_process');
    const N = 6;
    const runs = Array.from({ length: N }, () => new Promise((resolve, reject) => {
      const child = spawn(process.execPath, ['-e', runnerScript], { cwd: repo });
      let stdout = '';
      child.stdout.on('data', (d) => { stdout += d.toString(); });
      child.on('error', reject);
      child.on('close', () => resolve(stdout.trim()));
    }));

    const results = await Promise.all(runs);
    const successes = results.filter((r) => !r.startsWith('ERROR:'));

    // Every process that succeeded must have landed on a UNIQUE folder.
    const unique = new Set(successes);
    assert.equal(
      unique.size,
      successes.length,
      `expected ${successes.length} unique destinations, got ${unique.size}: ${JSON.stringify(successes)}`
    );

    // And each surviving folder must contain the complete, uncorrupted
    // fixture content (proves no cross-run clobbering of file contents).
    for (const dest of successes) {
      assert.equal(
        fs.readFileSync(path.join(dest, 'tracked.txt'), 'utf8'),
        'modified\n'
      );
      assert.equal(
        fs.readFileSync(path.join(dest, 'untracked.txt'), 'utf8'),
        'untracked content\n'
      );
    }
  } finally {
    rm(repo);
  }
});

// ---------------------------------------------------------------------------
// P1 regression: directory junctions must be skipped, with a loud warning,
// never followed into content outside the repository's own working tree.
// ---------------------------------------------------------------------------

test('pathCrossesReparsePoint: detects a real Windows junction on the path', () => {
  const outsideDir = fs.mkdtempSync(path.join(os.tmpdir(), 'claude-scratch-outside-'));
  const repo = makeFixtureRepo();
  try {
    fs.writeFileSync(path.join(outsideDir, 'secret.txt'), 'outside repo content\n');
    const junctionPath = path.join(repo, 'linked_dir');
    fs.symlinkSync(outsideDir, junctionPath, 'junction');

    assert.equal(
      pathCrossesReparsePoint(repo, path.join('linked_dir', 'secret.txt')),
      true
    );
    // Sanity: an ordinary nested file under a real directory is NOT flagged.
    assert.equal(pathCrossesReparsePoint(repo, 'tracked.txt'), false);
  } finally {
    rm(repo);
    rm(outsideDir);
  }
});

test('pathCrossesReparsePoint: detects a junction that IS the leaf itself, with no trailing slash', () => {
  // P1 round 2 regression: the original ancestor-only logic
  // (`relPath.split(/[\\/]/).slice(0, -1)`) never lstat'd the final path
  // component, so a reparse point that is the leaf itself was invisible
  // UNLESS the caller happened to pass a trailing slash (which git's
  // porcelain format does for directory-shaped untracked entries, but a
  // caller passing a bare path — exactly the shape a real file-level
  // symlink would have — would not). Prove the leaf case is now detected
  // with NO trailing slash in the path argument, which is precisely the
  // shape the Destructive proved broken pre-fix.
  const outsideDir = fs.mkdtempSync(path.join(os.tmpdir(), 'claude-scratch-outside-'));
  const repo = makeFixtureRepo();
  try {
    const junctionLeaf = path.join(repo, 'linked_repo');
    fs.symlinkSync(outsideDir, junctionLeaf, 'junction');

    // No trailing slash — this is the exact shape that was proven broken:
    // pre-fix, this returned `false` because 'linked_repo'.split(...).slice(0,-1)
    // is an empty ancestor list, so the leaf itself was never lstat'd.
    assert.equal(pathCrossesReparsePoint(repo, 'linked_repo'), true);

    // Same target, with a trailing slash (the git-porcelain shape) — must
    // still be true, so the fix doesn't regress the accidental case either.
    assert.equal(pathCrossesReparsePoint(repo, 'linked_repo/'), true);

    // Sanity: an ordinary leaf file/dir with no reparse point anywhere on
    // its path is still NOT flagged.
    assert.equal(pathCrossesReparsePoint(repo, 'tracked.txt'), false);
  } finally {
    rm(repo);
    rm(outsideDir);
  }
});

test('copyPaths: skips a file reached through a junction, warns loudly, and does not copy the outside-repo content', () => {
  const outsideDir = fs.mkdtempSync(path.join(os.tmpdir(), 'claude-scratch-outside-'));
  const repo = makeFixtureRepo();
  try {
    fs.writeFileSync(path.join(outsideDir, 'secret.txt'), 'outside repo content\n');
    fs.symlinkSync(outsideDir, path.join(repo, 'linked_dir'), 'junction');

    const repoRoot = getRepoRoot(repo);
    const destDir = path.join(repoRoot, '.scratch', 'junction-test');
    fs.mkdirSync(destDir, { recursive: true });

    const relPaths = ['tracked.txt', path.join('linked_dir', 'secret.txt')];

    let stderrCaptured = '';
    const origWrite = process.stderr.write;
    process.stderr.write = (chunk, ...args) => {
      stderrCaptured += chunk;
      return origWrite.call(process.stderr, chunk, ...args);
    };
    let skipped;
    try {
      skipped = copyPaths(repoRoot, relPaths, destDir);
    } finally {
      process.stderr.write = origWrite;
    }

    assert.deepEqual(skipped, [path.join('linked_dir', 'secret.txt')]);
    assert.match(stderrCaptured, /junction\/reparse point/);
    assert.match(stderrCaptured, /secret\.txt/);

    // The legitimate file WAS copied; the junction-reached one was NOT.
    assert.equal(
      fs.readFileSync(path.join(destDir, 'tracked.txt'), 'utf8'),
      'modified\n'
    );
    assert.equal(
      fs.existsSync(path.join(destDir, 'linked_dir', 'secret.txt')),
      false
    );
  } finally {
    rm(repo);
    rm(outsideDir);
  }
});

// BUG-147 regression: the pre-copy pathCrossesReparsePoint check and the
// actual fs.copyFileSync read are two separate syscalls, not one atomic
// operation, so a concurrent process can win the race in between. A true
// concurrent-process race is inherently non-deterministic to reproduce in a
// test, so this pins the exact same OUTCOME deterministically: it monkey-
// patches fs.copyFileSync so that, at the precise moment copyPaths would
// perform the real read — i.e. exactly the window the race targets — the
// ancestor directory is swapped from a real directory to a junction
// pointing outside the repo, before delegating to the real copyFileSync.
// This is the earliest point in copyPaths' own control flow the swap could
// land and still be "during the copy", matching the attacker's live
// repro. The junction is deliberately left in place afterward (not
// reverted), so the post-copy re-check the fix added must still see it and
// discard the result — see the file header's disclosed residual for the
// (undetectable-by-construction) swap-back-before-recheck case this does
// NOT cover.
test('copyPaths (BUG-147): a junction that appears at the moment of the read is caught by the post-copy re-check, and the smuggled content is discarded', () => {
  const outsideDir = fs.mkdtempSync(path.join(os.tmpdir(), 'claude-scratch-outside-'));
  const repo = makeFixtureRepo();
  const origCopyFileSync = fs.copyFileSync;
  try {
    fs.writeFileSync(path.join(outsideDir, 'smuggled.txt'), 'outside repo content\n');

    // At the moment copyPaths is called, 'swapped_dir' is a REAL directory
    // containing a real, legitimate file — the pre-copy check must pass it.
    const swappedDirPath = path.join(repo, 'swapped_dir');
    fs.mkdirSync(swappedDirPath);
    fs.writeFileSync(path.join(swappedDirPath, 'smuggled.txt'), 'placeholder — never actually read\n');

    const repoRoot = getRepoRoot(repo);
    const destDir = path.join(repoRoot, '.scratch', 'bug147-test');
    fs.mkdirSync(destDir, { recursive: true });

    const relPath = path.join('swapped_dir', 'smuggled.txt');
    let swapped = false;

    fs.copyFileSync = (src, dest, ...rest) => {
      if (!swapped && src.includes('swapped_dir')) {
        // This is the exact window BUG-147 attacks: right before the real
        // read, replace the ancestor directory with a junction to outside
        // content. Windows requires the target to not exist first.
        fs.rmSync(swappedDirPath, { recursive: true, force: true });
        fs.symlinkSync(outsideDir, swappedDirPath, 'junction');
        swapped = true;
      }
      return origCopyFileSync(src, dest, ...rest);
    };

    let stderrCaptured = '';
    const origWrite = process.stderr.write;
    process.stderr.write = (chunk, ...args) => {
      stderrCaptured += chunk;
      return origWrite.call(process.stderr, chunk, ...args);
    };
    let skipped;
    try {
      skipped = copyPaths(repoRoot, [relPath], destDir);
    } finally {
      process.stderr.write = origWrite;
      fs.copyFileSync = origCopyFileSync;
    }

    assert.equal(swapped, true, 'test setup sanity: the swap must actually have fired');

    // Pre-fix, this would have copied outside-repo content through
    // unmodified since the pre-copy check never re-ran. Post-fix, the
    // post-copy re-check must catch the still-present junction, discard
    // whatever was copied, and report it via `skipped` — same contract as
    // the pre-copy-detected case above.
    assert.deepEqual(skipped, [relPath]);
    assert.match(stderrCaptured, /BUG-147/);
    assert.equal(
      fs.existsSync(path.join(destDir, 'swapped_dir', 'smuggled.txt')),
      false,
      'smuggled outside-repo content must not survive in the destination'
    );
  } finally {
    fs.copyFileSync = origCopyFileSync;
    rm(repo);
    rm(outsideDir);
  }
});

// BUG-149 regression: if the BUG-147 discard's own fs.rmSync fails, the tool
// must not lie and claim "not kept" while the malicious copy is still on
// disk. Reproduces the exact fixture from the bug report: the post-copy
// recheck fires (junction present, matching the BUG-147 case above), but
// this time the discard rmSync itself throws every time it's called —
// simulating a persistent EPERM/locked-file condition, not a one-off
// transient blip. Monkey-patches fs.rmSync to always throw, matching the
// established pattern in this file (fs.copyFileSync monkey-patch above) for
// injecting a failure at a precise point in copyPaths' control flow.
test('copyPaths (BUG-149): a persistent rmSync failure during the BUG-147 discard is a fatal ScratchError, not a silently-swallowed "not kept"', () => {
  const outsideDir = fs.mkdtempSync(path.join(os.tmpdir(), 'claude-scratch-outside-'));
  const repo = makeFixtureRepo();
  const origCopyFileSync = fs.copyFileSync;
  const origRmSync = fs.rmSync;
  try {
    fs.writeFileSync(path.join(outsideDir, 'smuggled.txt'), 'outside repo content\n');

    const swappedDirPath = path.join(repo, 'swapped_dir');
    fs.mkdirSync(swappedDirPath);
    fs.writeFileSync(path.join(swappedDirPath, 'smuggled.txt'), 'placeholder — never actually read\n');

    const repoRoot = getRepoRoot(repo);
    const destDir = path.join(repoRoot, '.scratch', 'bug149-test');
    fs.mkdirSync(destDir, { recursive: true });

    const relPath = path.join('swapped_dir', 'smuggled.txt');
    const destFile = path.join(destDir, relPath);
    let swapped = false;

    fs.copyFileSync = (src, dest, ...rest) => {
      if (!swapped && src.includes('swapped_dir')) {
        fs.rmSync(swappedDirPath, { recursive: true, force: true });
        fs.symlinkSync(outsideDir, swappedDirPath, 'junction');
        swapped = true;
      }
      return origCopyFileSync(src, dest, ...rest);
    };

    // Simulate a persistent (non-transient) discard failure: every call to
    // fs.rmSync targeting the malicious copy throws EPERM, exactly like the
    // live-verified repro in the bug report. Calls for OTHER paths (e.g.
    // the outer test's own cleanup) are passed through untouched.
    fs.rmSync = (target, ...rest) => {
      if (target === destFile) {
        const err = new Error('EPERM: operation not permitted, unlink');
        err.code = 'EPERM';
        throw err;
      }
      return origRmSync(target, ...rest);
    };

    let thrown;
    try {
      copyPaths(repoRoot, [relPath], destDir);
    } catch (err) {
      thrown = err;
    } finally {
      fs.copyFileSync = origCopyFileSync;
      fs.rmSync = origRmSync;
    }

    assert.equal(swapped, true, 'test setup sanity: the swap must actually have fired');

    // Must fail LOUDLY (a fatal ScratchError), not return normally claiming
    // the path was "skipped"/"not kept".
    assert.ok(thrown instanceof ScratchError, 'expected a ScratchError, not a silent success');
    assert.match(thrown.message, /could NOT be removed/);
    assert.match(thrown.message, new RegExp(relPath.replace(/\\/g, '\\\\')));

    // The malicious content must still be reported as present by the test's
    // own accounting (proves the fix's premise: the file really was left
    // behind by the failed discard, which is exactly why silence here would
    // have been a lie). We can't assert fs.existsSync via the real fs here
    // since fs.rmSync was restored, but the destination file was never
    // actually deleted by our monkey-patched rmSync, so it must still exist.
    assert.equal(
      fs.existsSync(destFile),
      true,
      'the discard genuinely failed in this fixture, so the file must still be on disk'
    );
  } finally {
    fs.copyFileSync = origCopyFileSync;
    fs.rmSync = origRmSync;
    rm(repo);
    rm(outsideDir);
  }
});

// BUG-149 companion: a TRANSIENT rmSync failure (succeeds on retry) must
// still result in the normal, honest "skipped" outcome — proves the retry
// wrapper doesn't turn every flaky delete into a hard failure, only a
// persistent one.
test('copyPaths (BUG-149): a transient rmSync failure during the BUG-147 discard is absorbed by retry and still reports skipped honestly', () => {
  const outsideDir = fs.mkdtempSync(path.join(os.tmpdir(), 'claude-scratch-outside-'));
  const repo = makeFixtureRepo();
  const origCopyFileSync = fs.copyFileSync;
  const origRmSync = fs.rmSync;
  try {
    fs.writeFileSync(path.join(outsideDir, 'smuggled.txt'), 'outside repo content\n');

    const swappedDirPath = path.join(repo, 'swapped_dir');
    fs.mkdirSync(swappedDirPath);
    fs.writeFileSync(path.join(swappedDirPath, 'smuggled.txt'), 'placeholder — never actually read\n');

    const repoRoot = getRepoRoot(repo);
    const destDir = path.join(repoRoot, '.scratch', 'bug149-transient-test');
    fs.mkdirSync(destDir, { recursive: true });

    const relPath = path.join('swapped_dir', 'smuggled.txt');
    const destFile = path.join(destDir, relPath);
    let swapped = false;
    let rmAttempts = 0;

    fs.copyFileSync = (src, dest, ...rest) => {
      if (!swapped && src.includes('swapped_dir')) {
        fs.rmSync(swappedDirPath, { recursive: true, force: true });
        fs.symlinkSync(outsideDir, swappedDirPath, 'junction');
        swapped = true;
      }
      return origCopyFileSync(src, dest, ...rest);
    };

    // Fail the first two attempts against the malicious copy, then succeed —
    // simulating a transient lock (e.g. AV scanner) that clears on its own.
    fs.rmSync = (target, ...rest) => {
      if (target === destFile) {
        rmAttempts += 1;
        if (rmAttempts < 3) {
          const err = new Error('EBUSY: resource busy or locked, unlink');
          err.code = 'EBUSY';
          throw err;
        }
      }
      return origRmSync(target, ...rest);
    };

    let skipped;
    try {
      skipped = copyPaths(repoRoot, [relPath], destDir);
    } finally {
      fs.copyFileSync = origCopyFileSync;
      fs.rmSync = origRmSync;
    }

    assert.equal(swapped, true, 'test setup sanity: the swap must actually have fired');
    assert.ok(rmAttempts >= 3, 'expected the retry wrapper to have retried past the transient failures');
    assert.deepEqual(skipped, [relPath]);
    assert.equal(
      fs.existsSync(destFile),
      false,
      'once the retry succeeds, the malicious copy must actually be gone'
    );
  } finally {
    fs.copyFileSync = origCopyFileSync;
    fs.rmSync = origRmSync;
    rm(repo);
    rm(outsideDir);
  }
});
