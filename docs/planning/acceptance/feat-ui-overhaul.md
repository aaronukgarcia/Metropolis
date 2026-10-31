# Requirement pack: the webconsole UI overhaul (FEAT-2326609843, epic)

Standard: `docs/planning/acceptance/TEMPLATE-requirement-pack.md`. Author: lead BA pass, 2026-10-31, from Aaron's first sustained playtest, in his words: "the UI needs an overhaul its difficult to navigate ... a treenodal view would be much better ... I dont know what green means ... industry demand at -100 ... what am I supposed to build warehouses? ... the player needs support how can they get income up ... the main screen is too cluttered but all information is needed so it needs to be masked until called for ... consider toast and smooth menu hints what a user should be looking at ... we dont want to play it for the user but the user needs help maybe (helper = on or off) ... its not a clone of [Blue — GR#22 codename] but consider how its UI works ... plan this."

## 1. Context and goal

- **BOW:** FEAT-2326609843 (P1, epic). Each increment dispatches as its OWN child FEAT (one item per increment — the round-recency rule). mkey for docs commits: `[FEAT-2326609843]`.
- **Northstar stage:** dogfood. The sim works; the playtest stalled on the UI, not the model. Game-first queue priority applies.
- **What exists (verified in code, 2026-10-31 — the overhaul re-arranges, it does not invent):**
  1. **Build palette** (`components/bottom/BottomBar.tsx` BuildTab): a two-pane "tree-wrap" — left column of ~18 families grouped under domain headers (`paletteByDomain()`), right detail pane of subtypes sorted available-first (`sortPaletteItems`). Family selection fires on **hover** (`onMouseEnter`), the panes scroll independently, nothing collapses, there is no search, and the available-first sort re-orders subtypes as unlocks/funds change (spatial memory is destroyed). Aaron's road trip — scroll the family column to the bottom for Roads, then scroll the detail pane back up — is this layout working as built.
  2. **Demand** (`components/left/DemandDock.tsx`): ±100 meters. RCI rows read `demandOf` (engine.ts:791) — a ZONING meter where +100 = build more, **-100 = oversupplied** (industry at -100 means "no more industry needed", the exact opposite of Aaron's reading "demand unmet, build warehouses?"). Service rows read `serviceDemandOf` where **positive = shortfall**, and a positive value renders the `pos` (green) fill — so a service in shortfall shows a GREEN bar beside its Fix button ("demand is good, it's being met, why fix it?"). Two opposite sign conventions share one meter component and one colour code. No row shows the underlying quantities (need vs capacity).
  3. **Information docks:** LeftDock's six-group tab-tree (Finance/Services/Population/Build&Zoning/Projections/Alerts + Debug) is sound IA but everything is visible at once with the TopBar, DemandDock, NewsFeed, BottomBar and map; `DemandForecastTab`/`RevenueForecastTab` are honest stubs. `HelpOverlay`, `NewsFeed` (toasts), `upgradeToast.ts` and `demandFixUi.ts` (advisor copy + `demandFixPlan` SSOT) are the existing help/advice seams; `AUTO_BUILD_DEMAND_PERCENT`, `orderedDemandFixPlan`, `pickAutoSpec` are the existing "what should I build" model.
- **Blue reference (the reference title, GR#22 — consider, not clone):** bottom toolbar of icon categories with flyout subtype rows (two clicks, no scrolling); dedicated Info View overlays separating "modes of looking" from "always-on chrome"; RCI bars with hover detail; an advisor that pings with click-to-locate but never acts alone; milestone-gated progressive disclosure of UI itself.
- **Goal:** the same information, reorganised: a collapsible tree build browser with search; demand that says in words what to do and shows the real numbers; an optional advisor (helper ON/OFF) that surfaces the most-neglected thing with a concrete, clickable suggestion (income guidance first); a main screen that starts minimal and reveals on demand.

## 2. Increments (each becomes a child FEAT at dispatch; order is the build order)

### UX-1 — Tree-nodal build browser (replaces BuildTab's two-pane hover list)
- **AC-1.1** One scrollable tree: domain → family → subtype, expand/collapse on CLICK (hover never changes selection); expanded state survives tab switches within a session; at most one domain auto-expanded at start.
- **AC-1.2** Clicking a family shows its subtypes INLINE under it (indented), not in a separate pane — picking a road subtype never requires scrolling a second list. Sticky family header while its subtypes scroll.
- **AC-1.3** Stable subtype order within a family (spec order from data.ts); availability shown by badge/grey state, never by re-sorting (kills the sortPaletteItems reorder). Locked/placeholder/at-cap chips and the SpecCard-on-locked-click behaviour carry over unchanged.
- **AC-1.4** A search/filter box: typing filters the tree live across all levels (name + blurb), Esc clears; search never dispatches a tool change by itself.
- **AC-1.5** A "Recent" pseudo-family at the top holding the last N placed spec ids (session state, not SimState); keys 1–9 quick-pick stays.
- **AC-1.6** No regression: every spec reachable in ≤3 clicks from a cold open; placement dispatch path (`tool` action, placementGate) unchanged.

### UX-2 — Demand legibility (DemandDock + the meter language)
- **AC-2.1** One colour law everywhere: **red/amber = action needed, green = healthy**, applied by MEANING not sign — a service shortfall is red/amber, a satisfied service green; RCI "build more" pressure is its own hue with an explicit arrow/label, never the health green. The two sign conventions (zoning meter vs shortfall) are mapped to the law in ONE exported helper both the dock and tests read.
- **AC-2.2** Every row says it in words: RCI rows render a direction phrase from the value ("High demand — zone more industry", "Oversupplied — no more factories needed", "Balanced") so -100 can never read as unmet demand; service rows render "Need N, have M" from the real model quantities (the same numbers `demandFixPlan` uses, GR#3), not just the ±100 scalar.
- **AC-2.3** The meter keeps a visual bar, but with a labelled midpoint and end caps ("oversupplied ← balanced → build more") so the direction of travel is self-explanatory; hover/expand shows the model inputs (jobs vs workers for residential, counts vs base for C/I).
- **AC-2.4** "Fix (N)" renames to an action phrase naming the build ("Build 3× Clinic") sourced from the existing fix plan; the button only ever appears on action-needed (red/amber) rows.
- **AC-2.5** The DemandDock collapses to a one-line summary strip (worst item + count of others) and expands on click — its default state is collapsed when nothing is action-needed (feeds UX-4).

### UX-3 — Advisor / helper mode (helper = ON/OFF)
- **AC-3.1** A `helper` toggle in ConfigMenu (session-persisted UI pref, NOT SimState; default ON until Aaron rules otherwise — Q2). OFF = zero advisor surface (no toasts, no highlights, no nudges); the underlying panels are untouched either way.
- **AC-3.2** A neglect model, data-only, over EXISTING signals (ledger net, worst service shortfall, brownout, housing vs jobs imbalance, unemployment, line over-capacity, construction queue starvation incl. the BUG-1142 materials state when it lands): each candidate scored and the TOP one surfaced — one advisor message at a time, never a feed of ten.
- **AC-3.3** Income guidance is the first-class case: when net income is negative or the treasury trends down, the advisor names the top concrete levers from the REAL ledger and models ("Biggest cost: power upkeep £X — idle capacity Y MW", "Tax headroom: residential at R% vs baseline", "Z unemployed — more jobs raise income tax"), each with a "show me" action that opens the relevant existing tab (Finance→Ledger, TaxSettings, …). Numbers from the ledger/SSOT helpers only — no advice literal may contain a hand-typed figure.
- **AC-3.4** Delivery is a toast (reusing the NewsFeed/toast seam) plus a soft highlight on the menu/tab it points at ("smooth menu hints"); toasts rate-limited (one per topic per game month, dismiss remembered for the session); clicking navigates, dismissing never nags again until the signal materially worsens.
- **AC-3.5** The advisor NEVER dispatches a build/tax/policy action itself ("we don't want to play it for the user") — it may pre-select a palette entry or open a tab, nothing that mutates SimState.
- **AC-3.6** Determinism: the neglect model is a pure function of SimState (+ the UI pref); same state, same advice; no clock/storage reads in the scoring path (BUG-602).

### UX-4 — Progressive disclosure (declutter the main screen)
- **AC-4.1** Default view = map + TopBar + a thin status strip; DemandDock (collapsed summary per AC-2.5), NewsFeed and LeftDock start collapsed/closed; one keystroke or click re-opens each; open/closed state is a session UI pref, never SimState.
- **AC-4.2** The status strip carries ONLY: funds + net/tick, population, date/speed, the single worst alert chip, and the advisor chip when helper is ON — each chip clicks through to its full panel ("masked until called for, but all information is needed").
- **AC-4.3** Critical events (brownout, bankruptcy risk, disaster-class alerts) ALWAYS surface — an alert chip turns red and a toast fires even with everything collapsed and helper OFF (safety floor, distinct from advisor nudges).
- **AC-4.4** No information deleted: every pre-overhaul fact remains reachable in ≤2 clicks from its chip/tab; a parity checklist in the pack's test plan walks every LeftDock child tab.
- **AC-4.5** Layout holds at the dogfood window sizes (the house minimum and a laptop width) without overlap; keyboard map (existing hotkeys + new collapse keys) shown in HelpOverlay.

### UX-5 — IA polish and the tabs replan (last, smallest)
- **AC-5.1** The six-group LeftDock survives (it is good IA) but gains: per-group badge counts (alerts, action-needed), and the Projections stubs either get real content from existing models or fold behind a single "Coming" entry (no dead tabs on the default path).
- **AC-5.2** Build & Zoning's Queue/Structures/Reference children cross-link with the UX-1 tree (clicking a structure row jumps to its tree entry); SpecCard and the FEAT-2326609841 property card share the type-band styling so the UI reads as one system.
- **AC-5.3** A short "UI map" doc page (docs/) naming every surface, its purpose and its data source — the SSOT for future UI work.

## 3. Non-goals
- No sim/model change anywhere in the epic: presentation and navigation only; converge fixtures byte-identical throughout (any flows diff = design defect, stop).
- No new demand model — AC-2.2's words and quantities come from `demandOf`/`serviceDemandOf`/`demandFixPlan` as they are; retuning them is a separate (balance-regime) item.
- No tutorial/campaign system; the helper is ambient advice, not scripted onboarding.
- No TUI changes; no Go changes.
- Not a Blue clone: no radial menus, no social-feed-alike; the NewsFeed stays an event log.

## 4. Developer direction
- **Files (expected):** UX-1 rewrites `components/bottom/BottomBar.tsx` BuildTab (+styles); UX-2 reworks `components/left/DemandDock.tsx` + a new `src/sim/demandPresentation.ts` (the one colour/phrase mapping helper, exported, test-shared); UX-3 adds `src/sim/advisor.ts` (pure scoring) + `components/AdvisorToast.tsx` + ConfigMenu toggle; UX-4 touches `App.tsx`, `TopBar.tsx`, dock shells; UX-5 is LeftDock + docs. Session UI prefs live in ONE place (a `uiPrefs` context persisted to localStorage OUTSIDE render paths — reads at mount only, BUG-602).
- **Constraints:** StrictMode-safe throughout (BUG-756); TDZ rule — new sim-side modules are leaves importing FROM data.ts/engine.ts only, entry-order test for anything data.ts gains an import of; GR#3 — no literal thresholds in components (the colour law helper owns them); GR#21 — stable sorts with deterministic tiebreaks; scoped verification ONLY per lane; the bounded `--webconsole-ci` set at every port (these files sit on every fixture's render path).
- **Dispatch shape:** five child FEATs, UX-1 and UX-2 first (they answer the playtest pain directly), UX-3 next, UX-4/UX-5 after — UX-4 depends on UX-2's collapsed DemandDock and UX-3's chips. Property-card lane (FEAT-2326609841) also edits MapView/BottomBar-adjacent styling: port it BEFORE dispatching UX-1 or brief the collision explicitly.

## 5. Tester watch-fors
1. Built-but-not-wired: every AC proven in the RUNNING dev server (click paths, screenshots), not only unit renders.
2. UX-1: pick Roads→each subtype with zero list scrolling at the house minimum window; hover must never switch families.
3. UX-2: the inverted-green case specifically — a service in shortfall must be red/amber; industry -100 must read "oversupplied"; both pinned with mutation tests on the shared helper.
4. UX-3: helper OFF = zero advisor DOM; advice numbers mutation-tied to the ledger/SSOT (mutate a constant, the advice test fails).
5. UX-4: the parity checklist — every pre-overhaul fact reachable; critical alerts fire with everything collapsed.
6. Save/load and StrictMode double-mount across all new session-pref state.

## 6. Round guidance (GR#23)
Attacker ≠ author, own throwaway worktree, covering verdict after commit. Attack first: (1) the colour/phrase law — feed every sign/edge combination, hunt a state that renders green while action is needed; (2) advisor honesty — a hand-typed number or stale-threshold in advice copy; a nudge that mutates SimState; rate-limit bypass by signal flapping; (3) prefs leaking into SimState or the digest; (4) UX-1 keyboard/a11y and the ≤3-clicks claim measured mechanically; (5) perf — tree + advisor scoring on the big fixture city, no per-tick O(N) in render (memoOnState); (6) determinism under -count=3 equivalents (same state, same DOM).

## 7. Open questions for Aaron (interims let UX-1/UX-2 dispatch now)
- **Q1 — Build browser placement:** keep the tree in the bottom Tools panel (interim), or move building into a LeftDock "Build" group / a Blue-style bottom icon toolbar? Interim: same place, new tree — placement is UX-4/5 ground.
- **Q2 — Helper default:** ON or OFF for a fresh city? Interim: ON (the playtest says new players need it), toggle in Config.
- **Q3 — RCI meter survival:** keep a compact ±bar at all once words exist, or words+quantities only? Interim: keep the bar with labelled caps (AC-2.3).
- **Q4 — Advisor voice:** plain copy (interim) or game-flavoured (ties to the passport's whimsical-names ruling)?

## 8. Done means
A fresh player opens the city to a calm screen, finds any road type in three clicks without scrolling two lists, reads every demand row as a plain instruction with real numbers, is told — only while helper is ON — the single most useful thing to look at next with a working "show me", can collapse everything and still be interrupted for genuine emergencies, and no sim number changed anywhere; every AC mutation-proven, each increment carrying its own independent ACCEPT.
