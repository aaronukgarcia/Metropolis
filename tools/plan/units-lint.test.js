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

const { runLint, findMixing, VOCABULARY, collectSimTsFiles, EXTRA_DATA_DIRS } = require('./units-lint.js');

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

// ── FEAT-2326609803 (BUG-905 follow-up): pcu/lane/hour vs person/tick/tile ──

const MIXING_UNITS = FULL_UNITS.concat([
  { key: 'count.pcu-lane-hour', name: 'pcu per lane per hour', symbol: 'pcu/ln/h', dimension: 'count' },
  { key: 'count.person-tick-tile', name: 'people per tick per tile', symbol: 'ppl/tick/tile', dimension: 'count' },
]);

/** Write arbitrary extra files (e.g. the EXTRA_TS_FILES paths) into a repo dir built by makeRepo. */
function addFiles(dir, files) {
  for (const [rel, content] of Object.entries(files || {})) {
    const p = path.join(dir, rel);
    fs.mkdirSync(path.dirname(p), { recursive: true });
    fs.writeFileSync(p, content, 'utf8');
  }
}

test('UNITS-LINT-003 FIRES: raw pcu/lane/hour delta subtracted from a LineUsage.capacity value (BUG-905 shape)', () => {
  const dir = makeRepo(MIXING_UNITS, SAMPLE_GO, {});
  // Mirrors the actual BUG-905 arithmetic shape re-derived in trafficDemand.ts:
  // a pcu-per-lane-per-hour-derived delta, later subtracted directly from a
  // ROAD_TIER_CAPACITY-derived `.capacity` figure, with no conversion.
  addFiles(dir, {
    'webconsole/src/sim/trafficDemand.ts': [
      "function linkCapacityPerLane(id) { return 0; }",
      "const avenueCapPerLane = linkCapacityPerLane('avenue_2_plus_2');",
      "const busLaneCapPerLane = linkCapacityPerLane('bus_lane_variant');",
      "const requested = (avenueCapPerLane - busLaneCapPerLane) * 1;",
      "const avenueRawCapacity = lineUsageOf(s).find((u) => u.spec === busLaneSpec())?.capacity ?? 0;",
      "const delta = Math.max(0, Math.min(requested, avenueRawCapacity));",
      "const adjusted = u.capacity - delta;",
      '',
    ].join('\n'),
  });
  const res = runLint({ repoDir: dir });
  assert.ok(
    res.mixing.some(m => m.code === 'UNITS-LINT-003' && m.aKey === 'count.pcu-lane-hour' && m.bKey === 'count.person-tick-tile'),
    `expected a UNITS-LINT-003 mixing finding; got ${JSON.stringify(res.mixing)}`
  );
});

