# BUG-666 requirement pack — the rowOf index: complexity-evidence scale gate and residual O(N) terms on the 100M path

> BA pack, 2026-10-30, written to `TEMPLATE-requirement-pack.md`. **Read section 1 first: BUG-666 itself is already DONE. This pack covers what is still owed on the same path.** Docs only; no code was changed to write it.

## 1. Context and goal

**BOW:** BUG-666 (P0, `done` 2026-09-04, landed `7f8dc73`), BUG-663 (P1, `done` 2026-09-04, `adf0c5c`), BUG-665 (P0, `done`, `17345bc`). mkey for any new commit: the module key `engine.citizens` unless a specific feature key exists. **Northstar stage:** convergence / the 100M finish line (Aaron Q100114 "the BL1 finish line IS 100 million", Q100137 and the 2026-09-04 ruling: prove 100M individual citizens on the cloud Go engine; memory `metropolis-100m-finish-line`).

**What the repo shows (verified 2026-10-30, not recalled):**
- The original defect: `ColdShard.rowOf` was a linear scan over `ids` (`docs/planning/go-engine-100m-proving-plan.md` section 3.1). Measured Go day-tick curve had power-law exponent **1.55** over 1M to 10M (same doc, section 2.1: 17.63 ms at 1M, 624.35 ms at 10M).
- The fix **has landed**: `ColdShard.index map[uint64]int32` (`internal/engine/citizens/coldshard.go:119`), maintained by `append` (`:148`), swap-delete `removeAt` (`:261-264`) and `rebuildIndexLocked` (`:134`, called on the paging decode path, `paging.go:191` note). `rowOf` is now a map hit (`coldshard.go:274-279`).
- The landing commit message is honest that the blocker was NOT fully cleared by it alone: normal-mix day-tick 306.7 to 152.7 ms at 3M (2.0x), **residual exponent about 2.4 unchanged**; the title then passed to BUG-663 (global `DeathQueue.mu` taken once per citizen at `coldpass.go:206`). BUG-663 closed 2026-09-04 with "0.545 ms day-tick at 1M pending. 100m blocker cleared." That closing line is a **single-point 1M reading**, not a scaling curve.
- Existing correctness evidence is strong: `attack_bug666_round_test.go` (index-vs-linear-scan oracle after every operation, 20,000-op fuzz, paging round trip, PopulationHash goldens, int32 bound).

**The gap this pack closes.** Nobody has re-measured the scaling *shape* after BUG-666 + BUG-663 and recorded it as a gate. Specifically:
1. `bug665_population_perf_gate_test.go` still carries the pre-index framing ("a LINEAR SCAN on this branch", `:64`) and a **wall-clock** bound `popPerfGateTickBoundPerTick = 4 * time.Second` (`:139`); its own doc says it "still owes" a BUG-666 re-measurement. `.github/workflows/ci.yml:1071` says the same. A wall-clock bound at 1M cannot detect a regression back to O(N^2) (it would pass at 4 s) and is forbidden as a CI bound by the verification standards.
2. `docs/planning/go-engine-100m-proving-plan.md` section 3.3 / 3.5 / 3.6 list **other** O(N)-per-month and per-tick terms that the index does not remove (see AC-4..AC-6). The 30M linearity gate was "unbookable until BUG-663 falls" (BUG-666 landing message); BUG-663 has fallen and no linearity gate exists.
3. The index costs real memory: landing message states **about 10.0 GB at 100M trough occupancy** (24-38 B/entry), inside the 32 GiB floor. That figure is a Go-map estimate, not a measured 100M run.

**Goal:** turn "rowOf is fixed" from an assertion into a **counter-based complexity gate at a stated composition**, re-point the stale 1M gate off wall-clock, and bound the residual O(N) monthly terms that still sit on the same path.

