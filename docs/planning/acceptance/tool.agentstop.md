# Acceptance criteria — tool.agentstop (FEAT-115)

**BOW code:** FEAT-115
**code.json:** `tool.agentstop` (GUID `f71c9de6-93de-4d96-81eb-1ccb8651eddb`)
**Status:** RETROSPECTIVE — code committed. This file documents the shipped contract of
`claude-agent-stop.js` (the stop-logging hook). It is written after the code, to pin the
contract for future readers and to give the Tester a fail-able checklist against the
already-landed behaviour, not to commission new work.
**Spec refs:** FEAT-076 (the parent feature — this script is the "stop" half of
mechanical dispatch/stop logging); `docs/planning/acceptance/tool.agentlog.md` (the
authoritative forward-looking spec this script's own header cites — its AC-9, AC-10,
AC-11, AC-20 and AC-21 govern this script; this file's criteria are the same contract
re-stated as a module-specific retrospective); M0-ENG §5 (hooks); GR#1 (error trapping);
GR#3 (SSOT — `connect()` reuse); GR#15 (validators derive from data).
**Date:** 2026-08-16
**Package under test:** `claude-agent-stop.js` (PostToolUse + SubagentStop hook script),
with pure-logic dependencies in `claude-dispatch-log.js` and `connect()` reused from
`claude-dispatch-guard.js`.

Every criterion below must be individually verifiable and must be able to FAIL
(process v1.9). "the script" = `claude-agent-stop.js`; "the module" =
`claude-dispatch-log.js`; "the guard" = `claude-dispatch-guard.js`.

## A. Wiring and event source

- **AC-1. The script is wired under two hook events, both pointing at the same file.**
  `.claude/settings.json` contains (a) a `PostToolUse` entry with matcher `"Agent"` whose
  command is `node claude-agent-stop.js` (timeout 5), and (b) a `SubagentStop` entry
  (no matcher) whose command is the same, both coexisting with the pre-existing
  PostToolUse entries (ping-check, bow-autoref, reflection). Check: read
  `.claude/settings.json` — the `PostToolUse` array has an `"Agent"`-matcher block and
  the `SubagentStop` array exists, each running `node claude-agent-stop.js`. **Fails
  against** a settings.json that wires only one of the two (the script's two payload
  shapes are both load-bearing — see AC-5/AC-6).

- **AC-2. `SubagentStop` is the live "a subagent finished" signal and is never gated on
  tool shape.** When `payload.hook_event_name === 'SubagentStop'`, the event itself is the
  stop signal; the script proceeds regardless of the absence of `tool`/`tool_input` (a
  SubagentStop payload carries none). This is the tool.agentlog.md AC-11 amendment made
  live: PostToolUse was proven to fire at *launch* for backgrounded Agent dispatches, so
  `SubagentStop` became the authoritative stop event. Check: spawn test
  `{ hook_event_name: 'SubagentStop', session_id: 'x' }` reaches `connect()` (observable
  via the "cannot connect" stderr note under a dead `METRO_DB_PORT`); the existing
  `claude-agent-stop.test.js` asserts this.

- **AC-3. PostToolUse-shaped payloads are gated on tool name and tool_input.** For any
  payload whose `hook_event_name` is not `SubagentStop`, the script proceeds only when
  `tool_name || tool === 'Agent'` **and** `tool_input` is an object. A non-Agent payload,
  or an Agent payload with missing/`non-object` `tool_input`, exits 0 with no row and no
  exception (tool.agentlog.md AC-10). Check: spawn tests — `{ tool_name: 'Bash' }` exits 0
  with no connect attempt; `{ tool_name: 'Agent' }` (no tool_input) exits 0 with no
  connect attempt; `{ tool_name: 'Agent', tool_input: {...} }` reaches connect.

## B. Division of labour (lead ruling, 2026-08-13)

- **AC-4. SubagentStop inserts exactly one `stop` row and emits NO nag output.** On
  `SubagentStop`, the script calls `insertEvent(conn, buildStopEvent({ sessionId,
  subagentType, description }))` once, and writes no `hookSpecificOutput`. Rationale, from
  the script header: a SubagentStop hook's JSON output is delivered back to the *dying*
  subagent — emitting the nag there would re-wake finished agents and burn tokens, the
  exact thing FEAT-076 exists to stop. Check: source review — the `SubagentStop` branch is
  the only `insertEvent(..., buildStopEvent(...))` call site, and its body contains no
  `process.stdout.write`.

- **AC-5. PostToolUse inserts NO row and computes only the nag.** Because PostToolUse
  fires at launch for backgrounded Agent dispatches, a row inserted there would
  double-count every dispatch (the guard's PreToolUse already recorded the `dispatch`
  row). The PostToolUse branch therefore calls `maybeNag(...)` only, never
  `insertEvent`. Check: source review — the `else` (non-SubagentStop) branch contains no
  `insertEvent`/`buildStopEvent` call.

- **AC-6. The script never depends on a PostToolUse-only field except as optional
  metadata.** `tool_result` and the PostToolUse-specific envelope fields are not read at
  all; only `session_id` and `tool_input.subagent_type`/`description` are used, and those
  are plain hook-payload fields present under either event. This is what let the AC-11
  fallback (re-wire under SubagentStop) be a settings.json-only change. Check: source
  review of `main()` — the fields read are `hook_event_name`, `tool_name`/`tool`,
  `tool_input`, `session_id`, plus `CLAUDE_SESSION_ID` fallback.

## C. Fail-open and kill switch

