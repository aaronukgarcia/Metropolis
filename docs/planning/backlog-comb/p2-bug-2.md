# Backlog comb: p2-bug-2 (103 items)

## Summary

Class counts: WEAK 39, RULING 14, DONE? 6, STRONG 42, NONE 2.

Game-value counts: INFRA 45, GAME 23, ENABLER 32, NOISE 3.

Depth note: every item got a title-and-description look plus a git log grep on its code; a subset got a Grep check of named symbols (537, 556, 627, 632, 767, 830, 552, 869, 574). Others are judged from text only.

Five most worth doing next:
1. BUG-782 - migration and council tax blind to employment (capture 13); a direct Baseline One loop break, needs expected-behaviour criteria first.
2. BUG-818 then BUG-813 - rail/motorway runs ending in open ground likely cause the road-component split; both have exact probes.
3. BUG-854 - traffic delay uses the wrong divisor (v/c 1.82x high, BPR delay 10.9x); fix and data source are named.
4. BUG-767 - net worth badge shows 1000x too small; one-line fix still present at LiveEngineBadge.tsx:147.
5. BUG-774 (with BUG-771) - no game-over and no halt on module failure; needs Aaron policy, then a small build.

Close or merge:
- Close as junk: BUG-836, BUG-837 (title "P2", no body).
- Verify then close as DONE?: BUG-552, BUG-672, BUG-794, BUG-799, BUG-830, BUG-869.
- Merge into one edge-registration sweep (prove with /edge-lint): BUG-560, BUG-561, BUG-562, BUG-699.
- Fix together as one BFS-flood item: BUG-847, BUG-857, BUG-864.
- BUG-673: its "inert" premise is stale (restore path exists since 2bd94ad), so raise urgency.
- BUG-675, BUG-809 and BUG-667 are latent/unreachable; candidates to close as accepted.

Rulings Aaron owes: BUG-539 (XP pacing), BUG-698 (Docker reinstall vs amend CLAUDE.md), BUG-751 (wellbeing output boost cap), BUG-753 (capex ceiling vs rail), BUG-770 (liveEngine default-on), BUG-773 (layout upkeep share), BUG-774 (game-over policy), BUG-785 (tax rebalance), BUG-789 (airport jobs), BUG-792 (tax on filled jobs), BUG-793 (layout treasury floor), BUG-796 (consolidation upkeep ladder), BUG-797 (early-game capex floor); BUG-771 halt policy (lead/Aaron).

