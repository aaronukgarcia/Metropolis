// Module key: tool.codenamepatterns (see code.json; GUID 31c3389f-87b1-43b1-9cd4-fd711016ef44)
// Spec ref: GR#22

/**
 * claude-codename-patterns.js — shared GR#22 forbidden-pattern source
 * (FEAT-046 / BOW mkey: tool.codenameguard).
 *
 * Extracted out of claude-codename-guard.js (the PreToolUse advisory guard)
 * so it can be require()'d, UNCHANGED, by BOTH that guard AND the new
 * enforcing `commit-msg` content scan (claude-codename-content-scan.js,
 * wired into githooks/commit-msg). This is the ENTIRE point of this file's
 * existence (GR#3 — Single Source of Truth): one pattern set, one
 * fragment-assembly technique, one boundary-classification rule, so a
 * pattern added to catch a new bypass cannot silently apply to only one
 * layer and a future edit to one copy and not the other cannot happen — there
 * is only one copy.
 *
 * THE LOGIC ITSELF IS UNCHANGED FROM claude-codename-guard.js's PRE-EXTRACTION
 * VERSION. This file is a MOVE, not a REWRITE (see BUG-123/137/140/144 in
 * that file's git history before touching this — GR#6). Do not "improve" the
 * pattern set or boundary logic here without a fresh BOW item.
 *
 * WHY THE PATTERNS ARE ASSEMBLED FROM FRAGMENTS BELOW, which looks like
 * obfuscation and is not: this file lives IN git. If it contained the
 * forbidden strings as plain literals in order to search for them, this
 * module would be the single largest violation of the rule it exists to
 * enforce. Fragments are joined at runtime so no forbidden literal ever
 * appears on disk. See claude-codename-guard.js's own header for the fuller
 * GR#22 rationale (reference title, why it's checked mechanically, etc.) —
 * this file only owns the pattern DATA and the scan MECHANISM, not the
 * policy prose.
 */

'use strict';

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

// Assembled as a function, not a top-level const, purely to keep the two
// place-name fragments ("san" + "franci"+"sco") from ever sitting next to
// each other as a single joined literal anywhere in this file's source.
function SAN_FRAN() {
  return `san${SEP}${FRAN}sco`;
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

// BUG-140/BUG-144: patterns marked `boundary: true` carry no regex anchor at
// all — every candidate match is found via a global exec loop, then accepted
// if EITHER neighbour is a real boundary (case transition, digit, underscore,
// punctuation, or string edge — i.e. NOT a plain lowercase letter). Only a
// match embedded on BOTH sides in a continuing all-lowercase run is rejected,
// since that's the one case genuinely indistinguishable from ordinary prose.
// BUG-140's original fix used AND semantics (require BOTH sides boundary),
// which missed the camelCase middle-segment case (a forbidden word appearing
// mid-identifier, e.g. prefixWordEngine-shaped): the lowercase-adjacent left
// side was enough to reject it even though the right side had an unambiguous
// uppercase transition.
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

/** Regex for npm package-lock.json integrity-hash entries: exactly
 * "integrity": "sha<digits>-<base64>", with optional trailing comma.
 * These are machine-generated base64 hashes (sha256/sha512). Base64's alphabet
 * includes readable letters/digits, so the forbidden token can itself be valid
 * base64 — the shape check ALONE is insufficient to exempt it safely from
 * scanning. Only the guard (which has file path context) can safely skip these
 * lines, and ONLY in actual lockfile basenames (package-lock.json,
 * npm-shrinkwrap.json, yarn.lock, pnpm-lock.yaml). Exported for the guard's
 * per-file filtering logic.
 * BUG-416: skip decision is moved to claude-codename-guard.js (per-file scope);
 * scan() is kept honest by default (no skips).
 */
const NPM_INTEGRITY_HASH_RE = /^\s*"integrity":\s*"sha\d+-[A-Za-z0-9+/=_-]+"\s*,?\s*$/;

function isNpmIntegrityHashLine(line) {
  return NPM_INTEGRITY_HASH_RE.test(line);
}

/** Scans `text` (split into lines) against every pattern in PATTERNS, pushing
 * one human-readable hit description per (pattern, first-matching-line) onto
 * `hits`. `where` names the surface being scanned for the hit description
 * (e.g. "staged content (added lines)"). This is the ONE scan entry point
 * both consumers call — neither consumer reimplements matching.
 *
 * BUG-416: scan() makes NO file-specific exceptions (no integrity-hash skip).
 * It is kept honest by default. The guard and content-scan callers have file
 * context and must apply their own filtering before calling scan(). */
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

module.exports = {
  PATTERNS,
  isLowerLetter,
  lineMatches,
  lineMatchesWithBoundary,
  scan,
  NPM_INTEGRITY_HASH_RE,
  isNpmIntegrityHashLine,
};
