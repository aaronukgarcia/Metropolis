# FEAT-2326609764 requirement pack — enable the O(changed) spatial-partition tick (default ON)

> BA pack, 2026-10-30, to `TEMPLATE-requirement-pack.md`. Supplements, does not replace, the 26-AC design doc `docs/planning/acceptance/FEAT-2326609764.md` (increment ladder in its section 10). Docs only.

## 1. Context and goal

**BOW:** FEAT-2326609764 (P1, open); blockers listed on the item: BUG-1064, BUG-1065, BUG-1066, BUG-1067, BUG-1091 (all "inc2" findings). **mkey:** `[FEAT-2326609764]`. **Northstar stage:** convergence / the 100M finish line. The item's own BA ceiling statement (BOW comment 2026-09-03 "THE HONEST CEILING") says O(changed) is *necessary, not sufficient* for 100M in JavaScript: memory binds, density (FEAT-2326609761 / FEAT-2326609763) is mandatory, and O(changed) is a constant-factor win (about 20x on a mature or paused city, about 5x while building).

**The state of play (verified 2026-10-30):**
- **inc1 landed with the flag OFF** at `1a4668e` (2026-09-11, r2 ACCEPT), plus doc amendment `3bc8f25` and the CI fix `c4477c6` (BUG-1051 TDZ). The flag is `PARTITIONED_DERIVATIONS`, `webconsole/src/sim/sectorPartition.ts:317` (`let`, initial value from `DEFAULT_PARTITIONED_DERIVATIONS = false` at `:301`, test-only setter around `:327-335`). It is read in exactly one place outside the module: `webconsole/src/sim/data.ts:4658` (`if (PARTITIONED_DERIVATIONS) return totalJobsPartitioned(s);`), imported at `data.ts:47`.
- **Exactly one derivation is partitioned: `totalJobs`.** The residue list `NOT_PARTITIONED` is published (inc1 AC-17).
- **inc2 (dirty tracking) is NOT on main.** `grep markSectorsDirty webconsole/src` returns nothing. The inc2 work stopped mid-round on 2026-09-11 (single-thread order): r1 REJECT -> lead ruling (per-state side-band, BUG-1064..1070) -> rework applied in worktree `lane-764-v2` and **saved as a patch** `E:\git\metropolis-status\patches-2026-09-11\lane-764-v2.patch` (7 files: engine.ts +295/-16, sectorPartition.ts +564, three new test files); **r2 round never produced a verdict**. BUG-1091 (savepoint-restored city never rebuilds the completion schedule) is a later finding and is not covered by that rework as far as the BOW comments show.
- The design doc's own sequencing (its section 10): **the flip to default ON is inc6**, after inc2 (dirty tracking), inc3 (the integer family of ~13 derivations + Layer S/G split + self-audit + ratio gate), inc4 (road-connectivity diff) and inc5 (residue). inc2 itself admits a GAP: with the flag ON it "copies clean sectors by reference, recomputes dirty in one scan restricted to dirty keys" but is "still O(N) to locate dirty buildings (no reverse index) - true O(D) is inc3" (build report, 2026-09-11 08:15).

**What "inc2 was supposed to prove":** that a reducer-produced dirty-sector side-band (precise set, or `null` meaning FULL recompute) drives a partitioned `totalJobs` that is byte-identical to the whole-city function over a 200-tick action stream, and that "1 placement = exactly 1 dirty sector at every scale" (AC-24.5, the structural dirty-count gate - the gate that actually protects the feature, since a degradation to "everything dirty" is invisible to every equality test). It was to give "the first real win, on totalJobs only."

**Goal of this pack:** define the acceptance bar for **flipping the default to ON** (inc6 semantics) and, because that flip is dishonest unless the work under it is real, state the **entry gate**: the minimum landed increments without which flipping would turn on an O(N) path labelled O(changed). **Domain-of-effect honesty:** if only `totalJobs` is partitioned, flipping ON gives Aaron no felt win; the pack therefore makes the flip conditional on a measured speedup (AC-6), not on the calendar.

## 2. Acceptance criteria

