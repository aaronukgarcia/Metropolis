/**
 * claude-quote-mask.test.js — direct regression coverage for the shared,
 * canonical `buildQuoteMask()` scanner (claude-quote-mask.js), extracted in
 * BUG-123 round 6.
 *
 * WHY THIS FILE EXISTS (Bill's round-6 ruling, requirement 3): rounds 3, 4
 * and 5 of BUG-123 each shipped a hand-rolled quote scanner in
 * claude-git-commit-trigger.js that MODELED this module's escape-aware
 * behaviour instead of reusing it, and each hand-rolled copy shipped a gap
 * its model had already closed — most recently, attacker "Marrow"'s
 * round-5 finding that an ODD count of backslash-escaped quotes inside a
 * value (`-c key="a\"b"`) mispaired a positional quote scanner and caused a
 * real `git commit` to go completely unscanned. Porting Marrow's exact
 * repro (and its 1/3/5-embedded-quote generalisation) into THIS file, next
 * to buildQuoteMask()'s own definition, means every future consumer of this
 * module inherits the coverage automatically — nobody has to remember to
 * copy it into a new guard-specific test file the way each prior round's
 * fix had to be rediscovered independently.
 *
 * This file complements, not replaces, quote-mask-drift.test.js (which
 * checks for ACCIDENTAL RE-FORKING — a second file somewhere in the repo
 * declaring its own `function buildQuoteMask(`) and
 * claude-git-commit-trigger's own BUG-123-round-6 tests in
 * claude-secret-guard.test.js (which check the END-TO-END trigger behaviour
 * through the full `git -c ...` boundary/verb pipeline, not just the mask
 * itself).
 *
 * Run: node --test claude-quote-mask.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const { buildQuoteMask, consumeShellToken, dequoteShellToken } = require('./claude-quote-mask.js');

/** Renders a mask as a string of 0/1 for readable failure messages. */
function maskToString(mask) {
  return mask.map(b => (b ? '1' : '0')).join('');
}

function assertMask(text, expected, msg) {
  const actual = buildQuoteMask(text);
  assert.equal(actual.length, text.length, `${msg}: mask length must equal text length`);
  const actualStr = maskToString(actual);
  const expectedStr = maskToString(expected);
  assert.equal(
    actualStr,
    expectedStr,
    `${msg}\n  text:     ${JSON.stringify(text)}\n  expected: ${expectedStr}\n  actual:   ${actualStr}`
  );
}

// ---------------------------------------------------------------------------
// Baseline behaviour
// ---------------------------------------------------------------------------

test('plain unquoted text: mask is all false', () => {
  const text = 'git commit -m x';
  assertMask(text, new Array(text.length).fill(false), 'no quotes anywhere');
});

test('a simple double-quoted region is masked true, including the quote characters', () => {
  const text = 'echo "git commit" done';
  const expected = new Array(text.length).fill(false);
  for (let i = text.indexOf('"'); i <= text.lastIndexOf('"'); i++) expected[i] = true;
  assertMask(text, expected, 'simple double-quoted region');
});

test('a simple single-quoted region is masked true, including the quote characters', () => {
  const text = "echo 'git commit' done";
  const expected = new Array(text.length).fill(false);
  for (let i = text.indexOf("'"); i <= text.lastIndexOf("'"); i++) expected[i] = true;
  assertMask(text, expected, 'simple single-quoted region');
});

// ---------------------------------------------------------------------------
// BUG-077: backslash-escape rules
// ---------------------------------------------------------------------------

test('BUG-077: a backslash-escaped quote OUTSIDE any quoted region does not open a phantom region', () => {
  const text = 'echo \\"git commit';
  assertMask(text, new Array(text.length).fill(false), 'the escaped quote must stay unmasked, and nothing after it becomes masked');
});

test('an escaped quote INSIDE double quotes stays inside the same quoted region (does not close it early)', () => {
  // Shell text: echo "he said \"hi\" to git commit" done
  const text = 'echo "he said \\"hi\\" to git commit" done';
  const quoteStart = text.indexOf('"');
  const quoteEnd = text.lastIndexOf('"');
  const expected = new Array(text.length).fill(false);
  for (let i = quoteStart; i <= quoteEnd; i++) expected[i] = true;
  assertMask(text, expected, 'escaped quotes inside double quotes must not prematurely close the region');
});

test('single-quoted regions take NO backslash escapes (real shell semantics)', () => {
  // In a real shell, 'a\'  is a CLOSED quote ('a\') followed by an escaped
  // quote outside it — single quotes cannot contain an escaped quote at all.
  const text = "echo 'a\\'";
  // 'a\' -> quote opens at index 5, 'a' at 6, '\\' at 7 (inside quote, no
  // escape rule for single quotes so this is just a literal backslash
  // character), '\'' at 8 closes the quote.
  const expected = new Array(text.length).fill(false);
  for (let i = 5; i <= 8; i++) expected[i] = true;
  assertMask(text, expected, 'single quotes must not treat backslash as an escape character');
});

