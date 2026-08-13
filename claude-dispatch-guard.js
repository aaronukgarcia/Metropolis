/**
 * PreToolUse hook — dispatch guard (BOW mkey: tool.dispatchguard).
 *
 * Spec: GR#3 (Single Source of Truth); GR#13 (complete all identified issues);
 * docs/planning/dev-team-process.md v1.6.1 (file ownership is transferred, not
 * duplicated) and v1.7 (assumptions logged or rejected).
 *
 * WHY THIS EXISTS — the evidence, not the principle.
 *
 * In one multi-agent wave on 2026-08-10 the lead made eight dispatch-level
 * errors. Seven were mechanically checkable facts asserted from memory
 * instead of looked up:
 *
 *   - briefed mkey `engine.invariants`; the real one is `engine.invariant`
 *     (singular), so a naive check against the TYPED name would still have
 *     missed it — the check has to key off the BOW record (BUG-028)
 *   - three separate dispatches told a BA to write acceptance criteria that
 *     already existed and were committed (MOD-019, MOD-011, MOD-015)
 *   - briefed the path `internal/ui/harness`; code.json says
 *     `internal/harness/uitest`, and under internal/ui the GR#20 lint ban on
 *     ui->engine imports would have bitten immediately
 *   - briefed module key `tool.astgate` against a BOW record filed under
 *     `foundation.errors`
 *   - sent a correction to the wrong agent id; the recipient correctly
 *     refused to act on it
 *   - staged a shared data file wholesale and misattributed another agent's
 *     error codes into an unrelated commit (BUG-030)
 *
 * Only ONE error in that wave was real judgement (a bound placed after
 * json.Unmarshal had already paid the cost it was meant to prevent), and
 * that class is handled by BA-first acceptance criteria and independent
 * review, not by a hook. So this guard deliberately does NOT try to assess
 * whether a brief is any good. It checks facts that can be looked up, and
 * nothing else.
 *
 * WHAT IT CHECKS
 *
 *   1. BOW codes (MOD-/FEAT-/BUG-/SEC-/INT-/ASM-nnn) referenced in the brief
 *      exist. An unknown code is a DENY — dispatching an agent to work an
 *      item that does not exist wastes the whole dispatch.
 *   2. mkey agreement. If the brief attaches a module key directly to a cited
 *      item (a parenthetical, colon/dash, or possessive assertion — not
 *      merely nearby in the same sentence, see ATTACH_GAP_RE/BUG-142) and it
 *      disagrees with that item's BOW mkey, DENY and print the real one.
 *   3. Criteria that already exist. If the brief asks for acceptance criteria
 *      and docs/planning/acceptance/<mkey>.md is already present, DENY with
 *      the path. This is BUG-028 made mechanical, keyed off the BOW's mkey
 *      rather than the lead's prose.
 *   4. File-ownership collisions, via the metro DB's existing sync_file_claims
 *      table. A brief declaring ownership of a path another live session has
 *      claimed is a DENY. dev-team-process v1.6.1 exists because a second BA
 *      was put on a file a first was mid-refresh on and the first one's work
 *      was destroyed; this makes that rule enforceable rather than merely
 *      written down.
 *   5. Paths that do not exist get a WARNING, never a block — new files are
 *      legitimately absent, and a guard that fires on ordinary dispatches
 *      gets switched off within a day (SEC-026's pattern, which this project
 *      has now hit in three separate places).
 *
 * FAIL-OPEN, unlike claude-plan-guard.js. That guard is fail-closed because
 * its job is to stop divergent state reaching the repo. This one's job is to
 * catch the lead's slips, and its own bug must not halt an entire team of
 * agents. Any internal error allows the dispatch and says so on stderr.
 *
 * Deliberate disable: CLAUDE_DISABLE_DISPATCH_GUARD=1.
 *
 * Receives JSON on stdin: { tool: "Agent", tool_input: { prompt, ... } }
 * Denies via: { hookSpecificOutput: { hookEventName: "PreToolUse",
 *               permissionDecision: "deny", permissionDecisionReason: "..." } }
 */

'use strict';

const fs = require('fs');
const path = require('path');

