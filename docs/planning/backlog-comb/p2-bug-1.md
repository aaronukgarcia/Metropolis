# Backlog comb: p2-bug-1 (103 items)

Method: title and description read for all 103; `git log --grep` run for every code (mentions in other commits were read, not assumed to be fixes); Grep spot-checks on main for about 30 items. Items with an empty description (1152, 1153, 1158, 1162, 1165, 1169, 1171, 1174, 1179, 1181, 1208, 522) got a title-level look only. `claude-bow.js show` was not run per item (budget). The slice JSON was cleaned out of temp mid-run, so the table follows the slice order recorded earlier in the run.

## Summary

Counts per class: STRONG 29, WEAK 18, NONE 2, STALE 3, DONE? 19, DUP 6, RULING 26 (sums to 103; the 29 STRONG are open and testable as written).

Counts per game value: GAME 28, ENABLER 17, INFRA 52, NOISE 6.

Biggest finding: 2 lanes never reached main, and they account for 22 of the 103 items (21 classed RULING, plus 1084 as DUP).
- FEAT-1972079928 inc3 (anti-sprawl): `planAntiSprawlReroute` has 0 hits in `webconsole/src`. 11 items (1031-1034, 1052-1055, 1079-1081) are findings against code that only exists in an unlanded lane.
- FEAT-2326609806 inc9b (attract multiplier): `integrationMultiplier` is absent from `webconsole/src`. 11 items (1010, 1011, 1035, 1036, 1037, 1046, 1057-1059, 1071, 1084) are the same.
- I classed them RULING: Aaron or the lead must say whether each lane resumes. If it resumes, the findings are STRONG and become the lane's regression list. If it is retired, close all 22 as STALE.

Second finding: 8 items from the FEAT-2326609764 inc2 spatial partition are almost certainly fixed. Commit 816f9bb (round r4 ACCEPT) lands 1091 and pins 1064-1067. Main no longer has the module-global `baseStateOf` or `completionSchedule`; both survive only in comments. `scheduleSectorCompletion` now has 4 call sites in `engine.ts`, and relocate calls `rescheduleSectorCompletion`. Close 1064-1069, 1091, 1092 and 1137 after a one-line confirmation each. I did not verify 1092 beyond the commit message.

The 5 items most worth doing next:
1. BUG-1143 (save/load employment divergence at a month boundary): the Save/Load determinism contract is player-felt, and the repro is exact.
2. BUG-1135 (menu save/load does nothing): the F10 menu still ships list-only, and the slot name, clock restore and KindDebug ops are unfinished.
3. BUG-1043 (jobsOverride 2**53): `data.ts:667-679` still truncates but never checks safe-integer. It is a small fix, and the fold-vs-walk divergence it prevents is a correctness fault.
4. BUG-1140 (converge Compare passes with zero evaluated ticks): a gate that passes on empty input is the verification-standards failure. It is shared across the finance, services and population domains.
5. BUG-1096 and BUG-1178 (metroserve health under-reports the tick after rehydrate; the torn identity.json read): both are cheap, both are verified still present, and both sit on the cloud-hosting path.

Close or merge:
- DONE? (cite commit):
  - 035: 96d6fe3 author-identity guard.
  - 1117: f54ec13.
  - 1181: 569667c.
  - 1186: b44347c.
  - 1162: b127698.
  - 1198: 779d10b removed `allColdRecordsLocked`.
  - 1189: accea4b did the MariaDB paths; the x/sys and x/text bump is unverified.
  - 471: 095a0d7.
  - 259: the compose package is registered as `feat.compositionroot`, 51 hits in `code.json`.
  - 432: CLAUDE.md no longer claims a `claude-spec-guard.js`; confirm and close, or file one item to build it.
- STALE: 1101 (the bill/bro worktrees were removed on 2026-10-06), 426 (the HMR class was killed by the bundle serving mode, 84f5201), and 251.
- DUP: 1084 into 1071; 1131 into 1130; 258 into 259-262; 261 and 262 into 367/1127. BUG-367 itself overlaps BUG-1127 (code.json drift).

