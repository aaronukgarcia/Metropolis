/**
 * claude-codenamehook.test.js — end-to-end + unit tests for FEAT-046 (BOW
 * mkey: tool.codenamehook / code.json tool.codenameguard): the commit-msg
 * codename content scan (claude-codename-content-scan.js), the shared
 * pattern module it and claude-codename-guard.js both require
 * (claude-codename-patterns.js), and the two-check coexistence wiring in
 * githooks/commit-msg.
 *
 * GR#22 DISCIPLINE (this file's own compliance, not just the code under
 * test): every positive fixture below assembles its forbidden content at
 * TEST-RUNTIME from fragments, exactly the way claude-codename-patterns.js
 * itself does and the way claude-codename-guard.test.js's own ABBR constant
 * already does — never a whole literal typed anywhere in this file.
 *
 * Run: node --test claude-codenamehook.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawnSync } = require('child_process');

const install = require('./claude-committhook-install.js');
const patterns = require('./claude-codename-patterns.js');
const contentScan = require('./claude-codename-content-scan.js');

const SANCTIONED_NAME = 'Sanctioned Contributor';
const SANCTIONED_EMAIL = 'sanctioned@example.invalid';
const FABRICATED_NAME = 'Fabricated Author';
const FABRICATED_EMAIL = 'fabricated@example.invalid';

// GR#22: the numbered-abbreviation fixture, assembled from fragments at
// runtime — same technique and same fragment set as
// claude-codename-guard.test.js's own ABBR constant.
const ABBR = ['C', 'S', '1'].join('');

function withTempRepo(fn) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'codenamehook-fixture-'));
  try {
    return fn(dir);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

function git(cwd, args, extraEnv) {
  return spawnSync('git', args, {
    cwd,
    encoding: 'utf8',
    env: extraEnv ? { ...process.env, ...extraEnv } : process.env,
  });
}

function gitOk(cwd, args, extraEnv) {
  const r = git(cwd, args, extraEnv);
  if (r.status !== 0) {
    throw new Error(`git ${args.join(' ')} failed (${r.status}): ${r.stderr}\n${r.stdout}`);
  }
  return r.stdout.trim();
}

function commitCount(dir) {
  const r = git(dir, ['rev-list', '--all', '--count']);
  return parseInt((r.stdout || '0').trim(), 10);
}

/** Mirrors githooks/commit-msg's real production layout: the tracked
 * canonical hook installed, plus the repo-root modules it requires
 * (identity + codename-scan + shared pattern module) copied alongside it —
 * same shape claude-committhook.test.js's own initRepo() uses. */
function initRepo(dir) {
  gitOk(dir, ['init', '-b', 'main']);
  gitOk(dir, ['config', 'user.name', SANCTIONED_NAME]);
  gitOk(dir, ['config', 'user.email', SANCTIONED_EMAIL]);
  for (const f of ['claude-author-identity.js', 'claude-codename-content-scan.js', 'claude-codename-patterns.js', 'claude-codename-diff.js', 'claude-codename-guard.js', 'claude-git-commit-trigger.js', 'claude-quote-mask.js', 'claude-destructive-guard.js']) {
    fs.copyFileSync(path.join(__dirname, f), path.join(dir, f));
  }
  // BUG-340/BUG-336: githooks/commit-msg now also requires
  // githooks/verdict-guard.js (the third check in this hook slot) — mirror
  // the real production layout, same reasoning as claude-committhook.test.js's
  // own initRepo(). This fixture's commits never stage anything under an
  // enforced dir, so verdict-guard.js never needs claude-bow.js/claude-db.js/
  // mysql2 here (see loadClassificationDeps() vs loadVerdictDeps()).
  fs.mkdirSync(path.join(dir, 'githooks'), { recursive: true });
  fs.copyFileSync(path.join(__dirname, 'githooks', 'verdict-guard.js'), path.join(dir, 'githooks', 'verdict-guard.js'));
  fs.mkdirSync(path.join(dir, '.claude'), { recursive: true });
  fs.writeFileSync(path.join(dir, '.claude', 'settings.json'), JSON.stringify({ hooks: {} }), 'utf8');
  // Commit the support files BEFORE installing the hook (hook-free commit)
  // so every test's own `git add -A` has nothing new to say about them —
  // same reasoning as claude-committhook.test.js's own initRepo() fix.
  gitOk(dir, ['add', '-A']);
  gitOk(dir, ['commit', '-m', 'seed fixture support files (pre-hook, not under test)']);
  install.install(dir);
}

function writeAndStage(dir, name, content) {
  fs.writeFileSync(path.join(dir, name), content, 'utf8');
  gitOk(dir, ['add', '-A']);
}

