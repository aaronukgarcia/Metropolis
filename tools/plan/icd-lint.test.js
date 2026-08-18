/**
 * tools/plan/icd-lint.test.js — Integration Engine Increment 4 (FEAT-190)
 * ICD lint harness tests.
 *
 * Verification standard (dev-team-process.md / metropolis-verification-
 * standards): a check that cannot fail is not a check. Every finding class
 * below has a paired "fires on a broken fixture" test AND a "stays quiet on
 * a well-formed fixture" test, so the CAN-FAIL proof is explicit, not
 * assumed. Entirely self-contained: builds a synthetic repo tree in a temp
 * dir per test; never touches the live repo, code.json, or the DB. Run:
 *   node --test tools/plan/icd-lint.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');

const {
  runLint, loadRegistry, lintIcdContent, splitSections, normalizeSectionName,
  extractGuid, extractMkey, extractUpdateClass, hasNoWallClockDeclaration,
  collectAllGuids, REQUIRED_SECTIONS,
} = require('./icd-lint.js');

const GOOD_GUID = '11111111-1111-1111-1111-111111111111';
const UNKNOWN_GUID = '99999999-9999-9999-9999-999999999999';

/** Builds a throwaway repo tree with a minimal code.json (one module,
 * key "engine.testpkg", carrying GOOD_GUID as its module guid) and a
 * docs/planning/icd/ directory. Returns { repoDir, icdDir, cleanup }. */
function makeFixtureRepo() {
  const repoDir = fs.mkdtempSync(path.join(os.tmpdir(), 'icd-lint-fixture-'));
  const icdDir = path.join(repoDir, 'docs', 'planning', 'icd');
  fs.mkdirSync(icdDir, { recursive: true });
  fs.writeFileSync(path.join(repoDir, 'code.json'), JSON.stringify({
    modules: [
      {
        guid: GOOD_GUID,
        key: 'engine.testpkg',
        path: 'internal/engine/testpkg',
        inbound: { guid: '22222222-2222-2222-2222-222222222222', consumers: [] },
        outbound: { guid: '33333333-3333-3333-3333-333333333333', calls: [] },
      },
    ],
  }, null, 2), 'utf8');
  const cleanup = () => fs.rmSync(repoDir, { recursive: true, force: true });
  return { repoDir, icdDir, cleanup };
}

/** A complete, well-formed ICD body — every required section present,
 * non-empty, with a valid GUID/mkey/update-class/no-wall-clock line. Used
 * as the baseline every "stays quiet" test starts from and every "fires"
 * test deliberately breaks one piece of. */
function goodIcdBody(overrides = {}) {
  const o = Object.assign({
    guidLine: `**GUID:** \`${GOOD_GUID}\``,
    mkeyLine: '**Owning module (mkey):** engine.testpkg',
    updateClassBody: 'T1 (batchable) — heavy sweep, cadence-driven.',
    determinismBody: 'Seeded per-shard stream, ascending-order merge. No wall-clock time is read anywhere in this integration (AC-20).',
  }, overrides);

  return [
    '# ICD: Test Integration',
    '',
    '## 1. Identity',
    `- ${o.guidLine}`,
    '- **Name:** `test.integration`',
    `- ${o.mkeyLine}`,
    '- **code.json edge ref(s):** NONE YET',
    '',
    '## 2. Purpose',
    'Does a thing for a reason.',
    '',
    '## 3. Inputs',
    'Reads shard-state X from module Y.',
    '',
    '## 4. Outputs',
    'Writes effect Z to stock W.',
    '',
    '## 5. Update Class',
    o.updateClassBody,
    '',
    '## 6. Shard Scope',
    'All shards; sharded fan-out inside the source module.',
    '',
    '## 7. Determinism Guarantee',
    o.determinismBody,
    '',
    '## 8. Error / Registry Codes',
    'MET-X001, MET-X002.',
    '',
    '## 9. Resilience Behaviour',
    'In-process, LocalReconnectHooks, no queue.',
    '',
    '## 10. Monitoring Signals',
    'Status via PhaseObserver; throughput via VitalEvents-style totals.',
    '',
    '## 11. Required Tests',
    'determinism_test.go, resilience_test.go, contract test, AC-coverage.',
    '',
    '## 12. Change Control',
    'Additive-only; version table below.',
    '',
    '| Version | Date | Change |',
    '|---|---|---|',
    '| v1 | 2026-08-18 | Initial draft |',
    '',
  ].join('\n');
}

