# FEAT-1972079928: Road re-planning on placement

**Road layout optimization: when new tiles are placed, nearby existing roads are re-routed on an optimal path to respect hierarchy and avoid sprawl.**

**Mkey:** FEAT-1972079928

**Relates:** FEAT-1972079910 (contiguous-roads epic, inc1–3 all landed: anchored tracker, auto-junctions, rail-bridge/motorway-junction premiums), FEAT-1972079857 (sweep guide / radius rules), FEAT-1972079881 (auto-roundabout tier rules). Northstar waypoint 2 (dogfood road UX — player can build sensible street layouts, not breadcrumbs).

**GR#25:** `webconsole/src/sim` (`engine.ts` road replanning logic, `data.ts` hierarchy weights and cost models). No new `code.json` edge unless arc/path-finding is delegated to a utility module.

## Rationale

FEAT-1972079910 (landed) solved the breadcrumb fragmentation by anchoring the path and computing it deterministically in tile space — no more sampling gaps. But it places roads exactly as drawn: if a player drags a new road near an existing network, that existing network is not reconsidered. Road re-planning addresses layout UX: when the player places a new tile or new path, the system reconvenes nearby existing roads to find a better-connected layout (shorter routes, prefer higher tiers, avoid dumping minor roads into major ones, motorways stay uncongested with few junctions).

Aaron's vision: a soft-preference planner that recomputes costs and routes around newly-placed tiles. The re-plan respects road hierarchy — a collector avenue is BETTER than a local road for a through-route, but it's not a hard fail if only a local road exists. Motorway premiums already exist (FEAT-1972079910 inc3); re-planning must NOT *add* new motorway junctions beyond what a spacing/count rule permits. The entire re-plan is deterministic, journaled (like `placeRoadPath`), and atomic — all-or-nothing funds.

## Evidence (why this is in Northstar)

The current (landed) contiguous-roads system places roads contiguously but does not optimize their layout in response to the environment. Early dogfood cities show clusters of minor roads dumping into major roads at sub-optimal points, and motorway interchanges placed closer than strategy suggests. Re-planning gives the player a city-building tool: place a new avenue spine, and nearby locals re-route through it; place a motorway segment, and the junction spacing adjusts to your preferred granularity. Without it, the player must manually demolish and re-lay roads to fix their own layout — a friction point that undermines the contiguous-laying UX.

## Design

The re-planning system is triggered **after** a `placeRoadPath` action succeeds (funds charged, tiles placed, junctions auto-converted). It scans a square radius around the newly-placed tiles and identifies nearby roads that could be rerouted. The algorithm then:

1. **Enumerate candidate paths** from each nearby road to a sensible destination (a junction, a major road, or an off-map exit).
2. **Score each candidate** using a cost function: distance + hierarchy preference (soft) + motorway penalties + turn smoothness penalties.
3. **Atomically reroute** those roads if the re-plan saves cost or improves hierarchy. No funds are charged for the re-plan itself (the tiles already exist and were paid for in the original placement); if a re-plan involves upgrading a tile, the upgrade cost is charged and must be affordable.
4. **Preserve the player's intent:** If a road was explicitly placed by the player (e.g., a local road in a residential area), do not re-plan it away unless the player explicitly asks (see design question: non-destructive default below).

## Acceptance Criteria

### AC-1 (re-plan radius and nearby-tile detection)

A newly-placed road path triggers a search radius `REPLAN_RADIUS_TILES` (placeholder const, TBD) around the **bounding box** of all placed/converted tiles. All existing road buildings whose footprint overlaps or touches this radius are identified as "nearby roads" for re-planning consideration.

**Check:** Place a road at tile (50, 50), `REPLAN_RADIUS_TILES = 3`. Nearby roads are those with at least one tile in the square [47, 47] to [53, 53] (the placed tile ±3 on both axes). Place a road tile at (54, 50) (outside the radius) — it is NOT nearby. Place a road at (47, 50) (on the radius edge) — it IS nearby. Read the internal `replanSearch` result or inspect the re-plan journal payload.

**Mutation:** Set `REPLAN_RADIUS_TILES = 0` (no nearby roads found); the test goes red (re-plan finds no candidates to reroute).

**False-pass:** Checking the re-plan count without verifying the spatial logic (a re-plan that finds ANY road is not proof the radius is correct).

### AC-2 (optimal path computation: deterministic cost function)

The re-planning algorithm computes candidate paths for each nearby road using a cost function: `cost = distance + (hierarchy_mismatch_penalty × hierarchy_weight) + (motorway_turnoff_count × motorway_penalty)`. The cost function is **purely deterministic** — all inputs are from the map state (road tiers, tile positions, existing motorway junction counts), never from Date/random/pointer events.

