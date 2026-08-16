/**
 * claude-dispatch-guard.test.js — unit tests for claude-dispatch-guard.js.
 *
 * First test file for this hook (none existed before BUG-135). Covers the
 * three BUG-135 findings from FEAT-072's Destructive round:
 *   1. candidateMkeys() — the generalised mkey-agreement extraction that now
 *      runs for every dispatch type, not just BA-criteria ones, and rejects
 *      path-shaped false positives (code.json, data.catalogue.md) via the
 *      DB-derived-prefix + extension-stripping logic.
 *   2. BOW_CODE_RE case-insensitivity, exercised indirectly through the
 *      exported normalise()/candidateMkeys() plumbing is DB-dependent, so
 *      this file spot-checks the regex itself directly (no DB needed).
 *   3. foldPath()/overlaps() case-folding for Windows' case-insensitive FS.
 *
 * Round 2 (BUG-139): the mkey-agreement check above required a line to name
 * EXACTLY ONE BOW code and EXACTLY ONE candidate mkey before checking
 * anything — inert against ordinary brief prose, which routinely cites two
 * codes together ("fixing BUG-135, filed against FEAT-072"). Replaced with
 * nearestMkeyPerCode(), which paired every code with its nearest candidate by
 * raw character distance.
 *
 * Round 3 (BUG-142): raw distance itself proved too loose — it false-DENIED
 * TRUE multi-code sentences ("FEAT-072 and FEAT-073 both touch
 * tool.dispatchguard") because proximity alone can't tell which code actually
 * claims a shared nearby candidate. nearestMkeyPerCode() now requires an
 * explicit SYNTACTIC attachment (a parenthetical, colon/dash, bare copula, or
 * possessive/relative assertion — see ATTACH_GAP_RE in the guard) between a
 * code and a candidate before pairing them, trading some recall (BUG-139's
 * own synthetic fixture no longer resolves — see below) for precision on
 * real brief prose.
 *
 * DB-dependent behavior (the live per-line mismatch DENY, the BOW-code-exists
 * check, file-claim collisions) is integration-level and out of scope for a
 * unit file with no DB fixture harness — this file proves the pure logic
 * every one of those checks is built on.
 *
 * Run: node --test claude-dispatch-guard.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');

const {
  candidateMkeys,
  nearestMkeyPerCode,
  foldPath,
  overlaps,
  normalise,
  extractOwnedPaths,
  connect,
  resolveIdentity,
} = require('./claude-dispatch-guard.js');

const PREFIXES = new Set(['tool', 'engine', 'foundation', 'data', 'feat', 'harness']);

test('candidateMkeys finds a bare real-family mkey token', () => {
  const found = candidateMkeys('citing FEAT-072 (tool.authorguard) by mistake', PREFIXES);
  assert.ok(found.has('tool.authorguard'));
});

test('candidateMkeys catches the pluralization near-miss', () => {
  const found = candidateMkeys('write engine.invariants.md for this', PREFIXES);
  // "engine.invariants" survives extension-stripping (only 2 segs, "md" is the 3rd)
  assert.ok(found.has('engine.invariants'));
});

test('candidateMkeys drops bare filenames below the 2-segment floor (code.json, package.json)', () => {
  const found = candidateMkeys('run node against code.json and package.json', PREFIXES);
  assert.equal(found.size, 0);
});

test('candidateMkeys strips a real extension from a correctly-named acceptance doc, leaving the real mkey', () => {
  const found = candidateMkeys('see docs/planning/acceptance/data.catalogue.md for detail', PREFIXES);
  assert.ok(found.has('data.catalogue'));
  assert.equal(found.size, 1);
});

test('candidateMkeys ignores a dotted token whose family is not a known prefix', () => {
  const found = candidateMkeys('reference version.1.2.3 in the changelog', PREFIXES);
  assert.equal(found.size, 0);
});

test('candidateMkeys is a no-op on a line with no dotted tokens', () => {
  const found = candidateMkeys('dispatch a junior developer to build the thing', PREFIXES);
  assert.equal(found.size, 0);
});

test('foldPath lowercases for comparison', () => {
  assert.equal(foldPath('internal/Foundation/Data'), 'internal/foundation/data');
});

test('overlaps treats differently-cased spellings of the same path as colliding (BUG-135)', () => {
  assert.ok(overlaps('internal/Foundation/data', 'internal/foundation/Data'));
  assert.ok(overlaps('internal/foundation/data', 'internal/foundation/data/reload.go'));
  assert.ok(!overlaps('internal/foundation/data', 'internal/foundation/database'));
});

test('normalise still preserves original casing (only overlaps() folds)', () => {
  assert.equal(normalise('internal/Foundation/Data/'), 'internal/Foundation/Data');
});

// ── FEAT-136 reject-fix regressions: canonicalisation + glob-aware overlaps ──

test('normalise collapses ./, ../ and duplicate slashes to one canonical key', () => {
  assert.equal(normalise('internal/./ui/dash'), 'internal/ui/dash');
  assert.equal(normalise('internal/ui/../ui/dash'), 'internal/ui/dash');
  assert.equal(normalise('internal//ui//dash'), 'internal/ui/dash');
  assert.equal(normalise('./internal/ui/dash'), 'internal/ui/dash');
});

test('normalise anchors a relative ..-escape to the repo root (FEAT-136 r2 claim-store side)', () => {
  // The claim-store side of the r2 fix: a claim stored as
  // `../Metropolis/docs/x.md` must resolve to the same `docs/x.md` key the
  // edit guard's toRepoRelative() produces, so the two guards never disagree
  // on a foreign-claim collision.
  assert.equal(normalise(`../${path.basename(__dirname)}/docs/x.md`), 'docs/x.md');
  assert.equal(normalise('./docs/x.md'), 'docs/x.md');
});

test('overlaps collides a ./-spelled spelling with its canonical claim', () => {
  assert.ok(overlaps('internal/./ui/dash/x.md', 'internal/ui/dash'));
  // `nope/..` cancels to nothing, leaving internal/ui/dash/x.md.
  assert.ok(overlaps('internal/ui/nope/../dash/x.md', 'internal/ui/dash'));
  assert.ok(overlaps('internal/ui/dash/../dash/x.md', 'internal/ui/dash'));
});

test('overlaps expands a claimed glob to match a concrete path (FEAT-136 glob-claim)', () => {
  assert.ok(overlaps('internal/engine/consumption/s6_endtoend_test.go', 'internal/engine/consumption/*_test.go'));
  assert.ok(!overlaps('internal/engine/consumption/s6_endtoend.go', 'internal/engine/consumption/*_test.go'));
  // Reverse direction: a glob want (the brief's declaration) vs a concrete held claim.
  assert.ok(overlaps('internal/engine/consumption/*_test.go', 'internal/engine/consumption/s6_endtoend_test.go'));
});

test('overlaps translates ? to exactly one segment character, not a regex quantifier (FEAT-136 r2)', () => {
  // `?` is a glob signal (hasGlob matches it) but round-1 globToRegex never
  // translated it, so it leaked into the regex as a quantifier — a claim on
  // `dashboard?.md` failed to match `dashboard1.md` (under-protect) and would
  // have matched `dashboard.md` (zero chars). It must match exactly one
  // non-slash character.
  assert.ok(overlaps('internal/ui/dash/dashboard1.md', 'internal/ui/dash/dashboard?.md'));
  assert.ok(overlaps('internal/ui/dash/dashboard?.md', 'internal/ui/dash/dashboard1.md'));
  assert.ok(!overlaps('internal/ui/dash/dashboard12.md', 'internal/ui/dash/dashboard?.md'));
  assert.ok(!overlaps('internal/ui/dash/dashboard.md', 'internal/ui/dash/dashboard?.md'));
});

test('overlaps lets **/docs/x.md match the root-anchored key docs/x.md (FEAT-136 r2)', () => {
  // A leading `**/` may match ZERO segments, so a claim on `**/docs/x.md`
  // protects `docs/x.md` itself (the root-anchored repo-relative key), not
  // only `a/docs/x.md`.
  assert.ok(overlaps('docs/x.md', '**/docs/x.md'));
  assert.ok(overlaps('internal/ui/docs/x.md', '**/docs/x.md'));
  assert.ok(overlaps('**/docs/x.md', 'docs/x.md'));
});

