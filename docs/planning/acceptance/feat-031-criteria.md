# FEAT-031 acceptance criteria: F1 map overlays that show real data

**BOW:** FEAT-031 (P1, open). **Written:** 2026-11-18, BA pass for the backlog comb (FEAT-9995385605).
**Sources read:** `node claude-bow.js show FEAT-031` and FEAT-208, `internal/ui/screens/map/overlay.go`, `overlay_data.go`, `render.go:220-235`, `tripwire_test.go` (named only), `cmd/metropolis/boot.go` (key registrations 1133-1160), `internal/engine/` directory listing, the comb's rough criteria (`docs/planning/backlog-comb/p0p1-1.md:95-105`).
**Existing criteria doc:** none for FEAT-031. The ui.screen.map spec ACs (AC-3, AC-4, AC-6) are the parent text; they say what the cycle is but not what "real data" means per overlay.

## 1. What the player sees today

- The terminal game's F1 map has **no overlay key at all**. `CycleOverlay` has callers only in tests (`grep -rn CycleOverlay internal cmd`, non-test hits are doc comments). `boot.go` binds `y`, `b`, `c` on the map grammar and nothing for `o` or `O`. A player cannot cycle overlays.
- Even if they could, every overlay would show nothing: `overlayLiveValue` returns `(0, false)` for all ten (`overlay_data.go:29-31`), and `paintOverlay` skips cells with no value, so an overlay looks identical to no overlay (`overlay_data.go:86-104`).
- The mechanism is done and proven: ten overlays in a fixed order, background-only paint (AC-4, white-box, ed24c9b), three mechanical tripwires for traffic, per-service coverage and parking (`tripwire_test.go`).
- The BOW says live data is blocked on FEAT-208 (compose delta-publishing). Today the F2 and F3 screens already show live engine data through the router. **I could not tell from the code whether FEAT-208's blocker is still real for the map**: that is Q1.
- The comment in `overlayBlockedReason` says seven overlays have "no engine module". That is partly out of date. Looking at `internal/engine/`: `world` has an ownership command path (`worldapi.go:289`), `census` has a `LandValue` figure, `spiral` has decay (`decay.go`), `wellbeing` has a pollution source, `build` has zoning, `traffic`, `services`, `parking` exist. Whether any of them exposes a **per-cell** value is the open question for each overlay below.

## 2. What the player should see

On F1, pressing `o` steps to the next overlay and `O` the previous, in the order ownership, land value, zoning, utilities, traffic, pollution, decay, per-service coverage, parking occupancy, vitality. The status line names the active overlay. For each overlay that has a real source, the map background colours differ between cells where the underlying numbers differ; the glyphs (terrain, roads, buildings) never change. For an overlay with no source, the screen says so in words (not a silent identical map).

## 3. Acceptance criteria

### A. The key and the readout (all ten, one set)

