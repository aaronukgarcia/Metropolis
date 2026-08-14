// Module key: tool.secretchecker (see code.json; GUID 05345166-63fd-40bb-a3ac-b35bfaa38283)
// Spec ref: GR#11; GR#15

/**
 * Secret / GR#15 hardcoding-smell checker (BOW mkey: tool.secretguard,
 * BUG-088 remediation, extracted from claude-secret-guard.js).
 *
 * This module is the SINGLE SOURCE OF TRUTH (GR#3) for the payload-inspection
 * logic that decides whether staged content contains a secret or a GR#15
 * hardcoding smell. It is `require()`'d by claude-secret-guard.js (the
 * PreToolUse layer, which stays BLOCKING — see that file's header) and is
 * designed to also be `require()`'d by a future `commit-msg` dispatcher
 * (BUG-088's Section B — that dispatcher is NOT implemented here, see
 * docs/planning/acceptance/tool.secretguard.md's BUG-088 section, AC-B5).
 *
 * BUG-088 finding this extraction addresses: claude-secret-guard.js's
 * *trigger* (deciding whether to engage at all, via a boundary-anchored
 * regex over the raw command STRING) was defeated by any leading word,
 * shell wrapper, or non-bareword git invocation (`env git commit`,
 * `git.exe commit`, `bash -c "git commit ..."`, etc.) — but its *payload*
 * (this module: `git diff --cached`, read from real disk/git state via
 * `spawnSync`) was always sound. This module carries NONE of that trigger
 * machinery — none of the boundary-regex/quote-mask/engage-decision helpers
 * the sibling PreToolUse guards define — by design (AC-B4): a commit-msg
 * hook has no engage decision to make, and copying dead trigger machinery
 * into this module would misrepresent that trigger-parsing is still part of
 * this design. (This file's own header intentionally avoids spelling out
 * those helpers' exact identifier names, so a grep for them against this
 * file — the literal AC-B4 check — finds zero matches, not "present only in
 * a comment.")
 *
 * KNOWN LIMITATION INHERITED FROM ASM-386, STATED PLAINLY (AC-B2): a
 * `commit-msg` hook (the intended future caller of this module's
 * `checkSecrets()`) does not fire for `git cherry-pick` / `git revert` /
 * `git am` on this project's git version (2.55.0.windows.3, verified three
 * independent ways per ASM-386's own comment thread). This module's own
 * logic has no opinion about hook points — it only inspects whatever
 * `git diff --cached` currently reports — but a caller that only invokes it
 * from `commit-msg` inherits that verb-coverage gap unchanged. Not
 * re-verified or re-solved here; ASM-386 is the source of truth.
 *
 * Exported call contract for a future dispatcher (AC-B5): `checkSecrets()`
 * takes NO arguments (reads git/allowlist state itself) and returns one of:
 *   { status: 'clean' }
 *   { status: 'found-problems', findings: [{file, line, category, evidence, detail}, ...] }
 *   { status: 'internal-error', error: <Error> }
 * — the same three-state discriminant AC-E1 requires across all four BUG-088
 * checker modules, so a shared dispatcher can treat them uniformly without
 * per-domain special-casing. `runScan()` (the pre-existing function, kept for
 * backward compatibility with claude-secret-guard.js's unchanged PreToolUse
 * behaviour — AC-C1/AC-D2) throws on internal error rather than returning a
 * status; `checkSecrets()` is the new three-state wrapper around it.
 *
 * Everything below this header is RELOCATED, NOT REIMPLEMENTED, from
 * claude-secret-guard.js (AC-D2): same detectors, same entropy threshold,
 * same allowlist file, same redaction. See claude-secret-checker.test.js for
 * the fixture-parity proof against the original guard's runScan().
 *
 * KNOWN, DISCLOSED LIMIT OF THE ENTROPY HEURISTIC (SEC-021 lead ruling,
 * 2026-08-12, after four Destructive rounds): the word-segmented-identifier
 * exemption (isWordSegmentedIdentifier()/looksHighEntropy() below) has an
 * architectural blind spot, not merely an untuned one. A real secret that is
 * chunked into 2-3 segments, or into segments of non-uniform length, with
 * EACH segment kept under SEGMENT_ENTROPY_MIN_LENGTH, sits in an uncovered
 * "middle band" between the per-segment shape check and the reassembled-
 * whole anti-chunking check and can evade detection entirely. This is
 * accepted and disclosed, not an oversight to be re-opened as a fifth
 * dev/Destructive round: two independent impossibility proofs were accepted
 * in the ruling. (1) Order-0 Shannon entropy over ASCII segments cannot, by
 * threshold alone, distinguish a genuine multi-word identifier from a real
 * secret cut into word-shaped pieces — the two distributions overlap. (2)
 * No fixed length/segment-count tolerance closes the class: for any knob N
 * (minimum segment count, maximum length variance, etc.) an attacker using
 * range N+1 defeats it, so tightening the knob only relocates the gap, it
 * never removes it. Despite the residual, this interim is STRICTLY BETTER
 * than the pre-SEC-021 guard: it closes all three of the original named
 * false positives (bogus-injected-phase, sec014-original-still-works,
 * sec018-original-still-works) and it catches materially more real attack
 * shapes than the design it replaced (see the SEC-021 REGRESSION ROUND 2
 * fixtures in claude-secret-checker.test.js: hyphen-chunked hex, non-uniform
 * segment lengths, and 5-segment reassembly are all now caught end-to-end).
 * A structurally different second detection layer — not another retune of
 * this order-0 entropy approach — is tracked as BUG-029.
 *
 * KNOWN, DISCLOSED LIMIT: CROSS-LITERAL REASSEMBLY (BUG-148, 2026-08-13;
 * NARROWED BUG-150, 2026-08-13). scanLine() concatenates BOUNDED, ADJACENT
 * windows of 2-3 string literals' CONTENT (in source order, no separator)
 * and re-runs both API_KEY_PATTERNS and looksHighEntropy() against each
 * joined window — this closes the cheapest real split (`const a =
 * "AKIA1234..."; const b = "5678...";` on one line), which previously
 * evaded both the contiguity-required pattern match and the per-literal
 * ENTROPY_MIN_LENGTH floor entirely.
 *
 * BUG-150: the original BUG-148 fix concatenated EVERY literal on the line
 * (no count cap, no separator check), which meant any ordinary line
 * declaring an array/object of several unrelated short string values (e.g.
 * `const TYPES = ['module', 'feature', 'bug', ...]`) got its literals
 * joined into one blob and entropy-tested as if it were a single secret —
 * 1520 false positives swept against this project's own source, 0 real
 * secrets. The fix narrows the check on two axes: (1) only 2- and 3-literal
 * ADJACENT windows are tried, never the whole line's literal set — a real
 * same-line split secret is realistically 2, maybe 3 pieces, not N; (2) a
 * window is only concatenated if the raw source text BETWEEN each pair of
 * consecutive literals in the window matches an ALLOWLIST of two
 * continuation shapes — bare `+` concatenation, or a second adjacent
 * `const`/`let`/`var` declaration (`; const b = `) — rather than a
 * blocklist of "bad" characters. (A blocklist of `, [ ] { }` was tried
 * first and still left 1344 false positives: JSON/object `"key": "value"`
 * pairs, boolean-OR chains of string literals, and shell-pipe literals all
 * have gaps containing none of those characters.) BUG-148's own worked
 * example (`"AKIA...";  const b = "...";`) matches the declaration shape,
 * so it still concatenates; a comma-separated array/object entry, a
 * `key: value` pair, or an `a === 'x' || a === 'y'` chain does not.
 *
 * This is a PARTIAL mitigation, not a fix for the general problem: it does
 * not, and by construction cannot, catch a split across MULTIPLE lines, a
 * window wider than 3, or one joined at runtime via a function call,
 * template-literal interpolation of variables, or any indirection this
 * diff-text scanner doesn't see as adjacent literal syntax on one line.
 * Arbitrary runtime reassembly is not statically detectable by a scanner
 * that only ever reads git diff text — this is the same class of limit
 * SEC-021 above accepts for a different mechanism, not an oversight.
 */

