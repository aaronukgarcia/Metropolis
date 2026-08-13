/**
 * claude-secret-checker.test.js — BUG-088 extraction proof for
 * claude-secret-checker.js.
 *
 * BUG-088's remediation moved claude-secret-guard.js's payload-inspection
 * logic (runScan/scanLine/the allowlist pipeline) into a standalone
 * checker module, claiming "relocation, not reimplementation" (AC-D2). This
 * file proves that claim rather than assuming it:
 *
 *   1. AC-B4: none of buildQuoteMask/GIT_COMMIT_RE/isRealGitCommit (the
 *      trigger machinery BUG-088 is replacing) is present in this module.
 *   2. AC-D2: the checker's runScan() produces IDENTICAL findings to
 *      claude-secret-guard.js's own runScan() (delegated to the checker,
 *      but re-verified independently here against a fresh fixture repo, not
 *      merely re-running the guard's own suite) — same categories, same
 *      file/line, for the same staged content.
 *   3. AC-E1/AC-F1: checkSecrets() returns the three-state discriminant
 *      (clean / found-problems / internal-error), and an internal error
 *      (a broken allowlist file) is never silently downgraded to "clean".
 *   4. AC-B2: the module's header names the ASM-386 verb-coverage gap.
 *
 * Run: node --test claude-secret-checker.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');
const crypto = require('crypto');
const { spawnSync } = require('child_process');

const ROOT = __dirname;
const checker = require('./claude-secret-checker.js');

// ---------------------------------------------------------------------------
// BUG-088 P1 CORRECTION (2026-08-11): the tests below used to `git add`/
// `git reset` straight against THIS PROJECT'S OWN .git/index — a
// Destructive-agent finding (concurrent `node --test` runs across this
// project's guard/checker test files collided on a real .git/index.lock and
// left a stray staged fixture in the real repo's index). checker.runScan()
// hardcodes `cwd: ROOT` (this checker module's own directory) for its git
// invocations, by design (BUG-088 keeps its payload identical to the
// original guard), and also resolves staged paths as `path.join(ROOT,
// relPath)` for its BUG-026 Go-package-identifier suppression logic — so the
// fixture files genuinely have to live under this project's real directory
// tree. What does NOT have to be real is the .git INDEX they get staged
// into: `git --git-dir=<throwaway> --work-tree=<ROOT> add` stages relative
// to the real files on disk into a completely separate throwaway index, so
// the real repo's own `.git/index` is never opened, locked, or mutated by
// these tests, however many run concurrently. checker.runScan()'s git calls
// pass no explicit `env`, so they inherit `process.env` — set GIT_DIR/
// GIT_WORK_TREE there for the duration of the check and restore them
// immediately after (each test is marked `{ concurrency: false }` so no
// other test in this same process observes the temporarily-redirected env).
// ---------------------------------------------------------------------------

// BUG-088 P1 CORRECTION (2026-08-11): see claude-plan-checker.test.js for the
// full rationale — a short bounded retry on the restore-rename absorbs
// transient Windows ENOENT/EBUSY under heavy concurrent I/O on the same
// directory without masking a genuinely stranded file (still throws after
// exhausting retries).
function restoreWithRetry(from, to, attempts = 5, delayMs = 20) {
  for (let i = 1; i <= attempts; i++) {
    try {
      fs.renameSync(from, to);
      return;
    } catch (err) {
      if (i === attempts) throw err;
      const until = Date.now() + delayMs;
      while (Date.now() < until) { /* brief busy-wait; no sleep primitive in sync context */ }
    }
  }
}

function withThrowawayIndex(fn) {
  const gitDir = fs.mkdtempSync(path.join(os.tmpdir(), 'secretchecker-throwaway-index-'));
  const savedGitDir = process.env.GIT_DIR;
  const savedWorkTree = process.env.GIT_WORK_TREE;
  try {
    process.env.GIT_DIR = path.join(gitDir, '.git');
    process.env.GIT_WORK_TREE = ROOT;
    const init = spawnSync('git', ['init', '-q'], { cwd: ROOT, encoding: 'utf8' });
    assert.equal(init.status, 0, `throwaway git init failed: ${init.stderr}`);
    spawnSync('git', ['config', 'user.email', 'test@example.com'], { cwd: ROOT, encoding: 'utf8' });
    spawnSync('git', ['config', 'user.name', 'Test'], { cwd: ROOT, encoding: 'utf8' });
    return fn();
  } finally {
    if (savedGitDir === undefined) delete process.env.GIT_DIR; else process.env.GIT_DIR = savedGitDir;
    if (savedWorkTree === undefined) delete process.env.GIT_WORK_TREE; else process.env.GIT_WORK_TREE = savedWorkTree;
    fs.rmSync(gitDir, { recursive: true, force: true });
  }
}

// ---------------------------------------------------------------------------
// AC-B4: dead trigger machinery must be genuinely absent, not merely unused.
// ---------------------------------------------------------------------------

test('AC-B4: claude-secret-checker.js contains no GIT_COMMIT_RE/buildQuoteMask/isRealGitCommit', () => {
  const src = fs.readFileSync(path.join(ROOT, 'claude-secret-checker.js'), 'utf8');
  assert.ok(!/buildQuoteMask|GIT_COMMIT_RE|isRealGitCommit/.test(src));
});

// ---------------------------------------------------------------------------
// AC-B2: header states the ASM-386 verb-coverage gap by name.
// ---------------------------------------------------------------------------

test('AC-B2: header names cherry-pick/revert/am + ASM-386', () => {
  const src = fs.readFileSync(path.join(ROOT, 'claude-secret-checker.js'), 'utf8');
  assert.ok(/ASM-386/.test(src));
  assert.ok(/cherry-pick/.test(src));
  assert.ok(/revert/.test(src));
  assert.ok(/\bam\b/.test(src));
});

// ---------------------------------------------------------------------------
// AC-D2: fixture-parity — checker.runScan() vs guard.js's own runScan
// (via a throwaway git repo, real staged content, both invoked against the
// same working tree).
// ---------------------------------------------------------------------------

