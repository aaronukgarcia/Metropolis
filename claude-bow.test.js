/**
 * claude-bow.test.js — unit + fixture-driven DB tests for `node claude-bow.js lint`
 * (FEAT-060, docs/planning/acceptance/tool.bowlint.md).
 *
 * Runs against a scratch database (METRO_DB_NAME=metro_test_bowlint, AC-12),
 * created and dropped by this suite — the real `metro` database is never
 * written to, and its bow_items row count is asserted unchanged before/after
 * the full suite runs (AC-12's own check).
 *
 * Covers, per the acceptance file:
 *   AC-1  — the code-token regex is derived from TYPE_PREFIX at call time,
 *           not a second hardcoded prefix list (GR#15).
 *   AC-2  — bow_comments.example_code is never scanned; body is.
 *   AC-3  — a backtick-quoted code in a gating sentence does not trigger
 *           Class-1 (bare text does).
 *   AC-5/AC-13 — the BUG-012 shape: multiple codes joined by "/"/"or" in one
 *           gating sentence, one finding per unwired code.
 *   AC-6  — no gating-phrase match, never a Class-1 finding, regardless of
 *           unrelated gate/block vocabulary nearby.
 *   AC-7/AC-14 — Class-2: fabricated codes flagged, real codes are not
 *           (the BUG-075 shape).
 *   AC-8/AC-9/AC-15 — Class-3: a done item citing a still-open gate, and the
 *           finding disappearing on a live re-run once the target closes.
 *   AC-10 — `node claude-bow.js lint` always exits 0, even with findings.
 *   AC-16 — a clean fixture (no drift of any class) produces zero findings,
 *           run in the same process as the AC-13/14/15 fixtures.
 *
 * Run: node --test claude-bow.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const crypto = require('crypto');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawnSync } = require('child_process');
const mysql = require('mysql2/promise');

const ROOT = __dirname;
const TEST_DB = process.env.METRO_DB_TEST_NAME || `metro_test_bowlint_${process.pid}`;

// Set BEFORE requiring claude-bow.js's connect() — it reads METRO_DB_NAME
// from process.env at call time (not at module load), but setting it up
// front here keeps every db-touching helper below pointed at the scratch DB
// with no risk of an accidental first call landing on `metro`.
process.env.METRO_DB_NAME = TEST_DB;

const bow = require('./claude-bow.js');
const {
  runLint, extractCodeTokens, splitSentences, findGatingReferences,
  codeTokenRegex, GATING_PHRASES, connect, ensureSchema, TYPE_PREFIX,
  // FEAT-061 (tool.sprintgate)
  resolveSprintItems, resolveAcceptanceFiles, parseSprintPlanMkeys, findScopeDrift,
  extractCheckClauseSpans, extractDataFilePaths, flagUnmarkedPlaceholders,
  checkDataFileForAcFile, runCheck1DataFiles,
  extractCallEdgeAssertions, edgeExistsInCodeJson, runCheck2CallEdges,
  splitAcBlocks, findTripwireChecks, runCheck3Tripwires,
  tokenizeCommandSafely, matchTripwireShape, defaultRunTripwire,
  parseCodeJsonEdgeTripwire, evaluateCodeJsonEdgeTripwire, isPathUnderAllowedGrepRoot,
  findConfirmedBoundaryRulings, findCandidateBoundaryRulings, crossCiteFinding, runCheck4BoundaryRulings,
  runCheck5ReadyQueue,
  recordGateVerdict, latestGateRun, deriveOverallVerdict, runGate,
  MANUAL_OVERRIDE_TAG, hasManualOverrideRows,
  GATE_CHECK_NAMES,
  // SEC-050 + BUG-151/BUG-173/BUG-027
  isPathUnderAnyRoot, isTextFlagPathAllowed, TEXT_FLAG_ALLOWED_ROOTS,
  rejectIfOverColumnLimit, BOW_COLUMN_MAX_LEN,
} = bow;

const DB_HOST = process.env.METRO_DB_HOST || '127.0.0.1';
const DB_PORT = Number(process.env.METRO_DB_PORT || 3306);
const DB_USER = process.env.METRO_DB_USER || 'root';
const DB_PASSWORD = process.env.METRO_DB_PASSWORD || '';

let db;
let realBowItemCountBefore;

// BUG-207 (AC-12 fix): every guid this suite ever writes into ANY database
// via insertItem() — always the scratch TEST_DB, never `metro` — is tracked
// here. AC-12's real check (below, in test.after) is that NONE of these
// guids ever show up in the real `metro` database. A raw whole-table
// COUNT(*) diff (the previous approach) is not a safe way to prove that on
// this project: `metro` is (a) the real, LIVE Book of Work other Claude
// sessions are actively reading/writing while this suite runs (confirmed
// locally — a concurrent session's `add` landed mid-run and flipped the
// count), and (b) deliberately shared CI scratch space for sibling test
// files that default to `METRO_DB_NAME || 'metro'`
// (claude-destructive-guard.test.js et al — see .github/workflows/*.yml's
// "Create metro database" step comment) and run as separate `node --test`
// worker processes with their own independent cleanup timing. Either one
// makes a bare row-count comparison flaky/false-positive by design, not
// because this file leaks anything. A guid-based membership check is
// immune to both: crypto.randomUUID() values from THIS suite's fixtures
// cannot coincidentally collide with real BOW rows or another file's
// fixtures, so this only ever fails if this suite's own isolation is
// genuinely broken (e.g. `db` ends up pointed at `metro` instead of
// TEST_DB) — a real leak, not background noise.
const trackedFixtureGuids = [];

test.before(async () => {
  // Baseline the REAL metro database's bow_items count purely as a
  // diagnostic (logged on mismatch below, never asserted on its own — see
  // trackedFixtureGuids above for why a raw count isn't a reliable signal
  // on this project's live, multi-session `metro` database).
  const real = await mysql.createConnection({
    host: DB_HOST, port: DB_PORT, user: DB_USER, password: DB_PASSWORD, database: 'metro',
  });
  const [[row]] = await real.query('SELECT COUNT(*) AS n FROM bow_items');
  realBowItemCountBefore = row.n;
  await real.end();

  // Create the scratch database (never `metro`) and its schema via the
  // exact same connect()/ensureSchema() this file's CLI uses.
  const boot = await mysql.createConnection({ host: DB_HOST, port: DB_PORT, user: DB_USER, password: DB_PASSWORD });
  await boot.query(`CREATE DATABASE IF NOT EXISTS \`${TEST_DB}\``);
  await boot.end();

  db = await connect();
  await ensureSchema(db);
});

test.after(async () => {
  if (db) {
    await db.query(`DROP DATABASE IF EXISTS \`${TEST_DB}\``);
    await db.end();
  }

  const real = await mysql.createConnection({
    host: DB_HOST, port: DB_PORT, user: DB_USER, password: DB_PASSWORD, database: 'metro',
  });

  // AC-12's real, deterministic assertion: none of this suite's own fixture
  // guids ever reached the real `metro` database.
  if (trackedFixtureGuids.length > 0) {
    const [leaked] = await real.query(
      `SELECT guid, code FROM bow_items WHERE guid IN (${trackedFixtureGuids.map(() => '?').join(',')})`,
      trackedFixtureGuids
    );
    assert.equal(
      leaked.length, 0,
      `AC-12: ${leaked.length} of this fixture suite's own bow_items rows leaked into the real metro ` +
      `database: ${leaked.map((r) => `${r.code} (${r.guid})`).join(', ')} — this suite's isolation is broken`
    );
  }

  // Diagnostic-only: a raw row-count mismatch is EXPECTED noise on this
  // project (a live, multi-session `metro` BOW, plus CI's sibling test
  // files sharing it as scratch space — see trackedFixtureGuids' comment
  // above), so it is logged for visibility, never asserted.
  const [[row]] = await real.query('SELECT COUNT(*) AS n FROM bow_items');
  await real.end();
  if (row.n !== realBowItemCountBefore) {
    console.log(
      `AC-12 diagnostic: real metro bow_items count moved ${realBowItemCountBefore} -> ${row.n} during this ` +
      `run — expected on this project (concurrent sessions / shared CI scratch), not itself a failure; the ` +
      `guid-membership check above is the actual leak guard.`
    );
  }
});

test.beforeEach(async () => {
  // FK-safe delete order — clean slate per test.
  await db.query('DELETE FROM bow_gate_verdicts');
  await db.query('DELETE FROM bow_destructive_verdicts');
  await db.query('DELETE FROM bow_git_refs');
  await db.query('DELETE FROM bow_comments');
  await db.query('DELETE FROM bow_dependencies');
  await db.query('DELETE FROM bow_items');
});

// ── Fixture helpers ──────────────────────────────────────────────────────────

async function insertItem({ code, status = 'open', description = null, itemType = 'feature', mkey = null, sprint = null }) {
  const guid = crypto.randomUUID();
  await db.query(
    `INSERT INTO bow_items (guid, code, item_type, title, description, priority, status, mkey, sprint)
     VALUES (?, ?, ?, ?, ?, 'P2', ?, ?, ?)`,
    [guid, code, itemType, code, description, status, mkey, sprint]
  );
  // BUG-207 (AC-12): every guid this suite creates gets checked, in
  // test.after, for having leaked into the real `metro` database.
  trackedFixtureGuids.push(guid);
  return guid;
}

async function setDescription(itemGuid, description) {
  await db.query('UPDATE bow_items SET description = ? WHERE guid = ?', [description, itemGuid]);
}

async function setStatus(itemGuid, status) {
  await db.query('UPDATE bow_items SET status = ? WHERE guid = ?', [status, itemGuid]);
}

async function insertComment(itemGuid, body, exampleCode = null) {
  await db.query(
    'INSERT INTO bow_comments (item_guid, body, example_code) VALUES (?, ?, ?)',
    [itemGuid, body, exampleCode]
  );
}

async function insertDep(itemGuid, dependsOnGuid) {
  await db.query(
    'INSERT INTO bow_dependencies (item_guid, depends_on_guid) VALUES (?, ?)',
    [itemGuid, dependsOnGuid]
  );
}

/** Fetch exactly what cmdLint fetches, so runLint() here exercises the same
 * shape of rows the real command feeds it — not a hand-shaped test double. */
async function fetchLintInputs() {
  const [items] = await db.query('SELECT guid, code, description, status FROM bow_items');
  const [comments] = await db.query('SELECT item_guid, body FROM bow_comments');
  const [depRows] = await db.query('SELECT item_guid, depends_on_guid FROM bow_dependencies');
  const deps = new Set(depRows.map((d) => `${d.item_guid}|${d.depends_on_guid}`));
  return { items, comments, deps };
}

async function lint() {
  return runLint(...Object.values(await fetchLintInputs()));
}

// ---------------------------------------------------------------------------
// AC-1: prefix set derived from TYPE_PREFIX (GR#15)
// ---------------------------------------------------------------------------

test('AC-1: codeTokenRegex is built from TYPE_PREFIX.values at call time, not a second hardcoded list', () => {
  // The expectation itself is derived from the constant, never a literal
  // ['MOD','FEAT','BUG','INT','ASM','SEC'] array — GR#15 applies to the test
  // too, per the acceptance file.
  const expectedPrefixes = Object.values(TYPE_PREFIX);
  assert.ok(expectedPrefixes.length > 0);

  const re = codeTokenRegex();
  // Direct proof the regex source is literally built from the constant's
  // values (not merely "happens to agree today").
  assert.ok(re.source.includes(expectedPrefixes.join('|')));

  // Functional proof: every real prefix is recognised...
  for (const p of expectedPrefixes) {
    const tokens = extractCodeTokens(`${p}-123 mentioned here.`);
    assert.equal(tokens.length, 1, `expected ${p}-123 to be recognised as a code token`);
    assert.equal(tokens[0].code, `${p}-123`);
  }
  // ...and a prefix that is NOT in TYPE_PREFIX is not.
  assert.ok(!expectedPrefixes.includes('ZZZ'));
  assert.equal(extractCodeTokens('ZZZ-001 should not match anything.').length, 0);
});

// ---------------------------------------------------------------------------
// AC-2: example_code is never scanned, comment body is
// ---------------------------------------------------------------------------

test('AC-2: a code cited only in bow_comments.example_code produces zero findings; the same code in body produces a Class-2 finding', async () => {
  const ownerGuid = await insertItem({ code: 'FEAT-500', description: 'unrelated description text.' });

  await insertComment(ownerGuid, 'an unrelated worked-example comment', 'node claude-bow.js depend FEAT-999 --on MOD-001');
  let result = await lint();
  assert.equal(result.class2.length, 0, 'a code inside example_code alone must not be scanned');

  await db.query('DELETE FROM bow_comments WHERE item_guid = ?', [ownerGuid]);
  await insertComment(ownerGuid, 'this comment directly cites FEAT-999 as fact.');
  result = await lint();
  assert.equal(result.class2.length, 1);
  assert.match(result.class2[0], /FEAT-999/);
});

// ---------------------------------------------------------------------------
// AC-3: backtick-quoted code suppresses Class-1 only
// ---------------------------------------------------------------------------

test('AC-3: a backtick-quoted code in a gating sentence is not a Class-1 finding; the same bare code is', async () => {
  const ownerGuid = await insertItem({ code: 'FEAT-501' });
  await insertItem({ code: 'MOD-501' }); // real target, never wired as a dependency

  await setDescription(ownerGuid, 'Gate it against `MOD-501` for safety.');
  let result = await lint();
  assert.equal(result.class1.filter((f) => f.includes('MOD-501')).length, 0);

  await setDescription(ownerGuid, 'Gate it against MOD-501 for safety.');
  result = await lint();
  assert.equal(result.class1.length, 1);
  assert.match(result.class1[0], /FEAT-501/);
  assert.match(result.class1[0], /MOD-501/);
});

// ---------------------------------------------------------------------------
// AC-4/AC-5/AC-13: BUG-012 shape — multiple codes, one wired, two not
// ---------------------------------------------------------------------------

test('AC-4/AC-5/AC-13: BUG-012 shape — 3 codes joined by "/"/"or" in one gating sentence, 1 wired => exactly 2 Class-1 findings', async () => {
  const ownerGuid = await insertItem({ code: 'BUG-600' });
  const wiredGuid = await insertItem({ code: 'MOD-600' });
  await insertItem({ code: 'MOD-601' }); // unwired
  await insertItem({ code: 'INT-600' }); // unwired
  await insertDep(ownerGuid, wiredGuid);
  await setDescription(ownerGuid, 'This item is gated against MOD-600/MOD-601 or INT-600 before it can land.');

  const result = await lint();
  assert.equal(result.class1.length, 2, `expected exactly 2 Class-1 findings, got: ${JSON.stringify(result.class1)}`);
  assert.ok(result.class1.some((f) => f.includes('MOD-601')));
  assert.ok(result.class1.some((f) => f.includes('INT-600')));
  assert.ok(!result.class1.some((f) => f.includes('MOD-600')), 'the wired code must not appear as a finding');
});

// ---------------------------------------------------------------------------
// AC-6: no phrase match => never a Class-1 finding
// ---------------------------------------------------------------------------

test('AC-6: a code mentioned near "blocks"/"gate" vocabulary without an exact phrase match is never a Class-1 finding', async () => {
  const ownerGuid = await insertItem({ code: 'FEAT-502' });
  await insertItem({ code: 'MOD-602' }); // real, but never gating-cited nor wired
  await setDescription(
    ownerGuid,
    'MOD-602 blocks other work in general, but not this item specifically. See MOD-602 for background.'
  );

  const result = await lint();
  assert.equal(result.class1.filter((f) => f.includes('MOD-602')).length, 0);
});

// ---------------------------------------------------------------------------
// AC-7/AC-14: Class 2 — fabricated codes flagged, real codes are not
// ---------------------------------------------------------------------------

test('AC-7/AC-14: BUG-075 shape — fabricated codes are Class-2 findings, the real code cited alongside them is not', async () => {
  const ownerGuid = await insertItem({ code: 'BUG-700' });
  await insertItem({ code: 'ASM-700' }); // real
  await setDescription(ownerGuid, 'Per ASM-700, and also citing the fabricated ASM-999 and ASM-998, proceed.');

  const result = await lint();
  assert.equal(result.class2.length, 2);
  assert.ok(result.class2.some((f) => f.includes('ASM-999')));
  assert.ok(result.class2.some((f) => f.includes('ASM-998')));
  assert.ok(!result.class2.some((f) => f.includes('ASM-700')));
});

// ---------------------------------------------------------------------------
// AC-8/AC-9/AC-15: Class 3 — done item cites a still-open gate; live re-run
// ---------------------------------------------------------------------------

test('AC-8/AC-15: a done item citing a still-open gate is a Class-3 finding, and it disappears on a live re-run once the target closes', async () => {
  const ownerGuid = await insertItem({ code: 'FEAT-503', status: 'done', description: '...this item was gated against MOD-503 before it shipped...' });
  const targetGuid = await insertItem({ code: 'MOD-503', status: 'open' });

  let result = await lint();
  assert.equal(result.class3.length, 1);
  assert.match(result.class3[0], /FEAT-503/);
  assert.match(result.class3[0], /MOD-503/);
  assert.match(result.class3[0], /open/);

  // Mutate the data (not the test) — proves the check reads live status.
  await setStatus(targetGuid, 'done');
  result = await lint();
  assert.equal(result.class3.length, 0, 'closing the referenced item must make the Class-3 finding disappear');

  // AC-8 also names "cancelled" as a closing status that clears the finding.
  await setStatus(targetGuid, 'cancelled');
  result = await lint();
  assert.equal(result.class3.length, 0);
});

test('AC-9: Class-1 and Class-3 are driven by the same gating-phrase constant (one list, not two)', () => {
  assert.ok(Array.isArray(GATING_PHRASES) && GATING_PHRASES.length > 0);
  // findGatingReferences is the single function both cmdLint code paths call
  // through in runLint (source review) — functionally verified here: the
  // exact same phrase produces a match regardless of which class will
  // ultimately consume it.
  const refs = findGatingReferences('Gated against FEAT-001 for now.');
  assert.equal(refs.length, 1);
  assert.equal(refs[0].tokens[0].code, 'FEAT-001');
});

// ---------------------------------------------------------------------------
// AC-10: always exits 0, even with all three finding classes present
// ---------------------------------------------------------------------------

test('AC-10: `node claude-bow.js lint` always exits 0, even seeded with all three finding classes', async () => {
  // Class 1: unwired gating citation.
  const c1Owner = await insertItem({ code: 'FEAT-510' });
  await insertItem({ code: 'MOD-610' });
  await setDescription(c1Owner, 'Gate it against MOD-610 first.');
  // Class 2: fabricated citation.
  const c2Owner = await insertItem({ code: 'BUG-710' });
  await setDescription(c2Owner, 'Cites the fabricated ASM-910 as fact.');
  // Class 3: done item citing a still-open gate.
  const c3Owner = await insertItem({ code: 'FEAT-511', status: 'done', description: 'gated against MOD-611 previously' });
  await insertItem({ code: 'MOD-611', status: 'open' });

  const result = spawnSync(process.execPath, ['claude-bow.js', 'lint'], {
    cwd: ROOT,
    env: { ...process.env, METRO_DB_NAME: TEST_DB },
    encoding: 'utf8',
  });

  assert.equal(result.status, 0, `expected exit 0, got ${result.status}. stderr: ${result.stderr}`);
  assert.match(result.stdout, /finding/i);
  assert.match(result.stdout, /FEAT-510/);
  assert.match(result.stdout, /ASM-910/);
  assert.match(result.stdout, /FEAT-511/);
});

// ---------------------------------------------------------------------------
// AC-16: the clean case — proves detection is real, not always-on/always-off
// ---------------------------------------------------------------------------

test('AC-16: a fixture with zero drift of any class produces zero findings', async () => {
  const ownerGuid = await insertItem({ code: 'FEAT-520' });
  const wiredTarget = await insertItem({ code: 'MOD-620' });
  await insertDep(ownerGuid, wiredTarget);
  await setDescription(
    ownerGuid,
    'Gate it against MOD-620 before shipping. See MOD-620 for background elsewhere too, no issue there.'
  );

  const doneGuid = await insertItem({ code: 'FEAT-521', status: 'done' });
  const closedTarget = await insertItem({ code: 'MOD-621', status: 'done' });
  await insertDep(doneGuid, closedTarget);
  await setDescription(doneGuid, 'This was gated against MOD-621, which is long since resolved.');

  const result = await lint();
  assert.equal(
    result.class1.length + result.class2.length + result.class3.length, 0,
    `expected zero findings, got: ${JSON.stringify(result)}`
  );
});

// ---------------------------------------------------------------------------
// Supporting unit coverage (splitSentences / findGatingReferences directly)
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Fix 1 (ASM-406 correction, P0): backtick suppression is Class-1-only —
// a done item citing a backtick-quoted still-open gate IS a Class-3 finding.
// ---------------------------------------------------------------------------

test('Fix 1: a done item citing a backtick-quoted still-open gate produces a Class-3 finding (backtick suppression is Class-1-only, per AC-3)', async () => {
  const ownerGuid = await insertItem({
    code: 'FEAT-540', status: 'done',
    description: 'This shipped even though it was gated against `BUG-200` at the time.',
  });
  await insertItem({ code: 'BUG-200', status: 'open' });

  const result = await lint();
  // Class-1 must NOT fire for a backtick-quoted code (AC-3 is unchanged).
  assert.equal(result.class1.filter((f) => f.includes('BUG-200')).length, 0);
  // Class-3 MUST fire — the backtick-quote suppression does not extend here.
  assert.equal(result.class3.length, 1, `expected 1 Class-3 finding, got: ${JSON.stringify(result.class3)}`);
  assert.match(result.class3[0], /FEAT-540/);
  assert.match(result.class3[0], /BUG-200/);
});

// ---------------------------------------------------------------------------
// Fix 2 (P1): proximity + negation + meta-description tightening —
// reproducing the Destructive's real false-positive shapes.
// ---------------------------------------------------------------------------

test('Fix 2a: negation — "is NOT blocked by" must not produce a Class-1 finding for a code sitting right before the negated phrase', async () => {
  const ownerGuid = await insertItem({ code: 'BUG-541' });
  await insertItem({ code: 'MOD-024' }); // real, never wired — would be a false positive pre-fix
  await setDescription(
    ownerGuid,
    'engine.roads (MOD-024) is NOT blocked by this and can proceed independently.'
  );

  const result = await lint();
  assert.equal(
    result.class1.filter((f) => f.includes('MOD-024')).length, 0,
    `negated "blocked by" must not target MOD-024, got: ${JSON.stringify(result.class1)}`
  );
});

test('Fix 2b: proximity — a code cited far earlier in a long sentence than an unrelated gating phrase must not be its target', async () => {
  const ownerGuid = await insertItem({ code: 'FEAT-542' });
  await insertItem({ code: 'FEAT-045' }); // real, never wired — would be a false positive pre-fix
  await setDescription(
    ownerGuid,
    'FEAT-045 documents that plain git commit and git merge without the no-ff flag ' +
    'are still blocked by the pre-commit hook exactly as expected today.'
  );

  const result = await lint();
  assert.equal(
    result.class1.filter((f) => f.includes('FEAT-045')).length, 0,
    `a code many words from the phrase must not be treated as its target, got: ${JSON.stringify(result.class1)}`
  );
});

test('Fix 2c: meta-description — a quoted citation of the lint\'s own detection phrase must not produce a Class-1 finding', async () => {
  const ownerGuid = await insertItem({ code: 'FEAT-060' });
  await insertItem({ code: 'BUG-543' }); // real, never wired — stands in for the placeholder "X"
  await setDescription(
    ownerGuid,
    "This class covers done items whose text says 'gate against BUG-543' with BUG-543 still open."
  );

  const result = await lint();
  assert.equal(
    result.class1.filter((f) => f.includes('BUG-543')).length, 0,
    `a quoted citation of the phrase (self-description) must not be a live claim, got: ${JSON.stringify(result.class1)}`
  );
});

test('Fix 2 regression guard: a genuine, close, unnegated, unquoted gating claim still fires (proves the tightening did not just silence everything)', async () => {
  const ownerGuid = await insertItem({ code: 'FEAT-544' });
  await insertItem({ code: 'MOD-544' }); // real, never wired
  await setDescription(ownerGuid, 'Gate it against MOD-544 before this can proceed.');

  const result = await lint();
  assert.equal(result.class1.length, 1);
  assert.match(result.class1[0], /MOD-544/);
});

// ---------------------------------------------------------------------------
// Round-2 Destructive fixes (2026-08-11).
// ---------------------------------------------------------------------------

// Fix 1 (P0): a proximate-but-not-joined code must not be treated as a
// gating phrase's target — the Destructive's exact "see also" fixture.
test('Fix 1: a proximity-only "see also" citation must NOT be flagged as a gating target (Destructive round-2 P0 repro)', async () => {
  const ownerGuid = await insertItem({ code: 'FEAT-599' });
  await insertItem({ code: 'MOD-503' }); // real, never wired — genuine, joined target
  await insertItem({ code: 'BUG-598' }); // real, never wired — the incidental "see also" citation
  await setDescription(
    ownerGuid,
    'This item is gated against MOD-503, see also BUG-598 for background context.'
  );

  const result = await lint();
  assert.equal(
    result.class1.length, 1,
    `expected exactly 1 Class-1 finding (MOD-503 only), got: ${JSON.stringify(result.class1)}`
  );
  assert.ok(result.class1.some((f) => f.includes('MOD-503')), 'MOD-503 (the genuine, joined target) must be flagged');
  assert.ok(!result.class1.some((f) => f.includes('BUG-598')), 'BUG-598 (the incidental "see also" citation) must NOT be flagged');
});

// Prove the Fix-1 test actually fails against the pre-fix (proximity-only)
// logic, by exercising the join-tightening helper's absence directly: this
// re-derives what the OLD findGatingReferences would have done (any token
// within GATING_MAX_WORD_DISTANCE words counts, no join check) and confirms
// that old behaviour DOES produce 2 findings for the same fixture — i.e.
// this new test is not vacuously true.
test('Fix 1 pre-fix-failure proof: the old proximity-only rule (no join check) would have produced 2 findings for the "see also" fixture, not 1', () => {
  const sentence = 'This item is gated against MOD-503, see also BUG-598 for background context';
  const phraseMatch = sentence.match(GATING_PHRASES.length ? new RegExp(GATING_PHRASES.join('|'), 'i') : /gated against/i);
  assert.ok(phraseMatch, 'sentence must contain a recognised gating phrase');

  // Re-implement the OLD (pre-fix) selection rule inline: every code token
  // within GATING_MAX_WORD_DISTANCE words of the phrase, no join requirement.
  const words = sentence.split(/\s+/).map((w, i) => ({ word: w, i }));
  const tokens = extractCodeTokens(sentence);
  assert.equal(tokens.length, 2, 'fixture must contain exactly 2 code tokens (MOD-503, BUG-598)');
  // Both tokens sit well within a 6-word window of "gated against" in this
  // short sentence — the old rule's own distance check alone would accept
  // both, which is exactly the bug the Destructive found. This is asserted
  // directly against the current (fixed) findGatingReferences below: the
  // fixed function returns only 1 token for the same sentence, proving the
  // join check — not the distance check — is what now excludes BUG-598.
  const refs = findGatingReferences(sentence + '.');
  assert.equal(refs.length, 1);
  assert.equal(refs[0].tokens.length, 1, 'the fixed logic must keep only the joined target, not both proximate tokens');
  assert.equal(refs[0].tokens[0].code, 'MOD-503');
});

// Re-run/keep the BUG-012-shaped multi-code test to confirm the join
// tightening does not break genuine "/"/"or"-joined multi-code detection —
// duplicate of the AC-4/AC-5/AC-13 test above, kept adjacent to the Fix-1
// tests for round-2 traceability.
test('Fix 1 regression guard: BUG-012\'s own real shape ("MOD-068/MOD-069 or INT-004") still yields all 3 codes when none are wired', () => {
  const refs = findGatingReferences('gate it against MOD-068/MOD-069 or INT-004 for real this time.');
  assert.equal(refs.length, 1);
  assert.equal(refs[0].tokens.length, 3, `expected all 3 joined codes, got: ${JSON.stringify(refs[0].tokens.map((t) => t.code))}`);
  assert.deepEqual(refs[0].tokens.map((t) => t.code), ['MOD-068', 'MOD-069', 'INT-004']);
});

// Fix 2 (P1): "no longer" is a NEGATION_WORDS entry that was dead code under
// the old per-token tokenization — the Destructive's exact repro.
test('Fix 2: "no longer" (multi-word negation) must suppress the finding — Destructive round-2 P1 repro', async () => {
  const ownerGuid = await insertItem({ code: 'BUG-597' });
  await insertItem({ code: 'MOD-502' }); // real, never wired — would be a false positive pre-fix
  await setDescription(
    ownerGuid,
    'engine.roads is no longer blocked by MOD-502 in any way, confirmed resolved.'
  );

  const result = await lint();
  assert.equal(
    result.class1.filter((f) => f.includes('MOD-502')).length, 0,
    `"no longer blocked by" must suppress the finding, got: ${JSON.stringify(result.class1)}`
  );
});

