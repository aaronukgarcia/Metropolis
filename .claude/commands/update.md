# /update — harness & memory sync check (Aaron's "update" command)

When Aaron types `update` (or `/update`), it means: **check whether anything needs updating to reflect what has happened since the last update check, and do it.** It is a call to sync the project's memory surfaces with reality — not a request for a status report.

## ⚠️ WHO MAY RUN WHICH VERSION (2026-08-18 — parallel-team rule)

**The FULL checklist below is LEAD-ONLY (Bev, or whichever session Aaron designates lead).** CLAUDE.md, skills, hooks, planning SSOT, auto-memory, and Vestige are lead-owned surfaces; broad "commit everything that changed" is only safe in the lead's checkout.

**Coder sessions (Ben, Bill, Bob, or any lane session): run the SCOPED variant instead —**
1. **Your BOW items only** — statuses current (done work flipped with refs? verdicts recorded? decisions from your chat captured as comments)? New work you discovered in YOUR lane gets an item.
2. **Your worktree** — on your `lane/<name>` branch; fresh vs origin/main (`git fetch origin main` + rebase your OWN branch); everything committed + pushed (no local-only commits).
3. **Your acceptance docs** — if reality in your modules drifted from `docs/planning/acceptance/engine.<name>.md`, update THOSE files (they're in your lane) and note it.
4. **Everything else you noticed stale** (CLAUDE.md, skills, hooks, code.json, other lanes, shared tooling, the forge registry) — **REPORT it to Aaron/the lead; do NOT edit it.** No CLAUDE.md/skills/hooks/master-plan/code.json edits, no auto-memory/Vestige writes (those are Claude-lead surfaces your CLI may not even have), no broad staging — exact paths only, as always.

## Checklist (LEAD-ONLY) — assess each, update only where stale

1. **CLAUDE.md** — do the session protocol, Golden Rules table, dev-team process section, skills list, or environment facts lag behind decisions made this session?
2. **Skills** (`.claude/commands/*.md`) — do any reference retired workflows, old field names, or miss new commands/flags? Are new recurring behaviours worth a new skill?
3. **Hooks** (`.claude/settings.json` + `claude-*.js`) — do guards cover the current commit surface? Any new generated files needing drift protection? Any hook giving stale advice?
4. **BOW** — items discovered-but-untracked? Statuses stale (done work still open, blocked items whose blocker cleared)? New work from recent decisions needing items? Comments recording decisions made in chat but not in the BOW?
5. **Auto-memory** (`~/.claude/projects/E--git-Metropolis/memory/`) — MEMORY.md index + per-fact files current? New durable facts/preferences from this session captured? Stale facts corrected?
6. **Vestige** — `mcp__vestige__smart_ingest` the session's durable decisions/events; verify a recall query surfaces them; correct any wrongly-superseded memories.
7. **Planning SSOT** — master-plan / code.json / bow-import in sync (`node tools/plan/generate.js --check` + regenerate if the plan changed); sprint-plan / dev-team-process / acceptance docs reflecting current reality.
8. **Forge registry** (the cross-project memory — `node E:\GoogleDrive\Tools\new\forge.js`, run from that folder; **LEAD-ONLY** — coders report drift per the scoped rule above, never edit the registry):
   1. **Rules that moved.** Any GGR rule Aaron refined this session: `node forge.js rule set <no> --summary "..." --note "why" --from metropolis` (bumps `GGR<no>v<version>`, keeps history). Then regenerate **and** mark: `node forge.js ggr --out E:\git\Metropolis\GGR.md` then `node forge.js ggr-mark metropolis`. The mark is mandatory — `ggr --out` only writes the file, and without `ggr-mark` the drift check lists every rule as drift forever (the 2026-10-05 fix). Never hand-edit GGR.md.
   2. **Assets that moved.** If a forge-sourced hook/skill/script changed here, or a new one is worth sharing: confirm `metropolis` in `E:\GoogleDrive\Tools\new\harvest.json`, then `node forge.js harvest --only metropolis` (version bumps show in `node forge.js list --project metropolis`).
   3. **Lessons.** A cross-project lesson → `harvest\lessons.json` + `node forge.js import lessons ...`, or straight to a rule via `rule set`.
   4. **Drift check.** `node forge.js upgrade metropolis` — anything moved in the registry since assembly? Apply mechanical drift with Aaron's yes; any GGR-wording that disagrees with Metropolis's own statements goes to Aaron for a decision, never self-resolved.
   5. `node forge.js backup`.
9. **Git** — everything above that changed gets committed (policy: `[type]: description [mkey]`, no trailers, noreply author) and pushed the same session, with the push verified.

## Output

Report what was checked, what was updated (with commits), and what was already current — briefly. If a candidate update needs Aaron's decision (e.g. a Golden Rule change), list it as a question instead of acting.
