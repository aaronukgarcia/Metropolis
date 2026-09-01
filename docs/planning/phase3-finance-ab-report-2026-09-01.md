# FEAT-1972079936 Phase-3 inc2 — Finance A/B Divergence Report

> **Revision note (2026-09-01, post independent round r1):** the round REJECTed this
> report's first draft for seven findings (F1-F7) against the report text only — the
> harness/bridge/test machinery itself was accepted as-is. This revision corrects: the
> §2 table's columns, which were labelled backwards (F1); §3.3's tile-purchase root
> cause, which was empirically false (F2, see the permanent regression
> `internal/converge/attack_phase3_inc2_orientation_test.go`); §1's claim about the
> fixture's own self-check, which described a check that silently overwrote a
> tampered file rather than failing on it (F3, fixed in
> `webconsole/test/converge-fixture-emit.mjs`); the missing explanation for the
> tick-19/tick-82 spikes in §2 (F4, new §3.6 below); hand-typed cost figures in this
> report and in `converge-finance-actions.json`'s own notes (F5); three `engine.ts`
> line-pointer citations (F6); and a double-wrapped correlation ID in a bridge
> rejection message (F7, fixed in `finance_ab_actions.go`, regression
> `internal/converge/attack_phase3_inc2_correlation_test.go`).
>
> **Status:** REPORT-ONLY (Aaron's decided convergence strategy, `docs/planning/phase3-convergence-plan.md`
> §2/§6 "inc3.2 — first real domain (finance), PENDING P1"). This increment does **not** require
> the finance domain to pass parity — the Go finance hook (`internal/engine/compose/compose.go`'s
> `financeHook`) is still the documented baseline-one stub (P1, FEAT-083, not yet landed). The
> point of this report is to **measure** the divergence honestly, on a real fixture, through the
> same harness (`internal/converge`) that will gate the eventual flip once P1 lands.
>
> **The gate that proves this is not rigged:** `internal/converge/finance_ab_test.go`'s
> `TestFinanceAB_KnownDivergence_NonEmpty` runs `Compare()` for real and asserts the resulting
> `Report` is **non-empty** — i.e. it is a documented FAIL, not a hidden or vacuous pass. Its
> sibling `TestFinanceAB_KnownDivergence_GreenIfFixturesMatch` proves the same assertion mechanism
> CAN go green (Go trajectory compared against itself), so the "always red" behaviour above is a
> real signal, not a broken check.

## 1. Fixture provenance (GR#15 — no hand-typed numbers)

