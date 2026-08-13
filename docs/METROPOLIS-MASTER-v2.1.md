# METROPOLIS — Master Design Document v2.0
**Inception Project 22 · August 2026 · Consolidated edition — supersedes GDD v1.0, Systems Addenda v1.1–v1.4, Catalogue v1 + Supplements 1–3, UI Specification v1, and M0 Engineering Plan v1, all of which are incorporated in full below with their chapter numbering preserved (Book-of-Work `spec_ref`s remain valid).**

---

# PART I — INTRODUCTION

## I.1 What this game is

Metropolis is a city-building and society-simulation game for the Windows terminal — Windows Terminal as the primary host, classic cmd as a degraded fallback. There are no graphics. There are box-drawing characters, Braille-cell charts, colour heatmaps, and columns of truck glyphs queuing at a motorway junction. What the game lacks in pixels it repays in honesty: every number on screen is produced by a real model, every queue is a real queue, and every person in the city — up to one hundred million of them — is a persistent individual with a personality, a taste in Saturday afternoons, and a life shaped by the schools you funded or didn't twenty game-years ago.

The player starts with money, a two-kilometre-square tile of real Folkestone topography wiped of everything man-made except the M20 and one junction, and zero inhabitants. The objective is Centopolis: one hundred million citizens, reached across game-centuries of migration-driven growth over an expanding map of real East Kent. The failure states are insolvency and the Detroit spiral — a mass-emigration death whose mechanism is the same attractiveness engine that drives growth, run in reverse.

'Blue' is the explicit feature benchmark — its buildings, services, expansions, milestones and policies are all mapped into this design — but Metropolis is built to succeed precisely where 'Blue' is criticised: simulation depth over rendering, routing that models congestion honestly, policies that are real instruments rather than toggles, and performance architecture that does not collapse at a million agents.

## I.2 Design pillars

1. **Mechanics-first.** No decorative systems. If it's on screen, it's modelled.
2. **Individuals, not sheep.** Persistent citizens with personality vectors and life histories, at any population, via adaptive fidelity.
3. **Squeezed, never ambushed.** Projections make almost every crisis visible years out; difficulty is competing demands, not surprises.
4. **Real ground, real constraints.** OS Terrain 50 heightmaps, the chalk aquifer, the escarpment, the sea, Sellindge and Dungeness, the East Kent coalfield, Pfizer-at-Sandwich and Bluewater-in-a-quarry as precedents.
5. **High realism, playable pace.** Numbers bend; structures never do.
6. **Both directions on every dial.** Growth and collapse share mechanisms.
7. **The terminal as a strength.** A learnable key grammar and an instrument-panel UI that lets a practised player run the city at the speed of thought.

**Where the entertainment comes from.** These pillars describe how the sim behaves; `docs/planning/design-north-star.md` (Aaron, 2026-08-11) extends Pillar 3 with the *entertainment thesis* — the game is the juggle of conflicting requirements, and every feature, criteria set, and balance pass should be able to answer its five-question test (which conflicting demand does this sharpen; what does the player give up; does the consequence stick; does it snowball; can a long bet be made on it). That document stays at 10,000 feet by design — it is the test other documents are held to, not a spec to decompose. The same reference is threaded into `master-plan-v2.1.json`'s `designNorthStar` field so it flows through `generate.js` into `code.json` for tooling/BA consumption.

## I.3 Fixed decisions (the constitution)

Go; single binary v1 with engine-as-a-service behind a versioned protocol (gRPC-ready, dormant); bit-exact determinism as a CI-gated invariant; JSON everywhere (saves as sharded gzipped NDJSON); 10 m cells; 'Blue' speed model (pause/1×/2×/4×, debug 8×; one day-night cycle = one calendar month; ~8 real minutes per month at 1×; winning run ≈ 80–150 hours); Option B citizen model (persistent individuals, adaptive fidelity, no named-person culls — month-resolution ages and per-person mortality hazards); horizontal expansion over real Kent to ~60×60 km with era/milestone progression modelled on 'Blue's pattern; static import prices v1 behind a market interface; keyboard-first UI with optional mouse; debug mode as a runtime feature switch; full harness strategy so the front end is built against a stub engine before the simulation exists; Book of Work in local MariaDB; monorepo Git with trunk-based development; Claude Code as the builder under a written working agreement.

## I.4 Document map

Part II gives the high-level design — the architecture of the game as systems and the architecture of the software as components, each in a few pages. Part III is the complete low-level design: game systems chapters §1–§55 (numbering preserved from the source documents), then the UI specification, then the engineering and runtime plan. Part IV holds the building catalogue appendix (with supplements 1–3 embedded at the ends of their originating chapter sets in Part III). Part V is the cross-check: coverage verification against every requirement raised in planning, and the open items that remain for M0.

---

# PART II — HIGH-LEVEL DESIGN

## II.1 The world

A 2×2 km start tile (200×200 cells at 10 m) spanning Folkestone West to the Seabrook shore: escarpment north, M20 corridor with one junction, buildable shelf, sea south. Terrain from OS Terrain 50; geology layered beneath (chalk, clay pockets, gravels, deep coal east); all man-made features wiped except the motorway. Off-map connections are purchased: grid power at Sellindge, gas pipeline, bulk water, external rail, coach, and — by milestone — port, ferry, airport, and eventually the Channel Tunnel slot. Expansion is horizontal and real: purchasable 2×2 km tiles across ~60×60 km of East Kent, only owned tiles fully simulated. Late-tier density unlocks make 100 M arithmetically honest on real ground. (LLD §2, §32.)

## II.2 Time

Two-layer clock copied from 'Blue' and tuned: the displayed day-night cycle advances the calendar one month; thirty daily logistics ticks run inside each cycle; a monthly tick resolves economics, demographics and finance in fixed deterministic phases. Speeds pause/1/2/3 map to 0/1×/2×/4× (8× in debug); the master pacing knob is seconds-per-month at 1×, default 480. Seasonality rides the month index: winter power, summer water, harvests, September school intake, tourist summer. (LLD §3, §9.)

## II.3 People

Every citizen is a ~250-byte persistent record: month-resolution birth date, household and relationships, home/work/school, an eight-axis personality vector, education history that drifts personality, derived leisure-taste weights, health bands, wealth, employment. Fidelity is adaptive: HOT citizens (viewport, followed, and a rotating sample) get full daily simulation; everyone else advances by monthly batch mathematics whose parameters are measured from the hot sample; inspection deterministically writes a binding recent history for anyone. Deaths are per-person monthly hazards on an actuarial curve — no culls, ever. Cohort-free honesty at cohort-level cost. (LLD §5, §17–§18, §27.)

## II.4 Economy

Three nested layers. **Commodities:** water, power, gas, food (staple/fresh), fuel, materials, goods, waste — each importable (consuming junction/port/rail capacity in tonnes and slots) or locally produced through raw→intermediate→commodity chains reaching back to crude oil, coal, chalk and crops. **Firms:** founded by actual ambitious citizens, progressing startup→SME→enterprise, consuming professional services superlinearly, banked by a credit layer under an off-map central-bank rate cycle; multinationals arrive by competitive incentive bidding and anchor whole ecosystems. **Fiscal circuit:** money enters by exports, tourism, out-commuter wages, FDI and grants; the tax panel spans income, sales, corporation, rates, council tax bands and a dozen behavioural levies, each with visible elasticity; public employees are shown honestly as net cost and indispensable machinery; the Public Service Pie allocates fire/police/health/education/civil service per benchmark ratios with consequences that scale with the city. (LLD §6–§8, §33, §45–§46, §50, §54.)

## II.5 Movement

One network, everything on it: commuters, school runs, shopping trips, leisure trips, freight, refuse rounds, emergency blue-lights. Daily capacity-restrained stochastic user equilibrium spreads routes like real learning; junction-level models (signals, roundabouts, spillback) make bottlenecks local and legible; nothing despawns. Modes from walking to metro via minibus, taxi, bike (real hills) and motorbike compete in a personality-weighted logit; parking is land with charges as the strongest mode lever; fuel and EV charging are systems with a fiscal sting; external commuting to London supports the dormitory strategy. Induced demand is modelled, so the lane myth teaches itself, narrated by the advisor. (LLD §19, §21, §38, §47, §49, §51.)

## II.6 Society

Education cradle-to-grave with a twenty-year fuse; physical and mental health driven by real causes (commute time, active travel, pollution, isolation, financial stress); crime generated by measurable deprivation and policed with concave deterrence, detective clearance and preventive youth provision — gangs as named entities, an MI5-analogue at the top, prison with rehabilitation economics; social services as the floor of every downturn; café culture and leisure-hours (168 − work − sleep − commute) as the daily-wellbeing engine; tourism with its holiday-let disease; coastal arrivals handled neutrally as operations and policy; defence bases arriving by central-government mandate as anti-cyclical anchors. News reports all of it with real names. (LLD §18, §25–§30, §36–§44, §55.)

## II.7 Government (the player)

Land purchase, zoning (8-way), construction with lead times and materials, budgets, loans against a credit rating, the full tax panel, the policy library (every policy a modelled instrument with previewed impact, scoped citywide/district/road), named districts as identity mechanics (CBD, Science Park, Tax-Free Harbour), capacity-export contracts, incentive packages, grants and mandates. Milestones (13 tiers to Centopolis) grant unlocks, development points spent in 'Blue'-style per-service trees, expansion permits and loan uplift. (LLD §4, §7, §22, §34, §36, §39, §52, §55.)

## II.8 Software architecture

Engine and UI are separate domains speaking a versioned command/event/delta protocol — in-process channels v1, gRPC by config flag, which is also the seam for the GPU solver sidecar and the Azure tiers (Blob saves, Batch balance-tuning, stateless solver offload, surrogate models; persistent cloud server shelved). Determinism is absolute: 256 fixed shards, ordered merges, counter-based RNG, integer money, CI determinism gate. The UI is a retained cell-buffer diff renderer with decoupled input/render loops, view subscriptions, and hard latency budgets; the key grammar is a leader-key language with which-key discovery that withdraws as the player learns. Four permanent harnesses (stub engine, replay fixtures, headless, synthetic worlds) mean the front end is built first against canned data and modules go real one at a time behind a registry that the F12 info panel displays and toggles. The Book of Work (MariaDB) is the project's memory; Git is trunk-based with BoW-referenced conventional commits; Claude Code operates under the working agreement in LLD Part III-C §6. (LLD §15, UI-SPEC, M0-ENG.)

---

# PART III — LOW-LEVEL DESIGN

# III-A · Game Systems (§1–§55)

## 1. Vision & Pillars

1. **Mechanics-first.** Every system is a real model with visible internals — queues you can watch, stock levels that deplete, projections that warn you. No decorative systems.
2. **Individuals, not sheep.** Every citizen is a persistent person with personality, tastes, and a life history shaped by the city you build (§5). The player can stop on any street and meet a real, consistent human.
3. **Squeezed, never ambushed.** The projection engine (§13) means the player should almost always be able to see trouble coming. Difficulty comes from competing demands on finite money, land, and throughput — not from surprises.
4. **Real ground.** Real Folkestone topography from OS Terrain 50 open data. Real constraints: chalk aquifer, escarpment, one motorway junction, the sea.
5. **High realism, playable pace.** Where realism and playability conflict, we bend numbers (migration rates, construction speed), never structures (people still age month by month, goods still physically arrive).
6. **Both directions on every dial.** The mechanism that grows the city (attractiveness-driven migration) is the same mechanism that kills it (the Detroit spiral, §12). No scripted failure.

**Win:** 100,000,000 residents ("Centopolis"). **Lose:** insolvency, or the death-spiral condition (§12). **Target pacing:** a winning run ≈ 80–150 real hours.

---

## 2. The World

### 2.1 Start tile
2 km × 2 km, 200 × 200 cells at 10 m/cell, positioned to span **Folkestone West to the Sandgate/Seabrook shoreline**:

- **North edge:** chalk escarpment of the North Downs rising past 120 m — cheap land, expensive to build on (slope classes, §2.4).
- **Upper third:** the M20/A20 corridor at ~50–60 m elevation, running roughly E–W, with **one grade-separated junction** in-tile. At the tile edges the motorway terminates in simple turnaround loops (abstraction: traffic to/from "the world").
- **Middle:** the flat-to-gently-sloping shelf — the premium buildable land.
- **South edge:** shingle/sand shoreline and sea cells. Sea is a mechanical asset: future port (§9), fishing, desalination, land reclamation (late milestones).

All man-made features are wiped except the motorway. Heightmap is generated at build time from **OS Terrain 50** (OGL-licensed Ordnance Survey open data), downsampled to the 10 m grid. Real hydrology (Pent/Seabrook stream lines) derived from the terrain flow model.

### 2.2 Off-map connections (lore-accurate)
- **Road:** the motorway — import/export artery #1, finite junction throughput (§8).
- **Power:** purchasable grid connection referencing the real **Sellindge converter station** (and Dungeness down the coast as flavour). Buy capacity in tranches; standing charge + per-MWh import price.
- **Sea:** dormant until the port milestone.
- **Rail / Channel Tunnel:** absent at start; the Chunnel is a flagged **future-dev mega-project** (post-v1 content slot at the Megacity tier).

### 2.3 Expansion — the Kent map
Horizontal, 'Blue'-style tile purchase over real geography. The full theoretical extent is **~60 × 60 km of East Kent** (Dover–Ashford–Dungeness triangle, sea on two sides), stored as **2 × 2 km purchasable tiles** (up to ~900 tiles, ~36 M cells). Only owned tiles are fully simulated; unowned tiles exist as terrain + price. Tile price scales with terrain quality, adjacency to your city, and milestone tier. Late-game density unlocks (high-rise, then very-high-density residential at Megacity+) make 100 M arithmetically reachable on real ground at semi-real densities (~40–60 k/km² peak district density).

### 2.4 Cells
Per-cell state (kept lean, ~30 bytes core): elevation, slope class (flat / gentle / steep / unbuildable), surface (grass, woodland, water, shingle, rock), ownership, zoning, structure ref, land value, and per-overlay flow scratch (traffic, utility coverage, pollution, decay). Slope class multiplies construction cost and gates building types.

---

## 3. Time

Adopted directly from 'Blue', tuned for our scale:

| Control | Multiplier | Notes |
|---|---|---|
| Space | Pause | Full UI available; orders queue |
| 1 | 1× | 1 game month ≈ **8 real minutes** (config: `secondsPerMonthAt1x`, default 480) |
| 2 | 2× | 4 min/month |
| 3 | 4× | 2 min/month |
| Debug | 8× | Dev/debug mode only (mirrors 'Blue's hard cap) |

**Two-layer clock, 'Blue'-style:** the displayed clock runs one **day-night cycle per calendar month**. Inside each cycle run **30 logistics day-ticks** (~16 real seconds each at 1×): traffic assignment, deliveries, stock draw-down, queues, service dispatch. At each cycle end, the **monthly tick** resolves in fixed deterministic phases:

`production → logistics settlement → consumption & shortfall → population (births, aging, education, employment, health, death, migration) → land value & decay → finance (wages, tax, interest, contracts)`

Crisis events auto-pause and drop the camera/speed to the relevant queue. Save points: manual anytime + autosave every game year (rolling 10) + milestone saves.

Sanity check on pacing: 1 game year = 24 min at 4×; 250 game years ≈ 100 real hours. The `secondsPerMonthAt1x` constant is the master pacing knob, tuned by headless Batch runs (§15).

---

## 4. Population Scale & the Milestone Ladder

Growth is **migration-dominated**: natural increase is realistic (and therefore small); the engine of growth is **Attractiveness** (§11), pulling migrants from an abstract world pool (v1: infinite pool, static world — matching static import prices; both share the "living outside world" future hook).

| # | Tier | Population | Signature unlocks (full inventory in §10) |
|---|---|---|---|
| 1 | Wilderness | 0 | Land purchase, dirt roads, boreholes/wells, road import contracts, small housing |
| 2 | Hamlet | 100 | Farm plots, general store, basic clinic |
| 3 | Village | 500 | Primary school, paved roads, small warehouse |
| 4 | Small Town | 5,000 | **Grid connection (Sellindge)**, secondary school, bus routes, fire & police posts |
| 5 | Town | 20,000 | **Expansion tiles**, light industry, logistics depots, sewage works |
| 6 | Large Town | 50,000 | Hospital, further education, landfill/waste plant, leisure venues tier 1 |
| 7 | Small City | 100,000 | **Port**, university, districts & policies, mid-rise zoning |
| 8 | City | 250,000 | Heavy rail (internal), mass transit, high-rise zoning, stadium |
| 9 | Metropolis | 1,000,000 | Airport, financial sector, land reclamation, advanced healthcare |
| 10 | Conurbation | 5,000,000 | Regional expansion tranche (Dover/Ashford), desalination, metro |
| 11 | Megacity | 10,000,000 | **Channel Tunnel project slot (future dev)**, very-high-density housing |
| 12 | Megalopolis | 50,000,000 | Automated logistics, vertical farming, arterial megastructure roads |
| 13 | Centopolis | 100,000,000 | **WIN** |

Each milestone grants: unlocks, an expansion-permit allowance, a cash award, and a loan-facility uplift (a pattern taken from 'Blue'). Cheat/debug mode can force-unlock any tier for testing (per decision: e.g. port testing pre-100k).

---

## 5. Citizens — the Option B Model

**Every citizen is a persistent individual record, forever.** What varies is *simulation fidelity*, never existence or consistency.

