/**
 * claude-destructive-guard.test.js — unit + end-to-end tests for
 * claude-destructive-guard.js (FEAT-040, tool.destructiveguard, GR#23) and
 * the claude-bow.js additions it depends on (recordDestructiveVerdict,
 * latestDestructiveVerdict, `destructive`/`verdict` CLI commands).
 *
 * All end-to-end guard cases run against THROWAWAY git repos created under
 * the OS temp dir (never this repo's own working tree/index — see
 * claude-destructive-guard.js's header, ASM-destructiveguard-root-via-cwd:
 * many agents are concurrently active in this repo, so staging/unstaging
 * real files in the shared index the way claude-secret-guard.test.js does
 * would be a real collision hazard here). All BOW-backed cases create their
 * own throwaway bow_items rows (unique mkey/code per test run) directly
 * against the real local metro MariaDB and always delete them in a
 * `finally` (cascades to bow_destructive_verdicts via the FK).
 *
 * Run: node --test claude-destructive-guard.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');
const crypto = require('crypto');
const { spawnSync } = require('child_process');
const mysql = require('mysql2/promise');

const ROOT = __dirname;
const GUARD_PATH = path.join(ROOT, 'claude-destructive-guard.js');
const BOW_PATH = path.join(ROOT, 'claude-bow.js');

const guard = require('./claude-destructive-guard.js');
const bow = require('./claude-bow.js');
const authorGuard = require('./claude-author-guard.js');
const {
  extractMessage,
  extractTags,
  looksLikeRealTag,
  isEnforcedDirPath,
  isRootLevel,
  deriveRootGuardScripts,
  noTagDenyMessage,
  verdictDenyMessage,
  isCommitInvocation,
  getCommitInvocation,
  isExemptFile,
  isExemptFileSet,
  isArgvClassifiable,
  isExemptCommit,
  findGitAddInvocations,
  classifyAddArgs,
  computeAddedPathsOrAmbiguous,
  ambiguousAddDenyMessage,
  failClosedSweep,
  unparseableGitDenyMessage,
  shellEscapeAliasDenyMessage,
  unknownGitVerbDenyMessage,
} = guard;
const { recordDestructiveVerdict, latestDestructiveVerdict, findItemByRef } = bow;

// ---------------------------------------------------------------------------
// DB helpers
// ---------------------------------------------------------------------------

function connectDb() {
  return mysql.createConnection({
    host: process.env.METRO_DB_HOST || '127.0.0.1',
    port: Number(process.env.METRO_DB_PORT || 3306),
    user: process.env.METRO_DB_USER || 'root',
    password: process.env.METRO_DB_PASSWORD || '',
    database: process.env.METRO_DB_NAME || 'metro',
  });
}

/** Insert a throwaway bow_items row directly (feature type), unique per call. */
async function createFixtureItem(db, label) {
  const suffix = crypto.randomBytes(4).toString('hex');
  const guid = crypto.randomUUID();
  // BUG-152: the code must be shaped like a REAL production BOW code
  // (TYPE_PREFIX + "-" + a purely-numeric id, exactly what claude-bow.js's
  // nextCode() generates) so it survives claude-destructive-guard.js's new
  // looksLikeRealTag() shape filter and actually reaches BOW resolution —
  // the old "FEAT-T<hex>" shape mixed letters into the suffix and would now
  // be silently dropped as prose before ever hitting the DB. A large random
  // 32-bit number keeps this collision-free against the slowly-incrementing
  // sequential codes nextCode() produces, without needing letters at all.
  const code = `FEAT-${crypto.randomBytes(4).readUInt32BE(0)}`;
  const mkey = `test.destructiveguard.${label}.${suffix}`;
  await db.query(
    'INSERT INTO bow_items (guid, code, mkey, item_type, title, priority) VALUES (?, ?, ?, ?, ?, ?)',
    [guid, code, mkey, 'feature', `TEMP fixture — ${label} (${suffix})`, 'P3']);
  return { guid, code, mkey };
}

async function deleteFixtureItem(db, guid) {
  await db.query('DELETE FROM bow_items WHERE guid = ?', [guid]);
}

// ---------------------------------------------------------------------------
// Git/repo fixture helpers
// ---------------------------------------------------------------------------

function git(cwd, args) {
  const r = spawnSync('git', args, { cwd, encoding: 'utf8' });
  if (r.status !== 0) throw new Error(`git ${args.join(' ')} failed: ${r.stderr}`);
  return r.stdout.trim();
}

function defaultSettingsJson() {
  return JSON.stringify({
    hooks: {
      PreToolUse: [
        {
          matcher: 'Bash',
          hooks: [
            { type: 'command', command: 'node fixture-wired-guard.js' },
          ],
        },
      ],
    },
  });
}

function withTempRepo(fn, { settingsJson = defaultSettingsJson() } = {}) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'destructive-guard-fixture-'));
  try {
    git(dir, ['init', '-b', 'main']);
    git(dir, ['config', 'user.name', 'Fixture Contributor']);
    git(dir, ['config', 'user.email', 'fixture@example.invalid']);
    if (settingsJson !== null) {
      fs.mkdirSync(path.join(dir, '.claude'), { recursive: true });
      fs.writeFileSync(path.join(dir, '.claude', 'settings.json'), settingsJson, 'utf8');
      git(dir, ['add', '.claude/settings.json']);
      git(dir, ['commit', '-m', 'seed settings.json']);
    }
    return fn(dir);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

function stageFile(dir, relPath, content = 'x\n') {
  const full = path.join(dir, relPath);
  fs.mkdirSync(path.dirname(full), { recursive: true });
  fs.writeFileSync(full, content, 'utf8');
  git(dir, ['add', relPath]);
}

/** Invoke the guard exactly as the PreToolUse hook would. */
function runGuard(cwd, command, envOverrides = {}) {
  const payload = JSON.stringify({ tool: 'Bash', tool_input: { command } });
  const r = spawnSync(process.execPath, [GUARD_PATH], {
    cwd,
    input: payload,
    encoding: 'utf8',
    env: { ...process.env, ...envOverrides },
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
// Unit: extractMessage / extractTags (AC-13)
// ---------------------------------------------------------------------------

// BUG-164: the real TYPE_PREFIX set, sourced from claude-bow.js itself
// (GR#15 — never a second, independently-typed prefix list in this test
// file either) — every unit test below threads this through exactly as
// production main() does via loadDependencies()'s typePrefixes.
const REAL_TYPE_PREFIXES = new Set(Object.values(bow.TYPE_PREFIX));

test('extractMessage / extractTags — single and multi-tag messages', () => {
  assert.equal(extractMessage('git commit -m "[FEAT-040] hello"'), '[FEAT-040] hello');
  assert.deepEqual(extractTags('[FEAT-040] hello', REAL_TYPE_PREFIXES), ['FEAT-040']);
  assert.deepEqual(extractTags('[tool.destructiveguard] and [FEAT-040] both', REAL_TYPE_PREFIXES), ['tool.destructiveguard', 'FEAT-040']);
  assert.equal(extractMessage('git commit -F msgfile.txt'), null);
});

// ---------------------------------------------------------------------------
// BUG-152: bracketed prose (Go generics, quoted literals) must not be
// mistaken for a claimed [mkey]/[CODE] tag.
// ---------------------------------------------------------------------------

test('BUG-152: looksLikeRealTag accepts real mkey/CODE shapes, rejects the three reported false positives', () => {
  // Real shapes must still be recognised — no regression to the guard's
  // actual purpose.
  assert.ok(looksLikeRealTag('tool.destructiveguard', REAL_TYPE_PREFIXES));
  assert.ok(looksLikeRealTag('engine.core', REAL_TYPE_PREFIXES));
  assert.ok(looksLikeRealTag('data.modes-naming', REAL_TYPE_PREFIXES)); // hyphenated segment, real mkey on disk
  assert.ok(looksLikeRealTag('ui.screen.build', REAL_TYPE_PREFIXES));   // three segments, real mkey on disk
  assert.ok(looksLikeRealTag('FEAT-040', REAL_TYPE_PREFIXES));
  assert.ok(looksLikeRealTag('BUG-152', REAL_TYPE_PREFIXES));
  assert.ok(looksLikeRealTag('MOD-001', REAL_TYPE_PREFIXES));

  // The three reported false positives must NOT be treated as tags.
  assert.ok(!looksLikeRealTag('T,PT', REAL_TYPE_PREFIXES));          // from `Store[T,PT]`
  assert.ok(!looksLikeRealTag('Screen', REAL_TYPE_PREFIXES));         // from `atomic.Pointer[Screen]`
  assert.ok(!looksLikeRealTag('REDACTED-GR22', REAL_TYPE_PREFIXES));  // literal marker string in prose
});

test('BUG-152: extractTags drops bracketed prose that only looks like a tag, in isolation', () => {
  assert.deepEqual(extractTags('fix Store[T,PT] generic constraint handling', REAL_TYPE_PREFIXES), []);
  assert.deepEqual(extractTags('switch atomic.Pointer[Screen] to the new API', REAL_TYPE_PREFIXES), []);
  assert.deepEqual(extractTags('redact the literal [REDACTED-GR22] marker in logs', REAL_TYPE_PREFIXES), []);
});

test('BUG-152: extractTags extracts only the real tag when mixed in the same message as false-positive-shaped brackets', () => {
  assert.deepEqual(
    extractTags('[engine.core] fix Store[T,PT] generic constraint handling', REAL_TYPE_PREFIXES),
    ['engine.core']
  );
  assert.deepEqual(
    extractTags('switch atomic.Pointer[Screen] to the new API [FEAT-040]', REAL_TYPE_PREFIXES),
    ['FEAT-040']
  );
  assert.deepEqual(
    extractTags('[tool.destructiveguard] redact the literal [REDACTED-GR22] marker, also touches Store[T,PT]', REAL_TYPE_PREFIXES),
    ['tool.destructiveguard']
  );
});

// ---------------------------------------------------------------------------
// BUG-164: WORD-digit technical abbreviations in prose ([UTF-8], [RFC-2119],
// [SHA-256], [ISO-8601]) satisfy CODE_SHAPE_RE by accident and must not be
// treated as claimed BOW tags — only a CODE-shaped span whose prefix is a
// REAL claude-bow.js TYPE_PREFIX value is a genuine claim.
// ---------------------------------------------------------------------------

test('BUG-164: looksLikeRealTag rejects CODE-shaped prose abbreviations whose prefix is not a real TYPE_PREFIX', () => {
  assert.ok(!looksLikeRealTag('UTF-8', REAL_TYPE_PREFIXES));
  assert.ok(!looksLikeRealTag('RFC-2119', REAL_TYPE_PREFIXES));
  assert.ok(!looksLikeRealTag('SHA-256', REAL_TYPE_PREFIXES));
  assert.ok(!looksLikeRealTag('ISO-8601', REAL_TYPE_PREFIXES));

  // A genuine-shaped but nonexistent-item code must still be treated as a
  // CLAIMED tag (real prefix, purely-numeric id) — BOW resolution, not the
  // shape filter, is what must reject FEAT-999 as unresolvable. The fix
  // must not swing so far that it starts ignoring genuine-looking-but-
  // nonexistent BOW codes.
  assert.ok(looksLikeRealTag('FEAT-999', REAL_TYPE_PREFIXES));
});

test('BUG-164: extractTags drops prose abbreviations shaped like BOW codes, in isolation', () => {
  assert.deepEqual(extractTags('normalize [UTF-8] BOM handling before parsing', REAL_TYPE_PREFIXES), []);
  assert.deepEqual(extractTags('cite [RFC-2119] keyword semantics in the doc', REAL_TYPE_PREFIXES), []);
  assert.deepEqual(extractTags('verify the [SHA-256] checksum matches', REAL_TYPE_PREFIXES), []);
  assert.deepEqual(extractTags('dates now follow [ISO-8601] formatting', REAL_TYPE_PREFIXES), []);
});

test('BUG-164: extractTags extracts only the real tag when a prose abbreviation appears alongside it in the same message', () => {
  assert.deepEqual(
    extractTags('[FEAT-040] normalize [UTF-8] BOM handling before parsing', REAL_TYPE_PREFIXES),
    ['FEAT-040']
  );
  assert.deepEqual(
    extractTags('cite [RFC-2119] keyword semantics [FEAT-040], also touches [SHA-256] hashing and [ISO-8601] dates', REAL_TYPE_PREFIXES),
    ['FEAT-040']
  );
});

// ---------------------------------------------------------------------------
// Unit: isEnforcedDirPath / isRootLevel
// ---------------------------------------------------------------------------

test('isEnforcedDirPath covers cmd/internal/data/tools and excludes docs', () => {
  assert.ok(isEnforcedDirPath('internal/engine/world/foo.go'));
  assert.ok(isEnforcedDirPath('cmd/metro/main.go'));
  assert.ok(isEnforcedDirPath('data/errors.json'));
  assert.ok(isEnforcedDirPath('tools/plan/generate.js'));
  assert.ok(!isEnforcedDirPath('docs/planning/foo.md'));
  assert.ok(!isEnforcedDirPath('README.md'));
});

test('isRootLevel is true only for paths with no directory component', () => {
  assert.ok(isRootLevel('claude-bow.js'));
  assert.ok(!isRootLevel('internal/foo.go'));
  assert.ok(!isRootLevel('a/b'));
});

// ---------------------------------------------------------------------------
// Unit: deriveRootGuardScripts (AC-11) — data-derived, not a name pattern
// ---------------------------------------------------------------------------

test('deriveRootGuardScripts pulls .js filenames from hooks[*].hooks[*].command, any event type', () => {
  withTempRepo((dir) => {
    const cwd = process.cwd();
    try {
      process.chdir(dir);
      const scripts = deriveRootGuardScripts();
      assert.ok(scripts.has('fixture-wired-guard.js'));
      assert.ok(!scripts.has('unwired-scratch.js'));
    } finally {
      process.chdir(cwd);
    }
  });
});

test('deriveRootGuardScripts throws on missing settings.json (fail-closed consequence, ASM-192 ruling)', () => {
  withTempRepo((dir) => {
    const cwd = process.cwd();
    try {
      process.chdir(dir);
      assert.throws(() => deriveRootGuardScripts());
    } finally {
      process.chdir(cwd);
    }
  }, { settingsJson: null });
});

test('deriveRootGuardScripts throws on malformed settings.json JSON, never falls back to a name pattern', () => {
  withTempRepo((dir) => {
    const cwd = process.cwd();
    try {
      process.chdir(dir);
      assert.throws(() => deriveRootGuardScripts());
    } finally {
      process.chdir(cwd);
    }
  }, { settingsJson: '{ this is not valid json' });
});

// ---------------------------------------------------------------------------
// End-to-end: AC-9 / AC-12 — non-commit and non-enforced-path exemptions
// ---------------------------------------------------------------------------

test('AC-9: a non-commit command is silently allowed with no stdout', () => {
  withTempRepo((dir) => {
    const r = runGuard(dir, 'git status');
    assert.equal(r.denied, false);
    assert.equal(r.stdout, '');
  });
});

test('AC-12: a docs-only staged change is silently allowed, even with a broken settings.json (design: settings.json is only consulted for root-level files)', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'docs/foo.md', '# notes\n');
    const r = runGuard(dir, 'git commit -m "docs update, no tag needed"');
    assert.equal(r.denied, false);
    assert.equal(r.stdout, '');
  }, { settingsJson: '{ broken' });
});

