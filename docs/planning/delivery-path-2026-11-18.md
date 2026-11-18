# Delivery path to Baseline One (after Aaron's rulings of 2026-11-18)

Written by the lead (Bev), 2026-11-18. Facts about the code come from
`docs/planning/ten-levers-survey-findings-2026-11-18.md` (file and line
references are there). Sizes and day counts are the lead's estimates, not
measurements.

## The target

Aaron signs off Baseline One on the terminal game (Go engine) at 80x24 when:
a new city starts empty; the player buys land, lays roads and homes; people
arrive; and each of ten things the mayor does moves migration, with the
screen saying why: housing, jobs, services, health care, policy, shops,
parks, transport links, congestion, tourism. Homelessness is absorbed at a
cost and shown on a meter.

## What the survey changed

1. **Three pieces of shared plumbing sit under almost every lever.** Building
   them first is the optimisation; building levers first means building each
   piece nine times, badly.
   - *The reason panel.* Migration is decided from seven weighted terms
     (jobs, housing, service coverage, environment, leisure, safety,
     reputation), a wellbeing multiplier and a cap. Those values are locals
     today and never reach the screen. Store and publish them once, and every
     lever then shows its own effect on screen with no further UI work.
   - *The placement catalogue.* The build screen offers eight zone types and
     nothing else. 356 buildings are in the data; none can be placed. Carry
     the building id through the build command and show the real catalogue.
     That one change is the precondition for services, health care, parks,
     transport links and tourism.
   - *Rule versions in saves.* Needed before any rule change lands (the
     duplicate-build fix is the first), and every lever is a rule change.
2. **"Nearest to working" is not the order I gave you.** You ruled the
   principle (nearest-to-working first); the order I listed under it was a
   guess. Measured against the code:
   - Policy is the cheapest: commands, saving and the effect on migration
     already work (the minimum wage moves arrivals today). It only lacks a
     terminal screen.
   - Parks are next: the entertainment zone already feeds the leisure term.
   - Jobs are bigger than they look: the 0.20-weight jobs term reads zero
     because no firm is ever founded and zone job capacity is never read.
   - Services move nothing today even with a clinic: nothing pushes demand.
     Health care is nearly free once services work.
   - Transport links, congestion and tourism are large: their modules are
     not connected to the game at all.
3. **Start-empty must come early.** About 112 test files build the default
   city and assume 64 residents. Every lever built before the change adds
   tests that will need re-pinning after it.
4. **One file is the bottleneck.** Nearly every lever edits the same
   function in `compose.go`. Parallel lanes there collide. The reason-panel
   work therefore also moves each lever's input into its own small file, so
   later lanes do not touch the same lines.

## The path

Each stage ends with a capture at 80x24, filed as evidence (Aaron looks only
at the end). No stage starts new work in a file the previous stage has not landed.

### Stage 0. Land what is accepted (today)
BUG-1140, BUG-1208, BUG-1222, BUG-1220, BUG-1178 (committed 2026-11-18),
then BUG-1225 and the reconnect increment. Re-capture legs 2 to 4 at 80x24.
Ask SAFE to redeploy the home server.

### Stage 1. Plumbing (three lanes, mostly separate files; about 3 days)
| Lane | Work | Why now |
|---|---|---|
| A | Reason panel: store and publish the migration terms; one on-screen "why people come / why they leave" block; each lever input moved to its own file | Every later lever proves itself on screen; ends the `compose.go` pile-up |
| B | Rule version in the save header (FEAT-9995385612), then the duplicate-build refusal (BUG-1224) under it | Unblocks every rule change; stops the money leak |
| C | Placement catalogue: building id through the build command; the build screen lists real buildings by group; committed cost shown at queue time (FEAT-9995385611) | Precondition for five levers |

