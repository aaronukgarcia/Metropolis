# Rail Network: Capacity, Live Trains, Demand-Scaled Service & Auto-Branch-Lining — Design Brief

**Goal (Aaron, 2026-08-27):** make rail feel alive and self-wiring. Lines (incl. the M20) carry a capacity; trains visibly stop at stations and multiply with demand (more trains, then more lines); and placing a major gateway (Ashford International, the international airport / "Heathrow") **auto-lays branch lines** to both the slow rail line and the high-speed line — bidirectional, auto-routed around existing buildings (or by relocating a blocker, whichever is simpler in code) — with trains rendered by usage.

---

## 1. What exists today (webconsole)
- Specs: `m20` (motorway), `road`, `rail` (slow "Rail Line"), `hs1` (HS1 High-Speed Line), `station_sanderling`, `station_ashford` (Ashford International — HS1 gateway, `served 60,000`, ×3 commuter weight), `grand_terminus`, `metro_station`, `bus_station`. Kinds: `road`/`rail`/`station`/`motorway`.
- `stationLinks(s)` → `connectedIds` + a `stationWeight` that already feeds population growth (a connected Ashford weights ×3). So connectivity is modelled as a boolean/weight, not as flow.
- A `train` object DIMENSION exists (carriage 23×2.7×4 m) but trains are not simulated moving or rendered stopping.
- **Missing:** line capacity, train movement/stops, demand-scaled service (more trains/lines), auto-branch-lining, bidirectional double-track, usage rendering.

---

