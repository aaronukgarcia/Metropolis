# FEAT-1972079911: Bulldozer Size Selector — XXS through XXL

**Feature:** The bulldozer gains a size selector. Today's single-tile clear is XXS;
add stepped sizes up to XXL so large clears stop being tile-by-tile drudgery.

**Mkey:** FEAT-1972079911

## Design

- Sizes (dimensions PLACEHOLDER, balance regime): XXS 1×1 (current), XS 2×2, S 3×3,
  M 5×5, L 7×7, XL 10×10, XXL 15×15. The table lives in data (one registry object),
  not scattered constants (GR#3/15).
- The cursor previews the full clear footprint before click; a drag sweeps the
  footprint along the path (same anchored tracker as FEAT-1972079910 once it lands;
  until then, per-event footprints unioned).
- One swipe (down→up) = ONE atomic journal action carrying the demolished tile set
  (deterministic replay). Demolition compensation/refund posts per tile exactly as
  single-tile demolition does today — the size changes the AREA, never the rules.
- No sim wipe involved — GR#27 untouched.

## Acceptance Criteria

- **AC-1 (size registry).** All sizes derive from one exported registry; the UI
  selector renders FROM it. Check: a test asserts the selector option count equals
  the registry length; adding a registry entry without UI support turns it red.
- **AC-2 (footprint correctness).** For each size, a click at (x,y) demolishes
  exactly the registry-defined footprint clipped to the map bounds. Property test
  across sizes and edge positions; expected counts computed from the registry
  (GR#15), never hardcoded.
- **AC-3 (atomicity + replay).** One swipe = one journal action; genesis replay
  reproduces the identical post-demolition building set. Check: demolish, capture,
  replay, diff — empty.
- **AC-4 (per-tile economics unchanged).** Ledger movement for an N-tile clear
  equals the sum of the N single-tile demolitions of the same targets. Check:
  compute both ways in a test; assert equal (derives amounts from the same rules,
  asserts the batch path adds no discount/surcharge).
- **AC-5 (preview honesty).** The rendered preview footprint equals the tiles the
  click would demolish — same function feeds both (GR#3). Check: mutation-style —
  offsetting the preview by one tile must fail a test.
- **AC-6 (no regression).** XXS behaves byte-identically to today's bulldozer;
  existing demolition tests stay green.
- **AC-7 (selector UX).** Size control appears only while the bulldozer tool is
  active; current size is visible; keyboard cycling reserved for the KEYBINDINGS
  registry (FEAT-1972079861) — add the binding there, not a local listener.

## Design Decisions

- **DD1 — size steps:** the 7-step table above is a placeholder; Aaron may retune
  dimensions per step (balance regime).
- **DD2 — accidental-XXL protection:** recommended none for baseline (preview is the
  guard); flag if dogfood shows misfires.
