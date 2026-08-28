BOW code: FEAT-070

# Acceptance criteria — startup hook auto-arms each identity's standing /loop (FEAT-070)

**BOW code:** FEAT-070 (P1, open) — "Startup hook auto-arms each identity's standing /loop --
loops survive reboot by re-arming at launch, not by persisting".
**Spec refs:** No game-design-doc entry — dev-tooling, not engine/UI work. Grounded in the
actual, observed behaviour of the harness's `/loop` mechanism this very session (a Claude Code
`CronCreate`-backed schedule is **session-only** — not written to disk, dies when the Claude
process exits) and the existing `claude-sync.js`/`claude-startup.js` coordination layer (both
read in full before drafting this file). `dev-team-process.md`'s "loops & commit-ready
protocol" (2026-08-12, in force per `CLAUDE.md`'s Dev-Team Process): "the lead sweeps and
commits in batches on a self-paced `/loop`... worker windows run their own `/loop 15m` so no
session ever idles" — this item is what makes that standing configuration survive a session
restart without a human re-typing `/loop` every time a window relaunches.
**`code.json`:** key `tool.looparm` is **ABSENT** (verified 2026-08-27: `grep -n '"key": "tool.looparm"' code.json` returns no matches). This file's filename and ASM-473 proposed that key; the fold records registration status, it does not invent a replacement key. GR#20 registration remains Bill's `/register-guid` call.
**Date:** 2026-08-12
**Status:** active — pre-dispatch, written ahead of the sprint building it.
**Package under test:** `claude-sync.js` (new `loop-set`/`loop-clear`/`loop-show` commands,
schema addition, `read --full` extension) and `claude-startup.js` (`printSessionSummary`,
`claude-startup.js:88-117` — the one place that already prints mandatory startup instructions
for Claude to act on, e.g. its existing step 1: "Use the mcp__vestige__search tool NOW").

## What this item can and cannot literally do (binding constraint on every AC below)

`claude-startup.js` is a Node script run by the `SessionStart` hook — it executes **before**
the agent turn begins and has no mechanism to itself invoke the Claude Code `/loop` slash
command (that is an agent-level tool call, not a shell-reachable action). "Auto-arms... at
launch" can therefore only mean: **the hook prints a mandatory instruction telling the agent to
invoke `/loop <stored spec>` as one of its first actions**, in exactly the same "mandatory,
before your first response" style the hook already uses for its existing step 1 (Vestige
search, `claude-startup.js:111`). This is not a weaker version of "auto-arm" invented to dodge
scope — it is the *only* mechanism available, and it inherits the exact same trust boundary the
existing Vestige-search instruction already has (a script cannot force an agent to obey a
printed instruction; it can only make the instruction unambiguous and unmissable). No AC below
may be satisfied by claiming the hook literally executes `/loop` itself — that is not buildable
as specified and any implementation attempting it belongs to a different, larger item (a
programmatic `/loop`-equivalent trigger) not this one.

## Design proposal (schema and command shape — read before dispatch)

One new table, created idempotently inside `ensureSchema` alongside the existing tables
(`claude-sync.js:98-132`):

```sql
CREATE TABLE IF NOT EXISTS sync_loop_config (
  name           VARCHAR(16) PRIMARY KEY,  -- Bill / Bob / Ben
  spec           TEXT NOT NULL,            -- exact /loop argument text, e.g. "15m /oversight-sweep"
  set_ms         BIGINT NOT NULL,
  set_by_session CHAR(36) NULL,
  last_armed_ms  BIGINT NULL,
  armed_count    INT NOT NULL DEFAULT 0
) ENGINE=InnoDB;
```

