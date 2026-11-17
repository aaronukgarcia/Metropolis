# Backlog comb: p2-feat-2 (85 items)

## Summary
Review depth: titles and descriptions for all 85; BOW show, acceptance-doc lookup and git-log grep for the DONE?/STRONG calls. About 25 items have empty descriptions (mostly FEAT-23266097xx and FEAT-99953855xx) and were judged from the title alone. The slice file and brief were wiped from temp/backlog-comb mid-run, so the table was rebuilt from my notes; priorities for all rows are P2 per the slice definition.

Class counts: STRONG 22, WEAK 38, DONE? 11, RULING 8, DUP 5, STALE 1, NONE 0.
Value counts: GAME 43, INFRA 26, ENABLER 12, NOISE 4.

Five most worth doing next:
1. FEAT-195/196/197 (shopping archetypes, household cycles, delivery): all have AC docs and feed "citizens consume, money moves" directly.
2. FEAT-2326609838 (live-only resident enumeration): precise gate, on the 100M scale path, named fixture.
3. FEAT-1972079909 (grid connection rating, red at 100 percent): player-visible, absorbs FEAT-2326609753; criteria written below.
4. FEAT-1972079939 (why people move out): player can act on migration, which Baseline One names; gates written below.
5. FEAT-2326609721 (C/I revenue scales with population): 359-line doc, fixes a design-depth gap in the spine.

Close or merge:
- Verify then close as DONE?: FEAT-1972079872 (file TUI parity as a child), -897, -906, -907, -946, FEAT-217, FEAT-2326609713, -735, -742, -760, -779.
- Merge as DUP: FEAT-1972079857 into -881; FEAT-2326609753 into FEAT-1972079909; FEAT-2326609776 into FEAT-1972079911; FEAT-2326609769 into the snapshot-cadence doc and FEAT-2326609758; FEAT-2326609840 into FEAT-217.
- Close as STALE: FEAT-229 (393-item ready-queue filter).
- Overlaps to resolve: FEAT-9995385589 vs FEAT-209 (F6 census/passport); FEAT-2326609717/719 vs FEAT-1972079864 (emergency/health chain).
- Park as NOISE: FEAT-1972079870, -887, FEAT-2326609727, -742.

Rulings Aaron owes: FEAT-1972079921 (tourism balance pass; unlock 20 vs 99), FEAT-2326609729 (landlord entity), -786 (power cost row-by-row), -791 (flow basis a/b/c), -810 (GGR65 vs Dev-Team merge), -822 (add-error placement for lifecycle), FEAT-9995385582 (income/upkeep balance), FEAT-2326609727 (go for Tier 1).

