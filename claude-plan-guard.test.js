/**
 * claude-plan-guard.test.js — regression tests for claude-plan-guard.js's
 * commit-intercept trigger (SEC-008's boundary-anchored regex; BUG-088 P0
 * correction, 2026-08-11).
 *
 * BUG-088 P0 CORRECTION: a prior pass of this guard's refactor (extracting
 * the drift-check payload into claude-plan-checker.js) silently ported
 * claude-author-guard.js's buildQuoteMask()/isRealGitCommit() quote-tracking
 * machinery into THIS guard's trigger — machinery that never shipped here
 * (`git show HEAD:claude-plan-guard.js` has only ever had a bare
 * `GIT_COMMIT_RE.test(command)`) and that AC-C1 explicitly requires to stay
 * unchanged ("unchanged, behaviourally identical... except for whatever the
 * refactor into a shared checker module requires mechanically"). That quote
 * mask introduced a NEW false-negative: an unbalanced/odd-count quote
 * character earlier in the command string flips the mask's parity and hides
 * a real, later `git commit` from the trigger entirely — a live
 * Destructive-agent finding, reproduced below. Reverted to the bare regex.
 *
 * This is the first test file for this hook (no prior test infra existed).
 * Unit-level only: GIT_COMMIT_RE is a pure regex exported for testing, so
 * these tests exercise it directly via require('./claude-plan-guard.js')
 * rather than spawning the process — the module only touches stdin/exits
 * (and runs tools/plan/generate.js) when require.main === module (see the
 * guard at the bottom of the source file), so requiring it here is side-
 * effect free.
 *
 * Run: node --test claude-plan-guard.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');

const { GIT_COMMIT_RE, looksLikeCommitFallback } = require('./claude-plan-guard.js');

test('BUG-088 P0: the Destructive\'s exact fixture (unbalanced quote in a shell comment before a real commit) now correctly triggers', () => {
  // Exact fixture from the Destructive-agent finding: a single unescaped
  // apostrophe ("don't") ahead of a REAL, later `git commit` invocation.
  const command = '"# don\'t forget to review; git commit -m x"';

  assert.equal(
    GIT_COMMIT_RE.test(command),
    true,
    'a real, later git commit must still trigger the guard even after an unbalanced quote earlier in the command'
  );

  // Load-bearing proof: the buggy quote-masked implementation this
  // correction removed (reconstructed here verbatim from this guard's own
  // working tree immediately prior to the BUG-088 fix) gets this fixture
  // WRONG — the stray apostrophe flips buildQuoteMask's parity and hides the
  // real "git commit" that follows, so isRealGitCommit returns false where
  // it must return true.
  function preFixBuggyIsRealGitCommit(cmd) {
    const buggyRe = /(?:^|[;&|(\n])\s*git\s+(?:-C\s+\S+\s+)?commit\b/g;
    function buildQuoteMask(text) {
      const mask = new Array(text.length).fill(false);
      let quote = null;
      let i = 0;
      while (i < text.length) {
        const c = text[i];
        if (quote) {
          mask[i] = true;
          if (quote === '"' && c === '\\' && i + 1 < text.length) {
            mask[i + 1] = true;
            i += 2;
            continue;
          }
          if (c === quote) quote = null;
          i++;
          continue;
        }
        if (c === '\\' && i + 1 < text.length) {
          i += 2;
          continue;
        }
        if (c === '"' || c === "'") {
          quote = c;
          mask[i] = true;
          i++;
          continue;
        }
        i++;
      }
      return mask;
    }
    const mask = buildQuoteMask(cmd);
    buggyRe.lastIndex = 0;
    let m;
    while ((m = buggyRe.exec(cmd)) !== null) {
      const gitPos = m.index + m[0].toLowerCase().lastIndexOf('git');
      if (!mask[gitPos]) return true;
    }
    return false;
  }
  assert.equal(
    preFixBuggyIsRealGitCommit(command),
    false,
    'sanity: the reconstructed pre-fix (quote-masked) trigger must reproduce the bug (false negative) on this exact fixture — proving the regression test above is load-bearing'
  );
});

test('a REAL git commit immediately after an unquoted "(" still fires', () => {
  assert.equal(GIT_COMMIT_RE.test('(git commit --author="Fake <fake@evil.com>" -m x)'), true);
});

test('a REAL git commit after unquoted ";", "&&", "|", and newline still fires', () => {
  assert.equal(GIT_COMMIT_RE.test('true; git commit -m x'), true);
  assert.equal(GIT_COMMIT_RE.test('true && git commit -m x'), true);
  assert.equal(GIT_COMMIT_RE.test('true | git commit -m x'), true);
  assert.equal(GIT_COMMIT_RE.test('true\ngit commit -m x'), true);
});

test('a bare "git commit" at the very start of the command still fires', () => {
  assert.equal(GIT_COMMIT_RE.test('git commit -m x'), true);
});

test('a command with no "git commit" phrase at all does not fire', () => {
  assert.equal(GIT_COMMIT_RE.test('npm install'), false);
  assert.equal(GIT_COMMIT_RE.test('git status'), false);
});

// Accepted, HEAD-matching limitation (NOT a regression): the bare regex has
// no notion of "inside a string literal", so a BOW comment merely quoting
// "(git commit ...)" as prose still triggers this fail-closed guard (an
// over-trigger — costs a full generate.js regeneration + hash-compare, never
// a bypass). This is the pre-BUG-043 behaviour this guard has always had at
// HEAD and is exactly what AC-C1 requires be kept unchanged; asserted here
// so a future attempt to "fix" it by silently reintroducing quote-masking is
// caught by this test failing, not by it staying green.
test('accepted limitation (matches HEAD, not a regression): a quoted mention of "(git commit ...)" as prose still over-triggers', () => {
  const command =
    'node claude-bow.js comment FEAT-040 ' +
    '"... (git commit --author=... is the exact bypass ...)"';
  assert.equal(
    GIT_COMMIT_RE.test(command),
    true,
    'the bare trigger over-triggers on this quoted mention, exactly as it did at HEAD — an accepted false positive, not a bypass'
  );
});

// End-to-end sanity: a genuinely unrelated command (no "git commit" phrase
// at all, quoted or not) allows silently without touching generate.js.
test('end-to-end: guard ALLOWS (silent) a command with no "git commit" phrase at all', () => {
  const { spawnSync } = require('child_process');
  const path = require('path');
  const command = 'npm install';
  const result = spawnSync(process.execPath, [path.join(__dirname, 'claude-plan-guard.js')], {
    input: JSON.stringify({ tool: 'Bash', tool_input: { command } }),
    encoding: 'utf8',
  });
  assert.equal(result.status, 0);
  assert.equal(result.stdout.trim(), '', 'ALLOW is silent (empty stdout) per this guard\'s hook contract');
});

// ---------------------------------------------------------------------------
// BUG-123 regression: `git -c key=value commit` and other global-option forms
// ---------------------------------------------------------------------------

test('BUG-123: "git -c key=value commit" and stacked/-C-combined forms now fire', () => {
  assert.equal(GIT_COMMIT_RE.test('git -c foo=bar commit -m test'), true);
  assert.equal(GIT_COMMIT_RE.test('git -c user.email=x commit'), true);
  assert.equal(GIT_COMMIT_RE.test('git -c commit.gpgsign=false commit'), true);
  assert.equal(GIT_COMMIT_RE.test('git -c a=b -c c=d commit -m x'), true);
  assert.equal(GIT_COMMIT_RE.test('git -c a=b -C /some/dir commit -m x'), true);
  assert.equal(GIT_COMMIT_RE.test('git -C /some/dir -c a=b commit -m x'), true);
});

test('BUG-123: sanity — the pre-fix regex genuinely misses "git -c foo=bar commit"', () => {
  const preFixRe = /(?:^|[;&|(\n])\s*git\s+(?:-C\s+\S+\s+)?commit\b/;
  assert.equal(preFixRe.test('git -c foo=bar commit -m test'), false);
});

// ---------------------------------------------------------------------------
// BUG-123 ROUND 2 regression: Tester B's backtracking false-positive finding.
// Round 1's single-alternation regex backtracked its argument-less catch-all
// into an already-claimed -c/-C token when the trailing verb check failed,
// leaving the option's OWN VALUE unconsumed right where the verb check ran
// next — so a -c/-C value that happened to start with "commit" was misread
// as the verb. Fixed by switching claude-git-commit-trigger.js to a
// tokenizer (option-run parsed one option at a time, verb compared by exact
// Set equality, never substring/alternation match). See that module's header.
// ---------------------------------------------------------------------------

test('BUG-123 round 2: "git -c commit.gpgsign=false status" / "-c commit.template=... diff" / "-C commit-repo status" are NOT commit triggers', () => {
  assert.equal(GIT_COMMIT_RE.test('git -c commit.gpgsign=false status'), false);
  assert.equal(GIT_COMMIT_RE.test('git -c commit.template=/dev/null diff'), false);
  assert.equal(GIT_COMMIT_RE.test('git -C commit-repo status'), false);
});

test('BUG-123 round 2: sanity — the round-1 regex genuinely mis-fires on these fixtures', () => {
  const round1OptSrc =
    '(?:' +
      '-c\\s+(?:"[^"]*"|\'[^\']*\'|\\S+)' +
      '|-C\\s+(?:"[^"]*"|\'[^\']*\'|\\S+)' +
      '|--git-dir(?:=\\S+|\\s+\\S+)' +
      '|--work-tree(?:=\\S+|\\s+\\S+)' +
      '|--namespace(?:=\\S+|\\s+\\S+)' +
      '|--[A-Za-z][A-Za-z-]*(?:=\\S+)?' +
      '|-[A-Za-z]' +
    ')';
  const round1OptsRunSrc = `(?:${round1OptSrc}\\s+)*`;
  const round1Re = new RegExp(`(?:^|[;&|(\\n])\\s*git\\s+${round1OptsRunSrc}(?:commit)\\b`);
  assert.equal(round1Re.test('git -c commit.gpgsign=false status'), true);
  assert.equal(round1Re.test('git -C commit-repo status'), true);
});

// ---------------------------------------------------------------------------
// BUG-123 ROUND 4 regression: attacker Vex's quote-open-mid-token finding.
// Round 3's value grammar only recognised a quote as the option's value when
// it was the first character right after the required whitespace; the
// equally common `-c key="value with space"` shape (quote opening mid-token,
// after `=`) fell through to the bare `\S+` catch-all and leaked the value's
// tail into the verb-word position. Fixed by consumeShellToken() in
// claude-git-commit-trigger.js — see that module's header.
// ---------------------------------------------------------------------------

test('BUG-123 round 4 (Vex): quoted values with the quote opening mid-token (after "=") are handled correctly', () => {
  assert.equal(GIT_COMMIT_RE.test('git -c user.name="John Q Commit" commit -m x'), true);
  assert.equal(GIT_COMMIT_RE.test("git -c user.name='John Q Commit' commit -m x"), true);
  assert.equal(GIT_COMMIT_RE.test('git -c msg="please commit later" status'), false);
});

test('BUG-123 round 4: sanity — the round-3 value grammar genuinely mis-handles these fixtures', () => {
  const round3OptSrc =
    '(?:' +
      '-c\\s+(?:"[^"]*"|\'[^\']*\'|\\S+)' +
      '|-C\\s+(?:"[^"]*"|\'[^\']*\'|\\S+)' +
      '|--git-dir(?:=\\S+|\\s+\\S+)' +
      '|--work-tree(?:=\\S+|\\s+\\S+)' +
      '|--namespace(?:=\\S+|\\s+\\S+)' +
      '|--[A-Za-z][A-Za-z-]*(?:=\\S+)?' +
      '|-[A-Za-z]' +
    ')';
  const round3OptsRunSrc = `(?:${round3OptSrc}\\s+)*`;
  const round3Re = new RegExp(`(?:^|[;&|(\\n])\\s*git\\s+${round3OptsRunSrc}(?:commit)\\b`);
  assert.equal(round3Re.test('git -c user.name="John Q Commit" commit -m x'), false);
  assert.equal(round3Re.test('git -c msg="please commit later" status'), true);
});

// End-to-end: proves the real checker.checkPlan() payload (which spawns
// tools/plan/generate.js --check, then a full regen + hash-compare — a
// multi-hundred-millisecond operation) actually RUNS when the command is a
// `git -c ... commit` invocation, and that its result matches calling
// checker.checkPlan() directly. A command with the same `-c` prefix but no
// "commit" verb must NOT run it at all (fast, silent allow) — the contrast
// is what proves the trigger, not just the checker's payload, is what
// changed.
test('BUG-123 end-to-end: "git -c ... commit" actually invokes checker.checkPlan() (not just matches the trigger regex)', { concurrency: false }, () => {
  const { spawnSync } = require('child_process');
  const path = require('path');
  const os = require('os');
  const fs = require('fs');
  const crypto = require('crypto');
  const checker = require('./claude-plan-checker.js');

  const expected = checker.checkPlan();

  // BUG-291: this used to assert wall-clock elapsed time (>100ms) as a proxy
  // for "the real checkPlan() payload ran" — banned here per BUG-031 (count
  // work, not time); a fast CI runner completed the real work in 82ms and
  // false-failed an innocent PR. Instead use the BUG-165-pattern test-only
  // marker hatch in claude-plan-guard.js: it touches
  // CLAUDE_PLAN_GUARD_TEST_MARKER right before calling checker.checkPlan(),
  // so the marker file's existence directly proves the trigger fired,
  // independent of how fast the machine happened to be.
  const triggeredMarkerPath = path.join(os.tmpdir(), `claude-plan-guard-test-marker-${crypto.randomBytes(8).toString('hex')}.txt`);
  const untriggeredMarkerPath = path.join(os.tmpdir(), `claude-plan-guard-test-marker-${crypto.randomBytes(8).toString('hex')}.txt`);

  try {
    const triggered = spawnSync(process.execPath, [path.join(__dirname, 'claude-plan-guard.js')], {
      input: JSON.stringify({
        tool: 'Bash',
        tool_input: { command: 'git -c user.email=x@example.com -c commit.gpgsign=false commit -m "test: bug-123 e2e"' },
      }),
      encoding: 'utf8',
      env: { ...process.env, CLAUDE_PLAN_GUARD_TEST_MARKER: triggeredMarkerPath },
    });
    assert.equal(triggered.status, 0);
    assert.ok(
      fs.existsSync(triggeredMarkerPath),
      'expected the real checkPlan() payload to run (test-only marker file was not written) — looks like the trigger never fired'
    );
    if (expected.status === 'clean') {
      assert.equal(triggered.stdout.trim(), '', 'checkPlan() reported clean, so the guard must allow silently');
    } else {
      const parsed = JSON.parse(triggered.stdout);
      assert.equal(parsed?.hookSpecificOutput?.permissionDecision, 'deny');
    }

    const untriggered = spawnSync(process.execPath, [path.join(__dirname, 'claude-plan-guard.js')], {
      input: JSON.stringify({
        tool: 'Bash',
        tool_input: { command: 'git -c user.email=x@example.com status' },
      }),
      encoding: 'utf8',
      env: { ...process.env, CLAUDE_PLAN_GUARD_TEST_MARKER: untriggeredMarkerPath },
    });
    assert.equal(untriggered.status, 0);
    assert.equal(untriggered.stdout.trim(), '', 'a non-commit "git -c ..." command must allow silently');
    assert.ok(
      !fs.existsSync(untriggeredMarkerPath),
      'a non-commit command must never reach checkPlan() (test-only marker file was written, but the trigger should have stayed silent)'
    );
  } finally {
    try { fs.unlinkSync(triggeredMarkerPath); } catch { /* may not exist */ }
    try { fs.unlinkSync(untriggeredMarkerPath); } catch { /* may not exist */ }
  }
});