Deliberately **no `boot_id` column** — unlike `sync_permits` (`claude-sync.js:106`, whose
`boot_id` mismatch is exactly how a dead holder is proven dead across a reboot), this table's
entire purpose is to survive a reboot; gating it on `BOOT_ID` would defeat the item's own title.
A row is not tied to any one window/session beyond `set_by_session` (recorded for audit only,
never used to gate re-arming — matching how `sync_permits` is name-scoped, not window-scoped,
per `tool.syncmsg.md`'s identical reasoning for message delivery).

New flat-verb commands, matching the project's existing style (`claude-sync.js:493-509`):

- `loop-set "<interval> <command>"` — e.g. `node claude-sync.js loop-set "15m /oversight-sweep"`
  (positional text, exactly like `write`'s message argument, `claude-sync.js:439-448` —
  deliberately **not** a `--spec` value-flag, sidestepping `tool.syncmsg.md`'s AC-5 argv-parser
  gotcha entirely by not needing one).
- `loop-clear` — clears the calling identity's own row.
- `loop-show` — prints the calling identity's own row (spec, age, last-armed age, armed count),
  or "no standing loop configured" if none.

**Staleness** is measured from whichever is more recent of `set_ms` / `last_armed_ms` — a loop
re-armed every session for months never goes stale purely because it was originally configured
long ago; only genuine, sustained non-use ages it out. `LOOP_STALE_MS` is an env-overridable
constant (`METRO_LOOP_STALE_MS`, mirroring `TTL_MS`/`RESERVE_MS`'s existing env-override
precedent at `claude-sync.js:56-59`/`85-89`), proposed default 72 hours — see Escalations for
why the exact number is not this BA's call.

## Acceptance criteria

### A. Schema and configuration commands

- **AC-1 (idempotent schema creation).** `sync_loop_config` is created by `ensureSchema` on
  first run; a second `ensureSchema` run against an already-migrated database is a no-op. The
  table has **no** `boot_id` column (see Design — this is load-bearing, not incidental). Check:
  `node claude-sync.js init` run twice exits 0 both times; `DESCRIBE sync_loop_config` (or
  `information_schema.COLUMNS`) confirms no `boot_id` column exists.
- **AC-2 (`loop-set` requires an active permit; upserts the caller's own row).** `loop-set
  "<spec>"` with no active permit exits non-zero with no row written/changed (same error shape
  as `claim`'s permit guard). With an active permit, it upserts (`REPLACE INTO`, matching
  `acquire`'s own `sync_window_map` upsert pattern, `claude-sync.js:166-167`) the caller's own
  row: `spec` set verbatim, `set_ms = now`, `set_by_session` = the caller's permit session id,
  and — critically — `armed_count` reset to `0` and `last_armed_ms` cleared to `NULL` on every
  `loop-set` (a re-set is a fresh commitment, not a continuation of the old spec's arm history).
  Check: a passing test checks in as `Bob`, runs `loop-set "10m /foo"`, and asserts via direct DB
  query that the row's `spec`, `set_ms`, `armed_count=0`, `last_armed_ms IS NULL` are all
  correct; running `loop-set` a second time with a different spec after a fixture-advanced
  `armed_count` confirms the reset.
- **AC-3 (empty spec rejected).** `loop-set` with no spec argument (or an empty string) exits
  non-zero with a clear usage error (mirrors `write`'s existing empty-message guard,
  `claude-sync.js:441`) and does not touch any existing row. Check: `loop-set ""` after a prior
  successful `loop-set "10m /foo"` — non-zero exit, DB row unchanged from before the empty call.
- **AC-4 (`loop-clear` is per-identity, self-only, and a graceful no-op when nothing is set).**
  `loop-clear` with an active permit deletes only the caller's own row. Calling it when the
  caller has no row is a clean, non-erroring "nothing to clear" message (mirrors `release`'s
  "No claim found for: <path>" pattern, `claude-sync.js:473`), never a crash or a non-zero exit
  for the empty case. Check: `loop-clear` with no prior `loop-set` — exit 0, "nothing to clear"-
  shaped stdout; `loop-set` then `loop-clear` — row genuinely deleted (`SELECT` returns zero
  rows), confirmed via direct DB query, not just stdout.
- **AC-5 (`loop-show` reads back the caller's own state, never crashes on absence).** `loop-show`
  with a configured row prints the spec, its age since `set_ms` (formatted via the existing
  `fmtMs` helper style, `claude-sync.js:142-145`), the age since `last_armed_ms` if non-null
  (else "never armed"), and `armed_count`. With no row, it prints "no standing loop configured"
  and exits 0 — never throws on a missing row. Check: two passing tests, configured and
  unconfigured, asserting the respective exact output shapes and a 0 exit code in both cases.

### B. Auto-arm at SessionStart (the load-bearing behaviour)

- **AC-6 (a fresh, non-stale spec produces a mandatory `/loop` instruction).** After a
  successful checkin resolves a final identity name inside `claude-startup.js`'s
  `printSessionSummary` (`claude-startup.js:88-117`), if that identity has a `sync_loop_config`
  row whose staleness clock (AC-8) has not elapsed, the printed output contains a distinct,
  unambiguous instruction directing the agent to invoke `/loop <exact stored spec>` as one of
  its first actions this session — placed inside (or immediately alongside) the existing
  "MANDATORY STARTUP SEQUENCE — DO ALL OF THESE BEFORE YOUR FIRST RESPONSE" numbered block
  (`claude-startup.js:110-116`), not as a separate, skippable aside. Check: an integration test
  seeds a `sync_loop_config` row for the identity the test's checkin will resolve to, invokes
  the startup path (either the real hook script via subprocess, or `emitSuccess`/
  `printSessionSummary`'s exported entry points, `claude-startup.js:285`), and asserts stdout
  contains the literal string `/loop <the exact stored spec text>` positioned inside the
  mandatory-sequence block (line-range or marker-relative assertion, not "found anywhere in
  50KB of output"). **False-pass warning:** a check that only greps for the substring "loop"
  anywhere in stdout would pass an implementation that mentions loops in unrelated prose without
  ever telling the agent to actually run `/loop` with the stored spec — the assertion must
  anchor to the exact spec text inside the mandatory block, per this AC's own wording.
- **AC-7 (no spec configured — output is unchanged from today, no phantom messaging).** An
  identity with no `sync_loop_config` row produces startup output **byte-identical** to the
  current, pre-FEAT-070 behaviour for that identity (regression guard: the existing test suite
  for `claude-startup.js`'s success-path output, whatever it is at build time, must pass
  unmodified). Check: existing `printSessionSummary`/`emitSuccess` tests continue to pass
  unmodified; a new test explicitly asserts no `/loop` or loop-config-shaped text appears
  anywhere in the output for an identity with zero `sync_loop_config` rows.
- **AC-8 (staleness gate — measured from the most recent of `set_ms`/`last_armed_ms`, not
  origin date).** A spec is stale when `now - MAX(set_ms, COALESCE(last_armed_ms, 0)) >
  LOOP_STALE_MS`. A stale spec is **not** auto-armed (AC-6's instruction is withheld); instead a
  distinctly-labelled "STALE STANDING LOOP — NOT auto-armed" block is printed, naming the
  identity, the stored spec text, its age, and the exact `loop-clear`/`loop-set` commands to
  resolve it (cancel, or explicitly refresh). A spec that has been re-armed recently (AC-9) never
  goes stale purely because `set_ms` itself is old. Check: fixture A — a row with `set_ms` and
  `last_armed_ms` both older than `LOOP_STALE_MS` — asserts the mandatory `/loop` instruction is
  **absent** and the stale-warning block is present, naming the correct identity/spec/age.
  Fixture B — a row with an old `set_ms` but a `last_armed_ms` inside the staleness window
  (simulating months of faithful continuous use) — asserts the mandatory `/loop` instruction
  **is** present and no stale-warning appears, proving the "measured from the more recent
  timestamp" rule and not a naive `set_ms`-only check.
- **AC-9 (successful auto-arm updates `last_armed_ms`/`armed_count`, exactly once per
  SessionStart).** Printing AC-6's instruction updates that identity's row: `last_armed_ms =
  now`, `armed_count = armed_count + 1` — in the same DB transaction/call sequence the checkin
  already performs, not a separate untracked side effect. This update happens **exactly once**
  per `SessionStart` invocation even when the underlying checkin internally retries or falls
  back (e.g. the requested-identity-occupied → `--any` fallback path,
  `claude-startup.js:204-223`) — only the identity that actually ends up resolved and printed-to
  gets the increment; the originally-requested-but-not-obtained identity's row is untouched.
  Check: a test simulates the fallback path (requested identity's permit pre-occupied by another
  live session, forcing `--any` to assign a different identity) with both identities holding a
  `sync_loop_config` row; asserts the originally-requested identity's `armed_count` is unchanged
  and the actually-resolved identity's `armed_count` increased by exactly `1`, `last_armed_ms`
  updated to a recent timestamp.
- **AC-10 (reboot survival — the item's literal premise).** (a) `sync_loop_config` rows are
  never gated by `BOOT_ID` (already covered structurally by AC-1's "no `boot_id` column", re-
  asserted here behaviourally); (b) a spec set under one simulated `BOOT_ID` still produces
  AC-6's mandatory instruction when the startup path runs again under a **different** `BOOT_ID`
  (simulating an actual reboot, which — per `slotState`, `claude-sync.js:134-140` — voids every
  `sync_permits` reservation) — proving the standing-loop configuration is genuinely
  reboot-durable while the running loop process itself is not (that is the "survive reboot by
  re-arming at launch, not by persisting" distinction the item's own title draws). Check: seed a
  fresh `sync_loop_config` row, run the startup path once under `BOOT_ID = A` (confirm arm
  instruction appears, per AC-6), then force a different `BOOT_ID` value (mirroring how
  `slotState` itself is tested against boot-id mismatch, if such a test fixture exists — else
  construct one directly) and re-run the startup path for the same identity; assert the
  mandatory `/loop` instruction still appears despite the boot-id change that would have voided
  a `sync_permits` row.

### C. Oversight, inspection, safety

- **AC-11 (oversight visibility — `read --full` lists all three identities' standing loops).**
  `node claude-sync.js read` (`cmdStatus(db, { full: true })`, `claude-sync.js:422-437`) gains a
  section listing each of `Bill`/`Bob`/`Ben`'s configured spec (or "none"), age, and
  `armed_count`, so the lead's oversight sweep can see every worker window's standing loop
  without personally holding those identities — directly serving `CLAUDE.md`'s "the lead sweeps
  and commits in batches on a self-paced `/loop`" oversight role. Check: seed loop-config rows
  for `Bob` and `Ben` (leave `Bill` unconfigured), run `node claude-sync.js read`, assert stdout
  lists all three names with `Bob`/`Ben` showing their real spec text and `Bill` showing "none".
- **AC-12 (`loop-clear` is strictly self-scoped — no cross-identity clear in v1).** An identity
  can only ever clear its own row via `loop-clear`; there is no flag or mode in this item that
  lets identity X delete identity Y's standing-loop row (an admin-force variant, if ever needed,
  is a separate, escalated decision — see Escalations). Check: check in as `Bill`, seed a
  `Bob`-owned row, run `loop-clear` as `Bill`, and assert `Bob`'s row is **unchanged** (only
  `Bill`'s own — absent — row was the target).

### D. Documentation

- **AC-13.** `claude-sync.js`'s header comment gains `loop-set "<spec>"`, `loop-clear`,
  `loop-show` to its `Commands:` list. `claude-startup.js`'s auto-arm print site carries a
  comment explaining, in the same spirit as this file's own "What this item can and cannot
  literally do" section, that the hook can only ever *instruct* the agent to run `/loop` — it
  cannot invoke the slash command itself — and citing FEAT-070 by code. Check: `grep -n
  "loop-set\|loop-clear\|loop-show" claude-sync.js` finds the header documentation; `grep -n
  "FEAT-070" claude-startup.js` finds the explanatory comment at the auto-arm site.

## Out of scope

- Any mechanism that makes the hook literally invoke `/loop` itself rather than instructing the
  agent to — not buildable as specified (see the binding constraint section above); if the
  harness ever exposes a programmatic scheduling hook callable from outside the agent turn,
  that is new tooling capability, a different and larger item, not a variant of this one.
- Enforcing that the agent actually obeys the printed instruction — not code-checkable from a
  script; the existing Vestige-search mandatory-instruction step has the exact same trust
  boundary today and is not treated as a defect, so this item does not hold itself to a higher
  standard than the mechanism it's modeled on.
- An admin-force cross-identity `loop-clear`/`loop-set` (Bill clearing Bob's loop directly) —
  escalated, not built (AC-12 is the explicit boundary).
- Any change to `/loop`'s own interval-parsing or execution semantics — this item only stores
  and re-surfaces the exact spec text a human/agent already typed once; it never validates or
  interprets the spec's contents.
- Multiple standing loops per identity — `sync_loop_config` is one row per name; if a future
  need for concurrent standing loops per identity emerges, that is new scope.

## Escalations

- **Module key (GR#20, for Bill/`/register-guid`, not decided here).** This file proposes
  `tool.looparm` for the schema/command additions living inside `claude-sync.js` plus the
  `claude-startup.js` auto-arm print logic, parallel to `tool.syncmsg.md`'s `tool.syncmsg`
  proposal for the same file's message-delivery addition — logged below as an assumption.
- **`LOOP_STALE_MS` default value.** Proposed default 72 hours (three days of zero re-arm before
  a standing loop is treated as abandoned) is this BA's placeholder, not a considered number —
  matching the project's existing pattern of concrete-but-adjustable tuning constants
  (`TTL_MS`/`RESERVE_MS`) rather than a hardcoded magic number with no escape hatch
  (`METRO_LOOP_STALE_MS` env override, AC-8's design). The *right* default trades off "don't
  nag about a loop someone genuinely means to resume after a long weekend" against "don't
  silently keep re-instructing agents to arm a loop everyone actually forgot about" — a judgment
  call for Bill/Aaron, not this BA.
- **Admin-force cross-identity clear.** Not built (AC-12/Out of scope) — flagged in case
  oversight sweeps find they routinely need to cancel another identity's abandoned standing loop
  rather than waiting for `LOOP_STALE_MS` to lapse naturally.

## Assumptions logged (process v1.7)

- **ASM-473 (module key proposal, `tool.looparm`, and `LOOP_STALE_MS` default left as a
  placeholder)** — mirroring `tool.syncmsg.md`'s and `feat.helper.md`'s ASM-454 pattern;
  escalated to Bill for `/register-guid`, not decided unilaterally by this BA. The 72h staleness
  default (see Escalations) is flagged in the same item so a future reader does not mistake the
  proposed number for a settled design decision.

- **AC-14 (ASM-473 — key exists as this file; `code.json` registration is ABSENT).** The
  module key `tool.looparm` exists as this acceptance filename and as the proposed GR#20
  key. `code.json` does **not** contain `"key": "tool.looparm"`. `LOOP_STALE_MS` default 72h
  remains a placeholder (`METRO_LOOP_STALE_MS` override, AC-8), not a settled design
  number. Check: `grep -n "\"key\": \"tool.looparm\"" code.json` returns no matches;
  `Test-Path docs/planning/acceptance/tool.looparm.md` is true; `grep -n "72" claude-sync.js`
  (or the `LOOP_STALE_MS` default expression) is the placeholder, not an Aaron-signed
  constant. **False-pass:** grepping `looparm` in this markdown file would always pass.
  The registration check is against `code.json`.