// ---------------------------------------------------------------------------
// AC-1: hook point — commit-msg fires for commit + merge (never assumed;
// live evidence gathered specifically for content-scan visibility, mirroring
// claude-committhook.test.js's own AC-1 evidence method for the identity
// case, but re-run here rather than inferred by analogy).
// ---------------------------------------------------------------------------

test('AC-1: the codename content scan (via the installed hook) fires for a plain `git commit`, denying a fragment-assembled positive', () => {
  withTempRepo((dir) => {
    initRepo(dir);
    const before = commitCount(dir);
    writeAndStage(dir, 'notes.txt', `a stray reference to ${ABBR} slipped into this line\n`);
    const bad = git(dir, ['commit', '-m', 'x']);
    assert.notEqual(bad.status, 0, 'expected the codename violation to be rejected');
    assert.match(bad.stderr || '', /CODENAME/);
    assert.equal(commitCount(dir), before, 'no commit object should have been created');
  });
});

test('AC-1: the codename content scan fires for a real no-ff merge commit (the case a pre-commit-only implementation structurally cannot see)', () => {
  withTempRepo((dir) => {
    initRepo(dir);
    writeAndStage(dir, 'a.txt', '1');
    gitOk(dir, ['commit', '-m', 'base']);
    gitOk(dir, ['checkout', '-b', 'topic']);
    writeAndStage(dir, 'b.txt', `topic reference to ${ABBR} here\n`);
    gitOk(dir, ['commit', '-m', 'topic commit', '--no-verify']);
    gitOk(dir, ['checkout', 'main']);

    const before = commitCount(dir);
    const bad = git(dir, ['merge', '--no-ff', 'topic', '-m', 'merge it']);
    assert.notEqual(bad.status, 0, 'expected the merge introducing forbidden content to be rejected');
    assert.match(bad.stderr || '', /CODENAME/);
    assert.equal(commitCount(dir), before, 'no merge commit object should have been created');
    git(dir, ['merge', '--abort']);
  });
});

test('AC-1 evidence: an installed `pre-commit` hook does NOT fire for a real no-ff merge, while `commit-msg`-based content scanning does — reconfirmed for the content-scan case specifically, not inferred from the identity case', () => {
  withTempRepo((dir) => {
    gitOk(dir, ['init', '-b', 'main']);
    gitOk(dir, ['config', 'user.name', SANCTIONED_NAME]);
    gitOk(dir, ['config', 'user.email', SANCTIONED_EMAIL]);
    const hooksDir = path.join(dir, '.git', 'hooks');
    fs.mkdirSync(hooksDir, { recursive: true });
    // A pre-commit hook that reports the staged diff's length via stderr —
    // proves both whether it fires AND whether it sees real content, exactly
    // as the acceptance file's AC-1 evidence describes.
    fs.writeFileSync(
      path.join(hooksDir, 'pre-commit'),
      '#!/bin/sh\nLEN=$(git diff --cached --unified=0 | wc -c)\necho "PRECOMMIT-DIFFLEN:$LEN" 1>&2\nexit 0\n'
    );
    fs.writeFileSync(
      path.join(hooksDir, 'commit-msg'),
      '#!/bin/sh\nLEN=$(git diff --cached --unified=0 | wc -c)\necho "COMMITMSG-DIFFLEN:$LEN" 1>&2\nexit 0\n'
    );
    try {
      fs.chmodSync(path.join(hooksDir, 'pre-commit'), 0o755);
      fs.chmodSync(path.join(hooksDir, 'commit-msg'), 0o755);
    } catch {
      /* Windows best-effort, see claude-committhook-install.js */
    }

    writeAndStage(dir, 'a.txt', '1');
    const plain = git(dir, ['commit', '-m', 'base']);
    assert.equal(plain.status, 0, plain.stderr);
    assert.match(plain.stderr || '', /PRECOMMIT-DIFFLEN:\d+/, 'expected pre-commit to fire and see diff content for a plain commit');
    assert.match(plain.stderr || '', /COMMITMSG-DIFFLEN:\d+/, 'expected commit-msg to fire and see diff content for a plain commit');

    gitOk(dir, ['checkout', '-b', 'topic']);
    writeAndStage(dir, 'b.txt', '2');
    gitOk(dir, ['commit', '-m', 'topic commit']);
    gitOk(dir, ['checkout', 'main']);

    const merge = git(dir, ['merge', '--no-ff', 'topic', '-m', 'merge it']);
    assert.equal(merge.status, 0, merge.stderr);
    assert.doesNotMatch(merge.stderr || '', /PRECOMMIT-DIFFLEN/, 'pre-commit must NOT fire for a real merge commit on this git version');
    assert.match(merge.stderr || '', /COMMITMSG-DIFFLEN:\d+/, 'commit-msg MUST fire for a real merge commit, seeing the incoming content');
  });
});

