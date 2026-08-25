# Fiscal circuit brief (FEAT-225 / FEAT-228)

**Promoted 2026-08-22 by Bev** from the Google Drive-only copy at `docs/design/bank.txt`.
This is a **source brief**, not a dispatch to mint a second BoW. Live tracking: FEAT-225 (occupation matrix + M1 + LIBOR), FEAT-228 (this promotion), BUG-355 (the wiring-poor diagnosis is now a filed bug).

Sister notes still on Drive only: `E:\GoogleDrive\Projects\Metropolis\pay\gemini-code-*.md` (occupation matrix, generosity slider). Do not let those rot off Drive — copy them here when FEAT-225 starts.

---
# Claude Code Brief â€” Metropolis Fiscal Engine: Deep Plan & Book of Work

## Your task

You are planning, not coding. Do **not** write implementation code in this pass beyond
signatures and type definitions needed to make an acceptance criterion testable.

Produce:

1. A set of **Book of Work (BoW) items**, one per discrete ask below, each with its own
   Acceptance Criteria.
2. A **`bank.md`** file at the repo root that is the single cross-reference register for
   every BoW item, its dependencies, the modules it touches, and the invariants it must
   not break.

Work in this order: read the codebase â†’ interrogate the asks â†’ resolve the known defects
listed at the bottom â†’ *then* write ACs. An AC written before the defect list is resolved
will encode the defect.

## Context

Metropolis is a turn-based text city-builder rendered in a Windows terminal
(box-drawing, ANSI colour heatmaps). Engine is Go. The map is a 1 km tile at Folkestone,
Kent with real topography, all man-made features stripped except the motorway. Player
starts at zero population with seed capital and loans available.

The current state of the economy work is **plan-rich, wiring-poor**: `engine.tax` has an
instrument panel, wages post as a single aggregate via `PostWages(total Money)`, citizens
consume food in logic but never pay for it, and nothing is driven by the game tick. The
economy is a ledger sitting in memory, not a circuit.

## Architectural direction to plan against

**Client/server split.** Heavy simulation moves to a cloud backend; the Windows terminal
becomes a thin client that does UI layout, box-drawing, heatmaps, keyboard input, and
rendering of streamed state. It does zero simulation.

**Backend** runs the Go simulation engine, the tiered population model (5-citizen micro
blocks up to 10,000-citizen macro sectors), Monte Carlo loops, deterministic ticks, and
the economic ledgers.

**Transport** is low-latency bidirectional â€” gRPC or WebSockets. State updates (notice
feeds, well-being scores, the finance report) stream down; player commands (fund a
library, change a tax rate) go up.

**State** persists server-side via a distributed cache (Redis) or an in-memory Go snapshot
engine. Bit-exact determinism must hold server-side regardless of which is chosen.

Before writing ACs for any of the above, state explicitly which of gRPC vs WebSockets you
are recommending and why, and which of Redis vs in-memory snapshot, with the determinism
argument for each. Do not leave these as "or".

## Non-negotiable invariants

Every BoW item must be checked against these. Any item that can violate one must say in
its AC how it doesn't.

- **INV-1 Conservation.** At the end of every fiscal tick, the sum of all individual
  `Citizen.Wealth` values must exactly equal the `households.wealth` aggregate in the
  macro-ledger. No drift, no missing pennies. Violation is a fatal test panic, not a
  warning.
- **INV-2 Bit-exact determinism.** Same seed plus same input command sequence produces a
  byte-identical state hash, including under goroutine parallelism.
- **INV-3 Integer money.** All currency is `int64` micro-pounds. No floats anywhere in the
  fiscal path.
- **INV-4 Memory budget.** Citizen records stay tightly packed. State the actual
  `unsafe.Sizeof` of the struct and the total array footprint at target population â€” do
  not assert a byte count without measuring it.
- **INV-5 Strict ordering.** The fiscal pipeline order is fixed and unalterable:
  Wages â†’ Direct Taxes â†’ Mandatory Spend â†’ Disposable Income & Sentiment â†’
  Discretionary Spend â†’ City Opex.

## The asks

Create at least one BoW item per numbered ask. Split any ask that has more than one
independently testable outcome.

