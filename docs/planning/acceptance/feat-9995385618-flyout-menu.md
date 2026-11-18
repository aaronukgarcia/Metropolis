# FEAT-9995385618: The fly-out menu (Baseline One, terminal game)

Written by the business analyst, 2026-11-18. Documents only. Facts come from the code at HEAD 762c857 (file:line given). Items marked UNVERIFIED were not confirmed in the code. Where the code does not say, the text says "not found" and it is a question.

## Rulings (Aaron, 2026-11-18)

**Size.** Minimum terminal size is 120 columns by 30 rows. Aaron's words: "120x30 is the minimum (Recommended)". The 80x24 criteria are retired. Every size-dependent criterion is proven at 120x30 through the real draw loop (`core.RenderLoop`), not the draw functions alone.

**Missing actions.** Aaron's words: "Menu now, wire them straight after (Recommended)". The menu ships with save, load, new city, tax and borrow greyed as "not available yet". Then save and load are wired first, then tax, borrow and new city, each lighting its entry as it lands (BUG-1237).

**Defaults.** Aaron's words: "Accept all (Recommended)". F10 opens the menu and the saves list moves under Screens. The game clock keeps running while the menu is open. Placing a building closes the menu, and the menu remembers the last choice, so repeating is F10, B, Enter, Enter. There is a confirm step on demolish, on enacting a policy and on grant bids. Build is grouped as Housing, Jobs, Services, Health care, Shops, Parks, Transport and Leisure.

## 0. What this item is, in six lines

1. A menu bar sits on the top row of the screen. A key or a click drops a list down. A list can hold a sub-list, which opens beside it.
2. Keys and mouse both work. With no mouse the whole game and the whole menu still work.
3. The menu replaces F3 for building. The player stays on the map, picks Build > group > building (cost and capacity beside each entry), and Enter places it at the map cursor.
4. F3 stays only as a read-only page: the build queue and the money status.
5. The menu holds, on day one: everything you can build (nested by lever, groups not wired yet shown greyed as "not active yet"); every screen; game controls with their keys shown; policy and money actions.
6. Every drop-down blanks its own box before it draws (the BUG-1234 class). "Simple", "fast", "nested so comprehensive" are Aaron's words and are the test of every choice below.

Aaron's three rulings on the item are the contract and are not reinterpreted here. Two things in them cost something and he should hear them plainly (section 2.4): the bar takes one row (19 content rows left at 80x24), and most of the "policy and money actions" have no engine command behind them yet, so they appear greyed.

## 1. Today, from the code

### 1.1 How a frame is drawn at 80x24 (rows from the top)

| Row | What | Source |
|---|---|---|
| 0 | chrome top bar (date, money, population, rating) | `cmd/metropolis/chromebar.go:31` (`chromeTopBarRows = 1`), draw `:78-103` |
| 1 | homes line (places, lived in, free, moved in, left) | `chromebar.go:36` (`chromeHousingRows = 1`) |
| 2-21 | the active screen (20 rows) | `screenContentRect`, `chromebar.go:135-152` |
| 22 | hint row for the active screen | `screenhints.go:51-70`, `chromeHintRows = 1` at `:15` |
| 23 | status row (screen list, tick, month, speed, clock keys) | `statusbar.go:127-163` |

`composeDraw` (`run.go:176-186`) draws the active screen, then chrome, then the hint row, then the status row. The back buffer is never cleared between frames: `renderOnce` (`internal/ui/core/render.go:220-246`) runs the draw funcs and flushes the diff, and `Buffer.Fill` (`internal/ui/core/buffer.go:118`) is not called by the loop. So any cell a draw func does not write keeps last frame's content. That is the root of BUG-1234 ("the pop outs dont clear the text"). There is no "fill a rectangle" helper in `core`; only `Set` (one cell, `buffer.go:98`) and `Fill` (the whole buffer).

### 1.2 Screens that exist and their keys

Registered in the terminal (`boot.go:900-930`, `boot_screens.go:19-160`):

| Key | Screen id | Draw | Keys on it today |
|---|---|---|---|
| F1 | map | `boot.go:1537` | arrows move cursor, `y` buy cell, `b` build dwelling, `c` clear notice (`boot.go:1100-1170`) |
| F2 | finance | `boot.go:1562` | none ("view only", `screenhints.go:13`) |
| F3 | build | `boot_screens.go:164-203` | `n` `p` `b` (`boot_screens.go:81-117`) |
| F4 | services | `boot.go:1618` | leader path `s f +` / `s f -` for ONE placeholder service "clinic-1" (`boot.go:80-103`); hint says "view only" (`screenhints.go:15`) |
| F10 | menu (saves list, read only) | `boot_screens.go:205-225` | none |
| F12 | debug | `boot_screens.go:227-245` | none |

Global keys on `chromeGrammar` (`boot.go:942-960`, `boot.go:1040-1070`, `boot_screens.go:153-158`): F1, F2, F3, F4, F10, F12, Space (pause/resume), `]` faster, `[` slower. `q`, Esc and Ctrl+C quit the process (`run.go:442-447`, `isQuitInput`); Esc aborts a pending key sequence first (`run.go:196-215`).

Screen packages that exist in `internal/ui/screens/` but are NOT registered in the terminal binary (no import under `cmd/`, checked by search): census, districts, proj (projections), ticker, trade, demo. devmode is constructed (`boot.go:634`) but is a console, not a screen.

### 1.3 Input, popups and the mouse today