**Check:** Two separate re-plan passes on identical city states yield identical re-plan actions (same roads rerouted, same new tiles, same costs). Serialize the state before/after, replay the first re-plan action, then manually trigger the second re-plan with the same state snapshot; the journal entries are identical (structure, order, costs). **False-pass:** Both re-plans succeed but have different road choices (cost function is non-deterministic). **Caveat:** randomness in the search order (which nearby road is re-planned first) is acceptable as long as the final set of reroutes is identical, because order independence is the proof of determinism.

### AC-3 (hierarchy as soft preference: avenue is better than local, not mandatory)

The cost function includes a hierarchy multiplier: routing through an avenue tile is **cheaper** than routing through a local road tile, but if an avenue does not exist, the planner routes through local anyway. The hierarchy is a **soft preference weight**, not a hard constraint.

**Check:** 
- Scenario A: A nearby local road can reach a destination via a new avenue (cost 5 + hierarchy weight 10 = 15) or via existing locals only (cost 8 + no weight = 8). The planner chooses locals only (cost 8 < 15 after applying the weights).
- Scenario B: Place an avenue such that the same local road can now reach it (cost 5 + hierarchy weight 5 = 10) vs locals (cost 8 + no weight = 8). The planner still chooses locals (cost 8 < 10).
- Scenario C: Place an avenue such that cost 3 + hierarchy weight 2 = 5 vs locals cost 8 + 0 = 8. The planner now chooses the avenue (5 < 8).

Read the cost breakdown in the re-plan journal action (include per-tile hierarchy score + distance). Mutation: set hierarchy weight to 1000 (mandate); the planner always prefers avenue, even if costlier. Test goes red in Scenario A (planner should choose locals but prefers avenue).

**False-pass:** A test that checks hierarchy word appears in the code without validating the cost calculation.

### AC-4 (anti-sprawl: minor-into-major connection limiting)

The re-planner detects when a newly-placed major road (avenue or above) is being fed by multiple low-tier roads dumping directly into it. It suppresses new direct minor-to-major connections beyond a threshold (e.g., max 2 direct local roads per avenue tile). Instead, it routes minor roads through intermediate tiles (other locals, a collector) to avoid congestion.

**Check:** A city with a new avenue tile at (50, 50). Three local roads in the vicinity attempt to connect directly to this tile (one from the west, one from south, one from northeast). The re-planner allows the first two (within threshold) but re-routes the third to connect via an intermediate local road or collector instead of direct-to-avenue. Examine the re-plan journal: the third local road's new path does not include the avenue tile as a direct neighbor. **Mutation:** set the threshold to 0 (no direct minor-to-major); all three reroute away. Test goes red if the re-planner failed to reroute all three.

**False-pass:** A test that counts the final road count but does not verify the connectivity pattern (the roads could still feed the avenue, just indirectly, or the re-plan might have failed entirely).

### AC-5 (motorway junction scarcity: minimum spacing and count rules)

When the re-planner re-routes roads that cross a motorway, it enforces a minimum spacing rule: no two motorway junctions on the same motorway segment may be closer than `MOTORWAY_JUNCTION_MIN_SPACING_TILES` (placeholder const) apart. Additionally, a motorway segment may have at most `MOTORWAY_JUNCTION_MAX_PER_SEGMENT` junctions; beyond that, roads are rerouted away or downgraded to bypass the motorway.

**Check:** A motorway runs east–west from (40, 50) to (60, 50). Place a new north–south collector that would cross the motorway at (50, 50) and create a motorway junction. Next, place another collector that would cross at (52, 50) — within the min-spacing distance. The second collector's re-plan recognizes the spacing violation and either (a) reroutes the second collector away from the motorway (using a grade-separated underpass, if available, or a detour), or (b) merges the second collector into the first junction. Read the re-plan journal action and verify the second collector does not place a new motorway junction within `MOTORWAY_JUNCTION_MIN_SPACING_TILES` of the first.

**Mutation:** set `MOTORWAY_JUNCTION_MIN_SPACING_TILES = 0` (no minimum); the planner allows junctions at any spacing. Test goes red if the planner fails to enforce the spacing rule.

**False-pass:** Verifying the first junction was created (no proof the second was suppressed or rerouted) or checking only the max-count rule without the spacing rule.

### AC-6 (determinism + journaling: atomic all-or-nothing funds)

