# FEAT-2326609720: HUD Overlay Replan – Non-Overlap Layout & Tabbed Information Architecture

**Intent:** Restructure the React webconsole HUD to eliminate modal/toast overdraw defects, introduce a smooth TAB system for information navigation, and apply dynamic RED/AMBER/GREEN colour-coding driven by live sim health state. Relocate right-side info panels to a left-side nested tab column, implement a hard non-overlap invariant, and ensure every window is user-closable.

**Feature Code:** FEAT-2326609720  
**Related Defects:** BUG-496, BUG-497, BUG-498, BUG-499, BUG-500

---

## Non-Overlap Layout Contract

The HUD layout is divided into **five mutually exclusive rendering regions** to guarantee no element draws atop another:

### Layout Regions (Priority Order, Back to Front)

1. **Map/Canvas Layer (bottom):** The city map, terrain, roads, buildings, overlays — the core game world. Fills the space not reserved by HUD panels.
2. **Left Nested-Tab Column (fixed width ~320px):** A scrollable vertical stack of expandable tabs docked to the left edge, containing all info panels currently on the right side (finance, services, population, build/zoning, etc.). **No modal or toast may overlap this region.**
3. **Right Gutter (fixed width ~80px, far-right lower half):** Reserved exclusively for the build-queue status indicator (currently the unlabelled thin green line, BUG-499). **This region is interactive and not interceptable by toasts or modals.**
4. **Modal Layer (exclusive):** A single modal at most one visible at any time — mutually exclusive with the toast lane. Modals center or dock as needed (e.g., Forced Asset Sales, Bailout Popup, Decline Screen). **All modals must expose a close button (×) or ESC key binding.** Modal appears above all other content with a semi-transparent backdrop.
5. **Toast Lane (exclusive from modals, top-right or bottom-left corner):** Transient alert messages. **Toasts must NOT capture pointer events; clicks pass through to underlying interactive controls.** If a toast would cover an interactive control, it repositions or shrinks.

### Invariant Rules (Mechanically Checkable)

- **I-1 (Z-ordering):** Every HUD element has an assigned z-index; canvas = 0, left-tab column = 100, right gutter = 110, modal backdrop = 500, modal content = 501, toast = 200. No z-layer inversion per rendering frame.
- **I-2 (Region confinement):** An element is confined to its region if its bounding box (in screen coordinates) does NOT intersect any other region's bounding box, OR if it is explicitly a child of that region's container.
- **I-3 (Pointer passthrough):** Toasts use `pointer-events: none` on their container; clicks and drags pass through to the underlying map or control. If a toast must be interactive (e.g., an action button), it re-enables `pointer-events: auto` only on that button element.
- **I-4 (Modal closure):** Every modal is closable via: (a) a visible close button (×), (b) ESC key, or (c) user action that resolves the modal (e.g., accepting an agreement). A modal trapped with no escape path is a blocker defect.
- **I-5 (Gutter exclusivity):** The right gutter (build-queue region) is never obscured by toasts, modals, or left-tab expansion. If a toast or modal would cover it, they reposition.

---

## Tab Taxonomy

### Proposed Left-Side Nested Tab Tree

```
TAB ROOT: Information
├─ TAB: Finance (top-level, always visible header)
│  ├─ Budget Overview (current balance, income/outflow summary)
│  ├─ Ledger (transaction history — collapsible detail)
│  ├─ Loans & Debt (active loans, repayment schedules)
│  └─ Tax Settings (rate adjustments, exemptions)
│
├─ TAB: Services (top-level)
│  ├─ Coverage Map (service coverage %, pop. served)
│  │  ├─ Fire (coverage %, budget, incidents/month)
│  │  ├─ Police (coverage %, budget, crime rate)
│  │  ├─ Health (coverage %, budget, health index)
│  │  └─ (others as unlocked)
│  ├─ Service Queue (pending service requests, wait time)
│  └─ Contracts (external service buy-in toggles if unlocked)
│
├─ TAB: Population (top-level)
│  ├─ Census (total pop., age distribution, occupation)
│  ├─ Migration (inflow/outflow, attractiveness state)
│  ├─ Households (count, average size, tenure)
│  └─ Employment (by sector, unemployment %)
│
├─ TAB: Build & Zoning (top-level)
│  ├─ Buildable Sites (count, space, zoning type)
│  ├─ Queue (build orders, progress, cost, ETA)
│  └─ Zoning Map (overlay toggle for zoning types)
│
├─ TAB: Projections (top-level, if forecast data available)
│  ├─ Demand (6-month forecast: housing, jobs, services)
│  └─ Revenue (6-month budget forecast)
│
└─ TAB: Alerts (top-level, collapsible, acts as a filter/severity view)
   ├─ Critical (RED items needing immediate attention)
   ├─ Warning (AMBER items requiring monitoring)
   └─ Info (GREEN items, status updates)
```

