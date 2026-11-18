# FEAT-083 recapture on main e1e287a (2026-11-18), legs 2, 3, 4

Method: `cmd/metropolis/feat083_recapture_test.go`. Real boot path (bootCore), keys through routeKeyInput, frames from composeDraw, ticks via KindAdvanceTicks. Same seed for control and treatment. Frames: leg2.txt, leg3.txt, leg4.txt (regenerate with METRO_EVIDENCE_DIR set). Tester only, nothing fixed.

## Leg 2, you build: MET
- F1 `y` buys the cell under the cursor, F3 `b` queues a Dwelling. Tick 0: F3 queue `#1 dwelling materials 0/100t labour 40d lead 57d [materials-pending]`.
- Tick 30: materials 100/100t, labour 10d, `[labour-pending]`. Tick 60: `[complete]`.
- Money: control treasury (F2) 972,996.00 / 444,322.50 / 210,779.50 at ticks 30/60/90; build identical at 30 and 60 and 195,779.50 at tick 90. Exactly GBP 15,000.00 left.
- The money is now visible as a cause: the tick 90 F2 Profit & Loss has a `Construction 15000.00` outflow row (0.00 the month before). The cost lands at the month-3 close, after the build completed at tick 60.
- Cosmetic gaps: F3 catalogue `cost` and `cap` columns are blank.
- Discoverable without being told? No. The status line says `[F1/F2/F4] screens`; F3 is not listed and nothing says `y` buys or `b` builds.

## Leg 3, citizens consume: MET
- F2 PROFIT & LOSS, no player action. Close 1 (tick 30): Household consumption 0.00, Net treasury change 948,500.00 (opening capital). Close 2 (tick 60): consumption 1,760.00 (previous 0.00), Net -528,504.00. Close 3 (tick 90): consumption 2,090.00 (previous 1,760.00), Net -230,673.50 (previous -528,504.00).
- The player reads a "City economy (not treasury money)" block with the consumption line growing as the city grows, beside a Net line that equals the treasury change. Treasury 972,996 to 444,322.50 matches the Balance Sheet.
- Honest limit: consumption is shown as a city-economy figure, not treasury money; it reaches the treasury only through the tax rows. Month 1 shows 0.00 by design (first close).
- Discoverable? Yes once F2 is opened (it is in the status line hint); no hint that the consumption line is the thing to watch.

## Leg 4, migration responds: MET in the engine, thin on screen
Treatment: five dwellings queued at tick 0 (the sixth buy was refused "already owned", the cursor did not move far enough; the refusal text stays on the F1 frame all year). Population on the chrome bar, control vs treatment, ticks 30..360: identical 62, 74, 86, 101, 117 for months 1-5; then month 6 (tick 180) 122 vs 130; m7 124/132; m8 125/133; m9 130/138; m10 134/142; m11 143/151; m12 (tick 360) 148 vs 156.
- Where the player sees it: only the chrome figure `pop N` at the top of every screen, from month 6 on. No census, migration, vacancy or "homes full" figure on F1, F2, F3 or F4 (searched all four screens at month 12 for vacan/housing/migrat/census/capacity: only F4 "CAPACITY VS DEMAND", which lists services such as coastguard 0/20, not homes).
- GAP in the leg's spirit: nothing tells the player why population stalled (control flattens at about 120 from month 6 because the seed homes are full) or that building homes is what moves it. A player sees two numbers differ only if he runs a control city beside his own.
- Discoverable? No.

## Would Aaron see the loop run with no instructions?
He would see time, money and population move on the chrome bar and F2 (RUNNING at 1x is the boot state). He would not find buying or building without being told `y` and `b`, and would not learn what limits population.
