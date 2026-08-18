# Proposal: Population Growth, People-Flow & Automation

**Author:** Bev, 2026-08-18 · **Status:** Aaron-ruled (interview 2026-08-18); BOW items filed for future-stage build (post-Baseline-One). Grounded in a 2026-08-18 engine audit — every "today" claim is cited from the real tree.

## 1. How population works today (baseline)

- **Growth is 100% migration-driven.** `engine.attract` scores a 7-term attractiveness (jobs, service coverage, environment, leisure-fit, safety, housing affordability, reputation) vs a world-pool baseline and admits migrant households when net-positive; `engine.spiral` runs the same signal in reverse (Detroit death spiral). This is the Blue attractiveness model.
- **Childbearing does NOT exist.** Citizens are persistent individuals with `Partner`/`Children` fields, pair into households, age, and die (real Gompertz-Makeham mortality), but nothing makes residents have children — `LifeEventBirth` is only used to create migrants.
- **Attractiveness is not yet connected:** in the live loop 5 of 7 terms are a hardcoded `50.0` placeholder and `appealProfile` is empty on all 337 catalogue entries — so migration currently responds to almost nothing real.
- **Visitors/commuters are specced but unbuilt:** `engine.extcommute` (MOD-035, resident↔off-map commuting) and `engine.tourism` (MOD-057, day-tripper/staying visitors) have full acceptance criteria and ZERO code; `engine.traffic`, `engine.parking`, `engine.dispatch`, `engine.staffing` likewise spec-only. University in-commuting and day/night-population accounting are absent even in spec.

## 2. The target model (Aaron-ruled)

### 2.1 Population = natural increase + migration
- **ADD a fertility mechanic** (individual, per-couple): resident couples in a household bear children per a deterministic fertility hazard (age / wellbeing / housing driven), each child a persistent citizen tied to the parents' `Children`. Natural increase and migration both drive growth. (Determinism: hazard uses a seeded per-couple stream, GR#21.)
- Migration stays attractiveness-driven; wiring the real attractiveness terms (§2.4) is what makes it respond to gameplay.

### 2.2 Residents vs visitors/commuters — aggregate flows that LOAD the network
- Residents remain persistent individuals. **Visitors, tourists, and commuters are AGGREGATE day/night population flows** (counts, not individual records) — cheap at 100M scale.
- **Critically, these flows LOAD the transport network** — this is core play, not bookkeeping. Getting kids to school and people to work is "part of the pain and the joy." Model aggregate people-counts utilising roads (junction throughput), rail, and bus queues; **commute time is a major happiness driver** (feeds `engine.wellbeing`). In-commuters fill local jobs without becoming residents; out-commuters (residents) use the network to leave for off-map jobs (London/Ashford/Dover pools — data schema already built in `foundation/data/external_world.go`) and return each evening.
- Tourism/leisure visitors are a parallel inbound stream driven by a distinct visit-appeal score (separate from residential desirability) and also load the network + accommodation.

### 2.3 The transport "digital world" — auto vehicles, user-agreed infrastructure
This is the heart of the automation ask.
- **The network is a digital world:** we know how many people are waiting at each stop/station and travelling each junction.
- **Vehicles auto-allocate to saturation.** A bus/train LINE starts with **1 vehicle** at creation; the game auto-allocates more vehicles per line based on **vehicles available × line length × waiting demand**, up to **saturation** — the point where more vehicles don't help because the underlying infrastructure (roads/rails) can't carry more.
- **Line & stop identity (Blue-style):** each line gets a unique route id (e.g. `XZY2309`) and named stops (stop 001 "High Street" → stop 200 "Train Station"); the UI shows the **live vehicle count per line** (the auto-allocation result), like Blue.
- **Infrastructure expansion is NEVER auto — it is USER-AGREED.** When a line/junction saturates, the game **proposes** an expansion (bigger/more roads, more/faster trains) **at a stated cost**; the player must acknowledge and choose an **expansion rate: ×2 / ×5 / ×10 / ×50**. Big infrastructure is very expensive and a core strategic choice, so it never happens without explicit consent.

