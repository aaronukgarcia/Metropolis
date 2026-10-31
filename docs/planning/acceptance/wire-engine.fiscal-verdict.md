# engine.fiscal vs engine.finance: AC-1 overlap verdict (Step 0)

Author: analyst lane, 2026-10-31. BOW FEAT-2326609835 / MOD-066. Awaiting lead approval before the build phase. Evidence: `internal/engine/fiscal/finance_equality_test.go` (7 tests, all green; one mutation-proved). Every line cited is from `internal/engine/fiscal` and `internal/engine/finance` as of main fffd464.

## 1. The pack's overlap statement, claim by claim

| # | Pack claim (section 1) | Verdict | Evidence |
|---|---|---|---|
| C1 | fiscal is a read-model over finance plus a small write surface, not a competing ledger | CONFIRMED | No money field in `FiscalAPI` (`api.go`: only `civilServiceWageBill`, `planningFunding`, `childcarePlaces` inputs). Writers: `postBenefit` and `PostSocialHousingBuild`, both through `fin.Post`/`fin.SettleConstruction`. |
| C2 | "every figure is recomputed from finance's ledger and engine.tax" | PARTLY OVERTURNED | Imports, interest and drill-through come from finance. But `MoneyIn`, the tax node and `TaxBreakdown` come from `tax.TaxAPI.Instruments()` (`topology.go taxBreakdownFrom`), a PROJECTION, not the ledger. In compose tax posts via `finance.CollectTax` at a constant rate and `engine.tax` is unwired, so wired as-is the tax node is 0 (pack fact 1 confirmed). Fix on the pack's own assumed path: tax node = `finance.TaxRevenue()`. |
| C3 | "Nothing in fiscal replaces finance" | CONFIRMED with one exception | `fiscal.BudgetBalance` (`topology.go budgetBalance`) is tax minus imports minus interest only. `finance.BudgetBalance()` (`stages.go:516`) also subtracts maintenance, staff wages, materials, services opex, capex and construction. They agree only while those legs are zero; they diverge on the first construction posting (test `BudgetBalanceVsFinance`). Wired as-is, the F2 Sankey surplus would not match the balance sheet beside it. |
| C4 | "What MERGES: the Sankey query is the second sub-view of `f2.finance`; no new money" | CONFIRMED, with two defects to fix on merge | (a) balance must delegate to `finance.BudgetBalance()` (C3); (b) tax drill-through spans only `tax.income/sales/corp`, omitting `tax.council` that `finance.TaxRevenue()` counts (`topology.go financeTaxCategories` vs `accounts.go taxCategories`). Same quantity, different set; the test pins the omission. |
| C5 | five additions: Sankey topology, tax breakdown by type, civil-service truth, municipality, benefit postings | CONFIRMED, but item (5) overstated | Only two of the three "benefit" functions are fiscal-owned postings. `PostSocialHousingBuild` is a pure pass-through to `finance.SettleConstruction` (test `SocialHousingBuildEqualsSettleConstruction`): it adds nothing and is dropped. |
| C6 | benefit postings are "the ONLY fiscal functions that WRITE to the ledger" | CONFIRMED (three, incl. the pass-through) | `welfare.go`: `postBenefit` x2 + `PostSocialHousingBuild`. |
| C7 | money-in producers are Provisional zero nodes | CONFIRMED | `isProvisional`: exports, tourism, outCommuterWages, fdi, grants, plus the leakage SINK. Also `Node()`/`DrillThrough()` return 0/empty for them with no dependency required. |

## 2. Verdict table (every exported identifier)

Classes: MERGE = merges into f2.finance view; HOOK = wires as module hook; COVERED = already covered by finance, drop; PARK = parked.