function writeIcd(icdDir, filename, body) {
  fs.writeFileSync(path.join(icdDir, filename), body, 'utf8');
}

const sink = () => {}; // silence lint output inside tests

// ── unit: splitSections / normalizeSectionName ────────────────────────────

test('splitSections: parses numbered headings into normalized, trimmed sections; ### sub-headings stay nested', () => {
  const body = [
    '# Title',
    '## 1. Identity',
    'identity content',
    '### a sub-heading',
    'still identity content',
    '## 2. Purpose',
    'purpose content',
  ].join('\n');
  const sections = splitSections(body);
  assert.equal(sections['identity'], 'identity content\n### a sub-heading\nstill identity content');
  assert.equal(sections['purpose'], 'purpose content');
});

test('normalizeSectionName: strips ordinal prefix, lowercases, collapses whitespace', () => {
  assert.equal(normalizeSectionName('12.   Change   Control'), 'change control');
  assert.equal(normalizeSectionName('Error / Registry Codes'), 'error / registry codes');
});

// ── unit: field extractors ─────────────────────────────────────────────────

test('extractGuid: reads a backtick-quoted GUID; returns null when absent', () => {
  assert.equal(extractGuid(`- **GUID:** \`${GOOD_GUID}\``), GOOD_GUID);
  assert.equal(extractGuid('- **GUID:** <uuid>'), null);
  assert.equal(extractGuid(''), null);
});

test('extractMkey: reads a bare or backtick-quoted mkey; returns null when absent', () => {
  assert.equal(extractMkey('- **Owning module (mkey):** engine.testpkg'), 'engine.testpkg');
  assert.equal(extractMkey('- **Owning module (mkey):** `engine.testpkg`'), 'engine.testpkg');
  assert.equal(extractMkey('- **Owning module (mkey):** <engine.foo>'), 'engine.foo');
  assert.equal(extractMkey('no such line here'), null);
});

test('extractUpdateClass: recognises exactly one distinct T0/T1/T2 token; null on none or on conflicting tokens', () => {
  assert.equal(extractUpdateClass('T0 — critical every tick.'), 'T0');
  assert.equal(extractUpdateClass('This is T1 (batchable).'), 'T1');
  assert.equal(extractUpdateClass('no class token here'), null);
  assert.equal(extractUpdateClass('T0 or maybe T1, undecided'), null, 'two distinct tokens must not silently pick one');
  assert.equal(extractUpdateClass('T01x is not a valid token'), null, 'word-boundary match must reject a token embedded in a longer word');
});

test('hasNoWallClockDeclaration: matches "no wall-clock" and "no wall clock" case-insensitively; false otherwise', () => {
  assert.equal(hasNoWallClockDeclaration('No wall-clock time is read.'), true);
  assert.equal(hasNoWallClockDeclaration('NO WALL CLOCK anywhere.'), true);
  assert.equal(hasNoWallClockDeclaration('Fully seeded and deterministic.'), false);
  assert.equal(hasNoWallClockDeclaration(''), false);
});

test('collectAllGuids: finds every GUID-shaped string at any depth; ignores non-GUID strings', () => {
  const guids = collectAllGuids({ a: { guid: GOOD_GUID, list: [{ x: UNKNOWN_GUID }, 'not-a-guid'] } });
  assert.deepEqual([...guids].sort(), [GOOD_GUID, UNKNOWN_GUID].map(g => g.toLowerCase()).sort());
});

