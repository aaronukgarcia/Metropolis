# Session log template

This is the shape of a session log. The tool writes the header, the step labels
and the end line. The player writes the text in SEES, THINKS, RESULT and the review.
Nobody edits a log after it is written. Each entry carries the hash of the one before.

## Header (written by the tool)

```
# Session 2026-11-19-r0-slow-01
persona: slow (Walter Hughes)   prompt-sha256: <hash>
build: metropolis v0.x (abc1234, main, built 2026-11-19T09:12Z, host ...)   (from -version)
date: 2026-11-19   size: 120x30   game-mode: real
budget: 60 steps, 20 min, 150000 tokens
started: 2026-11-19T10:02:11Z   save-dir: empty at start
```

## One worked step (written by the tool and the player)

The game in this example is made up: "Pond Keeper", a fishing game. It is not a
city game. The header line is the tool's; the lines below it are the log step.

```
### 003 | clock 00:07:40 | phase 2
PHASE: 2
SEES: A list of three ponds, each with a number after it. A line at the bottom says "Press a letter to fish."
THINKS: The numbers look like fish counts. I will press the letter next to the first pond to see if it fishes there.
TRIES: <F2>, a
RESULT: Nothing happened. The line at the bottom now says "Choose a pond first." The number after the first pond did not change.
TAG: cannot-do
prev-hash: 9c1e0b7a4f2d...
```

Note the order: PHASE, SEES, THINKS, TRIES, RESULT, TAG. The tool writes TRIES
and the step number. The player writes the other four.

## Review (the player writes this after the tool reports the session has ended)

```
## Review
END: gave-up | budget-steps | budget-minutes | budget-tokens | game-closed | too-small
MANAGED: (what the player achieved, in words)
COULD NOT WORK OUT: (list)
1 FIRST-TIME EXPERIENCE AND ONBOARDING: score 1 to 10 (10 best) - what welcomed me, what taught me, the exact moments I felt lost or wished the game had told me something (cite step numbers)
2 BROKEN FEATURES AND DEAD CONTROLS: one line each: the key or screen, what the label on screen promised, what happened, EXACT KEYS to reproduce (a key string the tool can replay), step number
3 SIMULATION AND MONEY ANOMALIES: numbers that did not add up, with the step numbers where both figures show
4 FRUSTRATIONS AND FRICTION: things that are not broken but annoyed me
5 MOMENTS OF DELIGHT: what felt good, with step numbers
6 TOP FIVE FIXES, ranked by harm to a new player (1 = worst), one line each
ERROR TEXTS SEEN (verbatim): (each in quotes, one per line; the tool checks each appears on a screen)
RATINGS (1 to 5, 5 is best), each with one sentence:
  understood what to do: n  - why
  could do what I wanted: n  - why
  felt in control: n  - why
  entertainment: n  - why
  would play again: n  - why
```

The END line is written by the tool from how the session ended. The player does not write it.

## SUMMARY line (written by the tool at the end of the log)

One line, machine-readable. The tool fills in the driver-measured fields (steps,
minutes, end, ticks, months, screens, phase_steps, no_effect_sends). The player's
ratings, tags and counts come from the log. `tools/playtest/summarise.mjs` reads this line.

```
SUMMARY {"v":1,"id":"2026-11-19-r0-slow-01","persona":"slow","build":"<commit>","steps":41,"minutes":13.2,"end":"gave-up","ticks":402,"months":13,"screens":4,"tags":{"progress":6,"confusion":9,"frustration":3,"error":1,"cannot-do":7,"delight":1,"boredom":2},"ftue":4,"ratings":{"understood":2,"could":2,"control":3,"fun":2,"again":2},"phase_steps":[5,19,14,3],"dead_controls":6,"top_fixes":["welcome","dead keys","hint row"],"errors_seen":1,"no_effect_sends":8,"audit":"CLEAN","off_persona":false,"under_reported":false}
```
