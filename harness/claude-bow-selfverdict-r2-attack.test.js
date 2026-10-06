// Module key: tool.bow (attack tests — BUG-340 independent round r2)
// Spec ref: GR#23 independence amendment; BUG-340 r1 findings F1/F3.
//
// LEFT IN THE TREE DELIBERATELY by the r2 independent Destructive round.
// These encode attacks that ARE NOT YET CLOSED (r2 REJECT). Each RED test
// below is a live bypass proved by hand against the real DB + the real
// commit-msg hook in a throwaway repo, then reduced here to the smallest
// deterministic unit form so it can be run in CI without a database.
//
// DO NOT delete or weaken a test here to go green — fix the code it names.

'use strict';

const test = require('node:test');
const assert = require('node:assert');
const path = require('path');

const bow = require('./claude-bow.js');

// ---------------------------------------------------------------------------
// R2-F1 (P0, OPEN): recorder_cwd is stamped from the RECORDING process's raw
// process.cwd(), but isSelfVerdict() compares it against the COMMITTER's git
// TOPLEVEL. The two sides therefore have different granularity: any recorder
// that happened to be cd'd into a SUBDIRECTORY of the very repository the
// commit comes from produces a recorder_cwd that never string-equals the
// committer's toplevel, so the self-verdict refusal silently does not fire.
//
// Proved live in round r2, on BOTH enforcement surfaces:
//   - `node claude-bow.js destructive <code> --verdict accept` run from
//     <repo>/internal, then a real `git commit` in <repo> through the
//     INSTALLED githooks/commit-msg -> committed, exit 0.
//   - the same verdict evaluated by claude-destructive-guard.js (PreToolUse)
//     with cwd=<repo> -> allowSilently.
// The same verdict recorded from <repo> itself is correctly refused, which
// is what makes this a granularity bug rather than a dead check.
//
// This is not an adversarial spoof (F6 already documents env spoofability):
// recording from a subdirectory is ordinary usage — a builder sitting in
// `webconsole/` or `internal/<pkg>/` when it records its own verdict gets an
// accepted self-verdict for free, which is exactly the scenario BUG-340 was
// filed to make mechanically impossible.
//
// FIX DIRECTION: stamp the RECORDER's git toplevel (git rev-parse
// --show-toplevel from process.cwd(), falling back to process.cwd() when it
// is not a repo) rather than the raw cwd, so both sides of the comparison
// are the same kind of thing; or compare containment (committer toplevel is
// a prefix of recorder_cwd) instead of equality.
// ---------------------------------------------------------------------------
test('BUG-340 r2 F1: a verdict recorded from a SUBDIRECTORY of the committing repo must still count as a self-verdict', () => {
  const repoRoot = path.resolve('/tmp/r2fixture/repo');
  const subdir = path.join(repoRoot, 'internal', 'pkg');
  const session = 'session-under-test';

  const sameDirVerdict = {
    recorder_session: session,
    recorder_cwd: bow.normalizeRecorderCwd(repoRoot),
  };
  // Control: the already-working case. If THIS ever goes red the whole
  // mechanism is dead, not merely coarse.
  assert.equal(
    bow.isSelfVerdict(sameDirVerdict, session, repoRoot),
    true,
    'control: a verdict recorded from the repo root, same session, IS a self-verdict'
  );

  const subdirVerdict = {
    recorder_session: session,
    recorder_cwd: bow.normalizeRecorderCwd(subdir),
  };
  assert.equal(
    bow.isSelfVerdict(subdirVerdict, session, repoRoot),
    true,
    'BUG-340 r2 F1 (OPEN): recorder_cwd is the recorder\'s raw process.cwd() while the committer side ' +
      'is a git TOPLEVEL — so a self-verdict recorded from any subdirectory of the same repository ' +
      'silently escapes the independence check. Stamp/compare the recorder\'s git toplevel, or compare ' +
      'containment rather than equality.'
  );
});

// ---------------------------------------------------------------------------
// R2-F2 (P2, OPEN): a verdict row with NULL recorder_cwd (or NULL
// recorder_session) — i.e. every one of the 1043-of-1047 rows that predate
// this migration, and every row written by a lane with no session env — is
// ALLOWED, which is the correct posture, but is allowed SILENTLY. Nothing in
// either guard emits the "independence not verifiable for this verdict" line
// the r2 acceptance bar requires, so an operator reading a successful commit
// cannot distinguish "independence was checked and passed" from
// "independence could not be checked at all". Grepped across claude-bow.js,
// claude-destructive-guard.js and githooks/verdict-guard.js in r2: no such
// text exists anywhere.
//
// This test asserts only the ALLOW half (which is correct and must not
// regress to a deny); the missing-warning half is recorded here in prose
// because the guards have no warn channel to assert against yet — adding one
// is the fix.
// ---------------------------------------------------------------------------
test('BUG-340 r2 F2: a legacy verdict row (NULL recorder_cwd / NULL recorder_session) is allowed, never treated as a match', () => {
  const session = 'session-under-test';
  const repoRoot = path.resolve('/tmp/r2fixture/repo');
  assert.equal(bow.isSelfVerdict({ recorder_session: session, recorder_cwd: null }, session, repoRoot), false);
  assert.equal(bow.isSelfVerdict({ recorder_session: null, recorder_cwd: bow.normalizeRecorderCwd(repoRoot) }, session, repoRoot), false);
  assert.equal(bow.isSelfVerdict({ recorder_session: 'unknown', recorder_cwd: 'unknown' }, session, repoRoot), false);
  assert.equal(bow.isSelfVerdict({ recorder_session: session, recorder_cwd: bow.normalizeRecorderCwd(repoRoot) }, 'unknown', repoRoot), false);
});

// ---------------------------------------------------------------------------
// Path-normalisation controls for F1(c). These all PASS today; they exist so
// a future "simplify" of normalizeRecorderCwd() cannot silently reintroduce
// the two-spellings-of-one-directory hole that r1 named.
// ---------------------------------------------------------------------------
test('BUG-340 r2 F1(c): recorder_cwd normalisation folds slashes, trailing separators and case', () => {
  const base = path.resolve('/tmp/r2fixture/repo');
  const n = bow.normalizeRecorderCwd(base);
  assert.equal(bow.normalizeRecorderCwd(base + path.sep), n, 'trailing separator must normalise away');
  if (path.sep === '\\') {
    // Backslash-as-separator folding is Windows-only semantics; on POSIX a
    // backslash is a valid filename character, so this assertion is only
    // meaningful (and only true) where path.sep === '\\'. Without this guard
    // the case reddens on the Linux CI node-test runner.
    assert.equal(bow.normalizeRecorderCwd(base.replace(/[\\/]/g, '\\')), n, 'backslash spelling must normalise (Windows separator semantics)');
  }
  assert.equal(bow.normalizeRecorderCwd(base.replace(/[\\/]/g, '/')), n, 'forward-slash spelling must normalise');
  assert.equal(bow.normalizeRecorderCwd(base.toUpperCase()), n, 'case must fold (core.ignorecase filesystem)');
  assert.match(n, /^[^A-Z]*$/, 'the normalised form is lower-case');
  assert.equal(bow.normalizeRecorderCwd(''), '', 'empty input never throws and yields no cwd');
  assert.equal(bow.normalizeRecorderCwd(null), '', 'null input never throws and yields no cwd');
});
