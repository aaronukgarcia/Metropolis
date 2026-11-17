# FEAT-9995385598 - Configurable shard workers + staggered per-shard monthly money walks

mkey: FEAT-9995385598. Sources: a2.md section 1.2 (Option B recommended), A2Bev001 v23 Q100155 (APPROVED), BOW item. Status: criteria draft for dispatch. Placeholder error codes use `E-<NAME>`; real codes are claimed via `tools/plan/add-error.js` at build.

## Scope and glossary

- **Staggered ring**: a pass that runs per shard on that shard's scheduled day (`ColdPassSchedule(day)`: shard `i` runs on day `i*30/256`), fanned out over workers by `runShardsParallel`, reduced in ascending shard order.
- **Barrier pass**: a pass that needs cross-shard reads and runs once at month end (day 29 / `DaysPerMonth-1`), single-threaded or a deterministic parallel read followed by an ordered apply.
- **Shard-day**: the day-tick on which a citizen's shard is scheduled.
- **Month-start snapshot**: a read-only view of Employment, Sector and Wealth as at the first day-tick of the month, which barrier passes read instead of live state (ASM-1580, ASM-1582).

Passes classified (ASM-1580, ASM-1579):

| Pass | Reads | Class |
|---|---|---|
| `markEmploymentAndCount` | citizen's own id, age, Employment | staggered (shard-local) |
| `distributeWagesToResidents` | citizen's own Employment, Wealth | staggered (shard-local) |
| `formResidentHouseholds` | Household==0 across all shards, pairs by ascending id | barrier |
| fertility partner lookup | partner across shards | barrier-read (reads snapshot); the fertility pass itself stays daily |
| `DeathQueue.RealiseDrained` | whole queue | barrier (unchanged) |
| `postConsumptionAndTax`, `PostWages*`, tax legs | aggregates | day 30, single ledger post |

## A. Worker count configuration

**AC-1.** Given `metroserve` started with no worker option, when it boots, then the citizen worker count is `min(GOMAXPROCS, 8)` and the boot log prints the resolved value.

**AC-2.** Given `-citizen-workers 1`, `-citizen-workers 8` or env `METRO_CITIZEN_WORKERS=4`, when it boots, then that value is used; when both flag and env are set the flag wins (ASM-1581).

**AC-3.** Given `-citizen-workers 0`, a negative value, a non-integer, or a value above 256, when it boots, then it exits non-zero with `E-WORKERS-INVALID` naming the value; it is never silently clamped.

**AC-4.** Given a `CitizensAPI` built without the option, when `runShardsParallel` runs, then workers is 1 (library default unchanged; every existing test and golden is byte-stable) (ASM-1581).

**AC-5.** Given the worker count is set after the first tick has run, then the call is refused with `E-WORKERS-LATE`; the count is fixed at construction/wire time (ASM-1576).

**AC-6.** Given any worker count N, when `-citizen-paging` is on, then at most N shards are pinned at once by the pool and the residency budget doc (`registry.go` ~725) is updated to state the bound `budget + N`; a test with budget 2 and N 8 proves no pinned shard is ever evicted (existing BUG-664 acquire/release discipline) and no deadlock occurs.

## B. Determinism

**AC-7.** Given the same seed and inputs, when the composed engine runs for at least 14 months at worker counts 1, 2, 7 and 14, then `Composition.StateDigest` and `PopulationHash` are byte-identical across all runs.

**AC-8.** Given the gate, when it runs, then the worker count it varies is the CITIZEN worker count (the new option), not only `core.WithPoolSize`. Today `workers` has no setter and `detgate` varies only the engine pool, so the existing 1-vs-14 gate never exercises the citizen pool (ASM-1576). A test proves the gate drives `runShardsParallel` with more than one goroutine (counter of distinct concurrent shard workers greater than 1 at N=14).

**AC-9.** Given `PopulationHash`, when per-citizen Wealth, EmploymentState, Sector or Workplace differs for any one citizen, then the digest differs. This already holds (`registry.go` ~1703, ASM-1577); the AC is a mutation test per field: perturb one citizen's field in a copy and require the digest to change.

**AC-10.** Given a deliberately wrong reduction (per-shard totals summed in goroutine-completion order, or a per-day float or map-ordered fold), when the 1-vs-14 gate runs, then it goes red. Proven by a negative test that injects the fault via a test-only seam and asserts the gate reports a mismatch. The gate must run with a population large enough that every one of the 256 shards holds citizens and at least one month with hires and fires on distinct shards.

