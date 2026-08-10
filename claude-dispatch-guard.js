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
 *   2. mkey agreement. If the brief names a module key for a cited item and
 *      it disagrees with that item's BOW mkey, DENY and print the real one.
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

const BOW_CODE_RE = /\b(MOD|FEAT|BUG|SEC|INT|ASM)-(\d{3,})\b/g;

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

function overlaps(a, b) {
  return a === b || a.startsWith(b + '/') || b.startsWith(a + '/');
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

  const codes = [...new Set([...prompt.matchAll(BOW_CODE_RE)].map((m) => m[0]))];
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
      // The mkey trap: the brief's spelling may differ from the record's.
      for (const row of rows) {
        if (!row.mkey) continue;
        const spelled = new RegExp(`\\b${row.mkey.replace(/\./g, '\\.')}\\b`);
        const nearMiss = new RegExp(`\\b${row.mkey.replace(/\./g, '\\.')}s\\b`);
        if (!spelled.test(prompt) && nearMiss.test(prompt)) {
          problems.push(
            `${row.code}'s module key is "${row.mkey}", but the brief writes ` +
              `"${row.mkey}s". This exact slip sent a BA to write ` +
              `engine.invariants.md when complete criteria already sat at ` +
              `engine.invariant.md.`
          );
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

main().catch((err) => {
  // Fail OPEN, deliberately — see the header. A bug in this guard must not
  // stop the team working; it must only stop being useful.
  process.stderr.write(`dispatch-guard: internal error, allowing dispatch — ${err.message}\n`);
  process.exit(0);
});