## Items
| Code | P | Type | Class | Value | Note (<= 25 words) |
|------|---|------|-------|-------|------|
| FEAT-195 | P2 | feature | STRONG | GAME | Criteria doc FEAT-195.md (69 lines, AC-numbered); archetypes and tier profile testable. |
| FEAT-196 | P2 | feature | STRONG | GAME | FEAT-196.md has ACs; seeded dispersal and conserved shelf-stock are gateable. |
| FEAT-197 | P2 | feature | STRONG | GAME | FEAT-197.md criteria exist; van fleets and last-mile depend on transport and freight modules. |
| FEAT-1972079849 | P2 | feature | STRONG | ENABLER | 255-line criteria doc feat-1972079849-super-grid.md; reviewed 23d74f0. |
| FEAT-1972079850 | P2 | feature | STRONG | ENABLER | 369-line criteria doc for HVDC interconnector; pairs with super grid. |
| FEAT-1972079857 | P2 | feature | DUP | GAME | Sweep/minimum-radius roads is in FEAT-1972079881 (658-line AC) and contiguous-roads FEAT-1972079910. Merge. |
| FEAT-1972079864 | P2 | feature | STRONG | GAME | Dispatch/ETA covered by 407-line doc; waste rota half duplicates FEAT-1972079906. Split off waste. |
| FEAT-1972079870 | P2 | feature | WEAK | NOISE | Says BA to refine; no gate. Special building, endgame; no Baseline One value. |
| FEAT-1972079872 | P2 | feature | DONE? | INFRA | Webconsole badge+About landed 6254c0f; only AC-5 TUI parity remains. Close, file TUI child. |
| FEAT-1972079873 | P2 | feature | WEAK | GAME | Parent epic; children carry criteria. Needs a done-when list of children and gate. |
| FEAT-1972079881 | P2 | feature | STRONG | GAME | 658-line criteria doc; types, snap, bends, grid-fill specified. |
| FEAT-1972079887 | P2 | feature | WEAK | NOISE | Catalogue entry specified, but no test named; endgame megaproject, no loop value. |
| FEAT-1972079897 | P2 | feature | DONE? | ENABLER | Replay rebuild UX landed (1875fc0, 11a341d). Parent still open; list remaining increments or close. |
| FEAT-1972079905 | P2 | feature | WEAK | INFRA | Four-point spec with no test; add a gate matching id to debug-JSON buildings[].id. |
| FEAT-1972079906 | P2 | feature | DONE? | GAME | Waste inc1-3 landed (01a0cff, 344aa3e, c616b3c). Check recycling/diversion scope, then close. |
| FEAT-1972079907 | P2 | feature | DONE? | GAME | Road tiers (0e06755) and auto-scale (34e9c5d) landed; road UX cluster continues in children. |
| FEAT-1972079909 | P2 | feature | WEAK | GAME | Behaviour clear, no gate or doc; needs per-connection load model criteria. Overlaps grid overload v1. |
| FEAT-1972079911 | P2 | feature | STRONG | GAME | 51-line criteria doc; atomic journal entry per swipe. FEAT-2326609776 duplicates it. |
| FEAT-1972079918 | P2 | feature | STRONG | ENABLER | Names determinism gate (byte-identical to main thread), atomic swap, dependencies. |
| FEAT-1972079921 | P2 | feature | RULING | GAME | Inc1 landed 55403ee; inc2-4 gated on Aaron balance pass; description says unlock 99, code 20. |
| FEAT-1972079922 | P2 | feature | STRONG | INFRA | Three build steps and a can-fail test named; universalEdges not yet in tools/plan. |
| FEAT-1972079932 | P2 | feature | WEAK | INFRA | Two alloc cuts with approx counts, no gate. Add alloc-count bound test. |
| FEAT-1972079939 | P2 | feature | WEAK | GAME | Good design, no gate or doc. Attribution reasons need determinism and sum-check tests. |
| FEAT-1972079940 | P2 | feature | WEAK | GAME | Mall building idea clear; no numeric gate. Needs capacity, jobs, demand-effect tests. |
| FEAT-1972079946 | P2 | feature | DONE? | INFRA | Aaron ruled 2026-09-01; 28 void swallowers fixed 8920744/49c64fb. Close ruling item. |
| FEAT-199 | P2 | feature | WEAK | GAME | Palette keys specified, no test; data/zoning.json absent. Add range and palette gates. |
| FEAT-200 | P2 | feature | WEAK | GAME | Three control levels named; precedence and CBD falloff untestable as written. |
| FEAT-201 | P2 | feature | WEAK | GAME | Land value curve described only qualitatively; needs placeholders and monotonic tests. |
| FEAT-203 | P2 | feature | WEAK | GAME | Four milestone effects named; level function and unlock gates lack thresholds and tests. |
| FEAT-205 | P2 | feature | WEAK | GAME | Throttle sliders described; opex scaling and load-shed need testable numbers. |
| FEAT-209 | P2 | feature | STRONG | GAME | ui.screen.census.md (77 lines) exists; F6 owned by census. Overlaps FEAT-9995385589. |
| FEAT-216 | P2 | feature | STRONG | GAME | Aaron ruling with code-fact basis (no seasons in engine.season); 3 KB spec, not individually verified. |
| FEAT-217 | P2 | feature | DONE? | INFRA | A land-dev skill now exists in the skill list; /land-port item duplicates it. Verify and close. |
| FEAT-218 | P2 | feature | WEAK | GAME | Epic; children FEAT-219..223. engine.sentiment does not exist. Needs done-when and thin-slice order. |
| FEAT-219 | P2 | feature | WEAK | ENABLER | Driver spec for MOD-034; no gate per driver. Needs directional tests per modifier. |
| FEAT-220 | P2 | feature | WEAK | GAME | Notice board spec detailed; gate unclear. Needs determinism, ring-buffer, template-coverage tests. |
| FEAT-221 | P2 | feature | WEAK | GAME | Poll aggregator spec detailed; needs representativeness tolerance test. |
| FEAT-222 | P2 | feature | WEAK | GAME | Hardest piece; shadow-tick forecast needs purity and accuracy gates. Not Baseline One. |
| FEAT-223 | P2 | feature | WEAK | GAME | Screen follows FEAT-211 registry pattern; needs registration test and render gate. Blocked on 220-222. |
| FEAT-229 | P2 | feature | STALE | INFRA | 393-item ready queue filter from August; superseded by current P-class ordering and lead sweeps. Close. |
| FEAT-2326609712 | P2 | feature | STRONG | INFRA | Problem and build steps stated, ruling build-now; verdict-note lesson flow. Desc-level look only. |
| FEAT-2326609713 | P2 | feature | DONE? | INFRA | Landed a70437f, Opus r1 ACCEPT, BUG-491 follow-up filed. Close. |
| FEAT-2326609714 | P2 | feature | STRONG | ENABLER | 273-line criteria doc FEAT-2326609714-reliable-save-reload.md. |
| FEAT-2326609717 | P2 | feature | WEAK | GAME | Rule stated; no thresholds, gate or doc. Overlaps emergency-dispatch and SLA items. |
| FEAT-2326609718 | P2 | feature | WEAK | INFRA | Build list present; needs size cap, retention test and load-into-MariaDB gate. |
| FEAT-2326609719 | P2 | feature | WEAK | GAME | Ruling clear; benchmark values and gate missing. Overlaps FEAT-1972079864 dispatch ETA. |
| FEAT-2326609721 | P2 | feature | STRONG | GAME | 359-line criteria doc; C/I revenue scaling with population. |
| FEAT-2326609727 | P2 | feature | RULING | NOISE | Vestige side project awaiting go for Tier 1; unrelated to the game. |
| FEAT-2326609729 | P2 | feature | RULING | GAME | Needs Aaron design call on landlord entity before build. |
| FEAT-2326609735 | P2 | feature | DONE? | ENABLER | optimalProvider total-plan-cost selection landed 88bb6fa (DemandDock overhaul). Close. |
| FEAT-2326609738 | P2 | feature | WEAK | GAME | Title only; no costs, rates or gate. Extends BUG-569. |
| FEAT-2326609742 | P2 | feature | DONE? | NOISE | Rebuild-audit complete 2026-09-02 per memory (FINAL-ROLLUP). Close. |
| FEAT-2326609753 | P2 | feature | DUP | GAME | Per-line MW capacity and brownouts duplicates FEAT-1972079909. Merge into it. |
| FEAT-2326609756 | P2 | feature | WEAK | ENABLER | Durable recorder every 500 ticks; likely overtaken by phase1 durable persistence. Check, then close or add gate. |
| FEAT-2326609757 | P2 | feature | WEAK | GAME | Design seed, no testable deliverable. Overlaps FEAT-196 supply chain. |
| FEAT-2326609758 | P2 | feature | WEAK | ENABLER | Toast success and failure variants; needs render test and real-reason assertion. |
| FEAT-2326609760 | P2 | feature | DONE? | ENABLER | WebGPU renderer spike landed 5637dde; compute phases remain. Close or re-scope phase 2. |
| FEAT-2326609762 | P2 | feature | WEAK | GAME | Per-spec wage ruling; needs data field, validator and sector-equality test. Overlaps wage-model depth. |
| FEAT-2326609765 | P2 | feature | WEAK | GAME | Underground metro at 7M population; endgame, no criteria. |
| FEAT-2326609767 | P2 | feature | WEAK | GAME | Milestone tier 2 thresholds named; rewards and once-only test unspecified. |
| FEAT-2326609768 | P2 | feature | WEAK | GAME | Rail commuter ceiling idea; no formula or gate. |
| FEAT-2326609769 | P2 | feature | DUP | INFRA | Snapshot cadence has feat-1972079936-phase1-inc3 criteria; toast duplicates FEAT-2326609758. Merge. |
| FEAT-2326609773 | P2 | feature | WEAK | INFRA | Context-warning hook; threshold, trigger and test missing; no hook found in harness. |
| FEAT-2326609776 | P2 | feature | DUP | GAME | Duplicates FEAT-1972079911 bulldozer sizes (13x13 L footprint, preview). Merge. |
| FEAT-2326609779 | P2 | feature | DONE? | GAME | Inc3 and inc4 defrag landed (ac05b5b, round 17 ACCEPT); 255-line doc. Close if no inc left. |
| FEAT-2326609786 | P2 | feature | RULING | GAME | Needs Aaron row-by-row on power cost retune; reddens 5 suites. |
| FEAT-2326609787 | P2 | feature | WEAK | INFRA | Freshness probe idea; no endpoint, gate or failure case named. |
| FEAT-2326609791 | P2 | feature | RULING | GAME | Aaron picks flow basis (a), (b) or (c); no code until ruled. |
| FEAT-2326609806 | P2 | feature | STRONG | ENABLER | Three scoped changes with numbers (car 0.539 to 0.589); desc-level look. |
| FEAT-2326609809 | P2 | feature | STRONG | INFRA | Guard enforcement spec clear; no budget or model-table check in claude-dispatch-guard.js. |
| FEAT-2326609810 | P2 | feature | RULING | INFRA | Aaron ruling exists but merge is a process decision still open. |
| FEAT-2326609820 | P2 | feature | STRONG | INFRA | Command and restart steps given; verify by hook lines after cd. Operational. |
| FEAT-2326609821 | P2 | feature | STRONG | INFRA | Steps given; tools/lib-catalog.js still absent. |
| FEAT-2326609822 | P2 | feature | RULING | INFRA | Aaron decides add-error placement before lifecycle switch-on. |
| FEAT-2326609826 | P2 | feature | WEAK | INFRA | compose.go now 4445 lines; split has no gate. Add file-size and gate-green criteria. |
| FEAT-2326609838 | P2 | feature | STRONG | ENABLER | Deferred AC-4 with counter-based gate and 10 percent forced deaths; liveResidentIDs at compose.go:4137. |
| FEAT-2326609840 | P2 | feature | DUP | INFRA | Same as FEAT-217 land skill; merge or close once land-dev verified. |
| FEAT-9995385582 | P2 | feature | RULING | GAME | Balance flag needs Aaron row-by-row on income/upkeep regime. |
| FEAT-9995385589 | P2 | feature | WEAK | GAME | Title only; overlaps FEAT-209 F6 census. Needs queries named, id-as-string gate. |
| FEAT-9995385601 | P2 | feature | STRONG | INFRA | 212-line criteria doc; backend-agnostic launcher; cloud cost-gated. |
| FEAT-9995385604 | P2 | feature | STRONG | INFRA | Measured gates named (20 s, 15 s, 10M/30M); needs cost-gated bucket. |
| INT-004 | P2 | interface | WEAK | INFRA | Policy contract prose; remote backends now exist (offload). Needs per-rule checks. |
| SEC-091 | P2 | finding | WEAK | INFRA | Attack path clear; fix bound and regression test not stated. |
| SEC-096 | P2 | finding | WEAK | INFRA | Live-verified bypass; chosen mitigation and test not stated. |
| SEC-097 | P2 | finding | WEAK | INFRA | Cause known (shallow checkout); fix choice and gate not stated. |

