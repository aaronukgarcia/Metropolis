# Proposal: Integrated Transport — rail priority, frequency, multimodal hubs

**Author:** Bev, 2026-08-18 · **Status:** Aaron-directed + researched (real railway operations + transit-network design). BOW: FEAT-176–181. Post-Baseline-One. **An integrated, low-friction transport network is key gameplay** — it's how home→work/school/leisure stays easy as the city densifies, and easy access is a major wellbeing + attractiveness driver.

## 1. Rail priority & auto meet/pass (FEAT-176) — fixing the Blue rail-congestion failure

Blue's failure is unmanaged cargo-vs-passenger congestion. Fix it **by design** with a real railway meet/pass model:

- **Single-track = no overtaking.** Two trains can't occupy the same block; a faster train cannot pass a slower one ahead — both crawl behind until a passing point. Overtaking/meeting happens **only at a passing loop / siding** (a short double-track stretch holding one train).
- **The meet:** on a conflict (opposing direction, or a faster train catching a slower one), the **lower-priority train diverts into the next loop and holds**; the higher-priority train runs through at speed. Block-token rule: a train can't enter a block that's reserved/occupied, and can't leave the loop until the main line clears.
- **Priority order** (highest→lowest): express/high-speed passenger → stopping/local passenger → express freight → ordinary freight. Tie-break: on-time beats already-delayed (stops delay propagating). So Aaron's cases fall out naturally: a **cargo train holds in a siding to let a passenger train pass**; a **local passenger train holds to let the express pass**.
- **Line capacity = f(number of loops, loop spacing, headway).** More/closer loops → more meets/hour → higher capacity. Zero loops ≈ one alternating pipe. **Upgrade path:** add loops → then **double-track** the busiest section (removes the meet constraint there) — this is the user-agreed expansion (FEAT-162, ×2/×5/×10/×50 at cost).
- Trains occupy rail space including running from the **depot** to their line (like a bus occupies road space reaching its route).

## 2. Train frequency auto-allocation (FEAT-177)

`actual_frequency = min( demand_target, line_capacity_paths_per_hour, depot_available_units / round_trip_cycle_hours )` — whichever binds sets the real service. Frequency tiers: **hourly / daily / weekly / monthly / on-demand** (a cargo path runs as often as demand needs). Clock-face (regular memorable intervals) once demand is high; sparse/on-demand below. When all three bounds are hit → **saturation → user-agreed expansion** (more/faster track = more paths; more rolling stock from the depot). Same auto-allocation intelligence as bus vehicles (FEAT-161): a service starts at one unit and scales to demand within capacity.

## 3. Connection modes (FEAT-178)

Player choice, per-network or global: **(a) Manual** — services auto-pass-through and the player draws/connects lines and stops (the Blue manual model); **(b) Full-auto mesh** — the system connects the whole network intelligently and routes services to demand. The mesh reuses the demand+capacity intelligence of FEAT-161/176/177.

## 4. Station typology (FEAT-179)

- **Through-station:** trains stop to load then continue — short dwell, **high throughput** per platform.
- **Terminus:** stub tracks, trains reverse — dwell includes turnaround, so **lower throughput** per platform; busy termini add **bay platforms** (for terminating services) or buried through-platforms to mitigate.
- **Size ladder → throughput:** rural halt (1 platform, e.g. "Sanderling") → suburban (2 platforms) → junction/interchange (multi-platform, through+terminating) → major terminus/hub (many platforms, e.g. "Kings Cross"). Station type/size bounds line capacity and feeds the meet/pass model.

## 5. Multimodal interchange hubs + high-speed (FEAT-180)

- **Hub-and-spoke is the default** (economies of scale — a hub offers far higher frequency between any two points than direct routes could). But every transfer carries a **transfer penalty**: added time + a real drop in willingness-to-use beyond the raw time cost. **Hub quality** (short walk, low wait at the interchange) shrinks that penalty — so a well-designed interchange is worth building well.
- **Multimodal hubs** are where the player composes solutions: rail + bus + air + ferry meeting at one node (e.g. a harbour + rail connection with a bus terminus, an airport + rail + bus). The integrated, low-friction network raises attractiveness + wellbeing (easy home→leisure/work).
- **Late-game hyperloop / high-speed point-to-point** (catalogue `hyperloop_terminal`): unlock when city is a major tier AND a specific O-D pair (e.g. major airport ↔ major city centre) has sustained high demand in an efficient distance band with a time-sensitive segment. Dedicated non-shared corridor, city-centre-focused.

## 6. Bus network topology (FEAT-181)

- **Default: hub-and-spoke** — bus lines **converge at a bus terminus/interchange** (default ON), e.g. ~20 lines meeting at a harbour+rail terminus.
- **Point-to-point (direct) lines** are offered where a specific O-D pair's volume is high enough to justify bypassing the hub: **stadium ↔ station for the big game, theme park ↔ station, shopping centre ↔ station.** Trigger: `OD_demand > direct_line_viability_threshold → spawn dedicated point-to-point route`. These surge lines auto-allocate buses to saturation for the event (FEAT-161), subject to road capacity + depot, and are the crowd-movers that stop event congestion.

## 7. Cross-refs & determinism
Builds on FEAT-161 (vehicle auto-allocation to saturation), FEAT-162 (user-agreed ×N expansion), MOD-035 (commute-loading — these networks carry the aggregate people-flows that load roads/rail/junctions), engine.rail, engine.airport, engine.freight/containerport, engine.traffic (spec-only). All routing, meet-scheduling, frequency, and vehicle allocation use seeded deterministic streams (GR#21). All player-felt numbers (priority weights, loop-capacity curve, transfer-penalty size, direct-line thresholds, high-speed trigger) are balance-number-regime placeholders — directional/structural tests only, row-by-row at the balance pass.
