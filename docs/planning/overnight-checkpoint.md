# Overnight run checkpoint (2026-11-17 night)

The lead (Bev) rewrites this file at every landing and every dispatch burst.
If the session stalls (token limit, crash, compaction), a resumed lead reads
this file, the BOW comments on each item, `git log -10`, `gh run list
--branch main --limit 3` and `git worktree list`, and carries on. It never
redoes committed work and never trusts recall over those four sources.

## STATE AT 23:40 on 2026-11-18 (newest; supersedes everything below)

origin/main = e3bc970. **CI IS RED on main (BUG-1242)**: viewgate rejects f5.why; the BUG-1234 idle test fails on the slow runner. The fix is STAGED in the main checkout (3 files) and in review in .claude/worktrees/round-cifix; land it first, on an ACCEPT recorded on BUG-1242, then confirm green with `gh run list` (never a chained exit code).

| Lane | State | Where | Next |
|---|---|---|---|
| CI fix BUG-1242 | Sonnet round running | staged in main; copy in .claude/worktrees/round-cifix | commit [BUG-1242], push, see green |
| Lane 4 BUG-1224 as rule 1 | Opus r2 ACCEPT on the old base; port r3 REJECT (re-save relabels a version-0 replay fixture); r4 fix running | .claude/worktrees/port-lane4 (on e3bc970; changes partly STAGED: use `git diff HEAD`) | narrow round on the delta, allow entries for testdata/rules/v1, land |
| Lane 5 FEAT-9995385620 | three REJECTs; r4 = dry-run restore, fix running | .claude/worktrees/agent-adec187556609086f (base 762c857) | Opus r4 round; hosting work, spare lane only |
| Menu L1 FEAT-9995385622 | r1 REJECT (letters), r2 REJECT (sub-list hides an ancestor); r3 = one left-to-right cascade, fix running | .claude/worktrees/agent-afdb71fe010d0edec (base de23f76) | Sonnet r3 round; then L2 (Opus review), L3-L5 |
| Help/save/load/debug FEAT-9995385621 | all three parts built; second finisher making the suite green, moving its error range off V990-V999 | .claude/worktrees/agent-ab13ecb6178696f6c (base de23f76) | Opus round (save/load = data), land; then the welcome screen |
| Money format FEAT-9995385625 | Sonnet build running | isolation worktree (base e3bc970) | Sonnet round, land |
| Playability FEAT-9995385623 | plan + 8 personas + template + register written (uncommitted docs) | docs/playtest/, acceptance/feat-9995385623-playability-module.md | driver lane PT-A, welcome lane PT-E (after help), runner PT-C; NO session before a cost forecast and Aaron's Authorise |
| A2 publish f5.why | NOT dispatched | - | dispatch once CI is green: it answers "why is nobody moving in" on screen; it removes the f5.why accepted-views entry and flips whyViewServed |
| Roads FEAT-9995385608 | NOT dispatched (Aaron asked how to lay roads) | - | after lane 4 lands (same build code) |
| Diagnosis worktrees | done; read-only scratch | .claude/worktrees/diag-migration, diag-migration-old | remove (deletion gate) |

Migration finding (BUG-1243, P0, Aaron to rule): arrivals stop at about 1,400 people because safety and environment only fall; old and new formula alike. Also BUG-1244 (rating is the credit rating), BUG-1245 (parks costs 2.75 points from day one).

## STATE AT 22:30 on 2026-11-18 (superseded by 23:40 above)

origin/main = de23f76 (F5 screen f922c52 + BUG-1234 de23f76; CI run 44406336316 in progress, all jobs green but build-test-vet).
LOCAL, NOT PUSHED: fba12a5 rule versions lane 3; e3bc970 ten-lever formula A1 + BUG-1231. Push both when that CI run is green.
Aaron's rulings tonight (verbatim on the items): fly-out menu FEAT-9995385618 (bar on top row, keys and mouse, replaces F3 for building, all four contents; F10 opens; clock runs; closes after placing; confirms); minimum terminal 120x30, 80x24 retired (BUG-1236); menu first then wire save/load/tax/borrow (BUG-1237); help + save + load + save debug information FEAT-9995385621; standing OK this session for secret-guard allow entries for TEST FILES AND FIXTURES ONLY, one exact path each, named in the commit.