| Export | Class | Finance function compared against | Equality test / note |
|---|---|---|---|
| `FiscalAPI`, `New`, `Load`, `LoadDefault`, `Config`, `MunicipalityConfig`, `ChildcareConfig`, `Config.Validate` | HOOK (infrastructure) | n/a (no finance counterpart; data/fiscal.json loaders) | constructed once in compose, needed by every row below |
| `SetFinance` | HOOK (infrastructure) | n/a | compose wires `st.finance` |
| `SetTax` | PARK | n/a | needs `engine.tax` wired (pack Q1). The recommended path removes the dependency for the Sankey: tax node from `finance.TaxRevenue()`. Still needed by civil-service/childcare for the income rate unless compose supplies it. |
| `NodeCategory` consts, `NodeKind` consts, `SankeyNode`, `SankeyEdge`, `Topology` | MERGE | n/a (view types; the fixed topology is the addition) | become the `sankey` sub-view payload |
| `SankeyTopology` | MERGE | budget node `finance.BudgetBalance`; imports `finance.ImportsTotal`; interest `finance.DebtServiceTotal`; tax `finance.TaxRevenue` | `ImportsAndInterestNodesEqualFinanceTotals`; budget/tax per rows below |
| `Node` (imports, interest) | COVERED as quantity; MERGE as shape | `finance.ImportsTotal`, `finance.DebtServiceTotal` | `ImportsAndInterestNodesEqualFinanceTotals` (also asserts `Node()` equals the topology node) |
| `Node` (budget) | MERGE after fix | `finance.BudgetBalance` | `BudgetBalanceVsFinance`: equal with only tax/imports/interest posted, DIVERGES after `SettleConstruction(450)` (fiscal unchanged, finance down 450). Merge must delegate to finance. |
| `Node` (taxRevenue), `MoneyIn` | MERGE after fix | `finance.TaxRevenue` | `BudgetBalanceVsFinance` logs/asserts equality after `tax.CollectAll` on a fixed ledger (council tax is outside TaxAPI, so `TaxRevenue` = `RevenueTotal` only while no council tax posts). On a composed city source must be `finance.TaxRevenue()`. |
| `Node` (provisional categories) | MERGE (flag only) | none: zero by design | existing `TestProvisionalNodes` |
| `MoneyOut` | COVERED as quantity; MERGE as field | `finance.ImportsTotal` + `finance.OpexBreakdown().DebtService` | `MoneyOutEqualsFinanceOutflows` (**mutation-proved**, below). Note `finance.ComposedOpex`/`CapexTotal`/`ConstructionTotal` are NOT in fiscal's MoneyOut: see BudgetBalance. |
| `BudgetBalance` | COVERED (finance wins); drop fiscal's formula | `finance.BudgetBalance` | `BudgetBalanceVsFinance` as above |
| `DrillThrough` (imports, interest, budget) | COVERED | `finance.LinesByCategory(CatImports)`, `LinesByCategory(CatDebtService)`, `Lines(AcctTreasury)` | `DrillThroughEqualsFinanceLines` (exact `reflect.DeepEqual`) |
| `DrillThrough` (taxRevenue) | MERGE after fix | `finance.LinesByCategory` x(`tax.income`,`tax.sales`,`tax.corp`) + must add `tax.council` | test pins that council tax is excluded today while `finance.TaxRevenue()` counts it (55) |
| `TaxBreakdown`, `taxBreakdownFrom`-based totals | MERGE (needs Q1 decision) | partial: `finance.TaxRevenue` for the total; per-type via `LinesByCategory(CatTax*)` | No finance function returns the five-way split (income/sales/corp/rates/duties) so it is not "covered"; the ledger only has four tax categories (no duties line). With TaxAPI unwired it reads 0. Honest path: derive four lines from ledger categories, duties flagged Provisional. |
| `TaxBreakdown` type, `.Total()` | MERGE | `tax.RevenueTotal` (existing test) | `TestTaxBreakdownMatchesIndependentQuery` already covers |
| `SetCivilServiceWageBill`, `CivilServiceGross` | HOOK | none (finance has `PostWages` for the posting, not a bill getter) | input must come from compose summing `ServicesAPI.GrossWageCost`; compose does not call it today (grep: no `GrossWageCost` in compose.go) |
| `CivilServiceClawback`, `CivilServiceNet` | HOOK | `finance.CollectTax` income receipt at the same rate | `CivilServiceClawbackEqualsFinanceIncomeTax`: clawback equals `CollectTax(...).Income` for the same wage bill and rate (bp rounding identical); net = gross minus it. Same quantity, but fiscal owns the "public wages" framing and the TaxAPI rate input, so it stays as a hook rather than dropped. |
| `SetPlanningFunding`, `PlanningFunding`, `PlanningFundingTarget` | HOOK | none | state is non-derivable: needs a save participant (AC-9) |
| `PermitSpeedMultiplier`, `BuildCostErrorRate`, `LayoutQualityBonus`, `CorruptionRisk` | HOOK (needs a consumer; AC-6) | none | no Go consumer today (`engine/build` unaware). Without one this is built-but-not-wired; pack AC-6 stands. |
| `SetChildcarePlaces`, `ChildcarePlaces`, `ChildcareNetLine`, `ChildcareNetLine` type | PARK | none | no places source exists in the composed city (pack AC-8: present, zero, Provisional) |
| `PostUnemploymentSupport`, `PostHousingBenefit` | HOOK | `finance.Post` (raw treasury to households transfer) | `BenefitPostingEqualsRawFinancePost`: ledger effect identical to a hand-built transfer under `benefits.unemployment`; the category and the negative-amount guard are the only additions |
| `PostSocialHousingBuild` | COVERED, drop | `finance.SettleConstruction` | `SocialHousingBuildEqualsSettleConstruction`: identical lines, money stock and return value on twin ledgers. Callers should call `SettleConstruction` directly (it has no housing build path anyway; pack non-goal) |
| `Err*` codes (`errors.go`) | follow their callers | n/a | registry codes already allocated |

## 3. Net effect on the build phase

1. Sankey is a sub-view; **budget balance, tax inflow and tax drill-through must source from finance** (`BudgetBalance`, `TaxRevenue`, tax categories incl. `tax.council`), not from `engine.tax` or fiscal's own subtraction. This makes Q1's "smaller, honest path" the required path, not just an option, and removes the SetTax dependency for the Sankey.
2. Drop: `PostSocialHousingBuild`, fiscal's `BudgetBalance` formula, `MoneyOut` as an independent computation (keep the field, source from finance).
3. Hooks that need inputs: benefits (driver), civil-service (wage bill from services roster, income rate from compose's constant), municipality (consumer, save participant).
4. Parked: childcare, SetTax/TaxBreakdown five-way split.
5. Pack AC-2 mutation ("MoneyIn adds a constant") is checkable; AC-1 mutation proved (below).
