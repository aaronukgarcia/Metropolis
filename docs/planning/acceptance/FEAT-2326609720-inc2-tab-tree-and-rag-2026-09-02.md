# FEAT-2326609720 inc2 — HUD Tab-Tree Replan: Grouping + RAG Thresholds

**Date:** 2026-09-02
**Status:** DRAFT — for Aaron's row-by-row sign-off before build dispatch
**Direction approved:** Q100059 = A1 — left nested tabs grouped **Finance / Services / Population /
Build & Zoning / Projections / Alerts**, each colour-coded red/amber/green from real numbers.
**Supersedes:** the wider-scope draft `FEAT-2326609720-tab-tree-and-rag-thresholds-2026-09-02.md`
(same date, written before FEAT-2326609711/BUG-526/BUG-399/BUG-524 landed fire/police/unemployment
selectors — several rows there are now stale "missing selector" claims). That draft's structural
ideas (Coverage Map as one grid, Alerts as a severity filter) are carried forward here; this doc is
the one grounded against the CURRENT tree and is the one Aaron should mark up.
**Prior art (inc1, landed):** `docs/planning/acceptance/FEAT-2326609720-hud-overlay-replan.md` +
`webconsole/src/components/overlayLayers.ts` — the z-index scale and single-blocking-overlay
invariant this increment's Alerts group must keep obeying. Inc1 explicitly left tab grouping and RAG
colours out of scope pending this sign-off (`overlayLayers.ts` lines 17-23).
**Grounded in:** `webconsole/src/components/right/RightDock.tsx` (14 tabs), `left/LeftDock.tsx` (4
tabs), `left/DemandDock.tsx`, `TopBar.tsx`, `bottom/BottomBar.tsx`, `sim/data.ts`, `sim/engine.ts`,
`sim/fiscal.ts`, `sim/types.ts`.

---

## 0. Inventory — every current info element and where it lives today

**RightDock.tsx (`Information` panel, 14 tabs):**

| Tab id | Elements shown |
|---|---|
| `status` | Approval tile, Wellbeing tile, Citizens tile, Housing cap tile (+building/+disconnected sub-labels), Wellbeing breakdown (11 parts bar-list), Structures table (count/upkeep by family), fiscal-state hint (solvent/deficit + net/tick) |
| `population` | Births/Move-ins/Deaths/Move-outs tiles (last tick), Demographic flow Sankey (`PopulationSankey`), Arrivals-by-mode Sankey |
| `rates` | 3 tax sliders (residential/commercial/industrial) + live yield per rate + avg-tax warning |
| `units` | Unit registry table, Physical entities (metres) table |
| `power` | Capacity/Need/Imported-MW tiles, "Use external power cover" toggle, shortfall/import-cost hint |
| `water` | Clean/Discharge capacity tiles, Clean/Waste demand tiles, headroom hint, leak warning, Plants & pipes table (grid, pipe tier, served, pipe-use %, upgrade button) |
| `waste` | Generated/Collection-cap tiles, Collection coverage bar, Diversion-rate bar, Processing-mix table, Recovered (EfW power, material revenue) tiles |
| `lines` | Per-line saturation bars (road/M20/rail/HS1), headroom/over-capacity tooltip |
| `earnings` | Income-by-source table (Residential/Commercial/Offices/Industrial/Tourism + conditional Grid Import row), Total in/out, Margin % |
| `milestones` | Milestone list with met/open chips |
| `xp` | City level badge, XP bar, Unlock ladder table (levels 1-20) |
| `specialists` | Landmark/university list with locked/built chips |
| `policy` | Policy toggle list (recycling, transit subsidy, tourism drive, austerity) |
| `debug` | debug.json frame viewer, Commit/Download/Refresh buttons, Errors-captured list, dev cheat buttons (DEV-gated) |

**LeftDock.tsx (`Fiscal` panel, 4 tabs):**