test('extractOwnedPaths only claims paths under an ownership declaration', () => {
  const prompt = [
    'Read internal/other/thing.go for context.',
    '',
    'FILES YOU OWN:',
    'internal/engine/helper/registry.go',
    'internal/engine/helper/registry_test.go',
    '',
    'Do not touch anything else.',
  ].join('\n');
  const owned = extractOwnedPaths(prompt);
  assert.ok(owned.includes('internal/engine/helper/registry.go'));
  assert.ok(owned.includes('internal/engine/helper/registry_test.go'));
  assert.ok(!owned.includes('internal/other/thing.go'));
});

// --- BUG-139: nearestMkeyPerCode no longer requires exactly one code and ---
// --- one candidate per line -- the ordinary prose style of real briefs.   ---
// --- BUG-142: raw nearest-by-distance was replaced with a requirement    ---
// --- for direct SYNTACTIC attachment (see ATTACH_GAP_RE in the guard),   ---
// --- because distance alone false-DENIED true multi-code sentences.      ---

test('nearestMkeyPerCode falls outside detection scope for BUG-139\'s original fixture — no code is directly, syntactically attached to a candidate (BUG-142)', () => {
  const line = 'FEAT-072 is related to BUG-136, whose real mkey is tool.authorguard, not tool.dispatchguard';
  const pairs = nearestMkeyPerCode(line, PREFIXES);
  // Neither FEAT-072 nor BUG-136 is DIRECTLY adjacent to a candidate with
  // only a recognised connector between them for FEAT-072 (too much prose
  // in the gap) — correctly left unchecked now, trading this fixture's
  // coverage for BUG-142's false-positive fix. BUG-136's "whose real mkey
  // is" IS a recognised relative-clause connector, so BUG-136 itself may
  // still resolve if it's ever cited with a known mkey — this line only
  // asserts the FEAT-072 case, which was BUG-139's actual claim.
  assert.ok(!pairs.has('FEAT-072'));
});

