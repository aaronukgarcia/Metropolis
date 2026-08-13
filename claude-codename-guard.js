/**
 * PreToolUse hook — codename guard (BOW mkey: tool.codenameguard).
 *
 * Enforces GOLDEN RULE #22 (Codename Discipline): the reference title this
 * project's design docs compare against is 'Blue', and only 'Blue'. Its real
 * name, its abbreviations and its numbered sequel form must never be written
 * into git — not in code, data, docs, plans, comments, commit messages, or
 * branch names.
 *
 * WHY MECHANICAL. The repo is intended to go public. A name written into git
 * is a disclosure that cannot be withdrawn afterwards: clones, caches and
 * indexers outlive any later edit, which is exactly why the existing
 * occurrences were removed by rewriting history rather than by editing the
 * working tree. A rule that depends on everyone remembering it, across a
 * dozen concurrent agents, will be broken — so it is checked instead.
 *
 * WHY THE PATTERNS ARE ASSEMBLED FROM FRAGMENTS BELOW, which looks like
 * obfuscation and is not: this file lives IN git. If it contained the
 * forbidden strings as plain literals in order to search for them, the guard
 * would be the single largest violation of the rule it enforces — and it
 * would flag itself on every commit. The same trap catches a well-meaning
 * comment explaining a rename ("renamed <real name> to 'Blue'"), which is why
 * the rule covers comments and commit messages and not just code. Fragments
 * are joined at runtime so no forbidden literal ever appears on disk.
 *
 * WHAT IT CHECKS, on `git commit` and `git push`:
 *   1. Staged content (git diff --cached), added lines only — an existing
 *      violation elsewhere in a file must not block an unrelated fix.
 *   2. The commit message, including -m arguments and heredoc bodies. This is
 *      the likeliest place to slip: the message describing the removal is the
 *      easiest thing to write the name into.
 *   3. The current branch name.
 *
 * Ambiguity is handled deliberately. The bare two-letter abbreviation is NOT
 * matched: it appears innocently in ordinary technical prose, and a guard that
 * fires on false positives gets disabled within a day — a failure mode this
 * project has now catalogued three times (SEC-026, and twice since). The
 * numbered forms ARE matched, since those are unambiguous.
 *
 * Fail-CLOSED, like claude-plan-guard.js and unlike claude-dispatch-guard.js.
 * The cost asymmetry decides it: a false block is a minor annoyance that a
 * human resolves in seconds, while a miss is permanent and public. If this
 * guard cannot do its job it must not pretend the commit is clean.
 *
 * Deliberate disable: CLAUDE_DISABLE_CODENAME_GUARD=1. Use it to commit a
 * genuine false positive, never to push a real one.
 *
 * Receives JSON on stdin: { tool: "Bash", tool_input: { command: "..." } }
 */

'use strict';

const { execSync } = require('child_process');
const fs = require('fs');
const { buildBareGitVerbTriggerRegex } = require('./claude-git-commit-trigger.js');

// BUG-123 (2026-08-12): this guard's trigger used to be the bare
// `/\bgit\s+(commit|push)\b/`, which does not tolerate ANY global option
// between `git` and the verb — so `git -c user.email=... commit` (or
// `-c commit.gpgsign=false`, or `--git-dir=...`) slipped past this
// FAIL-CLOSED GR#22 guard entirely, unscanned. Built from the same shared
// option-run grammar the sibling commit-only guards now use (GR#3 — see
// claude-git-commit-trigger.js's header); this guard keeps its original bare
// word-boundary shape (no shell-boundary anchoring), unchanged in every other
// respect.
const GIT_COMMIT_OR_PUSH_RE = buildBareGitVerbTriggerRegex('commit|push');

// Fragments, never joined in source. See the header for why.
const SKY = 'sky';
const LINES = 'lines';
const CITY = 'cit';
const IES = '(?:y|ies)';