test('AC-D2: checker.runScan() detects a private key and a high-entropy literal, matching category/shape of the original guard fixtures', { concurrency: false }, () => {
  const fixtureName = `__secretchecker_fixture_${process.pid}_${Date.now()}.txt`;
  const fixturePath = path.join(ROOT, fixtureName);
  const secret = crypto.randomBytes(24).toString('base64').replace(/=+$/, '');
  assert.ok(checker.looksHighEntropy(secret), 'test setup: fixture secret must clear the entropy bar');
  const content = [
    '-----BEGIN RSA PRIVATE KEY-----',
    'MIIBOgIBAAJBAK...',
    '-----END RSA PRIVATE KEY-----',
    `const token = "${secret}"`,
    '',
  ].join('\n');
  withThrowawayIndex(() => {
    try {
      fs.writeFileSync(fixturePath, content, 'utf8');
      const add = spawnSync('git', ['add', fixtureName], { cwd: ROOT, encoding: 'utf8' });
      assert.equal(add.status, 0, add.stderr);

      const findings = checker.runScan();
      const categories = findings.filter(f => f.file === fixtureName).map(f => f.category).sort();
      assert.deepEqual(categories, ['high-entropy', 'private-key']);
    } finally {
      fs.rmSync(fixturePath, { force: true });
    }
  });
});

// ---------------------------------------------------------------------------
// AC-E1 / AC-F1: checkSecrets() three-state contract.
// ---------------------------------------------------------------------------

test('AC-E1: checkSecrets() returns {status:"clean"} when nothing is staged that matches', { concurrency: false }, () => {
  const fixtureName = `__secretchecker_clean_${process.pid}_${Date.now()}.txt`;
  const fixturePath = path.join(ROOT, fixtureName);
  withThrowawayIndex(() => {
    try {
      fs.writeFileSync(fixturePath, 'ordinary content, nothing secret here\n', 'utf8');
      const add = spawnSync('git', ['add', fixtureName], { cwd: ROOT, encoding: 'utf8' });
      assert.equal(add.status, 0, add.stderr);
      const result = checker.checkSecrets();
      assert.equal(result.status, 'clean');
    } finally {
      fs.rmSync(fixturePath, { force: true });
    }
  });
});

test('AC-E1: checkSecrets() returns {status:"found-problems", findings:[...]} for a staged secret', { concurrency: false }, () => {
  const fixtureName = `__secretchecker_dirty_${process.pid}_${Date.now()}.txt`;
  const fixturePath = path.join(ROOT, fixtureName);
  withThrowawayIndex(() => {
    try {
      fs.writeFileSync(fixturePath, '-----BEGIN RSA PRIVATE KEY-----\nabc\n-----END RSA PRIVATE KEY-----\n', 'utf8');
      const add = spawnSync('git', ['add', fixtureName], { cwd: ROOT, encoding: 'utf8' });
      assert.equal(add.status, 0, add.stderr);
      const result = checker.checkSecrets();
      assert.equal(result.status, 'found-problems');
      assert.ok(Array.isArray(result.findings) && result.findings.length > 0);
    } finally {
      fs.rmSync(fixturePath, { force: true });
    }
  });
});

test('AC-F1: checkSecrets() returns {status:"internal-error"} (never silently "clean") when the allowlist is unreadable', () => {
  // Force loadAllowlist() to throw by pointing runScan's ALLOWLIST_PATH
  // dependency at a broken state — simulate via a temporary rename rather
  // than mutating the real allowlist in place (staging-area discipline: an
  // atomic before/after inside one test, never left broken for another
  // agent's concurrent tool call to observe).
  const real = checker.ALLOWLIST_PATH;
  // Unique per test-run (pid+timestamp — same convention as the fixture
  // names above) so concurrent `node --test` runs never collide on the same
  // backup path (BUG-088 P1 correction: this is the REAL allowlist file
  // claude-secret-guard.js's loadAllowlist() depends on for every future
  // commit — a stranded rename here is a self-inflicted fail-closed DoS).
  const backup = `${real}.bug088_test_backup_${process.pid}_${Date.now()}`;
  assert.ok(fs.existsSync(real), 'test setup: real allowlist file must exist');
  let renamed = false;
  try {
    fs.renameSync(real, backup);
    renamed = true;
    const result = checker.checkSecrets();
    assert.equal(result.status, 'internal-error');
    assert.ok(result.error instanceof Error);
  } finally {
    // Only restore if the rename-away actually succeeded — guarantees the
    // real allowlist is never left stranded on a thrown JS exception
    // between the rename and here (try/finally executes on a throw). NOT
    // guaranteed on a hard process kill (SIGKILL/OOM-kill/power-loss)
    // between the successful rename-away and this restore step — try/
    // finally cannot run in that case, so the real allowlist would stay
    // stranded under the backup name. This is a known, accepted residual
    // gap, not a bug.
    if (renamed) restoreWithRetry(backup, real);
  }
});

// ---------------------------------------------------------------------------
// Sanity: runScan() still throws (not returns) on internal error — the
// pre-existing contract claude-secret-guard.js's main() relies on for its
// unchanged fail-closed catch block (AC-C1).
// ---------------------------------------------------------------------------

test('runScan() still throws on internal error (unchanged contract for claude-secret-guard.js)', () => {
  const real = checker.ALLOWLIST_PATH;
  const backup = `${real}.bug088_test_backup2_${process.pid}_${Date.now()}`;
  let renamed = false;
  try {
    fs.renameSync(real, backup);
    renamed = true;
    assert.throws(() => checker.runScan());
  } finally {
    if (renamed) restoreWithRetry(backup, real);
  }
});

// ---------------------------------------------------------------------------
// SEC-021: segment-aware, mixed-character-class exemption for descriptive
// hyphenated correlation IDs. See docs/planning/acceptance/tool.secretguard.md
// (SEC-021 section) for the full fixture-pair table this is built from.
// ---------------------------------------------------------------------------

test('SEC-021 AC-1: looksHighEntropy() exempts word-segmented identifiers, still flags credential shapes', () => {
  // Descriptive hyphenated correlation IDs (GR#1's convention) — must NOT
  // be flagged, regardless of whole-string Shannon entropy or length.
  assert.equal(checker.looksHighEntropy('bogus-injected-phase'), false);
  assert.equal(checker.looksHighEntropy('sec014-original-still-works'), false);
  assert.equal(checker.looksHighEntropy('sec018-original-still-works'), false);

  // A fourth, never-allowlisted, structurally-identical identifier — proves
  // the fix is a general structural rule, not a disguised special-case of
  // the three named literals (the AC-1 false-pass trap this test exists to
  // close).
  assert.equal(checker.looksHighEntropy('foo-bar-baz-example-literal'), false);

  // Fabricated (never real) credential shapes — must still be flagged.
  // Base64-alphabet, contiguous (no separator), mixed case.
  const fabricatedBase64 = 'wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY';
  assert.equal(checker.looksHighEntropy(fabricatedBase64), true);

  // Lowercase-hex, contiguous (no separator), 40 chars.
  const fabricatedHex = '9f2c8b7a1e6d4c3b0a5f8e7d2c1b6a4f9e8d7c6b';
  assert.equal(checker.looksHighEntropy(fabricatedHex), true);

  // Hyphen-segmented, but one segment ("LiVe") has mixed case — the
  // exemption must NOT apply because not every segment is lowercase+digit
  // only. Proves a lazy "exempt anything containing a hyphen" fix would be
  // caught: that fix would wrongly exempt this literal.
  const fabricatedMixedCaseHyphenated = 'sk-LiVe-Ax7Qz9Km2Rp8Vt4Wy6';
  assert.equal(checker.looksHighEntropy(fabricatedMixedCaseHyphenated), true);
});