## 2. The asks, decomposed
1. **Line capacity** — `m20` and rail lines carry a throughput capacity; usage vs capacity is visible (saturation).
2. **Trains stop at stations** — render trains travelling a line and pausing at each station.
3. **Demand-scaled service** — as commuter demand rises, show MORE trains, then MORE lines. Rail service is a function of demand, not static.
4. **Auto-branch-lining on gateway placement** — placing `station_ashford` auto-lays a branch to the nearest **slow** (`rail`) line AND the **HS** (`hs1`) line. Same for the international airport ("Heathrow"): auto-lay slow + HS rail. Routed **around existing buildings**, or by **moving a blocking building** — whichever is simpler in code.
5. **Bidirectional** — auto-laid rails are double-track / bidirectional.
6. **Trains rendered with usage** — train glyphs coloured/sized by how loaded the line is (ties into #1/#3).

---

## 3. Design

### 3.1 Line capacity & usage (inc1)
Give `road`/`m20`/`rail`/`hs1` a per-tile throughput capacity (spec field, PLACEHOLDER-balance). Compute usage per line/segment from the commuter flow it carries (derive from population × commuter weight routed over connected segments — extend `stationLinks`/`stationWeight` from a boolean into a flow). Surface usage/capacity as a saturation ratio (0..1) per line, shown on the map (colour) and in the info panel — reusing the existing meter idiom, and honouring BUG-425's surplus-vs-shortfall colour distinction.

### 3.2 Live trains + stops (inc2)
Render trains moving along connected rail polylines, pausing at each `station` on the route. Train COUNT on a line = f(demand served / capacity) — more commuter demand → more train glyphs, spaced along the line. Trains coloured/sized by usage (#6). **Determinism (GR#21):** train positions must be a pure function of `(tick, line geometry, demand)` — NO `Date.now`/`Math.random`; animate from `tick` so replays and the consistency checker stay byte-identical. Train rendering is UI-derived from state, not stored in SimState.

### 3.3 Demand-scaled service — more trains then more lines (inc2/inc3)
Stage the response to rising demand: first add trains on the existing line (up to its capacity), then, when a line saturates, auto-add a parallel line / branch (inc3, reuses the router). A deterministic threshold ladder (PLACEHOLDER-balance), not a live feed.

### 3.4 Auto-branch-lining + the router (inc3 — the keystone)
On placing a gateway spec (`station_ashford`, the airport spec), fire an auto-router that lays a branch connecting the station to:
- the nearest existing **slow** `rail` line, AND
- the nearest existing **HS** `hs1` line.
Both **bidirectional** (double-track — lay paired/annotated segments).

**Router algorithm — deterministic grid pathfinder:**
- A* / BFS on the tile grid from the station's edge to the target line's nearest reachable tile.
- Cost = path length + a turn penalty (prefer straight/gently-curved runs); **impassable = tiles occupied by existing buildings** → the path routes AROUND them.
- **Fully deterministic (GR#21):** no `Math.random`; tie-break strictly by lowest `(x,y)` then lowest direction ordinal, so the same board always yields the same route.
- **Blocked case:** if no clear route exists under a cost budget, EITHER (a) relocate the single cheapest blocking building to the nearest free tile (Aaron: "move any building… whichever is easier"), or (b) fail with a player notice. **Recommendation: pathfind-around is the default; building-relocation is a last-resort fallback behind a flag** — relocation is surprising and must itself be deterministic + captured (it mutates the city). Never silently destroy a building.
- All laid segments + any relocation go through the normal `place`/`bulldoze`/`move` reducer actions so they are journaled (feeds the genesis-replay epic) and conservation-safe.

### 3.5 M20 / roads
The M20 (`m20`) gets the same capacity/usage treatment as rail (inc1). Road auto-connection is out of scope here unless it falls out of the router cheaply.

---

## 4. Increments
- **inc1 — Capacity & usage:** capacity spec fields for `m20`/`road`/`rail`/`hs1`; commuter-flow usage per line (extend stationLinks to flow); saturation display (BUG-425-aware colours). Tests: usage math, capacity display, determinism.
- **inc2 — Live trains:** deterministic tick-driven train glyphs that traverse lines and stop at stations; count scales with demand; usage colouring. Tests: determinism (byte-identical train positions per tick), stop-at-station, demand→count.
- **inc3 — Auto-branch-lining:** the deterministic router; on `station_ashford` / airport placement, lay bidirectional branches to nearest `rail` + `hs1`, routing around buildings (relocation fallback behind a flag); auto-add a parallel line when a line saturates. All via journaled reducer actions. Tests: router determinism, around-buildings, both-lines-connected, blocked→fallback, journal/conservation.

---

## 5. Open questions for Aaron
1. **Router blocked case:** pathfind-around only (fail-with-notice if impossible), or allow deterministic building-relocation as a fallback? (Recommend around-only first; relocation later behind a flag.)
2. **"Heathrow":** the game has an International Airport spec — treat THAT as the airport gateway that triggers auto-rail? (No separate "Heathrow" spec unless you want one.)
3. **Capacity & train-count numbers:** placeholder-balance (I build directional, you sign off) — confirm.
4. **Demand→more-lines:** auto-add parallel lines when saturated (inc3), or only add trains and leave new lines to the player? 
5. **Scope home:** webconsole prototype first (as with the other systems), Go engine `transport`/`roads` later?

---

## 6. Risks
| Risk | Mitigation |
|---|---|
| Router non-determinism breaks replay/consistency (GR#21) | Strict deterministic tie-breaking, no Date/random; determinism test on the router. |
| Building-relocation silently destroys/moves player work | Default to pathfind-around; relocation only behind a flag, deterministic, journaled, never a silent bulldoze; capture pairs with BUG-420. |
| Train rendering leaks non-determinism into SimState | Trains are UI-derived from (tick, geometry, demand); nothing stored in SimState. |
| Auto-laid segments break conservation/journal | Lay via the normal place/move reducer actions so they journal + conserve (feeds FEAT-1972079897 replay). |
| Perf: pathfinding on a large occupied grid | Bounded A* with a cost budget; cache line geometry; only run on gateway placement, not per tick. |

---

*Design brief 2026-08-27. Webconsole-first. Deterministic router is the crux. Awaiting Aaron's answers on §5.*