// Fragments for the expansion-content pack names (FEAT-037, follow-on to
// ASM-150): this combination of pack names, taken together, is as much a
// fingerprint of the reference title as the title itself — see the ASM
// logged against foundation.data for the ruling. Same discipline as above:
// each word is split so no forbidden literal sits whole in this file, and a
// flexible separator regex absorbs "&" vs "and" and hyphenation variants.
const SEP = '[\\s:_-]*';
const AMP = '(?:&|and)';
const BRIDG = 'bridg';
const PORT = 'por';
const BEA = 'bea';
const PROPERT = 'propert';
const URB = 'urb';
const PROMENAD = 'promenad';
const STAT = 'stat';
const OFFIC = 'offic';
const EVOLUT = 'evolut';
const FRAN = 'franci';
const SCRAP = 'scrap';
const MODER = 'moder';
const ARCHITECT = 'architect';

// BUG-140: ordinary regex \b is a \w/non-\w transition, and underscore is a
// \w character — so \bword\b silently fails to match "word_export" (snake_
// case). A lookaround anchor built from a plain [a-zA-Z] class has a second
// problem: these patterns carry the 'i' (case-insensitive) flag, which folds
// case for the WHOLE regex including lookarounds — so [a-zA-Z] can't tell
// "still lowercase" from "just turned uppercase", which is exactly the signal
// needed to also catch camelCase (word immediately followed by a capital,
// e.g. "wordConfig" — no separator at all). JS has no scoped/inline
// case-insensitivity modifier, so the boundary check below is done in plain
// JS instead of regex: `boundary: true` patterns are matched WITHOUT anchors
// (via a global exec loop in scan()), and a match only counts as a real hit
// if the character immediately before/after it is not a literal lowercase
// letter — i.e. an adjacent digit, underscore, uppercase letter (camelCase
// transition), punctuation, whitespace, or string edge all count as a
// boundary; only continuing directly into another lowercase letter (embedded
// inside one longer all-lowercase run) does not. This still declines to fire
// in the middle of an ordinary lowercase word, matching \b's original
// false-positive-avoidance intent, just letter-case-aware instead of
// \w-based.
function isLowerLetter(ch) {
  return ch !== undefined && ch >= 'a' && ch <= 'z';
}

// Built at runtime. Matches the two-word title with any separator, the
// single distinctive word on its own, and the numbered abbreviations.
const PATTERNS = [
  {
    re: new RegExp(`${CITY}${IES}[\\s:_-]*${SKY}${LINES}`, 'gi'),
    what: 'the full reference title',
  },
  {
    re: new RegExp(`${SKY}${LINES}`, 'gi'),
    what: 'the distinctive single word from the reference title',
    boundary: true,
  },
  {
    re: /CS ?[12]/g,
    what: 'a numbered abbreviation of the reference title',
    boundary: true,
  },
  {
    re: new RegExp(`${BRIDG}es${SEP}(?:${AMP}${SEP})?${PORT}ts`, 'gi'),
    what: 'a former expansion-content pack name',
    boundary: true,
  },
  {
    re: new RegExp(`${BEA}ch${SEP}${PROPERT}ies`, 'gi'),
    what: 'a former expansion-content pack name',
    boundary: true,
  },
  {
    re: new RegExp(`${URB}an${SEP}${PROMENAD}e`, 'gi'),
    what: 'a former expansion-content pack name',
    boundary: true,
  },
  {
    re: new RegExp(`${CITY}y${SEP}${STAT}ions`, 'gi'),
    what: 'a former expansion-content pack name',
    boundary: true,
  },
  {
    re: new RegExp(`${OFFIC}e${SEP}${EVOLUT}ion`, 'gi'),
    what: 'a former expansion-content pack name',
    boundary: true,
  },
  {
    re: new RegExp(`${SAN_FRAN()}${SEP}set`, 'gi'),
    what: 'a former expansion-content pack name',
    boundary: true,
  },
  {
    re: new RegExp(`${SKY}${SCRAP}ers`, 'gi'),
    what: 'a former expansion-content pack name',
    boundary: true,
  },
  {
    re: new RegExp(`${MODER}n${SEP}${ARCHITECT}ure`, 'gi'),
    what: 'a former expansion-content pack name',
    boundary: true,
  },
];

