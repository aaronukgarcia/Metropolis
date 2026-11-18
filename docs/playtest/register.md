# Playtest feedback register

Findings from the cold-start playtest (FEAT-9995385623). No findings yet.

## What this register is, and what it is not

- Players and the playtest tool change nothing in the game. Only the lead does,
  afterwards, after setting each finding's priority against the Baseline One goals.
- The clean-room limit: the player model already knows what games are, so the
  persona only makes it act as if it knows nothing. Every session is audited, and
  contaminated sessions are excluded. Excluded sessions are listed in the table
  at the foot of this file, with the reason.
- Eight personas in two groups. Five newcomers, Aaron's five types: destructive,
  random, tries everything, slow, reckless. Three professionals, added by Aaron on
  2026-11-18: game developer, professional game tester, real-life expert city planner.
  The three professionals know their own field and have never seen this game.
- Side channel: a player may ask the lead a question. The question is itself a
  finding. Any answer that carries game knowledge marks the rest of that session as
  assisted. Cold and assisted evidence are never mixed.

## Baseline One goals (the priority is weighed against these)

Copied from the plan (docs/planning/acceptance/feat-9995385623-playability-module.md, section 6.2):

1. The terminal game on the Go engine is the game.
2. All ten migration levers move migration on screen, in this order: housing, jobs, services, health care, policy, shops, parks, transport links, congestion, tourism.
3. A new city starts empty, with a start hint.
4. Homes stack as density.
5. Homelessness is absorbed by emergency accommodation and shown on a meter.
6. Saves record their rule version and replay under it.
7. Game work first; hosting work only in spare lanes.

Priority rubric (proposed in the plan, question 8; not yet ruled by Aaron):
P0 = a cold player cannot start, or can destroy a save or the game, or the game gives false information about a goal item.
P1 = a player can only proceed by guessing or by luck, on a goal item.
P2 = confusion or frustration with a way round.
P3 = polish.
A finding that bears on no goal is `noted, no action` unless it hit three or more sessions in one round (then the lead decides again).

## Columns

| Column | Content |
|---|---|
| ID | `PT-001`, `PT-002`... never reused |
| Finding | one sentence, plain |
| Type | one of: `missing-info` (the screen does not say), `wrong-info` (it says something untrue), `bug` (error or crash), `cannot-do` (action does nothing; check BUG-1237 first), `missing-feature`, `pace`, `fun`, `other` |
| Seen in | session ids and step numbers, for example `r0-slow-01#007, r0-random-01#012`. Count of sessions out of the round's five |
| Goal | the Baseline One goal it bears on (number above) or `none` |
| Priority | P0 to P3, set by the lead, or `-` with the reason in Decision |
| Decision | `BOW <code>` (filed), `help text changed <commit>`, `noted, no action: <reason>`, or `waits for <item>` |
| Date | date decided |

## Findings

| ID | Finding | Type | Seen in | Goal | Priority | Decision | Date |
|---|---|---|---|---|---|---|---|
| (none yet) | | | | | | | |

## Discarded sessions

Contaminated and off-persona sessions are kept as logs but listed here, with why.

| Session | Reason | Date |
|---|---|---|
| (none yet) | | |
