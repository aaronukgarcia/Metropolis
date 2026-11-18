# FEAT-9995385615: Placement catalogue (Baseline One, Stage 1 lane C)

Folds in FEAT-9995385611 (committed cost shown at queue time).
Written by the business analyst, 2026-11-18. Documents only. Facts below come
from the code at HEAD 684b616 (file:line given). Items marked UNVERIFIED were
not confirmed in the code.

## 0. What this item is, in four lines

1. The build command already carries one string, `BuildingType`. Today the
   engine reads it only as a zone name and drops everything else.
2. After this item the string may also be a building id from a short curated
   list. The engine turns it into a zone plus a building id and builds it.
3. The F3 screen lists that curated list by group, with the real cost and
   capacity. A group that is not yet wired into the game is shown as "not
   active yet" and cannot be placed.
4. F3 also shows the committed cost: money promised to queued orders and not
   yet paid.

The levers themselves (jobs, services, health, parks, shops, transport,
tourism) are NOT in this item. This item only makes placement possible and
honest. Be clear to Aaron: after this item the only things the player can
place are the same two that move migration today (dwelling, entertainment).
Everything else is visible as "not active yet" until its lever lane flips it.

## 1. Today, from the code

### 1.1 Path of a build, keypress to standing structure

1. Player moves the cursor on F1, then presses `b` on F1 (builds a dwelling,
   `mapBuildDefaultZone`, cmd/metropolis/boot.go:1070-1076, key at :1147) or
   `b` on F3 (builds the selected catalogue row at the F1 cursor,
   cmd/metropolis/boot_screens.go:104-115). The cell must be bought first (`y`).
2. F3 sends `protocol.BuildPayload{Cell, BuildingType}` through
   `BuildOn` (internal/ui/screens/build/commands.go) as `KindBuild`.
   The payload type is internal/protocol/commands.go:226-229.
3. compose handles `KindBuild` at internal/engine/compose/compose.go:4336-4354.
   It maps the cell, reads the month, and calls
   `buildAPI.SubmitBuildCommand(build.BuildCommand{Tile, Local, OwnerID, Zone: build.ZoneType(p.BuildingType), Month})`.
   `BuildingID` is not set. This is the gap compose.go:449-460 documents.
4. `SubmitBuildCommand` (internal/engine/build/build.go:657-717): looks up the
   zone in the zone catalogue (unknown gives MET-G501), checks ownership
   (`requireOwned`, :597), reads the seasonal multiplier, and appends a
   `buildOrder` with the zone's materials, labour and lead time. It accepts
   `BuildingID` already (:201, stored at :696) and is deliberately lenient:
   an unknown or empty id is never an error at submission (:195-200, AC-7).
5. `BuildAPI.Tick` (:857-1033) each simulated day draws materials from
   logistics, burns labour, counts down lead time. On completion (:936-1029)
   it registers a service with engine.services only if the catalogue entry
   for `buildingID` declares `serviceKind` (:953), then sets
   `zoneState[cell] = order.zone` and `structures[cell] = order.id`
   (:1022-1024).
6. Capacity comes from the ZONE, not the building:
   `StandingCapacities` (:1517-1549) reads `zoneState` and the zone record's
   `residentCapacity` and `jobCapacity`. compose uses it for homes
   (compose_housing.go syncHousing, per the survey).
7. Money: `accrueAndSettleConstruction` (internal/engine/compose/moneycirc_inc2.go:222-273)
   adds `materialsDrawn delta x constructionMaterialLedgerPriceMicropoundsPerTonne`
   (150,000 micropounds, i.e. 150 pounds a tonne, :129) to an accrual as
   materials are drawn. The accrual is paid out only when `dayTick % 90 == 0`
   (`commercialPaymentTermTicks`, NET-90). So money leaves every 90 days,
   not at each month close. See question 2.

A dwelling is 100 tonnes, so 15,000 pounds (the figure in BUG-1224).

### 1.2 What a building entry carries (data/buildings.json, 356 entries)

Loader: internal/foundation/data/buildings.go:105 `BuildingEntry`. Fields:
`id`, `name`, `catalogueSection` (22 section codes: R, E, W, H, ED, F-P, G,
PK, L, T, PT, HS, C-I, LM, CM-WF, MP, SEC, FARM, MINE, COMMS, SUP2, SUP3),
`unlock` (milestone, DP, achievement flags), `costRaw`, `capacityRaw`,
`consumptionRef`, `blightClass`, `appealProfile`, `notes`, and on 8 entries
only `serviceKind`, `coverageRadius`, `staffingNeed`.

