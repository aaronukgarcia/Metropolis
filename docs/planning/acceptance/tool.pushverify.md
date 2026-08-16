BOW code: FEAT-128

# Acceptance criteria — tool.pushverify (FEAT-128, retrospective)

**Module key:** tool.pushverify (code.json GUID 45fbf782-a019-49a9-aec1-ed8d7e7c3e60)
**BOW code:** FEAT-128
**Spec refs:** GR#24 (No Code Left Behind — "verify after every push");
FEAT-059 (the feature this tool was built under, `claude-push-verify.js`);
BUG-021 / BUG-071 / BUG-083 (the three "watched it start, called it finished"
incidents the tool exists to prevent); GR#1 (Aggressive Error Trapping); GR#15
(Validators Derive From Data — the named, overridable constants).
**Date:** 2026-08-16
**Status:** retrospective — written after the code shipped (the tool landed as
part of the FEAT-059 wave in commit `b96559a`; this file states its contract so
the next round is judged against a contract, not against the code from scratch).
**Package under test:** `claude-push-verify.js` (repo root, Node.js).
**Standard gates:** Node, not Go — SG-1/2/4/7 do not apply. `node --check
claude-push-verify.js`; `node --test claude-push-verify.test.js` passing; SG-6
(no Co-Authored-By).

## Why this file exists

GR#2 says "verify after every push" in prose, and prose failed twice under a
busy session (BUG-021, and the BUG-071 incident where a lead's audit commit sat
red on `main` because a CI run's completion was watched *starting* but never
confirmed *finished*). "Watched it start" and "saw it finish" look identical
from inside a busy session unless a tool forces the distinction. This tool is
that mechanism: it blocks until every GitHub Actions run for a given commit SHA
has actually **completed**, then reports pass/fail/skip per run and exits
accordingly. It never pushes and never triggers a run — it only watches runs
that already exist.

The tool's one hard, load-bearing rule, stated in its header and carried into
every AC: **a skipped/failed *check* must never wear a *pass's* colours, and a
skipped/failed *verification attempt* must never either.** Exit 0 means
"verified green." Exit 1 means "verified red." Exit 2 means "could not verify"
— and "could not verify" is never reported as 0.

## Behaviour

### A. Conclusion classification (the single point of the pass/skip/fail distinction)

- **AC-1. `classifyConclusion(conclusion)` maps `success` → `pass`, `skipped` →
  `skip`, and everything else → `fail`.** The pass and skip sets are explicit
  allow-lists, not "anything that isn't failure is fine": `failure`, `cancelled`,
  `timed_out`, `neutral`, `action_required`, `stale`, `startup_failure`, and any
  unrecognised future value (including `null`/`undefined`) all classify `fail`.
  This is the one place the distinction is made, so it is the single point a
  future reader or test needs to check for the "a skip must not wear a pass's
  colours" requirement. Check: a passing test asserts `success` → `pass`,
  `skipped` → `skip` (and `skip !== pass`), the seven named fail conclusions →
  `fail`, and `null`/`undefined`/`'some_future_github_status_nobody_has_seen_yet'`
  → `fail` — the unrecognised-value case is load-bearing: a gate that treats an
  unknown conclusion as pass is a gate an unknown future GitHub status can
  silently defeat.
- **AC-2. `formatRunLine(run, cls)` puts the class tag as the first token and
  makes SKIP visibly distinct from PASS.** A skip line carries the explicit note
  "legitimately skipped, NOT a pass (does not indicate the check ran and
  succeeded)"; a fail line carries "did not succeed". The tag-first rule exists
  so skim-reading or grepping a long watch log cannot mistake a SKIP line for a
  PASS line. Check: a passing test asserts the pass line starts `[PASS]`, the
  skip line starts `[SKIP]` and contains "not a pass", the fail line starts
  `[FAIL]`, and a mutant check proves a formatter that collapsed pass/skip into
  one tag would fail the distinctness assertion.

### B. The decision function