- **AC-7. Every failure path exits 0.** Enumerated, each with at most a one-line stderr
  note and never a thrown error or non-zero exit: unparseable stdin
  (`agent-stop: unparsable stdin, nothing logged`); DB connect failure
  (`agent-stop: cannot connect to metro MariaDB, stop event not logged — <msg>`);
  stop-row insert failure including `ER_NO_SUCH_TABLE` (the `sync_dispatch_events`
  pre-migration window — `agent-stop: stop-event log failed (non-fatal) — <msg>`);
  nag-computation failure (`agent-stop: nag computation failed (non-fatal, no nag) —
  <msg>`); `conn.end()` failure (swallowed, never fails the hook). The top-level
  `main().catch()` also exits 0 (`agent-stop: internal error, exiting open`). Check: spawn
  tests with `METRO_DB_PORT=1` (dead port) for the gating paths; source grep for
  `process.exit(0)` in every failure branch and in the top-level catch.

- **AC-8. The kill switch is the dispatch guard's single switch.**
  `CLAUDE_DISABLE_DISPATCH_GUARD=1` disables this hook too (tool.agentlog.md AC-6's
  "along with the guard" scope), exiting 0 silently before any stdin parse or connect.
  Check: spawn test with the env var set and an otherwise-gating-passing Agent+tool_input
  payload → exit 0, empty stderr.

## D. `connect()` reuse (GR#3)

- **AC-9. The script introduces no fourth `connect()` implementation.** It requires
  `connect` from `claude-dispatch-guard.js` (which delegates to `claude-db.js`, host
  default `127.0.0.1`); `claude-sync.js` keeps its own unrelated connect for session
  coordination. Check: `grep -n "require('./claude-dispatch-guard.js')" claude-agent-stop.js`
  matches; a grep for a local `createConnection`/`connect =` definition inside
  `claude-agent-stop.js` finds none (only the import). All heavy requires
  (`claude-dispatch-guard.js`, `claude-dispatch-log.js`) are lazy/point-of-use so a
  load-time failure cannot take the fail-open hook down.

## E. The under-utilisation nag

- **AC-10. `maybeNag` computes measured running lanes, ready-work count and target lanes,
  and gates on both numbers.** It (a) sweeps `sync_dispatch_events` over a 24h
  `LOOKBACK_MS` window via `currentRunning` (window >= `capMs` so no still-open dispatch
  falls out before its synthetic close); (b) counts ready work with the SAME SELECT shape
  `claude-bow.js`'s `ready` command uses (`status IN ('open','in_progress')` with `NOT
  EXISTS` an `open`/`in_progress`/`blocked` dependency); (c) resolves target lanes via
  `resolveTargetLanes` (`project_meta.dispatch_target_lanes`, else default 12). It returns
  `buildNag(...)` — the `hookSpecificOutput` nag — only when `running < target` AND
  `readyCount > 0`; otherwise null. Check: unit tests in `claude-agent-stop.test.js` with a
  mock conn dispatching on SQL text (nag returned at 0/12 + ready; null when no ready
  work; null when running >= target).

- **AC-11. `hookEventName` is forwarded into the emitted nag.** `maybeNag(conn, helpers,
  hookEventName)` passes the caller's event name through to `buildNag` so
  `hookSpecificOutput.hookEventName` names the event that actually fired. Regression
  guarded: a Tester finding (2026-08-13) showed the 4th argument being dropped, making the
  JSON claim `PostToolUse` under the live `SubagentStop` wiring. Check: unit tests assert
  the forwarded name (`SubagentStop`) and the back-compat default (`PostToolUse`) when the
  caller omits it.

- **AC-12. The nag is best-effort.** Any error computing it means "no nag", never a retry
  or a crash, and (on the SubagentStop path) the stop row is already committed.
  Check: source review — the PostToolUse branch wraps `maybeNag` in its own try/catch
  writing the `nag computation failed (non-fatal, no nag)` note.

## F. Tests

- **AC-13. `claude-agent-stop.test.js` passes under `node --test` with no live DB.**
  `maybeNag` is unit-tested against a mock `{ query() }` conn that dispatches on which
  table the SQL string mentions; `main()`'s payload-shape gating is spawn-tested by piping
  JSON stdin and pointing `METRO_DB_PORT` at a dead port (1) so "did this reach `connect()`"
  is observable from outside the process without a DB. Every regression test has been
  shown able to fail (mutate the logic, watch it fail, restore). Check: run
  `node --test claude-agent-stop.test.js`.

## Assumptions

Logged via `node claude-bow.js add assumption` (see BOW):

- **ASM-786** — `LOOKBACK_MS` (24h) is hardcoded in `claude-agent-stop.js` on the
  assumption it stays at least as long as `claude-dispatch-log.js`'s `DEFAULT_CAP_MS`
  (2h). If `capMs` were ever raised above 24h, a still-open dispatch could fall out of
  the nag's query window before its synthetic close and running lanes would be
  under-counted. Cross-file constant coupling with no shared source of truth.
- **ASM-787** — The SubagentStop/PostToolUse division of labour depends on the harness
  firing `PostToolUse` at *launch* for backgrounded Agent dispatches (live-verified on the
  2026-08-13 harness version). A harness change that fired `PostToolUse` at completion
  would make the `PostToolUse` entry double-count stop rows.

## Out of scope

- The `dispatch`-side logging (guard) and the `sync_dispatch_events` schema are
  `tool.agentlog`/`tool.dispatchlog` scope — see `tool.agentlog.md`.
- The `util` command and hourly utilisation table live in `claude-sync.js` /
  `claude-dispatch-log.js`, not this hook script.