## Items
| Code | P | Type | Class | Value | Note (<= 25 words) |
|---|---|---|---|---|---|
| BUG-537 | P2 | bug | WEAK | INFRA | File exists (webconsole/test/austerity-checks.test.mjs); no hang cause, no bound, no gate stated. Needs a time cap and root cause. |
| BUG-539 | P2 | bug | RULING | GAME | XP pacing: Aaron must set target hours to level 20 and whether +1 XP/tick stays unconditional. |
| BUG-545 | P2 | bug | WEAK | GAME | Wording and Reload/dismiss behaviour stated; no test or observable check for banner states. |
| BUG-552 | P2 | bug | DONE? | INFRA | harness/claude-devfeedback-import.js:101 now has a sourceMkey field. Verify FEAT-066 note attributes to feat.metricsdash, then close. |
| BUG-553 | P2 | bug | WEAK | INFRA | Wants second detection layer; no measurable target (evasion rate) or corpus. Candidate for close as accepted residual risk. |
| BUG-556 | P2 | bug | WEAK | INFRA | Real AC/code mismatch. Needs decision: add core.Navigate API or amend ui.alerts AC-3/AC-7 to the Effects seam. |
| BUG-560 | P2 | bug | WEAK | INFRA | Three missing edges listed; no gate. Merge with 561/562/699 into one edge-registration sweep proven by /edge-lint. |
| BUG-561 | P2 | bug | WEAK | INFRA | Single edge engine.finance to foundation.data; confirm still unregistered with /edge-lint, fold into 560 sweep. |
| BUG-562 | P2 | bug | WEAK | INFRA | Residual edge list for farming/mining/refuse; duplicate class of 560. Fold into one sweep. |
| BUG-574 | P2 | bug | WEAK | GAME | Player-visible label overstates; fix choice (label vs coverage) undecided, no test. data.ts:7382 still says -7% utility and service upkeep. |
| BUG-598 | P2 | bug | WEAK | ENABLER | Has measurements (+20pc tick) but no target or gate. Needs a perf pin at 2971 buildings and a compute-once check. |
| BUG-612 | P2 | bug | WEAK | INFRA | Process lesson, not a build item. Needs an enforceable guard or close as a documented rule. |
| BUG-627 | P2 | bug | WEAK | INFRA | backend.ts:414 still uses code ?? MET-V803. Names defect and sites; lacks test and the named MET codes. |
| BUG-632 | P2 | bug | WEAK | ENABLER | Structural invariant wanted; no test. nextSafeBuildingId exists (engine.ts:968). Need property test across all write boundaries. |
| BUG-639 | P2 | bug | STRONG | ENABLER | Repro already parked as skip; expected re-stamp-on-resolution behaviour named (BUG-468). Unskip as the gate. |
| BUG-647 | P2 | bug | WEAK | GAME | Advisor basis undefined; BUG-641 (c9d0ccf) touched the sized advisor. Verify still live, then define normalised basis plus fixture. |
| BUG-661 | P2 | bug | WEAK | ENABLER | Wanted banner stated but no fixture or message code. Needs replay fixture with cap-exceeding placement. |
| BUG-667 | P2 | bug | WEAK | ENABLER | Four readers named, no test. Grandfathered zero-wage airport case is narrow; low priority. |
| BUG-670 | P2 | bug | STRONG | INFRA | Named failing test (config-confirm.test.tsx), cause and expectation clear. Fix is to wrap in SimProvider and put it in a CI suite. |
| BUG-672 | P2 | bug | DONE? | ENABLER | Commit 5f1ad92 [BUG-672]: restore path already existed, now loud (MET-V869) with six pins and round ACCEPT. Close. |
| BUG-673 | P2 | bug | WEAK | ENABLER | Premise "inert" is stale: restore path exists (5f1ad92), so the race is live. Fix named; needs a concurrency test. |
| BUG-675 | P2 | bug | WEAK | NOISE | Latent and "unreachable today". Needs round-trip test or close. |
| BUG-676 | P2 | bug | WEAK | ENABLER | f89dbe2 (glide landed) mentions BUG-676; unverified whether the NaN guard shipped. Check, else add corrupted-savepoint test. |
| BUG-678 | P2 | bug | WEAK | ENABLER | Single missed mirror call; no test that the self-heal save reaches IndexedDB. |
| BUG-688 | P2 | bug | WEAK | GAME | NewsFeed.tsx still only reads notices. Choose Clear-dispatch or auto-clear; add test. |
| BUG-690 | P2 | bug | WEAK | ENABLER | Decode accepts impossible state; expected sanitise vs reject unstated, no fixture. |
| BUG-697 | P2 | bug | WEAK | ENABLER | Defect is one phrase (safeUint32 on partner ids); no file, no repro. Needs location and a boundary test. |
| BUG-698 | P2 | bug | RULING | INFRA | Docker exe absent on disk. Aaron: reinstall, or amend CLAUDE.md and route image proofs via CI. |
| BUG-699 | P2 | bug | WEAK | INFRA | Edge-registration batch again (4 edges, 3 orphan dirs, DashAPI). Merge with 560 sweep; details live in BUG-327 comments. |
| BUG-702 | P2 | bug | WEAK | GAME | Two small polish asks (warning severity, dedup disambiguation); needs notice text fixtures. |
| BUG-705 | P2 | bug | STRONG | INFRA | Clear fix (checkout clears session_id, deletes key file). Gate is implicit: test that the ping hook cannot resurrect. |
| BUG-710 | P2 | bug | WEAK | INFRA | Two perf reds named, fix idiom named (BUG-694). 6865bf4 de-flaked three estates; confirm these two covered. |
| BUG-727 | P2 | bug | STRONG | GAME | Fixture, wrong output, fix, and pinned todo RR3-7 all stated. |
| BUG-729 | P2 | bug | STRONG | INFRA | Exact hole, seven e2e cases, and fix stated. Gate: those cases must now require a verdict. |
| BUG-751 | P2 | bug | RULING | GAME | Aaron: may wellbeing boost output above x1.0, and at what cap. |
| BUG-753 | P2 | bug | RULING | GAME | Aaron: relax R8-3, tier-specific ceiling, or accept no rail under 112M treasury. |
| BUG-754 | P2 | bug | WEAK | ENABLER | Symptom measured at 1bn but no target. Needs networkComponents non-increasing assertion across passes. |
| BUG-766 | P2 | bug | WEAK | ENABLER | Fix clear (post-increment tick in advance()); no regression test named for the boundary-tick upkeep. |
| BUG-767 | P2 | bug | STRONG | GAME | LiveEngineBadge.tsx:147 still divides by 1_000_000. Expected value derivable from Money unit; add one unit test. |
| BUG-770 | P2 | bug | RULING | GAME | Aaron: when does liveEngine default ON; until then nothing live reaches the default config. |
| BUG-771 | P2 | bug | RULING | ENABLER | Aaron/lead: halt policy when a phase hook errors. 509546c (BUG-713) made it loud only. |
| BUG-772 | P2 | bug | WEAK | GAME | Notice re-fires on unrelated source change; needs a tick-sequence test. No commit fixes it. |
| BUG-773 | P2 | bug | RULING | GAME | Aaron: layout lifetime upkeep share (currently 0.5 of income). |
| BUG-774 | P2 | bug | RULING | GAME | Aaron: game-over policy (halt, stop, modal). No Baseline One loop closure without it. |
| BUG-780 | P2 | bug | WEAK | ENABLER | Silent drop named at store.tsx ~1323; expected recordError code and a fixture with valid metadata but bad snapshot. |
| BUG-782 | P2 | bug | WEAK | GAME | Strong evidence (capture 13) but no expected behaviour. Migration should respond to jobs; core Baseline One bug. |
| BUG-783 | P2 | bug | WEAK | ENABLER | Warning count measured; list not identified. Gate: zero MET-V804 at 9.5M city. |
| BUG-784 | P2 | bug | WEAK | INFRA | Budget 2.0MB vs 16-24MB payloads; pick chunk vs raise; gate is a 24MB capture landing first time. |
| BUG-785 | P2 | bug | RULING | GAME | Aaron: rebalance who pays tax after office tax scoping drops 34 percent. |
| BUG-786 | P2 | bug | WEAK | ENABLER | Profile shares given but no budget. Needs tick-time target at 38k buildings and memo test. |
| BUG-789 | P2 | bug | RULING | GAME | Aaron: airport jobs catalogue balance (76,000). |
| BUG-790 | P2 | bug | STRONG | INFRA | Either pin with a reaching fixture or remove; mutant test is the gate. |
| BUG-792 | P2 | bug | RULING | GAME | Aaron: should Institutional Tax scale with filled jobs (like wages) not capacity. |
| BUG-793 | P2 | bug | RULING | GAME | Aaron: confirm or retune placeholder floor 0.75 for layout ON/OFF treasury ratio. |
| BUG-794 | P2 | bug | DONE? | ENABLER | inc4 landed in ac05b5b with ruling "box dwells until converged". Verify civic phase reached at runtime, then close. |
| BUG-796 | P2 | bug | RULING | GAME | Aaron: ladder upkeep and scrap-value balance (consolidation saves no upkeep). |
| BUG-797 | P2 | bug | RULING | GAME | Aaron: should early-game capex floor buy a longer run or fire more often. |
| BUG-799 | P2 | bug | DONE? | INFRA | Commit 8209164 fixes with OS advisory lock claim takeover. Close after CI evidence. |
| BUG-800 | P2 | bug | WEAK | INFRA | Pin wanted where cap does not bind; fixture shape described, no gate command. |
| BUG-802 | P2 | bug | WEAK | ENABLER | Measured 15.8ms per call; no target. Gate: main-thread diff cost under stated ms at 38k buildings. |
| BUG-803 | P2 | bug | WEAK | ENABLER | Measured 217KB per placement tick; no target. Gate: placement tick payload bytes bound. |
| BUG-804 | P2 | bug | WEAK | ENABLER | 100M-citizen paging path; options listed but no target or memory ceiling. Supports 100m finish line. |
| BUG-806 | P2 | bug | WEAK | INFRA | Two issues bundled (mutant survives, dead flows path); split and name fixtures. |
| BUG-807 | P2 | bug | STRONG | ENABLER | Fix and pin named (modest-treasury wide city never below INSOLVENCY_WARNING_THRESHOLD). File named. |
| BUG-808 | P2 | bug | STRONG | ENABLER | Fix and pin named (injected orphan removed within one pass). |
| BUG-809 | P2 | bug | STRONG | INFRA | Fix and pin named (foldTick); only reachable out-of-order, so low value. |
| BUG-812 | P2 | bug | STRONG | ENABLER | Pin is E2 assertion in feat-inc4-reimagine-engine.test.mjs; two fix candidates, lead picks. |
| BUG-813 | P2 | bug | STRONG | ENABLER | Probe-first plan; pin is the skipped E8 never-rises test. Likely producer is BUG-818. |
| BUG-817 | P2 | bug | STRONG | INFRA | Fixture shape specified (funds just above threshold plus one civic unit). |
| BUG-818 | P2 | bug | STRONG | ENABLER | Exact probe and fix rule given; likely BUG-813 producer. Do first. |
| BUG-820 | P2 | bug | STRONG | INFRA | 14 survivors listed with mutants; gate is the harness. Data-validator hardening. |
| BUG-821 | P2 | bug | STRONG | INFRA | Three rows, arithmetic contradiction, expected UK figures. Data fix, gate by validator. |
| BUG-822 | P2 | bug | STRONG | INFRA | Missing bands and mixed schedules named; gate is lookup resolving all CO2 values. |
| BUG-825 | P2 | bug | STRONG | INFRA | Test and line named, fix pattern given (inject sweep trigger / fake clock). |
| BUG-828 | P2 | bug | STRONG | INFRA | Proven aliasing repro with expected copy; Go and TS both. |
| BUG-829 | P2 | bug | STRONG | INFRA | Three proven outcomes; expected registry error at load. |
| BUG-830 | P2 | bug | DONE? | INFRA | participant_test.go:54 now classifies "ladder" with BUG-830 comment. Close. |
| BUG-831 | P2 | bug | STRONG | INFRA | Mutant stated, both languages; gate is a sort-order assertion. |
| BUG-832 | P2 | bug | STRONG | INFRA | Mutant and tightening stated. |
| BUG-833 | P2 | bug | STRONG | INFRA | Half-ulp input 0.49999999999999994 identified as the discriminator. |
| BUG-834 | P2 | bug | STRONG | INFRA | Needs a fixture with equal length, wrong per-index populations. |
| BUG-836 | P2 | bug | NONE | NOISE | Title is literally "P2", no body, created in same second as 837. Close as junk. |
| BUG-837 | P2 | bug | NONE | NOISE | Title is literally "P2", no body. Close as junk. |
| BUG-847 | P2 | bug | STRONG | ENABLER | Measured, mechanism and bound named. Overlaps 857/864 (same BFS flood); fix together. |
| BUG-848 | P2 | bug | STRONG | INFRA | Exact mutant stated; add pin that forecast uses ladder mode share. |
| BUG-854 | P2 | bug | STRONG | ENABLER | Wrong divisor and correct data source (peakHourFactor) named; v/c 1.82x high. |
| BUG-855 | P2 | bug | STRONG | ENABLER | Dead weights proven by mutant; fix is use cumFrac. |
| BUG-856 | P2 | bug | STRONG | INFRA | Eight surviving mutants listed individually. |
| BUG-857 | P2 | bug | STRONG | ENABLER | Measured 320,883 tiles; fix: reuse inc2 BFS and clamp to map. Duplicate of 847/864 area. |
| BUG-858 | P2 | bug | STRONG | INFRA | Wrong spec id res_tower named; use res_tower_nyc. Gate: fixture yields demand tiles. |
| BUG-861 | P2 | bug | STRONG | INFRA | Mutant e still survives; pin must target consumer line. |
| BUG-862 | P2 | bug | STRONG | INFRA | Untaken if guard hides assertions; mutant given. |
| BUG-863 | P2 | bug | STRONG | INFRA | Export bprParamsFor and assert beta 4.5; three lines. |
| BUG-864 | P2 | bug | STRONG | INFRA | Two informational findings, not production-reachable. Fold with 857. |
| BUG-865 | P2 | bug | STRONG | INFRA | Three unreachable MET codes; fix shape (loadTrafficConfigFrom) given. |
| BUG-869 | P2 | bug | DONE? | GAME | emergencyResponse.ts:29 comment records BUG-869 r1 REJECT fix: access leg no longer baseAccessMinutes. Verify coverageShare responds to stations. |
| BUG-874 | P2 | bug | STRONG | INFRA | Equal-weights mutant survives; measured 2.9 minute difference. |
| BUG-886 | P2 | bug | STRONG | INFRA | Port Go tokenFormat and required-field rules into add-error.js check; test with the BUG-885 message. |
| BUG-908 | P2 | bug | WEAK | INFRA | Flake with no root cause; 19 tests failing and passing on rerun. Needs isolation hypothesis and a stress gate. |
| BUG-952 | P2 | bug | STRONG | ENABLER | Mechanism measured; fix is trafficRecomputeTicks cadence. Pin: no full traffic derivation on non-cadence ticks. |
| BUG-953 | P2 | bug | STRONG | ENABLER | Fix named (cull plus memoised lookup); pin file named. |
| BUG-954 | P2 | bug | STRONG | GAME | Misleading tooltips; fix: relabel as city-wide or make spatial. |
| BUG-955 | P2 | bug | STRONG | INFRA | Mutants listed; gate is mounting MapView with fillRect and boundary fixtures. |