// ---------------------------------------------------------------------------
// AC-2: diff-scoped (added lines only), not whole-file-scoped.
// ---------------------------------------------------------------------------

test('AC-2: scanStagedDiff() reads via `git diff --cached`, added lines only (source-level check)', () => {
  const src = fs.readFileSync(path.join(__dirname, 'claude-codename-content-scan.js'), 'utf8');
  assert.match(src, /diff --cached/);
  assert.match(src, /startsWith\('\+'\)/);
});

test('AC-2: a pre-existing violation elsewhere in a file, left UNSTAGED by the current change, does not block the commit (diff-scoped, not whole-file-scoped)', () => {
  withTempRepo((dir) => {
    initRepo(dir);
    // Seed a violation into the repo history via --no-verify (never a real
    // unguarded commit through the enforcing hook).
    writeAndStage(dir, 'notes.txt', `line one\nrelated to ${ABBR} historically\nline three\n`);
    gitOk(dir, ['commit', '-m', 'seed (bypassing hooks deliberately for fixture setup)', '--no-verify']);

    // Now make an UNRELATED clean edit to a different line in the same
    // file — the pre-existing violation is not part of THIS diff.
    fs.writeFileSync(
      path.join(dir, 'notes.txt'),
      `line one, edited cleanly\nrelated to ${ABBR} historically\nline three\n`,
      'utf8'
    );
    gitOk(dir, ['add', '-A']);
    const before = commitCount(dir);
    const r = git(dir, ['commit', '-m', 'unrelated clean fix']);
    assert.equal(r.status, 0, `expected an unrelated clean edit to be allowed despite a pre-existing violation elsewhere in the same file. stderr: ${r.stderr}`);
    assert.equal(commitCount(dir), before + 1);
  });
});

// ---------------------------------------------------------------------------
// AC-3: the check can genuinely fail, and genuinely pass.
// ---------------------------------------------------------------------------

test('AC-3: a fragment-assembled positive staged content is rejected — non-zero exit AND no new commit object', () => {
  withTempRepo((dir) => {
    initRepo(dir);
    writeAndStage(dir, 'notes.txt', `a reference to ${ABBR} here\n`);
    const before = commitCount(dir);
    const r = git(dir, ['commit', '-m', 'x']);
    assert.notEqual(r.status, 0);
    assert.equal(commitCount(dir), before);
  });
});

// ---------------------------------------------------------------------------
// BUG-182 regression: stagedAddedLines() used to derive "added lines" via
// `l.startsWith('+') && !l.startsWith('+++')`, guessing the one-per-file
// '+++ b/<path>' header line by TEXT shape rather than position. An added
// line whose own content began with two literal '+' characters is emitted by
// `git diff --unified=0` as its own '+' marker followed by '++...' payload —
// textually identical to the header line — so the old filter silently
// dropped it before it ever reached the pattern scanner. Reproduced live by
// this session's Destructive attacker against FEAT-046, through the real
// installed commit-msg hook. Fixed via claude-codename-diff.js's
// splitDiffSections(), which classifies header-vs-hunk-body lines by
// POSITION ('@@'-hunk tracking), never by re-matching a line's own text.
// ---------------------------------------------------------------------------

test('BUG-182: the installed hook REJECTS a fragment-assembled positive whose added line starts with "++" (was silently dropped by the old text-prefix header filter)', () => {
  withTempRepo((dir) => {
    initRepo(dir);
    // Content itself begins with two literal '+' characters, so
    // `git diff --unified=0` emits "+++<ABBR>..." for this line — textually
    // identical to a '+++ b/<path>' diff header, but structurally hunk-body
    // content (it follows the '@@' marker, not in the file-header region).
    writeAndStage(dir, 'trick.txt', `++${ABBR} trick\n`);
    const before = commitCount(dir);
    const bad = git(dir, ['commit', '-m', 'trick commit']);
    assert.notEqual(bad.status, 0, 'expected the "++"-prefixed added line to be rejected, not silently dropped');
    assert.match(bad.stderr || '', /CODENAME/);
    assert.equal(commitCount(dir), before, 'no commit object should have been created');
  });
});

