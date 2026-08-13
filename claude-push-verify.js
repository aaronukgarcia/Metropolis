#!/usr/bin/env node
/**
 * claude-push-verify.js — FEAT-059: post-push CI verification helper.
 *
 * WHY THIS EXISTS:
 * GR#2 already says "verify after every push" in prose, and prose has now
 * failed twice under a busy session: BUG-021 (closed) and the BUG-071 incident
 * where a lead's audit commit sat red on `main` because a CI run's completion
 * was watched starting but never confirmed finished. "Watched it start" and
 * "saw it finish" look identical from inside a busy session unless a tool
 * forces the distinction. This tool is that mechanism: it blocks until every
 * GitHub Actions run for a given commit SHA has actually completed, then
 * reports pass/fail/skip per run and exits accordingly. It never pushes and
 * never triggers a run itself — it only watches runs that already exist.
 *
 * USAGE:
 *   node claude-push-verify.js [sha] [options]
 *
 *   sha              Commit SHA to verify. Defaults to the current HEAD
 *                     (via `git rev-parse HEAD`) so the common case is just
 *                     `node claude-push-verify.js` immediately after a push.
 *
 *   --interval-ms N  Poll interval while waiting for runs to complete.
 *                     Default 5000.
 *   --grace-ms N     How long to wait for AT LEAST ONE run to appear for the
 *                     SHA before giving up (GitHub Actions can take a few
 *                     seconds to register a run after a push lands). Default
 *                     60000. Exceeding this with zero runs seen is a hard
 *                     error, not a silent pass — a push with no CI at all
 *                     must never be reported as "verified".
 *   --timeout-ms N   Overall ceiling on how long to wait for ALL discovered
 *                     runs to reach a completed status once at least one has
 *                     been seen. Default 1800000 (30 min). Exceeding this is
 *                     a hard error — this tool must never hang silently
 *                     forever (GR#1).
 *   --max-errors N   Consecutive `gh` invocation failures (e.g. a network
 *                     blip) tolerated mid-watch before giving up loudly.
 *                     Default 3. A single dropped poll must not abort the
 *                     watch, but a sustained failure must never be silently
 *                     retried forever either.
 *
 * EXIT CODES:
 *   0  every run for the SHA completed with conclusion "success" or was
 *      legitimately "skipped" (per-conclusion detail is still printed).
 *   1  at least one run for the SHA completed with a conclusion other than
 *      "success" or "skipped" (failure, cancelled, timed_out, neutral,
 *      action_required, stale, startup_failure, or anything unrecognised —
 *      fails closed).
 *   2  usage / environment error: `gh` not installed, `gh` not authenticated,
 *      zero runs appeared within --grace-ms, a run failed to complete within
 *      --timeout-ms, or --max-errors consecutive `gh` calls failed. None of
 *      these are "verified" — they are "could not verify", and must never be
 *      confused with exit 0 (same shape as perfci's exitCouldNotEvaluate=3
 *      in internal/harness/synth/cmd/perfci/main.go: a skipped/failed
 *      *check* must never wear a *pass's* colours, and neither may a
 *      skipped/failed *verification attempt*).
 *
 * GR#1 (error trapping): every failure mode above is caught and printed with
 * a clear, specific message on stderr before exiting — this tool never hangs
 * silently and never reports success on an inconclusive watch.
 *
 * DESIGN FOR TESTABILITY: the GitHub-facing decision logic — classifying a
 * single run's conclusion, and deciding overall pass/fail/exit-code for a
 * whole snapshot of runs — is implemented as pure functions
 * (classifyConclusion, summarizeRuns) that take plain data and return plain
 * data. They never call `gh` themselves. All process/subprocess/polling
 * concerns live in fetchRunsForSha/watchUntilComplete and are exercised only
 * via manual/live use, per this item's own brief (no throwaway repo has real
 * CI runs to test against) — see claude-push-verify.test.js.
 */

'use strict';

const { execFileSync } = require('child_process');

/** Thrown for any fatal/usage-shaped condition; caught once at the CLI boundary. */
class PushVerifyError extends Error {}

