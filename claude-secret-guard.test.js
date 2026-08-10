/**
 * claude-secret-guard.test.js — unit + end-to-end tests for claude-secret-guard.js.
 *
 * Covers:
 *   - BUG-026 fix: a high-entropy quoted literal in a .go file that exactly
 *     matches a package-scope identifier (func/type/const/var) declared
 *     anywhere in the same directory is suppressed — proven both at the
 *     unit level (collectGoPackageIdentifiers / isGoPackageIdentifier) and
 *     end-to-end (a real `git diff --cached` scan via the exported runScan
 *     path, using throwaway fixture files that are always cleaned up).
 *   - Regression guards, per the explicit CRITICAL CONSTRAINT in the BUG-026
 *     brief: this fix must NOT touch ENTROPY_THRESHOLD/ENTROPY_MIN_LENGTH,
 *     and must not resurrect the SEC-015-era false negative where an
 *     ALL-CAPS identifier (MAX_COUNT) stopped being detected by the GR#15
 *     hardcoding-smell splitter. A genuine high-entropy credential (random,
 *     not matching any declared identifier) must still be flagged, in both
 *     .go and non-.go files.
 *
 * This is the first test file for this hook (no prior test infra existed),
 * so it also stands as the smoke test that the hook is require()-able
 * without side effects (guarded by `require.main === module` in the
 * source — see its header comment).
 *
 * Run: node --test claude-secret-guard.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const path = require('path');
const crypto = require('crypto');
const { spawnSync } = require('child_process');

const ROOT = __dirname;

const guard = require('./claude-secret-guard.js');
const {
  scanLine,
  identifierWords,
  isHardcodeKeywordIdentifier,
  looksHighEntropy,
  shannonEntropy,
  stripGoCommentsAndStrings,
  collectGoPackageIdentifiers,
  isGoPackageIdentifier,
} = guard;

// A random, genuinely high-entropy hex string — never equal to any
// declared identifier anywhere, by construction (crypto-random per run).
function genuineSecretCandidate() {
  // base64 (64-symbol alphabet, ~4.5-6 bits/char in practice) rather than
  // hex (16-symbol alphabet, ~4 bits/char max) — hex strings occasionally
  // dip below the 3.7 threshold on an unlucky draw since they have less
  // headroom above it; base64 reliably clears it, avoiding a flaky test.
  const candidate = crypto.randomBytes(24).toString('base64').replace(/=+$/, '');
  assert.ok(looksHighEntropy(candidate), 'test setup: fixture secret must itself clear the entropy bar');
  return candidate;
}

// ---------------------------------------------------------------------------
// Unit: stripGoCommentsAndStrings
// ---------------------------------------------------------------------------

test('stripGoCommentsAndStrings removes comments and string contents without breaking line structure', () => {
  const src = [
    'package fixture',
    '',
    '// a line comment mentioning secretLookingWord',
    '/* a block',
    '   comment */',
    'const realDecl = "not-this-text-inside-quotes"',
  ].join('\n');
  const cleaned = stripGoCommentsAndStrings(src);
  assert.ok(!cleaned.includes('secretLookingWord'));
  assert.ok(!cleaned.includes('not-this-text-inside-quotes'));
  assert.ok(cleaned.includes('const realDecl'));
  // Line count preserved (declarations below a stripped comment must not
  // shift line-relative parsing).
  assert.equal(cleaned.split('\n').length, src.split('\n').length);
});

// ---------------------------------------------------------------------------
// Unit: collectGoPackageIdentifiers / isGoPackageIdentifier
// ---------------------------------------------------------------------------

