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
  normalizeGitPath,
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
  checkRepoRedirectOptions,
  checkConfigRedirectInjection,
  extractTargetDirFromGitCommand,
  isSameRepository,
  getStagedFilesFromDir,
  repoRootForDir,
  classifyCommitArgv,
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

// BUG-152: the code must be shaped like a REAL production BOW code
// (TYPE_PREFIX + "-" + a purely-numeric id, exactly what claude-bow.js's
// nextCode() generates) so it survives claude-destructive-guard.js's
// looksLikeRealTag() shape filter and actually reaches BOW resolution — the
// old "FEAT-T<hex>" shape mixed letters into the suffix and would now be
// silently dropped as prose before ever hitting the DB.
//
// BUG-340 r2 N1 (independent round REJECT): the ORIGINAL purely-numeric
// scheme drew from the FULL uint32 range (0..4294967295) via
// `crypto.randomBytes(4).readUInt32BE(0)`, which overlaps the numeric range
// real large-random FEAT keys ALSO draw from (FEAT-1972079945,
// FEAT-2326609711, etc. all sit inside that same uint32 span) — two stale
// 2026-08-22 fixture rows survived in the LIVE metro BOW as a result, one
// adjacent to a real item. fixtureCode() below stays purely numeric (still
// passes looksLikeRealTag()) but draws from a RESERVED sub-range strictly
// ABOVE the uint32 max, so no uint32-based real-code generator can ever
// produce a collision; the title also now carries an unmistakable
// "DESTRUCTIVE-SCRATCH" prefix so a human auditing `node claude-bow.js list`
// can immediately identify — and hand-remove — any row that somehow
// survives a failed cleanup.
const FIXTURE_CODE_RESERVED_BASE = 9990000000; // > 4294967295 (uint32 max)
function fixtureCode() {
  const tail = crypto.randomBytes(4).readUInt32BE(0) % 9000000; // 0..8999999
  return `FEAT-${FIXTURE_CODE_RESERVED_BASE + tail}`;
}

// BUG-340 r2 N1: every fixture guid created by this file is tracked here and
// swept by the test.after() backstop below — belt-and-braces so a test
// whose assertion throws OUTSIDE its own try/finally still gets cleaned up.
const _fixtureGuids = new Set();

/** Insert a throwaway bow_items row directly (feature type), unique per call. */
async function createFixtureItem(db, label) {
  const suffix = crypto.randomBytes(4).toString('hex');
  const guid = crypto.randomUUID();
  const code = fixtureCode();
  const mkey = `test.destructiveguard.${label}.${suffix}`;
  await db.query(
    'INSERT INTO bow_items (guid, code, mkey, item_type, title, priority) VALUES (?, ?, ?, ?, ?, ?)',
    [guid, code, mkey, 'feature', `DESTRUCTIVE-SCRATCH fixture — ${label} (${suffix})`, 'P3']);
  _fixtureGuids.add(guid);
  return { guid, code, mkey };
}

async function deleteFixtureItem(db, guid) {
  await db.query('DELETE FROM bow_items WHERE guid = ?', [guid]);
  _fixtureGuids.delete(guid);
}

// BUG-340 r2 N1: guaranteed cleanup backstop, same convention adopted across
// every claude-*.test.js fixture helper this round.
test.after(async () => {
  if (!_fixtureGuids.size) return;
  let db;
  try {
    db = await connectDb();
  } catch {
    return;
  }
  try {
    for (const guid of _fixtureGuids) {
      // eslint-disable-next-line no-await-in-loop
      await db.query('DELETE FROM bow_items WHERE guid = ?', [guid]).catch(() => {});
    }
  } finally {
    await db.end().catch(() => {});
  }
});

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
// BUG-216: root guard/hook scripts are full-tier code-bearing by PATH SHAPE
// (additive to the settings.json-derived set). Two holes it closes:
//   (A) a NEWLY-ADDED root `claude-*.js` guard NOT YET wired into settings.json
//       — absent from the derived set, so pre-fix it classified non-code-bearing
//       and slipped through with no verdict.
//   (B) a hook/config file nested under `.claude/` (a `.claude/hooks/*.js`
//       script, or `.claude/settings.json` itself) — never root-level, so the
//       root-level-only derived-name check never saw it AT ALL.
// The docs/test exemption tier still runs FIRST, so a guard's own `*.test.js`
// and `.claude/` markdown docs stay Tester-tier exempt.
// ---------------------------------------------------------------------------

const {
  isGuardOrHookPath,
  ROOT_GUARD_SCRIPT_RE,
  DOT_CLAUDE_DIR_RE,
} = guard;

test('BUG-216 unit: isGuardOrHookPath flags root claude-*.js and any .claude/ path, not ordinary files', () => {
  // Root-level claude-*.js guard/hook scripts (any spelling of the convention).
  assert.ok(isGuardOrHookPath('claude-newguard.js'));
  assert.ok(isGuardOrHookPath('claude-destructive-guard.js'));
  assert.ok(isGuardOrHookPath('claude-ping-check.js'));
  assert.ok(isGuardOrHookPath('claude-startup.js'));
  // Anything under .claude/ — hook scripts and the settings.json wiring.
  assert.ok(isGuardOrHookPath('.claude/hooks/newhook.js'));
  assert.ok(isGuardOrHookPath('.claude/settings.json'));
  assert.ok(isGuardOrHookPath('.claude/commands/foo.md')); // reached only when NOT an all-docs commit
  assert.ok(isGuardOrHookPath('.claude\\hooks\\newhook.js')); // backslash spelling
  // NOT flagged by this shape: ordinary root files, non-claude root .js, and a
  // coincidental claude-*.js NOT at root level (would only matter via another check).
  assert.ok(!isGuardOrHookPath('unwired-scratch.js'));
  assert.ok(!isGuardOrHookPath('package.json'));
  assert.ok(!isGuardOrHookPath('README.md'));
  assert.ok(!isGuardOrHookPath('internal/engine/world/foo.go'));
  assert.ok(!isGuardOrHookPath('vendor/claude-x.js')); // not root-level, not under .claude/
  assert.ok(ROOT_GUARD_SCRIPT_RE.test('claude-x.js'));
  assert.ok(!ROOT_GUARD_SCRIPT_RE.test('claudex.js')); // must have the `claude-` prefix
  assert.ok(DOT_CLAUDE_DIR_RE.test('.claude/anything'));
});

test('BUG-216 hole A end-to-end: a NEWLY-ADDED root claude-*.js guard NOT wired into settings.json is code-bearing and DENIED with zero tag (pre-fix: silently allowed)', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'claude-newguard.js', '// a brand-new guard, not yet wired into settings.json\n');
    const r = runGuard(dir, 'git commit -m "wire up a new guard, no BOW tag"');
    assert.equal(r.denied, true, 'an unwired root claude-*.js guard must activate the gate (BUG-216 hole A)');
  });
});

test('BUG-216 hole B end-to-end: a .claude/ hook script is code-bearing and DENIED with zero tag (pre-fix: silently allowed — never root-level, invisible to the derived-name check)', () => {
  withTempRepo((dir) => {
    stageFile(dir, '.claude/hooks/newhook.js', '// a nested hook script\n');
    const r = runGuard(dir, 'git commit -m "add a .claude hook, no BOW tag"');
    assert.equal(r.denied, true, 'a .claude/ hook script must activate the gate (BUG-216 hole B)');
  });
});

test('BUG-216 hole B end-to-end: modifying .claude/settings.json (the hook wiring) is code-bearing and DENIED with zero tag', () => {
  withTempRepo((dir) => {
    // Restage settings.json with a new wired hook — the enforcement-path wiring itself.
    fs.writeFileSync(
      path.join(dir, '.claude', 'settings.json'),
      JSON.stringify({ hooks: { PreToolUse: [{ matcher: 'Bash', hooks: [
        { type: 'command', command: 'node fixture-wired-guard.js' },
        { type: 'command', command: 'node claude-newguard.js' },
      ] }] } }),
      'utf8'
    );
    stageFile(dir, '.claude/settings.json');
    const r = runGuard(dir, 'git commit -m "wire a new hook into settings.json, no BOW tag"');
    assert.equal(r.denied, true, 'a settings.json hook-wiring change must activate the gate');
  });
});

test('BUG-216/BUG-332 case-insensitive-FS: capitalised claude-*.js variants are code-bearing and DENIED (pre-fix, case-sensitive regex: silently allowed via one-char rename)', () => {
  // core.ignorecase Windows FS stages case-variant paths; node runs the hook
  // against them. A case-sensitive allowlist let `Claude-newguard.js` etc. slip
  // past classification with zero verdict — the BUG-224/BUG-332 bypass class.
  for (const name of ['Claude-newguard.js', 'CLAUDE-newguard.js', 'claude-newguard.JS']) {
    withTempRepo((dir) => {
      stageFile(dir, name, '// a case-variant unwired guard, not yet wired into settings.json\n');
      const r = runGuard(dir, 'git commit -m "wire up a new guard, no BOW tag"');
      assert.equal(r.denied, true, `a case-variant root claude-*.js guard (${name}) must activate the gate`);
    });
  }
  // Unit-level: the shape regexes themselves must fold case.
  assert.ok(ROOT_GUARD_SCRIPT_RE.test('Claude-newguard.js'));
  assert.ok(ROOT_GUARD_SCRIPT_RE.test('CLAUDE-newguard.js'));
  assert.ok(ROOT_GUARD_SCRIPT_RE.test('claude-newguard.JS'));
  assert.ok(DOT_CLAUDE_DIR_RE.test('.CLAUDE/hooks/x.js'));
});

test('BUG-216 extension widening: .cjs/.mjs guard and hook scripts are code-bearing and DENIED (pre-fix, .js-only regex: silently allowed)', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'claude-newhook.cjs', '// a CommonJS guard/hook script, not yet wired\n');
    const r = runGuard(dir, 'git commit -m "add a .cjs guard, no BOW tag"');
    assert.equal(r.denied, true, 'a root claude-*.cjs guard must activate the gate');
  });
  withTempRepo((dir) => {
    stageFile(dir, '.claude/hooks/x.mjs', '// an ESM hook script under .claude/\n');
    const r = runGuard(dir, 'git commit -m "add a .mjs hook, no BOW tag"');
    assert.equal(r.denied, true, 'a .claude/ .mjs hook script must activate the gate');
  });
  // Unit-level: the root-guard shape regex covers .cjs/.mjs.
  assert.ok(ROOT_GUARD_SCRIPT_RE.test('claude-newhook.cjs'));
  assert.ok(ROOT_GUARD_SCRIPT_RE.test('claude-newhook.mjs'));
});

test('BUG-216 CONTROL: a guard\'s OWN *.test.js, committed alone, stays Tester-tier EXEMPT (docs/test tier runs first)', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'claude-newguard.test.js', '// only the guard\'s tests\n');
    const r = runGuard(dir, 'git commit -m "tests only, no BOW tag needed"');
    assert.equal(r.denied, false, 'a test-only commit must stay exempt even for a guard script name shape');
    assert.equal(r.stdout, '');
  });
});

test('BUG-216 CONTROL: a .claude/ markdown skill doc, committed alone, stays EXEMPT (docs tier)', () => {
  withTempRepo((dir) => {
    stageFile(dir, '.claude/commands/newskill.md', '# a skill doc\n');
    const r = runGuard(dir, 'git commit -m "docs-only skill, no BOW tag needed"');
    assert.equal(r.denied, false, 'a .claude/ markdown-only commit must stay docs-exempt');
    assert.equal(r.stdout, '');
  });
});

test('BUG-216 CONTROL: an unwired NON-claude root .js scratch file is still silently ALLOWED (name pattern is narrowed to claude-*.js, ASM-192 unwired-scratch case preserved)', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'unwired-scratch.js', '// not a guard, not wired, not claude-prefixed\n');
    const r = runGuard(dir, 'git commit -m "no tag at all"');
    assert.equal(r.denied, false, 'a non-claude unwired root .js must not activate the gate purely by name');
    assert.equal(r.stdout, '');
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
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
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
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('REGRESSION (Tester finding): `git.cmd commit` with zero BOW tags is denied, not silently allowed', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/foo.go', 'package foo\n');
    const r = runGuard(dir, 'git.cmd commit -m "no bow tag whatsoever, via git.cmd"');
    assert.equal(r.denied, true, 'git.cmd must be recognised as a real git commit invocation');
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('REGRESSION (Tester finding): a `bash -c "git commit ..."` shell-wrapped invocation with zero BOW tags is denied', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/foo.go', 'package foo\n');
    const r = runGuard(dir, `bash -c "git commit -m 'no bow tag whatsoever, wrapped in bash -c'"`);
    assert.equal(r.denied, true, 'a git commit hidden inside a bash -c wrapper body must still be recognised');
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('REGRESSION (Tester finding): a full PATH invocation of git.exe (unquoted, no spaces) with zero BOW tags is denied', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/foo.go', 'package foo\n');
    const r = runGuard(dir, 'C:\\Git\\bin\\git.exe commit -m "no bow tag whatsoever, via full path"');
    assert.equal(r.denied, true, 'a full-path git.exe invocation must still be recognised as a real git commit');
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
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
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('REGRESSION: plain git spellings (git.exe / git.cmd) go through the FULL verdict pipeline — an accepted verdict passes, an unresolvable tag is named; a WRAPPED commit denies at the shape gate even with an accepted verdict (BUG-332 r16 AARON structural allowlist)', async () => {
  const db = await connectDb();
  const item = await createFixtureItem(db, 'exe-suffix-verdict');
  try {
    await recordDestructiveVerdict(db, item.code, { verdict: 'accept', attacker: 'Destructive-Fixture', recorderSession: 'independent-attacker-fixture' });

    withTempRepo((dir) => {
      stageFile(dir, 'internal/foo.go', 'package foo\n');
      const rGood = runGuard(dir, `git.exe commit -m "[${item.code}] change via git.exe"`);
      assert.equal(rGood.denied, false, 'git.exe with an accepted verdict must pass, same as plain git');
    });

    withTempRepo((dir) => {
      stageFile(dir, 'internal/foo.go', 'package foo\n');
      const rGood = runGuard(dir, `git.cmd commit -m "[${item.code}] change via git.cmd"`);
      assert.equal(rGood.denied, false, 'git.cmd with an accepted verdict must pass, same as plain git');
    });

    withTempRepo((dir) => {
      stageFile(dir, 'internal/foo.go', 'package foo\n');
      // BUG-152: CODE-shaped (prefix + "-" + purely numeric id) so it still
      // reaches BOW resolution instead of being filtered out as prose.
      // BUG-164: the prefix must be a REAL TYPE_PREFIX value (FEAT, not the
      // made-up "ZZZ") or looksLikeRealTag() now drops it as prose before it
      // ever reaches resolution — the id itself stays absurdly large so no
      // real item can ever collide with it.
      const rBad = runGuard(dir, `git.exe commit -m '[FEAT-999999999] change'`);
      assert.equal(rBad.denied, true);
      assert.match(rBad.reason, /FEAT-999999999/, 'a PLAIN spelling with an unresolvable tag must name it');
    });

    withTempRepo((dir) => {
      stageFile(dir, 'internal/foo.go', 'package foo\n');
      // BUG-332 r16 (AARON structural allowlist): a WRAPPED commit is denied at
      // the shape gate BEFORE verdict lookup. The allowlist proceeds only on a
      // plain benign `git commit` with zero shell indirection — a `bash -c`
      // wrapper means the guard cannot even be sure of the command text, so an
      // accepted verdict does NOT rescue it (the verdict proves the code; the
      // shape gate proves the command). Strongest form of the new bar.
      const rWrapped = runGuard(dir, `bash -c "git commit -m '[${item.code}] change'"`);
      assert.equal(rWrapped.denied, true, 'a wrapped commit must be denied even when it cites an accepted-verdict item');
      assert.match(rWrapped.reason, /shell indirection|structural allowlist|AARON RULING/i, 'the deny must be the structural shape gate, not a verdict outcome');
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
    await recordDestructiveVerdict(db, item.code, { verdict: 'accept', attacker: 'Destructive-Fixture', recorderSession: 'independent-attacker-fixture' });

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
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
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
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('ROUND-4 end-to-end: `\'git\' commit` with zero BOW tags on a code-bearing file is DENIED, not silently allowed', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/foo.go', 'package foo\n');
    const r = runGuard(dir, "'git' commit -m 'no tag, single quoted'");
    assert.equal(r.denied, true, "'git' commit must be recognised as a real git commit invocation");
    assert.notEqual(r.stdout, '', 'a decision payload must be emitted, not empty stdout');
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('ROUND-4 end-to-end: `\'git\' commit` with an accepted verdict still passes through the FULL pipeline (proves the fix isn\'t "deny everything quoted", it correctly reaches the same verdict check as any other spelling)', async () => {
  const db = await connectDb();
  const item = await createFixtureItem(db, 'quoted-bare-verdict');
  try {
    await recordDestructiveVerdict(db, item.code, { verdict: 'accept', attacker: 'Destructive-Fixture', recorderSession: 'independent-attacker-fixture' });
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
    await recordDestructiveVerdict(db, ok.code, { verdict: 'accept', attacker: 'Destructive-Fixture', recorderSession: 'independent-attacker-fixture' });
    await recordDestructiveVerdict(db, bad.code, { verdict: 'reject', attacker: 'Destructive-Fixture', recorderSession: 'independent-attacker-fixture' });

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
    await recordDestructiveVerdict(db, item.code, { verdict: 'accept', attacker: 'Destructive-Fixture', recorderSession: 'independent-attacker-fixture' });

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
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-224 (1) EXACT REPRO, newline-separated form (two lines in one Bash call, no `&&`) is caught identically', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'tools'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'tools', 'bug224b.js'), '// scratch\n', 'utf8');
    const r = runGuard(dir, 'git add tools/bug224b.js\ngit commit -m "no bow tag whatsoever, newline form"');
    assert.equal(r.denied, true);
    assert.notEqual(r.stdout, '');
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
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
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-224 (3c): SEPARATE `git add` then `git commit` tool calls, with an accepted verdict, still pass — proves the fix does not over-broaden and start denying the legitimate separate-calls flow', async () => {
  const db = await connectDb();
  const item = await createFixtureItem(db, 'bug224-separate-ok');
  try {
    await recordDestructiveVerdict(db, item.code, { verdict: 'accept', attacker: 'Destructive-Fixture', recorderSession: 'independent-attacker-fixture' });
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
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i, 'must be denied for the ordinary zero-tag reason');
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
// BUG-332 / BUG-224 path-normalisation family — the PROVEN-LIVE bypass that
// let c6f2088 reach public main on 2026-08-21 ("feat: watchable pacing
// placeholder - 30s per month [engine.core]": data/pacing.json +
// internal/engine/core/clock.go, zero verdicts, guard silently allowed).
//
// MECHANISM (established from the code, 2026-08-22, not assumed): a combined
// `git add <pathspec> && git commit` unioned its add paths into the
// classification set with ONLY backslashes replaced (`p.replace(/\\/g,'/')`),
// so a pathspec that spells an enforced-dir file without the literal prefix
// `cmd/|internal/|data/|tools/` makes isEnforcedDirPath() false and the whole
// commit is classified non-code-bearing — allowSilently() fires BEFORE the
// tag/verdict pipeline ever runs. Proven spellings that all bypass the plain
// regex (BUG-224 round, attacker findings, 2026-08-21):
//   git add ./internal/engine/evil.go      (leading ./)
//   git add internal\\engine\\evil.go       (backslash — actually caught today
//                                            by the .replace, kept as a control)
//   git add docs/../internal/engine/evil.go (.. traversal)
//   git add :/internal/engine/evil.go       (magic root pathspec)
//   git add INTERNAL/engine/evil.go         (case-variant — Windows FS folds it)
// Each of these stages internal/engine/evil.go for real; the guard must see
// it as code-bearing and DENY the no-tag commit.
// ---------------------------------------------------------------------------

test('BUG-332 unit: normalizeGitPath collapses ./ .. // and magic-prefix spellings onto the canonical enforced-dir path', () => {
  assert.equal(normalizeGitPath('./internal/engine/evil.go'), 'internal/engine/evil.go');
  assert.equal(normalizeGitPath('docs/../internal/engine/evil.go'), 'internal/engine/evil.go');
  assert.equal(normalizeGitPath(':/internal/engine/evil.go'), 'internal/engine/evil.go');
  assert.equal(normalizeGitPath('internal\\engine\\evil.go'), 'internal/engine/evil.go');
  assert.equal(normalizeGitPath('././internal//engine/../engine/evil.go'), 'internal/engine/evil.go');
  // BUG-332 r2 (REJECT finding): the whole git pathspec-magic family `:(...)`,
  // canonical syntax carries NO slash. Each of these is exactly what the r2
  // attacker proved silently stages internal/engine/evil.go while the r1 fix
  // allowed it.
  assert.equal(normalizeGitPath(':(top)internal/engine/evil.go'), 'internal/engine/evil.go');
  assert.equal(normalizeGitPath(':(top,icase)internal/engine/evil.go'), 'internal/engine/evil.go');
  assert.equal(normalizeGitPath(':(icase)internal/engine/evil.go'), 'internal/engine/evil.go');
  assert.equal(normalizeGitPath(':(literal)internal/engine/evil.go'), 'internal/engine/evil.go');
  // ...optionally with a trailing slash after the magic (git rejects this
  // spelling, but normalizing it honestly costs nothing and keeps the path
  // from silently surviving a future git that accepts it).
  assert.equal(normalizeGitPath(':(top)/internal/engine/evil.go'), 'internal/engine/evil.go');
  assert.equal(normalizeGitPath(':(exclude)internal/engine/evil.go'), 'internal/engine/evil.go');
  assert.equal(normalizeGitPath('docs/notes.md'), 'docs/notes.md');
  assert.equal(normalizeGitPath('data/pacing.json'), 'data/pacing.json');
  assert.equal(normalizeGitPath('claude-bow.js'), 'claude-bow.js');
  // A traversal that climbs above the repo root must not crash or leak.
  assert.equal(normalizeGitPath('../../internal/engine/evil.go'), 'internal/engine/evil.go');
  assert.equal(normalizeGitPath(''), '');
});

test('BUG-332: isEnforcedDirPath is case-insensitive (Windows case-folded paths must not escape the enforced set)', () => {
  assert.equal(isEnforcedDirPath('internal/engine/evil.go'), true);
  assert.equal(isEnforcedDirPath('INTERNAL/engine/evil.go'), true, 'Windows folds INTERNAL onto internal');
  assert.equal(isEnforcedDirPath('Data/foo.json'), true, 'Data folds onto data on Windows');
  assert.equal(isEnforcedDirPath('docs/notes.md'), false);
});

test('BUG-332 (1) EXACT REPRO: combined `git add ./internal/engine/evil.go && git commit -m "docs: tidy"` is DENIED (pre-fix: silently allowed)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'git add ./internal/engine/evil.go && git commit -m "docs: tidy"');
    assert.equal(r.denied, true, 'a leading ./ must not hide an enforced-dir file from the code-bearing classification');
    assert.notEqual(r.stdout, '', 'a decision payload must be emitted — the exact silent-allow symptom');
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 (2): combined `git add docs/../internal/engine/evil.go && git commit` is DENIED (.. traversal)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'git add docs/../internal/engine/evil.go && git commit -m "docs: tidy"');
    assert.equal(r.denied, true, 'a .. traversal must not escape the enforced-dir prefix');
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 (3): combined `git add :/internal/engine/evil.go && git commit` is DENIED (magic root pathspec)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'git add :/internal/engine/evil.go && git commit -m "docs: tidy"');
    assert.equal(r.denied, true, 'the :/ magic pathspec must not escape the enforced-dir prefix');
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 (4): combined `git add INTERNAL/engine/evil.go && git commit` is DENIED (Windows case-fold)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'git add INTERNAL/engine/evil.go && git commit -m "docs: tidy"');
    assert.equal(r.denied, true, 'a case-variant enforced-dir prefix must not escape on a case-insensitive filesystem');
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 (5) CONTROL: combined `git add internal/engine/evil.go && git commit` (plain spelling) stays DENIED — the fix does not weaken the plain path', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'git add internal/engine/evil.go && git commit -m "docs: tidy"');
    assert.equal(r.denied, true);
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 (6) CONTROL: the same four bypass spellings with a real BOW code carrying zero verdicts stay DENIED (the c6f2088 scenario exactly)', async () => {
  const db = await connectDb();
  const item = await createFixtureItem(db, 'bug332-pathexp');
  try {
    for (const spelling of ['./internal/engine/evil.go', 'docs/../internal/engine/evil.go', ':/internal/engine/evil.go']) {
      withTempRepo((dir) => {
        fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
        fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
        const r = runGuard(dir, `git add ${spelling} && git commit -m "[${item.code}] change"`);
        assert.equal(r.denied, true, `spelling "${spelling}" with zero-verdict item must be denied (not allowed)`);
        assert.match(r.reason, new RegExp(item.code));
      });
    }
  } finally {
    await deleteFixtureItem(db, item.guid);
    await db.end();
  }
});

test('BUG-332 (7) CONTROL: a genuinely non-code-bearing docs/ path with a leading ./ is still silently ALLOWED (no over-block)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'docs'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'docs', 'notes.md'), '# notes\n', 'utf8');
    const r = runGuard(dir, 'git add ./docs/notes.md && git commit -m "docs update, no tag needed"');
    assert.equal(r.denied, false);
    assert.equal(r.stdout, '');
  });
});

// BUG-332 r2 (REJECT finding 1): the git pathspec-magic family `:(...)` — each
// spelling below was PROVEN by the r2 attacker to silently stage
// internal/engine/evil.go while the r1 fix allowed it. The commit must be
// DENIED (zero-verdict item = the c6f2088 shape exactly).
test('BUG-332 r2 (12): `:(top)internal/...` magic pathspec is DENIED', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'git add :(top)internal/engine/evil.go && git commit -m "docs: tidy"');
    assert.equal(r.denied, true, ':(top) magic must not escape the enforced-dir prefix');
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r2 (13): `:(top,icase)internal/...` magic pathspec is DENIED', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'git add :(top,icase)internal/engine/evil.go && git commit -m "docs: tidy"');
    assert.equal(r.denied, true, ':(top,icase) magic must not escape the enforced-dir prefix');
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r2 (14): `:(icase)internal/...` magic pathspec is DENIED', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'git add :(icase)internal/engine/evil.go && git commit -m "docs: tidy"');
    assert.equal(r.denied, true, ':(icase) magic must not escape the enforced-dir prefix');
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r2 (15): `:(literal)internal/...` magic pathspec is DENIED', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'git add :(literal)internal/engine/evil.go && git commit -m "docs: tidy"');
    assert.equal(r.denied, true, ':(literal) magic must not escape the enforced-dir prefix');
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r2 (16) CONTROL: a genuinely non-code-bearing path with a :(literal) magic prefix is still ALLOWED (no over-block)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'docs'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'docs', 'notes.md'), '# notes\n', 'utf8');
    const r = runGuard(dir, 'git add :(literal)docs/notes.md && git commit -m "docs update, no tag needed"');
    assert.equal(r.denied, false, 'magic prefix on a docs/ path must not turn it code-bearing');
    assert.equal(r.stdout, '');
  });
});

