BOW code: FEAT-002

# Acceptance criteria — legacy.versionguard (FEAT-002)

**BOW code:** FEAT-002
**Spec refs:** GR#2 (Version Discipline, `CLAUDE.md` line 38); M0-ENG §3 (ldflags-injected build info, `docs/METROPOLIS-MASTER-v2.1.md` line 858, "version (`git describe --tags --dirty`)... all injected via `-ldflags` at build; NEVER hand-maintained"); the BOW item's own comments (`node claude-bow.js show legacy.versionguard`) — Bill's 2026-08-08 16:03 note and Aaron's 2026-08-08 16:15 DECIDED note (quoted below).
**Date:** 2026-08-09
**Status:** active — **urgent**: `cmd/` and `internal/` already exist in the repo (confirmed: `internal/engine/`, `internal/foundation/`, `internal/protocol/`, `internal/ui/`, `cmd/metctl/`, `cmd/metropolis/` are all tracked). `claude-version-guard.js` is **currently unretargeted** and, per its own unmodified logic, requires `app/package.json`+`app/src/lib/version.ts` to be staged on every non-exempt commit — paths that do not exist in this repo. Left unfixed, this hook blocks every commit touching `cmd/`, `internal/`, or `data/` that isn't docs/tooling-exempt. This is very likely why dev slots are stalled.
**Deliverable:** modifications to the existing `claude-version-guard.js` (repo root) — not a new file.
**Standard gates:** Node.js hook script — SG-1/SG-2/SG-4/SG-7 (go build/vet/test/determinism-grep) do not apply. This item's gates are AC-1 (`node --check`), SG-5, SG-6, plus a **live-fire check**: a real, disposable commit touching `internal/` must succeed end-to-end after this fix, since that is the actual blocking symptom.

## User stories

- As **every junior developer committing to `cmd/`, `internal/`, or `data/` right now**, I need `claude-version-guard.js` to stop demanding `app/package.json`+`app/src/lib/version.ts` — paths that were retired along with `MOD-001` (cancelled app-skeleton item) — so my commits aren't blocked by a check for files this project no longer has.
- As **GR#2**, I need version discipline satisfied a different way for the Go layout: `git describe --tags --dirty` injected via `-ldflags` at build, with milestone-cut annotated tags (`v0.<milestone>.<n>`) — not a hand-maintained version file (M0-ENG §3 explicitly bans that for this stack).
- As **`tool.bow`**, I need this guard to step out of the way of BOW-ref enforcement for `cmd/`/`internal/`/`data/` commits once its own hook exists, rather than both hooks independently (and possibly inconsistently) trying to enforce the same thing.

## Scope

Retarget `claude-version-guard.js`'s existing two-file (`package.json`+`version.ts`) check: detect when the Go skeleton (`cmd/` or `internal/`) exists and, for such repos, drop that check entirely rather than blocking on files that will never exist. Root `package.json` (hook-tooling deps) remains exempt as today, unchanged.

## Acceptance criteria

### Functional

- **AC-1.** `node --check claude-version-guard.js` exits 0 after the change.
- **AC-2.** The hook detects whether the Go skeleton exists — check: `fs.existsSync(path.join(ROOT, 'cmd'))` or `fs.existsSync(path.join(ROOT, 'internal'))` (or equivalent) is present in the script, evaluated fresh on each invocation (not cached/hardcoded to a boolean at some earlier point in time, since the whole point is this state has now changed).
- **AC-3.** When the Go skeleton is detected (as it now is), a `git commit` touching `cmd/`, `internal/`, or `data/` is **no longer blocked** for missing `app/package.json`/`app/src/lib/version.ts` — those files are not required to be staged. Check: a passing test (or a real disposable commit against a throwaway file under `internal/`) asserts the hook allows the commit through without requiring the two legacy files.
- **AC-4.** The existing docs/tooling exemption patterns (`docs/`, `*.md`, `.claude/`, root `claude-*.js`, `.gitignore`, root `package.json`/`package-lock.json`) remain unchanged and still exempt those commits from any version-related requirement — a passing test asserts a docs-only commit is still allowed exactly as before this change (no regression).
- **AC-5.** The two-file check's code path (the `hasPackageJson`/`hasVersionTs`/version-diff-verification logic) is either removed entirely or left in place but made **unreachable** once the Go-skeleton branch (AC-2/AC-3) is taken — check: source reading confirms no code path can reach the old "🛑 GOLDEN RULE #2 VIOLATION: Version bump required" deny message once `cmd/`/`internal/` exist, since that message now refers to files this project doesn't have and would be actively misleading if ever shown.
- **AC-6.** GR#2 is documented (in the hook's own header comment) as satisfied for the Go layout via `git describe --tags --dirty` + `-ldflags` injection at build time and annotated milestone tags (`v0.<milestone>.<n>`) — this hook does **not** attempt to verify a tag was bumped per-commit (that is a release/tag-time concern, not a commit-time one, per Aaron's DECIDED note); it only needs to stop demanding the retired two-file pattern.
- **AC-7.** This item does **not** implement BOW-`[mkey]`-ref enforcement itself — that is `tool.bow`'s (`MOD-007`) dedicated hook. Check: the diff for this item touches only `claude-version-guard.js` (and its header comment); it does not add new `[mkey]`-parsing or BOW-lookup logic (avoiding two independently-built, possibly-inconsistent implementations of the same check — see Escalations).