// ── e2e: the "can fail" proofs, one per finding class ──────────────────────

test('a fully well-formed ICD passes clean (the baseline every broken-fixture test deviates from)', () => {
  const { repoDir, icdDir, cleanup } = makeFixtureRepo();
  try {
    writeIcd(icdDir, 'engine.testpkg-integration.md', goodIcdBody());
    const r = runLint({ repoDir, log: sink, error: sink });
    assert.equal(r.totalErrors, 0, `expected a clean pass: ${JSON.stringify(r.findingsByFile)}`);
    assert.equal(r.filesChecked, 1);
  } finally {
    cleanup();
  }
});

test('ICD-LINT-001/002: a missing or emptied required section produces a finding (proves section-presence CAN fail)', () => {
  const { repoDir, icdDir, cleanup } = makeFixtureRepo();
  try {
    // Remove the "## 6. Shard Scope" section entirely.
    const broken = goodIcdBody().split('\n')
      .filter((line, i, arr) => {
        return true;
      })
      .join('\n')
      .replace(/## 6\. Shard Scope\nAll shards; sharded fan-out inside the source module\.\n\n/, '');
    writeIcd(icdDir, 'broken.md', broken);
    const r = runLint({ repoDir, log: sink, error: sink });
    const errs = r.findingsByFile['broken.md'] || [];
    assert.ok(r.totalErrors > 0, `expected a missing-section finding, got clean: ${JSON.stringify(r.findingsByFile)}`);
    assert.ok(errs.some(e => e.includes('[ICD-LINT-001]') && e.includes('Shard Scope')),
      `expected an ICD-LINT-001 finding for Shard Scope, got: ${JSON.stringify(errs)}`);
  } finally {
    cleanup();
  }
});

test('ICD-LINT-002: a present-but-empty section is distinguished from a missing one', () => {
  const { repoDir, icdDir, cleanup } = makeFixtureRepo();
  try {
    const broken = goodIcdBody().replace(
      '## 2. Purpose\nDoes a thing for a reason.\n',
      '## 2. Purpose\n\n'
    );
    writeIcd(icdDir, 'broken.md', broken);
    const r = runLint({ repoDir, log: sink, error: sink });
    const errs = r.findingsByFile['broken.md'] || [];
    assert.ok(errs.some(e => e.includes('[ICD-LINT-002]') && e.includes('Purpose')),
      `expected an ICD-LINT-002 (empty section) finding for Purpose, got: ${JSON.stringify(errs)}`);
  } finally {
    cleanup();
  }
});

test('ICD-LINT-003: a missing GUID line fires; a well-formed GUID line does not', () => {
  const { repoDir, icdDir, cleanup } = makeFixtureRepo();
  try {
    writeIcd(icdDir, 'broken.md', goodIcdBody({ guidLine: '**GUID:** (not yet assigned)' }));
    const r = runLint({ repoDir, log: sink, error: sink });
    const errs = r.findingsByFile['broken.md'] || [];
    assert.ok(errs.some(e => e.includes('[ICD-LINT-003]')), `expected ICD-LINT-003, got: ${JSON.stringify(errs)}`);
  } finally {
    cleanup();
  }
});

test('ICD-LINT-004: a GUID not present anywhere in code.json fires (proves the cross-reference check CAN fail)', () => {
  const { repoDir, icdDir, cleanup } = makeFixtureRepo();
  try {
    writeIcd(icdDir, 'broken.md', goodIcdBody({ guidLine: `**GUID:** \`${UNKNOWN_GUID}\`` }));
    const r = runLint({ repoDir, log: sink, error: sink });
    const errs = r.findingsByFile['broken.md'] || [];
    assert.ok(errs.some(e => e.includes('[ICD-LINT-004]') && e.includes(UNKNOWN_GUID)),
      `expected ICD-LINT-004 for the unregistered GUID, got: ${JSON.stringify(errs)}`);
  } finally {
    cleanup();
  }
});

test('ICD-LINT-004 control: the registered module GUID passes clean', () => {
  const { repoDir, icdDir, cleanup } = makeFixtureRepo();
  try {
    writeIcd(icdDir, 'ok.md', goodIcdBody());
    const r = runLint({ repoDir, log: sink, error: sink });
    assert.equal((r.findingsByFile['ok.md'] || []).filter(e => e.includes('ICD-LINT-004')).length, 0);
  } finally {
    cleanup();
  }
});

test('ICD-LINT-005/006: a missing or unregistered mkey fires; a registered mkey does not', () => {
  const { repoDir, icdDir, cleanup } = makeFixtureRepo();
  try {
    writeIcd(icdDir, 'missing-mkey.md', goodIcdBody({ mkeyLine: '**Owning module:** (unspecified)' }));
    writeIcd(icdDir, 'unknown-mkey.md', goodIcdBody({ mkeyLine: '**Owning module (mkey):** engine.doesnotexist' }));
    const r = runLint({ repoDir, log: sink, error: sink });

    const missingErrs = r.findingsByFile['missing-mkey.md'] || [];
    assert.ok(missingErrs.some(e => e.includes('[ICD-LINT-005]')), `expected ICD-LINT-005, got: ${JSON.stringify(missingErrs)}`);

    const unknownErrs = r.findingsByFile['unknown-mkey.md'] || [];
    assert.ok(unknownErrs.some(e => e.includes('[ICD-LINT-006]') && e.includes('engine.doesnotexist')),
      `expected ICD-LINT-006, got: ${JSON.stringify(unknownErrs)}`);
  } finally {
    cleanup();
  }
});

test('ICD-LINT-007: an update class section naming no T0/T1/T2 token, or two conflicting tokens, both fire', () => {
  const { repoDir, icdDir, cleanup } = makeFixtureRepo();
  try {
    writeIcd(icdDir, 'no-class.md', goodIcdBody({ updateClassBody: 'Whenever it feels like it.' }));
    writeIcd(icdDir, 'two-classes.md', goodIcdBody({ updateClassBody: 'Either T0 or T1, undecided.' }));
    const r = runLint({ repoDir, log: sink, error: sink });

    const noClassErrs = r.findingsByFile['no-class.md'] || [];
    assert.ok(noClassErrs.some(e => e.includes('[ICD-LINT-007]')), `expected ICD-LINT-007 for no class, got: ${JSON.stringify(noClassErrs)}`);

    const twoClassErrs = r.findingsByFile['two-classes.md'] || [];
    assert.ok(twoClassErrs.some(e => e.includes('[ICD-LINT-007]')), `expected ICD-LINT-007 for conflicting classes, got: ${JSON.stringify(twoClassErrs)}`);
  } finally {
    cleanup();
  }
});

test('ICD-LINT-007 control: T2 alone passes clean', () => {
  const { repoDir, icdDir, cleanup } = makeFixtureRepo();
  try {
    writeIcd(icdDir, 'ok.md', goodIcdBody({ updateClassBody: 'T2 — coalescible telemetry.' }));
    const r = runLint({ repoDir, log: sink, error: sink });
    assert.equal((r.findingsByFile['ok.md'] || []).filter(e => e.includes('ICD-LINT-007')).length, 0);
  } finally {
    cleanup();
  }
});

test('ICD-LINT-008: a Determinism Guarantee section without the no-wall-clock declaration fires (proves it CAN fail)', () => {
  const { repoDir, icdDir, cleanup } = makeFixtureRepo();
  try {
    writeIcd(icdDir, 'broken.md', goodIcdBody({ determinismBody: 'Seeded, deterministic, fixed merge order.' }));
    const r = runLint({ repoDir, log: sink, error: sink });
    const errs = r.findingsByFile['broken.md'] || [];
    assert.ok(errs.some(e => e.includes('[ICD-LINT-008]')), `expected ICD-LINT-008, got: ${JSON.stringify(errs)}`);
  } finally {
    cleanup();
  }
});

test('ICD-LINT-008 control: "no wall clock" (no hyphen) also satisfies the declaration', () => {
  const { repoDir, icdDir, cleanup } = makeFixtureRepo();
  try {
    writeIcd(icdDir, 'ok.md', goodIcdBody({ determinismBody: 'Seeded stream; no wall clock time is ever read.' }));
    const r = runLint({ repoDir, log: sink, error: sink });
    assert.equal((r.findingsByFile['ok.md'] || []).filter(e => e.includes('ICD-LINT-008')).length, 0);
  } finally {
    cleanup();
  }
});

// ── directory-level behaviour ───────────────────────────────────────────

test('TEMPLATE.md is skipped even if it would fail every check', () => {
  const { repoDir, icdDir, cleanup } = makeFixtureRepo();
  try {
    writeIcd(icdDir, 'TEMPLATE.md', '# not a real ICD at all\n\nno sections here.\n');
    const r = runLint({ repoDir, log: sink, error: sink });
    assert.equal(r.filesChecked, 0);
    assert.equal(r.totalErrors, 0);
    assert.ok(!('TEMPLATE.md' in r.findingsByFile));
  } finally {
    cleanup();
  }
});

test('a totally empty ICD file produces a finding for every one of the 12 required sections', () => {
  const { repoDir, icdDir, cleanup } = makeFixtureRepo();
  try {
    writeIcd(icdDir, 'empty.md', '# ICD: Nothing Here\n');
    const r = runLint({ repoDir, log: sink, error: sink });
    const errs = r.findingsByFile['empty.md'] || [];
    assert.equal(errs.filter(e => e.includes('[ICD-LINT-001]')).length, REQUIRED_SECTIONS.length,
      `expected one missing-section finding per required section, got: ${JSON.stringify(errs)}`);
  } finally {
    cleanup();
  }
});

test('no docs/planning/icd directory: runLint reports zero files checked rather than throwing', () => {
  const repoDir = fs.mkdtempSync(path.join(os.tmpdir(), 'icd-lint-nodir-'));
  fs.writeFileSync(path.join(repoDir, 'code.json'), JSON.stringify({ modules: [] }), 'utf8');
  try {
    const r = runLint({ repoDir, log: sink, error: sink });
    assert.equal(r.filesChecked, 0);
    assert.equal(r.totalErrors, 0);
  } finally {
    fs.rmSync(repoDir, { recursive: true, force: true });
  }
});

test('loadRegistry throws a clear error when code.json is missing', () => {
  const repoDir = fs.mkdtempSync(path.join(os.tmpdir(), 'icd-lint-nocodejson-'));
  try {
    assert.throws(() => loadRegistry(repoDir), /code\.json not found/);
  } finally {
    fs.rmSync(repoDir, { recursive: true, force: true });
  }
});

// ── the real reference ICD, against the LIVE repo's code.json ─────────────

test('the live reference ICD (docs/planning/icd/engine.citizens-coldpass.md) passes against the real repo code.json', () => {
  const repoDir = path.resolve(__dirname, '..', '..');
  const filePath = path.join(repoDir, 'docs', 'planning', 'icd', 'engine.citizens-coldpass.md');
  const content = fs.readFileSync(filePath, 'utf8');
  const registry = loadRegistry(repoDir);
  const errors = lintIcdContent(content, registry);
  assert.deepEqual(errors, [], `reference ICD must lint clean against the live repo: ${JSON.stringify(errors)}`);
});

test('the live TEMPLATE.md exists and is excluded from a full runLint pass over the real repo', () => {
  const repoDir = path.resolve(__dirname, '..', '..');
  const r = runLint({ repoDir, log: sink, error: sink });
  assert.ok(!('TEMPLATE.md' in r.findingsByFile), 'TEMPLATE.md must never be linted as a real ICD');
  assert.equal(r.totalErrors, 0, `expected a clean full pass over the live docs/planning/icd directory: ${JSON.stringify(r.findingsByFile)}`);
});
