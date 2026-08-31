# FEAT-1972079923: IMF bailout + administration + decline endgame

**Mkey:** FEAT-1972079923  
**Epic:** Baseline One—watchable deterministic year (Northstar waypoint 2)

**Closes:** BUG-396 (silent-fail insolvency)  
**Relates:** BUG-438 (overdraft compound), BUG-397 (economic shape), FEAT-146 (why-did-my-city-die autopsy)

**GR#25 scope:** webconsole-internal `fiscal.ts` / `engine.ts` / `MapView.tsx` + error framework registry codes. No new `code.json` edges.

---

## Design Ruling (Aaron, 2026-08-31)

The city enters an insolvency flow when debt crosses a threshold. The IMF bailout event lasts exactly 1 game-year (360 ticks) with imposed conditions: the player **must** choose to sell city assets (listed in construction order / by building age) to reduce debt, **or** enter Administration Mode. Administration constrains spending and lasts 1 year max; a POPUP communicates the active conditions when the mode is entered. If the city remains insolvent after the first bailout year, a SECOND bailout becomes available on worse terms. If still insolvent after the second year, a FINAL SCREEN shows the decline statistics (hard game-over; options: Start Over or Load Save). Every refused action while broke (place, advisor) gives explicit cannot-afford feedback, replacing silent no-ops.

---

## Placeholder Constants (balance-number regime)

All player-felt thresholds, terms, and spending limits are named PLACEHOLDER constants sourced from `fiscal.ts` for Aaron's row-by-row balance approval. The balance pass must update these values; all tests reference the exports, never hardcoded numbers.

| Constant | Value (placeholder) | Usage | Notes |
|----------|---------------------|-------|-------|
| `DEBT_THRESHOLD_FOR_BAILOUT` | −£10,000,000 | Funds <= this value triggers bailout | Negative threshold; exact floor TBD |
| `BAILOUT_DURATION_TICKS` | 360 | Duration of first bailout (1 game-year) | Must equal TICKS_PER_YEAR |
| `BAILOUT_SECOND_THRESHOLD_WORSE_TERMS` | −£8,000,000 | Funds <= this if still insolvent at year-end → second bailout available | More lenient than first (higher floor, more breathing room) |
| `ADMINISTRATION_SPENDING_MULTIPLIER` | 0.4 | All outflows multiplied by this in admin mode | e.g., 0.4 = 60% cutback; allows basic services only |
| `ADMINISTRATION_DURATION_TICKS` | 360 | Administration lasts 1 game-year | Must equal TICKS_PER_YEAR |
| `SECOND_BAILOUT_DURATION_TICKS` | 360 | Duration of second bailout | Must equal TICKS_PER_YEAR |
| `BAILOUT_INCOME_INJECTION` | £2,000,000 | Funds boosted by this on bailout entry | Positive injection to give breathing room (per-bailout TBD) |
| `FINAL_DECLINE_SCREEN_THRESHOLD` | (none) | Player reaches final screen if still insolvent after 2 years | Mechanical: end of year 2 bailout while `funds < 0` → game-over |

---

## Acceptance Criteria

### AC-1 (debt threshold triggers bailout state)

**Scenario:** City at `funds = £0`, steady state. An outflow (e.g., payroll) pushes `funds` negative. Check: after the tick that crosses below `DEBT_THRESHOLD_FOR_BAILOUT`, the engine state records:
- `game.insolvencyState === 'bailout'` (or equivalent; exposed state name TBD)
- `game.bailoutEnteredAt === currentTick`
- A MapView banner displays "BAILOUT: You have 1 year to restore solvency. Sell assets or enter Administration."

**Mutation:** raise `DEBT_THRESHOLD_FOR_BAILOUT` such that the test funds are above it; bailout does not trigger. This test turns red.

**False-pass:** banner appears but state field is not set; game-over logic later reads stale state and does not apply bailout rules.

---

### AC-2 (bailout duration is exactly 1 year, ticks are deterministic)

**Scenario:** Bailout enters at tick N. Check: after tick N + 360, if `funds >= DEBT_THRESHOLD_FOR_BAILOUT` (solvency restored), then:
- `game.insolvencyState === 'none'` (bailout ends)
- No second bailout triggers (funds above threshold)
- Map banner clears (if present)