### 5.1 The citizen record (~250 bytes)
- `id` (uint64), `birthMonth` (int32 — age is derived, continuous, **no shared birthdays, no culls**: stored to the month, deaths are per-person monthly hazard draws)
- `sex`, `householdId`, `partnerId`, `childIds`, `homeCell`, `workplaceRef | schoolRef`
- **Personality vector P** — 8 axes, 0–100: *sociability, ambition, conscientiousness, novelty-seeking, physicality, community-mindedness, patience, aesthetic drive*. Initialised at birth from parental blend + deterministic noise.
- **Education record** — stage history + quality-weighted attainment score. Education *modifies P over time*: good schooling widens ambition, novelty-seeking, and taste range; poor schooling narrows them. This is the literal mechanism by which underfunded schools produce, twenty game-years later, a workforce with lower skill *and* a leisure economy with narrower demand.
- **Leisure taste weights** — derived (deterministically) from P × education × age: a distribution over venue categories (sport, arts, nightlife, nature, community, gaming, dining…). Venues succeed or fail on personality-weighted patronage of the actual population within reach.
- `healthBand`, `wealth`, `employmentState` + sector, `satisfaction` components (housing, services, environment, leisure-fit, commute)

### 5.2 Adaptive fidelity — the dial
- **HOT** (full daily simulation): everyone in the viewport region, anyone the player follows/inspects, plus a rotating deterministic sample (~0.1–1%) citywide that keeps aggregate statistics honest. Hot citizens path to work, queue in traffic, attend venues, get sick, shop.
- **WARM:** recently-hot regions, simplified daily updates, cheap to re-promote.
- **COLD** (everyone else): advanced only at the monthly tick by a vectorised batch pass over the records — aging (implicit), mortality hazard `h(age, healthBand, healthcareAccess)` on a Gompertz–Makeham curve, education-stage transitions (September-gated under seasonality), statistical job matching, health drift, satisfaction update from district-level service coverage. Parameters for the batch pass are **measured from the hot sample**, not invented — cold behaviour is always an honest extrapolation of real individuals.
- **Life-writing:** when a cold citizen is inspected, the engine deterministically reconstructs their recent detail from their record + district statistics + `hash(seed, id, month)` — and the reconstruction is *binding* (it is what happened; aggregates already accounted for it).

**Determinism rule (absolute):** every stochastic draw for citizen `i` at month `m` uses an independent counter-based hash stream `hash(worldSeed, i, m, purpose)`. Same seed + same commands ⇒ bit-identical world, at any fidelity mix, on any machine or worker count. This is what makes cloud offload, replay, and regression testing free.

### 5.3 Memory & storage at 100 M
~250 B × 100 M ≈ 25 GB of citizen state: stored in **region shards**, cold shards paged to disk (and optionally Blob), memory-mapped; the monthly cold pass streams shards. Saves are **sharded gzipped NDJSON** (JSON-everywhere honoured; streamable; a save at 100 M is large but tractable, and headless tools can process saves without loading whole worlds).

### 5.4 Households & housing
Households are real (formed by partnering events among hot/sampled citizens; statistically among cold, calibrated to the sample). Housing demand = households × dwelling-size preference (personality- and wealth-driven). Overcrowding and rent burden feed satisfaction and the migration balance.

---

## 6. Needs & Commodities

Every need is either **imported** (opex, consumes logistics capacity, static v1 prices behind a `Market` interface so dynamic pricing is a config flip later), **produced locally** (capex + land + jobs + input commodities), or hybrid.

| Commodity | Import channel | Local production options | Notes |
|---|---|---|---|
| Water | Road (bowser, tiny) → pipe from off-map (later) | **Boreholes (chalk aquifer)** from turn 1; reservoir; desalination (tier 10) | Aquifer has a sustainable yield ceiling; over-abstraction degrades it. Seasonal stress (§11) |
| Power | **Grid tranche (Sellindge)** from tier 4 | Diesel gensets (early, dirty, dear), wind (escarpment!), solar, gas CCGT, nuclear slot (late) | Peak vs base load; winter peak |
| Food – staples | Road/port | Farm plots (harvest calendar), vertical farms (tier 12) | Storable; harvest lumps supply |
| Food – fresh | Road/port | Market gardens, fishing (post-port) | Short shelf life — the JIT poster child |
| Fuel | Road/port | None early; synthetic late | Powers logistics itself — a fuel shortage compounds |
| Construction materials | Road/port | Quarry (chalk/aggregate), timber, cement plant | Every build order consumes them — construction competes for the junction |
| Consumer goods | Road/port | Light → heavy industry chains | Demand shaped by wealth & personality mix |
| Waste (negative commodity) | Export by road (dear) | Landfill, incinerator (power co-product), recycling | Accumulates if unhandled → health & land value damage |

**Services** (non-tradable, §10): education, healthcare, elder care, fire, police, deathcare, leisure, transport — capacity + coverage radius + funding-level quality.

## 7. Economy, Land & Finance

