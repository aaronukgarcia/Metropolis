# Backlog comb: p3plus-2 (143 items)

## Summary
Counts per class: WEAK 116, STRONG 3, DONE? 10, DUP 2, RULING 5, STALE 2, NONE 5.
Counts per game value: INFRA 82, ENABLER 25, GAME 24, NOISE 12.

Depth caveat: the BOW descriptions were read as truncated excerpts (about 230 chars) plus a git-log grep per code and spot greps of reality. Items with empty descriptions got a title-level look only: BUG-523, 528, 532, 533, 558, 559, 563, 567, 573, 576, 578, 579, 582, 589, 591, 596, 609, 620, 626, 628, 629, 633, 634, 650, 668, 679, 680, 683, 692, 700, 707, 709, 711, 719, 746, 747, 748, 761, 765, 776, 777, 779, 795, 801, 805, FEAT-2326609726/2744. Many "git-log hits" are only the discovering round commit, not a fix; DONE? marks are only where a fix commit or code change was seen.

Five items most worth doing next:
1. BUG-776 and BUG-777 (firms missing from StateDigest, no firms reset on load): every save-integrity and converge gate is blind to the firm registry; money correctness for Baseline One.
2. BUG-724 (payroll shortfall status never clears in a zero-wage month): player-visible stuck state.
3. BUG-805 and BUG-411 (consolidation cost and Earnings Each show wrong money numbers): player-felt, small fixes.
4. BUG-650 and BUG-629 (month-rollover 0/0/0/0 flash, solvent label contradicting state): visible glitches.
5. BUG-810 (232 MB scratch buffer at 1M citizens): scale target blocker, evidence already measured.

Close or merge: close as DONE? after a quick verification: BUG-423, 475, 478, 485, 582, 610, 719, 728, 795, 977, SEC-021. Close as noise or stale: BUG-838, 839, 840 (empty), 611, 801, 451. Merge: BUG-558 into 555; BUG-844 into 826; BUG-868 into 867; BUG-970 into 957; BUG-972 into 971; BUG-999 into 998; BUG-528 into 444; BUG-934 into 826. Bundle BUG-563 into a bookkeeping sweep.

Rulings Aaron owes: BUG-567 and BUG-459 (motorway ramps and junction cap per segment vs per network); BUG-876 (emergency coverage denominator, BUG-873(3)); FEAT-1972079912, FEAT-2326609726 (auto-placement toggle and styles, siting rule); FEAT-1972079933 (auto-provision need+5 percent balance); FEAT-1972079937 (bulk swap cost rules); BUG-692 (cloud auth change needs cost gate); BUG-523 (is the TS money model still authoritative or retire it).

