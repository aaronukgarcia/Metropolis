/**
 * claude-startup.test.js — tests for claude-startup.js's session-summary
 * output, specifically FEAT-038 (git IDENTITY verification alongside the
 * existing git SYNC-state report).
 *
 * All fixtures run against THROWAWAY repos under the OS temp dir (never
 * this repo), removed in a `finally` — same rule as
 * claude-author-identity.test.js, whose withTempRepo()/withCwd() pattern
 * this file reuses, since checkGitIdentity()/gitIdentityLine() delegate to
 * claude-author-identity.js and its git() calls resolve relative to
 * process.cwd().
 *
 * Run: node --test claude-startup.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawnSync } = require('child_process');

const startup = require('./claude-startup.js');

const SANCTIONED_NAME = 'Sanctioned Contributor';
const SANCTIONED_EMAIL = 'sanctioned@example.invalid';
const UNRELATED_EMAIL = 'unrelated-real-address@example.invalid';

function withTempRepo(fn) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'startup-identity-fixture-'));
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

/** Builds a repo whose trunk history corroborates SANCTIONED_EMAIL (3
 * commits, at/above claude-author-identity.js's default HISTORY_THRESHOLD),
 * with local config ALSO set to SANCTIONED_EMAIL. */
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

function withCwd(dir, fn) {
  const prev = process.cwd();
  process.chdir(dir);
  try {
    return fn();
  } finally {
    process.chdir(prev);
  }
}

function captureLog(fn) {
  const lines = [];
  const original = console.log;
  console.log = (...args) => lines.push(args.join(' '));
  try {
    fn();
  } finally {
    console.log = original;
  }
  return lines.join('\n');
}

// ---------------------------------------------------------------------------
// GR#3 reuse check: this file must delegate to claude-author-identity.js's
// shared derivation, not reimplement a second identity-checking scanner.
// ---------------------------------------------------------------------------

test('FEAT-038 GR#3: claude-startup.js requires the shared claude-author-identity.js module rather than reimplementing identity derivation', () => {
  const src = fs.readFileSync(path.join(__dirname, 'claude-startup.js'), 'utf8');
  assert.match(src, /require\(['"]\.\/claude-author-identity\.js['"]\)/);
});

// ---------------------------------------------------------------------------
// checkGitIdentity() unit coverage
// ---------------------------------------------------------------------------

test('FEAT-038: checkGitIdentity() reports ok:true when the configured email is corroborated by trunk history', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    withCwd(dir, () => {
      const result = startup.checkGitIdentity();
      assert.equal(result.ok, true);
      assert.equal(result.email, SANCTIONED_EMAIL);
    });
  });
});

test('FEAT-038: checkGitIdentity() reports ok:false when the configured email is NOT corroborated by history (BUG-036 shape: local config repointed to an address history never saw)', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3); // history corroborates SANCTIONED_EMAIL
    git(dir, ['config', 'user.email', UNRELATED_EMAIL]); // config now points elsewhere
    withCwd(dir, () => {
      const result = startup.checkGitIdentity();
      assert.equal(result.ok, false);
      assert.equal(result.email, UNRELATED_EMAIL);
    });
  });
});

test('FEAT-038: checkGitIdentity() does NOT trivially pass just because deriveSanctioned() would always include the config value (regression against the "always true" bug this check exists to avoid)', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    git(dir, ['config', 'user.email', UNRELATED_EMAIL]);
    withCwd(dir, () => {
      // Sanity: deriveSanctioned() DOES include the (unrelated) configured
      // email unconditionally -- proving that a naive
      // `deriveSanctioned().has(configuredEmail())` check would have passed
      // here, which is exactly the bug this dedicated check must avoid.
      const identity = require('./claude-author-identity.js');
      assert.ok(identity.deriveSanctioned().has(UNRELATED_EMAIL), 'precondition: deriveSanctioned() trusts config unconditionally');
      // The startup check must still flag it.
      assert.equal(startup.checkGitIdentity().ok, false);
    });
  });
});

// ---------------------------------------------------------------------------
// gitIdentityLine() / printSessionSummary() behavioral coverage — proves the
// summary correctly flags a misconfigured identity and stays positive for a
// sanctioned one, in the actual captured session-start output.
// ---------------------------------------------------------------------------

