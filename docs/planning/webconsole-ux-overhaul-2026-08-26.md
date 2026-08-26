# Webconsole UX Overhaul — Aaron batch 2026-08-26

Aaron's directive (verbatim intent): the build is clunky; reorganise the layout, rebuild the build palette as a tree, fix the slider grouping bug, add road types + density options, populate with placeholders now (wire later), fix playback button state, enrich + throttle the debug snapshot, format numbers globally (£ + thousands commas), and do real roads work (snap/glue, sweeping bends, Milton-Keynes grid-fill with roundabouts) + density/growth (denser offices, bigger farms, or 10%/yr auto-scale-out).

Target: **webconsole/** (the :5173 Vite/React "Metropolis Command Console"). Parent module MOD-086 (ui.web). Data flows via INT-005 / FEAT-1972079852 (engine adapter) once wired; placeholders render before wiring.

## Mapping to EXISTING items (do NOT duplicate — extend/relate)
- FEAT-1972079857 — road hierarchy + **minimum-radius sweep guide** (covers "bends should sweep"). Extend with snap/glue + grid-fill.
- FEAT-1972079860 — palette available-first + locked click-through (relates to the tree rebuild).
- FEAT-1972079866 — info panel per building type wired into placement (relates to placeholder catalogue + info panel move).
- FEAT-1972079856 — debug performance HUD (relates to the debug-snapshot enrichment).
- FEAT-1972079855 — per-object utilisation indicator.
- MOD-086 — currently describes the OLD layout ("left fiscal / bottom build-move / right info tabs"); the relayout item supersedes that arrangement.

## NEW child items filed under the epic
1. **Panel relayout + Start Over reposition** — info panel -> bottom; build -> left; demand + fiscal -> right; Start Over button -> left. (Supersedes MOD-086's arrangement.)
2. **Playback controls toggle state** — Fast / Turbo (and Pause/Play) stay visually depressed/active while engaged; single source of truth = the engine speed state, not local button hover.
3. **Build palette tree + per-group scroll reset** — replace the flat grouping with a proper tree (families -> types). BUG: each group's sub-window keeps its own scroll; entering a new group must RESET scroll to top (today: scroll down in Education, switch to Landmarks -> its entries are scrolled off-screen, look empty). The tree + a controlled scroll container fixes the whole class.
4. **Placeholder object catalogue** — create placeholder entries for ALL object types now (name, footprint, category, glyph/colour, cost placeholder) so the palette looks populated; wire real mechanics later. Balance numbers are placeholders under the balance-number regime.
5. **Density + scale options** — wider people-density options (per zone) and object density; denser office buildings and bigger farms, OR auto-scale-out where they grow ~10%/yr (footprint/occupancy expansion over time). Directional tests + Aaron balance pass.
6. **Global number formatting** — funds carry a £ prefix; ALL numbers globally display with thousands separators (e.g. 33,000,000). One formatting helper used everywhere; no ad-hoc toLocaleString scattered.
7. **Debug snapshot enrichment + 15s throttle** — the snapshot/hot-commit panel updates too fast to select; throttle its refresh to once every 15 seconds (a stable, selectable frame), and add more data fields. Relates FEAT-1972079856.
8. **Roads overhaul** — more road types; **snap / magnetic glue** when laying near existing roads (the 'Blue' reference behaviour); **sweeping bends** (min-radius curve, relates FEAT-1972079857); a **grid-fill build function** using a Milton-Keynes gap size that auto-places **roundabouts** at junctions for denser fills.

## Sequencing note
Layout/format/button-state/tree-slider (1,2,3,6) are pure UI, low-risk, do first — they make the console feel less clunky immediately. Placeholders (4) unblock the "populated" feel. Roads (8) and density/growth (5) are deeper (engine-adjacent once wired) — later increments. Each item gets acceptance criteria before dev per the dev-team process; roads snap/grid + growth carry balance-number-regime placeholders needing Aaron's row-by-row pass.
