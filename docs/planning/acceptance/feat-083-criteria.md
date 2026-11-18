# FEAT-083 Baseline One: acceptance criteria for the four legs (sign-off sheet)

**BOW:** FEAT-083 (P0). **Written:** 2026-11-18, BA pass for the backlog comb (FEAT-9995385605).
**Sources read:** `node claude-bow.js show FEAT-083` (all comments to 2026-11-18 03:40), `docs/planning/evidence/feat-083/recapture-2026-11-18/` (README, leg2/3/4.txt), `cmd/metropolis/feat083_recapture_test.go`, `cmd/metropolis/boot.go`, `boot_screens.go`, `statusbar.go`, `internal/engine/compose/compose_vacancy.go`, `data/pacing.json`, `docs/planning/northstar.md`.
**Not an existing doc:** no criteria doc for FEAT-083 existed. The close checklist lives only in the BOW description. This file is that checklist made testable.

## 0. What this file is for

Aaron signs each leg off or rejects it in five minutes. For each leg below there is a **walk-through**: the keys to press, the tick to watch for, and the exact text to see. Then the numbered criteria say which test proves it and what mutation turns that test red.

Facts that hold for every walk-through:

- The game boots RUNNING at 1x (`boot.go`, recapture README). At 1x one tick is about one real second and a month (30 ticks) is about 30 real seconds (`data/pacing.json`, `secondsPerMonthAt1x` 30, a placeholder). Press `Space` to pause, `]` faster, `[` slower. The status row at the bottom shows `TICK n  MONTH m` (`statusbar.go:105-117`).
- Top bar (chrome) shows date, `cycle x/30`, `money`, `pop`, `rating`.
- Screens: `F1` map, `F2` finance, `F3` land and construction, `F4` services. Map cursor starts at cell 15,15. Arrow keys move it. On F1: `y` buys the cell under the cursor, `b` builds a dwelling, `c` clears a refusal notice (`boot.go:1133-1160`). On F3: `n`/`p` pick the building, `b` queues it at the map cursor (`boot_screens.go:100-115`).
- All figures below are for the default seed, default composition, on main at e1e287a, from the recapture. Money is in pounds.

## 1. What is true today, and what is not yet shown

| Leg | Engine | On screen, main (e1e287a) | Open work |
|---|---|---|---|
| 1 Watchable month | Met 2026-10-30 | Frames no longer reproduce: the 1,500,000 to 1,527,496 rise is gone, money now falls (see 2.1) | Restate or re-prove (Q1) |
| 2 You build | Met | Works. Not discoverable: status row lists `[F1/F2/F4]` only, F3 is missing, nothing names `y` or `b`. F3 catalogue `cost` and `cap` columns are blank (`leg2.txt`) | BUG-1220 (r1 ACCEPT, advisories open, not landed) |
| 3 Citizens consume | Met | F2 shows Household consumption 0.00 / 1,760.00 / 2,090.00 at ticks 30 / 60 / 90, in a block labelled "City economy (not treasury money)". At 80x24 the pane truncates (BUG-1222, not landed) | BUG-1222 |
| 4 Migration responds | Met in engine | Only the chrome `pop` figure differs, from tick 180. Nothing says why. BUG-1220 adds a homes row, not landed | BUG-1220 |

**Frame size warning.** The recapture frames were drawn at 140 columns by 40 rows (`plW, plH = 140, 40`, `bug1213_pl_frame_test.go:23`). Nothing on main proves any leg at 80x24. The BOW comments for BUG-1220 and BUG-1222 show the 80x24 case is where things get cut. Every leg below must therefore also pass at 80x24 (criterion C0).

## 2. The walk-through and criteria, leg by leg

### 2.0 Common criteria