test('UNITS-LINT-003 QUIET: the shipped dimensionless-fraction fix (BUG-905 remediation shape) does not fire', () => {
  const dir = makeRepo(MIXING_UNITS, SAMPLE_GO, {});
  // Mirrors the shipped fix: the pcu figures only ever combine with EACH
  // OTHER (a same-unit ratio), and the ROAD_TIER_CAPACITY-derived capacity is
  // only ever multiplied by that dimensionless fraction — no pcu value is
  // ever added to or subtracted from a person-tick-tile value.
  addFiles(dir, {
    'webconsole/src/sim/trafficDemand.ts': [
      "function linkCapacityPerLane(id) { return 0; }",
      "const avenueCapPerLane = linkCapacityPerLane('avenue_2_plus_2');",
      "const busLaneCapPerLane = linkCapacityPerLane('bus_lane_variant');",
      "const laneShareFraction = (avenueCapPerLane - busLaneCapPerLane) / avenueCapPerLane;",
      "const requested = laneShareFraction * ROAD_TIER_CAPACITY[2] * 1;",
      "const avenueRawCapacity = lineUsageOf(s).find((u) => u.spec === busLaneSpec())?.capacity ?? 0;",
      "const delta = Math.max(0, Math.min(requested, avenueRawCapacity));",
      "const adjusted = u.capacity - delta;",
      '',
    ].join('\n'),
  });
  const res = runLint({ repoDir: dir });
  assert.equal(
    res.mixing.length, 0,
    `expected zero UNITS-LINT-003 findings on the fixed shape; got ${JSON.stringify(res.mixing)}`
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

// ── opus-round-feat803-units r1 REJECT (row 7642) — rework r2 fixes ─────────
// These replace the r1 DEFECT-PINs (which pinned the broken behaviour so the
// suite stayed green while red) with positive regression assertions proving
// the fix. Each still names its BOW code so a future revert is traceable.

test('BUG-979 FIXED: UNITS-LINT-003 fires on the plainest mixing form (raw token − raw token, no variables)', () => {
  const src = 'const bad = row.capacityPcuPerLanePerHour - ROAD_TIER_CAPACITY[2];\n';
  const res = findMixing(src, 'webconsole/src/sim/trafficDemand.ts');
  assert.ok(
    res.some(m => m.code === 'UNITS-LINT-003' && m.aKey === 'count.pcu-lane-hour' && m.bKey === 'count.person-tick-tile'),
    `expected the bare literal-token subtraction to fire; got ${JSON.stringify(res)}`
  );
  // Control: the same arithmetic routed through named variables still fires.
  const viaVars = [
    'const d = row.capacityPcuPerLanePerHour;',
    'const p = ROAD_TIER_CAPACITY[2];',
    'const bad2 = d - p;',
    '',
  ].join('\n');
  assert.ok(
    findMixing(viaVars, 'webconsole/src/sim/trafficDemand.ts').some(m => m.code === 'UNITS-LINT-003'),
    'the named-variable form must still fire'
  );
});

test('BUG-979 FIXED: UNITS-LINT-003 fires through one layer of parentheses and compound assignment', () => {
  const parens = 'const bad = row.capacityPcuPerLanePerHour - (ROAD_TIER_CAPACITY[2]);\n';
  assert.ok(
    findMixing(parens, 'webconsole/src/sim/trafficDemand.ts').some(m => m.code === 'UNITS-LINT-003'),
    'parenthesised operand must fire'
  );
  const compoundMinus = [
    'let total = ROAD_TIER_CAPACITY[2];',
    'total -= row.capacityPcuPerLanePerHour;',
    '',
  ].join('\n');
  assert.ok(
    findMixing(compoundMinus, 'webconsole/src/sim/trafficDemand.ts').some(m => m.code === 'UNITS-LINT-003'),
    '-= compound assignment must fire'
  );
  const compoundPlus = [
    'let total = row.capacityPcuPerLanePerHour;',
    'total += ROAD_TIER_CAPACITY[2];',
    '',
  ].join('\n');
  assert.ok(
    findMixing(compoundPlus, 'webconsole/src/sim/trafficDemand.ts').some(m => m.code === 'UNITS-LINT-003'),
    '+= compound assignment must fire'
  );
});

test('BUG-980 FIXED: no hand-typed EXTRA_TS_FILES allowlist — every webconsole/src/sim/*.ts is scanned', () => {
  const lintSrc = fs.readFileSync(path.join(__dirname, 'units-lint.js'), 'utf8');
  assert.equal(
    lintSrc.includes('EXTRA_TS_FILES'), false,
    'BUG-980 REGRESSION — a hand-typed EXTRA_TS_FILES allowlist has returned'
  );
  const files = collectSimTsFiles(path.resolve(__dirname, '..', '..'));
  assert.ok(files.includes('webconsole/src/sim/data.ts'));
  assert.ok(files.includes('webconsole/src/sim/trafficDemand.ts'));
  assert.ok(
    files.includes('webconsole/src/sim/trafficAssignment.ts'),
    'trafficAssignment.ts (the biggest capacityPcuPerLanePerHour consumer) must be scanned'
  );
  assert.ok(
    files.includes('webconsole/src/sim/engine.ts'),
    'engine.ts (a ROAD_TIER_CAPACITY consumer) must be scanned'
  );
});

test('BUG-980 mutant guard: collectSimTsFiles only lists files directly in webconsole/src/sim (flat, deterministic)', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'units-lint-simdir-'));
  fs.mkdirSync(path.join(dir, 'webconsole', 'src', 'sim', 'nested'), { recursive: true });
  fs.writeFileSync(path.join(dir, 'webconsole', 'src', 'sim', 'b.ts'), '', 'utf8');
  fs.writeFileSync(path.join(dir, 'webconsole', 'src', 'sim', 'a.ts'), '', 'utf8');
  fs.writeFileSync(path.join(dir, 'webconsole', 'src', 'sim', 'notes.txt'), '', 'utf8');
  fs.writeFileSync(path.join(dir, 'webconsole', 'src', 'sim', 'nested', 'c.ts'), '', 'utf8');
  const files = collectSimTsFiles(dir);
  assert.deepEqual(files, ['webconsole/src/sim/a.ts', 'webconsole/src/sim/b.ts'], `got ${JSON.stringify(files)}`);
});