**AC-11.** Given the staggered ring, then per-shard results are written only into `results[shard]` (or the shard's own accumulator slot) and reduced in ascending shard order after the join; no shared counter is mutated by workers, and the AST gate (astgate) finds no `go` statement or shared write outside `runShardsParallel`.

**AC-12.** Given `-race`, when the determinism test suite runs at N=14, then no data race is reported (max two Go `-race` suites at once, 30-minute watchdog applies).

## C. Staggered passes and barrier passes

**AC-13.** Given a citizen on shard S, when day-tick `d = S*30/256` runs, then employment marking and wage crediting for that citizen happen on that day-tick inside the shard's worker, and not at month end.

**AC-14.** Given the month-end tick (day 29), then `markEmploymentAndCount` and `distributeWagesToResidents` perform no citizen walk; they only read the per-shard accumulators. A probe counter (as in the BUG-665 gate) asserts zero full-population gathers by these two passes at month end.

**AC-15.** Given `formResidentHouseholds` at month end, then it reads the month-start snapshot for any Employment/Sector/Wealth predicate, pairs unpaired citizens in ascending id order exactly as today, and its result is byte-identical at every worker count.

**AC-16.** Given the fertility partner lookup on shard S at day `d`, when it reads another shard, then it reads the month-start snapshot, never live state another worker may be mutating; a test mutates the partner shard concurrently and asserts the partner choice is unchanged (ASM-1579, ASM-1580).

**AC-17.** Given the month-start snapshot, then it is captured exactly once per month before the first staggered day runs, holds only the fields staggered passes mutate and barrier passes read, and its extra memory at 1M and 10M is recorded and stays within 10 percent of resident citizen memory (ASM-1582).

**AC-18.** Given any barrier pass reads a field the staggered passes mutate that is not in the snapshot, then a lint/test (reflection over the snapshot struct versus the barrier readers) fails the build; the snapshot field set is data, not a hand list in two places (GGR3/SSOT).

**AC-19.** Given `DeathQueue.RealiseDrained`, then it still runs once at day 29 and its output is unchanged by worker count; a citizen who dies at month end is still counted in that month's shard-day accumulators if their shard ran earlier (see AC-22).

## D. Payroll rule (Q100155), stated precisely

**AC-20.** Given a citizen whose shard-day is D, when the shard runs on day D, then the citizen is paid for the month if and only if their Employment.State is Employed after that shard's employment marking and cold-pass job matching on day D. A citizen Employed on D is credited the full monthly net wage; one not Employed on D is credited nothing for the month.

**AC-21.** Given a citizen hired on day 20 whose shard-day is 5, when the month closes, then they receive no wage that month and are paid from the following month's shard-day if still employed. This is accepted behaviour under Q100155 and is asserted by a named test (`TestPayroll_HireAfterShardDayUnpaid`). Symmetrically, a citizen fired on day 20 whose shard-day was 5 IS paid for the month (ASM-1583).

**AC-22.** Given a citizen who dies, emigrates or is removed after their shard-day but before day 30, then their credit stands (money already credited stays) and the accumulators are not rewound; a test pins this.

**AC-23.** Given a citizen who immigrates or is born after their shard-day in a month, then they are first paid in the next month; a newborn is never paid in the birth month.

**AC-24.** Given a month in which the shard-day pass runs before the city has any residents on a shard, then the shard contributes zero and no error is raised.

## E. Single balanced ledger post at day 30

**AC-25.** Given the per-shard accumulators (employedPrivate, employedPublic, creditedWealth per shard), when day 30 runs, then the wage bill posted is the ascending-shard sum of employed counts times the gross wage, posted once as a single balanced entry per leg (private from firms, public from treasury), and no ledger post happens on any other day for wages (ASM-1585).

**AC-26.** Given a completed month, then money is conserved in both directions: the sum of per-citizen net wage credited this month equals the net-of-tax amount the ledger posted to households for the same month, within 0 micropounds (exact integer equality), and `RecomputeMoneyStock` equals `MoneyStock` closing after the post.

**AC-27.** Given the BUG-548 coupling (private wages rejected when the firms credit line is exhausted), then citizens never end the month holding credited wealth for a wage the ledger did not pay. The mechanism is: the private-sector credit accumulates in a per-shard pending slot on the shard day and is committed to citizen Wealth at day 30 only when the post succeeds; if rejected, pending is dropped and a loud `E-WAGES-REJECTED` is logged (GR#1). This is an ASSUMPTION pending Aaron's ruling (Open Question OQ-1, ASM-1578). The commit at day 30 is a cheap per-shard pass, not a full walk, and must be shown by AC-29 to stay within budget.

**AC-28.** Given the wage post and the consumption/tax legs, then their relative ORDER inside the month tick (consumption and tax before wages, BUG-548) is unchanged and only the citizen walks have moved.

## F. Balance implication and directional tests (AARON BALANCE)

**AC-29.** Given the new payroll timing, then month-total wages, treasury and Household balances differ slightly from the pre-feature run on an identical seed; the delta over a 24-month run at the fixture population is recorded in the BOW item as a table (month, old total, new total, delta). The change is flagged `AARON BALANCE PASS` in the code comment at the formula site and listed in the balance flags for Aaron.

**AC-30.** Given balance numbers are placeholder, then they are pinned only by directional tests, never exact new constants: (a) full employment yields wages greater than half employment; (b) a hire before the shard-day raises that month's total, a hire after does not; (c) month-total wages over a steady-state year are within a stated tolerance band of the pre-feature total; (d) population does not collapse (BUG-452 / BUG-548 cascade guard) over 24 months at the standard fixtures.

**AC-31.** Given existing goldens that pin monthly wealth or wage totals, then each is regenerated once, with the diff reviewed and listed in the commit message as intended; any other golden moving is a failure.

## G. Performance (measured, recorded, not asserted in CI)

**AC-32.** Given 1M and 10M populations on the reference machine (same one used for the proving plan, spec recorded), when run at N=1 and N=default, then the builder records in the BOW item: month-end tick duration before this feature, after at N=1, after at N=default; per-day tick duration p50/p95/max; and the max-day to median-day ratio. No CI assertion on wall-clock (verification standards).

**AC-33.** Given the 1M run, then the recorded p95 per-day tick is within the 160 ms turbo budget and the month-end tick is no more than 2x the median day. If not met the BOW item is left open with the measurement, not marked done. CI asserts only structural proxies: the AC-14 zero-gather probe, and probe counts per day within the schedule's 8-9 shard bound.

**AC-34.** Given the 10M run, then the measured month-end stall (replacing the 2.4-6 s estimate in a2.md section 1.2) is recorded and a2.md's estimate is annotated with the measured figure and date; the speed-up versus worker count (1, 2, 4, 8) is recorded as a curve, since the walks are memory-bandwidth-bound and not expected to scale linearly.

## H. Save/load mid-month

**AC-35.** Given a save taken at any day-tick `t` in 0..29 of a month, when loaded into a fresh Composition via `LoadAt` and continued to the end of month and beyond, then `StateDigest` equals the digest of the uninterrupted run at every tick, for t in {0, 1, 14, 28, 29} at minimum and a sweep over all 30 in a nightly-tier test (BUG-1201 lesson: use a fresh Composition, not the same instance).

**AC-36.** Given the per-shard accumulators (employed counts, pending credit, ran-this-month flags), then they are written by the citizens save participant in ascending shard order as fail-closed records; a malformed or truncated record refuses the load with `E-ACCUM-CORRUPT` and never defaults to zero (ASM-1584).

**AC-37.** Given an old save with no accumulator records, when loaded mid-month, then it loads with accumulators absent, the current month's payroll runs once in legacy month-end mode, and the next month starts staggered; this is logged once, and no citizen is paid twice or zero times in the transition month (ASM-1584).

**AC-38.** Given a save taken at N=1 and loaded at N=8 (and the reverse), then the continued run is byte-identical to the uninterrupted run; the worker count is never part of the save.

**AC-39.** Given the month-start snapshot, then either it is derivable from saved state on load or it is saved; a mid-month load followed by a barrier pass yields the same pairings as the uninterrupted run (AC-15 under load).

## I. Registry, docs, wiring

**AC-40.** Given the new flag and option, then they are registered in code.json via the master plan flow, each new error code is claimed via `add-error.js`, `doc.go`/`registry.go` comments and `moneycirc.go` doc comments describe the staggered model, and the `GGR53` code.json check is green.

**AC-41.** Given built-but-not-wired risk (project lesson), then a test builds the real `metroserve` run path (not a unit constructor) at `-citizen-workers 4` and asserts more than one goroutine ran shards, plus an end-to-end run proving the month-end probe counters of AC-14.

## Open questions for Aaron

- **OQ-1 (ASM-1578).** The wage credit happens on the shard-day but whether the ledger pays is only known at day 30 (BUG-548 firms credit line). Option A (assumed in AC-27): hold the credit pending and commit at day 30 only on success. Option B: credit on the shard-day and, on a rejected post, claw back at day 30. Option C: drop the coupling. Recommend A.
- **OQ-2.** Is the default `min(GOMAXPROCS, 8)` also wanted for the engine pool and for local `cmd/metropolis`, or metroserve only (assumed)?
- **OQ-3.** Is the accepted unpaid-hire (AC-21) a one-month effect only, or should the first month after hire be pro-rated? Assumed unpaid, no proration, per the Q100155 text.
- **OQ-4.** Fertility: a2.md has it already daily per shard; the brief lists it as a month-end barrier. Assumed it stays daily and only its cross-shard partner read uses the snapshot (ASM-1579). Confirm.

## Assumptions logged

ASM-1576 (workers has no setter, detgate does not vary it), ASM-1577 (PopulationHash already covers Wealth/Employment), ASM-1578 (credit versus BUG-548 gate), ASM-1579 (fertility already daily), ASM-1580 (snapshot field set), ASM-1581 (default applies at metroserve only), ASM-1582 (COW snapshot, memory cost), ASM-1583 (shard-day state decides pay), ASM-1584 (accumulators are save state, legacy fallback), ASM-1585 (wage bill from summed shard counts).
