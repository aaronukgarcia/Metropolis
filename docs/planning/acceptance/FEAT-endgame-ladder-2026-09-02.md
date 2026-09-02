# FEAT: Endgame State Machine Integrity — Fix Dead-Stuck & Unbounded-Escape Defects

**Mkey:** `FEAT-endgame-ladder` (this feature bundles fixes for BUG-505, BUG-506, BUG-504)

**Intent:** Close three defects in the webconsole insolvency state machine (`webconsole/src/sim/engine.ts`, `fiscal.ts`) discovered by cold audit of the endgame ladder. Two defects are design-independent and must be fixed under any design (BUG-505: dead-stuck state; BUG-506: no early exit). One defect is design-dependent on Aaron's ruling on first-bailout re-arm policy (BUG-504: unbounded free rescue).

**Bugs affected:** BUG-505 (P1), BUG-506 (P2), BUG-504 (P1).

---

## Design-Independent Acceptance Criteria

### BUG-505: No Dead-Stuck Crisis State

**Defect:** The first-bailout year-end clean-end check (engine.ts line 1324: `if (funds >= DEBT_THRESHOLD_FOR_BAILOUT)`) uses a >= comparison. When funds equal `DEBT_THRESHOLD_FOR_BAILOUT` exactly (–1,500,000 GBP), the clean-end triggers and `bailoutState` is cleared to `null`. However, the raw insolvency band computed by `insolvencyStateForFunds(funds)` (fiscal.ts line 358-361) still returns `'crisis'` because the check uses `<=`. This creates a configuration where:
- Exposed `insolvencyState` is `'crisis'` (from the raw band)
- `bailoutState` is `null`
- `bailoutSecondState` is `null`
- `administrationState` is `null`

The fresh-bailout trigger (engine.ts line 1310-1316) is guarded by `prevInsolvencyState !== 'crisis'`, so it can never fire. The `enterAdministration` action (engine.ts line 3073) requires `bailoutState` or `bailoutSecondState` to be non-null, so it is unavailable. The city is stranded with no rescue path and no escalation path — a permanent dead-stuck crisis.

**AC-505-1: State Machine Reachability Invariant**

The state machine configuration must be impossible to reach: exposed `insolvencyState === 'crisis'` AND all of `bailoutState`, `bailoutSecondState`, `administrationState` are `null` simultaneously.

**Proof approach:** Verify that either:
- The clean-end threshold sits strictly ABOVE `DEBT_THRESHOLD_FOR_BAILOUT` (so clean-end implies funds > crisis threshold, not in crisis), OR
- The fresh-bailout trigger can re-fire from a still-crisis state (remove the `prevInsolvencyState !== 'crisis'` guard), OR
- The `enterAdministration` action is reachable without an active bailout (broaden the guard at engine.ts line 3073).

**AC-505-2: Exact-Boundary Test (RED test — must fail without fix)**

Reproduce the dead-stuck state explicitly:
1. Drive city to exactly `DEBT_THRESHOLD_FOR_BAILOUT` (–1,500,000) funds.
2. Advance 360 ticks so the first-bailout year ends exactly at funds = –1,500,000 (clean-end triggers, `bailoutState` clears).
3. Assert on the NEXT tick that `insolvencyState === 'crisis'` (raw band).
4. Assert that EITHER:
   - `bailoutState` is non-null (a new bailout has fired), OR
   - `administrationState` is non-null (administration is accessible), OR
   - The raw band has transitioned OUT of crisis (funds have recovered above the threshold).
5. If all three are null AND raw band is still crisis, the test FAILS (dead-stuck confirmed).

**Testable condition:** funds == DEBT_THRESHOLD_FOR_BAILOUT = -1500000; tick >= bailoutState.enteredAt + BAILOUT_DURATION_TICKS; no escalation path available next tick.

---

### BUG-506: No Early Exit From Bailout States; Single-Tick Decline Decision

**Defect:** Bailout and decline decisions are tied to year-end checkpoints only. A city in bailout (first or second) remains in the overlay state for exactly `BAILOUT_DURATION_TICKS` or `SECOND_BAILOUT_DURATION_TICKS` (both 360 ticks) regardless of funds recovery mid-period. More critically, the decline decision (second bailout year-end, line 1405-1407) is based on funds at a SINGLE tick: if funds < `FINAL_DECLINE_FUNDS_THRESHOLD` (0) at tick = enteredAt + 360, the game enters `declineState` (permanent game-over), even if funds have been positive for 300+ of those 360 ticks. Conversely, a solvent-all-year city going negative at tick 360 is forced into decline. This feels non-deterministic (and frustrating) despite being deterministic.

