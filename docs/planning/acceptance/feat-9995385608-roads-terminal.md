# FEAT-9995385608: Roads in the terminal game (Baseline One, Stage 5 item 1, pulled forward)

Written by the business analyst, 2026-11-18, at HEAD 6d109c1. Documents only. Facts come from the code (file:line given). Where the code does not say, the text says "not found" and it is a question.

## Aaron's ruling (2026-11-18)

"Placing a dwelling lays a road automatically; the player can also lay a road where they wish and then place a house on it." Playing that evening he asked "how do I lay down roads". The honest answer was: you cannot. There is no road command and no key.

## 0. What this item is, in six lines

1. A road becomes a thing the player can order: one key press at the map cursor orders one road cell. It is paid for, it takes a few days to build, it shows on the F1 map, and it survives save and load.
2. A road is a property of a cell, kept by the build module in its own set. It is NOT a building. A road and a home can share a cell.
3. Placing a dwelling also lays one road cell beside it, unless a road is already on or next to the home's cell. The player pays for that cell as part of the same press.
4. Nothing in the engine reads a road today and nothing will after this item, except the map. A home does not need a road to count (section 1.4). So this item changes what the player sees, orders and pays. It does not change who moves in. The transport-links lever, later, is what reads roads.
5. Roads are not traffic. No routing, no congestion, no junction types, no tiers, no bridges.
6. Two lines of Aaron's words have two readings (section 2.3). The engine rule below holds for both, so the answer can wait without blocking lane 1.

Be clear to Aaron: after this item the player can see and pay for roads, but a city with no roads and a city with fifty grow at the same speed. That is true until the transport lever (Stage 5 item 2). The roads are "real" in the sense that the lever has something honest to read.

## 1. Today, from the code

### 1.1 Roads in the engine

| Fact | Where |
|---|---|
| The world's cell holds elevation, slope, surface, owner, zoning, structure reference, land value, overlay scratch. It has NO road field. | `internal/engine/world/types.go:133-142` |
| There is no base-map road data in the Go engine. OS Open Roads is not imported; the world package says so. `data/terrain50/` holds terrain grids only. | `internal/engine/world/georef_verify.go:11-14`, `offmap.go:109-111`, `data/terrain50/` listing |
| `data/roads.json` is a class ladder (11 classes, lanes, speeds, `baseCostPounds`), not geometry. Gravel is 1 lane, 20 to 40 kph, 800 pounds; alley 500; residential street 2,000. It says its numbers are placeholders. The unit of `baseCostPounds` (per edge or per cell) is not stated: not found. | `data/roads.json` (`classes`), `internal/engine/roads/config.go:60-71` |
| A full road module exists: `engine.roads` (nodes, named edges, class ladder, upgrades, roadworks, naming). Nothing imports it except itself. It is not in the composition. | `internal/engine/roads/doc.go`, `api.go:158-290` (AddNode, AddRoad); importer search of `internal/` and `cmd/` found none |
| The viewport publisher says so itself: "Road is always omitted: no module composed by Wire writes a per-cell road today". | `internal/engine/compose/viewport_publish.go:137-143` |
| The traffic module is composed (daily reset hook) and extcommute reads congestion from it. It has no road geometry to read. | `compose/traffic_wire.go:1-60` |

### 1.2 How the F1 map draws

The map screen has one road glyph, `+` (`internal/ui/screens/map/render.go:67`), drawn when a cell's `Road` string is non-empty. A building draws `#` and wins on a shared cell (`render.go:535-548`, `cellStyleAndRune`). The wire cell carries `road` and `building` strings (`map/patch.go:35-42`). The engine fills `building` from the world's structure reference (`viewport_publish.go:144`, `structureLabel` at `:198-202`) and never fills `road`. So today a `+` can never appear in the terminal game.

F1 at 120x30: the screen gets 30 rows minus top bar, homes row, hint row and status row, so 26 rows (chrome constants `cmd/metropolis/chromebar.go:31-38`, hint row `screenhints.go:15`). The fly-out menu will take one more row (25). A build refusal shows on the map's notice line (`map/render.go:293`, `drawBuildNotice`); an ACCEPTED command shows nothing: `ApplyResult` clears the notice on accept (`map/screen.go:671-674`) and `CommandResult` carries no text except a `Result` blob that is empty for build (`protocol/envelope.go:221-235`).

### 1.3 The build path a road order would take

