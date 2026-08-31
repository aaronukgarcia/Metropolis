# FEAT-1972079930: Wealth, ownership + inheritance

**Feature:** Bell-curve wealth distribution on migrant arrival, ownership types (individual/LLP/state-owned), tenure decisions driven by wealth, and deterministic inheritance with capital gains tax and treasury escheat.

**Mkey:** FEAT-1972079930

**Relates:** FEAT-1972079927 (money inc1 — bell-curve wealth draw landed), engine.citizens (lifecycle/death event, parent-child relationship), engine.finance (tax collection, CGT ledger entries, escheat entries), engine.build (property ownership assignment).

**GR#25:** New edges (if needed) must be registered in `code.json` before acceptance. All wealth decisions must be deterministic/seeded — no `Math.random()`.

---

## Evidence (why this is P1)

The game's economy is frozen without a believable wealth distribution and tenure model. Migrants arrive with no money (or uniformly), so household affordability is a binary pass/fail and all citizens behave identically. A bell-curve wealth draw (already seeded in FEAT-1972079927 inc1) creates natural inequality — the game responds by offering rentals to the poor, home-ownership to the middle class, and business ownership to the wealthy. Inheritance completes the wealth cycle: accumulated wealth passes down, building class mobility and long-term economic narrative. Treasury escheat (unclaimed estates) closes the money loop and provides an anti-farming mechanism (hoarding without heirs drains wealth to the state).

Northstar waypoint 4 (Aaron, 2026-08-31): wealth, ownership, inheritance are the bedrock of Baseline One watchability.

---

## Design

### Wealth Distribution (already landed in inc1, repeated for completeness)

- **Bell-curve random draw:** Migrants arrive with wealth sampled from a bell-curve distribution (Box-Muller or cumulative table), seeded by citizen ID or a state-derived pseudorandom stream. Deterministic: same seed → same draw. Distribution models real-world UK income (low-income earners, middle-class majority, high-net-worth minority).
- **Range:** `[0, ~150,000]` micro-pounds (placeholder; balance pass adjusts). Seeded at arrival time; wealth persists until death/inheritance/spending.

### Ownership Types

Three property/business ownership models, assigned deterministically:

1. **Individual (100% private).** A single citizen owns the asset outright. Personal wealth = private asset balance + liquid cash. No corporate structure.
2. **Limited Liability Partnership (LLP).** A business/commercial entity whose individual owner is opaque to the game. LLP takes profits + pays corporate tax. Citizens do not own the LLP directly; the LLP is a "construct" that belongs to no citizen (abstracted from family details). Citizens may work for the LLP or use its services, but do not inherit it.
3. **State-Owned.** Neutral/essential infrastructure (nuclear plants, university, major hospital, etc.). Owned by the state; profits flow to the treasury (not to any citizen). Treated as a special case of "no individual owner" + automatic tax collection.

**Assignment rule (open question):** Which buildings/businesses are individual vs. LLP vs. state-owned at creation? Initial ruling: residents' homes are individual (single citizen or household); shops/offices are individual if owner-occupied, LLP if franchise/corporate chain (rule TBD), state-owned if designated as such (rule TBD by zone/type).

### Tenure (own vs. rent; sharing vs. single)

- **Tenure options:** A resident household chooses among (a) **own** — buy a home/flat outright (requires capital), (b) **rent** — lease a residential property, (c) **share** — co-own with family or live in a household of unrelated residents. Sharing reduces per-capita cost and is the default for low-wealth households.
- **Wealth-driven decision:** Affordability determines the choice. Wealth bands (open question) define thresholds:
  - **Very poor (< £20,000):** Must rent; sharing allowed to reduce expense.
  - **Lower-middle (£20,000–£80,000):** Rent or buy a starter home; sharing common.
  - **Middle (£80,000–£200,000):** Own a home; single or with partner.
  - **Wealthy (> £200,000):** Own multiple properties, single residence.