- Key routing: `routeKeyInput` (`run.go:408-427`) feeds `chromeGrammar` first, then the active screen's grammar. Only key messages are routed; mouse and resize messages are dropped (`run.go:408-412`).
- The input loop already turns tcell mouse events into messages (`internal/ui/core/input.go:34-53`: `MouseX`, `MouseY`, `MouseButtons`). Nothing consumes them.
- Mouse is never switched on. `core.Profile.Mouse` exists (`capability.go:14-37`; Windows Terminal true, conhost false) but a search of `cmd/` finds no call to `EnableMouse` and no use of `Probe`. The screen comes from `core.NewScreen` (`internal/ui/core/screen.go:27-44`, called at `run.go:63`) with no mouse call. So today the terminal receives no mouse events at all.
- Popups and confirm dialogs: none found in `cmd/metropolis` or `internal/ui/widgets` (`widgets/border.go` draws a box frame; `widgets/table.go` a table). No shared "popup" or "dialog" widget exists. The one screen with a notice is F1, which draws the engine's build refusal (BUG-493, `drawBuildNotice`) until `c` or the next cursor key.
- Key grammar: `keys.KeyGrammar.RegisterGlobal` fires even mid-sequence; Esc is reserved and cannot be bound (`internal/ui/keys/grammar.go`, per `run.go:196-206`). Bare digits are claimed by the count prefix (`boot.go:90-96`).

### 1.4 Every command the terminal can send

Protocol kinds (`internal/protocol/commands.go:33-90`) and what the terminal does with each:

| Kind | Payload | Has a key in the terminal today? | Status |
|---|---|---|---|
| AdvanceTicks | n | tick driver only (`tickdriver.go`) | not a player action |
| SetSpeed | speed | `]` `[` | key exists |
| Pause / Resume | none | Space | key exists |
| Subscribe / Unsubscribe | view | boot only | not a player action |
| InspectEntity | id | none (devmode) | out of scope |
| Debug | op, args | none | engine accepts and ignores (`internal/engine/core/commands.go:770-772`, `handleDebug` just accepts) |
| Buy | cell | `y` on F1 | key exists |
| Zone | cell, zoneType | none | command exists (`compose.go:4331`); no key; not needed once Build carries the zone |
| Build | cell, buildingType | `b` on F1 (dwelling), `b` on F3 | key exists; only zones work today, see 1.5 |
| Demolish | cell | none | command exists (`compose.go:4370`); no key |
| SetFunding | serviceId, level | `s f +/-` on F4 for placeholder "clinic-1" only | command exists; no live service to aim at (`boot.go:80-96`) |
| EnactPolicy | policyId, scopeKind, district, road | none | command exists and is handled (`policies_wire.go:262-287`) |
| RepealPolicy | enactmentId | none | command exists; the terminal has no list of enactments (see 1.6) |
| BidForGrant | pot, matchFundingMicropounds | none | command exists (`compose.go:4438`, `defence_wire.go:187`) |
| RespondToMandate | mandateId, refuse, choice, cell | none | command exists; no mandate list in the terminal |
| QueryCitizen / QueryOccupants | | none | read-only queries, out of scope |

Actions the screens' code offers but for which NO engine command exists (they ride as `KindDebug` ops that the engine accepts and ignores):

| Action | Where the terminal code sends it | Engine side |
|---|---|---|
| Set a tax rate | `finance.set-tax-rate`, `internal/ui/screens/finance/screen.go:19, :573-584` | `finance.TaxRates` and `CollectTax` exist (`internal/engine/finance/stages.go:136-163`); no setter reached by a command |
| Borrow a loan | `finance.borrow-loan`, `finance/screen.go:17, :546-560` | `FinanceAPI` holds loans (`finance.go:64`); no command reaches it |
| Repay a loan | `finance.repay-loan`, `finance/screen.go:18, :563-570` | same |
| Save the city | `menu.save-game`, `internal/ui/screens/menu/commands.go:17-20` | `Composition.Save(root)` exists (`compose/save_wire.go:197`); nothing in `cmd/` calls it (search found no call) |
| Load a city | `menu.load-save` | `Composition.Load` exists (`save_wire.go:261`); not called from `cmd/` |
| New city | `menu.new-game` | only the start-up flag `-game-mode` (`run.go:38`) |

These are listed as **gap** in the tree. They are not invented and not wired by this item. A menu item that sent a `Debug` op would look like it worked and do nothing: that is the built-but-not-wired class, so it is banned (criterion 46).

### 1.5 What can be built

- Today the engine reads the build string only as one of eight zone ids: dwelling, shop, office, farming, manufacturing, heavy_industry, mining, entertainment (`docs/planning/acceptance/feat-9995385615-placement-catalogue.md` 1.3). Only dwelling and entertainment move migration.
- The curated placement list (24 items in 8 groups, 2 placeable) is defined in the placement item section 2a. `data/placement.json` does not exist yet (checked: file not found). It is that item's lane C1.
- The ten levers, in Aaron's order (CLAUDE.md "Baseline One bar", `docs/planning/delivery-path-2026-11-18.md`): housing (done), jobs, services, health care, policy, shops, parks, transport links, congestion, tourism. The placement file has groups homes, leisure, jobs, shops, parks, health, services, transport. Policy, congestion and tourism have no build group (policy is the Policy menu; hotels are built automatically, roads belong to FEAT-9995385608). See question 2.

### 1.6 Views the terminal can read

The engine registers views for policies, defence, coastal, social and f3.build (`compose.go:719-745`). The terminal subscribes to f1.viewport, f2.finance, f4.services, chrome.topbar, f3.build and engine.status only. So it has no list of active policy enactments, districts or defence mandates today (not found).

### 1.7 Fallout (the 80x24 and the other BUGs)

- BUG-1220 (done): hint row, screen list on the status row, homes line. BUG-1222 (done): the F2 P&L was made to fit in 20 content rows. BUG-1223 (open): the status row is cut before `[Space] pause`, so pause, speed and quit keys are not visible at 80 columns. BUG-1234 (open): screens do not blank their rect; F3 queue lists every completed order; rows run off the column edge.
- A bar on row 0 pushes the chrome rows down. Content goes from 20 rows to 19 at 80x24 (see 2.4). The F2 fit of BUG-1222 was tuned at 20 rows and must be re-proved at 19.

