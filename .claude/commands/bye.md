---
description: Session end — git status check, claude-sync checkout, Vestige session summary, graceful sign-off
allowed-tools: Bash(git status:*), Bash(git log:*), Bash(git diff:*), Bash(node claude-sync.js:*)
---

## Context

- Git status: !`git status --short`
- Uncommitted changes: !`git diff --stat HEAD`
- Today's commits: !`git log --oneline --since="24 hours ago"`
- Current permit: !`node claude-sync.js read 2>/dev/null | head -20`

## Your task

The session is ending. Complete the shutdown sequence in order.

---

### STEP 1 — Uncommitted work check

Check git status above. If there are uncommitted changes:

- List them explicitly
- Ask the user: "There are uncommitted changes — do you want to commit before ending the session, or leave them staged?"
- If committing: run `/commit` first, then return here
- If leaving: note what's pending in the session summary

---

### STEP 2 — Write session summary to Vestige

Use `mcp__vestige__smart_ingest` to write a session summary. Include:

- Date and identity (bill> session YYYY-MM-DD)
- Commits made this session (from the git log above)
- Key decisions or architectural changes
- Any gotchas discovered
- Work left incomplete / what to pick up next session
- Any Golden Rule violations (if any)

Tag with: `prixsix`, `session-summary`, current date

---

### STEP 3 — Claude-sync checkout

Run the checkout to release the permit:

```
node claude-sync.js checkout --session $CLAUDE_SESSION_ID
```

If `$CLAUDE_SESSION_ID` is not set, check `node claude-sync.js read` for the active Bill session ID and use that.

---

### STEP 4 — Update MEMORY.md

If the current version number has changed, or any architectural gotcha was discovered this session, update `C:\Users\aarongarcia\.claude\projects\E--git-prix6\memory\MEMORY.md` to reflect it.

NOTE: project location moved 2026-04 from `E:\GoogleDrive\Papers\03-PrixSix\` to `E:\git\prix6\` — memory dir is at `C:\Users\aarongarcia\.claude\projects\E--git-prix6\memory\`. Older session prompts may reference the legacy path.

---

### STEP 4B — Memory hygiene quick-check (added v3.1.7)

Light-touch reminder, NOT a blocking gate. Surfaces obvious issues without forcing fixes.

Search Vestige for memories with timestamp-anchored "current X" facts that may be stale:

```
mcp__vestige__search query="prix six current version as of"
mcp__vestige__search query="prix six version state"
```

If results contain memories like `Prix Six current version is 2.0.12 as of 2026-03-04` and the current version differs, flag in the sign-off:

```
⚠️  N stale "current state" memories detected in Vestige. Consider running
    /memory-hygiene next session to clean these up. (Not blocking sign-off.)
```

Search for memories pointing to the legacy path:

```
mcp__vestige__search query="E:\\GoogleDrive\\Papers\\03-PrixSix"
```

If results > 0, flag:

```
⚠️  N memories reference the legacy project path. /memory-hygiene
    can rewrite these to the current path E:\git\prix6\.
```

The full hygiene pass is `/memory-hygiene` — this gate just notices issues.

Report inline with the sign-off; don't block on it.

---

### STEP 5 — Sign off

```
bill> Goodnight! Session ended cleanly.

     This session: [one-line summary of what was done]
     Version: vX.Y.Z [deployed/staged/in-progress]
     Next session: [what to pick up]

     Permit released. Vestige updated. Bill checked out. 👋
```
