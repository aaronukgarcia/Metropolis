/**
 * claude-devfeedback-import.test.js — regression tests for
 * claude-devfeedback-import.js (FEAT-065 AC-DM10/AC-DM11).
 *
 * Follows this session's house pattern for root-level claude-*.js tools
 * (see claude-secret-guard.test.js / claude-plan-guard.test.js): the
 * module only touches the real filesystem/spawns a real process when
 * require.main === module, so require()'ing it here is side-effect free,
 * and every test below wires its own tmp inbox/processed dirs plus a
 * stubbed spawnSyncFn (never the real claude-bow.js / metro DB — no test
 * in this file depends on the MariaDB instance being reachable).
 *
 * Run: node --test claude-devfeedback-import.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');

const {
  runImport,
  validateRecord,
  deriveTitle,
  deriveAttribution,
  deriveKind,
  SCHEMA_VERSION,
  DEFAULT_SOURCE_MKEY,
  DEFAULT_KIND,
} = require('./claude-devfeedback-import.js');

function mkTmpDir(prefix) {
  return fs.mkdtempSync(path.join(os.tmpdir(), prefix));
}

function writeRecord(dir, name, obj) {
  fs.mkdirSync(dir, { recursive: true });
  fs.writeFileSync(path.join(dir, name), JSON.stringify(obj, null, 2), 'utf8');
}

function wellFormedRecord(overrides) {
  return Object.assign(
    {
      schemaVersion: SCHEMA_VERSION,
      timestamp: '2026-08-12T10:30:00.000000000Z',
      tick: 42,
      correlationId: 'corr-fixture-1',
      body: 'the bridge is floating',
      debugTouched: true,
    },
    overrides || {}
  );
}

// A stub spawnSyncFn standing in for claude-bow.js — never invokes a real
// process or touches the metro DB. Records every invocation so tests can
// assert exactly how many (and with what args) claude-bow.js add was
// actually called, per AC-DM10's "False-pass warning": a test that only
// asserts "the script ran without throwing" is not sufficient.
//
// BUG-337 round-3: the stub now generates a NEW distinct BOW code per call,
// just like the real add does. This catches false-green tests that assume
// idempotency. Each call returns BUG-999, BUG-1000, BUG-1001, etc.
function makeStubSpawn(succeed) {
  const calls = [];
  let callCount = 0;
  const fn = (execPath, args, opts) => {
    calls.push({ execPath, args, opts });
    callCount++;
    if (succeed) {
      const bowCode = `BUG-${999 + callCount - 1}`;
      return { status: 0, stdout: `Added ${bowCode} [bug/P2] "stub"\nGUID: stub-guid-${callCount}\n`, stderr: '' };
    }
    return { status: 1, stdout: '', stderr: 'simulated claude-bow.js failure' };
  };
  fn.calls = calls;
  fn.callCount = () => callCount;
  return fn;
}

test('validateRecord accepts a well-formed record', () => {
  const raw = JSON.stringify(wellFormedRecord());
  const result = validateRecord(raw);
  assert.equal(result.ok, true);
  assert.equal(result.record.body, 'the bridge is floating');
});

test('validateRecord rejects invalid JSON', () => {
  const result = validateRecord('{not json');
  assert.equal(result.ok, false);
  assert.match(result.reason, /not valid JSON/);
});

test('validateRecord rejects a wrong schemaVersion', () => {
  const result = validateRecord(JSON.stringify(wellFormedRecord({ schemaVersion: 999 })));
  assert.equal(result.ok, false);
  assert.match(result.reason, /schemaVersion/);
});

test('validateRecord rejects a missing body field', () => {
  const record = wellFormedRecord();
  delete record.body;
  const result = validateRecord(JSON.stringify(record));
  assert.equal(result.ok, false);
  assert.match(result.reason, /"body"/);
});

test('validateRecord rejects a non-numeric tick', () => {
  const result = validateRecord(JSON.stringify(wellFormedRecord({ tick: 'forty-two' })));
  assert.equal(result.ok, false);
  assert.match(result.reason, /"tick"/);
});

test('deriveTitle truncates a long body and never includes raw newlines', () => {
  const longBody = 'x'.repeat(200) + '\nsecond line';
  const title = deriveTitle({ body: longBody });
  assert.ok(title.length < 120, `title too long: ${title.length}`);
  assert.ok(!title.includes('\n'), 'title must not contain a raw newline');
});

test('AC-DM10: a well-formed record produces exactly one claude-bow.js add invocation using --desc-file (never inline --desc), and is moved to processed/', () => {
  const inboxDir = mkTmpDir('devfb-inbox-');
  const processedDir = path.join(mkTmpDir('devfb-processed-'), 'processed');
  writeRecord(inboxDir, 'a.json', wellFormedRecord());

  const spawnStub = makeStubSpawn(true);
  const summary = runImport({ inboxDir, processedDir, spawnSyncFn: spawnStub, bowScript: '/fake/claude-bow.js' });

  assert.equal(summary.imported, 1);
  assert.equal(summary.malformed, 0);
  assert.equal(summary.failed, 0);

  assert.equal(spawnStub.calls.length, 1, 'expected exactly one claude-bow.js invocation');
  const args = spawnStub.calls[0].args;
  assert.ok(args.includes('add'));
  assert.ok(args.includes('bug'));
  const descFlagIdx = args.indexOf('--desc-file');
  assert.notEqual(descFlagIdx, -1, '--desc-file must be present');
  assert.ok(!args.includes('--desc'), 'must NEVER pass inline --desc (BUG-090)');

  // Moved to processed/, no longer in inbox/.
  assert.equal(fs.existsSync(path.join(inboxDir, 'a.json')), false);
  assert.equal(fs.existsSync(path.join(processedDir, 'a.json')), true);
});

test('AC-DM10: a malformed record stays in inbox/ with a .error sidecar naming the parse failure, and produces NO claude-bow.js call', () => {
  const inboxDir = mkTmpDir('devfb-inbox-');
  const processedDir = path.join(mkTmpDir('devfb-processed-'), 'processed');
  fs.mkdirSync(inboxDir, { recursive: true });
  fs.writeFileSync(path.join(inboxDir, 'bad.json'), '{ this is not valid json', 'utf8');

  const spawnStub = makeStubSpawn(true);
  const summary = runImport({ inboxDir, processedDir, spawnSyncFn: spawnStub, bowScript: '/fake/claude-bow.js' });

  assert.equal(summary.imported, 0);
  assert.equal(summary.malformed, 1);
  assert.equal(spawnStub.calls.length, 0, 'a malformed record must never reach claude-bow.js');

  assert.equal(fs.existsSync(path.join(inboxDir, 'bad.json')), true, 'malformed record must stay in inbox/');
  const sidecarPath = path.join(inboxDir, 'bad.json.error');
  assert.equal(fs.existsSync(sidecarPath), true, '.error sidecar must be written');
  const sidecar = JSON.parse(fs.readFileSync(sidecarPath, 'utf8'));
  assert.match(sidecar.reason, /JSON/);
});

test('AC-DM10 mixed fixture: one well-formed + one malformed record in the same run — the well-formed one imports and moves, the malformed one does not', () => {
  const inboxDir = mkTmpDir('devfb-inbox-');
  const processedDir = path.join(mkTmpDir('devfb-processed-'), 'processed');
  writeRecord(inboxDir, 'good.json', wellFormedRecord({ correlationId: 'corr-good' }));
  fs.writeFileSync(path.join(inboxDir, 'bad.json'), 'not json at all', 'utf8');

  const spawnStub = makeStubSpawn(true);
  const summary = runImport({ inboxDir, processedDir, spawnSyncFn: spawnStub, bowScript: '/fake/claude-bow.js' });

  assert.equal(summary.imported, 1);
  assert.equal(summary.malformed, 1);
  assert.equal(spawnStub.calls.length, 1);
  assert.equal(fs.existsSync(path.join(processedDir, 'good.json')), true);
  assert.equal(fs.existsSync(path.join(inboxDir, 'bad.json')), true);
  assert.equal(fs.existsSync(path.join(inboxDir, 'bad.json.error')), true);
});

test('AC-DM11: re-running against the same fixture inbox (well-formed already processed, malformed still present) causes NO duplicate claude-bow.js invocation — exactly one total across both runs', () => {
  const inboxDir = mkTmpDir('devfb-inbox-');
  const processedDir = path.join(mkTmpDir('devfb-processed-'), 'processed');
  writeRecord(inboxDir, 'good.json', wellFormedRecord({ correlationId: 'corr-good-2' }));
  fs.writeFileSync(path.join(inboxDir, 'bad.json'), 'not json at all', 'utf8');

  const spawnStub = makeStubSpawn(true);

  const first = runImport({ inboxDir, processedDir, spawnSyncFn: spawnStub, bowScript: '/fake/claude-bow.js' });
  assert.equal(first.imported, 1);
  assert.equal(spawnStub.calls.length, 1);

  const second = runImport({ inboxDir, processedDir, spawnSyncFn: spawnStub, bowScript: '/fake/claude-bow.js' });
  // good.json is now in processed/, not inbox/, so it is not re-scanned;
  // bad.json is still malformed and still produces zero BOW calls.
  assert.equal(second.imported, 0);
  assert.equal(second.malformed, 1);
  assert.equal(spawnStub.calls.length, 1, 'expected exactly ONE claude-bow.js add invocation total across both runs');
});

test('AC-DM11: an empty/nonexistent inbox is a no-op, not an error', () => {
  const inboxDir = path.join(mkTmpDir('devfb-inbox-'), 'never-created');
  const processedDir = path.join(mkTmpDir('devfb-processed-'), 'processed');
  const spawnStub = makeStubSpawn(true);

  const summary = runImport({ inboxDir, processedDir, spawnSyncFn: spawnStub, bowScript: '/fake/claude-bow.js' });
  assert.equal(summary.total, 0);
  assert.equal(summary.imported, 0);
  assert.equal(spawnStub.calls.length, 0);
});

test('a claude-bow.js invocation that exits non-zero leaves the record in processed/ with .processing marker and .error sidecar (orphan state)', () => {
  const inboxDir = mkTmpDir('devfb-inbox-');
  const processedDir = path.join(mkTmpDir('devfb-processed-'), 'processed');
  writeRecord(inboxDir, 'a.json', wellFormedRecord());

  const spawnStub = makeStubSpawn(false); // simulate claude-bow.js failing (e.g. DB down)
  const summary = runImport({ inboxDir, processedDir, spawnSyncFn: spawnStub, bowScript: '/fake/claude-bow.js' });

  assert.equal(summary.imported, 0);
  assert.equal(summary.failed, 1);
  // NEW DESIGN: file is in processed/ with .processing marker (orphan state)
  assert.equal(fs.existsSync(path.join(processedDir, 'a.json')), true, 'record must be in processed/ after move+add-fail');
  assert.equal(fs.existsSync(path.join(processedDir, 'a.json.processing')), true, '.processing marker must exist for recovery');
  assert.equal(fs.existsSync(path.join(processedDir, 'a.json.error')), true, 'error sidecar must be in processed/');
  assert.equal(fs.existsSync(path.join(inboxDir, 'a.json')), false, 'file must not be in inbox');
});

test('a transient claude-bow.js failure (orphan) followed by a later successful run recovers orphan via .processing marker, exactly once', () => {
  const inboxDir = mkTmpDir('devfb-inbox-');
  const processedDir = path.join(mkTmpDir('devfb-processed-'), 'processed');
  writeRecord(inboxDir, 'a.json', wellFormedRecord());

  // First run: move succeeds, add fails → orphan created
  const failingSpawn = makeStubSpawn(false);
  const firstSummary = runImport({ inboxDir, processedDir, spawnSyncFn: failingSpawn, bowScript: '/fake/claude-bow.js' });
  assert.equal(firstSummary.failed, 1);
  // File is now an orphan in processed/
  assert.equal(fs.existsSync(path.join(processedDir, 'a.json')), true);
  assert.equal(fs.existsSync(path.join(processedDir, 'a.json.processing')), true);
  assert.equal(fs.existsSync(path.join(processedDir, 'a.json.error')), true);

  // Second run: orphan recovery via .processing marker
  const succeedingSpawn = makeStubSpawn(true);
  const secondSummary = runImport({ inboxDir, processedDir, spawnSyncFn: succeedingSpawn, bowScript: '/fake/claude-bow.js' });
  assert.equal(secondSummary.imported, 1);
  // Exactly one add call in the second run (orphan recovery)
  assert.equal(succeedingSpawn.calls.length, 1, 'should call add exactly once for orphan recovery');
  // After recovery: .processing marker is cleared
  assert.equal(fs.existsSync(path.join(processedDir, 'a.json.processing')), false, '.processing marker must be cleared after recovery');
  assert.equal(fs.existsSync(path.join(processedDir, 'a.json')), true, 'recovered file stays in processed/');
});

// ── ASM-477: per-record source-mkey attribution ─────────────────────────

test('ASM-477: a record with sourceMkey "feat.metricsdash" imports with --codejson feat.metricsdash and a metricsdash-specific --code-path (not feat.devmode)', () => {
  const inboxDir = mkTmpDir('devfb-inbox-');
  const processedDir = path.join(mkTmpDir('devfb-processed-'), 'processed');
  writeRecord(inboxDir, 'a.json', wellFormedRecord({ sourceMkey: 'feat.metricsdash' }));

  const spawnStub = makeStubSpawn(true);
  const summary = runImport({ inboxDir, processedDir, spawnSyncFn: spawnStub, bowScript: '/fake/claude-bow.js' });

  assert.equal(summary.imported, 1);
  const args = spawnStub.calls[0].args;
  const codejsonIdx = args.indexOf('--codejson');
  assert.notEqual(codejsonIdx, -1);
  assert.equal(args[codejsonIdx + 1], 'feat.metricsdash', 'a FEAT-066/metricsdash-sourced note must attribute to feat.metricsdash, not feat.devmode');
  const codePathIdx = args.indexOf('--code-path');
  assert.ok(args[codePathIdx + 1].includes('metricsdash'), `--code-path should reference metricsdash, got ${args[codePathIdx + 1]}`);
});

test('ASM-477: a record with sourceMkey explicitly "feat.devmode" still imports with --codejson feat.devmode', () => {
  const inboxDir = mkTmpDir('devfb-inbox-');
  const processedDir = path.join(mkTmpDir('devfb-processed-'), 'processed');
  writeRecord(inboxDir, 'a.json', wellFormedRecord({ sourceMkey: 'feat.devmode' }));

  const spawnStub = makeStubSpawn(true);
  runImport({ inboxDir, processedDir, spawnSyncFn: spawnStub, bowScript: '/fake/claude-bow.js' });

  const args = spawnStub.calls[0].args;
  const codejsonIdx = args.indexOf('--codejson');
  assert.equal(args[codejsonIdx + 1], 'feat.devmode');
});

test('ASM-477 backward-compat: a record with NO sourceMkey field falls back to --codejson feat.devmode exactly as before', () => {
  const inboxDir = mkTmpDir('devfb-inbox-');
  const processedDir = path.join(mkTmpDir('devfb-processed-'), 'processed');
  const record = wellFormedRecord();
  assert.equal('sourceMkey' in record, false, 'fixture must genuinely lack the field for this to be a real backward-compat test');
  writeRecord(inboxDir, 'a.json', record);

  const spawnStub = makeStubSpawn(true);
  runImport({ inboxDir, processedDir, spawnSyncFn: spawnStub, bowScript: '/fake/claude-bow.js' });

  const args = spawnStub.calls[0].args;
  const codejsonIdx = args.indexOf('--codejson');
  assert.equal(args[codejsonIdx + 1], DEFAULT_SOURCE_MKEY);
  const codePathIdx = args.indexOf('--code-path');
  assert.ok(args[codePathIdx + 1].includes('devmode'));
});

test('validateRecord rejects a non-string sourceMkey', () => {
  const result = validateRecord(JSON.stringify(wellFormedRecord({ sourceMkey: 42 })));
  assert.equal(result.ok, false);
  assert.match(result.reason, /sourceMkey/);
});

test('deriveAttribution: unknown sourceMkey still derives a non-devmode code-path (a future writer attributes correctly without touching this file)', () => {
  const attribution = deriveAttribution({ sourceMkey: 'feat.somethingnew' }, '/default/code/path');
  assert.equal(attribution.codejson, 'feat.somethingnew');
  assert.ok(!attribution.codePath.includes('devmode'));
});

// ── BUG-126: per-record kind selects the claude-bow.js add verb ────────

test('BUG-126: a record with kind "finding" imports as a real `add finding` BOW item (not bug), with a --class flag supplied', () => {
  const inboxDir = mkTmpDir('devfb-inbox-');
  const processedDir = path.join(mkTmpDir('devfb-processed-'), 'processed');
  writeRecord(inboxDir, 'a.json', wellFormedRecord({ kind: 'finding' }));

  const spawnStub = makeStubSpawn(true);
  const summary = runImport({ inboxDir, processedDir, spawnSyncFn: spawnStub, bowScript: '/fake/claude-bow.js' });

  assert.equal(summary.imported, 1);
  const args = spawnStub.calls[0].args;
  assert.equal(args[1], 'add');
  assert.equal(args[2], 'finding', 'must file as finding, not bug');
  assert.ok(!args.includes('bug'), 'the literal "bug" verb must not appear anywhere in the invocation');
  assert.ok(args.includes('--class'), '`add finding` requires --class');
});

test('BUG-126: a record with kind "assumption" imports as a real `add assumption` BOW item (not bug)', () => {
  const inboxDir = mkTmpDir('devfb-inbox-');
  const processedDir = path.join(mkTmpDir('devfb-processed-'), 'processed');
  writeRecord(inboxDir, 'a.json', wellFormedRecord({ kind: 'assumption' }));

  const spawnStub = makeStubSpawn(true);
  const summary = runImport({ inboxDir, processedDir, spawnSyncFn: spawnStub, bowScript: '/fake/claude-bow.js' });

  assert.equal(summary.imported, 1);
  const args = spawnStub.calls[0].args;
  assert.equal(args[1], 'add');
  assert.equal(args[2], 'assumption', 'must file as assumption, not bug');
});

test('BUG-126 backward-compat: a record with NO kind field still imports as bug exactly as before', () => {
  const inboxDir = mkTmpDir('devfb-inbox-');
  const processedDir = path.join(mkTmpDir('devfb-processed-'), 'processed');
  const record = wellFormedRecord();
  assert.equal('kind' in record, false, 'fixture must genuinely lack the field for this to be a real backward-compat test');
  writeRecord(inboxDir, 'a.json', record);

  const spawnStub = makeStubSpawn(true);
  runImport({ inboxDir, processedDir, spawnSyncFn: spawnStub, bowScript: '/fake/claude-bow.js' });

  const args = spawnStub.calls[0].args;
  assert.equal(args[2], DEFAULT_KIND);
});

test('BUG-126: an unrecognized kind value falls back to bug rather than passing an unknown verb through to claude-bow.js', () => {
  const inboxDir = mkTmpDir('devfb-inbox-');
  const processedDir = path.join(mkTmpDir('devfb-processed-'), 'processed');
  writeRecord(inboxDir, 'a.json', wellFormedRecord({ kind: 'not-a-real-kind' }));

  const spawnStub = makeStubSpawn(true);
  runImport({ inboxDir, processedDir, spawnSyncFn: spawnStub, bowScript: '/fake/claude-bow.js' });

  const args = spawnStub.calls[0].args;
  assert.equal(args[2], 'bug');
});

test('validateRecord rejects a non-string kind', () => {
  const result = validateRecord(JSON.stringify(wellFormedRecord({ kind: 7 })));
  assert.equal(result.ok, false);
  assert.match(result.reason, /kind/);
});

test('deriveKind returns the valid kind unchanged, and falls back to "bug" for anything else', () => {
  assert.equal(deriveKind({ kind: 'finding' }), 'finding');
  assert.equal(deriveKind({ kind: 'assumption' }), 'assumption');
  assert.equal(deriveKind({ kind: 'garbage' }), 'bug');
  assert.equal(deriveKind({}), 'bug');
});

test('multiple concurrent-style submissions never interleave: two records, two distinct processed files, two distinct titles preserved', () => {
  const inboxDir = mkTmpDir('devfb-inbox-');
  const processedDir = path.join(mkTmpDir('devfb-processed-'), 'processed');
  writeRecord(inboxDir, 'one.json', wellFormedRecord({ correlationId: 'corr-1', body: 'first submission' }));
  writeRecord(inboxDir, 'two.json', wellFormedRecord({ correlationId: 'corr-2', body: 'second submission' }));

  const spawnStub = makeStubSpawn(true);
  const summary = runImport({ inboxDir, processedDir, spawnSyncFn: spawnStub, bowScript: '/fake/claude-bow.js' });

  assert.equal(summary.imported, 2);
  assert.equal(spawnStub.calls.length, 2);
  assert.equal(fs.existsSync(path.join(processedDir, 'one.json')), true);
  assert.equal(fs.existsSync(path.join(processedDir, 'two.json')), true);
  const oneTitle = spawnStub.calls.find(c => c.args.includes(path.join(processedDir, 'one.json')));
  const twoTitle = spawnStub.calls.find(c => c.args.includes(path.join(processedDir, 'two.json')));
  assert.ok(oneTitle && oneTitle.args.some(a => a.includes('first submission')));
  assert.ok(twoTitle && twoTitle.args.some(a => a.includes('second submission')));
});

// ── BUG-337: orphan-recoverable two-phase design with .processing marker ──
// The import operation uses move-then-add with a .processing marker to ensure
// crash safety: no failure sequence (move-fail, add-fail, rollback-fail) can
// create a silent loss. Orphans (files in processed/ with .processing marker)
// are discoverable and recoverable on next run, making silent loss impossible.

test('BUG-337 TRIPLE FAILURE: move succeeds, add fails → orphan is RECOVERED on next run via .processing marker (RED→GREEN)', () => {
  const inboxDir = mkTmpDir('devfb-inbox-');
  const processedDir = path.join(mkTmpDir('devfb-processed-'), 'processed');
  writeRecord(inboxDir, 'a.json', wellFormedRecord({ correlationId: 'corr-orphan-1' }));

  // First run: move succeeds, add fails → file is now an orphan
  const failingSpawn = makeStubSpawn(false);
  const firstSummary = runImport({ inboxDir, processedDir, spawnSyncFn: failingSpawn, bowScript: '/fake/claude-bow.js' });

  assert.equal(firstSummary.imported, 0);
  assert.equal(firstSummary.failed, 1);
  // RED: OLD CODE would lose the file here (or duplicate on next run).
  // GREEN: NEW CODE creates a .processing marker, making the file discoverable.
  // After failure: file should be in processed/ (move succeeded)
  assert.equal(fs.existsSync(path.join(processedDir, 'a.json')), true, 'orphaned file must be in processed/');
  // The .processing marker should exist, making the orphan discoverable
  assert.equal(fs.existsSync(path.join(processedDir, 'a.json.processing')), true, '.processing marker must exist for orphan recovery');
  // File should NOT be in inbox (it was moved out)
  assert.equal(fs.existsSync(path.join(inboxDir, 'a.json')), false);
  // Error sidecar should be in processed/ (where the file actually is)
  assert.equal(fs.existsSync(path.join(processedDir, 'a.json.error')), true, 'error sidecar must be in processed/');

  // Second run: scanner discovers .processing marker and retries add
  const succeedingSpawn = makeStubSpawn(true);
  const secondSummary = runImport({ inboxDir, processedDir, spawnSyncFn: succeedingSpawn, bowScript: '/fake/claude-bow.js' });

  // The orphan should be recovered (imported)
  assert.equal(secondSummary.imported, 1, 'orphan must be discovered and recovered');
  // Exactly one add call: the orphan recovery
  assert.equal(succeedingSpawn.calls.length, 1, 'should call add exactly once for orphan recovery');
  // The .processing marker should be removed after successful recovery
  assert.equal(fs.existsSync(path.join(processedDir, 'a.json.processing')), false, '.processing marker must be cleared after recovery');
  // File still in processed/
  assert.equal(fs.existsSync(path.join(processedDir, 'a.json')), true, 'recovered file stays in processed/');
});

test('BUG-337 partial-failure case: move fails — file stays in inbox, add never called, next run retries', () => {
  const inboxDir = mkTmpDir('devfb-inbox-');
  const processedDir = path.join(mkTmpDir('devfb-processed-'), 'processed');
  writeRecord(inboxDir, 'a.json', wellFormedRecord({ correlationId: 'corr-2' }));

  const spawnStub = makeStubSpawn(true);
  const summary = runImport({ inboxDir, processedDir: '/nonexistent/readonly/dir', spawnSyncFn: spawnStub, bowScript: '/fake/claude-bow.js' });

  // Move should fail
  assert.equal(summary.failed, 1);
  // Add should NEVER be called
  assert.equal(spawnStub.calls.length, 0, 'add must not be called if move fails');
  // File should still be in inbox
  assert.equal(fs.existsSync(path.join(inboxDir, 'a.json')), true, 'file must stay in inbox if move fails');
});

test('BUG-337 orphan recovery: if new submissions exist, orphan recovery defers to next run; next run with no new submissions recovers orphans', () => {
  const inboxDir = mkTmpDir('devfb-inbox-');
  const processedDir = path.join(mkTmpDir('devfb-processed-'), 'processed');

  // Simulate an orphan in processed/ with .processing marker and .error sidecar (failed add)
  // In reality, orphans come from failed adds (have .error) or succeeded-but-cleanup-failed (have .done).
  // A .processing-only orphan shouldn't exist in production; .error makes this realistic.
  fs.mkdirSync(processedDir, { recursive: true });
  writeRecord(processedDir, 'orphan.json', wellFormedRecord({ correlationId: 'corr-orphan-2', body: 'orphaned submission' }));
  fs.writeFileSync(path.join(processedDir, 'orphan.json.processing'), JSON.stringify({ movedAt: '2026-08-27T00:00:00Z' }), 'utf8');
  fs.writeFileSync(path.join(processedDir, 'orphan.json.error'), JSON.stringify({ reason: 'prior add failure' }), 'utf8');

  // Create a new submission in inbox/
  writeRecord(inboxDir, 'new.json', wellFormedRecord({ correlationId: 'corr-new', body: 'new submission' }));

  // First run: process new submission ONLY (Phase 2 defers because Phase 1 found new submissions)
  const spawnStub1 = makeStubSpawn(true);
  const summary1 = runImport({ inboxDir, processedDir, spawnSyncFn: spawnStub1, bowScript: '/fake/claude-bow.js' });

  assert.equal(summary1.imported, 1, 'first run imports new submission only, orphan recovery deferred');
  assert.equal(spawnStub1.calls.length, 1, 'first run calls add once (for new submission)');
  assert.equal(fs.existsSync(path.join(processedDir, 'orphan.json.processing')), true, 'orphan marker still exists (recovery deferred)');
  assert.equal(fs.existsSync(path.join(processedDir, 'new.json')), true, 'new submission moved to processed/');

  // Second run: now inbox is empty, so Phase 2 runs and recovers the orphan
  const spawnStub2 = makeStubSpawn(true);
  const summary2 = runImport({ inboxDir, processedDir, spawnSyncFn: spawnStub2, bowScript: '/fake/claude-bow.js' });

  assert.equal(summary2.imported, 1, 'second run recovers orphan');
  assert.equal(spawnStub2.calls.length, 1, 'second run calls add once (for orphan recovery)');
  assert.equal(fs.existsSync(path.join(processedDir, 'orphan.json.processing')), false, 'orphan marker cleared after recovery');
});

test('BUG-337 orphan with persistent add failure is NOT silently lost — .processing marker remains, retryable next run', () => {
  const inboxDir = mkTmpDir('devfb-inbox-');
  const processedDir = path.join(mkTmpDir('devfb-processed-'), 'processed');

  // Simulate an orphan that has already failed once
  fs.mkdirSync(processedDir, { recursive: true });
  writeRecord(processedDir, 'orphan.json', wellFormedRecord({ correlationId: 'corr-orphan-3' }));
  fs.writeFileSync(path.join(processedDir, 'orphan.json.processing'), JSON.stringify({ movedAt: '2026-08-27T00:00:00Z' }), 'utf8');
  fs.writeFileSync(path.join(processedDir, 'orphan.json.error'), JSON.stringify({ failedAt: '2026-08-27T00:00:00Z', reason: 'prior add failure' }), 'utf8');

  // Add fails again
  const failingSpawn = makeStubSpawn(false);
  const summary = runImport({ inboxDir, processedDir, spawnSyncFn: failingSpawn, bowScript: '/fake/claude-bow.js' });

  assert.equal(summary.failed, 1, 'orphan add failure must be recorded');
  // .processing marker should still exist (not removed on failure)
  assert.equal(fs.existsSync(path.join(processedDir, 'orphan.json.processing')), true, '.processing marker must remain on add failure');
  // .error sidecar should be updated
  assert.equal(fs.existsSync(path.join(processedDir, 'orphan.json.error')), true, 'error sidecar must exist');
});

test('BUG-337 stray .processing marker without record is cleaned up (defensive)', () => {
  const inboxDir = mkTmpDir('devfb-inbox-');
  const processedDir = path.join(mkTmpDir('devfb-processed-'), 'processed');

  // Create a .processing marker without the corresponding record
  fs.mkdirSync(processedDir, { recursive: true });
  fs.writeFileSync(path.join(processedDir, 'missing.json.processing'), JSON.stringify({ movedAt: '2026-08-27T00:00:00Z' }), 'utf8');

  const spawnStub = makeStubSpawn(true);
  const summary = runImport({ inboxDir, processedDir, spawnSyncFn: spawnStub, bowScript: '/fake/claude-bow.js' });

  // No add calls should be made
  assert.equal(spawnStub.calls.length, 0, 'should not call add for missing record');
  // The stray marker should be cleaned up
  assert.equal(fs.existsSync(path.join(processedDir, 'missing.json.processing')), false, 'stray marker must be cleaned up');
});

// ── BUG-337 round-2: marker-independent recovery holes ─────────────────────

test('BUG-337 HOLE#1: marker-write-fail + add-fail → recoverable by .error sidecar (RED→GREEN)', () => {
  const inboxDir = mkTmpDir('devfb-inbox-');
  const processedDir = path.join(mkTmpDir('devfb-processed-'), 'processed');
  writeRecord(inboxDir, 'a.json', wellFormedRecord({ correlationId: 'corr-marker-fail' }));

  // First run: move succeeds, marker-write fails (simulated by stubbing),
  // add fails. The file ends up in processed/ with .error sidecar but NO .processing marker.
  // RED: old marker-only scanner wouldn't find it (no *.processing file).
  // GREEN: new marker-independent scanner finds it via .error sidecar.

  let markWriteFailed = false;
  const spawnStub1 = makeStubSpawn(false);

  // Manually simulate: move + marker-write-fail + add-fail
  fs.mkdirSync(processedDir, { recursive: true });
  const recordPath = path.join(processedDir, 'a.json');
  fs.renameSync(path.join(inboxDir, 'a.json'), recordPath);
  // Marker write: simulated as failed (don't write it)
  markWriteFailed = true;
  // Call add (fails)
  const result = spawnStub1(process.execPath, ['fake', 'args'], {});
  // Write error sidecar (our code does this on add failure)
  fs.writeFileSync(recordPath + '.error', JSON.stringify({ reason: 'add failed' }), 'utf8');

  // After first run: processed/a.json, processed/a.json.error (NO .processing marker)
  assert.equal(fs.existsSync(recordPath), true, 'file in processed');
  assert.equal(fs.existsSync(recordPath + '.error'), true, '.error sidecar exists');
  assert.equal(fs.existsSync(recordPath + '.processing'), false, '.processing marker missing (write failed)');

  // Second run: scanner finds file via .error sidecar (not marker), recovers it
  const spawnStub2 = makeStubSpawn(true);
  const secondSummary = runImport({ inboxDir, processedDir, spawnSyncFn: spawnStub2, bowScript: '/fake/claude-bow.js' });

  // File should be recovered
  assert.equal(secondSummary.imported, 1, 'file with .error sidecar must be discovered and recovered');
  assert.equal(spawnStub2.calls.length, 1, 'should call add once for recovery');
  // Sidecars should be cleared on success
  assert.equal(fs.existsSync(recordPath + '.error'), false, '.error sidecar cleared after recovery');
});

test('BUG-337 HOLE#2: marker-cleanup-fail after add-success → .done prevents double-import (RED→GREEN)', () => {
  const inboxDir = mkTmpDir('devfb-inbox-');
  const processedDir = path.join(mkTmpDir('devfb-processed-'), 'processed');

  // BUG-337 round-3: the real problem is that add generates a NEW unique code
  // every call (e.g., BUG-999, then BUG-1000). If marker-cleanup fails and we
  // retry add without checking .done, we create a duplicate item.
  //
  // Simulate first import success: move → add → .done written → marker cleanup FAILS.
  // After first run: processed/a.json, processed/a.json.processing (NOT cleaned),
  // processed/a.json.done (success marker exists).
  fs.mkdirSync(processedDir, { recursive: true });
  const recordPath = path.join(processedDir, 'a.json');
  writeRecord(processedDir, 'a.json', wellFormedRecord({ correlationId: 'corr-double-import-test' }));
  fs.writeFileSync(recordPath + '.processing', JSON.stringify({ movedAt: '2026-08-27T00:00:00Z' }), 'utf8');
  // Simulate successful add: write .done with the first item's code
  fs.writeFileSync(recordPath + '.done', JSON.stringify({ completedAt: '2026-08-27T00:00:00Z', bowCode: 'BUG-999' }), 'utf8');

  // Second run: scanner sees .processing marker, but ALSO sees .done marker.
  // RED (without .done check): add would be retried → BUG-1000 created (duplicate)
  // GREEN (with .done check): add is NOT called, only cleanup happens (no duplicate)
  const spawnStub = makeStubSpawn(true);
  const summary = runImport({ inboxDir, processedDir, spawnSyncFn: spawnStub, bowScript: '/fake/claude-bow.js' });

  // CRITICAL: add should NOT be called at all (no retry because .done exists)
  assert.equal(spawnStub.calls.length, 0, 'add must NOT be called when .done exists (prevents double-import)');
  // The record was already imported in a prior run (.done exists), so it should
  // not be counted as a new import in this run (summary.imported counts NEW imports only).
  assert.equal(summary.imported, 0, 'record with prior .done should not be counted as NEW import');
  // Markers should be cleaned up (stale from prior run's failed cleanup)
  assert.equal(fs.existsSync(recordPath + '.processing'), false, '.processing marker must be cleared on .done recovery');
  assert.equal(fs.existsSync(recordPath + '.done'), false, '.done marker must be cleared on recovery');
  // The key insight: if add HAD been called, it would have generated BUG-1000,
  // a different code from the original BUG-999 in .done, causing a duplicate item
  // in the BOW. Our .done check prevents the retry entirely, preventing the double-import.
});

test('BUG-337 ROUND-4 WINDOW: .done-write-fail + cleanup-fail → no double-import (RED→GREEN)', () => {
  const inboxDir = mkTmpDir('devfb-inbox-');
  const processedDir = path.join(mkTmpDir('devfb-processed-'), 'processed');

  // BUG-337 round-4: the residual window is when add SUCCEEDS but .done-write FAILS.
  // After first import:
  // - add returns success
  // - .done-write fails (filesystem error)
  // - .processing-cleanup fails (file lock, permissions, etc.)
  // Result: processed/a.json with .processing marker, but NO .error and NO .done
  //
  // RED (without hasError guard): recovery sees .processing, retries add → BUG-1000 created
  // GREEN (with hasError guard): recovery sees "hasMarker && !hasError" → add succeeded,
  //        don't retry, just cleanup → only BUG-999 exists (no duplicate)
  fs.mkdirSync(processedDir, { recursive: true });
  const recordPath = path.join(processedDir, 'a.json');
  writeRecord(processedDir, 'a.json', wellFormedRecord({ correlationId: 'corr-done-write-fail' }));
  // Simulate: add succeeded (would have written .done, but didn't due to write failure)
  // and cleanup failed (marker still present). File has NO .error, NO .done, only .processing
  fs.writeFileSync(recordPath + '.processing', JSON.stringify({ movedAt: '2026-08-27T00:00:00Z' }), 'utf8');

  // Recovery: scanner sees .processing but NO .error → should NOT retry
  const spawnStub = makeStubSpawn(true);
  const summary = runImport({ inboxDir, processedDir, spawnSyncFn: spawnStub, bowScript: '/fake/claude-bow.js' });

  // CRITICAL: add must NOT be called (prevents double-import)
  assert.equal(spawnStub.calls.length, 0, 'add must NOT be called when hasMarker && !hasError (prevents double-import)');
  // Record is already in BOW from first run, so no new import count
  assert.equal(summary.imported, 0, '.processing-only record should not be re-added');
  // Marker should be cleaned
  assert.equal(fs.existsSync(recordPath + '.processing'), false, '.processing marker must be cleaned on recovery');
});