| Lane | State | Where | Next |
|---|---|---|---|
| Lane 4 BUG-1224 as rule 1 | r1 REJECT (final snapshot could not read rv:1 frames) fixed; Opus r2 round running | .claude/worktrees/agent-a5a79cef252f1901c (base 762c857) | on ACCEPT port (3-way: compose.go, ruleversion_test.go, errors.json, master-plan), allow entry for testdata/rules/v1, land; then FEAT-9995385608 roads and placement C1 |
| Lane 5 FEAT-9995385620 | r1 and r2 REJECT; r3 structural fix running (one shared decision function for restore and pre-check) | .claude/worktrees/agent-adec187556609086f (base 762c857) | Opus r3 round |
| Menu L1 engine FEAT-9995385618 | Sonnet build running (new package internal/ui/flyout) | isolation worktree | Sonnet round; then L2 (Opus review), L3-L5 per the criteria doc section 6 |
| Help/save/load/debug FEAT-9995385621 | Sonnet build running (may stop at the design if a safe save needs an engine command) | isolation worktree | finisher or Opus round (save/load = data) |
| A2 publisher (per-lever compose files, publish f5.why, flip whyViewServed in cmd/metropolis/boot.go) | NOT dispatched: needs e3bc970 on origin | - | dispatch after the push |
| Uncommitted in main | docs/planning/acceptance/feat-9995385618-flyout-menu.md (commit with a docs commit), kit files (need a review verdict) | main | - |

New items tonight: BUG-1234..1241, FEAT-9995385618..621. SAFE asked on the bus at 19:35 (secret guard false positive; host the browser console on pix): no reply yet.
LESSON: a port agent that runs `git apply --3way` leaves its changes STAGED; take its change set with `git diff HEAD`, not `git diff` (the first A1 port into main missed everything but one file and broke the build for a few minutes).

## RESUMED 2026-11-18 19:25 - state at 20:00 (superseded by the 22:30 state above)

main = origin/main = 762c857 (rule versions lane 2 LANDED; CI run to be seen). Aaron is at the keyboard, playing the terminal game.