What it does NOT carry: any number for cost or capacity. `costRaw` and
`capacityRaw` are free text ("600k", "150 visits/d", "8 hh/ha", "opex",
"zoning"). Housing entries (section HS) have no cost at all. No entry has a
materials bill, labour, lead time, resident capacity or job capacity.
Those numbers exist only on the 8 zones (`zones[]` in the same file):
dwelling 100t / 8 homes, shop 80t / 10 jobs, office 150t / 40, entertainment
200t / 15, farming 60t / 6, manufacturing 250t / 50, heavy_industry 400t /
80, mining 300t / 30.

The 8 entries with `serviceKind`: clinic, small_hospital, general_hospital,
teaching_hospital (healthcare); one_room_school, primary_school,
secondary_school (education); fire_station (fire). No building id equals a
zone id (checked).

So "356 buildings" cannot all be placed by wiring an id through. Most have
no numbers to build from. That is why section 2a proposes a short curated
list with a small data file.

### 1.3 The eight zone types

dwelling, shop, office, entertainment, farming, manufacturing, heavy_industry,
mining (build/zone.go:24-31). Survey: only dwelling (homes) and entertainment
(one leisure venue, capacity 500, compose.go:3213-3236) change migration
today. Shop, office, farming, manufacturing, heavy_industry and mining add
job capacity that compose never reads (survey, jobs row), so they take the
player's money and do nothing.

### 1.4 Where the building id is dropped

compose.go:4350-4353 (the `KindBuild` case): `build.BuildCommand` is built
without `BuildingID`. The protocol and the build module both already allow
it. One call site loses it.

### 1.5 What F3 shows at 80x24

Screen content is 20 rows (24 minus top bar, homes row, hint row, status
bar: `chromeReservedRows` 2 + 2, cmd/metropolis/chromebar.go:35-38 and :135-154).
`buildDrawFunc` (boot_screens.go:168-206): row 1 header, then a left column
(zones above, catalogue below) and a right column (queue), each half the
width, last row the key line or `REJECTED: ...`. The catalogue shown is the
zone list (`catalogue[]` = the eight zones, build_publish.go:86-123,
decision record at :38-62), with cost = materials x 150 pounds and capacity
text "N homes, M jobs". Section is the literal "zone". Every row is
"unlocked". Keys: `n`/`p` step the selection, `b` queues it at the F1
cursor. The hint row text is `screenHintBuild` (screenhints.go:19-20).

## 2. Design decisions

### 2a. The catalogue shown to the player