// ---------------------------------------------------------------------------
// BUG-123 round 6 (Marrow): odd vs. even embedded escaped-quote counts.
//
// This is the exact scenario that broke claude-git-commit-trigger.js's
// round-4 hand-rolled consumeShellToken(): given a value like
// `key="a\"b"`, a naive scanner that pairs quote characters POSITIONALLY
// (ignoring a preceding backslash) will treat the escaped `\"` as an
// ordinary close-quote, ending the region one character early and then
// mis-reading the value's REAL closing `"` as a fresh, unterminated
// open-quote. buildQuoteMask() gets this right because it is escape-aware:
// an escaped quote inside a double-quoted region never toggles `quote`.
// ---------------------------------------------------------------------------

/** Builds the shell text `key="` + body + `"` where `body` contains `n`
 * embedded backslash-escaped double quotes (alternating literal segments),
 * and returns { text, quoteStart, quoteEnd } — quoteStart/quoteEnd are the
 * indices of the value's own opening and closing quote characters, i.e. the
 * full correctly-parsed masked region. */
function buildEmbeddedQuoteFixture(n) {
  const letters = 'abcdefghijklmnop'.split('');
  let body = '';
  for (let i = 0; i <= n; i++) {
    body += letters[i];
    if (i < n) body += '\\"';
  }
  const text = `git -c key="${body}" commit -m x`;
  return { text, body };
}

function assertEmbeddedQuoteCaseMasksCorrectly(n, label) {
  const { text, body } = buildEmbeddedQuoteFixture(n);
  const openQuote = text.indexOf('"');
  const closeQuote = openQuote + 1 + body.length; // one char past the body is the closing quote
  assert.equal(text[closeQuote], '"', `${label}: fixture construction sanity check — expected a quote character here`);
  assert.equal(text.slice(closeQuote + 1, closeQuote + 9), ' commit ', `${label}: fixture construction sanity check — expected " commit " right after the value`);

  const mask = buildQuoteMask(text);
  // Every position from the opening quote through the closing quote
  // (inclusive) must be masked true, i.e. "inside the value's quoted
  // region" — this is what lets a caller correctly find the token's TRUE
  // end (the closing quote), not a spuriously-early or spuriously-late one.
  for (let i = openQuote; i <= closeQuote; i++) {
    assert.equal(mask[i], true, `${label}: position ${i} ('${text[i]}') must be inside the quoted region`);
  }
  // Immediately after the closing quote is unquoted whitespace, then the
  // real verb word "commit" — none of that may be masked.
  const verbStart = closeQuote + 1;
  const verbEnd = verbStart + ' commit'.length;
  for (let i = verbStart; i < verbEnd; i++) {
    assert.equal(mask[i], false, `${label}: position ${i} ('${text[i]}') is past the value and must NOT be masked`);
  }
}

test('BUG-123 round 6 (Marrow): 1 embedded escaped quote — mask correctly spans the whole value, not one char short', () => {
  assertEmbeddedQuoteCaseMasksCorrectly(1, '1 embedded escaped quote (Marrow exact repro shape)');
});

test('BUG-123 round 6: 3 embedded escaped quotes (odd) mask correctly', () => {
  assertEmbeddedQuoteCaseMasksCorrectly(3, '3 embedded escaped quotes');
});

test('BUG-123 round 6: 5 embedded escaped quotes (odd) mask correctly', () => {
  assertEmbeddedQuoteCaseMasksCorrectly(5, '5 embedded escaped quotes');
});

test('BUG-123 round 6: 0 embedded escaped quotes (even, baseline) masks correctly', () => {
  assertEmbeddedQuoteCaseMasksCorrectly(0, '0 embedded escaped quotes');
});

test('BUG-123 round 6: 2 embedded escaped quotes (even) masks correctly', () => {
  assertEmbeddedQuoteCaseMasksCorrectly(2, '2 embedded escaped quotes');
});

test('BUG-123 round 6: 4 embedded escaped quotes (even) masks correctly', () => {
  assertEmbeddedQuoteCaseMasksCorrectly(4, '4 embedded escaped quotes');
});

test("Marrow's exact literal repro text end-to-end against the mask", () => {
  const text = 'git -c key="a\\"b" commit -m x';
  // git -c key="a\"b" commit -m x
  // indices:    0123456789...
  const openQuote = text.indexOf('"');
  const closeQuote = text.lastIndexOf('"');
  const mask = buildQuoteMask(text);
  for (let i = openQuote; i <= closeQuote; i++) {
    assert.equal(mask[i], true, `position ${i} must be inside the quoted value`);
  }
  assert.equal(mask[closeQuote + 1], false, 'the space right after the closing quote must not be masked');
  const commitIdx = text.indexOf('commit');
  for (let i = commitIdx; i < commitIdx + 'commit'.length; i++) {
    assert.equal(mask[i], false, `the real "commit" verb word (index ${i}) must never be masked`);
  }
});

// ---------------------------------------------------------------------------
// Unterminated quote — the established swallow-to-EOF fail-safe.
// ---------------------------------------------------------------------------