### Stage 2. The new start (two lanes; about 3 days)
| Lane | Work |
|---|---|
| A | Start empty (FEAT-9995385607): zero homes, zero people, 3M pot (one number in the data), coastal charges after month 12, start hint; migration must bootstrap from zero people; fixture lane re-pins the tests that assume 64 residents |
| B | Homelessness (FEAT-9995385613): emergency places, nightly cost on the finance screen, the push on migration, the meter (in the reason panel); old saves housed on load (BUG-1217) |

### Stage 3. The quick levers (three lanes in parallel; about 3 days)
| Lane | Lever | Smallest honest link |
|---|---|---|
| A | Policy | A policies screen in the terminal game that sends the existing enact and repeal commands; minimum wage first (works citywide today) |
| B | Parks | A park in the placement catalogue mapped to a leisure venue, on the bridge the entertainment zone already uses |
| C | Jobs (BUG-782) | Standing job capacity becomes firm vacancies and employment; arrivals fade and stop at 20 percent unemployment; a jobless city loses people gradually |

### Stage 4. Services family (two lanes, then a third; about 4 days)
| Lane | Lever | Smallest honest link |
|---|---|---|
| A | Services | Placeable service buildings (from Stage 1 lane C) plus demand pushed from population, so coverage is no longer stuck at 100 |
| B | Shops | The shop zone mapped to shopping access, feeding a term |
| A, after services | Health care | Rides the services bridge plus a citizen-level access figure into wellbeing |

Alongside, as single lanes when a slot is free: density stacking
(FEAT-9995385606), council tax (FEAT-9995385610), hotels appearing per 1,000
people.

### Stage 5. The connected city (the large ones; about 7 days)
In this order, because each needs the one before:
1. Roads: a dwelling lays its own road; the player's road tool
   (FEAT-9995385608).
2. Transport links: road and transit capacity replaces the placeholder cap
   on arrivals (the cap is a constant 1,000,000 today); rail and airport
   connected to the game.
3. Congestion: travel demand pushed into the traffic module; commute time
   into wellbeing.
4. Tourism: the tourism module connected; hotels as accommodation; visitors.

### Stage 6. Sign-off
One scripted walk-through at 80x24 from an empty city through all ten
levers, each with its control-against-treatment numbers, plus the same by
hand. Aaron marks the legs.

## Estimate

About 20 working days of lane time at last night's measured pace (a
medium item took three to five review rounds, roughly half a day each),
with three Go lanes at once. Stages 1 to 4 are about 13 days and give you
seven of the ten levers; Stage 5 is the long tail. This is an estimate; the
first two stages will show whether it holds.

## How the work is run (lessons from the overnight run)

- One owner per hot file per stage; a lane never starts in a file with
  unlanded work.
- Land after every accepted review, CI checked between pushes; nothing
  waits in a queue overnight.
- Every rule change states what an old save does; every lever ships a
  control-against-treatment test and a frame test at 80x24.
- Reviews record their verdict early; the lead runs any leftover checks.
- Opus reviews only for money, saves and restore; Sonnet builds and
  reviews the rest.
- Hosting work runs only when game lanes are blocked on a landing or a
  ruling.

## Rulings (Aaron, second interview, 2026-11-18; verbatim on the Book of Work)

- Lever order: the survey order above is confirmed.
- The migration formula becomes **ten equal weights**, one term per lever at
  10 percent each. It lands with the reason panel in Stage 1 and re-pins the
  migration tests once; today's seven terms (safety, environment, reputation)
  fold into the ten or drop out, to be stated in that item's criteria.
- Check-ins: **only at the end**. No stage walk-throughs; the captures are
  still taken and filed after each stage as evidence.
- Unattended overnight runs every night, game lanes only, with a morning
  report.
- First residents: a founding wave fills up to half the first homes at the
  next month close.
- Hotels: the first ten are funded by an outside investor (an invented
  company name; no real brand in the repository); after that the funder
  rotates between the city's firms, the outside investor, the city treasury
  and a list of 20 invented hotel chains. A hotel empty for 12 months is
  removed and its land freed.
- The shared branch guard stays off until after Baseline One.