The entire re-plan is executed as a single atomic journal action: `{ type: 'replanRoads'; affectedRoads: [...]; newTiles: [...]; conversions: [...]; cost: number }`. If the total cost of the re-plan (new tiles + upgrades) exceeds available funds, the re-plan is NOT applied and the state is unchanged. The re-plan can be replayed deterministically from the journal.

**Check:** A city with funds F. Place a new road path whose re-plan would cost R. If F ≥ R, the re-plan action is journaled and funds become F − R. If F < R, the re-plan is not applied, funds remain F, and the journal action is **not recorded** (no "attempted re-plan" ghost). Replay the game from genesis: the same state deterministically triggers the same re-plan, with identical tiles rerouted and identical cost charged.

**Mutation:** Delete the all-or-nothing check (allow partial re-plans); the test goes red when the re-plan is applied halfway and funds go negative or the state diverges on replay.

**False-pass:** A test that checks the `placeRoadPath` action alone, without verifying the subsequent `replanRoads` action was journaled.

### AC-7 (non-destructive default: re-plan does not demolish existing player roads)

By default, the re-planner optimizes connection patterns but does **not** demolish or downgrade existing roads laid by the player. A road the player explicitly placed stays on the map unless the player explicitly demolishes it. The re-planner may (a) upgrade a road to a higher tier if hierarchy demands it, or (b) reroute traffic through other paths by adding new connector tiles, but it does not remove tiles the player placed.

**Check:** A city with a local road at (50, 50), laid by the player. A new avenue is placed at (51, 50), adjacent to the local. The re-planner does NOT demolish the local. It may upgrade it to a connector tier (e.g., collector) if the hierarchy logic demands it, or add new tiles to better integrate it, but the player's original local road remains. Inspect the final buildings list: the local road building ID is unchanged (or updated in place if upgraded, not deleted). **Mutation:** enable re-plan demolition of sub-optimal roads. The test goes red if the player's original local road is deleted.

**False-pass:** A test that checks the avenue was placed but does not verify the local road's fate (it might be demolished but the test does not assert).

### AC-8 (interaction with landed auto-junction convert-in-place: no double-conversion)

FEAT-1972079910 inc2 (landed) auto-converts existing road tiles to junctions when a new road crosses them (convert-in-place model, id preserved, no extra cost). The re-planner must **not** conflict with or double-apply this logic: (a) when `placeRoadPath` places a road over an existing road, the convert-in-place happens immediately (inc2); (b) the re-planner then runs on the **already-converted** state. The re-planner does not re-convert a junction or undo the convert-in-place.

**Check:** A local road exists at (50, 50). Place an avenue that crosses (50, 50). FEAT-1972079910 inc2 converts the local to `rd_roundabout` (avenue tier, id preserved, placed at tick T). The re-planner then runs, seeing `rd_roundabout` at (50, 50). It does **not** convert it again or attempt to reverse the conversion. The roundabout remains the same building ID, same tick, with spec `rd_roundabout`. Inspect the final building: one entry at (50, 50), spec = roundabout, id unchanged. **Mutation:** re-planner attempts to convert an already-junction tile; the test goes red (duplicate conversion or ID collision).

**False-pass:** A test that only checks the final spec without verifying the building ID or ticket are stable (a new building might be placed on top of the old one, passing the spec check).

## Placeholder Constants

| Constant | Meaning | Initial Estimate | Notes |
|----------|---------|------------------|-------|
| `REPLAN_RADIUS_TILES` | Distance (tiles) from newly-placed path bbox to search for nearby roads | 4 | Aaron TBD; larger = more aggressively re-plans distant roads; smaller = more conservative |
| `HIERARCHY_WEIGHT_MULTIPLIER` | Cost multiplier for prefer-higher-tier routing | 1.0–2.0 | Soft preference: higher values prefer avenue/A-road more strongly; balance regime pending |
| `MOTORWAY_JUNCTION_MIN_SPACING_TILES` | Minimum tiles between motorway junctions on the same segment | 6 | From FEAT-1972079910 inc3 motorway-junction premium; Aaron TBD |
| `MOTORWAY_JUNCTION_MAX_PER_SEGMENT` | Max junctions on a motorway segment between major exits | 4 | Prevents motorway congestion; Aaron TBD |
| `ANTI_SPRAWL_MAJOR_DIRECT_LIMIT` | Max direct minor roads feeding a single major road tile | 2 | Avoid hub-and-spoke on major roads; re-route excess through collectors |
| `REPLAN_UPGRADE_COST_MULTIPLIER` | Fraction of base tier cost charged when re-plan upgrades a road | 0.5 | Half-price upgrades (re-using existing footprint); balance regime |

