BOW code: FEAT-127

# Acceptance criteria — tool.planchecker (FEAT-127, retrospective)

**Module key:** tool.planchecker (code.json GUID 3402d2ad-bd3d-4f04-9166-8e473247263f)
**BOW code:** FEAT-127
**Spec refs:** GR#3 (Single Source of Truth); GR#6 (GUID Documentation); BUG-088
(the remediation this extraction is part of); BUG-015 / BUG-112 / BUG-194 /
BUG-197 / BUG-198 (the hardening rounds narrated in the module's own header);
`docs/planning/acceptance/tool.secretguard.md` (the BUG-088 spec whose AC-B2/
B4/B5/E1/F1 this module's contract implements); `docs/planning/acceptance/tool.planguard.md`
(the PreToolUse guard this module's payload was extracted from).
**Date:** 2026-08-16
**Status:** retrospective — written after the code shipped (the module landed as
part of the BUG-088 checker/hook-layer extraction; this file states its contract
so the next round is judged against a contract, not against the code from scratch).
**Package under test:** `claude-plan-checker.js` (repo root, Node.js).
**Standard gates:** Node, not Go — SG-1/2/4/7 do not apply. `node --check
claude-plan-checker.js`; `node --test claude-plan-checker.test.js` passing;
SG-6 (no Co-Authored-By).

## Why this file exists, and what it is NOT

`claude-plan-checker.js` is the **payload half** of the plan-drift guard,
extracted from `claude-plan-guard.js` as part of the BUG-088 remediation. The
split is the whole point: BUG-088 proved the guard's *trigger* (a
boundary-anchored regex over the raw command string, deciding whether to engage
at all) was defeatable by a leading word, shell wrapper, or non-bareword git
invocation — while its *payload* (regenerate via `tools/plan/generate.js` and
hash-compare against the working tree) was always sound, because it reads real
filesystem state, never re-parses the command string. This module is that sound
payload, now `require()`-able by both the PreToolUse guard (which still calls
`checker.checkPlan()`) and a future `commit-msg` dispatcher (BUG-088 Section B,
not implemented here — ASM-762).

Two things the module is deliberately **not**, both from its own header:

1. It carries **none** of the sibling guards' boundary-regex/quote-mask/
   engage-decision machinery (AC-B4 of `tool.secretguard.md`). A `commit-msg`
   hook has no engage decision to make — git only invokes it for a real
   commit-creating verb — so copying dead trigger machinery in would misrepresent
   that trigger-parsing is still part of this design.
2. It does **not** implement the dispatcher itself. It only exports `checkPlan()`
   with a three-state return contract (AC-B5/AC-E1) precise enough that a
   follow-on dispatcher can call it mechanically.

## Behaviour

### A. The exported call contract (AC-B5 / AC-E1)

- **AC-1. `checkPlan()` takes no arguments and returns one of exactly three
  states** — `{ status: 'clean' }`, `{ status: 'found-problems', findings: [<string>, ...] }`,
  or `{ status: 'internal-error', error: <Error> }` — the same three-state
  discriminant `tool.secretguard.md` AC-E1 mandates across all four BUG-088
  checker modules, so a future dispatcher treats them uniformly. Check: a
  passing test asserts the `status` field uses these three literal values (not
  four independently-shaped objects); the contract is restated in the module
  header in enough detail a reviewer could write the dispatcher's calling code
  from the header alone.
- **AC-2. `checkPlan()` never throws** — every failure mode (missing
  `generate.js`, a spawn error, validation failure, regeneration failure, drift)
  is captured into the return value, never an uncaught exception. Check: a
  passing test forces each failure mode and asserts a structured return (see
  AC-5/AC-8), never a `throw`.

### B. The check itself (relocated, not reimplemented — AC-D4)

- **AC-3. The drift check is a regenerate-and-hash-compare, byte-for-byte the
  same logic `claude-plan-guard.js` shipped.** The sequence is: (1) validate the
  master plan via `spawnSync(node, [tools/plan/generate.js, '--check'])`; (2)
  hash `code.json` + `tools/plan/bow-import.json` as they sit in the working
  tree; (3) regenerate for real via `spawnSync(node, [tools/plan/generate.js])`;
  (4) hash again; (5) report drift if the hashes differ. Check: the fixture
  parity test (AC-9) asserts a clean working tree reports `clean`, exactly as the
  original guard's hash-compare would — relocation, not reimplementation.
- **AC-4. `hashFiles(paths)` is deterministic and content-sensitive, with a
  plain space separator and a `__MISSING__` sentinel for absent files** (the
  BUG-015 fix: the separator was a literal NUL byte that made the guard file
  itself read as binary to git). Check: a passing test asserts two identical
  files hash identically and a content change changes the hash; `grep -n "\\\\x00\|Buffer.from('__MISSING__')" claude-plan-checker.js`
  confirms the space separator and the sentinel, not the old NUL.
- **AC-5. A validation failure is a `found-problems` state, not a silent pass.**
  If `generate.js --check` exits non-zero, `checkPlan()` returns
  `found-problems` with the combined stdout/stderr as the finding. Check: a
  passing test (or a reviewer-traced code path) confirms the non-zero-status
  branch returns `found-problems`, carrying the generator's own message rather
  than a bare "it failed".
- **AC-6. Drift is reported with the specific, actionable message the working
  tree has already been refreshed in place** — the finding names
  `code.json` / `tools/plan/bow-import.json` as stale or hand-edited, states
  that `generate.js` has already refreshed both (idempotent, safe to keep), and
  directs the caller to `git diff` and re-stage. Check: the finding string is
  asserted to contain both the "stale or hand-edited" wording and the "already
  refreshed" wording — a lazy "something changed" message would fail it.

### C. The cross-process mutex (BUG-194 / BUG-197 / BUG-198)

- **AC-7. The hash-before/regenerate/hash-after critical section is serialized
  across processes with a directory-based mutex** (TOCTOU-free: `fs.mkdirSync`
  either creates the lock or throws `EEXIST` — there is no window between
  "is it free" and "make it mine"). This is the BUG-194 fix: two concurrent
  `checkPlan()` calls (this project routinely runs several `node --test`
  invocations in parallel) must not interleave and produce a spurious
  `found-problems`. Check: the BUG-194 stress test (AC-11) spawns N concurrent
  subprocesses over many rounds and asserts none reports `found-problems`
  against an already-clean tree.
- **AC-8. An orphaned lock (a holder that died without running its `finally {
  releaseLock() }`) is recovered, not blocked-on forever.** The holder writes
  its own PID into the lock directory; a waiter reads that PID and probes it
  with `process.kill(pid, 0)` (signal-0 liveness, verified on Windows — `ESRCH`
  means dead); a confirmed-dead holder's lock is reaped immediately rather than
  waiting out the 30s timeout. An `mtime` backstop (`STALE_MTIME_MS`, 5 minutes)
  covers a missing/unreadable PID file. Check: the BUG-197 repro test (AC-11)
  asserts a SIGKILLed holder's lock is reaped in under 5s, and the negative
  control asserts a live holder is **not** evicted and a waiter actually waits
  the holder's real duration.
- **AC-9. The reap decision is itself serialized by a second, nested mutex**
  (BUG-198): inspecting a suspect lock and deleting it runs under a dedicated
  reap-mutex so two waiters cannot both reap and re-acquire, minting two
  simultaneous "holders". A per-instance compare-and-delete (re-read the PID
  file immediately before deleting) is retained as a cheap secondary layer, not
  the primary fix. Check: the BUG-198 multi-waiter stress test (AC-11) asserts
  zero canary `EEXIST` double-acquire violations and zero overlapping
  hold-window violations across batches.
- **AC-10. `checkPlan()` is synchronous** (it is a `spawnSync`-based API), and
  the lock wait is therefore a synchronous sleep (`Atomics.wait`), not an async
  `setTimeout` — so the PreToolUse caller (which is not async) does not have to
  become promise-based. Check: the exported contract is synchronous; a passing
  test calls `checkPlan()` and reads its return value directly, not via
  `.then()`.

## Fail-open / fail-closed posture

- **AC-11. The module never reports `clean` unless it genuinely regenerated and
  the hashes match — every other outcome is `found-problems` or
  `internal-error`.** This is the module's own fail-closed posture
  (`tool.secretguard.md` AC-F1): an internal error is its own state, never
  silently downgraded to "clean". Check: the AC-F1 test (missing `generate.js`)
  asserts `internal-error` with a real `Error` instance, not `clean`.
- **AC-12. What the module returns is a state, not a deny/allow decision.** The
  caller (the PreToolUse `claude-plan-guard.js`, which is fail-closed, or the
  future `commit-msg` dispatcher) decides what to do with each state — the
  module's contract obligation ends at an honest, structured report. Check:
  reviewed by eye — this AC exists so a future reader does not assume the module
  emits `permissionDecision` or an exit code; it never does.
- **AC-13. The regeneration is a documented, idempotent side-effect (a mutating
  check), and the commit-msg-timing divergence is disclosed in the header
  (AC-D4).** At `commit-msg` time the regeneration happens *after* the commit's
  tree is already fixed, so refreshed files land in the working tree for the
  **next** commit, not this one — unlike at `pre-commit` time, where the same
  side-effect happens before `git write-tree`. The header names this divergence
  explicitly rather than leaving it implicit. Check: a passing test asserts the
  header references `write-tree` and both `pre-commit` and `commit-msg`; the
  mutating-check nature is flagged for review (ASM-763).

## Tests

- **AC-14. `claude-plan-checker.test.js` passes under `node --test`** and covers,
  one-to-one: AC-B4 (no boundary-regex/quote-mask trigger machinery — a grep
  over the source for `buildQuoteMask|GIT_COMMIT_RE|isRealGitCommit` finds zero
  matches), AC-B2 (the header names cherry-pick/revert/am and ASM-386), AC-D4
  (the header documents the `write-tree` divergence), AC-D4 fixture parity
  (`checkPlan()` returns `clean` against the already-regenerated working tree),
  AC-F1 (missing `generate.js` → `internal-error`, via a scratch copy that never
  touches the real generator), BUG-194 (concurrent subprocesses never spuriously
  report `found-problems`), BUG-197 (orphaned-lock reap + live-holder
  negative control + the false "stale lock still resolves" header claim removed),
  BUG-198 (multi-waiter reap-race canary/overlap), and `hashFiles` determinism.
  Check: `node --test claude-plan-checker.test.js` exits 0.
- **AC-15. The concurrency/staleness tests exercise real OS processes, not one
  event loop.** `checkPlan()` is synchronous, so the BUG-194/BUG-197/BUG-198
  races only manifest across separate processes; the tests spawn real child
  processes (a SIGKILLed holder, 12 concurrent waiters) rather than simulating
  them in-process. Check: the reviewer confirms the tests use `spawn`/`child_process`,
  not just in-process mocks — this is what makes the mutex coverage real.
- **AC-16. The tests use scratch copies of the module (not the shared repo
  files) for any state-mutating fixture**, so the BUG-112 class of bug (a test
  renaming the real shared `generate.js` away and back, which other concurrent
  processes observed as missing) cannot recur: the "missing generate.js" fixture
  copies `claude-plan-checker.js` into a throwaway directory that deliberately
  has no `tools/plan/generate.js`, and the lock fixtures use a scratch copy so
  `LOCK_PATH` is private. Check: the reviewer confirms the fixture helpers
  (`loadScratchCheckerMissingGenerate`, `loadScratchChecker`) copy the module
  into a temp dir and never mutate `E:\git\Metropolis\...` paths.

## Out of scope (stated, not silently absent)

- **Wiring this module into `.git/hooks/commit-msg`** — the dispatcher (BUG-088
  Section B) is not implemented here; this file defines the call contract
  (AC-1/AC-2) precisely enough for that wiring to be mechanical (ASM-762).
- **Closing ASM-386's cherry-pick/revert/`am` gap** — inherited from FEAT-045
  unchanged: `commit-msg` does not fire for those verbs on this project's git
  2.55.0.windows.3, and this module does not attempt to close it (the header
  names it, per AC-B2; the check re-asserts the header names it).
- **Changing the drift-detection logic** (the hash comparison method, the
  generate.js invocation, the output paths) — AC-3/AC-4 require relocation, not
  reimplementation.
- **Closing `git commit --no-verify`** — same native, hook-invisible-by-
  construction bypass `tool.committhook.md` AC-15 discloses for identity;
  unchanged and un-closeable from any hook.
- **A paired CI plan-drift check** — not raised by the brief; not assumed here.

## Assumptions

Logged via `node claude-bow.js add assumption`:

- **ASM-762** — the `commit-msg` dispatcher is not implemented; these criteria
  document the module's exported call contract, not the wiring.
- **ASM-763** — `checkPlan()` mutates the working tree (regenerates
  `code.json`/`bow-import.json` in place); idempotent and disclosed, but a
  mutating check, not a read-only one.

## Escalations

- **The mutating-check nature (ASM-763) is worth a lead eye** — a "check" that
  rewrites the very files it verifies is correct for the drift-detection purpose
  (the refresh is the remediation the finding instructs) but is exactly the kind
  of side-effect a future caller might not expect from a function named
  `checkPlan`. Already disclosed in the header; flagged here so the
  documentation pass and the commit-msg dispatcher design account for it
  explicitly rather than rediscovering it.
- **No new escalation beyond ASM-762/ASM-763** — the BUG-194/197/198 hardening
  is already its own closed record; this file's job is to make the module's
  contract greppable, not to relitigate those rounds.

- **ASM-914 (FEAT-084 CC fold).** claude-plan-checker.js header line 5 still cites BOW mkey tool.planguard while the module key is tool.planchecker per code.json (re-keying drift).
