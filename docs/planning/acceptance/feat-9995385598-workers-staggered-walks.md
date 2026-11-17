# FEAT-9995385598 - Configurable shard workers + staggered per-shard monthly money walks

mkey: FEAT-9995385598. Sources: a2.md section 1.2 (Option B recommended), A2Bev001 v23 Q100155 (APPROVED), BOW item. Status: reviewed 2026-11-17, NOT READY (see Independent review at the end); Aaron's OQ-1 and the lead's OQ-2..OQ-4 rulings applied in place. Placeholder error codes use `E-<NAME>`; real codes are claimed via `tools/plan/add-error.js` at build.

## Scope and glossary

- **Staggered ring**: a pass that runs per shard on that shard's scheduled day (`ColdPassSchedule(day)`: shard `i` runs on day `i*30/256`), fanned out over workers by `runShardsParallel`, reduced in ascending shard order.
- **Barrier pass**: a pass that needs cross-shard reads and runs once at month end (day 29 / `DaysPerMonth-1`), single-threaded or a deterministic parallel read followed by an ordered apply.
- **Shard-day**: the day-tick on which a citizen's shard is scheduled.
- **Month-start snapshot**: a read-only view of Employment, Sector and Wealth as at the first day-tick of the month, which barrier passes read instead of live state (ASM-1580, ASM-1582). *Review note:* no barrier pass on main reads any of these three fields (ASM-1626, see AC-17/AC-18), so the snapshot is built only for fields a reader actually needs.
- **Day 30 / month close**: the PhaseFinance month-close tick of a month, strictly after the day-29 (`DaysPerMonth-1`) shard-day pass of that month has run, so all 256 shards have contributed before the post. "Day 29" in this doc means the last cold-pass day-tick and "day 30" means the month close.
- **Pending credit** (Aaron OQ-1): a per-citizen wage credit recorded on the shard-day but not yet added to Wealth; it is committed at the month close only if that month's firms wage post succeeds, otherwise dropped.

Passes classified (ASM-1580, ASM-1579):

| Pass | Reads | Class |
|---|---|---|
| `markEmploymentAndCount` | citizen's own id, age, Employment | staggered (shard-local) |
| `distributeWagesToResidents` | citizen's own Employment, Wealth | staggered (shard-local) |
| `formResidentHouseholds` | Household==0 across all shards (reads no Employment/Sector/Wealth), pairs by ascending id | barrier, reads live Household (no staggered pass mutates it) |
| fertility partner lookup | partner BirthMonth, household membership, partner liveness | stays daily and sequential after the parallel join (fertility.go, as today); partner read per lead OQ-4 ruling, see AC-16 |
| `DeathQueue.RealiseDrained` | whole queue | barrier (unchanged) |
| `postConsumptionAndTax`, `PostWages*`, tax legs | aggregates | day 30, single ledger post |

## A. Worker count configuration

**AC-1.** (lead OQ-2: metroserve only; library, `cmd/metropolis` and the engine pool keep their current defaults) Given `metroserve` started with no worker option, when it boots, then the citizen worker count is `min(GOMAXPROCS, 8)` and the boot log prints the resolved value.

**AC-2.** Given `-citizen-workers 1`, `-citizen-workers 8` or env `METRO_CITIZEN_WORKERS=4`, when it boots, then that value is used; when both flag and env are set the flag wins (ASM-1581).

**AC-3.** Given `-citizen-workers 0`, a negative value, a non-integer, or a value above 256, when it boots, then it exits non-zero with `E-WORKERS-INVALID` naming the value; it is never silently clamped.

**AC-4.** Given a `CitizensAPI` built without the option, when `runShardsParallel` runs, then workers is 1 (library default unchanged; every existing test and golden is byte-stable) (ASM-1581).

**AC-5.** The worker count is a construction/wire-time option only. If the implementation adds any setter, a call after the first tick returns `E-WORKERS-LATE` and a test proves it; if there is no setter, the AC is met by the option's type and `E-WORKERS-LATE` is not registered (ASM-1576).

**AC-6.** Given any worker count N, when `-citizen-paging` is on, then at most N shards are pinned at once by the pool and the residency budget doc (`registry.go` ~725) is updated to state the bound `budget + N`; a test with budget 2 and N 8 proves no pinned shard is ever evicted (existing BUG-664 acquire/release discipline) and no deadlock occurs.

## B. Determinism