## Improved criteria
Written for WEAK and NONE items where useful; many describe the gate to add. STRONG, RULING, DONE? and junk items need none. Items marked WEAK but missing here got the Note column only (title-level): .

### BUG-537 test/austerity-checks.test.mjs hangs/times out at 100s+ on unmodified 
1. Reproduce with `node tools/test/scoped.mjs webconsole/test/austerity-checks.test.mjs` and name the blocking await or loop (unverified).
2. Fix the cause or give the test an explicit per-test timeout under 30 s.
3. Gate: the file completes in under 30 s three times in a row on main; a deliberately re-introduced hang fails by timeout, not by runner kill.

### BUG-545 Staleness banner UX: says 'restart dev server' (wrong when server is l
1. Banner text reads "A newer build is available" with a Reload button; it never says "restart dev server".
2. Reload button reloads the page; dismiss hides the banner until the next different build id.
3. Gate: component test for three states (stale, dismissed, same build) asserting text and no banner on a dev commit that changes nothing player-visible (unverified how detected).

### BUG-553 Secret guard camelCase entropy exemption still allows ~15pct residual 
1. Define a corpus of N adversarial letters-only secrets and N real identifiers (incl. lineMatchesWithBoundary).
2. Second layer (dictionary-segment or character-class diversity) must block at least X percent of the adversarial corpus with zero false positives on the repo tree.
3. Gate: the secret-guard test run over the corpus; red if evasion exceeds X or any repo file false-positives. Aaron to set X, else close as accepted residual.