**AC-506-1: Sustained Recovery Early Exit**

The bailout/bailout_second states must have an early-exit condition based on sustained solvency, not just year-end.

**Definition of "sustained":** funds >= 0 for N consecutive ticks without interruption, where N is a configuration parameter (Aaron's balance-pass decision; suggested N = 30 ticks = 1 month, matching monthly UI cadence). An early exit clears the `bailoutState` or `bailoutSecondState` (whichever is active) and reverts the exposed `insolvencyState` to the raw funds band (solvent/warning/crisis as computed by `insolvencyStateForFunds`).

**Proof approach:**
- When `bailoutState` is active AND funds >= 0 for N consecutive ticks, clear `bailoutState` and update `insolvencyState`.
- When `bailoutSecondState` is active AND funds >= 0 for N consecutive ticks, clear `bailoutSecondState`.
- Track tick-sequence of recovery without breaking the invariant: the N-tick counter resets to 0 if funds dip below 0.

**AC-506-2: Sustained Recovery Early-Exit Test (RED test — must fail without fix)**

1. Enter first bailout (funds below DEBT_THRESHOLD_FOR_BAILOUT).
2. Advance to exactly N = 30 ticks with funds >= 0 every tick.
3. Assert on tick N that `bailoutState === null` (state cleared by early exit).
4. Assert that `insolvencyState` reads the raw funds band (solvent if funds > 0, warning if in the warning range, never 'bailout').

**AC-506-3: Averaged Decline Decision (Not Single-Tick)**

The second bailout's year-end decline check (engine.ts line 1405) must base the decline decision on MORE than a single-tick sample. The decision must summarize funds stability over the final period of the bailout window.

**Definition:** Compute one of:
- **Averaging:** the mean of funds over the FINAL N ticks of the bailout period (e.g., final 30 ticks = final month). Enter decline only if mean < `FINAL_DECLINE_FUNDS_THRESHOLD`.
- **Threshold window:** funds must be >= `FINAL_DECLINE_FUNDS_THRESHOLD` for at least M of the FINAL N ticks (e.g., at least 20 of the final 30). Enter decline only if M is not satisfied.
- **Conservative gate:** track the maximum funds achieved during the bailout; enter decline only if max < `FINAL_DECLINE_FUNDS_THRESHOLD` (city never recovered at all).

Aaron's balance-pass ruling will select and parameterize the exact method. The test must use a derived expectation value (computed from the selected method + constants), never hardcoded.

**AC-506-4: Averaged Decline-Decision Test (RED test — must fail without fix)**

1. Enter second bailout at funds below DEBT_THRESHOLD_FOR_BAILOUT.
2. Advance 340 ticks with funds >= 0 every tick (good recovery).
3. On ticks 341–360 (the FINAL 20 ticks), drive funds to < FINAL_DECLINE_FUNDS_THRESHOLD (but NOT low enough to trigger overdraft interest collapse).
4. At tick 360 (year-end), assert that the game does NOT enter `declineState` (averaged/window method should detect the bulk-period recovery and only penalise the tail dip).
5. Now repeat the test reversed: funds < 0 for 340 ticks, then funds >= 0 for the final 20. Assert that the game DOES enter decline (bulk-period insolvency dominates).

**Testable conditions:**
- Averaging: `mean(funds[finalN_start..tick-1]) >= FINAL_DECLINE_FUNDS_THRESHOLD` must be true to avoid decline.
- Window: `sum(funds[finalN_start..tick-1] >= FINAL_DECLINE_FUNDS_THRESHOLD) >= M` must be true to avoid decline.
- Test derives M and N from code constants, never hardcodes them.

---

## DECISION REQUIRED: First-Bailout Re-Arm Policy (Q100045, BUG-504)

### The Defect

The first-bailout clean-end condition (engine.ts line 1324: `funds >= DEBT_THRESHOLD_FOR_BAILOUT`, i.e., funds >= –1,500,000) and the injection amount (`BAILOUT_INCOME_INJECTION` = 750,000 GBP) create a loop: if a city drains less than 750,000 GBP per year, it will stay above the clean-end threshold indefinitely, re-entering clean-end yearly, and re-collecting a fresh 750,000 GBP bailout grant each year forever. This bypasses the endgame ladder entirely — the city never escalates to a second bailout or administration, because it never stays below the clean-end bar for more than one year at a time.

**Constants involved:**
- `DEBT_THRESHOLD_FOR_BAILOUT` = –`STARTING_TREASURY` = –1,500,000 (fiscal.ts line 343)
- `BAILOUT_INCOME_INJECTION` = 0.5 × `STARTING_TREASURY` = 750,000 (fiscal.ts line 384)
- `BAILOUT_DURATION_TICKS` = 360 (fiscal.ts line 371)

**Proof:** A city with annual spending rate S < 750,000 will end the bailout year with funds = (funds_at_entry) + 750,000 – S × 360_ticks, which simplifies to funds > –1,500,000 + (750,000 – S × 360) > –1,500,000 if S < 750,000 / 360 ≈ 2,083 GBP/tick. This is well within the range of small cities.

### Design Options

**Option A: Clean-End Only on Real Solvency; Re-Arm Capped**

- Raise the clean-end threshold from `DEBT_THRESHOLD_FOR_BAILOUT` to `FINAL_DECLINE_FUNDS_THRESHOLD` (0). Clean-end only when funds >= 0 (truly solvent).
- Cap first-bailout re-arms: a city may receive AT MOST N first bailouts (e.g., N = 2) before forced escalation to second bailout or administration.
- Each bailout (or each bailout after the first) carries a gameplay cost: e.g., a permanent rating hit (–10 approval?), a standing interest surcharge on next overdraft, or a cooldown period before re-armament.
- **Effect:** Longer bailout windows (city must climb to solvency, not just above the debt floor). Finite rescues force endgame escalation. The cost makes each bailout a felt consequence, not free.
- **AC shape:** Clean-end threshold, re-arm counter per city, cost formula, cooldown duration (if used).

**Option B: Bailouts Are Repeatable; Add Per-Bailout Cost**

- Keep the clean-end threshold at `DEBT_THRESHOLD_FOR_BAILOUT` (no change to the current threshold logic; the design intent is a "safety net" for recurring slumps).
- Add a cost to each bailout: e.g., a one-time interest surcharge on the next year's overdraft, or a reduced injection on subsequent bailouts (first = 750k, second = 500k, third = 250k, then capped at 0).
- OR reduce the injection RATE over subsequent bailouts, so the 2nd injection = 0.5 × 750k, etc.
- OR add a standing "bailout tax" that activates whenever a city is in `bailoutState`: increase overdraft interest, reduce tax revenue, impose a service-disruption penalty (lower happiness while actively bailed out).
- **Effect:** The safety net remains (matching the "small cities get support" lore), but it's not free. Players are incentivised to fix underlying problems (spending discipline, revenue growth) rather than rely on yearly rescues.
- **AC shape:** Cost/tax formula, reduced-injection sequence, overdraft surcharge during bailout.

**Option C: Aaron Specifies Exact Balance Decision**

- Define a new ruling that specifies the exact clean-end bar, re-arm policy, and per-bailout cost.
- Likely candidates from the balance-number regime: Aaron picks a clean-end threshold (e.g., funds > –750k to force a real recovery window), a max re-arm count (e.g., 1 bailout total = no second bailout), and/or a cost (e.g., a rating hit or future interest surcharge).

### Acceptance Criteria Shape (Aaron to Select)

**For each option, the build will produce:**

1. A clean-end funds threshold constant, named and documented in `fiscal.ts` (distinct from `DEBT_THRESHOLD_FOR_BAILOUT` if option A is chosen).
2. If re-arm is capped: a `MAX_FIRST_BAILOUTS` constant and a counter field in `SimState` tracking re-arms.
3. If per-bailout cost applies: a cost-formula function in `fiscal.ts` (e.g., `bailoutInterestSurcharge()` or `bailoutInjectionSequence()`) and a ledger entry or field recording the cost.
4. RED tests for each path:
   - A city at the clean-end threshold stays bailed-out (not cleared early).
   - A city above the clean-end threshold is released from bailout (if no cap applies) or hits the re-arm cap (if option A).
   - Per-bailout cost is applied and visible to the player (via ledger or UI penalty).

**Every constant and threshold is a PLACEHOLDER under the balance-number regime.** Aaron's row-by-row approval is required before the code is committed. Test expectations must derive from code constants (via functions like `insolvencyStateForFunds()` and `bailoutInjectionSequence()`), never hardcoded.

---

## Test Coverage Required

### RED Tests (must fail without the fix; all must pass after)

1. **BUG-505 / AC-505-2: Exact-Boundary Dead-Stuck Test**
   - Setup: City at funds = –1,500,000 (DEBT_THRESHOLD_FOR_BAILOUT exactly) at first-bailout year-end tick.
   - Advance one tick. Assert: (bailoutState is non-null) OR (administrationState is non-null) OR (raw insolvencyState out of crisis).
   - Current behavior: All three are null, raw state is crisis. TEST FAILS.
   - Expected behavior: At least one rescue path exists. TEST PASSES.

2. **BUG-506 / AC-506-2: Sustained Recovery Early-Exit Test**
   - Setup: City in first bailout, funds below DEBT_THRESHOLD_FOR_BAILOUT.
   - Advance 30 ticks (N = MONTH_TICKS = 30, derived from constants) with funds >= 0 every tick.
   - Assert: bailoutState is null; insolvencyState reads solvent (not 'bailout').
   - Current behavior: bailoutState still active at tick +30. TEST FAILS.
   - Expected behavior: Cleared by early exit. TEST PASSES.

3. **BUG-506 / AC-506-4: Averaged Decline-Decision Test (Recovery then Collapse)**
   - Setup: City in second bailout at tick 0 (enteredAt = tick).
   - Advance to tick +340 with funds >= 0 every tick (340 ticks of good recovery).
   - Advance to tick +360 with funds < FINAL_DECLINE_FUNDS_THRESHOLD for the final 20 ticks.
   - Assert at tick +360: declineState is null (NOT entered; averaged method detected bulk recovery).
   - Current behavior: Funds < 0 at tick 360, so declineState is set. TEST FAILS.
   - Expected behavior: Decline avoided by recovery history. TEST PASSES.

4. **BUG-506 / AC-506-4: Averaged Decline-Decision Test (Collapse then Recovery)**
   - Setup: City in second bailout.
   - Advance to tick +340 with funds < FINAL_DECLINE_FUNDS_THRESHOLD every tick.
   - Advance to tick +360 with funds >= 0 for the final 20 ticks.
   - Assert at tick +360: declineState is non-null (entered; bulk-period insolvency dominates).
   - Current behavior: Funds >= 0 at tick 360, so declineState is not set. TEST FAILS.
   - Expected behavior: Decline entered despite tail recovery. TEST PASSES.

5. **BUG-504 / AC-504 (Pending Aaron's Q100045 Selection)**
   - Once Aaron selects option A, B, or C, the test(s) for re-arm policy, clean-end threshold, and per-bailout cost will be defined here.
   - Test shape: Drive funds to exactly clean-end threshold, verify escalation path (cap hit, cost applied, or forced second bailout).
   - Derived expectation: All thresholds and costs computed from `fiscal.ts` constants, never hardcoded.

### Invariant Properties (Hold at All Ticks)

1. **No Dead-Stuck (BUG-505):** If `insolvencyState === 'crisis'`, then `bailoutState || bailoutSecondState || administrationState` must be non-null, OR the next tick's transition must exit crisis.
2. **Reachability (BUG-506):** If in bailout/bailout_second with funds >= 0 for N ticks, an early exit MUST clear the state within N+1 ticks.
3. **Decline Stability (BUG-506):** If `declineState` is set, it must remain set (hard stop); the hard-stop guard at top of `advance()` (line 995) must never be removed.
4. **Cost Visibility (BUG-504):** If a per-bailout cost applies, it must appear in `lastFlows` (ledger) and in the debug JSON snapshot, deriving from a named constant.

---

## Assumptions for Aaron / Bev

1. **BUG-505 and BUG-506 are design-independent:** The fixes are mechanical state-machine repairs that do not depend on Aaron's Q100045 ruling. They must be implemented regardless of which option (A/B/C) is chosen for BUG-504.

2. **BUG-504 is design-dependent:** The fix requires Aaron's selection of re-arm policy (Q100045). Until Aaron rules, a stub or placeholder constant is acceptable (e.g., clean-end threshold stays at the current value, re-arm cap is infinite). Once the ruling is made, the build must update the constant(s) and pass the new test(s).

3. **All thresholds and injection amounts are PLACEHOLDERs per the balance-number regime.** Aaron's row-by-row approval is needed before any number is "final." Test expectations must derive from code constants via functions (e.g., `insolvencyStateForFunds()`, a `bailoutCleanEndThreshold()` function, or constants like `MAX_FIRST_BAILOUTS`), never hardcoded.

4. **BUG-506 depends on N (sustained-recovery tick window) and M/method (decline-decision averaging method).** These are not named constants in the current code. Aaron's balance pass must define N (default suggestion: 30 ticks = 1 month) and select the decline-decision method (averaging, threshold-window, or max-achievement). The build will expose these as named constants in `fiscal.ts` (e.g., `SUSTAINED_RECOVERY_TICKS` = 30, `DECLINE_DECISION_METHOD` = "averaging" with `DECLINE_AVERAGING_WINDOW_TICKS` = 30).

5. **Conservation invariant must hold exactly:** Every inflow/outflow (including bailout injections and per-bailout costs) must be booked as `FlowItem` entries in `inflows`/`outflows` so that `fundsAtTickEnd === fundsAtTickStart + Σinflows − Σoutflows` remains byte-exact. Use the existing SSOT labels (e.g., `BAILOUT_INJECTION_LABEL`, `BAILOUT_SECOND_INJECTION_LABEL`) and any new ones (e.g., `BAILOUT_COST_LABEL` if option B is chosen).

6. **Determinism (GR#21) must be maintained:** All state transitions (bailout entry/exit, early-exit detection, decline decision) are derived from `funds`, `tick`, and the constant thresholds — no `Date.now()`, no `Math.random()`, no external state. A deterministic replay will reproduce the exact sequence of states byte-identically.

7. **Admission to the second bailout must remain automatic at first bailout year-end** (engine.ts line 1328-1341: if funds still < DEBT_THRESHOLD_FOR_BAILOUT, `bailoutSecondState` enters automatically, no player action). The only exception is if Option A's re-arm cap is chosen and the cap is exhausted (forced escalation to administration instead). This ensures the ladder is escape-proof: a player cannot stall in the first bailout forever by doing nothing.

---

## Real Code References (Grounding)

| Concept | Real Symbol | File | Lines |
|---------|-------------|------|-------|
| Crisis threshold (bailout entry) | `DEBT_THRESHOLD_FOR_BAILOUT` | fiscal.ts | 343 |
| Warning threshold | `INSOLVENCY_WARNING_THRESHOLD` | fiscal.ts | 351 |
| Raw band computation | `insolvencyStateForFunds(funds)` | fiscal.ts | 358–361 |
| First-bailout duration | `BAILOUT_DURATION_TICKS` | fiscal.ts | 371 |
| First-bailout injection | `BAILOUT_INCOME_INJECTION` | fiscal.ts | 384 |
| Admin duration | `ADMINISTRATION_DURATION_TICKS` | fiscal.ts | 416 |
| Second-bailout duration | `SECOND_BAILOUT_DURATION_TICKS` | fiscal.ts | 450 |
| Second-bailout injection | `BAILOUT_INCOME_INJECTION_SECOND` | fiscal.ts | 460 |
| Decline threshold | `FINAL_DECLINE_FUNDS_THRESHOLD` | fiscal.ts | 479 |
| First-bailout trigger | Fresh-bailout guard | engine.ts | 1310–1316 |
| First-bailout clean-end check | `if (funds >= DEBT_THRESHOLD_FOR_BAILOUT)` | engine.ts | 1324 |
| First-bailout auto-escalate | Auto-trigger second bailout | engine.ts | 1328–1341 |
| Admin entry (user-initiated) | `enterAdministration` action | engine.ts | 3072–3082 |
| Hard-stop on decline | Decline-state guard | engine.ts | 995 |
| Exposed state overlay | Overlay logic | engine.ts | 1465–1472 |

---

## Summary

This feature fixes three defects in the endgame state machine:

1. **BUG-505 (P1, design-independent):** Dead-stuck crisis state where clean-end clears bailout but raw band stays in crisis, blocking all escape paths. **Fix:** Ensure the clean-end threshold sits strictly above the crisis threshold, or allow fresh-bailout re-trigger from crisis, or make administration reachable without an active bailout.

2. **BUG-506 (P2, design-independent):** No early exit from bailout/decline states; single-tick decline decision feels non-deterministic. **Fix:** Add sustained-recovery early exit (N consecutive ticks of funds >= 0 clears bailout) and average the decline decision over a final window (not a single-tick sample).

3. **BUG-504 (P1, design-dependent on Q100045):** Unbounded yearly re-rescue loop where draining < 750k/year lets a city stay above clean-end bar and re-collect 750k every year. **Fix:** Requires Aaron's ruling on first-bailout re-arm policy (option A: cap re-arms + raise clean-end bar to solvency; option B: repeatable with per-bailout cost; option C: Aaron-specified exact balance).

**All thresholds, injection amounts, durations, and costs are PLACEHOLDERs per the balance-number regime.** Test expectations derive from code constants, never hardcoded. Determinism, conservation, and reachability invariants must hold exactly.