| Tab id | Elements shown |
|---|---|
| `overview` | Treasury/Net-per-tick/Income/Expense/Loan-owed tiles, Take-loan/Repay-loan button, margin hint |
| `flow` | Sankey (fiscal flow), Inflows/Outflows table with per-stream % share |
| `ledger` | Chronological event table (tick/event/amount) |
| `trend` | Funds histogram (`state.history`), 72-tick trend summary (avg net/tick, funds ×72t, pop ×72t) |

**DemandDock.tsx (`Demand` panel, no tabs — always visible):** Brownout banner, Housing/Shops/
Industry demand meters, per-service demand meters (`serviceDemandOf` — nursery/primary/college/gp/
hosp/police/fire/cleanwater/waste/power) each with an optional "Fix (N)" one-click button, Auto-build
button.

**TopBar.tsx (always visible, no tabs):** Treasury + trend arrows, Population + trend arrows,
Wellbeing dot + score + mini-bar, game date, "SIMULATION ENDED" badge (decline), Level + XP mini-bar,
speed control (Pause/Play/Fast/Turbo).

**BottomBar.tsx (`Tools` panel, Build/Move tabs):** structure palette, tool modes (Select/Move/
Bulldoze/Clone). **Out of scope for this replan** — it is the build TOOL, not an information surface;
inc2 does not touch it, noted so nothing is silently dropped.

**Transient/overlay elements (MapView.tsx + overlayLayers.ts, not currently tabbed):** Level-up
banner, Place-notice banner (cannot-afford), Insolvency banner (warning/crisis/administration/
second-bailout), Insolvency popup (one-shot bailout entry), Forced-asset-sales panel, Decline-screen
overlay, Decline-reopen chip, Rebuild prompt, Stale-build banner (currently unmounted, BUG-564),
Playmode banner.

---

## 1. Tab-tree grouping: element → current location → new group → notes

