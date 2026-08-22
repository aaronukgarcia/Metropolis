/**
 * tools/plan/codejson-audit.test.js — FEAT-062 fixture-mutation tests.
 *
 * Per dev-team-process.md v1.9 ("an acceptance criterion's CHECK must be
 * able to fail") every test here proves the audit catches a REAL injected
 * mutation, then proves it stops catching it once the mutation is reverted
 * — a check that only ever reads the current, already-clean tree cannot
 * prove it would catch a real defect (docs/planning/acceptance/
 * plan.pipeline.md, AC-4/AC-6's explicit fixture-test requirement).
 *
 * Every test that mutates a real repo file restores it byte-for-byte in a
 * `finally` block, so a failing assertion can never leave residue (same
 * discipline as tools/plan/generate.test.js).
 *
 * Requires the metro MariaDB (`node claude-bow.js summary` must work) — the
 * audit's AC-1 gate queries live BOW status, same as every other Metropolis
 * BOW-aware tool.
 *
 * Run: node tools/plan/codejson-audit.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const path = require('path');
const crypto = require('crypto');

const ROOT = path.resolve(__dirname, '..', '..');
const { runAudit, normalizeModulePath, isGoTreePath, runAstinfo } = require('./codejson-audit.js');

const sha256 = (buf) => crypto.createHash('sha256').update(buf).digest('hex');

/**
 * BUG-207: the two AC-6 end-to-end tests below used to key off whichever
 * module happened to already report 'contract-ok' under the LIVE BOW status
 * gate (runAudit()'s default path, which queries the metro MariaDB for each
 * module's `done`/not-done status). That precondition held locally (the dev
 * box's BOW state happens to have done modules with resolvable Go contracts)
 * but not on CI's fresh checkout — same commit, but the BOW MariaDB there
 * has no matching 'done' rows, so every module falls into "not-yet-built"
 * and zero contract rows are even produced, let alone a contract-ok one.
 * The precondition was never about repo *content* drifting; it was about an
 * external, environment-specific system (the live BOW DB) that this test
 * has no business depending on to prove a pure name-matching classifier.
 *
 * Fix: construct a synthetic bowStatuses map (runAudit()'s existing
 * test-isolation override, same pattern used elsewhere in this project for
 * BOW-independent fixture tests) that forces exactly ONE specific,
 * deterministically-chosen module to 'done' — chosen by scanning the real
 * code.json + real Go source (both committed, identical on every checkout)
 * for a module whose inbound.name already resolves to a real exported Go
 * symbol. This makes the test self-contained: it no longer talks to the
 * live BOW DB at all, so it can't drift between environments the way the
 * live-DB-gated precondition did.
 */
function findContractOkFixtureModule() {
  const codeJson = JSON.parse(fs.readFileSync(path.join(ROOT, 'code.json'), 'utf8'));
  for (const m of codeJson.modules) {
    const inboundName = m.inbound && m.inbound.name;
    if (!inboundName) continue;
    const normPath = normalizeModulePath(m.path);
    if (!isGoTreePath(normPath)) continue;
    if (!fs.existsSync(path.join(ROOT, normPath))) continue;
    const info = runAstinfo([normPath]).get(normPath);
    if (!info || info.error) continue;
    const exact = info.exported.find(s => s.name === inboundName);
    if (!exact) continue;
    return {
      key: m.key,
      name: inboundName,
      dir: normPath,
      bowStatuses: new Map([[m.key, { code: 'BUG-207-FIXTURE', status: 'done' }]]),
    };
  }
  return null;
}

function stripHeader(report) {
  const clone = JSON.parse(JSON.stringify(report));
  delete clone.runId;
  delete clone.startedAt;
  return clone;
}

// ── AC-1: determinism — identical repo state must classify identically ────

test('AC-1: two consecutive runs against the same unchanged commit are byte-identical modulo the run header', async () => {
  const a = await runAudit();
  const b = await runAudit();
  assert.deepEqual(stripHeader(a), stripHeader(b), 'two back-to-back runs against unchanged state must produce identical findings/classification');
});