test('BUG-182: scanStagedDiff() source-level — the "++"-prefixed content line is present in stagedAddedLines(), not dropped as a header', () => {
  withTempRepo((dir) => {
    initRepo(dir);
    writeAndStage(dir, 'trick.txt', `++${ABBR} trick\n`);
    const cwd = process.cwd();
    try {
      process.chdir(dir);
      delete require.cache[require.resolve('./claude-codename-content-scan.js')];
      const freshScan = require('./claude-codename-content-scan.js');
      const added = freshScan.stagedAddedLines();
      assert.match(added, new RegExp(`\\+\\+${ABBR}`), `expected the "++"-prefixed line to survive as added content, got: ${JSON.stringify(added)}`);
    } finally {
      process.chdir(cwd);
    }
  });
});

// ---------------------------------------------------------------------------
// BUG-183 regression: the enforcing commit-msg content scan used to inspect
// added hunk-body content only, never the diff's per-file path-header lines
// — so a forbidden pattern living ONLY in a new/renamed/copied file's PATH,
// with clean body content, committed cleanly straight through the real
// installed hook. The sibling PreToolUse claude-codename-guard.js already
// scanned this surface (BUG-137); this closes the same gap in the enforcing
// layer by scanning splitDiffSections()'s pathHeaderLines here too, via the
// same shared pattern module (GR#3) — see claude-codename-content-scan.js's
// stagedPathHeaderLines().
// ---------------------------------------------------------------------------

test('BUG-183: the installed hook REJECTS a new file whose PATH (not body) carries a fragment-assembled positive, clean body content', () => {
  withTempRepo((dir) => {
    initRepo(dir);
    writeAndStage(dir, `${ABBR}-notes.txt`, 'clean body content, nothing forbidden here\n');
    const before = commitCount(dir);
    const bad = git(dir, ['commit', '-m', 'x']);
    assert.notEqual(bad.status, 0, 'expected the forbidden filename to be rejected even though the body is clean');
    assert.match(bad.stderr || '', /CODENAME/);
    assert.equal(commitCount(dir), before, 'no commit object should have been created');
  });
});

test('BUG-183: scanStagedDiff() source-level — a forbidden pattern in a staged file PATH is present in stagedPathHeaderLines() and surfaces as a hit', () => {
  withTempRepo((dir) => {
    initRepo(dir);
    writeAndStage(dir, `${ABBR}-notes.txt`, 'clean body content\n');
    const cwd = process.cwd();
    try {
      process.chdir(dir);
      delete require.cache[require.resolve('./claude-codename-content-scan.js')];
      const freshScan = require('./claude-codename-content-scan.js');
      const pathHeaders = freshScan.stagedPathHeaderLines();
      assert.match(pathHeaders, new RegExp(ABBR), `expected the forbidden filename to survive as a path header line, got: ${JSON.stringify(pathHeaders)}`);
      const hits = freshScan.scanStagedDiff();
      assert.ok(hits.length > 0, 'expected scanStagedDiff() to report at least one hit for the path-only violation');
    } finally {
      process.chdir(cwd);
    }
  });
});

test('BUG-183: a clean rename (no forbidden content in old path, new path, or body) is still accepted (no false positive introduced by path scanning)', () => {
  withTempRepo((dir) => {
    initRepo(dir);
    writeAndStage(dir, 'original.txt', 'ordinary technical prose\n');
    gitOk(dir, ['commit', '-m', 'seed']);
    gitOk(dir, ['mv', 'original.txt', 'renamed.txt']);
    gitOk(dir, ['add', '-A']);
    const before = commitCount(dir);
    const r = git(dir, ['commit', '-m', 'clean rename']);
    assert.equal(r.status, 0, r.stderr);
    assert.equal(commitCount(dir), before + 1);
  });
});

test('AC-3: clean staged content is accepted — zero exit AND exactly one new commit (the check can also pass)', () => {
  withTempRepo((dir) => {
    initRepo(dir);
    writeAndStage(dir, 'notes.txt', 'ordinary technical prose, nothing forbidden here\n');
    const before = commitCount(dir);
    const r = git(dir, ['commit', '-m', 'x']);
    assert.equal(r.status, 0, r.stderr);
    assert.equal(commitCount(dir), before + 1);
  });
});

test('AC-3 false-pass guard: a commit MESSAGE containing an unrelated flagged-sounding word ("blocked"), with clean staged content, is accepted (rejects a grep-on-message implementation)', () => {
  withTempRepo((dir) => {
    initRepo(dir);
    writeAndStage(dir, 'notes.txt', 'ordinary technical prose, nothing forbidden here\n');
    const before = commitCount(dir);
    const r = git(dir, ['commit', '-m', 'this message literally contains the word blocked and must still pass']);
    assert.equal(r.status, 0, r.stderr);
    assert.equal(commitCount(dir), before + 1);
  });
});

