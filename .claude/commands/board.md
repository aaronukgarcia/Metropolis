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
2. If a ready item exists but has NO acceptance-criteria doc yet (check `docs/planning/acceptance/<mkey>.md`), the default action is to dispatch a BA (Business Analyst) agent to write it — per the pipeline, BA criteria must exist before dev dispatch, so an idle tick with un-BA'd ready work should never sit idle waiting for a human to say "write the criteria." Dispatch the BA lane right now via the Agent tool, THEN print the board showing that new lane. Multiple ready items with no criteria may each get their own BA agent in parallel (one per item) rather than dispatching just one at a time, since BA work is cheap and independent per item.
3. If a ready item already HAS acceptance criteria: dispatch the next real lane (dev/Tester/Destructive) per the dev-team pipeline, same as before.
4. If genuinely none exists (every ready item is blocked, or the whole tracked wave/arc is closed with nothing left to pull): print the board as 0 running, but the accompanying text MUST say so explicitly and name what's blocking (registration pending, Aaron's ruling needed, etc.) — "no agents running" alone is not an acceptable terminal state description under the standing never-hold policy.

**BA-on-idle is the default, not an exception.** A tick that reports 0 running twice in a row without either dispatching a BA for an un-BA'd ready item or dispatching real dev/Tester/Destructive work on an already-BA'd item is a bug in following this skill, not an acceptable steady state — there is essentially always a next module/feature in the BOW that lacks acceptance criteria, so "nothing to do" should be rare.

Also check: are there any Destructively-ACCEPTed items still sitting at BOW `status=open` instead of `done`? (`node claude-bow.js verdict <code>` shows ACCEPT but the item was never closed.) If you find any, close them with `node claude-bow.js done <code> --note "..."` before reporting the board — this was the exact gap that let 9 accepted items sit open for a whole session.

No prose before the table beyond the never-idle dispatch note (if triggered); the board itself stays exactly as before.
