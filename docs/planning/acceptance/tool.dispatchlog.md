BOW code: FEAT-125

# Acceptance criteria — tool.dispatchlog (FEAT-125, retrospective)

**Module key:** tool.dispatchlog (code.json GUID 2bceac9d-f4f7-46fa-9ece-914a3a98afdd)
**BOW code:** FEAT-125
**Spec refs:** FEAT-076 (the feature this module was built under); M0-ENG §5 (hooks);
GR#1 (Aggressive Error Trapping); GR#15 (Validators Derive From Data);
`docs/planning/acceptance/tool.agentlog.md` (the full-feature spec — this file's
AC numbers reference that file's AC numbering deliberately, because the module's
own doc header is written against it).
**Date:** 2026-08-16
**Status:** retrospective — written after the code shipped (the code landed under
FEAT-076 in commit `584f9fe`; this file documents the contract of the pure-logic
module only, after the fact, so a future round has something to be judged against).
**Package under test:** `claude-dispatch-log.js` (repo root, Node.js).
**Standard gates:** Node, not Go — SG-1/2/4/7 (see README.md) do not apply.
`node --check claude-dispatch-log.js`; `node --test claude-dispatch-log.test.js`
passing (pure logic, zero live DB); SG-6 (no Co-Authored-By).

## Why this file exists, and what it is NOT

`claude-dispatch-log.js` is the **pure-logic half** of FEAT-076's mechanical
agent dispatch/stop logging. FEAT-076 has four surfaces — the dispatch guard
(`claude-dispatch-guard.js`), the stop hook (`claude-agent-stop.js`), the `util`
command in `claude-sync.js`, and this module — and `tool.agentlog.md` already
specifies all four. This file documents **only the module's** contract, after
the fact, because the module's own header cites `tool.agentlog.md`'s AC numbers
(AC-2/3/10/12/13/14/15/16/20/24) but no single document states what the module
alone guarantees, fail-mode, as a requireable unit. The guard/stop-hook/util/nag
surfaces stay out of scope here (ASM-758).

The module's one architectural claim, stated in its own header and carried into
every AC below: **every exported function is either pure (no DB) or takes an
already-open `conn` and does exactly one query — none of it opens or closes a
connection itself.** That is what makes the whole file unit-testable against a
mock `{ query() }` with zero live MariaDB (AC-1 below, mirroring
`tool.agentlog.md` AC-24).

## Behaviour

### A. Row builders and the single write path

- **AC-1. Pure logic / mock-conn testability.** Every exported function is
  either pure (no I/O at all) or takes an already-open `conn` and issues exactly
  one query against it. No function opens, closes, or manages a connection, and
  no function touches `process.env`, the filesystem, or `Date.now()` as a hidden
  side effect (the one time default `Date.now()` appears it is an explicit
  function parameter default, overridable by the caller). Check:
  `grep -n "createConnection\|createPool\|mysql" claude-dispatch-log.js` finds
  zero matches; a passing test suite (`claude-dispatch-log.test.js`) exercises
  every DB-touching function against a mock `{ query() }` stub and never a live
  connection — this is the binding proof, not the grep alone.
- **AC-2. `buildDispatchEvent` produces the full dispatch row shape** (per
  `tool.agentlog.md` AC-3): `event: 'dispatch'`, `session_id`, `name`,
  `subagent_type`, `description`, `bow_codes` (an array input is comma-joined
  here; an already-joined string passes through), `prompt_chars` (a finite
  number, else `null`). Check: a passing test asserts the array-joined
  `bow_codes`, a passing test asserts each field truncates to its VARCHAR
  width (`name` 16, `description` 255, `subagent_type` 64, `session_id` 36,
  `bow_codes` 255), and a passing test asserts `prompt_chars` is `null` when not
  a finite number — all three are asserted separately because a truncation bug
  and a null-coercion bug are different defects.
- **AC-3. `buildStopEvent` produces the stop row shape with `name` always
  `null`** (per `tool.agentlog.md` AC-10): the stop hook does not reliably know
  which identity dispatched the agent, so the stop event's identity is
  deliberately absent, and concurrency accounting keys on `session_id` alone, so
  this is not a loss. `bow_codes` and `prompt_chars` are also `null`. Check: a
  passing test asserts `name === null`, `bow_codes === null`, `prompt_chars ===
  null` for a stop row, so a future change that starts populating identity on
  stop is caught by name.
- **AC-4. `insertEvent` is the only write path onto the table, and it is
  append-only** (per `tool.agentlog.md` AC-2): exactly one `INSERT INTO
  sync_dispatch_events (...)` with the row's own fields in the canonical column
  order `event, session_id, name, subagent_type, description, bow_codes,
  prompt_chars`. No `UPDATE` or `DELETE` appears anywhere in the module. Check:
  `grep -n "UPDATE\|DELETE" claude-dispatch-log.js` finds zero matches; a passing
  test asserts `insertEvent` issues exactly one INSERT and its parameter array is
  exactly the row's seven fields in order.

