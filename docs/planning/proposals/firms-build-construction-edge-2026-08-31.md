# GR#25 Edge-Registration Proposal — Firms Supply & Cost Construction Materials

**Status:** DRAFT — for Architect (Bev) and Lead Designer (Aaron) review. No commits or edits to `master-plan-v2.1.json`/`code.json` have been made. This is a proposal only.
**Prepared:** 2026-08-31. **Unblocks:** FEAT-1972079927 (money-circulation inc2: firms pay for construction).

---

## 0. TL;DR for the busy reader

**The problem:** Aaron ruled (money brief Q3) that FIRMS supply and PAY for construction — not free, not treasury. Currently:
- `engine.build` does NOT call `engine.firms` (missing edge).
- `SettleConstruction()` in `finance/stages.go` posts treasury→external (no firm involved).
- Construction materials are drawn from `engine.logistics` at no cost; there is no cost SOURCE.
- **Budget closure breaks:** total-money-in-circulation cannot be conserved if money leaves the treasury for construction but nobody supplies the materials.

**The proposal:**
1. **Register one missing edge:** `engine.build → engine.firms` in master-plan-v2.1.json (to be added to `engine.build`'s `calls` array).
2. **Rationale:** build needs to query or consume materials from firms at a market-determined price; firms supply construction materials as a commodity.
3. **Money flow sketch:** Treasury pays Firms for construction materials (not External); Firms price materials at market rates (possibly via `engine.market`); build consumes those materials instead of getting them free from logistics.
4. **Open questions:** (1) Does build pull materials from firms as a distinct stock, or does it buy them market-priced? (2) Does `SettleConstruction()` change from treasury→external to treasury→firms? (3) Does firms' `Financial.MonthlyCashFlow` include construction material revenue? (4) Which construction material types does firms supply (all, or specific commodities from the §33 chain)?

---

## 1. Current state — verified in source

### engine.build's current outbound calls (from code.json line 4371-4419 and master-plan line 1565-1575)

```
"calls": [
  "engine.world",
  "engine.finance",
  "engine.logistics",
  "engine.market",
  "foundation.data",
  "engine.season",
  "foundation.errors",
  "foundation.num",
  "engine.staffing"
]
```

**Not listed:** `engine.firms`.

### engine.build's Tick() behaviour (internal/engine/build/build.go lines 561-633)

Line 594-604: "Materials: draw through engine.logistics's shared Stock/Draw mechanism — no bespoke materials-only path (AC-4). A shortfall leaves MaterialsRemaining > 0, keeping the order materials-pending."

```go
dr, err := b.logistics.Draw(b.district, market.ConstructionMaterials, order.materialsRemaining, logistics.ConsumerConstruction)
```

**Observed fact:** Build draws materials from logistics; the cost-carrier account is never interrogated. The Draw operation appears to be a resource-inventory transaction, not a payment.

### engine.finance's SettleConstruction() (internal/engine/finance/stages.go lines 238-256)

```go
func (f *FinanceAPI) SettleConstruction(cost Money) (Money, error) {
  // ...
  if _, err := f.Post(Transaction{
    Description: "construction spend",
    Entries: []Entry{
      {Account: AcctTreasury, Side: SideDebit, Amount: cost, Category: CatConstruction},
      {Account: AcctExternal, Side: SideCredit, Amount: cost, Category: CatConstruction},
    },
  }); err != nil {
    // ...
  }
}
```

**Observed fact:** `SettleConstruction()` posts from treasury (debit) to external (credit). No firm is credited; no firm inventory is debited. **This is currently NEVER CALLED** (grepped `internal/**/*.go`, confirmed 2026-08-31: zero references outside the function definition itself and its test file).

### engine.firms' current outbound calls (from code.json line 8221-8269 and master-plan)

```
"calls": [
  "engine.citizens",
  "engine.finance",
  "engine.market",
  "engine.build",
  "engine.freight",
  "foundation.data",
  "foundation.det",
  "foundation.errors",
  "foundation.num"
]
```

**Already registered:** `engine.firms → engine.build` (line 8240-8242, `engine.build` in code.json at line 4342). This is a ONE-WAY edge: firms can query build (for premises, AC-7 of MOD-058).

**Not registered:** `engine.build → engine.firms` (the reverse edge).

### engine.firms' current data state (internal/engine/firms/firms.go lines 85-95)

```go
type Financial struct {
  CreditOutstanding int64  // outstanding credit (micro-pounds)
  MonthlyCashFlow   int64  // revenue − input cost − wage cost (micro-pounds)
  OutputScale       int64  // per-mille, 1000 = fully supplied
}
```

**Observed fact:** Firms track `MonthlyCashFlow` (line 92-93 comment: "revenue − input cost − wage cost"). This suggests firms have revenue streams (from selling their output). **Construction material supply is NOT explicitly mentioned** in this struct; it would need to be either (a) a separate revenue line, (b) integrated into the existing commodity-chain revenue model, or (c) a new distinct commodity track.

---

## 2. Primary proposal — one edge registration

### The edge

**From:** `engine.build`
**To:** `engine.firms`
**Rationale:** Build needs to query or consume construction materials from firms at a market-determined price. Without this edge, there is no registered contract allowing build to call into firms' APIs or query firms' material inventory/pricing.

### Master-plan edits (copy-pasteable JSON fragment)

In `docs/planning/master-plan-v2.1.json`, locate the `engine.build` item (currently seq 340), and replace its `calls` array:

```json
// engine.build (seq 340) — add engine.firms
"calls": [
  "engine.world",
  "engine.finance",
  "engine.logistics",
  "engine.market",
  "foundation.data",
  "engine.season",
  "foundation.errors",
  "foundation.num",
  "engine.staffing",
  "engine.firms"
]
```

**Impact:** This makes `engine.build → engine.firms` a registered call edge in code.json. After `generate.js` runs:
- `engine.build`'s `outbound.calls[]` will gain a new entry for `engine.firms` with a freshly-minted `inboundGuid`.
- `engine.firms`'s `inbound.consumers[]` will automatically gain a reciprocal entry for `engine.build` (generate.js's reverse-pointer pass, lines ~254-290).

