# FEAT-082 Money Circulation + Household Formation Design Brief

**For:** Aaron
**Status:** Decision required (6 design questions with options)
**Evidence:** BUG-362 diagnosis + BUG-394 (frozen population) + code inspection
**Goal:** Unblock the watchable month (money visibly moves, population responds to affordability)

---

## The Problem

Baseline-one has a complete but disconnected money-circulation loop:
- **Current state:** flat 1M wages + 100% income tax every month = zero net change. Treasury (10M) and households (5M) static after month 1. Citizens have zero wealth (never credited). Households never form (HouseholdIDs passed as nil).
- **Result:** attract equilibrium frozen (HousingAffordability pinned at 100 for a vacant city). Population does not respond to affordability pressure.

The finance module has all required posting methods (PostWages, PostHouseholdSpend, CollectTax, SettleConstruction — tested, never called). The missing wiring is in **compose.go** (the composition root). This brief surfaces 6 coupled design decisions Aaron must rule on to wire them.

---

## Design Question 1: Household Formation Timing

**Current Broken State:**
- `compose.go:1612` passes `HouseholdIDs: nil` to attract's term inputs every month.
- `households/api.go:522-524` — when `householdIDs` is empty (nil), `affordabilityIndex` returns 100 ("vacant city fully affordable").
- No resident households ever formed; households module stays inert.

**Why This Matters:**
- HousingAffordability is the only signal that makes migration respond to housing pressure. A frozen 100 guarantees frozen migration.
- Household formation is a prerequisite to meaningful housing feedback.

**Option A: Form households monthly in compose from resident citizens**
- Pros: Real monthly household composition; households naturally age with citizens; scales with population growth.
- Cons: Need a citizen→household mapping strategy (ASM-247 exists but not integrated); every citizen needs a household assignment; may create artificial household boundaries.
- Evidence: households/api.go has HouseholdProfile and DwellingSizePref methods waiting for household IDs.

**Option B: Form households at citizen birth (migrate-time, not monthly)**
- Pros: One household per migrant; simpler accounting (1:N citizens per household rule).
- Cons: Households lag population changes by one month (new births not immediately housed); requires lifecycle stage tracking in citizens.
- Evidence: citizens module has Stage, School, HealthBand tracking; could add HouseholdID.

**Option C: Stub-form N households at start (fixed set, rebalance each month)**
- Pros: Simplest baseline-one implementation; no new citizen data.
- Cons: Households never grow/shrink; totally unrealistic; afford ability becomes a flat scaling exercise.

**Recommendation:** **Option A (monthly from residents)** — households is a real module with a published HouseholdID field waiting in ASM-247; monthly formation aligns with the monthly tick; if it's too much work to wire ASM-247, fall back to a temporary hardcoded stub (e.g., "assign all residents to household 1" just to unfreeze affordability).

**Unblocks:** Q2 (affordability signal), Q5 (wealth crediting can target households).

---

## Design Question 2: Housing Affordability Signal

**Current Broken State:**
- `households/api.go:467-530` implements real affordability math (overcrowding + rent burden + unhoused-by-preference, AC-9).
- `compose.go` never calls it; passes hardcoded `baselineOneMonthlyRent = 0` (line 85).
- Result: `affordabilityIndex(0, 0)` → 100 always (vacant city).

**Why This Matters:**
- attract §11 formula: if HousingAffordability is always 100, the term is dead weight; the master dial cannot respond to housing shortage.
- Real affordability must reflect dwelling-vs-population pressure to close the feedback loop.

**Option A: Call HousingAffordability(householdIDs, rent, income) monthly from compose**
- Pros: Real signal; HousingAffordability is already implemented and tested; compose has access to FinanceAPI which knows citizen income (can derive from wage bill).
- Cons: Requires households to exist (tied to Q1).
- Evidence: stages.go PostWages posts wages; f.WagesPosted() queries them (line 365).

**Option B: Derive a simpler placeholder (dwelling stock vs population ratio)**
- Pros: No new dependencies; faster baseline-one loop.
- Cons: Not the real households math; won't respond to rent pressure; defeat Q1.

**Option C: Hardcode a feedback gain (affordability = 100 - population/housing_vacancy × k)**
- Pros: Immediate signal; parametric.
- Cons: Not households-based; contradicts the real module being built.