test('AC-10/AC-11: a staged internal/ file activates the gate (deny with no verdict); the identical repo with only a root scratch .js NOT wired in settings.json does not activate', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/foo.go', 'package foo\n');
    // BUG (found by Tester baseline sweep, fixed here): this used to tag the
    // commit "[FEAT-040]" — the destructive guard's OWN real, live BOW item
    // in the shared metro DB, which by design already carries an accepted
    // Destructive verdict (see `node claude-bow.js verdict FEAT-040`, dated
    // 2026-08-12 09:52:47 — recorded precisely so the guard's own commit
    // wouldn't deny itself). Tagging a fixture commit with a real,
    // already-accepted code made this "no verdict recorded" test pass
    // through the ALLOW path instead of the DENY path it claims to prove,
    // silently — every other verdict-lookup-reaching test in this file
    // correctly mints a fresh throwaway `createFixtureItem()` row instead of
    // reusing a real code (see AC-2/AC-3/AC-4/AC-6/AC-7 etc. below); this is
    // the one place that didn't. A random, syntactically-plausible but
    // never-inserted code reproduces the intended "cannot resolve -> still
    // denied" path (the comment's original intent) without needing DB
    // fixture setup/teardown, and can never collide with a real item.
    // BUG-152: the code must still be CODE-shaped (prefix + "-" + purely
    // numeric id) — a fake suffix like "NOSUCH1234" would now be filtered
    // out by looksLikeRealTag() as prose, never reaching resolution at all,
    // which would test the wrong path entirely.
    const unresolvableCode = `FEAT-${crypto.randomBytes(4).readUInt32BE(0)}`;
    const r = runGuard(dir, `git commit -m "[${unresolvableCode}] change"`);
    assert.equal(r.denied, true); // tag does not resolve to any live BOW item -> still a deny path
  });

  withTempRepo((dir) => {
    stageFile(dir, 'unwired-scratch.js', '// not a real guard\n');
    const r = runGuard(dir, 'git commit -m "no tag at all"');
    assert.equal(r.denied, false, 'an unwired root .js file (even if it looks guard-like) must not trigger enforcement purely by name');
    assert.equal(r.stdout, '');
  });

  withTempRepo((dir) => {
    stageFile(dir, 'fixture-wired-guard.js', '// this one IS in settings.json hooks\n');
    const r = runGuard(dir, 'git commit -m "no tag at all"');
    assert.equal(r.denied, true, 'a root .js file actually wired into settings.json hooks must activate the gate');
  });
});

// ---------------------------------------------------------------------------
// End-to-end: AC-16 — the bypass trap
// ---------------------------------------------------------------------------

test('AC-16: a code-bearing commit with ZERO tags is denied (not vacuously allowed)', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/foo.go', 'package foo\n');
    const r = runGuard(dir, 'git commit -m "no bow tag whatsoever, just prose"');
    assert.equal(r.denied, true);
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]/i);
  });
});

test('noTagDenyMessage names the bypass explicitly', () => {
  const msg = noTagDenyMessage();
  assert.match(msg, /GR#23/);
  assert.match(msg, /bypass/i);
});

// ---------------------------------------------------------------------------
// End-to-end: regression — the Tester's bypass reproductions against v1
// (v1's GIT_COMMIT_RE required the literal token `git` followed immediately
// by whitespace, so `git.exe`, a shell-wrapped invocation, and a full PATH
// invocation all sailed past the gate entirely: isCommitInvocation()
// returned false and the ENTIRE rest of this guard's logic — the zero-tag
// check AC-16 exists to prove — never ran. Every case below stages the
// EXACT AC-16 shape (a code-bearing file, a message with NO BOW tag at
// all) through the vector the Tester used, so a false PASS here would mean
// "the tag/verdict checks are fine but the trigger that reaches them is
// bypassable" — the same defect AC-16 already proves does not exist for the
// plain `git commit` spelling. These are proven able to fail: reverting
// isCommitInvocation() to the old GIT_COMMIT_RE (or the old GIT_TOKEN_RE
// without the executable-suffix/path-prefix tolerance, or dropping the
// gatherScanTexts() wrapper recursion) turns every one of these back to
// `denied === false`, red against the pre-fix code, green after.
// ---------------------------------------------------------------------------

test('REGRESSION (Tester finding): `git.exe commit` with zero BOW tags is denied, not silently allowed', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/foo.go', 'package foo\n');
    const r = runGuard(dir, 'git.exe commit -m "no bow tag whatsoever, via git.exe"');
    assert.equal(r.denied, true, 'git.exe must be recognised as a real git commit invocation');
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]/i);
  });
});

test('REGRESSION (Tester finding): `git.cmd commit` with zero BOW tags is denied, not silently allowed', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/foo.go', 'package foo\n');
    const r = runGuard(dir, 'git.cmd commit -m "no bow tag whatsoever, via git.cmd"');
    assert.equal(r.denied, true, 'git.cmd must be recognised as a real git commit invocation');
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]/i);
  });
});

test('REGRESSION (Tester finding): a `bash -c "git commit ..."` shell-wrapped invocation with zero BOW tags is denied', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/foo.go', 'package foo\n');
    const r = runGuard(dir, `bash -c "git commit -m 'no bow tag whatsoever, wrapped in bash -c'"`);
    assert.equal(r.denied, true, 'a git commit hidden inside a bash -c wrapper body must still be recognised');
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]/i);
  });
});

test('REGRESSION (Tester finding): a full PATH invocation of git.exe (unquoted, no spaces) with zero BOW tags is denied', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/foo.go', 'package foo\n');
    const r = runGuard(dir, 'C:\\Git\\bin\\git.exe commit -m "no bow tag whatsoever, via full path"');
    assert.equal(r.denied, true, 'a full-path git.exe invocation must still be recognised as a real git commit');
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]/i);
  });
});

test('REGRESSION (Tester finding, exact repro): a QUOTED full PATH invocation of git.exe — Git for Windows\' actual default install path, which contains a space ("C:\\Program Files\\Git\\bin\\git.exe") — with zero BOW tags is denied', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/foo.go', 'package foo\n');
    const r = runGuard(
      dir,
      '"C:\\Program Files\\Git\\bin\\git.exe" commit -m "no bow tag whatsoever, via quoted full path"'
    );
    assert.equal(r.denied, true, 'a quoted full-path git.exe invocation (the real default Windows install path) must still be recognised as a real git commit');
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]/i);
  });
});

test('REGRESSION: git.exe / git.cmd / a shell-wrapped commit still go through the FULL verdict pipeline (not just the tag check) — an accepted verdict passes, an unresolvable tag is named', async () => {
  const db = await connectDb();
  const item = await createFixtureItem(db, 'exe-suffix-verdict');
  try {
    await recordDestructiveVerdict(db, item.code, { verdict: 'accept', attacker: 'Destructive-Fixture' });

    withTempRepo((dir) => {
      stageFile(dir, 'internal/foo.go', 'package foo\n');
      const rGood = runGuard(dir, `git.exe commit -m "[${item.code}] change via git.exe"`);
      assert.equal(rGood.denied, false, 'git.exe with an accepted verdict must pass, same as plain git');
    });

    withTempRepo((dir) => {
      stageFile(dir, 'internal/foo.go', 'package foo\n');
      // BUG-152: CODE-shaped (prefix + "-" + purely numeric id) so it still
      // reaches BOW resolution instead of being filtered out as prose.
      // BUG-164: the prefix must be a REAL TYPE_PREFIX value (FEAT, not the
      // made-up "ZZZ") or looksLikeRealTag() now drops it as prose before it
      // ever reaches resolution — the id itself stays absurdly large so no
      // real item can ever collide with it.
      const rBad = runGuard(dir, `bash -c "git commit -m '[FEAT-999999999] change'"`);
      assert.equal(rBad.denied, true);
      assert.match(rBad.reason, /FEAT-999999999/, 'the unresolvable tag must still be named even when the invocation was wrapped');
    });
  } finally {
    await deleteFixtureItem(db, item.guid);
    await db.end();
  }
});

test('BUG-164 end-to-end: a commit citing a REAL, accepted-verdict tag whose message also mentions [UTF-8]/[RFC-2119]/[SHA-256]/[ISO-8601] prose is ALLOWED, not denied over the incidental match', async () => {
  const db = await connectDb();
  const item = await createFixtureItem(db, 'bug164-prose-abbrev');
  try {
    await recordDestructiveVerdict(db, item.code, { verdict: 'accept', attacker: 'Destructive-Fixture' });

    withTempRepo((dir) => {
      stageFile(dir, 'internal/foo.go', 'package foo\n');
      const r = runGuard(
        dir,
        `git commit -m "[${item.code}] normalize [UTF-8] BOM handling per [RFC-2119], verify [SHA-256] checksum and [ISO-8601] dates"`
      );
      assert.equal(r.denied, false, 'a fully clean, already-attacked commit must not be blocked by incidental WORD-digit prose');
    });
  } finally {
    await deleteFixtureItem(db, item.guid);
    await db.end();
  }
});

test('REGRESSION (adjacent gap closed by shared machinery, no Tester finding required): a `git commit` inside a QUOTED, non-shell string (prose) is not mistaken for a real invocation', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/foo.go', 'package foo\n');
    // No real `git commit` anywhere in this command — "git commit" only
    // appears as English prose inside an -m message body. Re-scanning
    // wrapper bodies (added by this fix) raises this false-positive surface
    // relative to v1, so it is proven not to have regressed: this must
    // still be treated as an ordinary commit whose message happens to
    // mention the words, i.e. normal AC-16 zero... no, it HAS zero tags, so
    // it is correctly denied by AC-16, but for the right reason (no tag),
    // not because some inner "git commit" text was misparsed as a second
    // invocation with a different, confusing failure mode. We assert only
    // that the guard behaves identically to the plain zero-tag case.
    const r = runGuard(dir, 'git commit -m "reminder: please git commit later, no tag here"');
    assert.equal(r.denied, true);
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]/i);
  });
});

// ---------------------------------------------------------------------------
// ROUND 4 — total bypass: `"git" commit` / `'git' commit` (zero BOW tags,
// healthy dependencies, no overrides) walked straight through round 3's
// isCommitInvocation() because round 3's LOCAL GIT_TOKEN_RE required a path
// separator before "git" inside its quoted alternative
// (`"[^"]*[\\/]git..."`), while claude-author-guard.js's own GIT_TOKEN_RE has
// always made that separator OPTIONAL (`"(?:[^"]*[\\/])?git..."` — the whole
// prefix group is `?`). ASM-360 (filed round 3) predicted exactly this shape
// of drift the same day it was filed. THE FIX (this round): stop
// maintaining a second, hand-copied token regex at all. isCommitInvocation()
// now calls authorGuard.findCommitInvocation() directly — the ONE tested
// recogniser in this repo — and only checks the VERB it resolves to,
// preserving this file's own commit-only scope decision (ASM-193/ASM-359)
// without re-implementing recognition. See claude-destructive-guard.js's
// COMMAND RECOGNITION block for the full account.
// ---------------------------------------------------------------------------

// Reconstruction of round 3's LOCAL GIT_TOKEN_RE + isCommitInvocation, for
// the red-before-green-after proof below ONLY — not live code, never
// imported by claude-destructive-guard.js. Captured verbatim from the file
// as it stood immediately before this round's fix (see git history / Ben's
// brief, which quotes the same two lines). This mirrors the existing
// precedent at the top of the "REGRESSION (Tester finding)" block above
// ("verified by hand against a reconstructed copy of the round-2 file") —
// the same technique, one round later, for the same reason: proving a defect
// was real without needing to check out old code and re-run the whole
// process.
const ROUND3_LOCAL_GIT_TOKEN_RE =
  /(?:^|[;&|(\n])(?:\s*(?:export\s+)?[A-Za-z_][A-Za-z0-9_]*=(?:"[^"]*"|'[^']*'|\S+))*\s*(?:"[^"]*[\\/]git(?:\.(?:exe|cmd))?"|'[^']*[\\/]git(?:\.(?:exe|cmd))?'|(?:[^\s;&|()\n"']*[\\/])?git(?:\.(?:exe|cmd))?)(?=\s)/gi;