## 2. Design

### 2.1 The proposed menu tree

Notation: `[x]` is the letter key. Leaf kinds: **screen** (activates a screen), **command** (sends a protocol command; name given), **build** (a building or group id from the placement file), **local** (acts inside the terminal, no protocol command), **not active yet** (shown greyed, cannot be picked), **gap** (the action has no engine command; shown greyed and says so). Letters are unique inside each list. Keys shown beside an entry are the real key bindings.

```
Menu bar (row 0):   [B]uild  [S]creens  [G]ame  [P]olicy  [M]oney          opens with F10

[B]uild
  [H]ousing
     [D]welling                £15,000   8 homes        build: dwelling
  [J]obs                       not active yet
     [O]ffice, [F]arming, [M]anufacturing, [H]eavy industry, m[I]ning      not active yet (zones)
  [S]ervices                   not active yet
     [O]ne-room school, [P]rimary school, [S]econdary school, [F]ire station    not active yet
  Health [C]are                not active yet
     [C]linic, [S]mall hospital, [G]eneral hospital                        not active yet
  Sh[O]ps                      not active yet
     [S]hop, [C]orner shop, s[U]permarket                                  not active yet
  [P]arks                      not active yet
     [P]ocket park, [V]illage green, [T]own park                           not active yet
  [T]ransport                  not active yet
     [B]us station, [R]ail station, regional [A]irport                     not active yet
  [L]eisure
     [E]ntertainment           £30,000   15 jobs        build: entertainment
  ---
  Bu[Y] this cell              y                         command: Buy (cursor cell)
  Demolish he[R]e                                        command: Demolish (cursor cell), asks "Yes, demolish" / "No" first

[S]creens
  [M]ap                        F1                        screen: map
  [F]inance                    F2                        screen: finance
  Build [Q]ueue (read only)    F3                        screen: build
  [S]ervices                   F4                        screen: services
  Sa[V]es list                 F10 (menu key moves; see 2.5)   screen: menu
  [D]ebug                      F12                       screen: debug
  ---
  [C]ensus                     not active yet (screen code exists, not registered in the terminal)
  D[I]stricts                  not active yet (same)
  [J] Projections              not active yet (same)
  [K] Ticker                   not active yet (same)
  [T]rade                      not active yet (same)

[G]ame
  [P]ause / Resume             Space                     command: Pause / Resume (label follows the clock)
  [F]aster                     ]                         command: SetSpeed
  [S]lower                     [                         command: SetSpeed
  ---
  Sa[V]e city                  gap (no engine command; Composition.Save exists but nothing in the terminal calls it)
  [L]oad city                  gap (same, Composition.Load)
  [N]ew city                   gap (only the -game-mode start-up flag today)
  ---
  [Q]uit                       q                         local: stop the process

[P]olicy
  [E]nact policy               (lists the policies in data/policies.json, citywide ones first)
     (nine policies, letters in the table below: six citywide = command EnactPolicy,
      three district-scoped = gap, they need a district and the terminal has no district list)
  [R]epeal policy              gap (RepealPolicy exists; the terminal has no list of enactments)

[M]oney
  [T]ax rates                  gap (no engine command)
  [B]orrow                     gap (no engine command)
  [R]epay a loan               gap (no engine command)
  [F]unding for a service      not active yet (no live service to fund; F4 is view only)
  [G]rants
     [T]ransport pot           command: BidForGrant (amount picked in a third list, see criterion 51)
     [R]egeneration pot        command: BidForGrant
     [C]ulture pot             command: BidForGrant
  [D]efence mandate            not active yet (no mandate list in the terminal)
```

The Policy > Enact list as a table, because the one-letter keys must be unique inside it (the sketch above is not final for that list):

| Letter | Policy (name from data/policies.json:21-159) | Scope | Kind |
|---|---|---|---|
| C | Cycle Priority Network | citywide | command EnactPolicy |
| O | Congestion Charge | citywide | command EnactPolicy |
| F | 15-Minute Neighbourhood | citywide | command EnactPolicy |
| N | National Minimum Wage | citywide | command EnactPolicy |
| H | 40-Hour Working Week | citywide | command EnactPolicy |
| S | Social Housing Grant | citywide | command EnactPolicy |
| L | Low Emission Zone | district | gap (district choice) |
| P | Park Access Programme | district | gap (district choice) |
| T | Tax-Free Harbour freeport | district | gap (district choice) |

Letters for the sub-lists under Build (Jobs, Services, Health care, Shops, Parks, Transport): the first letter of each item where free, else the highlighted one in the tree; the validator (criterion 2) rejects any clash, so the final letters are the table's, not this sketch's.

**Three-key path for a dwelling:** open the menu with F10, then `B` (Build), `H` (Housing), `D` (Dwelling). `D` is a leaf, so the letter runs it: one Build command for the dwelling at the F1 cursor cell. With Enter instead: F10, `B`, `H`, then Enter on Dwelling (it is the only entry, so it is already highlighted). The F1 key `b` still builds a dwelling in one press.

Top row of the tree has five entries; the second level has 10, 11 (6 + 5 greyed), 9, 5 respectively; the deepest list is two drop-downs down (Build > group > building; Money > Grants > pot, then a small amount list, which is a third drop-down for grants only).

### 2.2 Where the data comes from (one table, no retyping)