1. Keys on F1 today: arrows, `y` buy, `b` build a dwelling, `c` clear notice (`cmd/metropolis/boot.go:1157-1179`). Letter `r` is not bound anywhere in `cmd/metropolis` (key search). The hint row names every key that works and a test feeds each advertised key into the live grammar (`screenhints.go:15-20`, text at `:20`).
2. `b` sends `protocol.BuildPayload{Cell, BuildingType}` as `KindBuild` (`boot.go:1204-1212`, payload `protocol/commands.go:226-229`).
3. `compose.handleGameplay` `KindBuild` case (`compose/compose.go:4358-4375`) maps the cell with `cellFromRef` (200x200 cells of the start tile only, `:4463-4470`) and calls `buildAPI.SubmitBuildCommand` with `Zone: build.ZoneType(p.BuildingType)`.
4. `SubmitBuildCommand` (`build/build.go:696-751`): zone must be in the catalogue (else MET-G501 `ErrUnknownZoneType`), month valid, tile owned by the player (`requireOwned`, `:636-658`; ownership is per 2 km TILE, so one `y` owns all 200x200 cells), seasonal multiplier read, then under the lock the BUG-1224 check (`:737`, `rules.Has(b.activeRules(), rules.RefuseOccupiedCell)` and `cellInUse`), then the order is appended with the zone's materials, labour and lead time.
5. The zone catalogue is closed to exactly the eight zone types: any other id in `data/buildings.json` `zones` fails the load (`build/zone.go:170-184`). So "road" cannot simply be a ninth zone row in data.
6. On completion the order writes `zoneState[cell]` and `structures[cell]` and calls `world.SetStructure` so the map shows it (`build.go:1090-1100`). `StandingCapacities` reads `zoneState` and the zone's capacities (`build.go:1585-1617`).
7. `cellInUse` (`build.go:755-766`): a standing structure, or any queued order not yet complete on that cell, holds the cell. A queued ROAD order must not count here or a road would block the home Aaron wants on it (section 2.3).
8. Money: a build order draws construction materials; the ledger pays them at the construction price per tonne, NET-90 (`compose/moneycirc_inc2.go:55-109`; 150 pounds a tonne, so a 100-tonne dwelling is 15,000 pounds as BUG-1224 states). A road order can ride this unchanged.
9. Saves: `build.NewSaveParticipant` (`compose/save_wire.go:103`) saves meta, orders (with their `zone` string), zones, structures, demands, demolitions as record kinds (`build/participant.go:84-195`). A new set of road cells needs one new record kind. An order whose zone string is `road` already fits `buildOrderWire` (`participant.go:116-132`).

### 1.4 What a dwelling needs to count

Nothing checks road access. Housing stock is the seed city's 120 people plus the resident capacity of every standing structure build reports (`compose/compose_vacancy.go:12-60`, seed constant `:21`); `housingVacancyFor` reads only build's capacity list and the population. A search of `compose_vacancy.go` and `compose_housing.go` for road, access, adjacency, frontage, connect found nothing. So "placing a dwelling lays a road automatically" is, today, about what the player sees and pays, and about what the transport lever can later read. It does not unlock the dwelling.

### 1.5 Saves and replay

- A journal frame is one command plus the rule version it was accepted under (`compose/persistjournal.go:170-285`, `encodeJournalFrame`). Frames from before FEAT-9995385612 mean version 0. The registry holds one entry today: version 1, `RefuseOccupiedCell` (BUG-1224), wired, with an old-journal fixture at `internal/engine/compose/testdata/rules/v1` (`internal/engine/rules/registry.go:49-63`, `rules.go:19,32`).
- A new rule must provide (package doc `rules/doc.go`, "How a rule change is made"): the next number; a named constant; one registry entry with date, item, old and new text and fixture path; a call site that branches with `rules.Has(active, rules.Const)` and KEEPS the old branch; an old-journal fixture; status wired AND `Current` raised in the same commit as the call site. The registry tests fail on a literal version number, an entry with no call site, a call site with a pending entry, or a missing fixture (`rules/registry_test.go`, `TestRegistry_LiveTree`, `TestValidate_CanFail`, `TestScanTree_CanFail`).
- What counts as a rule: only a change to what a player's COMMAND does, that is, whether it is accepted and what it costs (`rules/doc.go`, "What gets a rule version"). Refused commands are never journaled (`feat-9995385612-rule-versions.md` section 5.2).
- A save made under a rule version newer than this build knows is refused, never clamped (`rules/doc.go`, "Versions in saves"; `rules.Classify`).
- Density stacking (FEAT-9995385606) is planned as the next rule version after BUG-1224 (`feat-9995385612-rule-versions.md` section 5.2). Its code and this feature both edit `SubmitBuildCommand`. This feature takes the next free number at the time it lands; the registry test forces contiguous numbers.

### 1.6 What the browser did (ideas only; the browser simulation is frozen, FEAT-9995385626)

FEAT-1972079910 found that drag-laying left 293 disconnected road fragments, 152 of them single crumbs, because pointer samples skipped cells. Its answer was to compute the path between two points instead of trusting samples. Lessons used here: one cell per command (no sampling), and a line tool that computes the cells itself.

## 2. Design decisions

### 2.1 What a road IS: a property of a cell, kept in engine.build (RECOMMENDED)

