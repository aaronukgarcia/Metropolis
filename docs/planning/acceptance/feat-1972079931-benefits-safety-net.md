# FEAT-1972079931: Benefits — social-services safety net so non-earners don't starve

**Mkey:** FEAT-1972079931

**Size:** XXL

**Relates:** FEAT-1972079929 (jobs model — WHO earns a wage; this feature is its complement, WHO doesn't), FEAT-1972079930 (wealth/ownership/inheritance — benefits credit the same `citizen.Wealth` field, and pensioner means-testing may interact with inherited wealth), FEAT-1972079923 (IMF/insolvency — welfare is a named treasury outflow that can push the city into the bailout/administration/decline flow), FEAT-142 (Detroit death spiral — benefits are the safety valve), BUG-391 (diversify-the-tax-base — more dependents drives more tax demand), `engine.citizens` (lifecycle stage, employment state, wealth), `engine.finance` (treasury, ledger, `PostWages`-style crediting), `engine.services` (MOD-033, named as the likely eventual home per FEAT-1972079929's Out-of-Scope note), `data/occupations.json` / `data/balance/jobs_model.json` (sibling data-placeholder convention).

**GR#25:** This feature reaches `engine.finance → engine.citizens` (welfare credit — likely the SAME edge FEAT-1972079929/FEAT-225 already registered for wage crediting, but the Architect must confirm the edge covers a second named-flow, not just wages), a probable NEW edge `engine.citizens → engine.consumption` (benefit income unlocks consumption spend) if not already covered by the wage path, and a probable NEW edge into sentiment/approval (`engine.social` or equivalent) for the welfare-generosity policy lever (INC3). See § GR#25 Edge Audit — **the BA does not register these; the Architect does, before code.**

