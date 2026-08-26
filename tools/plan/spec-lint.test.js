/**
 * tools/plan/spec-lint.test.js — BUG-246 spec-lint repair-wave tests.
 *
 * Verification standard (dev-team-process.md v1.9 / metropolis-verification-
 * standards): a check that cannot fail is not a check. SPEC-LINT-002 shipped
 * with `goContent.includes(method)` — a whole-file substring match satisfied
 * by comments and longer identifiers — and had never produced a finding.
 * These tests prove the repaired, identifier-aware check:
 *   (a) FIRES on a synthetic violation (a spec citing a Go symbol the
 *       package only mentions in a comment / as part of a longer name), and
 *   (b) stays quiet on every real Go declaration form (func, method, type,
 *       grouped const block, interface method line).
 * Plus coverage for the BUG-246 fix-round-2 findings (Destructive-BUG246-r1):
 *   - SPEC-LINT-004 unregistered-key citations (finding 1)
 *   - filename≠key alias resolution via title line / code.json suffix map
 *     (finding 2)
 *   - EXEMPT_MODULES validated against the registry; dead entries dropped
 *     (finding 3)
 *   - relationship-level SPEC-LINT-001: either direction, either record
 *     (finding 4)
 *   - identifier-aware method-citation gating: stdlib/CamelCase tokens never
 *     enter the pipeline; registered modules always do (finding 5)
 *
 * Entirely self-contained: builds a synthetic repo tree in a temp dir; never
 * touches the live repo, code.json, or the DB. Run:
 *   node tools/plan/spec-lint.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');

const {
  runLint, loadRegistry, goContentExportsSymbol,
  effectiveExemptions, resolveCitedKey, edgeRegisteredEitherDirection,
  foldMkeyToken,
} = require('./spec-lint.js');

// ── unit: the identifier-aware symbol matcher ─────────────────────────────

test('SPEC-LINT-002 matcher: a comment mention alone does NOT satisfy the check (the old includes() bug)', () => {
  const go = [
    'package finance',
    '',
    '// DepositPool is documented here but never declared in this file.',
    'func SomethingElse() {}',
  ].join('\n');
  // Old code: go.includes('DepositPool') === true -> check could never fail.
  assert.equal(go.includes('DepositPool'), true, 'precondition: the OLD substring check would have passed this file');
  assert.equal(goContentExportsSymbol(go, 'DepositPool'), false, 'the new matcher must reject a comment-only mention');
});

test('SPEC-LINT-002 matcher: a longer identifier containing the name does NOT satisfy the check', () => {
  const go = [
    'package finance',
    'func DepositPoolLedgerRebuild() {}',
    'var x = DepositPoolLedger{}',
  ].join('\n');
  assert.equal(go.includes('DepositPool'), true, 'precondition: the OLD substring check would have passed this file');
  assert.equal(goContentExportsSymbol(go, 'DepositPool'), false, 'word boundaries must exclude longer identifiers');
});

test('SPEC-LINT-002 matcher: real Go declaration forms all satisfy the check', () => {
  const cases = [
    ['plain func', 'package p\nfunc DepositPool(amount int) error { return nil }'],
    ['generic func', 'package p\nfunc DepositPool[T any](v T) {}'],
    ['method decl', 'package p\nfunc (f *Finance) DepositPool(amount int) error { return nil }'],
    ['type decl', 'package p\ntype DepositPool struct{}'],
    ['single const', 'package p\nconst DepositPool = 1'],
    ['single var', 'package p\nvar DepositPool int'],
    ['grouped const block', 'package p\nconst (\n\tDepositPool Kind = "deposit"\n\tOther Kind = "other"\n)'],
    ['grouped var block', 'package p\nvar (\n\tDepositPool int64 = 30_000_000\n)'],
    ['interface method line', 'package p\ntype API interface {\n\tDepositPool(amount int) error\n}'],
    ['struct field line', 'package p\ntype Cmd struct {\n\tDepositPool uint64\n}'],
  ];
  for (const [name, src] of cases) {
    assert.equal(goContentExportsSymbol(src, 'DepositPool'), true, `declaration form "${name}" must satisfy the check`);
  }
});

// ── end-to-end: synthetic repo fixture ────────────────────────────────────

/** Builds a throwaway repo tree. Defaults:
 *   code.json with engine.testpkg (no edges) + engine.other (no edges)
 *   internal/engine/testpkg/api.go declaring RealMethod, comment-mentioning
 *     MissingMethod
 *   docs/planning/acceptance/engine.testpkg.md with the given body
 * Options:
 *   modules      — REPLACE the default module list
 *   extraModules — append to the default module list
 *   specName     — name of the primary spec file (default engine.testpkg.md)
 *   files        — { repoRelativePath: content } extra files to create
 * Returns { repoDir, cleanup }.
 */
function makeFixtureRepo(specBody, opts = {}) {
  const repoDir = fs.mkdtempSync(path.join(os.tmpdir(), 'spec-lint-fixture-'));
  const pkgDir = path.join(repoDir, 'internal', 'engine', 'testpkg');
  const acceptDir = path.join(repoDir, 'docs', 'planning', 'acceptance');
  fs.mkdirSync(pkgDir, { recursive: true });
  fs.mkdirSync(acceptDir, { recursive: true });
  const defaultModules = [
    {
      key: 'engine.testpkg', path: 'internal/engine/testpkg',
      inbound: { consumers: [] }, outbound: { calls: [] },
    },
    {
      key: 'engine.other', path: 'internal/engine/other',
      inbound: { consumers: [] }, outbound: { calls: [] },
    },
  ];
  const modules = (opts.modules || defaultModules).concat(opts.extraModules || []);
  fs.writeFileSync(path.join(repoDir, 'code.json'), JSON.stringify({ modules }, null, 2), 'utf8');
  fs.writeFileSync(path.join(pkgDir, 'api.go'), [
    'package testpkg',
    '',
    '// MissingMethod is mentioned in this comment but NEVER declared —',
    '// the old substring check counted this as the symbol existing.',
    'func (t *TestAPI) RealMethod(v int) error { return nil }',
    'type TestAPI struct{}',
    '',
  ].join('\n'), 'utf8');
  fs.writeFileSync(path.join(acceptDir, opts.specName || 'engine.testpkg.md'), specBody, 'utf8');
  for (const [rel, content] of Object.entries(opts.files || {})) {
    const abs = path.join(repoDir, rel);
    fs.mkdirSync(path.dirname(abs), { recursive: true });
    fs.writeFileSync(abs, content, 'utf8');
  }
  const cleanup = () => fs.rmSync(repoDir, { recursive: true, force: true });
  return { repoDir, cleanup };
}

const sink = () => {}; // silence lint output inside tests

