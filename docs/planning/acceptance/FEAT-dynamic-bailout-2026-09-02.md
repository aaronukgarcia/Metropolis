# FEAT-dynamic-bailout — Dynamic Auto-Scaled Bailout (supersedes the fixed-threshold ladder)

**Status:** DRAFT — docs only, no code. Awaiting Aaron's row-by-row approval on every placeholder number.
**mkey:** `ui.webconsole` (no dedicated FEAT- numeric key issued yet; claim one via `add-error.js`/BOW before build dispatch, tag commits `[ui.webconsole]` until then).
**Supersedes:** FEAT-1972079923 incs 1-4 and BUG-452/504/505/506's fixed £1.5M-anchored thresholds (`DEBT_THRESHOLD_FOR_BAILOUT`, `INSOLVENCY_WARNING_THRESHOLD`, `BAILOUT_INCOME_INJECTION`, `BAILOUT_INCOME_INJECTION_SECOND`) in `webconsole/src/sim/fiscal.ts:343-460`. **Keeps unchanged:** the state-machine shape (`solvent → warning → crisis → bailout → administration → bailout_second → decline`, `webconsole/src/sim/types.ts:553`), the Decline screen, and the Play Mode one-way latch (`webconsole/src/sim/fiscal.ts:565-581`, `engine.ts:3717-3744`).

## 1. Context

The landed ladder (FEAT-1972079923 incs 1-4, hardened by BUG-452/504/505/506) uses **fixed absolute thresholds** derived from `STARTING_TREASURY = 1_500_000` (`fiscal.ts:17`):