| Lane | State | Where | Next action |
|---|---|---|---|
| Rule versions lane 2 | LANDED 762c857 (lead gates 14/14; Aaron approved the one allow-list entry) | main | see CI green |
| Rule versions lane 3 (replay harness C21, C22) | Sonnet build running | isolation worktree (agent report names it) | Sonnet round, then land |
| Rule versions lane 4 (BUG-1224 as rule 1) | Sonnet build running (tests first; a finisher is likely) | isolation worktree | finisher if needed, then OPUS round, land, then placement C1 gets its rule number |
| Rule versions lane 5 (metroserve operator line, journalscan) | NOT dispatched (load: hold until a compose lane finishes) | - | dispatch |
| BUG-1231 r2 | finisher DONE, all gates rc=0; Opus round r2 running (records its own verdict) | .claude/worktrees/agent-a2f4e3f8ee1ba231e | on ACCEPT land WITH formula A1 (same estate; data/errors.json needs a text merge: main holds G5610-G5619); then A2 |
| F5 why screen A3 | r1 REJECT (missing plan edge; huge values print garbage; MET-E004 on every boot); r2 fix running | .claude/worktrees/agent-a73d11fa88e9fc982 | narrow Sonnet r2 round, verdict stated in the report |
| BUG-1234 (P0, Aaron's frame): screens do not blank their background, F3 queue lists completed orders, rows cut off | Sonnet build running; told to fix centrally and avoid boot.go/boot_screens.go/statusbar.go (F5 lane holds them) | isolation worktree | Sonnet round, land; port may need a hand merge with F5 |
| FEAT-9995385618 fly-out menu (P0, Aaron's rulings on the item) | BA criteria being written | docs/planning/acceptance/feat-9995385618-flyout-menu.md | read, interview Aaron on its questions, dispatch lanes; supersedes placement C3 |
| Placement C1 | r1 ACCEPT, held for a rule number | .claude/worktrees/agent-a385f81166b77c3f3 | after lane 4 |

Asked SAFE on the bus (19:35): secret guard false positive on SHA256 fields in goldens; host the browser console on pix (Aaron's request). No reply yet.
Kit files still uncommitted in the main checkout (.claude/settings.json, .claude/commands/lifecycle.md, tools/bow-summary.js, vestige-pointer.js, tools/lib-catalog.js): need a review verdict.

## STOPPED FOR A LAPTOP RESTART - 2026-11-18 about 19:15 (Aaron's instruction)

All agents and background runs stopped by the lead. main = origin/main = 3951276, CI green, pix running 3951276.
Nothing half-committed. Uncommitted in the main checkout: the forge kit files only (.claude/settings.json,
.claude/commands/lifecycle.md, tools/bow-summary.js, vestige-pointer.js, tools/lib-catalog.js) - need a review verdict.
The authoritative plan is docs/planning/delivery-path-2026-11-18.md; rulings are on the BOW items. STAGE 1 STATE:

| Lane | State | Where | Next action |
|---|---|---|---|
| Rule versions lane 1 (package) | LANDED 3951276 | main | - |
| Rule versions lane 2 (record + restore) | Opus r1 ACCEPT recorded; leftover gates NOT finished (only gofmt ran) | estate .claude/worktrees/agent-aba8d30c0c0eaf048; round worktree round-rules2-r1 (holds the reviewer's round_r1_attack_test.go - ADOPT it at port) | run temp/lead-gates/rules2.sh, then port + commit [FEAT-9995385612]; then lanes 3 (replay harness header), 4 (BUG-1224 as rule 1: wire, flip to wired, raise Current, v1 fixture), 5 (metroserve operator line + journalscan) |
| Placement C1 (data + resolver) | r1 ACCEPT; NOT landing until it can be a versioned rule (after rules lane 2 + a rule number for the placement gate) | .claude/worktrees/agent-a385f81166b77c3f3 | then C2 (committed cost) and C3 (F3 screen) |
| Formula A1 (ten levers, attract) | Opus r1 ACCEPT; lands WITH BUG-1231 (same files) | .claude/worktrees/agent-a2f4e3f8ee1ba231e | wait for BUG-1231 |
| BUG-1231 save fix | r1 REJECT (record not validated); r2 code + test file written, NEVER compiled or run | same worktree | fresh finisher: brief is in the conversation ("BUG-1231 r2 finisher") - envelope evidence first, then tests, red-proofs, gates; then a narrow Opus round |
| F5 why screen A3 | built + wired, all gates green but edge-lint (1 finding, believed pre-existing - unverified); review interrupted, NO verdict | .claude/worktrees/agent-a73d11fa88e9fc982 | fresh Sonnet round (do not record on the item while the formula lane's verdict is the recorded one - state the verdict in the report) |
| A2 (per-lever compose files, publish f5.why, housing = free homes AND affordability) | not started | - | after A1 + BUG-1231 land; contract = internal/ui/screens/why/types.go Patch |
| BUG-1224 refusal | built, held for rules lane 4 | .claude/worktrees/agent-ad5c8b543590393b6 | lane 4 |

Aaron's rulings today are in his words on FEAT-083, FEAT-9995385606..616, BUG-782, BUG-1217, BUG-1224, BUG-1228, BUG-1231.
Standing: overnight runs every night, game lanes only - NOT started tonight (restart); start on Aaron's return.

## RUN ENDED 08:45 on 2026-11-18

The overnight timer (cron 11999eb3) is deleted; no lanes are running. Summary for Aaron:
docs/planning/morning-report-2026-11-18.md. Next action is his: clear the git lock, then the lead lands the
LAND-READY QUEUE below in order.

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

## LANDING IS BLOCKED (since 03:20) - needs Aaron

`E:\git\Metropolis\.git\index.lock` is a stale, zero-byte lock written 02:27 with no git process running.
Every `git add` / `git commit` in the main checkout fails on it. Removing it was DENIED to the lead by the
permission classifier, so the lead has not removed it and must not work around it (no alternate index, no
side-door commits). Aaron clears it with:  ! rm .git/index.lock   (typed in the Claude prompt, from the repo root).
Until then: builds and review rounds continue; accepted work queues below as LAND-READY and is landed in
order the moment the lock is gone. Do not copy any more estates into the main working tree while blocked.

## Landed tonight

Pushed and CI-green through 01946d2; 6dcfa4c (docs) pushed and CI green (checked 03:44).
BUG-1209, 1194, 1190, 1192, 1191, 1200, 1212, 1211, 1136, 767, 1210, 1215, 1214 (leg 4), 1213 (leg 3), 1140 Compare half,
1143 regression test, deploy files, docs. Closed without code: BUG-854, BUG-1143. 18 verified-done P1 items closed.
pix redeployed by SAFE at 01946d2: host label pix, tick continues.

## LAND-READY QUEUE (verdict ACCEPT recorded; land in this order when the lock is cleared; check CI between pushes)

1. BUG-1140 Run side (r5 ACCEPT) - ALREADY COPIED into the main working tree, unstaged: data/errors.json,
   internal/converge/{errors.go,finance_domain.go,population_domain.go,population_domain_test.go,services_domain.go,
   argkeys.go,strict.go,strict_test.go,strict_r4_test.go,strict_r5_test.go}. Local checks passed (converge, detgate,
   add-error check, errs render gate). Stage exactly those files, commit [BUG-1140], then `done` the item.
2. BUG-1208 r2 (Opus ACCEPT 02:59) - LAND-READY: lead gates clean (1208-bug764 rc=0 in 388 s, 19 tests; 1208-compose
   rc=0 at 04:44, clean, 53 s). History: the round's post-verdict targeted compose run hit a
   20 min timeout under load (astgate, errs, detgate, headless passed). Owed before the port: one clean run of
   `go test -count=1 -run "Bug1197|BUG1208|Paging|Page" -skip "BUG799" -timeout 30m ./internal/engine/compose/`
   in a round worktree holding the estate, when no other compose run is going. Estate
   .claude/worktrees/agent-a8bc01e3c389428e3; 3-way merge compose.go, data/errors.json (adds MET-G015; main will
   already hold H513-H519 after item 1), accepted-findings.json.

3. BUG-1222 P&L at 80x24 (r2 ACCEPT, sonnet-round-r2-bug1222; all gates green in the round worktree) - estate
   .claude/worktrees/agent-ae780e15bd2b0b739: internal/ui/screens/finance/render.go, bug1222_pl_priority_test.go,
   cmd/metropolis/bug1222_f2_80x24_test.go, docs/planning/evidence/feat-083/bug1222-f2-80x24.txt. Base = main, plain copy.
   Land BEFORE BUG-1220 if 1220 is not accepted yet; if 1220 lands first, rerun ./cmd/metropolis/ in full after the
   port (1220 shrinks the content area to 20 rows; the render tests cover it, the frame test was run at 22).

4. BUG-1220 on-screen cues (r1 ACCEPT + narrow r2 ACCEPT, sonnet-round-r2-bug1220; all gates green) - estate
   .claude/worktrees/agent-ad7321429133e120c, 23 changed/new files (`git -C <estate> status --porcelain`), incl.
   cmd/metropolis/feat083_recapture_test.go (the untracked copy in the main checkout is the same test - overwrite it
   from the estate). compose.go needs a 3-way merge if BUG-1208 landed first. After the port: ./cmd/metropolis/ in
   full (BUG-1222's frame test then runs at 20 content rows), then the leg 3 + leg 4 capture at 80x24.
   Follow-up filed (P1): pause/speed/quit keys are cut off the 80-column status row; game boots RUNNING so not a blocker.

5. BUG-1178 atomic identity (round r2 ACCEPT; lead gates in the round worktree: vet, lint, astgate, paging slice
   all rc=0). Land AFTER BUG-1208: the metroserve race slice failed once in TestBug1197_AbandonedSigtermReleasesPagingClaim
   with "citizen id 5 already exists (cold)" - that is the BUG-1208 failure itself (the 1178 round worktree is main
   without 1208). After both are ported, rerun `go test -race -count=1 -run "Paging|Evict|Bug1197" ./cmd/metroserve/`
   in main before committing 1178. Plain copy: internal/engine/compose/citizen_paging.go, bug1178_identity_atomic_test.go
   from .claude/worktrees/agent-a582127cc6b251645.

6. BUG-1225 dropped connection's subscriptions + restore re-creating them (Opus r3 judged the code correct; narrow
   r4 ACCEPT sonnet-round-r4-bug1225, all gates green incl. lint, detgate, converge, astgate) - estate
   .claude/worktrees/agent-ac3ca57b6294d4f17: cmd/metroserve/{cityhost.go,main.go,sessiongate.go}, internal/engine/core/
   {subscribe.go,commands.go}, internal/engine/compose/snapshot.go, internal/protocol/envelope.go,
   internal/protocol/wsserver/server.go, accepted-findings.json (3-way merge with BUG-1208's entries), 6 test files.
   Base 6dcfa4c: 3-way merge anything items 1-5 changed (accepted-findings.json; check cityhost.go). After the port:
   metroserve race subset + compose Snapshot|Replay|Restore slice + astgate in main before committing. Then ask SAFE
   to redeploy pix (the live server leaks a subscription per reconnect and per restore today).

7. FEAT-9995385597 reconnect increment 1 (Opus r5 ACCEPT after four rejects; client + protocol types only) - estate
   .claude/worktrees/agent-ab7d0c718839f055a (`git -C <estate> status --porcelain`): internal/protocol/lifecycle.go
   (+test, 4 goldens), internal/protocol/wsserver/server.go (optional clientSessionId - 3-way merge with BUG-1225's
   server.go), wsserver/lifecycle_caps_test.go, data/errors.json (MET-V936..V939 - 3-way merge), webconsole/src/sim/
   {wire.ts,reconnect.ts,protocolClient.ts}, webconsole/src/components/LiveEngineBadge.tsx, tests (protocol-reconnect,
   -r4, -r5, protocol-lifecycle-wire, protocol-lifecycle-badge, four edited badge tests). After the port: tsc, the
   scoped set, add-error check, astgate, then `SCOPED_TIMEOUT_MS=1500000 node tools/test/scoped.mjs --webconsole-tsx-all`
   in a gate worktree BEFORE the commit (the BUG-1194 lesson). Commit as inc1; the item stays OPEN. Follow-up test owed:
   the readyBeforeReply leak probe (temp/round-reconnect-r5/, B1b).

## Baseline One (FEAT-083) - re-captured on main at e1e287a

Leg 2 MET on screen. Leg 3 MET on screen ONLY on a tall terminal: at 80x24 the P&L pane is cut off above the
Net and consumption lines -> BUG-1222 (P0), build running. Leg 4 real in the engine, on-screen cue built in
BUG-1220 (P0), round running. Legs are NOT marked met on the BOW; Aaron signs off.

## In flight (update as they report) - as of 07:45 (comment times on BOW items between 03:50 and 04:30 are the lead's estimates and run up to 20 min fast)

| Item | Stage | Where |
|---|---|---|
| BUG-1220 on-screen cues | r1 + r2 ACCEPT - LAND-READY (queue item 4) | estate .claude/worktrees/agent-ad7321429133e120c |
| BUG-1222 P&L at 80x24 | r2 ACCEPT - LAND-READY (queue item 3); advisories moved to BUG-1218 | estate .claude/worktrees/agent-ae780e15bd2b0b739; after BUG-1220 lands: give the P&L more grid height (boot.go) |
| BUG-1178 atomic identity | round r2 ACCEPT (partial round); the lead is running the gates it skipped (vet, lint, astgate, paging slice, metroserve race) - results in temp/lead-gates/gates.out; LAND-READY when those are rc=0 (plain copy of citizen_paging.go + bug1178_identity_atomic_test.go; base has no later change on main) | estate .claude/worktrees/agent-a582127cc6b251645; round worktree round-bug1178-r2 |
| FEAT-9995385597 reconnect inc1 | Opus r5 ACCEPT - LAND-READY (queue item 7) | estate .claude/worktrees/agent-ab7d0c718839f055a |
| BUG-1224 (P0) duplicate build on one cell is charged and builds nothing | BUILT, compiles, astgate green; HELD for Aaron's replay ruling (see Parked) before the Opus round | .claude/worktrees/agent-ad5c8b543590393b6 |
| BUG-1225 (P1) | r4 ACCEPT - LAND-READY (queue item 6) | estate .claude/worktrees/agent-ac3ca57b6294d4f17 |
| Comb: acceptance criteria | four docs written (FEAT-083, BUG-1168, BUG-763, FEAT-031); FEAT-141 v2 and FEAT-9995385585 not written | docs/planning/acceptance/*-criteria.md (uncommitted, lock) |
| FEAT-083 leg 4 size mismatch | EXPLAINED: the scripted re-capture put five builds on ONE cell (cursor cannot move before the first frame) -> one home. Engine rule sound (6 homes 190, 5 homes 184, 1 home 156, control 148). The re-capture after landing must draw a frame first and move the cursor between builds | FEAT-083 comment |
| BUG-1208 | Opus r2 ACCEPT; the lead's gate script runs its compose slice after the BUG-1178 gates (same gates.out: 1208-compose, 1208-bug764) | round worktree round-bug1208-r2 |
| Comb: p3plus + asm 'probably done' (44) | read-only verifier running | output docs/planning/backlog-comb/p3-asm-done-verification.md |
| FEAT-9995385596 meter | r2 built on OLD base 2fa4531; needs a rebase finisher AFTER the land queue (cityhost/main/health moved on main and move again with BUG-1208), then the Opus round | .claude/worktrees/agent-afac334e274decd2f |
| FEAT-9995385598 workers part 1 | state UNKNOWN; needs a fresh finisher | .claude/worktrees/agent-a59928c4840009f1f |
| BUG-1193 resolver cwd-first | r1 REJECT, probably a false alarm; recheck | estate .claude/worktrees/agent-a65bb1f0a0538394d |

BLOCKER FOR MORE GAME LANES: every remaining no-ruling game item (BUG-1223, BUG-1218, BUG-1219, BUG-1216, BUG-1221)
edits files that the four land-ready estates also change (statusbar/boot/save_wire/finance render/citizen_paging/
converge strict). They wait for the lock to be cleared and the queue to land. Offload builds that touch other files
run meanwhile.

## Queue, in order (THREE heavy Go lanes at most; compose in slices; every lane checks first that its bug still exists)

1. Rounds as lanes report: BUG-1222 (Sonnet), BUG-1178 (Sonnet). Add each ACCEPT to the land-ready queue.
2. Once the lock is cleared: land the queue in order, CI check between pushes, then ask SAFE for a redeploy
   (cmd/ internal/ data/ must have no uncommitted changes when asking), then one more leg 3/4 capture at 80x24.
3. Waiting on Aaron's rulings before any build: BUG-782 (migration and jobs), BUG-1217 (one count of homes).
4. Game items needing no ruling: BUG-1218 (F2 follow-ups, after BUG-1222), BUG-1219 (remove the plain Load trap),
   BUG-1216 (deterministic hook for the BUG-799 race tests), BUG-1221 (finance A/B reader strictness), FEAT-2326609746 (zone tax wiring).
5. Comb verification: close the CLOSE rows from the two P2 files after a spot-check; file the WIRE findings as bugs; then a verifier for p3plus-1/2 and asm-1 (44 items).
6. Meter Opus round r2; workers fresh finisher; BUG-1193 recheck.
7. Offload builds in remaining lanes: reconnect FEAT-9995385597, inc2a FEAT-9995385594 after the meter lands, hibernation after that.
8. Replace docs/planning/assessment.docx with assessment-v2.docx when Word releases it.
9. BUG-1202, BUG-1203, BUG-1207, BUG-1199.

## Comb verification results (04:10)

P2 features (docs/planning/backlog-comb/p2-feat-done-verification.md, 36 rows): 22 closed after a spot-check
(FEAT-115..123, 126, 127, 129..132, 134, 193, 1972079946, 2326609713, 2326609735). Held for a second look:
FEAT-217 (skill file not found in repo), FEAT-2326609779 (later increment not checked), FEAT-155 (registration
only), FEAT-2326609742 (evidence is outside this repo). BUILT BUT NOT WIRED, commented on each item, all stay
open: MOD-055 leisure (monthly advance never called - the leisure budget does not run in the game), FEAT-099
container port, FEAT-049 deposit shuffle, FEAT-103 mine types, FEAT-128 push-verify script. PARTIAL: FEAT-101,
FEAT-1972079872, FEAT-1972079897, FEAT-1972079906, FEAT-1972079907, FEAT-2326609760. P2 bugs (p2-bug-done-verification.md, 25 rows): 18 closed (fix commit confirmed on main); BUG-259, BUG-552, BUG-830
rated CLOSE but held (no commit hash in the evidence - second look). NOT done, commented: BUG-1092 (guard never
built), BUG-432, BUG-035, BUG-1198 (partial). Caveat for Aaron: the eight spatial-partition fixes (BUG-1064..1069,
1091, 1137) are only live with PARTITIONED_DERIVATIONS on, default off; BUG-1066's leak test skips without --expose-gc.
P3 + assumptions (p3-asm-done-verification.md, 38 rows): CLOSE 14, PARTIAL 16, WIRE 5, OPEN 1, UNSURE 2. Hash-backed
bug rows closed; BUG-610 and BUG-719 held (caveats); the five ASM CLOSE rows (ASM-1552, 1577, 1578, 1579, 579) left
for Aaron (they close on his recorded rulings). NOT WIRED, commented: MOD-061 destination, MOD-065 tunnels, MOD-076
spaceport, MOD-077 accelerator (no importer anywhere). Standing gap: the fold-then-close step in
docs/planning/asm-disposition.md has not been done for any shipped assumption (ASM-1540..1548, 1632..1638).

## Parked for Aaron (morning)

- FIRST: clear the stale git lock (see LANDING IS BLOCKED at the top): `! rm .git/index.lock`.
- CORRECTION to the lead's Baseline One reports: the 01:xx re-capture ran at 140x40, not 80x24. Legs 2 and 3 are
  on screen on a large terminal only; at 80x24 they depend on BUG-1220 and BUG-1222 (accepted, not landed). Read
  docs/planning/acceptance/feat-083-criteria.md (walk-through + 6 questions). Leg 1 as written no longer reproduces
  (money falls: 1.5M -> ~973k at tick 30 -> ~211k at tick 90, coastal charges). Leg 4 size mismatch (six dwellings
  +42 at engine level, five dwellings +8 on the terminal) is being diagnosed.
- BUG-1224 RULING NEEDED (blocks a P0 fix): refusing a duplicate build on a cell breaks replay of any journal
  that recorded an accepted duplicate (replay fails on the first rejected command). Options: tolerate the refusal
  on replay (the replayed city then has MORE money than the original - 15,000 charged, not 75,000), gate the rule on
  a journal version (lead recommends), or accept the break. Also yours: should homes stack on one cell instead?
- New criteria docs with questions for Aaron: bug-1168-criteria.md (rebuild overwrites the city with no way back),
  bug-763-criteria.md, feat-031-criteria.md (the terminal game cannot cycle map overlays at all). Not written:
  FEAT-141 v2, FEAT-9995385585.

- 103 dirty worktrees were kept (uncommitted content; not forced). 32 clean
  ones were removed under token 20261117-221954-de3c67.
- Hibernation ASM-H6 (store retries run inside the stop, city not playable
  for up to ~70 s) and ASM-H7 (pause counts through the no-command clock).
- GGR95 delete reports went to SAFE's fallback log (bus refused the text).
- Meter: a ~5-minute store failure now hibernates every city on a metered host (per the lead's fail-closed ruling) - confirm.
- BUG-1214 changes the default game: a city where the player builds nothing levels off at 148 (was 244) because homes run out; seed home capacity 120 is a balance placeholder (ASM-1644).
- Default city loses about 550k a month to an outflow shown as 'Coastal/other' (balance flag); BUG-1213 r2 is tracing what it really is.
- BUG-1200 edge: Esc closes a confirm sitting UNDER a blocking popup (z-order), not the visible popup. Not filed.
- Baseline One balance: coastal charges are 1.4M of the default city's first three months against a 1.5M pot; seed homes 120 and 2 people per migrant household are placeholders (ASM-1644/1645); building before the city reaches ~120 people does nothing for five months.
- BUG-1217 options paper (docs/planning/bug-1217-one-count-of-homes.md): seven questions, recommendation = seed homes into the citizens ledger first.
- BUG-1140: the comparison half is in; the trajectory Run side is in build.
- pix EXPOSURE: metroserve has no login; whether other LAN devices can reach port 8120 is NOT proven blocked (SAFE-572, P1: depends on the Hyper-V firewall default action; SAFE is testing). Only the dogfood city is on it. Nothing sensitive goes on pix until SAFE-572 is proven or the Cloudflare front door (FEAT-9995385599) lands.
- Windows Defender may be what makes file-heavy tests collapse on this machine (BUG-1215, unproven); an exclusion for the project temp folder is Aaron's call.
- forge upgrade --apply would make every hook path absolute (prevents the cwd hook failure) and update 8 kit assets: needs Aaron's yes.