### Error handling

- **AC-8.** If the `cmd`/`internal` existence check itself throws (e.g. a filesystem permission error), the hook fails open (allows the commit) exactly as its existing top-level `catch` already does for other unexpected errors — no new fail-closed behaviour is introduced by this change.
- **AC-9.** `CLAUDE_DISABLE_VERSION_GUARD=1` continues to bypass the hook entirely, unchanged.

### Determinism & safety

- **AC-10.** The skeleton-detection check (AC-2) and the resulting allow/deny decision are deterministic given the same repo state and staged files — no randomness, and any wall-clock use in the script remains confined to what already existed (none, per the current source) — `grep -n "time.Now\|Date.now" claude-version-guard.js` returns no matches, same as before this change.
- **AC-11.** No regression to the BOM-tolerant parsing (`input.replace(/^﻿/, '')`) or the shell-command-boundary regex (`/(?:^|[;&|(\n])\s*git\s+.../`) already fixed in prior versions of this hook (see its own `@FIX` history comments) — a passing test re-exercises both existing regression cases (BOM-prefixed stdin; "git commit" appearing inside a string literal in the commit message rather than as the actual shell command) and confirms both still behave correctly after this change.

### Documentation

- **AC-12.** The hook's header comment is updated to reflect the retarget: it must no longer describe itself as unconditionally requiring `app/package.json`+`app/src/lib/version.ts` — it should state the Go-skeleton branch, cite GR#2 and M0-ENG §3, and reference this BOW item (`legacy.versionguard`/`FEAT-002`) and Aaron's 2026-08-08 DECIDED note for the rationale, matching the `@FIX`/dated-comment convention already used in this file's history.

## Out of scope

- Implementing BOW-`[mkey]`-ref validation or auto-ref-on-commit — entirely `tool.bow`'s (`MOD-007`) job; see AC-7 and the Escalation below.
- Any change to how `-ldflags` actually injects version info into the built binary (that's a build-script/Makefile/CI concern, not this hook) — this item only needs the *commit-time gate* to stop blocking on retired files.
- Retroactively tagging or releasing anything — annotated `v0.<milestone>.<n>` tag discipline is a process the team follows at milestone cuts, not something this hook enforces per-commit.

## Escalations

1. **Coordination risk with `tool.bow` (MOD-007), dispatched in the same wave.** Aaron's DECIDED note explicitly says the BOW-ref requirement "merges with the tool.bow git-integration item (MOD-007)" — read literally, that could mean either item's junior might reach for implementing `[mkey]` validation. This file scopes `legacy.versionguard` to **retiring the two-file check only** (AC-7) and leaves all BOW-ref enforcement to `tool.bow.md`'s criteria, to avoid two independently-built hooks with subtly different `[mkey]`-matching regexes or BOW-lookup behaviour landing at the same time and disagreeing with each other on some edge case. Bill: please confirm this split (version-guard retires the old check; `tool.bow` owns 100% of the new BOW-ref check) is the intended reading, since the BOW comment's wording is genuinely ambiguous between "these two pieces of work happen to compose" and "build the ref-check once, shared."
2. **This item is unblocking, not additive** — since `cmd/`/`internal/` already exist and the guard is unretargeted today, every commit touching them is very likely already failing (or would fail on the next non-exempt commit) until this lands. Flagging the urgency is already reflected in `status: active` / the priority-shift request, not a separate ask — noting it here so Bill's final review treats this item's live-fire check (a real passing commit against `internal/`) as load-bearing evidence, not a nice-to-have.
