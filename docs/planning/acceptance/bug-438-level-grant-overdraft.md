# BUG-438: Level-up cash injection goes hugely negative; overdraft 0.4%/tick unbounded compound

**Bug:** `computeLevelRewards` sets `cash = Math.round(funds * LEVEL_REWARD_RATE)`
with no floor. When treasury is already negative the “grant” is a debit, booked as
inflow `Level Rewards`, and `MapView` still says “Cash injection … granted.” The
precondition is BUG-402 overdraft: `Math.round(Math.abs(funds) * 0.004)` every tick
while `funds < 0`, uncapped, so `|funds|` grows as `~1.004^n` until it leaves the
integer domain.

**Mkey:** BUG-438

**Relates:** BUG-396 (open — no insolvency halt; this item depends on it but does
**not** implement game-over), BUG-402 (done — overdraft line exists; the rate/cap
is the bomb), BUG-391 (income shape delayed the first negative crossing). Northstar
waypoint 2 (webconsole dogfood).

**GR#25:** webconsole-internal (`engine.ts` / `fiscal.ts` / `MapView.tsx`). No new
`code.json` edge.

## Evidence (why this is P1)

Aaron dogfood 2026-08-29, Level 15 banner:

```
Level 15 reached
Cash injection -£65,078,139,053,138,090,000,000,000,000,000,000,000,000,000,000 granted.
```

`debug (22).json` (generatedAt 2026-08-29T09:25:05Z, tick 15585,
`v0.3.0-141-gb4b8786-dirty`):

- `sim.funds`: `-1.683920392307245e+28` (IEEE float; past `Number.MAX_SAFE_INTEGER`)
- `sim.notice`: `{ level: 13, cash: -1986434899816271000, unlocked: ["Penthouse Tower", "The Folkestone Eye"] }`
- `lastRewardedLevel`: 13, `xp`: 18135, `population`: 39864, `loanBalance`: 0
- inflows ~£30,523 (Council Tax 23918, Tourism 4784, rest small)
- outflows: every real bucket is thousands (Roads 20153, Wages 19932, Education 12103, …)
  **except** Overdraft Interest `6.708846184491016e+25` — `flowShares` share **1.0**
- F2 screenshot 2026-08-28 212011.png: TREASURY `£-16.8e27`, Net/tick `£-67e24`

Comment at `engine.ts:387-389` claims “50% annual = 0.4%/tick (50% / 125 ticks)”.
`TICKS_PER_YEAR` is 360. `1.004^360 ≈ 4.2×` per year, unbounded.

## Design

- **Grant floor.** `computeLevelRewards` cash per crossed level is
  `Math.max(0, Math.round(funds * LEVEL_REWARD_RATE))`. Never a debit. Positive-funds
  compounding (existing `webbatch.test.mjs` multi-level case) stays. A zero grant
  still marks `lastRewardedLevel` and still shows unlocks — XP/level is not gated on
  treasury.
- **Banner copy.** `notice.cash > 0` → existing “Cash injection {fmtMoney} granted.”
  `notice.cash <= 0` → that sentence is omitted (or a non-grant line with **no**
  negative £ and **no** word “granted”). Unlocks line unchanged.
- **Overdraft SSOT + structural cap.** The `0.004` literal leaves `engine.ts`. Named
  exported `OVERDRAFT_PER_TICK` lives in `fiscal.ts` next to the other placeholder
  coefficients (GR#3; consistency already excludes the line from upkeep reconcile —
  keep that). Charge for a tick:

  `min(round(|funds| * OVERDRAFT_PER_TICK), max(sum of other outflows this tick, 1))`

  Overdraft cannot dwarf the rest of the economy. Empty city (no other outflows)
  still emits the line at £1 so BUG-402’s presence test stays meaningful. Rate value
  itself remains PLACEHOLDER (balance-number regime) — tests are directional against
  the cap, not a restated 0.004.
- **Safe integer fail-closed.** After `funds + income - expense` (and after applying
  any level-reward inflow), if the result is not `Number.isSafeInteger` or not
  finite, do **not** commit it: keep previous `funds`, `recordError` (webconsole
  envelope already used by reset-abort; do not mint a Go `data/errors.json` code
  unless FEAT-1972079916 has landed a registry path this item can reuse). F2 must
  never be asked to render 1e25-scale overdraft as a normal expense.
- **This item does not close BUG-396.** No game-over, no funds floor at £0, no
  placement freeze. Capped overdraft + grant floor stop the *explosion*; insolvency
  as a death condition stays on BUG-396 / `engine.finance` AC-7.

## Acceptance Criteria