function round3IsCommitInvocation(command) {
  const candidates = authorGuard.gatherScanTexts(command, 0);
  for (const text of candidates) {
    ROUND3_LOCAL_GIT_TOKEN_RE.lastIndex = 0;
    const quoteMask = authorGuard.buildQuoteMask(text);
    let m;
    while ((m = ROUND3_LOCAL_GIT_TOKEN_RE.exec(text)) !== null) {
      const priorQuoted = m.index > 0 && quoteMask[m.index - 1];
      if (priorQuoted) continue;
      const inv = authorGuard.parseGitInvocation(text, m.index + m[0].length);
      if (!inv) continue;
      const resolved = authorGuard.resolveAlias(inv.verbWord, 0, new Set());
      if (resolved === 'commit') return true;
    }
  }
  return false;
}

test('ROUND-4 defect repro, RED (pre-fix reconstruction): round 3\'s local GIT_TOKEN_RE does NOT recognise `"git" commit` / `\'git\' commit` as a commit invocation — this is the actual bug, proven able to fail', () => {
  const quotedBare = '"git" commit -m "no bow tag whatsoever"';
  const singleQuotedBare = "'git' commit -m 'no tag, single quoted'";
  const redQuoted = round3IsCommitInvocation(quotedBare);
  const redSingleQuoted = round3IsCommitInvocation(singleQuotedBare);
  console.log(`  [round-4 red] round3IsCommitInvocation(${JSON.stringify(quotedBare)}) = ${redQuoted}`);
  console.log(`  [round-4 red] round3IsCommitInvocation(${JSON.stringify(singleQuotedBare)}) = ${redSingleQuoted}`);
  assert.equal(redQuoted, false, 'this IS the bug: the pre-fix recogniser misses a quoted-bare "git" token entirely');
  assert.equal(redSingleQuoted, false, 'same bug, single-quoted variant');
  // Control: the pre-fix recogniser DOES catch the plain, unquoted spelling —
  // proves the reconstruction is faithful (not simply broken outright) and
  // that the gap is specifically the quoted-bare shape.
  assert.equal(round3IsCommitInvocation('git commit -m "control case"'), true, 'sanity: unquoted plain "git commit" was never broken');
});

test('ROUND-4 defect repro, GREEN (current fix): isCommitInvocation() — now backed by authorGuard.findCommitInvocation() — DOES recognise `"git" commit` / `\'git\' commit`', () => {
  const quotedBare = '"git" commit -m "no bow tag whatsoever"';
  const singleQuotedBare = "'git' commit -m 'no tag, single quoted'";
  const greenQuoted = isCommitInvocation(quotedBare, authorGuard);
  const greenSingleQuoted = isCommitInvocation(singleQuotedBare, authorGuard);
  console.log(`  [round-4 green] isCommitInvocation(${JSON.stringify(quotedBare)}) = ${greenQuoted}`);
  console.log(`  [round-4 green] isCommitInvocation(${JSON.stringify(singleQuotedBare)}) = ${greenSingleQuoted}`);
  assert.equal(greenQuoted, true);
  assert.equal(greenSingleQuoted, true);
});

test('ROUND-4 end-to-end: `"git" commit` with zero BOW tags on a code-bearing file is DENIED, not silently allowed (the exact bypass reproduced against the Tester\'s real repro shape)', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/foo.go', 'package foo\n');
    const r = runGuard(dir, '"git" commit -m "no bow tag whatsoever"');
    assert.equal(r.denied, true, '"git" commit must be recognised as a real git commit invocation');
    assert.notEqual(r.stdout, '', 'a decision payload must be emitted, not empty stdout (the exact symptom reported)');
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]/i);
  });
});

test('ROUND-4 end-to-end: `\'git\' commit` with zero BOW tags on a code-bearing file is DENIED, not silently allowed', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/foo.go', 'package foo\n');
    const r = runGuard(dir, "'git' commit -m 'no tag, single quoted'");
    assert.equal(r.denied, true, "'git' commit must be recognised as a real git commit invocation");
    assert.notEqual(r.stdout, '', 'a decision payload must be emitted, not empty stdout');
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]/i);
  });
});

test('ROUND-4 end-to-end: `\'git\' commit` with an accepted verdict still passes through the FULL pipeline (proves the fix isn\'t "deny everything quoted", it correctly reaches the same verdict check as any other spelling)', async () => {
  const db = await connectDb();
  const item = await createFixtureItem(db, 'quoted-bare-verdict');
  try {
    await recordDestructiveVerdict(db, item.code, { verdict: 'accept', attacker: 'Destructive-Fixture' });
    withTempRepo((dir) => {
      stageFile(dir, 'internal/foo.go', 'package foo\n');
      const r = runGuard(dir, `'git' commit -m '[${item.code}] change via quoted-bare git'`);
      assert.equal(r.denied, false, 'a quoted-bare git invocation with an accepted verdict must pass, same as any other spelling');
    });
  } finally {
    await deleteFixtureItem(db, item.guid);
    await db.end();
  }
});

test('ROUND-4 dependency-free fallback also recognises the quoted-bare shape (looksLikeCommitFallback, used only when loadDependencies() itself has failed)', () => {
  assert.equal(guard.looksLikeCommitFallback('"git" commit -m "x"'), true);
  assert.equal(guard.looksLikeCommitFallback("'git' commit -m 'x'"), true);
});

// ---------------------------------------------------------------------------
// BUG-123 ROUND 10 regression (attacker "Thresher" REJECT, applied here
// proactively for consistency — Thresher's live repro was against the
// sibling copies of this exact construct in claude-secret-guard.js /
// claude-plan-guard.js, but this file is the pattern's origin and shared the
// identical 200-char distance cap). Fixed by rebuilding
// looksLikeCommitFallback() as unbounded indexOf/startsWith scanning (no
// regex, no distance cap, strictly O(n), zero backtracking).
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
  const adversarial = 'git '.repeat(300000) + 'x'.repeat(100000);
  assert.equal(adversarial.length > 1_000_000, true, 'sanity: the adversarial fixture is genuinely over 1MB');
  const t0 = process.hrtime.bigint();
  const result = guard.looksLikeCommitFallback(adversarial);
  const elapsedMs = Number(process.hrtime.bigint() - t0) / 1e6;
  assert.equal(result, false, 'no "commit" anywhere in the fixture -- must resolve to false, not hang');
  assert.equal(elapsedMs < 100, true, `must resolve well under 100ms on a 1MB adversarial input (took ${elapsedMs.toFixed(2)}ms) -- a slow fallback would itself be a DoS vector`);
});

// ---------------------------------------------------------------------------
// ROUND 4 — scope stays commit-only (ASM-193/ASM-359) after the reuse
// change. authorGuard.findCommitInvocation() matches a WIDER verb set
// (commit, cherry-pick, revert, am, merge) than this guard enforces —
// reusing its recognition machinery must not silently widen what this file
// intercepts. isCommitInvocation() keeps its own equality check against the
// literal string "commit" after calling into the shared recogniser, so the
// four non-commit verbs must still be un-intercepted, including via the
// exact quoted-bare shape that was the round-4 bypass for "commit" itself
// (proving the scope check, not merely the recognition machinery, is what
// keeps them out).
// ---------------------------------------------------------------------------

for (const verb of ['cherry-pick', 'revert', 'am', 'merge']) {
  test(`ROUND-4 scope: "git" ${verb} (quoted-bare git token) is NOT recognised as a commit invocation by isCommitInvocation()`, () => {
    const cmd = `"git" ${verb} -m "not a commit, verb=${verb}"`;
    assert.equal(isCommitInvocation(cmd, authorGuard), false, `git ${verb} must stay out of scope (ASM-193/ASM-359), even via the quoted-bare git token that now IS recognised for commit`);
  });
}

test('ROUND-4 scope, end-to-end: `git merge` on a code-bearing file with zero BOW tags is silently ALLOWED (out of scope, not the bypass trap — merge is not commit)', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/foo.go', 'package foo\n');
    // No actual merge to perform in this throwaway single-commit repo, but
    // isCommitInvocation() decides purely from the command STRING (staged
    // files only matter once a commit is already recognised), so this
    // proves the guard exits before ever reaching the staged-file check for
    // a non-commit porcelain verb.
    const r = runGuard(dir, 'git merge some-branch');
    assert.equal(r.denied, false);
    assert.equal(r.stdout, '');
  });
});

// ---------------------------------------------------------------------------
// ROUND 4 — recognition corpus ("divergence table", now against ground
// truth rather than a second regex, since full reuse means there is no
// second recogniser left in this file to diverge FROM). Each row is
// {label, command, expected}; the assertion loop collects every mismatch
// into a table and asserts it is EMPTY, printing the table either way so a
// reviewer sees the full corpus result, not just a pass/fail count.
// ---------------------------------------------------------------------------

const RECOGNITION_CORPUS = [
  { label: 'bare', command: 'git commit -m "x"', expected: true },
  { label: '.exe suffix', command: 'git.exe commit -m "x"', expected: true },
  { label: '.cmd suffix', command: 'git.cmd commit -m "x"', expected: true },
  { label: 'quoted-with-separator', command: '"/usr/bin/git" commit -m "x"', expected: true },
  { label: 'unquoted-with-separator', command: '/usr/bin/git commit -m "x"', expected: true },
  { label: 'quoted-with-separator, Windows path', command: '"C:\\Program Files\\Git\\bin\\git.exe" commit -m "x"', expected: true },
  { label: 'quoted-bare (ROUND-4 DEFECT)', command: '"git" commit -m "x"', expected: true },
  { label: 'single-quoted-bare (ROUND-4 DEFECT)', command: "'git' commit -m 'x'", expected: true },
  { label: 'leading env assignment', command: 'GIT_AUTHOR_NAME=x git commit -m "x"', expected: true },
  { label: 'export prefix', command: 'export FOO=bar; git commit -m "x"', expected: true },
  { label: 'boundary: semicolon', command: 'npm install; git commit -m "x"', expected: true },
  { label: 'boundary: &&', command: 'true && git commit -m "x"', expected: true },
  { label: 'boundary: pipe', command: 'echo hi | git commit -m "x"', expected: true },
  { label: 'boundary: open paren', command: '(git commit -m "x")', expected: true },
  { label: 'boundary: newline', command: 'true\ngit commit -m "x"', expected: true },
  { label: 'boundary: quoted-bare after semicolon', command: 'npm install; "git" commit -m "x"', expected: true },
  { label: 'negative: prose, no real boundary', command: 'echo "please git commit later"', expected: false },
  { label: 'negative: lookalike binary name', command: 'mygithub commit -m "x"', expected: false },
  { label: 'negative: non-commit verb', command: 'git status', expected: false },
  { label: 'negative: out-of-scope verb (cherry-pick)', command: 'git cherry-pick abc123', expected: false },
  { label: 'negative: out-of-scope verb, quoted-bare (merge)', command: '"git" merge some-branch', expected: false },
];

test('ROUND-4 recognition corpus: isCommitInvocation() matches expected truth for every shape (empty divergence table)', () => {
  const divergences = [];
  for (const row of RECOGNITION_CORPUS) {
    const actual = isCommitInvocation(row.command, authorGuard);
    if (actual !== row.expected) {
      divergences.push({ ...row, actual });
    }
  }
  if (divergences.length) {
    console.log('  [round-4 divergence table]', JSON.stringify(divergences, null, 2));
  } else {
    console.log(`  [round-4 divergence table] EMPTY — all ${RECOGNITION_CORPUS.length} corpus shapes matched expected truth`);
  }
  assert.deepEqual(divergences, []);
});

// ---------------------------------------------------------------------------
// End-to-end: AC-17 — unresolvable tag
// ---------------------------------------------------------------------------

test('AC-17: an unresolvable tag is denied and named individually', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/foo.go', 'package foo\n');
    // BUG-152: CODE-shaped (prefix + "-" + purely numeric id) so it still
    // reaches BOW resolution instead of being filtered out as prose.
    // BUG-164: real TYPE_PREFIX ("FEAT", not the made-up "ZZZ") with an
    // absurdly large, never-real id, so the shape filter still lets it
    // through to be correctly named as unresolvable.
    const r = runGuard(dir, 'git commit -m "[FEAT-999999999] change"');
    assert.equal(r.denied, true);
    assert.match(r.reason, /FEAT-999999999/);
  });
});

// ---------------------------------------------------------------------------
// End-to-end: AC-14 / AC-15 — latest-verdict lookup, per-item naming
// ---------------------------------------------------------------------------

test('AC-14/AC-15: an item with an accepted verdict passes; a second item with only a reject is denied and named individually (not the accepted one)', async () => {
  const db = await connectDb();
  const ok = await createFixtureItem(db, 'accept-path');
  const bad = await createFixtureItem(db, 'reject-path');
  try {
    await recordDestructiveVerdict(db, ok.code, { verdict: 'accept', attacker: 'Destructive-Fixture' });
    await recordDestructiveVerdict(db, bad.code, { verdict: 'reject', attacker: 'Destructive-Fixture' });

    withTempRepo((dir) => {
      stageFile(dir, 'internal/foo.go', 'package foo\n');
      const rGood = runGuard(dir, `git commit -m "[${ok.code}] change"`);
      assert.equal(rGood.denied, false, 'an item whose LATEST verdict is accept must pass');
    });

    withTempRepo((dir) => {
      stageFile(dir, 'internal/foo.go', 'package foo\n');
      const rBad = runGuard(dir, `git commit -m "[${bad.code}] change"`);
      assert.equal(rBad.denied, true, 'an item whose LATEST verdict is reject (never later accepted) must be denied — this is the "latest, not any" check');
      assert.match(rBad.reason, new RegExp(bad.code));
    });

    withTempRepo((dir) => {
      stageFile(dir, 'internal/foo.go', 'package foo\n');
      const rMixed = runGuard(dir, `git commit -m "[${ok.code}] and [${bad.code}] together"`);
      assert.equal(rMixed.denied, true);
      assert.match(rMixed.reason, new RegExp(bad.code));
      assert.doesNotMatch(rMixed.reason, new RegExp(`${ok.code} "`));
    });
  } finally {
    await deleteFixtureItem(db, ok.guid);
    await deleteFixtureItem(db, bad.guid);
    await db.end();
  }
});

