# FEAT-1972079860: React Console Palette Availability-First Sort

**Feature:** Palette list sorts placeable objects available-first; unavailable (locked) items render greyed-out with hover tooltip and click-through to a requirements/deliverables card showing what is needed to unlock and what the object delivers.

**Mkey:** FEAT-1972079860

## Overview

The build palette (BuildTab in BottomBar.tsx, rendering PALETTE.items for each family) reorders its item list within each family to surface immediately placeable specs first, with locked specs grouped below. Clicking a locked spec opens a modal card displaying:
- **Unlock requirement:** city level threshold to make it available
- **What it delivers:** capacity/jobs/residents/mw/served/tourism from the spec
- **Dismissal:** click outside or press Esc to close

Available specs remain fully interactive (click to select). No regression to existing place/drag/paint/key-shortcut flows.

---

## Design Decisions Flagged for Lead

### DD1: Card Content Sourcing — Spec Properties vs Curated Strings
**Where do "requirements" and "deliverables" prose come from?**

- **Option A (CHOSEN UNLESS OVERRULED):** Card reads directly from the Spec object:
  - Requirements: `unlock` property → "Unlocks at city level {sp.unlock}"
  - Deliverables: `cost`, `served`, `jobs`, `mw`, `residents`, `tourism` (if present) → auto-generate prose, e.g., "Serves 40,000" (from `hea_hospital.served`), "Provides 300 jobs" (from `off_tower.jobs`), "Generates 80 MW" (from `pow_coal.mw`)
  - **Rationale:** Spec is the SSOT (GR#3); no hand-duplicated requirement strings. Deliverables list may be formatted for readability but values come from data.

- **Option B:** Hand-authored markdown catalogue per spec (e.g., specs.deliverables.md) — more polished prose but duplicate SSOT violation if spec values later change.

- **Option C:** Partial curation: requirements always from `sp.unlock`, deliverables for select categories (landmarks, utilities) hand-authored; others auto-generated.

**Flag for Aaron:** Confirm Option A (auto-generated + Spec sourced) or prefer hand curation for deliverables prose (accepting SSOT duplication risk).

---

### DD2: Placeholder vs Locked Visual Distinction
**Both placeholders ("coming soon") and locked-but-real specs are currently greyed-out (opacity 0.45, grayscale). Should they be visually distinct?**

- **Option A (CURRENT BEHAVIOUR):** No distinction. Both render greyed. Placeholder has `disabled` + `aria-disabled`; locked spec has `disabled` + no aria-disabled. Hover tooltip distinguishes them (placeholder: "coming soon"; locked: "unlocks at level N").

- **Option B:** Locked specs stay greyed but clickable/card-open on click; placeholders stay fully disabled (no click handler, no card).

- **Option C:** Add a badge or border style to locked specs so they visually signal "clickable for more info" vs "not ready".

**Flag for Aaron:** Confirm whether locked specs should have a distinct visual cue (e.g., a small "ℹ" badge, or border highlight on hover) or stay indistinguishable from placeholders.

---

### DD3: Sort Stability Within Groups
**If palette has duplicate families or mixed real/placeholder items in one family, how stable is the sort?**

- **Requirement:** Within each family (e.g., PALETTE[...].items), items are sorted by `isPlaceable(state, sp)` descending (available first), then by `sp.unlock` ascending (lowest level first among locked), then by original order in PALETTE (stable sort). This guarantees:
  - Available items always at top
  - Locked items always below
  - Within each subgroup, order is deterministic and consistent across sessions

- **Implementation:** `sortedItems = items.sort((a, b) => compare(isPlaceable state a, isPlaceable state b) || compare(unlock a, unlock b) || indexInPalette(a) - indexInPalette(b))`

**Flag for Aaron:** Confirm this sort order matches game design intent.

---

### DD4: Card Dismiss Scope — Single Card vs Multiple Selection
**Can the player open multiple spec cards simultaneously, or is only one visible at a time?**

- **Option A (CHOSEN UNLESS OVERRULED):** Only one card open at a time. Clicking another locked spec closes the current card and opens the new one. Clicking an open spec's button again also closes it (toggle).

- **Option B:** Multiple cards stack (uncommon for mobile/narrow screens, likely out of scope for baseline one).

**Flag for Aaron:** Confirm single-card modal is the right UX.

---

## Acceptance Criteria

### AC-1: Available-First Sort Within Each Family
**Each palette family sorts placeable specs first, locked specs second.**

- **Sorting rule:** Within `PALETTE[i].items`, items are sorted by:
  1. `isPlaceable(state, sp)` descending (true before false — available items first)
  2. `sp.unlock` ascending (lowest level first among locked items)
  3. Position in original `PALETTE[i].items` array (stable secondary sort — original order preserved when both sort keys are equal)
  
- **Scope:** Applied to every family tab — Network, Transport, Housing, etc.

- **No mutation of PALETTE:** The global `PALETTE` constant is immutable. Sorting is computed fresh every render by BuildTab (or re-render trigger).

- **Test:** Given state with city level 5 (specs unlock at 1, 2, 3, 5, 6 in one family):
  - Expected render order: [unlock 1], [unlock 2], [unlock 3], [unlock 5] (available), then [unlock 6] (locked).
  - Advance state to level 6 → re-render → all items now available, no locked items visible.
  - Advance state back to level 4 → [1, 2, 3] available at top, [5, 6] locked at bottom.

---

### AC-2: Locked Items Greyed Out and Disabled
**Specs that are locked (`!isPlaceable(state, sp) && !sp.placeholder`) render visually distinct and non-interactive (non-placeable).**

- **Visual:** Apply to `.pal-item` button in BuildTab item list:
  - CSS class: `locked` (already exists in BottomBar.tsx, line 78)
  - Opacity/desaturation consistent with placeholder greying (PLACEHOLDER items are already grey; locked real specs should match or be distinguishable per DD2)
  - Hover state should not imply it can be selected (no "active" highlight on click)

- **Interactivity:** `disabled` HTML attribute is NOT set on locked specs (GR#5 and the feature request imply they ARE clickable). Instead, the onClick handler routes to the card-open logic.
  - Placeholder items: `disabled={true}` (no click handler)
  - Locked real specs: `disabled={false}` (click handler opens card)

- **Test:** Locked spec renders with class `locked`, no `disabled` attribute, and cursor changes to pointer on hover. Clicking it does NOT select it for placement; instead, it opens the requirements card.

---

### AC-3: Hover Tooltip Enhanced for Locked Items
**Locked items show hover tooltip explaining unlock requirement and hint to click for more.**

- **Tooltip text (PLACEHOLDER — directional only):** For locked real spec:
  - `"${sp.name} — unlocks at city level ${sp.unlock}. Click for requirements & what it delivers."`
  - E.g., "International Airport — unlocks at city level 6. Click for requirements & what it delivers."

- **Placeholder items:** Keep existing text (e.g., "coming soon (planned)...").

- **Implementation:** Update the `title` attribute in BottomBar.tsx line 82 (locked branch) or render a styled `<span>` tooltip overlay.

- **Accessibility:** Hover tooltip is advisory; the card (AC-4) is the primary destination for detailed requirements.

- **Test:** Hover over a locked spec (e.g., at level 3, over an unlock-6 item) → tooltip shows unlock level + click hint. Tooltip disappears on mouse-out.

---

### AC-4: Click Locked Item Opens Requirements/Deliverables Card
**Clicking a locked spec opens a modal card displaying what is needed to unlock it and what it delivers.**

- **Card trigger:** `onClick` handler in `.pal-item` button:
  ```typescript
  if (!isPlaceable(state, sp) && !sp.placeholder) {
    // Open card (dispatch action or state hook)
  }
  ```

- **Card component:** New React component `SpecCard` (or extend existing component hierarchy in BottomBar.tsx or separate modal). Displays:
  - **Header:** Spec name + color swatch (matching palette item)
  - **Requirements section:** "Unlocks at city level {sp.unlock}"
  - **What it delivers section:** List of capacities/services from spec properties:
    - If `sp.residents` present: "Houses {sp.residents} residents"
    - If `sp.jobs` present: "Provides {sp.jobs} jobs"
    - If `sp.mw` present: "Generates {sp.mw} MW of power"
    - If `sp.served` present: "Serves {sp.served} population" (water/police/health/etc.)
    - If `sp.tourism` present: "Attracts {sp.tourism} tourists"
    - If `sp.cost` > 0: "Costs {fmtMoney(sp.cost)} to place"
    - If `sp.upkeep` > 0: "Upkeep {fmtMoney(sp.upkeep)}/tick"
  - **Blurb section:** Spec's `blurb` text (e.g., "Heathrow-scale · 1,227 ha · twin 3.9 km runways" for airport)

- **Sourcing:** All values MUST come from the Spec object (data.ts), never hardcoded. See DD1.

- **Test:** Click locked spec airport (unlock level 6) at city level 4 → card opens showing:
  - "Unlocks at city level 6"
  - "Houses 0 residents" OR omitted if residents is 0/undefined
  - "Generates 0 MW" OR omitted if mw is 0/undefined
  - "Attracts 140 tourists"
  - "Costs £450,000 to place"
  - "Upkeep £3,000/tick"
  - Blurb text

---

### AC-5: Card Dismissal — Click Outside or Esc Key
**Card closes when player clicks outside the card or presses Escape.**

- **Click outside:** Clicking any area outside the card modal (including the palette list or map) closes it.

- **Esc key:** Pressing Escape also closes the open card.

- **No confirmation:** Card dismissal is immediate, no prompt.

- **Re-open same spec:** Clicking the same locked spec again after dismissal re-opens the card (card state is not sticky across multiple dismissals).

- **Test:** Open card, click outside → card closes. Open again, press Esc → card closes. Open third time, click on another locked spec in palette → previous card closes, new card opens.

---

### AC-6: Keyboard/Mouse Accessibility
**Card and palette interaction are fully keyboard-accessible (Tab, Enter, Esc).**

- **Palette tabs:** Existing Tab navigation between family buttons and item buttons in BuildTab remains working. No change to existing BottomBar focus management.

- **Locked item button:** Tab to locked item button, press Enter → opens card (same as click).

- **Card dismissal:** 
  - Esc key closes card (AC-5)
  - Tab out of card (no trap — focus can move to other UI elements)
  - After card closes, focus returns to the locked spec button that opened it (or nearest focusable element)

- **ARIA labels:** Card modal should have `role="dialog"`, `aria-labelledby` pointing to card title, and `aria-modal="true"`.

- **Test:** Tab through palette, reach locked spec button, press Enter → card opens. Press Tab inside card → focus moves through card content (if any focusable elements). Press Esc → card closes, focus returns to locked spec button.

---

### AC-7: No Regression to Available Items
**Placeable specs retain full interactivity: click selects for placement, drag/paint works, key shortcuts (1–9) work.**

- **Available spec in palette:** `isPlaceable(state, sp) === true`
  - Click → dispatch `{ type: 'tool', tool: { mode: 'build', spec: id } }` (existing behavior, unchanged)
  - Drag (if implemented) → paint multiple tiles (existing, no change)
  - Key 1–9 shortcuts → select quick-pick (existing, no change)
  - Hover → show normal tooltip (cost, upkeep, dims, build time) — no "click for card" hint

- **Card is never opened for available specs:** Only locked real specs trigger the card.

- **Test:** Available building (e.g., Small Holding at level 1) remains clickable, selectable, draggable. Clicking it selects it for placement, not opens a card. Drag to place multiple → works. Press key shortcut → selects.

---

### AC-8: Sort Determinism
**Palette sort is deterministic: given identical state (xp, level, unlockedAll flag), sort order is identical across re-renders and sessions.**

- **No randomness:** No shuffle, no session-dependent sort keys.

- **Stable sort:** Java/Python `sort()` is stable; the implementation uses `Array.sort()` with explicit comparison logic (AC-1 sort rule). Spec properties (`unlock`, placeholder status) are immutable after load.

- **Test:** Save state at level 5. Render palette, record item order. Advance 1 tick, go back to level 5 → order identical. Reload save → order identical.

---

### AC-9: Balance Numbers — Placeholder Regime (Spec Properties)
**All deliverables values (cost, upkeep, served, jobs, mw, residents, tourism) are read from Spec and are PLACEHOLDER under Aaron's balance-number regime.**

- **Current sourcing:** Every value in SpecCard is derived from SPECS[spec_id], never hardcoded in the card component.

- **Testability:** Directional tests only. Never assert "when airport costs 450k and player has 400k, card shows 'not enough funds'" — that's balance tuning, not correctness. Test structure: "card displays spec.cost property" (property value is a parameter, not a literal).

- **Future tuning:** If Aaron changes `sp.cost` or `sp.served` in data.ts, the card automatically reflects the new value without code change (SSOT).

- **Test:** Create a test spec with `{ cost: 100000, served: 50000, mw: 0, residents: 100 }`. Open card → displays "Costs £100,000", "Serves 50,000", "Houses 100 residents". No hardcoded assertions of exact values.

---

### AC-10: Unlock Level Display Clarity
**Unlock requirement is shown clearly, not ambiguous.**

- **Format:** "Unlocks at city level {sp.unlock}" — e.g., "Unlocks at city level 6"

- **Exception:** Placeholder specs that have `unlock: 99` (never unlock) should NOT appear in a locked card (they are `disabled` and non-clickable). This AC does not apply to them.

- **Uniqueness:** Each real spec has exactly one unlock level (the `unlock` property). No ambiguity.

- **Test:** Card for spec with `unlock: 6` displays "Unlocks at city level 6". Card for spec with `unlock: 2` displays "Unlocks at city level 2".

---

### AC-11: Card Data Consistency — SSOT (GR#3)
**All deliverables displayed in the card come from the single Spec object; no alternative source of truth or re-derived values.**

- **Sourcing rule:** SpecCard component queries `SPECS[sp_id]` and reads properties directly (`sp.cost`, `sp.upkeep`, `sp.residents`, etc.). Does NOT re-compute, infer, or cache these values elsewhere.

- **Consequence:** If a bug is fixed in how `served` or `mw` is calculated in engine.ts, the card does not need a separate fix — it already reads the Spec's declared value.

- **Relation to other ACs:** AC-9 ensures testing uses Spec values; AC-11 ensures the code does too.

- **Test:** Spec object is the only lookup source. No parallel data structure or re-derived list. Grep `SpecCard.tsx` for any hardcoded capacity numbers — should find none.

---

### AC-12: Card Rendering Per Family Context
**Card displays correctly regardless of which palette family it was opened from.**

- **No family-specific logic:** Card displays the same deliverables and requirements for a spec, whether it was clicked in the Power family, Transport family, or any other.

- **Scope:** Spec data is global (SPECS), not family-specific. The card is opened from a family list, but its content is family-agnostic.

- **Test:** Open spec card for `pow_wind` from Power family → displays "Generates 8 MW". (Hypothetically) open same spec if it appeared in another family → displays identically.

---

### AC-13: No State Pollution — Card Dismissal
**Opening a card does NOT mutate the palette sort, selected tool, or game state. It is a UI-only modal overlay.**

- **Game state unchanged:** `state.tool`, `state.funds`, `state.buildings`, etc. remain unaffected by opening/closing a card.

- **Palette state unchanged:** `BuildTab`'s family/scroll state is unaffected. Closing card does not reset family selection or scroll position.

- **Test:** Select a tool, open card for a locked spec, close card → tool is still selected. Scroll down in a family, open card, close card → scroll position preserved.

---

### AC-14: Available-First Rendering Interaction with Placeholder Specs
**Placeholder specs (marked `placeholder: true`) are always grouped at the bottom of their family, after all locked real specs.**

- **Sort rule refinement:** Within each family:
  1. Available real specs (isPlaceable && !placeholder) — first
  2. Locked real specs (!isPlaceable && !placeholder) — second
  3. Placeholder specs (placeholder === true) — last

- **Rationale:** Placeholders are "coming soon" roadmap items; real locked specs are unlockable. Both are visually greyed, but locked real specs should appear before pure placeholders.

- **Test:** Family with specs [available_real, locked_real, placeholder]. Render → [available_real, locked_real, placeholder]. No reordering of placeholders.

---

### AC-15: Mouse/Touch Interactions
**Card opening is triggered by click/tap on a locked spec button. Card closing by click outside is click/tap anywhere outside card.**

- **Click:** Standard mouse click on `.pal-item` locked button → card opens.

- **Tap:** On touch devices, tap (single touch, not long-press) → card opens.

- **Dismiss by tap outside:** Tap on map or palette area outside card → card closes (same as mouse click outside).

- **Drag to dismiss:** Dragging outside card does not prematurely dismiss it (drag is not a click). Only the tap/click release outside dismisses.

- **Test (manual/integration):** On desktop, click locked spec → card opens. Click map area → card closes. On mobile, tap locked spec → card opens. Tap outside → closes.

---

## Testing Strategy

### Unit Tests (node --test)
- `isPlaceable(state, spec)` returns false for locked specs and placeholders (existing, verify no regression).
- Sort comparator (new): given two specs with different unlock levels and isPlaceable states, comparison returns correct ordering (-1, 0, 1).
- Spec data sourcing: `SpecCard` reads only from `SPECS[id]` properties; no hardcoded strings or re-derived values (Grep test: no magic numbers in component).

### Component/Integration Tests
- BuildTab renders families with items in correct sort order (available first, locked second, placeholders last).
- Clicking locked spec opens card; clicking available spec does NOT open card (selects for placement instead).
- Card displays all present Spec properties (cost, served, jobs, mw, residents, tourism, upkeep).
- Card closes on Esc, click outside, or clicking another locked spec.
- Tab navigation reaches locked spec button, Enter opens card.

### Regression Tests
- Available spec remains selectable, draggable, and responsive to key shortcuts (1–9).
- Palette scroll position preserved after opening/closing card.
- No change to placed building behavior or game tick simulation.

---

## Non-Acceptance Criteria (Out of Scope)

- **Spec search/filter:** Filtering palette items by name or property (e.g., "show all power generators") is not required.
- **Comparison view:** Comparing two specs side-by-side is not required.
- **Favourites/pinning:** Saving favourite specs or pinning to top is not required.
- **Animation:** Card open/close animations are not required (instant pop is acceptable).
- **Transitive requirements:** Showing "to unlock this, you need X which requires Y..." chains is not required (only direct unlock level shown).
- **In-game affordability check:** Card does not show "you can/cannot afford this" — the palette button is `disabled` if funds are insufficient (existing behavior, kept as is).

---

## References

- **Current palette code:** `webconsole/src/components/bottom/BottomBar.tsx` (BuildTab, lines 22–124)
- **Spec data:** `webconsole/src/sim/data.ts` (SPECS, PALETTE, Spec interface, isPlaceable, specUnlocked)
- **Example: locked item title tooltip:** BottomBar.tsx line 86 (existing placeholder/locked check)
- **Unlock gate:** `engine.ts` line 235 (specUnlocked function)
- **Design precedent:** FEAT-1972079891 (Building Activation) shows how to surface prerequisite info via hover and card-style UI
- **GR#3 SSOT:** No hand-duplicated requirement/deliverable strings; read from Spec object only
- **GR#5 Verbose Confirmations:** Card should clearly state unlock level and deliverables, not cryptic codes

---

## AC Summary

| AC | Title | Type |
|----|-------|------|
| AC-1 | Available-first sort within families | Sort logic |
| AC-2 | Locked items greyed out and disabled | Rendering |
| AC-3 | Hover tooltip for locked items | UX/Accessibility |
| AC-4 | Click locked item opens card | Feature core |
| AC-5 | Card dismissal (click outside / Esc) | UX |
| AC-6 | Keyboard/mouse accessibility | Accessibility |
| AC-7 | No regression to available items | Regression gate |
| AC-8 | Sort determinism | Correctness |
| AC-9 | Balance numbers placeholder regime | Data sourcing |
| AC-10 | Unlock level display clarity | UX clarity |
| AC-11 | Card data consistency (SSOT) | Architecture |
| AC-12 | Card rendering per family context | Scope boundary |
| AC-13 | No state pollution on card open/close | Side effect gate |
| AC-14 | Placeholders always at bottom | Sort edge case |
| AC-15 | Mouse/touch interactions | Input handling |

**Total ACs: 15**
**Total DDs: 4**

---

## State Gaps & Implementation Notes

### Data Structure Extensions (if any needed)
- **Spec.unlock:** Already present; range ~1–19 (level thresholds). No change.
- **Spec.placeholder:** Already present; boolean flag. No change.
- **Spec properties (cost, upkeep, served, jobs, mw, residents, tourism):** All present; card iterates and renders only if present. No new fields required.

### New Components/Functions
- **SpecCard component:** New React component (or modal wrapper) to display spec details. Should accept `spec: Spec | null` and `onClose: () => void` as props.
- **sortPaletteItems function:** Utility to sort items in each PALETTE family according to AC-1 rule. Takes `state: SimState`, `items: string[]` and returns sorted array.

### Existing Code Touch Points
- **BottomBar.tsx BuildTab:** Update item list rendering to call `sortPaletteItems()`. Update onClick handler to route locked specs to card-open logic. Keep placeholder items disabled (no change).
- **data.ts isPlaceable:** No change (already the gate).
- **engine.ts specUnlocked:** No change (already the unlock check).

### No Changes Required
- **Game state (engine.ts reducer):** No action types added; card is a UI concern.
- **PALETTE constant:** No change to data; sorting is computed at render time.
- **Existing tests:** Regression tests should pass unchanged (AC-7 gate).