- The tree is one Go table in `cmd/metropolis` (new file): label, letter, kind, target. The menu package reads the table and knows nothing else.
- The Build branch is NOT typed in that table. It is generated from the published `f3.build` catalogue (groups, items, wired flag, cost, capacity) that the placement item (its criterion 11) puts in the view. So when a lever lane flips a group to wired in `data/placement.json`, the menu changes with no menu edit.
- The Policy > Enact list is generated from the policy library, not retyped. Which seam (subscribe to the existing policies view, or read the data file through `internal/foundation/data`) is not decided in the code: see question 6.
- Key labels beside Game entries come from the same constants that register the keys (as `statusBarKeyHelp` does, `statusbar.go:127`).

### 2.3 What happens to the placement item (FEAT-9995385615)

That item's lane C3 is the F3 catalogue screen. This item replaces it. Exactly:

| Placement criterion | Fate |
|---|---|
| 11 (compose publishes the curated list by group in `f3.build`) | STAYS. It feeds the Build branch. Lane C3's compose part (`compose/build_publish.go`) is kept and passed to this item's lane L3. |
| 17 (F3 frame with catalogue, character exact) | DIES. Replaced by criterion 54 (the F3 page is a queue and status page). |
| 18 (`n`/`p` selection skips unwired) | DIES with the `n`/`p` keys. Its rule (an unwired item is never picked) moves to criterion 35. |
| 19 (`b` queues the selected item at the F1 cursor) | MOVES to criterion 32 (Enter or letter in the menu). |
| 20 (hint-truth for `n` `p` `b`) | MOVES to criteria 17 and 54 (hint rows stay true). |
| 21 (WARNING when committed exceeds treasury; build still queues) | MOVES: the warning text is shown on the F3 page (criterion 57) and in the Build box footer (criterion 36); placing still works. |
| 22 (60-column rules) | MOVES to criterion 14 (drop capacity, then clip). |
| 23 (31-row list scrolls) | MOVES to criterion 13. |
| 24 (`REJECTED: <reason>` on a refused build) | MOVES to criterion 34 (the reason shows on the F1 notice). F3 no longer sends commands, so its own REJECTED row dies. |

Placement C1 (data, resolver, error codes) and C2 (committed cost) are unchanged and are prerequisites (section 6).

### 2.4 The row cost, stated plainly

Aaron's ruling says the bar "costs one row at 80x24". The code says what that means: row 0 is the chrome top bar, row 1 the homes line (`chromebar.go:31-37`). The bar needs its own row, so chrome moves to rows 1-2, `chromeReservedRows` goes from 2 to 3 (`chromebar.go:37`), and `screenContentRect` returns 19 rows at 80x24. The hint and status rows stay at 22 and 23. The `hintRoom` threshold (`screenhints.go:77`) must count the new row. I found no conflict between the rulings; the cost is real and is the 19-row figure. The alternative (put the bar and the figures on the same row) was not measured and is not what he said. See question 1.

### 2.5 Keys and conflicts found in the code

- The opener. F10 is the saves-list screen today (`boot_screens.go:150`). F10 is the usual menu key. Recommended: F10 opens the menu; the saves list moves to Screens > Saves list (letter `V`, no key). Alt+letter direct opening is wanted only if tcell delivers Alt (UNVERIFIED). See question 4.
- Plain letters cannot open menus: `b`, `y`, `c`, `q` are game keys.
- Esc quits the process when nothing is pending (`run.go:196-215`, `isQuitInput`). The menu must see Esc first when open (criterion 7), or Esc would close the whole game from inside a menu.
- `q` and Space are global keys. When the menu is open `q` is a letter for the menu and must not quit; Space must not pause. Pause and quit remain items in the Game list.

### 2.6 The game clock while the menu is open

Recommended: the clock keeps running, as it does today whenever the player is on any screen (the tick driver is its own goroutine, `tickdriver.go`, not tied to input). The menu is a view, not a pause. If Aaron wants menu-open to pause, it needs a Pause/Resume pair around the menu and a rule for a game already paused: more state and more bugs. See question 3.

## 3. Acceptance criteria

Tags: E engine, D drawing, M mouse, S speed, K clock, B build, SC screens, G game, P policy and money, F the F3 page, U UI contract. Test names are for the developer to choose; all frame tests run on the real boot path (`bootCore`, `composeDraw`, key input through `routeKeyInput`/`pressKey`, as `bug1222_f2_80x24_test.go` does) at 120x30 unless stated (was 80x24 before the 2026-11-18 ruling).

### Menu engine

1. E. The tree is built from one table (a Go slice of nodes: label, letter, kind, target, children). Opening, drawing, key handling and hit-testing are written once and never per menu. Expected: a test adds a node to a fixture table and the new entry draws, is reachable by letter and Enter, with no other code changed.
2. E. The table is validated when built. It fails with a registered error code (claimed with `tools/plan/add-error.js`) on: two entries in one list with the same letter (case-insensitive); an empty label; a leaf with no kind; a node that is both a leaf and a list; a list with no entries; depth over 4. Expected: one broken fixture per rule, each fails; the shipped table passes.
3. E. Open. The menu key opens the bar with the first entry highlighted and its list dropped. Pressing the menu key again, or Esc at the first level, closes it. Expected: frame before/after.
4. E. Move. Up and Down move the highlight inside a list and wrap. Left and Right at the first level move to the neighbouring bar entry (its list drops). Enter or Right on a list entry opens its sub-list; Enter on a leaf runs it. Left or Esc goes back one level. Expected: a key script over the shipped tree reaches every entry and returns.
5. E. Back and close. Esc closes one level; at the first level it closes the menu. A leaf that runs closes the menu. Closing leaves the active screen exactly as it was. Expected: after any close the frame equals the frame before opening, except for what the command changed.
6. E. Letters. In a list, pressing an entry's letter (either case) moves to it and acts: a list entry opens, a leaf runs, a greyed entry shows its reason (criterion 9). A key that is no entry's letter does nothing and sends nothing. Type-ahead is not required. Expected: every entry of the shipped tree reached by letter only.
7. E. Input capture. While the menu is open every key goes to the menu and to nothing else: `q` does not quit, Space does not pause, arrows do not move the F1 cursor, digits start no count prefix, F1-F4 do not switch screens until the menu closes (F10 or Esc closes it). Esc never quits the game from an open menu. Expected: with the menu open, feed `q`, Space, `]`, an arrow, `1`; the clock, the cursor, the active screen and the process are unchanged.
8. E. When closed, the menu takes only its opener key (and the mouse click on the bar, criterion 20). Every other key behaves exactly as today. Expected: the existing key tests (`bug362_map_keyboard_build_test.go`, `bug322_tickdriver_test.go`, `run_test.go`, the F-key tests) pass unchanged.
9. E. Greyed entries (kinds **not active yet** and **gap**) can be highlighted but not run. Letter, Enter and click show the entry's reason on the box's footer line and send nothing. Expected: a counting transport sees zero sends for every greyed entry of the shipped tree.

