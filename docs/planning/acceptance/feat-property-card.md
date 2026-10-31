# Requirement pack: the Monopoly-style property card (FEAT-2326609841)

Standard: `docs/planning/acceptance/TEMPLATE-requirement-pack.md`. Author: lead BA pass, 2026-10-31, from Aaron's words: "for every building we need a monopoly style property card (information) which shows its utility usage, cost, maturity level, building name and a picture, the card can be closed but should look nice for the type of property and show building value, water consumption, waste water etc."

## 1. Context and goal

- **BOW:** FEAT-2326609841 (P2). mkey for commits: `[FEAT-2326609841]`.
- **Northstar stage:** Baseline One → dogfood ("you decide, the city responds" needs the player to SEE what each building is doing). Webconsole is the playable surface.
- **What exists (verified in code, 2026-10-31 — this pack upgrades, it does not invent):**
  1. **The selection seam is live.** `MapView.tsx` holds `selected: Building | null` (click selects, click-again deselects, ~line 1203) and renders a selected-building panel at ~line 1794 feeding `BuildingProfileView` (FEAT-1972079866): CAPEX/OPEX line, REQUIRES list, PRODUCES list, with PRODUCES suppressed under construction (Aaron Q100092 — keep that ruling).
  2. **A catalogue modal exists:** `SpecCard.tsx` (FEAT-1972079860), Escape-to-close, deliverables from the Spec only.
  3. **The data is already on `Spec`** (`src/sim/data.ts` ~line 118): `name`, `blurb`, `color` (the Monopoly band colour, per spec), `category` ('network'|'zones'|'services'), `kind`, `cost`, `upkeep`, `mw`, `served`, `residents`, `jobs`, `tourism`, `children`, `unlock`, `tag` ('pollution'|'clean'|'waste'), plus `buildingProfile(spec, taxRates)` which already derives REQUIRES/PRODUCES with units.
  4. **Instance facts** live on `Building` + state: construction progress (`builtTick` + `constructionTicks(sp)`, the same maths `isOnline` uses), tier/consolidation lineage, coverage/brownout state (`effectiveCleanWaterCoverageOf`, `effectiveWastewaterCoverageOf`, `brownoutOf`), and upkeep actually charged (`upkeepChargeableOf` — genesis map pieces pay £0, BUG class already ruled).
  5. **Water/waste are citywide demand models** (`waterDemandOf(s)` → {clean, waste}): per-building consumption is DERIVED (the building's residents/served share of the demand constants), not stored per instance. The card must present it as the derivation, never invent a second water model (GR#3).
- **Goal:** one closable, type-styled property card per building instance — Monopoly visual grammar (coloured title band, name, picture, ruled stat lines) — showing: name + picture, type band, placement cost, **building value**, **maturity level**, utility usage (power MW in/out, water consumption, waste water, refuse where applicable), upkeep/opex, and the existing REQUIRES/PRODUCES facts. It replaces the current selected-building panel's chrome (the panel's data layer, `buildingProfile`, is reused).

## 2. Acceptance criteria

