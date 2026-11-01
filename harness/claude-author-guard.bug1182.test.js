/**
 * claude-author-guard.bug1182.test.js - BUG-1182: command-substitution detection
 * gaps found by the BUG-1166 r1 round. One test per context so removing
 * support for any single context turns exactly that named test red.
 * Detection only: strings go to guard.hasDecoderFedShell / normalizeContinuations,
 * nothing is executed.
 *
 *  gap 1  backtick-newline: a bash substitution closer vs a PowerShell continuation
 *  gap 2  command positions: newline, !, if, then, do, time, env, unquoted eval,
 *         a double-quoted command word (both substitution forms)
 *  gap 3  an unterminated backtick outside quotes is refused only if a decoder follows
 *  gap 4  python -c with a single-quoted backtick is not shell reach
 *  adv.   isEscapedAt pin (r2 advisory)
 */
'use strict';
const test = require('node:test');
const assert = require('node:assert');
const guard = require('./claude-author-guard.js');

const T = String.fromCharCode(96); // backtick
const b64 = Buffer.from('cd internal/engine && git add evil.go && git commit -m x', 'utf8').toString('base64');
const P = `echo '${b64}' | base64 -d`;
const BT = T + P + T;
const DP = `$(${P})`;

function refused(name, cmd) {
  test(`BUG-1182 refused - ${name}`, () => {
    assert.equal(guard.hasDecoderFedShell(cmd), true, cmd);
  });
}
function clear(name, cmd) {
  test(`BUG-1182 stays clear - ${name}`, () => {
    assert.equal(guard.hasDecoderFedShell(cmd), false, cmd);
  });
}

// --- gap 1: backtick-newline ---------------------------------------------
refused('gap1 backtick decoder closing at end of line, piped to bash', `${BT}\n`);
refused('gap1 backtick decoder at end of line then more lines', `true\n${BT}\necho done`);
refused('gap1 backtick decoder at end of line, CRLF', `${BT}\r\necho done`);
refused('gap1 heredoc to bash with decoder at end of line', `bash <<EOF\n${BT}\nEOF`);
refused('gap1 backtick decoder at end of line inside bash -c body', `bash -c "true\n${BT}\n"`);
test('BUG-1182 gap1: PowerShell continuation is still collapsed by normalizeContinuations', () => {
  assert.equal(guard.normalizeContinuations(`git commit ${T}\n -m x`), 'git commit   -m x');
});
clear('gap1 real PowerShell continuation', `git commit ${T}\n  -m "msg"`);
clear('gap1 bash substitution at end of line that is not a decoder', `cd ${T}git rev-parse --show-toplevel${T}\nls`);

// --- gap 2: command positions --------------------------------------------
const positions = [
  ['newline', (s) => `echo hi\n${s}`],
  ['!', (s) => `! ${s}`],
  ['if', (s) => `if ${s}; then true; fi`],
  ['then', (s) => `if true; then ${s}; fi`],
  ['do', (s) => `for x in 1; do ${s}; done`],
  ['time', (s) => `time ${s}`],
  ['env', (s) => `env ${s}`],
  ['unquoted eval', (s) => `eval ${s}`],
  ['double-quoted command word piped', (s) => `"${s}" | cat`],
  ['double-quoted command word after ;', (s) => `true; "${s}"`],
];
for (const [name, wrap] of positions) {
  refused(`gap2 dollar-paren after ${name}`, wrap(DP));
  refused(`gap2 backtick after ${name}`, wrap(BT));
}
clear('gap2 echo then dollar-paren is data', `echo then ${DP}`);
clear('gap2 echo double-quoted dollar-paren is data', `echo "${DP}"`);
clear('gap2 echo eval backtick is data', `echo eval ${BT}`);
clear('gap2 double-quoted dollar-paren inside a longer word', `echo "x ${DP} y"`);
clear('gap2 newline then echo of quoted substitution', `true\necho "${BT}"`);