test('BUG-981 PINNED: the two new VOCABULARY tokens resolve to the right keys', () => {
  const byToken = new Map(VOCABULARY.map(v => [v.token, v.key]));
  assert.equal(byToken.get('capacityPcuPerLanePerHour'), 'count.pcu-lane-hour');
  assert.equal(byToken.get('ROAD_TIER_CAPACITY'), 'count.person-tick-tile');
});

test('BUG-981 PINNED: EXTRA_DATA_DIRS still includes "traffic" (link_capacity.json lives there)', () => {
  assert.ok(Array.isArray(EXTRA_DATA_DIRS));
  assert.ok(EXTRA_DATA_DIRS.includes('traffic'));
  // Mutant proof: a registry+repo built without a traffic/ subdir scan finds
  // nothing there — demonstrating EXTRA_DATA_DIRS is what makes the file
  // reachable at all, not incidental to some other scan path.
  const dir = makeRepo(FULL_UNITS, {}, {});
  fs.mkdirSync(path.join(dir, 'data', 'traffic'), { recursive: true });
  fs.writeFileSync(path.join(dir, 'data', 'traffic', 'link_capacity.json'), '{"Litres": 1}', 'utf8');
  const res = runLint({ repoDir: dir });
  assert.ok(
    res.filesScanned.includes('data/traffic/link_capacity.json'),
    'EXTRA_DATA_DIRS must make data/traffic/*.json reachable to the scanner'
  );
});

test('BUG-982: a same-unit ratio (pcu/pcu) is untagged — division cancels the dimension', () => {
  // laneShareFraction = (avenueCapPerLane - busLaneCapPerLane) / avenueCapPerLane
  // both operands are pcu-per-lane-per-hour (group A); dividing A by A yields
  // a dimensionless fraction, so it must NOT still read as an 'A' value.
  const src = [
    "const avenueCapPerLane = linkCapacityPerLane('avenue_2_plus_2');",
    "const busLaneCapPerLane = linkCapacityPerLane('bus_lane_variant');",
    "const laneShareFraction = (avenueCapPerLane - busLaneCapPerLane) / avenueCapPerLane;",
    // If laneShareFraction were still tagged 'A', this bare subtraction
    // against a person-tick-tile value would (WRONGLY) fire.
    "const probe = ROAD_TIER_CAPACITY[2] - laneShareFraction;",
    '',
  ].join('\n');
  const res = findMixing(src, 'webconsole/src/sim/trafficDemand.ts');
  assert.equal(
    res.length, 0,
    `a dimensionless ratio must not be tagged into a mismatch; got ${JSON.stringify(res)}`
  );
});

