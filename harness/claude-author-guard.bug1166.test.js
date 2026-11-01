/**
 * claude-author-guard.bug1166.test.js - BUG-1166: backtick command substitution
 * must be recognised exactly like dollar-paren substitution. Detection only:
 * strings go to guard.hasDecoderFedShell, nothing is executed.
 * Run: node --test harness/claude-author-guard.bug1166.test.js
 */
'use strict';
const test = require('node:test');
const assert = require('node:assert');
const guard = require('./claude-author-guard.js');

const T = String.fromCharCode(96); // backtick
const payload = 'cd internal/engine && git add evil.go && git commit -m x';
const b64 = Buffer.from(payload, 'utf8').toString('base64');
const bt = (s) => T + s + T;

test('BUG-1166: backtick twins of the dollar-paren decoder cases are refused', () => {
  for (const cmd of [
    `bash -c "${bt(`echo '${b64}' | base64 -d`)}"`,
    `${bt(`echo '${b64}' | base64 -d`)} | bash`,
    `${bt(`base64 -d <<< '${b64}'`)} | bash`,
  ]) {
    assert.equal(guard.hasDecoderFedShell(cmd), true, `must flag: ${cmd}`);
  }
});

test('BUG-1166: backtick substitution in an echo argument stays data (twin of the dollar-paren control)', () => {
  assert.equal(guard.hasDecoderFedShell(`echo "${bt(`echo '${b64}' | base64 -d`)}"`), false);
});

test('BUG-1166: single-quoted backticks are inert', () => {
  const cmd = `echo '${bt(`echo ${b64} | base64 -d`)}' | cat`;
  assert.equal(guard.hasDecoderFedShell(cmd), false, cmd);
  // (bash -c '<backticks>' is NOT inert: the body is itself run by a shell, so
  // it is refused exactly like bash -c '$(...)'.)
  assert.equal(guard.hasDecoderFedShell(`bash -c '${bt(`echo ${b64} | base64 -d`)}'`), true);
});

test('BUG-1166: backslash-escaped backticks are literal', () => {
  const e = '\\' + T;
  const cmd = `${e}echo '${b64}' | base64 -d${e} | cat`;
  assert.equal(guard.hasDecoderFedShell(cmd), false, cmd);
});
