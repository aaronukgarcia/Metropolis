# Morning report, 2026-11-18 (overnight run, 22:15 to 08:30)

Written by the lead (Bev) at 08:45. The live state table is
`docs/planning/overnight-checkpoint.md`; this is the summary.

## Do this first

A stale git lock has blocked every commit in the main checkout since 03:20:
`E:\git\Metropolis\.git\index.lock` (zero bytes, written 02:27, no git process
running). The lead's delete was denied by the permission system and was not
worked around. From the repo root, in the Claude prompt:

    ! rm .git/index.lock

Seven accepted pieces of work are waiting behind it.

## Landed and pushed (CI green through 6dcfa4c)

BUG-1204, 1206, 1197, 1205 (before the night), then BUG-1209, 1194, 1190,
1192, 1191, 1200, 1212, 1211 (+1096), 1136, 767, 1210, 1215, 1214 (leg 4
engine rule), 1213 (leg 3 Profit and Loss), 1140 comparison half, the
BUG-1143 regression test, the pix deploy files and the planning documents.
pix was redeployed by SAFE at 01946d2.

Three CI reds during the night, each fixed: the astgate findings (BUG-1206),
a socket hard-close (BUG-1212), and an old webconsole assertion broken by
BUG-1194 because the lead skipped the full webconsole run.

## Accepted, not landed (land in this order once the lock is cleared)

1. BUG-1140 convergence run side (r5 ACCEPT). Already copied into the main
   working tree, unstaged.
2. BUG-1208 sealed page store (Opus ACCEPT). Fixes the frequent
   "citizen id already exists" test failure on main.
3. BUG-1222 F2 Profit and Loss readable at 80x24 (r2 ACCEPT).
4. BUG-1220 on-screen cues: homes line, HOMES FULL cue, screen list and key
   hints (r1 + r2 ACCEPT).
5. BUG-1178 page-folder identity and claim order (r2 ACCEPT). Lands after
   BUG-1208; rerun the metroserve race slice with both in place.
6. BUG-1225 dropped connections leak subscriptions, and every city restore
   re-created dead ones (Opus judged the code, r4 ACCEPT). The server on pix
   has this today: ask SAFE to redeploy after it lands.
7. FEAT-9995385597 increment 1, browser reconnect and the three protocol
   messages (Opus r5 ACCEPT after four rejects). Rerun the full webconsole
   set in a gate worktree before the commit.

Per-item file lists and merge notes are in the checkpoint's LAND-READY QUEUE.
After items 3 and 4: capture legs 3 and 4 at 80x24 (draw a frame first, move
the cursor between builds).

## Built, held for a ruling

BUG-1224 (P0): a build order on a cell that already has a queued or standing
structure is accepted and charged and builds nothing (five presses of `b` on
one cell cost 75,000 for one dwelling). The fix (refuse at submit) is built
and compiles. Not reviewed, because the ruling changes the code: journals
recorded before the fix can contain accepted duplicates, and replay stops at
the first refused command.

- Tolerate the refusal on replay: old cities restore with MORE money than
  they had (one build charged, not five).