test('BUG-982 GUARD: the shipped busPriorityCapacityInfoOf shape (ratio × ROAD_TIER_CAPACITY, min against raw capacity) never fires', () => {
  // The real production shape from webconsole/src/sim/trafficDemand.ts — a
  // pcu/pcu ratio applied to a fresh ROAD_TIER_CAPACITY figure, then clamped
  // via Math.min/Math.max against another person-tick-tile figure. No
  // pcu-vs-people arithmetic ever happens; this is the documented KNOWN
  // false-positive risk surface, pinned against the real shape so a future
  // change to findMixing that starts flagging it is caught immediately.
  const dir = makeRepo(MIXING_UNITS, SAMPLE_GO, {});
  addFiles(dir, {
    'webconsole/src/sim/trafficDemand.ts': [
      "function linkCapacityPerLane(id) { return 0; }",
      "const avenueCapPerLane = linkCapacityPerLane('avenue_2_plus_2');",
      "const busLaneCapPerLane = linkCapacityPerLane('bus_lane_variant');",
      "const laneShareFraction = avenueCapPerLane > 0 ? (avenueCapPerLane - busLaneCapPerLane) / avenueCapPerLane : 0;",
      "const avenuePerTileCapacity = ROAD_TIER_CAPACITY[2];",
      "const requested = laneShareFraction * avenuePerTileCapacity * 1;",
      "const avenueRawCapacity = lineUsageOf(s).find((u) => u.spec === busLaneSpec())?.capacity ?? 0;",
      "const clampCeiling = avenueRawCapacity * 0.5;",
      "const delta = Math.max(0, Math.min(requested, clampCeiling));",
      "const adjusted = u.capacity - delta;",
      '',
    ].join('\n'),
  });
  const res = runLint({ repoDir: dir });
  assert.equal(
    res.mixing.length, 0,
    `expected the shipped busPriorityCapacityInfoOf shape to stay quiet; got ${JSON.stringify(res.mixing)}`
  );
});

// ── opus-reround-feat803-units r2 REJECT (row 7647) — rework r3 fixes ───────
// The two DEFECT-PINs from r2 (which asserted the CURRENT, WRONG behaviour so
// the suite stayed green while the defect was open) are inverted here into
// positive regression assertions proving the fix, per the lead's r3
// amendment. The BUG-996 regression pin below is unchanged (it already
// asserted correct behaviour and continues to pass).

test('BUG-994 FIXED: UNITS-LINT-003 literal matching is direction-agnostic (all three orderings fire)', () => {
  // r2's pass-2 operand pattern only allowed a receiver prefix (`row.`) on
  // the LEFT operand and an index/member suffix (`[k]`) on the RIGHT
  // operand, so only one literal-to-literal ordering could ever fire. Real
  // code writes ROAD_TIER_CAPACITY (always indexed) and
  // capacityPcuPerLanePerHour (always receiver-prefixed) in BOTH orders, so
  // the mirror image and the un-aliased BUG-905 shape were both silently
  // missed. RECEIVER_PREFIX + MEMBER_SUFFIX are now applied to BOTH sides
  // (BUG-994 rework), so all three orderings below fire identically.
  const mirror = 'const bad = ROAD_TIER_CAPACITY[2] - linkCapacityRow(id).capacityPcuPerLanePerHour;\n';
  assert.ok(
    findMixing(mirror, 'webconsole/src/sim/trafficDemand.ts').some(m => m.code === 'UNITS-LINT-003'),
    'the mirror-image literal subtraction must fire'
  );
  // The un-aliased form of BUG-905's own arithmetic (indexed person-tick-tile
  // literal minus a tagged pcu variable) must fire too.
  const unaliased = [
    "const avenueCapPerLane = linkCapacityPerLane('avenue_2_plus_2');",
    'const bad2 = ROAD_TIER_CAPACITY[2] - avenueCapPerLane;',
    '',
  ].join('\n');
  assert.ok(
    findMixing(unaliased, 'webconsole/src/sim/trafficDemand.ts').some(m => m.code === 'UNITS-LINT-003'),
    'the un-aliased BUG-905 arithmetic must fire'
  );
  // The BUG-982 control probe is now a REAL, non-vacuous probe: `derived` is
  // definitely tagged 'A' (no division, so the ratio-cancel rule never
  // applies), so subtracting it from the indexed B literal must fire — this
  // is what makes the companion "BUG-982: ... is untagged" test's OWN probe
  // (`ROAD_TIER_CAPACITY[2] - laneShareFraction`, which stays quiet because
  // laneShareFraction genuinely IS a cancelled ratio, not because the
  // mixing scan can't see that ordering) a meaningful negative result.
  const taggedNotRatio = [
    "const avenueCapPerLane = linkCapacityPerLane('avenue_2_plus_2');",
    'const derived = avenueCapPerLane * 2;', // definitely tagged 'A', no division
    'const probe = ROAD_TIER_CAPACITY[2] - derived;',
    '',
  ].join('\n');
  assert.ok(
    findMixing(taggedNotRatio, 'webconsole/src/sim/trafficDemand.ts').some(m => m.code === 'UNITS-LINT-003'),
    'a tagged-and-not-a-ratio variable subtracted from the indexed B literal must fire'
  );
});

