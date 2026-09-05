BOW code: FEAT-141

# Acceptance criteria — FEAT-141 Dynamic Food & Groceries Tonnes Supply Chain and Household Refuse Loop

**BOW code:** FEAT-141
**Spec refs:** § Food & Groceries Tonnes Supply Chain (`docs/planning/acceptance/FEAT-141` this file)
**Date:** 2026-09-05 (BA-S10a, first pass)
**Status:** draft-pending — FEATURE BLOCKED on MOD-050 (engine.shopping) status and edge registration; see Escalations and GR#25 edge-conformance section below.
**Dependencies:** MOD-050 (engine.shopping, REWORK in_progress), MOD-039 (engine.refuse, open), MOD-025 (engine.logistics, ready), MOD-068 (engine.freight, open), MOD-021 (engine.consumption, ready), MOD-019 (engine.finance, ready)

## Feature Summary

This feature integrates household food consumption and waste generation into a unified **physical tonnes-based** loop, bridging supply (food/grocery imports via freight and logistics), retail distribution (supermarket/warehouse stock), household demand (tonnes consumed per person per month from data coefficients), and waste generation (consumed tonnes become refuse tonnes). The core mechanic is **mass conservation**: food tonnes consumed by households must map deterministically to refuse tonnes generated, collected on a scheduled cycle, with uncollected refuse affecting health and wellbeing.

## User Stories