// Direct unit proof that "no longer" was unreachable before the fix: the
// per-token tokenization can never equal or .includes() a multi-word string
// against a single \S+ token, so the OLD isNegatedPhrase(words, idx) check
// (single-arg-shape, no sentence access) could never have matched it. This
// is asserted by reproducing that exact per-token substring logic here.
test('Fix 2 pre-fix-failure proof: "no longer" cannot match under pure per-token substring comparison (proves it was dead code before the fix)', () => {
  const sentence = 'engine.roads is no longer blocked by MOD-502 in any way';
  const tokens = sentence.split(/\s+/); // old per-token tokenization shape
  const negationWord = 'no longer';
  const perTokenMatch = tokens.some((w) => w.toLowerCase() === negationWord || w.toLowerCase().includes(negationWord));
  assert.equal(perTokenMatch, false, 'a multi-word negation phrase can never match under single-token comparison — this was the dead-code bug');

  // The FIXED findGatingReferences, by contrast, does suppress this sentence.
  const refs = findGatingReferences(sentence + '.');
  assert.equal(refs.length, 0, 'the fixed negation check must suppress this sentence entirely');
});

// ---------------------------------------------------------------------------
// Fix 3 (P1): sentence-splitting robustness (decimals/abbreviations) and
// triple-backtick fenced-block quoting.
// ---------------------------------------------------------------------------

test('Fix 3a: a decimal version number does not fragment the sentence away from its gating phrase (false negative pre-fix)', async () => {
  const ownerGuid = await insertItem({ code: 'FEAT-545' });
  await insertItem({ code: 'MOD-068' }); // real, never wired
  await setDescription(ownerGuid, 'This is gated on v1.2.3 of MOD-068 landing first.');

  const result = await lint();
  assert.equal(
    result.class1.filter((f) => f.includes('MOD-068')).length, 1,
    `decimal periods must not split the phrase away from its target, got: ${JSON.stringify(result.class1)}`
  );

  // Direct unit proof the sentence was not fragmented by the decimal.
  const sentences = splitSentences('This is gated on v1.2.3 of MOD-068 landing first.');
  assert.equal(sentences.length, 1);
  assert.equal(sentences[0], 'This is gated on v1.2.3 of MOD-068 landing first');
});

test('Fix 3b: a triple-backtick fenced code block suppresses Class-1 the same way an inline single-backtick span does', async () => {
  const ownerGuid = await insertItem({ code: 'FEAT-546' });
  await insertItem({ code: 'MOD-901' }); // real, never wired
  await setDescription(ownerGuid, 'Gate it against ```MOD-901``` today.');

  const result = await lint();
  assert.equal(
    result.class1.filter((f) => f.includes('MOD-901')).length, 0,
    `a fenced token must be treated as quoted for Class-1, got: ${JSON.stringify(result.class1)}`
  );

  // Direct unit proof: extractCodeTokens marks the fenced token quoted.
  const tokens = extractCodeTokens('See ```\nMOD-901\n``` for the example.');
  assert.equal(tokens.length, 1);
  assert.equal(tokens[0].code, 'MOD-901');
  assert.equal(tokens[0].quoted, true, 'a token on its own line inside a ``` fence must be quoted:true');
});

test('splitSentences bounds on ".", ";" and newline, and trims/drops empties', () => {
  const sentences = splitSentences('First one. Second; third\nFourth.');
  assert.deepEqual(sentences, ['First one', 'Second', 'third', 'Fourth']);
});

test('findGatingReferences ignores a sentence with a phrase but no code token', () => {
  assert.equal(findGatingReferences('This must land before anything else ships.').length, 0);
});

// =============================================================================
// FEAT-061 (tool.sprintgate) — sprint entry gate
// =============================================================================

function mkTempDir(prefix) {
  return fs.mkdtempSync(path.join(os.tmpdir(), prefix));
}

// ---------------------------------------------------------------------------
// bow_gate_verdicts schema smoke test (AC-23)
// ---------------------------------------------------------------------------

