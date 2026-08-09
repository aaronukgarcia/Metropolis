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
 * To disable deliberately: set env var CLAUDE_DISABLE_SECRET_GUARD=1.
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

const ENTROPY_THRESHOLD = 3.7;
const ENTROPY_MIN_LENGTH = 20;

const SENSITIVE_FILE_EXTENSIONS = ['.pem', '.key', '.p12', '.pfx', '.jks', '.crt', '.cer'];

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
  if (!Array.isArray(allowedPaths) || !allowedPaths.every(p => typeof p === 'string')) {
    throw new Error('allowlist "allowedPaths" must be an array of strings');
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
  for (const pattern of allowedPaths) {
    if (globMatch(pattern, normalized)) return true;
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

function redact(secret) {
  if (secret.length <= 8) return '*'.repeat(secret.length);
  return `${secret.slice(0, 4)}${'*'.repeat(Math.max(4, secret.length - 8))}${secret.slice(-4)}`;
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
const HARDCODE_KEYWORD_RE = /(count|total|expected|actual|num|length|size)/i;
const HARDCODE_CMP_RE = /\b([A-Za-z_$][A-Za-z0-9_.$]*)\s*(===|!==|==|!=)\s*(\d+(?:\.\d+)?)\b|\b(\d+(?:\.\d+)?)\s*(===|!==|==|!=)\s*([A-Za-z_$][A-Za-z0-9_.$]*)\b/g;

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
    if (ident && HARDCODE_KEYWORD_RE.test(ident)) {
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

    // Only intercept git commit commands.
    if (!command.includes('git commit')) {
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
    deny(
      '🛑 SECRET GUARD: internal error while scanning staged content — denying commit ' +
      '(fail-closed by design; see claude-secret-guard.js header).\n\n' +
      `${err && err.stack ? err.stack : err}`
    );
  }
});