test('FEAT-038: printSessionSummary() output stays positive (no warning) for a sanctioned git identity', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    withCwd(dir, () => {
      const captured = captureLog(() => {
        startup.printSessionSummary('bob', 'fake checkin output for this test', dir);
      });
      assert.match(captured, /GIT IDENTITY:.*sanctioned/i);
      assert.match(captured, new RegExp(SANCTIONED_EMAIL.replace('.', '\\.')));
      assert.doesNotMatch(captured, /⚠️ GIT IDENTITY/);
    });
  });
});

test('FEAT-038: printSessionSummary() output flags a misconfigured git identity with a clear warning', () => {
  withTempRepo((dir) => {
    initRepoWithHistory(dir, 3);
    git(dir, ['config', 'user.email', UNRELATED_EMAIL]);
    withCwd(dir, () => {
      const captured = captureLog(() => {
        startup.printSessionSummary('bob', 'fake checkin output for this test', dir);
      });
      assert.match(captured, /⚠️ GIT IDENTITY/);
      assert.match(captured, new RegExp(UNRELATED_EMAIL.replace('.', '\\.')));
      assert.match(captured, /BUG-036/);
    });
  });
});

test('FEAT-038: printSessionSummary() surfaces the GIT IDENTITY warning in the "surface immediately" mandatory-sequence line, mirroring the existing "git NOT SYNCED" wording', () => {
  const src = fs.readFileSync(path.join(__dirname, 'claude-startup.js'), 'utf8');
  assert.match(src, /git NOT SYNCED.*GIT IDENTITY warning|GIT IDENTITY warning.*git NOT SYNCED/s);
});

// ---------------------------------------------------------------------------
// Retire-Bob checkin fix (Aaron, 2026-08-18/19, coordinator scope extension):
// Bob was retired permanently but claude-startup.js's VALID_NAMES and several
// hardcoded example/fallback strings still named Bob (and were missing Bev,
// the lead's slot in the current team shape). VALID_NAMES now gates
// parseName()'s acceptance of a resolved checkin identity, so a stale
// checkin reply naming "Bob" must be treated exactly like any other garbage
// value — rejected, not accepted as a real identity.
// ---------------------------------------------------------------------------

test('VALID_NAMES is exactly the current four slots — Bob removed, Bro added, Ben still seeded', () => {
  assert.deepEqual(startup.VALID_NAMES, ['bill', 'ben', 'bev', 'bro']);
  assert.ok(!startup.VALID_NAMES.includes('bob'), 'Bob must not be accepted as a resolved identity any more');
});

test('LIVE_NAMES excludes retired Bob and parked Ben (BUG-344 roster drift)', () => {
  assert.deepEqual(startup.LIVE_NAMES, ['bill', 'bev', 'bro']);
  assert.ok(!startup.LIVE_NAMES.includes('ben'), 'parked Ben must not be a live slot');
  assert.ok(!startup.LIVE_NAMES.includes('bob'), 'retired Bob must not be a live slot');
});

test('parseName() rejects "YOU ARE: Bob" (retired name is not a valid resolved identity)', () => {
  assert.equal(startup.parseName('YOU ARE: Bob\nSession: abc-123\n'), null,
    'a checkin reply naming the retired Bob slot must not parse as a valid identity');
});

test('parseName() accepts "YOU ARE: Bev" (the lead\'s slot in the current team shape)', () => {
  assert.equal(startup.parseName('YOU ARE: Bev\nSession: abc-123\n'), 'bev');
});

test('false-pass guard: parseName() still accepts the unaffected slots (Bill, Ben)', () => {
  assert.equal(startup.parseName('YOU ARE: Bill\nSession: abc-123\n'), 'bill');
  assert.equal(startup.parseName('YOU ARE: Ben\nSession: abc-123\n'), 'ben');
});

test('no remaining "Bob" literal in claude-startup.js source outside historical/comment context', () => {
  const src = fs.readFileSync(path.join(__dirname, 'claude-startup.js'), 'utf8');
  // Every remaining "bob"/"Bob" occurrence, if any, must be inside a // comment
  // line (the retirement history note) — never inside a string literal that
  // could reach stdout/stderr or a VALID_NAMES-style array.
  const offendingLines = src.split('\n')
    .map((line, i) => ({ line, n: i + 1 }))
    .filter(({ line }) => /bob/i.test(line))
    .filter(({ line }) => !/^\s*\/\//.test(line.trim()) && !/^\s*\*/.test(line.trim()));
  assert.deepEqual(offendingLines, [], `unexpected non-comment "bob" literal(s): ${JSON.stringify(offendingLines)}`);
});
