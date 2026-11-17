# Cold assessment: heading for Baseline One, or lost in the weeds? (2026-11-17)

Reviewer: independent cold reviewer for FEAT-9995385605. Read-only. No tests or builds were run. Every number below comes from git, the `metro` MariaDB, `gh run list` or files on disk on 2026-11-17. Anything I could not check is marked **UNVERIFIED**.

## (a) Verdict

**Drifting, not lost, and currently pointed away from the milestone.** Baseline One (FEAT-083) is closer than the board suggests. Its 2026-10-30 rewrite cut it to four legs on the local Go engine. Leg 1 is met. The last build blocker on leg 2 (BUG-1142) landed on 2026-11-01. Since then, in 16 days, **nobody has recorded any evidence on FEAT-083: its last comment is dated 2026-11-01**. Today's work went elsewhere. Of 30 commits, **19 (63%) were cloud-offload work** (8 code, 11 docs) and **7 (23%) were game or game-loop work**. The cloud-offload work is pix, R2, hibernation, an active-hours meter, a launcher and Spot pricing, and none of it is needed to close Baseline One. The programme Aaron approved on 2026-10-31 put WS-A and WS-B first, and its five new epics (WS-A, C, D, E, F) have **zero comments and zero activity** since. The team is productive. It ships real fixes, and rounds catch real defects. But it keeps starting new fronts instead of finishing the one milestone that is nearly done. The structural risk sits underneath that. Baseline One is judged on the Go engine and the TUI, while the game Aaron actually plays is the webconsole's TypeScript simulation, with the Go engine off by default (`metropolis.liveEngine` must be `'1'`). Closing FEAT-083 on the TUI without settling that split would be a technicality.

## (b) Evidence

### For the verdict (strongest three)

1. **The milestone is stalled on evidence, not on code.** FEAT-083's close checklist: leg 1 was MET on 2026-10-30, and legs 2–4 are "[OPEN]". All 11 dependencies are done; the last one, BUG-1142, landed in 49c760d on 2026-11-01. No comment on FEAT-083 since 2026-11-01. The TUI already registers the surfaces the legs need: F1 map, F2 finance (`f2.finance`), F3 build (wired 2026-10-31) and F4 services. So legs 2–4 look like an evidence run plus possibly small fixes. Whether they actually pass is **UNVERIFIED** (no binary was run).
2. **Effort went to a new front, at the expense of the approved programme.**

   | Period | Commits | GAME | ENABLER (Go loop/wiring) | SCALE/CLOUD | INFRA (guards/CI/kit) | DOCS |
   |---|---|---|---|---|---|---|
   | 10-06 to 10-29 (dormant) | 13 | 0 | 0 | 0 | 10 | 3 (GGR renders) |
   | 10-30 to 11-01 (burst) | ~54 | 10 | 17 | 7 | 12 | 8 (game packs) |
   | 11-17 (the last 7 days are this one day) | 30 | 6 | 1 | 8 | 4 | 11 (all offload) |
   | **6 weeks total** | **97–98** | **16 (16%)** | **18 (18%)** | **15 (15%)** | **27 (28%)** | **22 (22%)** |

   ENABLER counts include merge commits. There are about 9 distinct units: 5 wirings, TUI screens, BUG-1142, the passport and homes.

   - **Offload items opened this week:** 24 of the 35 non-assumption items opened this week are offload items (FEAT-9995385594..604, BUG-1196..1210).
   - **FEAT-1972079936 (offload epic):** 27 comments, the latest today.
   - **The programme epics FEAT-9995385583/84/85/86/87:** 0 comments each.
   - **FEAT-1972079852 (the webconsole switches to the live Go engine, the real convergence step):** last comment 2026-09-05.
3. **The backlog is growing, and closure tracking has stopped.**
   - **Open items:** 876 today (874 open, 2 in progress), against 706 on 2026-10-31. That is +170 in 17 days.
   - **ISO week 47:** 141 items opened, 5 closed.
   - **Assumptions:** 107 of the 141 are assumption items minted by AC reviews. 115 assumptions are now open, while FEAT-084 ("ASM close-out") has been open since 2026-08-14.
   - **Done flips lag landings:** BUG-1196, BUG-1160, BUG-1188, FEAT-9995385591 (UX-1) and FEAT-9995385592 (UX-2) all have fixes on main but are still `open`.

