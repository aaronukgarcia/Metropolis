# Wage / employment / tax model — audit + proposal (2026-09-02)

Deep-dive prompted by Aaron ("the wages bill does not look right"). Read-only audit of the Go engine + TS sim. **Aaron's instinct confirmed.**

## Root cause
**No employer identity exists.** Nothing records *who employs a citizen* (a specific business vs the state), so nothing can decide *who pays the wage*. The default is "the treasury pays everyone" — correct for the tiny civil-servant slice (`engine.staffing`, `SectorPublic`), wrong for the bulk of the population (hardcoded `SectorTertiary` yet still paid out of `AcctTreasury`).

## Confirmed defects (Go engine)
- **D1 (P1, clear bug):** `compose.go:1576` `PostWages` posts one aggregate `treasury→households` transfer for the *entire* employed population, ignoring sector. Building a private economy drains the treasury like payroll. No `AcctFirms→AcctHouseholds` wage flow exists anywhere.
- **D2 (P1, integrity bug):** two disconnected wage models — (i) a ledger-level fake income tax at `IncomeRate:10000` = 100%, which claws back the whole wage the same tick so the pair nets to zero (pure observability noise); (ii) a real per-citizen `Wealth` credit (`distributeWagesToResidents`) whose 28% "tax" is **subtracted and credited to nothing** — silent money destruction, off-ledger. Neither actually moves income tax to the treasury.
- **D3 (medium-high):** public-sector (`staffing`) wages use the same `PostWages` bucket, are never taxed, and `staffing.LocalWage` never reaches the citizen's wallet; `Sector` is economically dead (never selects payer, sizes wage, or gates tax).
- **D4 (depth, not a bug):** no rent/landlord/rent-tax chain at all — finance has 5 accounts (`Treasury/Households/Firms/Debt/External`), no landlord; rent is only a scoring input to housing affordability, never a money transfer.
- **D5 (latent):** conservation holds today *only because* the real per-citizen wage is off-ledger; folding it on-ledger would break conservation (the 28% has no beneficiary).

**TS sim:** simpler and worse — `wagesPerTick(population)` (not employed count), no employment concept, pure outflow leakage, no income tax, no rent (BUG-523).

## Proposed model — the distinct flows
| Flow | Direction | Booking |
|---|---|---|
| Private wages | Firm → Worker | `AcctFirms → AcctHouseholds` (NEW primitive; `PostWages` needs a payer-account param) |
| State wages | Treasury → Worker | existing `PostWages`, but **restricted to `SectorPublic`/staffing**, not the whole population |
| Income tax | Worker → Treasury | ONE real leg (~28%), on the actual wage, replacing both the fake-100% and the vanishing-28% |
| Rent | Worker → Landlord | NEW — needs a landlord entity decision |
| Rent tax | Landlord → Treasury | NEW — depends on the rent decision |

## Priority split
1. **Fix now (closes D1/D2/D3, no new *design*, but real plumbing):** gate the treasury-paid wage path to `Sector==SectorPublic` only; introduce a private-wage payer `AcctFirms→AcctHouseholds`; collapse the two income-tax computations into one real leg that actually posts to the treasury.
2. **Needs a decision — employer identity:** (a) a per-citizen `EmployerID` linking to a specific firm (realistic, ties into `engine.firms`, more state), or (b) use `Sector` as the single dispatch key (`SectorPublic`→state, else→firms in aggregate) — the pragmatic first cut. **Recommend (b) first.**
3. **Needs a decision — landlord/rent (D4, genuine new depth):** landlords as (a) distinct rentier citizens (most realistic, most state), (b) a firm-class reusing `AcctFirms` (cheapest with real entities), or (c) an abstract `AcctLandlords` account (cheapest, mirrors how council tax is already modelled). **Recommend (c) as a first cut**, upgradeable later.
4. **Balance numbers** (income-tax rate, rent level, rent-tax rate): your row-by-row approval per the balance-number regime.

Tracked as: the P1 wage bug (D1/D2/D3) + a P2 rent/landlord depth feature. The fix collides with the in-flight BUG-547 (same moneycirc.go area), so it sequences after BUG-547's CI-red fix lands, and after your model decisions.
