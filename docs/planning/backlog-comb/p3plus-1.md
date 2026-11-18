# Backlog comb: p3plus-1 (144 items)

## Summary
Cold review of 144 P3-and-below items (modules, features, bugs). Read-only; evidence from git log (messages only), file existence checks and the item text. Most rows are a title-plus-description look; criteria were checked in code only where the Note says so, otherwise "unverified". Items with an empty description were classed NONE on the title alone.

Counts per class: DONE? 10, WEAK 42, RULING 11, STALE 1, NONE 18, DUP 4, STRONG 58.
Counts per game value: GAME 43, INFRA 71, NOISE 7, ENABLER 23.

Five most worth doing next (closest to the Baseline One loop and cheapest to unblock):
1. FEAT-2326609730 goods economy: producers with no consumer is the missing link in "citizens consume, money moves".
2. FEAT-2326609746 zone tax wired: built-but-not-wired (RateInZone has no production caller), small and testable.
3. BUG-1141 ledger observer: any non-finance-phase poster is invisible at month end; blocks honest money tests (FEAT-165, social, defence payroll).
4. BUG-1170 and BUG-1175 save lineage hazards: both can overwrite a real city; fix 1175's lineage guard first.
5. FEAT-163 / FEAT-165 civic auto-placement and body disposal: player-felt, with a ready model (feat.faith auto-build, engine.refuse export contracts).

Close or merge (evidence in Note column):
- Close as built: MOD-061, MOD-065, MOD-076, MOD-077 (packages exist, spaceport/accelerator landed), FEAT-1972079901, FEAT-1972079900 (inc1; keep only if coarsening remains), FEAT-1972079902 (HS line remainder unverified), BUG-1184 (d55ee88), FEAT-2326609770 (dev:full exists; confirm), BUG-084 (half done).
- Duplicates: FEAT-097 -> MOD-076, FEAT-098 -> MOD-077, FEAT-100 -> MOD-065, BUG-1072 -> BUG-1086 (stale-comment cluster with BUG-1013), FEAT-1972079859 overlaps FEAT-2326609808.
- Stale or noise: MOD-069 (superseded by FEAT-2326609775 and GGR94), FEAT-023 (idea list), FEAT-9995385588 (epic bucket), BUG-1134 (parked airport), BUG-1154 (dead field, delete it), BUG-1110 (belongs in the SAFE/forge BOW).
- FEAT-2326609816 has a title and description that describe two different jobs; fix before anyone acts on it.

Rulings Aaron owes: MOD-068 (keep GPU sidecar?), FEAT-039 (CI-after-push hook go/no-go), FEAT-178 (manual vs auto transport mesh scope), FEAT-1972079904 (build-cost rebalance rows), FEAT-2326609715 (visualiser, parked), BUG-1056 (XP for anti-sprawl connector tiles), BUG-1078 (water tariff balance), BUG-1090 (fixed vs percent level-up reward), BUG-1103 (history purge vs the 2026-08-22 no-rewrite ruling), BUG-1102 (backup deletion, due 2027-01-06, deletion gate), BUG-1167 (address label scheme past Z).

Title-level look only (no code or BOW comments read): all rows not named in the Note as checked. BOW comments were not read for any item.

