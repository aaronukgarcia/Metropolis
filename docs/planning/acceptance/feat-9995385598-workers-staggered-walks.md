# FEAT-9995385598 - Configurable shard workers + staggered per-shard monthly money walks

mkey: FEAT-9995385598. Sources: a2.md section 1.2 (Option B recommended), A2Bev001 v23 Q100155 (APPROVED), BOW item. Status: READY FOR DEV (re-checked 2026-11-17 by the BA after the A2Bev001 v23.4 rulings; the two review blockers AC-13 and AC-27 are rewritten to the rulings and nothing else is open for Aaron; builder-level notes are in the re-check section at the end). Aaron's OQ-1 (including the AC-27 mechanism) and the lead's OQ-2..OQ-4, AC-13 and AC-20 rulings are applied in place. Placeholder error codes use `E-<NAME>`; real codes are claimed via `tools/plan/add-error.js` at build.

## Scope and glossary

- **Staggered ring**: a pass that runs per shard on that shard's scheduled day (`ColdPassSchedule(day)`: shard `i` runs on day `i*30/256`). The citizens cold pass is fanned out over workers by `runShardsParallel` and reduced in ascending shard order; the compose money passes (employment marking, pending accrual, fold) run SERIALLY after `AdvanceDayTick` returns (AC-13), so they never depend on the worker count.
- **Barrier pass**: a pass that needs cross-shard reads and runs once at month end (day 29 / `DaysPerMonth-1`), single-threaded or a deterministic parallel read followed by an ordered apply.
- **Shard-day**: the day-tick on which a citizen's shard is scheduled.
- **Month-start snapshot**: a read-only view of Employment, Sector and Wealth as at the first day-tick of the month, which barrier passes read instead of live state (ASM-1580, ASM-1582). *Review note:* no barrier pass on main reads any of these three fields (ASM-1626, see AC-17/AC-18), so the snapshot is built only for fields a reader actually needs.
- **Day 30 / month close**: the PhaseFinance month-close tick of a month, strictly after the day-29 (`DaysPerMonth-1`) shard-day pass of that month has run, so all 256 shards have contributed before the post. "Day 29" in this doc means the last cold-pass day-tick and "day 30" means the month close.
- **Pending credit** (Aaron OQ-1 and AC-27 ruling, A2Bev001 v23.4): a per-citizen wage credit recorded (accrued) on the shard-day and held OFF Wealth. At the month close a single O(1) verdict per sector records whether the wage post paid (private: the firms post succeeded; public: always, ASM-1625). On that citizen's NEXT shard-day, before any new accrual, a paid pending is folded into Wealth and a rejected one is dropped. Wealth therefore lags pay by up to one month. A citizen holds at most one unfolded pending at a time.
- **Fold**: the per-citizen step on a shard-day that moves last month's pending into Wealth (verdict paid) or drops it (verdict rejected).

Passes classified (ASM-1580, ASM-1579):

| Pass | Reads | Class |
|---|---|---|
| `markEmploymentAndCount` | citizen's own id, age, Employment | staggered, serial compose pass after `AdvanceDayTick` (AC-13) |
| `distributeWagesToResidents` | citizen's own Employment, Wealth, pending | staggered, serial compose pass after `AdvanceDayTick` (AC-13): fold last month's pending, then accrue this month's |
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

**AC-9.** Given `PopulationHash`, when per-citizen Wealth, EmploymentState, Sector, Workplace or the unfolded pending credit (and its accrual month) differs for any one citizen, or the saved commit verdict differs, then the digest differs. Wealth/Employment already hold (`registry.go` ~1703, ASM-1577); the pending column and verdict are new and must be added to the hash (AC-27). The AC is a mutation test per field: perturb one citizen's field in a copy and require the digest to change.

