---
description: Session end (Metropolis) — git status check, claude-sync checkout (metro MariaDB), Vestige session summary, graceful sign-off
allowed-tools: Bash(git status:*), Bash(git log:*), Bash(git diff:*), Bash(node claude-sync.js:*), Bash(node claude-bow.js:*)
---

## Context

- Git status: !`git status --short`
- Uncommitted changes: !`git diff --stat HEAD`
- Today's commits: !`git log --oneline --since="24 hours ago"`
- Current permit: !`node claude-sync.js read 2>/dev/null | head -20`
- BOW state: !`node claude-bow.js summary 2>/dev/null`

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

### STEP 1B — BOW reconciliation

Look at the BOW state above against what happened this session:

- Items worked on this session: is their status honest (`in_progress` / `done`), and does every completed item carry its git ref (`node claude-bow.js ref <CODE> <hash>`)?
- Work discovered but not done: does it have a BOW item yet? If not, add one now (`node claude-bow.js add ...`) — next session's checkin summary is how it gets remembered.
- Fix any drift before checkout; mention the BOW top item in the sign-off's "Next session" line.

---

### STEP 2 — Write session summary to Vestige

Use `mcp__vestige__smart_ingest` to write a session summary. Include:

- Date and identity (bill> session YYYY-MM-DD)
- Commits made this session (from the git log above)
- Key decisions or architectural changes
- Any gotchas discovered
- Work left incomplete / what to pick up next session
- Any Golden Rule violations (if any)

Tag with: `metropolis-dev`, `session-summary`, current date

---

### STEP 3 — Claude-sync checkout

Run the checkout to release the permit:

```
node claude-sync.js checkout --session $CLAUDE_SESSION_ID
```

If `$CLAUDE_SESSION_ID` is not set, check `node claude-sync.js read` for the active Bill session ID and use that.

---

### STEP 4 — Update MEMORY.md

If the current version number has changed, or any architectural gotcha was discovered this session, update the project memory at `C:\Users\aarongarcia\.claude\projects\E--git-Metropolis\memory\` (add/update the relevant memory file and its `MEMORY.md` index line).

---

### STEP 4B — Memory hygiene quick-check (added v3.1.7)

Light-touch reminder, NOT a blocking gate. Surfaces obvious issues without forcing fixes.

Search Vestige for memories with timestamp-anchored "current X" facts that may be stale:

```
mcp__vestige__search query="metropolis current version as of"
mcp__vestige__search query="metropolis dev project state"
```

If results contain stale "current state" claims (superseded versions, setup details that have since changed), flag in the sign-off:

```
⚠️  N stale "current state" memories detected in Vestige. Consider running
    /memory-hygiene next session to clean these up. (Not blocking sign-off.)
```

Also watch for cross-contamination: results tagged `papers_bow` about the 18/22-Metropolis papers are a DIFFERENT project — never "fix" those from here.

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
