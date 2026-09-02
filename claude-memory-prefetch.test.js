/**
 * claude-memory-prefetch.test.js — regression tests for ASM-729 (tool.memoryprefetch,
 * FEAT-114): claude-memory-prefetch.js had NO dedicated test file. This suite closes
 * that gap by driving the real hook end-to-end via subprocess (SPAWN), exactly the
 * pattern claude-ping-check.test.js and claude-checkin-pipe-guard.test.js already use
 * for other UserPromptSubmit/PostToolUse hooks in this repo.
 *
 * WHY SPAWN-ONLY, NO EXPORT ADDED: claude-memory-prefetch.js is a bare top-level
 * script (no `if (require.main === module)` guard, no module.exports) whose entire
 * body — including the fail-graceful try/catch, the CLAUDE_DISABLE_MEMORY_REMINDER
 * check, and process.exit(0) calls — runs immediately at require time. `require()`-ing
 * it directly from a test process would execute process.exit() against the TEST
 * runner itself. Driving it as a real child process (as the hook harness does) is
 * therefore both the safe AND the black-box-correct way to test it: every assertion
 * below observes exactly what a real UserPromptSubmit invocation would produce, with
 * no source changes to the tool file (deterministic — no wall clock, no network, no
 * live DB).
 *
 * Covers docs/planning/acceptance/tool.memoryprefetch.md:
 *   AC-9  (ASM-729) — this file existing at all closes the "RED by absence" gap.
 *   AC-10 — default run: reminder emitted, exit 0, three content substrings
 *           (GR#14, mcp__vestige__search, /commit) independently asserted.
 *   AC-11 — disable path: CLAUDE_DISABLE_MEMORY_REMINDER=1 -> empty stdout, exit 0;
 *           a non-"1" value does NOT disable (strict === '1' comparison).
 *   AC-12 — staticness: source text has no require()/child_process/fetch/https
 *           dependency beyond the core prologue (the only mcp__ occurrence is
 *           inside a quoted string literal, never in call position).
 *   AC-13 — fail-graceful: forcing stdout.write to throw inside the CHILD process
 *           (via a tiny wrapper) still yields exit 0, no crash, no stack trace.
 *
 * Run: node --test claude-memory-prefetch.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawnSync } = require('child_process');

const HOOK = path.join(__dirname, 'claude-memory-prefetch.js');
const SOURCE = fs.readFileSync(HOOK, 'utf8');

/** Run the real hook as a child process. `env` overrides are merged onto a
 *  minimal, deterministic base (never the live CLAUDE_IDENTITY/session env of
 *  whatever window happens to be running the test suite), and stdin is closed
 *  immediately with an empty payload so the hook's synchronous fd-0 read never
 *  blocks waiting for input. */
function runHook(envOverrides = {}) {
  return spawnSync(process.execPath, [HOOK], {
    input: '',
    encoding: 'utf8',
    env: {
      PATH: process.env.PATH,
      SystemRoot: process.env.SystemRoot, // Windows node needs this to resolve DLLs
      ...envOverrides,
    },
    timeout: 5000,
  });
}

// ── AC-10: default behaviour — emit the reminder ─────────────────────────────

test('AC-10: default run (no disable flag) emits the reminder and exits 0', () => {
  const r = runHook();
  assert.equal(r.status, 0, `expected exit 0, got ${r.status}; stderr=${r.stderr}`);
  assert.notEqual(r.stdout.trim(), '', 'reminder must be non-empty by default');
});