---

## 3. Money-flow sketch

**The goal:** Close the budget-balance equation. Currently, `SettleConstruction()` posts money from treasury to external with no offsetting firm revenue. Conservation breaks: where does that money go? Who supplied the materials?

**The proposed flow:**

1. **Material supply:** Firms produce/supply construction materials as a commodity (either as part of their manufacturing-chain input, or as a distinct service commodity). This is new data/logic — currently unspecified.
2. **Build's material purchase:** `Build.Tick()` continues to call `Logistics.Draw()` for construction materials. But instead of logistics providing them "free" (from an implicit free pool), logistics sources them FROM firms' inventories or firms quote a market price via `engine.market`.
3. **Payment:** `SettleConstruction()` is called with a cost derived from material quantity × market price (or firms' quoted price). The transaction changes from treasury→external to treasury→firms. Firms' `MonthlyCashFlow` gains a revenue line for construction-material sales.
4. **Result:** Treasury debits (construction spend still leaves the budget as an outflow), but firms credit (they become a revenue stream). Total money in circulation is conserved: `+firms, -treasury = 0`.

**Example ledger entries:**
- Today: `Treasury -100, External +100` → money leaves the city, nobody gets it.
- Proposed: `Treasury -100, Firms +100` → treasury spends, firms earn; money circulates.

---

## 4. Evidence: construction-related imports and data

**Why build would need firms:**

1. **To query material pricing:** Build needs to know the cost of construction materials so it can charge the budget appropriately. `engine.market` provides generic commodity pricing, but firms might supply construction materials at their own margin or with supply-chain mark-ups.
2. **To source materials:** If firms produce/stock construction materials (as a derived commodity or explicitly), build needs to check availability (is there supply?) and draw from firms' inventory.
3. **To close the ledger:** Without the edge, there is no registered contract allowing build to make these queries.

**Current data (data/buildings.json and data/firms.json):**
- `data/buildings.json` declares construction materials as a quantity (tonnes) per zone type (line 162 in build.go: `Materials int64`).
- `data/firms.json` (not yet read in this task, but per master-plan §45) defines §33-chain input commodities firms consume and produce. Construction materials are not yet explicitly listed as a producible commodity by firms, but this is data — not code — so it can be added.

---

## 5. Open questions for the Architect and Lead Designer

1. **Material sourcing model:** Does build pull construction materials from:
   - (a) A free public-works pool in logistics (today's model, breaks budget closure)?
   - (b) Firms' inventories, bought at a market price?
   - (c) A dedicated construction-materials industry (a §33 chain stage registered as a firm)?
   - (d) A mix of the above?

2. **SettleConstruction() target:** Does the transaction change from `Treasury → External` to `Treasury → Firms`? Or does build pass the cost to firms separately (e.g., firms are charged for selling materials)?

3. **Firms' revenue inclusion:** Does firms' `Financial.MonthlyCashFlow` explicitly include construction-material revenue, or is it already implicitly captured by the existing "revenue − input cost" model?

4. **Material types:** Do firms supply ALL construction materials (generic "tonnes"), or specific commodities from the §33 production chains? (E.g., steel from heavy industry, timber from farming, etc.?)

5. **Timing:** Does material cost settle immediately (at Tick time when materials are drawn), or is there a billing cycle (firms invoice at month-end)?

6. **Dependencies on other inc1 decisions:** This is inc2 of FEAT-1972079927. Does inc1 (households affordability/consumption/wealth) establish a wage/income floor that affects construction demand? Should that be factored into this proposal, or is this edge independent?

---

## 6. Why this edge is necessary now

Without the edge:
- `spec-lint` (GR#25 enforcement) will continue to flag any acceptance criteria prose citing `engine.build` + `engine.firms` as an unregistered dependency.
- Build cannot legally call into firms' APIs or query firms' pricing/inventory, even if the implementation is ready.
- The composition root cannot wire the dependency without first registering it.
- Budget closure remains broken: construction spend leaves the treasury with no source firm to credit.

With the edge:
- Build can query firms for material pricing/availability.
- SettleConstruction() can credit firms instead of external, closing the loop.
- Money circulation is conserved: `TotalMoneyInCirculation` remains constant.
- Firms become a cost centre for construction (an economic consequence the player can influence: limit firm growth → fewer materials → slower construction).

---

## 7. Regeneration command and expected effects

```
node tools/plan/generate.js
```

**Expected effects once the master-plan edit above is applied:**

1. `code.json`'s `engine.build` module entry (currently at line ~4371-4419) will gain a new entry in `outbound.calls[]`:
   ```json
   {
     "key": "engine.firms",
     "moduleGuid": "df06bec1-ea6c-456d-9d06-690cb18fa2a0",
     "inboundGuid": "<freshly-minted-guid>"
   }
   ```

2. `code.json`'s `engine.firms` module entry (currently at line ~8193-8220, `inbound.consumers[]`) will automatically gain:
   ```json
   {
     "key": "engine.build",
     "outboundGuid": "7e791c72-d564-4504-b921-33adb1d486c9"
   }
   ```

3. `node tools/plan/spec-lint.js` should show **reduced** SPEC-LINT-001 violations if any acceptance docs cite `engine.build ↔ engine.firms`; those citations will now pass the edge-registration check.

4. `code.json`'s `conventions.universalEdges` remains unchanged (the `foundation.*` edges and the per-layer universal calls are separate from this specific edge).

---

## 8. Exec summary for dispatch

**Proposed edges:** `engine.build → engine.firms` (1 new edge).

**Evidence class:** NO EXISTING GO IMPORT (this is new forward-facing design, not a pre-existing drift). The edge represents a new architectural decision, not a code-already-present-but-unregistered situation.

**Money-flow one-liner:** Treasury pays Firms for construction materials (not External); Firms price materials at market rates; Build consumes those materials and money circulates instead of leaving the city.

**Open questions:**
1. Material sourcing model (free pool vs. firm inventory vs. dedicated industry)?
2. Does SettleConstruction() retarget to firms?
3. How is construction-material revenue integrated into firms' cash-flow accounting?
4. Specific material types (generic tonnes vs. §33-chain commodities)?
5. Immediate settlement vs. billing cycle?