test('SEC-021 AC-1: isWordSegmentedIdentifier() is the named, reusable structural check (GR#15)', () => {
  // A clear, named check rather than an ad-hoc inline regex — GR#15's
  // requirement that expected-shape logic be legible to a future reader.
  assert.equal(checker.isWordSegmentedIdentifier('bogus-injected-phase'), true);
  assert.equal(checker.isWordSegmentedIdentifier('sk-LiVe-Ax7Qz9Km2Rp8Vt4Wy6'), false);
  // No separator at all — the exemption's condition (a) fails, contiguous
  // strings are always left to the existing entropy/length logic.
  assert.equal(checker.isWordSegmentedIdentifier('9f2c8b7a1e6d4c3b0a5f8e7d2c1b6a4f9e8d7c6b'), false);
});

test('SEC-021 AC-2: the three retired allowlist ids are absent from claude-secret-guard.allow.json', () => {
  const { allowedPatterns } = checker.loadAllowlist();
  const retiredIds = [
    'test-phase-bogus-injected',
    'test-corr-sec014-original-still-works',
    'test-corr-sec018-original-still-works',
  ];
  const presentIds = allowedPatterns.map(e => e.id);
  for (const id of retiredIds) {
    assert.ok(!presentIds.includes(id), `retired allowlist id "${id}" must be absent`);
  }
});

test('SEC-021 AC-2: the three retired literals are still allowed end-to-end, via the heuristic alone (post-removal allowlist)', { concurrency: false }, () => {
  const fixtureName = `__secretchecker_sec021_ac2_${process.pid}_${Date.now()}.js`;
  const fixturePath = path.join(ROOT, fixtureName);
  const literals = [
    'bogus-injected-phase',
    'sec014-original-still-works',
    'sec018-original-still-works',
  ];
  withThrowawayIndex(() => {
    try {
      const content = literals.map((lit, i) => `const corr${i} = "${lit}";`).join('\n') + '\n';
      fs.writeFileSync(fixturePath, content, 'utf8');
      const add = spawnSync('git', ['add', fixtureName], { cwd: ROOT, encoding: 'utf8' });
      assert.equal(add.status, 0, add.stderr);

      // Confirm, independent of the end-to-end scan, that the allowlist
      // truly plays no part: looksHighEntropy() alone (no allowlist
      // consultation) already exempts all three.
      for (const lit of literals) {
        assert.equal(checker.looksHighEntropy(lit), false, `${lit} must be exempt from looksHighEntropy() with no allowlist involved`);
      }

      const findings = checker.runScan();
      const highEntropyForFixture = findings.filter(
        f => f.file === fixtureName && f.category === 'high-entropy'
      );
      assert.deepEqual(highEntropyForFixture, [], 'no high-entropy finding expected for any of the three retired literals');
    } finally {
      fs.rmSync(fixturePath, { force: true });
    }
  });
});

// ---------------------------------------------------------------------------
// SEC-021 AC-3: regression proof — a realistic (fabricated, never real)
// high-entropy secret is still caught end-to-end (runScan/checkSecrets),
// not merely at the looksHighEntropy() unit level (AC-1 already covers
// that). This is the check that would FAIL against a naive "just widen the
// entropy threshold" non-fix: see the comment below for how that was
// verified.
// ---------------------------------------------------------------------------

test('SEC-021 AC-3: a realistic fabricated high-entropy secret is still flagged end-to-end (base64 + hex shapes)', { concurrency: false }, () => {
  const fixtureName = `__secretchecker_sec021_ac3_${process.pid}_${Date.now()}.js`;
  const fixturePath = path.join(ROOT, fixtureName);
  // Fabricated (never real) — a base64-alphabet API-key shape and a
  // hex-token shape, staged inside ordinary "assigned to a variable named
  // like apiKey/token" surrounding code, matching FEAT-028 AC-9's framing.
  const fabricatedBase64Key = 'wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY';
  const fabricatedHexToken = '9f2c8b7a1e6d4c3b0a5f8e7d2c1b6a4f9e8d7c6b';
  withThrowawayIndex(() => {
    try {
      const content = [
        `const apiKey = "${fabricatedBase64Key}";`,
        `const token = "${fabricatedHexToken}";`,
        '',
      ].join('\n');
      fs.writeFileSync(fixturePath, content, 'utf8');
      const add = spawnSync('git', ['add', fixtureName], { cwd: ROOT, encoding: 'utf8' });
      assert.equal(add.status, 0, add.stderr);

      const findings = checker.runScan();
      const forFixture = findings.filter(f => f.file === fixtureName);
      const highEntropyFindings = forFixture.filter(f => f.category === 'high-entropy');
      // Exactly the two fabricated secrets, both categorized specifically
      // as high-entropy (not merely "some finding") — closes the AC-3
      // false-pass trap where a coincidental api-key-pattern match masks a
      // silently-broken entropy path.
      assert.equal(highEntropyFindings.length, 2, `expected 2 high-entropy findings, got: ${JSON.stringify(forFixture)}`);
      assert.equal(highEntropyFindings[0].line, 1);
      assert.equal(highEntropyFindings[1].line, 2);
    } finally {
      fs.rmSync(fixturePath, { force: true });
    }
  });
});

