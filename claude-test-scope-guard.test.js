'use strict';
// Tests for claude-test-scope-guard.js — the cost/hygiene guard that blocks
// whole-suite test floods and redirects to the bounded scoped runner.
// Run:  node --test claude-test-scope-guard.test.js

const { test } = require('node:test');
const assert = require('node:assert');
const { decide, testTargetsInfo, usesScopedRunner } = require('./claude-test-scope-guard.js');

// Ensure the bypass env is not leaking in from the caller.
delete process.env.CLAUDE_ALLOW_FULL_TEST;

test('BLOCKS `npm test` (the exact flood that caused the waste)', () => {
  assert.equal(decide('npm test').block, true);
  assert.equal(decide('cd webconsole && npm test').block, true);
  assert.equal(decide('npm run test').block, true);
  assert.equal(decide('npm test -- --something').block, true);
});

test('BLOCKS a globbed --test target (whole-suite discovery)', () => {
  assert.equal(decide('node --test "test/*.test.mjs"').block, true);
  assert.equal(decide('node --test test/*.test.mjs').block, true);
  assert.equal(decide('npx tsx --test test/**/*.tsx').block, true);
});

test('BLOCKS a bare --test with no file argument (whole-tree flood)', () => {
  assert.equal(decide('node --test').block, true);
  assert.equal(decide('node --test --test-reporter=dot').block, true, 'flags are not file targets');
});

test('BLOCKS naming more than MAX_NAMED files (effectively a full run)', () => {
  const many = Array.from({ length: 14 }, (_, i) => `test/f${i}.test.mjs`).join(' ');
  assert.equal(decide(`node --test ${many}`).block, true);
});

test('ALLOWS a scoped, named run (the sanctioned targeted path)', () => {
  assert.equal(decide('node --test test/consistency.test.mjs test/debugjson.test.mjs').block, false);
  assert.equal(decide('npx tsx --test test/mount.test.tsx').block, false);
  assert.equal(decide('node --test --test-reporter=dot test/a.test.mjs test/b.test.mjs').block, false);
});

test('ALWAYS ALLOWS the bounded scoped runner, even for the full CI set', () => {
  assert.equal(decide('node tools/test/scoped.mjs --webconsole-ci').block, false);
  assert.equal(decide('node tools/test/scoped.mjs webconsole/test/x.test.mjs').block, false);
  assert.equal(usesScopedRunner('node tools\\test\\scoped.mjs --webconsole-ci'), true, 'windows path sep');
});

test('bypass env allows anything (deliberate supervised full run)', () => {
  process.env.CLAUDE_ALLOW_FULL_TEST = '1';
  try {
    assert.equal(decide('npm test').block, false);
    assert.equal(decide('node --test').block, false);
  } finally {
    delete process.env.CLAUDE_ALLOW_FULL_TEST;
  }
});

test('does NOT block unrelated commands (no false positives)', () => {
  assert.equal(decide('go test ./internal/engine/compose/ -run TestX').block, false, 'go test is a different tool');
  assert.equal(decide('npm run build').block, false);
  assert.equal(decide('node tools/plan/generate.js').block, false);
  assert.equal(decide('git commit -m "test the thing"').block, false, 'the word test in prose is not a run');
  assert.equal(decide('').block, false);
});

test('PROVE-CAN-FAIL: a glob target really is detected as glob', () => {
  const info = testTargetsInfo('node --test test/*.test.mjs');
  assert.equal(info.hasGlob, true);
  const named = testTargetsInfo('node --test test/one.test.mjs test/two.test.mjs');
  assert.equal(named.hasGlob, false);
  assert.equal(named.count, 2);
});

test('compound command: a flood in ANY segment is caught', () => {
  assert.equal(decide('npx tsc --noEmit && npm test').block, true);
  assert.equal(decide('node --test test/a.test.mjs && node --test').block, true, 'second segment is a bare flood');
});
