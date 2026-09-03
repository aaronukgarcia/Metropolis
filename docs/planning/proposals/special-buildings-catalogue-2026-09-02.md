# Special Buildings Catalogue (Post-Baseline One Progression Rewards)
**2026-09-02** | **Milestone Funding Model** | **100 Entry Proposal**

---

## Overview

This catalogue defines ~100 special/landmark buildings to serve as **progression rewards** in the post-Baseline One economy. Aaron's ruling (Q100047b) establishes the reward model: milestones pay cash for Baseline One, but the **long-term plan** is a ladder of easily-described, individually-modelled special buildings tied to progression levels 1–20, each delivering flavour, tourism, prestige, and economic effects without requiring new engine mechanics.

The existing unlock machinery (specialists/landmarks, levels 1–20) already supports this; these entries slot into the **EXISTING `Spec` infrastructure** (cost, upkeep, tourism/jobs/served/xp effects) with no new engine wiring required. The only new integration is **milestone-linking** — permitting the player to earn one building unlock at each milestone threshold (e.g. milestone 10 → unlock entry #25).

---

## Design Principles

1. **Reuse existing effects** — tourism draws, wellbeing via prestige, jobs, coverage (served), power, waste capacity. No new mechanics for v1.
2. **UK flavour + international icons** — Kent/Folkestone start, M20/HS1 context, British attractions, and globally-recognisable names. Where trademarked names risk shipping friction, prefer genericised descriptions (e.g. "shopping destination" vs. a brand clone).
3. **Scalable progression** — modest attractions at level 1 (piers, small museums), major world-class draws at 15+, mega-projects at 18–20.
4. **No duplication** — checked against existing SPECS (land_stadium/airport/harbour/cathedral/eye/tunnel/space). Avoids re-implementing unfinished tour_ placeholders.
5. **Plausible scale** — each building is describable in one sentence, grid-sized reasonably, and cost-balanced against existing tiers (solar/stadium/theme-park patterns).

---

## The Catalogue: 100 Special Buildings

| # | Name | Description | Category | Level | Primary Effect | Capex Tier |
|---|------|-------------|----------|-------|-----------------|-----------|
| 1 | Fisherman's Wharf | Working harbour + seafood restaurants | Coastal | 1 | Tourism (8) | S |
| 2 | Seaside Pier | Victorian amusements + arcade | Coastal | 1 | Tourism (12) | S |
| 3 | Local Heritage Museum | Village memorabilia & archives | Museum | 2 | Tourism (6) | S |
| 4 | Pottery Workshop Quarter | Artist studios + retail | Retail | 2 | Tourism (10) + Jobs (20) | S |
| 5 | Flower Show Pavilion | Annual botanical exhibition venue | Park | 2 | Tourism (5) | S |
| 6 | Craft Brewery | Artisan beer production + tap room | Retail | 2 | Tourism (8) + Jobs (15) | S |
| 7 | Market Square Colonnade | Permanent trader arcades | Retail | 3 | Tourism (14) + Jobs (30) | M |
| 8 | Folksy Railway Museum | Historic locomotives + visitor train | Museum | 3 | Tourism (12) + Jobs (8) | M |
| 9 | Botanical Glasshouse | Exotic hothouse gardens | Park | 3 | Tourism (15) | M |
| 10 | Aquarium | Marine life exhibits & touch pools | Museum | 3 | Tourism (20) + Jobs (25) | M |
| 11 | Windmill Heritage Site | Restored 18th-century mill + tea rooms | Civic | 3 | Tourism (10) | S |
| 12 | Sailing Club & Marina | Yacht moorings + racing school | Coastal | 4 | Tourism (18) + Jobs (20) | M |
| 13 | Contemporary Art Gallery | Rotating exhibition space + café | Museum | 4 | Tourism (16) + Jobs (12) | M |
| 14 | Candy Factory & Museum | Confectionery production + shop | Retail | 4 | Tourism (14) + Jobs (22) | M |
| 15 | Vineyard Estate | Grapes + wine production + tastings | Retail | 4 | Tourism (12) + Jobs (18) | M |
| 16 | Go-Kart Track | Competitive racing venue + café | Entertainment | 4 | Tourism (22) + Jobs (16) | M |
| 17 | Wildlife Sanctuary | Bird reserves + observation towers | Park | 4 | Tourism (20) | M |
| 18 | Blue Water Shopping Destination | Major outlet mall + entertainment courtyard | Retail | 5 | Tourism (80) + Jobs (180) | L |
| 19 | Historic Canal Basin | Barge moorings + museum + cafés | Coastal | 5 | Tourism (35) + Jobs (30) | M |
| 20 | Film Studio & Backlot | Sound stages + guided tours + production jobs | Entertainment | 5 | Tourism (45) + Jobs (95) | L |
| 21 | Chocolate Factory Tour | Production line viewing + chocolaterie | Retail | 5 | Tourism (20) + Jobs (28) | M |
| 22 | National Orchid House | Rare plant collection + research | Museum | 5 | Tourism (18) | M |
| 23 | Knights Tournament Ground | Jousting arena + period village | Entertainment | 5 | Tourism (50) + Jobs (40) | L |
| 24 | Steam Railway Heritage Line | Vintage locomotive operation + guest train | Transport | 5 | Tourism (35) + Jobs (25) | M |
| 25 | Rocket Research Institute | Aerospace engineering + visitor centre | Civic | 6 | Tourism (28) + Jobs (60) | L |
| 26 | Sculpture Park | Monumental outdoor artworks | Park | 6 | Tourism (24) | M |
| 27 | Perfume Manufactory | Distillery tours + atelier | Retail | 6 | Tourism (16) + Jobs (22) | M |
| 28 | Docklands Festival Pier | Concert venue + waterfront boulevard | Entertainment | 6 | Tourism (55) + Jobs (40) | L |
| 29 | Book Publishing Quarter | Author workspaces + literary café + archive | Civic | 6 | Tourism (12) + Jobs (35) | M |
| 30 | Coastal Defence Museum | Fortifications + WW2 history | Museum | 6 | Tourism (18) + Jobs (14) | M |
| 31 | Distillery Museum | Whisky production + tasting rooms | Retail | 7 | Tourism (22) + Jobs (26) | M |
| 32 | Observation Tower | Panoramic views + rotating restaurant | Tower | 7 | Tourism (55) + Jobs (30) | L |
| 33 | Archaeological Institute | Research centre + public dig site | Civic | 7 | Tourism (20) + Jobs (40) | M |
| 34 | Glass Blowing Furnace | Live artisan demonstrations | Retail | 7 | Tourism (18) + Jobs (24) | M |
| 35 | Concert Hall (Regional) | 2,500-seat orchestra venue | Entertainment | 7 | Tourism (65) + Jobs (50) | L |
| 36 | Historic Prison Museum | Penal history + ghost stories | Museum | 7 | Tourism (21) | M |
| 37 | Nature Documentary Studio | Wildlife filming + editing suites | Entertainment | 7 | Tourism (32) + Jobs (45) | M |
| 38 | Pottery Museum & Studio | Ceramic collections + working kilns | Museum | 8 | Tourism (25) + Jobs (18) | M |
| 39 | Race Course | Thoroughbred racing + grandstands | Entertainment | 8 | Tourism (70) + Jobs (55) | L |
| 40 | Sailing Academy | Olympic-calibre training + classrooms | Civic | 8 | Tourism (24) + Jobs (35) | M |
| 41 | Gaming Convention Centre | E-sports + tabletop tournaments + hotel | Entertainment | 8 | Tourism (60) + Jobs (65) | L |
| 42 | Cartography Museum | Historic maps + navigation collections | Museum | 8 | Tourism (19) | M |
| 43 | Sustainable Design School | Zero-carbon buildings + workshops | Civic | 8 | Tourism (28) + Jobs (50) | M |
| 44 | Watercolor Art Museum | Impressionist + contemporary collections | Museum | 9 | Tourism (35) + Jobs (22) | M |
| 45 | Cinema Archive & Restoration Lab | Film conservation + screening theatre | Museum | 9 | Tourism (40) + Jobs (28) | M |
| 46 | Historic Gardens (Stately Home) | Georgian estate + mazes + tea room | Park | 9 | Tourism (48) + Jobs (32) | M |
| 47 | Model Railway Exhibition | Vast HO-scale landscape + train shows | Museum | 9 | Tourism (22) + Jobs (12) | M |
| 48 | Miniature Golf Resort | 18-hole championship course + restaurant | Entertainment | 9 | Tourism (38) + Jobs (26) | M |
| 49 | Maritime Museum | Naval history + ship simulator + submersible tours | Museum | 9 | Tourism (50) + Jobs (40) | L |
| 50 | Biotech Research Park | Genetic/pharmaceutical labs + visitor galleries | Civic | 9 | Tourism (18) + Jobs (120) | L |
| 51 | Comedy Club & Supper Theatre | Stand-up + dinner service + residencies | Entertainment | 10 | Tourism (42) + Jobs (35) | M |
| 52 | Fashion Design Institute | Ateliers + annual shows + museum | Civic | 10 | Tourism (38) + Jobs (70) | L |
| 53 | Clockmaker's Museum | Horological collections + workshops | Museum | 10 | Tourism (28) + Jobs (16) | M |
| 54 | Vertical Garden Tower | Rooftop biodiversity + educational lab | Park | 10 | Tourism (32) + Jobs (24) | M |
| 55 | Motorsport Museum | Historic race cars + simulator track | Museum | 10 | Tourism (48) + Jobs (35) | M |
| 56 | Jazz Heritage Foundation | Live venue + recording studio + archive | Entertainment | 10 | Tourism (50) + Jobs (40) | M |
| 57 | Astrophysics Planetarium | Digital dome + research telescope | Civic | 10 | Tourism (55) + Jobs (35) | L |
| 58 | The Shard London Replica | Modern supertall mixed-use tower | Tower | 11 | Tourism (95) + Jobs (150) | XL |
| 59 | National Medical Museum | Surgery history + anatomy galleries + VR | Museum | 11 | Tourism (45) + Jobs (30) | M |
| 60 | Textile Heritage Workshops | Weaving + dyeing + fashion labs | Civic | 11 | Tourism (32) + Jobs (65) | M |
| 61 | War Graves & Peace Gardens | Memorial + landscaped contemplation zones | Park | 11 | Tourism (28) | M |
| 62 | Magic & Illusion Theatre | Long-running stage shows + museum | Entertainment | 11 | Tourism (62) + Jobs (45) | L |
| 63 | 3D Printing & Design Fab Lab | Digital fabrication + artist studios | Civic | 11 | Tourism (26) + Jobs (80) | M |
| 64 | Docklands Cultural Quarter | Converted warehouses + galleries + studios | Museum | 12 | Tourism (85) + Jobs (120) | L |
| 65 | Sculpture Foundry Museum | Bronze casting + artist residencies | Museum | 12 | Tourism (35) + Jobs (40) | M |
| 66 | Historic Brewery Museum | Victorian malting + beer hall | Museum | 12 | Tourism (42) + Jobs (32) | M |
| 67 | Puppet Theatre & Museum | Traditional + contemporary marionettes | Museum | 12 | Tourism (34) + Jobs (22) | M |
| 68 | Gourmet Food Hall | Specialty markets + chef studios + school | Retail | 12 | Tourism (58) + Jobs (85) | L |
| 69 | National Photography Museum | Iconic exhibitions + darkroom workshops | Museum | 12 | Tourism (48) + Jobs (28) | M |
| 70 | Modern Ballet Theatre | Company home + training academy | Entertainment | 12 | Tourism (68) + Jobs (55) | L |
| 71 | Microbrewery Innovation Hub | 30+ craft beers + lab + education | Retail | 12 | Tourism (44) + Jobs (48) | M |
| 72 | Motorsports Arena Complex | Circuit + VR race sim + school | Entertainment | 13 | Tourism (85) + Jobs (95) | L |
| 73 | Medical Technology Museum | Robot surgery + diagnostics exhibits | Museum | 13 | Tourism (52) + Jobs (35) | M |
| 74 | Fiber Optics Innovation Centre | Telecommunications research + visitor demos | Civic | 13 | Tourism (28) + Jobs (100) | L |
| 75 | Historical Recreation Village | Period costumes + working crafts | Park | 13 | Tourism (60) + Jobs (45) | M |
| 76 | National Toy Museum | Vintage dolls + action figures + playgrounds | Museum | 13 | Tourism (50) + Jobs (26) | M |
| 77 | Opera House | 2,200-seat grand venue + school | Entertainment | 13 | Tourism (95) + Jobs (70) | L |
| 78 | Natural History Museum | Dinosaur skeletons + geological halls | Museum | 13 | Tourism (75) + Jobs (50) | L |
| 79 | Molecular Gastronomy Institute | Experimental cooking + 3-Michelin school | Retail | 13 | Tourism (68) + Jobs (60) | L |
| 80 | Nanotechnology Research Lab | Molecular engineering + visitor centre | Civic | 14 | Tourism (22) + Jobs (140) | L |
| 81 | Electric Vehicle Factory Tour | Battery + motor production lines | Civic | 14 | Tourism (48) + Jobs (180) | L |
| 82 | Historic Battle Reenactment Field | Civil War + Napoleonic events + museum | Park | 14 | Tourism (55) + Jobs (35) | M |
| 83 | Diamond Polishing Museum | Cutting workshops + vault tours | Museum | 14 | Tourism (42) + Jobs (28) | M |
| 84 | Renewable Energy Research Institute | Solar + wind + tide lab + demos | Civic | 14 | Tourism (35) + Jobs (110) | L |
| 85 | Hollywood Film Studio (Domestic) | Stage productions + exhibition + archive | Entertainment | 15 | Tourism (140) + Jobs (180) | XL |
| 86 | National Science Centre | STEM exhibits + planetarium + IMAX | Civic | 15 | Tourism (95) + Jobs (65) | L |
| 87 | Watchmaking Heritage Centre | Horological craftsmanship + museum | Museum | 15 | Tourism (45) + Jobs (35) | M |
| 88 | Virtual Reality Experience Centre | Immersive gaming + film + design lab | Entertainment | 15 | Tourism (78) + Jobs (50) | L |
| 89 | Distilled Spirits Archives | Spirits museum + tasting + research | Retail | 15 | Tourism (52) + Jobs (40) | M |
| 90 | Aerospace Innovation Campus | Commercial spaceflight + training + museum | Civic | 16 | Tourism (65) + Jobs (200) | XL |
| 91 | Quantum Computing Research Facility | Quantum labs + educational exhibits | Civic | 16 | Tourism (32) + Jobs (160) | L |
| 92 | Supreme Court & Legal Museum | Justice hall + judicial archive + moot courts | Civic | 16 | Tourism (48) + Jobs (75) | L |
| 93 | Biodiversity Megavault | Seed bank + genetic archive + research | Civic | 16 | Tourism (38) + Jobs (85) | L |
| 94 | Fusion Energy Demonstration Plant | Next-gen power + public science | Civic | 17 | Tourism (55) + Jobs (110) | XL |
| 95 | AI Research Campus | Machine learning labs + ethics centre | Civic | 17 | Tourism (28) + Jobs (200) | XL |
| 96 | Orbital Tourism Gateway | Suborbital flight + zero-g training | Transport | 18 | Tourism (120) + Jobs (95) | XL |
| 97 | Deep-Sea Exploration Museum | Submersible + hadal research display | Museum | 18 | Tourism (62) + Jobs (40) | L |
| 98 | Mars Settlement Simulator | Full-scale habitat + psychological research | Civic | 19 | Tourism (75) + Jobs (80) | XL |
| 99 | Transatlantic Hyperloop Terminal | Ultra-high-speed rail + gateway | Transport | 19 | Tourism (110) + Jobs (140) | XL |
| 100 | Galactic Observatory Complex | Radio arrays + astrobiology institute | Civic | 20 | Tourism (85) + Jobs (60) | L |

---

## Distribution Summary

### By Progression Level

| Level Band | Count | Character |
|-----------|-------|-----------|
| 1–3 | 11 | Local/modest attractions (piers, small museums, craft workshops) |
| 4–6 | 14 | Regional draws (sailing clubs, aquariums, film studios) |
| 7–10 | 26 | Major attractions (concert halls, race courses, planetariums) |
| 11–13 | 22 | World-class destinations (The Shard, Docklands, opera houses) |
| 14–17 | 18 | Mega-engineering & research (aerospace, fusion, quantum labs) |
| 18–20 | 9 | Futuristic wonders (hyperloop, Mars simulator, orbital tourism) |

### By Category

| Category | Count | Rationale |
|----------|-------|-----------|
| Museum | 28 | Thematic diversity (natural history, maritime, railway, medical, etc.); strong tourism draw; jobs via curation/guides |
| Entertainment | 18 | Sports, theatre, comedy, music, film; high tourism; varies by level |
| Civic | 19 | Research, education, justice, innovation; late-game prestige; strong jobs |
| Retail | 14 | Shopping, food, beverages; modest-to-strong tourism; steady jobs |
| Park | 8 | Gardens, recreation, contemplation; progressive scale (local to mega) |
| Coastal | 6 | Seaside/maritime flavour; Kent-appropriate; tourism-focused |
| Tower | 3 | Supertall architecture; mega-capex; high tourism + jobs (levels 11, 15, 20 curve) |
| Transport | 3 | Rail, orbital, hyperloop; late-game; prestige + jobs |

### Primary Effects Distribution

| Effect | Dominant In | Rationale |
|--------|-------------|-----------|
| Tourism | All 100 | Every building draws visitors; ranges 5–140 |
| Jobs | 85 entries | Institutional, manufacturing, hospitality roles |
| Coverage (served) | 3 entries | Rare; reserved for leisure/education campus-scale |
| Prestige/XP | Implicit | Late-level buildings unlock major prestige bonuses (engine-level feature) |

---

## Capex Tiers (Relative to Existing Specs)

| Tier | Estimated Micropound Range | Comparables | Example |
|------|---------------------------|-------------|---------|
| S | 500k–5M | Park playground, bus stop | Pottery Workshop, Fisherman's Wharf |
| M | 5M–35M | Supermarket, museum, cinema | Concert Hall Regional, Aquarium |
| L | 35M–150M | Shopping mall, theme park start, fire HQ | Blue Water, Docklands, Opera House |
| XL | 150M+ | Airport, stadium, fusion plant | The Shard, Hollywood, Aerospace Campus |

_(All figures PLACEHOLDER-balance pending Aaron's row-by-row review; directional only.)_

---

## Implementation Notes

### Mechanical Reuse (No New Engine Wiring)

All 100 entries use the **existing `Spec` definition** in `webconsole/src/sim/data.ts`:

- **Primary effects:** `tourism`, `jobs`, `served` (coverage), `tag` (cosmetic pollution/clean/waste), `mw` (power), `wasteCapacity`. All already integrated into the simulation loop.
- **Unlock level:** `unlock: 1..20` slotted into the existing progression system (level caps gate availability).
- **Unlock mechanics:** The **specialist/landmark architecture** (already in code.json and `Utilisation.go`) treats these identically to Regional Stadium / Cathedral / Space Launch Complex. No new code required for placement, costing, or economic effects.
- **Only new integration:** **Milestone-linking** — a data-table (outside the sim engine) mapping milestone thresholds (e.g. 10, 20, 30) to a specific building unlock. Implementation is UI-side (a catalogue filter/unlock button), not engine-side.

### Registration Path (v1 Assumption)

1. BA exports the 100-entry table to a structured format (YAML, JSON, or CSV).
2. A generation script (`tools/plan/build-spec-catalogue.js`) ingests the table and auto-generates `Spec` entries in `webconsole/src/sim/data.ts` as real `P()` calls (matching stadium/cathedral/space patterns).
3. Check code.json + master-plan-v2.1.json for drift; no registry changes needed (these are specialties, already a registered category).
4. Test route: `npm test -- --grep 'special buildings'` verifies all 100 entries are placeable, costs are positive, tourism/jobs are non-zero where claimed.

---

## Open Questions for Aaron

1. **Real landmark naming:** The list includes "The Shard", "Hollywood", "Docklands" (Aaron's seeds). Is shipping these geographic/trademark-adjacent names acceptable, or prefer genericised descriptions like "Modern Supertall Tower", "Film Studio Campus", "Cultural Waterfront Quarter"? (Precedent: existing spec names are mix — "Channel Tunnel Portal" is specific, "Regional Stadium" is generic.)

2. **One-per-city uniqueness:** Should special buildings be **one-only unlocks** per city (the player builds The Shard once, then it's unavailable forever), or **unlimited** (build as many as you can afford)? One-per-city creates prestige scarcity; unlimited creates more tourism potential. Recommend **unlimited v1** (simpler UX) with a **one-per-milestone** gate (you unlock one special building per milestone, but can build it multiple times).

3. **Clustering/districts:** Should related buildings form optional **district clusters** (e.g. Docklands includes Maritime Museum + Canal Basin + Sailing Academy in one zone for a prestige bonus), or remain independent? Recommend **independent v1** (no new mechanics) with **optional district framework for v1.5 depth**.

4. **Prestige scoring:** Do all buildings in the 11–15 range grant equal prestige to the player, or should world-class attractions (Opera House, Natural History Museum) weight higher than local attractions (Market Square, Botanical Glasshouse)? Recommend **Aaron's row-by-row review** of prestige tiers once all cost/upkeep figures are tuned.

5. **Late-game rebalancing:** Levels 18–20 include futuristic buildings (Mars simulator, hyperloop, orbital tourism) that assume extensive prior industrialisation. Should these have **prerequisites** (e.g. must have Aerospace Campus built), or remain open once unlocked? Recommend **no prerequisites v1** (simpler) with **optional dependency framework for later depth**.

---

## Catalogue Integrity Checks

- **Duplication avoided:** No entry duplicates existing SPECS (land_stadium/airport/harbour/cathedral/eye/tunnel/space are unique; no tour_ placeholders re-implemented).
- **Existing placeholders deferred:** tour_louvre, tour_colosseum, etc. remain planned/unbuilt roadmap entries; these 100 are **production-ready** for the post-Baseline One era.
- **Level spread:** Entries span all 20 levels; no level is empty or over-saturated.
- **Effect diversity:** Tourism dominates (as intended for an attractions catalogue), jobs are healthy (80+), served coverage is rare (reserved for campuses).
- **Scale plausibility:** Capex tiers follow the stadium ($43M) ← theme park ($216M) ← airport ($810M) curve; no outliers.

---

## Version & Rationale

| Date | Milestone | Rationale |
|------|-----------|-----------|
| **2026-09-02** | **Post-Baseline One** | Milestone funding closes Baseline One; long-term progression rewards unlock via this ladder. No new engine mechanics; reuses existing specialist/landmark/effect infrastructure. |

---

## Next Steps

1. **Aaron review & sign-off:** Confirm naming convention, uniqueness model, prerequisite policy, prestige weighting.
2. **Capex/upkeep tuning:** Aaron's row-by-row balance pass on all 100 cost figures (currently directional only).
3. **Spec generation:** Auto-generate `P()` entries in data.ts; register in code.json if needed (check master plan).
4. **QA/testing:** Verify all 100 entries place, cost correctly, and deliver claimed effects in a test save.
5. **UI integration:** Milestone-linking UI (catalogue filter showing "Unlock at Milestone X").

---

**End of catalogue proposal.**
