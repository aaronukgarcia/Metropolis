# FEAT-1972079891: Building Activation Prerequisites

**Feature:** Webconsole building activation prerequisites — connected road grid, road-side adjacency, water, power, and workers gates.

**Mkey:** FEAT-1972079891

## Overview

Extends the building activation gate beyond construction-time to enforce five prerequisites:
1. Road-side adjacency: building occupies a tile orthogonally adjacent to a road tile
2. Road connectivity: that road is part of a connected road network (not an isolated segment)
3. Water capacity: clean water supply covers this building's population
4. Power capacity: electricity supply covers this building's demand
5. Worker availability: employment capacity covers this building's population

Offline buildings render distinctly (under-construction hatching precedent) with WHY information via hover tooltip. Disconnected road segments flash as a warning. All gates are deterministic (same inputs → same output set per tick).

---

## Design Decisions Flagged for Lead

### DD1: Road Network Root/Anchor
**What defines a "connected" road network?** Currently no anchor exists. Options:
- **Option A (CHOSEN UNLESS OVERRULED):** Road network is connected if reachable via orthogonal adjacency from ANY of these seeds:
  - Map edges (global boundaries at x=0, x=MAP_W-1, y=0, y=MAP_H-1)
  - Motorway tiles (m20, hs1, rail network — "trunk infrastructure")
  - Designated town-hall/civic anchor building (name TBD)
- **Option B:** Single designated town hall is the only root — all roads must connect to it
- **Option C:** Multiple seed points (stations, ports, major landmarks) form independent networks; buildings can connect to any

**Consequence:** Option A is permissive (orphan roads far from town still activate buildings if someone connects them). Option B is restrictive (single point of failure). Option C is complex but realistic.

**Flag for Aaron:** Confirm which option, and if Option A, which building spec (if any) acts as civic anchor.

---

### DD2: Disconnected Road Flash Cadence and Colour
**Visual spec for orphan road segments:**
- **Layer:** Rendered AFTER buildings, ON TOP of the road tile (not blended with building)
- **Trigger:** `isRoadDisconnected(state, roadTile) === true` (new helper)
- **Cadence (PLACEHOLDER):** Sinusoidal pulse 500 ms period (visible 250 ms, dim 250 ms)
  - `alpha = 0.5 + 0.3 * Math.sin((state.tick * SPEED_MS[state.speed] / 500) * Math.PI * 2)`
- **Colour:** Warn yellow from the palette (PLACEHOLDER, e.g., `#ffd166` or `#e3b341`)
- **Flag for Aaron:** Confirm cadence ms, alpha range, colour code

---

### DD3: Inactive Building Behaviour
**What do offline buildings do?**
- **AC-4a (no services):** Service coverage consumption is ZERO for offline buildings (power/water/workers not drawn)
- **AC-4b (no production):** Offline buildings generate ZERO jobs, residents (housing), or tax income
- **AC-4c (zero upkeep):** Offline buildings pay ZERO upkeep (maintenance suspended)
- **AC-4d (refund risk):** Offline status is persistent across saves/loads (not "in progress" construction state)

