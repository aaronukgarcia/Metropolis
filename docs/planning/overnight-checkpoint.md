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

BUG-1209 663dc0e, docs 15eeaab, BUG-1194 fdd06c0, BUG-1190 99cc157,
BUG-1192 d99d4a4, BUG-1191 387d19b.

## CI IS RED (23:30) - land nothing until BUG-1212 is fixed and green

Run 44192415055 (15eeaab) failed: wsserver TestCommandFailures send_failure got
a TCP abort instead of the 1001 close. Cause: BUG-1209's watcher hard-closes
right after writing the close frame. BUG-1212 (P0) fix lane running: graceful
close (read deadline, let the handshake finish). Four webconsole commits were
pushed on top before the red was seen; they do not touch Go.
When BUG-1212 reports: Sonnet round, land, watch CI to green, THEN resume landing.

## Baseline One (FEAT-083) evidence run result

Leg 2 MET on screen (build completes at tick 60, 15,000.00 leaves the treasury).
Leg 3 PARTIAL: spend posts each month but F2 PROFIT & LOSS says unavailable -> BUG-1213.
Leg 4 GAP: population identical with and without builds or policies -> BUG-1214.
Evidence files + two evidence tests sit in .claude/worktrees/agent-aaec4b9f1792dc788
(docs/planning/evidence/feat-083/, cmd/metropolis and compose feat083_evidence_test.go):
port and commit them under [FEAT-083] once CI is green.

## In flight (update as they report)

| Item | Stage | Where |
|---|---|---|
| BUG-1212 CI red (P0) | fix lane running | isolated worktree |
| BUG-1213 Baseline One leg 3 (P&L on F2) | build running | .claude/worktrees/agent-a4a2b532e45f4e7b2; brief temp/briefs/bug1213-leg3.md |
| BUG-1214 Baseline One leg 4 (housing vacancy feeds attract) | build running | .claude/worktrees/agent-a76b3407e3912a9c3; brief temp/briefs/bug1214-leg4.md |
| BUG-1211 health tick (= BUG-1096) | round r1 ACCEPT (no caller computes a rate from the counter); land after CI is green, then close BUG-1096 too and ask SAFE to redeploy pix | estate .claude/worktrees/agent-ac624e2d316ee6350 |
| BUG-1200 confirm Esc | r2 ACCEPT; COMMITTED LOCALLY, not pushed (push with the BUG-1212 fix once CI can go green) | .claude/worktrees/agent-a5cc590b4a9369adb |
| BUG-1136 test infra (astgate ceiling) | ACCEPT; land gate.go + gate_test.go only, after CI is green | estate .claude/worktrees/agent-a65bb1f0a0538394d |
| BUG-1193 resolver cwd-first | round r1 REJECT (a metroserve paging test times out with the change); needs an r2 lane: diagnose (resolver on a hot path? cache it), rerun metroserve -race x3; queued behind Baseline One for load | estate .claude/worktrees/agent-a65bb1f0a0538394d |
| FEAT-9995385596 meter | r2 rework BUILT; HELD for an Opus round r2 until load drops (compose suite timing out at 30 m under load); the round must run full compose + metroserve -race | .claude/worktrees/agent-afac334e274decd2f |
| FEAT-9995385598 workers part 1 | finisher running | .claude/worktrees/agent-a59928c4840009f1f |
| deploy/compose.yml + .dockerignore | uncommitted; needs a round (mkey FEAT-9995385600) | main checkout |

## Queue, in order (start as lanes free up)

1. BUG-1212: round, land, CI green. Nothing else lands first.
1b. BUG-1208 (now P1): TestBug1197_AbandonedSigtermReleasesPagingClaim flakes under -race ('citizen id N already exists (cold)') - the abandoned build still pages after the claim is released. Dispatch a fix lane as the next Go lane (see the BOW comment).
2. Baseline One: round + land BUG-1213 and BUG-1214 as they report (BUG-1214
   will move golden pins and converge bounds - Opus round, it is money/migration);
   then re-run the evidence capture and record legs 3 and 4 on FEAT-083.
3. Close the verified-done P1 items: docs/planning/backlog-comb/p1-done-verification.md
   lists 23 CLOSE with closing notes. Close the plain bugs/UX ones; leave
   FEAT-230, FEAT-1972079852 and FEAT-2326609823 for Aaron (policy). 6 are WIRE
   (built, never called): MOD-072/073/078/080/085, FEAT-094 - keep open.
4. Rounds/landings for BUG-1211, test infra, BUG-1200 r2, meter r2 (Opus), workers part 1.
5. Game bugs from the comb: BUG-782, BUG-767, BUG-854, BUG-1143, BUG-1140,
   BUG-1178, BUG-1141; then FEAT-2326609746 (zone tax wiring).
6. Offload builds in remaining lanes: reconnect FEAT-9995385597, inc2a
   FEAT-9995385594 after the meter lands, hibernation after that.
7. Replace docs/planning/assessment.docx with assessment-v2.docx once Word no
   longer holds the file; commit with p3plus-1.md, p1-done-verification.md and
   this checkpoint.
8. BUG-1210, BUG-1208, BUG-1202, BUG-1203, BUG-1207, BUG-1199.

## Parked for Aaron (morning)

- 103 dirty worktrees were kept (uncommitted content; not forced). 32 clean
  ones were removed under token 20261117-221954-de3c67.
- Hibernation ASM-H6 (store retries run inside the stop, city not playable
  for up to ~70 s) and ASM-H7 (pause counts through the no-command clock).
- GGR95 delete reports went to SAFE's fallback log (bus refused the text).