const ROOT = __dirname;
const ACCEPTANCE_DIR = path.join(ROOT, 'docs', 'planning', 'acceptance');

// Claim TTL matches the session-permit TTL in claude-sync.js: a claim from a
// dead session must not block the team forever. Five minutes is long enough
// that a live agent's claim never lapses mid-task (the PostToolUse ping
// refreshes it) and short enough that a crashed session frees its files
// before anyone notices.
const CLAIM_TTL_MS = 5 * 60 * 1000;

// BUG-135: case-insensitive so a lowercase/mixed-case code (mod-072, Feat-072)
// is still recognised rather than silently skipped. Matches are normalised to
// uppercase wherever the code is looked up, since that's the real stored form.
const BOW_CODE_RE = /\b(MOD|FEAT|BUG|SEC|INT|ASM)-(\d{3,})\b/gi;

// A dotted lowercase token that COULD be an mkey (tool.dispatchguard,
// engine.invariant). Bounded segment count as a sanity cap, not a real limit.
const MKEY_TOKEN_RE = /\b([a-z][a-z0-9]*(?:\.[a-z][a-z0-9]*){1,4})\b/g;

// File-extension-shaped trailing segments that make a token look like a path
// (data.catalogue.md, code.json) rather than a bare mkey. Stripped before the
// segment-count/prefix checks below, so a correctly-named acceptance-doc path
// is never mistaken for a wrong mkey, and bare filenames like code.json/
// package.json fall below the 2-segment floor and are dropped entirely.
const PATH_EXTENSIONS = new Set(['md', 'json', 'js', 'go', 'ts', 'txt']);

/**
 * BUG-135/BUG-139: the general form of the mkey-agreement check. Given a line
 * of the brief, returns each dotted token that looks like a REAL registered
 * mkey family (first segment matches a known prefix from the live BOW, not
 * just "looks dotted") together with its character offset in the line — this
 * is what keeps code.json/data.catalogue.md from ever being candidates,
 * without hardcoding an extension or family list, and the offset is what
 * lets BUG-139's nearest-pairing associate the right candidate with the
 * right BOW code on a line that names more than one of either.
 */
function candidateMkeysWithPositions(line, knownPrefixes) {
  const found = [];
  for (const m of line.matchAll(MKEY_TOKEN_RE)) {
    let segs = m[1].split('.');
    const last = segs[segs.length - 1];
    if (segs.length > 1 && PATH_EXTENSIONS.has(last)) segs = segs.slice(0, -1);
    if (segs.length < 2) continue;
    if (!knownPrefixes.has(segs[0])) continue;
    found.push({ mkey: segs.join('.'), index: m.index });
  }
  return found;
}

/** Back-compat wrapper: the plain set of candidate mkeys, no positions. */
function candidateMkeys(line, knownPrefixes) {
  return new Set(candidateMkeysWithPositions(line, knownPrefixes).map((c) => c.mkey));
}