The build module keeps a sorted-on-save set of road cells, `roadCells`, next to `structures`. A road is ordered through the existing build queue (so cost, lead time, committed cost, the F3 queue and saves all work), but on completion it writes `roadCells`, not `structures`/`zoneState`.

Why this is the smallest honest thing:
- No new module, no node or edge ids, no naming, no footprint maths.
- The transport lever can read it with one accessor: `HasRoad(cell)` and `RoadCells(tile)` (sorted). It can build adjacency itself.
- It never holds a home's cell.

Alternatives rejected:
- A ninth zone "road". The catalogue is closed to eight (`zone.go:170-184`), a road would be a structure and so block a home on its cell, and `world.SetStructure` would show it as `#`.
- A field on `world.Cell`. Changes the world's save shape and every tile for one bit.
- An edge between cells (the `engine.roads` module). It needs nodes, ids, naming, a Bresenham footprint and a composition wiring nobody has done (`roads/api.go:158-290`). Right for the real road network, far too big now. The cell set can be migrated into it later because it holds exactly what the module's footprint holds: a list of cells. The road class (gravel) comes from `data/roads.json` and is the same for every cell in this item.

The road's economics (tonnes of materials, labour days, lead days, class id) live in a new `data/roadlay.json` with its own loader, not in `data/roads.json`. Reason: whether the `engine.roads` loader tolerates extra keys is not found, and that file is owned by an unwired module. See question 4 for the number.

### 2.2 How the command travels: `BuildingType "road"` on the existing Build command (RECOMMENDED)

`BuildPayload.BuildingType` is already an opaque string (`protocol/commands.go:226-229`). Compose checks for `"road"` first and calls a new build method, `SubmitRoadCommand`. No protocol change, no new kind.

Alternative rejected: a new `KindLayRoad`. It touches the protocol, `core/commands.go`, `core/engine.go`, `integration/queue.go` and `stub/engine.go` (the files that name `KindDemolish`) for nothing the string does not give. It remains the right move if roads ever need a payload the string cannot carry (a path).

The placement-catalogue lane (FEAT-9995385615) edits the same `KindBuild` case. The road branch is a three-line `if` at the top of the case that calls a function in a new file, so the two merge as different hunks, and the lane that lands second rebases.

### 2.3 "Place a house on it": two readings, one engine rule

Aaron's words are "lay a road where they wish and then place a house on it".

Reading 1, same cell. The home goes ON the road cell.
Reading 2, beside. The home goes on a cell next to the road ("on" meaning "along").

The engine rule that serves both: a road never holds a cell. `cellInUse` ignores road orders and road cells, so a Build dwelling is accepted on a road cell, and a road is accepted on a cell with a home or a queued home. Nothing in the engine forbids Reading 1, and nothing requires it. This is what lane 1 builds.

What differs is what the player sees, so he should choose by looking at a frame (120x30, `=` road, `#` home, `.` nothing, cursor shown as `[ ]`):

```
Reading 1 (house on the road cell; '#' wins the glyph, so the road is hidden under it)
 y11  . . = = = = = = = . . .
 y12  . . = # = # = # = . . .     homes sit IN the street; you see the road only between them
 y13  . . . . . . . . . . . .

Reading 2 (house beside the road; the player puts homes next to the line)
 y11  . . # . # . # . # . . .
 y12  . . = = = = = = = = . .     a street, homes along it
 y13  . . . . . . . . . . . .
```

RECOMMENDED answer to Aaron: allow Reading 1 (do not refuse) and make the automatic road land BESIDE the home (section 2.4), so the default look is Reading 2 and nobody is stopped from Reading 1. Reason: refusing a home on a road cell would be a refusal nobody asked for and would add a rule-version entry; allowing costs nothing. If Aaron says "beside only", the refusal is one extra check in lane 1 and one rule entry; the cost is stated in question 1.

How a shared cell draws: a home wins (`#`), as today (`map/render.go:535-548`). The inspect text for a cell with both says "home on road". Rejected: a third glyph for the shared cell. At one character per cell there is no honest combined sign, and the second glyph costs a legend entry.

### 2.4 The automatic road: one cell, beside the home, predictable (RECOMMENDED)

When a Build dwelling is accepted:
1. If the home's own cell, or any of its four neighbours (north, east, south, west), already carries a road (completed or queued), do nothing and charge nothing extra.
2. Otherwise queue ONE road order on the first FREE neighbour in the order SOUTH, EAST, NORTH, WEST. South is the next row down on the screen.
3. "Free" means: inside the start tile's 200x200 cells; no road, completed or queued, on it; no standing structure and no queued non-road order on it.
4. If no neighbour is free, the home is still built, no road is laid, and the player is told on screen. The home is NOT refused.

Who pays: the player, for the road cell, exactly as for any road order (section 2.5), in the same press. The F3 queue shows the home and the road as two rows.