// Assembled as a function, not a top-level const, purely to keep the two
// place-name fragments ("san" + "franci"+"sco") from ever sitting next to
// each other as a single joined literal anywhere in this file's source.
function SAN_FRAN() {
  return `san${SEP}${FRAN}sco`;
}

function allow() {
  process.exit(0);
}

function deny(reason) {
  process.stdout.write(
    JSON.stringify({
      hookSpecificOutput: {
        hookEventName: 'PreToolUse',
        permissionDecision: 'deny',
        permissionDecisionReason: reason,
      },
    })
  );
  process.exit(0);
}

// BUG-140/BUG-144: patterns marked `boundary: true` carry no regex anchor at
// all — every candidate match is found via a global exec loop, then accepted
// if EITHER neighbour is a real boundary (case transition, digit, underscore,
// punctuation, or string edge — i.e. NOT a plain lowercase letter). Only a
// match embedded on BOTH sides in a continuing all-lowercase run is rejected,
// since that's the one case genuinely indistinguishable from ordinary prose.
// BUG-140's original fix used AND semantics (require BOTH sides boundary),
// which missed the camelCase middle-segment case (a forbidden word appearing
// mid-identifier, e.g. prefixWordEngine-shaped): the
// lowercase-adjacent left side was enough to reject it even though the right
// side had an unambiguous uppercase transition. See the PATTERNS comment
// above for why this can't be done as a regex lookaround once the 'i' flag
// is in play.
function lineMatches(re, line) {
  re.lastIndex = 0;
  let m;
  while ((m = re.exec(line))) {
    return true; // caller already filtered to boundary:false patterns here
  }
  return false;
}

function lineMatchesWithBoundary(re, line) {
  re.lastIndex = 0;
  let m;
  while ((m = re.exec(line))) {
    const before = m.index > 0 ? line[m.index - 1] : undefined;
    const after = m.index + m[0].length < line.length ? line[m.index + m[0].length] : undefined;
    if (!isLowerLetter(before) || !isLowerLetter(after)) return true;
    if (re.lastIndex === m.index) re.lastIndex += 1; // guard against zero-length matches
  }
  return false;
}

function scan(text, where, hits) {
  if (!text) return;
  const lines = String(text).split(/\r?\n/);
  for (const p of PATTERNS) {
    for (let i = 0; i < lines.length; i++) {
      const hit = p.boundary
        ? lineMatchesWithBoundary(p.re, lines[i])
        : lineMatches(p.re, lines[i]);
      if (!hit) continue;
      hits.push(
        `${where}${lines.length > 1 ? ` (line ${i + 1})` : ''}: contains ${p.what}.`
      );
      break; // One report per pattern per location is enough to act on.
    }
  }
}