test('nearestMkeyPerCode attaches a possessive/relative assertion ("whose ... key is") to its own nearest preceding code', () => {
  const line = 'BUG-136, whose real mkey is tool.authorguard, needs triage';
  const pairs = nearestMkeyPerCode(line, PREFIXES);
  assert.equal(pairs.get('BUG-136'), 'tool.authorguard');
});

test('nearestMkeyPerCode does NOT false-DENY a true multi-code sentence sharing one candidate (BUG-142 exact fixture)', () => {
  const line = 'FEAT-072 and FEAT-073 both touch tool.dispatchguard';
  const pairs = nearestMkeyPerCode(line, PREFIXES);
  // Neither code is directly attached to the shared candidate — "and
  // FEAT-073 both touch" / "both touch" are not recognised connectors —
  // so both are correctly left unchecked rather than falsely flagged.
  assert.ok(!pairs.has('FEAT-072'));
  assert.ok(!pairs.has('FEAT-073'));
});

test('nearestMkeyPerCode still resolves the plain single-code/single-candidate parenthetical case (BUG-135 regression)', () => {
  const pairs = nearestMkeyPerCode('citing FEAT-072 (tool.authorguard) by mistake', PREFIXES);
  assert.equal(pairs.get('FEAT-072'), 'tool.authorguard');
  assert.equal(pairs.size, 1);
});

test('nearestMkeyPerCode resolves a colon-attached assertion', () => {
  const pairs = nearestMkeyPerCode('FEAT-072: tool.authorguard is wrong', PREFIXES);
  assert.equal(pairs.get('FEAT-072'), 'tool.authorguard');
});

test('nearestMkeyPerCode resolves a dash-attached assertion', () => {
  const pairs = nearestMkeyPerCode('FEAT-072 - tool.authorguard', PREFIXES);
  assert.equal(pairs.get('FEAT-072'), 'tool.authorguard');
});

test('nearestMkeyPerCode resolves a bare-copula assertion', () => {
  const pairs = nearestMkeyPerCode('FEAT-072 is tool.authorguard, apparently', PREFIXES);
  assert.equal(pairs.get('FEAT-072'), 'tool.authorguard');
});