test('BUG-995 FIXED: UNITS-LINT-003 never fires on comment or string-literal text', () => {
  // findMixing now strips // and /* */ comments and string/template literal
  // bodies before either pass scans the text (stripCommentsAndStrings), so a
  // remediation comment or log line merely naming both tokens across a `-`
  // can never red the ci.yml lint job BUG-983 wired up.
  const commentOnly = '// BUG-905: we used to do row.capacityPcuPerLanePerHour - ROAD_TIER_CAPACITY[2] here\n';
  assert.equal(
    findMixing(commentOnly, 'webconsole/src/sim/trafficDemand.ts').length, 0,
    'a line comment naming both tokens must never fire'
  );
  const blockComment = '/* capacityPcuPerLanePerHour - ROAD_TIER_CAPACITY[2] */\n';
  assert.equal(
    findMixing(blockComment, 'webconsole/src/sim/trafficDemand.ts').length, 0,
    'a block comment naming both tokens must never fire'
  );
  const logLine = "console.log('capacityPcuPerLanePerHour - ROAD_TIER_CAPACITY[2] mismatch');\n";
  assert.equal(
    findMixing(logLine, 'webconsole/src/sim/trafficDemand.ts').length, 0,
    'a plain string literal naming both tokens must never fire'
  );
  const templateLine = '`capacityPcuPerLanePerHour - ROAD_TIER_CAPACITY[2]`;\n';
  assert.equal(
    findMixing(templateLine, 'webconsole/src/sim/trafficDemand.ts').length, 0,
    'a template literal naming both tokens must never fire'
  );
  // Control: the same shape as real CODE (not inside a comment/string) must
  // still fire, proving the strip pass targets comments/strings only.
  const realCode = 'const bad = row.capacityPcuPerLanePerHour - ROAD_TIER_CAPACITY[2];\n';
  assert.ok(
    findMixing(realCode, 'webconsole/src/sim/trafficDemand.ts').some(m => m.code === 'UNITS-LINT-003'),
    'the same shape as real code (not a comment/string) must still fire'
  );
});