// Conclusions GitHub Actions can report on a completed run. Kept as an
// explicit allow-list (rather than "anything not failure/cancelled is fine")
// so an unrecognised future conclusion value fails closed, not open.
const PASS_CONCLUSIONS = new Set(['success']);
const SKIP_CONCLUSIONS = new Set(['skipped']);

// Minimum real wall-clock time (ms) that must have elapsed since the
// observed run count last changed (or since the watch started, if it has
// never changed) before an all-completed snapshot is trusted as "settled" —
// see the settling comment on watchUntilComplete below. Chosen as a few
// seconds: long enough to comfortably exceed the sub-second-to-low-single-
// digit-second staggering typically seen when GitHub Actions registers
// several runs for one push close together (separate workflow files, or a
// push+PR event both firing), while staying small next to the default
// --grace-ms (60000) and --timeout-ms (1800000) so the ordinary case is not
// meaningfully slowed down. Named and overridable (opts.settleFloorMs) per
// GR#15 rather than a bare literal — tests override it to stay fast, and an
// operator whose repo is known to stagger registration by longer than this
// could raise it. It is a mitigation, not a proof; see the doc comment on
// watchUntilComplete for what it does and does not guarantee.
const SETTLE_FLOOR_MS = 3000;
// Everything else GitHub can report — plus anything this tool has never seen
// — is treated as a fail. Listed explicitly for documentation purposes; the
// classifier below does not actually need this set to make its decision.
const KNOWN_FAIL_CONCLUSIONS = new Set([
  'failure',
  'cancelled',
  'timed_out',
  'action_required',
  'stale',
  'startup_failure',
  'neutral',
]);

/**
 * Classify a single run's GitHub Actions `conclusion` string into one of
 * 'pass' | 'skip' | 'fail'. This is the ONLY place that distinction is made,
 * so it is the single point a future reader (or test) needs to check for
 * the "a skip must not wear a pass's colours" requirement (FEAT-059).
 *
 * Deliberately an allow-list for 'pass' and 'skip', with everything else —
 * including null/undefined/unrecognised strings — falling through to 'fail'.
 * A gate that treats an unknown conclusion as a pass is a gate an unknown
 * future GitHub Actions status can silently defeat.
 */
function classifyConclusion(conclusion) {
  if (PASS_CONCLUSIONS.has(conclusion)) return 'pass';
  if (SKIP_CONCLUSIONS.has(conclusion)) return 'skip';
  return 'fail';
}

/**
 * Format one classified run as a single loud, human-scannable line. The
 * class tag is the FIRST token specifically so skim-reading a long watch log
 * (or grepping it) cannot mistake a SKIP line for a PASS line — they must
 * never share a tag, colour word, or column position.
 */
function formatRunLine(run, cls) {
  const workflow = run.workflowName || run.name || '(unnamed workflow)';
  const job = run.name && run.name !== workflow ? ` / ${run.name}` : '';
  const conclusion = run.conclusion == null ? '(none)' : run.conclusion;
  const tag = cls === 'pass' ? 'PASS' : cls === 'skip' ? 'SKIP' : 'FAIL';
  let note = '';
  if (cls === 'skip') {
    note = ' — legitimately skipped, NOT a pass (does not indicate the check ran and succeeded)';
  } else if (cls === 'fail') {
    note = ' — did not succeed';
  }
  return `[${tag}] ${workflow}${job} (${conclusion})${note} ${run.url || ''}`.trimEnd();
}

/**
 * Pure decision function: given a snapshot of `gh run list --json ...`
 * objects (already filtered to the SHA being verified — see
 * fetchRunsForSha), decide whether the watch is complete and, if so, whether
 * it passed.
 *
 * Input run shape (subset of gh's --json fields actually used):
 *   { name, workflowName, status, conclusion, url }
 *
 * Returns:
 *   {
 *     runCount:      number of runs in the snapshot
 *     allCompleted:  true iff every run's status === 'completed'
 *     pendingNames:  names/workflows of runs not yet completed (for progress
 *                    printing while polling)
 *     classified:    [{ run, cls }] for every run (cls only meaningful once
 *                    that run is completed; incomplete runs are still
 *                    classified 'fail' defensively but callers should gate
 *                    on allCompleted before trusting exitCode)
 *     anyFail:       true iff allCompleted and at least one run classified
 *                    'fail'
 *     exitCode:      0 or 1 when allCompleted is true; null otherwise (the
 *                    caller must keep polling, never invent an exit code
 *                    for an incomplete snapshot)
 *     lines:         formatted report lines, one per run, ready to print
 *   }
 *
 * This function makes NO subprocess calls and has no I/O — it is exhaustively
 * unit tested against constructed fixtures in claude-push-verify.test.js.
 */