### Strongest evidence against (the case that it is on track)

- The spine is genuinely built and wired.
  - MOD-018/020/021/022/026/028/029/031 and FEAT-082 are done.
  - On 10-31 the wiring wave put policies, fiscal, coastal, social and defence into the composition root, and wired the TUI F3/F10/F12 screens.
  - BUG-1142 made builds settle on the ledger.
  - Today's FEAT-2326609845 (Home half) gives citizens homes from built housing.
- The headless 12-month suite already proves that money moves with conservation intact, that population grows, and that runs are deterministic (bill, 2026-08-17).
- Aaron plays the webconsole and finds real defects (programme §diagnosis).
- Rounds keep paying for themselves: in week 47, 21 ACCEPT and 11 REJECT, all with findings.
- The offload work is not frivolous. It fixes real defects in metroserve: a duplicate connection, a missing final snapshot, a handshake stall, a 40 s snapshot at 1M citizens. And Aaron engaged in it today by ruling Q100154–Q100165, so it was not team-invented.

## Where the game stands today

- **What a player can do today (webconsole, default config):** play a TypeScript mock simulation.
  - Building uses a tree build palette (UX-1).
  - 87 buildings can be placed.
  - The demand dock carries a colour law (UX-2), there is a consolidator, and there is a property card.
  - Saves go to IndexedDB, and File > Cities lists every saved city.
  - The Go engine shows only as an opt-in shadow badge (`liveEngineFlag.ts`: absent means "mock-only, the safe default").
- **What a player can do on the real engine (cmd/metropolis TUI):**
  - Watch months pass, which is leg 1 (money 1,500,000 to 1,527,496; population 64 to 62).
  - Open F2 finance, F3 build and F4 services.
  - Whether a build issued from F3, consumption and migration show up on screen is **UNVERIFIED**. That is exactly what legs 2–4 need recorded.
