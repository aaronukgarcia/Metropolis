/**
 * tools/plan/generate.test.js — regression test for BUG-023.
 *
 * generate.js used to merge security-scan ledger entries with
 * `securityScans[m.key] || null`: a ledger key matching no code.json module
 * (a typo, e.g. "tool.scretguard" instead of "tool.secretguard") vanished
 * silently — no error, no warning, nothing written anywhere. This test
 * proves the fix: generate.js now emits a `[MET-T031]` warning on stderr
 * naming the offending key, non-fatally (exit code stays 0, code.json is
 * still written).
 *
 * This is necessarily an integration-style test (spawns the real script as
 * a child process against the real repo files) rather than a unit test,
 * because generate.js is a top-level script with `process.exit()` calls,
 * not a module of exported functions — refactoring that shape is out of
 * scope for this fix. The test mutates data/security-scans.json with an
 * extra bogus key, runs the generator, and restores the original file
 * (byte-for-byte) in a `finally` block regardless of outcome, so a failing
 * assertion can never leave the repo's real ledger file mutated.
 *
 * Run: node tools/plan/generate.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const path = require('path');
const { spawnSync } = require('child_process');

const ROOT = path.resolve(__dirname, '..', '..');
const SCANS_PATH = path.join(ROOT, 'data', 'security-scans.json');
const GENERATE_PATH = path.join(__dirname, 'generate.js');

test('generate.js warns loudly (non-fatal) on a security-scan ledger key matching no code.json module', () => {
  const originalRaw = fs.readFileSync(SCANS_PATH, 'utf8');
  try {
    const scans = JSON.parse(originalRaw);
    const BOGUS_KEY = 'tool.this-key-does-not-exist-typo';
    scans.scans[BOGUS_KEY] = {
      scannedAt: '2026-08-10T00:00:00Z',
      scanVersion: 'v1',
      by: 'test-harness',
      findings: [],
      verdict: 'clean',
    };
    fs.writeFileSync(SCANS_PATH, JSON.stringify(scans, null, 2) + '\n', 'utf8');

    const result = spawnSync(process.execPath, [GENERATE_PATH], {
      cwd: ROOT,
      encoding: 'utf8',
    });

    assert.equal(result.status, 0, `generate.js should exit 0 (non-fatal warning), got ${result.status}. stderr:\n${result.stderr}`);
    assert.match(result.stderr, /\[MET-T031\]/, 'expected a MET-T031 warning on stderr');
    assert.ok(
      result.stderr.includes(BOGUS_KEY),
      `expected the warning to name the offending key "${BOGUS_KEY}"; stderr was:\n${result.stderr}`
    );

    // The bogus key must not have leaked into code.json — it is dropped,
    // just loudly instead of silently.
    const codeJson = JSON.parse(fs.readFileSync(path.join(ROOT, 'code.json'), 'utf8'));
    assert.ok(
      !codeJson.modules.some(m => m.key === BOGUS_KEY),
      'bogus ledger key must not appear as a code.json module'
    );
  } finally {
    // Restore the real ledger byte-for-byte and regenerate code.json /
    // bow-import.json back to their pre-test state, so this test leaves no
    // residue no matter how it exits.
    fs.writeFileSync(SCANS_PATH, originalRaw, 'utf8');
    spawnSync(process.execPath, [GENERATE_PATH], { cwd: ROOT, encoding: 'utf8' });
  }
});

test('generate.js gives the documented-exemption pointer for a key listed in knownOrphanedKeys', () => {
  const result = spawnSync(process.execPath, [GENERATE_PATH], { cwd: ROOT, encoding: 'utf8' });
  assert.equal(result.status, 0);
  assert.match(
    result.stderr,
    /\[MET-T031\].*legacy\.versionguard.*documented exemption/,
    `expected a documented-exemption warning for legacy.versionguard; stderr was:\n${result.stderr}`
  );
});