**AC-7.** Given the same seed and inputs, when the composed engine runs for at least 14 months at worker counts 1, 2, 7 and 14, then `Composition.StateDigest` and `PopulationHash` are byte-identical across all runs.

**AC-8.** Given the gate, when it runs, then the worker count it varies is the CITIZEN worker count (the new option), not only `core.WithPoolSize`. Today `workers` has no setter and `detgate` varies only the engine pool, so the existing 1-vs-14 gate never exercises the citizen pool (ASM-1576). *Review amendment:* the draft's "distinct concurrent shard workers greater than 1" is scheduler-dependent (`runShardsParallel` work-steals from a channel, so one fast goroutine may take every shard on a one-CPU runner) and would flake. The test instead asserts, through a test seam at the call site, that `runShardsParallel` received `workers == N` for N in {2, 7, 14}; real concurrency is proven by AC-12's `-race` run.

**AC-9.** Given `PopulationHash`, when per-citizen Wealth, EmploymentState, Sector or Workplace differs for any one citizen, then the digest differs. This already holds (`registry.go` ~1703, ASM-1577); the AC is a mutation test per field: perturb one citizen's field in a copy and require the digest to change.

**AC-10.** Given a deliberately wrong, ORDER-DEPENDENT reduction, when the 1-vs-14 gate runs, then it goes red. *Review amendment:* summing integer per-shard totals in completion order is commutative and cannot change any digest, so it is not a valid negative control. The injected fault must change state when order changes, and must do so deterministically, not by scheduler luck: a test-only seam that, whenever workers > 1, applies the per-shard results (or the pending-credit commits) in reverse shard order through an order-sensitive step (for example the next id allocation or an ascending pairing). The negative test asserts the gate reports a mismatch. The gate must run with a population large enough that every one of the 256 shards holds citizens and at least one month with hires and fires on distinct shards.

**AC-11.** Given the staggered ring, then per-shard results are written only into `results[shard]` (or the shard's own accumulator slot) and reduced in ascending shard order after the join; no shared counter is mutated by workers, and a test or astgate rule finds no `go` statement in the staggered pass code (internal/engine/compose's money-pass files and the new citizens seam) outside `runShardsParallel`.

**AC-12.** Given `-race`, when the determinism test suite runs at N=14, then no data race is reported (max two Go `-race` suites at once, 30-minute watchdog applies).

## C. Staggered passes and barrier passes