**Recommendation:** **Option A** — HousingAffordability is the real formula and it exists. Pass the actual household IDs (from Q1's answer) and a real rent figure (see next paragraph).

**On rent:** baseline-one has `baselineOneMonthlyRent = 0` (line 85 comment: "vacant city rent placeholder"). **Proposal:** set this to a small non-zero figure (e.g., 10_000 micropounds/month = 0.01 pounds, a balance placeholder per GR#15) so affordability math can distinguish rich vs. poor households. Aaron to set the value; recommend it be a named constant (not a hardcoded 0 or 1M).

**Unblocks:** attract term motion (population response), Q5 (wealth tracking can compare to rent).

---

## Design Question 3: Building Cost / SettleConstruction

**Current Broken State:**
- `build/build.go:568-634` Tick() method draws materials and labour, advances lead time; **nowhere debits construction cost from the treasury**.
- `finance/stages.go:238-256` SettleConstruction() exists, tested, never called.
- `compose.go` has no buildHook that posts construction spend.
- Result: buildings are free; budget closure stays zero (construction cost never leaves the treasury).

**Why This Matters:**
- The BUG-362 diagnosis says "SettleConstruction breaks budget closure." This means: if you wire it naively, money vanishes or re-appears incorrectly.
- Budget closure formula (`finance/stages.go:402-412`): `Tax − Opex − Debt − Construction − Imports = Balance`. If Construction is always 0, budget never tightens.
- The real issue: **Who pays for construction — the treasury (player), or firms, or households?** The answer determines which account SettleConstruction targets.

**Current SettleConstruction design (`stages.go:246-251`):**
```
AcctTreasury (debit) → AcctExternal (credit)
```
This posts construction as a treasury outflow to the external world (like opex).

**But the budget-closure problem is:** if build orders are free (no materials cost, no labour cost), there's no source to post _from_. Where does the cost come from?

**Option A: Treasury pays for materials and labour (player funds construction)**
- Pros: Simple; player has agency (city budget is the lever).
- Cons: Requires materials and labour to have _prices_ (currently they don't — logistics.Draw is free, labour is free); need a market-sourced cost model.
- Evidence: finance.SettleConstruction already posts treasury → external; just need compose to call it with a derived cost.

**Option B: Firms supply materials and labour (debit firms.cash, credit construction)**
- Pros: Separates city budget from construction cost; firms own the supply chain.
- Cons: Requires firms module to track construction-materials inventory and pricing (out of scope for baseline-one); requires a build→firms edge in code.json.
- Evidence: code.json already has engine.build → engine.firms edge (code.json:3899-3901, inbound consume), but no firms supply-to-build edge.

**Option C: Households pay rent, which funds construction (debit AcctHouseholds, credit construction)**
- Pros: Rent becomes the bridge between housing and infrastructure.
- Cons: Requires a rent-collection hook; makes households directly responsible for public works (unrealistic).

**Option D: Stub-accept the free-construction model (do not post SettleConstruction yet)**
- Pros: Baseline-one watchable-month does not require a mature cost model; building is already visible (materials/labour gates work, structures render).
- Cons: Budget never closes; finance aggregates stay misleading; construction never appears on the ledger.

**Recommendation:** **Option D (defer to post-baseline-one)** — for the watchable month, **do not call SettleConstruction**. The real cost model (materials sourced from firms, priced, paid from treasury) is a later sprint. **Unblock watchable by leaving construction free, with a clear TODO in compose.go commenting why SettleConstruction is parked.** This lets Q1–2–5 wire without waiting for a build→firms pricing bridge.

**Rationale:** BUG-362 says "SettleConstruction breaks budget closure" — it breaks because there's no cost source yet. Avoiding the call prevents the break.

**Unblocks:** watchable month (buildings build freely); deferred: budget-closure closure (needs sprint 5 build costs).

---

## Design Question 4: Money Circulation Loop

**Current Broken State:**
- `finance/stages.go` has the full loop:
  1. PostWages (treasury → households) ✓ exists, tested
  2. PostHouseholdSpend (households → firms, lines 55-76) ✓ exists, tested, **never called**
  3. CollectTax (households/firms → treasury) ✓ exists, tested
  4. (Q3) SettleConstruction (treasury → external) — parked
  5. SettleOpex/ServiceDebt/Imports — similar pattern
- `compose.go` posts only wages (from financeHook, line 1500-1520).
- Result: households accumulate wages (flat 1M/month) and pay 100% tax (also 1M); net zero; money static.

**The missing link:** PostHouseholdSpend must be called monthly to move money households → firms → (back to treasury via corp tax).

**But where does consumption quantity come from?**
- `compose.go:1397-1414` drawConsumption() solves water/power/gas demand and accumulates delivered quantity (line 1412: `st.consumptionDelivered`).
- No connection to finance: the consumption solve _succeeds silently_; the household cost is never posted.

**The budget question:** consumption costs money. How much?
- Option: per-person baseline consumption (e.g., all citizens consume the same bundle = `population × consumptionPerPerson`).
- Price: market-sourced (e.g., `market.Price(water)` per unit, times total water consumed).

**Option A: Post consumption spend monthly (households → firms) using delivered quantity**
- Pros: Closes the loop; money visibly moves households → firms → treasury (via tax).
- Cons: Requires mapping consumption quantities (litres + kWh summed, line 1412) to a money figure. Current code does not price utilities.
- Evidence: finance.PostHouseholdSpendAtMarket (stages.go:84-95) already does quantity × market.Price lookup; just need to feed it the consumption total.

**Option B: Use a fixed household consumption budget (e.g., all citizens spend 50k µ£/month on utilities)**
- Pros: Simplest baseline-one model; no pricing logic needed.
- Cons: Hardcoded placeholder; does not scale with consumption solve (wasted solve result).
- Evidence: compose.go already hardcodes other placeholders (wages, tax, rent, vacancy).

**Option C: Defer consumption→finance wire to post-baseline-one**
- Pros: Keeps watchable-month scope tighter (wages + tax only).
- Cons: Money loop stays incomplete; no visible spending signal.

**Recommendation:** **Option B (fixed household consumption budget)** — for watchable month, add a const `monthlyConsumptionSpendPerCapita = 100_000` (µ£, a placeholder) at the top of compose.go. In a new consumptionFinanceHook (monthly, PhaseConsumption), post:
```
consumption.Tick: 
  spend = population × monthlyConsumptionSpendPerCapita
  f.PostHouseholdSpend(1, Money(spend))  // quantity=1, price=spend (degenerate but works)
```
This moves money households → firms each month. Pair it with CollectTax to tax the firms' gain → treasury closes.

**Bonus:** post-baseline-one can wire the real consumption quantities (from drawConsumption) and market prices, replacing the placeholder.

**Unblocks:** visible monthly money flow (wages → households → (consumption) → firms → (corp tax) → treasury). Budget stays in balance: wages = tax + net accumulation.

---

## Design Question 5: Per-Citizen Wealth Crediting

**Current Broken State:**
- `citizens/citizen.go:114` Wealth is a field (int64, micro-pounds).
- `citizens/registry.go:444-450` LifeEventWealth exists (command to set wealth on a citizen).
- **No production code calls LifeEventWealth**. Citizens birth at Wealth 0; migrant citizens birth at Wealth 0.
- Result: all citizens are broke; can never express inequality, savings, or lifecycle wealth changes.

**Why This Matters:**
- Households module will want to query citizen wealth (for targeting, satisfaction, migration preference).
- The pay/ keystone (FEAT-225 A3 acceptance) requires staffing to post wages per-citizen, not aggregate.

**Where should wealth be credited?**

**Option A: Post a monthly personal-income transfer (each employed citizen gains their share of wages)**
- Pros: Ties wealth to employment; reward employed citizens; natural inequality emerges (different wages by sector/skill).
- Cons: Requires per-citizen wage calculation (currently a flat 1M total); needs a citizens.LifeEventCommand loop.

**Option B: Credit wealth at each milestone (birth, partnership, employment, promotion)**
- Pros: Lifecycle events are natural crediting moments; already exist in LifeEventCommand protocol.
- Cons: Requires ASM/spec for each event's wealth delta; more complex.

**Option C: Post a lump-sum wealth gift at birth (migrants enter with seed wealth)**
- Pros: Simplest; immediate inequality if seed varies by type.
- Cons: No ongoing wealth accumulation; stale model (no wages reach citizens).

**Option D: Defer per-citizen wealth to post-baseline-one (keep aggregate household wealth only)**
- Pros: Watchable month doesn't need it; wealth aggregates (total citizens accumulated 5M) already visible on F2.
- Cons: Misses the chance to close per-citizen data pipelines.

**Recommendation:** **Option C (migrate-time seed) + Option A (monthly wage distribution) together** — for watchable month:
1. Modify citizens.go to accept a Wealth parameter in Citizen (migrations already set citizen ID, personality, etc.; add Wealth from attract's migrant draw).
2. Modify compose's financeHook (post wages) to distribute wages to per-citizen LifeEventWealth commands — not a lump sum, but spread to actual citizens proportional to employment state (employed citizens get more, unemployed get baseline).

This closes the cycle: attract → citizen.Wealth → finance ledger (PostWages aggregate) → visibility.

**Unblocks:** watchable month (citizens visibly own money); pay/ keystone (staffing can post per-citizen wages); household wealth targeting.

---

## Design Question 6: Sequencing for Watchable Month

**Constraint:** Aaron's Baseline-One priority — the loop must **RUN** (money visibly moves, population responds), not be perfect.

**Minimal build order to unblock watchable month (money circulates, population responds to affordability):**

1. **Land household formation (Q1, minimal implementation)**
   - Temporary: hardcode "all residents assigned to household 1" in compose's applyMigration.
   - Real (post-baseline): wire ASM-247 (citizen→household mapping).
   - Unblocks: HousingAffordability can compute.

2. **Wire HousingAffordability monthly (Q2)**
   - Call `h.households.HousingAffordability(householdIDs, rent=10_000µ£, income=derived)` in applyMigration after setting households.
   - Pass result to attract.SetTermInputs.
   - Unblocks: attract responds to housing pressure; population will migrate if affordability drops.

3. **Post consumption spending (Q4, simplified)**
   - Add `const monthlyConsumptionSpendPerCapita = 100_000µ£` to compose.go.
   - New hook (monthly, PhaseConsumption or bundled into financeHook): `f.PostHouseholdSpend(1, Money(population × const))`.
   - Unblocks: visible monthly household→firms transfer.

4. **Distribute wages to per-citizen wealth (Q5, minimal)**
   - Modify attract's migrant creation to assign Wealth (e.g., 50_000µ£ baseline).
   - Modify financeHook to loop citizens and call ApplyLifeEventCommand(LifeEventWealth) proportional to employment.
   - Unblocks: citizens own money; household wealth aggregates correctly.

5. **Leave SettleConstruction parked (Q3)**
   - Do NOT call it. Add comment: "TODO: wire post-baseline when build→firms materials pricing exists."
   - Result: buildings free, budget doesn't formally close (OK for watchable-month).

6. **Verify ledger closes:**
   - Check: `TotalMoneyInCirculation()` (treasury + households + firms + reserves) stays constant month-to-month.
   - If closed: watchable month is live (money moves, just doesn't leave the system).

**Critical Coupling Risks:**

| Risk | Mitigation |
|------|-----------|
| Q1 (households) must land before Q2 (affordability) works | Land Q1 minimally (hardcode all→household-1) if Q2 is urgent. |
| Q4 (consumption spend) requires Q2 (affordability) be running first for population test | Wire Q2 first, then Q4. |
| Q5 (per-citizen wealth) couples to attract's migrant.Wealth field (new field) | Add field to attract's migrant; minor API change. |
| Budget closure (Q3 parked) leaves construction off the ledger | Acceptable for baseline; document as deferred. |

**Sequencing Recommendation:**
```
Week 1: Q1 (households stub) + Q2 (affordability)
        → population now responds to housing pressure ✓

Week 1: Q4 (consumption spend) 
        → money visibly moves households→firms ✓

Week 2: Q5 (per-citizen wealth distribution)
        → citizens accumulate earnings ✓

Week 2: Full-loop regression test (Headless 360 ticks, F1/F2 render correctly)
        → watchable month complete ✓
```

---

## Summary: Recommended Design for Watchable Month

| Question | Design | Rationale |
|----------|--------|-----------|
| **Q1: Household Formation** | Temporary hardcode (all residents → household 1); wire ASM-247 post-baseline | Unfreeze affordability signal; real wiring deferred. |
| **Q2: Housing Affordability** | Call HouseholdIDs.HousingAffordability(rent=10kµ£) monthly | Real signal; attend module fully; population responds. |
| **Q3: Building Cost** | Park SettleConstruction; leave buildings free | Simplifies scope; cost model is a later sprint. |
| **Q4: Money Circulation** | Fixed per-capita consumption budget (100kµ£/month) via PostHouseholdSpend | Closes loop without pricing; visible monthly transfer. |
| **Q5: Per-Citizen Wealth** | Distribute monthly wages per-citizen via LifeEventWealth | Closes citizen data pipeline; supports future targeting. |
| **Q6: Sequencing** | Q1→Q2 (affordability flow), parallel Q4, then Q5; budget-closure deferred | Minimum blocks; maximum observable coupling. |

---

## Player-Felt Numbers (GR#15)

All numerical placeholders are directional, testable only by observation (not hardcoded targets):

- `monthlyWages = 1_000_000µ£` (1 pound aggregate) — placeholder, will vary by population/employment when staffing wires.
- `monthlyTax = 1_000_000µ£` — closes Q1 on flat model; will vary when tax rates/wages are real.
- `monthlyConsumptionSpendPerCapita = 100_000µ£` (0.1 pound/person) — placeholder, will scale when market prices utilities.
- `baselineOneMonthlyRent = 10_000µ£` (0.01 pound/household) — placeholder, will match real housing economics when build costs houses.
- `baselineOneHousingVacancy = 1_000_000` (dwellings) — placeholder, will come from households.ReportStock when build reports completions.

All are marked const and documented; balance-pass deferred to post-baseline per the proportionality ruling.

---

## Open for Aaron

1. **Approval to wire Q1–2–4–5 in sequence?**
2. **Per-capita consumption budget: 100kµ£ acceptable, or different placeholder?**
3. **Rent per household: 10kµ£ acceptable, or different?**
4. **Delay SettleConstruction wiring until build→firms pricing exists (Q3 parked)?**