// ---------------------------------------------------------------------------
// AC-4/AC-5: pattern sharing — one source, mechanically enforced.
// ---------------------------------------------------------------------------

test('AC-4: both claude-codename-guard.js and githooks/commit-msg resolve the SAME shared pattern module path', () => {
  const guardSrc = fs.readFileSync(path.join(__dirname, 'claude-codename-guard.js'), 'utf8');
  const scanSrc = fs.readFileSync(path.join(__dirname, 'claude-codename-content-scan.js'), 'utf8');
  assert.match(guardSrc, /require\('\.\/claude-codename-patterns\.js'\)/);
  assert.match(scanSrc, /require\('\.\/claude-codename-patterns\.js'\)/);
});

test('AC-4: a change observed through the shared module\'s exported pattern list is observed IDENTICALLY through both the guard\'s and the content-scan\'s entry points (one codepath, not two copies)', () => {
  const before = patterns.PATTERNS.slice();
  try {
    // Emptying the shared array (a live reference, not a copy) — both
    // consumers read the SAME array object, so this proves there is no
    // second, independently-defined pattern list backing either one.
    patterns.PATTERNS.length = 0;

    const hits = contentScan.stagedAddedLines; // sanity: module still loads
    assert.equal(typeof hits, 'function');

    const scanHits = [];
    patterns.scan(`a reference to ${ABBR} here`, 'test', scanHits);
    assert.equal(scanHits.length, 0, 'expected the shared scan() to find nothing once PATTERNS is emptied');

    const guard = require('./claude-codename-guard.js');
    assert.equal(guard.PATTERNS.length, 0, 'expected claude-codename-guard.js\'s re-exported PATTERNS to be the SAME emptied array, not an independent copy');
  } finally {
    patterns.PATTERNS.length = 0;
    for (const p of before) patterns.PATTERNS.push(p);
  }
});

test('AC-4 combined check: "module broken" (throws) is distinguished from "module says nothing forbidden" (empty result) — the former denies, the latter allows', () => {
  withTempRepo((dir) => {
    initRepo(dir);
    writeAndStage(dir, 'notes.txt', `a reference to ${ABBR} here\n`);
    const before = commitCount(dir);

    // "says nothing forbidden": force the installed copy's shared module to
    // export an empty pattern list for this one invocation via a forced
    // monkeypatch is impractical across a subprocess boundary, so this half
    // is proven at the unit level in the test immediately above (an empty
    // PATTERNS allows). This half proves the OTHER direction: "broken"
    // (forced throw) denies, via the real subprocess contract.
    const forced = git(dir, ['commit', '-m', 'x'], { CLAUDE_CODENAME_SCAN_FORCE_ERROR: '1' });
    assert.notEqual(forced.status, 0, 'expected a forced internal error to deny the commit (fail-closed), not silently allow it');
    assert.equal(commitCount(dir), before, 'no commit object should have been created when the scan module is broken');
  });
});

