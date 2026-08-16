BOW code: FEAT-107

# Acceptance criteria — claude-sync session coordination (tool.sync, FEAT-107)

**BOW code:** FEAT-107 (P1, open) — "Session coordination harness (metro MariaDB): permits, file claims, dispatch events, wake recovery".
**Spec refs:** M0-ENG §5 (hooks); session coordination. This is dev-tooling, not engine/UI work — the specification is the committed behaviour of `claude-sync.js` (root tooling) and the two hook scripts that consume it (`claude-startup.js`, `claude-ping-check.js`), all read in full before drafting. `dev-team-process.md` v1.9: every AC's check must be able to fail.
**code.json:** `tool.sync` — GUID `eae1b5fc-9fc9-46fa-af15-5333c5db21f8`, already registered (no module-key decision needed here).
**Date:** 2026-08-16
**Status:** retrospective — documents the contract of **committed** code. AC-18/AC-19 are the exception: they document the open BUG-238 failure mode and are **fix targets expected to FAIL against current code**, not pass-today criteria (see Escalations).
**Package under test:** `claude-sync.js` (root tooling; the permit/slot/identity state machine), `claude-ping-check.js` (the PostToolUse renewal hook that drives `renew --auto`), and the string contract consumed by `claude-startup.js`.
**Assumptions:** ASM-732 (BUG-238 ACs are fix targets), ASM-734 (core state machine has no dedicated suite), ASM-741 (FEAT-069/070/076 out of scope).

## What the permit contract is (binding on every AC below)

