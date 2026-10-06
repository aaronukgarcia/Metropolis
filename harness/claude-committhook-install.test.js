/**
 * claude-committhook-install.test.js — tests for claude-committhook-install.js
 * (FEAT-045 AC-12, AC-13) and its wiring into claude-startup.js's
 * unconditional session-start summary (AC-14).
 *
 * Run: node --test claude-committhook-install.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');

const install = require('./claude-committhook-install.js');

function withTempRepo(fn) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'committhook-install-fixture-'));
  try {
    return fn(dir);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

// ---------------------------------------------------------------------------
// AC-12: install() copies the canonical source into .git/hooks/commit-msg
// and makes it executable/invocable.
// ---------------------------------------------------------------------------

test('AC-12: install() creates .git/hooks/commit-msg, byte-for-byte matching the tracked source, where none existed before', () => {
  withTempRepo((dir) => {
    const dest = install.installedPath(dir);
    assert.ok(!fs.existsSync(dest), 'test precondition: no hook installed yet');

    const returned = install.install(dir);
    assert.equal(returned, dest);
    assert.ok(fs.existsSync(dest), 'expected the hook to now exist');

    const installedBytes = fs.readFileSync(dest);
    const canonicalBytes = fs.readFileSync(install.CANONICAL_SOURCE);
    assert.ok(Buffer.compare(installedBytes, canonicalBytes) === 0, 'installed copy must match the tracked source byte-for-byte');

    if (process.platform !== 'win32') {
      const mode = fs.statSync(dest).mode;
      assert.ok(mode & 0o111, 'expected the installed hook to be executable on POSIX');
    }
  });
});

// ---------------------------------------------------------------------------
// AC-13: verify() distinguishes healthy / stale / absent — never collapsed.
// ---------------------------------------------------------------------------

test('AC-13: verify() reports "absent" when no hook file exists at all', () => {
  withTempRepo((dir) => {
    const result = install.verify(dir);
    assert.equal(result.state, 'absent');
  });
});

test('AC-13: verify() reports "healthy" immediately after a real install', () => {
  withTempRepo((dir) => {
    install.install(dir);
    const result = install.verify(dir);
    assert.equal(result.state, 'healthy');
  });
});

test('AC-13: verify() reports "stale" for an existing-but-corrupted installed copy — not "absent" and not "healthy" (rejects an existence-only check)', () => {
  withTempRepo((dir) => {
    install.install(dir);
    const dest = install.installedPath(dir);
    fs.appendFileSync(dest, '\n// tampered\n');
    const result = install.verify(dir);
    assert.equal(result.state, 'stale');
  });
});

test('AC-13: verify() reports "stale" for a wholly unrelated leftover file at the hook path (an existence-only check would call this "healthy")', () => {
  withTempRepo((dir) => {
    fs.mkdirSync(install.hooksDir(dir), { recursive: true });
    fs.writeFileSync(install.installedPath(dir), '#!/bin/sh\necho unrelated leftover script\n', 'utf8');
    const result = install.verify(dir);
    assert.equal(result.state, 'stale', 'an existing-but-wrong file must be distinguished from healthy');
  });
});

test('AC-13: verify() re-run after deleting a previously-healthy hook flips to "absent", not silently staying "healthy"', () => {
  withTempRepo((dir) => {
    install.install(dir);
    assert.equal(install.verify(dir).state, 'healthy');
    fs.rmSync(install.installedPath(dir));
    assert.equal(install.verify(dir).state, 'absent');
  });
});

// ---------------------------------------------------------------------------
// AC-14: the "absent"/"stale" states are surfaced in claude-startup.js's
// unconditional session-start summary — traced structurally (grep) and
// behaviorally (captured output for a simulated-missing hook).
// ---------------------------------------------------------------------------

test('AC-14 structural: claude-startup.js invokes the committhook verify check unconditionally inside the session-summary path used by every successful checkin (not behind a skill/slash command)', () => {
  const src = fs.readFileSync(path.join(__dirname, 'claude-startup.js'), 'utf8');
  assert.match(src, /require\(['"]\.\/claude-committhook-install\.js['"]\)/);
  // The call must be textually inside printSessionSummary(), which
  // emitSuccess() calls unconditionally on every successful checkin — not
  // inside a function only a skill invokes on demand.
  const body = src.slice(src.indexOf('function printSessionSummary'), src.indexOf('function emitSuccess'));
  assert.match(body, /committhook\.summaryLine\(/, 'expected the verify-check invocation inside printSessionSummary(), the unconditional per-session path');
  // And emitSuccess() (the actual per-checkin entry point) must call it.
  const emitSuccessBody = src.slice(src.indexOf('function emitSuccess'), src.indexOf('function emitDeferredCheckin'));
  assert.match(emitSuccessBody, /printSessionSummary\(/, 'expected emitSuccess() to call printSessionSummary() unconditionally');
});

test('AC-14 behavioral: a simulated ABSENT hook produces session-start output containing the "ABSENT"/unprotected wording (captured console output, not just the return value)', () => {
  const startup = require('./claude-startup.js');
  withTempRepo((dir) => {
    // No install() call — .git/hooks/commit-msg genuinely does not exist in
    // this throwaway repo. printSessionSummary() has no filesystem side
    // effects of its own (unlike emitSuccess(), which also writes this
    // machine's real, shared .claude/.identity session-coordination file —
    // deliberately not exercised here).
    const lines = [];
    const originalLog = console.log;
    console.log = (...args) => lines.push(args.join(' '));
    try {
      startup.printSessionSummary('bob', 'fake checkin output for this test', dir);
    } finally {
      console.log = originalLog;
    }
    const captured = lines.join('\n');
    assert.match(captured, /ABSENT/, 'expected the captured session-start output to name the ABSENT state explicitly');
    assert.match(captured, /NOT[\s\S]{0,40}identity-protected|not identity-protected/i);
  });
});

test('AC-14 behavioral: a simulated STALE hook produces session-start output containing the "STALE" wording', () => {
  const startup = require('./claude-startup.js');
  withTempRepo((dir) => {
    install.install(dir);
    fs.appendFileSync(install.installedPath(dir), '\n// tampered for this test\n');
    const lines = [];
    const originalLog = console.log;
    console.log = (...args) => lines.push(args.join(' '));
    try {
      startup.printSessionSummary('bob', 'fake checkin output for this test', dir);
    } finally {
      console.log = originalLog;
    }
    const captured = lines.join('\n');
    assert.match(captured, /STALE/);
  });
});