- **Sharing modes:**
  - **Household:** Unrelated co-residents, shared rent/mortgage cost split N ways.
  - **Family:** Related citizens (spouse, children, parents), shared costs, some assets pooled.
  - **Single:** Individual in their own property (own or rent).
- **Determinism:** Given a citizen's wealth, household composition (if tracked), and local rental/purchase prices, the tenure is deterministic. Prices are state-derived (not random per-citizen).

### Inheritance & Escheat

- **On death:** 100% of the deceased's wealth is distributed to their **children** (all children equally, regardless of age or in-game status). If the wealth is **≤ £350,000 (placeholder)**, all passes untaxed. If wealth **> £350,000**, capital gains tax is applied to the excess:
  - Excess = Wealth − £350,000
  - Tax = Excess × CGT_RATE (placeholder ~20%; open question)
  - Post-tax inheritance = £350,000 + (Excess × (1 − CGT_RATE))
- **Equal split:** If N children, each receives (post-tax inheritance) / N.
- **No heirs (escheat):** If the deceased has no children, 100% of wealth flows to the **treasury** as an escheat entry in the finance ledger (named label: `Escheat` or `Estate Escheat`). This is a non-taxable inflow to the state.
- **Determinism:** Inheritance is calculated from the citizen's recorded children (engine.citizens family tree). Deaths are deterministic (age/health model); children are assigned at birth or adoption.
- **Conservation:** Every inheritance transaction and escheat is booked in the finance ledger with named labels (`Inheritance`, `Capital Gains Tax`, `Escheat`) so the audit trail is legible. Total money in the city is never increased or destroyed by inheritance; it is only redistributed.

### Prerequisite: Parent-Child Tracking

**Open question:** Does `engine.citizens` track parent-child relationships today? Inheritance requires the ability to identify a deceased citizen's children at death time. If the engine does not track this, the BA must request an extension to engine.citizens before acceptance or propose a fallback (e.g., all wealth to the treasury if no family tree).

---

## Placeholder Constants & Tuning

| Parameter | Placeholder Value | Notes |
|-----------|-------------------|-------|
| **Wealth Range** | £0 to £150,000 | Box-Muller draw; mean ~£45,000; balance pass tunes. |
| **Inheritance Threshold** | £350,000 | Wealth ceiling for untaxed inheritance; excess taxed at CGT_RATE. |
| **CGT Rate** | 20% | Applied to inheritance excess; balance pass tunes. |
| **Very Poor Band** | < £20,000 | Must rent; sharing typical. |
| **Lower-Middle Band** | £20,000–£80,000 | Rent or buy starter home. |
| **Middle Band** | £80,000–£200,000 | Own home; single or couple. |
| **Wealthy Band** | > £200,000 | Multiple properties, business ownership. |
| **Rental Cost (baseline)** | ~£800/month | Per-property; scaled by zone/quality. |
| **Home Purchase Price (baseline)** | ~£150,000–£250,000 | Starter home; balance pass tunes. |
| **LLP vs. Individual Rule** | TBD | Which businesses are franchises (LLP) vs. owner-operated (individual)? |
| **State-Owned Assignment** | TBD | Which zones/types are state-owned (e.g., schools, hospitals, power plants)? |

---

## Acceptance Criteria

### AC-1: Wealth Distribution on Migrant Arrival (Deterministic Bell-Curve)

**Requirement:** When a migrant citizen is created, wealth is sampled from a bell-curve distribution using a state-derived seed (citizen ID or pseudorandom stream from a fixed global seed). The same input (seed) always produces the same wealth value.

**Check:**
- Create two cities with identical engine seeds and configuration.
- In both cities, create the same sequence of migrants (same arrival order).
- Verify that migrant N in city 1 has the same wealth as migrant N in city 2 (compare `citizen.wealth` values).
- Verify that wealth values follow a roughly bell-shaped histogram (most citizens in a middle band, fewer at extremes).
- **Mutation:** Replace the seeded draw with `Math.random()`; this test goes red (different cities have different distributions).
- **False-pass:** A constant wealth (all migrants get £50,000); this passes the determinism check but fails the histogram shape.

