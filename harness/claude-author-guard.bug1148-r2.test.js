/**
 * claude-author-guard.bug1148-r2.test.js — BUG-1148 round r2 (opus-round-r2-bug1148)
 * kept adversarial cases against the node-only shellReachIn() narrowing.
 *
 * REGRESSIONS (flagged at HEAD ab6633a/b55223e, cleared by the r1 build):
 *  - an UNESCAPED backtick inside a DOUBLE-QUOTED `node -e "..."` body is BASH
 *    command substitution, executed by the shell before node ever runs - not a
 *    JS template literal. With a decoder inside it the decoded text runs as a
 *    command (proved with a harmless echo payload in the r2 scratch run).
 *  - `<receiver>/**\/.exec(` (block comment) and `(c)/1/.exec(` (division) are
 *    not RegExp receivers; the `/flags.exec(` blanker removes real exec( calls.
 * RELIEF (must stay clear): single-quoted bodies and backslash-escaped
 * backticks are genuine template literals; a real /re/.exec( is RegExp.exec.
 * CONTROLS: non-node runtime backticks still flag (runtime-name capture).
 *
 * Run: node test runner on this file (node --test <file>).
 */
'use strict';
const test = require('node:test');
const assert = require('node:assert');
const guard = require('./claude-author-guard.js');

const T = String.fromCharCode(96); // backtick
const D = '$';
const B64 = Buffer.from('git commit -m x').toString('base64');

test('BUG-1148 r2: unescaped backtick in a double-quoted node -e body is bash command substitution and still flags', () => {
  const flagged = [
    `node -e "x=${T}${D}(echo ${B64} | base64 -d)${T}"`,
    `node -e "console.log('${T}${D}(echo ${B64} | base64 -d)${T}')"`,
  ];
  for (const cmd of flagged) {
    assert.equal(guard.hasDecoderFedShell(cmd), true, `must still flag: ${cmd}`);
  }
});

test('BUG-1148 r2: the /flags.exec( blanker must not swallow a comment- or division-adjacent real exec( call', () => {
  const flagged = [
    `node -e "const c=require('child_'+'process');c/**/.exec('git commit -m x')"`,
    `node -e "const c=require('child_'+'process');c/*g*/.exec('git commit -m x')"`,
  ];
  for (const cmd of flagged) {
    assert.equal(guard.hasDecoderFedShell(cmd), true, `must still flag: ${cmd}`);
  }
});

test('BUG-1148 r2: genuine template literals and RegExp.exec stay clear (the intended relief)', () => {
  const allowed = [
    `node -e 'const fs=require("fs");let s=fs.readFileSync("a.md","utf8");s=s.replace(/x/,${T}y${T});fs.writeFileSync("a.md",s)'`,
    `node -e 'x=${T}git commit${T}'`,
    `node -e "x=\\${T}inline code\\${T}"`,
    `node -e "const m=/a(b)/.exec('ab');console.log(m)"`,
    `node.exe -e "const m=/a/g.exec('a')"`,
  ];
  for (const cmd of allowed) {
    assert.equal(guard.hasDecoderFedShell(cmd), false, `must stay clear: ${cmd}`);
  }
});

test('BUG-1148 r2: runtime-name capture keeps non-node backtick exec flagged', () => {
  const flagged = [
    `PERL -e 'print ${T}git commit -m x${T}'`,
    `perl.exe -e 'print ${T}git commit -m x${T}'`,
    `ruby -e 'puts ${T}git commit -m x${T}'`,
    `php -r 'echo ${T}git commit -m x${T};'`,
    `node -e "console.log(1)"; perl -e 'print ${T}git commit -m x${T}'`,
    `(perl -e 'print ${T}git commit -m x${T}')`,
    `perl -e 'print ${T}git commit${T}; # node x'`,
    `node -e "require('child_process').execSync('git commit -m x')"`,
    `node -e "const c=require('child_'+'process');c/**/.execSync('git commit -m x')"`,
  ];
  for (const cmd of flagged) {
    assert.equal(guard.hasDecoderFedShell(cmd), true, `must still flag: ${cmd}`);
  }
});

// r3 (opus-round-r3-bug1148): in a bash double-quoted string `\\` is an escaped
// BACKSLASH, so a backtick after it is LIVE command substitution (bash ran it
// in the r3 inert-echo check). Only a backtick after an ODD backslash run is a
// literal template tick. Pre-fix flagged these; the r3 build cleared the pair.
test('BUG-1148 r3: a backtick after an escaped backslash in a dq node -e body is live substitution and flags', () => {
  const BS = '\\';
  const flagged = [
    `node -e "x=${BS}${BS}${T}echo hi${BS}${BS}${T}"`,
    `node -e "x=${BS}${BS}${T}${D}(echo ${B64} | base64 -d)${BS}${BS}${T}"`,
  ];
  for (const cmd of flagged) {
    assert.equal(guard.hasDecoderFedShell(cmd), true, `must still flag: ${cmd}`);
  }
  // odd run: \\\` is backslash + escaped tick - literal, stays clear
  assert.equal(guard.hasDecoderFedShell(`node -e "x=${BS}${BS}${BS}${T}a${BS}${BS}${BS}${T}"`), false);
});
