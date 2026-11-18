# Acceptance criteria: the reason panel and the ten-lever migration formula

Written 2026-11-18 by the business analyst. Documents only. Read against HEAD 466f873 plus the uncommitted tree. Code facts below were read in the files named. **File name note:** the budget hook refused the requested name `feat-9995385614-reason-panel.md` and only allowed a name containing "findings". The lead should rename this file to the requested name.

**Item note.** The task named FEAT-9995385614. On the Book of Work that code is the placement catalogue (Stage 1 lane C). The reason panel (Stage 1 lane A, `docs/planning/delivery-path-2026-11-18.md`) has no item of its own. The lead should file one and point this document at it. No BOW comment was posted on FEAT-9995385614 because it would sit on the wrong item.

Aaron's rulings (2026-11-18, FEAT-9995385609): the formula becomes **ten equal weights**, one term per lever at 10 percent: housing, jobs, services, health care, policy, shops, parks, transport links, congestion, tourism. The screen says why people come and why they leave. He looks only at the end, so the panel is the evidence for every later lever.

---

## 1. Today, from the code

### 1.1 The formula
- `attract.Weights` has seven fields (`internal/engine/attract/weights.go:19-27`): jobs, housing affordability, service coverage, environment, leisure fit, safety, reputation. Each in [0,1], sum 1 (`weights.go:51-73`).
- Values (`internal/engine/compose/compose.go:4464-4479`, `baselineOneAttractConfig`): jobs 0.20, housing 0.20, services 0.15, environment 0.10, leisure 0.10, safety 0.10, reputation 0.15. In code, not data.
- `A = sum(weight x term)` over six terms on a 0..100 scale plus `0.15 x reputation` (`attract/api.go:484-492`). Reputation is signed, clamped to plus or minus 100 (compose.go:240).
- `ApplyMigration` (`attract/migration.go:204-319`): advance reputation once a month, `score = weightedSum`, `score *= satisfactionMod` (wellbeing, `migration.go:281-282`), `net = migrationRate x (score - A_world)`, `A_world = 40`, rate 1.0 (compose.go:222-223, `G` at `api.go:533`).
- Net above zero admits migrants, capped at `min(JunctionThroughput, HousingVacancy x 2)` (`migration.go:350-352`). Net below zero removes residents by a personality-weighted hazard, capped at 2 percent of residents a month.
- `MigrationResult` carries only Month, A, AWorld, Net, Inflow, Outflow, Reputation (`migration.go:175-183`). The term values and the cap are not returned.

### 1.2 Where each term comes from

| Today's term | Source | Real today? |
|---|---|---|
| Housing affordability | `attract/api.go:421-462` (`snapshotTerms`): households affordability index from the posted wage bill and a monthly rent | Real. Placeholder: rent per household. |
| Home vacancy (a cap, not a term) | `compose_vacancy.go:36-73`, seed 120 people | Real since BUG-1214. Placeholders: seed 120, 2 per household. |
| Safety | `compose.go:3907-3921`, crime `AdvanceMonth` then `SafetyTerm` | Real, one input (population). |
| Environment | `compose.go:3949-3974`, refuse `Generate` then a half-saturation curve (`data/attract_terms.json`, 50000 kg) | Real, one input. Nothing collects waste, so it only falls. |
| Leisure fit | `compose.go:3929-3936`, entertainment zone becomes one venue, capacity 500 | Real, moves when the player zones entertainment. |
| Job availability | `servicesfirms_wire.go:59-98`, firms vacancy rate | **Dead.** Reads 0: no firm is founded except the builders' merchant. |
| Service coverage | `servicesfirms_wire.go:35-43` | **Dead.** Reads 100: nothing pushes demand. |
| Reputation | `attract/reputation.go`, momentum of the mean of the six terms, rise 0.2, fall 0.8 | Real. Baseline anchored at the first month. |
| Wellbeing multiplier | `compose_wellbeing.go` via `SetWellbeingModifiers` | Real; health and shopping inputs degraded. |

The terms are locals in `applyMigration` (`compose.go:3315-3380`), pushed with `SetTermInputs`, then lost. Two have monthly side effects that must still happen: `safetyTerm` calls `crime.AdvanceMonth`, `environmentTerm` calls `refuse.Generate`.