// BUG-142: BUG-139's raw-nearest-by-distance pairing produced false DENYs on
// ordinary TRUE prose ("FEAT-072 and FEAT-073 both touch tool.dispatchguard")
// because proximity alone doesn't mean possession — two codes can each be
// "nearest" to one candidate that only one of them actually claims. Distance
// is replaced with a requirement for an explicit SYNTACTIC attachment: the
// candidate must sit immediately after the code (code appears first — this
// deliberately does not cover "mkey, then code", trading recall for
// precision) separated only by one of a short, fixed set of connectors that
// this project's own citation styles actually use:
//   - a parenthetical: "FEAT-072 (tool.dispatchguard)"
//   - a colon or dash: "FEAT-072: tool.dispatchguard", "FEAT-072 - ..."
//   - a bare copula: "FEAT-072 is tool.dispatchguard"
//   - a possessive/relative assertion, matching this guard's OWN deny-message
//     phrasing: "FEAT-072's (module) key is X", "BUG-136, whose mkey is X"
// A gap of unrelated prose between the code and the candidate ("and FEAT-073
// both touch") matches none of these and is correctly left unattached, rather
// than guessed at by distance.
const ATTACH_GAP_RE =
  /^(?:\s*\(\s*|\s*:\s*|\s{0,2}-{1,2}\s{0,2}|\s+is\s+|'s\s+(?:real\s+|own\s+)?(?:module\s+)?(?:m)?key\s+is\s+|,?\s*whose\s+(?:real\s+|own\s+)?(?:module\s+)?(?:m)?key\s+is\s+)$/i;

/**
 * BUG-139/BUG-142: pairs every BOW code on a line with the candidate mkey
 * token it is directly, syntactically attached to (see ATTACH_GAP_RE) —
 * not gated on the line containing exactly one of each (BUG-139), and not
 * inferred from raw character proximity alone (BUG-142). Returns a
 * Map<code, attachedMkey>. A code with no attached candidate, or one tied
 * between two equally-close attached candidates, is omitted (genuinely
 * ambiguous, left unchecked rather than risked as a false DENY).
 */
function nearestMkeyPerCode(line, knownPrefixes) {
  const codeMatches = [...line.matchAll(BOW_CODE_RE)].map((m) => ({
    code: m[0].toUpperCase(),
    index: m.index,
    length: m[0].length,
  }));
  const nearestByCode = new Map();
  if (!codeMatches.length) return nearestByCode;
  const candidates = candidateMkeysWithPositions(line, knownPrefixes);
  if (!candidates.length) return nearestByCode;

  for (const { code, index, length } of codeMatches) {
    let best = null;
    let bestGapLen = Infinity;
    let tied = false;
    for (const cand of candidates) {
      if (cand.index <= index) continue; // code-before-candidate only, see header note
      const gap = line.slice(index + length, cand.index);
      if (!ATTACH_GAP_RE.test(gap)) continue;
      if (gap.length < bestGapLen) {
        best = cand;
        bestGapLen = gap.length;
        tied = false;
      } else if (gap.length === bestGapLen) {
        tied = true;
      }
    }
    if (best && !tied) nearestByCode.set(code, best.mkey);
  }
  return nearestByCode;
}

/** GR#15: the set of real mkey families, derived from the live BOW, never hardcoded. */
async function knownMkeyPrefixes(conn) {
  const [rows] = await conn.query(
    `SELECT DISTINCT mkey FROM bow_items WHERE mkey IS NOT NULL AND mkey <> ''`
  );
  return new Set(rows.map((r) => r.mkey.split('.')[0]));
}

// Paths are recognised only under the repo's real top-level directories.
// Matching anything slash-shaped would flag prose ("PASS/FAIL", "and/or")
// and URLs, and a guard that cries wolf is one that gets ignored.
const PATH_RE =
  /\b((?:internal|cmd|docs|data|tools|fixtures|\.github|\.claude)\/[A-Za-z0-9_./*-]+)/g;

// Phrases that mean "produce acceptance criteria". Kept narrow on purpose:
// a brief that merely mentions criteria in passing must not be blocked.
const WANTS_CRITERIA_RE =
  /\b(write|author|produce|draft)\s+(the\s+)?(acceptance\s+)?criteria\b/i;

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

function readStdin() {
  try {
    return fs.readFileSync(0, 'utf8');
  } catch {
    return '';
  }
}

async function connect() {
  const mysql = require('mysql2/promise');
  return mysql.createConnection({
    host: process.env.METRO_DB_HOST || 'localhost',
    port: Number(process.env.METRO_DB_PORT || 3306),
    user: process.env.METRO_DB_USER || 'root',
    password: process.env.METRO_DB_PASSWORD || '',
    database: process.env.METRO_DB_NAME || 'metro',
  });
}

/**
 * Paths the brief declares this agent OWNS, as opposed to paths it merely
 * mentions for grounding. Only owned paths are claimed and collision-checked:
 * every brief cites files to read, and treating those as ownership would make
 * every concurrent dispatch collide with every other one.
 */
function extractOwnedPaths(prompt) {
  const owned = new Set();
  const lines = prompt.split(/\r?\n/);
  for (let i = 0; i < lines.length; i++) {
    if (!/\b(FILES? YOU OWN|you own exactly|YOU OWN)\b/i.test(lines[i])) continue;
    // Ownership declarations run to the end of their paragraph.
    for (let j = i; j < lines.length && lines[j].trim() !== ''; j++) {
      for (const m of lines[j].matchAll(PATH_RE)) owned.add(normalise(m[1]));
    }
  }
  return [...owned];
}

// Trailing glob/slash noise is stripped so `internal/ui/harness/**`,
// `internal/ui/harness/`, and `internal/ui/harness` are one claim, not three.
function normalise(p) {
  return p.replace(/\\/g, '/').replace(/\/+$/, '').replace(/\/\*+$/, '');
}

// BUG-135: this repo's filesystem (Windows) is case-insensitive, so two
// differently-cased spellings of the same real path must be treated as one
// path for collision purposes. Folding happens only for the comparison —
// claims are still stored and displayed in their original casing.
function foldPath(p) {
  return p.toLowerCase();
}

function overlaps(a, b) {
  const fa = foldPath(a);
  const fb = foldPath(b);
  return fa === fb || fa.startsWith(fb + '/') || fb.startsWith(fa + '/');
}

async function main() {
  if (process.env.CLAUDE_DISABLE_DISPATCH_GUARD === '1') allow();

  let payload;
  try {
    payload = JSON.parse(readStdin() || '{}');
  } catch {
    allow(); // Unparsable input is not this guard's problem to adjudicate.
  }

  if (payload.tool !== 'Agent') allow();

  const input = payload.tool_input || {};
  const prompt = String(input.prompt || '');
  if (!prompt.trim()) allow();

  const sessionId = payload.session_id || process.env.CLAUDE_SESSION_ID || 'unknown';
  const identity = (process.env.CLAUDE_IDENTITY || 'lead').slice(0, 16);

  const problems = [];
  const warnings = [];

  const codes = [...new Set([...prompt.matchAll(BOW_CODE_RE)].map((m) => m[0].toUpperCase()))];
  const ownedPaths = extractOwnedPaths(prompt);

  const conn = await connect();
  try {
    // --- 1 & 2: BOW codes exist, and the brief's mkey matches the record ---
    let rows = [];
    if (codes.length) {
      const [r] = await conn.query(
        `SELECT code, mkey, title, status FROM bow_items WHERE code IN (?)`,
        [codes]
      );
      rows = r;
      const found = new Set(rows.map((x) => x.code));
      for (const c of codes) {
        if (!found.has(c)) {
          problems.push(
            `BOW code ${c} does not exist. A dispatch citing a non-existent item ` +
              `wastes the whole agent run — check the code before sending.`
          );
        }
      }
    }

    // --- 3: criteria that already exist -------------------------------------
    if (WANTS_CRITERIA_RE.test(prompt)) {
      for (const row of rows) {
        if (!row.mkey) continue;
        const file = path.join(ACCEPTANCE_DIR, `${row.mkey}.md`);
        if (fs.existsSync(file)) {
          problems.push(
            `${row.code} ("${row.title}") already HAS acceptance criteria at ` +
              `docs/planning/acceptance/${row.mkey}.md. Read them before dispatching. ` +
              `If they need extending, say so explicitly and name the gaps — ` +
              `do not commission a second competing file for one BOW item ` +
              `(dev-team-process v1.6.1).`
          );
        }
      }
    }

    // --- 2: mkey agreement (BUG-135, BUG-139, BUG-142) — ANY dispatch type,
    // not just criteria. Per-line: every BOW code on the line is paired with
    // the candidate mkey token it is directly, SYNTACTICALLY attached to
    // (parenthetical, colon/dash, bare "is", or a possessive/relative
    // assertion — see ATTACH_GAP_RE), not gated on the line containing
    // exactly one of each (BUG-135's original gate made the check inert for
    // ordinary two-code brief prose) and not inferred from raw character
    // proximity alone (BUG-139's distance-only fix falsely DENIED true
    // sentences like "FEAT-072 and FEAT-073 both touch tool.dispatchguard",
    // where proximity alone can't tell which code actually claims a shared
    // nearby candidate). A code with no syntactically-attached candidate is
    // left unchecked rather than risked as a false DENY.
    if (rows.length) {
      const knownPrefixes = await knownMkeyPrefixes(conn);
      for (const line of prompt.split(/\r?\n/)) {
        const nearestByCode = nearestMkeyPerCode(line, knownPrefixes);
        for (const [code, candidate] of nearestByCode) {
          const row = rows.find((r) => r.code === code);
          if (!row || !row.mkey) continue;
          if (candidate !== row.mkey) {
            problems.push(
              `${row.code}'s module key is "${row.mkey}", but the brief writes ` +
                `"${candidate}" near it. This exact slip sent a BA to write ` +
                `engine.invariants.md when complete criteria already sat at ` +
                `engine.invariant.md — verify against the BOW record, not memory.`
            );
          }
        }
      }
    }

    // --- 4: file-ownership collisions ---------------------------------------
    if (ownedPaths.length) {
      const cutoff = Date.now() - CLAIM_TTL_MS;
      const [claims] = await conn.query(
        `SELECT path, name, session_id FROM sync_file_claims WHERE claimed_ms > ?`,
        [cutoff]
      );
      for (const want of ownedPaths) {
        for (const held of claims) {
          const heldPath = normalise(String(held.path));
          if (held.session_id === sessionId) continue;
          if (overlaps(want, heldPath)) {
            problems.push(
              `Path "${want}" overlaps "${heldPath}", claimed by ${held.name}. ` +
                `Two agents on one path is how a BA's in-flight work got destroyed ` +
                `(dev-team-process v1.6.1) — wait for that agent, or give this one ` +
                `a disjoint area.`
            );
          }
        }
      }
    }

    // --- 5: paths that do not exist (warn only) -----------------------------
    const mentioned = new Set(
      [...prompt.matchAll(PATH_RE)].map((m) => normalise(m[1]))
    );
    for (const p of mentioned) {
      if (p.includes('*')) continue;
      if (fs.existsSync(path.join(ROOT, p))) continue;
      // A brief that says it is creating the file is not making a claim about
      // the present, so only flag paths presented as existing context.
      const creating = new RegExp(
        `(create|new|write|add)[^\\n]{0,80}${p.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}`,
        'i'
      );
      if (creating.test(prompt)) continue;
      warnings.push(`Path "${p}" does not exist. Intentional (a new file), or a typo?`);
    }

    if (problems.length) {
      deny(
        `🛑 DISPATCH GUARD (${problems.length} problem(s)) — these are ` +
          `look-up-able facts, so they are blocked rather than warned:\n\n` +
          problems.map((p) => `  - ${p}`).join('\n\n') +
          (warnings.length
            ? `\n\nAlso worth checking (not blocking):\n` +
              warnings.map((w) => `  - ${w}`).join('\n')
            : '') +
          `\n\nFix the brief and re-send. Deliberate bypass: ` +
          `CLAUDE_DISABLE_DISPATCH_GUARD=1`
      );
    }

    // Nothing blocking: record this dispatch's claims so the NEXT one collides
    // instead of silently overwriting. Claims are refreshed, not duplicated.
    for (const p of ownedPaths) {
      await conn.query(
        `INSERT INTO sync_file_claims (path, name, session_id, claimed_ms)
         VALUES (?, ?, ?, ?)
         ON DUPLICATE KEY UPDATE name = VALUES(name),
                                 session_id = VALUES(session_id),
                                 claimed_ms = VALUES(claimed_ms)`,
        [p.slice(0, 512), identity, sessionId, Date.now()]
      );
    }

    if (warnings.length) {
      process.stderr.write(
        `dispatch-guard: ${warnings.length} warning(s)\n` +
          warnings.map((w) => `  - ${w}`).join('\n') +
          '\n'
      );
    }
  } finally {
    try {
      await conn.end();
    } catch {
      /* closing a already-dead connection must not fail the dispatch */
    }
  }

  allow();
}

if (require.main === module) {
  main().catch((err) => {
    // Fail OPEN, deliberately — see the header. A bug in this guard must not
    // stop the team working; it must only stop being useful.
    process.stderr.write(`dispatch-guard: internal error, allowing dispatch — ${err.message}\n`);
    process.exit(0);
  });
}

// Exported for unit testing (BUG-135) — no side effects on require, guarded above.
module.exports = {
  candidateMkeys,
  candidateMkeysWithPositions,
  nearestMkeyPerCode,
  foldPath,
  overlaps,
  normalise,
  extractOwnedPaths,
};