test('BUG-997 FIXED: a multi-line declaration is tagged by joining continuation lines to the terminating ";"', () => {
  // r2's decl scan was per-line (`(.+)$`), so `const x =\n  a;` tagged
  // nothing — the RHS of the declaration line was empty. The real shipped
  // laneShareFraction declaration in trafficDemand.ts is exactly this shape
  // (a multi-line ternary), so it was NEVER tagged at all pre-fix; the
  // BUG-982 "ratio cancels the tag" rule was not what kept it quiet, an
  // unrelated false negative was. Prove the join works both ways: a
  // multi-line decl that SHOULD end up tagged (and then mixed) fires, and
  // the real multi-line ratio shape stays correctly quiet (not vacuously).
  const multiLineFires = [
    'const bad =',
    '  row.capacityPcuPerLanePerHour;',
    'const probe = ROAD_TIER_CAPACITY[2] - bad;',
    '',
  ].join('\n');
  assert.ok(
    findMixing(multiLineFires, 'webconsole/src/sim/trafficDemand.ts').some(m => m.code === 'UNITS-LINT-003'),
    'a multi-line declaration must still get tagged so downstream mixing fires'
  );

  // laneShareFraction: the REAL shape from webconsole/src/sim/trafficDemand.ts
  // (a multi-line ternary whose every branch is either a pcu/pcu ratio or a
  // plain override/zero). Must be tagged-and-quiet: tagged because it
  // references avenueCapPerLane/busLaneCapPerLane (both 'A'), quiet because
  // the RHS contains a `/` and every reference shares the 'A' tag (BUG-982's
  // ratio-cancel rule) — proven here NOT to be a false negative from the
  // decl-scan simply missing this declaration altogether (BUG-997's actual
  // finding), by then combining laneShareFraction with the B side and
  // confirming that ALSO stays quiet (it is untagged, not merely unseen).
  const laneShareFractionShape = [
    'const avenueCapPerLane = linkCapacityPerLane("avenue_2_plus_2");',
    'const busLaneCapPerLane = linkCapacityPerLane("bus_lane_variant");',
    'const laneShareFraction =',
    '  __busLaneShareFractionOverrideForTest !== null',
    '    ? __busLaneShareFractionOverrideForTest',
    '    : avenueCapPerLane > 0',
    '      ? (avenueCapPerLane - busLaneCapPerLane) / avenueCapPerLane',
    '      : 0;',
    'const avenuePerTileCapacity = ROAD_TIER_CAPACITY[2];',
    'const requested = laneShareFraction * avenuePerTileCapacity * 1;',
    '',
  ].join('\n');
  assert.equal(
    findMixing(laneShareFractionShape, 'webconsole/src/sim/trafficDemand.ts').length, 0,
    `expected the real multi-line laneShareFraction shape to stay quiet; got ${JSON.stringify(findMixing(laneShareFractionShape, 'x'))}`
  );
});

test('BUG-996 regression pin: CRLF input is normalised before declaration tagging', () => {
  // The only mutant of ten that survived the r2 round was removing
  // `text.replace(/\r\n/g, '\n')` from findMixing — JS `.` never matches CR,
  // so on a CRLF-checked-out file `(.+)$` reaches no declaration and the
  // named-variable route goes silently blind. (Every file in THIS repo is LF
  // — .gitattributes text=auto eol=lf — so no real-tree test covers it.)
  const lf = [
    'const d = row.capacityPcuPerLanePerHour;',
    'const p = ROAD_TIER_CAPACITY[2];',
    'const bad = d - p;',
    '',
  ].join('\n');
  const crlf = lf.replace(/\n/g, '\r\n');
  assert.ok(
    findMixing(lf, 'webconsole/src/sim/trafficDemand.ts').some(m => m.code === 'UNITS-LINT-003'),
    'the LF control must fire'
  );
  assert.ok(
    findMixing(crlf, 'webconsole/src/sim/trafficDemand.ts').some(m => m.code === 'UNITS-LINT-003'),
    'CRLF input must fire identically — findMixing must normalise line endings first'
  );
});

// ─── ROUND-3 DESTRUCTIVE PINS (opus-round3-feat803-units) ────────────────────
// Appended by the independent r3 attacker. Two kinds:
//   • REGRESSION pin — asserts correct behaviour the shipped code already has,
//     proven to go RED when the feature it names is removed.
//   • DEFECT-PIN — asserts the CURRENT, WRONG behaviour so the defect cannot
//     change silently. Each names its BOW code and says INVERT ON FIX.