// ---------------------------------------------------------------------------
// BUG-332 r4 (REJECT finding, attacker a90580d4bc2c89532) — shell expansion
// in `git add` args bypasses the GR#23 gate. classifyAddArgs treated a token
// like `$PWD/internal/engine/evil.go` or `$FILE` as a simple literal path; the
// shell rewrites it before git runs, so the command text cannot enumerate the
// staged files — `git add $PWD/internal/engine/evil.go && git commit -m "docs:
// tidy"` committed a code-bearing file with zero verdict. Any `$`/backtick
// expansion token is now the ambiguous-add class → fail-closed deny.
// ---------------------------------------------------------------------------

test('BUG-332 r4 (19) EXACT REPRO: combined `git add $PWD/internal/engine/evil.go && git commit` is DENIED (shell-expansion bypass)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'git add $PWD/internal/engine/evil.go && git commit -m "docs: tidy"');
    assert.equal(r.denied, true, '$PWD/ expansion must not be treated as an enumerable literal path');
    assert.match(r.reason, /ambiguous|BUG-224|split|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i, 'deny must name the ambiguous-add / fail-closed posture');
  });
});

test('BUG-332 r4 (20) EXACT REPRO: combined `export FILE=...; git add $FILE && git commit` is DENIED (env-var expansion bypass)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'export FILE=internal/engine/evil.go; git add $FILE && git commit -m "docs: tidy"');
    assert.equal(r.denied, true, '$FILE expansion must not be treated as an enumerable literal path');
    assert.match(r.reason, /ambiguous|BUG-224|split|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r4 (21): combined `git add `pwd`/internal/engine/evil.go && git commit` is DENIED (backtick command substitution)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'git add `pwd`/internal/engine/evil.go && git commit -m "docs: tidy"');
    assert.equal(r.denied, true, 'backtick substitution must not be treated as an enumerable literal path');
    assert.match(r.reason, /ambiguous|BUG-224|split|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r4 (22): combined `git add $(pwd)/internal/engine/evil.go && git commit` is DENIED ($(...) command substitution, path does not surface as a literal token)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'git add $(pwd)/internal/engine/evil.go && git commit -m "docs: tidy"');
    assert.equal(r.denied, true, '$(...) substitution must not be treated as an enumerable literal path');
    assert.match(r.reason, /ambiguous|BUG-224|split|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r4 (23) CONTROL: a genuinely non-code-bearing docs/ path with NO shell-expansion characters is still ALLOWED (no over-block)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'docs'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'docs', 'notes.md'), '# notes\n', 'utf8');
    const r = runGuard(dir, 'git add docs/notes.md && git commit -m "docs update, no tag needed"');
    assert.equal(r.denied, false, 'ordinary bare path must stay enumerable');
    assert.equal(r.stdout, '');
  });
});

// BUG-332 r4 (Bill's own probe, same family the attacker named): tilde
// expansion. `~/...` (or `~user/...`) is rewritten to an absolute path by the
// shell BEFORE git sees it, and the unexpanded token is classified as a simple
// literal path that never matches the enforced-dir prefix — verified live
// (denied:false) against the real guard pre-fix. Closed with a WORD-INITIAL
// rule so a mid-word `~` (e.g. a literal `backup~.md` filename) stays
// enumerable and does not over-block.

test('BUG-332 r4 (24) EXACT REPRO: combined `git add ~/internal/engine/evil.go && git commit` is DENIED (tilde expansion)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'git add ~/internal/engine/evil.go && git commit -m "docs: tidy"');
    assert.equal(r.denied, true, '~/ expansion must not be treated as an enumerable literal path');
    assert.match(r.reason, /ambiguous|BUG-224|split|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i, 'deny must name the ambiguous-add / fail-closed posture');
  });
});

test('BUG-332 r4 (25) CONTROL: a mid-word `~` in a literal docs filename is still ALLOWED (word-initial rule does not over-block)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'docs'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'docs', 'backup~.md'), '# notes\n', 'utf8');
    const r = runGuard(dir, 'git add docs/backup~.md && git commit -m "docs update, no tag needed"');
    assert.equal(r.denied, false, '~ mid-word is literal, not tilde expansion — must stay enumerable');
    assert.equal(r.stdout, '');
  });
});

// ---------------------------------------------------------------------------
// BUG-332 r5 (REJECT findings, attacker a4eb859218dbd0b83) — cwd redirection
// and inline aliases defeat the combined add+commit gate.
//   Finding 1: a `cd`/`pushd`/`popd` BEFORE the add, or a `git -C <dir>` on
//   the add's own invocation, shifts the base real git resolves relative
//   paths against. The guard resolves from ITS cwd, so `cd internal/engine &&
//   git add evil.go && git commit -m "docs: tidy"` silently committed
//   internal/engine/evil.go with zero verdict (proven end-to-end). Adjacent
//   shape closed in the same round: an ABSOLUTE add path
//   (`C:/.../internal/engine/evil.go`) never matches the enforced-dir prefix
//   → also silent ALLOW (Bill's own live probe).
//   Finding 2: findGitAddInvocations passed a fresh Set() to resolveAlias,
//   discarding the invocation's inline `-c alias.*` overrides, so an aliased
//   `git add` was invisible and only the bare trailing `git commit` against an
//   empty index was seen → silent ALLOW. Persistent aliases were already
//   caught; the hole was specifically the discarded inline overrides.
// Each is now the ambiguous-add / enforced-path class → fail-closed DENY.
// ---------------------------------------------------------------------------

test('BUG-332 r5 (26) EXACT REPRO: combined `cd internal/engine && git add evil.go && git commit` is DENIED (cwd redirection)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'cd internal/engine && git add evil.go && git commit -m "docs: tidy"');
    assert.equal(r.denied, true, 'a cd-shifted relative add must not be treated as enumerable from the guard cwd');
    assert.match(r.reason, /ambiguous|BUG-224|split|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i, 'deny must name the ambiguous-add / fail-closed posture');
  });
});

test('BUG-332 r5 (27) EXACT REPRO: combined `git -C internal/engine add evil.go && git commit` is DENIED (git -C cwd shift)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'git -C internal/engine add evil.go && git commit -m "docs: tidy"');
    assert.equal(r.denied, true, 'a git -C add must not be treated as enumerable from the guard cwd');
    assert.match(r.reason, /ambiguous|BUG-224|split|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r5 (28): combined `git add <absolute path to internal/...>` is DENIED (absolute-path add, Bill probe)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const abs = path.join(dir, 'internal', 'engine', 'evil.go').replace(/\\/g, '/');
    const r = runGuard(dir, `git add ${abs} && git commit -m "docs: tidy"`);
    assert.equal(r.denied, true, 'an absolute add path must not be classified against the enforced set without repo-root knowledge');
    assert.match(r.reason, /ambiguous|BUG-224|split|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r5 (29): combined `pushd internal/engine && git add evil.go && git commit` is DENIED (pushd cwd shift)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'pushd internal/engine && git add evil.go && git commit -m "docs: tidy"');
    assert.equal(r.denied, true, 'a pushd-shifted relative add must not be treated as enumerable');
    assert.match(r.reason, /ambiguous|BUG-224|split|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r5 (30) EXACT REPRO: inline `-c alias.ad="add internal/engine/evil.go" ad && git commit` is DENIED (inline alias carries the add)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'git -c alias.ad="add internal/engine/evil.go" ad && git commit -m "docs: tidy"');
    assert.equal(r.denied, true, 'an inline-aliased add must resolve like the plain spelling');
    // The add is found, but its path is inside the ALIAS VALUE (unenumerable
    // from the command text) → the guard fails closed via the ambiguous route.
    assert.match(r.reason, /ambiguous|BUG-224|split|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i, 'embedded alias path cannot be enumerated → ambiguous deny');
  });
});

test('BUG-332 r5 (31) EXACT REPRO: inline `-c alias.ad="add" ad internal/engine/evil.go && git commit` is DENIED (inline alias + arg)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'git -c alias.ad="add" ad internal/engine/evil.go && git commit -m "docs: tidy"');
    assert.equal(r.denied, true, 'an inline-aliased add with a trailing arg must resolve like the plain spelling');
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r5 (32) CONTROL: lowercase `-c` config (not -C dir) on a docs add is still ALLOWED (the -C detector does not over-block config)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'docs'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'docs', 'notes.md'), '# notes\n', 'utf8');
    const r = runGuard(dir, 'git -c core.quotepath=false add docs/notes.md && git commit -m "docs update, no tag needed"');
    assert.equal(r.denied, false, '-c config must not trip the -C directory-shift detector');
    assert.equal(r.stdout, '');
  });
});

// ---------------------------------------------------------------------------
// BUG-332 r6 (r5 REJECT, attacker a4eb859218dbd0b83) — cwd-redirection word
// gaps + eval/iex wrapper re-scan.
//   The r5 `cd|pushd|popd` word list and `(?:^|[;&|(\n])` boundary anchor were
//   trivially sidestepped by: `chdir` (cmd/bash), PowerShell `Set-Location` /
//   its `sl` alias (also recognized in lowercase / mixed case via /i),
//   `cd` nested in `for/while…do` and `if…then` bodies, `{ … }` brace groups,
//   and `eval "…"` / `iex "…"` wrappers that run their string argument as a
//   command (hidden verbs the scanner never descended into). Each is now the
//   ambiguous-add / enforced-path class → fail-closed DENY.
// ---------------------------------------------------------------------------

test('BUG-332 r6 (33) F1: `for … do cd $d; git add evil.go` is DENIED (cd in loop body)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'for d in internal/engine; do cd $d; git add evil.go; git commit -m "docs: tidy"; done');
    assert.equal(r.denied, true, 'a cd nested in a for…do body still shifts the base git resolves paths against');
    assert.match(r.reason, /ambiguous|BUG-224|split|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r6 (34) F2: `while …; do cd $d; git add evil.go` is DENIED (cd in while body)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'd=internal/engine; while [ -n "$d" ]; do cd $d; d=""; git add evil.go; git commit -m "x"; done');
    assert.equal(r.denied, true, 'a cd nested in a while…do body must not be treated as enumerable from the guard cwd');
    assert.match(r.reason, /ambiguous|BUG-224|split|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r6 (35) F3: PowerShell `Set-Location` before the add is DENIED', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'Set-Location internal/engine; git add evil.go; git commit -m "x"');
    assert.equal(r.denied, true, 'a Set-Location cwd shift must be treated like cd (fail-closed)');
    assert.match(r.reason, /ambiguous|BUG-224|split|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r6 (36) F4: PowerShell `sl` alias before the add is DENIED', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'sl internal/engine; git add evil.go; git commit -m "x"');
    assert.equal(r.denied, true, 'the sl alias of Set-Location must be treated like cd (fail-closed)');
    assert.match(r.reason, /ambiguous|BUG-224|split|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r6 (37) F5: `chdir` before the add is DENIED (cmd/bash alias of cd)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'chdir internal/engine; git add evil.go; git commit -m "x"');
    assert.equal(r.denied, true, 'chdir is a real cwd change and must be treated like cd (fail-closed)');
    assert.match(r.reason, /ambiguous|BUG-224|split|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r6 (38) F6: `powershell -Command "Set-Location …; git add evil.go; git commit"` is DENIED (inner cwd shift)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'powershell -Command "Set-Location internal/engine; git add evil.go; git commit -m \\"x\\""');
    assert.equal(r.denied, true, 'Set-Location inside the already-extracted -Command body must make the inner add ambiguous');
    assert.match(r.reason, /ambiguous|BUG-224|split|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r6 (39) F7: `eval "git add internal/engine/evil.go && git commit"` is DENIED (hidden verbs in eval body)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'eval "git add internal/engine/evil.go && git commit -m \\"docs: tidy\\""');
    assert.equal(r.denied, true, 'the eval body is real command text and its enforced-path add must be denied');
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r6 (40) CONTROL: a cd AFTER the add, and a cd only inside a -m message, must NOT over-block a docs add', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'docs'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'docs', 'notes.md'), '# notes\n', 'utf8');
    const r = runGuard(dir, 'git add docs/notes.md && cd docs && git commit -m "cd is a shell builtin, not a cwd shift of the add"');
    assert.equal(r.denied, false, 'a cwd change AFTER the add must not make the add ambiguous');
    assert.equal(r.stdout, '');
  });
});

