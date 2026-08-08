---
description: Vestige memory hygiene — detect duplicates, stale facts, dead pointers; suggest demote/merge/delete actions
allowed-tools: Bash(node:*), Bash(test:*)
---

## Context

- Current version: !`node -e "try{console.log(require('./app/package.json').version)}catch(e){console.log('NOT FOUND')}" 2>/dev/null`
- Today's date: !`date +%Y-%m-%d`

## Your task

Scan Vestige for memory hygiene issues. Output a list of recommended actions but DO NOT auto-execute them — present to the user for sign-off, then act.

---

### STEP 1 — Search for stale "current state" facts

Many memories take the form "Prix Six current X is Y as of YYYY-MM-DD". These are timestamp-anchored facts that decay; Vestige preserves them at retention 1.0 because it has no notion of "current state vs historical state".

Run these queries and inspect results:

```
mcp__vestige__search query="prix six current version as of"
mcp__vestige__search query="prix six version state deployed"
mcp__vestige__search query="prix six current build status"
mcp__vestige__search query="prix six wave progress security"
```

For each result, compare the date in the memory body to today's date in Context. If older than 90 days AND the memory is `node_type: fact` AND content is timestamp-anchored:

- **Recommendation:** demote via `mcp__vestige__demote_memory` (lowers retention)
- OR rewrite via `mcp__vestige__smart_ingest` with current state (supersedes the stale one)

---

### STEP 2 — Detect duplicate clusters

Run a series of broad queries and look at similarity scores:

```
mcp__vestige__search query="prix six golden rule reference"
mcp__vestige__search query="prix six error handling pattern"
mcp__vestige__search query="prix six commit policy attribution"
mcp__vestige__search query="prix six SSOT single source truth"
```

For each query, if 2+ results have similarity > 0.85, they are candidates for merging:

- **Recommendation:** keep the most recent / most detailed entry; demote the others
- Use `smart_ingest` to write a consolidated version, which will trigger merge/supersede automatically

---

### STEP 3 — Detect dead-pointer memories

Search for memories that mention specific file paths:

```
mcp__vestige__search query="E:\\GoogleDrive\\Papers\\03-PrixSix"
```

If results > 0, those memories reference the **legacy** project location (the project moved to `E:/git/prix6/` around 2026-04). The path content is stale even if the lesson isn't.

For each match:
- **Recommendation:** rewrite via `smart_ingest` with the current path
- The content stays the same, just the absolute path is updated
- Vestige should `update`/`merge` the existing memory in response

Also search for memories naming code paths that may have been renamed/removed:

```
mcp__vestige__search query="prix six file path module location"
```

For any result mentioning `app/src/...` paths, sample 2-3 with `Test-Path` (PowerShell) or `test -f` (bash):

```bash
test -f app/src/lib/<path> && echo "exists" || echo "MISSING"
```

If a memory's referenced file no longer exists, the lesson may still be valid but the file pointer is stale.

---

### STEP 4 — Detect rules-as-memory drift

Some rules live both in CLAUDE.md / golden-rules-detail.md AND as Vestige memories. Verify the canonical doc is in sync.

```
mcp__vestige__search query="prix six golden rule complete reference all rules"
```

If the result lists 11 rules but the project has 19, the memory is out-of-date with `CLAUDE.md`. Same for any feature-specific rule.

- **Recommendation:** rewrite the canonical reference memory with the current 19-rule list
- Use `smart_ingest`; it will supersede the older 11-rule version

---

### STEP 5 — Output the action list

Present findings as a single table for user sign-off:

```
bill> Memory hygiene scan — YYYY-MM-DD
     ─────────────────────────────────────────────────────────────────
     ID (truncated)  | Issue                              | Action
     ─────────────────────────────────────────────────────────────────
     307c96d8...     | "current version 2.0.12 as of...." | demote (89d old fact)
     92d29227...     | duplicate of 31095ac4 (sim 0.91)   | demote duplicate
     3ae907eb...     | references E:/GoogleDrive path    | rewrite with new path
     ...
     ─────────────────────────────────────────────────────────────────
     X actions recommended. Apply all? (y/N)
```

Wait for user confirmation before executing. Apply via `mcp__vestige__demote_memory`, `mcp__vestige__delete_memory`, or `mcp__vestige__smart_ingest` depending on action type.

Do NOT auto-delete `node_type: pattern` or `node_type: decision` memories — those are project knowledge regardless of date. Only `node_type: fact` is a candidate for demote/delete.

---

### Cadence

Run quarterly, or after any session that creates 5+ new memories. Frequent enough to keep memory accurate, infrequent enough to not interrupt active work.
