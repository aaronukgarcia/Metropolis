---
description: Never idle — check lane utilisation and dispatch ready work so tokens are never wasted polling (Aaron's use-it-or-lose-it rule, 2026-08-31)
---

# /loadup — keep every lane working, never sit idle

Aaron pays for tokens on a use-it-or-lose-it basis. Idle polling wastes his money. The build is essentially **never 100% blocked** — only specific loops may be gated on a ruling. This skill makes the standing loop tick a WORK tick.

Run this whenever you would otherwise "hold" or idle — and automatically fold it into every `/loop` sync-read tick.

## Steps

1. **Utilisation check.** Count running lanes (Agent tasks in flight). If ≥4 are running and healthy, you're loaded up — do the sync read and stop. If <4, continue.

2. **Find ready work, in northstar priority order** (do NOT dispatch P5 distractions — northstar §3):
   - `node claude-bow.js list --by-seq` (from repo root) — open P0/P1/P2 items whose dependencies are met.
   - Spec'd increments ready to build (acceptance docs on trunk, rulings recorded on the item).
   - Verdict-pending estates awaiting a destructive round.
   - Follow-up bugs and test-rigor debt filed this session.
   - Cold-audit sweeps of recent landings.

3. **Name the true blocker, per item.** "Blocked" means a SPECIFIC item needs a SPECIFIC Aaron ruling. If a headline item (e.g. the finance loop) is gated, dispatch everything that does NOT depend on that ruling. Never say "the board waits on you" as a blanket — that's the stopping-short failure ([[metropolis-never-idle-load-up]]).

4. **If Aaron is present and rulings are pending: interview him NOW** via AskUserQuestion (batch the questions). Never sit on pending questions for a later session — ask the moment he's reachable.

5. **Dispatch.** Fire the ready lanes (Haiku for BA/docs/simple; Sonnet for trap-path/money/framework/game-logic — see [[metropolis-haiku-dev-agents]]). Respect the one-webconsole-code-lane funnel where files collide (northstar §3); never run concurrent lanes on the SAME shared file ([[metropolis-subagent-git-guard-hole]]). Each build lane's brief carries the mandatory countermeasures (real handlers, RED self-proofs, on-disk verification).

6. **Then** do the sync read and end the turn. Each dispatched lane feeds the verdict → commit → push pipeline as it reports.

## The bar

Ending a turn with <4 lanes running AND ready work undispatched AND no per-item blocker named = a wasted tick. Don't.