## Design Decisions

**D1: Soft hierarchy, not hard constraint.** Road tier is a *preference weight*, not a mandate. A tier-1 local road is always available if no higher tier exists, avoiding un-routable dead ends. This is consistent with FEAT-1972079910's design: tiers describe throughput and cost, not network topology requirements.

**D2: Re-plan triggered after `placeRoadPath` succeeds.** The re-planner runs as a post-placement pass. It observes the newly-placed tiles and recomputes nearby routes *given those new tiles*. If the re-planner itself fails (e.g., insufficient funds for an upgrade), the original placement stands (all-or-nothing for re-plan, separate from placement).

**D3: No player-invisible demolition.** By default, re-planning does not demolish roads the player laid. This respects player intent and avoids surprise road removal during normal play. If a re-plan requires demolition (e.g., a road is fundamentally misplaced), it is surfaced as a multi-step: "Demolish this road (cost X) and re-connect?" — player approval required. **Aaron's ruling pending on whether re-plan may suggest demolitions or always preserve.**

**D4: Motorway scarcity reuses FEAT-1972079910 inc3 premium costs.** The motorway junction cost (£250k flat) is already paid by `placeRoadPath`. Re-planning enforces the *spacing* rule, not additional cost (the spacing rule is a capacity/UX constraint, not a budget one).

## Open Questions for Aaron

1. **Re-plan radius value:** What distance in tiles should trigger re-planning? Early suggestion: 4 tiles from the placed path bbox (a reasonable neighborhood). If 0, re-planning is disabled (MVP: no re-planning, just contiguous laying).

2. **Re-plan aggressiveness on hierarchy:** How much preference weight should hierarchy carry? A local road that's 3 tiles away via avenue vs 8 tiles via locals only — should the planner prefer the avenue? The current placeholder `HIERARCHY_WEIGHT_MULTIPLIER = 1.5` is a guess.

3. **Upgrade cost for re-planned roads:** If a re-planner upgrades a local to an avenue (e.g., because it's now a through-route), should the player pay full upgrade cost (£50 − £250) or a fraction (reusing the footprint)? Placeholder: 50% of the new tier's cost. Or, should up-tier be free (cost already paid via the major road that triggered the re-plan)?

4. **Demolition authority:** May the re-planner suggest demolition of a player-placed road (e.g., "this dead-end local would be better removed")? Or is re-planning *only* additive (new tiles, upgrades, rerouting via alternatives, never demolition)? Current default: non-destructive (no demolition unless player approves via a separate action). **Aaron's ruling requested.**

5. **Motor way junction scarcity:** Should the spacing rule be per-motorway-segment (each motorway tile or continuous section) or per-motorway-network (all m20 tiles globally)? Placeholder: per-segment (a motorway that bends counts as separate segments). Aaron's preference?

6. **Interaction with FEAT-1972079881 (auto-roundabout tier rules):** When re-planner upgrades a junction to a higher tier, does it trigger the auto-roundabout rules from 1881 (e.g., avenue-tier junction becomes roundabout)? Or is the re-planner's decision independent? Current: reuse 1881's tier→junction spec mapping.

## Out of Scope

- FEAT-1972079910 inc2's convert-in-place logic (landed; re-planner observes it).
- FEAT-1972079881 auto-roundabout rules (landed; re-planner defers to them).
- Balance tuning of the hierarchy weights or costs (Aaron's balance pass, once criteria are approved).
- Road demolition tools for the player (separate feature; re-planner does not demolish by default).
- Pathfinding optimizations (Dijkstra vs A* vs b-tree) — left to implementer once the cost function is approved.
- Traffic simulation or congestion feedback (future; re-planning does not yet observe live traffic).
- Undo/redo of re-plan actions (journaled but no explicit undo UI; player can hard-reset via replay if needed).

## Implementation Increments (Suggestion)

**Inc1:** Radius detection + cost function skeleton. Identify nearby roads within `REPLAN_RADIUS_TILES`. Compute baseline cost (distance + road tier) for each existing road, before and after placing the new path. Determine which roads can be cheaper via the new tiles. Journal the re-plan decision (no mutations yet).

**Inc2:** Hierarchy soft preference + anti-sprawl. Add hierarchy multiplier to cost. Detect minor-into-major sprawl patterns and suppress or reroute excess connections. Validate hierarchy changes (avenue tier is cheaper than local) in a tester round.

**Inc3:** Motorway junction scarcity. Enforce min-spacing and max-per-segment rules when re-routing roads that would cross motorways. Validate via tester that junctions are spaced and capped correctly.