test('r3 regression pin (BUG-997 discriminating): a tag token on a LATER continuation line is still tagged', () => {
  // The builder's own BUG-997 test does NOT discriminate: reverting declRe to
  // r2's per-line form `(.+)$` leaves the whole suite green, because `\s*=\s*`
  // eats the newline, so a decl whose ENTIRE rhs sits on the next line is
  // tagged either way, and the laneShareFraction assertion is "stays quiet",
  // which holds tagged or untagged. The join only matters when the tag token
  // sits on a continuation line that is NOT the first one — exactly the real
  // laneShareFraction shape (its first continuation line is the override test;
  // the pcu tokens are two lines further down). Proven RED with declRe reverted
  // to /\b(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*(.+)$/gm.
  const laterLineTag = [
    'const d =',
    '  flagForTest',
    '    ? row.capacityPcuPerLanePerHour',
    '    : 0;',
    'const probe = ROAD_TIER_CAPACITY[2] - d;',
    '',
  ].join('\n');
  assert.ok(
    findMixing(laterLineTag, 'webconsole/src/sim/trafficDemand.ts').some(m => m.code === 'UNITS-LINT-003'),
    'a tag token on a non-first continuation line must still tag the declaration'
  );
});

test('r3 DEFECT-PIN BUG-998: a declaration without a semicolon swallows the next decls and FALSELY fires', () => {
  // declRe reads `=` .. the next `;` ANYWHERE. `const innocent = Math.max(1, 2)`
  // with no semicolon (legal TS — the webconsole has no eslint/prettier
  // semicolon rule) swallows the FOLLOWING declaration, so `innocent` inherits
  // a person-tick-tile tag it never had and `innocent - pA` is reported as a
  // units mismatch. units-lint is a required CI lint job since BUG-983, so this
  // reds CI on code with no unit defect. INVERT ON FIX (BUG-998): terminate the
  // RHS at a `;` at brace/paren depth 0.
  const asi = [
    'const pA = row.capacityPcuPerLanePerHour;',
    'const innocent = Math.max(1, 2)',
    'const capB = ROAD_TIER_CAPACITY[2];',
    'const zFP = innocent - pA;',
    '',
  ].join('\n');
  assert.equal(
    findMixing(asi, 'webconsole/src/sim/trafficDemand.ts').length, 1,
    'DEFECT-PIN BUG-998: currently fires a FALSE POSITIVE on ASI code — invert to 0 when fixed'
  );
});

test('r3 DEFECT-PIN BUG-998: a function-body declaration tags the OUTER name and FALSELY fires', () => {
  // Same root cause, the realistic half: `const f = (n) => { const cap = …; }`
  // terminates at the first `;` INSIDE the arrow body, so `f` (a function) is
  // tagged from its body's internals. INVERT ON FIX (BUG-998).
  const arrowBody = [
    'const avenueCapPerLane = row.capacityPcuPerLanePerHour;',
    'const helperFn2 = (n: number) => { const capIn2 = ROAD_TIER_CAPACITY[2]; return capIn2 * n; };',
    'const zFP2 = helperFn2.length - avenueCapPerLane;',
    '',
  ].join('\n');
  assert.ok(
    findMixing(arrowBody, 'webconsole/src/sim/trafficDemand.ts').length > 0,
    'DEFECT-PIN BUG-998: currently fires on a subtraction involving a FUNCTION value — invert to 0 when fixed'
  );
});