- **Built but not wired** (the memory note's claim holds):
  - **BUG-1125:** 27 of 66 engine packages had no non-test importer on 2026-10-31. Five were wired that day, so about 22 remain (**UNVERIFIED** recount).
  - **BUG-1126:** 9 of 14 UI screens were unwired. F3, F10 and F12 have since been wired, so census, demo, districts, proj, ticker and trade remain (6).
  - **FEAT-211** (F-key registry) is still open.
  - **WS-F**, the wire/park sitting that was meant to resolve all this, has not happened (0 comments).

## Process cost

| Measure | Value |
|---|---|
| Destructive verdicts, all time | 1,714 (1,108 accept, 606 reject, 35% reject) |
| Verdicts in week 47 | 32 for about 23 code commits, so about 1.4 rounds per landing |
| Recent landings that needed several rounds | spatial-partition inc2 (r4 after r2/r3 REJECT); social and defence wirings (r3 after r1/r2 REJECT); BUG-1197 (r3); FEAT-2326609845 (r1 REJECT, r2 rework still uncommitted) |
| CI on main, last 60 runs | 17 failures. Every burst reddens main: 10-31 4/13, 11-01 5/11, 11-17 7/23. Today needed a dedicated fix (BUG-1206: about 60 astgate findings) |
| Suite times | compose -race 1,104 s and citizens -race 1,118 s per package (FEAT-2326609845 lead gate); BUG-859 records CI "at the cliff" at about 1,200 s |
| Code, prod vs test vs tooling (git-tracked LOC) | Go prod 191k (885 files) against Go tests 228k (1,015 files; 222 attack/round/bug test files). Webconsole src 76k against webconsole tests 145k (484 files). Harness 55k plus tools 23k. **Test plus tooling is about 1.7× production.** Docs: 414 .md files, 63k lines |
| AC volume for non-game infra | inc2 store 94 ACs, active-hours meter 58, waking/reconnect 54, workers 41. That is 247 ACs written in one day, none of them on the Baseline One path |
| Guard-surface freeze | FEAT-230 "Guard-surface freeze until Baseline One is watchable" (P1, open, 0 comments) has been ignored. Since 10-26 there have been 8 or more guard commits (BUG-1116, 1118, 1144, 1148, 1163, 1166, 1182, quotepath siblings) |

## Backlog snapshot (open plus in progress = 876)

| Type | P0 | P1 | P2 | P3 | P4/P5 |
|---|---|---|---|---|---|
| feature | 1 | 69 | 149 | 54 | 6 |
| bug | 0 | 14 | 206 | 217 | 0 |
| module | 0 | 13 | 18 | 7 | 0 |
| assumption | 0 | 3 | 110 | 1 | 0 |
| other (interface/finding) | 0 | 1 | 4 | 3 | 0 |

- **Opened and closed per ISO week:**

  | Week | Opened | Closed |
  |---|---|---|
  | 35 | 99 | 203 |
  | 36 | 488 | 372 |
  | 37 | 346 | 150 |
  | 38 | 3 | 2 |
  | 41 | 19 | 7 |
  | 42 | 2 | 1 |
  | 43 | 2 | 1 |
  | 44 | 114 | 102 |
  | 47 | 141 | 5 |

  Weeks 39, 40, 45 and 46 have no rows, meaning no activity. The backlog grows whenever the team is active.
- **Open feature P1s (69):**
  - About 13 are cloud/offload or scale: FEAT-9995385594..603, FEAT-2326609775, FEAT-1972079936, FEAT-2326609764 and FEAT-2326609824.
  - About 6 are process/harness: FEAT-107, FEAT-136, FEAT-080, FEAT-230, FEAT-084 and FEAT-085.
  - The rest are game or depth, most of them old: from 2026-08-08 to 08-31 and untouched (for example FEAT-014 F2 Finance, FEAT-019, FEAT-016 and FEAT-055 mega-facilities).
  - **Bug P1s (14):** 6 are offload/metroserve or CI (BUG-1097, 1099, 1196, 1209, 1211, 859), and about 5 are game.
- **Bug P2s:** 106 open bugs carry pin, vacuous, mutant or test wording, about a quarter of all open bugs (the programme estimated 40% of all items).

## Cloud offload against Baseline One

- **Not on the Baseline One path.** FEAT-083's 10-30 rewrite says so explicitly ("local is sufficient; Azure/cloud is FEAT-2326609824 scope").
- **Partly a prerequisite for the 100M goal (Q100137).** The 100M goal needs individual citizens on a cloud Go engine viewed from a browser. Some of today's work serves it directly: BUG-1198 (stop materialising the whole population), BUG-1205 (snapshot at 1M), and workers and staggered walks (FEAT-9995385598). Much of it serves *running a hosted service cheaply* rather than *proving 100M*: the active-hours meter, hibernation, waking/reconnect protocol messages, the launcher, Cloudflare Access, the arm64 gate, Spot tiers, R2 fencing and group commit.
- **The 100M proof's own named blocker list** (FEAT-2326609824: BUG-666 chain, partition flag ON, per-domain convergence) has 1 comment, from 10-30.
- **How the two goals compete:** both draw on the same lead and lane capacity and the same 18-minute race suites. Every offload landing also adds astgate surface, as BUG-1206 shows. The offload work is a third goal, *a cheap hosted service*. It is being run as if it were the 100M proof, and it is crowding out both Baseline One and the actual 100M blockers.

## (c) What Baseline One still needs, in order

1. **An evidence run on cmd/metropolis for legs 2–4: S, half a day.**
   - **Leg 2:** issue a build on F3 and show the ledger and world effect on F2 or F1.
   - **Leg 3:** run one month and show the consumption line moving on F2.
   - **Leg 4:** make a player change (zone, tax or policy) and show population or attract moving within a named horizon.
   - Record the frames on FEAT-083 the way leg 1 was recorded.
2. **Fix whatever the run shows missing: S to M, 1–3 days, estimated.** Likely gaps:
   - F2 may not break out consumption.
   - Migration may have no on-screen surface, beyond population in the top bar.
   - FEAT-2326609845's Workplace half and its r2 landing (the rework is uncommitted in a round worktree).
3. **Aaron's sign-off: one sitting.**
4. **Then the real gap, which Aaron must scope: XL.** The game he plays is the TypeScript sim. "Watchable on the real engine" only means something to the player once FEAT-1972079852 (the webconsole switching to the live Go engine) and per-domain convergence happen. BUG-523 (the TypeScript and Go money models differ) is the big known divergence. Either Baseline One is accepted on the TUI and convergence becomes Baseline Two, or convergence is the real Baseline One. **This needs a ruling.**

## (d) What to stop or park

- **Park the offload increments until FEAT-083 closes:**
  - FEAT-9995385594 inc2 R2 store and FEAT-9995385603 inc2b.
  - FEAT-9995385595 hibernation, FEAT-9995385596 meter, FEAT-9995385597 waking.
  - FEAT-9995385599 Cloudflare, FEAT-9995385601 launcher, FEAT-9995385602 arm64, FEAT-9995385604 measurements.
  - Keep at most FEAT-9995385600 (metroserve on pix, £0) as a dogfood host. Finish the in-flight r-rounds (BUG-1207/1208/1209) or park them.
- **Enforce FEAT-230's guard freeze, or cancel it honestly.** Guard-bypass hardening (the author-guard command-substitution family) is not game work.
- **Stop writing 40–94-AC documents for non-game infrastructure,** and stop minting an assumption item for every reviewer note: 107 were minted today against an ASM close-out (FEAT-084) open for three months.
- **Stop extending the TypeScript sim's depth** (buildings, consolidator features) without a convergence plan. Every TypeScript-only mechanic adds to the convergence debt.
- **Kit and forge adoption churn** (GGR re-renders, NATS, library catalogue) should land as one batch per month, not as a stream of commits.

## (e) What should change in how the team works

1. **Session-open check on the milestone.**
   - Every lead session opens by reading FEAT-083's legs.
   - No new epic or AC pack is dispatched while a leg is OPEN and unblocked, unless Aaron names it as an override.
   - The 16-day silence after BUG-1142 is exactly what this check catches.
2. **Done flips in the landing step.** The commit that fixes an item flips it to done (or the lead flips it at merge, the same day). Five landed items are still open today, and "5 closed in week 47" is false.
3. **Carry the full CI gate (golangci plus astgate plus the tsx-all job) into every burst before the push.** Main went red on all three burst days: 4, 5 and 7 failures. Name a burst-ending "CI green" landing as part of the plan, not as a surprise.
4. **Proportionate rounds.** Keep rounds for engine, money, persistence and concurrency code, where the 35% reject rate is earning its keep. Use the docs/test-only exempt tier, and a single light round, for UI copy and layout changes.
5. **Hold the Aaron sittings the programme already prepared** (WS-D balance workbook, WS-F wire/park table) before planning anything new. A plan with 0 comments in 17 days is either not wanted or not being run, and either way Aaron should hear that.
6. **One goal per week.** Label every dispatch BL1, 100M-proof, HOSTED-SERVICE or DEPTH, and report the split weekly. Today it would have read about 63% HOSTED-SERVICE.

## (f) Questions only Aaron can answer

1. **Is Baseline One closed on the TUI** (cmd/metropolis, the 10-30 rewrite)? Or will you only call it a game once the webconsole you play runs on the Go engine (FEAT-1972079852 and convergence)?
2. **Order of goals:** Baseline One first, then the 100M proof, then a cheap hosted service? Or did today's offload rulings mean you want the hosted service now?
3. **Programme 2026-11 (WS-A..H):** is it still the plan? If so, when are the WS-D balance and WS-F wire/park sittings?
4. **The TypeScript sim:** keep adding mechanics to it, or freeze it until convergence?
5. **FEAT-230 guard freeze:** enforce it or cancel it?
6. **The assumption flood** (115 open, 107 minted today): may the team stop recording reviewer notes as BOW items?

## Unverified

- Whether legs 2–4 pass today (I ran no binary).
- The exact unwired-package count after the 10-31 wiring wave.
- Whether Aaron has play-tested since 2026-10-31. BOW comments mentioning playtest or dogfood stop at 2026-09-11, apart from the 10-31 programme document and BUG-1211 (pix dogfood, today).
- Whether the commits dated 11-17 represent one day's work or work accumulated from 11-02 to 11-16 (author and committer dates are both 11-17).
- My commit classification is by hand from subject lines. Borderline cases: the consolidator counts as GAME, and BUG-1097/1196 count as CLOUD.