| | |
|---|---|
| Base commit | `a990ec927d301b88cf57a646b27eae3163262c98` (2026-09-01 16:28:31+01:00) — `origin/main` was one docs-only commit (`fa27c36`) ahead at dispatch time, not relevant to `internal/engine`, `internal/converge`, or `webconsole/src/sim` |
| RNG seed (Go side) | `core.WithWorldSeed(1972079936)` — `internal/converge/finance_ab_actions.go`'s `RunFinanceActionsComposed`; irrelevant to the sampled field since finance is RNG-free on both sides (plan §1c: "Both finance paths are RNG-free and deterministic") |
| Journal (shared, canonical) | `webconsole/test/converge-fixtures/converge-finance-actions.json` — 8 ops, hand-authored ONCE as the canonical action list (not a captured trajectory — the trajectories themselves are derived, never hand-typed). NOTE: originally placed at `webconsole/test/fixtures/...` but relocated during this increment's gate run — `webconsole/test/serve-bundle.test.mjs` owns the entire `test/fixtures/` directory as its own `rmSync`-based scratch space and wiped this fixture out from under the harness mid-session (§5.1 below) |
| Months / logical ticks | 3 checkpoints: tick 30 (Month 1, zero gameplay), tick 60 (Month 2, 3 zone placements applied), tick 90 (Month 3, spans 2 TS-only utility-placement attempts) |
| Command counts (Go side) | 3× `KindAdvanceTicks` (n=30 each) + 1× `KindBuy` + 3× `KindZone` = 7 real `protocol.Command`s issued through `core.Engine.HandleCommand`; 2 ops (`place_utility_ts_only` ×2) have no Go `protocol.Kind` and are recorded as `SkippedOp`, never silently dropped (`TestFinanceAB_SkippedOps_DocumentedAndOnlyUtilityPlacements`) |
| TS action counts | 3× `{type:'tick'}` batches (30 ticks each, 90 reducer calls total) + 3× `{type:'place', ...}` (2 residential + 1 commercial land) + 2× `{type:'place', ...}` utility attempts (both expected-rejected by the TS affordability gate — see §3) |
| Fixture emission | `webconsole/test/converge-fixture-emit.mjs`'s `emitFixture()`, a pure function of `converge-finance-actions.json` + the real `webconsole/src/sim/engine.ts` `initialState`/`reducer`/`computeFlows` — no hand-typed sample values. The committed fixture Go reads, `internal/converge/testdata/finance-webconsole-v1.json`, is checked (never rewritten) by the SAME file's `node --test` block: **F3 correction (2026-09-01, independent round r1) —** the previous revision of this row claimed a stale/tampered on-disk fixture "would fail `writeFixtureFile()`'s own round-trip assertion". That was false, and proven false: the round corrupted a value in the committed fixture, ran `node --test`, and the old assertion (write the fixture, then read back what was JUST written — an `x==x` self-check with nothing independent on either side) silently overwrote the corruption back to a correct value and reported zero failures. The check is now a real diff — it reads the file ALREADY on disk and compares it against a fresh `emitFixture()` run, failing loudly on any mismatch instead of repairing it — so a stale/tampered committed fixture now genuinely fails `node --test`. Regenerating the fixture on purpose is a manual step (`node webconsole/test/converge-fixture-emit.mjs --write`, committed like any other change) |
| Go reference trajectory | `internal/converge/finance_ab_actions.go`'s `RunFinanceActionsComposed`, driving a freshly `compose.Wire`'d `*core.Engine` via real `protocol.Command`s through `HandleCommand` — the same composition root `cmd/metropolis` uses (GR#20) |

## 2. Per-month ledger diff table

All values in milli-pounds (`finance.MicropoundsPerPound = 1000`, `internal/engine/finance/money.go`,
BUG-452 rebase 2026-09-01). Source: `go test ./internal/converge/... -race -count=2 -v` output,
`TestFinanceAB_KnownDivergence_NonEmpty` (`internal/converge/finance_ab_test.go:255-258`), reproduced
verbatim below — not hand-recomputed.

**F1 correction (2026-09-01, independent round r1):** the previous revision of this table
labelled its two columns backwards. `finance_ab_test.go` calls `Compare("finance", goTraj,
tsTraj, financeABContract)`, and `compare.go`'s signature is `Compare(domain string, ref,
candidate Trajectory, contract Contract)` — so `ref` is ALWAYS the Go composed-engine
trajectory and `got`/candidate is ALWAYS the TS webconsole trajectory. The numbers below are
unchanged from the original run (they were never wrong), only the column headers and the
surrounding prose direction are corrected. This orientation is now pinned mechanically by
`internal/converge/attack_phase3_inc2_orientation_test.go`'s
`TestAttack_Phase3Inc2_CompareOrientation_RefIsGo`.

| Tick | Go reference (`ref`) | TS candidate (`got`) | Delta (`got - ref`) | Tier | Result |
|---:|---:|---:|---:|---|---|
| 30 | 1,500,071,750 | 1,647,027,000 | **+146,955,250** | exact | mismatch |
| 60 | 1,500,143,500 | 965,269,000 | **-534,874,500** | exact | mismatch |
| 90 | 1,500,215,250 | 1,034,725,000 | **-465,490,250** | exact | mismatch |

Both trajectories start from the same nominal opening treasury (`STARTING_TREASURY = £1,500,000`,
`webconsole/src/sim/fiscal.ts:17`, vs. `initialTreasury = 1_500_000_000` milli-pounds,
`internal/engine/compose/compose.go:55` — same £1.5M under the shared `MicropoundsPerPound` scale)
and diverge immediately: the **Go side barely moves** (its stub `financeHook` posts a fixed
monthly wage/tax pair designed to close net-zero, plus small consumption/tax legs —
`compose.go:69-70`), while the **TS side swings by hundreds of millions of milli-pounds**
(tens/hundreds of thousands of pounds) per checkpoint, driven by its ~15 bespoke per-tick
revenue/expense lines (§3.1) plus two one-off spikes explained in §3.6 below.
`TestFinanceAB_SignConvention_Holds` and `TestFinanceAB_GoTreasuryBounded_ZeroActivityMonth`
confirm neither side has overflowed or gone negative — this is genuine model divergence, not
an arithmetic bug in either engine or in the bridge.

## 3. Top divergence causes, with file:line pointers on both sides

### 3.1 Different revenue/expense models entirely (root cause, plan §1b)

- **TS** (`webconsole/src/sim/engine.ts:459` `computeFlows`): ~15 bespoke per-**tick** line items —
  Council Tax (`engine.ts:463`), Business Tax (`engine.ts:464`), Freight Tax (`engine.ts:465`
  — **F6 correction, 2026-09-01: not Regional Grant**), Regional Grant (`engine.ts:468`),
  Commuter Revenue (`engine.ts:484`), Office Tax (`engine.ts:492`), Tourism (`engine.ts:504`),
  Grid Export (`engine.ts:512`), per-building upkeep buckets (`engine.ts:517-524`), Wages
  (`engine.ts:528` — **F6 correction: not 527**), Transit Subsidy (`engine.ts:563`), Loan
  Interest (`engine.ts:566`), Refuse Collection (`engine.ts:577`), Recycling/Compost/Waste
  Disposal (`engine.ts:588-592`), Overdraft Interest (`engine.ts:597` — **F6 correction: not
  596**) — all keyed off live zone/building counts and policy flags. (Line numbers verified
  against the actual file at revision time; F6 flagged the previous draft's citations as
  unverified copy/paste that had drifted from the real file.)
- **Go** (`internal/engine/compose/compose.go:1461-1560` `financeHook.ApplyEffect`): a fixed,
  budget-closing wage/tax pair per **month** (`monthlyWages = 150_000_000`,
  `monthlyTax = 150_000_000`, `compose.go:69-70`) plus `postConsumptionAndTax()`
  (`moneycirc.go`, FEAT-1972079927 Q4) — no representation at all for freight, office, tourism,
  grid-export, recycling, or per-zone upkeep. This is exactly the gap `phase3-convergence-plan.md`
  §1b calls out: *"Go has no representation for freight/office/commuter/tourism/grid-export/
  recycling revenue, per-zone upkeep buckets, or the policy multipliers."*

### 3.2 Cadence mismatch: per-tick (TS) vs per-month (Go)

- TS's `computeFlows` runs (conceptually) **every tick** — 90 evaluations across the fixture's 3
  checkpoints, most of them near-zero net because the fixture's zoned city is tiny.
- Go's `financeHook` posts **once per month** (`core.PhaseFinance` is the last monthly phase,
  `compose.go`'s own comment at line ~1480), so its ledger jumps discretely at tick boundaries
  that don't line up 1:1 with any TS per-tick accrual. Plan §1c: *"Cadence differs 30× (tick vs
  month)."*

### 3.3 Buy-before-Zone: measured to be a NON-contributor (F2 correction, 2026-09-01)

**The previous revision of this section claimed the fixture's `zone` ops cost the Go side a
real `KindBuy` (tile purchase) spend with no TS counterpart. That claim is empirically false,
and was proven false by the independent round: `compose.go`'s `KindBuy` handling
(`handleGameplay`, `case protocol.KindBuy`) calls `world.PurchaseTile`
(`internal/engine/world/worldapi.go`), which allocates the tile's simulation storage and
records ownership — it never touches `simState.treasury` or calls into `engine.finance` at
all. The round deleted the bridge's entire `"zone"` case (`finance_ab_actions.go`) — no
`KindBuy`, no `KindZone` ever issued — and the resulting Go trajectory was byte-identical to
the one with gameplay included.**

This is now a permanent, mechanical regression:
`internal/converge/attack_phase3_inc2_orientation_test.go`'s
`TestAttack_Phase3Inc2_GameplayIsInert_ForTreasury` replays ONLY the fixture's `advance`
segments (no `zone`/`KindBuy`/`KindZone` at all) through an independently-constructed engine
and asserts the resulting treasury trajectory equals the full journal's trajectory at every
sampled tick — i.e. it measures, rather than assumes, that the 3 zone placements in this
fixture move the sampled Go treasury by **exactly £0** at ticks 30/60/90. The Go side's entire
divergence contribution in the table above comes from `financeHook`'s fixed monthly wage/tax
pair (§3.1) and the consumption/tax legs (FEAT-1972079927 Q4) — **not** from any tile-purchase
or zoning cost, because baseline-one's Go economy does not yet charge for either.

The correct, evidence-based statement of what each side prices: the TS side has real
zone-adjacent costs today (road auto-connect spend when a placed building is wired to the
network — see §3.6/F5 below), while the Go side has none. When either side's tile-purchase or
zoning economy becomes real, `TestAttack_Phase3Inc2_GameplayIsInert_ForTreasury` goes RED by
design, and this section must be re-derived from the new numbers rather than restated from
plausibility (see that test's own failure message).

### 3.4 The two TS-only utility placements are (correctly) unaffordable in TS, and unrepresentable in Go

- `converge-finance-actions.json`'s `place_utility_ts_only` ops (Wind Turbine £4,800,000, Water
  Works £4,680,000) both exceed `STARTING_TREASURY` (£1,500,000) even before the Month-1 zoning
  spend. The TS reducer's `place` case affordability gate (`engine.ts`, `cost>0 && funds<cost`)
  is expected to silently reject both — an honest, load-bearing observation the fixture's own
  note captures: *"a fresh baseline-one city cannot literally afford a power plant on day one
  under the current balance numbers."* On the Go side there is no `protocol.Kind` for a
  standalone utility-building placement at all (`internal/protocol/commands.go` — baseline-one's
  catalogue exposes only the eight `ZoneType` land-use zones), so both ops are recorded as
  `SkippedOp` (`finance_ab_actions.go`'s `place_utility_ts_only` case) rather than translated.
  **Net effect on this report: this cause is currently a wash** (rejected on one side, untranslatable
  on the other) but it masks a real coverage gap that would matter the moment either side's
  utility economy becomes real.

### 3.5 Money-scale convention agrees; rounding does not compound meaningfully here

- Both sides use the SAME milli-pound scale (`MicropoundsPerPound = 1000`) and the same
  truncate-toward-zero rounding rule (`money.go`'s `mulDiv` doc comment; `converge-fixture-emit.mjs`'s
  `toMilliPounds` mirrors it explicitly, `Math.trunc`). This was BUG-355's original scale-mismatch
  concern and it is **not** a contributor to the deltas above — the divergence is 100% model
  disagreement (§3.1-§3.4), not a units bug.

### 3.6 The tick-19 and tick-82 one-tick spikes (F4, new section, 2026-09-01)

The headline tick-30 delta (+146,955,250 milli-pounds) is dominated by a single one-tick
credit, not by the ~15-line-items-at-30x-cadence framing of §3.1/§3.2 (which, on its own,
cannot produce a one-tick discontinuity — per-tick lines move by small, roughly-flat amounts
tick over tick, as §3.1's own trace shows). Re-running the TS reducer over this fixture's
exact action list and diffing `state.funds` tick-over-tick (not hand-typed — a fresh trace)
finds exactly two outlier ticks:

| Logical tick | Funds before -> after | Delta |
|---:|---:|---:|
| 19 | 1,498,283 -> 1,648,021 | **+149,738** |
| 82 | 947,559 -> 1,041,472 | **+93,913** |

Neither tick's `computeFlows()` breakdown (Council/Business/Freight Tax, Regional Grant,
Roads/Power Grid/Transport/Wages upkeep — all single-digit-to-low-hundred-pound magnitudes at
this fixture's tiny city size) accounts for a swing of this size. The actual source is
`advance()`'s **Level Reward** mechanism (`webconsole/src/sim/engine.ts`): every tick adds
`+1` XP (`engine.ts:1214`, `const newXp = s.xp + 1`), and `computeLevelRewards()`
(`engine.ts:632-648`) pays out `Math.round(funds * LEVEL_REWARD_RATE)` — 10% of the
**pre-tick** treasury (`LEVEL_REWARD_RATE = 0.1`, `engine.ts:89`) — as a one-off "Level
Rewards" inflow (`engine.ts:1211-1222`) the FIRST tick a level threshold is crossed. Tick 19
and tick 82 are exactly the two ticks, under this fixture's XP trajectory (starting XP plus
+4 XP per zone placement, `engine.ts:2588`/`2863`), where cumulative XP crosses the next
level's threshold. £149,738 is within rounding of 10% of the pre-tick treasury at tick 19
(£1,498,283 x 0.1 = £149,828), and £93,913 is within rounding of 10% of the pre-tick treasury
at tick 82 (£947,559 x 0.1 = £94,756) — the small residual gaps are the accumulated per-tick
flows between the snapshot instants, not a separate cause. **This is a TS-only mechanic with
no Go counterpart** (baseline-one's Go engine has no XP/level system at all), so it is itself
a genuine, evidence-backed divergence cause, on top of §3.1/§3.2 — not something inc3
de-stubbing work needs to close on the Go side, but something the AB report needed to name
rather than silently fold into the per-tick-line framing.

## 4. What inc3 (real de-stubbing, per `phase3-convergence-plan.md` §3 P1 + §6 "inc3.2") would need to close each cause

| Cause | What closes it |
|---|---|
| §3.1 model gap | De-stub `financeHook` (or its successor) to implement Go-side equivalents of freight/office/tourism/grid-export/recycling revenue and per-zone upkeep — this is FEAT-083 baseline-one de-stubbing work, upstream of this harness (plan §3 "P1 — The Go engine domains must become authoritative") |
| §3.2 cadence | Either move Go's finance posting to per-tick granularity, or redefine the parity contract's sample points to per-month boundaries only and accept per-tick TS noise averaged into each Go month (a Tier-B "bounded" contract per plan §2, not Tier-A exact) |
| §3.3 Buy-before-Zone cost | **F2 correction:** measured to be a non-contributor today (§3.3) — `TestAttack_Phase3Inc2_GameplayIsInert_ForTreasury` is the tripwire. Once either side's tile-purchase/zoning economy becomes real, give the OTHER side an equivalent cost model, or exclude the priced side's cost from the comparable "treasury" field |
| §3.4 utility placements | Register a `protocol.Kind` for standalone utility-building placement once baseline-one's catalogue grows one (tracked as a coverage gap, not urgent — see `finance_ab_actions.go`'s `SkippedOp` reasoning) |
| §3.5 scale/rounding | No action needed — already agreed and verified non-contributory |
| §3.6 TS Level Rewards spikes | Either give the Go side an equivalent XP/level cash-reward mechanic, or exclude the TS-only "Level Rewards" inflow from the comparable "treasury" field so the two sides price the same thing (would require the fixture emitter to expose a rewards-excluded TS figure — out of this harness's current single-field `Contract`, same shape as the §3.3 row above) |
| Contract itself | Once §3.1/§3.2 close, retune `financeABContract` (`finance_ab_test.go`) from `Tier: TierExact` on `treasury` alone to whatever tier the reconciled model actually supports (plan §2's three-tier ladder), and only THEN is a passing `TestFinanceAB_KnownDivergence_NonEmpty`-successor expected — at which point it should be renamed/restructured, since its CURRENT name and doc comment explicitly predict this test going red the day the stub is de-stubbed (see its own doc comment, `finance_ab_test.go`) |

## 5. Operational gotcha found while running the gates: `test/fixtures/` namespace collision

Mid-session, `webconsole/test/fixtures/converge-finance-actions.json` was silently deleted with no
code change in between two gate runs. Root cause: `webconsole/test/serve-bundle.test.mjs`
(FEAT-1972079924, unrelated to this increment) treats the entire `test/fixtures/` directory as ITS
OWN scratch space — `cleanupFixtures()` calls `rmSync(resolve(__dirname, 'fixtures'), {recursive:
true, force: true})` unconditionally, and `node --test`'s default glob runs every `test/*.mjs` file
in the same process tree with no ordering guarantee against this increment's fixture. **Fix applied:**
this increment's fixture and its consumers were relocated to a dedicated `webconsole/test/converge-fixtures/`
directory that `serve-bundle.test.mjs` does not touch — `webconsole/test/converge-fixture-emit.mjs`'s
`ACTIONS_PATH` and `internal/converge/finance_ab_test.go`'s `actionsListPath` both updated accordingly,
each with a doc comment recording why. This is recorded here as a real, load-bearing finding: any
future fixture placed directly under `test/fixtures/` will collide with `serve-bundle.test.mjs`'s
lifecycle the same way.

## 6. Harness plumbing proven by this increment (not gated on the above)

Independent of whether the two models agree, this increment proves the **machinery** end-to-end
(`internal/converge/finance_ab_test.go`, all PASS under `-race -count=2`):

- `TestFinanceAB_TickAlignment_Holds` — both trajectories report samples at the fixture's own
  canonical logical ticks (30/60/90), never either engine's raw internal tick.
- `TestFinanceAB_SignConvention_Holds` — no int64 overflow/saturation on either side.
- `TestFinanceAB_GoTreasuryBounded_ZeroActivityMonth` — the Go stub's zero-gameplay-month behaviour
  stays within its documented net-zero-plus-consumption-legs bound.
- `TestFinanceAB_ComposedRun_Deterministic` — GR#21: 5 repeat runs of `RunFinanceActionsComposed`
  produce a `reflect.DeepEqual` trajectory.
- `TestFinanceAB_SkippedOps_DocumentedAndOnlyUtilityPlacements` — exactly the 2 expected ops are
  skipped, each with a non-empty `Reason`.
- `TestFinanceAB_KnownDivergence_NonEmpty` / `TestFinanceAB_KnownDivergence_GreenIfFixturesMatch` —
  the honesty pairing described at the top of this report.

Independent round r1's own permanent regressions (`internal/converge/attack_phase3_inc2_orientation_test.go`,
`internal/converge/attack_phase3_inc2_correlation_test.go`), added by this revision:

- `TestAttack_Phase3Inc2_CompareOrientation_RefIsGo` — pins F1's fix: `Compare`'s `ref` is
  always the Go trajectory and `got` is always the TS trajectory, so the report's table
  orientation cannot silently invert again.
- `TestAttack_Phase3Inc2_GameplayIsInert_ForTreasury` — pins F2's fix as a measured tripwire:
  the 3 zone placements in this fixture move the sampled Go treasury by exactly £0; the test
  goes RED (by design) the day Go's build/world seam starts charging for a tile or a zone,
  forcing §3.3 to be re-derived from real numbers.
- `TestAttack_Phase3Inc2_RejectedCommandMessage_NoDoubleWrap` — pins F7's fix: a rejected
  bridge command's error message carries exactly one correlation-ID occurrence and never
  repeats an inner registry code back-to-back.

And in `webconsole/test/converge-fixture-emit.mjs` (F3's fix): "the committed testdata fixture
matches a fresh regeneration (no silent drift)" replaces the old write-then-read-back
self-check with a real diff against the committed file, proven RED->GREEN by corrupting a
value in the committed fixture (test fails with a clear diff message) and then restoring it
(test passes again) — see this revision's dispatch notes for the reproduction transcript.

## 7. Not compared (explicitly out of scope this increment)

`compose.Composition`'s public API exposes exactly one money-stock accessor, `Treasury()`
(`compose.go`) — no `Reserves()`/`Debt()`/`NetWorth()` accessor was added to reach this increment's
`financeABContract`, so those fields are not part of this report (`finance_ab_test.go`'s field-mapping
doc comment). The TS-only `income`/`expenses`/`net` fields in `testdata/finance-webconsole-v1.json`
are descriptive-only (`Compare()` only checks fields the Go reference trajectory reports —
`compare.go`'s `refReportedFields`), not fed through parity.