### BUG-556 ui.core has no Navigate/Focus/SwitchScreen or speed-control API; chrom
1. Decide: add Navigate/SwitchScreen/SetSpeed to internal/ui/core, or amend ui.alerts AC-3/AC-7 to name the chrome Effects seam.
2. If API added: Grep of internal/ui/core exports finds the symbols and chrome calls them (not a comment).
3. Gate: go test ./internal/ui/... plus a test that fails if chrome stops calling the ui.core symbol.

### BUG-560 GR#25 edge-registration cluster: cloud.azure to balance.harness, cloud
1. Register the three edges (cloud.azure to balance.harness, cloud.azure to cloud.netpolicy, cloud.netpolicy inbound INT-004) in master-plan-v2.1.json and regenerate code.json.
2. Gate: /edge-lint and /codejson-audit report zero unregistered edges for these modules.
3. Fold 561, 562 and 699 into the same sweep and verify each edge first (some may already be registered).

### BUG-561 engine.finance to foundation.data edge is unregistered, blocking the b
1. Verify with /edge-lint whether engine.finance to foundation.data is still unregistered.
2. If so register it in the master plan and regenerate code.json; gate: edge-lint clean and the borrowing_instruments.json loader (FEAT-057) imports without a registration error.

### BUG-562 GR#25 residual edge gaps post-BUG-058: farming (tourism/attract/wellbe
1. List each missing edge (farming to tourism/attract/wellbeing, mining to build/farming/refuse/traffic, refuse to invariant/news) and confirm in code each real import exists.
2. Register only edges backed by real imports or contract usage.
3. Gate: edge-lint clean for farming, mining, refuse.