test('AC-1: the three-state path-resolution breakdown always sums to the full module count (no silently-dropped modules)', async () => {
  const report = await runAudit();
  const sc = report.directionA.pathResolution.stateCounts;
  const sum = sc['path-exists-done'] + sc['path-missing-done'] + sc['not-yet-built'];
  assert.equal(sum, report.directionA.pathResolution.total);
  assert.equal(report.directionA.pathResolution.total, report.actualModuleCount);
  assert.equal(report.moduleCount, report.actualModuleCount, 'code.json moduleCount field must match modules[].length');
});

// ── AC-4: orphan directory detection — fixture create/remove ───────────────

test('AC-4: a new Go-bearing directory with no registry entry is caught as an orphan, and clears once removed', async () => {
  const fixtureDir = path.join(ROOT, 'internal', '_fixture_orphan_feat062');
  const fixtureFile = path.join(fixtureDir, 'x.go');
  const relDir = 'internal/_fixture_orphan_feat062';
  try {
    fs.mkdirSync(fixtureDir, { recursive: true });
    fs.writeFileSync(fixtureFile, 'package fixtureorphanfeat062\n\n// Marker exists only to give this throwaway directory at least one .go file.\nvar Marker = true\n', 'utf8');

    const withFixture = await runAudit();
    const orphanDirs = withFixture.directionB.rows.filter(r => !r.registered).map(r => r.dir);
    assert.ok(orphanDirs.includes(relDir), `expected ${relDir} to be reported as a new orphan; got: ${JSON.stringify(orphanDirs)}`);
    const classInstances = withFixture.findingsByClass['orphan-directory'].instances.map(i => i.dir);
    assert.ok(classInstances.includes(relDir), 'expected the orphan-directory finding class to enumerate the fixture directory');
  } finally {
    fs.rmSync(fixtureDir, { recursive: true, force: true });
  }

  const withoutFixture = await runAudit();
  const orphanDirsAfter = withoutFixture.directionB.rows.filter(r => !r.registered).map(r => r.dir);
  assert.ok(!orphanDirsAfter.includes(relDir), 'the fixture directory must disappear from orphan findings once deleted');
});

test('AC-4: a subdirectory of an already-registered module path is NOT a false orphan (documented parent/child relationship)', async () => {
  // internal/engine/helper/helperfixture is a real on-disk child of the
  // registered feat.helper module path (internal/engine/helper/) — proves
  // the ancestor-coverage rule the acceptance doc's AC-4 text requires
  // ("a package split across subpackages under one registered module path
  // must be named as such, not silently treated as N orphans").
  const report = await runAudit();
  const row = report.directionB.rows.find(r => r.dir === 'internal/engine/helper/helperfixture');
  assert.ok(row, 'expected internal/engine/helper/helperfixture to appear in the Go-bearing-directory sweep');
  assert.equal(row.registered, true, 'a documented child of a registered parent must not be flagged as an orphan');
  assert.equal(row.matchType, 'child-of');
});

// ── AC-6: near-miss name/type drift — fixture rename/revert ────────────────

