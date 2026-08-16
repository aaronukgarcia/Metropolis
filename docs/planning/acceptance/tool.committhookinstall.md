BOW code: FEAT-122

# Acceptance criteria — tool.committhookinstall (FEAT-122)

**BOW code:** FEAT-122
**code.json:** GUID `634fa535-7bfd-400c-a773-b6a68722e0b2`, key `tool.committhookinstall` (seq 969, M0 tooling)
**Spec refs:** M0-ENG §5 (hooks); the tracked hook source `githooks/commit-msg` (FEAT-045, `tool.committhook`) that this file installs
**Date:** 2026-08-16
**Status:** retrospective — documenting the contract of already-committed code (`claude-committhook-install.js`), not forward-looking criteria for new work
**Package under test:** `claude-committhook-install.js` (repo root); exercised by `claude-committhook-install.test.js`. This is the **install/verify companion** to the enforcing `githooks/commit-msg` hook (FEAT-045). The enforcing, fail-closed logic lives in the hook file, not here — this file is the report-only half of the pair.
**Standard gates:** Node.js — `node --check claude-committhook-install.js`; SG-6 (no Co-Authored-By). No Go gates apply (root tooling, not a Go package).

## Scope

One deliverable, already committed: `claude-committhook-install.js` copies the tracked canonical hook (`githooks/commit-msg`) into `.git/hooks/commit-msg` for a given repo root, verifies that the installed copy matches the canonical source, and renders the result as a single human-readable line. That last part is the load-bearing requirement: the whole point of this file is that a **missing** hook must never go silently unnoticed — `.git/hooks/` is never version-controlled, so a hook that isn't installed protects nothing, and an install-status check that nobody sees protects equally nothing.

## Acceptance criteria

### Behaviour

- **AC-1. `install(repoRoot)` copies the canonical source into `.git/hooks/commit-msg`.** The canonical source is resolved once at module load to `githooks/commit-msg` relative to `__dirname` (`CANONICAL_SOURCE`). `install()` creates `.git/hooks` recursively (`fs.mkdirSync(..., { recursive: true })`), writes the canonical bytes to the installed path, and returns the installed path. The installed copy is the canonical source byte-for-byte — there is no transformation, templating, or local edit.
- **AC-2. `install()` makes the copy executable best-effort, and swallows the failure.** It calls `fs.chmodSync(dest, 0o755)` inside a `try`/`catch` whose body is a comment — a POSIX exec bit that is deliberately a no-op on Windows. The header records why this is not an error worth reporting: Git for Windows dispatches hooks via its bundled shell reading the shebang line regardless of the exec bit (ASM-754).
- **AC-3. `verify(repoRoot)` returns exactly one of three states and never throws.** The three states are never collapsed (the "silently unprotected" failure mode this file exists to prevent): `healthy` (installed copy exists and matches the canonical source byte-for-byte), `stale` (exists but does not match — the tracked source moved on and the copy didn't), `absent` (no file, **or** a file that exists but cannot be read — reported as `absent` with a `note` naming the underlying error, never `healthy`). The comparison is content-based (length check plus `Buffer.compare`), never existence-only: an existing-but-hand-edited or unrelated leftover file reads as `stale`, not `healthy`.
- **AC-4. `summaryLine(repoRoot)` renders each state as one human-readable line naming the state explicitly.** `healthy` prints "COMMIT-MSG IDENTITY HOOK: healthy (installed, matches tracked source)."; `stale` and `absent` print the state in caps and append the remediation command (`node claude-committhook-install.js install`). The distinction must reach a human as **text** — a session-start summary has nothing to print from a bare exit code.
- **AC-5. The CLI has three branches.** `install [repoRoot]` installs then prints `summaryLine`; `verify [repoRoot]` prints `summaryLine` and sets `process.exitCode` 0 for `healthy` / 1 otherwise; any other first argument prints usage to stderr and sets exit code 2. The default `repoRoot` is `__dirname` (the repo root), overridable by the second argv.

### Fail-open posture

- **AC-6. The installer/verify is report-only — it never blocks and never denies.** There is no code path in this file that exits non-zero for the purpose of refusing an action (the CLI's `verify` exit 1 is a *report* of the `absent`/`stale` state, not a refusal — nothing has been prevented; a hook is simply not in place and the exit code tells a caller that). The enforcing, **fail-closed** half of the pair is `githooks/commit-msg` itself (FEAT-045, `tool.committhook`), which this file installs but does not implement. A future edit must not give this file a blocking decision — its job is to observe and say so, loudly.
- **AC-7. Failures are surfaced as states, not exceptions.** A missing file is the `absent` state; a present-but-unreadable file is the `absent` state **with a `note`** (ASM-755) — the read error is not swallowed silently. The only deliberately-swallowed error is the Windows `chmod` (AC-2). `verify()` never throws and never propagates a filesystem error to its caller.

### Tests

- **AC-8. `claude-committhook-install.test.js` (run: `node --test claude-committhook-install.test.js`) proves the behaviour ACs.** It covers: `install()` creating a byte-for-byte copy where none existed, plus the POSIX exec bit (skipped on win32); `verify()` reporting `absent` on no file, `healthy` immediately after a real install, `stale` for a tampered copy and for an unrelated leftover file, and flipping back to `absent` after deleting a previously-healthy hook.
- **AC-9. The session-start wiring is proven unconditional, not merely "mentioned somewhere".** The test traces `summaryLine()` into `claude-startup.js`'s `printSessionSummary()`, and asserts `emitSuccess()` (the per-checkin entry point) calls it — not a skill/slash command a human must remember to run. Two behavioural tests capture `printSessionSummary()`'s output for a simulated ABSENT and STALE hook and assert the words "ABSENT"/"STALE" and the "not identity-protected" wording actually appear on stdout.

### Determinism

- **AC-10. `install`/`verify`/`summaryLine` are deterministic given the same repo state.** The decision reads only two files (the canonical source and the installed copy); there is no randomness and no wall-clock use in the state decision.

## Out of scope

- The enforcing hook's own logic — identity check, codename content scan, and their fail-closed posture. That is `tool.committhook` (FEAT-045), the file this one installs.
- Closing `git commit --no-verify` — impossible from a hook by construction; that gap belongs to `tool.committhook`'s AC-15, not here.
- The identity/codename shared modules the hook requires (`claude-author-identity.js`, `claude-codename-content-scan.js`).

## Assumptions

Logged via `node claude-bow.js add assumption`:

- **ASM-754 (P2)** — the Windows exec-bit swallow and the "Git for Windows dispatches via shebang regardless of exec bit" rationale are inherited from FEAT-045's AC-12 and were not independently re-verified this session against a Node-scripted hook; if Git for Windows ever gates hook execution on the exec bit, the installed hook would silently fail to run while `verify()` still reports `healthy` (content comparison alone cannot see executability).
- **ASM-755 (P2)** — the `verify()` present-but-unreadable → `absent` branch has no test fixture (the suite exercises absent/healthy/stale/tampered/leftover/deleted, but not a file that throws on read), so that branch is documented from source reading only.

## Escalations

- **ASM-755's direction is the safe one, but note it.** A permissions-denied *healthy* hook reads as `absent` (fail toward warning, never toward a false "protected"). No action needed — flagged only so a future reader understands the deliberate asymmetry and does not "fix" the read to report `healthy` for an unreadable file.
