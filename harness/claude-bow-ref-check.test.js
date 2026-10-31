/**
 * claude-bow-ref-check.test.js — BUG-1118 regression: the SAFE-338/448 quotepath/-z fix
 * never reached this guard. With core.quotepath at its default (true) a staged non-ASCII
 * name under an enforced path (cmd/ internal/ data/) arrived quote-wrapped, failed
 * ENFORCED_PATH_RE and the commit was silently allowed with no [mkey] check.
 *
 * Fixture mechanics: the hook resolves ROOT = join(__dirname,'..'), so the tool is COPIED
 * into a throwaway repo's harness/ (with inert stubs for its two DB-backed requires — the
 * path under test never reaches the DB). Observable: `git commit -F file` carries no -m body,
 * so a commit that touches an enforced path yields the "could not extract the commit message"
 * warn-allow JSON, while a commit judged non-enforced is allowed with NO output at all.
 *
 * Run: node --test claude-bow-ref-check.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawnSync } = require('child_process');

const TOOL_SOURCE_PATH = path.join(__dirname, 'claude-bow-ref-check.js');

function makeRepo() {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'bow-ref-quotepath-'));
  const run = (args) => {
    const r = spawnSync('git', args, { cwd: dir, encoding: 'utf8' });
    assert.equal(r.status, 0, `git ${args.join(' ')} failed: ${r.stderr}`);
  };
  run(['init', '-q']);
  run(['config', 'core.quotepath', 'true']);
  fs.mkdirSync(path.join(dir, 'harness'));
  fs.copyFileSync(TOOL_SOURCE_PATH, path.join(dir, 'harness', 'claude-bow-ref-check.js'));
  fs.writeFileSync(path.join(dir, 'harness', 'claude-db.js'), 'module.exports = { connect: async () => { throw new Error("stub"); } };\n');
  fs.writeFileSync(path.join(dir, 'harness', 'claude-bow.js'), 'module.exports = { findItemByRef: async () => null };\n');
  return { dir, run };
}

function runHook(dir, command) {
  return spawnSync(process.execPath, [path.join(dir, 'harness', 'claude-bow-ref-check.js')], {
    input: JSON.stringify({ tool: 'Bash', tool_input: { command } }),
    encoding: 'utf8',
    env: { ...process.env, CLAUDE_DISABLE_BOW_REF: '' },
    timeout: 15000,
  });
}

test('BUG-1118: a staged internal/café.go (core.quotepath default) is SEEN as an enforced path, not silently allowed', () => {
  const { dir, run } = makeRepo();
  try {
    fs.mkdirSync(path.join(dir, 'internal'));
    fs.writeFileSync(path.join(dir, 'internal', 'café.go'), 'package x\n', 'utf8');
    run(['add', 'internal/café.go']);
    const r = runHook(dir, 'git commit -F msg.txt');
    assert.equal(r.status, 0, r.stderr);
    assert.match(r.stdout, /BOW REF CHECK/, `the accented enforced-path file must reach the message check; stdout=${JSON.stringify(r.stdout)} stderr=${JSON.stringify(r.stderr)}`);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('BUG-1118 CONTROL: a staged non-enforced accented docs/café.md is still silently allowed (no over-block)', () => {
  const { dir, run } = makeRepo();
  try {
    fs.mkdirSync(path.join(dir, 'docs'));
    fs.writeFileSync(path.join(dir, 'docs', 'café.md'), '# x\n', 'utf8');
    run(['add', 'docs/café.md']);
    const r = runHook(dir, 'git commit -F msg.txt');
    assert.equal(r.status, 0, r.stderr);
    assert.equal(r.stdout, '', `a non-enforced path must stay silent; stdout=${JSON.stringify(r.stdout)}`);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

// BUG-1118 round r1 (Destructive-BUG1118-quotepath-r1): the fail-closed quote-wrap backstop was
// unreachable from the accented test above (-z means git never quotes), so deleting it left every
// test green. A name that LITERALLY begins with a double quote (stageable via update-index once
// core.protectNTFS is off) is the only input that exercises it: it must count as enforced.
test('BUG-1118 r1: a staged name that literally starts with a double quote trips the fail-closed backstop', () => {
  const { dir, run } = makeRepo();
  try {
    run(['config', 'core.protectNTFS', 'false']);
    const h = spawnSync('git', ['hash-object', '-w', '--stdin'], { cwd: dir, input: 'package x\n', encoding: 'utf8' });
    assert.equal(h.status, 0, h.stderr);
    run(['update-index', '--add', '--cacheinfo', `100644,${h.stdout.trim()},"internal/x.go"`]);
    const r = runHook(dir, 'git commit -F msg.txt');
    assert.equal(r.status, 0, r.stderr);
    assert.match(r.stdout, /BOW REF CHECK/, `a quote-led name must be treated as enforced (fail-closed); stdout=${JSON.stringify(r.stdout)}`);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});