Why this rule: a player can predict it with no map in his head ("below the home, else right, else above, else left") and it needs no path-finding, no shared state, no randomness. It is also stable for replay: the answer depends only on the state at the moment of the command.

Why not refuse the home when no road can be laid: nothing reads roads yet (section 1.4), so a refusal would punish for a rule the engine does not use. It would also be a newly refused command, which is a rule-version burden for no benefit.

Alternatives rejected:
- Road on the home's own cell. Always possible, no "no room" case, but under the "building wins" glyph the player would never see the road he paid for.
- A path to the nearest existing road. Needs a search, a tie-break, a cost that varies by distance and a refusal when none is reachable. It is the right long-term behaviour for the transport lever; it is a later item with its own criteria. Single stubs then become a network by the player laying roads between them (the lines tool, lane 3).
- A fixed compass order of north first. The choice is arbitrary; south is picked because it is one fixed, tested order. Aaron can change the order in one line and one test (question 3).

Only dwellings do this (his words). Shops, offices and the rest do not. Question 3 asks.

Isolated stubs will be many one-cell roads. The browser had the same shape (the "crumbs") and the fix there was a line tool. The transport lever decides what counts as "connected"; this item stores cells and does not judge.

### 2.5 What a road costs: a placeholder, taken from the build economics, and a question

No per-cell road cost exists. `data/roads.json` has a per-class `baseCostPounds` with the unit not stated. The only money path in build is materials times the construction price (150 pounds a tonne). So a road order is expressed as tonnes, labour days and lead days in `data/roadlay.json`. Recommended placeholder, to match the gravel class (800 pounds): 5 tonnes (750 pounds), 5 labour days, 5 lead days. BALANCE PLACEHOLDER for Aaron's row-by-row pass. The F1 hint row shows the price read from that file (never a literal).

### 2.6 One cell per key press; a line tool later (RECOMMENDED)

Lane 1: `r` orders one road cell at the cursor. No drag, no mouse. Lane 3 adds `R`: press, move the cursor along one row or column, press Enter; the screen computes the cells and sends one Build "road" per cell. Each command is journaled, accepted and charged on its own, so replay is trivial and a refusal part-way stops the line and says how many were laid. Rejected: one command carrying a path (needs a protocol payload, atomic cost, a rule for partial insolvency; the browser item chose it, and it is large).

After the fly-out menu lands: Build > Transport > Road sends the same command as `r` (lane 4).

### 2.7 What the map shows

- A completed road shows `=` on F1. A queued road shows nothing yet (a home also shows only when complete, `build.go:1100`); the F3 queue lists it.
- `=` differs from the existing base-road glyph `+` (`render.go:67`). No base-map road exists in this game today, so `+` stays for the future import and `=` is the player's. The map decides by a short label on the wire cell: `road: "laid"` draws `=`; any other non-empty road label draws `+`.
- No colour is needed to tell a road from nothing: the rune differs. Terrain background colour still shows under it.
- The notice line shows the one result that is not on the map: "Home queued. A road is queued beside it." or "Home queued. No room for a road beside it." This needs a one-line note on the accepted result (`CommandResult.Result`, which is documented as empty for every other kind and omitted when empty, `envelope.go:229-234`). Rejected: a new message type; rejected: no feedback (the player cannot tell why his money moved).

```
 F1 MAP at 120x30 (excerpt, columns 38..55, rows 10..13)
 y10  . . . . . . . . . . . . . . . . . .
 y11  . . . = = = = = = . . . . . . . . .      '=' road laid by you
 y12  . . . # . # . # . . . . . . . . . .      '#' homes (complete)
 y13  . . . = . . . . . . . . [.] . . . .      '=' road laid beside a home; [.] cursor
 ...
 Notice: Home queued. A road is queued beside it.
 Hint:   F1 MAP  arrows move | y buy cell | b build home | r lay road (750) | c clear notice
```

### 2.8 Demolition

Out of scope for lane 1. `Demolish` clears a home and leaves any road on its cell. Removing a player's own road is lane 3 (Demolish on a cell that holds a road and no home removes the road; refund 0 as a placeholder, question 5). It is a separate rule entry because it newly accepts a command that was refused. Removing base-map roads is out of scope: there are none.

### 2.9 Rule-version entries (what this feature needs under FEAT-9995385612)

Numbers are "the next free one when the lane lands"; today that would be 2, and a later number if density stacking lands first.

