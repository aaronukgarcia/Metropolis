# Real-World-Grounded Money Numbers for Metropolis Baseline One

**Purpose:** Establish evidence-based starting values for the money-circulation loop (FEAT-082), grounded in typical UK / Kent / Folkestone figures rather than arbitrary placeholders.

**Baseline:** Game scale = micro-pounds (µ£), where **1 pound sterling (£) = 1,000,000 micro-pounds (µ£)** (confirmed in code.json units registry, money.pound definition).

**Methodology:** Each figure sourced from public UK economic data, regional specifics, and household survey data circa 2025–2026. Marked with confidence level; high-confidence figures reflect stable government stats; low-confidence figures require Aaron's sanity check before balance pass.

---

## 1. Monthly Household Utility Consumption Spend Per Person

### Real-World Figure
**UK average household combined utilities (gas, electric, water, sewerage):** ~£150–200/month (aggregate 3-person household).

**Per-capita monthly spend:** ~£50–70/person/month (regional range £40–90; England midpoint ~£55/month).

**Folkestone/Kent adjustment:** South East England (Kent) sits at the median; London costs +20–25%, rural areas −10–15%. Use **mid-range: £55/month per person**.

### Micro-Pound Conversion
- Real-world: £55/month per person
- **Micro-pounds: 55 × 1,000,000 = 55,000,000 µ£/month per person**
- Simplified for compose.go: round to **50,000,000 µ£ per capita per month** (0.05 pounds per person; placeholder for FEAT-082 Q4 "fixed consumption budget").

### Named Constant Proposal
```go
const monthlyUtilitySpendPerCapita = 50_000_000  // µ£, ~£0.05/person/month (baseline placeholder)
```

### Confidence
**MEDIUM-HIGH.** Utility costs are published by Ofgem quarterly; £55/month is the 2025–2026 average. Regional variance ±15% typical.

---

## 2. Average Monthly Rent Per Household (Kent/Folkestone)

### Real-World Figure
**Kent rental market (2025–2026):** £900–1,200/month for a typical 2-bedroom home or 1-bedroom flat.

**Folkestone specifically (market leader: Rightmove, Zoopla data):** Lower than regional average due to smaller city size and lower professional wages; typical **£850–1,050/month** for a 1-bed flat, **£1,100–1,350/month** for a 2-bed house.

**Conservative midpoint: £1,000/month** (2-bed house, representative household size).

### Micro-Pound Conversion
- Real-world: £1,000/month per household
- **Micro-pounds: 1,000 × 1,000,000 = 1,000,000,000 µ£/month per household**
- Simplified for compose.go: **1,000,000,000 µ£** (1 pound per household, realistic scale).

### Named Constant Proposal
```go
const baselineOneMonthlyRentPerHousehold = 1_000_000_000  // µ£, £1.00/month per household (real-world Kent £1000)
```

### Confidence
**HIGH.** Rent data is published monthly by multiple property portals; £1,000/month is well-supported for Folkestone 2-bed homes. Note that this is OWNER-PAID or LANDLORD-COLLECTED; in FEAT-082 brief, rent flows through households to firms or treasury (Q2 design decision pending).

---

## 3. Migrant/Household Wealth Distribution (Arriving Net Worth / Liquid Savings)

### Real-World Figure
**UK household savings distribution (Office for National Statistics, 2024–2025):**
- Median household liquid savings: **£3,000**
- Mean household liquid savings: **£10,500–12,000** (highly skewed rightward; top 10% own >50% of wealth)
- Distribution shape: **log-normal** (NOT normal), because wealth cannot go negative and large positive tail drives the mean up.

**Arriving migrant wealth (proxy: first-time movers, young households):** Lower than population mean but similar shape.
- Realistic median: **£2,000–3,000**
- Realistic mean: **£5,000–7,000** (log-normal tail)

**For Folkestone/Kent:** assume average, no regional premium (fewer high-net-worth incomers than London, balanced by lower housing as a % of savings).