If `funds <= DEBT_THRESHOLD_FOR_BAILOUT` still, then:
- `game.insolvencyState === 'bailout'` still (no transition; second bailout logic separate)

**Mutation:** change the duration hardcoded in the tick-counting to 359 or 361; this test fails (bailout ends early or persists too long).

**False-pass:** duration logic is wall-clock-based (Date.now()) instead of tick-count deterministic. Re-running from a save skips or doubles the final tick.

---

### AC-3 (asset list presented in construction/age order at bailout entry)

**Scenario:** Bailout triggers. Check: engine exposes an immutable asset list (array of buildings, TBD shape) sorted by:
- **Primary:** construction age (oldest-placed buildings first, or newest-placed buildings first per Aaron confirmation)
- **Secondary:** within each age tier, by construction zone kind (roads / power / etc.)

MapView renders a "FORCED ASSET SALES" panel listing these in the sorted order, showing each building's name, location, and estimated sale value (deterministic per current state, not a guess).

**Mutation:** shuffle the asset list randomly (e.g., `sort(() => Math.random() - 0.5)`); the UI shows them out of order. This test fails.

**False-pass:** asset list sorted by building type (zone kind) instead of construction order; chronology is reversed.

---

### AC-4 (forced asset sale: sale inflow reduces debt, labeled in ledger)

**Scenario:** Bailout active, FORCED ASSET SALES panel visible. Player selects a building of value £V and "Sell". Check:
- `state.funds` increases by £V (or £V minus a fee, if applicable; fee structure TBD in design call)
- `lastFlows` contains a new inflow entry with:
  - `label === 'Asset Sale'` (or 'Forced Asset Sale'; exact label TBD)
  - `value === V` (or V − fee, matching the funds increase exactly)
- The building is removed from the map
- The asset list updates to show the building is sold (disabled/removed from UI)

**Mutation:** apply the sale but do not write the ledger entry; `lastFlows` lacks the Asset Sale line. Consistency checker catches a funds jump with no explaining inflow.

**False-pass:** ledger entry exists but has wrong label (e.g., 'Buildings Sold' instead of asset-sale label); conservation logic cannot trace the flow.

---

### AC-5 (administration mode: player can enter to skip forced sales)

**Scenario:** Bailout active, FORCED ASSET SALES panel visible, popup states: "Sell assets or enter Administration Mode (spending constrained, 1 year to recover)." Player clicks "Enter Administration" button. Check:
- `game.insolvencyState === 'administration'` (or state-machine state TBD)
- `game.administrationEnteredAt === currentTick`
- FORCED ASSET SALES panel closes (no more sales offered)
- A new "ADMINISTRATION MODE" banner appears (MapView)
- The city **remains playable**: clock ticks, citizens move, decay/growth proceed

**Mutation:** delete the "Enter Administration" button; no alternative to forced sales. Player cannot avoid the asset-sale flow.

**False-pass:** administration state is set but the clock stops (frozen gameplay); or spending is not constrained (AC-6 missing).

---

### AC-6 (administration spending constrained, no paid buildings allowed)

**Scenario:** City in Administration mode. Player attempts:
1. Place a paid building (e.g., School, cost £32,000) → **fail**
2. Place a free building (e.g., road, cost £0) → **succeed**

Check:
- Paid `place()` call returns unchanged state + error feedback: "Cannot afford building under Administration Mode" (or similar; exact message TBD).
- Advisor does **not** offer paid buildings when in Administration
- All outflows (except debt interest / mandatory charges, if any) are multiplied by `ADMINISTRATION_SPENDING_MULTIPLIER` (e.g., 0.4)
- `lastFlows` shows each outflow reduced (e.g., Roads normally £50k → £20k during admin)

**Mutation:** remove the outflow multiplier; admin mode exists in state but outflows proceed at full cost. Test goes red: funds deplete faster, city recovers slower or not at all.

**False-pass:** popup message says "Administration" but advisor still recommends paid buildings; UI says constrained but ledger still books full outflows.