## Improved criteria
Files and functions are named only where verified; the rest is marked unverified.

### FEAT-1972079909 Grid connections rated and load-checked (absorbs FEAT-2326609753)
1. Each pylon/cable segment carries a capacity rating in MW; per-connection load comes from the existing aggregate powerStats() model (name per description; location unverified).
2. At load >= 100 percent the connection renders red on the power layer and a warning appears; test asserts red at 100 percent and normal at 99 percent.
3. The warning offers add substation plus super-grid hookup, or add generation; each is one journalled action, and a test asserts load falls below 100 percent afterwards.
4. Saturation causes brownouts even with enough total generation: fixture with 2 MW generation, a 1 MW line and 2 MW demand serves 1 MW. Goes red if load is only checked in aggregate.

### FEAT-1972079939 Attributed move-out reasons
1. Every move-out in a tick is attributed to exactly one reason from a closed list (wellbeing component, jobs, tax, service, housing cost, pollution, commute); test: per-tick reason counts sum to moveOuts.
2. Attribution is a pure function of start-of-tick state; two identical runs give byte-identical breakdowns (determinism gate).
3. History is bounded (cap stated in the item) and survives save/load; test saves, loads, compares.
4. A drill-in panel ranks reasons with percentages; render test with a 42/30/18 fixture shows those figures.

