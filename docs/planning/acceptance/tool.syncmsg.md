BOW code: FEAT-069

# Acceptance criteria — claude-sync unread-message delivery: checkin surfaces directed messages, per-identity read cursor (FEAT-069)

**BOW code:** FEAT-069 (P1, open) — "claude-sync unread-message delivery: checkin surfaces
directed messages, per-identity read cursor".
**Spec refs:** No game-design-doc entry — this is dev-tooling, not engine/UI work. The only
real specification is the existing behaviour of `claude-sync.js` (the metro MariaDB session
coordination layer) and `claude-startup.js` (the SessionStart hook that drives it), both read
in full before drafting this file. `dev-team-process.md` v1.9 "an acceptance criterion's CHECK
must be able to fail." `tool.bowcli.md`'s AC-1/AC-2 (`--desc-file`/`--note-file` free-text
input pattern, BUG-090) — cited below as the precedent this file extends to the new `message`
command's body text, for the identical shell-injection reason.
**`code.json`:** `--desc` (no module registered) — this file proposes a module key (see
Escalations) but does not decide it; GR#20 registration is Bill's call at `/register-guid`
time, mirroring `feat.helper.md`'s ASM-454 precedent for an item whose criteria filename
doesn't resolve its own module-key question.
**Date:** 2026-08-12
**Status:** active — pre-dispatch, written ahead of the sprint building it (BA pipelining,
`CLAUDE.md` Dev-Team Process).
**Package under test:** `claude-sync.js` (new `message`/`unread` commands, schema additions,
`printSuccess`) and `claude-startup.js` (nothing changes there — see Design, below; message
surfacing is a `claude-sync.js checkin`-path concern, already relayed to Claude verbatim via
the existing `SUMMARY_MARKER` mechanism `claude-startup.js:80-117` uses for the BOW/Vestige/git
summary today).

## What "directed message" means (binding on every AC below)

A **directed message** is a short, free-text note aimed at one specific identity (`Bob`) or
**broadcast** to all three (`--to` omitted) — sent by whichever identity currently holds an
active permit, read by whichever identity later holds the *name* the message targets. The
project's slot model is **name-scoped, not session-scoped** (`sync_permits` is keyed by
`Bill`/`Bob`/`Ben`, `CLAUDE.md`'s Session Coordination Protocol) — a message to "Bob" is a
message to whoever is Bob next, not to today's window. This is why the read cursor below is
keyed by `name`, matching `sync_permits`' own keying, not by `window_id`/`session_id`
(matching `sync_window_map`'s keying would deliver the message to the wrong entity: a specific
window that happened to hold Bob once, not the Bob slot itself).

## Design proposal (schema and command shape — read before dispatch)

Two new tables, created idempotently inside `ensureSchema` alongside the existing four
(`sync_permits`, `sync_activity`, `sync_file_claims`, `sync_window_map`,
`claude-sync.js:98-132`), following that function's existing `CREATE TABLE IF NOT EXISTS`
style exactly:

```sql
CREATE TABLE IF NOT EXISTS sync_messages (
  id        INT AUTO_INCREMENT PRIMARY KEY,
  ts        TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  from_name VARCHAR(16) NULL,   -- sender identity (NULL only if sent by a permit-less caller — see AC-2)
  to_name   VARCHAR(16) NULL,   -- NULL = broadcast to all three identities
  body      TEXT NOT NULL
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS sync_read_cursor (
  name          VARCHAR(16) PRIMARY KEY,   -- Bill / Bob / Ben, same NAMES array
  last_read_id  INT NOT NULL DEFAULT 0
) ENGINE=InnoDB;
```

`sync_read_cursor` is seeded for all three `NAMES` the same way `sync_permits` is
(`for (const n of NAMES) INSERT IGNORE ...`, `claude-sync.js:129-131`) — a cursor row always
exists for Bill/Bob/Ben, never conditionally created on first message.

New CLI commands, added to the existing flat-verb style (`init`, `checkin`, `renew`, …,
`claude-sync.js:493-509`) — **not** a nested `message send`/`message read` subcommand shape,
to match the project's own convention:

