# Acceptance Criteria — tool.agentlog (FEAT-076)

Mechanical agent dispatch/stop logging + hourly utilisation report + under-utilisation nag.
Approved plan: Aaron 2026-08-13 (target lanes = 12 per dev-team-process v1.5 caps; enforcement = measure + mechanical nag; no hard gate in v1).

Every criterion below must be individually verifiable and must be able to FAIL (process v1.9). "The guard" = `claude-dispatch-guard.js`; "the stop hook" = `claude-agent-stop.js`; "the module" = `claude-dispatch-log.js`.

## Schema

- **AC-1** `claude-sync.js ensureSchema()` creates `sync_dispatch_events` exactly as specified: columns `id` (INT AUTO_INCREMENT PK), `ts` (TIMESTAMP(6) DEFAULT CURRENT_TIMESTAMP(6)), `event` ENUM('dispatch','stop') NOT NULL, `session_id` CHAR(36) NULL, `name` VARCHAR(16) NULL, `subagent_type` VARCHAR(64) NULL, `description` VARCHAR(255) NULL, `bow_codes` VARCHAR(255) NULL, `prompt_chars` INT NULL; indexes `idx_dispatch_ts (ts)` and `idx_dispatch_session (session_id)`; ENGINE=InnoDB DEFAULT CHARSET=utf8mb4. Verify: run any claude-sync command, then `SHOW CREATE TABLE sync_dispatch_events`.
- **AC-2** The table is append-only by usage: no code path anywhere UPDATEs or DELETEs rows in it. Verify: grep the three scripts for `UPDATE sync_dispatch_events|DELETE FROM sync_dispatch_events` → zero hits.

## Dispatch logging (guard)

- **AC-3** A successful (non-denied) Agent dispatch inserts exactly one `dispatch` row carrying `session_id`, resolved identity `name`, `subagent_type`, truncated `description`, the guard's already-extracted BOW codes (comma-joined) and `prompt_chars`. Verify: live smoke — dispatch a trivial agent, SELECT the row.
- **AC-4** A DENIED dispatch inserts NO row. Verify: force a deny (unknown BOW code in a brief) and confirm no new row.
- **AC-5** Fail-open is preserved: with the DB stopped or the table absent, the dispatch still proceeds (hook exits 0) and a one-line note goes to stderr. The event-insert failure must be caught by its own try/catch — it must not reach the guard's outer fail-open handler in a way that skips the `sync_file_claims` write. Verify: point METRO_DB_PORT at a dead port; dispatch succeeds.
- **AC-6** `CLAUDE_DISABLE_DISPATCH_GUARD=1` disables event logging along with the guard (single kill switch).
- **AC-7** Identity resolution no longer relies on env alone: exported `resolveIdentity(dotClaudeDir, sessionId)` checks `.claude/.identity-<session_id>`, then `.claude/.identity`, then `CLAUDE_IDENTITY` env, then `'lead'` — matching claude-statusline.js's chain. Unit-tested with fixture dirs, including precedence when multiple sources disagree.
- **AC-8** The guard's `connect()` is exported and its host default is `'127.0.0.1'` (parity with claude-sync/claude-bow; no `localhost`/`::1` divergence). The stop hook requires it — FEAT-076 adds NO new `createConnection` site (GR#3). Verify: grep for `createConnection` across non-test claude-*.js → the same site set as before this feature (6 as of 2026-08-13: bow, bow-autoref, bow-ref-check, destructive-guard, dispatch-guard, sync — the pre-existing 6-site duplication is registered as separate GR#3 debt, not this feature's scope; the original "exactly 3 sites" wording was written from stale exploration data and amended after the Tester falsified it).

## Stop logging (new PostToolUse hook)