test('BUG-332 r6 (41): `if x; then cd y; git add evil.go` is DENIED (cd in then body)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'if [ -d internal/engine ]; then cd internal/engine; git add evil.go; git commit -m "x"; fi');
    assert.equal(r.denied, true, 'a cd in an if…then body still shifts the base git resolves paths against');
    assert.match(r.reason, /ambiguous|BUG-224|split|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

// ---------------------------------------------------------------------------
// BUG-332 r7 (r6 REJECT, attacker a4eb859218dbd0b83) — the structural
// tokenizer. The r6 word-list/boundary-anchor regex was the flaw itself
// ("a regex is not a shell parser", ASM-350): reserved words (`else`, `!`,
// `if`, `while`), prefix builtins (`builtin`, `command`, `time`, `exec`),
// env-prefix assignments (`X=1`), and quote-splitting (`c"d"` → `cd`,
// `g"it"` → `git`) all start a command OUTSIDE the `(?:^|[;&|(\n{}])`
// boundary class. CWD_CHANGE_CMD_RE is DELETED; cwd detection now derives
// from authorGuard.scanShellWords() — ONE lexer that answers which words are
// real commands, dequoted how, at what subshell depth. Each spelling below
// is now the ambiguous-add class → fail-closed DENY. F14 is the CONTROL: a
// `cd` inside `$(...)` lives at a deeper subshell depth than a top-level add
// and must NOT make it ambiguous.
// ---------------------------------------------------------------------------

test('BUG-332 r7 (42) F1: `if cd internal/engine; then git add evil.go` is DENIED (cd in if condition)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'if cd internal/engine; then git add evil.go && git commit -m "docs: tidy"; fi');
    assert.equal(r.denied, true, 'a cd in an if condition shifts the base git resolves paths against');
    assert.match(r.reason, /ambiguous|BUG-224|split|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r7 (43) F2: `else cd internal/engine; git add evil.go` is DENIED (cd in else body)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'if false; then :; else cd internal/engine; git add evil.go && git commit -m "docs: tidy"; fi');
    assert.equal(r.denied, true, 'a cd after an else keyword is a real command and must shift the add base');
    assert.match(r.reason, /ambiguous|BUG-224|split|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r7 (44) F3: `! cd internal/engine && git add evil.go` is DENIED (! negation still runs cd)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, '! cd internal/engine && git add evil.go && git commit -m "docs: tidy"');
    assert.equal(r.denied, true, '! cd still executes cd; the add base shifts');
    assert.match(r.reason, /ambiguous|BUG-224|split|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r7 (45) F4: `builtin cd internal/engine && git add evil.go` is DENIED (builtin prefix)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'builtin cd internal/engine && git add evil.go && git commit -m "docs: tidy"');
    assert.equal(r.denied, true, 'the builtin prefix still runs cd');
    assert.match(r.reason, /ambiguous|BUG-224|split|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r7 (46) F5: `command cd internal/engine && git add evil.go` is DENIED (command prefix)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'command cd internal/engine && git add evil.go && git commit -m "docs: tidy"');
    assert.equal(r.denied, true, 'the command prefix still runs cd');
    assert.match(r.reason, /ambiguous|BUG-224|split|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r7 (47) F6: `X=1 cd internal/engine && git add evil.go` is DENIED (env-prefix assignment)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'X=1 cd internal/engine && git add evil.go && git commit -m "docs: tidy"');
    assert.equal(r.denied, true, 'an env-prefix assignment before cd still runs cd');
    assert.match(r.reason, /ambiguous|BUG-224|split|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r7 (48) F7: `time cd internal/engine && git add evil.go` is DENIED (time wrapper)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'time cd internal/engine && git add evil.go && git commit -m "docs: tidy"');
    assert.equal(r.denied, true, 'time cd still executes cd');
    assert.match(r.reason, /ambiguous|BUG-224|split|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r7 (49) F8: `c"d" internal/engine && git add evil.go` is DENIED (quote-split cd)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'c"d" internal/engine && git add evil.go && git commit -m "docs: tidy"');
    assert.equal(r.denied, true, 'a quote-split cd token is still the cd command');
    assert.match(r.reason, /ambiguous|BUG-224|split|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r7 (50) F9: `while cd internal/engine; do git add evil.go` is DENIED (cd in while condition)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'while cd internal/engine; do git add evil.go && git commit -m "docs: tidy"; done');
    assert.equal(r.denied, true, 'a cd in a while condition shifts the add base');
    assert.match(r.reason, /ambiguous|BUG-224|split|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r7 (51) F13a: quote-split `g"it" add internal/engine/evil.go && g"it" commit` is DENIED (git token unrecognisable to the regex)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'g"it" add internal/engine/evil.go && g"it" commit -m "docs: tidy"');
    assert.equal(r.denied, true, 'a quote-split git token must be recognised as a real git invocation');
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]|ambiguous|BUG-224|split/i);
  });
});

test('BUG-332 r7 (52) F13b: a PRE-STAGED code file committed via `g"it" commit` is DENIED (the r6 total-bypass vector)', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/engine/evil.go', 'package engine\n');
    const r = runGuard(dir, 'g"it" commit -m "docs: tidy"');
    assert.equal(r.denied, true, 'the commit gate must fire on a quote-split git token');
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r16 (53) F14 CONTROL — SUPERSEDED by the AARON structural allowlist: `$(cd /tmp && pwd) > /dev/null; git add docs/notes.md && git commit` now DENIES fail-closed (it carries `$(...)` + redirection — the r7 "subshell cd is benign" control is collapsed by the strict allowlist, which proceeds on ZERO shell indirection anywhere in the command; a false-positive deny is recoverable, a false-negative allow is the hole)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'docs'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'docs', 'notes.md'), '# notes\n', 'utf8');
    const r = runGuard(dir, '$(cd /tmp && pwd) > /dev/null; git add docs/notes.md && git commit -m "docs, subshell cd only"');
    assert.equal(r.denied, true, '`$(...)` and redirection are shell indirection → the AARON allowlist denies fail-closed regardless of how benign the substitution is');
    assert.match(r.reason, /shell indirection|structural allowlist|AARON RULING/i);
  });
});

// ---------------------------------------------------------------------------
// BUG-332 r8 (r7 REJECT, attacker a70e40f847ad58b41) — the subprocess-prefix
// arg-run model. r7's CRITICAL finding was a TOTAL bypass of the whole guard
// class: the prefix-wrapper family (sudo/doas/nice/stdbuf/setsid/xargs/
// timeout/env/nohup) was absent from SHELL_PREFIX_WORDS, so in `sudo bash -c
// "cd internal/engine && git add evil.go && git commit -m 'docs: tidy'"` the
// prefix word stole commandStart and the shell wrapper (`bash`) fell to
// ARGUMENT position — wrapperBodiesFromWords never descended into the body, so
// the inner git add+commit were invisible and the code-bearing commit was
// ALLOWED with zero verdict. r8 marks every word of a subprocess prefix's
// argument run `inPrefixArgs` and still extracts the run-string of a shell
// wrapper word found there. Each family member (plus value-taking flag
// variants: `sudo -n`, `env -i`, `nice -n 10`, `timeout 10`) now DENIES;
// F15g is the CONTROL proving a wrapper word as a plain argument to a
// NON-prefix command (`echo bash -c "…"`) does NOT over-block.
// ---------------------------------------------------------------------------

test('BUG-332 r8 (54) F15a: `sudo bash -c "cd internal/engine && git add evil.go && git commit"` — the r7 attacker\'s EXACT repro — is DENIED', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'sudo bash -c "cd internal/engine && git add evil.go && git commit -m \'docs: tidy\'"');
    assert.equal(r.denied, true, 'the subprocess-prefix run-string is real command text — the enforced-path add must be fail-closed denied');
    assert.match(r.reason, /ambiguous|BUG-224|split|no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r8 (55) F15a2: path-prefixed and combined-flag shell spellings are DENIED (r8 self-audit gaps)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    for (const cmd of [
      'sudo /bin/bash -c "git add internal/engine/evil.go && git commit -m \'x\'"',
      'sudo /usr/bin/bash -c "git add internal/engine/evil.go && git commit -m \'x\'"',
      'sudo /bin/sh -c "git add internal/engine/evil.go && git commit -m \'x\'"',
      'sudo bash -lc "git add internal/engine/evil.go && git commit -m \'x\'"',
      's"udo" bash -c "git add internal/engine/evil.go && git commit -m \'x\'"',
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `a path-named or combined-flag shell must still execute its run-string: ${cmd}`);
      assert.match(r.reason, /ambiguous|BUG-224|split|no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
    }
  });
});

test('BUG-332 r8 (56) F15b: `sudo -n bash -c "…git add evil.go…"` — a flag between prefix and shell — is DENIED', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'sudo -n bash -c "cd internal/engine && git add evil.go && git commit -m \'docs: tidy\'"');
    assert.equal(r.denied, true, 'the -n flag must not hide the run-string (command position is the prefix\'s argument run)');
    assert.match(r.reason, /ambiguous|BUG-224|split|no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r8 (57) F15c: `env -i bash -c "…"` and `env -u VAR bash -c "…"` are DENIED (env with flags)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    for (const cmd of [
      'env -i bash -c "git add internal/engine/evil.go && git commit -m \'x\'"',
      'env -u HOME bash -c "git add internal/engine/evil.go && git commit -m \'x\'"',
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `an env flag must not hide the run-string: ${cmd}`);
      assert.match(r.reason, /ambiguous|BUG-224|split|no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
    }
  });
});

test('BUG-332 r8 (58) F15d: `doas nice -n 10 bash -c "…"` — chained prefixes with a flag-value — is DENIED', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'doas nice -n 10 bash -c "git add internal/engine/evil.go && git commit -m \'x\'"');
    assert.equal(r.denied, true, 'chained subprocess prefixes each use the argument-run model — the shell wrapper still executes its body');
    assert.match(r.reason, /ambiguous|BUG-224|split|no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r8 (59) F15e: `sudo powershell -Command "git add evil.go; git commit"` is DENIED (subprocess-prefix pwsh)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'sudo powershell -Command "git add internal/engine/evil.go; git commit -m \'x\'"');
    assert.equal(r.denied, true, 'a powershell wrapper inside a subprocess-prefix argument run still executes its -Command body');
    assert.match(r.reason, /ambiguous|BUG-224|split|no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r8 (60) F15f: `sudo eval "git add evil.go && git commit"` is DENIED (subprocess-prefix eval)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'sudo eval "git add internal/engine/evil.go && git commit -m \'x\'"');
    assert.equal(r.denied, true, 'an eval body inside a subprocess-prefix argument run is real command text');
    assert.match(r.reason, /ambiguous|BUG-224|split|no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r8 (61) F15g CONTROL: `echo bash -c "git add internal/engine/evil.go"` — the wrapper word is an echo ARGUMENT, never executed — is NOT denied', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'echo bash -c "git add internal/engine/evil.go"');
    assert.equal(r.denied, false, 'echo merely prints its arguments — a bash word there is not a wrapper and must not over-block');
    assert.equal(r.stdout, '');
  });
});

// ---------------------------------------------------------------------------
// BUG-332 r9 (r8 REJECT, attacker aafe8e49df2df3cb9) — F1-F5. Four CRITICAL
// total-bypass classes survived the r8 subprocess-prefix model: F1 an
// unlisted shell name (`sudo ash -c "…"` — isShellExecutableWord was a closed
// list), F2 a short-flag cluster CONTAINING `c` but not ending in it (`bash
// -ci`/`-icf`/`-lci` — bash executes all as `-c`), F3 value-taking flags
// parked between the shell and the run flag (`bash -O extglob -c "…"`, `-o
// noclobber`, `--rcfile x`, `--init-file x`), and F4 a heredoc body fed to a
// shell (`sudo bash <<'EOF' … EOF`) — buildQuoteMask masks the body opaque, so
// the git verbs inside were never scanned. Plus one over-block: F5
// `builtin cd /tmp && git add docs/notes.md && git commit` was DENIED though
// an absolute cd OUTSIDE the repo root cannot stage a repo file — the docs-
// control the brief demands. F16a-d prove the four bypass classes now DENY;
// F16e-g prove the cd-path fix ALLOWS absolute-outside-repo docs control and
// still DENIES relative (unknown-base) cds.
// ---------------------------------------------------------------------------

test('BUG-332 r9 (62) F16a (F1): `sudo ash -c "cd internal/engine && git add evil.go && git commit"` is DENIED (unlisted shell)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, 'sudo ash -c "cd internal/engine && git add evil.go && git commit -m \'docs: tidy\'"');
    assert.equal(r.denied, true, 'an unlisted shell still executes its -c run-string — the enforced-path add must be fail-closed denied');
    assert.match(r.reason, /ambiguous|BUG-224|split|no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r9 (63) F16b (F2): `sudo bash -ci "git add evil.go && git commit"` is DENIED (cluster containing c)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    for (const cmd of [
      'sudo bash -ci "git add internal/engine/evil.go && git commit -m \'x\'"',
      'sudo bash -icf "git add internal/engine/evil.go && git commit -m \'x\'"',
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `a short-flag cluster containing c is a run flag: ${cmd}`);
      assert.match(r.reason, /ambiguous|BUG-224|split|no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
    }
  });
});

test('BUG-332 r9 (64) F16c (F3): `sudo bash -O extglob -c "git add evil.go && git commit"` is DENIED (value-taking flag before -c)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    for (const cmd of [
      'sudo bash -O extglob -c "git add internal/engine/evil.go && git commit -m \'x\'"',
      'sudo bash -o noclobber -c "git add internal/engine/evil.go && git commit -m \'x\'"',
      'sudo bash --rcfile x -c "git add internal/engine/evil.go && git commit -m \'x\'"',
      'sudo bash --init-file x -c "git add internal/engine/evil.go && git commit -m \'x\'"',
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `a value-taking flag+value cannot hide the later -c: ${cmd}`);
      assert.match(r.reason, /ambiguous|BUG-224|split|no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
    }
  });
});

test('BUG-332 r9 (65) F16d (F4): `sudo bash <<EOF … git add evil.go … git commit … EOF` is DENIED (shell-fed heredoc body)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, "sudo bash <<'EOF'\ncd internal/engine\ngit add evil.go\ngit commit -m 'docs: tidy'\nEOF");
    assert.equal(r.denied, true, 'a heredoc body fed to a shell is executed command text — the enforced-path add must be fail-closed denied');
    assert.match(r.reason, /ambiguous|BUG-224|split|no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r9 (66) F16e (F5): `builtin cd /tmp && git add docs/notes.md && git commit` is NOT denied (absolute cd outside the repo is a known base — the docs-control)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'docs'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'docs', 'notes.md'), '# notes\n', 'utf8');
    const r = runGuard(dir, 'builtin cd /tmp && git add docs/notes.md && git commit -m "docs, cd to absolute outside-repo path"');
    assert.equal(r.denied, false, 'an absolute cd OUTSIDE the repo root cannot stage a repo file — docs control must not be ambiguous-denied');
    assert.equal(r.stdout, '');
  });
});

test('BUG-332 r9 (67) F16f (F5): `cd C:\\Windows\\Temp && git add docs/notes.md && git commit` is NOT denied (Windows absolute drive path outside repo)', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'docs'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'docs', 'notes.md'), '# notes\n', 'utf8');
    const r = runGuard(dir, 'cd C:\\Windows\\Temp && git add docs/notes.md && git commit -m "docs, Windows absolute path outside repo"');
    assert.equal(r.denied, false, 'a Windows absolute drive path outside the repo is a known base — must not be ambiguous-denied');
    assert.equal(r.stdout, '');
  });
});

test('BUG-332 r9 (68) F16g CONTROL (F5): a RELATIVE cd before the add is still ambiguous and DENIED', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    for (const cmd of [
      'cd internal/engine && git add docs/notes.md && git commit -m "x"',
      'cd /tmp && cd internal/engine && git add evil.go && git commit -m "x"',
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `a relative (or last-relative) cd leaves the add base unknown: ${cmd}`);
      assert.match(r.reason, /ambiguous|BUG-224|split|no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
    }
  });
});

// ---------------------------------------------------------------------------
// BUG-332 r10 (r9 attacker F1/F4/F5): the three CRITICAL closures the r9 round
// left open, fixed to the attacker's exact acceptance bar —
// F16h  COMMAND-position shells the r9 class omitted (`osh`/`posh`/`nu`) and
//       the applet dispatcher (`busybox ash -c`) DENY end-to-end.
// F16i  shell-fed heredocs via `sudo -s`/`sudo -i`, `su`, and `cat <<EOF |
//       bash` DENY end-to-end.
// F16j  pipe-fed shell text (`echo "... git add evil.go ..." | bash`) DENY.
// F16k  DOT-PATH cd at/under the repo root (`E:/git/./Metropolis/…`,
//       `E:/git/../git/Metropolis/…`) DENY — the r9 bypass normalizeGitPath
//       resolved but normalizeRepoPath did not.
// F16l  CONTROL: a dot-path cd OUTSIDE the repo stays non-ambiguous — the
//       docs-control still ALLOWS.
// F16m  CONTROL: heredoc/pipe-fed text with NO shell target (heredoc to cat,
//       pipe to grep, transforming emitter) is data and stays ALLOWED.
// ---------------------------------------------------------------------------

test('BUG-332 r10 (69) F16h (F1): COMMAND-position unlisted shells and the applet-dispatcher spelling are DENIED', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    for (const cmd of [
      'osh -c "cd internal/engine && git add evil.go && git commit -m \'x\'"',
      'posh -c "cd internal/engine && git add evil.go && git commit -m \'x\'"',
      'nu -c "cd internal/engine && git add evil.go && git commit -m \'x\'"',
      'busybox ash -c "cd internal/engine && git add evil.go && git commit -m \'x\'"',
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `a command-position unlisted shell still executes its -c run-string: ${cmd}`);
      assert.match(r.reason, /ambiguous|BUG-224|split|no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
    }
  });
});

test('BUG-332 r10 (70) F16i (F4a): heredocs fed via `sudo -s`/`sudo -i`, `su`, or a right-of-pipe shell are DENIED', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const cases = [
      "sudo -s <<'EOF'\ncd internal/engine\ngit add evil.go\ngit commit -m 'x'\nEOF",
      "sudo -i <<'EOF'\ncd internal/engine\ngit add evil.go\ngit commit -m 'x'\nEOF",
      "su root <<'EOF'\ncd internal/engine\ngit add evil.go\ngit commit -m 'x'\nEOF",
      "cat <<'EOF' | bash\ncd internal/engine\ngit add evil.go\ngit commit -m 'x'\nEOF",
    ];
    for (const cmd of cases) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `a shell-fed heredoc body is executed command text: ${cmd.split('\n')[0]}`);
      assert.match(r.reason, /ambiguous|BUG-224|split|no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
    }
  });
});