### Epic 1 â€” Fiscal Circuit Orchestrator (`FEAT-082`)

The master deterministic sequence for the financial month. `ExecuteFiscalMonth()` fires on
the final tick of every in-game month and drives the whole pipeline in INV-5 order,
returning a `FinancialReport` and a notice feed. Until this exists the economy is frozen.
Plan the orchestrator as a batch job: it iterates the citizen arrays, updates individual
wealth scalars, and reconciles the sum against the macro-ledger every tick.

### Epic 2 â€” Wage Distribution (`MOD-022.A`)

Translate the aggregate wage bill into individual balances. Compute the total macro wage
pool from `firms.cash` (private) and `city.treasury` (public sector jobs). Iterate employed
citizens, add an integer micro-pound amount to `Citizen.Wealth`, stratified by
`IncomeBandFor(wealth)` tier and sector â€” Tech and Retail do not draw equal shares.
Unemployed citizens get Â£0 here; welfare is a separate concern. Integer division of a pool
across a population leaves a remainder â€” the AC must say exactly where the remainder goes
and prove INV-1 still holds.

### Epic 3 â€” Direct Taxation (`MOD-052.A`)

Apply the `engine.tax` panel to citizen wealth and roll it up to the treasury.

- Income Tax (PAYE): deduct the player-defined percentage from the monthly wage before it
  settles into base wealth.
- Council Tax: deduct the flat/banded fee directly from `Citizen.Wealth`.
- Wealth below Â£0 puts the citizen in arrears (debt state) with a severe immediate
  happiness penalty.
- All deducted micro-pounds aggregate into a single credit transaction to `city.treasury`.

**Strategy: Look-Up Tables.** Wages are fixed per `JobTier`, so PAYE due is a fixed integer
per tier. Council Tax keys off a `HousingBand` (0â€“4, 0 = no property). Build both LUTs in
an O(1) pre-pass; the O(N) loop then does array lookup and integer subtraction only, with
sequential memory access and branch-predictable conditionals.

**Parallelism.** The extraction function must accumulate into a local `int64` and return
it rather than touching a global treasury, making it thread-safe. The orchestrator slices
the citizen array into fixed chunks, runs them on goroutines, and sums the returns. The AC
must pin chunk boundaries and summation order so INV-2 holds â€” "sum the returned integers"
is not sufficient if the order can vary.

### Epic 4 â€” Mandatory Consumption & Rent (`MOD-021.A`)

Citizens pay for Maslow tier-1 needs before they have anything discretionary.

- **Rent/mortgage**: localised housing cost derived from the citizen's tile land value,
  expressed as a `RentByBand[5]` LUT.
- **Utilities & food**: flat baseline for water, electricity, and daily food staples.
  *The source notes give food as both 1.4 kg/day and 2.1 kg/day â€” pick one, state which
  and why, and make it a named constant.*
- **Transit**: monthly pass or daily fares by `CommuteMode` (walk/none, bus, train, car),
  costed from the `engine.transport` logit model.
- Deductions debit `Citizen.Wealth` and credit `firms.cash` (rent, retail, food) or
  `city.treasury` (state transit). Two accumulators, two macro credits.
- A citizen who cannot cover basic needs gets a `BasicNeedsUnmet` flag â€” distinct from
  `InArrears`, see defect D-1.

### Epic 5 â€” Disposable Income & Sentiment

Disposable income is a **derived metric computed on the fly**, not a stored field. Do not
bloat the citizen struct with it.

- Build `CalculateDisposableIncome(c *Citizen, macroState *EconomyState) int64`, called by
  the Sentiment Engine per citizen or per block.
- Zero-bound trigger: if disposable â‰¤ 0, apply the âˆ’12 happiness penalty.
- Regressive-tax listener: if a low-wealth citizen is hit with a high flat Council Tax,
  generate a complaint.
- Positive disposable income is partially spent on leisure/tourism, generating VAT that
  rolls up to `city.treasury`.
- See defect D-2 â€” there are two contradictory formulas in the source notes.

### Epic 5b â€” Notice Board feed

