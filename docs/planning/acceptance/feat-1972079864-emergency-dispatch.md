# FEAT-1972079864: React Console — Emergency Services Dispatch with ETA and Waste Rota

**Feature:** Webconsole emergency services dispatch system (fire/police/ambulance) with visible vehicle movement, ETA display, and waste+recycling collection rota.

**Mkey:** FEAT-1972079864

## Overview

Extends emergency service buildings (fire stations, police stations, ambulance stations) from static coverage providers into active dispatchers. When an emergency incident occurs (fire, crime, medical emergency), a vehicle spawns at the nearest service building, travels deterministically along roads to the incident site, displays ETA in ticks, arrives to resolve the incident, and returns to base. Multiple concurrent incidents are queued and handled by available vehicles. A separate waste-and-recycling rota displays scheduled collection rounds on a map overlay with deterministic scheduling (same seed+actions → same schedule).

---

## Design Decisions Flagged for Lead

### DD1: Incident Generation Model
**What triggers emergency incidents, and at what rate?**

Incidents are discrete events, not continuous hazards. Options:
- **Option A (CHOSEN UNLESS OVERRULED):** Incidents spawn probabilistically per tick, with rates tuned per incident type:
  - Fires: rare, tied to building density + age (PLACEHOLDER: 1 fire per 5,000 residents per month)
  - Crime: tied to population + policing coverage deficit (PLACEHOLDER: 1 crime per 2,000 residents per month)
  - Medical emergencies: tied to population + health coverage deficit (PLACEHOLDER: 1 emergency per 1,500 residents per month)
  - Rates are deterministic (seeded RNG, same state → same incidents per tick)
- **Option B:** Incidents spawn on a fixed calendar (e.g., monthly incident sweep), no randomness per tick
- **Option C:** Incidents spawn only when a player explicitly triggers them (debug-only, no gameplay randomness)

**Consequence:** Option A is most realistic and engaging; option B loses responsiveness; option C is simulation-only.

**Flag for Aaron:** Confirm which option and which rates.

---

### DD2: Incident Location
**Where on the map do incidents spawn?**

- **Option A (CHOSEN UNLESS OVERRULED):** Uniform random tile within the map, or weighted to populated tiles (residential/commercial/industrial)
- **Option B:** Incidents spawn ONLY on or adjacent to occupied tiles (no incidents in empty wilderness)
- **Option C:** Incidents spawn at specific landmark/building types (e.g., fires at industrial sites, crime at parks)

**Consequence:** Option A is simple and legacy-compatible. Option B is realistic. Option C is complex but narratively rich.

**Flag for Aaron:** Confirm which option.

---

### DD3: Vehicle Dispatch Algorithm
**Which service building dispatches a vehicle to an incident?**

- **Option A (CHOSEN UNLESS OVERRULED):** Nearest available (idle) vehicle building of the correct type (fire → fire station, crime → police, medical → ambulance). Ties broken by lowest building ID. If no vehicle is available (all dispatched), the incident waits in a queue until a vehicle returns to base.
- **Option B:** Round-robin across available buildings, cycling dispatch to balance load
- **Option C:** Dispatch from the building with the highest coverage rating for that incident type (preferred/specialist buildings first)

