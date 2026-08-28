BOW code: FEAT-114

# Acceptance criteria — tool.memoryprefetch (FEAT-114)

**Module key:** tool.memoryprefetch
**BOW code:** FEAT-114 (GUID 4f83f462-e5a9-48b7-9094-8565789fc1e7)
**Spec refs:** GR#14 (Memory Recall at Task Start); M0-ENG §5 (hooks); `CLAUDE.md` hooks table ("UserPromptSubmit: `claude-memory-prefetch.js` — GR#14 Vestige recall reminder").
**Date:** 2026-08-16 (written retrospectively — the code is already committed; this file documents its contract, not a pre-dispatch brief)
**Status:** active — archaeology (same register as `tool.authorguard.md`: criteria written after the fact)
**Package under test:** `claude-memory-prefetch.js` (repo root, Node.js).
**Standard gates:** Node, not Go — SG-1/2/4/7 do not apply. This item's own gate: `node --check claude-memory-prefetch.js`. **No dedicated test file exists today** — see ASM-729 and section C.

## Why this file exists, and why it is documented after the fact

GR#14 says memory recall happens *at task start*, but the rule's whole point is that commit/deploy/security decisions happen mid-session, long after the startup summary has scrolled away. This hook nudges at the moment of action instead: on every user prompt it appends a short reminder to the conversation, telling Claude to query Vestige (`mcp__vestige__search`) before composing a commit message, a deploy command, or security-sensitive code — and, crucially, that the `/commit` and `/deploy` skills already do this in their GATE 0, so the reminder only demands manual recall for *ad-hoc* requests.

It is a **static** reminder: the script has no MCP access and deliberately does not call Vestige itself — it only nudges the model to. Like the other `tool.*` hook files, it shipped without a criteria file; the contract below is stated after the fact.

## A. Behaviour

- **AC-1 (engagement — hook type).** The hook is wired as a **UserPromptSubmit** hook in `.claude/settings.json` with `timeout: 2` (the only UserPromptSubmit entry). It runs before/at every user prompt and its stdout is appended to the conversation as user-prompt-submit-hook context. Check: `grep -n "claude-memory-prefetch.js" .claude/settings.json` shows it inside the `UserPromptSubmit` array with `"timeout": 2`.
- **AC-2 (default behaviour — emit the reminder).** With no disable flag set, the script writes the fixed reminder string to stdout and exits 0 (source: `claude-memory-prefetch.js:40-41`). Check: a test runs the script with an unset `CLAUDE_DISABLE_MEMORY_REMINDER` and asserts stdout is non-empty and exit code 0.
- **AC-3 (content contract).** The emitted reminder must (a) name the rule (`GR#14`), (b) point at the Vestige search tool by its exact MCP name (`mcp__vestige__search`), and (c) state that `/commit` and `/deploy` already handle the recall in their GATE 0 so the manual recall applies to ad-hoc requests only (source: `claude-memory-prefetch.js:30-38`). Check: a test asserts stdout contains `GR#14`, contains `mcp__vestige__search`, and contains `/commit` — three independent substring assertions, because each is a separately load-bearing clause and any one dropping would change what the reminder actually teaches.
- **AC-4 (static — no MCP, no network, no subprocess).** The reminder is a hardcoded string; the script never `require`s an MCP client, never opens a network connection, and never spawns a child process (header: "the script has no MCP access"). Check: `grep -n "require\|child_process\|fetch\|https\|mcp__" claude-memory-prefetch.js` shows no dynamic dependency beyond the core `'use strict'` prologue — the only `mcp__` occurrence is *inside the string literal* (the tool name it tells Claude to call), not a call itself. **What a false pass looks like:** a check that only greps for `mcp__vestige__search` would also pass a build that actually *invoked* the tool; the binding part is confirming the token appears inside a quoted string, not in an executable position.
- **AC-5 (disable escape hatch).** `CLAUDE_DISABLE_MEMORY_REMINDER=1` (read from `process.env`) suppresses the reminder entirely — exit 0 with **empty** stdout (source: `claude-memory-prefetch.js:25-27`). Check: a test sets the var to `1` in the test process's env and asserts empty stdout + exit 0. The value `"1"` specifically is the trigger (any other value does not disable — the comparison is strict `=== '1'`).
- **AC-6 (strict mode).** The script runs under `'use strict'` (source: `claude-memory-prefetch.js:22`), so a silent global-leak or bad-assignment bug surfaces as a thrown error and is caught by the fail-graceful wrapper (AC-7) rather than producing undefined behaviour. Check: `head -n 24 claude-memory-prefetch.js | grep -n "use strict"` matches.