**Relates to:** FEAT-1972079927 inc1 (wealth draw implementation).

---

### AC-2: Wealth Persistence Through Citizen Lifecycle

**Requirement:** Once assigned, wealth persists across ticks, employment changes, and residential moves until the citizen spends, earns, or dies. A citizen's `wealth` field is not reset or erased.

**Check:**
- Create a migrant with wealth `£45,000`.
- Advance 10 ticks without income/expense transactions.
- Assert that `citizen.wealth === 45_000` (unchanged).
- Add employment (salary 30 ticks); after one wage payment, wealth increases by the wage amount (e.g., £1,200 if salary is £36,000/year).
- Advance 10 more ticks (no employment).
- Assert that wealth is `45_000 + 1_200` (income only, no decay).
- **Mutation:** Apply a tick-based wealth decay (e.g., `wealth *= 0.99`); this test goes red (wealth decreases).
- **False-pass:** Wealth increases every tick from a hidden income source (not detected as actual earned income).

---

### AC-3: Ownership Type Assignment (Individual, LLP, State)

**Requirement:** Buildings and businesses are created with a deterministic ownership type (Individual, LLP, or State-Owned). Type is immutable for the building's lifetime.

**Check:**
- Build a residential property in a standard zone; verify ownership is `Individual`.
- Build a commercial shop in a commercial zone; verify ownership is either `Individual` or `LLP` depending on the building type (rule TBD; check the assignment logic).
- Place a power plant (or other state infrastructure); verify ownership is `State-Owned`.
- Create an identical city with the same seed and layout; verify the same buildings have the same ownership types in the same locations.
- **Mutation:** Randomize ownership type assignment (use `Math.random()`); this test goes red (same-seed cities have different ownership types).
- **False-pass:** All buildings are `Individual`; the test never checks LLP or State-Owned cases.

**Relates to:** engine.build (property creation), code.json edge registration (if needed).

---

### AC-4: Tenure Decision by Wealth (Own vs. Rent vs. Share)

**Requirement:** A household's tenure (own, rent, share) is determined by the residents' combined wealth and local prices. Wealth drives the decision; the same household wealth + prices always yield the same tenure.

**Check:**
- Create a household with combined wealth £15,000 (below £20,000 threshold).
- Assert that the household is assigned `Tenant` status (renting, not owning).
- Create a household with combined wealth £100,000 (middle band).
- Assert that the household is assigned `Owner` status (or can choose to own, passing affordability checks).
- Create a household with combined wealth £10,000,000 (very wealthy).
- Assert that the household is assigned `Owner` status with no affordability constraints.
- Verify that sharing status (household vs. family vs. single) is also wealth-driven (lower wealth → more sharing).
- **Mutation:** Hard-code all households as `Owner`; this test goes red (low-wealth households fail affordability).
- **False-pass:** Tenure is assigned but never enforced (household has high rent/mortgage payments regardless of tenure).

**Relates to:** engine.citizens (household composition), engine.finance (rental/purchase cost booking).

---

### AC-5: Inheritance—100% to Children, Capped Threshold, Equal Split

**Requirement:** When a citizen dies, their wealth is distributed to their children. Up to £350,000 (placeholder) passes untaxed; excess is subject to capital gains tax at CGT_RATE (placeholder 20%). Wealth is split equally among all children.