### 2.4 Attractiveness wiring (integration, not new design)
Populate `appealProfile` on catalogue entries and wire `engine.attract`'s currently-placeholder terms (service coverage, environment, leisure-fit, safety, jobs) to real signals, plus a distinct tourism visit-appeal portfolio (beach/promenade/pier/venues/events/heritage × reputation × access × season, per `engine.tourism.md`). Reuse `AttractAPI.Reputation()` for both (no second reputation figure).

## 3. Automation of amenities & utilities (Aaron-ruled)

**Default: ON early to remove legwork, with a PROMPT at mega-city milestones before heavy auto-expand** (interview option 3), and **both a global master toggle and per-item toggles** (turn off auto-bus-routes but keep auto-naming, etc.). Design precedent: `feat.faith` (FEAT-138) already specs zero-interaction auto-building of places of worship — reuse that structural pattern (never routes through player `BuildAPI`, deterministic, off-catalogue).

- **Auto-amenities at milestones (just-in-time):** council office, swimming pool, library, education (JIT), fire, police, postal sorting, maintenance, bus station — auto-placed when a population/coverage milestone triggers. Includes the tedium-reducers: auto-lay bus stops and optimised bus routes, auto-add bus stations (e.g. ~200 dwellings / ~700 people → one bus stop from the centre point to the nearest hub — station or out-of-area town).
- **Auto-naming hierarchy (extends the existing `engine.roads/naming.go` service):** startup asks the player for a city name (free text, e.g. "Folkestone"); as **parishes / wards / suburbs / zones** are created they are auto-named from real East Kent toponyms (`data/naming_corpus.json`), player-editable on any object (the rename registry already protects edits). Add the admin hierarchy tiers + bus route/stop id+name generation (§2.3).
- **Utilities auto-expand at scale:** early game the player requests and places water pump / sewage works; at mega-city scale water treatment works (multiple), sewage, solar farm, wind farm go into **auto-expand mode** (prompt-at-scale per the option-3 default). NB this differs from transport: **utilities auto-expand; transport infrastructure is user-agreed (§2.3)** — because transport capacity is a strategic, very-expensive choice.
- **Body-disposal pipeline (wire the existing catalogue):** death is already simulated but bodies aren't routed anywhere. Add **auto dead-body collection** and disposal, mirroring `engine.refuse`'s out-of-area export-contract pattern: **in-area crematorium / cemetery (cheap)** vs **out-of-area services (expensive)**. Wire cemetery plot depletion and crematorium throughput like refuse's bin/truck capacity. Recycling/compost/incineration are already modelled in `engine.refuse`.

## 4. Catalogue coverage vs Blue

The catalogue (`data/buildings.json`, ~500+ entries across residential/commercial/industrial/civic/utilities/transport/leisure/mega + SUP2/SUP3 expansions) is already broad and clearly Blue-informed. Blue-expansion content Aaron named is **present**: cycle hire + cycle lanes, multi-storey/surface/on-street car parks, park-and-ride, recycling centre, crematorium, composting works, congestion charge. **Real gaps are not building types — they are the unbuilt engine MODULES** (traffic, parking, tourism, extcommute, dispatch, staffing +6). Minor catalogue candidates to consider: dedicated **bike storage/parking** (distinct from cycle *hire*), and confirming the car-park size ladder is complete. A full line-by-line catalogue-vs-Blue diff is filed as a review task.

## 5. Determinism & balance guard-rails
- Fertility hazard, vehicle allocation, auto-placement, and naming all use seeded deterministic streams (GR#21); identical seed ⇒ identical city.
- All player-felt numbers (fertility rate, saturation thresholds, expansion costs/×N, out-of-area service premiums, milestone triggers) are placeholders under the balance-number regime — directional tests only, row-by-row at the balance pass.
- Auto-features must be inert on the determinism/perf-gate paths where relevant, and every auto-action is player-reversible.

## 6. Sequencing
All of the above is **post-Baseline-One** future-stage work. Natural ordering: wire attractiveness terms (§2.4) → build extcommute (MOD-035) + tourism (MOD-057) as aggregate flows that load transport → traffic/transport digital-world + vehicle auto-allocation + user-agreed expansion → fertility → auto-amenities + auto-naming hierarchy → utility auto-expand → body-disposal → catalogue gap-fill. Nothing here blocks Baseline One.