## B. Fail-open posture

- **AC-7 (fail-graceful — never blocks a prompt).** The entire body is wrapped in `try/catch`; **any** error is swallowed and the script exits 0 silently (source: `claude-memory-prefetch.js:24, 42-44`). The reminder is explicitly nice-to-have, not blocking (header: "Fail-graceful: any error → exit 0 silently"). A UserPromptSubmit hook that throws could stall the prompt; this one cannot. ASM-728 records this posture as deliberate. Check: a test that forces an error (e.g. monkey-patches `process.stdout.write` to throw) asserts exit 0 with no unhandled rejection. The runnable form is "any throw → exit 0"; the prose form ("reminder is nice-to-have, not blocking") is reviewed by eye.
- **AC-8 (no gate, nothing to bypass).** Unlike the PreToolUse security guards, this hook has no deny semantics and no permission decision to make — its only outputs are "emit the reminder" or "emit nothing". There is therefore no bypass surface of the `CLAUDE_DISABLE_*`-in-command-string family that `tool.destructiveguard.md`/`tool.authorguard.md` spend ACs on: a UserPromptSubmit hook receives no command string to smuggle an env var through, and `CLAUDE_DISABLE_MEMORY_REMINDER` is read from `process.env` only (AC-5). Check: reviewed by eye — the header names the env var and the read site is `process.env.CLAUDE_DISABLE_MEMORY_REMINDER`, not any parse of an input string.

## C. Tests

- **AC-9 (ASM-729 — the gap, written RED today).** **No test file exists** for this hook (`claude-memory-prefetch.test.js` is absent from the repo root). The ACs below are tests a future test file must contain and are **RED by absence** today — no check currently proves any of the contract above. Do not mark this file's ACs as passing without a `claude-memory-prefetch.test.js` landing that covers AC-10–AC-13. Check: `Test-Path claude-memory-prefetch.test.js` is false today; the AC turns green only when that path exists AND AC-10–AC-13 have independently failing tests. **False-pass:** marking AC-2–AC-8 green by reading the source, with no test file, is a false pass — a regression would land silently.
- **AC-10.** A passing test asserts the default run emits the reminder and exit 0, with the three content substrings from AC-3 each asserted independently.
- **AC-11.** A passing test asserts the disable path: `CLAUDE_DISABLE_MEMORY_REMINDER=1` → empty stdout, exit 0, and that a non-`"1"` value does **not** disable (the strict `=== '1'` comparison).
- **AC-12.** A passing test asserts statieness: no `require` of a non-core module and no `child_process`/network usage — i.e. the script's dependency surface is `'use strict'` plus the standard library (nothing to stub, no MCP client in scope).
- **AC-13.** A passing test asserts the fail-graceful path: a forced throw (e.g. `process.stdout.write` stubbed to throw) still exits 0.

## Out of scope (stated, not silently absent)

- Actually querying Vestige / returning recalled memory — this hook only *nudges*; the recall itself is the model's job via the `mcp__vestige__search` tool (and via `/commit`/`/deploy` GATE 0 for those flows). ASM-728 records that this is by design (the script has no MCP access), not an unfinished feature.
- De-duplicating or rate-limiting the reminder across prompts — the reminder is emitted on *every* prompt by design (the header calls it "short to minimise noise"); any suppression beyond `CLAUDE_DISABLE_MEMORY_REMINDER=1` is future work.
- Editing the reminder copy to stay current with the `/commit`/`/deploy` skill internals — if those skills change their GATE 0 behaviour, this string is a second place that must be updated by hand (a mild GR#3 duplication, acknowledged here rather than silently ignored).

## Escalations / Assumptions

- **ASM-729 — no test file.** The single largest gap: the hook has survived without automated coverage. Flagged for Bill: commission `claude-memory-prefetch.test.js` (turns AC-9 green) or rule that UserPromptSubmit reminder hooks are exempt and say so in `dev-team-process.md`.
- **ASM-728 — static / no-Vestige / fail-graceful posture.** Recorded deliberately (see AC-4/AC-7): the script is a fixed string plus a disable flag, and its failure mode is "no reminder" rather than "blocked prompt". A future Destructive round should attack "can any environment or error make it crash or block a prompt", not "can it be tricked into querying memory".