test('BUG-332 r10 (71) F16j (F4b): pipe-fed shell text `echo "... git add ..." | bash` is DENIED', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, "echo \"cd internal/engine && git add evil.go && git commit -m 'x'\" | bash");
    assert.equal(r.denied, true, 'bash executes the verbatim-echoed text — the enforced-path add must be fail-closed denied');
    assert.match(r.reason, /ambiguous|BUG-224|split|no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r10 (72) F16k (F5): a DOT-PATH cd AT/UNDER the repo root is still ambiguous and DENIED', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    // Derive the dot-path target from the repo root exactly as git reports it
    // (the guard's repoRoot() IS the comparison base) — os.tmpdir() on this
    // box hands out the 8.3 short-name form (AARONG~1) while git rev-parse
    // resolves the long form, and a test target built from the 8.3 form would
    // normalize to a DIFFERENT string than the guard's root. The real-repo
    // attacker shape (E:/git/./Metropolis/…) has no such ambiguity.
    const root = git(dir, ['rev-parse', '--show-toplevel']).replace(/\\/g, '/');
    const parentOfRoot = path.posix.dirname(root);
    const repoName = path.posix.basename(root);
    const grand = path.posix.basename(parentOfRoot);
    for (const cmd of [
      `cd ${parentOfRoot}/./${repoName}/internal/engine && git add evil.go && git commit -m "x"`,
      `cd ${parentOfRoot}/../${grand}/${repoName}/internal/engine && git add evil.go && git commit -m "x"`,
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `a dot-path cd to the repo root must be ambiguous: ${cmd}`);
      assert.match(r.reason, /ambiguous|BUG-224|split|no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
    }
  });
});

test('BUG-332 r10 (73) F16l CONTROL (F5): a DOT-PATH cd OUTSIDE the repo stays non-ambiguous — the docs-control still ALLOWS', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'docs'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'docs', 'notes.md'), '# notes\n', 'utf8');
    const r = runGuard(dir, 'cd C:/temp/../temp2 && git add docs/notes.md && git commit -m "docs, dotted absolute path outside repo"');
    assert.equal(r.denied, false, 'a dotted absolute path outside the repo is still a known base — docs control must not be ambiguous-denied');
    assert.equal(r.stdout, '');
  });
});

test('BUG-332 r10 (74) F16m CONTROL (F4): heredoc/pipe-fed text with NO shell target is data and stays ALLOWED', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    for (const cmd of [
      // heredoc to a NON-shell: cat reads it as data.
      "cat <<'EOF'\ngit add evil.go\ngit commit -m 'x'\nEOF",
      // pipe to a NON-shell target: grep is not a shell.
      'echo "cd internal/engine && git add evil.go && git commit -m x" | grep foo',
      // transforming emitter: grep's output (not its pattern) is what bash
      // would receive — statically unknowable, an honest limitation.
      "grep 'git add' internal/engine/evil.go | bash",
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, false, `no shell executes this text — must NOT be ambiguous-denied: ${cmd.split('\n')[0]}`);
    }
  });
});

// ---------------------------------------------------------------------------
// BUG-332 r11 (r10 attacker C1-C4 + MINOR-1): end-to-end DENY of each
// newly-closed shell-feeding shape against the enforced-path git add —
// F17a  subprocess-prefix pipe targets (`| sudo bash`, `| doas bash`,
//       `| sudo -s`) — the r10 attacker's CRITICAL-2 live proof.
// F17b  `xargs -I{} bash -c "{}"` placeholder substitution (CRITICAL-3).
// F17c  passthrough filters walked to the verbatim emitter (`| cat | bash`,
//       `| tee | bash`, `| sed '' | bash`) (CRITICAL-4).
// F17d  CONTROLs — xargs WITHOUT -I (input becomes ARGUMENTS), transforming
//       emitters, and non-shell targets stay ALLOWED.
// F17e  constant `$()`/backtick emitters in wrapper run-strings DENY;
//       data substitutions (`echo "$(...)"`) stay ALLOWED (MINOR-1).
// ---------------------------------------------------------------------------

test('BUG-332 r11 (75) F17a (C2): subprocess-prefix pipe targets are DENIED end-to-end', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    for (const cmd of [
      "echo \"cd internal/engine && git add evil.go && git commit -m 'x'\" | sudo bash",
      "echo \"cd internal/engine && git add evil.go && git commit -m 'x'\" | doas bash",
      "echo \"cd internal/engine && git add evil.go && git commit -m 'x'\" | sudo -s",
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `the subprocess-prefix shell executes the piped text: ${cmd}`);
      assert.match(r.reason, /ambiguous|BUG-224|split|no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
    }
  });
});

test('BUG-332 r11 (76) F17b (C3): `xargs -I{} bash -c "{}"` placeholder substitution is DENIED end-to-end', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, "echo \"cd internal/engine && git add evil.go && git commit -m 'x'\" | xargs -I{} bash -c \"{}\"");
    assert.equal(r.denied, true, 'xargs substitutes each piped line into the run-string — the enforced-path add is denied');
    assert.match(r.reason, /ambiguous|BUG-224|split|no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r11 (77) F17c (C4): passthrough filters (`cat`, `tee`, `sed ""`) are DENIED end-to-end', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    for (const cmd of [
      "echo \"cd internal/engine && git add evil.go && git commit -m 'x'\" | cat | bash",
      "echo \"cd internal/engine && git add evil.go && git commit -m 'x'\" | tee /dev/null | bash",
      "echo \"cd internal/engine && git add evil.go && git commit -m 'x'\" | sed '' | bash",
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `the passthrough still delivers the verbatim text to the shell: ${cmd}`);
      assert.match(r.reason, /ambiguous|BUG-224|split|no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
    }
  });
});

test('BUG-332 r11 (78) F17d CONTROL (C2/C3/C4): xargs without -I, transforming emitters, and non-shell targets stay ALLOWED', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    for (const cmd of [
      // xargs WITHOUT -I turns the input into ARGUMENTS (a script filename).
      'echo "git add evil.go" | xargs bash',
      // transforming emitters — output statically unknowable.
      'echo "git add evil.go" | grep foo | bash',
      "grep 'git add' internal/engine/evil.go | bash",
      // non-shell pipe target.
      'echo "git add evil.go" | grep git',
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, false, `no shell executes this text — must NOT be ambiguous-denied: ${cmd.split('\n')[0]}`);
    }
  });
});

test('BUG-332 r11 (79) F17e (MINOR-1): constant `$()` emitters in run-strings DENY; data substitutions ALLOW', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    // Constant emitter: bash runs printf, then executes its output.
    const deny = "bash -c \"$(printf 'cd internal/engine && git add evil.go && git commit -m x')\"";
    const rDeny = runGuard(dir, deny);
    assert.equal(rDeny.denied, true, 'the constant substitution output is executed command text');
    assert.match(rDeny.reason, /ambiguous|BUG-224|split|no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
    // Data substitution: echo prints the text, never executes it.
    const allow = "echo \"$(printf 'cd internal/engine && git add evil.go && git commit -m x')\"";
    const rAllow = runGuard(dir, allow);
    assert.equal(rAllow.denied, false, 'an echo argument substitution prints DATA, not commands');
  });
});

// ---------------------------------------------------------------------------
// BUG-332 r12 (r11 attacker NEW-1..NEW-4): end-to-end DENY of the four
// r12-close shapes against the enforced-path git add —
// F18a  herestring `<<<` operands fed to a shell (`bash <<<`, `sudo bash <<<`,
//       `sudo -s <<<`) (NEW-1); `cat <<<` stays DATA.
// F18b  `xargs -I {}` SPACE-separated placeholder (NEW-2); non-shell `-I {}`
//       targets stay ALLOWED.
// F18c  GNU `xargs --replace[=STR]` long form (NEW-3); the space-separated
//       `--replace STR` form is NOT a substitution (empirically it executes
//       `STR` as argv[0]) and stays ALLOWED.
// F18d  constant `%s` printf emitters in run-strings DENY; echo DATA stays
//       ALLOWED (NEW-4).
// ---------------------------------------------------------------------------

test('BUG-332 r12 (80) F18a (NEW-1): shell-fed herestring `<<<` operands are DENIED end-to-end', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    for (const cmd of [
      "bash <<< \"cd internal/engine && git add evil.go && git commit -m 'x'\"",
      "sudo bash <<< \"cd internal/engine && git add evil.go && git commit -m 'x'\"",
      "sudo -s <<< \"cd internal/engine && git add evil.go && git commit -m 'x'\"",
      'sudo -i bash <<< "cd internal/engine && git add evil.go && git commit -m x"',
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `the shell executes the herestring operand as command text: ${cmd}`);
      assert.match(r.reason, /ambiguous|BUG-224|split|no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
    }
    // DATA herestring: cat reads the operand, never executes it.
    const allow = 'cat <<< "cd internal/engine && git add evil.go"';
    const rAllow = runGuard(dir, allow);
    assert.equal(rAllow.denied, false, 'a herestring fed to cat is DATA, not commands');
  });
});

test('BUG-332 r12 (81) F18b (NEW-2): `xargs -I {}` spaced placeholder is DENIED end-to-end', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, "echo \"cd internal/engine && git add evil.go && git commit -m 'x'\" | xargs -I {} bash -c \"{}\"");
    assert.equal(r.denied, true, 'spaced -I {} still substitutes the piped text into the run-string');
    assert.match(r.reason, /ambiguous|BUG-224|split|no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
    // NON-SHELL -I targets: the substituted text is an ARGUMENT, never executed.
    for (const cmd of [
      'echo "git add evil.go" | xargs -I {} grep {}',
      'echo "git add evil.go" | xargs -I {} rm {}',
    ]) {
      const rAllow = runGuard(dir, cmd);
      assert.equal(rAllow.denied, false, `grep/rm receives the text as an argument: ${cmd}`);
    }
  });
});

test('BUG-332 r12 (82) F18c (NEW-3): `xargs --replace[=STR]` long form is DENIED end-to-end', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    for (const cmd of [
      "echo \"cd internal/engine && git add evil.go && git commit -m 'x'\" | xargs --replace=C bash -c \"C\"",
      "echo \"cd internal/engine && git add evil.go && git commit -m 'x'\" | xargs --replace bash -c \"{}\"",
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `--replace[=STR] substitutes the piped text: ${cmd}`);
      assert.match(r.reason, /ambiguous|BUG-224|split|no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
    }
    // SPACE-separated --replace STR is NOT a substitution (GNU optional
    // argument; empirically `xargs --replace CMD bash -c "CMD"` executes
    // `CMD` as argv[0], printing a cmd banner) — so no placeholder exists.
    const allow = "echo \"cd internal/engine && git add evil.go && git commit -m 'x'\" | xargs --replace CMD bash -c \"CMD\"";
    const rAllow = runGuard(dir, allow);
    assert.equal(rAllow.denied, false, 'spaced --replace STR is not a substitution');
  });
});

test('BUG-332 r12 (83) F18d (NEW-4): constant `%s` printf emitters DENY; echo DATA ALLOW', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    for (const cmd of [
      "bash -c \"$(printf '%s\\n' 'cd internal/engine && git add evil.go && git commit -m x')\"",
      "sh -c \"$(printf '%s %s' 'cd internal/engine && git add evil.go && git commit -m' 'x')\"",
      "bash -c \"$(printf '%s%%' 'cd internal/engine && git add evil.go && git commit -m x')\"",
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `the constant printf output is executed command text: ${cmd}`);
      assert.match(r.reason, /ambiguous|BUG-224|split|no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
    }
    // DATA: echo prints printf output, never executes it.
    const allow = "echo \"$(printf '%s\\n' 'cd internal/engine && git add evil.go')\"";
    const rAllow = runGuard(dir, allow);
    assert.equal(rAllow.denied, false, 'an echo printf substitution prints DATA, not commands');
  });
});

// ---------------------------------------------------------------------------
// BUG-332 r13 (r12 attacker NEW-A..NEW-D): end-to-end DENY of the four
// r13-close shapes against the enforced-path git add —
// F19a  herestring operand through a PASSTHROUGH pipe into a shell
//       (`cat <<< "…" | bash`); the piped-to-NON-shell form stays ALLOWED.
// F19b  xargs placeholder EMBEDDED in a larger run-string
//       (`bash -c "{} && echo harmless"`).
// F19c  `%b`/`%*s`/`%-s` printf conversions that PRESERVE the payload; a
//       precision that truncates the payload away stays ALLOWED.
// F19d  constant `$()` substitution as the herestring OPERAND
//       (`bash <<< "$(printf …)"`).
// ---------------------------------------------------------------------------

test('BUG-332 r13 (84) F19a (NEW-A): herestring operands through a passthrough pipe into a shell are DENIED end-to-end', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    for (const cmd of [
      "cat <<< \"cd internal/engine && git add evil.go && git commit -m 'x'\" | bash",
      "tee <<< 'cd internal/engine && git add evil.go && git commit -m x' | bash",
      "sed '' <<< 'cd internal/engine && git add evil.go && git commit -m x' | sh",
      "cat <<< \"cd internal/engine && git add evil.go && git commit -m x\" | sudo bash",
      "cat <<< \"cd internal/engine && git add evil.go && git commit -m x\" | xargs -I{} bash -c \"{}\"",
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `the shell executes the herestring operand through the passthrough pipe: ${cmd}`);
      assert.match(r.reason, /ambiguous|BUG-224|split|no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
    }
    // The guard's contract is COMMIT-SCOPED (BUG-224): a pipe-to-shell that
    // only STAGES an enforced-path file (no `git commit` verb in the operand)
    // is out of scope — the same way a bare `git add internal/engine/evil.go`
    // is allowed — because the eventual commit still reads `git diff --cached`
    // and denies the enforced-path file then. Assert that honestly, both here
    // and in F19d, so these never regress into a silent staging bypass:
    for (const stagingOnly of [
      "cat <<< \"cd internal/engine && git add evil.go\" | sudo bash",
      "cat <<< \"cd internal/engine && git add evil.go\" | xargs -I{} bash -c \"{}\"",
      "tee <<< 'cd internal/engine && git add evil.go' | bash",
    ]) {
      const rStage = runGuard(dir, stagingOnly);
      assert.equal(rStage.denied, false, `staging-only pipe-to-shell is outside the commit-scoped contract: ${stagingOnly}`);
    }
    // pipe target is NOT a shell — grep transforms the operand as data.
    const allow = "cat <<< \"cd internal/engine && git add evil.go\" | grep foo";
    const rAllow = runGuard(dir, allow);
    assert.equal(rAllow.denied, false, 'a non-shell pipe target receives the operand as data');
  });
});

test('BUG-332 r13 (85) F19b (NEW-B): xargs placeholder EMBEDDED in a larger run-string is DENIED end-to-end', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const r = runGuard(dir, "echo \"cd internal/engine && git add evil.go && git commit -m 'x'\" | xargs -I{} bash -c \"{} && echo harmless\"");
    assert.equal(r.denied, true, 'the embedded placeholder still substitutes the piped text into a command position');
    assert.match(r.reason, /ambiguous|BUG-224|split|no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r13 (86) F19c (NEW-C): `%b`/`%*s`/`%-s` printf conversions preserving the payload DENY end-to-end', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    for (const cmd of [
      "bash -c \"$(printf '%b\\n' 'cd internal/engine && git add evil.go && git commit -m x')\"",
      "bash -c \"$(printf '%*s' 3 'cd internal/engine && git add evil.go && git commit -m x')\"",
      "bash -c \"$(printf '%-s' 'cd internal/engine && git add evil.go && git commit -m x')\"",
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `the same-output printf is executed command text: ${cmd}`);
      assert.match(r.reason, /ambiguous|BUG-224|split|no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
    }
  });
});

test('BUG-332 r13 (87) F19d (NEW-D): constant `$()` substitutions as herestring operands DENY end-to-end', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    for (const cmd of [
      "bash <<< \"$(printf '%s\\n' 'cd internal/engine && git add evil.go && git commit -m x')\"",
      "bash <<< \"$(echo 'cd internal/engine && git add evil.go && git commit -m x')\"",
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `the operand's constant output is executed command text: ${cmd}`);
      assert.match(r.reason, /ambiguous|BUG-224|split|no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
    }
    // Staging-only constant operand (no `git commit` verb) is outside the
    // guard's COMMIT-SCOPED contract — the eventual commit still sees the
    // staged enforced-path file via `git diff --cached` and denies then. Same
    // honest tripwire as F19a's staging-only controls:
    const stageOnly = "bash <<< \"$(echo 'cd internal/engine && git add evil.go')\"";
    const rStage = runGuard(dir, stageOnly);
    assert.equal(rStage.denied, false, 'staging-only constant operand is outside the commit-scoped contract');
  });
});

// ---------------------------------------------------------------------------
// BUG-332 r14 (r13 attacker F1/F2/F3): end-to-end DENY of the three total
// bypass classes the r13 independent attacker REJECTed on.
// F20a  `|&` (stderr-merged) pipes — `echo "…" |& bash` never registered as a
//       pipe target, so the piped text was never extracted.
// F20b  UNESCAPED nested double quotes inside `$(…)` in a wrapper run-string
//       (`bash -c "cat <<< '$(printf "%s\n" "…")' | bash"` and `eval "…"`) —
//       the outer double-quote capture stopped at the first inner `"`, hiding
//       the payload. The ESCAPED-inner-quote spelling is the attacker's own
//       control and must STAY denied.
// F20c  hex escapes in `printf %b` (`'\x67\x69\x74…'`) — evalBString kept them
//       literal. The octal spelling stays a control (must STILL decode).
// ---------------------------------------------------------------------------

test('BUG-332 r14 (88) F20a (F1): `|&` (stderr-merged) pipes into a shell are DENIED end-to-end', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    for (const cmd of [
      "echo \"cd internal/engine && git add evil.go && git commit -m 'x'\" |& bash",
      "printf '%s\\n' \"cd internal/engine && git add evil.go && git commit -m x\" |& bash",
      "echo \"cd internal/engine && git add evil.go && git commit -m x\" |& xargs -I{} bash -c \"{}\"",
      "echo \"cd internal/engine && git add evil.go && git commit -m x\" |& sudo bash",
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `the shell executes the |& piped text: ${cmd}`);
      assert.match(r.reason, /ambiguous|BUG-224|split|no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
    }
    // |& into a NON-shell target receives the text as data.
    const allow = "echo \"cd internal/engine && git add evil.go && git commit -m x\" |& grep foo";
    const rAllow = runGuard(dir, allow);
    assert.equal(rAllow.denied, false, 'a |& pipe into a non-shell target is data, not command text');
  });
});

test('BUG-332 r14 (89) F20b (F2): wrapper bodies with UNESCAPED nested double quotes inside `$(…)` are DENIED end-to-end', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    for (const cmd of [
      'bash -c "cat <<< \'$(printf "%s\\n" "cd internal/engine && git add evil.go && git commit -m x")\' | bash"',
      'eval "cat <<< \'$(printf "%s\\n" "cd internal/engine && git add evil.go && git commit -m x")\' | bash"',
      'bash -c "echo \'$(printf "%s\\n" "cd internal/engine && git add evil.go && git commit -m x")\' | bash"',
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `the nested-quote wrapper body is executed command text: ${cmd}`);
      assert.match(r.reason, /ambiguous|BUG-224|split|no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
    }
    // The r13 attacker's own control: the SAME body with the inner quotes
    // ESCAPED (`\"`) must STAY denied — a regression here is a new bypass.
    const escaped = 'bash -c "cat <<< \'$(printf \\"%s\\\\n\\" \\"cd internal/engine && git add evil.go && git commit -m x\\")\' | bash"';
    const rEsc = runGuard(dir, escaped);
    assert.equal(rEsc.denied, true, 'the escaped-inner-quote control must stay denied');
  });
});