**C0. All legs are legible at 80x24.** The text named in each leg's "see" list appears, uncut, in a frame drawn at 80 columns by 24 rows.
- Test: frame test on the real boot path (`bootCore`, `routeKeyInput`, `composeDraw`), same harness as `feat083_recapture_test.go`, with the buffer set to 80x24.
- Expected: each named string is found in the frame text.
- Can-fail proof: shrink the buffer from 140x40 to 80x24 on main today. The leg 3 P&L is cut below Benefits and the status row is cut before the F3 entry (BUG-1222, BUG-1220 r1 advisory). The test must be red there, green after those fixes.

**C0b. One command reproduces every figure here.** The test writes the frames to `METRO_EVIDENCE_DIR`, and a README states the command. Can-fail proof: change the seed; the pinned figures change and the test goes red.

### 2.1 Leg 1: a watchable month (already marked MET; needs a ruling)

Checklist text: "Jan to Feb Y1, TICK 3 to 31, money 1500000 to 1527496, pop 64 to 62, RUNNING at 1x."

What the player sees now (`leg2.txt`, control): Jan Y1 money 1,500,000 pop 64 at tick 0; Feb Y1 money 972,996 pop 62 at tick 30. Pop matches. Money no longer matches, because coastal charges (250,000 processing plus 300,000 integration in month 1) post ahead of the finance hook.

**L1-1.** Boot, wait 30 ticks, watch the top bar: date goes Jan Y1 to Feb Y1, `cycle` runs 0/30 to 30/30 then resets, money changes, pop changes, state stays RUNNING.
- Test: frame test on the real boot path, 30 ticks, compare tick-0 and tick-30 frames.
- Expected: tick 30 frame says `Feb Y1`; money differs from tick 0; pop differs from tick 0; status row says `RUNNING`.
- Can-fail proof: stub the tick driver so ticks do not advance; the date and money stay put and the test goes red.

**L1-2.** Money at tick 30 is a number Aaron has ruled acceptable (see Q2). Until he rules, the test pins direction only: money at tick 30 is below tick 0 and the P&L (leg 3) names why.

### 2.2 Leg 2: you build

**Walk-through (human, 80x24).**
1. Boot. Press `F1`. Cursor is on cell 15,15. See top bar `money 1500000`.
2. Press `y`. See the cell bought, no refusal text. (Today a second `y` on the same cell prints `Build rejected: PurchaseTile rejected for tile {15 15}: already owned`, and that text stays all year; see L2-6.)
3. Press `F3`. See `Build queue` reads `no build orders`.
4. Press `b`. **Tick 0.** See `#1 dwelling  materials 0/100t  labour 40d  lead 57d  [materials-pending]`.
5. Wait for **tick 30**. See `#1 dwelling  materials 100/100t  labour 10d  lead 27d  [labour-pending]`. Top bar money 972,996 (same as a city where you built nothing).
6. Wait for **tick 60**. See `#1 dwelling ... [complete]`. Money 444,322.50 (same as the no-build city).
7. Wait for **tick 90**. Press `F2`. See in the P&L an outflow row `Construction 15000.00` (the month before it read 0.00). Money 195,779.50 against 210,779.50 in a city where you built nothing. The difference is exactly 15,000.00.