- **AC-1 (grant never a debit).** `funds = -1_000_000`, `lastRewardedLevel` behind
  `levelOf(xp)`, `computeLevelRewards` (or `debugXp` that crosses a level, then one
  `tick` to drain) yields `notice.cash === 0` (not `-100_000`). `Level Rewards`
  inflow is absent **or** `value === 0`. Funds after the drain are **not** more
  negative *because of the reward* (overdraft may still apply; subtract the
  overdraft line before asserting). **Mutation:** delete the `Math.max(0, …)` floor;
  this test goes red. **False-pass:** asserting only the banner string.

- **AC-2 (banner copy).** With `notice = { level: 15, cash: -1, unlocked: [] }` (or
  the AC-1 result), `LevelUpBanner` output does not contain `granted` and does not
  contain `fmtMoney` of a negative amount. With `cash > 0`, existing “Cash injection
  … granted.” remains. Check: mount or a pure render helper; both branches.
  **False-pass:** `fmtMoney` of a negative still on screen with the word “injection”
  even if “granted” was removed.

- **AC-3 (overdraft cannot explode).** 1000 `tick`s from `funds = -1_000_000`,
  `loanBalance = 0`, a city whose *other* outflows are the test’s own
  `computeFlows` result (do not stub them to 0 and then claim victory). After 1000
  ticks: `|funds| < Number.MAX_SAFE_INTEGER`, `Number.isSafeInteger(funds)`, and
  every tick’s Overdraft Interest `value <= max(otherOutflowSum, 1)` (read other
  outflows from that tick’s `lastFlows`, GR#15 — do not hardcode £80k).
  **Mutation:** restore uncapped `abs(funds)*0.004`; this test goes red (funds leave
  the safe-integer set or overdraft exceeds other outflows). **False-pass:** a test
  that only checks the label exists (today’s `fiscal.test.mjs` BUG-402 GREEN).

- **AC-4 (dump-scale overdraft is not a displayable F2 number).** A state whose
  uncapped overdraft would be `> 1e12` must, after `computeFlows` / one `tick`,
  either (a) emit Overdraft Interest at the AC-3 cap, or (b) hit the AC-5
  fail-closed path. F2/`lastFlows` must not carry a `6.7e25`-class Overdraft
  Interest as a normal outflow. Check: seed `funds` just below
  `-Number.MAX_SAFE_INTEGER / 4` (or the smallest `|funds|` that makes
  `round(|funds| * OVERDRAFT_PER_TICK) > 1e12` — derive from the exported rate,
  do not restated 0.004); tick once; assert the overdraft line is capped **or**
  funds unchanged + error recorded. **False-pass:** hiding the line in the UI while
  `lastFlows` still holds the raw 1e25.

- **AC-5 (safe-integer fail-closed).** If applying a tick’s net would make `funds`
  not `Number.isSafeInteger` or not finite, the tick does **not** commit that
  value: previous `funds` retained, `recordError` called. Check: construct a net
  that would overflow (cap-disabled test double **or** a one-tick outflow larger
  than `Number.MAX_SAFE_INTEGER - funds`); assert funds identity to pre-tick and
  an error in `recentErrors()` / the existing recordError sink. **False-pass:**
  `Math.round` of Infinity displayed as £0 via `fmtMoney`’s non-finite guard
  while `state.funds` is still non-finite.

- **AC-6 (positive-funds regression).** Existing `webbatch.test.mjs` level-reward
  cases stay green: solvent `initialState()` crossing one level still books
  `Math.round(funds * LEVEL_REWARD_RATE)` (read `LEVEL_REWARD_RATE`, don’t
  restate 0.1); crossing three levels still compounds on the **positive** running
  balance. Overdraft line is absent while `funds >= 0`. Check: `cd webconsole &&
  npm test` includes these; deleting the positive-path `round(funds * rate)` turns
  them red.

- **AC-7 (SSOT + gate).** `0.004` is not a second literal in `engine.ts`. Tests
  live in `webconsole/test/*.test.mjs` (extend `fiscal.test.mjs` / `webbatch.test.mjs`)
  and/or `webconsole/test/mount.test.tsx` for AC-2. **`rebuild-prompt.test.tsx` is
  not in the gate.** Check: `cd webconsole && npm test` green; `npx tsc --noEmit`
  green. **False-pass:** a new `.test.tsx` that `package.json` `"test"` does not run.

## Out of scope

- BUG-396 insolvency game-over / funds floor at £0 / placement freeze.
- Recalibrating the “50% annual” story to 360-tick years (balance pass; Aaron).
- Repairing Aaron’s already-exploded save (no auto-rewrite of dump (22)).
- Go `engine.finance` / int64 micro-pounds / `data/errors.json` (unless 9916’s
  registry path is already on trunk and this item reuses it).
- Changing `LEVEL_REWARD_RATE` itself (still 0.1 placeholder).
- F2 layout / histogram scale (capped numbers make them readable; no new chart).
