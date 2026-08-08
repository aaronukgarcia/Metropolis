---
description: Book of Work — view, add, update Metropolis work items (modules/features/bugs/interfaces) in the metro MariaDB via claude-bow.js
allowed-tools: Bash(node:*)
---

## Context

- BOW summary: !`node claude-bow.js summary`
- Today: !`node -e "console.log(new Date().toISOString().split('T')[0])"`

## Your task

Manage the Metropolis Book of Work — the single source of truth for planned/active work. Backend: `bow_items` / `bow_dependencies` / `bow_comments` / `bow_git_refs` tables in the `metro` MariaDB, driven entirely through `claude-bow.js` (never raw SQL for writes).

Every item has a GUID, a short code (`MOD-001` / `FEAT-001` / `BUG-001` / `INT-001`), priority `P0`–`P3`, status (`open` / `in_progress` / `blocked` / `done` / `cancelled`), dependencies, comments (optionally with example code), and git commit refs.

**ARGUMENTS:** $ARGUMENTS — if provided, treat as a sub-command. If empty, default to VIEW.

---

### Sub-commands

| Argument | Action |
|----------|--------|
| (empty) or `view` | `node claude-bow.js list` — open items grouped by priority |
| `all` | `node claude-bow.js list --all` — every item incl. done/cancelled |
| `seq` or `order` | `node claude-bow.js list --by-seq` — build order (seq + sprint) |
| `ready` | `node claude-bow.js ready` — open items with no open deps, by sprint (see also /sprint) |
| `show <CODE>` | `node claude-bow.js show <CODE>` — full detail: deps, comments, code, git refs |
| `add` | Create a new item (infer details from context, confirm with user) |
| `done <CODE>` | Mark done — ask for a one-line resolution note first |

---

### Command reference (claude-bow.js)

```bash
node claude-bow.js add <module|feature|bug|interface> "title" [--priority P0..P3] [--desc "..."]
                       [--mkey k] [--seq N] [--sprint N] [--milestone M1] [--layer L] [--spec "§n"]
node claude-bow.js list [--type T] [--status S] [--all] [--by-seq]
node claude-bow.js ready                                        # open items, no open deps, by sprint+seq
node claude-bow.js show <CODE|GUID|mkey>
node claude-bow.js comment <CODE> "text" [--example-file F | --example "code"] [--lang js]
node claude-bow.js depend <CODE> --on <CODE> [--note "..."]     # cycle-checked
node claude-bow.js undepend <CODE> --on <CODE>
node claude-bow.js ref <CODE> <commit-hash> [--note "..."]      # link a git commit
node claude-bow.js set <CODE> [--priority P1] [--status ...] [--seq N] [--sprint N] [...]
node claude-bow.js done <CODE> [--note "resolution"] [--force]  # GR#12: blocked while deps open
node claude-bow.js import tools/plan/bow-import.json [--dry-run] # bulk load from master plan (idempotent)
```

**Master-plan items** (those with an `mkey` matching `docs/planning/master-plan-v2.1.json`) are edited in the plan file, then `node tools/plan/generate.js` regenerates `code.json` + `tools/plan/bow-import.json`, then `import` refreshes the BOW. Ad-hoc items (bugs, discovered work) live only in the BOW.
```bash
```

---

### Discipline

- **When you complete work tracked by a BOW item:** after the commit, run `ref <CODE> <hash>` to link it, then `done <CODE> --note "..."`. Never mark done without the git ref if code changed.
- **GR#12:** `done` refuses while the item has open dependencies. Only use `--force` when the user confirms the dependency is genuinely not a blocker.
- **New work discovered mid-task:** add it as a BOW item immediately rather than carrying it in your head.
- Comments carrying design decisions or example code beat re-deriving them next session — be generous with `comment`.

### Confirm

```
bill> 📋 BOW — N open items
     P1: [list]
     P2: [list]
```

or after an update:

```
bill> ✅ BOW updated — FEAT-003 marked done (linked commit abc1234)
     Resolution: [note]
```