Rulings Aaron owes:
- The inc3 and inc9b lane fate (above).
- BUG-1011 / 1036 / 1071 / 1084: whether the bare map out-scoring built cities is acceptable. 1036 states outright that it needs a balance ruling.
- BUG-518: whether wellbeing should drive move-ins, not only move-outs.
- BUG-522: whether to wire education-to-outcome state, which needs a design decision.
- BUG-289 / 371: month atomicity. The options brief 2dac185 is waiting on him: deferred-apply, snapshot-rollback or halt.
- BUG-1125 / 1126: for each of the 27 unwired engine packages and 9 unwired screens, wire, park or delete.
- BUG-247: whether to split the 193 KB `errors.json` or live with it.
- BUG-1129: whether `world.Cell` gains material, density, wind and hydrant fields, which MOD-040 AC-6 needs.

## Items

| Code | P | Type | Class | Value | Note |
|---|---|---|---|---|---|
| BUG-035 | P2 | bug | DONE? | INFRA | 96d6fe3 added the author guard against fabricated identities. The agent rule (never commit on a tracking branch) is unverified. |
| BUG-1010 | P2 | bug | RULING | ENABLER | inc9b attract-multiplier mutants. Code absent from main; lane fate. Findings themselves are testable. |
| BUG-1011 | P2 | bug | RULING | GAME | Bare map out-scores built cities in the measured table. Needs an Aaron balance ruling. |
| BUG-1031 | P2 | bug | RULING | GAME | inc3 reroute strands the road the player paid for. Exact fixture. Lane unlanded. |
| BUG-1032 | P2 | bug | RULING | GAME | inc3 anti-sprawl demolishes pre-existing roads on repeat drag. Lane unlanded. |
| BUG-1033 | P2 | bug | RULING | GAME | inc3 excludes pre-existing direct connections from the count. Lane unlanded. |
| BUG-1034 | P2 | bug | RULING | GAME | inc3 refunds spec cost, not paid amount; a zero-cost action can credit. Lane unlanded. |
| BUG-1035 | P2 | bug | RULING | ENABLER | inc9b NaN unmeasured sentinel lost on save/load. Lane unlanded; real class of bug. |
| BUG-1036 | P2 | bug | RULING | GAME | Neutral 1.0 outranks built multipliers. Item itself says it needs a balance ruling. |
| BUG-1037 | P2 | bug | RULING | INFRA | inc9b crime AC-5 threshold loosened on a non-reproducing measurement. Lane unlanded. |
| BUG-1043 | P2 | bug | STRONG | ENABLER | `data.ts:667-679` still has no safe-integer check; 2**53 diverges fold from walk. |
| BUG-1046 | P2 | bug | RULING | INFRA | inc9b broke the bug-519 well-served baseline (54 vs 55). Lane unlanded. |
| BUG-1052 | P2 | bug | RULING | GAME | inc3 connector double-places and double-charges its source tile. Lane unlanded. |
| BUG-1053 | P2 | bug | RULING | GAME | inc3 replan cascade recreates the connection anti-sprawl removed. Lane unlanded. |
| BUG-1054 | P2 | bug | RULING | INFRA | inc3 five surviving mutants. Becomes the lane's test list if it resumes. |
| BUG-1055 | P2 | bug | RULING | GAME | inc3 connector routes through and stacks a road on non-road tiles. Lane unlanded. |
| BUG-1057 | P2 | bug | RULING | INFRA | inc9b strict-boolean sanitizer coercion unpinned; mutant survives 13 suites. |
| BUG-1058 | P2 | bug | RULING | NOISE | inc9b test comment says 18 parts, real count 17. Comment-only. |
| BUG-1059 | P2 | bug | RULING | INFRA | inc9b port obligation: TRAFFIC_SNAPSHOT_KEY_ORDER half undone. Lane predates BUG-974. |
| BUG-1064 | P2 | bug | DONE? | ENABLER | Landed in 816f9bb (r4 ACCEPT); relocate now calls `rescheduleSectorCompletion`. |
| BUG-1065 | P2 | bug | DONE? | ENABLER | Module-global `completionSchedule` now only in comments; schedule is per-state side-band. |
| BUG-1066 | P2 | bug | DONE? | INFRA | `baseStateOf` WeakMap chain removed from code; pinned in 816f9bb. |
| BUG-1067 | P2 | bug | DONE? | INFRA | advance() unconditional `markSectorsDirty([])`; pinned in 816f9bb. |
| BUG-1068 | P2 | bug | DONE? | ENABLER | Schedule sites now at `engine.ts` 7282, 10032, 11131 plus relocate. Confirm no site missed. |
| BUG-1069 | P2 | bug | DONE? | INFRA | `new Set(pendingDirty)` no longer present in `sectorPartition.ts`. |
| BUG-1071 | P2 | bug | RULING | GAME | Ladder-keyed row still leaks integration reward by population. Tied to the 1011 ruling. |
| BUG-1075 | P2 | bug | STRONG | INFRA | GR#3 duplication of the leak-consequence predicate; no behaviour defect today. |
| BUG-1079 | P2 | bug | RULING | GAME | inc3 pass-2 strands the player's own drag tile. Minimal repro. Lane unlanded. |
| BUG-1080 | P2 | bug | RULING | INFRA | inc3 connector major-tile block has no test; mutant named. Lane unlanded. |
| BUG-1081 | P2 | bug | RULING | INFRA | inc3 headline assertion untested; mutant M14 removes the whole pass. Lane unlanded. |
| BUG-1084 | P2 | bug | DUP | GAME | Same population-sensitive reward as BUG-1071, found again in round 5. |
| BUG-1091 | P2 | bug | DONE? | ENABLER | 816f9bb: "BUG-1091 fixed". Savepoint-restored schedule rebuild. |
| BUG-1092 | P2 | bug | DONE? | INFRA | Structural-guard half of the r4 ACCEPT; I did not find the test itself. Unverified. |
| BUG-1096 | P2 | bug | STRONG | INFRA | `health.go:103` still reports `TicksCompleted()`, not the engine clock. |
| BUG-1098 | P2 | bug | STRONG | INFRA | Named CI flake with run id and test line; the fix (isolate the git index) is implicit. |
| BUG-1101 | P2 | bug | STALE | NOISE | bill/bro lane worktrees were removed in the 2026-10-06 declutter. Close. |
| BUG-1111 | P2 | bug | WEAK | INFRA | Forge asset bug; it belongs in SAFE's BOW. No Metropolis gate or failure named. |
| BUG-1117 | P2 | bug | DONE? | INFRA | f54ec13: duration-matched sampling and a two-exceed rule. Confirm CI stays green. |
| BUG-1119 | P2 | bug | STRONG | INFRA | `claude-db.js:36` still defaults and accepts any `METRO_DB_NAME`. |
| BUG-1125 | P2 | bug | RULING | ENABLER | 27 unwired engine packages; wire, park or delete is Aaron's call per package. |
| BUG-1126 | P2 | bug | RULING | ENABLER | 9 of 14 screens unwired. Partly changed since: fa56d78 wired F3, F10 and F12. |
| BUG-1127 | P2 | bug | WEAK | INFRA | Edges partly registered (1113c8b). The path-existence lint is still unspecified. |
| BUG-1129 | P2 | bug | WEAK | ENABLER | Blocks MOD-040 AC-6; fields listed but no ruling on materials or units. |
| BUG-1130 | P2 | bug | WEAK | INFRA | Register engine.freight to engine.invariant edge; the gate (edge-lint) is not named. |
| BUG-1131 | P2 | bug | DUP | INFRA | Same registry fix as BUG-1130 for market and logistics. Merge them. |
| BUG-1135 | P2 | bug | STRONG | ENABLER | fa56d78 shipped F10 list-only and descoped save/load to this item. |
| BUG-1137 | P2 | bug | DONE? | INFRA | `engine.ts:7121` and `sectorPartition.ts:688` carry "BUG-1137 (r4)" fixes. |
| BUG-1140 | P2 | bug | STRONG | INFRA | Compare passes with zero evaluated ticks. A gate that cannot evaluate must not pass. |
| BUG-1143 | P2 | bug | STRONG | GAME | Exact repro: seed 4103, 3 months, Save, Load, advance 1 month. |
| BUG-1152 | P2 | bug | WEAK | GAME | Title only. Consolidation dips online capacity (AC-18). b44347c left its rungs untouched. |
| BUG-1153 | P2 | bug | WEAK | GAME | Title only. Glide-mode health/school consolidation runs once a year. |
| BUG-1158 | P2 | bug | WEAK | INFRA | Title only. MET-P010 on a stale engine; note says "correct", so the ask is unclear. |
| BUG-1162 | P2 | bug | DONE? | GAME | b127698 "folds BUG-1162" (palette tree). |
| BUG-1165 | P2 | bug | WEAK | GAME | Title only. Fix copy names the cheaper option, builds the dearer one. |
| BUG-1169 | P2 | bug | WEAK | GAME | Title only. Fix re-buys fleet while offline providers exist; split from BUG-1164. |
| BUG-1171 | P2 | bug | WEAK | ENABLER | Title only. Layout transaction upkeepDelta 0 vs cumulative field disagree. |
| BUG-1174 | P2 | bug | WEAK | ENABLER | Title only. Re-plan "served" rule weaker than `computeRoadConnectivity`. |
| BUG-1178 | P2 | bug | STRONG | INFRA | `citizen_paging.go:1009-1015` is still a plain truncating `os.WriteFile`. |
| BUG-1179 | P2 | bug | WEAK | INFRA | Title only. Budget hook cites another agent's BUDGET line. |
| BUG-1181 | P2 | bug | DONE? | INFRA | 569667c: savepoint index serves unchanged saves with zero decodes. |
| BUG-1186 | P2 | bug | DONE? | GAME | b44347c removes the 4 lossy rungs (117 to 113); mid-save load tested. |
| BUG-1189 | P2 | bug | DONE? | INFRA | accea4b repointed MariaDB 13.0 paths. The x/sys and x/text bump is unverified. |
| BUG-1194 | P2 | bug | STRONG | GAME | One-line label fix, gated on UX-2's formatMixText. |
| BUG-1198 | P2 | bug | DONE? | INFRA | 779d10b removed `allColdRecordsLocked`; only a comment remains. |
| BUG-1199 | P2 | bug | WEAK | INFRA | Placeholder secret on the Azure create path; cost-gated, fix named but no check. |
| BUG-1208 | P2 | bug | WEAK | INFRA | Title only. Abandoned SIGTERM build can write after claim release (BUG-1197 residual). |
| BUG-215 | P2 | bug | WEAK | INFRA | Destructive agents ran `git checkout --`. Incident named; no gate or regression test. |
| BUG-230 | P2 | bug | WEAK | INFRA | Vacuous guard tests; names two, not a list. |
| BUG-247 | P2 | bug | RULING | INFRA | The 193 KB `errors.json` friction is a design decision (split or accept). |
| BUG-251 | P2 | bug | STALE | NOISE | Review attributed to the wrong plan items; the code is complete. Paperwork only. |
| BUG-257 | P2 | bug | NONE | NOISE | An audit narrative of five guards with no defect or gate. Close or split out findings. |
| BUG-258 | P2 | bug | DUP | INFRA | Umbrella for the BUG-259 to 262 audit findings. |
| BUG-259 | P2 | bug | DONE? | INFRA | The compose package is registered as `feat.compositionroot` in `code.json`. |
| BUG-260 | P2 | bug | WEAK | INFRA | ui.diagrams and foundation.num contract-name mismatches. Mismatches named, gate not. |
| BUG-261 | P2 | bug | DUP | INFRA | Unregistered Go imports. Same class as BUG-1127 and BUG-367. |
| BUG-262 | P2 | bug | DUP | INFRA | 35 stale edges. Same class as BUG-367. |
| BUG-289 | P2 | bug | RULING | ENABLER | Month atomicity: options brief 2dac185 is waiting on Aaron. |
| BUG-309 | P2 | bug | STRONG | INFRA | `add-error.js:503-514` sandwich slip; mechanism and lines named. |
| BUG-338 | P2 | bug | STRONG | INFRA | Startup summary shows the main checkout's git state to every worker. |
| BUG-351 | P2 | bug | STRONG | INFRA | Secret guard scans all staged bytes, not the introduced diff. |
| BUG-352 | P2 | bug | STRONG | NOISE | CLAUDE.md roster contradicts itself in 3 named places; still true today. |
| BUG-360 | P2 | bug | STRONG | INFRA | BUG-354 residual: ambient WINDOW_ID auth for read/message/claim. |
| BUG-361 | P2 | bug | STRONG | INFRA | 5 named guard-era commits with REJECT or no verdict. Clear action. |
| BUG-363 | P2 | bug | STRONG | INFRA | Unit symbol drift, five spellings; named. |
| BUG-365 | P2 | bug | STRONG | INFRA | GWh/GL unregistered; units-lint blind spots listed. |
| BUG-366 | P2 | bug | STRONG | ENABLER | `DashAPI` has no exported symbol; this is the built-but-not-wired class. |
| BUG-367 | P2 | bug | DUP | INFRA | 248 over-registered and 86 under-registered edges. Overlaps BUG-1127; merge. |
| BUG-371 | P2 | bug | STRONG | INFRA | Month-restore clobbers Pause/SetSpeed; fix named (restore only the tick field). |
| BUG-373 | P2 | bug | STRONG | GAME | Tax SetRate after SetDistrictMultiplier drifts below `rateRange`; exact numbers. |
| BUG-378 | P2 | bug | STRONG | ENABLER | `attractHook.ApplyEffect` swallows an error mid-tick. Line numbers have drifted. |
| BUG-379 | P2 | bug | STRONG | ENABLER | `migration.go:437` still returns 0 on a copy-guard hit. |
| BUG-387 | P2 | bug | STRONG | INFRA | Destructive-guard recency rule self-blocks legitimate second commits. |
| BUG-426 | P2 | bug | STALE | NOISE | The HMR class was retired by the bundle serving mode (84f5201). |
| BUG-432 | P2 | bug | DONE? | INFRA | CLAUDE.md no longer claims a spec guard. Close, or file a build item for one. |
| BUG-441 | P2 | bug | WEAK | INFRA | 469 aspirational citation pairs need BA triage; 42 edges registered (b0d2748). No criterion. |
| BUG-450 | P2 | bug | STRONG | GAME | Mutation named: constant `MOVE_OUT_BASE_RATE` kills zero tests. |
| BUG-454 | P2 | bug | STRONG | GAME | Net-90 settlement silently writes off unpayable debt; needs inc3 debt mechanics. |
| BUG-463 | P2 | bug | STRONG | GAME | Sankey over-reports net growth at capacity; measured, exact numbers. |
| BUG-465 | P2 | bug | STRONG | GAME | Click-to-place zooms the map; the expected behaviour is stated. |
| BUG-471 | P2 | bug | DONE? | INFRA | 095a0d7: "BUG-471 FIX" normalises protocolVersion in the shim. |
| BUG-489 | P2 | bug | STRONG | GAME | PowerTab import-cost hint ignores austerity (20 vs 18). |
| BUG-518 | P2 | bug | RULING | GAME | Wellbeing moves people out but not in. Needs a design ruling. |
| BUG-522 | P2 | bug | NONE | GAME | No description. Education to outcome unwired; needs a ruling first. |

