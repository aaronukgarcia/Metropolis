# World-class tourism catalogue, hotels, and 3+ sites/day itinerary

**Northstar:** waypoint 2 (webconsole dogfood). §44 visitor economy. Not NASA — one parent FEAT, 50 catalogue rows (not 50 BOW items).

**Mkeys:** parent FEAT (this file) · hotels child · itinerary/attract child.

**GR#25:** webconsole-internal. Do **not** register `engine.tourism` / `engine.attract` edges in this wave. Go MOD-057 stays the later convergence.

**Balance-number regime:** every capex/opex/MW/water/jobs/visitors/tourism figure below is PLACEHOLDER until Aaron's row-by-row pass. Tests are directional (bigger class → bigger draw/load), never pinned magnitudes.

## Increments

| Inc | Ship | Notes |
|-----|------|--------|
| **1** | 50 attraction `PH()` + 5 stay `PH()` in palette **Tourism** / **Stay** | Grey "coming soon". `placeholder: true`, zero sim stats, `canEnterSim` false. |
| **2** | Graduate attractions in waves (S then M then L/XL/Mega) | Real `cost`/`upkeep`/`jobs`/`mw`/`tourism`. Water via existing consumption coefficients when that path exists; until then document m³/day on the spec extra only if a field exists — do **not** invent a second water table. |
| **3** | Graduate hotels / B&B / resort / caravan | Accommodation **stock** caps staying visitors (day-trippers uncapped by beds). |
| **4** | Itinerary force | If a visitor can do **≥3 distinct online attractions in one day**, apply a multiplier to tourism income **and** a leisure/attract term. Hotels convert day-trippers → staying visitors up to bed cap. |

## Itinerary force (inc4, design)