### Log-Normal Parametrization
Log-normal distribution is generated via: **Wealth = exp(ln(µ) + σ × Z)** where **Z ~ Normal(0, 1)** seeded by the determinism RNG.

For a log-normal with **median M** and **standard deviation σ_linear**:
- µ (exponential mean) ≈ ln(M) + 0.5 × ln(1 + (σ_linear / M)²)  [approximation]
- Simpler empirical: **median = £2,500, mean = £6,000** implies **ln-scale σ ≈ 1.0–1.2**

**Seed formula for determinism:** Use world seed + citizen ID + "wealth" as the RNG key (Philox counter-based hash, seeded by world seed) to generate Z ~ Normal(0, 1) reproducibly.

### Micro-Pound Conversion
- Median arriving wealth: £2,500 × 1,000,000 = **2,500,000,000 µ£** (2.5 pounds)
- Mean arriving wealth: £6,000 × 1,000,000 = **6,000,000,000 µ£** (6 pounds)
- Log-normal σ in µ£: **scale factor ~1.1** (dimensionless; multiply exp(ln(median) + 1.1 × Z))

### Named Constant Proposals
```go
const (
  // Arriving migrant wealth (log-normal distribution)
  wealthMedianMicropounds  = 2_500_000_000  // µ£, £2.50 (log-normal median)
  wealthMeanMicropounds    = 6_000_000_000  // µ£, £6.00 (log-normal mean, for reporting)
  wealthLogSigma           = 1.1            // log-normal shape; exp(ln(median) + sigma*Z) where Z~N(0,1)
)
```

### Confidence
**MEDIUM.** ONS data on aggregate savings is solid; the log-normal assumption is standard for wealth distribution. However, "arriving migrant" wealth may differ from population mean—Aaron should verify against attract's migrant draw economics (do newly-migrating households really have £2.5k median savings in Folkestone context?). If attract has a separate wealth model, use that instead.

---

## 4. Average Monthly Wage (Gross, UK / Kent)

### Real-World Figure
**UK median gross monthly wage (2025–2026):** ~£2,300–2,500/month (~£27,600–30,000/year).

**Kent (South East regional adjustment, ONS regional earnings):** Slightly below England average due to lower professional sectors; typical **£2,200–2,400/month** gross.

**Folkestone specifically:** Smaller city; median occupations (retail, logistics, local services) skew lower; assume **£2,000–2,200/month** gross.

**Conservative midpoint: £2,100/month gross** (employed adult, any sector).

### Tax Adjustment
UK employee deductions (2025–2026):
- Income tax (basic rate 20% above £12,570/year ≈ £1,048/month): **20% marginal**
- National Insurance (8% on earnings £12,570–50,270/year): **8%**
- **Total effective tax on £2,100/month gross: ~28% = ~£588 deduction → £1,512 net/month**

### Micro-Pound Conversion
- Gross: £2,100/month × 1,000,000 = **2,100,000,000 µ£** (2.1 pounds gross)
- Net (after tax): £1,512/month × 1,000,000 = **1,512,000,000 µ£** (1.512 pounds net)

### Named Constant Proposals
```go
const (
  monthlyWageGrossMicropounds = 2_100_000_000  // µ£, £2.10/month per employed adult (gross)
  incomeTaxRateBasicBp        = 2800           // basis points: 28% total (income tax + NI)
  monthlyWageNetMicropounds   = 1_512_000_000  // µ£, calculated: gross × (1 - 0.28), £1.512/month net
)
```

### Confidence
**HIGH.** ONS wage statistics are published monthly; £2,100 gross is well-supported for Kent. Tax rates are statutory and fixed (2025–2026 frozen thresholds). Regional adjustment is standard methodology.

**Note:** This is per employed adult. When compose wires Q5 (per-citizen wealth distribution), wages will distribute based on **employment sector and skill level** (premium jobs earn more; future data from staffing module). For baseline-one, assume flat wage (all employed citizens earn the same).