test('SPEC-LINT-002 e2e: a spec citing a comment-only symbol produces a finding (the check CAN fail)', () => {
  const { repoDir, cleanup } = makeFixtureRepo(
    '# engine.testpkg\n\nAC-1: calling testpkg.MissingMethod persists the record.\n');
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    const errs = r.findingsByFile['engine.testpkg.md'] || [];
    assert.equal(r.totalErrors, 1, `expected exactly one finding, got: ${JSON.stringify(errs)}`);
    assert.ok(errs[0].includes('[SPEC-LINT-002]') && errs[0].includes('testpkg.MissingMethod'),
      `expected a SPEC-LINT-002 finding for testpkg.MissingMethod, got: ${errs[0]}`);
  } finally {
    cleanup();
  }
});

test('SPEC-LINT-002 e2e: a spec citing a genuinely declared method passes clean', () => {
  const { repoDir, cleanup } = makeFixtureRepo(
    '# engine.testpkg\n\nAC-1: calling testpkg.RealMethod persists the record.\n');
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    assert.equal(r.totalErrors, 0, `expected a clean pass, got: ${JSON.stringify(r.findingsByFile)}`);
  } finally {
    cleanup();
  }
});

test('SPEC-LINT-001 e2e: a sentence-terminal citation ("...engine.other.") is no longer skipped', () => {
  const { repoDir, cleanup } = makeFixtureRepo(
    '# engine.testpkg\n\nAC-1: this module depends on engine.other.\n');
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    const errs = r.findingsByFile['engine.testpkg.md'] || [];
    assert.equal(r.totalErrors, 1, `expected the trailing-dot citation to be linted, got: ${JSON.stringify(errs)}`);
    assert.ok(errs[0].includes('[SPEC-LINT-001]') && errs[0].includes('"engine.other"'),
      `expected a SPEC-LINT-001 finding for engine.other (dot stripped), got: ${errs[0]}`);
  } finally {
    cleanup();
  }
});

test('key prefixes are derived from code.json at runtime (GR#15), and unmapped real specs are surfaced', () => {
  const { repoDir, cleanup } = makeFixtureRepo('# engine.testpkg\n\nNo citations here.\n');
  try {
    const reg = loadRegistry(repoDir);
    assert.deepEqual(reg.keyPrefixes, ['engine'], 'prefix set must come from the fixture code.json, not a hardcoded list');
    // An unmapped file with a REAL prefix must be surfaced; a BUG-* one must not.
    const acceptDir = path.join(repoDir, 'docs', 'planning', 'acceptance');
    fs.writeFileSync(path.join(acceptDir, 'engine.ghostmodule.md'), '# unmapped real spec\n', 'utf8');
    fs.writeFileSync(path.join(acceptDir, 'BUG-999.md'), '# bug notes\n', 'utf8');
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    assert.deepEqual(r.unmappedRealSpecs, ['engine.ghostmodule.md']);
    assert.ok(r.unmappedFiles.includes('BUG-999.md'), 'BUG-* files still skip silently (correct), listed only as unmapped');
    assert.equal(r.totalErrors, 0);
  } finally {
    cleanup();
  }
});

// ── BUG-246 reject finding 1: SPEC-LINT-004 unregistered-key citations ─────

test('SPEC-LINT-004: citing a key registered NOWHERE (no ancestor either) produces a warning finding (used to pass silently)', () => {
  const { repoDir, cleanup } = makeFixtureRepo(
    '# engine.testpkg\n\nAC-1: hands the record to engine.ghost for reconciliation.\n');
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    assert.equal(r.totalErrors, 0, 'an unregistered key is a warning class, distinct from SPEC-LINT-001');
    const warns = r.warningsByFile['engine.testpkg.md'] || [];
    assert.equal(r.totalWarnings, 1, `expected exactly one warning, got: ${JSON.stringify(warns)}`);
    assert.ok(warns[0].includes('[SPEC-LINT-004]') && warns[0].includes('"engine.ghost"'),
      `expected a SPEC-LINT-004 warning for engine.ghost, got: ${warns[0]}`);
  } finally {
    cleanup();
  }
});

test('SPEC-LINT-004 controls: registered keys, file references (.md/.go), and bare prefixes never fire the class', () => {
  const { repoDir, cleanup } = makeFixtureRepo(
    '# engine.testpkg\n\nSee engine.other.md and engine.go for details.\n');
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    assert.equal(r.totalErrors, 0, `file references must not be treated as key citations: ${JSON.stringify(r.findingsByFile)}`);
    assert.equal(r.totalWarnings, 0, `file references must not warn either: ${JSON.stringify(r.warningsByFile)}`);
  } finally {
    cleanup();
  }
});

test('ancestor resolution: "engine.other.tick" resolves to registered ancestor engine.other and is edge-checked (SPEC-LINT-001, not -004)', () => {
  const { repoDir, cleanup } = makeFixtureRepo(
    '# engine.testpkg\n\nAC-1: invokes engine.other.tick every day.\n');
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    const errs = r.findingsByFile['engine.testpkg.md'] || [];
    assert.equal(r.totalErrors, 1, `expected one SPEC-LINT-001 via ancestor resolution, got: ${JSON.stringify(errs)}`);
    assert.ok(errs[0].includes('[SPEC-LINT-001]') && errs[0].includes('"engine.other"'));
    assert.equal(r.totalWarnings, 0, 'a resolvable ancestor must not ALSO fire SPEC-LINT-004');
  } finally {
    cleanup();
  }
});

test('resolveCitedKey unit: exact key, ancestor, unregistered, file ref, bare prefix', () => {
  const modulesByKey = { 'engine.other': {}, 'engine.other.sub': {} };
  assert.deepEqual(resolveCitedKey('engine.other', modulesByKey), { kind: 'key', key: 'engine.other' });
  assert.deepEqual(resolveCitedKey('engine.other.sub', modulesByKey), { kind: 'key', key: 'engine.other.sub' });
  assert.deepEqual(resolveCitedKey('engine.other.tick.', modulesByKey), { kind: 'key', key: 'engine.other' });
  assert.deepEqual(resolveCitedKey('engine.ghost', modulesByKey), { kind: 'unregistered', token: 'engine.ghost' });
  assert.deepEqual(resolveCitedKey('engine.other.md', modulesByKey), { kind: 'skip' });
  assert.deepEqual(resolveCitedKey('engine...', modulesByKey), { kind: 'skip' });
});

// ── BUG-246 reject finding 2: filename≠key alias resolution ────────────────