- Gate the rule on a journal version (lead's recommendation).
- Accept the break.

Also yours: should homes stack on one cell instead? Nobody has checked
whether a real journal (the dogfood city on pix) holds a duplicate.

## Rejected during the night, and why (all reworked unless stated)

- BUG-1213 r1: an invented figure after a load, mixed viewpoints, no net line.
- BUG-1208 r1: a closed store read was treated as an empty shard.
- BUG-1140 r1, r3, r4: global check only; null, duplicate keys, unbounded
  days; allow-lists not pinned.
- BUG-1222 r1: one signed fold line under "outflows" misled on the direction
  of money.
- BUG-1178 r1/r2 build: claim-first changed an operator-visible refusal code;
  an old attack test caught it.
- BUG-1225 r1, r2, r3: a queued subscribe applied after the cleanup; cleanup
  by "the city's latest owner"; then a test gap on the replay skip.
- Reconnect r1 to r4: each time one more path left a command promise
  unsettled; r4 was restructured (one settle function, one exit function, a
  deadline on every command).
- FEAT-9995385596 meter r1 (earlier): reworked; now needs rebuilding on top
  of the land queue before its Opus review. Not started.

## Baseline One (FEAT-083): where it really stands

Correction to what the lead reported at about 02:00: the re-capture ran at
140x40, not 80x24. Nothing on main proves any leg at the standard terminal
size. Items 3 and 4 above are what make legs 3 and 4 visible at 80x24.

- Leg 2 (build): works on screen at a large terminal.
- Leg 3 (consumption on a named surface): F2 shows it; cut off at 80x24 until
  BUG-1222 lands.
- Leg 4 (migration responds): the engine rule is sound (control 148, one home
  156, five homes 184, six homes 190 at month 12). The on-screen cue is in
  BUG-1220. The earlier "five dwellings gave only +8" was the scripted run
  putting five builds on one cell, which is BUG-1224.
- Leg 1 as written no longer reproduces: money falls from the first month
  (1.5M to about 973k at tick 30, about 211k at tick 90) because the coastal
  charges post first. No evidence past tick 90.
- Legs are NOT marked met on the Book of Work. Sign-off is yours:
  `docs/planning/acceptance/feat-083-criteria.md` has the five-minute
  walk-through and six questions.

## Backlog comb

122 "probably done" items were checked against the code; 65 were closed after
a spot-check. 14 turned out to be built but never reached at runtime,
including five game modules (leisure, destination, tunnels, spaceport,
accelerator). Verification files: `docs/planning/backlog-comb/
p1-done-verification.md`, `p2-bug-done-verification.md`,
`p2-feat-done-verification.md`, `p3-asm-done-verification.md`.
New criteria documents: `docs/planning/acceptance/feat-083-criteria.md`,
`bug-1168-criteria.md`, `bug-763-criteria.md`, `feat-031-criteria.md`.
Held for you: five assumptions that close on your recorded rulings
(ASM-1552, 1577, 1578, 1579, 579); FEAT-230, FEAT-1972079852,
FEAT-2326609823.

## New items filed

BUG-1222 (landed queue), BUG-1223 P1 (pause, speed and quit keys cut off the
80-column status row), BUG-1224 P0 (held, above), BUG-1225 P1 (landed
queue), BUG-1226 P2 (a subscription's first delta can be evicted, view stays
silent), BUG-1227 P2 (a command on a half-open live connection pends
forever; pre-existing).

## Questions parked for you

1. BUG-1224 replay ruling, and stacking (above).
2. Baseline One sign-off and the six questions in feat-083-criteria.md,
   including whether 1.5M falling to 211k in three months is the intended
   start (coastal charges).
3. BUG-1217 one count of homes: options paper, seven questions.
4. BUG-782 migration and jobs: rulings R1 to R7.
5. Reconnect assumptions: command deadline 120 s, wake deadline 120 s,
   reconnect-after clamp 60 s.
6. BUG-1168 (rebuild overwrites the city with no way back), BUG-763 and
   FEAT-031 (the terminal game cannot cycle map overlays at all): questions
   in their criteria documents.
7. Balance placeholders: 120 seed homes (ASM-1644), two people per migrant
   household (ASM-1645).
8. pix: metroserve has no login and exposure is not proven closed
   (SAFE-572). Nothing sensitive goes there until it is.
9. Meter: a five-minute store failure hibernates every city on a metered
   host (per the lead's fail-closed ruling): confirm.
10. Offload design questions: inc2b ICD OQ-2/3/4, launcher OQ-1 to 14,
    hibernation ASM-H6/H7.
11. Machine: Windows Defender exclusion for the project temp folder
    (BUG-1215, unproven cause of slow file tests); `forge upgrade --apply`
    (absolute hook paths); 103 dirty worktrees kept.

## Not done

- Nothing after 6dcfa4c is committed. The comb verification files, the four
  criteria documents, this report and the checkpoint are uncommitted.
- The meter rebuild and Opus review; workers part 1; BUG-1193 recheck;
  BUG-1223, 1218, 1219, 1216, 1221 (all wait for the land queue: same files).
- Criteria documents for FEAT-141 (second version) and FEAT-9995385585.
- `docs/planning/assessment.docx` still holds the interim figures; replace
  it with `assessment-v2.docx` when Word releases the file.
- Scratch left in place: review worktrees `.claude/worktrees/round-*`, lane
  worktrees `.claude/worktrees/agent-*`, and probe files under `temp/round-*`
  and `temp/lead-gates` (all inside the project).