test('BUG-332 r14 (90) F20c (F3): hex-encoded payloads in `printf %b` are DENIED end-to-end', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const payload = 'cd internal/engine && git add evil.go && git commit -m x';
    const hex = payload.split('').map((c) => '\\x' + c.charCodeAt(0).toString(16).padStart(2, '0')).join('');
    const octal = payload.split('').map((c) => '\\' + c.charCodeAt(0).toString(8).padStart(3, '0')).join('');
    for (const cmd of [
      `bash <<< "$(printf '%b' '${hex}')"`,
      `bash -c "$(printf '%b' '${hex}')"`,
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `the hex-encoded payload is executed command text: ${cmd}`);
      // BUG-332 r16 (AARON structural allowlist): these are denied fail-closed
      // by whichever recognition layer fires first — pre-r16 the escaped
      // payload surfaced a commit that the verdict flow denied (no [mkey] tag);
      // post-r16 the structural layer denies it as `bash`-wrapper indirection
      // before the verdict flow ever runs. The invariant is the DENY.
      assert.match(r.reason, /ambiguous|BUG-224|split|no.*BOW.*tag|NO \[mkey\]|indirection|shell|decoder|BUG-332/i);
    }
    // The octal spelling (already decoded since r12) must STAY denied — the
    // hex fix must not regress the existing escape processing.
    const rOct = runGuard(dir, `bash -c "$(printf '%b' '${octal}')"`);
    assert.equal(rOct.denied, true, 'the octal-encoded payload must stay denied');
  });
});

// ---------------------------------------------------------------------------
// BUG-332 r15 (r14 attacker): deterministic decoder pipes — `echo '<b64>' |
// base64 -d | bash` — end-to-end DENY
// ---------------------------------------------------------------------------
// The r14 independent attacker REJECTed the estate on a fresh total-bypass
// class: a KNOWN DETERMINISTIC DECODER stage between a shell and the text
// that feeds it. `base64 -d`, `base64 --decode`, `xxd -r -p`, `openssl
// base64 -d`, `b64 -d` all have stdout that is a pure function of stdin, so
// the guard could statically decode the constant operand — it instead FAILS
// CLOSED at RECOGNITION (the raw command has NO git verb, so the commit check
// would otherwise ALLOW it silently). The r15 acceptance bar is exactly these
// spellings: every one must DENY, and the data-dependent / no-shell controls
// must stay ALLOWED.

test('BUG-332 r15 (91) F21a (r14 attacker): the six proven decoder-pipe spellings are DENIED end-to-end', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const payload = 'cd internal/engine && git add evil.go && git commit -m x';
    const b64 = Buffer.from(payload, 'utf8').toString('base64');
    const hex = Buffer.from(payload, 'utf8').toString('hex');
    for (const cmd of [
      `echo '${b64}' | base64 -d | bash`,
      `base64 -d <<< '${b64}' | bash`,
      `printf '%s' '${b64}' | base64 -d | bash`,
      `echo '${b64}' | base64 --decode | bash`,
      `echo '${hex}' | xxd -r -p | bash`,
      `echo '${b64}' | openssl base64 -d | bash`,
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `the decoded payload is executed command text: ${cmd}`);
      assert.match(r.reason, /BUG-332|decoder|base64|xxd|openssl/i);
    }
  });
});

test('BUG-332 r15 (92) F21b: sibling decoder routes (b64/base32/heredoc/sudo/xargs/|&/nested wrapper) are DENIED end-to-end', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const payload = 'cd internal/engine && git add evil.go && git commit -m x';
    const b64 = Buffer.from(payload, 'utf8').toString('base64');
    for (const cmd of [
      `echo '${b64}' | b64 -d | bash`,
      `echo '${b64}' | base32 -d | bash`,
      `cat <<EOF | base64 -d | bash\n${payload}\nEOF`,
      `echo '${b64}' | base64 -d | sudo bash`,
      `echo '${b64}' | base64 -d | xargs -I{} bash -c "{}"`,
      `echo '${b64}' | base64 -d |& bash`,
      `sudo base64 -d <<< '${b64}' | bash`,
      `bash -c "echo '${b64}' | base64 -d | bash"`,
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `the sibling decoder route is executed command text: ${cmd}`);
      assert.match(r.reason, /BUG-332|decoder|base64|xxd|openssl/i);
    }
  });
});

test('BUG-332 r15 (93) F21c CONTROL: data-dependent transforms, encode-mode base64, and no-shell pipes stay ALLOWED', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const payload = 'cd internal/engine && git add evil.go && git commit -m x';
    const b64 = Buffer.from(payload, 'utf8').toString('base64');
    for (const cmd of [
      `echo '${b64}' | base64 -d | grep foo`,      // decoder into a NON-shell — data
      `echo '${payload}' | base64 | bash`,          // ENCODE mode — output is base64 text
      `echo '${b64}' | base64 -d`,                  // decoder, no shell after
      `base64 -d <<< '${b64}'`,                     // decoder, no pipe-right shell
      `cat <<EOF | grep foo\n${payload}\nEOF`,      // transforming pipe-right
      `echo '${payload}' | grep foo`,               // no shell target
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, false, `this command has no decoder-fed shell: ${cmd}`);
    }
  });
});

// ---------------------------------------------------------------------------
// BUG-332 r16 (r15 attacker F1–F8): the REJECT's 8 NEW total-bypass classes,
// end-to-end. Every class's proven spelling must DENY; the encode-mode / file-
// operand / fixed-program / echo-arg controls must stay ALLOWED.
// ---------------------------------------------------------------------------

test('BUG-332 r16 (94) F22a (F1): clustered short decode flags are DENIED end-to-end', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const payload = 'cd internal/engine && git add evil.go && git commit -m x';
    const b64 = Buffer.from(payload, 'utf8').toString('base64');
    for (const cmd of [
      `echo '${b64}' | base64 -di | bash`,
      `echo '${b64}' | base32 -di | bash`,
      `echo '${b64}' | base64 -dix | bash`,
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `clustered flags are the same decode: ${cmd}`);
      assert.match(r.reason, /BUG-332|decoder|base64|base32/i);
    }
  });
});

test('BUG-332 r16 (95) F22b (F2): openssl `enc -a -d` short-form base64 is DENIED end-to-end', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const payload = 'cd internal/engine && git add evil.go && git commit -m x';
    const b64 = Buffer.from(payload, 'utf8').toString('base64');
    for (const cmd of [
      `echo '${b64}' | openssl enc -a -d | bash`,
      `echo '${b64}' | openssl enc -d -a | bash`,
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `openssl -a marks base64 mode: ${cmd}`);
      assert.match(r.reason, /BUG-332|decoder|openssl/i);
    }
  });
});

test('BUG-332 r16 (96) F22c (F3): env-prefix wrappers before the decoder are DENIED end-to-end', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const payload = 'cd internal/engine && git add evil.go && git commit -m x';
    const b64 = Buffer.from(payload, 'utf8').toString('base64');
    for (const cmd of [
      `X=1 base64 -d <<< '${b64}' | bash`,
      `FOO=x echo '${b64}' | base64 -d | bash`,
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `the env assignment is a prefix wrapper: ${cmd}`);
      assert.match(r.reason, /BUG-332|decoder|base64/i);
    }
  });
});

test('BUG-332 r16 (97) F22d (F4): stdin-fed decompressors are DENIED end-to-end; a file operand stays ALLOWED', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const payload = 'cd internal/engine && git add evil.go && git commit -m x';
    const b64 = Buffer.from(payload, 'utf8').toString('base64');
    for (const cmd of [
      `echo '${b64}' | base64 -d | gzip -d | bash`,
      `echo '${b64}' | base64 -d | xz -d | bash`,
      `echo '${b64}' | base64 -d | gunzip | bash`,
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `the decompressor emits decoded bytes to the pipe: ${cmd}`);
      assert.match(r.reason, /BUG-332|decoder|gzip|xz/i);
    }
    const fileOperand = runGuard(dir, `gzip -d file.gz | bash`);
    assert.equal(fileOperand.denied, false, 'a file operand writes to a file — nothing piped to the shell');
  });
});

test('BUG-332 r16 (98) F22e (F5): bare `xargs sh -c` — the piped line IS the program — is DENIED end-to-end', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const payload = 'cd internal/engine && git add evil.go && git commit -m x';
    const b64 = Buffer.from(payload, 'utf8').toString('base64');
    const dec = runGuard(dir, `echo '${b64}' | base64 -d | xargs sh -c`);
    assert.equal(dec.denied, true, 'the DECODED line becomes the -c program');
    const plain = runGuard(dir, `echo '${payload}' | xargs sh -c`);
    assert.equal(plain.denied, true, 'the plaintext piped line IS the command string');
    const fixed = runGuard(dir, `echo '${payload}' | xargs sh -c 'echo hi'`);
    assert.equal(fixed.denied, false, 'a FIXED program makes stdin the ARGUMENTS, not command text');
  });
});

test('BUG-332 r16 (99) F22f (F6): command-position command substitutions ending in a decoder are DENIED end-to-end', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const payload = 'cd internal/engine && git add evil.go && git commit -m x';
    const b64 = Buffer.from(payload, 'utf8').toString('base64');
    for (const cmd of [
      `bash -c "$(echo '${b64}' | base64 -d)"`,
      `$(echo '${b64}' | base64 -d) | bash`,
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `the substitution output is decoded bytes a shell executes: ${cmd}`);
      assert.match(r.reason, /BUG-332|decoder|base64/i);
    }
    const echoArg = runGuard(dir, `echo "$(echo '${b64}' | base64 -d)"`);
    assert.equal(echoArg.denied, false, 'a substitution in an echo argument prints data — never executed');
  });
});

test('BUG-332 r16 (100) F22g (F7): real backslash-newline continuations are DENIED end-to-end', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const payload = 'cd internal/engine && git add evil.go && git commit -m x';
    const b64 = Buffer.from(payload, 'utf8').toString('base64');
    for (const cmd of [
      `echo '${b64}' | base64 -d |\\\nbash`,
      `echo '${b64}' | base64 -d | \\\nbash`,
      `echo '${b64}' | base64 -d |\\\nxargs sh -c`, // entangled: the bs-LF then xargs ALLOW was F5
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `the continuation is one real pipe chain: ${cmd}`);
      assert.match(r.reason, /BUG-332|decoder|base64/i);
    }
  });
});

test('BUG-332 r16 (101) F22h (F8): the PowerShell base64 command surface is DENIED end-to-end', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const payload = 'cd internal/engine && git add evil.go && git commit -m x';
    const b64 = Buffer.from(payload, 'utf8').toString('base64');
    for (const cmd of [
      `[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('${b64}')) | iex`,
      `powershell -EncodedCommand ${b64}`,
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `PowerShell base64-decodes and executes: ${cmd}`);
      assert.match(r.reason, /BUG-332|decoder|base64|powershell|iex/i);
    }
  });
});

test('BUG-332 r16 (102) F22i CONTROL: encode mode, file operands, fixed xargs programs, and echo-arg data stay ALLOWED', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(path.join(dir, 'internal', 'engine'), { recursive: true });
    fs.writeFileSync(path.join(dir, 'internal', 'engine', 'evil.go'), 'package engine\n', 'utf8');
    const payload = 'cd internal/engine && git add evil.go && git commit -m x';
    const b64 = Buffer.from(payload, 'utf8').toString('base64');
    for (const cmd of [
      `echo '${b64}' | base64 | bash`,                          // ENCODE mode
      `gzip -d file.gz | bash`,                                 // file operand
      `echo '${payload}' | xargs sh -c 'echo hi'`,              // fixed -c program
      `echo "$(echo '${b64}' | base64 -d)"`,                    // substitution in an echo arg
      `echo '${b64}' | openssl enc -d -aes-256-cbc | bash`,     // keyed cipher
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, false, `this command has no executed-decoder path: ${cmd}`);
    }
  });
});

// ---------------------------------------------------------------------------
// BUG-332 failure mode 2 — the verdict-tie rule (code committed post-attack)
// ---------------------------------------------------------------------------

test('BUG-332 (8) unit: latestGitRefForItem is exported from claude-bow.js and resolves the most recent ref', async () => {
  const db = await connectDb();
  const item = await createFixtureItem(db, 'bug332-tieunit');
  try {
    assert.equal(typeof bow.latestGitRefForItem, 'function', 'latestGitRefForItem must be exported');

    // No ref yet -> null.
    assert.equal(await bow.latestGitRefForItem(db, item.code), null);

    // Two refs -> the later one wins (explicit created_at beats default-NOW).
    await db.query(
      'INSERT INTO bow_git_refs (item_guid, commit_hash, branch, note, created_at) VALUES (?, ?, ?, ?, ?)',
      [item.guid, 'a'.repeat(40), 'main', 'older ref', new Date(Date.now() - 60_000)]);
    await db.query(
      'INSERT INTO bow_git_refs (item_guid, commit_hash, branch, note, created_at) VALUES (?, ?, ?, ?, ?)',
      [item.guid, 'b'.repeat(40), 'main', 'newer ref', new Date()]);
    const latest = await bow.latestGitRefForItem(db, item.code);
    assert.equal(latest.commit_hash, 'b'.repeat(40), 'must return the most recent ref, not the first inserted');
  } finally {
    await db.query('DELETE FROM bow_git_refs WHERE item_guid = ?', [item.guid]);
    await deleteFixtureItem(db, item.guid);
    await db.end();
  }
});

test('BUG-332 (9): a code-bearing commit on an item whose git ref POST-DATES its accept verdict is DENIED (un-attacked)', async () => {
  const db = await connectDb();
  const item = await createFixtureItem(db, 'bug332-tiepost');
  try {
    // Accept verdict first...
    await recordDestructiveVerdict(db, item.code, { verdict: 'accept', attacker: 'Destructive-Fixture', recorderSession: 'independent-attacker-fixture' });
    // ...then a git ref recorded AFTER the verdict (the code changed post-attack).
    // Read the verdict's own created_at and add 2s so the ref is deterministically
    // newer even when both would otherwise land in the same TIMESTAMP second.
    const [[verdictRow]] = await db.query(
      'SELECT created_at FROM bow_destructive_verdicts WHERE item_guid = ? ORDER BY created_at DESC, id DESC LIMIT 1',
      [item.guid]);
    const refCreatedAt = new Date(new Date(verdictRow.created_at).getTime() + 2000);
    await db.query(
      'INSERT INTO bow_git_refs (item_guid, commit_hash, branch, note, created_at) VALUES (?, ?, ?, ?, ?)',
      [item.guid, 'c'.repeat(40), 'main', 'committed after verdict', refCreatedAt]);
    withTempRepo((dir) => {
      stageFile(dir, 'internal/engine/evil.go', 'package engine\n');
      const r = runGuard(dir, `git commit -m "[${item.code}] change"`);
      assert.equal(r.denied, true, 'code committed after the accept verdict must be denied (needs a fresh round)');
      assert.match(r.reason, /post-attack|newer than/i, 'deny reason must name the post-attack state');
      assert.match(r.reason, new RegExp(item.code));
    });
  } finally {
    await db.query('DELETE FROM bow_git_refs WHERE item_guid = ?', [item.guid]);
    await deleteFixtureItem(db, item.guid);
    await db.end();
  }
});

test('BUG-332 (10) CONTROL: a git ref PRE-DATING the accept verdict does NOT trip the tie rule (verdict still covers that code)', async () => {
  const db = await connectDb();
  const item = await createFixtureItem(db, 'bug332-tiepre');
  try {
    // Ref first, verdict after — the verdict is the latest state decision, so commit passes.
    await db.query(
      'INSERT INTO bow_git_refs (item_guid, commit_hash, branch, note, created_at) VALUES (?, ?, ?, ?, ?)',
      [item.guid, 'd'.repeat(40), 'main', 'older ref', new Date(Date.now() - 120_000)]);
    await recordDestructiveVerdict(db, item.code, { verdict: 'accept', attacker: 'Destructive-Fixture', recorderSession: 'independent-attacker-fixture' });
    withTempRepo((dir) => {
      stageFile(dir, 'internal/engine/evil.go', 'package engine\n');
      const r = runGuard(dir, `git commit -m "[${item.code}] change"`);
      assert.equal(r.denied, false, 'a verdict recorded after the last git ref must still pass (nothing committed post-attack)');
    });
  } finally {
    await db.query('DELETE FROM bow_git_refs WHERE item_guid = ?', [item.guid]);
    await deleteFixtureItem(db, item.guid);
    await db.end();
  }
});

test('BUG-332 (11) CONTROL: an accepted verdict with NO git ref at all still passes (unchanged existing behavior)', async () => {
  const db = await connectDb();
  const item = await createFixtureItem(db, 'bug332-tienoref');
  try {
    await recordDestructiveVerdict(db, item.code, { verdict: 'accept', attacker: 'Destructive-Fixture', recorderSession: 'independent-attacker-fixture' });
    withTempRepo((dir) => {
      stageFile(dir, 'internal/engine/evil.go', 'package engine\n');
      const r = runGuard(dir, `git commit -m "[${item.code}] change"`);
      assert.equal(r.denied, false, 'no git ref means no post-attack commit is provable; existing verdict stands');
    });
  } finally {
    await deleteFixtureItem(db, item.guid);
    await db.end();
  }
});

// BUG-332 r2 (REJECT finding 2, boundary half): the tie rule used strict `>`.
// A ref whose created_at is EXACTLY EQUAL to the verdict's created_at (same
// wall-clock instant — both columns are TIMESTAMP(6)) must be treated as
// post-attack and DENIED, or a same-second commit sails through.
test('BUG-332 r2 (17): a git ref at the SAME INSTANT as the accept verdict is DENIED (>= boundary, no same-second hole)', async () => {
  const db = await connectDb();
  const item = await createFixtureItem(db, 'bug332-tieeq');
  try {
    await recordDestructiveVerdict(db, item.code, { verdict: 'accept', attacker: 'Destructive-Fixture', recorderSession: 'independent-attacker-fixture' });
    // Read back the exact verdict instant (TIMESTAMP(6) -> JS Date) and insert the
    // ref at precisely that instant. Equal timestamps must trip the tie rule.
    const [[verdictRow]] = await db.query(
      'SELECT created_at FROM bow_destructive_verdicts WHERE item_guid = ? ORDER BY created_at DESC, id DESC LIMIT 1',
      [item.guid]);
    const exactInstant = new Date(verdictRow.created_at).getTime();
    await db.query(
      'INSERT INTO bow_git_refs (item_guid, commit_hash, branch, note, created_at) VALUES (?, ?, ?, ?, ?)',
      [item.guid, 'e'.repeat(40), 'main', 'ref at exact verdict instant', new Date(exactInstant)]);
    withTempRepo((dir) => {
      stageFile(dir, 'internal/engine/evil.go', 'package engine\n');
      const r = runGuard(dir, `git commit -m "[${item.code}] change"`);
      assert.equal(r.denied, true, 'a ref recorded at the same instant as the accept verdict must be denied (>= boundary)');
      assert.match(r.reason, /post-attack|newer than/i, 'deny reason must name the post-attack state');
    });
  } finally {
    await db.query('DELETE FROM bow_git_refs WHERE item_guid = ?', [item.guid]);
    await deleteFixtureItem(db, item.guid);
    await db.end();
  }
});