---

### AC-7 (administration lasts exactly 1 year, then re-evaluate)

**Scenario:** Administration enters at tick N. Check: after tick N + 360:
- If `funds >= DEBT_THRESHOLD_FOR_BAILOUT` → `game.insolvencyState === 'none'` (admin ends, city recovers)
- If `funds < DEBT_THRESHOLD_FOR_BAILOUT` AND `funds >= BAILOUT_SECOND_THRESHOLD_WORSE_TERMS` → no auto-transition (city remains broke, but second bailout is now available to player if they click)
- If `funds <= BAILOUT_SECOND_THRESHOLD_WORSE_TERMS` → second bailout is automatically **offered** (OPEN QUESTION: Aaron, is this automatic, or does the player have to request it? Interim: offering is mechanical; acceptance is optional)

**Mutation:** administration duration is hardcoded to 180 ticks (half a year); admin ends early. Test goes red: re-evaluation happens at the wrong tick.

**False-pass:** wall-clock timer used instead of tick-count; re-running a save advances the timer incorrectly.

---

### AC-8 (popup communicates bailout conditions exactly once per mode entry)

**Scenario 1 (bailout entry):** Funds cross `DEBT_THRESHOLD_FOR_BAILOUT`. Check: a modal/popup appears with:
- Title: "BAILOUT: <duration> Game-Year Intervention" (or similar wording)
- Body: lists the player's two choices:
  1. Sell city assets (forced sales list follows)
  2. Enter Administration Mode (spending cut to X%, lasts 1 year)
- Buttons: "I understand" or similar dismissal (not "Ignore" or "Close")
- **Appears exactly once** (tick N only; not on every subsequent tick)

**Scenario 2 (admin mode entry):** Player clicks "Enter Administration". Check: popup updates to:
- Title: "ADMINISTRATION MODE: <duration> Game-Year Recovery Plan"
- Body: spending is cut to (100 * ADMINISTRATION_SPENDING_MULTIPLIER)%, mandatory paid buildings are blocked, clock continues
- Button: "Begin" or dismissal

**Mutation:** delete the popup render; game enters bailout silently. Player has no UI cue. Test goes red: popup is not observed.

**False-pass:** toast/banner instead of modal; ephemeral message that disappears in 3 seconds is not a "statement of conditions" (AC spec says popup **states** conditions, implying persistent until dismissed).

---

### AC-9 (cannot-afford feedback on all refused actions while broke)

**Scenario 1 (place fails while broke):** Funds are negative, player clicks on a tile to place a £50k building. Check:
- `place()` returns unchanged state + explicit error in registry code (error framework): e.g., "Cannot afford: School costs £50,000, funds £<negative>."
- Error is **visible to the player** (toast, banner, or modal; exact UX TBD) and **persists until acknowledged or a new action supersedes it**.

**Scenario 2 (advisor filtered):** City is in bailout/admin, advisor does **not** suggest a building the player cannot place. Advisor checks `place()` preconditions (affordability, space) before offering.

**Scenario 3 (construct mode locked):** If construct mode (roads / zones) has an affordability check, the same "Cannot afford" message appears for free-tier upgrades (free road tier → paid upgrade).

Check across all three:
- Error code is registered in `data/errors.json` (or equivalent registry; FEAT-1972079916 integration TBD)
- Message text includes the shortfall amount (`costs X, funds Y`)
- Message is deterministic (same funds = same message; no randomness)
- Test can **mutate the error message to the opposite tone** (e.g., "You can afford this") and the test goes red (proving the message is actually read, not stubbed)

**Mutation:** silent no-op `return state` with no error; player sees no feedback. Test goes red: no toast / no error in state.

**False-pass:** error appears in console only; player UI is blank. Or message says "Action blocked" without saying why (funds not mentioned).

---

### AC-10 (second bailout available on worse terms after first year if insolvent)

