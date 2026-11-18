# BUG-763 acceptance criteria: consolidator layout leaves road spend off its capex books

**BOW:** BUG-763 (P1, open). **Written:** 2026-11-18, BA pass for the backlog comb (FEAT-9995385605).
**Sources read:** `node claude-bow.js show BUG-763` (title only: no description, no comments), `webconsole/src/sim/consolidator.ts:1795-1840` (`capexSpent`, `upkeepDelta`), `webconsole/src/sim/engine.ts` (layout stage 4749 onward, budget 5033-5140, per-tier loop 5690-5725, reconnect lane 5880-5960, density lane 6380-6440), `webconsole/test/attack-inc3-round11-dogfood.test.mjs` (the 2,292-building dogfood fixture the finding came from).
**Existing criteria doc:** none. **Duplicate check:** not a duplicate. The `capexSpent` field now exists and is populated for layout transactions (`consolidator.ts:1812-1820`) and for re-plan units (`engine.ts:5368`). The density lane already books its connector spend (`engine.ts:6425-6431`, "bill the REAL spend"). What the item says is still missing is the road spend that layout tiles cause. No test in `webconsole/test` mentions BUG-763. I could not find a saved repro of "3.7 times deeper overdrawn"; the original probe was a round 11 attacker run. So the numbers below are directional and the first task is to rebuild that repro.

## 1. What the player sees today

With the consolidator layout stage ON (`consolidatorLayoutEnabled`, default true), a city of about 5M funds lays rail, motorway and park tiles in passes. The stage has a per-pass capex ceiling (`layoutCapexCeilingThisPass`, `engine.ts:5128-5137`) and a reserve floor (`layoutCapexFundsFloor`, `engine.ts:5036`). Both are measured against `layoutCapexSpentThisPass`, which adds only each tier placement's own `buildCost` (`engine.ts:5720`).

Layout tiles occupy ground. Residential buildings next to them can lose their road link. The reconnect lane then lays Road Auto-Connect tiles for those stranded buildings (`engine.ts:5905-5930`) and spends real treasury money on it. That money is booked as reconnect spend, not as layout capex. So the ceiling and the reserve never see it. Per the item, a 5M city ends **3.7 times deeper overdrawn** with layout ON than with layout OFF, and the layout transactions' `capexSpent` says 0 for that spend.

## 2. What the player should see

The treasury cost of everything layout causes is counted against the layout budget. The ceiling and the reserve floor stop layout while the sum of its own tile cost and the road spend it induces is within budget. A city with layout ON never ends materially deeper in the red than the budget says it can.

## 3. Definitions (so the criteria are testable)

- **Induced spend** = pounds the reconnect lane (or any other autoConnect call in the same pass) spends laying road connectors for a building that was connected before the pass's layout placements and is stranded after them. Attribution rule: a stranded building counts as layout-induced when it was online at the start of the pass and offline after the layout stage, in the same pass (Q1 asks Aaron whether later passes count).
- **Layout capex booked** = the sum of `capexSpent` over the pass's `kind: 'layout'` transactions, plus induced spend under the rule above.
- **Treasury delta** = funds before the pass minus funds after the pass, with income and upkeep ticks excluded (run the pass function directly, as the round 11 fixture does).

All tests are engine tests in `webconsole/test/` using the `attack-inc3-round11-dogfood` fixture (160x96 city, 2,292 buildings, road spine every 8 tiles), reducer run directly.

## 4. Acceptance criteria

**AC-1. A repro that fails today.** Build a test that runs ONE consolidator pass on the 5M-funds dogfood fixture twice, layout ON and layout OFF. Record layout-ON treasury delta, layout-OFF delta and the sum of `capexSpent` on the layout-ON pass.
- Expected on main today: the layout-ON delta exceeds the booked `capexSpent` sum by the induced spend (a non-zero gap), and the end-of-run funds with layout ON are lower than OFF by a multiple of the budget. Print the gap.
- This test first proves the defect is real at the pass level. If the gap is zero on main, stop and report: the item is already fixed or mis-described.

**AC-2. Booked equals spent.** On the same fixture, for each pass 1 to 10: layout capex booked equals the treasury delta caused by layout and its induced spend, to the pound (arithmetic, exact: the ledger delta is an integer sum).
- Test: engine test, fixture above, 10 passes.
- Expected: `|booked - spent| <= 0` each pass.
- Can-fail proof: remove the induced-spend term from the booked sum; the equality fails by exactly the induced amount.

**AC-3. The ceiling counts induced spend.** With a ceiling `C` for the pass, layout capex booked in that pass never exceeds `C` (plus at most one unit of rounding stated in the test). Set `C` low enough that on main today the induced spend alone pushes the pass over `C`.
- Test: engine test; same fixture with the ceiling lowered through the existing data constant or the existing test hook (name it in the test).
- Expected: booked <= `C` after the fix; booked > `C` on main.
- Can-fail proof: leave `layoutCapexSpentThisPass` counting only tier `buildCost`; the test goes red.