## Improved criteria

### BUG-1111 forge codename scan ships Metropolis patterns
1. A project with no codename list gets an inert scan. A fixture project with no `GR#22` config must exit 0 and report "inert: no list".
2. Gate: a test builds a temp project, runs `harness/claude-codename-patterns.js` on text that holds a Metropolis pattern, and asserts no hit.
3. Failure caught: any Metropolis literal in the shipped patterns file goes red.
4. Owner is SAFE/forge, so re-file this item in SAFE's BOW.

### BUG-1127 code.json path drift
1. A lint step fails when any module `path` in `code.json` does not exist on disk. Output lists the offenders.
2. Baseline: the 36 stale `tool.*` paths are repointed to `harness/` and the 33 missing BOW mkeys are registered or exempted by name.
3. Gate: `node tools/plan/generate.js --check` plus the new lint is exit 0 in CI.
4. Failure caught: renaming a registered file without updating the plan.

### BUG-1129 world.Cell lacks fire-spread fields
1. Ruling first: which of material, density, wind and hydrant live on `Cell` versus a side table. Aaron decides; `types.go:133` today has none of them.
2. Once ruled, a determinism test serialises and restores a Cell with the new fields byte-identically.
3. Gate: the MOD-040 AC-6 fire-spread test turns from skipped to passing.