function summarizeRuns(runs) {
  const runCount = runs.length;
  const pendingNames = [];
  const classified = [];

  for (const run of runs) {
    const completed = run.status === 'completed';
    if (!completed) {
      pendingNames.push(`${run.workflowName || run.name || '(unnamed)'} [${run.status}]`);
    }
    classified.push({ run, cls: classifyConclusion(run.conclusion) });
  }

  const allCompleted = runCount > 0 && pendingNames.length === 0;

  let anyFail = false;
  let exitCode = null;
  if (allCompleted) {
    anyFail = classified.some((c) => c.cls === 'fail');
    exitCode = anyFail ? 1 : 0;
  }

  const lines = classified.map((c) => formatRunLine(c.run, c.cls));

  return { runCount, allCompleted, pendingNames, classified, anyFail, exitCode, lines };
}

/**
 * Resolve the SHA to verify: the given argument if non-empty, else the
 * current HEAD via `git rev-parse HEAD`. Throws PushVerifyError if HEAD
 * cannot be resolved (not a git repo, git not on PATH) — this tool must
 * never silently guess a SHA.
 */
function resolveSha(explicitSha, cwd) {
  if (explicitSha) return explicitSha;
  try {
    return execFileSync('git', ['rev-parse', 'HEAD'], { cwd, encoding: 'utf8' }).trim();
  } catch (err) {
    throw new PushVerifyError(
      `could not resolve HEAD via "git rev-parse HEAD" (not a git repo, or git not on PATH): ${err.message}`
    );
  }
}

/**
 * Fetch the current `gh run list` snapshot for `sha`, parsed to plain
 * objects. Throws PushVerifyError with a specific, actionable message for
 * every failure mode this project has been burned by or could reasonably
 * hit: `gh` not installed, `gh` not authenticated, any other nonzero exit,
 * or output that is not valid JSON (a `gh` protocol/version drift). GR#1:
 * never swallow the underlying error message — pass it through.
 */
function fetchRunsForSha(sha, cwd) {
  const fields = 'name,workflowName,status,conclusion,url,headSha';
  let raw;
  try {
    raw = execFileSync(
      'gh',
      ['run', 'list', '--commit', sha, '--json', fields, '--limit', '100'],
      { cwd, encoding: 'utf8', maxBuffer: 16 * 1024 * 1024 }
    );
  } catch (err) {
    if (err.code === 'ENOENT') {
      throw new PushVerifyError(
        'the "gh" CLI is not installed or not on PATH — cannot verify CI runs without it. Install it and re-run.'
      );
    }
    const stderrText = err.stderr ? err.stderr.toString() : '';
    if (/auth/i.test(stderrText) || /not logged into/i.test(stderrText)) {
      throw new PushVerifyError(
        `"gh" is not authenticated: ${stderrText.trim() || err.message}. Run "gh auth login" and re-run.`
      );
    }
    throw new PushVerifyError(
      `"gh run list" failed: ${stderrText.trim() || err.message}`
    );
  }

  let parsed;
  try {
    parsed = JSON.parse(raw);
  } catch (err) {
    throw new PushVerifyError(
      `"gh run list" returned output that is not valid JSON (${err.message}); this may indicate a gh CLI version mismatch. Raw output: ${raw.slice(0, 500)}`
    );
  }
  if (!Array.isArray(parsed)) {
    throw new PushVerifyError('"gh run list --json ..." did not return a JSON array as expected');
  }
  // Defensive: gh's -c/--commit already filters server-side, but a caller
  // reusing this function (e.g. from a test harness) should not trust that
  // silently — re-filter locally so a mismatched entry never counts.
  return parsed.filter((r) => r.headSha === sha);
}