**AC-10.** Given a deliberately wrong, ORDER-DEPENDENT reduction, when the 1-vs-14 gate runs, then it goes red. *Review amendment:* summing integer per-shard totals in completion order is commutative and cannot change any digest, so it is not a valid negative control. The injected fault must change state when order changes, and must do so deterministically, not by scheduler luck: a test-only seam that, whenever workers > 1, applies the per-shard cold-pass results in reverse shard order through an order-sensitive step (the compose money pass is serial and worker-independent per AC-13, so its order is covered by AC-13's order-digest test, not by this control) (for example the next id allocation or an ascending pairing). The negative test asserts the gate reports a mismatch. The gate must run with a population large enough that every one of the 256 shards holds citizens and at least one month with hires and fires on distinct shards.

**AC-11.** Given the staggered ring, then per-shard results are written only into `results[shard]` (or the shard's own accumulator slot) and reduced in ascending shard order after the join; no shared counter is mutated by workers, and a test or astgate rule finds no `go` statement in the staggered pass code (internal/engine/compose's money-pass files and the new citizens seam) outside `runShardsParallel`; the compose per-day pass (AC-13) contains no `go` statement at all.

**AC-12.** Given `-race`, when the determinism test suite runs at N=14, then no data race is reported (max two Go `-race` suites at once, 30-minute watchdog applies).

## C. Staggered passes and barrier passes

**AC-13.** (lead ruling, option (b), A2Bev001 v23.4: REWRITTEN; resolves ASM-1623) Given a citizen on shard S, when day-tick `d = S*30/256` runs, then employment marking, the fold of last month's pending credit and the accrual of this month's pending credit (AC-20, AC-27) for that citizen happen in a compose per-day money pass that runs SERIALLY after `CitizensAPI.AdvanceDayTick` has returned on that day-tick, and not at month end. The pass is registered as its own `PhaseDailyTick` hook immediately after the existing `citizens` cold-pass hook (`coldPassHook`) and before `build`, so it sees the day's cold-pass job matching and runs while no `c.mu` is held (no new `CitizensAPI` mutation surface; mutations go through the existing `ApplyLifeEventCommand`, so no deadlock and no SEC-020 copyguard change). It walks only that day's `ColdPassSchedule(d)` shards, in ascending shard order, and within a shard in ascending citizen id; it is never run inside `runShardsParallel`. On day 29 it runs before the month-end barrier passes (`RealiseDrained`, `formResidentHouseholds`, ASM-1628). Worker count therefore speeds only the cold pass; the money pass order is fixed and independent of workers.
  - Observable: a test seam records the ordered `(day, shard, citizenID)` sequence the pass visits; its hash is identical at workers 1, 2, 7 and 14 and equals the ascending-shard, ascending-id sequence computed independently in the test.
  - Gate: AC-7's 14-month detgate run covers it through `StateDigest`/`PopulationHash`; the seam hash is asserted in the same run.
  - Failure it must catch: a negative control (test-only) that visits shards in completion or reverse order makes the sequence hash or the digest differ and the gate goes red; a call into a `CitizensAPI` mutator from inside a worker, or a pass that runs while `c.mu` is held, deadlocks or trips AC-11/AC-12 (`-race`) and must be red, not hang (30-minute watchdog applies).

**AC-14.** Given the month-end tick (day 29), then `markEmploymentAndCount` and `distributeWagesToResidents` perform no citizen walk; they only read the per-shard accumulators and record the O(1) per-sector commit verdict (AC-27); the close folds or drops nothing per citizen. A probe counter (as in the BUG-665 gate) asserts zero full-population gathers and zero per-citizen Wealth writes by these two passes and by the close at month end.

**AC-15.** Given `formResidentHouseholds` at month end, then it stays a month-end barrier walk that reads live Household (no staggered pass mutates Household), pairs unpaired citizens in ascending id order exactly as today, and its result is byte-identical to the pre-feature run on the same seed and at every worker count. *Review amendment:* the draft's snapshot read was for predicates the pass does not have (it reads Household==0 only).

**AC-16.** (lead OQ-4: fertility stays daily, partner read on the month-start snapshot) Fertility stays daily, sequential, after the parallel join, as `applyFertilityLocked` already is. Its partner read takes any field that a staggered pass mutates from the month-start snapshot, never from live state. *Review finding, for the lead:* on main the partner read uses BirthMonth (immutable), live household membership and partner resolvability, and runs single-goroutine after the join, so there is no race to fix and no staggered-mutated field is read today (ASM-1626). Moving BirthMonth, Household or liveness onto a month-start snapshot would change behaviour: a partner who joined mid-month would be unresolvable. So the ruling is met by AC-18's lint: fertility reads no staggered-mutated field live, plus a test that fertility births are byte-identical to the pre-feature run on the same seed at N=1 and N=14. The draft's "mutates the partner shard concurrently" test is withdrawn because no concurrency exists at that point.

**AC-17.** Given the month-start snapshot, then it is captured exactly once per month before the first staggered day runs and holds only the fields that staggered passes mutate and some reader in AC-18's inventory must see at month-start state. If that set is empty (as on main, ASM-1626), no snapshot is built and the BOW item records why. If it is non-empty, its extra memory at 1M and 10M is recorded and stays within 10 percent of resident citizen memory (ASM-1582).

**AC-18.** Given the staggered passes mutate Employment, Sector and Wealth (and the pending-credit column; Wealth now lags pay by up to one month per AC-27, so every Wealth reader sees only folded wealth and never pending), then an inventory of every reader of those fields outside the staggered pass is written into the BOW item and enforced by an AST test over internal/engine. A reader is a selector or accessor on the Citizen record or on the shard columns. On main the readers include attract/migration.go, firms/founding.go, households, compose/citizen_query.go and PopulationHash. Each reader is classified: month-close (sees complete state, nothing needed), mid-month (sees mixed shard-day state; must read the snapshot or be listed as accepting mixed state with a one-line reason), or digest. A new unclassified reader fails the test. A reader that gates or spends on Wealth is marked feedback-bearing, which AC-30(c) needs. The classification is data, not a hand list in two places (GGR3/SSOT). *Review amendment:* the draft covered barrier passes only and missed the mid-month readers.

**AC-19.** Given `DeathQueue.RealiseDrained`, then it still runs once at day 29 and its output is unchanged by worker count. A citizen who dies at month end is still counted in that month's shard-day employed accumulators if their shard ran earlier (the ledger bill counts them); their unfolded pending credit follows AC-22.

## D. Payroll rule (Q100155), stated precisely

**AC-20.** Given a citizen whose shard-day is D, when the shard runs on day D, then the citizen accrues the month's wage (as a pending credit, AC-27; it is not in Wealth until the fold on their next shard-day) if and only if their Employment.State is Employed after that shard's cold-pass job matching AND the compose employment marking on day D, in that order (ASM-1627; a2.md 1.2 says "state at the start of the shard-day", Q100155 says "as of the shard's scheduled day", and this AC deliberately takes the after-marking reading so a newly decided working-age resident is paid that month; CONFIRMED by lead ruling 2026-11-17: pay on the state after that day's job matching and marking). A citizen Employed on D accrues the full monthly net wage as pending; one not Employed on D accrues nothing for the month. The fold of the previous month's pending happens first on the same shard-day and does not re-check employment.

**AC-21.** Given a citizen hired on day 20 whose shard-day is 5, when the month closes, then they receive no wage that month and accrue pending from the following month's shard-day if still employed (their Wealth rises one further month later, on the shard-day after that, AC-27). This is accepted behaviour under Q100155 and the lead's OQ-3 ruling (unpaid that month, no proration), asserted by a named test (`TestPayroll_HireAfterShardDayUnpaid`). Hires after the shard-day reach a citizen only through paths other than the shard's own cold pass, for example a direct `LifeEventEmployment` command; the test injects one that way. Symmetrically, a citizen fired on day 20 whose shard-day was 5 accrues pending for the month, it is committed at the close and folded into Wealth on their next shard-day even though they are then unemployed (ASM-1583).

**AC-22.** Given a citizen who dies, emigrates or is removed after their shard-day but before the month-close commit, then the employed accumulators are not rewound (the ledger bill counts them, per Q100155's per-shard totals), and their unfolded pending credit is dropped at the removal itself (an O(1) step per departing citizen, not a walk) and counted as `departedDropped` against its accrual month, whether the verdict is still open or already paid, because no live citizen can receive it and no inheritance exists (ASM-1624, ASM-1630). Deaths realise on day 29 and emigration is monthly, so this case occurs every month; a test pins both sub-cases (departs before the close, departs after the close but before the fold). *Review amendment:* the draft's "credit stands" cannot hold under the pending-credit ruling.

**AC-23.** Given a citizen who immigrates or is born after their shard-day in a month, then they are first paid in the next month; a newborn is never paid in the birth month.

**AC-24.** Given a month in which the shard-day pass runs before the city has any residents on a shard, then the shard contributes zero and no error is raised.

## E. Single balanced ledger post at day 30

**AC-25.** Given the per-shard accumulators (employedPrivate, employedPublic, pendingCredit per shard), when the month close runs (one balanced day-30 ledger post, A2Bev001 v23.4; accrual, fold and drop never post to the ledger because per-citizen Wealth is off-ledger), then the wage bill posted is the ascending-shard sum of employed counts times the gross wage, posted once per leg (private from firms, public from treasury), and no ledger post for wages happens on any other day (ASM-1585). The close then records the per-sector commit verdict (paid / rejected) for the month, O(1). The existing later legs are unchanged in order and rule: the `monthlyWagesFloor` treasury backstop, the policy wage-level extra and `CollectTax` income on `wagePosted`.

**AC-26.** REWRITTEN by review, reconciled 2026-11-17 to the deferred-fold ruling (AC-27). The draft's equation (per-citizen net credited equals the ledger's net posted to households, exactly) is false on main and would stay false: per-citizen Wealth is off-ledger (moneycirc.go doc), the ledger also posts the floor backstop and the policy wage extra with no per-citizen counterpart, and income tax is collected on the whole `wagePosted`. Money conservation for the pending-credit rule is stated in both directions, with exact integer equality in micropounds, per month M, per sector, checked at the close of M and again at the close of M+1 (after every shard has folded):
  - (i) Ledger: `RecomputeMoneyStock == MoneyStock` after every month close (unchanged invariant).
  - (ii) Accrual equals bill: at the close of M the pending total accrued = employedPrivate x net wage (private), and employedPublic x net wage (public), from the same shard-day counts that sized the bill in AC-25.
  - (iii) Folded when paid (no lost money): if the verdict for M is paid, then by the close of M+1, folded into Wealth + `departedDropped` = pending accrued in M, every live citizen with pending was folded exactly once, and no pending for M remains.
  - (iv) Nothing folded when unpaid (no minted money): if the firms post for M was rejected, folded = 0 for private, every private pending of M is dropped at its owner's next shard-day (or at departure), nothing carries into a later month, there is no claw-back, and `E-WAGES-REJECTED` plus the existing MET-G217 shortfall surface fire at the close of M.
  - (v) Public: verdict is always paid, exactly as on main; public is not gated on `PostWages` success (ASM-1625).
  A test per direction drives the firms credit line to exhaustion (the BUG-548 attack fixture) and to success across two consecutive months and asserts (i)..(v) at both closes.

**AC-27.** (Aaron option (a), deferred fold, A2Bev001 v23.4: REWRITTEN; resolves OQ-1) Given the BUG-548 coupling, then wage/payroll credits are PENDING: a citizen's wage for month M is accrued as a pending credit on their shard-day in M and does not touch Wealth. At the month close of M the close records only the per-sector commit verdict (private: the firms wage post succeeded or was rejected; public: paid), an O(1) step with no citizen walk. On that citizen's next shard-day in M+1, before any new accrual, the fold runs: verdict paid, the pending is added to Wealth; verdict rejected, the pending is dropped (`E-WAGES-REJECTED` has already been logged loudly at the close, GR#1). A citizen's Wealth may therefore lag pay by up to one month. A credit that arrives mid-month (a late hire reached through another path, a newborn, an immigrant) is never added to Wealth directly: it follows AC-21/AC-23 and is only ever accrued on a shard-day. One balanced day-30 ledger post remains (AC-25). The pending column, its accrual month and the verdict are saved state and are covered by `PopulationHash` and the save (AC-9, AC-36).
  - Observable: per-citizen `(Wealth, pending)` read through the test seam; the month-close verdict; the `departedDropped` and `folded` counters per month.
  - Gate: named tests `TestPayroll_PendingFoldedNextShardDay` (Wealth unchanged between accrual and the owner's next shard-day, then rises by exactly the net wage on it), `TestPayroll_RejectedPostDropsPending` (BUG-548 fixture: Wealth never rises, no carry-over), `TestPayroll_NoWealthWriteAtClose` (AC-14 probe: zero per-citizen Wealth writes at the close), plus AC-26 (i)..(v).
  - Failure it must catch: crediting Wealth on the shard-day (money minted if the post later fails), folding at the close (the month-end stall), folding a rejected month, folding twice, a fold that re-checks employment, or an unsaved pending/verdict (a mid-month save/load digest mismatch, AC-35/AC-36).

**AC-28.** Given the wage post and the consumption/tax legs, then their relative ORDER inside the month tick (consumption and tax before wages, BUG-548) is unchanged and only the citizen walks have moved.

## F. Balance implication and directional tests (AARON BALANCE)

**AC-29.** Given the new payroll timing and the deferred fold (Wealth lags pay by up to one month, a balance-visible change), then month-total wages, treasury and Household balances differ slightly from the pre-feature run on an identical seed; the delta over a 24-month run at the fixture population is recorded in the BOW item as a table (month, old total, new total, delta) including per-citizen Wealth and the outstanding pending total. The change is flagged `AARON BALANCE PASS` in the code comment at the formula site and listed in the balance flags for Aaron.

**AC-30.** Given balance numbers are placeholder, then they are pinned only by directional tests, never exact new constants: (a) full employment yields wages greater than half employment; (b) a hire before the shard-day raises that month's total, a hire after does not; (c) in a fixture where no citizen's employment changes during any month, month-total wages at each close are exactly equal to the pre-feature run, and every citizen's Wealth plus their unfolded pending at each close equals the pre-feature Wealth exactly (staggering and the lag change nothing when nothing changes mid-month); the Wealth identity is asserted in a fixture whose AC-18 feedback-bearing Wealth readers are disabled or proven absent, otherwise only the month-total wages equality is asserted (this replaces the draft's unstated "tolerance band"); (c2) Wealth lag: in a paid month a citizen's Wealth is unchanged between their accrual shard-day and their next shard-day, and rises by exactly the net wage on it; (d) population does not collapse (BUG-452 / BUG-548 cascade guard) over 24 months at the standard fixtures.

**AC-31.** Given existing goldens that pin monthly wealth or wage totals, then each is regenerated once, with the diff reviewed and listed in the commit message as intended; any other golden moving is a failure.

## G. Performance (measured, recorded, not asserted in CI)

**AC-32.** Given 1M and 10M populations on the reference machine (same one used for the proving plan, spec recorded), when run at N=1 and N=default, then the builder records in the BOW item: month-end tick duration before this feature, after at N=1, after at N=default; per-day tick duration p50/p95/max; and the max-day to median-day ratio. No CI assertion on wall-clock (verification standards).

**AC-33.** Given the 1M run, then the recorded p95 per-day tick is within the 160 ms turbo budget and the month-end tick is no more than 2x the median day. If not met the BOW item is left open with the measurement, not marked done. CI asserts only structural proxies: the AC-14 zero-gather probe, and probe counts per day within the schedule's 8-9 shard bound.

**AC-34.** Given the 10M run, then the measured month-end stall (replacing the 2.4-6 s estimate in a2.md section 1.2) is recorded and a2.md's estimate is annotated with the measured figure and date; the speed-up versus worker count (1, 2, 4, 8) is recorded as a curve, since the walks are memory-bandwidth-bound and not expected to scale linearly.

## H. Save/load mid-month

**AC-35.** Given a save taken at any day-tick `t` in 0..29 of a month, when loaded into a fresh Composition via `LoadAt` and continued to the end of month and beyond, then `StateDigest` equals the digest of the uninterrupted run at every tick, for t in {0, 1, 14, 28, 29} at minimum and a sweep over all 30 in a nightly-tier test (BUG-1201 lesson: use a fresh Composition, not the same instance).

**AC-36.** Given the per-shard accumulators (employed counts, pending totals, ran-this-month flags), the per-citizen pending-credit column with its accrual month, and the AC-27 commit verdict per sector for the last closed month (until every shard has folded it), then they are written by the citizens save participant in ascending shard order as fail-closed records; a malformed or truncated record refuses the load with `E-ACCUM-CORRUPT` and never defaults to zero (ASM-1584).

**AC-37.** Given an old save with no accumulator records, when loaded mid-month, then it loads with accumulators, pending and verdict absent (nothing to fold), the current month's payroll runs once in legacy month-end mode (credited straight to Wealth as today, so no pending is created), and the next month starts staggered; this is logged once, and no citizen is paid twice or zero times in the transition month (ASM-1584). The test loads into a fresh Composition via `LoadAt` (BUG-1201).

**AC-38.** Given a save taken at N=1 and loaded at N=8 (and the reverse), then the continued run is byte-identical to the uninterrupted run; the worker count is never part of the save.

**AC-39.** Given the month-start snapshot, then either it is derivable from saved state on load or it is saved; a mid-month load followed by a barrier pass yields the same pairings as the uninterrupted run (AC-15 under load).

## I. Registry, docs, wiring

**AC-40.** Given the new flag and option, then they are registered in code.json via the master plan flow, each new error code is claimed via `add-error.js`, `doc.go`/`registry.go` comments and `moneycirc.go` doc comments describe the staggered model, and the `GGR53` code.json check is green.

**AC-41.** Given built-but-not-wired risk (project lesson), then a test builds the real `metroserve` run path (not a unit constructor) at `-citizen-workers 4` and asserts that the composed CitizensAPI's resolved worker count is 4 and that `runShardsParallel` was called with workers == 4 (the AC-8 seam, not observed concurrency), plus an end-to-end run proving the month-end probe counters of AC-14 and that the compose per-day pass hook of AC-13 is registered and fires on the scheduled shard-days.

## Open questions for Aaron (all RESOLVED 2026-11-17; kept for the record)

- **OQ-1 (ASM-1578).** RESOLVED (Aaron, BOW comments 2026-11-17 15:21 and 16:43; A2Bev001 v23.4): Option A, pending credit; dropped if the firms post fails, no claw-back; mechanism ruled as deferred fold on the owner's next shard-day (AC-27). Original: The wage credit happens on the shard-day but whether the ledger pays is only known at day 30 (BUG-548 firms credit line). Option A (assumed in AC-27): hold the credit pending and commit at day 30 only on success. Option B: credit on the shard-day and, on a rejected post, claw back at day 30. Option C: drop the coupling. Recommend A.
- **OQ-2.** RESOLVED (lead, BOW comment 2026-11-17 15:21): metroserve only. Original: Is the default `min(GOMAXPROCS, 8)` also wanted for the engine pool and for local `cmd/metropolis`, or metroserve only (assumed)?
- **OQ-3.** RESOLVED (lead, BOW comment 2026-11-17 15:21): unpaid that month, no proration. Original: Is the accepted unpaid-hire (AC-21) a one-month effect only, or should the first month after hire be pro-rated? Assumed unpaid, no proration, per the Q100155 text.
- **OQ-4.** RESOLVED (lead, BOW comment 2026-11-17 15:21): daily, partner read on the month-start snapshot; see AC-16 for why that read is empty today. Original: Fertility: a2.md has it already daily per shard; the brief lists it as a month-end barrier. Assumed it stays daily and only its cross-shard partner read uses the snapshot (ASM-1579). Confirm.

## Assumptions logged

ASM-1576 (workers has no setter, detgate does not vary it), ASM-1577 (PopulationHash already covers Wealth/Employment), ASM-1578 (credit versus BUG-548 gate), ASM-1579 (fertility already daily), ASM-1580 (snapshot field set), ASM-1581 (default applies at metroserve only), ASM-1582 (COW snapshot, memory cost), ASM-1583 (shard-day state decides pay), ASM-1584 (accumulators are save state, legacy fallback), ASM-1585 (wage bill from summed shard counts). Review additions: ASM-1623 (RESOLVED by the AC-13 option (b) ruling), ASM-1624, ASM-1625 (CONFIRMED by lead), ASM-1626, ASM-1627 (CONFIRMED by lead). BA re-check additions: ASM-1628 (the compose per-day pass runs before the day-29 barrier passes; the builder confirms the ordering against compose.go and records it in a code comment), ASM-1629 (one unfolded pending per citizen: the fold always precedes the next accrual on the same shard-day, and the verdict for M is retained until M+1's day-29 pass has run), ASM-1630 (a departing citizen's unfolded pending is dropped at removal and counted against its accrual month).

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

## BA re-check 2026-11-17 (AC-13 and AC-27 to the rulings)

Rulings applied: AC-13 lead option (b), serial compose per-day pass after `AdvanceDayTick`; AC-27 Aaron option (a), deferred fold on the owner's next shard-day with one balanced day-30 ledger post (BOW comment 2026-11-17 16:43; A2Bev001 v23.4). Compose structure checked: the daily hook is `coldPassHook` ("citizens", PhaseDailyTick) at compose.go ~576 and the month sequence (`markEmploymentAndCount`, `distributeWagesToResidents`, `formResidentHouseholds`) at ~2619, ~2829, ~3310.

**Rewritten:** AC-13, AC-26 (reconciled), AC-27. **Reconciled:** status line, glossary (staggered ring, pending credit, fold), pass table rows, AC-9 (pending column and verdict in the hash), AC-10 (fault seam scoped to the cold pass), AC-11 (no `go` in the compose pass), AC-14 (verdict only, zero Wealth writes at close), AC-18 (Wealth lag, feedback-bearing readers), AC-19, AC-20 (after-marking confirmed, fold first), AC-21 (timing of accrual and fold), AC-22 (drop at removal), AC-25 (verdict, no ledger post for fold), AC-29 (Wealth lag in the delta table), AC-30(c) and new (c2), AC-36 (verdict and accrual month saved), AC-37 (legacy mode creates no pending), AC-41 (hook registered), OQ-1..OQ-4 marked RESOLVED, assumptions list.

**Builder notes, not blocking:** AC-30(c)'s Wealth identity depends on AC-18 showing which Wealth readers feed back into flows; ASM-1628 ordering is confirmed against compose.go at build. Nothing else is open and nothing needs Aaron except the AARON BALANCE PASS flag in AC-29 (balance numbers at row-by-row review, as always).

**Verdict: READY FOR DEV.** (Supersedes the 2026-11-17 review verdict above, whose two blockers are now closed.)
