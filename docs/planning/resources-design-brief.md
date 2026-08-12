# Subsurface Resources & Heavy Industry — design brief

**Author:** Aaron (dictated 2026-08-11, transcribed by Bob)
**Status:** Approved design intent, pending SSOT integration (master-plan amendment → generate.js → code.json/BOW modules). BOW cluster: see FEAT items referencing this doc.
**Codename note (GR#22):** the mechanics below reference the reference title's sequel, written here as **'Blue 2'** and only that — never its real name or abbreviation, in this file or anything derived from it.
**North-star check (§ at end):** this system passes all five questions — it is close to a worked example of the fun thesis.

---

## 1. Intent

Mining and heavy industry become a major wealth-generation path — real money, real risk, real investment — built on a finite subsurface resource model the player can survey, exploit, exhaust, and must eventually make good. 'Blue 2'-style resource visibility on the map; Gold-Rush-scale ambition in the economics.

## 2. The resource set

- **Metallic ores:** copper, tin, iron, uranium, rare-earth metals (REM) — plus **fictional resources allowed** where they serve gameplay, blended in at *realistic depths* alongside the real ones.
- **Hydrocarbons & coal:** gas, oil, coal. **Kent's real coalfield is the calibration point** — the East Kent coal measures (Betteshanger/Tilmanstone/Snowdown country, inside our TR extent) were genuinely huge, so **deposit generosity is policy: don't be stingy.**
- **Offshore deposits exist** — some fields sit out to sea (gas/oil especially), giving the sea a second mechanical role beyond the port/fishing/reclamation set (§2.1/§9).
- **Co-location is geological, not random:** gas/oil/coal co-exist where it makes sense (hydrocarbon systems cluster; coal measures imply gas; uranium doesn't sit in the chalk). Depth is a first-class attribute with realistic ranges per resource type.

## 3. Seeding — the deposit shuffle

At world seeding, **before play starts**, a pseudo-random shuffle lays out deposits of varying **size, density, and depth** across the owned-and-unowned tile extent (deposits exist in unowned tiles — a reason to buy them).

- **Deterministic by construction (GR#21):** the shuffle draws from the seeded deterministic RNG; same seed → bit-identical deposit map. It runs at world-gen alongside the Terrain 50 heightmap pipeline.
- **Geology-aware:** the shuffle biases by the terrain/geology model (chalk escarpment vs coal measures vs offshore basins), which is what makes co-location sensible rather than scripted.
- Distribution parameters (counts, size curves, depth bands, co-location rules) live in a **data file** (GR#15), balance-tunable, not code constants.

## 4. Surveying & the map

The map shows resources to the player **as 'Blue 2' does**: a resource overlay rendering deposit type and extent. Slots into F1's existing overlay-cycle architecture (FEAT-031's two-data-layers-per-cell rule). Design question for the BA: whether full deposit knowledge is free or **surveying is itself a progression** (prospecting → survey → proven reserves — which fits the risk ladder in §5 and the projection philosophy: squeezed, never ambushed, but you pay to see underground).

## 5. Extraction — from two men and a dog to Parker

Wealth extraction spans a **scale ladder with risk and investment at every rung**:

- **Artisanal:** "two men and their dog" — trivial capex, small yield, low risk, the early-game snowball seed.
- **Industrial:** Parker-style gold-mining operations — serious capex, serious throughput, real operating risk (yield variance against survey estimates, price exposure, breakdowns).
- **Mining is a large money generator** by design — one of the big pies — but:
- **The ground resource is finite.** Deposits deplete and in some situations **run out**. A mine-dependent district is a §32 deep-mine Detroit test the player seeded themselves. Depletion is visible (surveys, projections) — squeezed, never ambushed.

## 6. International market & the production chain

- An **international market is always available** for raw and refined product — the always-on price floor/ceiling that makes extraction monetisable from day one (consistent with the off-map connections model, §2.2).
- **In-game production facilities** for the full chain: **extraction → refinement → manufacture**. Reference archetypes: **Billingham/Wilton-class integrated chemical works** (reading Aaron's "ICI Wiltshire" as ICI **Wilton**, the cracker complex — flag if a different site was meant), **crackers** as named facility types, **Groton-Pfizer-class pharma/R&D campus** (note: Pfizer's real UK campus was Sandwich, Kent — in-map; a lovely FDI-anchor tie-in), **Parker-class mines**.
- Refinement/manufacture margins vs raw export is the classic value-add decision: sell ore cheap now, or invest in the works and sell refined dear later — a north-star long bet.

## 7. Facilities grow — and everything needs land and a permit

- **All facilities can grow and expand** — increasing run costs, increasing profits. Growth applies to the whole estate: mine, works, lab, factory, airport, port.
- **Land allocation scales with facility size and is gated by a permit.** Permits for any large facility (lab, airport, factory, mine, works — any) are acquired via **experience points, milestones, or purchase** — three routes, so progression, achievement, and money are all currencies for the same gate.
- **Every permitted facility carries a "put back to nature" decommissioning liability from day one** — a real balance-sheet cost accrued at build/permit time, not a surprise at closure. Finite resources + mandatory restoration liability = the full lifecycle priced in: the player who strip-mines Kent owns the bill for the landscape afterwards.

## 8. End-game and mega-facilities

- **Ultimate tier — THORP-reprocessing / CANDU-class nuclear plant:** available **only toward end-game**, gated on a **large workforce of highly trained experts** (the education system's long bet paying off at civilisational scale — you cannot buy this with money alone).
- **SpaceX / CERN / Aldermaston-class** science/defence mega-facilities are also permitted — same shape: expert-workforce-gated, permit-gated, enormous.
- **Container port at Felixstowe scale or larger** is a build option — upgrading §9's port milestone from "port exists" to a genuine deep-sea container terminal tier.
- These extend the existing mega-projects supplement in buildings.json rather than replacing it.

## 9. Existing-spec integration points

| This brief | Existing spec |
|---|---|
| Deposit shuffle at world-gen | engine.world terrain/geology pipeline, seeded RNG (GR#21) |
| Resource overlay | F1 overlay cycle, FEAT-031 two-layers rule |
| Deep-mine depletion | §32 deep-mine closure ("scripted-by-you Detroit test") — now generalised to every finite deposit |
| Mining/fuel/chemicals/mega-project buildings | buildings.json supplements (FEAT-010) — extend, don't fork |
| International market | §2.2 off-map connections; JIT import/export (§8) |
| Port tiers | §9 port milestone |
| Expert-workforce gates | education/employment systems (§5, education long-bet) |
| Decommission liability | engine.finance balance sheet; land value/decay on restoration |

## 10. North-star check (the five questions)

1. **Which conflicting demand does it sharpen?** Capex-hungry extraction vs everything else the pie funds; refine-later vs sell-raw-now.
2. **What does the player give up?** Land, permits, capital, and a clean balance sheet (the decommission liability).
3. **Does the consequence stick?** Depleted deposits never refill; the restoration bill always comes due; a mine town is a self-seeded Detroit test.
4. **Does it snowball?** Two-men-and-a-dog → Parker → integrated works → THORP. The expert workforce for end-game plants is the education snowball's summit.
5. **Can a long bet be made?** The whole system is long bets — survey speculation, tile purchase on unproven ground, refinement capacity ahead of demand, the university-to-reprocessing-plant pipeline.