// ---------------------------------------------------------------------------
// End-to-end: BUG-152 — bracketed prose vs. a genuine tag, full pipeline
// ---------------------------------------------------------------------------

test('BUG-152 end-to-end: a real, accepted tag passes even when the SAME message also contains false-positive-shaped brackets (Go generics, a quoted literal)', async () => {
  const db = await connectDb();
  const item = await createFixtureItem(db, 'bug152-mixed');
  try {
    await recordDestructiveVerdict(db, item.code, { verdict: 'accept', attacker: 'Destructive-Fixture' });

    withTempRepo((dir) => {
      stageFile(dir, 'internal/foo.go', 'package foo\n');
      const message =
        `[${item.code}] switch atomic.Pointer[Screen] to the new API, ` +
        `add Store[T,PT] generic constraint, redact literal [REDACTED-GR22] from logs`;
      const r = runGuard(dir, `git commit -m "${message}"`);
      assert.equal(
        r.denied,
        false,
        'the genuine, accepted tag must pass even though the same message is full of bracket prose that used to false-match'
      );
    });
  } finally {
    await deleteFixtureItem(db, item.guid);
    await db.end();
  }
});

// ---------------------------------------------------------------------------
// End-to-end: AC-18 — the escape hatch, BOTH halves
// ---------------------------------------------------------------------------

test('AC-18a: CLAUDE_DISABLE_DESTRUCTIVE_GUARD=1 in the real process env allows', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/foo.go', 'package foo\n');
    const r = runGuard(dir, 'git commit -m "no tag, would normally be denied"', { CLAUDE_DISABLE_DESTRUCTIVE_GUARD: '1' });
    assert.equal(r.denied, false);
  });
});

test('AC-18b: the same text INLINE in the command string, with the real env unset, is STILL denied (proves the hatch cannot be self-granted)', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/foo.go', 'package foo\n');
    const r = runGuard(
      dir,
      'CLAUDE_DISABLE_DESTRUCTIVE_GUARD=1 git commit -m "no tag, trying to bypass inline"',
      { CLAUDE_DISABLE_DESTRUCTIVE_GUARD: undefined }
    );
    assert.equal(r.denied, true, 'inline env-var text in the command string must never satisfy the escape hatch');
  });
});

// ---------------------------------------------------------------------------
// End-to-end (round 3): crash-open dependency load failure (Tester finding
// against round 2). `require('./claude-author-guard.js')` and
// `require('./claude-bow.js')` used to sit at module TOP LEVEL, outside
// main() and therefore outside the main().catch() net AC-25's fail-closed
// guarantee depends on. A synchronous throw during either require() (missing
// file, syntax error in the required file, wrong export shape) happened
// during MODULE EVALUATION, before main() was ever called — so it crashed
// the whole process (uncaught exception, exit 1, ZERO stdout) instead of
// producing a decision. Under the PreToolUse contract that is a
// non-blocking error: the proposed `git commit` PROCEEDS, disabling the
// entire gate (not merely bypass detection) for as long as the dependency
// stays broken — exactly the failure mode AC-21/AC-25 claim cannot happen.
//
// Fix: both requires moved into loadDependencies(), called from inside
// main(), so a load-time throw rejects main()'s promise and is caught by
// the EXISTING main().catch() wrapper (no new mechanism — the one AC-25
// already relies on now actually covers this path). loadDependencies()
// resolves its two paths via CLAUDE_DESTRUCTIVE_GUARD_AUTHORGUARD_PATH /
// CLAUDE_DESTRUCTIVE_GUARD_BOW_PATH env var overrides, defaulting to the
// real sibling files when unset — production behaviour is unchanged, and
// these three tests use the override to point at disposable fixture files
// under the OS temp dir, WITHOUT ever touching the real
// claude-author-guard.js / claude-bow.js (both explicitly off-limits this
// round — other agents are live-editing claude-author-guard.js in this same
// tree right now).
//
// Each case below is proven able to fail: reverting to the round-2 shape
// (top-level `require`, no loadDependencies()) reproduces the exact
// crash-open behaviour the Tester found — verified by hand against a
// reconstructed copy of the round-2 file: MISSING and SYNTAX-ERROR both
// exited 1 with empty stdout (no decision emitted, commit would proceed);
// WRONG-EXPORTS already denied correctly even in round 2 (a TypeError deep
// in isCommitInvocation() was still inside main()'s try, so main().catch()
// caught it) — included here as a non-regression case, not a new red case.
// ---------------------------------------------------------------------------

function withFixtureDep(content, fn) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'destructive-guard-depfixture-'));
  const fixturePath = path.join(dir, 'fixture-dep.js');
  try {
    if (content !== null) fs.writeFileSync(fixturePath, content, 'utf8');
    // content === null -> deliberately do NOT create the file (missing-dependency case)
    return fn(fixturePath);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

/** A valid, minimal claude-bow.js stand-in — used as the OTHER dependency in
 * each author-guard-failure case below, so only one dependency is broken at
 * a time and the failure is attributable to the one under test. */
function withValidBowFixture(fn) {
  return withFixtureDep(
    "module.exports = { findItemByRef: async () => null, latestDestructiveVerdict: async () => null };\n",
    fn
  );
}

test('round-3 dependency failure (MISSING): claude-author-guard.js failing to load on a code-bearing commit denies with a decision payload, not a bare crash', () => {
  withValidBowFixture((bowFixturePath) => {
    withTempRepo((dir) => {
      stageFile(dir, 'internal/foo.go', 'package foo\n');
      const r = runGuard(dir, 'git commit -m "[FEAT-040] change"', {
        // Point at a path that does not exist at all -> require() throws
        // MODULE_NOT_FOUND, the exact "dependency missing" case from the
        // Tester's report.
        CLAUDE_DESTRUCTIVE_GUARD_AUTHORGUARD_PATH: path.join(os.tmpdir(), 'this-file-does-not-exist-' + crypto.randomBytes(4).toString('hex') + '.js'),
        CLAUDE_DESTRUCTIVE_GUARD_BOW_PATH: bowFixturePath,
      });
      assert.notEqual(r.status, 1, 'must not crash with a bare uncaught-exception exit(1) — that is the exact bug being fixed (no decision emitted, commit proceeds)');
      assert.equal(r.status, 0, 'the guard process must exit 0 (it makes the ALLOW/DENY decision itself, same convention as every other path in this file)');
      assert.equal(r.denied, true, 'a missing dependency on a real git-commit-shaped command must DENY, not silently allow');
      assert.notEqual(r.stdout, '', 'a decision payload MUST be emitted — this is the crash-open bug: round 2 emitted nothing at all here');
      assert.match(r.reason, /depend|load|author-?guard/i);
    });
  });
});

test('round-3 dependency failure (SYNTAX ERROR): a syntax error in claude-author-guard.js on a code-bearing commit denies with a decision payload, not a bare crash', () => {
  withValidBowFixture((bowFixturePath) => {
    withFixtureDep('this is ( not valid javascript at all {{{\n', (brokenAuthorGuardPath) => {
      withTempRepo((dir) => {
        stageFile(dir, 'internal/foo.go', 'package foo\n');
        const r = runGuard(dir, 'git commit -m "[FEAT-040] change"', {
          CLAUDE_DESTRUCTIVE_GUARD_AUTHORGUARD_PATH: brokenAuthorGuardPath,
          CLAUDE_DESTRUCTIVE_GUARD_BOW_PATH: bowFixturePath,
        });
        assert.equal(r.status, 0, 'the guard process must exit 0, not crash with an uncaught SyntaxError (exit 1)');
        assert.equal(r.denied, true, 'a syntax-broken dependency on a real git-commit-shaped command must DENY, not silently allow');
        assert.notEqual(r.stdout, '', 'a decision payload MUST be emitted — round 2 emitted nothing at all here (uncaught SyntaxError during require())');
      });
    });
  });
});

test('round-3 dependency failure (WRONG EXPORTS): claude-author-guard.js loading but missing the expected functions on a code-bearing commit denies with a decision payload, not a bare crash', () => {
  withValidBowFixture((bowFixturePath) => {
    withFixtureDep('module.exports = { someUnrelatedExport: 42 };\n', (wrongShapeAuthorGuardPath) => {
      withTempRepo((dir) => {
        stageFile(dir, 'internal/foo.go', 'package foo\n');
        const r = runGuard(dir, 'git commit -m "[FEAT-040] change"', {
          CLAUDE_DESTRUCTIVE_GUARD_AUTHORGUARD_PATH: wrongShapeAuthorGuardPath,
          CLAUDE_DESTRUCTIVE_GUARD_BOW_PATH: bowFixturePath,
        });
        assert.equal(r.status, 0, 'the guard process must exit 0, not crash');
        assert.equal(r.denied, true, 'a wrong-shape dependency on a real git-commit-shaped command must DENY');
        assert.notEqual(r.stdout, '', 'a decision payload MUST be emitted');
        assert.match(r.reason, /gatherScanTexts|depend|export/i, 'the deny reason should name what is missing, not just "internal error"');
      });
    });
  });
});

test('round-3: a dependency failure on a NON-commit-shaped command is silently allowed, not a blanket deny (bricking every Bash command over an unrelated broken file would be disproportionate)', () => {
  withValidBowFixture((bowFixturePath) => {
    withTempRepo((dir) => {
      const r = runGuard(dir, 'npm install', {
        CLAUDE_DESTRUCTIVE_GUARD_AUTHORGUARD_PATH: path.join(os.tmpdir(), 'this-file-does-not-exist-' + crypto.randomBytes(4).toString('hex') + '.js'),
        CLAUDE_DESTRUCTIVE_GUARD_BOW_PATH: bowFixturePath,
      });
      assert.equal(r.status, 0);
      assert.equal(r.denied, false);
      assert.equal(r.stdout, '');
    });
  });
});

// ---------------------------------------------------------------------------
// End-to-end: AC-21/22/23/24/25 — fail-closed error handling
// ---------------------------------------------------------------------------

test('AC-22: DB unreachable on a code-bearing commit denies (inverse of tool.bow AC-10)', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/foo.go', 'package foo\n');
    const r = runGuard(dir, 'git commit -m "[FEAT-040] change"', { METRO_DB_PORT: '1' });
    assert.equal(r.denied, true);
    assert.match(r.reason, /unreachable|DB/i);
  });
});

test('AC-23: an unparseable commit message (-F file) on a code-bearing commit denies, not warn-allow', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/foo.go', 'package foo\n');
    fs.writeFileSync(path.join(dir, 'msgfile.txt'), '[FEAT-040] from a file\n', 'utf8');
    const r = runGuard(dir, 'git commit -F msgfile.txt');
    assert.equal(r.denied, true);
  });
});

test('AC-24: unparseable stdin — allow when it does not look like a git commit, deny when it does', () => {
  withTempRepo((dir) => {
    const rAllow = spawnSync(process.execPath, [GUARD_PATH], { cwd: dir, input: 'not json at all, npm install', encoding: 'utf8' });
    assert.equal((rAllow.stdout || '').trim(), '');

    const rDeny = spawnSync(process.execPath, [GUARD_PATH], { cwd: dir, input: 'not json at all, but mentions git commit somewhere', encoding: 'utf8' });
    const parsed = JSON.parse((rDeny.stdout || '').trim());
    assert.equal(parsed.hookSpecificOutput.permissionDecision, 'deny');
  });
});

test('AC-25: git diff --cached failing (not a git repo at all) denies rather than silently allowing', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'destructive-guard-nongit-'));
  try {
    const payload = JSON.stringify({ tool: 'Bash', tool_input: { command: 'git commit -m "[FEAT-040] change"' } });
    const r = spawnSync(process.execPath, [GUARD_PATH], { cwd: dir, input: payload, encoding: 'utf8' });
    const parsed = JSON.parse((r.stdout || '').trim());
    assert.equal(parsed.hookSpecificOutput.permissionDecision, 'deny');
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

// ---------------------------------------------------------------------------
// End-to-end: AC-27 — determinism
// ---------------------------------------------------------------------------

test('AC-27: evaluating the same staged commit + BOW state twice produces identical decisions', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/foo.go', 'package foo\n');
    const r1 = runGuard(dir, 'git commit -m "no tag at all"');
    const r2 = runGuard(dir, 'git commit -m "no tag at all"');
    assert.equal(r1.denied, r2.denied);
    assert.equal(r1.reason, r2.reason);
  });
});

// ---------------------------------------------------------------------------
// End-to-end: AC-28 — read-only with respect to git and the BOW
// ---------------------------------------------------------------------------

test('AC-28: the guard never calls recordDestructiveVerdict (grep) and never mutates git state or verdict rows (behavioural check)', async () => {
  const src = fs.readFileSync(GUARD_PATH, 'utf8');
  assert.doesNotMatch(src, /recordDestructiveVerdict/);

  const db = await connectDb();
  const item = await createFixtureItem(db, 'readonly-check');
  try {
    const [[{ before }]] = await db.query('SELECT COUNT(*) AS `before` FROM bow_destructive_verdicts WHERE item_guid = ?', [item.guid]);
    withTempRepo((dir) => {
      stageFile(dir, 'internal/foo.go', 'package foo\n');
      runGuard(dir, `git commit -m "[${item.code}] change"`);
      const status = git(dir, ['status', '--porcelain']);
      assert.match(status, /internal\/foo\.go/, 'the guard must not have touched the index/working tree beyond what the test itself staged');
    });
    const [[{ after }]] = await db.query('SELECT COUNT(*) AS `after` FROM bow_destructive_verdicts WHERE item_guid = ?', [item.guid]);
    assert.equal(after, before, 'the guard must never write a verdict row itself');
  } finally {
    await deleteFixtureItem(db, item.guid);
    await db.end();
  }
});

