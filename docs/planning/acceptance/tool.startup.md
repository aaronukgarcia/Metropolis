BOW code: FEAT-108

# Acceptance criteria — claude-startup SessionStart hook (tool.startup, FEAT-108)

**BOW code:** FEAT-108 (P1, open) — "SessionStart hook: auto checkin + identity + startup summary (BOW/Vestige/git sync)".
**Spec refs:** M0-ENG §5 (hooks). Dev-tooling, not engine/UI work — the specification is the committed behaviour of `claude-startup.js` and the `claude-sync.js` checkin it drives, both read in full. `dev-team-process.md` v1.9: every AC's check must be able to fail.
**code.json:** `tool.startup` — GUID `7220138c-1822-4cc1-9e5e-b17714e2f42b`, already registered.
**Date:** 2026-08-16
**Status:** retrospective — documents the contract of **committed** code. AC-15/AC-16 are the exception: they document the open BUG-238 failure mode and are **fix targets expected to FAIL against current code** (see Escalations).
**Package under test:** `claude-startup.js` (root tooling, the SessionStart hook), driven checkin output from `claude-sync.js`, plus `claude-author-identity.js` (FEAT-038 git-identity check) and `claude-committhook-install.js` (FEAT-045).
**Assumptions:** ASM-744 (BUG-238 per-window identity is the target), ASM-746 (branch coverage is partial), ASM-747 (fail-loud identity / fail-open session posture).

## What the hook does (binding on every AC below)

`claude-startup.js` is the SessionStart hook. It resolves the window's session id from the hook's stdin JSON (`session_id`), passes it to `claude-sync.js checkin` as `CLAUDE_CODE_SESSION_ID` so the permit is mapped to **this** window (per-window identity), then turns the checkin result into the mandatory startup sequence for the agent. It never shells out with a template string — all checkins go through `execFileSync('node', args, …)` with an argv array (BUG-124). The hook's four terminal outcomes are: **success** (identity confirmed), **fallback** (requested slot taken → next free), **deferred/hard-block** (all slots full), and **technical failure**. In every non-success outcome the hook says "you have no (confirmed) identity, do not prefix" — it never silently adopts a stale or guessed name.

## Acceptance criteria

### A. Window-id resolution and identity assignment

- **AC-1 (window id from hook stdin, env fallback).** The hook reads the window's `session_id` from the hook stdin JSON; when absent, it falls back to `CLAUDE_CODE_SESSION_ID` env; an unparseable/empty stdin falls back cleanly rather than throwing. Verify: read the entry block (`if (require.main === module)`); assert the `session_id` → `windowId` assignment and the try/catch around `JSON.parse`.
- **AC-2 (per-window checkin, not a shared identity).** Every `checkin` this hook runs is invoked with `CLAUDE_CODE_SESSION_ID = windowId`, so the permit lands on this window. Verify: read `tryCheckin`'s `env` spread; assert `CLAUDE_CODE_SESSION_ID` is forced to `windowId`.
- **AC-3 (requested identity → `checkin --name`).** When `CLAUDE_IDENTITY` is set, the hook first runs `checkin --name <id>`; on success it emits that identity. When unset, it runs a plain `checkin` (first-come, first-served). Verify: read `runStartup`'s two branches.
- **AC-4 (`parseName` accepts only the three valid names).** `parseName` matches `YOU ARE:\s*(\w[\w-]*)` and lowercases, but returns `null` unless the result is one of `bob|bill|ben` — checkin output is never trusted beyond that allowlist. Verify: unit-call `parseName` (or assert the `VALID_NAMES` gate) with `YOU ARE: Bill`, `YOU ARE: Mallory`, and empty output.

### B. Successful checkin → startup summary

