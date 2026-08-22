/**
 * tools/plan/errors-mojibake-guard.test.js — BUG-350 guard tests.
 *
 * Verification standard (metropolis-verification-standards): a check that
 * cannot fail is not a check. These tests prove the guard:
 *   (a) FIRES on the mojibake signature byte pairs C3 82 / C3 A2 — a real
 *       corrupted file built from the exact 9-class byte patterns recovered
 *       from the 2026-08-22 data/errors.json incident (em dash â€" = C3 A2
 *       E2 82 AC E2 80 9D, section sign Â§ = C3 82 C2 A7, multiplication
 *       sign Ã— = C3 83 E2 80 94), and
 *   (b) stays quiet on legitimate single-encoded UTF-8 (é = C3 A9, ä = C3 A4,
 *       a real em dash = E2 80 94) and on pure ASCII.
 * Plus reverseMojibake() coverage proving the repair primitive reconstructs
 * the original pre-corruption bytes for every incident class.
 *
 * Entirely self-contained: fixtures are built in a fresh os.tmpdir()
 * directory per test; the live repo data dir, code.json and the DB are never
 * touched. Run:
 *   node tools/plan/errors-mojibake-guard.test.js
 * or via `node --test tools/plan/`.
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawnSync } = require('child_process');

const {
  scanBuffer,
  scanDataDir,
  reverseMojibake,
  runCheck,
  MOJIBAKE_SIGNATURES,
  main,
} = require('./errors-mojibake-guard.js');

const GUARD_PATH = path.join(__dirname, 'errors-mojibake-guard.js');

// Byte sequences recovered from the incident (each = UTF-8 bytes of the
// mojibake text that replaced one original character).
const CORRUPT_EMDASH = Buffer.from([0xc3, 0xa2, 0xe2, 0x82, 0xac, 0xe2, 0x80, 0x9d]); // â€" = original —
const CORRUPT_SECTION = Buffer.from([0xc3, 0x82, 0xc2, 0xa7]); // Â§ = original §
const CORRUPT_PLUSMINUS = Buffer.from([0xc3, 0x82, 0xc2, 0xb1]); // Â± = original ±
const CORRUPT_TIMES = Buffer.from([0xc3, 0x83, 0xe2, 0x80, 0x94]); // Ã— = original ×

function makeTempDir(prefix) {
  return fs.mkdtempSync(path.join(os.tmpdir(), prefix));
}

function makeTempDataDir(files) {
  const dir = makeTempDir('errguard-');
  for (const [name, buf] of Object.entries(files)) {
    fs.writeFileSync(path.join(dir, name), buf);
  }
  return dir;
}

// ── unit: scanBuffer fires on the signature ──────────────────────────────

test('scanBuffer FIRES on a lone C3 82 pair (the Â prefix of §/± classes)', () => {
  const buf = Buffer.concat([Buffer.from('"x": "'), CORRUPT_SECTION, Buffer.from('"')]);
  const hits = scanBuffer(buf);
  assert.ok(hits.length > 0, 'C3 82 must be detected');
  assert.equal(hits[0].bytes, 'C3 82');
  // offset of the C3 of the corrupt § within the buffer
  assert.equal(hits[0].offset, Buffer.from('"x": "').length);
});

test('scanBuffer FIRES on the C3 A2 pair (the â prefix of the em-dash/quote/bullet classes)', () => {
  const buf = Buffer.concat([Buffer.from('"F": "foundation '), CORRUPT_EMDASH, Buffer.from(' internal"')]);
  const hits = scanBuffer(buf);
  assert.ok(hits.some((h) => h.bytes === 'C3 A2'), 'C3 A2 must be detected');
  assert.equal(hits.length, 1, 'em dash corrupts to exactly one C3 A2 pair');
});

test('scanBuffer reports a sensible offset and the signature bytes', () => {
  const prefix = Buffer.from('{\n  "msg": "');
  const buf = Buffer.concat([prefix, CORRUPT_EMDASH, Buffer.from('" }')]);
  const [hit] = scanBuffer(buf);
  assert.equal(hit.offset, prefix.length);
  assert.equal(hit.bytes, 'C3 A2');
  assert.ok(hit.label && hit.label.includes('C3 A2'));
});

test('scanBuffer does NOT double-count overlapping signatures', () => {
  // A C3 A2 pair is a distinct 2-byte window; adjacent corrupt runs count once each.
  const buf = Buffer.concat([CORRUPT_EMDASH, CORRUPT_SECTION]);
  const hits = scanBuffer(buf);
  assert.equal(hits.length, 2);
});

// ── unit: scanBuffer stays quiet on legit content ────────────────────────

test('scanBuffer stays QUIET on legitimate single-encoded é and ä (C3 A9 / C3 A4)', () => {
  const clean = Buffer.from('{"accented": "café ära — fine"}', 'utf8');
  const hits = scanBuffer(clean);
  assert.equal(hits.length, 0, 'single-encoded é/ä/— must not trip the guard');
});

test('scanBuffer stays QUIET on pure ASCII', () => {
  const ascii = Buffer.from('{"ok": "no non-ascii bytes here", "n": 42}', 'utf8');
  assert.equal(scanBuffer(ascii).length, 0);
});

test('scanBuffer stays QUIET on a real (single-encoded) em dash E2 80 94', () => {
  const real = Buffer.from('foundation — internal/foundation/*', 'utf8');
  const hits = scanBuffer(real);
  assert.equal(hits.length, 0, 'a legitimate em dash is not the mojibake signature');
});

// ── scanDataDir / runCheck over fixture trees ────────────────────────────

test('runCheck FAILS (totalHits > 0, findings list file:offset) on a corrupted data dir', () => {
  const dataDir = makeTempDataDir({
    'errors.json': Buffer.concat([
      Buffer.from('{\n  "layers": { "F": "foundation '),
      CORRUPT_EMDASH,
      Buffer.from(' internal" }\n}\n'),
    ]),
    'clean.json': Buffer.from('{"fine": "café é ä"}', 'utf8'),
  });
  try {
    const report = runCheck({ dataDir });
    assert.equal(report.totalHits, 1);
    assert.equal(report.findings.length, 1);
    assert.equal(report.filesChecked, 2);
    const f = report.findings[0];
    assert.ok(f.file.endsWith('errors.json'), `expected errors.json, got ${f.file}`);
    assert.equal(f.hits[0].bytes, 'C3 A2');
    assert.equal(typeof f.hits[0].offset, 'number');
  } finally {
    fs.rmSync(dataDir, { recursive: true, force: true });
  }
});

test('runCheck PASSES (totalHits === 0) on a clean data dir', () => {
  const dataDir = makeTempDataDir({
    'errors.json': Buffer.from('{"a": "café ä — ok", "b": 1}', 'utf8'),
    'modes.json': Buffer.from('{"list": ["x", "y"]}', 'utf8'),
  });
  try {
    const report = runCheck({ dataDir });
    assert.equal(report.totalHits, 0);
    assert.equal(report.findings.length, 0);
    assert.equal(report.filesChecked, 2);
  } finally {
    fs.rmSync(dataDir, { recursive: true, force: true });
  }
});

test('scanDataDir returns per-file hits arrays including clean files', () => {
  const dataDir = makeTempDataDir({
    'bad.json': Buffer.concat([Buffer.from('{"v": "'), CORRUPT_SECTION, Buffer.from('"}')]),
    'good.json': Buffer.from('{}'),
  });
  try {
    const results = scanDataDir(dataDir);
    assert.equal(results.length, 2);
    const bad = results.find((r) => r.file.endsWith('bad.json'));
    const good = results.find((r) => r.file.endsWith('good.json'));
    assert.equal(bad.hits.length, 1);
    assert.equal(bad.hits[0].bytes, 'C3 82');
    assert.equal(good.hits.length, 0);
  } finally {
    fs.rmSync(dataDir, { recursive: true, force: true });
  }
});

// ── reverseMojibake: the repair primitive ────────────────────────────────

test('reverseMojibake reconstructs the original em dash from â€"', () => {
  const repaired = reverseMojibake(CORRUPT_EMDASH);
  assert.equal(repaired.toString('utf8'), '—');
});

test('reverseMojibake reconstructs §, ± and × from their corrupted forms', () => {
  assert.equal(reverseMojibake(CORRUPT_SECTION).toString('utf8'), '§');
  assert.equal(reverseMojibake(CORRUPT_PLUSMINUS).toString('utf8'), '±');
  assert.equal(reverseMojibake(CORRUPT_TIMES).toString('utf8'), '×');
});

test('reverseMojibake round-trips a whole corrupted line back to the original clean line', () => {
  const originalLine = Buffer.from('      "F": "foundation — internal/foundation/* (registry)"', 'utf8');
  const corrupted = Buffer.concat([
    Buffer.from('      "F": "foundation '),
    CORRUPT_EMDASH,
    Buffer.from(' internal/foundation/* (registry)"'),
  ]);
  const repaired = reverseMojibake(corrupted);
  assert.equal(repaired.toString('utf8'), originalLine.toString('utf8'));
});

test('reverseMojibake leaves ASCII-only buffers untouched', () => {
  const ascii = Buffer.from('{"plain": "ascii only", "n": 1}', 'utf8');
  assert.equal(reverseMojibake(ascii).toString('utf8'), ascii.toString('utf8'));
});

// ── CLI: exit codes prove it can fail ────────────────────────────────────

test('CLI exits 1 and lists file:offset on a corrupted file', () => {
  const dataDir = makeTempDataDir({
    'errors.json': Buffer.concat([Buffer.from('{"v": "'), CORRUPT_EMDASH, Buffer.from('"}')]),
  });
  try {
    const res = spawnSync(process.execPath, [GUARD_PATH, '--data-dir', dataDir], { encoding: 'utf8' });
    assert.equal(res.status, 1, `expected exit 1, got ${res.status}: ${res.stderr}`);
    assert.ok(res.stderr.includes('errors.json'), 'stderr should name the offending file');
    assert.ok(/errors\.json:\d+/.test(res.stderr), `stderr should carry a file:offset, got: ${res.stderr}`);
    assert.ok(res.stderr.includes('C3 A2'), 'stderr should name the signature bytes');
  } finally {
    fs.rmSync(dataDir, { recursive: true, force: true });
  }
});

test('CLI exits 0 on a clean data dir (no false pass trigger)', () => {
  const dataDir = makeTempDataDir({
    'errors.json': Buffer.from('{"a": "café é ä — fine", "b": 2}', 'utf8'),
    'modes.json': Buffer.from('{"list": ["a"]}', 'utf8'),
  });
  try {
    const res = spawnSync(process.execPath, [GUARD_PATH, '--data-dir', dataDir], { encoding: 'utf8' });
    assert.equal(res.status, 0, `expected exit 0, got ${res.status}: ${res.stderr}`);
    assert.ok(res.stdout.includes('OK:'), 'clean run should print OK');
  } finally {
    fs.rmSync(dataDir, { recursive: true, force: true });
  }
});

test('CLI --preview-fix prints the repaired content to stdout and never writes', () => {
  const dir = makeTempDir('errguard-preview-');
  const badPath = path.join(dir, 'errors.json');
  const corrupted = Buffer.concat([
    Buffer.from('{"layers": { "F": "foundation '),
    CORRUPT_EMDASH,
    Buffer.from(' internal" } }\n'),
  ]);
  fs.writeFileSync(badPath, corrupted);
  try {
    const res = spawnSync(process.execPath, [GUARD_PATH, '--preview-fix', badPath], { encoding: 'utf8' });
    assert.equal(res.status, 0, `--preview-fix should exit 0: ${res.stderr}`);
    assert.equal(res.stdout, '{"layers": { "F": "foundation — internal" } }\n');
    // The file on disk must be untouched (the guard never writes).
    assert.deepEqual(fs.readFileSync(badPath), corrupted);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('MOJIBAKE_SIGNATURES exports the exact BUG-350 pair list', () => {
  assert.equal(MOJIBAKE_SIGNATURES.length, 2);
  assert.deepEqual(MOJIBAKE_SIGNATURES[0].bytes, [0xc3, 0x82]);
  assert.deepEqual(MOJIBAKE_SIGNATURES[1].bytes, [0xc3, 0xa2]);
});