test('AC-6: renaming a done module\'s exported contract identifier so it no longer matches ANY exported symbol is AC-2 no-match (not near-miss), and clears on revert', async () => {
  const fixture = findContractOkFixtureModule();
  assert.ok(fixture, 'need at least one code.json module whose inbound.name resolves to a real exported Go symbol on disk to run this fixture test against');
  const { bowStatuses } = fixture;

  const before = await runAudit({ bowStatuses });
  const okRow = before.directionA.contracts.find(c => c.key === fixture.key && c.result === 'contract-ok');
  assert.ok(okRow, `expected forcing ${fixture.key} to BOW status done to produce a contract-ok row for it`);

  // Resolve the real file:line for okRow from astinfo's detail string
  // ("<dir>/<file>:<line>").
  const m = /^(.+)\/([^/]+):(\d+)$/.exec(okRow.detail);
  assert.ok(m, `could not parse contract-ok detail "${okRow.detail}"`);
  const [, dir, file, lineStr] = m;
  const filePath = path.join(ROOT, dir, file);
  const line = Number(lineStr);
  const original = fs.readFileSync(filePath, 'utf8');

  try {
    const lines = original.split('\n');
    const idx = line - 1;
    assert.ok(lines[idx].includes(okRow.name), `expected line ${line} of ${filePath} to contain identifier "${okRow.name}"; got: ${lines[idx]}`);
    // Rename by exactly one character (append "X") at the FIRST occurrence
    // of the identifier as a whole word on that line.
    const re = new RegExp(`\\b${okRow.name}\\b`);
    assert.ok(re.test(lines[idx]), 'identifier must appear as a whole word on the target line');
    lines[idx] = lines[idx].replace(re, okRow.name + 'X');
    fs.writeFileSync(filePath, lines.join('\n'), 'utf8');

    const mutated = await runAudit({ bowStatuses });
    const mutatedRow = mutated.directionA.contracts.find(c => c.key === okRow.key);
    assert.ok(mutatedRow, `expected a contract row for ${okRow.key} after mutation`);
    assert.equal(mutatedRow.result, 'no-match',
      `expected "${okRow.name}" to become a no-match once renamed to "${okRow.name}X" (the recorded name no longer matches ANY exported symbol including the renamed one), got: ${mutatedRow.result}`);

    // Now flip it the other way: rename the recorded code.json name is not
    // something we may do (read-only), so to exercise the near-miss path
    // specifically we mutate the Go identifier the other direction — drop
    // one character instead, landing on a case/spelling neighbour of the
    // recorded name is not guaranteed generically. Assert the no-match path
    // above (which is what a bare rename produces) AND separately assert
    // the near-miss classifier logic directly: same normalized form,
    // different literal string, is classified 'near-miss' not 'no-match'.
    const { CLASS_TITLES } = require('./codejson-audit.js');
    assert.ok(CLASS_TITLES['name-type-near-miss'], 'name-type-near-miss class must be defined');
    const foundClassAfterRename = mutated.findingsByClass['contract-does-not-resolve'].instances.some(i => i.key === okRow.key);
    assert.ok(foundClassAfterRename, 'the rename must surface as a contract-does-not-resolve finding instance');
  } finally {
    fs.writeFileSync(filePath, original, 'utf8');
  }

  const after = await runAudit({ bowStatuses });
  const afterRow = after.directionA.contracts.find(c => c.key === okRow.key);
  assert.equal(afterRow.result, 'contract-ok', 'reverting the file must restore contract-ok');
  assert.ok(sha256(fs.readFileSync(filePath)) === sha256(Buffer.from(original, 'utf8')), 'reverted file must be byte-identical to the original');
});

