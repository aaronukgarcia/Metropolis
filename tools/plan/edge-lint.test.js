/**
 * tools/plan/edge-lint.test.js — BUG-482 reverse-direction edge lint tests.
 *
 * Per dev-team-process.md ("an acceptance criterion's CHECK must be able to
 * fail") this proves three things:
 *
 *   (a) a real run against the live repo produces ONLY baselined findings
 *       (0 NEW) — the gate is green on trunk;
 *   (b) deleting a real, currently-registered edge from a SCRATCH COPY of
 *       code.json (never the real file — engine.finance->int.serializer,
 *       the FEAT-1972079941 pilot edge, real Go imports back it) makes
 *       EDGE-LINT-001 fire naming exactly that edge, and — the load-bearing
 *       proof the brief asks for — that finding is NOT in the shipped
 *       baseline, so it reports as NEW and would fail the gate;
 *   (c) a scratch Go file carrying a MISSPELT "(edge engine.typoo-> ...)"
 *       header annotation makes EDGE-LINT-002 fire, entirely independent of
 *       the real repo tree (no `go run` needed for this path — see
 *       edge-lint.js's header-annotation-is-astinfo-independent note).
 *
 * (b) never writes to the real code.json (opts.codeJsonPath points at a
 * temp-dir copy); (c) never writes inside the real repo tree at all (a
 * fully synthetic repoDir under os.tmpdir()). Every temp file/dir is
 * cleaned up in a `finally` block.
 *
 * Run: node tools/plan/edge-lint.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');

const ROOT = path.resolve(__dirname, '..', '..');
const {
  runLint, extractEdgeAnnotations, findingIdentity,
  loadBaseline, partitionAgainstBaseline,
} = require('./edge-lint.js');

const BASELINE_PATH = path.join(__dirname, 'edge-lint-baseline.json');

test('edge-lint: real repo run produces zero NEW findings (baseline covers all present findings)', () => {
  const result = runLint();
  const baseline = loadBaseline(BASELINE_PATH);
  const { newFindings, baselined } = partitionAgainstBaseline(result.findings, baseline);
  assert.equal(
    newFindings.length, 0,
    `edge-lint found ${newFindings.length} NEW (unbaselined) finding(s) against the live repo: ` +
    newFindings.map(f => f.detail || `${f.code} ${f.key}(${f.side})`).join(' | ')
  );
  // Sanity: the baseline is not vacuously "everything" — it names the exact
  // findings actually present today, no more, no less (proves the baseline
  // wasn't padded to silence unrelated drift).
  assert.equal(baselined.length, result.findings.length);
  assert.ok(result.dirsScanned > 0, 'expected at least one registered Go-tree directory to be scanned');
});

test('edge-lint: extractEdgeAnnotations parses the real repo header spelling, incl. the wrapped engine.finance form', () => {
  const oneLine = extractEdgeAnnotations(
    '// contract (edge engine.attract→int.serializer), following the pattern'
  );
  assert.deepEqual(oneLine.map(a => [a.from, a.to]), [['engine.attract', 'int.serializer']]);

  // The real engine.finance participant.go wraps the annotation across a
  // `//` comment line break.
  const wrapped = extractEdgeAnnotations(
    '// implement the save.Participant contract (edge engine.finance→\n' +
    '// int.serializer, registered a6293cb). This file is the serialization'
  );
  assert.deepEqual(wrapped.map(a => [a.from, a.to]), [['engine.finance', 'int.serializer']]);

  // Non-module-key prose ("(edge prereq -> dependent)") is filtered out by
  // the module-key-shape check upstream in runLint, but extractEdgeAnnotations
  // itself is a permissive extractor — prove it still returns the raw pair
  // so the shape check has something to reject.
  const prose = extractEdgeAnnotations('// depend on it (edge prereq -> dependent), built in order');
  assert.deepEqual(prose.map(a => [a.from, a.to]), [['prereq', 'dependent']]);
});

test('edge-lint: (b) RED PROOF — deleting a real registered edge from a scratch code.json fires EDGE-LINT-001 and is NOT in the shipped baseline', () => {
  const realCodeJson = JSON.parse(fs.readFileSync(path.join(ROOT, 'code.json'), 'utf8'));
  const financeModule = realCodeJson.modules.find(m => m.key === 'engine.finance');
  assert.ok(financeModule, 'precondition: engine.finance must exist in the real registry');
  const edgeIdx = financeModule.outbound.calls.findIndex(c => c.key === 'int.serializer');
  assert.ok(edgeIdx >= 0, 'precondition: engine.finance->int.serializer must be a real registered edge today (FEAT-1972079941 pilot)');

  // Mutate a SCRATCH COPY only — never the real code.json on disk.
  const scratchDir = fs.mkdtempSync(path.join(os.tmpdir(), 'edge-lint-scratch-'));
  const scratchPath = path.join(scratchDir, 'code.json');
  const mutated = JSON.parse(JSON.stringify(realCodeJson)); // deep clone, isolated from realCodeJson
  const mutatedFinance = mutated.modules.find(m => m.key === 'engine.finance');
  mutatedFinance.outbound.calls = mutatedFinance.outbound.calls.filter(c => c.key !== 'int.serializer');
  fs.writeFileSync(scratchPath, JSON.stringify(mutated, null, 2), 'utf8');

  try {
    const before = fs.readFileSync(path.join(ROOT, 'code.json'), 'utf8');

    const mutatedResult = runLint({ codeJsonPath: scratchPath });

    const after = fs.readFileSync(path.join(ROOT, 'code.json'), 'utf8');
    assert.equal(before, after, 'edge-lint must never write to the real code.json');

    const hit = mutatedResult.findings.find(
      f => f.code === 'EDGE-LINT-001' && f.from === 'engine.finance' && f.to === 'int.serializer'
    );
    assert.ok(
      hit,
      `expected EDGE-LINT-001 engine.finance->int.serializer after deleting the edge; got: ` +
      mutatedResult.findings.map(f => `${f.from || f.key}->${f.to || f.side}`).join(', ')
    );
    assert.match(hit.source, /^(import|header)$/);

    // Load-bearing: this exact finding is NOT part of the shipped baseline
    // (the real repo still HAS this edge), so against the real baseline it
    // reports NEW and would fail the gate.
    const baseline = loadBaseline(BASELINE_PATH);
    const { newFindings } = partitionAgainstBaseline(mutatedResult.findings, baseline);
    const newHit = newFindings.find(f => f.code === 'EDGE-LINT-001' && f.from === 'engine.finance' && f.to === 'int.serializer');
    assert.ok(newHit, 'the injected finding must be classified NEW (not silently absorbed by the baseline)');
  } finally {
    fs.rmSync(scratchDir, { recursive: true, force: true });
  }
});

test('edge-lint: (c) RED PROOF — a misspelt header module key fires EDGE-LINT-002, entirely in a synthetic scratch repo', () => {
  const scratchRoot = fs.mkdtempSync(path.join(os.tmpdir(), 'edge-lint-synthrepo-'));
  try {
    fs.writeFileSync(path.join(scratchRoot, 'go.mod'), 'module github.com/example/scratch\n\ngo 1.25\n', 'utf8');
    // Minimal code.json: no modules resolve on disk, so runLint's dirToKeys
    // map is empty and it never shells `go run` (see edge-lint.js's
    // header-annotation-is-astinfo-independent design note) — this test is
    // fully self-contained and needs no real Go toolchain interaction.
    fs.writeFileSync(path.join(scratchRoot, 'code.json'), JSON.stringify({ modules: [] }, null, 2), 'utf8');

    const pkgDir = path.join(scratchRoot, 'internal', 'engine', 'scratchmod');
    fs.mkdirSync(pkgDir, { recursive: true });
    fs.writeFileSync(
      path.join(pkgDir, 'participant.go'),
      'package scratchmod\n\n' +
      '// implement the save.Participant contract (edge engine.typoo->int.serializer)\n' +
      '// deliberately misspelt module key for the EDGE-LINT-002 red-proof test.\n' +
      'package scratchmod\n',
      'utf8'
    );

    const result = runLint({ repoDir: scratchRoot });

    const hit = result.findings.find(f => f.code === 'EDGE-LINT-002' && f.key === 'engine.typoo' && f.side === 'from');
    assert.ok(
      hit,
      `expected EDGE-LINT-002 for unregistered key "engine.typoo"; got: ` +
      result.findings.map(f => JSON.stringify(f)).join(' | ')
    );
    assert.match(hit.file, /participant\.go$/);

    // The baseline (built against the real repo) has no entry for this
    // synthetic key, so it too classifies as NEW.
    const baseline = loadBaseline(BASELINE_PATH);
    const { newFindings } = partitionAgainstBaseline(result.findings, baseline);
    assert.ok(newFindings.some(f => f.code === 'EDGE-LINT-002' && f.key === 'engine.typoo'));
  } finally {
    fs.rmSync(scratchRoot, { recursive: true, force: true });
  }
});

test('edge-lint: findingIdentity is stable and distinguishes code/side/source', () => {
  const a = { code: 'EDGE-LINT-001', from: 'engine.a', to: 'engine.b', source: 'import' };
  const b = { code: 'EDGE-LINT-001', from: 'engine.a', to: 'engine.b', source: 'header' };
  const c = { code: 'EDGE-LINT-002', key: 'engine.a', side: 'from' };
  assert.notEqual(findingIdentity(a), findingIdentity(b));
  assert.notEqual(findingIdentity(a), findingIdentity(c));
  assert.equal(findingIdentity(a), findingIdentity({ ...a }));
});

// ── Independent destructive round r1 (Opus, BUG-482) — attack regressions ───
// Added by the attacker, not the author. Each pins a behaviour an attack
// actually exercised, so a future refactor that silently loses it reddens.

/** Builds a fully synthetic repo (go.mod + code.json + Go files) under a temp
 * dir. `files` is a {relPath: contents} map. Returns the root. */