/**
 * Poll fetchRunsForSha until summarizeRuns reports allCompleted, honouring
 * grace/timeout/backoff per the module doc comment above. `sleepFn` and
 * `nowFn` are injectable so this can be driven fast in a test without real
 * timers; production use defaults to real setTimeout-based sleeping and
 * Date.now().
 *
 * RUN-SET SETTLING — A BOUNDED MITIGATION, NOT A PROOF (P0 fix from
 * Destructive round 1, narrowed further by round 2; see BOW for both):
 * "every currently-visible run is complete" is NOT the same claim as "every
 * run that is going to exist for this SHA is complete" — GitHub Actions can
 * register multiple runs for one push at staggered times (separate workflow
 * files, or a push+PR event both firing), so a fast run can appear and finish
 * before a slower, possibly-failing run has even registered with `gh run
 * list`. Declaring victory the instant the visible set happens to be all-done
 * is exactly the "watched it start, called it finished" shape this project
 * already got burned by (BUG-021/BUG-071/BUG-083) — reproduced here inside
 * the very tool meant to prevent it.
 *
 * Round 1 fix: require the OBSERVED RUN COUNT to be IDENTICAL across two
 * consecutive polls before an all-completed snapshot is trusted. Round 2
 * Destructive review found this narrows the false-positive window but does
 * NOT close it: with a small enough --interval-ms (including 0), "two
 * consecutive polls" can be satisfied in milliseconds of real time, which
 * defeats the whole point — the check needs GitHub real time to register a
 * slower workflow, and near-zero elapsed time between polls gives it none.
 *
 * Round 3 fix (this one): in addition to two consecutive matching polls, also
 * require a minimum real WALL-CLOCK time — SETTLE_FLOOR_MS, or 2 *
 * --interval-ms, whichever is larger — to have elapsed since the observed run
 * count last changed (or since the watch started, if it never has) before
 * treating the snapshot as settled. This closes the "near-zero real time"
 * loophole: even with --interval-ms 0, the watch cannot settle before
 * SETTLE_FLOOR_MS of genuine wall-clock time has passed since the count was
 * last surprising.
 *
 * WHAT THIS DOES AND DOES NOT GUARANTEE — read this before trusting a pass:
 * this is a best-effort, bounded mitigation, not a proof that no further runs
 * exist for this SHA. Finite polling fundamentally cannot prove that negative
 * — only a positive-evidence check against GitHub's own configured workflow
 * set for the repo (comparing the observed run count against how many
 * workflows are actually configured to trigger on this event) could do that,
 * and that is out of scope for this bounded fix. What the settle floor DOES
 * protect against: a workflow that registers with `gh run list` up to
 * SETTLE_FLOOR_MS (a few seconds) after another workflow for the same SHA has
 * already completed — the typical shape of GitHub Actions' staggered run
 * registration for a single push. What it does NOT protect against: a
 * workflow that takes longer than SETTLE_FLOOR_MS to register at all (an
 * unusually slow or heavily-queued runner, GitHub API lag beyond a few
 * seconds, etc.) — that run can still register after this tool has already
 * declared the watch settled and exited. This is a residual, disclosed risk,
 * not a bug to "fix" with more dev iteration on this same mechanism; an
 * operator who needs a stronger guarantee needs the positive-evidence check
 * described above, not a bigger constant here.
 *
 * Returns the final `summarizeRuns` result on success. Throws
 * PushVerifyError on any of: zero runs within graceMs, runs still incomplete
 * (or not yet settled, including not yet past the settle floor) after
 * timeoutMs (measured from first sighting), or maxErrors consecutive fetch
 * failures.
 */
