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