- **AC-5 (success writes the identity file and prints the summary).** `emitSuccess(name, output)` writes `.claude/.identity` and prints, in order: `IDENTITY: <name>`, `PREFIX EVERY RESPONSE with "<name>". No exceptions.`, `HOOKS: ACTIVE.`, the commit-msg hook install/survival line, then the relayed startup summary (everything from `── METROPOLIS STARTUP SUMMARY ──` to end of checkin stdout, verbatim). Verify: capture `printSessionSummary` output against a throwaway repo (the test-only third arg) and assert each line.
- **AC-6 (missing summary is loud, not silent).** If checkin output contains no `SUMMARY_MARKER`, the hook prints `WARNING: checkin returned no startup summary (BOW/Vestige/git state unknown).` plus the manual `node claude-bow.js startup-summary` command. Verify: `printSessionSummary('bob', 'no marker here', tmpRepo)` → warning present.
- **AC-7 (git identity line, printed unconditionally — FEAT-038).** The summary always ends with the git-identity line, which is corroborated by trunk history or `CLAUDE_AUTHOR_GUARD_EXTRA_IDENTITIES` — never self-corroborated by the configured value itself. Verify: `claude-startup.test.js`'s `checkGitIdentity`/`gitIdentityLine` cases (ok / not-ok / not-configured / internal-error).
- **AC-8 (mandatory startup sequence, numbered).** The summary prints the numbered `MANDATORY STARTUP SEQUENCE — DO ALL OF THESE BEFORE YOUR FIRST RESPONSE:` block: step 1 Vestige search, step 2 read CLAUDE.md, step 3 `node claude-sync.js read`, step 4 confirm identity/hooks/BOW/Vestige/git-sync/git-identity, plus step 5 (the standing-loop arm) when present, and the "surface git NOT SYNCED / Vestige / GIT IDENTITY immediately" and "do not skip step 1" lines. Verify: capture `printSessionSummary` output and assert steps 1-4 and the two closing lines are present in order.

### C. Requested slot taken → fallback

- **AC-9 (occupied/reserved falls back to the next free slot).** When the requested slot is live-held or reserved for another window (`SLOT IS OCCUPIED` / `SLOT IS RESERVED` / `name-occupied` / `name-reserved`), the hook warns, runs `checkin --any`, and on success prints `You have been assigned "<assigned>" instead.` with the assigned identity. Verify: `isNameOccupied` detection plus the fallback branch in `runStartup`.
- **AC-10 (all-full detection strings).** `isAllFull` returns true on `all-full` / `ALL SLOTS FULL` / `ALL PERMITS OCCUPIED`; `isNameOccupied` on the occupied/reserved strings. These match `claude-sync.js`'s emitted stderr verbatim (single producer). Verify: grep both files for each literal.

### D. All slots full → deferred vs hard block

- **AC-11 (short TTLs → deferred checkin).** On all-full with a max `expires in Xm Ys` ≤ 3 minutes, the hook prints `CHECKIN DEFERRED …`, `YOU HAVE NO IDENTITY YET. Do not prefix responses until checkin succeeds.`, and the exact first-action `node claude-sync.js checkin --any` plus the human-ok force-evict fallback. Verify: feed a synthetic all-full stderr with short TTLs to `handleAllFull` and assert the deferred block; assert `parseMaxTTLMs` returns the max of the listed TTLs.
- **AC-12 (long TTLs → hard block).** On all-full with TTLs > 3 minutes the hook prints `ERROR: ALL PERMIT SLOTS ARE FULL …`, `YOU HAVE NO IDENTITY. DO NOT PREFIX RESPONSES WITH ANY NAME.`, and instructs the agent to tell the user and run `node claude-sync.js read`. Verify: `handleAllFull` with a long-TTL stderr.

### E. Force-evict blocked

- **AC-13 (FORCE-EVICT BLOCKED is surfaced, not retried silently).** If the requested-name checkin fails with `FORCE-EVICT BLOCKED`, the hook prints that human authorisation is required, forbids the `<name>` prefix, and gives the human-ok command. Verify: read the `FORCE-EVICT BLOCKED` branch of `runStartup`.

### F. Technical failure

- **AC-14 (any other failure → technical-failure block).** A checkin failure that is not occupied/reserved/all-full/force-evict prints `ERROR: claude-sync.js checkin failed with a technical error.`, `YOU HAVE NO CONFIRMED IDENTITY.`, and the manual-checkin instruction — it never falls back to a prior or guessed identity. Verify: `emitTechnicalFailure` golden string.