test('alias (suffix): feat.<name>.md is linted under the registered engine.<name> module (the feat.worklife class of skip)', () => {
  const { repoDir, cleanup } = makeFixtureRepo(
    // Primary spec renamed: feat.testpkg.md, registry key engine.testpkg.
    // Body contains a REAL violation so we can prove the file was linted.
    '# Acceptance criteria — feat.testpkg (FEAT-999)\n\nAC-1: depends on engine.other.\n',
    {
      specName: 'feat.testpkg.md',
      // A feat.* module must exist for "feat" to be in the derived prefix
      // namespace (GR#15 — prefixes come from the registry).
      extraModules: [{ key: 'feat.unrelated', path: 'internal/engine/unrelated', inbound: { consumers: [] }, outbound: { calls: [] } }],
    });
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    assert.deepEqual(r.aliasMappedFiles['feat.testpkg.md'], { key: 'engine.testpkg', via: 'suffix' },
      `expected feat.testpkg.md to alias-resolve to engine.testpkg, got: ${JSON.stringify(r.aliasMappedFiles)}`);
    const errs = r.findingsByFile['feat.testpkg.md'] || [];
    assert.equal(errs.length, 1, `the alias-mapped file must actually be LINTED (violation must fire), got: ${JSON.stringify(r.findingsByFile)}`);
    assert.ok(errs[0].includes('[SPEC-LINT-001]') && errs[0].includes('"engine.other"'));
    assert.ok(!r.unmappedRealSpecs.includes('feat.testpkg.md'), 'an alias-resolved file is no longer an unmapped real spec');
  } finally {
    cleanup();
  }
});

test('alias (title): a real-prefix file whose title cites exactly one registered key is linted under that key', () => {
  const { repoDir, cleanup } = makeFixtureRepo(
    '# engine.testpkg\n\nNo citations.\n',
    {
      files: {
        // engine.headless.md-style case: filename maps to nothing, title
        // names the owning module.
        'docs/planning/acceptance/engine.zzz.md':
          '# Acceptance criteria — engine.testpkg headless-run contract\n\nAC-1: depends on engine.other.\n',
      },
    });
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    assert.deepEqual(r.aliasMappedFiles['engine.zzz.md'], { key: 'engine.testpkg', via: 'title' });
    const errs = r.findingsByFile['engine.zzz.md'] || [];
    assert.equal(errs.length, 1, 'the title-resolved file must actually be linted');
    assert.ok(errs[0].includes('[SPEC-LINT-001]'));
  } finally {
    cleanup();
  }
});

test('alias guard: BUG-*/SEC-*/README files keep skipping even when their content cites a registered key', () => {
  const { repoDir, cleanup } = makeFixtureRepo(
    '# engine.testpkg\n\nNo citations.\n',
    {
      files: {
        'docs/planning/acceptance/BUG-998.md': '# engine.testpkg regression notes\n\nMentions engine.other freely.\n',
        'docs/planning/acceptance/README.md': '# engine.testpkg index\n\nMentions engine.other freely.\n',
      },
    });
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    assert.ok(r.unmappedFiles.includes('BUG-998.md') && r.unmappedFiles.includes('README.md'),
      'non-module files must stay skipped');
    assert.equal(r.totalErrors, 0, `no graph checks may run on skipped files: ${JSON.stringify(r.findingsByFile)}`);
    assert.equal(Object.keys(r.aliasMappedFiles).length, 0, 'alias resolution must never claim a BUG-*/README file');
  } finally {
    cleanup();
  }
});

// ── BUG-246 reject finding 3: EXEMPT_MODULES validated against registry ───

test('EXEMPT_MODULES: a declared+registered exemption suppresses SPEC-LINT-001; a DEAD declared entry masks nothing', () => {
  const { repoDir, cleanup } = makeFixtureRepo(
    // foundation.num (declared exempt AND registered here) — no edge, must pass.
    // foundation.serialize (declared exempt but NOT registered) — must fire
    // SPEC-LINT-004 like any unregistered key, proving the dead entry is inert.
    '# engine.testpkg\n\nAC-1: uses foundation.num helpers and foundation.serialize framing.\n',
    { extraModules: [{ key: 'foundation.num', path: 'internal/foundation/num', inbound: { consumers: [] }, outbound: { calls: [] } }] });
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    assert.equal(r.totalErrors, 0, `an effective exemption must suppress the edge check: ${JSON.stringify(r.findingsByFile)}`);
    const warns = r.warningsByFile['engine.testpkg.md'] || [];
    assert.equal(warns.length, 1, `expected exactly the dead-exemption citation to warn, got: ${JSON.stringify(warns)}`);
    assert.ok(warns[0].includes('[SPEC-LINT-004]') && warns[0].includes('"foundation.serialize"'),
      `a DEAD exemption entry must not mask its citation, got: ${warns[0]}`);
    assert.ok(r.deadExemptions.includes('foundation.serialize'), 'dead declared exemptions must be reported');
    assert.ok(!r.deadExemptions.includes('foundation.num'), 'a registered exemption is not dead');
  } finally {
    cleanup();
  }
});

test('effectiveExemptions unit: derives the live set from the registry and lists dead entries', () => {
  const { effective, dead } = effectiveExemptions({ 'foundation.num': {} }, new Set(['foundation.num', 'foundation.serialize']));
  assert.deepEqual([...effective], ['foundation.num']);
  assert.deepEqual(dead, ['foundation.serialize']);
});

// ── BUG-246 reject finding 4: relationship-level SPEC-LINT-001 semantics ───

function edgeFixtureModules({ outboundOnCiting = false, consumerOnCiting = false, outboundOnCited = false, consumerOnCited = false } = {}) {
  return [
    {
      key: 'engine.testpkg', path: 'internal/engine/testpkg',
      inbound: { consumers: consumerOnCiting ? [{ key: 'engine.other' }] : [] },
      outbound: { calls: outboundOnCiting ? [{ key: 'engine.other' }] : [] },
    },
    {
      key: 'engine.other', path: 'internal/engine/other',
      inbound: { consumers: consumerOnCited ? [{ key: 'engine.testpkg' }] : [] },
      outbound: { calls: outboundOnCited ? [{ key: 'engine.testpkg' }] : [] },
    },
  ];
}