**Check:**
- Citizen A has 2 children and dies with wealth £300,000 (below threshold).
- Each child receives £150,000 untaxed.
- Assert that both children's wealth increases by £150,000 and no CGT transaction appears in the ledger.
- Citizen B has 2 children and dies with wealth £450,000 (exceeds threshold by £100,000).
- Excess = £100,000; Tax = £100,000 × 0.2 = £20,000; Post-tax = £100,000 − £20,000 = £80,000.
- Inheritance available to children = £350,000 + £80,000 = £430,000.
- Each child receives £215,000.
- Assert that finance ledger has entries: `Inheritance` (£430,000 from deceased to household), `Capital Gains Tax` (£20,000 from household to treasury).
- Verify that the sum of children's wealth increases is exactly £215,000 per child (no rounding errors).
- **Mutation:** Give 100% of wealth to the eldest child only (no equal split); this test goes red (second child does not inherit).
- **Mutation:** Apply CGT to all inherited wealth, not just the excess; this test goes red (each child gets < £150,000 in the first case).
- **False-pass:** Wealth is transferred but ledger entries are missing or mislabeled.

**Relates to:** engine.citizens (death event, children list), engine.finance (CGT booking, inheritance label).

---

### AC-6: Escheat—No Heirs to Treasury

**Requirement:** When a citizen dies with no children (no heirs), 100% of their wealth flows to the treasury as an escheat entry in the finance ledger, named `Escheat` or `Estate Escheat`.

**Check:**
- Citizen C has no recorded children and dies with wealth £500,000.
- Assert that `citizen.wealth` is 0 and the treasury's balance increases by £500,000.
- Assert that the finance ledger contains an entry: `Escheat` (£500,000 inflow to treasury, label matches `Escheat` exactly).
- Assert that no `Inheritance` or `Capital Gains Tax` entries appear for this death.
- Verify that the city's total money supply is unchanged (money moved from citizen to state, not created).
- **Mutation:** Delete the escheat ledger entry (only transfer the cash, no label); this test goes red (audit trail is incomplete).
- **Mutation:** Pay escheat only if wealth exceeds a threshold (e.g., only if > £100,000); this test goes red (citizen with £50,000 and no heirs gets their wealth erased, not escheated).
- **False-pass:** Treasury cash increases but the label is generic (`Tax`) or missing.

**Relates to:** engine.citizens (parent-child relationship, death event), engine.finance (ledger entries, treasury).

---

### AC-7: Determinism (All Draws & Decisions State-Derived)

**Requirement:** All wealth draws, ownership assignments, tenure decisions, and inheritance calculations are deterministic and seeded. No use of `Math.random()` in the hot path. The same engine state → same decisions every time.

**Check:**
- Create a city seed S and run 100 ticks.
- Checkpoint the full engine state (citizens, buildings, finance ledger).
- Reload the checkpoint and run 100 more ticks; verify that the engine state at tick 200 is identical whether loaded mid-way or replayed from genesis.
- Specifically: migrant arrivals have identical wealth, buildings have identical ownership types, households choose identical tenures, and inheritance calculations yield identical results.
- Grep the codebase for `Math.random()` calls in wealth/ownership/tenure/inheritance paths; assert that none exist (only seeded pseudorandom or deterministic lookups).
- **Mutation:** Introduce `Math.random()` in the wealth draw; this test goes red (replay diverges).
- **False-pass:** Seeded only at startup; if the global seed is re-sampled per-tick, determinism fails.

**Relates to:** GR#21 (determinism gate), replay system (FEAT-1972079897).

---

### AC-8: Conservation (No Money Minted/Destroyed)

**Requirement:** Inheritance, capital gains tax, and escheat all move money through the ledger with named labels. No wealth is created or destroyed; it is only redistributed.

