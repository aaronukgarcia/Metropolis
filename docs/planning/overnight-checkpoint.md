# Overnight run checkpoint (2026-11-17 night)

The lead (Bev) rewrites this file at every landing and every dispatch burst.
If the session stalls (token limit, crash, compaction), a resumed lead reads
this file, the BOW comments on each item, `git log -10`, `gh run list
--branch main --limit 3` and `git worktree list`, and carries on. It never
redoes committed work and never trusts recall over those four sources.

## Aaron's order (22:15)

"do all with fan out and low cost agents which you must orchestrate for
quality, I want lots of progress made against all the outstanding bow items".
Checkpoints so a token stall waits and resumes; auto restart.

## Rules for the night

- Sonnet builds and small reviews, Haiku mechanical docs. Opus reviews only
  for money and data-loss items (meter, R2 store, reconnect).
- Land only on a recorded ACCEPT and a green CI run. CI red = stop landing,
  fix first.
- No spend: no R2, Cloudflare, Azure, AWS calls. pix: SAFE deploys; this
  session only asks SAFE. Load nothing big on pix until SAFE confirms the
  WSL memory raise.
- No deletes outside the project temp folder (the 22:19 deletion token is
  spent; new worktrees wait for Aaron).
- Every Go brief lists: astgate live-tree, the FULL touched packages,
  go vet ./..., scratch inside E:\git\Metropolis\temp only, commands run
  from the worktree root (never cd into a subfolder of the main checkout:
  the hooks resolve from the repo root).
- At most two heavy Go -race suites at once; about four code lanes.
- Plain `git add` then plain `git commit -m` in separate calls; no trailers.
- Never /tmp in Bash (it maps to another project's temp, SAFE-571); scratch in the repo temp only.
- LOAD: with full-compose runs timing out at 30 m, keep to THREE heavy Go lanes; hold new Go rounds until one finishes.

## Landed and CI-green before the night started

BUG-1204 2431b40, BUG-1206 6381d4f, BUG-1197 2fa4531, BUG-1205 781e605.
pix: metroserve container up (FEAT-9995385600), SAFE owns deploys, port 8120.

## Priority change (23:20, after the cold assessment)

docs/planning/assessment.docx says: drifting, not lost. Baseline One (FEAT-083)
legs 2-4 are open with no evidence since 30 October. So: Baseline One gaps and
game bugs go FIRST in every free lane; Aaron's order for the offload builds
still stands and they run in the lanes that are left.

## Landed tonight

Pushed and CI-green through ef61b1d: BUG-1209, 1194, 1190, 1192, 1191, 1200, 1212, 1211, 1136, 767 + docs.
Pushed, CI running since 01:25 (ca395e4): BUG-1210 748c427, deploy files 6784c41, BUG-1215 9001e9b, BUG-1214 leg 4 ca395e4.
COMMITTED LOCALLY, NOT PUSHED: BUG-1213 leg 3 (r2 ACCEPT) - push when the ca395e4 run is green.
Closed as already fixed: BUG-854 (8b173d0). pix redeployed by SAFE at ef61b1d; tick continues.

## Baseline One (FEAT-083)

Leg 2 MET on screen. Leg 4 pushed (CI pending). Leg 3 committed locally. NOT marked met:
after CI, re-run the evidence capture on main (legs 2-4), commit the frames, comment FEAT-083; Aaron signs off.

## In flight (update as they report)

| Item | Stage | Where |
|---|---|---|
| CI on ca395e4 | running; leg 4 changes default population - watch the perf/population probes | |
| BUG-1208 sealed page store | Opus round r1 running (long test runs) | estate .claude/worktrees/agent-a8bc01e3c389428e3 |
| BUG-1140 converge zero-evaluation | r1 REJECT, r2 built (per-field counts, H510-H512), round r2 running | estate .claude/worktrees/agent-a7e4f9fa8eab25d4d |
| BUG-1143 save/load employment | investigate + fix running | isolated worktree |
| BUG-1178 page identity atomic write | build running (step 0: is it already fixed?) | isolated worktree |
| BUG-1217 three counts of homes | options paper being written for Aaron (docs/planning/bug-1217-one-count-of-homes.md); do NOT build | main checkout, docs only |
| FEAT-9995385596 meter | r2 built; HELD for an Opus round r2 | .claude/worktrees/agent-afac334e274decd2f |
| FEAT-9995385598 workers part 1 | state UNKNOWN; needs a fresh finisher that reads the worktree first | .claude/worktrees/agent-a59928c4840009f1f |
| BUG-1193 resolver cwd-first | r1 REJECT, probably a false alarm; recheck then land | estate .claude/worktrees/agent-a65bb1f0a0538394d |

## Queue, in order (THREE heavy Go lanes at most; compose in slices with -skip BUG799; every lane checks first that its bug still exists)

1. CI green on ca395e4 -> push leg 3 -> watch CI -> evidence capture on main -> ask SAFE for a redeploy.
2. Land BUG-1208 and BUG-1140 on ACCEPT (BUG-1208 needs compose.go + accepted-findings.json 3-way merges).
3. Game: BUG-782 (migration + council tax ignore employment - the next migration link, jobs), BUG-1141 (ledger observer), FEAT-2326609746 (zone tax wiring), BUG-1218 (F2 follow-ups), BUG-1216 (deterministic hook for the BUG-799 race tests).
4. Close the remaining verified-done P1 items from docs/planning/backlog-comb/p1-done-verification.md.
5. Meter Opus round r2; workers fresh finisher; BUG-1193 recheck.
6. Offload builds in remaining lanes: reconnect FEAT-9995385597, inc2a FEAT-9995385594 after the meter lands, hibernation after that.
7. Replace docs/planning/assessment.docx with assessment-v2.docx when Word releases it.
8. BUG-1202, BUG-1203, BUG-1207, BUG-1199.

## Parked for Aaron (morning)

- 103 dirty worktrees were kept (uncommitted content; not forced). 32 clean
  ones were removed under token 20261117-221954-de3c67.
- Hibernation ASM-H6 (store retries run inside the stop, city not playable
  for up to ~70 s) and ASM-H7 (pause counts through the no-command clock).
- GGR95 delete reports went to SAFE's fallback log (bus refused the text).
- Meter: a ~5-minute store failure now hibernates every city on a metered host (per the lead's fail-closed ruling) - confirm.
- BUG-1214 changes the default game: a city where the player builds nothing levels off at 148 (was 244) because homes run out; seed home capacity 120 is a balance placeholder (ASM-1644).
- Default city loses about 550k a month to an outflow shown as 'Coastal/other' (balance flag); BUG-1213 r2 is tracing what it really is.
- BUG-1200 edge: Esc closes a confirm sitting UNDER a blocking popup (z-order), not the visible popup. Not filed.
- pix EXPOSURE: metroserve has no login; whether other LAN devices can reach port 8120 is NOT proven blocked (SAFE-572, P1: depends on the Hyper-V firewall default action; SAFE is testing). Only the dogfood city is on it. Nothing sensitive goes on pix until SAFE-572 is proven or the Cloudflare front door (FEAT-9995385599) lands.
- Windows Defender may be what makes file-heavy tests collapse on this machine (BUG-1215, unproven); an exclusion for the project temp folder is Aaron's call.
- forge upgrade --apply would make every hook path absolute (prevents the cwd hook failure) and update 8 kit assets: needs Aaron's yes.