### BUG-574 Recycling Mandate description overstates effect (P2): data.ts labels i
1. Choose: relabel to list the actual covered buckets, or extend the discount to fire/civic/leisure/transport.
2. Gate: test that sums upkeep per bucket with the policy on and off and asserts the label text matches the covered buckets (or all buckets drop 7 percent).
3. Failure caught: Fire upkeep unchanged while label promises -7 percent.

### BUG-598 Congestion factor recomputed 4x per tick (round follow-up): congestion
1. Compute congestionFactorOf once per tick and thread it into wellbeingOf and buildWellbeingCoreParts.
2. Gate: a call-count test asserting lineUsageOf runs once per tick, plus tick time at 2971 buildings within 5 percent of pre-congestion baseline.
3. Failure caught: the measured +20pc tick or +71pc wellbeingOf.

### BUG-612 Round-agent backup discipline (GR#24 incident 2026-09-03): attacker ch
1. Add a round-brief rule and a guard check: a backup copy must be its own command, existence-verified, before any overwrite.
2. Gate: a unit test for the guard (harness/) that refuses a chained cp-then-overwrite, red if chained.
3. Or close as documented rule in /round skill (Aaron call).

### BUG-627 GR#7: DOMException .code defeats the MET fallback - reportUnhandledRej
1. reportUnhandledRejection must use a MET code when reason is a DOMException (code is numeric 22) - treat only string MET-* codes as codes.
2. Route the 3-4 quota recordError sites in store.tsx to the real quota code (name registry code; unverified which).
3. Gate: unit test rejecting with a quota DOMException; ring entry carries MET-V803 or the quota MET code, never bare 22.