- `DEBT_THRESHOLD_FOR_BAILOUT = -1,500,000` (crisis band, `fiscal.ts:343`)
- `INSOLVENCY_WARNING_THRESHOLD = -750,000` (warning band, `fiscal.ts:351`)
- `BAILOUT_INCOME_INJECTION = 750,000` (50% of the debt hole, `fiscal.ts:384`)
- `BAILOUT_INCOME_INJECTION_SECOND = 375,000` (25% of the debt hole, worse terms, `fiscal.ts:460`)
- `MAX_FIRST_BAILOUTS = 2`, a per-tick standing cost, a sustained-recovery early exit (BUG-506), and `BAILOUT_CLEAN_END_THRESHOLD = 0` (BUG-504's closed re-arm loop).

### Aaron's ruling (2026-09-02, verbatim)

> "the 750K and 1.5M are wrong - in a long standing game there could be 10's of billions of investment and these amounts are not even a days run cost, conversely at the start of the game this could be a real life saver, so we need some proportional 'offer of help' based on the CAPEX already spent and the current bleed. if losing 20M a day then a loan of 1m won't be of any value, if we offer 25M that's 1 and 1/4 of a day's liquidity, so we should be offering a year's worth of support, and it runs for a year so the funds made available need to cover OPEX and CAPEX 'spend to save'. this only happens once. then that's it. so the direction is dynamic auto scaled help response. The player can select to go into 'play mode' where a trillion is injected - a one way latch, make clear it's play no longer simulation."

Every element of that ruling maps onto this spec:

| Ruling element | Spec section |
|---|---|
| Fixed thresholds wrong at scale | §2 trigger bands |
| Proportional offer based on CAPEX spent + current bleed | §2 formula |
| "offering a year's worth of support" | §2 injection sizing |
| "runs for a year so funds need to cover OPEX **and** CAPEX spend-to-save" | §2 facility shape, §2.4 |
| "this only happens once. then that's it." | §3 once-only |
| Play Mode trillion / one-way latch | §3.4 (unchanged, cross-referenced) |

## 2. The formula

### 2.1 Inputs the formula needs (state additions)

The landed engine tracks **funds** and **`recentFundsWindow`** (a rolling mean, `types.ts:480`, `engine.ts:1351-1356`) but has **no cumulative CAPEX tracker** — `placementCost` (`engine.ts:18`) is applied at spend time and never summed. Two new `SimState` fields are required:

- `cumulativeCapexSpent: number` — running total of every `placementCost` charged since genesis (increment at every placement/upgrade/tile-cost site currently listed in the `engine.ts` grep: lines ~2802, 2935, 2988, 3178, 3520, 3991 and the connector/rail-bridge/replan-upgrade sites ~1755-2535). Never decremented by refunds/asset sales (CAPEX spent is spent; a refund is a separate ledger event) — PLACEHOLDER design call, see Open Question §7.1.
- `recentOpexBleedPerTick: number` (derived, not stored) — the current OPEX run-rate, read from the existing `recentFundsWindow` mean-delta OR (recommended, see §7.2) from `lastFlows` directly: `sum(lastFlows.outflows) − sum(lastFlows.inflows excluding one-off injections)`, i.e. the structural burn rate independent of a single lucky/unlucky tick. Both approaches reuse machinery BUG-506 already built (`DECLINE_AVERAGING_WINDOW_TICKS`); pick one SSOT function in `fiscal.ts`, do not duplicate.

### 2.2 Trigger bands (replaces the fixed `DEBT_THRESHOLD_FOR_BAILOUT` / `INSOLVENCY_WARNING_THRESHOLD`)

Bands scale with a **days-of-runway** measure instead of an absolute £ floor, so a 10-billion-CAPEX city and a hamlet get bands proportional to their own liquidity needs:

```
dailyBleedRate = recentOpexBleedPerTick * TICKS_PER_DAY        // PLACEHOLDER: TICKS_PER_DAY constant TBD, see §7.3
runwayDays = dailyBleedRate > 0 ? funds / dailyBleedRate : Infinity   // negative when funds < 0 and bleeding

WARNING_BAND:  runwayDays <= WARNING_RUNWAY_DAYS_THRESHOLD   (⚠ PLACEHOLDER: e.g. 14 days)
CRISIS_BAND:   funds < 0  AND  runwayDays <= CRISIS_RUNWAY_DAYS_THRESHOLD  (⚠ PLACEHOLDER: e.g. 3 days, OR simply funds < 0 with bleed continuing — see §7.4)
```

`funds < 0` alone is NOT sufficient to trigger crisis (a city with £-50 and zero bleed is not in crisis); the band must read the trend, not just the level — this is the direct fix for "these amounts are not even a day's run cost" at scale, and the mirror fix for "a real life-saver" at the start (a tiny city's tiny absolute deficit is proportionally still real trouble if its bleed rate is also tiny relative to its own economy... but small in absolute terms, which is where the FLOOR in §2.5 does its job).

### 2.3 Injection sizing — "a year's worth of support"

```
capexAllowance = cumulativeCapexSpent * CAPEX_SPEND_TO_SAVE_FRACTION   // ⚠ PLACEHOLDER: e.g. 0.05 (5% of total historic CAPEX — "fix the cause")
opexAllowance  = recentOpexBleedPerTick * TICKS_PER_YEAR              // 360 ticks, existing constant, full year of CURRENT bleed rate
rawOffer       = opexAllowance + capexAllowance
offer          = clamp(rawOffer, BAILOUT_FLOOR, BAILOUT_CAP)          // §2.5
```

Worked example from the ruling: bleed = £20M/day → `opexAllowance` = 20M × 360 days-equivalent... **units check required** (see §7.3: the ruling speaks in days, ticks currently model ~1 day each per `TICKS_PER_MONTH_REF = 30`/month — confirm `TICKS_PER_YEAR = 360` really means 360 days before wiring `dailyBleedRate` and `opexAllowance` off two different tick-to-day assumptions). Assuming 1 tick ≈ 1 day (consistent with `BAILOUT_DURATION_TICKS = 360` already meaning "one game-year" at 360 ticks): `opexAllowance = 20,000,000 × 360 = 7,200,000,000`. The ruling's own arithmetic ("a loan of 25M is 1.25 days of liquidity, so offer a year") is satisfied by this shape — the offer is sized in bleed-days, not a flat multiple.

### 2.4 The facility shape — drawdown, not lump sum

Aaron's wording — **"the funds made available need to cover OPEX and CAPEX... and it runs for a year"** — means the money is *made available over the year*, not dumped into the treasury in one tick (a lump sum reintroduces the old exploit: bank it, exit crisis, the money's real value is unrelated to ongoing need). Recommendation: a **drawdown facility**, not a grant:

- On trigger, the engine records `bailoutFacility: { totalAvailable: offer, drawnSoFar: 0, enteredAt: tick }` (new `SimState` field, alongside the existing `bailoutState`).
- Each tick while active, the facility **tops up funds to a target floor** derived from the SAME bleed measure (e.g. "funds must not go below `-1 tick's OPEX`" — i.e. it behaves like an overdraft line with the offer as its ceiling), drawing down `totalAvailable` as it pays out. This directly implements "funds made available... to cover OPEX" without an unconditional cash gift.
- CAPEX portion (`capexAllowance`): made available as a **spend-to-save** allowance — drawable only against actual `placementCost` spend during the facility's active year (a `capexDrawnSoFar` sub-counter), not fungible into general funds. This is the literal reading of "spend to save": money that fixes the deficit's cause (build the missing power plant, the missing service) rather than money that just delays the reckoning.
- If `totalAvailable` is exhausted before the year ends, the facility is spent — no top-ups occur, funds fall on their own trajectory, and the year-end re-evaluation (mirroring the existing `BAILOUT_DURATION_TICKS` checkpoint) proceeds exactly as the landed ladder's crisis/no-crisis check already does.
- If the year ends with the facility still partly unused, the unused portion **lapses** (does not carry over — this is a one-time, one-year offer per the ruling, not a rolling credit line).

This is a materially different mechanic from the landed single-tick `BAILOUT_INCOME_INJECTION` credit (`engine.ts:1408-1413`) and is flagged as the single biggest build-scope item in this spec (§7.5 open question: is the simpler "size a big number, credit it all up front like today" an acceptable Phase-1, with the drawdown facility as Phase-2?).

### 2.5 Floor and cap

- **Floor:** `BAILOUT_FLOOR` — the offer must never be *less* than a minimum "the old £750k really was a lifesaver at the start" ⚠ PLACEHOLDER: propose `BAILOUT_FLOOR = STARTING_TREASURY * 0.5` (numerically = the old `BAILOUT_INCOME_INJECTION`, keeping today's early-game feel exact while everything above it now scales). Handles the ruling's own "conversely at the start of the game this could be a real life saver" clause and the `recentOpexBleedRate ≈ 0` edge case (a brand-new city insolvent from a one-off shock — e.g. a huge forced asset-sale-adjacent event — with near-zero ongoing bleed; `opexAllowance` alone would round to ~0 and undershoot the floor).
- **Cap:** `BAILOUT_CAP` — ⚠ PLACEHOLDER, needed so a runaway/degenerate bleed number (an engine bug producing an absurd per-tick outflow) cannot mint an absurd one-time injection. Propose `BAILOUT_CAP = cumulativeCapexSpent * BAILOUT_CAP_FRACTION_OF_CAPEX` (e.g. 2.0× total historic capex) — a size-of-the-city-relative ceiling, not an absolute number, keeping the "no fixed £ constant" spirit of the ruling even for the safety rail.
- Both floor and cap fire the SAME once-only consumption in §3 regardless of which formula branch produced the number — the player-visible UI messaging should say which branch applied (floored / formula / capped) so a distressed-then-recovered play session isn't confusing to debug.

## 3. Once-only enforcement

> "this only happens once. then that's it."

This is a **strictly stronger** rule than the landed `MAX_FIRST_BAILOUTS = 2` re-arm counter — the ruling allows **exactly one** dynamic offer per playthrough, full stop, not two.

- `MAX_FIRST_BAILOUTS` becomes `MAX_FIRST_BAILOUTS = 1` (or the constant is renamed/retired — the "first bailout" and "second (worse terms) bailout" two-stage ladder itself is arguably superseded too: re-read against §7.6, because "then that's it" could mean *either* (a) no second attempt at all — first insolvency after the one dynamic offer goes straight to Administration/Decline, or (b) the existing worse-terms-second-bailout stage still exists as the teeth, just never re-offering the FIRST-tier calculation twice). This spec recommends (a): **one dynamic offer, full stop** — a second insolvency in the same playthrough proceeds straight down the existing Administration → Decline path (`engine.ts:1476-1533`) with NO further injection of any kind. The existing `bailoutSecondState`/`BAILOUT_INCOME_INJECTION_SECOND` machinery is retired by this spec, not reused (see §7.6 for the alternative if Aaron prefers the two-stage ladder retained under new sizing).
- Enforcement point: the SAME `prevInsolvencyState !== 'crisis' && prevInsolvencyState !== 'administration'` guard the landed code already uses (`engine.ts:1403-1404`), gated additionally on a new boolean `dynamicBailoutUsed: boolean` (replacing the `firstBailoutCount` counter's role — a bool is simpler than a counter now that the max is 1). Once `true`, it is **never** reset by anything except a genuinely new playthrough (new-game / hard-reset-replay from genesis, GR#27-compliant).
- The **standing cost** (`bailoutStandingCostPerTick`, `fiscal.ts:561-563`) is kept as-is in shape (a felt cost while the facility is active) but its re-arm multiplier logic (`Math.max(1, firstBailoutCount)`) collapses to a constant `1` since there is no re-arm to multiply against.

### 3.4 Play Mode (unchanged)

`PLAY_MODE_INJECTION_AMOUNT = 1_000_000_000_000` (`fiscal.ts:574`) and its one-way latch (`engine.ts:3717-3744`, offered from the Decline screen, clears `declineState`, never reversible) are **kept exactly as landed** — this satisfies the ruling's closing sentence verbatim and needs no change. Cross-reference only, no new ACs beyond re-confirming the existing ones still pass once the dynamic ladder feeds into the same Decline entry point.

## 4. Migration (existing saves)

Old saves may be captured in any of these states relative to the retired fixed-ladder fields:

| Old save state | New-code behaviour |
|---|---|
| `insolvencyState: 'solvent'`, no bailout history (`firstBailoutCount` 0/undefined) | Loads clean: `dynamicBailoutUsed = false`, `cumulativeCapexSpent` backfilled once at load (see below), no double-dip risk. |
| Currently mid-`bailoutState` (first bailout active, old fixed £750k already injected) | The in-flight bailout is **grandfathered to completion under the OLD fixed terms** — do not retroactively resize an already-injected grant. On its existing year-end checkpoint (`BAILOUT_DURATION_TICKS`), mark `dynamicBailoutUsed = true` so the save is IMMEDIATELY treated as having used its one shot, regardless of the old `firstBailoutCount` value. |
| `firstBailoutCount === 1` (used the old first-tier once already, currently solvent) | Maps to `dynamicBailoutUsed = true` on load — the player already had their once-only help under the old terms; they do not get a second dynamic offer. This is the deliberate no-double-dip rule the migration table exists to state explicitly. |
| `firstBailoutCount === 2` / in or past `bailoutSecondState`/`administrationState`/`declineState` | Maps to `dynamicBailoutUsed = true`; if currently in `bailoutSecondState`, let it complete under its OLD worse-terms fixed injection (already credited) rather than resizing funds already given — the ladder below it (administration/decline) is unaffected by this spec. |
| `declineState` already set | No change — Decline/Play Mode paths are untouched (§3.4). |

`cumulativeCapexSpent` **does not exist in old saves** — it must be backfilled at load time. ⚠ PLACEHOLDER approach: since the exact historic capex spend is not recoverable from a bare funds snapshot, backfill from the CURRENT standing asset base — sum `placementCost` for every tile/building present at load (a reasonable proxy: "what it would cost to build what's standing today"), understating true lifetime spend (ignores demolished/refunded structures) but never overstating it, and never zero for a real city (avoiding a false "tiny city, tiny offer" migration cliff for an old, large, already-mid-crisis save). This backfill runs exactly once (a `capexBackfilled: boolean` migration flag) so a save is never re-summed on every subsequent load.

No migration path may crash, throw, or silently zero a loaded save's funds — every branch above is a pure additive/defaulting mapping over already-optional (`?`) fields, consistent with how `types.ts`'s existing optional fields (`firstBailoutCount?`, `recentFundsWindow?`) already tolerate absence.

## 5. Acceptance Criteria

Each AC below states an observable pass condition AND how it can fail (per `metropolis-verification-standards` — mutate the data, don't grep for it).

1. **AC-1 (proportionality — CAPEX):** Two otherwise-identical simulated cities differing ONLY in `cumulativeCapexSpent` (one 10x the other) both driven into crisis with the SAME `recentOpexBleedPerTick`: the higher-CAPEX city's computed offer is strictly greater than the lower-CAPEX city's, and the ratio of `(offer − opexAllowance)` (the CAPEX-only component) is ≈10x. FAILS if both offers are equal (proves CAPEX is not wired into the formula) or if the ratio is not monotonic with CAPEX.
2. **AC-2 (proportionality — bleed):** Two cities identical in CAPEX, differing only in `recentOpexBleedPerTick` (one 10x the other): offers differ, and the `opexAllowance` component scales ≈linearly with bleed rate. FAILS if offer is CAPEX-only (bleed ignored) or non-monotonic.
3. **AC-3 (year-of-bleed sizing):** For a city with a STABLE bleed rate B ticks⁻¹ and zero further CAPEX spend during the facility's active year, if the facility runs its full year and the player takes no corrective action, the treasury trajectory under the facility is provably better than the no-facility trajectory by an amount that converges to `B * TICKS_PER_YEAR` (± the floor/cap clamp) by year-end. FAILS if the facility's total payout across the year does not sum to (approximately) the computed `opexAllowance`.
4. **AC-4 (floor):** A city with `recentOpexBleedPerTick ≈ 0` (insolvent from a single shock, not ongoing bleed) still receives at least `BAILOUT_FLOOR`. FAILS if the raw formula (bleed×year + tiny capex%) is allowed to round below the floor and no clamp is applied.
5. **AC-5 (cap):** A synthetic/adversarial `recentOpexBleedPerTick` set to an absurd value (e.g. `Number.MAX_SAFE_INTEGER / TICKS_PER_YEAR`) produces an offer clamped at `BAILOUT_CAP`, never an overflow, `NaN`, or `Infinity` credited to funds. FAILS if `sanitizeFunds` (`fiscal.ts:328`) is the only thing standing between an absurd bleed and an absurd credited number (the cap must be a deliberate business-rule clamp, not an accidental integer-safety catch).
6. **AC-6 (once-only, single playthrough):** After the one dynamic offer is consumed (facility exhausted or year-end reached) and the city becomes insolvent a SECOND time in the same playthrough, NO further injection of any kind is credited — funds proceed straight through Administration/Decline per §3's chosen branch. FAILS if a second `IMF`-labelled inflow of any size appears in `lastFlows` after `dynamicBailoutUsed === true`.
7. **AC-7 (once-only, across save/load):** Save a game mid-facility (or immediately after `dynamicBailoutUsed` flips true), reload, and drive the city into crisis again: still no second offer. FAILS if reload resets `dynamicBailoutUsed` to false (the exact shape of the old `firstBailoutCount` re-arm bug this migration table exists to prevent) — this is the single most important regression test given BUG-504's history.
8. **AC-8 (no double-dip on migration):** Load each of the five old-save states enumerated in §4's table; assert the resulting `dynamicBailoutUsed` value matches the table exactly, and that no save produces a fresh dynamic offer it hadn't earned. FAILS if any pre-populated `firstBailoutCount >= 1` save is granted a NEW dynamic-formula offer.
9. **AC-9 (determinism):** Two runs from the same genesis + identical deterministic input stream (GR#21: no Date/random) that both cross into crisis produce byte-identical `offer`, `dynamicBailoutUsed`, `cumulativeCapexSpent`, and facility drawdown trajectories tick-for-tick. FAILS on any divergence; this is a straight port of the existing determinism-gate pattern already proven for the fixed ladder.
10. **AC-10 (conservation):** Every tick the facility pays out is a normal labelled `lastFlows.inflows` entry (new label, e.g. `Dynamic Bailout Facility Drawdown`) such that `fundsAtTickEnd === fundsAtTickStart + Σinflows − Σoutflows` holds exactly, same as every other inflow (mirrors `BAILOUT_INJECTION_LABEL`'s existing conservation guarantee). FAILS if the facility credits funds outside the flows ledger (an invisible top-up the consistency checker can't see — the class of bug GR#3/BUG-422 exists to prevent).
11. **AC-11 (CAPEX allowance is spend-gated):** During an active facility, `capexDrawnSoFar` only increases when `placementCost` is actually charged, and NEVER exceeds `capexAllowance`; attempting to place a building costing more than the remaining CAPEX allowance either partially covers it from the allowance + charges the remainder from ordinary funds, or blocks per Administration-style discretionary rules (pick one — recommend the former, see §7.5) — but never allows the CAPEX allowance to leak into ordinary discretionary spend it wasn't intended for (e.g. hiring, policies). FAILS if `capexDrawnSoFar` increases on a non-placementCost outflow, or if total CAPEX draws exceed `capexAllowance`.
12. **AC-12 (decline still reachable):** After the one dynamic offer is fully spent and the city remains insolvent past the facility's one-year window, the SAME year-end re-evaluation → Administration → (no second bailout, per chosen §3 branch) → Decline path fires exactly as today's ladder's teeth do. FAILS if a city can sit in an exhausted-facility, still-insolvent state indefinitely with the clock still advancing but no Decline ever reached (mirrors BUG-505's dead-stuck-window class).
13. **AC-13 (Play Mode unaffected):** `PLAY_MODE_INJECTION_AMOUNT`/latch behaviour and its own existing tests are unchanged and still pass byte-for-byte after this spec's fields are added to `SimState` (a pure additive change should not perturb an unrelated code path — a regression here means the new fields leaked into shared logic they shouldn't touch).

## 6. Placeholder-numbers table (for `balance-values-consolidated`)

| Constant | Proposed placeholder | Rationale (all ⚠ PLACEHOLDER, Aaron's row-by-row pass required) |
|---|---|---|
| `CAPEX_SPEND_TO_SAVE_FRACTION` | `0.05` (5% of cumulative historic CAPEX) | "fix the cause" allowance sized as a fraction of what's already been built |
| `WARNING_RUNWAY_DAYS_THRESHOLD` | `14` days runway | early warning window, mirrors old warning-band's advance-notice intent |
| `CRISIS_RUNWAY_DAYS_THRESHOLD` | `3` days runway (or `funds < 0` alone — see §7.4) | crisis = imminent, not merely "in the red" |
| `BAILOUT_FLOOR` | `STARTING_TREASURY * 0.5` (= 750,000, numerically unchanged from today's `BAILOUT_INCOME_INJECTION`) | preserves today's early-game feel exactly as a floor |
| `BAILOUT_CAP_FRACTION_OF_CAPEX` | `2.0` (cap = 2x historic capex) | safety rail against a runaway bleed number, city-relative not absolute |
| `MAX_FIRST_BAILOUTS` (retired/renamed) | replaced by `dynamicBailoutUsed: boolean` | ruling: "this only happens once", strictly < today's 2 |
| `BAILOUT_STANDING_COST_PER_TICK` | unchanged, `500` | ruling doesn't address standing cost; kept as felt-lifeline mechanic |
| `TICKS_PER_DAY` (new, if not already 1:1 with a tick) | `1` (assume 1 tick ≈ 1 day, pending §7.3 confirmation) | needed to convert the ruling's "days of liquidity" language into tick arithmetic |

Total new/changed placeholders: **7** (6 new constants + 1 semantic replacement of `MAX_FIRST_BAILOUTS`).

## 7. Open questions (with recommendations)

1. **Does `cumulativeCapexSpent` net out refunds/asset sales, or only count gross spend?** Recommend gross-only (spec §2.1) — a refund is a separate ledger event and netting it out would let a demolish/rebuild cycle manipulate the offer size downward right before triggering crisis.
2. **Is the bleed measure `recentFundsWindow`'s existing rolling mean, or a fresh `lastFlows`-based net-outflow function?** Recommend a fresh `netOpexBleedPerTick()` SSOT function in `fiscal.ts` that explicitly excludes one-off injections (bailouts, asset sales, Play Mode) from the bleed calculation — reusing `recentFundsWindow` as-is would let a PAST bailout's own injection distort the NEXT bleed reading.
3. **Tick-to-day ratio.** `BAILOUT_DURATION_TICKS = 360` is documented as "one game-year"; confirm whether 1 tick = 1 day (360-day year) or some other ratio before wiring `dailyBleedRate`/`runwayDays` — this affects every number in §2.2/§2.3 by a scale factor. Needs an engine-side confirmation, not a design guess.
4. **Crisis trigger: pure `funds < 0` vs runway-days threshold?** The ruling's own example ("losing 20M a day... a loan of 1m won't be of value") is about the OFFER size, not the trigger point. Recommend keeping the trigger simple — `funds < 0` (matches `FINAL_DECLINE_FUNDS_THRESHOLD`'s existing "truly broke" semantics) — and reserving the runway-days math for offer SIZING only, to avoid over-engineering the trigger band when the ruling didn't ask for one.
5. **Drawdown facility (§2.4) vs simple lump-sum credit (today's mechanic, just resized).** Recommend **Phase 1: lump sum with the new dynamic formula** (smallest change that satisfies "proportional offer" + "once only" + is buildable/testable quickly), **Phase 2: convert to a true drawdown facility** once Phase 1 is proven, since the drawdown facility is a materially bigger state-machine change (new `bailoutFacility` state, per-tick top-up logic, CAPEX-gated sub-ledger) that risks scope-creeping this already-large supersession. Flag for Aaron: does "runs for a year" require the facility shape at launch, or is a resized lump sum (still crediting once, still lasting through a one-year protected `bailoutState` window as today) an acceptable v1?
6. **Retire the two-stage (first/worse-terms-second) bailout ladder entirely, or keep it as the teeth after the one dynamic offer?** This spec recommends full retirement (§3) reading "then that's it" literally. Alternative: keep `bailoutSecondState` as a structurally-different, deliberately UNHELPFUL stage (e.g. a token/zero injection, existing purely to preserve the Administration-Mode UX beat) — Aaron's call.
7. **Standing-cost interaction with a resized/facility bailout — does accepting it carry a rating hit beyond `BAILOUT_STANDING_COST_PER_TICK`?** Carries forward the earlier Q100045 option-A idea (referenced in `fiscal.ts:482`) that a bailout should be a "felt lifeline", not free — Aaron has not contradicted this; recommend keeping `bailoutStandingCostPerTick` in its current shape (now always ×1, no re-arm multiplier since there is no re-arm) unless Aaron wants a NEW standing-cost dimension tied to the loan-vs-grant question below.
8. **Loan-to-repay vs grant.** The ruling calls it "help" / "support", not explicitly a loan. Recommend treating it as a grant (as today, matches `BAILOUT_INJECTION_LABEL`'s existing framing as "a legitimate external inflow... not manufactured money") — a repayment mechanic would need its own ACs and is not asked for by the ruling. Flag for Aaron to confirm no repayment obligation is wanted.

---

*Docs-only. No BOW `[mkey]` ref yet — claim a FEAT- code via `add-error.js`/`claude-bow.js add` before dispatching build work, per GR#25 (any spec with new cross-module state — `bailoutFacility`, `cumulativeCapexSpent` — must have its edges registered in code.json before implementation prose is acted on).*
