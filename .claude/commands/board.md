---
description: Agent status board with mandatory never-idle enforcement — never report 0 agents running without either dispatching the next ready BOW item or stating explicitly why none is available
allowed-tools: Bash(node:*), Agent
---

## Context

- Live coordination state: !`node claude-sync.js read`

## Your task

Print the compact agent status board (agent/ID | BOW item | ~15-word status, DONE/DEAD shown once then dropped) using the live agent list, not memory.

**MANDATORY never-idle check — this is the part that was missed before, do not skip it:**

If the board would show **AGENTS: 0 running**, you MUST NOT just print the empty board and stop. Before printing:

1. Run `node claude-bow.js list --by-seq` and find the next OPEN item with no unmet dependency (`⛓` marker absent, or all listed deps are `done`), highest priority first.
2. If one exists: dispatch it right now (BA/dev/Tester/Destructive lane per the dev-team pipeline) via the Agent tool, THEN print the board showing that new lane.
3. If genuinely none exists (every ready item is blocked, or the whole tracked wave/arc is closed with nothing left to pull): print the board as 0 running, but the accompanying text MUST say so explicitly and name what's blocking (registration pending, Aaron's ruling needed, etc.) — "no agents running" alone is not an acceptable terminal state description under the standing never-hold policy.

Also check: are there any Destructively-ACCEPTed items still sitting at BOW `status=open` instead of `done`? (`node claude-bow.js verdict <code>` shows ACCEPT but the item was never closed.) If you find any, close them with `node claude-bow.js done <code> --note "..."` before reporting the board — this was the exact gap that let 9 accepted items sit open for a whole session.

No prose before the table beyond the never-idle dispatch note (if triggered); the board itself stays exactly as before.