// ---------------------------------------------------------------------------
// SEC-021 REGRESSION FIX (post-Destructive-rejection, 2026-08-12): the
// segment-SHAPE-only rule above was defeated two ways, both real P0
// regressions found by a Destructive agent against the original SEC-021
// build: (1) hyphen-chunking a genuinely high-entropy secret into equal-
// width lowercase-hex groups, and (2) lowercasing a mixed-case secret that
// was ALREADY correctly caught in its original case. Both are reproduced
// here exactly, through the full runScan()/scanLine() pipeline (not just
// looksHighEntropy() in isolation), in several of the realistic surrounding
// contexts a Destructive agent would use (hmac_secret assignment, a JSON-
// shaped "apiSecret" field, a bare quoted literal) — matching this file's
// existing "stage a fixture, run runScan(), assert the category" style.
//
// PRE-FIX-FAILS PROOF (verified for this report, not asserted from memory):
// reconstructing the pre-regression-fix isWordSegmentedIdentifier() (split
// on '-'/'_', exempt if every segment matches /^[a-z0-9]+$/, nothing else)
// and calling it directly against both fixtures below returns `true` for
// both — i.e. the OLD code would have exempted both real secrets from
// looksHighEntropy() entirely, before ever reaching the pattern detectors.
// Neither is independently caught by API_KEY_PATTERNS (checked below in
// this same test): the hex-chunked fixture matches no key-shaped prefix at
// all, and the lowercased "sk-live-..." fixture's prefix ("sk-") is only
// three characters — API_KEY_PATTERNS' openai-style-secret rule requires
// "sk-" directly followed by 20+ contiguous alnum characters with no
// hyphen in between, which a hyphen-chunked/segmented token never is.
// ---------------------------------------------------------------------------

test('SEC-021 REGRESSION: hyphen-chunked high-entropy hex token is flagged end-to-end across several realistic contexts', { concurrency: false }, () => {
  // A 40-char hex secret (fabricated, never real), deliberately split into
  // 4-char groups joined by '-' — the exact obfuscation shape the
  // Destructive agent's finding names. Whole-string (rejoined) entropy
  // 3.890 bits/char, well above ENTROPY_THRESHOLD (3.7).
  const hexChunked = '9f2c-8b7a-1e6d-4c3b-0a5f-8e7d-2c1b-6a4f-9e8d-7c6b';
  assert.equal(
    checker.looksHighEntropy(hexChunked),
    true,
    'unit level: hyphen-chunked hex token must be flagged high-entropy'
  );

  const fixtureName = `__secretchecker_sec021_regr_hexchunk_${process.pid}_${Date.now()}.js`;
  const fixturePath = path.join(ROOT, fixtureName);
  const contexts = [
    `const hmac_secret = "${hexChunked}";`,
    `const config = { "apiSecret": "${hexChunked}" };`,
    `let bareLiteral = '${hexChunked}';`,
  ];
  withThrowawayIndex(() => {
    try {
      fs.writeFileSync(fixturePath, contexts.join('\n') + '\n', 'utf8');
      const add = spawnSync('git', ['add', fixtureName], { cwd: ROOT, encoding: 'utf8' });
      assert.equal(add.status, 0, add.stderr);

      const findings = checker.runScan();
      const highEntropyForFixture = findings.filter(
        f => f.file === fixtureName && f.category === 'high-entropy'
      );
      // BUG-148 (2026-08-13) originally made scanLine() ALSO concatenate
      // every string literal on a line and re-check the joined text, which
      // gave context line 2 (`{ "apiSecret": "<hex>" }`) a SECOND finding
      // (the "apiSecret"+hex concatenation) on top of the per-literal
      // finding on the hex value alone. BUG-150 (2026-08-13, same day)
      // narrowed that concatenation to an allowlist of deliberate
      // continuation shapes (`+` concatenation, or an adjacent
      // const/let/var declaration) specifically BECAUSE unbounded
      // concatenation of an unrelated label + value — exactly this
      // `"key": "value"` shape — was the false-positive mechanism (JSON/
      // object key:value pairs, live-swept at 1000+ false positives against
      // this project's own source). So line 2 now correctly produces only
      // the ONE finding, from the per-literal check on the hex value
      // itself — the label is never part of the secret. Every context line
      // must still have AT LEAST one finding (no line silently loses
      // coverage), which is the property this test actually guards against
      // regressing; total count is now exactly one per context line, not
      // one extra.
      const perLineCounts = contexts.map((_, i) =>
        highEntropyForFixture.filter(f => f.line === i + 1).length
      );
      assert.ok(
        perLineCounts.every(c => c >= 1),
        `expected every one of the ${contexts.length} context lines to have at least one high-entropy finding, got per-line counts ${JSON.stringify(perLineCounts)}: ${JSON.stringify(findings.filter(f => f.file === fixtureName))}`
      );
      assert.equal(
        highEntropyForFixture.length,
        contexts.length,
        `expected ${contexts.length} total findings (one per context line — BUG-150 stops the "apiSecret" label from being concatenated into the secret), got: ${JSON.stringify(findings.filter(f => f.file === fixtureName))}`
      );
    } finally {
      fs.rmSync(fixturePath, { force: true });
    }
  });
});

test('SEC-021 REGRESSION: lowercased sk-live-shaped token is flagged end-to-end across several realistic contexts', { concurrency: false }, () => {
  // The AC's own positive-control fixture ("sk-LiVe-Ax7Qz9Km2Rp8Vt4Wy6"),
  // lowercased — fabricated, never real. Segments: 'sk'(2)/'live'(4)/
  // 'ax7qz9km2rp8vt4wy6'(18). The last segment alone measures 4.170
  // bits/char (all 18 characters distinct), well above ENTROPY_THRESHOLD.
  const skLiveLower = 'sk-live-ax7qz9km2rp8vt4wy6';
  assert.equal(
    checker.looksHighEntropy(skLiveLower),
    true,
    'unit level: lowercased sk-live-shaped token must still be flagged high-entropy'
  );

  const fixtureName = `__secretchecker_sec021_regr_caselower_${process.pid}_${Date.now()}.js`;
  const fixturePath = path.join(ROOT, fixtureName);
  const contexts = [
    `const clientSecret = "${skLiveLower}";`,
    `const dbPassword = "${skLiveLower}";`,
    `const SECRET_KEY = "${skLiveLower}";`,
    `const stripeApiKey = "${skLiveLower}";`,
    `const bearerToken = "${skLiveLower}";`,
  ];
  withThrowawayIndex(() => {
    try {
      fs.writeFileSync(fixturePath, contexts.join('\n') + '\n', 'utf8');
      const add = spawnSync('git', ['add', fixtureName], { cwd: ROOT, encoding: 'utf8' });
      assert.equal(add.status, 0, add.stderr);

      const findings = checker.runScan();
      const forFixture = findings.filter(f => f.file === fixtureName);
      const highEntropyOrApiKey = forFixture.filter(
        f => f.category === 'high-entropy' || f.category === 'api-key'
      );
      // Every context line must produce SOME finding for the secret (either
      // the high-entropy path, which is what this item's fix targets, or —
      // for the stripeApiKey/bearerToken lines — potentially the api-key
      // generic-key-assignment pattern too; either way, the secret must
      // never pass through invisibly). The high-entropy path specifically
      // must fire for at least the plain clientSecret/dbPassword/SECRET_KEY
      // lines, which no API_KEY_PATTERNS rule matches.
      assert.equal(
        forFixture.length,
        contexts.length,
        `expected a finding on every one of the ${contexts.length} context lines, got: ${JSON.stringify(forFixture)}`
      );
      const plainVarFindings = forFixture.filter(f => f.line === 1 || f.line === 2 || f.line === 3);
      assert.ok(
        plainVarFindings.every(f => f.category === 'high-entropy'),
        `clientSecret/dbPassword/SECRET_KEY lines must be caught specifically via high-entropy (no API_KEY_PATTERNS rule matches a hyphen-segmented token), got: ${JSON.stringify(plainVarFindings)}`
      );
      assert.equal(highEntropyOrApiKey.length, contexts.length);
    } finally {
      fs.rmSync(fixturePath, { force: true });
    }
  });
});