**Check:**
- Sum the total `citizen.wealth` across all citizens at tick T.
- Record the treasury balance at tick T.
- Advance to tick T+100 (including deaths and inheritances).
- Sum the total `citizen.wealth` across all remaining citizens.
- Sum inherited wealth from dead citizens (from ledger: `Inheritance` + `Capital Gains Tax` inflows).
- Sum escheated wealth from dead citizens (from ledger: `Escheat` inflows).
- Assert that: (Total citizen wealth at T+100) + (Treasury inflows from inheritance/CGT/escheat) = (Total citizen wealth at T) + (Original treasury balance changes from other sources).
- No unexplained surplus or deficit.
- **Mutation:** Create money on inheritance (book `Inheritance` but don't deduct from the deceased); this test goes red (money totals exceed initial).
- **False-pass:** Ledger entries exist but are not used to reconcile; test only checks that entries are present, not that totals balance.

**Relates to:** GR#3 (single source of truth), finance conservation, ledger audit.

---

### AC-9: Parent-Child Tracking Prerequisite

**Requirement:** Before acceptance, verify that `engine.citizens` has a field or method to identify a citizen's children at death time. Inheritance cannot be implemented without this.

**Check:**
- Grep `engine/citizens/citizen.go` (or equivalent) for a `Children` field, `GetChildren()` method, or similar.
- If the field exists, spawn a child citizen and verify that the parent's children list is updated.
- If the field does not exist, this AC fails and the feature is blocked pending a citizens extension.
- Create a test: parent citizen dies, query their children, distribute wealth equally.
- **Relates to:** engine.citizens (prerequisite).
- **Open question:** Does the engine track parent-child relationships today? If not, scope a separate issue to add it.

---

### AC-10: Ownership Type Immutability

**Requirement:** Once a building is assigned an ownership type (Individual, LLP, State), the type cannot be changed. Ownership persists for the lifetime of the building.

**Check:**
- Create a building with ownership `Individual`.
- Attempt to change ownership to `LLP` or `State-Owned` (via edit, reconfigure, or any game mechanism).
- Assert that the ownership remains `Individual`.
- Verify that no game mechanic (upgrade, sell, demolish-and-rebuild) changes the ownership type.
- **Mutation:** Allow ownership type to be mutable; this test goes red (type can be changed).
- **False-pass:** Ownership is stored but never validated against mutation.

---

### AC-11: Inheritance Ledger Labels (Audit Trail)

**Requirement:** Finance ledger entries for inheritance use consistent, specific labels: `Inheritance` (wealth transfer to heirs), `Capital Gains Tax` (CGT deduction), `Escheat` (unclaimed estate to treasury). These labels are exact matches (case-sensitive) and appear in the ledger with proper transaction metadata.

**Check:**
- When a citizen with 2 children dies with £400,000 (excess £50,000):
  - Expect ledger entries:
    - `Inheritance` source: deceased citizen, target: heir 1 (£175,000)
    - `Inheritance` source: deceased citizen, target: heir 2 (£175,000)
    - `Capital Gains Tax` source: heirs, target: treasury (£10,000)
  - All labels match exactly (no "InheritanceTax", "Capital Gains", "Estate Tax").
- When a citizen with no heirs dies:
  - Expect ledger entry:
    - `Escheat` source: deceased citizen, target: treasury (full wealth).
- Audit trail is human-readable: grep the ledger for `Inheritance`, `Capital Gains Tax`, `Escheat` and find all related transactions.
- **Mutation:** Use generic label `Tax` for all three; this test goes red (audit trail is ambiguous).
- **False-pass:** Labels exist but transactions are incomplete (e.g., inheritance transfer to child but no CGT deduction from child).

---

### AC-12: No Earnings/Wealth Changes on Death Tick

**Requirement:** When a citizen dies, their wealth is frozen at the moment of death. No wage, tax, inheritance income, or other transaction is processed for that citizen on their death tick. Wealth passes to heirs; the dead citizen has no further transactions.

**Check:**
- Citizen D earns £1,200/month; their wage is due on tick T.
- Citizen D dies on tick T (death engine event fires).
- Assert that D does not receive the wage; D's wealth at death is pre-wage.
- Heirs receive D's pre-wage wealth, not post-wage.
- Verify that D does not appear in the next wage or tax cycle.
- **Mutation:** Process wages/taxes after death; this test goes red (dead citizen has transactions).
- **False-pass:** Wage is skipped but a "death bonus" or other transaction is applied instead.

---

## Out of Scope

- **Individual vs. LLP rule tuning.** The assignment logic (which buildings are franchises vs. owner-operated) is a balance decision, not an AC; the design will be approved separately.
- **State-ownership assignment.** Which building types are state-owned is a design decision (requires Aaron approval) and is deferred to a separate item.
- **CGT rate tuning.** The rate (placeholder 20%) is a balance number; Aaron's approval required before commit.
- **Wealth band thresholds.** Exact threshold values (£20k, £80k, £200k) are placeholders; balance pass tunes.
- **Multiple inheritance scenarios.** Blended families, adopted children, co-parenting, guardianship. Scope: 1:N parent-children, equal split, no special cases.
- **Selling inherited property.** A heir inherits a home; selling it later is a separate transaction (out of scope here).
- **Debt inheritance.** Citizens have no debts in the current model; inheritance does not transfer liabilities.
- **Lifetime gifts.** Citizens do not gift wealth during life; only inheritance on death.
- **LLP profit distribution.** How LLP profits are computed and distributed is engine.finance's scope; this item assigns the ownership type only.

---

## Open Questions for Aaron / Architect

1. **Parent-child tracking (prerequisite).** Does `engine.citizens` track parent-child relationships (e.g., a `Children` list or `Parent` field)? If not, should we add it as a separate issue before starting inheritance, or defer both?

2. **Individual vs. LLP assignment rule.** Which buildings are individual-owner and which are LLP (corporate/franchise)? Examples: a family bakery (individual), a Tesco supermarket (LLP). Is the rule based on zone type, building class, or a property flag?

3. **State-ownership assignment rule.** Which building types are state-owned? Examples: primary school, hospital, power plant, water treatment. Is there a fixed list or a zone-based rule?

4. **CGT rate.** Is 20% (placeholder) the correct real-world UK rate? Should the game model it or use a stylized rate?

5. **Tenure wealth bands.** Are the thresholds (£20k, £80k, £200k) reasonable for a £150k average migrant wealth? Should they be adaptive (e.g., bands as percentiles of the local distribution)?

6. **Household affordability check.** When a household decides to rent, who pays the landlord? Is rent a direct outflow from the household budget, or does it route through the finance ledger? Same question for home purchase.

7. **LLP profit mechanics.** Once an LLP is assigned as the owner, how does it accrue profits and pay tax? Is this part of the firm model or a separate finance rule?

8. **Inheritance of dependent children.** If a parent dies and a child is still a minor (alive in the city but under age), can the child inherit? Or does inheritance only apply to adult heirs?

9. **Incremental rollout.** Is the intended build order: (a) tenure-by-wealth, (b) ownership types, (c) inheritance+CGT+escheat? Or should they land in parallel?

---

## Increments Suggested

1. **Increment 1: Tenure by Wealth.** Implement wealth bands and tenure decision logic (own vs. rent). No ownership types, no inheritance yet. Household affordability checks against rental/purchase prices.
2. **Increment 2: Ownership Types.** Assign Individual/LLP/State to buildings. Verify no inheritance mechanics yet (wealth is destroyed at death).
3. **Increment 3: Inheritance + CGT + Escheat.** Implement death event, child distribution, CGT calculation, and treasury escheat. Ledger labels required.

---

## Test Coverage Gate

- `cd internal && go test ./...` includes engine/citizens death+inheritance tests (determinism, equal split, CGT calculation, escheat).
- `cd webconsole && npm test` covers finance ledger labels and UI display (if F2 shows inheritance transactions).
- `tools/plan/spec-lint.js` verifies that all AC checks are cited in git and no ACs are orphaned.

---

*Acceptance criteria authored for FEAT-1972079930 (2026-08-31). Aaron's design is the authoritative source.*