**AC-4. The reserve floor counts induced spend.** After any layout pass, funds are at or above `layoutCapexFundsFloor` (`engine.ts:5036`) minus nothing: layout plus its induced road spend never takes funds below the reserve it was told to leave.
- Test: engine test with funds set just above the floor plus one tile's cost, so that one more tile would be affordable but its induced road spend would not.
- Expected: layout places nothing, or places only what keeps funds at or above the floor, and logs one named skipped line (GR#17: never silent). Funds at or above the floor in every case.
- Can-fail proof: reconnect-lane spend still outside the gate; funds end below the floor and the test is red.

**AC-5. Layout ON is not deeper in the red than its own budget allows (the item's own headline number).** Over 12 simulated months on the 5M fixture, ending funds with layout ON are not lower than ending funds with layout OFF by more than the layout capex budget booked plus the upkeep delta layout added (both reported by the passes). Today the shortfall is 3.7 times that.
- Test: engine test, 12 months, both modes, same seed. Directional and arithmetic: shortfall <= booked capex + cumulative upkeep delta (placeholder tolerance: 0 beyond rounding).
- Can-fail proof: the main-today code gives a shortfall of about 3.7 times the allowance; the test goes red there. If the ratio on main is not near 3.7 when measured, record the measured ratio on the item and keep the criterion.

**AC-6. Undo reverses what was booked.** `undoLastConsolidatorPass` refunds the layout capex including the induced spend it booked, and the upkeep delta (`upkeepDelta` already exists, `consolidator.ts:1822-1834`). After pass then undo, funds equal funds before the pass (exact).
- Test: engine test; run one pass, undo, compare funds.
- Can-fail proof: book induced spend but skip it in the undo refund; funds are lower after undo and the test is red.

**AC-7. The log tells the truth.** Each persisted `consolidatorLog` layout entry has `capexSpent` equal to its own cost plus its induced spend, and the pass's total line matches the sum. Frame check only if the log is shown on screen; otherwise engine test on the log.
- Can-fail proof: zero the field in the log push; the test fails (the same class of bug the field's own comment describes: "every persisted entry read capex 0 while millions of pounds moved").

**AC-8. The density and re-plan lanes stay correct.** The existing density-lane and re-plan booking (the "REAL spend" idiom at `engine.ts:6425-6431`, `capexSpent: exec.buildCost` at 5368) stays as is; the existing estate files `attack-inc3-round8`, `round9`, `round10`, `round11-*`, `round14-*` all stay green. Run them with the scoped runner.

## 5. Out of scope

- Re-tuning the capex ceiling fraction, the reserve months, the upkeep share or the tier shares. They are balance placeholders and stay with Aaron's row-by-row pass.
- The consolidator's density, merge and re-plan stages, except that their tests must keep passing.
- The Go engine; this is the webconsole TypeScript consolidator.
- Making layout cheaper or avoiding the stranding in the first place (a layout planner that keeps buildings connected). That would be a design change, not a booking fix; see Q2.

## 6. Unstated assumptions

1. The item means the reconnect lane's autoConnect spend when it follows layout in the same pass. The item title says "Road Auto-Connect spend induced by layout tiles"; the code shows only the reconnect lane and the density lane call autoConnect inside the consolidator, and the density lane already books it. If a third call site exists, AC-2 will expose it as a gap.
2. "3.7x deeper overdrawn" was measured on the round 11 dogfood fixture at 5M funds. I could not find the run, so AC-1 and AC-5 rebuild it.
3. The treasury going negative is real "overdrawn" in the player's eyes, not just the insolvency warning threshold (`INSOLVENCY_WARNING_THRESHOLD`). The floor math in `engine.ts:5033-5036` is built on that threshold.

## 7. Questions only Aaron can answer

**Q1. Which road spend counts as layout's?**
a) Only spend in the same pass, for buildings stranded by that pass's layout tiles (recommended: testable, bounded, no attribution drift).
b) Any reconnect spend in the following N passes for buildings next to layout tiles.
c) All consolidator road spend counts against one budget, whatever the cause.

**Q2. When induced road spend would break the budget, what should layout do?**
a) Place less (trim the last tile groups until the sum fits) and log it (recommended: matches how the capex gates already trim).
b) Skip the whole section.
c) Place the tiles and let the reconnect lane wait a pass.

**Q3. Is a shortfall against layout-OFF acceptable at all?**
a) Yes, up to the booked budget (recommended: that is the point of layout).
b) No, layout must be free of net treasury cost over 12 months.

## 8. Size

One lane. A fix in `engine.ts` (the budget sum at 5720 and the reconnect lane's booking) plus one new test file with AC-1 to AC-7, and the existing round 8 to 14 estate re-run. Risk: the layout stage is the most hardened part of the estate (rounds 8 to 16), so the destructive round must try funds exactly at the floor and a 10-pass run. If AC-1 shows no gap, the whole item closes without code.