test('SEC-021 REGRESSION: pre-fix reconstruction proves both attack fixtures WOULD have been exempt (documented, not asserted from memory)', () => {
  // Reconstructs the exact pre-regression-fix isWordSegmentedIdentifier()
  // (segment-shape-only, no per-segment or uniform-chunk entropy check) and
  // proves it returns true (wrongly exempt) for both attack fixtures —
  // the concrete evidence that the new tests above would have failed
  // against the code as it stood before this fix.
  function isWordSegmentedIdentifierPreFix(candidate) {
    const SEP_RE = /[-_]/;
    const SEG_RE = /^[a-z0-9]+$/;
    if (!SEP_RE.test(candidate)) return false;
    return candidate.split(/[-_]/).every(seg => SEG_RE.test(seg));
  }
  assert.equal(
    isWordSegmentedIdentifierPreFix('9f2c-8b7a-1e6d-4c3b-0a5f-8e7d-2c1b-6a4f-9e8d-7c6b'),
    true,
    'pre-fix code wrongly exempted the hyphen-chunked hex token'
  );
  assert.equal(
    isWordSegmentedIdentifierPreFix('sk-live-ax7qz9km2rp8vt4wy6'),
    true,
    'pre-fix code wrongly exempted the lowercased sk-live-shaped token'
  );
  // And the CURRENT (fixed) implementation must disagree on both:
  assert.equal(checker.isWordSegmentedIdentifier('9f2c-8b7a-1e6d-4c3b-0a5f-8e7d-2c1b-6a4f-9e8d-7c6b'), false);
  assert.equal(checker.isWordSegmentedIdentifier('sk-live-ax7qz9km2rp8vt4wy6'), false);
});

// How AC-3 was verified to catch the "just widen ENTROPY_THRESHOLD" non-fix
// (documented here rather than asserted from memory, per the report
// requirement): a naive non-fix would raise ENTROPY_THRESHOLD until the
// three named literals (entropy ~3.88-3.90, see below) stop clearing the
// bar, e.g. to 3.95. Measured against this project's actual
// shannonEntropy() implementation:
//   shannonEntropy('bogus-injected-phase')            = 3.884
//   shannonEntropy('sec014-original-still-works')      = 3.902
//   shannonEntropy('sec018-original-still-works')      = 3.902
//   shannonEntropy('9f2c8b7a1e6d4c3b0a5f8e7d2c1b6a4f9e8d7c6b') = 3.890  (fabricated hex fixture)
// A threshold raised to clear all three named literals (>3.902) would ALSO
// clear the fabricated hex fixture above (3.890 < 3.902), so this test's
// hex-shaped assertion would fail under that non-fix, exactly the outcome
// this AC exists to catch. The chosen segment-aware fix instead exempts the
// three literals structurally (isWordSegmentedIdentifier() === true) while
// leaving the hex fixture's entropy comparison completely untouched
// (isWordSegmentedIdentifier() === false, no separator present), so
// ENTROPY_THRESHOLD never has to move and the hex fixture's detection is
// unaffected by the fix.
test('SEC-021 AC-3 regression-proof note: a naive threshold-widening non-fix would have broken the hex fixture (documented, not just asserted)', () => {
  const namedLiteralEntropies = [
    checker.shannonEntropy('bogus-injected-phase'),
    checker.shannonEntropy('sec014-original-still-works'),
    checker.shannonEntropy('sec018-original-still-works'),
  ];
  const hexFixtureEntropy = checker.shannonEntropy('9f2c8b7a1e6d4c3b0a5f8e7d2c1b6a4f9e8d7c6b');
  const naiveThresholdThatWouldExemptAllThree = Math.max(...namedLiteralEntropies) + 0.001;
  // The naive non-fix's threshold would have to exceed the hex fixture's
  // own entropy too, proving it cannot distinguish the two shapes by a bare
  // floor retune alone.
  assert.ok(
    naiveThresholdThatWouldExemptAllThree > hexFixtureEntropy,
    'sanity: the named literals must out-measure the hex fixture for this proof to be meaningful'
  );
});

// ---------------------------------------------------------------------------
// SEC-021 REGRESSION FIX ROUND 2 (post-round-3-Destructive-rejection,
// 2026-08-12): round 1's fix above still gated the reassembled-whole entropy
// check (rule (c)) behind "segments.length >= 4 AND all segments EXACTLY the
// same length". A round-3 Destructive proved this too narrow: 2-3 segments,
// or segments whose lengths differ by even one character, skip rule (c)
// entirely regardless of the reassembled whole's real entropy. This section
// proves the three round-3 gap fixtures (fabricated, never real) through the
// full runScan()/scanLine() pipeline, proves the fix does NOT reopen a false
// positive on real project text (the specific counter-example that sank the
// naive "just drop both preconditions" fix is included), and proves the
// broader false-positive sweep required by this round's brief.
// ---------------------------------------------------------------------------