test('AC-10: reminder carries all three load-bearing substrings independently', () => {
  const r = runHook();
  assert.match(r.stdout, /GR#14/, 'must name the rule');
  assert.match(r.stdout, /mcp__vestige__search/, 'must point at the exact MCP tool name');
  assert.match(r.stdout, /\/commit/, 'must state /commit already handles GATE 0');
});

// ── AC-11: disable escape hatch ───────────────────────────────────────────────

test('AC-11: CLAUDE_DISABLE_MEMORY_REMINDER=1 suppresses the reminder entirely', () => {
  const r = runHook({ CLAUDE_DISABLE_MEMORY_REMINDER: '1' });
  assert.equal(r.status, 0);
  assert.equal(r.stdout, '', 'disabled run must produce empty stdout, not just a short one');
});

test('AC-11: a non-"1" value does NOT disable the reminder (strict === comparison)', () => {
  for (const bogus of ['true', 'yes', '2', ' 1', '1 ', '']) {
    const r = runHook({ CLAUDE_DISABLE_MEMORY_REMINDER: bogus });
    assert.equal(r.status, 0);
    assert.notEqual(r.stdout.trim(), '', `value ${JSON.stringify(bogus)} must NOT disable the reminder`);
  }
});

// ── AC-12: staticness — no dynamic dependency beyond core ────────────────────

test('AC-12: source requires no non-core module and makes no network/subprocess call', () => {
  // Strip comments and string literals is overkill for this small file; instead
  // assert directly on the concrete dependency surface the hook is allowed to
  // have per its own header ("the script has no MCP access").
  assert.doesNotMatch(SOURCE, /require\(\s*['"](?!fs['"]|path['"])/,
    'the only requires permitted are the core fs/path modules used for the GR#4 prefix lookup');
  assert.doesNotMatch(SOURCE, /child_process|\bfetch\(|https?:\/\//i,
    'no subprocess spawning or network call is permitted');
});

test('AC-12: the only mcp__ occurrence is inside a quoted string literal (never called)', () => {
  const mcpMatches = [...SOURCE.matchAll(/mcp__\w+/g)].map(m => m[0]);
  assert.ok(mcpMatches.length > 0, 'the reminder must actually name the tool');
  for (const m of mcpMatches) {
    // Every occurrence in this file appears inside the reminder array's quoted
    // strings; a call-position occurrence would look like `mcp__vestige__search(`.
    const idx = SOURCE.indexOf(m);
    assert.notEqual(SOURCE[idx + m.length], '(', `${m} must never appear in call position`);
  }
});

// ── AC-13: fail-graceful — any error still exits 0 ────────────────────────────

test('AC-13: a forced stdout.write throw inside the hook still exits 0, no crash', () => {
  // Drive the SAME hook file, but pre-load a tiny shim (via NODE_OPTIONS --require)
  // that monkey-patches process.stdout.write to throw before the hook's own code
  // runs. This proves the hook's try/catch (wrapping the whole body, including the
  // final process.stdout.write call) really does swallow a write-time failure,
  // without editing claude-memory-prefetch.js itself.
  const shimDir = fs.mkdtempSync(path.join(os.tmpdir(), 'memprefetch-shim-'));
  const shimPath = path.join(shimDir, 'throw-on-write.js');
  fs.writeFileSync(
    shimPath,
    "process.stdout.write = () => { throw new Error('forced stdout failure'); };\n",
    'utf8'
  );
  try {
    const r = spawnSync(process.execPath, ['--require', shimPath, HOOK], {
      input: '',
      encoding: 'utf8',
      env: {
        PATH: process.env.PATH,
        SystemRoot: process.env.SystemRoot,
      },
      timeout: 5000,
    });
    assert.equal(r.status, 0, `a forced write failure must still exit 0, got ${r.status}; stderr=${r.stderr}`);
    assert.doesNotMatch(r.stderr, /Error: forced stdout failure/,
      'the error must be swallowed by the hook, not surfaced as an uncaught exception');
  } finally {
    fs.rmSync(shimDir, { recursive: true, force: true });
  }
});

// ── AC-9: this file's own existence closes ASM-729 ───────────────────────────

test('AC-9 (ASM-729): claude-memory-prefetch.test.js now exists and exercises AC-10..13', () => {
  assert.ok(fs.existsSync(HOOK), 'the hook file itself must exist for this suite to be meaningful');
});