test('AC-6: end-to-end — uppercasing a done module\'s exported contract identifier (same normalized form, different literal string) fires a REAL near-miss finding distinct from AC-2\'s no-match, and clears on revert', async () => {
  const fixture = findContractOkFixtureModule();
  assert.ok(fixture, 'need at least one code.json module whose inbound.name resolves to a real exported Go symbol on disk to run this fixture test against');
  const { bowStatuses } = fixture;

  const before = await runAudit({ bowStatuses });
  const okRow = before.directionA.contracts.find(c => c.key === fixture.key && c.result === 'contract-ok');
  assert.ok(okRow, `expected forcing ${fixture.key} to BOW status done to produce a contract-ok row for it`);

  const m = /^(.+)\/([^/]+):(\d+)$/.exec(okRow.detail);
  assert.ok(m, `could not parse contract-ok detail "${okRow.detail}"`);
  const [, dir, file, lineStr] = m;
  const filePath = path.join(ROOT, dir, file);
  const line = Number(lineStr);
  const original = fs.readFileSync(filePath, 'utf8');

  try {
    const lines = original.split('\n');
    const idx = line - 1;
    const re = new RegExp(`\\b${okRow.name}\\b`);
    assert.ok(re.test(lines[idx]), 'identifier must appear as a whole word on the target line');
    // Uppercase the WHOLE identifier: "Transport" -> "TRANSPORT". Character-
    // for-character this is a different Go identifier (exact match fails),
    // but it normalizes (lowercase, strip non-alnum) to the same string as
    // the recorded code.json name — exactly the "resolves to something
    // similar but not identical" condition AC-6 exists to catch, and
    // exactly the condition a naive `strings.ToLower(a)===strings.ToLower(b)`
    // comparator (the AC's own false-pass warning) would wrongly swallow as
    // a pass instead of reporting.
    lines[idx] = lines[idx].replace(re, okRow.name.toUpperCase());
    fs.writeFileSync(filePath, lines.join('\n'), 'utf8');

    const mutated = await runAudit({ bowStatuses });
    const mutatedRow = mutated.directionA.contracts.find(c => c.key === okRow.key);
    assert.ok(mutatedRow, `expected a contract row for ${okRow.key} after mutation`);
    assert.equal(mutatedRow.result, 'near-miss',
      `expected uppercasing "${okRow.name}" to "${okRow.name.toUpperCase()}" to classify as near-miss (same normalized form, different literal string), got: ${mutatedRow.result}`);

    const classInstance = mutated.findingsByClass['name-type-near-miss'].instances.find(i => i.key === okRow.key);
    assert.ok(classInstance, 'expected a name-type-near-miss finding class instance for the mutated module');
    assert.equal(classInstance.recorded, okRow.name);
    assert.equal(classInstance.actual, okRow.name.toUpperCase());

    // And it must NOT also appear as an AC-2 contract-does-not-resolve
    // finding — the two classes are mutually exclusive per instance.
    const alsoNoMatch = mutated.findingsByClass['contract-does-not-resolve'].instances.some(i => i.key === okRow.key);
    assert.equal(alsoNoMatch, false, 'a near-miss must not ALSO be filed as a no-match — the classes are disjoint per the acceptance doc');
  } finally {
    fs.writeFileSync(filePath, original, 'utf8');
  }

  const after = await runAudit({ bowStatuses });
  const afterRow = after.directionA.contracts.find(c => c.key === okRow.key);
  assert.equal(afterRow.result, 'contract-ok', 'reverting the file must restore contract-ok');
  assert.ok(sha256(fs.readFileSync(filePath)) === sha256(Buffer.from(original, 'utf8')), 'reverted file must be byte-identical to the original');
});

// ── AC-7/AC-8: read-only constraint ─────────────────────────────────────────

test('AC-7/AC-8: a full audit run never modifies code.json or master-plan-v2.1.json (hash-compared before/after)', async () => {
  const codeJsonPath = path.join(ROOT, 'code.json');
  const masterPlanPath = path.join(ROOT, 'docs', 'planning', 'master-plan-v2.1.json');
  const before = { codeJson: sha256(fs.readFileSync(codeJsonPath)), masterPlan: sha256(fs.readFileSync(masterPlanPath)) };

  const report = await runAudit();

  const after = { codeJson: sha256(fs.readFileSync(codeJsonPath)), masterPlan: sha256(fs.readFileSync(masterPlanPath)) };
  assert.equal(before.codeJson, after.codeJson, 'code.json must be byte-identical before/after the audit');
  assert.equal(before.masterPlan, after.masterPlan, 'master-plan-v2.1.json must be byte-identical before/after the audit');
  assert.equal(report.selfCheck.codeJsonUnchanged, true);
  assert.equal(report.selfCheck.masterPlanUnchanged, true);
});