### B. Concurrency sweep and the unmatched-dispatch cap

- **AC-5. `sweepConcurrency` matches dispatches to stops per session by FIFO,
  and the resulting concurrency count is exact regardless of which specific
  dispatch a stop is "really" closing** (per `tool.agentlog.md` AC-12). The
  module's header spells out why: for two overlapping same-session agents, the
  open-queue depth at any instant — and therefore every derived interval's
  contribution to the running total — is invariant under which of several
  simultaneously-open dispatches a stop pops. Check: a passing test with two
  overlapping same-session dispatches of different durations asserts the running
  count is `1` / `2` / `1` / `0` across the four distinct time windows — a
  pairing-sensitive implementation (one that tried to match a specific stop to a
  specific dispatch) would not produce the same invariant.
- **AC-6. The unmatched-dispatch cap (default 2h) bounds "running" forever**
  (per `tool.agentlog.md` AC-13): a dispatch with no stop row is closed
  synthetically at `open.ts + capMs` and counted in the returned
  `unmatchedCount`; a stop with nothing open to match against (an "orphan stop")
  is dropped, never driving anything negative. Check: a passing test asserts an
  unmatched dispatch yields `unmatchedCount === 1` and an interval ending at
  `capMs` with `matched: false`; a passing test asserts an orphan stop alone
  yields zero intervals, zero unmatched, and a running count of 0; a passing test
  asserts `capMs` defaults to `DEFAULT_CAP_MS` (2h) when not supplied.
- **AC-7. `currentRunning` floors the total and every per-identity count at
  zero**, and returns a per-identity breakdown (`byName`) in addition to the
  total. Check: a passing test with two identities where one has already
  stopped asserts the total is 1 while the stopped identity's `byName` count is
  `0` (not `-1`).

### C. Hourly bucketing and the target-lane resolution

- **AC-8. `bucketHours` emits per-hour, per-identity rows plus a TEAM rollup,
  with time-weighted averaging across hour boundaries** (per
  `tool.agentlog.md` AC-14): an interval spanning a boundary contributes its
  time-weighted average to **each** hour it overlaps, not just the hour it
  started in. Check: a passing test with a dispatch at 09:30 and stop at 10:30
  asserts the identity has `avg === 0.5` in **both** the 09:00 and 10:00
  buckets, and that `disp`/`done` (which count starts/ends, not overlap) fall in
  the correct single bucket each.
- **AC-9. Hours with zero events still yield a TEAM zero row** (sags must be
  visible, not skipped), while individuals do **not** get zero rows. Check: a
  passing test on a window whose earliest hour has no events asserts that hour
  has exactly one row (`TEAM`, all counts 0); a second passing test asserts a
  quiet identity does not appear at all in a bucket it had no intervals in.
