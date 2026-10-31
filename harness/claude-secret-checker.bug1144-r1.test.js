// BUG-1144 round r1 (opus-round-r1-bug1144) attack tests - added by the independent attacker.
// (a) and (b) pass on the build and kill two mutants the builder's tests let survive:
//     (a) deleting the runScan parse-failure backstop, (b) deleting the \ooo octal decoder.
// (c) and (d) are RED on the build: the content scan is still silently skipped when a repo
//     textconv driver rewrites the diff, or when a file diffs as binary (a `-diff` attribute or a
//     NUL byte) - "Binary files ... differ" carries no +++ header, so nothing is scanned and
//     nothing fails closed. Make them green (e.g. --no-textconv, plus --text or a fail-closed
//     finding for binary-diffed staged files) before the round can accept.
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawnSync } = require('child_process');

// Fabricated (never real) AKIA-shaped fixture. Uses the exact low-entropy
// literal pieces already committed in claude-secret-checker.test.js (~line
// 814): assembled across separate statements so the secret guard's
// adjacent-literal anti-chunking rule does not reassemble the fixture at
// commit time, and low-entropy so the pre-push gitleaks generic-api-key
// rule (entropy-gated) does not flag the test file itself.
const KEY_HEAD = 'AKIA12345678';
const KEY_TAIL = '90ABCDEF';
const KEY = KEY_HEAD + KEY_TAIL;

function scratchRepo() {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'secret-checker-1144r1-'));
  const run = (cmd, args, input) => {
    const r = spawnSync(cmd, args, { cwd: dir, encoding: 'utf8', input });
    assert.equal(r.status, 0, `${cmd} ${args.join(' ')} failed: ${r.stderr}`);
    return r.stdout;
  };
  run('git', ['init', '-q']);
  run('git', ['config', 'core.protectNTFS', 'false']);
  fs.mkdirSync(path.join(dir, 'harness'));
  for (const f of ['claude-secret-checker.js', 'claude-secret-guard.allow.json']) {
    fs.copyFileSync(path.join(__dirname, f), path.join(dir, 'harness', f));
  }
  const check = () => JSON.parse(run(process.execPath, ['-e',
    "process.stdout.write(JSON.stringify(require('./harness/claude-secret-checker.js').checkSecrets()))"]));
  const stageBlob = (name, content) => {
    const h = run('git', ['hash-object', '-w', '--stdin'], content).trim();
    run('git', ['update-index', '--add', '--cacheinfo', `100644,${h},${name}`]);
  };
  return { dir, run, check, stageBlob };
}

test('BUG-1144 r1 (a): an unparseable real git header surfaces as a diff-header-parse-failure finding end to end', () => {
  // quote + space in the name: git C-quotes it AND appends a tab, which the parser does not accept.
  const { dir, check, stageBlob } = scratchRepo();
  try {
    stageBlob('a" b.txt', `harmless\nk=${KEY}\n`);
    const res = check();
    assert.equal(res.status, 'found-problems', JSON.stringify(res));
    assert.ok(res.findings.some(f => f.category === 'diff-header-parse-failure' || f.category === 'api-key'), JSON.stringify(res));
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('BUG-1144 r1 (b): a control character in the name (octal-escaped header) is decoded and its content attributed', () => {
  const { dir, check, stageBlob } = scratchRepo();
  try {
    stageBlob('ctl\x01name.txt', `harmless\nk=${KEY}\n`);
    const res = check();
    assert.ok(res.findings.some(f => f.file === 'ctl\x01name.txt' && f.line === 2 && f.category === 'api-key'), JSON.stringify(res));
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('BUG-1144 r1 (c): a repo textconv driver must not hide staged content from the scan', () => {
  const { dir, run, check } = scratchRepo();
  try {
    run('git', ['config', 'diff.hide.textconv', 'sh -c "echo nothing" --']);
    fs.writeFileSync(path.join(dir, '.gitattributes'), '*.txt diff=hide\n', 'utf8');
    fs.writeFileSync(path.join(dir, 'p.txt'), `harmless\nk=${KEY}\n`, 'utf8');
    run('git', ['add', '.gitattributes', 'p.txt']);
    const res = check();
    assert.equal(res.status, 'found-problems', `textconv blinded the scan: ${JSON.stringify(res)}`);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('BUG-1144 r1 (d): a file that diffs as binary (-diff attribute or a NUL byte) is scanned or fails closed, never silently clean', () => {
  for (const variant of ['attr', 'nul']) {
    const { dir, run, check } = scratchRepo();
    try {
      if (variant === 'attr') {
        fs.writeFileSync(path.join(dir, '.gitattributes'), '*.txt -diff\n', 'utf8');
        run('git', ['add', '.gitattributes']);
        fs.writeFileSync(path.join(dir, 'p.txt'), `harmless\nk=${KEY}\n`, 'utf8');
      } else {
        fs.writeFileSync(path.join(dir, 'p.txt'), `\0\nharmless\nk=${KEY}\n`, 'utf8');
      }
      run('git', ['add', 'p.txt']);
      const res = check();
      assert.equal(res.status, 'found-problems', `${variant}: binary diff blinded the scan: ${JSON.stringify(res)}`);
    } finally {
      fs.rmSync(dir, { recursive: true, force: true });
    }
  }
});