**Scenario:** First bailout entered at tick N. At tick N+360, `funds < DEBT_THRESHOLD_FOR_BAILOUT` (still broke). Check:
- A new "Second Bailout Available" button/link appears on the HUD or in a status panel
- Player clicks it; state transitions: `game.insolvencyState === 'bailout_second'`
- `game.bailoutSecondEnteredAt === currentTick`
- A new `BAILOUT_INCOME_INJECTION_SECOND` (PLACEHOLDER, worse terms) is applied to funds
- All ACs from AC-2 through AC-4 apply to the second bailout:
  - Duration is 360 ticks
  - Forced asset sales list reappears
  - Administration mode is again available as an alternative
- **OPEN QUESTION:** Aaron confirms: is second bailout automatic (state enters it without user click) or user-initiated (button required)? Interim: assume user-initiated; button is always available once first year ends and funds <= threshold.

**Mutation:** second bailout is not offered (no button, no state transition). At the end of year 2, the city proceeds directly to decline screen. Depending on Aaron's intention, this test may be red (if auto) or green (if player-initiated and player did not click).

**False-pass:** second bailout state is set but `bailoutSecondEnteredAt` is not recorded; later logic cannot compute the true 2-year duration.

---

### AC-11 (decline screen shows stats and hard game-over on third-year insolvency)

**Scenario:** Second bailout enters at tick M. At tick M+360, `funds < 0` still. Check:
- Game state transitions: `game.insolvencyState === 'decline'` (or 'game_over')
- A FINAL SCREEN renders showing:
  - "City in Decline: Insolvency Unresolved"
  - Decline statistics:
    - Peak population (before decline)
    - Final population (at game-over tick)
    - Years played (ticks / 360, rounded)
    - Total funds deficit (min funds reached during play)
    - Total spending over play (sum of all outflows from start to end)
    - Cause of decline: "Persistent insolvency after 2 bailout years" (or similar)
  - Buttons: "Start Over" (new game), "Load Save" (if save-load is implemented)
- Clock **stops**: no further ticks until an action is taken
- **No** third bailout is offered
- Game is not closed or quit; player sees the stats screen until they act

**Mutation:** remove one of the stats (e.g., population delta); screen still renders but is incomplete. True assertion: all listed stats are present and are non-zero / non-default values.

**False-pass:** screen shows default placeholders instead of computed stats (e.g., "Population: 0" when actual is 100k).

---

### AC-12 (determinism: all triggers derive from state, no wall-clock)

**Scenario:** Replay a save from tick 50 to tick 400, passing through all insolvency states (bailout → admin → second bailout → decline). Check:
- Every state transition (bailout entry, admin entry, year-end re-evaluation, decline) occurs at the **same tick** as the first playthrough
- Popup text, asset list order, and decline stats are **identical** on replay
- No `Date.now()`, `Math.random()`, or other non-state inputs are used in the logic

Mechanically:
- `bailoutEnteredAt`, `administrationEnteredAt`, `bailoutSecondEnteredAt` are recorded tick numbers (not Date timestamps)
- Year-end checks are `currentTick === bailoutEnteredAt + 360`, not `Date.now() - timeOfEntry > 31536000000ms`
- Asset list is sorted deterministically (construction order / building ID, not random)

**Mutation:** introduce `Math.random()` into the asset-sort comparator or trigger the second bailout at `bailoutEnteredAt + Math.random() * 360`. Replay differs from the original run. Test goes red.

**False-pass:** determinism is claimed but untested; no replay scenario is actually executed.

---

## Increments

### Inc1: Debt threshold + cannot-afford feedback + popup

- AC-1: debt threshold triggers bailout state
- AC-8: popup communicates conditions once
- AC-9: cannot-afford feedback on refused actions (place, advisor)
- **Deliverables:** fiscal.ts + engine.ts: `DEBT_THRESHOLD_FOR_BAILOUT`, `insolvencyState` field, transition logic; MapView.tsx: popup + banner; place() feedback path
- **Gate:** `npm test` green (AC-1, AC-8, AC-9 unit/mount tests); manual dogfood: see popup, see error messages

### Inc2: Bailout event, forced sales, asset list