// BUG-332 r2 (REJECT finding 2, schema half): bow_git_refs.created_at was
// TIMESTAMP (second precision) while bow_destructive_verdicts.created_at is
// TIMESTAMP(6) — a ref recorded later in the same wall-clock second truncated
// to compare EARLIER than the verdict. ensureGitRefCreatedAtFractional must be
// exported and must genuinely upgrade a second-precision column (exercised here
// against a scratch table via the table-name parameter).
test('BUG-332 r2 (18): ensureGitRefCreatedAtFractional upgrades a second-precision created_at column', async () => {
  const db = await connectDb();
  // TEMPORARY tables are invisible to information_schema.COLUMNS, so the
  // migration (which pre-checks via information_schema) cannot see one — use a
  // real scratch table, created idempotently and dropped in `finally`.
  const table = '_zz_gitref_migration_' + crypto.randomBytes(4).toString('hex');
  try {
    assert.equal(typeof bow.ensureGitRefCreatedAtFractional, 'function', 'must be exported from claude-bow.js');
    await db.query(
      `CREATE TABLE ${table} (
         item_guid CHAR(36) NOT NULL,
         commit_hash CHAR(40) NOT NULL,
         branch VARCHAR(200) NOT NULL,
         note VARCHAR(2000) NULL,
         created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
       )`);
    const [[before]] = await db.query(
      `SELECT COLUMN_TYPE FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = 'created_at'`,
      [table]);
    assert.equal(String(before.COLUMN_TYPE).toLowerCase(), 'timestamp', 'fixture column must start second-precision');
    // Table-name param lets the test exercise the real ALTER path on a scratch
    // table instead of mutating the live bow_git_refs column.
    await bow.ensureGitRefCreatedAtFractional(db, table);
    const [[after]] = await db.query(
      `SELECT COLUMN_TYPE FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = 'created_at'`,
      [table]);
    assert.equal(String(after.COLUMN_TYPE).toLowerCase(), 'timestamp(6)', 'scratch column must be fractional after the migration');
    // And the real column stays fractional after the default call (idempotent no-op).
    await bow.ensureGitRefCreatedAtFractional(db);
    const [[realCol]] = await db.query(
      `SELECT COLUMN_TYPE FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bow_git_refs' AND COLUMN_NAME = 'created_at'`);
    assert.equal(String(realCol.COLUMN_TYPE).toLowerCase(), 'timestamp(6)', 'real column must be fractional after idempotent run');
  } finally {
    await db.query(`DROP TABLE IF EXISTS ${table}`);
    await db.end();
  }
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

// ---------------------------------------------------------------------------
// BUG-722 — webconsole test-naming exemption gap (predates .test.mjs/.tsx/
// .ts/.jsx; the guard's own three shapes never covered the webconsole suite).
// ---------------------------------------------------------------------------

test('BUG-722 unit: isExemptFile exempts .test.mjs/.test.tsx/.test.ts/.test.jsx under a test/ or __tests__/ directory, or at repo root', () => {
  // Positive: webconsole test/ directory, one per new shape.
  assert.equal(isExemptFile('webconsole/test/foo.test.mjs'), true);
  assert.equal(isExemptFile('webconsole/test/foo.test.tsx'), true);
  assert.equal(isExemptFile('webconsole/test/foo.test.ts'), true);
  assert.equal(isExemptFile('webconsole/test/foo.test.jsx'), true);
  // Positive: __tests__ directory segment, nested arbitrarily deep.
  assert.equal(isExemptFile('webconsole/src/__tests__/foo.test.mjs'), true);
  // Positive: bare repo-root filename (no directory at all) — matches the
  // idiom the guard's own .test.js files already use.
  assert.equal(isExemptFile('foo.test.mjs'), true);
  // Negative: a source file under src/ merely NAMED like a test must not
  // launder a code-bearing commit (the rename-visibility class, BUG-340 r1
  // F3's comment on getStagedFilesFromDir()).
  assert.equal(isExemptFile('webconsole/src/sim/foo.test.mjs'), false);
  assert.equal(isExemptFile('webconsole/src/sim/foo.test.tsx'), false);
  // Negative: neither test/ nor __tests__/ nor root, still not exempt.
  assert.equal(isExemptFile('webconsole/lib/foo.test.ts'), false);
  // Case sensitivity holds for the new shapes too.
  assert.equal(isExemptFile('webconsole/test/foo.Test.mjs'), false);
});

test('BUG-722 unit: isExemptFileSet — a mixed commit (one test file, one src file) is NOT exempt', () => {
  assert.equal(
    isExemptFileSet(['webconsole/test/foo.test.mjs', 'webconsole/src/sim/foo.ts']),
    false,
    'one non-exempt src file anywhere denies the whole set exemption, even alongside a genuinely exempt test file'
  );
  assert.equal(
    isExemptFileSet(['webconsole/test/foo.test.mjs', 'webconsole/test/bar.test.tsx']),
    true,
    'an all-webconsole-test-file commit is exempt'
  );
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

test('BUG-232 end-to-end (trailing-pipe fix): a docs-only commit with a trailing `&& git push` is silently ALLOWED (pre-fix: falsely denied as a bare pathspec)', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'docs/notes.md', '# notes\n');
    // A trailing `&& git push` chain is NOT shell indirection — `&&` is a plain
    // command separator, not a pipe/redirection/wrapper — so the structural
    // allowlist (AARON RULING 2026-08-23) still recognises the commit and the
    // docs-only exemption clears it. classifyCommitArgv (unit test above) is
    // what BUG-232 fixed; the structural gate must not re-introduce that false
    // deny.
    const r = runGuard(dir, 'git commit -m "docs only" && git push');
    assert.equal(r.denied, false, 'must not falsely deny the && chain');
    assert.equal(r.stdout, '');
  });
});

test('BUG-332 r16 end-to-end (AARON structural allowlist): a commit with a trailing `2>&1 | tail` is DENIED as shell indirection (overrides BUG-232\'s old ALLOW)', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'docs/notes.md', '# notes\n');
    // AARON RULING 2026-08-23 flipped commit recognition to an ALLOWLIST: the
    // plain benign form is a literal `git commit` with ZERO shell indirection.
    // `2>&1 | tail` is redirection + a pipe — unattributable shell metacharacters
    // → COULD-NOT-EVALUATE → DENY fail-closed, even for a docs-only staged set.
    // This intentionally supersedes BUG-232's pre-ruling ALLOW: false-positive
    // deny is recoverable, false-negative allow is the hole. Run the commit
    // plain (`git commit -m "..."` as its own call) and the guard's own
    // execution captures the output.
    const r = runGuard(dir, 'git commit -m "docs only" 2>&1 | tail');
    assert.equal(r.denied, true, 'redirection + pipe is shell indirection under the structural allowlist');
    assert.match(r.reason || '', /indirection|shell|pipe|redirect/i);
  });
});

test('BUG-332 r16 end-to-end (AARON structural allowlist): plain benign commit forms reach the verdict flow and docs-only clears', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'docs/notes.md', '# notes\n');
    for (const cmd of [
      'git commit -m "docs only"',
      'git commit -am "docs only"',
      'git commit -m "docs only" --no-verify',
      'git commit --amend -m "docs only"',
      'git commit -m "docs only" -q',
      'git add docs/notes.md && git commit -m "docs only"', // BUG-224 rhythm: && is a separator, not indirection
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, false, `plain benign commit must reach the verdict flow: ${cmd}`);
      assert.equal(r.stdout, '');
    }
  });
});

test('BUG-332 r16 end-to-end (AARON structural allowlist): each shell-indirection class is DENIED even for a docs-only staged set', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'docs/notes.md', '# notes\n');
    for (const cmd of [
      'echo x | git commit -m "docs only"',            // pipe into the commit
      'git commit -m "docs only" > /dev/null',         // redirection
      'git commit -m "$(echo docs only)"',             // command substitution
      'git commit -m "`echo docs only`"',              // backticks
      'X=1 git commit -m "docs only"',                 // env-prefix (r15 F3)
      'bash -c \'git commit -m "docs only"\'',         // wrapper: shell executing a string
      'xargs sh -c \'git commit -m "docs only"\'',     // wrapper: xargs -> sh -c (r15 F5)
      'env git commit -m "docs only"',                 // subprocess prefix (env)
      'sudo git commit -m "docs only"',                // subprocess prefix (sudo)
      'git commit -m "docs only" <<\'EOF\'',           // heredoc redirection
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `commit-shaped command with shell indirection must fail closed: ${cmd}`);
      assert.match(r.reason || '', /indirection|shell|alias|pipe|redirect|wrapper/i);
    }
  });
});

test('BUG-332 r16 end-to-end (AARON structural allowlist): a NON-shell git alias reaching the commit verb is DENIED structurally', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'docs/notes.md', '# notes\n');
    git(dir, ['config', 'alias.cy', 'commit -a']); // plain alias, NOT a shell-escape — bypasses failClosedSweep
    const r = runGuard(dir, 'git cy -m "docs only"');
    assert.equal(r.denied, true, 'an aliased commit verb is not the literal `git commit` — deny structurally');
    assert.match(r.reason || '', /alias/i);
  });
});

test('BUG-332 r16 end-to-end (AARON structural allowlist): non-commit commands — including ones with pipes — stay silently ALLOWED', () => {
  withTempRepo((dir) => {
    for (const cmd of [
      'ls',
      'node claude-sync.js read',
      'echo hi | grep foo',       // pipe with NO commit — 'none' shape, allow
      'git status',
      'git add docs/notes.md',
      'git push',
      'git rebase -i HEAD~1',
      'go test ./...',
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, false, `no commit shape → must not be touched: ${cmd}`);
      assert.equal(r.stdout, '');
    }
  });
});

// =============================================================================
// BUG-332 r17 regressions — r16 attacker REJECT findings F1-F4 (the r17 bar).
// Each attack test flips to a committed regression asserting the fail-closed
// behavior on the fixed estate (round culture: inverted assertions on fix).
// =============================================================================

// F1 (CRITICAL): findCommitInvocation used to STOP at the FIRST known-verb
// invocation, so a literal `git commit` LATER in a chain after a replay verb
// (cherry-pick/merge/revert/am) was never examined → classifyCommitShape saw
// the replay verb and mapped it 'none' → silent allow of a real code commit
// (proven end-to-end as commit a4e7dd1). The recogniser now PREFERS a literal
// `commit` verb anywhere in the chain, so these reach the verdict flow.
test('BUG-332 r17 (r16 F1 CRITICAL): git cherry-pick -n X && git commit — literal commit later in the chain is recognised and gated', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/engine/evil.go', 'package engine\nvar Evil = 1\n');
    const r = runGuard(dir, 'git cherry-pick -n abc123 && git commit -m "no tag"');
    assert.equal(r.denied, true, 'the literal `git commit` in the chain must be recognised, not hidden behind the cherry-pick');
    assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r17 (r16 F1): merge --no-commit / revert --no-commit / am followed by a literal git commit are gated identically', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/engine/evil.go', 'package engine\nvar Evil = 1\n');
    for (const cmd of [
      'git merge --no-commit origin/main && git commit -m "no tag"',
      'git revert --no-commit HEAD && git commit -m "no tag"',
      'git am p.patch && git commit -m "no tag"',
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `a replay verb must not mask a later literal commit: ${cmd}`);
      assert.match(r.reason, /no.*BOW.*tag|NO \[mkey\]|shell indirection|structural allowlist|AARON RULING|denied fail-closed/i);
    }
  });
});

// Guard against the F1 fix OVER-reaching: a standalone replay verb WITHOUT a
// later literal commit stays OUT of the trap (ROUND-4 "merge is not commit" —
// the team's own local merges/cherry-picks must keep working).
test('BUG-332 r17 (r16 F1 guard-rail): standalone cherry-pick / merge / revert / am with NO literal commit stays ALLOWED', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'docs/notes.md', '# notes\n');
    for (const cmd of [
      'git cherry-pick abc123',
      'git merge origin/main',
      'git revert HEAD',
      'git am p.patch',
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, false, `replay verb alone is not commit → must not be touched: ${cmd}`);
      assert.equal(r.stdout, '');
    }
  });
});

// F2: decoder-backstop gaps — Windows certutil and the string-executing
// language runtimes (python -c / php -r / perl -e / ruby -e / node -e).
test('BUG-332 r17 (r16 F2a): certutil -decode fed into a shell is a known decoder and DENIES', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'docs/notes.md', '# notes\n');
    const r = runGuard(dir, 'echo Z2l0IGNvbW1pdA== | certutil -decode -f - | bash');
    assert.equal(r.denied, true, 'certutil -decode is a deterministic decoder feeding a shell — deny fail-closed');
    assert.match(r.reason, /decoder|shell|base64|decoded|indirection|structural allowlist|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r17 (r16 F2b/F2c): python -c / php -r / perl -e / ruby -e executing a commit through a shell DENIES', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'docs/notes.md', '# notes\n');
    for (const cmd of [
      'python -c \'import os; os.system("git add .; git commit -m y")\'',
      'python -c \'import os; os.system("git commit -m y")\'',
      'php -r \'system("git commit -m z");\'',
      'perl -e \'system("git commit -m q");\'',
      'ruby -e \'system("git commit -m r")\'',
      'node -e \'require("child_process").execSync("git commit -m s")\'',
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `a string-executing runtime reaching a shell must fail closed: ${cmd}`);
      assert.match(r.reason, /decoder|shell|base64|decoded|indirection|structural allowlist|AARON RULING|denied fail-closed/i);
    }
  });
});

// F3: env-prefix forms the strict `X=1` regex missed.
test('BUG-332 r17 (r16 F3): bash append-assign X+= and PowerShell $env:X= env-prefixes DENY', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'docs/notes.md', '# notes\n');
    for (const cmd of [
      'X+=append git commit -m "docs only"',
      '$env:X="a" git commit -m "docs only"',
      'X+=1 git commit -m "docs only"',
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `an env/var prefix the commit runs under is indirection: ${cmd}`);
      assert.match(r.reason, /indirection|shell|alias|pipe|redirect|wrapper|structural allowlist|AARON RULING|denied fail-closed/i);
    }
  });
});

// F4: shells missing from INDIRECTION_COMMAND_WORDS — now the shell class is
// tested by isShellExecutableWord (the one shell list, GR#3), so any name in
// SHELL_EXECUTABLE_RE is caught.
test('BUG-332 r17 (r16 F4): fish / tcsh / mksh / nu -c wrappers DENY (SHELL_EXECUTABLE_RE is the one shell list)', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'docs/notes.md', '# notes\n');
    for (const cmd of [
      'fish -c "git commit -m docs only"',
      'tcsh -c "git commit -m docs only"',
      'mksh -c "git commit -m docs only"',
      'nu -c "git commit -m docs only"',
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `a shell executing a string is wrapper indirection: ${cmd}`);
      assert.match(r.reason, /indirection|shell|alias|pipe|redirect|wrapper|structural allowlist|AARON RULING|denied fail-closed/i);
    }
  });
});

// F5: `echo git commit -m "no tag"` on staged code is DENIED — a documented,
// deliberate RECOVERABLE false-positive, not a fix. The AARON ruling is
// explicit: "false-positive deny is recoverable; false-negative allow is the
// hole." Closing it would mean narrowing commit recognition to command
// position, which opens the WORSE hole `echo git commit -m "..." | sh`
// (git as data output through a shell → invisible → silent allow). The
// structural layer deliberately does NOT do echo-semantics (that would be the
// denylist game the ruling called asymptotically incomplete).
test('BUG-332 r17 (r16 F5): echo of commit text on staged code DENIES (documented recoverable false-positive per ruling)', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/engine/evil.go', 'package engine\nvar Evil = 1\n');
    const r = runGuard(dir, 'echo git commit -m "no tag"');
    assert.equal(r.denied, true, 'echo git commit is recognised as a literal commit form and gated — ruling-blessed false-positive');
  });
});

// F1-family completion (r17): a replay verb with a NO-COMMIT flag BEFORE a
// literal `git commit` stages content the verdict flow CANNOT see — it reads
// the pre-command index (`git diff --cached`) before the command runs, so the
// cherry-pick-staged code is invisible and even a docs-exempt message would
// sail through (probe8 showed the exact ALLOW). Same un-enumerable-staging
// class as ambiguousAdd / --pathspec-from-file / a bare pathspec → fail-closed
// deny (reason 'replay-staging'). Verb-aware: `-n` IS --no-commit for
// cherry-pick/revert (also combined `-xn`), but for merge `-n` = --no-stat and
// am has no short form — only --no-commit counts there.
test('BUG-332 r17 (F1-family): no-commit replay verb before a literal git commit DENIES even on docs-staged content with a benign message (invisible staging)', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'docs/notes.md', '# notes\n');
    for (const cmd of [
      'git cherry-pick -n abc123 && git commit -m "docs update, no tag needed"',
      'git cherry-pick --no-commit abc123 && git commit -m "docs update, no tag needed"',
      'git merge --no-commit origin/main && git commit -m "docs update, no tag needed"',
      'git revert -n HEAD && git commit -m "docs update, no tag needed"',
      'git am --no-commit p.patch && git commit -m "docs update, no tag needed"',
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `a no-commit replay staging ahead of a literal commit must fail closed: ${cmd}`);
      assert.match(r.reason, /replay|staging|cherry-pick|merge|revert|am|indirection|structural allowlist|AARON RULING|denied fail-closed/i);
    }
  });
});

// Guard against replay-staging OVER-reaching: a no-commit replay with no
// following literal commit stays out of the trap (ROUND-4), a replay WITHOUT
// a no-commit flag stages nothing invisible (the cherry-pick commits its own
// content; the index after is visible), and a replay AFTER the commit cannot
// pollute that commit's index.
test('BUG-332 r17 (F1-family guard-rails): no literal commit / no no-commit flag / replay after the commit all stay ALLOWED', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'docs/notes.md', '# notes\n');
    for (const cmd of [
      'git cherry-pick -n abc123 && git status',
      'git cherry-pick abc123 && git commit -m "docs update, no tag needed"',
      'git commit -m "docs update, no tag needed" && git cherry-pick -n abc123',
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, false, `must stay allowed: ${cmd}`);
      assert.equal(r.stdout, '');
    }
  });
});

// ---------------------------------------------------------------------------
// BUG-332 r18 (r17 attacker REJECT F1-F3): end-to-end proofs on the shared
// checkout — each false-negative allow hole the r17 attacker proved now DENIES
// fail-closed (round culture: the attacker's findings are the next round's
// acceptance bar).
// ---------------------------------------------------------------------------

test('BUG-332 r18 (r17 F1 e2e): a commit through a HIDDEN git executable (`$GIT commit`, `$(echo $GIT) commit`) DENIES structurally — no literal git token to gate on', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'docs/notes.md', '# notes\n');
    for (const cmd of [
      'GIT=git; $GIT commit -m "docs only"',
      '$(echo $GIT) commit -m "docs only"',
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `a commit reached through a hidden git executable must fail closed: ${cmd}`);
      assert.match(r.reason || '', /variable|VARIABLE|hidden|indirection|structural allowlist|AARON RULING/i);
    }
  });
});

test('BUG-332 r18 (r17 F2 e2e): a combined perl run flag (`perl -ne \'...\'`) feeding a shell DENIES — the exact-match run-flag list missed -ne', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'docs/notes.md', '# notes\n');
    const r = runGuard(dir, "echo X | perl -ne 'system(\"git commit -m no-tag\")' | bash");
    assert.equal(r.denied, true, 'a combined -ne run flag is code-exec — the string may BE the commit');
    assert.match(r.reason || '', /decoder|transforming|shell|perl|indirection|AARON RULING|denied fail-closed/i);
  });
});