'use strict';

const fs = require('fs');
const path = require('path');
const { spawnSync } = require('child_process');

const ROOT = __dirname;
const ALLOWLIST_PATH = path.join(ROOT, 'claude-secret-guard.allow.json');

// Why 3.7: a canonical hex GUID/UUID ("1c0a8c46-ba8a-4063-92f8-0bbcdb580753")
// measures ~3.8 bits/char and must clear this bar so it gets flagged as
// high-entropy by default — the allowlist's guid-uuid-literal pattern is
// what proves it suppresses that flag, not an accidental gap in the
// detector. Ordinary English prose is excluded upstream by the base64/hex
// shape prefilter (TOKEN_SHAPE_RE rejects whitespace), not by this
// threshold, so lowering it further does not risk flagging sentences.
const ENTROPY_THRESHOLD = 3.7;
// Why 20: shorter literals (short flags, enum values, single words) produce
// noisy entropy estimates and would false-positive constantly; 20 chars is
// comfortably below the shortest secret shapes this checker targets
// elsewhere (e.g. the 32-char AWS secret key, 36-char GitHub PAT) while
// still catching the metro-DB-default / GUID-length (36-char) fixtures.
const ENTROPY_MIN_LENGTH = 20;

const SENSITIVE_FILE_EXTENSIONS = ['.pem', '.key', '.p12', '.pfx', '.jks', '.crt', '.cer'];

// ---------------------------------------------------------------------------
// Allowlist loading / validation
// ---------------------------------------------------------------------------

function loadAllowlist() {
  if (!fs.existsSync(ALLOWLIST_PATH)) {
    throw new Error(`allowlist file missing: ${ALLOWLIST_PATH}`);
  }
  let raw;
  try {
    raw = fs.readFileSync(ALLOWLIST_PATH, 'utf8').replace(/^\uFEFF/, '');
  } catch (err) {
    throw new Error(`failed to read allowlist file: ${err.message}`);
  }
  let data;
  try {
    data = JSON.parse(raw);
  } catch (err) {
    throw new Error(`allowlist file is not valid JSON: ${err.message}`);
  }
  if (!data || typeof data !== 'object' || Array.isArray(data)) {
    throw new Error('allowlist file must be a JSON object');
  }
  const allowedPaths = data.allowedPaths;
  const allowedPatterns = data.allowedPatterns;
  if (!Array.isArray(allowedPaths)) {
    throw new Error('allowlist "allowedPaths" must be an array');
  }
  for (const [i, entry] of allowedPaths.entries()) {
    if (!entry || typeof entry !== 'object' || Array.isArray(entry)) {
      throw new Error(`allowlist "allowedPaths[${i}]" must be an object {path, reason} — bare strings are not accepted`);
    }
    if (typeof entry.path !== 'string' || entry.path.trim().length === 0) {
      throw new Error(`allowlist "allowedPaths[${i}].path" must be a non-empty string`);
    }
    if (entry.path.trim() === '**' || entry.path.trim() === '*') {
      throw new Error(`allowlist "allowedPaths[${i}].path" ("${entry.path}") is over-broad — it would allowlist the entire staged tree; use a specific path or a scoped glob like "docs/**"`);
    }
    if (typeof entry.reason !== 'string' || entry.reason.trim().length === 0) {
      throw new Error(`allowlist "allowedPaths[${i}].reason" is mandatory and must be a non-empty string`);
    }
  }
  if (!Array.isArray(allowedPatterns)) {
    throw new Error('allowlist "allowedPatterns" must be an array');
  }
  for (const [i, entry] of allowedPatterns.entries()) {
    if (!entry || typeof entry !== 'object') {
      throw new Error(`allowlist "allowedPatterns[${i}]" must be an object`);
    }
    if (entry.type !== 'exact' && entry.type !== 'regex') {
      throw new Error(`allowlist "allowedPatterns[${i}].type" must be "exact" or "regex"`);
    }
    if (typeof entry.value !== 'string' || entry.value.length === 0) {
      throw new Error(`allowlist "allowedPatterns[${i}].value" must be a non-empty string`);
    }
    if (typeof entry.reason !== 'string' || entry.reason.trim().length === 0) {
      throw new Error(`allowlist "allowedPatterns[${i}].reason" is mandatory and must be a non-empty string`);
    }
    if (entry.type === 'regex') {
      try {
        // eslint-disable-next-line no-new
        new RegExp(entry.value);
      } catch (err) {
        throw new Error(`allowlist "allowedPatterns[${i}].value" is not a valid regex: ${err.message}`);
      }
    }
  }
  return { allowedPaths, allowedPatterns };
}