**Balance regime (GR#15, binding):** every player-felt number (weekly benefit rates, means-test thresholds, taper rates, uprating %) is a **data placeholder pending Aaron's balance pass** — sourced from `data/balance/benefits.json` (proposed), never a hardcoded Go/TS literal. All figures below are marked PLACEHOLDER with a citation; checks are mechanical (does eligibility gate correctly, does the ledger conserve, is it deterministic), never pinned to a specific £ magnitude.

**Status:** `draft-ahead` (BA criteria ahead of build, per the jobs-model precedent).

**Date:** 2026-08-31.

---

## Overview

A city where every non-earner's wealth silently drains to zero is not a simulation of a welfare state, it's a mortality-farm — and it is exactly the FEAT-142 "Detroit death spiral" trigger with the safety valve removed. This feature builds the safety net: a real, ledgered, treasury-funded payment to every citizen who **cannot** earn a wage under the jobs model (FEAT-1972079929), sized so they can still afford consumption (food/utilities) and keep their wellbeing/health from collapsing.

It mirrors the structure of UK working-age and non-working-age welfare: **Jobseeker's Allowance** (unemployed, job-seeking), **Disability/PIP** (cannot work due to health), **State Pension** (retired/over working age), **Student Maintenance** (in education), **Child Benefit** (dependents). Each is a distinct, named treasury→citizen payment, credited into the same `citizen.Wealth` int64 micro-pound field the jobs model already wages into — benefits are just a different *source* of the same money, never a separate currency or shadow ledger.

This is deliberately the mirror image of FEAT-1972079929: that feature answers "who earns a wage," this one answers "who doesn't, and how do they still eat." Both features share `engine.citizens.EmploymentState` as the primary eligibility signal, so the two BA docs must stay consistent — a citizen is in exactly one of {employed-and-waged, non-earner-and-benefited, off-map} at any tick, never both or neither (see § Eligibility Rules, "no double-dip" AC).

---

## 1. Payment-Cycle & Money-Flow Model

### 1.1 Existing cadences (context, not this feature's to change)

Per `money-numbers-real-world.md` and FEAT-1972079929: **PAYE-style wages post monthly** (`finance.PostWages` fires on the month tick). FEAT-1972079923 confirms treasury outflows/inflows are tick-counted (`BAILOUT_DURATION_TICKS = 360` = 1 game-year), never wall-clock. Commercial settlement elsewhere in the codebase is net-90 (referenced in the money-numbers doc's Q2/Q3 backlog) — that cadence is irrelevant here; benefits are a **citizen-facing consumption-affording payment**, not a B2B settlement, so it should track the wage cadence the player already understands, not the commercial one.

### 1.2 Proposed benefits cadence: **monthly, same tick as wages**

**Rationale:**
- **Consistency with the consumption model:** if wages post monthly and household consumption solves monthly (per the money-loop invariant in `money-numbers-real-world.md` §"Hook Sequencing"), a non-earner's benefit must also land before that same month's `PostHouseholdSpend` hook, or they show a false negative-affordability signal for the whole month even though real-world JSA/PIP/pension are paid four-weekly or weekly in the UK.
- **Determinism/perf:** a weekly cadence would need its own tick-alignment inside the monthly hook sequence (`month_tick`, `week_tick` as two clocks) — extra state for no player-visible benefit at this scale (Baseline One doesn't yet model week-level consumption granularity). Monthly keeps benefits inside the SAME hook family as wages (`PostWages` → `PostBenefits` → `PostHouseholdSpend` → `CollectTax`), which is the minimal change to the existing sequencing.
- **UK-realism concession:** real JSA/PIP pay four-weekly, State Pension four-weekly, Child Benefit four-weekly, Student Maintenance termly (3x/year). The weekly **£ figures in § 4 are true to the real cadence for citation purposes**; the game converts every one of them to a **monthly µ£ credit** (weekly × 52 ÷ 12, or the appropriate termly-to-monthly amortisation for maintenance) so the sim's single monthly hook can post all five benefit types without introducing a second clock. This conversion is itself a placeholder decision — flagged as Open Question 1.

### 1.3 Ledger flow & named labels (treasury → citizen)

Reusing the `PostWages`-shaped API (FEAT-1972079929 AC-7's precedent — `finance.PostWages(citizen, wage)`), this feature needs an equivalent `finance.PostBenefit(citizen, benefitType, amount)` (or a single `PostBenefit` taking a benefit-type enum) that:

1. **Debits** the treasury's welfare-outflow ledger line (a *visible budget line*, distinct from wages — see § "Welfare Outflow Budget Line" below).
2. **Credits** `citizen.Wealth` by the exact same `finance.Money` (int64 µ£) amount.
3. **Labels** the transaction with the specific benefit type — **not** a generic `Welfare` label. Proposed exact labels (case-sensitive, mirroring FEAT-1972079930's inheritance-label discipline):
   - `Jobseeker's Allowance`
   - `Disability Benefit` (PIP-equivalent)
   - `State Pension`
   - `Student Maintenance`
   - `Child Benefit`

A single generic `Welfare` label fails the audit-trail bar the wealth/inheritance feature already set (FEAT-1972079930 AC-11) — the player-facing F2 dashboard and any autopsy tooling (FEAT-146 "why did my city die") need to distinguish "pensions ballooned because the city aged" from "JSA ballooned because unemployment spiked," which is only legible with distinct labels.

### 1.4 Conservation invariant (testable equation)

For every citizen `c` eligible for exactly one benefit type `B` in month `m`:

```
treasury_balance(m) = treasury_balance(m-1)
                       − Σ_c BenefitAmount(c, B, m)      // welfare outflow
                       + (other flows: tax, wages, etc. — untouched by this feature)

citizen_wealth(c, m) = citizen_wealth(c, m-1) + BenefitAmount(c, B, m)   // for benefited citizens only
```

Stated as the single testable equation this feature owns (holding all other flows constant, i.e. isolating the welfare hook in a test harness):

```
treasury_debit_this_tick(welfare) == Σ_over_all_benefited_citizens( credit_to_citizen.Wealth )
```

**to the micro-pound, exact, every tick.** No benefit payment may mint money (citizen credited but no treasury debit) or destroy money (treasury debited but citizen not credited, e.g. a payment silently dropped for an ineligible-but-flagged-eligible citizen). This mirrors FEAT-1972079929 AC-7/AC-13 and FEAT-1972079930 AC-8's conservation shape — this feature's tests should literally extend the same `TestMoneyConservationOver120Months`-shaped harness (per the "Verification standards" project convention: mutate the data, don't just check ledger-entry presence).

### 1.5 Welfare Outflow Budget Line (visible, population-scaled)

The BOW description requires "a real WELFARE OUTFLOW from the treasury (a visible budget line that scales with the dependent population)." Concretely:

- `finance` exposes a queryable `WelfareOutflowThisMonth() Money` (or a per-benefit-type breakdown `WelfareOutflowByType() map[BenefitType]Money`), computed as the sum of the month's `PostBenefit` debits.
- This line is displayed on the F2 macro dashboard (mirroring FEAT-1972079929's unfilled-jobs-by-family display convention) as its own budget row, separate from wages, separate from construction spend, separate from debt interest.
- **Scaling with dependent population:** the line is not a fixed constant — it is `Σ_c eligible(c) × benefit_rate(c)`, so a growing population of pensioners/students/unemployed mechanically grows the line without a developer touching a constant. This is the hook that ties into BUG-391 (diversify-the-tax-base): a city with a large elderly population but a narrow tax base (e.g. only income tax, no council tax / corporate tax) will see the welfare line outpace the tax line and trip the FEAT-1972079923 insolvency threshold — which is the intended systemic pressure, not a bug.

---

## 2. Eligibility Rules — Deterministic Decision Table

Eligibility is a **pure function of citizen state at the start of the benefits hook** — `Eligibility(citizen) -> {BenefitType | None}` — never randomised, never order-dependent (see § Determinism below for the map-range-order caution).

### 2.1 Decision table: lifecycle stage × employment state → benefit(s)

Keyed on `engine.citizens.EmploymentState` (the CLOSED 0–5 enum: `EmploymentNone=0` (child/never worked), `EmploymentStudent=1`, `EmploymentEmployed=2`, `EmploymentUnemployed=3`, `EmploymentRetired=4`, `EmploymentOffMap=5`) crossed with `AgeBand` (0–17, 18–34, 35–54, 55–74, 75+) and the two **REQUIRED-FIELD gaps** (disability, carer — see § 2.2):

| EmploymentState | AgeBand | Additional state | Benefit(s) | Notes |
|---|---|---|---|---|
| `EmploymentNone` (0) | 0–17 | — | **Child Benefit** (paid to the parent/guardian — see Open Q 2) | The "child" reading of EmploymentNone. Distinguishing "child" from "adult who never worked" needs AgeBand as the disambiguator (EmploymentNone alone is ambiguous per its own doc-comment: "child / never worked") |
| `EmploymentNone` (0) | 18+ | not disabled, not a carer | **Jobseeker's Allowance** (treated as a non-participating job-seeker; same as Unemployed) | Gap: EmploymentNone doesn't distinguish "never registered as job-seeking" from "genuinely disengaged." Treat as JSA-eligible by default per the safety-net mandate (no citizen falls through) |
| `EmploymentStudent` (1) | any (typically 5–24 in `Stage`) | — | **Student Maintenance** | `Stage` (nursery..university, 0–7) sub-bands the maintenance amount — nursery/primary/secondary pupils are minors and likely fold into Child Benefit instead of Student Maintenance (see Open Q 8); university/sixth-form/technical/adult-ed are the Maintenance-eligible bands |
| `EmploymentEmployed` (2) | any | wage below subsistence floor (low-wage top-up) | **possible partial benefit top-up** (Open Q 3 — Universal-Credit-style taper) | NOT a default AC in INC1/INC2 — flagged as an open design question, see § 9 |
| `EmploymentEmployed` (2) | any | — (normal case) | **none** | Wage already covers the safety net; no double-dip (see § 2.3) |
| `EmploymentUnemployed` (3) | 18–74 | not disabled | **Jobseeker's Allowance** | The FEAT-1972079929 "unfilled-jobs signal" complement: a citizen actively job-seeking but unmatched still needs to eat |
| `EmploymentUnemployed` (3) | any | **disabled** (REQUIRED-FIELD gap) | **Disability Benefit (PIP-equivalent)**, NOT JSA | Disability supersedes the unemployment-driven JSA path — a disabled citizen is not "job-seeking failed to match," they are structurally out of the labour pool |
| `EmploymentRetired` (4) | 55–74, 75+ (typically) | — | **State Pension** | Retired is itself the eligibility signal — no additional means test in INC1 (see Open Q for means-testing against FEAT-1972079930 inherited wealth) |
| `EmploymentOffMap` (5) | any | — | **none** (out of scope) | Off-map commuters hold a real off-map job (FEAT-198 doc-comment: "still a resident... commutes out and back"); they are earning wages off-map, not a benefits case. Confirmed no eligibility |
| any state | any | **carer** (REQUIRED-FIELD gap) | **Carer's Allowance** (a 6th benefit type, or folded into Disability Benefit — Open Q) | The BOW description lists "carers" among the eligible groups but the engine has no carer relationship/flag today (see § 2.2) |
| any state | any | **sick** (temporary, distinct from chronic disability — REQUIRED-FIELD gap) | **Statutory-Sick-Pay-equivalent** or short-term Disability Benefit | The BOW description separately lists "the sick" from "DISABLED" — engine only exposes the continuous `HealthBand` (0 Critical..5 Excellent), which is a mortality/wellbeing signal, not a discrete "temporarily unable to work" flag |

### 2.2 REQUIRED-FIELD gap list (GR#25-style — do not assume; verified against `internal/engine/citizens/types.go` and `citizen.go` as of this doc's writing)

The following fields **do not exist** in `engine.citizens`' current state model (`EmploymentState`, `HealthBand`, `Stage`, `Sex`, `Sector`, `AgeBand`, `IncomeBand`, `Fidelity`, `CellRef` are the full enum surface confirmed by reading `types.go`):

1. **`Disabled` flag** — REQUIRED for Disability/PIP eligibility (§2.1 rows 5, "any state × disabled"). The closed `HealthBand` enum (Critical→Excellent) is a *continuous health/mortality* signal, not a work-capacity flag — a citizen can be `HealthPoor` (band 1) without being unable to work, and conversely a stable chronic disability might sit at `HealthGood` (band 3) health-wise while still being work-incapacitated. **Do not repurpose `HealthBand` as a disability proxy** — that conflates two different real-world concepts and would make the mortality model and the benefits model silently coupled (a HealthBand tuning change for mortality balance would accidentally re-gate disability benefit eligibility). **Recommendation:** a new boolean or small enum field on the citizen record (e.g., `WorkCapability uint8` — 0=full, 1=limited, 2=none — mirroring the UK ESA "support group / work-related activity group" split), assigned at a life event (birth defect roll, accident, chronic-illness onset) — scope TBD by the Architect, likely a small `engine.citizens` extension, not a new module.
2. **`Carer` status** — REQUIRED for Carer's Allowance eligibility (§2.1, "any state × carer"). No relationship field exists linking one citizen to a dependent they care for (parent-child tracking is ALSO a gap per FEAT-1972079930 AC-9's own open question — this feature's carer flag would need to reuse whatever parent-child/household structure that feature ends up adding, or a new one). **Recommendation:** defer carer eligibility to a later increment (INC2/INC3, not INC1) pending FEAT-1972079930's family-tree resolution — do not build a redundant relationship model in this feature.
3. **"Sick" (short-term, distinct from chronic disability)** — REQUIRED to distinguish Statutory-Sick-Pay-equivalent from PIP-equivalent disability. No discrete "temporarily off work due to illness" state exists; only continuous `HealthBand`. **Recommendation:** fold into Disability Benefit for INC1/INC2 (a citizen below a HealthBand threshold receives Disability Benefit at a lower/sick-pay rate) rather than inventing a sixth benefit type on a field that doesn't exist yet — flagged as Open Question 7, not blocking.
4. **"In education" for children (nursery/primary/secondary) vs. Student Maintenance** — `Stage` (0–7: None, Nursery, Primary, Secondary, SixthForm, Technical, University, AdultEd) DOES exist and IS sufficiently granular — this is **not** a gap, but the BA flags it because `Stage` and `EmploymentState` can be inconsistent today (a citizen could in theory be `Stage=StageUniversity` but `EmploymentState=EmploymentNone` if the two fields are set by different code paths) — an AC below (§5) requires a consistency check between the two before this feature ships.

**Summary: 3 REQUIRED-FIELD gaps** (Disabled/WorkCapability, Carer status, Sick/short-term-incapacity), plus one flagged-but-not-blocking cross-field consistency risk (`Stage` vs `EmploymentState`).

### 2.3 No-double-dip rule (deterministic, ordered)

A citizen receives **at most one** benefit type per month (or zero, if employed-and-waged with no top-up). Precedence order (deterministic, never map-iteration-derived):

1. Employed + wage ≥ subsistence floor → **no benefit** (default; wage suffices)
2. Employed + wage < subsistence floor → **top-up only, if Open Q 3 is ruled in** (else: no benefit, out of scope for INC1)
3. Disabled (once the field exists) → **Disability Benefit**, regardless of EmploymentState (overrides JSA/Retired if both would otherwise apply — a disabled retiree gets whichever is HIGHER per Open Q 6, not both)
4. Retired → **State Pension**
5. Student (EmploymentState=1, Stage≥SixthForm) → **Student Maintenance**
6. Child (AgeBand 0-17, Stage < SixthForm or EmploymentState=None) → **Child Benefit**
7. Unemployed / EmploymentNone-adult → **Jobseeker's Allowance**
8. OffMap → **none**

This precedence list itself must be **data-defined** (e.g. `data/balance/benefits.json`'s `eligibility_precedence: [...]` array), not a hardcoded Go `switch` fall-through chain with implicit ordering — per this project's AC-writing convention (FEAT-1972079929 AC-1's "no hardcoded family assignment" precedent) and to keep it balance-pass-editable without a code change.

---

## 3. Real-World £ Figures Table (ALL PLACEHOLDER)

Weekly UK figures (2025–2026 rates, the same vintage as `money-numbers-real-world.md`), converted to sim µ£ per the monthly-payment-cycle decision in § 1.2 (weekly × 52 ÷ 12 ≈ weekly × 4.333):

| Benefit Type | UK weekly figure (real-world) | Citation | Monthly £ (×4.333) | Sim µ£/month (×1,000,000) | Named constant proposal |
|---|---|---|---|---|---|
| Jobseeker's Allowance | ~£90.50/week (single adult 25+, new-style JSA, 2025–2026 rate) | DWP benefit rates, "New Style Jobseeker's Allowance," uprated annually each April | ~£392/month | 392,000,000 µ£ | `jsaMonthlyMicropounds` |
| Disability Benefit (PIP-equivalent) | ~£72.65–£184.30/week (PIP daily living + mobility components combined, standard→enhanced rate span) | DWP "Personal Independence Payment" rate tables 2025–2026 | ~£315–£799/month (use mid-band ~£450/month as the single-rate placeholder pending a tiered model) | 450,000,000 µ£ (single-tier placeholder) | `disabilityBenefitMonthlyMicropounds` |
| State Pension | ~£221.20/week (full new State Pension, 2025–2026, triple-lock uprated) | DWP "New State Pension" full rate | ~£959/month | 959,000,000 µ£ | `statePensionMonthlyMicropounds` |
| Student Maintenance | ~£120–£180/week equivalent (Student Finance England maintenance loan, non-London rate, averaged over a 39-week term year then amortised across 12 months) | Student Finance England maintenance loan tables 2025–2026 (termly disbursement, NOT weekly in reality — converted here for the single monthly hook, see § 1.2 caveat) | ~£475–£650/month (placeholder mid-point ~£550/month) | 550,000,000 µ£ | `studentMaintenanceMonthlyMicropounds` |
| Child Benefit | ~£25.60/week (eldest/only child, 2025–2026 rate; ~£16.95/week for additional children) | HMRC "Child Benefit" rates 2025–2026 | ~£111/month (eldest child); ~£73/month (additional children) | 111,000,000 µ£ (first child); 73,000,000 µ£ (subsequent) | `childBenefitFirstChildMonthlyMicropounds`, `childBenefitAdditionalChildMonthlyMicropounds` |

**All figures above are PLACEHOLDER pending Aaron's balance pass (GR#15).** They must live in `data/balance/benefits.json` (proposed, siblings `data/balance/jobs_model.json`), never as Go/TS literals, and must be:
- **YoY-uprateable** exactly like `S_base`/`inflation_rate_annual` in FEAT-1972079929 § "Config Page" — an `annual_uprating_pct` per benefit type (or one shared rate), applied at the year boundary (`month % 12 == 0`), never mid-month.
- **Config-page editable** — extending the same F8/F9 Config screen FEAT-1972079929 AC-10 specifies, with a new "Benefits" section listing the five (or more) rates plus the uprating slider.

**Confidence note (mirroring `money-numbers-real-world.md`'s confidence-labelling convention):** JSA/Pension/Child Benefit rates are HIGH confidence (published DWP/HMRC statutory rates, updated annually, easy to verify). PIP is MEDIUM (it's genuinely two components with multiple tiers in reality; this doc collapses it to one placeholder rate, flagged as Open Question 5). Student Maintenance is MEDIUM-LOW (real payment is termly not weekly/monthly, and varies hugely by household income means-testing and whether living at home — the weekly-equivalent figure above is a rough amortisation for citation purposes only).

---

## 4. Safety-Net / Anti-Starvation Mechanic

### 4.1 The mechanic

Without this feature: `EmploymentState ∈ {None(adult), Unemployed, Retired}` (and, once the field lands, `Disabled`) → zero wage → zero income → `citizen.Wealth` trends to 0 as consumption (utilities/food, per `money-numbers-real-world.md` § "Monthly Household Utility Consumption") debits it every month with no offsetting credit → the citizen cannot afford consumption → (per whatever consumption-gating exists — likely a wellbeing/health penalty for unmet consumption, and per FEAT-142's death-spiral shape) wellbeing/health declines → mortality hazard rises (via the `HealthBand`-driven mortality modifier already in `types.go`'s `MaxHealthBand` doc-comment: "worse band ⇒ higher hazard") → the death spiral self-reinforces (fewer earners → less tax base → more strain on remaining services → more non-earners, per BUG-391's diversify-the-base logic).

**With this feature:** every non-earner in an eligible state receives a benefit sized to at least cover the **subsistence floor** — defined as the sum of the monthly utility spend per capita (`monthlyUtilitySpendPerCapita` = 50,000,000 µ£, per `money-numbers-real-world.md`) plus a food/essentials placeholder (NOT yet priced in the existing money-numbers doc — flagged as Open Question 4, propose a new `monthlyFoodSpendPerCapita` placeholder, e.g. ~£150/month per UK ONS household food spend data, ≈150,000,000 µ£, pending its own citation pass).

**Subsistence floor (proposed formula):**
```
SubsistenceFloor = monthlyUtilitySpendPerCapita + monthlyFoodSpendPerCapita
                  ≈ 50,000,000 + 150,000,000 = 200,000,000 µ£/month (≈£200/month placeholder)
```

Every benefit type's rate in § 3 is **above** this floor (JSA ≈£392, Pension ≈£959, etc.) — this is intentional: the safety net must clear the floor with margin, not sit exactly at it (a benefit sized to exactly the floor with zero margin would be a knife-edge that any rounding/timing quirk pushes negative). **AC: every benefit rate in `data/balance/benefits.json` must be validated ≥ SubsistenceFloor × a margin factor (placeholder 1.2×) at load time** — a config that would set, say, JSA below the floor should fail a data-validation check (not silently ship a starvation-inducing configuration), mirroring the units-lint / spec-lint mechanical-check convention this project already runs.

### 4.2 Testable ACs for the anti-starvation mechanic

**AC-SN-1 (a benefited non-earner does not starve over a sustained period).**
**Requirement:** A citizen in `EmploymentUnemployed` state, with zero other income, who receives Jobseeker's Allowance monthly and pays the standard per-capita utility+food consumption debit each month, has **non-decreasing** `citizen.Wealth` over 12 consecutive months (assuming benefit rate ≥ subsistence floor and no other spend).
**Check:** (a) Seed a citizen: `EmploymentUnemployed`, `Wealth = 0`. (b) Advance 12 months with the benefits hook enabled and consumption hook enabled. (c) Assert `Wealth(month 12) >= Wealth(month 0)` (non-negative delta) — i.e., the benefit at minimum offsets essential consumption. (d) **Mutation:** disable the benefits hook (simulate "no safety net"); re-run the same 12 months; assert `Wealth(month 12) < Wealth(month 0)` (wealth trends toward zero/negative) — this proves the test can actually fail, and proves the safety net is the mechanism preventing the death-spiral trigger, not an artefact of the test setup.

**AC-SN-2 (benefit rate is validated against the subsistence floor at data-load time).**
**Requirement:** `data/balance/benefits.json` is rejected at load (registry-sourced error, GR#7) if any benefit rate is below `SubsistenceFloor × margin_factor`.
**Check:** (a) Load a config where JSA = 100,000,000 µ£ (below floor of ~200,000,000 × 1.2 = 240,000,000). Assert load fails with a specific error code (not a silent clamp, not a panic). (b) Load a config where JSA = 300,000,000 µ£ (above the margin). Assert load succeeds. (c) **Mutation:** remove the validation check; assert (a) now silently succeeds — proving the check is load-bearing, not decorative.

**AC-SN-3 (interaction with the FEAT-142 death spiral — benefits reduce, not eliminate, spiral risk under fiscal stress).**
**Requirement:** Per § "IMF/insolvency" relation (FEAT-1972079923): if the welfare outflow line grows faster than the tax base (e.g., a city ages into a pensioner-heavy population with a narrow tax base per BUG-391), the treasury itself can go insolvent — the safety net protects **individual citizens** from starvation but does **not** protect the **city** from fiscal collapse. This is intentional systemic tension, not a bug: the player must balance generosity (§ INC3 policy lever) against solvency.
**Check:** (a) Seed a city with a high dependent-to-earner ratio and a narrow tax base (income tax only). (b) Advance N months. Assert the welfare outflow line rises (population-scaled, § 1.5) while treasury balance falls faster than a control run with a broader tax base. (c) Assert that when treasury crosses `DEBT_THRESHOLD_FOR_BAILOUT` (FEAT-1972079923's threshold), the bailout flow triggers exactly as FEAT-1972079923 specifies — this feature does NOT special-case welfare spend as bailout-exempt (welfare debits count toward insolvency like any other outflow, unless Aaron rules otherwise — see Open Question 9). (d) **Mutation:** exempt welfare from the insolvency calculation (route it around `TotalCirculation()`); assert the city never goes insolvent regardless of population aging — this proves the test can detect an accidentally-uncounted outflow (the classic "silent-fail insolvency" class FEAT-1972079923 was built to close, BUG-396).

---

## 5. Increment Breakdown

Each increment is independently shippable, GR#23-rounded (Destructive verdict required, independent-attacker per the amendment), and gated by its own test suite.

### INC1 — Eligibility + a single subsistence benefit (proves the mechanic works at all)

**Scope:** Implement `Eligibility(citizen) -> BenefitType|None` for the SIMPLEST slice: unemployed adults only (`EmploymentUnemployed`, AgeBand 18–74), receiving a single generic "Subsistence Benefit" (a stand-in for JSA, before the full 5-type taxonomy lands in INC2). Wire `finance.PostBenefit` reusing the `PostWages`-shaped API. No config page yet (rate is a fixed data placeholder). No policy lever yet.

**AC-1.1 (eligibility is deterministic and data-driven).** `Eligibility()` for an `EmploymentUnemployed` 18–74 citizen with no other flags returns the Subsistence Benefit type; for an `EmploymentEmployed` citizen returns None. Precedence table (§ 2.3) is loaded from data, not hardcoded. Check: unit test both cases; grep for hardcoded `"unemployed"` string literals outside JSON-loading code (mirrors FEAT-1972079929 AC-1's check style).

**AC-1.2 (benefit posts monthly via `finance.PostBenefit`, ledger-labeled).** On the month tick, every eligible citizen is credited exactly the Subsistence Benefit rate; the ledger shows a `Jobseeker's Allowance`-labeled (or `Subsistence Benefit` for this increment) entry per citizen (or aggregated per-tick with per-citizen breakdown queryable). Check: seed N unemployed citizens, advance one month, assert each `Wealth` increased by exactly the rate, assert the ledger has the matching debit/credit pair, assert conservation (§ 1.4 equation) holds exactly to the micro-pound.

**AC-1.3 (anti-starvation, the mechanic's raison d'être).** This is § 4.2's AC-SN-1, scoped to INC1: over 12 months, a benefited unemployed citizen's `Wealth` does not trend to zero (with the mutation-disable-hook check proving the test can fail).

**AC-1.4 (no double-dip: employed citizens are never also benefited).** An `EmploymentEmployed` citizen never receives a Subsistence Benefit credit in the same month as a wage credit. Check: seed one employed, one unemployed citizen; advance a month; assert exactly one of the two receives the benefit credit, the other receives only the wage credit, and there is no month where both apply to the SAME citizen at once — extend to a citizen who transitions employed→unemployed mid-simulation and assert benefit crediting starts only from the month after the transition is recorded (no overlap, no gap longer than one tick).

**AC-1.5 (determinism).** Replay the same city snapshot twice; benefit eligibility and amounts are byte-identical both runs. No `time.Now`/`Math.random()` in the eligibility or crediting path (grep check, mirroring FEAT-1972079929 AC-4/AC-12's check style).

### INC2 — The five distinct UK benefit types, real figures, config-page editing

**Scope:** Replace the INC1 stand-in with the full taxonomy (§ 2.1's decision table): JSA, Disability Benefit (using the HealthBand-threshold fallback per § 2.2 item 3 if the `WorkCapability` field is not yet landed — flagged dependency), State Pension, Student Maintenance, Child Benefit. Wire the § 3 real-figures table into `data/balance/benefits.json`. Extend the F8/F9 Config screen (FEAT-1972079929 AC-10's pattern) with a "Benefits" section: per-type rate sliders + a shared (or per-type) annual uprating %.

**AC-2.1 (all five benefit types are data-defined with distinct ledger labels).** `data/balance/benefits.json` carries all five rates + labels; no benefit type or label string is hardcoded in a Go/TS `switch`. Check: mirrors FEAT-1972079929 AC-1(a)-(d)'s rename/mutate-the-JSON check style — rename a label in JSON, re-run, assert the new label appears in the ledger.

**AC-2.2 (eligibility precedence, § 2.3, resolves correctly across overlapping conditions).** A citizen who is both Retired AND (once the field exists) Disabled receives exactly one benefit per the precedence rule (§ 2.3 item 3, "whichever is higher" per Open Q 6 — until that's ruled, default to the FIRST-matching rule in the data-defined precedence array, and this AC pins that default behaviour so it's testable now rather than left ambiguous). Check: table-driven test over every AgeBand × EmploymentState × {disabled, not-disabled} combination in § 2.1's decision table, asserting exactly one (or zero) benefit type results, never two.

**AC-2.3 (Child Benefit paid per-child, first-child vs additional-child rate).** A household with 3 children under the child-benefit-eligible age receives `childBenefitFirstChildMonthlyMicropounds` for one child and `childBenefitAdditionalChildMonthlyMicropounds` × 2 for the other two, credited per § Open Question 2's ruling on recipient (parent vs child wealth). Check: seed a 3-child household, advance a month, assert the exact tiered sum lands in the correct recipient's `Wealth`.

**AC-2.4 (config page: rate edits + annual uprating apply at month/year boundary, never mid-cycle).** Mirrors FEAT-1972079929 AC-10 exactly: edit a benefit rate mid-month, assert it does NOT apply until the next month tick; set an annual uprating %, advance 11 months (no change), advance to month 12 (year boundary), assert rates uprate by the configured %.

**AC-2.5 (subsistence-floor validation, § 4.2 AC-SN-2, extended to all five types).** Every one of the five rates (not just JSA) is validated ≥ SubsistenceFloor × margin at load AND at config-edit time (a player edit that would drop a rate below the floor is rejected with explicit feedback, mirroring FEAT-1972079923 AC-9's "cannot-afford feedback, never silent no-op" convention).

**AC-2.6 (conservation extended across all five types simultaneously).** Extend the INC1 conservation test to a city with citizens in all five eligible categories at once; assert `Σ treasury_debit == Σ citizen_credit` exactly, per benefit type AND in aggregate.

### INC3 — Policy lever (welfare generosity) + approval/sentiment coupling

**Scope:** A single "Welfare Generosity" policy lever (placeholder range, e.g. -5 austerity ... +5 generous, mirroring FEAT-1972079929's Generosity-slider shape but this is a DIFFERENT lever — see Open Question 10 on whether these should actually be the SAME slider or must stay distinct per the FEAT-1972079929 D-9 precedent of keeping player-input sliders and derived-sentiment signals separate). Austerity (-ve) scales all benefit rates down (budget relief) and lowers approval/wellbeing; generosity (+ve) scales rates up (budget strain) and raises approval/wellbeing.

**AC-3.1 (generosity lever scales all benefit rates by a data-defined multiplier).** Setting the lever to -5 (max austerity) reduces every benefit rate by a data-defined % (placeholder, e.g. 30% cut at -5); setting it to +5 increases rates by a data-defined % (placeholder, e.g. 20% boost at +5). Check: unit test the multiplier curve at several lever positions; assert monotonicity (more austere → strictly lower or equal rates, never non-monotonic).

**AC-3.2 (austerity reduces the welfare outflow budget line, measurably).** Set the lever to austerity; advance a month; assert `WelfareOutflowThisMonth()` (§ 1.5) is lower than the same city at the neutral/default lever setting. This is the "budget relief" half of the design ruling's austerity description.

**AC-3.3 (austerity/generosity moves approval/sentiment in the expected direction).** Setting the lever to austerity, holding population and all else constant, causes the city's approval/sentiment metric (whichever `engine.social`-equivalent signal exists — see GR#25 audit item) to move measurably downward over a sustained period (not just a one-tick blip); generosity moves it upward. Check: run two parallel simulations (austerity vs. generous) from the same seed/state, diverging only on the lever; assert the sentiment trajectories diverge in the expected direction by a statistically detectable margin (not asserting a specific magnitude — per GR#15's "directional, not pinned" convention).

**AC-3.4 (the lever is a genuine trade-off, not a free lunch — both directions have a real cost).** Austerity: assert treasury health improves (or degrades slower) AND approval degrades. Generosity: assert approval improves AND treasury outflow rises (moving the city closer to the FEAT-1972079923 insolvency threshold, all else equal). Neither direction is strictly dominant — check: no test configuration shows BOTH treasury health and approval improving simultaneously from a lever move in either direction (that would indicate a broken/one-sided implementation).

**AC-3.5 (determinism, replay-stable, same shape as INC1/INC2 checks).** Lever position is engine state (part of the save), not a runtime-only UI toggle; a replay from a checkpoint reproduces identical benefit amounts, welfare outflow, and sentiment trajectory for a fixed lever-position history.

---

## 6. GR#25 EDGE-AUDIT (for the Architect — NOT registered here)

Every cross-module interaction this feature needs, with a call on whether it likely needs NEW `code.json`/`master-plan-v2.1.json` registration or can reuse an edge already confirmed to exist (per the grep of `code.json` performed for this doc, `engine.finance ↔ engine.citizens` edges already exist in multiple places — likely covering FEAT-1972079929's wage-crediting path):

| # | Edge | Direction | Likely status | Notes for Architect |
|---|---|---|---|---|
| 1 | `engine.finance → engine.citizens` (welfare credit, `PostBenefit`) | finance writes citizen.Wealth | **PROBABLY REUSES** the existing wage-crediting edge (same shape as `PostWages`) — confirm the registered edge's contract covers a SECOND named-flow (benefits) or only literally covers `PostWages`; if the contract is function-signature-specific, a new registered method/edge may be needed even if the module-pair edge exists | Check `code.json` around the `engine.finance`/`engine.citizens` edge entries found near lines 995, 2459, 3327, 3635, 4626, 4744, 5209/5214, 5466, 7143 (this doc's grep) for contract specificity |
| 2 | `engine.citizens` eligibility ← lifecycle stage/employment state | citizens reads its own state (no cross-module edge, but the BENEFITS module/feature reading `EmploymentState`/`Stage`/`AgeBand` from citizens IS a new consumer) | **NEW edge**, `<benefits module> → engine.citizens` (read) | Whatever module/package houses the benefits logic (likely `engine.services` per FEAT-1972079929's own Out-of-Scope pointer, or a new `engine.benefits`) needs a registered read-edge into citizens' employment/lifecycle fields |
| 3 | `engine.citizens → engine.finance` (welfare outflow visible/queryable) | citizens' non-earner population feeds the finance module's `WelfareOutflowThisMonth()` sizing | **NEW edge** (or folds into #1 if bidirectional) | The "scales with dependent population" requirement (§1.5) means finance needs to query citizens' population-by-eligibility-category counts, not just receive individual credit calls |
| 4 | consumption ← benefit income | Whatever module resolves household consumption/affordability (referenced but not named precisely in the available docs — likely `engine.households` or the webconsole `fiscal.ts`/`consistency.ts` dogfood-side equivalent) needs to treat benefit-credited `Wealth` identically to wage-credited `Wealth` | **LIKELY NO NEW EDGE** if consumption already reads generic `citizen.Wealth` (source-agnostic) — confirm this is true; if consumption logic special-cases "wage income" vs. "other wealth," a new edge/contract change is needed | Verify in `engine.households`/`engine.finance`'s consumption-solve code whether wealth-source is ever distinguished |
| 5 | sentiment/approval ← welfare generosity (INC3 only) | the policy-lever module (wherever `engine.social`'s Civic Sentiment epic — FEAT-218-224 per Vestige memory — lives) needs to read the welfare-generosity lever position and/or the welfare-outflow trend | **NEW edge**, `<benefits module> → engine.social` (or the sentiment module's actual key) | Confirm against the Civic Sentiment epic's registered edges (FEAT-218-224, "extends MOD-034 not a new calculus" per Vestige) — this feature's INC3 lever may need to plug into an ALREADY-registered generosity-like input rather than adding a parallel one |
| 6 | jobs model ← unemployment eligibility | benefits' JSA eligibility reads `EmploymentUnemployed`, which FEAT-1972079929's job-assignment algorithm sets/clears | **LIKELY NO NEW EDGE** — both features read the SAME `engine.citizens.EmploymentState` field; this is not a jobs-model → benefits direct call, it's both features being downstream consumers of citizens' state | Confirm there's no need for benefits to call INTO the jobs-model package directly (it shouldn't — eligibility is citizens-state-only per § 2, not a cross-call into job-assignment internals) |
| 7 | wealth/inheritance ← pensioner means-testing (if Open Question 6 rules means-testing IN) | benefits eligibility reading a citizen's TOTAL wealth (including inherited amounts from FEAT-1972079930) to decide whether to reduce/deny State Pension | **CONDITIONAL NEW edge**, only if Aaron rules means-testing in (§ Open Question 6) | Do not build this edge speculatively — GR#25 explicitly bans "hand-written, unregistered speculative dependency prose"; this row exists ONLY to flag the conditional, not to assert the edge is needed today |

**Architect action required:** confirm/register edges #2, #3, #5, and conditionally #7 in `master-plan-v2.1.json` → regenerate `code.json` BEFORE any INC1+ code lands, per GR#25's "hand-written, unregistered speculative dependency prose is an instant fail-closed build rejection" rule. Edge #1 and #6 are probably already covered; edge #4 needs a quick confirmation read of the consumption code, not a new registration.

---

## 7. Determinism + Conservation AC Section

**AC-DET-1 (byte-identical replay).** A city snapshot with a mixed population (employed, unemployed, retired, student, child citizens) run for 100 ticks, checkpointed, and replayed from the checkpoint for 100 more ticks produces byte-identical eligibility decisions, benefit amounts, and ledger entries on every tick, whether run continuously or resumed from checkpoint. Mirrors FEAT-1972079929 AC-12 / FEAT-1972079930 AC-7's exact check shape.

**AC-DET-2 (treasury-debit == citizen-credit, to the µ£, every tick, aggregated across all benefit types).** Per § 1.4's equation, extended across a full mixed population. No float arithmetic anywhere in the benefit-amount calculation path (rate lookups, taper/means-test math if Open Q 3/6 land, uprating math) — all `finance.Money` int64 µ£, using `num.SatAdd`/`satAddMoney` for overflow safety (mirroring FEAT-1972079929 AC-7(c)'s overflow-guard check: a pathological config — e.g., 10M citizens all drawing max-rate benefits simultaneously — must fail closed with a registry error, not silently wrap).

**AC-DET-3 (deterministic eligibility ordering — the map-range-order class, explicitly).** Per the project's own documented gotcha (Vestige: "Map-range-with-break gotcha... two attackers' greps missed it"), the eligibility computation and per-tick benefit-posting loop MUST iterate citizens in a fixed sorted order (by citizen ID, never raw Go/TS object/map iteration) wherever the iteration order could affect a shared/aggregate outcome (e.g., if a budget cap or rate-limiting mechanism is ever introduced in a later increment — not planned for INC1-3, but the ordering discipline must be in place from INC1 so a later increment doesn't retrofit non-determinism). Check: `grep -rn "for .* := range"` over the benefits-posting loop confirms sorted-slice or pre-sorted iteration, mirroring FEAT-1972079929 AC-4(b)'s check.

**AC-DET-4 (seed source for any randomised element — none expected, but if a future increment adds e.g. a randomised disability-onset roll, it must be state-derived).** If/when the `WorkCapability`/disability field (§ 2.2 gap 1) is populated via a probabilistic life-event roll (birth defect, accident), that roll's seed MUST be `hash(citizen_id || tick)` or equivalent, never `time.Now`/unseeded `Math.random()` — flagged here even though it's out of THIS feature's direct scope (it belongs to whichever increment adds the `WorkCapability` field) because a benefits-eligibility test suite would silently pass/fail non-deterministically if that upstream field generation is non-deterministic.

---

## 8. Out of Scope

- **The `WorkCapability`/disability field itself, and the carer-relationship field itself.** This feature CONSUMES those fields once they exist; building them is a citizens-module extension, scoped separately (see § 2.2's REQUIRED-FIELD gaps — the BA is not authorised to assume they exist, and is not scoping their implementation here).
- **Household/family-tree resolution for Child Benefit recipient routing.** Depends on FEAT-1972079930's own open parent-child-tracking question (its AC-9/Open-Q-1) — this feature's Open Question 2 explicitly defers to whatever that feature lands.
- **Means-testing of State Pension against inherited/accumulated wealth.** Flagged as Open Question 6 — not built until ruled.
- **Universal-Credit-style taper for low-wage top-ups.** Flagged as Open Question 3 — not built in INC1-3.
- **The specific PIP two-component (daily living + mobility) tiering.** INC2 collapses PIP to a single rate placeholder; a tiered model is a future refinement (flagged in § 3's confidence note).
- **Regional benefit-rate variation (London weighting, etc.).** Out of scope, mirroring FEAT-1972079929's "no per-district wage adjusters" precedent — flat rate city-wide for Baseline One.
- **Fraud/eligibility-gaming mechanics** (a citizen falsely claiming a benefit type). Eligibility is engine-computed from true state, not player-declared — no fraud surface exists by construction.
- **Integration with the Go engine's `engine.finance`/`engine.citizens` packages if this lands webconsole-side first** (per the FEAT-1972079923 precedent of a TS-side `fiscal.ts`/`engine.ts` stub landing before Go-engine wiring) — the Architect must rule whether this feature targets the Go engine, the webconsole dogfood TS sim, or both in parallel; this doc writes ACs generically enough to apply to either, but the increment plan assumes ONE target is chosen first.

---

## 9. Open Design Questions for Aaron

1. **Payment-cycle conversion factor.** § 1.2 proposes monthly benefit posting (weekly UK figure × 52 ÷ 12) to align with the existing monthly wage/consumption cadence. Is monthly acceptable, or does Aaron want a true weekly/four-weekly cadence with its own sub-month clock (more real-world-accurate, more implementation cost)?

2. **Child Benefit recipient: parent or child's `Wealth`?** Real-world UK Child Benefit pays the parent/guardian, but the sim may not yet have a clean "guardian of this child citizen" pointer (depends on FEAT-1972079930's parent-child tracking, itself open). Does Child Benefit credit the PARENT's wealth (realistic, but blocked on parent-child tracking existing) or the CHILD's own `Wealth` field (works today, but a child citizen is not the real-world payee)?

3. **Do benefits taper as earned income rises (partial top-up for low earners)?** § 2.1 flags this as a possible 6th case (Universal-Credit-style). Is this in scope for Baseline One at all, or strictly out of scope until a later balance-and-depth pass (per the Northstar's phased "watchable → dogfood → convergence → depth → balance" waypoints)?

4. **Food/essentials spend placeholder.** § 4.1 proposes a `monthlyFoodSpendPerCapita` (~£150/month placeholder, NOT yet in `money-numbers-real-world.md`) to complete the subsistence floor alongside the existing utility figure. Should this be added to that document as a sixth real-world-grounded figure (with its own ONS citation pass), or does Aaron want a different floor definition (e.g., utilities only, no separate food line)?

5. **PIP single-rate vs. tiered (daily living / mobility, standard / enhanced).** § 3 collapses PIP to one placeholder rate for INC2. Is a single rate acceptable for Baseline One, or does Aaron want the real two-component tiering modeled from the start (meaningfully more complex eligibility/data-shape)?

6. **Are pensioners' benefits means-tested against inherited wealth from FEAT-1972079930?** Real UK State Pension is NOT means-tested (it's contribution-based), but the BOW description's "safety net" framing raises the question of whether a citizen who inherited £2M (per FEAT-1972079930's inheritance mechanic) should still draw a full State Pension, or whether the sim wants a means-test departure from real-world policy for gameplay/balance reasons. Also determines the precedence question in § 2.3 item 3 (disabled + retired: "whichever is higher" needs a means-test-aware comparison if this is ruled in).

7. **Does "the sick" (short-term illness) need its own benefit type, or fold into Disability Benefit?** § 2.2 gap 3 proposes folding sick citizens into the Disability Benefit path (using a `HealthBand` threshold as a stand-in) rather than inventing a distinct short-term "Statutory Sick Pay" type on top of a field that doesn't exist yet. Confirm this simplification is acceptable, or rule that a genuine short-term-incapacity field/benefit type is needed even in INC1/INC2.

8. **Does a school-age child (Stage = Nursery/Primary/Secondary) receive Child Benefit, and does a Sixth-Form/Technical/University student receive Student Maintenance instead, with the crossover exactly at Sixth Form — or does Aaron want a different Stage-to-benefit-type cutover?** § 2.1's table proposes Sixth-Form-and-above = Student Maintenance, below = Child Benefit, but this is the BA's inference, not an explicit ruling.

9. **Does welfare spend count toward the FEAT-1972079923 insolvency/bailout threshold like any other treasury outflow, or is it protected/exempt (e.g., "core services can't be the reason the city goes bankrupt")?** § 4.2 AC-SN-3 assumes NO special exemption (welfare is just another outflow), which creates the intended demographic-fiscal tension (aging population + narrow tax base → real insolvency risk) — confirm this is the intended design, not an oversight.

10. **Is the INC3 "Welfare Generosity" lever the SAME control as FEAT-1972079929's "Generosity" (wage-desirability) slider, or a genuinely separate lever?** FEAT-1972079929 D-9 already flags Generosity-slider ambiguity (player input vs. derived sentiment output) as unresolved in that feature's own Open Questions. This feature must not silently create a SECOND overloaded "Generosity" concept — either (a) reuse the same resolved lever once FEAT-1972079929's Open Q 5 is ruled, with welfare rates as an ADDITIONAL effect of that one lever, or (b) introduce a genuinely distinct "Welfare Generosity" lever with its own name to avoid the collision. Aaron's ruling on FEAT-1972079929 Open Q 5 should settle this too — flagged here so INC3 doesn't get built against a still-open upstream ambiguity.

---

*Acceptance criteria authored for FEAT-1972079931 (2026-08-31), following the house format and cross-referencing conventions established by FEAT-1972079929 (jobs model) and FEAT-1972079930 (wealth/inheritance). Aaron's design ruling (reproduced in the BOW item, quoted at the top of this document) is the authoritative source; this document's job is to make it testable, flag every real gap honestly, and never assume a field or edge exists without checking the actual code.*