- **AC-3. `summarizeRuns(runs)` returns a complete snapshot decision object**
  — `runCount`, `allCompleted` (true iff `runCount > 0` and every run's `status
  === 'completed'`), `pendingNames`, `classified` (one `{ run, cls }` per run),
  `anyFail` (true iff allCompleted and at least one run classified `fail`),
  `exitCode` (`0`/`1` when allCompleted, else `null`), and `lines`. It makes no
  subprocess calls and has no I/O — exhaustively unit-testable against
  constructed fixtures. Check: a passing test asserts the exact object shape for
  a completed single-success run (`exitCode 0`), a completed single-failure run
  (`exitCode 1`), a cancelled run (`exitCode 1`), a skipped run (`exitCode 0`
  with `cls === 'skip'`, reported distinctly from pass), an in-progress run
  (`allCompleted false`, `exitCode null`), and an empty list (`allCompleted
  false`, `exitCode null` — zero runs is never treated as a completed pass).
- **AC-4. One failure among many successes still fails.** A passing/skipping
  majority never masks one real failure: `summarizeRuns([success, skip,
  failure])` → `exitCode 1`. Check: a passing test asserts this, and two mutant
  checks prove a first-run-only summarizer and an ANY-passed (instead of
  NONE-failed) summarizer would both wrongly report success on the mixed fixture
  — proving the fixture distinguishes correct from wrong, not merely "the
  function returns something".

### C. Argument parsing and SHA resolution

- **AC-5. `parseArgs(argv)` handles the positional SHA and the four numeric
  flags** (`--interval-ms`, `--grace-ms`, `--timeout-ms`, `--max-errors`) plus
  `--help`/`-h`, rejects an unrecognised flag, and rejects a non-numeric or
  negative value for a numeric flag. Check: a passing test asserts the no-arg
  case yields empty `sha`/`opts`, the full-flag case parses each number, `--help`
  sets `opts.help`, and both the unrecognised-flag and non-numeric cases throw
  `PushVerifyError`.
- **AC-6. `resolveSha(explicitSha, cwd)` returns the explicit SHA if given, else
  `git rev-parse HEAD`** — and throws `PushVerifyError` (never silently guesses)
  if HEAD cannot be resolved (not a git repo, git not on PATH). Check: a passing
  test (or a reviewer-traced path) confirms the explicit-SHA short-circuit and
  the throw-on-unresolvable path.

### D. The watch loop

- **AC-7. `watchUntilComplete(sha, cwd, opts, log)` polls `fetchRunsForSha`
  until `summarizeRuns` reports `allCompleted`, honouring `graceMs` (zero runs →
  hard error), `timeoutMs` (runs never complete → hard error), and `maxErrors`
  (consecutive `gh` failures → hard error).** `sleepFn`/`nowFn`/`fetchFn` are
  injectable so the loop can be driven fast and deterministically in tests with
  no real subprocess or timers. Check: a passing test with fake fetch/sleep/now
  asserts resolution once `allCompleted`; a passing test asserts `PushVerifyError`
  when zero runs ever appear before `graceMs`; a passing test asserts
  `PushVerifyError` when runs never complete before `timeoutMs`; a passing test
  asserts a single transient fetch failure is tolerated and retried, while
  `maxErrors` consecutive failures fail loudly (with a mutant check proving the
  `maxErrors` ceiling is what makes the test terminate at all).
- **AC-8. The run-set must settle before a pass is trusted** — "every
  currently-visible run is complete" is not the same claim as "every run that is
  going to exist for this SHA is complete". GitHub Actions can register multiple
  runs for one push at staggered times, so a fast run can appear and finish
  before a slower, possibly-failing run has even registered. The watch therefore
  requires the observed run count to be **identical across two consecutive
  polls** AND a minimum real wall-clock time (`max(SETTLE_FLOOR_MS, 2 *
  intervalMs)`, `SETTLE_FLOOR_MS = 3000` default) to have elapsed since the run
  count last changed, before an all-completed snapshot is treated as settled.
  Check: the dedicated settling tests (see AC-11) assert a later-registering
  failing run flips the final verdict to fail, and that the bare two-consecutive-
  polls rule alone (without the floor) would have wrongly passed the same
  fixture.
- **AC-9. `fetchRunsForSha(sha, cwd)` surfaces every failure mode with a
  specific, actionable message** (GR#1): `gh` not installed (`ENOENT`), `gh` not
  authenticated (stderr matching `auth`/`not logged into`), any other non-zero
  exit (passing the underlying stderr through), output that is not valid JSON, a
  non-array result, and — defensively — it re-filters locally by `headSha ===
  sha` so a mismatched entry never counts even though `--commit` already filters
  server-side. Check: a passing test (or reviewer-traced path) confirms each
  throw is a `PushVerifyError` carrying the underlying error, never a swallow.
- **AC-10. The ordinary one-workflow, one-run case still resolves promptly**
  (a bounded small number of polls, not an ever-growing wait) once the run count
  is confirmed stable — the settling fix must not turn every ordinary push into
  an unbounded or excessive wait. Check: a passing test asserts the common case
  settles in exactly 2 polls with the floor disabled, and in the floor-enabled
  case settles once real wall-clock time (not just poll count) has passed.

## Fail-open / fail-closed posture (fails closed on the verification)

- **AC-11. The tool never reports a pass on an inconclusive watch.** Exit 0
  requires every discovered run to be `success` or legitimately `skipped`; exit
  1 means at least one run finished with anything else; exit 2 means the watch
  itself could not complete — `gh` missing, `gh` unauthenticated, zero runs
  within `--grace-ms`, runs incomplete within `--timeout-ms`, or `--max-errors`
  consecutive `gh` failures. "Could not verify" is printed explicitly as
  "NOT a pass" and exits 2, never 0. Check: the exit-code triage is asserted by
  the pure-function tests (AC-1/AC-3) and the watch-loop tests (AC-7), and the
  `main()` boundary writes the "COULD NOT VERIFY — this is NOT a pass" line on
  every watch failure (reviewer-traced).
- **AC-12. `main()` cannot throw.** A belt-and-braces top-level `.catch()` on the
  `main()` promise writes the unexpected error to stderr and sets exit code 2, so
  a bug in `main()` itself still fails loudly and non-zero, never hangs and never
  reports a false pass (GR#1). Check: `grep -n "unexpected failure in claude-push-verify"
  claude-push-verify.js` matches the catch handler; reviewed by eye that it sets
  `process.exitCode = 2`.
- **AC-13. The settle floor is a bounded mitigation, not a proof — and the
  header says so.** Finite polling fundamentally cannot prove no further runs
  exist for a SHA; the floor protects against a workflow that registers within
  `SETTLE_FLOOR_MS` (a few seconds) of another completing, and does **not**
  protect against a workflow that takes longer than the floor to register at
  all. The header discloses this residual risk plainly rather than overclaiming.
  Check: the header contains the "WHAT THIS DOES AND DOES NOT GUARANTEE" framing
  (reviewed by eye); the residual-race test in the suite asserts the floor
  catches the within-window case while its own comment states it cannot prove
  the general case (ASM-764).

## Tests

- **AC-14. `claude-push-verify.test.js` passes under `node --test`** and covers:
  `classifyConclusion` (pass/skip/all-fail/unrecognised), `formatRunLine`
  (tag-first + skip-not-pass + a mutant check), `summarizeRuns` (single pass /
  fail / cancelled / skip / in-progress / empty / mixed-with-one-failure /
  mixed-success-skip / mixed-success-skip-failure, plus two decision mutant
  checks and a skip/pass-collapse mutant check), `parseArgs` (no-arg / full /
  `--help` / unknown-flag / non-numeric), and `watchUntilComplete` (resolve-on-
  allCompleted, zero-runs-grace, never-complete-timeout, transient-failure
  tolerance, sustained-failure ceiling, the two settling tests, the residual-
  race test, and the common-case promptness tests). Check: `node --test
  claude-push-verify.test.js` exits 0.
- **AC-15. Every "exits nonzero" test is paired with an explicit mutant check
  proving the fixture can fail** (per FEAT-059's quality bar, "every test must
  be proven able to fail") — a first-run-only summarizer, an ANY-passed
  summarizer, a pass/skip-collapsing formatter, a no-ceiling maxErrors, and a
  one-poll-trusting watcher are each re-implemented as a hand-rolled "wrong"
  version and asserted to produce the wrong answer on the same fixture. Check:
  the reviewer confirms the mutant checks exist and each is shown to disagree
  with the correct implementation, not merely to exist.
- **AC-16. The `gh` subprocess and polling glue are exercised via injected
  fakes and manual live use, not against real GitHub Actions** (ASM-765) —
  the pure decision functions carry the exhaustive unit coverage; `fetchRunsForSha`/
  `watchUntilComplete`/`parseArgs`/`main` are covered by fast async-flow tests
  with fake fetch/sleep/now and by manual live use per FEAT-059's own brief (no
  throwaway repo has real CI runs to test against). Check: the test file's own
  header states this split (reviewed by eye), and no test in the file shells out
  to a real `gh` or a real GitHub repo.

## Out of scope (stated, not silently absent)

- A positive-evidence check against GitHub's configured workflow set (comparing
  the observed run count against how many workflows are actually configured to
  trigger on this event) — the only thing that could prove "no further runs
  exist." Out of scope for this bounded fix; an operator needing a stronger
  guarantee needs that, not a bigger constant (ASM-764).
- Triggering runs, or pushing — the tool only watches runs that already exist.
- A paired deterministic-replay of the CI result — the tool reports GitHub's
  own conclusion, it does not re-derive one.
- Any change to the exit-code contract (0/1/2) — that is the tool's interface
  to the `/push` skill and to operators, and AC-11 states it as fixed.

## Assumptions

Logged via `node claude-bow.js add assumption`:

- **ASM-764** — the run-set settle floor is a bounded mitigation, not a proof;
  a workflow registering after the floor is a residual, disclosed risk, not a
  bug to fix with a bigger constant.
- **ASM-765** — the `gh` subprocess/polling glue is tested via injected fakes
  plus manual live use, not against real GitHub Actions runs, per FEAT-059's
  brief.

## Escalations

- **The residual-race disclosure (ASM-764) should be visible to the `/push`
  skill's operator-facing copy**, so an operator who reads "PASS — all runs
  succeeded" also knows it is "all *discovered* runs, after a bounded settle
  window," not a mathematical proof of completeness. Already disclosed in the
  header; flagged so the `/push` skill surface states it too rather than
  relaying the bare PASS line as stronger than it is.