### Drawing

10. D. Greyed entries are drawn dim and carry the words not active yet (or the gap reason) in text, so they read with no colour.
11. D. Blank box (BUG-1234 class). Each drop-down fills its whole rectangle, border included, with spaces before it draws; the bar fills its row. Expected: fill the back buffer with `#` in every cell, run `composeDraw` with each list of the shipped tree open at 120x30 (was 80x24 before the 2026-11-18 ruling): no `#` inside any box or on row 0; `#` still present outside the boxes (the fill is not over-wide).
12. D. Never off the screen. Every box rectangle lies inside the buffer and above the hint row (to be measured at 120x30) at 120x30 (was 80x24 before the 2026-11-18 ruling), 60x24 and a small 40x12; every entry is still reachable. Expected: a sweep over every path of the shipped tree at the three sizes asserts the rectangles and reachability.
13. D. A list longer than the space scrolls. The highlight is always on screen, and a `... N more above` / `... N more below` line shows when rows are hidden. Expected: a fixture list of 40 items at 120x30, and the shipped Policy > Enact list at 80x8, with the highlight visible in every frame over a full Down-run and wrap.
14. D. Right edge. A sub-list that would run past the last column opens to the left of its parent; if neither side fits, it is clamped and names are cut with an ellipsis, the capacity column going first, then the cost column last. Expected: Money > Grants at 120x30 (was 80x24 before the 2026-11-18 ruling) and 60x24 and Build > Housing at 60x24 frames; no cell past column W-1.
15. D. The bar. All five entries show at 80 and at 60 columns with their letters marked; the highlighted entry is reversed; the bar's row is blanked before drawing. Expected: exact bar row at both widths, open and closed.
16. D. Layout. The bar is row 0. Chrome moves to rows 1-2. `chromeReservedRows` is 3 and `screenContentRect` gives a content row count to be measured at 120x30; every registered screen draws in it; the hint row and status row positions are to be measured at 120x30. The `hintRoom` threshold counts the new row. Expected: frames of F1, F2, F3, F4, F10, F12 at 120x30 (was 80x24 before the 2026-11-18 ruling) show their headings on row 3 and nothing covered; the F2 P&L still shows its Net and Household consumption lines (the `bug1222_f2_80x24_test.go` assertions) within the content rows measured at 120x30.
17. D. Hint row while the menu is open shows the menu keys (move, open, back, letter, close) and every key named works. Closed, each screen's hint is unchanged except F3's (criterion 54). Expected: a hint-truth test feeds every named key.
18. D. Box widths come from content: a Build item row is name, cost, capacity, with the longest at most 42 cells including border at full width. Expected: the longest shipped row fits 80 columns open at its deepest list.

### Mouse

