/**
 * claude-author-guard.bug1166-r1.test.js - BUG-1166 round r1 (attacker
 * opus-round-r1-bug1166). Per-context pins for backtick command substitution,
 * one test per context so removing support for any single context turns
 * exactly one named test red, plus the r1 regression pin.
 * Detection only: strings go to guard.hasDecoderFedShell, nothing is executed.
 *
 * Test "R1 regression" is RED on the r1 build by design: the backtick branch's
 * `i = j` skip jumps over a dollar-paren substitution INSIDE the backtick body,
 * so `true; $(decoder) ; true` wrapped in backticks - which bash runs, executing
 * the decoded bytes - was refused before the change and is allowed after it.
 */
'use strict';
const test = require('node:test');
const assert = require('node:assert');
const guard = require('./claude-author-guard.js');

const T = String.fromCharCode(96); // backtick
const b64 = Buffer.from('cd internal/engine && git add evil.go && git commit -m x', 'utf8').toString('base64');
const P = `echo '${b64}' | base64 -d`;
const BT = T + P + T;

const positives = [
  ['bare top-level', BT],
  ['top-level piped to bash', `${BT} | bash`],
  ['after ;', `true; ${BT} | bash`],
  ['after &&', `true && ${BT}`],
  ['after ||', `false || ${BT}`],
  ['after (', `(${BT})`],
  ['after {', `{ ${BT}; }`],
  ['pipe stage', `echo hi | ${BT}`],
  ['bash -c double-quoted body', `bash -c "${BT}"`],
  ['bash -c single-quoted body', `bash -c '${BT}'`],
  ['sh -c double-quoted body', `sh -c "${BT}"`],
  ['eval double-quoted body', `eval "${BT}"`],
  ['herestring decoder', `${T}base64 -d <<< '${b64}'${T} | bash`],
  ['inside a dollar-paren', `$( ${BT} )`],
];
for (const [name, cmd] of positives) {
  test(`BUG-1166 r1: backtick decoder refused - ${name}`, () => {
    assert.equal(guard.hasDecoderFedShell(cmd), true, cmd);
  });
}

const negatives = [
  ['echo double-quoted argument (data)', `echo "${BT}"`],
  ['echo single-quoted (literal)', `echo '${BT}' | cat`],
  ['assignment (data)', `x=${BT}`],
  ['commit message double-quoted', `git commit -m "note ${BT}"`],
  ['commit message single-quoted', `git commit -m 'note ${BT}'`],
  ['quoted heredoc to cat', `cat <<'EOF'\n${BT}\nEOF`],
  ['comment', `true # ${BT}`],
  ['escaped backticks are literal', `\\${T}${P}\\${T} | cat`],
  ['node -e single-quoted template literal in console.log(', `node -e 'console.log(${BT})'`],
  ['node -e single-quoted template literal after ;', `node -e 'let a=1;${BT}'`],
  ['node -e single-quoted template literal after {', `node -e 'if(1){${BT}}'`],
];
for (const [name, cmd] of negatives) {
  test(`BUG-1166 r1: backtick stays clear - ${name}`, () => {
    assert.equal(guard.hasDecoderFedShell(cmd), false, cmd);
  });
}

test('BUG-1166 r1 regression: a dollar-paren decoder inside a backtick body is still refused', () => {
  const cmd = `${T}true; $(${P}) ; true${T}`;
  assert.equal(guard.hasDecoderFedShell(cmd), true, cmd);
});