**Filing note for the lead (not the developer's job):** BUG-666 is `done`. Work under this pack needs a new child item (suggest a P1 under the 100M epic, e.g. "rowOf-class complexity gate + residual monthly O(N) terms"); BOW is not edited by this BA. The AC doc stays named for BUG-666 as instructed.

## 2. Acceptance criteria

Composition vocabulary used below (scale gates bound the **load**, not only the ticks - memory `metropolis-scale-gates-bound-compositions`): a **"live composition"** means a real compose-built city whose seed population is demographically live: paired childbearing-age households registered via `SeedHouseholds` and made moneycirc-visible via `Deps.SeedResidentIDBase/Count` (the BUG-665 round-2 lesson - an idle seed measures nothing; see `bug665_population_perf_gate_test.go` doc and `tickperf_test.go:15-40` `buildPairedRecords`). A fixture whose citizens have no real partner/household is NOT acceptable for any AC below.

**AC-1 (lookups are counted, not timed).** Given a counter exported only to tests (package-private atomic, zero cost when unused or compiled behind a test-only seam, no clock read, BUG-602 class) that counts `rowOf` calls and `rowOf` *probe steps* (map lookups = 1 step each; a linear scan would add 1 per element visited), when a live composition of 100,000 citizens runs 90 day-ticks (3 simulated months, so every monthly pass fires at least 3 times) then the report line `rowOf probes/lookup` is exactly 1.000 (max across all lookups = 1). *Mutation:* restore the linear scan in `rowOf` (or bypass the index for a miss) -> probes/lookup becomes about N/256 and the test reddens.
*Surface:* the test's printed table plus the assertion; no internal variable beyond the test seam.

**AC-2 (complexity ratio gate at stated compositions).** Given live compositions at N = 100,000, 300,000 and 1,000,000 citizens, each run for the same 90 day-ticks with births/deaths active (not a frozen city), when total `rowOf` lookups and total allocations-as-counted-bytes (runtime `MemStats.TotalAlloc` deltas are counts, not time) are recorded per run, then lookups(3N)/lookups(N) <= 3.3 and lookups(10N)/lookups(N) <= 11 (i.e. empirical exponent <= 1.05 on the *operation count*). *Mutation:* reintroduce any per-citizen inner pass that does a lookup per resident per resident-pass (the original four `moneycirc` loops with an extra nested lookup) -> ratio goes to about 9 / 100 and reddens. The bound is on counts, so it holds on a slow shared CI runner. **Forbidden:** any `time.Since` / `time.Now` / ms bound as the pass condition. Wall time may be *printed* for humans, never asserted.

**AC-3 (the stale wall-clock gate is retired or demoted).** Given `bug665_population_perf_gate_test.go`, when this pack is delivered then `popPerfGateTickBoundPerTick` (`:139`) is no longer the pass/fail condition: either (a) it is replaced by the AC-1/AC-2 counters, or (b) it is kept only as an extremely loose sanity ceiling documented as "informational, not a regression detector" and `.github/workflows/ci.yml` comment block at `:1071` is corrected to say the index has landed. The "still owes a BUG-666 re-measurement" text in both files is replaced by the measured numbers from AC-2. *Mutation:* with the linear scan restored, the gate must go RED (today it would not at 1M inside 4 s - that is the defect being fixed).

**AC-4 (resident enumeration does not walk ever-minted ids).** Given a city that has run enough months for D deaths (D >= 10% of the seed population, forced by mortality in the fixture), when the four `moneycirc` resident passes run (`markEmploymentAndCount` `moneycirc.go:335`, `employedResidentCount` `:399`, `formResidentHouseholds` `:450`, `distributeWagesToResidents` `:508`) then the number of per-id `CitizenAt` attempts per pass equals the **live** resident count within 1%, not the ever-minted count. This is the proving-plan section 3.3 finding that `liveResidentIDs` (now `compose.go:3964`) enumerates by counter range, so dead ids still cost a (now O(1)) failed lookup. *Mutation:* stop pruning / widen the enumeration back to the whole minted range -> attempts/live grows with D and reddens. If the developer finds the enumeration is already pruned, AC-4 becomes a pure regression pin and the developer states so with the grep evidence.

**AC-5 (monthly full-population materialisation is bounded).** Given `coldParamsLocked` -> `allColdRecordsLocked` (`registry.go:1927` -> `:1718`), which builds a `[]ColdRecord` of every citizen once per month (proving plan section 3.5: more than 12 GB transient at 100M), when a live 1M composition crosses a month boundary then the counted bytes allocated by that call are <= 2% of `N * sizeof(ColdRecord)` (sampling only what the params need). *Mutation:* restore the full materialise -> bytes ratio about 100% and the test reddens. If the owner decides this belongs to a separate item the AC is deferred in section 3 with that item's code; it is NOT silently dropped.

**AC-6 (per-tick hot-set rebuild is not O(N) allocation).** Given `hotIDSetLocked` (`registry.go:1710`) rebuilt as a fresh `map[uint64]bool` every day-tick (proving plan section 3.6), when 90 day-ticks run at 1M then total allocation counted for the hot-set path is O(H) in the hot count H, not O(N). *Mutation:* rebuild the whole population set each tick -> reddens.

**AC-7 (determinism intact).** Given the same seed and journal, when the gated runs of AC-2 execute twice then `PopulationHash` (`registry.go:1631`) is byte-identical between runs AND equals the pre-existing golden in `attack_bug666_round_test.go` (`TestBUG666PopulationHashGolden`) where the fixture overlaps. No map iteration decides tick order (GR#21; `coldshard.go:116` records this contract). *Mutation:* iterate `index` in a path that affects ordering -> hash differs between runs under `-count=3` and reddens.

**AC-8 (index memory is measured, not estimated).** Given the 10.0 GB at 100M figure is an extrapolation, when a 10M live composition is built then `runtime.MemStats` heap-in-use attributable to `index` (difference with and without index construction, or a `reflect`-free counted entries x measured bytes/entry) is recorded as a measured bytes-per-citizen figure and the 100M extrapolation line is updated in `doc.go:37` area and in `go-engine-100m-proving-plan.md`. This is an evidence AC (a recorded number), gated only by "figure <= 40 B/entry", the upper end of the landing round's measured band. *Mutation:* replace `map[uint64]int32` with `map[uint64]int` or a larger value type -> bytes/entry rises and the gate reddens.

## 3. Non-goals

- Re-doing or re-attacking the index correctness already proven by `attack_bug666_round_test.go`; that suite must keep passing, nothing here replaces it.
- The `DeathQueue` global-mutex fix (BUG-663, done). Do not re-open.
- Reducing the 100M memory floor or moving to a flat/sorted-array index (an alternative to the map; separate decision, ask Aaron before changing the data structure).
- Cloud/Azure sizing and the per-day-tick budget at 100M (proving plan sections 2.4 and 3.4; Azure cost is GGR16 gated and out of scope for a BA pack).
- Fixing the "duplicate-id seed divergence" and the "one-process curve methodology" round follow-ups named in the BUG-666 landing message, unless the developer finds one blocks AC-2's measurement (then escalate, do not widen).
- Anything in the TS webconsole sim (that is FEAT-2326609764's lane).

## 4. Developer direction

**Files/packages:** `internal/engine/citizens` (`coldshard.go`, `registry.go`, `tickperf_test.go`, a new counters seam test-only), `internal/engine/compose` (`compose.go:3964` `liveResidentIDs`, `moneycirc.go:335/399/450/508`), `internal/harness/headless/bug665_population_perf_gate_test.go`, `.github/workflows/ci.yml` (comment block near `:1071` and the `perf-population-probe` job; changing the job's required status is Aaron's branch-protection touch, not yours).

**Constraints that bind:**
- Determinism (GR#21): `index` is lookup-only. No `range` over a map that influences state; order-independent folds only.
- No clock/storage reads in hot paths (BUG-602). The probe counter must be a plain integer increment or an atomic, behind a build tag or test seam, and must not be compiled into the production hot path as an extra branch if it measurably costs; state the measured cost.
- Counter-based and ratio-based evidence only. **No wall-clock assertion anywhere**; wall time may be logged.
- Error codes only via `tools/plan/add-error.js` (claim-range, add, check) and riding in the feature commit. Parallel lanes cut from one HEAD collide on codes; pre-assign in the dispatch brief.
- Scoped verification only: `go test ./internal/engine/citizens/... ./internal/engine/compose/... -run <named>` and the one headless gate you touch. Never `go test ./...`. 30-minute watchdog; max two Go `-race` suites at once. Use `-count=1` (cached runs launder tree-scanning gates).
- The heavy curve tests are env-gated (`METRO_PERF_CURVES=1`, `tickperf_test.go:201`); keep AC-2's 100k/300k points in the default run (small) and the 1M point behind the existing gate if it exceeds a few minutes, and say which in the report.
- Branch/worktree: cut from current `main` in a fresh worktree. Commit format `[type]: ... [mkey]`. No Co-Authored-By or Claude-Session trailers beyond what the project hook accepts (GGR64/I5).

## 5. Tester watch-fors

- **Built-but-not-wired:** prove the counter is read from the REAL compose-built city path (grep that the gate builds through `compose` / `metroserve`-equivalent wiring, not a bare `CitizensAPI` shortcut). The BUG-665 round-2 history is exactly an idle seed that measured nothing.
- **Composition honesty:** print the composition (N, households paired, months run, births, deaths) in the test output; a run with births = 0 and deaths = 0 is a FAIL of the fixture, not a pass.
- Prove each new test CAN fail: restore the linear scan in a scratch copy, watch AC-1/2/3 red, revert, verify the file md5 is restored (GR#24: scratch copies only, no destructive git).
- No wall-clock assertion sneaks in as `testing.Benchmark` `ns/op` threshold. Both-bound rule: any liveness check needs a progress floor AND a rate ceiling.
- Gate exit codes: capture the runner's own exit code to a file; never pipe a gate into `tail`/`echo` (exit-code laundering, 2026-09-05).
- Dual-engine: this is Go-only. The TS twin question is answered "not applicable - the webconsole sim has no cold-store id index"; if the developer touches anything under `webconsole/` stop and bounce.
- Existing `attack_bug666_round_test.go`, `TestBUG666PopulationHashGolden`, and `TestAdvanceDayTickCurve*` must still pass untouched.
- Tester gives PASS/FAIL against the ACs only and never fixes; FAIL bounces to the same developer.

## 6. Round guidance (GR#23)

Independent Opus attacker, never the author, verdict recorded from a worktree cwd (`node claude-bow.js destructive <code> --verdict ...`; a main-checkout cwd is refused as a self-verdict). Attack in this order:
1. **Counter laundering:** can the probe counter report 1.000 while real lookups still scan (counting map hits at the wrong layer; counting calls but not steps)? Mutate `rowOf` to scan on miss only and see whether AC-1 notices the *miss* path (dead ids are the worst case).
2. **Vacuous fixture:** does the AC-2 composition actually exercise fertility partner lookups and all four moneycirc passes? Check births/deaths and lookups-per-tick are non-trivial and scale with N.
3. **Ratio gaming:** can a fixture whose work is dominated by a constant (e.g. seed generation) hide a quadratic term in the ratio? Require ratios computed on the tick phase only, with seed and wire excluded.
4. **Wall-clock re-entry:** grep the changed test files for `time.` assertions.
5. **Determinism:** `-count=3` hash equality; any new map range.

## 7. Done means

On a live 100k/300k/1M composition with births and deaths active, `rowOf` probes/lookup equals 1, total lookups scale with exponent <= 1.05, the stale wall-clock gate no longer decides pass/fail, the residual monthly O(N) terms (AC-4..AC-6) are either bounded or explicitly deferred to named BOW items, `PopulationHash` is unchanged, and the child BOW item carries an independent ACCEPT verdict plus the commit refs.