**L2-1. The queue row appears at once and moves through the stages.**
- Test: frame test on the real boot path. Press F1, `y`, F3, `b`. Assert F3 frame at tick 0 contains `#1 dwelling` and `[materials-pending]`; at tick 30 contains `materials 100/100t`; at tick 60 contains `[complete]`.
- Expected: exactly those strings (these are engine arithmetic on placeholder data: 100t materials, 40d labour, 57d lead; the numbers may be restated if the data file changes, the stage order may not).
- Can-fail proof: remove the ConstructionMaterials supply from the default composition (BUG-1142's fix). The row stays `materials 0/100t [materials-pending]` and the test goes red.

**L2-2. Building moves the ledger by exactly the build cost, once.**
- Test: engine test on the default composition, two runs with the same seed (control, build). Compare treasury at ticks 30, 60, 90.
- Expected: equal at 30 and 60; at 90 control minus build equals 15,000.00 exactly (arithmetic, not balance). Same difference from tick 90 to tick 360 (no second charge, no refund).
- Can-fail proof: change the dwelling cost in the build data by 1.00; the equality assertion fails. Also delete the settlement posting; the difference becomes 0 and the test fails.

**L2-3. The cause is named on F2.** At tick 90 the F2 P&L has a `Construction 15000.00` outflow row, and the Net line still equals the treasury change.
- Test: frame test on F2 at tick 90, build city.
- Expected: row present with 15000.00; row reads 0.00 or is absent at tick 60.
- Can-fail proof: post the cost under category Other; the row is missing and the test is red.

**L2-4. Discoverable without being told (BUG-1220).** At 80x24 on a fresh boot the player can see, without opening a menu, that F3 exists and that `y` buys and `b` builds.
- Test: frame test on the real boot path at 80x24, F1 screen, tick 0.
- Expected: the frame contains the text `F3` and a key hint naming `y` and `b`.
- Can-fail proof: today's status row `[F1/F2/F4] screens` fails it.

**L2-5. F3 catalogue shows cost and capacity.** Each catalogue row shows a number under `cost` and under `cap` (today both blank, `leg2.txt`).
- Test: frame test on F3. Expected: Dwelling row cost is non-blank and equals the cost that L2-2 measures (15,000.00); cap is a positive person count.
- Can-fail proof: blank the catalogue's cost field; red.

**L2-6. A refusal notice does not stick for the rest of the year.** After a refused `y` or `b`, the notice clears on the next key or after a stated time.
- Test: frame test. Press `y` twice on one cell, press an arrow key, assert the `Build rejected` text is gone.
- Today: it stays all year (`leg4.txt`). BUG-1220 says it clears on the next key (not landed).

### 2.3 Leg 3: citizens consume, money moves at a named surface

**Walk-through (human, 80x24).** Boot, do nothing, press `F2`. The named surface is the F2 Profit and Loss pane, block `City economy (not treasury money)`.
- **Tick 30** (month 1 close): `Household consumption 0.00`. `Net treasury change 948500.00` (opening capital 1,500,000 less coastal 250,000 + 300,000 and social support 1,500). Balance Sheet Treasury 972,996.00.
- **Tick 60** (month 2 close): `Household consumption 1760.00` (previous column 0.00). Net `-528504.00`. Treasury 444,322.50.
- **Tick 90** (month 3 close): `Household consumption 2090.00` (previous 1760.00). Net `-230673.50` (previous -528504.00). Treasury 210,779.50.
- The Net line equals the change in the Balance Sheet Treasury every month.

**L3-1. Consumption is non-zero from month 2 and rises with the city.**
- Test: engine test on the default composition, ledger log by category, months 2 to 6.
- Expected (directional, balance placeholder): `spend` category from households is below zero each close from month 2, and rises while population rises (1,760 then 2,090 at pop 62 then 74 then 86 in the recapture). Month 1 may be zero by design.
- Can-fail proof: zero the consumption coefficient in the data; the series is flat zero and the test is red.

**L3-2. The F2 figure equals the ledger figure.** The `Household consumption` number on F2 at each close equals the sum of the household `spend` postings for that month, to the penny.
- Test: frame test reads F2 at ticks 30, 60, 90; engine test reads the ledger; compare.
- Can-fail proof: shift the F2 builder to read the previous month's window; the two disagree and the test is red.

**L3-3. The Net line reconciles.** At every close, Net treasury change equals the difference between consecutive Balance Sheet Treasury figures (arithmetic, exact).
- Test: frame test, ticks 30..270. Expected: equal to the penny, months 1-9.
- Can-fail proof: drop one outflow row from the builder; Net no longer matches.

**L3-4. The label never lets consumption pass as treasury money.** The block header contains `not treasury money`. Frame test; red if the header is edited out.

**L3-5. At 80x24 the consumption line is on screen at the month 2 close** (C0). Today the P&L pane truncates below Benefits at 80x24 (BUG-1222 comment). Frame test at 80x24 at tick 60: contains `Household consumption` and `1760.00`.

**L3-6. The player is told which line to watch.** (Open, see Q3.) Recapture: "no hint that the consumption line is the thing to watch." If Aaron wants this, the criterion is: the F2 screen or hint row names the City economy block. Otherwise it is out of scope.

### 2.4 Leg 4: migration responds to something the player did

**Walk-through (human, 80x24), two cities side by side or two runs.** City A: do nothing. City B: F1, `y`, F3, `b` five times on cursor cells (the recapture queued five; the sixth `y` was refused as already owned).
- **Ticks 30 to 150:** both cities read `pop` 62, 74, 86, 101, 117. Identical. (Seed homes are not yet full.)
- **Tick 180** (month 6): A `pop 122`, B `pop 130`.
- **Tick 240:** A 125, B 133. **Tick 360** (month 12): A 148, B 156.
- With BUG-1220: second row under the top bar, `homes 120 places, 122 lived in, 0 free | moved in 2, left 0 this month`. At tick 180 in city A it leads with `HOMES FULL - build dwellings (F3) to grow`. In city B after dwellings complete, `places` and `free` rise and the cue clears.

**L4-1. Directional engine gate.** Same seed, same ticks: with n completed dwellings, population at month 12 is strictly higher than with none, and strictly higher with more dwellings than with fewer, up to the point where vacancy stops binding.
- Test: engine test on the default composition, n = 0, 1, 3, 5, 6. Expected: month-12 population non-decreasing in n, strictly greater for n >= 1 (placeholder balance numbers: 120 seed homes ASM-1644, 2 people per migrant household ASM-1645, so no absolute value is pinned).
- Can-fail proof: restore the placeholder `HousingVacancy = 1,000,000` (BUG-1214's old state); every n gives the same population and the test is red.
- **Known anomaly to resolve first (Q4):** BUG-1214's landing note says six dwellings give 190 against 148 at month 12. The TUI recapture with five dwellings gives 156 against 148. One more dwelling cannot explain +34. Either the TUI path queues all builds on one cell (cursor never moves) and capacity differs, or the two harnesses differ. The L4-1 test must run through BOTH paths for the same n and get the same population.

**L4-2. No effect before it could exist.** Population is identical in both cities until the first completed dwelling raises vacancy (ticks 30 to 150 above). Test: same pair of runs; assert equality over months 1-5 and a gap at month 6. Can-fail proof: add a free population bonus at queue time; the early months diverge and the test is red.

**L4-3. The player can see why (BUG-1220).** At 80x24 the homes row is on every screen's top area, its figures equal the engine's (places = seed 120 + completed capacity; lived in = population; free = max(0, places - lived in); moved in and left = this month's migration counts), and it carries the `HOMES FULL` cue exactly when the engine's vacancy rule is binding.
- Test: frame test on the real boot path, ticks 30 to 360 in city A and B; assert the row's numbers against the engine's own query, every month.
- Can-fail proof: hard-code `0 free`; the row disagrees with the engine in months 1-5 and the test is red. Flip the cue condition; red.

**L4-4. The cause of a stall is named in the player's words** (covered by L4-3's cue). The check is human: Aaron plays city A to tick 180 and can say, without help, why population stopped and what to press.

**L4-5. The other levers (jobs, services, policy) are not required for the leg.** Recorded in BOW: jobs, services and policy links are still unwired (`SetFunding` police/teachers rejected `MET-G1202`; no build-to-firm bridge). Leg 4 asks only for "a change the player makes", and housing is that change. See Q5.

## 3. Out of scope for FEAT-083

- The 100 million cloud proof (FEAT-2326609824; Q100114, Q100137).
- Balance values: seed homes 120, 2 people per migrant household, coastal charge size, treasury trajectory, consumption coefficients. Each stays a placeholder with a directional test until Aaron's row-by-row pass.
- Jobs and employment (BUG-782), policies reaching attract, services bridge.
- Webconsole parity. This item is the Go engine behind the terminal game only.
- Closing the item: the lead closes it after Aaron signs off. This file does not authorise a `done`.

## 4. Unstated assumptions (made visible)

1. "The real engine" means `compose.Wire` through `bootCore`, not the stub. The recapture used it.
2. "Observable" means on the 80x24 terminal. The recapture proved only 140x40.
3. A leg is MET when its engine test is green and the player can see the effect without being told. The recapture marked legs met while saying "not discoverable"; this file treats discoverability as part of the leg.
4. Tile purchase (`y`) costs nothing visible: control and build treasuries are identical at ticks 30 and 60. Either the starting cell is free or the cost lands in the tick 90 15,000.00. Nobody has checked which.
5. The 15,000.00 leaves the treasury at the month 3 close (tick 90), after the dwelling completes at tick 60, not at queue time. A player may expect the money to go at `b`.
6. The treasury falls 1,500,000 to 210,779.50 in three months and the evidence stops at tick 90. Nobody has shown whether the default city survives a year.

## 5. Questions only Aaron can answer

**Q1. Leg 1 no longer reproduces (money falls instead of rising 1,527,496). What should leg 1 say?**
a) Keep as MET on history, restate the checklist to "date, money and pop all move at 1x" (recommended: the loop does run).
b) Re-prove it against a rise in money, which means changing the coastal charges first.
c) Drop leg 1 from the checklist; legs 2-4 imply it.

