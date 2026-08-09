/**
 * PreToolUse hook — secret & hardcoding pre-commit guard (BOW mkey: tool.secretguard).
 *
 * Spec: GR#11 (Pre-Commit Security Review); GR#15 (Validators Derive From
 * Data); M0-ENG §5 (hooks)
 *
 * GR#11 requires a mandatory security threat model before every commit; this
 * hook mechanises the secrets half of that review so it no longer depends on
 * lead vigilance. It intercepts `git commit` and scans STAGED content only
 * (`git diff --cached`, plus the staged file list for binary
 * certificate/keystore files that never show up as text diff lines) for:
 *
 *   - private-key blocks         (-----BEGIN ... PRIVATE KEY-----)
 *   - certificate/keystore files (.pem/.key/.p12/.pfx/.jks/.crt/.cer staged)
 *   - api-key / token patterns   (AKIA..., ghp_/gho_..., sk-..., xox[baprs]-,
 *                                 generic key/token/secret = "literal",
 *                                 Bearer <token>)
 *   - connection-string-password (scheme://user:pass@host, any scheme)
 *   - high-entropy string literals (Shannon entropy over base64/hex-looking
 *     quoted literals, length-gated to avoid firing on short tokens)
 *   - GR#15 hardcoding smells (a comparison/assertion against a bare numeric
 *     literal where the other operand reads like an expected count/total,
 *     e.g. `assert(count === 45)`)
 *
 * A finding is suppressed only when it matches claude-secret-guard.allow.json
 * exactly (an allowlisted path skips the whole file; an allowlisted pattern
 * must match the extracted candidate string exactly / by anchored regex —
 * never a loose substring match, see AC-12).
 *
 * Fail-CLOSED, scoped to commits only (same posture as claude-plan-guard.js,
 * and the same lead-review lesson it already learned): this guard's entire
 * job is to stop a secret-bearing commit from landing, so an internal error
 * while scanning a `git commit` (git itself failing, a malformed allowlist
 * file, an unexpected exception) results in a DENY rather than an allow. But
 * that fail-closed posture must NOT bleed into non-commit commands — if
 * stdin is unparseable, we fall back to a raw substring sniff: deny only if
 * the raw text looks like it might be a `git commit`, otherwise allow
 * immediately. A hook-input hiccup must never brick `git status`, `npm
 * install`, or any other unrelated Bash command.
 *
 * To disable deliberately: set env var CLAUDE_DISABLE_SECRET_GUARD=1 in the
 * environment of the harness process that runs this hook (e.g. the shell
 * that launches Claude Code, or a persistent env entry in its settings) —
 * BEFORE the session starts, not inside a command an agent submits for
 * approval. This is a human/operator-only escape hatch, not a per-command
 * agent bypass: PreToolUse hooks run in the harness process to decide
 * whether to allow a proposed command, so they never inherit env vars set
 * inline within that same proposed command string (`CLAUDE_DISABLE_SECRET_GUARD=1
 * git commit ...` in a Bash tool_input does not reach this process — it
 * would only apply, if anything, to the child shell that runs the command
 * AFTER this hook has already allowed or denied it). That is intentional,
 * not a bug: an agent must never be able to self-authorize bypassing a
 * fail-closed security guard from within the very command being gated (see
 * ASM-045, ASM-048 follow-up — SEC-015).
 *
 * Receives JSON on stdin: { tool: "Bash", tool_input: { command: "..." } }
 * Denies via: { hookSpecificOutput: { hookEventName: "PreToolUse",
 *               permissionDecision: "deny", permissionDecisionReason: "..." } }
 * (same convention as claude-plan-guard.js / claude-pre-commit-check.js)
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
// what proves it suppresses that flag (see AC-11), not an accidental gap in
// the detector. Ordinary English prose is excluded upstream by the
// base64/hex shape prefilter (TOKEN_SHAPE_RE rejects whitespace), not by
// this threshold, so lowering it further does not risk flagging sentences.
const ENTROPY_THRESHOLD = 3.7;
// Why 20: shorter literals (short flags, enum values, single words) produce
// noisy entropy estimates and would false-positive constantly; 20 chars is
// comfortably below the shortest secret shapes this guard targets elsewhere
// (e.g. the 32-char AWS secret key, 36-char GitHub PAT) while still catching
// the metro-DB-default / GUID-length (36-char) fixtures used to prove the
// allowlist path in AC-11.
const ENTROPY_MIN_LENGTH = 20;

const SENSITIVE_FILE_EXTENSIONS = ['.pem', '.key', '.p12', '.pfx', '.jks', '.crt', '.cer'];

// @FIX (SEC-008 follow-up, Bill-directed scope expansion 2026-08-09): this
// hook's commit-intercept used to be a bare `command.includes('git commit')`
// substring test — the same unanchored-substring fragility flagged in
// claude-plan-guard.js / claude-pre-commit-check.js (SEC-008), just not one
// of the five findings a single Destructive-agent sweep happened to sample.
// Bill's ruling: fix it here too rather than leave one hook below the
// standard the other four were just raised to — a below-standard hook left
// in place produces false confidence that the class is closed. Replaced with
// the same shell-command-boundary-anchored regex already proven in
// claude-version-guard.js / claude-bow-ref-check.js / claude-plan-guard.js /
// claude-pre-commit-check.js: the phrase must sit at the start of the
// command or immediately after a shell separator (`;`, `&`, `|`, `(`,
// newline), so a quoted mention never matches but every real invocation
// still does.
const GIT_COMMIT_RE = /(?:^|[;&|(\n])\s*git\s+(?:-C\s+\S+\s+)?commit\b/;

function deny(reason) {
  const output = JSON.stringify({
    hookSpecificOutput: {
      hookEventName: 'PreToolUse',
      permissionDecision: 'deny',
      permissionDecisionReason: reason,
    },
  });
  process.stdout.write(output);
  process.exit(0);
}

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
  // BUG-001 follow-up: entries must be {path, reason} objects — bare strings
  // are rejected (same "mandatory, non-empty reason" discipline as
  // allowedPatterns), and a path of exactly "**" or "*" is rejected outright
  // as over-broad (it would allowlist the entire staged tree).
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

// Precise match only (AC-12): "exact" entries compare the whole candidate
// string byte-for-byte; "regex" entries match the whole candidate string
// anchored (never a bare substring search), regardless of whether the
// author remembered to anchor their own pattern.
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
// matches within a single segment. Sufficient for allowlist entries like
// "go.sum", "code.json", "tools/plan/bow-import.json", "docs/**".
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

// base64/hex-looking: no whitespace, only characters plausible in a
// base64/hex/token alphabet. This is the primary filter — it is what keeps
// ordinary English sentences and identifiers-with-separators out of scope
// before entropy is even considered.
const TOKEN_SHAPE_RE = /^[A-Za-z0-9+/_=-]+$/;

function looksHighEntropy(candidate) {
  if (candidate.length < ENTROPY_MIN_LENGTH) return false;
  if (!TOKEN_SHAPE_RE.test(candidate)) return false;
  return shannonEntropy(candidate) >= ENTROPY_THRESHOLD;
}

// ---------------------------------------------------------------------------
// Redaction
// ---------------------------------------------------------------------------

// BUG-001 (QA finding on FEAT-028, commit 0d09b04): a fixed 4-prefix+4-suffix
// reveal was up to 89% disclosure for a 9-char secret. Reveal is now capped
// at <=25% of the secret's length (rounded down), split as evenly as
// possible between prefix and suffix, with a mask floor of >=50% of length
// enforced explicitly (redundant with the 25% cap today, but keeps the
// invariant self-documenting if the reveal cap is ever loosened). Secrets of
// 8 chars or fewer stay fully masked — there is no safe partial reveal at
// that length.
function redact(secret) {
  const len = secret.length;
  if (len <= 8) return '*'.repeat(len);
  const maxRevealTotal = Math.floor(len * 0.25); // never reveal more than 25% of characters
  const minMaskLen = Math.ceil(len * 0.5); // mask floor: always mask at least half the characters
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

// GR#15 hardcoding smell: a comparison against a bare numeric literal where
// the other operand's identifier reads like an expected count/total, e.g.
// `assert(count === 45)`, `if (total != 1200)`. Comparisons against a
// non-literal (EXPECTED.count, a config lookup) are the compliant form and
// are not matched here because the RHS/LHS group requires \d.
//
// SEC-015 fix (2026-08-09): the original heuristic had two independent
// false-positive sources, both fixed here rather than allowlisted (SEC-015's
// explicit instruction — allowedPaths must not be used to blind the guard on
// real source):
//
//  1. Keyword match was a bare substring test (`/count|total|.../i.test(ident)`),
//     so `accountNumber` matched `num` and `sizeLimit` matched `size` purely
//     by letters-in-sequence, with no relation to what the identifier means.
//     Fixed by requiring the keyword to equal one whole WORD of the
//     identifier — identifiers are split on `.`/`_` and camelCase boundaries
//     (isHardcodeKeywordIdentifier below), so `accountNumber` -> "account",
//     "number" (no match) but `expectedCount` -> "expected", "count" (match,
//     correctly).
//  2. No distinction was drawn between a STRUCTURAL check (is this container
//     empty / does it have exactly one element?) and a DOMAIN assertion (is
//     this magnitude the specific number the spec says it must be?).
//     `x.length === 0`, `results.length === 1` assert nothing about domain
//     data — emptiness and singleton-ness are properties of the container,
//     true for literally any correctly-functioning collection in that state.
//     `results.length === 24` and `expectedCount === 12` assert a specific
//     domain magnitude, which is exactly what GR#15 requires be sourced from
//     data/config, not hardcoded. The line is drawn at {0, 1} only, not
//     wider: 2 is already a real cardinality claim ("exactly two drivers"),
//     and every value above 1 is unambiguously about domain content rather
//     than container shape. HARDCODE_EXEMPT_LITERALS below encodes that cut.
const HARDCODE_KEYWORDS = new Set(['count', 'total', 'expected', 'actual', 'num', 'length', 'size']);
const HARDCODE_EXEMPT_LITERALS = new Set([0, 1]);
const HARDCODE_CMP_RE = /\b([A-Za-z_$][A-Za-z0-9_.$]*)\s*(===|!==|==|!=)\s*(\d+(?:\.\d+)?)\b|\b(\d+(?:\.\d+)?)\s*(===|!==|==|!=)\s*([A-Za-z_$][A-Za-z0-9_.$]*)\b/g;

// Splits an identifier (possibly dotted, e.g. `filePaths.length`) into its
// constituent words on `.`, `_`, and camelCase boundaries, lowercased.
//
// Bill/Tester-2 regression fix (2026-08-09, second pass on this code — logged
// per v1.7.2 as ASM-049): the first version split on a lookahead before
// EVERY uppercase letter (`seg.split(/(?=[A-Z])/)`), which is correct for
// `expectedCount` (one boundary, before `C`) but SHATTERS an all-uppercase
// run into single letters — `MAX_COUNT` -> "m","a","x","c","o","u","n","t" —
// so no word ever equals a whole keyword and the check went silently blind
// on SCREAMING_SNAKE_CASE constants, an entirely ordinary pattern for
// hardcoded literals. That is a false NEGATIVE in a security guard, strictly
// worse than the false positives this fix exists to remove, and the old
// substring regex caught it (accidentally) before this file was touched.
//
// Fixed with the standard two-boundary insertion technique instead of a
// single blanket lookahead:
//   1. lower/digit -> upper   ("fooBar" -> "foo_Bar", "file9Bar" -> "file9_Bar")
//   2. upper-run -> upper+lower  ("HTTPServer" -> "HTTP_Server": the run
//      "HTTP" ends and "Server" begins at the last capital before a
//      lowercase letter, not at every capital)
// Both insert a separator; the string is then split on `.`/`_`/inserted
// separators as one pass. This keeps a same-case run (SCREAMING_SNAKE_CASE,
// or an all-lowercase word) intact as a single word, which is exactly what
// closes the regression: "MAX_COUNT" already has explicit underscores, so no
// case-boundary insertion is even needed there — split on `_` alone now
// yields ["MAX","COUNT"], not eight single letters.
function identifierWords(ident) {
  return ident
    .replace(/([a-z0-9])([A-Z])/g, '$1_$2')
    .replace(/([A-Z]+)([A-Z][a-z])/g, '$1_$2')
    .split(/[._]+/)
    .map(w => w.toLowerCase())
    .filter(Boolean);
}

// True only if one WHOLE word of the identifier equals one of the
// hardcoding-smell keywords — never a substring match (see SEC-015 point 1
// above).
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

  // High-entropy quoted literals.
  const stringLiteralRe = /'([^'\\]{1,200})'|"([^"\\]{1,200})"/g;
  let strMatch;
  while ((strMatch = stringLiteralRe.exec(lineText))) {
    const literal = strMatch[1] !== undefined ? strMatch[1] : strMatch[2];
    if (looksHighEntropy(literal)) {
      findings.push({ category: 'high-entropy', evidence: redact(literal), candidate: literal });
    }
  }

  // GR#15 hardcoding smell.
  let hcMatch;
  HARDCODE_CMP_RE.lastIndex = 0;
  while ((hcMatch = HARDCODE_CMP_RE.exec(lineText))) {
    const ident = hcMatch[1] || hcMatch[6];
    const literalStr = hcMatch[3] || hcMatch[4];
    if (ident && isHardcodeKeywordIdentifier(ident)) {
      // Structural emptiness/singleton checks (x.length === 0, x.length ===
      // 1) are not GR#15 violations — see SEC-015 point 2 above. Everything
      // else (a real domain magnitude) is still flagged.
      if (HARDCODE_EXEMPT_LITERALS.has(parseFloat(literalStr))) continue;
      findings.push({ category: 'hardcoding-smell', evidence: lineText.trim().slice(0, 160) });
    }
  }

  return findings;
}

// ---------------------------------------------------------------------------
// Staged diff parsing
// ---------------------------------------------------------------------------

// Parses `git diff --cached -U0` output into { file, line, text } records
// for added lines only. With -U0 there are no context lines, so every '+'
// (that isn't the '+++' file header) is a staged addition.
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
    // '-' lines (removed) and diff/index metadata lines don't advance the
    // added-line cursor and are not scanned (staged-scope only, AC-5).
  }
  return records;
}

// ---------------------------------------------------------------------------
// Main scan
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

  // Certificate/keystore files staged as-is (binary, no useful text diff).
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
// Entry point
// ---------------------------------------------------------------------------
//
// ASM (logged as SEC-015 follow-up testability note, not a behaviour change):
// wrapped in `require.main === module` so a test harness can `require()` this
// file to reach the pure functions below (scanLine, isHardcodeKeywordIdentifier,
// etc.) without the process blocking on stdin — this guard previously could
// only be exercised end-to-end via a real `git commit` hook invocation, which
// is why it had no test suite at all. When run directly as the hook (the only
// way it is invoked in production — PreToolUse always execs it as a script,
// never requires it), require.main === module is always true, so behaviour is
// unchanged.

function main() {
  let input = '';
  process.stdin.setEncoding('utf8');
  process.stdin.on('data', chunk => { input += chunk; });
  process.stdin.on('end', () => {
  try {
    if (process.env.CLAUDE_DISABLE_SECRET_GUARD === '1') {
      process.exit(0);
    }

    // Strip a UTF-8 BOM (PowerShell pipes prepend one) before parsing.
    let command;
    try {
      const data = JSON.parse(input.replace(/^\uFEFF/, ''));
      command = data?.tool_input?.command ?? '';
    } catch {
      // Unparseable input: fail-closed applies to commits ONLY — denying
      // every Bash command because stdin hiccuped would brick the whole
      // session. Fall back to a raw substring sniff: deny only if it might
      // be a commit, allow anything else. (Same lesson claude-plan-guard.js
      // already hardcoded from its own BOM incident.)
      if (input.includes('git commit')) {
        deny('🛑 SECRET GUARD: hook input was unparseable but appears to contain a git commit — denying (fail-closed). Raw input parse error; retry the commit.');
      }
      process.exit(0);
    }

    // Only intercept real git commit invocations (SEC-008-class fix — see
    // GIT_COMMIT_RE comment above).
    if (!GIT_COMMIT_RE.test(command)) {
      process.exit(0);
    }

    const findings = runScan();

    if (findings.length > 0) {
      deny(
        '🛑 SECRET GUARD: staged content contains possible secrets or GR#15 hardcoding smells (GR#11).\n\n' +
        `${formatFindings(findings)}\n\n` +
        'Remove or rotate the secret(s), or read the value from config/data instead of hardcoding it, then re-stage and retry.\n' +
        'If this is a false positive, add a precise entry to claude-secret-guard.allow.json (see its inline docs) — never widen a pattern to fix a single case.\n' +
        'Emergency bypass (use deliberately, not routinely): CLAUDE_DISABLE_SECRET_GUARD=1'
      );
      return;
    }

    // Clean: no findings. Allow.
    process.exit(0);

  } catch (err) {
    // Fail-CLOSED by design (see header comment), scoped to commits only:
    // an internal guard error (git failure, malformed allowlist, etc.) must
    // never silently let a possibly-secret-bearing commit through.
    //
    // ** UNREDACTED CHANNEL — BUG-001 follow-up (QA note) **
    // err.stack (and err itself) is echoed to the deny reason VERBATIM below,
    // with no redact() pass. That is safe today because every throw on this
    // path is guard-internal plumbing (git invocation failures, allowlist
    // parse errors) — never scanned repo content. It MUST stay that way:
    // never construct or rethrow an Error whose message/stack embeds a
    // matched line, literal, or candidate secret from runScan()/scanLine(),
    // or this catch-all becomes a way to leak exactly what redact() exists
    // to hide. Findings must only ever reach the user through
    // formatFindings(), which redacts every evidence string.
    deny(
      '🛑 SECRET GUARD: internal error while scanning staged content — denying commit ' +
      '(fail-closed by design; see claude-secret-guard.js header).\n\n' +
      `${err && err.stack ? err.stack : err}`
    );
  }
  });
}

if (require.main === module) {
  main();
} else {
  // Exported for tests only (see comment above `main`). Not part of the
  // hook's runtime contract — these are internal helpers, kept intentionally
  // ungrouped so a test file can import exactly the pure functions it needs.
  module.exports = {
    scanLine,
    identifierWords,
    isHardcodeKeywordIdentifier,
    HARDCODE_KEYWORDS,
    HARDCODE_EXEMPT_LITERALS,
    looksHighEntropy,
    shannonEntropy,
    redact,
    isAllowlistedValue,
    isAllowlistedPath,
    globMatch,
    parseAddedLines,
    loadAllowlist,
  };
}