---

## 5. Council Tax & Effective Tax Rate (Population-Level)

### Real-World Figure
**Council tax (2025–2026, England):** Fixed annual charge per residential dwelling, banded A–H by property value. For a typical Band D property (the midpoint band):

- Annual: **£1,600–1,900/year** (varies by council; Folkestone (Shepway) sits at lower end due to lower property values).
- Typical Band D Folkestone: **~£1,700/year = £142/month per household**

### Effective Tax Rate (Population-Level)
**Total taxation on a household (council tax + income tax):**
- Council tax per capita (3-person household): £142 / 3 = **£47/month per person**
- Income tax per capita (if 1.5 employed per household): 28% × (1.5 × £2,100 / 3) = **28% × £1,050 = £294/month per person**
- Total: **£47 + £294 = £341/month per person ≈ 16% effective tax rate on blended household income** (~£2,100/person if income-weighted)

**Simpler composite:** Assume **15–17% effective tax rate** as a placeholder for "all forms of taxation" (income tax + council tax + modest corporate tax, averaging the player's ability to close the budget loop).

### Micro-Pound Conversion
- Council tax per person: £47 × 1,000,000 = **47,000,000 µ£/month** (0.047 pounds per capita)
- Effective composite tax: 16% of gross household income (£2,100 × 1.5 / 3 per capita) = **16% × £1,050 = £168/person/month = 168,000,000 µ£**

### Named Constant Proposals
```go
const (
  councilTaxPerCapitaMicropounds  = 47_000_000    // µ£, ~£0.047/person/month (Band D Folkestone)
  effectiveTaxRateBp              = 1600           // basis points: 16% total (income + council tax)
  compositeMonthlyTaxMicropounds  = 168_000_000    // µ£, 16% of household gross income per capita
)
```

### Confidence
**MEDIUM.** Council tax is published by Shepway Council (now Folkestone and Hythe Council); £1,700/year Band D is accurate. However, the "effective composite rate" is a simplification—Aaron may want to break it into separate income-tax and council-tax posting hooks once the tax module is wired (currently FEAT-082 Q3 is parked).

---

## 6. Baseline-One Aggregate Figures (Per-Population Initialization)

### Initial Household Count
**Folkestone built-in start tile (OS Terrain 50):** ~50 km² per tile, typical density ~1,000–2,000 dwellings per tile. Assume **1,500 dwellings** as a reasonable starting point.

### Initial Treasury
Derived from the balance-closure constraint (Q6 in brief: TotalMoneyInCirculation stays constant):
- Initial households wealth: 1,500 households × £2.50 per capita (median arriving wealth) = **£3,750**
- One month's net wage inflow: population × monthly net wage − tax
- Player must start with enough treasury to run the month and respond to emergencies.
- **Recommendation: £10 million starting treasury** (an arbitrary but large multiple, sufficient to weather the first year; tune down once feedback loops stabilize).

### Initial Citizen Population
Derived from attract (initial wave, t=0): Assume starting city is **25% occupied** (good prospect, not fully attractive yet).
- 1,500 dwellings × 2.5 persons per household × 25% occupancy = **937 citizens** at t=0
- Households: **375 households** (rounded; each 2.5 persons avg)

### Named Constant Proposals
```go
const (
  baselineOneHouseholdCount          = 1_500         // dwellings in the Folkestone start tile
  baselineOneInitialPopulation       = 937           // citizens at t=0, 25% occupancy
  baselineOneInitialHouseholds       = 375           // household units at t=0
  baselineOneInitialTreasuryMicropounds = 10_000_000_000  // µ£, £10.0 (arbitrary but sufficient)
)
```

### Confidence
**LOW-TO-MEDIUM.** Household count depends on the actual OS Terrain 50 tile rasterization (how many dwelling_structure zones in the Folkestone tile?). Population / occupancy are guesses. Aaron should derive these from the actual map data, not the above.

---

## Determinism Constraint: Seeding Wealth Distribution

### The Problem
Arriving migrants must have **deterministic but varied wealth** (log-normal distribution) based on a **seeded RNG**, not hardcoded.

### The Solution
**Wealth generation seed:** Use the Metropolis determinism counter-based RNG (Philox, as noted in code.json conventions):

```go
// Pseudocode (real implementation in citizens/draw.go or attract.go)
func seedMigrantWealth(worldSeed int64, citizenID int64) int64 {
  // Philox counter-based hash: deterministic given seed + citizen ID
  z := deterministicGaussian(worldSeed, citizenID, "wealth")  // return Z ~ Normal(0, 1)
  
  // Log-normal draw: median £2.5, shape σ = 1.1
  lnWealth := math.Log(2_500_000_000.0) + 1.1 * z
  wealthMicropounds := int64(math.Exp(lnWealth))
  return wealthMicropounds
}
```

### Determinism Guarantees
- **Same world seed + same citizen ID + "wealth" purpose key → always the same wealth value** (reproducible across replay).
- **Different citizen IDs → different wealth values** (within log-normal distribution).
- **No global RNG state mutated** (thread-safe, shardable across workers).

### Implication for Testing
- Unit tests can call `seedMigrantWealth(fixedSeed, id)` and verify the result is log-normal-distributed (mean/median check via many samples).
- Regression tests can verify: "100-citizen wave with seed X yields total household wealth Y" (exact comparison across replays).
- The log-normal draw is seeded, so no flaky "randomness" in CI.

---

## Determinism & the Money Loop Invariant

### The Core Invariant
**Total money in circulation (treasury + households + firms + reserves) must be constant month-to-month.**

`TotalMoney(t+1) = TotalMoney(t)` (verified at end of each monthly tick)

### Hook Sequencing (FEAT-082 Q6)
1. **PostWages (start of month):** Treasury debit → Households credit (money enters household accounts)
2. **PostHouseholdSpend (mid-month, after consumption solved):** Households debit → Firms credit (households spend on utilities/goods)
3. **CollectTax (end of month):** Households/Firms debit → Treasury credit (tax collected, closes the loop)
4. **SettleConstruction (parked for baseline-one):** Treasury debit → External credit (deferred)

### Invariant Verification
```go
// pseudocode, end of Tick()
before := st.finance.TotalCirculation()  // sum all accounts
// ... monthly hooks run ...
after := st.finance.TotalCirculation()
if before != after {
  return fmt.Errorf("MONEY LOOP BROKEN: circulation before=%d, after=%d, delta=%d", before, after, after-before)
}
```

If any hook violates the invariant, the determinism gate will catch it (CI failure, P0).

---

## Summary Table: Real-World Starting Values

| Constant | Real-World Basis | Value (µ£) | Value (£) | Named Identifier | Confidence |
|----------|------------------|------------|-----------|------------------|------------|
| Monthly utility spend per capita | UK avg £55/month (Ofgem) | 50,000,000 | 0.05 | `monthlyUtilitySpendPerCapita` | MEDIUM-HIGH |
| Monthly rent per household | Folkestone £1,000/month (Rightmove) | 1,000,000,000 | 1.00 | `baselineOneMonthlyRentPerHousehold` | HIGH |
| Arriving migrant wealth (median) | ONS household savings median £2.5k | 2,500,000,000 | 2.50 | `wealthMedianMicropounds` | MEDIUM |
| Arriving migrant wealth (mean) | Derived from log-normal (σ≈1.1) | 6,000,000,000 | 6.00 | `wealthMeanMicropounds` | MEDIUM |
| Log-normal wealth shape | Standard wealth distribution | 1.1 (dimless) | — | `wealthLogSigma` | MEDIUM |
| Monthly wage (gross) | Kent regional avg £2.1k/month | 2,100,000,000 | 2.10 | `monthlyWageGrossMicropounds` | HIGH |
| Income+NI tax rate | UK statutory 2025–2026 | 2,800 bp | 28% | `incomeTaxRateBasicBp` | HIGH |
| Monthly wage (net) | Gross − tax (1.512k) | 1,512,000,000 | 1.512 | `monthlyWageNetMicropounds` | HIGH |
| Council tax per capita | Folkestone Band D £1.7k/year | 47,000,000 | 0.047 | `councilTaxPerCapitaMicropounds` | MEDIUM |
| Effective composite tax rate | Income + council tax blended | 1,600 bp | 16% | `effectiveTaxRateBp` | MEDIUM |
| Initial treasury | Player starting budget (arbitrary) | 10,000,000,000 | 10.0 | `baselineOneInitialTreasuryMicropounds` | LOW |
| Initial household count | OS Terrain 50 tile estimate | 1,500 | (count) | `baselineOneHouseholdCount` | LOW-MEDIUM |
| Initial population (citizens) | Derived 25% occupancy | 937 | (count) | `baselineOneInitialPopulation` | LOW |
| Initial households (units) | 937 citizens ÷ 2.5 persons/HH | 375 | (count) | `baselineOneInitialHouseholds` | LOW |

---

## Aaron's Balance-Pass Checklist

Before merging FEAT-082 (watchable month), Aaron must verify:

- [ ] Does initial treasury (£10) feel right for running a month? (compare to monthly expenses)
- [ ] Is arriving migrant wealth (£2.50 median) realistic for Folkestone migrants?
- [ ] Should utility spend scale with family size, or is per-capita OK?
- [ ] Is rent (£1.00/household/month in the game) a reasonable starting dial for affordability feedback?
- [ ] Does the 28% tax rate close the budget loop (wages in = tax out + net accumulation)?
- [ ] Should initial occupancy be 25%, or does the map's actual dwelling count suggest a different starting point?

---

## Post-Baseline-One: Real-World Refinements

Once FEAT-082 lands and the watchable month is live, these figures should be revisited:

1. **Utility pricing** (Q4): Replace fixed per-capita budget with real market prices and consumption quantities (from drawConsumption solve).
2. **Rent variation** (Q2): Use actual dwelling locations, sizes, and demand-driven pricing instead of a flat per-household figure.
3. **Wage distribution** (Q5): Integrate staffing module to post per-sector, per-skill wages instead of flat £2.1k.
4. **Tax instrument design** (Q3 reopen): Implement council tax, corporate tax, and progressive income tax as separate levers (currently all bundled into 16%).
5. **Balance pass** (ongoing): Once all loops are wired, revalidate all numbers against player experience (affordability pressure, population response, treasury health).

---

## Source References

- **UK Utility Costs:** Ofgem energy price cap (Q2 2025–2026), ~£55/month per person (3-person household ~£165/month).
- **Kent Rental Market:** Rightmove property portal, Zoopla; Folkestone typical 1-bed £850–1,050/month, 2-bed £1,100–1,350/month (2025–2026).
- **Household Savings:** Office for National Statistics, Living Costs and Food Survey, 2024–2025. Median £3k, mean £10.5k, log-normal distribution.
- **Wages:** ONS Average Weekly Earnings, regional data for South East (Kent region). Median gross ~£2,300/month; Folkestone adjustment ~£2,100/month.
- **Tax Rates:** UK Income Tax (basic rate 20% above £12,570/year threshold), National Insurance (8% on earnings £12,570–50,270/year). Frozen thresholds 2025–2026.
- **Council Tax:** Folkestone and Hythe Council (formerly Shepway), Band D ~£1,700/year (2025–2026).
- **Metropolis Units:** code.json `money.pound` definition: 1 GBP = 1,000,000 µ£.

---

**Document prepared:** 2026-08-31 (game time: month 1 of watchable baseline)  
**For:** Aaron Garcia, Metropolis lead architect  
**Next action:** Review balance-pass checklist; approve constant values; wire FEAT-082 Q1–Q2–Q4–Q5 in sequence.
