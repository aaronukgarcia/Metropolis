# READ THIS NOW — Claude startup (Vestige 4.x / GR#67)

**Standing startup instruction. Do this at the start of every session in this project.** Installed by forge under GR#67. SSOT: the forge registry + SAFE (`E:/projects/63-SAFE`).

## Vestige is 4.x (Strata) — find-then-pull, not search
Vestige's free-text/semantic recall and the `mcp__vestige__search` tool were **removed in 4.x by design**. This is a capability change, **not** an outage or a "degraded" state — do not report it as degraded.

For memory recall:
1. `mcp__vestige__session_start` — bulk startup context in one call.
2. Free text → handle: `node vestige-pointer.js find "<what you want>" --scope <slug>`  (add `--all` to search every scope).
3. Pull it exactly: `mcp__vestige__recall { handle: "<id-or-exact-tag>" }`.

**Never** call `mcp__vestige__search` (gone) or pass free text to `recall` (returns `similarity_disabled`).

## The finder
Forge installs this project's **own** `vestige-pointer.js` (GR#67): under `harness/` in code projects (`node harness/vestige-pointer.js find ...`), at the project root elsewhere. Run that local copy — it queries the **shared** `safe.vestige_pointer` index (rebuilt across all scopes at the SAFE project's `/update`, so this project's memories are already in it).

**Prerequisite: the `mysql2` node module**. The finder looks for it in this project first, then in SAFE's checkout (`SAFE_HOME`, default `E:/projects/63-SAFE/node_modules`), so it runs with no install on this machine. If it prints `mysql2 not found here or under ...`, either set `SAFE_HOME` to the SAFE checkout or run `npm install mysql2` in this project. An old copy that dies with `Cannot find module 'mysql2/promise'` predates the fallback (asset v1/v2): refresh it with `node E:/git/forge/forge.js upgrade <slug> --only vestige-pointer --apply`. A local copy is used on purpose: running SAFE's copy from another project is blocked by the auto-mode classifier as external code. If the local finder is ever missing, either restore it (`/new --upgrade <slug>`) or add a Bash permission allow-rule for SAFE's copy at `E:/projects/63-SAFE/vestige-pointer.js`.

## If this project's callers aren't migrated yet
If this project's startup/hook text still names `mcp__vestige__search` or uses free-text recall, migrate it to find-then-pull. Authority: `E:/projects/63-SAFE/docs/vestige-4x-caller-migration.md`. Reference commits: SAFE `fadaba5`, Metropolis `7e83cc3`.

