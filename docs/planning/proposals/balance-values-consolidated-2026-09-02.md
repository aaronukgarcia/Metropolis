# Consolidated Balance-Value Proposal (2026-09-02)

**For:** Aaron's row-by-row approval, per his ruling on Q100060/Q100061/Q100074 ("A1 — propose
values", each cell adjustable in-game under Config where he has separately ruled a lever is
player-facing). **Docs only — no code changed by this document.**

This file gathers **every outstanding PLACEHOLDER balance number** currently sitting in specs,
acceptance-criteria docs, and code comments across the webconsole TS sim and the Go engine, into
one editable master table. Nothing here is invented without a source — every row cites the spec,
BOW item, or file:line it came from, and every rationale is grounded in a real UK reference where
Aaron's realism preference applies (crime → ONS, wages/tax → HMRC/ONS Kent, construction → BCIS /
Kent County Council tenders, grid tariffs → UK wholesale power market) or flagged as an
internal-consistency lever where no real-world number applies.

**Status key:**
- **PROPOSED** — a new placeholder from an unimplemented spec, awaiting first sign-off.
- **APPROVED-RETUNABLE** — Aaron has already approved this number once (a prior ruling or a landed
  feature's default) but it remains open to retuning here.
- **EXISTING-CONVENTION** — a number already live in shipped code, listed for completeness/context
  so Aaron can retune it in the same pass if desired, not because it is currently blocking anything.

---

## 1. Commercial/Industrial consumption-feedback (FEAT-2326609721)

Spec: `docs/planning/acceptance/FEAT-2326609721-commercial-industrial-feedback-2026-09-02.md`
(webconsole-only, no Go engine changes, no new cross-module edges per GR#25).

| Value name | Proposed value | Unit | Where it lives | Rationale | Adjustable in-game? |
|---|---|---|---|---|---|
| `COMMERCIAL_CUSTOMER_SHARE` | 0.22 | fraction of population | NEW constant, `webconsole/src/sim/engine.ts` (mirrors existing `shopBase` formula in `demandOf()`, engine.ts:50) | Matches the already-shipped `demandOf()` shop-base coefficient — internal consistency, no independent real-world citation exists for "fraction of a city that shops locally" | No — sim-internal tuning constant, not exposed as a player lever |
| `COMMERCIAL_CATCHMENT_PER_ZONE` | 5,000 | people/zone | NEW constant | Midpoint of the spec's suggested 3,000–8,000 range; sets the population at which one commercial zone's marginal revenue saturates | No |
| `INDUSTRIAL_CUSTOMER_SHARE` | 0.18 | fraction of population | NEW constant (mirrors `indBase` in `demandOf()`, engine.ts:52) | Matches the already-shipped industrial-base coefficient | No |
| `INDUSTRIAL_CATCHMENT_PER_ZONE` | 4,000 | people/zone | NEW constant | Midpoint of the spec's suggested 2,000–6,000 range; industrial saturates faster than commercial (fewer, larger customers) per the spec's own rationale | No |
| `COMMERCIAL_DEMAND_SCALE_FACTOR` | 175 | dimensionless (demand-to-attractiveness sensitivity) | NEW constant | Midpoint of the spec's suggested 150–200 range | No |
| `INDUSTRIAL_DEMAND_SCALE_FACTOR` | 175 | dimensionless | NEW constant | Same as commercial for symmetry (spec proposes the same 150–200 range for both, no differentiating citation) | No |
| `COMMERCIAL_ATTRACTIVENESS_BASE` | 1.0 | dimensionless multiplier | NEW constant | Spec's own suggested baseline; keeps commercial desirability on the same scale as residential's existing attractiveness term before demand modulation | No — design/flavor lever, not player-facing |
| `INDUSTRIAL_ATTRACTIVENESS_BASE` | 1.0 | dimensionless multiplier | NEW constant | Same as commercial | No |

---

## 2. Grid import/export tariffs (FEAT-2326609711 + FEAT-2326609740)

Spec: `docs/planning/acceptance/feat-2326609711-inc1-grid-import.md`,
`docs/planning/acceptance/FEAT-2326609740-construction-lifecycle-2026-09-02.md`. Already **landed
and live** in `webconsole/src/sim/fiscal.ts` — listed here for the arbitrage-guard pair Aaron asked
for and because AC-8 of FEAT-2326609740 requires this invariant to be re-verified on any
rebalancing.

| Value name | Proposed value | Unit | Where it lives | Rationale | Adjustable in-game? |
|---|---|---|---|---|---|
| `GRID_EXPORT_TARIFF_PER_MW` | 1.6 (unchanged) | £/MW/tick | `fiscal.ts:93` (also cited `fiscal.ts:38` in the construction-lifecycle doc) | **EXISTING-CONVENTION.** Must stay strictly `< GRID_IMPORT_TARIFF_PER_MW` (arbitrage guard, feat-2326609711 AC-4) and `> local_cheapest_amortised_£/MW/tick` (payback hurdle) | Not currently — a future Config exposure would need the invariant test to move with it |
| `GRID_IMPORT_TARIFF_PER_MW` | 2.5 (unchanged) | £/MW/tick | `fiscal.ts:113` | **EXISTING-CONVENTION.** Import > export by design so buying-to-resell can never profit; ~56% premium over export today | Not currently |
| No-arbitrage invariant | `EXPORT < IMPORT` (hard assertion) | — | `feat-2326609711-inc1-grid-import.md` AC-4; re-affirmed `FEAT-2326609740-construction-lifecycle-2026-09-02.md` AC-8 | Structural safety rule, not a balance number — any retuning of either tariff must keep this true | N/A — invariant, not a tunable value |
| Grid-tariff real-world grounding | not yet researched | £/MWh | `docs/planning/bug-452-realistic-money-scale-plan.md` Open Question 4 | UK 2024-25 wholesale/export electricity market rates run **£50-120/MWh** — the current 1.6/2.5 (£/MW/tick, a different unit basis entirely — tick, not hour) has never been reconciled against a real tariff. **Flagged, not proposed** — Aaron must decide whether to re-anchor to a real £/MWh figure (see BUG-452 Q4) or keep this an internal-consistency-only lever | Open question, not yet a value |

---

## 3. Opening treasury / seed economy (BUG-452)

Spec: `docs/planning/bug-452-realistic-money-scale-plan.md` (plan-only, Aaron's ruling chain
2026-08-31 → 2026-09-01 already on the BOW item).

| Value name | Proposed value | Unit | Where it lives | Rationale | Adjustable in-game? |
|---|---|---|---|---|---|
| Starting treasury (Go engine `initialTreasury`) | £10,000,000 (10,000,000,000,000 µ£) | £ (micropounds) | `internal/engine/compose/compose.go:48` (currently £10 — toy value) | **APPROVED-RETUNABLE.** Aaron's carried-forward ruling 2026-08-31 12:04: "a round starting grant... trial figure, maybe start with 10m". Grounded as a defensible multi-year capital grant against Folkestone & Hythe DC's real ~£20-25M/year net revenue budget for a real town of similar eventual scale | Not yet — becomes a Config-exposed "starting grant" lever if Aaron wants difficulty presets |
| Starting treasury (webconsole `funds` initial) | 10,000,000 (unchanged) | £ (already numerically £10M-equivalent) | `webconsole/src/sim/engine.ts:345` | Already matches the Go-side anchor numerically; only needs an explicit unit doc-comment, no value change | Not yet |
| `initialCitizenWealth` | £5,000,000 (5,000,000,000,000 µ£) | £ (micropounds) | `internal/engine/compose/compose.go:49` | Ratio-preserved at 0.5:1 against the new treasury anchor (was £5 : £10 = 0.5:1 at toy scale) — an internal-consistency lever, not derived from the ONS migrant-wealth figures (those apply to arriving migrants, not the seed population; see BUG-452 Open Question 7) | No |
| Seed population (`seedCitizenCount`) | 50–200 (interim illustrative; NOT tile-derived) | citizens | `internal/engine/compose/compose.go:46` (currently 64) | **Open, not proposed as final** — Aaron's original ruling asked for OS-Terrain-tile-derived dwelling count, which this plan explicitly did not attempt (BUG-452 Open Question 1). UK census "hamlet" order-of-magnitude (<100-300, no services) used only as an illustrative placeholder pending that separate derivation | Open question — needs Aaron's steer on whether to fold the tile-derivation into this rebase or ticket separately |
| Ledger-scale divisor (`ledgerScaleDivisor`) | DELETE (retire the hack) | — | `internal/engine/compose/moneycirc.go:67` | Once treasury/population are re-anchored, the 1000x divisor that bridges toy-vs-real magnitudes has no remaining purpose — every real constant posts at full scale | N/A — removal, not a tunable value |

---

## 4. Wage / employment / income-tax model

Specs: `docs/planning/proposals/wage-employment-tax-model-2026-09-02.md` (audit),
`docs/planning/proposals/wage-ownership-model-2026-09-02.md` (design deep-dive). **Per Aaron's
Q100061 ruling, income-tax rate and rent level are Config-adjustable in-game.**

| Value name | Proposed value | Unit | Where it lives | Rationale | Adjustable in-game? |
|---|---|---|---|---|---|
| Income + NI blended tax rate (`incomeNITaxRateBp`) | 28% (2,800 bp, unchanged) | % of gross wage | `internal/engine/compose/moneycirc.go:160` | **EXISTING-CONVENTION**, already UK-grounded (basic-rate income tax ~20% + employee NI ~8-12% blended, `money-numbers-real-world.md` §4) — currently computed but never actually reaches the Treasury (D2ii silent destruction per the audit); this proposal just carries the number forward as the ONE real leg once D1/D2/D3 are fixed | **YES — Config, per Aaron's Q100061 ruling** |
| Corp tax rate (worked example, not yet a named constant) | 19% | % of firm pre-tax profit | `wage-ownership-model-2026-09-02.md` §6 example (b), new — not yet in code | UK corporation tax small-profits rate (2024-25) — used illustratively in the supermarket worked example; needs a named constant once Stage 3 (LTD roll-up) is built | **YES — Config, same lever family as income tax** |
| Existing (separate, already-registered) industrial/commercial tax-rate constants | `commercialTaxRateBp`=2000 (20%, VAT-grounded), `industrialTaxRateBp`=2500 (25%, UK corp-tax-grounded) | bp | `internal/engine/compose/moneycirc.go:141,148` | **EXISTING-CONVENTION** — already real-UK-rate-grounded, scale-invariant, no change proposed here | Not yet — could join the same Config surface as income tax for consistency |
| Average gross wage | £2,100/month (unchanged) | £/month/employed citizen | `internal/engine/compose/moneycirc.go:155` | **EXISTING-CONVENTION**, Kent regional ONS-grounded (`money-numbers-real-world.md` §4) | No — a derived economic output, not a player lever |
| Net wage (post-tax) | £1,512/month (= gross × (1-28%), unchanged) | £/month | `moneycirc.go:166-167` | **EXISTING-CONVENTION**, arithmetic derivative of the two rows above | Tracks the income-tax lever above |
| Monthly rent per household (flat baseline, real figure) | £1,000/month | £/month | `moneycirc.go:90` (`baselineOneMonthlyRentPerHousehold`, cited but not currently posted at full scale — `moneycirc.go:124` posts a hand-tuned `£10` placeholder instead because the real figure collapsed population 64→4 in testing) | ONS/Rightmove-grounded average UK rent figure (`money-numbers-real-world.md`), **must be re-verified against the new treasury/population scale** (BUG-452 §6, Inc2) before posting at full value — the historical collapse was a symptom of the OLD £10-treasury/64-population mismatch, not necessarily of the figure itself | **YES — Config, per Aaron's Q100061 ruling (rent level)** |
| Rent tax rate | same as income tax (28%), reusing `CatTaxIncome` | % | `wage-ownership-model-2026-09-02.md` §2 flow 7, §5 — new, not yet in code | Design recommendation: rental income taxed as ordinary personal income for simplicity (no separate rental-tax band, matches real UK self-assessment treatment at a basic level) | **YES — Config, same lever as income tax (rent-tax per the task's framing)** |

---

## 5. Crime mechanic (FEAT-crime-mechanic)

Spec: `docs/planning/acceptance/FEAT-crime-mechanic-2026-09-02.md` (Aaron's D2 direction,
Q100046). TS sim only; Go engine convergence is phase 2+.

| Value name | Proposed value | Unit | Where it lives | Rationale | Adjustable in-game? |
|---|---|---|---|---|---|
| `BASELINE_CRIME_RATE` | 35 | crimes/100k-population-equiv/month | NEW constant, `webconsole/src/sim/data.ts` or `engine.ts` | UK ONS Crime Survey for England & Wales 2024-25, mid-size urban area average (real range 20-60 by region/deprivation) — a city with zero services still has this ambient rate. **Sanity-checked against ONS directionality**: mid-sized-town baseline sits well below inner-city extremes, consistent with Folkestone's real profile | No — ambient environmental constant |
| `CRIME_BREEDS_CRIME_FACTOR` | 0.05 | fraction of prior-month rate carried forward | NEW constant | Small by design (spec requires ≤0.1 to keep feedback bounded/non-divergent) — each point of prior crime adds 5% of itself to the next month | No |
| Crime-breeding feedback cap | 30 | points (max feedback addition) | NEW constant | Prevents runaway even at very high starting crime (AC-6's bounded-feedback requirement) | No |
| `POLICE_REDUCTION_FACTOR` | 25 | points at 100% coverage | NEW constant | Directionally consistent with ONS findings that visible policing is the single largest measured deterrent lever among the four reducers — given the largest weight of the four | No — but paired with the existing Police-service budget, which IS a player spend lever |
| `EDUCATION_REDUCTION_FACTOR` | 15 | points at 100% coverage | NEW constant | Weighted below police (education is a slower, cultural lever, consistent with criminology literature's weaker/lagged effect vs. direct enforcement) | No — paired with the Education spend lever |
| `PARKS_REDUCTION_FACTOR` | 12 | points at 100% coverage | NEW constant | Weighted lowest of the three service reducers — parks/green space affect community cohesion and mental health but have the weakest direct crime-suppression evidence of the three | No — paired with the Parks spend lever |
| `WELLBEING_CRIME_FACTOR` | 0.15 | points per (100 − wellbeing) | NEW constant | At wellbeing 0, adds 15 points of crime pressure; at wellbeing 100, adds none — models the despair/isolation → crime pathway | No — derived from the wellbeing system, not directly player-set |

---

## 6. Congestion teeth (FEAT-congestion-teeth)

Spec: `docs/planning/acceptance/FEAT-congestion-teeth-2026-09-02.md` (Aaron's ruling Q100057, A1
approved — the mechanic itself is approved; the specific numbers below are still PLACEHOLDER per
the doc's own "⚠️ PLACEHOLDER" markers).

| Value name | Proposed value | Unit | Where it lives | Rationale | Adjustable in-game? |
|---|---|---|---|---|---|
| `CONGESTION_PENALTY_THRESHOLD` | 0.75 | saturation ratio (0-1) | NEW constant, `webconsole/src/sim/data.ts`/`engine.ts` | BA's own recommendation in the spec: sits below the existing auto-widen trigger (~0.80) so the player feels congestion **before** auto-scale spends money on their behalf, not after | No |
| `CONGESTION_SUSTAINED_TICKS` | 60 | ticks (≈2 in-game months) | NEW constant | BA's recommendation: long enough that a temporary spike doesn't sting, short enough a widened road gives felt relief within a couple of months | No |
| Aggregation strategy across congested lines | AVERAGE (of sustainably-congested lines only; factor = 1.0 if none sustained) | — | NEW, spec Open Question 5 | BA's recommendation, "blended" rather than MIN (harshest) or MAX (softest) — avoids one minor side-street tanking city-wide wellbeing | No — a formula choice, not a numeric lever |
| Penalty curve shape | Linear (threshold → 1.0) | — | NEW, spec Open Question 4 | BA's recommendation: deterministic, no surprise curves; the multiplier below can still tune the slope | No |
| `CONGESTION_INCOME_K` (secondary/optional income penalty) | 0.10 | fraction of business/freight/office income at full congestion | NEW constant, only if Aaron approves the income-penalty mechanism at all (spec Open Question 1) | BA's recommendation: 10% is "noticeable but not game-ending" — applied strictly after the existing brownout income penalty so the two never double-charge one root cause | No |

---

## 7. Construction lifecycle & baseload power (FEAT-2326609740)

Spec: `docs/planning/acceptance/FEAT-2326609740-construction-lifecycle-2026-09-02.md` (Aaron's
verbal design, 2026-09-02).

| Value name | Proposed value | Unit | Where it lives | Rationale | Adjustable in-game? |
|---|---|---|---|---|---|
| Construction jobs formula | `cost / 1_500_000` | jobs per building under construction | NEW, mirrors the existing `constructionTicks()` divisor pattern (`data.ts:212-220`, also `cost / 1_500_000`) | Reuses the SAME divisor already shipped for build-time, for internal consistency (a bigger, longer build also employs proportionally more people) — no independent real citation, an internal-consistency lever | No |
| Construction water draw | 5 | litres/tick, flat, independent of building type | NEW constant | Aaron's own proposed placeholder in the spec ("~5 litres/tick minimum") — flagged explicitly as his figure, not the BA's invention | No |
| Baseload plant list | nuclear, coal | — (classification, not a number) | NEW `baseload?: boolean` flag on `data.ts` `Spec` interface | Spec's Q2 recommendation: "smallest honest set" — CCGT is peaking/middle-load (excluded), wind/solar are obviously variable, hydro not yet in the catalogue | N/A — a data classification, not a tunable number |
| Upkeep-as-%-of-capex ratio (context for Section 8 below, BUG-452 §3.5) | ~2%/year | % of replacement capex | `bug-452-realistic-money-scale-plan.md` §3.5 | UK infrastructure asset-management rule-of-thumb (roads/schools/plant routine maintenance budgeting), explicitly flagged LOW confidence — no single UK-wide published ratio found | No |

---

## 8. Building capex catalogue rebase (BUG-452 §3.5 — anchors only, not the full 114-entry table)

The full catalogue rebase (114 `data.ts` `P(...)` entries) is BUG-452's single largest execution
item and is **not reproduced row-by-row here** — it needs its own dedicated balance pass once
Aaron approves the anchor citations below. Listed for completeness per the task's requirement to
surface every outstanding placeholder.

| Value name | Proposed value | Unit | Where it lives | Rationale | Adjustable in-game? |
|---|---|---|---|---|---|
| Primary school capex anchor | £8-11M | £ per new primary school | `bug-452-realistic-money-scale-plan.md` §3.5 | BCIS 2025 rate (~£1,850/m² × ~3,000m²) plus a real 2025 Kent example (More Park Catholic Primary, West Malling, delivered at £11M, Kent County Council June 2025) | No — catalogue price, not a runtime lever |
| Gas/CCGT power plant capex anchor | £0.75-1.5M/MW installed | £/MW | `bug-452-realistic-money-scale-plan.md` §3.5 | Using the new-build international reference (~£0.75M/MW) rather than the UK major-maintenance-event benchmark (~£50M/MW, a different thing entirely) as more representative of a "build a power plant" game action — flagged as a two-orders-of-magnitude judgement call for Aaron | No |
| Dual-carriageway road capex anchor | £10-15M/km | £/km | `bug-452-realistic-money-scale-plan.md` §3.5 | 2005 UK average (~£7.5M/km) inflation-adjusted to 2024-25 (+60-70% CPI); large/complex schemes (A465, Lower Thames Crossing) excluded as outliers | No |

---

## 9. HUD RAG thresholds (already Aaron-approved, listed for completeness)

Spec: `docs/planning/acceptance/FEAT-2326609720-inc2-tab-tree-and-rag-2026-09-02.md`. Aaron has
already approved rec-on-all for these bands; included so he can retune any cell in the same pass.

| Value name | Proposed value | Unit | Where it lives | Rationale | Adjustable in-game? |
|---|---|---|---|---|---|
| Wellbeing overall RAG bands | GREEN ≥70, AMBER 45-69, RED <45 | wellbeing score (0-100) | **EXISTING-CONVENTION**, `TopBar.tsx:30` | Already shipped and reused (GR#3 SSOT) — not re-invented for the new tab tree | **APPROVED** (per task's framing) |
| Wellbeing per-part RAG bands | GREEN ≥70, AMBER 45-69, RED <45 | part score (0-100) | **EXISTING-CONVENTION**, `RightDock.tsx:142` | Identical split, reused for all 11 wellbeing parts | **APPROVED** |
| Approval rating RAG bands | GREEN ≥55, AMBER 40-54, RED <40 | approval score (0-100) | `RightDock.tsx:101` (GREEN/RED line existing); AMBER band new | 55 already shipped as the pos/neg line; 40 chosen as a below-baseline erosion floor (avgTax=0 gives 62 today) | **APPROVED** |
| Service coverage RAG bands | GREEN ≥1.0, AMBER 0.8-0.99, RED <0.8 | cap/need ratio | Mirrors `waterBalanceOf`'s existing 0.8 leak line | Reuses one number across the whole Coverage Map instead of inventing one per service | **APPROVED** |
| Unemployment RAG bands | GREEN <7%, AMBER 7-15%, RED >15% | unemployment rate | NEW, `unemploymentOf(state)` selector already exists (BUG-524), no UI convention yet | Directional real-world-ish bands (UK full-employment is conventionally ~4-5%, recession-level is often flagged above ~8%; 15% RED is a severe-crisis band) — explicitly flagged placeholder in the source spec | **APPROVED** (per task's framing) |
| Housing capacity headroom RAG bands | GREEN ≥20% headroom, AMBER 5-20%, RED <5% (at/over cap) | headroom % | NEW, `onlineResidentsCapacity(state)` vs `state.population` | Matches the existing "comfortable / tight / at cap" framing already used in RightDock's housing sub-labels | **APPROVED** (per task's framing) |
| Line saturation RAG bands | GREEN <0.8, AMBER 0.8-1.0, RED = `overCapacity` true | saturation ratio | Reuses the same 0.8 line as service coverage/water-leak for cross-meter consistency | Internal consistency — one "headroom" convention across all meters | **APPROVED** |
| Insolvency band thresholds | warning at funds ≤ −£750,000, crisis at funds ≤ −£1,500,000 | £ (derived from `STARTING_TREASURY`) | `fiscal.ts`'s `insolvencyStateForFunds` | **EXISTING-CONVENTION** — direct state-machine mapping, already live; ratios to the treasury anchor (−0.5× / −1× at the OLD £10M anchor — will need re-deriving if the BUG-452 treasury rebase changes the anchor) | Not directly — tracks the treasury anchor lever |

---

## 10. Other placeholder numbers found while scanning `docs/planning/acceptance/*2026-09*.md`

| Value name | Proposed value | Unit | Where it lives | Rationale | Adjustable in-game? |
|---|---|---|---|---|---|
| DemandDock `optimalProvider()` budget parameter | `s.funds` (current treasury) at call time | £ | `FEAT-demanddock-overhaul-2026-09-02.md` §2.1 | Explicitly flagged PLACEHOLDER in the source — open question whether admin-mode/in-flight-affordability needs a different number than raw current funds | No — an implementation detail, not a player lever |
| Bailout re-arm policy numbers (clean-end threshold, re-arm cap, per-bailout cost) | not yet numbered — **design-dependent on Aaron's Q100045 ruling** (option A: cap re-arms + raise clean-end bar; option B: repeatable with per-bailout cost; option C: Aaron-specified) | £ / count | `FEAT-endgame-ladder-2026-09-02.md` (BUG-504/505/506) | Every threshold in this doc is explicitly marked PLACEHOLDER pending Aaron's choice of re-arm policy shape — **cannot be given a proposed value until that design choice is made**; flagged here as an open item, not a proposal | Not yet determined |

**Not found / could not source a number for:** a real UK £/MWh grid-tariff citation for
Section 2's open question (searched `bug-452-realistic-money-scale-plan.md` and
`fiscal.ts`; the plan itself states "not researched in this plan"); a per-milestone
RAG threshold for `FEAT-2326609720`'s Milestones row (the spec itself recommends NOT
inventing an AMBER/RED state — binary met/open only, correctly not a placeholder to fill in);
and BUG-504's endgame-ladder re-arm numbers (genuinely blocked on a prior, separate design
ruling, not a sourcing gap).

---

## How to approve

1. **Edit any cell above directly in this file**, then tell me **"balance table approved"** — your
   edited values become the build inputs for the corresponding features/fixes.
2. Or say **"approved as proposed"** to accept every PROPOSED/APPROVED-RETUNABLE value in this
   table exactly as written.
3. Rows marked as open questions (Section 2's grid-tariff citation, Section 3's seed-population
   derivation, Section 10's bailout re-arm policy) need a decision, not just a number — flag those
   separately if you want to rule on them now rather than leave them open.
