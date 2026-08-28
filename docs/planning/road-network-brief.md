# Road Types + Auto-Connect + Traffic-Monitored Auto-Scaling — Design Brief

**Goal (Aaron, 2026-08-28):** roads become a real, self-wiring network. Multiple road TYPES (by capacity); every placed building auto-gets a **fitting connector** to the nearest existing road at placement; if the connector outranks the road it joins, **upgrade that road**; then **monitor the connector + joined road for 1 year** and **auto-scale both** as traffic demands.

Sibling of the rail brief (`docs/planning/rail-network-brief.md`) — reuses the same deterministic grid-router idea for connectors.

---

## 1. Current state
`data.ts` has `road` (Road, kind `road`, cost 40) and `m20` (M20 Motorway, kind `motorway`), plus placeholder tiers just added (rd_avenue/rd_dual/rd_aroad). There is no road CAPACITY, no auto-connection at placement, no traffic model, no upgrade/scale. Buildings are placed free-standing.

---

## 2. Road types & the "fitting" rule (inc1)
Define an ordered **road tier ladder** with a per-tile CAPACITY (vehicles/tick, PLACEHOLDER-balance):

| tier | spec | capacity (placeholder) | typical use |
|---|---|---|---|
| 1 | `road` (Lane) | small | housing, small shops |
| 2 | `rd_avenue` Avenue | med | denser residential/retail |
| 3 | `rd_aroad` A-Road | high | commercial/office districts |
| 4 | `rd_dual` Dual Carriageway | very high | industry, large footprints |
| 5 | `m20` Motorway | max | mega-facilities, city arterials |

**Fitting rule:** `fittingTier(spec)` maps a building's size/type to a minimum road tier — small footprint/low-traffic → tier 1; large footprint, industry, mega-facility, high jobs/served → higher tiers. Deterministic pure function of the spec (footprint w×h, kind, jobs/served).

---

## 3. Auto-connect at placement (inc1)
On `place(building)`:
1. If the building already touches a road, done.
2. Else find the NEAREST existing road tile (deterministic: lowest path cost, tie-break by lowest (x,y)).
3. Lay a **connector** of `fittingTier(spec)` from the building to that road, via a **deterministic grid pathfinder** (A*/BFS) that routes AROUND existing buildings (impassable), tie-broken strictly (GR#21 — no Math.random). If blocked under a cost budget, fail with a player notice (do NOT silently skip; relocation is out of scope for inc1).
4. **Upgrade-on-connect:** if the connector's tier > the joined road's tier, upgrade the joined road tile(s) at the junction to the connector's tier (a fat road shouldn't dump into a lane).
5. All laid/upgraded tiles go through the normal `place`/`upgrade` reducer actions so they are journaled (feeds the genesis-replay epic) and conservation-safe (the connector costs money through the ledger).

**Determinism is the crux:** same board + same placement → same connector route + same upgrades, every run. A determinism test asserts byte-identical placement outcomes.

---

## 4. One-year traffic monitoring + auto-scale (inc2)
After a connector is laid, register the connector + the joined road segment for **monitoring for 1 in-game year** (a fixed tick window). Each tick (or on a monthly aggregate), compute the **traffic load** on those segments = f(the buildings feeding them: population/jobs/freight, deterministic), compare to the segment capacity, and when load exceeds a threshold, **auto-upgrade (scale)** the connector and/or the joined road one tier (up to the ladder max), re-charging through the ledger. At the end of the monitoring year, the segment settles at whatever tier the traffic demanded. Monitoring state is per-segment sim state (deterministic, tick-driven — no wall-clock).

- **Traffic model (placeholder):** a coarse per-segment load from the connected buildings (reuse the commuter-weight idea from `stationLinks`). Full traffic sim is out of scope; this is a demand-vs-capacity scalar.
- **Auto-scale both:** the connector AND the road it joined expand together as needed.

---

## 5. Increments
- **inc1 — Types + auto-connect + upgrade-on-connect.** Road tier ladder + capacity fields, `fittingTier`, the deterministic connector router (around buildings, fail-with-notice if blocked), upgrade-on-connect, all via journaled reducer actions + conservation. Tests: fitting math, router determinism + around-buildings, upgrade-on-connect, journal/conservation, blocked→notice.
- **inc2 — Traffic monitoring + auto-scale.** Per-segment monitoring for 1 year, deterministic traffic load, auto-upgrade connector + joined road on saturation, ledger-charged, settles at year end. Tests: load→upgrade threshold, both-scale, deterministic over the window, monitoring expiry.
- **inc3 (stretch) — visualisation.** Show road tier (colour/width) + saturation on the map (BUG-425-aware colours), reuse formatPower-style scaling for capacity display.

---

## 6. Open questions for Aaron
1. **Blocked connector:** fail-with-notice (recommended inc1), or allow deterministic building-relocation later (as with rail)?
2. **Capacity & scale numbers:** placeholder-balance (I build directional, you sign off)?
3. **Traffic granularity:** coarse per-segment demand scalar (recommended) vs a real vehicle sim (big, later)?
4. **Auto-scale aggressiveness:** upgrade one tier per saturation event, or jump straight to the tier the load implies?
5. **Scope home:** webconsole-first (as with water/rail); Go `engine.roads` later.

---

## 7. Risks
| Risk | Mitigation |
|---|---|
| Router / auto-scale non-determinism breaks replay/consistency (GR#21) | Strict deterministic tie-breaking, tick-driven, no Date/random; determinism tests. |
| Auto-laid roads break conservation/journal | Lay/upgrade via the normal reducer actions so they journal + conserve (feeds FEAT-1972079897). |
| Perf: pathfinding + per-tick monitoring at scale | Bounded A* with a cost budget; monitoring runs on a monthly aggregate, expires after 1 year; cache segment geometry. |
| Auto-connect surprises the player (unexpected roads/spend) | Show the connector cost in the ledger ("Started Connector Road"); the notice on a blocked/failed connect. |
| Upgrade churn (endless re-scaling) | One tier per saturation event, capped at ladder max, monitoring window bounded to 1 year. |

---

*Design brief 2026-08-28. Webconsole-first. Deterministic connector router + tick-driven monitoring are the crux. Pairs with the rail brief's router.*