### BUG-1130 and BUG-1131 conservation edges
1. `code.json` registers engine.freight, engine.market and engine.logistics edges to engine.invariant.
2. Gate: `edge-lint` reports 0 new findings and the AC-11 tonnes-conserved registration succeeds.
3. Failure caught: the invariant check silently never sees a goods leg.

### BUG-1152 consolidation dips online capacity (title only)
1. During a successor's construction window, the old building's online capacity is unchanged, or the dip is documented and bounded.
2. Gate: a test consolidates under construction and reads online capacity every tick across the window.
3. Failure caught: AC-18 broken operation, capacity below the pre-merge value at any tick.

### BUG-1153 Glide-mode consolidation once a year
1. Health/school consolidation fires on the same cadence as the community passes, not only at `isMonth12Boundary`. The cadence (monthly vs yearly) needs Aaron's confirmation, since the title implies a mismatch.
2. Gate: a 24-month run counts consolidation passes and compares them with the cadence the other passes use.

### BUG-1158 stale local engine rejects the fresh client
1. Reproduce: engine build `a2ee8ea`, client `1113c8b`, handshake reply MET-P010.
2. Expected: the reply names the fix (restart the engine or reload) in the player-facing message. State the intended text.
3. Gate: wsserver handshake test with mismatched build strings asserts the code and the remedy text.

