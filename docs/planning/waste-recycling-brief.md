# Garbage Collection & Total Recycling Engine — Design Brief

**Goal (Aaron, 2026-08-27):** a full waste loop — citizens and industry generate refuse; it must be **collected** (rounds/rota), **processed** (landfill / energy-from-waste / recycling / compost), and driven toward a **total recycling** endgame (near-100% diversion) — with uncollected waste hurting wellbeing/health and recovered materials/energy feeding back economically.

Consolidates the scattered BOW items: **MOD-039** `engine.refuse` (refuse rounds + waste-health loop, spec §25/§31), **FEAT-141** (household refuse in tonnes, food→waste), **FEAT-1972079864** (webconsole waste+recycling rota), and the missing waste **building specs**. All are currently open / M4 / unbuilt.

---

## 1. The loop (end to end)
1. **Generation** — households (by population/consumption) and industry/commerce/offices produce refuse per tick, measured in **tonnes** (ties to FEAT-141's tonnes food→waste chain), split by **stream**: residual, dry-recyclables (paper/plastic/glass/metal), organic/food, and hazardous/bulky.
2. **Collection** — refuse **depots** run **rounds** (a rota) covering nearby buildings, like a service-coverage radius. Coverage = fraction of generated tonnage actually collected. Uncollected waste **accumulates** and drives the **waste-health penalty** (wellbeing + health/disease risk — the MOD-039 waste-health loop).
3. **Processing** (where collected tonnage goes, player-configurable mix):
   - **Landfill** — cheap, finite capacity, a wellbeing/environment penalty; the thing "total recycling" minimises.
   - **Energy-from-Waste incinerator** — burns residual, **produces power** (feeds the grid — ties to the MW/GW surplus + MOD-049 export), some emissions penalty.
   - **MRF (recycling centre)** — sorts dry-recyclables, **recovers materials** at a recovery rate; recovered material is **sold** (revenue) or **fed back** as an industrial input (closes the loop).
   - **Composting** — organic/food → compost (revenue / parks/farms input; the §31 compost loop).
4. **Total recycling engine** — the endgame dial: as the player builds MRF + compost + EfW capacity and adopts policies (the existing `recycling` policy + new ones), the **diversion rate** rises toward ~100% (nothing to landfill). A visible **diversion %** KPI. "Total recycling" = diversion→100%, landfill→0, materials/energy fully recovered.

---

## 2. Model (webconsole-first)
- **Waste stats** (like `serviceCoverageOf`): `wasteGeneratedOf(s)` (tonnes/tick by stream from population + industry), `collectionCoverageOf(s)` (depot capacity vs generated), `processingMixOf(s)` (landfill/EfW/MRF/compost shares from built capacity), `diversionRateOf(s)` (1 − landfill share).
- **Capacity meters** reuse the demand-meter idiom (BUG-425-aware colours): collection coverage, and per-processor capacity vs throughput.
- **Economic hooks** (conservation-safe, via the flows): collection **OPEX** (rounds cost ∝ tonnage/coverage), landfill **tipping cost**, EfW **power revenue** (inflow), MRF **material revenue** (inflow) + recovered-input value, compost revenue.
- **Wellbeing/health hook**: uncollected tonnage → a waste-health penalty term in `wellbeingOf` (new part), and a disease/health pressure at high accumulation.
- **Determinism (GR#21):** all pure functions of state; no Date/random; tonnage/rounds derived from (tick, buildings, population).

## 3. Building specs to add (catalogue)
`waste_depot` (Refuse Depot — collection rounds), `waste_landfill` (Landfill — finite), `waste_incinerator` (Energy-from-Waste — power out), `waste_recycling` (MRF Recycling Centre — material recovery), `waste_compost` (Composting Site — organic loop). Group: **Water & Waste**. Placeholder-balance stats (capacity, cost, upkeep, power/material yields) pending Aaron sign-off. (These also feed the placeholder-catalogue FEAT-1972079877.)

## 4. UI (FEAT-1972079864 rota)
A **waste/recycling rota** view: collection coverage, the processing mix + diversion %, per-depot rounds, and the recovered power/materials. Surfaced like the water panel (capacity + demand + headroom + the new diversion KPI).

---

## 5. Increments (webconsole-first; Go `engine.refuse` MOD-039 is the canonical long-term home)
- **inc1 — Generation + collection + waste-health.** `wasteGeneratedOf`/`collectionCoverageOf`, depot spec + rounds coverage, uncollected→wellbeing/health penalty, collection OPEX. The core "garbage collection" Aaron asked for.
- **inc2 — Processing + recycling engine.** Landfill/EfW/MRF/compost specs + processing mix, diversion %, power/material/compost revenue + recovered-input feedback, the total-recycling dial + KPI.
- **inc3 — Rota UI + policies.** The FEAT-1972079864 rota view; recycling/separate-collection policies that raise diversion; balance pass.

---

## 6. Open questions for Aaron
1. **Priority vs Baseline One:** MOD-039 is M4 (post-B1). Pull garbage collection (inc1) forward into the webconsole now, or hold for the milestone?
2. **Streams granularity:** 4 streams (residual/dry/organic/hazardous) or start with just residual-vs-recyclable?
3. **"Total recycling" reward:** is 100% diversion a scored achievement / unlock, or just a KPI?
4. **Material feedback:** does recovered material become a real industrial INPUT (closes the loop, more sim), or just revenue for inc1/2?
5. **Scope home:** webconsole prototype first (as with water/finance), fold into Go `engine.refuse` later?

## 7. Risks
| Risk | Mitigation |
|---|---|
| Waste flows break conservation | Route all costs/revenues through the flows (like FEAT-896/MOD-049); conservation test. |
| Non-determinism in rounds/generation | Pure fns of (tick, buildings, pop); determinism test. |
| Balance (tipping fees, recovery rates, health penalty) | Placeholder + directional tests + Aaron row-by-row sign-off (balance-number regime). |
| Scope creep (full materials economy) | inc1 = collection + health only; recycling/feedback deferred to inc2/inc3. |

---

*Design brief 2026-08-27. Webconsole-first. Consolidates MOD-039 / FEAT-141 / FEAT-1972079864 + waste specs. Awaiting Aaron's §6 answers — chiefly whether to pull inc1 forward now.*
