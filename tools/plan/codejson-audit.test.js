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
const { spawnSync } = require('child_process');

const ROOT = path.resolve(__dirname, '..', '..');
const {
  runAudit, normalizeModulePath, isGoTreePath, runAstinfo,
  parseGoImports, resolveDirOwners, isImportRegistered, unhedgedOwners, primaryOwner, isBenignSibling,
} = require('./codejson-audit.js');

const sha256 = (buf) => crypto.createHash('sha256').update(buf).digest('hex');

/** Runs a git command against ROOT and returns trimmed stdout. Only used to
 * stage/unstage the Direction-D e2e fixture (D-1) — Direction D scans
 * git-TRACKED files, so a filesystem-only fixture (the AC-4 pattern) would be
 * invisible to it. `git add`/`git rm --cached` are safe here: they touch ONLY
 * the throwaway fixture path, never other work. */
function git(args) {
  const r = spawnSync('git', args, { cwd: ROOT, encoding: 'utf8' });
  if (r.status !== 0) {
    throw new Error(`git ${args.join(' ')} failed: ${r.stderr || r.stdout}`);
  }
  return r.stdout.trim();
}

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

test('AC-9: every finding instance in every populated class carries a valid fix-route label (master-plan-fix / code-side-defect / not-yet-built / informational / advisory)', async () => {
  const report = await runAudit();
  // BUG-327 D-5/D-4 added two routes: not-yet-built (unbuilt from-module edges,
  // the truth is "not written yet") and advisory (laundered-edge visibility).
  const VALID = ['master-plan-fix', 'code-side-defect', 'not-yet-built', 'informational', 'advisory'];
  for (const [cls, data] of Object.entries(report.findingsByClass)) {
    assert.ok(VALID.includes(data.fixRoute), `class ${cls} must have a class-level fixRoute, got ${data.fixRoute}`);
    for (const inst of data.instances) {
      assert.ok(VALID.includes(inst.fixRoute), `every instance of ${cls} must carry a fixRoute label, got ${inst.fixRoute}`);
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
  assert.ok(populated.length <= 8, 'at most 8 classes are defined; filing must never exceed one item per class');
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

// ── BUG-327: Direction-D reverse import-edge check (pure-unit helpers) ─────

test('BUG-327 parseGoImports: parses single, aliased, dot, blank and grouped imports, ignores line/block comments, and stops at the first top-level declaration', () => {
  const src = [
    'package foo',
    '',
    '// leading line comment',
    'import "github.com/x/internal/a"',
    'import alias "github.com/x/internal/b"',
    'import . "github.com/x/internal/c"',
    'import _ "github.com/x/internal/d"',
    'import (',
    '\t"github.com/x/internal/e"',
    '\tf "github.com/x/internal/f"',
    ')',
    '/* a block',
    '   comment spanning lines */',
    'var Marker = func() { _ = "import \\"not-a-real-import\\"" }',
  ].join('\n');
  assert.deepEqual(parseGoImports(src), [
    'github.com/x/internal/a',
    'github.com/x/internal/b',
    'github.com/x/internal/c',
    'github.com/x/internal/d',
    'github.com/x/internal/e',
    'github.com/x/internal/f',
  ]);
});

test('BUG-327 parseGoImports: robust to non-canonical-but-compilable forms (same-line block, no-space, split import)', () => {
  const sameLine = 'package x\nimport ("github.com/x/internal/a")\nvar V = 1\n';
  assert.deepEqual(parseGoImports(sameLine), ['github.com/x/internal/a']);
  const noSpace = 'package x\nimport"github.com/x/internal/b"\n';
  assert.deepEqual(parseGoImports(noSpace), ['github.com/x/internal/b']);
  const split = 'package x\nimport\n(\n\t"github.com/x/internal/c"\n)\n';
  assert.deepEqual(parseGoImports(split), ['github.com/x/internal/c']);
});

test('BUG-327 resolveDirOwners: resolves a child dir to its nearest registered ancestor and returns ALL owners sharing that path (owner-set exposure, not single-winner)', () => {
  const ownersByDir = new Map([
    ['internal/engine/citizens', new Set(['engine.citizens', 'feat.deathwave'])],
    ['internal/engine', new Set(['engine.core'])],
  ]);
  const registeredDirList = [...ownersByDir.keys()].sort((a, b) => (b.length - a.length) || a.localeCompare(b));
  const child = resolveDirOwners(registeredDirList, ownersByDir, 'internal/engine/citizens/fertility');
  assert.equal(child.path, 'internal/engine/citizens', 'deepest registered ancestor wins over a shallower one');
  assert.deepEqual([...child.owners].sort(), ['engine.citizens', 'feat.deathwave'], 'both entries sharing the path must be exposed');
  assert.equal(resolveDirOwners(registeredDirList, ownersByDir, 'internal/nowhere'), null, 'unregistered dir resolves to null');
});

test('BUG-327 isImportRegistered: ANY-owner — an edge held only by the FEATURE owner still counts as registered', () => {
  const fromOwners = new Set(['engine.citizens', 'feat.deathwave']);
  const toOwners = new Set(['foundation.num']);
  const featureOnly = new Map([['feat.deathwave', new Set(['foundation.num'])]]);
  assert.equal(isImportRegistered(fromOwners, toOwners, featureOnly), true, 'the feature owner holding the edge must register the import');
  const nobody = new Map([['engine.citizens', new Set(['foundation.errors'])]]);
  assert.equal(isImportRegistered(fromOwners, toOwners, nobody), false, 'no owner holding the edge means unregistered');
});

test('BUG-327 D-4 unhedgedOwners: ANY-owner laundering — a co-owner holding no edge is exposed even though the import registers via a housemate\'s edge', () => {
  const fromOwners = new Set(['engine.citizens', 'feat.deathwave']);
  const toOwners = new Set(['foundation.num']);
  const featureOnly = new Map([['feat.deathwave', new Set(['foundation.num'])]]);
  assert.deepEqual(unhedgedOwners(fromOwners, toOwners, featureOnly), ['engine.citizens'],
    'engine.citizens holds no edge while feat.deathwave covers the import — the gap is laundered');
  const both = new Map([
    ['feat.deathwave', new Set(['foundation.num'])],
    ['engine.citizens', new Set(['foundation.num'])],
  ]);
  assert.deepEqual(unhedgedOwners(fromOwners, toOwners, both), [], 'every owner hedged means no laundering');
  assert.deepEqual(unhedgedOwners(fromOwners, toOwners, new Map()), ['engine.citizens', 'feat.deathwave'],
    'no owner holding the edge is not laundering — it is the plain unregistered finding (caller only reports laundering when registered)');
});

test('BUG-327 primaryOwner: module-type wins over feature-type even when the feature sorts FIRST alphabetically (D-2 — rank must drive, not alphabetical tie-break)', () => {
  // r3 REJECT re-fixture: the original used {engine.citizens, feat.deathwave}
  // where the module is ALSO alphabetically first, so rank and alphabetical
  // tie-break agreed and the module-over-feature rank could be deleted without
  // any test noticing. `aaa.feature` sorts before `engine.citizens`, so the
  // module must win by RANK alone — the only thing distinguishing it.
  const byKey = new Map([
    ['engine.citizens', { bowType: 'module' }],
    ['aaa.feature', { bowType: 'feature' }],
  ]);
  assert.equal(primaryOwner(new Set(['aaa.feature', 'engine.citizens']), byKey), 'engine.citizens');
  // And the alphabetical fallback still applies when ranks are equal (both features).
  const bothFeats = new Map([
    ['zz.feature', { bowType: 'feature' }],
    ['aaa.feature', { bowType: 'feature' }],
  ]);
  assert.equal(primaryOwner(new Set(['zz.feature', 'aaa.feature']), bothFeats), 'aaa.feature');
});

test('BUG-327 isBenignSibling: only "descendant imports its module root" is benign; same-root NOT-benign rulings are the caller\'s D-8 intra-module skip (r4 F-1 reconciliation)', () => {
  // child imports its parent module root (replay/gen -> replay) — benign, the
  // sole case that reaches the classification step as a finding candidate.
  assert.equal(isBenignSibling('internal/harness/replay', 'internal/harness/replay', 'internal/harness/replay'), true);
  // Parent module root imports its own unregistered child — NOT benign at the
  // function level, but the Direction D caller's D-8 skip drops it before
  // classification (both sides resolve to the same registered root; intra-
  // module, deliberately not a GR#20 module-crossing finding — the child's gap
  // is Direction B's orphan class). The assertion documents the composed rule:
  // the import is NOT surfaced, and this is deliberate.
  assert.equal(isBenignSibling('internal/foo', 'internal/foo/bar', 'internal/foo'), false);
  // Two distinct unregistered siblings under one coarse ancestor — NOT benign
  // at the function level, equally dropped by the caller's D-8 skip when both
  // sides collapse to the same registered root.
  assert.equal(isBenignSibling('internal/foo', 'internal/foo/b', 'internal/foo'), false);
  // The NOT-benign ruling DOES reach a finding when the two sides resolve to
  // DIFFERENT registered roots — D-8's same-root test fails, so the edge is a
  // real module-crossing candidate. (r4 F-1: this is the case the old docstring
  // overgeneralised; the caller's D-8 skip only pre-empts the same-root ones.)
  assert.equal(isBenignSibling('internal/foo/a', 'internal/bar/b', 'internal/bar'), false);
});

test('BUG-327 D-1: Direction D end-to-end — a real injected module-crossing PROD import with no registered edge is REPORTED, and clears once removed (r3 REJECT acceptance)', async () => {
  // Pair verified at write-time (2026-08-22): engine.core is the sole owner of
  // internal/engine/core and registers NO outbound.calls edge to foundation.num
  // (sole owner of internal/foundation/num), so a real import from a child of
  // the former to the latter MUST fire Direction D's import-edge-not-registered
  // PROD class. The old suite only unit-tested helpers — a feature can be
  // deleted and stay green; this e2e test makes Direction D fire against a real
  // injected import (the same fixture create/remove shape as the AC-4 orphan
  // test). Direction D scans git-TRACKED files, so the fixture is staged with
  // `git add` for the scan to see it, then unstaged+removed in `finally`.
  const fixtureDir = path.join(ROOT, 'internal', 'engine', 'core', '_fixture_dirD_e2e');
  const fixtureFile = path.join(fixtureDir, 'imports_edge.go');
  const relFile = 'internal/engine/core/_fixture_dirD_e2e/imports_edge.go';
  const edge = { fromPath: 'internal/engine/core', toPath: 'internal/foundation/num' };
  try {
    fs.mkdirSync(fixtureDir, { recursive: true });
    fs.writeFileSync(fixtureFile,
      'package fixturedirde2e\n\n' +
      '// BUG-327 D-1 e2e fixture — real import, no registered edge (test-managed).\n' +
      'import _ "github.com/aaronukgarcia/Metropolis/internal/foundation/num"\n',
      'utf8');
    git(['add', '--', relFile]);

    const withFixture = await runAudit();
    const row = withFixture.directionD.rows.find(r => r.fromPath === edge.fromPath && r.toPath === edge.toPath);
    assert.ok(row,
      `expected a Direction-D row for ${edge.fromPath} -> ${edge.toPath}; got: ${JSON.stringify(withFixture.directionD.rows.map(r => `${r.fromPath}->${r.toPath}`))}`);
    assert.equal(row.registered, false, 'a real import with no registered edge must be flagged unregistered');
    assert.ok(row.prodFileCount > 0, `expected the fixture import to count as PROD (non-test), got prodFileCount=${row.prodFileCount}`);
    const inst = withFixture.findingsByClass['import-edge-not-registered'].instances
      .find(i => i.from === 'engine.core' && i.to === 'foundation.num');
    assert.ok(inst, 'the fixture edge must surface in the import-edge-not-registered finding class');
    assert.match(inst.detail, /PROD import edge/, 'the finding detail must describe a PROD (non-test) import');
  } finally {
    try { git(['rm', '--cached', '-q', '--', relFile]); } catch { /* fixture never staged — nothing to unstage */ }
    fs.rmSync(fixtureDir, { recursive: true, force: true });
  }

  const withoutFixture = await runAudit();
  const rowAfter = withoutFixture.directionD.rows.find(r => r.fromPath === edge.fromPath && r.toPath === edge.toPath);
  assert.equal(rowAfter, undefined, 'the fixture edge must disappear from Direction-D rows once the file is removed');
  const instAfter = withoutFixture.findingsByClass['import-edge-not-registered'].instances
    .filter(i => i.from === 'engine.core' && i.to === 'foundation.num');
  assert.equal(instAfter.length, 0, 'the fixture finding must clear once the file is removed');
});