// ---------------------------------------------------------------------------
// BUG-123 ROUND 9 regression (attacker "Cinder" REJECT): claude-git-commit-
// trigger.js used to be require()'d at TRUE module top level in
// claude-plan-guard.js, before main()'s try/catch existed. A synchronous
// throw during that require (e.g. a broken claude-quote-mask.js, its own
// transitive dependency) crashed the whole process at MODULE LOAD TIME --
// uncaught exception, exit 1, ZERO stdout, no hookSpecificOutput JSON --
// which the PreToolUse contract treats as a non-blocking failure: the
// proposed `git commit` PROCEEDS completely unscanned, contradicting this
// guard's own documented fail-closed posture. Fixed by loading the trigger
// module lazily, inside main()'s try block (loadGitCommitTrigger(), via the
// CLAUDE_PLAN_GUARD_TRIGGER_PATH env override so these tests never touch the
// real, live-edited claude-git-commit-trigger.js / claude-quote-mask.js), so
// a load-time throw is caught by the SAME catch() that already denies on any
// other internal error, mirroring claude-destructive-guard.js's own proven
// round-3 fix.
// ---------------------------------------------------------------------------

function withBrokenTriggerFixturePlan(content, fn) {
  const fs = require('fs');
  const os = require('os');
  const path = require('path');
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'planguard-triggerfixture-'));
  const fixturePath = path.join(dir, 'fixture-trigger.js');
  try {
    if (content !== null) fs.writeFileSync(fixturePath, content, 'utf8');
    return fn(fixturePath);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

test('BUG-123 round 9 (Cinder) sanity: reverting to a top-level require() genuinely reproduces the uncaught module-load crash', () => {
  const fs = require('fs');
  const os = require('os');
  const path = require('path');
  const { spawnSync } = require('child_process');
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'planguard-round9-sandbox-'));
  try {
    fs.writeFileSync(
      path.join(dir, 'broken-toplevel-guard.js'),
      "'use strict';\nconst {buildAnchoredGitVerbTriggerRegex} = require('./broken-trigger.js');\nconst RE = buildAnchoredGitVerbTriggerRegex('commit');\nprocess.stdin.on('data', () => {});\nprocess.stdin.on('end', () => { process.stdout.write(RE.test('git commit') ? 'yes' : 'no'); });\n",
      'utf8'
    );
    fs.writeFileSync(path.join(dir, 'broken-trigger.js'), 'this is ( not valid javascript {{{\n', 'utf8');
    const r = spawnSync(process.execPath, [path.join(dir, 'broken-toplevel-guard.js')], {
      input: JSON.stringify({ tool: 'Bash', tool_input: { command: 'git commit -m test' } }),
      encoding: 'utf8',
    });
    assert.equal(r.status, 1, 'sanity: the OLD top-level-require shape genuinely crashes with exit 1');
    assert.equal((r.stdout || '').trim(), '', 'sanity: the OLD shape emits ZERO stdout -- no decision, the commit would proceed unscanned');
    assert.match(r.stderr, /SyntaxError/);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('BUG-123 round 9: a broken claude-git-commit-trigger.js dependency on a real "git commit" invocation now DENIES cleanly with JSON, not a bare crash', () => {
  const path = require('path');
  const { spawnSync } = require('child_process');
  withBrokenTriggerFixturePlan('this is ( not valid javascript {{{\n', (brokenTriggerPath) => {
    const r = spawnSync(process.execPath, [path.join(__dirname, 'claude-plan-guard.js')], {
      cwd: __dirname,
      encoding: 'utf8',
      env: { ...process.env, CLAUDE_PLAN_GUARD_TRIGGER_PATH: brokenTriggerPath },
      input: JSON.stringify({ tool: 'Bash', tool_input: { command: 'git commit -m test' } }),
    });
    assert.notEqual(r.status, 1, 'must not crash with a bare uncaught-exception exit(1) -- that is the exact bug this round fixes');
    assert.equal(r.status, 0, 'the guard process must exit 0 (it makes the decision itself)');
    assert.notEqual((r.stdout || '').trim(), '', 'a decision payload MUST be emitted -- the pre-fix bug emitted nothing at all here');
    const parsed = JSON.parse(r.stdout);
    assert.equal(parsed?.hookSpecificOutput?.permissionDecision, 'deny', 'a broken dependency on a real git-commit-shaped command must DENY, not silently allow');
    assert.match(parsed.hookSpecificOutput.permissionDecisionReason, /depend|load|trigger/i);
  });
});

test('BUG-123 round 9: a MISSING claude-git-commit-trigger.js on a real "git commit" invocation also DENIES cleanly, not crashes', () => {
  const path = require('path');
  const os = require('os');
  const crypto = require('crypto');
  const { spawnSync } = require('child_process');
  const r = spawnSync(process.execPath, [path.join(__dirname, 'claude-plan-guard.js')], {
    cwd: __dirname,
    encoding: 'utf8',
    env: {
      ...process.env,
      CLAUDE_PLAN_GUARD_TRIGGER_PATH: path.join(os.tmpdir(), 'this-file-does-not-exist-' + crypto.randomBytes(4).toString('hex') + '.js'),
    },
    input: JSON.stringify({ tool: 'Bash', tool_input: { command: 'git commit -m test' } }),
  });
  assert.equal(r.status, 0);
  const parsed = JSON.parse(r.stdout);
  assert.equal(parsed?.hookSpecificOutput?.permissionDecision, 'deny');
});

test('BUG-123 round 9: a broken claude-git-commit-trigger.js on a NON-commit command still allows silently (dependency-load fail-closed is scoped to commit-shaped input only)', () => {
  const path = require('path');
  const { spawnSync } = require('child_process');
  withBrokenTriggerFixturePlan('this is ( not valid javascript {{{\n', (brokenTriggerPath) => {
    const r = spawnSync(process.execPath, [path.join(__dirname, 'claude-plan-guard.js')], {
      cwd: __dirname,
      encoding: 'utf8',
      env: { ...process.env, CLAUDE_PLAN_GUARD_TRIGGER_PATH: brokenTriggerPath },
      input: JSON.stringify({ tool: 'Bash', tool_input: { command: 'npm install' } }),
    });
    assert.equal(r.status, 0, 'must not crash');
    assert.equal((r.stdout || '').trim(), '', 'an unrelated command must allow silently even with a broken trigger dependency -- never brick the whole session over a bug this command does not need');
  });
});

test('BUG-123 round 9: normal operation is completely unaffected -- healthy dependency, real trigger still allows a non-commit command silently', () => {
  const path = require('path');
  const { spawnSync } = require('child_process');
  const r = spawnSync(process.execPath, [path.join(__dirname, 'claude-plan-guard.js')], {
    cwd: __dirname,
    encoding: 'utf8',
    input: JSON.stringify({ tool: 'Bash', tool_input: { command: 'npm install' } }),
  });
  assert.equal(r.status, 0);
  assert.equal((r.stdout || '').trim(), '', 'ALLOW is silent for a non-commit command, unchanged posture');
});

// ---------------------------------------------------------------------------
// BUG-123 ROUND 10 regression (attacker "Thresher" REJECT): the round-9
// dependency-free fallback, FALLBACK_LOOKS_LIKE_COMMIT_RE, capped the gap
// between "git" and "commit" at ~200 chars. A single legitimately long `-c`
// option value pushes a real commit's git-to-commit distance past that
// window, so with the trigger dependency broken NOTHING catches it -- a
// silent, unscanned allow. Fixed by rebuilding looksLikeCommitFallback() as
// unbounded indexOf/startsWith scanning (no regex, no distance cap, strictly
// O(n), zero backtracking).
// ---------------------------------------------------------------------------

test('BUG-123 round 10 (Thresher) exact repro, unit level: looksLikeCommitFallback() now fires on a 250-char -c value', () => {
  const longVal = 'A'.repeat(250);
  const cmd = `git -c user.name="${longVal}" commit -m test`;
  assert.equal(looksLikeCommitFallback(cmd), true, 'a real commit with a long -c value must still be recognised by the dependency-free fallback');
});

test('BUG-123 round 10 (Thresher) sanity: the pre-fix 200-char-capped regex genuinely misses this fixture (proves the regression test is load-bearing)', () => {
  const PRE_FIX_RE = /\bgit(?:\.(?:exe|cmd))?\b[\s\S]{0,200}?\bcommit\b/i;
  const longVal = 'A'.repeat(250);
  const cmd = `git -c user.name="${longVal}" commit -m test`;
  assert.equal(PRE_FIX_RE.test(cmd), false, 'sanity: the OLD bounded regex genuinely fails to match Thresher\'s exact repro');
});

test('BUG-123 round 10 (Thresher) end-to-end: a broken trigger dependency + a real commit with a 250-char -c value now DENIES, not silently allows', () => {
  const path = require('path');
  const { spawnSync } = require('child_process');
  withBrokenTriggerFixturePlan('this is ( not valid javascript {{{\n', (brokenTriggerPath) => {
    const longVal = 'A'.repeat(250);
    const cmd = `git -c user.name="${longVal}" commit -m test`;
    const r = spawnSync(process.execPath, [path.join(__dirname, 'claude-plan-guard.js')], {
      cwd: __dirname,
      encoding: 'utf8',
      env: { ...process.env, CLAUDE_PLAN_GUARD_TRIGGER_PATH: brokenTriggerPath },
      input: JSON.stringify({ tool: 'Bash', tool_input: { command: cmd } }),
    });
    assert.equal(r.status, 0, 'the guard process must exit 0 (it makes the decision itself)');
    assert.notEqual((r.stdout || '').trim(), '', 'a decision payload MUST be emitted -- the round-10 bug emitted nothing at all here');
    const parsed = JSON.parse(r.stdout);
    assert.equal(parsed?.hookSpecificOutput?.permissionDecision, 'deny', 'a broken dependency on a real, long-option git-commit-shaped command must DENY, not silently allow');
  });
});

test('BUG-123 round 10: even more extreme distances (2KB, 20KB, 200KB gap between "git" and "commit") still fire -- no residual bound anywhere', () => {
  for (const size of [2000, 20000, 200000]) {
    const longVal = 'B'.repeat(size);
    const cmd = `git -c user.name="${longVal}" commit -m test`;
    assert.equal(looksLikeCommitFallback(cmd), true, `must fire at gap size ${size}`);
  }
});

test('BUG-123 round 10: prior fallback behaviour is preserved -- a quoted-bare "git" commit and a non-commit command still behave as before', () => {
  assert.equal(looksLikeCommitFallback('"git" commit -m "x"'), true);
  assert.equal(looksLikeCommitFallback('npm install'), false);
  assert.equal(looksLikeCommitFallback(''), false);
  assert.equal(looksLikeCommitFallback(null), false);
});

test('BUG-123 round 10: ReDoS safety -- a 1MB adversarial non-matching string (many "git" tokens, no "commit" anywhere) resolves quickly with no catastrophic backtracking', () => {
  const adversarial = 'git '.repeat(300000) + 'x'.repeat(100000);
  assert.equal(adversarial.length > 1_000_000, true, 'sanity: the adversarial fixture is genuinely over 1MB');
  const t0 = process.hrtime.bigint();
  const result = looksLikeCommitFallback(adversarial);
  const elapsedMs = Number(process.hrtime.bigint() - t0) / 1e6;
  assert.equal(result, false, 'no "commit" anywhere in the fixture -- must resolve to false, not hang');
  assert.equal(elapsedMs < 100, true, `must resolve well under 100ms on a 1MB adversarial input (took ${elapsedMs.toFixed(2)}ms) -- a slow fallback would itself be a DoS vector`);
});
