/**
 * claude-push-verify.test.js — unit tests for claude-push-verify.js.
 *
 * FEAT-059's own brief says this tool cannot be tested against real GitHub
 * Actions runs in a throwaway way (no throwaway repo has real CI), so the
 * GitHub-facing decision logic is factored into pure functions
 * (classifyConclusion, summarizeRuns) that take plain JSON-shaped fixtures
 * and are tested exhaustively here. fetchRunsForSha/watchUntilComplete (the
 * actual `gh` subprocess/polling glue) are exercised only via manual live
 * use per the brief, plus a couple of fast async-flow tests below that
 * inject fake fetch/sleep/now functions so no real subprocess or real timer
 * is involved.
 *
 * Every "exits nonzero when X" test below is paired with an explicit mutant
 * check proving the fixture actually distinguishes correct behaviour from a
 * plausible wrong implementation (first-run-only checking, OR'd the wrong
 * way, etc.) — see the "mutant" tests, per FEAT-059's quality bar: "every
 * test must be proven able to fail".
 *
 * Run: node --test claude-push-verify.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');

const {
  classifyConclusion,
  formatRunLine,
  summarizeRuns,
  parseArgs,
  watchUntilComplete,
  PushVerifyError,
} = require('./claude-push-verify.js');

// ---------------------------------------------------------------------------
// Fixture builders
// ---------------------------------------------------------------------------

function run(overrides) {
  return {
    name: 'CI',
    workflowName: 'CI',
    status: 'completed',
    conclusion: 'success',
    url: 'https://github.com/example/example/actions/runs/1',
    headSha: 'deadbeef',
    ...overrides,
  };
}

const SUCCESS_RUN = run({ name: 'build', conclusion: 'success' });
const FAILURE_RUN = run({ name: 'test', conclusion: 'failure' });
const CANCELLED_RUN = run({ name: 'lint', conclusion: 'cancelled' });
const SKIPPED_RUN = run({ name: 'deploy', conclusion: 'skipped' });
const IN_PROGRESS_RUN = run({ name: 'build', status: 'in_progress', conclusion: null });

// ---------------------------------------------------------------------------
// classifyConclusion
// ---------------------------------------------------------------------------

test('classifyConclusion: success is pass', () => {
  assert.equal(classifyConclusion('success'), 'pass');
});

test('classifyConclusion: skipped is skip, NOT pass', () => {
  assert.equal(classifyConclusion('skipped'), 'skip');
  assert.notEqual(classifyConclusion('skipped'), 'pass');
});

test('classifyConclusion: failure, cancelled, timed_out, neutral, action_required, stale, startup_failure are all fail', () => {
  for (const c of [
    'failure',
    'cancelled',
    'timed_out',
    'neutral',
    'action_required',
    'stale',
    'startup_failure',
  ]) {
    assert.equal(classifyConclusion(c), 'fail', `expected ${c} to classify as fail`);
  }
});

test('classifyConclusion: unrecognised/null/undefined conclusions fail closed (never pass)', () => {
  assert.equal(classifyConclusion(null), 'fail');
  assert.equal(classifyConclusion(undefined), 'fail');
  assert.equal(classifyConclusion('some_future_github_status_nobody_has_seen_yet'), 'fail');
});

// ---------------------------------------------------------------------------
// formatRunLine — skip vs pass must be visually distinct
// ---------------------------------------------------------------------------

test('formatRunLine: pass and skip lines use different tags and skip carries an explicit "not a pass" note', () => {
  const passLine = formatRunLine(SUCCESS_RUN, 'pass');
  const skipLine = formatRunLine(SKIPPED_RUN, 'skip');
  assert.match(passLine, /^\[PASS\]/);
  assert.match(skipLine, /^\[SKIP\]/);
  assert.notEqual(passLine.slice(0, 6), skipLine.slice(0, 6));
  assert.match(skipLine, /not a pass/i);
  assert.doesNotMatch(passLine, /not a pass/i);
});

test('formatRunLine: fail line is tagged FAIL and distinct from both pass and skip', () => {
  const failLine = formatRunLine(FAILURE_RUN, 'fail');
  assert.match(failLine, /^\[FAIL\]/);
});

// MUTANT CHECK: a plausible-but-wrong formatter might reuse the same prefix
// for pass and skip (e.g. tagging both "[OK]") since both are "not a
// failure". Prove the real assertion above would catch that.
test('mutant check: a formatter collapsing pass/skip into one tag would fail the distinctness assertion', () => {
  function wrongFormatRunLine(r, cls) {
    // Treats anything that isn't 'fail' as "[OK]" — the exact bug this
    // tool must never ship (a skip wearing a pass's colours).
    const tag = cls === 'fail' ? 'FAIL' : 'OK';
    return `[${tag}] ${r.name}`;
  }
  const passLine = wrongFormatRunLine(SUCCESS_RUN, 'pass');
  const skipLine = wrongFormatRunLine(SKIPPED_RUN, 'skip');
  assert.throws(() => {
    assert.notEqual(passLine.slice(0, 4), skipLine.slice(0, 4));
  }, assert.AssertionError, 'the mutant formatter should make the real assertion fail, proving the test is not vacuous');
});

// ---------------------------------------------------------------------------
// summarizeRuns — the core pass/fail/exit-code decision
// ---------------------------------------------------------------------------

test('summarizeRuns: single successful run -> allCompleted, no fail, exitCode 0', () => {
  const s = summarizeRuns([SUCCESS_RUN]);
  assert.equal(s.allCompleted, true);
  assert.equal(s.anyFail, false);
  assert.equal(s.exitCode, 0);
});

test('summarizeRuns: single failed run -> exitCode 1', () => {
  const s = summarizeRuns([FAILURE_RUN]);
  assert.equal(s.allCompleted, true);
  assert.equal(s.anyFail, true);
  assert.equal(s.exitCode, 1);
});

test('summarizeRuns: single cancelled run -> exitCode 1', () => {
  const s = summarizeRuns([CANCELLED_RUN]);
  assert.equal(s.exitCode, 1);
});

test('summarizeRuns: single skipped run -> exitCode 0 (skip counts as legitimately-not-a-failure), and is reported distinctly from pass', () => {
  const s = summarizeRuns([SKIPPED_RUN]);
  assert.equal(s.allCompleted, true);
  assert.equal(s.anyFail, false);
  assert.equal(s.exitCode, 0);
  assert.equal(s.classified[0].cls, 'skip');
  assert.notEqual(s.classified[0].cls, 'pass');
  assert.match(s.lines[0], /^\[SKIP\]/);
});

test('summarizeRuns: a run still in_progress -> allCompleted false, exitCode null (never guess)', () => {
  const s = summarizeRuns([IN_PROGRESS_RUN]);
  assert.equal(s.allCompleted, false);
  assert.equal(s.exitCode, null);
  assert.ok(s.pendingNames.length === 1);
});

test('summarizeRuns: empty run list -> allCompleted false (zero runs is never treated as a completed pass)', () => {
  const s = summarizeRuns([]);
  assert.equal(s.allCompleted, false);
  assert.equal(s.exitCode, null);
});

test('summarizeRuns: mix of several runs where ONE failed must still exit nonzero even though others passed', () => {
  const s = summarizeRuns([SUCCESS_RUN, FAILURE_RUN, run({ name: 'other', conclusion: 'success' })]);
  assert.equal(s.allCompleted, true);
  assert.equal(s.anyFail, true);
  assert.equal(s.exitCode, 1);
});

test('summarizeRuns: mix of success + skip (no failures) -> exitCode 0, and the skip line is still marked distinctly', () => {
  const s = summarizeRuns([SUCCESS_RUN, SKIPPED_RUN]);
  assert.equal(s.exitCode, 0);
  const skipEntry = s.classified.find((c) => c.run === SKIPPED_RUN);
  assert.equal(skipEntry.cls, 'skip');
  const skipLine = s.lines[s.classified.indexOf(skipEntry)];
  assert.match(skipLine, /^\[SKIP\]/);
});

test('summarizeRuns: mix of success + skip + one failure -> exitCode 1 (a passing/skipping majority never masks one real failure)', () => {
  const s = summarizeRuns([SUCCESS_RUN, SKIPPED_RUN, FAILURE_RUN]);
  assert.equal(s.exitCode, 1);
  assert.equal(s.anyFail, true);
});

// MUTANT CHECK #1 (first-run-only): prove a summarizer that only inspects
// runs[0] would WRONGLY pass the "one failure among several" fixture above.
test('mutant check: a summarizer that only checks the first run would wrongly report success on runs = [success, failure]', () => {
  function wrongSummarize(runs) {
    // Bug: only ever looks at the first run.
    const first = runs[0];
    return classifyConclusion(first.conclusion) === 'fail' ? 1 : 0;
  }
  const runsFixture = [SUCCESS_RUN, FAILURE_RUN];
  const correct = summarizeRuns(runsFixture).exitCode;
  const wrong = wrongSummarize(runsFixture);
  assert.equal(correct, 1, 'sanity: the real function must catch this');
  assert.equal(wrong, 0, 'the buggy first-run-only summarizer wrongly reports success — proves the fixture distinguishes correct from wrong');
  assert.notEqual(correct, wrong);
});

// MUTANT CHECK #2 (wrong boolean combination): prove a summarizer that ORs
// "all passed" across runs instead of checking "any failed" would also
// wrongly pass the same fixture (ANY-pass instead of ALL-pass / NONE-fail).
test('mutant check: a summarizer using ANY-passed instead of NONE-failed would wrongly report success on a mixed pass/fail set', () => {
  function wrongSummarizeAnyPassed(runs) {
    // Bug: "at least one run passed" instead of "no run failed".
    const anyPassed = runs.some((r) => classifyConclusion(r.conclusion) === 'pass');
    return anyPassed ? 0 : 1;
  }
  const runsFixture = [SUCCESS_RUN, FAILURE_RUN, run({ name: 'other', conclusion: 'success' })];
  const correct = summarizeRuns(runsFixture).exitCode;
  const wrong = wrongSummarizeAnyPassed(runsFixture);
  assert.equal(correct, 1);
  assert.equal(wrong, 0, 'the ANY-passed summarizer wrongly reports success when one run in the mix failed');
  assert.notEqual(correct, wrong);
});

// MUTANT CHECK #3 (skip/pass collapse at the decision layer, not just
// formatting): prove a summarizer treating skip as a distinct-but-still-
// "did not fail" case is fine (matches spec), but one that maps skip TO
// 'pass' internally would be indistinguishable in exit code yet WOULD be
// wrong for a stricter future policy — demonstrated by showing the classifier
// contract itself (skip !== pass) is load-bearing for formatRunLine's output
// even though today's exit-code policy treats them the same for exit 0.
test('mutant check: collapsing skip into pass at the classification layer would break the skip-vs-pass line-tagging contract', () => {
  function wrongClassify(conclusion) {
    if (conclusion === 'success' || conclusion === 'skipped') return 'pass'; // bug: no distinct 'skip'
    return 'fail';
  }
  const correctLine = formatRunLine(SKIPPED_RUN, classifyConclusion(SKIPPED_RUN.conclusion));
  const wrongLine = formatRunLine(SKIPPED_RUN, wrongClassify(SKIPPED_RUN.conclusion));
  assert.match(correctLine, /^\[SKIP\]/);
  assert.match(wrongLine, /^\[PASS\]/, 'sanity: the mutant really does mislabel a skip as a pass');
  assert.notEqual(correctLine.slice(0, 6), wrongLine.slice(0, 6));
});

// ---------------------------------------------------------------------------
// parseArgs
// ---------------------------------------------------------------------------

test('parseArgs: no args -> empty sha, empty opts', () => {
  const { sha, opts } = parseArgs(['node', 'claude-push-verify.js']);
  assert.equal(sha, '');
  assert.deepEqual(opts, {});
});

test('parseArgs: positional sha plus numeric flags', () => {
  const { sha, opts } = parseArgs([
    'node',
    'claude-push-verify.js',
    'abc123',
    '--interval-ms',
    '1000',
    '--grace-ms',
    '2000',
    '--timeout-ms',
    '3000',
    '--max-errors',
    '5',
  ]);
  assert.equal(sha, 'abc123');
  assert.deepEqual(opts, { intervalMs: 1000, graceMs: 2000, timeoutMs: 3000, maxErrors: 5 });
});

test('parseArgs: --help sets opts.help', () => {
  const { opts } = parseArgs(['node', 'claude-push-verify.js', '--help']);
  assert.equal(opts.help, true);
});

test('parseArgs: rejects an unrecognised flag rather than silently ignoring it', () => {
  assert.throws(() => parseArgs(['node', 'x.js', '--bogus-flag']), PushVerifyError);
});

test('parseArgs: rejects a non-numeric value for a numeric flag', () => {
  assert.throws(() => parseArgs(['node', 'x.js', '--interval-ms', 'notanumber']), PushVerifyError);
});

// ---------------------------------------------------------------------------
// watchUntilComplete — async control flow, driven with fake fetch/sleep/now
// (no real subprocess, no real timers, so this runs fast and deterministically)
// ---------------------------------------------------------------------------

test('watchUntilComplete: resolves once fetchFn reports allCompleted, without waiting for real time', async () => {
  let calls = 0;
  const fetchFn = () => {
    calls += 1;
    if (calls === 1) return [IN_PROGRESS_RUN];
    return [SUCCESS_RUN];
  };
  let sleeps = 0;
  const sleepFn = async () => {
    sleeps += 1;
  };
  const logs = [];
  // settleFloorMs: 0 disables the wall-clock settle floor so this test can
  // focus purely on "does it resolve once allCompleted, without needing real
  // timers" — the floor itself is exercised by its own dedicated tests below.
  // nowFn still needs to advance (a real clock always does) so the 2 *
  // intervalMs part of the settle window — which is NOT disabled by
  // settleFloorMs: 0 — can actually be satisfied; a frozen clock would make
  // this hang forever waiting for time that never passes.
  let clock = 0;
  const nowFn = () => (clock += 1000);
  const summary = await watchUntilComplete('deadbeef', '/tmp', {
    fetchFn,
    sleepFn,
    nowFn,
    intervalMs: 1,
    graceMs: 100,
    timeoutMs: 1000,
    settleFloorMs: 0,
  }, (m) => logs.push(m));

  assert.equal(summary.allCompleted, true);
  assert.equal(summary.exitCode, 0);
  assert.equal(calls, 2);
  assert.equal(sleeps, 1);
});

test('watchUntilComplete: throws PushVerifyError if zero runs ever appear before graceMs elapses (no silent hang, no false pass)', async () => {
  const fetchFn = () => [];
  // nowFn advances past graceMs immediately so the test does not need real time.
  let t = 0;
  const nowFn = () => {
    const v = t;
    t += 1000; // jumps 1000ms of simulated time per check
    return v;
  };
  await assert.rejects(
    watchUntilComplete('deadbeef', '/tmp', {
      fetchFn,
      sleepFn: async () => {},
      nowFn,
      intervalMs: 1,
      graceMs: 500,
      timeoutMs: 1000,
    }, () => {}),
    PushVerifyError
  );
});

test('watchUntilComplete: throws PushVerifyError after timeoutMs if runs never complete (never hangs silently forever)', async () => {
  const fetchFn = () => [IN_PROGRESS_RUN];
  let t = 0;
  const nowFn = () => {
    const v = t;
    t += 1000;
    return v;
  };
  await assert.rejects(
    watchUntilComplete('deadbeef', '/tmp', {
      fetchFn,
      sleepFn: async () => {},
      nowFn,
      intervalMs: 1,
      graceMs: 500,
      timeoutMs: 2000,
    }, () => {}),
    PushVerifyError
  );
});

test('watchUntilComplete: a single transient fetch failure is tolerated and retried, not fatal', async () => {
  let calls = 0;
  const fetchFn = () => {
    calls += 1;
    if (calls === 1) throw new Error('simulated transient network blip');
    return [SUCCESS_RUN];
  };
  let clock = 0;
  const nowFn = () => (clock += 1000);
  const summary = await watchUntilComplete('deadbeef', '/tmp', {
    fetchFn,
    sleepFn: async () => {},
    nowFn,
    intervalMs: 1,
    graceMs: 1000,
    timeoutMs: 1000,
    maxErrors: 3,
    // Settle floor disabled (see comment on the first watchUntilComplete
    // test above) — this test targets transient-failure tolerance, not the
    // floor, which has its own dedicated tests below.
    settleFloorMs: 0,
  }, () => {});
  assert.equal(summary.exitCode, 0);
  // Call 1: transient failure (retried, not fatal). Call 2: first sighting
  // of the run, already complete, but nothing yet to compare its count
  // against (run-set settling fix — see watchUntilComplete's doc comment),
  // so it is not trusted yet. Call 3: same run count as call 2 (1 == 1) AND
  // enough simulated wall-clock time has passed (settle floor disabled here,
  // so only the 2 * intervalMs part applies) -> settled -> returns. Updated
  // from 2 -> 3 by the settling fix; this is an intentional behaviour change
  // (an extra confirmation poll), not a regression of the transient-failure
  // tolerance this test targets.
  assert.equal(calls, 3);
});

test('watchUntilComplete: sustained fetch failures (>= maxErrors consecutive) fail loudly rather than retrying forever', async () => {
  const fetchFn = () => {
    throw new Error('simulated sustained network outage');
  };
  await assert.rejects(
    watchUntilComplete('deadbeef', '/tmp', {
      fetchFn,
      sleepFn: async () => {},
      nowFn: () => 0,
      intervalMs: 1,
      graceMs: 100000,
      timeoutMs: 100000,
      maxErrors: 3,
    }, () => {}),
    PushVerifyError
  );
});

// ---------------------------------------------------------------------------
// Run-set settling (P0 fix, Destructive rejection) — watchUntilComplete must
// not declare success the instant the CURRENTLY-VISIBLE runs are all done;
// it must wait for the observed run count to be stable across two
// consecutive polls before trusting an all-completed snapshot. Reproduces
// the Destructive's exact fixture: one completed run visible on poll 1, a
// second (still-registering) run appears only on poll 2.
// ---------------------------------------------------------------------------

test('watchUntilComplete: does NOT return success after poll 1 when a second, later-registering run turns out to have FAILED — waits for the run set to settle and reports the real outcome', async () => {
  let calls = 0;
  const fetchFn = () => {
    calls += 1;
    if (calls === 1) {
      // Poll 1: only the fast run is visible yet, and it already succeeded.
      // A tool that trusts "all currently-visible runs completed" on this
      // single poll would wrongly return exitCode 0 right here.
      return [SUCCESS_RUN];
    }
    // Poll 2 onward: the slower workflow has now registered, and it failed.
    return [SUCCESS_RUN, FAILURE_RUN];
  };
  const logs = [];
  // Small tick relative to timeoutMs (1000) so several polls fit comfortably
  // before a real timeout would fire — this test is about settling taking
  // 3+ polls, not about racing the timeout.
  let clock = 0;
  const nowFn = () => (clock += 10);
  const summary = await watchUntilComplete('deadbeef', '/tmp', {
    fetchFn,
    sleepFn: async () => {},
    nowFn,
    intervalMs: 1,
    graceMs: 1000,
    timeoutMs: 1000,
    // Settle floor disabled: this test targets the two-consecutive-polls
    // count-matching logic specifically (poll-count assertion below), not
    // the wall-clock floor, which has its own dedicated tests below.
    settleFloorMs: 0,
  }, (m) => logs.push(m));

  // Must have polled at least 3 times: poll 1 (1 run, complete but
  // unsettled), poll 2 (2 runs, complete but count just changed from 1 -> 2,
  // still unsettled), poll 3 (2 runs, count stable at 2 -> settled).
  assert.ok(calls >= 3, `expected at least 3 polls to settle, got ${calls}`);
  // The FULL settled set must be reflected in the outcome — the second run's
  // failure must NOT be masked by the first run's early success.
  assert.equal(summary.runCount, 2);
  assert.equal(summary.allCompleted, true);
  assert.equal(summary.anyFail, true);
  assert.equal(summary.exitCode, 1, 'the later-appearing failing run must flip the final verdict to a fail, not be missed');
});

test('watchUntilComplete: does NOT return success after poll 1 when a second, later-registering run also succeeds — still waits for settling, then correctly reports a full pass', async () => {
  let calls = 0;
  const fetchFn = () => {
    calls += 1;
    if (calls === 1) return [SUCCESS_RUN];
    return [SUCCESS_RUN, run({ name: 'second-workflow', conclusion: 'success' })];
  };
  let clock = 0;
  const nowFn = () => (clock += 10);
  const summary = await watchUntilComplete('deadbeef', '/tmp', {
    fetchFn,
    sleepFn: async () => {},
    nowFn,
    intervalMs: 1,
    graceMs: 1000,
    timeoutMs: 1000,
    settleFloorMs: 0,
  }, () => {});

  assert.ok(calls >= 3, `expected at least 3 polls to settle, got ${calls}`);
  assert.equal(summary.runCount, 2);
  assert.equal(summary.exitCode, 0);
});

// MUTANT CHECK: prove the settling fixture above actually distinguishes
// correct behaviour from the exact pre-fix bug (return the instant the
// FIRST poll's visible set is all-completed) — reproducing the Destructive's
// original one-poll reproduction directly against a hand-rolled "old"
// implementation shape.
test('mutant check: a watcher that trusts the first all-completed snapshot unconditionally reproduces the Destructive-reported P0 (returns success after exactly 1 poll, missing the failing second run)', async () => {
  // Minimal re-implementation of the PRE-FIX bug: return the moment
  // summarizeRuns(runs).allCompleted is true, with no settling check at all.
  async function buggyWatchUntilComplete(sha, cwd, opts) {
    const { fetchFn } = opts;
    for (;;) {
      const runs = fetchFn(sha, cwd);
      const summary = summarizeRuns(runs);
      if (summary.allCompleted) return summary;
    }
  }

  let calls = 0;
  const fetchFn = () => {
    calls += 1;
    if (calls === 1) return [SUCCESS_RUN];
    return [SUCCESS_RUN, FAILURE_RUN];
  };

  const buggySummary = await buggyWatchUntilComplete('deadbeef', '/tmp', { fetchFn });
  assert.equal(calls, 1, 'sanity: the buggy watcher really does stop after exactly one poll');
  assert.equal(buggySummary.exitCode, 0, 'sanity: the buggy watcher wrongly reports success, proving the fixture reproduces the real P0');

  // Now prove the REAL (fixed) implementation does not make this mistake on
  // the identical fixture.
  calls = 0;
  let clock = 0;
  const nowFn = () => (clock += 10);
  const fixedSummary = await watchUntilComplete('deadbeef', '/tmp', {
    fetchFn,
    sleepFn: async () => {},
    nowFn,
    intervalMs: 1,
    graceMs: 1000,
    timeoutMs: 1000,
    settleFloorMs: 0,
  }, () => {});
  assert.equal(fixedSummary.exitCode, 1, 'the fixed watcher must catch the failing second run the buggy one missed');
  assert.notEqual(calls, 1, 'the fixed watcher must not settle after a single poll');
});

// Common-case check: a single workflow with a single run must still resolve
// promptly (a bounded, small number of polls, not an ever-growing wait) once
// the run count is confirmed stable — the settling fix must not turn every
// ordinary push into an unbounded or excessive multi-poll wait.
test('watchUntilComplete: common case (one workflow, one run) still resolves promptly once settled, not endlessly delayed', async () => {
  let calls = 0;
  const fetchFn = () => {
    calls += 1;
    return [SUCCESS_RUN];
  };
  let clock = 0;
  const nowFn = () => (clock += 1000);
  const summary = await watchUntilComplete('deadbeef', '/tmp', {
    fetchFn,
    sleepFn: async () => {},
    nowFn,
    intervalMs: 1,
    graceMs: 1000,
    timeoutMs: 1000,
    // Settle floor disabled here: this test is about the bare poll-count
    // behaviour of the common case (still exactly 2 polls once the floor is
    // out of the way), not about SETTLE_FLOOR_MS itself, which has its own
    // dedicated test below (and does add extra polls in the realistic,
    // floor-enabled case).
    settleFloorMs: 0,
  }, () => {});

  assert.equal(summary.exitCode, 0);
  // Exactly 2 polls: poll 1 sees the run complete but has nothing yet to
  // compare against (never settle on the first sighting); poll 2 sees the
  // same run count (1 == 1) and, with the settle floor disabled, only needs
  // 2 * intervalMs of simulated wall-clock time to have passed (it has, via
  // the ticking nowFn) to settle. Must not take more than that when nothing
  // is actually changing between polls.
  assert.equal(calls, 2, `expected exactly 2 polls for the stable common case, got ${calls}`);
});

// Settle-floor-enabled variant of the common case: with the wall-clock floor
// left ON (not overridden to 0), the ordinary one-workflow case now needs
// enough simulated real time to elapse — not just enough polls — before
// settling. This is the "calls: 2 -> 3"-style adjustment the settling fix
// itself required previously, applied again here for the floor: the honest
// common-case poll count depends on how fast simulated time advances per
// poll relative to the floor, not on poll count alone.
test('watchUntilComplete: with the settle floor enabled, the common case still settles once real wall-clock time (not just poll count) has passed', async () => {
  let calls = 0;
  const intervalMs = 100;
  const settleFloorMs = 250; // small vs. the SETTLE_FLOOR_MS production default, to keep the test fast, but still > 2 * intervalMs so the floor (not the poll-count rule) is what's under test
  let clock = 0;
  const fetchFn = () => {
    calls += 1;
    if (calls > 1) clock += intervalMs; // simulates the real elapsed time an actual sleepFn(intervalMs) would produce between polls
    return [SUCCESS_RUN];
  };
  const nowFn = () => clock;
  const summary = await watchUntilComplete('deadbeef', '/tmp', {
    fetchFn,
    sleepFn: async () => {},
    nowFn,
    intervalMs,
    graceMs: 100000,
    timeoutMs: 100000,
    settleFloorMs,
  }, () => {});

  assert.equal(summary.exitCode, 0);
  // Floor requires 250ms since the count last changed (poll 1, t=0). Each
  // subsequent poll only advances the clock by intervalMs (100ms), so poll 2
  // (t=100) and poll 3 (t=200) are both still short of the floor; poll 4
  // (t=300) finally clears it. More polls than the bare "2 consecutive
  // matches" rule alone would need — proving the floor, not just poll count,
  // is gating settlement here.
  assert.equal(calls, 4, `expected the wall-clock floor to require 4 polls here, got ${calls}`);
});

// ---------------------------------------------------------------------------
// Residual race, exact shape (Destructive round 2's explicitly-missing test):
// run count stable at N across 2+ polls (the bare round-1 rule would already
// treat this as "settled" and return) -> an (N+1)th run registers on a LATER
// poll that only happens because the settle floor forced the watch to keep
// polling past the bare 2-poll-match point. Proves the floor -- not just the
// poll-count rule -- is what catches the late-registering run.
//
// NOTE ON HONESTY: this test proves the floor closes THIS instance of the
// race (a run registering within the floor window). It does not, and cannot,
// prove the race is closed in general -- a run that registers AFTER the
// floor window closes would still be missed. See the doc comment on
// watchUntilComplete for the disclosed residual risk.
// ---------------------------------------------------------------------------

test('watchUntilComplete: settle floor forces polling past the bare "2 consecutive matching polls" point, catching a failing run that registers within the floored window', async () => {
  let calls = 0;
  const intervalMs = 100;
  const settleFloorMs = 500; // exaggerated vs. SETTLE_FLOOR_MS's production default so the test stays fast while still spanning multiple polls
  let clock = 0;
  const fetchFn = () => {
    calls += 1;
    if (calls > 1) clock += intervalMs; // simulate real elapsed time between polls, same as an actual sleepFn(intervalMs) would produce
    // Run count is stable at 1 across calls 1-2 -- the bare round-1 "2
    // consecutive matching polls" rule would ALREADY consider this settled
    // and return right after call 2, at t=100ms. The slower second workflow
    // (which FAILS) only registers on call 4 (t=300ms) -- after the bare
    // rule would have already exited, but still well inside the 500ms
    // settle floor window measured from when the count was last stable.
    if (calls < 4) return [SUCCESS_RUN];
    return [SUCCESS_RUN, FAILURE_RUN];
  };
  const nowFn = () => clock;
  const summary = await watchUntilComplete('deadbeef', '/tmp', {
    fetchFn,
    sleepFn: async () => {},
    nowFn,
    intervalMs,
    graceMs: 100000,
    timeoutMs: 100000,
    settleFloorMs,
  }, () => {});

  // The floor must have forced polling well past the point (call 2) where
  // the bare two-poll rule would already have declared victory.
  assert.ok(calls > 4, `expected the floor to force polling past call 4 (where the failing run appeared), got ${calls} calls`);
  // The full, settled picture -- including the late-registering failure --
  // must be what's reported. It must not be masked by the early, misleading
  // "count stable at 1" snapshot from calls 1-2.
  assert.equal(summary.runCount, 2);
  assert.equal(summary.allCompleted, true);
  assert.equal(summary.anyFail, true);
  assert.equal(summary.exitCode, 1, 'the later-registering failing run, which appeared within the settle floor window, must not be missed');

  // Sanity: confirm the bare round-1 "2 consecutive matching polls" rule
  // (no wall-clock floor at all) really would have missed this -- this is
  // the EXACT residual race Destructive round 2 identified as unclosed by
  // that fix, reproduced here as a hand-rolled minimal re-implementation of
  // the pre-floor rule (mirrors the existing "mutant check" pattern above).
  function bareTwoPollSettle(fetchFn2) {
    let previous = null;
    let n = 0;
    for (;;) {
      n += 1;
      const runs = fetchFn2();
      const s = summarizeRuns(runs);
      if (s.allCompleted && previous !== null && s.runCount === previous) {
        return { summary: s, polls: n };
      }
      previous = s.runCount;
    }
  }
  let bareCalls = 0;
  const bareFetch = () => {
    bareCalls += 1;
    if (bareCalls < 4) return [SUCCESS_RUN];
    return [SUCCESS_RUN, FAILURE_RUN];
  };
  const bareResult = bareTwoPollSettle(bareFetch);
  assert.equal(
    bareResult.polls,
    2,
    'sanity: the bare (pre-floor) two-consecutive-polls rule settles after poll 2, before the failing run at poll 4 ever registers'
  );
  assert.equal(
    bareResult.summary.exitCode,
    0,
    'sanity: the pre-floor rule wrongly reports success here -- this is exactly the residual race the settle floor closes for this fixture'
  );
});

// MUTANT CHECK: prove the maxErrors ceiling is load-bearing — a version with
// no ceiling (or an infinite one) would never reject and this test would
// hang/timeout instead of completing, which is the failure mode this
// feature exists to prevent. Demonstrated narrowly (without actually
// hanging the test suite) by asserting the call count is bounded.
test('mutant check: without a maxErrors ceiling, a sustained failure would retry unboundedly instead of failing loudly', async () => {
  let calls = 0;
  const fetchFn = () => {
    calls += 1;
    throw new Error('simulated sustained network outage');
  };
  try {
    await watchUntilComplete('deadbeef', '/tmp', {
      fetchFn,
      sleepFn: async () => {},
      nowFn: () => 0,
      intervalMs: 1,
      graceMs: 100000,
      timeoutMs: 100000,
      maxErrors: 3,
    }, () => {});
    assert.fail('expected watchUntilComplete to reject');
  } catch (err) {
    assert.ok(err instanceof PushVerifyError);
  }
  // The real implementation stops at exactly maxErrors calls. A version with
  // no ceiling would keep calling fetchFn every loop iteration forever (only
  // bounded here by the fake sleepFn resolving instantly) — proving the
  // ceiling is what makes this test terminate at all.
  assert.equal(calls, 3, 'expected exactly maxErrors calls before giving up');
});