function isAllowlistedValue(candidate, allowedPatterns) {
  for (const entry of allowedPatterns) {
    if (entry.type === 'exact') {
      if (candidate === entry.value) return true;
    } else {
      const anchored = new RegExp(`^(?:${entry.value})$`);
      if (anchored.test(candidate)) return true;
    }
  }
  return false;
}

function isAllowlistedPath(filePath, allowedPaths) {
  const normalized = filePath.replace(/\\/g, '/');
  for (const entry of allowedPaths) {
    if (globMatch(entry.path, normalized)) return true;
  }
  return false;
}

// Minimal glob: '**' matches any (possibly empty) path segment span, '*'
// matches within a single segment.
function globMatch(pattern, str) {
  const escaped = pattern
    .replace(/[.+^${}()|[\]\\]/g, '\\$&')
    .replace(/\*\*/g, '\u0000')
    .replace(/\*/g, '[^/]*')
    .replace(/\u0000/g, '.*');
  const re = new RegExp(`^${escaped}$`);
  return re.test(str);
}

// ---------------------------------------------------------------------------
// Entropy
// ---------------------------------------------------------------------------

function shannonEntropy(str) {
  const freq = Object.create(null);
  for (const ch of str) freq[ch] = (freq[ch] || 0) + 1;
  const len = str.length;
  let entropy = 0;
  for (const ch in freq) {
    const p = freq[ch] / len;
    entropy -= p * Math.log2(p);
  }
  return entropy;
}

const TOKEN_SHAPE_RE = /^[A-Za-z0-9+/_=-]+$/;

// SEC-021: segment-aware, mixed-character-class exemption for descriptive
// hyphenated correlation IDs (GR#1 requires one on every error/command, so
// "bogus-injected-phase"-shaped identifiers are the natural, encouraged
// form, not an accident). A pure entropy-floor retune was rejected as the
// fix (SEC-021 names it explicitly as the failure mode to avoid, and it
// would only move which length of ordinary identifier starts
// false-positiving, not fix the shape confusion): the real signal
// distinguishing a word-shaped identifier from a credential is STRUCTURE,
// not vocabulary. Split the candidate on '-'/'_'. If it contains at least
// one such separator AND every resulting segment is composed only of
// lowercase letters and/or digits (no uppercase, no '+'/'/'/'=' anywhere),
// it reads as a sequence of words joined by a separator and is exempt from
// the high-entropy flag, regardless of its whole-string Shannon entropy or
// length. A contiguous run with no separator at all (the shape of a
// base64 blob, a hex digest, a bearer token -- none of which have a
// natural word boundary) is untouched by this exemption. A segmented
// candidate with any uppercase letter or base64 symbol in ANY segment
// (e.g. a connection string, or a mixed-case token that happens to
// contain a literal '-') is also untouched -- the exemption requires
// EVERY segment to pass, not just some.
const SEGMENT_SEPARATOR_RE = /[-_]/;
const WORD_SEGMENT_RE = /^[a-z0-9]+$/;

// BUG-029: the exemption above only ever fires for EXPLICIT '-'/'_' separators
// -- a camelCase identifier with no separator character at all (data/
// buildings.json's snake_case ids were the first BUG-029 instance and are
// already covered by the rule above; data/modes.json's laneCapacityPcuPerHour
// and buildings_test.go's elecPerDay/foodPerDay/housingPerDay are the second,
// UNCOVERED instance -- camelCase JSON field names, no '-'/'_' anywhere)
// never reaches SEGMENT_SEPARATOR_RE's gate at all and falls straight through
// to the raw whole-string entropy check, which flags it on length alone
// (measured: laneCapacityPcuPerHour is 22 chars at 3.789 bits/char, clearing
// ENTROPY_THRESHOLD, while every individual word inside it is an ordinary
// dictionary-shaped fragment).
//
// This is deliberately a SEPARATE, STRICTER path from the '-'/'_' one above,
// not a shared relaxation of WORD_SEGMENT_RE -- measured live (see this
// file's own ad-hoc sweep in the BUG-029 fix commit) before picking the
// rule: splitting on every lowercase/digit-to-uppercase transition and then
// reusing the permissive lowercase-or-digit WORD_SEGMENT_RE per segment
// (matching the '-'/'_' path's own rule) lets roughly 1 in 400 random 20-36
// char mixed-case-plus-digit strings slip through as "exempt", because
// random case noise alone produces plenty of transitions that happen to look
// segmented, and the resulting segment lengths are irregular enough to dodge
// rule (c) below. A deliberate '-'/'_' character, by contrast, essentially
// never appears in a real generic-secret literal by chance -- an attacker
// has to choose to add it -- so the permissive rule is safe there but not
// here. Requiring every camelCase-derived segment to be LETTERS ONLY (no
// digits) and at least 3 characters long closed that gap entirely across a
// 150,000-sample sweep (100k digit-free, 50k with digits, 20-36 chars each,
// zero false negatives at either width) while still recognizing every real
// fixture this bug was filed against. The residual this can't close --
// pronounceable-looking ALL-LETTER random noise landing on this same shape
// purely by chance -- measured at ~1 in 7,000 samples in that same sweep,
// which is the same order of magnitude as, and no worse than, the disclosed
// order-0-entropy residual this file's header already accepts for the
// '-'/'_' path (SEC-021): no wordlist is available to close it further
// (FEAT-028 AC-3, stdlib-only). A digit anywhere in the candidate falls back
// to the '-'/'_' path (which requires an explicit separator) or the raw
// entropy check, same as any other non-word-shaped string.
const CAMEL_BOUNDARY_RE = /[a-z][A-Z]/;
const CAMEL_LOWER_TO_UPPER_RE = /([a-z])([A-Z])/g;
const CAMEL_ACRONYM_TO_WORD_RE = /([A-Z]+)([A-Z][a-z])/g;
const CAMEL_WORD_SEGMENT_RE = /^[A-Z]?[a-z]{2,}$/;