- **AC-9** `.claude/settings.json` gains a PostToolUse entry with matcher `"Agent"` running `node claude-agent-stop.js` (timeout 5). Existing PostToolUse entries are untouched.
- **AC-10** The stop hook inserts exactly one `stop` row per Agent PostToolUse event, carrying `session_id` and `subagent_type`/`description` when present in `tool_input`. Non-Agent payloads, unparseable stdin, and missing `tool_input` all exit 0 with no row and no exception. Every failure path exits 0.
- **AC-11** Documented fallback: if the live smoke shows PostToolUse does not fire for backgrounded Agent dispatches, the same script is re-wired under `SubagentStop` (settings.json-only change) — the script must not depend on any PostToolUse-only field except as nullable metadata.
  **AMENDMENT (2026-08-13, lead, evidence-based): the fallback IS the live wiring.** PostToolUse was proven to fire at *launch* for backgrounded Agent dispatches (a stop row landed the instant an agent was dispatched), so settings.json wires `claude-agent-stop.js` under `SubagentStop` (no matcher), and the script treats `hook_event_name === 'SubagentStop'` (no `tool`/`tool_input` present) as the stop signal, gating on `tool === 'Agent'` + `tool_input` only for PostToolUse-shaped payloads. AC-9's PostToolUse wording is satisfied by this documented fallback; AC-10's tolerance requirements apply to both payload shapes.

## Utilisation computation (module — pure, no DB in tests)

- **AC-12** `sweepConcurrency(rows, {capMs, now})`: per-session counter (+1 dispatch, −1 stop, floored at 0). Concurrency figures must be exact for parallel same-session agents regardless of FIFO pairing ambiguity — test with two overlapping agents of different durations.
- **AC-13** Unmatched dispatches (no stop within `capMs`, default 2h) are treated as ended at +capMs and their count is reported. Orphan stops (no open dispatch) never drive the counter negative.
- **AC-14** `bucketHours` produces per-hour, per-identity rows plus a TEAM rollup; an agent spanning an hour boundary contributes time-weighted average to each bucket. Hours with zero events still yield a TEAM zero row (sags must be visible, not skipped).
- **AC-15** `resolveTargetLanes(conn)` reads `project_meta.dispatch_target_lanes`; absent row/table → default 12; the report output states the target's source (project_meta key or default). GR#15: no other hardcoded target anywhere.
- **AC-16** `formatUtilTable` renders the padded table (printBowSummary style) with columns hour/who/disp/done/peak/avg/util%; util% = avgRunning/targetLanes. Golden-string tested.

## util command (claude-sync.js)

- **AC-17** `node claude-sync.js util` (default `--hours 12`) prints the table; `--hours N`, `--target N` (one-run override), `--set-target N` (writes project_meta and confirms), `--now` (compact RUNNING NOW block with per-identity counts and oldest-open age), `--json` (machine-readable buckets) all work. `util` is added to the command-list strings (both sites).
- **AC-18** A dangling value-flag (e.g. `util --hours` with nothing after) is rejected with a clear error, not silently mis-parsed (BUG-168/BUG-196 precedent).
- **AC-19** The checkin startup summary gains a one-line utilisation summary (12h window) between the BOW summary and the loop status; on empty table it says so explicitly; on failure it prints `(utilisation unavailable: ...)` — it must never abort checkin.

## Nag (Aaron ruling: measure + mechanical nag)

- **AC-20** After inserting a stop row, the stop hook computes measured running lanes and open ready-work count; if `running < target` AND ready work exists, it emits hook JSON with `additionalContext` naming the numbers (e.g. `UTILISATION: 3/12 lanes running, N ready BOW items — standing order is load up, never hold. Dispatch until saturated or name the blocker.`). No nag when `running >= target` or ready count is 0. Threshold logic unit-tested via exported `buildNag`.
- **AC-21** The nag path is failure-tolerant: any error computing it → no nag, exit 0, stop row already committed.

## Integration

- **AC-22** `.claude/commands/board.md`: context gains `node claude-sync.js util --now`; the board's running count is the measured one; the never-idle rule is upgraded to the saturation rule (below target + ready work ⇒ dispatch until saturated or name each blocker).
- **AC-23** `CLAUDE.md`: DB table list includes `sync_dispatch_events`; Dev-Team Process section notes utilisation is measured via `node claude-sync.js util`.

## Tests

- **AC-24** New `claude-dispatch-log.test.js` and `claude-agent-stop.test.js` pass under `node --test` with no DB; `claude-dispatch-guard.test.js` extended for `resolveIdentity` and the `connect` export. Every new regression test has been shown able to fail (mutate the logic, watch it fail, restore).