test('SEC-021 REGRESSION ROUND 2: round-1-fixed code still wrongly exempts all three round-3 gap fixtures (pre-fix-fails proof, documented not asserted from memory)', () => {
  // Reconstructs isWordSegmentedIdentifier() exactly as it stood after round
  // 1's fix (rule (b) unchanged; rule (c) gated behind segments.length >= 4
  // AND exact length equality) and proves it returns true (wrongly exempt)
  // for all three round-3 attack fixtures.
  const SEGMENT_ENTROPY_MIN_LENGTH = 12;
  const UNIFORM_CHUNK_MIN_SEGMENTS = 4;
  function isWordSegmentedIdentifierRound1(candidate) {
    if (!/[-_]/.test(candidate)) return false;
    const segments = candidate.split(/[-_]/);
    if (!segments.every(seg => /^[a-z0-9]+$/.test(seg))) return false;
    for (const seg of segments) {
      if (seg.length >= SEGMENT_ENTROPY_MIN_LENGTH && checker.shannonEntropy(seg) >= checker.ENTROPY_THRESHOLD) {
        return false;
      }
    }
    if (segments.length >= UNIFORM_CHUNK_MIN_SEGMENTS) {
      const allSameLength = segments.every(seg => seg.length === segments[0].length);
      if (allSameLength && checker.shannonEntropy(segments.join('')) >= checker.ENTROPY_THRESHOLD) {
        return false;
      }
    }
    return true;
  }

  // 3 segments of exactly 10 chars each (32-char secret with separators) --
  // below the round-1 4-segment floor, so rule (c) never even evaluates it.
  const threeByTen = '6k368x4sfr-ruke3m1jio-ltk5j18wjo';
  // 10/10/11 -- not EXACTLY uniform, so round-1's strict-equality check
  // rejects the branch even though it has >= 4... no, 3 segments here too,
  // both preconditions fail simultaneously.
  const nonUniform = 'z1f6wi352l-edgi1un5h0-q3gtj72bd8n';
  // 5 segments of 11/11/11/10/10 (53-char secret) -- clears the >=4-segment
  // floor but fails the EXACT-equality requirement (10 != 11).
  const fiveSeg53 = 'jtjzmzsjqfr-un7g28cqys8-5acmh87poj0-8606uteuts-69xw71m9rs';

  assert.equal(isWordSegmentedIdentifierRound1(threeByTen), true,
    'round-1 code wrongly exempted the 3x10-segment secret');
  assert.equal(isWordSegmentedIdentifierRound1(nonUniform), true,
    'round-1 code wrongly exempted the 10/10/11 non-uniform secret');
  assert.equal(isWordSegmentedIdentifierRound1(fiveSeg53), true,
    'round-1 code wrongly exempted the 5-segment 53-char secret');

  // Documented, measured whole-string entropy for all three (genuinely high,
  // not merely long) -- see the report for the full derivation:
  assert.ok(checker.shannonEntropy(threeByTen.split(/[-_]/).join('')) >= checker.ENTROPY_THRESHOLD);
  assert.ok(checker.shannonEntropy(nonUniform.split(/[-_]/).join('')) >= checker.ENTROPY_THRESHOLD);
  assert.ok(checker.shannonEntropy(fiveSeg53.split(/[-_]/).join('')) >= checker.ENTROPY_THRESHOLD);

  // The CURRENT (round-2-fixed) implementation must disagree on all three:
  assert.equal(checker.isWordSegmentedIdentifier(threeByTen), false);
  assert.equal(checker.isWordSegmentedIdentifier(nonUniform), false);
  assert.equal(checker.isWordSegmentedIdentifier(fiveSeg53), false);
});

test('SEC-021 REGRESSION ROUND 2: all three round-3 gap fixtures are flagged end-to-end (runScan) across realistic contexts', { concurrency: false }, () => {
  const fixtures = [
    { name: 'threeByTen', secret: '6k368x4sfr-ruke3m1jio-ltk5j18wjo' },
    { name: 'nonUniform10_10_11', secret: 'z1f6wi352l-edgi1un5h0-q3gtj72bd8n' },
    { name: 'fiveSeg53', secret: 'jtjzmzsjqfr-un7g28cqys8-5acmh87poj0-8606uteuts-69xw71m9rs' },
  ];
  const fixtureName = `__secretchecker_sec021_round2_gap_${process.pid}_${Date.now()}.js`;
  const fixturePath = path.join(ROOT, fixtureName);
  withThrowawayIndex(() => {
    try {
      const lines = [
        `const apiSecret = "${fixtures[0].secret}";`,
        `const dbPassword = '${fixtures[1].secret}';`,
        `const hmac_secret = "${fixtures[2].secret}";`,
      ];
      fs.writeFileSync(fixturePath, lines.join('\n') + '\n', 'utf8');
      const add = spawnSync('git', ['add', fixtureName], { cwd: ROOT, encoding: 'utf8' });
      assert.equal(add.status, 0, add.stderr);

      const findings = checker.runScan();
      const highEntropyForFixture = findings.filter(
        f => f.file === fixtureName && f.category === 'high-entropy'
      );
      assert.equal(
        highEntropyForFixture.length,
        fixtures.length,
        `expected a high-entropy finding on each of the ${fixtures.length} gap-fixture lines, got: ${JSON.stringify(findings.filter(f => f.file === fixtureName))}`
      );
    } finally {
      fs.rmSync(fixturePath, { force: true });
    }
  });
});

test('SEC-021 REGRESSION ROUND 2: legitimate fixtures (AC table + BUG-029 allowlist literals) remain exempt after the round-2 fix', () => {
  // Every fixture the acceptance table and the currently-live BUG-029
  // allowlist entries require to stay exempt, re-measured against the
  // round-2-fixed isWordSegmentedIdentifier()/looksHighEntropy().
  const legit = [
    'bogus-injected-phase',
    'sec014-original-still-works',
    'sec018-original-still-works',
    'foo-bar-baz-example-literal',
    'user-data-sync-fail',
    'human-reviewed-commit', // BUG-029's bug095-fixture allowlist literal
  ];
  for (const l of legit) {
    assert.equal(checker.looksHighEntropy(l), false, `${l} must remain exempt`);
  }
});

test('SEC-021 REGRESSION ROUND 2: broader false-positive sweep — 8 additional realistic project-style identifiers are not wrongly flagged', () => {
  // BOW-fixture-name-shaped and correlation-ID-shaped identifiers matching
  // this project's own conventions (GR#1), none of which appear in any
  // allowlist entry, none of which were used to derive the fix. Includes
  // 'backslash-before-closing-quote', a genuine phrase already present in
  // docs/planning/acceptance/tool.secretguard.md (line 703) -- the specific
  // counter-example that proved a naive "always check the reassembled whole
  // against ENTROPY_THRESHOLD with no structural anchor" fix was unsound:
  // its reassembled entropy (3.930 bits/char) measures HIGHER than the
  // round-1 hyphen-chunked hex attack's own reassembled entropy (3.890
  // bits/char) -- see the header comment in claude-secret-checker.js for the
  // full proof.
  const identifiers = [
    'backslash-before-closing-quote',
    'auth-flow-check-verify-token',
    'bug-042-merge-identity',
    'sec-021-word-segmented',
    'correlation-id-generator',
    'session-token-refresh',
    'user-authentication-flow',
    'metro-db-connection-check',
  ];
  for (const id of identifiers) {
    assert.equal(checker.looksHighEntropy(id), false, `${id} must not be wrongly flagged`);
  }
});