| # | Element | Current location | → New top-level | → New nested tab | Notes |
|---|---|---|---|---|---|
| 1 | Treasury, Net/tick, Income, Expense, Loan owed tiles + loan/repay button | LeftDock `overview` | **Finance** | Overview | Direct move; stays the landing tab for Finance |
| 2 | Fiscal Sankey + inflow/outflow table | LeftDock `flow` | **Finance** | Flow | Direct move |
| 3 | Ledger event table | LeftDock `ledger` | **Finance** | Ledger | Direct move |
| 4 | Funds histogram + 72-tick trend summary | LeftDock `trend` | **Finance** | Trend | Direct move |
| 5 | Tax sliders + yields + avg-tax warning | RightDock `rates` | **Finance** | Tax Settings | Relocate right→left |
| 6 | Income-by-source table + Grid Import row + Total/Margin | RightDock `earnings` | **Finance** | Earnings | Relocate right→left |
| 7 | Policy toggles (recycling/transit/tourism/austerity) | RightDock `policy` | **Finance** | Policies | Relocate right→left; these are budget levers, kept with the money they move (transit subsidy and austerity both alter `approvalOf`/spend, tourism drive gates the Earnings row) |
| 8 | Approval tile, Wellbeing tile, Wellbeing breakdown (11 parts) | RightDock `status` | **Population** | Wellbeing | Split out of Status — wellbeing is a population-health readout, not a service-coverage one |
| 9 | Citizens tile, Housing cap tile (+building/+disconnected) | RightDock `status` | **Population** | Housing | Split out of Status |
| 10 | Structures table (count/upkeep by family) + fiscal hint | RightDock `status` | **Build & Zoning** | Structures | The remaining Status content is "what's built", which belongs with Build & Zoning, not Finance/Population |
| 11 | Births/Move-ins/Deaths/Move-outs tiles + both Sankeys | RightDock `population` | **Population** | Demographics | Direct move |
| 12 | Employment (jobs vs workers, `unemploymentOf`) | *not currently tabbed — derived in `wellbeingOf`'s "Jobs/Employment" part and `totalJobs`/`unemploymentOf`* | **Population** | Employment | **NEW tab** — the selector already exists (`data.ts totalJobs()`, `unemploymentOf()`), only the UI surface is new |
| 13 | Power capacity/need/import tiles + external-cover toggle | RightDock `power` | **Services** | Power | Relocate right→left |
| 14 | Water clean/discharge tiles + demand + leak warning + plants/pipes table | RightDock `water` | **Services** | Water | Relocate right→left |
| 15 | Waste generated/collection/diversion/processing-mix/recovered | RightDock `waste` | **Services** | Waste & Recycling | Relocate right→left |
| 16 | Fire/Police/GP/Hospital/School coverage (currently only in DemandDock meters + wellbeing parts, no dedicated readout) | *not currently tabbed — `serviceCoverageOf()` rows `fire`/`police`/`gp`/`hosp`/`nursery`/`primary`/`college`* | **Services** | Coverage Map | **NEW tab** — one grid of coverage tiles (need/cap/coverage %) for every `serviceCoverageOf()` row not already given its own tab (Power/Water are big enough to keep their own tabs; Fire/Police/GP/Hospital/Education share this grid) |
| 17 | Line saturation bars (road/M20/rail/HS1) | RightDock `lines` | **Build & Zoning** | Lines & Networks | Relocate right→left; grouped with infrastructure the player builds, not "Services" (roads/rail are not a demand-meter service) |
| 18 | Unlock ladder, City level, XP bar | RightDock `xp` | **Build & Zoning** | Unlocks | Relocate right→left; ladder is about what can be built |
| 19 | Landmark/specialist list | RightDock `specialists` | **Build & Zoning** | Specialists | Relocate right→left |
| 20 | Milestone list | RightDock `milestones` | **Projections** | Milestones | See open question 1 — milestones are forward-looking targets, fits Projections better than Alerts |
| 21 | Unit registry + physical-entity metadata | RightDock `units` | **Build & Zoning** | Reference | Relocate right→left as a low-frequency reference sub-tab; NOT worth its own top-level slot |
| 22 | debug.json viewer, commit/download/refresh, error list, dev cheats | RightDock `debug` | **Debug** (unchanged, dev-gated, outside the 6-group tree) | — | Stays exactly as-is; Debug is not one of the 6 approved groups and Aaron's Q100059 answer did not ask it to move |
| 23 | Housing/Shops/Industry + per-service demand meters + Fix buttons + brownout banner | DemandDock (its own always-visible panel, unchanged column) | **unchanged — stays its own docked panel** | — | DemandDock is a live action surface (Fix buttons dispatch placements), not a passive info readout; folding it into the six groups would bury a one-click affordance behind tab clicks. Recommend leaving it as-is; flagged as open question 2 |
| 24 | Treasury/Population/Wellbeing/Date/Level readouts | TopBar (always visible header) | **unchanged — stays the header** | — | Same reasoning: always-visible glance strip, not a tabbed dock |
| 25 | Level-up / Place-notice / Insolvency / Second-bailout / Decline / Forced-asset-sales / Rebuild-prompt / Playmode banners | MapView overlays (`overlayLayers.ts` Z_INDEX + `BLOCKING_OVERLAY_*`) | **Alerts** | Critical / Warning / Info (severity split) | The banners themselves keep rendering in place per the inc1 non-overlap contract — Alerts is a **persistent log/review surface** of the same events, not a replacement renderer. Building it means tapping the SAME state each banner already reads (`insolvencyState`, `declineState`, `notice`, bailout state), not inventing a new one |
| 26 | Revenue/demand forecast | *does not exist yet* | **Projections** | Demand / Revenue | Forward-declared **stub only** — no engine forecast model exists; render "coming in a future increment", do not block inc2 on building one |
| 27 | Migration attractiveness score | *not exposed — `attractiveness` is a local variable inside `engine.ts`'s move-in calculation (~line 1158), never returned on `SimState`* | **Population** | Migration | **BLOCKED pending a selector.** inc2 can ship Migration with births/moveIns/deaths/moveOuts (already exposed via `state.lastDemographics` and `state.demographicHistory`) and mark the attractiveness figure "not yet available" (GR#1 fallback) rather than inventing a number |