One new data file, `data/placement.json`, is the curated placement list. It
is the only place that says which buildings the player may place. Reason:
buildings.json is a transcribed spec table (GR#15) and has no numbers;
adding numbers to 356 rows is balance work Aaron has not asked for.
Alternative rejected: derive placeability from buildings.json fields
(only 8 entries have anything to derive from).

An item is eligible when ALL of these hold (a loader test enforces them):
1. its `id` exists in buildings.json, or it is one of the 8 zone ids;
2. it names `zone`, one of the 8 zone ids (the zone supplies materials,
   labour, lead time and capacities in this item; per-building figures are
   a lever lane's decision);
3. it has a one-line `summary` of 60 characters or fewer, plain English;
4. its group exists in the group list of the same file;
5. if it has `serviceKind` in buildings.json, the group is a service group
   (health, services); a serviceKind entry in any other group fails the load.

Curated list for Baseline One (names are the ones buildings.json uses):

| Group | wired after this item | Items (id) |
|---|---|---|
| homes | yes | dwelling (zone) |
| leisure | yes | entertainment (zone) |
| jobs | no (jobs lane) | office, farming, manufacturing, heavy_industry, mining (zones) |
| shops | no (shops lane) | shop (zone), corner_shop_general_store, supermarket |
| parks | no (parks lane) | pocket_park, village_green, town_park |
| health | no (services then health lanes) | clinic, small_hospital, general_hospital |
| services | no (services lane) | one_room_school, primary_school, secondary_school, fire_station |
| transport | no (needs roads, FEAT-9995385608, and a traffic network) | bus_station, external_rail_station, regional_airport |

That is 8 groups and 24 items, 2 placeable. Why these: they are the
cheapest entry of each kind that the survey says a lever needs (clinic
before hospital chain, pocket park before botanical garden). The four
healthcare/education entries with serviceKind are exactly those the build to
services bridge can register today. Left out on purpose: first_aid_post,
pharmacy, ambulance_station (no serviceKind, so they cannot register with
services); hotels (FEAT-9995385613 builds them automatically; the player
never places one); roads (FEAT-9995385608 owns the road tool); the whole
HS housing section other than dwelling (density stacking, FEAT-9995385606,
is how homes grow, and per-type capacity needs a per-building capacity read
that does not exist); the other 330 entries.

Unlock milestones (M1..M12, DP, achievements) are ignored in this item. The
unlock gate is not wired for placement (build_publish.go:46-50), and showing
a milestone badge the engine ignores would be a second lie. See question 3.

Alternatives, one line each: list all 356 (most have no numbers, most do
nothing: the exact built-but-not-wired pile); mark placeability inside
buildings.json (mixes balance data into a transcribed spec table).

### 2b. How a group is marked wired

In `data/placement.json`, `groups[].wired` (true or false) and
`groups[].leverItem` (the BOW code of the lane that flips it, e.g. the
services lane). This is the ONE place a lever lane flips. Rule for a lever
lane: set `wired` to true in the same commit that makes the lever show its
effect on the screen's reason panel, and attach the 80x24 capture.

A Go test pins the exact set of wired groups (`homes`, `leisure` after this
item) as a literal list. Flipping a group therefore forces a visible
one-line test change in the lever's commit. Reason: nobody can flip a flag
quietly. Alternative rejected: a Go constant (GR#15 says data, not
literals).

What the player sees for an unwired group: the group is collapsed to one
line, `NAME   not active yet: item, item, item`, clipped to the width. Its
items cannot be selected by `n`/`p` and `b` can never queue them. Reason:
the player's money is never taken for something that does nothing, and the
list stays short. When all eight groups are wired the list is up to 31 rows
(8 headers + 23 placeable items), so the list must scroll (2d).

Engine rule behind it (2c): the engine refuses an unwired item too, so the
rule does not live only in the screen.

### 2c. The command

The protocol payload does NOT change. `BuildPayload.BuildingType` stays the
single string. Reason: the journal stores this payload; an unchanged shape
means every existing journal entry keeps its meaning with no migration.

Resolution (new method `BuildAPI.ResolvePlacement(buildingType string)
(zone ZoneType, buildingID string, err error)` in the build module, new file
internal/engine/build/placement.go, reading the loaded placement file):
1. `buildingType` is a zone id: return that zone and an empty building id.
   This is today's behaviour, byte for byte, including for the 6 zones the
   screen will mark not active (see question 1).
2. Else it is an item id in the placement file: if its group is unwired,
   reject with new `ErrGroupNotActive`; otherwise return the item's zone and
   the item id.
3. Else if the id exists in buildings.json but not in the placement file,
   reject with new `ErrBuildingNotPlaceable`.
4. Else reject with the existing `ErrUnknownZoneType` (MET-G501), unchanged,
   so existing tests that send a made-up string keep their expected code.

compose.go:4350-4353 changes to call `ResolvePlacement` first and pass both
results into `BuildCommand`. `SubmitBuildCommand` itself is NOT changed: its
lenient `BuildingID` behaviour (AC-7) is pinned by many tests, and direct
callers inside the engine keep it.

Journal and old saves: nothing is stored differently. A journal entry
recorded before this item contains `BuildingType` equal to one of the 8 zone
ids and takes branch 1, which is the old code path. So existing entries
replay identically and no rule-version mechanism (FEAT-9995385612) is needed
for this item. The same holds for snapshots: `BuildingID` is already saved
per order (build/participant.go:126, :432). New orders that carry a building
id replay the same way new code runs them. One caveat to state plainly:
editing the figures of a zone in buildings.json, or an item's `zone` mapping
in placement.json, after play has started changes how old orders replay.
That is the same rule-change problem every balance edit already has, owned
by FEAT-9995385612. This item must not edit existing zone figures.

New error codes: two, `ErrGroupNotActive` and `ErrBuildingNotPlaceable`, in
the build range. MET-G510 is already taken by the held, unlanded BUG-1224
refusal (worktree agent-ad5c8b543590393b6). The developer must claim the two
codes with `tools/plan/add-error.js` (claim-range, then add, then check)
before starting, and the lead must pre-assign them in the brief so lane B
(which lands G510) does not collide.

Not done here: the engine does not yet refuse a zone-only build of the six
not-active zones (that would reject entries in existing journals and needs
the rule version). Another client (web console) can still send them. This
is recorded as a follow-up under FEAT-9995385612, not fixed here.

### 2d. The F3 screen at 80x24

One column, stacked. No new keys: `n` and `p` walk the placeable items only
(unwired items are skipped), `b` queues the selected item at the F1 cursor,
as today. Scrolling follows the selection (window of the list rows; a line
`... N more above/below` when clipped). The hint row stays
`F3 BUILD  n/p choose | b queue at the F1 map cursor (move it on F1)`, which
stays true. The hint is for the existing keys; no new key means no hint
change. The old on-screen line `keys: n/p select, b build at map cursor` is
kept as the notice row and replaced by `REJECTED: <reason>` as now.

Rows (20 content rows), 80 columns, sample state: cursor on cell (12,7),
Dwelling selected, 3 orders queued (2 dwellings, 1 entertainment), treasury
1,500,000, committed 60,000:

```
F3 Land & Construction
Cell (12,7) from the F1 cursor
Committed £60,000 of £1,500,000 treasury
HOMES
> Dwelling                                     £15,000    8 homes
LEISURE
  Entertainment                                £30,000    15 jobs
JOBS       not active yet: Office, Farming, Manufacturing, Heavy industry, Mini…
SHOPS      not active yet: Shop, Corner shop, Supermarket
PARKS      not active yet: Pocket park, Village green, Town park
HEALTH     not active yet: Clinic, Small hospital, General hospital
SERVICES   not active yet: One-room school, Primary school, Secondary school, F…
TRANSPORT  not active yet: Bus station, Rail station, Regional airport

Dwelling: a home for 8 people. Built on the F1 cursor cell. Paid at settlement.
Build queue (3)
  #1 Dwelling (12,7)       in-progress    lead 31d    £15,000
  #2 Dwelling (13,7)       materials-pending            £15,000
  #3 Entertainment (14,7)  materials-pending            £30,000
(row 20: keys: n/p select, b build at map cursor   or   REJECTED: <reason>)
```

Money format: whole pounds with thousands separators, no pence ("£15,000").
The current catalogue text uses "£%.2f" (build_publish.go:46-51); the
developer follows the finance screen's format and the frame test pins it.
Capacity text for a zone item stays "N homes" or "M jobs" (zero side
dropped); today's "8 homes, 0 jobs" shows a meaningless zero.

At 60 columns: the cell line and committed line stay (`Committed £60,000 /
£1,500,000`); the cost column stays; the capacity column is dropped; the
"not active yet" lines clip with an ellipsis at column 60 (`JOBS       not
active yet: Office, Farming, Manufacturing, …`); the selected-item line
shortens to `Dwelling: a home for 8 people. Paid at settlement.`; queue rows
drop the lead-time column. The committed line and the selected line must
never be cut. Rows below 20 (a lane A chrome row may shorten the content
area to 19): the screen reads the rect height, never a literal 20, and drops
in this order: the blank row, queue rows from the bottom, then the
collapsed-group lines from the bottom. Header, committed line, the selected
item and the notice row are never dropped.

The committed-cost warning replaces row 3 text when committed exceeds the
treasury: `WARNING committed £2,100,000 is more than the treasury £1,500,000`
(2e). It does not stop the build.

### 2e. Committed cost

Definition: the pounds already promised to queued orders and not yet paid.
That is (tonnes not yet drawn on incomplete orders x 150 pounds a tonne) plus
the accrual of tonnes drawn but not yet settled. It falls when the
settlement posts (the accrual is zeroed in `accrueAndSettleConstruction`),
and it is exactly the money that settlement then removes from the treasury.

Source, one money model: the build module exposes
`CommittedMaterialsTonnes()` (sum of `materialsRemaining` over incomplete
orders, read under its lock). compose multiplies by
`constructionMaterialLedgerPriceMicropoundsPerTonne` (the same constant the
ledger charges with, not a copy) and adds `constructionAccrualLocal +
constructionAccrualExternal`. The `f3.build` view carries two new integers,
`committedMicropounds` and `treasuryMicropounds`, so the screen needs no
second view (the screen does not join views).

Warning, not refusal (Aaron's choice): the screen shows the warning line
(2d). The engine does not refuse a build because committed exceeds the
treasury. Reason: Aaron chose "show". Note UNVERIFIED: whether a settlement
that exceeds the treasury overdraws or is clipped; the developer finds out
and reports it on the BOW item (not fixed here).

Known difference: the ledger multiplies by a planning-funding cost error rate
(`applyBuildCostError`, a no-op at full funding). The displayed committed
cost is the base figure. The developer must say this in a code comment and
the criterion tests at full funding only.

Timing: "the money leaves at the month close" is not what the code does
today (NET-90, every 90 days). This item states the truth on screen: the
selected-item line says "Paid at settlement", not "at month close". See
question 2.

### 2f. Density stacking and the BUG-1224 refusal

One structure per cell today. Lane B lands the BUG-1224 refusal (a second
order on a cell with a queued or standing structure is refused, MET-G510).
This item adds nothing to that and must not depend on it landing first. The
two combine like this:
1. Every item in the curated list builds on one cell and takes the cell. A
   second order on the same cell, with any item, is refused by lane B's rule
   once it lands. Before it lands the old (money-wasting) behaviour stands;
   this item's tests use distinct cells.
2. When density stacking (FEAT-9995385606) replaces the refusal, the stacking
   rule applies to homes only at first (a further dwelling order adds a
   level). Non-home items on an occupied cell are still refused. The
   catalogue needs a `stackable` flag per item then; this item does not
   add it, but the file format must tolerate unknown future fields being
   added by a later version, so the loader rejects unknown fields only in
   this version's schema (a version number in the file).
3. The F3 list never shows a "can stack" hint in this item.

## 3. Acceptance criteria

Test kinds: B = build unit test (internal/engine/build); C = compose test on
the default composition (internal/engine/compose); F = cmd/metropolis frame
test at 80x24 on the real boot path with real key input (draw a frame first;
the cursor cannot move before the first draw); J = journal replay test;
D = data loader test (internal/foundation/data).

### Data

1. D. `data/placement.json` loads. Every item id exists in buildings.json or
   is a zone id; every item names a valid zone; every item has a summary of
   60 characters or fewer; every item's group exists; a serviceKind entry
   in a non-service group fails the load. Expected: load OK for the shipped
   file; each of five broken fixture files fails with its own named error.
2. D. The shipped file has exactly 8 groups and the 24 items listed in 2a,
   with exactly `homes` and `leisure` wired. Expected: a literal-list
   comparison passes.
3. D. A building id that appears twice, or that is both a zone id and an
   item id, fails the load. Expected: named error.

### Build module

4. B. `ResolvePlacement("dwelling")` returns zone dwelling and an empty
   building id. `ResolvePlacement("entertainment")` likewise.
   Expected: exact values.
5. B. With a fixture file where group `health` is wired,
   `ResolvePlacement("clinic")` returns the item's zone and building id
   `clinic`. Expected: exact values.
6. B. With the shipped file, `ResolvePlacement("clinic")` fails with
   `ErrGroupNotActive`; `"first_aid_post"` (in buildings.json, not in the
   file) fails with `ErrBuildingNotPlaceable`; `"zzz"` fails with MET-G501.
   Expected: exact codes.
7. B. `CommittedMaterialsTonnes()` is 0 with no orders; after two dwelling
   orders it is 200; after one has drawn 40 tonnes it is 160; a completed
   order contributes 0. Expected: exact numbers.

### Compose (default composition)

8. C. Send `KindBuild` with `BuildingType: "dwelling"` on a bought cell:
   the order exists with an empty `BuildingID`, and the result is identical
   to the pre-change behaviour (same order, same bill). Expected: same
   fields as a pinned pre-change capture.
9. C. With a fixture composition where `health` is wired, send `KindBuild`
   with `BuildingType: "clinic"` on a bought cell: the queued order carries
   `BuildingID == "clinic"`; after enough ticks it completes and the service
   is registered with engine.services (a service with kind healthcare
   exists). Expected: both observations.
10. C. With the shipped file, send `KindBuild` `"clinic"`: the result is
    rejected with `ErrGroupNotActive`, no order exists, and the treasury and
    the committed cost are unchanged after a settlement day. Expected:
    rejection code, empty queue, equal money.
11. C. The published `f3.build` view lists the curated items by group: 24
    items, each with `group`, `wired`, `summary`, `costMicropounds` and
    `capacityText`; wired items have a cost equal to materials x the ledger
    price; unwired items have `wired:false` and no cost. Expected: exact
    contents for the shipped file.
12. C. Committed cost equals money that leaves. Queue two dwellings
    (distinct cells), read `committedMicropounds` (expected 200 tonnes x 150
    = 30,000 pounds), run to the settlement day, read the treasury before
    and after the settlement tick, and `committedMicropounds` after.
    Expected: treasury drop equals the committed figure read before (full
    funding, no cost error), committed after is 0.
13. C. Committed cost is visible at queue time: after the `KindBuild` result
    is accepted and before any tick, the next published `f3.build` patch
    carries `committedMicropounds` of 15,000 pounds in micropounds for one
    dwelling. Expected: exact number.
14. C. Warning data: with a treasury forced below the committed figure, the
    view carries both numbers (committed greater than treasury) and a
    further `KindBuild` is still accepted (not refused). Expected: accepted.

### Journal

15. J. A journal recorded by the pre-change code (fixture file captured from
    the current build: buys, a zone-only `KindBuild "dwelling"`, a
    `KindBuild "entertainment"`) replays on the new code with the same
    state digest, population and treasury as an uninterrupted control, from
    genesis and from snapshot plus tail. Expected: digest equal.
16. J. A new journal containing a `KindBuild "clinic"` under the wired
    fixture replays to the same digest as the uninterrupted run, and the
    restored city still has the registered service (snapshot restore
    re-registers it, as the services bridge does today). Expected: digest
    equal, service present.

### Screen (frame tests at 80x24, real boot path, key input)

17. F. After the first draw, F3 shows the frame in 2d for the sample state
    (character-exact comparison, except the lead-time numbers which the
    test reads from the live queue). Expected: the 20 rows match.
18. F. `n` from Dwelling selects Entertainment; `n` again wraps to Dwelling;
    `p` goes back. An unwired item is never selected. Expected: the `>`
    marker is on the expected row after each key.
19. F. `b` with Entertainment selected queues an order whose published zone
    is entertainment, and the queue row appears in the frame within the
    ticks needed. Expected: queue shows `Entertainment`.
20. F. Every key named in the F3 hint row (`n`, `p`, `b`) changes the frame
    or the queue; no key in the hint is dead. No key outside the hint is
    needed. Expected: the existing hint-truth test passes unchanged.
21. F. With committed above treasury, row 3 reads the WARNING text and `b`
    still queues. Expected: warning text present, queue grew by one.
22. F. At 60x24 the frame keeps the committed line and the selected line
    uncut and clips "not active yet" lines with an ellipsis at column 60.
    Expected: exact rows.
23. F. With a fixture where all eight groups are wired (31 list rows), the
    list scrolls: the selected row is always on screen after 24 `n`
    presses, a "more above/below" line shows when clipped. Expected:
    selected marker visible in every frame; no row beyond 80 columns.
24. F. A rejected build (unowned cell) still shows `REJECTED: <reason>` on
    row 20 as today (existing test
    `TestWireTUI_AC_B4_RejectionReasonShown`, input change only). Expected: unchanged.

## 4. Can-fail proof for each criterion

The developer proves each test can fail by making the stated change and
seeing the test go red, then reverting.

1. Remove a required field check in the loader: the matching broken-fixture
   case loads when it should fail.
2. Flip `wired` on `jobs` in the shipped file: the pin fails.
3. Add a duplicate id to a fixture: loader must fail; skip the duplicate
   check and the test fails.
4. Make `ResolvePlacement` return the item id for a zone: the empty-id
   assertion fails.
5. Return the group's zone but an empty id: assertion on `clinic` fails.
6. Skip the wired check: `clinic` resolves and the rejection test fails.
   Return G501 for the not-placeable case: the code assertion fails.
7. Count completed orders: expected 0 contribution fails with 100 added.
8. Pass the building type as the id even for zones: the empty-id assertion
   fails.
9. Revert the compose change (the one line): `BuildingID` is empty and the
   order and the service assertions fail.
10. Move the wired check to the screen only: the engine accepts and the order
    exists, so the test fails.
11. Publish the old zone list: the count 24 fails.
12. Change the committed formula to use the full bill (not remaining): the
    equality fails after the first draw. Forget to add the accrual: it
    fails on the day after materials are drawn and before settlement
    (add one assertion at that point).
13. Compute committed from drawn tonnes only: it is 0 at queue time and the
    assertion fails.
14. Make the engine refuse when committed exceeds treasury: the acceptance
    assertion fails.
15. Make resolution reject unknown-to-placement ids that are zones: the
    replay stops on the first command and the digest comparison fails.
16. Do not persist `buildingID` (a mutant of the participant): the service
    is absent after restore and the test fails.
17. Change a column width or a label: the character-exact comparison fails.
18. Let the selection include unwired items: the marker lands on a "not
    active" row and the test fails.
19. Send the item name instead of the id from `BuildOn`: the zone assertion
    fails.
20. Add a hint key that is not wired: the hint-truth test fails (it feeds
    every hinted key).
21. Make the screen refuse locally: the queue does not grow and it fails.
22. Remove the clip logic: a row exceeds column 60 and the width assertion
    fails.
23. Remove the scroll offset: the selected marker leaves the frame.
24. Remove the notice row draw: no `REJECTED` text.

## 5. Fallout

### 5.1 Existing tests that move

- `internal/engine/compose/build_publish_test.go`
  (`TestBuildView_AC_B2_B3_QueuedBuildVisible` and the attack tests that
  read `catalogue[]`): they assume `catalogue[]` is the zone list. The
  catalogue entries gain fields; the section is no longer the literal
  "zone". The developer lists every assertion on `Section`, `CostRaw` or
  `CapacityRaw` and updates inputs, not meaning.
- `cmd/metropolis/wire_tui_screens_test.go` (`...AC_B2_BuildScreenShowsEngineState`,
  `...AC_B4_RejectionReasonShown`): both use `cat[0]` and draw its name. The
  new ordering puts `dwelling` first among placeable items, but the list
  now includes unwired entries; the tests must pick the first PLACEABLE
  entry. Input change only.
- `cmd/metropolis/bug1220_hints_frame_test.go` and
  `bug1220_housing_frame_test.go` and `bug1220_r2_narrow_frame_test.go`:
  they assert the F3 key line, catalogue cost/cap text and narrow-width text
  (`cost`, `cap`, "homes, jobs" format). These are pinned strings and will
  change; list them in the commit.
- `cmd/metropolis/feat083_recapture_test.go`, `bug1222_f2_80x24_test.go`
  (F2 only, expected unaffected), `feat211_inc1_*` (screen registry:
  unaffected unless the F3 entry changes).
- `internal/ui/screens/build/*_test.go` (render_test, screen_test,
  sf1_test, sf3_test, copyguard_test, determinism_test, race_test): the
  `CatalogueEntry` type gains group, wired, summary, cost and capacity text;
  the render functions change. Copyguard and determinism tests must still
  pass (no clock, no map range in render).
- `internal/engine/build/*_test.go`: no assertion changes expected,
  `SubmitBuildCommand` is unchanged; services_bridge and attack tests stay.
- Goldens: the snapshot goldens under
  `internal/harness/uitest/testdata/snapshots/` render the Folkestone boot
  and F1; UNVERIFIED whether they include the F3 screen. Check before
  regenerating; regenerating is allowed only for an F3 text change.
- Web console / TypeScript: `data/` change and a new `catalogue[]` field set
  could hit webconsole tests that read `f3.build` (UNVERIFIED; the
  memory note says catalogue/data changes must also run
  `--webconsole-tsx-all`, BUG-1188).
- `data/errors.json` and the errors scan test: two new codes.

### 5.2 Protocol view shape (contract-first, GR#20)

- `BuildPayload` is unchanged: no protocol type change, no parity fixture
  change, no `ProtocolVersion` bump.
- The `f3.build` view changes. compose owns its own wire copy
  (build_publish.go `buildWire*` structs) and the UI package owns a second
  copy (internal/ui/screens/build/wire.go). Both are changed together:
  - catalogue entry gains `group`, `wired`, `summary`, `costMicropounds`,
    `capacityText` (and keeps `id`, `name`, `unlockState`; `section`,
    `costRaw`, `capacityRaw`, `notes` stay for the verbatim spec text);
  - patch gains `groups[]` (id, label, wired, order), `committedMicropounds`,
    `treasuryMicropounds`;
  - `buildWireSchemaVersion` goes from 1 to 2, and the UI decoder reads
    version 1 and 2 (a version 1 patch has no groups, so everything is
    treated as one wired group; this keeps old fixtures readable).
- The view is not registered in code.json by name (grep found no `f3.build`
  in code.json), so the registry needs no new view entry. The new data file
  should be registered the way buildings.json is (4 mentions in code.json);
  UNVERIFIED how, run `/codejson-audit` and `/register-guid` flow. Edge
  lint: build already imports foundation/data; compose already imports
  build; the new loader is in foundation/data. No new Go import edge is
  expected. Run `/edge-lint` and report.
- The UI must not import the engine (protocol-only split): `ResolvePlacement`
  runs in the engine; the UI only sends the item id as `BuildingType`.

## 6. Out of scope, assumptions, questions

### 6.1 Out of scope

- The levers (jobs, services, health, parks, shops, transport,
  congestion, tourism, policy) and anything that makes a placed non-home
  building change migration.
- A road tool and road access checks (FEAT-9995385608).
- Hotels (FEAT-9995385613 builds them automatically).
- Density stacking (FEAT-9995385606) and the BUG-1224 refusal (lane B).
- Per-building capacities, costs and lead times (a lever lane adds them to
  the item when it wires the group).
- The web console build UI.
- Engine refusal of the six not-active zones for other clients (needs the
  rule version).
- Unlock milestones, development points, achievements.
- Demolition changes.

### 6.2 Unstated assumptions (change nothing unless Aaron says)

1. The player owns the cell first (`y` on F1); F3's `b` builds at the F1
   cursor, as today. Cell selection is not changed.
2. The cost shown is the materials bill at 150 pounds a tonne. That is not
   the `costRaw` figure in buildings.json (a clinic's 600k). The shown cost
   is what the ledger will charge; `costRaw` is spec text. Real-money
   scale for service buildings is a balance pass for the lever lane.
3. One order per cell (BUG-1224 or the old behaviour); tests use distinct
   cells.
4. The placeable list is small enough that no free-text search is needed.
5. A wired group has all its items live at once; there is no per-item wired
   flag.
6. All money figures are placeholders pending Aaron's balance pass.

### 6.3 Questions only Aaron can answer

1. Six existing zones (shop, office, farming, manufacturing, heavy industry,
   mining) can be built from F3 today and do nothing. May they become "not
   active yet" on F3 until their lever lanes land, which removes something
   the player can do today?
   - Option A: yes, hide them as not active (recommended; it matches your
     rule that the player's money is never taken for nothing).
   - Option B: keep them placeable and label them "adds jobs, no effect
     yet".
   - Option C: keep them exactly as they are.
2. The code pays for construction every 90 days (NET-90), not at the month
   close. The committed cost falls on that settlement. Which do you want?
   - Option A: keep the 90-day settlement and say "paid at settlement"
     (recommended for this item; changing it is a rule change for the
     rule-version lane).
   - Option B: change it to month close in a later item.
3. Ignore unlock milestones and development points for placement in
   Baseline One (every curated item is available once its group is wired)?
   - Option A: yes (recommended; there is no unlock gate wired for
     placement).
   - Option B: respect M-levels from buildings.json (needs a wiring
     decision about what a milestone means).
4. Should this stage also make one new real building placeable, for
   example a pocket park mapped to the leisure venue, so the capture shows
   something new beyond dwelling and entertainment?
   - Option A: no, the parks lane does it (recommended; keeps this item
     to plumbing and honesty).
   - Option B: yes, pocket park mapped to the entertainment zone (about a
     day more, needs a venue figure ruling).

## 7. Size and developer lanes

Size guess: M. About 2 to 3 developer-days plus a round: new data file and
loader, one build method, one compose line, a wider view, a rewritten F3
render, and about 24 tests plus the fallout list in 5.1. The wide part is
the fallout, not the design.

Split (no overlap with lane A, which works in compose.go's attract and the
chrome rows, or lane B, which works in save, journal, snapshot and
`SubmitBuildCommand` for G510):

- C1, engine and data (Sonnet developer): `data/placement.json`,
  `internal/foundation/data/placement.go` (+ test),
  `internal/engine/build/placement.go` (+ test), new error codes, and the
  single compose.go edit at the `KindBuild` case (:4350-4353).
  Criteria 1-10, 15, 16. Files shared with others: compose.go (lane A edits
  other regions; ask lane A to rebase before landing) and
  `build/errors.go` (lane B adds G510 there: claim codes first).
- C2, committed cost: `build.CommittedMaterialsTonnes`, the compose
  computation and the `committedMicropounds` / `treasuryMicropounds`
  fields (new small file `compose/build_committed.go`). Criteria 7, 12-14.
  Starts after C1's resolver lands; touches `build_publish.go` (shared with
  C3, so C2 merges first).
- C3, view and screen: `compose/build_publish.go` (catalogue fields),
  `internal/ui/screens/build/*`, `cmd/metropolis/boot_screens.go`,
  `screenhints.go` and their frame tests. Criteria 11, 17-24. Starts after
  C1 so the published list exists. Must read the rect height (lane A may
  change the number of chrome rows).

The three can run as one developer in sequence or C2 and C3 in parallel
once C1 is in. Per project rule: round-first (GR#23); BUDGET line on every
dispatch; agents verify only the files they touch plus one scoped run; the
lead runs the full gates before green.