function main() {
  if (process.env.CLAUDE_DISABLE_CODENAME_GUARD === '1') allow();

  let payload;
  try {
    payload = JSON.parse(fs.readFileSync(0, 'utf8') || '{}');
  } catch {
    // Unparsable hook input is not evidence of a clean commit, but it is also
    // not this guard's call to make — the shell will fail on its own.
    allow();
  }

  const cmd = String((payload.tool_input || {}).command || '');
  if (!GIT_COMMIT_OR_PUSH_RE.test(cmd)) allow();

  const hits = [];

  // 2 & 3 first: they need no subprocess and cover the likeliest slip.
  scan(cmd, 'the git command (message text or arguments)', hits);

  try {
    const branch = execSync('git rev-parse --abbrev-ref HEAD', {
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'ignore'],
    }).trim();
    scan(branch, `the branch name "${branch}"`, hits);
  } catch {
    /* detached HEAD or no commits yet — nothing to check */
  }

  // 1: staged ADDED lines only. Scanning whole files would block an unrelated
  // fix in a file that still carries an occurrence somewhere else.
  try {
    const diff = execSync('git diff --cached --unified=0', {
      encoding: 'utf8',
      maxBuffer: 64 * 1024 * 1024,
      stdio: ['ignore', 'pipe', 'ignore'],
    });
    const diffLines = diff.split(/\r?\n/);
    const added = diffLines
      .filter((l) => l.startsWith('+') && !l.startsWith('+++'))
      .join('\n');
    scan(added, 'staged content (added lines)', hits);

    // BUG-137: a forbidden word appearing ONLY in a new/renamed/copied file's
    // PATH — never in file content or the commit message — bypassed the
    // content-only scan above, since a unified diff's path-header lines
    // ('+++ b/<path>', '--- a/<path>', 'rename to/from <path>', 'copy
    // to/from <path>') all start with something other than a plain '+' and
    // were excluded outright rather than stripped and scanned.
    const PATH_HEADER_RE = /^(\+\+\+ |--- |rename to |rename from |copy to |copy from )/;
    const paths = diffLines
      .filter((l) => PATH_HEADER_RE.test(l))
      .map((l) => l.replace(PATH_HEADER_RE, ''))
      .join('\n');
    scan(paths, 'staged file path (new, renamed, or copied file)', hits);
  } catch (err) {
    deny(
      `🛑 CODENAME GUARD (GR#22): could not read the staged diff to check it ` +
        `— ${err.message}\n\nFailing closed: an unchecked commit is not a clean ` +
        `one, and a name written into git cannot be withdrawn once the repo is ` +
        `public. Resolve the git error and retry.`
    );
  }

  if (hits.length) {
    deny(
      `🛑 CODENAME GUARD — GOLDEN RULE #22 violation (${hits.length}):\n\n` +
        hits.map((h) => `  - ${h}`).join('\n') +
        `\n\nThe reference title is 'Blue', and only 'Blue'. Its real name, its ` +
        `abbreviations and its numbered form must never enter git — code, data, ` +
        `docs, comments, commit messages or branch names.\n\n` +
        `Rewrite to say 'Blue' or "the reference title". Where a sentence only ` +
        `reads sensibly with the real name, rewrite the sentence: the reference ` +
        `is being renamed, not deleted, so keep the technical point.\n\n` +
        `Note the trap this guard exists to catch — do NOT write a commit ` +
        `message or comment EXPLAINING the rename that quotes the old name. ` +
        `The explanation would itself be the exposure.\n\n` +
        `Deliberate bypass (genuine false positive only): ` +
        `CLAUDE_DISABLE_CODENAME_GUARD=1`
    );
  }

  allow();
}

// require.main === module guard (BUG-123, same testability pattern already
// used by claude-secret-guard.js / claude-version-guard.js /
// claude-plan-guard.js): when run directly as the hook, behaviour is
// unchanged — main() still runs unconditionally below. When require()'d by a
// test harness, main() is never called (so stdin is never touched) and the
// trigger regex is exported for direct, unit-level testing.
if (require.main === module) {
try {
  main();
} catch (err) {
  // Fail closed — see the header on cost asymmetry.
  deny(
    `🛑 CODENAME GUARD (GR#22) internal error: ${err.message}\n\n` +
      `Failing closed deliberately. Bypass only if you have checked by hand: ` +
      `CLAUDE_DISABLE_CODENAME_GUARD=1`
  );
}
} else {
  module.exports = {
    GIT_COMMIT_OR_PUSH_RE,
    // BUG-061 (tool.bow `redact` subcommand): exported so claude-bow.js can
    // reuse this guard's own fragment-assembled pattern set and boundary
    // logic verbatim (GR#3 single source of truth) instead of re-deriving a
    // second copy of the forbidden-pattern list — a drifted second copy is
    // exactly the kind of gap that would let a real name back into the BOW
    // even after this guard blocks it from reaching git. Nothing here adds a
    // new literal to this file: PATTERNS is still fragment-assembled above,
    // isLowerLetter is the same boundary-classification helper the guard's
    // own scan() uses.
    PATTERNS,
    isLowerLetter,
  };
}