test('SPEC-LINT-001 semantics: a relationship registered in EITHER direction on EITHER record passes; registered NOWHERE fails', () => {
  const spec = '# engine.testpkg\n\nAC-1: interacts with engine.other every tick.\n';
  const cases = [
    ['outbound edge on the citing module', { outboundOnCiting: true }, 0],
    ['consumer entry on the citing module (reverse direction)', { consumerOnCiting: true }, 0],
    // One-sided registry records — the old per-module-record union FAILED
    // both of these (it only read the citing module\'s own record):
    ['edge recorded ONLY on the cited module\'s consumers (one-sided forward)', { consumerOnCited: true }, 0],
    ['edge recorded ONLY on the cited module\'s outbound (one-sided reverse)', { outboundOnCited: true }, 0],
    ['relationship registered NOWHERE', {}, 1],
  ];
  for (const [name, shape, expectedErrors] of cases) {
    const { repoDir, cleanup } = makeFixtureRepo(spec, { modules: edgeFixtureModules(shape) });
    try {
      const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
      assert.equal(r.totalErrors, expectedErrors,
        `case "${name}": expected ${expectedErrors} error(s), got ${r.totalErrors}: ${JSON.stringify(r.findingsByFile)}`);
    } finally {
      cleanup();
    }
  }
});

test('edgeRegisteredEitherDirection unit: reads the combined edge set both ways', () => {
  const edges = new Set(['a|b']);
  assert.equal(edgeRegisteredEitherDirection(edges, 'a', 'b'), true);
  assert.equal(edgeRegisteredEitherDirection(edges, 'b', 'a'), true);
  assert.equal(edgeRegisteredEitherDirection(edges, 'a', 'c'), false);
});

test('loadRegistry: edges include BOTH record sides, so a one-sidedly-recorded registry still yields the edge', () => {
  const { repoDir, cleanup } = makeFixtureRepo('# engine.testpkg\n\nx\n', { modules: edgeFixtureModules({ consumerOnCited: true }) });
  try {
    const reg = loadRegistry(repoDir);
    assert.ok(reg.edges.has('engine.testpkg|engine.other'),
      'a consumer entry on the cited module must contribute the testpkg->other edge');
  } finally {
    cleanup();
  }
});

// ── BUG-246 reject finding 5: method-citation gating ──────────────────────

test('SPEC-LINT-003: stdlib idioms (time.Now, json.Marshal, sync.Mutex, errors.Is) never enter the pipeline — zero findings, zero warnings', () => {
  const { repoDir, cleanup } = makeFixtureRepo(
    '# engine.testpkg\n\nAC-1: stamps time.Now, encodes via json.Marshal, guards with sync.Mutex, matches errors.Is.\n');
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    assert.equal(r.totalErrors, 0, `stdlib tokens must not error: ${JSON.stringify(r.findingsByFile)}`);
    assert.equal(r.totalWarnings, 0, `stdlib tokens must not warn (the old code emitted SPEC-LINT-003 for each): ${JSON.stringify(r.warningsByFile)}`);
  } finally {
    cleanup();
  }
});

test('SPEC-LINT-003: a CamelCase tail ("Response.Payload") no longer contributes a bogus "esponse.Payload" citation', () => {
  const { repoDir, cleanup } = makeFixtureRepo(
    '# engine.testpkg\n\nAC-1: the Response.Payload field carries the delta.\n');
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    assert.equal(r.totalErrors + r.totalWarnings, 0,
      `CamelCase tails must never enter the pipeline: ${JSON.stringify({ e: r.findingsByFile, w: r.warningsByFile })}`);
  } finally {
    cleanup();
  }
});

test('SPEC-LINT-003: an unregistered, non-stdlib package token (ghostpkg.DoesNotExist) is plain prose — skipped, not warned', () => {
  const { repoDir, cleanup } = makeFixtureRepo(
    '# engine.testpkg\n\nAC-1: calls ghostpkg.DoesNotExist for the transfer.\n');
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    assert.equal(r.totalErrors + r.totalWarnings, 0,
      `a token matching no registered module and no stdlib name is not a citation: ${JSON.stringify({ e: r.findingsByFile, w: r.warningsByFile })}`);
  } finally {
    cleanup();
  }
});

test('SPEC-LINT-003 fires for a REGISTERED module package whose Go directory is not on disk yet (the check CAN fail)', () => {
  const { repoDir, cleanup } = makeFixtureRepo(
    '# engine.testpkg\n\nAC-1: calls unbuilt.Frobnicate for the transfer.\n',
    { extraModules: [{ key: 'engine.unbuilt', path: 'internal/engine/unbuilt', inbound: { consumers: [] }, outbound: { calls: [] } }] });
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    assert.equal(r.totalErrors, 0, 'unverifiable citations warn, never fail');
    const warns = r.warningsByFile['engine.testpkg.md'] || [];
    assert.equal(warns.length, 1, `expected exactly one warning, got: ${JSON.stringify(warns)}`);
    assert.ok(warns[0].includes('[SPEC-LINT-003]') && warns[0].includes('unbuilt.Frobnicate'),
      `expected a SPEC-LINT-003 warning for unbuilt.Frobnicate, got: ${warns[0]}`);
  } finally {
    cleanup();
  }
});

test('SPEC-LINT-003 fires for a registered key segment with no registered Go directory of that name', () => {
  const { repoDir, cleanup } = makeFixtureRepo(
    '# engine.testpkg\n\nAC-1: the nodetool.Rewire helper does the wiring.\n',
    // Registered module whose key ends in "nodetool" but whose path is NOT a
    // Go-tree dir named nodetool (a claude-*.js-style root tool).
    { extraModules: [{ key: 'engine.nodetool', path: 'tools/nodetool', inbound: { consumers: [] }, outbound: { calls: [] } }] });
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    assert.equal(r.totalErrors, 0);
    const warns = r.warningsByFile['engine.testpkg.md'] || [];
    assert.equal(warns.length, 1, `expected one unverifiable-package warning, got: ${JSON.stringify(warns)}`);
    assert.ok(warns[0].includes('[SPEC-LINT-003]') && warns[0].includes('nodetool.Rewire'));
  } finally {
    cleanup();
  }
});

// ── BUG-256: silent false-negative closures ───────────────────────────────
// Each class is proven able to FAIL (verification standard: a check that
// cannot fail is not a check). Fixture: engine.testpkg + engine.other, no
// edges registered — so any resolved citation of engine.other MUST produce a
// SPEC-LINT-001 error.

test('BUG-256(a) lock: an unknown mkey with a registered prefix flags SPEC-LINT-004, never silence', () => {
  const { repoDir, cleanup } = makeFixtureRepo(
    '# engine.testpkg\n\nAC-1: hands lift control to engine.hovercraft.\n');
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    const warns = r.warningsByFile['engine.testpkg.md'] || [];
    assert.equal(warns.length, 1, `expected exactly one warning, got: ${JSON.stringify(warns)}`);
    assert.ok(warns[0].includes('[SPEC-LINT-004]') && warns[0].includes('"engine.hovercraft"'),
      `unknown mkey must flag, got: ${warns[0]}`);
  } finally {
    cleanup();
  }
});