// ---------------------------------------------------------------------------
// BUG-224: combined `git add` + `git commit` bypass.
//
// ROOT CAUSE. This guard decides codeBearing from `git diff --cached
// --name-only`, evaluated ONCE, when the PreToolUse hook fires — BEFORE any
// of the proposed `tool_input.command` string has executed. A command that
// combines `git add <paths>` and `git commit -m "..."` in ONE Bash
// invocation (`&&`, `;`, or a bare newline) stages its own files AFTER this
// guard has already taken its `--cached` snapshot, so a genuinely
// code-bearing commit (touching cmd/, internal/, data/, tools/) reached
// history with zero recorded Destructive verdict, silently — no deny, empty
// stdout, exactly the crash-open shape this file's whole design exists to
// prevent for every OTHER failure mode.
//
// THE FIX. computeAddedPathsOrAmbiguous() scans the command text (reusing
// authorGuard.gatherScanTexts/buildQuoteMask/parseGitInvocation/resolveAlias,
// the same primitives isCommitInvocation() already relies on) for `git add`
// invocations. A SIMPLE one (bare literal paths, no flags/globs/`.`/`..`) has
// its paths unioned into the classification set before `codeBearing` is
// decided. An AMBIGUOUS one (a flag, `.`/`..`, a glob, or zero args — none of
// which can be resolved to concrete paths by reading the command text alone)
// denies outright, fail-closed, with a message asking the operator to split
// `git add` and `git commit` into separate tool calls.
// ---------------------------------------------------------------------------

test('BUG-224 root-cause sanity: `git diff --cached` is genuinely empty at hook-time for the combined command — proves the bypass mechanism this fix closes is real, not hypothetical', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'tools'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'tools', 'bug224.js'), '// scratch\n', 'utf8');
    // No `git add` has run in this process yet — exactly the guard's own
    // vantage point when it evaluates `git diff --cached` for a combined
    // `git add ... && git commit ...` command whose `add` half hasn't
    // executed yet.
    const cached = git(dir, ['diff', '--cached', '--name-only']);
    assert.equal(cached, '', 'the staging area is empty at hook-time — this is the exact root cause BUG-224 reports');
  });
});

test('BUG-224 (1) EXACT REPRO: combined `git add tools/bug224.js && git commit` in ONE call, code-bearing, zero BOW tags — now DENIED (pre-fix: silently allowed, empty stdout)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'tools'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'tools', 'bug224.js'), '// scratch\n', 'utf8');
    const r = runGuard(dir, 'git add tools/bug224.js && git commit -m "no bow tag whatsoever"');
    assert.equal(r.denied, true, 'the combined add+commit must be recognised as code-bearing and denied for lacking a BOW tag');
    assert.notEqual(r.stdout, '', 'a decision payload must be emitted — this is the exact bypass symptom (pre-fix: nothing at all)');
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]/i);
  });
});

test('BUG-224 (1) EXACT REPRO, newline-separated form (two lines in one Bash call, no `&&`) is caught identically', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'tools'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'tools', 'bug224b.js'), '// scratch\n', 'utf8');
    const r = runGuard(dir, 'git add tools/bug224b.js\ngit commit -m "no bow tag whatsoever, newline form"');
    assert.equal(r.denied, true);
    assert.notEqual(r.stdout, '');
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]/i);
  });
});

test('BUG-224 (1) EXACT REPRO with a real BOW code carrying zero verdicts stays denied (the P0 report\'s precise scenario)', async () => {
  const db = await connectDb();
  const item = await createFixtureItem(db, 'bug224-noverdict');
  try {
    withTempRepo((dir) => {
      fs.mkdirSync(path.join(dir, 'tools'), { recursive: true });
      fs.writeFileSync(path.join(dir, 'tools', 'bug224c.js'), '// scratch\n', 'utf8');
      const r = runGuard(dir, `git add tools/bug224c.js && git commit -m "[${item.code}] change, no verdict recorded"`);
      assert.equal(r.denied, true, 'a real BOW code with zero recorded Destructive verdicts must still be denied once the code-bearing status is correctly detected');
      assert.match(r.reason, new RegExp(item.code));
    });
  } finally {
    await deleteFixtureItem(db, item.guid);
    await db.end();
  }
});

test('BUG-224 (2): combined add+commit for a genuinely NON-code-bearing path (docs/) is still silently allowed', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'docs'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'docs', 'notes.md'), '# notes\n', 'utf8');
    const r = runGuard(dir, 'git add docs/notes.md && git commit -m "docs update, no tag needed"');
    assert.equal(r.denied, false);
    assert.equal(r.stdout, '');
  });
});

test('BUG-224 (3a): a bare `git add` tool call, on its own, is still silently allowed (not mistaken for a commit invocation)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'foo.go'), 'package foo\n', 'utf8');
    const r = runGuard(dir, 'git add internal/foo.go');
    assert.equal(r.denied, false);
    assert.equal(r.stdout, '');
  });
});

test('BUG-224 (3b): SEPARATE `git add` then `git commit` tool calls still work — the later commit correctly sees the already-staged file via the normal `--cached` path (existing passing case, unaffected by this fix)', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/foo.go', 'package foo\n');
    const r = runGuard(dir, 'git commit -m "no bow tag whatsoever, separate calls"');
    assert.equal(r.denied, true, 'a separately-staged code-bearing file with zero tags must still be denied, exactly as before this fix');
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]/i);
  });
});

test('BUG-224 (3c): SEPARATE `git add` then `git commit` tool calls, with an accepted verdict, still pass — proves the fix does not over-broaden and start denying the legitimate separate-calls flow', async () => {
  const db = await connectDb();
  const item = await createFixtureItem(db, 'bug224-separate-ok');
  try {
    await recordDestructiveVerdict(db, item.code, { verdict: 'accept', attacker: 'Destructive-Fixture' });
    withTempRepo((dir) => {
      stageFile(dir, 'internal/foo.go', 'package foo\n');
      const r = runGuard(dir, `git commit -m "[${item.code}] change, separate calls"`);
      assert.equal(r.denied, false, 'a separately-staged, already-verdicted commit must still pass cleanly');
    });
  } finally {
    await deleteFixtureItem(db, item.guid);
    await db.end();
  }
});

test('BUG-224 (4a): an AMBIGUOUS `git add -A` combined with commit is denied conservatively, even though the actual paths staged would only have been docs/', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'docs'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'docs', 'notes.md'), '# notes\n', 'utf8');
    const r = runGuard(dir, 'git add -A && git commit -m "docs only, but ambiguous add shape"');
    assert.equal(r.denied, true, 'an ambiguous git add (-A) combined with commit must fail closed rather than guess what it staged');
    assert.match(r.reason, /ambiguous|split/i);
    assert.notEqual(r.stdout, '');
  });
});

test('BUG-224 (4b): other ambiguous `git add` shapes ("." wildcard, "--all", glob, "-u", zero pathspec) are all caught the same conservative way', () => {
  const ambiguousCmds = [
    'git add . && git commit -m "x"',
    'git add --all && git commit -m "x"',
    'git add tools/*.js && git commit -m "x"',
    'git add -u && git commit -m "x"',
    'git add && git commit -m "x"',
  ];
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'docs'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'docs', 'notes.md'), '# notes\n', 'utf8');
    for (const cmd of ambiguousCmds) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `must deny ambiguous shape: ${cmd}`);
      assert.match(r.reason, /ambiguous|split/i, `deny reason must explain for: ${cmd}`);
    }
  });
});

test('BUG-224 unit: classifyAddArgs — simple bare paths are OK; flags/glob/dot/empty are ambiguous; a bare "--" separator is skipped, not flagged', () => {
  assert.deepEqual(classifyAddArgs(' tools/x.js internal/y.go', authorGuard), { ok: true, paths: ['tools/x.js', 'internal/y.go'] });
  assert.deepEqual(classifyAddArgs(' -- tools/x.js', authorGuard), { ok: true, paths: ['tools/x.js'] });
  assert.equal(classifyAddArgs(' -A', authorGuard).ok, false);
  assert.equal(classifyAddArgs(' --all', authorGuard).ok, false);
  assert.equal(classifyAddArgs(' -u', authorGuard).ok, false);
  assert.equal(classifyAddArgs(' .', authorGuard).ok, false);
  assert.equal(classifyAddArgs(' ..', authorGuard).ok, false);
  assert.equal(classifyAddArgs(' tools/*.js', authorGuard).ok, false);
  assert.equal(classifyAddArgs('', authorGuard).ok, false);
});

test('BUG-224 unit: computeAddedPathsOrAmbiguous — no add present, simple add present, ambiguous add present', () => {
  assert.deepEqual(computeAddedPathsOrAmbiguous('git commit -m "x"', authorGuard), { hasAdd: false, ambiguous: false, paths: [] });
  assert.deepEqual(
    computeAddedPathsOrAmbiguous('git add tools/x.js && git commit -m "x"', authorGuard),
    { hasAdd: true, ambiguous: false, paths: ['tools/x.js'] }
  );
  assert.equal(computeAddedPathsOrAmbiguous('git add -A && git commit -m "x"', authorGuard).ambiguous, true);
});

test('BUG-224: `git add` recognised via the same spellings isCommitInvocation() tolerates for commit (git.exe, quoted full path, bash -c wrapper) — a corpus, empty divergence table', () => {
  const corpus = [
    { label: 'bare', command: 'git add tools/x.js && git commit -m "x"' },
    { label: '.exe suffix', command: 'git.exe add tools/x.js && git commit -m "x"' },
    { label: 'quoted full path', command: '"C:\\Program Files\\Git\\bin\\git.exe" add tools/x.js && git commit -m "x"' },
    { label: 'bash -c wrapper', command: 'bash -c "git add tools/x.js && git commit -m \'x\'"' },
    { label: 'quoted-bare git token', command: '"git" add tools/x.js && "git" commit -m "x"' },
  ];
  const divergences = [];
  for (const row of corpus) {
    const info = computeAddedPathsOrAmbiguous(row.command, authorGuard);
    if (!(info.hasAdd === true && info.ambiguous === false && info.paths.includes('tools/x.js'))) {
      divergences.push({ ...row, info });
    }
  }
  if (divergences.length) console.log('  [BUG-224 divergence table]', JSON.stringify(divergences, null, 2));
  assert.deepEqual(divergences, []);
});

test('BUG-224: `git add` mentioned only in commit-message PROSE is not mistaken for a real invocation (quote-aware, same skip logic as isCommitInvocation)', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/foo.go', 'package foo\n');
    const r = runGuard(dir, 'git commit -m "reminder: run git add -A before committing next time, no tag here"');
    assert.equal(r.denied, true);
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]/i, 'must be denied for the ordinary zero-tag reason');
    assert.doesNotMatch(r.reason, /ambiguous|split/i, 'prose mentioning "git add -A" must NOT trigger the BUG-224 ambiguous-add deny path');
  });
});

test('ambiguousAddDenyMessage names GR#23/BUG-224 and the split-into-separate-calls remedy', () => {
  const msg = ambiguousAddDenyMessage();
  assert.match(msg, /GR#23/);
  assert.match(msg, /BUG-224/);
  assert.match(msg, /separate/i);
});

// ---------------------------------------------------------------------------
// FEAT-077 — GR#23 proportionality tier (docs-only / test-only exemption)
// ---------------------------------------------------------------------------

test('FEAT-077 unit: isExemptFile matches only *.md / *.test.js / *_test.go, case-sensitively', () => {
  assert.equal(isExemptFile('docs/notes.md'), true);
  assert.equal(isExemptFile('README.md'), true);
  assert.equal(isExemptFile('claude-bow.test.js'), true);
  assert.equal(isExemptFile('internal/engine/core/pacing_test.go'), true);
  // Non-exempt shapes.
  assert.equal(isExemptFile('internal/engine/core/pacing.go'), false);
  assert.equal(isExemptFile('data/pacing.json'), false);
  assert.equal(isExemptFile('claude-bow.js'), false);
  // Case sensitivity (git's own casing — no case-folding anywhere here).
  assert.equal(isExemptFile('README.MD'), false);
  assert.equal(isExemptFile('foo.Test.js'), false);
  assert.equal(isExemptFile('foo_Test.go'), false);
});

test('FEAT-077 unit: isExemptFileSet requires a NON-EMPTY list where EVERY file is exempt', () => {
  assert.equal(isExemptFileSet([]), false, 'empty staged diff must be full tier, not exempt (spec, verbatim)');
  assert.equal(isExemptFileSet(['docs/a.md']), true);
  assert.equal(isExemptFileSet(['docs/a.md', 'internal/x_test.go']), true);
  assert.equal(isExemptFileSet(['docs/a.md', 'internal/x.go']), false, 'one non-exempt file anywhere denies the whole set exemption');
});

/** Builds a real authorGuard.findCommitInvocation() result for `command` —
 * never a hand-built fixture object — so isArgvClassifiable() is exercised
 * against the exact shape production main() hands it. */
function invocationFor(command) {
  const inv = authorGuard.findCommitInvocation(command);
  assert.ok(inv && inv.verb === 'commit', `fixture command must resolve to a real commit invocation: ${command}`);
  return inv;
}

test('FEAT-077 unit: isArgvClassifiable is true for plain -m commits (any recognised boolean/value flag combo)', () => {
  assert.equal(isArgvClassifiable(invocationFor('git commit -m "docs: x [FEAT-077]"'), authorGuard), true);
  assert.equal(isArgvClassifiable(invocationFor('git commit --message="docs: x"'), authorGuard), true);
  assert.equal(isArgvClassifiable(invocationFor('git commit -q -m "x" --no-verify'), authorGuard), true);
  assert.equal(isArgvClassifiable(invocationFor('git commit --amend -m "x"'), authorGuard), true);
});

test('FEAT-077 unit: isArgvClassifiable is FALSE for -a / --all in any spelling, including combined short flags', () => {
  assert.equal(isArgvClassifiable(invocationFor('git commit -a -m "x"'), authorGuard), false);
  assert.equal(isArgvClassifiable(invocationFor('git commit --all -m "x"'), authorGuard), false);
  assert.equal(isArgvClassifiable(invocationFor('git commit -am "x"'), authorGuard), false, 'combined short flag -am hides -a inside one token');
});

test('FEAT-077 unit: isArgvClassifiable is FALSE for an explicit pathspec or a bare "--" separator', () => {
  assert.equal(isArgvClassifiable(invocationFor('git commit -m "x" some/file.go'), authorGuard), false);
  assert.equal(isArgvClassifiable(invocationFor('git commit -m "x" --'), authorGuard), false);
  assert.equal(isArgvClassifiable(invocationFor('git commit -m "x" -- some/file.go'), authorGuard), false);
});

test('FEAT-077 unit: isArgvClassifiable is FALSE for any unrecognised flag (fail-closed default, not fail-open)', () => {
  assert.equal(isArgvClassifiable(invocationFor('git commit --some-future-flag -m "x"'), authorGuard), false);
  assert.equal(isArgvClassifiable(invocationFor('git commit -Q -m "x"'), authorGuard), false);
});

test('FEAT-077 unit: isArgvClassifiable is FALSE when the invocation cannot be classified at all (null/malformed input)', () => {
  assert.equal(isArgvClassifiable(null, authorGuard), false);
  assert.equal(isArgvClassifiable({}, authorGuard), false);
});

test('FEAT-077 unit: isExemptCommit requires BOTH a classifiable argv AND an all-exempt file set', () => {
  const plainInv = invocationFor('git commit -m "docs only"');
  const allInv = invocationFor('git commit -a -m "docs only"');
  assert.equal(isExemptCommit(['docs/a.md'], plainInv, authorGuard), true);
  assert.equal(isExemptCommit(['docs/a.md'], allInv, authorGuard), false, '-a makes the diff untrustworthy even for an all-md file list');
  assert.equal(isExemptCommit(['internal/x.go'], plainInv, authorGuard), false, 'non-exempt file even with a classifiable argv');
  assert.equal(isExemptCommit([], plainInv, authorGuard), false, 'empty file list is never exempt');
});

test('FEAT-077 end-to-end: a docs-only commit under an ENFORCED dir (data/) is silently allowed with ZERO tags — no verdict lookup', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'data/CHANGES.md', '# changes\n');
    const r = runGuard(dir, 'git commit -m "docs only, no tag needed"');
    assert.equal(r.denied, false);
    assert.equal(r.stdout, '');
  });
});