function splitCamelCaseSegments(candidate) {
  const normalized = candidate
    .replace(CAMEL_LOWER_TO_UPPER_RE, '$1_$2')
    .replace(CAMEL_ACRONYM_TO_WORD_RE, '$1_$2');
  return normalized.split('_').filter(Boolean);
}

function isCamelCaseSegmentedIdentifier(candidate) {
  if (SEGMENT_SEPARATOR_RE.test(candidate)) return false; // handled by the '-'/'_' path instead
  if (!CAMEL_BOUNDARY_RE.test(candidate)) return false; // no case transition at all -- nothing to segment
  const segments = splitCamelCaseSegments(candidate);
  if (segments.length < 2) return false;
  if (!segments.every(seg => CAMEL_WORD_SEGMENT_RE.test(seg))) return false;

  const lowerSegments = segments.map(seg => seg.toLowerCase());

  // Rule (b) is shared verbatim with the '-'/'_' path: a single long
  // high-entropy segment disqualifies the exemption on its own.
  for (const seg of lowerSegments) {
    if (seg.length >= SEGMENT_ENTROPY_MIN_LENGTH && shannonEntropy(seg) >= ENTROPY_THRESHOLD) {
      return false;
    }
  }

  // BUG-189: rule (c) is DELIBERATELY NOT shared verbatim here. The '-'/'_'
  // path's rule (c) only runs the reassembled-whole entropy check when
  // lengthRange <= SEGMENT_LENGTH_RANGE_TOLERANCE, and grants an
  // UNCONDITIONAL, no-scrutiny-at-all exemption outside that band (the
  // "middle band" gap disclosed in this file's SEC-021 header). That gap was
  // accepted for '-'/'_' only because reaching it requires an attacker to
  // deliberately insert separator characters into a secret -- a conspicuous,
  // unusual act. BUG-029 reused the identical rule (c) shape for bare
  // camelCase, which requires NO deliberate attacker action at all (camelCase
  // is the single most common value shape in this codebase), so the same
  // unconditional-exemption gap became trivially, silently reachable by any
  // ordinary randomly-generated camelCase-shaped secret (measured: 93.6%
  // evasion on a 20k-sample adversarial sweep of length-range >= 2 camelCase
  // candidates; live repro `qzxkWpfjtlaZbnmqrs` / `kjhVxplowqzmBtrsu` both
  // wrongly exempt under the inherited rule).
  //
  // Fix: the camelCase path's rule (c) runs the reassembled-whole entropy
  // check UNCONDITIONALLY -- no lengthRange gate, no band where scrutiny is
  // skipped -- making it strictly narrower than the separator path's
  // exemption, as it should be given camelCase's lower attacker-effort bar.
  // The threshold is intentionally NOT reused as-is: ENTROPY_THRESHOLD
  // (3.7) is too tight against this path's own accepted BUG-029 true
  // exemptions once checked unconditionally (housingPerDay reassembles to
  // 3.7004 bits/char -- a hair over 3.7 -- so reusing 3.7 here would
  // re-break the exact false-positive fix BUG-029 shipped to close).
  // CAMEL_REASSEMBLY_ENTROPY_THRESHOLD (3.75) clears every BUG-029 true
  // exemption fixture with margin (housingPerDay 3.700, laneCapacityPcuPer-
  // Hour 3.538, elecPerDay 2.846, foodPerDay 2.922) while still catching
  // both live repro strings (3.948 / 4.087). A fresh 20k-sample sweep of
  // this specific (post-fix) function, mirroring the attacker's own
  // methodology, is recorded in claude-secret-checker.test.js.
  //
  // This does not claim to close the gap to zero -- this file's SEC-021
  // header already proves, with a live counterexample, that no fixed
  // order-0-entropy threshold can perfectly separate a machine-cut secret
  // from a genuine multi-word phrase at these string lengths. What this
  // closes is the specific defect BUG-189 was filed against: an
  // UNCONDITIONAL, zero-scrutiny exemption band reachable with no deliberate
  // attacker signal. Every camelCase candidate now gets SOME entropy
  // scrutiny; only the '-'/'_' path, gated behind a conspicuous deliberate
  // separator, still gets the free pass outside its tolerance band.
  const lengths = lowerSegments.map(seg => seg.length);
  const lengthRange = Math.max(...lengths) - Math.min(...lengths);
  const reassembledEntropy = shannonEntropy(lowerSegments.join(''));
  if (lengthRange <= SEGMENT_LENGTH_RANGE_TOLERANCE) {
    if (reassembledEntropy >= ENTROPY_THRESHOLD) return false;
  } else if (reassembledEntropy >= CAMEL_REASSEMBLY_ENTROPY_THRESHOLD) {
    return false;
  }

  return true;
}