**Consequence:** Option A is deterministic and simple (GR#21). Option B requires explicit load tracking. Option C is complex but realistic.

**Flag for Aaron:** Confirm which option.

---

### DD4: ETA Calculation
**How is ETA (estimated time to arrival) computed and displayed?**

- **Definition:** ETA = the number of ticks from NOW until the vehicle arrives at the incident site.
- **Route:** Shortest road-path from vehicle building to incident tile (same pathfinding as road auto-connect; use `planConnector()` inverted: given building and incident, find shortest drivable path).
- **Travel time:** Deterministic, per-segment cost + no acceleration. PLACEHOLDER: 1 tick per road segment (tunable without code change — can become 0.5 ticks or 2 ticks per segment).
- **Display:** ETA shown on the MapView as an overlay label on the moving vehicle glyph (e.g., "ETA: 12t" at normal speed), or as a tooltip/hover preview.
- **Determinism:** Same state + same incident location + same dispatch base → same ETA (no randomness, no Date.now, no frame-dependent jitter).

**Flag for Aaron:** Confirm ETA display style (overlay label vs tooltip), precision (ticks vs seconds), and travel-time cost per segment.

---

### DD5: Incident Resolution and Vehicle Return
**What happens when a vehicle arrives at an incident?**

- **On arrival:** Vehicle plays a brief holding animation (deterministic from tick, no Date.now), then disappears from the map (incident resolved, vehicle returns to base off-screen).
- **Return time:** Vehicle is unavailable for DWELL_TICKS (same constant as trains.ts; PLACEHOLDER: 3 ticks) while "returning to base" (off-map). Then it re-appears at its base building, idle, ready for the next dispatch.
- **Consequence:** If incidents queue faster than vehicles can resolve them, the queue grows. Player must build more service buildings to handle load.

**Flag for Aaron:** Confirm dwell/return time and whether the return journey should be visible (travelled back) vs. off-map (instant).

---

### DD6: Waste Rota Scheduling
**How is the waste-collection rota scheduled, and what does it display?**

FEAT-1972079906 (garbage generation + collection + processing) has already landed (inc1 inc2). This feature's waste-rota part (inc3) is the UI for the collection SCHEDULE: which streets are serviced on which ticks/days.

- **Rota model:** A waste depot (collection building) has a deterministic round schedule. It services a fixed set of street tiles (road network reachable from the depot within a budget, or closest N tiles by distance, PLACEHOLDER: all reachable tiles within 50 tiles of the depot). On each scheduled round tick, the depot collects refuse from that set.
- **Round frequency:** PLACEHOLDER: once per month (TICKS_PER_MONTH / number of depots = ~30 / 3 = 10 ticks per round if 3 depots). Tunable without code change.
- **Display:** Map overlay toggle (like showWater / showPower / showLines in MapView) that colors roads on the rota schedule. On rota this tick = bright green. Next 2 ticks = dimmer green. Not on rota for >2 ticks = grey. Hovering over a road tile shows "Rota: day 15 of 30" or similar.
- **Determinism:** Same seed + same buildings + same depot assignment → same rota schedule forever.

**Consequence:** Player can plan where to place depots to avoid overburdening any area. Uncollected waste penalties are incurred on uncovered streets (visible in waste panel, FEAT-1972079906 inc1/inc2 already computed this).

**Flag for Aaron:** Confirm rota-display toggle location (MapView menu?), budget for rota reachability (50 tiles?), and round frequency formula.

---

## Acceptance Criteria

### AC-1: Emergency Incident Entity
**Incidents are first-class SimState entities with deterministic spawn logic.**

- **Definition:** An incident is a tuple `{ id, type, x, y, spawnTick, baseId, arrivals: [{buildingId, eta}] }` where:
  - `type` ∈ { 'fire', 'crime', 'medical' }
  - `x, y` = tile location (deterministic from spawn logic, not user input)
  - `spawnTick` = tick when the incident was created
  - `baseId` = nearest available service building dispatching to this incident (set at dispatch time)
  - `arrivals` = array of {buildingId, eta} for competing vehicles on the way (fire incidents may have multiple trucks en route)
- **Storage:** Incidents stored in `SimState.incidents: Incident[]`; each incident has a unique auto-incrementing `id` (like buildings).
- **Determinism:** Given identical `SimState` and random seed, incident spawn location, type, and timing are identical.
- **Test:** Create a seeded state, run 100 ticks, capture all incident spawns (id, type, tick, x, y). Replay from the same seed, verify spawns match exactly.

---

### AC-2: Incident Spawn Logic — Rates and Seeding
**Incidents spawn probabilistically per tick, driven by seeded RNG (GR#21 determinism).**

- **Rate model:** Per-incident-type, per-tick spawn probability. Rates are derived from population + service coverage:
  - Fire spawn rate (PLACEHOLDER): `max(0, (population / 5000) / TICKS_PER_MONTH) * (1 + (1 - fireServiceCoverage))` (more fires when coverage is low)
  - Crime spawn rate (PLACEHOLDER): `max(0, (population / 2000) / TICKS_PER_MONTH) * (1 + (1 - policeServiceCoverage))`
  - Medical spawn rate (PLACEHOLDER): `max(0, (population / 1500) / TICKS_PER_MONTH) * (1 + (1 - healthServiceCoverage))`
- **Seeding:** RNG state is derived from `state.tick` and a master seed (never Date.now, never Math.random() unseeded). Same state → same RNG draw → same spawn decision and location every time.
- **Coverage source:** `serviceCoverageOf(state)` same as building-activation gates (SSOT, GR#3).
- **Test:** Run 100 ticks at 100% coverage, zero population → expect zero incidents. Run 100 ticks at 1,000 residents + 0% fire coverage → expect proportional fire incidents.

---

### AC-3: Dispatch Queue
**When an incident spawns, find the nearest available service building and add to that building's dispatch queue.**

- **Availability:** A service building is "available" iff:
  - It is online (passes activation gates from FEAT-1972079891)
  - It matches the incident type (fire → fire_station/fire_post/fire_hq, crime → pol_station/pol_hq, medical → hea_ambulance)
  - It is NOT currently dispatched (its vehicle is not en route)
- **Distance metric:** Shortest road-network distance (use `planConnector()` inverted to find route length). Ties broken by lowest building ID.
- **Queue state:** `Building.dispatchQueue: Incident[]` (array of incidents waiting for this building's vehicle to become free).
- **On arrival:** When a vehicle arrives at an incident, remove the incident from the queue. If the queue is non-empty, immediately dispatch the next incident.
- **Test:** Place a fire station at (10, 10). Spawn 3 fire incidents. Verify the first goes to the dispatch queue. After first is resolved, verify second is dispatched.

---

### AC-4: Vehicle Entity and Movement
**Dispatched vehicles are UI-derived glyphs, like trains (not stored in SimState).**

- **Vehicle definition:** A vehicle is a tuple `{ buildingId, incidentId, progress }` where:
  - `buildingId` = source service building
  - `incidentId` = target incident
  - `progress` = fractional position along the route (0 = at base, 1 = arrived)
- **Computation:** Vehicles are computed fresh each render tick by a pure function `dispatchedVehicles(state: SimState): Vehicle[]` (mirrors `trainPositions()`):
  - For each incident with a dispatched vehicle (baseId set, not resolved):
    - Compute route from base building to incident tile using `planConnector()` logic
    - Compute position interpolation: `progress = (tick - dispatchTick) / routeLength`
    - If `progress >= 1`, mark incident as arrived
  - Return all in-flight vehicles
- **Rendering:** MapView loops over vehicles and draws a glyph (e.g., a small icon or 📍 emoji, styled by type: red for fire, blue for police, green for ambulance) at the interpolated (x, y) with ETA label.
- **Determinism:** Same state + tick + route → same vehicle position (no movement jitter, no frame-dependent animation). Replay consistency: if a recording plays back from the same seed, vehicles follow identical paths.
- **Test:** Dispatch a vehicle at tick 100 from (10, 10) to (20, 20) with route length 10. At tick 105, verify progress = 0.5. At tick 110, verify progress = 1 and incident is marked arrived.

---

### AC-5: ETA Display on Moving Vehicle
**Each vehicle displays its ETA (ticks to arrival) as a label on the glyph.**

- **ETA formula:** `ETA = max(0, routeLength - (tick - dispatchTick))`
- **Display format:** Rendered as text (e.g., "ETA: 7t") on the vehicle glyph in MapView, updating every tick.
- **No decimal places:** ETA is an integer (ticks remaining). At normal speed, can optionally display wall-clock estimate (e.g., "7t = ~4s") but this is PLACEHOLDER and awaits Aaron's UI guidance (DD4).
- **Zero ETA:** Once ETA reaches 0, the label disappears (vehicle at destination).
- **Test:** Vehicle with 10-tick route displays "ETA: 10t" at dispatch, "ETA: 5t" at tick 5, "ETA: 0t" at tick 10 (then disappears on arrival).

---

### AC-6: Incident Arrival and Resolution
**When a vehicle reaches an incident (progress >= 1), the incident is resolved and the vehicle returns to base.**

- **Arrival trigger:** At end-of-tick during `advance()`, check if any dispatched vehicles have `progress >= 1`.
- **On arrival:**
  - Remove incident from state (or mark as resolved)
  - Set vehicle to "returning" state: unavailable for DWELL_TICKS ticks (off-map, invisible)
  - If dispatch queue for this building is non-empty, start the next dispatch (compute route to next queued incident)
- **Dwell period:** After DWELL_TICKS, building is idle again. If new incidents spawn during dwell, they wait in the queue.
- **Consequence:** The more incidents spawn, the more service buildings are needed to keep up. An understaffed city develops a backlog of unresolved incidents.
- **Test:** Dispatch vehicle, verify it arrives. Verify incident is no longer in state. Verify building is unavailable for DWELL_TICKS. Verify queued incidents are dispatched after dwell expires.

---

### AC-7: Concurrent Dispatches (Multi-Building)
**Multiple service buildings can have vehicles dispatched simultaneously; each tracks its own queue.**

- **No global limit on concurrent incidents:** If 5 fires spawn and 3 fire buildings are available, all 3 dispatch to different incidents, and the 4th and 5th incidents wait in their respective queues.
- **Building independence:** Each building's dispatch queue is tracked independently. Building A's vehicle can be en route while Building B is idle or returning to base.
- **Test:** Place 2 fire stations. Spawn 5 fire incidents. Verify first 2 dispatch to different buildings. Verify remaining 3 are queued. As first 2 resolve, verify queued incidents are dispatched in order.

---

### AC-8: Incident Type → Building Type Routing
**Fire incidents only dispatch from fire buildings, crime from police, medical from ambulance stations.**

- **Type mapping:**
  - Fire → fire_post, fire_station, fire_hq
  - Crime → pol_station, pol_hq
  - Medical → hea_ambulance
- **No cross-type:** A medical emergency does NOT dispatch a fire truck, even if no ambulance is available (it waits in queue until an ambulance frees up).
- **Consequence:** City must zone appropriate buildings for each emergency type or face unhandled backlog.
- **Test:** Spawn a medical incident with only fire stations available. Verify medical incident waits in queue. Place an ambulance. Verify it dispatches immediately.

---

### AC-9: Route Pathfinding — Reuse Road Network
**Dispatch routes use the same road-network graph and pathfinding as road auto-connect (roadConnect.ts).**

- **Algorithm:** Use `planConnector()` inverted: given a source building and target incident tile, plan the shortest road-connected path (same level-synchronous BFS, same CONNECT_BUDGET cost limit, deterministic).
- **Budget:** Same CONNECT_BUDGET (PLACEHOLDER: 6000 cells) for each dispatch route. If no route exists within budget, the incident waits indefinitely (unresolvable — player must connect the area to the road network or build a closer service building).
- **Edge case:** If a building is on a disconnected road island, incidents on the same island can be serviced; incidents on other islands cannot (no route).
- **Test:** Place a fire station on one side of an impassable gap. Spawn a fire on the other side. Verify no route and incident waits. Close the gap. Verify fire is dispatched.

---

### AC-10: Waste Rota Scheduling — Deterministic Round Assignment
**Each waste depot has a fixed, deterministic collection schedule for the tiles it services.**

- **Rota service set:** A depot at (bx, by) services all road tiles reachable from the depot within a budget (PLACEHOLDER: 50 tiles, or a fixed time-distance cost like auto-connect CONNECT_BUDGET). Road tiles are sorted by (x, y) for determinism.
- **Round frequency:** Depots are assigned to rounds on a rotating schedule. PLACEHOLDER: each depot completes one round every TICKS_PER_MONTH (e.g., 30 ticks). If there are N depots, each gets 1/Nth of the month (round 0: depot 0 collects ticks 0–N-1, depot 1 collects ticks N–2N-1, etc.), repeating.
- **Assigned round:** `Incident.rotaRound: number` (not an incident; sorry, wrong analogy). Actually: `WasteRound: { depotId, roundTicketTile[], nextScheduledTick }` — each depot has a list of rounds, each round scheduled for a specific tick. Stored in `SimState.wasteRounds: WasteRound[]`.
- **Determinism:** Same seed + same buildings → same rota schedule forever (same tiles, same ticks).
- **Test:** Build 3 waste depots. Verify 1st services tiles {(10,10), (11,10), …}. Verify it collects on ticks 0, 30, 60, … (every month). Verify 2nd services different tiles and collects on ticks 10, 40, 70, …

---

### AC-11: Waste Rota Display — Map Overlay
**A map-layer toggle displays waste collection routes with a per-tick schedule indicator.**

- **Layer placement:** New toggle button in MapView (like showWater, showPower, showLines — component-local state, UI-only, never in SimState or journal).
- **Visual coding:**
  - Collected THIS tick: bright green (#2ecc71 or similar), full alpha
  - Collected next 2 ticks (PLACEHOLDER): dim green, alpha 0.6
  - Not collected for >2 ticks: grey (#666), alpha 0.4
  - Uncovered tiles (no depot can reach): grey with X pattern (diagonal hatching, like offline buildings)
- **Hover label:** Hovering over a road tile in rota view shows "Rota: Day 5 of 30" or "Next collection: tick 7" (exact format: PLACEHOLDER for Aaron).
- **Rendering:** Loop over all waste tiles in active rounds, compute their visual state from `state.tick`, draw on canvas AFTER buildings/vehicles (layering: buildings → vehicles → rota overlay → hover).
- **Test:** Enable rota overlay. Verify collected tiles are bright green THIS tick. Verify next-tick tiles are dim green. Verify far-future tiles are grey.

---

### AC-12: Determinism — Incidents and Vehicles
**Incident spawn, dispatch routing, and vehicle movement are purely deterministic (GR#21).**

- **Rule:** Given identical `SimState` + tick + random seed, all incident positions, dispatch routes, and vehicle positions are byte-identical.
- **No randomness in:**
  - Vehicle movement (always `progress = (tick - dispatchTick) / routeLength`, linear interpolation)
  - Route computation (deterministic BFS with tie-breaks at every stage)
  - Spawn probability evaluation (seeded RNG, not unseeded Math.random)
- **Test:** Save state at tick 0. Compute all incidents and vehicles for ticks 0–100. Replay from tick 0 in a fresh session. Verify all incident ids, positions, and vehicle x/y match exactly (byte-identical).

---

### AC-13: Integration with Waste System (FEAT-1972079906)
**Waste rota scheduling does NOT duplicate or override the landed collection logic (inc1/inc2).**

- **Scope:** inc1 (generation + collection coverage) and inc2 (processing mix) are DONE. inc3 adds the ROTA UI only.
- **Collection still driven by:** `collectionCapacityOf()` and `wasteStatsOf()` — one-tick aggregation, not multi-tick rounds. A depot with 50t/tick capacity collects 50t per tick it's online, regardless of rota.
- **Rota layer is:** Informational display only (player sees which streets are "scheduled" for servicing this month), NOT a gate or cost modifier. Uncollected waste penalties (from `wasteStatsOf().uncollected > 0`) are driven by capacity vs. generation, NOT by rota timing.
- **Consequence:** Rota is a transparency tool (player sees the schedule), not a hard mechanic. The real constraint is capacity + coverage, both already landed.
- **Test:** Disable all waste depots. Verify rota display shows no tiles. Enable a depot. Verify it shows serviceably tiles. Create uncollected waste. Verify waste panel reflects the shortfall (not the rota).

---

### AC-14: Interaction with Building Activation (FEAT-1972079891)
**Emergency service buildings must be online (pass activation gates) to dispatch vehicles.**

- **Gate check:** A building with `isOnline(state, b) === false` (missing road, power, water, or workers) is unavailable for dispatch. Its dispatch queue is preserved, but no vehicle is sent until it comes online.
- **Consequence:** A city without road access to a fire station can build the station, but it can't dispatch vehicles until the station is connected and has full services.
- **Test:** Build a fire station without road access. Spawn a fire. Verify incident waits in queue. Connect the station to the road. Verify it dispatches.

---

### AC-15: Incident Spawn Exclusion — Infrastructure Tiles
**Incidents do NOT spawn on roads, rail, stations, pylons, or water infrastructure.**

- **Exclusion set:** Any tile occupied by a building with `kind ∈ { 'road', 'rail', 'station', 'pylon', 'water' }` is excluded from incident spawn.
- **Reason:** Incidents happen in buildings or the streets around them, not on infrastructure.
- **Test:** Densely grid the map with roads and pylons. Spawn 100 incidents. Verify none spawn on road/rail/pylon tiles.

---

### AC-16: Rendering Integration — Vehicle Glyphs and ETA
**Moving vehicles are rendered alongside trains, roads, buildings, and power/water overlays.**

- **Render order (from back to front):**
  1. Buildings (background colour, hatching if offline)
  2. Water/power overlays (if toggled)
  3. Trains (from trains.ts)
  4. **Vehicles (NEW — fire truck, police car, ambulance icon + ETA label)**
  5. Waste rota overlay (if toggled)
  6. Disconnected road flash (from FEAT-1972079891)
  7. Hover preview / selected building outline
- **Vehicle glyph:** A small icon (emoji or SVG shape) scaled by zoom level, colored by type (red fire, blue police, green ambulance). No animation or flashing; position is a pure function of tick (deterministic).
- **ETA label:** Rendered as small text ("ETA: Nt") anchored to the vehicle glyph, using monospace font for alignment (matching building profile labels).
- **Alpha & blend mode:** Vehicle glyphs use `alpha = 1.0` (opaque). No glow or halo (defer visual flourishes).
- **Test:** Dispatch a vehicle. Verify it renders at the expected x/y for the current tick. Zoom in/out. Verify ETA label remains legible.

---

### AC-17: Determinism Tests
**All dispatch and rota logic must pass byte-identical replay.**

- **Test framework:** webconsole/test/dispatch-*.test.mjs (plain node, imports exact sim code).
- **Test 1:** Given seed S and incident spawns, create vehicles and replay. Verify all vehicle x/y/progress match exactly per tick.
- **Test 2:** Given depot layout, compute rota schedule. Save state, replay, verify rota tiles and ticks match exactly.
- **Test 3:** Combine incidents + vehicles + rota in one 100-tick run. Verify all state is byte-identical on replay.
- **False-pass note:** A test that doesn't actually verify the vehicles' positions (e.g., only checks that vehicles exist) can pass even if the movement logic is wrong. Every test must assert on concrete x/y or progress values.

---

### AC-18: Balance Numbers — Placeholder Regime
**All spawn rates, travel times, dwell periods, and rota frequencies are tuneable placeholders.**

- **Spawn rates (AC-2):** Fire/crime/medical rates are formulae with PLACEHOLDER constants (divide by 5000 / 2000 / 1500 pop). Change the divisor or per-service-type weights without code change (constant tuning only).
- **Travel time (AC-4, AC-5):** 1 tick per road segment. Can become 0.5 or 2 ticks/segment without code change (edit TRAVEL_TICKS constant).
- **Dwell time (AC-6):** DWELL_TICKS (PLACEHOLDER: 3 ticks). Tune without code change.
- **Rota frequency (AC-10):** TICKS_PER_MONTH / N depots. Tune TICKS_PER_MONTH without code change.
- **Rota budget (AC-10):** 50 tiles or 6000 cost. Tune without code change.
- **Testing guidance:** Directional tests only. Never assert "at 200 residents, expect 0.2 fires per tick" — that's tuning, not correctness. Test structure: "spawn rate is proportional to population and inversely proportional to coverage" (parameters vary, not hardcoded numbers).

---

### AC-19: Non-Acceptance Criteria (Out of Scope)

- **Incident effects:** Fires do not spread, crimes do not escalate, medical emergencies do not affect wellbeing — incidents are resolved on-arrival, full stop (no cascading effects).
- **Player notification:** No pop-up alerts or badge counters for unresolved incidents (player monitors the dispatch queue manually or via status bar — PLACEHOLDER for UI/UX decision).
- **Vehicle economics:** Dispatch does not cost money or consume fuel (vehicles are free once the building is built; operations cost is already in the building's upkeep). Costing is a future increment.
- **Incident prioritization:** No priority queue (FIFO: first-in-first-out dispatch from queue). Prioritization (e.g., fires before crimes) is deferred.
- **Weather / seasonal incidents:** No dynamic rate scaling for winter/summer or storm conditions. Rates are static (only driven by population and coverage).
- **Incident visibility on map:** Incidents are NOT marked on the map (no incident marker, no red X). Only the despatch vehicles are visible. (Incident visibility deferred to a future increment.)
- **Multi-vehicle dispatch:** One incident = one vehicle. Multi-vehicle response (3 fire trucks for a major fire) is deferred.
- **Vehicle coordination:** No traffic management or collision detection. Vehicles on the same road tile do not interact. Overlap is OK (cosmetic).

---

## Testing Strategy

### Unit Tests
- `incidentSpawnLogic(state, tick)` returns incidents with deterministic type, location, spawn rate.
- `nearestServiceBuilding(state, incident)` returns the correct building by distance and type.
- `planDispatchRoute(state, building, incident)` returns the shortest road path, deterministic.
- `computeVehicleProgress(ticket, tick)` returns correct fractional progress.
- `wasteRotaSchedule(state)` returns deterministic tile/tick assignments, sorted.

### Integration Tests
- Place buildings, spawn incidents, dispatch vehicles, verify they arrive.
- Place multiple fire stations, spawn multiple fires, verify load balancing (nearest-first dispatch).
- Verify dispatch queue processes correctly: after first incident resolved, second is dispatched from queue.
- Verify building activation gates gate dispatch: offline building cannot dispatch.

### Determinism Tests
- Save state at tick 0, compute incidents/vehicles/rota for ticks 0–100. Replay, verify byte-identical.
- Run 1,000-tick session with random spawning + dispatching. Verify no Date.now or unseeded randomness (grep + linting).

### Balance Tuning (NOT required for AC)
- Measure typical city (5,000 residents, balanced services). Count incidents per month, average response time, queue length. Tune rates/times if necessary to match design intent (delegated to Aaron's balance pass).

---

## References

- **Existing code:** trains.ts (moving-vehicle model), roadConnect.ts (route pathfinding), data.ts (incident/dispatch types, spawn rates)
- **Service buildings:** fire_post, fire_station, fire_hq, pol_station, pol_hq, hea_ambulance (SPECS)
- **Service coverage:** serviceCoverageOf(), serviceStatsOf() (GR#3 SSOT)
- **Waste system:** FEAT-1972079906 inc1 (generation/collection), inc2 (processing), inc3 (rota UI)
- **Activation gates:** isOnline() (FEAT-1972079891)
- **Determinism firewall:** GR#21, trains.ts model
- **Route storage:** SimState.incidents, SimState.wasteRounds (new fields)
- **Map rendering:** MapView.tsx lines 127–450 (building + overlay loops), trains.ts integration
- **Design decisions:** DD1–DD6 above

---

## Summary of Built vs. Missing

### Already Built (FEAT-1972079906 inc1–inc3)
- Garbage generation from residents + jobs (wasteGeneratedOf)
- Collection capacity + coverage (collectionCapacityOf, wasteStatsOf)
- Collection OPEX cost (collectionOpexOf)
- Processing mix: landfill / EfW / MRF / compost (processingMixOf)
- Diversion rate KPI (diversionRateOf)
- Processing economics: tipping fees, material revenue, compost revenue (landfillTippingOf, recyclingRevenueOf, compostRevenueOf)
- WasteTab UI panel (waste-panel-inc3.test.mjs, wasteDisplayModel)

### Missing / This Feature (FEAT-1972079864)
- **Emergency incidents:** Spawn logic, incident storage, type/location/timing
- **Dispatch queue:** Queue data structure, nearest-building routing, multi-building load tracking
- **Vehicle entity & movement:** Deterministic route computation, progress tracking, rendering
- **ETA display:** ETA formula, label rendering on MapView
- **Vehicle arrival & dwell:** Incident resolution, return-to-base mechanics
- **Waste rota scheduling:** Depot round assignment, deterministic tile/tick mapping
- **Rota map overlay:** MapView toggle, color-coded display, hover labels
- **Determinism tests:** All-new test file for incidents/vehicles/rota

### State Gaps for Developer
1. **SimState fields:** Add `incidents: Incident[]`, `nextIncidentId: number`, `wasteRounds: WasteRound[]`
2. **Types.ts:** Add `Incident`, `WasteRound`, `Vehicle` interfaces; extend `Building` with `dispatchQueue: Incident[]`
3. **data.ts:** Add `SPAWN_RATES`, `TRAVEL_TICKS`, `DWELL_TICKS` constants; add incident spawn + routing logic functions
4. **engine.ts:** Add `advance()` incident spawn + dispatch logic; add dispatch queue processing on arrival
5. **MapView.tsx:** Import `dispatchedVehicles()` function; loop rendering vehicles + ETA labels; add rota overlay toggle + rendering
6. **trains.ts:** No changes (vehicle model in dispatch.ts is separate, mirrors trains.ts pattern)
7. **New file dispatch.ts:** Extract dispatch + vehicle logic (mirrors trains.ts structure)
8. **Test file dispatch-*.test.mjs:** Unit + determinism tests per Testing Strategy