test('BUG-181: a third-party mutation of a scanned Go file DURING the run (not just an unreverted one) makes runAudit() throw the AC-7/AC-8 violation, naming the changed path', async () => {
  const target = path.join(ROOT, 'internal', 'foundation', 'buildinfo', 'buildinfo.go');
  const original = fs.readFileSync(target, 'utf8');

  // Previously this raced a fixed 300ms setTimeout (in a separate process)
  // against runAudit()'s internal timing, betting that the mutation would
  // land somewhere between the pre- and post-run snapshots. It didn't
  // reliably: the pre/post window's actual duration is dominated by a
  // blocking `spawnSync('go run ...')` call whose wall-clock time varies by
  // machine and filesystem/scheduler characteristics (CI's ubuntu-latest
  // containers vs a local Windows dev box), so the 300ms guess sometimes
  // fired after runAudit() had already taken its post-run snapshot — the
  // mutation missed the window entirely and runAudit() completed normally
  // (BUG-181's CI-only flake).
  //
  // runAudit() now accepts an `afterPreSnapshot` test hook (codejson-audit.js)
  // that is awaited immediately after the pre-run snapshot is taken and
  // before any scanning work begins. Mutating the file from that hook lands
  // inside the snapshot window BY CONSTRUCTION — no wall-clock guess, no
  // separate process, no flake, on any machine.
  try {
    let threw = null;
    try {
      await runAudit({
        afterPreSnapshot: () => {
          fs.appendFileSync(target, '\n// BUG-181 regression-test mutation\n');
        },
      });
    } catch (e) {
      threw = e;
    }
    assert.ok(threw, 'expected runAudit() to throw when a scanned Go file changes mid-run; it completed normally instead (BUG-181 regression)');
    assert.match(threw.message, /AC-7\/AC-8 VIOLATION/, `expected an AC-7/AC-8 VIOLATION error, got: ${threw.message}`);
    assert.match(threw.message, /gitStatusUnchangedForScannedPaths=false/, `expected the violation message to name gitStatusUnchangedForScannedPaths as the failing check, got: ${threw.message}`);
  } finally {
    fs.writeFileSync(target, original, 'utf8');
    assert.equal(sha256(fs.readFileSync(target)), sha256(Buffer.from(original, 'utf8')), 'fixture file must be restored byte-for-byte');
  }
});

// ── AC-9: every finding instance carries exactly one fix-route label ──────

test('AC-9: every finding instance in every populated class carries exactly one of "master-plan-fix"/"code-side-defect"', async () => {
  const report = await runAudit();
  for (const [cls, data] of Object.entries(report.findingsByClass)) {
    assert.ok(['master-plan-fix', 'code-side-defect'].includes(data.fixRoute), `class ${cls} must have a class-level fixRoute`);
    for (const inst of data.instances) {
      assert.ok(['master-plan-fix', 'code-side-defect'].includes(inst.fixRoute), `every instance of ${cls} must carry a fixRoute label`);
    }
  }
});

// ── AC-10: filing shape — one item per class, never per instance ──────────

test('AC-10: fileFindingsToBow would file at most one BOW item per POPULATED class, never one per instance (dry-run shape check, no DB write)', async () => {
  const report = await runAudit();
  const populated = Object.entries(report.findingsByClass).filter(([, d]) => d.instanceCount > 0);
  // This is a shape assertion on the report the filer consumes, not an
  // invocation of the filer itself (the real filer writes to the live BOW
  // and is deliberately never called by this test suite or by the audit's
  // default run — see codejson-audit.js's --file flag and FEAT-062's
  // report-only scope).
  assert.ok(populated.length <= 6, 'at most 6 classes are defined; filing must never exceed one item per class');
  for (const [cls, data] of populated) {
    assert.ok(data.instanceCount >= 1);
    assert.ok(Array.isArray(data.instances) && data.instances.length === data.instanceCount);
  }
});

// ── AC-12: report header names the commit + content hashes ────────────────

test('AC-12: report header states the exact commit hash and content hashes it ran against', async () => {
  const { execSync } = require('child_process');
  const report = await runAudit();
  const headNow = execSync('git rev-parse HEAD', { cwd: ROOT, encoding: 'utf8' }).trim();
  assert.equal(report.commitHash, headNow, 'commit hash must match git rev-parse HEAD (assuming no commit landed between audit run and this check)');
  assert.match(report.codeJsonSha256, /^[0-9a-f]{64}$/);
  assert.match(report.masterPlanSha256, /^[0-9a-f]{64}$/);
});