// SEC-021 REGRESSION FIX ROUND 2 (post-round-3-Destructive-rejection,
// 2026-08-12): round 1's fix (below, rule (b), unchanged) closed the
// case-folding attack, and round 1's rule (c) — gated behind "segments.length
// >= 4 AND all segments exactly the same length" — closed the hyphen-chunked
// hex attack. Round 3's Destructive found the SAME shape of gap one level up:
// rule (c)'s two preconditions (>=4 segments, EXACT length equality) are
// narrow enough that a secret split into just 2-3 chunks, or chunks whose
// lengths differ by even one character, skips rule (c) ENTIRELY regardless
// of how high the reassembled whole's entropy is. Proven live (see
// claude-secret-checker.test.js): a 3x10-char-segment random secret, a
// 10/10/11-char non-uniform secret, and a 5x~11-char secret all measure
// 4.19-4.60 bits/char reassembled (comfortably clearing ENTROPY_THRESHOLD)
// yet isWordSegmentedIdentifier() returned true (wrongly exempt) for all
// three under round 1's code, because 3 segments < the 4-segment floor, and
// 10/10/11 is not EXACTLY uniform.
//
// The naive "fix" — drop both preconditions and just check the reassembled
// whole against ENTROPY_THRESHOLD unconditionally — was tried first and
// REJECTED after measurement, not on suspicion: it reopens the exact false-
// positive problem rule (c) was built to avoid, and does so provably, using
// this project's own real text, not a hypothetical. The phrase "backslash-
// before-closing-quote" (docs/planning/acceptance/tool.secretguard.md line
// 703, a genuine descriptive phrase in this project's own prose, of the
// exact shape a correlation ID takes) reassembles to 'backslashbeforeclosing
// quote' at 3.930 bits/char — HIGHER than the round-1 hyphen-chunked hex
// attack's own 3.890 bits/char. Because 3.930 > 3.890, NO single fixed
// threshold can accept the former while rejecting the latter: any threshold
// low enough to catch the hex attack (<=3.890) also catches this real
// phrase, and any threshold high enough to spare this phrase (>3.930) also
// spares the hex attack. This is not a close call to retune away — it is a
// proof that whole-string order-0 Shannon entropy alone, with no structural
// anchor at all, cannot separate the two classes at these string lengths (a
// 200,000-sample simulation of random 30-char lowercase+digit strings
// independently confirms this: the tail of that distribution reaches vowel-
// heavy, low-looking-random samples too, so entropy's blindness to character
// SEQUENCE, not just frequency, is the root cause, and no threshold escapes
// it). A wordlist-based fix is unavailable (FEAT-028 AC-3, stdlib-only,
// already binding), so a structural anchor equivalent to rule (c)'s original
// one is kept, generalized just enough to close round 3's gap:
//
//   (b) SEGMENT_ENTROPY_MIN_LENGTH = 12 (UNCHANGED from round 1). Any
//       individual segment at least this long is checked against
//       ENTROPY_THRESHOLD on ITS OWN — still required as its own,
//       independent catch: a single long high-entropy segment padded with a
//       long LOW-entropy segment (e.g. a run of a repeated character) dilutes
//       the reassembled whole's entropy below ENTROPY_THRESHOLD even though
//       a real secret is embedded in it (measured: an 18-char random segment
//       at 4.170 bits/char, padded with just 10 extra same-length-class
//       characters, drags the reassembled whole down to 3.448 bits/char —
//       below threshold). Rule (c) below cannot see this padding attack;
//       rule (b) is the only defense against it, so it stays.
//   (c) SEGMENT_LENGTH_RANGE_TOLERANCE = 1 (replaces UNIFORM_CHUNK_MIN_
//       SEGMENTS's exact-equality + >=4-segments gate). If the LONGEST and
//       SHORTEST segment differ in length by at most this many characters —
//       i.e. "near-uniform", not strictly identical, and with NO minimum
//       segment count (a 2-segment candidate qualifies) — the reassembled
//       whole is checked against ENTROPY_THRESHOLD (unchanged, never
//       retuned). This closes all three of round 3's gap fixtures: 10/10/10
//       (range 0), 10/10/11 (range 1), and 11/11/11/10/10 (range 1) are all
//       within tolerance, and all three reassemble to 4.19-4.60 bits/char,
//       clearing ENTROPY_THRESHOLD. It is near-uniform length specifically
//       (not entropy, not segment count) that distinguishes machine-cut
//       chunking from genuine multi-word phrases: verified against every
//       hyphenated identifier of 3+ segments actually present in this
//       project's own source/docs/tests (131 candidates swept across
//       claude-secret-checker.js/.test.js, claude-secret-guard.js,
//       claude-bow.js, claude-author-guard.js, claude-plan-guard.js,
//       claude-pre-commit-check.js, claude-version-guard.js, CLAUDE.md, and
//       dev-team-process.md/tool.secretguard.md) — NONE has a segment-length
//       range <= 1 while also clearing ENTROPY_THRESHOLD once reassembled;
//       every real phrase's word lengths vary by 2 or more characters. The
//       three SEC-021 AC-1 literals, 'foo-bar-baz-example-literal',
//       'user-data-sync-fail', and all five BUG-029 allowlist literals stay
//       exempt under this rule (see claude-secret-checker.test.js for the
//       measured per-fixture numbers) — the ones with range <= 1
//       ('user-data-sync-fail', range 0) are protected because their
//       reassembled entropy (3.578) stays below ENTROPY_THRESHOLD, exactly
//       as round 1's rule (c) already relied on for that same fixture.
const SEGMENT_ENTROPY_MIN_LENGTH = 12;
const SEGMENT_LENGTH_RANGE_TOLERANCE = 1;
// BUG-189: the reassembled-whole entropy bar used by the camelCase path's
// rule (c) specifically for candidates OUTSIDE SEGMENT_LENGTH_RANGE_
// TOLERANCE (see isCamelCaseSegmentedIdentifier's own comment above its
// use). Deliberately higher than ENTROPY_THRESHOLD (3.7): reused as-is it
// would re-flag BUG-029's own accepted true exemption housingPerDay
// (reassembles to 3.7004 bits/char). 3.75 clears every BUG-029 true
// exemption fixture with margin while still catching both BUG-189 live
// repro strings (3.948 / 4.087 bits/char).
const CAMEL_REASSEMBLY_ENTROPY_THRESHOLD = 3.75;

function isWordSegmentedIdentifier(candidate) {
  if (!SEGMENT_SEPARATOR_RE.test(candidate)) {
    // BUG-029: no explicit '-'/'_' separator at all -- try the stricter,
    // separately-validated camelCase path (see isCamelCaseSegmentedIdentifier's
    // own comment for why it is not just a relaxed WORD_SEGMENT_RE reuse).
    return isCamelCaseSegmentedIdentifier(candidate);
  }
  const segments = candidate.split(/[-_]/);
  if (!segments.every(seg => WORD_SEGMENT_RE.test(seg))) return false;

  // (b) A single segment carrying all the entropy on its own (a short
  // prefix followed by one long high-entropy blob) disqualifies the
  // exemption regardless of the other segments.
  for (const seg of segments) {
    if (seg.length >= SEGMENT_ENTROPY_MIN_LENGTH && shannonEntropy(seg) >= ENTROPY_THRESHOLD) {
      return false;
    }
  }

  // (c) Near-uniform-width chunking (a secret cut into equal-or-nearly-equal
  // groups to evade the per-segment shape check, regardless of segment
  // count) disqualifies the exemption once the reassembled whole is itself
  // high-entropy. No minimum segment count: even a 2-segment candidate is
  // checked, closing round 3's gap.
  const lengths = segments.map(seg => seg.length);
  const lengthRange = Math.max(...lengths) - Math.min(...lengths);
  if (lengthRange <= SEGMENT_LENGTH_RANGE_TOLERANCE && shannonEntropy(segments.join('')) >= ENTROPY_THRESHOLD) {
    return false;
  }

  return true;
}