Procedural flavour text that names the actual cause. The player should read
*"Working a Tier 1 job and I still can't afford groceries in District 4"* and know to cut
tax in District 4, zone cheaper housing, or lift Tier 1 wages. Templates are selected from
citizen data â€” `DistrictID`, `JobTier`, `HousingBand`, `CommuteMode` â€” so a car commuter
complains about fuel and tolls, a Band 4 renter complains about rent.

String allocation is slow, so generation is gated behind a low sampling probability while
the integer happiness maths runs over the whole array at full speed. The resulting feed is
a few dozen strings â€” a light payload for the stream to the terminal. See defect D-3 on
the sampling arithmetic and D-4 on `rand` and determinism.

### Epic 6 â€” City Opex, Insolvency & Player Roll-up (`FEAT-094`)

Settle the city's running costs and produce the player's actionable budget.

- Debit `city.treasury` for public services (police, fire, hospitals, schools),
  infrastructure maintenance (roads, water, grid), and loan interest.
- Treasury below Â£0 triggers municipal insolvency â€” **not** an instant game over. Emergency
  state instead: forced IMF loans at punitive interest (`FEAT-057`) or auto-slashing of
  public services. Specify which, and the trigger thresholds.
- F2 Finance screen heatmap: GREEN positive net income, AMBER burning reserves,
  RED insolvent.
- Display available Capex for next month's builds. The source assumes only a fraction of
  treasury is liquid for Capex â€” justify the fraction or make it a tunable constant.

### Epic 7 â€” Transport & client contract

Define the wire contract for `FinancialReport` and `NoticeFeed`: schema, versioning,
update cadence, and behaviour on client reconnect mid-tick. The client renders; it never
recomputes. Include a fallback for a local single-process mode so the game is testable and
playable without the cloud fleet.

## BoW item schema

Write each item to `docs/bow/<ID>.md` using exactly this shape.

```markdown
# <ID> â€” <Title>

**Epic:** <n>  |  **Status:** Not Started  |  **Owner:** unassigned

## Requirement
<One paragraph. What must be true when this is done.>

## Context
<Why this doesn't work today. Reference the actual current code â€” file and function.>

## Scope
In scope: <bullets>
Out of scope: <bullets â€” be explicit, this is where scope creep dies>

## Interfaces
<Function signatures and type definitions this item introduces or changes.>

## Acceptance Criteria
- [ ] AC-1 <Observable, testable, no adverbs. "Fast" is not an AC; a Âµs budget is.>
- [ ] AC-2 ...

## Invariants touched
INV-1, INV-3 â€” <how this item proves it doesn't break them>

## Dependencies
Blocked by: <IDs>
Blocks: <IDs>

## Test strategy
<Unit, property-based, and determinism-replay tests. Name the conservation assertion.>

## Open questions
<Anything you could not resolve from the codebase. Do not invent an answer.>
```

Rules for ACs:

- Every AC is independently verifiable by a test that could fail. If you cannot describe
  the failing case, it is not an AC.
- No AC may contain "should", "as appropriate", or "where possible".
- Any AC involving money includes the conservation assertion.
- Any AC involving parallelism includes the determinism replay assertion.
- Performance ACs carry a number and the population at which it is measured.

## `bank.md` â€” the cross-reference register

Create `bank.md` at repo root. It is the index everything else hangs off, and it is
regenerated, never hand-patched. Include:

1. **Header** â€” what the file is, that it is generated, and the command that regenerates
   it.
2. **Master table** â€” every BoW item: ID, title, epic, status, blocked-by, blocks,
   modules touched, invariants touched, AC count.
3. **Dependency graph** â€” a Mermaid `graph TD` of blocked-by edges. Explicitly assert
   the graph is acyclic, and if it isn't, say where the cycle is rather than hiding it.
4. **Module â†’ item map** â€” for each package (`engine.tax`, `engine.transport`,
   `engine.market`, `finance`, `ui`, `transport`), which items modify it. This is the
   merge-conflict early-warning system.
5. **Invariant â†’ item map** â€” for each of INV-1..INV-5, which items could violate it and
   which test guards it.
6. **Legacy ID map** â€” `FEAT-082`, `FEAT-094`, `FEAT-057`, `MOD-021.A`, `MOD-022.A`,
   `MOD-052.A` mapped to the new BoW IDs, so the older planning docs stay navigable.