test('AC-5: neither claude-codename-guard.js nor claude-codename-content-scan.js independently defines a `new RegExp(` outside the shared-module require (no dormant duplicate pattern list)', () => {
  for (const file of ['claude-codename-guard.js', 'claude-codename-content-scan.js']) {
    const src = fs.readFileSync(path.join(__dirname, file), 'utf8');
    assert.doesNotMatch(src, /new RegExp\(/, `${file} must not independently construct any pattern regex — only claude-codename-patterns.js may`);
  }
});

// ---------------------------------------------------------------------------
// AC-6: the retained PreToolUse guard's three scan surfaces (command text,
// branch name, staged diff) behave identically post-extraction. Command text
// and staged diff are already exercised end-to-end by
// claude-codename-guard.test.js's own BUG-123/137/140/144 suite (21 tests,
// all passing post-extraction). Branch name is added here.
// ---------------------------------------------------------------------------

test('AC-6: the guard still DENIES via the BRANCH NAME surface post-extraction (fragment-assembled positive branch name, clean content)', () => {
  const ROOT = __dirname;
  const fixtureDir = path.join(ROOT, '__codenamehook_e2e_fixture_ac6_branch__');
  fs.rmSync(fixtureDir, { recursive: true, force: true });
  fs.mkdirSync(fixtureDir);
  const gitDir = fs.mkdtempSync(path.join(os.tmpdir(), 'codenamehook-branch-index-'));
  try {
    const gitEnv = { ...process.env, GIT_DIR: path.join(gitDir, '.git'), GIT_WORK_TREE: ROOT };
    let r = spawnSync('git', ['init', '-q', '-b', 'main'], { cwd: ROOT, env: gitEnv, encoding: 'utf8' });
    assert.equal(r.status, 0, `throwaway git init failed: ${r.stderr}`);
    spawnSync('git', ['config', 'user.email', 'test@example.com'], { cwd: ROOT, env: gitEnv });
    spawnSync('git', ['config', 'user.name', 'Test'], { cwd: ROOT, env: gitEnv });

    // `git rev-parse --abbrev-ref HEAD` (which the guard's branch-name scan
    // depends on) fails on an UNBORN HEAD — a real commit is needed first so
    // HEAD resolves, otherwise the branch scan is silently skipped for a
    // reason unrelated to the pattern under test.
    fs.writeFileSync(path.join(fixtureDir, 'seed.txt'), 'seed\n', 'utf8');
    r = spawnSync('git', ['add', '--', fixtureDir], { cwd: ROOT, env: gitEnv, encoding: 'utf8' });
    assert.equal(r.status, 0, `git add (seed) failed: ${r.stderr}`);
    r = spawnSync('git', ['commit', '-m', 'seed'], { cwd: ROOT, env: gitEnv, encoding: 'utf8' });
    assert.equal(r.status, 0, `seed commit failed: ${r.stderr}`);

    r = spawnSync('git', ['checkout', '-b', `feature-${ABBR}-work`], { cwd: ROOT, env: gitEnv, encoding: 'utf8' });
    assert.equal(r.status, 0, `checkout of forbidden branch name failed: ${r.stderr}`);

    fs.writeFileSync(path.join(fixtureDir, 'clean.txt'), 'nothing suspicious here\n', 'utf8');
    r = spawnSync('git', ['add', '--', fixtureDir], { cwd: ROOT, env: gitEnv, encoding: 'utf8' });
    assert.equal(r.status, 0, `git add failed: ${r.stderr}`);

    const result = spawnSync(process.execPath, [path.join(ROOT, 'claude-codename-guard.js')], {
      cwd: ROOT,
      encoding: 'utf8',
      env: gitEnv,
      input: JSON.stringify({ tool: 'Bash', tool_input: { command: 'git commit -m "clean content, forbidden branch name"' } }),
    });
    let denied = false;
    let reason = '';
    if (result.stdout) {
      try {
        const parsed = JSON.parse(result.stdout);
        denied = parsed?.hookSpecificOutput?.permissionDecision === 'deny';
        reason = parsed?.hookSpecificOutput?.permissionDecisionReason || '';
      } catch {
        /* not JSON — treat as not denied */
      }
    }
    assert.equal(denied, true, `expected the guard to deny based on a forbidden branch name alone. raw stdout: ${result.stdout}`);
    assert.match(reason, /branch name/);
  } finally {
    fs.rmSync(fixtureDir, { recursive: true, force: true });
    fs.rmSync(gitDir, { recursive: true, force: true });
  }
});

// ---------------------------------------------------------------------------
// AC-7: fail-closed on the codename scan's own internal error.
// ---------------------------------------------------------------------------

test('AC-7: a forced internal error in the codename scan makes the hook fail CLOSED — non-zero exit, no commit created, even with clean content and a sanctioned identity', () => {
  withTempRepo((dir) => {
    initRepo(dir);
    writeAndStage(dir, 'notes.txt', 'ordinary technical prose, nothing forbidden here\n');
    const before = commitCount(dir);
    const r = git(dir, ['commit', '-m', 'x'], { CLAUDE_CODENAME_SCAN_FORCE_ERROR: '1' });
    assert.notEqual(r.status, 0, 'expected the hook to fail closed when the codename scan module throws');
    assert.equal(commitCount(dir), before, 'no commit object should have been created');
  });
});

test('AC-7: the hook header states the fail-closed posture for the codename scan explicitly (grep floor)', () => {
  const src = fs.readFileSync(path.join(__dirname, 'githooks', 'commit-msg'), 'utf8');
  assert.match(src, /FAIL-CLOSED/);
  assert.match(src, /claude-author-guard\.js/);
  const scanSrc = fs.readFileSync(path.join(__dirname, 'claude-codename-content-scan.js'), 'utf8');
  assert.match(scanSrc, /FAIL-CLOSED/);
});

// ---------------------------------------------------------------------------
// AC-8: the two checks coexist at the same commit-msg slot — all three
// combined-installed-state outcomes proven independently.
// ---------------------------------------------------------------------------

test('AC-8(a): sanctioned identity + clean content is allowed', () => {
  withTempRepo((dir) => {
    initRepo(dir);
    writeAndStage(dir, 'notes.txt', 'ordinary technical prose, nothing forbidden here\n');
    const before = commitCount(dir);
    const r = git(dir, ['commit', '-m', 'x']);
    assert.equal(r.status, 0, r.stderr);
    assert.equal(commitCount(dir), before + 1);
  });
});

test('AC-8(b): sanctioned identity + forbidden content is denied by the CODENAME check (identity check does not swallow it)', () => {
  withTempRepo((dir) => {
    initRepo(dir);
    writeAndStage(dir, 'notes.txt', `a reference to ${ABBR} here\n`);
    const before = commitCount(dir);
    const r = git(dir, ['commit', '-m', 'x']);
    assert.notEqual(r.status, 0);
    assert.match(r.stderr || '', /CODENAME/);
    assert.equal(commitCount(dir), before);
  });
});

test('AC-8(c): unsanctioned identity + clean content is denied by the IDENTITY check (installing the codename scan did not silently drop identity checking)', () => {
  withTempRepo((dir) => {
    initRepo(dir);
    writeAndStage(dir, 'notes.txt', 'ordinary technical prose, nothing forbidden here\n');
    const before = commitCount(dir);
    const r = git(dir, ['commit', '-m', 'x', '--author', `${FABRICATED_NAME} <${FABRICATED_EMAIL}>`], {
      GIT_COMMITTER_NAME: FABRICATED_NAME,
      GIT_COMMITTER_EMAIL: FABRICATED_EMAIL,
    });
    assert.notEqual(r.status, 0);
    assert.match(r.stderr || '', /IDENTITY/);
    assert.equal(commitCount(dir), before);
  });
});

// ---------------------------------------------------------------------------
// AC-9(b): negative control — ordinary technical prose superficially similar
// to a forbidden fragment must pass (mirrors the guard's own documented
// ambiguity handling: the bare two-letter abbreviation, on its own, is not
// matched).
// ---------------------------------------------------------------------------

test('AC-9(b): ordinary prose containing the bare two-letter abbreviation ALONE (no digit) is not flagged — negative control on the staged-diff surface', () => {
  withTempRepo((dir) => {
    initRepo(dir);
    // The bare two-letter abbreviation, with no trailing digit, inside
    // ordinary prose — the guard's own documented non-match (only the
    // abbreviation immediately followed by a "1" or "2" digit matches;
    // see claude-codename-patterns.js's PATTERNS entry for the numbered
    // abbreviation).
    writeAndStage(dir, 'notes.txt', 'the CS department reviewed this change and it looks fine\n');
    const before = commitCount(dir);
    const r = git(dir, ['commit', '-m', 'x']);
    assert.equal(r.status, 0, `expected the bare abbreviation-lookalike to pass cleanly. stderr: ${r.stderr}`);
    assert.equal(commitCount(dir), before + 1);
  });
});

// ---------------------------------------------------------------------------
// AC-10/AC-11: disclosed limitations, named explicitly in the header text —
// grep floor (reviewed by eye per the acceptance file's own check text).
// ---------------------------------------------------------------------------

test('AC-10: the hook header names cherry-pick, revert, and am by name, citing ASM-386', () => {
  const src = fs.readFileSync(path.join(__dirname, 'githooks', 'commit-msg'), 'utf8');
  assert.match(src, /cherry-pick/);
  assert.match(src, /revert/);
  assert.match(src, /\bam\b/);
  assert.match(src, /ASM-386/);
});

test('AC-11: the hook header names the editor-composed commit-message-body gap explicitly (neither layer covers it)', () => {
  const src = fs.readFileSync(path.join(__dirname, 'githooks', 'commit-msg'), 'utf8');
  assert.match(src, /message body|MESSAGE BODY/i);
  const scanSrc = fs.readFileSync(path.join(__dirname, 'claude-codename-content-scan.js'), 'utf8');
  assert.match(scanSrc, /message body|MESSAGE BODY/i);
});

// ---------------------------------------------------------------------------
// BUG-416: npm lockfile integrity-hash exemption — integrity-hash-shaped
// lines are skipped ONLY in known lockfile basenames (package-lock.json,
// npm-shrinkwrap.json, yarn.lock, pnpm-lock.yaml), preventing false positives
// from machine-generated hashes. In other files, integrity-hash-shaped lines
// are scanned normally (a crafted line carrying the forbidden token is caught).
// ---------------------------------------------------------------------------

test('BUG-416 test 1: a fake integrity-hash line carrying the numbered abbreviation in a NON-lockfile (.ts/.go/.md) is FLAGGED', () => {
  withTempRepo((dir) => {
    initRepo(dir);
    // Construct a line that LOOKS like an integrity hash but is in a TypeScript
    // file (not a lockfile). The forbidden token is embedded in the base64
    // portion. The line format matches NPM_INTEGRITY_HASH_RE but the file is
    // not a known lockfile, so it should be scanned and flagged.
    const fakeHashLine = `  "integrity": "sha512-${ABBR}abcdefghijklmnopqrstuvwxyz1234567890/+/=",\n`;
    writeAndStage(dir, 'src.ts', fakeHashLine);
    const before = commitCount(dir);
    const r = git(dir, ['commit', '-m', 'fake integrity in ts file']);
    assert.notEqual(r.status, 0, 'expected the forbidden token in a non-lockfile to be flagged, even if the line looks like an integrity hash');
    assert.match(r.stderr || '', /CODENAME/);
    assert.equal(commitCount(dir), before, 'no commit object should have been created');
  });
});

test('BUG-416 test 2: a genuine integrity-hash line in package-lock.json with the numbered abbreviation by chance is SKIPPED', () => {
  withTempRepo((dir) => {
    initRepo(dir);
    // A real package-lock.json file with an integrity-hash line containing
    // the forbidden token by chance (simulating the tsx/esbuild scenario from
    // BUG-416). The file is named exactly "package-lock.json" so it matches
    // the known lockfile basename. The line should be skipped during scanning.
    const lockFileContent = `{
  "packages": {
    "node_modules/tsx": {
      "integrity": "sha512-${ABBR}abcdefghijklmnopqrstuvwxyz1234567890/+/=",
      "name": "tsx"
    }
  }
}
`;
    writeAndStage(dir, 'package-lock.json', lockFileContent);
    const before = commitCount(dir);
    const r = git(dir, ['commit', '-m', 'add tsx to lock file']);
    assert.equal(r.status, 0, `expected integrity hash in package-lock.json to be skipped. stderr: ${r.stderr}`);
    assert.equal(commitCount(dir), before + 1, 'the commit should succeed because the integrity hash line is skipped in lockfiles');
  });
});

test('BUG-416 test 3: basename exactness — package-lock.json.bak and mypackage-lock.json are NOT treated as lockfiles (FLAGGED)', () => {
  withTempRepo((dir) => {
    initRepo(dir);
    // A file named "package-lock.json.bak" (not an exact match) should NOT
    // be treated as a lockfile, so the forbidden token should be flagged.
    const fakeHashLine = `  "integrity": "sha512-${ABBR}abcdefghijklmnopqrstuvwxyz1234567890/+/=",\n`;
    writeAndStage(dir, 'package-lock.json.bak', fakeHashLine);
    const before = commitCount(dir);
    const r = git(dir, ['commit', '-m', 'fake integrity in bak file']);
    assert.notEqual(r.status, 0, 'expected the forbidden token in package-lock.json.bak to be flagged (not a known lockfile basename)');
    assert.match(r.stderr || '', /CODENAME/);
    assert.equal(commitCount(dir), before, 'no commit object should have been created');
  });
});

test('BUG-416 test 3b: mypackage-lock.json (wrong prefix) is also NOT treated as a lockfile', () => {
  withTempRepo((dir) => {
    initRepo(dir);
    // A file named "mypackage-lock.json" (basename doesn't match exactly)
    // should NOT be treated as a lockfile.
    const fakeHashLine = `  "integrity": "sha512-${ABBR}abcdefghijklmnopqrstuvwxyz1234567890/+/=",\n`;
    writeAndStage(dir, 'mypackage-lock.json', fakeHashLine);
    const before = commitCount(dir);
    const r = git(dir, ['commit', '-m', 'fake integrity in mypackage-lock.json']);
    assert.notEqual(r.status, 0, 'expected the forbidden token in mypackage-lock.json to be flagged (basename must match exactly)');
    assert.match(r.stderr || '', /CODENAME/);
    assert.equal(commitCount(dir), before, 'no commit object should have been created');
  });
});

test('BUG-416 test 4: ordinary prose with the numbered abbreviation is still FLAGGED (integrity-hash shape alone does not exempt a line)', () => {
  withTempRepo((dir) => {
    initRepo(dir);
    // A line that is NOT an integrity hash but contains the forbidden token
    // should always be flagged, regardless of what file it's in.
    const ordinaryProse = `This is a comment about ${ABBR} in the code.\n`;
    writeAndStage(dir, 'package-lock.json', ordinaryProse);
    const before = commitCount(dir);
    const r = git(dir, ['commit', '-m', 'ordinary prose in lock file']);
    assert.notEqual(r.status, 0, 'expected ordinary prose with the forbidden token to be flagged, even in a lockfile');
    assert.match(r.stderr || '', /CODENAME/);
    assert.equal(commitCount(dir), before, 'no commit object should have been created');
  });
});
