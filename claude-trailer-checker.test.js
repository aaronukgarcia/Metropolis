/**
 * claude-trailer-checker.test.js — BUG-088 tests for
 * claude-trailer-checker.js (AC-D1: the trailer checker is a REPLACEMENT,
 * not a relocation, of claude-pre-commit-check.js's extraction machinery —
 * see that module's header for the full reasoning).
 *
 * Proves:
 *   1. AC-B4: no boundary-regex/quote-mask/engage-decision trigger
 *      machinery, and none of the old extraction helpers
 *      (extractMFlagMessages/extractFileFlagPaths/extractHeredocBodies/
 *      MSG_FLAG_RE/FILE_FLAG_RE/HEREDOC_RE) are carried forward.
 *   2. AC-B2: header names the ASM-386 verb-coverage gap.
 *   3. AC-D1: detects a trailer anywhere in a real message FILE (not a
 *      command string) — a single test collapses what used to require
 *      three separate extraction-path suites, per the acceptance file's own
 *      framing. A second test proves the three previously-catalogued
 *      residual gaps (bare git commit / -F - via a plain pipe / unreadable
 *      -F target) no longer exist AS GAPS at this hook point, because the
 *      file is always readable when it exists.
 *   4. AC-F1: an unreadable message file is 'internal-error', never
 *      silently "no trailer found" ("clean").
 *
 * Run: node --test claude-trailer-checker.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const path = require('path');

const ROOT = __dirname;
const checker = require('./claude-trailer-checker.js');

test('AC-B4: claude-trailer-checker.js contains no boundary-regex/quote-mask trigger machinery, and no old extraction helpers', () => {
  const src = fs.readFileSync(path.join(ROOT, 'claude-trailer-checker.js'), 'utf8');
  assert.ok(!/buildQuoteMask|GIT_COMMIT_RE|isRealGitCommit/.test(src));
  assert.ok(!/extractMFlagMessages|extractFileFlagPaths|extractHeredocBodies|MSG_FLAG_RE|FILE_FLAG_RE|HEREDOC_RE/.test(src));
});

test('AC-B2: header names cherry-pick/revert/am + ASM-386', () => {
  const src = fs.readFileSync(path.join(ROOT, 'claude-trailer-checker.js'), 'utf8');
  assert.ok(/ASM-386/.test(src));
  assert.ok(/cherry-pick/.test(src));
  assert.ok(/revert/.test(src));
  assert.ok(/\bam\b/.test(src));
});

function withMessageFile(content, fn) {
  const p = path.join(ROOT, `__trailerchecker_msg_${process.pid}_${Date.now()}_${Math.random().toString(36).slice(2)}.txt`);
  try {
    fs.writeFileSync(p, content, 'utf8');
    return fn(p);
  } finally {
    fs.rmSync(p, { force: true });
  }
}

// ---------------------------------------------------------------------------
// AC-D1: detection over a real message file, collapsing the three old
// extraction paths (-m, -F, heredoc) into one — because commit-msg always
// hands over the same COMMIT_EDITMSG file regardless of source flag.
// ---------------------------------------------------------------------------

test('AC-D1: detects a trailer anywhere in the message file text (-m-shaped content)', () => {
  withMessageFile('feat: something\n\nCo-Authored-By: Claude <noreply@anthropic.com>\n', p => {
    const result = checker.checkTrailer(p);
    assert.equal(result.status, 'found-problems');
    assert.ok(result.findings.length > 0);
  });
});

test('AC-D1: detects a trailer even mid-body, matching the -F/heredoc-sourced content shape', () => {
  withMessageFile(
    ['fix: whatever', '', 'Some body text.', 'co-authored-by: someone <x@y.com>', 'More text.'].join('\n'),
    p => {
      const result = checker.checkTrailer(p);
      assert.equal(result.status, 'found-problems');
    }
  );
});

test('AC-D1: a clean message (no trailer) reports clean', () => {
  withMessageFile('chore: routine update\n\nNo trailer here.\n', p => {
    const result = checker.checkTrailer(p);
    assert.equal(result.status, 'clean');
  });
});

// ---------------------------------------------------------------------------
// AC-D1: the three previously-catalogued residual gaps no longer exist as
// gaps at this hook point — there is no "couldn't find the message" case,
// because commit-msg always receives a real file.
// ---------------------------------------------------------------------------

test('AC-D1: an empty message file (the old "bare git commit" gap) is inspectable, not a gap — reports clean, not an error', () => {
  withMessageFile('', p => {
    const result = checker.checkTrailer(p);
    // An empty file is legitimately inspectable (git itself would reject an
    // empty commit message before commit-msg even runs in the general
    // case, but this checker's OWN job is just "is a trailer present" —
    // an empty file plainly has no trailer, which is a clean read, not an
    // internal error).
    assert.equal(result.status, 'clean');
  });
});

test('AC-D1: the old "-F - via a plain pipe" gap does not exist here — commit-msg always supplies a real file, never inspected as a gap', () => {
  // There is no equivalent scenario to construct for this checker (by
  // design — it never reads a pipe, only a resolved file path), so this
  // test documents the claim structurally: checkTrailer's signature takes
  // only a file path, never a command string or a stdin source.
  assert.equal(checker.checkTrailer.length, 1);
});

// ---------------------------------------------------------------------------
// AC-F1: unreadable file is 'internal-error', never silently "clean".
// ---------------------------------------------------------------------------

test('AC-F1: an unreadable/missing message file is {status:"internal-error"}, never "clean"', () => {
  const missingPath = path.join(ROOT, `__trailerchecker_missing_${process.pid}_${Date.now()}.txt`);
  assert.ok(!fs.existsSync(missingPath));
  const result = checker.checkTrailer(missingPath);
  assert.equal(result.status, 'internal-error');
  assert.ok(result.error instanceof Error);
});