**AC-1. `o` and `O` cycle on the real boot path.** Boot, press F1, press `o` ten times, then once more.
- Test: frame test on the real boot path (`bootCore`, `routeKeyInput`, `composeDraw`) at 80x24.
- Expected: the status or legend area names each overlay in order, the eleventh press returns to ownership, `O` goes backward and wraps from ownership to vitality.
- Can-fail proof: unbind the key (today's state); the frame never changes and the test is red. It is red on main now.

**AC-2. Glyphs never change when the overlay changes (AC-4 stays green).** For each of the ten overlays, the foreground glyph of every cell equals the glyph with no overlay.
- Test: existing white-box test (`overlay_paint_internal_test.go`) kept green, plus one frame test across all ten.
- Can-fail proof: make `paintOverlay` write a rune; both tests go red.

**AC-3. A blocked overlay says it is blocked.** With no live source, the overlay's name is shown with a short reason (for example "no data yet"), and the map is unchanged. It must not look like a working overlay that shows a flat map.
- Test: frame test on each blocked overlay.
- Expected: the text `no data` (or the ruled wording, Q3) is on screen.
- Can-fail proof: today nothing says so; red.

### B. One criterion per overlay with a live source

For each overlay below the test is the same shape: **engine test + frame test on the default composition, the real publish path, 80x24 frame.** Run the city for a stated number of ticks, switch to the overlay, read the background colour of cell A and cell B, which the test names and which the engine says have different values. Expected: colours differ in the directional sense stated. The test also asserts that on a flat fixture (all cells equal) the colours are all the same, so the test cannot pass by painting noise.

Can-fail proof, same for every overlay: make `overlayLiveValue` return a constant; the two-cell test goes red. Remove the publish of that overlay's data; the overlay shows the AC-3 "no data" text and the test goes red.

| # | Overlay | Source named by the code | Expected observation (directional) |
|---|---|---|---|
| AC-4 | Ownership | `world` ownership (`worldapi.go:289`), tiles bought by `y` | A bought cell and an unbought cell differ. Buy cell 15,15 at tick 0 (`y`); at tick 1 that cell's background differs from a neighbour's. |
| AC-5 | Land value | `census.LandValue` / finance land market, monthly land-value phase | A cell next to a built dwelling has a higher value than a cell on open ground after the month 2 close; colours rank the same way. The absolute number is a balance placeholder. |
| AC-6 | Zoning | `build` zones (8-way) | A cell queued as Dwelling (F3, `b`) and a cell with no zone differ. Each of the 8 zone types maps to a distinct step on the legend, listed in the test. |
| AC-7 | Utilities | consumption's four utility networks (MOD-021) | A cell on a served network and one off it differ. If no per-cell source exists, mark BLOCKED with the missing module named. |
| AC-8 | Traffic | `internal/engine/traffic` (tripwire exists) | A cell on a road with vehicles and a cell on a road with none differ; the tripwire test now fails if the overlay is still blocked. |
| AC-9 | Pollution | `wellbeing` pollution source / `refuse` airshed | A cell beside a heavy industry building and a far cell differ. BLOCKED if no per-cell source (see Q2). |
| AC-10 | Decay | `spiral` decay (`decay.go`) | A cell in a decayed section and a healthy cell differ. BLOCKED until a decayed section can occur in the default city (state the tick at which it first does, or mark BLOCKED). |
| AC-11 | Per-service coverage | `services.CoverageSummary` (tripwire exists) | A cell inside a station's reach and a cell outside differ, for the service the player picked. Needs a rule for which service is shown (Q4). |
| AC-12 | Parking occupancy | `internal/engine/parking` (tripwire exists) | A cell with a full car park and one with an empty car park differ. |
| AC-13 | Vitality | none found | Documented BLOCKED with the module it needs. Not part of done (Q2). |

### C. The blocked set and the tripwires

**AC-14. Every blocked overlay has a recorded owner.** A table in this item's closing comment lists each overlay still BLOCKED, the module or edge it needs, and the BOW item that will deliver it. Count check: ten rows, each either LIVE (with its AC number) or BLOCKED (with a BOW code that exists in `claude-bow.js show`).
- Test: a script or test that parses the closing comment is overkill; the lead checks the ten rows by hand against `node claude-bow.js show`.

**AC-15. The three tripwires are updated, not left.** For traffic, coverage and parking, `tripwire_test.go` now fails if the overlay is still blocked once its source is wired, and passes while it is genuinely blocked. Can-fail proof: wire the traffic source and leave `overlayLiveValue` returning false; the tripwire is red.

**AC-16. Performance does not cost a frame.** Switching overlay on the 100-by-100 default map completes inside the frame budget the other screens use. Measured in a Go benchmark, not a wall-clock assert in CI (project rule: no wall-clock bounds in CI). The test asserts the paint calls per frame are at most one per visible cell.

## 4. Done means

AC-1, AC-2, AC-3, AC-15, AC-16 green, AC-14 table complete, and every overlay with a live source in section B green. Overlays marked BLOCKED are parked with an owner, not part of done. This keeps the item from staying open forever on the seven overlays whose engines do not exist yet.

## 5. Out of scope

- Building the missing engines (vitality, probably pollution, utilities per cell). Each is its own item.
- The webconsole map HUD overlays (FEAT-2326609720, done). Q5 asks which surface is the player's.
- Colour palette choice and accessibility ramps beyond using the existing `widgets.HeatRamp`.
- Mouse support, overlay legends with units (a later UI item).

## 6. Unstated assumptions

1. The terminal game (cmd/metropolis) is the surface this item serves, since AC-4's code lives in `internal/ui/screens/map`.
2. "Real overlay data" means a per-cell number from an engine module, not a number computed in the UI. That keeps the UI free of engine facts (GR#20).
3. The map keeps running while an overlay is shown; values update each tick or month, whichever the source updates on.

## 7. Questions only Aaron can answer

**Q1. Is FEAT-208 still the blocker for map overlays?**
a) Re-check first: one agent proves whether the map can receive per-cell data through the router the other screens use (recommended: the F2 and F3 screens already get live data, so the BOW text may be stale).
b) Treat FEAT-208 as still blocking and work it first.
c) Drop FEAT-208 and wire overlays through the existing router.

**Q2. Overlays with no per-cell source (pollution, vitality, maybe utilities, decay): ship the live ones now and park the rest?**
a) Yes: done = the overlays that have a source, others parked with owner items (recommended).
b) No: nothing ships until all ten are live.
c) Cut the overlay list to the ones the game has mechanics for.

**Q3. What does a blocked overlay say?**
a) "<name>: no data yet" in the status line (recommended).
b) Skip blocked overlays in the cycle so the player never lands on one.
c) Show them greyed in the cycle with the reason.

**Q4. Service coverage covers many services. Which one does the overlay show?**
a) One service per press of a second key (recommended: needs one more key, honest).
b) All services combined as a single worst-covered figure.
c) Fixed order: police first.

**Q5. Is the terminal map or the webconsole HUD the player's overlay surface?**
a) Both, the terminal for Baseline One (recommended: northstar waypoint 1 is the terminal).
b) Webconsole only; park this item.
c) Terminal only; the webconsole overlays stay as they are.

## 8. Size

Several lanes. Split: (1) **one lane, small**: AC-1, AC-2, AC-3 (key plus the blocked readout), no engine change, lands first and makes the screen honest. (2) **one lane per source family**, in this order: ownership, zoning and land value (data exists: AC-4, 5, 6), then traffic, coverage and parking (tripwires: AC-8, 11, 12, AC-15), then the rest if Q2 option b. (3) AC-14 table and close, lead only. The comb's earlier count of "10 overlays in one item" is too big for one lane.