async function watchUntilComplete(sha, cwd, opts, log) {
  const {
    intervalMs = 5000,
    graceMs = 60000,
    timeoutMs = 30 * 60 * 1000,
    maxErrors = 3,
    settleFloorMs = SETTLE_FLOOR_MS,
    sleepFn = (ms) => new Promise((resolve) => setTimeout(resolve, ms)),
    nowFn = () => Date.now(),
    fetchFn = fetchRunsForSha,
  } = opts;

  const startedAt = nowFn();
  let firstSeenAt = null;
  let consecutiveErrors = 0;
  // Run count observed on the PREVIOUS poll that actually saw >=1 run. null
  // until the first such poll — an all-completed snapshot is never trusted
  // against a null previous count (see settling comment on this function).
  let previousRunCount = null;
  // Wall-clock time (per nowFn) at which previousRunCount last changed value
  // (including the very first time it went from null to a real count).
  // Starts at startedAt so a run count that has NEVER changed is still
  // measured against "since the watch started" per the settle-floor doc.
  let lastCountChangeAt = startedAt;

  for (;;) {
    let runs;
    try {
      runs = fetchFn(sha, cwd);
      consecutiveErrors = 0;
    } catch (err) {
      consecutiveErrors += 1;
      log(`WARNING: poll failed (${consecutiveErrors}/${maxErrors} consecutive): ${err.message}`);
      if (consecutiveErrors >= maxErrors) {
        throw new PushVerifyError(
          `gave up after ${consecutiveErrors} consecutive failed polls of "gh run list" — likely a sustained network or auth problem. Last error: ${err.message}`
        );
      }
      await sleepFn(intervalMs);
      continue;
    }

    const now = nowFn();

    if (runs.length === 0) {
      if (firstSeenAt === null && now - startedAt >= graceMs) {
        throw new PushVerifyError(
          `no GitHub Actions runs found for commit ${sha} after waiting ${graceMs}ms. Either Actions has not registered the run yet (try a longer --grace-ms), no workflow triggers on this event, or ${sha} was never actually pushed.`
        );
      }
      await sleepFn(intervalMs);
      continue;
    }

    if (firstSeenAt === null) firstSeenAt = now;

    const summary = summarizeRuns(runs);

    // Did the observed run count change on this poll (including the very
    // first time it becomes non-null)? If so, the settle clock restarts here
    // — see the settle-floor doc comment above this function.
    const runCountChanged = previousRunCount === null || summary.runCount !== previousRunCount;
    if (runCountChanged) {
      lastCountChangeAt = now;
    }
    const settleWindowMs = Math.max(2 * intervalMs, settleFloorMs);
    const elapsedSinceLastChange = now - lastCountChangeAt;
    const countStableAcrossPolls =
      summary.allCompleted && previousRunCount !== null && summary.runCount === previousRunCount;
    const settled = countStableAcrossPolls && elapsedSinceLastChange >= settleWindowMs;

    if (settled) {
      return summary;
    }

    if (summary.allCompleted) {
      if (countStableAcrossPolls) {
        // Count has matched for 2+ consecutive polls, but not enough real
        // wall-clock time has passed since it last changed to trust that no
        // further runs are about to register (the settle floor — see doc
        // comment above). Keep polling; this is NOT an error, just not yet
        // settled.
        log(
          `run count stable at ${summary.runCount} but only ${elapsedSinceLastChange}ms have elapsed since it last changed (settle floor requires ${settleWindowMs}ms) — waiting before treating this as settled`
        );
      } else {
        // Every currently-visible run is done, but the run count has not yet
        // been confirmed stable across two consecutive polls (either this is
        // the very first poll that saw any runs, or the count just changed) —
        // more runs may still be about to register with GitHub Actions. Do NOT
        // treat this as a pass yet; poll again and compare counts.
        log(
          previousRunCount === null
            ? `all ${summary.runCount} currently-visible run(s) completed on first sighting — waiting for the run set to settle (matching run count across polls, held for the settle floor) before treating this as done`
            : `run count changed since last poll (${previousRunCount} -> ${summary.runCount}); currently-visible runs are complete but the set may still be growing — waiting for it to stabilise`
        );
      }
    } else {
      log(`waiting on: ${summary.pendingNames.join(', ')}`);
    }

    previousRunCount = summary.runCount;

    if (now - firstSeenAt >= timeoutMs) {
      throw new PushVerifyError(
        summary.allCompleted
          ? `timed out after ${timeoutMs}ms waiting for the run set for commit ${sha} to settle: every currently-visible run completed, but the observed run count never held stable for the settle floor (${settleWindowMs}ms; still ${summary.runCount} at timeout) — more runs may still be registering, or --interval-ms may be too coarse relative to how GitHub Actions is staggering runs for this SHA. This tool cannot prove no further runs exist — see the settling doc comment on watchUntilComplete for what a pass here does and does not guarantee.`
          : `timed out after ${timeoutMs}ms waiting for all runs to complete for commit ${sha}. Still pending: ${summary.pendingNames.join(', ')}`
      );
    }

    await sleepFn(intervalMs);
  }
}