test('an unterminated quote swallows to end of string (declared fail-safe)', () => {
  const text = 'git -c key="unterminated commit -m x';
  const mask = buildQuoteMask(text);
  const openQuote = text.indexOf('"');
  for (let i = openQuote; i < text.length; i++) {
    assert.equal(mask[i], true, `position ${i} must remain masked through to end of string for an unterminated quote`);
  }
});

// ---------------------------------------------------------------------------
// Heredoc handling (BUG-078) — sanity that the extraction preserved this.
// ---------------------------------------------------------------------------

test('BUG-078: a stray unbalanced quote inside a heredoc body does not leak past the terminator', () => {
  const text = 'cat <<EOF\n' + "it's a test\n" + 'EOF\n' + 'git commit -m "x"\n';
  const mask = buildQuoteMask(text);
  const heredocStart = text.indexOf('<<EOF');
  const heredocEnd = text.indexOf('EOF\n', heredocStart + 5) + 4; // through the terminator line
  for (let i = heredocStart; i < heredocEnd; i++) {
    assert.equal(mask[i], true, `position ${i} (heredoc body) must be masked inert`);
  }
  const commitIdx = text.indexOf('git commit');
  for (let i = commitIdx; i < commitIdx + 'git commit'.length; i++) {
    assert.equal(mask[i], false, `the real git commit invocation after the heredoc (index ${i}) must not be masked`);
  }
});

// ---------------------------------------------------------------------------
// Requirement 2 proof: this module is the ONLY place buildQuoteMask is
// literally declared in the repo (author-guard / pre-commit-check /
// git-commit-trigger must all require() it, never redefine it).
// ---------------------------------------------------------------------------

test('claude-author-guard.js and claude-pre-commit-check.js both re-export the SAME shared function object (no reimplementation)', () => {
  const authorGuard = require('./claude-author-guard.js');
  const preCommitCheck = require('./claude-pre-commit-check.js');
  assert.equal(authorGuard.buildQuoteMask, buildQuoteMask, 'claude-author-guard.js must export the shared function by reference, not a copy');
  assert.equal(preCommitCheck.buildQuoteMask, buildQuoteMask, 'claude-pre-commit-check.js must export the shared function by reference, not a copy');
});

// ---------------------------------------------------------------------------
// consumeShellToken / dequoteShellToken (BUG-044 round 2 extraction) — moved
// here from claude-git-commit-trigger.js's own copy so claude-author-guard.js
// could share it instead of hand-rolling a fourth quote scanner. Requirement
// 2's "single declaration" guarantee only ever covered buildQuoteMask() by
// name (see quote-mask-drift.test.js), so these get their own direct tests.
// ---------------------------------------------------------------------------

test('consumeShellToken stops at the first UNQUOTED whitespace, not the first whitespace of any kind', () => {
  const text = 'user.email="fake attacker <fake@evil.com>" commit';
  const end = consumeShellToken(text, 0);
  assert.equal(text.slice(0, end), 'user.email="fake attacker <fake@evil.com>"');
});

test('consumeShellToken returns -1 for an empty token (whitespace/EOF right at start)', () => {
  assert.equal(consumeShellToken('   ', 0), -1);
  assert.equal(consumeShellToken('abc', 3), -1);
});

test('consumeShellToken returns -1 for an unterminated quote (swallowed to EOF)', () => {
  const text = 'user.email="unterminated';
  assert.equal(consumeShellToken(text, 0), -1);
});

test('consumeShellToken reuses a precomputed mask when passed one (no rebuild)', () => {
  const text = 'user.email="a b" commit';
  const mask = buildQuoteMask(text);
  assert.equal(consumeShellToken(text, 0, mask), 'user.email="a b"'.length);
});

test('dequoteShellToken strips a fully double-quoted value and preserves the embedded space', () => {
  assert.equal(dequoteShellToken('user.email="fake attacker <fake@evil.com>"'), 'user.email=fake attacker <fake@evil.com>');
});

test('dequoteShellToken strips a fully single-quoted value', () => {
  assert.equal(dequoteShellToken("user.name='Fake Attacker'"), 'user.name=Fake Attacker');
});

test('dequoteShellToken resolves an escaped embedded double quote inside a double-quoted region (BUG-123 round 6 shape)', () => {
  assert.equal(dequoteShellToken(String.raw`user.email="a\"b c"`), 'user.email=a"b c');
});

test('dequoteShellToken leaves a bare unquoted token unchanged', () => {
  assert.equal(dequoteShellToken('user.email=fake@evil.com'), 'user.email=fake@evil.com');
});

test('consumeShellToken + dequoteShellToken together recover BUG-044 round 2\'s exact repro value as one dequoted token', () => {
  const text = 'git -c user.email="fake attacker <fake@evil.com>" commit --allow-empty -m x';
  const valueStart = text.indexOf('user.email');
  const valueEnd = consumeShellToken(text, valueStart);
  const kv = dequoteShellToken(text.slice(valueStart, valueEnd));
  assert.equal(kv, 'user.email=fake attacker <fake@evil.com>');
});