function looksHighEntropy(candidate) {
  if (candidate.length < ENTROPY_MIN_LENGTH) return false;
  if (isWordSegmentedIdentifier(candidate)) return false;
  if (!TOKEN_SHAPE_RE.test(candidate)) return false;
  return shannonEntropy(candidate) >= ENTROPY_THRESHOLD;
}

// ---------------------------------------------------------------------------
// BUG-026: Go package-identifier suppression for the high-entropy check
// (relocated unchanged — see original claude-secret-guard.js history for the
// full derivation comment).
// ---------------------------------------------------------------------------

function stripGoCommentsAndStrings(src) {
  let out = '';
  let i = 0;
  const n = src.length;
  while (i < n) {
    const c = src[i];
    const c2 = i + 1 < n ? src[i + 1] : '';
    if (c === '/' && c2 === '/') {
      while (i < n && src[i] !== '\n') i++;
      continue;
    }
    if (c === '/' && c2 === '*') {
      i += 2;
      while (i < n && !(src[i] === '*' && src[i + 1] === '/')) {
        if (src[i] === '\n') out += '\n';
        i++;
      }
      i = Math.min(n, i + 2);
      continue;
    }
    if (c === '"' || c === "'" || c === '`') {
      const quote = c;
      out += ' ';
      i++;
      while (i < n && src[i] !== quote) {
        if (quote !== '`' && src[i] === '\\') i++;
        else if (src[i] === '\n') out += '\n';
        i++;
      }
      i++;
      out += ' ';
      continue;
    }
    out += c;
    i++;
  }
  return out;
}

