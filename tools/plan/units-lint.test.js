/**
 * tools/plan/units-lint.test.js — failing-first tests for the units registry lint.
 *
 * Verification standard (dev-team-process.md / metropolis-verification-
 * standards): a check that cannot fail is not a check. Each test proves the
 * corresponding units-lint check FIRES on a synthetic violation and stays
 * quiet on a clean registry:
 *
 *   (a) UNITS-LINT-001 unregistered unit — a source file using a unit token
 *       whose SPECIFIC unit key is unregistered is flagged, even when its
 *       dimension still has another registered unit (the F3 unit-level fix:
 *       the old dimension-level check could not catch this).
 *   (b) UNITS-LINT-002 stale definition — a registered unit whose `definedAt`
 *       points at a missing file / out-of-range line is flagged.
 *   (c) the CLEAN case — a registry covering every unit key used by a sample
 *       source file produces zero findings.
 *
 * Self-contained: builds a synthetic repo tree in a temp dir and points runLint
 * at it via opts.repoDir; never touches the live repo, code.json, or the DB.
 * Run: node tools/plan/units-lint.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');

const { runLint } = require('./units-lint.js');

function makeRepo(units, goFiles, dataFiles) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'units-lint-'));
  fs.mkdirSync(path.join(dir, 'internal', 'engine', 'demo'), { recursive: true });
  fs.mkdirSync(path.join(dir, 'data'), { recursive: true });
  fs.writeFileSync(path.join(dir, 'code.json'), JSON.stringify({ units: units || [] }, null, 2), 'utf8');
  for (const [rel, content] of Object.entries(goFiles || {})) {
    const p = path.join(dir, rel);
    fs.mkdirSync(path.dirname(p), { recursive: true });
    fs.writeFileSync(p, content, 'utf8');
  }
  for (const [rel, content] of Object.entries(dataFiles || {})) {
    fs.writeFileSync(path.join(dir, rel), content, 'utf8');
  }
  return dir;
}

// A registry where the `volume` dimension has TWO units, so removing one still
// leaves the dimension covered — the setup that exposes the old dimension-level
// false-negative (F3).
const FULL_UNITS = [
  { key: 'volume.litre', name: 'litre', symbol: 'L', dimension: 'volume' },
  { key: 'volume.cubic-metre', name: 'cubic metre', symbol: 'm³', dimension: 'volume' },
  { key: 'money.micropound', name: 'micro-pound', symbol: 'µ£', dimension: 'money', scale: 1, definedAt: 'internal/engine/demo/money.go:1' },
  { key: 'mass.tonne', name: 'tonne', symbol: 't', dimension: 'mass' },
];

// Only tokens whose keys are in FULL_UNITS (plus "Litres" for the volume probe).
const SAMPLE_GO = {
  'internal/engine/demo/money.go':
    'package demo\n// Litres of water and tonnes of goods.\n',
};

test('UNITS-LINT-001 FIRES unit-level: a token whose specific unit is unregistered, dimension still covered', () => {
  // Remove ONLY volume.litre; volume.cubic-metre remains, so the `volume`
  // dimension is still covered. The old dimension-level check would pass here.
  const units = FULL_UNITS.filter(u => u.key !== 'volume.litre');
  const dir = makeRepo(units, SAMPLE_GO, {});
  const res = runLint({ repoDir: dir });
  assert.ok(
    res.findings.some(f => f.code === 'UNITS-LINT-001' && f.key === 'volume.litre'),
    `expected a UNITS-LINT-001 finding for the missing key volume.litre; got ${JSON.stringify(res.findings)}`
  );
});

test('UNITS-LINT-002 FIRES: a definedAt pointing at a missing file is stale', () => {
  const units = FULL_UNITS.map(u => ({ ...u }));
  units[2].definedAt = 'internal/engine/demo/nonexistent.go:1';
  const dir = makeRepo(units, SAMPLE_GO, {});
  const res = runLint({ repoDir: dir });
  assert.ok(
    res.staleDefinitions.some(s => s.key === 'money.micropound'),
    `expected a stale-definition finding for money.micropound; got ${JSON.stringify(res.staleDefinitions)}`
  );
});

test('UNITS-LINT-002 FIRES: a definedAt line out of range is stale', () => {
  const units = FULL_UNITS.map(u => ({ ...u }));
  units[2].definedAt = 'internal/engine/demo/money.go:9999';
  const dir = makeRepo(units, SAMPLE_GO, {});
  const res = runLint({ repoDir: dir });
  assert.ok(
    res.staleDefinitions.some(s => s.key === 'money.micropound' && /out of range/.test(s.reason)),
    `expected an out-of-range stale-definition for money.micropound; got ${JSON.stringify(res.staleDefinitions)}`
  );
});

test('CLEAN registry covering every used unit key produces zero findings', () => {
  const dir = makeRepo(FULL_UNITS, SAMPLE_GO, {});
  const res = runLint({ repoDir: dir });
  assert.equal(res.totalErrors, 0, `expected zero findings, got ${JSON.stringify(res)}`);
});

test('UNITS-LINT-001 FIRES on a missing count.child (F5 regression — the fertility/defence "children" unit)', () => {
  // Registry has a covered `count` dimension (mass.tonne is unrelated but
  // keeps the dimension non-empty); count.child itself is absent.
  const units = FULL_UNITS.concat([{ key: 'count.pax', name: 'passenger', symbol: 'pax', dimension: 'count' }]);
  const go = {
    'internal/engine/demo/child.go': 'package demo\n// maxChildrenPerHousehold counts children.\n',
  };
  const dir = makeRepo(units, go, {});
  const res = runLint({ repoDir: dir });
  assert.ok(
    res.findings.some(f => f.code === 'UNITS-LINT-001' && f.key === 'count.child'),
    `expected a UNITS-LINT-001 finding for count.child; got ${JSON.stringify(res.findings)}`
  );
});

test('UNITS-LINT-001 FIRES on a missing count.case (round-3 regression — the money-rate denominator class)', () => {
  // Registry has a covered `count` dimension but count.case absent; a source
  // field using the PerCase denominator must fire.
  const units = FULL_UNITS.concat([{ key: 'count.pax', name: 'passenger', symbol: 'pax', dimension: 'count' }]);
  const go = {
    'internal/engine/demo/coastal.go': 'package demo\n// hotelCostPerCase micro-pounds per case.\n',
  };
  const dir = makeRepo(units, go, {});
  const res = runLint({ repoDir: dir });
  assert.ok(
    res.findings.some(f => f.code === 'UNITS-LINT-001' && f.key === 'count.case'),
    `expected a UNITS-LINT-001 finding for count.case; got ${JSON.stringify(res.findings)}`
  );
});