**AC-13.** REJECTED as written. Given a citizen on shard S, when day-tick `d = S*30/256` runs, then employment marking and wage crediting for that citizen happen on that day-tick, and not at month end. The words "inside the shard's worker" assume a seam that does not exist: both passes mutate through `CitizensAPI.ApplyLifeEventCommand`, which takes `c.mu` (registry.go ~1241), while `AdvanceDayTick` holds `c.mu` across `runShardsParallel`. Calling it from a worker deadlocks, and compose has no other mutation route (SEC-020 copyguard) (ASM-1623). **The BA/lead must supply** the seam as an ICD before dispatch. Option (a): a new CitizensAPI per-shard mutation surface, for example `ApplyInShards(shards, fn)`, where fn gets a shard-scoped registered mutator, runs under the existing lock and work-stealing discipline, and has its error-abort semantics stated. Option (b): compose runs its staggered pass serially over `ColdPassSchedule(day)` after `AdvanceDayTick` returns, which flattens the month-end stall by about 30x with no parallelism; workers then speed only the cold pass. The ICD must also say which compose daily hook runs the pass and its order relative to `AdvanceDayTick` (AC-20 needs it after the day's cold pass).

**AC-14.** Given the month-end tick (day 29), then `markEmploymentAndCount` and `distributeWagesToResidents` perform no citizen walk; they only read the per-shard accumulators. A probe counter (as in the BUG-665 gate) asserts zero full-population gathers by these two passes at month end.

**AC-15.** Given `formResidentHouseholds` at month end, then it stays a month-end barrier walk that reads live Household (no staggered pass mutates Household), pairs unpaired citizens in ascending id order exactly as today, and its result is byte-identical to the pre-feature run on the same seed and at every worker count. *Review amendment:* the draft's snapshot read was for predicates the pass does not have (it reads Household==0 only).

**AC-16.** (lead OQ-4: fertility stays daily, partner read on the month-start snapshot) Fertility stays daily, sequential, after the parallel join, as `applyFertilityLocked` already is. Its partner read takes any field that a staggered pass mutates from the month-start snapshot, never from live state. *Review finding, for the lead:* on main the partner read uses BirthMonth (immutable), live household membership and partner resolvability, and runs single-goroutine after the join, so there is no race to fix and no staggered-mutated field is read today (ASM-1626). Moving BirthMonth, Household or liveness onto a month-start snapshot would change behaviour: a partner who joined mid-month would be unresolvable. So the ruling is met by AC-18's lint: fertility reads no staggered-mutated field live, plus a test that fertility births are byte-identical to the pre-feature run on the same seed at N=1 and N=14. The draft's "mutates the partner shard concurrently" test is withdrawn because no concurrency exists at that point.

**AC-17.** Given the month-start snapshot, then it is captured exactly once per month before the first staggered day runs and holds only the fields that staggered passes mutate and some reader in AC-18's inventory must see at month-start state. If that set is empty (as on main, ASM-1626), no snapshot is built and the BOW item records why. If it is non-empty, its extra memory at 1M and 10M is recorded and stays within 10 percent of resident citizen memory (ASM-1582).

**AC-18.** Given the staggered passes mutate Employment, Sector and Wealth (and the pending-credit column), then an inventory of every reader of those fields outside the staggered pass is written into the BOW item and enforced by an AST test over internal/engine. A reader is a selector or accessor on the Citizen record or on the shard columns. On main the readers include attract/migration.go, firms/founding.go, households, compose/citizen_query.go and PopulationHash. Each reader is classified: month-close (sees complete state, nothing needed), mid-month (sees mixed shard-day state; must read the snapshot or be listed as accepting mixed state with a one-line reason), or digest. A new unclassified reader fails the test. The classification is data, not a hand list in two places (GGR3/SSOT). *Review amendment:* the draft covered barrier passes only and missed the mid-month readers.

**AC-19.** Given `DeathQueue.RealiseDrained`, then it still runs once at day 29 and its output is unchanged by worker count. A citizen who dies at month end is still counted in that month's shard-day employed accumulators if their shard ran earlier (the ledger bill counts them); their pending credit follows AC-22.

## D. Payroll rule (Q100155), stated precisely

**AC-20.** Given a citizen whose shard-day is D, when the shard runs on day D, then the citizen earns the month's wage (as a pending credit, AC-27) if and only if their Employment.State is Employed after that shard's cold-pass job matching AND the compose employment marking on day D, in that order (ASM-1627; a2.md 1.2 says "state at the start of the shard-day", Q100155 says "as of the shard's scheduled day", and this AC deliberately takes the after-marking reading so a newly decided working-age resident is paid that month; lead to confirm). A citizen Employed on D is credited the full monthly net wage; one not Employed on D is credited nothing for the month.

**AC-21.** Given a citizen hired on day 20 whose shard-day is 5, when the month closes, then they receive no wage that month and are paid from the following month's shard-day if still employed. This is accepted behaviour under Q100155 and the lead's OQ-3 ruling (unpaid that month, no proration), asserted by a named test (`TestPayroll_HireAfterShardDayUnpaid`). Hires after the shard-day reach a citizen only through paths other than the shard's own cold pass, for example a direct `LifeEventEmployment` command; the test injects one that way. Symmetrically, a citizen fired on day 20 whose shard-day was 5 IS paid for the month (ASM-1583).

**AC-22.** Given a citizen who dies, emigrates or is removed after their shard-day but before the month-close commit, then the employed accumulators are not rewound (the ledger bill counts them, per Q100155's per-shard totals), and their pending credit is dropped at the commit and counted as `departedDropped`, because no live citizen can receive it and no inheritance exists (ASM-1624). Deaths realise on day 29 and emigration is monthly, so this case occurs every month; a test pins it. *Review amendment:* the draft's "credit stands" cannot hold under the pending-credit ruling.

**AC-23.** Given a citizen who immigrates or is born after their shard-day in a month, then they are first paid in the next month; a newborn is never paid in the birth month.

**AC-24.** Given a month in which the shard-day pass runs before the city has any residents on a shard, then the shard contributes zero and no error is raised.

## E. Single balanced ledger post at day 30

**AC-25.** Given the per-shard accumulators (employedPrivate, employedPublic, pendingCredit per shard), when the month close runs, then the wage bill posted is the ascending-shard sum of employed counts times the gross wage, posted once per leg (private from firms, public from treasury), and no ledger post for wages happens on any other day (ASM-1585). The existing later legs are unchanged in order and rule: the `monthlyWagesFloor` treasury backstop, the policy wage-level extra and `CollectTax` income on `wagePosted`.

**AC-26.** REWRITTEN by review. The draft's equation (per-citizen net credited equals the ledger's net posted to households, exactly) is false on main and would stay false: per-citizen Wealth is off-ledger (moneycirc.go doc), the ledger also posts the floor backstop and the policy wage extra with no per-citizen counterpart, and income tax is collected on the whole `wagePosted`. Money conservation for the pending-credit rule is stated in both directions, with exact integer equality in micropounds, per month, per sector:
  - (i) Ledger: `RecomputeMoneyStock == MoneyStock` after the month close (unchanged invariant).
  - (ii) Accrual equals bill: the pending total = employedPrivate x net wage (private), and employedPublic x net wage (public), from the same shard-day counts that sized the bill in AC-25.
  - (iii) Commit when paid (no lost money): if the firms post succeeded, private committed + private departedDropped = private pending total, and every live citizen with pending is credited exactly once.
  - (iv) No credit when unpaid (no minted money): if the firms post was rejected, private committed = 0, every private pending is dropped, nothing carries into the next month, there is no claw-back, and `E-WAGES-REJECTED` plus the existing MET-G217 shortfall surface fire.
  - (v) Public: committed unconditionally as today; public commit is not gated on `PostWages` success, exactly as on main (ASM-1625).
  A test per direction drives the firms credit line to exhaustion (the BUG-548 attack fixture) and to success, and asserts (i)..(v).

**AC-27.** (Aaron OQ-1: PENDING CREDIT, ruled) Given the BUG-548 coupling, then citizens never hold credited Wealth for a private wage the ledger did not pay: the credit is recorded as pending on the shard-day and committed at the month close only if the firms wage post succeeds; if it is rejected, pending is dropped and a loud `E-WAGES-REJECTED` is logged (GR#1). **REJECTED on mechanism, blocking.** The draft says the commit is "a cheap per-shard pass, not a full walk". A per-citizen commit to Wealth at the month close is a walk over every employed citizen (about half the population), which is the very month-end stall this feature exists to remove. The draft's own "per-shard pending slot" cannot know which citizens to credit. **The lead/BA must choose and specify one of:**
  - (a) Deferred fold: the month close records only the commit verdict (O(1)), and the next month's shard-day pass folds last month's pending into Wealth if the verdict was "paid". Wealth then lags by up to one month, which is a balance-visible change and needs Aaron's nod. The verdict and the per-row pending must be saved state.
  - (b) Lazy materialisation: the per-shard commit flag flips at the close in O(256), and every Wealth reader (AC-18's inventory, PopulationHash, save) sees `Wealth + pending` when the flag is set.
  - (c) Accept a month-close walk over pending citizens only, measured under AC-32/33. This conflicts with the feature's purpose.
  Whichever is chosen, PopulationHash and the save must cover the pending column (AC-9, AC-36).

**AC-28.** Given the wage post and the consumption/tax legs, then their relative ORDER inside the month tick (consumption and tax before wages, BUG-548) is unchanged and only the citizen walks have moved.

## F. Balance implication and directional tests (AARON BALANCE)

**AC-29.** Given the new payroll timing, then month-total wages, treasury and Household balances differ slightly from the pre-feature run on an identical seed; the delta over a 24-month run at the fixture population is recorded in the BOW item as a table (month, old total, new total, delta). The change is flagged `AARON BALANCE PASS` in the code comment at the formula site and listed in the balance flags for Aaron.

**AC-30.** Given balance numbers are placeholder, then they are pinned only by directional tests, never exact new constants: (a) full employment yields wages greater than half employment; (b) a hire before the shard-day raises that month's total, a hire after does not; (c) in a fixture where no citizen's employment changes during any month, month-total wages and every citizen's Wealth at each month close are exactly equal to the pre-feature run (staggering changes nothing when nothing changes mid-month); this replaces the draft's unstated "tolerance band"; (d) population does not collapse (BUG-452 / BUG-548 cascade guard) over 24 months at the standard fixtures.

**AC-31.** Given existing goldens that pin monthly wealth or wage totals, then each is regenerated once, with the diff reviewed and listed in the commit message as intended; any other golden moving is a failure.

## G. Performance (measured, recorded, not asserted in CI)

**AC-32.** Given 1M and 10M populations on the reference machine (same one used for the proving plan, spec recorded), when run at N=1 and N=default, then the builder records in the BOW item: month-end tick duration before this feature, after at N=1, after at N=default; per-day tick duration p50/p95/max; and the max-day to median-day ratio. No CI assertion on wall-clock (verification standards).

**AC-33.** Given the 1M run, then the recorded p95 per-day tick is within the 160 ms turbo budget and the month-end tick is no more than 2x the median day. If not met the BOW item is left open with the measurement, not marked done. CI asserts only structural proxies: the AC-14 zero-gather probe, and probe counts per day within the schedule's 8-9 shard bound.

**AC-34.** Given the 10M run, then the measured month-end stall (replacing the 2.4-6 s estimate in a2.md section 1.2) is recorded and a2.md's estimate is annotated with the measured figure and date; the speed-up versus worker count (1, 2, 4, 8) is recorded as a curve, since the walks are memory-bandwidth-bound and not expected to scale linearly.

## H. Save/load mid-month

**AC-35.** Given a save taken at any day-tick `t` in 0..29 of a month, when loaded into a fresh Composition via `LoadAt` and continued to the end of month and beyond, then `StateDigest` equals the digest of the uninterrupted run at every tick, for t in {0, 1, 14, 28, 29} at minimum and a sweep over all 30 in a nightly-tier test (BUG-1201 lesson: use a fresh Composition, not the same instance).

**AC-36.** Given the per-shard accumulators (employed counts, pending totals, ran-this-month flags), the per-citizen pending-credit column and any AC-27 commit verdict, then they are written by the citizens save participant in ascending shard order as fail-closed records; a malformed or truncated record refuses the load with `E-ACCUM-CORRUPT` and never defaults to zero (ASM-1584).

**AC-37.** Given an old save with no accumulator records, when loaded mid-month, then it loads with accumulators absent, the current month's payroll runs once in legacy month-end mode, and the next month starts staggered; this is logged once, and no citizen is paid twice or zero times in the transition month (ASM-1584). The test loads into a fresh Composition via `LoadAt` (BUG-1201).

**AC-38.** Given a save taken at N=1 and loaded at N=8 (and the reverse), then the continued run is byte-identical to the uninterrupted run; the worker count is never part of the save.

**AC-39.** Given the month-start snapshot, then either it is derivable from saved state on load or it is saved; a mid-month load followed by a barrier pass yields the same pairings as the uninterrupted run (AC-15 under load).

## I. Registry, docs, wiring

**AC-40.** Given the new flag and option, then they are registered in code.json via the master plan flow, each new error code is claimed via `add-error.js`, `doc.go`/`registry.go` comments and `moneycirc.go` doc comments describe the staggered model, and the `GGR53` code.json check is green.

**AC-41.** Given built-but-not-wired risk (project lesson), then a test builds the real `metroserve` run path (not a unit constructor) at `-citizen-workers 4` and asserts that the composed CitizensAPI's resolved worker count is 4 and that `runShardsParallel` was called with workers == 4 (the AC-8 seam, not observed concurrency), plus an end-to-end run proving the month-end probe counters of AC-14.

## Open questions for Aaron (all answered 2026-11-17; kept for the record)

- **OQ-1 (ASM-1578).** ANSWERED (Aaron): Option A, pending credit; dropped if the firms post fails, no claw-back. The commit mechanism is still open (AC-27). Original: The wage credit happens on the shard-day but whether the ledger pays is only known at day 30 (BUG-548 firms credit line). Option A (assumed in AC-27): hold the credit pending and commit at day 30 only on success. Option B: credit on the shard-day and, on a rejected post, claw back at day 30. Option C: drop the coupling. Recommend A.
- **OQ-2.** ANSWERED (lead): metroserve only. Original: Is the default `min(GOMAXPROCS, 8)` also wanted for the engine pool and for local `cmd/metropolis`, or metroserve only (assumed)?
- **OQ-3.** ANSWERED (lead): unpaid that month, no proration. Original: Is the accepted unpaid-hire (AC-21) a one-month effect only, or should the first month after hire be pro-rated? Assumed unpaid, no proration, per the Q100155 text.
- **OQ-4.** ANSWERED (lead): daily, partner read on the month-start snapshot; see AC-16 for why that read is empty today. Original: Fertility: a2.md has it already daily per shard; the brief lists it as a month-end barrier. Assumed it stays daily and only its cross-shard partner read uses the snapshot (ASM-1579). Confirm.

## Assumptions logged

ASM-1576 (workers has no setter, detgate does not vary it), ASM-1577 (PopulationHash already covers Wealth/Employment), ASM-1578 (credit versus BUG-548 gate), ASM-1579 (fertility already daily), ASM-1580 (snapshot field set), ASM-1581 (default applies at metroserve only), ASM-1582 (COW snapshot, memory cost), ASM-1583 (shard-day state decides pay), ASM-1584 (accumulators are save state, legacy fallback), ASM-1585 (wage bill from summed shard counts).

## Independent review 2026-11-17 (opus-review-FEAT-9995385598)

Hostile requirements review by an agent that did not write these ACs. Checked against internal/engine/citizens (registry.go workers field and ApplyLifeEventCommand locking, coldpass.go runShardsParallel and ColdPassSchedule, fertility.go applyFertilityLocked), internal/engine/compose (moneycirc.go, and compose.go's financeHook wage sequence ~2600-2815), internal/engine/detgate, a2.md 1.2 and A2Bev001 v23 Q100155.

**Rulings applied.**
- Aaron OQ-1, pending credit: AC-27 and the new pending-credit glossary entry; conservation restated in both directions in AC-26.
- Lead OQ-2, metroserve only: AC-1.
- Lead OQ-3, unpaid with no proration: AC-21.
- Lead OQ-4, fertility daily with the partner read on the snapshot: AC-16, with a finding.

**ACs amended in place (19):** AC-1, AC-5, AC-8, AC-10, AC-11, AC-15, AC-16, AC-17, AC-18, AC-19, AC-20, AC-21, AC-22, AC-25, AC-26 (rewritten), AC-30(c), AC-36, AC-37, AC-41. Code-fact findings behind them:
- AC-8/AC-41: "more than one goroutine ran shards" is scheduler-dependent, because `runShardsParallel` work-steals and one goroutine can take all shards, so it would flake. Now asserts the `workers` argument through a seam, with `-race` (AC-12) as the concurrency proof.
- AC-10: an integer sum in completion order is commutative, so the proposed negative control could never go red. Now a deterministic order-dependent fault.
- AC-15/16/17/18: no barrier pass reads Employment, Sector or Wealth. formResidentHouseholds reads Household; fertility reads BirthMonth, household membership and liveness, single-goroutine after the join. So the month-start snapshot is empty for today's passes (ASM-1626). The real exposure the draft missed is the **mid-month readers** of Wealth and Employment: attract migration, firms founding, households, citizen_query. AC-18 now inventories and classifies every reader.
- AC-22: "credit stands" is impossible under pending credit. A citizen departing before the commit has their pending dropped (ASM-1624). Deaths realise on day 29, so this happens every month.
- AC-26: the draft's exact equation, per-citizen net credited equals the ledger's net to households, is false on main: Wealth is off-ledger, there is the floor backstop and the policy extra, and tax is on the whole wagePosted. Replaced with five exact checks covering both directions.
- AC-30(c): the tolerance band was never stated. Replaced with an exact steady-state equality.

**ACs rejected (blocking, 2):**
1. **AC-13, the staggered pass seam.** Compose mutates only through `ApplyLifeEventCommand`, which takes `c.mu`. `AdvanceDayTick` holds `c.mu` across `runShardsParallel`. "Inside the shard's worker" therefore deadlocks (ASM-1623). Supply an ICD that picks either (a) a CitizensAPI per-shard mutation surface or (b) a serial per-day compose pass after `AdvanceDayTick`, naming the daily hook and its order.
2. **AC-27, the pending-credit commit mechanism.** A per-citizen commit at the month close is a walk over about half the population, which reintroduces the stall the feature removes. Choose (a) deferred fold into next month's shard-day, which is balance-visible and needs Aaron's nod, (b) lazy materialisation behind a per-shard commit flag, or (c) accept a measured close-time walk. Then state the save and PopulationHash coverage.

**Needs lead confirmation, not blocking:** AC-20's "after marking" reading of Q100155 versus a2.md's "start of the shard-day" (ASM-1627); public-sector commit stays ungated (ASM-1625).

**New assumptions:** ASM-1623, ASM-1624, ASM-1625, ASM-1626, ASM-1627.

**Verdict: NOT READY.** Blocking list: AC-13 (seam ICD) and AC-27 (commit mechanism; option (a) also needs Aaron). Sections A (worker config) and B (determinism gate) are dispatchable on their own as a first slice, once AC-13's choice is known, if the lead wants to split the item.