### FEAT-1972079940 Major shopping complex
1. A catalogue entry exists (footprint at least 4x4) with placeholder capacity, jobs, cost, upkeep, tier and unlock; a catalogue test pins its fields.
2. Placing one raises commercial capacity and jobs by exactly its catalogue values; test compares city state before and after.
3. It satisfies the stated share of shop demand in the RCI model; goes red if demand is unchanged after placement.

### FEAT-1972079905 Building ref-UI toggle
1. Toggle is off by default; when on, each building shows its id at the footprint corner.
2. Hover label reads name - provision - #id.
3. Test: for a fixture city every displayed id equals buildings[].id in the debug JSON capture.
4. Render path uses no Date or Math.random (existing purity gate stays green).

### FEAT-1972079932 Secondary per-tick allocation reductions
1. invariant.Registry.Invariants() allocates zero per call after seal; testing.AllocsPerRun asserts 0.
2. buildings.json is decoded once in compose.Wire; a test counts loader invocations equal to 1.
3. The mutating-the-returned-slice contract is preserved: a test mutates the result and the registry is unchanged (red if the cache is shared mutably).

### FEAT-199 / 200 / 201 / 203 / 205 Zoning, density, land value, maturity, throttles
1. FEAT-199: data/zoning.json defines six zone types with density 1-5 and palette keys res1..res5 etc; schema validator test fails on a missing or out-of-range key.
2. FEAT-200: precedence is paint over district band over citywide default; table-driven test over the nine combinations.
3. FEAT-201: land value rises monotonically with demand pressure and falls when it slackens; property test using the placeholder constants named in the item.
4. FEAT-203: level 1-99 with a milestone every 10 applies all four effects; test at levels 9, 10, 11 shows the step.
5. FEAT-205: stopping a line sets service to zero and riders shift to other modes; opex scales with vehicle count; test pins both.