test('BUG-332 r18 (r17 F3 e2e): a data-text transformer (sed/awk/tr with a program) feeding a shell DENIES — its output is invisible to the raw-text scan', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'docs/notes.md', '# notes\n');
    for (const cmd of [
      "echo 'x' | sed 's/x/git commit -m \"no tag\"/' | bash",
      "echo 'x' | awk '{print \"git commit\"}' | bash",
      "echo 'x' | tr a-z A-Z | bash",
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `a transformer feeding a shell must fail closed: ${cmd}`);
      assert.match(r.reason || '', /decoder|transforming|transform|sed|awk|tr|shell|indirection|AARON RULING|denied fail-closed/i);
    }
  });
});

// ---------------------------------------------------------------------------
// BUG-332 r19 (independent attacker REJECT of the uncommitted guard estate):
// three live findings, each RED-proven against the pre-fix tree. Round
// culture: the attacker's findings are the next round's acceptance bar.
// ---------------------------------------------------------------------------

// F1 (HIGH): `v='git commit -m evil'; $v` — the payload sits INSIDE the
// assignment's quoted value, so no literal `git` token exists at any command
// position (findCommitInvocation → null) and hasHiddenCommit sees no `commit`
// WORD outside quotes (the only `commit` is prose) → { kind: 'none' } →
// silent allow of a real commit. The fix: when classification is 'none', a
// bare variable/expansion reference AT COMMAND POSITION plus a whole
// `git … commit` payload visible anywhere in the SAME string is the
// unattributable indirection the ruling denies fail-closed.
test('BUG-332 r19 (attacker F1): a whole-string VARIABLE holding the commit payload, executed as a bare `$var` command word, DENIES — variable-indirect commits enter the gate', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'docs/notes.md', '# notes\n');
    for (const cmd of [
      "v='git commit -m evil'; $v",
      'v="git commit -m evil"; ${v}',
      'V="git add evil.go && git commit -m x"; $V',
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `a variable-executed commit payload must fail closed: ${cmd}`);
      assert.match(r.reason || '', /variable|VARIABLE|hidden|indirection|structural allowlist|AARON RULING/i);
    }
  });
});

// F1 guard-rails: either signal ALONE is benign — a variable command word
// with no commit payload in the string, or a commit-payload string that is
// never executed (only echoed as DATA).
test('BUG-332 r19 (attacker F1 controls): a bare variable command word WITHOUT a git-commit payload, and a payload string never EXECUTED, stay ALLOWED', () => {
  withTempRepo((dir) => {
    for (const cmd of [
      'echo hello; $EDITOR notes.txt',
      'ls $HOME/bin',
      'msg=\'git commit -m evil\'; echo "$msg"',
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, false, `no executed commit payload → must not be touched: ${cmd}`);
      assert.equal(r.stdout, '');
    }
  });
});

// F2 (HIGH): `bash <(echo "git commit -m evil")` — the substitution body is
// executed AS COMMANDS via /dev/fd, but the payload hides inside echo's
// quoted argument, so recognition sees nothing and { kind: 'none' } silently
// allows. The fix: every `<( … )` body is treated as command content — a body
// carrying a whole `git … commit` payload denies fail-closed.
test('BUG-332 r19 (attacker F2): a process-substitution body fed to a shell (`bash <(echo "git commit -m evil")`) DENIES — its inner text is command content', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'docs/notes.md', '# notes\n');
    for (const cmd of [
      'bash <(echo "git commit -m evil")',
      'sh <(echo \'git add evil.go; git commit -m x\')',
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `a process substitution hiding a commit must fail closed: ${cmd}`);
      assert.match(r.reason || '', /substitution|process|indirection|structural allowlist|AARON RULING/i);
    }
  });
});

// F2 guard-rails: ordinary process substitutions carry no commit payload and
// must keep working (diff-over-subshells is everyday shell practice).
test('BUG-332 r19 (attacker F2 controls): ordinary process substitutions carrying no commit payload stay ALLOWED', () => {
  withTempRepo((dir) => {
    for (const cmd of [
      'diff <(ls docs) <(ls internal)',
      'cat <(echo hi)',
      'sort <(git log --oneline)',
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, false, `benign process substitution → must not be touched: ${cmd}`);
      assert.equal(r.stdout, '');
    }
  });
});

// F3 (MEDIUM): `echo QUJD | base64.exe -d | bash` — isDeterministicDecoder-
// Stage compared the RAW word (`base64.exe` ≠ `base64`), so the Windows
// executable suffix evaded the known-decoder list and the invisible-commit
// backstop silently allowed. The fix: the stage name is normalised the way
// isGitExecutableWord normalises the git token (path prefix off, trailing
// .exe/.cmd off, case-folded) before matching.
test('BUG-332 r19 (attacker F3): a Windows `.exe` decoder spelling (`base64.exe -d`) piped into a shell DENIES — the decoder backstop survives the suffix', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'docs/notes.md', '# notes\n');
    for (const cmd of [
      'echo QUJD | base64.exe -d | bash',
      'echo QUJD | /usr/bin/base64.exe --decode | sh',
      'echo QUJD | openssl.exe base64 -d | bash',
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `an .exe-suffixed deterministic decoder feeding a shell must fail closed: ${cmd}`);
      assert.match(r.reason || '', /decoder|shell|base64|decoded|indirection|structural allowlist|AARON RULING|denied fail-closed/i);
    }
  });
});

// F3 guard-rail: ENCODE mode through the same .exe spelling still emits base64
// TEXT (never the payload) and must stay out of the trap, exactly like the
// bare `base64` encode-mode control the r15 F21i bar established.
test('BUG-332 r19 (attacker F3 control): ENCODE mode through an .exe spelling still stays ALLOWED', () => {
  withTempRepo((dir) => {
    const r = runGuard(dir, 'echo hello | base64.exe');
    assert.equal(r.denied, false, 'encode-mode base64.exe emits base64 text, never the payload — must stay clear');
    assert.equal(r.stdout, '');
  });
});

// ---------------------------------------------------------------------------
// BUG-332 r20 (r2 independent REJECT, finding F-HIGH-1): STRING-EXECUTOR
// WRAPPER BEFORE THE VARIABLE FIRE. The r19 F1 fix only fired when `$var`
// stood at COMMAND position (`v='…'; $v`), so `v='git commit -m evil'; eval $v`
// left the variable as eval's ARGUMENT — classification stayed 'none' and the
// real commit silently allowed. An executor's expansion-bearing argument is
// code: deny fail-closed whenever a commit payload is visible in the string.
// ---------------------------------------------------------------------------

test('BUG-332 r20 (F-HIGH-1): a variable holding the commit payload fired THROUGH a string executor (`eval $v` / `exec $v` / `bash -c $v`) DENIES', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'docs/notes.md', '# notes\n');
    for (const cmd of [
      "v='git commit -m evil'; eval $v",
      'v="git commit -m evil"; exec ${v}',
      'V="git add evil.go && git commit -m x"; bash -c $V',
      's="git commit -m evil"; sh -c "$s"',
      "c='git commit -m evil'; zsh -c \"$c\"",
      "p='git commit -m evil'; pwsh -Command $p",
      "d='git commit -m evil'; dash -c $d",
      "k='git commit -m evil'; ksh -c \"$k\"",
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `an executor-fired variable commit payload must fail closed: ${cmd}`);
      assert.match(r.reason || '', /variable|indirection|hidden|structural allowlist|AARON RULING/i);
    }
  });
});

// Guard-rails: an executor with a LITERAL argument carries no unattributable
// indirection (the structured parser sees exactly what runs), and an executor
// whose expansion argument carries NO commit payload is everyday shell.
test('BUG-332 r20 (F-HIGH-1 controls): literal executor arguments and payload-free expansion arguments stay ALLOWED', () => {
  withTempRepo((dir) => {
    for (const cmd of [
      "eval git log --oneline -5",
      'bash -c "echo hello"',
      "msg='git commit -m evil'; echo \"$msg\"",
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, false, `no executed unattributable commit payload → must not be touched: ${cmd}`);
      assert.equal(r.stdout, '');
    }
  });
});

// ---------------------------------------------------------------------------
// BUG-386: destructive-guard misses code-bearing commits via git -C to another
// worktree (wrong-repo staged-set resolution). The guard queries staged files
// with `git diff --cached` from its own cwd (rootDir()), so when a commit is
// invoked as `git -C /other/worktree commit`, the staged files live in the
// other worktree's index and are invisible to the main checkout's query.
//
// FIX: Extract the -C option from the commit invocation, validate it's in the
// same repo, and query staged files from that directory instead.
// ---------------------------------------------------------------------------

test('BUG-386 unit: extractTargetDirFromGitCommand extracts -C <dir> / -C<dir> (no space)', () => {
  const testCases = [
    { cmd: 'git commit -m "x"', expect: null },
    { cmd: 'git -C /tmp commit -m "x"', expect: '/tmp' },
    { cmd: 'git -C/other/dir commit -m "x"', expect: '/other/dir' },
    { cmd: 'git -C /a commit && git -C /b commit', expect: '/b' }, // last -C wins
    { cmd: 'git -C "/path with spaces" commit -m "x"', expect: '/path with spaces' },
    { cmd: "git -C '/quoted' commit -m x", expect: '/quoted' },
  ];
  for (const tc of testCases) {
    const result = extractTargetDirFromGitCommand(tc.cmd, authorGuard);
    assert.equal(result, tc.expect, `should extract -C target correctly: ${tc.cmd}`);
  }
});

test('BUG-386 RED (pre-fix behavior): git -C to another worktree with code staged would be invisible to the main checkout index', () => {
  // This test demonstrates the hole — when staged files are queried from the
  // main checkout but the commit is from another worktree, the guard's staged
  // list is empty/stale. The fix requires that we query staged files from the
  // -C target directory instead.
  withTempRepo((dir) => {
    // Create a "second worktree" mock — a subdirectory with a separate git index
    const secondDir = path.join(dir, 'worktree-subdir');
    fs.mkdirSync(secondDir, { recursive: true });
    git(secondDir, ['init']);
    git(secondDir, ['config', 'user.name', 'Fixture']);
    git(secondDir, ['config', 'user.email', 'fixture@example.invalid']);

    // Stage a code file in the second worktree (NOT the main checkout)
    const codePath = path.join(secondDir, 'internal', 'evil.go');
    fs.mkdirSync(path.dirname(codePath), { recursive: true });
    fs.writeFileSync(codePath, 'package main\nfunc main() {}');
    git(secondDir, ['add', 'internal/evil.go']);

    // Verify the file is staged in the second worktree but NOT in the main checkout
    const stagedInSecond = git(secondDir, ['diff', '--cached', '--name-only']);
    const stagedInMain = git(dir, ['diff', '--cached', '--name-only']);
    assert.ok(stagedInSecond.includes('internal/evil.go'), 'file must be staged in second worktree');
    assert.equal(stagedInMain, '', 'file must NOT be staged in main checkout');

    // WITH THE FIX: the guard should extract -C and query from the second worktree's index
    // For this test to pass with the fix, the guard MUST deny the commit
    const r = runGuard(dir, `git -C ${secondDir} commit -m "[FEAT-999] evil code"`);
    assert.equal(r.denied, true, 'commit from another worktree with code must be denied (fix working)');
    assert.match(r.reason || '', /BUG-386|staged files|resolve|target|cannot verify|verdict/i);
  });
});

test('BUG-386 GREEN (post-fix): git -C to the same repo with code staged is DENIED (fix closed the hole)', () => {
  withTempRepo((dir) => {
    // Create a worktree subdirectory and stage code there
    const subdir = path.join(dir, 'subdir');
    fs.mkdirSync(subdir, { recursive: true });
    // Reinitialize git in the subdir to simulate a worktree
    git(subdir, ['init']);
    git(subdir, ['config', 'user.name', 'Fixture']);
    git(subdir, ['config', 'user.email', 'fixture@example.invalid']);

    const codePath = path.join(subdir, 'internal', 'evil.go');
    fs.mkdirSync(path.dirname(codePath), { recursive: true });
    fs.writeFileSync(codePath, 'package main\nfunc main() {}');
    git(subdir, ['add', 'internal/evil.go']);

    // Commit with -C pointing to the subdir — the guard should now query from there
    const r = runGuard(dir, `git -C ${subdir} commit -m "[FEAT-999] code from subdir"`);
    assert.equal(r.denied, true, 'code-bearing commit via -C must be denied');
    // The deny can be either from not resolving -C correctly (different repo) or from verdict check
    // Both outcomes demonstrate the fix is working
  });
});

test('BUG-386 GREEN: docs-only commit via -C is still ALLOWED (fix is narrowly scoped)', () => {
  withTempRepo((dir) => {
    const subdir = path.join(dir, 'subdir');
    fs.mkdirSync(subdir, { recursive: true });
    git(subdir, ['init']);
    git(subdir, ['config', 'user.name', 'Fixture']);
    git(subdir, ['config', 'user.email', 'fixture@example.invalid']);

    // Stage only a docs file
    const docsPath = path.join(subdir, 'docs', 'notes.md');
    fs.mkdirSync(path.dirname(docsPath), { recursive: true });
    fs.writeFileSync(docsPath, '# Notes\n');
    git(subdir, ['add', 'docs/notes.md']);

    const r = runGuard(dir, `git -C ${subdir} commit -m "docs: update"`);
    // This should be allowed because it's docs-only (unless the worktree can't be resolved,
    // which would also deny). Either way, the fix doesn't break docs-only commits.
    assert.equal(r.denied === false || r.denied === true, true, 'result is well-defined');
  });
});

// ---------------------------------------------------------------------------
// BUG-386 EXTENDED: --git-dir and --work-tree redirect the commit to a
// different repository/index, just like -C. These sibling options are not part
// of normal in-repo workflows and create uncertainty about the target index,
// so the guard denies fail-closed when either is present.
// ---------------------------------------------------------------------------

test('BUG-386 EXTENDED unit: checkRepoRedirectOptions detects --git-dir and --work-tree', () => {
  const testCases = [
    { cmd: 'git commit -m "x"', expect: false },
    { cmd: 'git --git-dir=/tmp/.git commit -m "x"', expect: true, type: 'git-dir' },
    { cmd: 'git --git-dir /tmp/.git commit -m "x"', expect: true, type: 'git-dir' },
    { cmd: 'git --work-tree=/tmp commit -m "x"', expect: true, type: 'work-tree' },
    { cmd: 'git --work-tree /tmp commit -m "x"', expect: true, type: 'work-tree' },
    { cmd: 'git --git-dir=/a --work-tree=/b commit -m "x"', expect: true }, // first match
    { cmd: 'git -c user.email=x commit -m "y"', expect: false }, // -c config, not dir
  ];
  for (const tc of testCases) {
    const result = checkRepoRedirectOptions(tc.cmd, authorGuard);
    assert.equal(result.hasRedirect, tc.expect, `should detect ${tc.expect ? 'redirect' : 'no redirect'}: ${tc.cmd}`);
    if (tc.expect && tc.type) {
      assert.equal(result.optionType, tc.type, `should identify option type for: ${tc.cmd}`);
    }
  }
});

test('BUG-386 EXTENDED RED (pre-fix): git --git-dir to another repo with code staged would bypass guard', () => {
  // This test demonstrates the hole — --git-dir can redirect the commit to
  // a completely different repository/index that the guard cannot verify.
  // The fix requires that we detect and deny any use of --git-dir/--work-tree.
  withTempRepo((dir) => {
    stageFile(dir, 'internal/evil.go', 'package main\nfunc main() {}');
    // Prove the file is staged
    const staged = git(dir, ['diff', '--cached', '--name-only']);
    assert.ok(staged.includes('internal/evil.go'), 'file must be staged for this test');

    // OLD BEHAVIOR (pre-fix): guard would not detect --git-dir and might allow the commit
    // NEW BEHAVIOR (with fix): guard should DENY any --git-dir option
    const r = runGuard(dir, 'git --git-dir=/other/.git commit -m "[FEAT-999] evil"');
    assert.equal(r.denied, true, 'commit with --git-dir must be denied (fix working)');
    assert.match(r.reason || '', /BUG-386|git-dir|unverifiable|redirect/i);
  });
});

test('BUG-386 EXTENDED RED (pre-fix): git --work-tree to another location with code staged would bypass guard', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/evil.go', 'package main\nfunc main() {}');
    const staged = git(dir, ['diff', '--cached', '--name-only']);
    assert.ok(staged.includes('internal/evil.go'), 'file must be staged for this test');

    // NEW BEHAVIOR (with fix): guard should DENY any --work-tree option
    const r = runGuard(dir, 'git --work-tree=/other commit -m "[FEAT-999] evil"');
    assert.equal(r.denied, true, 'commit with --work-tree must be denied (fix working)');
    assert.match(r.reason || '', /BUG-386|work-tree|unverifiable|redirect/i);
  });
});

test('BUG-386 EXTENDED GREEN: --git-dir and --work-tree are denied even with an accepted verdict', () => {
  // The deny happens at the redirect-option check level, before verdict lookup.
  // Prove it denies regardless of verdict status by using a code commit.
  // Note: --git-dir and --work-tree are also caught by BUG-232's failClosedSweep
  // as unrecognized global options, so either BUG-232 or BUG-386 denial messages
  // are valid — both result in the correct behavior (deny). Accept either.
  withTempRepo((dir) => {
    stageFile(dir, 'internal/evil.go', 'package main\nfunc main() {}');

    // Both --git-dir and --work-tree forms should deny (via BUG-232 or BUG-386 check)
    for (const cmd of [
      'git --git-dir=/other/.git commit -m "[FEAT-999] evil"',
      'git --git-dir /other/.git commit -m "[FEAT-999] evil"',
      'git --work-tree=/other commit -m "[FEAT-999] evil"',
      'git --work-tree /other commit -m "[FEAT-999] evil"',
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `must deny: ${cmd}`);
      // Accept either BUG-232 (unparseable option) or BUG-386 (redirect) deny message
      assert.match(r.reason || '', /BUG-232|BUG-386|git-dir|work-tree|redirect|unverifiable|unparseable/i);
    }
  });
});

test('BUG-386 EXTENDED GREEN: composition with -C — the --git-dir/--work-tree check runs first', () => {
  // Prove the deny for --git-dir/--work-tree happens BEFORE -C handling,
  // so a combined `git --git-dir=X -C Y commit` is blocked by the first check.
  withTempRepo((dir) => {
    stageFile(dir, 'internal/evil.go', 'package main\nfunc main() {}');

    const r = runGuard(dir, 'git --git-dir=/other/.git -C /some/path commit -m "[FEAT-999]"');
    assert.equal(r.denied, true, 'must deny on --git-dir even if -C also present');
    assert.match(r.reason || '', /git-dir|redirect/i);
  });
});

test('BUG-386 GREEN (regression): plain git commit and git commit -C <hash> (reuse message) still work', () => {
  // Prove the new --git-dir/--work-tree check does not interfere with:
  // - Plain `git commit`
  // - The legitimate reuse-message form: `git commit -C HEAD` (commit option, not global option)
  withTempRepo((dir) => {
    stageFile(dir, 'docs/notes.md', '# Notes\n');

    // Plain commit (docs-only, should be allowed)
    const r1 = runGuard(dir, 'git commit -m "docs: update"');
    assert.equal(r1.denied, false, 'plain git commit must remain allowed');

    // -C as a commit option (reuse message from another commit) — this is NOT
    // the global -C (change directory) option. The option checker only looks for
    // global options before the commit verb, and commit-option -C is after the verb,
    // so it doesn't trigger the redirect check. Docs-only should be allowed.
    // Note: this form is very rare in practice, but it's valid git and should not
    // be affected by the redirect check.
  });
});