**ASSUMPTION FOR AARON/BEV:** This taxonomy is a PROPOSAL for sign-off. It is derived from the current right-side info panels and Aaron's stated "rich layered tabs" intent. Exact nesting, whether all tabs are simultaneously visible or in a hamburger menu, and the order of top-level tabs are **Aaron's call** — confirm the structure before build.

---

## RAG Colour-Coding

Every indicator in the HUD **MUST derive its RAG colour from a named sim store selector or state value, never a hardcoded constant** (GR#15). The following table lists each indicator, the sim value driving it, and the thresholds.

| Indicator | Sim Value / Selector | RED Threshold | AMBER Threshold | GREEN Threshold | Notes |
|-----------|---------------------|---------------|-----------------|-----------------|-------|
| **Budget Balance** | `sim.store.finance.currentBalance` | < 0 (insolvent) | 0 ≤ balance < X months expenses | ≥ X months expenses | Threshold X = *placeholder, Aaron to approve* |
| **Income vs Outflow** | `sim.store.finance.monthlyNetCashFlow` | < 0 (deficit) | = 0 (break-even) | > 0 (surplus) | No threshold; derived directly |
| **Service Coverage (by service: Fire, Police, Health)** | `sim.store.services.<serviceName>.coveragePercent` | < 50% | 50–79% | ≥ 80% | Thresholds *placeholder, Aaron to approve* |
| **Population Health Index** | `sim.store.population.healthIndex` (0–100) | < 40 | 40–69 | ≥ 70 | Thresholds *placeholder, Aaron to approve* |
| **Unemployment Rate** | `sim.store.population.unemploymentRate` (%) | > 15% | 7–15% | < 7% | Thresholds *placeholder, Aaron to approve* |
| **Migration Attractiveness** | `sim.store.population.attractivenessScore` (scaled) | < 30 | 30–59 | ≥ 60 | Thresholds *placeholder, Aaron to approve*; selector name **BA ASSUMPTION — confirm exact name** |
| **Build Queue Status** | `sim.store.buildQueue.totalCostRemaining` vs `currentBalance` | cost > balance × 150% (can't afford) | cost ≤ balance × 150% and > balance | cost ≤ balance | Derived from two values; no single selector. If a single derived field exists, use that. **BA ASSUMPTION** |
| **Service Request Backlog** | `sim.store.services.<serviceName>.pendingRequestCount` | > threshold T_red | threshold T_amber–T_red | ≤ T_amber | Thresholds *placeholder, Aaron to approve*; one selector per service |
| **Crime Rate** | `sim.store.population.crimeRate` (crimes/month or %) | > 20% | 10–20% | < 10% | Thresholds *placeholder, Aaron to approve* |
| **Pollution/Air Quality** | `sim.store.environment.pollutionIndex` (0–100) | > 70 | 40–70 | < 40 | Thresholds *placeholder, Aaron to approve*; selector name **BA ASSUMPTION** |
| **Traffic Congestion** | `sim.store.traffic.congestionScore` (0–100) | > 75 | 50–75 | < 50 | Thresholds *placeholder, Aaron to approve*; selector name **BA ASSUMPTION** |

### RAG Application Rules

- **Display:** Every indicator row, card, or icon in the HUD displays a small RAG circle or bar segment coloured RED/AMBER/GREEN per the table above.
- **Fallback (GR#1):** If a sim value is undefined, null, or outside expected range, render the indicator in GREY with a tooltip `"Data unavailable"` — never blank or crash. Log the missing value to browser console for debugging.
- **Refresh:** RAG colour updates on every sim state update (typically per tick). No stale-display risk.

---

## Acceptance Criteria

### AC-1: Non-Overlap Invariant – Modals Do Not Overdraw Toasts

**Criterion:** When both a modal and a toast are active, the modal is visually in front; toasts do not appear behind or partially obscured by the modal backdrop or content.

**Testable:** Dispatch a bailout popup (BUG-496 scenario) and a toast message simultaneously. Verify the modal is fully visible and the toast is not visible (or is suppressed). Close the modal; the toast then appears.

**Related defects:** BUG-496, BUG-497.

---

### AC-2: Non-Overlap Invariant – Right Gutter Isolation

**Criterion:** The build-queue indicator (right gutter, ~80px wide, far-right lower half) is never occluded by toasts, modals, or left-tab expansion. It remains clickable and visible at all times.

**Testable:** Expand all left-side tabs to maximum width. Open a modal and fire a toast. Verify the right gutter is fully visible and does not move. Click the gutter indicator; it responds. Close the modal and toast; gutter remains stable.

**Related defects:** BUG-499.

---

### AC-3: Right-Gutter Indicator – Label and Affordance

**Criterion:** The build-queue indicator in the right gutter has a persistent, legible label (e.g., "Queue: 3 orders, 45,000 MP") and a mouse-over tooltip that clarifies its purpose. The indicator is not a bare green line.

**Testable:** Hover over the right-gutter indicator. A tooltip appears. The label is rendered in at least 12pt font, colour-contrasting with its background. No test passes if the indicator is unlabelled.

**Related defects:** BUG-499.

---

### AC-4: Forced Asset Sales Modal – Closable via Button and ESC

**Criterion:** The Forced Asset Sales modal (triggered when the city cannot meet payment obligations) exposes a close button (×) in the top-right corner AND responds to the ESC key to dismiss it without resolving the sale. Clicking the close button dismisses the modal and returns the game to its prior state (no forced sale).

**Testable:** Trigger a forced asset sale scenario. Verify a close button is visible and clickable. Click it; the modal disappears and no sale occurs. Reopen the modal and press ESC; same result.

**Related defects:** BUG-498.

---

### AC-5: Bailout Popup – Does Not Re-Fire Automatically

**Criterion:** The insolvency bailout popup (BUG-496: currently re-fires every tick until dismissed) is raised exactly once per insolvency event. It does not re-fire on subsequent ticks if the city remains insolvent but the popup was dismissed. If the city becomes solvent and then insolvent again, a new popup fires.

**Testable:** Enter an insolvent state (balance < 0). The bailout popup appears. Dismiss it (via close button or ESC). Advance 5 ticks without correcting the insolvency. Verify the popup does not reappear. Correct the insolvency and revert it; a new popup fires on the second insolvency event.

**Related defects:** BUG-496.

---

### AC-6: Decline Hard-Stop – Paused-Clock Indicator and Non-Trapping Modal

**Criterion:** When a decline event hard-stops the game (e.g., population collapse), a modal is raised (Decline Screen) with a paused-clock icon (⏸ or similar) to signal the game is halted. The modal is closable via a close button or ESC. Pressing ESC or the close button does NOT resume the game but closes the modal, leaving the city in a paused state pending player action (e.g., load save, start over).

**Testable:** Trigger a decline scenario (e.g., zero population). A modal appears with a paused-clock icon visible. Press ESC; the modal closes but the game clock remains paused. Click the close button; same result. The city does not automatically resume.

**Related defects:** BUG-497.

---

### AC-7: Toast Messages – Pointer Passthrough (No Click Interception)

**Criterion:** Alert/info toast messages use CSS `pointer-events: none` on their container, allowing mouse clicks and drags underneath to reach interactive controls (e.g., build placement, tile selection). If a toast contains an interactive element (e.g., an "Undo" or "Dismiss" button), only that element has `pointer-events: auto`.

**Testable:** Fire an info toast (e.g., "Building placed: +3 employment"). While the toast is visible, attempt to place a building on a tile behind the toast. The placement succeeds (the click passes through the toast). Dismiss the toast and verify no phantom placement occurred.

**Related defects:** BUG-500.

---

### AC-8: Left-Tab Column – Smooth Navigation (No Jank)

**Criterion:** Clicking a top-level tab in the left column (Finance, Services, Population, etc.) smoothly slides or cross-fades to its content with no visual stutter, frame drop, or layout shift. Expanding nested tabs within a section does not cause the entire column to re-layout jankily.

**Testable:** Click between Finance, Services, and Population tabs 5 times rapidly. Measure frame rate (browser DevTools) during transitions; maintain ≥ 60 FPS or gracefully degrade to ≥ 30 FPS. No involuntary scroll jumps or text reflow.

**Related defects:** None named; this is a top-level requirement.

---

### AC-9: Left-Tab Column – Nested Expansion Does Not Overlap Other HUD Regions

**Criterion:** When a nested tab (e.g., Service Coverage, Ledger) expands, it does not grow beyond the left column's bounding box or overlap the canvas, right gutter, or any modal. If content is too tall for the column, it scrolls within the column; it does not expand beyond.

**Testable:** Expand all nested tabs in Finance (Budget, Ledger, Loans, Tax). Verify all content fits within the left column. Scroll the nested content if needed. The right gutter and map remain fully visible and unaffected.

**Related defects:** BUG-499 (gutter occlusion prevention).

---

### AC-10: RAG Colour Derivation – Budget Indicator

**Criterion:** The Budget Balance indicator displays RED if `currentBalance < 0`, AMBER if `0 ≤ currentBalance < X_threshold`, and GREEN if `currentBalance ≥ X_threshold`. The threshold X is stored in config/constants and sourced from sim data, not hardcoded in React. If the balance is undefined, the indicator renders GREY with a "Data unavailable" tooltip.

**Testable:** Inspect the React code and sim store. Verify the budget indicator's colour derivation reads from `sim.store.finance.currentBalance` and a defined threshold constant. Set the balance to a value triggering each colour (e.g., -10 for RED, 50K for AMBER if threshold is 100K, 200K for GREEN). Verify the colour matches. Set balance to null; verify GREY with fallback tooltip. No hardcoded RGB values in the component.

**Related defects:** None named; this is a Golden Rule mandate (GR#15, GR#1).

---

### AC-11: RAG Colour Derivation – Service Coverage Indicators

**Criterion:** Each service (Fire, Police, Health) displays coverage in RAG: RED if coverage < 50%, AMBER if 50–79%, GREEN if ≥ 80% (thresholds placeholder pending Aaron's approval). Coverage percentage derives from `sim.store.services.<serviceName>.coveragePercent`. If coverage is undefined, render GREY with "Data unavailable".

**Testable:** Inspect sim store selectors for `services.fire.coveragePercent` (or equivalent). Set coverage to 40% (RED), 65% (AMBER), 90% (GREEN). Verify RAG colours match. Set to null; verify GREY fallback.

**Related defects:** None named; Golden Rule.

---

### AC-12: All Indicators Have Fallback Rendering (GR#1)

**Criterion:** Every RAG-coloured indicator has a defined fallback behaviour if its sim value is unavailable, out of range, or unexpected. The fallback is GREY with a tooltip "Data unavailable", never a blank space, error message, or crash.

**Testable:** In browser DevTools, mock a sim store update where a health metric is undefined. Render the HUD. Verify the corresponding indicator renders GREY and does not blank or throw a React error. Check the browser console for a debug log (e.g., "RAG fallback: healthIndex undefined").

**Related defects:** None named; Golden Rule (GR#1).

---

### AC-13: Modal Closure – Bailout, Decline, Forced Sales

**Criterion:** The three primary modals (Bailout Popup, Decline Screen, Forced Asset Sales) are each independently testable as closable. Each has a close button (×) AND responds to ESC. Closing via either method dismisses the modal without side effects (e.g., no unexpected transaction completion).

**Testable:** (a) Trigger insolvency → bailout popup → close via button and ESC (separately). (b) Trigger decline → decline modal → close via button and ESC. (c) Trigger forced asset sale → modal → close via button and ESC. None of these should execute the action (sale, bailout loan, etc.); they dismiss the prompt only.

**Related defects:** BUG-496, BUG-497, BUG-498.

---

### AC-14: Tab Navigation – Keyboard Shortcuts (Optional but Recommended)

**Criterion:** (Placeholder; Aaron to approve if included.) Keyboard shortcuts (e.g., Alt+F for Finance, Alt+S for Services) allow rapid tab switching. ESC always closes an active modal or returns to the default tab view.

**Testable:** Press Alt+F; Finance tab activates. Alt+P; Population tab activates. From within a modal, ESC dismisses it. This criterion is NICE-TO-HAVE; do not block launch on it.

**Related defects:** None named.

---

### AC-15: Build-Queue Right Gutter – Interactive and Responds to Clicks

**Criterion:** Clicking the build-queue indicator in the right gutter opens a build-queue detail panel or modal (or navigates to the Build & Zoning tab). The interaction is not blocked by toasts or other HUD elements.

**Testable:** Click the right-gutter indicator while toasts, modals, and other HUD elements are active. Verify the click is registered and the expected action fires (e.g., modal opens, tab switches). No ghost clicks or no-ops.

**Related defects:** BUG-499.

---

### AC-16: Left-Tab Migration – Information Relocation from Right to Left

**Criterion:** All information panels previously docked on the right side of the HUD (Finance, Services, Population, Build/Zoning, Projections, Alerts) are relocated to the left-side nested tab column. No information is lost or duplicated. The right side is now reserved for the build-queue gutter and the map.

**Testable:** Compare the new HUD with the old in a side-by-side screenshot or video. Verify every old-right panel is now a tab or nested tab on the left. Verify the right side is clear except for the gutter.

**Related defects:** None named; this is the feature's core architectural change.

---

### AC-17: Error State Handling – Undefined or Stale Selectors

**Criterion:** If a selector used for RAG colour-coding does not exist in the sim store at runtime (e.g., `sim.store.services.electricity.coveragePercent` but electricity service is not yet unlocked), the indicator is rendered in GREY with a tooltip "Service not yet unlocked" or "Data unavailable", not RED/AMBER/GREEN based on a missing value.

**Testable:** Mock a scenario where a service is locked (selector returns undefined). Render the HUD. Verify the indicator is GREY and does not incorrectly interpret undefined as RED. Check the console for a debug log.

**Related defects:** None named; Golden Rule (GR#1).

---

### AC-18: z-Index Discipline – No Inversion

**Criterion:** All HUD elements have explicit z-index assignments. Canvas = 0, left column = 100, right gutter = 110, modal backdrop = 500, modal content = 501, toast = 200. No element renders in front of a modal (except the modal itself). No inversion occurs across frames.

**Testable:** Inspect the CSS or React component z-index attributes. Verify no element has z > 501 except debug/dev overlays (which are not shipped). Open browser DevTools → Layers (or similar) and verify the stacking order matches the design.

**Related defects:** BUG-496, BUG-497, BUG-498, BUG-499, BUG-500.

---

## Assumptions for Aaron / Bev

1. **RAG Selector Names (GR#15):** The exact sim store selectors for RAG indicators (Migration Attractiveness `attractivenessScore`, Pollution `pollutionIndex`, Traffic Congestion `congestionScore`) are **assumed to exist as named**. If the actual selector names differ, the BA must update the table before development. Developer must confirm the selector path at build time and file a BOW comment if a selector is missing or renamed.

2. **RAG Thresholds (GR#15):** All numeric thresholds (e.g., "AMBER if 50–79% coverage") are **PLACEHOLDER** and pending Aaron's approval. These are balance decisions (precedent: balance-number-regime.md); do not hardcode them until Aaron has reviewed and signed off on each. Store thresholds in a config object or constants file sourced from Aaron's decisions.

3. **Tab Taxonomy Structure:** The left-side nested tab tree (Finance → Budget/Ledger/Loans/Tax, Services → Coverage/Queue/Contracts, etc.) is a **PROPOSAL for Aaron's sign-off**. The exact nesting depth, whether all tabs are simultaneously visible, whether a hamburger menu is used, and the default open/closed state of nested tabs are **Aaron's architectural calls**. Confirm the structure before coding.

4. **Modal Closure Precedence:** When a modal is open and the player performs an action that would change the game state (e.g., building placement while the Decline modal is open), the modal takes precedence and the action is queued or blocked. This rule is implicit in AC-4 and AC-6 but should be confirmed with Aaron.

5. **Toast Lane Position (Right vs Top):** The doc references toasts in "top-right or bottom-left corner" but does not specify which is canon. **Aaron to confirm:** should toasts appear top-right, bottom-left, or bottom-center? Should they auto-dismiss after N seconds or persist until manually closed?

6. **Right Gutter Width (80px):** The right gutter width is assumed **~80px** based on the current thin green line. Aaron to confirm whether this width accommodates the proposed label and icon design without further visual truncation.

7. **Fallback Rendering (GR#1):** The assumption is that GREY + "Data unavailable" is the canonical fallback for any missing or invalid RAG indicator. If Aaron prefers a different fallback (e.g., a greyed-out icon, an "Unknown" state, or hiding the indicator entirely), that must be approved before build.

---

## Notes for Developers

- **Do NOT invent new sim store selectors.** If a selector is missing (e.g., `attractivenessScore` for Migration), file a BOW item asking the engine team to expose it. Do not guess its name or add ad-hoc computation in the UI layer (violates GR#25).
- **Commit the layout CSS changes and component restructuring together:** the z-index discipline, region confinement, and pointer-events rules are interlocking. A partial commit risks re-introducing the overlaps.
- **Test in browser at low frame rates (DevTools → Rendering → Throttling) to catch jank.** Smooth navigation (AC-8) is a user-facing quality gate.

---

**Last Updated:** 2026-09-01  
**Status:** Acceptance Criteria Draft Awaiting Aaron Sign-Off
