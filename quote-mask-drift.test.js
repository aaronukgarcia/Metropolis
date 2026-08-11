/**
 * quote-mask-drift.test.js — behavioural drift control for buildQuoteMask()
 * and its heredoc helpers (BUG-076).
 *
 * BACKGROUND (see BUG-076, ASM-356's lead ruling). buildQuoteMask() started
 * life in claude-author-guard.js (BUG-043's fix, hardened by BUG-077/BUG-078)
 * and was deliberately COPIED — not factored into a shared module — into
 * four sibling PreToolUse guards: claude-pre-commit-check.js,
 * claude-secret-guard.js, claude-version-guard.js, claude-plan-guard.js. The
 * copy decision stands (a PreToolUse guard must still emit a decision when a
 * shared dependency is broken; claude-destructive-guard.js proved a `require`
 * of another guard's module fails OPEN, not closed, when that module is
 * missing/broken — see ASM-356). But nothing was watching the five copies
 * for divergence, and this project has already seen this exact shape drift
 * silently once: claude-destructive-guard.js's own independent copy of a
 * DIFFERENT shared regex drifted by one character in the same day (a mandatory
 * vs. optional path separator), and "git" commit slipped the GR#23 gate.
 * "Currently identical, comments aside" (Tester-9's finding) is precisely the
 * state every drift starts from.
 *
 * WHAT THIS FILE DOES NOT DO: it does not diff source text. A comment-only
 * difference (the four ports deliberately carry shorter comments than the
 * original) must never fail a build, and source-identity is a stronger claim
 * than the guards actually need — two copies can read differently and still
 * decide identically. Instead this file calls the five copies' actual
 * buildQuoteMask() and runs a shared corpus of shell-quoting constructs
 * through every one of them, asserting BEHAVIOURAL equivalence.
 *
 * LOCATING THE COPIES (judgement call #1, logged here per the brief).
 * A hardcoded five-file list was rejected on the same grounds BUG-076 itself
 * documents: a sixth copy (or a copy under a renamed/moved file) would be
 * invisible to a hardcoded list, which is exactly the blind spot that let
 * five copies accumulate with nothing watching them in the first place.
 * Instead, discoverCopies() below recursively scans the whole repo (skipping
 * node_modules/.git/dotdirs and this file's own *.test.js siblings) for any
 * *.js file whose source matches /function\s+buildQuoteMask\s*\(/, and the
 * corpus loop below runs against WHATEVER that scan finds, not a fixed list.
 * This means: (a) a new copy dropped in anywhere in the repo is automatically
 * pulled into the comparison the next time this test runs, no edit to this
 * file required; (b) a copy that is REMOVED (e.g. consolidated into a shared
 * module per ASM-356's "what would change my mind") silently shrinks the
 * comparison set instead of erroring — a known gap, see
 * ASM-357 below.
 * WHAT THIS STILL MISSES (logged honestly, not swept under the discovery
 * mechanism's apparent thoroughness):
 *   - A copy that renames the function (e.g. a local `const maskQuotes = ...`
 *     or `function _buildQuoteMask`) is invisible — the scan matches on the
 *     literal declaration text, not on behaviour or call sites.
 *   - A copy inside node_modules, a generated/vendored file, or a non-.js
 *     file (e.g. compiled/bundled output) is invisible — the scan is
 *     extension- and directory-filtered.
 *   - A file that RE-EXPORTS another module's buildQuoteMask (as
 *     claude-destructive-guard.js does for the whole author-guard toolkit)
 *     is correctly NOT flagged as a copy, because it contains no
 *     `function buildQuoteMask(` declaration of its own — see the
 *     destructive-guard note below for what that means in practice.
 *
 * claude-destructive-guard.js AND THIS TEST (judgement call #2). That guard
 * does not have a copy today — it `require()`s claude-author-guard.js and
 * calls the shared export directly (ASM-356). Because discoverCopies() keys
 * on the literal function declaration, destructive-guard is correctly
 * excluded from the corpus loop right now. If a future edit reintroduced a
 * literal `function buildQuoteMask(...)` body inside claude-destructive-
 * guard.js (reverting the ASM-356 sharing decision back to a copy), THIS
 * MECHANISM WOULD NOTICE AUTOMATICALLY on the next run — the scan is
 * source-pattern based, not a hardcoded exclude list, so a reintroduced copy
 * is picked up the same way a brand new one would be. What it would NOT
 * notice: destructive-guard's require() of author-guard silently starting to
 * resolve a STALE or shadowed author-guard module (e.g. a second
 * claude-author-guard.js appearing on a require path), because that is a
 * module-resolution question, not a source-pattern one. That risk is
 * out of scope for a drift test between literal copies and is not this
 * file's job.
 *
 * THE CORPUS (judgement call #3). Every construct below was chosen because
 * it already mattered in this project's own history, not because it is a
 * generic quoting edge case:
 *   - plain double- and single-quoted regions (the baseline the mask exists
 *     for at all)
 *   - an escaped quote INSIDE double quotes (real shell semantics the mask
 *     has always modelled — single quotes take no escapes)
 *   - a backslash-escaped quote OUTSIDE any quoted region (BUG-077 — must
 *     NOT open a phantom quoted region)
 *   - a stray, unbalanced quote inside a heredoc body (BUG-078 — heredoc
 *     bodies are opaque to a real shell; no quote parsing happens inside one
 *     at all)
 *   - a `<<-` heredoc with a tab-indented terminator line (Tester-10's
 *     post-BUG-078 adversarial pass)
 *   - an unterminated heredoc (ASM-351 — swallow-to-EOF is the declared,
 *     verified-accurate fail-safe; ASM-357 below records that this corpus
 *     entry asserts CURRENT documented behaviour, not a claim about what a
 *     real shell would do with truly malformed input)
 *   - the BUG-043 case: "git commit" appearing as prose inside a quoted
 *     string must not be read as a real invocation
 *   - a CRLF-terminated heredoc (BUG-081) — see the dedicated note below;
 *     this one is deliberately NOT asserted against a golden expectation.
 *
 * GOLDEN vs. CROSS-COPY-ONLY ASSERTIONS. Most corpus cases assert every
 * discovered copy's output against an explicit, hand-built expected mask
 * (built compositionally from labelled true/false segments so the expected
 * region for each case is visible in the test source, not computed via
 * error-prone manual index arithmetic). This catches BOTH divergence between
 * copies AND a synchronised-but-wrong update applied to all of them alike —
 * the brief's "a legitimate synchronised update to all five passes while a
 * partial update fails" requirement needs the golden check for the "wrong in
 * the same way" case, since pure cross-copy comparison cannot see it.
 *
 * BUG-081 (CRLF heredoc) is the one exception, and deliberately so: it is
 * filed and OPEN against claude-author-guard.js itself as of this test being
 * written (STATE TESTED AGAINST, see below) — findHeredocBodyEnd() does not
 * yet normalise \r before the terminator-line equality check in ANY of the
 * five copies, so a golden "correct" expectation would fail all five today
 * for a bug that is BUG-081's job to fix, not this drift test's. Asserting a
 * hand-derived "correct" answer here would conflate "not yet fixed anywhere"
 * with "diverged between copies," which is exactly the distinction this file
 * exists to keep clean. So the CRLF case only asserts the five copies AGREE
 * WITH EACH OTHER (whatever that shared answer is) — if BUG-081 is fixed in
 * one copy and not the others, this case starts failing for the right
 * reason: divergence, not incorrectness. See ASM-358.
 *
 * STATE TESTED AGAINST: 2026-08-11, the five files as they stood when
 * claude-bow.js show BUG-076 / ASM-356 were read for this task. All five
 * files were reported by the assigning brief as "changing under you" — this
 * file was NOT edited to chase that, per its own instructions (own only this
 * test file). A re-run against a later state is expected and is the entire
 * point: this file is meant to be run again after every synchronised update.
 *
 * PROVEN ABLE TO FAIL: this test was red-then-green proven via a throwaway
 * mutant copy under .scratch/ (never committed, never touching any of the
 * five live files) that reintroduced the exact BUG-077 shape into a stand-in
 * for claude-version-guard.js. See the Report to Ben for the transcript;
 * reproducing that proof is not part of this file's normal run (it would
 * require shipping a deliberately-broken fixture, which is a worse idea than
 * a one-time manual proof).
 *
 * Run: node --test quote-mask-drift.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const path = require('path');

const ROOT = __dirname;
const SKIP_DIRS = new Set(['node_modules', '.git']);

/** Recursively collects absolute paths of every *.js file under `dir`,
 * skipping node_modules/.git and any dotdir, and skipping this file's own
 * *.test.js siblings (a copy's own regression test file is not a copy). */
