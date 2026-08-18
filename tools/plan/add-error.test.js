/**
 * tools/plan/add-error.test.js — tests for BUG-273's add-error.js.
 *
 * Every test operates on a fixture errors.json copied into a fresh
 * os.tmpdir() directory per test -- the real data/errors.json is NEVER
 * mutated. Where a real-registry check is wanted it is done separately,
 * read-only, outside this test file.
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { execFileSync } = require('child_process');

const addError = require('./add-error.js');

// A small, hand-built fixture that mirrors data/errors.json's exact
// indentation conventions (2 / 4 / 6 spaces) so the text-splice logic
// under test behaves exactly as it does against the real file.
function fixtureText() {
  return [
    '{',
    '  "version": 1,',
    '  "ranges": {',
    '    "format": "MET-<layer><NNN>",',
    '    "layers": {',
    '      "F": "foundation",',
    '      "G": "engine overflow"',
    '    },',
    '    "reserved": {',
    '      "F000-F099": "reserved for foundation.test (fixture)",',
    '      "G000-G099": "reserved for engine.testmod (fixture)"',
    '    },',
    '    "toolingNote": "fixture note"',
    '  },',
    '  "codes": {',
    '    "MET-F001": {',
    '      "severity": "fatal",',
    '      "module": "foundation.test",',
    '      "message": "boom {x}",',
    '      "remedy": "fix it"',
    '    },',
    '    "MET-G000": {',
    '      "severity": "error",',
    '      "module": "engine.testmod",',
    '      "message": "bad {y}",',
    '      "remedy": "fix it too"',
    '    }',
    '  }',
    '}',
    '',
  ].join('\n');
}

function makeFixtureDir() {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'add-error-test-'));
  const errorsPath = path.join(dir, 'errors.json');
  fs.writeFileSync(errorsPath, fixtureText(), 'utf8');
  return { dir, errorsPath };
}

// ---------------------------------------------------------------------
// claim-range: lowest-free, deterministic, non-overlapping
// ---------------------------------------------------------------------

test('claim-range allocates the lowest free block, skipping the reserved G000-G099', () => {
  const { errorsPath } = makeFixtureDir();
  const result = addError.claimRange({
    errorsPath,
    mkey: 'engine.newmod',
    size: 100,
    actor: 'test',
  });
  assert.equal(result.rangeKey, 'G100-G199');
  assert.equal(result.wrote, true);

  const after = JSON.parse(fs.readFileSync(errorsPath, 'utf8'));
  assert.ok(after.ranges.reserved['G100-G199']);
  assert.match(after.ranges.reserved['G100-G199'], /reserved for engine\.newmod/);
});

test('claim-range allocates non-overlapping ranges across repeated calls', () => {
  const { errorsPath } = makeFixtureDir();
  const r1 = addError.claimRange({ errorsPath, mkey: 'engine.modA', size: 100, actor: 't' });
  const r2 = addError.claimRange({ errorsPath, mkey: 'engine.modB', size: 100, actor: 't' });
  const r3 = addError.claimRange({ errorsPath, mkey: 'engine.modC', size: 50, actor: 't' });

  assert.equal(r1.rangeKey, 'G100-G199');
  assert.equal(r2.rangeKey, 'G200-G299');
  // modC asked for size 50; G300-G349 is the first free 50-wide aligned block.
  assert.equal(r3.rangeKey, 'G300-G349');

  const final = JSON.parse(fs.readFileSync(errorsPath, 'utf8'));
  const keys = Object.keys(final.ranges.reserved);
  assert.equal(new Set(keys).size, keys.length, 'no duplicate reservation keys');
});

test('claim-range respects --layer override for an ambiguous feat.* mkey', () => {
  const { errorsPath } = makeFixtureDir();
  assert.throws(
    () => addError.claimRange({ errorsPath, mkey: 'feat.something', size: 100, actor: 't' }),
    /could not infer a layer/
  );
  const result = addError.claimRange({
    errorsPath,
    mkey: 'feat.something',
    size: 100,
    layerOverride: 'F',
    actor: 't',
  });
  // F000-F099 already reserved by the fixture -> next free is F100-F199.
  assert.equal(result.rangeKey, 'F100-F199');
});

test('claim-range dry-run changes nothing on disk', () => {
  const { errorsPath } = makeFixtureDir();
  const before = fs.readFileSync(errorsPath, 'utf8');
  const result = addError.claimRange({
    errorsPath,
    mkey: 'engine.newmod',
    size: 100,
    dryRun: true,
    actor: 't',
  });
  assert.equal(result.wrote, false);
  assert.equal(result.rangeKey, 'G100-G199');
  const after = fs.readFileSync(errorsPath, 'utf8');
  assert.equal(after, before, 'dry-run must not touch the file');
});

// ---------------------------------------------------------------------
// add: refuses out-of-range / duplicate / foreign-range codes
// ---------------------------------------------------------------------

test('add refuses a code with no owning reservation', () => {
  const { errorsPath } = makeFixtureDir();
  assert.throws(
    () =>
      addError.addCode({
        errorsPath,
        code: 'MET-G500',
        mkey: 'engine.testmod',
        name: 'Whatever',
        template: 'boom',
      }),
    /does not fall inside any reserved range/
  );
});

test('add refuses a duplicate code', () => {
  const { errorsPath } = makeFixtureDir();
  assert.throws(
    () =>
      addError.addCode({
        errorsPath,
        code: 'MET-F001',
        mkey: 'foundation.test',
        name: 'Dup',
        template: 'boom',
      }),
    /already registered/
  );
});

test('add refuses a code inside a range reserved for a DIFFERENT mkey', () => {
  const { errorsPath } = makeFixtureDir();
  assert.throws(
    () =>
      addError.addCode({
        errorsPath,
        code: 'MET-G050', // inside G000-G099, reserved for engine.testmod
        mkey: 'engine.impostor',
        name: 'Impostor',
        template: 'boom',
      }),
    /reserved for a DIFFERENT owner/
  );
});

test('add refuses a malformed code', () => {
  const { errorsPath } = makeFixtureDir();
  assert.throws(
    () =>
      addError.addCode({
        errorsPath,
        code: 'MET-g050',
        mkey: 'engine.testmod',
        name: 'Bad',
        template: 'boom',
      }),
    /does not match/
  );
});

test('add succeeds for a code correctly inside its own reservation, and preserves formatting', () => {
  const { errorsPath } = makeFixtureDir();
  const before = fs.readFileSync(errorsPath, 'utf8');

  const result = addError.addCode({
    errorsPath,
    code: 'MET-G001',
    mkey: 'engine.testmod',
    name: 'ThingBroke',
    template: 'thing broke: {cause}',
    remedy: 'fix the thing',
    severity: 'warn',
  });
  assert.equal(result.wrote, true);

  const after = fs.readFileSync(errorsPath, 'utf8');
  const afterData = JSON.parse(after);
  assert.deepEqual(afterData.codes['MET-G001'], {
    severity: 'warn',
    module: 'engine.testmod',
    message: 'thing broke: {cause}',
    remedy: 'fix the thing',
  });

  // Byte-diff limited to the addition: find the single existing line that
  // changes (the old final "    }" of the codes object gaining a trailing
  // comma), then prove everything before it is untouched, everything
  // after it (once the inserted block is skipped) is untouched, and the
  // inserted block itself is exactly the new entry.
  const beforeLines = before.split('\n');
  const afterLines = after.split('\n');

  let firstDiff = -1;
  for (let i = 0; i < beforeLines.length; i++) {
    if (beforeLines[i] !== afterLines[i]) {
      firstDiff = i;
      break;
    }
  }
  assert.notEqual(firstDiff, -1, 'expected exactly one changed line before the insertion point');
  assert.equal(beforeLines[firstDiff], '    }');
  assert.equal(afterLines[firstDiff], '    },');

  // Everything strictly before the change is untouched.
  assert.deepEqual(afterLines.slice(0, firstDiff), beforeLines.slice(0, firstDiff));

  const inserted = afterLines.length - beforeLines.length;
  assert.ok(inserted > 0, 'expected new lines to have been inserted');

  // Everything strictly after the inserted block is untouched.
  assert.deepEqual(
    afterLines.slice(firstDiff + 1 + inserted),
    beforeLines.slice(firstDiff + 1)
  );

  // The inserted block itself is exactly the new entry.
  const insertedBlock = afterLines.slice(firstDiff + 1, firstDiff + 1 + inserted).join('\n');
  assert.match(insertedBlock, /"MET-G001": \{/);
  assert.match(insertedBlock, /"severity": "warn"/);
});

test('add dry-run changes nothing on disk', () => {
  const { errorsPath } = makeFixtureDir();
  const before = fs.readFileSync(errorsPath, 'utf8');
  const result = addError.addCode({
    errorsPath,
    code: 'MET-G001',
    mkey: 'engine.testmod',
    name: 'ThingBroke',
    template: 'thing broke: {cause}',
    dryRun: true,
  });
  assert.equal(result.wrote, false);
  const after = fs.readFileSync(errorsPath, 'utf8');
  assert.equal(after, before);
});

test('claim-range then add round-trips end to end', () => {
  const { errorsPath } = makeFixtureDir();
  const claimed = addError.claimRange({ errorsPath, mkey: 'engine.newmod', size: 100, actor: 't' });
  assert.equal(claimed.rangeKey, 'G100-G199');

  const added = addError.addCode({
    errorsPath,
    code: 'MET-G100',
    mkey: 'engine.newmod',
    name: 'NewThing',
    template: 'new thing failed: {cause}',
  });
  assert.equal(added.wrote, true);

  const final = JSON.parse(fs.readFileSync(errorsPath, 'utf8'));
  assert.ok(final.codes['MET-G100']);
  assert.equal(final.codes['MET-G100'].module, 'engine.newmod');
});

// ---------------------------------------------------------------------
// check: proves every violation class CAN be detected (and CAN pass)
// ---------------------------------------------------------------------

test('check passes clean on the untouched fixture', () => {
  const { errorsPath, dir } = makeFixtureDir();
  const result = addError.check({ errorsPath, repoDir: dir });
  assert.deepEqual(result.problems, []);
});

test('check catches a planted duplicate code', () => {
  const { errorsPath, dir } = makeFixtureDir();
  const raw = fs.readFileSync(errorsPath, 'utf8');
  // Duplicate the MET-F001 block wholesale (a literal second key with the
  // same name) -- this is what a manual copy/paste collision looks like.
  const duped = raw.replace(
    '    "MET-G000": {',
    '    "MET-F001": {\n      "severity": "fatal",\n      "module": "foundation.test",\n      "message": "boom {x}",\n      "remedy": "fix it"\n    },\n    "MET-G000": {'
  );
  fs.writeFileSync(errorsPath, duped, 'utf8');
  const result = addError.check({ errorsPath, repoDir: dir });
  assert.ok(result.problems.some((p) => /duplicate code MET-F001/.test(p)));
});

test('check catches an orphan code with no reservation', () => {
  const { errorsPath, dir } = makeFixtureDir();
  const raw = fs.readFileSync(errorsPath, 'utf8');
  const withOrphan = raw.replace(
    '    "MET-G000": {',
    '    "MET-G500": {\n      "severity": "error",\n      "module": "engine.orphan",\n      "message": "no reservation",\n      "remedy": "n/a"\n    },\n    "MET-G000": {'
  );
  fs.writeFileSync(errorsPath, withOrphan, 'utf8');
  const result = addError.check({ errorsPath, repoDir: dir });
  assert.ok(result.problems.some((p) => /MET-G500 has no owning reservation/.test(p)));
});

test('check catches overlapping reservations', () => {
  const { errorsPath, dir } = makeFixtureDir();
  const raw = fs.readFileSync(errorsPath, 'utf8');
  const overlapping = raw.replace(
    '"G000-G099": "reserved for engine.testmod (fixture)"',
    '"G000-G099": "reserved for engine.testmod (fixture)",\n      "G050-G149": "reserved for engine.overlapper (fixture, deliberately overlapping)"'
  );
  fs.writeFileSync(errorsPath, overlapping, 'utf8');
  const result = addError.check({ errorsPath, repoDir: dir });
  assert.ok(result.problems.some((p) => /overlap on layer G/.test(p)));
});

test('check flags stale reservations vs origin/main', () => {
  const repoDir = fs.mkdtempSync(path.join(os.tmpdir(), 'add-error-repo-'));
  const git = (...args) => execFileSync('git', args, { cwd: repoDir, encoding: 'utf8' });

  git('init', '-q');
  git('config', 'user.email', 'test@example.com');
  git('config', 'user.name', 'Test');

  const dataDir = path.join(repoDir, 'data');
  fs.mkdirSync(dataDir);
  const errorsPath = path.join(dataDir, 'errors.json');

  // Commit 1: the fixture as-is.
  fs.writeFileSync(errorsPath, fixtureText(), 'utf8');
  git('add', '.');
  git('commit', '-q', '-m', 'a');

  // Commit 2: add an extra reservation, simulating a teammate's push.
  const withExtra = fixtureText().replace(
    '"G000-G099": "reserved for engine.testmod (fixture)"',
    '"G000-G099": "reserved for engine.testmod (fixture)",\n      "G100-G199": "reserved for engine.teammate (landed on main)"'
  );
  fs.writeFileSync(errorsPath, withExtra, 'utf8');
  git('add', '.');
  git('commit', '-q', '-m', 'b');

  // Point refs/remotes/origin/main at the up-to-date commit.
  git('update-ref', 'refs/remotes/origin/main', 'HEAD');

  // Roll the WORKING TREE file back to the stale (commit-1) content --
  // this simulates a local worktree that has not rebased.
  fs.writeFileSync(errorsPath, fixtureText(), 'utf8');

  const result = addError.check({ errorsPath, repoDir });
  assert.ok(
    result.warnings.some((w) => /G100-G199/.test(w) && /rebase/.test(w)),
    `expected a stale-reservation warning, got: ${JSON.stringify(result.warnings)}`
  );
  // Staleness is a WARNING, not a failure -- the local file itself has no
  // duplicate/orphan/overlap problems, so problems must stay empty.
  assert.deepEqual(result.problems, []);
});

// ---------------------------------------------------------------------
// atomic write
// ---------------------------------------------------------------------

test('atomicWrite replaces the target only via a rename, never a partial in-place write', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'add-error-atomic-'));
  const target = path.join(dir, 'out.json');
  fs.writeFileSync(target, 'ORIGINAL', 'utf8');

  const realWriteFileSync = fs.writeFileSync;
  let sawTmpFile = false;
  fs.writeFileSync = (p, content, enc) => {
    if (typeof p === 'string' && p.includes('.tmp-')) {
      sawTmpFile = true;
      // While the tmp write is "in flight", the real target must still
      // read as the original content -- proving a reader can never
      // observe a partial write.
      assert.equal(fs.readFileSync(target, 'utf8'), 'ORIGINAL');
    }
    return realWriteFileSync(p, content, enc);
  };
  try {
    addError.atomicWrite(target, 'REPLACED');
  } finally {
    fs.writeFileSync = realWriteFileSync;
  }
  assert.ok(sawTmpFile, 'expected atomicWrite to stage through a .tmp- file');
  assert.equal(fs.readFileSync(target, 'utf8'), 'REPLACED');

  const leftovers = fs.readdirSync(dir).filter((f) => f.includes('.tmp-'));
  assert.deepEqual(leftovers, [], 'no tmp file should remain after a successful atomic write');
});

test('atomicWrite leaves the target untouched if the staged write fails', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'add-error-atomic-fail-'));
  const target = path.join(dir, 'out.json');
  fs.writeFileSync(target, 'ORIGINAL', 'utf8');

  const realWriteFileSync = fs.writeFileSync;
  fs.writeFileSync = (p, content, enc) => {
    if (typeof p === 'string' && p.includes('.tmp-')) {
      throw new Error('simulated disk full');
    }
    return realWriteFileSync(p, content, enc);
  };
  try {
    assert.throws(() => addError.atomicWrite(target, 'REPLACED'), /simulated disk full/);
  } finally {
    fs.writeFileSync = realWriteFileSync;
  }
  assert.equal(fs.readFileSync(target, 'utf8'), 'ORIGINAL', 'target must be unchanged on failure');
});

// ---------------------------------------------------------------------
// misc unit coverage
// ---------------------------------------------------------------------

test('formatCode pads under 1000 to 3 digits and leaves 1000+ at natural width', () => {
  assert.equal(addError.formatCode('G', 0), 'G000');
  assert.equal(addError.formatCode('G', 99), 'G099');
  assert.equal(addError.formatCode('G', 1000), 'G1000');
  assert.equal(addError.formatCode('G', 9999), 'G9999');
});

test('inferLayer matches the exhausted-layer overflow convention', () => {
  assert.equal(addError.inferLayer('foundation.num'), 'F');
  assert.equal(addError.inferLayer('engine.roads'), 'G');
  assert.equal(addError.inferLayer('ui.dash'), 'V');
  assert.equal(addError.inferLayer('harness.replay'), 'H');
  assert.equal(addError.inferLayer('unknown.thing'), null);
});

test('BUG-273 r1 REJECT regression: a hyphen-prefix mkey CANNOT claim a foreign range (exact owner-token equality)', () => {
  const { errorsPath } = makeFixtureDir();
  const doc = JSON.parse(fs.readFileSync(errorsPath, 'utf8'));
  doc.ranges.reserved['G500-G599'] = 'reserved for engine.fiscal-circuit (fixture)';
  fs.writeFileSync(errorsPath, JSON.stringify(doc, null, 2) + "\n", 'utf8');
  assert.throws(
    () => addError.addCode({
      errorsPath, code: 'MET-G550', mkey: 'engine.fiscal',
      name: 'Bypass', template: 'planted in a foreign range', dryRun: true,
    }),
    /DIFFERENT owner/,
    'hyphen-prefix mkey must be rejected as a foreign owner'
  );
  const ok = addError.addCode({
    errorsPath, code: 'MET-G550', mkey: 'engine.fiscal-circuit',
    name: 'Legit', template: 'legitimate add by the real owner', dryRun: true,
  });
  assert.equal(ok.wrote, false);
});

test('claimRange rejects non-positive size at the library surface (not only the CLI)', () => {
  const { errorsPath } = makeFixtureDir();
  for (const bad of [0, -5, 1.5, NaN]) {
    assert.throws(() => addError.claimRange({ errorsPath, mkey: 'engine.newmod', size: bad, dryRun: true }));
  }
});
