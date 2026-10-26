# READ THIS NOW — Claude startup (Vestige 4.x / GR#67)

**Standing startup instruction. Do this at the start of every session in this project.** Distributed 2026-10-06 under GR#67. SSOT: the forge registry + SAFE (`E:/projects/63-SAFE`).

## Vestige is 4.x (Strata) — find-then-pull, not search
Vestige's free-text/semantic recall and the `mcp__vestige__search` tool were **removed in 4.x by design**. This is a capability change, **not** an outage or a "degraded" state — do not report it as degraded.

For memory recall:
1. `mcp__vestige__session_start` — bulk startup context in one call.
2. Free text → handle: `node <finder> find "<what you want>" --scope metropolis`  (add `--all` to search every scope).
3. Pull it exactly: `mcp__vestige__recall { handle: "<id-or-exact-tag>" }`.

**Never** call `mcp__vestige__search` (gone) or pass free text to `recall` (returns `similarity_disabled`).

## The finder
`<finder>` = this project's own `vestige-pointer.js` if it has one, else SAFE's at `E:/projects/63-SAFE/vestige-pointer.js`. **Caveat:** running SAFE's copy from another project is blocked by the auto-mode classifier as external code. So this project should either ship its **own** `vestige-pointer.js` (it still queries the shared `safe.vestige_pointer` index) or add a Bash permission allow-rule for SAFE's. The index is shared and rebuilt nightly across all scopes, so this project's memories are already in it.

## If this project's callers aren't migrated yet
If this project's startup/hook text still names `mcp__vestige__search` or uses free-text recall, migrate it to find-then-pull. Authority: `E:/projects/63-SAFE/docs/vestige-4x-caller-migration.md`. Reference commits: SAFE `fadaba5`, Metropolis `7e83cc3`.