function collectJsFiles(dir, out) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    if (entry.name.startsWith('.')) continue;
    if (SKIP_DIRS.has(entry.name)) continue;
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      collectJsFiles(full, out);
    } else if (entry.isFile() && entry.name.endsWith('.js') && !entry.name.endsWith('.test.js')) {
      out.push(full);
    }
  }
  return out;
}

/** Discovers every file in the repo containing a literal buildQuoteMask
 * function declaration (see the header comment for what this does and does
 * not catch). Returns absolute paths, sorted for a deterministic report. */
function discoverCopies() {
  const all = collectJsFiles(ROOT, []);
  const pattern = /function\s+buildQuoteMask\s*\(/;
  const copies = [];
  for (const file of all) {
    const src = fs.readFileSync(file, 'utf8');
    if (pattern.test(src)) copies.push(file);
  }
  return copies.sort();
}

const KNOWN_COPIES_AS_OF_2026_08_11 = [
  'claude-author-guard.js',
  'claude-plan-guard.js',
  'claude-pre-commit-check.js',
  'claude-secret-guard.js',
  'claude-version-guard.js',
].sort();

const discovered = discoverCopies();

test('inventory: the discovered copy set matches the documented state (informational drift alarm)', () => {
  const relDiscovered = discovered.map(f => path.relative(ROOT, f).replace(/\\/g, '/')).sort();
  assert.deepEqual(
    relDiscovered,
    KNOWN_COPIES_AS_OF_2026_08_11,
    'The set of files containing a literal buildQuoteMask() declaration changed since this test ' +
      'was written. This is not automatically a bug: it could mean a copy was legitimately ' +
      'consolidated into a shared module (the ASM-356 "what would change my mind" case), or a ' +
      'NEW undocumented copy appeared (the exact failure mode BUG-076 exists to catch) — either ' +
      'way it needs a human look and, if intentional, this KNOWN_COPIES_AS_OF_2026_08_11 list ' +
      'and its date should be updated. The behavioural corpus tests below run against whatever ' +
      'was discovered regardless of this assertion\'s outcome, so a new copy still gets checked ' +
      'even if nobody updates this list.'
  );
});

/** Loads buildQuoteMask from a discovered copy, failing loudly (naming the
 * file) if the file matched the source pattern but does not actually export
 * a callable buildQuoteMask — that would itself be a silent gap in this
 * control (a copy this test can see but cannot exercise). */
function loadMaskFn(file) {
  const mod = require(file);
  assert.equal(
    typeof mod.buildQuoteMask,
    'function',
    `${path.relative(ROOT, file)} contains a buildQuoteMask() declaration but does not export it ` +
      '(or require.main === module short-circuited before the export ran) — this drift test ' +
      'cannot exercise it and that gap needs fixing before this control is trustworthy.'
  );
  return mod.buildQuoteMask;
}

// ---------------------------------------------------------------------------
// Corpus construction: each case is built from labelled true/false segments
// so the expected masked region is visible in the source next to the text
// that produces it, instead of being computed via separate index arithmetic
// that could itself be wrong.
// ---------------------------------------------------------------------------

function seg(text, masked) {
  return { text, mask: new Array(text.length).fill(masked) };
}

function build(...segs) {
  return {
    text: segs.map(s => s.text).join(''),
    mask: [].concat(...segs.map(s => s.mask)),
  };
}

// A tab character, spelled explicitly so the <<- corpus case below is
// unambiguous in source.
const TAB = '\t';

const GOLDEN_CASES = [
  {
    name: 'plain double-quoted region',
    ...build(seg('echo ', false), seg('"git commit"', true), seg(' done', false)),
  },
  {
    name: 'plain single-quoted region',
    ...build(seg('echo ', false), seg("'git commit'", true), seg(' done', false)),
  },
  {
    name: 'escaped quote INSIDE double quotes stays inside the same quoted region',
    // Shell text: echo "he said \"hi\" to git commit" done
    ...build(
      seg('echo ', false),
      seg('"he said \\"hi\\" to git commit"', true),
      seg(' done', false)
    ),
  },
  {
    name: 'BUG-077: backslash-escaped quote OUTSIDE quotes must not open a phantom region',
    // Shell text: echo \"git commit  (a literal backslash then a literal
    // quote, consumed as an escape pair — never opens `quote`).
    ...build(seg('echo \\"git commit', false)),
  },
  {
    name: 'BUG-043: "git commit" as prose inside a real quoted region stays masked',
    ...build(seg('note: ', false), seg('"...(git commit --author=x is the bypass)"', true)),
  },
  {
    name: 'BUG-078: a stray unbalanced quote inside a heredoc body does not leak past the terminator',
    ...build(
      seg('cat ', false),
      // <<EOF\n + "it's a test\n" (one stray, unbalanced quote) + "EOF\n"
      seg('<<EOF\n' + "it's a test\n" + 'EOF\n', true),
      seg('git commit -m ', false),
      seg('"x"', true),
      seg('\n', false)
    ),
  },
  {
    name: '<<- heredoc with a tab-indented terminator line is still recognised',
    ...build(
      seg('cat ', false),
      seg('<<-EOF\n' + TAB + 'body line\n' + TAB + 'EOF\n', true),
      seg('git commit\n', false)
    ),
  },
  {
    name: 'unterminated heredoc swallows to end of string (ASM-351, declared fail-safe)',
    ...build(seg('cat ', false), seg('<<EOF\nbody with no terminator anywhere in this string', true)),
  },
];

// BUG-081: CRLF-terminated heredoc. Deliberately NOT a golden case — see the
// header comment. Only cross-copy agreement is asserted for this one.
const CRLF_CASE = {
  name: 'BUG-081 (open, tracked separately): CRLF heredoc — copies must at least agree with each other',
  text:
    "cat <<'EOF'\r\n" +
    "stray quote in body: it's fine normally\r\n" +
    'EOF\r\n' +
    'git commit --author="Fake <fake@evil.com>" -m x\r\n',
};

// ---------------------------------------------------------------------------
// The comparison itself
// ---------------------------------------------------------------------------

function maskToString(mask) {
  return mask.map(b => (b ? '1' : '0')).join('');
}

function firstDivergingIndex(a, b) {
  const len = Math.max(a.length, b.length);
  for (let i = 0; i < len; i++) {
    if (a[i] !== b[i]) return i;
  }
  return -1;
}

test(`discovered ${discovered.length} copy/copies of buildQuoteMask: ${discovered
  .map(f => path.relative(ROOT, f))
  .join(', ')}`, () => {
  assert.ok(discovered.length > 0, 'discovery found zero copies — the pattern itself may have drifted');
});

for (const file of discovered) {
  const rel = path.relative(ROOT, file).replace(/\\/g, '/');

  for (const { name, text, mask: expected } of GOLDEN_CASES) {
    test(`golden: ${rel} :: ${name}`, () => {
      const fn = loadMaskFn(file);
      const actual = fn(text);
      if (firstDivergingIndex(actual, expected) !== -1 || actual.length !== expected.length) {
        const at = firstDivergingIndex(actual, expected);
        assert.fail(
          `${rel} diverges from the expected mask for "${name}" at index ${at}.\n` +
            `  text:     ${JSON.stringify(text)}\n` +
            `  expected: ${maskToString(expected)}\n` +
            `  actual:   ${maskToString(actual)}`
        );
      }
    });
  }
}

test(`cross-copy agreement: ${CRLF_CASE.name}`, () => {
  if (discovered.length < 2) return; // nothing to compare
  const results = discovered.map(file => ({ file, mask: loadMaskFn(file)(CRLF_CASE.text) }));
  const [reference, ...rest] = results;
  for (const other of rest) {
    const at = firstDivergingIndex(reference.mask, other.mask);
    if (at !== -1 || reference.mask.length !== other.mask.length) {
      assert.fail(
        `${path.relative(ROOT, other.file)} disagrees with ` +
          `${path.relative(ROOT, reference.file)} on the CRLF-heredoc case at index ${at} ` +
          '(BUG-081 is open against this exact construct — divergence here means one copy has ' +
          'been fixed and the others have not, which is precisely what this test exists to catch).\n' +
          `  text:                        ${JSON.stringify(CRLF_CASE.text)}\n` +
          `  ${path.relative(ROOT, reference.file)}: ${maskToString(reference.mask)}\n` +
          `  ${path.relative(ROOT, other.file)}: ${maskToString(other.mask)}`
      );
    }
  }
});

// Belt-and-braces: even for the golden cases, also assert direct cross-copy
// agreement (not just "each copy matches golden"), so a bug in the golden
// fixture itself (all copies agree with each other but not with a mistaken
// golden expectation) is distinguishable in the failure output from a real
// divergence between copies.
for (const { name, text } of GOLDEN_CASES) {
  test(`cross-copy agreement: ${name}`, () => {
    if (discovered.length < 2) return;
    const results = discovered.map(file => ({ file, mask: loadMaskFn(file)(text) }));
    const [reference, ...rest] = results;
    for (const other of rest) {
      const at = firstDivergingIndex(reference.mask, other.mask);
      if (at !== -1 || reference.mask.length !== other.mask.length) {
        assert.fail(
          `${path.relative(ROOT, other.file)} disagrees with ` +
            `${path.relative(ROOT, reference.file)} on "${name}" at index ${at}.\n` +
            `  text: ${JSON.stringify(text)}\n` +
            `  ${path.relative(ROOT, reference.file)}: ${maskToString(reference.mask)}\n` +
            `  ${path.relative(ROOT, other.file)}: ${maskToString(other.mask)}`
        );
      }
    }
  });
}