test('FEAT-077 end-to-end: a test-only commit (*_test.go) under internal/ is silently allowed with ZERO tags — proves the exemption overrides the pre-existing enforced-dir codeBearing check', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/engine/core/pacing_test.go', 'package core\n');
    const r = runGuard(dir, 'git commit -m "test only, no tag needed"');
    assert.equal(r.denied, false);
    assert.equal(r.stdout, '');
  });
});

test('FEAT-077 end-to-end: a *.test.js-only commit is silently allowed with ZERO tags', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'claude-bow.test.js', '// test only\n');
    const r = runGuard(dir, 'git commit -m "test only, no tag needed"');
    assert.equal(r.denied, false);
    assert.equal(r.stdout, '');
  });
});

test('FEAT-077 end-to-end: a MIXED commit (one exempt test file + one ordinary .go file) under internal/ keeps the FULL existing fail-closed behaviour (denied, zero tags)', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/engine/core/pacing_test.go', 'package core\n');
    stageFile(dir, 'internal/engine/core/pacing.go', 'package core\n');
    const r = runGuard(dir, 'git commit -m "no bow tag whatsoever"');
    assert.equal(r.denied, true, 'one non-exempt file in the same commit must keep the full tier');
  });
});

test('FEAT-077 end-to-end: `git commit -a` on a repo whose ONLY staged file is test-only still gets the FULL tier (denied, zero tags) — -a makes --cached untrustworthy', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/engine/core/pacing_test.go', 'package core\n');
    const r = runGuard(dir, 'git commit -a -m "no bow tag whatsoever"');
    assert.equal(r.denied, true, '-a must force the full tier even though the staged snapshot looks all-exempt');
  });
});

test('FEAT-077 end-to-end: an explicit pathspec argument on a test-only staged file still gets the FULL tier (denied, zero tags)', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/engine/core/pacing_test.go', 'package core\n');
    const r = runGuard(dir, 'git commit -m "no bow tag whatsoever" internal/engine/core/pacing_test.go');
    assert.equal(r.denied, true, 'an explicit pathspec must force the full tier even though the staged snapshot looks all-exempt');
  });
});

test('FEAT-077 end-to-end: a non-.md/.test.js/_test.go code file (data/pacing.json) stays FULL tier as before (unaffected by the new exemption)', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'data/pacing.json', '{}\n');
    const r = runGuard(dir, 'git commit -m "no bow tag whatsoever"');
    assert.equal(r.denied, true);
  });
});

// ---------------------------------------------------------------------------
// claude-bow.js: recordDestructiveVerdict / latestDestructiveVerdict (A)
// ---------------------------------------------------------------------------

test('AC-1: bow_destructive_verdicts exists after init (auto-run on every command)', async () => {
  const db = await connectDb();
  try {
    const [rows] = await db.query("SHOW TABLES LIKE 'bow_destructive_verdicts'");
    assert.equal(rows.length, 1);
  } finally {
    await db.end();
  }
});

test('AC-2: bad --verdict value and missing/empty --attacker both reject without writing a row', async () => {
  const db = await connectDb();
  const item = await createFixtureItem(db, 'ac2');
  try {
    const [[{ n0 }]] = await db.query('SELECT COUNT(*) AS n0 FROM bow_destructive_verdicts WHERE item_guid = ?', [item.guid]);

    await assert.rejects(() => recordDestructiveVerdict(db, item.code, { verdict: 'maybe', attacker: 'X' }));
    await assert.rejects(() => recordDestructiveVerdict(db, item.code, { verdict: 'accept', attacker: '' }));
    await assert.rejects(() => recordDestructiveVerdict(db, item.code, { verdict: 'accept' /* no attacker at all */ }));

    const [[{ n1 }]] = await db.query('SELECT COUNT(*) AS n1 FROM bow_destructive_verdicts WHERE item_guid = ?', [item.guid]);
    assert.equal(n1, n0);
  } finally {
    await deleteFixtureItem(db, item.guid);
    await db.end();
  }
});

test('AC-3: --class validates against the shared FINDING_CLASSES constant (bad class rejects, real class succeeds)', async () => {
  const grepCount = (fs.readFileSync(BOW_PATH, 'utf8').match(/FINDING_CLASSES\s*=\s*\[/g) || []).length;
  assert.equal(grepCount, 1, 'exactly one FINDING_CLASSES array literal definition');

  const db = await connectDb();
  const item = await createFixtureItem(db, 'ac3');
  try {
    await assert.rejects(() => recordDestructiveVerdict(db, item.code, { verdict: 'accept', attacker: 'X', classes: 'not-a-real-class' }));
    const result = await recordDestructiveVerdict(db, item.code, { verdict: 'accept', attacker: 'X', classes: 'concurrency-safety' });
    assert.deepEqual(result.classes, ['concurrency-safety']);
  } finally {
    await deleteFixtureItem(db, item.guid);
    await db.end();
  }
});

test('AC-4: --findings with an unresolvable code aborts the whole write (all-or-nothing, no partial row)', async () => {
  const db = await connectDb();
  const item = await createFixtureItem(db, 'ac4');
  try {
    const [[{ n0 }]] = await db.query('SELECT COUNT(*) AS n0 FROM bow_destructive_verdicts WHERE item_guid = ?', [item.guid]);
    await assert.rejects(() => recordDestructiveVerdict(db, item.code, { verdict: 'accept', attacker: 'X', findings: 'SEC-999-does-not-exist' }));
    const [[{ n1 }]] = await db.query('SELECT COUNT(*) AS n1 FROM bow_destructive_verdicts WHERE item_guid = ?', [item.guid]);
    assert.equal(n1, n0);
  } finally {
    await deleteFixtureItem(db, item.guid);
    await db.end();
  }
});

test('AC-5: destructive on an unknown ref errors without writing a row', async () => {
  const db = await connectDb();
  try {
    await assert.rejects(() => recordDestructiveVerdict(db, 'FEAT-999999-NOPE', { verdict: 'accept', attacker: 'X' }));
  } finally {
    await db.end();
  }
});

test('AC-6: append-only — reject then accept leaves TWO rows, and verdict reports the latest (accept)', async () => {
  const db = await connectDb();
  const item = await createFixtureItem(db, 'ac6');
  try {
    await recordDestructiveVerdict(db, item.code, { verdict: 'reject', attacker: 'X' });
    await recordDestructiveVerdict(db, item.code, { verdict: 'accept', attacker: 'X' });
    const [[{ n }]] = await db.query('SELECT COUNT(*) AS n FROM bow_destructive_verdicts WHERE item_guid = ?', [item.guid]);
    assert.equal(n, 2);
    const latest = await latestDestructiveVerdict(db, item.code);
    assert.equal(latest.verdict, 'accept');
  } finally {
    await deleteFixtureItem(db, item.guid);
    await db.end();
  }
});

test('AC-7: no verdict recorded is reported distinctly (not "accept", not "reject", not an absent JSON field)', async () => {
  const db = await connectDb();
  const item = await createFixtureItem(db, 'ac7');
  try {
    const latest = await latestDestructiveVerdict(db, item.code);
    assert.equal(latest, null);

    const r = spawnSync(process.execPath, [BOW_PATH, 'verdict', item.code, '--json'], { encoding: 'utf8' });
    const parsed = JSON.parse(r.stdout.trim());
    assert.ok('verdict' in parsed, 'the field must be present, not simply absent');
    assert.equal(parsed.verdict, null);
  } finally {
    await deleteFixtureItem(db, item.guid);
    await db.end();
  }
});

test('AC-8: recordDestructiveVerdict/latestDestructiveVerdict are exported from claude-bow.js module.exports', () => {
  const src = fs.readFileSync(BOW_PATH, 'utf8');
  assert.match(src, /module\.exports\s*=\s*\{[\s\S]*recordDestructiveVerdict[\s\S]*\}/);
  assert.match(src, /module\.exports\s*=\s*\{[\s\S]*latestDestructiveVerdict[\s\S]*\}/);
  assert.match(fs.readFileSync(GUARD_PATH, 'utf8'), /require\(['"]\.\/claude-bow\.js['"]\)/);
});

test('BUG-213 regression: isArgvClassifiable is FALSE for --pathspec-from-file / --pathspec-file-nul (commits working-tree paths outside the index, so --cached is not a truthful preview)', () => {
  // Attack: stage only exempt .md, then `git commit -m "[TAG]" --pathspec-from-file=paths.txt`
  // where paths.txt names an unstaged production .go file — git commits that
  // file's working-tree changes regardless of the index. Must fall to full tier.
  assert.equal(isArgvClassifiable(invocationFor('git commit -m "x" --pathspec-from-file=paths.txt'), authorGuard), false);
  assert.equal(isArgvClassifiable(invocationFor('git commit --pathspec-from-file paths.txt -m "x"'), authorGuard), false, 'two-token form must also be caught');
  assert.equal(isArgvClassifiable(invocationFor('git commit -m "x" --pathspec-from-file=p --pathspec-file-nul'), authorGuard), false);
  // And the exempt-tier gate itself must refuse to exempt a docs-only staged set under this argv:
  const psInv = invocationFor('git commit -m "docs: x [FEAT-013]" --pathspec-from-file=paths.txt');
  assert.equal(isExemptCommit(['README.md'], psInv, authorGuard), false, 'a docs-only staged set is NOT exempt when --pathspec-from-file can smuggle other paths');
  // Regression guard: an ordinary -m commit stays classifiable (fix must not over-broaden).
  assert.equal(isArgvClassifiable(invocationFor('git commit -m "docs: x [FEAT-013]"'), authorGuard), true);
});

test('BUG-224 round-4 unit: getCommitInvocation resolves an inline -c alias override and retains verbWord for alias detection', () => {
  const inv = getCommitInvocation('git -c alias.ca="commit -a" ca -m "x"', authorGuard);
  assert.ok(inv, 'inline -c alias must still resolve to a commit invocation (BUG-231)');
  assert.equal(inv.verb, 'commit');
  assert.equal(inv.verbWord, 'ca', 'verbWord must be the original (aliased) word, not the resolved verb');
});

test('BUG-224 round-4: a persistent git alias for commit (body smuggling -a) is DENIED, not silently allowed', () => {
  withTempRepo((dir) => {
    // Seed a tracked internal/ file, then modify it WITHOUT staging so the `-a`
    // smuggled inside the alias body is the only thing that would stage it.
    fs.mkdirSync(path.join(dir, 'internal'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'foo.go'), 'package foo\n', 'utf8');
    git(dir, ['add', 'internal/foo.go']);
    git(dir, ['commit', '-m', 'seed']);
    fs.writeFileSync(path.join(dir, 'internal', 'foo.go'), 'package foo\n// changed\n', 'utf8');
    git(dir, ['config', 'alias.ca', 'commit -a']);
    const r = runGuard(dir, 'git ca -m "no bow tag"');
    assert.equal(r.denied, true, 'an aliased commit verb must fail closed (alias body flags are invisible)');
    assert.match(r.reason || '', /alias/i);
  });
});

test('BUG-231: an inline -c alias (git -c alias.ca="commit -a" ca) is DENIED, not silently allowed', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'foo.go'), 'package foo\n', 'utf8');
    git(dir, ['add', 'internal/foo.go']);
    git(dir, ['commit', '-m', 'seed']);
    fs.writeFileSync(path.join(dir, 'internal', 'foo.go'), 'package foo\n// changed\n', 'utf8');
    const r = runGuard(dir, 'git -c alias.ca="commit -a" ca -m "no bow tag"');
    assert.equal(r.denied, true, 'an inline -c alias must be resolved AND denied (total non-recognition is the bypass)');
  });
});

// ---------------------------------------------------------------------------
// BUG-232: fail-closed git-recognition sweep (deny unrecognised/unresolvable
// git invocations — absorbs BUG-224/231/213/214/216/084). Built on
// claude-author-guard.js's scanGitInvocations primitive (parsed:false /
// shellEscapeAlias / shell-segment-bounded tail).
// ---------------------------------------------------------------------------