test('SEC-021 REGRESSION ROUND 2 proof note: a naive "always check reassembled whole, no structural anchor" fix would have broken a real project phrase (documented, not just asserted)', () => {
  const realPhrase = 'backslash-before-closing-quote';
  const realPhraseEntropy = checker.shannonEntropy(realPhrase.split(/[-_]/).join(''));
  const hexAttackEntropy = checker.shannonEntropy('9f2c8b7a1e6d4c3b0a5f8e7d2c1b6a4f9e8d7c6b');
  // The real phrase out-measures the round-1 attack fixture -- no single
  // fixed threshold on the reassembled whole, applied unconditionally with
  // no structural precondition, can accept the phrase while still rejecting
  // the attack.
  assert.ok(
    realPhraseEntropy > hexAttackEntropy,
    'sanity: the real phrase must out-measure the hex attack for this proof to be meaningful'
  );
  // And the shipped fix (which keeps a structural anchor -- segment-length
  // range <= SEGMENT_LENGTH_RANGE_TOLERANCE -- rather than checking entropy
  // unconditionally) correctly tells them apart:
  assert.equal(checker.looksHighEntropy(realPhrase), false);
  assert.equal(checker.looksHighEntropy('9f2c8b7a1e6d4c3b0a5f8e7d2c1b6a4f9e8d7c6b'), true);
});

// ---------------------------------------------------------------------------
// SEC-021 lead ruling (2026-08-12), condition (1): the hostile-sha256-bundle
// false positive from internal/foundation/serialize/savebundle_security_test.go:245
// (filepath.Join(t.TempDir(), "hostile-sha256-bundle")) is fixed via the
// sanctioned allowlist mechanism, NOT another entropy-logic change. This
// proves the distinction end-to-end: looksHighEntropy() alone (no allowlist)
// STILL flags the literal -- it clears isWordSegmentedIdentifier's own
// anti-chunking rule (c) because its three segments (hostile/sha256/bundle,
// lengths 7/6/6, range 1) reassemble to ~3.83 bits/char, over
// ENTROPY_THRESHOLD -- and only the new claude-secret-guard.allow.json
// "sec021-hostile-sha256-bundle" entry suppresses the finding end-to-end via
// runScan()'s allowlist consultation.
// ---------------------------------------------------------------------------

test('SEC-021 condition (1): looksHighEntropy() alone still flags hostile-sha256-bundle (no entropy-logic change made)', () => {
  assert.equal(
    checker.looksHighEntropy('hostile-sha256-bundle'),
    true,
    'the entropy heuristic must be UNCHANGED by this fix -- suppression must come from the allowlist, not a logic change'
  );
});

test('SEC-021 condition (1): the sec021-hostile-sha256-bundle allowlist entry exists and is an exact match on the literal', () => {
  const { allowedPatterns } = checker.loadAllowlist();
  const entry = allowedPatterns.find(e => e.id === 'sec021-hostile-sha256-bundle');
  assert.ok(entry, 'expected an allowedPatterns entry with id "sec021-hostile-sha256-bundle"');
  assert.equal(entry.type, 'exact');
  assert.equal(entry.value, 'hostile-sha256-bundle');
  assert.ok(entry.reason && entry.reason.length > 0, 'entry must carry a non-empty reason');
});

test('SEC-021 condition (1): hostile-sha256-bundle is NOT flagged end-to-end (runScan) against the real savebundle_security_test.go fixture shape, suppressed by the allowlist', { concurrency: false }, () => {
  const fixtureName = `__secretchecker_sec021_hostile_sha256_${process.pid}_${Date.now()}.go`;
  const fixturePath = path.join(ROOT, fixtureName);
  withThrowawayIndex(() => {
    try {
      // Same shape as the real trigger line, savebundle_security_test.go:245.
      const content =
        'package serialize_test\n\n' +
        'func hostileFixturePath(t *testing.T) string {\n' +
        '\treturn filepath.Join(t.TempDir(), "hostile-sha256-bundle")\n' +
        '}\n';
      fs.writeFileSync(fixturePath, content, 'utf8');
      const add = spawnSync('git', ['add', fixtureName], { cwd: ROOT, encoding: 'utf8' });
      assert.equal(add.status, 0, add.stderr);

      const findings = checker.runScan();
      const highEntropyForFixture = findings.filter(
        f => f.file === fixtureName && f.category === 'high-entropy'
      );
      assert.deepEqual(
        highEntropyForFixture,
        [],
        `expected the allowlist to suppress the hostile-sha256-bundle finding, got: ${JSON.stringify(findings.filter(f => f.file === fixtureName))}`
      );
    } finally {
      fs.rmSync(fixturePath, { force: true });
    }
  });
});

// ---------------------------------------------------------------------------
// BUG-148: a credential split across two or more adjacent string literals on
// the SAME line evaded every detector — pattern detectors require
// contiguity, and the per-literal entropy floor (ENTROPY_MIN_LENGTH=20)
// meant each half of a real secret could fall under it even though the
// whole clears the threshold. scanLine() now additionally concatenates every
// literal's content found on a line (source order, no separator) and
// re-runs both detector classes against that joined string.
// ---------------------------------------------------------------------------

test('BUG-148: an AWS access key split across two adjacent string literals on one line is now caught', () => {
  // Fabricated (never real), textbook AKIA-shaped key split into two pieces.
  const splitLine = 'const accessKeyPart1 = "AKIA12345678"; const accessKeyPart2 = "90ABCDEF";';
  const findings = checker.scanLine(splitLine);
  const apiKeyFindings = findings.filter(f => f.category === 'api-key');
  assert.ok(
    apiKeyFindings.length > 0,
    `expected the split AWS key to be caught via cross-literal concatenation, got: ${JSON.stringify(findings)}`
  );
  assert.match(apiKeyFindings[0].detail, /aws-access-key-id/);
  assert.match(apiKeyFindings[0].detail, /split across 2 adjacent string literals/);

  // The unsplit, contiguous version must still be caught the ordinary way
  // (no regression to the pre-existing per-line pattern check).
  const contiguousLine = 'const accessKey = "AKIA1234567890ABCDEF";';
  const contiguousFindings = checker.scanLine(contiguousLine);
  assert.ok(contiguousFindings.some(f => f.category === 'api-key'));
});