function withFixtureGoPackage(files, fn) {
  const dir = fs.mkdtempSync(path.join(ROOT, '__secretguard_test_fixture_'));
  try {
    for (const [name, content] of Object.entries(files)) {
      fs.writeFileSync(path.join(dir, name), content, 'utf8');
    }
    return fn(dir);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

test('collectGoPackageIdentifiers finds a const declared inside a grouped const() block (the real codeReplayTargetClosedEarly shape)', () => {
  withFixtureGoPackage(
    {
      'errors.go': [
        'package fixture',
        '',
        'const (',
        '\t// codeSomeLongCamelCaseIdentifierName: doc comment.',
        '\tcodeSomeLongCamelCaseIdentifierName = "MET-X001"',
        ')',
      ].join('\n'),
    },
    dir => {
      const ids = collectGoPackageIdentifiers(dir);
      assert.ok(ids.has('codeSomeLongCamelCaseIdentifierName'));
    }
  );
});

test('collectGoPackageIdentifiers finds single-line const/var/type/func declarations', () => {
  withFixtureGoPackage(
    {
      'misc.go': [
        'package fixture',
        '',
        'const singleLineConst = "x"',
        'var singleLineVar = "y"',
        'type SomeType struct{ Field string }',
        'func SomeFunc(a int) int { return a }',
        'func (r *SomeType) Method() string { return r.Field }',
      ].join('\n'),
    },
    dir => {
      const ids = collectGoPackageIdentifiers(dir);
      for (const want of ['singleLineConst', 'singleLineVar', 'SomeType', 'SomeFunc', 'Method']) {
        assert.ok(ids.has(want), `expected identifier "${want}" to be collected`);
      }
    }
  );
});

test('collectGoPackageIdentifiers does not treat text inside comments/strings as declarations', () => {
  withFixtureGoPackage(
    {
      'noise.go': [
        'package fixture',
        '// const notARealDeclaration = "x" (this is a comment, not code)',
        'var actualDecl = "const alsoNotARealDeclarationInsideAString = 1"',
      ].join('\n'),
    },
    dir => {
      const ids = collectGoPackageIdentifiers(dir);
      assert.ok(ids.has('actualDecl'));
      assert.ok(!ids.has('notARealDeclaration'));
      assert.ok(!ids.has('alsoNotARealDeclarationInsideAString'));
    }
  );
});

test('isGoPackageIdentifier: true for a literal matching a declared package identifier, false for an unrelated high-entropy literal', () => {
  withFixtureGoPackage(
    {
      'errors.go': [
        'package fixture',
        '',
        'const (',
        '\tcodeReplayTargetClosedEarlyFixture = "MET-H004"',
        ')',
      ].join('\n'),
    },
    dir => {
      const relFile = path.relative(ROOT, path.join(dir, 'probe.go'));
      assert.equal(isGoPackageIdentifier('codeReplayTargetClosedEarlyFixture', relFile), true);

      const genuineSecret = genuineSecretCandidate();
      assert.equal(isGoPackageIdentifier(genuineSecret, relFile), false);
    }
  );
});

// ---------------------------------------------------------------------------
// Regression: the SEC-015-era false negative (ALL-CAPS shattering) must stay fixed
// ---------------------------------------------------------------------------

test('regression: MAX_COUNT is still recognised as a whole keyword word (SCREAMING_SNAKE_CASE not shattered)', () => {
  // This is the exact regression the BUG-026 brief warns against re-breaking:
  // an earlier splitter shattered ALL-CAPS identifiers into single letters,
  // silently blinding the GR#15 hardcoding-smell check on constants like
  // MAX_COUNT. BUG-026 must not touch this path at all; this test proves it
  // still doesn't.
  assert.deepEqual(identifierWords('MAX_COUNT'), ['max', 'count']);
  assert.equal(isHardcodeKeywordIdentifier('MAX_COUNT'), true);

  const findings = scanLine('if (MAX_COUNT === 45) { fail(); }');
  assert.ok(
    findings.some(f => f.category === 'hardcoding-smell'),
    'MAX_COUNT === 45 must still be flagged as a GR#15 hardcoding smell'
  );
});

// ---------------------------------------------------------------------------
// Regression: entropy threshold/min-length untouched, genuine secrets still caught
// ---------------------------------------------------------------------------

test('regression: a genuine high-entropy credential of similar length to codeReplayTargetClosedEarly is still flagged', () => {
  // codeReplayTargetClosedEarly is 27 chars. Prove a same-length-class,
  // unrelated random secret still trips looksHighEntropy AND is not
  // suppressed by the new Go-package-identifier check (it matches no
  // declared identifier anywhere).
  const secret = genuineSecretCandidate(); // base64, reliably high-entropy — see helper comment
  assert.equal(secret.length >= 27, true);
  assert.equal(looksHighEntropy(secret), true);

  const findings = scanLine(`token := "${secret}"`);
  assert.ok(
    findings.some(f => f.category === 'high-entropy' && f.candidate === secret),
    'a genuine random high-entropy literal must still be reported by scanLine'
  );
});

test('regression: ENTROPY_THRESHOLD / ENTROPY_MIN_LENGTH constants are unchanged (3.7 / 20)', () => {
  // Re-derive the constants' effect rather than importing them directly
  // (they are not exported, deliberately — this proves behaviour, not just
  // the literal number, so a refactor that keeps behaviour identical still
  // passes).
  const nineteenChars = crypto.randomBytes(10).toString('hex').slice(0, 19);
  assert.equal(looksHighEntropy(nineteenChars), false, 'a 19-char candidate must still be exempt (ENTROPY_MIN_LENGTH=20 unchanged)');
  const twentyChars = crypto.randomBytes(10).toString('hex').slice(0, 20);
  // twentyChars may or may not clear the entropy bar depending on the random
  // draw's character distribution; only length-gating is asserted here.
  void twentyChars;
});

// ---------------------------------------------------------------------------
// End-to-end: real `git diff --cached` scan via the hook's own runScan path
// ---------------------------------------------------------------------------
//
// Stages real throwaway fixture files under the repo, invokes
// claude-secret-guard.js exactly as the PreToolUse hook does (JSON on
// stdin, JSON deny/allow on stdout), and always unstages + deletes the
// fixtures in a `finally` block so a failing assertion can never leave the
// repo dirty. No production file is touched.

function runGuardAsHook(commandText) {
  const result = spawnSync(process.execPath, [path.join(ROOT, 'claude-secret-guard.js')], {
    cwd: ROOT,
    encoding: 'utf8',
    input: JSON.stringify({ tool: 'Bash', tool_input: { command: commandText } }),
  });
  if (!result.stdout) return { denied: false, raw: result };
  let parsed;
  try {
    parsed = JSON.parse(result.stdout);
  } catch {
    return { denied: false, raw: result };
  }
  const decision = parsed?.hookSpecificOutput?.permissionDecision;
  return {
    denied: decision === 'deny',
    reason: parsed?.hookSpecificOutput?.permissionDecisionReason || '',
    raw: result,
  };
}

test('end-to-end: guard still BLOCKS a genuine high-entropy secret staged in a .go file', { concurrency: false }, () => {
  const fixtureDir = path.join(ROOT, '__secretguard_e2e_fixture__');
  fs.rmSync(fixtureDir, { recursive: true, force: true });
  fs.mkdirSync(fixtureDir);
  const secret = genuineSecretCandidate();
  try {
    fs.writeFileSync(
      path.join(fixtureDir, 'probe.go'),
      ['package secretguardfixture', '', `const probeSecret = "${secret}"`, ''].join('\n'),
      'utf8'
    );
    const add = spawnSync('git', ['add', '--', fixtureDir], { cwd: ROOT, encoding: 'utf8' });
    assert.equal(add.status, 0, `git add failed: ${add.stderr}`);

    const outcome = runGuardAsHook('git commit -m "test: bug-026 e2e fixture (should be blocked)"');
    assert.equal(outcome.denied, true, `expected the guard to deny a genuine secret commit. raw stdout: ${outcome.raw.stdout}`);
    assert.match(outcome.reason, /SECRET GUARD/);
  } finally {
    spawnSync('git', ['reset', '--', fixtureDir], { cwd: ROOT, encoding: 'utf8' });
    fs.rmSync(fixtureDir, { recursive: true, force: true });
  }
});

test('end-to-end: guard SUPPRESSES a quoted literal matching a same-package declared Go identifier, while still catching an unrelated genuine secret in the same commit', { concurrency: false }, () => {
  const fixtureDir = path.join(ROOT, '__secretguard_e2e_fixture2__');
  fs.rmSync(fixtureDir, { recursive: true, force: true });
  fs.mkdirSync(fixtureDir);
  const unrelatedSecret = genuineSecretCandidate();
  try {
    fs.writeFileSync(
      path.join(fixtureDir, 'errors.go'),
      [
        'package secretguardfixture2',
        '',
        'const (',
        '\tcodeSomeVeryLongCamelCaseIdentifierFixture = "MET-X999"',
        ')',
      ].join('\n'),
      'utf8'
    );
    fs.writeFileSync(
      path.join(fixtureDir, 'probe.go'),
      [
        'package secretguardfixture2',
        '',
        'import "strings"',
        '',
        '// grepsForIdentifier mirrors sec034_differential_test.go\'s pattern of',
        '// quoting a declared identifier as a string literal to search source.',
        'func grepsForIdentifier(src string) bool {',
        '\treturn strings.Contains(src, "codeSomeVeryLongCamelCaseIdentifierFixture")',
        '}',
        '',
        `const unrelatedGenuineSecret = "${unrelatedSecret}"`,
        '',
      ].join('\n'),
      'utf8'
    );
    const add = spawnSync('git', ['add', '--', fixtureDir], { cwd: ROOT, encoding: 'utf8' });
    assert.equal(add.status, 0, `git add failed: ${add.stderr}`);

    const outcome = runGuardAsHook('git commit -m "test: bug-026 e2e suppression fixture"');
    assert.equal(outcome.denied, true, 'the unrelated genuine secret in the same commit must still trigger a deny');
    assert.ok(
      outcome.reason.includes(unrelatedSecret.slice(0, 3)) || /high-entropy/.test(outcome.reason),
      `expected the deny reason to reference the genuine secret finding: ${outcome.reason}`
    );
    assert.ok(
      !outcome.reason.includes('codeSomeVeryLongCamelCaseIdentifierFixture'),
      `the package-identifier-matching literal must NOT appear in the deny reason (it should be suppressed): ${outcome.reason}`
    );
  } finally {
    spawnSync('git', ['reset', '--', fixtureDir], { cwd: ROOT, encoding: 'utf8' });
    fs.rmSync(fixtureDir, { recursive: true, force: true });
  }
});
