# Wage / ownership / tax model — design deep-dive (2026-09-02)

Deep-dive requested by Aaron on Q100048: he rejected the interview's coarser options in
favour of a richer model — employer identity by **building type**, buildings owned by
**LTD / LLP / Person / State**, revenue/tax **rolling up** the ownership chain to a
taxable Person or the State. This doc turns that into a concrete design, four worked
money-flow examples, and a numbered list of follow-up questions. **Read-only — no code,
no commit** (GR#24). Builds directly on the audit in
`docs/planning/proposals/wage-employment-tax-model-2026-09-02.md` (D1–D5, the broken
100%-clawback / vanishing-28% / no-rent baseline).

## 0. What's already there (grounding — confirmed by code read, not memory)

- **Finance** (`internal/engine/finance/accounts.go`): 6 accounts today — `AcctTreasury`,
  `AcctHouseholds`, `AcctFirms`, `AcctReserves`, `AcctDebt`, `AcctExternal`. Categories
  already include `CatWages`, `CatTaxIncome`, `CatTaxSales`, `CatTaxCorp`,
  `CatTaxCouncil` — **corp tax and council tax categories already exist and are unused**,
  which matters below (no new categories needed for Stage 2/3).
- **`PostWages(total Money)`** (`stages.go:30`) posts one aggregate Treasury→Households
  transfer; only caller is `compose.go:1618`, paying the *entire* employed population
  regardless of sector (D1). `CollectTax` posts a fake `IncomeRate:10000` (100%) leg
  right after (D2i) that exists only to net against D1's over-payment.
- **The real per-citizen wage** lives entirely off-ledger: `moneycirc.go`'s
  `distributeWagesToResidents` credits `cit.Wealth` directly at a 28% blended
  income+NI rate (`incomeNITaxRateBp`), with the 28% **going nowhere** (D2ii, silent
  money destruction).
- **`citizens.Employment`** (`citizen.go:89`) has only `State EmploymentState` (a closed
  6-value enum incl. `OffMap`) and `Sector Sector` (Primary/Secondary/Tertiary/Public).
  **No employer or building reference exists on a citizen today.**
- **`engine.staffing`** computes its own `LocalWage`-based wage cost per assigned
  building independently — **not wired to `PostWages`/`CollectTax` at all** (D3).
- **`engine.build`**: buildings/zones already carry `OwnerID uint32` and a
  `requireOwned` gate (`build.go:129,137,148,388,406,435,480`) — ownership as a raw ID
  already exists mechanically, but with **no typed owner (Person/LTD/LLP/State), no
  roll-up, and no tax consequence attached to it.** The zone catalogue itself is a
  fixed 8-way §34 vocabulary (`dwelling, shop, office, entertainment, farming,
  manufacturing, heavy_industry, mining` — `build/zone.go:24-31`), data-priced but
  Go-constant-named; civic building types (school, college, hospital) are **not** in
  this catalogue — they live under `engine.education` / `engine.services`, always
  State-run today.
- **`engine.firms`** already exists (not net-new): `FirmID uint64`, a 4-stage lifecycle
  (`StageStartup/Small/Medium/Enterprise`, `firms.go:29-36`), band data from
  `data/firms.json` (GR#15), and its own `labourmarket.go`. It has no ownership field
  today (no `OwnedBy`) — firms are currently unowned entities.
- **Rent**: `moneycirc.go:361-376` `monthlyRentForHouseholds` is a flat constant
  (`baselineOneMonthlyRentPerHousehold`) with **no landlord, no building link, no tax**
  (D4). It is pure outflow with no counterparty.

The upshot: this design is less "invent new state from nothing" and more "give the
already-existing `OwnerID` and `FirmID` primitives a *type* and a *roll-up*, and give
`Employment` a *building reference* instead of only a coarse `Sector`."

## 1. The ownership graph

### 1.1 Entity types (Owner Kinds)

```
OwnerKind = Person | LTD | LLP | State
```

- **Person** — an existing `citizens.CitizenID`. Persons are the *only* entity that can
  ultimately receive personal income tax or hold personal Wealth. Terminal node.
- **State** — a singleton (there is exactly one State, matching the single
  `AcctTreasury`). Terminal node. Anything owned by State pays no tax to itself in the
  sense of "profit tax" — see Q5 below for the wage/income-tax wash question.
- **LTD** — a private limited company. Maps onto the *already-existing*
  `firms.FirmID`. An LTD is owned by exactly one of {Person, LLP, State} (a
  wholly-owned subsidiary of another LTD is deferred — see §4 Stage 4).
- **LLP** — a partnership: owned by a *set* of {Person} with a profit-share ratio.
  Genuinely new state (Persons don't currently have any group-membership concept) and,
  per the staged plan below, the one entity type recommended for deferral (Q7).

### 1.2 New state required

1. **`OwnerRef` type** — `{Kind OwnerKind, ID uint64}` (ID meaningless for `State`).
   Replaces the bare `OwnerID uint32` on a building/zone with something that carries a
   *kind*, not just a number. Minimal, mechanical change to `engine.build`.
2. **An owner registry** — not a new module, but a small table (could live in
   `engine.firms` since LTD/LLP ownership is inherently a firms concern):
   `ownerOf: map[FirmID]OwnerRef` for firms, plus the per-building `OwnerRef` already
   covered by (1). Persons and State need no registry entry — they *are* terminal.
3. **`citizens.Employment.EmployerRef`** — a new field alongside (not necessarily
   replacing) `Sector`: `{BuildingID uint64, EmployerKey string}`. `BuildingID` is the
   real join key money moves against; `EmployerKey` is the derived
   `<owner-class>\<domain>\<building-type>` label for UI/stats/policy (see §3 — the
   label is *not* the wage-pool join key, only a classification).

### 1.3 The roll-up algorithm

`ResolveTaxableOwner(ref OwnerRef) → (TaxableEntity, chain []OwnerRef)`

- `Person` / `State` → return self (depth 0, terminal).
- `LTD` → look up `ownerOf[FirmID]`, recurse.
- `LLP` → recurse into *each* partner Person, returning a pro-rata split (only matters
  once §4 Stage 4 exists).
- **Depth cap** (Q8): hard-fail (GR#1 registry error, fail-loud not fail-silent) past a
  fixed hop limit (recommend 3: Building → LTD → LLP → Person) and on any detected
  cycle. No open-ended corporate-structuring realism — matches the project's existing
  posture of finite, closed enums (`EmploymentState`, `Sector`) rather than unbounded
  graphs.

## 2. Money flows

All flows below use accounts that **already exist** — no new `finance.AccountID` is
needed for Stage 2/3 given `CatTaxCorp`/`CatTaxCouncil` are already-registered unused
categories. The one structural gap is that `PostWages` needs a **payer-account
parameter** (today hardcoded to `AcctTreasury`) — already flagged in the audit as the
"fix now" plumbing change.

| # | Flow | Payer | Payee | Booking | New? |
|---|---|---|---|---|---|
| 1 | Private wage | Firm (LTD) or Person (sole trader) via the building | Worker (Person) | `AcctFirms → AcctHouseholds`, `CatWages` | Payer param is new; account pair already modeled in `finance` (unused today) |
| 2 | State wage | Treasury | Worker (Person, civil servant) | `AcctTreasury → AcctHouseholds`, `CatWages`, gated to `state\*` employer buildings only | Gate is new (closes D1) |
| 3 | Income tax | Worker (Person) | State | `AcctHouseholds → AcctTreasury`, `CatTaxIncome`, ONE real leg (~28%, replacing both D2i and D2ii) | Replaces two broken legs with one real one |
| 4 | Building revenue | Consumer / market (existing consumption flows) | Owner (LTD's `AcctFirms` bucket, or a Person's Wealth if sole-trader-owned, no LTD wrapper) | Existing revenue path, just needs to be *addressed* to the building's `OwnerRef` instead of a generic firm pool | Addressing is new; the flow itself already exists |
| 5 | Revenue/corp tax | LTD (owner-resolved) | State | `AcctFirms → AcctTreasury`, `CatTaxCorp` — **category already exists, unused** | Wiring only |
| 6 | Rent | Tenant (Person) | Landlord (Person) | **Wealth-to-Wealth transfer, NOT an aggregate-ledger event** — both parties are already inside `AcctHouseholds` in aggregate, so this nets to zero at the pooled-account level; needs a per-citizen transfer primitive parallel to the existing `distributeWagesToResidents` pattern, not a new account | New primitive, no new account |
| 7 | Rent tax | Landlord (Person) | State | `AcctHouseholds → AcctTreasury`, `CatTaxIncome` (rental income taxed as ordinary personal income — see §5 simplification) | Wiring only, reuses (3)'s category |
| 8 | LTD profit → owner (Person/LLP) | LTD | Person or LLP | `AcctFirms → AcctHouseholds` (owner is Person) or an internal firm-to-firm transfer (owner is another LTD/LLP node) | New — dividend leg; **not** taxed again in the first cut (Q2) |

**Key architectural point on flow 6**: because `AcctHouseholds` is a single pooled
account across every citizen, a Person→Person transfer (rent, and eventually LLP
profit-splits) is invisible to the aggregate ledger no matter what — it only shows up
in the *per-citizen* `Wealth` ledger, exactly like the existing (buggy) wage
distribution already does. The **tax** on that transfer (flow 7) is the only leg that
needs to touch the pooled `AcctHouseholds → AcctTreasury` ledger. This means rent
itself is cheap to add; only rent *tax* needs real ledger plumbing.

## 3. The employer-ID scheme

`<owner-class>\<domain>\<building-type>` — e.g. `state\education\college`,
`private\retail\shop`, `state\health\hospital`, `private\finance\office`.

- **owner-class** = `state` | `private` (a private LLP/LTD/Person-owned building is
  simply "private" at this granularity — the class label does not distinguish LTD from
  sole-trader; that distinction lives in the `OwnerRef.Kind` on the building, not the
  string).
- **domain** = the service/industry domain (`education`, `health`, `retail`, `finance`,
  `manufacturing`, …) — a coarser grouping than the 8-way `ZoneType` catalogue, closer
  to `Sector` today, needed because "office" alone doesn't say what kind of office.
- **building-type** = the specific catalogue entry (`college`, `shop`, `office`, …).

**This string is a derived UI/stats/policy label, never the wage-paying join key**
(see Q6) — the actual money always moves against the concrete `BuildingID` /
`FirmID`, because two colleges must be able to have independent finances (one can go
under while the other doesn't) even though both render as `state\education\college`.

Citizens attach via `Employment.EmployerRef{BuildingID, EmployerKey}` — `BuildingID`
resolved once at hire time (or on `staffing`'s existing `AssignedCitizens` mechanism)
and cached; `EmployerKey` recomputed from the building's catalogue entry + `OwnerRef`
for display, never persisted as authoritative.

## 4. Staged plan

**Stage 0 — today (broken).** Documented fully in the audit doc. Treasury pays 100% of
wages; fake 100%/vanishing-28% tax; no rent counterparty; no ownership types.

**Stage 1 — fix now, no new ownership types (already scoped in the audit's priority
split, sequenced after BUG-547).** Gate `PostWages`'s existing Treasury-payer path to
`Sector==SectorPublic` only; add the `AcctFirms→AcctHouseholds` private-wage leg keyed
by `Sector` (not yet per-building — `Sector` is the existing coarse dispatch key,
already on every citizen); collapse the two broken tax computations (D2i/D2ii) into
ONE real ~28% `CollectTax` leg that actually posts to Treasury. Rent stays the flat
stub. **No `OwnerRef`, no `EmployerRef`, no roll-up yet** — this closes the D1/D2/D3
bugs with plumbing only, matching the audit's own recommendation (b) as the pragmatic
first cut.

**Stage 2 — owner-per-building, depth-1 (the first REAL cut of Aaron's model).**
Upgrade `build`'s bare `OwnerID uint32` to `OwnerRef{Kind, ID}`, restricted at this
stage to `Person` or `State` only (no LTD/LLP yet — depth is always 0 or terminal).
Every building has exactly one direct owner. Wage/revenue flows route by that owner's
`Kind`: `State`-owned buildings behave like today's civil-service path; `Person`-owned
buildings pay/collect directly against that Person's own Wealth/`AcctFirms` bucket —
no roll-up needed because depth is 1. **This is also where real rent becomes possible**
(flow 6/7 above): a residential building owned by a Person, tenanted by a different
Person, is the landlord/flat case (worked example d, below). Add `Employment.EmployerRef`
to citizens so wages target a concrete building rather than only a `Sector` bucket.

**Stage 3 — LTD/LLP roll-up (ties into the already-existing `engine.firms`).** Add
`LTD` and `LLP` as valid `OwnerKind`s; a building's owner can now be a `FirmID`; a
firm's owner can be a `Person` or (once built) an `LLP`. Roll-up walks the chain with
the depth cap from §1.3. Corp tax posts at the firm level (`CatTaxCorp`, wiring an
already-registered but unused category) before the dividend leg (flow 8) distributes
the remainder to the ultimate Person/State.

**Stage 4 — depth/realism polish (explicitly later, not blocking Baseline One).**
Rentier Persons as a distinct investor archetype (own multiple flats without living in
any of them); LLP multi-partner profit splits; dividend double-taxation policy (if
Aaron later wants one); firm-type-specific tax treatment nuances; building-type
granularity beyond the current 8-way `ZoneType` catalogue for civic buildings
(per-instance vs per-type employer identity, Q6).

## 5. "Realistic, not a UK-tax clone" — deliberate simplifications

- **One flat income-tax rate**, no bands/personal-allowance thresholds, no separate
  employee-NI vs income-tax split (today's `IncomeRate` collapses to one number by
  design — keep it that way).
- **No employer's-NI equivalent** — wage cost to the firm/State is the gross wage,
  full stop; no separate payroll-tax leg on top of the wage itself.
- **No dividend tax** — an LTD's profit is taxed once, at the firm (`CatTaxCorp`); the
  distribution to its Person/LLP owner is untaxed (Q2). Avoids modeling a
  dividend-tax band on top of corp tax.
- **No depreciation, capital allowances, or loss carry-forward** for firms — profit is
  simply revenue minus wages minus (existing) opex/construction-financing costs this
  tick.
- **Rent stays a data-driven baseline, not a negotiated/market rate** — Stage 2 keeps
  the existing flat-stub constant (scaled by a data-file per-zone-type multiplier, GR#15,
  never a new Go literal), deferring true supply/demand market rent to a separate
  housing-market epic rather than conflating it with ownership plumbing.
- **Sales tax and council tax stay exactly as they are today** — `CatTaxSales` and
  `CatTaxCouncil` already exist and are out of scope for this deep-dive; this design
  only touches wages/income-tax/corp-tax/rent.
- **Roll-up depth capped** (§1.3) — no infinite holding-company-of-holding-companies
  realism.

## 6. Worked examples — every £ traced

### (a) Corner shop, sole trader

Building: `private\retail\shop`, `OwnerRef{Person, P1}` (Stage 2, no LTD wrapper).
Two employees, Person W1/W2, gross wage £1,600/month each.

1. Shop revenue (consumption module) → £8,000 credited to P1's shop bucket (`AcctFirms`
   addressed to P1, flow 4).
2. Wages: P1's shop pays W1+W2 £3,200 total → `AcctFirms→AcctHouseholds`, `CatWages`
   (flow 1).
3. W1/W2 each pay income tax on their £1,600 (~28%, £448 each = £896 total) →
   `AcctHouseholds→AcctTreasury`, `CatTaxIncome` (flow 3).
4. P1's remaining margin (£8,000 − £3,200 = £4,800, ignoring input costs for
   brevity) is P1's own **personal** self-employment income (no LTD, no corp tax) →
   P1 pays income tax on it at the same rate as W1/W2 (flow 3, same category — Q4).
5. Net: Treasury receives income tax on £1,600+£1,600+£4,800; P1 keeps £4,800 net-of-tax
   as personal Wealth; W1/W2 each keep £1,600 net-of-tax as personal Wealth.

### (b) Chain supermarket, LTD owned by a Person

Building: `private\retail\supermarket` (Stage-4 finer type; Stage-2/3 reuses the
`shop` zone at `firms.StageEnterprise`), `OwnerRef{LTD, F1}`; `F1`'s `ownerOf` entry is
`OwnerRef{Person, P2}`. 50 employees, £1,800/month gross each = £90,000/month wage bill.

1. Supermarket revenue → £500,000 credited to F1's `AcctFirms` bucket (flow 4).
2. F1 pays 50 employees £90,000 → `AcctFirms→AcctHouseholds`, `CatWages` (flow 1); each
   employee pays ~28% income tax (£504/head, £25,200 total) → Treasury (flow 3).
3. F1's pre-tax profit after wages and opex (say £150,000 for this example) pays corp
   tax (`CatTaxCorp`, flow 5) — e.g. at a lower rate than income tax, say 19%:
   £28,500 → Treasury.
4. F1's remaining after-tax profit (£121,500) is **fully distributed** this tick
   (Stage-2/3 default, Q1) to its resolved owner P2 → `AcctFirms→AcctHouseholds`, an
   untaxed dividend leg (flow 8, Q2) → P2's personal Wealth.
5. Net: Treasury receives income tax (employees) + corp tax (F1); P2 receives £121,500
   untaxed dividend income this tick; the 50 employees receive net wages.

### (c) Council school, State-run

Building: `state\education\college` (Aaron's own example), `OwnerRef{State}`.
30 teachers, Person T1..T30, gross wage £2,000/month each = £60,000/month.

1. No "revenue" — education is a public good, not sold; no flow 4/5 exist for this
   building at all.
2. State pays T1..T30 £60,000 total → `AcctTreasury→AcctHouseholds`, `CatWages`, gated
   to the `state\*` employer key (flow 2, Stage-1 fix).
3. T1..T30 each pay income tax (~28%, £560/head, £16,800 total) →
   `AcctHouseholds→AcctTreasury`, `CatTaxIncome` (flow 3) — **this is the Treasury
   paying itself back through 30 Persons; whether it's modeled as two real legs or
   skipped as a wash is Q5, open.**
4. Net (if modeled as real legs): Treasury nets −£43,200 this tick funding the college
   (its wage outlay minus the tax it claws back), which is the correct fiscal picture
   for a State employer — the same shape as the private case, just with State as both
   payer and ultimate tax receiver.

### (d) Flat rented to a worker

Building: `private\dwelling\flat`, `OwnerRef{Person, L1}` (landlord, does not live
there). Tenant: Person W3, who has a separate job (say the corner shop above, W1).
Monthly rent: £700.

1. W1's wage/income-tax legs run exactly as in example (a) — unaffected by rent
   (orthogonal flows, Q10).
2. Rent: W1 (as tenant) → L1 (as landlord), £700/month — a **Wealth-to-Wealth transfer
   only**, invisible to the aggregate `AcctHouseholds` total since both parties are
   inside it (flow 6, §2's key architectural point).
3. Rent tax: L1 pays income tax on the £700 rental income (~28%, £196) →
   `AcctHouseholds→AcctTreasury`, `CatTaxIncome`, reusing the same category as wage
   income tax (flow 7, Q3 for the rate/basis, treating rental income as ordinary
   personal income for simplicity per §5).
4. Net: L1 nets £504/month rental income after tax on top of L1's own employment (or
   lack of it — L1 could be `EmploymentRetired` and still owe this tax, Q10); W1 nets
   wages minus income tax minus the £700 rent outflow.

## 7. Follow-up questions for Aaron

1. **Dividend distribution timing.** Does an LTD retain profit and distribute on some
   cadence, or is 100% of after-tax profit swept to the owner every tick (no retained
   earnings)? *Example:* the supermarket nets £121,500 after corp tax this month —
   does P2's Wealth jump £121,500 this tick, or does F1 bank it for later reinvestment?
   **Recommendation: fully distribute every tick for the first cut** — simplest, no new
   "retained earnings" balance-sheet line; revisit only if `engine.firms`'s existing
   lifecycle mechanics need a cash buffer for expansion.

2. **Dividend taxation.** Is the LTD→Person dividend taxed again as personal income, or
   is corp tax the only levy on business profit? *Example:* P2's £121,500 already paid
   corp tax at F1 — does P2 pay income tax on it too when it lands in Wealth?
   **Recommendation: no second tax** — one real tax leg per flow, matching the audit's
   own "collapse to ONE real leg" principle; avoids modeling dividend-tax bands.

3. **Rent basis.** Is rent a % of the building's own modeled construction/replacement
   value (from `data/buildings.json`), a flat per-zone-type rate (today's stub), or a
   true market-clearing rate driven by housing supply/demand? *Example:* a `dwelling`
   flat vs a `farming`-zone worker's cottage — same rent today, should they differ?
   **Recommendation: keep the existing flat baseline for Stage 2, scaled by a
   per-zone-type data-driven multiplier (GR#15, never a Go literal)** — defer true
   market-rate rent to a separate housing-market epic so it doesn't block the
   ownership plumbing.

4. **Self-employed vs LTD wrapper.** Does a sole-trader Person-owned building (the
   corner shop) pay corp tax like an LTD, or personal income tax on its net revenue
   with no corp-tax leg at all? *Example:* P1's £4,800 shop margin — `IncomeRate` or
   `CorpRate`? **Recommendation: personal income tax whenever a building is owned
   directly by a Person (no LTD in the chain)** — matches real self-employment, needs
   no new category, and corp tax only ever applies once an LTD node exists.

5. **Civil-servant wash.** Does the State pay a teacher and then really collect income
   tax back from that same teacher as two conserved ledger legs, or is it modeled as a
   single net wage (skip the round-trip)? *Example:* a teacher's gross £2,000 — does
   Treasury pay £2,000 and claw back £560, or just pay £1,440 net with no tax leg?
   **Recommendation: model it as two real legs, same code path as private wages** — one
   `CollectTax` call regardless of employer avoids a special-cased "public employees
   are pre-netted" branch and keeps income-tax revenue reporting uniform.

6. **Employer-ID granularity.** Is every college one employer
   (`state\education\college`), or does each individual college building get its own
   instance-scoped identity so two colleges' finances/staffing aren't lumped together?
   *Example:* two colleges in the city — can #42 go bankrupt/downsize independently of
   #17 if they share one string key? **Recommendation: the string is a derived
   classification label only; the actual wage-paying join key is always the concrete
   `BuildingID`/`FirmID`** — no two buildings ever share a wage pool regardless of
   sharing a label.

7. **LLP necessity now vs later.** Does Metropolis need a genuinely distinct LLP entity
   in the first stages, or can it be deferred and later modeled as "an LTD with a
   fixed-ratio multi-Person owner list" (same roll-up code, different cardinality)?
   *Example:* a firm owned 50/50 by two Persons — LLP from day one, or Stage-3+ only?
   **Recommendation: defer LLP to Stage 3/4** — none of the four worked examples need a
   partnership; ship single-owner LTD + Person + State first.

8. **Roll-up depth/cycle limit.** What's the maximum ownership-chain depth the engine
   should support, and what should happen if a save somehow creates a cycle (firm A
   owns firm B owns firm A)? *Example:* Building → LTD → LLP → Person — is that the
   ceiling? **Recommendation: hard cap at 3 hops with a fail-loud GR#1 registry error on
   overflow or cycle detection** — matches the project's existing closed-enum posture
   rather than open-ended corporate-law modeling.

9. **State-owned firms.** Can the State own an LTD (a council-run trading company,
   e.g. a leisure centre run at arm's length), or is State strictly a direct building
   owner with no firm wrapper ever? *Example:* a council leisure centre —
   `state\leisure\centre` with State as direct owner (like a school), or a
   council-owned LTD rolling up to State instead of a Person? **Recommendation: allow
   `State` as a valid terminal `OwnerKind` at ANY level of the roll-up, not just on
   buildings** — costs nothing extra in the roll-up algorithm and unlocks
   council-owned-enterprise depth later with no redesign.

10. **Ownership income vs `EmploymentState`.** Does a Person who owns no building and
    is `EmploymentUnemployed`/`Retired` still get taxed on rent/dividend income they
    receive as an owner, independent of their own employment status? *Example:* a
    retired Person who owns 3 rental flats — do they still owe rent-income tax despite
    being `EmploymentRetired`? **Recommendation: yes — taxation attaches to the FLOW,
    never to the recipient's `EmploymentState`**; `EmploymentState` only gates whether
    a Person *receives a wage*, keeping the tax model orthogonal to the employment
    model (GR#3, no duplicated eligibility logic in two places).
