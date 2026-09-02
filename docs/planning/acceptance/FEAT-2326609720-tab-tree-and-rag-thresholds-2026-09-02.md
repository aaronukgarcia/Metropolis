# FEAT-2326609720: Concrete Tab Tree & RAG Threshold Proposal

**Date:** 2026-09-02  
**Status:** Ready for Aaron's Row-by-Row Approval  
**Grounded in:** RightDock.tsx (current 14 tabs), MapView.tsx (banners/modals), data.ts (serviceCoverageOf/serviceDemandOf), engine.ts (wellbeingOf/approvalOf), debugjson.ts (exposed selectors)

---

## 1. THE TAB TREE: Current → Proposed Structure

**Mapping table: [Current Panel/Info] → [New Top-Level Tab] → [Nested Child Tabs/Panels]**

| Current Tab | Current Content | → New Top-Level | → New Nested Child | Notes |
|-------------|-----------------|-----------------|-------------------|-------|
| **Status** | Approval, Wellbeing (breakdown), Citizens, Housing cap, Structures, Fiscal state | **Finance** | **Budget Overview** | Wellbeing overall score; approval; structures upkeep summary; net fiscal state (income − expense) |
| **Rates** | Tax sliders (residential/commercial/industrial) + yields + avg tax warning | **Finance** | **Tax Settings** | Full rate control + per-category yield display + city-wide tax average warning |
| **Earnings** | Income by source (taxes, tourism, grid export); Outflows (service upkeep, grid import, loans) | **Finance** | **Ledger** | Inflows table + Outflows table + Net margin + Grid import/export highlighted |
| (implied by ledger history) | (not yet tabbed; users ask for transaction history) | **Finance** | **Ledger Detail** | Collapsible historical entries from state.ledger (if available, else placeholder) |
| (Status tab references loans in the crisis flow) | (no loan UI exists yet, forward-declared in spec AC-1) | **Finance** | **Loans & Debt** | FUTURE: active loans, repayment schedules. Currently STUB. |
| **Power** | Capacity, Need, Imported MW, Grid Import toggle, External cover explanation | **Services** | **Power Coverage** | Coverage %, capacity/need balance, import cost, toggle with explanation |
| **Water** | Clean capacity, Discharge capacity, demand, plant list, pipe tiers | **Services** | **Water Coverage** | Coverage %, clean/discharge balance, pipe utilization per plant, upgrade buttons |
| **Waste** | Collection coverage %, generated/collection capacity, diversion rate, processing mix, EfW power, material revenue | **Services** | **Waste & Recycling** | Coverage %, diversion rate, processing mix table, recovered power/materials |
| (Policing, Fire, Health are references in wellbeing but no dedicated tab yet) | (covered by serviceDemandOf but no UI tab) | **Services** | **Coverage Map** | Per-service tile: Fire %, Police %, Health (GP + Hospital separate), with coverage % + budget + key metric (e.g. incidents/month) — see RAG table below for indicators. FUTURE tab, placeholder in tree. |
| (no explicit service queue tab) | (service requests are part of demand model) | **Services** | **Service Queue** | FUTURE: pending requests + wait times. Currently STUB. |
| (external cover is in Power spec AC-2, not yet built) | (toggles for external buy-in) | **Services** | **Contracts** | FUTURE: external service cover toggles if unlocked (inc2/inc3 features). Currently STUB. |
| **Population** | Births, move-ins, deaths, move-outs, Sankey flow, arrivals-by-mode Sankey | **Population** | **Census** | Total pop, age distribution (FUTURE; currently only births/deaths tracked), occupation (FUTURE) |
| (implied by population tab) | Population flows over last tick | **Population** | **Migration** | Inflow/outflow rate, attractiveness score (GR#15: MISSING SELECTOR — see below), net migration trend |
| (Housing cap is in Status tab) | Housing cap, under-construction, not-on-road breakdowns | **Population** | **Households** | Count, average size (FUTURE), tenure breakdown (FUTURE) |
| (Employment is derived but not tabbed) | Jobs vs workers, unemployment % | **Population** | **Employment** | By sector (commercial/industrial/office/mine), unemployment %, job deficit/surplus |
| **Milestones** | Milestone list + met/open status | **Alerts** (or **Projections**) | **Milestones** | Milestone tracker, progress toward each |
| (implied by advisor on map) | Auto-build suggestions, demand warnings | (implied; not explicit tab) | (could be part of Alerts or Projections) | Currently rendered as on-map advisor floating text; no dedicated info tab. May fold into Alerts. |
| **Debug** | debug.json, error list, dev cheats, snapshot controls | **Debug** (separate, dev-gated) | (single tab, not nested) | Remains as-is; gated behind dev builds. |
| (implied by Level-up banner, Placement notice, Insolvency banners, etc.) | Transient alerts (level-up, placement blocked, insolvency, bailout, decline) | **Alerts** | **Critical** | RED items needing immediate action (insolvency band, bailout, decline hard-stop, failed placement) |
| (continuation of above) | (continuation) | **Alerts** | **Warning** | AMBER items requiring monitoring (insolvency warning band, service shortfalls, housing cap nearing, unemployment rising) |
| (continuation) | (continuation) | **Alerts** | **Info** | GREEN items and status updates (level-up, new unlock, high wellbeing, surplus budget) |
| **Units** | Unit registry, physical entities metadata | (optional; metadata only) | (could remain separate or fold into settings) | Low-frequency reference; recommend a separate **Settings/Info** tab or hide behind a "?" button. Currently standalone tab in RightDock; optional to relocate. |
| **Policy** | Policy toggles (recycling, transit subsidy, tourism drive, austerity) | **Finance** or **Build & Zoning** | **Policies** | Policies that affect economy should be in Finance or a shared Policies tab. Recommend Finance as the owner. |
| (implied by XP tab) | City level, XP, unlock ladder, specialist buildings | **Build & Zoning** | **Specialists** | Unlocked landmark buildings, level requirement, count built |
| (implied by build queue visual feedback) | Queue indicator on map (right gutter; BUG-499), queue orders, progress, cost, ETA | **Build & Zoning** | **Build Queue** | Active build orders, progress bars, remaining cost, time to completion |
| (implied by zone selection tool) | Buildable sites, available zoning types | **Build & Zoning** | **Buildable Sites** | Count of available sites, space, zoning type summary |
| (implied by map layer toggles) | Zoning map overlay toggle (future; currently no zoning layer exists) | **Build & Zoning** | **Zoning Map** | Toggle to show zoning types on map (FUTURE; currently only road/rail overlays exist) |
| (implied by projections in spec) | 6-month forecast: housing/jobs/services demand; revenue forecast | **Projections** (if forecast data available) | **Demand** | Housing, job, service demand trend (FUTURE; currently only live demand, no forecast) |
| (continuation) | (continuation) | **Projections** | **Revenue** | Budget forecast (FUTURE; currently only live flows) |
| **Lines** | Road/rail line saturation %, usage, capacity | **Build & Zoning** (or **Infrastructure**) | **Lines & Networks** | Line saturation tile view (road/m20/rail/hs1), usage vs capacity, headroom indicator |

---

## Proposed Left-Side Nested Tab Tree (Aaron Approves/Edits Per Row)

```
INFORMATION (root dock title)
│
├─ FINANCE (top-level tab, always visible header)
│  ├─ Budget Overview
│  │   ├─ Current balance (RAG: RED/AMBER/GREEN)
│  │   ├─ Income vs outflow summary (RAG: RED negative/AMBER break-even/GREEN positive)
│  │   ├─ Monthly cash flow chart (if history available)
│  │   └─ Structures upkeep total
│  ├─ Ledger (transaction history)
│  │   ├─ Inflows by category (taxes, tourism, grid export, materials revenue)
│  │   ├─ Outflows by category (service upkeep, grid import, loan repayment)
│  │   └─ Collapsible detail (per-entry drill-down if ledger history exposed)
│  ├─ Loans & Debt (forward-declared; currently STUB)
│  │   └─ Active loans, repayment schedules (not yet built)
│  └─ Tax Settings
│     ├─ Sliders for residential, commercial, industrial rates
│     ├─ Per-category yield display
│     ├─ Policies toggle (recycling, transit subsidy, tourism drive, austerity)
│     └─ Avg tax warning (if >15% avg, show impact on approval + migration)
│
├─ SERVICES (top-level tab)
│  ├─ Coverage Map (service-by-service grid)
│  │  ├─ Fire
│  │  │   ├─ Coverage % (RAG)
│  │  │   ├─ Budget (from flows)
│  │  │   └─ Key metric: incidents/month (FUTURE; currently not tracked)
│  │  ├─ Police
│  │  │   ├─ Coverage % (RAG)
│  │  │   ├─ Budget
│  │  │   └─ Key metric: crime rate (FUTURE; currently not tracked)
│  │  ├─ Health (split as GP + Hospital)
│  │  │   ├─ Coverage % (RAG)
│  │  │   ├─ Budget
│  │  │   └─ Wellbeing health index from wellbeingOf()
│  │  ├─ Water (Clean + Waste)
│  │  │   ├─ Coverage % (RAG; state-derived from clean/waste balance)
│  │  │   ├─ Budget
│  │  │   └─ Headroom (capacity − demand)
│  │  ├─ Waste & Recycling
│  │  │   ├─ Coverage % (RAG)
│  │  │   ├─ Budget
│  │  │   └─ Diversion rate (%)
│  │  └─ Power
│  │     ├─ Coverage % (RAG; derived from capacity ≥ need)
│  │     ├─ Budget + Grid import cost
│  │     └─ Imported MW (if external cover active)
│  ├─ Service Queue (forward-declared; currently STUB)
│  │   └─ Pending requests, wait times
│  └─ Contracts (forward-declared; currently STUB)
│     └─ External service buy-in toggles (inc2/inc3)
│
├─ POPULATION (top-level tab)
│  ├─ Census
│  │  ├─ Total population (live)
│  │  ├─ Age distribution (FUTURE; currently births/deaths tracked, not age cohorts)
│  │  └─ Occupation breakdown (FUTURE; currently job-type available but not UI'd)
│  ├─ Migration
│  │  ├─ Inflow/outflow (last tick + running average)
│  │  ├─ Attractiveness score (GR#15: MISSING SELECTOR; see section 3 below)
│  │  └─ Net migration trend (births + moveIns − deaths − moveOuts)
│  ├─ Households
│  │  ├─ Count (= state.population, proxy)
│  │  ├─ Average size (FUTURE; requires household aggregation, not exposed)
│  │  └─ Tenure breakdown (FUTURE; currently not tracked)
│  ├─ Employment
│  │  ├─ Total jobs vs workers (state-derived from building kinds)
│  │  ├─ By sector (commercial/industrial/office/mine)
│  │  └─ Unemployment % (RAG)
│  └─ Demographic flows (Sankey + arrivals-by-mode; same as current Population tab)
│
├─ BUILD & ZONING (top-level tab)
│  ├─ Buildable Sites
│  │  ├─ Available count
│  │  ├─ Space (tiles)
│  │  └─ Zoning type summary (residential/commercial/industrial available)
│  ├─ Build Queue
│  │  ├─ Active orders (list with progress, cost, ETA)
│  │  └─ Total cost remaining vs current balance (RAG: can-afford / stretched / cannot-afford)
│  ├─ Specialists (Unlocked Landmarks)
│  │  ├─ List of landmark buildings by unlock level
│  │  └─ Count built per specialist
│  ├─ Lines & Networks
│  │  ├─ Road/M20 saturation
│  │  ├─ Rail/HS1 saturation
│  │  └─ Saturation by line type (usage vs capacity, headroom indicator, RAG)
│  └─ Zoning Map (toggle for map layer; FUTURE)
│     └─ Show/hide zoning types on map canvas
│
├─ PROJECTIONS (if forecast data available; currently placeholder)
│  ├─ Demand (6-month forecast)
│  │   ├─ Housing demand trend
│  │   ├─ Job demand trend
│  │   └─ Service shortfall forecast
│  └─ Revenue (6-month budget forecast)
│     ├─ Income projection
│     ├─ Expense projection
│     └─ Surplus/deficit outlook
│
├─ ALERTS (collapsible, acts as severity filter)
│  ├─ Critical (RED items needing immediate action)
│  │  ├─ Insolvency: "Bailout triggered at tick X; funds at Y; restore solvency within Z ticks"
│  │  ├─ Decline hard-stop: "Population collapse (or other decline trigger)"
│  │  ├─ Failed placement: "Cannot afford or blocked placement"
│  │  └─ Any other RED condition (see RAG table)
│  ├─ Warning (AMBER items requiring monitoring)
│  │  ├─ Insolvency pre-warning: "Treasury approaching threshold"
│  │  ├─ Service shortfalls: "Fire coverage at 45% (target 80%)"
│  │  ├─ Housing cap nearing: "Population within 10% of housing cap"
│  │  └─ Any other AMBER condition
│  └─ Info (GREEN items and status updates)
│     ├─ Level-up: "City level 5 reached; +£50K granted"
│     ├─ New unlock: "Fire stations now available"
│     ├─ High wellbeing: "Overall wellbeing 75 (good)"
│     └─ Any GREEN status milestone
│
└─ DEBUG (separate tab, dev-gated; not in production)
   ├─ debug.json capture + download
   ├─ Error list (with codes, timestamps, stack traces)
   ├─ Dev cheats (if build=dev)
   └─ Snapshot refresh controls
```

---

## 2. RAG THRESHOLD TABLE (Grounded in Real Selectors)

**Every indicator DERIVES its RAG colour from named sim store selectors. NO hardcoded constants. Thresholds are PLACEHOLDERS pending Aaron's approval.**

| Indicator | Sim Value / Selector | Type | RED Threshold | AMBER Threshold | GREEN Threshold | Notes |
|-----------|---------------------|------|---------------|-----------------|-----------------|-------|
| **Budget Balance** | `state.funds` | number (micropounds) | < 0 (insolvent) | 0 ≤ funds < (monthly_expenses × X months) | funds ≥ (monthly_expenses × X months) | X = *placeholder (Aaron to set: 3 months? 6 months?)*. Compute monthly_expenses from state.lastFlows.outflows.sum(). GR#15: derive from data, not hardcode. |
| **Income vs Outflow** | `(state.lastFlows.inflows.sum() − state.lastFlows.outflows.sum())` | number (net flow) | deficit < 0 | zero (break-even) | surplus > 0 | No numeric threshold; direct derivation. Flows are already raw (not formatted). |
| **Service Coverage — Fire** | `serviceCoverageOf(state).find(c => c.id === 'fire')?.coverage` (as %) | 0–1 ratio, display as % | < 50% | 50–79% | ≥ 80% | Thresholds *placeholder, Aaron to approve*. GR#15: ratio from serviceCoverageOf, not mocked. |
| **Service Coverage — Police** | `serviceCoverageOf(state).find(c => c.id === 'police')?.coverage` | 0–1 ratio, display as % | < 50% | 50–79% | ≥ 80% | Same thresholds. Separate indicator per service. |
| **Service Coverage — Health (GP)** | `serviceCoverageOf(state).find(c => c.id === 'gp')?.coverage` | 0–1 ratio, display as % | < 50% | 50–79% | ≥ 80% | Separate indicator for GP clinics. |
| **Service Coverage — Health (Hospital)** | `serviceCoverageOf(state).find(c => c.id === 'hosp')?.coverage` | 0–1 ratio, display as % | < 50% | 50–79% | ≥ 80% | Separate indicator for hospitals. |
| **Service Coverage — Water (Clean)** | `serviceCoverageOf(state).find(c => c.id === 'cleanwater')?.coverage` | 0–1 ratio, display as % | < 50% | 50–79% | ≥ 80% | Water clean supply. |
| **Service Coverage — Water (Waste)** | `serviceCoverageOf(state).find(c => c.id === 'waste')?.coverage` | 0–1 ratio, display as % | < 50% | 50–79% | ≥ 80% | Water waste/sewage treatment. |
| **Service Coverage — Power** | `serviceCoverageOf(state).find(c => c.id === 'power')?.coverage` | 0–1 ratio, display as % | < 1.0 (deficit) | 1.0–1.5 (imported) | > 1.5 or local-only surplus | Power has inverted scale: shortage = RED (brownout), surplus = GREEN. Aaron to confirm thresholds. |
| **Service Coverage — Waste** | `serviceCoverageOf(state).find(c => c.id === 'waste')?.coverage` | 0–1 ratio, display as % | < 50% | 50–79% | ≥ 80% | Collection coverage (refuse collected ÷ generated). |
| **Wellbeing Overall** | `wellbeingOf(state).overall` | 0–100 scale | < 40 | 40–69 | ≥ 70 | Thresholds *placeholder, Aaron to approve*. Function returns overall score + parts array. |
| **Approval Rating** | `approvalOf(state)` | 0–100 scale | < 40 | 40–69 | ≥ 70 | Thresholds *placeholder, Aaron to approve*. Derived from avg tax rate + wellbeing. |
| **Population Health Index** | `wellbeingOf(state).parts.find(p => p.label === 'Healthcare')?.value` OR direct health wellbeing part | 0–100 scale | < 40 | 40–69 | ≥ 70 | Thresholds *placeholder*. Extracted from wellbeingOf's health-component part. |
| **Unemployment Rate** | `(totalJobs(state) − state.population) / state.population * 100` if (state.population − totalJobs > 0) | percentage | > 15% | 7–15% | < 7% | Thresholds *placeholder, Aaron to approve*. Derived from building-capacity job counts minus population. **SELECTOR STATUS: PARTIALLY EXPOSED** — totalJobs exists but may not be directly exposed as a selector; may require synthesis in the UI layer (GR#15: ground in data, not guess). |
| **Population Trend** | `(state.lastDemographics.births + state.lastDemographics.moveIns) − (state.lastDemographics.deaths + state.lastDemographics.moveOuts)` | net per-tick | declining < 0 | stable ≈ 0 | growing > 0 | Directional only; no fixed thresholds. Indicates whether city is shrinking/stable/growing. |
| **Housing Capacity Headroom** | `(onlineResidentsCapacity(state) − state.population) / onlineResidentsCapacity(state) * 100` | percentage | < 5% (at cap) | 5–20% (tight) | ≥ 20% (comfortable) | Thresholds *placeholder*. Derived from state functions already exposed. |
| **Build Queue Affordability** | `state.funds >= buildQueue.totalCostRemaining` (two-value logic) | boolean + ratio | cost > balance × 1.5 (cannot afford) | cost ≤ balance × 1.5, > balance (stretched) | cost ≤ balance (can afford fully) | Thresholds *placeholder*. Requires synthesizing a buildQueue cost — currently toolState has active tool, but no explicit queue; may need to scan state.buildings for under-construction. **SELECTOR STATUS: MISSING — build queue as a distinct structure does not exist yet.** |
| **Crime Rate** | (Currently not tracked in engine; no crimeRate selector) | crimes/month or % | *MISSING DATA SOURCE* | *MISSING* | *MISSING* | Crime tracking is not yet implemented (BUG-YYY placeholder). Placeholder in spec pending feature implementation. Mark as "Not yet available" in UI with a note "Planned for later release". |
| **Pollution / Air Quality** | (Currently not tracked in engine; no pollutionIndex selector) | 0–100 or ppm | *MISSING DATA SOURCE* | *MISSING* | *MISSING* | Pollution/environmental model is not yet built. Placeholder in spec. Mark as "Planned feature". |
| **Traffic Congestion** | `lineUsageOf(state)` filtered to road lines (road/m20), then saturation ratio | 0–100% | > 75% | 50–75% | < 50% | Thresholds *placeholder*. Derived from line saturation overlay; road-class only. |
| **Insolvency State** | `state.insolvencyState` enum | string: 'solvent' \| 'warning' \| 'crisis' \| 'administration' \| 'bailout_second' \| 'decline' | 'crisis' \| 'administration' \| 'bailout_second' \| 'decline' | 'warning' | 'solvent' | State machine; no threshold. Map directly: RED states need alert, AMBER warning, GREEN solvent. |
| **Rail Line Saturation** | `lineUsageOf(state)` filtered to rail lines (rail/hs1) | 0–100% saturation | > 75% | 50–75% | < 50% | Thresholds *placeholder*. Separate from road to allow different urgency. |

---

## 3. Missing Data Sources (Build Must Address)

**These indicators lack a real selector or are only partially exposed. Developers file BOW items if a selector is missing.**

| Indicator | Current Status | Required Selector | Notes |
|-----------|-----------------|-------------------|-------|
| **Migration Attractiveness Score** | PARTIALLY EXPOSED | `state.attractivenessScore` or derived calculation | The spec assumes `sim.store.population.attractivenessScore` exists, but code review found no live `attractivenessScore` field in SimState. Migration inflow/outflow exists (state.lastDemographics), but the *driver* (what makes citizens want to move in?) is not exposed. Likely exists as an internal calculation in the engine but not serialized. **BLOCKER for RAG:** either expose the score as a new SimState field or compute it (e.g., as a function of approval + wellbeing + unemployment + housing availability). Grounded in what real values drive it (GR#15). |
| **Crime Rate** | NOT IMPLEMENTED | `state.crimeStats.ratePerMonth` or equivalent | Crime tracking is not yet built. A skeleton for this indicator exists in the spec as a placeholder. If the RAG table includes crime, developers must build the engine model first (fire extinguishing incidents, police response time, etc.). Until then, render as "Not yet available" in the UI (GR#1 fallback rendering). |
| **Pollution / Air Quality Index** | NOT IMPLEMENTED | `state.environment.pollutionIndex` or equivalent | Environmental model (factories, vehicles, waste burning, etc.) not yet built. Placeholder in spec pending feature implementation. Render as "Planned for later release" in UI. |
| **Unemployment Rate (Direct Selector)** | PARTIALLY EXPOSED | `totalJobs(state)` and `state.population` are data; selector needs UI computation | The building system calculates total available jobs (summing spec.workers for buildings), but this is not directly exposed as `state.unemployment`. Compute it in the UI layer as `(totalJobs − population) / population`, or expose a selector in the engine. **DESIGN CHOICE for Aaron:** should unemployment be a live engine selector (like approval/wellbeing) or UI-layer derived? |
| **Build Queue Cost Total** | PARTIALLY EXPOSED | `state.buildQueue.totalCostRemaining` or scan state.buildings for in-progress | The build queue is currently implicit (tool state + buildings with builtTick constraints), not a first-class list. To display "Queue: 3 orders, 145,000 MP", the UI must either (a) expose buildQueue as a structured array in SimState, or (b) compute it by summing placementCost for buildings where !isOnline(state, b). **DESIGN CHOICE for Aaron:** structured queue or derived? |
| **Housing Cap Headroom** | EXPOSED | `onlineResidentsCapacity(state)` | Function exists in data.ts; RAG can use it directly. ✓ No missing selector. |
| **Service Queue (Pending Requests, Wait Time)** | NOT EXPOSED | `state.services.<serviceName>.pendingRequestCount` | Service request backlog tracking is not yet implemented. Spec forward-declares this tab as placeholder. Until built, render as "Coming in next increment". |
| **Attractiveness Driver Breakdown** | NOT EXPOSED | `state.attractivenessBreakdown.approval`, `state.attractivenessBreakdown.housing`, etc. | The UI has no way to show players "What's making us attractive or repulsive?" A decomposition of the attractiveness score would help, but it doesn't exist. Low priority for launch; label as "Planned feature". |

---

## 4. Key Design Decisions (Aaron to Confirm)

1. **Finance as Owner of Economy + Policies:** Tax sliders, policies, and ledger all live in Finance since they are levers on the budget. Policy toggles could also live in a separate "Politics" tab, but recommending Finance as SSOT to avoid tab proliferation.

2. **Services as Coverage Grid, Not Separate Tabs:** Rather than `Finance → Services → Fire` (each service a separate nested tab), propose a single `Services → Coverage Map` that renders a grid/tile view of all services side-by-side (Fire %, Police %, Health GP %, Health Hospital %, Water Clean %, Water Waste %, Power %, Waste %), so players see the city-wide health snapshot at once. Each tile is RAG-coloured and expandable for detail.

3. **Build & Zoning Owns Infrastructure (Lines, Specialists, Queue):** Roads, rail, pylons, landmarks all in one tab since they are player-controlled structures. This co-locates the "what we've built" view with "what we can build next".

4. **Alerts as a Filter View:** Rather than pushing every alert into separate UI components (banners + popups + modals), the Alerts tab aggregates them by severity. Banners still render in their layout regions (top-right toast, center modal, etc.) per the non-overlap spec, but the persistent tab lets players review past/current alerts on demand.

5. **Projections Deferred:** If forecast data doesn't yet exist in the engine, leave Projections as a placeholder tab (stub: "Demand and revenue forecasts coming in a future update"). Do not block launch on forecasts.

6. **Units & Metadata:** The Units tab is low-frequency reference material. Recommend either:
   - Move it to a top-level **Settings** tab (alongside units, physical entities, controls).
   - Hide it behind a "?" info button at the top of the dock.
   - Remove it from the tab bar and make it a modal-on-demand (not persistent).
   
   Aaron to choose.

7. **Debug Tab:** Remains as-is, gated behind dev builds. Visible only to developers.

8. **RAG Thresholds as Config, Not Hardcode:** All numeric thresholds (50% service coverage, 40 wellbeing, 7% unemployment, etc.) must be stored in a config object or constants file that Aaron can adjust per-balance pass, not scattered as magic numbers in React components. Example:
   ```typescript
   const RAG_THRESHOLDS = {
     servicesCoverage: { red: 0.5, amber: 0.8, green: 1.0 },
     wellbeingOverall: { red: 40, amber: 70, green: 100 },
     unemployment: { red: 0.15, amber: 0.07, green: 0 },
     budgetMonths: 3, // months of expenses = GREEN threshold for funds
   };
   ```

9. **Fallback Rendering (GR#1):** If any selector returns undefined, null, out-of-range, or is not yet exposed, render the indicator in GREY with tooltip "Data unavailable" or "Not yet available". Never blank, never crash, never guess the value. Log to console: `console.log("RAG fallback: [indicator] unavailable", { reason, selector, state })`.

---

## 5. Summary of Grounding

- **14 current RightDock tabs** mapped to 6 new top-level tabs + 1 debug tab, with nested children and forward-declared stubs.
- **18 RAG indicators** grounded in real sim store functions and selectors from data.ts/engine.ts:
  - `state.funds`, `state.lastFlows`, `wellbeingOf()`, `approvalOf()`, `serviceCoverageOf()`, `lineUsageOf()`, `onlineResidentsCapacity()`, `totalJobs()`, `state.insolvencyState`, etc.
- **5 missing selectors** identified (attractiveness score, crime rate, pollution, build queue structure, unemployment direct selector) with design choices for Aaron: expose as new fields or compute in UI layer?
- **3 forward-declared stubs** (Loans & Debt, Service Queue, Contracts, Projections) ready for future increments; render as "Coming soon" placeholders until built.
- **All thresholds are PLACEHOLDERS** pending Aaron's balance pass; config-sourced, not hardcoded.
- **Fallback rendering** ensures no crashes if a selector is missing (GR#1).

---

**Real Components/Selectors Grounded In:**

- `webconsole/src/sim/data.ts`: `serviceCoverageOf()`, `serviceDemandOf()`, `waterBalanceOf()`, `lineUsageOf()`, `onlineResidentsCapacity()`, `totalJobs()`
- `webconsole/src/sim/engine.ts`: `wellbeingOf()`, `approvalOf()`, `demandOf()`, `levelOf()`
- `webconsole/src/sim/types.ts`: `SimState` structure (funds, insolvencyState, population, lastFlows, lastDemographics, etc.)
- `webconsole/src/components/right/RightDock.tsx`: 14 current tabs (Status, Population, Rates, Units, Power, Water, Waste, Lines, Earnings, Milestones, XP, Specialists, Policy, Debug)
- `webconsole/src/components/MapView.tsx`: banners/modals (Level-up, Placement notice, Insolvency, Administration, Second Bailout, Decline, Forced Sales)

---

**Status:** Ready for Aaron row-by-row approval.  
**Next Steps:** Aaron approves/edits this table, then build begins per the accepted spec.

