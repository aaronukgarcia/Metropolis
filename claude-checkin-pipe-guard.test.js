/**
 * claude-checkin-pipe-guard.test.js — unit + spawn tests for the FEAT-107
 * checkin-pipe hygiene hook (claude-checkin-pipe-guard.js).
 *
 * Covers:
 *   - Pure predicate (isSuppressedCheckin): bare checkin is silent, every
 *     named suppressing sink ($null, Out-Null, >NUL, >/dev/null,
 *     Select-Object, Select-String, head, tail) warns, and unrelated
 *     commands (including ones that merely mention "checkin" as a word, or
 *     pipe an UNRELATED command to $null) never trigger it.
 *   - SPAWN: the real hook, driven end-to-end via stdin exactly like every
 *     other PreToolUse guard's own test file (see claude-worktree-guard.test.js),
 *     asserting the JSON warn-allow envelope vs silent (empty-stdout) allow.
 *   - Escape hatch (CLAUDE_DISABLE_CHECKIN_PIPE_GUARD=1) and malformed-input
 *     fail-open behaviour.
 *
 * This hook NEVER denies (warn-only, fail-open hygiene guard — see its own
 * header for why the FEAT-107 delivery split dropped the stakes of a piped
 * checkin from "data loss" to "hygiene nudge"), so there is no isDeny() here,
 * only isWarn()/isSilent().
 *
 * Run: node --test claude-checkin-pipe-guard.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const path = require('path');
const { spawnSync } = require('child_process');

const { isSuppressedCheckin } = require('./claude-checkin-pipe-guard.js');

// ── Pure predicate ───────────────────────────────────────────────────────────

test('bare checkin (no redirection at all) does not trigger', () => {
  assert.equal(isSuppressedCheckin('node claude-sync.js checkin'), false);
  assert.equal(isSuppressedCheckin('node claude-sync.js checkin --any'), false);
  assert.equal(isSuppressedCheckin('node claude-sync.js checkin --name Bill'), false);
});

test('checkin piped to PowerShell $null / Out-Null triggers', () => {
  assert.equal(isSuppressedCheckin('node claude-sync.js checkin > $null'), true);
  assert.equal(isSuppressedCheckin('node claude-sync.js checkin --any | Out-Null'), true);
  assert.equal(isSuppressedCheckin('node claude-sync.js checkin *> $null'), true);
});

test('checkin redirected to NUL (cmd.exe-style, case-insensitive) triggers', () => {
  assert.equal(isSuppressedCheckin('node claude-sync.js checkin > NUL'), true);
  assert.equal(isSuppressedCheckin('node claude-sync.js checkin > nul'), true);
  assert.equal(isSuppressedCheckin('node claude-sync.js checkin >nul'), true);
});

test('checkin redirected to /dev/null (POSIX) triggers', () => {
  assert.equal(isSuppressedCheckin('node claude-sync.js checkin > /dev/null'), true);
  assert.equal(isSuppressedCheckin('node claude-sync.js checkin 2>&1 > /dev/null'), true);
});

test('checkin piped through Select-Object / Select-String triggers', () => {
  assert.equal(isSuppressedCheckin('node claude-sync.js checkin | Select-Object -First 1'), true);
  assert.equal(isSuppressedCheckin('node claude-sync.js checkin | Select-String YOU'), true);
});

test('checkin piped through head / tail (the documented "tail-piping hangs" habit) triggers', () => {
  assert.equal(isSuppressedCheckin('node claude-sync.js checkin 2>&1 | head -80'), true);
  assert.equal(isSuppressedCheckin('node claude-sync.js checkin --any | tail -5'), true);
});

test('a suppressing sink on an UNRELATED command does not trigger', () => {
  assert.equal(isSuppressedCheckin('echo hello > $null'), false);
  assert.equal(isSuppressedCheckin('node claude-sync.js read | head -80'), false);
  assert.equal(isSuppressedCheckin('git status | Select-String modified'), false);
});

test('the word "checkin" appearing without the claude-sync.js invocation does not trigger', () => {
  assert.equal(isSuppressedCheckin('echo "please checkin later" > $null'), false);
});

test('flags between checkin and the sink still trigger (piping applies to the whole command)', () => {
  assert.equal(isSuppressedCheckin('node claude-sync.js checkin --name Bill --force --human-ok | Out-Null'), true);
});

test('empty/undefined/null command is handled without throwing', () => {
  assert.equal(isSuppressedCheckin(''), false);
  assert.equal(isSuppressedCheckin(undefined), false);
  assert.equal(isSuppressedCheckin(null), false);
});

// ── SPAWN: real hook, end-to-end via stdin ───────────────────────────────────

function runGuard(command, tool, env) {
  return spawnSync(process.execPath, [path.join(__dirname, 'claude-checkin-pipe-guard.js')], {
    input: JSON.stringify({ tool_name: tool || 'Bash', hook_event_name: 'PreToolUse', tool_input: { command } }),
    encoding: 'utf8',
    env: { ...process.env, CLAUDE_DISABLE_CHECKIN_PIPE_GUARD: '', ...(env || {}) },
  });
}

function isWarn(r) {
  if (r.status !== 0) return false;
  if (!r.stdout.trim()) return false;
  try {
    const out = JSON.parse(r.stdout);
    return out.hookSpecificOutput.permissionDecision === 'allow' && !!out.hookSpecificOutput.permissionDecisionReason;
  } catch {
    return false;
  }
}

function isSilent(r) {
  return r.status === 0 && r.stdout.trim() === '';
}

test('SPAWN (Bash): checkin | Out-Null WARNS, never denies', () => {
  const r = runGuard('node claude-sync.js checkin --any | Out-Null', 'Bash');
  assert.equal(isWarn(r), true, `expected a warn envelope, got: ${r.stdout}`);
  const parsed = JSON.parse(r.stdout);
  assert.notEqual(parsed.hookSpecificOutput.permissionDecision, 'deny', 'this hook must NEVER deny (fail-open hygiene guard)');
  assert.match(parsed.hookSpecificOutput.permissionDecisionReason, /read in full|UNREAD/i);
});

test('SPAWN (PowerShell): checkin > $null WARNS via the PowerShell matcher tool_name too', () => {
  const r = runGuard('node claude-sync.js checkin > $null', 'PowerShell');
  assert.equal(isWarn(r), true, `expected a warn envelope, got: ${r.stdout}`);
});

test('SPAWN: bare checkin (no redirection) is SILENT — exit 0, empty stdout', () => {
  const r = runGuard('node claude-sync.js checkin --any', 'Bash');
  assert.equal(isSilent(r), true, `expected silent allow, got: ${r.stdout}`);
});

test('SPAWN: an unrelated command is SILENT even if it redirects to $null', () => {
  const r = runGuard('node claude-sync.js read > $null', 'Bash');
  assert.equal(isSilent(r), true, `expected silent allow, got: ${r.stdout}`);
});

test('SPAWN: escape hatch CLAUDE_DISABLE_CHECKIN_PIPE_GUARD=1 suppresses the warning entirely', () => {
  const r = runGuard('node claude-sync.js checkin --any | Out-Null', 'Bash', { CLAUDE_DISABLE_CHECKIN_PIPE_GUARD: '1' });
  assert.equal(isSilent(r), true, `escape hatch should silence the hook, got: ${r.stdout}`);
});

test('SPAWN: malformed JSON on stdin fails open (exit 0, no crash, no warn)', () => {
  const r = spawnSync(process.execPath, [path.join(__dirname, 'claude-checkin-pipe-guard.js')], {
    input: 'not valid json{{{',
    encoding: 'utf8',
    env: { ...process.env, CLAUDE_DISABLE_CHECKIN_PIPE_GUARD: '' },
  });
  assert.equal(r.status, 0, `must fail open (exit 0) on malformed input, got status ${r.status}`);
  assert.equal(r.stdout.trim(), '', 'malformed input must produce no warn output');
});

test('SPAWN: missing tool_input.command entirely fails open silently', () => {
  const r = spawnSync(process.execPath, [path.join(__dirname, 'claude-checkin-pipe-guard.js')], {
    input: JSON.stringify({ tool_name: 'Bash', hook_event_name: 'PreToolUse' }),
    encoding: 'utf8',
    env: { ...process.env, CLAUDE_DISABLE_CHECKIN_PIPE_GUARD: '' },
  });
  assert.equal(r.status, 0);
  assert.equal(r.stdout.trim(), '');
});