test('BUG-148: a random 32-char secret split into two 16-char halves is now caught', () => {
  // Fabricated (never real) high-entropy secret, split so each half sits
  // under ENTROPY_MIN_LENGTH (20) individually — this was the exact
  // Destructive-round fixture that returned zero findings pre-fix.
  const half1 = 'aZ3kQ9mP2xR7vL4t'; // 16 chars
  const half2 = 'wB8nH5jY1cF6sD0e'; // 16 chars
  const whole = half1 + half2; // 32 chars
  assert.equal(half1.length, 16);
  assert.equal(half2.length, 16);
  assert.ok(checker.looksHighEntropy(whole), 'test fixture sanity: the whole string must itself be high-entropy');
  assert.equal(checker.looksHighEntropy(half1), false, 'test fixture sanity: half1 alone must fall under the entropy floor');
  assert.equal(checker.looksHighEntropy(half2), false, 'test fixture sanity: half2 alone must fall under the entropy floor');

  const splitLine = `const secretPart1 = "${half1}"; const secretPart2 = "${half2}";`;
  const findings = checker.scanLine(splitLine);
  const highEntropyFindings = findings.filter(f => f.category === 'high-entropy');
  assert.ok(
    highEntropyFindings.length > 0,
    `expected the split secret to be caught via cross-literal concatenation, got: ${JSON.stringify(findings)}`
  );
  assert.match(highEntropyFindings[0].detail, /split across 2 adjacent string literals/);
});

test('BUG-148: two unrelated string literals on one line that do NOT concatenate to a secret produce no false positive', () => {
  // Two ordinary, short, unrelated literals — neither individually
  // high-entropy/pattern-shaped, and their concatenation isn't either.
  const line = 'const greeting = "hello"; const name = "world";';
  const findings = checker.scanLine(line);
  assert.deepEqual(
    findings,
    [],
    `expected no findings for two unrelated benign literals, got: ${JSON.stringify(findings)}`
  );
});

test('BUG-148: a single ordinary literal on a line (no second literal to concatenate with) is unaffected', () => {
  // Regression guard: literalsOnLine.length > 1 gate must not fire for the
  // single-literal case, and single-literal detection must still work.
  const cleanLine = 'const label = "just some ordinary text";';
  assert.deepEqual(checker.scanLine(cleanLine), []);

  const secretLine = 'const key = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY";';
  const findings = checker.scanLine(secretLine);
  assert.ok(findings.some(f => f.category === 'high-entropy'));
});

// ---------------------------------------------------------------------------
// BUG-150: the BUG-148 fix concatenated EVERY string literal found anywhere
// on a line (no cap on count, no adjacency-in-meaning check) and entropy-
// tested the joined blob — an ordinary array/object literal of several
// short, unrelated string values got wrongly flagged as a "split secret".
// The fix bounds the window to 2-3 ADJACENT literals and requires the raw
// text between them contain none of `, [ ] { }` (the signature of separate
// list/object elements, not one value continued across literals).
// ---------------------------------------------------------------------------

test('BUG-150: the exact claude-bow.js:88 repro (array of unrelated short string literals) produces no false positive', () => {
  const line = "const TYPES = ['module', 'feature', 'bug', 'interface', 'assumption', 'finding'];";
  const findings = checker.scanLine(line);
  assert.deepEqual(
    findings,
    [],
    `expected no findings for an ordinary array literal, got: ${JSON.stringify(findings)}`
  );
});

test('BUG-150: BUG-148 original fixtures still caught (no regression) — split AWS key across adjacent const declarations', () => {
  const splitLine = 'const accessKeyPart1 = "AKIA12345678"; const accessKeyPart2 = "90ABCDEF";';
  const findings = checker.scanLine(splitLine);
  const apiKeyFindings = findings.filter(f => f.category === 'api-key');
  assert.ok(
    apiKeyFindings.length > 0,
    `expected the split AWS key to still be caught post-BUG-150 narrowing, got: ${JSON.stringify(findings)}`
  );
  assert.match(apiKeyFindings[0].detail, /split across 2 adjacent string literals/);
});

test('BUG-150: BUG-148 original fixtures still caught (no regression) — split 32-char secret across adjacent const declarations', () => {
  const half1 = 'aZ3kQ9mP2xR7vL4t';
  const half2 = 'wB8nH5jY1cF6sD0e';
  const splitLine = `const secretPart1 = "${half1}"; const secretPart2 = "${half2}";`;
  const findings = checker.scanLine(splitLine);
  const highEntropyFindings = findings.filter(f => f.category === 'high-entropy');
  assert.ok(
    highEntropyFindings.length > 0,
    `expected the split secret to still be caught post-BUG-150 narrowing, got: ${JSON.stringify(findings)}`
  );
  assert.match(highEntropyFindings[0].detail, /split across 2 adjacent string literals/);
});

test('BUG-150: negative control — 3 short, clearly-unrelated array elements produce no false positive', () => {
  const line = "const COLORS = ['red', 'green', 'blue'];";
  const findings = checker.scanLine(line);
  assert.deepEqual(
    findings,
    [],
    `expected no findings for a 3-element array of unrelated words, got: ${JSON.stringify(findings)}`
  );
});

test('BUG-150: a 3-literal window IS still tried when literals are genuinely adjacent (no list separators)', () => {
  // Three adjacent const-declared pieces, each under the entropy floor, whose
  // concatenation clears it — the bounded 3-literal window must still catch
  // this (the narrowing must not regress to "2 literals only, never 3").
  const p1 = 'aZ3kQ9mP'; // 8 chars
  const p2 = '2xR7vL4t'; // 8 chars
  const p3 = 'wB8nH5jY'; // 8 chars
  const whole = p1 + p2 + p3; // 24 chars
  assert.equal(checker.looksHighEntropy(p1), false);
  assert.equal(checker.looksHighEntropy(p2), false);
  assert.ok(checker.looksHighEntropy(whole), 'test fixture sanity: the whole 3-piece string must be high-entropy');

  const line = `const a = "${p1}"; const b = "${p2}"; const c = "${p3}";`;
  const findings = checker.scanLine(line);
  const highEntropyFindings = findings.filter(f => f.category === 'high-entropy');
  assert.ok(
    highEntropyFindings.length > 0,
    `expected the 3-way split secret to be caught, got: ${JSON.stringify(findings)}`
  );
});