function makeSynthRepo(codeJson, files) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'edge-lint-attack-'));
  fs.writeFileSync(path.join(root, 'go.mod'), 'module example.com/synth\n\ngo 1.25\n', 'utf8');
  fs.writeFileSync(path.join(root, 'code.json'), JSON.stringify(codeJson, null, 1), 'utf8');
  for (const [rel, body] of Object.entries(files)) {
    const abs = path.join(root, rel);
    fs.mkdirSync(path.dirname(abs), { recursive: true });
    fs.writeFileSync(abs, body, 'utf8');
  }
  return root;
}

test('edge-lint r1 attack: a Windows backslash module path still resolves to its Go dir (no silent coverage loss)', () => {
  const BS = String.fromCharCode(92);
  const root = makeSynthRepo(
    {
      modules: [
        { key: 'engine.aaa', path: 'internal' + BS + 'engine' + BS + 'aaa', outbound: { calls: [{ key: 'engine.bbb' }] } },
        { key: 'engine.bbb', path: 'internal/engine/bbb', outbound: { calls: [] } },
      ],
    },
    {
      'internal/engine/aaa/a.go': 'package aaa\n',
      'internal/engine/bbb/b.go': 'package bbb\n',
    }
  );
  try {
    const r = runLint({ repoDir: root });
    // Both dirs must be registered/scanned — a backslash path silently
    // failing isGoTreePath() would drop that module's imports from the lint
    // entirely and the gate would go quiet without saying so.
    assert.equal(r.dirsScanned, 2, 'backslash module path must normalise to a scanned Go-tree dir');
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});

test('edge-lint r1 attack: a CRLF, line-wrapped header annotation whose edge IS declared produces no finding', () => {
  const root = makeSynthRepo(
    {
      modules: [
        { key: 'engine.aaa', path: 'internal/engine/aaa', outbound: { calls: [{ key: 'engine.bbb' }] } },
        { key: 'engine.bbb', path: 'internal/engine/bbb', outbound: { calls: [] } },
      ],
    },
    {
      // CRLF line endings + the wrapped "(edge A->\n// B, registered <sha>)"
      // form engine.finance actually uses.
      'internal/engine/aaa/a.go': '// participant (edge engine.aaa->\r\n// engine.bbb, registered abc1234)\r\npackage aaa\r\n',
      'internal/engine/bbb/b.go': 'package bbb\n',
    }
  );
  try {
    const r = runLint({ repoDir: root });
    assert.equal(r.headerAnnotationCount, 1, 'the wrapped CRLF annotation must be parsed, not skipped');
    assert.deepEqual(r.findings, [], 'a declared edge must not produce a finding');
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});

test('edge-lint r1 attack: "(edge A->B)" inside a _test.go file is never a finding source', () => {
  const root = makeSynthRepo(
    { modules: [{ key: 'engine.aaa', path: 'internal/engine/aaa', outbound: { calls: [] } }] },
    {
      'internal/engine/aaa/a.go': 'package aaa\n',
      'internal/engine/aaa/a_test.go': '// (edge engine.aaa->engine.nosuchkeyatall)\npackage aaa\n',
    }
  );
  try {
    const r = runLint({ repoDir: root });
    assert.equal(r.headerAnnotationCount, 0);
    assert.ok(
      !JSON.stringify(r.findings).includes('nosuchkeyatall'),
      'test-file annotations must be excluded from the header scan'
    );
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});

test('edge-lint r1 attack: co-located-key leniency is real and pinned — a feature key inherits its host module dir edge', () => {
  // This pins the CONTESTED semantic the r1 round reported as a follow-up
  // finding: when a module and one of its features share a package
  // directory, an import satisfied only by the MODULE key's outbound.calls
  // does NOT fire for the feature key. Whether that matches GR#25's per-key
  // intent is a lead decision; if it is ever tightened, this test must be
  // updated deliberately rather than the behaviour changing unnoticed.
  const root = makeSynthRepo(
    {
      modules: [
        // engine.aaa and feat.zzz share internal/engine/aaa; only engine.aaa
        // declares the edge to engine.bbb.
        { key: 'engine.aaa', path: 'internal/engine/aaa', outbound: { calls: [{ key: 'engine.bbb' }] } },
        { key: 'feat.zzz', path: 'internal/engine/aaa', outbound: { calls: [] } },
        { key: 'engine.bbb', path: 'internal/engine/bbb', outbound: { calls: [] } },
      ],
    },
    {
      'internal/engine/aaa/a.go': '// (edge feat.zzz->engine.bbb)\npackage aaa\n',
      'internal/engine/bbb/b.go': 'package bbb\n',
    }
  );
  try {
    const r = runLint({ repoDir: root });
    // The HEADER path is strictly per-key (it names feat.zzz explicitly), so
    // it DOES fire — proving the leniency is confined to the import path.
    const headerHit = r.findings.find(f => f.code === 'EDGE-LINT-001' && f.from === 'feat.zzz' && f.source === 'header');
    assert.ok(headerHit, 'a header annotation naming the feature key is checked per-key, with no module fallback');
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});