### 1.3 What the screen shows today
- Row 1 top bar. Row 2 the homes line (`chrome/housing.go:36-48`): `homes N places, N lived in, N free | moved in N, left N this month`, with `HOMES FULL - build dwellings (F3) to grow` leading when full. Published inside `chrome.topbar` (`chrome_publish.go:222`) from a month snapshot captured at the migration step (`housing_pub.go`, `recordHousingMonth`, called at `compose.go:3377`), cleared on load (`save_wire.go:378`).
- At 80x24: 2 chrome rows on top, hint row and status row at the bottom (`chromebar.go:31-37`, `screenhints.go:17`, `screenContentRect` `chromebar.go:135`). 20 content rows. 60 columns must also work.
- Nothing says why anyone came or left beyond the homes line. Status row: `F1 map F2 money F3 build F4 services` then clock keys (`statusbar.go:127`).
- Screens: F1 map, F2 finance, F3 build, F4 services (placeholder), F10 menu, F12 debug.

---

## 2. Design decisions

### 2a. The ten-term formula
**Decision.** `A = 0.1 x (housing + jobs + services + healthcare + policy + shops + parks + transport + congestion + tourism) + w_rep x reputation`. Then `score = A x wellbeing_factor`, `net = migrationRate x (score - A_world)`.