// --- gap 3: unterminated backtick ----------------------------------------
// gap 3 narrowed after r1: refused only when the text after the backtick ends in a decoder
refused('gap3 unterminated backtick + decoder, bare', `${T}${P}`);
refused('gap3 unterminated backtick + decoder in a commit command', `git commit -m ${T}${P}`);
refused('gap3 unterminated backtick + decoder after a closed pair', `echo ${T}a${T} ${T}${P}`);
// r1 pins: PowerShell treats a lone backtick as its escape character, so these must stay clear
// exact text of the real-history command (realps-cmd.txt, whole file), inlined: temp/ is not in the repo.
// Its lone backtick-quote escapes made the unconditional unterminated-backtick rule refuse it (r1 finding).
clear('gap3 PowerShell real-history multi-line command with backtick-quote escapes', `$sp='E:\\git\\Metropolis\\temp\\claude\\E--git-Metropolis\\24181473-bdb0-4142-8f4c-9da08af7feac\\scratchpad'
foreach ($f in 'claude-secret-checker','claude-bow-ref-check','claude-pre-push-check') {
 cmd /c "git show HEAD:harness/$f.js > ${T}"$sp\\$f.orig.js${T}""
 Copy-Item "$sp\\$f.orig.js" "harness\\$f.js" -Force
}
foreach ($f in 'secret-checker','bow-ref-check','pre-push-check') { $o = node --test "harness/claude-$f.test.js" 2>&1 | Out-String; "== $f exit $LASTEXITCODE"; ($o -split "${T}n") | Select-String -Pattern '^not ok|✖.*BUG-1118|BUG-1118.*(must|expected|seen)|AssertionError|^# (pass|fail)' | Select -First 6 }`);
clear('gap3 PowerShell backtick-space in a path', `Get-ChildItem C:\\Program${T} Files`);
clear('gap3 PowerShell backtick-t escape', `Write-Host a${T}tb`);
clear('gap3 bare unterminated backtick without a decoder', `echo ${T}hi`);
clear('gap3 unterminated backtick inside double quotes', `git commit -m "it ${T}is"`);
clear('gap3 unterminated backtick inside single quotes', `git commit -m 'it ${T}is'`);
clear('gap3 unterminated backtick in a comment', `true # ${T}x`);
clear('gap3 unterminated backtick in a quoted heredoc', `cat <<'EOF'\nuses ${T}x\nEOF`);
clear('gap3 escaped backtick is literal', `echo \\${T}x`);

// --- gap 4: python -c single-quoted backtick -----------------------------
clear('gap4 python -c single-quoted backtick', `python -c 'print("${T}x${T}")'`);
clear('gap4 python3 -c single-quoted lone backtick', `python3 -c 'print(1) # ${T}'`);
refused('gap4 python -c os.system still refused', `python -c 'import os; os.system("ls")'`);
refused('gap4 perl backtick still refused', `perl -e 'print ${T}ls${T}'`);

// --- advisory: isEscapedAt pin -------------------------------------------
// A nested escaped backtick decoder: the outer pair is a substitution whose
// body holds an escaped inner pair. With isEscapedAt forced false the closer
// search stops at the first escaped backtick and the inner decoder is missed.
// (no pipe to a shell here: only the backtick path can catch it)
refused('adv nested escaped-backtick decoder', `${T}\\${T}${P}\\${T}${T}`);
refused('adv nested escaped-backtick decoder after ;', `${T}true; \\${T}${P}\\${T}${T}`);
clear('adv escaped backticks alone are literal data', `echo \\${T}${P}\\${T} | cat`);

// --- no over-flag on ordinary commands -----------------------------------
clear('commit message double-quoted backticks', `git commit -m "fix ${T}foo${T} handling"`);
clear('commit message single-quoted backticks', `git commit -m 'fix ${T}foo${T} handling'`);
clear('commit message heredoc via -F -', `git commit -F - <<'EOF'\nfix ${T}foo${T}\n\nbody ${T}bar${T}\nEOF`);
clear('echo with backticks', `echo "see ${T}foo${T}"`);
clear('printf with backticks', `printf '%s\\n' "${T}foo${T}"`);
clear('node -e template literal', `node -e 'console.log(${T}a${T})'`);
clear('quoted heredoc to cat', `cat <<'EOF'\n${BT}\nEOF`);
clear('which node', `${T}which node${T}`);
clear('cd with git rev-parse', `cd ${T}git rev-parse --show-toplevel${T} && ls`);
clear('multi-line plain script', `set -e\nls\n\ngit status\n`);