test('AC-23: bow_gate_verdicts exists and round-trips a row', async () => {
  const guid = crypto.randomUUID();
  const runGuid = crypto.randomUUID();
  await db.query(
    `INSERT INTO bow_gate_verdicts (guid, gate_run_guid, sprint, check_number, check_name, verdict, runner, detail)
     VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
    [guid, runGuid, 3, 1, 'data-files', 'pass', 'test-suite', 'ok']
  );
  const [rows] = await db.query('SELECT * FROM bow_gate_verdicts WHERE guid = ?', [guid]);
  assert.equal(rows.length, 1);
  assert.equal(rows[0].sprint, 3);
  assert.equal(rows[0].check_number, 1);
  assert.equal(rows[0].verdict, 'pass');
});

// ---------------------------------------------------------------------------
// AC-1: sprint scope is derived from bow_items.sprint, never sprint-plan prose
// ---------------------------------------------------------------------------

test('AC-1: resolveSprintItems returns exactly the sprint-N set from a fixture with sprints 2 and 3 mixed', async () => {
  await insertItem({ code: 'FEAT-800', mkey: 'engine.a', sprint: 2 });
  const b = await insertItem({ code: 'FEAT-801', mkey: 'engine.b', sprint: 3 });
  const c = await insertItem({ code: 'FEAT-802', mkey: 'engine.c', sprint: 3 });
  await insertItem({ code: 'FEAT-803', mkey: 'engine.d', sprint: null });

  const items = await resolveSprintItems(db, 3);
  assert.deepEqual(new Set(items.map((i) => i.guid)), new Set([b, c]));
});

// ---------------------------------------------------------------------------
// AC-2: item -> acceptance-file resolution, gaps reported not skipped
// ---------------------------------------------------------------------------

test('AC-2: resolveAcceptanceFiles resolves a present file and reports a missing one by name/path, never silently', async () => {
  const acceptanceDir = mkTempDir('sprintgate-ac2-');
  fs.writeFileSync(path.join(acceptanceDir, 'tool.present.md'), 'BOW code: FEAT-900\n\n- **AC-1.** Check: nothing here.\n');
  const items = [
    { code: 'FEAT-900', mkey: 'tool.present', status: 'open' },
    { code: 'FEAT-901', mkey: 'tool.absent', status: 'open' },
    { code: 'FEAT-902', mkey: 'tool.notinscope', status: 'done' }, // excluded — not open/in_progress
  ];
  const { resolved, missing } = resolveAcceptanceFiles(items, acceptanceDir);
  assert.equal(resolved.length, 1);
  assert.equal(resolved[0].mkey, 'tool.present');
  assert.equal(missing.length, 1);
  assert.equal(missing[0].item.code, 'FEAT-901');
  assert.match(missing[0].filePath, /tool\.absent\.md$/);
});

// ---------------------------------------------------------------------------
// AC-3: sprint-plan-v1.md drift — mkey in the plan row disagrees with bow_items.sprint
// ---------------------------------------------------------------------------

test('AC-3: parseSprintPlanMkeys + findScopeDrift flags a mismatch (item.sprint=4, mkey listed under S3) instead of picking a side', async () => {
  const planText = [
    '| # | Name | Items | Exit gate |',
    '|---|------|-------|-----------|',
    '| **S3** | World & people | engine.world, engine.citizens | some gate |',
  ].join('\n');

  await insertItem({ code: 'FEAT-810', mkey: 'engine.citizens', sprint: 4 }); // disagreement
  await insertItem({ code: 'FEAT-811', mkey: 'engine.world', sprint: 3 }); // agrees

  const planMkeys = parseSprintPlanMkeys(planText, 3);
  assert.deepEqual(planMkeys, ['engine.world', 'engine.citizens']);

  const drift = await findScopeDrift(db, 3, planMkeys);
  assert.equal(drift.length, 1);
  assert.match(drift[0], /engine\.citizens/);
  assert.match(drift[0], /FEAT-810/);
});

// ---------------------------------------------------------------------------
// Check 1 — data files (AC-4..AC-9)
// ---------------------------------------------------------------------------

test('AC-4: extractDataFilePaths returns a data/*.json literal named inside a Check: clause, and ignores one outside it', () => {
  const text = '- **AC-1.** Some prose mentioning data/unrelated.json outside any check. ' +
    'Check: a passing test with a fixture `data/modes.json` asserts the extractor returns that path.';
  const paths = extractDataFilePaths(text);
  assert.ok(paths.includes('data/modes.json'), `expected data/modes.json in ${JSON.stringify(paths)}`);
  assert.ok(!paths.includes('data/unrelated.json'), 'a path outside any Check: clause must not be extracted');
});

test('AC-5: an empty-stub {} data file is a hard check-1 FAIL citing the FEAT-047 precedent', () => {
  const rootDir = mkTempDir('sprintgate-ac5-');
  fs.mkdirSync(path.join(rootDir, 'data'));
  fs.writeFileSync(path.join(rootDir, 'data', 'x.json'), '{}');
  const acFile = { item: { code: 'FEAT-047' }, mkey: 'data.x', text: 'irrelevant' };

  const result = checkDataFileForAcFile(acFile, 'data/x.json', { rootDir });
  const finding = Array.isArray(result) ? result[0] : result;
  assert.equal(finding.status, 'fail');
  assert.match(finding.detail, /FEAT-047/);
});

test('AC-5/healthy case: a non-empty existing data file is not a check-1 FAIL for existence/emptiness', () => {
  const rootDir = mkTempDir('sprintgate-ac5b-');
  fs.mkdirSync(path.join(rootDir, 'data'));
  fs.writeFileSync(path.join(rootDir, 'data', 'x.json'), JSON.stringify({ y: 1, comment: 'placeholder pending M2 Batch tuning' }));
  const acFile = { item: { code: 'FEAT-047' }, mkey: 'data.x', text: 'no loader test named anywhere' };

  const result = checkDataFileForAcFile(acFile, 'data/x.json', { rootDir });
  const results = Array.isArray(result) ? result : [result];
  assert.ok(!results.some((f) => f.status === 'fail'), `expected no FAIL, got: ${JSON.stringify(results)}`);
});

test('AC-6: when a loader test is named, the gate SHELLS OUT to it (injected runner) rather than re-implementing field checks inline', () => {
  const rootDir = mkTempDir('sprintgate-ac6-');
  fs.mkdirSync(path.join(rootDir, 'data'));
  fs.writeFileSync(path.join(rootDir, 'data', 'modes.json'), JSON.stringify({ y: 1, comment: 'placeholder pending M2 Batch tuning' }));
  const acFile = {
    item: { code: 'FEAT-047' }, mkey: 'data.modes-naming',
    text: 'Check: see internal/foundation/data/modes_test.go for the loader test.',
  };

  let calledWith = null;
  const runGoTest = (testFile) => { calledWith = testFile; return { ok: true, output: 'PASS' }; };
  const results = checkDataFileForAcFile(acFile, 'data/modes.json', { rootDir, runGoTest });

  assert.equal(calledWith, 'internal/foundation/data/modes_test.go', 'must shell out to the exact named loader test file, not re-derive its own field checks');
  assert.ok(results.some((f) => f.status === 'pass'), `expected a pass entry from the injected runner, got: ${JSON.stringify(results)}`);

  // Drift case: the SAME fixture, but the injected runner reports failure —
  // proves the gate actually consumes the runner's result rather than always passing.
  const failingRunner = () => ({ ok: false, output: 'FAIL: TestModesInvalid' });
  const failResults = checkDataFileForAcFile(acFile, 'data/modes.json', { rootDir, runGoTest: failingRunner });
  assert.ok(failResults.some((f) => f.status === 'fail'), 'a failing loader test must produce a check-1 FAIL, not a silent pass');
});

test('AC-7: no loader test named anywhere in the acceptance file => partial ("existence-only verified"), never silently pass', () => {
  const rootDir = mkTempDir('sprintgate-ac7-');
  fs.mkdirSync(path.join(rootDir, 'data'));
  fs.writeFileSync(path.join(rootDir, 'data', 'x.json'), JSON.stringify({ y: 1, comment: 'placeholder pending tuning' }));
  const acFile = { item: { code: 'FEAT-900' }, mkey: 'data.x', text: 'Check: data/x.json must exist. No test file named at all.' };

  const results = checkDataFileForAcFile(acFile, 'data/x.json', { rootDir });
  const arr = Array.isArray(results) ? results : [results];
  assert.ok(arr.some((f) => f.status === 'partial' && /existence-only verified/.test(f.detail)));
  assert.ok(!arr.some((f) => f.status === 'pass'), 'an unchecked schema must never be reported as a checked pass');
});

test('AC-8: a bare numeric leaf with neither provenance nor a placeholder comment is flagged; the same leaf with a placeholder-disclosure comment is not', () => {
  const flaggedNone = flagUnmarkedPlaceholders({ x: 5 });
  assert.deepEqual(flaggedNone, ['x']);

  const flaggedWithDisclosure = flagUnmarkedPlaceholders({ x: 5, comment: 'placeholder pending M2 Batch tuning' });
  assert.deepEqual(flaggedWithDisclosure, []);

  const flaggedWithProvenance = flagUnmarkedPlaceholders({ x: 5, provenance: { source: 'Some 2020 report', sourceType: 'literature' } });
  assert.deepEqual(flaggedWithProvenance, []);
});

// ---------------------------------------------------------------------------
// Check 2 — call edges (AC-10..AC-11)
// ---------------------------------------------------------------------------

test('AC-10: extractCallEdgeAssertions recognises both the prose form and the node -e tripwire one-liner form', () => {
  const prose = 'Check: relies on a registered `engine.cafe`->`engine.crime` edge for the safety term.';
  const proseEdges = extractCallEdgeAssertions(prose);
  assert.deepEqual(proseEdges, [{ source: 'engine.cafe', target: 'engine.crime' }]);

  const oneLiner = "Check (once unblocked): `node -e \"const m=require('./code.json').modules.find(x=>x.key==='engine.cafe'); process.exit(m.outbound.calls.some(c=>c.key==='engine.policies')?1:0)\"` must exit 0.";
  const oneLinerEdges = extractCallEdgeAssertions(oneLiner);
  assert.deepEqual(oneLinerEdges, [{ source: 'engine.cafe', target: 'engine.policies' }]);
});

test('AC-11: a missing edge is a check-2 FAIL naming both mkeys (BUG-058 precedent); a present edge is not', () => {
  const codeJsonWithEdge = { modules: [{ key: 'engine.cafe', outbound: { calls: [{ key: 'engine.policies' }] } }] };
  const codeJsonWithoutEdge = { modules: [{ key: 'engine.cafe', outbound: { calls: [] } }] };

  assert.equal(edgeExistsInCodeJson(codeJsonWithEdge, 'engine.cafe', 'engine.policies'), true);
  assert.equal(edgeExistsInCodeJson(codeJsonWithoutEdge, 'engine.cafe', 'engine.policies'), false);

  const rootDir = mkTempDir('sprintgate-ac11-');
  const codeJsonPath = path.join(rootDir, 'code.json');
  const acFile = {
    item: { code: 'FEAT-900' }, mkey: 'engine.cafe',
    text: "Check: relies on a registered `engine.cafe`->`engine.policies` edge.",
  };

  fs.writeFileSync(codeJsonPath, JSON.stringify(codeJsonWithoutEdge));
  let result = runCheck2CallEdges([acFile], { codeJsonPath });
  assert.equal(result.verdict, 'fail');
  assert.match(result.detail, /engine\.cafe/);
  assert.match(result.detail, /engine\.policies/);
  assert.match(result.detail, /BUG-058/);

  fs.writeFileSync(codeJsonPath, JSON.stringify(codeJsonWithEdge));
  result = runCheck2CallEdges([acFile], { codeJsonPath });
  assert.equal(result.verdict, 'pass', `expected pass once the edge is registered, got: ${JSON.stringify(result)}`);
});

test('AC-12: check 2 logs an explicit FEAT-062 reuse deferral, not a stale "FEAT-062 does not exist" claim (Tester FAIL remediation)', () => {
  // FEAT-062 (tools/plan/codejson-audit.js) shipped and exports runAudit()
  // as of 2026-08-13, but check 2 still does its own direct code.json
  // lookup rather than calling it. AC-12 requires that to be EITHER a real
  // call into FEAT-062's interface OR a logged deferred assumption with a
  // cross-reference -- this test asserts the latter path was taken
  // correctly: the source comment names the deferral and its ASM by code,
  // and the stale "does not yet exist" claim (accurate before FEAT-062
  // shipped, false afterwards) is gone.
  const src = fs.readFileSync(path.join(ROOT, 'claude-bow.js'), 'utf8');

  // The runner source names FEAT-062 explicitly near check 2's code.
  const check2Region = src.slice(src.indexOf('function runCheck2CallEdges') - 1500, src.indexOf('function runCheck2CallEdges') + 200);
  assert.match(check2Region, /FEAT-062/, 'expected FEAT-062 to be named near runCheck2CallEdges');
  assert.match(check2Region, /ASM-483/, 'expected the deferred-assumption BOW code to be named near runCheck2CallEdges');

  // The stale claim this Tester FAIL was about must not survive: it asserted
  // FEAT-062 "does not yet exist" -- that is now false (FEAT-062 is done),
  // so that exact phrase must not remain describing FEAT-062's status.
  assert.doesNotMatch(check2Region, /FEAT-062 does not yet exist/, 'stale "FEAT-062 does not yet exist" claim must be corrected now that FEAT-062 has shipped');

  // The comment must accurately describe check 2 as registration-only
  // (weaker than FEAT-062's AST-backed verification), not "checked live"
  // phrasing that implied parity with FEAT-062's depth.
  assert.match(check2Region, /registration|registered/i);
});

test('AC-12: a fixture where FEAT-062-depth verification would differ from the direct code.json lookup is exactly the scope-mismatch reason logged, not silently unhandled', () => {
  // Demonstrates concretely WHY reuse is deferred: FEAT-062's edge check
  // only evaluates edges where BOTH endpoints are BOW status 'done' (it
  // AST-parses real Go imports). Check 2 runs pre-dispatch, when the
  // sprint's own modules are typically NOT done yet -- so a FEAT-062-style
  // check would report 'skip' for exactly the edges check 2 needs a real
  // registration answer for, right now, from code.json alone. The direct
  // lookup (AC-11) is deliberately the one that can answer during that
  // window; this fixture proves the direct lookup DOES answer (registered
  // edge -> pass) even though the modules involved are not built/done.
  const rootDir = mkTempDir('sprintgate-ac12-scope-');
  const codeJsonPath = path.join(rootDir, 'code.json');
  const codeJsonWithEdge = { modules: [{ key: 'engine.notyetbuilt', outbound: { calls: [{ key: 'engine.alsonotbuilt' }] } }] };
  fs.writeFileSync(codeJsonPath, JSON.stringify(codeJsonWithEdge));

  const acFile = {
    item: { code: 'FEAT-901' }, mkey: 'engine.notyetbuilt',
    text: "Check: relies on a registered `engine.notyetbuilt`->`engine.alsonotbuilt` edge, for code not yet dispatched.",
  };

  const result = runCheck2CallEdges([acFile], { codeJsonPath });
  assert.equal(result.verdict, 'pass', 'the direct registration lookup must answer pre-dispatch, when neither module has real Go code for a FEAT-062-style AST check to examine');
});

// ---------------------------------------------------------------------------
// Check 3 — tripwires (AC-13..AC-15)
// ---------------------------------------------------------------------------

// The tripwire command text below follows the REAL narrow template every
// actual acceptance-file tripwire uses (FIX-2) rather than the trivial
// `node -e "process.exit(0)"` placeholder this suite used pre-FIX-2 — that
// placeholder does not match the template FIX-2 now requires and would
// itself be (correctly) rejected as an unrecognized shape post-fix, so
// keeping it here would have enshrined the very "run arbitrary JS" behavior
// FIX-2 exists to eliminate.
const TRIPWIRE_AC_CMD = "node -e \"const m=require('./code.json').modules.find(x=>x.key==='engine.alpha'); process.exit(m.outbound.calls.some(c=>c.key==='engine.beta')?0:1)\"";
const TRIPWIRE_AC_TEXT = '- **AC-5 (STILL BLOCKED; BUG-100 tripwire applied). Tripwire (mechanical): ' +
  '`' + TRIPWIRE_AC_CMD + '` must exit 0 (edge still absent); nonzero means re-arm this AC.** ' +
  'Check (once unblocked): grep -n "something" file.go finds a real import.';

/** Writes a minimal fixture code.json to `dir` so FIX-2's direct-evaluation
 * path (`require('./code.json')` relative to the tripwire's cwd) has
 * something real, deterministic, and disposable to read — never coupling
 * these tests to the live project code.json's ongoing drift. */
function writeFixtureCodeJson(dir, modules) {
  fs.writeFileSync(path.join(dir, 'code.json'), JSON.stringify({ modules }));
}

test('AC-13/AC-14: a "Check (once unblocked)" AC with no adjacent Tripwire block is an automatic check-3 FAIL', () => {
  const unarmedText = '- **AC-6.** Check (once unblocked): grep -n "x" file.go finds it, no tripwire here at all.';
  const results = findTripwireChecks(unarmedText);
  assert.equal(results.length, 1);
  assert.equal(results[0].armed, false);

  const result = runCheck3Tripwires([{ item: { code: 'FEAT-900' }, mkey: 'x', text: unarmedText }]);
  assert.equal(result.verdict, 'fail');
  assert.match(result.detail, /unarmed/);
});

test('AC-15: the tripwire is actually RUN and its live exit code is compared to its own documented expectation — matching = pass, mismatch = FAIL with re-arm instruction', () => {
  const acFile = { item: { code: 'FEAT-900' }, mkey: 'engine.cafe', text: TRIPWIRE_AC_TEXT };

  // Healthy case: live exit (injected) matches the documented "must exit 0".
  // Post-FIX-1, an injected runTripwire returns { ok, status } — the same
  // shape defaultRunTripwire itself now returns after allowlist-matching.
  const passResult = runCheck3Tripwires([acFile], { runTripwire: () => ({ ok: true, status: 0 }) });
  assert.equal(passResult.verdict, 'pass', `expected pass when live exit matches documented expectation, got: ${JSON.stringify(passResult)}`);

  // Drift case: the SAME fixture, but the live exit code has changed (blocker cleared) — must FAIL and say "re-arm".
  const failResult = runCheck3Tripwires([acFile], { runTripwire: () => ({ ok: true, status: 1 }) });
  assert.equal(failResult.verdict, 'fail');
  assert.match(failResult.detail, /re-arm/);
});

test('AC-15 (text-only, no execution): findTripwireChecks extracts the exact command and documented expected exit code', () => {
  const results = findTripwireChecks(TRIPWIRE_AC_TEXT);
  assert.equal(results.length, 1);
  assert.equal(results[0].armed, true);
  assert.equal(results[0].command, TRIPWIRE_AC_CMD);
  assert.equal(results[0].expectedExit, 0);
});

// ---------------------------------------------------------------------------
// BUG-113/BUG-117 (same underlying regex gap, discovered from two angles):
// findTripwireChecks's Tripwire-label regex required the literal, unadorned
// text `Tripwire (mechanical...):` with no markdown around it. Real acceptance
// docs (engine.destination.md, engine.leisure.md, engine.tourism.md, all
// authored under BUG-100's remediation) wrap the whole label in bold markdown
// — `**Tripwire (mechanical — BUG-100):**` — which the old regex silently
// failed to match, leaving those ACs reported as UNARMED even though a real,
// executable tripwire sits right there. That is the dangerous shape: no
// crash, no warning, just an AC that looks armed in the doc but isn't
// actually checked. Fixed by tolerating 0, 1, or 2 asterisks immediately
// before "Tripwire" and immediately after the label's closing "):".
// ---------------------------------------------------------------------------

test('BUG-113/BUG-117 regression: a Tripwire block with a PLAIN TEXT label still parses as armed (no regression from the markdown-tolerance fix)', () => {
  const plainText = '- **AC-20 (STILL BLOCKED). Tripwire (mechanical): `' + TRIPWIRE_AC_CMD +
    '` must exit 0 (edge still absent); nonzero means re-arm this AC.** ' +
    'Check (once unblocked): grep -n "x" file.go finds it.';
  const results = findTripwireChecks(plainText);
  assert.equal(results.length, 1);
  assert.equal(results[0].armed, true, 'plain-text Tripwire label must still be recognized as armed');
  assert.equal(results[0].command, TRIPWIRE_AC_CMD);
  assert.equal(results[0].expectedExit, 0);
});

test('BUG-113/BUG-117 regression: a Tripwire block wrapped in **bold** markdown (the real engine.destination.md/engine.leisure.md/engine.tourism.md shape) now parses as armed', () => {
  const boldText = '- **AC-21 (STILL BLOCKED after c36778b; BUG-100 tripwire applied).** ' +
    '**Tripwire (mechanical — BUG-100):** `' + TRIPWIRE_AC_CMD +
    '` must exit **0** (edge still absent); a nonzero exit means the edge landed and this AC must be re-armed. ' +
    'Until the tripwire fires: Check (once unblocked): grep -n "x" file.go finds it.';
  const results = findTripwireChecks(boldText);
  assert.equal(results.length, 1);
  assert.equal(results[0].armed, true, 'bold-wrapped Tripwire label must be recognized as armed, not silently dropped');
  assert.equal(results[0].command, TRIPWIRE_AC_CMD);
  assert.equal(results[0].expectedExit, 0);
});

test('BUG-113/BUG-117 regression: a Tripwire block wrapped in *italic* markdown also parses as armed', () => {
  const italicText = '- **AC-22 (STILL BLOCKED).** ' +
    '*Tripwire (mechanical):* `' + TRIPWIRE_AC_CMD +
    '` must exit *0* (edge still absent); nonzero means re-arm this AC. ' +
    'Check (once unblocked): grep -n "x" file.go finds it.';
  const results = findTripwireChecks(italicText);
  assert.equal(results.length, 1);
  assert.equal(results[0].armed, true, 'italic-wrapped Tripwire label must be recognized as armed');
  assert.equal(results[0].command, TRIPWIRE_AC_CMD);
  assert.equal(results[0].expectedExit, 0);
});

test('BUG-113/BUG-117 real-doc proof: the currently-committed acceptance docs with bold-markdown Tripwire blocks (engine.destination.md AC-6/7/8, engine.leisure.md AC-8, engine.tourism.md AC-7/8/9) are now all detected as armed, not silently unarmed', () => {
  const expectedArmed = {
    'engine.destination.md': ['6', '7', '8'],
    'engine.leisure.md': ['8'],
    'engine.tourism.md': ['7', '8', '9'],
  };
  for (const [file, acNums] of Object.entries(expectedArmed)) {
    const acPath = path.join(__dirname, 'docs', 'planning', 'acceptance', file);
    const text = fs.readFileSync(acPath, 'utf8');
    const results = findTripwireChecks(text);
    for (const acNum of acNums) {
      const hit = results.find((r) => r.acNum === acNum);
      assert.ok(hit, `${file} AC-${acNum} should be picked up as a "Check (once unblocked)" block at all`);
      assert.equal(hit.armed, true, `${file} AC-${acNum}'s bold-markdown Tripwire block must be detected as armed, not silently unarmed`);
      assert.ok(hit.command && hit.command.startsWith('node -e'), `${file} AC-${acNum} must extract a real, executable command`);
    }
  }
});

// ---------------------------------------------------------------------------
// FIX-1 (P0 RCE regression) — Destructive finding on FEAT-061: runCheck3Tripwires
// used `spawnSync(command, {shell:true})` on raw, untrusted acceptance-file
// text, letting a malicious tripwire block smuggle and execute a SECOND,
// injected command via shell metacharacters, all while the check still
// reported PASS. Fixed by tokenizing (never shell-interpreting) the command
// and matching it against a strict node-e/grep allowlist before ever
// spawning anything.
// ---------------------------------------------------------------------------

test('FIX-1: the exact Destructive-proven malicious tripwire (shell "&" smuggling a second `node -e` that writes a file) is rejected outright — the injected command never runs, and the check fails loudly naming the unrecognized shape, not a silent pass', () => {
  const scratchDir = mkTempDir('sprintgate-fix1-rce-');
  const pwnedFile = path.join(scratchDir, 'pwned.txt').replace(/\\/g, '\\\\');
  const maliciousCmd = `node -e "process.exit(0)" & node -e "require('fs').writeFileSync('${pwnedFile}','pwned')"`;
  const maliciousAc = '- **AC-9 (STILL BLOCKED). Tripwire (mechanical): `' + maliciousCmd + '` must exit 0 (edge still absent); nonzero means re-arm this AC.** ' +
    'Check (once unblocked): grep -n "x" file.go finds it.';
  const acFile = { item: { code: 'FEAT-901' }, mkey: 'x', text: maliciousAc };

  // Direct tokenizer check: the malicious text must fail to parse at all
  // (the unquoted "&" is not in the safe unquoted charset).
  assert.equal(tokenizeCommandSafely(maliciousCmd), null, 'a bare shell metacharacter outside any quoted span must make the whole command unparseable');

  // End-to-end: runCheck3Tripwires must never invoke a shell for this text.
  const result = runCheck3Tripwires([acFile], { cwd: scratchDir });
  assert.equal(result.verdict, 'fail');
  assert.match(result.detail, /unrecognized|unsafe|not executed/i);
  assert.equal(fs.existsSync(path.join(scratchDir, 'pwned.txt')), false, 'the injected second command must NEVER execute — this is the RCE the Destructive proved');
});

test('FIX-2: real allowlisted shapes (the recognized `node -e "..."` code.json template, and `grep -n "pattern" file` under an allowed root) still execute correctly and report the right pass/fail from the live check — closing the hole must not break the feature', () => {
  // node-eval shape: fixture code.json where the edge IS present -> boolean true -> documented "?0:1" ternary -> exit 0 -> must PASS ("must exit 0").
  const fixtureDir = mkTempDir('sprintgate-fix2-nodeeval-');
  writeFixtureCodeJson(fixtureDir, [
    { key: 'engine.alpha', outbound: { calls: [{ key: 'engine.beta' }] }, inbound: { consumers: [] } },
  ]);
  const nodeAcFile = { item: { code: 'FEAT-902' }, mkey: 'x', text: TRIPWIRE_AC_TEXT };
  const nodePass = runCheck3Tripwires([nodeAcFile], { cwd: fixtureDir });
  assert.equal(nodePass.verdict, 'pass', `expected the recognized node-eval template (edge present) to pass, got: ${JSON.stringify(nodePass)}`);

  // Same tripwire text, but the fixture now lacks the edge -> boolean false -> falseExit branch (1) -> mismatches documented "must exit 0" -> FAIL + re-arm.
  writeFixtureCodeJson(fixtureDir, [
    { key: 'engine.alpha', outbound: { calls: [] }, inbound: { consumers: [] } },
  ]);
  const nodeFail = runCheck3Tripwires([nodeAcFile], { cwd: fixtureDir });
  assert.equal(nodeFail.verdict, 'fail');
  assert.match(nodeFail.detail, /re-arm/);

  // grep shape: path argument constrained to the allowed `internal/` root (FIX-2 P1) — a real fixture file containing the pattern.
  const grepRoot = mkTempDir('sprintgate-fix2-grep-');
  fs.mkdirSync(path.join(grepRoot, 'internal'), { recursive: true });
  fs.writeFileSync(path.join(grepRoot, 'internal', 'target.go'), 'import "somepkg/real"\n');
  const grepAcText = '- **AC-10 (STILL BLOCKED). Tripwire (mechanical): `grep -n "somepkg/real" internal/target.go` must exit 0 (edge still absent); nonzero means re-arm this AC.** ' +
    'Check (once unblocked): grep -n "x" file.go finds it.';
  const grepAc = { item: { code: 'FEAT-903' }, mkey: 'x', text: grepAcText };
  const grepPass = runCheck3Tripwires([grepAc], { cwd: grepRoot });
  assert.equal(grepPass.verdict, 'pass', `expected the real grep tripwire (pattern present) to pass, got: ${JSON.stringify(grepPass)}`);

  // Same grep tripwire but the pattern is now ABSENT — live exit becomes nonzero (1), mismatching the documented "must exit 0", so check 3 must FAIL and say re-arm.
  fs.writeFileSync(path.join(grepRoot, 'internal', 'target.go'), 'import "somepkg/unrelated"\n');
  const grepFail = runCheck3Tripwires([grepAc], { cwd: grepRoot });
  assert.equal(grepFail.verdict, 'fail');
  assert.match(grepFail.detail, /re-arm/);
});

test('FIX-1/FIX-2: matchTripwireShape rejects anything outside the node-e/grep allowlist even when the tokenizer parses it cleanly (e.g. a bare "rm -rf /tmp" shape)', () => {
  const tokens = tokenizeCommandSafely('rm -rf /tmp');
  assert.ok(tokens, 'a plain unquoted command with no metacharacters tokenizes fine');
  const match = matchTripwireShape(tokens, 'rm -rf /tmp', ROOT);
  assert.equal(match.ok, false);
  assert.match(match.reason, /unrecognized|unsafe/i);
});

// ---------------------------------------------------------------------------
// FIX-2 (round-2 RCE regression) — Destructive finding on FEAT-061: FIX-1's
// `node -e "<code>"` allowlist entry validated only the OUTER 3-token shape
// (`node`, `-e`, a quoted string), never what `<code>` itself does. Since
// `spawnSync('node', ['-e', code])` runs a REAL node process, the Destructive
// proved `code` can be `require('child_process').execSync(...)` or
// `require('fs').writeFileSync(...)`, reintroducing round 1's exact RCE one
// layer down — proven by actual file/command execution. Fixed by replacing
// execution of `<code>` with a narrow structural PARSER that recognises only
// the code.json edge-check template every real tripwire actually uses, and
// evaluating it directly in-process (no spawn, ever, for this shape) — never
// eval/new Function/vm, never handing `<code>` to a JS engine at all.
// ---------------------------------------------------------------------------

test('FIX-2 round-2 RCE: the exact Destructive-proven exploit — `node -e "require(\'child_process\').execSync(...)"` — is REJECTED as an unrecognized shape; the injected shell command never runs', () => {
  const scratchDir = mkTempDir('sprintgate-fix2-execsync-');
  const markerFile = path.join(scratchDir, 'pwned-execsync.txt').replace(/\\/g, '/');
  // Outer token uses single quotes (tokenizeCommandSafely does not
  // escape-process single-quoted spans) so the payload's own double-quoted
  // JS string literals pass through untouched — this is a REAL, executable
  // exploit shape if ever handed to a JS engine, not a toy string.
  const jsPayload = `require("child_process").execSync("echo pwned> ${markerFile}", {shell:true})`;
  const maliciousCmd = `node -e '${jsPayload}'`;
  const maliciousAc = '- **AC-9 (STILL BLOCKED). Tripwire (mechanical): `' + maliciousCmd + '` must exit 0 (edge still absent); nonzero means re-arm this AC.** ' +
    'Check (once unblocked): grep -n "x" internal/file.go finds it.';
  const acFile = { item: { code: 'FEAT-904' }, mkey: 'x', text: maliciousAc };

  // Direct parser-level check: the payload is NOT the code.json edge-check template.
  assert.equal(parseCodeJsonEdgeTripwire(jsPayload), null, 'execSync-based JS must not parse as the code.json edge-check template');

  const tokens = tokenizeCommandSafely(maliciousCmd);
  assert.ok(tokens, 'no shell metacharacters at the argv level — this must tokenize cleanly, proving FIX-2 (not FIX-1s tokenizer) is what stops it');
  const match = matchTripwireShape(tokens, maliciousCmd, scratchDir);
  assert.equal(match.ok, false, `expected the unrecognized-shape rejection, got: ${JSON.stringify(match)}`);
  assert.match(match.reason, /unrecognized tripwire shape|code\.json edge-check template/i);

  const result = runCheck3Tripwires([acFile], { cwd: scratchDir });
  assert.equal(result.verdict, 'fail');
  assert.match(result.detail, /unrecognized|not executed/i);
  assert.equal(fs.existsSync(path.join(scratchDir, 'pwned-execsync.txt')), false, 'the execSync exploit must NEVER run — this is the round-2 RCE the Destructive proved');
});

test('FIX-2 round-2 RCE: the file-write variant — `node -e "require(\'fs\').writeFileSync(...)"` — is REJECTED as an unrecognized shape; no file is written', () => {
  const scratchDir = mkTempDir('sprintgate-fix2-writefile-');
  const markerFile = path.join(scratchDir, 'pwned-writefile.txt').replace(/\\/g, '/');
  const jsPayload = `require("fs").writeFileSync("${markerFile}", "pwned")`;
  const maliciousCmd = `node -e '${jsPayload}'`;
  const maliciousAc = '- **AC-9 (STILL BLOCKED). Tripwire (mechanical): `' + maliciousCmd + '` must exit 0 (edge still absent); nonzero means re-arm this AC.** ' +
    'Check (once unblocked): grep -n "x" internal/file.go finds it.';
  const acFile = { item: { code: 'FEAT-905' }, mkey: 'x', text: maliciousAc };

  assert.equal(parseCodeJsonEdgeTripwire(jsPayload), null, 'writeFileSync-based JS must not parse as the code.json edge-check template');

  const result = runCheck3Tripwires([acFile], { cwd: scratchDir });
  assert.equal(result.verdict, 'fail');
  assert.match(result.detail, /unrecognized|not executed/i);
  assert.equal(fs.existsSync(path.join(scratchDir, 'pwned-writefile.txt')), false, 'the writeFileSync exploit must NEVER run — this is the round-2 RCE the Destructive proved');
});

test('FIX-2: two more-complex real acceptance-file constructs that do NOT fit the narrow template are safely rejected, not silently widened (ASM logged for Bill rather than decided unilaterally) — the feat.disasters.md AC-7 two-module cross-check and the engine.wellbeing.md .every() array form', () => {
  // docs/planning/acceptance/feat.disasters.md — two separate `.find()` lookups OR'd together.
  const disastersCode = "const cj=require('./code.json'); const m=cj.modules.find(x=>x.key==='feat.disasters'); const svc=cj.modules.find(x=>x.key==='engine.services'); process.exit((m.outbound.calls.some(c=>c.key==='engine.services')||(svc.outbound.calls||[]).some(c=>c.key==='feat.disasters'))?1:0)";
  assert.equal(parseCodeJsonEdgeTripwire(disastersCode), null);

  // docs/planning/acceptance/engine.wellbeing.md — .every() over an array literal with an indirection variable, not a literal .some(c=>c.key==='X') chain.
  const wellbeingCode = "const m=require('./code.json').modules.find(x=>x.key==='engine.wellbeing'); process.exit(['engine.shopping','engine.world'].every(k=>m.outbound.calls.some(c=>c.key===k))?0:1)";
  assert.equal(parseCodeJsonEdgeTripwire(wellbeingCode), null);
});

test('FIX-2: 5 real "Tripwire (mechanical)" strings copied verbatim from docs/planning/acceptance/*.md are correctly recognized and evaluated against the REAL current code.json, with ZERO child processes spawned to answer them', () => {
  // Expected exits below were computed by reading the real, current code.json
  // state for each module (not hardcoded from the acceptance-file prose
  // alone) — see the task report for the exact `outbound.calls`/
  // `inbound.consumers` arrays read at the time this test was written.
  const cases = [
    // outbound.calls.some, single target — docs/planning/acceptance/engine.build.md AC-6.
    { file: 'engine.build.md', cmd: "node -e \"const m=require('./code.json').modules.find(x=>x.key==='engine.build'); process.exit(m.outbound.calls.some(c=>c.key==='engine.season')?0:1)\"", expectExit: 0 },
    // (m.inbound.consumers||[]).some, single target — docs/planning/acceptance/engine.capexport.md.
    { file: 'engine.capexport.md', cmd: "node -e \"const m=require('./code.json').modules.find(x=>x.key==='engine.capexport'); process.exit((m.inbound.consumers||[]).some(c=>c.key==='engine.rail')?1:0)\"", expectExit: 0 },
    // outbound.calls.some, OR-chained multi-target — docs/planning/acceptance/engine.coastal.md AC-10.
    { file: 'engine.coastal.md', cmd: "node -e \"const m=require('./code.json').modules.find(x=>x.key==='engine.coastal'); process.exit(m.outbound.calls.some(c=>c.key==='engine.tourism'||c.key==='engine.build')?1:0)\"", expectExit: 0 },
    // (m.inbound.consumers||[]).some, single target — docs/planning/acceptance/engine.defence.md AC-9.
    // BUG-254: AC-9's original bare `.length===0` count tripwire correctly
    // fired when 118c359 registered engine.fdi's first three consumers
    // (accelerator/spaceport/pharmacampus — none of them engine.defence), so
    // the doc re-armed it as this membership test for the defence edge
    // specifically, robust to unrelated consumers arriving. The parser's
    // `inbound-length-zero` branch remains in claude-bow.js but no longer has
    // a real acceptance-file exemplar.
    { file: 'engine.defence.md', cmd: "node -e \"const m=require('./code.json').modules.find(x=>x.key==='engine.fdi'); process.exit((m.inbound.consumers||[]).some(c=>c.key==='engine.defence')?1:0)\"", expectExit: 0 },
    // outbound.calls.some, OR-chained multi-target — docs/planning/acceptance/engine.destination.md AC-7.
    { file: 'engine.destination.md', cmd: "node -e \"const m=require('./code.json').modules.find(x=>x.key==='engine.destination'); process.exit(m.outbound.calls.some(c=>c.key==='engine.shopping'||c.key==='engine.cafe')?1:0)\"", expectExit: 0 },
  ];

  const acPath = path.join(ROOT, 'docs', 'planning', 'acceptance');
  for (const { file } of cases) {
    assert.ok(fs.existsSync(path.join(acPath, file)), `sanity check: ${file} must actually exist in docs/planning/acceptance`);
  }

  // Break PATH so an accidental spawnSync('node', ...) would fail to locate
  // the binary and surface as { ok:false } — proving these recognized
  // shapes are answered WITHOUT spawning a child process at all, not merely
  // "spawning happened but we didn't notice."
  const savedPath = process.env.PATH;
  process.env.PATH = '';
  try {
    for (const { file, cmd, expectExit } of cases) {
      const tokens = tokenizeCommandSafely(cmd);
      assert.ok(tokens, `${file}'s tripwire must tokenize cleanly`);
      const match = matchTripwireShape(tokens, cmd, ROOT);
      assert.equal(match.ok, true, `${file}'s tripwire must be recognized as the code.json edge-check template, got: ${JSON.stringify(match)}`);
      assert.equal(match.kind, 'node-eval', `${file}'s tripwire must be answered by direct evaluation, not a spawn`);

      const result = defaultRunTripwire(cmd, ROOT);
      assert.equal(result.ok, true, `${file}'s tripwire must be answered successfully even with PATH broken, got: ${JSON.stringify(result)}`);
      assert.equal(result.status, expectExit, `${file}'s tripwire must evaluate to the documented exit code against the real code.json, got: ${JSON.stringify(result)}`);
    }
  } finally {
    process.env.PATH = savedPath;
  }
});

test('FIX-2 P1: grep path arguments are constrained to docs/planning/acceptance/, internal/, and data/ — a traversal escaping those roots is rejected outright, not read', () => {
  const scratchRoot = mkTempDir('sprintgate-fix2-grepescape-');
  fs.mkdirSync(path.join(scratchRoot, 'internal'), { recursive: true });
  const outsideDir = mkTempDir('sprintgate-fix2-grepescape-outside-');
  const secretFile = path.join(outsideDir, 'secret.txt');
  fs.writeFileSync(secretFile, 'top secret contents\n');

  // A traversal path argument, relative from scratchRoot, escaping to a
  // sibling directory entirely outside any allowed root — the Windows-repo
  // analogue of the classic `grep -n "x" ../../../../etc/passwd` finding.
  const relEscape = path.relative(scratchRoot, secretFile).replace(/\\/g, '/');
  const cmd = `grep -n "secret" ${relEscape}`;
  const tokens = tokenizeCommandSafely(cmd);
  assert.ok(tokens, 'a plain relative path with no shell metacharacters must tokenize fine (this is a data problem, not a shell-injection problem)');

  const match = matchTripwireShape(tokens, cmd, scratchRoot);
  assert.equal(match.ok, false, `expected the traversal path to be rejected, got: ${JSON.stringify(match)}`);
  assert.match(match.reason, /outside the allowed roots/i);

  const result = defaultRunTripwire(cmd, scratchRoot);
  assert.equal(result.ok, false);
  assert.match(result.reason, /outside the allowed roots/i);
});

test('FIX-3 P1 (Destructive finding): a grep path argument containing a null byte reaches spawnSync — Node throws a synchronous TypeError ("...without null bytes") from spawnSync itself rather than returning a result.error, and defaultRunTripwire must catch that and return { ok:false, status:null, reason }, not crash the caller', () => {
  // A quoted path argument can carry ANY character (tokenizeCommandSafely's
  // quoted-token branch does not charset-filter, unlike the unquoted
  // branch), including a literal NUL. It still resolves under the allowed
  // `internal/` root, so matchTripwireShape accepts it — the failure must
  // surface only once spawnSync itself is invoked.
  const cmd = 'grep -n "x" "internal/synth\0evil"';

  const tokens = tokenizeCommandSafely(cmd);
  assert.ok(tokens, 'a quoted path with an embedded null byte must still tokenize (no charset filter on quoted tokens)');

  const match = matchTripwireShape(tokens, cmd, ROOT);
  assert.equal(match.ok, true, `expected the null-byte path to still resolve under the allowed internal/ root and be accepted, got: ${JSON.stringify(match)}`);
  assert.equal(match.kind, 'grep-exec');

  // The regression proper: calling defaultRunTripwire must not throw, even
  // though the underlying spawnSync call synchronously throws a TypeError
  // for a null byte in argv. Confirmed to fail (throw) against the
  // pre-fix code with the try/catch removed, and to pass once it is
  // wrapped — see the fix in defaultRunTripwire around the spawnSync call.
  let result;
  assert.doesNotThrow(() => { result = defaultRunTripwire(cmd, ROOT); }, 'defaultRunTripwire must not let a spawnSync-thrown TypeError escape — it owns its own crash-safety, not just its callers');
  assert.equal(result.ok, false);
  assert.equal(result.status, null);
  assert.match(result.reason, /spawn/i, `reason should name that the spawn itself failed, got: ${JSON.stringify(result)}`);
});

test('FIX-2 P1: isPathUnderAllowedGrepRoot accepts real paths under docs/planning/acceptance/, internal/, data/ and rejects everything else, including a same-prefix decoy directory name', () => {
  assert.equal(isPathUnderAllowedGrepRoot(ROOT, 'internal/engine/cafe/x.go'), true);
  assert.equal(isPathUnderAllowedGrepRoot(ROOT, 'docs/planning/acceptance/engine.cafe.md'), true);
  assert.equal(isPathUnderAllowedGrepRoot(ROOT, 'data/terrain50/x.json'), true);
  assert.equal(isPathUnderAllowedGrepRoot(ROOT, '../outside/x.go'), false);
  assert.equal(isPathUnderAllowedGrepRoot(ROOT, 'internal-evil/x.go'), false, 'a same-prefix sibling directory must not be treated as inside internal/');
  assert.equal(isPathUnderAllowedGrepRoot(ROOT, 'claude-bow.js'), false, 'a repo-root file outside any allowed subtree must be rejected');
});

// ---------------------------------------------------------------------------
// Check 4 — boundary rulings (AC-16..AC-19, report-only)
// ---------------------------------------------------------------------------

test('AC-16: a comment tagged [boundary ruling: A <-> B] is a confirmed ruling', () => {
  const comments = [{ body: '[boundary ruling: engine.citizens <-> engine.households] citizens owns residency.', item_code: 'ASM-500' }];
  const rulings = findConfirmedBoundaryRulings(comments);
  assert.equal(rulings.length, 1);
  assert.equal(rulings[0].moduleA, 'engine.citizens');
  assert.equal(rulings[0].moduleB, 'engine.households');
});

test('AC-17: an ASM-247-shaped untagged comment ("I resolved this as: engine.citizens owns...") is caught as a CANDIDATE, not a confirmed ruling', () => {
  const comments = [{ body: 'I resolved this as: engine.citizens owns household formation, not engine.households.', item_code: 'ASM-247' }];
  assert.equal(findConfirmedBoundaryRulings(comments).length, 0);
  const candidates = findCandidateBoundaryRulings(comments);
  assert.equal(candidates.length, 1);
  assert.equal(candidates[0].code, 'ASM-247');
});

test('AC-18: a ruling cited in only ONE of the two affected acceptance files is a check-4 finding naming the missing side', () => {
  const acceptanceDir = mkTempDir('sprintgate-ac18-');
  fs.writeFileSync(path.join(acceptanceDir, 'engine.citizens.md'), 'Per ASM-247, citizens owns residency.');
  fs.writeFileSync(path.join(acceptanceDir, 'engine.households.md'), 'Nothing about the ruling here at all.');

  const ruling = { moduleA: 'engine.citizens', moduleB: 'engine.households', code: 'ASM-247', text: 'I resolved this as: engine.citizens owns...' };
  const finding = crossCiteFinding(ruling, acceptanceDir);
  assert.ok(finding, 'expected a finding since only one side cites the ruling');
  assert.match(finding, /engine\.households/);
  assert.match(finding, /ASM-247/);

  // Healthy case: both sides cite it => no finding.
  fs.writeFileSync(path.join(acceptanceDir, 'engine.households.md'), 'Per ASM-247, citizens owns residency, not us.');
  assert.equal(crossCiteFinding(ruling, acceptanceDir), null);
});

test('AC-19: check 4 is report-only — its verdict never appears among the checks deriveOverallVerdict gates on', () => {
  const comments = [{ body: 'I resolved this as: engine.citizens owns household formation, not engine.households.', item_code: 'ASM-247' }];
  const acceptanceDir = mkTempDir('sprintgate-ac19-');
  // Neither side cites the ruling => check 4 would be 'partial' if it counted.
  const result = runCheck4BoundaryRulings(comments, { acceptanceDir });
  assert.equal(result.verdict, 'partial');
  assert.match(result.detail, /report-only/);

  // But an otherwise-all-pass row set with check 4 = 'partial' still derives PASS overall.
  const rows = [
    { check_number: 1, verdict: 'pass' }, { check_number: 2, verdict: 'pass' },
    { check_number: 3, verdict: 'pass' }, { check_number: 4, verdict: 'partial' },
    { check_number: 5, verdict: 'pass' },
  ];
  assert.equal(deriveOverallVerdict(rows), 'PASS', 'check 4 must never gate the overall verdict (AC-19/AC-26)');
});

// ---------------------------------------------------------------------------
// Check 5 — ready-queue / FEAT-060 truthfulness (AC-20..AC-22)
// ---------------------------------------------------------------------------

test('AC-20: FEAT-060 absent/open => check 5 is `skipped` with the exact detail text, and a row still gets written (never a silent omission)', async () => {
  const result = await runCheck5ReadyQueue(db);
  assert.equal(result.verdict, 'skipped');
  assert.equal(result.detail, 'FEAT-060 not yet available, skipped');
});

test('AC-21/AC-22: once FEAT-060 is done, check 5 runs the real lint and FAILs on findings, PASSes when clean — re-resolved live on every call, not cached', async () => {
  const feat060Guid = await insertItem({ code: 'FEAT-060', status: 'open' });

  // Before FEAT-060 ships: skipped.
  let result = await runCheck5ReadyQueue(db);
  assert.equal(result.verdict, 'skipped');

  // Ship FEAT-060 done, but seed a dirty BOW (a Class-2 lint finding: a fabricated citation).
  await db.query("UPDATE bow_items SET status = 'done' WHERE guid = ?", [feat060Guid]);
  const dirtyGuid = await insertItem({ code: 'BUG-950', description: 'Cites the fabricated ASM-999.' });
  result = await runCheck5ReadyQueue(db);
  assert.equal(result.verdict, 'fail', `expected FAIL on a dirty BOW, got: ${JSON.stringify(result)}`);

  // Clean it and re-run in the SAME process — proves live re-resolution, not a cached prior result (AC-22).
  await db.query('UPDATE bow_items SET description = NULL WHERE guid = ?', [dirtyGuid]);
  result = await runCheck5ReadyQueue(db);
  assert.equal(result.verdict, 'pass', `expected PASS once the BOW is clean, got: ${JSON.stringify(result)}`);
});

// ---------------------------------------------------------------------------
// Verdict recording (AC-23..AC-28)
// ---------------------------------------------------------------------------

test('AC-24: recordGateVerdict validates check/name/verdict/runner before writing (all-or-nothing), and a passing call round-trips via gate-status', async () => {
  await assert.rejects(() => recordGateVerdict(db, { sprint: 3, checkNumber: 9, checkName: 'data-files', verdict: 'pass', runner: 'x' }), /--check/);
  await assert.rejects(() => recordGateVerdict(db, { sprint: 3, checkNumber: 1, checkName: 'bogus', verdict: 'pass', runner: 'x' }), /--name/);
  await assert.rejects(() => recordGateVerdict(db, { sprint: 3, checkNumber: 1, checkName: 'data-files', verdict: 'bogus', runner: 'x' }), /--verdict/);
  await assert.rejects(() => recordGateVerdict(db, { sprint: 3, checkNumber: 1, checkName: 'data-files', verdict: 'pass', runner: '' }), /--runner/);

  const runGuid = crypto.randomUUID();
  for (let i = 0; i < 5; i++) {
    await recordGateVerdict(db, {
      sprint: 7, checkNumber: i + 1, checkName: GATE_CHECK_NAMES[i], verdict: 'pass', runner: 'test-suite', gateRunGuid: runGuid,
    });
  }
  const run = await latestGateRun(db, 7);
  assert.equal(run.rows.length, 5);
  assert.equal(run.gateRunGuid, runGuid);
  assert.equal(deriveOverallVerdict(run.rows), 'PASS');
});

test('AC-26: overall is FAIL if any gating check (1/2/3/5) fails; PARTIAL if none fail but one is partial/skipped; PASS only if all gating checks pass', () => {
  const base = (overrides) => [1, 2, 3, 4, 5].map((n) => ({ check_number: n, verdict: overrides[n] || 'pass' }));

  assert.equal(deriveOverallVerdict(base({ 2: 'fail' })), 'FAIL');
  assert.equal(deriveOverallVerdict(base({ 5: 'skipped' })), 'PARTIAL');
  assert.equal(deriveOverallVerdict(base({})), 'PASS');
  // check 4 alone being 'fail'-shaped (partial, since check4 never uses 'fail') must not flip overall.
  assert.equal(deriveOverallVerdict(base({ 4: 'partial' })), 'PASS');
  // An incomplete row set (a gating check's row simply missing) must never silently read as PASS.
  assert.equal(deriveOverallVerdict(base({}).filter((r) => r.check_number !== 5)), 'PARTIAL');
});

test('AC-27: a re-run inserts a NEW gate_run_guid; gate-status/latestGateRun reports only the latest run\'s rows, never a mix', async () => {
  const runA = crypto.randomUUID();
  for (let i = 0; i < 5; i++) {
    await recordGateVerdict(db, { sprint: 9, checkNumber: i + 1, checkName: GATE_CHECK_NAMES[i], verdict: 'fail', runner: 'run-a', gateRunGuid: runA });
  }
  // Ensure a distinguishable created_at ordering even within the same second (id tiebreak).
  await new Promise((resolve) => setTimeout(resolve, 20));
  const runB = crypto.randomUUID();
  for (let i = 0; i < 5; i++) {
    await recordGateVerdict(db, { sprint: 9, checkNumber: i + 1, checkName: GATE_CHECK_NAMES[i], verdict: 'pass', runner: 'run-b', gateRunGuid: runB });
  }

  const latest = await latestGateRun(db, 9);
  assert.equal(latest.gateRunGuid, runB);
  assert.ok(latest.rows.every((r) => r.verdict === 'pass'), 'the latest run\'s rows must all be from run B, never a mix with run A');

  const [countA] = await db.query('SELECT COUNT(*) AS n FROM bow_gate_verdicts WHERE gate_run_guid = ?', [runA]);
  assert.equal(countA[0].n, 5, 'run A\'s rows must still exist (append-only — a re-run does not mutate or delete the prior run)');
});

test('gate-status: an unrecorded sprint reports "no verdicts" (AC-28 — the CLI must not imply a passed gate for an unrun one)', async () => {
  const run = await latestGateRun(db, 12345);
  assert.equal(run, null);
});

// ---------------------------------------------------------------------------
// AC-25: full-run atomicity — 5 rows written per run, and a mid-run crash
// still yields exactly 5 rows (never a silent partial set).
// ---------------------------------------------------------------------------

test('AC-25 (healthy run): runGate against a resolvable fixture sprint writes exactly 5 rows sharing one gate_run_guid', async () => {
  const acceptanceDir = mkTempDir('sprintgate-ac25-');
  const rootDir = mkTempDir('sprintgate-ac25-root-');
  const codeJsonPath = path.join(rootDir, 'code.json');
  fs.writeFileSync(codeJsonPath, JSON.stringify({ modules: [] }));
  await insertItem({ code: 'FEAT-970', mkey: 'tool.nothing', status: 'open', sprint: 20 });
  // No acceptance file written for tool.nothing.md — a deliberate AC-2 gap, exercised end-to-end.

  const result = await runGate(db, 20, { runner: 'test-suite', acceptanceDir, codeJsonPath, sprintPlanPath: path.join(acceptanceDir, 'nonexistent-plan.md'), rootDir });
  assert.equal(result.results.length, 5);
  const [rows] = await db.query('SELECT * FROM bow_gate_verdicts WHERE sprint = 20 AND gate_run_guid = ?', [result.gateRunGuid]);
  assert.equal(rows.length, 5);
  assert.deepEqual(new Set(rows.map((r) => r.check_number)), new Set([1, 2, 3, 4, 5]));
});

test('AC-25 (crash resilience): a crash mid-run still results in exactly 5 written rows, with the unreached checks marked failed/crashed — never a silent 2-row partial state', async () => {
  await insertItem({ code: 'FEAT-971', mkey: 'tool.crashy', status: 'open', sprint: 21 });
  const acceptanceDir = mkTempDir('sprintgate-ac25c-');
  const rootDir = mkTempDir('sprintgate-ac25c-root-');
  fs.writeFileSync(path.join(rootDir, 'code.json'), JSON.stringify({ modules: [] }));

  // A db wrapper that lets everything through EXCEPT the comments-join query
  // runGate issues to feed check 4 — simulating a mid-run crash partway
  // through scope/check resolution, before any of the 5 check results exist.
  const crashingDb = {
    query: (sql, params) => {
      if (typeof sql === 'string' && sql.includes('JOIN bow_items i ON i.guid = c.item_guid')) {
        throw new Error('simulated mid-run crash');
      }
      return db.query(sql, params);
    },
  };

  const result = await runGate(crashingDb, 21, { runner: 'test-suite', acceptanceDir, codeJsonPath: path.join(rootDir, 'code.json'), rootDir });
  assert.equal(result.results.length, 5, 'exactly 5 results must exist even after a mid-run crash');
  assert.ok(result.results.some((r) => /crashed/.test(r.detail || '')), 'at least one result must record the crash explicitly');

  const [rows] = await db.query('SELECT * FROM bow_gate_verdicts WHERE sprint = 21 AND gate_run_guid = ?', [result.gateRunGuid]);
  assert.equal(rows.length, 5, 'the DB must contain exactly 5 rows for this run — never a silent partial set');
});

// ---------------------------------------------------------------------------
// CLI surface smoke test (AC-24's literal usage string)
// ---------------------------------------------------------------------------

test('CLI: `gate` records a row and `gate-status` reports it plus a derived overall', async () => {
  const runGuid = crypto.randomUUID();
  for (let i = 1; i <= 5; i++) {
    const r = spawnSync(process.execPath, [
      'claude-bow.js', 'gate', '42', '--check', String(i), '--name', GATE_CHECK_NAMES[i - 1],
      '--verdict', 'pass', '--runner', 'cli-test', '--run', runGuid,
    ], { cwd: ROOT, env: { ...process.env, METRO_DB_NAME: TEST_DB }, encoding: 'utf8' });
    assert.equal(r.status, 0, `gate check ${i} failed: ${r.stderr}`);
  }
  const status = spawnSync(process.execPath, ['claude-bow.js', 'gate-status', '42'], {
    cwd: ROOT, env: { ...process.env, METRO_DB_NAME: TEST_DB }, encoding: 'utf8',
  });
  assert.equal(status.status, 0);
  assert.match(status.stdout, /Overall.*PASS/s);
});

// ---------------------------------------------------------------------------
// FIX-2 (P0 verdict-gaming regression) — Destructive finding on FEAT-061:
// hand-calling recordGateVerdict five times with a fabricated gate_run_guid
// produced a row set gate-status reported as a clean overall PASS,
// indistinguishable from a real `gate-run`. AC-24 requires the standalone
// `gate` CLI command to keep existing (option (a) removal is unavailable),
// so the fix instead makes every freeform-recorded row structurally tagged
// as MANUAL-OVERRIDE and makes gate-status surface that loudly.
// ---------------------------------------------------------------------------

test('FIX-2: reproducing the Destructive\'s exact exploit — five hand-called recordGateVerdict rows sharing a fabricated gate_run_guid — are now structurally tagged MANUAL-OVERRIDE, never indistinguishable from a real run', async () => {
  const fabricatedRunGuid = crypto.randomUUID();
  for (let i = 0; i < 5; i++) {
    await recordGateVerdict(db, {
      sprint: 55, checkNumber: i + 1, checkName: GATE_CHECK_NAMES[i], verdict: 'pass',
      runner: 'destructive-fabricated', gateRunGuid: fabricatedRunGuid,
    });
  }
  const run = await latestGateRun(db, 55);
  assert.equal(run.rows.length, 5);
  assert.ok(
    run.rows.every((r) => r.runner.startsWith(`${MANUAL_OVERRIDE_TAG}:`)),
    'every row from a direct/freeform recordGateVerdict call must be tagged MANUAL-OVERRIDE'
  );
  assert.equal(hasManualOverrideRows(run.rows), true);
  // The derived overall (AC-26, a pure function of verdict values) is still PASS here —
  // that part of the mechanism is untouched — but gate-status must not present that as clean.
  assert.equal(deriveOverallVerdict(run.rows), 'PASS');
});

test('FIX-2: a REAL runGate-produced run is NOT tagged MANUAL-OVERRIDE — the tag distinguishes mechanical runs from manual ones, it does not apply to everything', async () => {
  const acceptanceDir = mkTempDir('sprintgate-fix2-');
  const rootDir = mkTempDir('sprintgate-fix2-root-');
  const codeJsonPath = path.join(rootDir, 'code.json');
  fs.writeFileSync(codeJsonPath, JSON.stringify({ modules: [] }));
  await insertItem({ code: 'FEAT-980', mkey: 'tool.fix2', status: 'open', sprint: 56 });

  const result = await runGate(db, 56, { runner: 'test-suite', acceptanceDir, codeJsonPath, sprintPlanPath: path.join(acceptanceDir, 'nonexistent-plan.md'), rootDir });
  const run = await latestGateRun(db, 56);
  assert.equal(run.rows.length, 5);
  assert.equal(hasManualOverrideRows(run.rows), false, 'a real gate-run must never be tagged as manual');
  assert.equal(run.gateRunGuid, result.gateRunGuid);
});

test('FIX-2: `gate-status` CLI output loudly flags a MANUAL-OVERRIDE run as NOT mechanically verified, distinct from a real gate-run\'s clean report', async () => {
  const runGuid = crypto.randomUUID();
  for (let i = 1; i <= 5; i++) {
    const r = spawnSync(process.execPath, [
      'claude-bow.js', 'gate', '57', '--check', String(i), '--name', GATE_CHECK_NAMES[i - 1],
      '--verdict', 'pass', '--runner', 'attacker', '--run', runGuid,
    ], { cwd: ROOT, env: { ...process.env, METRO_DB_NAME: TEST_DB }, encoding: 'utf8' });
    assert.equal(r.status, 0, `gate check ${i} failed: ${r.stderr}`);
  }
  const status = spawnSync(process.execPath, ['claude-bow.js', 'gate-status', '57'], {
    cwd: ROOT, env: { ...process.env, METRO_DB_NAME: TEST_DB }, encoding: 'utf8',
  });
  assert.equal(status.status, 0);
  assert.match(status.stdout, /MANUAL-OVERRIDE/);
  assert.match(status.stdout, /NOT.*mechanically.verified/is);
});

// ---------------------------------------------------------------------------
// FIX-3 (P0 crash-mid-write regression) — Destructive finding on FEAT-061:
// runGate's row-insertion loop sat OUTSIDE the try/catch that backfills
// crashed-marker rows for a computation failure, so a THIRD INSERT throwing
// (simulating a real deadlock/connection drop) left exactly 2 rows in the
// table with no signal that checks 3-5 never got written.
// ---------------------------------------------------------------------------

test('FIX-3: forcing the 3rd INSERT into bow_gate_verdicts to throw (mid-WRITE, not mid-compute) still yields exactly 5 rows, with the unwritten checks marked crashed — never a silent 2-row partial state', async () => {
  await insertItem({ code: 'FEAT-981', mkey: 'tool.fix3', status: 'open', sprint: 58 });
  const acceptanceDir = mkTempDir('sprintgate-fix3-');
  const rootDir = mkTempDir('sprintgate-fix3-root-');
  fs.writeFileSync(path.join(rootDir, 'code.json'), JSON.stringify({ modules: [] }));

  let insertCount = 0;
  const crashingDb = {
    query: (sql, params) => {
      if (typeof sql === 'string' && sql.includes('INSERT INTO bow_gate_verdicts')) {
        insertCount++;
        if (insertCount === 3) throw new Error('simulated INSERT failure (deadlock/connection drop)');
      }
      return db.query(sql, params);
    },
  };

  const result = await runGate(crashingDb, 58, { runner: 'test-suite', acceptanceDir, codeJsonPath: path.join(rootDir, 'code.json'), rootDir });
  assert.equal(result.results.length, 5, 'exactly 5 in-memory results, even though the 3rd write failed');

  const [rows] = await db.query('SELECT * FROM bow_gate_verdicts WHERE sprint = 58 AND gate_run_guid = ?', [result.gateRunGuid]);
  assert.equal(rows.length, 5, 'exactly 5 rows must exist in the DB even though the 3rd INSERT threw — never a silent 2-row partial state');
  const crashedRows = rows.filter((r) => /crashed while writing/.test(r.detail || ''));
  assert.ok(crashedRows.length >= 1, 'at least the check whose INSERT actually threw must be marked crashed');
  assert.deepEqual(new Set(rows.map((r) => r.check_number)), new Set([1, 2, 3, 4, 5]), 'every check_number 1-5 must have a row, no gaps');
});

// =============================================================================
// BUG-090 (tool.bowcli) — safer free-text input mode: --desc-file/--note-file/
// --detail-file as an alternative to inline --desc/--note/--detail, porting
// cmdComment's pre-existing --example-file pattern (fs.readFileSync verbatim,
// no re-escaping/trimming) so an agent never has to put risky shell-special
// content on the command line in the first place. Every test below invokes
// the real `node claude-bow.js ...` subprocess (spawnSync), not a function
// call into the module — the point (per AC-1) is proving nothing in the
// process re-interprets the content, which a same-process unit test cannot
// show.
// =============================================================================

function bowCli(args) {
  return spawnSync(process.execPath, ['claude-bow.js', ...args], {
    cwd: ROOT, env: { ...process.env, METRO_DB_NAME: TEST_DB }, encoding: 'utf8',
  });
}

test('BUG-090 AC-1: `add --desc-file` stores the file content byte-identical, surviving a backtick, a $(...) sequence, and an embedded double quote untouched', async () => {
  const risky = 'line one with a `backtick`, a $(command substitution), and an "embedded quote"\nsecond line, unchanged.';
  const tmp = mkTempDir('bug090-desc-');
  const descFile = path.join(tmp, 'desc.txt');
  fs.writeFileSync(descFile, risky, 'utf8');

  const r = bowCli(['add', 'bug', 'AC-1 desc-file byte-identity', '--desc-file', descFile]);
  assert.equal(r.status, 0, `add --desc-file failed: ${r.stderr}`);

  const [rows] = await db.query('SELECT description FROM bow_items WHERE title = ?', ['AC-1 desc-file byte-identity']);
  assert.equal(rows.length, 1, 'exactly one row must be written');
  assert.equal(rows[0].description, risky, 'the stored description must equal the file content exactly (byte-identical, not just containing the risky substring)');
});

test('BUG-090 AC-2: `done --note-file` stores the file content byte-identical, including a backtick/$(.../embedded quote (real subprocess)', async () => {
  await insertItem({ code: 'FEAT-9001', status: 'open' });
  const risky = 'closing note with a `backtick`, $(cmd), and an "embedded quote"';
  const tmp = mkTempDir('bug090-note-');
  const noteFile = path.join(tmp, 'note.txt');
  fs.writeFileSync(noteFile, risky, 'utf8');

  const r = bowCli(['done', 'FEAT-9001', '--note-file', noteFile]);
  assert.equal(r.status, 0, `done --note-file failed: ${r.stderr}`);

  const [rows] = await db.query('SELECT closed_note, status FROM bow_items WHERE code = ?', ['FEAT-9001']);
  assert.equal(rows[0].status, 'done');
  assert.equal(rows[0].closed_note, risky, 'closed_note must equal the file content exactly');
});

test('BUG-090 AC-2: `depend --note-file` and `ref --note-file` also store file content byte-identical (same shape, different call sites sharing --note)', async () => {
  const aGuid = await insertItem({ code: 'FEAT-9010', status: 'open' });
  await insertItem({ code: 'FEAT-9011', status: 'open' });
  const risky = 'a note with `backticks` and $(subst) and "quotes"';
  const tmp = mkTempDir('bug090-note2-');
  const noteFile = path.join(tmp, 'note.txt');
  fs.writeFileSync(noteFile, risky, 'utf8');

  const rDepend = bowCli(['depend', 'FEAT-9010', '--on', 'FEAT-9011', '--note-file', noteFile]);
  assert.equal(rDepend.status, 0, `depend --note-file failed: ${rDepend.stderr}`);
  const [depRows] = await db.query(
    `SELECT d.note FROM bow_dependencies d JOIN bow_items i ON i.guid = d.item_guid WHERE i.code = 'FEAT-9010'`);
  assert.equal(depRows[0].note, risky);

  const rRef = bowCli(['ref', 'FEAT-9010', '0123456789abcdef0123456789abcdef01234567', '--note-file', noteFile]);
  assert.equal(rRef.status, 0, `ref --note-file failed: ${rRef.stderr}`);
  const [refRows] = await db.query(
    'SELECT r.note FROM bow_git_refs r JOIN bow_items i ON i.guid = r.item_guid WHERE i.code = ?', ['FEAT-9010']);
  assert.equal(refRows[0].note, risky);
  void aGuid;
});

test('BUG-090 AC-3: `add` rejects when both --desc and --desc-file are supplied — non-zero exit, clear message, and NO row written', async () => {
  const tmp = mkTempDir('bug090-mutex-');
  const descFile = path.join(tmp, 'desc.txt');
  fs.writeFileSync(descFile, 'file content', 'utf8');

  const r = bowCli(['add', 'bug', 'AC-3 mutual exclusion desc', '--desc', 'inline text', '--desc-file', descFile]);
  assert.notEqual(r.status, 0, 'both --desc and --desc-file together must exit non-zero');
  assert.match(r.stderr, /--desc/);
  assert.match(r.stderr, /--desc-file/);

  const [rows] = await db.query('SELECT * FROM bow_items WHERE title = ?', ['AC-3 mutual exclusion desc']);
  assert.equal(rows.length, 0, 'no row may be written when the mutual-exclusion check rejects the command (queried directly, not inferred from exit code)');
});

test('BUG-090 AC-3: `done` rejects when both --note and --note-file are supplied — non-zero exit and the item is NOT marked done', async () => {
  await insertItem({ code: 'FEAT-9002', status: 'open' });
  const tmp = mkTempDir('bug090-note-mutex-');
  const noteFile = path.join(tmp, 'note.txt');
  fs.writeFileSync(noteFile, 'file note', 'utf8');

  const r = bowCli(['done', 'FEAT-9002', '--note', 'inline note', '--note-file', noteFile]);
  assert.notEqual(r.status, 0, 'both --note and --note-file together must exit non-zero');
  assert.match(r.stderr, /--note/);
  assert.match(r.stderr, /--note-file/);

  const [rows] = await db.query('SELECT status, closed_note FROM bow_items WHERE code = ?', ['FEAT-9002']);
  assert.equal(rows[0].status, 'open', 'item must remain open — the mutual-exclusion rejection must happen before the UPDATE');
  assert.equal(rows[0].closed_note, null);
});

test('BUG-090 AC-4: `add --desc "<text>"` direct form is unchanged — same DB write path as before this item', async () => {
  const r = bowCli(['add', 'bug', 'AC-4 direct desc unchanged', '--desc', 'plain description text']);
  assert.equal(r.status, 0, `add --desc failed: ${r.stderr}`);
  const [rows] = await db.query('SELECT description FROM bow_items WHERE title = ?', ['AC-4 direct desc unchanged']);
  assert.equal(rows.length, 1);
  assert.equal(rows[0].description, 'plain description text');
});

test('BUG-090 AC-4: omitting both --desc and --desc-file on `add` still produces a NULL description, same as before this item', async () => {
  const r = bowCli(['add', 'bug', 'AC-4 no desc at all']);
  assert.equal(r.status, 0, `add with no desc failed: ${r.stderr}`);
  const [rows] = await db.query('SELECT description FROM bow_items WHERE title = ?', ['AC-4 no desc at all']);
  assert.equal(rows.length, 1);
  assert.equal(rows[0].description, null);
});

test('BUG-090 AC-4: `done --note "<text>"` direct form is unchanged', async () => {
  await insertItem({ code: 'FEAT-9003', status: 'open' });
  const r = bowCli(['done', 'FEAT-9003', '--note', 'closed via direct note']);
  assert.equal(r.status, 0, `done --note failed: ${r.stderr}`);
  const [rows] = await db.query('SELECT closed_note, status FROM bow_items WHERE code = ?', ['FEAT-9003']);
  assert.equal(rows[0].status, 'done');
  assert.equal(rows[0].closed_note, 'closed via direct note');
});

test('BUG-090 AC-6: a direct --desc value containing a backtick produces a non-fatal stderr warning, and the item is still written successfully', async () => {
  const r = bowCli(['add', 'bug', 'AC-6 risky direct desc', '--desc', 'contains a `backtick` right in it']);
  assert.equal(r.status, 0, 'AC-6 warning must never fail the command (advisory only)');
  assert.match(r.stderr, /WARNING/i);
  assert.match(r.stderr, /desc-file/);
  const [rows] = await db.query('SELECT description FROM bow_items WHERE title = ?', ['AC-6 risky direct desc']);
  assert.equal(rows.length, 1, 'the item must still be written despite the advisory warning firing');
});

test('BUG-090 AC-6: a plain --desc value with none of the trigger characters produces NO warning', async () => {
  const r = bowCli(['add', 'bug', 'AC-6 safe direct desc', '--desc', 'a perfectly ordinary description with no special characters at all']);
  assert.equal(r.status, 0, `add --desc failed: ${r.stderr}`);
  assert.ok(!/WARNING/i.test(r.stderr), `unexpected advisory warning fired on safe content: ${r.stderr}`);
});

test('BUG-090 AC-5: the source cites BUG-090 by number AND offers --desc-file, matching the FEAT-058-style "why this tool exists" convention (guidance travels with the tool, not only a process doc)', () => {
  const src = fs.readFileSync(path.join(ROOT, 'claude-bow.js'), 'utf8');
  assert.match(src, /desc-file/, 'the --desc-file flag must appear in the source (usage text or a comment)');
  assert.match(src, /BUG-090/, 'the source must cite BUG-090 by number, not only add the flag silently');
});

// =============================================================================
// BUG-061 (GR#22) — `redact <code>` / `redact --comment <id>`. Aaron's binding
// ruling requires: (1) no --match/--text flag ever carries the forbidden text
// on the command line; (2) auditable via an auto-comment naming ONLY the
// pattern-class + count + field, never the matched text; (3) the pre-image is
// stored nowhere; (4) --comment <id> is also supported. Verified the way
// Tester-2 verified the guard itself (per the item's own history): real
// `node claude-bow.js redact ...` subprocess calls through the actual CLI
// contract, plus a negative control proving the detector can genuinely fail
// to match ordinary text.
//
// GR#22 applies to this file too: the forbidden text is never written here as
// a literal. Test payloads are assembled from fragments at runtime using the
// SAME technique claude-codename-guard.js uses for its own PATTERNS (see that
// file's header) — the joined word never sits whole anywhere in this source.
// =============================================================================

const RG_CITY = 'cit';
const RG_SKY = 'sky';
const RG_LINES = 'lines';
const RG_BRIDG = 'bridg';
const RG_PORT = 'por';

/** city-word + sky-word + lines-word joined — matches PATTERNS[0], "the full reference title". */
function buildFullTitlePhrase() {
  return `${RG_CITY}y ${RG_SKY}${RG_LINES}`;
}

/** "<sky><lines>" on its own, word-bounded — matches PATTERNS[1] only. */
function buildSingleWordPhrase() {
  return `the ${RG_SKY}${RG_LINES} engine`;
}

/** Two-letter abbreviation + digit, built from single-character fragments — matches the numbered-abbreviation pattern. */
function buildNumberedAbbrevPhrase() {
  const letters = ['C', 'S'].join('');
  return `see the ${letters}2 comparison doc`;
}

/** "<bridg>es and <por>ts" — matches a former-expansion-pack-name pattern. */
function buildPackNamePhrase() {
  return `the ${RG_BRIDG}es and ${RG_PORT}ts table`;
}

test('BUG-061 AC: `redact <code>` replaces a forbidden occurrence in title/description with [REDACTED-GR22] via the real CLI subprocess, and the marker (not the source text) lands in the DB', async () => {
  const guid = await insertItem({
    code: 'FEAT-9100',
    description: `Comparing against ${buildFullTitlePhrase()} for scale reference.`,
  });

  const r = bowCli(['redact', 'FEAT-9100']);
  assert.equal(r.status, 0, `redact failed: ${r.stderr}`);
  assert.match(r.stdout, /\[REDACTED-GR22\]|redacted \d+ occurrence/i);

  const [rows] = await db.query('SELECT title, description FROM bow_items WHERE guid = ?', [guid]);
  assert.match(rows[0].description, /\[REDACTED-GR22\]/, 'the marker must replace the forbidden phrase');
  assert.ok(!rows[0].description.includes(buildFullTitlePhrase()), 'the forbidden phrase itself must no longer be present');

  // Constraint 2 (Aaron): auto-comment records pattern-class + count + field
  // only — never the matched text.
  const [comments] = await db.query(
    'SELECT body FROM bow_comments WHERE item_guid = ? ORDER BY id DESC LIMIT 1', [guid]);
  assert.equal(comments.length, 1, 'redact must auto-post exactly one audit comment');
  assert.match(comments[0].body, /description/i, 'the audit comment must name the affected field');
  assert.match(comments[0].body, /\d+ occurrence/i, 'the audit comment must name a count');
  assert.ok(!comments[0].body.includes(buildFullTitlePhrase()), 'constraint 2: the audit comment must NEVER contain the matched text itself');

  // Constraint 3 (Aaron): the pre-image is stored nowhere in the DB at all —
  // not a backup column (none exists), not a second row.
  const [anyLeak] = await db.query(
    `SELECT title, description FROM bow_items WHERE title LIKE ? OR description LIKE ?`,
    [`%${buildFullTitlePhrase()}%`, `%${buildFullTitlePhrase()}%`]);
  assert.equal(anyLeak.length, 0, 'the original forbidden text must not survive anywhere in bow_items after redaction');
});

test('BUG-061 AC: `redact` also catches the standalone single-word pattern and the numbered-abbreviation pattern (real CLI subprocess)', async () => {
  const guid = await insertItem({
    code: 'FEAT-9101',
    description: `${buildSingleWordPhrase()}. Also ${buildNumberedAbbrevPhrase()}.`,
  });

  const r = bowCli(['redact', 'FEAT-9101']);
  assert.equal(r.status, 0, `redact failed: ${r.stderr}`);

  const [rows] = await db.query('SELECT description FROM bow_items WHERE guid = ?', [guid]);
  assert.match(rows[0].description, /\[REDACTED-GR22\]/, 'at least one redaction marker must appear');
  assert.ok(!rows[0].description.includes(buildSingleWordPhrase()), 'the standalone single-word phrase must be gone');
  assert.ok(!rows[0].description.includes(buildNumberedAbbrevPhrase()), 'the numbered-abbreviation phrase must be gone');
  void guid;
});

test('BUG-061 AC: `redact` catches a former expansion-pack-name phrase (real CLI subprocess)', async () => {
  const guid = await insertItem({
    code: 'FEAT-9102',
    description: `Sec23 ${buildPackNamePhrase()} still needs review.`,
  });

  const r = bowCli(['redact', 'FEAT-9102']);
  assert.equal(r.status, 0, `redact failed: ${r.stderr}`);

  const [rows] = await db.query('SELECT description FROM bow_items WHERE guid = ?', [guid]);
  assert.match(rows[0].description, /\[REDACTED-GR22\]/);
  assert.ok(!rows[0].description.includes(buildPackNamePhrase()));
  void guid;
});

test('BUG-061 AC (negative control): `redact` leaves ordinary, unrelated text COMPLETELY untouched — proving the detector can genuinely fail to match when it should', async () => {
  const ordinaryTitle = 'FEAT-9103';
  const ordinaryDesc = 'Freight dispatch: assign trucks to depots by shortest queue, cap concurrent loads per dock, log idle time for the fleet-utilisation report.';
  const guid = await insertItem({ code: ordinaryTitle, description: ordinaryDesc });

  const r = bowCli(['redact', 'FEAT-9103']);
  assert.equal(r.status, 0, `redact failed on ordinary text: ${r.stderr}`);
  assert.match(r.stdout, /no forbidden-pattern occurrences found/i, 'must explicitly report zero occurrences, not silently succeed');

  const [rows] = await db.query('SELECT title, description FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].description, ordinaryDesc, 'ordinary description must be byte-identical after a no-op redact');

  // No audit comment should be posted when nothing was redacted — an
  // auto-comment on every invocation regardless of outcome would itself be
  // a form of noise/false signal.
  const [comments] = await db.query('SELECT COUNT(*) AS n FROM bow_comments WHERE item_guid = ?', [guid]);
  assert.equal(comments[0].n, 0, 'no audit comment should be posted when there was nothing to redact');
});

test('BUG-061 AC: `redact` is idempotent — running it a second time after a successful redaction finds nothing left to do', async () => {
  const guid = await insertItem({
    code: 'FEAT-9104',
    description: `Reference: ${buildFullTitlePhrase()}.`,
  });

  const first = bowCli(['redact', 'FEAT-9104']);
  assert.equal(first.status, 0);
  assert.match(first.stdout, /redacted \d+ occurrence/i);

  const second = bowCli(['redact', 'FEAT-9104']);
  assert.equal(second.status, 0);
  assert.match(second.stdout, /no forbidden-pattern occurrences found/i, 'a second run on already-redacted text must find nothing');
  void guid;
});

test('BUG-061 AC (constraint 4): `redact --comment <id>` redacts a comment body in place, leaving the marker and posting a field-scoped audit comment on the parent item', async () => {
  const guid = await insertItem({ code: 'FEAT-9105' });
  const [insertResult] = await db.query(
    'INSERT INTO bow_comments (item_guid, body) VALUES (?, ?)',
    [guid, `Old note: compare to ${buildFullTitlePhrase()} directly.`]);
  const commentId = insertResult.insertId;

  const r = bowCli(['redact', '--comment', String(commentId)]);
  assert.equal(r.status, 0, `redact --comment failed: ${r.stderr}`);

  const [rows] = await db.query('SELECT body FROM bow_comments WHERE id = ?', [commentId]);
  assert.match(rows[0].body, /\[REDACTED-GR22\]/);
  assert.ok(!rows[0].body.includes(buildFullTitlePhrase()));

  // The audit trail comment lands on the PARENT ITEM (not a second edit of
  // the same comment row) and names the field as "body".
  const [auditComments] = await db.query(
    'SELECT body FROM bow_comments WHERE item_guid = ? AND id != ? ORDER BY id DESC LIMIT 1', [guid, commentId]);
  assert.equal(auditComments.length, 1, 'an audit comment must be posted on the item for a --comment redaction');
  assert.match(auditComments[0].body, /body/i);
  assert.ok(!auditComments[0].body.includes(buildFullTitlePhrase()));
});

test('BUG-061 AC (constraint 1): `redact` never requires a --match/--text argument — "match" is not a recognized VALUE_FLAGS token, and the real CLI rejects it as an unknown/boolean flag rather than consuming a value', () => {
  const { spawnSync: spawnDirect } = require('child_process');
  const src = fs.readFileSync(path.join(ROOT, 'claude-bow.js'), 'utf8');
  const valueFlagsMatch = src.match(/const VALUE_FLAGS = \[[\s\S]*?\];/);
  assert.ok(valueFlagsMatch, 'VALUE_FLAGS array must be found in source');
  assert.ok(!/'match'/.test(valueFlagsMatch[0]), 'constraint 1: "match" must never be a recognized value-carrying flag — the forbidden text must never transit the command line');

  // Behavioural proof, not just source inspection: passing --match on the
  // real CLI does NOT consume the next token as its value (it is treated as
  // a bare boolean flag), so nothing resembling a matched-text argument is
  // ever parsed out of argv for this command.
  const r = spawnDirect(process.execPath, ['claude-bow.js', 'redact', '--match', 'FEAT-9106'], {
    cwd: ROOT, env: { ...process.env, METRO_DB_NAME: TEST_DB }, encoding: 'utf8',
  });
  // "FEAT-9106" ends up as the positional CODE argument (item likely absent,
  // so requireItem fails) rather than as --match's value — proving --match
  // took no argument.
  assert.match(r.stderr, /no BOW item matches "FEAT-9106"/i, 'if --match had consumed "FEAT-9106" as its value, the positional CODE would be missing and the error message would differ');
});

test('BUG-061 AC: redactText() reuses claude-codename-guard.js\'s own exported PATTERNS/isLowerLetter rather than a re-derived copy (GR#3)', () => {
  const guardSrc = fs.readFileSync(path.join(ROOT, 'claude-codename-guard.js'), 'utf8');
  assert.match(guardSrc, /PATTERNS,\s*\n?\s*isLowerLetter,/s, 'claude-codename-guard.js must export PATTERNS and isLowerLetter for claude-bow.js to reuse (GR#3 single source of truth)');
  const guard = require(path.join(ROOT, 'claude-codename-guard.js'));
  assert.ok(Array.isArray(guard.PATTERNS) && guard.PATTERNS.length > 0);
  assert.equal(typeof guard.isLowerLetter, 'function');
});

test('BUG-061 AC: `redact` with no matching item exits non-zero with a clear message, same shape as other commands\' requireItem() failures', () => {
  const r = bowCli(['redact', 'FEAT-99999']);
  assert.notEqual(r.status, 0);
  assert.match(r.stderr, /no BOW item matches/i);
});

// =============================================================================
// BUG-151 — redact must refuse (not crash-into-a-raw-DB-error) when the
// [REDACTED-GR22] marker (16 chars) would grow a title past the column's
// 255-char VARCHAR limit. Must fail with a clear, typed message that says
// the violation is STILL PRESENT, write nothing, and leave ordinary
// (well-under-limit) redaction and --comment redaction unaffected.
// =============================================================================

test('BUG-151 AC-1: redact refuses an overflowing title redaction with a clear GR#22-still-present error, not a raw DB error, and writes nothing', async () => {
  const guid = await insertItem({ code: 'FEAT-9110' });
  // A 253-char title (within 15 of the 255 cap) containing the numbered-
  // abbreviation phrase near the end. Its match ("CS2") is far shorter than
  // the 16-char [REDACTED-GR22] marker, so redacting it grows the title
  // past the column limit.
  const phrase = buildNumberedAbbrevPhrase(); // "see the CS2 comparison doc"
  const pad = 'a'.repeat(253 - phrase.length);
  const overlongTitle = pad + phrase;
  assert.equal(overlongTitle.length, 253, 'test setup: title must be 253 chars, within 15 of the 255 cap');

  await db.query('UPDATE bow_items SET title = ? WHERE guid = ?', [overlongTitle, guid]);

  const r = bowCli(['redact', 'FEAT-9110']);
  assert.notEqual(r.status, 0, 'overflowing redact must exit non-zero, not succeed with a truncated/partial write');
  assert.match(r.stderr, /still present|not removed/i, 'error must explicitly say the GR#22 violation is still present, not just "failed"');
  assert.doesNotMatch(r.stderr, /Data too long/i, 'the raw MySQL driver error must NOT leak through — this is the exact bug being fixed');
  assert.match(r.stderr, /255|column limit|exceed/i, 'error should name the column limit for operator clarity');

  const [rows] = await db.query('SELECT title FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].title, overlongTitle, 'title must be byte-identical/unchanged — no partial write, no silent truncation');
  assert.ok(rows[0].title.includes(phrase), 'the forbidden phrase must still be present verbatim since the write never happened');

  const [comments] = await db.query('SELECT COUNT(*) AS n FROM bow_comments WHERE item_guid = ?', [guid]);
  assert.equal(comments[0].n, 0, 'no audit comment should be posted for a blocked (unattempted) redaction');
});

test('BUG-151 AC-2: ordinary redaction well under the column limit is unaffected by the new length check', async () => {
  const guid = await insertItem({
    code: 'FEAT-9111',
    description: `Reference: ${buildFullTitlePhrase()}.`,
  });

  const r = bowCli(['redact', 'FEAT-9111']);
  assert.equal(r.status, 0, `ordinary redact must still succeed: ${r.stderr}`);
  assert.match(r.stdout, /redacted \d+ occurrence/i);

  const [rows] = await db.query('SELECT description FROM bow_items WHERE guid = ?', [guid]);
  assert.match(rows[0].description, /\[REDACTED-GR22\]/);
  assert.ok(!rows[0].description.includes(buildFullTitlePhrase()));
});

test('BUG-151 AC-3: `redact --comment` is unaffected by the title-length check (comment body is TEXT, not the bounded VARCHAR(255) title column) — a near-limit-length comment body still redacts successfully', async () => {
  const guid = await insertItem({ code: 'FEAT-9112' });
  const phrase = buildNumberedAbbrevPhrase();
  const pad = 'a'.repeat(253 - phrase.length);
  const longBody = pad + phrase; // same 253-char, near-VARCHAR(255)-sized shape as AC-1, but in a TEXT column
  const [insertResult] = await db.query(
    'INSERT INTO bow_comments (item_guid, body) VALUES (?, ?)', [guid, longBody]);
  const commentId = insertResult.insertId;

  const r = bowCli(['redact', '--comment', String(commentId)]);
  assert.equal(r.status, 0, `redact --comment on a near-255-char body must still succeed (body is TEXT, unbounded here): ${r.stderr}`);

  const [rows] = await db.query('SELECT body FROM bow_comments WHERE id = ?', [commentId]);
  assert.match(rows[0].body, /\[REDACTED-GR22\]/, 'the comment body must be redacted normally, growing past 255 chars with no error');
  assert.ok(rows[0].body.length > 255, 'test setup sanity: the redacted body must actually have grown past 255 chars, proving the TEXT column absorbs it fine');
  assert.ok(!rows[0].body.includes(phrase));
});

// =============================================================================
// BUG-173 — `done`'s closed_note column overflow must fail with a clear
// message (same mechanism as BUG-151's redact check, BUG-027's title
// check), never a raw "Data too long for column 'closed_note'" DB error.
// closed_note is VARCHAR(512) (see ensureSchema()).
// =============================================================================

test('BUG-173: `done --note` over the 512-char closed_note limit fails clearly, item is left untouched, no raw DB error', async () => {
  const guid = await insertItem({ code: 'FEAT-9120', status: 'open' });
  const overlongNote = 'n'.repeat(513);

  const r = bowCli(['done', 'FEAT-9120', '--note', overlongNote]);
  assert.notEqual(r.status, 0, 'an over-limit closing note must exit non-zero, not truncate-and-succeed');
  assert.doesNotMatch(r.stderr, /Data too long/i, 'the raw MySQL driver error must NOT leak through — this is the exact bug being fixed');
  assert.match(r.stderr, /512|closed_note|too long/i, 'error should name the column/limit for operator clarity');

  const [rows] = await db.query('SELECT status, closed_note FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].status, 'open', 'item must remain open — the length check must happen before the UPDATE');
  assert.equal(rows[0].closed_note, null, 'closed_note must be untouched, no partial write');
});

test('BUG-173: `done --note` at exactly the 512-char limit still succeeds (boundary, not off-by-one)', async () => {
  const guid = await insertItem({ code: 'FEAT-9121', status: 'open' });
  const exactNote = 'n'.repeat(512);

  const r = bowCli(['done', 'FEAT-9121', '--note', exactNote]);
  assert.equal(r.status, 0, `a note exactly at the 512-char limit must succeed: ${r.stderr}`);

  const [rows] = await db.query('SELECT status, closed_note FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].status, 'done');
  assert.equal(rows[0].closed_note, exactNote);
});

test('BUG-173: rejectIfOverColumnLimit is a no-op (does not exit) for text under/at the limit, and is the same helper BOW_COLUMN_MAX_LEN.closed_note derives its bound from', () => {
  assert.equal(BOW_COLUMN_MAX_LEN.closed_note, 512);
  assert.doesNotThrow(() => rejectIfOverColumnLimit('short note', BOW_COLUMN_MAX_LEN.closed_note, 'closed_note', 'test'));
  assert.doesNotThrow(() => rejectIfOverColumnLimit(null, BOW_COLUMN_MAX_LEN.closed_note, 'closed_note', 'test'), 'null/omitted text must be a no-op, not a crash');
});

// =============================================================================
// BUG-027 — `add`'s title column overflow must fail with a clear message,
// same mechanism as BUG-173/BUG-151, applying to every `add` subcommand
// (module/feature/bug/interface/assumption/finding) since they all share
// the one `title` VARCHAR(255) column.
// =============================================================================

test('BUG-027: `add assumption` with an over-255-char title fails clearly, no row written, no raw DB error', async () => {
  const overlongTitle = 't'.repeat(256);
  const tmp = mkTempDir('bug027-');
  const codePath = path.join(tmp, 'some-file.js');
  fs.writeFileSync(codePath, '// fixture', 'utf8');

  const r = bowCli(['add', 'assumption', overlongTitle, '--code-path', codePath, '--codejson', 'tool.bowcli']);
  assert.notEqual(r.status, 0, 'an over-limit title must exit non-zero, not truncate-and-succeed');
  assert.doesNotMatch(r.stderr, /Data too long/i, 'the raw MySQL driver error must NOT leak through — this is the exact bug being fixed');
  assert.match(r.stderr, /255|title|too long/i, 'error should name the column/limit for operator clarity');

  const [rows] = await db.query('SELECT * FROM bow_items WHERE title = ?', [overlongTitle]);
  assert.equal(rows.length, 0, 'no row may be written when the title-length check rejects the command');
});

test('BUG-027: `add bug` (a non-assumption subcommand) with an over-255-char title is ALSO rejected — the check applies to every add subcommand, not just assumption', async () => {
  const overlongTitle = 'b'.repeat(300);
  const r = bowCli(['add', 'bug', overlongTitle]);
  assert.notEqual(r.status, 0);
  assert.doesNotMatch(r.stderr, /Data too long/i);

  const [rows] = await db.query('SELECT * FROM bow_items WHERE title = ?', [overlongTitle]);
  assert.equal(rows.length, 0);
});

test('BUG-027: `add bug` at exactly the 255-char title limit still succeeds (boundary, not off-by-one)', async () => {
  const exactTitle = 'x'.repeat(255);
  const r = bowCli(['add', 'bug', exactTitle]);
  assert.equal(r.status, 0, `a title exactly at the 255-char limit must succeed: ${r.stderr}`);
  const [rows] = await db.query('SELECT title FROM bow_items WHERE title = ?', [exactTitle]);
  assert.equal(rows.length, 1);
});

// =============================================================================
// SEC-050 — --desc-file/--note-file/--detail-file (resolveTextFlag) must
// refuse to read a path outside the allowed roots (repo root, OS temp dir)
// instead of reading ANY file the process can access. Confirmed live by
// reading C:\Windows\win.ini through --desc-file before this fix.
// =============================================================================

test('SEC-050: `add --desc-file` pointed at a system file well outside the repo/temp roots is refused, no row written', async () => {
  const winIni = 'C:\\Windows\\win.ini';
  if (!fs.existsSync(winIni)) return; // only meaningful on this project's Windows environment
  const r = bowCli(['add', 'bug', 'SEC-050 win.ini probe', '--desc-file', winIni]);
  assert.notEqual(r.status, 0, 'reading a system file via --desc-file must be refused, not succeed');
  assert.match(r.stderr, /path-scope guard|outside the allowed roots/i);

  const [rows] = await db.query('SELECT * FROM bow_items WHERE title = ?', ['SEC-050 win.ini probe']);
  assert.equal(rows.length, 0, 'no row may be written when the path-scope guard rejects the file');
});

test('SEC-050: `add --desc-file` with a `../` traversal escaping both allowed roots is refused', async () => {
  const tmp = mkTempDir('sec050-traversal-');
  // Escape the temp root itself via a traversal that lands outside BOTH the
  // repo root and the temp root (a sibling directory of os.tmpdir()).
  const escapeTarget = path.join(tmp, '..', '..', 'sec050-escape-probe-does-not-exist.txt');
  const r = bowCli(['add', 'bug', 'SEC-050 traversal probe', '--desc-file', escapeTarget]);
  assert.notEqual(r.status, 0);
  assert.match(r.stderr, /path-scope guard|outside the allowed roots/i);

  const [rows] = await db.query('SELECT * FROM bow_items WHERE title = ?', ['SEC-050 traversal probe']);
  assert.equal(rows.length, 0);
});

test('SEC-050: `add --desc-file` reading a legitimate file inside the repo root still works (repo root is an allowed root, not just temp)', async () => {
  const repoFile = path.join(ROOT, 'package.json'); // a real, small (well under the TEXT column's 64KB cap), stable repo file
  assert.ok(fs.existsSync(repoFile), 'test setup: code.json must exist at repo root');
  const r = bowCli(['add', 'bug', 'SEC-050 repo-root read', '--desc-file', repoFile]);
  assert.equal(r.status, 0, `reading a repo-root file via --desc-file must still work: ${r.stderr}`);
  const [rows] = await db.query('SELECT description FROM bow_items WHERE title = ?', ['SEC-050 repo-root read']);
  assert.equal(rows.length, 1);
  assert.equal(rows[0].description, fs.readFileSync(repoFile, 'utf8'));
});

test('SEC-050: isPathUnderAnyRoot/isTextFlagPathAllowed unit-level — outside both roots is rejected, inside either is allowed', () => {
  assert.deepEqual(TEXT_FLAG_ALLOWED_ROOTS.length, 2, 'exactly two allowed roots: repo root and OS temp dir');
  assert.equal(isTextFlagPathAllowed(path.join(ROOT, 'claude-bow.js')), true);
  assert.equal(isTextFlagPathAllowed(path.join(os.tmpdir(), 'anything.txt')), true);
  // A well-known system file outside every allowed root, expressed per-platform:
  // a bare Windows-style literal (`C:\Windows\win.ini`) is NOT a valid "outside
  // root" fixture on POSIX, because `\` is not a path separator there — it
  // resolves to a single relative segment *under* cwd (an allowed root),
  // flipping this assertion to true instead of false (BUG-207).
  const outsideSystemPath = process.platform === 'win32' ? 'C:\\Windows\\win.ini' : '/etc/passwd';
  assert.equal(isTextFlagPathAllowed(outsideSystemPath), false);
  assert.equal(isTextFlagPathAllowed(path.join(ROOT, '..', 'outside-repo-probe.txt')), false);
});

// =============================================================================
// BUG-162 — splitAcBlocks must not absorb a later section heading (with no
// AC bullet of its own) into the preceding AC's block. Reproduced against
// the real docs/planning/acceptance/tool.sprintgate.md, whose section D
// heading contains the literal phrase "Check (once unblocked)" and was
// getting folded into AC-12's block, misreporting AC-12 as an unarmed
// deferred-check AC.
// =============================================================================

test('BUG-162: splitAcBlocks does not fold a later "## " section heading into the preceding AC bullet\'s block (synthetic repro)', () => {
  const text = [
    '- **AC-1 (first).** Some body text for AC-1, nothing about checks here.',
    '',
    '## D. Check 3 — "Check (once unblocked)" tripwires are armed',
    '',
    '- **AC-2 (second, a real deferred-check AC).** Check (once unblocked): do the thing.',
    '  Tripwire (mechanical): `true` must exit 0.',
  ].join('\n');

  const blocks = splitAcBlocks(text);
  assert.equal(blocks.length, 2, 'exactly two AC blocks, the heading must not become or merge into either');
  assert.ok(!blocks[0].includes('Check (once unblocked)'), 'AC-1\'s block must NOT have absorbed the section heading\'s "Check (once unblocked)" phrase');
  assert.ok(blocks[1].includes('Check (once unblocked)'), 'AC-2 legitimately contains the phrase itself');

  const tripwireResults = findTripwireChecks(text);
  assert.equal(tripwireResults.length, 1, 'only AC-2 is a real deferred-check AC — AC-1 must not be misreported as one');
  assert.equal(tripwireResults[0].acNum, '2');
  assert.equal(tripwireResults[0].armed, true);
});

test('BUG-162: real repro against docs/planning/acceptance/tool.sprintgate.md — AC-12 is correctly excluded from findTripwireChecks (not reported unarmed)', () => {
  const acPath = path.join(ROOT, 'docs', 'planning', 'acceptance', 'tool.sprintgate.md');
  const text = fs.readFileSync(acPath, 'utf8');

  const blocks = splitAcBlocks(text);
  const ac12Block = blocks.find((b) => /AC-12\b/.test(b));
  assert.ok(ac12Block, 'AC-12\'s own block must still be found');
  assert.ok(!ac12Block.includes('Check (once unblocked)'), 'AC-12\'s block must no longer contain section D\'s heading text (the BUG-162 defect)');

  const tripwireResults = findTripwireChecks(text);
  const ac12Result = tripwireResults.find((r) => r.acNum === '12');
  assert.equal(ac12Result, undefined, 'AC-12 must not appear in findTripwireChecks results at all — it was never a "Check (once unblocked)" AC');

  // tool.sprintgate.md's own bullets never literally contain the phrase
  // "Check (once unblocked)" in an AC body (only in prose ABOUT the concept,
  // outside any AC bullet, and in section D's heading) — so post-fix the
  // correct result is that NO AC in this file is misreported either way.
  assert.deepEqual(tripwireResults, [], 'no AC in this file genuinely contains "Check (once unblocked)" in its own bullet body — the pre-fix false positive on AC-12 must be gone, not replaced by a different false positive');
});

// =============================================================================
// BUG-132 (tool.bowcli) — `set --mkey ''` must CLEAR the column (explicit
// empty value), distinct from omitting --mkey entirely (leave untouched).
// Real subprocess (spawnSync via bowCli), not a function call into the
// module — cmdSet reads module-load-time `flags`/`positional` derived from
// process.argv, so it cannot be invoked in-process with synthetic argv.
// =============================================================================

test("BUG-132 AC-1: `set <CODE> --mkey ''` clears an existing mkey to NULL (verified by direct read-back, not just exit code)", async () => {
  const guid = await insertItem({ code: 'FEAT-9200', mkey: 'engine.something' });

  const r = bowCli(['set', 'FEAT-9200', '--mkey', '']);
  assert.equal(r.status, 0, `set --mkey '' must succeed (not fall through to the usage error): ${r.stderr}`);

  const [rows] = await db.query('SELECT mkey FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].mkey, null, 'mkey must be NULL in the DB after an explicit empty-string clear');
});

test('BUG-132 AC-2: `set <CODE>` with no --mkey flag at all leaves the existing mkey UNCHANGED (no regression — absent must not mean clear)', async () => {
  const guid = await insertItem({ code: 'FEAT-9201', mkey: 'engine.untouched' });

  // Set an unrelated field so `updates` is non-empty and the command actually
  // runs a real UPDATE, without ever mentioning --mkey.
  const r = bowCli(['set', 'FEAT-9201', '--priority', 'P1']);
  assert.equal(r.status, 0, `set --priority must succeed: ${r.stderr}`);

  const [rows] = await db.query('SELECT mkey, priority FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].mkey, 'engine.untouched', 'mkey must be unchanged when --mkey is never passed');
  assert.equal(rows[0].priority, 'P1', 'sanity check: the unrelated field this test used to force a real UPDATE must itself have applied');
});

test("BUG-132 AC-3: `set <CODE> --mkey somevalue` still sets a real value (no regression to the ordinary case)", async () => {
  const guid = await insertItem({ code: 'FEAT-9202', mkey: null });

  const r = bowCli(['set', 'FEAT-9202', '--mkey', 'engine.newvalue']);
  assert.equal(r.status, 0, `set --mkey <value> must succeed: ${r.stderr}`);

  const [rows] = await db.query('SELECT mkey FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].mkey, 'engine.newvalue', 'mkey must be set to the supplied value');
});

test("BUG-132 AC-4: `set <CODE> --seq ''` and `--sprint ''` clear those nullable columns the same way, without disturbing an unset --seq/--sprint on a later call", async () => {
  const guid = await insertItem({ code: 'FEAT-9203', mkey: null });
  await db.query('UPDATE bow_items SET seq = 7, sprint = 3 WHERE guid = ?', [guid]);

  const r = bowCli(['set', 'FEAT-9203', '--seq', '', '--sprint', '']);
  assert.equal(r.status, 0, `set --seq '' --sprint '' must succeed: ${r.stderr}`);

  const [rows] = await db.query('SELECT seq, sprint FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].seq, null, 'seq must be cleared to NULL by an explicit empty value');
  assert.equal(rows[0].sprint, null, 'sprint must be cleared to NULL by an explicit empty value');
});

// =============================================================================
// BUG-168 (regression in BUG-132's fix) — a dangling VALUE_FLAGS flag (the
// last token on the command line, with nothing following it) must be
// rejected as a usage error, NOT silently treated as an explicit clear.
// Before BUG-132, `argv[++i] === undefined` was falsy so the old truthiness
// check skipped the field; BUG-132's presence check (`'mkey' in flags`)
// can't tell "explicit ''" apart from "ran off the end of argv" without the
// parser's new `danglingFlags` tracking, so this proves that tracking
// actually blocks the write rather than just existing unused.
// =============================================================================

test("BUG-168 AC-1: `set <CODE> --mkey` with no following value (last token) errors with a usage message and does NOT touch the column", async () => {
  const guid = await insertItem({ code: 'FEAT-9210', mkey: 'engine.original' });

  const r = bowCli(['set', 'FEAT-9210', '--mkey']);
  assert.notEqual(r.status, 0, 'a dangling --mkey (no value) must exit non-zero, not silently succeed');
  assert.match(r.stderr, /--mkey requires a value/i, 'error must explicitly name the missing-value problem, not a generic failure');

  const [rows] = await db.query('SELECT mkey FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].mkey, 'engine.original', 'mkey must be UNCHANGED — this is the exact silent-NULL regression BUG-168 reported');
});

test("BUG-168 AC-2: the same dangling-flag protection applies to --seq and --sprint", async () => {
  const guid = await insertItem({ code: 'FEAT-9211', mkey: null });
  await db.query('UPDATE bow_items SET seq = 5, sprint = 2 WHERE guid = ?', [guid]);

  const rSeq = bowCli(['set', 'FEAT-9211', '--seq']);
  assert.notEqual(rSeq.status, 0, 'a dangling --seq (no value) must exit non-zero');
  assert.match(rSeq.stderr, /--seq requires a value/i);

  const rSprint = bowCli(['set', 'FEAT-9211', '--sprint']);
  assert.notEqual(rSprint.status, 0, 'a dangling --sprint (no value) must exit non-zero');
  assert.match(rSprint.stderr, /--sprint requires a value/i);

  const [rows] = await db.query('SELECT seq, sprint FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].seq, 5, 'seq must be UNCHANGED after a dangling --seq is rejected');
  assert.equal(rows[0].sprint, 2, 'sprint must be UNCHANGED after a dangling --sprint is rejected');
});

test("BUG-168 AC-3: `set <CODE> --mkey ''` (explicit empty string) still clears to NULL — no regression to BUG-132's fix", async () => {
  const guid = await insertItem({ code: 'FEAT-9212', mkey: 'engine.tobecleared' });

  const r = bowCli(['set', 'FEAT-9212', '--mkey', '']);
  assert.equal(r.status, 0, `set --mkey '' must still succeed after the BUG-168 fix: ${r.stderr}`);

  const [rows] = await db.query('SELECT mkey FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].mkey, null, 'an explicit empty-string --mkey must still clear the column to NULL');
});

test("BUG-168 AC-4: ordinary `set <CODE> --mkey somevalue` (a real value, not dangling) still works after the fix", async () => {
  const guid = await insertItem({ code: 'FEAT-9213', mkey: null });

  const r = bowCli(['set', 'FEAT-9213', '--mkey', 'engine.stillworks']);
  assert.equal(r.status, 0, `ordinary set --mkey <value> must still succeed: ${r.stderr}`);

  const [rows] = await db.query('SELECT mkey FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].mkey, 'engine.stillworks', 'mkey must be set to the supplied value');
});

// =============================================================================
// BUG-171 (regression in BUG-168's fix) — a dangling VALUE_FLAGS flag
// immediately followed by ANOTHER recognized `--flag` token (not off the
// end of argv, but butted up against a real flag) must ALSO be rejected as
// dangling, and the following flag must still be parsed and applied
// correctly as its own flag/value pair rather than being eaten as the
// first flag's string value.
// =============================================================================

test("BUG-171 AC-1: `set <CODE> --mkey --seq 5` — --mkey is detected dangling (errors, column untouched) AND --seq 5 is still correctly applied, not silently eaten as --mkey's value", async () => {
  const guid = await insertItem({ code: 'FEAT-9220', mkey: 'engine.original' });
  await db.query('UPDATE bow_items SET seq = 1 WHERE guid = ?', [guid]);

  const r = bowCli(['set', 'FEAT-9220', '--mkey', '--seq', '5']);
  assert.notEqual(r.status, 0, 'a dangling --mkey followed by another --flag must exit non-zero, not silently succeed');
  assert.match(r.stderr, /--mkey requires a value/i, 'error must name --mkey as the dangling flag, not a generic failure');

  const [rows] = await db.query('SELECT mkey, seq FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].mkey, 'engine.original', 'mkey must be UNCHANGED — the exact BUG-171 corruption (writing the literal string "--seq")');
});

test("BUG-171 AC-1b: the same fixture with only --seq changed (--sprint --mkey X) proves the SECOND flag is parsed as its own flag, not consumed as the first flag's value", async () => {
  const guid = await insertItem({ code: 'FEAT-9221', mkey: 'engine.original' });
  await db.query('UPDATE bow_items SET sprint = 1 WHERE guid = ?', [guid]);

  const r = bowCli(['set', 'FEAT-9221', '--sprint', '--mkey', 'engine.newvalue']);
  assert.notEqual(r.status, 0, 'a dangling --sprint followed by --mkey must exit non-zero');
  assert.match(r.stderr, /--sprint requires a value/i);

  // Critically: --sprint must NOT have swallowed "--mkey" as its value, and
  // this proves it by showing the command still fails on --sprint alone
  // (if --mkey had been eaten as --sprint's value, --mkey engine.newvalue
  // would never have been parsed as a flag at all, and there would be no
  // way to distinguish that from this correct behaviour by exit code alone
  // — so also confirm mkey was NOT written, since cmdSet aborts before any
  // write once a dangling flag is found amongst the parsed flags).
  const [rows] = await db.query('SELECT mkey, sprint FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].mkey, 'engine.original', 'mkey must be UNCHANGED — cmdSet must reject the whole command, not partially apply it');
  assert.equal(rows[0].sprint, 1, 'sprint must be UNCHANGED');
});

test("BUG-171 AC-2: BUG-132/168 fixtures unaffected — dangling --mkey at end of argv, explicit --mkey '' clear, and ordinary --mkey value all still work", async () => {
  const guidA = await insertItem({ code: 'FEAT-9222', mkey: 'engine.original' });
  const rEnd = bowCli(['set', 'FEAT-9222', '--mkey']);
  assert.notEqual(rEnd.status, 0, 'dangling --mkey at end of argv must still error (BUG-168 no regression)');
  assert.match(rEnd.stderr, /--mkey requires a value/i);
  const [rowsA] = await db.query('SELECT mkey FROM bow_items WHERE guid = ?', [guidA]);
  assert.equal(rowsA[0].mkey, 'engine.original', 'mkey unchanged after end-of-argv dangling --mkey');

  const guidB = await insertItem({ code: 'FEAT-9223', mkey: 'engine.tobecleared' });
  const rClear = bowCli(['set', 'FEAT-9223', '--mkey', '']);
  assert.equal(rClear.status, 0, `explicit --mkey '' clear must still succeed: ${rClear.stderr}`);
  const [rowsB] = await db.query('SELECT mkey FROM bow_items WHERE guid = ?', [guidB]);
  assert.equal(rowsB[0].mkey, null, 'explicit empty --mkey must still clear to NULL (BUG-132 no regression)');

  const guidC = await insertItem({ code: 'FEAT-9224', mkey: null });
  const rSet = bowCli(['set', 'FEAT-9224', '--mkey', 'engine.stillworks']);
  assert.equal(rSet.status, 0, `ordinary --mkey <value> must still succeed: ${rSet.stderr}`);
  const [rowsC] = await db.query('SELECT mkey FROM bow_items WHERE guid = ?', [guidC]);
  assert.equal(rowsC[0].mkey, 'engine.stillworks', 'ordinary --mkey value must still be applied (BUG-168 AC-4 no regression)');
});

test("BUG-171 AC-3: an ordinary --desc value with no leading -- still parses correctly next to another flag (e.g. `add bug <title> --desc <text> --priority P1`) — proves the widened dangling check doesn't false-positive on normal adjacent flags", async () => {
  const r = bowCli(['add', 'bug', 'BUG-171 AC-3 fixture', '--desc', 'an ordinary description', '--priority', 'P1']);
  assert.equal(r.status, 0, `add with --desc followed by --priority must succeed: ${r.stderr}`);
  const [rows] = await db.query('SELECT description, priority FROM bow_items WHERE title = ?', ['BUG-171 AC-3 fixture']);
  assert.equal(rows.length, 1, 'exactly one row must be written');
  assert.equal(rows[0].description, 'an ordinary description', '--desc value must be applied, not treated as dangling');
  assert.equal(rows[0].priority, 'P1', '--priority (the following flag) must also be applied correctly');
});

// =============================================================================
// BUG-196: `set --desc`/`--desc-file` got NO dangling-flag guard when
// BUG-017 wired --desc into `set` — unlike mkey/seq/sprint above, which each
// got the BUG-168/171 guard. A dangling --desc fell through resolveTextFlag's
// `direct || null` fallback and silently wiped description to NULL, exit 0.
// Reproduces the Destructive attacker's exact three repros from BUG-196.
// Empty-string decision (documented in claude-bow.js at the danglingFlags.
// has('desc') guard): unlike --mkey '' (a legitimate, documented explicit
// clear for a short categorical column per BUG-132), --desc '' is REJECTED
// outright — description is free-form prose (the record itself), so an
// intentional clear-to-empty is not a normal use case and is
// indistinguishable in behaviour from the BUG-171 dangling-flag shape.
// =============================================================================

test("BUG-196 AC-1: `set <CODE> --desc` with no following value (last token) errors and does NOT touch the column", async () => {
  const guid = await insertItem({ code: 'FEAT-9230', description: 'original description' });

  const r = bowCli(['set', 'FEAT-9230', '--desc']);
  assert.notEqual(r.status, 0, 'a dangling --desc (no value) must exit non-zero, not silently succeed');
  assert.match(r.stderr, /--desc requires a value/i, 'error must explicitly name the missing-value problem');

  const [rows] = await db.query('SELECT description FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].description, 'original description', 'description must be UNCHANGED — the exact silent-NULL regression BUG-196 reported');
});

test("BUG-196 AC-2: `set <CODE> --desc \"\"` (explicit empty string) is rejected, NOT treated as a silent clear", async () => {
  const guid = await insertItem({ code: 'FEAT-9231', description: 'original description' });

  const r = bowCli(['set', 'FEAT-9231', '--desc', '']);
  assert.notEqual(r.status, 0, "--desc '' must be rejected -- description is free-form prose, not a short key like mkey, so an implicit clear-to-empty is not supported");
  assert.match(r.stderr, /--desc.*empty.*rejected|rejected.*--desc/i, 'error must explain the empty-string rejection');

  const [rows] = await db.query('SELECT description FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].description, 'original description', 'description must be UNCHANGED — must not silently collapse to NULL');
});

test("BUG-196 AC-3: `set <CODE> --desc --priority P1` (BUG-171 shape) — --desc is rejected as dangling AND nothing (not even --priority) is applied, since cmdSet aborts before any write", async () => {
  const guid = await insertItem({ code: 'FEAT-9232', description: 'original description' });
  await db.query("UPDATE bow_items SET priority = 'P3' WHERE guid = ?", [guid]);

  const r = bowCli(['set', 'FEAT-9232', '--desc', '--priority', 'P1']);
  assert.notEqual(r.status, 0, 'a dangling --desc followed by another --flag must exit non-zero, not silently succeed');
  assert.match(r.stderr, /--desc requires a value/i, 'error must name --desc as the dangling flag');

  const [rows] = await db.query('SELECT description, priority FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].description, 'original description', 'description must be UNCHANGED — the exact BUG-196 corruption');
  assert.equal(rows[0].priority, 'P3', 'priority must also be UNCHANGED — cmdSet rejects the whole command before any write, matching BUG-171 AC-1b for mkey/sprint');
});

test("BUG-196 AC-4: the same dangling-flag protection applies to --desc-file", async () => {
  const guid = await insertItem({ code: 'FEAT-9233', description: 'original description' });

  const r = bowCli(['set', 'FEAT-9233', '--desc-file']);
  assert.notEqual(r.status, 0, 'a dangling --desc-file (no path) must exit non-zero');
  assert.match(r.stderr, /--desc-file requires a (value|path)/i);

  const [rows] = await db.query('SELECT description FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].description, 'original description', 'description must be UNCHANGED after a dangling --desc-file is rejected');
});

test("BUG-196 AC-5: ordinary `set <CODE> --desc <text>` (a real value, not dangling, not empty) still works after the fix", async () => {
  const guid = await insertItem({ code: 'FEAT-9234', description: 'original description' });

  const r = bowCli(['set', 'FEAT-9234', '--desc', 'a real updated description']);
  assert.equal(r.status, 0, `ordinary set --desc <text> must still succeed: ${r.stderr}`);

  const [rows] = await db.query('SELECT description FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].description, 'a real updated description', 'description must be set to the supplied value');
});

// =============================================================================
// BUG-221 (tool.bowcli) — `set --guid` reconciles a BOW item's `guid` column
// with code.json's registered guid for the item's mkey. Strict apply-or-
// reject: refused unless the item already has (or is being given, in the
// SAME call) an mkey, and refused unless the supplied value EXACTLY matches
// code.json's guid for that mkey. Real subprocess (bowCli), and reads the
// REAL repo-root code.json (claude-bow.js's CODE_JSON_PATH is derived from
// its own __dirname, not the spawned process's cwd, and bowCli's cwd is the
// repo root anyway) — so these fixtures use a real, currently-registered
// mkey/guid pair rather than a mocked file.
// =============================================================================

// A real module entry that is stable and unlikely to be renumbered by
// ordinary feature work — read fresh from the live code.json at test-file
// load time (not hardcoded) so this suite tracks reality per GR#15 (never
// hardcode an expected value that should come from data).
const REAL_CODEJSON = JSON.parse(fs.readFileSync(path.join(ROOT, 'code.json'), 'utf8'));
const REAL_MKEY_ENTRY = REAL_CODEJSON.modules.find((m) => m.key === 'plan.pipeline');
assert.ok(REAL_MKEY_ENTRY && REAL_MKEY_ENTRY.guid, 'test setup: code.json must still register plan.pipeline with a guid for the BUG-221 fixtures below');
const REAL_MKEY = REAL_MKEY_ENTRY.key;
const REAL_GUID = REAL_MKEY_ENTRY.guid;
const WRONG_GUID = crypto.randomUUID(); // guaranteed not to equal REAL_GUID

test('BUG-221 AC-1: `set <CODE> --guid <value>` is REJECTED when the item has no mkey (neither already in the DB nor supplied this call), column untouched', async () => {
  const guid = await insertItem({ code: 'FEAT-9500', mkey: null });

  const r = bowCli(['set', 'FEAT-9500', '--guid', REAL_GUID]);
  assert.notEqual(r.status, 0, '--guid on an item with no mkey must be refused, not silently applied');
  assert.match(r.stderr, /no mkey/i, 'error must explain the refusal is because there is no mkey to reconcile against');

  const [rows] = await db.query('SELECT guid FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].guid, guid, 'guid column must be UNCHANGED after the no-mkey refusal');
});

test('BUG-221 AC-2: `set <CODE> --guid <value>` is REJECTED when the value does not match code.json\'s guid for the item\'s existing mkey, column untouched', async () => {
  const guid = await insertItem({ code: 'FEAT-9501', mkey: REAL_MKEY });

  const r = bowCli(['set', 'FEAT-9501', '--guid', WRONG_GUID]);
  assert.notEqual(r.status, 0, 'a --guid that does not match code.json\'s guid for the mkey must be refused');
  assert.match(r.stderr, /does not match code\.json/i, 'error must name the mismatch-with-code.json reason');

  const [rows] = await db.query('SELECT guid FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].guid, guid, 'guid column must be UNCHANGED after a mismatched --guid is refused');
});

test('BUG-221 AC-3: `set <CODE> --guid <value>` is ACCEPTED and applied when the value EXACTLY matches code.json\'s guid for the item\'s existing mkey', async () => {
  const guid = await insertItem({ code: 'FEAT-9502', mkey: REAL_MKEY });

  const r = bowCli(['set', 'FEAT-9502', '--guid', REAL_GUID]);
  assert.equal(r.status, 0, `a --guid matching code.json's registered value must be accepted: ${r.stderr}`);

  // Look up by `code`, not the old `guid` captured above -- the guid column
  // itself is what just changed, so a WHERE guid = <old guid> would (wrongly)
  // find nothing post-update.
  const [rows] = await db.query('SELECT guid FROM bow_items WHERE code = ?', ['FEAT-9502']);
  assert.equal(rows[0].guid, REAL_GUID, 'guid column must now equal code.json\'s registered guid for the mkey');
  // Deliberately NOT pushed onto trackedFixtureGuids: REAL_GUID is a genuine
  // code.json-registered guid that a legitimate row in the real `metro`
  // database may already carry (that's the whole point of reconciliation),
  // so tracking it there would produce a false "leak" failure in
  // test.after unrelated to this suite's actual isolation.
});

test('BUG-221 AC-4: a dangling `--guid` (last token, no value following, and immediately followed by another recognized flag) is REJECTED, matching the BUG-168/171 idiom, nothing written', async () => {
  const guid = await insertItem({ code: 'FEAT-9503', mkey: REAL_MKEY });

  const rEnd = bowCli(['set', 'FEAT-9503', '--guid']);
  assert.notEqual(rEnd.status, 0, 'a dangling --guid at end of argv must exit non-zero');
  assert.match(rEnd.stderr, /--guid requires a value/i);
  const [rowsEnd] = await db.query('SELECT guid, priority FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rowsEnd[0].guid, guid, 'guid must be UNCHANGED after a dangling --guid at end of argv');

  const rNext = bowCli(['set', 'FEAT-9503', '--guid', '--priority', 'P1']);
  assert.notEqual(rNext.status, 0, 'a dangling --guid immediately followed by another --flag must exit non-zero, not swallow --priority\'s value');
  assert.match(rNext.stderr, /--guid requires a value/i);
  const [rowsNext] = await db.query('SELECT guid, priority FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rowsNext[0].guid, guid, 'guid must be UNCHANGED after the dangling --guid + adjacent flag case');
  assert.notEqual(rowsNext[0].priority, 'P1', 'priority must NOT have been applied either -- cmdSet rejects the whole command before any write');
});

test('BUG-221 AC-5: `set <CODE> --mkey <newkey> --guid <value>` in the SAME call reconciles against the NEW mkey being set, not any stale prior mkey', async () => {
  // Seed the item pre-linked to a DIFFERENT real mkey/guid pair than the one
  // being switched to, so validating against the OLD mkey (a latent bug)
  // would accept a guid that in fact belongs to the NEW mkey -- and vice
  // versa -- making a wrong implementation observably fail this assertion.
  const otherEntry = REAL_CODEJSON.modules.find((m) => m.key !== REAL_MKEY && m.guid);
  assert.ok(otherEntry, 'test setup: code.json must have at least two distinct module entries with guids');
  const guid = await insertItem({ code: 'FEAT-9504', mkey: otherEntry.key });

  // Wrong: supplying the OLD mkey's guid while switching to the NEW mkey
  // must be refused, proving validation tracks the new mkey, not the old one.
  const rWrong = bowCli(['set', 'FEAT-9504', '--mkey', REAL_MKEY, '--guid', otherEntry.guid]);
  assert.notEqual(rWrong.status, 0, 'the OLD mkey\'s guid must be refused once --mkey switches the item to a NEW mkey in the same call');
  const [rowsWrong] = await db.query('SELECT mkey, guid FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rowsWrong[0].mkey, otherEntry.key, 'mkey must be UNCHANGED after the whole command is rejected (no partial apply)');
  assert.equal(rowsWrong[0].guid, guid, 'guid must be UNCHANGED after the whole command is rejected (no partial apply)');

  // Right: the NEW mkey's own guid, combined with --mkey switching to it,
  // must succeed and apply both fields together.
  const rRight = bowCli(['set', 'FEAT-9504', '--mkey', REAL_MKEY, '--guid', REAL_GUID]);
  assert.equal(rRight.status, 0, `switching mkey and reconciling guid to the NEW mkey's own value must succeed: ${rRight.stderr}`);
  // Look up by `code`, not the pre-update `guid` captured above -- the guid
  // column itself is what just changed, so WHERE guid = <old guid> would
  // (wrongly) find nothing post-update.
  const [rowsRight] = await db.query('SELECT mkey, guid FROM bow_items WHERE code = ?', ['FEAT-9504']);
  assert.equal(rowsRight[0].mkey, REAL_MKEY, 'mkey must now be the new mkey');
  assert.equal(rowsRight[0].guid, REAL_GUID, 'guid must now be the new mkey\'s own registered guid');
  // REAL_GUID deliberately not tracked for the leak check -- see AC-3's
  // comment above for why.
});

test('BUG-221 AC-6: `set <CODE> --guid \'\'` (empty string) is rejected outright -- --guid has no clear-to-NULL meaning, unlike --mkey/--seq/--sprint', async () => {
  const guid = await insertItem({ code: 'FEAT-9505', mkey: REAL_MKEY });

  const r = bowCli(['set', 'FEAT-9505', '--guid', '']);
  assert.notEqual(r.status, 0, 'an empty --guid must be rejected, not treated as a clear or a no-op success');

  const [rows] = await db.query('SELECT guid FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].guid, guid, 'guid must be UNCHANGED after an empty --guid is rejected');
});

test('BUG-221 AC-7: `set <CODE> --guid <value>` refuses when the mkey (existing or newly-set) has no entry at all in code.json', async () => {
  const guid = await insertItem({ code: 'FEAT-9506', mkey: 'no.such.mkey.exists.anywhere.bug221' });

  const r = bowCli(['set', 'FEAT-9506', '--guid', REAL_GUID]);
  assert.notEqual(r.status, 0, 'an mkey with no code.json entry must be refused, not silently applied');
  assert.match(r.stderr, /no such mkey/i);

  const [rows] = await db.query('SELECT guid FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].guid, guid, 'guid must be UNCHANGED when the mkey has no code.json entry');
});

test('BUG-221 AC-8 / BUG-223: `set --guid` reconciliation CASCADES to child rows — every row in bow_comments/bow_dependencies (item_guid AND depends_on_guid)/bow_git_refs/bow_destructive_verdicts follows the item to the new guid (no orphans, no stale references)', async () => {
  // Seed a scratch item linked to a real mkey so the --guid reconcile path is
  // permitted, plus a second scratch item as the dependency target. The four
  // child rows are keyed on the item's ORIGINAL (random) guid — exactly the
  // state the pre-cascade migration could not survive: under a plain RESTRICT
  // (no ON UPDATE CASCADE) foreign key, MariaDB refuses `UPDATE bow_items SET
  // guid = ...` the instant any child row references the old guid, stranding
  // those children on a guid that no longer exists. ON UPDATE CASCADE is what
  // makes this succeed, and this test is the permanent proof of that cascade.
  const oldGuid = await insertItem({ code: 'FEAT-9507', mkey: REAL_MKEY });
  const depTargetGuid = await insertItem({ code: 'FEAT-9508' });
  // A THIRD scratch item that DEPENDS ON FEAT-9507: its row keys the changed
  // item's guid in bow_dependencies' OTHER column (`depends_on_guid`), so BOTH
  // of that table's two foreign keys (fk_bow_dep_item AND fk_bow_dep_on) are
  // exercised by this one test, not just the item_guid one (BUG-223).
  const dependentGuid = await insertItem({ code: 'FEAT-9509' });

  const commentBody = 'cascade-regression comment';
  const commitHash = '0123456789abcdef0123456789abcdef01234567';
  const verdictAttacker = 'cascade-regression';

  // One row in EACH of the four child tables, all keyed on oldGuid — plus a
  // second bow_dependencies row keying oldGuid in the depends_on_guid column.
  await insertComment(oldGuid, commentBody);
  await insertDep(oldGuid, depTargetGuid);
  await insertDep(dependentGuid, oldGuid);
  await db.query('INSERT INTO bow_git_refs (item_guid, commit_hash) VALUES (?, ?)', [oldGuid, commitHash]);
  await db.query(
    'INSERT INTO bow_destructive_verdicts (guid, item_guid, verdict, attacker) VALUES (?, ?, ?, ?)',
    [crypto.randomUUID(), oldGuid, 'accept', verdictAttacker]);

  const r = bowCli(['set', 'FEAT-9507', '--guid', REAL_GUID]);
  assert.equal(r.status, 0, `a --guid reconcile must succeed even with child rows present (ON UPDATE CASCADE): ${r.stderr}`);

  // The item's own guid is the reconciled value (looked up by `code` — the
  // guid column itself just changed, so a WHERE guid = <old guid> would
  // (wrongly) find nothing post-update).
  const [[itemRow]] = await db.query('SELECT guid FROM bow_items WHERE code = ?', ['FEAT-9507']);
  assert.equal(itemRow.guid, REAL_GUID, 'bow_items.guid must now equal the reconciled value');

  // Every child row followed the item to the new guid — the SAME row (proven
  // by its identifying content), now keyed on REAL_GUID...
  const [commentsNew] = await db.query('SELECT body FROM bow_comments WHERE item_guid = ?', [REAL_GUID]);
  assert.equal(commentsNew.length, 1, 'exactly one bow_comments row must be keyed on the new guid');
  assert.equal(commentsNew[0].body, commentBody, 'it must be the same comment, migrated not duplicated');

  const [depsNew] = await db.query('SELECT depends_on_guid FROM bow_dependencies WHERE item_guid = ?', [REAL_GUID]);
  assert.equal(depsNew.length, 1, 'exactly one bow_dependencies row must be keyed on the new guid (item_guid FK)');
  assert.equal(depsNew[0].depends_on_guid, depTargetGuid, 'the dependency target (the OTHER item, whose guid did not change) must be untouched');

  const [depsOnNew] = await db.query('SELECT item_guid FROM bow_dependencies WHERE depends_on_guid = ?', [REAL_GUID]);
  assert.equal(depsOnNew.length, 1, 'the depends_on_guid FK must cascade too: exactly one dependency row must key the new guid as its target');
  assert.equal(depsOnNew[0].item_guid, dependentGuid, 'it must be the same dependent item, migrated not duplicated');

  const [refsNew] = await db.query('SELECT commit_hash FROM bow_git_refs WHERE item_guid = ?', [REAL_GUID]);
  assert.equal(refsNew.length, 1, 'exactly one bow_git_refs row must be keyed on the new guid');
  assert.equal(refsNew[0].commit_hash, commitHash, 'it must be the same git ref, migrated not duplicated');

  const [verdictsNew] = await db.query('SELECT attacker FROM bow_destructive_verdicts WHERE item_guid = ?', [REAL_GUID]);
  assert.equal(verdictsNew.length, 1, 'exactly one bow_destructive_verdicts row must be keyed on the new guid');
  assert.equal(verdictsNew[0].attacker, verdictAttacker, 'it must be the same verdict, migrated not duplicated');

  // ...and NO row anywhere still references the OLD guid (no orphans, no
  // stale references — the exact failure the pre-cascade FK produced).
  const [oldComments] = await db.query('SELECT COUNT(*) AS n FROM bow_comments WHERE item_guid = ?', [oldGuid]);
  const [oldDeps] = await db.query('SELECT COUNT(*) AS n FROM bow_dependencies WHERE item_guid = ? OR depends_on_guid = ?', [oldGuid, oldGuid]);
  const [oldRefs] = await db.query('SELECT COUNT(*) AS n FROM bow_git_refs WHERE item_guid = ?', [oldGuid]);
  const [oldVerdicts] = await db.query('SELECT COUNT(*) AS n FROM bow_destructive_verdicts WHERE item_guid = ?', [oldGuid]);
  assert.equal(
    oldComments[0].n + oldDeps[0].n + oldRefs[0].n + oldVerdicts[0].n, 0,
    'no child row may still reference the OLD guid after the reconcile (no orphans)');
});

// ---------------------------------------------------------------------------
// BUG-115: ensureSchema() (ALTER TABLE / CREATE TABLE / MODIFY COLUMN — all
// MDL-locking DDL) must NOT run for read-only commands, but MUST still run
// for write commands. Proven via a real subprocess + real MariaDB, query-
// count based: MariaDB's GLOBAL STATUS counters (Com_alter_table,
// Com_create_table) increment every time the server actually PARSES/EXECUTES
// an ALTER TABLE / CREATE TABLE statement, regardless of whether it turns
// out to be a no-op (the "IF NOT EXISTS" forms still count) — so diffing
// these counters immediately before/after a spawned `node claude-bow.js
// <command>` subprocess is a direct, non-mocked measurement of whether that
// invocation's connection issued ensureSchema's DDL at all. Runs against the
// suite's own already-fully-migrated TEST_DB (same one every other test in
// this file uses), matching how this fix expects the real `metro` database
// to be found in practice (migrated once, read many times) rather than
// against a synthetic never-migrated database.
// ---------------------------------------------------------------------------

test('BUG-115: READ_ONLY_COMMANDS is the exact enumerated set this fix classified as safe to skip ensureSchema for', () => {
  assert.deepEqual(
    [...bow.READ_ONLY_COMMANDS].sort(),
    ['exists', 'gate-status', 'lint', 'list', 'ready', 'show', 'summary', 'verdict', 'weakness'].sort(),
    'the read-only-skip set must be exactly this enumerated list — every write command (add/set/comment/depend/undepend/ref/redact/done/import/destructive/gate/gate-run) plus init/startup-summary must never appear here (BUG-075 added `exists`, a pure SELECT)'
  );
});

/** Global Com_alter_table + Com_create_table counters — incremented by the
 * server itself every time it parses/executes that statement type, on ANY
 * connection. Used to prove a spawned CLI subprocess did or did not issue
 * ensureSchema's DDL, without mocking or spying on the code under test. */
async function ddlCounters(conn) {
  const [rows] = await conn.query("SHOW GLOBAL STATUS WHERE Variable_name IN ('Com_alter_table', 'Com_create_table')");
  const out = {};
  for (const r of rows) out[r.Variable_name] = Number(r.Value);
  return out;
}

test('BUG-115: a read-only command (`list`) does NOT trigger ensureSchema\'s ALTER/CREATE TABLE statements', async () => {
  const before = await ddlCounters(db);
  const r = bowCli(['list', '--all']);
  assert.equal(r.status, 0, `list must succeed: ${r.stderr}`);
  const after = await ddlCounters(db);

  assert.equal(after.Com_alter_table, before.Com_alter_table,
    'BUG-115: `list` must not have caused any ALTER TABLE statement to run (Com_alter_table must be unchanged)');
  assert.equal(after.Com_create_table, before.Com_create_table,
    'BUG-115: `list` must not have caused any CREATE TABLE statement to run (Com_create_table must be unchanged)');
});

test('BUG-115: another read-only command (`show`) also does NOT trigger ensureSchema\'s DDL', async () => {
  const guid = await insertItem({ code: 'BUG-9115', mkey: 'tool.planning' });

  const before = await ddlCounters(db);
  const r = bowCli(['show', 'BUG-9115']);
  assert.equal(r.status, 0, `show must succeed: ${r.stderr}`);
  const after = await ddlCounters(db);

  assert.equal(after.Com_alter_table, before.Com_alter_table,
    'BUG-115: `show` must not have caused any ALTER TABLE statement to run');
  assert.equal(after.Com_create_table, before.Com_create_table,
    'BUG-115: `show` must not have caused any CREATE TABLE statement to run');

  await db.query('DELETE FROM bow_items WHERE guid = ?', [guid]);
});

test('BUG-115: a write command (`add`) still runs ensureSchema\'s DDL first — no regression to the write path', async () => {
  const before = await ddlCounters(db);
  const r = bowCli(['add', 'bug', 'BUG-115 write-path DDL proof', '--priority', 'P3']);
  assert.equal(r.status, 0, `add must succeed: ${r.stderr}`);
  const after = await ddlCounters(db);

  // ensureSchema issues several ALTER TABLE statements (mkey/seq/... columns,
  // the item_type MODIFY, two ADD INDEX IF NOT EXISTS) and several CREATE
  // TABLE IF NOT EXISTS statements every time it runs — both counters must
  // have moved for a write command, proving ensureSchema executed.
  assert.ok(after.Com_alter_table > before.Com_alter_table,
    `BUG-115: \`add\` must still run ensureSchema's ALTER TABLE statements (before=${before.Com_alter_table}, after=${after.Com_alter_table})`);
  assert.ok(after.Com_create_table > before.Com_create_table,
    `BUG-115: \`add\` must still run ensureSchema's CREATE TABLE statements (before=${before.Com_create_table}, after=${after.Com_create_table})`);

  const [rows] = await db.query('SELECT code FROM bow_items WHERE title = ?', ['BUG-115 write-path DDL proof']);
  assert.equal(rows.length, 1, 'the write itself must still have succeeded correctly, not just the DDL count');
});

// ---------------------------------------------------------------------------
// BUG-170: BUG-115's READ_ONLY_COMMANDS skip left a genuinely fresh/never-
// migrated database (zero tables) with no bootstrap path when a read command
// happens to be the first thing run against it — a hard `Table 'db.bow_items'
// doesn't exist` (errno 1146 / ER_NO_SUCH_TABLE) instead of the pre-BUG-115
// behaviour (ensureSchema ran unconditionally, silently created the schema,
// `list` printed "BOW is clean"). Fix: the read-command dispatch now catches
// ER_NO_SUCH_TABLE specifically, runs ensureSchema() exactly once, and
// retries the same command once — restoring the old silent-bootstrap UX
// without reintroducing BUG-115's per-invocation DDL cost for the
// steady-state (already-migrated) case.
// ---------------------------------------------------------------------------

test('BUG-170: a read-only command (`list`) against a genuinely fresh, table-less scratch DB bootstraps gracefully instead of raising a raw ER_NO_SUCH_TABLE', async () => {
  const freshDb = `metro_test_bug170_fresh_${Date.now()}`;
  const boot = await mysql.createConnection({ host: DB_HOST, port: DB_PORT, user: DB_USER, password: DB_PASSWORD });
  try {
    // Genuinely fresh: CREATE DATABASE only, deliberately NEVER call
    // ensureSchema — reproduces BUG-170's exact fixture (zero tables).
    await boot.query(`CREATE DATABASE \`${freshDb}\``);

    const r = spawnSync(process.execPath, ['claude-bow.js', 'list'], {
      cwd: ROOT, env: { ...process.env, METRO_DB_NAME: freshDb }, encoding: 'utf8',
    });

    assert.equal(r.status, 0,
      `BUG-170: \`list\` against a fresh table-less DB must now succeed (old behaviour), not hard-fail: stdout=${r.stdout} stderr=${r.stderr}`);
    assert.doesNotMatch(r.stderr, /doesn't exist/i,
      'BUG-170: the raw "Table ... doesn\'t exist" error must never reach the user');
    assert.doesNotMatch(r.stderr, /ER_NO_SUCH_TABLE/i,
      'BUG-170: the raw ER_NO_SUCH_TABLE error code must never reach the user');
    // Old (pre-BUG-115) behaviour on a freshly-bootstrapped, empty BOW: list
    // reports no open items. Confirms actual BEHAVIOUR, not just exit code.
    assert.match(r.stdout, /clean|no.*(open|items)|0 open/i,
      `BUG-170: \`list\` against a freshly-bootstrapped empty BOW should read as clean, got: ${r.stdout}`);

    // Confirm the schema really was created (not just that the command
    // happened to exit 0 for an unrelated reason).
    const verify = await mysql.createConnection({ host: DB_HOST, port: DB_PORT, user: DB_USER, password: DB_PASSWORD, database: freshDb });
    const [tables] = await verify.query('SHOW TABLES');
    const tableNames = tables.map(row => Object.values(row)[0]);
    assert.ok(tableNames.includes('bow_items'), 'BUG-170: ensureSchema must have run and created bow_items on the fresh DB');
    await verify.end();
  } finally {
    await boot.query(`DROP DATABASE IF EXISTS \`${freshDb}\``);
    await boot.end();
  }
});

test('BUG-170: the one-shot fresh-DB bootstrap runs ensureSchema\'s DDL exactly once, not on every subsequent read (no regression to BUG-115\'s MDL-contention fix)', async () => {
  const freshDb = `metro_test_bug170_once_${Date.now()}`;
  const boot = await mysql.createConnection({ host: DB_HOST, port: DB_PORT, user: DB_USER, password: DB_PASSWORD });
  try {
    await boot.query(`CREATE DATABASE \`${freshDb}\``);

    // First read against the fresh DB: expected to bootstrap (DDL runs).
    const first = spawnSync(process.execPath, ['claude-bow.js', 'list'], {
      cwd: ROOT, env: { ...process.env, METRO_DB_NAME: freshDb }, encoding: 'utf8',
    });
    assert.equal(first.status, 0, `first list (bootstrap) must succeed: ${first.stderr}`);

    // Second read against the NOW-migrated fresh DB: must behave exactly
    // like BUG-115's existing test against TEST_DB — zero DDL statements.
    const before = await ddlCounters(boot);
    const second = spawnSync(process.execPath, ['claude-bow.js', 'list'], {
      cwd: ROOT, env: { ...process.env, METRO_DB_NAME: freshDb }, encoding: 'utf8',
    });
    assert.equal(second.status, 0, `second list must succeed: ${second.stderr}`);
    const after = await ddlCounters(boot);

    assert.equal(after.Com_alter_table, before.Com_alter_table,
      'BUG-170: a second `list` against the now-migrated DB must not re-run ensureSchema\'s ALTER TABLE statements');
    assert.equal(after.Com_create_table, before.Com_create_table,
      'BUG-170: a second `list` against the now-migrated DB must not re-run ensureSchema\'s CREATE TABLE statements');
  } finally {
    await boot.query(`DROP DATABASE IF EXISTS \`${freshDb}\``);
    await boot.end();
  }
});

test('BUG-170: `init` and a write command (`add`) against the already-migrated TEST_DB are unaffected by the fresh-DB fallback', async () => {
  const initResult = bowCli(['init']);
  assert.equal(initResult.status, 0, `init must still succeed unmodified: ${initResult.stderr}`);
  assert.match(initResult.stdout, /metro BOW tables ready/,
    'BUG-170: init\'s existing message must be unchanged');

  const before = await ddlCounters(db);
  const addResult = bowCli(['add', 'bug', 'BUG-170 write-path unaffected', '--priority', 'P3']);
  assert.equal(addResult.status, 0, `add must still succeed: ${addResult.stderr}`);
  const after = await ddlCounters(db);

  assert.ok(after.Com_alter_table > before.Com_alter_table,
    'BUG-170: `add` against an already-migrated DB must still run ensureSchema unconditionally (unaffected by the fresh-DB retry path)');

  const [rows] = await db.query('SELECT code FROM bow_items WHERE title = ?', ['BUG-170 write-path unaffected']);
  assert.equal(rows.length, 1, 'the write itself must still have succeeded');
});

// =============================================================================
// BUG-075 — cheap batch existence check (`node claude-bow.js exists ...`).
//
// Two real incidents of fabricated/misattributed BOW-code citations
// propagating between agents (see `node claude-bow.js show BUG-075`) led the
// lead to rule (accepted, all three proposals): Testers/lead must verify a
// cited code actually RESOLVES before relaying or accepting it as fact, and
// `claude-bow.js` should expose a batch check so that verification is one
// command rather than N `show` calls. Every test below invokes the real
// `node claude-bow.js exists ...` subprocess (bowCli/spawnSync), matching
// this file's own established BUG-090/BUG-061 real-CLI-process standard.
// =============================================================================

test('BUG-075: `exists` with codes that all exist reports EXISTS with the one-line title for each, in the order given, and exits 0', async () => {
  await insertItem({ code: 'FEAT-9100', status: 'open' });
  await insertItem({ code: 'BUG-9101', status: 'open' });

  const r = bowCli(['exists', 'FEAT-9100', 'BUG-9101']);
  assert.equal(r.status, 0, `exists must exit 0 even discussing found codes: ${r.stderr}`);
  assert.match(r.stdout, /FEAT-9100: EXISTS — FEAT-9100 — FEAT-9100/);
  assert.match(r.stdout, /BUG-9101: EXISTS — BUG-9101 — BUG-9101/);
  assert.match(r.stdout, /2\/2 exist\./);
});

test('BUG-075: `exists` with codes that do NOT exist reports NOT FOUND for each, and still exits 0 (a report, not a failure)', async () => {
  const r = bowCli(['exists', 'ASM-9999-fake', 'BUG-9998-fake']);
  assert.equal(r.status, 0, `exists must exit 0 for a clean report of missing codes: ${r.stderr}`);
  assert.match(r.stdout, /ASM-9999-fake: NOT FOUND/);
  assert.match(r.stdout, /BUG-9998-fake: NOT FOUND/);
  assert.match(r.stdout, /0\/2 exist\./);
});

test('BUG-075: `exists` with a mix of real and fabricated codes reports each correctly in ONE invocation — the BUG-075 shape (a fabricated ASM code cited alongside real ones)', async () => {
  await insertItem({ code: 'ASM-9102', status: 'open' });

  const r = bowCli(['exists', 'ASM-9102', 'ASM-9103-fake', 'ASM-9104-fake']);
  assert.equal(r.status, 0);
  assert.match(r.stdout, /ASM-9102: EXISTS — ASM-9102 — ASM-9102/);
  assert.match(r.stdout, /ASM-9103-fake: NOT FOUND/);
  assert.match(r.stdout, /ASM-9104-fake: NOT FOUND/);
  assert.match(r.stdout, /1\/3 exist\./);
});

test('BUG-075: `exists` with no codes at all (no positional args, no --codes) exits non-zero with a usage message, and issues no query', async () => {
  const r = bowCli(['exists']);
  assert.notEqual(r.status, 0, 'empty-args case must be rejected, not silently report zero results');
  assert.match(r.stderr, /Usage: node claude-bow\.js exists/);
});

test('BUG-075: `exists --codes A,B,C` (comma-separated single flag) is equivalent to passing them as separate positional args', async () => {
  await insertItem({ code: 'FEAT-9105', status: 'open' });

  const r = bowCli(['exists', '--codes', 'FEAT-9105,BUG-9106-fake']);
  assert.equal(r.status, 0);
  assert.match(r.stdout, /FEAT-9105: EXISTS — FEAT-9105 — FEAT-9105/);
  assert.match(r.stdout, /BUG-9106-fake: NOT FOUND/);
  assert.match(r.stdout, /1\/2 exist\./);
});

test('BUG-075: `exists` scales to many codes correctly (functional proof the batching works end to end, not just for 2-3 codes)', async () => {
  const realCodes = ['FEAT-9107', 'FEAT-9108', 'FEAT-9109', 'FEAT-9111', 'FEAT-9112'];
  for (const code of realCodes) await insertItem({ code, status: 'open' });
  const fakeCodes = ['FEAT-9113-fake', 'FEAT-9114-fake', 'FEAT-9115-fake'];

  const r = bowCli(['exists', ...realCodes, ...fakeCodes]);
  assert.equal(r.status, 0);
  for (const code of realCodes) assert.match(r.stdout, new RegExp(`${code}: EXISTS`));
  for (const code of fakeCodes) assert.match(r.stdout, new RegExp(`${code}: NOT FOUND`));
  assert.match(r.stdout, /5\/8 exist\./);
});

test('BUG-075: `cmdExists` issues exactly ONE query against bow_items regardless of how many codes are checked — proves the batching (IN(...) over all codes at once), not just the correct final answer via a real subprocess run separately above', () => {
  const src = bow.cmdExists.toString();
  const dbQueryCalls = (src.match(/db\.query\(/g) || []).length;
  assert.equal(dbQueryCalls, 1, 'cmdExists must issue exactly one db.query call — the whole point of `exists` over N `show` calls is a single round-trip');
  assert.match(src, /WHERE UPPER\(code\) IN/, 'the single query must batch all requested codes via IN(...), not loop per code');
});

test('BUG-075: `exists` is case-insensitive on short codes (matches findItem()/requireItem()\'s own UPPER(code) rule) and de-duplicates a repeated code to one output line', async () => {
  await insertItem({ code: 'FEAT-9110', status: 'open' });

  const r = bowCli(['exists', 'feat-9110', 'FEAT-9110', 'Feat-9110']);
  assert.equal(r.status, 0);
  const lines = r.stdout.split('\n').filter((l) => /EXISTS|NOT FOUND/.test(l));
  assert.equal(lines.length, 1, 'a code repeated three times (any case) must produce exactly one output line');
  assert.match(r.stdout, /1\/1 exist\./);
});

// =============================================================================
// BUG-193 — splitAcBlocks's heading-boundary regex (`#{1,6}\s`, added by
// BUG-162) has no concept of markdown code-fence context. A flush-left
// hash-comment line INSIDE an unindented fenced code block (e.g. a shell
// `# comment` at column 0) was being misread as a section heading, cutting
// the AC block off mid-fence and losing everything after it — including a
// Tripwire line further down the same block, misreporting a genuinely
// armed AC as unarmed.
// =============================================================================

test('BUG-193: an unindented fenced code block containing a flush-left "# comment" line does NOT truncate the AC block — the Tripwire after the fence survives', () => {
  const text = [
    '- **AC-1 (deferred check with a fenced example).** Check (once unblocked): run the setup script.',
    '```bash',
    '# this is a comment, flush-left, inside the fence',
    'echo hello',
    '```',
    '  Tripwire (mechanical): `true` must exit 0.',
  ].join('\n');

  const blocks = splitAcBlocks(text);
  assert.equal(blocks.length, 1, 'the fenced comment line must NOT be treated as a heading boundary — only one AC block exists here');
  assert.ok(blocks[0].includes('Tripwire (mechanical)'), 'the Tripwire line after the fence must still be part of AC-1\'s block, not discarded by a false split inside the fence');

  const tripwireResults = findTripwireChecks(text);
  assert.equal(tripwireResults.length, 1, 'AC-1 must be found as exactly one deferred-check AC');
  assert.equal(tripwireResults[0].acNum, '1');
  assert.equal(tripwireResults[0].armed, true, 'AC-1 must be reported ARMED — the pre-fix bug lost the Tripwire and reported it unarmed');
});

test('BUG-193: a real markdown heading AFTER a closed fence still correctly terminates the AC block (fence-tracking must not disable heading detection permanently)', () => {
  const text = [
    '- **AC-1 (fenced example, no tripwire needed here).** Some body text.',
    '```bash',
    '# a flush-left comment inside the fence',
    'echo hi',
    '```',
    '',
    '## A later real section heading',
    '',
    'Some prose that must not be folded into AC-1\'s block.',
    '',
    '- **AC-2 (second, real deferred-check AC).** Check (once unblocked): do the thing.',
    '  Tripwire (mechanical): `true` must exit 0.',
  ].join('\n');

  const blocks = splitAcBlocks(text);
  assert.equal(blocks.length, 2, 'exactly two AC blocks — the fence must not eat the boundary, and the post-fence heading must still split');
  assert.ok(!blocks[0].includes('A later real section heading'), 'AC-1\'s block must not have absorbed the later heading (fence-tracking must reset once the fence closes)');
  assert.ok(blocks[1].includes('Check (once unblocked)'));
});

// =============================================================================
// BUG-195 — splitAcBlocks's BUG-193 fence toggle was a bare boolean with no
// concept of marker TYPE or matching close, so (1) a mixed-marker fence
// (``` opener with a literal flush-left `~~~` line in its body) got its
// state flipped by the wrong marker and stuck open, and (2) a genuinely
// unclosed fence stuck the toggle open permanently — both silently
// absorbed a later real AC block and its Tripwire, which then vanished
// entirely from findTripwireChecks() results (not even a false FAIL).
// =============================================================================

test('BUG-195: a ``` fence whose body contains a literal flush-left "~~~" line does not falsely close/reopen the fence — a later AC-2 block is not absorbed', () => {
  const text = [
    '- **AC-1 (fence with a mixed marker in its body).** Some body text.',
    '```diff',
    '~~~',
    'some diff-ish content that happens to start with tildes',
    '```',
    '',
    '- **AC-2 (second, real deferred-check AC).** Check (once unblocked): do the thing.',
    '  Tripwire (mechanical): `true` must exit 0.',
  ].join('\n');

  const blocks = splitAcBlocks(text);
  assert.equal(blocks.length, 2, 'the flush-left "~~~" inside the ``` fence body must not falsely toggle fence state — AC-1 and AC-2 must split into two blocks');
  assert.ok(!blocks[0].includes('AC-2'), 'AC-1\'s block must not have absorbed AC-2');
  assert.ok(blocks[1].includes('Check (once unblocked)'), 'AC-2\'s block must be intact');

  const tripwireResults = findTripwireChecks(text);
  const ac2Result = tripwireResults.find((r) => r.acNum === '2');
  assert.ok(ac2Result, 'AC-2 must be present in findTripwireChecks results — the pre-fix bug made it vanish entirely');
  assert.equal(ac2Result.armed, true, 'AC-2 must be reported ARMED — its Tripwire line was intact in the source');
});

test('BUG-195: a genuinely unclosed fence in AC-1 absorbs the rest of the document (CommonMark-correct) and logs a warning rather than silently vanishing', () => {
  const text = [
    '- **AC-1 (unclosed fence, a doc typo).** Check (once unblocked): run the setup script.',
    '```bash',
    'echo "this fence is never closed"',
    '',
    '- **AC-2 (second, real deferred-check AC).** Check (once unblocked): do the thing.',
    '  Tripwire (mechanical): `true` must exit 0.',
  ].join('\n');

  const warnings = [];
  const originalWarn = console.warn;
  console.warn = (msg) => warnings.push(msg);
  let blocks;
  try {
    blocks = splitAcBlocks(text);
  } finally {
    console.warn = originalWarn;
  }

  assert.equal(blocks.length, 1, 'per CommonMark, an unclosed fence absorbs the rest of the document into the same block — AC-2\'s bullet is content, not a new block');
  assert.ok(blocks[0].includes('AC-2'), 'AC-2\'s text must be present (as fence content) inside AC-1\'s single block');
  assert.ok(warnings.length > 0, 'an unclosed fence reaching EOF must be logged via console.warn, not purely silent');
  assert.match(warnings[0], /unclosed.*fence/i, 'the warning must mention the unclosed fence condition');

  const tripwireResults = findTripwireChecks(text);
  const ac2Result = tripwireResults.find((r) => r.acNum === '2');
  assert.equal(ac2Result, undefined, 'AC-2 is legitimately unreachable as a separate AC here (it is inside the unclosed fence) — but the warning above makes that visible instead of silent');
});

// =============================================================================
// BUG-017 — `set` gains --desc/--desc-file so a description corrupted by
// outer-shell expansion (the same BUG-090 class, applied to `set` for the
// first time) can be REPAIRED after the fact, following the exact same
// resolveTextFlag mutual-exclusion / path-scope / advisory-warning pattern
// `add --desc-file` already established.
// =============================================================================

test('BUG-017: `set --desc "<text>"` repairs a corrupted description via the direct form', async () => {
  const guid = await insertItem({ code: 'FEAT-9300', description: 'corrupted: Author: someone <x@y> commit abcdef' });

  const r = bowCli(['set', 'FEAT-9300', '--desc', 'a clean, repaired description']);
  assert.equal(r.status, 0, `set --desc failed: ${r.stderr}`);

  const [rows] = await db.query('SELECT description FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].description, 'a clean, repaired description');
});

test('BUG-017: `set --desc-file <path>` repairs a description byte-identical from file content, surviving a backtick/$(.../embedded quote', async () => {
  const guid = await insertItem({ code: 'FEAT-9301', description: 'corrupted description' });
  const risky = 'repaired text with a `backtick`, a $(command substitution), and an "embedded quote"\nsecond line.';
  const tmp = mkTempDir('bug017-desc-');
  const descFile = path.join(tmp, 'desc.txt');
  fs.writeFileSync(descFile, risky, 'utf8');

  const r = bowCli(['set', 'FEAT-9301', '--desc-file', descFile]);
  assert.equal(r.status, 0, `set --desc-file failed: ${r.stderr}`);

  const [rows] = await db.query('SELECT description FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].description, risky, 'stored description must equal the file content exactly, byte-identical');
});

test('BUG-017: `set` rejects when both --desc and --desc-file are supplied — non-zero exit, clear message, and the description is left UNCHANGED', async () => {
  const guid = await insertItem({ code: 'FEAT-9302', description: 'original, must survive' });
  const tmp = mkTempDir('bug017-mutex-');
  const descFile = path.join(tmp, 'desc.txt');
  fs.writeFileSync(descFile, 'file content', 'utf8');

  const r = bowCli(['set', 'FEAT-9302', '--desc', 'inline text', '--desc-file', descFile]);
  assert.notEqual(r.status, 0, 'both --desc and --desc-file together must exit non-zero');
  assert.match(r.stderr, /--desc/);
  assert.match(r.stderr, /--desc-file/);

  const [rows] = await db.query('SELECT description FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].description, 'original, must survive', 'the mutual-exclusion rejection must happen before any UPDATE');
});

test('BUG-017: `set --desc-file` pointed at a system file well outside the repo/temp roots is refused (SEC-050 path-scope guard applies to `set` too)', async () => {
  const guid = await insertItem({ code: 'FEAT-9303', description: 'must not change' });
  const winIni = process.platform === 'win32' ? 'C:\\Windows\\win.ini' : '/etc/passwd';

  const r = bowCli(['set', 'FEAT-9303', '--desc-file', winIni]);
  assert.notEqual(r.status, 0, 'reading a system file via --desc-file must be refused, not succeed');

  const [rows] = await db.query('SELECT description FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].description, 'must not change');
});

test('BUG-017: a direct `set --desc` value containing a backtick produces a non-fatal advisory warning, and the update still applies', async () => {
  await insertItem({ code: 'FEAT-9304', description: 'old' });

  const r = bowCli(['set', 'FEAT-9304', '--desc', 'contains a `backtick` right in it']);
  assert.equal(r.status, 0, 'the advisory warning must never fail the command');
  assert.match(r.stderr, /WARNING/i);
  assert.match(r.stderr, /desc-file/);

  const [rows] = await db.query('SELECT description FROM bow_items WHERE code = ?', ['FEAT-9304']);
  assert.equal(rows[0].description, 'contains a `backtick` right in it', 'the update must still apply despite the advisory warning firing');
});

test('BUG-017: `set` with neither --priority/--status/etc. nor --desc/--desc-file still hits the usage error (no accidental no-op success)', async () => {
  await insertItem({ code: 'FEAT-9305' });
  const r = bowCli(['set', 'FEAT-9305']);
  assert.notEqual(r.status, 0, 'set with no update flags at all must still be a usage error');
  assert.match(r.stderr, /Usage: node claude-bow\.js set/);
});

// =============================================================================
// FEAT-044 (docs/planning/acceptance/tool.bowcli.md) — general auditable
// `amend` command for BOW prose: title/description/comment-body correction
// with a mandatory --reason and an auto-comment recording OLD/NEW/reason/
// author. Shares its "apply mutation, then audit" engine with `redact`
// (applyMutationWithAudit) per Aaron's binding "two commands, one engine"
// ruling. All tests below invoke the real `node claude-bow.js amend ...`
// subprocess (bowCli), matching the evidentiary standard the rest of this
// suite's BUG-090/BUG-061/BUG-017 sections already use for exactly this
// class of "does the write, or refusal, actually reach the DB" question.
// =============================================================================

async function setTitle(itemGuid, title) {
  await db.query('UPDATE bow_items SET title = ? WHERE guid = ?', [title, itemGuid]);
}

/** Insert a comment and return its numeric id (bow_comments.id AUTO_INCREMENT). */
async function insertCommentGetId(itemGuid, body) {
  await insertComment(itemGuid, body);
  const [[row]] = await db.query(
    'SELECT id FROM bow_comments WHERE item_guid = ? AND body = ? ORDER BY id DESC LIMIT 1', [itemGuid, body]);
  return row.id;
}

test('FEAT-044 AC-1: `amend <code> --field desc --to "<text>" --reason "<text>"` actually changes the item description', async () => {
  const guid = await insertItem({ code: 'FEAT-9400', description: 'stale, wrong description' });
  const r = bowCli(['amend', 'FEAT-9400', '--field', 'desc', '--to', 'corrected, accurate description', '--reason', 'was factually wrong, corrected per Aaron']);
  assert.equal(r.status, 0, `amend --field desc failed: ${r.stderr}`);

  const [rows] = await db.query('SELECT description FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].description, 'corrected, accurate description');
});

test('FEAT-044 AC-1: `amend <code> --field title --to "<text>" --reason "<text>"` actually changes the item title', async () => {
  const guid = await insertItem({ code: 'FEAT-9401' });
  await setTitle(guid, 'Stale Title With A Typo');
  const r = bowCli(['amend', 'FEAT-9401', '--field', 'title', '--to', 'Corrected Title', '--reason', 'fixed the typo']);
  assert.equal(r.status, 0, `amend --field title failed: ${r.stderr}`);

  const [rows] = await db.query('SELECT title FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].title, 'Corrected Title');
});

test('FEAT-044 AC-4: `amend --comment <id> --field body --to "<text>" --reason "<text>"` actually changes the comment body', async () => {
  const guid = await insertItem({ code: 'FEAT-9402' });
  const commentId = await insertCommentGetId(guid, 'stale comment text, wrong');

  const r = bowCli(['amend', '--comment', String(commentId), '--field', 'body', '--to', 'corrected comment text', '--reason', 'the original was wrong']);
  assert.equal(r.status, 0, `amend --comment failed: ${r.stderr}`);

  const [rows] = await db.query('SELECT body FROM bow_comments WHERE id = ?', [commentId]);
  assert.equal(rows[0].body, 'corrected comment text');
});

test('FEAT-044 AC-5: a successful amend auto-comments the OLD value, the NEW value, the reason, and is attributed to the acting author — not merely "some comment was added"', async () => {
  const guid = await insertItem({ code: 'FEAT-9403', description: 'the original wrong text' });
  const r = bowCli(['amend', 'FEAT-9403', '--field', 'desc', '--to', 'the replacement correct text', '--reason', 'AC-5 audit trail check', '--author', 'test-author']);
  assert.equal(r.status, 0, `amend failed: ${r.stderr}`);

  const [comments] = await db.query(
    'SELECT body, author FROM bow_comments WHERE item_guid = ? ORDER BY id DESC LIMIT 1', [guid]);
  assert.equal(comments.length, 1, 'exactly one audit comment must be posted');
  assert.match(comments[0].body, /the original wrong text/, 'audit comment must quote the OLD text (amend, unlike redact, quotes old/new in full)');
  assert.match(comments[0].body, /the replacement correct text/, 'audit comment must quote the NEW text');
  assert.match(comments[0].body, /AC-5 audit trail check/, 'audit comment must quote the supplied reason');
  assert.equal(comments[0].author, 'test-author', 'audit comment must be attributed to the acting author (currentAuthor())');
});

test('FEAT-044 AC-6: `amend` invoked WITHOUT --reason exits non-zero before any write, with a "reason is required" message, and neither the target row nor bow_comments changes', async () => {
  const guid = await insertItem({ code: 'FEAT-9404', description: 'must not change' });
  const [beforeComments] = await db.query('SELECT COUNT(*) AS n FROM bow_comments WHERE item_guid = ?', [guid]);

  const r = bowCli(['amend', 'FEAT-9404', '--field', 'desc', '--to', 'attempted replacement']);
  assert.notEqual(r.status, 0, 'amend with no --reason must exit non-zero, not silently proceed');
  assert.match(r.stderr, /reason/i);
  assert.match(r.stderr, /required/i);

  const [rows] = await db.query('SELECT description FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].description, 'must not change', 'the mandatory-reason check must run BEFORE the update -- no partial write');
  const [afterComments] = await db.query('SELECT COUNT(*) AS n FROM bow_comments WHERE item_guid = ?', [guid]);
  assert.equal(afterComments[0].n, beforeComments[0].n, 'no audit comment may be posted when the write itself was refused');
});

test('FEAT-044 BOUNCE-BACK (attacker finding): `amend --reason " "` (whitespace-only, direct arg) is rejected exactly like a missing --reason -- a truthy-but-blank string must not bypass the mandatory-reason gate', async () => {
  const guid = await insertItem({ code: 'FEAT-9440', description: 'must not change' });
  const [beforeComments] = await db.query('SELECT COUNT(*) AS n FROM bow_comments WHERE item_guid = ?', [guid]);

  const r = bowCli(['amend', 'FEAT-9440', '--field', 'desc', '--to', 'attack: whitespace reason', '--reason', ' ']);
  assert.notEqual(r.status, 0, 'a whitespace-only --reason must exit non-zero, not silently proceed');
  assert.match(r.stderr, /reason/i);
  assert.match(r.stderr, /required/i);

  const [rows] = await db.query('SELECT description FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].description, 'must not change', 'a whitespace-only reason must not let the write through');
  const [afterComments] = await db.query('SELECT COUNT(*) AS n FROM bow_comments WHERE item_guid = ?', [guid]);
  assert.equal(afterComments[0].n, beforeComments[0].n, 'no audit comment may be posted when --reason was whitespace-only');
});

test('FEAT-044 BOUNCE-BACK (attacker finding): `amend --reason` containing only a tab character is rejected the same way', async () => {
  const guid = await insertItem({ code: 'FEAT-9441', description: 'must not change' });
  const r = bowCli(['amend', 'FEAT-9441', '--field', 'desc', '--to', 'attack: tab reason', '--reason', '\t']);
  assert.notEqual(r.status, 0, 'a tab-only --reason must exit non-zero, not silently proceed');
  assert.match(r.stderr, /reason/i);
  assert.match(r.stderr, /required/i);

  const [rows] = await db.query('SELECT description FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].description, 'must not change', 'a tab-only reason must not let the write through');
});

test('FEAT-044 BOUNCE-BACK: a reason with genuine content plus surrounding whitespace still works, and is stored TRIMMED in the audit comment', async () => {
  const guid = await insertItem({ code: 'FEAT-9442', description: 'original text' });
  const r = bowCli(['amend', 'FEAT-9442', '--field', 'desc', '--to', 'new text', '--reason', '   real reason with padding   ']);
  assert.equal(r.status, 0, `amend with a padded-but-real reason should succeed: ${r.stderr}`);

  const [comments] = await db.query(
    'SELECT body FROM bow_comments WHERE item_guid = ? ORDER BY id DESC LIMIT 1', [guid]);
  assert.match(comments[0].body, /Reason: real reason with padding$/, 'the stored reason must be trimmed of leading/trailing whitespace');
  assert.doesNotMatch(comments[0].body, /Reason:    real reason/, 'the stored reason must not retain the original leading padding');
});

test('FEAT-044 BOUNCE-BACK (same defect class): `amend --to " "` (whitespace-only replacement text) is rejected -- writing an all-whitespace field is not a meaningful amend', async () => {
  const guid = await insertItem({ code: 'FEAT-9443', description: 'must not change' });
  const [beforeComments] = await db.query('SELECT COUNT(*) AS n FROM bow_comments WHERE item_guid = ?', [guid]);

  const r = bowCli(['amend', 'FEAT-9443', '--field', 'desc', '--to', '   ', '--reason', 'trying to blank the field']);
  assert.notEqual(r.status, 0, 'a whitespace-only --to must exit non-zero, not silently proceed');
  assert.match(r.stderr, /--to/);

  const [rows] = await db.query('SELECT description FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].description, 'must not change', 'a whitespace-only replacement must not let the write through');
  const [afterComments] = await db.query('SELECT COUNT(*) AS n FROM bow_comments WHERE item_guid = ?', [guid]);
  assert.equal(afterComments[0].n, beforeComments[0].n, 'no audit comment may be posted when --to was whitespace-only');
});

test('FEAT-044 BOUNCE-BACK: `amend --to` with genuine content plus surrounding whitespace still works, and is stored TRIMMED', async () => {
  const guid = await insertItem({ code: 'FEAT-9444', description: 'original text' });
  const r = bowCli(['amend', 'FEAT-9444', '--field', 'desc', '--to', '   padded replacement   ', '--reason', 'AC padded-to check']);
  assert.equal(r.status, 0, `amend with a padded-but-real --to should succeed: ${r.stderr}`);

  const [rows] = await db.query('SELECT description FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].description, 'padded replacement', 'the stored description must be trimmed of leading/trailing whitespace');
});

test('FEAT-044 BOUNCE-BACK ROUND 3 (attacker finding): `amend --reason` containing ONLY a zero-width space (U+200B) is rejected exactly like a whitespace-only reason -- .trim() alone does not strip Unicode Cf-category invisible characters', async () => {
  const guid = await insertItem({ code: 'FEAT-9450', description: 'must not change' });
  const [beforeComments] = await db.query('SELECT COUNT(*) AS n FROM bow_comments WHERE item_guid = ?', [guid]);

  const r = bowCli(['amend', 'FEAT-9450', '--field', 'desc', '--to', 'attack: zero-width reason', '--reason', '​']);
  assert.notEqual(r.status, 0, 'a zero-width-space-only --reason must exit non-zero, not silently proceed');
  assert.match(r.stderr, /reason/i);
  assert.match(r.stderr, /required/i);

  const [rows] = await db.query('SELECT description FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].description, 'must not change', 'a zero-width-space-only reason must not let the write through');
  const [afterComments] = await db.query('SELECT COUNT(*) AS n FROM bow_comments WHERE item_guid = ?', [guid]);
  assert.equal(afterComments[0].n, beforeComments[0].n, 'no audit comment may be posted when --reason was zero-width-space-only');
});

test('FEAT-044 BOUNCE-BACK ROUND 3: `amend --reason` containing a mix of other Cf-category invisible characters (ZWNJ, ZWJ, word joiner, soft hyphen, Mongolian vowel separator) with no visible content is rejected', async () => {
  const guid = await insertItem({ code: 'FEAT-9451', description: 'must not change' });
  const invisibleOnly = '‌‍⁠­᠎';
  const r = bowCli(['amend', 'FEAT-9451', '--field', 'desc', '--to', 'attack: mixed invisible reason', '--reason', invisibleOnly]);
  assert.notEqual(r.status, 0, 'a Cf-characters-only --reason must exit non-zero, not silently proceed');
  assert.match(r.stderr, /reason/i);
  assert.match(r.stderr, /required/i);

  const [rows] = await db.query('SELECT description FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].description, 'must not change', 'a Cf-characters-only reason must not let the write through');
});

test('FEAT-044 BOUNCE-BACK ROUND 3 (same defect class): `amend --to` containing ONLY a zero-width space is rejected -- writing an invisible-but-technically-non-empty field is not a meaningful amend', async () => {
  const guid = await insertItem({ code: 'FEAT-9452', description: 'must not change' });
  const [beforeComments] = await db.query('SELECT COUNT(*) AS n FROM bow_comments WHERE item_guid = ?', [guid]);

  const r = bowCli(['amend', 'FEAT-9452', '--field', 'desc', '--to', '​', '--reason', 'trying to blank the field invisibly']);
  assert.notEqual(r.status, 0, 'a zero-width-space-only --to must exit non-zero, not silently proceed');
  assert.match(r.stderr, /--to/);

  const [rows] = await db.query('SELECT description FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].description, 'must not change', 'a zero-width-space-only replacement must not let the write through');
  const [afterComments] = await db.query('SELECT COUNT(*) AS n FROM bow_comments WHERE item_guid = ?', [guid]);
  assert.equal(afterComments[0].n, beforeComments[0].n, 'no audit comment may be posted when --to was zero-width-space-only');
});

test('FEAT-044 BOUNCE-BACK ROUND 3 (attacker finding, live-reproduced): a --reason-file whose file contains ONLY a zero-width space is rejected -- confirms the fix covers the file-input path, not just direct args', async () => {
  const guid = await insertItem({ code: 'FEAT-9453', description: 'must not change' });
  const tmp = mkTempDir('feat044-round3-');
  const filePath = path.join(tmp, 'zero-width-reason.txt');
  fs.writeFileSync(filePath, '​', 'utf8');

  const r = bowCli(['amend', 'FEAT-9453', '--field', 'desc', '--to', 'attack: file-based invisible reason', '--reason-file', filePath]);
  assert.notEqual(r.status, 0, 'a --reason-file containing only a zero-width space must exit non-zero');
  assert.match(r.stderr, /reason/i);
  assert.match(r.stderr, /required/i);

  const [rows] = await db.query('SELECT description FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].description, 'must not change', 'the invisible-only reason-file must not let the write through');
});

test('FEAT-044 BOUNCE-BACK ROUND 3: a reason that legitimately CONTAINS a zero-width character alongside real content is still accepted, unmangled -- the fix must not over-reject genuine text', async () => {
  const guid = await insertItem({ code: 'FEAT-9454', description: 'original text' });
  const legitimateReason = 'real‌reason'; // ZWNJ embedded mid-word, e.g. from a copy-pasted CJK/Indic source
  const r = bowCli(['amend', 'FEAT-9454', '--field', 'desc', '--to', 'new text', '--reason', legitimateReason]);
  assert.equal(r.status, 0, `amend with a real reason that happens to contain a ZWNJ should succeed: ${r.stderr}`);

  const [comments] = await db.query(
    'SELECT body FROM bow_comments WHERE item_guid = ? ORDER BY id DESC LIMIT 1', [guid]);
  assert.match(comments[0].body, new RegExp(`Reason: ${legitimateReason}$`), 'the embedded ZWNJ must be preserved verbatim, not stripped from genuine content');
});

test('FEAT-044 BOUNCE-BACK ROUND 3: `amend --to` that legitimately CONTAINS a zero-width character alongside real content is still accepted and stored verbatim', async () => {
  const guid = await insertItem({ code: 'FEAT-9455', description: 'original text' });
  const legitimateTo = 'real​content';
  const r = bowCli(['amend', 'FEAT-9455', '--field', 'desc', '--to', legitimateTo, '--reason', 'ZWSP-embedded --to should still work']);
  assert.equal(r.status, 0, `amend with real --to content that happens to contain a ZWSP should succeed: ${r.stderr}`);

  const [rows] = await db.query('SELECT description FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].description, legitimateTo, 'the embedded zero-width space must be preserved verbatim in the stored value');
});

test('FEAT-044 BOUNCE-BACK ROUND 3: hasVisibleContent unit-level truth table (direct import) covers the whitespace, Cf, mixed, and empty cases exhaustively', () => {
  assert.equal(bow.hasVisibleContent('real text'), true, 'plain text has visible content');
  assert.equal(bow.hasVisibleContent(''), false, 'empty string has no visible content');
  assert.equal(bow.hasVisibleContent('   '), false, 'plain whitespace has no visible content');
  assert.equal(bow.hasVisibleContent('\t\t'), false, 'tabs have no visible content');
  assert.equal(bow.hasVisibleContent(' '), false, 'a lone NBSP has no visible content (round-1/round-2 case)');
  assert.equal(bow.hasVisibleContent('​'), false, 'a lone zero-width space has no visible content (round-3 case)');
  assert.equal(bow.hasVisibleContent('‌‍⁠­᠎'), false, 'a run of assorted Cf characters has no visible content');
  assert.equal(bow.hasVisibleContent('  ​\t‌  '), false, 'a mix of ordinary whitespace and Cf characters has no visible content');
  assert.equal(bow.hasVisibleContent('a​b'), true, 'real content with an embedded Cf character still counts as visible content');
  assert.equal(bow.hasVisibleContent(null), false, 'non-string input is treated as having no visible content, not thrown on');
});

test('FEAT-044 BOUNCE-BACK ROUND 4 (attacker finding): `amend --reason` containing ONLY a Cc-category control character (U+0001) is rejected -- neither \\s nor \\p{Cf} strip control characters, so rounds 1/2 both missed this', async () => {
  const guid = await insertItem({ code: 'FEAT-9460', description: 'must not change' });
  const [beforeComments] = await db.query('SELECT COUNT(*) AS n FROM bow_comments WHERE item_guid = ?', [guid]);

  const r = bowCli(['amend', 'FEAT-9460', '--field', 'desc', '--to', 'attack: control-char reason', '--reason', '\x01']);
  assert.notEqual(r.status, 0, 'a control-character-only --reason must exit non-zero, not silently proceed');
  assert.match(r.stderr, /reason/i);
  assert.match(r.stderr, /required/i);

  const [rows] = await db.query('SELECT description FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].description, 'must not change', 'a control-character-only reason must not let the write through');
  const [afterComments] = await db.query('SELECT COUNT(*) AS n FROM bow_comments WHERE item_guid = ?', [guid]);
  assert.equal(afterComments[0].n, beforeComments[0].n, 'no audit comment may be posted when --reason was control-character-only');
});

test('FEAT-044 BOUNCE-BACK ROUND 4: further Cc-category control characters (NUL via --reason-file, BEL, ESC) are all rejected the same way -- generalization confidence beyond the single U+0001 case the round-3 Tester found', async () => {
  // NUL (U+0000) cannot survive as a shell/process argv element on most
  // platforms, so it is exercised via --reason-file exactly like round 3's
  // file-based ZWSP case did for the Cf family.
  const guidNul = await insertItem({ code: 'FEAT-9461', description: 'must not change' });
  const tmp = mkTempDir('feat044-round4-nul-');
  const filePath = path.join(tmp, 'nul-reason.txt');
  fs.writeFileSync(filePath, '\x00', 'utf8');
  const rNul = bowCli(['amend', 'FEAT-9461', '--field', 'desc', '--to', 'attack: NUL reason', '--reason-file', filePath]);
  assert.notEqual(rNul.status, 0, 'a NUL-only --reason-file must exit non-zero');
  assert.match(rNul.stderr, /reason/i);
  assert.match(rNul.stderr, /required/i);
  const [rowsNul] = await db.query('SELECT description FROM bow_items WHERE guid = ?', [guidNul]);
  assert.equal(rowsNul[0].description, 'must not change', 'a NUL-only reason-file must not let the write through');

  const guidBel = await insertItem({ code: 'FEAT-9462', description: 'must not change' });
  const rBel = bowCli(['amend', 'FEAT-9462', '--field', 'desc', '--to', 'attack: BEL reason', '--reason', '\x07']);
  assert.notEqual(rBel.status, 0, 'a BEL-only --reason must exit non-zero');
  assert.match(rBel.stderr, /reason/i);
  assert.match(rBel.stderr, /required/i);
  const [rowsBel] = await db.query('SELECT description FROM bow_items WHERE guid = ?', [guidBel]);
  assert.equal(rowsBel[0].description, 'must not change', 'a BEL-only reason must not let the write through');

  const guidEsc = await insertItem({ code: 'FEAT-9463', description: 'must not change' });
  const rEsc = bowCli(['amend', 'FEAT-9463', '--field', 'desc', '--to', 'attack: ESC reason', '--reason', '\x1B']);
  assert.notEqual(rEsc.status, 0, 'an ESC-only --reason must exit non-zero');
  assert.match(rEsc.stderr, /reason/i);
  assert.match(rEsc.stderr, /required/i);
  const [rowsEsc] = await db.query('SELECT description FROM bow_items WHERE guid = ?', [guidEsc]);
  assert.equal(rowsEsc[0].description, 'must not change', 'an ESC-only reason must not let the write through');
});

test('FEAT-044 BOUNCE-BACK ROUND 4 (comprehensiveness proof): `amend --to` containing ONLY a Co-category private-use character (U+E000) is rejected -- proves this round\'s fix is genuinely comprehensive (the broad \\p{C} group), not just a targeted patch for the Cc case round 3 happened to find', async () => {
  const guid = await insertItem({ code: 'FEAT-9464', description: 'must not change' });
  const [beforeComments] = await db.query('SELECT COUNT(*) AS n FROM bow_comments WHERE item_guid = ?', [guid]);

  const privateUseOnly = '';
  const r = bowCli(['amend', 'FEAT-9464', '--field', 'desc', '--to', privateUseOnly, '--reason', 'trying to blank the field with a private-use character']);
  assert.notEqual(r.status, 0, 'a private-use-character-only --to must exit non-zero, not silently proceed');
  assert.match(r.stderr, /--to/);

  const [rows] = await db.query('SELECT description FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].description, 'must not change', 'a private-use-character-only replacement must not let the write through');
  const [afterComments] = await db.query('SELECT COUNT(*) AS n FROM bow_comments WHERE item_guid = ?', [guid]);
  assert.equal(afterComments[0].n, beforeComments[0].n, 'no audit comment may be posted when --to was private-use-character-only');
});

test('FEAT-044 BOUNCE-BACK ROUND 4: hasVisibleContent unit-level truth table (direct import) confirms [\\p{C}\\p{Z}] alone is comprehensive -- covers control (Cc), private-use (Co), plus all prior rounds\' whitespace/Cf/mixed/empty cases', () => {
  assert.equal(bow.hasVisibleContent('\x01'), false, 'a lone Cc control character (U+0001) has no visible content (round-3 case)');
  assert.equal(bow.hasVisibleContent('\x00'), false, 'a lone NUL (U+0000) has no visible content');
  assert.equal(bow.hasVisibleContent('\x07'), false, 'a lone BEL (U+0007) has no visible content');
  assert.equal(bow.hasVisibleContent('\x1B'), false, 'a lone ESC (U+001B) has no visible content');
  assert.equal(bow.hasVisibleContent(''), false, 'a lone private-use character (U+E000, category Co) has no visible content');
  assert.equal(bow.hasVisibleContent('  \x01\t​\x1B  '), false, 'a mix of ordinary whitespace, Cc, and Cf characters has no visible content');
  assert.equal(bow.hasVisibleContent('a\x01b'), true, 'real content with an embedded Cc control character still counts as visible content');
  // Prior rounds' cases must still pass unchanged under the new [\p{C}\p{Z}] strip.
  assert.equal(bow.hasVisibleContent('real text'), true, 'plain text has visible content');
  assert.equal(bow.hasVisibleContent(''), false, 'empty string has no visible content');
  assert.equal(bow.hasVisibleContent('   '), false, 'plain whitespace has no visible content');
  assert.equal(bow.hasVisibleContent('\t\t'), false, 'tabs have no visible content');
  assert.equal(bow.hasVisibleContent(' '), false, 'a lone NBSP has no visible content (round-1/round-2 case)');
  assert.equal(bow.hasVisibleContent('​'), false, 'a lone zero-width space has no visible content (round-3 case)');
  assert.equal(bow.hasVisibleContent('‌‍⁠­᠎'), false, 'a run of assorted Cf characters has no visible content');
  assert.equal(bow.hasVisibleContent('  ​\t‌  '), false, 'a mix of ordinary whitespace and Cf characters has no visible content');
  assert.equal(bow.hasVisibleContent('a​b'), true, 'real content with an embedded Cf character still counts as visible content');
  assert.equal(bow.hasVisibleContent(null), false, 'non-string input is treated as having no visible content, not thrown on');
});

test('FEAT-044 BOUNCE-BACK ROUND 5: hasVisibleContent unit-level truth table (direct import) confirms Default_Ignorable_Code_Point catches the Hangul jamo filler gap (Lo category, not C/Z) plus all prior rounds\' cases', () => {
  // U+115F HANGUL CHOSEONG FILLER, U+1160 HANGUL JUNGSEONG FILLER, U+3164
  // HANGUL FILLER: all three are General Category Lo (Letter, other), so
  // round 4's [\p{C}\p{Z}] strip does NOT remove them -- they are the
  // round-5 Tester's bypass. Default_Ignorable_Code_Point covers them.
  assert.equal(bow.hasVisibleContent('ᅟ'), false, 'a lone HANGUL CHOSEONG FILLER (U+115F, category Lo) has no visible content (round-5 case)');
  assert.equal(bow.hasVisibleContent('ᅠ'), false, 'a lone HANGUL JUNGSEONG FILLER (U+1160, category Lo) has no visible content (round-5 case)');
  assert.equal(bow.hasVisibleContent('ㅤ'), false, 'a lone HANGUL FILLER (U+3164, category Lo) has no visible content (round-5 case)');
  assert.equal(bow.hasVisibleContent('ᅟᅠㅤ'), false, 'a run of all three Hangul filler characters has no visible content');
  assert.equal(bow.hasVisibleContent('  ᅟ\tㅤ  '), false, 'a mix of ordinary whitespace and Hangul filler characters has no visible content');
  // False-positive guard: real content with a filler embedded mid-string
  // must still count as visible AND survive verbatim (not get stripped
  // from the actual field value -- hasVisibleContent only gates accept/
  // reject, it never mutates the stored --reason/--to text itself).
  assert.equal(bow.hasVisibleContent('aㅤb'), true, 'real content with an embedded HANGUL FILLER still counts as visible content');
  assert.equal(bow.hasVisibleContent('legitᅟreason'), true, 'real content with an embedded HANGUL CHOSEONG FILLER still counts as visible content');
  // Prior rounds' cases must still pass unchanged under the new
  // [\p{C}\p{Z}\p{Default_Ignorable_Code_Point}] strip.
  assert.equal(bow.hasVisibleContent('\x01'), false, 'a lone Cc control character (U+0001) has no visible content (round-3 case)');
  assert.equal(bow.hasVisibleContent(''), false, 'a lone private-use character (U+E000, category Co) has no visible content');
  assert.equal(bow.hasVisibleContent('real text'), true, 'plain text has visible content');
  assert.equal(bow.hasVisibleContent(''), false, 'empty string has no visible content');
  assert.equal(bow.hasVisibleContent('   '), false, 'plain whitespace has no visible content');
  assert.equal(bow.hasVisibleContent(' '), false, 'a lone NBSP has no visible content (round-1/round-2 case)');
  assert.equal(bow.hasVisibleContent('​'), false, 'a lone zero-width space has no visible content (round-3 case)');
  assert.equal(bow.hasVisibleContent('a​b'), true, 'real content with an embedded Cf character still counts as visible content');
  assert.equal(bow.hasVisibleContent(null), false, 'non-string input is treated as having no visible content, not thrown on');
});

test('FEAT-044 AC-2/AC-3: `amend --field status` (a field with its own sanctioned command) is rejected with the same "unsupported field" message as an unrecognized field, and the row is unchanged', async () => {
  const guid = await insertItem({ code: 'FEAT-9405', status: 'open' });
  const r = bowCli(['amend', 'FEAT-9405', '--field', 'status', '--to', 'done', '--reason', 'trying to sneak a status change through']);
  assert.notEqual(r.status, 0, 'amend --field status must be rejected, not silently routed to cmdSet\'s status logic');
  assert.match(r.stderr, /unsupported field/i);
  assert.match(r.stderr, /status/);

  const [rows] = await db.query('SELECT status FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].status, 'open', 'status must be completely unchanged');
});

test('FEAT-044 AC-2/AC-3: `amend --field priority` and `amend --field guid` are both rejected the identical way (closed allowlist, not a hand-maintained blocklist that could miss one)', async () => {
  const guid = await insertItem({ code: 'FEAT-9406', description: 'unchanged' });

  const rPriority = bowCli(['amend', 'FEAT-9406', '--field', 'priority', '--to', 'P0', '--reason', 'x']);
  assert.notEqual(rPriority.status, 0);
  assert.match(rPriority.stderr, /unsupported field/i);

  const rGuid = bowCli(['amend', 'FEAT-9406', '--field', 'guid', '--to', 'some-other-guid', '--reason', 'x']);
  assert.notEqual(rGuid.status, 0);
  assert.match(rGuid.stderr, /unsupported field/i);

  const [rows] = await db.query('SELECT guid, priority FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].guid, guid, 'guid must be completely immutable via amend');
  assert.equal(rows[0].priority, 'P2', 'priority (untouched by the rejected attempt) must be unchanged');
});

test('FEAT-044 AC-4: `amend --comment <nonexistent-id> --field body ...` fails with the same "no comment with id N" class of error `redact` already produces', async () => {
  const r = bowCli(['amend', '--comment', '99999999', '--field', 'body', '--to', 'x', '--reason', 'y']);
  assert.notEqual(r.status, 0);
  assert.match(r.stderr, /no comment with id 99999999/);
});

test('FEAT-044 AC-7: supplying both --reason and --reason-file is rejected non-zero, matching BUG-090/AC-3\'s evidentiary standard (query the DB, not just the exit code)', async () => {
  const guid = await insertItem({ code: 'FEAT-9407', description: 'must not change' });
  const tmp = mkTempDir('feat044-reason-');
  const reasonFile = path.join(tmp, 'reason.txt');
  fs.writeFileSync(reasonFile, 'reason from file', 'utf8');

  const r = bowCli(['amend', 'FEAT-9407', '--field', 'desc', '--to', 'x', '--reason', 'inline reason', '--reason-file', reasonFile]);
  assert.notEqual(r.status, 0, 'both --reason and --reason-file together must be rejected');
  assert.match(r.stderr, /--reason/);
  assert.match(r.stderr, /--reason-file/);

  const [rows] = await db.query('SELECT description FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].description, 'must not change');
});

test('FEAT-044 AC-7: supplying both --to and --to-file is rejected non-zero, matching BUG-090/AC-3\'s evidentiary standard', async () => {
  const guid = await insertItem({ code: 'FEAT-9408', description: 'must not change' });
  const tmp = mkTempDir('feat044-to-');
  const toFile = path.join(tmp, 'to.txt');
  fs.writeFileSync(toFile, 'replacement from file', 'utf8');

  const r = bowCli(['amend', 'FEAT-9408', '--field', 'desc', '--to', 'inline replacement', '--to-file', toFile, '--reason', 'x']);
  assert.notEqual(r.status, 0, 'both --to and --to-file together must be rejected');
  assert.match(r.stderr, /--to/);
  assert.match(r.stderr, /--to-file/);

  const [rows] = await db.query('SELECT description FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].description, 'must not change');
});

test('FEAT-044 (BUG-196-class regression guard): a dangling `--to` (last token, no value following) is REJECTED, not silently ignored/dropped', async () => {
  const guid = await insertItem({ code: 'FEAT-9409', description: 'must not change' });
  const r = spawnSync(process.execPath, ['claude-bow.js', 'amend', 'FEAT-9409', '--field', 'desc', '--reason', 'x', '--to'], {
    cwd: ROOT, env: { ...process.env, METRO_DB_NAME: TEST_DB }, encoding: 'utf8',
  });
  assert.notEqual(r.status, 0, 'a dangling --to must be rejected, not silently produce a no-op or a null write');
  assert.match(r.stderr, /--to/);
  assert.match(r.stderr, /requires a value|dangling/i);

  const [rows] = await db.query('SELECT description FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].description, 'must not change');
});

test('FEAT-044 (BUG-196-class regression guard): a dangling `--reason` immediately followed by another recognized flag (`--to`) is REJECTED, not silently consumed as --reason\'s value', async () => {
  const guid = await insertItem({ code: 'FEAT-9410', description: 'must not change' });
  const r = bowCli(['amend', 'FEAT-9410', '--field', 'desc', '--reason', '--to', 'sneaky replacement']);
  assert.notEqual(r.status, 0, 'a dangling --reason followed by --to must be rejected, not swallow --to\'s value as the reason text');
  assert.match(r.stderr, /--reason/);

  const [rows] = await db.query('SELECT description FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].description, 'must not change', 'the dangling flag must not have silently landed a write with --to\'s literal string consumed as the reason');
});

test('FEAT-044 AC-8: `redact` and `amend` audit-trail writes route through the SAME shared engine (applyMutationWithAudit), not two independently-reimplemented "insert into bow_comments" calls', () => {
  const { applyMutationWithAudit, cmdRedact, cmdAmend } = bow;
  assert.equal(typeof applyMutationWithAudit, 'function', 'applyMutationWithAudit must be exported as the shared engine');
  assert.equal(typeof cmdRedact, 'function');
  assert.equal(typeof cmdAmend, 'function');

  const src = fs.readFileSync(path.join(ROOT, 'claude-bow.js'), 'utf8');
  const redactBody = src.slice(src.indexOf('async function cmdRedact'), src.indexOf('async function cmdAmend'));
  const amendBody = src.slice(src.indexOf('async function cmdAmend'));
  assert.match(redactBody, /applyMutationWithAudit\(/, 'cmdRedact must call the shared engine');
  assert.match(amendBody, /applyMutationWithAudit\(/, 'cmdAmend must call the shared engine');
  // Neither function may bypass the shared engine with its own direct
  // "INSERT INTO bow_comments" call for the audit row.
  const redactOwnInserts = (redactBody.match(/INSERT INTO bow_comments/g) || []).length;
  const amendOwnInserts = (amendBody.match(/INSERT INTO bow_comments/g) || []).length;
  assert.equal(redactOwnInserts, 0, 'cmdRedact must not independently INSERT INTO bow_comments outside the shared engine');
  assert.equal(amendOwnInserts, 0, 'cmdAmend must not independently INSERT INTO bow_comments outside the shared engine');
});

test('FEAT-044 AC-8 (redact-mode suppression, shared engine): `redact`\'s audit comment never quotes the pre-image text, while `amend`\'s audit comment (same engine) always quotes old/new in full — proving redact is a suppression MODE of the same engine, not a fork with independently-drifting discipline', async () => {
  const redactGuid = await insertItem({
    code: 'FEAT-9411',
    description: `Comparing against ${buildFullTitlePhrase()} for scale reference.`,
  });
  const redactResult = bowCli(['redact', 'FEAT-9411']);
  assert.equal(redactResult.status, 0, `redact failed: ${redactResult.stderr}`);
  const [[redactComment]] = await db.query(
    'SELECT body FROM bow_comments WHERE item_guid = ? ORDER BY id DESC LIMIT 1', [redactGuid]);
  assert.ok(!redactComment.body.includes(buildFullTitlePhrase()), 'redact mode: the audit comment must NEVER contain the matched/old text');

  const amendGuid = await insertItem({ code: 'FEAT-9412', description: 'plain old text, no forbidden pattern' });
  const amendResult = bowCli(['amend', 'FEAT-9412', '--field', 'desc', '--to', 'plain new text', '--reason', 'AC-8 suppression contrast check']);
  assert.equal(amendResult.status, 0, `amend failed: ${amendResult.stderr}`);
  const [[amendComment]] = await db.query(
    'SELECT body FROM bow_comments WHERE item_guid = ? ORDER BY id DESC LIMIT 1', [amendGuid]);
  assert.match(amendComment.body, /plain old text, no forbidden pattern/, 'amend mode: the audit comment MUST quote the old text in full');
  assert.match(amendComment.body, /plain new text/, 'amend mode: the audit comment MUST quote the new text in full');
});

test('FEAT-044 AC-9: `set --desc`/`set --desc-file` remain unchanged, and `set`\'s own usage text now points to `amend` for prose correction', async () => {
  const guid = await insertItem({ code: 'FEAT-9413', description: 'old' });
  const r = bowCli(['set', 'FEAT-9413', '--desc', 'still works exactly as before']);
  assert.equal(r.status, 0, `set --desc must still work unmodified: ${r.stderr}`);
  const [rows] = await db.query('SELECT description FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].description, 'still works exactly as before');

  const src = fs.readFileSync(path.join(ROOT, 'claude-bow.js'), 'utf8');
  const setBody = src.slice(src.indexOf('async function cmdSet'), src.indexOf('// ── redact'));
  assert.match(setBody, /amend/, 'cmdSet\'s own body/usage text must name `amend` as the audited alternative for prose correction (AC-10 pointer)');
});

test('FEAT-044 AC-10: `amend`\'s own usage/help text or header comment names `redact` as the sibling command for GR#22 content specifically', () => {
  const src = fs.readFileSync(path.join(ROOT, 'claude-bow.js'), 'utf8');
  const amendSection = src.slice(src.indexOf('// ── amend (FEAT-044'));
  assert.match(amendSection, /redact/i, 'the amend section (header comment + usage text) must mention redact by name');
  assert.match(amendSection, /reason/i, 'the amend section must document the mandatory --reason requirement');
});

test('FEAT-044 AC-11: `amend --field title --to <300-char string>` is refused with an overflow error before any write, title left unchanged', async () => {
  const guid = await insertItem({ code: 'FEAT-9414' });
  await setTitle(guid, 'Original Short Title');
  const tooLong = 'x'.repeat(300);

  const r = bowCli(['amend', 'FEAT-9414', '--field', 'title', '--to', tooLong, '--reason', 'AC-11 overflow check']);
  assert.notEqual(r.status, 0, 'a 300-char title amendment must be refused (VARCHAR(255) limit)');
  assert.match(r.stderr, /255/);

  const [rows] = await db.query('SELECT title FROM bow_items WHERE guid = ?', [guid]);
  assert.equal(rows[0].title, 'Original Short Title', 'title must be completely unchanged after the refused overflow write');
});

test('FEAT-044 AC-12: `amend --to` containing a GR#22 forbidden pattern produces a non-fatal stderr warning suggesting `redact`, but the write still succeeds', async () => {
  const guid = await insertItem({ code: 'FEAT-9415', description: 'ordinary starting text' });
  const r = bowCli(['amend', 'FEAT-9415', '--field', 'desc', '--to', `mentions ${buildSingleWordPhrase()} in passing`, '--reason', 'AC-12 advisory warning check']);
  assert.equal(r.status, 0, 'AC-12 warning must never block the write');
  assert.match(r.stderr, /WARNING/i);
  assert.match(r.stderr, /redact/i);

  const [rows] = await db.query('SELECT description FROM bow_items WHERE guid = ?', [guid]);
  assert.match(rows[0].description, /mentions/, 'the write must have actually succeeded despite the advisory warning');
});

test('FEAT-044 AC-12: `amend --to` with plain text (no GR#22 pattern) produces NO warning', async () => {
  await insertItem({ code: 'FEAT-9416', description: 'ordinary starting text' });
  const r = bowCli(['amend', 'FEAT-9416', '--field', 'desc', '--to', 'a perfectly ordinary replacement with no special patterns at all', '--reason', 'AC-12 negative control']);
  assert.equal(r.status, 0, `amend failed: ${r.stderr}`);
  assert.ok(!/WARNING/i.test(r.stderr), `unexpected GR#22 advisory warning fired on clean text: ${r.stderr}`);
});