`claude-sync.js` is a DHCP-style permit system over the project's own `metro` MariaDB. There are exactly three named slots — **Bill, Bob, Ben** (`NAMES`). Each slot row (`sync_permits`) is keyed by name and, while held, carries a per-window `window_id` (the Claude window's session UUID, `CLAUDE_CODE_SESSION_ID` → `CLAUDE_SESSION_ID` fallback) and a **server-minted** `session_id` (`crypto.randomUUID()`, disclosed once in the checkin's own stdout as `Session: <uuid>`). The window id maps a permit to the physical window so two windows never cross-renew; the session id is the *secret* proof-of-possession used by the standing-loop commands (see ASM-741 — that contract is documented in `tool.looparm.md`, not here).

Three constants define the TTL/reservation contract:
- `TTL_MS = 5 * 60 * 1000` — permit lifetime.
- `RENEW_THRESHOLD_MS = 3.5 * 60 * 1000` — `renew --auto` only extends below this remaining.
- `RESERVE_MS = 30 * 60 * 1000` — an expired slot stays reserved for its idle window.

## Acceptance criteria

### A. Permit TTL and renewal

- **AC-1 (5-minute TTL, set at acquire).** A fresh `checkin` writes `expires_ms = acquired_ms + 300000` (plus sub-second clock skew, at most a few ms) and `released = 0`, with `heartbeat_ms` initialised to `acquired_ms`. Verify: check in against a scratch DB, then `SELECT (expires_ms - acquired_ms) FROM sync_permits WHERE name = '<Name>'` → `300000`.
- **AC-2 (`renew --auto` threshold = 3.5 min).** `renew --auto` with more than 3.5 minutes remaining updates `heartbeat_ms` only, prints nothing, exits 0, and **does not** touch `expires_ms`. With less than 3.5 minutes remaining it extends `expires_ms = now + 300000` and still prints nothing (heartbeat/silent). Verify: two subprocess runs of `node claude-sync.js renew --auto` on either side of the 3.5-minute boundary, asserting `expires_ms` unchanged in the first and advanced in the second, and empty stdout in both. **False-pass warning:** a check that only asserts "exit 0, no output" would pass a bug that *also* skipped the extension — the check must assert the `expires_ms` column change on the far side.
- **AC-3 (`renew` without `--auto` is always loud).** `renew` (no flag) always extends and prints `<Name> renewed — expires in 5m 0s.` Verify: subprocess run asserts the exact golden string.
- **AC-4 (TTL/RESERVE constants are hard module constants, not env-tainted).** `TTL_MS`, `RENEW_THRESHOLD_MS`, `RESERVE_MS` are module-level `const` expressions (`5 * 60 * 1000`, `3.5 * 60 * 1000`, `30 * 60 * 1000`) with no `process.env` dependency; only `LOOP_STALE_MS` reads the environment (and that is FEAT-070's, out of scope). Verify: read the top constants block and confirm `process.env` appears only on the `LOOP_STALE_MS` line, never on the three TTL/RESERVE constants.

### B. Slot states, reservation, eviction

- **AC-5 (the three-state `slotState` mapping).** `slotState(row, now)` returns `ACTIVE` when `expires_ms > now` and `boot_id` matches; `RESERVED` when expired `< RESERVE_MS` ago and boot matches; `FREE` otherwise (released, no session, boot mismatch, or expired beyond the reserve window). Verify: unit-level — call the exported `slotState` with fixture rows (it is exported in `module.exports`).
- **AC-6 (reboot voids permits AND reservations).** `BOOT_ID` is derived from boot time rounded to 10s; a `boot_id` mismatch makes a slot `FREE` immediately, so a stale holder across a reboot is never honoured, and its reservation never survives the reboot. Verify: a fixture row with `boot_id = 'dead-boot'` and an in-the-future `expires_ms` still evaluates `FREE`.
- **AC-7 (eviction is human-only, fail-closed).** `checkin --name X --force` (without `--human-ok`) exits non-zero with `FORCE-EVICT BLOCKED: evicting a live holder requires the human-only --human-ok flag.` and does **not** change the row. `checkin --name X --force --human-ok` evicts, logs `X FORCE-EVICTED previous holder (human-authorised) and checked in`, and succeeds. Verify: two subprocess runs against a live holder, asserting exit code, the exact stderr string, and (for the second) the row's new `session_id`.
- **AC-8 (a reserved slot is held for its idle window).** A window requesting a name that is `RESERVED` for a *different* `window_id` is rejected with `SLOT IS RESERVED` and told to take the next free slot; the idle window itself (matching `window_id`) reclaims it as a normal acquire. Verify: check in window A as Bill, let the permit expire (or fake `expires_ms` into the past within 30 min), then check in window B as Bill → `SLOT IS RESERVED`; re-check-in window A as Bill → succeeds.
- **AC-9 (`checkout` releases the permit and its claims).** `checkout` sets `released = 1` for the window's own permit and deletes that name's rows from `sync_file_claims`; `checkout --force <Name>` evicts a specific holder. Verify: check in, claim a path, checkout, then `SELECT` both tables — permit released, claims gone.
- **AC-10 (`gc` reaps beyond the reserve window).** `gc` releases any permit with `released = 0 AND (expires_ms < now - RESERVE_MS OR boot_id <> BOOT_ID)` and deletes `sync_file_claims` whose name has no live permit. Verify: seed an expired-beyond-reserve row and an orphan claim, run `gc`, assert both gone and a fresh ACTIVE permit untouched.

### C. Identity and wake recovery

- **AC-11 (slot-name validation is case-insensitive, error is registry-shaped).** `checkin --name` resolves case-insensitively against `NAMES`; an unknown name exits non-zero with `Unknown slot name "<value>". Valid: Bill, Bob, Ben`. Verify: `checkin --name Nobody` → non-zero, exact string.
- **AC-12 (first-come, first-served order is Bill → Bob → Ben).** A plain `checkin` with no `--name` and no `CLAUDE_IDENTITY` acquires the first `FREE` slot in that order; `--any` ignores a preferred `CLAUDE_IDENTITY`. Verify: release all slots, check in three windows with no preference, assert the acquisition order.
- **AC-13 (renew-of-self: an existing permit is re-confirmed, not reassigned).** A window that already holds an ACTIVE permit, when it checks in, renews **that same name** — the requested `--name` is not used to move it to a different slot. Verify: window A checks in as Bill, then re-runs `checkin --name Bob` from the same window → output still `YOU ARE: Bill`.
- **AC-14 (wake recovery on `renew`).** A window whose permit expired while idle re-acquires the same name on the next `renew --auto` (logged `wake recovery — re-acquired after idle expiry`); if that name is now gone or held elsewhere, it is assigned the next free slot with the `⚠ IDENTITY CHANGED` warning; if no slot is free, it prints `You hold NO identity — do not prefix responses until a checkin succeeds.` and holds none. Verify: each branch via fixture windows against a scratch DB.
- **AC-15 (the checkin success contract string).** A successful checkin prints, in order: `YOU ARE: <Name>`, `Session: <uuid>`, `Permit TTL: 5 minutes — auto-renewed by the PostToolUse hook while you work.`, and `Prefix every response with "<name lowercased>".`. Verify: assert the golden block on a fresh checkin's stdout.
- **AC-16 (`--session` fallback matches by the DB-issued secret).** With `--session <uuid>`, `renew`/`read`/`status` resolve the permit by `row.session_id === <uuid>` even when `CLAUDE_CODE_SESSION_ID` is unset (plain-terminal usage); a non-matching or stale value resolves to nothing. Verify: subprocess run without the env var but with a captured real `Session:` id succeeds; with a bogus id it does not.

### D. BUG-238 — checkin must not silently reassign an active window

- **AC-17 (fix target — no silent reassignment).** A checkin from a window that already holds an identity must **preserve or re-confirm that identity**, never silently reassign it to a different name because the requested name happens to be held by another live window. The current guard is `findMine`'s renew-of-self (AC-13), which only fires when `WINDOW_ID` is reliably present; a **manual** checkin that runs without the hook-supplied session id can resolve against the shared last-writer-wins identity file instead of the window's own DB permit, which is exactly the BUG-238 failure ("Bob's window, running as Bill all session, was silently reassigned to Bob"). **Expected FAIL against current committed code** (ASM-732).
- **AC-18 (fix target — identity is per-window, not a shared file).** The authority for which window holds which name is the `sync_permits` row keyed to that window's session id; the shared `.claude/.identity` file is a statusline fallback only and must never be the basis for a slot assignment or reassignment. **Expected FAIL against current committed code** (ASM-744).

### E. Fail posture (open vs closed)

- **AC-19 (fail-open: display-only output never costs a checkin).** Inside `printSuccess`, a failure of the startup summary (`claude-bow.js printStartupSummary`), the utilisation line, or the standing-loop status is caught and printed as `(<thing> unavailable: <reason>)` — the checkin still succeeds and returns `YOU ARE:`. Verify: point `METRO_DB_NAME` at a DB where one of the summary's tables is missing (or stub `printStartupSummary` to throw) and assert the checkin still emits `YOU ARE:` and exits 0.
- **AC-20 (fail-closed: identity-critical gates exit non-zero).** Unknown name (AC-11), force-evict without `--human-ok` (AC-7), permit-required commands (`message`, `claim`, `loop-set/clear/show`), and a dangling value-flag (`--name`/`--session`/`--to`/`--body-file`/`--hours`/`--target`/`--set-target` with no value or followed by another flag) all exit non-zero with a clear error — none silently degrades into "any slot" or a broadcast. Verify: the Culvert-regression suite in `claude-sync.test.js` plus a manual `--name` dangling run.
- **AC-21 (fail-open: identity-file write is best-effort).** `writeIdentityFiles` wraps its `.claude` mkdir/writes in try/catch — a filesystem failure never fails a checkin (the DB permit is the authority, the file is a statusline nicety). Verify: read the function; assert no throw path.

### F. CLI-surface compatibility (the hook contract)

- **AC-22 (the strings the hooks parse are stable).** The literal strings `YOU ARE: <Name>`, `SLOT IS OCCUPIED`, `SLOT IS RESERVED`, `ALL SLOTS FULL`, `FORCE-EVICT BLOCKED`, and `expires in Xm Ys` are produced by this file and consumed by `claude-startup.js` (`parseName`, `isNameOccupied`, `isAllFull`, `parseMaxTTLMs`, the `FORCE-EVICT BLOCKED` branch) and `claude-ping-check.js` (runs `renew --auto`). Verify: grep both hook scripts for each literal and confirm a single producer in `claude-sync.js` (GR#3 — no second drifting copy).
- **AC-23 (schema idempotency, all nine tables).** `ensureSchema` auto-runs before every command and creates `sync_permits`, `sync_activity`, `sync_file_claims`, `sync_window_map`, `sync_messages`, `sync_read_cursor`, `sync_dispatch_events`, `sync_loop_config`, `project_meta` — all `CREATE TABLE IF NOT EXISTS`, with `sync_permits` and `sync_read_cursor` pre-seeded with exactly the three `NAMES` rows. A second run against an already-migrated DB is a byte-for-byte no-op. Verify: `node claude-sync.js init` twice against a throwaway DB exits 0 both times and the row counts are unchanged by the second run.

### G. Test coverage

- **AC-24 (`claude-sync.test.js` runs green, isolated from the live DB).** `node --test claude-sync.test.js` passes; the suite runs against a scratch DB (`metro_test_syncmsg_<pid>`), never the real `metro` database, and snapshots/restores `.claude/.identity` around its synthetic acquires. Verify: run the suite; grep the file for `METRO_DB_TEST_NAME`/`DROP DATABASE`.
- **AC-25 (coverage-gap note — the core state machine has no dedicated suite).** `claude-sync.test.js` is scoped to FEAT-069 (message delivery) and FEAT-070 (standing-loop) — its `checkin`/`renew --auto` calls are fixtures for those ACs, not a test of the tool.sync contract itself. There is currently **no** dedicated test exercising TTL expiry, `RESERVE` transition, `--force --human-ok` eviction, `checkout`, `gc`, or wake-recovery IDENTITY CHANGED directly. This is a documented gap, not a pass (ASM-734). Verify: grep `claude-sync.test.js` for `checkout|--force|human-ok|gc|RESERVE|slotState` → zero hits.

## Out of scope

- **FEAT-069** message delivery and the read cursor — `docs/planning/acceptance/tool.syncmsg.md`.
- **FEAT-070** standing-loop auto-arm and the session-secret identity boundary — `docs/planning/acceptance/tool.looparm.md`.
- **FEAT-076** dispatch/stop logging and the `util` report — `docs/planning/acceptance/tool.agentlog.md`.
- **FEAT-108** the SessionStart hook's checkin-driving logic — `docs/planning/acceptance/tool.startup.md`.
- NO-TOUCH file claims (`claim`/`release`) are exercised only as part of the checkout/gc path above; their full contract is not re-documented here.

## Escalations (for Bill)

1. **BUG-238 fix scope.** AC-17 and AC-18 are written against the open BUG-238 and are expected to FAIL against current code. They are the acceptance surface for the next fix round, not a current pass gate — a Tester should mark them "FAIL (open BUG-238)" rather than bounce committed work. Confirm this framing before the next wave sweeps.
2. **Coverage gap.** AC-25 records that the tool.sync core state machine has no dedicated test suite. Recommend a small `tool.sync`-scoped regression file (TTL/renewal-threshold/RESERVE/eviction/checkout/gc/wake-recovery) be queued, or this gap be accepted as known debt.