- Each lever value is **0..100** (the range attract already validates, `api.go:293`). Higher pulls, lower pushes.
- Each lever input is a pair **(Value, Built)**.
- **A lever that is not built contributes exactly A_world (40).** Its Value is ignored.
- Reason: with neutral = A_world and weights summing to 1, a city with every lever neutral, reputation 0 and factor 1 scores exactly 40 and has net exactly 0. Wiring one lever later changes only that lever's line. Its points are `0.1 x (value - A_world)`, which is also what the panel prints.
- The neutral value is read from the world pool (`attract.AWorld()`) at the same moment as the gap, never a second literal.
- Built or not is decided in the lever's own file by what it really reads. Day one: housing, services (partly), parks built; the other seven not.
- Ten weights live in data: a `weights` block in `data/attract_terms.json` (GR#15), replacing the inline literal. All 0.1. Validation: ten finite weights in [0,1] summing to 1 within 1e-6. Equal is the data, not a code rule, so a balance pass is a data edit.
- Fixed lever order in one array: housing, jobs, services, healthcare, policy, shops, parks, transport, congestion, tourism. No map is ranged (GR#21).

| Lever | Value today | Built today | Note |
|---|---|---|---|
| housing | households affordability index, unchanged | yes | Vacancy stays a cap (2b). |
| jobs | not read | no | `jobAvailabilityTerm` stays; lever returns Built=false. |
| services | mean of safety and environment | yes, partly | Coverage joins when the services lane lands. |
| healthcare, policy, shops, transport, congestion, tourism | none | no | Wage policy still moves housing through wages. |
| parks | leisure fit, unchanged | yes | The parks lane extends it. |

### 2b. Old terms and modifiers

| Old item | Decision | Reason | What changes |
|---|---|---|---|
| Housing affordability | the **housing** lever, unchanged | Only truly wired lever. | Weight 0.20 to 0.10. |
| Leisure fit | the **parks** lever, unchanged | Moves on entertainment zoning; tests depend on it (`feat167_attract_terms_test.go`). | Weight 0.10, same. |
| Job availability | **jobs** lever, **not built** | Dead (reads 0); counting a dead zero as a push punishes every city for a missing system. | The dead drag goes. |
| Service coverage | part of **services**, **not read yet** | Dead (reads 100). | The free +15 pull goes. |
| Safety, environment | fold into **services**: value = plain mean of its built inputs (safety and environment now; coverage added by the services lane) | Police and refuse are services. Keeps the population-grows-crime-rises-people-leave brake that `TestFEAT167_WorsenPopulation_LowersSafety_LowersNetMigration` pins, without an eleventh weight. | 0.10 + 0.10 become one 0.10 term. |
| Reputation | stays **outside the ten** as a signed kick `w_rep x reputation`, `w_rep` 0.15 in the same data block, own line | Detroit-trap mechanic, not a lever the mayor pulls. Dropping loses `reputation_test.go` and AC-5. | Its input becomes the mean of the **ten** effective values (not-built count as A_world), so a real swing moves it about 0.4 as far as before. |
| Wellbeing multiplier | stays, multiplies the score, own line | A modifier; health care and congestion feed it later. | Its effect is now printed: `(factor - 1) x A`. |
| Arrivals cap | stays a cap; panel prints it | Homes are the cap BUG-1214 set. Transport takes `JunctionThroughput` later. | Result gains wanted and room. |

Identity (testable, C5): `sum(lever points) + reputation points + wellbeing points = net / migrationRate`.

### 2c. One file per lever input
In `internal/engine/compose/`: `lever_housing.go`, `lever_jobs.go`, `lever_services.go`, `lever_healthcare.go`, `lever_policy.go`, `lever_shops.go`, `lever_parks.go`, `lever_transport.go`, `lever_congestion.go`, `lever_tourism.go`.
- Each defines one method: `func (st *simState) <name>Lever(month int64) (attract.LeverTerm, error)`.
- `lever_registry.go` holds the one ordered list and `st.collectLevers(month)`. Seven files start as a three-line "not built yet" stub; later lanes edit only their own file.
- Code that moves: `safetyTerm`, `environmentTerm` (compose.go:3907, 3949) to `lever_services.go`; `leisureFitTerm` (3929) to `lever_parks.go`; `jobAvailabilityTerm`, `serviceCoverageTerm` (`servicesfirms_wire.go`) to `lever_jobs.go`, `lever_services.go`. A pure move. **Call order and monthly side effects must not change:** safety (crime advance), leisure, environment (refuse generate), then the pure reads.
- After this lane `applyMigration` has no term code: `collectLevers`, `attract.SetLeverTerms`, `ApplyMigration`, record the publish.

### 2d. Data published each month
**Decision.** A new engine view **`f5.why`**, not new fields on `chrome.topbar` (chrome replaces its whole figures block each delta and has a two-row budget). Homes figures are included, built by the same function (`buildChromeHousing`): one notion of homes.

Capture at the migration step, beside `recordHousingMonth` (`compose.go:3377`), from the `MigrationResult` migration actually used, never re-queried (avoids the stale-snapshot race noted at `compose.go:3360`). Atomic pointer like `housingPub`. **Reset on load** beside `save_wire.go:378`; the view says "no month closed yet" until the next close. Files: `reason_pub.go` and one registration line (`compose.go:1866` loop).

Wire shape (schemaVersion 1; compose and the UI each keep their own copy, GR#20):

```
{ "schemaVersion":1, "known":true, "month":13, "aWorld":40.0,
  "levers":[ {"id":"housing","built":true,"value":71.0,"points":3.1,
              "parts":[{"k":"rent share","v":29}]}, ...ten, fixed order... ],
  "reputation":2.0, "reputationPoints":0.3,
  "wellbeingFactor":0.98, "wellbeingPoints":-1.0,
  "score":48.4, "net":8.4,
  "wanted":8, "room":62, "limitedBy":"homes", "movedIn":8, "left":0,
  "homes":{ same fields as chrome housing },
  "homeless":{"built":false} }
```
- `points` = `0.1 x (value - aWorld)` if built, exactly 0 if not. `value` is 0 and ignored when not built.
- `wanted` = whole-person net before the cap (negative when people want to leave). `room` = the cap in people. `limitedBy` = "homes" or "transport".
- `parts` carry the numbers behind a built lever; the screen owns the words.
- `homeless` is a reserved slot for FEAT-9995385613.
- Add to `MigrationResult`: per-lever effective value and points (fixed array of 10), `Wanted`, `Room`, `LimitedBy`, `ReputationPoints`, `WellbeingFactor`, `WellbeingPoints`, `Score`.

### 2e. Where the panel lives and what it looks like
**Decision.** A new screen on **F5 "WHY"**, package `internal/ui/screens/why`. Reason: the map and build screens use every row; F4 is an empty placeholder but belongs to the services lever. The screen has 20 content rows and needs about 17.

`statusBarScreens` becomes `F1 map F2 money F3 build F4 services F5 why` (43 columns, fits at 60). F5 hint row: `F5 WHY  view only | F1 map: y buy cell | F3: b build`. The homes row (row 2) is **unchanged**.

Lever line: `NAME(16) BAR(12) VALUE(4) WORD(14) POINTS(5) WHY`. Bar `[` + 10 cells + `]`, filled = value/10 rounded. Not built: dotted bar, value `--`, word `not built yet`, points `0.0`, no WHY. Words: `pulling` more than 1.0 above A_world, `pushing` more than 1.0 below, else `neutral`.

**Exact 80-column text**, sample city (month 13: housing 71, services = mean of safety 90 and clean 80 = 85, parks 55, reputation +2.0, wellbeing 0.98, 8 wanted, room 62, 120 places, 89 lived in). Longest line 75 columns; 17 of 20 rows:

```
WHY PEOPLE COME OR GO   Mar Y2   moved in 8, left 0 this month
LEVER           SCORE         VAL WORD            PTS  WHY
Housing         [#######---]  71 pulling        +3.1  rent is 29% of pay
Jobs            [..........]  -- not built yet   0.0
Services        [#########-]  85 pulling        +4.5  safe 90, clean 80
Health care     [..........]  -- not built yet   0.0
Policy          [..........]  -- not built yet   0.0
Shops           [..........]  -- not built yet   0.0
Parks           [######----]  55 pulling        +1.5  1 entertainment venue
Transport links [..........]  -- not built yet   0.0
Congestion      [..........]  -- not built yet   0.0
Tourism         [..........]  -- not built yet   0.0
Momentum        reputation +2.0, beyond the ten +0.3
Wellbeing       city mood scales score x0.98    -1.0
TOTAL           score 48.4 vs world 40.0        +8.4
Wanted 8, room for 62 (limit: free homes), moved in 8
Homes 120 places, 89 lived in, 31 free
Homeless        not built yet
```
Check: 3.1 + 4.5 + 1.5 + 0.3 - 1.0 = 8.4. When people want to leave: `Wanted to leave 5, left 5`, room clause dropped. Before the first close: title and `no month closed yet`.

Spare: 3 rows. The homelessness meter fills the reserved last row and may take one more. No scrolling. If a later lane needs more, drop the WHY text, not lines.

**Cut order as width falls:** (1) below 76 columns the WHY text goes (every line then fits in 52); (2) below 52 the bar goes; (3) below 40 the word shortens (`pull`, `push`, `neut`, `none`) and the title becomes `WHY`; (4) the ten names, points and TOTAL are never cut. **At 60 columns:** every line, no WHY column, title loses "this month". **Short terminals:** loss order is homeless row, homes row, wanted row, momentum row, wellbeing row, then the column-header row. Ten levers and TOTAL are kept.

---

## 3. Acceptance criteria

Test kinds: **U** attract unit; **C** compose test on the default composition (`Wire` as boot uses it), control against treatment; **F** `cmd/metropolis` frame test at 80x24 on the real boot path (pattern of `bug1220_housing_frame_test.go`); **S** save/load with a fresh Composition and `LoadAt`; **G** gate (detgate, converge, lint tools).

### Formula (attract)
- **C1 (U).** Ten weights come from data, each 0.1, sum 1. Nine or eleven weights, sum outside 1 +/- 1e-6, NaN, or outside [0,1] is refused with `ErrInvalidWeights`, no state change. Observation: parse of shipped `data/attract_terms.json` gives ten values of 0.1.
- **C2 (U).** Neutral city: all ten Built=false, reputation 0, factor 1: `A == AWorld` exactly, `Net == 0`, no inflow or outflow. Uses `==`.
- **C3 (U).** Not-built cannot move migration. For each lever, run the same month with (Built=false, Value=0) and (Built=false, Value=100): `MigrationResult` identical in every field.
- **C4 (U).** Isolation and weight. Raise one built lever by 10, all else fixed (reputation 0, factor 1): `Score` rises by exactly 1.0, that lever's points by 1.0, every other lever's points unchanged. Repeat per lever index.
- **C5 (U).** Identity: for 200 fixed-seed random inputs, `sum(points) + ReputationPoints + WellbeingPoints` equals `Net / migrationRate` within 1e-9.
- **C6 (U).** NaN, infinite, below 0 or above 100 is refused with `ErrInvalidTermInput`, no state change, even when Built=false.
- **C7 (U).** Reputation uses the mean of the ten **effective** values. An all-neutral city keeps reputation exactly 0 for 36 months. The fall-faster-than-rise tests keep their meaning; numbers are re-pinned, direction is not.
- **C8 (U).** Cap unchanged. `Wanted` = whole-person net before the cap; `Room` = `min(JunctionThroughput, vacancy x 2)`; `Inflow = min(Wanted, Room)` rounded down to whole households (2 people), as `migration.go:350-357`. `LimitedBy` = "homes" when vacancy x 2 is smaller. Vacancy 0 gives `Inflow == 0`, `Room == 0`.
- **C9 (U).** Determinism: two APIs, same config and seed, same inputs for 24 months, byte-identical serialised results each month. Fixed-length arrays, no maps; astgate/GR#21 green.

### Wiring (compose)
- **C10 (C).** Published equals used. On the default composition after each of 12 month closes: patch `net` equals the `Net` of that month's step (test-only accessor on the last result); every lever's `points` and `value` equal the result's; `movedIn - left` equals the population change over that step; `score - 40` equals `net / migrationRate`.
- **C11 (C).** Day-one built flags: `built == true` for exactly housing, services, parks; false for the other seven, in fixed order; unbuilt `points == 0`.
- **C12 (C).** Unbuilt cannot move migration in the real composition. Control against treatment where the jobs source is pushed hard (register firms with vacancies via `engine.firms` so `jobAvailabilityTerm` exceeds 50): months 1 to 12 give the same net, population and state digest. Repeat for services coverage (register a service and demand).
- **C13 (C).** Housing as BUG-1214 established, **restated directionally.** The old exact pin (control 148 against six homes 190 at month 12) is **not carried over**. Keep: treatment (six dwellings) population at month 6 above control; control population at months 1 to 3 at least 50; `room` larger in treatment in the first month after the build completes; `limitedBy == "homes"` in control from the first month `homes.full` is true; the housing lever `value` identical in control and treatment when only dwellings differ (it is the rent index, not stock). The new measured table goes on the item and in the evidence folder.
- **C14 (C).** Other built levers, control against treatment, directional: zoning entertainment raises the parks value and points; growing population lowers the services `safe` part and the services points. Keeps the intent of `TestFEAT167_LeisureFit...` and `TestFEAT167_WorsenPopulation...`.
- **C15 (C).** In a 12-month run `crime.AdvanceMonth` and `refuse.Generate` are each called exactly once per month, safety first. A lever that skips its call when "not built" fails.
- **C16 (C).** Structural: the ten `lever_*.go` files exist; each defines exactly one `<name>Lever` method; `compose.go` contains none of `safetyTerm(`, `environmentTerm(`, `leisureFitTerm(`; `applyMigration` calls `collectLevers`.
- **C17 (C).** Two compositions, same seed and commands, 24 months: equal state digests and byte-equal `f5.why` patches each month. No wall clock, no map range.

### Saves
- **C18 (S).** Save at month N after a close, `LoadAt` into a fresh Composition: patch `known:false` straight after load; after the next close its `levers`, `net`, `movedIn` equal an uninterrupted control's. Reputation round-trips exactly.
- **C19 (S).** An **old** attract save (no formula marker) loads. The attract meta gains a marker (`leverFormula`, a pointer so absence means old, as with `MigrantAdmittedMonths`). On an old record reputation re-anchors (`hasBaseline=false`, value 0), because the old baseline was a mean of six. Hand-built old blob: reputation 0 after the first month, no jump. A modern save keeps its reputation exactly.
- **C20 (S).** Old journals. A journal recorded before this change replays to different populations from the first month with migration. This lane does **not** claim old-journal equality. Every checked-in journal or fixture crossing a month close with migration is listed on the item and re-recorded or tagged with the rule version from FEAT-9995385612 when that lands. No previously valid command is newly refused, so there is no refusal to explain.

### Screen
- **C21 (F).** At 80x24 on the real boot path F5 shows the panel: each of the ten names exactly once, in order; every line at most 80 columns; no more than 20 content rows; chrome, hint and status rows unmoved; before the first close `no month closed yet`. Built levers show bar, value and pulling/pushing/neutral; unbuilt show `not built yet` and `0.0`.
- **C22 (F).** TOTAL equals the published net to one decimal; lever points plus momentum plus wellbeing sum to TOTAL within 0.1 (rounding).
- **C23 (F).** Live response, control against treatment, real path: (a) six dwellings: the `room for N` figure is larger than control the month after the build completes; (b) entertainment zone: the Parks value is higher than control; (c) 12 months of growth: the Services value falls from its first reading. Directional only.
- **C24 (F).** Row 2 is byte-identical to today for the same state (BUG-1220 frame tests green unmodified); the panel's Homes row shows the same three numbers as row 2 in the same frame.
- **C25 (F).** At 60x24 every line at most 60 columns, ten names and TOTAL present, WHY gone. At 40x24 bars gone, nothing wraps. At 20x6 no panic, order kept as far as rows allow.
- **C26 (F).** Status row reads `F1 map F2 money F3 build F4 services F5 why` on every screen at 80 and 60 columns. F5 switches from every screen, including with a leader sequence pending. The F5 hint exists and every key it names works.
- **C27 (F).** The screen draws nothing on a struct-copied receiver (copyguard pattern of `chrome.RenderHousing`); a corrupt patch (bad schema version, wrong lever count, NaN) is rejected with a registry error and the last good frame stays.

### Registry and gates
- **C28 (G).** New error codes only through `tools/plan/add-error.js` (claim-range, add, check), pre-assigned in the lane brief, owner chosen so the Go scanner stays green. New module and screen keys (`ui.screen.why`, any compose publish key) registered via the master plan and `/register-guid`; `/codejson-audit`, `/spec-lint`, `/edge-lint` green.
- **C29 (G).** The 0..100 lever value and "points" are registered in `code.json` units; `/units-lint` green.
- **C30 (G).** `/ci-green` verbatim, astgate, golangci-lint, determinism gate, converge tests and the 1M perf gate are green after re-pins. The ten-lever compute is a fixed ten-step loop per month, no per-citizen work. No "known red" skips.
- **C31 (G).** Go only. `git diff --stat` shows nothing under the web console tree and no protocol message-type change (a new view name is not a message).

---

## 4. The can-fail proof

The developer applies each mutation, shows the named test red, and removes it. Record the red output in the BOW comment.

| Crit. | Mutation that must turn it red |
|---|---|
| C1 | One weight 0.15 in data; remove the sum check; accept a missing weight as 0. |
| C2 | Neutral value 0 or 50 instead of A_world. |
| C3 | Ignore Built and use Value. |
| C4 | Swap two weight indexes, or housing weight 0.2. |
| C5 | Apply the factor before reputation, or omit wellbeing points. |
| C6 | Skip validation when Built=false. |
| C7 | Feed reputation the mean of built levers only. |
| C8 | `max` instead of `min`, or drop vacancy. |
| C9 | Build the per-lever result through a map and range it. |
| C10 | Capture the snapshot before `ApplyMigration`, or recompute a lever after the step. |
| C11 | Jobs lever returns Built=true. |
| C12 | Jobs lever returns Built=true, or reads firms vacancy while Built=false. |
| C13 | Remove the vacancy cap; housing lever reads stock not rent index. |
| C14 | Parks lever constant; services ignores population. |
| C15 | Skip the crime/refuse call when unbuilt, or call twice. |
| C16 | Put `safetyTerm` back in compose.go; add an eleventh file; rename a method. |
| C17 | Seed a draw from the wall clock; range a map in the publisher. |
| C18 | Do not reset the snapshot on load; drop reputation from the attract save. |
| C19 | Skip the re-anchor on an old record. |
| C20 | Documentation criterion: the fixture list must match `git grep` of the fixture directories. |
| C21 | Wrong lever order; a 90-column line; drop the `known:false` message. |
| C22 | Print net from a second computation that omits the wellbeing term. |
| C23 | Ignore dwellings in `room`; fix parks constant; freeze services. |
| C24 | Change one character of the homes row. |
| C25 | Leave WHY at 60 columns; wrap instead of cut. |
| C26 | Do not register F5; leave F5 out of the status row. |
| C27 | Remove the copyguard; accept schemaVersion 2. |
| C28 | Hand-edit `errors.json`; leave the screen out of the master plan. |
| C29 | Use a unit string not in the registry. |
| C30 | Add a map range to the lever collection (astgate must object). |
| C31 | Diff check. |

---

## 5. Fallout

Rule: a test pinning an exact migration number is re-pinned on the new formula with a comment saying why; a test pinning a direction stays and passes unchanged. Counts are files found by `git grep` for the attract API and the migration result. Some need no change. Running the full packages is the only proof; the developer lists the final set in the BOW comment.

**attract package (18 files).** `helpers_test.go`, `s6_endtoend_test.go`, `weights_test.go`, `worldpool_test.go` build the seven-weight config or JSON and must change to ten. Numeric `A()` and `Net` pins likely move in `terms_test.go`, `migration_test.go`, `wellbeingmodifiers_test.go`, `reputation_test.go`. Touched by the API change (`TermInputs` fields and the five getters `JobAvailability()`, `ServiceCoverage()`, `Environment()`, `LeisureFit()`, `Safety()` are replaced): `attack_participant_test.go`, `bug380_fair_selection_test.go`, `bug380_tenure_grace_test.go`, `bug517_agedist_test.go`, `conservation_test.go`, `feat_1972079927_migrantwealth_test.go`, `float_safety_test.go`, `numeric_test.go`, `participant_test.go` (also gains the C19 test), `attack_bug380_round_test.go`.

**compose package (about 17 files).** `feat167_attract_terms_test.go` (direction tests must survive, C14), `servicesfirms_wire_test.go` (reads the getters, re-point at the lever functions), `bug1214_housing_vacancy_test.go`, `bug1214_harness_test.go`, `bug1214_saveload_test.go`, `bug1220_housing_pub_test.go`, `bug380_emigration_migrants_test.go` (its "fully-floored term scenario" is rebuilt with levers pinned low), `attack_bug380_round_test.go`, `attack_loadat_attract_test.go`, `attack_mod034_apply_round_test.go`, `coastal_wire_test.go`, `compose_ledger_participant_test.go`, `compose_test.go`, `feat_1972079927_moneycirc_test.go`, `save_roundtrip_test.go`, `save_roundtrip_teeth_test.go`, `wellbeing_wire_test.go`. Tests that assert the registered view-name set (`bug333_test.go`, `viewport_publish_test.go`, `services_publish_test.go`, `chrome_publish_test.go`, if they list names) change for `f5.why`. Population and digest pins anywhere in the 72 `Wire`-calling compose test files may move; run the whole package.

**cmd/metropolis (about 5 files).** `bug322_tickdriver_test.go`, `bug1220_hints_frame_test.go`, `bug1220_r2_narrow_frame_test.go` assert the status row (`statusBarScreens`), which gains `F5 why`. The router and priming tests (`feat208_*`, `router_testutil_test.go`) enumerate screens and views and gain F5.

**Goldens and evidence.** `docs/planning/evidence/feat-083/bug1220/*80x24.txt` hold the old status row (evidence, not gates; re-capture). `internal/harness/uitest/testdata/snapshots/*.golden` (2 files) may contain the status row or a population figure: check, regenerate if so. Converge fixtures `internal/converge/testdata/{finance,population,services}-webconsole-v1.json` come from the TypeScript console; the Go converge tests compare against them. Expected unaffected (that domain exercises citizens and its own seed, not attract migration). Verify by running `internal/converge`; any movement is a finding to report, not a fixture to regenerate blindly. The state digest does not include attract state (`state_digest.go:83-200`), so digests move only through population.

**Web console TS sim.** Not affected (C31).

**Count to quote:** about 18 + 17 + 5 + 2 checks = **about 42 files**, perhaps half mechanical (the helper that builds the config). An estimate until the packages are run.

---

## 6. Out of scope, assumptions, questions

**Out of scope.** Wiring any other lever. The homelessness meter itself (only its reserved row). Start-empty and the 3M pot. The placement catalogue and rule version in the save header (Stage 1 lanes B and C). A real `JunctionThroughput`. Any balance change to the 40 baseline, migration rate or reputation rates. Any TypeScript change.

**Assumptions the developer need not ask about.**
- Neutral equals A_world (40): it only sets what "no effect" means, and the panel prints it.
- Jobs and services lose their dead values (0 and 100). This changes the feel of the default city; it is not a bug; C13 asks for the new table to be recorded.
- Migration rate stays 1.0, so points and people are the same number.
- Equal weights live in data so Aaron can unequal them later without code.
- No Go-TypeScript wire-parity test is needed (a view name is not a message type).
- The panel is view-only.

**Questions only Aaron can answer.** Four. Each has a recommendation; the lane can start on the recommended answer.

1. **Where do safety and environment go, now there is no seventh and eighth term?**
   - A. Fold them into the services lever as its two live inputs until coverage joins (recommended: police and refuse are services, and crime rising with size keeps its brake on growth).
   - B. Keep them as two extra modifiers outside the ten, on their own panel lines (keeps today's brake at full strength, but ten equal weights is no longer the whole story).
   - C. Drop them from migration (simplest, loses the brake).
2. **Does reputation stay?**
   - A. Keep it as a separate "momentum" line outside the ten, weight 0.15 (recommended: it is the Detroit-trap mechanic, not a lever).
   - B. Remove it (the rise and fall behaviour is lost, many tests rewritten).
   - C. Keep it as a visible eleventh lever the mayor cannot pull (confusing).
3. **Where does the panel live?**
   - A. Its own screen on F5 (recommended: all 20 rows, no cost to the map).
   - B. Replace the empty F4 services screen (saves a key, but the services lane needs F4 back).
   - C. An overlay on the map toggled by a key (hides the map while you read).
4. **What does an old save do?** Your ruling says saves replay under their rule version, and this change makes the seven-term formula obsolete.
   - A. Old saves load and carry on under the new formula from the load point; reputation re-anchors once; an old journal replayed from genesis will not reach the same population (recommended: pre-release game, and the rule-version lane can still label them).
   - B. Keep the old seven-term path alive, chosen by rule version (replay exact, but two formulas to carry and test, and the old one is dead code the day it ships).
   - C. Refuse saves from before this change (clean, but strands every existing save).

---

## 7. Size and lanes

**Size guess.** Medium to large. Attract about 600 lines with tests; compose (ten small files, registry, publisher, view) about 800 with tests; UI (one screen package, key, status row, frame tests) about 500. The re-pin of about 40 files is mostly mechanical. Estimate 3 to 4 developer-days including 3 to 5 review rounds, about 1.5 elapsed days with the three lanes in parallel. A guess, not a measurement.

**Lanes with no shared files** (interface fixed in 2a, 2c, 2d: `attract.LeverTerm{Value float64; Built bool}`, the ordered `LeverID` list, the `MigrationResult` additions, the JSON above).

| Lane | Files | Depends on |
|---|---|---|
| A1 attract | `internal/engine/attract/*`, `data/attract_terms.json`, error codes | none; lands first or publishes the API stub on day one |
| A2 compose | `internal/engine/compose/lever_*.go`, `lever_registry.go`, `reason_pub.go`, the move out of `compose.go` and `servicesfirms_wire.go`, view registration, `save_wire.go` reset, compose tests | A1's Go signatures |
| A3 screen | `internal/ui/screens/why/*`, `cmd/metropolis/{boot.go,boot_screens.go,statusbar.go,screenhints.go,chromebar.go}`, frame tests, master-plan entry | the JSON shape only; builds against a hand-written patch fixture |

`compose.go` is edited by A2 only (the `applyMigration` body and the config function). Stage 1 lane C edits a different part of that file (`KindBuild`, near line 4331): land one, rebase the other. Order A1, A2, A3 for the final integrated frame test and C23, which needs all three.