- **Day-trip window:** one in-game day = 1 tick for v1 (webconsole). PLACEHOLDER.
- **Sites:** count of **online** (activation gates) buildings whose spec is in the tourism catalogue (inc2 graduates) or existing `tourism > 0` landmarks/leisure.
- **Rule:** `n >= 3` → `itineraryForce = 1 + k * min(n - 2, cap)` with `k`, `cap` PLACEHOLDER exported constants. `n < 3` → force `1`.
- **Why:** one icon is a photo stop; three is a planned day — people stay, spend, tell others (attract).
- **Hotels:** staying visitors = min(implied overnight demand, sum of stay-spec bed stock). Overflow remains day-trippers (transport load, less spend).
- **Conservation:** tourism cash still goes through `computeFlows` as the existing `Tourism` inflow (GR#3). Do not add a second Tourism line.

## Stay stock (inc3)

| id | name | class | w×h | beds (PH) | jobs | MW | water m³/d | capex | opex/tick |
|----|------|-------|-----|-----------|------|----|------------|-------|-----------|
| stay_bb | B&B / guesthouse | S | 1×1 | 8 | 2 | 0.02 | 2 | 4_000 | 25 |
| stay_hotel | City hotel | M | 2×2 | 80 | 25 | 0.4 | 40 | 40_000 | 220 |
| stay_luxury | Luxury hotel | L | 3×2 | 120 | 60 | 0.8 | 80 | 90_000 | 480 |
| stay_resort | Resort campus | XL | 4×4 | 400 | 180 | 2.5 | 250 | 220_000 | 900 |
| stay_caravan | Caravan & camping | M | 3×3 | 60 | 8 | 0.1 | 15 | 12_000 | 40 |

Kind: `leisure`. Palette family **Stay** (inc1 PH).

## Attractions (inc1 PH, inc2 stats)

Class → footprint: S 1×1 · M 2×2 · L 3×3 · XL 4×4 · Mega 5×5. Unlock 99 until graduated.

| # | id | name | class | visitors/d | jobs | MW | water m³/d | capex | opex | tourism |
|---|----|------|-------|------------|------|----|------------|-------|------|---------|
| 1 | tour_greatwall | Great Wall | Mega | 80_000 | 2_000 | 8 | 400 | 800_000 | 4_000 | 220 |
| 2 | tour_colosseum | Colosseum | L | 25_000 | 400 | 1.2 | 80 | 180_000 | 900 | 140 |
| 3 | tour_tajmahal | Taj Mahal | L | 20_000 | 350 | 1.0 | 90 | 200_000 | 850 | 150 |
| 4 | tour_machupicchu | Machu Picchu | XL | 6_000 | 500 | 0.8 | 40 | 250_000 | 700 | 160 |
| 5 | tour_petra | Petra | XL | 5_000 | 280 | 0.6 | 35 | 160_000 | 550 | 130 |
| 6 | tour_giza | Great Pyramid | XL | 15_000 | 400 | 1.5 | 50 | 300_000 | 1_000 | 170 |
| 7 | tour_eiffel | Eiffel Tower | M | 25_000 | 300 | 2.0 | 60 | 120_000 | 800 | 145 |
| 8 | tour_liberty | Statue of Liberty | M | 12_000 | 180 | 0.5 | 30 | 90_000 | 400 | 110 |
| 9 | tour_grandcanyon | Grand Canyon rim | Mega | 18_000 | 600 | 1.0 | 70 | 400_000 | 1_200 | 180 |
| 10 | tour_niagara | Niagara Falls | XL | 22_000 | 450 | 3.0 | 100 | 280_000 | 1_100 | 165 |
| 11 | tour_angkor | Angkor Wat | Mega | 10_000 | 800 | 1.2 | 80 | 350_000 | 900 | 155 |
| 12 | tour_stonehenge | Stonehenge | M | 4_000 | 80 | 0.2 | 10 | 40_000 | 120 | 70 |
| 13 | tour_acropolis | Acropolis | L | 8_000 | 200 | 0.4 | 25 | 100_000 | 350 | 100 |
| 14 | tour_redeemer | Christ the Redeemer | M | 8_000 | 120 | 0.3 | 15 | 70_000 | 220 | 95 |
| 15 | tour_sagrada | Sagrada Família | L | 12_000 | 250 | 0.8 | 40 | 150_000 | 500 | 120 |
| 16 | tour_forbidden | Forbidden City | Mega | 30_000 | 1_200 | 4.0 | 200 | 500_000 | 2_000 | 190 |
| 17 | tour_stpeters | St Peter's / Vatican | L | 18_000 | 400 | 1.5 | 70 | 220_000 | 800 | 135 |
| 18 | tour_alhambra | Alhambra | L | 7_000 | 180 | 0.5 | 40 | 90_000 | 300 | 90 |
| 19 | tour_chichenitza | Chichén Itzá | XL | 6_000 | 220 | 0.4 | 20 | 110_000 | 320 | 100 |
| 20 | tour_fuji | Mount Fuji station | XL | 9_000 | 300 | 0.8 | 35 | 140_000 | 450 | 115 |
| 21 | tour_opera | Sydney Opera House | L | 10_000 | 400 | 2.5 | 80 | 200_000 | 900 | 125 |
| 22 | tour_goldengate | Golden Gate | XL | 14_000 | 250 | 0.6 | 20 | 180_000 | 400 | 110 |
| 23 | tour_louvre | Louvre | XL | 25_000 | 800 | 3.5 | 150 | 280_000 | 1_400 | 160 |
| 24 | tour_santorini | Caldera town | L | 8_000 | 350 | 0.7 | 90 | 95_000 | 380 | 105 |
| 25 | tour_venice | Canal quarter | XL | 15_000 | 600 | 1.2 | 120 | 240_000 | 800 | 140 |
| 26 | tour_neuschwanstein | Neuschwanstein | M | 6_000 | 150 | 0.4 | 25 | 80_000 | 250 | 85 |
| 27 | tour_burj | Supertall observatory | M | 12_000 | 400 | 8.0 | 100 | 400_000 | 2_200 | 150 |
| 28 | tour_iguazu | Iguazú Falls | XL | 7_000 | 280 | 0.5 | 40 | 160_000 | 420 | 120 |
| 29 | tour_banff | Alpine lake park | Mega | 9_000 | 500 | 0.8 | 50 | 220_000 | 600 | 130 |
| 30 | tour_aurora | Aurora lodge | M | 2_000 | 80 | 0.6 | 15 | 50_000 | 180 | 75 |
| 31 | tour_reef | Reef visitor centre | L | 5_000 | 200 | 1.0 | 40 | 90_000 | 350 | 95 |
| 32 | tour_yellowstone | Geyser park | Mega | 10_000 | 700 | 1.5 | 60 | 300_000 | 800 | 145 |
| 33 | tour_serengeti | Safari lodge | XL | 3_000 | 250 | 0.8 | 30 | 120_000 | 400 | 100 |
| 34 | tour_fushimi | Fushimi Inari | M | 9_000 | 120 | 0.2 | 20 | 45_000 | 140 | 80 |
| 35 | tour_prague | Castle quarter | L | 11_000 | 300 | 0.7 | 50 | 110_000 | 360 | 105 |
| 36 | tour_dubrovnik | Walled city | L | 8_000 | 280 | 0.5 | 45 | 100_000 | 330 | 100 |
| 37 | tour_cappadocia | Cappadocia | XL | 5_000 | 220 | 0.4 | 25 | 90_000 | 280 | 90 |
| 38 | tour_moai | Moai field | L | 2_000 | 90 | 0.2 | 10 | 60_000 | 150 | 70 |
| 39 | tour_uluru | Uluru | XL | 3_000 | 150 | 0.3 | 20 | 80_000 | 200 | 85 |
| 40 | tour_tablemountain | Table Mountain | XL | 6_000 | 200 | 0.8 | 25 | 100_000 | 280 | 95 |
| 41 | tour_hallstatt | Lakeside village | M | 4_000 | 140 | 0.3 | 30 | 55_000 | 160 | 75 |
| 42 | tour_antelope | Slot canyon | M | 3_000 | 70 | 0.2 | 8 | 40_000 | 110 | 65 |
| 43 | tour_halong | Ha Long Bay | Mega | 7_000 | 400 | 0.9 | 50 | 180_000 | 500 | 120 |
| 44 | tour_zhangjiajie | Pillar peaks | XL | 5_000 | 260 | 0.6 | 30 | 130_000 | 350 | 110 |
| 45 | tour_matterhorn | Matterhorn | XL | 4_000 | 220 | 0.7 | 25 | 150_000 | 400 | 105 |
| 46 | tour_towerlondon | Tower of London | M | 9_000 | 250 | 0.5 | 35 | 70_000 | 280 | 90 |
| 47 | tour_versailles | Versailles | XL | 15_000 | 600 | 2.0 | 120 | 250_000 | 900 | 140 |
| 48 | tour_montstmichel | Mont-Saint-Michel | M | 6_000 | 180 | 0.4 | 40 | 85_000 | 260 | 95 |
| 49 | tour_giantscauseway | Giant's Causeway | M | 3_000 | 80 | 0.15 | 8 | 35_000 | 90 | 60 |
| 50 | tour_edinburgh | Edinburgh Castle | M | 7_000 | 200 | 0.4 | 30 | 75_000 | 240 | 88 |

Existing magnets (`land_eye`, `lei_themepark`, `land_stadium`, …) **count toward `n`** in inc4. Do not duplicate them in this table.

## Inc1 acceptance (placeholders)

- **AC-1.** Each `tour_*` / `stay_*` id exists in `SPECS` with `placeholder === true`, cost 0, upkeep 0, no jobs/mw/tourism.
- **AC-2.** Each id appears in exactly one `PALETTE` family (`Tourism` or `Stay`).
- **AC-3.** `canEnterSim` / place / stamp / unlock-all all refuse them (existing placeholder tests cover the gate).
- **AC-4.** Palette sorts them last (coming soon). Catalogue tests stay green.

## Out of scope this parent

- Graduating stats (inc2) without Aaron's balance pass.
- Go `engine.tourism` wiring / new code.json edges.
- Trademarked park operators (use generic Theme Park already in leisure).
- Forcing all 50 unique per city (player picks; uniqueness optional later).