- **US-1.** As a household, I need food/grocery consumption to be measured in physical tonnes per month (not abstract satisfaction %), so the supply-chain mechanics are grounded in real throughput constraints, not metaphor.
- **US-2.** As the logistics system (`engine.logistics`, a registered outbound consumer), I need food/grocery stock replenishment orders to flow through the same `LogisticsAPI` movement machinery (per `engine.logistics.md` AC-4's "reuse, not parallel types" principle), so food supply is not a special case but a commodity like any other.
- **US-3.** As the refuse system (`engine.refuse`, a registered outbound consumer), I need household-consumed food tonnes to feed refuse generation in a deterministic, independently-verifiable ratio, so the mass-conservation identity `food_consumed == refuse_generated (± water/sewage delta)` is a testable invariant, not a narrative claim.
- **US-4.** As the household wellbeing/health system (`engine.wellbeing`, consumed by `engine.refuse.md` AC-7), I need uncollected refuse to affect health/satisfaction, which is already connected; this feature makes the refuse quantity real (tonnes, not a flag), so health consequences scale with actual overflow magnitude.
- **US-5.** As the player building a city, I need stockouts at supermarkets (caused by logistics shortfalls flowing through from insufficient supply) to be queryable and to cascade consequences (satisfaction, migration), so supply-chain visibility is real end-to-end, not silently absorbed in an aggregate demand figure.

## Scope

**In scope:**
- Food/grocery consumption coefficients (kg/person/day per household class) loaded from `data/consumption.json` (existing per `engine.consumption.md`), converted to tonnes/month for consistency with logistics/refuse.
- Household-generated refuse tonnes derived from consumed tonnes via a documented conversion ratio (data-sourced per GR#15, not a hardcoded constant).
- Refuse accumulation, collection scheduling, and uncollected overflow already modelled in `engine.refuse.md` — this feature wires the food-consumption input to refuse-generation.
- Stock management for food/groceries flowing through `engine.logistics`'s existing `LogisticsAPI` (stock draw, replenishment order, shortage reporting).
- Mass-conservation identity test (demand == supply + shortage, independent terms, per `engine.refuse.md` AC-11 pattern).

**Out of scope:**
- Supermarket/warehouse pricing and profit margins — that stays `engine.market`'s domain (this feature only consumes price queries, never sets them).
- Refuse disposal site allocation, incineration/landfill/composting mechanics — fully owned by `engine.refuse.md` (this feature only feeds tonnage).
- Household income, poverty geography, and food-desert definition mechanics — `engine.shopping.md` owns access scores and food deserts (this feature only provides the tonnes groundwork).
- The specific per-capita consumption coefficients and refuse-conversion ratio figures — balance data pending Aaron's pass (see Escalations).

## Acceptance Criteria

### Functional — Supply side (food import and stock)

- **AC-1 (GR#20, code.json conformance).** Food/grocery supply flows through the registered `engine.logistics` outbound edge (code.json shows `engine.logistics.outbound.calls` exists and includes [no self-reference check needed, logistics is a module], with no new edge required here — food is an existing commodity type). Food tonnes produced/imported enter the logistics order book and movement scheduler as a commodity class (`foodStaples`, `foodFresh` per `engine.market.md` commodity list). Check: `go doc ./internal/engine/logistics Stock` (or equivalent, per `engine.logistics.md` AC-2) confirms a `Commodity` type with at least `foodStaples` and `foodFresh` registered; a passing test orders food tonnes into a warehouse and asserts the stock level rises by the order magnitude (`grep -rn "func Test.*[Ff]ood.*[Oo]rder\|func Test.*[Gg]rocery.*[Ss]tock" internal/engine/logistics/*_test.go`).

- **AC-2 (GR#20, `engine.refuse` boundary).** `engine.refuse` calls `engine.logistics` (per code.json, the "rounds are logistics movements" pattern) to schedule refuse collection rounds. Refuse is modelled as a commodity in the logistics system with the same `Stock`/`Draw`/`Movement` semantics as food — refuse collection is a movement, refuse bins are stocks, shortfall queuing is standard logistics queue behaviour. Check: `grep -n "refuse\." internal/engine/logistics/*.go` (excluding `_test.go`) shows no refuse-specific types or queuing path separate from the general commodity machinery; a passing test schedules a refuse round (a movement), then saturates the junction to trigger queue delay, and asserts the round's in-transit tonnage queue exactly like `engine.logistics.md` AC-4 defines (`grep -rn "func Test.*[Rr]efuse.*[Mm]ovement\|func Test.*[Rr]ound.*[Qq]ueue" internal/engine/logistics/*_test.go`).

- **AC-3 (data-driven, GR#15 — food consumption coefficients).** Household food consumption rates (`kgPersonMonth`, distinct from `kgPersonDay` in `data/consumption.json`) for food-staples and food-fresh are loaded from `data/consumption.json` per household class (residential, office, industrial, etc.). The module does not hardcode consumption rate constants. Check: `data/consumption.json` carries a `foodStaplesKgPersonMonth` and `foodFreshKgPersonMonth` field (or equivalent naming) for every household-bearing class; a passing test loads the file, confirms values are present and positive, then asserts multiplying by occupancy and converting to tonnes yields a sensible figure (`grep -rn "func Test.*[Ff]oodConsumption.*[Dd]ata\|func Test.*[Cc]oefficient.*[Ll]oad" [TBD module]/*_test.go`).

### Functional — Demand side (household consumption)

- **AC-4 (GR#15, data-sourced — no hardcoded demand constants).** Household monthly food consumption in tonnes is computed as: `(foodStaples_kg + foodFresh_kg) / 1000` for a household of known occupancy, where the per-capita-per-month kg figures come from `data/consumption.json` (AC-3), multiplied by the household's occupancy (person count per household). A household's weekly+monthly+annual aggregate demand remains computable and consistent across views. Check: `grep -n "[0-9]+" internal/engine/consumption/consumption.go` (excluding `_test.go`) finds no bare consumption-rate numeric literal outside documented balance-data comments (per GR#15, `engine.consumption.md` AC-11 pattern); a passing test loads fixture data, constructs a 4-person household, and asserts `annual_tonnes = (monthly_kg_per_person × 4 persons × 12 months) / 1000` exactly (`grep -rn "func Test.*[Ff]ood.*[Tt]onnes\|func Test.*[Hh]ousehold.*[Dd]emand" internal/engine/consumption/*_test.go`).

- **AC-5 (data-sourced conversion, GR#15 — refuse generation ratio).** Refuse tonnes generated per month are proportional to food tonnes consumed via a data-configured conversion factor (default ~1.0, representing water loss/spoilage/packaging; exact value pending Aaron's balance pass). The ratio is not hardcoded in source; it lives in `data/refuse.json` as `foodConsumptionToRefuseRatio` (or equivalent) and is applied multiplicatively: `refuse_tonnes_per_month = food_tonnes_consumed × foodConsumptionToRefuseRatio`. Check: `grep -n "foodConsumptionToRefuseRatio" data/refuse.json` or equivalent key present and non-zero; `grep -n "[0-9]\.[0-9]" internal/engine/refuse/*.go` (excluding `_test.go` and doc comments) finds no hardcoded ratio literal (per GR#15); a passing test loads the ratio from data, consumes a known tonnage of food, and asserts refuse generation equals `food × ratio` exactly (`grep -rn "func Test.*[Rr]efuse.*[Rr]atio\|func Test.*[Cc]onversion" internal/engine/refuse/*_test.go`).

### Functional — Refuse loop (collection and accumulation)

- **AC-6 (per `engine.refuse.md` AC-11 — mass-conservation identity, the load-bearing check).** For every accounting period (monthly tick), the following identity holds exactly for the food/refuse system and every district/city-wide aggregate:

  ```
  RefuseGenerated == RefuseCollected + RefuseUncollected(overflow) + RefuseInTransit(en-route rounds) + RefuseDisposalBacklog(landfill/compost queue)
  ```

  All four right-hand terms are computed independently from their own data source — never inferred as a remainder. Check: a passing test runs a synthetic city for multiple months with a mix of successful and missed refuse rounds, independently sums each term from its accessor, and asserts the sum equals `RefuseGenerated` to exact integer tonnage. Specifically:
  - `RefuseGenerated` = sum of all households' `consumed_food_tonnes × foodConsumptionToRefuseRatio` over the period (sourced from consumption records per AC-4/AC-5)
  - `RefuseCollected` = sum of completed round deliveries (sourced from `engine.refuse`'s movement completion ledger per `engine.refuse.md` AC-4)
  - `RefuseUncollected` = current bin overflow state (sourced from `engine.refuse`'s per-cell stock accessor per `engine.refuse.md` AC-2)
  - `RefuseInTransit` = tonnage in active movement (sourced from `engine.logistics`'s movement ledger per `engine.logistics.md` AC-4/AC-5)
  - `RefuseDisposalBacklog` = tonnage queued at landfill/compost sites (sourced from disposal-site queue state per `engine.refuse.md` AC-8/AC-10)

  (`grep -rn "func Test.*[Mm]assConserv.*[Ff]ood\|func Test.*[Rr]efuse.*[Ii]dentity" internal/engine/refuse/*_test.go`). **Lazy implementation this rejects:** computing any term as a remainder (e.g. `RefuseUncollected = RefuseGenerated - collected - inTransit - backlog`) makes the identity tautologically true and hides bugs where an individual term is wrong.

- **AC-7 (per `engine.refuse.md` AC-7 — overflow health chain).** Uncollected refuse from AC-6 triggers the documented health consequence cascade: overflow state → vermin index → physical-health penalty through `WellbeingAPI` (already integrated per `engine.refuse.md` AC-7). This feature only adds the tonnes groundwork; the health wiring already exists. Check: A city with high refuse consumption (many households eating) that misses a round enters the refuse-overflow health penalty (already checked by `engine.refuse.md` AC-7; this feature's new contribution is making the tonnage real, which the AC-6 identity test verifies). No new check needed here — AC-6's test proves tonnage is real, and `engine.refuse.md`'s existing health test proves the overflow → health chain works.

### Functional — Save & Restore

- **AC-8 (save/restore participation).** If new state is introduced (household consumption-to-date, food stock levels), it participates in the save/restore cycle via `int.serializer` following the pattern established by `engine.consumption.md` (which already calls `int.serializer` per code.json) and `engine.refuse.md` (which already calls `int.serializer` per code.json). Specifically:
  - Household cumulative food consumption is persisted as part of each household's citizen record (via `engine.citizens`'s existing `int.serializer` participant, since food is a consumption property).
  - Food stock at warehouses/supermarkets is persisted as part of the logistics stock ledger (via `engine.logistics`'s existing persistence, or by adding `engine.logistics` → `int.serializer` if not already present).
  - Refuse stock/overflow is persisted as part of `engine.refuse`'s existing participant (already in code.json).

  Check: After saving and restoring a city, the `RefuseGenerated` and `RefuseCollected` figures continue from their saved state, not reset; a passing test saves a city mid-month, restores it, advances one more month, and asserts the yearly identity still holds (`grep -rn "func Test.*[Ss]ave.*[Rr]efuse\|func Test.*[Pp]ersist.*[Cc]onsumption" [TBD module]/*_test.go`).

  **AARON DECISION: ENGINE.LOGISTICS SERIALIZER EDGE.** Code.json currently shows `engine.logistics` outbound calls do NOT include `int.serializer`. If food/refuse stock needs to be persisted as part of the logistics ledger (rather than only through consumer-side participants), either: (1) `engine.logistics` needs a new outbound edge to `int.serializer` (register it in master-plan-v2.1.json, regenerate code.json), or (2) food/refuse stock is persisted only by the consumer side (refugees by `engine.refuse`'s participant, food by `engine.shopping`'s participant once it lands — current ACs assume option 2). Recommend option 2 for baseline scope; option 1 is a later refactor if the logistics state itself proves non-recoverable.

### Functional — Tests

- **AC-9 (determinism, GR#21).** Household food consumption, refuse generation, stock draw, and replenishment order scheduling are deterministic functions of `(worldSeed, tick, prior state, commands)` — repeated runs from identical starting state and command sequence produce byte-identical consumption tallies, refuse tonnage figures, and stock levels, across worker counts. Check: `grep -rn "func Test.*[Dd]eterminis" [TBD module]/*_test.go` exists and passes.

- **AC-10 (no wall-clock bounds, SG-7).** `grep -rn "time.Now\|time.Since" [TBD module]/*.go` (excluding `_test.go`) returns no matches — food consumption, refuse generation, and stock draw are driven entirely by simulation tick/month, never wall clock.

- **AC-11 (conservation as the testable assertion, directional numbers only).** The AC-6 mass-conservation test is the verification; it is the only test that *must* pass with exact integer tonnage. All other directional tests (AC-4 stock rising on order, AC-5 conversion ratio applied, etc.) use fixture data and directional assertions, never hard-coded constants or wall-clock upper bounds. Check: test comments on each directional test note that the value is a placeholder and may change during balance tuning.

## Error Handling

- **AC-12 (GR#7).** Querying or drawing a food-commodity type from logistics stock that is not registered in `engine.market.md`'s commodity list (e.g. a misspelled `foodFreshh`), or consuming more refuse tonnage than physically exists in a bin, returns a registry-sourced error (new `MET-E`-range code per GR#7, or assignment TBD) rather than silently clamping or ignoring. Check: `grep -n "MET-" internal/engine/logistics/*.go | grep -E "[Ff]ood|[Rr]efuse"` finds a registry code; passing test coverage for both cases (`grep -rn "func Test.*[Uu]nregisteredCommodity\|func Test.*[Ii]nvalidDraw" internal/engine/logistics/*_test.go`).

- **AC-13 (GR#7).** A `data/refuse.json` entry with a missing or negative `foodConsumptionToRefuseRatio` produces a registry-sourced error at load time, not a silent default substitution. Check: passing test coverage (`grep -rn "func Test.*[Mm]alformed.*[Rr]atio" internal/engine/refuse/*_test.go`).

## Documentation

- **AC-14.** `internal/engine/consumption/doc.go` documents that food consumption coefficients (per household class) are sourced from `data/consumption.json` and feed into the tonnes-per-month calculation for this feature. Check: `grep -n "food\|tonne\|FEAT-141" internal/engine/consumption/doc.go` matches.

- **AC-15.** `internal/engine/refuse/doc.go` or a new `internal/engine/[refuse-or-shopping]/doc.go` documents the food-consumption-to-refuse-generation ratio and cites the mass-conservation identity (AC-6) as the proof-of-correctness check. Check: `grep -n "food.*tonne\|consumption.*refuse\|conserv" internal/engine/refuse/doc.go` (or equivalent) matches.

- **AC-16.** `data/refuse.json` carries a `$comment`/`meta` block citing FEAT-141 and stating the unit of measure for all food/refuse tonnes, consistent with the convention in `data/logistics.json` (per `engine.logistics.md` AC-19). Check: `grep -n "\$comment\|\"meta\"\|tonne" data/refuse.json` matches and states units explicitly.

## Escalations & Assumptions

### AARON DECISION: NEW EDGES REQUIRED (GR#25 — graph-driven specification)

**This feature is BLOCKED on the following new code.json edges being registered** before acceptance criteria prose can be finalized and before dispatch:

1. **`engine.consumption → engine.refuse` (CRITICAL — mass conservation depends on this).**
   - Current state: `engine.consumption` outbound does NOT include `engine.refuse`.
   - Reason needed: Consumed food tonnes must flow to refuse generation deterministically. Without this edge, refuse tonnage becomes an independent data constant rather than a derived consequence of consumption.
   - How it's used: `engine.refuse` queries current-period food consumption tonnes from `engine.consumption` (via a new query surface like `FoodConsumedTonnes(month)` or similar) to compute refuse generation per AC-5.
   - Register in: `master-plan-v2.1.json` add consumption module's `consumesFrom` to include refuse, then regenerate code.json via `tools/plan/generate.js`.
   - AC impact: Once registered, AC-5/AC-6 become executable; until then, AC-5/AC-6 are aspirational prose.

2. **`engine.shopping → engine.logistics` (HIGH PRIORITY — supply-chain visibility requires this).**
   - Current state: `engine.shopping` outbound does NOT include `engine.logistics`; `engine.logistics` inbound does NOT include `engine.shopping`.
   - Reason needed: Supermarkets must draw food stock from logistics replenishment orders. Without this edge, shopping cannot query stock levels or report shortfalls when food runs out.
   - How it's used: `engine.shopping` queries `LogisticsAPI.Stock(foodStaples)` to determine current stock at warehouses serving a district, and `engine.shopping.md`'s own AC-2 (format-access-driven demand) depends on knowing whether a supermarket actually has stock to serve trips.
   - Note: This edge may already be intended in `engine.shopping.md`'s AC-2 ("stock-out consequences for wellbeing/migration expressed only via registered edges") but is not yet registered in code.json.
   - Register in: `master-plan-v2.1.json` add shopping module's `consumesFrom` to include logistics, regenerate.
   - AC impact: AC-1/AC-2 (supply-chain flow) cannot be fully verified without this edge; the test can construct stock levels manually, but real gameplay wiring waits on registration.

3. **`engine.logistics → int.serializer` (MEDIUM PRIORITY — if logistics stock state needs persistence).**
   - Current state: `engine.logistics` outbound does NOT include `int.serializer`.
   - Reason: Food/refuse stock levels at warehouses must survive save/restore. If logistics state is only persisted through consumer-side participants (AC-8 option 2), this edge is not needed. If logistics needs its own participant, this edge must be registered.
   - Decision point: Recommend AC-8 option 2 (consumer-side persistence only) for baseline scope; defer option 1 (logistics' own participant) to a later refactor.
   - AC impact: AC-8 save/restore test can proceed with option 2; option 1 requires this edge.

### Data-driven balance numbers

The following are **NOT spec-stated magnitudes** and are subject to Aaron's balance-tuning pass (GR#15):
- Per-capita monthly food consumption (kg/person/month for foodStaples and foodFresh) in `data/consumption.json` — currently loaded from `engine.consumption.md`'s AC-2/AC-3; this feature assumes those exist and converts to tonnes.
- Food-to-refuse conversion ratio (`foodConsumptionToRefuseRatio` in `data/refuse.json`) — a placeholder until tuned; affects refuse tonnage magnitude.
- Food commodity per-unit price in `engine.market` — drives logistics ordering costs but does not change the AC structure.

### Assumption: MOD-050 rework completion

The feature awaits MOD-050 (engine.shopping) REWORK status change from "in_progress" to something committable. The shopping module's acceptance criteria (`engine.shopping.md`) are draft-ahead but the package itself has no code on disk yet. FEAT-141 can proceed in parallel (this BA's ACs do not depend on shopping's implementation details), but the two features share the edge list and the three new edges above. Recommend: both features move to "ready" once all three edges are registered and both BA files are finalized.

## GR#25 Edge Conformance — Modules & Edges Relied On

**This section lists every module and edge referenced in the ACs above, checked against the current code.json (regenerated 2026-09-01).**

### Existing edges (already registered, ACs can rely on these):

1. **engine.logistics** (MOD-025, ready)
   - Outbound calls: engine.market, engine.traffic, engine.world, foundation.data, foundation.errors, foundation.num
   - Used by AC-1/AC-2: logistics Stock/Draw/Movement API, food and refuse as commodities, junction queue mechanics
   - Status: ✓ edge exists, ACs valid

2. **engine.refuse** (MOD-039, open)
   - Outbound calls: engine.logistics, engine.services, engine.wellbeing, engine.farming, engine.market, foundation.data, foundation.errors, foundation.num, int.serializer
   - Used by AC-2/AC-6/AC-7: refuse rounds as logistics movements, mass-conservation identity, overflow health chain, save/restore
   - Status: ✓ edge exists, ACs valid

3. **engine.consumption** (MOD-021, ready)
   - Outbound calls: engine.world, foundation.data, engine.market, engine.season, engine.finance, foundation.errors, foundation.num, int.serializer
   - Used by AC-3/AC-4/AC-5: food consumption coefficients from data, tonnes-per-household calculation, conversion ratio application
   - Status: ✓ edge exists, ACs valid

4. **engine.market** (MOD-020, ready)
   - Used by AC-1: food commodities (foodStaples, foodFresh) are standard market commodities
   - Status: ✓ edge exists, ACs assume foodStaples/foodFresh are registered commodity types in engine.market

5. **int.serializer** (foundation, always present)
   - Used by AC-8: save/restore via existing consumption and refuse participants
   - Status: ✓ edge exists (both engine.consumption and engine.refuse call it), ACs valid

6. **engine.citizens** (MOD-018, ready)
   - Implicitly used: household occupancy/class data feeds AC-4/AC-5
   - Status: ✓ referenced indirectly, no new edge needed

### NEW edges (NOT currently registered — **ACs BLOCKED until registered**):

1. **`engine.consumption → engine.refuse` (CRITICAL)**
   - Why: AC-5 (refuse generation from consumption), AC-6 (mass-conservation identity using consumed tonnes)
   - Current code.json status: consumption.outbound.calls does NOT include refuse; refuse.inbound does NOT include consumption
   - Blocker: Without this edge, refuse tonnage is decoupled from consumption and becomes an independent constant
   - Register in: master-plan-v2.1.json, consumption's `consumesFrom` or refuse's `inbound` (clarify direction), regenerate code.json

2. **`engine.shopping → engine.logistics` (HIGH PRIORITY)**
   - Why: AC-1/AC-2 (supermarket stock queries, shortfall cascades)
   - Current code.json status: shopping.outbound does NOT include logistics
   - Blocker: Without this edge, shopping cannot query or draw from logistics stock
   - Register in: master-plan-v2.1.json, shopping's `consumesFrom` to include logistics, regenerate code.json

3. **`engine.logistics → int.serializer` (MEDIUM PRIORITY — conditional)**
   - Why: AC-8 (save/restore of logistics stock state)
   - Current code.json status: logistics.outbound does NOT include int.serializer
   - Blocker: Only if logistics state needs its own persistence participant; AC-8 option 2 (consumer-side persistence) avoids this
   - Recommendation: Defer to later refactor; baseline scope uses option 2

### Modules NOT used (no edges needed):

- engine.build (MOD-026): not used in this feature (no construction-material supply-chain integration)
- engine.services (MOD-033): refuse collection is owned by engine.refuse; service-level quality already wired per engine.refuse.md AC-4
- engine.world (MOD-017): used indirectly by logistics (junctions) and consumption (network geometry), no new edge needed
- engine.finance (MOD-022): used indirectly (bill tracking in AC-20 of consumption, not this feature), no new edge needed

---

## Summary for Aaron

**AC count:** 16 numbered ACs + 3 "AARON DECISION" items in Escalations  
**Status:** Feature blocked on GR#25 edge registration; prose is draft-ahead but executable only once all three NEW edges above are registered in code.json.  
**New edges required:** 3 total (1 critical, 1 high, 1 medium/conditional)  
**Critical path item:** `engine.consumption → engine.refuse` (mass conservation depends on this)

**Next step:** Architect (Bev) to confirm edge registration plan with Aaron; once edges are in code.json, this BA file becomes immediately actionable for dispatch.
