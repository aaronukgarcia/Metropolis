# Morning report, 2026-11-19

Written by the lead at 06:35. Everything below is on `origin/main` unless it says otherwise.

## Where things stand

Main is at `d649413`. CI on that commit was still running when this was written; the three runs before it failed only on the start-up race tests that `d649413` fixes. Check with `gh run list --branch main --limit 1`.

## Landed overnight

| Commit | What | Item |
|---|---|---|
| `ec4970c` | A hosted city is checked against the rule versions this build supports before it is restored; `tools/journalscan` reports the same for a journal folder | FEAT-9995385620 |
| `8b5871b` | The three candidate fixes for the migration stall are data switches; play is unchanged | BUG-1243 (open) |
| `5e6d5e4` | A hosted-server test no longer panics its package on a slow machine | BUG-1252 |
| `0028ad4` | Roads can be ordered in the engine as rule version 2; fixtures moved to short paths | FEAT-9995385628, BUG-1250 |
| `d649413` | Frame tests wait for real signals; this is what kept CI red | BUG-1239 |

Earlier in the night: the money format (`caaff08`), the menu engine (`6d109c1`), the duplicate-build refusal as rule 1 (`785bc70`), the kit files (`181830a`).

## What you can and cannot see in the game yet

- Money reads as whole pounds with commas.
- A second build on an occupied cell is refused and costs nothing.
- Roads: the engine accepts them, but there is no key for them in the terminal yet (lane 1b).
- Help, save, load and debug capture: built and accepted in review, being ported onto the new main; not in the game yet.
- The fly-out menu: the engine is in; it is not wired into the game yet.
- Migration still stalls at about 1,400 people until you choose an option.

## In progress

- Help/save/load port in `.claude/worktrees/hsport`. It then needs a review of the port and the landing gates.

## Decisions for you

1. **Migration stall (BUG-1243):** A (hold both readings at neutral, as you first ruled; early growth about six times slower), B (lower the world baseline), or C (floor the readings at the baseline; same early game as today, no stall). The lead recommends C. Each is a one-line edit in `data/attract_terms.json`.
2. **Roads:** six questions in `docs/planning/acceptance/feat-9995385608-roads-terminal.md`. Assumed for now: a home may share a cell with a road; a road costs 750 pounds and takes 5 days; roads are allowed on water.
3. **Saves:** they default to a `saves` folder under wherever the game is launched from. Say if they should live somewhere fixed.
4. **Playtest:** needs a cost forecast and your Authorise before any session runs.
5. **Secret guard:** the roads commit added an allow entry for the v2 fixture's path string, which sits in production source (`internal/engine/rules/registry.go`). Your standing approval covered tests and fixtures. It follows the existing v1 entry; say if you want it done differently.
6. **Old worktrees and scratch:** about fifteen finished worktrees under `.claude/worktrees` and scratch under `temp/` need your deletion token to remove.

## Things that went wrong

- CI was red from the money push at 00:13 until the race fix. The cause was tests guessing when start-up had finished; a different test failed each run. The fix took three review rounds: the first two each found a way a test helper could hang the whole package.
- The usage limit stopped three agents at about 01:30. Resuming them failed (their budget guards counted the wait), so fresh agents were started.
- One landing gate hit a panic in an old hosted-server test under load (BUG-1252, fixed).

## Next, in order

1. See CI green on `d649413`.
2. Land help/save/load.
3. Roads lane 1b (the key), menu wiring, the welcome screen, then the playtest driver.
4. Publish the F5 view from the engine (lane A2).
5. Run `tools/journalscan` on a copy of the pix journal and ask SAFE to redeploy.