7. **Open questions register** â€” every unresolved question from every item, in one place,
   with the item that raised it.
8. **Defect register** â€” D-1..D-n below plus anything you find, with resolution and the
   item that carries the fix.

Every BoW file links back to `bank.md`; `bank.md` links to every BoW file. Broken links
are a build failure.

## Known defects in the source design â€” resolve before writing ACs

These came from the earlier design pass and are wrong or underspecified. Do not carry them
forward. For each, record the resolution in the `bank.md` defect register.

- **D-1 â€” `BasicNeedsUnmet` is a tautology.** The proposed check
  `if citizens[i].Wealth + totalBurden < totalBurden` reduces to `Wealth < 0`, which is
  already the enclosing branch. As written the flag is identical to `InArrears` and carries
  no information. Define the real condition: the citizen's pre-deduction wealth was
  insufficient to cover *subsistence specifically*, which needs the subsistence component
  tested separately from the total burden, and probably needs deduction priority ordering
  (rent before food? food before transit?). Decide the priority order â€” it is a design
  choice with gameplay consequences.
- **D-2 â€” Two contradictory disposable income formulas.** The notes give both
  `Wages âˆ’ (Taxes + RentBurden + EssentialSpend)` and
  `MonthStartWealth + Wages âˆ’ (Taxes + MandatorySpend)`. The first is a pure flow, the
  second mixes a stock into a flow, so a citizen with savings never registers as
  struggling. Pick one, and say what the âˆ’12 penalty is actually meant to detect: monthly
  cashflow distress, or destitution.
- **D-3 â€” Notice sampling arithmetic doesn't hold.** A 0.00001 probability over a
  100-million citizen array yields roughly 1,000 notices per tick, not the ~50 the notes
  claim. Either fix the probability, or switch to a reservoir sample of fixed size k,
  which is both deterministic-friendly and bounds the allocation exactly. Recommend one.
- **D-4 â€” `rand.Intn` breaks INV-2.** Template selection using the global `math/rand`
  source is not reproducible across replays or safe across goroutines. Replace with a
  per-tick seeded deterministic PRNG whose seed derives from the tick number and a chunk
  index.
- **D-5 â€” `InArrears` is clobbered.** `ExtractTaxes` sets `InArrears = false` in its else
  branch, so a citizen who entered the tick in debt is silently cleared mid-pipeline, and
  the flag's meaning depends on which phase last touched it. Decide whether arrears is a
  per-tick assessment or a persistent state, and if persistent, only ever set it â€” never
  clear it outside an explicit resolution step.
- **D-6 â€” Integer division loses pennies in three places**: the PAYE calculation
  (`wagePayouts * rate / 100`), the wage pool distribution, and the Capex fraction
  (`Treasury / 2`). Each truncation is deterministic, which is fine, but the lost
  micro-pounds must be routed somewhere explicit or INV-1 fails. Define a single rounding
  and remainder-sweep policy and apply it uniformly.
- **D-7 â€” Struct size claims are unverified.** The notes assert "still fits in 24 bytes"
  and "well under 32 bytes" for structs that grew fields between revisions. Measure with
  `unsafe.Sizeof`, account for alignment padding, and state the real total array footprint
  at target population. If the footprint is the reason for the cloud backend, the number
  needs to be right.
- **D-8 â€” Target population is unstated as a requirement.** "100-million citizen" appears
  in the architecture notes, but the game starts at zero population on a 1 km tile and
  grows to a megacity. Establish the actual ceiling as a hard requirement with the memory
  and tick-budget consequences, because it drives every other decision here. If the real
  ceiling is far lower, most of this architecture is over-built and you should say so.

## Definition of done for this planning pass

- Every ask above has at least one BoW item; no item covers more than one testable
  outcome.
- Every defect D-1..D-8 has a recorded resolution and an owning item.
- `bank.md` exists, all links resolve both ways, and the dependency graph is acyclic.
- Every open question is in the register rather than silently answered.
- You have stated, in plain terms, which parts of this design you think are wrong or
  premature. Do not ratify the plan because it was handed to you.