test('BUG-256(b): a hyphen compound no longer swallows the citation — "engine.other-owned" is edge-checked as engine.other', () => {
  const { repoDir, cleanup } = makeFixtureRepo(
    '# engine.testpkg\n\nAC-1: the engine.other-owned ledger is read nightly.\n');
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    const errs = r.findingsByFile['engine.testpkg.md'] || [];
    assert.equal(r.totalErrors, 1, `the swallowed citation must fire SPEC-LINT-001, got: ${JSON.stringify({ e: errs, w: r.warningsByFile })}`);
    assert.ok(errs[0].includes('[SPEC-LINT-001]') && errs[0].includes('"engine.other"'),
      `expected the compound to resolve to engine.other, got: ${errs[0]}`);
    assert.equal(r.totalWarnings, 0, 'a compound resolving to a registered head must not ALSO warn SPEC-LINT-004');
  } finally {
    cleanup();
  }
});

test('BUG-256(b): a hyphen compound whose head resolves to nothing still fires SPEC-LINT-004 citing the full token', () => {
  const { repoDir, cleanup } = makeFixtureRepo(
    '# engine.testpkg\n\nAC-1: the engine.ghost-backed cache is refreshed.\n');
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    assert.equal(r.totalErrors, 0);
    const warns = r.warningsByFile['engine.testpkg.md'] || [];
    assert.equal(warns.length, 1, `expected one SPEC-LINT-004, got: ${JSON.stringify(warns)}`);
    assert.ok(warns[0].includes('[SPEC-LINT-004]') && warns[0].includes('"engine.ghost-backed"'));
  } finally {
    cleanup();
  }
});

test('BUG-256(b) unit: resolveCitedKey trims hyphen tails and trailing separators', () => {
  const modulesByKey = { 'engine.other': {} };
  assert.deepEqual(resolveCitedKey('engine.other-owned', modulesByKey), { kind: 'key', key: 'engine.other' });
  assert.deepEqual(resolveCitedKey('engine.other-multi-part-tail', modulesByKey), { kind: 'key', key: 'engine.other' });
  assert.deepEqual(resolveCitedKey('engine.other-', modulesByKey), { kind: 'key', key: 'engine.other' });
  assert.deepEqual(resolveCitedKey('engine.ghost-backed', modulesByKey), { kind: 'unregistered', token: 'engine.ghost-backed' });
  // file references keep skipping even with a hyphen in the name
  assert.deepEqual(resolveCitedKey('engine.other-spec.md', modulesByKey), { kind: 'skip' });
});

test('BUG-256(c): a case-variant citation ("Engine.other") flags SPEC-LINT-005 AND still fails the edge check', () => {
  const { repoDir, cleanup } = makeFixtureRepo(
    '# engine.testpkg\n\nAC-1: Engine.other must be polled every tick.\n');
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    const errs = r.findingsByFile['engine.testpkg.md'] || [];
    const warns = r.warningsByFile['engine.testpkg.md'] || [];
    assert.equal(r.totalErrors, 1, `the disguised citation must still fire SPEC-LINT-001, got: ${JSON.stringify(errs)}`);
    assert.ok(errs[0].includes('[SPEC-LINT-001]') && errs[0].includes('"engine.other"'));
    assert.equal(warns.length, 1, `expected the SPEC-LINT-005 typo warning, got: ${JSON.stringify(warns)}`);
    assert.ok(warns[0].includes('[SPEC-LINT-005]') && warns[0].includes('"Engine.other"'),
      `expected a case-variant warning naming the raw token, got: ${warns[0]}`);
  } finally {
    cleanup();
  }
});

test('BUG-256(c): a case variant of an UNKNOWN key ("Engine.hovercraft") still flags — the double evasion is closed', () => {
  const { repoDir, cleanup } = makeFixtureRepo(
    '# engine.testpkg\n\nAC-1: Engine.hovercraft supplies lift.\n');
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    const warns = r.warningsByFile['engine.testpkg.md'] || [];
    assert.equal(warns.length, 1, `expected one SPEC-LINT-005, got: ${JSON.stringify(warns)}`);
    assert.ok(warns[0].includes('[SPEC-LINT-005]') && warns[0].includes('"Engine.hovercraft"')
      && warns[0].includes('matches no registered module key'),
      `expected a case-variant warning for the unknown key, got: ${warns[0]}`);
  } finally {
    cleanup();
  }
});

test('BUG-256(c): with the edge registered, a case variant is warning-only (no false SPEC-LINT-001)', () => {
  const { repoDir, cleanup } = makeFixtureRepo(
    '# engine.testpkg\n\nAC-1: Engine.other must be polled every tick.\n',
    { modules: edgeFixtureModules({ outboundOnCiting: true }) });
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    assert.equal(r.totalErrors, 0, `a registered edge must satisfy the disguised citation: ${JSON.stringify(r.findingsByFile)}`);
    assert.equal(r.totalWarnings, 1, 'the typo warning still fires');
  } finally {
    cleanup();
  }
});

test('BUG-256(c): unicode dot-lookalike (U+2024) and Cyrillic homoglyph citations are ERRORS and edge-checked', () => {
  for (const [name, body] of [
    ['one-dot-leader', '# engine.testpkg\n\nAC-1: cites engine․other daily.\n'],
    ['Cyrillic e homoglyph', '# engine.testpkg\n\nAC-1: cites еngine.other daily.\n'],
  ]) {
    const { repoDir, cleanup } = makeFixtureRepo(body);
    try {
      const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
      const errs = r.findingsByFile['engine.testpkg.md'] || [];
      assert.equal(r.totalErrors, 2, `${name}: expected SPEC-LINT-005 error + SPEC-LINT-001, got: ${JSON.stringify(errs)}`);
      assert.ok(errs.some(e => e.includes('[SPEC-LINT-005]') && e.includes('engine.other')),
        `${name}: expected a SPEC-LINT-005 lookalike error, got: ${JSON.stringify(errs)}`);
      assert.ok(errs.some(e => e.includes('[SPEC-LINT-001]') && e.includes('"engine.other"')),
        `${name}: the disguised citation must still be edge-checked, got: ${JSON.stringify(errs)}`);
    } finally {
      cleanup();
    }
  }
});

test('BUG-256 dedupe: plain + case-variant citations of the same key yield exactly ONE SPEC-LINT-001', () => {
  const { repoDir, cleanup } = makeFixtureRepo(
    '# engine.testpkg\n\nAC-1: depends on engine.other. AC-2: Engine.other is polled.\n');
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    const errs = (r.findingsByFile['engine.testpkg.md'] || []).filter(e => e.includes('[SPEC-LINT-001]'));
    assert.equal(errs.length, 1, `one 001 per cited key per file, got: ${JSON.stringify(errs)}`);
    assert.equal(r.totalWarnings, 1, 'the 005 typo warning still fires alongside');
  } finally {
    cleanup();
  }
});