### FEAT-218 to FEAT-223 Civic Sentiment epic
1. FEAT-218: done when 219-223 are closed and a demo run on the real engine produces a notice-board entry, a poll and a forecast.
2. FEAT-219: each driver has a directional test (more sanitation, wellbeing rises) and a magnitude placeholder flagged for balance.
3. FEAT-220: same seed gives identical feed; ring buffer never exceeds capacity; every template has a coverage test.
4. FEAT-221: poll sample matches population demographics within a stated tolerance; test with a skewed fixture.
5. FEAT-222: EvaluatePolicy never mutates live state (state hash before and after equal) and forecast is within a stated tolerance of an actual run. FEAT-223: screen registered in compose viewRegistrationOrder; registration and render tests.

### FEAT-2326609717 / 719 Health chain and emergency SLA
1. Health decline occurs only when any of doctor, ambulance, hospital or medical stock is missing or offline; table test over the 16 combinations.
2. Response time versus the national benchmark is a stated number per service; a breach lowers happiness and raises fire and death rates in a fixture.
3. Travel time depends on traffic; the same call under congestion takes longer; deterministic test.

### FEAT-2326609718 Dogfood debug-JSON auto-capture
1. Capture fires about every 10 minutes of play (counter-based in tests, not wall-clock) and rows land in the metro database.
2. Rows older than 1 month are purged; test with seeded old rows.
3. A failed write surfaces the real reason, never silently.