| Entry | Lane | Old behaviour | New behaviour | Call site | What an old journal does |
|---|---|---|---|---|---|
| `LayRoad` | 1 | A Build whose type is "road" is refused as an unknown zone (MET-G501). | It is accepted, charged as materials, and on completion the cell holds a road. | `SubmitRoadCommand` entry, branch on `rules.Has(active, rules.LayRoad)`; below it the old branch returns the unknown-zone refusal | Frames stamped below this number cannot hold a road command (refused commands are never journaled), so they replay exactly as before. A hand-built old frame with "road" in it replays as refused and charges nothing (criterion 13). A save made after this lands is stamped with the new number and an older build refuses it instead of replaying road frames as refusals. |
| `AutoRoadBesideDwelling` | 2 | A dwelling order costs its own materials and lays nothing else. | A dwelling order with no road on or beside its cell also queues one road cell and pays for it. | In `SubmitBuildCommand`, after the BUG-1224 check, branch on `rules.Has(active, rules.AutoRoadBesideDwelling)` | Frames stamped below the number replay the dwelling alone: same cost, same treasury, same state digest as recorded. Frames at or above it lay the road. Restoring an old city from genesis or from a snapshot, then building a new home, lays a road for the new home only (criterion 25). |
| `DemolishRoad` | 3 | Demolish on a cell with no structure is refused. | Demolish on a cell with a road and no structure removes the road. | In `SubmitDemolishCommand` | Old frames never hold it (refused). Nothing replays differently. Entry exists so an older build refuses a save that holds it. |

If Aaron answers "beside only" to question 1, a fourth entry `RefuseHomeOnRoad` is added in lane 1 and its old branch accepts (a pre-entry journal may hold a home on a road cell only after lane 1 itself, so in practice the entry lands in the same version as `LayRoad`; the lane would fold it in).

None of the road work changes a daily formula. Migration weights, tax and the transport lever are not versioned (`rules/doc.go`). Nothing here touches them.

## 3. Acceptance criteria

Proof kinds: E engine test, U terminal test through `core.RenderLoop` at 120x30 (the minimum size, flyout ruling), S save and replay, R registry, D data. "Mutation" means: break the production code in the named way and show the test fails. Every criterion needs one.

### Lane 1: the road key

