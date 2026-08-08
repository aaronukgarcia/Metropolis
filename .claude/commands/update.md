# /update — harness & memory sync check (Aaron's "update" command)

When Aaron types `update` (or `/update`), it means: **check whether anything needs updating to reflect what has happened since the last update check, and do it.** It is a call to sync the project's memory surfaces with reality — not a request for a status report.

## Checklist — assess each, update only where stale

1. **CLAUDE.md** — do the session protocol, Golden Rules table, dev-team process section, skills list, or environment facts lag behind decisions made this session?
2. **Skills** (`.claude/commands/*.md`) — do any reference retired workflows, old field names, or miss new commands/flags? Are new recurring behaviours worth a new skill?
3. **Hooks** (`.claude/settings.json` + `claude-*.js`) — do guards cover the current commit surface? Any new generated files needing drift protection? Any hook giving stale advice?
4. **BOW** — items discovered-but-untracked? Statuses stale (done work still open, blocked items whose blocker cleared)? New work from recent decisions needing items? Comments recording decisions made in chat but not in the BOW?
5. **Auto-memory** (`~/.claude/projects/E--git-Metropolis/memory/`) — MEMORY.md index + per-fact files current? New durable facts/preferences from this session captured? Stale facts corrected?
6. **Vestige** — `mcp__vestige__smart_ingest` the session's durable decisions/events; verify a recall query surfaces them; correct any wrongly-superseded memories.
7. **Planning SSOT** — master-plan / code.json / bow-import in sync (`node tools/plan/generate.js --check` + regenerate if the plan changed); sprint-plan / dev-team-process / acceptance docs reflecting current reality.
8. **Git** — everything above that changed gets committed (policy: `[type]: description [mkey]`, no trailers) and pushed, with verification.

## Output

Report what was checked, what was updated (with commits), and what was already current — briefly. If a candidate update needs Aaron's decision (e.g. a Golden Rule change), list it as a question instead of acting.