## Items
| Code | P | Type | Class | Value | Note (<= 25 words) |
|---|---|---|---|---|---|
| BUG-382 | P3 | bug | WEAK | INFRA | Hand-copied phase list; CI test catches drift. Wish: generate or import from core so compiler catches it. |
| BUG-383 | P3 | bug | WEAK | ENABLER | Untracked placeholders (shopping 0.15, cafe, foundation TODO-SPEC); no gate; needs a lint listing TODO/STUB without BOW ref. |
| BUG-384 | P3 | bug | WEAK | INFRA | Test-coverage gap across many packages; no per-package target. Split per conservation-relevant package. |
| BUG-389 | P3 | bug | WEAK | INFRA | Windows-only test assumes grep on PATH; expected fix clear, gate is the test itself. |
| BUG-409 | P3 | bug | WEAK | INFRA | Speculative guard vector (-c include.path); needs a concrete denied-command test. |
| BUG-410 | P3 | bug | STRONG | GAME | Blurb ages 5-16 vs meter 5-15; file and strings named; trivial. |
| BUG-411 | P3 | bug | STRONG | GAME | Earnings Each rounds sub-pound rates to 0; evidence numbers given; needs expected format only. |
| BUG-423 | P3 | bug | DONE? | INFRA | TopBar.tsx and PerfHud.tsx now use import.meta.env?.DEV (verified by grep). Close after confirming SSR test. |
| BUG-425 | P3 | bug | WEAK | GAME | Surplus shown same red as shortage; 3684474 added demand-dock colour legend, so maybe partly done; verify then close. |
| BUG-429 | P3 | bug | WEAK | ENABLER | Coastline vs synth-noise mismatch for tile {0,20}; needs expected land/sea outcome and a test over all tiles. |
| BUG-443 | P3 | bug | WEAK | INFRA | Missing end-to-end test for watchdog generation bump; test-only, advisory. |
| BUG-444 | P3 | bug | WEAK | INFRA | Integration test hangs instead of failing; needs a bounded failure assertion. |
| BUG-451 | P3 | bug | WEAK | NOISE | Unreachable edge case in version normalisation; close as won't-fix or one-line test. |
| BUG-453 | P3 | bug | WEAK | INFRA | Test-rigor nit: assert bounded range not !=100; mutant evidence given. |
| BUG-455 | P3 | bug | WEAK | INFRA | Weak boundary test for crisis classification; code correct. Test-only. |
| BUG-459 | P3 | bug | WEAK | GAME | Motorway junction cap counted per-network not per-segment; Aaron Q5; needs ruling on intended cap. |
| BUG-461 | P3 | bug | WEAK | ENABLER | Save leaves in-memory journal state so next write re-persists full journal; clear fix, needs regression test. |
| BUG-462 | P3 | bug | WEAK | INFRA | Stale closure in tick-loop guard; pre-existing, store.tsx:442; needs a repro test. |
| BUG-470 | P3 | bug | STRONG | INFRA | TS parseWireVersion looser than Go; concrete inputs listed; add parity table test. Phase 0 landed, still open? |
| BUG-474 | P3 | bug | WEAK | INFRA | austerity-checks.test.mjs hangs in full node --test; no root cause or gate. |
| BUG-475 | P3 | bug | DONE? | INFRA | Test file proportional-provision-test.mjs is gone and pwr_plant absent from data.ts; stale test removed. Close. |
| BUG-478 | P3 | bug | DONE? | INFRA | Edge lint landed (423640c BUG-482) and 7f444de closed graph drift for save participants; verify attract/market/consumption edges then close. |
| BUG-481 | P3 | bug | WEAK | INFRA | Snapshot trigger keyed on client-visible correlation id; needs an attack test (WS client forces snapshots). |
| BUG-483 | P3 | bug | WEAK | ENABLER | DeathQueue handoff unbounded; blocked on FEAT-088 consumer cursor. Fix when consumer lands. |
| BUG-485 | P3 | bug | DONE? | INFRA | fa34a7e fix threads seed check through non-compose restore paths. Verify and close. |
| BUG-487 | P3 | bug | WEAK | ENABLER | Walk-back probe uses default Deps not caller's (brick risk, MED); f9dd209 landed BUG-480 but follow-ups unverified. |
| BUG-491 | P3 | bug | WEAK | INFRA | Backfill-rulings tooling follow-ups; low value. |
| BUG-492 | P3 | bug | WEAK | INFRA | CI timeout of a TUI test on a no-Go-change commit; flake, no repro; watch item. |
| BUG-494 | P3 | bug | WEAK | INFRA | Three TUI follow-ups already characterised by failing TestBug493R1 tests; strongest of its kind, just fix. |
| BUG-495 | P3 | bug | WEAK | INFRA | Checkpoint writes with ctx.WorldSeed but validates against expected seed; needs mismatch test. |
| BUG-502 | P3 | bug | WEAK | GAME | Cosmetic overlap of insolvency banner and BLOCKED label; screenshot test or CSS rule needed. |
| BUG-507 | P3 | bug | WEAK | GAME | Insolvency popup re-stamps on boundary flap; needs hysteresis rule and test. |
| BUG-523 | P3 | bug | WEAK | ENABLER | TS money model diverges from Go FEAT-083 (wages as leakage); no description; Go is the real engine, maybe STALE. |
| BUG-528 | P3 | bug | WEAK | INFRA | Flaky store-reset-capture test in full-glob run; no description; duplicate class of BUG-444. |
| BUG-530 | P3 | bug | WEAK | INFRA | Test-scope guard regex false-blocks quoted pattern; clear repro, needs test. |
| BUG-532 | P3 | bug | WEAK | GAME | stationLinks counts only road kind, misses motorway/rail-bridge; no description. |
| BUG-533 | P3 | bug | WEAK | INFRA | lineUsageOf saturation absent from debug JSON; no description. |
| BUG-549 | P3 | bug | WEAK | INFRA | Three author-guard residual gaps (-C reuse, cherry-pick, alias); converted ASM; needs three denial tests. |
| BUG-550 | P3 | bug | WEAK | INFRA | Static-analysis reachability proxy vs runtime accessor; design wish. |
| BUG-551 | P3 | bug | WEAK | INFRA | Register feat.weathermode, tool.syncmsg, tool.loopar in master plan; verified absent from code.json. Gate: codejson-audit. |
| BUG-554 | P3 | bug | WEAK | ENABLER | Register facilitypermits -> engine.unlocks edge; gate edge-lint; plan-level chore. |
| BUG-555 | P3 | bug | WEAK | INFRA | Ticker f9 wire drift test vs engine.news; MOD-043 landed; test-only. |
| BUG-557 | P3 | bug | WEAK | INFRA | dash/diagrams hit types lack drift test; same class as BUG-555. |
| BUG-558 | P3 | bug | DUP | INFRA | Ticker news.event drift test; duplicates BUG-555. |
| BUG-559 | P3 | bug | WEAK | ENABLER | TrafficAPI lacks fleet-composition method; no description; fuel->traffic edge registered. |
| BUG-563 | P3 | bug | WEAK | NOISE | Back-link git refs on eight old FEAT items; bookkeeping, one script; merge into a hygiene sweep. |
| BUG-567 | P3 | bug | RULING | GAME | Motorway ramps (Aaron P3): connecting access roads to motorway needs design ruling; related BUG-459. |
| BUG-573 | P3 | bug | WEAK | INFRA | Archive-cap input unclamped; ConfigMenu has min/max 1-100 at lines 252-253 but typed values bypass; needs onChange clamp test. |
| BUG-576 | P3 | bug | WEAK | INFRA | saveCodec decode empty-string mislabels error; tiny, no description. |
| BUG-578 | P3 | bug | WEAK | GAME | Station attractiveness ungated on isOnline; BUG-569 follow-up; test needed. |
| BUG-579 | P3 | bug | WEAK | GAME | Residential HUD counts under-construction; gross vs online split; no description. |
| BUG-582 | P3 | bug | DONE? | GAME | 88bb6fa DemandDock overhaul changed optimalProvider to total-plan-cost selection; check step-2 fallback then close. |
| BUG-589 | P3 | bug | WEAK | ENABLER | SetStructure abort leaves ghost building; engine correctness; needs regression test. |
| BUG-591 | P3 | bug | WEAK | INFRA | Worker offload tests validate a model not store.tsx; test-fidelity. |
| BUG-596 | P3 | bug | WEAK | GAME | resolveDemand notice mis-attributes admin lockout; message wording. |
| BUG-609 | P3 | bug | WEAK | INFRA | runAuto click handler untested; test-only. |
| BUG-610 | P3 | bug | DONE? | INFRA | 97ce155 (BUG-605) reworked Construction Queue; verify LeftDock.tsx no longer walks all buildings unconditionally. |
| BUG-611 | P3 | bug | STALE | INFRA | chunked-replay test took 350s on old base, 3s on later main; no longer applies. |
| BUG-620 | P3 | bug | WEAK | INFRA | Cap-reason notice untested; mutant survives; test-only. |
| BUG-626 | P3 | bug | WEAK | INFRA | One-in-92k upkeep uncategorised bucket from debug3; no repro. |
| BUG-628 | P3 | bug | WEAK | GAME | Roads upkeep display mismatch 0.6 percent; likely policy multiplier; needs expected invariant. |
| BUG-629 | P3 | bug | WEAK | GAME | Solvent label contradicts insolvencyState; UI label fix. |
| BUG-633 | P3 | bug | WEAK | NOISE | Getter reward throws on second read; hostile-input only. |
| BUG-634 | P3 | bug | WEAK | INFRA | Launcher tests vacuous; test-fidelity. |
| BUG-650 | P3 | bug | WEAK | GAME | Demographic accumulator flashes 0/0/0/0 at month rollover; visible glitch; needs render-frame test. |
| BUG-668 | P3 | bug | WEAK | ENABLER | jobsOverride:0 honoured only by wage consumers; needs list of consumers and test. |
| BUG-679 | P3 | bug | WEAK | NOISE | Perf-evidence test logs 0.000ms on Windows; clock granularity. |
| BUG-680 | P3 | bug | WEAK | INFRA | Conditional note: rebuild shardIndex if death state serialisable; latent. |
| BUG-683 | P3 | bug | WEAK | INFRA | PerMonthTick 2s wall-clock ceiling flakes under load; violates no-wall-clock rule; 73a1593 made related pin structural. |
| BUG-692 | P3 | bug | WEAK | INFRA | Azure registry auth to managed identity; cloud/cost; needs cost-gate and Aaron. |
| BUG-700 | P3 | bug | WEAK | INFRA | claude-bow list --priority does not filter; header documents no such flag in list usage; trivial CLI fix. |
| BUG-707 | P3 | bug | WEAK | INFRA | hmrLiveness polish; dev-only. |
| BUG-709 | P3 | bug | WEAK | INFRA | Comment overclaims, dedup meta; polish. |
| BUG-711 | P3 | bug | WEAK | INFRA | scoped.mjs hardening post BUG-708; tooling. |
| BUG-719 | P3 | bug | DONE? | INFRA | Fix commit 1b656a5 landed (idempotent secret seeding on update path) and linked; close. |
| BUG-724 | P3 | bug | WEAK | ENABLER | Payroll shortfall status never clears in a zero private wage month; compose.go financeHook; clear fix, needs test. |
| BUG-728 | P3 | bug | DONE? | INFRA | Synth scanners: skipscandir_test.go shows worktree skip logic exists; verify local full go test green then close. |
| BUG-730 | P3 | bug | WEAK | ENABLER | NaN tax rate poisons money path; reducer needs sanitising and test. |
| BUG-731 | P3 | bug | WEAK | INFRA | Re-arm TargetRef tripwires in two acceptance docs; docs-only. |
| BUG-732 | P3 | bug | WEAK | INFRA | Absolute wall-clock perf bound reds under load; violates no-wall-clock rule. |
| BUG-746 | P3 | bug | WEAK | INFRA | Latent: checkpoint never sets GameMode; no non-test callers. Defer until wired. |
| BUG-747 | P3 | bug | WEAK | ENABLER | Demolitions log append-only forever; memory growth at scale; needs trim rule. |
| BUG-748 | P3 | bug | WEAK | ENABLER | Death-service roster loop lacks OrderComplete guard; no description, 55aa276 context. |
| BUG-761 | P3 | bug | WEAK | INFRA | add-error check ignores --errors-path; tooling. |
| BUG-765 | P3 | bug | WEAK | INFRA | StrictMode double boot records two V868 rows; minor. |
| BUG-776 | P3 | bug | WEAK | ENABLER | StateDigest omits firms; digest-parity gates blind to firm registry; real gap, needs test mutating firm. |
| BUG-777 | P3 | bug | WEAK | ENABLER | Load lacks explicit firms reset when shard absent; sibling of BUG-776. |
| BUG-779 | P3 | bug | WEAK | ENABLER | Finance publish tear across two locks; race; needs race test. |
| BUG-795 | P3 | bug | DONE? | INFRA | errs render gate now uses skipScanDir (render_gate_realtree_test.go:123); verify and close. |
| BUG-801 | P3 | bug | STALE | NOISE | CI flake from runtime fatal error in one run; no repro; close unless recurs. |
| BUG-805 | P3 | bug | WEAK | GAME | Consolidation flow line shows gross cost vs real delta; player-visible number; needs expected label. |
| BUG-810 | P3 | bug | WEAK | ENABLER | Scratch buffer keeps peak capacity (232 MB at 1M); good evidence; needs cap rule and memory test. |
| BUG-811 | P3 | bug | WEAK | INFRA | Worktree prune policy; 115 worktrees today after 2026-10-06 declutter; needs retention rule. |
| BUG-823 | P3 | bug | WEAK | INFRA | Traffic data doc gaps: wrong cross-ref, carry rule; docs-only. |
| BUG-824 | P3 | bug | WEAK | INFRA | Per-band provenance missing for mode_share table; data hygiene. |
| BUG-826 | P3 | bug | WEAK | INFRA | Validator blind spots; four surviving mutants; sibling of BUG-844. |
| BUG-827 | P3 | bug | WEAK | INFRA | Assumptions doc schema drift; docs-only. |
| BUG-835 | P3 | bug | WEAK | ENABLER | Out-of-root JSON import may not bundle; inc1/2 landed; verify production build contains ladder. |
| BUG-838 | P3 | bug | NONE | NOISE | Empty body, title just P3; created in a bulk burst. Close. |
| BUG-839 | P3 | bug | NONE | NOISE | Empty body, title just P3. Close. |
| BUG-840 | P3 | bug | NONE | NOISE | Empty body, title just P3. Close. |
| BUG-841 | P3 | bug | WEAK | INFRA | Test rmSyncs shared fixtures dir; clear repro; fix to use own temp dir. |
| BUG-844 | P3 | bug | DUP | INFRA | Twelve validator mutants survive; merge with BUG-826. |
| BUG-845 | P3 | bug | WEAK | INFRA | Provenance year string vs number inconsistency; needs one rule. |
| BUG-846 | P3 | bug | WEAK | NOISE | Acceptance doc says no code.json edit but landing added 72 lines; doc correction. |
| BUG-850 | P3 | bug | WEAK | ENABLER | NaN population yields silent NaN rung; fail-closed expected; needs test. |
| BUG-866 | P3 | bug | WEAK | INFRA | Seed bounds fix not applied to sibling function; copy-paste miss; needs test. |
| BUG-867 | P3 | bug | WEAK | INFRA | Five TrafficConfig fields unguarded casts; fail-closed pattern known (BUG-865). |
| BUG-868 | P3 | bug | WEAK | INFRA | debugjson reads tile metres unguarded; sibling of BUG-867; merge. |
| BUG-875 | P3 | bug | WEAK | INFRA | Near-vacuous tie-break test; code correct. |
| BUG-876 | P3 | bug | RULING | GAME | Emergency coverageShare ~1 at tile scale; Aaron denominator ruling BUG-873(3) pending. |
| BUG-884 | P3 | bug | WEAK | INFRA | Source-text pin where behavioural pin possible; test-fidelity. |
| BUG-902 | P3 | bug | WEAK | ENABLER | parking.json hospital_bed/school_pupil rows unused; data or code gap. |
| BUG-909 | P3 | bug | WEAK | ENABLER | ambulanceUnlocked defaults true on raw penalty functions; needs default decision. |
| BUG-910 | P3 | bug | WEAK | ENABLER | Go MentalFile accepts zero weights; add require-positive loader check and test. |
| BUG-911 | P3 | bug | WEAK | INFRA | Four unasserted mutants in parking tests; test-only. |
| BUG-933 | P3 | bug | WEAK | ENABLER | busLaneMaxShareOfClass=1 accepted, defeats guard; add upper bound and test. |
| BUG-934 | P3 | bug | WEAK | INFRA | New field not validated by validate-traffic-tables; sibling of BUG-826. |
| BUG-942 | P3 | bug | WEAK | INFRA | road_wear reconciliation note argues ratio backwards; data doc fix. |
| BUG-943 | P3 | bug | WEAK | INFRA | Wear tests still inject wear; residual test-strength. |
| BUG-945 | P3 | bug | WEAK | INFRA | Two stale statements in inc9 acceptance doc; docs-only. |
| BUG-956 | P3 | bug | WEAK | INFRA | Hand-typed thresholds and hex palette in Transport screen; GR#3/15; move to overlays.json. |
| BUG-957 | P3 | bug | WEAK | GAME | Commute p90 row shows p50; disclosed; real p90 exported; fix plus test (see BUG-970). |
| BUG-964 | P3 | bug | WEAK | INFRA | Reference-identity test not pinned against ==; mutant evidence. |
| BUG-965 | P3 | bug | WEAK | INFRA | No pin that computeFlows runs once per tick; add call-count test. |
| BUG-970 | P3 | bug | WEAK | INFRA | No behavioural pin for BUG-957 p90 row; merge with BUG-957. |
| BUG-971 | P3 | bug | WEAK | GAME | Overlay render 12.4ms/tick at 6400 buildings vs 5ms bar; measured; perf gate needed (structural, not wall-clock). |
| BUG-972 | P3 | bug | WEAK | INFRA | Per-frame filter still in draw pass; sibling of BUG-971; merge. |
| BUG-975 | P3 | bug | WEAK | NOISE | Comment misstates why keys protected; comment fix. |
| BUG-977 | P3 | bug | WEAK | ENABLER | First snapshot 33.9ms vs 25ms bar on real state; 72474a7 landed perf fix; verify and close. |
| BUG-984 | P3 | bug | WEAK | INFRA | UNITS-LINT-002 checks line range not content; tooling. |
| BUG-993 | P3 | bug | WEAK | NOISE | Two load-bearing comments misstate __proto__ hazard; comments only. |
| BUG-998 | P3 | bug | WEAK | INFRA | units-lint regex runs past declaration end; tooling; merge with BUG-999. |
| BUG-999 | P3 | bug | WEAK | INFRA | units-lint mis-lexes regex literals; probes proven; merge with BUG-998. |
| SEC-021 | P3 | finding | DONE? | INFRA | b9af1ee gave the secret guard stricter entropy handling for camelCase exemption; confirm hyphenated IDs pass then close. |
| SEC-073 | P3 | finding | WEAK | INFRA | BindSubscription accepts any view; add validation and test mirroring Subscribe MET-U702. |
| SEC-075 | P3 | finding | WEAK | INFRA | n/N stepping reimplemented without drift test; add test importing ui.keys. |
| FEAT-1972079912 | P4 | feature | RULING | GAME | Auto-placement toggle (P4); siting rule and style need ruling; related FEAT-2326609726. |
| FEAT-1972079933 | P4 | feature | RULING | GAME | Auto-provision on shortfall; prompt then place need+5 percent; balance numbers need Aaron. |
| FEAT-1972079937 | P4 | feature | RULING | GAME | Bulk select-all and like-for-like swap; explicitly low priority; cost rules unspecified. |
| FEAT-2326609726 | P4 | feature | NONE | GAME | Auto-placement style radio with five named styles; no description; depends on FEAT-1972079912. |
| FEAT-2326609744 | P4 | feature | NONE | GAME | Auto road improvement on sustained congestion; no description beyond title. |
| FEAT-1972079915 | P5 | feature | WEAK | NOISE | Cosmetic compass/advisor stacking; northstar P5 distraction; fix with a CSS offset or close. |