test('BUG-232 unit: failClosedSweep returns null for benign non-commit verbs, --version/--help, and non-git commands', () => {
  assert.equal(failClosedSweep('git status', authorGuard), null);
  assert.equal(failClosedSweep('git add internal/foo.go', authorGuard), null);
  assert.equal(failClosedSweep('git push origin main', authorGuard), null);
  assert.equal(failClosedSweep('git --version', authorGuard), null);
  assert.equal(failClosedSweep('git --help', authorGuard), null);
  assert.equal(failClosedSweep('npm install', authorGuard), null);
  assert.equal(failClosedSweep('git commit -m "x"', authorGuard), null);
});

test('BUG-232 unit (rule 1): failClosedSweep denies a parsed:false git invocation (unrecognised value-taking global option)', () => {
  assert.notEqual(failClosedSweep('git --config-env=alias.X=EV commit -m "x"', authorGuard), null);
  assert.notEqual(failClosedSweep('git --exec-path=/tmp commit -m "x"', authorGuard), null);
});

test('BUG-232 unit (rule 2): failClosedSweep denies a shell-escape alias body via an inline -c override', () => {
  assert.notEqual(failClosedSweep('git -c alias.ci="!git commit -a" ci -m "x"', authorGuard), null);
});

test('BUG-232 unit (rule 3): failClosedSweep denies an unrecognised verb (neither a commit verb nor in git --list-cmds)', () => {
  assert.notEqual(failClosedSweep('git committ -m "x"', authorGuard), null);
  assert.notEqual(failClosedSweep('git notarealgitverb -m "x"', authorGuard), null);
});

test('BUG-232: the three deny messages name GR#23 and BUG-232', () => {
  for (const msg of [unparseableGitDenyMessage(), shellEscapeAliasDenyMessage(), unknownGitVerbDenyMessage('committ')]) {
    assert.match(msg, /GR#23/);
    assert.match(msg, /BUG-232/);
  }
});

test('BUG-232 unit (trailing-pipe fix): classifyCommitArgv treats a trailing shell chain/redirect as plumbing, not a bare pathspec', () => {
  assert.equal(isArgvClassifiable(invocationFor('git commit -m "docs: x" && git push'), authorGuard), true);
  assert.equal(isArgvClassifiable(invocationFor('git commit -m "docs: x" 2>&1 | tail'), authorGuard), true);
  // Regression guard: a REAL pathspec still fails closed (the fix must not over-broaden).
  assert.equal(isArgvClassifiable(invocationFor('git commit -m "docs: x" some/file.go'), authorGuard), false);
  assert.equal(isArgvClassifiable(invocationFor('git commit -m "docs: x" --'), authorGuard), false);
});

test('BUG-232 trailing-pipe RED (pre-fix): the pre-fix classifier (unbounded suffix) tokenizes the shell-chain tokens as commit argv — proves the fix is load-bearing', () => {
  const preFixTokens = (cmd) => {
    const inv = authorGuard.findCommitInvocation(cmd);
    return authorGuard.tokenize(inv.text.slice(inv.suffixStart)).filter((t) => t !== '');
  };
  // The pre-fix (unbounded) token stream includes the shell-chain tokens that
  // classifyCommitArgv would read as a bare pathspec -> the BUG-214 false deny.
  assert.ok(preFixTokens('git commit -m "x" && git push').includes('&&'), 'pre-fix: `&&` leaks into the argv token stream');
  assert.ok(preFixTokens('git commit -m "x" 2>&1 | tail').includes('2>&1'), 'pre-fix: `2>&1` leaks into the argv token stream');
  // Control: the fixed classifier reads neither shape as a bare pathspec.
  assert.equal(isArgvClassifiable(invocationFor('git commit -m "x" && git push'), authorGuard), true);
  assert.equal(isArgvClassifiable(invocationFor('git commit -m "x" 2>&1 | tail'), authorGuard), true);
});

test('BUG-232 end-to-end (rule 1): a commit hidden behind an unparseable --config-env=... global option is DENIED, even for a docs-only staged set (pre-fix: silently allowed)', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'docs/notes.md', '# notes\n');
    const r = runGuard(dir, 'git --config-env=alias.X=EV commit -m "no tag"');
    assert.equal(r.denied, true, 'an unparseable git invocation must fail closed, not be treated as a non-commit');
    assert.match(r.reason || '', /BUG-232|parse|unparseable/i);
  });
});

test('BUG-232 end-to-end (rule 2): a shell-escape alias body (!git commit -a) is DENIED, even for a docs-only staged set (pre-fix: silently allowed)', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'docs/notes.md', '# notes\n');
    git(dir, ['config', 'alias.ca', '!git commit -a']);
    const r = runGuard(dir, 'git ca -m "no tag"');
    assert.equal(r.denied, true, 'a shell-escape alias must fail closed (its body cannot be enumerated)');
    assert.match(r.reason || '', /alias|shell/i);
  });
});

test('BUG-232 end-to-end (rule 3): an unrecognised git verb (typo, not in git --list-cmds) is DENIED, even for a docs-only staged set (pre-fix: silently allowed)', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'docs/notes.md', '# notes\n');
    const r = runGuard(dir, 'git committ -m "no tag"');
    assert.equal(r.denied, true, 'an unrecognised verb must fail closed, not be treated as a non-commit');
    assert.match(r.reason || '', /committ/);
  });
});

test('BUG-232 end-to-end: a bare `git --version` / `git --help` is still silently allowed (the benign exception)', () => {
  withTempRepo((dir) => {
    for (const cmd of ['git --version', 'git --help']) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, false, `${cmd} must remain allowed`);
      assert.equal(r.stdout, '');
    }
  });
});

test('BUG-232 end-to-end (trailing-pipe fix): a docs-only commit with a trailing `2>&1 | tail` / `&& git push` is silently ALLOWED (pre-fix: falsely denied as a bare pathspec)', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'docs/notes.md', '# notes\n');
    for (const cmd of ['git commit -m "docs only" 2>&1 | tail', 'git commit -m "docs only" && git push']) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, false, `must not falsely deny: ${cmd}`);
      assert.equal(r.stdout, '');
    }
  });
});

// ---------------------------------------------------------------------------
// BUG-332: "does the accepted verdict COVER THIS WORK?" (three outcomes)
//
// The hole: commit c6f2088 landed code-bearing paths on public main tagged
// [engine.core], which resolves to MOD-012 — a module CLOSED on 2026-08-13
// carrying an `accept` from 2026-08-18 for entirely different code — while the
// item that actually tracked the work (FEAT-215) held ZERO verdicts. The
// pre-fix rule ("the item's LATEST verdict is accept") is a statement about
// the item's history, not about the diff being proposed.
//
// Each test below was mutation-proved: with the corresponding check removed
// from a scratch copy of the guard, it goes red.
// ---------------------------------------------------------------------------

const {
  OUTCOME,
  classifyCoverage,
  latestLandedRef,
  toTimeMs,
  TERMINAL_ITEM_STATUSES,
  KNOWN_ITEM_STATUSES,
} = guard;

const BUG332_T0 = new Date('2026-08-18T20:44:22Z');
const BUG332_T1 = new Date('2026-08-20T10:00:00Z');
const bug332OpenItem = { code: 'FEAT-215', title: 'live item', status: 'open' };
const bug332DoneItem = { code: 'MOD-012', title: 'closed module', status: 'done' };
const bug332Accept = (at) => ({ verdict: 'accept', created_at: at });

test('BUG-332 unit: an unresolvable tag is BLOCKED (never PASSED) and the message names the tag', () => {
  const r = classifyCoverage({ tag: 'FEAT-9999', item: null, verdict: null, landedRef: null });
  assert.equal(r.outcome, OUTCOME.BLOCKED);
  assert.equal(r.reason, 'unresolved');
  const msg = guard.coverageDenyMessage([r]);
  assert.match(msg, /\[FEAT-9999\]/);
  assert.match(msg, /use a REAL item code/i);
});

test('BUG-332 unit: the c6f2088 shape — a DONE item with a historical accept cannot cover new code', () => {
  const r = classifyCoverage({ tag: 'engine.core', item: bug332DoneItem, verdict: bug332Accept(BUG332_T0), landedRef: null });
  assert.equal(r.outcome, OUTCOME.BLOCKED);
  assert.equal(r.reason, 'terminal-item');
  assert.match(guard.coverageDenyMessage([r]), /already CLOSED/);
});

test('BUG-332 unit: a CANCELLED item is terminal too', () => {
  const r = classifyCoverage({ tag: 'x.y', item: { ...bug332DoneItem, status: 'cancelled' }, verdict: bug332Accept(BUG332_T0), landedRef: null });
  assert.equal(r.outcome, OUTCOME.BLOCKED);
  assert.equal(r.reason, 'terminal-item');
});

test('BUG-332 unit: a LIVE item with an accept and no recorded commit ref PASSES', () => {
  for (const status of ['open', 'in_progress', 'blocked']) {
    const r = classifyCoverage({ tag: 'FEAT-215', item: { ...bug332OpenItem, status }, verdict: bug332Accept(BUG332_T0), landedRef: null });
    assert.equal(r.outcome, OUTCOME.PASSED, `status ${status} must pass`);
  }
});

test('BUG-332 unit: an accept OLDER than the item last LANDED commit is stale -> BLOCKED', () => {
  const r = classifyCoverage({ tag: 'FEAT-215', item: bug332OpenItem, verdict: bug332Accept(BUG332_T0), landedRef: { commit_hash: 'deadbeefdeadbeef', created_at: BUG332_T1 } });
  assert.equal(r.outcome, OUTCOME.BLOCKED);
  assert.equal(r.reason, 'stale-verdict');
  assert.match(guard.coverageDenyMessage([r]), /do NOT cover this work/);
});

// BUG-332 SOFTENING (lead ruling 2026-08-21): staleness is measured against
// LANDED work only, so main() passes landedRef=null when nothing the item has
// recorded is on origin/main — a wave of local commits after one accepted
// round is this team's normal rhythm and must not be blocked.
test('BUG-332 softening unit: local-only refs mean landedRef=null, and the accept then still covers the work', () => {
  const r = classifyCoverage({ tag: 'FEAT-215', item: bug332OpenItem, verdict: bug332Accept(BUG332_T0), landedRef: null, landingProblem: null });
  assert.equal(r.outcome, OUTCOME.PASSED);
});

test('BUG-332 softening unit: an undetermined landing is COULD-NOT-EVALUATE, never PASSED', () => {
  const r = classifyCoverage({ tag: 'FEAT-215', item: bug332OpenItem, verdict: bug332Accept(BUG332_T0), landedRef: null, landingProblem: 'origin/main could not be resolved' });
  assert.equal(r.outcome, OUTCOME.COULD_NOT_EVALUATE);
  assert.equal(r.reason, 'undetermined-landing');
  assert.match(guard.couldNotEvaluateDenyMessage([r]), /origin\/main could not be resolved/);
});

test('BUG-332 unit: an accept NEWER than the item last landed commit covers the work -> PASSED', () => {
  const r = classifyCoverage({ tag: 'FEAT-215', item: bug332OpenItem, verdict: bug332Accept(BUG332_T1), landedRef: { commit_hash: 'deadbeef', created_at: BUG332_T0 } });
  assert.equal(r.outcome, OUTCOME.PASSED);
});

test('BUG-332 unit: no verdict / a REJECT verdict on a live item are both BLOCKED', () => {
  const none = classifyCoverage({ tag: 'FEAT-215', item: bug332OpenItem, verdict: null, landedRef: null });
  assert.equal(none.outcome, OUTCOME.BLOCKED);
  assert.equal(none.reason, 'no-verdict');
  const rej = classifyCoverage({ tag: 'FEAT-215', item: bug332OpenItem, verdict: { verdict: 'reject', created_at: BUG332_T1 }, landedRef: null });
  assert.equal(rej.outcome, OUTCOME.BLOCKED);
  assert.equal(rej.reason, 'rejected');
});

test('BUG-332 unit: COULD-NOT-EVALUATE is a distinct third outcome, never folded into PASSED', () => {
  const cases = [
    ['unreadable status', { tag: 't', item: { code: 'X-1', title: '', status: 'weird-new-status' }, verdict: bug332Accept(BUG332_T1), landedRef: null }],
    ['unreadable verdict value', { tag: 't', item: bug332OpenItem, verdict: { verdict: 42, created_at: BUG332_T1 }, landedRef: null }],
    ['unreadable verdict time', { tag: 't', item: bug332OpenItem, verdict: { verdict: 'accept', created_at: 'not-a-date' }, landedRef: null }],
    ['unreadable ref time', { tag: 't', item: bug332OpenItem, verdict: bug332Accept(BUG332_T1), landedRef: { commit_hash: 'a', created_at: {} } }],
  ];
  for (const [label, input] of cases) {
    const r = classifyCoverage(input);
    assert.equal(r.outcome, OUTCOME.COULD_NOT_EVALUATE, `${label} must be COULD-NOT-EVALUATE`);
    assert.notEqual(r.outcome, OUTCOME.PASSED);
  }
  assert.notEqual(OUTCOME.COULD_NOT_EVALUATE, OUTCOME.PASSED);
  assert.equal(new Set(Object.values(OUTCOME)).size, 3);
  const msg = guard.couldNotEvaluateDenyMessage([{ tag: 't', detail: 'because reasons' }]);
  assert.match(msg, /COULD-NOT-EVALUATE/);
  assert.match(msg, /treated as BLOCKED/i);
});