**Entry gate (preconditions, checked by the lead before dispatch, not by the developer):**
- EG-1: inc2 is landed on main with an ACCEPT verdict (the stopped r2 round is run first, on the `lane-764-v2.patch`, cut onto current `main`; the patch was cut from `c4477c6`). BUG-1064/1065/1066/1067/1091 are each done with a regression test; BUG-1091 in particular needs a restore-path pin.
- EG-2: inc3's integer family covers every derivation the felt tick spends time in, per the design doc's AC-23 budget table, OR Aaron rules the flip may go ahead with a smaller family and the AC-6 gate sized to that family.
- EG-3: the self-audit (design AC-13) exists. Flipping ON without a production staleness audit is refused.

**AC-1 (the default is ON, at the one definition).** Given `DEFAULT_PARTITIONED_DERIVATIONS` in `sectorPartition.ts:301`, when a fresh process imports the module with no test seam active then `PARTITIONED_DERIVATIONS === true`, and the value is read from that single constant (no second literal; GR#3). *Mutation:* set the constant back to `false` -> the test (which imports in a fresh process, mirroring the `sectorPartition-entry-order` pattern) reddens. *Surface:* the exported flag via a fresh-process test and the debug JSON line that reports the partition mode.

**AC-2 (O(changed) is proved by counters at a bounded composition).** Given the 13k-building scale fixture (`webconsole/test/scale/fixture.mjs`, after the BUG-653/BUG-654 repairs the design doc's AC-18/AC-24 require) and a counter on "buildings visited by the partitioned derivation path" (a module-level integer incremented per building evaluated, no clock), when 1 building is placed and 100 quiet ticks follow then: visits on the placement tick <= (buildings in the one dirty sector + a documented constant), and visits on each quiet tick == 0. When 50 buildings are placed in 50 distinct sectors then visits <= 50 x (max buildings/sector). *Mutation:* make `markSectorsDirty` always escalate to FULL (the "correctness-preserving degradation to everything dirty") -> visits jump to about 13k and the test reddens; this is design AC-24.5 and must be a named test. **No wall-clock assertion.** Wall time may be printed.

**AC-3 (O(changed) at a second, larger composition shows the same shape).** Given the 30k-building synthetic fixture (design doc AC-18: a synthetic shape-matched fixture, NOT Aaron's private savepoint, which must never be committed) when the AC-2 scenario repeats then visits per placement are within 10% of the 13k figure (independent of N), proving the count is O(D) and not O(N). *Mutation:* an O(N) locate-the-dirty-building scan (the inc2 GAP named above) -> visits scale with N and the test reddens. If EG-2 leaves this gap open, AC-3 is recorded as RED-expected and the flip is blocked until inc3 closes it (the flip must not ride on a known O(N) locate).

**AC-4 (parity with flag OFF on a converge fixture).** Given the converge fixtures `webconsole/test/converge-fixtures/converge-finance-actions.json` / `converge-services-actions.json` and the committed trajectories `internal/converge/testdata/finance-webconsole-v1.json` and `services-webconsole-v1.json`, when the emitters (`webconsole/test/converge-fixture-emit.mjs`, `converge-fixture-emit-services.mjs`) run with the flag ON then the emitted JSON is **byte-identical** to the committed fixtures (the emitters' own "no silent drift" test must stay green without `--write`). Also: a 300-tick sha256 of the canonical full-state serialisation on the 13k fixture, flag ON vs flag OFF, is identical (design AC-21/22 method used in the inc1 round). *Mutation:* introduce the design doc's three RED-proof defects (off-by-one sector key, float accumulation, stale clean-sector reuse) one at a time -> each reddens at least one of these. Note the finance trajectory can legitimately change when derivations change; if an emitter diff appears, it is a **finding to bounce**, not a regen: regenerate only for an intended, ruled change (lead port hygiene: converge-fixture regen is the lead's call).

**AC-5 (rollback path exists, is documented, and is exercised).** Given the flag is default ON, when an operator needs to revert then there is a documented single switch: (a) a runtime debug toggle exposed in the debug panel / `debugjson` (design doc inc6: "exposed as a runtime debug toggle so Aaron can A/B his own live city") and (b) a one-line source revert of `DEFAULT_PARTITIONED_DERIVATIONS` to `false`. The rollback is documented in the module header of `sectorPartition.ts` and in the pack's BOW comment. The toggle test flips ON -> OFF -> ON mid-session on a running city and asserts the derived totals are identical before and after each flip (no stale carry-over across a flip; the side-band must treat a flip as FULL). *Mutation:* omit the FULL escalation on flip -> a stale aggregate survives and the test reddens. The whole-city functions remain exported and the comparison harness stays available for at least one full milestone after the flip (design doc section 10); deleting them is a separate approved item.

**AC-6 (the flip is justified by a same-run ratio, not a wall-clock bound).** Given the design doc's AC-24 ratio gate (partitioned vs whole-city, measured in the same process in the same run so the hardware factor cancels) when run on the 30k fixture then speedup >= the family-appropriate `MIN_SPEEDUP` (design doc: >= 10x steady state at 30k, >= 4x under the burst profile; EG-2 may reduce these in proportion to the derivations actually partitioned, as an Aaron-visible number). If the measured ratio is below the bound the flip is NOT made. *Mutation:* disable the incremental path (always rebuild) -> ratio about 1 and reddens. The ratio is of two timings taken together; it is the only place time may appear, and only as a ratio; no absolute ms bound.

**AC-7 (production staleness audit is on and loud).** Given the self-audit (design AC-13, about every 300 ticks, mismatch registry error + fall back to the whole-city path for the session) when a deliberately stale aggregate is injected in a test then the audit raises the registry error (code via `add-error.js`) and the session falls back to the whole-city path. *Mutation:* disable the comparison -> the injected staleness goes unnoticed and the test reddens.

**AC-8 (the Worker path is not silently left behind).** inc2's build report flags that `hydrate`/`source==='tick'` through the Web Worker cannot carry a `WeakMap` side-band across `postMessage` ("flagged, not dropped"). Given the sim runs in the Web Worker (`simWorker.ts`, `webWorkerFlag.ts`) when the flag is ON then dirty state either travels explicitly or the worker path takes the FULL recompute fail-safe, and a test proves both the main-thread and worker routes give identical totals. *Mutation:* worker path reuses a stale index -> reddens. (If this proves out of reach, it is a named non-goal with a BOW code, and the flag stays OFF for the worker route.)

## 3. Non-goals

- Partitioning derivations beyond the inc3 family; the residue (`buildingDisplayStates`, `crimeRateOf`, `wellbeingOf`, `demandFixPlan`, `stationLinks`, `lineUsageOf`, `congestionLinesOf`) is inc5, tracked on this item.
- Road-connectivity diff (inc4). inc3's naive "road change = all sectors Layer-G dirty" rule is acceptable for the flip; it is a known cliff, stated in the release note.
- A Go engine port of the heavy path. The BA ceiling comment (2026-09-03, point c) recommends against it unless measurement after inc6 AND density still shows the tick over budget; that would be a fresh pack.
- Memory/density work (FEAT-2326609761, FEAT-2326609763) and the IndexedDB save move: 100M critical path, separate items.
- Deleting the whole-city path or the differential harness.
- Hierarchical summation tree (inc7), only if measured fold cost dominates.

## 4. Developer direction

- **Files:** `webconsole/src/sim/sectorPartition.ts` (flag at `:301/:317`, side-band), `webconsole/src/sim/data.ts` (dispatch at `:4658`, import `:47`), `webconsole/src/sim/engine.ts` (the reducer choke point where dirty sets are committed), `webconsole/src/sim/debugjson.ts` and the debug panel (toggle + mode line), `webconsole/src/sim/simWorker*.ts` (AC-8), tests under `webconsole/test/` (`sectorDirty.test.mjs`, `partition-differential.mjs`, `sectorPartition-entry-order.test.mjs`, attacker suites `attack-feat764-*.test.mjs` stay).
- **Constraints:** GR#21 determinism (design AC-26: no map-range-with-break; integer order-independent folds, `Object.is`-grade equality); no storage or clock reads in hot paths (BUG-602: the flag read must be a module constant, not a `localStorage` read per call); import-cycle TDZ rule (BUG-1051): any new constant lives in the `grid.ts` leaf and a fresh-process entry-order test covers each new module; `SimState` gains **no** field (design AC-21), side-band is a `WeakMap` keyed by state with no strong reference to a previous state (lead ruling 2026-09-11); error codes only via `tools/plan/add-error.js` riding in the feature commit; units registry (`/units-lint`) if a new unit appears.
- **Converge-fixture regeneration:** any flows change requires regenerating via the emitter's `--write` **only on lead instruction**; the default expectation is byte-identical (AC-4).
- **Scoped verification only:** `webconsole/test/scoped.mjs` on the files you touch plus `tsc --noEmit`; the bounded set (`scoped.mjs --webconsole-ci`) only in a fresh gate worktree with `gen-version.mjs` run first, at the lead's request. Never a full suite; 30-minute watchdog.
- **Branch/worktree:** cut from current main. Commit `[feat]: ... [FEAT-2326609764]` with the increment named. Verdict-before-commit applies (code-bearing commit).

## 5. Tester watch-fors

- **Built but not wired:** with the flag ON, prove the partitioned path is actually REACHED at runtime in the dev server / headless run: the visit counter of AC-2 is non-zero on a real placement and the debug JSON mode line reads "partitioned". A green equality test with the flag silently reverted to OFF (or the dispatch at `data.ts:4658` unreachable) is the classic miss.
- **Degradation to everything-dirty:** this is invisible to equality tests; AC-2 counters are the only protection. Verify the test goes red under the always-FULL mutation.
- **Prove the harness can fail:** run the design doc's three RED proofs (off-by-one, stale reuse, float accumulate) in scratch copies, watch red, restore, verify md5.
- **StrictMode double-invoke:** ref-mutating or side-band-mutating updaters double-run under React StrictMode (BUG-756 class). Test with a real StrictMode mount; the commit of the side-band must be idempotent. Once-per-load vs reused-action class: reset / newGame / hydrate / savepoint restore must escalate to FULL (BUG-1067, BUG-1091).
- **Two timelines in one process:** a branch A/B lockstep test (BUG-1065 pin) must be present and green.
- **Dual-engine trap:** this is webconsole TS. A converge-fixture regen check is mandatory (AC-4). TS twin question: the Go engine has no sector partition and none is requested; state "not applicable" explicitly.
- No wall-clock bounds except the same-run ratio of AC-6; flaky `consolidator-glide-perf` under parallel load is a known pre-existing flake, run isolated 3x before calling a regression.
- Tester PASS/FAIL against the ACs only; never fixes; FAIL bounces to the same developer.

## 6. Round guidance (GR#23)

Independent Opus attacker, never the author; verdict from a worktree cwd. Attack first:
1. **Missed dirty trigger:** enumerate every site that writes `s.buildings` (design AC-8 table + BUG-1068's structural grep of write sites); find one that neither marks a sector nor escalates to FULL. Savepoint restore, reset/newGame, hydrate, consolidator ladder/glide/replan, auto-connect, `sellAsset` and `consolidatorUndo` (listed in inc2's NOT_TRACKED set) are the prime suspects.
2. **Stale clean-sector reuse:** copy-by-reference of clean sectors is only sound if no input of a Layer-S aggregate changed outside a marked sector; probe tier change, relocation across sectors mid-construction (BUG-1064), and the completion schedule across a quiet-tick empty-set promotion.
3. **Flip semantics:** toggle ON/OFF/ON mid-session; a stale carry-over is the bug.
4. **Memory leak:** `WeakRef` + forced GC proof that no state history is retained (BUG-1066).
5. **Self-audit laundering:** can the audit compare the partitioned result against itself through the flag (the BUG-1019 class: "the harness compared the fold against itself through the flag")?
6. **Worker route.**

## 7. Done means

With `PARTITIONED_DERIVATIONS` defaulting to ON, a single-building placement visits only its sector's buildings and quiet ticks visit none (counter evidence at 13k and 30k), flag-ON output is byte-identical to flag-OFF on the converge fixtures and the 300-tick state hash, the same-run speedup ratio meets its stated bound, the debug toggle rolls back live, and the BOW item carries an independent ACCEPT verdict and commit refs for the entry-gate increments and the flip.

---

## Open questions for Aaron (not resolvable from the repo)

1. **Is a flip on a one-derivation family acceptable?** Today only `totalJobs` is partitioned. Flipping with only inc2 gives no felt win and leaves an O(N) locate. This pack recommends: do not flip until inc3 (the integer family) lands. Confirm, or rule a smaller threshold.
2. Design doc Q1-Q5 (x50 map as the 100M geography; the two-column 20x/5x promise; self-audit default ON, recommended YES) are not answered anywhere I could find in the BOW comments; confirm they are still open.
3. The r2 round on `lane-764-v2.patch` never ran. Re-running it on a patch cut from `c4477c6` against today's main will need a rebase; the lead decides whether to re-cut or re-apply.