- **Money flows:** household income (wages by sector/skill) → taxes (residential/commercial/industrial rates, player-set) → city budget → service opex, import contracts, debt service, construction. Businesses are simple firms: revenue from local demand ± export, costs from wages/inputs/rent; they open/close on profitability (this is what makes commercial zones live or die honestly).
- **Land market:** every cell priced continuously: `price = base(terrain) × access(junction, roads) × amenity(services, coast view, pollution⁻¹) × scarcity(city size)`. Player buys before building; the city's own growth raises the price of the next acre — the core spend-early tension.
- **Loans:** facilities unlocked by milestone; interest set by **credit rating** = f(debt/revenue, payment history, reserve months). Miss payments → rating slides → refinancing spiral risk. Insolvency (can't meet obligations for 3 consecutive months with no available credit) = **game over**.
- **Investments:** surplus can be parked in interest-bearing reserves or invested into productivity (per standing "going for investments" requirement — modelled as capex programmes with multi-year payback curves shown in projections).

## 8. Logistics & Just-in-Time

The beating heart. Daily-tick resolution:

1. Each consumer (households via shops, firms, services, construction sites) draws from local **stock** (shops/warehouses have capacity, holding cost, and per-commodity shelf life).
2. Stock managers issue replenishment orders against **forecast demand + safety buffer** (player-tunable per commodity: lean = cheap but fragile, fat = dear but resilient).
3. Orders become **truck movements** on the road graph. The **junction has finite slots/day**; excess queues — rendered literally as a text column of trucks coded by cargo, with wait times.
4. Deliveries with lead time (import lead: days by road, longer by sea but 10× bulk). Perishables expire in queue. Shortfalls hit consumption → satisfaction, health (food/water), production stoppages (inputs), construction stalls (materials).

**Traffic:** commuter + freight demand assigned over the road graph daily (capacity-restrained assignment; cached routes via contraction hierarchies, re-pathed only on network/life changes). Congestion lengthens commutes → satisfaction ↓ and delivery reliability ↓. Port (tier 7) adds artery #2: redundancy against junction saturation and road events.

## 9. Seasonality

Month index drives: **power demand** (winter peak), **water stress** (summer), **harvest calendar** (local staples arrive in lumps → storage strategy), **construction speed** (winter slowdown), **school year** (September intake gates education transitions), **leisure mix** (beach summer / indoor winter), minor **health wave** (winter). All seasonal curves visible in projections.

## 10. Service & Feature Inventory (mapped to 'Blue')

Full 'Blue' surface, all in v1, gated by tier (§4): roads & road maintenance · electricity · water/sewage · healthcare & deathcare (hearses, cemeteries/crematoria — deaths are continuous, so is deathcare demand) · garbage · education (primary → secondary → further → university) · fire · police & jail · elder care & child benefit analogues · public transport (bus → rail → metro) · parks, recreation & **leisure venues** (personality-patronised, §5.1) · communications/post analogue · districts & policies (tier 7: per-district taxes, ordinances) · city services buildings with upgrade paths · disasters-lite (road closure, storm surge on the shore, aquifer drought) as event pressure on JIT.

## 11. Attractiveness & Migration (the master dial)

`A = w₁·jobAvailability(skill-matched) + w₂·housingAffordability + w₃·serviceCoverage + w₄·environment(pollution, coast, parks) + w₅·leisureFit(venue mix vs would-be migrant personality distribution) + w₆·safety + w₇·reputation(momentum term)`

Monthly net migration = `g(A − A_world)` × capacity constraints (housing vacancy, junction throughput for arrivals — you can literally bottleneck your own growth). Emigration uses the same function per resident (personality-weighted: ambitious citizens leave sooner when opportunity dries up). **Reputation** is a slow-moving momentum term — cities rising attract beyond fundamentals; cities falling repel beyond fundamentals. That asymmetric momentum is the Detroit trap.

## 12. Failure — the Detroit Spiral

No scripted loss. The spiral: shock (major employer closes, service collapse, sustained shortage) → emigration → tax base ↓ → forced service cuts or debt → attractiveness ↓ → more emigration → **abandoned buildings** (decay state: drag neighbouring land value, small ongoing hazard/fire/crime pressure, cost money to demolish) → district blight spreads cell-by-cell. Recovery is possible but expensive (demolition, targeted investment, tax relief districts). **Death conditions:** insolvency (§7), or population falls below **10% of historic peak** after the city has ever exceeded 50,000 — the ghost-city ending, with an epilogue screen generated from the city's history log.

## 13. UI

Terminal, keyboard-first (Fn keys + arrows + command palette `:`), optional mouse (Windows Terminal primary target; conhost degraded-but-works). Persistent chrome: top bar (date, clock-cycle, speed, money, population, rating), bottom **Alert stack** — prioritised, colour-coded, each alert jumps to its screen (`Water deficit in 3 months`, `School capacity exceeded next September`, `Loan payment due`, `Junction at 94%`).

- **F1 Map** — scrollable viewport + minimap; overlays (`o` cycles): ownership, land value, zoning, utilities, traffic, pollution, decay, coverage per service. Inspect any cell/building/citizen (`enter`), follow a citizen (`f`).
- **F2 Finance** — P&L, balance sheet, loans, credit rating, budget sliders.
- **F3 Land & Construction** — purchase, zone, build queue (materials + labour + lead time), demolition.
- **F4 Services** — per-service funding slider, capacity vs demand, coverage map jump.
- **F5 Trade & Logistics** — import contracts, junction queue live view, warehouse stock/buffer policy per commodity, port (when unlocked).
- **F6 Demographics** — ASCII population pyramid (by month-age, so it's smooth), education pipeline, workforce by sector/skill, personality & leisure-taste distribution of the city.
- **F7 Projections** — every demand/supply curve N years forward (N grows with unlocked forecasting; cloud offload extends it), all seasonally aware; the anti-ambush machine.
- **F8 Districts & Policies** (tier 7+).
- **F9 Ticker & History** — the news ticker: generated from real events ("340 families left for the mainland this month citing rents"; "First graduates of Cheriton University enter the workforce"), full searchable history log (also the epilogue source).
- **F10 Menu/saves · F12 Debug.**

## 14. Debug Mode (F12, dev builds & cheat-unlocked)

Fixed RNG seed per save; time warp incl. 8×; free money / instant build / force milestone unlock (e.g. port testing now); entity inspector (any cell/citizen/firm/market dumped as JSON); **invariant checker** every tick (conservation: people, money, goods must balance — hard assert in dev); fidelity-dial exposure (set HOT radius, watch cost); **headless mode**: run N months from a save, emit result + diff — the regression/balance harness.

## 15. Architecture (decisions folded; full detail in the Architecture Doc)

- **Go**, single binary v1. **Engine as a service from day one:** TUI ↔ engine speak only a versioned message protocol (commands in: `BuyLand`, `SetBudget`, `AdvanceTicks`…; state deltas + events out). In-process channel v1; **gRPC transport implemented but dormant** — flip a flag for out-of-process, remote, or cloud, TUI unchanged.
- **Determinism** enforced from first commit (fixed-order tick phases, counter-based hash streams, no map-iteration nondeterminism, integer/fixed-point money).
- **Cloud path (designed, unbuilt v1):** Blob cloud saves early → Azure Batch headless balance-tuning during development (thousands of parameter runs, centuries per run) → hybrid solver offload slots (traffic equilibrium, deep projections, batch life-writing) as stateless request/response with local-fallback → surrogate-model path (train offline on cloud solves, ship fast approximators) if any subsystem outgrows local. Persistent cloud sim server stays shelved unless shared worlds ever appeal. LLM strictly optional soft-layer (ticker prose, advisor persona) — never the number cruncher.
- **TUI framework:** tcell (mouse-capable, Windows Terminal solid) — confirm in Architecture Doc.

## 16. Roadmap

1. **M0 — Architecture Doc** (module layout, protocol schema, save schema, tick pipeline spec, determinism rules).
2. **M1 — Headless engine core:** terrain import, time, citizens (Option B), needs/logistics, finance; invariant suite; runs 300 game-years in seconds.
3. **M2 — Balance harness:** Batch parameter sweeps; tune pacing knob & growth curves until 0→100 M is achievable-but-hard.
4. **M3 — TUI:** F1–F7 + alerts + ticker on the proven engine.
5. **M4 — Full v1 surface:** districts/policies, port, all services, seasonality polish, Detroit spiral tuning, save/load UX.
6. **Future-dev slots:** Chunnel mega-project, dynamic world market, finite world pool, multiplayer/shared worlds (architecture-ready), LLM soft layer.

---
*End of GDD v1.0 — every § references a decision made in planning sessions; open items are marked "confirm in Architecture Doc". Mark it up.*

---


## 17. Resource Consumption Model

Consumption is **coefficient-driven**: every building's demand = class coefficients × occupancy/throughput, so the catalogue (separate doc) never hard-codes utility numbers and rebalancing is a table edit. All utilities are networked: **water, wastewater (sewage), electricity, gas** are separate networks with sources, pipes/wires (built in road corridors for free, cross-country at cost), storage, and losses. Wastewater ≈ **95% of water drawn** unless stated. Numbers below are UK-plausible defaults (config: `data/consumption.json`), before seasonal modifiers (§9).

### 17.1 Per-person daily baseline (residential)
| Need | Rate/person/day | Notes |
|---|---|---|
| Water | 145 L | +25% summer peak; gardens on detached housing +20 L |
| Electricity | 3.5 kWh | Household base 2 kWh + 1.5/person; winter +15% |
| Gas (heating/cooking) | 13 kWh | Strongly seasonal: ×2.2 Jan, ×0.2 Jul; electric-heated homes shift this to E |
| Food – staples | 1.4 kg | Storable |
| Food – fresh | 0.7 kg | 5-day shelf life chain |
| Household waste | 1.1 kg | Recycling rate reduces landfill share |
| Wastewater | ~138 L | 95% rule |

### 17.2 Per-user coefficients by building class (occupied-day)
| Class (unit) | Water L | Elec kWh | Gas kWh | Waste kg |
|---|---|---|---|---|
| School (pupil) | 18 | 1.5 | 3.0 | 0.20 |
| University (student) | 30 | 4.0 | 3.5 | 0.30 |
| Clinic (visit) | 40 | 3.0 | 2.0 | 0.50 |
| Hospital (bed) | 400 | 28 | 30 | 3.2 |
| Elder-care home (resident) | 220 | 9 | 14 | 1.4 |
| Office (desk) | 25 | 5 | 2 | 0.35 |
| Shop (m² sales/10) | 6 | 4.5 | 0.8 | 0.9 |
| Restaurant/café (cover) | 25 | 2.5 | 2.0 | 0.6 |
| Hotel (room-night) | 300 | 20 | 18 | 2.0 |
| Light industry (worker) | 60 | 22 | 12 | 4 |
| Heavy industry (worker) | 400 | 90 | 60 | 15 |
| Leisure venue (visitor) | 15 | 1.8 | 0.5 | 0.25 |
| Stadium (spectator-event) | 25 | 1.2 | 0.3 | 0.4 |
| Swimming pool/leisure centre (visitor) | 80 | 5 | 6 | 0.2 |
| Park (visitor) | 4 | 0.1 | 0 | 0.15 |
| Station – rail/metro (boarding) | 2 | 0.4 | 0 | 0.05 |
| Airport (passenger) | 15 | 6 | 2 | 0.7 |
| Water treatment works | — | 0.6 kWh/m³ treated | — | sludge 0.05 kg/m³ |
| Sewage works | — | 0.5 kWh/m³ | — | sludge 0.25 kg/m³ |
| Desalination | — | 3.8 kWh/m³ | — | brine (sea-return) |

Farms, quarries, plants: producer coefficients in catalogue (output/day, input demands, land, seasonal profile). **Gas network** is optional strategy: all-electric city possible (higher elec peak, no gas capex); gas arrives by off-map pipeline tranche (like Sellindge for power) or LNG via port.

## 18. Wellbeing — Physical & Mental Health

Two per-citizen state tracks (0–100), updated monthly (cold) / daily-influenced (hot), driving mortality/morbidity, productivity, satisfaction, and migration. **Wellbeing = f(physical, mental, satisfaction)** is the headline city stat.

**Physical health** drivers: age curve; healthcare access & quality (coverage + funding + queue time for treatment — an under-funded hospital shows as a *waiting list*, our JIT-for-people); diet (fresh-food availability share); **active travel** — walking/cycling commute share materially improves it (mode choice feeds health, a real loop); pollution exposure (industry/traffic overlays at home cell); sport participation (venue access × physicality trait). Illness events reduce workforce availability; epidemics-lite under low health + crowding.

**Mental health** drivers (each with a real mechanism, not vibes): **commute time** — the single biggest modifiable hit, nonlinear penalty past 45 min door-to-door; job–ambition mismatch (over-qualified/unemployed ambitious citizens degrade fastest); green-space access within 400 m; leisure-fit (venue mix vs personal taste weights, §5.1); crowding (persons/room); isolation (sociability trait × community venue access); noise (proximity to motorway/industry/airport); financial stress (rent burden > 35% income); unemployment duration. **Services:** counselling rooms (clinic upgrade), mental-health unit (hospital upgrade), community centres as prevention. Poor mental health raises emigration probability, lowers productivity and physical health (coupled), and surfaces in the ticker honestly.

## 19. Transport, Routing & Traffic — doing what 'Blue' couldn't

**'Blue's known routing flaws, and our structural answers:**
1. *Individually-optimal pathing with no congestion anticipation* → herds pile onto the same "cheapest" route. **Ours:** daily **capacity-restrained stochastic user equilibrium**: iterative assignment where link times update via volume-delay functions and flows re-split until stable — routes spread across alternatives exactly as real commuters learn. Deterministic, converges in a few iterations on cached warm starts.
2. *Lane choice decided at path time* → mile-long single-lane queues beside empty lanes. **Ours:** lanes are link capacity, not routed objects; turn-movements at junctions carry their own capacities. The pathology cannot exist.
3. *Despawn/teleport masking gridlock.* **Ours:** nothing ever despawns — vehicle-conservation is an invariant-checker assert. Gridlock is real, visible, and yours to fix.
4. *Weak junction modelling.* **Ours (§19.2).**

**19.1 Modes** — per-trip **nested logit** over generalised cost (time + money + comfort + reliability), personality-weighted (physicality→cycle/walk, patience→transit tolerance, wealth→taxi/car):
| Mode | Speed (urban) | Cap/unit | Road/track load | Notes |
|---|---|---|---|---|
| Walk | 5 km/h | — | none | free; health+; ≤2 km trips |
| Bicycle | 15 | — | bike lane 5k/h or road (0.2 PCU) | health++; hills matter (real slopes!) |
| Motorbike | 28 | 1–2 | 0.4 PCU | cheap, risky (accident rate) |
| Car | 15–48 by congestion | 1–5 | 1.0 PCU; lane ≈1,800 PCU/h | needs parking (land!) |
| Taxi | as car | 4 | 1.0 + deadhead | fleet size player/market-set |
| Minibus (demand-responsive) | 20 | 14 | 1.2 PCU | early transit before fixed routes |
| Bus | 17 (stops) | 80 | 2.0 PCU | routes player-drawn; bus lanes buildable |
| Tram | 22 | 250 | street track | tier 8 |
| Metro ("tube") | 33 | 1,200/train, 30k/h/dir | tunnel (capex!) | tier 10 |
| Heavy rail | 70 | 1,000/train | surface track | internal tier 8; **external from tier 5** |
| Ferry | 15 kn | 300 | — | post-port |
| Air | — | — | — | tier 9, external only |

**19.2 Junctions:** every node has a control type — priority, mini-roundabout, roundabout (entry capacity formula), or signals (cycle time + green splits, auto-optimised or hand-set — a signals screen for the traffic obsessive). Turn-movement capacities; **queue spillback**: each link stores a queue; when queue length exceeds link storage it blocks the upstream junction — so one failed roundabout genuinely locks a district, and the map view shows the queue as a growing text bar per approach with wait time. Level crossings, bus priority, and pedestrian phases included.

**19.3 Commute accounting:** every worker/pupil has a computed **door-to-door time** (access walk + wait + in-vehicle + transfers), stored per citizen (hot) / distributional per district (cold), shown citywide as a distribution in F6 and fed to mental health (§18). Freight shares the same network — trucks are PCUs with junction slot demand (§8) — so commuters and JIT genuinely compete for road space, which is the whole game.

## 20. Roads & Auto-Naming

Roads are **named edges with identity**: class (dirt track → street → avenue → dual carriageway → motorway), lanes, speed limit (player-settable), start/end nodes, and maintenance state (potholes raise costs & lower speeds — maintenance budget is real). The inspector for any road shows: name, endpoints, daily volume by segment, v/c ratio, top 5 origin→destination flows using it, and *which alternative routes exist* — 'Blue' never told you why a road was full; we always can, because assignment is ours.

**Auto-naming (every object, deterministic from seed+id, player-renameable):**
- **Roads:** Kentish corpus (Cheriton, Seabrook, Pent, Risborough, Sandgate, Downs, Alkham, Saltwood…) × class-appropriate suffix (Lane/Road/Street/Avenue/Way/Drive/Close; motorways numbered M-, A-). Continuation rules so one road keeps one name through junctions.
- **Civic buildings:** named for **notable deceased citizens** (first mayor-era settlers, long-serving teachers — pulled from real records: "The Edith Harrow Primary School") — the individual-citizen model paying off in flavour; or toponym + type.
- **Infrastructure:** functional numbering ("Pumping Station No. 3", "Seabrook Substation").
- **Districts:** real local toponyms first, then generated compounds.
- **Transit:** lines auto-lettered/coloured, stations named by street/district.
The ticker uses names, so the city reads like a place, not a spreadsheet.

## 21. External Commuting & Housing

**Out-commuting:** citizens may hold **off-map jobs** (London, Ashford, Dover — abstract job pools with wage levels and capacity) reachable via motorway (car/coach) or, from tier 5, the **external rail station** (fast, expensive season ticket, big capacity). The city collects their income tax without providing the job — the classic **dormitory-town strategy**, strongest early: a coastal village of London commuters is a legitimate opening. Costs: their commute time hammers mental health (§18) and long-run they demand local everything else. **In-commuting:** when local labour is short, off-map workers fill jobs (wages leak out, no residents gained) — visible in F6 so the player sees the leak.

**Housing typologies** (full stats in catalogue): mobile home, cottage, terrace, semi, detached, bungalow, farmhouse, mansion, **beach house** (shore cells), low/mid/high-rise flats, penthouse tower, student halls, retirement complex, co-living block, mega-density residential (tier 11+). Each has an **appeal profile** over household stage × wealth × personality (novelty-seekers → towers; community-minded families → terraces; retirees → bungalows by the sea). Citywide demand is therefore a *distribution over types* — "people want to live in all different types" is literal: a city of identical blocks leaves whole personality segments unhoused-by-preference, suppressing attractiveness even with vacancy. Mismatch is visible in F6 (demand vs stock by type).

## 22. Unlock Economy ('Blue' model, adopted)

Three currencies, exactly as 'Blue' structures it plus purchase:
- **XP** — earned continuously (construction, population, service performance, milestones' progress bar).
- **Milestones** (§4) — XP thresholds; each grants cash, expansion permits, loan uplift, and **Development Points**.
- **Development Points** — spent in **per-category progression trees** (Roads, Electricity, Water & Gas, Health & Deathcare, Education, Fire, Police, Garbage, Parks & Rec, Transport, Communications, Welfare) to unlock specific buildings/abilities within a tier — so two players at tier 6 own different toolkits.
- **Buy** — off-map capacity is purchased directly with money regardless of points: grid tranches, gas pipeline tranches, external rail access, port permits, water bulk-supply contracts. Cheat/debug can force any unlock for testing.

## 23. Expansion-Content Mapping ('Blue' expansion content → our catalogue, all in v1 data)

| Group | What we take |
|---|---|
| Waterfront Transport | Full port ecosystem: cargo & ferry terminals, drawbridges, lighthouses, shipyard, fishing harbour, marina (catalogue §P) |
| Coastal Shoreline | Beach houses, waterfront leisure strip, promenade assets on shore cells |
| High-Rise | High-rise & landmark towers backing tiers 8–12 density |
| High-Street Retail | Mixed-use streetscape blocks, pedestrianised high street |
| Rail Stations | Station variants: through/terminus/underground/interchange |
| Office Tiers | Office tiers: Victorian chambers → glass HQ (sector progression) |
| Regional Cosmetic Set | Cosmetic — skipped (mechanics-first) |

## 24. Config Data Files
`consumption.json` · `modes.json` · `buildings.json` (catalogue) · `unlock_trees.json` · `naming_corpus.json` · `seasonal.json` · `external_world.json` — all JSON, all hot-reloadable in debug, all the balance surface for Batch tuning.

---


## 25. Refuse Collection & the Waste–Health Loop

Waste is not a building problem, it's a **logistics round**: every occupied cell generates waste (§17 coefficients) into bin stock (residential wheelie, commercial trade bins, industrial skips — capacities differ). Collection is scheduled **rounds run by real trucks on real roads** from depots/transfer stations — routes auto-optimised (player-overridable), consuming road space and fuel like all freight. Missed collections (truck shortage, gridlock, strike event, depot underfunding) leave bin overflow on the cell: → vermin index ↑ → local physical health ↓, land value ↓, fire risk ↑, and the ticker names the street. Streams: general → landfill/incinerator; recycling (player sets service level; contamination reduces resale); food waste → composting → farm input (§31). Landfills fill *permanently* and blight; incineration trades airshed pollution for energy; exhausted sites can be capped and reclaimed as parkland (§32 reclamation shares the mechanic). **The loop:** waste budget cuts are invisible for months, then vermin, then illness, then emigration — a classic Detroit on-ramp.

## 26. Emergency & Care Dispatch Model (unified)

Fire, ambulance, air ambulance, and police response share one **dispatch engine**: incident spawns at a cell → nearest available unit assigned → travels the real road network at blue-light speed (congestion still bites, ~×1.6 free-flow not teleport) → outcome quality = f(response time). Fire spread is per-cell (material, density, wind, hydrant pressure); slow response = block loss. Medical outcome curves make response minutes literally lethal or not; the **air ambulance** ignores roads (weather-limited, 1 unit ≈ 10 ground units of marginal coverage in congested eras — the anti-gridlock asset). Hospitals queue non-urgent care as **waiting lists** (months, visible, funded down); elder care and home-care draw from the same staffing pool as hospitals — a nurse shortage is one shortage everywhere. All response-time distributions appear in F4 and Projections.

## 27. The Educational Lifecycle (cradle → grave)

Month-aged citizens flow: **nursery** (enables second-earner households) → **primary** (5y, Sept gate) → **secondary** (11y) → fork: **sixth form** (academic) / **technical college** (trades → industry skill pool) / leave at 16 (unskilled pool) → **university** (needs halls; produces graduates + **research points**) → **adult education** (re-skills the unemployed mid-life; the answer to a dying industry) → **U3A/library programmes** (elder mental health). Each stage: capacity, funding→quality, distance/commute (children walk/bus — school-run traffic is real and awful), attainment carried on the citizen record, and personality drift (§5.1). Quality shortfalls surface 10–20 game-years later as workforce skill gaps and narrowed leisure economies — the longest fuse in the game. University research points buy productivity/tech modifiers and gate the mega-projects (§CS).

## 28. Crime, Policing & Security

**Crime generation** per district, monthly, by type: petty theft, burglary, vehicle crime, criminal damage, violent crime, drugs supply, organised crime, fraud/cyber (grows with era & wealth), **smuggling** (scales with port/harbour throughput — success rate vs customs funding). Drivers: unemployment (esp. young men, ambition-frustrated — the citizen model gives us this honestly), deprivation, inequality between adjacent districts, blight/abandonment, youth leisure deserts (no venues fitting under-25 taste profiles), and **low policing presence**.

**"More police = less crime", with shape:** deterrence = concave in patrol coverage (first cars per 10k cut most; diminishing returns), plus **clearance** (detectives at Divisional HQ) which removes active offenders and suppresses *persistence*, plus **prevention** (youth centres, job centres, lit streets, community centres) which cuts *generation*. Over-policing a settled district wastes money; under-policing a stressed one breeds gangs.

**Gangs:** when a district holds high youth unemployment + blight + low clearance for >24 months, a gang entity forms (named, tracked): claims territory, raises all local crime, taxes businesses (closures ↑), recruits from the matching demographic, and *fights* rival gangs (violent spikes on borders). Removal needs the full stack — clearance pressure + prison + regeneration + youth provision; decapitation without regeneration respawns it. Effects citywide: safety term in Attractiveness (§11), insurance costs on firms, land value, school outcomes in-territory, and emigration of families first — crime is a Detroit accelerant.

**The top floor:** Police posts → stations → Divisional HQ → **Constabulary Headquarters** (force-wide command: strategy sliders — patrol/detective/community mix) → at Metropolis scale, the **Security Service liaison office (MI5-analogue)**: handles organised-crime networks spanning districts, port smuggling rings, and a low-frequency terror-threat dial (major events/stadiums/airport raise exposure; Security funding + liaison lowers it; a successful attack is a reputation and mental-health shock — rare, never random-spam, always preceded by visible threat-level intel). Justice chain: courthouse throughput (backlogs release offenders), prison (capacity + **rehabilitation funding** → reoffending rate), probation.

## 29. The News System

Four layers, all generated from *real* sim events with real names (§20):
1. **Ticker** (rolling, F9) — atomic events.
2. **Monthly Bulletin** — front page at month-end: 3–5 ranked stories (editor = salience scoring: deaths, firsts, records, crises, milestones), e.g. *"SEABROOK GANG TAXES TRADERS — third shop shuts on Pent Lane"*, *"First Cheriton University graduates enter workforce"*, *"M20 queue hits 2 hours as port strike bites"*. Optional read-on-pause; archive searchable.
3. **Annual Review** — year in numbers + biggest story.
4. **Epilogue** — the city's whole history at win/death.
LLM soft-layer (optional, online): rewrites bulletin prose with flavour; the *facts* always come from the engine — no hallucinated news.

## 30. Coastal Arrivals (irregular migration)

Folkestone's real geography includes this and we model it neutrally, as operations + policy, both sides of the ledger honest. Small-boat **arrival events** on shore cells (frequency scales with era, world conditions, and weather/season; never player-triggered). Immediate needs: rescue (coastguard/lifeboat — new catalogue entries), **reception & processing capacity** (centre with caseworker throughput; insufficient capacity → hotels requisitioned at high cost + local satisfaction friction), then a **status pipeline** (months): granted → join the population as citizens (records like anyone — skills distribution set by world profile; long-run they work, pay tax, open businesses; integration speed helped by ESOL classes at adult-ed, job-centre matching, dispersed vs concentrated housing policy) or not granted → managed departure (cost). Policy sliders with real trade-offs, not right answers: processing funding (fast/cheap/slow), housing approach (dispersal vs centres), integration investment. Poorly handled: cost blow-outs, district tension events, reputation noise both directions. Well handled: a modest, steady, younger-skewing population and workforce inflow — historically how port towns grew. Ticker/bulletin report it factually.

## 31. Farming & the Biodiversity Engine

Each farm cell has **soil quality** and each map region a **Biodiversity Index (BDI)** (0–100) driven by: habitat share (woodland, hedgerows, field margins, wetland, unimproved chalk grassland — the escarpment is a BDI treasure), farming intensity, pesticide/fertiliser load, watercourse quality, and connectivity (corridors matter — fragmented habitat scores less).

**Player choices per farm:** crop — wheat, barley, rapeseed, potatoes, field veg, **orchards (Kent apples/cherries), soft fruit, hops, vines** (chalk slopes: real Kent wine), poly-tunnel salads; or livestock — **dairy, beef, sheep (downland), pigs, poultry**, with stocking density; and regime — **conventional** (fertilised/sprayed: +30–40% yield, −BDI, nitrate runoff → watercourse & **aquifer quality** (§W — your boreholes!), pollinator decline) vs **organic** (−yield, +price premium, +BDI, certification lag of 24 months). Add-ons: hedgerow restoration, margins, agroforestry (subsidy-style costs, BDI+).

**BDI feeds back:** pollinator index multiplies fruit/veg/rapeseed yields (collapse is possible and self-inflicted); watercourse quality gates fishing stocks (§PT) and raises water-treatment costs; BDI is a term in environment→Attractiveness and mental health (green quality, not just green area); resilience — high-BDI farming suffers less in drought/pest events. Chains: milk→dairy plant; livestock→**abattoir**→meat processing (an unpopular neighbour — §32 blight rules apply); grain→mill→bakery; fruit→packhouse; grapes→winery (premium export). Food miles: local fresh beats imported on freshness satisfaction.

## 32. Mining, Extraction & the Blight Model

Kent's real geology, layered under the map (**geology layer**: chalk everywhere; brick-clay pockets; sand/gravel in valleys; **East Kent coal seams** at depth in the eastern tiles — the real Betteshanger/Snowdown coalfield; offshore aggregate banks): mining is only available where geology permits — prospecting (cheap surveys) reveals it.

| Extraction | Output | Character |
|---|---|---|
| Chalk quarry | cement/lime feed | open pit, dust |
| Sand & gravel pit | aggregates | shallow, water-table risk |
| Brickworks clay pit | bricks | with kilns (air) |
| Ragstone quarry | building stone | small, premium |
| **Deep coal mine** | coal (power/steel/export) | headgear + spoil tip; subsidence risk radius; mining jobs culture |
| Offshore dredger | marine aggregate | port-based; seabed BDI hit |

**The blight model (general — applies to mines, heavy industry, abattoir, incinerator, landfill, airport, motorway):** every blighting object has a **noise radius** (dBA falloff) and a **viewshed** (computed from real elevation — the escarpment hides or exposes things honestly). A home cell that *hears* it takes a mental-health/satisfaction hit; one that *sees* it takes land-value + satisfaction hits; both stack. **Mitigations are real earthworks:** screening bunds, tree belts (5-year grow-in), enclosure buildings, night-working bans (policy: less noise, less output). Siting behind the escarpment is free mitigation — topography as gameplay. Exhausted pits **reclaim**: lake (leisure/BDI+), country park, or landfill void (then §25 rules). Deep-mine closure without transition = a one-industry district unemployed overnight — a scripted-by-you Detroit test.

## 33. The Freight Harbour — Tonnes & Chains

All freight is accounted in **tonnes/day** end-to-end. Port capacity = berths × crane rate (t/hr) × hours; customs throughput separate (smuggling risk when saturated, §28). Modal reach: road (25 t/truck — junction slots §8), rail freight (1,000 t/train, tier 8 yard), sea (small coaster 3 kt → container ship 40 kt). Storage: quayside stacks, silos (grain), tank farm (fuel), cold store (fresh/fish).

**Production chains (raw → intermediate → commodity)** — each stage a firm with input t/day → output t/day, jobs, power/water draw, blight class:

| Chain | Stages |
|---|---|
| Construction | chalk→cement; aggregate+cement→concrete products; clay→bricks; timber(import/forestry)→joinery |
| Steel & machinery | coal+ore(import)→steel→fabrication→machinery/vehicles |
| Food | grain→mill→bakery; milk→dairy; livestock→abattoir→processing; fish→processing; fruit→packhouse/winery |
| Consumer goods | plastics(import)+fabrication→light goods→(rail/port export) |
| Energy | coal→power; fuel import→distribution |

Exports earn; the balance-of-trade screen (F5 extension) shows t/day and £/day by commodity and by artery — watching your port flip the city from importer to exporter is a mid-game arc.

## 34. Zoning — Land Types (mapped to 'Blue')

Player zones; firms/households move in per demand: **Dwelling** (low/med/high density — typology mix per §21 within zone class) · **Shop** (local/high-street/large-format) · **Office** (t1–t3 §C/I) · **Entertainment** (leisure venues cluster — the night-time economy zone, noise rules apply) · **Farming** (§31 regimes) · **Manufacturing** (light industrial) · **Heavy Industry** (blight class, buffer rules) · **Mining** (only on revealed geology, §32). Zone demand bars (the classic RCI, now 8-way) driven by the real underlying models — unfillable demand tells you *why* (no labour, no power, no freight capacity) instead of 'Blue's mute bars.

## 35. Communications, Internet & E-commerce

**Eras of connectivity** (unlock tree, Comms category): telephone exchange → dial-up → **broadband hub** → **fibre backbone** → cellular masts (coverage overlay, 2G→5G by era) → a **submarine cable landing station** (real Kent-coast feature; late-game data-industry magnet). Internet quality gates: office tiers, data centres, university research rate, and the **remote-work share** — good fibre lets a personality/sector-dependent slice work from home, directly cutting commute demand (a traffic tool disguised as telecoms) and boosting the dormitory strategy (§21).

**Post & parcels:** letters (sorting office, declining volume by era — a managed-decline mini-story) vs **parcels** (growing with wealth, era, and e-commerce share). **E-commerce**: share of retail demand shifts online as connectivity + wealth rise; requires a **fulfilment centre** (the Amazon-scale warehouse: huge shed, thousands of jobs at modest wages, big rates income, serious van + truck traffic) and **last-mile depots** whose vans hit the same roads as everything else. The tension is deliberate: fulfilment convenience raises satisfaction and jobs while **draining high-street retail** — shop vacancies → town-centre vitality ↓ → §12 blight risk in the core. Counterplay: entertainment zoning, markets, pedestrianisation convert the high street from retail to experience. Nothing is free.

---

# Catalogue Supplement (adds to CATALOGUE v1)

## MP — Mega-Projects (ach + research points + M-tier; one each; multi-year builds)
| Object | Unlock | Cost | Effect |
|---|---|---|---|
| **Hadron Research Ring** | M10 + univ research | 2B | research rate ××, science tourism, prestige |
| **Space Launch Complex** | M11 + research | 3B | launches (events, exports), aerospace sector unlock, noise blight (coastal siting realistic) |
| **Heathrow-class International Airport** | M11 + regional airport ach | 5B | 4 runways, ~200k pax/day, migration & tourism ××, huge noise contour + surface-access burden (its own motorway/rail spurs required) |
| National Medical Centre | M10 + teaching hospital | 1.5B | citywide health ceiling ↑ |
| Eden Biodome | M9 + BDI>60 | 800M | BDI showcase, tourism, food research |
| Fusion Station | M12 + research | 2B | (from §E) |
| Channel Tunnel portal | M11 | reserved | future-dev slot |

## Security & Justice additions
| Object | Unlock | Notes |
|---|---|---|
| Constabulary HQ | M8+DP | force strategy sliders |
| Security Service liaison (MI5) | M9+ach | organised crime/terror desk |
| Customs house | with port | smuggling interdiction rate |
| Youth centre | M4+DP | crime *generation* cut |
| Probation office | M7+DP | reoffending ↓ |
| Coastguard station | M5+DP | §30 rescue + sea incidents |
| Lifeboat station | M4+DP | RNLI-style; coastal ach |
| Reception & processing centre | M5+DP | §30 caseworker throughput |
| ESOL programme | adult-ed upgrade | integration speed |

## Farming & food additions
Dairy plant · abattoir (blighting) · grain mill · bakery chain · packhouse · winery · hop garden · orchard · vineyard · poly-tunnels · agroforestry scheme · hedgerow restoration · wetland creation (BDI) · fish processing plant · cold store

## Mining additions
Prospecting survey · chalk quarry (exists) · sand & gravel pit · brick clay pit + brickworks · ragstone quarry · **deep coal mine** + spoil tip · offshore aggregate dredger berth · screening bund · tree belt · pit reclamation (lake/park/void)

## Comms & logistics additions
Sorting office · parcel hub · **fulfilment centre** · last-mile depot · cellular mast (2G–5G variants) · fibre backbone node · submarine cable landing station · data centre (exists)

---


## 36. Service Capacity Export — selling your slack

Any service with measurable capacity can run a **surplus book**: capacity − internal demand = exportable slack, sold to off-map neighbours (Ashford, Dover, "the region") on contracts (£/unit, term, cancellation penalty). One mechanic, many markets:

| Exportable | Unit | Notes |
|---|---|---|
| Refuse collection & disposal | £/t | run your rounds over the border; landfill void is a sellable asset |
| Incineration | £/t + you keep the MWh | |
| **Toxic/hazardous waste processing** | £££/t | dedicated facility (see supplement): the best margin in the game and the worst neighbour — blight class max, accident-event risk, reputation drag; the classic devil's bargain |
| Sewage & water treatment | £/m³ | |
| Surplus power | £/MWh via Sellindge (two-way) | your nuclear/wind overbuild becomes an income strategy |
| Hospital beds | £/bed-day | regional contracts; queue priority conflicts if you oversell |
| University places | £/student-yr | international students: fees + halls demand + young inflow |
| Crematorium/cemetery | £/service | |
| Prison places | £/prisoner-yr | ties §43; ethics-free money until overcrowding bites |
| Port transshipment | £/t | cargo that never enters your economy but uses your berths |
| Fire/ambulance mutual aid | standby retainer | units may be away when *you* need them |

**The rule that makes it a game:** contracts are commitments. Your own demand grows into sold capacity, and you face penalty-payment vs service-cut choices — the projection screen shows contracted vs internal demand curves crossing years out. Slack is never free money; it's a short position on your own growth.

## 37. Shopping & Grocery Access

Households generate **shopping trips** (real travel demand — cars, walks, bus): frequency and destination by format access: corner shop (walkable, dear, thin range) · market hall (fresh++, §31 local link) · supermarket (car-borne, cheap, freight anchor) · retail park (car-only) · **online delivery** (§35 share: no trip, van instead). Each home cell gets a **grocery access score** (time × price × freshness); poor scores = *food deserts* → diet quality ↓ → physical health (§18) — a poverty-geography mechanic that emerges naturally in blighted districts when the last supermarket leaves. Shopping trips load the network (Saturday peak differs from commute peak — the daily-tick clock shows it), and grocery price level feeds cost-of-living → real wages → attractiveness.

## 38. Parking

Parking is land wearing a disguise. On-street (capacity per road type; permits by district), surface car parks, multi-storey (exists), park & ride (exists), workplace parking. **Charges are a player instrument** (per district, per hour, permit prices): revenue *and* the strongest mode-choice lever in the toolkit — expensive parking + good transit shifts commuters off the network. Insufficient parking near destinations creates **cruising traffic** (search circulation added to local load — the invisible congestion 'Blue' never modelled) and overspill into residential streets (satisfaction ↓, permit politics). Every non-walking car trip must terminate in a real space; the F1 parking overlay shows occupancy heat. Late-era autonomy (M12 tree) shrinks demand — car parks become redevelopment land, a lovely endgame dividend.

## 39. Taxation — fine-grain controls (F2 expanded)

Beyond the three headline rates, the full instrument panel, each with response curves (every tax has an elasticity — push too far and the base moves, evades, or leaves; the curve is *shown*, Laffer-honest):

- **Residential:** council-tax **bands by housing typology** (tax the mansions, spare the terraces — or invert), per-district multipliers (tier 7+), empty-homes premium, **holiday-let surcharge** (§44 lever).
- **Business:** rates by zone class × land value; small-business relief threshold; per-district enterprise-zone discounts (regeneration tool — pairs with §28 gang counterplay).
- **Behavioural/levies:** parking charges (§38) · **congestion charge** (cordon around districts, gantries, per-entry) · road/bridge tolls on player-built links · landfill tax (internal — pushes your own recycling) · nightlife levy (funds police overtime; too high kills the night economy) · workplace parking levy · fuel duty share · **tourist bed tax** (§44) · planning fees · user charges per service (pool entry, tip fees, prescription analogue).
Every instrument: revenue line in F2, incidence display (who actually pays), and projection of behavioural response. Fiscal identity is a build: dormitory low-tax haven, high-service Nordic seaside, or squeeze-the-visitors resort are all coherent strategies.

## 40. Social Services

The safety net beneath everything: caseload generated by deprivation, unemployment duration, family stress (crowding + financial stress from §18), addiction (nightlife/deprivation coupling), and domestic crisis events. Provision: **family support & child protection** (underfunding shows up 10 years later as attainment ↓ and crime ↑ in the affected cohort — the citizen records make this literal and auditable), **homelessness services** (prevention + hostels + housing-first policy; failure = rough sleeping concentrated in the town centre → vitality ↓, and a person-shaped reproach in the ticker), disability & carers support (releases informal carers back to workforce), fostering/adoption, addiction services (couples with §28 drug-crime generation — treatment cuts demand where policing cuts supply). **Systemic role:** social services set the *floor* of the Detroit spiral — a funded net makes decline shallower and recovery faster; a cut net turns recessions into collapses. Cheapest insurance in the game, easiest line to cut, longest fuse.

## 41. Café Culture & the Street-Life Economy

Town-centre **vitality** gets its positive engine: cafés, restaurants, delis, pubs with **outdoor seating** (pavement licensing policy; effective only in decent weather — season and even the day-cycle matter, a Mediterranean July pavement is dead in January gales), pedestrianised streets (exists) with market days, street performance licensing. Effects: "third places" directly counter isolation (sociability × access, §18), vitality raises adjacent retail survival and land value (the anti-e-commerce force, §35), and a strong café strip is an *attraction* feeding §44. Vitality index per centre = footfall × venue density × dwell quality × safety(§28) × weather-adjusted outdoor capacity; visible as an F1 overlay. This is deliberately the *small-money, high-leverage* system: a few hundred k of pedestrianisation and licensing can outperform a stadium for daily wellbeing.

## 42. Leisure Time & Exploration

Every citizen has a weekly **discretionary-hours budget**: 168 − work − sleep − chores − **commute** (the coupling that makes transport policy a leisure policy: an hour saved on the M20 is an hour in your economy's tills and your citizens' heads). Hours are spent across venues by taste weights (§5.1) subject to *access time* (leisure has its own trip generation — evening/weekend network loads). **Novelty decay:** venues lose freshness per visit for novelty-seeking citizens; a city that never opens anything slowly bores its most dynamic residents into emigrating — the **openings pipeline** (new venues, refurbishments, *events*) is retention infrastructure. **Events calendar:** player-run one-offs and seasons — seafront festival, food fair, match days (§L), concerts, Christmas market — each a mini logistics exercise (crowd transport, policing, waste spike) with satisfaction, vitality, and tourism payoffs. F6 gains a "how your city spends Saturday" view: hours by activity by district — unmet taste demand tells you literally what to build next.

## 43. Prison, Rehabilitation & Re-entry

Deepening §28's back end. Prison estate: local jail → **category prisons** (open/standard/high-security — mix must match offender profile; holding minor offenders in high-sec *raises* their reoffending), each with a **regime budget**: education-in-prison, work programmes, addiction treatment. **Reoffending rate** = base(offence, age) − regime effects − **re-entry support**: probation capacity, ex-offender employment (job-centre scheme + employer-incentive policy — some firms take the subsidy, satisfaction friction in some districts), housing-on-release (release-to-streets ≈ release-to-reoffend). Youth offending is a separate cheaper pipeline where every point of prevention beats cure (§28 youth centres feed in). **Overcrowding** (population > capacity, incl. §36 sold places) degrades every regime effect simultaneously — the export contract that quietly breaks your rehabilitation numbers. The long ledger: rehab spend returns over 5–15 years as crime ↓, workforce ↑; the F7 projection makes the case so the player can choose to believe it.

## 44. Holiday Tourism — the Visitor Economy

Visitors are a parallel population stream: **day-trippers** (rail/coach/car; hours, spend small, load big) and **staying visitors** (nights × spend). Draw = **attraction portfolio score** (beach + promenade + pier + venues + events + landmarks + heritage + *café culture §41* + countryside/BDI §31 — the escarpment walks are an asset) × **reputation** × **access** (external rail, coach, ferry terminal, airport tiers step-change reach: domestic → continental → global) × season (seaside curve: summer ×3, plus event spikes). **Accommodation stock** caps staying visitors: hotels, B&Bs, campsite/caravan park, and **holiday lets** — which convert *housing* into visitor beds: high yield, but each let is a home a resident doesn't have → rents ↑, workforce housing squeezed, winter ghost-streets in let-heavy districts (the real coastal-town disease; counter-instruments: let surcharge §39, licensing caps, seasonal-worker housing). Tourists spend into shops, cafés, venues, transport; bed tax skims; but they also queue at your attractions (capacity!), fill your trains, and spike waste and policing on event days — August is a logistics boss-fight your July projections warn you about. Tourism income is seasonal and reputation-fragile (a crime wave or a sewage-outfall event empties next summer's bookings — §28/§W consequences with a lag), so it's a strong second income, and a trap as a first one.

---

# Catalogue Supplement 2

| Object | Unlock | Notes |
|---|---|---|
| **Toxic waste processing plant** | M8+DP+ach | §36: max blight class, accident event table, £££ contracts |
| Surface car park / on-street schemes / residents-permit zone | M4/M3/M6 | §38 |
| Congestion-charge gantry cordon | M8 (policy+capex) | per-district |
| Hostel / housing-first units | M5/M7+DP | §40 |
| Family centre | M4+DP | child protection + support |
| Addiction treatment clinic | M6+DP | pairs with nightlife/deprivation |
| Café strip zoning + pavement licence policy | M3 / policy | §41 |
| Street market (day-licence) | M3 | vitality + fresh food |
| Festival ground (seafront) | M6+DP | §42 events |
| Christmas market kit | M5 | seasonal event |
| Open prison / high-security prison | M8/M9+DP | §43 category mix |
| Prison education wing / workshops | upgrade | regime budget targets |
| Probation & re-entry hub | M7+DP | exists ext.: adds housing-on-release |
| Hotel (boutique→grand) | M5/M8+DP | §44 stock |
| B&B conversion policy | M3 (policy) | terraces → visitor beds, reversible |
| Caravan & camping park | M3 | exists ext.: seasonal capacity |
| Holiday-let licensing regime | M7 (policy) | caps + surcharge lever |
| Tourist information & heritage trail | M4+DP | portfolio glue, small draw + |
| Lido (seafront pool) | M6+DP | seaside heritage, summer draw |
| Conference/convention centre | M9+DP | exists — adds shoulder-season demand |

---


## 45. Firms — Entrepreneur Culture to Enterprise

Firms are entities with a lifecycle, and **citizens found them**: monthly, ambitious citizens (ambition × education × sector experience × access to capital × premises availability × local demand signal) spin out **startups**. Progression: **Startup** (1–5 staff, unit/home office) → **Small** (6–25) → **Medium** (26–250) → **Enterprise** (250+, multi-site, may export, may list). Growth needs, at every step: customers (local demand or export), **premises** (right zone, right size — a scale-up with nowhere to move dies or leaves), staff (skill-matched hiring from the real labour pool), **credit** (below), and inputs (§33 chains). Failure is normal and modelled (insolvency, acquisition); a healthy economy churns. **Entrepreneur culture** is a measurable city property: density of startups per 1k, driven by university presence, incubators, professional-services depth (below), successful-founder alumni (records!), and — negatively — by rents and crime. Culture compounds: exits fund angels fund startups.

**Professional & financial services — the white-collar engine:** accountancy, law, insurance, recruitment, consulting, marketing, and **banking** are firm sectors whose *customers are other firms*: every firm consumes professional services proportional to size (a compliance/overhead coefficient), so the services industry scales superlinearly with the business base — this is where mass white-collar job demand comes from, filling your office zones and employing your graduates. Depth matters: thin local services force firms to buy off-map (leakage) and slow startup formation (no local commercial lawyer = slower incorporations).

**Banking layer:** local branch banks → commercial banks → (M9) **financial district** (glass HQs): banks take citizen deposits, extend firm credit and mortgages; credit availability is a real input to §45 growth and §21 housing. **Central-bank environment** is off-map and era-driven: a national base rate cycle (config curve + events) moves everyone's borrowing costs — including *yours* (§7 loans) — so a rate spike is a citywide weather system: startups starve, mortgages bite, your debt service jumps. The player doesn't control it; the player *positions* for it (F7 shows the rate outlook).

Blue-collar vs white-collar demand is thus emergent: chains (§33) + construction + logistics + services-to-buildings generate blue-collar demand; firm overhead + finance + public administration generate white-collar; the F6 workforce view shows both against supply — mismatch drives §27 adult-ed strategy and §11 migration profiles.

## 46. Multinational Attraction (FDI) & Anchor Employers

At M7+, **prospects** appear: named-archetype multinationals scouting sites, each with a requirement sheet and a competing off-map region. The player bids an **incentive package**: land (free/discounted), tax holiday (years, capped), infrastructure commitments (road/rail/power/water to site by date — real build orders with penalties), training partnership (college/university), planning fast-track. Win = an **anchor**: thousands of direct jobs, supply-chain firms spawning around it (§45 gets a demand injection), university links, exports, reputation. Risks: incentive cost, dependency (an anchor closing is a §32-coal-mine-scale shock — one-employer towns are fragile by design), and utility gluttony.

| Archetype (inspiration) | Needs | Character |
|---|---|---|
| Pharma R&D campus ("Pfizer" — *Sandwich, Kent is the real precedent*) | university, graduates, clean utilities | labs: high-wage white collar, low freight |
| Semiconductor fab ("Intel") | **colossal**: 40 MW, 20k m³ water/day ultra-pure, vibration-free land, port access | the utility boss-fight; highest wages |
| PC/logistics assembler ("Dell") | fulfilment-scale sheds, motorway, mid-skill | volume freight |
| Chemicals complex ("ICI") | §50 pipeline network, port tank farm, buffer zone | blight class high; feeds plastics chain |
| Steel process plant | coal+ore via port/rail, heavy power | §33 chain anchor |
| EV gigafactory ("Tesla") | 60+ ha flat land, 100 MW, rail freight, 8k mixed-skill | the land-assembly challenge |
| Rail engineering works ("Siemens") | rail connection, technical college pipeline | builds *your* rolling stock at discount (§47) |
| Aerospace campus ("Airbus Toulouse") | airport adjacency, runway access, supply park, engineers | apex anchor: pulls a whole ecosystem |

## 47. Rail Industry & Interconnected Transport

Rail becomes an industry, not just a mode: **stabling sidings → maintenance depot → heavy works** (mandatory per fleet size — unserviced fleets fail in traffic), **freight yards + intermodal terminal** (container crane: sea↔rail↔road transfer in tonnes, §33), and a **rolling-stock works** (Siemens anchor or home-grown): build trains/trams/metros locally (capex ↓, jobs, export contracts to off-map regions — §36 in physical form). **Interconnection doctrine** (people *and* cargo): hubs score on transfer quality — timed connections, single ticketing (policy), walk distance between modes; the journey planner the citizens use is the same router the player can query ("why is this commute 68 minutes?" — shown leg by leg). Cargo mirror: consolidation centres let motorway trucks hand off to rail/e-van last-mile (policy §52). Badly interconnected networks show up as transfer-penalty mode-share loss — build the interchange, watch the logit shift.

## 48. Destination Leisure & Retail

Two buildable destination archetypes, both regional-draw (visitors from off-map, §44 machinery):
- **Forest holiday resort ("Center Parcs")**: 100+ ha woodland (grow it or find it — BDI synergy: the resort *wants* your nature), lodges, subtropical pool dome (energy hog), ~1.5k jobs. Steady year-round staying visitors; countryside blight-free if screened.
- **Mega-mall ("Bluewater")**: the real Bluewater sits in a worked-out chalk quarry at Greenhithe — so ours unlocks as a **pit-reclamation option** (§32): the exhausted quarry becomes the site, walls hiding it from the viewshed (blight-clever, historically exact). 300+ shops' worth of retail floorspace, 7k jobs, colossal parking (§38), bus/rail links required, daily stock logistics in hundreds of tonnes. Effect: hoovers regional retail spend *in* (net importer of shoppers = income) while pressuring your own high streets (§35/§41 tension at maximum scale) — the town-centre-vs-mall war, player-refereed.

## 49. Vehicles, Fuel & the EV Transition

Every vehicle-km burns something, and both somethings are systems:
**Fuel era:** petrol stations (network coverage — a growing city needs forecourts like it needs substations), supplied by tanker truck from the **port tank farm / refinery** (§50) — fuel is a §8 commodity with its own JIT fragility (a fuel shortage strands *the logistics that fix shortages*; strategic reserve is a buildable). **Fuel duty share** is a fat early tax line.
**EV transition** (era + policy accelerated): fleet share shifts car→van→truck (trucks last). Charging stack: home chargers (driveway housing only — *terraces can't charge*, an equity mechanic pushing public provision), street posts, rapid hubs, depot charging (bus/van fleets), forecourt conversions. **Grid coupling:** charging is real load with an evening peak — mass EV + electric heating + no storage = winter peak crisis; managed-charging policy and §E batteries flatten it. **Fiscal sting:** fuel duty erodes as EVs grow — the tax base drives away, and the player must replace it (road pricing per-km, §39 instruments) — a genuinely modern fiscal problem, on rails, in the projections.

## 50. Oils, Rubber, Plastics & the Chemical Network

Upstream of §33: **crude oil** lands by tanker at the port (t/day, tank farm) → **refinery** (major build: fuel + feedstock outputs, top blight class + §26 hazmat fire category) → **petrochemical works** (feedstock → plastics, solvents) → plastics converters (→ consumer goods chain) and **rubber** (imported natural + synthetic from feedstock) → tyre plant (→ vehicle chain). Distribution is a fourth pipe network: the **chemical/fuel pipeline grid** — port ↔ refinery ↔ works ↔ industrial estates — expensive per km, leak-event risk (small, inspectable, maintenance-funded), but removes hundreds of hazardous truck movements/day from your roads (the safety-vs-capex trade shown in F5). No refinery? Import refined product at margin and skip the chain — the make-vs-buy doctrine (§6) at its largest scale.

## 51. Roads v2 — Types, Upgrades & the Lane Myth

Full 'Blue'-parity ladder (all §R types remain): alley · gravel · residential street · two-lane · one-way pairs · avenue 2+2 · bus-lane variants · tram-track variants · dual carriageway · urban expressway · motorway — each with lane counts, parking presence, tree/verge options (BDI + noise absorption).
**In-place upgrade** is first-class: any road converts to any compatible type; cost = delta + rebuild disruption — **roadworks are simulated** (weeks of lane closures re-routing real traffic — the projection warns you *before* you approve, and the schedule tool lets you phase works at night/summer). Widening into buildings requires purchase/demolition (compensation cost) — expansion is *rewarded* thereafter by lower maintenance/km-capacity and the milestone XP for congestion cleared.
**The pedagogy:** the game teaches **induced demand** honestly — capacity releases suppressed trips and shifts mode share back to cars, so the new lane refills (modelled: the equilibrium assignment plus latent-demand pool does this naturally, no script needed). The traffic advisor narrates it: *"Pent Way widened: journey times fell 6 minutes, then recovered 4 as 900 daily trips switched from bus."* Unblocking a jam usually **moves the bottleneck downstream** — the F1 flow view shows the new critical junction the moment you fix the old one; chasing bottlenecks *is* the learning loop. And the advisor's standing rule, surfaced when you draft lane 6+: **if you're building 12 lanes for cars, the failure happened years earlier and lives in your zoning and transit map.** The cure is always upstream: mode shift, proximity (jobs near homes), pricing — all of which you own.

## 52. Policies v2 & Named Districts

'Blue's policies are rightly criticised as shallow toggles: citywide binaries, opaque effects, negligible feel. Ours are **modelled instruments**: every policy states its mechanism (which coefficients it moves), its projected impact (F7 preview *before* enactment), its cost/enforcement needs, and its scope — citywide, **district**, or road-level.

**Named districts are the scope system:** the player draws and *names* districts — "Central Business District", "Cheriton Science Park", "**Tax-Free Harbour**" (freeport: customs exemption inside the wire, §33 boost + §28 smuggling exposure), "Old Town", enterprise zones — each carrying a policy bundle + §39 tax settings + an identity that feeds firm location choice and reputation (a named Science Park with a university link genuinely pulls labs; the *name is a mechanic*).

**Policy library** (highlights; full list in `policies.json`): **movement** — cycle-priority network, school streets, 20 mph zones, truck bans (by time/route/weight) with mandated **truck parks** + consolidation centres (§47), delivery windows, low-emission zone, congestion cordon (§39), park-&-ride mandate, bus priority, managed EV charging, road pricing per-km; **layout & wellbeing** — design codes (15-minute-neighbourhood coefficients: services-within-walk targets that zoning approvals must meet), green-space standards, active-frontage requirements (§41 vitality), noise curfews, pavement licensing; **economy** — small-business relief, incubator funding, local-procurement preference, freeport, tourism levies; **social** — housing-first, childcare subsidy (§54), ESOL, rehabilitation-first justice. Policies interact (LEZ + cycle network + consolidation = compounding, shown), and conflicting bundles warn.

## 53. Tunnels, TBMs & Hyperloop

**TBM programme** (M10+): buy/lease a boring machine — high capex, then tunnelling at a per-km rate that *falls with cumulative km* (learning curve, Boring-Company-style thesis): road tunnels, rail/metro tunnels (retro-cheapens §T metro), utility tunnels (bundle pipes/cables/chemical lines under streets — dig once). Escarpment and under-town crossings without demolition; spoil becomes §PT reclamation fill (chalk spoil built real Channel Tunnel land — Samphire Hoe — our reclamation mechanic quotes reality). **Hyperloop** (M12, research-gated): an *external* premium link (London in minutes) — small pax volume, huge attractiveness/reputation for the executive/novelty segment, monstrous capex; explicitly a prestige-infrastructure bet, not a transit backbone, and the advisor says so.

## 54. The Fiscal Circuit — top-down

The whole-economy view (new F2 master screen): **money enters** the city economy via exports (§33), tourism (§44), out-commuter wages (§21), FDI (§46), central grants (§55) — and **leaves** via imports, leakage, debt interest. Inside, the tax machine: **income tax** (per wage band), **sales tax/VAT share** (on local consumption), **corporation tax share** (on firm profits), rates & council tax (§39), duties & levies. 

**The civil-service truth, modelled honestly:** a public employee's income tax is a partial rebate of a wage *you paid* — public workers are a **net fiscal cost** (wage − tax clawback), and the game's books show gross vs net so the player learns it. But they are the *machinery of everything else*: teachers make future taxpayers, police protect the tax base, planners (below) compound the whole city. The art is the ratio — a successful society's benchmark ratios ship as the default **Public Service Pie** (per-1k-population targets: police officers ~2.4, teachers per pupil, nurses & GPs, dentists & opticians (now in §H), firefighters, social workers, refuse crews, **council officers**): the player adjusts every slice; consequences are mild at village scale and *systemic* at city scale (a 10% police cut at 2k is a quiet month; at 2M it's §28 arithmetic).

**Municipality quality:** the council itself is a modelled department — **planning & administration funding** sets permit speed, build-cost error rate (underfunded planning = projects 10–20% over), layout quality bonuses (well-planned districts get the §52 design-code compounding by default), and corruption risk at rock-bottom funding. Better-managed society at a cost, exactly as specified.

**Childcare:** subsidised childcare (policy + §ED nurseries) raises second-earner participation — partially self-funding through the income tax it unlocks, shown as a net line so the player sees a social policy *paying*. **Benefits & social housing** (§40 extended): unemployment support, housing benefit, social-housing build programme (below-market rents: costs capital, stabilises the low-wage workforce every other system needs — hoteliers, carers, drivers).

## 55. Defence & Central Government

The city sits in a nation. **Central government** interacts two ways:
**Grants** — optional competitive pots (transport, regeneration, culture — bid with match funding; §54 planning quality raises win rate) and formula support at low tax capacity.
**Mandates** — at population thresholds, national requirements arrive: *at 100k you will host a naval facility; at 500k an army garrison; at 1M contribute to air defence* — each mandate offers a **choice within compliance** (which facility, where) plus compensation grants; refusal is possible but costs grant access and reputation (a legitimate libertarian-city strategy with a price tag).

**Facilities** (each: service + civilian jobs, stable payroll (recession-proof anchor — the anti-cyclical employer), land take, resources, and character):
| Facility | Tier gate | Character |
|---|---|---|
| Army: infantry barracks → garrison → armoured (tank) regiment + training area | M7–M9 | large land take (ranges: noise days, public-access closures), steady jobs |
| Navy: patrol berth → **naval base** (the 100k mandate) → submarine pen | M7–M10 | needs harbour depth (dredging), shipyard contracts (§PT synergy) |
| Air force: radar station → airfield → fast-jet station | M8–M10 | serious noise contour (§32 blight rules), air-show event (+tourism) |
| Defence research: biological defence laboratory (Porton-style) · nuclear-engineering establishment | M9+ | high-security zones, elite science jobs, public-perception friction (consultation events) |
| Reserve & cadet centres | M5 | small, community + youth-provision (§28 prevention synergy) |

Defence integrates, not decorates: bases consume §17 utilities and §33 goods, personnel are citizens (housing! schools for forces families!), procurement flows to your shipyard/rail works/aerospace anchors (§46 — the Airbus campus bidding for defence contracts), and closure events (national policy) are §32-scale local shocks. 

---

# Catalogue Supplement 3
**Business & finance:** incubator hub · accountancy/law/insurance offices (§C/I sector variants) · branch bank → commercial bank → clearing house · stock-exchange listing venue (M10) · recruitment/consulting suites
**FDI anchors:** pharma campus · semiconductor fab · assembler sheds · chemicals complex · steel works · gigafactory · rolling-stock works · aerospace campus (all §46 sheets)
**Fuel & chemicals:** petrol station (rural/urban/motorway services) · tank farm · **refinery** · petrochemical works · plastics converter · tyre plant · chemical pipeline km · strategic fuel reserve · EV: home-charger subsidy (policy) · street posts · rapid hub · depot charging · forecourt conversion
**Rail industry:** stabling sidings · maintenance depot · heavy works · intermodal terminal · consolidation centre
**Destination:** forest holiday resort · mega-mall (quarry-sited) · truck park & driver facilities
**Tunnelling:** TBM (buy/lease) · road/rail/utility tunnel km · hyperloop terminal
**Health additions:** dental surgery · optician · GP surgery (splitting clinic tiers)
**Civic:** council offices → planning department → city administration · social-housing blocks (typology) · childcare expansion (nursery network programme)
**Defence:** per §55 table + married-quarters housing typology · cadet centre


# III-B · Terminal UI Specification


## 1. Rendering architecture — where "smooth and fast" comes from

The TUI is a **retained cell-buffer renderer with diff flushing**, not a print loop:
- Two buffers (front/back) of styled cells for the whole terminal. Widgets draw to the back buffer; a flusher diffs against front and emits *only changed runs* as ANSI. A typical sim-tick update touches a few hundred cells of a ~12,000-cell screen (200×60) — flushes are microseconds, and **flicker is structurally impossible** (no clears, ever).
- **Two loops, decoupled:** the input loop echoes within **<10 ms** (cursor moves, key-HUD updates, palette keystrokes never wait on anything); the render loop runs at a **10 Hz UI tick** (plus immediate on input), consuming **state deltas** from the engine over the §15 protocol. The UI never computes simulation values and never blocks on the engine — if a delta is late, the last frame stands and a staleness dot shows in the status bar. Smoothness is an architectural property, not an optimisation pass.
- **Terminal targets:** Windows Terminal primary (truecolor, mouse, full Unicode); conhost degraded profile (16-colour palette map, no mouse) selected by capability probe. Minimum 120×30; designed for 160×45+; layout engine reflows on resize (panes have min sizes; below minimum a pane collapses to a tab stub).
- Go implementation: **tcell** for terminal I/O and events; our own thin widget layer above it (tview and friends are form-oriented and allocation-happy; our map, charts, and diagrams are custom widgets on a shared cell-buffer API). Zero-allocation draw paths on the hot widgets; glyph/style lookup tables precomputed.

## 2. The visual language — block text as a real instrument panel

One consistent grammar across every screen, so the player reads the city like a trading terminal:
- **Structure:** box-drawing borders (`─│┌┐└┘├┤`), heavy variants for focused pane, dim for unfocused. Semantic palette: money green, water blue, power yellow, danger red, warning amber, decay grey-purple, selection inverse — colourblind alternative palettes ship day one (deficient red/green is disqualifying in a dashboard game).
- **Sparklines everywhere:** `▁▂▃▄▅▆▇█` — every number that can trend carries a 12-cell sparkline of its last 24 months. The F2 cash line, a road's volume, a school's roll: same idiom.
- **High-res graphs with Braille:** Braille patterns give a 2×4 sub-grid per cell — a 60×20-cell chart pane plots at an effective 120×80 resolution: real line charts (projections with confidence bands as dim dots), scatter, and the population pyramid at month-age smoothness. This is the single biggest "rich" trick in a terminal and it is cheap.
- **Heatmaps:** background-colour ramps on the map viewport for overlays (land value, traffic v/c, noise dBA, BDI, coverage). Foreground glyph stays informational (building/terrain), background carries the metric — two data layers per cell.
- **Gauges & big numbers:** dashboard tiles with large figure, delta arrow, sparkline, and threshold colouring; block-element gauges (`█▓▒░`) for capacities (junction slots, berth utilisation, prison occupancy).
- **Queues rendered literally:** the junction pane draws each approach as a lane of cargo-coded truck glyphs growing leftward with a wait-time figure — the signature image of the game.
- **Block-text diagrams (auto-laid-out):** three diagram engines — **chain diagrams** (production chains as boxes and arrows, §33/§50, live t/day figures on the arrows), **network schematics** (power/water/chemical grids as node-and-edge schematics with load colouring; transit lines as a tube-map style strip), and a **text Sankey** for the §54 Fiscal Circuit: money flows as proportional block-width bands from sources through the budget to sinks — the whole economy on one screen, widths updating monthly. These are computed layouts (layered graph drawing, small n), cached until topology changes.
- **Motion, sparingly:** ticker scroll, queue growth, and a 300 ms highlight pulse on any value that just crossed a threshold. Nothing else animates; calm is a feature.

## 3. Interaction — tab-driven, a learnable key grammar

**Spatial model:** F1–F12 screens (GDD §13) → each screen is a fixed arrangement of **panes** → `Tab`/`Shift-Tab` cycles pane focus (visible heavy border) → arrows *or* `hjkl` move within a pane → `Enter` drills in → `Esc` backs out one level, never losing where you were. `Alt+1..9` jumps straight to a pane. Every screen answers Esc-Esc-Esc to a known home state.

**The sequence grammar (the heart of it):** a **leader-key command language** with consistent mnemonics, which-key style discovery, and vim-family power tools:

- **Verb → noun → variant:** `b` (build) opens the build class row: `b r` roads → `b r s` residential street; `b r m` motorway. `z` (zone): `z d h` = zone dwelling high; `z f` farming. `p` policies, `s` services, `d` districts, `i` inspect, `g` go-to (screens by name), `t` transport-line editor. Two to four keystrokes reach **everything**; the grammar is regular, so knowing thirty keys means knowing three hundred commands.
- **Which-key HUD:** after any prefix, a bottom strip shows the available continuations (`b → r roads · e electricity · w water · …`) within one UI tick. Novices read it; experts outrun it; a setting dims it after N uses of a sequence — **the UI itself withdraws as you learn**, which is how the muscle memory is built rather than documented.
- **Counts and repeat:** numeric prefixes vim-style — `5 b r s` lays five segments; `.` repeats the last build/zone action at the cursor (the terraced-street workhorse); `u` undo where the engine permits (pre-commit orders), `U` redo.
- **Marks & search:** `m a` marks the cursor's map location as `a`; `' a` jumps back — twelve marks turn a 60 km map into a keyboard neighbourhood. `/` searches objects by *name* (auto-naming §20 pays off: `/pent` cycles Pent Lane, Pent Way, Pent Stream) and `n/N` steps matches.
- **Command palette `:`** — fuzzy-searched, every action listed **with its key sequence printed beside it** (the VS Code trick: the palette is the tutorial). Also takes parameterised commands (`:loan 5M 10y`, `:speed 3`, `:save alpha`) and is the scripting hook for debug.
- **Globals that never move:** `Space` pause, `1/2/3` speed, `o` overlay cycle (`O` reverse), `?` context help for the focused pane, `!` jumps to the top alert, `F9` ticker, `` ` `` toggles the console (debug builds).
- **Mouse, optional always:** click focuses/selects, wheel pans/scrolls, drag draws roads/zones — but **every mouse act has a key path**, and the key path is always faster. Mouse is the on-ramp; keys are the destination.

**Learning arc, by design:** stage 1 — menus via palette and which-key, reading everything; stage 2 — sequences with the HUD as a safety net; stage 3 — pure muscle memory, HUD dimmed, palette only for rarities. An optional first-hour scenario teaches the grammar by task ("zone your first street: `z d l`…"). Target: by hour ten the player operates the city at the speed of thought, which is precisely the fantasy a terminal game can deliver better than any mouse UI.

## 4. Dashboards & the drill-through rule

- **Composable dashboards:** a widget grid (tiles: bignum, gauge, sparkline chart, table, mini-map, alert list) with shipped layouts per screen and a **user layout editor** (`F10 → layouts`; saved in the profile JSON). The Overview (F1's right rail) is just a dashboard instance.
- **The drill-through rule (absolute):** *every number on every screen is selectable and `Enter` goes to its source* — a cash figure opens its ledger lines; a congestion percentage opens that junction; a school-roll number opens that school. No dead ends; the whole UI is one navigable graph. This is the discipline that makes forty-four systems feel like one game.
- **Tables:** sortable (`s` cycles columns), filterable (`f` inline query), exportable (`x` → CSV to the save folder) — the player who wants to spreadsheet their city is a player we love.
- **Projections pane idiom:** every demand/supply chart shows history (solid Braille), projection (dim), threshold lines, and the *decision markers* the player has queued (a planned school appears as a capacity step in the projection **before** it's built — the UI shows futures conditional on your build queue, which is the anti-ambush contract §1.3 kept).

## 5. Performance budget (hard numbers, tested in CI)

| Path | Budget |
|---|---|
| Keystroke → visible echo | < 10 ms |
| Pane focus/tab switch | < 5 ms |
| Screen switch (F-key) | < 30 ms (pre-built layouts, cached data) |
| Full-terminal diff flush | < 3 ms typical, < 8 ms worst (resize) |
| Map pan step | < 8 ms (viewport re-cull only) |
| Delta apply (typical month) | < 15 ms budgeted off the input thread |
| Memory (UI process) | < 150 MB at any city size — the UI holds views, never the world |

Headless UI tests drive the widget layer with scripted key sequences against recorded delta streams and assert cell-buffer snapshots — the UI gets the same regression rigour as the sim.

## 6. Additions to M0 scope
The protocol gains **view subscriptions** (UI subscribes to named projections of state: "junction 14 approaches", "F2 ledger", "viewport r=…"; engine pushes deltas only for live subscriptions — this is also exactly the remote-play seam) and a **layout/profile JSON schema** (keymaps are remappable; the grammar above is the default binding set, stored as data).


# III-C · Engineering & Runtime Plan (M0)


## 1. Target hardware & the resource doctrine

Reference machine: **Intel i7 (8C/16T class), 20 GB RAM available to us, NVIDIA RTX 3050-class GPU (~2,560 CUDA cores, 4 GB VRAM)**, Windows, Windows Terminal.

Doctrine: *the game may use everything, but the interface may never feel it.* The UI's fluidity budget (UI-SPEC §5) is inviolable; simulation eats whatever is left. Since we have no graphics pipeline, the GPU is a **compute asset**, not a display asset — it is the *local* tier of the solver-offload seam (GDD §15): the same solver interface will have three interchangeable backends — **CPU (v1, always works) → GPU sidecar (local acceleration) → cloud (Azure)**. One seam, three muscles. Do not entangle CUDA with the engine.

### 1.1 Process & thread topology

```
metropolis.exe
├── UI PROCESS-DOMAIN (main goroutines)
│   ├── T-INPUT     tcell event poll → input channel        (never blocks, ever)
│   ├── T-RENDER    10Hz UI tick + on-input render; sole owner of the screen buffer
│   └── T-VIEWS     delta subscription client: applies engine deltas to view models
│                   (double-buffered view models; T-RENDER reads front, T-VIEWS writes back)
├── ENGINE DOMAIN (in-process v1; identical over gRPC later)
│   ├── T-ENGINE    tick orchestrator: runs the phase pipeline, owns world state
│   ├── POOL-SIM    worker pool, N = runtime.NumCPU()-2 (leave 2 for UI+OS)
│   │               data-parallel within each phase over FIXED shards (see 1.2)
│   ├── T-SUBSCR    view-subscription server: computes/pushes deltas per UI subscription
│   └── T-PERSIST   snapshot writer: copy-on-write save marshalling off the tick path
├── SIDE PROCESSES (optional, same solver gRPC contract)
│   ├── solver-gpu.exe   C++/CUDA sidecar (traffic assignment, cold-pass batch) — LATER
│   └── cloud endpoints  Azure (GDD §15) — LATER
└── T-MISC      log writer, BoW telemetry hook, Blob sync (all fire-and-forget queues)
```

**Rules Claude Code must not break:**
- tcell screen access is **single-goroutine** (T-RENDER). All widget draws happen there. T-INPUT only translates events to messages.
- The engine **owns** world state. The UI holds *view models* only, built from deltas. No shared memory between domains — channels/protocol only, even in-process. This is what keeps the gRPC flip a config change.
- The tick path (T-ENGINE + POOL-SIM) allocates from **pre-sized arenas/slices**; steady-state monthly tick target is **zero heap allocation** on hot paths. GC pauses are the enemy of "solid fluid fast" — starve the GC and it cannot hurt us. Set `GOGC=200`, verify with `GODEBUG=gctrace=1` in the perf harness.
- Nothing in the engine ever calls wall-clock time for logic. Simulation time is the only time.

### 1.2 Deterministic parallelism (the crown rule, spelled out)

Same seed + same command log ⇒ **bit-identical** world, regardless of worker count, on any machine. Parallelism must therefore be *structured*, never opportunistic:
1. World is partitioned into **256 fixed shards** (spatial for cells/network, id-hash for citizens/firms). 256 is constant forever — never derived from core count. Workers *steal shards*; results are **merged in shard order 0→255** at each phase barrier.
2. Every phase is a barrier: phase k+1 reads only phase-k-committed state. Within a phase, shards write only shard-local scratch; cross-shard effects are emitted as **messages routed and applied in (shard, sequence) order** at the barrier.
3. RNG: counter-based (Philox-style) streams keyed `(worldSeed, entityId, month, purposeTag)` — draws are position-independent and order-free. **No shared RNG object anywhere.**
4. Money is **int64 micro-pounds**. Simulation aggregates that must sum across shards use int64 or fixed-point; where float64 is unavoidable (physics-ish diffusion), summation is performed in fixed shard order. Never range over a Go map on the tick path — iteration order is nondeterministic; use sorted keys or slices.
5. CI runs the **determinism gate** on every merge: same seed, 120 months, twice, `sha256(worldSnapshot)` must match; then again with `POOL-SIM=1` vs `=14`. A mismatch fails the build. This test is written FIRST, in M1 week one, against the walking-skeleton world.

### 1.3 Memory budget (20 GB envelope)

| Region | Budget | Notes |
|---|---|---|
| Citizen shards (hot+warm resident) | 8 GB | ~250 B/citizen ⇒ ~32 M resident; beyond that cold shards page to disk (mmap), LRU |
| World cells + networks + route cache | 4 GB | route cache is the big line; capped LRU, deterministic *contents-independent* (cache only affects speed, never results — assert in CI) |
| Firms, markets, logistics state | 1.5 GB | |
| Scratch arenas (phase-local) | 2 GB | pre-sized at world-load from world dimensions |
| UI process domain | 0.15 GB | UI-SPEC §5, holds views never world |
| Snapshot COW headroom | 2 GB | T-PERSIST marshals while sim continues |
| OS + slack | ~2 GB | |

GPU (when the sidecar lands): road graph + OD matrices + flow vectors ≪ 4 GB VRAM at full 60×60 km. The sidecar speaks the solver gRPC contract; the engine cannot tell CPU from GPU from cloud except by latency. **v1 ships CPU-only** — the sidecar is a BoW module with a dependency on the frozen solver contract.

## 2. Harness strategy — building without the whole game

Nothing exists on day one; everything must still run, demo, and test. Four harnesses, all permanent fixtures (they never get deleted; they become the test estate):

1. **H-STUB — StubEngine.** Implements the full engine protocol with canned behaviour: serves a static handcrafted world ("Folkestone-64", a 64×64 fixture), accepts commands, replies with scripted/recorded delta streams, obeys speed controls with fake ticks. **The UI is built against H-STUB from week one** — every screen, every widget, the whole key grammar, before one line of real simulation exists. Also includes chaos knobs (delayed deltas, burst deltas) to prove UI budgets under stress.
2. **H-REPLAY — recorded fixtures.** Delta streams and command logs recorded from any engine (stub or real) into `fixtures/*.ndjson.gz`; replayable into the UI (visual/UX work on stable data) and into the engine (regression: replay commands, compare snapshots). The save format IS the fixture format — one serialisation to rule them all.
3. **H-HEADLESS — engine without UI.** `metropolis -headless -seed N -months M -out snap.json` plus scenario scripts (JSON command lists). This is the balance/Batch/CI workhorse (GDD §16 M2). Emits per-phase timing + invariant reports every tick.
4. **H-SYNTH — synthetic world generator.** Parametric cities (population, sprawl, network shape) for perf/scale testing: we must know the 10 M-citizen tick cost in month 3 of development, not month 30. Perf CI graphs tick-time vs scale per commit.

**Module stubbing inside the real engine:** every simulation module registers behind an interface with a mandatory `Stub` implementation (e.g., `TrafficStub` returns free-flow times; `CrimeStub` returns zeros). The engine boots with ANY mix of real/stub modules — so the walking skeleton runs the full pipeline end-to-end from the first month of development, and modules go real one at a time. Which mix is running is visible in the info panel (§3) and toggleable in debug. *Definition of "static until the back end is built": UI on H-STUB, then UI on real engine with all-stub modules, then modules turn real in BoW priority order.*

## 3. Debug mode & the Info Panel — a first-class feature

Debug is a **runtime feature switch**, not a build flavour (release builds carry it, default off): enable via `--debug`, config, or `:debug on` (palette). Switching ON is recorded in the save header (debug-touched saves are flagged forever — balance data hygiene).

**F12 Info Panel** (visible whenever debug is on; pane layout per UI-SPEC):
- **Build & code:** version (`git describe --tags --dirty`), commit hash, branch, build UTC timestamp, Go version, build host — all injected via `-ldflags` at build; NEVER hand-maintained.
- **Runtime:** uptime, sim date vs real elapsed, speed, tick number; memory (heap in-use, sys, GC count/pause p99, arena occupancy vs budget table §1.3), goroutine count, channel depths (input, delta, persist queues), FPS-equivalent (UI tick actuals vs 10 Hz), input-echo latency p99.
- **Module registry (the heart):** one row per registered module — name · semver · **status (real/stub/off)** · health (ok/degraded/error) · last-tick cost µs · feature-flag source — with **UI-driven ON/OFF/STUB toggles** where the module declares it safe (`CanToggle`), guarded by a confirm. Toggling emits a world-event (ticker in debug: "*Crime module → STUB*").
- **Error log tail:** last 50 warn/error entries, live, `Enter` opens the full log; logs are structured JSON (`logs/engine.ndjson`, `logs/ui.ndjson`, rotated) so the panel just tails and pretty-prints.
- **Phase timing strip:** per-phase µs sparkline across last 60 ticks — the profiler you glance at.
- Debug also unlocks: 8× speed, cheats (GDD §14), console `` ` ``, fixture record/replay controls, and `:bow` quick-log (below).

Implementation note for Claude Code: the module registry is the same mechanism the engine uses to boot (§2 stubbing) — the panel is a *view* of it, not a parallel system. One registry, two consumers.

## 4. The Book of Work — local MariaDB

Everything to be built is logged here; Claude Code keeps it current as it works (see §6 working agreement). Local MariaDB (native service or docker-compose in `tools/bow/`), database `metropolis_bow`. A tiny Go CLI **`bow`** (in `tools/bow/`) wraps common operations so updates are one-liners and scriptable from commit hooks.

```sql
-- schema.sql  (MariaDB 10.11+, utf8mb4)
CREATE TABLE modules (
  id            INT AUTO_INCREMENT PRIMARY KEY,
  mkey          VARCHAR(64) NOT NULL UNIQUE,      -- 'traffic', 'crime', 'ui.map'
  name          VARCHAR(200) NOT NULL,
  description   TEXT,
  spec_ref      VARCHAR(200),                     -- 'GDD §19; UI-SPEC §2'
  version       VARCHAR(32)  NOT NULL DEFAULT '0.0.0',
  status        ENUM('planned','stub','building','built','frozen') NOT NULL DEFAULT 'planned',
  owner         VARCHAR(64)  NOT NULL DEFAULT 'claude-code',
  created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  updated_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
);

CREATE TABLE features (
  id            INT AUTO_INCREMENT PRIMARY KEY,
  module_id     INT NOT NULL,
  fkey          VARCHAR(64) NOT NULL UNIQUE,      -- 'traffic.equilibrium', 'ui.whichkey'
  title         VARCHAR(200) NOT NULL,
  description   TEXT,
  spec_ref      VARCHAR(200),
  milestone     ENUM('M0','M1','M2','M3','M4','future') NOT NULL DEFAULT 'M1',
  priority      ENUM('P0','P1','P2','P3') NOT NULL DEFAULT 'P2',
  status        ENUM('backlog','planned','in_progress','blocked','done','parked') NOT NULL DEFAULT 'backlog',
  estimate_days DECIMAL(5,1),
  FOREIGN KEY (module_id) REFERENCES modules(id),
  created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  updated_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
);

CREATE TABLE bugs (
  id            INT AUTO_INCREMENT PRIMARY KEY,
  module_id     INT NOT NULL,
  feature_id    INT NULL,
  title         VARCHAR(200) NOT NULL,
  description   TEXT,                              -- include repro steps
  severity      ENUM('S1_crash','S2_wrong','S3_degraded','S4_cosmetic') NOT NULL,
  priority      ENUM('P0','P1','P2','P3') NOT NULL DEFAULT 'P2',
  status        ENUM('open','triaged','fixing','fixed','verified','wontfix','duplicate') NOT NULL DEFAULT 'open',
  found_version VARCHAR(64),                       -- git describe
  fixed_version VARCHAR(64),
  determinism   BOOLEAN NOT NULL DEFAULT FALSE,    -- flags §1.2 violations: always P0
  FOREIGN KEY (module_id) REFERENCES modules(id),
  FOREIGN KEY (feature_id) REFERENCES features(id),
  created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  updated_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
);

CREATE TABLE dependencies (                        -- module→module and feature→feature
  id            INT AUTO_INCREMENT PRIMARY KEY,
  from_type     ENUM('module','feature') NOT NULL,
  from_id       INT NOT NULL,
  to_type       ENUM('module','feature') NOT NULL,
  to_id         INT NOT NULL,
  dep_kind      ENUM('requires','blocks','relates') NOT NULL DEFAULT 'requires',
  note          VARCHAR(500),
  UNIQUE KEY uq_dep (from_type, from_id, to_type, to_id, dep_kind),
  created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE notes (                               -- long-form design notes on any entity
  id            INT AUTO_INCREMENT PRIMARY KEY,
  entity_type   ENUM('module','feature','bug') NOT NULL,
  entity_id     INT NOT NULL,
  author        VARCHAR(64) NOT NULL,
  title         VARCHAR(200),
  body          MEDIUMTEXT NOT NULL,
  created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  INDEX ix_notes_entity (entity_type, entity_id)
);

CREATE TABLE comments (                            -- threaded discussion on any entity
  id                INT AUTO_INCREMENT PRIMARY KEY,
  entity_type       ENUM('module','feature','bug','note') NOT NULL,
  entity_id         INT NOT NULL,
  parent_comment_id INT NULL,
  author            VARCHAR(64) NOT NULL,
  body              TEXT NOT NULL,
  FOREIGN KEY (parent_comment_id) REFERENCES comments(id),
  created_at        TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  INDEX ix_comments_entity (entity_type, entity_id)
);

CREATE TABLE status_history (                      -- audit: who moved what, when
  id            INT AUTO_INCREMENT PRIMARY KEY,
  entity_type   ENUM('module','feature','bug') NOT NULL,
  entity_id     INT NOT NULL,
  field         VARCHAR(32) NOT NULL,              -- 'status','priority','milestone'
  old_value     VARCHAR(64), new_value VARCHAR(64),
  author        VARCHAR(64) NOT NULL,
  created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  INDEX ix_hist_entity (entity_type, entity_id)
);

-- Working views
CREATE VIEW v_ready_to_build AS                    -- planned features whose requires-deps are all done
  SELECT f.* FROM features f
  WHERE f.status IN ('backlog','planned') AND NOT EXISTS (
    SELECT 1 FROM dependencies d
    JOIN features f2 ON d.to_type='feature' AND d.to_id=f2.id
    WHERE d.from_type='feature' AND d.from_id=f.id
      AND d.dep_kind='requires' AND f2.status <> 'done');

CREATE VIEW v_blocked AS
  SELECT f.*, GROUP_CONCAT(f2.fkey) AS blocking_features
  FROM features f
  JOIN dependencies d ON d.from_type='feature' AND d.from_id=f.id AND d.dep_kind='requires'
  JOIN features f2 ON d.to_type='feature' AND d.to_id=f2.id AND f2.status<>'done'
  GROUP BY f.id;
```

**Seeding:** `tools/bow/seed.sql` is generated from the design corpus — one module per engine subsystem and UI screen (~45 modules), features extracted per GDD chapter with `spec_ref` pointing at the exact §. Claude Code's first BoW task is generating and reviewing this seed. `bow` CLI verbs: `bow ls ready|blocked|bugs`, `bow start <fkey>`, `bow done <fkey>`, `bow bug add ...`, `bow note add ...`, `bow dep add A requires B`. The F12 info panel (debug) gains a read-only BoW tab (open counts by priority, what's in_progress) — the game knows its own backlog.

## 5. Git — repository & conventions

- **Layout:** monorepo `metropolis/`: `cmd/metropolis/`, `cmd/bow/`, `internal/engine/<module>/…`, `internal/ui/…`, `internal/protocol/`, `data/*.json` (GDD config files), `fixtures/`, `tools/`, `docs/` (this corpus lives IN the repo — specs are versioned with code), `sql/`.
- **Branching:** trunk-based. `main` is always green (CI: build, unit, determinism gate §1.2, UI snapshot tests, invariant suite, perf smoke). Short-lived branches `feat/<fkey>`, `fix/bug-<id>`; squash-merge; tags `v0.<milestone>.<n>` at milestone cuts.
- **Commits:** Conventional Commits with BoW refs — `feat(traffic): equilibrium inner loop [traffic.equilibrium]`, `fix(ui.map): heatmap palette clamp [bug-214]`. A commit-msg hook validates the ref exists in BoW and auto-comments the commit hash onto the entity (via `bow`). Every determinism-affecting change notes it in the body with `DETERMINISM:` line.
- **Hygiene:** `go vet`, `golangci-lint`, `gofmt` enforced pre-commit; no binaries in repo; fixtures compressed; saves never committed except curated fixtures.

## 6. Working agreement for Claude Code (read this twice)

1. **Spec is law, BoW is state.** Before building anything: read the feature's `spec_ref` sections in full. If code must deviate from spec, STOP — write a BoW note explaining why, mark the feature `blocked`, surface it for Aaron. Never silently drift.
2. **Order of work:** always from `v_ready_to_build`, priority then milestone order. Never start a `blocked` item; fix the dependency or escalate.
3. **Definition of Done (all of):** code + tests (unit; determinism-relevant modules also add a shard-count invariance test); module registered in the registry with correct status; stub counterpart still passes (stubs are maintained forever); info-panel row shows sane health; docs comment in code header pointing at spec §; BoW feature → `done` with a closing comment; conventional commit referencing the fkey.
4. **Walking skeleton first (M1 order):** protocol + registry + stub-everything engine + H-STUB + determinism gate + F1 map on fixture — a playable-looking nothing, end-to-end, before any real model. Then modules go real in BoW order.
5. **Perf is a test, not a hope:** H-SYNTH perf runs in CI; a commit that regresses monthly-tick time >10% at the 1M-citizen synthetic fails. UI budgets (UI-SPEC §5) are asserted in the headless UI harness.
6. **When in doubt, log it:** anything odd — a flaky test, a suspicious float, an unclear spec line — becomes a BoW bug or note immediately. The database is the project's memory; the chat is not.


---

# PART IV — APPENDIX A · BUILDING & OBJECT CATALOGUE
*(Supplements 1–3 appear inline at the ends of chapter sets §25–§35, §36–§44 and §45–§55 above.)*


## R — Roads & Paths
| Object | Unlock | Cost/km | Notes |
|---|---|---|---|
| Footpath | M1 | 20k | walk/cycle only |
| Dirt track | M1 | 40k | 30km/h, degrades in winter |
| Gravel road | M2 | 90k | |
| Residential street | M3 | 250k | 30 km/h, on-street parking |
| Two-lane road | M3 | 400k | 40 km/h |
| Avenue (2+2, parking) | M5+DP | 900k | |
| Bus-lane road | M5+DP | 650k | transit priority |
| Cycle-lane street | M4+DP | 300k | health engine |
| Dual carriageway | M7+DP | 2.2M | 70 km/h |
| Motorway extension | M8+DP | 6M | ties into M20 |
| Bridge (road) | M5+DP | 4M/crossing | span cost × width |
| Tunnel (road) | M8+DP | 15M/km | escarpment piercer |
| Pedestrianised high street | M7+DP | 500k | High-street retail group; retail appeal++ |
| Junction controls: signals / mini-rbt / roundabout / grade-separated | M3/M3/M4/M8 | 80k–8M | §19.2 |

## E — Electricity
| Object | Unlock | Cost | Output/Cap | Notes |
|---|---|---|---|---|
| Diesel genset | M1 | 120k | 0.5 MW | dear fuel, noisy, dirty |
| Sellindge grid tranche | M4+£ | 2M+standing | 10 MW/tranche | import price/MWh |
| Small wind turbine | M3+DP | 300k | 0.3 MW avg | escarpment wind bonus |
| Wind farm (onshore) | M5+DP | 4M | 8 MW avg | |
| Offshore wind array | M9+DP | 60M | 80 MW avg | needs port |
| Solar farm | M4+DP | 2M | 3 MW avg | seasonal, land-hungry |
| Rooftop solar policy | M7 (policy) | subsidy | dist. | |
| Battery storage | M6+DP | 3M | 20 MWh | peak shaving |
| Gas peaker | M6+DP | 8M | 25 MW | needs gas net |
| CCGT gas plant | M7+DP | 45M | 150 MW | |
| Waste incinerator w/ energy | M6+DP | 30M | 15 MW + waste sink | §G |
| Nuclear station (Dungeness II-and-a-half) | M10+DP+ach | 900M | 1,200 MW | decade build time |
| Substation / HV line | with sources | 500k/1M km | — | network |
| Fusion pilot | M12+ach | 2B | 2,000 MW | endgame |

## W — Water, Wastewater & Gas
| Object | Unlock | Cost | Cap | Notes |
|---|---|---|---|---|
| Well | M1 | 30k | 20 m³/d | |
| Chalk borehole | M1+DP | 150k | 400 m³/d | aquifer yield ceiling shared |
| Water tower | M2 | 200k | 1k m³ store | pressure/buffer |
| Bulk water contract (road tanker) | M1+£ | per m³ | small | emergency/early |
| Water treatment works | M4+DP | 3M | 10k m³/d | needed as city scales |
| Reservoir (valley dam) | M6+DP | 25M | seasonal store | terrain-sited |
| Bulk supply pipeline (off-map) | M7+£ | 12M+per m³ | 30k m³/d | |
| Desalination plant | M10+DP | 80M | 50k m³/d | energy-hungry (§17.2) |
| Septic field | M1 | 40k | small | pollutes if dense |
| Sewage works (small→regional) | M4/M6/M8+DP | 2M–40M | 8k–200k m³/d | river/sea outfall rules |
| Storm drains | M5+DP | 300k/km | flood defence | shore storm events |
| Gas pipeline tranche (off-map) | M5+£ | 3M+per kWh | 200 MWh/d | optional network |
| Gas mains | M5 | 250k/km | — | |
| LNG terminal | M9+DP | 50M | port-fed gas | needs port |

## H — Health & Deathcare
| Object | Unlock | Cost | Cap | Notes |
|---|---|---|---|---|
| First-aid post | M1 | 80k | 30 visits/d | |
| Clinic | M2+DP | 600k | 150 visits/d | +counselling room upgrade (mental) |
| Pharmacy | M3 | 200k | retail-health hybrid | |
| Small hospital | M6+DP | 12M | 120 beds | waiting-list mechanics |
| General hospital | M7+DP | 45M | 500 beds | +mental-health unit upgrade |
| Teaching hospital | M9+DP+univ | 120M | 1,200 beds | boosts health citywide |
| Ambulance station | M5+DP | 1.5M | 6 units | response time on real roads |
| Air ambulance pad | M8+DP | 4M | 1 unit | beats traffic |
| Cemetery | M2 | 300k | 2k plots | fills permanently — land pressure |
| Crematorium | M5+DP | 3M | 12/d | |
| Memorial woodland | M6+DP | 1M | 5k | park hybrid |
| Elder-care home | M4+DP | 2.5M | 60 residents | aging wave demand |
| Home-care service | M6+DP | opex | district | cheaper than beds |

## ED — Education
| Object | Unlock | Cost | Cap | Notes |
|---|---|---|---|---|
| One-room school | M2 | 250k | 60 pupils | |
| Primary school | M3+DP | 1.2M | 240 | Sept intake |
| Secondary school | M4+DP | 4M | 900 | |
| Sixth-form college | M5+DP | 3M | 500 | further ed |
| Technical college | M6+DP | 6M | 800 | trade skills → industry |
| University campus | M7+DP | 40M | 8k students | research bonus; halls demand |
| University expansion faculties | M8+DP | 15M ea | +4k | med/eng/arts flavours |
| Library | M4+DP | 800k | district | small edu+mental boost |
| Adult education centre | M6+DP | 1M | 400 | re-skills unemployed |
| Nursery | M3+DP | 400k | 80 | enables both-parent work |

## F/P — Fire, Police & Justice
| Object | Unlock | Cost | Notes |
|---|---|---|---|
| Volunteer fire post | M2 | 150k | slow response |
| Fire station | M4+DP | 2M | 4 appliances |
| Regional fire HQ | M7+DP | 8M | +hazmat (industry) |
| Fire hydrant network | M4 | 50k/km | needs water pressure |
| Police desk | M2 | 100k | |
| Police station | M4+DP | 1.8M | patrol radius, real roads |
| Divisional HQ | M7+DP | 7M | detectives: cuts crime persistence |
| Courthouse | M7+DP | 5M | required past 100k |
| Prison | M8+DP | 20M | or pay off-map remand £ |

## G — Garbage & Recycling
| Object | Unlock | Cost | Cap | Notes |
|---|---|---|---|---|
| Tip (small landfill) | M1 | 100k | fills, leaches | |
| Landfill site | M4+DP | 1.5M | 50 t/d | land value hit radius |
| Transfer station | M5+DP | 2M | routes trucks | |
| Recycling centre | M6+DP | 6M | 40 t/d | sells materials |
| Incinerator w/ energy | M6+DP | 30M | 80 t/d | shared with §E |
| Composting works | M5+DP | 1M | food waste → farm input | loop |
| Export contract (road) | M1+£ | per t | relief valve | dear |

## PK — Parks, Recreation & Nature
| Object | Unlock | Cost | Notes |
|---|---|---|---|
| Pocket park | M2 | 60k | green-space 400m rule (§18) |
| Playground | M3 | 90k | family appeal |
| Village green | M2 | 120k | community events |
| Community centre | M4+DP | 700k | sociability/isolation counter |
| Allotments | M3+DP | 80k | food micro + wellbeing |
| Town park | M5+DP | 1.5M | |
| Botanical garden | M7+DP | 6M | aesthetic-drive appeal |
| Country park (escarpment) | M6+DP | 2M | uses steep land! |
| Coastal promenade | M5+DP | 1M/km | shore cells; Beach pack |
| Pier | M7+DP | 8M | leisure landmark |
| Beach management (lifeguards, cleaning) | M5 | opex | summer leisure driver |
| Nature reserve | M6+DP | 500k | caps land value, boosts adjacency |
| Marina | M8+DP | 12M | wealth magnet; waterfront transport group |
| City park (large) | M8+DP | 10M | |
| Zoo | M8+DP | 15M | |
| Aquarium | M8+DP | 12M | coastal |
| Theme park | M9+DP+ach | 60M | external visitors |
| Camp site | M3 | 150k | early tourism |
| Dog park | M4 | 70k | |
| Skate park | M5 | 120k | youth novelty-seekers |

## L — Leisure, Sport & Culture (personality-patronised, §5.1)
| Object | Unlock | Cost | Cap | Draws (taste axes) |
|---|---|---|---|---|
| Pub | M2 | 200k | 80 | sociability |
| Café / restaurant row | M3 | 150k+ | | dining |
| Cinema | M5+DP | 2M | 600 | novelty |
| Theatre | M7+DP | 8M | 900 | aesthetic |
| Concert hall | M8+DP | 25M | 2.2k | aesthetic/sociability |
| Arena | M9+DP | 60M | 12k | events |
| Nightclub district | M7 (policy) | — | | novelty/sociability; noise! |
| Bingo hall | M5 | 400k | 300 | patience/community |
| Gaming lounge / esports hub | M7+DP | 1M | 200 | gaming |
| Museum | M6+DP | 5M | | aesthetic/patience |
| Art gallery | M7+DP | 3M | | aesthetic |
| Sports pitch | M3 | 100k | | physicality |
| Leisure centre + pool | M5+DP | 4M | 800/d | physicality |
| Climbing wall | M6 | 900k | 150/d | physicality/novelty |
| Golf course | M7+DP | 6M | land-hungry | patience/wealth |
| **Football ground (non-league)** | M4+DP | 800k | 1.5k | community identity starts |
| **Football stadium (league)** | M7+DP+ach | 25M | 18k | matchday logistics events! |
| **Grand stadium** | M9+DP+ach | 150M | 60k | city reputation++; transit stress test |
| Racecourse | M8+DP | 30M | 20k | Folkestone had one — ach nod |
| Ice rink | M7+DP | 5M | | |

## T — Transport
| Object | Unlock | Cost | Notes |
|---|---|---|---|
| Bus stop / shelter | M4 | 5k/15k | |
| Minibus depot (demand-responsive) | M4+DP | 500k | pre-network transit |
| Bus depot | M5+DP | 2M | fixed routes, player-drawn |
| Bus station | M6+DP | 3M | interchange |
| Coach station (external) | M5+£ | 1.5M | London/Ashford commuter coaches |
| Taxi rank / fleet licence | M5+DP | 300k+ | fleet size setting |
| Cycle-hire scheme | M6+DP | 800k | mode-shift lever |
| Park & Ride | M7+DP | 2.5M | edge parking + shuttle |
| Tram depot + track | M8+DP | 8M + 3M/km | street-running |
| **External rail station** | M5+£ | 6M | the dormitory-town unlock (§21) |
| Rail line + internal station | M8+DP | 5M/km + 4M | through/terminus variants (rail stations group) |
| Grand terminus | M9+DP | 30M | interchange hub |
| **Metro (tube) tunnel + station** | M10+DP | 25M/km + 15M | 30k pax/h/dir |
| Underground interchange | M10+DP | 40M | |
| Freight rail yard | M8+DP | 12M | bulk import artery #3 |
| Ferry pier / terminal | M7+DP | 2M/8M | post-port coastal hops |
| Heliport | M9+DP | 5M | |
| **Regional airport** | M9+DP+ach | 200M | external only; noise contour |
| International terminal | M11+DP | 500M | migration inflow++ |
| Multi-storey car park | M6+DP | 3M | parking is land (§19.1) |
| Automated logistics hub | M12+DP | 300M | JIT endgame |

## PT — Port & Coastal (Waterfront Transport)
| Object | Unlock | Cost | Notes |
|---|---|---|---|
| Fishing quay | M6+DP | 1M | fresh food source |
| Fishing harbour | M7+DP | 6M | fleet scale |
| **Cargo port (small)** | M7+£+ach | 40M | import artery #2 |
| Container terminal | M9+DP | 150M | 10× bulk |
| Ferry terminal (external) | M8+£ | 25M | passenger route to continent |
| Shipyard | M9+DP | 60M | industry chain |
| Drawbridge | M8+DP | 15M | harbour access |
| Lighthouse | M7 | 800k | shipping safety + landmark |
| Sea wall / storm barrier | M6+DP | 2M/km | storm-surge defence |
| Land reclamation | M9+DP | 20M/ha | makes flat land — the ultimate answer |
| Breakwater | M7+DP | 5M | enables harbour siting |
| Beach house zone | M6+DP | zoning | Coastal shoreline group; premium shore living |

## HS — Housing Typologies (zoned; appeal profiles §21)
| Type | Unlock | Density (hh/ha) | Profile sketch |
|---|---|---|---|
| Mobile-home park | M1 | 25 | cheap entry |
| Cottage | M1 | 8 | |
| Farmhouse | M1 | 1 | with farm plots |
| Terrace | M3 | 45 | community families |
| Semi-detached | M3 | 25 | |
| Detached | M4 | 12 | garden water +, wealth |
| Bungalow | M4 | 14 | retirees, coastal |
| Beach house | M6+DP | 10 | premium, shore only |
| Mansion plot | M7 | 2 | wealth magnet |
| Student halls | M7+univ | 400 beds/block | |
| Low-rise flats | M5 | 90 | |
| Mid-rise flats | M7+DP | 180 | |
| High-rise | M8+DP | 350 | High-rise pack tier |
| Penthouse tower | M9+DP | 300+premium | novelty/wealth |
| Co-living block | M9+DP | 500 | young singles |
| Retirement complex | M6+DP | 120 | care-integrated |
| Mega-density residential | M11+DP | 1,200 | the 100M enabler |

## C/I — Commerce, Industry & Offices (zoned + signature)
| Object | Unlock | Notes |
|---|---|---|
| Corner shop / general store | M1 | |
| High-street retail | M3 | |
| Supermarket | M5+DP | JIT anchor customer |
| Retail park | M7+DP | car-dependent |
| Market hall | M4+DP | fresh-food distribution |
| Victorian office chambers | M5 | Office tiers group t1 |
| Office block | M7+DP | t2 |
| Glass HQ tower | M9+DP | t3; finance sector |
| Farm plots / market garden | M1/M2 | harvest calendar |
| Vertical farm | M12+DP | season-proof food |
| Light industrial units | M5+DP | |
| Heavy industry estate | M7+DP | pollution radius |
| Quarry (chalk/aggregate) | M4+DP | construction materials |
| Cement works | M7+DP | |
| Warehouse (small→automated) | M3/M5/M12 | buffer capacity (§8) |
| Data centre | M9+DP | power-hungry, few jobs, big rates |

## LM — Landmarks & Uniques (ach-gated, one each)
| Object | Condition | Effect |
|---|---|---|
| War memorial | first 100 deaths | community + |
| Town hall → City hall → Metropolitan hall | M3/M7/M9 | governance capacity (policies slots) |
| Clock tower | M5 + funded arts | aesthetic landmark |
| Cathedral | M8 + community high | reputation + |
| Observation tower | M9 | tourism |
| Convention centre | M9+DP | business visitors |
| "The Folkestone Eye" coastal wheel | M8 + promenade | leisure landmark |
| Grand library | M8 + all edu tiers built | education quality + |
| Science centre | M9 + university research | research + |
| Winter gardens | M7 | seaside heritage nod |
| Broadcasting house | M8+DP (comms tree) | reputation reach |
| **Channel Tunnel portal** | M11 (future-dev slot) | reserved |

## CM/WF — Communications & Welfare
| Object | Unlock | Notes |
|---|---|---|
| Post & parcel depot | M4+DP | last-mile freight on real roads |
| Telephone exchange → broadband hub → fibre backbone | M5/M7/M9+DP | office/data-centre prerequisite; remote-work modifier on commuting |
| Mast network | M6+DP | coverage overlay |
| Welfare office | M5+DP | unemployment support (mental health floor) |
| Food bank | M5 | shortage shock absorber |
| Housing office | M6+DP | homelessness prevention |
| Job centre | M5+DP | matching speed + |
| Citizens advice | M6 | satisfaction + |

---

# PART V — CROSS-CHECK & OPEN ITEMS

## V.1 Coverage verification
Every requirement raised in planning, traced to its LLD home:

| Requirement (as raised) | Covered in |
|---|---|
| Land price/purchase, starting money, loans, allocation screens | §7, §13, §39 |
| Folkestone topography, motorway + junction, turnaround loops, wiped landscape, sea | §2 |
| Sellindge grid & Dungeness lore; gas pipeline; bulk water | §2.2, §W, §17 |
| Turn-based/'Blue' speeds 1-2-3, month=day-cycle, save points, pacing | §3 |
| Village→megacity→100M; era/milestone progression; XP/dev-points/buy unlocks | §4, §22 |
| Individuals ("carbon units" → Option B), personality, leisure tastes, education shaping, scattered birthdays, no culls | §5, §27 |
| Food, water, all services import-or-make; JIT, queues, warehouses, junction slots | §6, §8 |
| Projections & attention pointers | §13 (F7, alerts), UI-SPEC §4 |
| Seasons & harvest | §9, §31 |
| Detroit spiral, insolvency, ghost-city ending | §12 |
| Consumption tables (person/school/etc.; water, gas, wastewater, electric, food, waste) | §17 |
| Wellbeing, mental & physical health; commute time | §18, §42 |
| Routing without 'Blue's flaws; jams, bottleneck learning, lane myth; signals/roundabouts; road names, endpoints, route-taken inspection | §19, §20, §51 |
| Trains, tube, bus, minibus, taxi, bicycle, motorbike, walking; London commuting; housing type variety | §19.1, §21 |
| Refuse rounds & waste-health; dispatch (fire/police/ambulance/air ambulance); education lifecycle incl. universities | §25–§27 |
| Crime types, gangs, more-police-less-crime curve, police HQ, MI5-analogue; prison & rehabilitation | §28, §43 |
| News bulletin/ticker/annual/epilogue | §29 |
| Coastal boat arrivals | §30 |
| Farming: crop choice, fertilised vs organic, dairy & meat, biodiversity engine | §31 |
| Mining types; noise + seen/heard blight; reclamation; sea fishing; harbour freight in tonnes; raw→commodity chains; 'Blue' land-type zoning | §32–§34 |
| Telecoms, internet, cellular, post, parcels, Amazon-style fulfilment | §35 |
| Service capacity export incl. toxic waste; grocery shopping; car parking; fine-grain tax; social services; café culture; spare-time/exploration; tourism | §36–§44 |
| Entrepreneur SME→enterprise; accounts/law/insurance/banking/central banking; white & blue collar demand | §45 |
| Multinational incentives (Pfizer, Intel, Dell, ICI, steel, Tesla, Siemens, Airbus); rail yards/works/terminals | §46–§47 |
| Center Parcs & Bluewater archetypes | §48 |
| Fuel for cars/vans/trucks; EV charging | §49 |
| Oils, rubber, plastics, refinery, chemical pipe network | §50 |
| Road types, in-place upgrade, rebuild cost, roadworks | §51 |
| Policies best-practice (cycle paths, truck bans, truck parks, P&R, interconnection); named zones (CBD, science park, tax-free harbour) | §52 |
| Hyperloop & TBM tunnels | §53 |
| Top-down tax; civil servants as net cost; municipality/planning quality; childcare; benefits, social housing, dentists/opticians; Public Service Pie | §54, Supp.3 |
| Defence branches (bio, nuclear-eng, air, submarine, surface, tank, infantry), mandates at thresholds, central grants | §55 |
| 'Blue' expansions content (waterfront transport, coastal shoreline, high-rise, stations, offices) | §23, catalogue |
| LHC / SpaceX / Heathrow-class megabuilds; auto-naming of every object | Supp.2 §MP, §20 |
| Front/back separation, cloud (Blob/Batch/solver offload/AI-surrogate), GPU sidecar, threading, 20GB/i7/RTX budget | §15, M0-ENG §1 |
| Harness & static-first build; /debug feature switch + info panel (version, logs, memory, modules, on/off) | M0-ENG §2–§3 |
| Book of Work MariaDB (modules, features, bugs, priority, notes, comments, dependencies); Git; Claude Code directions | M0-ENG §4–§6 |
| Rich text UI: diagrams, graphs, dashboards, tab-driven learned key sequences, smooth/fast | III-B entire |

**Explicitly out of scope v1 (decided or by omission, now recorded):** multiplayer/shared worlds (architecture-ready, shelved); dynamic world market & finite migration pool (interfaces ready); Channel Tunnel content (slot reserved); audio (none — terminal game); localisation (English only v1); mod support beyond the JSON data files (which are, de facto, the modding surface); LLM soft-layer (optional online feature, post-v1).

## V.2 Open items for M0 completion
1. **Protocol schema** — the concrete command/event/delta message definitions and view-subscription contract (the one remaining pre-skeleton artefact).
2. **Save/fixture schema** — NDJSON shard layout, header, versioning/migration rules.
3. **BoW seed** — generate `seed.sql` from this document (~45 modules, features per §, spec_refs).
4. **Balance numbers** — all coefficients in `data/*.json` are defaults pending M2 Batch tuning.
5. **Tile georeference** — exact OS grid squares for the start tile and expansion extent.

*End of Master Design Document v2.0.*

---

# PART VI — AMENDMENT A1 (external review adjudication)

An external architectural review (Gemini) was assessed point by point. Verdicts and resulting design amendments follow. Amendments are binding; earlier sections should be read as modified by this part.

## VI.1 Adjudication

**R1 — "25 GB citizen state vs 20 GB RAM; monthly cold pass thrashes disk." ACCEPTED IN PART → A1, A2, A7.** The threat is real but the arithmetic overstated: the fix is layout, scheduling, and the cloud, not abandoning scale. (a) Cold citizens move to **columnar struct-of-arrays storage** with field-level compression (bucketed enums, delta-coded ages, bit-packed states): realistic cold footprint is **60–100 B/citizen**, i.e. 6–10 GB at 100 M — inside budget before paging even starts. (b) The monthly cold pass is **amortised**: a fixed rolling schedule processes 1/30 of shards per daily tick instead of one monthly spike — smooth I/O, deterministic (the schedule is fixed), and each citizen still advances exactly once per month. (c) NVMe SSD becomes a **stated hardware requirement** for very large cities. (d) Beyond the local comfort line (~20–30 M citizens), the cold-pass and shard store migrate to the **cloud citizen-shard service** — the same solver seam; Aaron's directive stands: *the cloud is used when needed*, and the thresholds are now explicit in §VI.2.

**R2 — "JSON everywhere is a death sentence for saves." ACCEPTED IN PART → A3.** The critique conflates tick-path allocation with background snapshot marshalling, but the scale point is right: 100 M citizens through a JSON marshaller is minutes of CPU. Amendment: a **`StateSerializer` interface from day one** (the reviewer's suggestion, adopted). JSON/NDJSON remains **canonical** for configs, protocol debug, fixtures, exports, and saves below a size threshold; above it, snapshots write a **versioned binary shard format**, with a bundled `metctl export` tool producing lossless NDJSON from any binary save — JSON stays the lingua franca of the project without ever sitting on a hot path. "JSON everywhere" is thus honoured as *universal accessibility*, not universal wire format.

**R3 — "36 M cells will blow 4 GB VRAM." REJECTED AS STATED; DISCIPLINE ADOPTED → A4.** Routing never touches cells: it runs on the road **graph** (order 10⁵–10⁶ edges even late-game) with **zone-aggregated OD matrices** (how every real transport model works — you never route citizen-pairs). ~5,000 zones ⇒ ~100 MB of OD; flows and path sets fit 4 GB comfortably. Amendment: zone aggregation is now the *specified* assignment structure (it was implicit), sizing tables go in the solver contract, and if late-game fidelity ever exceeds VRAM the identical contract runs in the cloud — that is what the seam is for.

**R4 — "100 M entities inside the 16 s daily tick is improbable in Go." REJECTED AS MISREADING; CAUTION KEPT.** Daily ticks touch logistics + HOT citizens only; cold citizens are monthly (now amortised, A2) — this is the entire point of adaptive fidelity (§5.2). The residual truth: Go hot loops need arena discipline and the GPU/cloud sidecars exist precisely for the day the CPU pool falls behind. No structural change; enforcement strengthened (A8).

**R5 — "Twenty-year education fuse is hostile design." REJECTED AS MISREADING; PRINCIPLE EXTRACTED → A5.** The anti-ambush contract (§1.3) already means consequences are *projected at decision time* — cut school funding and F7 shows the attainment/skill-gap curve that instant, not 32 hours later; adult education (§27) is the designed repair path. But the reviewer found a rule worth writing down: **the Slow-Fuse Principle** — any decision whose principal effects land more than 5 game-years out MUST render its projected consequence in the confirmation UI at the moment of decision. Now binding across all systems (education, planning quality, rehabilitation, BDI, debt).

**R6 — "Out-commuter tax farming exploit." ACCEPTED → A6.** Infinite static off-map pools do enable a degenerate dormitory farm. Amendments: (a) off-map job pools get **finite, era-scaled capacity** (London absorbs a bounded and slowly growing share); (b) external rail/coach capacity and crowding already bind — now explicitly listed as the second cap; (c) the fiscal arithmetic is surfaced: dormitory districts yield income tax but **no business rates and no corp share** — structurally revenue-thin per head, visible in F2 incidence; (d) commute burden → mental health → emigration remains the soft cap. Dormitory-city stays a *viable opening* (intended, historically honest) and stops being an endgame exploit.

**R7 — "Camera-skewed hot sample corrupts cold extrapolation." REJECTED AS MISREADING; WORDING HARDENED → A7.** §5.2 already separates the *rotating deterministic sample* from camera-hot citizens; but the spec now says it in bold: **cold-pass parameters are estimated ONLY from the stratified rotating sample** (stratified by district × age band × income, coverage-guaranteed), never from viewport-hot citizens, whose elevation is display fidelity only. An invariant test asserts parameter estimates are camera-invariant.

**R8 — "Shift to sparse ECS?" ANSWERED: SoA yes, generic ECS no.** Cold storage becomes columnar SoA per shard (A1) — the cache/compression win the reviewer is really asking for. A generic sparse ECS adds dynamic-composition indirection our fixed-schema, determinism-audited simulation neither needs nor wants. Hot citizens stay AoS structs (few, richly accessed); cold columns vectorise the batch pass.

**R9 — Claude Code enforcement suggestions. ACCEPTED → A8.** Added to the working agreement (III-C §6): CI runs escape analysis (`-gcflags="-m"`) and `GODEBUG=gctrace=1` on the perf harness per merge — allocations on tick paths fail the build mechanically, not by review; the determinism gate is built and passing on the stub engine **before any simulation logic** (TDD order now explicit); the map-range ban and pre-allocation rules are lint-enforced (custom golangci-lint rule), not prose.

## VI.2 Amended constitution entries
- **A1 Cold citizen store:** columnar SoA per shard, field-compressed, 60–100 B/citizen target.
- **A2 Amortised cold pass:** 1/30 of shards per daily tick on a fixed schedule; each citizen advances once per calendar month; deterministic.
- **A3 Serialization:** `StateSerializer` interface; NDJSON canonical + binary shard snapshots at scale + lossless `metctl export` to NDJSON.
- **A4 Assignment structure:** zone-aggregated OD (~10³–10⁴ zones) on the road graph; sizing tables in the solver contract; identical contract CPU/GPU/cloud.
- **A5 Slow-Fuse Principle:** >5-game-year consequences render projections at decision time, everywhere.
- **A6 Off-map job pools:** finite, era-scaled; dormitory arithmetic (no rates/corp share) surfaced in F2.
- **A7 Sampling firewall:** cold-pass parameters from the stratified rotating sample only; camera-invariance is a CI invariant. NVMe required for >20 M-citizen local play.
- **A8 Mechanical enforcement:** escape-analysis gate, gctrace perf gate, determinism-gate-first TDD, lint-enforced determinism rules.
- **A9 Cloud thresholds (explicit):** local CPU covers ≲20–30 M citizens end-to-end; beyond, cold-pass/shard store and (if needed) assignment move to cloud services on the existing seams; GPU sidecar is the intermediate tier. The player experience is identical at every tier.

*End of Amendment A1. Master document version: v2.1.*