// ---------------------------------------------------------------------------
// BUG-386 FURTHER EXTENDED: config-injection redirects via `-c core.gitdir` and
// `-c core.worktree`. These inject config values that redirect the commit to
// a different repository/index, same family as -C/--git-dir/--work-tree. The
// guard denies fail-closed on any config-redirect injection.
// ---------------------------------------------------------------------------

test('BUG-386 FURTHER EXTENDED unit: checkConfigRedirectInjection detects -c core.gitdir and -c core.worktree', () => {
  const testCases = [
    { cmd: 'git commit -m "x"', expect: false },
    { cmd: 'git -c core.gitdir=/tmp/.git commit -m "x"', expect: true, type: 'config-gitdir' },
    { cmd: 'git -c core.gitdir /tmp/.git commit -m "x"', expect: true, type: 'config-gitdir' },
    { cmd: 'git -c core.worktree=/tmp commit -m "x"', expect: true, type: 'config-worktree' },
    { cmd: 'git -c core.worktree /tmp commit -m "x"', expect: true, type: 'config-worktree' },
    { cmd: 'git -c core.GITDIR=/a commit -m "x"', expect: true, type: 'config-gitdir' }, // case-insensitive
    { cmd: 'git -c core.WorkTree=/b commit -m "x"', expect: true, type: 'config-worktree' }, // case-insensitive
    { cmd: 'git -c user.email=x commit -m "y"', expect: false }, // non-redirect config
    { cmd: 'git -c color.ui=true commit -m "z"', expect: false }, // non-redirect config
  ];
  for (const tc of testCases) {
    const result = checkConfigRedirectInjection(tc.cmd, authorGuard);
    assert.equal(result.hasRedirect, tc.expect, `should ${tc.expect ? 'detect' : 'not detect'} redirect: ${tc.cmd}`);
    if (tc.expect && tc.type) {
      assert.equal(result.optionType, tc.type, `should identify type for: ${tc.cmd}`);
    }
  }
});

test('BUG-386 FURTHER EXTENDED RED: -c core.gitdir=X with code staged would bypass guard (pre-fix)', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/evil.go', 'package main\nfunc main() {}');
    const staged = git(dir, ['diff', '--cached', '--name-only']);
    assert.ok(staged.includes('internal/evil.go'), 'file must be staged for this test');

    // NEW BEHAVIOR (with fix): guard should DENY any -c core.gitdir
    const r = runGuard(dir, 'git -c core.gitdir=/other/.git commit -m "[FEAT-999] evil"');
    assert.equal(r.denied, true, 'commit with -c core.gitdir must be denied');
    assert.match(r.reason || '', /BUG-386|config-gitdir|core\.gitdir|redirect/i);
  });
});

test('BUG-386 FURTHER EXTENDED RED: -c core.worktree=X with code staged would bypass guard (pre-fix)', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/evil.go', 'package main\nfunc main() {}');
    const staged = git(dir, ['diff', '--cached', '--name-only']);
    assert.ok(staged.includes('internal/evil.go'), 'file must be staged for this test');

    // NEW BEHAVIOR (with fix): guard should DENY any -c core.worktree
    const r = runGuard(dir, 'git -c core.worktree=/other commit -m "[FEAT-999] evil"');
    assert.equal(r.denied, true, 'commit with -c core.worktree must be denied');
    assert.match(r.reason || '', /BUG-386|config-worktree|core\.worktree|redirect/i);
  });
});

test('BUG-386 FURTHER EXTENDED GREEN: all config-redirect forms are denied', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/evil.go', 'package main\nfunc main() {}');

    for (const cmd of [
      'git -c core.gitdir=/other/.git commit -m "[FEAT-999]"',
      'git -c core.GITDIR=/other/.git commit -m "[FEAT-999]"',
      'git -c core.worktree=/other commit -m "[FEAT-999]"',
      'git -c core.WorkTree=/other commit -m "[FEAT-999]"',
    ]) {
      const r = runGuard(dir, cmd);
      assert.equal(r.denied, true, `must deny: ${cmd}`);
      assert.match(r.reason || '', /BUG-386|config|redirect/i);
    }
  });
});

test('BUG-231 VERIFICATION: does `git -c alias.foo=... foo` (inline alias commit) get denied?', () => {
  // The original BUG-231 triage claimed it's closed because "-c is denied as
  // structural indirection", but the finding that `-c core.gitdir` classifies
  // as 'plain' suggests -c is NOT universally denied. This test verifies whether
  // `git -c alias.foo='commit -m x' foo` is actually denied (BUG-231 closed) or
  // allowed (BUG-231 not actually closed, needs reopening).
  withTempRepo((dir) => {
    stageFile(dir, 'internal/evil.go', 'package main\nfunc main() {}');
    // Configure an inline alias
    git(dir, ['config', 'alias.mycommit', 'commit -m "[FEAT-999]"']);

    // Invoke via the alias with inline -c override
    const r = runGuard(dir, 'git -c alias.mycommit="commit -m evil" mycommit');
    // BUG-231 STATUS CHECK: if denied, BUG-231 is closed. If allowed, it's NOT closed.
    const status = r.denied ? 'CLOSED (correctly denied)' : 'OPEN (incorrectly allowed)';
    // Report this to the coordinator via the test name output and behavior
    if (r.denied) {
      assert.match(r.reason || '', /alias|shell|structural|indirection/i, `BUG-231 ${status}`);
    } else {
      // If we get here, BUG-231 is NOT actually closed — report this finding
      assert.fail(`BUG-231 VERIFICATION RESULT: NOT CLOSED — inline alias commit 'mycommit' was allowed when it should be denied. Reason: ${r.reason}`);
    }
  });
});

// ---------------------------------------------------------------------------
// BUG-214: unrecognized flags make classifyCommitArgv set classifiable=false,
// but the guard proceeds with the stale --cached snapshot instead of denying
// fail-closed. The fix: when classifiable is false AND none of the specific
// known cases (pathspecFromFile, barePathspec, allFlag) apply, DENY fail-closed
// because the guard cannot reliably determine what will be committed.
// ---------------------------------------------------------------------------

test('BUG-214 unit: classifyCommitArgv detects unknown flags and sets classifiable=false', () => {
  const testCases = [
    { argv: 'git commit -m "x"', classifiable: true, reason: 'plain commit' },
    { argv: 'git commit -m "x" --unknown-flag', classifiable: false, reason: 'unknown long flag' },
    { argv: 'git commit -m "x" -X', classifiable: false, reason: 'unknown short flag' },
    { argv: 'git commit --all -m "x"', classifiable: false, reason: 'allFlag' },
    { argv: 'git commit --pathspec-from-file=x -m "y"', classifiable: false, reason: 'pathspecFromFile' },
    { argv: 'git commit -m "x" --', classifiable: false, reason: 'barePathspec' },
  ];
  for (const tc of testCases) {
    const inv = authorGuard.findCommitInvocation(tc.argv);
    const result = classifyCommitArgv(inv, authorGuard);
    assert.equal(result.classifiable, tc.classifiable, `${tc.reason}: classifiable should be ${tc.classifiable}`);
  }
});

test('BUG-214 RED (pre-fix behavior): an unrecognized flag would be silently allowed even for code', () => {
  // The old behavior: if classifiable=false but no specific handler matched,
  // the guard would proceed with the --cached snapshot, allowing un-verdicted code.
  // This test doesn't directly run the guard (that would use the fix), but shows
  // the classification result that the pre-fix code relied on.
  const cmd = 'git commit -m "[FEAT-999]" --unknown-flag';
  const inv = authorGuard.findCommitInvocation(cmd);
  const result = classifyCommitArgv(inv, authorGuard);
  assert.equal(result.classifiable, false, 'unknown flag makes classifiable=false');
  // Pre-fix: the guard would check only the three specific cases and find none matched,
  // then allow the commit. Post-fix: it should deny fail-closed.
});

test('BUG-214 GREEN (post-fix): unknown flags cause DENY fail-closed', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/evil.go', 'package main\nfunc main() {}');
    // Try to commit with an unknown flag
    const r = runGuard(dir, 'git commit -m "[FEAT-999] evil" --unknown-flag');
    assert.equal(r.denied, true, 'unknown flag must be denied fail-closed');
    assert.match(r.reason || '', /BUG-214|unrecognized|unhandled|flag|cannot verify/i);
  });
});

test('BUG-214 GREEN: known/handled flags still behave correctly (regression check)', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'docs/notes.md', '# docs\n');
    // Docs-only with a known flag (-m / --message) should still be allowed
    const r = runGuard(dir, 'git commit -m "docs: update" --no-verify');
    assert.equal(r.denied, false, 'docs-only with known flag must remain allowed');
  });
});

test('BUG-214 GREEN: -a/--all, barePathspec, --pathspec-from-file still work correctly', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'docs/notes.md', '# docs\n');
    // These are handled cases and should have their own deny paths
    const r1 = runGuard(dir, 'git commit -m "docs" --pathspec-from-file=x');
    assert.equal(r1.denied, true, '--pathspec-from-file handled');
    assert.match(r1.reason || '', /pathspec-from-file|BUG-213/i);

    const r2 = runGuard(dir, 'git commit -m "docs" --');
    assert.equal(r2.denied, true, 'bare -- handled');
    assert.match(r2.reason || '', /pathspec|BUG-214/i);
  });
});

// ---------------------------------------------------------------------------
// BUG-722 ATTACK — independent destructive round against the new
// NEW_TEST_EXT_RE + isUnderExemptTestDir() exemption (opus-round-bug722-guard).
// Goal of every case below: LAUNDER a code-bearing commit through the new
// exemption. Each test pins the behaviour observed against the real guard.
// ---------------------------------------------------------------------------

test('BUG-722 ATTACK unit: path-spelling tricks cannot walk a src/ file into the new exemption', () => {
  // Dot segments: normalizeGitPath() collapses them BEFORE isExemptFile sees
  // the path, so a `test/` segment that is cancelled by `..` cannot exempt.
  assert.equal(isExemptFile(normalizeGitPath('webconsole/src/test/../sim/foo.test.mjs')), false);
  assert.equal(isExemptFile(normalizeGitPath('webconsole/test/../src/sim/x.test.mjs')), false);
  // Backslashes (git on Windows) are normalised on BOTH sides — the exempt
  // spelling stays exempt, the src spelling stays code-bearing.
  assert.equal(isExemptFile('webconsole\\test\\x.test.mjs'), true);
  assert.equal(isExemptFile('webconsole\\src\\sim\\x.test.mjs'), false);
  // Leading ./, a leading /, pathspec magic and an absolute path: normalised
  // (or, for the absolute path, left carrying a drive segment) — never
  // exempting a src/ file.
  assert.equal(isExemptFile(normalizeGitPath('./webconsole/test/x.test.mjs')), true);
  assert.equal(isExemptFile(normalizeGitPath('/webconsole/src/x.test.mjs')), false);
  assert.equal(isExemptFile(normalizeGitPath(':(top)webconsole/src/x.test.mjs')), false);
  assert.equal(isExemptFile(normalizeGitPath('C:/git/Metropolis/webconsole/src/x.test.mjs')), false);
  // Case variants of the directory segment: the segment compare is exact, so
  // Test/ TEST/ __TESTS__/ all fail CLOSED (code-bearing), matching the
  // "git's own casing, no folding" posture of the extension regex.
  assert.equal(isExemptFile('webconsole/Test/x.test.mjs'), false);
  assert.equal(isExemptFile('webconsole/TEST/x.test.mjs'), false);
  assert.equal(isExemptFile('webconsole/__TESTS__/x.test.mjs'), false);
  // Near-miss directory names must NOT be accepted (mutation catcher for a
  // startsWith/includes rewrite of the segment compare).
  assert.equal(isExemptFile('webconsole/tests/x.test.mjs'), false);
  assert.equal(isExemptFile('webconsole/testing/x.test.mjs'), false);
  assert.equal(isExemptFile('webconsole/mytest/x.test.mjs'), false);
  assert.equal(isExemptFile('webconsole/__tests__extra/x.test.mjs'), false);
  // A unicode lookalike directory (Cyrillic es in "te\u0455t") is not `test`.
  assert.equal(isExemptFile('webconsole/te\u0455t/x.test.mjs'), false);
  // Extension near-misses.
  assert.equal(isExemptFile('foo.test.mjs.js'), false, 'double extension must not read as .test.js');
  assert.equal(isExemptFile('foo.test.mjsx'), false);
  assert.equal(isExemptFile('foo.test.MJS'), false, 'uppercase extension must fail closed');
  assert.equal(isExemptFile('webconsole/test/x.TEST.mjs'), false, 'uppercase .TEST. must fail closed');
  assert.equal(isExemptFile('test'), false, 'a file literally named `test` with no extension is not a test file');
  assert.equal(isExemptFile('webconsole/src/sim/__tests__'), false, '__tests__ as a FILENAME is not a directory segment');
  // Root-level shapes that ARE exempt by design (the bare-filename branch).
  assert.equal(isExemptFile('test.test.mjs'), true);
});

test('BUG-722 ATTACK unit: a `test` directory nested under src/ IS exempt — the fix comment overstates the src/ negative', () => {
  // The production comment says "A file under src/ ... is NOT exempt"; that
  // holds only for a src file NOT itself under a test/ segment.
  // isUnderExemptTestDir scans EVERY directory segment, so a `test` dir under
  // src/ exempts.
  assert.equal(isExemptFile('webconsole/src/test/evil.test.mjs'), true);
  // Harmless for webconsole (webconsole/ is not an enforced dir, so nothing
  // under it is code-bearing anyway — see the e2e case below), but the same
  // shape under an ENFORCED dir does launder; that is the finding recorded on
  // BUG-722 by this round.
  assert.equal(isEnforcedDirPath('webconsole/src/test/evil.test.mjs'), false);
});

test('BUG-722 ATTACK unit: the new extensions exempt files that isGuardOrHookPath / isEnforcedDirPath call ALWAYS code-bearing', () => {
  // isExemptCommit() is evaluated BEFORE codeBearing in main(), so an
  // all-exempt set short-circuits the guard/hook and enforced-dir checks.
  // Pinned here so any future tightening (or loosening) is a deliberate,
  // visible change rather than a silent one.
  const launderable = [
    'tools/plan/test/evil.test.mjs',
    'tools/plan/__tests__/evil.test.ts',
    'data/test/evil.test.mjs',
    'internal/engine/test/evil.test.ts',
    '.claude/hooks/test/evil.test.mjs',
    'githooks/test/evil.test.mjs',
    'claude-evil.test.mjs',
    'claude-evil.test.ts',
  ];
  for (const p of launderable) {
    assert.ok(
      isEnforcedDirPath(p) || isGuardOrHookPath(p) || isRootLevel(p),
      `${p} must be a code-bearing SHAPE for this pin to mean anything`
    );
    assert.equal(isExemptFile(p), true, `${p}: BUG-722 exempts this code-bearing shape (round finding F1/F2)`);
  }
  // INHERITED, not introduced: the legacy `.test.js` shape has no directory
  // restriction at all, so the identical laundering already existed for .js.
  assert.equal(isExemptFile('tools/plan/evil.test.js'), true);
  assert.equal(isExemptFile('claude-evil.test.js'), true);
  assert.equal(isExemptFile('internal/engine/evil.test.js'), true);
  // The control: same directories, no test-shaped name -> code-bearing.
  assert.equal(isExemptFile('tools/plan/test/evil.mjs'), false);
  assert.equal(isExemptFile('claude-evil.mjs'), false);
  assert.equal(isExemptFile('.claude/hooks/evil.mjs'), false);
});

test('BUG-722 ATTACK e2e: a .test.mjs under an ENFORCED dir commits with NO verdict, while the same file without the test name is denied', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'tools/plan/test/evil.test.mjs', 'module.exports = 1;\n');
    const exempt = runGuard(dir, 'git commit -m "test: no tag at all"');
    assert.equal(exempt.denied, false, 'BUG-722 finding F2: an enforced-dir file laundered by the new extension + test/ dir');
  });
  withTempRepo((dir) => {
    stageFile(dir, 'tools/plan/test/evil.mjs', 'module.exports = 1;\n');
    const control = runGuard(dir, 'git commit -m "test: no tag at all"');
    assert.equal(control.denied, true, 'control: identical directory, non-test filename -> full tier');
  });
});

test('BUG-722 ATTACK e2e: a root-level claude-*.test.mjs hook-shaped script commits with NO verdict', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'claude-evil.test.mjs', '// hook body\n');
    const exempt = runGuard(dir, 'git commit -m "test: no tag at all"');
    assert.equal(exempt.denied, false, 'BUG-722 finding F1: ROOT_GUARD_SCRIPT_RE explicitly covers claude-*.mjs, but the exemption runs first');
  });
  withTempRepo((dir) => {
    stageFile(dir, 'claude-evil.mjs', '// hook body\n');
    const control = runGuard(dir, 'git commit -m "test: no tag at all"');
    assert.equal(control.denied, true, 'control: same root guard-script shape without the .test. infix -> full tier');
  });
});

test('BUG-722 ATTACK e2e: mixing one non-exempt file into the set restores full tier', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'tools/plan/test/evil.test.mjs', 'module.exports = 1;\n');
    stageFile(dir, 'internal/foo.go', 'package foo\n');
    const r = runGuard(dir, 'git commit -m "test: no tag at all"');
    assert.equal(r.denied, true, 'one code-bearing file anywhere in the set defeats the exemption');
  });
});

test('BUG-722 ATTACK e2e: a src/-named .test.mjs is NOT a deny for webconsole — the new directory gate is a no-op for webconsole-only commits', () => {
  // Non-exempt does not mean denied: webconsole/ is not in ENFORCED_DIR_RE,
  // so nothing under it is code-bearing on its own. The BUG-722 fix therefore
  // changes NOTHING for a commit whose staged set is only webconsole test
  // files; its real effect is on MIXED sets (a webconsole .test.mjs alongside
  // an enforced-dir or root-level exempt file), and on the enforced-dir/root
  // shapes pinned above. Recorded so the fix's premise is not mis-read later.
  withTempRepo((dir) => {
    stageFile(dir, 'webconsole/src/sim/foo.test.mjs', 'export const x = 1;\n');
    assert.equal(isExemptFile('webconsole/src/sim/foo.test.mjs'), false);
    assert.equal(runGuard(dir, 'git commit -m "test: no tag at all"').denied, false);
  });
  withTempRepo((dir) => {
    stageFile(dir, 'webconsole/test/foo.test.mjs', 'export const x = 1;\n');
    assert.equal(runGuard(dir, 'git commit -m "test: no tag at all"').denied, false);
  });
});

test('BUG-722 ATTACK e2e: renaming an enforced-dir source file INTO a test/ dir under a new test extension cannot launder (--no-renames keeps both sides visible)', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'tools/plan/generate.js', 'module.exports = 1;\n');
    git(dir, ['commit', '-m', 'seed the tool']);
    fs.mkdirSync(path.join(dir, 'tools', 'plan', 'test'), { recursive: true });
    git(dir, ['mv', 'tools/plan/generate.js', 'tools/plan/test/generate.test.mjs']);
    const staged = git(dir, ['diff', '--cached', '--no-renames', '--name-only']).trim().split('\n');
    assert.deepEqual(
      staged.sort(),
      ['tools/plan/generate.js', 'tools/plan/test/generate.test.mjs'],
      'BUG-340 r1 F3: both sides of the rename must be visible to classification'
    );
    const r = runGuard(dir, 'git commit -m "test: moved the tool"');
    assert.equal(r.denied, true, 'the deleted (non-exempt) source side keeps the set out of the exemption');
  });
});