- AC-2: bailout duration exactly 360 ticks
- AC-3: asset list in construction order
- AC-4: forced sale inflow labeled in ledger
- **Deliverables:** engine.ts: bailout tick-counting, year-end re-eval logic; fiscal.ts: `BAILOUT_DURATION_TICKS`, `BAILOUT_INCOME_INJECTION`; MapView.tsx: forced sales panel + asset list; ledger labeling
- **Gate:** `npm test` green (tick-duration test, asset-sort test, ledger-line test); manual dogfood: place a building, go broke, see asset list, sell one, see funds + ledger

### Inc3: Administration mode, spending constraint, 1-year duration

- AC-5: enter admin mode (alt to forced sales)
- AC-6: spending multiplied, paid buildings blocked
- AC-7: admin duration 360 ticks, re-evaluate at year-end
- **Deliverables:** engine.ts: administration state machine, outflow multiplier pass; MapView.tsx: admin banner + re-evaluation message; place() affordability precondition
- **Gate:** `npm test` green (admin-duration test, outflow-multiplier test); manual dogfood: enter admin, place free road (succeeds), attempt paid school (fails), observe reduced outflows in F2

### Inc4: Second bailout on worse terms + decline screen + game-over

- AC-10: second bailout available if year 1 ends insolvent
- AC-11: decline screen shows stats, hard game-over, no third bailout
- **Deliverables:** engine.ts: second-bailout state, end-game trigger; fiscal.ts: `BAILOUT_SECOND_THRESHOLD_WORSE_TERMS`, `BAILOUT_INCOME_INJECTION_SECOND`, `SECOND_BAILOUT_DURATION_TICKS`; MapView.tsx: decline screen + stats rendering
- **Gate:** `npm test` green (second-bailout logic test, decline-screen rendering test); manual dogfood: let admin mode time out, see second bailout, let that time out, see decline screen, verify stats, click "Start Over"

### Inc5: Determinism + replay consistency

- AC-12: all triggers from state, no wall-clock, replays match
- **Deliverables:** refactor any Date.now() or Math.random() out of the insolvency path; test replay from a save
- **Gate:** `npm test` green (replay determinism test); manual dogfood: save at tick X during bailout, reload, tick forward to year-end, state matches original run (same transition ticks)

---

## Out of Scope

- Integration with Go `engine.finance` module (webconsole uses TS `fiscal.ts` / `engine.ts` stubs; Go engine does not run in dogfood)
- Detailed balance-pass tuning of PLACEHOLDER constants (Aaron row-by-row approval pending)
- Saveload state management (assumes saves preserve `insolvencyState` + tick counters; implementation TBD with FEAT-1972079897)
- Visual design of decline screen (UX TBD; acceptance criteria cover data, not layout)
- Overdraft interest capping during bailout/admin (BUG-438 scope; this feature assumes interest is already capped per BUG-438)
- Tutorial / onboarding for bailout rules (educational flow; FEAT-TBD scope)
- Localization (English-only for baseline)

---

## Open Questions for Aaron

1. **Construction order vs. reverse-construction order for forced sales list:** The AC specifies "construction order" (oldest-placed first). Should the player be forced to sell the **newest** buildings first (reverse age), or the oldest? This affects the strategy: newest = lose recent investments; oldest = lose the city's historic character. Which is the intended punishment?

2. **Second bailout: automatic or user-initiated?** AC-10 assumes the second bailout is **user-initiated** (player clicks a button). Alternatively, it could be **automatic** (state transitions without user input at the end of year 1 if insolvent). Which flow do you prefer? (Automatic is faster; user-initiated gives the player a choice to accept decline instead.)

3. **Decline screen final flow:** Is "Start Over" (new game) the player's only option, or does "Load Save" need to be implemented in this increment? If Load is not yet built, should the button be present but disabled, or absent entirely?

4. **Administration spending constraint scope:** AC-6 constrains **outflows** by multiplying them. Does this include:
   - Mandatory decay/upkeep? (Yes, likely; all outflows multiplied)
   - Debt interest / overdraft charges? (TBD; interest might be mandatory even in admin)
   - Citizen basic needs (wages, health, education)? (Yes; all subject to the multiplier)

5. **Validation of '2 then 1' endgame interpretation:** The AC assumes second bailout *then* final decline screen is the intended sequence. Is this correct, or does the endgame differ?