## Items
| Code | P | Type | Class | Value | Note |
|---|---|---|---|---|---|
| MOD-061 | P3 | module | DONE? | GAME | internal/engine/destination exists with tests; confirm resort and mega-mall archetypes are wired before closing. |
| MOD-063 | P3 | module | WEAK | GAME | Whole refinery-to-tyre chain in one item; only chemicals/refinery.go exists. Split per facility, each with a gate. |
| MOD-065 | P3 | module | DONE? | GAME | internal/engine/tunnels (api.go, tests) exists; TBM learning curve and hyperloop unverified. Overlaps FEAT-100. |
| MOD-068 | P3 | module | RULING | INFRA | C++/CUDA solver sidecar; fit with cloud Go engine plan unclear. Aaron: keep or cancel. |
| MOD-069 | P3 | module | STALE | INFRA | Superseded by FEAT-2326609775 (Azure inc1) and GGR94 (services on pix). Close or rewrite. |
| MOD-076 | P3 | module | DONE? | GAME | 98e9597 landed engine.spaceport; close if wired into compose. |
| MOD-077 | P3 | module | DONE? | GAME | db6344d and a122e2c landed engine.accelerator; close if wired. |
| FEAT-023 | P3 | feature | NONE | NOISE | Parking list of shelved future ideas, not buildable. Close; keep the list in a doc. |
| FEAT-039 | P3 | feature | RULING | INFRA | Awaiting Aaron go since 2026-08-10; /ci-green and GGR cover CI verification. Decide or close. |
| FEAT-097 | P3 | feature | DUP | GAME | Duplicate of MOD-076 (spaceport, built 98e9597). Close. |
| FEAT-098 | P3 | feature | DUP | GAME | Duplicate of MOD-077 (accelerator, built). Close. |
| FEAT-100 | P3 | feature | DUP | GAME | Duplicate of MOD-065 tunnels (Chunnel and hyperloop). Merge. |
| FEAT-158 | P3 | feature | WEAK | ENABLER | 16x/32x speed ladder; needs gate that state hash is identical at every speed. |
| FEAT-163 | P3 | feature | WEAK | GAME | Auto-placed civic amenities; milestone thresholds, toggles and pass/fail gate unspecified. See improved criteria. |
| FEAT-164 | P3 | feature | WEAK | GAME | Utility auto-expand at scale; thresholds and prompt behaviour unspecified. See improved criteria. |
| FEAT-165 | P3 | feature | WEAK | GAME | Body disposal pipeline; good model (engine.refuse) but no numbers or gate. See improved criteria. |
| FEAT-166 | P3 | feature | WEAK | GAME | Extends engine.roads naming.go; no gate for determinism or rename persistence. |
| FEAT-168 | P3 | feature | WEAK | ENABLER | Review task with no deliverable. Define output as a filed gap list. |
| FEAT-174 | P3 | feature | WEAK | GAME | Leisure access signal; no weights and no named consumer beyond leisure. |
| FEAT-178 | P3 | feature | RULING | GAME | Manual vs full-auto transport mesh is two features; Aaron to pick scope first. |
| FEAT-179 | P3 | feature | WEAK | GAME | Station typology and tiers; capacity numbers absent (balance rows need Aaron). |
| FEAT-184 | P3 | feature | WEAK | GAME | Demolition and terraform costs are balance numbers; depends on decommission items. |
| FEAT-1972079858 | P3 | feature | NONE | GAME | One-line wish: cycle lanes and footpaths; no widths, effects or gate. |
| FEAT-1972079859 | P3 | feature | NONE | GAME | One-line wish: parking supply; overlaps FEAT-2326609808. |
| FEAT-1972079862 | P3 | feature | NONE | GAME | One-line wish: people-flow overlay; no data source or gate. |
| FEAT-1972079863 | P3 | feature | NONE | GAME | One-line wish: wealth/cost heat layer; needs per-building net definition. |
| FEAT-1972079865 | P3 | feature | NONE | GAME | One-line wish: inbound vs outbound vehicle marker. |
| FEAT-1972079867 | P3 | feature | NONE | GAME | One-line wish: farm auto-size and colours; no thresholds. |
| FEAT-1972079868 | P3 | feature | NONE | GAME | One-line wish: terrain elevation layer. |
| FEAT-1972079869 | P3 | feature | NONE | GAME | One-line wish: ambient citizen circles; low value, scale rules missing. |
| FEAT-1972079871 | P3 | feature | WEAK | INFRA | Concrete: data.ts ind_farm dead branch (~5913) and DIMS key (~220) still present; compile-error gate vague. |
| FEAT-1972079894 | P3 | feature | WEAK | INFRA | LLM autoplay to 100M in Turbo; useful dogfood but no pass/fail definition. Cost gate applies. |
| FEAT-1972079900 | P3 | feature | DONE? | ENABLER | inc1 landed 40336b6 (estate-scale specs); coarsening of existing objects unverified. |
| FEAT-1972079901 | P3 | feature | DONE? | GAME | cef80bb landed Five Gorges dam; rework lives in FEAT-2326609749. |
| FEAT-1972079902 | P3 | feature | DONE? | GAME | inc1-3 landed (e12c848, 20d33fa, f489b8f): capacity, trains, auto-branch. HS line part unverified. |
| FEAT-1972079904 | P3 | feature | RULING | GAME | Cost rebalance is a balance row; cef80bb did power costs. Aaron row-by-row for the rest. |
| FEAT-212 | P3 | feature | WEAK | INFRA | Ruling exists; needs spec-lint exit code and a fixture that goes red on an undisclosed numeric. |
| FEAT-213 | P3 | feature | WEAK | GAME | Requires roads->citizens edge registered first (GR#25); eligibility rule numbers absent. |
| FEAT-214 | P3 | feature | WEAK | INFRA | astgate rule for copy-on-write; needs a positive and a negative fixture named. |
| FEAT-2326609715 | P3 | feature | RULING | NOISE | Aaron ruled after Baseline One; no game value. Leave parked. |
| FEAT-2326609722 | P3 | feature | WEAK | ENABLER | Coded-error toast; names protocolClient.ts range but no gate. Real player-felt gap. |
| FEAT-2326609730 | P3 | feature | NONE | ENABLER | Title only; the goods economy is the missing game loop link. See improved criteria. |
| FEAT-2326609732 | P3 | feature | NONE | GAME | Title only and explicitly post-BL1; ~100 landmarks needs splitting and ruling. |
| FEAT-2326609739 | P3 | feature | NONE | GAME | Title only: 4-reactor plant; no capex, output or footprint numbers. |
| FEAT-2326609741 | P3 | feature | NONE | ENABLER | Title only: grid transmission overload. See improved criteria. |
| FEAT-2326609746 | P3 | feature | WEAK | ENABLER | Built-but-not-wired: RateInZone only in tax.go, no production caller. See improved criteria. |
| FEAT-2326609749 | P3 | feature | NONE | GAME | Title only; reservoir, flooded valley and land use unspecified. Follow-on to FEAT-1972079901. |
| FEAT-2326609754 | P3 | feature | NONE | GAME | Ruling made (Q100091=B) but no criteria: which pieces connect, what test. |
| FEAT-2326609755 | P3 | feature | NONE | GAME | Title only, post-BL1; consolidate action with cost-benefit display. |
| FEAT-2326609770 | P3 | feature | DONE? | INFRA | webconsole/package.json has dev:full running tools/debugsink/dev-with-sink.mjs; check plain dev co-launches. |
| FEAT-2326609789 | P3 | feature | WEAK | GAME | Richer consolidation arc; no gate beyond BUG-736 safe default. |
| FEAT-2326609808 | P3 | feature | WEAK | GAME | Parked cars consume space and cut capacity; good model but no numbers, overlaps FEAT-1972079859. |
| FEAT-2326609813 | P3 | feature | STRONG | INFRA | Names the mutant (repoHasGoMod returns false) that must go red; ready. |
| FEAT-2326609816 | P3 | feature | WEAK | INFRA | Title (delete 21MB backup) and description (root doc clutter) disagree; deletion needs the gate. |
| FEAT-2326609827 | P3 | feature | WEAK | INFRA | Refactor batch with no gate beyond green suite; split per file and cap. |
| FEAT-2326609828 | P3 | feature | WEAK | INFRA | Edge graph strengthening; 43 isolated modules but no target count or lint gate. |
| FEAT-2326609829 | P3 | feature | WEAK | INFRA | Startup summary stale-item flag; no staleness line found in harness/claude-startup.js (unverified). |
| FEAT-2326609839 | P3 | feature | STRONG | ENABLER | Criteria in acceptance/BUG-666-rowof-index.md AC-5 remainder; byte-identical to golden is the gate. |
| FEAT-9995385588 | P3 | feature | NONE | NOISE | Epic bucket for ~80 test-debt items; convert to a query, not an item. |
| FEAT-9995385593 | P3 | feature | WEAK | INFRA | GGR94 move to pix; inventory not done, cost gate needed. Port list and exit test missing. |
| FEAT-9995385602 | P3 | feature | STRONG | INFRA | Gate named (TestDeterminismGate on arm64, bit-identical); blocked on a runner and cost gate. |
| BUG-041 | P3 | bug | WEAK | INFRA | Return value mismatch RunCommandLoop vs StubEngine.Run; no test named; low value. |
| BUG-084 | P3 | bug | DONE? | INFRA | Part 1 (command substitution) closed by 8005236 (BUG-1182); symlinked git binary half unverified. |
| BUG-1000 | P3 | bug | STRONG | INFRA | Vacuous CRLF pin; mutant M5 named. Fix is clear. |
| BUG-1002 | P3 | bug | STRONG | ENABLER | realisedCityWideModeShareOf returns live memoised object; freeze or clone, named. |
| BUG-1003 | P3 | bug | WEAK | INFRA | By-design absent rows log at error severity; chosen fix (downgrade or data) not stated. |
| BUG-1004 | P3 | bug | STRONG | INFRA | Copy of jobsAtTier rule; export and share, with parity test. |
| BUG-1005 | P3 | bug | STRONG | INFRA | Validator lacks loader band coverage check; mutation measured. |
| BUG-1007 | P3 | bug | WEAK | INFRA | Tick-driver seam keyed on NODE_TEST_CONTEXT; proposed fix not stated. |
| BUG-1008 | P3 | bug | STRONG | ENABLER | Present-but-corrupt trafficSnapshot decodes to undefined key; measured. |
| BUG-1009 | P3 | bug | STRONG | ENABLER | Third TrafficSnapshot builder bypasses canonicalSnapshot (engine.ts ~7676). |
| BUG-1012 | P3 | bug | STRONG | INFRA | Deferred half of circular-import fix unpinned; deletion mutant named. |
| BUG-1013 | P3 | bug | STRONG | INFRA | Stale doc comment plus scoped.mjs residue; two named fixes. |
| BUG-1017 | P3 | bug | STRONG | GAME | Bend legality only on player-drawn paths; two other laying paths named. |
| BUG-1038 | P3 | bug | STRONG | INFRA | Near-vacuous mutation test in road-antisprawl.test.mjs; test named. |
| BUG-1041 | P3 | bug | STRONG | ENABLER | Unscoped try/catch swallows MET-V960; placeRoadPath named. |
| BUG-1042 | P3 | bug | STRONG | INFRA | errors.json lines 159 and 161 both carry V930-V934 (may be different parents; verify). |
| BUG-1045 | P3 | bug | STRONG | INFRA | Bare Error in test seam violates GR#7; file and function named. |
| BUG-1050 | P3 | bug | STRONG | INFRA | Three surviving mutants, all test gaps; each named. |
| BUG-1056 | P3 | bug | RULING | GAME | Do anti-sprawl connector tiles grant XP, and is the threshold injectable? Aaron on XP. |
| BUG-1060 | P3 | bug | STRONG | INFRA | Two functions disagree on non-boolean flag; stale comments named. |
| BUG-1070 | P3 | bug | STRONG | INFRA | Five surviving AC-8 mutants listed; strengthen pins. |
| BUG-1072 | P3 | bug | DUP | INFRA | Stale-comment cluster; merge with BUG-1086 and BUG-1013. |
| BUG-1073 | P3 | bug | STRONG | INFRA | Latent GR#3 duplication in integratedTransportScoreOf; fix is clear. |
| BUG-1076 | P3 | bug | STRONG | INFRA | isWaterShortageActive dead in production (data.ts ~4331); remove or consume. |
| BUG-1077 | P3 | bug | STRONG | GAME | Fix and Fix All still queue water plants while demand rows are gated. |
| BUG-1078 | P3 | bug | RULING | GAME | Tariff invariant tolerates halving; record for Aaron balance pass. |
| BUG-1082 | P3 | bug | WEAK | INFRA | Three structural P3s, none reproduced in 1,800-drag fuzz; no failing test. |
| BUG-1085 | P3 | bug | STRONG | NOISE | Non-ASCII reintroduced in mode_split_local.json mirror; ASCII lint gate. |
| BUG-1086 | P3 | bug | STRONG | INFRA | Three stale comments incl. ERR code; absorb BUG-1072, BUG-1013. |
| BUG-1087 | P3 | bug | STRONG | INFRA | local_access_high exclusion rests on a code literal; derive from data. |
| BUG-1088 | P3 | bug | STRONG | INFRA | One-reader rule applied to flag but not to the rest; named. |
| BUG-1090 | P3 | bug | RULING | GAME | Level-up reward pays 10 percent of treasury; Aaron to rule fixed sum or percent. |
| BUG-1093 | P3 | bug | STRONG | INFRA | sectorPartition section 8 table cites dead isReplaying branch; doc fix. |
| BUG-1094 | P3 | bug | STRONG | INFRA | sectorsCompletingAtTick consumes every tick unconditionally; behaviour named. |
| BUG-1102 | P3 | bug | RULING | INFRA | Backup eligible 2027-01-06 and not yet due; deletion gate needed. |
| BUG-1103 | P3 | bug | RULING | INFRA | History rewrite contradicts the 2026-08-22 no-rewrite ruling; Aaron to reconfirm or cancel. |
| BUG-1105 | P3 | bug | WEAK | INFRA | Flaky MET-G817 vs G819 in ClaimRaceExactlyOneWinner; assertion fix not stated. |
| BUG-1106 | P3 | bug | STRONG | INFRA | Tests write to real .git/hooks under TEMP-inside-repo; risk, deserves P2. |
| BUG-1107 | P3 | bug | STRONG | INFRA | Stale mcp__vestige__search instructions remain (24 hits in .claude/commands and docs). |
| BUG-1110 | P3 | bug | WEAK | INFRA | forge.js lives outside repo; belongs in SAFE/forge BOW, not here. |
| BUG-1115 | P3 | bug | STRONG | INFRA | Test suites leak ~450 session-key files; cleanup in afterEach is the gate. |
| BUG-1120 | P3 | bug | WEAK | INFRA | 168 orphan dirs (now ~294 in .claude/worktrees); surgical cleanup plan needed. |
| BUG-1132 | P3 | bug | WEAK | ENABLER | RegisterTransfer refuse-to-farming unbuilt; no gate or numbers. |
| BUG-1133 | P3 | bug | WEAK | GAME | Verify then net EV share from fuel duty base. See improved criteria. |
| BUG-1134 | P3 | bug | NONE | NOISE | Parked airport module; no table or gate. Close until MOD-075 is scheduled. |
| BUG-1136 | P3 | bug | STRONG | INFRA | Two named tests fail when TEMP is inside repo; fix is clear. |
| BUG-1138 | P3 | bug | STRONG | INFRA | Debug fast-build flag mid-construction desyncs; debug-only. |
| BUG-1139 | P3 | bug | STRONG | INFRA | Pins C4 and C5 do not kill named mutants; file named. |
| BUG-1141 | P3 | bug | STRONG | ENABLER | Phase-stable ledger observer; deliverable and test update named. |
| BUG-1146 | P3 | bug | STRONG | ENABLER | social ImportState accepts five crafted shapes; each listed. |
| BUG-1154 | P3 | bug | STRONG | NOISE | Dead field relocateAllCostUpperBound; fix or delete. |
| BUG-1159 | P3 | bug | WEAK | ENABLER | Uncoded load-refused error confirmed at store.tsx:4154. See improved criteria. |
| BUG-1167 | P3 | bug | RULING | NOISE | AddressLabel past Z; Aaron: extend scheme or accept repeats. |
| BUG-1170 | P3 | bug | WEAK | ENABLER | Two tabs share scratch lineage; save corruption risk. See improved criteria. |
| BUG-1173 | P3 | bug | WEAK | INFRA | Wall-clock timeout flake; replace with bounded condition. No test named beyond title. |
| BUG-1175 | P3 | bug | WEAK | ENABLER | Stale-engine restore keeps old state; guard rebuildLineageId first. See improved criteria. |
| BUG-1176 | P3 | bug | STRONG | ENABLER | pageDirClaim.Release races takeover lock; function named. |
| BUG-1177 | P3 | bug | STRONG | ENABLER | Rail tier must not count road joins; fix and comment named. |
| BUG-1180 | P3 | bug | STRONG | INFRA | Fixed 3-4s waits; replace with condition polling. |
| BUG-1184 | P3 | bug | DONE? | ENABLER | d55ee88 caps every restore path for over-cap saves. |
| BUG-1185 | P3 | bug | STRONG | GAME | Cap copy hardcodes one per city; pow_hvdc has maxPerCity 2. |
| BUG-1187 | P3 | bug | STRONG | INFRA | Remaining author-guard command-position forms (time -p, env); partly closed by 8005236. |
| BUG-1190 | P3 | bug | STRONG | INFRA | bug606 M1 cannot detect its own regression; mutation C named. |
| BUG-1191 | P3 | bug | WEAK | INFRA | Engine-lag-chip test 87s vs 15s timeout; fix unspecified. |
| BUG-1192 | P3 | bug | STRONG | INFRA | sortPaletteItems (data.ts:1806) now dead after UX-1 b127698; remove. |
| BUG-1195 | P3 | bug | STRONG | INFRA | 4-minute DEADLOCK guard trips on healthy -race run; measured. |
| BUG-1200 | P3 | bug | STRONG | GAME | Affordability confirm lacks focus trap; real-app evidence. |
| BUG-1202 | P3 | bug | STRONG | INFRA | Remove liveResidentIDs cache; add 500-sample pin. |
| BUG-1203 | P3 | bug | STRONG | INFRA | AC-2 test always skips at wire major 1; fix named. |
| BUG-1207 | P3 | bug | STRONG | INFRA | Rename over-claiming test and add zipDir method-per-extension test. |
| BUG-248 | P3 | bug | WEAK | INFRA | bow lint quotes removed description; fix location (isQuotedPhraseCitation) named, no test. |
| BUG-290 | P3 | bug | STRONG | ENABLER | dash.DrillTarget.Valid lacks EntityID shape check (drill.go:53). |
| BUG-292 | P3 | bug | STRONG | INFRA | Wall-clock bound assertion in plan-guard test; breaks verification standard. |
| BUG-293 | P3 | bug | WEAK | INFRA | Loader-wide strict decoding; builder proved generic strict breaks buildings.json. |
| BUG-310 | P3 | bug | WEAK | INFRA | Grab bag of lows from audit; split, MulRat overflow first. |
| BUG-312 | P3 | bug | STRONG | INFRA | RunGate unguarded months multiply, latent; SafeMul idiom named. |
| BUG-326 | P3 | bug | WEAK | INFRA | Windows EPERM rename flakes; retry-wrapper gate not stated. |
| BUG-339 | P3 | bug | STRONG | INFRA | closed_note limit counts appended period; fix named. |
| BUG-358 | P3 | bug | STRONG | INFRA | 29 dormant remedies with placeholders; render check. |
| BUG-364 | P3 | bug | STRONG | INFRA | Unit key/name mismatches (dBA, pax); registry edit. |
| BUG-368 | P3 | bug | STRONG | INFRA | Edge ui.screen.menu->engine.core should be ui.core; current state unverified. |
| BUG-372 | P3 | bug | STRONG | ENABLER | tourism AdvanceMonth TOCTOU reproduced with LeisureMix(0). |
| BUG-381 | P3 | bug | STRONG | INFRA | Noop hooks contradict compose/doc.go dead-weight policy; sites named. |

## Improved criteria
Ten items chosen as the WEAK/NONE items closest to the Baseline One loop (money, goods, power, civic services, saves). Anything not read in code is marked unverified.

### FEAT-2326609730 Goods and commodity economy
1. Industry, mine and farm buildings produce a named output good per tick into a per-city stock (unverified: which module owns the stock; candidate engine.market or a new engine.goods); the catalogue's goods/materials/freight text must match a real producer-consumer pair.
2. At least one consumer draws from that stock (retail and construction first); consumption is no longer a flat population multiplier for those goods. Gate: a Go test where output is doubled and a consumer's draw or price visibly changes, and a test where output is zero and consumers register a shortfall.
3. Shortfall and surplus reach the player: one coded signal per good in the existing advisor or debug feed, with a regression test that fails if the signal is never emitted.
4. Determinism gate (detgate) unchanged for fixtures that do not enable goods; a new fixture covers the enabled path. Failure caught: built-but-not-wired producers with no consumer.
5. Numbers (prices, rates) are placeholders with directional tests only; Aaron rules them in the balance pass.

### FEAT-2326609741 Grid transmission capacity
1. Each power link carries a capacity value in the data file with a disclosure sibling; a plant's output beyond the connecting link's capacity is clipped and counted as curtailed.
2. When demand routed over a link exceeds capacity the grid reports OVERLOAD for that link, observable in the existing power view or debug feed with a registry code.
3. Gate: Go test with one large plant on an undersized link (curtailment > 0, overload raised) and the same plant on an adequate link (zero curtailment, no overload).
4. Super/transmission versus local distribution is a two-tier distinction in the data, not two code paths; unverified whether the existing grid module (MOD-062) has the node/edge model needed.
5. Depends on FEAT-2326609739 (multi-reactor plant) only for a motivating fixture; not a build dependency.

### FEAT-2326609746 Zone-scoped tax wiring
1. A named spec/data entry selects a zone and a tax multiplier; internal/engine/tax RateInZone, RevenueInZone and BusinessRateRevenue gain a production caller (today only tax.go and tests reference RateInZone).
2. The multiplier is capped against the rate range maximum percent; a test with a multiplier above the cap proves it is clamped.
3. Gate: end-to-end test via compose where a zone rate change moves monthly revenue for that zone and no other, and a mutation that removes the caller turns it red.
4. The base-versus-rate split is documented in the package doc and checked by a doc-or-test assertion.

### FEAT-163 Auto-placed civic amenities
1. Data table (with disclosure siblings) lists each amenity, its population or coverage trigger and the auto-bus-stop ratio (about 200 dwellings per stop, from the item text).
2. Below the mega-city threshold, the amenity is placed once when its trigger is crossed, deterministically (same seed gives same placement hash); above it the player is prompted before heavy auto-expand.
3. A global master toggle and per-item toggles exist; with the master off nothing is auto-placed. Gate: three Go tests (on, off, per-item off) plus the detgate fixture.
4. Placement uses the feat.faith auto-build path, never the player BuildAPI; a test asserts the player build log is unchanged.
5. Failure caught: an amenity placed twice, or placed while its toggle is off.

### FEAT-164 Utility auto-expand at mega-city scale
1. Auto-expand applies to water treatment, sewage, solar and wind only above a named population threshold (data, disclosed); below it the player places them by hand.
2. On crossing the threshold the player gets a prompt; with consent the module adds capacity when headroom falls below a data-defined margin, and not otherwise.
3. Gate: test where demand rises past headroom with auto on (capacity added, cost charged to treasury) and auto off (shortfall reported, nothing added).
4. Same toggle structure as FEAT-163 (share the master toggle code, unverified whether it exists yet).

### FEAT-165 Body disposal pipeline
1. Deaths enter a per-city collection queue; each month a collection capacity (hearse or service units, data) clears the queue, and uncleared bodies increment a coded backlog signal.
2. Disposal is either in-area (cemetery plot or crematorium throughput, cheap) or out-of-area export contract (expensive), mirroring engine.refuse; costs are posted through finance in a phase visible to month end (see BUG-1141).
3. Cemetery plot depletion and crematorium throughput are enforced, not catalogue-only. Gate: Go tests for plot exhaustion forcing export, and for zero capacity producing a growing backlog with a wellbeing or health penalty (penalty number is a balance placeholder).
4. detgate unchanged for fixtures with no deaths wired.

### BUG-1133 Fuel duty ignores EV share
1. Evidence step: locate the inc7 fuel duty base (unverified file) and show whether it multiplies by full litres; paste the result as a BOW comment.
2. If confirmed, duty base = litres * (1 - evShare), with evShare read from the same source parkingFuel.ts uses (SSOT, GR#3).
3. Gate: test with evShare 0 (unchanged revenue), 0.5 (half the duty), 1 (zero duty and no division error). Failure caught: a copy of the share constant that drifts.
4. If the base already nets evShare, close the item with the test as the proof.

### BUG-1159 Uncoded load-refused error
1. Replace the free-text recordError in webconsole/src/sim/store.tsx:4154 (Load refused: no city named ${slug}) with a registry code created via tools/plan/add-error.js, with a template carrying the slug.
2. Gate: webconsole test that loads a missing city and asserts the recorded error has a MET- code, and the registry validator passes.
3. Failure caught: any other recordError call in store.tsx with a bare string; add a grep-style guard test for the file (unverified whether one exists).

### BUG-1170 Two tabs share one scratch lineage
1. Define the invariant: at most one tab may autosave into a given scratch lineage at a time (per-tab lease or single-writer guard, BUG-1156 r2 F3).
2. Gate: test simulating two tab contexts booting with a withheld pointer; the second either gets its own lineage or is refused autosave with a coded message, and the first lineage bytes are unchanged.
3. Failure caught: both tabs autosaving, producing the BUG-687 two-writer shape (lineage-blind overwrite).
4. Must not regress single-tab autosave timing; existing autosave tests stay green.

### BUG-1175 Cross-build scratch restore keeps old-engine state
1. Prerequisite first: guard rebuildLineageId so a Rebuild persists under the correct lineage and cannot overwrite the pointer lineage (BUG-1156 r3 F3).
2. Then enable the Rebuild prompt when a scratch restore crosses an engine build boundary (meta.appVersion differs).
3. Gate: test that restores a scratch save from an older build and asserts (a) the prompt is shown, (b) accepting rebuilds and saves under the intended lineage, (c) declining leaves the original lineage byte-identical.
4. Failure caught: silent retention of old-engine state, and the overwrite of the real city.
