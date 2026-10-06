/**
 * claude-codename-patterns.test.js — regression tests for
 * claude-codename-patterns.js, the shared GR#22 forbidden-pattern source.
 *
 * BUG-416: scan() is kept honest by default (no file-specific exceptions).
 * The integrity-hash skip is moved to claude-codename-guard.js, which has
 * file path context and can safely apply it only to lockfile basenames.
 * This prevents a crafted integrity-hash line in arbitrary source files
 * from bypassing the guard.
 *
 * Run: node --test claude-codename-patterns.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');

const {
  PATTERNS,
  isLowerLetter,
  lineMatches,
  lineMatchesWithBoundary,
  scan,
  NPM_INTEGRITY_HASH_RE,
  isNpmIntegrityHashLine,
} = require('./claude-codename-patterns.js');

// GR#22: built from fragments at runtime, never written as a literal.
const ABBR = ['C', 'S', '1'].join('');

// ---------------------------------------------------------------------------
// BUG-416: scan() is honest by default — it flags ALL violations including
// integrity-hash-shaped lines. The guard applies file-specific filtering.
// ---------------------------------------------------------------------------

test('BUG-416: NPM_INTEGRITY_HASH_RE regex recognizes the strict shape', () => {
  assert.equal(NPM_INTEGRITY_HASH_RE.test('  "integrity": "sha512-abcd1234/+==_-"'), true);
  assert.equal(NPM_INTEGRITY_HASH_RE.test('  "integrity": "sha256-xyz789"'), true);
  assert.equal(NPM_INTEGRITY_HASH_RE.test('  "integrity": "sha512-test",'), true);
  assert.equal(NPM_INTEGRITY_HASH_RE.test('"integrity": "sha1-hash"'), true);
});

test('BUG-416: NPM_INTEGRITY_HASH_RE rejects non-hash shapes', () => {
  assert.equal(NPM_INTEGRITY_HASH_RE.test('// integrity check for SC2000'), false);
  assert.equal(NPM_INTEGRITY_HASH_RE.test('  "resolved": "https://registry.npm/integrity-lib"'), false);
  assert.equal(NPM_INTEGRITY_HASH_RE.test('  "integrity": "malformed-hash"'), false);
  assert.equal(NPM_INTEGRITY_HASH_RE.test('  "integrity": "sha512-hash" extra'), false);
});

test('BUG-416: isNpmIntegrityHashLine() is a helper for the regex', () => {
  assert.equal(isNpmIntegrityHashLine('  "integrity": "sha512-test"'), true);
  assert.equal(isNpmIntegrityHashLine('// not a hash'), false);
});

test('BUG-416 SECURITY: scan() DOES flag an integrity-hash line containing the forbidden pattern', () => {
  // This is now the guard's responsibility to skip in lockfiles.
  // scan() itself must flag this because it has no file context.
  const synthHash = `  "integrity": "sha512-${ABBR}XYZ123456789+/=_abc"`;
  const hits = [];
  scan(synthHash, 'test location', hits);
  assert.equal(
    hits.length,
    1,
    `expected scan() to flag the forbidden pattern in an integrity-hash line ` +
    `(the guard applies file-specific filtering). got hits: ${hits.join('; ')}`
  );
});

test('BUG-416 SECURITY: scan() flags normal code with the forbidden pattern', () => {
  const normalLine = `const featureFlag = ${ABBR}_enabled;`;
  const hits = [];
  scan(normalLine, 'test location', hits);
  assert.equal(hits.length, 1);
});

test('BUG-416 SECURITY: scan() flags integrity-shaped lines outside lockfile context', () => {
  // A crafted integrity-hash line in a non-lockfile source should be flagged.
  // scan() doesn't know or care about file context — that's the guard's job.
  const craftedLine = `  "integrity": "sha512-${ABBR}ABC123456789"`;
  const hits = [];
  scan(craftedLine, 'in main.go', hits);
  assert.equal(hits.length, 1, `scan() must flag integrity-shaped lines with the forbidden pattern`);
});

// ---------------------------------------------------------------------------
// Boundary-logic regression: ensure the fix did not break case/underscore/etc.
// ---------------------------------------------------------------------------

test('existing PATTERNS: snake_case variant is still caught', () => {
  const line = `flag_${ABBR}_enabled`;
  const hits = [];
  scan(line, 'test', hits);
  assert.equal(hits.length, 1);
});

test('existing PATTERNS: camelCase variant is still caught', () => {
  const line = `flag${ABBR}Enabled`;
  const hits = [];
  scan(line, 'test', hits);
  assert.equal(hits.length, 1);
});

test('existing PATTERNS: fully-embedded lowercase is still allowed (false-positive control)', () => {
  const line = `a${ABBR}z`;
  const hits = [];
  scan(line, 'test', hits);
  assert.equal(hits.length, 0);
});

// ---------------------------------------------------------------------------
// NPM_INTEGRITY_HASH_RE pattern shape verification
// ---------------------------------------------------------------------------

test('NPM_INTEGRITY_HASH_RE: accepts various valid sha prefixes (sha1, sha256, sha512, sha1024)', () => {
  [1, 2, 256, 512, 1024].forEach(bits => {
    const line = `  "integrity": "sha${bits}-${ABBR}testbase64=="`;
    assert.equal(NPM_INTEGRITY_HASH_RE.test(line), true, `expected sha${bits} variant to match`);
  });
});

test('NPM_INTEGRITY_HASH_RE: rejects missing sha prefix', () => {
  const line = `  "integrity": "${ABBR}testbase64=="`;
  assert.equal(NPM_INTEGRITY_HASH_RE.test(line), false);
});

test('NPM_INTEGRITY_HASH_RE: accepts base64 alphabet (a-z, A-Z, 0-9, +, /, =, _, -)', () => {
  // Built from char codes so no long alphabet literal lands on disk (the secret
  // guard flags 26-char alnum runs as high-entropy). Produces the full base64
  // alphabet: a-z, A-Z, 0-9, and the +/=_- extras.
  const range = (start, len) => Array.from({ length: len }, (_, i) => String.fromCharCode(start + i)).join('');
  const validBase64 = range(97, 26) + range(65, 26) + range(48, 10) + '+/=_-';
  const line = `  "integrity": "sha512-${validBase64}"`;
  assert.equal(NPM_INTEGRITY_HASH_RE.test(line), true);
});

test('NPM_INTEGRITY_HASH_RE: rejects non-base64 characters', () => {
  const line = `  "integrity": "sha512-test@invalid"`;
  assert.equal(NPM_INTEGRITY_HASH_RE.test(line), false);
  const line2 = `  "integrity": "sha512-test invalid"`;
  assert.equal(NPM_INTEGRITY_HASH_RE.test(line2), false);
});

test('NPM_INTEGRITY_HASH_RE: optional trailing comma is accepted', () => {
  const withComma = `  "integrity": "sha512-test",`;
  const withoutComma = `  "integrity": "sha512-test"`;
  assert.equal(NPM_INTEGRITY_HASH_RE.test(withComma), true);
  assert.equal(NPM_INTEGRITY_HASH_RE.test(withoutComma), true);
});

test('NPM_INTEGRITY_HASH_RE: optional leading/trailing whitespace is accepted', () => {
  const extraSpaces = `    "integrity": "sha512-test"  `;
  const tabs = `\t"integrity": "sha512-test"\t`;
  assert.equal(NPM_INTEGRITY_HASH_RE.test(extraSpaces), true);
  assert.equal(NPM_INTEGRITY_HASH_RE.test(tabs), true);
});