Surface key: **Card** = the React component rendered when `selected` is non-null in MapView. All figures from the SSOT derivations — no literal copied into the component (GR#15/GR#3).

- **AC-1 (card opens and closes).** Given a placed building, when the player clicks it, then the card renders with the building's `name`, its type band coloured `spec.color`, and a close affordance; Escape, the close button, and clicking another building all dismiss/replace it; clicking the same building again deselects (the existing MapView contract stays). *Mutation:* remove the close handler — the close test fails.
- **AC-2 (type-styled, data-driven).** Given any two specs with different `color`/`category`, the band colour and the category label differ accordingly, sourced from the Spec (no per-building hardcoded styles). *Mutation:* hardcode one colour — the two-spec comparison test fails.
- **AC-3 (picture).** Every card shows a picture area that is NEVER empty: the per-spec art if present, else the deterministic fallback (see Q1 — the fallback must exist regardless of the art ruling: a generated SVG motif from `kind` + `color` + footprint is acceptable). Two different kinds must render visibly different fallbacks. *Mutation:* return the same motif for all kinds — the difference test fails.
- **AC-4 (cost and value).** The card shows: **Cost** = `placementCost(spec)` as paid at placement; **Value** = the ruled value model (Q2; interim = `Math.round(placementCost * BULLDOZE_REFUND_FRACTION)` labelled "resale", which is the only value figure the sim already owns). Both formatted with `fmtMoney`. *Mutation:* swap cost and value — the labelled-figure test fails.
- **AC-5 (maturity level).** The card shows maturity as the ruled ladder (Q3; interim = three states derived from existing facts: "Under construction (n/m days)" via `builtTick`+`constructionTicks`, "Operational", and "Consolidated tier N" where the instance is a consolidator product). The under-construction state keeps Q100092: nameplate PRODUCES suppressed. *Mutation:* report Operational during construction — the construction test fails.
- **AC-6 (utility usage, derived not invented).** The card shows, where applicable to the spec: power (MW demand or generation from `spec.mw` and the powerStats sign convention), **water consumption** and **waste water** (the building's share of `waterDemandOf`'s constants for its residents/served — computed by one exported helper the tests also call, so card and sim cannot disagree), refuse (where `wasteCapacity`/collection applies), and **upkeep actually charged** (`upkeepChargeableOf`, so genesis pieces honestly show £0). A spec with no water-bearing capacity shows "—", never 0-implying-measured. *Mutation:* double the helper's constant — the card test and the sim demand test disagree and fail.
- **AC-7 (live, not stale).** With the sim running, a card left open updates its instance facts (construction countdown, brownout/coverage badges) on tick without reopening; a brownout or zero-coverage state shows a visible badge, not silently wrong numbers. *Mutation:* freeze the props at open — the countdown test fails.
- **AC-8 (no layout regression).** The existing MapView interactions (pickup/move, bulldoze, keyboard) still pass their suites; the card never traps clicks meant for the map outside its own bounds. StrictMode double-mount safe (BUG-756 class: no ref-mutating updaters).
- **AC-9 (determinism / perf).** Rendering the card reads NO clock or storage (BUG-602) and performs no per-tick O(N) scans beyond what memoised selectors already provide (`memoOnState` for any new derivation).

## 3. Non-goals

- No change to any sim number, demand model or money flow — presentation only (the one new exported water-share helper is a refactor of existing constants, proven equal to the citywide total by test).
- No Go/TUI card this increment (the TUI's F3 screen shows queue facts; a text card is a follow-up if Aaron wants it).
- No art pipeline beyond the ruled source (Q1); no per-building photographs.
- No trade/mortgage mechanics despite the Monopoly look (the back-of-card "mortgaged" skeuomorph is out).
- Catalogue `SpecCard` modal stays for the build menu; this card is the INSTANCE view (they may share styling).

## 4. Developer direction

- **Files:** new `webconsole/src/components/PropertyCard.tsx` (+ `.css` or styled block in the existing stylesheet), new `webconsole/src/sim/buildingUtility.ts` (the exported water/waste/power share helper over the existing constants), `MapView.tsx` (swap the selected-panel chrome for the card; keep `BuildingProfileView`'s data via `buildingProfile`), tests under `webconsole/test/` (`property-card.test.tsx` with a REAL StrictMode mount per the BUG-756 memory, plus a node-test for the utility helper's sum-equals-citywide identity).
- **Monopoly grammar:** title band (spec.color, white name text), picture plate, ruled table of stat lines, footer with cost/value — one component, styled by data. Spec.color is the SSOT for the band; category gives the band's label.
- **Constraints:** GR#21 (no map-range ordering anywhere new); BUG-602 (no Date.now/localStorage in render); TDZ rule (new module constants live in leaf modules; fresh-process entry-order test if `buildingUtility.ts` is imported by data.ts — prefer importing FROM data.ts only); converge fixtures must stay byte-identical (presentation-only — if any flows change, STOP, that is a defect in the approach); scoped verification ONLY (`node tools/test/scoped.mjs` on touched files + `npx tsc --noEmit`); 30-minute watchdog.
- **The wiring-port floor does NOT apply** (no Go change) but golangci is irrelevant and the bounded webconsole set runs at the lead's port if `computeFlows`-adjacent files are touched (they should not be).

## 5. Tester watch-fors

1. **Built-but-not-wired:** open the real dev server, click five building kinds incl. one under construction, one genesis map piece (£0 upkeep must show), one brownout case — screenshot each; a green unit test on a hand-fed Spec proves nothing about the live selection path.
2. The water-share helper's identity test: sum of per-building shares over a fixture city == `waterDemandOf(s)` exactly (both clean and waste) — this is the gate that stops a second water model creeping in.
3. Prove each AC mutation red (scratch copy, md5 restore).
4. StrictMode double-mount, and a card held open across a save/load.
5. No wall-clock assertions; the AC-7 countdown test uses ticks.
6. Dual-engine: no Go change; state "not applicable" explicitly; no converge regen.

## 6. Round guidance (GR#23)

Attacker ≠ author; verdict from the attacker's OWN throwaway worktree; covering verdict after commit. Attack first: (1) the water-share helper vs the citywide model (the second-money-model class — mutate the shared constant one side only); (2) stale-props/frozen card (AC-7); (3) genesis/£0-upkeep honesty and the construction-suppression ruling (Q100092 regression); (4) StrictMode + rapid select/deselect/move interleavings; (5) click-trapping outside card bounds; (6) fallback art determinism (same kind + seed → same motif twice).

## 7. Done means

Clicking any building in the running webconsole opens a type-coloured, closable Monopoly-style card showing name, picture (or deterministic fallback), cost, the ruled value figure, the ruled maturity ladder, and derived power/water/waste/upkeep that provably sum to the citywide model, live-updating under ticks, with the existing map interactions untouched, all ACs mutation-proven, and FEAT-2326609841 carrying an independent ACCEPT and refs.

---

## Open questions for Aaron (the pack is dispatchable once these are ruled; interims are stated above)

1. **Picture source (the big one).** No building art exists in the repo. Options: (a) deterministic generated SVG motifs per kind/colour (zero assets, ships now — the interim AC-3 fallback IS this); (b) a commissioned/AI-generated sprite set checked into `webconsole/public/art/` (needs your sign-off on provenance and style, ~90 specs); (c) your own art later, fallback until then. Recommend (a) now + (c) slot.
2. **Building value model.** Interim = resale (`BULLDOZE_REFUND_FRACTION` of placement cost). Alternatives: depreciating value by age, land-value-linked (engine.landvalue exists Go-side only), or market value once a property market exists. Which figure should the card call "value"?
3. **Maturity ladder.** Interim = Under construction → Operational → Consolidated tier N. Did you mean something richer (age bands, upgrade levels, the unlock era it belongs to)? Name the ladder and the card will render it.
4. **Scope of "every building":** roads/pipes/network pieces too, or zoned+service buildings only? (Interim: everything selectable gets a card; network pieces get the slim variant — band, name, cost, wear where tracked.)