### BUG-632 nextId uniqueness is call-site discipline not a structural invariant (
1. Every write to state.buildings goes through a helper that re-establishes nextId >= max id + 1.
2. Gate: property test over each write boundary (hydrate, import, consolidator, undo) asserting place never mints a duplicate id.
3. Failure caught: two buildings sharing one id.

### BUG-647 worstAnyDemandFix compares incomparable units - a commercial demand IN
1. Verify the defect still exists after BUG-641 (c9d0ccf) by test: city with commercial demand index 100 and hospital gap of 45 people.
2. Advisor ranks by a normalised basis (named in code, e.g. shortfall as share of need).
3. Gate: fixture asserting hospital recommended before shops; red on the old comparison.

### BUG-661 Resume tail-replay refuses a cap-exceeding journalled placement with N
1. Tail-replay completion with a cap-exceeding journalled placement records a durable notice, same text/severity as the hydrate over-cap banner.
2. Gate: replay fixture with one over-cap placement; assert notice present after load and survives reload.

### BUG-667 jobsOverride:0 coherence gap - totalJobs honours it but feederTrafficW
1. With jobsOverride:0, feederTrafficWeight, fittingTier, densityTier and profile use 0 jobs.
2. Gate: test a grandfathered airport at jobsOverride 0: displayed Jobs 0 and road load equals a non-employer.