1. D. `data/roadlay.json` carries `materialsTonnes`, `labourDays`, `leadDays` and `class`. The loader rejects a missing field, a negative number, zero tonnes, and a class id that is not in `data/roads.json`. No Go literal holds a road number. Expected: four bad files each fail with a registry code; editing the file changes the cost. Mutation: hard-code 5 tonnes; the edit-the-file test fails.
2. E. A Build with type "road" on an owned cell returns accepted and queues one road order with the file's tonnes, labour and lead time. Committed cost shown at queue time equals tonnes times the construction price (750 pounds at the placeholder). Expected: compose test reads the F3 queue and the ledger. Mutation: zero tonnes; fails.
3. E. A road is complete exactly at the lead time: `HasRoad` is false the day before and true on the day. Expected: tick-by-tick test with a winter and a summer start (the seasonal multiplier applies, `build.go:714-720`). Mutation: off by one; fails.
4. E. A road never holds a cell. Four cases, all accepted: home then road; road then home; road on a cell with a queued home; home on a completed road cell. A queued road order does not make `cellInUse` true. Expected: table test. Mutation: let road orders count in `cellInUse`; case "road then home" fails with MET-G510.
5. E. A second road on a cell with a completed or queued road is refused with a new registry code, before an order number is taken, charged nothing, journaled nothing. Ten presses give one order and one charge (the BUG-1224 measure). Expected: the order counter and treasury are unchanged after the refusal. Mutation: drop the check; ten charges.
6. E. A road on an unowned cell or a cell out of bounds is refused with the existing codes (`ErrCellNotOwned`, `ErrCellOutOfBounds`) and queues nothing. Mutation: skip `requireOwned`; fails.
7. E. Roads change nothing about housing. `StandingCapacities` has no road rows; housing vacancy, migration and the state digest of everything but build are identical for two cities that differ only by 50 completed roads, over 12 months. Expected: two-city test. Mutation: give a road resident capacity 8; fails. This is the "nothing reads a road" statement made executable. Note for the transport lane: this test is the one it will change on purpose.
8. E. `Demolish` on a cell with a home and a road removes the home and leaves the road. Expected: table test. Mutation: demolish clears roads; fails.
9. E. Read API: `HasRoad(tile, cell)` and `RoadCells(tile)` exist, return cells in a fixed order (ascending row then column), take the lock once, and have no consumer yet. Expected: ordered output after inserting in shuffled order; a lock-count test for `RoadCells`. Mutation: return map order; the shuffle test fails sometimes, so run it 200 times with different insert orders.
10. S. Save and restore. A city with 3 completed and 2 queued roads, saved and restored from a snapshot and from a genesis journal replay, has the same roads, the same queue and the same state digest. Expected: round-trip test through `save` and through replay. Mutation: leave the road record out of the participant; fails.
11. S. A snapshot written before this feature (no road record) loads with zero roads and no error. Expected: the committed old snapshot fixture. Mutation: make the loader require the record; fails.
12. R. Registry entry `LayRoad` exists with number, constant, date, item FEAT-9995385608, the old and new text from section 2.9, a fixture path, status wired, `Current` raised, in the same commit as the call site. Expected: `TestRegistry_LiveTree` passes; each of the five parts removed in turn makes it fail.
13. R/S. Old journals. (a) The committed rule fixture replays to the digest recorded under the previous version. (b) A hand-built journal frame stamped below the new number holding Build "road" replays as refused (MET-G501), charges nothing and builds nothing. (c) A save stamped with the new number, opened by a build whose `Current` is one lower, is refused (`rules.Classify`). Expected: three tests. Mutation: make the new branch unconditional; (b) fails.
14. U. `r` on F1 sends exactly one Build with type "road" at the cursor cell. Three presses send three. `r` does nothing on F2 to F5, F10 and F12. Expected: counting transport. Mutation: send twice; fails. The hint row for F1 names `r` and its price, the price read from the data file (no literal), the line fits in 120 columns with no truncation, and the existing rule holds that every advertised key reaches the live grammar (`screenhints.go:15-20`). Mutation: change the price in the data file; the hint text changes; a literal fails.
15. U. A completed road draws `=` on F1 at 120x30 through the real draw loop; a home on the same cell draws `#`; the base-road label still draws `+`. Expected: three cells in one frame, runes compared. Mutation: use `+` for both; fails. The proof does not depend on colour.
16. U. The viewport publisher reads road cells in one locked call per publish, not one call per cell, and a 200x200 publish with 2,000 road cells is within the existing publish budget. Expected: a counting wrapper (one call) and the existing viewport benchmark within 10 percent. Mutation: per-cell call; the count test fails.
17. U. A refusal (road on road, unowned cell) shows its registry text on the notice line, and the next cursor key clears it (existing behaviour, `boot.go:1131-1135`). Expected: draw test. Mutation: skip the display text; fails.
18. E. Determinism. Two runs with the same commands give the same bytes; the road set is flattened sorted on save; no `for range` over the map feeds a result (GR#21). Expected: the repo's determinism gate plus a test that inserts the same roads in two orders and compares saved bytes. Mutation: map-order save; fails.

### Lane 2: the automatic road

19. E. A home with no road on or beside its cell queues one road on the first free neighbour in the order south, east, north, west. Expected: table over the 16 patterns of blocked neighbours (a neighbour is blocked by a road, a standing structure or a queued non-road order) plus the four edge cells of the 200x200 grid and the four corners. Mutation: change the order to north first; the table fails.
20. E. Cost. With a road already on or beside the cell the home costs exactly what it costs today (15,000 pounds, same order id sequence, same treasury). Without one, the cost is the home plus one road. Expected: treasury and committed cost compared across both cases. Mutation: charge a road always; the first case fails.
21. E. No free neighbour: the home is accepted, no road is queued, nothing extra is charged. Expected: surround a cell with standing structures; corner case with the two in-grid neighbours blocked. Mutation: refuse the home; fails.
22. E. Only a dwelling lays a road. Build "shop" and "entertainment" lay none. Expected: table. Mutation: lay for all zones; fails. (Question 3.)
23. E. Order and atomicity. The home takes its order number first and the road the next. Both are queued under one lock or neither is. A home refused by any existing check (not owned, unknown zone, `ErrCellInUse`) queues no road. Expected: id sequence test; refused-home test checks the road count is unchanged. Mutation: queue the road before the home checks; the refused-home test fails.
24. E. Density stacking (only once FEAT-9995385606 has landed). A further order that adds a level to a home lays no second road, and lays one only if the cell and its neighbours have none. Expected: three-level stack test. Mutation: lay on each level; fails. Until it lands this criterion is written as a skipped test that names the item, and the lead may not mark lane 2 done with it skipped after stacking has landed.
25. R/S. Rule entry `AutoRoadBesideDwelling` per section 2.9, wired with fixture. A journal of three homes recorded under the previous version replays from genesis and from a snapshot to the recorded digest and treasury, with no roads; a fourth home ordered after the restore lays one road. Expected: fixture test. Mutation: make the branch unconditional; the old replay digest differs and the test fails. Also: the same tests as criterion 13(c) for this number.
26. U. After an accepted home the notice line says "Home queued. A road is queued beside it." or "Home queued. No room for a road beside it." The text comes from the registry or data, not a literal in the screen, fits in 120 columns, and the next cursor key clears it. Expected: draw test of both messages. Mutation: drop the note; fails. The note rides on the accepted result and an old result without a note still decodes (test with a pre-change result).
27. U. The F3 queue lists the road order as its own row, with its cost, and the F3 layout still holds at 120x30 with 20 queued orders (no overlap, no truncation of the cost column). Expected: draw test. Mutation: omit road rows; fails.

### Lane 3: line tool and remove a road

28. U. `R` starts a line at the cursor; moving the cursor and pressing Enter orders every cell on the straight run (same row or same column) as separate Build "road" commands, at most the data-set cap (placeholder 40) per line; Esc cancels and sends nothing; a diagonal target is refused on screen with no command sent. Expected: counting transport for four paths: run, cancel, diagonal, over the cap. Mutation: send a path as one command; fails.
29. U. If a command in the line is refused (road already there, not enough room) the line stops at the first refusal and the notice says how many cells were laid. Expected: transport that refuses the third. Mutation: continue after refusal; fails.
30. E/R/S. Demolish on a cell with a road and no home removes the road, returns the placeholder refund (0), frees the cell for a new road, and survives save and load. Rule entry `DemolishRoad` wired with fixture; an old journal replays unchanged. Expected: four tests. Mutation: remove the road when a home is also there; criterion 8 fails.

### Lane 4: the menu entry

31. U. Build > Transport > Road is listed (not greyed) and sends the same command bytes as `r` (apart from the correlation id) once per Enter, closes the menu, and is remembered as the last choice. Greyed neighbours (bus station and the rest) send nothing. Expected: counting transport. Mutation: send a different type; fails. The menu table's validator must accept a road item that names no zone (see fallout 4).

### Cross-cutting

32. E. Every new error code is registry-sourced, claimed with `tools/plan/add-error.js` in the lead's brief before any lane starts (parallel lanes mint the same code otherwise, see the collision lesson in memory), and no road message is a string literal. Expected: the existing registry gate. Mutation: a literal error; the gate fails.
33. U. At 120x30 no F1 frame shows a stale road or home glyph after a demolish or a restore (the screen is not cleared between frames: BUG-1234). Expected: demolish a home, restore a save, redraw; compare to a clean-draw frame. Mutation: skip the cell write; fails.

## 4. Lanes

No two lanes share a file except where marked. Sizes: S about half a day, M about a day to two, for one build agent plus review. Build is Sonnet for all lanes. Review is Opus for any lane that changes what a command accepts or costs (lanes 1, 2, 3), Sonnet for lane 4.

| Lane | What Aaron can do the day it lands | Size | Build / review | Files it owns |
|---|---|---|---|---|
| 1. Road key | Press `r` on a bought tile, see `=` appear after 5 days, see the cost on F3, save, load, still there | M | Sonnet / Opus | new `internal/engine/build/road.go` and `road_test.go`; `build/participant.go` (new road record); `data/roadlay.json`; new `internal/foundation/data/roadlay.go`; `internal/engine/rules/registry.go` and `rules.go` (SHARED with density stacking and any rule lane); new `compose/compose_roads.go`; `compose/viewport_publish.go`; `compose/compose.go` KindBuild case (SHARED, 3 lines, with FEAT-9995385615); `compose/testdata/rules/<new>` fixture; `internal/ui/screens/map/render.go` and `patch.go`; `cmd/metropolis/boot.go` (key registration, SHARED with FEAT-9995385621 and the menu wiring lane) and `screenhints.go`; `data/errors.json` through `add-error.js` |
| 2. Automatic road | Build a home, see a road appear beside it, pay for both | M | Sonnet / Opus | `internal/engine/build/build.go` (SHARED, `SubmitBuildCommand`, with density stacking: must land after it or be rebased onto it); new `build/autoroad.go`; `rules` entry; fixture; `compose.go` accepted-result note; `map/screen.go` notice; `build_publish.go` and the F3 draw for road rows |
| 3. Line tool and remove | Draw a street in one go; take back a mistake | M | Sonnet / Opus | `cmd/metropolis` (new `roadline.go`, key bindings, SHARED with the menu lane); `build/build.go` `SubmitDemolishCommand` (SHARED); `rules` entry; fixture |
| 4. Menu entry | F10, B, Transport, Road | S | Sonnet / Sonnet | the menu table (lane L2 of the fly-out menu owns it); `placement.json` loader test change (SHARED with FEAT-9995385615) |

Order: 1, then 2, then 3 and 4 in either order. Lane 1 can start now: BUG-1224 has landed (`785bc70`). Lane 2 waits for density stacking if that is next in `build.go`, otherwise it goes straight after lane 1. Lane 4 waits for the menu wiring lane.

Shared files in flight tonight and what to do: `internal/engine/build` (BUG-1224 just landed; density stacking next): cut the lane from current main and keep road code in new files, touching `build.go` for one branch only; `cmd/metropolis` (FEAT-9995385621 help and save; menu wiring): the key registration is one `RegisterGlobal` block beside `y`/`b` (`boot.go:1157-1179`), keep it a separate function; `compose.go` KindBuild case (FEAT-9995385615): the road branch goes first and calls a new file.

## 5. Fallout

1. Tests that fix the F1 hint text and the advertised-key test (`screenhints.go`): the F1 line gains `r`. Every size-dependent hint test runs at 120x30.
2. `viewport_publish.go:137-143` carries a comment saying road is always omitted. It becomes false and must be rewritten with the change. A test pinning "no road published" (search `viewport` tests) must be turned into the new behaviour; not searched yet.
3. FEAT-9995385607 (start empty) promises a start hint that names laying roads and housing. The hint text must name the real key `r` and must not ship before lane 1. Raise with that lane's owner.
4. FEAT-9995385615 (placement catalogue): its rule 2 says every item names one of the eight zone ids, and its curated list leaves roads out on purpose. A menu entry for roads is not a catalogue item; lane 4 either adds an item kind `road` to that file's loader or keeps the road entry in the menu table by hand. Decide in lane 4's brief, with the lead.
5. FEAT-9995385612 rule doc and the rules README: the three new entries are the proof the registry scales. The doc's section 5 lists stacking as version 2; whichever lands first takes the next number and the other renumbers its fixture name. The lead renumbers at port.
6. The reason-panel (FEAT-9995385609): the transport lever row says "not built" and stays so. Criterion 7 is the sentinel that makes the lever author change it knowingly.
7. `docs/planning/delivery-path-2026-11-18.md` Stage 5 lists roads first; this item is pulled forward because Aaron asked for it by hand, and the transport lever still follows.
8. `code.json`/plan: a feature key for roads (for the `[mkey]` commit tag) is not found. The lead adds it through the master plan, not by hand. Until then commits are tagged `[FEAT-9995385608]`.
9. Cost to the player: every home now costs more (a road) unless it sits next to one. The 3 million start pot is large relative to 750 pounds, so this will not be felt; the balance pass should see it.
10. A bigger run of road cells in saves: 2,000 cells is a few kilobytes of JSON. Measure at 20,000 in lane 1 and record it; not a criterion.

## 6. Out of scope

Traffic, congestion, routing, journey times, junction types, roundabouts, bridges, road tiers and class upgrades (the `engine.roads` module's work), road names, speed limits, maintenance and potholes, one-way streets, parking, base-map roads and their demolition (none exist), mouse or drag laying, a single command carrying a path, roads across tile borders (the game addresses one tile, `compose.go:4463-4470`), connectivity checks, "not on a road network" warnings (the browser had these; here the transport lever owns them), wiring `engine.roads` into the composition, and any browser (TypeScript) change.

## 7. Assumptions

Logged in the Book of Work as they were found (codes in the report). In short:
- A. Nothing in the engine needs a road for a home to count. If a hidden check exists, "lays a road automatically" would also unlock homes. Searched: `compose_vacancy.go`, `compose_housing.go`, the build module; nothing found.
- B. A road may be placed on any cell of an owned tile, including water, because build does not check the surface for any building (`build.go:896-912` uses surface only for land price). If Aaron wants roads off water, that is a new check in lane 1.
- C. Roads draw `=` and `+` stays free. `=` is not used by another overlay glyph in `internal/ui/screens/map` (searched `render.go` constants only; the overlay paint is background only, per `map/doc.go:57-61`).
- D. `data/roads.json`'s loader may reject unknown keys, so the new numbers go in their own file. Not verified.
- E. A save record kind unknown to an older build is not silently dropped. Not verified. The rule version stamp is what protects an older build (criterion 13c), so this does not block.
- F. The F3 queue and its publisher handle an order whose zone string is `road` without a catalogue entry. Not verified (the F3 catalogue comes from the zone list, `build_publish.go:86-123`, but the queue rows are read from orders). Lane 1 must prove it (criterion 27 is lane 2; lane 1 adds a smoke test).

## 8. Questions only Aaron can answer

Each has a recommended answer. Ask in this order; only question 1 shapes lane 1.

1. "Place a house on it": may a home stand on the same cell as a road (Reading 1), or must it stand beside (Reading 2)? Recommended: allow both. The road never blocks a home, and the automatic road lands beside. Cost of "beside only": one extra check and one extra rule entry in lane 1.
2. What does a player-laid road cost? Recommended: 750 pounds a cell (5 tonnes at 150 a tonne), 5 days to build, as a balance placeholder in `data/roadlay.json`. It matches gravel in `data/roads.json` (800 pounds). Say if you want it dearer, so that roads feel like a choice, or free during the empty start.
3. Which buildings lay an automatic road, and which side first? Recommended: homes only (your words), south then east then north then west.
4. When a home has no free neighbour, build it without a road and say so (recommended), or refuse the home?
5. Taking a road back: allowed in lane 3 with no refund (recommended), a refund, or not at all?
6. Do you want the one-key line tool (`R`, move, Enter, at most 40 cells, recommended) as lane 3, or a different feel (a held key that lays as the cursor moves)? The held-key version is simpler to play and harder to take back; it would be one command per cell as well.