function parseArgs(argv) {
  const args = argv.slice(2);
  let sha = '';
  const opts = {};
  const numericFlags = {
    '--interval-ms': 'intervalMs',
    '--grace-ms': 'graceMs',
    '--timeout-ms': 'timeoutMs',
    '--max-errors': 'maxErrors',
  };
  for (let i = 0; i < args.length; i += 1) {
    const arg = args[i];
    if (numericFlags[arg]) {
      const key = numericFlags[arg];
      const value = Number(args[i + 1]);
      if (!Number.isFinite(value) || value < 0) {
        throw new PushVerifyError(`${arg} requires a non-negative number, got ${JSON.stringify(args[i + 1])}`);
      }
      opts[key] = value;
      i += 1;
    } else if (arg === '--help' || arg === '-h') {
      opts.help = true;
    } else if (!arg.startsWith('-') && !sha) {
      sha = arg;
    } else {
      throw new PushVerifyError(`unrecognised argument: ${arg}`);
    }
  }
  return { sha, opts };
}

function printUsage(stream) {
  stream.write(
    [
      'Usage: node claude-push-verify.js [sha] [--interval-ms N] [--grace-ms N] [--timeout-ms N] [--max-errors N]',
      '',
      '  Watches ALL GitHub Actions workflow runs for `sha` (default: HEAD) to',
      '  completion via the gh CLI, prints each run\'s conclusion loudly, and',
      '  exits 0 only if every run succeeded or was legitimately skipped.',
      '  Exits 1 if any run failed/cancelled/etc. Exits 2 on a usage or',
      '  environment problem (gh missing, not authenticated, no runs found,',
      '  timed out, or sustained network failure) — never confused with a',
      '  genuine pass.',
      '',
    ].join('\n')
  );
}

async function main(argv, stdout, stderr) {
  let sha;
  let opts;
  try {
    ({ sha, opts } = parseArgs(argv));
  } catch (err) {
    stderr.write(`ERROR: ${err.message}\n`);
    printUsage(stderr);
    return 2;
  }

  if (opts.help) {
    printUsage(stdout);
    return 0;
  }

  const cwd = process.cwd();
  const log = (msg) => stdout.write(`claude-push-verify: ${msg}\n`);

  let resolvedSha;
  try {
    resolvedSha = resolveSha(sha, cwd);
  } catch (err) {
    stderr.write(`ERROR: ${err.message}\n`);
    return 2;
  }

  log(`watching GitHub Actions runs for commit ${resolvedSha} ...`);

  let summary;
  try {
    summary = await watchUntilComplete(resolvedSha, cwd, opts, log);
  } catch (err) {
    stderr.write(`ERROR: ${err.message}\n`);
    stderr.write('claude-push-verify: COULD NOT VERIFY — this is NOT a pass. Do not treat the push as confirmed green.\n');
    return 2;
  }

  for (const line of summary.lines) {
    stdout.write(`${line}\n`);
  }

  if (summary.anyFail) {
    stderr.write(`claude-push-verify: FAIL — at least one run for ${resolvedSha} did not succeed.\n`);
    return 1;
  }

  log(`PASS — all ${summary.runCount} run(s) for ${resolvedSha} succeeded or were legitimately skipped.`);
  return 0;
}

if (require.main === module) {
  main(process.argv, process.stdout, process.stderr)
    .then((code) => {
      process.exitCode = code;
    })
    .catch((err) => {
      // Belt-and-braces: any bug in main() itself must still fail loudly
      // and nonzero, never hang or report a false pass (GR#1).
      process.stderr.write(`ERROR: unexpected failure in claude-push-verify: ${err.stack || err.message}\n`);
      process.exitCode = 2;
    });
}

module.exports = {
  PushVerifyError,
  classifyConclusion,
  formatRunLine,
  summarizeRuns,
  resolveSha,
  fetchRunsForSha,
  watchUntilComplete,
  parseArgs,
  main,
};
