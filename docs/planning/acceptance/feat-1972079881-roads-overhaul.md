# FEAT-1972079881: Webconsole Roads Overhaul

**Feature:** Webconsole roads overhaul — road type hierarchy, snap/magnetic glue placement, sweeping bends, Milton-Keynes-derived grid-fill tool with auto-roundabouts.

**Mkey:** FEAT-1972079881

## Overview

Extends the roads system from a single 1×1 'road' tile to a multi-type hierarchy (path/street/avenue/boulevard/motorway; placeholder names for Aaron's review) supporting:
1. **Road type hierarchy:** differentiated by speed, capacity, cost, visual appearance
2. **Snap/magnetic glue placement:** endpoint snapping (PLACEHOLDER radius TBD) and axis-alignment when dragging near existing roads
3. **Sweeping bends:** curves (not sharp kinks) using a simplified polyline or arc-segment model
4. **Grid-fill tool:** places a road grid at Milton-Keynes-derived spacing (~1 km grid, PLACEHOLDER-mapped to game tiles) with roundabouts auto-placed at intersections
5. **Roundabouts as objects:** distinct spec with multi-tile footprint, connectivity treated as orthogonally-adjacent roads

All road types participate in the building-activation road-connectivity gate (FEAT-1972079891); the feature is backward-compatible with saved games.

---

## Design Decisions Flagged for Lead

### DD1: Road Type Hierarchy — Catalogue & Visual Distinction
**What are the road types and how do they differ?**

**Proposed (PLACEHOLDER for Aaron's review):**
- **Path** (1×1): foot-only, £20 cost, £1 upkeep, colour #7a8696 (grey-green)
  - Connects parks, playgrounds, pedestrian areas
  - Does NOT support vehicles; buildings adjacent to paths only cannot activate
- **Street** (1×1): local residential, £40 cost, £3 upkeep, colour #4a525c (current road grey)
  - Supports cars; buildings activate on street adjacency + connectivity
  - Current 'road' spec becomes 'street'
- **Avenue** (1×1): urban collector, £80 cost, £6 upkeep, colour #3a4856 (darker grey)
  - Supports buses + cars; visual distinction (thicker painted lines or lane marks)
  - Speed hint 40 km/h (unlocks at level 4)
- **Boulevard** (1×1): major urban, £150 cost, £12 upkeep, colour #2a3846 (very dark grey)
  - Supports buses, trams, cars; dual-carriageway visual hint
  - Speed hint 60 km/h (unlocks at level 6)
- **Motorway** (1×1): intercity trunk, £300 cost, £20 upkeep, colour #1d5fa8 (current m20 blue)
  - High-speed, bypass-only (does not activate adjacent buildings)
  - Current 'm20' spec remains; motorway is its alias in the catalogue

**Rendering distinction:**
- Path: thin lines, dashed or dotted
- Street: solid lines, neutral
- Avenue: thick lines with lane marks (parallel dashes)
- Boulevard: dual lanes (two parallel lines, gap between)
- Motorway: bold lines, distinct colour

**Testing guidance:** Each type's spec (`id`, `kind='road'`, `cost`, `upkeep`, `color`, `unlock`) is stored and retrievable. A palette palette tree shows all types in unlock order.

**Flag for Aaron:** Approve the type names, costs, upkeep values, unlock levels, speed hints, and visual rendering approach.

---

### DD2: Snap & Magnetic Glue Placement
**How does the player connect roads to existing roads?**

**Proposed (PLACEHOLDER-flagged values):**
- **Endpoint snap radius (PLACEHOLDER):** When dragging a road tile within N tiles (PLACEHOLDER: 3) of an existing road endpoint, snap the new road to align with the endpoint (N/S/E/W align, no diagonal).
  - Example: drag a street tile toward an existing street at (100, 50). If within snap distance, placing at (100, 51) automatically aligns to (100, 49) or (100, 51) to connect orthogonally.
- **Axis alignment (PLACEHOLDER):** When dragging a road tile toward an existing road perpendicular axis (e.g., horizontal road exists; placing vertical), snap to the nearest orthogonal-axis intersection.
  - Example: avenue at (100, 50)–(105, 50) exists. Dragging a street tile at (102, y) downward snaps to (102, 51) to connect perpendicularly.
- **Extend-existing behaviour:** Placing a road adjacent to an existing road of the same type automatically extends the line if in-plane.
  - Example: street segment (100, 50)–(100, 52) exists. Placing a street at (100, 53) automatically connects without gaps.
- **No snap to motorway:** Motorway tiles do NOT participate in snap/glue to regular roads (motorway is separate infrastructure).

**Implementation:**
- New function `snapRoadTarget(state: SimState, tool: Tool, hoverTile: {x, y}, snapRadius: number): {x: number, y: number}`.
  - Computes nearest road endpoint within radius.
  - Returns snapped tile coordinates or unmodified hover tile.
- Modify placement preview (MapView.tsx lines 328–347) to show snapped position in white if snap is active; red if blocked.

**Testing:**
- Place a street. Hover near its endpoint: snap indicator shows.
- Place a second street at snapped position: verifies it connects orthogonally.
- Drag a street perpendicular to an avenue: verifies axis-alignment snap.
- Motorway tiles do not snap to street/avenue (separate network).

**Flag for Aaron:** Confirm snap radius (in tiles), snap visual feedback (white/green colour), enable/disable snap per road type (motorway always off?), and tuning of extend-existing logic.

---

### DD3: Sweeping Bends — Curve Model
**How are curves stored and rendered?**

**Proposed (PLACEHOLDER for structural choice):**
- **Model:** Simplified polyline. A "bend" is a sequence of tiles that forms a curve, stored as a list of (x, y) vertices.
  - Example: `[{x: 100, y: 50}, {x: 101, y: 51}, {x: 102, y: 52}]` describes a diagonal sweep.
  - Minimum bend radius: 2 tiles (no tight 90° kinks; must have smooth turn).
- **Rendering:** Canvas line-join `round` to smooth vertices at render time.
  - No arc parametrization or bezier — just polyline rendering with rounding.
- **Placement:** User draws by dragging mouse; each mouse move adds a vertex if it's at least 1 tile away from the last.
  - Preview shows the polyline as a dashed line; snapping rules apply to the start point only.
- **Storage:** Extended `Building` type to include optional `roadCurve?: {x: number, y: number}[]` field for non-straight roads.
  - Straight road has `roadCurve` === undefined or `null`.

**Alternative DD3B (arc-segment model, if polyline proves insufficient):**
- Store bends as arc specs: `{startTile: {x, y}, direction: N|S|E|W, radius: number, angle: degrees}`.
- Renders as drawn arcs (Math.cos/sin in canvas drawing).
- More compact but more complex.

**Rendering requirements:**
- Line width matches road type (thicker for avenue/boulevard).
- Colour matches road type.
- Curves visible at zoom ≥ 4 (geom.s > 4).
- Zoom levels test: zoom out (s < 3) curve is simplified to straight line; zoom in (s > 8) curve details visible.

**Testing:**
- Draw a curve by dragging mouse S-shaped: verifies vertices capture the curve.
- Render at zoom levels 1, 4, 8, 48: verify curve is visible and smooth at all levels.
- Reload saved game with curves: verify `roadCurve` array is preserved.
- A curve whose minimum radius is < 2 tiles is rejected (error tooltip: "Curve too sharp").

**Flag for Aaron:** Confirm polyline vs arc-segment model, minimum bend radius (2 tiles?), and line-join rendering (rounded vs beveled).

---

### DD4: Grid-Fill Tool — Milton-Keynes Spacing
**What does the grid-fill tool do, and at what spacing?**

**Proposed (PLACEHOLDER values):**
- **Real-world reference:** Milton Keynes uses a hierarchical grid: 1 km × 1 km supergrid with neighbourhood grids at 200 m spacing (a 5×5 block per superblock).
- **Game mapping (PLACEHOLDER — Aaron's call):** 1 km = 20 tiles (50 m/tile × 20 = 1000 m).
  - Supergrid: 20 × 20 tile spacing (1 km).
  - Neighbourhood: 4 × 4 tile spacing (200 m).
  - **Proposed mode for baseline:** Supergrid only (1 km); neighbourhood mode is deferred to FEAT-XXX.
- **Activation:** Right-click (or long-press) on the map after selecting the grid-fill tool. Drag a rectangle (corner A to corner B) to select the fill area.
- **Placement:** Grid-fill places roads on a 20×20 tile lattice, filling the rectangle (x ∈ [minX, maxX], y ∈ [minY, maxY], step 20).
  - Respects road type selected in tool (all placed roads are the same type).
  - All placed roads snap-connect to neighbours.
- **Auto-roundabout:** At each grid intersection (where N/S road meets E/W road), a roundabout is placed.
  - Roundabout is a 2×2 tile building (footprint).
  - If the space is occupied, skip that intersection (no error; silent failure per balance).
- **Cost & upkeep:** Sum of all placed roads + roundabouts is charged upfront. Placement fails if insufficient funds.

**Implementation:**
- New tool mode: `gridFill` (extends `ToolMode`).
- New function `gridFillRectangle(state: SimState, spec: string, x1: number, y1: number, x2: number, y2: number, gridSize: number = 20): {buildings: Building[], cost: number}`.
  - Returns list of buildings to place + total cost.
  - Does NOT modify state; preview only.
- UI button in palette to activate grid-fill mode.
- Drag preview: show outlined rectangles for each road and roundabout that will be placed, green if affordable, red if funds insufficient.

**Testing:**
- Select grid-fill, choose 'street' type, drag a 40×40 tile rectangle: verifies 2×2 grid of streets (at 0, 20, 40, 60) + 1 roundabout (at 20, 20) is previewed.
- Verify cost = (4 streets × £40) + (1 roundabout × TBD) = £160 + roundabout cost.
- Place grid, verify all roads are connected orthogonally and roundabout sits at intersection.
- Reload saved game: verify grid is persisted.

**Flag for Aaron:** Confirm grid spacing (20 tiles = 1 km?), roundabout size (2×2), auto-roundabout enable/disable, neighbourhood-level grid deferred or included.

---

### DD5: Roundabout as an Object
**What is a roundabout in the sim, and how does it participate in connectivity?**

**Proposed:**
- **Spec:** New building spec `roundabout` (kind='road', not a zone).
  - Size: 2×2 tiles (footprint).
  - Cost: PLACEHOLDER £200.
  - Upkeep: PLACEHOLDER £15/tick.
  - Colour: #6a7a8a (lighter grey, visually distinct from roads).
  - Unlock: level 1 (seed infrastructure, or deferred?).
- **Connectivity:** A roundabout is treated as a road tile for connectivity purposes.
  - Building-activation road-connectivity check (FEAT-1972079891) treats all 4 tiles of the roundabout as part of the road network.
  - Orthogonal roads (N/S/E/W of the roundabout's edges) are connected through it.
- **Rendering:** Rendered as a circle or rounded square (distinct from rectangular roads).
  - Rotary fill at roundabout centre (static, no animation in baseline).
- **Placement rules:**
  - Only placed by grid-fill; manual placement disabled (or unlock at very high level).
  - Cannot be placed on top of existing buildings.
  - When grid-fill encounters an occupied intersection, that roundabout is skipped (no error).

**Alternative DD5B (roundabout is visual only):**
- Roundabout is NOT a separate building; it's a visual decoration placed automatically where two road types cross.
- No spec, no upkeep, no cost — purely cosmetic.
- Simplifies state but loses flexibility for later traffic simulation.

**Testing:**
- Place two perpendicular road lines via grid-fill, verifying roundabout appears at their intersection.
- Verify building-adjacent roundabout (distance 1 tile) counts as road-adjacent for activation.
- Bulldoze a road leading to a roundabout, verify other connected roads remain connected through the roundabout.
- Roundabout rendering matches road hierarchy (colour, scale).

**Flag for Aaron:** Approve roundabout as a building spec vs visual-only, size (2×2), cost, upkeep, placement rules, and rendering style.

---

### DD6: Determinism
**Same road inputs → same layout.**

**Proposed:**
- Road placement is deterministic: given identical click/drag sequences and state, the layout is identical.
- No randomness in snap/glue, curve drawing, or grid-fill.
- Grid-fill is deterministic: identical rectangle input → identical road/roundabout positions.
- Rendering is deterministic: same road list → same canvas output (no flickering, no animation except for highlight/selection).

**Testing:**
- Save state, place roads via user input, save result.
- Replay from the same state, repeat input, verify road list matches exactly (same ids, positions, specs, curves).
- Load in a fresh browser session, verify layout is identical.

**Non-determinism sources to avoid:**
- Date.now() in placement logic (allowed in rendering/animation only).
- Snap decisions that depend on frame timing.
- Randomised roundabout placement (always at grid intersections).

---

### DD7: Cost & Zoning Interaction
**Do roads cost money? Are they free?**

**Proposed (from FEAT-1972079882 zoning precedent):**
- **Zoning is free:** All 'zones'-category buildings (residential, commercial, industrial, parks, etc.) cost £0 to place.
- **Roads cost money:** Roads are 'network' category, NOT zones. Road types (path, street, avenue, boulevard, motorway) retain their `cost` field and are charged when placed.
  - Street: £40 per tile.
  - Avenue: £80 per tile.
  - Boulevard: £150 per tile.
  - Motorway: £300 per tile.
  - Path: £20 per tile.
- **Roundabouts cost money:** PLACEHOLDER £200 (per grid-fill placement).
- **Upkeep is charged:** Roads incur upkeep per tick (existing implementation in engine.ts line 235 already gates on `isOnline()`; road upkeep continues).
- **Refund on bulldoze:** Bulldozing a road returns 50% of its original cost to funds.

**Testing:**
- Place a street (cost £40): verify funds decrease by £40.
- Bulldoze the street: verify funds increase by £20 (50% refund).
- Place housing (free zone) next to a street: verify housing costs £0, street still costs £40.
- Insufficient funds for road: placement blocked (error: "Insufficient funds for road placement").

**Flag for Aaron:** Confirm road costs are kept as-is; confirm refund percentage (50%?).

---

### DD8: Legacy Save Game Handling
**Saved games from before roads overhaul: what happens?**

**Proposed (Option: No migration needed):**
- Saved games contain a `buildings` array with spec='road' (the old single type).
- On load, old 'road' specs are silently re-keyed to 'street' (the new default).
- Curves field (`roadCurve`) is absent; roads render as straight lines.
- Roundabouts (new spec) do not exist in legacy saves.
- All road connectivity gates (FEAT-1972079891) work unchanged.

**Alternative DD8B (Migration with new-type promotion):**
- On load, scan for buildings with spec='road'.
- Promote some to higher types (e.g., 20% to avenue, 5% to boulevard) based on connectivity degree.
  - Roads with ≥4 neighbours → avenue.
  - Roads with ≥8 neighbours → boulevard.
- This adds visual interest to old saves without requiring user edits.

**Testing:**
- Load a saved game from before roads overhaul.
- Verify old roads are still present, still connected, still activate adjacent buildings.
- Verify no new road types appear unless explicitly placed.
- Verify curves field is absent (straight lines only).

**Flag for Aaron:** Approve no-migration or auto-promotion approach.

---

## Acceptance Criteria

### AC-1: Road Type Catalogue Expansion
**The game includes N road types (PLACEHOLDER: path, street, avenue, boulevard, motorway), each with distinct spec properties.**

- **Catalogue:** SPECS includes entries for `path`, `street`, `avenue`, `boulevard`. The existing `m20` remains (or is aliased as `motorway`).
- **Type properties:**
  - `id`: distinct string (e.g., 'street', 'avenue').
  - `kind`: 'road' for all.
  - `name`: human-readable (e.g., "Street", "Avenue").
  - `w`, `h`: 1, 1 (all are single tiles, except roundabout).
  - `cost`: PLACEHOLDER-flagged (path £20, street £40, avenue £80, boulevard £150, motorway £300).
  - `upkeep`: PLACEHOLDER-flagged per DD1.
  - `color`: distinct hex codes (PLACEHOLDER: greys for path–boulevard, blue for motorway).
  - `unlock`: PLACEHOLDER level (path/street level 1, avenue level 4, boulevard level 6, motorway level 99 seed).
  - `category`: 'network' (not zones).
- **Rendering:** Each type is visually distinct (line style, thickness, colour).
  - Path: dashed/dotted, thin.
  - Street: solid, normal.
  - Avenue: lane marks, thicker.
  - Boulevard: dual-lane visual, darker.
  - Motorway: bold, blue.
- **Palette:** All road types appear in a "Roads" family in the build palette, ordered by unlock level.
- **Test:** `SPECS['street'].cost === 40 && SPECS['avenue'].unlock === 4`. User can select each type from palette and see distinct preview.

---

### AC-2: Snap & Magnetic Glue Placement Semantics
**When dragging a road near an existing road, the new road snaps to connect orthogonally.**

- **Snap radius (PLACEHOLDER):** N tiles (default: 3). When a road drag is within N tiles of an existing road endpoint, display snap visual and constrain placement.
- **Snap direction:** Snap occurs along the orthogonal (N/S/E/W) axis only; no diagonals.
  - Example: dragging a street downward toward an existing street at (100, 50) snaps to (100, 51) if within snap radius.
- **Axis alignment:** When dragging perpendicular to an existing road, snap to the nearest intersection on that road's axis.
  - Example: avenue at (100, 50)–(105, 50) exists (horizontal). Dragging a street downward around (102, 54) snaps to (102, 51) (vertical connection).
- **Preview:** Snap target is shown as a white or green outline (PLACEHOLDER colour per DD2); blocked snap is red.
- **Extend-existing:** Placing a road adjacent to an existing road of the same type and in-line automatically connects without gaps.
- **Motorway exclusion:** Motorway tiles do NOT snap to or receive snaps from street/avenue/boulevard (separate network).
- **Function signature:** `snapRoadTarget(state, tool, hoverTile, snapRadius): {x, y}` returns snapped position or unmodified hover.
- **Test:**
  - Place street at (100, 50). Drag a street toward it from (100, 60). Verify snap preview shows at (100, 51). Place: verifies connection.
  - Place avenue at (100, 50)–(104, 50). Drag street downward at (102, 60). Verify snap to (102, 51). Place: verifies T-junction.
  - Drag motorway near street: verify motorway does not snap (no blue → grey connection).

---

### AC-3: Sweeping Bends — Curve Model & Rendering
**Roads can be curved; curves are smooth (minimum radius 2 tiles) and render distinctly.**

- **Model (DD3 polyline):** Curves are stored as optional `roadCurve?: {x: number, y: number}[]` array on Building, containing waypoint vertices.
  - Straight road: `roadCurve` is null/undefined.
  - Curved road: `roadCurve` is `[{x, y}, {x, y}, ...]` with ≥3 points (start, middle, end).
- **Minimum radius:** Any bend with a turn angle ≥ 90° must have ≥ 2 intermediate tiles (Chebyshev distance) to smooth the turn.
  - Example: curve from (100, 50) → (101, 51) → (102, 52) is valid (45° per step = smooth).
  - Curve from (100, 50) → (101, 50) → (101, 51) is TOO SHARP (90° turn in 1 tile) and rejected.
- **User input:** User drags mouse to draw curve. Each mouse move ≥ 1 tile away from last vertex adds a vertex.
  - Start point: first click (subject to snap).
  - Dragging: adds intermediate vertices.
  - Release: finalizes curve.
  - Preview shows dashed polyline as user drags.
- **Rendering:**
  - Canvas `lineJoin = 'round'` to smooth vertices.
  - Line width and colour match road type.
  - Curves visible at zoom ≥ 4; simplified to straight line at zoom < 3 (performance).
  - Curve cost: PLACEHOLDER: charged per tile traversed (e.g., curve through 5 tiles = 5 × road type cost).
- **Backwards compatibility:**
  - Legacy roads (no `roadCurve` field) render as straight.
  - Saving/loading preserves `roadCurve` array.
- **Test:**
  - Draw S-curve by dragging: verify `roadCurve` array has ≥5 points.
  - Render at zoom 1, 4, 8, 48: verify curve is visible/smooth at zooms 4+ and simplified at zoom 1.
  - Draw a too-sharp curve (90° in 1 tile): verify error tooltip "Curve too sharp".
  - Bulldoze a curve: verify `roadCurve` is deleted.
  - Reload saved game with curves: verify `roadCurve` persists and renders correctly.

---

### AC-4: Grid-Fill Tool — Milton-Keynes Grid with Auto-Roundabouts
**Dragging a rectangle on the map places a road grid with roundabouts at intersections.**

- **Tool activation:** New tool mode `gridFill`. Button in palette or right-click context menu.
- **User interaction:** 
  - Select grid-fill tool.
  - Select road type (path/street/avenue/boulevard) from toolbar or palette.
  - Drag-paint a rectangle on the map (corner A to corner B).
  - A preview shows outlined roads and roundabouts.
  - Release to place all roads at once (or cancel).
- **Grid spacing (PLACEHOLDER):** 20 tiles (1 km real-world equivalent).
  - Roads placed at x = x_min, x_min + 20, x_min + 40, …, x_max (every 20 tiles).
  - Roads placed at y = y_min, y_min + 20, y_min + 40, …, y_max (every 20 tiles).
- **Road placement:**
  - Horizontal roads: placed at grid y-coordinates, spanning x_min to x_max.
  - Vertical roads: placed at grid x-coordinates, spanning y_min to y_max.
  - All roads are the selected type (e.g., all streets if street selected).
- **Roundabout placement:**
  - At each grid intersection (x, y) where a horizontal and vertical road meet, place a roundabout building (2×2 footprint, centred at (x, y) → occupies (x, y), (x+1, y), (x, y+1), (x+1, y+1)).
  - If the space is occupied, skip that intersection (silent; no error).
  - Roundabout spec: `roundabout` (DD5).
- **Cost calculation:**
  - Horizontal roads: (x_max - x_min) / 20 × road cost.
  - Vertical roads: (y_max - y_min) / 20 × road cost.
  - Roundabouts: count of placed roundabouts × roundabout cost (DD5).
  - Total cost displayed in preview; placement fails if funds insufficient.
- **Placement:**
  - All roads are placed via standard placement logic (occupancy checked, funds deducted).
  - All roads snap-connect to neighbours (via AC-2 snap).
  - Roundabouts occupy their 2×2 footprint; no buildings can overlap.
- **Function:** `gridFillRectangle(state, spec, x1, y1, x2, y2, gridSize=20): {buildings: Building[], cost: number}`.
  - Returns list of buildings (roads + roundabouts) + total cost.
  - Does not modify state (preview only).
- **Test:**
  - Select street type, drag 40×40 rectangle: preview shows 2×2 grid of streets (at 0, 20, 40, 60) + 1 roundabout (at 20, 20).
  - Verify cost = (4 streets × £40) + (1 roundabout × £200) = £360 (PLACEHOLDER).
  - Place grid: verify all roads connect, roundabout sits at intersection.
  - Place grid with occupied space: verify only unoccupied intersections get roundabouts (no error).
  - Reload saved game: verify grid is persisted.

---

### AC-5: Roundabout as a Building Spec (DD5)
**Roundabout is a 2×2 building that serves as a road junction and counts as network infrastructure.**

- **Spec:**
  - `id`: 'roundabout'.
  - `kind`: 'road'.
  - `name`: "Roundabout".
  - `w`, `h`: 2, 2 (occupies 4 tiles).
  - `cost`: PLACEHOLDER £200 (placed via grid-fill only).
  - `upkeep`: PLACEHOLDER £15/tick.
  - `color`: #6a7a8a (lighter grey, distinct from roads).
  - `category`: 'network'.
  - `unlock`: 1 (seed/grid-fill only).
- **Connectivity:** All 4 tiles of the roundabout are treated as road tiles for building-activation connectivity (FEAT-1972079891).
  - A building adjacent to any edge of the roundabout counts as road-adjacent.
  - Roads orthogonally adjacent to the roundabout's edges connect through it.
- **Rendering:**
  - Drawn as a circle or rounded square (distinct from rectangular roads).
  - Colour #6a7a8a.
  - Static (no animation; rotary centre is visual only).
  - Visible at all zoom levels.
- **Placement:**
  - Auto-placed by grid-fill at intersections (AC-4).
  - Manual placement disabled (or unlock at very high level, deferred).
  - Occupancy checked: if any of the 4 tiles are occupied, skip that roundabout.
- **Bulldoze:** Roundabout can be bulldozed like any building; refund is 50% of cost (DD7).
- **Test:**
  - Place grid-fill; verify roundabout appears at one intersection.
  - Verify roundabout occupies 2×2 footprint in occupancy set.
  - Place building adjacent to roundabout edge: verify building is road-adjacent.
  - Bulldoze a road leading to roundabout; verify other roads remain connected through roundabout.
  - Reload game: verify roundabout persists with 2×2 footprint.

---

### AC-6: Determinism
**Given identical user inputs and state, road placement (including snaps, curves, grid-fill) is deterministic.**

- **Snap is deterministic:** Same hover position + state → same snap target (no randomness).
- **Curve drawing is deterministic:** Same drag path + state → same `roadCurve` vertices.
- **Grid-fill is deterministic:** Same rectangle + state → same road/roundabout list (same ids, positions, specs).
- **No non-deterministic sources:**
  - No Date.now() in placement logic.
  - No frame-dependent decisions.
  - No randomised roundabout placement (always at grid intersections).
- **Rendering is deterministic:** Same road list → identical canvas pixels (except for hover/selection highlights).
- **Test:**
  - Save state at tick 100. Place roads via drag input. Record final road list.
  - Reload state, repeat drag input, verify road list matches exactly (same specs, positions, curves, ids).
  - Replay in fresh browser session: verify identical layout.

---

### AC-7: Cost & Zoning Interaction (from FEAT-1972079882)
**Roads cost money per tile; zoning (residential, commercial, industrial, parks) remains free.**

- **Road cost:** Each road type has a `cost` field (path £20, street £40, avenue £80, boulevard £150, motorway £300 — PLACEHOLDER).
  - `placementCost(spec)` returns spec.cost for roads (category='network').
  - Placement charges funds immediately (or placement fails if insufficient funds).
- **Roundabout cost:** PLACEHOLDER £200 per roundabout (from DD5).
- **Zoning cost:** All 'zones'-category buildings (residential, commercial, industrial, parks) cost £0 to place.
  - `placementCost(spec)` returns 0 for zones.
- **Upkeep:** Roads incur upkeep per tick (existing engine.ts line 235 already gates on `isOnline()`).
  - Roundabouts incur upkeep (PLACEHOLDER £15/tick).
- **Refund:** Bulldozing a road/roundabout returns 50% of placement cost.
- **Test:**
  - Place a street (cost £40): verify funds decrease by £40.
  - Place housing (free): verify funds unchanged, housing still placed.
  - Bulldoze street: verify funds increase by £20 (50% refund).
  - Place grid-fill (cost £360 for 4 streets + 1 roundabout): verify funds decrease by £360; insufficient funds blocks placement.

---

### AC-8: Legacy Save Game Handling
**Saved games from before roads overhaul remain valid; old roads render and function as 'streets'.**

- **Migration:** On load, if a building has `spec='road'` (the old single type), silently re-key to `spec='street'`.
- **Curves:** Old roads have no `roadCurve` field; they render as straight lines.
- **Roundabouts:** Do not exist in legacy saves; old roads remain straight.
- **Connectivity:** All road connectivity gates (FEAT-1972079891) work unchanged on old roads (now streets).
- **Backwards compatibility:** Users can edit old saves, add new road types, place roundabouts, draw curves without issue.
- **Test:**
  - Load a saved game from before roads overhaul (containing buildings with `spec='road'`).
  - Verify roads are present, still connected, still activate adjacent buildings.
  - Verify no new road types appear unless explicitly placed.
  - Verify curves field is absent.
  - Edit the save: place a new avenue, bulldoze an old street, place a curve. Verify new edits coexist with old roads.
  - Reload: verify all roads (old + new) persist.

---

### AC-9: Rendering — Visual Distinction & Zoom Behaviour
**Road types render distinctly; curves and grid patterns are visible at appropriate zoom levels.**

- **Visual distinction:**
  - Path: dashed/dotted lines, thin, grey-green.
  - Street: solid lines, normal thickness, grey.
  - Avenue: lane marks (parallel dashes), thicker, darker grey.
  - Boulevard: dual-lane (two parallel lines with gap), darkest grey.
  - Motorway: bold, blue.
  - Roundabout: circle/rounded square, lighter grey, 2×2 footprint.
- **Zoom behaviour:**
  - Zoom ≥ 8 (geom.s > 8): All roads and road details (curves, lane marks) are fully visible.
  - Zoom 4–7 (3 < geom.s ≤ 8): Roads are visible; lane marks simplified; curves smooth.
  - Zoom 1–3 (geom.s ≤ 3): Roads visible as single lines; curves simplified to straight; lane marks hidden (performance).
  - Zoom < 1: Map is zoomed out; roads render as thin lines; grid pattern may become aliased.
- **Curve rendering:**
  - Canvas `lineJoin='round'` smooths vertices.
  - Colours and widths match road type.
  - Visible at zoom ≥ 4.
- **Grid pattern:**
  - Grid-fill produces a visible lattice of roads visible at zoom ≥ 4.
  - Roundabouts are distinct circles at junctions.
- **Test:**
  - Place all road types, zoom to levels 1, 4, 8, 48.
  - Verify each type is colour/style distinct.
  - Verify curves smooth at zoom 4+, simplified at zoom 1.
  - Verify grid pattern is recognizable at zoom 4+.

---

### AC-10: Migration of Existing Saved Roads (Backward Compatibility)
**Saved games with old 'road' specs load correctly; users can edit old saves without loss.**

- **Spec aliasing:** On load, `spec='road'` is re-keyed to `spec='street'` (the new default).
- **No re-calculation:** Old connectivity, activation status, upkeep are unchanged.
- **User edits:** Old streets can be bulldozed, replaced with new types, curved, or incorporated into grid-fills.
- **Curves on old roads:** Old roads have no `roadCurve` field. If user re-places an old road with a curve, a new entry with `roadCurve` is created.
- **Roundabouts in old saves:** Do not exist; users must place them manually or via grid-fill.
- **Test:**
  - Load a save with roads, housing, industries (all using old 'road' spec).
  - Verify roads are present, render as streets, connect as before.
  - Verify housing remains road-adjacent and activated.
  - Edit: bulldoze one old street, place a new avenue, place a curve on another.
  - Reload: verify all roads (old streets + new avenue + curved road) persist.

---

### AC-11: Interaction with Building Activation (FEAT-1972079891)
**Roads (all types) and roundabouts participate in the building-activation road-connectivity gate.**

- **Scope:** All road types (path, street, avenue, boulevard, motorway) and roundabouts are treated as road network infrastructure.
- **AC-1 & AC-3 of FEAT-1972079891 apply:**
  - Road-adjacency: Building is adjacent to a road tile (orthogonal, not diagonal).
  - Road-connectivity: Adjacent road is part of the connected road network (computed via flood-fill from map edges / motorway / town hall per FEAT-1972079891 DD1).
- **Motorway caveat:** Motorways are road tiles, but `isFreeZone()` does NOT apply to them. They are NOT zones; they cost money and count in building activation.
- **Roundabout as road:** All 4 tiles of a roundabout are treated as road tiles for connectivity.
  - A building at distance 1 from any roundabout edge is road-adjacent.
  - Roundabout connects the roads it sits at.
- **Path caveat (future):** Paths do not activate vehicles (buildings); buildings adjacent to paths only may not activate. **Deferred to FEAT-XXX if motorway traffic simulation is required.**
- **Test (integration with FEAT-1972079891):**
  - Place a residential building, then a street adjacent. Verify building is road-adjacent (AC-2 of FEAT-1972079891).
  - Connect the street to map edge via roads. Verify building is road-connected (AC-3 of FEAT-1972079891).
  - Bulldoze the street, verify building goes offline (AC-12 of FEAT-1972079891).
  - Place a roundabout, build around it, verify buildings adjacent to roundabout are road-adjacent.

---

### AC-12: Determinism Across Replay & CI
**Identical state + user input → identical road layout (verifiable in debug snapshots).**

- **Determinism gate:** Same as FEAT-1972079891 AC-8: given identical SimState, road placement produces identical on-disk set (same ids, positions, specs, curves).
- **No randomness:** No Monte Carlo, no frame-dependent decisions, no Date.now().
- **Snapshots:** Debug snapshots (FEAT-1972079886) capture road state deterministically.
- **CI:** Tests replay identical input sequences and verify road layout matches.
- **Test:** (Same as AC-6, with emphasis on CI reproducibility.)

---

## Non-Acceptance Criteria (Out of Scope)

- **Traffic simulation:** Roads do not simulate vehicle movement, congestion, or traffic flow (deferred to FEAT-XXX).
- **Pathfinding:** Roads are NOT used for NPC pathfinding or citizen commute routing (deferred).
- **One-way / directional roads:** All roads are bidirectional; one-way rules are deferred (FEAT-XXX).
- **Speed enforcement:** Road types do NOT have speed limits that affect vehicle behaviour (hint only; enforcement deferred).
- **Lane-changing / vehicle queuing:** No micro-simulation of lane changes or queue dynamics (deferred).
- **Road damage / maintenance:** Roads do not degrade or require repair (deferred).
- **Seasonal effects on roads:** Roads do not become impassable in winter or flooded in spring (deferred).
- **Partial curves / bezier interpolation:** Curves use polyline (AC-3); bezier/spline interpolation is not required for baseline.
- **Path-only connectivity:** Paths do not activate vehicles; buildings adjacent to paths only may be a future feature (AC-11 caveat).
- **Roundabout traffic rules:** Roundabout does not enforce traffic priority or yield rules (deferred).
- **Motorway interchanges:** Grade-separated junctions and overpasses are deferred (FEAT-XXX).
- **Tolls / congestion pricing:** Roads do not incur usage fees beyond upkeep (deferred).

---

## Testing Strategy

### Unit Tests
- `snapRoadTarget(state, tool, hover, radius)` returns snapped position or unmodified hover.
- `gridFillRectangle(state, spec, x1, y1, x2, y2, gridSize)` returns correct road/roundabout list and cost.
- Road type specs (`SPECS['street']`, `SPECS['avenue']`, etc.) have correct properties (cost, upkeep, colour, unlock).
- Curve validation: minimum radius 2 tiles, rejects too-sharp curves.
- Roundabout spec has size 2×2, distinct colour, correct upkeep.

### Integration Tests
- Snap placement: drag road near existing road, verify snap preview, place and verify connection.
- Grid-fill placement: drag rectangle, verify road lattice + roundabouts, verify cost, place and verify persistence.
- Curve drawing: drag mouse, verify polyline captures path, render at multiple zoom levels.
- Legacy save load: load old 'road' spec, verify re-key to 'street', verify connectivity unchanged.
- Building activation interaction (FEAT-1972079891): place building adjacent to each road type, verify road-adjacent and road-connected gates work.

### Rendering Tests
- Road types render with distinct colours and line styles.
- Curves render smoothly (lineJoin='round').
- Roundabout renders as circle/rounded square, distinct from roads.
- Zoom levels: zoom in/out, verify details appear/simplify appropriately.
- Grid pattern is visible and recognizable.

### Determinism Tests
- Save state, place roads via drag, save result. Replay from same state, repeat input, verify road list matches exactly.
- Grid-fill: same rectangle input → same road/roundabout list (same ids, positions, specs).
- Curves: same drag path → same polyline vertices.

### Balance Tuning (NOT required for AC, delegated to Aaron)
- Measure typical city with grid-fill roads + buildings: check road cost vs income ratio.
- Measure upkeep impact of dense road networks (grid-fill generates many tiles).
- Tune snap radius, grid spacing, roundabout cost, curve minimum radius if necessary (Aaron's row-by-row balance pass).

---

## Design Decision Summary

| DD | Topic | Recommendation | Flagged |
|----|-------|---|---|
| DD1 | Road type hierarchy (names, costs, unlock levels) | PLACEHOLDER: path/street/avenue/boulevard/motorway; costs £20–£300 | Yes |
| DD2 | Snap radius & visual feedback | PLACEHOLDER: 3-tile radius, white/green snap indicator | Yes |
| DD3 | Curve model (polyline vs arc-segment) | PLACEHOLDER: polyline; minimum radius 2 tiles | Yes |
| DD4 | Grid spacing (tiles per km) | PLACEHOLDER: 20 tiles = 1 km; 2×2 roundabout at intersections | Yes |
| DD5 | Roundabout as building vs visual-only | PLACEHOLDER: building spec, 2×2, £200 cost, £15 upkeep | Yes |
| DD6 | Determinism | Required (no randomness) | No |
| DD7 | Road cost vs zoning free | Confirmed: roads cost, zoning free, 50% refund | No |
| DD8 | Legacy save migration | PLACEHOLDER: silent re-key 'road' → 'street', no auto-promotion | Yes |
| DD9 | (Rendering/zoom) | Covered in AC-9 | No |
| DD10 | (Building activation integration) | Covered in AC-11 | No |

---

## Compatibility with FEAT-1972079891 (Building Activation Prerequisites)

### Touchpoints
1. **Road-adjacency (FEAT-1972079891 AC-2):** All road types count as road tiles. Roundabout's 4 tiles all count as road tiles.
2. **Road-connectivity (FEAT-1972079891 AC-1, AC-3):** Connectivity is computed via orthogonal flood-fill. All road types (path, street, avenue, boulevard, motorway) and roundabouts participate in the same connected-set calculation. No type has priority or special routing.
3. **Determinism (FEAT-1972079891 AC-8):** Road placement is deterministic; road-connectivity computation is deterministic (same state → same connected set).
4. **Unit consistency (FEAT-1972079891 AC-15):** No new units introduced for roads (they remain fixed 1×1 or 2×2 tile footprints; costs are in £; upkeep is £/tick).

### Separation of Concerns
- **FEAT-1972079881:** Road types, placement, snapping, curves, grid-fill, rendering.
- **FEAT-1972079891:** Building activation gates, prerequisite checks, offline buildings, connectivity reporting.

Both features read/write `roadConnectivity: {connectedRoadTiles: Set<string>}` on SimState (FEAT-1972079891 computes it; FEAT-1972079881 places roads that feed into it). No conflict.

---

## References

- **Current code:** `webconsole/src/sim/data.ts` (SPECS, placementCost, placementZone), `webconsole/src/components/MapView.tsx` (placement, rendering), `webconsole/src/sim/types.ts` (Building, SimState).
- **Building activation gate:** FEAT-1972079891 (road-adjacency, road-connectivity, determinism).
- **Zoning:** FEAT-1972079882 (free zones, placement cost).
- **Related road features (deferred to FEAT-XXX):** traffic simulation, pathfinding, speed limits, vehicle routing, tolls, grade separation, one-way rules.

---

## DESIGN-DECISION Checklist for Aaron

- [ ] **DD1:** Road type names, costs (£20–£300), upkeep values, unlock levels, speed hints — **APPROVED?**
- [ ] **DD1 rendering:** Path (dashed), Street (solid), Avenue (lane marks), Boulevard (dual-lane), Motorway (bold blue) — **APPROVED?**
- [ ] **DD2:** Snap radius (3 tiles?), snap visual (white/green), motorway exclusion — **APPROVED?**
- [ ] **DD3:** Polyline vs arc-segment, minimum bend radius (2 tiles?), line-join rendering (round) — **APPROVED?**
- [ ] **DD4:** Grid spacing (20 tiles = 1 km?), neighbourhood-level grid deferred or included, roundabout auto-placement — **APPROVED?**
- [ ] **DD5:** Roundabout as building spec, size (2×2), cost (£200?), upkeep (£15?), placement rules — **APPROVED?**
- [ ] **DD8:** Legacy save migration (silent re-key vs auto-promotion?) — **APPROVED?**

---

**Total ACs:** 12 (AC-1 through AC-12)
**Total DESIGN-DECISIONs:** 8 (DD1 through DD8, plus sub-topics)