**Q2. The default city loses 1.5M to 211k in three months (coastal charges 550,000 in month 1 alone). Is that the Baseline One experience?**
a) Yes for now; the P&L explains it, balance pass later.
b) Move the coastal charges after month 12 or halve them so a new city survives a year (recommended: a player reaching tick 120 with no money cannot judge legs 2-4).
c) Give the player a starting pot large enough for the charges.

**Q3. Must the game tell the player which F2 line to watch for consumption?**
a) No, F2 is in the hint and the labelled block is enough (recommended; the label already says what it is).
b) Yes, add a one-line cue on F2.

**Q4. Five dwellings give +8 people at month 12; six give +42. Is the larger effect the intended size of "a player change"?**
a) Fix the harness mismatch first, then rule on size (recommended; the number is unexplained).
b) Accept both as placeholders.

**Q5. Is housing alone enough for leg 4, or does "a change the player makes" need a second lever (a policy or a service) before sign-off?**
a) Housing alone (recommended: it is the first proven link; jobs and policy are BUG-782 and later).
b) Housing plus one policy.
c) Housing plus jobs.

**Q6. When a build is queued, should the cost show at queue time?**
a) Leave it: cost appears at the month close (recommended for Baseline One; it is a ledger design choice).
b) Show a committed-cost line on F3 at queue time, no ledger change.
c) Take the money at queue time.

## 6. Size

Mostly done. What remains is **one lane, medium**: a single 80x24 sign-off test file (C0, L2-1..L2-5, L3-5, L4-3) plus finishing the two open lanes BUG-1220 (r2 small) and BUG-1222 (landing). Splitting: (a) land BUG-1220 and BUG-1222; (b) add the 80x24 sign-off test and the L4-1 two-path check; (c) Aaron plays the walk-through. Total: about one lane day after the two landings.

## 7. Check against the code

- No duplicate. BUG-1213 (leg 3) and BUG-1214 (leg 4) are done. BUG-1220 and BUG-1222 are the open on-screen work and are not on main (git status shows no changes under `cmd/metropolis/boot.go` for them).
- The BOW leg wording "named surface" for leg 3 is now met: the named surface is F2, P&L, `City economy` block.
- `internal/engine/compose/compose_vacancy.go:30-60` is the vacancy rule L4-1 tests; `baselineOneSeedHomeCapacityPeople = 120` and `vacancyPeoplePerUnit = 2` are marked balance placeholders there.
