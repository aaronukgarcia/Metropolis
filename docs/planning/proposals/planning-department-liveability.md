# Proposal: Planning Department, Parks & Cleanliness (policy-driven liveability)

**Author:** Bev, 2026-08-18 · **Status:** Aaron-directed (2026-08-18); BOW items filed for future-stage build. The design heart is the **crowding-vs-liveability juggle** ("the game is the juggle" north star).

## 1. The tension Aaron wants

Dense living (skyscraper flats) is a real crowding penalty (`engine.wellbeing` Crowding driver / PersonsPerRoom; FEAT-170 housing adequacy). But it can be **offset** by great public realm — the London model: some of the densest housing in Europe sits beside Hyde Park, Regent's Park, Hampstead Heath. So the game should let a player build dense **if** they provide enough quality green space, keep the place clean, and make leisure easy to reach. That trade — cram them in, but give them amazing parks and a clean, walkable city — is the juggle. Conversely, dense + no parks + dirty = wellbeing collapse and the emigration spiral.

## 2. The Planning Department — a policy-driven auto-provision framework

A **policy layer**: the player sets *targets/levels*, and the Planning Department auto-delivers and maintains to them (default-on-early, prompt-at-mega-scale, per-item + global toggle — same model as FEAT-163 auto-amenities, which becomes a policy under this framework). Policies are extensible; the first three:

### 2.1 Recreational-space policy (parks)
- Player sets **park space area per person** (e.g. m² of accessible green space per capita — a real UK planning metric; ~9 m²/person is a common benchmark, placeholder).
- The department **auto-creates** parks (from the existing catalogue: pocket_park → town_park → city_park_large → botanical_garden, etc.) to meet the per-capita target as population grows, siting them for access (see 2.3).
- **Maintenance tier per park/policy: High / Medium / Low / None.** Higher tier = better condition = bigger wellbeing contribution, but higher upkeep cost (via `engine.maintenance`).
- **Luxury flag**: benches, waste bins, cafés, dog areas — amenity fit-out that adds **hugely** to wellbeing (and café-culture ties into leisure/tourism). Luxury parks are the "amazing parks" that offset crowding.
- Parks feed `engine.wellbeing` (a green-space/recreation driver) and the `engine.attract` Environment/LeisureFit terms (currently placeholder 50.0 — FEAT-167 wiring).

### 2.2 Cleanliness policy
- A **cleanliness score**, tracked hierarchically: **area / zone / parish / city** (explicit Aaron call-out).
- **Degrades simply by having people in it** — footfall makes places dirty over time; denser/busier = dirtier faster.
- **Cleaners + maintenance restore it**, deployed to a **policy level: High / Medium / Low / None** (auto per policy).
- Clean places are **far more attractive** (people like living there) → feeds `engine.attract` (a cleanliness/Environment contribution) and `engine.wellbeing`; dirty places repel. Distinct from `engine.refuse` (bins/collection of waste mass) — cleanliness is the *public-realm condition* score, though the two interact (overflowing refuse worsens cleanliness).

### 2.3 Walkable leisure access
- Access from **home → leisure/parks** matters: good transport is good, but **best is an easy walk, cycle, or free bus** (Aaron). 
- Model an **access-quality** signal per home/area: proximity to parks + leisure, weighted by mode (walk/cycle > free bus > paid transit > car/long commute). Best access = highest wellbeing + LeisureFit; poor access = the green space "doesn't count" for people who can't easily reach it.
- Ties to the transport digital-world (FEAT-161/162) and commute-loading (MOD-035): a free-bus policy to parks is both a leisure-access and a transport-loading lever.

## 3. How the juggle resolves (the wellbeing arithmetic)
Wellbeing ≈ … − Crowding(persons/room) + GreenSpace(per-capita park × maintenance tier × luxury × access) + Cleanliness(score) + … . So a player can run high crowding **if** GreenSpace + Cleanliness + Access are high enough to net positive — and that costs money (build + maintenance + cleaners), which is the finite-pie tension. Under-provision → wellbeing falls → attractiveness falls → emigration (the spiral). Attractiveness (`engine.attract`) gains Environment/LeisureFit/ServiceCoverage from all three, making a green, clean, walkable city genuinely draw migrants and tourists.

## 4. Build-on / cross-references
- `engine.wellbeing` (drivers: add green-space/recreation + cleanliness contributions alongside Crowding), `engine.attract` (Environment/LeisureFit/ServiceCoverage wiring — FEAT-167), `engine.leisure` (parks as patronage venues), `engine.maintenance` (park upkeep + cleaner deployment backlog), `engine.refuse` (refuse overflow worsens cleanliness), FEAT-163 auto-amenities (becomes a Planning-Dept policy), FEAT-170 housing adequacy (the crowding side of the juggle), FEAT-161/162 transport (walkable access). Parks already exist in `data/buildings.json` (20 PK entries) — what's new is the policy-driven per-capita provision, maintenance tiers + luxury, the cleanliness score, and access weighting.