## Improved criteria
### BUG-776 StateDigest has no firms observable
1. Composition.StateDigest includes a firms component derived from the firm registry (not only finance.AcctFirms).
2. Test: build two compositions identical except one firm's field; digests must differ (red today).
3. Gate: a save, Load, digest-parity round-trip test over a city with firms; a dropped firm shard makes it red.

### BUG-777 Composition.Load adds no firms reset when shard absent
1. Loading a bundle with no firms shard leaves the firm registry empty, as the BUG-689 death-services path does for its shard.
2. Test: Load into a composition that already holds firms from a bundle without the shard; registry must be empty afterwards (red today).
3. Gate: go test of the compose package for this test, plus the digest test from BUG-776.

### BUG-724 Payroll-shortfall status never clears
1. A month with zero private wage bill and no shortfall clears lastPayrollShortfall (compose.go financeHook).
2. Test: set the shortfall, run a month with privateWageBill == 0, assert it is cleared (red today).
3. Gate: a month with real unpaid wages must still keep the status set.

### BUG-805 Consolidation flow line reports gross cost
1. The flow line shows the net funds delta (cost minus refund), matching the actual funds change on a 9M city.
2. Test: consolidate with a known successor cost and refund; the displayed figure equals funds before minus funds after.
3. Gate: webconsole sim test; reverting to gross makes it red. The label states whether the figure is gross or net.