test('BUG-256 false-positive controls: Go-symbol idioms, acronyms, codes, and file refs never fire SPEC-LINT-005', () => {
  const { repoDir, cleanup } = makeFixtureRepo(
    // engine.Frobnicate: Go-symbol form whose folded key is UNREGISTERED —
    // owned by CHECK 2, must not fire 005. Response.Payload / time.Now /
    // MET-E403 / FEAT-999 / Engine (bare) / Engine.other.md (file ref):
    // none are mkey citations.
    '# engine.testpkg\n\nAC-1: engine.Frobnicate wraps Response.Payload at time.Now; see MET-E403, FEAT-999, the Engine, and Engine.other.md.\n');
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    assert.equal(r.totalErrors + r.totalWarnings, 0,
      `no SPEC-LINT-005 storm on legitimate prose: ${JSON.stringify({ e: r.findingsByFile, w: r.warningsByFile })}`);
  } finally {
    cleanup();
  }
});

test('BUG-256: a Go-symbol-form token whose folded key IS registered ("engine.Other") still flags as a probable typo', () => {
  const { repoDir, cleanup } = makeFixtureRepo(
    '# engine.testpkg\n\nAC-1: engine.Other coordinates the handoff.\n');
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    const warns = r.warningsByFile['engine.testpkg.md'] || [];
    assert.ok(warns.some(w => w.includes('[SPEC-LINT-005]') && w.includes('"engine.Other"')),
      `expected a SPEC-LINT-005 warning, got: ${JSON.stringify(warns)}`);
    assert.equal(r.totalErrors, 1, 'and the folded citation is edge-checked (no edge in fixture)');
  } finally {
    cleanup();
  }
});

test('BUG-256 code-span rule: an unregistered-folding case variant in backticks is a Go identifier, not a typo (the Engine.self corpus class)', () => {
  const { repoDir, cleanup } = makeFixtureRepo(
    '# engine.testpkg\n\nAC-1: mirrors `Engine.self` and calls `Engine.Snapshot()` before save.\n\n```go\nEngine.RunCommandLoop(ctx)\n```\n');
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    assert.equal(r.totalErrors + r.totalWarnings, 0,
      `code-span Go identifiers must not fire SPEC-LINT-005: ${JSON.stringify({ e: r.findingsByFile, w: r.warningsByFile })}`);
  } finally {
    cleanup();
  }
});

test('BUG-256 code-span rule: a REGISTERED-folding case variant flags even inside backticks, and unicode lookalikes flag everywhere', () => {
  const { repoDir, cleanup } = makeFixtureRepo(
    '# engine.testpkg\n\nAC-1: reads `Engine.other` state; AC-2: cites `engine․other` too.\n');
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    const warns = r.warningsByFile['engine.testpkg.md'] || [];
    const errs = r.findingsByFile['engine.testpkg.md'] || [];
    assert.ok(warns.some(w => w.includes('[SPEC-LINT-005]') && w.includes('"Engine.other"')),
      `registered-key case variant must flag in a code span, got: ${JSON.stringify(warns)}`);
    assert.ok(errs.some(e => e.includes('[SPEC-LINT-005]') && e.includes('UNICODE')),
      `unicode lookalike must error even in a code span, got: ${JSON.stringify(errs)}`);
    assert.ok(errs.some(e => e.includes('[SPEC-LINT-001]') && e.includes('"engine.other"')),
      `the disguised citations must be edge-checked, got: ${JSON.stringify(errs)}`);
  } finally {
    cleanup();
  }
});

// BUG-256 r2: invisible/format character evasion regression tests.
// These prove the strip-before-fold fix catches zero-width characters that
// survive NFKC and would otherwise break the foldedMkeyShapeRe pattern.
// Invisible non-ASCII chars are classified as UNICODE LOOKALIKES (ERRORS),
// not warnings.

test('BUG-256 r2: ZWJ-embedded citation (U+200D zero-width joiner) flags SPEC-LINT-005 and edges', () => {
  const { repoDir, cleanup } = makeFixtureRepo(
    '# engine.testpkg\n\nAC-1: engine.oth‍er must be polled (ZWJ embedded in token).\n');
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    const errs = r.findingsByFile['engine.testpkg.md'] || [];
    assert.ok(errs.some(e => e.includes('[SPEC-LINT-005]') && e.includes('UNICODE') && e.includes('engine.oth')),
      `ZWJ-embedded token must flag SPEC-LINT-005 UNICODE LOOKALIKE error, got: ${JSON.stringify(errs)}`);
    assert.ok(errs.some(e => e.includes('[SPEC-LINT-001]') && e.includes('engine.other')),
      `invisible chars stripped, citation must be edge-checked, got: ${JSON.stringify(errs)}`);
  } finally {
    cleanup();
  }
});

test('BUG-256 r2: ZWSP-embedded citation (U+200B zero-width space) flags SPEC-LINT-005 and edges', () => {
  const { repoDir, cleanup } = makeFixtureRepo(
    '# engine.testpkg\n\nAC-1: engine.ot​her state (ZWSP embedded).\n');
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    const errs = r.findingsByFile['engine.testpkg.md'] || [];
    assert.ok(errs.some(e => e.includes('[SPEC-LINT-005]') && e.includes('UNICODE') && e.includes('engine.ot')),
      `ZWSP-embedded token must flag SPEC-LINT-005 UNICODE LOOKALIKE error, got: ${JSON.stringify(errs)}`);
    assert.ok(errs.some(e => e.includes('[SPEC-LINT-001]') && e.includes('engine.other')),
      `invisible chars stripped, citation must be edge-checked, got: ${JSON.stringify(errs)}`);
  } finally {
    cleanup();
  }
});

test('BUG-256 r2: soft-hyphen-embedded citation (U+00AD) flags SPEC-LINT-005 and edges', () => {
  const { repoDir, cleanup } = makeFixtureRepo(
    '# engine.testpkg\n\nAC-1: en­gine.other must work (soft hyphen in first segment).\n');
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    const errs = r.findingsByFile['engine.testpkg.md'] || [];
    assert.ok(errs.some(e => e.includes('[SPEC-LINT-005]') && e.includes('UNICODE')),
      `soft-hyphen-embedded token must flag SPEC-LINT-005 UNICODE LOOKALIKE error, got: ${JSON.stringify(errs)}`);
    assert.ok(errs.some(e => e.includes('[SPEC-LINT-001]') && e.includes('engine.other')),
      `invisible chars stripped, citation must be edge-checked, got: ${JSON.stringify(errs)}`);
  } finally {
    cleanup();
  }
});