- **AC-10. `bucketHours` `peak` is true maximum-simultaneous-overlap, not a sum
  of distinct identities.** Check: a passing test with three dispatches where
  only two ever overlap asserts the TEAM `peak` is 2 (a "peak = distinct
  dispatch count" implementation would wrongly report 3).
- **AC-11. `resolveTargetLanes(conn)` reads `project_meta.dispatch_target_lanes`
  and always reports the value's source** (per `tool.agentlog.md` AC-15, and
  GR#15): present-and-parseable → `{ target, source:
  'project_meta.dispatch_target_lanes' }`; absent row **or** absent table
  (`ER_NO_SUCH_TABLE`) → `{ target: 12, source: 'default (...)' }` with a source
  string that says so; any **other** DB error (e.g. connection reset) throws
  rather than silently defaulting. Check: a passing test asserts the three
  non-error outcomes (present / absent row / missing table) and a fourth passing
  test asserts `await assert.rejects(resolveTargetLanes(conn), ...)` for a
  genuine non-missing-table error — the fourth is what distinguishes "fail-open
  on not-configured" from "silently default on a real outage".
- **AC-12. `formatUtilTable` renders the fixed, padded table** with columns
  `HOUR/WHO/DISP/DONE/PEAK/AVG/UTIL%`, golden-string asserted character-for-
  character (per `tool.agentlog.md` AC-16). Check: a passing test compares
  `formatUtilTable(rows)` against the exact expected string including trailing
  spaces; a passing test asserts the empty case is exactly the header line.
  **What a lazy implementation looks like:** a format change that renames or
  resizes a column would pass a human eye-test but fail the golden-string
  assertion — that assertion is load-bearing, not cosmetic.

### D. The nag decision

- **AC-13. `buildNag(running, target, readyCount, hookEventName)` returns the
  exact `PostToolUse` `hookSpecificOutput` JSON shape, or `null`** — and only
  when `running < target` AND `readyCount > 0` (per `tool.agentlog.md` AC-20).
  Saturated (`running >= target`) or nothing-to-load (`readyCount <= 0`) both
  yield `null`, exactly the two conditions the spec names. Check: a passing test
  deep-equals the nag's full JSON (hook event name + the exact
  `additionalContext` string carrying the numbers); a passing test asserts
  `null` for `running >= target`; a passing test asserts `null` for
  `readyCount === 0`.

## Fail-open / fail-closed posture (caller-decided, not buried here)

- **AC-14. The module is fail-open **at the call site**, not self-contained.**
  The module assumes `sync_dispatch_events` already exists (its DDL lives in
  `claude-sync.js`'s `ensureSchema()`), and it lets `ER_NO_SUCH_TABLE` and
  connection errors **propagate to its caller**, which is always a hook wrapping
  the call in its own `try/catch` — per GR#1 the fail-open decision lives at the
  call site, not buried inside the module. The module never swallows an error
  into a silent default. Check: this is a documentation posture AC — the module
  header states it (reviewed by eye); the one bounded exception is
  `resolveTargetLanes`'s `ER_NO_SUCH_TABLE` → default, which is a declared
  "nothing configured yet" state, not an error (see AC-11's fourth test, which
  proves a *real* error still throws).
- **AC-15. The module performs no UPDATE/DELETE and no side effects on the
  table beyond the single INSERT** (restated from AC-4 as the safety property:
  a logging module must not be able to destroy the history it exists to build).
  Check: `grep -n "UPDATE\|DELETE\|DROP\|TRUNCATE" claude-dispatch-log.js` finds
  zero matches.

## Tests

- **AC-16. `claude-dispatch-log.test.js` passes under `node --test` with zero
  live DB**, using a mock `{ query() }` for `insertEvent`/`resolveTargetLanes`
  and pure fixtures for everything else (per `tool.agentlog.md` AC-24). The
  suite's coverage is enumerated 1:1 by the ACs above: row builders (AC-2/3),
  the single INSERT (AC-4), the concurrency sweep and cap (AC-5/6/7), bucketing
  (AC-8/9/10), target resolution (AC-11), golden-string table (AC-12), and the
  nag decision (AC-13). Check: `node --test claude-dispatch-log.test.js` exits 0;
  a reviewer confirms no test in the file opens a network socket or a MariaDB
  client.
- **AC-17. Each regression test has been shown able to fail** (process v1.9):
  the test file's fixtures are structured so the correct answer is a fixed,
  hand-derived value (an exact open-count sequence, an exact `avg`, an exact
  golden string, an exact nag JSON), not a re-derivation of the code's own
  logic. Check: this is a review-pass AC — the reviewer confirms the assertions
  are literal expected values, and spot-mutates one (e.g. flip the FIFO to a
  stack, or change `>=` to `>` in the nag) to see the relevant test go red.

## Out of scope (stated, not silently absent)

- The dispatch guard (`claude-dispatch-guard.js`), the stop hook
  (`claude-agent-stop.js`), the `util` command, the startup-summary line, and
  the nag's **wiring** — those are FEAT-076's `tool.agentlog.md` surfaces. This
  module is the compute half they all call into; its contract stops at the
  exported-function boundary (ASM-758).
- The DDL for `sync_dispatch_events` itself — owned by `claude-sync.js`
  `ensureSchema()`, not this module (`tool.agentlog.md` AC-1).
- Any live MariaDB integration test — the module is deliberately testable with
  no DB, and a live smoke belongs to FEAT-076's integration surface, not this
  module's unit contract.

## Assumptions

Logged via `node claude-bow.js add assumption`:

- **ASM-758** — the module is caller-fail-open pure logic; these criteria cover
  the module only, with the guard/stop-hook/util/nag surfaces remaining FEAT-076
  under `tool.agentlog.md`.
- **ASM-759** — mkey duality: the module header line 1 declares `tool.dispatchlog`
  while its doc comment still says "BOW mkey: tool.agentlog" and points at
  `tool.agentlog.md`; both name the same file, documented here as a consistency
  note rather than reconciled.

## Escalations

- **The mkey duality (ASM-759) is worth a one-line header fix** — either the doc
  comment's "tool.agentlog" becomes "tool.dispatchlog (FEAT-125)" with a note
  that it was built under FEAT-076, or the code.json/BOW entry is re-scoped.
  Flagging, not deciding: this is a registry/header consistency call, not a
  criteria call, and it does not change any AC above.
