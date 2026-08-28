# FEAT-1972079910: Contiguous Road Laying — Anchored Sweep, Auto Junctions, Auto Crossings

**Feature:** Replace sampled-mouse road placement with an anchored, path-tracked sweep so laid roads are contiguous by construction; fit bends to minimum-radius arcs; auto-build junctions/roundabouts where the path crosses existing roads; auto-build grade-separated crossings (rail bridge, motorway junction) at extra cost.

**Mkey:** FEAT-1972079910

## Evidence (why this is P1)

Screenshot `2026-08-28 193148` + `debug (19).json` (v0.3.0.134, tick 2810, 92,600 citizens):
3,933 road tiles fragment into **293 connected components** — 152 single-tile orphans,
214 components of ≤3 tiles, 81 `rd_dual` tiles with zero road neighbours. Cause: the
current drag handler places a tile per sampled pointer event; under page load events
skip tiles, so a dual carriageway becomes breadcrumbs. Player-visible damage:
"+121,500 NOT ON ROAD NETWORK — CONNECT TO GROW". **No retrofit of existing crumbs
(Aaron ruling)** — the fix is the placement model, not the map.

## Design

- **Anchor + tracker:** `pointerdown` anchors the start vertex. On every `pointermove`,
  the candidate path is COMPUTED in tile space from the last committed vertex to the
  cursor tile (deterministic line interpolation; arcs for bends). Raw pointer samples
  are never placed directly, so skipped events cannot create gaps.
- **Preview → atomic commit:** the tracked path renders as a ghost; `pointerup` commits
  the WHOLE path (road tiles + junctions + crossings + their costs) as ONE journal
  action. All-or-nothing: if funds fail for the total, nothing places (BUG-396 rules)
  and the feedback says why. `Esc`/right-click abandons the preview.
- **Bends:** direction changes are fitted to minimum-radius arcs per road tier
  (higher tiers = larger radius; placeholder radii). A mouse path tighter than the
  legal radius previews the closest legal curve (FEAT-1972079857 sweep guide).
- **Auto junctions:** where the committed path crosses an existing road tile, the
  crossing tile becomes a junction: plain crossroads at street tier, roundabout at
  avenue tier and above (consistent with FEAT-1972079881's auto-roundabout model).
- **Auto grade separation:** path crosses RAIL and road tier is dual carriageway or
  above → the crossing segment becomes a rail bridge at a cost multiplier. Path
  crosses a MOTORWAY-class road → an auto motorway junction object at a (larger)
  cost multiplier. Multipliers are placeholders (balance-number regime).

## Acceptance Criteria

- **AC-1 (contiguity by construction).** After any single drag-commit, the placed
  tiles form exactly ONE 4-connected component that includes the anchor tile.
  Check: a test drives the tracker with a sparse event sequence (e.g. anchor at
  (0,0), next event at (30,17), commit) and asserts every consecutive pair of path
  tiles is orthogonally adjacent and the component count is 1. **False-pass:** a
  test whose synthetic events are already adjacent tiles — the event GAP is the
  thing under test.
- **AC-2 (frame-rate independence).** The committed path for (anchor, cursor-path,
  release) is IDENTICAL whether the tracker saw every intermediate pointer event or
  only 10% of them, provided the sampled cursor positions lie on the same path.
  Check: same drag replayed at two sampling densities → byte-identical tile list.
- **AC-3 (atomic journal action).** One drag = one journal entry carrying the full
  tile list, junction set, and crossing set. Replay from genesis reproduces the
  identical network (component count included). Check: place, capture debug JSON,
  hard-reset replay, re-capture, diff the road tile sets — empty diff.
- **AC-4 (all-or-nothing funds).** If treasury cannot cover the WHOLE commit
  (tiles + junctions + bridges), nothing is placed, no partial spend posts to the
  ledger, and the placement feedback names the shortfall. Check: set funds to
  total−1, commit, assert zero tiles placed and zero ledger movement.
- **AC-5 (bend legality).** No committed path contains a direction change tighter
  than its tier's minimum radius. Check: drive the tracker through a hairpin mouse
  path; assert the committed arc's radius ≥ the tier minimum from the road spec
  (GR#15: read the radius from the spec, don't restate the number).
- **AC-6 (auto junction on crossing).** Committing a path across an existing road
  places a junction at the shared tile: crossroads below avenue tier, roundabout at
  avenue+. The junction tile(s) count as connected road for `computeRoadConnectivity`
  (FEAT-1972079891). Check: lay A, lay B across it, assert junction spec at the
  intersection and one merged connected component.
- **AC-7 (rail bridge).** A dual-carriageway-or-above path crossing a rail tile
  commits a bridge segment: the road is continuous across it, the RAIL stays
  continuous (rail connectivity unchanged before/after), and the commit cost
  includes the bridge multiplier from the spec. Check: assert both networks'
  component counts unchanged by the crossing, and ledger delta = base + bridge
  premium (values read from spec data). **False-pass:** a "bridge" that deletes the
  rail tile — the rail-continuity assertion must be present and able to fail.
- **AC-8 (motorway junction).** A path crossing a motorway-class road commits an
  auto motorway-junction object at its (larger) multiplier; both roads remain
  continuous and mutually connected through it. Check: as AC-7 with connectivity
  asserted in BOTH directions.
- **AC-9 (no regression for click-placement).** Single-click still places a single
  tile; existing road tests stay green.
- **AC-10 (determinism).** The tracker uses no `Date`/`Math.random`; identical
  (anchor, sampled cursor tiles, release) → identical commit. Property test over
  several synthetic drags.
- **AC-11 (activation integration).** A city grown along one committed sweep has
  its roadside buildings pass the road-adjacency + connectivity gates without any
  manual gap-patching. Check: place a sweep + a building beside it; assert online.
- **AC-12 (legibility).** During preview, the running total cost (incl. junction/
  bridge premiums) displays before commit; the display derives from the same cost
  computation the commit uses (one source of truth — GR#3).

## Design Decisions (Aaron unless noted)

- **DD1 — cost multipliers:** rail bridge and motorway junction premiums.
  PLACEHOLDER: bridge ×4 base tile cost over the crossed span; motorway junction
  flat £250k-class. Balance regime; directional tests only.
- **DD2 — roundabout threshold:** recommended avenue-tier-and-above crossings get
  roundabouts, streets get plain crossroads (matches 1881). Confirm.
- **DD3 — which tiers auto-bridge over rail:** Aaron named dual carriageway;
  recommended dual-and-above. Below dual: level crossing (existing behaviour) until
  1881's hierarchy lands. Confirm.
- **DD4 — junction/bridge specs:** new specs (`rd_junction`, `rd_roundabout`,
  `rd_railbridge`, `rd_mwyjunction`) vs flags on road tiles. Recommended: distinct
  specs (renderable, cost-carrying, GR#15-auditable). Lead default unless overruled.

## Scope notes

Road TYPES, snap/magnetic glue, and grid-fill remain FEAT-1972079881. The sweep
guide visual belongs to FEAT-1972079857 — this item consumes its minimum-radius
rule for commit legality. No retrofit pass over existing breadcrumb roads.