### BUG-1165 Fix copy names the cheaper option, builds the dearer
1. The label text and the placed spec come from one value. Gate: with the fusion 1.28bn example, the copy equals the placed spec's name and cost.
2. Failure caught: any divergence between copy and action.

### BUG-1169 Demand Fix re-buys a fleet while offline providers exist
1. When finished-but-offline providers can cover the gap (26 road-gated), Fix places 0 new providers, or only the shortfall.
2. Gate: `demandFixPlan` unit test with 26 offline providers expects 0 purchases and an advice line naming the road gate.
3. Failure caught: a Fix that spends money the player already spent.

### BUG-1171 and BUG-1174 consolidator accounting
1. 1171: a layout transaction's `upkeepDelta` equals the change in `consolidatorLayoutCumulativeUpkeepDelta`. Gate: assert equality across a layout and its undo.
2. 1174: the re-plan "served" rule delegates to `computeRoadConnectivity`'s rule. Gate: a tile on or next to a plan tile but unconnected is not served.

### BUG-1179 budget hook cites another agent's BUDGET line
1. The hook reads the BUDGET line from the dispatching agent's own prompt keyed by agent id.
2. Gate: two concurrent agents with 15m/45k and 45m/130k; neither is stopped under the other's limits.

### BUG-1199 azure-deploy placeholder secret
1. The create path seeds `metroserve-shared-secret` from `METROSERVE_SHARED_SECRET` (masked step env) and fails the job if it is empty.
2. Gate: a workflow lint test greps for `REPLACE-BEFORE-FIRST-RUN` and fails if found. No cloud spend is needed.
3. Failure caught: a deployed app running with a known placeholder secret.