function collectGoPackageIdentifiers(dirAbsPath) {
  const identifiers = new Set();
  let entries;
  try {
    entries = fs.readdirSync(dirAbsPath).filter(f => f.endsWith('.go'));
  } catch {
    return identifiers;
  }
  for (const fname of entries) {
    let src;
    try {
      src = fs.readFileSync(path.join(dirAbsPath, fname), 'utf8');
    } catch {
      continue;
    }
    const cleaned = stripGoCommentsAndStrings(src);

    let m;
    const funcRe = /\bfunc\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)\s*\(/g;
    while ((m = funcRe.exec(cleaned))) identifiers.add(m[1]);
    const typeRe = /\btype\s+([A-Za-z_]\w*)\b/g;
    while ((m = typeRe.exec(cleaned))) identifiers.add(m[1]);

    let parenDepth = 0;
    for (const rawLine of cleaned.split('\n')) {
      const trimmed = rawLine.trim();
      if (parenDepth === 0) {
        if (/^(?:const|var)\s*\($/.test(trimmed)) {
          parenDepth = 1;
          continue;
        }
        const single = trimmed.match(/^(?:const|var)\s+([A-Za-z_]\w*(?:\s*,\s*[A-Za-z_]\w*)*)\b/);
        if (single) for (const name of single[1].split(',')) identifiers.add(name.trim());
        continue;
      }
      const blockLine = trimmed.match(/^([A-Za-z_]\w*(?:\s*,\s*[A-Za-z_]\w*)*)\b/);
      if (blockLine) for (const name of blockLine[1].split(',')) identifiers.add(name.trim());
      for (const ch of trimmed) {
        if (ch === '(') parenDepth++;
        else if (ch === ')') parenDepth = Math.max(0, parenDepth - 1);
      }
    }
  }
  return identifiers;
}

const goPackageIdentifierCache = new Map();

function isGoPackageIdentifier(candidate, fileRelPath) {
  const dirAbs = path.dirname(path.join(ROOT, fileRelPath));
  let identifiers = goPackageIdentifierCache.get(dirAbs);
  if (!identifiers) {
    identifiers = collectGoPackageIdentifiers(dirAbs);
    goPackageIdentifierCache.set(dirAbs, identifiers);
  }
  return identifiers.has(candidate);
}

// ---------------------------------------------------------------------------
// Redaction (relocated unchanged — BUG-001 fix)
// ---------------------------------------------------------------------------

function redact(secret) {
  const len = secret.length;
  if (len <= 8) return '*'.repeat(len);
  const maxRevealTotal = Math.floor(len * 0.25);
  const minMaskLen = Math.ceil(len * 0.5);
  const revealTotal = Math.max(0, Math.min(maxRevealTotal, len - minMaskLen));
  const prefixLen = Math.floor(revealTotal / 2);
  const suffixLen = revealTotal - prefixLen;
  const maskLen = len - prefixLen - suffixLen;
  const prefix = prefixLen > 0 ? secret.slice(0, prefixLen) : '';
  const suffix = suffixLen > 0 ? secret.slice(-suffixLen) : '';
  return `${prefix}${'*'.repeat(maskLen)}${suffix}`;
}

// ---------------------------------------------------------------------------
// Pattern-based detectors (run per added line)
// ---------------------------------------------------------------------------

const PRIVATE_KEY_RE = /-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----/;

const API_KEY_PATTERNS = [
  { name: 'aws-access-key-id', re: /AKIA[0-9A-Z]{16}/ },
  { name: 'openai-style-secret', re: /sk-[A-Za-z0-9]{20,}/ },
  { name: 'github-pat', re: /gh[pousr]_[A-Za-z0-9]{36}/ },
  { name: 'slack-token', re: /xox[baprs]-[A-Za-z0-9-]+/ },
  { name: 'bearer-token', re: /\bBearer\s+[A-Za-z0-9._-]{16,}/i },
  {
    name: 'generic-key-assignment',
    re: /\b(api[_-]?key|api[_-]?token|secret|access[_-]?token|auth[_-]?token|password)\s*[:=]\s*["']([A-Za-z0-9+/_.=-]{12,})["']/i,
  },
];

const CONNECTION_STRING_RE = /\b([a-zA-Z][a-zA-Z0-9+.-]*):\/\/([A-Za-z0-9_.%-]+):([^@\s'"]*)@([^\s'"]+)/g;

const HARDCODE_KEYWORDS = new Set(['count', 'total', 'expected', 'actual', 'num', 'length', 'size']);
const HARDCODE_EXEMPT_LITERALS = new Set([0, 1]);
const HARDCODE_CMP_RE = /\b([A-Za-z_$][A-Za-z0-9_.$]*)\s*(===|!==|==|!=)\s*(\d+(?:\.\d+)?)\b|\b(\d+(?:\.\d+)?)\s*(===|!==|==|!=)\s*([A-Za-z_$][A-Za-z0-9_.$]*)\b/g;

function identifierWords(ident) {
  return ident
    .replace(/([a-z0-9])([A-Z])/g, '$1_$2')
    .replace(/([A-Z]+)([A-Z][a-z])/g, '$1_$2')
    .split(/[._]+/)
    .map(w => w.toLowerCase())
    .filter(Boolean);
}

function isHardcodeKeywordIdentifier(ident) {
  return identifierWords(ident).some(w => HARDCODE_KEYWORDS.has(w));
}

function scanLine(lineText) {
  const findings = [];

  if (PRIVATE_KEY_RE.test(lineText)) {
    findings.push({ category: 'private-key', evidence: redact(lineText.trim()) });
  }

  for (const pat of API_KEY_PATTERNS) {
    const m = lineText.match(pat.re);
    if (m) {
      const secret = m[2] || m[0];
      findings.push({ category: 'api-key', detail: pat.name, evidence: redact(secret), candidate: secret });
    }
  }

  let connMatch;
  CONNECTION_STRING_RE.lastIndex = 0;
  while ((connMatch = CONNECTION_STRING_RE.exec(lineText))) {
    const full = connMatch[0];
    findings.push({ category: 'connection-string-password', evidence: redact(full), candidate: full });
  }

  const stringLiteralRe = /'([^'\\]{1,200})'|"([^"\\]{1,200})"/g;
  let strMatch;
  const literalRecords = [];
  while ((strMatch = stringLiteralRe.exec(lineText))) {
    const literal = strMatch[1] !== undefined ? strMatch[1] : strMatch[2];
    literalRecords.push({ literal, start: strMatch.index, end: strMatch.index + strMatch[0].length });
    if (looksHighEntropy(literal)) {
      findings.push({ category: 'high-entropy', evidence: redact(literal), candidate: literal });
    }
  }

  // BUG-148/BUG-150: a credential split across two or three adjacent string
  // literals on the SAME line evades both the per-literal entropy check
  // above (each piece can fall under ENTROPY_MIN_LENGTH even though the
  // whole clears the threshold) and API_KEY_PATTERNS' contiguity requirement
  // (the pattern matches against lineText, but the literals aren't
  // textually adjacent — there's always at least a closing quote/opening
  // quote and whatever source sits between them). Re-run both detector
  // classes against BOUNDED windows of 2-3 literals' CONTENTS, concatenated
  // in source order with no separator — this reconstructs the runtime
  // string a naive same-line split would produce, WITHOUT trying the whole
  // line's literal set (BUG-150: that unbounded version treated any
  // array/object of unrelated short strings as one giant "split secret").
  //
  // A window is only considered a plausible single-value split if the raw
  // source text between each consecutive pair of literals in the window
  // is ITSELF SHAPED LIKE deliberate value-continuation, not merely "absent
  // any list-separator character". An earlier version of this fix used a
  // blocklist (reject if the gap contains `, [ ] { }`), but a live sweep
  // against this project's own source (BUG-150 round 2) showed that still
  // left 1344 false positives: ordinary `"key": "value"` JSON/object pairs
  // (gap `": "`, no comma/bracket), boolean-OR chains of string literals
  // (`v === 'a' || v === 'b'`, gap ` || v === '`), and shell-pipe literals
  // all slipped through a pure blocklist. Inverted to an ALLOWLIST instead:
  // a gap only counts as continuation if it matches one of the two shapes
  // BUG-148 was actually filed against —
  //   (a) string concatenation: `"piece1" + "piece2"` (a bare `+`, optional
  //       whitespace, nothing else between the literals), or
  //   (b) a second adjacent declaration splitting the same conceptual value:
  //       `"piece1"; const/let/var name = "piece2"`
  // Anything else (comma, colon, brackets, braces, `||`, `===`, pipes,
  // arbitrary prose) is NOT a continuation shape and the window is skipped.
  const CONTINUATION_GAP_RE = /^\s*\+\s*$|^;\s*(?:const|let|var)\s+[A-Za-z_$][\w$]*\s*=\s*$/;
  const seenCandidates = new Set();
  for (let windowSize = 2; windowSize <= 3; windowSize++) {
    for (let i = 0; i + windowSize <= literalRecords.length; i++) {
      const window = literalRecords.slice(i, i + windowSize);
      let adjacentEnough = true;
      for (let j = 1; j < window.length; j++) {
        const between = lineText.slice(window[j - 1].end, window[j].start);
        if (!CONTINUATION_GAP_RE.test(between)) {
          adjacentEnough = false;
          break;
        }
      }
      if (!adjacentEnough) continue;

      const concatenated = window.map(r => r.literal).join('');
      if (seenCandidates.has(concatenated)) continue;
      seenCandidates.add(concatenated);

      for (const pat of API_KEY_PATTERNS) {
        const m = concatenated.match(pat.re);
        if (m) {
          const secret = m[2] || m[0];
          findings.push({
            category: 'api-key',
            detail: `${pat.name} (split across ${window.length} adjacent string literals)`,
            evidence: redact(secret),
            candidate: secret,
          });
        }
      }
      if (looksHighEntropy(concatenated)) {
        findings.push({
          category: 'high-entropy',
          detail: `split across ${window.length} adjacent string literals`,
          evidence: redact(concatenated),
          candidate: concatenated,
        });
      }
    }
  }

  let hcMatch;
  HARDCODE_CMP_RE.lastIndex = 0;
  while ((hcMatch = HARDCODE_CMP_RE.exec(lineText))) {
    const ident = hcMatch[1] || hcMatch[6];
    const literalStr = hcMatch[3] || hcMatch[4];
    if (ident && isHardcodeKeywordIdentifier(ident)) {
      if (HARDCODE_EXEMPT_LITERALS.has(parseFloat(literalStr))) continue;
      findings.push({ category: 'hardcoding-smell', evidence: lineText.trim().slice(0, 160) });
    }
  }

  return findings;
}

// ---------------------------------------------------------------------------
// Staged diff parsing
// ---------------------------------------------------------------------------

function parseAddedLines(diffText) {
  const records = [];
  let currentFile = null;
  let nextLineNo = null;
  const lines = diffText.split('\n');
  for (const raw of lines) {
    if (raw.startsWith('+++ ')) {
      const m = raw.match(/^\+\+\+ b\/(.+)$/);
      currentFile = m ? m[1] : null;
      continue;
    }
    if (raw.startsWith('--- ')) {
      continue;
    }
    if (raw.startsWith('@@')) {
      const m = raw.match(/^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@/);
      nextLineNo = m ? parseInt(m[1], 10) : null;
      continue;
    }
    if (raw.startsWith('+') && currentFile !== null && nextLineNo !== null) {
      records.push({ file: currentFile, line: nextLineNo, text: raw.slice(1) });
      nextLineNo += 1;
      continue;
    }
  }
  return records;
}

// ---------------------------------------------------------------------------
// Main scan (relocated unchanged from claude-secret-guard.js's runScan/
// formatFindings — see claude-secret-checker.test.js for the parity proof)
// ---------------------------------------------------------------------------

function runScan() {
  const { allowedPaths, allowedPatterns } = loadAllowlist();

  const nameResult = spawnSync('git', ['diff', '--cached', '--name-only'], { cwd: ROOT, encoding: 'utf8' });
  if (nameResult.error || nameResult.status !== 0) {
    const details = nameResult.error ? nameResult.error.message
      : [nameResult.stdout, nameResult.stderr].filter(Boolean).join('\n').trim();
    throw new Error(`git diff --cached --name-only failed: ${details}`);
  }
  const stagedFiles = nameResult.stdout.split('\n').map(s => s.trim()).filter(Boolean);

  const diffResult = spawnSync('git', ['diff', '--cached', '-U0', '--', '.'], {
    cwd: ROOT,
    encoding: 'utf8',
    maxBuffer: 64 * 1024 * 1024,
  });
  if (diffResult.error || diffResult.status !== 0) {
    const details = diffResult.error ? diffResult.error.message
      : [diffResult.stdout, diffResult.stderr].filter(Boolean).join('\n').trim();
    throw new Error(`git diff --cached failed: ${details}`);
  }

  const findings = [];

  for (const file of stagedFiles) {
    if (isAllowlistedPath(file, allowedPaths)) continue;
    const ext = path.extname(file).toLowerCase();
    if (SENSITIVE_FILE_EXTENSIONS.includes(ext)) {
      findings.push({ file, line: 'N/A', category: 'certificate-file', evidence: `staged file with sensitive extension (${ext})` });
    }
  }

  const addedLines = parseAddedLines(diffResult.stdout);
  for (const rec of addedLines) {
    if (isAllowlistedPath(rec.file, allowedPaths)) continue;
    const lineFindings = scanLine(rec.text);
    for (const f of lineFindings) {
      if (f.candidate && isAllowlistedValue(f.candidate, allowedPatterns)) continue;
      if (
        f.category === 'high-entropy' &&
        f.candidate &&
        rec.file.toLowerCase().endsWith('.go') &&
        isGoPackageIdentifier(f.candidate, rec.file)
      ) {
        continue;
      }
      findings.push({ file: rec.file, line: rec.line, category: f.category, evidence: f.evidence, detail: f.detail });
    }
  }

  return findings;
}

function formatFindings(findings) {
  return findings
    .map(f => `  - ${f.file}:${f.line} [${f.category}]${f.detail ? ` (${f.detail})` : ''} — ${f.evidence}`)
    .join('\n');
}

// ---------------------------------------------------------------------------
// checkSecrets() — the new three-state call contract for a future dispatcher
// (AC-B5, AC-E1). Wraps runScan() (unchanged) without altering its behaviour.
// ---------------------------------------------------------------------------

function checkSecrets() {
  let findings;
  try {
    findings = runScan();
  } catch (err) {
    // AC-F1: an internal error (git failure, malformed allowlist, etc.) is
    // its own state — never silently downgraded to "clean".
    return { status: 'internal-error', error: err };
  }
  if (findings.length > 0) {
    return { status: 'found-problems', findings };
  }
  return { status: 'clean' };
}

module.exports = {
  ROOT,
  ALLOWLIST_PATH,
  ENTROPY_THRESHOLD,
  ENTROPY_MIN_LENGTH,
  SENSITIVE_FILE_EXTENSIONS,
  loadAllowlist,
  isAllowlistedValue,
  isAllowlistedPath,
  globMatch,
  shannonEntropy,
  isWordSegmentedIdentifier,
  looksHighEntropy,
  SEGMENT_ENTROPY_MIN_LENGTH,
  SEGMENT_LENGTH_RANGE_TOLERANCE,
  CAMEL_REASSEMBLY_ENTROPY_THRESHOLD,
  stripGoCommentsAndStrings,
  collectGoPackageIdentifiers,
  isGoPackageIdentifier,
  redact,
  PRIVATE_KEY_RE,
  API_KEY_PATTERNS,
  CONNECTION_STRING_RE,
  HARDCODE_KEYWORDS,
  HARDCODE_EXEMPT_LITERALS,
  identifierWords,
  isHardcodeKeywordIdentifier,
  scanLine,
  parseAddedLines,
  runScan,
  formatFindings,
  checkSecrets,
};