test('nearestMkeyPerCode resolves this project\'s own dispatch-brief citation style', () => {
  const pairs = nearestMkeyPerCode(
    'FEAT-072 (tool.dispatchguard, claude-dispatch-guard.js) after a junior fixed BUG-135',
    PREFIXES
  );
  assert.equal(pairs.get('FEAT-072'), 'tool.dispatchguard');
});

test('nearestMkeyPerCode ignores an unattached candidate that merely happens to be nearby', () => {
  // tool.two sits right after FEAT-072's parenthetical closes, with no
  // connector of its own -- BUG-142's whole point is that bare proximity
  // (formerly picked by raw distance) is no longer sufficient on its own.
  const line = 'FEAT-072 (tool.one) tool.two';
  const prefixes = new Set(['tool']);
  const pairs = nearestMkeyPerCode(line, prefixes);
  assert.equal(pairs.get('FEAT-072'), 'tool.one');
});

test('nearestMkeyPerCode is a no-op when the line has codes but no candidate mkeys', () => {
  const pairs = nearestMkeyPerCode('dispatch FEAT-072 and BUG-136 together', PREFIXES);
  assert.equal(pairs.size, 0);
});

test('nearestMkeyPerCode is a no-op when the line has candidates but no codes', () => {
  const pairs = nearestMkeyPerCode('see tool.authorguard for detail', PREFIXES);
  assert.equal(pairs.size, 0);
});

// --- FEAT-076 (tool.agentlog) AC-7: resolveIdentity's file-then-env-then- ---
// --- fallback chain, mirroring claude-statusline.js. Exercised against    ---
// --- throwaway fixture directories — never this machine's real .claude.   ---