test('BUG-256 r2: BOM-prefixed citation (U+FEFF) flags SPEC-LINT-005 and edges', () => {
  const { repoDir, cleanup } = makeFixtureRepo(
    '# engine.testpkg\n\nAC-1: ﻿engine.other coordinates (BOM at start).\n');
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    const errs = r.findingsByFile['engine.testpkg.md'] || [];
    assert.ok(errs.some(e => e.includes('[SPEC-LINT-005]') && e.includes('UNICODE')),
      `BOM-prefixed token must flag SPEC-LINT-005 UNICODE LOOKALIKE error, got: ${JSON.stringify(errs)}`);
    assert.ok(errs.some(e => e.includes('[SPEC-LINT-001]') && e.includes('engine.other')),
      `invisible chars stripped, citation must be edge-checked, got: ${JSON.stringify(errs)}`);
  } finally {
    cleanup();
  }
});

// BUG-256 r3: the r2 hand list (00AD/200B-200F/2060/FEFF) missed U+2028/U+2029
// (Zl/Zp line/paragraph separators) and U+202A-202E (directional overrides) —
// the r2 REJECT. r3 replaces the list with the full Cf category plus the two
// Zl/Zp separators, and adds the FAIL-CLOSED RESIDUE RULE so that any exotic
// character NO list ever named still produces a finding, never a silent pass.
// Payloads are built with String.fromCodePoint: the evasion chars must never
// appear as literals in this source file (U+2028/U+2029 are not even legal in
// JS regex literals — that is what killed the first r3 attempt mid-write).

test('BUG-256 r3 killshot: U+2028 LINE SEPARATOR embedded citation flags SPEC-LINT-005 and edges', () => {
  const body = '# engine.testpkg\n\nAC-1: engine.oth' + String.fromCodePoint(0x2028) + 'er must be polled (Zl separator embedded).\n';
  const { repoDir, cleanup } = makeFixtureRepo(body);
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    const errs = r.findingsByFile['engine.testpkg.md'] || [];
    assert.ok(errs.some(e => e.includes('[SPEC-LINT-005]') && e.includes('UNICODE')),
      `U+2028-embedded token must flag SPEC-LINT-005 UNICODE LOOKALIKE error, got: ${JSON.stringify(errs)}`);
    assert.ok(errs.some(e => e.includes('[SPEC-LINT-001]') && e.includes('engine.other')),
      `folded key engine.other must be edge-checked, got: ${JSON.stringify(errs)}`);
  } finally {
    cleanup();
  }
});

test('BUG-256 r3 killshot: U+2029 PARAGRAPH SEPARATOR embedded citation flags SPEC-LINT-005 and edges', () => {
  const body = '# engine.testpkg\n\nAC-1: engine.oth' + String.fromCodePoint(0x2029) + 'er state (Zp separator embedded).\n';
  const { repoDir, cleanup } = makeFixtureRepo(body);
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    const errs = r.findingsByFile['engine.testpkg.md'] || [];
    assert.ok(errs.some(e => e.includes('[SPEC-LINT-005]') && e.includes('UNICODE')),
      `U+2029-embedded token must flag SPEC-LINT-005 UNICODE LOOKALIKE error, got: ${JSON.stringify(errs)}`);
    assert.ok(errs.some(e => e.includes('[SPEC-LINT-001]') && e.includes('engine.other')),
      `folded key engine.other must be edge-checked, got: ${JSON.stringify(errs)}`);
  } finally {
    cleanup();
  }
});

test('BUG-256 r3 killshot: U+202E RTL OVERRIDE embedded citation flags SPEC-LINT-005 and edges', () => {
  const body = '# engine.testpkg\n\nAC-1: engine.oth' + String.fromCodePoint(0x202E) + 'er must work (directional override embedded).\n';
  const { repoDir, cleanup } = makeFixtureRepo(body);
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    const errs = r.findingsByFile['engine.testpkg.md'] || [];
    assert.ok(errs.some(e => e.includes('[SPEC-LINT-005]') && e.includes('UNICODE')),
      `U+202E-embedded token must flag SPEC-LINT-005 UNICODE LOOKALIKE error, got: ${JSON.stringify(errs)}`);
    assert.ok(errs.some(e => e.includes('[SPEC-LINT-001]') && e.includes('engine.other')),
      `folded key engine.other must be edge-checked, got: ${JSON.stringify(errs)}`);
  } finally {
    cleanup();
  }
});

test('BUG-256 r3 class closure: U+FFF9 INTERLINEAR ANNOTATION ANCHOR (never in any hand list; Cf) still flags', () => {
  // U+FFF9 was never on the r1/r2 strip lists. It is General_Category=Cf, so
  // the category-wide strip covers it with no list edit — proving the strip
  // is list-free for the whole Format category.
  const body = '# engine.testpkg\n\nAC-1: engine.oth' + String.fromCodePoint(0xFFF9) + 'er coordinates (exotic Cf char embedded).\n';
  const { repoDir, cleanup } = makeFixtureRepo(body);
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    const errs = r.findingsByFile['engine.testpkg.md'] || [];
    assert.ok(errs.some(e => e.includes('[SPEC-LINT-005]')),
      `U+FFF9-embedded token must flag SPEC-LINT-005, got: ${JSON.stringify(errs)}`);
    assert.ok(errs.some(e => e.includes('[SPEC-LINT-001]') && e.includes('engine.other')),
      `folded key engine.other must be edge-checked, got: ${JSON.stringify(errs)}`);
  } finally {
    cleanup();
  }
});

test('BUG-256 r3 class closure: U+FE00 VARIATION SELECTOR (not Cf, not Zl/Zp, NFKC-inert, unmapped) flags via the FAIL-CLOSED RESIDUE RULE', () => {
  // U+FE00 is category Mn: it survives the Cf/Zl/Zp strip, NFKC leaves it
  // unchanged, and the confusable map does not know it. Under r1/r2 semantics
  // it silently failed the folded-shape regex — a guaranteed silent pass for
  // a character no list ever named. The residue rule closes the CLASS: an
  // mkey-shaped candidate whose folded form still carries any char outside
  // [a-z0-9.-] is flagged fail-closed, and its residue-stripped key is
  // edge-checked. RED-proven: with the residue rule scratch-removed this
  // test fails (the token produces zero findings).
  const body = '# engine.testpkg\n\nAC-1: engine.oth' + String.fromCodePoint(0xFE00) + 'er must be polled (unmapped exotic char embedded).\n';
  const { repoDir, cleanup } = makeFixtureRepo(body);
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    const errs = r.findingsByFile['engine.testpkg.md'] || [];
    assert.ok(errs.some(e => e.includes('[SPEC-LINT-005]') && e.includes('MKEY RESIDUE') && e.includes('U+FE00')),
      `U+FE00-embedded token must flag the SPEC-LINT-005 MKEY RESIDUE fail-closed error naming the residue char, got: ${JSON.stringify(errs)}`);
    assert.ok(errs.some(e => e.includes('[SPEC-LINT-001]') && e.includes('engine.other')),
      `residue-stripped key engine.other must be edge-checked, got: ${JSON.stringify(errs)}`);
  } finally {
    cleanup();
  }
});