test('BUG-332 unit: toTimeMs reads Date/SQL-string/epoch and returns null for anything unreadable', () => {
  assert.equal(toTimeMs(new Date('2026-08-18T20:44:22Z')), Date.parse('2026-08-18T20:44:22Z'));
  assert.equal(toTimeMs('2026-08-18 20:44:22'), Date.parse('2026-08-18T20:44:22'));
  assert.equal(toTimeMs(1755000000000), 1755000000000);
  for (const bad of [null, undefined, {}, [], 'not-a-date', NaN, new Date('nope')]) {
    assert.equal(toTimeMs(bad), null, `${String(bad)} must be unreadable`);
  }
  assert.ok(TERMINAL_ITEM_STATUSES.has('done') && TERMINAL_ITEM_STATUSES.has('cancelled'));
  for (const s of TERMINAL_ITEM_STATUSES) assert.ok(KNOWN_ITEM_STATUSES.has(s));
});

/**
 * BUG-332 softening fixture: a working repo that has a REAL origin whose
 * `main` is a genuine remote-tracking ref, so "has this recorded commit
 * landed?" is a question git can actually answer here. Yields
 * { dir, originDir, landedHash, localHash } — landedHash IS origin/main,
 * localHash is a commit that exists only in the working clone.
 *
 * Async by construction: these tests interleave DB writes with guard runs, and
 * a sync `finally { rmSync }` would delete the repo out from under a pending
 * promise (the fixture would then "pass" against a directory that no longer
 * exists — a fake-verification shape this file exists to avoid).
 */
async function withTempRepoOnOrigin(fn) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'destructive-guard-origin-'));
  const originDir = path.join(root, 'origin');
  const dir = path.join(root, 'work');
  try {
    fs.mkdirSync(originDir);
    git(originDir, ['init', '-b', 'main', '.']);
    git(originDir, ['config', 'user.name', 'Fixture Contributor']);
    git(originDir, ['config', 'user.email', 'fixture@example.invalid']);
    fs.writeFileSync(path.join(originDir, 'seed.txt'), 'seed\n', 'utf8');
    git(originDir, ['add', 'seed.txt']);
    git(originDir, ['commit', '-m', 'seed origin main']);
    const landedHash = git(originDir, ['rev-parse', 'HEAD']);

    git(root, ['clone', '--quiet', originDir, 'work']);
    git(dir, ['config', 'user.name', 'Fixture Contributor']);
    git(dir, ['config', 'user.email', 'fixture@example.invalid']);
    fs.mkdirSync(path.join(dir, '.claude'), { recursive: true });
    fs.writeFileSync(path.join(dir, '.claude', 'settings.json'), defaultSettingsJson(), 'utf8');
    git(dir, ['add', '.claude/settings.json']);
    git(dir, ['commit', '-m', 'seed settings.json (local only)']);
    const localHash = git(dir, ['rev-parse', 'HEAD']);

    return await fn({ dir, originDir, landedHash, localHash });
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
}

test('BUG-332 softening end-to-end: a ref that is only LOCAL leaves the accept valid; the SAME shape once the ref is ON ORIGIN/MAIN is BLOCKED', async () => {
  const db = await connectDb();
  const item = await createFixtureItem(db, 'bug332soft');
  const insertRef = (hash) => db.query(
    'INSERT INTO bow_git_refs (item_guid, commit_hash, branch, note, created_at) VALUES (?, ?, ?, ?, DATE_ADD(NOW(), INTERVAL 60 SECOND))',
    [item.guid, hash, 'main', 'BUG-332 softening fixture']);
  const clearRefs = () => db.query('DELETE FROM bow_git_refs WHERE item_guid = ?', [item.guid]);
  try {
    await recordDestructiveVerdict(db, item.code, { verdict: 'accept', attacker: 'BUG-332 softening test' });

    await withTempRepoOnOrigin(async ({ dir, landedHash, localHash }) => {
      // (1) THE SOFTENING: the item's newest ref is a commit that exists only
      // locally — the second commit of a wave the attacker already rounded.
      // The accept still covers it.
      await insertRef(localHash);
      stageFile(dir, 'internal/engine/core/clock.go', 'package core\n');
      let r = runGuard(dir, `git commit -m "fix: second commit of the same wave [${item.code}]"`);
      assert.equal(r.denied, false, `a ref that is only LOCAL must not make the accept stale: ${r.reason}`);

      // (2) THE HAZARD, unchanged: the same ref, once it is on origin/main,
      // makes that accept attest shipped work — BLOCKED.
      await clearRefs();
      await insertRef(landedHash);
      r = runGuard(dir, `git commit -m "fix: new work after the wave landed [${item.code}]"`);
      assert.equal(r.denied, true, 'a verdict older than a LANDED commit ref must BLOCK');
      assert.match(r.reason, /do NOT cover this work/);

      // (3) EDGE CASE (a): a ref whose commit is not in the local object
      // database at all (never fetched / another lane's worktree) cannot be
      // shown to have landed, so it does not count toward staleness.
      await clearRefs();
      await insertRef('b'.repeat(40));
      r = runGuard(dir, `git commit -m "fix: unknown-object ref [${item.code}]"`);
      assert.equal(r.denied, false, `an unfetchable ref must not count as landed: ${r.reason}`);

      // (4) Newest-first wins: a landed ref newer than the accept blocks even
      // when a local-only ref was recorded after it.
      await clearRefs();
      await insertRef(landedHash);
      await insertRef(localHash);
      r = runGuard(dir, `git commit -m "fix: local ref on top of a landed one [${item.code}]"`);
      assert.equal(r.denied, true, 'a landed ref under a local one must still block');
      assert.match(r.reason, /do NOT cover this work/);
    });
  } finally {
    await clearRefs();
    await db.query('DELETE FROM bow_destructive_verdicts WHERE item_guid = ?', [item.guid]);
    await deleteFixtureItem(db, item.guid);
    await db.end();
  }
});

test('BUG-332 end-to-end: live item + fresh accept ALLOWS; a repo where origin/main cannot be resolved is COULD-NOT-EVALUATE -> BLOCKED; once DONE it is BLOCKED', async () => {
  const db = await connectDb();
  const item = await createFixtureItem(db, 'bug332');
  try {
    await recordDestructiveVerdict(db, item.code, { verdict: 'accept', attacker: 'BUG-332 regression test' });

    withTempRepo((dir) => {
      stageFile(dir, 'internal/engine/core/clock.go', 'package core\n');
      const r = runGuard(dir, `git commit -m "fix: real work [${item.code}]"`);
      assert.equal(r.denied, false, `a covered code-bearing commit must PASS: ${r.reason}`);
    });

    // EDGE CASE (c): withTempRepo has NO origin, so with a ref on file the
    // guard cannot tell whether that commit landed. "Cannot tell" is BLOCKED
    // with the COULD-NOT-EVALUATE message, never folded into a pass (BUG-071).
    await db.query(
      'INSERT INTO bow_git_refs (item_guid, commit_hash, branch, note, created_at) VALUES (?, ?, ?, ?, DATE_ADD(NOW(), INTERVAL 60 SECOND))',
      [item.guid, 'b'.repeat(40), 'main', 'BUG-332 regression fixture']);
    withTempRepo((dir) => {
      stageFile(dir, 'internal/engine/core/clock.go', 'package core\n');
      const r = runGuard(dir, `git commit -m "fix: more work [${item.code}]"`);
      assert.equal(r.denied, true, 'an unresolvable origin/main must BLOCK, not pass');
      assert.match(r.reason, /COULD-NOT-EVALUATE/);
      assert.match(r.reason, /origin\/main/);
    });

    await db.query('DELETE FROM bow_git_refs WHERE item_guid = ?', [item.guid]);
    await db.query("UPDATE bow_items SET status = 'done' WHERE guid = ?", [item.guid]);
    withTempRepo((dir) => {
      stageFile(dir, 'internal/engine/core/clock.go', 'package core\n');
      const r = runGuard(dir, `git commit -m "fix: post-close work [${item.code}]"`);
      assert.equal(r.denied, true, 'a DONE item verdict must not cover new code');
      assert.match(r.reason, /already CLOSED/);
    });
  } finally {
    await db.query('DELETE FROM bow_git_refs WHERE item_guid = ?', [item.guid]);
    await db.query('DELETE FROM bow_destructive_verdicts WHERE item_guid = ?', [item.guid]);
    await deleteFixtureItem(db, item.guid);
    await db.end();
  }
});

test('BUG-332 end-to-end: the FEAT-077 proportionality exemptions and the operator escape hatch are untouched', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'docs/notes.md', '# notes\n');
    assert.equal(runGuard(dir, 'git commit -m "docs: notes"').denied, false);
  });
  withTempRepo((dir) => {
    stageFile(dir, 'internal/engine/core/clock_test.go', 'package core\n');
    assert.equal(runGuard(dir, 'git commit -m "test: cover clock"').denied, false);
  });
  withTempRepo((dir) => {
    stageFile(dir, 'internal/engine/core/clock.go', 'package core\n');
    assert.equal(runGuard(dir, 'git commit -m "feat: no tag"', { CLAUDE_DISABLE_DESTRUCTIVE_GUARD: '1' }).denied, false);
  });
});

test('BUG-332: claude-bow.js exports listGitRefs and the guard requires it (a missing export must deny, not degrade)', () => {
  assert.equal(typeof bow.listGitRefs, 'function');
  const src = fs.readFileSync(GUARD_PATH, 'utf8');
  assert.match(src, /requiredBowFns\s*=\s*\[[^\]]*'listGitRefs'/);
});

// ---------------------------------------------------------------------------
// BUG-332 softening: the git half of the rule — "has this recorded ref
// actually landed on origin/main?" — against real repositories, because the
// three awkward cases (unknown object, rebased-under-a-new-hash, no
// origin/main at all) are exactly the ones a fixture object cannot fake.
// ---------------------------------------------------------------------------

test('BUG-332 softening: refLandingStatus answers landed / not-landed / unreadable-hash against a real repo, including the REBASED-under-a-new-hash case', async () => {
  await withTempRepoOnOrigin(async ({ dir, originDir, landedHash, localHash }) => {
    const originMain = guard.resolveOriginMain(dir);
    assert.equal(originMain, landedHash, 'the clone origin/main must resolve to the seeded commit');

    assert.equal(guard.refLandingStatus(dir, landedHash, originMain), 'landed');
    assert.equal(guard.refLandingStatus(dir, landedHash.slice(0, 7), originMain), 'landed', 'abbreviated hashes (what claude-bow.js actually records) must resolve');
    assert.equal(guard.refLandingStatus(dir, localHash, originMain), 'not-landed', 'a local-only commit has not landed');
    assert.equal(guard.refLandingStatus(dir, 'b'.repeat(40), originMain), 'not-landed', 'a commit absent from the local object database cannot be shown to have landed');
    assert.equal(guard.refLandingStatus(dir, 'not-a-hash', originMain), 'unreadable-hash');
    assert.equal(guard.refLandingStatus(dir, '', originMain), 'unreadable-hash');

    // EDGE CASE (b): this repo merges with `gh pr merge --rebase`, so a ref
    // recorded on a lane branch lands under a DIFFERENT hash. Exact-hash
    // ancestry says no; patch equivalence says yes, and yes is the truth.
    git(dir, ['checkout', '--quiet', '-b', 'lane', 'origin/main']);
    fs.writeFileSync(path.join(dir, 'lane.txt'), 'lane work\n', 'utf8');
    git(dir, ['add', 'lane.txt']);
    git(dir, ['commit', '-m', 'lane work']);
    const laneHash = git(dir, ['rev-parse', 'HEAD']);
    // Move origin/main on first, so the replay lands on a different parent and
    // the hash genuinely changes — which is what a rebase merge does.
    fs.writeFileSync(path.join(originDir, 'other.txt'), 'someone else\n', 'utf8');
    git(originDir, ['add', 'other.txt']);
    git(originDir, ['commit', '-m', 'another lane landed first']);
    git(originDir, ['fetch', '--quiet', dir.replace(/\\/g, '/'), 'lane']);
    git(originDir, ['cherry-pick', 'FETCH_HEAD']); // rewrites the hash, same patch
    const rebasedHash = git(originDir, ['rev-parse', 'HEAD']);
    assert.notEqual(rebasedHash, laneHash, 'the fixture must actually change the hash');
    git(dir, ['fetch', '--quiet', 'origin']);
    const originMain2 = guard.resolveOriginMain(dir);
    assert.equal(originMain2, rebasedHash);
    assert.equal(guard.refLandingStatus(dir, laneHash, originMain2), 'landed', 'a rebased ref that exists on main under another hash HAS landed');
  });
});

test('BUG-332 softening: latestLandedRef picks the newest LANDED row, reports a problem when it cannot tell, and is bounded', async () => {
  await withTempRepoOnOrigin(async ({ dir, originDir, landedHash, localHash }) => {
    const t = (s) => new Date(Date.now() + s * 1000);
    // Newest-first input (listGitRefs()'s contract).
    const refs = [
      { commit_hash: localHash, created_at: t(20) },
      { commit_hash: landedHash, created_at: t(10) },
    ];
    assert.equal(latestLandedRef(dir, refs).ref.commit_hash, landedHash, 'the local row is skipped, the landed one is the answer');
    assert.equal(latestLandedRef(dir, [refs[0]]).ref, null, 'nothing landed -> no staleness input');
    assert.equal(latestLandedRef(dir, []).ref, null);

    // EDGE CASE (c): no origin/main at all -> a PROBLEM, never a null ref
    // (which would read as "nothing landed" and silently pass).
    const noOrigin = latestLandedRef(originDir, refs);
    assert.equal(noOrigin.ref, undefined);
    assert.match(noOrigin.problem, /origin\/main could not be resolved/);

    // An unreadable hash is a problem, not a shrug.
    assert.match(latestLandedRef(dir, [{ commit_hash: 'nope', created_at: t(1) }]).problem, /unreadable commit_hash/);

    // Bounded: more rows than the budget, none landed -> a problem, not a
    // silent "nothing landed".
    const many = Array.from({ length: guard.LANDING_REF_BUDGET + 1 }, () => ({ commit_hash: localHash, created_at: t(1) }));
    assert.match(latestLandedRef(dir, many).problem, /more than this guard checks/);
  });
});
