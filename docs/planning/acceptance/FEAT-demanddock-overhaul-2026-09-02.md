# FEAT-demanddock-overhaul — unified provider-selection + DemandDock completeness

**mkey:** `ui.webconsole` (no single feature key covers all three source items; tag commits `[ui.webconsole]`, reference BUG-572 / BUG-571 / FEAT-2326609735 in the body per item actually touched)
**Author:** BA pass, 2026-09-02
**Status:** DRAFT — awaiting Aaron ruling on the two DemandDock design points (§3) and the "best value" definition (§6)

---

## 1. Context — why these three are one build

Three open items all resolve to the SAME two functions in `webconsole/src/sim/data.ts`:

| Item | Symptom | Root cause |
|---|---|---|
| **BUG-572** | DemandDock is missing the refuse row; rows render in a fixed static order; Aaron wants Health pinned top and the rest sorted worst-shortfall-first | `serviceDemandOf()` (data.ts:2231-2262) never folds in `wasteStatsOf()` (data.ts:2371-2378); `DemandDock.tsx` (`webconsole/src/components/left/DemandDock.tsx:53-67`) renders `services.map()` in `serviceCoverageOf()`'s fixed array order (data.ts:2198-2212) with no sort |
| **BUG-571** | The fire "Auto-build" recommends `fire_station` even when it is locked at the player's level | `serviceCoverageOf()`'s `row('fire', 'Fire cover', pop, fire, 'fire_station')` (data.ts:2208) hardcodes the recommended spec id as a literal string; `pickAutoSpec()` (data.ts:2685-2716) reads that same hardcoded `.spec` straight through `serviceDemandOf()` with **no unlock or tier check at all** — it is the one row-provider in the whole panel that was never routed through a real provider-selection function |
| **FEAT-2326609735** | "1 dam not 20 water towers" — the one-click Fix button always proposes the cheapest unit, so a huge shortfall proposes 20 of the smallest tower instead of 1 dam | `cheapestProvider()` (data.ts:2805-2816) is a pure cost-minimiser: `if (!best \|\| sp.cost < best.cost ...) best = sp` — it has no concept of "how much of the shortfall does one unit clear," so it always converges on the cheapest SINGLE unit regardless of how many of them `demandFixPlan()` (data.ts:2823-2845) then has to multiply out |

**The unifying insight:** all three symptoms are downstream of the SAME missing concept — a real **provider selector** that is unlock-aware (fixes BUG-571) and value-aware, not just cost-aware (fixes FEAT-...735) — plus a **DemandDock render pass** that is complete and sorted (fixes BUG-572). Building `optimalProvider()` once and wiring it into both `serviceCoverageOf()`'s fire row and `demandFixPlan()`'s `cheapestProvider()` call site means **one pass over data.ts** instead of three serial ones that would each touch the same neighbourhood of code and risk stepping on each other's diffs (BUG-571's provider-selection fix and FEAT-...735's provider-selection fix are the SAME function if written together, and two separate passes would either duplicate it or have the second bulldoze the first's tests).