- `message "<text>" [--to <Name>] [--body-file <path>]` — send. Requires an active permit
  (mirrors `claim`'s `if (!mine) { ...error... }` guard, `claude-sync.js:456-457`); the caller
  need not be the target. Omitting `--to` broadcasts.
- (surfacing itself is **not** a separate command — it happens inside `cmdCheckin`'s existing
  success path, see AC-5.)

## Acceptance criteria

### A. Schema

- **AC-1 (idempotent schema creation).** `sync_messages` and `sync_read_cursor` are created by
  `ensureSchema` on first run and calling `ensureSchema` a second time against an already-
  migrated database is a no-op (no error, no duplicate-table failure) — matching the existing
  four tables' `CREATE TABLE IF NOT EXISTS` behaviour exactly. `sync_read_cursor` has exactly
  three rows (`Bill`, `Bob`, `Ben`) immediately after the first `ensureSchema` run, before any
  message is ever sent. Check: `node claude-sync.js init` run twice against a throwaway
  database exits 0 both times; `SELECT COUNT(*) FROM sync_read_cursor` returns `3` immediately
  after. **False-pass warning:** a check that only asserts "no error on second run" would pass
  a naive `CREATE TABLE` (no `IF NOT EXISTS`) wrapped in a try/catch that silently swallows the
  duplicate-table error — the check must also assert the table's row count/shape is unchanged
  by the second run, not just that the process didn't crash.

### B. Sending

- **AC-2 (`message` requires an active permit; sender identity is recorded).** `message` with
  no active permit for the calling window exits non-zero with a clear error (same shape as
  `claim`'s "No active permit — checkin before claiming paths." at `claude-sync.js:457`) and
  writes no row to `sync_messages`. With an active permit, the written row's `from_name` is the
  caller's own resolved identity — never `NULL`, never trusted from an argv flag (mirrors
  `cmdWrite`'s existing `mine ? mine.row.name : null` resolution pattern, `claude-sync.js:445-
  447`, extended here to make the identity mandatory rather than optional prose). Check: a
  passing test runs `message "hi"` with no prior checkin against a fresh session id, asserts
  non-zero exit and `SELECT COUNT(*) FROM sync_messages` is unchanged; a second test checks in
  as `Bob`, runs `message "hi"`, and asserts the row's `from_name` column equals `'Bob'` via a
  direct DB query (not by parsing stdout).
- **AC-3 (broadcast vs. directed — `to_name` is the switch).** `message "<text>"` with no
  `--to` writes a row with `to_name IS NULL` (broadcast). `message "<text>" --to Bob` writes a
  row with `to_name = 'Bob'`. Check: two passing tests, one per form, each asserting the exact
  `to_name` column value via direct DB query.
- **AC-4 (unknown `--to` target rejected, no partial write).** `--to` naming anything outside
  `NAMES` (`Bill`/`Bob`/`Ben`, case-insensitive to match `checkin --name`'s own case-
  insensitive resolution at `claude-sync.js:241`) exits non-zero with `Unknown slot name
  "<value>". Valid: Bill, Bob, Ben` (reusing the exact string `checkin` already emits at
  `claude-sync.js:244`, not a second, drifting copy of the same validation message) and writes
  no row. Check: `message "hi" --to Nobody` — non-zero exit, `SELECT COUNT(*) FROM
  sync_messages` unchanged, stderr contains the exact reused string.
- **AC-5 (argv parser gap — `--to` and `--body-file` must actually consume a value).** The
  existing flag parser (`claude-sync.js:71-76`) only treats `--name` and `--session` as value-
  consuming flags; every other `--flag` is parsed as a bare boolean (`flags[a.slice(2)] =
  true`), which would silently break `--to Bob` (`--to` becomes `true`, `"Bob"` lands in
  `positional` and gets mistaken for message text) if the parser is left unchanged. The parser
  must be extended so `--to` and `--body-file` also consume the following argv token as their
  value. Check: a passing test runs `message "hi" --to Bob` and asserts (a) the stored `body`
  is exactly `"hi"`, not `"hi Bob"` or similar concatenation, and (b) `to_name` is `'Bob'` —
  this AC exists specifically because a naive extension of the existing parser (e.g. copying
  the `--name`/`--session` special case without adding `--to`/`--body-file` to it) passes a
  shallow "the flag doesn't crash" smoke test while silently corrupting the message body.
  **What a lazy implementation looks like:** leaving `--to`/`--body-file` in the boolean-flag
  branch and having `message` fish the target name out of `positional[1]` "since that's where
  it ends up anyway" — this technically works for `message "hi" --to Bob` but breaks the
  instant `--to` appears before the message text or `--body-file` is combined with `--to`, and
  is exactly the kind of positional-order coupling AC-6 below independently guards against.
- **AC-6 (`--body-file`, mirroring `tool.bowcli.md`'s AC-1 free-text-injection precedent).**
  `message` accepts `--body-file <path>` as an alternative to inline text, reading the body
  from the file's content byte-identically (same shape as `claude-bow.js`'s `--desc-file`,
  BUG-090) — because a message body is exactly the kind of copy-pasted, possibly shell-special
  free text `tool.bowcli.md` was written to get off the command line. Supplying both the
  inline-text positional and `--body-file` in the same invocation is rejected non-zero with no
  row written (mirrors `tool.bowcli.md` AC-3). Check: a passing test writes a body containing a
  backtick, `$(...)`, and an embedded double quote to a temp file, invokes `message --to Bob
  --body-file <path>` as a real subprocess, and asserts the stored `body` column is byte-
  identical to the file's content; a second test supplies both inline text and `--body-file`
  and asserts non-zero exit with no row written.

### C. Delivery on checkin (the load-bearing behaviour)

- **AC-7 (checkin surfaces unread messages for the resolved identity).** `cmdCheckin`'s success
  path (`printSuccess`, `claude-sync.js:186-198`, called from every branch that ends in a
  successful acquire-or-renew) queries `sync_messages` for rows where (`to_name = <resolved
  name>` OR `to_name IS NULL`) AND `id > sync_read_cursor.last_read_id` for that name, prints
  each one (sender, timestamp, body) before or alongside the existing startup summary block,
  and advances that identity's cursor to the highest `id` just delivered — inside the same
  transaction `cmdCheckin` already holds (`db.beginTransaction()`/`db.commit()`,
  `claude-sync.js:225/233` and equivalents on the other success branches), so a message is never
  lost silently: if the process dies after the transaction commits, the cursor has genuinely
  advanced and re-delivery is correctly skipped; if it dies before commit, nothing advanced and
  the message is (harmlessly) re-delivered next checkin — **at-least-once, never at-most-once**.
  Check: a passing test inserts a `sync_messages` row via direct DB write (`to_name = 'Bob'`),
  runs `node claude-sync.js checkin --name Bob` as a real subprocess, asserts stdout contains
  the message body, and asserts `sync_read_cursor.last_read_id` for `'Bob'` now equals or
  exceeds that row's `id` via a direct DB query taken *after* the process exits. **False-pass
  warning:** a test that only checks stdout, never re-querying the cursor afterward, would pass
  an implementation that prints unread messages but never advances the cursor — which would
  silently re-print the same message on every future checkin forever (the exact "surfaces
  once" promise the item's own title implies is untested without the DB-side assertion).
- **AC-8 (identity offline at send time — persists, delivered on next checkin regardless of
  window/session).** A message sent `--to Ben` while no window currently holds the `Ben` slot
  is stored regardless (no requirement that the target currently be checked in — `sync_messages`
  carries no `window_id`/`session_id` foreign key on the recipient side) and is delivered the
  next time **any** window checks in as `Ben`, even a different `window_id`/`session_id` than
  held `Ben` at send time — proving the cursor is genuinely per-*identity* (name-keyed), not
  per-window (which `sync_window_map`'s own keying would have produced instead, and which the
  item's own title explicitly rules out). Check: with no active `Ben` permit, send `message "hi"
  --to Ben`; then check in as `Ben` from a **different** synthetic `CLAUDE_CODE_SESSION_ID` than
  any prior session in the test; assert the message is delivered on that checkin.
- **AC-9 (sender never sees their own message flagged unread).** Sending a message — directed
  or broadcast — advances the *sender's own* read cursor to include the just-sent message's `id`
  at send time (within `message`'s own transaction), so the sender's next checkin never re-
  surfaces text they themselves just wrote. Check: check in as `Bill`, run `message "note" --to
  Bob`, then run a **second** `checkin` as `Bill` (renew-of-self path) and assert stdout does
  **not** contain "note" — while a `Bob` checkin in the same test **does** see it (control case,
  proves the suppression is sender-specific, not a global "already delivered" flag).
- **AC-10 (broadcast delivery is independent per identity — not a single shared "read" flag).**
  A broadcast message (`to_name IS NULL`) is delivered to `Bill`, `Bob`, and `Ben` independently
  — one identity's checkin advancing *their* cursor past the message must not mark it read for
  either of the other two. Check: send a broadcast; check in as `Bob` (message delivered, `Bob`'s
  cursor advances); then check in as `Ben` in the same test run and assert the broadcast is
  **still** delivered to `Ben` (proves per-name cursor rows are independent, catching the bug
  class of a single shared "highest delivered id" scalar instead of one row per name).
- **AC-11 (ordering — oldest-first, chronological).** Multiple unread messages for the same
  identity are surfaced in ascending `id` order (send order), never reverse-chronological —
  this is the opposite convention from `cmdStatus`'s `full` branch, which deliberately reverses
  `sync_activity`'s `ORDER BY id DESC ... .reverse()` (`claude-sync.js:429-433`) to also land on
  chronological display; the risk this AC guards against is a junior copying the *raw* `DESC`
  query for messages without also either reversing it or querying `ASC` directly, landing on
  newest-first by accident. Check: send three messages to `Bob` in sequence with distinct,
  identifiable bodies ("first", "second", "third"); check in as `Bob`; assert stdout shows
  "first" before "second" before "third" (index-of comparison on the raw stdout string, not just
  "all three present").
- **AC-12 (surfacing is `checkin`-only, not every `renew --auto` heartbeat).** Unread-message
  delivery fires from `cmdCheckin`'s success path only — never from `cmdRenew`'s `--auto`
  branch (`claude-sync.js:315-333`, which the `PostToolUse` hook calls on essentially every tool
  use per `CLAUDE.md`'s "DHCP-style permit auto-renewal"). Firing on every auto-renew would
  reprint delivered-and-cursor-advanced-away messages never (correct), but would also mean any
  *newly arrived* message gets shouted mid-session on the next tool call rather than waiting for
  a deliberate checkin — out of scope for v1 (see Escalations) and this AC is the regression
  guard against it happening by accident via shared code path reuse. Check: send a message to
  `Bob` while `Bob`'s permit is already active (mid-session); call `node claude-sync.js renew
  --auto` directly; assert stdout is empty (matching today's silent-heartbeat behaviour,
  `claude-sync.js:325-327`) and the cursor has **not** advanced (message still pending); only a
  subsequent genuine `checkin` (e.g. after wake recovery, or a fresh window) delivers it.

### D. Documentation

- **AC-13.** `claude-sync.js`'s own header comment (`claude-sync.js:1-46`) gains `message
  "<text>" [--to <Name>] [--body-file <path>]` to its `Commands:` list, in the same style as
  the existing entries, and a short note next to `checkin`'s existing entry stating that a
  successful checkin also delivers any unread directed/broadcast messages for the resolved
  identity and advances that identity's read cursor. Check: `grep -n "message" claude-sync.js`
  finds the new command documented in the header block (not only in the implementation).

## Out of scope

- Surfacing on `renew`/`ping`/`status`/`read` — v1 is `checkin`-only (AC-12); a future item may
  add an explicit `node claude-sync.js unread` peek-without-consuming command if the team wants
  mid-session visibility, but that is new scope, not implied by FEAT-069's own title.
- Message retention/pruning policy — `sync_messages` grows unbounded, same as the existing,
  already-unbounded `sync_activity` table (`claude-sync.js:109-114`, no pruning exists for it
  today) — not a regression this item introduces, escalated below only if Bill wants a policy
  decided now rather than deferred to match existing practice.
- Any UI/formatting richness beyond plain sender/timestamp/body text — this is a terminal tool
  output, not a rendered surface.
- Editing or recalling a sent message — not requested, not built.
- An admin/force delivery-reset (marking another identity's messages read on their behalf) —
  no such need identified; if it emerges, new BOW item.

## Escalations

- **Module key (GR#20, for Bill/`/register-guid`, not decided here).** This file proposes
  `tool.syncmsg` for the schema/command additions living entirely inside `claude-sync.js`
  (extending the same file `tool.bow`/`tool.bowcli`-adjacent items already treat as its own
  module surface), rather than folding it into an unregistered "claude-sync" umbrella key or
  deciding the final key unilaterally — mirroring `feat.helper.md`'s ASM-454 precedent (logged
  below).
- **Retention policy for `sync_messages`.** Left unbounded/unpruned to match the existing,
  already-unbounded `sync_activity` table's precedent (see Out of scope) — flagged here so it is
  a deliberate "matches existing practice" choice, not a silent oversight, and so Bill can
  override it if unbounded growth is judged unacceptable specifically for message content
  (which, unlike `sync_activity`'s terse milestone log lines, may carry longer free text).

## Assumptions logged (process v1.7)

- **ASM-472 (module key proposal, `tool.syncmsg`)** — mirroring `feat.helper.md`'s ASM-454
  pattern for an item with no existing `code.json` module to attach to; escalated to Bill for
  `/register-guid`, not decided unilaterally by this BA.
