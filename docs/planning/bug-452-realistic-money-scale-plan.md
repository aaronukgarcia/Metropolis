# BUG-452 — Move to a Realistic GBP Money Scale (PLAN ONLY)

**Status:** plan-first, per Aaron's 2026-09-01 ruling (BOW-452 comment, bro 09:09:03): *"move to a REALISTIC GBP money scale (retire the 1000x ledger divisor hack). This re-bases every money number across the webconsole TS sim AND the Go engine ... must span hamlet->megacity ... PLAN-FIRST ... get Aaron's approval BEFORE executing the rebasing."*

**No production code is changed by this document.** It is the inventory + proposal Aaron asked for. Execution is a separate, later set of commits (Section 6), each individually rounded (GR#23) and CI-verified (GR#28).

---

## 0. Background — how we got here

- **money-numbers-real-world.md** (`docs/planning/money-numbers-real-world.md`, commit `632971c`, 2026-08-31) gave real UK-grounded absolute £ figures for utility spend, rent, migrant wealth, wage, council tax — all correctly converted to micropounds (1 £ = 1,000,000 µ£, `internal/foundation/det/money.go:18`).
- **money-circ inc1** (`a242066`, `internal/engine/compose/moneycirc.go`) tried to *post* those real absolute figures onto the Go engine's ledger and hit two walls documented in the file itself (`moneycirc.go:40-64`, `98-124`):
  1. Baseline-One's seed treasury (`initialTreasury = 10_000_000` micropounds = **£10**, `compose.go:48`) and seed population (`seedCitizenCount = 64`, `compose.go:46`) are three-plus orders of magnitude smaller than the real figures assume.
  2. Posting the real absolute amounts directly instantly overdrafted every ledger transaction and, when tried for rent, collapsed population 64→4 over 12 months (RentBurdenOf pegged at ~100% permanently).
- The workaround: `ledgerScaleDivisor = 1_000` (`moneycirc.go:67`) scales ledger-facing amounts down 1000x, and a hand-tuned flat placeholder rent (`baselineOneMonthlyRentMicropounds = 10_000`, `moneycirc.go:124`) was substituted for the real £1,000/month figure. **This is the "1000x divisor" the task refers to** — it is confined to `moneycirc.go`/`moneycirc_inc2.go`, not spread through the engine.
- Aaron's chain of rulings on BUG-452 (all on the BOW item, `node claude-bow.js show BUG-452`):
  1. 2026-08-31 10:15 — scale up the *starting conditions* (real treasury + tile-derived seed population), then remove the divisor and post full real figures directly.
  2. 2026-08-31 12:04 — starting treasury = a round starting grant, **trial figure £10,000,000** (`10,000,000,000,000` micropounds), explicitly balance-pass-tunable.
  3. **2026-09-01 09:09 (this task) — go further: retire the divisor hack entirely, rebase BOTH the webconsole TS sim and the Go engine onto one realistic GBP scale spanning hamlet to megacity (100M citizens).**

So this plan supersedes ruling (2) only in scope (it now also covers webconsole, and drops the divisor rather than tuning around it) — the **£10,000,000 trial starting-treasury figure from ruling (2) is carried forward as this plan's proposed anchor**, not re-derived from scratch, unless Aaron wants to revisit it (see Open Questions).

---

## 1. Inventory — every money constant, file:line, current value

### 1a. Webconsole TS sim (`webconsole/src/sim/`)

**Treasury / starting state**

| Constant | File:line | Value | Notes |
|---|---|---|---|
| `funds` (initial state) | `engine.ts:345` | `10000000` | Starting treasury. Unitless in code — no explicit currency scale comment. Numerically equal to Go's newly-ruled £10M trial figure, but nothing in the TS code names it as pounds or micropounds. |
| `fundsAtTickStart` / `fundsAtTickEnd` (initial) | `engine.ts:361-362` | `10000000` | Mirrors of the above for the conservation checker. |

**Taxation**

| Constant | File:line | Value | Notes |
|---|---|---|---|
| `taxRates` initial | `engine.ts:349` | `{ residential: 9, commercial: 11, industrial: 13 }` | Catalogue-rate units, not £ directly — feeds `councilTaxPerTick`/`businessTaxPerTick` below. |
| `councilTaxPerTick()` | `fiscal.ts:13-15` | `population * taxRate * 2 / 100` | PLACEHOLDER, "2% effective rate per citizen" comment. |
| `businessTaxPerTick()` | `fiscal.ts:21-23` | `commercialZones * taxRate * 0.4` | PLACEHOLDER, "~40% of catalogue rate per zone". |
| `FISCAL_COEFFICIENTS.councilTaxEffectiveRate` | `fiscal.ts:120` | `2` | Same rate, named constant form. |
| `FISCAL_COEFFICIENTS.businessTaxFraction` | `fiscal.ts:122` | `0.4` | ditto. |

**Wages / upkeep**

| Constant | File:line | Value | Notes |
|---|---|---|---|
| `wagesPerTick()` | `fiscal.ts:29-31` | `population * 0.5` | PLACEHOLDER, "directional cost per citizen". |
| `FISCAL_COEFFICIENTS.wagesPerCitizen` | `fiscal.ts:124` | `0.5` | ditto. |
| `UPKEEP_BUCKET` (per-ZoneKind outflow labels) | `fiscal.ts:93-115` | n/a (labels only) | Actual upkeep values live per-building in `data.ts` catalogue (`P(...)` 7th positional arg — see below), not here. |
| `RECYCLING_DISCOUNT_LABELS` policy multiplier | `fiscal.ts:61-79` | `0.93` | Not a money constant, a rate. |
| austerity multiplier | `fiscal.ts:82` | `0.9` | ditto. |

**Utilities**

| Constant | File:line | Value | Notes |
|---|---|---|---|
| `GRID_EXPORT_TARIFF_PER_MW` | `fiscal.ts:38` | `1.6` | PLACEHOLDER, "£/MW" implied — comment cites "~9,920/tick upkeep over ~6,095 MW basis". |

**Overdraft / insolvency**

| Constant | File:line | Value | Notes |
|---|---|---|---|
| `OVERDRAFT_PER_TICK` | `fiscal.ts:128` | `0.004` | interest rate per tick on negative funds (capped). |
| `DEBT_THRESHOLD_FOR_BAILOUT` | `fiscal.ts:153` | `-10_000_000` | crisis band trigger = −1× starting treasury (10,000,000). |
| `INSOLVENCY_WARNING_THRESHOLD` | `fiscal.ts:162` | `-5_000_000` | warning band = −0.5× starting treasury. |
| `BAILOUT_DURATION_TICKS` | `fiscal.ts:182` | `360` | = `TICKS_PER_YEAR` (`engine.ts:667`), not a money figure but coupled. |
| `BAILOUT_INCOME_INJECTION` | `fiscal.ts:192` | `2_000_000` | = 0.2× starting treasury. |
| `ASSET_SALE_VALUE_FRACTION` | `fiscal.ts:202` | `0.6` | fraction of `placementCost` credited on forced sale. |
| `ADMINISTRATION_DURATION_TICKS` | `fiscal.ts:225` | `360` | = TICKS_PER_YEAR. |
| `SECOND_BAILOUT_DURATION_TICKS` | `fiscal.ts:259` | `360` | = TICKS_PER_YEAR. |
| `BAILOUT_INCOME_INJECTION_SECOND` | `fiscal.ts:269` | `1_000_000` | = 0.1× starting treasury, deliberately lower than the first (worse terms). |
| `FINAL_DECLINE_FUNDS_THRESHOLD` | `fiscal.ts:288` | `0` | game-over bar (funds < 0 at second bailout re-evaluation). |
| existing bulldoze refund fraction (referenced by `ASSET_SALE_VALUE_FRACTION`'s doc comment) | `engine.ts` (`case 'bulldoze'`) | `0.25` | 25% of paid cost. |

**Building capex (catalogue, `data.ts`, `P(id, zoneKind, name, detail, w, h, cost, upkeep, colour, category, order, extras)`)** — representative spread (114 `P(...)` entries total, `grep -c "^  [a-z_]*: P("` = 114):

| Building | File:line | placementCost | upkeep |
|---|---|---|---|
| `road` | `data.ts:1007` | `40` | — |
| `rd_dual` (dual carriageway) | `data.ts:1192` | `320` | `16` |
| `fire_post` | `data.ts:1161` | `1,000` | `16` |
| `farm_orchard` | `data.ts:1017` | `1,000` | `5` |
| `pol_station` | `data.ts:1035` | `2,600` | `34` |
| `ferry_pier` | `data.ts:1072` | `11,000` | `90` |
| `res_highrise` (600 residents) | `data.ts:1080` | `21,000` | `60` |
| `ind_estate` (heavy industrial, ≈18 factories) | `data.ts:1248` | `180,000` | `900` |
| `land_airport` (Heathrow-scale, 1,227ha) | `data.ts:1050` | `450,000` | `3,000` |
| `pow_nuke` (twin AGR, 1,120MW, Dungeness-scale) | `data.ts:1028` | `560,000` | `1,400` |
| `MOTORWAY_JUNCTION_COST` | `data.ts:274` | `250,000` | (flat conversion cost) |
| `RAIL_BRIDGE_COST_MULTIPLIER` | `data.ts:268` | `4` (multiplier on base spec cost) | — |
| `REPLAN_UPGRADE_COST_FRACTION` | `engine.ts:1953` | `0.9` | fraction of new-tier cost charged on auto-scale upgrade |

Full 114-entry catalogue is the exhaustive list; the above is the range (min ~£40 road tile to ~£560K nuclear plant) that anchors the ratio math in Section 5.

**Auto-scale / capacity cost example (comment, `engine.ts:920`):** "~45k); trigger auto-scale -> cost ~6.75k charged" — confirms auto-scale spend is ~15% of a tier's base `sp.cost`.

### 1b. Go engine (`internal/`)

**Base unit**

| Constant | File:line | Value |
|---|---|---|
| `Micropounds` type | `internal/foundation/det/money.go:15` | `int64`, 1 unit = 1e-6 GBP |
| `MicropoundsPerPound` | `internal/foundation/det/money.go:18` | `1_000_000` |
| `code.json` units registry: `money.micropound` (base), `money.penny` (scale 10,000), `money.pound` (scale 1,000,000) | `code.json:44-69` | registered, consistent with `det/money.go` |

**Seed / starting conditions (`internal/engine/compose/compose.go`)**

| Constant | File:line | Value |
|---|---|---|
| `seedCitizenCount` | `compose.go:46` | `64` |
| `initialTreasury` | `compose.go:48` | `10_000_000` µ£ (**£10**) |
| `initialCitizenWealth` | `compose.go:49` | `5_000_000` µ£ (**£5**) |

**Money-circ inc1 constants (`internal/engine/compose/moneycirc.go`)**

| Constant | File:line | Real-world value (µ£) | Ledger-posted value (µ£) |
|---|---|---|---|
| `ledgerScaleDivisor` | `moneycirc.go:67` | — | `1_000` (THE divisor to retire) |
| `monthlyUtilitySpendPerCapitaMicropounds` | `moneycirc.go:72` | `55_000_000` (£55/mo/person) | via `/ledgerScaleDivisor` below |
| `monthlyConsumptionSpendMicropounds` | `moneycirc.go:81` | — | `55_000` (real ÷ 1000) |
| `baselineOneMonthlyRentPerHousehold` | `moneycirc.go:90` | `1_000_000_000` (£1,000/mo) | *not posted* — cited only |
| `baselineOneMonthlyRentMicropounds` | `moneycirc.go:124` | — | `10_000` (hand-tuned flat placeholder, NOT real÷1000 — see doc comment, real÷1000 = £1 which still collapsed population) |
| `councilTaxPerCapitaMicropounds` | `moneycirc.go:130` | `47_000_000` (£47/mo/person) | via `/ledgerScaleDivisor` below |
| `monthlyCouncilTaxMicropounds` | `moneycirc.go:135` | — | `47_000` (real ÷ 1000) |
| `commercialTaxRateBp` | `moneycirc.go:141` | `2000` bp (20%, VAT-grounded) | scale-invariant, unscaled |
| `industrialTaxRateBp` | `moneycirc.go:148` | `2500` bp (25%, UK corp tax) | scale-invariant, unscaled |
| `monthlyWageGrossPerEmployedMicropounds` | `moneycirc.go:155` | `2_100_000_000` (£2,100/mo) | *not ledger-posted* — credited to `citizen.Wealth` (untracked by conservation) at FULL scale |
| `incomeNITaxRateBp` | `moneycirc.go:160` | `2800` bp (28%) | scale-invariant |
| `monthlyWageNetPerCitizenMicropounds` | `moneycirc.go:166-167` | computed `= gross - gross*2800/10000` = `1_512_000_000` (£1,512/mo) | full scale (per-citizen field, untracked) |

**Money-circ inc2 constants (`internal/engine/compose/moneycirc_inc2.go`)**

| Constant | File:line | Real-world value | Ledger-posted value |
|---|---|---|---|
| `commercialPaymentTermTicksReal` / `COMMERCIAL_PAYMENT_TERM_TICKS` | `moneycirc_inc2.go:75,83` | `90` (NET-90 days) | unscaled (tick count, not money) |
| `constructionMaterialPricePerTonneRealMicropounds` | `moneycirc_inc2.go:95` | `150_000_000` µ£ (£150/tonne, UK builders'-merchant blended price) | *not posted* — cited only |
| `constructionMaterialLedgerPriceMicropoundsPerTonne` | `moneycirc_inc2.go:104` | — | `100` µ£/tonne (a **1.5-million-to-one** scale-down from the real figure, not the 1000x divisor — hand-tuned separately because even ÷1000 still overdrafted the £10 seed treasury by 1-2 orders of magnitude, per the file's own doc comment at lines 54-65) |

**Conservation / invariant plumbing (unaffected by the rebase, must stay green):**
- `internal/engine/invariant/money.go:19-26` — `MoneyInvariant`/`StockMoney` wraps a generic `stockCheck` that reads the aggregate `AcctHouseholds` ledger balance each tick; this is scale-agnostic (checks `before == after`, not absolute magnitude) so it is safe across a rebase as long as every ledger-facing post still uses the checked `Add`/`Sub`/`MulRat` helpers in `det/money.go`.
- `internal/foundation/det/money.go:41-93` (`Add`, `Sub`, `MulRat`, `checkedMul64`) — overflow-checked arithmetic already in place; the rebase does not need new overflow guards, it needs the existing ones exercised at the new (larger) magnitudes in CI.

---

## 2. The 1000× divisor — confirmed findings

- **Exact location:** `ledgerScaleDivisor = 1_000` at `internal/engine/compose/moneycirc.go:67`.
- **What it bridges:** the real UK-grounded absolute monthly figures (utility £55, council tax £47 — both in `money-numbers-real-world.md`) vs. the toy seed treasury (`initialTreasury` = £10, `compose.go:48`) and toy seed population (`seedCitizenCount` = 64, `compose.go:46`). It is applied ONLY to the two ledger-facing legs (`monthlyConsumptionSpendMicropounds`, `monthlyCouncilTaxMicropounds`) — NOT to the rent term (hand-tuned flat placeholder instead, because ÷1000 still broke affordability) and NOT to the wage figure (credited off-ledger to `citizen.Wealth`, which the conservation invariant does not track, so it was left at full real scale from day one).
- **A second, separate divisor exists**: `constructionMaterialLedgerPriceMicropoundsPerTonne = 100` vs. real `constructionMaterialPricePerTonneRealMicropounds = 150_000_000` (`moneycirc_inc2.go:95,104`) — a ~1.5-million-to-one scale-down, NOT reusing `ledgerScaleDivisor`, because even ÷1000 still overdrafted the seed treasury (documented at `moneycirc_inc2.go:54-65`).
- **Is removing it safe once both sides use one realistic scale?** Yes, structurally: every consumer of these constants is a plain `finance.Money`/`Micropounds` int64 post through the existing checked-arithmetic helpers (`det.Add`/`Sub`/`MulRat`) and the conservation invariant only checks `before == after` on the aggregate ledger stock, not an absolute magnitude. The divisor's entire reason to exist is the **treasury/population mismatch**, not any arithmetic or determinism constraint — so once `initialTreasury`/`seedCitizenCount` (and the webconsole `funds`/population-implied constants) are re-anchored to a scale where the real absolute figures don't overdraft on day one, `ledgerScaleDivisor` and `constructionMaterialLedgerPriceMicropoundsPerTonne` can both be deleted and every constant in Section 1b's "real-world value" column posted directly. This is exactly execution increment 2 in Section 6.

---

## 3. Realism anchors (PROPOSED — Aaron's approval required before use)

All figures below are either (a) already-approved constants from `money-numbers-real-world.md`/Aaron's BOW rulings, carried forward unchanged, or (b) new anchors this plan proposes with citations, clearly marked NEW.

### 3.1 Starting treasury — Folkestone-start hamlet

- **Carried forward (already ruled 2026-08-31):** Aaron's trial figure is **£10,000,000** (`10,000,000,000,000` micropounds) — "a round starting grant... figure is TBD/trial, maybe start with 10m and see how that goes," explicitly balance-pass-tunable.
- **Grounding context (from CLAUDE.md's own research):** Folkestone & Hythe District Council's actual net revenue budget is ~£20-25M/year — i.e. Aaron's £10M trial figure is already in the right order of magnitude as a *fraction of one year's real council spending power* for a real town of Folkestone's size (pop. ~46,000), which is far larger than the game's opening "hamlet" (seed population target, see 3.2). Treating £10M as a **multi-year capital grant** (not one year's revenue) for a hamlet that will grow into a town/city over the playthrough is a defensible reading.
- **PROPOSAL:** keep £10,000,000 as the confirmed starting-treasury anchor (both engines), superseding the toy `£10` (Go) and clarifying the webconsole's already-numerically-£10,000,000 `funds` initial value (`engine.ts:345`) as **pounds, not an ambiguous unit** — i.e. the webconsole side needs no numeric change here, only an explicit unit doc-comment, while the Go side needs `initialTreasury` changed from `10_000_000` µ£ (£10) to `10_000_000_000_000` µ£ (£10M).

### 3.2 Seed population — hamlet dwelling count

- Go's `seedCitizenCount = 64` (`compose.go:46`) is explicitly flagged low-confidence in `money-numbers-real-world.md` §6 and by Aaron's own ruling ("derive from ACTUAL OS Terrain tile dwelling count, not a guess").
- **This plan does NOT re-derive the tile dwelling count** — that requires a separate investigation into the OS Terrain 50 Folkestone tile data (`internal/foundation/data/`), which is out of scope for a money-scale plan and is its own ruling thread already open on BUG-452. **OPEN QUESTION 1** below asks Aaron whether to fold that derivation into this rebase's execution or keep it a separate ticket.
- For the purpose of this plan's ratio math (Section 5), a **hamlet order-of-magnitude of 50-200 residents** (UK census definition: a hamlet has no services and typically <100-300 population) is used as the illustrative low end; the exact number is Aaron's call per Open Question 1.

### 3.3 Council-tax-per-capita — carried forward, HIGH confidence

- **£47/month per person** = **Band D Folkestone & Hythe council tax ÷ ~3 residents/household** (`money-numbers-real-world.md` §5, `moneycirc.go:130`). No change proposed — already real-world-grounded and already at full scale (only the *posted* amount was divisor-scaled, per Section 2).

### 3.4 Average wage — carried forward, HIGH confidence

- **£2,100/month gross / £1,512/month net** (Kent regional ONS-grounded, `money-numbers-real-world.md` §4, `moneycirc.go:155,166-167`). No change proposed.

### 3.5 Typical building capex — NEW anchors proposed for this plan

| Building type | Real-world citation | Proposed realistic capex |
|---|---|---|
| A primary school | BCIS 2025 rate ~£1,850/m² for a ~3,000m² school → headline construction ~£7.5–8.3M; a real 2025 Kent example (More Park Catholic Primary, West Malling) delivered at **£11M** all-in (Kent County Council, June 2025) | **£8–11M** per new primary school |
| A gas/CCGT power plant | UK CCGT typical major-maintenance capex benchmark ~£50,000/kW = **£50M/MW**; large-format international reference plants run ~$950/kW (~£750/kW ≈ £0.75M/MW) for NEW-BUILD (vs. the £50M/MW figure being a *maintenance* capex event, not full build cost) — figures diverge by two orders of magnitude depending on source, flagged for Aaron's judgement | **£0.75–1.5M/MW installed** for a new-build gas plant (using the new-build international reference, not the maintenance-event figure, as more representative of a "build a power plant" game action) |
| A road (per km, urban dual carriageway) | 2005 UK average dual-carriageway cost ~£12M/mile (~£7.5M/km), inflation-adjusted to 2024-25 (~+60-70% CPI since 2005) → **~£12-13M/km**; large/complex schemes (A465, Lower Thames Crossing) run £40-435M/km but are outliers (tunnelling, mountains) | **£10-15M/km** for a typical new dual carriageway |
| Typical annual upkeep as % of capex | No single UK-wide published ratio found; UK infrastructure asset-management practice commonly budgets **1-3% of replacement capex per year** for routine maintenance (roads, schools, plant) — this is an industry rule-of-thumb, not a specific citation, flagged LOW confidence | **~2% of capex per year** as the default upkeep ratio, tunable per building category |

### 3.6 Scale-span endpoints

- **Hamlet (game start):** treasury ~£10M (3.1), population ~50-200 (3.2).
- **Megacity (100M-citizen design target):** London's GLA Group alone budgets **£22.7 billion/year** (Mayor's draft consolidated budget 2026-27, London Assembly) for ~9M people — scaling proportionally to 100M citizens (≈11x London's population) suggests an annual civic budget on the order of **£100-250 billion/year** at full scale, consistent with the task's own "£100B+/yr" ballpark.

---

## 4. Scale-span + int64 overflow check

- **Base unit:** `Micropounds int64`, 1 £ = 1e6 µ£ (`det/money.go:18`).
- **int64 max:** 9,223,372,036,854,775,807 (~9.22e18).
- **£100B (megacity annual budget, single aggregate figure):** 100,000,000,000 × 1e6 = **1e17 µ£**. Headroom to int64 max: **~92x** — comfortable for any single value (a treasury balance, a year's total budget).
- **Cumulative per-citizen sums at 100M population:** e.g. summing `citizen.Wealth` (currently credited at full real scale, `monthlyWageNetPerCitizenMicropounds` ≈ £1,512/mo → plausible steady-state per-citizen wealth on the order of £5,000-£10,000 if this plan's wage-accrual model is left as-is) across 100M citizens: 100,000,000 × £7,500 × 1e6 = **7.5e17 µ£** — headroom to int64 max: **~12x**. Tighter, but still safe.
- **Combined risk:** if the conservation invariant's aggregate (`StockMoney`, `invariant/money.go`) ever sums treasury + households + firms + external simultaneously at full 100M-citizen, megacity-budget scale, the combined total could approach the **1e18-2e18** range — still under 9.2e18 but with headroom shrinking to single-digit multiples. **Recommendation:** this is NOT an immediate blocker (>9x headroom exists at the design target), but:
  1. Add a CI/determinism-gate test that constructs a synthetic 100M-citizen, megacity-budget worldstate and asserts every `Add`/`Sub`/`MulRat` call still succeeds (exercises the existing overflow guards at the NEW magnitude, not just the old toy one).
  2. Do NOT change the money-conservation invariant's own arithmetic — it already reads through the checked helpers per Section 2's finding.
  3. Micropound precision remains appropriate at megacity scale (1e17 well within int64) — a coarser base unit is **not** recommended; it would only trade overflow headroom for lost sub-pound precision on transactions that still matter at hamlet scale (a £0.05 utility charge), and the headroom margin computed above does not require it.

---

## 5. Mapping table — toy value → proposed realistic value

Ratios Aaron already approved are preserved exactly (marked "ratio-preserved"); everything else is either scale-derived from the new treasury anchor or left as an independent gameplay lever (marked accordingly).

### 5a. Go engine (`internal/engine/compose/`)

| Constant | Current (toy) | Proposed (realistic) | Basis |
|---|---|---|---|
| `initialTreasury` (`compose.go:48`) | `10_000_000` µ£ (£10) | `10_000_000_000_000` µ£ (**£10,000,000**) | Aaron's carried-forward ruling (3.1) |
| `seedCitizenCount` (`compose.go:46`) | `64` | **Open Question 1** (tile-derived, or an interim illustrative hamlet figure ~50-200) | Not derived in this plan |
| `initialCitizenWealth` (`compose.go:49`) | `5_000_000` µ£ (£5) | scale-derived: keep the SAME ratio to `initialTreasury` as today (£5 : £10 = 0.5:1) → **£5,000,000** (5,000,000,000,000 µ£) | ratio-preserved (gameplay-lever proportion, independent of real-world wealth data — the ONS £2.5k median figure is used for MIGRANT wealth, not this seed constant) |
| `ledgerScaleDivisor` (`moneycirc.go:67`) | `1_000` | **DELETE** | Section 2 |
| `monthlyConsumptionSpendMicropounds` (`moneycirc.go:81`) | `55_000` µ£ (real ÷ 1000) | `55_000_000` µ£ (real, full scale, no division) | remove divisor |
| `baselineOneMonthlyRentMicropounds` (`moneycirc.go:124`) | `10_000` µ£ (hand-tuned flat) | `1_000_000_000` µ£ (real £1,000/mo, `baselineOneMonthlyRentPerHousehold`, full scale) — **must be re-tested against the NEW treasury scale for the same emigration-collapse failure mode documented at `moneycirc.go:98-112`; if it recurs, the flat-placeholder technique stays but the flat value scales up proportionally with treasury, not with a re-introduced divisor** | remove divisor / independent gameplay lever (re-verify empirically, GR#15) |
| `councilTaxPerCapitaMicropounds` used amount (`monthlyCouncilTaxMicropounds`, `moneycirc.go:135`) | `47_000` µ£ (real ÷ 1000) | `47_000_000` µ£ (real, full scale) | remove divisor |
| `commercialTaxRateBp` / `industrialTaxRateBp` (`moneycirc.go:141,148`) | `2000` / `2500` bp | **unchanged** | scale-invariant, already real UK rates |
| `monthlyWageGrossPerEmployedMicropounds` / net (`moneycirc.go:155,166-167`) | full scale already | **unchanged** | already real, off-ledger |
| `constructionMaterialLedgerPriceMicropoundsPerTonne` (`moneycirc_inc2.go:104`) | `100` µ£/tonne | `150_000_000` µ£/tonne (real, `constructionMaterialPricePerTonneRealMicropounds`, full scale) | remove divisor |

### 5b. Webconsole TS sim (`webconsole/src/sim/`)

Webconsole's `funds` initial value is ALREADY £10,000,000-equivalent numerically (`engine.ts:345`), so no starting-treasury change is needed there — only a doc comment naming the unit as £ explicitly, and every derived-from-treasury insolvency constant is **ratio-preserved** (they scale automatically since they're defined as multiples of the same base):

| Constant | Current | Proposed | Basis |
|---|---|---|---|
| `funds` initial (`engine.ts:345`) | `10000000` | **unchanged** (10,000,000 = £10M, confirmed unit) | matches 3.1 anchor already |
| `DEBT_THRESHOLD_FOR_BAILOUT` (`fiscal.ts:153`) | `-10_000_000` (−1× treasury) | **unchanged** — ratio (−1×) preserved | ratio-preserved |
| `INSOLVENCY_WARNING_THRESHOLD` (`fiscal.ts:162`) | `-5_000_000` (−0.5× treasury) | **unchanged** | ratio-preserved |
| `BAILOUT_INCOME_INJECTION` (`fiscal.ts:192`) | `2_000_000` (0.2× treasury) | **unchanged** | ratio-preserved |
| `BAILOUT_INCOME_INJECTION_SECOND` (`fiscal.ts:269`) | `1_000_000` (0.1× treasury) | **unchanged** | ratio-preserved |
| `ASSET_SALE_VALUE_FRACTION` (`fiscal.ts:202`) | `0.6` | **unchanged** | fraction, scale-invariant |
| `OVERDRAFT_PER_TICK` (`fiscal.ts:128`) | `0.004` | **unchanged** | rate, scale-invariant |
| `wagesPerTick()` coefficient (`fiscal.ts:29-31,124`) | `population * 0.5` | **rebase to real £/citizen/month, tick-adjusted**: real net wage £1,512/mo ÷ (ticks/month) — **independent lever, needs Aaron's balance-pass sign-off** since it currently reads as a directional placeholder, not a real-anchored figure | independent gameplay lever — see Open Question 3 |
| `councilTaxPerTick()` coefficient (`fiscal.ts:13-15,120`) | `population * taxRate * 2 / 100` | **rebase toward real £47/person/month** (already used verbatim on the Go side) — same tick-adjustment treatment as wages | independent gameplay lever |
| `businessTaxPerTick()` coefficient (`fiscal.ts:21-23,122`) | `commercialZones * taxRate * 0.4` | no direct real-world per-zone citation exists; **keep as an independent gameplay lever**, re-tuned only so its OUTPUT magnitude sits in the same £-per-tick range as the rebased council tax (internal consistency, not a new citation) | independent gameplay lever |
| `GRID_EXPORT_TARIFF_PER_MW` (`fiscal.ts:38`) | `1.6` | **rebase** — needs a real UK wholesale/export electricity tariff citation (not researched in this plan; £/MWh market rates run £50-120/MWh in 2024-25, i.e. this constant's implied units need to be reconciled with real tariffs) | independent gameplay lever — Open Question 4 |
| Building `placementCost`/upkeep catalogue (114 entries, `data.ts`) | £40 (road tile) to £560,000 (nuclear plant) | **rebase entire catalogue against Section 3.5's real capex anchors** (school ~£8-11M, power plant ~£0.75-1.5M/MW × building's MW rating, road ~£10-15M/km × tile length) — this is the single largest execution item, touches all 114 entries | scale-derived from 3.5, needs a dedicated balance pass |
| `MOTORWAY_JUNCTION_COST` (`data.ts:274`) | `250,000` | rescale proportionally with the road catalogue | scale-derived |
| `RAIL_BRIDGE_COST_MULTIPLIER` (`data.ts:268`) | `4` | **unchanged** | multiplier, scale-invariant |
| `REPLAN_UPGRADE_COST_FRACTION` (`engine.ts:1953`) | `0.9` | **unchanged** | fraction, scale-invariant |
| bulldoze refund fraction (`engine.ts`, `case 'bulldoze'`) | `0.25` | **unchanged** | fraction, scale-invariant |

---

## 6. Execution plan (increments — NOT executed by this task)

**Inc0 — Aaron's row-by-row approval of Section 3/5** (this document, plus the open questions in Section 7). Nothing below starts until this lands.

**Inc1 — webconsole constants.** Change the 114-entry `data.ts` capex catalogue (scale-derived from 3.5) and the `fiscal.ts` per-tick coefficients (wages/council-tax/business-tax/grid-tariff — independent levers per Open Questions 3-4). `funds` initial and all insolvency-threshold ratios are UNCHANGED (already at £10M anchor). **Must stay green:** `webconsole/test/fiscal.test.mjs`, `webconsole/test/journal.test.mjs`, `webconsole/test/capture-before-wipe.test.mjs`, `webconsole/test/mount.test.tsx` — plus the conservation checker in `webconsole/src/sim/consistency.ts` (fundsAtTickEnd === fundsAtTickStart + Σinflows − Σoutflows must still hold at the new magnitudes). GR#23 round required (engine/data code, not docs/test-only).

**Inc2 — Go engine constants + divisor removal.** Change `initialTreasury` (£10 → £10M), `initialCitizenWealth` (ratio-preserved), post `monthlyConsumptionSpendMicropounds`/`monthlyCouncilTaxMicropounds`/`constructionMaterialLedgerPriceMicropoundsPerTonne` at full real scale, delete `ledgerScaleDivisor` and `constructionMaterialPricePerTonneRealMicropounds`'s "unused" `var _ =` guard (both become live-used, not just referenced), re-verify the rent-figure emigration-collapse risk empirically at the new treasury scale (`moneycirc.go:98-112`'s documented failure mode — MUST re-run the same regression tests that caught it, e.g. `internal/engine/compose/feat_1972079927_moneycirc_test.go`). **Must stay green:** `go test ./internal/engine/compose/...` (money-circ + emigration-collapse regressions), `internal/engine/invariant` conservation tests, `internal/foundation/det/money_test.go` (overflow-guard tests — extend with a new 100M-citizen/£100B-scale case per Section 4's recommendation), `-race`. GR#23 round required — independent attacker, per the independence amendment (GR#23), since this touches the finance/money core.

**Inc3 — reconcile + regression.** Cross-check the two sides now agree on what "£1" means (webconsole has no explicit micropound type — confirm its raw numbers are being treated as whole pounds consistently everywhere a cross-reference exists, e.g. any future save-file interop between the TS dogfood sim and the Go engine). Full determinism-gate + perf-gate run (GR#21/BUG-034). Update `docs/planning/money-numbers-real-world.md` §6 to mark `initialTreasury`/`seedCitizenCount` confidence as resolved (or still-open per Open Question 1). Update `code.json`'s `units` registry notes if any new unit/scale note is warranted (none expected — base unit is unchanged, only the constants using it are rescaled).

**Cross-file consistency risks (all three increments):**
- The webconsole TS sim and the Go engine are **two independent simulations of the same game concept**, not a shared codebase — nothing mechanically enforces they stay numerically aligned. This plan's mapping table is the only thing keeping them consistent; a future change to one side without the other re-diverges them silently. Recommend a lightweight cross-check test/lint (comparing a shared reference doc's canonical figures against both `moneycirc.go`'s real-world constants AND `fiscal.ts`'s rebased coefficients) as a **follow-up ticket**, not part of this rebase.
- GR#27 (Capture Before Wipe) applies if any increment's testing requires wiping a running webconsole sim/map.
- The insolvency/bailout cluster (FEAT-1972079923) and money-circ (FEAT-1972079927) both read `fiscal.ts`/`moneycirc.go` constants — Inc1/Inc2 must re-run BOTH feature's test suites, not just money-circ's, since the ratio-preservation in Section 5b depends on the base `funds`/`initialTreasury` anchor these other features also reference.

---

## 7. Open questions for Aaron (answer row-by-row)

1. **Seed population derivation.** Do you want the OS-Terrain-tile-derived dwelling count (your original 2026-08-31 ruling) folded into THIS rebase's execution, or kept as a separate ticket with an interim illustrative hamlet figure (this plan suggests 50-200) used for now?
2. **Starting treasury — final or still trial?** Confirm £10,000,000 stands as the anchor (carried forward from your 2026-08-31 12:04 ruling), or give a different figure now that it's tied to a full rebase rather than just money-circ's divisor.
3. **Wages/council-tax per-tick coefficients (webconsole `fiscal.ts`).** These are currently directional placeholders (`population * 0.5`, `population * taxRate * 2/100`) with no real-£ citation. Approve rebasing them toward the SAME real figures already used on the Go side (£1,512/mo net wage, £47/mo council tax, tick-adjusted), or specify different target figures/ratios?
4. **Grid export tariff (`GRID_EXPORT_TARIFF_PER_MW = 1.6`).** No real UK wholesale/export electricity tariff was researched for this plan (2024-25 market rates run roughly £50-120/MWh). Should this be re-anchored to a real tariff, or left as an independent gameplay lever (current comment already flags it "balance pass pending")?
5. **Building capex catalogue rebase (114 entries).** Approve the three anchor citations in Section 3.5 (school ~£8-11M, power plant ~£0.75-1.5M/MW, road ~£10-15M/km, ~2% annual upkeep ratio) as the basis for rescaling the full `data.ts` catalogue, or provide different reference figures/citations?
6. **Micropound precision at megacity scale.** Section 4 recommends KEEPING micropound (1e-6 GBP) precision at 100M-citizen scale (headroom ~12-92x depending on the aggregate in question) rather than moving to a coarser base unit. Confirm, or do you want a belt-and-suspenders coarser base unit anyway (e.g. milli-pounds) for extra headroom?
7. **`initialCitizenWealth` ratio.** This plan proposes keeping its current 0.5:1 ratio to `initialTreasury` (£5M once treasury is £10M) rather than deriving it from the ONS £2.5k/£6k migrant-wealth figures (which apply to ARRIVING migrants, not the seed population). Confirm this is the right constant to ratio-preserve, or should the seed population's wealth instead use the migrant-wealth log-normal draw directly?
8. **Cross-engine consistency mechanism.** Section 6 flags that nothing mechanically keeps the webconsole TS sim and the Go engine's money constants aligned once both are independently rebased. Worth a follow-up ticket for a lint/test that checks both sides cite the same canonical figures, or is manual review at PR time sufficient?

---

## Files referenced in this inventory (all read-only for this task)

- `webconsole/src/sim/fiscal.ts`
- `webconsole/src/sim/data.ts`
- `webconsole/src/sim/engine.ts`
- `internal/foundation/det/money.go`
- `internal/engine/compose/compose.go`
- `internal/engine/compose/moneycirc.go`
- `internal/engine/compose/moneycirc_inc2.go`
- `internal/engine/invariant/money.go`, `internal/engine/invariant/snapshot.go`
- `code.json` (units registry, lines 44-69)
- `docs/planning/money-numbers-real-world.md`
- BOW item BUG-452 (`node claude-bow.js show BUG-452`)
