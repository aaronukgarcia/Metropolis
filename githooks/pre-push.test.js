/**
 * githooks/pre-push.test.js — tests for githooks/pre-push (BUG-340/BUG-336
 * deliverable 3, GR#28 floor gate). Uses fake gofmt/build/vet runners for
 * every unit test (no real Go toolchain invoked, no dependency on this
 * machine having `go`/`gofmt` on PATH) plus a couple of real-subprocess
 * checks for the pure stdin-parsing helpers against a throwaway git repo.
 *
 * Run: node --test githooks/pre-push.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawnSync } = require('child_process');

const pp = require('./pre-push');

function git(cwd, args) {
  const r = spawnSync('git', args, { cwd, encoding: 'utf8' });
  if (r.status !== 0) throw new Error(`git ${args.join(' ')} failed: ${r.stderr}`);
  return r.stdout.trim();
}

function withTempRepo(fn) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'pre-push-fixture-'));
  try {
    git(dir, ['init', '-b', 'main']);
    git(dir, ['config', 'user.name', 'Fixture Contributor']);
    git(dir, ['config', 'user.email', 'fixture@example.invalid']);
    return fn(dir);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

const ZERO = pp.ZERO_SHA;
const SOME_SHA = 'a'.repeat(40);

// ---------------------------------------------------------------------------
// parsePushedRefs / touchesMainPush
// ---------------------------------------------------------------------------

test('parsePushedRefs(): parses git\'s pre-push stdin contract (one line per ref)', () => {
  const stdin = `refs/heads/feature/x ${SOME_SHA} refs/heads/feature/x ${ZERO}\nrefs/heads/main ${SOME_SHA} refs/heads/main ${ZERO}\n`;
  const refs = pp.parsePushedRefs(stdin);
  assert.equal(refs.length, 2);
  assert.equal(refs[1].remoteRef, 'refs/heads/main');
});

test('touchesMainPush(): true when a NON-DELETION ref targets refs/heads/main', () => {
  const refs = [{ localRef: 'refs/heads/main', localSha: SOME_SHA, remoteRef: 'refs/heads/main', remoteSha: ZERO }];
  assert.equal(pp.touchesMainPush(refs), true);
});

test('touchesMainPush(): false for a feature-branch-only push (PROVE-CAN-FAIL companion to the main-push case above)', () => {
  const refs = [{ localRef: 'refs/heads/feature/x', localSha: SOME_SHA, remoteRef: 'refs/heads/feature/x', remoteSha: ZERO }];
  assert.equal(pp.touchesMainPush(refs), false);
});

test('touchesMainPush(): false for a DELETION of main (local sha is all zeros — nothing to gate)', () => {
  const refs = [{ localRef: '(delete)', localSha: ZERO, remoteRef: 'refs/heads/main', remoteSha: SOME_SHA }];
  assert.equal(pp.touchesMainPush(refs), false);
});

test('parsePushedRefs(): blank lines are dropped, not turned into malformed entries', () => {
  const refs = pp.parsePushedRefs(`\n\nrefs/heads/main ${SOME_SHA} refs/heads/main ${ZERO}\n\n`);
  assert.equal(refs.length, 1);
});

// ---------------------------------------------------------------------------
// checkPush() — fake runners, no real Go toolchain
// ---------------------------------------------------------------------------

const MAIN_PUSH_STDIN = `refs/heads/main ${SOME_SHA} refs/heads/main ${ZERO}\n`;
const FEATURE_PUSH_STDIN = `refs/heads/feature/x ${SOME_SHA} refs/heads/feature/x ${ZERO}\n`;

function passingRunners(overrides = {}) {
  return {
    listGoFiles: () => ['main.go'],
    gofmt: () => ({ ok: true, output: '' }),
    build: () => ({ ok: true, output: '' }),
    vet: () => ({ ok: true, output: '' }),
    ...overrides,
  };
}

test('checkPush(): a push that does not touch main skips ALL gates entirely (never calls the fake runners)', () => {
  const calls = [];
  const runners = {
    listGoFiles: () => { calls.push('listGoFiles'); return ['main.go']; },
    gofmt: () => { calls.push('gofmt'); return { ok: true, output: '' }; },
    build: () => { calls.push('build'); return { ok: true, output: '' }; },
    vet: () => { calls.push('vet'); return { ok: true, output: '' }; },
  };
  const result = pp.checkPush(FEATURE_PUSH_STDIN, '/fake/cwd', runners);
  assert.equal(result.ok, true);
  assert.equal(result.skipped, true);
  assert.deepEqual(calls, [], 'no gate should run for a non-main push');
});

test('checkPush(): a push to main with all three gates passing is allowed', () => {
  const result = pp.checkPush(MAIN_PUSH_STDIN, '/fake/cwd', passingRunners());
  assert.equal(result.ok, true);
});

test('checkPush(): gofmt failure denies with the gofmt-l reason (PROVE-CAN-FAIL: flip gofmt to failing and the identical push must now be denied)', () => {
  const result = pp.checkPush(MAIN_PUSH_STDIN, '/fake/cwd', passingRunners({
    gofmt: () => ({ ok: false, output: 'main.go' }),
  }));
  assert.equal(result.ok, false);
  assert.match(result.reason, /gofmt -l/);
});

test('checkPush(): go build failure denies with the build output', () => {
  const result = pp.checkPush(MAIN_PUSH_STDIN, '/fake/cwd', passingRunners({
    build: () => ({ ok: false, output: 'undefined: Foo' }),
  }));
  assert.equal(result.ok, false);
  assert.match(result.reason, /go build/);
  assert.match(result.reason, /undefined: Foo/);
});

test('checkPush(): go vet failure denies with the vet output', () => {
  const result = pp.checkPush(MAIN_PUSH_STDIN, '/fake/cwd', passingRunners({
    vet: () => ({ ok: false, output: 'suspicious Printf call' }),
  }));
  assert.equal(result.ok, false);
  assert.match(result.reason, /go vet/);
});

test('checkPush(): the deny message always names the bypass env var (loud remediation)', () => {
  const result = pp.checkPush(MAIN_PUSH_STDIN, '/fake/cwd', passingRunners({
    build: () => ({ ok: false, output: 'boom' }),
  }));
  assert.equal(result.ok, false);
  assert.match(result.reason, new RegExp(pp.BYPASS_ENV));
});

test('checkPush(): the bypass env var allows even a push to main with a failing gate', () => {
  const saved = process.env[pp.BYPASS_ENV];
  try {
    process.env[pp.BYPASS_ENV] = '1';
    const result = pp.checkPush(MAIN_PUSH_STDIN, '/fake/cwd', passingRunners({
      build: () => ({ ok: false, output: 'boom' }),
    }));
    assert.equal(result.ok, true);
    assert.equal(result.bypassed, true);
  } finally {
    if (saved === undefined) delete process.env[pp.BYPASS_ENV]; else process.env[pp.BYPASS_ENV] = saved;
  }
});

test('CONTROL (PROVE-CAN-FAIL): with the bypass env var UNSET, the identical failing-gate push to main is denied', () => {
  const saved = process.env[pp.BYPASS_ENV];
  try {
    delete process.env[pp.BYPASS_ENV];
    const result = pp.checkPush(MAIN_PUSH_STDIN, '/fake/cwd', passingRunners({
      build: () => ({ ok: false, output: 'boom' }),
    }));
    assert.equal(result.ok, false);
  } finally {
    if (saved !== undefined) process.env[pp.BYPASS_ENV] = saved;
  }
});

test('checkPush(): listGoFiles() throwing (git ls-files failure) denies fail-closed, naming the failure', () => {
  const result = pp.checkPush(MAIN_PUSH_STDIN, '/fake/cwd', {
    listGoFiles: () => { throw new Error('git ls-files exploded'); },
    gofmt: () => ({ ok: true, output: '' }),
    build: () => ({ ok: true, output: '' }),
    vet: () => ({ ok: true, output: '' }),
  });
  assert.equal(result.ok, false);
  assert.match(result.reason, /git ls-files exploded/);
});

test('checkPush(): zero Go files staged/tracked still runs all three gates (the DEFAULT gofmt runner itself is what short-circuits on an empty file list — see runGofmtCheck below)', () => {
  const calls = [];
  const result = pp.checkPush(MAIN_PUSH_STDIN, '/fake/cwd', {
    listGoFiles: () => [],
    gofmt: () => { calls.push('gofmt'); return { ok: true, output: '' }; },
    build: () => { calls.push('build'); return { ok: true, output: '' }; },
    vet: () => { calls.push('vet'); return { ok: true, output: '' }; },
  });
  assert.equal(result.ok, true);
  assert.deepEqual(calls, ['gofmt', 'build', 'vet']);
});

test('runGofmtCheck(): with an empty file list, returns ok:true WITHOUT invoking the gofmt binary at all (real default runner, not a fake)', () => {
  const result = pp.runGofmtCheck('/fake/cwd', []);
  assert.deepEqual(result, { ok: true, output: '' });
});

// ---------------------------------------------------------------------------
// listTrackedGoFiles() — real git, throwaway repo
// ---------------------------------------------------------------------------

test('listTrackedGoFiles(): returns tracked .go files from a real throwaway repo', () => {
  withTempRepo((dir) => {
    fs.writeFileSync(path.join(dir, 'main.go'), 'package main\n', 'utf8');
    fs.writeFileSync(path.join(dir, 'notes.txt'), 'x\n', 'utf8');
    git(dir, ['add', '-A']);
    git(dir, ['commit', '-m', 'seed']);
    const files = pp.listTrackedGoFiles(dir);
    assert.deepEqual(files, ['main.go']);
  });
});

// ---------------------------------------------------------------------------
// CLI entry — real subprocess, fake stdin, no real Go toolchain dependency
// (a non-main push exits 0 without ever invoking go/gofmt)
// ---------------------------------------------------------------------------

test('CLI: a non-main push exits 0 immediately, regardless of whether go/gofmt exist on this machine', () => {
  withTempRepo((dir) => {
    const hookPath = path.join(__dirname, 'pre-push');
    const stdin = FEATURE_PUSH_STDIN;
    const r = spawnSync(process.execPath, [hookPath, 'origin', 'https://example.invalid/repo.git'], {
      cwd: dir, input: stdin, encoding: 'utf8',
    });
    assert.equal(r.status, 0, r.stderr);
  });
});

test('CLI: the bypass env var allows a main push without needing go/gofmt on PATH', () => {
  withTempRepo((dir) => {
    const hookPath = path.join(__dirname, 'pre-push');
    const r = spawnSync(process.execPath, [hookPath, 'origin', 'https://example.invalid/repo.git'], {
      cwd: dir, input: MAIN_PUSH_STDIN, encoding: 'utf8',
      env: { ...process.env, [pp.BYPASS_ENV]: '1' },
    });
    assert.equal(r.status, 0, r.stderr);
  });
});
