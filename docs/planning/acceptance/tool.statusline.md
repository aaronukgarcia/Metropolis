BOW code: FEAT-113

# Acceptance criteria — tool.statusline (FEAT-113)

**Module key:** tool.statusline
**BOW code:** FEAT-113 (GUID 4886304f-d2ad-4fca-8355-e4c9bc09d1a3)
**Spec refs:** M0-ENG §5 (hooks); `CLAUDE.md` hooks table ("statusLine: `claude-statusline.js` — identity/status display"); `claude-sync.js` lines 305-314 (the per-window `.identity-<window>` file this hook reads, written lowercased on every acquire/checkin).
**Date:** 2026-08-16 (written retrospectively — the code is already committed; this file documents its contract, not a pre-dispatch brief)
**Status:** active — archaeology (same register as `tool.authorguard.md`: criteria written after the fact)
**Package under test:** `claude-statusline.js` (repo root, Node.js).
**Standard gates:** Node, not Go — SG-1/2/4/7 do not apply. This item's own gate: `node --check claude-statusline.js`. **No dedicated test file exists today** — see ASM-727 and section C.

## Why this file exists, and why it is documented after the fact

The status line is the only always-visible identity signal in a multi-window setup: it shows *which window is which* (`bill>` vs `bob>` vs `ben>`), which model, and which directory. The file exists because a plain `echo` or a name hardcoded in `settings.json` cannot do per-window identity — the correct name lives in a per-window file that `claude-sync.js` writes at checkin, and the shared `.identity` file is last-checkin-wins (so in two windows it would show the *other* window's name). The hook reads the per-window file first and falls back to the shared one.

Like the other `tool.*` hook files, this shipped without a criteria file; the contract below is stated after the fact so the next round (a future test file, or a Destructive round) has something to judge against.

## A. Behaviour

- **AC-1 (engagement — hook type).** The hook is wired as the `statusLine` command in `.claude/settings.json` (`statusLine.command = "node claude-statusline.js"`), a single-command status line with no `matcher` — it renders continuously, not per-tool-type. Check: `grep -n "claude-statusline.js" .claude/settings.json` shows it in the top-level `statusLine` object, not inside any `hooks` array.
- **AC-2 (output shape).** The script emits exactly one line, `${name}> [${model}] ${dir}`, to stdout with **no trailing newline** (source: `claude-statusline.js:35`). The `name> ` prefix is GR#4 (identity prefix); the `[model]` and trailing `dir` are the status/context fields. Check: a test feeds a fixture payload and asserts stdout is exactly `bill> [Sonnet] internal` (or the fixture's expected string) with `stdout.endsWith('\n') === false`. **What a false pass looks like:** a check that only asserts "contains `bill>`" would also pass a build that appended a newline, added a stray field, or reordered the tokens — the exact-string (or per-segment) assertion is the binding part.
- **AC-3 (model — passthrough with fallback).** `model` is `data.model?.display_name`, falling back to the literal `Claude` when the payload has no model or no `display_name` (source: `claude-statusline.js:19`). Check: a test with `{"model":{"display_name":"Sonnet"}}` asserts `[Sonnet]`; a test with `{}` asserts `[Claude]`.
- **AC-4 (dir — basename only).** `dir` is `path.basename(data.workspace?.current_dir)`, empty when the payload has no workspace or no `current_dir` (source: `claude-statusline.js:20`). Check: a test with `current_dir = "E:\\git\\Metropolis\\internal\\ui"` asserts the token is `ui`, not the full path — a build that forgot `path.basename` would leak the absolute path into the status line, so the check must use a multi-segment path to catch it.
- **AC-5 (identity — per-window first, then shared, then default).** `name` is read from the first readable file among: `.claude/.identity-${session_id}` (only when `session_id` is present), then `.claude/.identity`; if neither exists or is readable, it stays `???` (sources: `claude-statusline.js:13, 28-32`). Check: a test creates both files with different contents and asserts the per-window value wins; a test creates only `.identity` and asserts that value wins; a test with neither asserts `???`. **What a false pass looks like:** a test that only checks the shared-file case would also pass a build that never reads the per-window file (the exact multi-window bug this file exists to fix), so the per-window-wins case must be asserted separately.
- **AC-6 (project dir resolution).** The `.claude` directory the identity files are read from is `data.workspace?.project_dir` joined with `.claude`, falling back to `__dirname` when the payload has no `project_dir` (source: `claude-statusline.js:25-26`). Check: a test with a fixture `project_dir` and a fixture identity file inside its `.claude` asserts the name is read from *that* location (not the repo root's).
- **AC-7 (name is verbatim — lowercase is upstream's choice).** The name is written to stdout exactly as read from the identity file, with no re-capitalisation; because `claude-sync.js` writes these files lowercased (`name.toLowerCase()`, `claude-sync.js:313-314`), the displayed name is lowercase (`bill`, not `Bill`). Check: a test writes a fixture identity file containing `BILL` and asserts the output shows `BILL` — proving the statusline does not transform case, which is what tells a future reader "the lowercase is `claude-sync.js`'s doing, not this hook's".

## B. Fail-open posture

- **AC-8 (must never fail — always a line, exit 0).** The entire parse/read body is wrapped in `try/catch` (source: `claude-statusline.js:17-33`); on any malformed JSON, missing field, or unreadable file, the hook falls back to the defaults (`???` / `Claude` / empty dir) and still emits the line, and the process exits 0 implicitly by reaching end-of-script. The status line can never be blank, and it can never crash the harness's render. ASM-726 records this as deliberate. Check: a test feeds `not-json` (or `{}`) and asserts exit 0 with stdout exactly `???> [Claude] ` (or `???> [Claude] ` + the resolved dir, if any) — the binding part is that it emits *something* well-formed, not that it errors.
- **AC-9 (read-only).** The hook reads identity files and stdin JSON; it writes nothing to disk and changes no state. Check: `grep -n "writeFile\|appendFile\|mkdir" claude-statusline.js` returns no matches — the only writes are `process.stdout.write`.

## C. Tests

- **AC-10 (the gap — written RED today).** **No test file exists** for this hook (`claude-statusline.test.js` is absent from the repo root). ASM-727 records this. The ACs below are tests a future test file must contain and are **RED by absence** today — no check currently proves any of the contract above. Do not mark this file's ACs as passing without a `claude-statusline.test.js` landing that covers AC-11–AC-14.
- **AC-11.** A passing test asserts the exact output shape `name> [model] dir` with no trailing newline for a full fixture payload (AC-2/AC-3/AC-4 combined).
- **AC-12.** A passing test asserts the identity precedence chain — per-window file beats shared file, shared file beats `???` (AC-5), including the `project_dir`-resolved location (AC-6).
- **AC-13.** A passing test asserts fallbacks: absent `model.display_name` → `Claude`; absent `workspace.current_dir` → empty dir token (AC-3/AC-4).
- **AC-14.** A passing test asserts malformed JSON still yields the default line and exit 0 (AC-8).

## Out of scope (stated, not silently absent)

- **No stdin timeout safety net** — unlike `claude-ping-check.js` (which has a 2000 ms `.unref()` timeout), this hook simply waits for stdin `end`. That is correct under the statusLine harness contract (the harness always provides JSON and closes the pipe); a manual `node claude-statusline.js` run in a TTY would block until EOF. Noted as a divergence, not a defect — the two hooks serve different callers.
- Writing the identity file — that is `claude-sync.js`'s job (line 313-314). This hook only reads.
- Any colour/formatting/emoji in the status line — the output is plain text; the prefix+model+dir shape is the whole contract.

## Escalations / Assumptions

- **ASM-727 — no test file.** The single largest gap: the hook has survived without automated coverage. Flagged for Bill: commission `claude-statusline.test.js` (turns AC-10 green) or rule that statusLine display hooks are exempt and say so in `dev-team-process.md`.
- **ASM-726 — read-only / must-never-fail posture.** Recorded deliberately (see AC-8): a status line that throws would degrade the entire harness UI for zero security benefit, so the fail-open posture is the only sane one. A future Destructive round should attack "can any payload crash it or blank the line", not "can it be bypassed".
