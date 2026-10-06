/**
 * claude-worktree-guard.test.js — unit + spawn tests for the GR#24 working-tree
 * protection guard (BUG-215). Pure classifier logic needs no repo; the spawn
 * tests drive the real hook with a stdin payload and assert allow (empty
 * stdout) vs deny (permissionDecision:"deny") — no live index is touched.
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const path = require('path');
const { spawnSync } = require('child_process');

const {
  isDestructiveInvocation,
  argTokens,
  looksLikePath,
} = require('./claude-worktree-guard.js');

const tk = (s) => argTokens(s);

// --- checkout: the command that caused the 211-line loss ---
test('checkout -- <path> is destructive (the exact 2026-08-13 loss)', () => {
  assert.equal(isDestructiveInvocation('checkout', tk('-- claude-destructive-guard.js')), true);
});
test('checkout . is destructive', () => {
  assert.equal(isDestructiveInvocation('checkout', tk('.')), true);
});
test('checkout -f is destructive', () => {
  assert.equal(isDestructiveInvocation('checkout', tk('-f main')), true);
});
test('checkout <path-shaped bareword> is destructive', () => {
  assert.equal(isDestructiveInvocation('checkout', tk('internal/engine/foo.go')), true);
  assert.equal(isDestructiveInvocation('checkout', tk('README.md')), true);
});
test('checkout <branch> is SAFE (must not block a branch switch)', () => {
  assert.equal(isDestructiveInvocation('checkout', tk('main')), false);
  assert.equal(isDestructiveInvocation('checkout', tk('develop')), false);
});
test('checkout -b <newbranch> is SAFE', () => {
  assert.equal(isDestructiveInvocation('checkout', tk('-b feature/x')), false);
});

// --- restore ---
test('restore <path> is destructive; restore --staged is SAFE', () => {
  assert.equal(isDestructiveInvocation('restore', tk('foo.go')), true);
  assert.equal(isDestructiveInvocation('restore', tk('--staged foo.go')), false);
  assert.equal(isDestructiveInvocation('restore', tk('--staged --worktree foo.go')), true);
});

// --- reset ---
test('reset --hard/--keep/--merge destructive; soft/mixed/default SAFE', () => {
  assert.equal(isDestructiveInvocation('reset', tk('--hard HEAD~1')), true);
  assert.equal(isDestructiveInvocation('reset', tk('--keep HEAD')), true);
  assert.equal(isDestructiveInvocation('reset', tk('--soft HEAD~1')), false);
  assert.equal(isDestructiveInvocation('reset', tk('HEAD')), false); // mixed (default)
  assert.equal(isDestructiveInvocation('reset', tk('--mixed HEAD')), false);
});

// --- clean ---
test('clean -f / -fd destructive; -n dry-run SAFE', () => {
  assert.equal(isDestructiveInvocation('clean', tk('-f')), true);
  assert.equal(isDestructiveInvocation('clean', tk('-fd')), true);
  assert.equal(isDestructiveInvocation('clean', tk('-xdf')), true);
  assert.equal(isDestructiveInvocation('clean', tk('--force')), true);
  assert.equal(isDestructiveInvocation('clean', tk('-n')), false);
  assert.equal(isDestructiveInvocation('clean', tk('--dry-run')), false);
});

// --- stash ---
test('stash / push / save destructive; list/show/pop SAFE', () => {
  assert.equal(isDestructiveInvocation('stash', tk('')), true); // bare == push
  assert.equal(isDestructiveInvocation('stash', tk('push')), true);
  assert.equal(isDestructiveInvocation('stash', tk('save "wip"')), true);
  assert.equal(isDestructiveInvocation('stash', tk('list')), false);
  assert.equal(isDestructiveInvocation('stash', tk('show')), false);
  assert.equal(isDestructiveInvocation('stash', tk('pop')), false);
});

test('looksLikePath basics', () => {
  assert.equal(looksLikePath('.'), true);
  assert.equal(looksLikePath('a/b.go'), true);
  assert.equal(looksLikePath('foo.md'), true);
  assert.equal(looksLikePath('main'), false);
});

// --- spawn: end-to-end through the real hook ---
function runGuard(command, env) {
  return spawnSync(process.execPath, [path.join(__dirname, 'claude-worktree-guard.js')], {
    input: JSON.stringify({ tool_name: 'Bash', hook_event_name: 'PreToolUse', tool_input: { command } }),
    encoding: 'utf8',
    env: { ...process.env, CLAUDE_ALLOW_WORKTREE_RESET: '', ...(env || {}) },
  });
}

function isDeny(r) {
  if (r.status !== 0) return false;
  if (!r.stdout.trim()) return false;
  try {
    return JSON.parse(r.stdout).hookSpecificOutput.permissionDecision === 'deny';
  } catch {
    return false;
  }
}

test('SPAWN: git checkout -- <file> is DENIED (the loss command)', () => {
  assert.equal(isDeny(runGuard('git checkout -- claude-destructive-guard.js')), true);
});
test('SPAWN: git reset --hard is DENIED', () => {
  assert.equal(isDeny(runGuard('git reset --hard HEAD')), true);
});
test('SPAWN: git clean -fd is DENIED', () => {
  assert.equal(isDeny(runGuard('git clean -fd')), true);
});
test('SPAWN: git stash is DENIED', () => {
  assert.equal(isDeny(runGuard('git stash')), true);
});
test('SPAWN: bypass via a wrapper/chain is still caught (git status; git checkout -- x)', () => {
  assert.equal(isDeny(runGuard('git status && git checkout -- foo.go')), true);
});
test('SPAWN: git -c x=y checkout -- foo.go is DENIED (global-option run does not hide the verb)', () => {
  assert.equal(isDeny(runGuard('git -c advice.detachedHead=false checkout -- foo.go')), true);
});
test('SPAWN: git checkout main (branch switch) is ALLOWED', () => {
  assert.equal(runGuard('git checkout main').stdout.trim(), '');
});
test('SPAWN: git commit -m "x" is ALLOWED (not a destructive verb)', () => {
  assert.equal(runGuard('git commit -m "x"').stdout.trim(), '');
});
test('SPAWN: operator override CLAUDE_ALLOW_WORKTREE_RESET=1 allows a reset --hard', () => {
  assert.equal(runGuard('git reset --hard', { CLAUDE_ALLOW_WORKTREE_RESET: '1' }).stdout.trim(), '');
});
test('SPAWN: non-git command is ALLOWED', () => {
  assert.equal(runGuard('rm -rf /tmp/scratch').stdout.trim(), '');
});

test('checkout of a tag/version ref is SAFE (not a path — the v1.2 false-positive fix)', () => {
  assert.equal(isDestructiveInvocation('checkout', tk('v1.2')), false);
  assert.equal(isDestructiveInvocation('checkout', tk('v1.2.3')), false);
  assert.equal(isDestructiveInvocation('checkout', tk('release-2024.08')), false);
  // but real source files still read as paths
  assert.equal(isDestructiveInvocation('checkout', tk('main.go')), true);
  assert.equal(isDestructiveInvocation('checkout', tk('config.yaml')), true);
});
