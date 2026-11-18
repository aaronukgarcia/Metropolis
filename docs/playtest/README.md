# Playtest folder

This folder holds the cold-start playtest for the terminal game: player sessions,
the persona briefs they play from, the log template, and the feedback register.
The plan is docs/planning/acceptance/feat-9995385623-playability-module.md
(Book of Work item FEAT-9995385623).

## Layout

- `personas/`: one brief per player. Each file has the same shared block, then
  "About you" and "How you play". Players are given only the text of their brief.
- `templates/session-log-template.md`: the shape of a session log, with one worked step and the review and SUMMARY lines.
- `sessions/`: one log per session, written by the tool (created by the runner, not by hand).
  Logs are append-only evidence. Nobody edits a log, including the lead.
- `register.md`: the feedback register. One row per distinct finding. The lead fills it in after each round.
- `README.md`: this page.

## The eight players

| Name | Persona file | Type | Group |
|---|---|---|---|
| Darren Cole | destructive.md | Destructive: looks for the way to remove, break or ruin things | Newcomer |
| Maya Osei | random.md | Random: presses keys with no plan (the control) | Newcomer |
| Priya Nair | tries-everything.md | Tries everything: counts and collects every part | Newcomer |
| Walter Hughes | slow.md | Slow: reads first, builds a little, checks | Newcomer |
| Callum Reid | reckless.md | Reckless: spends at once, wants speed | Newcomer |
| Sam Okafor | game-developer.md | Game developer: maps the structure and the loop | Professional |
| Jo Lindqvist | game-tester.md | Professional game tester: checklists and boundaries | Professional |
| Dr Amara Bello | city-planner.md | Real-life expert city planner: tests whether it behaves like a place | Professional |

The five newcomers are Aaron's five types. The three professionals were added by
Aaron on 2026-11-18.

## Rules that hold for every session

- Each session starts cold. The player knows only what the screen shows.
- Each session is audited afterwards. A contaminated session is excluded from the register and listed there with the reason.
- The player does not change the game. The lead does, after the register's priority step.