**Consequence:** A city with no road network can build infinitely without breaking cash flow — no upkeep, no service draw. This is correct (buildings literally can't operate), but will feel strange in early game. Flag to confirm this is intentional.

**Flag for Aaron:** Approve zero-upkeep rule for offline buildings.

---

### DD4: Legacy Save Game Handling
**What happens to buildings already active in saved games that would NOW fail the new gates?**
- **Option A (GRACE PERIOD):** Existing active buildings remain online for 30 ticks, then re-evaluate
- **Option B (LEGACY PASS):** Buildings placed before this feature release never subject to new gates (flag with metadata)
- **Option C (HARD REVERT):** All buildings re-evaluate immediately; orphaned buildings go offline and may wreck cash flow
- **Option D (MIGRATION AT LOAD):** On save load, scan for buildings that fail gates, auto-bulldoze them with 50% refund, log to ledger

**Consequence:** Option A is forgiving but delays the gate. Option B breaks parity (future and legacy saves differ). Option C is harsh but consistent. Option D is clearest (explicit ledger entry).

**Flag for Aaron:** Choose enforcement model and grace period (if any).

---

## Acceptance Criteria

### AC-1: Road Connectivity Graph
**Deterministic flood-fill computes the connected road network.**

- **Definition:** Two road tiles are directly connected if orthogonally adjacent (dx+dy=1, no diagonals).
- **Root:** For Option A, connectivity graph includes any road reachable from:
  - Map edges (boundary tiles connect the grid)
  - Motorway/trunk tiles (m20, hs1, rail, station seed tiles — full list in code comment)
  - Town hall (if designated anchor exists)
- **Computation:** Computed fresh every tick during `advance()` before any gate evaluation (deterministic, no randomness, same inputs always produce same connected set).
- **Storage:** Cached in `SimState` as `roadConnectivity: { connectedRoadTiles: Set<string> }` (keyed as `"x,y"` strings like `stationLinks.connectedIds`).
- **Test:** Unit test: given a map with three separate road segments (one connected to edge, two isolated), verify connectedRoadTiles contains only the edge-connected segment.

---

### AC-2: Road Adjacency Check per Building
**Buildings must occupy a tile orthogonally adjacent to at least one road tile.**

- **Rule:** Building is "road-side" iff any of its footprint tiles has an orthogonal neighbour (N/S/E/W, not diagonal) that is a road tile.
- **Scope:** Applies to all building kinds except network/landmarks (roads, pylons, stations, m20, rail, hs1 do not need road-side adjacency; they ARE infrastructure).
- **Implementation:** New helper `isRoadAdjacent(state: SimState, b: Building): boolean`.
- **Test:** Place a residential building at (100, 50). Place a road at (100, 51). Verify isRoadAdjacent returns true. Move building to (100, 52) (now two tiles away). Verify returns false. Diagonal road at (101, 51) should return false.

---

### AC-3: Road Connectivity Gate per Building
**Building is "road-connected" iff its road-adjacent tile touches a road in the connected network.**

- **Rule:** Building is online only if `isRoadAdjacent(state, b) && adjacentRoadTile ∈ state.roadConnectivity.connectedRoadTiles`.
- **Scope:** Same as AC-2 (all non-infrastructure buildings).
- **Determinism:** Identical to other deterministic gates (same state → same result, verifiable from debug snapshot).
- **Test:** Create two road segments: (100, 50)–(100, 56) connected to map edge, and (200, 50)–(200, 56) isolated. Place housing at (100, 49) and (200, 49). Verify first is road-connected, second is not (both are road-adjacent).

---

### AC-4: Building Activation Gate — All Prerequisites
**Building goes online iff ALL five gates hold. Fails deterministically if ANY gate fails.**

Each gate failure is tracked separately for UI (AC-5 WHY tooltip):
- **G1 (construction):** `isOnline(state, b)` existing gate: `tick - b.builtTick >= constructionTicks(spec)`.
- **G2 (road-adjacent):** `isRoadAdjacent(state, b) === true`.
- **G3 (road-connected):** Road adjacency + connected network (AC-3).
- **G4 (water):** `serviceCoverageOf(state).find(s => s.id === 'cleanwater')?.coverage >= 1.0` (or see AC-9 caveat).
- **G5 (power):** `serviceCoverageOf(state).find(s => s.id === 'power')?.coverage >= 1.0` (or see AC-9 caveat).
- **G6 (workers):** Building kind is 'residential' OR `serviceCoverageOf(state).find(s => s.id === 'employment')?.coverage >= 1.0`.

**Extended isOnline():**
```typescript
function isOnline(s: SimState, b: Building): boolean {
  const sp = SPECS[b.spec];
  if (!sp || sp.category === 'network') return true; // infrastructure always on
  if (b.builtTick == null) return true; // placed but not yet constructed — still online during build
  // G1: construction time
  if (s.tick - b.builtTick < constructionTicks(sp)) return false;
  // G2, G3: road gates (skip for roads/pylons/stations)
  if (sp.kind !== 'road' && sp.kind !== 'pylon' && sp.kind !== 'station') {
    if (!isRoadAdjacent(s, b)) return false;
    if (!isRoadConnected(s, b)) return false;
  }
  // G4: water
  const water = serviceCoverageOf(s).find(r => r.id === 'cleanwater');
  if (water && water.coverage < 1.0) return false;
  // G5: power
  const power = serviceCoverageOf(s).find(r => r.id === 'power');
  if (power && power.coverage < 1.0) return false;
  // G6: workers (residential exempt, others need jobs capacity)
  if (sp.kind !== 'residential') {
    const jobs = serviceCoverageOf(s).find(r => r.id === 'employment');
    if (jobs && jobs.coverage < 1.0) return false;
  }
  return true;
}
```

- **Test:** Building with all prerequisites met goes online. Remove any one prerequisite (e.g., cut the road) → goes offline. Restore it → goes online again.

---

### AC-5: WHY Tooltip — Building Offline Reason
**Selected/hovered offline building displays which prerequisite(s) fail.**

- **Trigger:** When building is offline (`isOnline(state, b) === false`) and player hovers or selects it.
- **UI Location:** Extend `BuildingCard` component (MapView.tsx lines 711–772) to include reason.
- **Reason Text (PLACEHOLDER — directional only):**
  - If G1 fails: `"Under construction — {remaining} ticks remaining"` (existing, keep as is)
  - If G2 fails: `"Not road-side — move adjacent to a road"`
  - If G3 fails: `"Road not connected — connect to the main road network"`
  - If G4 fails: `"Insufficient water supply"`
  - If G5 fails: `"No power — build generators or power plants"`
  - If G6 fails (jobs): `"No jobs available — zone commercial or industrial"`
  - **Multiple failures:** List all failed gates, e.g., `"Missing: water, power, workers"`.
- **Implementation:** Add computed field to BuildingCard:
  ```typescript
  const failedGates = computeFailedGates(state, building);
  ```
  Then render as comma-separated list or multi-line.
- **Test:** Offline building due to no water shows "Insufficient water supply". Offline building due to no road and no power shows "Not road-side, No power".

---

### AC-6: Disconnected Road Flash Layer
**Orphan roads (not in connected network) render with pulsing overlay.**

- **Render location:** Canvas layer in MapView.tsx `effect` (lines 127–348), AFTER building render loop, BEFORE hover preview.
- **Trigger:** `roadConnectivity.connectedRoadTiles` is computed (AC-1), any road tile NOT in set is orphaned.
- **Visual:** Sinusoidal alpha pulse (cadence per DD2), drawn as semi-transparent rectangle overlay on the road tile (same footprint as the road building itself).
- **Colour:** DD2 nominated colour (PLACEHOLDER flag).
- **Code shape:**
  ```typescript
  const orphanRoads = state.buildings.filter(b => 
    SPECS[b.spec]?.kind === 'road' && 
    !state.roadConnectivity.connectedRoadTiles.has(`${b.x},${b.y}`)
  );
  for (const road of orphanRoads) {
    const alpha = 0.5 + 0.3 * Math.sin((state.tick * SPEED_MS[state.speed] / 500) * Math.PI * 2);
    ctx.globalAlpha = alpha;
    ctx.fillStyle = FLASH_COLOR; // DD2 choice
    ctx.fillRect(px + 0.5, py + 0.5, sp.w * geom.s - 1, sp.h * geom.s - 1);
  }
  ctx.globalAlpha = 1;
  ```
- **Test:** Create isolated road segment (not touching map edge or any connected road). Verify it flashes with configured cadence. Connect it to main network → flash stops.

---

### AC-7: Service Coverage Sourcing — SSOT (GR#3)
**Building activation gates consume serviceCoverageOf(state) as the single source of truth.**

- **Scope:** Water, power, and workers coverage must NOT be re-derived; they MUST read from the same `serviceCoverageOf()` call that demand meters and wellbeing use.
- **Consequence:** If a building goes offline due to power shortfall, the demand index rises by the same formula (no contradiction).
- **Relation to BUG-393:** Power coverage sourced from `serviceCoverageOf()` is `cap / need` (same as `brownoutOf.deficitRatio = 1 - cap/need`). No parallel power calculation.
- **Test:** Place a power plant that covers 100% of population demand. Verify `serviceCoverageOf()[power].coverage >= 1.0`. Verify building stays online. Add population to exceed capacity → coverage < 1.0 → building goes offline.

---

### AC-8: Determinism
**Building activation state is identical across replay, debug snapshots, and CI.**

- **Rule:** Given identical `SimState`, gate evaluation produces identical online set.
- **No randomness:** No Monte Carlo, no frame-dependent flashing, no Date.now() in gates.
- **Idempotent tick:** Calling `advance(state)` twice with the same input does NOT change activation status twice (it's deterministic, not accumulating).
- **Test:** Save state at tick 100. Compute activation set. Replay from tick 100 in a fresh session. Verify activation set matches exactly (by building id set).

---

### AC-9: Balance Numbers — Tuneable Thresholds (Placeholder Regime)
**All prerequisite thresholds are placeholders pending Aaron's balance pass.**

- **Current thresholds (DIRECTIONAL ONLY):**
  - Water/power/workers coverage must be ≥ 1.0 (i.e., no deficit).
  - These can be tuned downward (allow 80% coverage to activate) without code change — modify the `>= 1.0` comparison in AC-4.
  - **No hardcoded building-level tuning:** The gate does not say "residentials need 0.8 coverage but factories need 0.95" — it is uniform per service.
  
- **Testing guidance:** Directional tests only. Never assert "at 92 residents and 5 water plants, X buildings are online" — that's balance tuning, not correctness. Test structure: "given coverage ratio C, building activation matches coverage rule" (C is a parameter, not a literal number).

---

### AC-10: Migration & Backward Compatibility
**Saved games remain valid; building activation behavior is switchable.**

- **On load:** If save predates this feature, buildings are migrated per DD4 chosen option.
  - **Option C (hard revert):** Buildings fail gates immediately, going offline. Map may break cash flow (users will see "oh, that building is offline now"). Ledger shows which buildings went offline (new log entry per building).
  - **Option A (grace period):** Loaded buildings get a `graceTick` field (added to `Building` type), initially set to `state.tick + 30`. Gate evaluation skips for buildings where `state.tick < graceTick`. After grace period, gates apply normally.

- **Spec:** Add comment in `isOnline()` documenting the migration rule chosen.

---

### AC-11: Interaction with Brownout (BUG-393 SSOT)
**Building power gate reads the same coverage as demand index and brownout severity.**

- **Rule:** Power coverage = `serviceStats(state).find(r => r.id === 'power')?.coverage`.
- **No separate brownout gate:** Brownout (AC-4 severity) determines income penalty and wellbeing multiplier, but does NOT gate activation. A city can still build with deficit; the consequences (lost income, low wellbeing) are consequences, not the prerequisite.
- **Consequence:** If coverage < 1.0, both building goes offline AND demand index rises AND income is penalized.
- **Test:** Create power deficit (need > cap). Verify buildings stay online if coverage >= 1.0 (shortfall but not deficit). If coverage < 1.0, verify buildings go offline AND brownoutOf().active === true AND demand index reflects deficit.

---

### AC-12: Road Network Changes Trigger Instant Re-evaluation
**When a road is placed or removed, affected buildings re-evaluate connectivity immediately (same tick).**

- **Rule:** `advance()` computes road connectivity at the START of the tick. All gate checks use that frame's connectivity graph.
- **Consequence:** Building placed at (100, 50) orthogonal to a connected road goes online immediately (no delay tick). Building's road is demolished → building goes offline immediately.
- **No transition animation:** Unlike construction, which has a visible in-progress state, connectivity gates are binary on/off per tick.
- **Test:** Place building adjacent to orphan road (building offline). Connect the road to main network in same tick. Verify building comes online (no "wait a tick" period).

---

### AC-13: Rendering Integration — isOnline Marker
**Under-construction hatching (MapView.tsx line 203) extends to offline (non-construction) buildings.**

- **Current code:**
  ```typescript
  if (!online && geom.s > 3) {
    ctx.strokeStyle = 'rgba(255,255,255,0.7)';
    // diagonal hatching...
  }
  ```
- **Change:** Condition already handles all offline states (construction, missing roads, missing services, missing workers). No code change needed if `isOnline()` extended per AC-4.
- **Visual:** Offline buildings render with alpha 0.45 (line 183: `const baseAlpha = b.id === state.movingId ? 0.6 : online ? 1 : 0.45`). Hatching is shown. This is distinct from online (alpha 1.0).
- **Test:** Offline building due to missing water renders dimmed with hatching, same as under-construction building. On-line building renders full alpha, no hatching.

---

### AC-14: Upkeep / Income / Capacity Disabled for Offline Buildings
**Offline buildings contribute ZERO to every economy/capacity flow.**

- **Power consumption:** Offline buildings do NOT consume MW (power need excludes them).
- **Water consumption:** Offline buildings do NOT consume litres.
- **Worker demand:** Offline buildings do NOT provide jobs or consume workers.
- **Residents:** Offline residential buildings do NOT house population.
- **Upkeep:** Offline buildings pay ZERO upkeep (not counted in `computeFlows()` expense).
- **Income:** Offline buildings generate ZERO tax, trade income.
- **Consequence:** City with no road network can zone unlimited free buildings without effect (upkeep = 0, capacity = 0, demand = 0).

- **Implementation:** Line 235 of engine.ts already gates computeFlows on `isOnline()`:
  ```typescript
  for (const b of s.buildings) {
    if (!isOnline(s, b)) continue; // skip offline
    const sp = SPECS[b.spec];
    if (!sp || !sp.upkeep) continue;
    // ... add upkeep
  }
  ```
  No change needed if AC-4 `isOnline()` extended correctly. Verify all income/upkeep/capacity flows already use `isOnline()` check.

- **Test:** Offline building contributes nothing to power need, water need, job count, upkeep budget, income. Online building of same type contributes normally.

---

### AC-15: Units Consistency
**All building prerequisites measured in units from code.json registry.**

- **Power:** MW (unit: megawatts; from `powerStats()` and `serviceCoverageOf()`)
- **Water:** Liters (kL/day; from water plant capacity and `waterCaps()`)
- **Workers:** Persons (from `totalJobs()` and population * 0.55)
- **Population (housing):** Persons (from residential capacity and `s.population`)

No mixed units (e.g., "10 power points" or fuzzy "enough water"). See code.json `units` section for registry.

---

## Non-Acceptance Criteria (Out of Scope)

- **Visual flourishes:** Glow effects, particle systems, or animations beyond the flashing (DD2) are not required.
- **Partial activation:** Buildings do not activate "partially" based on surplus/deficit degree. It is binary: prerequisites met or not.
- **Grace period UI:** No countdown timer or notification when a building is about to lose connectivity (would be nice, but not required for AC).
- **Road type precedence:** Motorways, HS1, rail, and regular roads all count as roads for adjacency. No preferential "major road" boost.
- **Regional networks:** No distinction between "eastern network" and "western network" — all connected roads are one network.
- **Fire spread:** Offline buildings do not spread fire, disease, or other cascading failures. (Simplification for baseline one.)

---

## Testing Strategy

### Unit Tests
- `isRoadAdjacent(state, building)` returns true iff building tile has orthogonal road neighbour.
- `isRoadConnected(state, building)` returns true iff adjacent road is in `roadConnectivity.connectedRoadTiles`.
- `computeRoadConnectivity(state)` produces deterministic connected component.
- `isOnline(state, building)` combines all gates; each gate can be independently tested.
- `computeFailedGates(state, building): FailedGate[]` returns array of gate names that fail (for AC-5 tooltip).

### Integration Tests
- Place building, check online status after place, after road demolition, after road connection, after power/water removal.
- Verify upkeep, income, and capacity all track online/offline status.
- Verify demand index and wellbeing do not contradict activation state.

### Determinism Tests
- Save state, compute activation set, replay, verify set unchanged.
- No timing-dependent flashing in gate logic (only rendering).

### Balance Tuning (NOT required for AC)
- Measure typical city with 1,000 population; count online vs. offline buildings by type.
- Tune coverage thresholds if necessary to match design intent (delegated to Aaron's balance pass).

---

## References

- **Current code:** `webconsole/src/sim/engine.ts`, `data.ts`, `MapView.tsx`
- **Existing gates:** `isOnline()` (construction time), `stationLinks()` (station road adjacency)
- **SSOT coverage:** `serviceCoverageOf()` (power, water, jobs — GR#3 BUG-392)
- **Power deficit:** `brownoutOf()` (BUG-393) — does NOT gate activation, only penalizes income/wellbeing
- **Rendering:** Canvas loop (MapView.tsx lines 127–348)
- **Design decisions:** BUG-392 (coverage SSOT), BUG-393 (brownout mechanics), DD1–DD4 above