### G. BUG-238 — no silent reassignment / per-window identity

- **AC-15 (fix target — startup must never leave a stale or wrong identity).** A session that already holds an identity and re-runs startup/checkin must **re-confirm that same identity**, never be silently reassigned to a different name because the requested name is held by another live window. The current per-window guard lives in `claude-sync.js`'s `findMine` renew-of-self and depends on the hook-supplied session id being present; a **manual** checkin without it resolves against the shared last-writer-wins identity file — the BUG-238 failure ("Bob's window, running as Bill all session, was silently reassigned to Bob"). **Expected FAIL against current committed code** (ASM-744).
- **AC-16 (fix target — per-window identity is the contract).** The window's identity is keyed to its own session id, never to a shared last-writer-wins file; the shared `.claude/.identity` file is a statusline fallback, not the authority for who holds a slot. **Expected FAIL against current committed code** (ASM-744).

### H. Fail posture

- **AC-17 (fail-open for session start: the hook never wedges).** No branch of `runStartup` throws out of the hook — every terminal outcome emits a message and returns; stdin parse failure falls back to env; the `setTimeout(start, 3000).unref()` ensures a stuck pipe never blocks the session from starting; the `timeout: 15000` on `execFileSync` bounds each checkin. Verify: read the entry block and `tryCheckin`; assert the 3s unref timer and the timeout.
- **AC-18 (fail-loud for identity: no silent adoption).** Every non-success outcome (deferred, all-full, force-evict-blocked, technical) forbids prefixing and tells the user, rather than silently carrying a name the window may not hold (ASM-747). Verify: grep the four emit functions for the `YOU HAVE NO IDENTITY` / `NO CONFIRMED IDENTITY` / `DO NOT PREFIX` wording.

### I. Test coverage

- **AC-19 (`claude-startup.test.js` runs green).** `node --test claude-startup.test.js` passes. All fixtures use throwaway repos under the OS temp dir (never this repo), removed in `finally`. Verify: run the suite.
- **AC-20 (require is side-effect-free; run as a script is unchanged).** Because entry is gated behind `require.main === module`, `require('./claude-startup.js')` in a test performs no real checkin and no identity-file write; running `node claude-startup.js` still runs the hook. Verify: read the entry block; the test file already `require`s it safely.
- **AC-21 (coverage-gap note — branch logic is not directly unit-tested).** `claude-startup.test.js` covers FEAT-038 (git identity) and `printSessionSummary` output only. The `runStartup` branches — fallback (`--any`), deferred, all-full, force-evict-blocked, technical-failure — are **not** directly unit-tested; they are exercised only indirectly through `claude-sync.test.js`'s subprocess checkin fixtures or live manual runs. This is a documented gap, not a pass (ASM-746). Verify: grep `claude-startup.test.js` for `emitDeferredCheckin|emitAllFull|emitTechnicalFailure|isNameOccupied|runStartup` → no behavioural tests.

## Out of scope

- The permit/slot/identity **state machine** itself — `docs/planning/acceptance/tool.sync.md` (FEAT-107).
- Git-identity derivation (`claude-author-identity.js`) — that module's own contract (FEAT-038 surfaced here, not re-derived).
- Standing-loop auto-arm (FEAT-070) — `docs/planning/acceptance/tool.looparm.md`; the hook only *relays* the `── STANDING LOOP ──` block it lifts out of checkin stdout.
- Message delivery (FEAT-069) — `docs/planning/acceptance/tool.syncmsg.md`.

## Escalations (for Bill)

1. **BUG-238 fix scope.** AC-15 and AC-16 are written against the open BUG-238 and are expected to FAIL against current code; they are the acceptance surface for the fix round, not a current pass gate. Confirm this framing before the next wave sweeps.
2. **Coverage gap.** AC-21 records that the `runStartup` branch logic has no direct unit tests. Recommend a branch-level test (drive `runStartup` with a stubbed `tryCheckin`) or accept the gap as known debt.