### BUG-673 IndexedDB mirror crash-consistency race across overlapping mirrorSaveC
1. Overlapping mirrorSaveCheckpoint calls cannot leave an older value as the last IndexedDB write.
2. Fix: per-key mutex or monotonic saveSeq stamp rejected on write.
3. Gate: extend attack-indexeddb-round FINDING 2/2b into a green test; mutant removing serialization turns it red.

### BUG-675 ConsolidationRecord does not round-trip jobsOverride or footprintW/H/s
1. ConsolidationRecord round-trips jobsOverride, footprintW, footprintH and scaleLocked through consolidatorUndo.
2. Gate: undo test on a grandfathered six-spec building asserts jobs unchanged; or close as unreachable.

### BUG-676 Non-numeric consolidatorSectionMetres in a corrupted savepoint bypasse
1. First verify against f89dbe2 whether a NaN backfill guard exists.
2. If not: a non-numeric consolidatorSectionMetres in a savepoint hydrates to the default, mirroring cumulativeCapexSpent.
3. Gate: corrupted-savepoint fixture; glide grid is finite after hydrate.

### BUG-678 BUG-617 self-heal savepoint write skips mirrorSavepointCheckpoint - ev
1. The BUG-617 self-heal savepoint write calls mirrorSavepointCheckpoint.
2. Gate: test that a self-heal after large-tail replay writes the savepoint to the fake IndexedDB; red if the mirror call is removed.

### BUG-688 NewsFeed orphaned dismissNotice/dismissMilestoneNotice - notice fields
1. Choose behaviour: Clear dispatches dismissNotice/dismissMilestoneNotice, or notices auto-clear after N ticks.
2. Gate: test that after Clear (or N ticks) the Alerts tab shows no stale row; red with the old orphaned state.

### BUG-690 citizens wire decode lacks impossible-state validation: a citizenID in
1. Citizens wire decode rejects (or sanitises, to be stated) a citizenID present in both Pending and RealisedAt with a registry error.
2. Gate: decode fixture with the duplicate; realised sequence not duplicated.

### BUG-697 BUG-529 follow-up undertracked: partner-id safeUint32 truncation issue
1. Locate the partner-id safeUint32 use in fertility (unverified: grep safeUint32 under internal/engine/citizens).
2. Partner ids above uint32 must error or widen, never truncate or saturate.
3. Gate: boundary test at 2^32 and 2^32+1.

### BUG-699 codejson-audit live run 2026-09-05: 4 PROD unregistered import edges +
1. Register the four unregistered PROD edges and adjudicate the three orphan dirs (keep and register, or delete with token) per BUG-327 comments.
2. Fix DashAPI contract so it resolves.
3. Gate: /codejson-audit clean; fold into the 560 sweep.

### BUG-702 placeNotice polish (BUG-490 round F2/F3): occupied/OOB severity should
1. Occupied and out-of-bounds placement rejections use warning severity.
2. Identical consecutive rejections include coords or blocker so they do not dedup silently.
3. Gate: two rejections at different tiles yield two distinct notices.

### BUG-710 Two pre-existing perf reds exposed by the first completing --webconsol
1. Rewrite the ATTACK e2 cache-benefit test and the 6-predicate detection loop on the BUG-694 de-flake idiom (counts, not wall clock).
2. Gate: --webconsole-ci passes 5 consecutive runs under CPU load.

### BUG-754 Consolidator layout: networkComponents RISE across passes at 1bn becau
1. Across consolidator passes at 1bn, networkComponents is non-increasing.
2. Placement extends existing runs (border-aware) instead of opening virgin sections.
3. Gate: multi-pass fixture asserting the series is monotone; red on today's rise.

