# Identity: `<Project>-<Name>`

Kit spec v0.2 section 6 and invariant I9 (SAFE-195, Wave 4, 2026-10-20). Every Claude Code window says which project it is and writes only to its own database.

## The scheme

`CLAUDE_IDENTITY` is `<Project>-<Name>`:

- **Project** is the registry slug in display case: `Safe`, `Audit`, `Metro` (metropolis), `SitRep`, `Khan` (cc-khan), `PhotoCurator`, `Photo`, `Prix6`, `Email` (cleanemail), `Tax`, `OD`, `Bio`, `FC` (finalcollapse), `RF` (residualflux), `GF` (ghostflux), `Papers`, `Sunset`, `SecureNotes`, `Marula` (marula-factory), `Minvoice`, `Forge`. The overrides live in `harvest.json` `display_names`; a slug not listed is display-cased by capitalising each hyphen-separated part.
- **Name** is the codename of the window's lead: `Bev` by default (`node forge.js identity <slug>` prints the default). Lane windows keep the project part and change the name: `Safe-Bill`, `Metro-Bro`. Two exceptions Aaron set: `Photo-Bob` (replacing the human's own name) and `Prix6-Bill`.

Before this scheme six live projects all launched as `Bev`; nothing in the window said which project it was, and seven launchers set no database name at all.

## Where it is set and read

| Place | What |
|---|---|
| `project.identity` in the `forge` registry | the source of truth; `register` writes it (explicit `--identity`, else the existing value, else the default) |
| the launcher (`C:\bat\<slug>.bat`) | generated from `launchers/template.bat.tpl` by `node forge.js launchers --apply`; sets `CLAUDE_IDENTITY`, `METRO_DB_NAME`, `FORGE_PROJECT`, `TEMP`/`TMP`, a per-window session GUID, then starts Claude with the memory add-dir and the project's own add-dirs. Never hand-edited. |
| the project's `CLAUDE.md` | `assemble` writes the identity line and the `<identity>> ` response prefix |
| the statusline (`claude-statusline.js`, wired by `assemble`) | shows `<identity> [model] <folder>`; reads `CLAUDE_IDENTITY` first, the per-window `.identity-<session>` file only as a fallback (forge patch `statusline-identity`) |
| `claude-sync.js` slot claims | **the slot name is the part after the last hyphen**: `Safe-Bev` claims `Bev`. Matching stays case-insensitive against `NAMES` (`Bill`, `Ben`, `Bev`, `Bro`). A bare name still works. Forge patch `claude-sync-identity` on the canonical asset; `claude-startup.js` strips the project part the same way (patch `claude-startup-identity`). Project copies are not touched by forge: each project applies the registry version with `node E:\git\forge\forge.js upgrade <slug> --apply`. |
| `claude-db.js` | refuses to connect when `METRO_DB_NAME` (or an explicit `database`) is unset; no fallback to `metro` (patch `claude-db-no-fallback`) |
| `forge` registry writes | every `event_log` and `rule_version` row carries the project, the session and the identity (I12); `node forge.js event` and `rule set` refuse without them |
| `litter_event`, `backup_run` (SAFE) | record `CLAUDE_IDENTITY` |

## Checks

- `node forge.js doctor <slug>`: the launcher exists, sets the registry's identity (with a hyphen), the registry's database name and `TEMP`.
- `node forge.js drift`: the launcher audit for every registered project, run nightly by `safe-status`.
- `node forge.js launchers --check`: renders every launcher and dry-runs it (`FORGE_LAUNCHER_DRYRUN=1` makes the launcher print its resolved environment and exit before Claude starts, GGR62). It also fails when a project root holds a file named like its launcher with an extension in PATHEXT (todo.js beside todo.bat): cmd searches the current folder before PATH, so typing the launcher name inside that root runs the file instead (SAFE-221). `drift` reports the same collision.