### FEAT-2326609738 / 762 / 767 / 768 / 765 Economy and progression depth
1. 738: a build consumes named resources besides money; baseload plants sell surplus power to the grid for revenue; fixtures for both.
2. 762: each spec carries a wage field validated from data; a hypermarket cashier and a tower trader differ in a test.
3. 767: milestones at 1M, 5M, 10M pay once only; reload does not re-pay.
4. 768: commuter ceiling is a stated function of city size; test at three sizes.
5. 765: unlock at 7M population and tube network lowers surface congestion; unverified design, needs Aaron numbers first.

### FEAT-2326609756 / 757 / 758 / 773 / 787 Persistence, goods, toast and infra items
1. 756: journal flush every 500 ticks; crash test loses at most 500 ticks (check phase1 persistence first, may be done).
2. 757: produce a design note listing the basket categories and the first testable slice; no code gate until then.
3. 758: save success and failure toasts render; failure shows the real error string; component test.
4. 773: a hook warns at a stated context size; test with a fake counter at and below threshold.
5. 787: freshness endpoint returns the git sha it would serve; test with a stale worktree shows a mismatch.

### FEAT-2326609826 Split compose.go
1. compose.go falls below a stated line count (target to be set; now 4445).
2. go build, go vet, golangci-lint and the astgate gate stay green; compose.Wire remains the entry.
3. Registration order test unchanged and the engine hash on the determinism gate is byte-identical.

### FEAT-9995385589 Citizen passport UI
1. Names the citizen and occupants queries and the relationship to FEAT-209 on F6.
2. TUI screen shows a citizen with id above 2^53 without precision loss (ids carried as string/BigInt).
3. Webconsole card renders the same record; both covered by a registration and render test.

### INT-004 Remote-call policy
1. Each rule (short-lived tokens, TLS, retry cap, rate limit) maps to a named check in code or CI.
2. The offload client has a test that retries stop at the cap and honour a rate-limit response.
3. A config scan fails on long-lived keys (tool.secretguard).

### SEC-091 / SEC-096 / SEC-097
1. SEC-091: cap threshold and curve counts at a stated limit; an f7.projections patch over it is rejected with a registry error; test asserts bounded canvas allocation.
2. SEC-096: ledger provenance requires HEAD to be an ancestor of a reviewed ref (origin/main); a test with a locally fabricated HEAD fails.
3. SEC-097: gitCommitExists checks only entries touching the commit under test, or CI sets fetch-depth 0; a shallow-clone test passes on ancestor-naming entries.

### FEAT-1972079870, 887, 873 (low priority)
1. 870/887: each adds one catalogue entry with schema-valid fields, a placeholder balance flag and a catalogue test pin; nothing else until Aaron sets endgame scope.
2. 873: done when every child listed in docs/planning/webconsole-ux-overhaul-2026-08-26.md is closed; the item names the child codes.
