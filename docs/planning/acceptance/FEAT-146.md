BOW code: FEAT-146

# Acceptance criteria — FEAT-146 (Early Game 'Hamlet Bootstrap' Flow & 'Why Did My City Die?' Diagnostic Autopsy Tool)

**BOW code:** FEAT-146
**Mkey:** `feat.hamletbootstrap` (PROPOSED — NOT registered in code.json; see GR#25 Escalations below)
**Spec refs:** FEAT-146's own Desc (concept document: Hamlet Bootstrap hours 0-2, growth sigmoid, player archetypes, failure autopsy). Master plan anchors: §8 (Attract & Growth), §6 (Finance & Insolvency), §11 (citizen wellbeing). Existing machinery this item depends on: `int.protocol` (engine state view subscriptions for insolvency/decline data — REGISTERED), TypeScript sim's own unlock/level mechanics (`specUnlocked(s, sp)` function in `webconsole/src/sim/engine.ts` already gates specs by `sp.unlock <= levelOf(s.xp)` — no new engine edge needed per GR#20).
**Date:** 2026-09-05 (BA revised per GR#20 architecture correction)
**Status:** Criteria-first draft — pending (a) Aaron's tier-reconciliation ruling (100/500/5k spec vs 13-tier ladder), (b) composition root wiring for autopsy state capture + new autopsyEvents field on SimState.
**Surface:** `webconsole/src/sim/` — TypeScript TS sim only. The hamlet bootstrap reframes unlock gates around the EXISTING XP-based level system (NOT a new ui.screen.map→engine.unlocks edge; GR#20 forbids UI→engine imports). The autopsy reads pre-captured debug state (types.ts SimState fields including NEW autopsyEvents field) and renders diagnostics. No Go engine changes in this build; the autopsy runs on TS state snapshots (GR#27: capture-before-wipe holds the pre-decline state).
**Package under test:** `webconsole/src/sim/engine.ts` (autopsy event ledger, tier-unlock notifications via XP), `webconsole/src/sim/types.ts` (SimState.autopsyEvents — NEW FIELD), `webconsole/src/sim/data.ts` (event threshold logic), `webconsole/src/components/` (autopsy render).

---

## User stories

- **US-1.** As a cold player in their first 30 minutes, I need the UI to start minimal (only road, basic residential, power, water zones; basic utilities) so I am not paralysed by 47 unlocked building types and 30 tax policy levers.
- **US-2.** As my city grows past its first 100 residents and the free auto-import budget drains (Month 2-3), I need a tooltip/notification linking me to the next UI unlocks ("commercial zones + shops", "food production + freight hub") so the tutorial is organic, not external.
- **US-3.** As a player who just watched my city spiral into decline and hit insolvency, I need an autopsy screen showing the causal chain — which CAPEX decision, which budget cut, which population exodus — so I understand what broke instead of feeling the game is rigged.
- **US-4.** As a save file, I need the autopsy data (and pre-wipe debug snapshot) persisted before the "Game Over" screen appears so a player can always revisit what went wrong, even if they immediately click "Start Over" (GR#27).

---

## Design constraints

1. **No new Go engine changes for this build** — all hamlet bootstrap and autopsy logic lives in the TypeScript sim (webconsole/src/sim/). The Go engine's `engine.attract` migration, `engine.spiral` S-score, and `engine.finance` insolvency paths are consumed read-only via existing delta feeds.
2. **Tier gates are UI-only** — a building that is "locked" due to tier is not actually unavailable in the engine; it is merely hidden from the player's build menu. The lock is enforced at placement time (MapView.tsx place action), not at engine level.
3. **Autopsy runs on captured state** — GR#27 mandate: a pre-wipe debug capture (full SimState JSON) is written before any wipe/reset, and the autopsy screen renders diagnostics from that capture, never from the live (wiped) state. The autopsy is a read-only forensics tool.
4. **Balance is directional, not pinned** — per Aaron's balance regime, tier thresholds (100/500/etc population) and event thresholds (CAPEX >1M credits, budget cut >25%) are placeholders. AC names the test shape; no AC locks a number.

---

## Acceptance criteria

### A. Hamlet Bootstrap — Progressive UI Un-hiding (Tier Gates)

**AC-1 (US-1; the cold start is minimal UI, entry-level specs only).** A fresh city starts at XP level 0 (no unlocks yet) or low XP. The UI surfaces available at level 0 are: the Road tool, three Zoning types (Residential, Commercial, Industrial in their most basic form), one Power spec (e.g. Diesel Generator), one Water spec (e.g. Water Pump), and a Finance panel showing the starting balance. All other building specs, policies, and screens are hidden from the menu/toolbar. The existing `specUnlocked(state, spec)` function in `engine.ts` already gates specs via `sp.unlock <= levelOf(state.xp)` — this AC reuses that gate, not a new `bootstrapTier` field (no GR#20 edge required). The placement menu calls `specUnlocked()` to determine visibility. Test: `webconsole/test/hamlet-bootstrap-tier0-visibility.test.ts` — a fresh city state with `levelOf(state.xp) = 0` renders only specs with `sp.unlock <= 0`; a `place` action attempting a spec requiring level 5+ fails with "locked" feedback (reusing existing placement gates); assert `specUnlocked(state, airportSpec)` explicitly returns false. **False-pass risk:** a city at any level with all buildings visible — the test must assert specUnlocked gate is consulted before every place attempt.

**AC-2 (US-1; reaching XP level N grants access to next tier of buildings).** As the city grows and accumulates XP, `levelOf(state.xp)` increases. When level crosses a defined threshold (AARON DECISION: the spec says thresholds at XP → level 1, 2, 3; the master plan's 13-tier ladder says level boundaries at pop [100, 500, 5k, 20k, 250k]; how do XP levels map to this?), a new set of specs becomes visible via `specUnlocked()`. Example: at level 0, only specs with `sp.unlock <= 0` are visible; crossing to level 1 unlocks specs with `sp.unlock <= 1` (e.g. low-density commercial, basic school). A notification fires marking this crossing (AC-4). Test: `webconsole/test/hamlet-bootstrap-tier-crossings.test.ts` — drive population/income to build up XP; at each level crossing, assert `levelOf(state.xp)` increments; assert affected specs (e.g. schools with `sp.unlock = 5`) switch from `specUnlocked(state, spec) = false` to `true`; assert the existing specUnlocked gate is the SSOT (not a duplicate tier check). **False-pass risk:** a separate tier field that overrides specUnlocked, or manually setting level in a dispatch — the test must assert level is derived deterministically from XP, not manually set.

**AC-3 (US-1; mid-game level unlocks farming + freight).** At a higher level (AARON DECISION: which level and corresponding population? spec says 500 pop = tier 2; plan says 5k = SmallTown = level X; reconcile?), specs like farms, freight hubs, and medium-density residential become visible (their `sp.unlock` value is set to this level in data.ts). Test: same shape as AC-2, next level boundary; assert that at this level, `specUnlocked(state, farmSpec)` and `specUnlocked(state, freightHubSpec)` both return true. **AARON DECISION:** reconcile the tier-level mapping: spec says tier boundaries at pop [100, 500, 2000]; plan says [100, 500, 5k, 20k, 250k]. How do these XP levels map? One is placeholder authority.

**AC-4 (US-2; organic tutorial: level promotion + notification).** The moment a city's XP level increases, a notification displays on the UI (mirror the existing LevelUpNotice pattern in types.ts): `"New level reached! Unlocked: Commercial zones, schools, police. Explore the Build menu."` The notification is not a modal; it lands in the news feed / ticker and can be dismissed. Each level's notification names the newly-unlocked spec families. Test: `webconsole/test/hamlet-bootstrap-level-notifications.test.ts` — drive population to reach a new level threshold; assert `notice` field on state is set (reusing LevelUpNotice if possible, or a LevelUnlockNotice if required); assert the UI renders it; dismiss it and verify it clears. **Design note:** notifications are state-journaled (types.ts), so replay determinism is preserved — same save, same ticks = same notifications.

**AC-5 (US-2; "Import Surcharges" warning at mid-game).** Once a city reaches a defined mid-game level (AARON DECISION: which level and corresponding population? ~500?), an ongoing notification appears if the city imports significant food: `"Food imports are draining your treasury (~£X/month). Build local farms and a freight hub to self-supply."` This warning is TS-side only, reading from the existing `lastFlows` array in SimState (specifically the food-import outflow item if present) — no new Go edge needed. The warning clears once (a) the player builds at least one farm OR (b) the monthly import outflow drops below a threshold (AARON DECISION: absolute cost or % of income?). Test: `webconsole/test/hamlet-bootstrap-import-warning.test.ts` — a mid-level city without farms has `lastFlows` showing a food-import cost >threshold; assert warning renders; place a farm, re-derive flows (next tick), assert warning clears. **Design note:** this is the organic hook that teaches the "import surcharge" mechanic without requiring the player to read external docs. The warning logic is pure TS state + flows, not dependent on Go-side engine.projections (if that module exists, it may later provide additional predictive data, but this AC does not require it).

### B. Hamlet Bootstrap — Testing the Loop is Alive (Directional Mechanics, Not Pinned Numbers)

**AC-6 (Loop closure: citizens consume, money moves, migration responds).** In a tier-0/tier-1 fresh city with just roads, basic residential, and a power plant, advance 20 game-ticks and observe: (1) population increases (citizens migrate in due to housing + power), (2) funds decrease (water/power/basic services OPEX is deducted), (3) a small positive cash inflow appears (Council Tax from residents). This is directional — the exact amounts are placeholders pending Aaron's balance pass, but the SHAPE (inflow > 0, outflow > 0, net closing each tick, population trend up if inflow > outflow + consumption) must hold. Test: `webconsole/test/hamlet-bootstrap-loop-alive.test.ts` — fresh city state advanced 20 ticks; extract history[0..19]; assert population trend is upward (or flat if supply limits cap); assert funds delta is negative (early cities burn cash on OPEX); assert lastFlows has council-tax inflow item; assert conservation invariant holds: `fundsAtTickEnd === fundsAtTickStart + sum(inflows) - sum(outflows)` for every tick. **False-pass risk:** a city that populates but never consumes anything (broken consumption loop) — the test must assert outflows include at minimum one consumption category (water, power, food) proportional to population.

**AC-7 (Attraction + Spiral mechanics are live, not stubbed).** The attractiveness score (engine.attract AttractAPI) and death spiral score (engine.spiral S-score, if available) both drive observed population trends. A city with high crime / low wellbeing (high S spiral score) sees population DECLINE even with housing available (outmigration rate increases). A city with good services and no pollution sees population INCREASE more aggressively. Test: `webconsole/test/hamlet-bootstrap-attract-spiral-linked.test.ts` — create two cities in identical terrain; City A: build schools + police + parks (good services); City B: same residential but no services (low attraction, rising spiral). Advance both 30 ticks; assert City A population > City B population by a directional margin (not pinned: "City A has +30-100 more residents", not "exactly +47"). **Design note:** this AC does NOT test the sigmoid migration curve (AC-8 in concept doc, FEAT-142 cross-coordinate) — it only asserts that engine.attract and engine.spiral are wired and observably affect population.

### C. Autopsy — Diagnostic Timeline at Decline

**AC-8 (US-3; autopsy screen exists and is reachable from game-over state).** When `declineState` is set (hard game-over, per types.ts), the next UI render shows a modal/screen titled "City Autopsy" or "Post-Mortem Report" instead of the standard game-over screen. The screen has: (1) a scrollable event timeline (newest-first), (2) a summary row showing peak population, final population, lowest funds, and total spending (read from `declineState` fields: peakPopulation, finalPopulation, minFundsEver, totalSpending). Test: `webconsole/test/hamlet-autopsy-screen.test.ts` — dispatch decline-triggering actions (drive funds to crisis, trigger bailout→decline flow), assert `declineState` is set on state, render the UI, assert the autopsy modal is visible and displays the four summary stats from declineState (no new state fields needed; reuse existing). **False-pass risk:** an autopsy screen that only appears if the player navigates to a menu item (not automatic on game-over) — the test must assert the autopsy is FORCED, not optional, when the state reaches decline.

**AC-9 (Autopsy events are recorded at threshold crossings: CAPEX, budget cuts, population exodus, insolvency).** Add new field `SimState.autopsyEvents: AutopsyEvent[]` (a bounded ring, max 50 entries per save). Define four event types that auto-record to this array:
- **CAPEXThreshold:** A single building placement + construction cost > 1M credits (AARON DECISION: placeholder).
- **BudgetCut:** A policy change or expense reduction that cuts any service budget (e.g. police, education) by > 25% (AARON DECISION: placeholder) in one month.
- **PopulationExodus:** Month-over-month population drop > 10% (AARON DECISION: placeholder) from the previous month's peak.
- **InsolvencyBand:** State transition into `insolvencyState !== 'solvent'` (warning/crisis/administration/bailout/decline).

Each AutopsyEvent records: { month, tick, type, label, deltaValue, context }. Example: `{ month: 48, tick: 12000, type: 'CAPEXThreshold', label: 'Steel Mill construction started', deltaValue: -1500000, context: { reason: 'building placed', specId: 'ind_steelmill' } }`. **Justification for new field:** The autopsy narrative (AC-10) MUST be deterministic and readable from a pre-wipe capture; it cannot rely on ephemeral UI state or on-demand Go-side analysis. Pre-recording events as they cross thresholds satisfies both GR#21 (determinism) and GR#27 (capture-before-wipe). Test: `webconsole/test/hamlet-autopsy-events.test.ts` — (a) Place a building that costs > 1M; assert a CAPEXThreshold event lands in autopsyEvents; (b) Dispatch an action cutting police budget by 30%; assert BudgetCut event recorded; (c) Drive population to a 15% month-over-month drop; assert PopulationExodus event; (d) Cross into insolvency (funds to -1000); assert InsolvencyBand event. **False-pass risk:** events recorded at every tick of a falling population (noise) instead of only on threshold crossings — the test must assert a PopulationExodus event fires ONCE per decline onset, not every tick that population is low. **Design note:** The TS sim records these; no Go engine wiring needed.

**AC-10 (Autopsy narrative: chronological causal chain from ledger).** The autopsy timeline renders events in REVERSE chronological order (newest first). For each event, the UI shows a one-line description ("Month 48: Insolvency declared") and a 2-3 line context body ("Treasury funds hit 0 after 3 months of deficits. No available loans."). The context is deterministically derived from recorded event fields + existing SimState fields (lastFlows, history, declineState) at RENDER time, not pre-computed at event-record time. No Go-side spiral or projections data is required; the narrative reads only from TS state (the autopsyEvents array + the existing DeclineState struct which is already exposed via int.protocol). The player can hover/click on each event to expand it and see the exact numbers (delta value, state snapshot). Test: `webconsole/test/hamlet-autopsy-narrative.test.ts` — load a pre-wipe capture (AC-11) that has autopsyEvents; render the autopsy modal; assert events are ordered newest-first; assert the one-line summary text is generated correctly (e.g. "Month 48: Insolvency Declared" from the event.month + event.type); assert drill-down on an event shows context text derived from the event context field + lookups in history/lastFlows. **Design note:** the autopsy narrative is a DETERMINISTIC READ of already-recorded state — it does not derive new analysis, it only formats what was captured. No new Go edges required; all data is TS-state-sourced.

**AC-11 (GR#27: capture-before-wipe persists autopsy state).** Before any city wipe / "Start Over" / hard reset, the sim captures the full `SimState` (including autopsyEvents) into a persisted debug JSON. Per GR#27, this capture is FAIL-CLOSED: if writing fails, the wipe is rejected with an error and the player is returned to the game (never silently wipes without a capture). The capture is written to `localStorage` under a namespaced key `metropolis.autopsy.<lineageId>` or a similar durable location. Test: `webconsole/test/hamlet-autopsy-capture-before-wipe.test.ts` — (a) Create a city, drive it to decline, trigger the wipe-confirmation action; intercept the save-capture call; assert it attempts to write the full autopsyEvents array; (b) mock a write failure (e.g. localStorage quota exceeded); assert the wipe action is rejected and the game state is unchanged; (c) on success, assert the capture is readable and contains autopsyEvents; (d) on a fresh new city, assert the player can access a "Review Previous Autopsy" option in a post-game menu, which loads and renders the captured state. **Design note:** The capture is opportunistic (only if the player starts a new game after a decline); a "Load Previous Autopsy" feature is a forward-compat placeholder, not built in this AC.

**AC-12 (Autopsy state fields: the SSOT for diagnostics).** The autopsy derives its timeline and narrative entirely from these SimState fields (no other data sources):
- `declineState: { enteredAt, peakPopulation, finalPopulation, minFundsEver, totalSpending }`
- `autopsyEvents: AutopsyEvent[]` (new field, recorded by AC-9 logic)
- `lastFlows: { inflows, outflows, population }` (already exists, used for context)
- `history: TickRecord[]` (already exists, used to find inflection points)
- `demographicHistory: MonthlyDemographics[]` (already exists, used to detect exodus events)
- `insolvencyState, insolvencyRawBand, administrationState, bailoutState` (already exist, used to trace insolvency path)

No new engine fields are REQUIRED to be added by this AC. All autopsy logic is computed from existing state. Test: `webconsole/test/hamlet-autopsy-fields-ssot.test.ts` — (a) Load a real save file (a pre-decline city snapshot); do NOT add new fields; render autopsy from the six fields above; assert the timeline is complete and no "missing data" placeholders appear; (b) load a save that is missing declineState (e.g. an old save before the decline feature landed); assert the autopsy gracefully shows "City has not reached decline" and exits without error.

### D. Autopsy — Cross-Item Dependencies & Capture-Before-Wipe

**AC-13 (AC-9 cross-coordinate: FEAT-142 death-spiral events and FEAT-146 autopsy events coexist).** FEAT-142 "Death Spiral" and FEAT-146 "Autopsy" both track decline; they are INDEPENDENT systems. FEAT-142 is Go-side (engine.spiral S-score thresholds and outcomes); FEAT-146 is TS-side (autopsyEvents array recording population/budget/insolvency crossings). The two CAN coexist without conflict: FEAT-146's PopulationExodus event (AC-9) is a TS-side OBSERVATION ("pop dropped 10% month-over-month"); FEAT-142's S-score damping is a Go-side ENGINE MECHANIC ("S>0.6 suppresses migration inflow"). If both are active, the autopsy's narrative can cite BOTH the S-score STATE (read from the captured state if available via int.protocol) AND the TS-side event ledger to construct a complete causality chain. AARON DECISION: confirm build order and whether captured S-score state is needed in the autopsy (if yes, that state is already exposed via int.protocol; no new edge required). Test: this AC is NOT blocked — FEAT-146 builds with or without FEAT-142. If FEAT-142 ships first, the captured SimState may include a `spiralScore` field (TBD); the autopsy would cite it in context. If FEAT-142 ships later, the autopsy still works, just with fewer causal signals.

**AC-14 (GR#27 interplay: autopsy runs on captured, not live state).** The instant declineState is set (tick T), advance() must ALSO call the capture-before-wipe hook (same hook that fires on "Start Over" button). The capture writes the full SimState including autopsyEvents to durable storage. If the player immediately clicks "Start Over" (before advancing another tick), the autopsy modal loads the captured state, not the (now-wiped) live state. Test: `webconsole/test/hamlet-autopsy-capture-determinism.test.ts` — (a) Reach decline at tick T=12000; capture is written; (b) render the autopsy modal at tick T=12000; assert it reads from the same capture; (c) advance to tick T=12001 (hypothetically, no new game yet); the live state now has no decline (wiped); (d) render autopsy again; assert it STILL reads the capture from T=12000, not the live state. **Design note:** This is critical: autopsy is a FORENSICS tool, always examining a frozen-in-time capture, never the current live state.

### E. Tests: Coverage, Determinism, Backward Compat

**AC-15 (Unlock gates are deterministic: same save, same XP = same visibility).** The `levelOf(state.xp)` is deterministic (no Date.now() or Math.random()); spec visibility via `specUnlocked(state, spec)` is a pure function. Replay a city journal (via genesisReplay.ts) and assert that the same ticks produce identical unlock states. Test: `webconsole/test/hamlet-bootstrap-unlock-determinism.test.ts` — serialize a city at tick 5000; replay the journal from tick 0 to tick 5000; at every 100-tick checkpoint, assert `levelOf(state.xp)` matches the serialized version; assert `specUnlocked(state, spec)` returns identical values for a sample of specs. **False-pass risk:** reading level from a stale cache or a UI-local useState instead of sim state — the test must assert that xp is on the types.ts SimState object, journaled, and survives load/save cycles.

**AC-16 (Autopsy events are deterministic: no wall-clock time, no UUID).** Each AutopsyEvent is derived ENTIRELY from deterministic state (tick, month, financial deltas, population deltas). No event uses Date.now(), Math.random(), or UUID generation. Two runs of the same journal produce identical autopsyEvents arrays. Test: `webconsole/test/hamlet-autopsy-determinism.test.ts` — record journal J; replay J twice (fresh state each time); assert the autopsyEvents arrays are BYTE-IDENTICAL (not just "same count", but `JSON.stringify(events1) === JSON.stringify(events2)`). **False-pass risk:** a GUID or timestamp appended to each event, or an event order that depends on object iteration — the test must assert events are ordered by (month, tick, type) with no non-deterministic fields.

**AC-17 (Backward compat: old saves without autopsyEvents deserialize safely).** A save file written before this feature existed has no `autopsyEvents` field. On load, initialState() or the hydrate path must treat absence as `[]` (empty ledger). The existing `xp` field on SimState is not new; this AC asserts autopsyEvents gracefully absent. Test: `webconsole/test/hamlet-bootstrap-backward-compat.test.ts` — load a real save file from before the autopsy feature (or construct one by deleting autopsyEvents); assert the UI renders without crashing; assert autopsy-render code gracefully shows "No prior events" when autopsyEvents is missing or empty. **False-pass risk:** a load that throws an error — the test must assert the fallback value explicitly.

**AC-18 (UI screens respect unlock gates: build menu, finance panel, policies).** The three main UI surfaces (MapView build menu, FinancePanel, PolicyPanel) all call the existing `specUnlocked(state, spec)` function to determine visibility (reusing the EXISTING gate, per GR#20; no new isBuildableAtTier function). This function is pure, deterministic, and reads only from `state.xp` via `levelOf()` and the spec's `sp.unlock` threshold. Test: `webconsole/test/hamlet-bootstrap-ui-gates.test.ts` — at level 0, assert the build menu only shows specs with `sp.unlock <= 0`; attempt to place a spec with `sp.unlock = 10`; assert placement fails with "locked" feedback; grow XP to level 10; re-test; the same spec is now visible and placeable. Repeat for FinancePanel (some budget sliders hidden until level N) and PolicyPanel (tourism/austerity policies gated by unlock levels). **Design note:** The unlock gate is data-driven via `sp.unlock` in data.ts (one SSOT, not scattered conditionals). The existing specUnlocked function is the sole gatekeeper; this AC only asserts it is consulted by UI surfaces.

### F. Tests: Balance & Placeholders

**AC-19 (XP-level boundaries are placeholders, directional only).** The XP thresholds for each `levelOf()` boundary (100 XP → level 1, 500 XP → level 2, etc.) are marked in code as PLACEHOLDER values pending Aaron's balance pass and reconciliation with the 13-tier population ladder. Each level-crossing constant has a comment: `// PLACEHOLDER: Aaron balance pass will tune this per the tier-level mapping`. Also, each spec's `sp.unlock` level value is a placeholder pending Aaron's confirmation of which specs unlock at which levels. Test: `webconsole/test/hamlet-bootstrap-placeholders.test.ts` — grep the code for `LEVEL_*_THRESHOLD`, `SPEC_*.unlock` values, and assert each has a matching `// PLACEHOLDER` comment in data.ts. No AC pins a numeric value. **False-pass risk:** code that runs the test but has actual hard-coded thresholds inline instead of in named constants — the test must assert the constants are named, exported, and used by specUnlocked().

**AC-20 (Event thresholds (1M CAPEX, 25% budget cut, 10% exodus) are placeholders, directional only).** The event-recording thresholds in AC-9 are similarly marked as placeholders. Test: same pattern as AC-19 — grep for `AUTOPSY_EVENT_CAPEX_THRESHOLD`, `AUTOPSY_EVENT_BUDGET_CUT_PCT`, `AUTOPSY_EVENT_EXODUS_PCT`, assert each has a `// PLACEHOLDER` comment. No AC pins a value.

**AC-21 (Autopsy event context is human-readable, not a raw data dump).** The one-line label and body text for each event is generated by a pure function `autopsyEventDescription(event, state)` that formats human-readable prose: e.g. `"Month 48: Insolvency Declared"` from `{ month: 48, type: 'InsolvencyBand', … }`. The body text (2-3 lines) is derived from event.context fields + fallback to state snapshots. Never render raw JSON or field names to the player. Test: `webconsole/test/hamlet-autopsy-narratives.test.ts` — construct a synthetic autopsyEvents array with known events; render each via autopsyEventDescription(); assert the output is readable English with no bare field names or JSON fragments; spot-check specific events (e.g. CAPEXThreshold → "Building placed: Steel Mill, cost £1.5M").

**AC-22 (Autopsy summary stats (peak pop, min funds, total spending) are sourced from declineState, not re-computed).** The four summary rows on the autopsy screen read DIRECTLY from `declineState` fields (not recalculated from history). This ensures the autopsy shows what the engine observed at decline time, not a fresh recalculation. Test: `webconsole/test/hamlet-autopsy-summary.test.ts` — load a pre-decline capture with a known declineState; extract the summary values; assert they exactly match the screen's rendered text (no rounding/formatting discrepancies). **False-pass risk:** a summary that recalculates max population from history instead of reading declineState.peakPopulation — the test must assert the read is direct, not derived.

---

## GR#25 Conformance & Aaron Decisions

### GR#25 Edge Conformance

**Statement per GR#20:** This feature's ACs conform to the UI/engine protocol-only split. The tier gates (AC-1 through AC-5) reuse the EXISTING `specUnlocked(state, spec)` function in the TS sim (`webconsole/src/sim/engine.ts`), which is pure TS logic with no Go edge. The autopsy (AC-8 through AC-14) reads only from existing `SimState` fields plus one NEW field (`autopsyEvents`, justified in AC-9) — all TS-side, no new Go edges.

**NO NEW EDGES REQUIRED.** The only potential future integration is with FEAT-142 (if its S-score state is captured and available via int.protocol), but that is OPTIONAL and does not block FEAT-146.

**Registered modules consumed (via int.protocol views, already REGISTERED):**
- `int.protocol` (Engine↔UI contract) — carries insolvency/decline state already exposed to UI
- TS sim's own `specUnlocked()` function (existing, no Go dependency)

### Aaron Decisions Required (Must Be Resolved Before Code Dispatch)

1. **AARON DECISION A1: XP-level ↔ population mapping (AC-2, AC-3, AC-4, AC-5, AC-19)**
   - Spec doc (FEAT-146 Desc §1) names thresholds at population: 100 (Hamlet), 500 (Village), 2000 (Town).
   - Master plan (code.json §8) defines a 13-tier population ladder: 100, 500, 5k, 20k, 250k, …
   - Which population boundaries map to which XP levels?  (If plan says "5k pop = level 5", then AC-2 sets `LEVEL_5_THRESHOLD` based on accumulated XP; spec and plan must align.)
   - **Impact:** ACs 2-5 and AC-19 depend on this; code thresholds are wrong until Aaron rules.

2. **AARON DECISION A2: Mid-game unlock set — which specs? (AC-3, AC-5, AC-19)**
   - At a mid-game level (TBD, ~500 pop?), does the player unlock: farms ONLY, or farms + freight hub + medium-density residential?
   - Each unlock decision sets `sp.unlock = <level>` in data.ts; which specs belong at which levels?
   - **Impact:** AC-3 test must know which specs become visible at which levels; AC-19 must verify PLACEHOLDER comments on each sp.unlock value.

3. **AARON DECISION A3: Import surcharge warning threshold (AC-5)**
   - AC-5 clears the warning once import costs drop below a threshold OR the player builds a farm.
   - What is the target threshold? (e.g. "import cost < £10k/month" or "<5% of income"?)
   - **Impact:** AC-5 test needs the clear condition.

4. **AARON DECISION A4: FEAT-142 optional integration (AC-13, optional)**
   - If FEAT-142 "Death Spiral" ships before or after FEAT-146, can the autopsy narrative cite S-score state if available?
   - If yes, is the S-score state already exposed via int.protocol (no new edge needed), or does it need a new protocol view?
   - **Impact:** AC-13 tests; FEAT-146 does NOT block on FEAT-142 (both can coexist independently), but full causality narrative benefits from both.

---

### GR#25 Detailed Conformance Check

**Final Statement:** FEAT-146 requires **ZERO new edges** per GR#20 (UI/engine protocol-only split).

**Tier gates (AC-1 through AC-5):** Reuse existing `specUnlocked(state, spec)` function in TS sim. No UI→engine import. ✓ **CONFORMS.**

**Autopsy (AC-8 through AC-14):** Read entirely from existing SimState fields (`declineState`, `lastFlows`, `history`, `demographicHistory`, `insolvencyState`, `administrationState`, `bailoutState`, `ledger`) plus ONE NEW FIELD (`autopsyEvents`). The new field is sim state only; no new Go API. ✓ **CONFORMS.**

**AC-9 autopsyEvents justification:** Needed for GR#21 (deterministic replay) and GR#27 (capture-before-wipe). Pre-recording events as they fire satisfies both constraints; on-demand Go-side analysis would violate both (non-deterministic re-analysis, cannot capture a post-wipe diagnosis). ✓ **JUSTIFIED.**

**Optional future integration (AC-13 / FEAT-142):** If FEAT-142's S-score state is exposed via existing int.protocol views, the autopsy can cite it in its narrative. No new edge required; leverages existing int.protocol contract. ✓ **NO BLOCKER.**

**Conclusion:** All ACs conform to GR#20 and GR#25. The feature is eligible for code dispatch once Aaron's four decisions are recorded.

---

## Recommended increments (if edge registration requires staged dispatch)

1. **inc1:** Hamlet bootstrap tier gates (AC-1 through AC-6, AC-15 through AC-20)
   - Requires: `ui.screen.map → engine.unlocks` edge registered
   - Includes: tier-0 UI, tier crossings, tier notifications, import-warning placeholder
   - Does NOT include: autopsy (inc2)

2. **inc2:** Autopsy events & diagnostics (AC-8 through AC-14, AC-21 through AC-22)
   - Requires: GR#27 capture-before-wipe hook already in place
   - Does NOT require: new Go edges (optional: if slow-fuse or spiral integration is desired, route to FEAT-142/FEAT-147)
   - Includes: event recording, autopsy render, determinism + backward-compat tests

---

## Test files (not built, test structure only)

- `webconsole/test/hamlet-bootstrap-tier0-visibility.test.ts` — AC-1
- `webconsole/test/hamlet-bootstrap-tier-crossings.test.ts` — AC-2, AC-3
- `webconsole/test/hamlet-bootstrap-tier-notifications.test.ts` — AC-4
- `webconsole/test/hamlet-bootstrap-import-warning.test.ts` — AC-5
- `webconsole/test/hamlet-bootstrap-loop-alive.test.ts` — AC-6
- `webconsole/test/hamlet-bootstrap-attract-spiral-linked.test.ts` — AC-7
- `webconsole/test/hamlet-autopsy-screen.test.ts` — AC-8
- `webconsole/test/hamlet-autopsy-events.test.ts` — AC-9
- `webconsole/test/hamlet-autopsy-narrative.test.ts` — AC-10
- `webconsole/test/hamlet-autopsy-capture-before-wipe.test.ts` — AC-11
- `webconsole/test/hamlet-autopsy-fields-ssot.test.ts` — AC-12
- `webconsole/test/hamlet-autopsy-capture-determinism.test.ts` — AC-14
- `webconsole/test/hamlet-bootstrap-determinism.test.ts` — AC-15
- `webconsole/test/hamlet-autopsy-determinism.test.ts` — AC-16
- `webconsole/test/hamlet-bootstrap-backward-compat.test.ts` — AC-17
- `webconsole/test/hamlet-bootstrap-ui-gates.test.ts` — AC-18
- `webconsole/test/hamlet-bootstrap-placeholders.test.ts` — AC-19
- `webconsole/test/hamlet-autopsy-placeholders.test.ts` — AC-20
- `webconsole/test/hamlet-autopsy-narratives.test.ts` — AC-21
- `webconsole/test/hamlet-autopsy-summary.test.ts` — AC-22

---

**Total AC count: 22 acceptance criteria.**

**GR#25 conformance: 0 new edges required. Feature conforms to GR#20 (protocol-only UI/engine split). ✓**

**Aaron decisions pending: A1 (XP-level ↔ population mapping), A2 (mid-game unlock specs), A3 (import surcharge threshold), A4 (FEAT-142 optional integration).**