### BUG-766 advance() computes flows with the PRE-increment tick, charging a bound
1. advance() passes the post-increment tick to flow computation.
2. Gate: a building completing at tick T is charged upkeep from T, not T-1; red on the pre-increment code.

### BUG-772 NewsFeed consolidator capacity-unknown notice re-fires on any unrelate
1. The capacity-unknown notice fires once per cause.
2. Gate: tick-sequence test where an unrelated source changes later; no second notice.

### BUG-780 store.tsx boot-freshness effect drops a mirror whose metadata is valid
1. When mirror metadata is valid but prepared.success/state/tail fails, the freshness effect calls recordError with a registry MET code (new if needed).
2. Gate: boot fixture with valid metadata and a bad snapshot; assert exactly one error entry and that local state is kept.

### BUG-782 Capture 13: 9.48M citizens with jobs=0 (no shops/offices/industry), bu
1. Migration move-ins per tick depend on available jobs (or job-adjusted attractiveness); a city with 0 jobs gets no net inflow.
2. Council tax share cannot exceed a stated cap when business and freight tax are 0 (Aaron to set cap).
3. Gate: headless fixture from capture 13 shape (shops 0, jobs 0): move-ins fall to near zero within N ticks; red on 47k/tick.

### BUG-783 Capture 13: React duplicate-key warning fired 3,826 times (MET-V804) i
1. Identify the list producing MET-V804 duplicate keys at 9.5M scale and key it uniquely.
2. Gate: render the list from the capture-13 state in a test with console error capture; zero duplicate-key warnings.

### BUG-784 Debug sink: six consecutive captures 09-03..09-04 dropped with MET-V85
1. Debug sink accepts captures of at least 24MB (chunking or raised budget).
2. Gate: a 24MB capture lands on first attempt with no MET-V854; if chunked, reassembled JSON is byte-equal.

### BUG-786 Perf (capture 13 headless, 9.5M/38k buildings): anonymous data.ts lamb
1. Memoise the computeFlows walk keyed on building set.
2. Target: mean tick at 38k buildings reduced by the measured share (25 percent self time) and 20-tick spike under a stated ms (Aaron/lead sets).
3. Gate: headless --cpu-prof at the capture-13 fixture; call-count test that computeFlows walks once per state.

### BUG-800 MOD-034 emigration modifier unpinned at compose level after BUG-380: t
1. Pin MOD-034 emigration modifier at compose level on a fixture where the 2 percent cap does not bind.
2. Gate: mutant removing the modifier turns that test red.

### BUG-802 FEAT-2326609777 follow-up: diffBuildings is 15.8ms/call on the MAIN th
1. The per-tick building diff runs on the worker, or incrementally from a reducer dirty set.
2. Gate: main-thread diff cost at 38k buildings below a stated ms (lead sets; today 15.8).

### BUG-803 FEAT-2326609777 follow-up: the reorder fallback ships the full 217KB i
1. Reorder fallback ships index splices, not the full id-order array.
2. Gate: bytes for a single placement tick at 38k buildings below a stated bound (today 217KB).

### BUG-804 BUG-775 follow-up: after PinForBatch (1095x -> 130x) three full-popula
1. A month-wide pin or residency tuning prevents full-shard sweeps in the three monthly sweeps.
2. Gate: shard-touch count per month at the 1M and 100M fixtures below a stated number, and memory high-water recorded before paging defaults on.

### BUG-806 BUG-684 follow-up: structural-baseline mutant survives at 9M (netCost 
1. Split into two items.
2. (a) Fixture with netOutflow x 900 > netCost kills the baseline mutant.
3. (b) Remove or exercise consolidatorFlowsAreCold; gate: coverage proves the genesis path reaches it or the code is gone.

### BUG-908 CI FLAKE: claude-sync.test.js 19 message/loop/slot tests red on run 34
1. Find why a message row inserted in one claude-sync invocation is invisible to the next (suspect: identity resolution or cursor).
2. Gate: loop the 19 named tests 50 times in CI conditions; zero failures.
3. Until then, tag as quarantined with a visible retry.