**Six-group tab tree (nested, left dock):**

```
FINANCE        → Overview, Flow, Ledger, Trend, Tax Settings, Earnings, Policies
SERVICES       → Power, Water, Waste & Recycling, Coverage Map (fire/police/GP/hospital/education)
POPULATION     → Wellbeing, Housing, Demographics, Employment, Migration (partial — see #27)
BUILD & ZONING → Structures, Lines & Networks, Unlocks, Specialists, Reference (units)
PROJECTIONS    → Milestones, Demand (stub), Revenue (stub)
ALERTS         → Critical, Warning, Info
DEBUG          → unchanged, dev-gated, outside the 6-group tree (Aaron's approval only covered the 6)
```

DemandDock and TopBar are **not folded into the tree** — see open questions.

---

## 2. RAG threshold table

Every row is grounded in a real function/field that exists **today** in `sim/data.ts`, `sim/engine.ts`
or `sim/fiscal.ts` — no invented selectors except where marked NEW (the value exists, only the UI
surface doesn't) or STUB (neither exists yet). All numeric bounds are **PLACEHOLDER**, proposed as
sensible defaults for Aaron's row-by-row sign-off, per the project's balance-number regime. Where an
existing convention already fixes a number (TopBar's wellbeing dot), that convention is reused rather
than re-invented.

| # | Metric | Source (function/field) | GREEN | AMBER | RED | Rationale |
|---|---|---|---|---|---|---|
| 1 | Wellbeing overall | `wellbeingOf(state).overall` (0-100) | ≥ 70 | 45-69 | < 45 | **Reuses the exact TopBar convention** already shipped (`TopBar.tsx` line 30: `wb.overall >= 70 ? done : wb.overall >= 45 ? warn : danger`) — do not invent a second number for the same metric |
| 2 | Wellbeing part (any of the 11: Approval, Parks, Healthcare, Hospital, Education, Safety, Fire safety, Jobs/Employment, Utilities, Sewage, Refuse) | `wellbeingOf(state).parts[i].value` (0-100) | ≥ 70 | 45-69 | < 45 | **Reuses the RightDock Status-tab convention already shipped** (`RightDock.tsx` line 142: identical 70/45 split per-part) |
| 3 | Approval rating | `approvalOf(state)` (0-100) | ≥ 55 | 40-54 | < 40 | RightDock's Status tab already colours the Approval tile pos/neg at the 55 line (line 101); AMBER band added beneath it since the tile today is binary — 40 chosen as a below-baseline floor (avgTax=0 gives 62, so 40 means real erosion has happened) |
| 4 | Service coverage — Fire / Police / GP / Hospital / Nursery / Primary / College / Clean water / Sewage (the `serviceCoverageOf()` rows sharing the `cap/need` shape) | `serviceCoverageOf(state).find(r => r.id === X).coverage` (ratio, 1.0 = exactly met) | ≥ 1.0 | 0.8 – 0.99 | < 0.8 | Mirrors the Water tab's own leak convention (`waterBalanceOf` flags `leak` at ratio < 0.8, line 707) — reusing the same 80% line keeps one number across the whole Coverage Map instead of one per service |
| 5 | Power coverage | `powerStats(state)` → `cap/need`, and `isBrownoutActive(state)` | cap ≥ need (no shortfall) | shortfall covered by Grid Import (`isBrownoutActive` false, import cost showing) | `isBrownoutActive(state)` true (uncovered brownout) | Power is not a plain ratio like other services — Aaron's 2026-09-01 ruling (data.ts ~2045) makes a *covered* shortfall a price premium, not a brownout, so RED must track the toggle-aware predicate, not raw `cap<need`, or a paying city would wrongly show RED |
| 6 | Unemployment rate | `unemploymentOf(state)` (0..1) | < 0.07 | 0.07 – 0.15 | > 0.15 | No existing UI convention for this metric (BUG-524 landed the selector only; no tab shows it yet) — 7%/15% chosen as directional real-world-ish bands, explicitly placeholder |
| 7 | Waste collection coverage | `wasteDisplayModel(state)` → `hasUncollected` / `coverage` | 100% collected | n/a — this metric is binary today | < 100% (`hasUncollected` true) | The underlying model (`WasteTab`, RightDock.tsx line 485) is currently 2-state (green/red), not 3-band; propose adding an AMBER band at ≥ 95% collected ("nearly there") if Aaron wants three bands, otherwise keep it binary and treat AMBER as unused for this row |
| 8 | Line saturation (road/M20/rail/HS1) | `lineUsageOf(state)[i].saturation` (0..1) + `overCapacity` | < 0.8 | 0.8 – 1.0 | `overCapacity` true (usage > capacity) | Reuses the Water-leak 80% line for consistency across all "headroom" style meters (see #4's rationale); `overCapacity` is already a computed boolean (data.ts line 1911), so RED is exact, not a guessed cutoff |
| 9 | Housing capacity headroom | `onlineResidentsCapacity(state)` vs `state.population` | ≥ 20% headroom | 5-20% headroom | < 5% headroom (at/over cap) | No existing convention; placeholder bands matching the "comfortable / tight / at cap" framing already used in RightDock's Housing-cap sub-labels (building/disconnected) |
| 10 | Fiscal net/tick | `state.lastFlows.inflows.sum() - outflows.sum()` | ≥ 0 | n/a (see rationale) | < 0 | The current LeftDock Overview tile is already binary pos/neg (`net >= 0 ? pos : neg`, LeftDock.tsx line 39) — propose adding an AMBER "shrinking but still positive" band only if Aaron wants a 3-state Overview tile; default recommendation is to KEEP IT BINARY and let the Insolvency state machine (#11) carry the graduated risk signal instead of duplicating it here |
| 11 | Insolvency band | `state.insolvencyState` (enum) | `'solvent'` | `'warning'` | `'crisis'` / `'administration'` / `'bailout_second'` / `'decline'` | Direct state-machine mapping, no numeric threshold needed — bands are already fixed by `fiscal.ts`'s `insolvencyStateForFunds` (warning at funds ≤ −£750,000, crisis at funds ≤ −£1,500,000, both derived from `STARTING_TREASURY`) |
| 12 | Milestones progress | `MILESTONES[i].test(state)` (boolean per milestone) | all met | some met, none overdue (no "overdue" concept exists) | n/a today | Milestones are binary met/open per item today; a true RAG would need a per-milestone due-date/decay concept that doesn't exist — recommend Projections shows the met/open list as-is (GREEN dot when met, grey/open otherwise) rather than inventing an AMBER/RED state with no backing data |
| 13 | Build queue affordability | *no `buildQueue` structure exists — under-construction buildings are inferred by scanning `state.buildings` for not-yet-online entries* | n/a | n/a | n/a | **STUB** — same gap the prior draft found; do not build this RAG row until a real build-queue selector exists. Note in the tree as "coming soon", no colour |
| 14 | Crime rate | *not modelled anywhere in `sim/`* | n/a | n/a | n/a | **STUB** — no engine model. Render grey "not yet available" per GR#1's fallback-rendering rule, never fabricate a number |
| 15 | Migration attractiveness | *`attractiveness` is a local variable in `engine.ts`'s move-in calc (~line 1158), never returned on `SimState`* | n/a | n/a | n/a | **BLOCKED** (see grouping table #27) — needs an engine change (expose the value on `SimState` or a new pure selector) before any RAG colour can be assigned; file as a dependency, do not guess a formula in the UI layer |

---

## 3. Acceptance criteria (inc2 build increment)

**Structure**

- **AC-1** The left dock (`LeftDock.tsx`, retitled from "Fiscal") renders exactly the six top-level
  groups in this order: Finance, Services, Population, Build & Zoning, Projections, Alerts — each a
  first-level tab whose selection reveals a second row of nested child tabs per §1's tree. Debug stays
  a separate, dev-gated tab outside the six-group selector (it is not one of Aaron's six).
- **AC-2** Every element listed in §1's grouping table (rows 1-22, 25-26) renders inside its assigned
  new tab and **nowhere else** — the corresponding old RightDock tab (`rates`, `earnings`, `policy`,
  `power`, `water`, `waste`, `lines`, `xp`, `specialists`, `milestones`, `units`) is removed from
  RightDock once its content has a new home, and the old `status`/`population` tabs are removed once
  their pieces relocate to Population/Build & Zoning per rows 8-10.
- **AC-3** RightDock itself is retired as a docked panel once every row it carried has a destination in
  §1 (Debug is the sole content that may remain right-docked, or move left as its own tab — Aaron's
  call, see open question 3); a regression test asserts RightDock's tab list no longer contains any of
  the eleven relocated ids.
- **AC-4** DemandDock and TopBar are **unchanged** by this increment (rows 23-24) — a regression test
  snapshotting their current tab/element list must still pass untouched, proving the replan did not
  silently absorb the live-action Fix-button surface into a passive tab.
- **AC-5** The two NEW tabs introduced without existing UI (Employment, Coverage Map — grouping rows
  12/16) render real, non-placeholder numbers on first landing: Employment shows `totalJobs(state)`
  vs. working-age population and `unemploymentOf(state)`; Coverage Map shows a tile per
  `serviceCoverageOf(state)` row not already owned by Power/Water (fire, police, gp, hosp, nursery,
  primary, college), each tile showing need/cap/coverage%.
- **AC-6** Migration (row 27) and the two Projections stub children (Demand/Revenue forecast, row 26)
  render an explicit "not yet available" state (GR#1 fallback — grey, not blank, not crashing, not a
  fabricated number) rather than a guessed figure; a test asserts the fallback string is present and
  no numeric RAG colour is applied to these rows.

**RAG colouring**

- **AC-7** Every RAG-eligible metric in §2's table (rows 1-11) is coloured from the exact
  function/field cited in that row's "Source" column — never a locally re-derived copy (GR#3 SSOT). A
  regression test constructs states straddling each threshold boundary (e.g. wellbeing 69/70, coverage
  0.79/0.80, funds at exactly `INSOLVENCY_WARNING_THRESHOLD`) and asserts the rendered colour class
  flips at the documented boundary, not one tick early or late.
- **AC-8** The RAG thresholds live in ONE named constants object (mirroring the draft's
  `RAG_THRESHOLDS` proposal), not scattered magic numbers per component, so Aaron's eventual balance
  pass is a single-file edit. Existing conventions that are being REUSED (wellbeing 70/45, water-leak
  0.8) import the SAME constant the pre-existing component uses rather than duplicating the literal —
  a test greps for the shared constant name at both call sites.
- **AC-9** Power's RAG state (row 5) is computed via `isBrownoutActive(state)`, never a raw
  `cap < need` comparison — a regression test proves a covered shortfall (Grid Import ON, price
  premium showing) renders AMBER/GREEN, never RED, matching the inc1 ruling this increment must not
  regress.
- **AC-10** STUB rows (§2 rows 13-15) render no colour at all (not a default grey-as-RAG-state) and
  carry a distinguishable "coming soon" / "not yet available" marker so a player can never mistake an
  absent metric for a real GREEN.

**Non-overlap (inc1 carry-forward)**

- **AC-11** The new left-dock tab tree draws entirely within the existing `LEFT_TAB_COLUMN` z-index
  budget from `overlayLayers.ts` — no new magic z-index is introduced by this increment; if a new
  overlay-worthy element is needed (e.g. a Coverage Map tooltip), it is added to `Z_INDEX` in
  `overlayLayers.ts`, never hand-rolled in a component's inline style or stylesheet rule
  (`overlayLayers.ts` line 7-8's rule).
- **AC-12** None of the six new top-level tabs, nor Alerts' severity sub-tabs, may render as a second
  simultaneous blocking overlay alongside an active `BLOCKING_OVERLAY_ID` candidate (Rebuild Prompt /
  Decline Screen / Insolvency Popup / Forced Asset Sales) — the existing
  `resolveBlockingOverlay`/`BLOCKING_OVERLAY_PRIORITY` single-winner invariant from inc1 must still
  hold; the existing `hud-overlay-discipline.test.tsx` suite passes unmodified plus new cases for the
  Alerts tab specifically (since Alerts is the one new tree branch whose whole purpose is showing the
  same events the blocking overlays show).
- **AC-13** Existing regression tests asserting literal z-index numbers via regex
  (`bug500-advisor-click-overlap.test.tsx` line 85, `mount.test.tsx`'s "BUG-497 (3)" case) continue to
  pass unmodified — this increment must not renumber the inc1-ratified v1 Z_INDEX scale.

**Determinism / GR#21**

- **AC-14** Every new derived value feeding a RAG colour (Employment, Coverage Map tiles) is a pure,
  order-independent fold over `state.buildings` with no `Date.now`/`Math.random`/map-iteration-order
  dependence — same acceptance bar `serviceCoverageOf`/`totalJobs` already meet; a determinism test
  (two identical states → identical tile colours) is added for both new tabs.

---

## 4. Open questions for Aaron

1. **Milestones — Projections or Alerts?** §1 row 20 proposes Projections (forward-looking targets
   fit better than a severity log), but the prior draft proposed Alerts. **Recommendation: Projections**
   — a met/open milestone is not a "thing needs your attention now" signal the way a brownout or
   insolvency banner is.
2. **DemandDock's fate.** It currently sits as its own always-visible docked panel with live
   "Fix (N)" buttons. Folding it into Services (its meters are 1:1 with `serviceDemandOf()`) would put
   the coverage numbers and the one-click fix in the same place, but would hide the fix buttons behind
   a tab click instead of always-visible. **Recommendation: leave DemandDock exactly where it is**,
   unchanged by this increment (AC-4) — the tab-tree replan is for passive information docks, not the
   live action strip. Revisit in a later increment if Aaron wants it merged.
3. **Where does Debug live once RightDock is retired?** Options: (a) its own persistent left-dock tab
   outside the six groups (current recommendation, matches "stays exactly as-is" in §1 row 22), (b) a
   modal reachable from a settings/"?" affordance, (c) keep a minimal right-side panel alive for Debug
   only. **Recommendation: (a)** — simplest migration, no behaviour change to the Debug tab's own
   logic, and it is already dev-gated so its visual placement is low-stakes.
4. **Units/Reference tab's placement.** §1 row 21 puts it under Build & Zoning as "Reference". The
   prior draft floated a top-level Settings tab or a "?" modal instead. **Recommendation: keep it a
   low-priority child tab under Build & Zoning** (its content — unit registry, physical entity sizes —
   is closest in spirit to "things about what you build") rather than adding a 7th top-level group for
   one reference table.
5. **Three-band vs binary metrics (rows 7 and 10 in §2).** Waste collection and fiscal net/tick are
   binary today (green/red only, no existing amber convention). Does Aaron want a genuine AMBER band
   added for inc2 (e.g. waste ≥95% collected = amber), or should these two stay binary and rely on the
   Insolvency state machine / other rows to carry the graduated signal? **Recommendation: stay binary**
   for inc2 — inventing two more balance numbers so soon after BUG-524/526 landed risks more
   placeholder churn than the improved granularity is worth; revisit in the balance pass.