## 5. Determinism & balance guard-rails
Policy auto-provision, park siting, cleanliness decay, and cleaner deployment use seeded deterministic streams (GR#21). All player-felt numbers (m²/person target, maintenance-tier wellbeing multipliers, luxury bonuses, cleanliness decay rate per capita, cleaner cost/throughput, access-mode weights) are balance-number-regime placeholders — directional/structural tests only, row-by-row at the balance pass. Post-Baseline-One future-stage work.

## 6. Park composition & demographic fit (Aaron 2026-08-18; researched)

A park is not green space — it is **a place people go to feel better**, modelled as a **composition of FEATURES**, each with an attractiveness weight and a **demographic appeal profile**. Each park is effectively pitched at a demographic (or a curated mix). Grounded in real park-design research (Central Park, the Royal Parks, Project for Public Spaces, pocket-park literature).

### 6.1 Scoring model
`park value = Σ(feature attractiveness points) × demographic-fit(local population mix) × usability-gate`, where:
- **demographic-fit** rewards how well the park's feature mix matches the *local* population's demographic composition (a family-heavy district wants playgrounds/splash-pads; an ageing district wants gardens/benches/bowling green).
- **Coverage-breadth bonus (destination parks):** large parks earn a bonus for serving *many distinct demographics at once* — the reason Central Park/Hyde Park are beloved is stacked variety (water + landmark + sport + nature + open lawn + play), not one feature. So five benches must NOT out-score one skatepark + one dog area + one lake.
- **Usability gate:** benches, toilets, lighting, paths, safe sightlines are low-weight individually but act as a **multiplier/gate** — a park failing these underperforms for every demographic regardless of its feature list.
- **Iconic features** (boating lake, signature landmark/unique layout, ornamental gardens) drive destination + tourism draw (feeds `engine.tourism` MOD-057) and pull visitors beyond the local catchment.

### 6.2 Pocket vs destination
- **Pocket park** (infill/small): scored purely on **fit to the 1–2 nearest dominant demographics** — no breadth bonus; over-provisioning is wasted. Serves its block.
- **Destination/community park** (large): scored on **breadth** — enough distinct features to cover many demographics simultaneously; this is where an iconic feature + multi-demographic coverage compound into outsized value and tourism draw.

### 6.3 Feature catalogue (weights: Low/Med/High/Iconic) → demographics
Play: swings/sandpit (Low-Med) → young families; full playground, splash pad (High) → families/children. Sport: basketball/volleyball/tennis (Med) → teens/active adults; skate park (High) → teens; sports pitches, cricket nets, bowling green (Low-Med) → sports-players/elderly; outdoor gym (Med-High) → elderly/adults; running-cycling loop (High) → active adults/dog-owners. Nature/scenic: fountain (Med-High), boating lake/pond (Iconic), rose/ornamental garden (Med-High) → elderly/tourists/nature-lovers; wildflower meadow, wildlife area, aviary (Med-High) → nature-lovers/families. Social/culture: picnic lawn (High), bandstand (High), café (Med-High), statues/public art (High), maze (Med) → general/tourists/families. Dog area (High to dog-owners). Unique layout / signature landmark (Iconic) → tourists/destination. Gates: benches, toilets, lighting, paths, trees (Low, multiplier).

### 6.4 Demographic → "a great park for them"
Young families: playground + splash pad + toilets + café within sight of seating. Teens: skate/basketball/volleyball + open hangout + lighting (NOT babyish supervised kit — real research shows planners guess wrong, so fit must be a genuine weighted match). Active adults: running loop + tennis + outdoor gym. Working adults: café + lunchtime lawn + running loop + benches. Elderly: shaded benches + ornamental gardens + low-impact outdoor gym + bowling green + bandstand + level accessible paths. Dog-owners: off-leash dog area + loop + open lawn. Tourists: iconic landmark + water + gardens + bandstand + café. Nature-lovers: meadow + wildlife area + lake + boardwalk/viewpoint.

### 6.5 The juggle payoff
This makes §1's tension richer: a dense district of skyscraper flats stays liveable if its nearby parks are **well-composed for its actual residents** (a young-family tower block beside a park with no play features still fails). It rewards thoughtful, demographic-aware provision over generic green squares — and the Planning Department's recreational-space policy can auto-compose parks to the local demographic mix, or the player can hand-tune. Balance weights (per-feature points, demographic-fit curve, breadth-bonus, gate multipliers) are all balance-number-regime placeholders.