### BUG-1208 abandoned SIGTERM build writes after claim release
1. After the claim is released, the abandoned build's page-file writes either fail or no-op.
2. Gate: a test starts a slow build, times the SIGTERM out, releases the claim, lets the build finish, and asserts the page directory is unchanged.

### BUG-215 destructive agents run `git checkout --`
1. The PreToolUse guard denies `git checkout -- <path>` and `git restore` for subagents, including the dispatch prompts.
2. Gate: guard unit tests with both commands, expecting a deny.
3. Safe replacement: tell agents to restore from a scratch copy they made.

### BUG-230 vacuous guard tests
1. List every guard fix lacking a test (BUG-015 NUL separator, FEAT-037 sweep, plus a grep-built list).
2. Each gets a test that fails when the fix is reverted. Prove it by mutating the fix once.

### BUG-260 contract-name mismatches
1. `ui.diagrams` and `foundation.num` inbound contract names resolve to real exported symbols, or the plan is corrected.
2. Gate: `spec-lint` / `codejson-audit` reports 0 "contract-does-not-resolve" for both.

### BUG-441 aspirational spec pairs
1. BA triage output: each of the roughly 469 pairs is tagged keep, drop or register. Batch by module.
2. Gate: `spec-lint` finding count drops to the tagged "keep" set, with a stated number.

### BUG-522 education to outcome (NONE)
1. Ruling first: does a citizen carry education or skill state? What outcomes does it drive (wage, job eligibility, crime)?
2. Once ruled, a deterministic test runs two cities that differ only in school coverage and expects different outcomes in the stated direction.
3. Failure caught: schools that raise only a wellbeing number.

### BUG-257 audit narrative (NONE)
1. Either close it, or split each concrete guard defect into its own item with the defect, location and test.