// ── BUG-309: edge-GUID well-formedness scan is actually exercised ──────────

test('BUG-309: a malformed edge GUID (outbound.calls[].moduleGuid) is caught by the well-formedness scan, and clears on revert', async () => {
  // The edge-GUID scan (codejson-audit.js:329-353) is the path the original
  // BUG-309 fix broke with a temporal-dead-zone ReferenceError — and no test
  // caught it, because every fixture happened to carry no outbound.calls[]
  // edge GUIDs at all, so the loop body never ran. This test mutates a
  // SANDBOXED COPY of code.json's edge GUID to a non-UUID and proves the scan
  // flags it, closing the "green tests that never execute the path" gap.
  //
  // The copy, NOT the real code.json (round D9): a mutate-then-restore against
  // the real file races other tests under `node --test`, which runs files in
  // parallel — a concurrent test can read the half-mutated file. runAudit
  // accepts opts.codeJsonPath to read the sandbox copy instead; the audit's
  // own AC-7/AC-8 self-check then compares pre/post hashes of the same sandbox
  // file (which this test does not touch mid-run), so the invariant holds.
  const sandboxPath = path.join(ROOT, 'code.json.d9-sandbox.tmp');
  const original = fs.readFileSync(path.join(ROOT, 'code.json'), 'utf8');
  fs.writeFileSync(sandboxPath, original, 'utf8');

  try {
    const codeJson = JSON.parse(original);

    let target = null;
    for (const m of codeJson.modules) {
      for (const call of (m.outbound && m.outbound.calls) || []) {
        if (call.moduleGuid) { target = call; break; }
      }
      if (target) break;
    }
    assert.ok(target, 'code.json must contain at least one outbound.calls[].moduleGuid to exercise the edge-GUID scan');
    assert.ok(target.moduleGuid, 'fixture edge GUID must be non-empty');

    target.moduleGuid = 'not-a-uuid-v4';
    fs.writeFileSync(sandboxPath, JSON.stringify(codeJson, null, 2), 'utf8');

    const report = await runAudit({ codeJsonPath: sandboxPath });
    const malformed = report.directionA.guids.malformed;
    assert.ok(
      malformed.some((f) => f.guid === 'not-a-uuid-v4'),
      `expected the malformed edge GUID to be flagged; malformed GUIDs seen: ${JSON.stringify(malformed.map((f) => f.guid))}`
    );

    // Prove the finding clears once the edge GUID is well-formed again.
    fs.writeFileSync(sandboxPath, original, 'utf8');
    const after = await runAudit({ codeJsonPath: sandboxPath });
    assert.ok(
      !after.directionA.guids.malformed.some((f) => f.guid === 'not-a-uuid-v4'),
      'the malformed edge GUID must disappear from findings once reverted'
    );
  } finally {
    fs.rmSync(sandboxPath, { force: true });
  }
});

// ── normalizeModulePath / isGoTreePath unit coverage ───────────────────────

test('normalizeModulePath: repo-root path "/" normalizes to "." (a real, always-existing path), not null', () => {
  assert.equal(normalizeModulePath('/'), '.');
});

test('normalizeModulePath: composite "A + B" reduces to primary component A, trailing slash stripped', () => {
  assert.equal(normalizeModulePath('internal/harness/replay/ + fixtures/'), 'internal/harness/replay');
});

test('isGoTreePath: only internal/ and cmd/ paths are Go-tree paths', () => {
  assert.equal(isGoTreePath('internal/protocol'), true);
  assert.equal(isGoTreePath('cmd/metctl'), true);
  assert.equal(isGoTreePath('tools/plan'), false);
  assert.equal(isGoTreePath('.'), false);
  assert.equal(isGoTreePath(null), false);
});