Non-goals (explicitly out of scope for this spec): balance-number tuning (every threshold below is a PLACEHOLDER per Aaron's blanket rule), the `placeMany`/manual-drag path (data.ts is not touched there), and parks (still excluded from `DEMAND_FIX_PROVIDERS` per the existing "don't invent demand" note at data.ts:2735-2737 — no coverage metric exists for them).

---

## 2. A unified `optimalProvider(state, service, budget)` selector

### 2.1 Signature and placement

Add to `webconsole/src/sim/data.ts`, adjacent to `cheapestProvider()` (data.ts:2799-2816), which it replaces at both call sites:

```ts
function optimalProvider(s: SimState, serviceKey: string, budget: number, shortfall: number): Spec | null
```

- `serviceKey` — same key space as `DEMAND_FIX_PROVIDERS` (data.ts:2771-2797); the rule (`match`/`unitCapacity`) is looked up from that SAME registry, unchanged. No new registry, no re-derivation (keeps the GR#3 SSOT property `cheapestProvider()`'s own doc comment calls out at data.ts:2766-2770).
- `budget` — funds available to spend on this fix right now (`s.funds` at both call sites below; PLACEHOLDER decision — see §6 on whether admin-mode / in-flight-affordability nuances need a different number).
- `shortfall` — the quantity of `need`-unit capacity still required (`target - have`, i.e. the same numerator `demandFixPlan()` already computes at data.ts:2840, and — for the fire case — `serviceCoverageOf()`'s own `need - cap`).

### 2.2 Selection algorithm

1. **Candidate set** — same filter `cheapestProvider()` already applies (data.ts:2810-2812): `canEnterSim(sp) && specUnlocked(s, sp) && rule.match(sp) && rule.unitCapacity(sp) > 0`. This is the line that was **never applied to the fire row** (§1) — `optimalProvider` applied here for `fire` is BUG-571's entire fix.
2. Partition candidates into:
   - **Clears-in-one**: `unitCapacity(sp) >= shortfall && sp.cost <= budget` — a single unit of this spec both closes the whole shortfall and is affordable right now.
   - **Fallback pool**: everything else unlocked+affordable-in-principle (cost checked per-unit by the existing `resolveDemand` placement loop, `engine.ts:2980-2991`, which already stops when `cur.funds < cost` — `optimalProvider` does not need to re-simulate multi-unit affordability, only single-unit).
3. If **Clears-in-one** is non-empty: pick the one with the **lowest cost-per-capacity** (`sp.cost / unitCapacity(sp)`), tie-broken by `sp.id` ascending (deterministic, GR#21 — same tie-break style as `cheapestProvider()`'s `sp.cost === best.cost && sp.id < best.id` at data.ts:2813). This is the FEAT-...735 fix: among big-enough affordable units, prefer the efficient one (dam over an oversized alternative), never the merely-cheapest-per-unit tower that would need 20 copies.
4. Else, fall back to **cheapest by absolute `sp.cost`** over the full candidate set from step 1 — i.e. exactly today's `cheapestProvider()` behaviour, unchanged, tie-broken the same way. This is the fire fix in the common case: none of `fire_post`/`fire_station`/`fire_hq` can single-handedly cover a whole city's population, so fire always falls through to this branch, which now correctly picks the cheapest **unlocked** tier instead of the hardcoded literal.
5. Return `null` only when the step-1 candidate set is empty (nothing unlocked+enterable serves this service at all) — identical null contract to `cheapestProvider()` today, so both call sites' existing "omit this row" / "no auto-build" handling needs no change.

### 2.3 Why one function fixes all three

- **BUG-571** — routing the fire row through `optimalProvider()` (see §4) makes it unlock-aware for the first time. Since no fire tier ever clears a whole-population shortfall in one unit, fire always lands in step 4 (cheapest-unlocked-tier) — exactly Aaron's ask, with no fire-specific branch required.
- **FEAT-2326609735** — water/power/refuse rows already have shortfalls that a single large unit CAN clear (a dam's capacity vs. a remaining clean-water shortfall, once the city is big enough that towers alone can't keep up); step 3 catches this and prefers the dam. When the shortfall is still small (a starter city), no unit "clears in one" trivially — actually every unit that has `unitCapacity >= shortfall` qualifies for step 3, so a small shortfall lets even a small tower qualify; the cost-per-capacity tie-break then naturally picks the cheapest one that clears it, which is usually still a tower at that scale. The dam only wins once the shortfall grows past what a tower can singly clear AND the dam is affordable — the exact "grows into needing the big unit" curve Aaron described.
- **BUG-572** is untouched by `optimalProvider()` — it is a DemandDock render-order and completeness fix (§3), independent of provider selection. It is unified here only because implementing all three in the same data.ts pass means the DemandDock diff and the selector diff can be reviewed and Destructive-attacked together, rather than three lanes fighting over the same ~150 lines of file.

---

## 3. DemandDock completeness + sort (BUG-572)

### 3.1 Fold refuse into `serviceDemandOf()`

`serviceDemandOf()` (data.ts:2231-2262) currently maps only `serviceCoverageOf()`'s rows. Extend it to append one more row sourced from `wasteStatsOf()` (data.ts:2371-2378), using the SAME `demandIndexOf()` curve (data.ts:2224-2225) every other non-power row uses:

```ts
const waste = wasteStatsOf(s);
const refuseRow = {
  id: 'refuse',
  label: 'Refuse',
  value: Math.round(demandIndexOf(waste.coverage) * f),
  spec: /* see optimalProvider(s, 'refuse', ...) — no hardcoded literal */,
};
```

`DEMAND_FIX_PROVIDERS.refuse` (data.ts:2796) already exists and is already consumed by `demandFixPlan()` (data.ts:2827) for the "Fix (N)" button — this closes the gap where refuse has a Fix button possibility today (if `fixPlanByService` had an entry) but **no row to attach it to** in `DemandDock.tsx`'s `services.map()` (`DemandDock.tsx:53`), since `services` never contained a `refuse` entry. `serviceKey` id must be the literal string `'refuse'` to match `fixPlanByService.get(m.id)` (`DemandDock.tsx:54`) with zero DemandDock.tsx changes needed beyond the sort (§3.2).

### 3.2 Sort: dynamic, Health pinned top

`DemandDock.tsx:53` currently does `services.map((m) => ...)` with no sort — Aaron wants highest-demand-to-top with Health pinned. Change to:

```ts
const sorted = [...services].sort((a, b) => {
  const aHealth = a.id === 'gp' || a.id === 'hosp';
  const bHealth = b.id === 'gp' || b.id === 'hosp';
  if (aHealth !== bHealth) return aHealth ? -1 : 1;
  return b.value - a.value;
});
```

using the SAME `.value` descending comparator `pickAutoSpec()` already applies at data.ts:2693 (`serviceDemandOf(s).sort((a, b) => b.value - a.value)`) — no new comparator invented, reused verbatim for the "highest demand to top" half. This is a `DemandDock.tsx`-local sort (render concern only); `serviceDemandOf()`'s own return order is untouched so `pickAutoSpec()`'s existing sort-and-scan (data.ts:2693-2714) is unaffected by this change.

### 3.3 Open design point (a) — what does "Health pinned top" mean?

`serviceCoverageOf()` has TWO health rows: `gp` (data.ts:2205) and `hosp` (data.ts:2206) — GP clinics and Hospital are tracked and gated separately (different `served` sums, different specs). Two readings:

- **(a1)** Both `gp` and `hosp` pinned at the top, sorted between themselves by `.value` desc (whichever of the two is in worse shortfall leads).
- **(a2)** A single synthetic "Health" priority bucket — average or worse-of the two coverages folded into one meter, one Fix button.

**Recommendation: (a1).** The two rows already have independent Fix buttons wired to independent `DEMAND_FIX_PROVIDERS` entries (`gp`/`hosp`, data.ts:2781-2785) and independent specs (`hea_clinic` vs `hea_hospital`/`hea_teaching`) — folding them into one meter would need a new composite `DemandFixPlanItem` type or force a choice of which spec the single Fix button places, neither of which is a small change. (a1) is a pure sort-key change (§3.2's `aHealth`/`bHealth` predicate) with no data-model impact. **Needs Aaron's confirmation** — this is the one place the spec text ("Health pinned at the very top") is genuinely ambiguous between "the Health *label*" (implying one row) and "the Health *rows*" (implying both, together).

### 3.4 Open design point (b) — do the 3 zone rows (Housing/Shops/Industry) join the sortable list?

`DemandDock.tsx:50-52` renders `Housing`/`Shops`/`Industry` from `demandOf(state)` (a DIFFERENT function, `engine.ts`, zone-growth demand — not a `serviceCoverageOf()` row, no `spec`/Fix-button shape) BEFORE the `services.map()` block, unconditionally, in fixed order.

**Recommendation: leave them separate, unsorted, above the list — do not fold them in.** They come from a structurally different function (`demandOf`, not `serviceDemandOf`) with a different value semantics (zone growth pressure, not coverage shortfall) and no `DemandFixPlanItem`/spec/Fix-button — merging them into the sortable array would require either (i) giving `demandOf()`'s rows a fake `spec`/fix identity they don't have, or (ii) writing a heterogeneous sort comparator across two unrelated value scales. Both add real risk for a request ("dynamic sort, Health pinned top") that Aaron scoped to the demand *panel*'s service rows, evidenced by every other point in the ask (refuse, fire, Health) being a `serviceCoverageOf()`/`wasteStatsOf()` concept. **Needs Aaron's confirmation** if he intended the zone rows to also move.

---

## 4. Fire into the fix plan

Per data.ts:2737-2743, fire was **deliberately excluded** from `DEMAND_FIX_PROVIDERS` — "the one-click bulk-place feature for it is a separate follow-up." This spec IS that follow-up (BUG-571 is exactly "fire's auto-recommend is wrong," and giving it a real Fix button is the natural fix once `optimalProvider()` exists).

1. Add to `DEMAND_FIX_PROVIDERS` (data.ts:2771-2797):
   ```ts
   fire: { match: (sp) => sp.kind === 'fire', unitCapacity: (sp) => sp.served ?? 0 },
   ```
   mirroring `police`'s existing `kind`-keyed entry (data.ts:2786) — `fire_post`/`fire_station`/`fire_hq` (data.ts:1256-1258) are already `kind: 'fire'`.
2. `demandFixPlan()` (data.ts:2823-2845) already iterates every `serviceCoverageOf()` row generically (`rows` array built at data.ts:2825-2828) — fire is already IN that array (`serviceCoverageOf()` returns a `fire` row at data.ts:2208) but was silently skipped only because `cheapestProvider(s, 'fire')` returned `null` (no rule existed). Adding the rule above is the ENTIRE change needed for fire to get a sized "Fix (N)" button — no `demandFixPlan()` code change required.
3. Replace `cheapestProvider()`'s single call site inside `demandFixPlan()` (data.ts:2835) with `optimalProvider(s, row.serviceKey, s.funds, target - row.have)`, and update `serviceCoverageOf()`'s fire row (data.ts:2208) to source its recommended spec from the same selector instead of the literal `'fire_station'`:
   ```ts
   row('fire', 'Fire cover', pop, fire, optimalProvider(s, 'fire', s.funds, pop - fire)?.id ?? 'fire_post'),
   ```
   (fallback literal `'fire_post'` only for the pathological all-locked case, matching the null-handling `pickAutoSpec()`/`DemandDock.tsx:68-82` already do for an unaffordable/locked auto-build recommendation — the UI already renders "needs a X — unlocks at level Y" for that state, so a locked fallback spec is safe to surface.)

---

## 5. Acceptance criteria

Each AC is written to be checkable against a specific `SimState` fixture and able to FAIL under the pre-fix code.

1. **AC-1 (BUG-572 completeness):** Given a state with `wasteGeneratedOf(s) > collectionCapacityOf(s)` (a refuse shortfall) and no other change, `serviceDemandOf(s)` contains an entry with `id === 'refuse'`. **Fails today** — no such entry exists (data.ts:2231-2262 never reads `wasteStatsOf`).
2. **AC-2 (BUG-572 sort — descending):** Given a state with at least two non-health services at different `.value`s, the array `DemandDock` renders (post-sort) has every non-health row's `.value` monotonically non-increasing. **Fails today** — `DemandDock.tsx:53` has no sort.
3. **AC-3 (BUG-572 sort — Health pinned):** Given a state where `gp` and `hosp` are NOT the worst-covered services (some other service has a higher `.value`), both `gp` and `hosp` still render before every non-health row. **Fails today** for the same reason as AC-2, and would still fail under a naive value-only sort with no health predicate.
4. **AC-4 (determinism, GR#21):** Calling `serviceDemandOf(s)` (with the refuse fold) and the DemandDock sort twice on the identical `s` object produces byte-identical arrays in byte-identical order — no `Date.now`/`Math.random`, no reliance on `Object.values()`/`Map` iteration order for the sort's tie-breaking (ties broken by a stable secondary key, e.g. `id` ascending, when two rows share `.value`).
5. **AC-5 (BUG-571 fire unlock-awareness):** Given a state where `fire_station` and `fire_hq` are locked (`s.xp` below their `unlock` level) and only `fire_post` is unlocked, `optimalProvider(s, 'fire', s.funds, shortfall)` (and the `auto.spec` `DemandDock` would recommend) returns/resolves to `fire_post`, never `fire_station`/`fire_hq`. **Fails today** — `pickAutoSpec` recommends the literal `'fire_station'` regardless of lock state (data.ts:2208).
6. **AC-6 (BUG-571 no-shortfall-sizing regression guard):** The fire fix places exactly one unit per the existing `pickAutoSpec`/`runAuto` click path (`DemandDock.tsx:33-40`) — this spec does NOT change that single-unit-per-click behaviour; only the CHOSEN spec changes. (Fire's Fix-button sizing, once added per §4, is a SEPARATE code path — `resolveDemand`/`demandFixPlan`, already `count`-based — and AC-9 covers that.)
7. **AC-7 (FEAT-...735 big-unit preference):** Given a state where a large-capacity spec for some service is unlocked, its `unitCapacity(sp) >= shortfall`, and `sp.cost <= s.funds`, AND a small-capacity spec for the same service would need N>1 units to clear the same shortfall, `optimalProvider()` returns the large-capacity spec, not the small one — even when the small spec's absolute `sp.cost` is lower. **Fails today** — `cheapestProvider()` (data.ts:2805-2816) always returns the small spec (lowest `sp.cost`).
8. **AC-8 (FEAT-...735 fallback when unaffordable):** Given the same large-capacity spec but with `sp.cost > s.funds` (not affordable in one shot), `optimalProvider()` falls back to the cheapest unlocked small-capacity spec — the one-big-unit preference never returns a spec the player cannot afford at all when a smaller affordable one exists.
9. **AC-9 (fire gets a sized Fix button):** Given a fire shortfall (`pop - fire > 0`) and `fire_post` unlocked, `demandFixPlan(s)` contains an entry with `serviceKey === 'fire'` and `count === Math.ceil((pop*1.05 - fire) / servedOf(fire_post))`. **Fails today** — `demandFixPlan()` never emits a fire entry (no `DEMAND_FIX_PROVIDERS.fire` rule exists).
10. **AC-10 (affordability respected — Q100055 A1 pattern):** Placing a `demandFixPlan()` fire (or any) entry via `resolveDemand` places as many units as `s.funds` allows and stops without placing a partial/free unit past the funds limit, reusing the existing `engine.ts:2980-2991` loop unchanged — this spec introduces no new placement path, so no new affordability bug is possible, but the AC must be run against the fire entry specifically since it is new.
11. **AC-11 (conservation unaffected):** Before/after this change, `wasteStatsOf(s)`, `serviceCoverageOf(s)`'s `need`/`cap` values, and every existing non-fire, non-refuse `DemandFixPlanItem` are numerically IDENTICAL for the same fixture state — this is a provider-selection and render-layer change only; no coverage/need/have formula is touched. A differential test running the full pre-fix vs post-fix `demandFixPlan()`/`serviceCoverageOf()` over a corpus of saved states should show zero diffs outside the `fire`/`refuse` keys and the specific specs `optimalProvider()` picks differently from `cheapestProvider()` on FEAT-...735-shaped fixtures.
12. **AC-12 (null contract preserved):** When NO spec for a service is unlocked+enterable, `optimalProvider()` returns `null`, and both call sites (`serviceCoverageOf()`'s fire row fallback, `demandFixPlan()`'s row-skip at data.ts:2836) handle it exactly as `cheapestProvider()`'s null was handled today (row omitted from the fix plan / DemandDock's existing "needs a X — unlocks at level Y" hint path, `DemandDock.tsx:77-82`).

---

## 6. Open questions for Aaron

1. **DemandDock design point (a)** — does "Health pinned at the very top" mean BOTH the `gp` and `hosp` rows pinned together (sorted between themselves by shortfall), or a single combined Health meter? **Recommendation: both rows pinned, sorted between themselves (§3.3, option a1)** — cheapest, no data-model change, and both rows keep their independent Fix buttons.
2. **DemandDock design point (b)** — do the Housing/Shops/Industry zone-growth rows (`DemandDock.tsx:50-52`, sourced from `demandOf()`, not `serviceCoverageOf()`) join the new sortable/Health-pinned list, or stay as a separate fixed block above it? **Recommendation: stay separate (§3.4)** — different function, different value semantics, no Fix-button shape; folding them in for a "sort the service rows" ask over-reaches the request.
3. **"Best value" definition for `optimalProvider()`** — this spec defines it as: prefer the lowest **cost-per-unit-capacity** among specs that can single-handedly clear the shortfall and are affordable right now; fall back to lowest **absolute cost** otherwise (§2.2). The alternative would be **fewest total buildings** as the primary key (i.e., always prefer 1 unit over N when *any* single unit clears it, even if a cheaper multi-unit combination costs less overall) — that reading never falls back to "cheapest absolute" for the big-unit branch, so it could recommend an expensive big unit over a much cheaper pair of small ones. **Recommendation: cost-per-capacity as written above** — it satisfies "1 dam not 20 towers" (a dam clearly wins on cost-per-capacity at scale) without ever recommending an inefficient big unit just because it's singular. Every numeric threshold in this spec (the 1.05 headroom multiplier reused from `demandFixPlan()`, the affordability check against raw `s.funds`) is otherwise unchanged from existing code and remains a balance-number PLACEHOLDER per Aaron's blanket rule — nothing new to tune here beyond the selection RULE itself, which is the thing awaiting sign-off.

---

## ADDENDUM (2026-09-02, post-round correction — SUPERSEDES §2.2/§2.3's selection rule)

The independent round REJECTed the §2.2 rule as specified: ranking cost-per-capacity only within
the clears-in-one-unit set systematically overspends (measured: 5x on cleanwater at pop 20k, a 17x
cliff at shortfall capacity+1, £225M offshore array where £67M of turbines suffices, and a strictly
dominated same-unit-count case). §2.3's prediction that the tie-break "naturally picks the cheapest"
was proven false, as was the claim that no fire tier clears a whole-population shortfall in one unit
(fire_station 20,000 and fire_hq 80,000 both can).

**Corrected rule (authoritative):** score EVERY unlocked candidate by TOTAL PLAN COST —
`units = ceil(shortfall / unitCapacity)`, `planCost = units * cost` — and pick the minimum planCost
that fits the budget (fall back to cheapest single affordable unit when none fits, retaining the
Q100055 place-as-many-as-affordable downstream behaviour). Tie-break: fewer units first (the "prefer
1 dam" preference expressed correctly — it wins ties and genuine value, never overspends), then
id-ascending for determinism. This yields Aaron's intent exactly: the dam wins when its total cost
beats N towers; towers win otherwise; the capacity+1 cliff disappears.