test('resolveIdentity prefers the per-session file over the shared file and env when all three disagree', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'dispatch-guard-identity-'));
  try {
    fs.writeFileSync(path.join(dir, '.identity-sess-1'), 'bill');
    fs.writeFileSync(path.join(dir, '.identity'), 'bob');
    const prevEnv = process.env.CLAUDE_IDENTITY;
    process.env.CLAUDE_IDENTITY = 'ben';
    try {
      assert.equal(resolveIdentity(dir, 'sess-1'), 'bill');
    } finally {
      if (prevEnv === undefined) delete process.env.CLAUDE_IDENTITY; else process.env.CLAUDE_IDENTITY = prevEnv;
    }
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('resolveIdentity falls back to the shared .identity file when no per-session file exists', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'dispatch-guard-identity-'));
  try {
    fs.writeFileSync(path.join(dir, '.identity'), 'bob');
    assert.equal(resolveIdentity(dir, 'sess-missing'), 'bob');
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('resolveIdentity falls back to CLAUDE_IDENTITY env when neither file exists', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'dispatch-guard-identity-'));
  try {
    const prevEnv = process.env.CLAUDE_IDENTITY;
    process.env.CLAUDE_IDENTITY = 'ben';
    try {
      assert.equal(resolveIdentity(dir, 'sess-1'), 'ben');
    } finally {
      if (prevEnv === undefined) delete process.env.CLAUDE_IDENTITY; else process.env.CLAUDE_IDENTITY = prevEnv;
    }
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('resolveIdentity falls back to the literal "lead" when no file and no env exist', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'dispatch-guard-identity-'));
  try {
    const prevEnv = process.env.CLAUDE_IDENTITY;
    delete process.env.CLAUDE_IDENTITY;
    try {
      assert.equal(resolveIdentity(dir, 'sess-1'), 'lead');
    } finally {
      if (prevEnv !== undefined) process.env.CLAUDE_IDENTITY = prevEnv;
    }
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('resolveIdentity works with no sessionId at all (falls straight to shared file, then env, then lead)', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'dispatch-guard-identity-'));
  try {
    fs.writeFileSync(path.join(dir, '.identity'), 'bob');
    assert.equal(resolveIdentity(dir, undefined), 'bob');
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

// --- FEAT-076 AC-8: connect() is exported and defaults host to 127.0.0.1 ---
// --- (parity with claude-sync.js/claude-bow.js — no more 'localhost').   ---

test('connect() is exported as a function', () => {
  assert.equal(typeof connect, 'function');
});

test('connect() defaults host to 127.0.0.1 when METRO_DB_HOST is unset (AC-8)', async () => {
  const mysql = require('mysql2/promise');
  const originalCreateConnection = mysql.createConnection;
  const prevHost = process.env.METRO_DB_HOST;
  let capturedOptions = null;
  mysql.createConnection = async (opts) => { capturedOptions = opts; return { end: async () => {} }; };
  delete process.env.METRO_DB_HOST;
  try {
    await connect();
  } finally {
    mysql.createConnection = originalCreateConnection;
    if (prevHost !== undefined) process.env.METRO_DB_HOST = prevHost;
  }
  assert.ok(capturedOptions, 'createConnection should have been called');
  assert.equal(capturedOptions.host, '127.0.0.1');
});

test('BOW_CODE_RE-equivalent lowercase code is matched case-insensitively', () => {
  // The guard's own regex is internal, but its /gi behavior is verified via
  // the same pattern shape here since candidateMkeys/extractOwnedPaths don't
  // touch code matching directly — this locks the case-insensitive contract.
  const re = /\b(MOD|FEAT|BUG|SEC|INT|ASM)-(\d{3,})\b/gi;
  const matches = [...'dispatch on feat-072 and Mod-070'.matchAll(re)].map((m) => m[0].toUpperCase());
  assert.deepEqual(matches, ['FEAT-072', 'MOD-070']);
});

// ---------------------------------------------------------------------------
// BUG-205 regression — the gate must read the REAL hook payload field.
// Live payloads carry `tool_name`; the guard read only `tool`, so every real
// dispatch early-allow()ed before ANY check or the FEAT-076 insert ran (0
// dispatch rows ever landed live vs 114 stops). These tests spawn the real
// script with a dead DB port: a payload that PASSES the gate reaches
// connect(), which throws, producing the fail-open stderr note — a payload
// that is early-allowed produces NO stderr. That stderr difference proves
// which side of the gate a shape lands on, with no live DB needed. This is
// exactly the end-to-end coverage whose absence let BUG-205 ship.
// ---------------------------------------------------------------------------
const { spawnSync: spawnGuard } = require('child_process');

function runGuardWithDeadDb(payload) {
  return spawnGuard(process.execPath, [path.join(__dirname, 'claude-dispatch-guard.js')], {
    input: JSON.stringify(payload),
    encoding: 'utf8',
    env: { ...process.env, METRO_DB_PORT: '1', CLAUDE_DISABLE_DISPATCH_GUARD: '' },
  });
}

test('BUG-205: a real tool_name-shaped payload passes the gate (reaches connect, fail-open stderr) instead of being silently early-allowed', () => {
  const r = runGuardWithDeadDb({
    tool_name: 'Agent',
    hook_event_name: 'PreToolUse',
    session_id: 'bug205-test-session',
    tool_input: { prompt: 'do real work', subagent_type: 'general-purpose', description: 'x' },
  });
  assert.equal(r.status, 0, 'guard must stay fail-open');
  assert.match(
    r.stderr,
    /allowing dispatch|cannot connect|ECONNREFUSED/i,
    'a tool_name payload must get PAST the gate to the DB step — empty stderr means the BUG-205 early-allow is back'
  );
});

test('BUG-205: the legacy tool-shaped payload still passes the gate (crafted-payload tests stay valid)', () => {
  const r = runGuardWithDeadDb({
    tool: 'Agent',
    session_id: 'bug205-test-session-2',
    tool_input: { prompt: 'do real work', subagent_type: 'general-purpose', description: 'x' },
  });
  assert.equal(r.status, 0);
  assert.match(r.stderr, /allowing dispatch|cannot connect|ECONNREFUSED/i);
});

test('BUG-205: a non-Agent tool_name payload is still ignored (no DB attempt, no stderr)', () => {
  const r = runGuardWithDeadDb({
    tool_name: 'Bash',
    session_id: 'bug205-test-session-3',
    tool_input: { prompt: 'irrelevant' },
  });
  assert.equal(r.status, 0);
  assert.equal(r.stderr, '', 'a non-Agent payload must exit at the gate without touching the DB');
});