test('r3 DEFECT-PIN BUG-999: a regex literal blinds real mixing (unbounded, not just its own line)', () => {
  // stripCommentsAndStrings has no regex-literal state. A regex whose body
  // contains an escaped-slash pair reads as a `//` line comment (rest of line
  // blanked); a regex whose character class contains a quote opens a phantom
  // string that blanks every character until the next matching quote, so the
  // blinding is NOT bounded to the offending line. Both are FALSE NEGATIVES on
  // real mixing. Inert on the real tree today (the 5 regex literals under
  // webconsole/src/sim carry neither shape). INVERT ON FIX (BUG-999).
  const sameLine = 'const re1 = /https?:\\/\\//; const bad = row.capacityPcuPerLanePerHour - ROAD_TIER_CAPACITY[2];\n';
  assert.equal(
    findMixing(sameLine, 'webconsole/src/sim/trafficDemand.ts').length, 0,
    'DEFECT-PIN BUG-999: escaped slashes in a regex currently blank the rest of the line — invert to 1 when fixed'
  );
  const quoteInClass = [
    "const re3 = /[a-z']+/;",
    'const bad2 = row.capacityPcuPerLanePerHour - ROAD_TIER_CAPACITY[2];',
    '',
  ].join('\n');
  assert.equal(
    findMixing(quoteInClass, 'webconsole/src/sim/trafficDemand.ts').length, 0,
    'DEFECT-PIN BUG-999: a quote inside a regex currently blanks following lines — invert to 1 when fixed'
  );
  // Controls that MUST keep working — these are what make the pin meaningful:
  // a block-comment opener inside a regex, one inside a string, a template
  // literal with a nested interpolation containing quotes, and an apostrophe
  // inside a line comment all leave real mixing visible.
  const controls = [
    'const re5 = /\\/\\*/;',
    'const s6 = "/*";',
    'const t7 = `x ${cond ? \'a\' : \'b\'} y`;',
    "// don't do this any more",
    'const bad3 = row.capacityPcuPerLanePerHour - ROAD_TIER_CAPACITY[2];',
    '',
  ].join('\n');
  assert.ok(
    findMixing(controls, 'webconsole/src/sim/trafficDemand.ts').some(m => m.code === 'UNITS-LINT-003'),
    'controls: block-comment openers inside a regex/string, a nested template interpolation and an apostrophe in a comment must NOT blind the scan'
  );
});

test('r3 DEFECT-PIN BUG-1000: the CRLF normalisation is inert — LF and CRLF agree with or without it', () => {
  // The BUG-996 pin above claims removing `text.replace(/\r\n/g, '\n')` makes
  // the named-variable route go blind. That was true of r2's `(.+)$` declRe
  // (JS `.` never matches CR); it is NOT true of r3's `[^;]*`, which matches CR
  // happily, nor of the pass-2 patterns, which use `\s`. Mutant proof: deleting
  // the normalisation leaves the suite green. This pin records what the other
  // pin cannot: every shape below gives the SAME answer in LF and CRLF, so the
  // normalisation is dead defensive code. INVERT ON FIX (BUG-1000) — either
  // delete normalisation and pin together, or make the BUG-996 pin discriminate.
  const shapes = [
    ['const d = row.capacityPcuPerLanePerHour;', 'const p = ROAD_TIER_CAPACITY[2];', 'const bad = d - p;', ''],
    ['const d =', '  flagForTest', '    ? row.capacityPcuPerLanePerHour', '    : 0;', 'const bad = ROAD_TIER_CAPACITY[2] - d;', ''],
    ['const bad = row.capacityPcuPerLanePerHour -', '  ROAD_TIER_CAPACITY[2];', ''],
    ['// row.capacityPcuPerLanePerHour - ROAD_TIER_CAPACITY[2]', ''],
  ];
  for (const lines of shapes) {
    const lf = lines.join('\n');
    const crlf = lf.replace(/\n/g, '\r\n');
    assert.equal(
      findMixing(crlf, 'webconsole/src/sim/trafficDemand.ts').length,
      findMixing(lf, 'webconsole/src/sim/trafficDemand.ts').length,
      `LF and CRLF must agree for shape: ${JSON.stringify(lines[0])}`
    );
  }
});
