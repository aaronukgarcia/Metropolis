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
  PROCESSING_SUFFIX,
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
function makeStubSpawn(succeed) {
  const calls = [];
  const fn = (execPath, args, opts) => {
    calls.push({ execPath, args, opts });
    if (succeed) {
      return { status: 0, stdout: 'Added BUG-999 [bug/P2] "stub"\nGUID: stub-guid\n', stderr: '' };
    }
    return { status: 1, stdout: '', stderr: 'simulated claude-bow.js failure' };
  };
  fn.calls = calls;
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

test('a claude-bow.js invocation that exits non-zero leaves the record in inbox/ with a .error sidecar, and is NOT moved to processed/', () => {
  const inboxDir = mkTmpDir('devfb-inbox-');
  const processedDir = path.join(mkTmpDir('devfb-processed-'), 'processed');
  writeRecord(inboxDir, 'a.json', wellFormedRecord());

  const spawnStub = makeStubSpawn(false); // simulate claude-bow.js failing (e.g. DB down)
  const summary = runImport({ inboxDir, processedDir, spawnSyncFn: spawnStub, bowScript: '/fake/claude-bow.js' });

  assert.equal(summary.imported, 0);
  assert.equal(summary.failed, 1);
  assert.equal(fs.existsSync(path.join(inboxDir, 'a.json')), true, 'record must stay in inbox/ on a bow-add failure');
  assert.equal(fs.existsSync(path.join(inboxDir, 'a.json.error')), true);
  assert.equal(fs.existsSync(path.join(processedDir, 'a.json')), false);
});

test('a transient claude-bow.js failure followed by a later successful run recovers and imports exactly once', () => {
  const inboxDir = mkTmpDir('devfb-inbox-');
  const processedDir = path.join(mkTmpDir('devfb-processed-'), 'processed');
  writeRecord(inboxDir, 'a.json', wellFormedRecord());

  const failingSpawn = makeStubSpawn(false);
  const firstSummary = runImport({ inboxDir, processedDir, spawnSyncFn: failingSpawn, bowScript: '/fake/claude-bow.js' });
  assert.equal(firstSummary.failed, 1);
  assert.equal(fs.existsSync(path.join(inboxDir, 'a.json.error')), true);

  const succeedingSpawn = makeStubSpawn(true);
  const secondSummary = runImport({ inboxDir, processedDir, spawnSyncFn: succeedingSpawn, bowScript: '/fake/claude-bow.js' });
  assert.equal(secondSummary.imported, 1);
  assert.equal(succeedingSpawn.calls.length, 1);
  assert.equal(fs.existsSync(path.join(processedDir, 'a.json')), true);
  assert.equal(fs.existsSync(path.join(inboxDir, 'a.json.error')), false, 'stale .error sidecar must be cleared on eventual success');
});

// ── ASM-766: post-success rename failure must NOT double-file on retry ──

test('ASM-766: a post-success move failure does not double-file the BOW item on retry (mark-then-move recovers, never re-adds)', () => {
  const inboxDir = mkTmpDir('devfb-inbox-');
  const processedDir = path.join(mkTmpDir('devfb-processed-'), 'processed');
  writeRecord(inboxDir, 'a.json', wellFormedRecord({ correlationId: 'corr-asm-766' }));

  const spawnStub = makeStubSpawn(true);
  // A rename stub that only fails the move-into-processed/ rename (the
  // post-success failure ASM-766 is about) — the mark rename
  // (inbox/a.json -> inbox/a.json.processing) and any rollback must still
  // work.
  let failMoves = true;
  const flakyRename = (src, dest) => {
    if (failMoves && String(dest).includes(processedDir)) {
      throw new Error('simulated post-success rename failure');
    }
    fs.renameSync(src, dest);
  };

  const first = runImport({
    inboxDir, processedDir, spawnSyncFn: spawnStub, bowScript: '/fake/claude-bow.js', renameFn: flakyRename,
  });
  assert.equal(first.imported, 0);
  assert.equal(first.failed, 1, 'the first run must report the post-success move failure');
  assert.equal(spawnStub.calls.length, 1, 'exactly one claude-bow.js add on the first run');
  // The record is now marked *.processing in the inbox, NOT a plain .json —
  // so a later run can tell it is already owned by an import.
  assert.equal(fs.existsSync(path.join(inboxDir, 'a.json')), false, 'plain record must no longer be in inbox/ after the mark');
  assert.equal(fs.existsSync(path.join(inboxDir, 'a.json' + PROCESSING_SUFFIX)), true, 'record must be marked *.processing, not re-importable');
  assert.equal(fs.existsSync(path.join(processedDir, 'a.json')), false, 'move failed, so nothing in processed/ yet');

  // Second run: the rename works now. The marked file must be recovered
  // into processed/ WITHOUT a second claude-bow.js add — the ASM-766
  // regression this test exists to pin.
  failMoves = false;
  const second = runImport({
    inboxDir, processedDir, spawnSyncFn: spawnStub, bowScript: '/fake/claude-bow.js', renameFn: flakyRename,
  });
  assert.equal(second.recovered, 1, 'the marked record must be recovered, not re-imported');
  assert.equal(second.imported, 0, 'no fresh import on the retry');
  assert.equal(spawnStub.calls.length, 1, 'NO duplicate claude-bow.js add on retry — this is the ASM-766 double-file regression');
  assert.equal(fs.existsSync(path.join(processedDir, 'a.json')), true, 'record ends up in processed/ on the recovery run');
  assert.equal(fs.existsSync(path.join(inboxDir, 'a.json' + PROCESSING_SUFFIX)), false, 'marked file must be gone after recovery');
});

test('ASM-766: a failed claude-bow.js add rolls the mark back so a later run re-attempts exactly once (no stuck *.processing, no duplicate)', () => {
  const inboxDir = mkTmpDir('devfb-inbox-');
  const processedDir = path.join(mkTmpDir('devfb-processed-'), 'processed');
  writeRecord(inboxDir, 'a.json', wellFormedRecord({ correlationId: 'corr-asm-766-rollback' }));

  const failingSpawn = makeStubSpawn(false); // claude-bow.js add fails (e.g. DB down)
  const firstSummary = runImport({ inboxDir, processedDir, spawnSyncFn: failingSpawn, bowScript: '/fake/claude-bow.js' });
  assert.equal(firstSummary.failed, 1);
  // The mark was rolled back: the record is a plain a.json again, NOT
  // *.processing, so a later run will re-attempt it.
  assert.equal(fs.existsSync(path.join(inboxDir, 'a.json')), true, 'record must be rolled back to a plain a.json after a bow-add failure');
  assert.equal(fs.existsSync(path.join(inboxDir, 'a.json' + PROCESSING_SUFFIX)), false, 'no *.processing file may be left behind by a failed add');
  assert.equal(fs.existsSync(path.join(inboxDir, 'a.json.error')), true, '.error sidecar still names the bow-add failure');

  const succeedingSpawn = makeStubSpawn(true);
  const secondSummary = runImport({ inboxDir, processedDir, spawnSyncFn: succeedingSpawn, bowScript: '/fake/claude-bow.js' });
  assert.equal(secondSummary.imported, 1);
  assert.equal(succeedingSpawn.calls.length, 1, 'exactly one add on the retry');
  assert.equal(fs.existsSync(path.join(processedDir, 'a.json')), true);
  assert.equal(fs.existsSync(path.join(inboxDir, 'a.json.error')), false, 'stale .error sidecar must be cleared on eventual success');
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
  // ASM-766 mark-then-move: --desc-file points at the *marked* path
  // (`one.json.processing`), which is still the record itself on disk.
  const oneTitle = spawnStub.calls.find(c => c.args.includes(path.join(inboxDir, 'one.json' + PROCESSING_SUFFIX)));
  const twoTitle = spawnStub.calls.find(c => c.args.includes(path.join(inboxDir, 'two.json' + PROCESSING_SUFFIX)));
  assert.ok(oneTitle && oneTitle.args.some(a => a.includes('first submission')));
  assert.ok(twoTitle && twoTitle.args.some(a => a.includes('second submission')));
});