19. M. Mouse is switched on at start-up when the capability profile says so (`core.Profile.Mouse`) and left off when it does not. Today nothing calls `EnableMouse` (1.3). Expected: a simulation screen records the call; true for the Windows Terminal profile, absent for conhost. Which profile the real terminal gets is UNVERIFIED (nothing calls `Probe` in `cmd/`; the lane must wire it).
20. M. Click a bar entry: its list opens (another entry's list switches). Click an item: a leaf runs, a list opens. Click outside every box: the menu closes and the click is swallowed (it moves no cursor and reaches no screen). Click on a greyed entry: its reason shows. Expected: scripted mouse messages produce the same frames and the same command counts as the matching key scripts.
21. M. Hover. If the terminal reports motion, the entry under the pointer is highlighted. If no motion events arrive, nothing breaks and nothing is needed. Expected: motion messages move the highlight; a run with none passes the whole key suite.
22. M. The game is fully playable with no mouse: every criterion above and below is tested with key input only, and the mouse tests are the only ones that use mouse messages. Expected: the mouse path can be switched off and the suite stays green.
23. M. Mouse messages while the menu is closed are ignored, except a click on the bar's row, which opens the menu. Expected: a click on the map area changes nothing.

### Speed ("fast")

24. S. The menu never waits on the engine. Opening, moving, going back, closing and hovering send no command and no subscribe, and read no engine state under a lock. Expected: a transport that fails the test on any send, plus a view store that is never updated; run the whole key script over the shipped tree; zero sends, every frame drawn.
25. S. One key press shows its result in the next frame. The menu changes state in the input handler before the render is triggered (`run.go:197` triggers a render per delivered input). Expected: feed one key, call `composeDraw` once, the frame already shows the new state; no second tick or frame is needed.
26. S. Draw cost depends on the rows shown, not on the size of the tree. The tree is built once at boot; the Build cost and capacity read the last cached `f3.build` patch. Expected: a fixture tree of 10,000 entries with a counting accessor: one draw reads at most the visible rows plus a fixed constant. This is how "fast" is checked without wall-clock timing (Aaron's rule: never wall-clock bounds in CI). The constant is stated in the test.

### Game clock

27. K. The clock keeps running while the menu is open. Expected: open the menu, advance ticks, `engine.status` tick rises. If Aaron answers question 3 differently, this criterion is replaced by its opposite and the test flips.

### Build branch

28. B. The Build branch is generated from the published catalogue, not typed: group names, order, wired flag, item names, cost and capacity all come from the `f3.build` view. Expected: a fixture catalogue with a new group and item appears in the menu with no menu-code change.
29. B. Row format: name, then cost as `£15,000` (whole pounds, thousands separators), then capacity as `8 homes` or `15 jobs` with a zero side dropped (placement 2d). Expected: exact frame of the Housing and Leisure lists at 120x30 (was 80x24 before the 2026-11-18 ruling).
30. B. A group not wired shows greyed with `not active yet` after its name; opening it lists its items greyed, so the player sees what is coming. Expected: exact frame for Jobs.
31. B. The wired flag is read from the view, never from a literal in the menu. Expected: fixture where `health` is wired makes Clinic pickable; the shipped data does not.
32. B. Placing. With a bought cell under the F1 cursor at (12,7): the key script F10, `B`, `H`, `D` sends exactly one Build command with cell (12,7) and building type `dwelling`; the order shows in the F3 queue within the ticks needed. The same with Enter in place of `D`. Entertainment likewise sends `entertainment`. Expected: the sent command is compared field by field.
33. B. One run, one command. Enter (or the letter) sends once; holding the key sends nothing further after the menu closes. Expected: counting transport shows one send for a script that presses Enter twice quickly.
34. B. The refusal shows. For an unowned cell the engine refuses; the engine's own display text appears on the F1 notice row (the same result path as `sendMapCommand`, `boot.go:1115-1135`). If the menu was opened on another screen, picking a Build item first activates F1 so the result is where the player is looking. Expected: the existing `TestWireTUI_AC_B4_RejectionReasonShown` idea, driven through the menu.
35. B. Greyed buildings are never sent. Enter, the letter and a click on a not-active-yet building send nothing and show `not active yet`. Expected: counting transport; mutation that ignores the flag fails.
36. B. The Build box footer shows `Committed £60,000 of £1,500,000` from the view (placement C2 fields), and `WARNING committed ... is more than the treasury ...` when above; placing still works. Expected: frames for both states. (Stays true the day C2 lands; if C2 is not in yet, this criterion waits.)
37. B. Buy and Demolish. `Buy this cell` sends Buy at the cursor. `Demolish here` opens a two-entry list `Yes, demolish` / `No`; only Yes sends Demolish; Esc or No sends nothing. Expected: counting transport for the three paths.
38. B. Remembering. Reopening Build highlights the last group and last item used. Expected: F10, `B`, `H`, `D`, then F10, `B`, Enter, Enter repeats the same build.

### Screens branch

39. SC. Each registered screen has an entry showing its F-key. Choosing it activates it the same way the F-key does (same registry call; for Saves the same refresh before activation, `boot_screens.go:150`). Expected: after each entry the active screen id is the one named.
40. SC. The unregistered screens (census, districts, projections, ticker, trade) show greyed `not active yet`. A pin test lists the exact set; when a lane registers a screen it flips that entry and the pin changes in the same commit. Expected: pin test fails if an entry is flipped without the registry holding the screen.
41. SC. The F3 entry reads `Build queue (read only)`. Expected: frame.

### Game controls branch

42. G. Pause/Resume, Faster and Slower send the same commands as Space, `]` and `[` (the same functions, `w.togglePause`, `w.stepSpeed`, `boot.go:1053-1058`). The Pause entry shows Pause or Resume to match the clock. Expected: command-for-command equality with the keys.
43. G. The key beside each entry is built from the constant that registers that key, so it cannot drift. Expected: a test binds a different key in a fixture and the label follows; mutation that hard-codes `Space` text fails.
44. G. Quit stops the process through the same path as `q` (`closeStop`, `run.go:188-216`). Expected: test with a fake stop channel.
45. G. BUG-1223 is answered: at 120x30 (was 80x24 before the 2026-11-18 ruling) the Game list frame shows Space, `]`, `[` and `q` beside their entries. BUG-1223 closes on this frame. Expected: exact frame.
46. G. Save, Load and New city are shown as **gap**. None of them sends a `Debug` op (that would be accepted and do nothing: `core/commands.go:770`). Expected: a test walks the shipped tree and fails on any leaf that builds a `KindDebug` command.

### Policy and money branch

47. P. Every entry in 2.1 marked **gap** or **not active yet** is greyed and says why in words a player reads ("no engine command yet", "needs a district list", "no live service"). Expected: the reason strings are in one table with the kinds; a test pins the exact gap set so a lane that wires one must flip it.
48. P. The Enact list is generated from the policy library; names and scopes are not retyped in the menu table. Expected: fixture library with a tenth policy appears.
49. P. Enacting a citywide policy sends `EnactPolicy{policyId, scopeKind:"citywide"}` after a confirm step that shows the policy name; Esc or No sends nothing. Expected: counting transport.
50. P. Every command a menu sends registers its result handler before sending (the `sendMapCommand` order, `boot.go:1115`), and the result (accepted, or refused with the engine's display text) shows on the hint row until the next key. Expected: refusal text appears for a forced refusal; an accepted command shows a one-line confirmation.
51. P. Grants. `BidForGrant{pot, matchFundingMicropounds}` is sent only after a third small list picks the amount and a confirm step shows pot and amount. The amounts come from `data/defence.json` (`maxMatchMicropounds` per pot, `data/defence.json:5-30`), not literals. Expected: three paths (cancel, confirm, refused) with the counting transport.
52. P. Money-moving or state-changing commands (Enact, Bid, Demolish) cannot be sent by one stray key: each needs the confirm step. Expected: no script of fewer than the full path sends the command.
53. P. No menu leaf sends a command kind the engine does not handle. Expected: a table test maps each command leaf to a kind that `compose.handleGameplay` or `engine.core` handles.

### The F3 page

54. F. F3 shows the build queue and the money status only: no catalogue, no zone list, no selection. The keys `n`, `p`, `b` do nothing on F3 (the F1 `b` is unchanged). The hint reads `F3 BUILD QUEUE  read only | build from the menu: F10 > Build`. Expected: frame, hint-truth test, and `n`/`p`/`b` on F3 change nothing and send nothing.
55. F. The F3 page blanks its whole rectangle every frame (BUG-1234 item 1). Expected: marker-fill test as criterion 11 for F3.
56. F. The queue shows active orders first and collapses completed ones into `N completed` (BUG-1234 item 2). Rows are cut at the column edge with an ellipsis (item 3). Expected: a 23-completed fixture shows one count row and the active orders.
57. F. F3 keeps the committed-cost line and the WARNING text (placement 2e, its criteria 12-14 unchanged). Expected: frame with committed above treasury.
58. F. The F3 key and screen id stay. Expected: F3 still activates `build`.

### UI contract and fallout

59. U. The UI stays protocol-only. The menu package imports no `internal/engine/...` package; commands are built from `internal/protocol` types; Build cost and capacity come from the view patch. Expected: an import-scan test (the pattern of `internal/ui/screens/menu/encapsulation_test.go`) fails when an engine import is added.
60. U. The new package follows the screen packages' rules: `copyguard.go`, determinism test (no map ranging in draw, no clock reads, BUG-602), registered error codes for every failure (GR#7), a doc header with the module GUID registered in the master plan (`/register-guid`). Expected: the existing guards pass.
61. U. Fallout: every existing frame test that pins a row number moves with the 19-row content area: the `bug1213`, `bug1220` (3 files), `bug1222`, `bug324_chrome_topbar`, `feat143_financedraw`, `wire_tui_screens*` tests are the likely set (UNVERIFIED which fail; the developer runs the cmd tests and lists them). Expected: no pin is loosened to pass; each is re-derived and the diff is in the report.

Can-fail proof, per criterion (one mutation each; the test must go red):

1 hard-code one list's draw. 2 drop the duplicate-letter check. 3 open on the second entry. 4 stop wrapping. 5 leave the menu open after a leaf runs. 6 ignore upper case. 7 let `q` through to quit. 8 take plain `b` for the menu. 9 let Enter run a greyed leaf. 11 remove the fill. 12 remove the clamp. 13 remove the scroll offset. 14 remove the left-open rule. 15 drop the bar row fill. 16 leave `chromeReservedRows` at 2. 17 add a hint key that does nothing. 18 widen the capacity column. 19 never call EnableMouse. 20 let the outside click fall through. 21 make hover required. 22 route a mouse-only path. 23 handle a map click. 24 query the engine on open. 25 render on the next tick only. 26 loop the whole tree each frame. 27 pause on open. 28 type the groups in the table. 29 print `£15000.00`. 30 hide the items of a greyed group. 31 hard-code `wired`. 32 send the item name instead of the id. 33 send on key repeat. 34 swallow the result. 35 skip the wired check. 36 compute committed in the menu. 37 send Demolish without the confirm. 38 drop the memory. 39 activate without the refresh. 40 flip an entry without the screen. 41 keep the old label. 42 send a different speed step. 43 hard-code `Space`. 44 skip the stop. 45 cut the key text. 46 send a Debug op for Save. 47 enable a gap leaf. 48 retype the policy names. 49 send without confirm. 50 register the handler after the send. 51 hard-code an amount. 52 send on the first key of a path. 53 add a leaf for a kind nothing handles. 54 leave `n` live on F3. 55 remove the F3 blank. 56 list completed orders. 57 remove the committed line. 58 unregister F3. 59 add an engine import. 60 map-range in draw. 61 loosen a pin.

## 4. Fallout and what moves

- Files that change meaning: `cmd/metropolis/chromebar.go` (row count), `screenhints.go` (F3 hint, `hintRoom`), `boot_screens.go` (F3 draw and keys), `run.go` (input priority, mouse), `boot.go` (F10 binding via `boot_screens.go:150`).
- Placement item: see 2.3. Its C3 lane is cancelled; its C1 and C2 proceed; its `build_publish.go` catalogue fields are kept.
- BUG-1223 closes on criterion 45. BUG-1234 items 1 and 2 close on criteria 55-56 for the F3 page; item 3 for the menu is criteria 11 and 14. Other screens' blanking (F2, F4, F10, F12) is NOT in this item: BUG-1234 says "likely shared" and stays open for them.

## 5. Out of scope

- Wiring any gap: tax, borrowing, repay, save, load, new city, repeal, district choice, mandate list, service funding. They appear greyed. Each is its own item once Aaron wants it.
- Registering the census, districts, projections, ticker and trade screens.
- The levers themselves (jobs, services, health, shops, parks, transport, congestion, tourism); flipping a group to wired.
- A road tool (FEAT-9995385608). The Zone command and the Debug and Inspect commands.
- Mouse wheel, drag, right-click, text search, a pop-up help screen.
- Web console menus. Menu look and colour beyond reversed highlight and dim greyed text.
- Blanking the other screens (BUG-1234 for F2, F4, F10, F12).

## 6. Lanes (no shared files between lanes except where stated)

| Lane | Work | Files | Size | Review |
|---|---|---|---|---|
| L1 menu engine | table type, validator, state machine, key and mouse hit logic, draw with box blanking, scroll and flip | new package `internal/ui/flyout/` (all new files) | M, 2-3 days | Sonnet developer; round r1 |
| L2 wiring and layout | bar on row 0, chrome shift to 3 rows, F10 opener, input priority over quit and clock keys, mouse on, hint row while open, frame-test fallout | `cmd/metropolis/chromebar.go`, `run.go`, `screenhints.go`, new `flymenu.go`; fallout in cmd tests | M, 1-2 days | Sonnet; **Opus review recommended** (it changes the quit path and every pinned frame) |
| L3 Build branch | generate the branch from `f3.build`, place at cursor, Buy, Demolish with confirm, committed footer | new `cmd/metropolis/flymenu_build.go`; `compose/build_publish.go` (shared with placement C2: C2 merges first) | S-M | Sonnet; Opus review for Demolish |
| L4 Screens, Game, F3 page | screens list, game controls with key labels, gap items, F3 read-only page | new `flymenu_game.go`; `boot_screens.go` (F3), `screenhints.go` hint text (shared with L2: land L2 first) | S | Sonnet |
| L5 Policy and Money | Enact list, confirm steps, grants with amounts, result line | new `flymenu_policy.go`; data reads only | S-M | Sonnet build; **Opus review required** (anything that sends money or state commands) |

Order: L1 first. L2 and L3 can follow in parallel once L1's table type is in. L4 after L2. L5 after L1 and L2. Prerequisites outside this item: placement C1 (resolver and data) and C2 (committed cost) before L3 criteria 28-36 can be true; Stage 1 lane A (the reason panel) also edits chrome rows: whichever lands second rebases on the other's row count. Per project rule: round-first (GR#23), BUDGET line on every dispatch, agents verify only the files they touch plus one scoped run, the lead runs the full gates before green (the bounded set also runs `--webconsole-tsx-all` only if data or catalogue files change).

## 7. Assumptions (logged on the Book of Work)

- ASM-1646: the bar takes row 0; chrome and the homes line move down one row (19 content rows at 80x24).
- ASM-1647: F10 opens the menu; the saves list moves under Screens.
- ASM-1648: menu state is UI-local; opening and moving send nothing; costs come from the cached `f3.build` view.
- ASM-1649: an open menu takes Esc, `q` and Space before the quit and clock keys; the clock keeps running.
- ASM-1650: Enter on a Build item closes the menu and places at the F1 cursor; the refusal shows on the F1 notice; mouse is switched on only when the profile allows. Refinement made in this document (criterion 34): picking a Build item from another screen first activates F1.
- Not logged because they follow the placement item's own assumptions: costs and capacities are placeholders; one order per cell until BUG-1224 or density stacking.

## 8. Questions only Aaron can answer

1. The bar takes one row, so content is 19 rows at 80x24 (the F2 money page was fitted at 20 in BUG-1222 and gets re-proved). Is that what you meant?
   - Option A: yes, take a row; chrome moves down (recommended; it is what you ruled).
   - Option B: share row 0: menu on the left, the money figures on the right (needs measuring; may not fit 80 columns).
2. The ten levers and Build. Policy has its own menu; congestion and tourism have no building to place. Is the Build list these eight groups (Housing, Jobs, Services, Health care, Shops, Parks, Transport, Leisure)?
   ANSWERED: Yes, these eight groups, Leisure last. See "Rulings (Aaron, 2026-11-18)", Defaults.
   - Option A: yes, as drawn; Leisure last because it is not one of the ten (recommended).
   - Option B: show all ten lever names, congestion and tourism greyed "nothing to build".
3. While the menu is open, does the game clock run?
   ANSWERED: Option A, the clock keeps running. See "Rulings (Aaron, 2026-11-18)", Defaults.
   - Option A: keeps running (recommended; simplest, and nothing else on screen pauses either).
   - Option B: pauses while open and resumes when it closes.
4. Which key opens the menu?
   ANSWERED: Option A, F10 opens the menu and the saves list moves under Screens. See "Rulings (Aaron, 2026-11-18)", Defaults.
   - Option A: F10, and the saves list moves under Screens (recommended; the usual menu key).
   - Option B: keep F10 for saves; use F9 for the menu.
   - Option C: also Alt+letter direct (only if the terminal delivers Alt; not verified).
5. After you place a building, should the menu stay open or close?
   ANSWERED: Option A, it closes and remembers the last choice. See "Rulings (Aaron, 2026-11-18)", Defaults.
   - Option A: close, and reopening remembers your last choice so repeating is F10, `B`, Enter, Enter (recommended).
   - Option B: stay open until Esc, so you can place several in a row.
6. Where do the policy names come from in the terminal?
   - Option A: subscribe to the existing policies view, so the list always matches the engine (recommended; costs one subscription).
   - Option B: read `data/policies.json` directly in the terminal.
7. Grants need an amount of match funding. How is it picked?
   - Option A: three steps (25, 50, 100 percent of the pot's maximum) in a small list, then a confirm (recommended).
   - Option B: a typed amount (needs a text box the UI does not have).
8. Demolish and Enact and grant bids all ask "are you sure". Keep the confirm on all three?
   ANSWERED: Option A, confirm on all three. See "Rulings (Aaron, 2026-11-18)", Defaults.
   - Option A: yes (recommended; one stray letter must never spend or destroy).
   - Option B: no confirm on Enact.
9. Gap items (tax, borrow, repay, save, load, new city, repeal, district policies, mandates, service funding) are greyed in this item. Do you want any of them wired before sign-off? Save, load and new city look the most likely to matter to a player.
   ANSWERED IN PART: save, load, tax, borrow and new city are wired, in that order, after the menu ships. See "Rulings (Aaron, 2026-11-18)", Missing actions. Repeal, district policies, mandates and service funding are still open.
   - Option A: no, grey them now and open one item per gap after sign-off (recommended; keeps this item to the menu).
   - Option B: Save and Load first, because the engine functions already exist.