test('BUG-256 r3 residue carve-out: arrow-joined citation pairs (U+2194) are notation, not evasion — no 005, but every fragment is still edge-checked', () => {
  // Real-corpus shape ("engine.build" U+2194 "engine.season"): the evasion
  // tokenizer over-merges the pair into one token. The residue rule must NOT
  // flag it (every fragment is a well-formed citation — the exotic char sits
  // BETWEEN citations), but each fragment still routes through SPEC-LINT-001.
  const body = '# engine.testpkg\n\nAC-1: the engine.testpkg' + String.fromCodePoint(0x2194) + 'engine.other flow is bidirectional.\n';
  const { repoDir, cleanup } = makeFixtureRepo(body);
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    const errs = r.findingsByFile['engine.testpkg.md'] || [];
    assert.ok(!errs.some(e => e.includes('MKEY RESIDUE')),
      `arrow-joined citation pair must not flag MKEY RESIDUE, got: ${JSON.stringify(errs)}`);
    assert.ok(errs.some(e => e.includes('[SPEC-LINT-001]') && e.includes('"engine.other"')),
      `the arrow-joined fragment engine.other must still be edge-checked, got: ${JSON.stringify(errs)}`);
  } finally {
    cleanup();
  }
});

test('BUG-256 r3 foldMkeyToken unit: Zl/Zp separators and exotic Cf chars strip to canonical form; unmapped non-Cf residue survives for the residue rule', () => {
  assert.equal(foldMkeyToken('engine.oth' + String.fromCodePoint(0x2028) + 'er'), 'engine.other', 'U+2028 (Zl) stripped');
  assert.equal(foldMkeyToken('engine.oth' + String.fromCodePoint(0x2029) + 'er'), 'engine.other', 'U+2029 (Zp) stripped');
  assert.equal(foldMkeyToken('engine.oth' + String.fromCodePoint(0x202A) + 'er'), 'engine.other', 'U+202A (Cf, LRE) stripped');
  assert.equal(foldMkeyToken('engine.oth' + String.fromCodePoint(0x202E) + 'er'), 'engine.other', 'U+202E (Cf, RLO) stripped');
  assert.equal(foldMkeyToken('engine.oth' + String.fromCodePoint(0xFFF9) + 'er'), 'engine.other', 'U+FFF9 (Cf, never hand-listed) stripped');
  // Deliberate: FE00 must NOT be silently dropped by the fold — it is the
  // residue rule's job to turn it into a finding, keeping the fold honest.
  assert.equal(foldMkeyToken('engine.oth' + String.fromCodePoint(0xFE00) + 'er'),
    'engine.oth' + String.fromCodePoint(0xFE00) + 'er', 'U+FE00 (Mn) is residue, not silently stripped');
});

test('foldMkeyToken unit: NFKC compatibility forms, case, and confusables all fold to canonical mkey form', () => {
  assert.equal(foldMkeyToken('Engine.Other'), 'engine.other');
  assert.equal(foldMkeyToken('ENGINE.FINANCE'), 'engine.finance');
  assert.equal(foldMkeyToken('engine․other'), 'engine.other', 'U+2024 ONE DOT LEADER folds via NFKC');
  assert.equal(foldMkeyToken('engine．other'), 'engine.other', 'U+FF0E FULLWIDTH FULL STOP folds via NFKC');
  assert.equal(foldMkeyToken('еngine.other'), 'engine.other', 'Cyrillic е folds via the confusable map');
  assert.equal(foldMkeyToken('engine.оther'), 'engine.other', 'Cyrillic о folds via the confusable map');
  assert.equal(foldMkeyToken('engine.other...'), 'engine.other', 'trailing dots stripped');
});

// BUG-256 r2 regression tests: invisible/format characters must be stripped
// BEFORE shape-matching, else they survive NFKC and break regex patterns
test('foldMkeyToken unit: ZWJ-embedded mkey folds to canonical form', () => {
  // U+200D zero-width joiner: engine.oth[ZWJ]er should strip the ZWJ
  // and fold to engine.other, not remain as engine.oth[ZWJ]er
  assert.equal(foldMkeyToken('engine.oth‍er'), 'engine.other',
    'U+200D zero-width joiner stripped before shape-matching');
});

test('foldMkeyToken unit: ZWSP-embedded mkey folds to canonical form', () => {
  // U+200B zero-width space in the second segment
  assert.equal(foldMkeyToken('engine.ot​her'), 'engine.other',
    'U+200B zero-width space stripped before shape-matching');
});

test('foldMkeyToken unit: soft-hyphen-embedded mkey folds to canonical form', () => {
  // U+00AD soft hyphen (appears in the first segment)
  assert.equal(foldMkeyToken('en­gine.other'), 'engine.other',
    'U+00AD soft hyphen stripped before shape-matching');
});

test('foldMkeyToken unit: BOM-prefixed mkey folds to canonical form', () => {
  // U+FEFF zero-width no-break space / BOM at the start
  assert.equal(foldMkeyToken('﻿engine.other'), 'engine.other',
    'U+FEFF BOM/zero-width no-break space stripped before shape-matching');
});

test('gating precedence: a registered module path basename BEATS a stdlib name collision (e.g. "build"), and SPEC-LINT-002 still fires there', () => {
  const { repoDir, cleanup } = makeFixtureRepo(
    '# engine.testpkg\n\nAC-1: places the structure via build.PlaceStructure; build.GhostSymbol is never declared.\n',
    {
      extraModules: [{ key: 'engine.build', path: 'internal/engine/build', inbound: { consumers: [] }, outbound: { calls: [] } }],
      files: { 'internal/engine/build/api.go': 'package build\n\nfunc PlaceStructure(id int) error { return nil }\n' },
    });
  try {
    const r = runLint({ repoDir, log: sink, warn: sink, error: sink });
    const errs = r.findingsByFile['engine.testpkg.md'] || [];
    assert.equal(errs.length, 1, `"build" must be checked as the registered module, not skipped as go/build stdlib: ${JSON.stringify(r.findingsByFile)}`);
    assert.ok(errs[0].includes('[SPEC-LINT-002]') && errs[0].includes('build.GhostSymbol'),
      `expected only the undeclared symbol to fail, got: ${errs[0]}`);
  } finally {
    cleanup();
  }
});