### BUG-411 Earnings Each rounds sub-pound rates to 0
1. The Each column shows two decimal places below 10 pounds (Tourism 0.12 not 0).
2. Test: render with Tourism 37710 over 314252 citizens, expect 0.12; Residential at 30 percent tax expects 0.60.
3. Gate: webconsole test of the Earnings formatter; integer rounding makes it red.

### BUG-810 DemandByType scratch buffer keeps peak capacity
1. After a gather at 1M citizens, retained scratch capacity falls back under a stated cap (for example 2x the current member count) within N calls.
2. Test: gather 1M then a small set, assert cap(demandMemberRecords) below the bound (red today at about 232 MB).
3. Gate: households package test with an allocation or capacity assertion, not wall-clock.

### BUG-650 Demographic accumulator flashes 0/0/0/0 at month rollover
1. The demographic panel never renders an all-zero frame between months in a running city.
2. Test: advance across a month boundary on a non-empty city, sample the panel value every tick, assert non-zero throughout (red today for one render).
3. Gate: webconsole engine/reducer test; fix is ordering of the accumulator reset versus publish.

### BUG-629 solvent label contradicts insolvencyState
1. The status never shows solvent=false beside insolvencyState=solvent; the label is named for what it measures (this-tick cashflow) or derived from insolvencyState.
2. Test: a state with negative cashflow but solvent insolvency band; assert the displayed label and debug JSON agree with the band.
3. Gate: debugjson test; a mismatch makes it red.

### BUG-957 Commute p90 row renders p50
1. The Transport row shows snapshot.p90CommuteMinutes when finite; fallback only when absent, and then labelled.
2. Test: snapshot with p50 20 and p90 45; row shows 45 (hand-patching to p50 must be red, see BUG-970).
3. Gate: transportTab test; merge BUG-970 in.

### BUG-971 Overlay render path 12.4 ms per tick at 6,400 buildings
1. The inc10 overlay derivation set stays under 5 ms per tick at 6,400 buildings by structural counting (derivations per tick, not wall-clock).
2. Test: count derivation or allocation work per frame on a 6,400-building state; a full per-frame rescan is red.
3. Gate: webconsole bounded set; do not use a wall-clock assertion (see BUG-683, 732). Fold BUG-972 in.
