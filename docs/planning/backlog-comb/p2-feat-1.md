# Backlog comb: p2-feat-1 (86 items)

## Summary
Counts by class: STRONG 28, DONE? 25, WEAK 28, NONE 1, DUP 3, RULING 1.
Counts by game value: GAME 56, ENABLER 5, INFRA 25.

Caveats: the slice JSON was deleted from temp/backlog-comb mid-run, so codes, mkeys and descriptions were read before it vanished, and some BOW comment checks were skipped. Reality checks were file or grep level only. "Unwired" means no non-test importer of the package; verdicts were not checked. All rows got a real look at title and description plus criteria-doc and code existence; none is title-only.

The 5 most worth doing next:
1. FEAT-170 housing adequacy and moving home: most player-felt item here; the crowding driver exists, relocation does not. Criteria below.
2. FEAT-157 game-speed pacing: small remaining delta, directly affects watchability.
3. MOD-040 dispatch (with Aaron's SLA ruling): criteria strong, module missing, makes traffic matter.
4. FEAT-161/162 then MOD-084 transit: criteria exist; gives lines and saturation proposals.
5. MOD-049 inc2 surplus export: inc1 landed; Aaron expected excess power to be sold in dogfood.

Close or merge: the 18 tool.* items (FEAT-115..132) plus FEAT-134, FEAT-155, FEAT-193, FEAT-049, FEAT-099, FEAT-101, FEAT-103 look built (DONE?); verify and close in one sweep. Merge FEAT-095 into MOD-074; FEAT-096, MOD-075 and FEAT-183 into one airport item; FEAT-125 into FEAT-115; FEAT-067 and FEAT-147 into one weather item (or under FEAT-012). MOD-055 and MOD-049 need per-increment tracking rather than a blanket close.

Rulings Aaron owes: FEAT-186 housing styles; FEAT-053 permit expansion-threshold shape; MOD-040 SLA numbers and MOD-049 tariff are placeholders for the balance pass; FEAT-067 easy-mode trade.

Pattern: many module items (MOD-048..062, 074) have built packages with no non-test importer ("built but not wired"). Their criteria are fine; what is missing is a wiring item and a gate that runs the binary.

## Items
| Code | P | Type | Class | Value | Note (<= 25 words) |
|---|---|---|---|---|---|
| MOD-039 | P2 | module | STRONG | GAME | engine.refuse.md 18 AC; package 14 files, 2 importers; verdict and wiring state not checked. |
| MOD-040 | P2 | module | STRONG | GAME | 19 AC plus Aaron SLA ruling 2026-09-01; internal/engine/dispatch does not exist; standing REJECT. SLA numbers are placeholders. |
| MOD-045 | P2 | module | STRONG | GAME | 17 AC; BDI engine unbuilt (farming pkg is the FEAT-104 catalogue, BUG-250/251). Huge scope; consider splitting. |
| MOD-048 | P2 | module | STRONG | GAME | 15 AC; commit 7a79103 landed eras, remote work, e-commerce; package has zero non-test importers (unwired). |
| MOD-049 | P2 | module | STRONG | GAME | 13 AC; inc1 power grid export landed 48c45d5 (placeholder tariff); inc2/inc3 open. Track per increment. |
| MOD-050 | P2 | module | STRONG | GAME | 14 AC; package built (4 files), no importers; BOW said destructive round pending; unwired. |
| MOD-051 | P2 | module | STRONG | GAME | 15 AC; package built, 2 importers; wiring and verdict state unverified. |
| MOD-054 | P2 | module | STRONG | GAME | 15 AC; built on lane/ben, zero importers; small-money system, low Baseline One priority. |
| MOD-055 | P2 | module | DONE? | GAME | Feature commit 493cba5; 23 Go files, 4 importers. Check verdict then close or list remaining AC. |
| MOD-056 | P2 | module | STRONG | GAME | 15 AC; package 6 files, no importers; unwired. |
| MOD-057 | P2 | module | STRONG | GAME | 20 AC; AC-7/8/9 were blocked on BUG-058 edges; package 8 files, 1 importer. Verify block cleared. |
| MOD-059 | P2 | module | STRONG | GAME | 13 AC; anchor.go self-declared stub; real FDI engine unbuilt; no valid destructive verdict (Bev correction). |
| MOD-060 | P2 | module | STRONG | GAME | 11 AC; only compile-stub IntermodalTransfer exists; fleet, depot and scheduling unbuilt. |
| MOD-062 | P2 | module | STRONG | GAME | 11 AC; built (6 files), zero importers; two BUG-058 mechanics blocked (commodity pricing, terraces equity). |
| MOD-074 | P2 | module | STRONG | GAME | 17 AC; package built (8 files), zero importers; Aaron approval-sentiment ruling 2026-09-01 present. |
| MOD-075 | P2 | module | WEAK | GAME | engine.airport.md is 7 lines with no AC; duplicates FEAT-096 which has 18 AC. |
| MOD-081 | P2 | module | WEAK | GAME | No mkey, no criteria doc, no gate; overlaps FEAT-148 renewables. |
| MOD-084 | P2 | module | WEAK | GAME | No engine.transit.md; AC exist only under FEAT-161/162 docs; package absent. |
| FEAT-012 | P2 | feature | STRONG | GAME | 15 AC; internal/engine/events path does not exist; unbuilt. |
| FEAT-022 | P2 | feature | STRONG | GAME | 13 AC in ui.screen.districts.md; path names the TUI, check against the webconsole reality. |
| FEAT-049 | P2 | feature | DONE? | ENABLER | Seeded deposit shuffle landed 07c4ceb (engine.mining). Close unless AC remain. |
| FEAT-050 | P2 | feature | STRONG | GAME | 12 AC, four-tier survey ruling; docs commit bb24cf0; implementation beyond the prospect gate unverified. |
| FEAT-051 | P2 | feature | STRONG | GAME | 17 AC; extraction ladder and depletion; mining package has 5 importers, remaining scope unverified. |
| FEAT-052 | P2 | feature | STRONG | GAME | 17 AC; commodity market and chains; large, consider splitting market from refinement chain. |
| FEAT-053 | P2 | feature | STRONG | GAME | 15 AC; cross-cutting permit gate; BA left the expansion-threshold shape open (RULING). |
| FEAT-054 | P2 | feature | STRONG | GAME | 13 AC; liability accrual via finance; no code check made. |
| FEAT-067 | P2 | feature | NONE | GAME | Description empty; feat.weathermode.md exists but item has no mkey; overlaps FEAT-147. |
| FEAT-081 | P2 | feature | WEAK | INFRA | code.json has no per-module version field (0 hits); fix sketched, no gate named. |
| FEAT-092 | P2 | feature | STRONG | GAME | 13 AC in feat.repairfacilities.md; depends on engine.maintenance. |
| FEAT-093 | P2 | feature | WEAK | GAME | Doc exists (84 lines) but no AC-numbered lines; no code under internal/engine; graph suite undefined. |
| FEAT-095 | P2 | feature | DUP | GAME | Duplicates MOD-074 engine.airunits (helicopters, four roles); criteria are in engine.airunits.md. |
| FEAT-096 | P2 | feature | DUP | GAME | Duplicates MOD-075 engine.airport; this one holds the 18 AC. Merge, keep one. |
| FEAT-099 | P2 | feature | DONE? | GAME | Container port landed 680c2f3 (engine.freight); 14 AC doc. Verify then close. |
| FEAT-101 | P2 | feature | DONE? | GAME | Pharma campus catalogue landed c62624c; FEAT-156 export edge still open. |
| FEAT-102 | P2 | feature | STRONG | GAME | 14 AC; chemicals package exists, zero importers; build state unverified. |
| FEAT-103 | P2 | feature | DONE? | GAME | Mine types catalogue landed 1eadce3; 12 AC doc. |
| FEAT-115 | P2 | feature | DONE? | INFRA | harness/claude-agent-stop.js and test exist; 14 AC doc. Close. |
| FEAT-116 | P2 | feature | DONE? | INFRA | harness/claude-author-identity.js and test exist. Close. |
| FEAT-117 | P2 | feature | DONE? | INFRA | harness/claude-bow-autoref.js exists; no test file found. Close or add a test. |
| FEAT-118 | P2 | feature | DONE? | INFRA | harness/claude-bow-ref-check.js and test exist. Close. |
| FEAT-119 | P2 | feature | DONE? | INFRA | harness/claude-codename-content-scan.js exists; no test file found. |
| FEAT-120 | P2 | feature | DONE? | INFRA | harness/claude-codename-diff.js exists; no test file found. |
| FEAT-121 | P2 | feature | DONE? | INFRA | harness/claude-codename-patterns.js and test exist. Close. |
| FEAT-122 | P2 | feature | DONE? | INFRA | harness/claude-committhook-install.js and test exist. Close. |
| FEAT-123 | P2 | feature | DONE? | INFRA | harness/claude-db.js and test exist. Close. |
| FEAT-124 | P2 | feature | DONE? | INFRA | harness/claude-devfeedback-import.js and test exist. Close. |
| FEAT-125 | P2 | feature | DUP | INFRA | Same script family as FEAT-115 (dispatch/stop log, FEAT-076); claude-dispatch-log.js exists. Merge and close. |
| FEAT-126 | P2 | feature | DONE? | INFRA | harness/claude-git-commit-trigger.js exists; no test file found. |
| FEAT-127 | P2 | feature | DONE? | INFRA | harness/claude-plan-checker.js and test exist. Close. |
| FEAT-128 | P2 | feature | DONE? | INFRA | harness/claude-push-verify.js and test exist. Close. |
| FEAT-129 | P2 | feature | DONE? | INFRA | harness/claude-quote-mask.js and test exist. Close. |
| FEAT-130 | P2 | feature | DONE? | INFRA | harness/claude-secret-checker.js exists; BUG-1144 test present. Close. |
| FEAT-131 | P2 | feature | DONE? | INFRA | harness/claude-trailer-checker.js and test exist. Close. |
| FEAT-132 | P2 | feature | DONE? | INFRA | harness/claude-version-checker.js and test exist. Close. |
| FEAT-134 | P2 | feature | DONE? | INFRA | Codebase viz landed 3da2e21; 14 AC doc. Close. |
| FEAT-135 | P2 | feature | WEAK | INFRA | Finding narrative, no gate; feat.securehelpers.md exists but item has no mkey. Needs a scoped deliverable. |
| FEAT-138 | P2 | feature | STRONG | GAME | 14 AC; Aaron ruling 2026-08-20, light background dimension. Low priority. |
| FEAT-139 | P2 | feature | STRONG | GAME | 16 AC; harbour, fishing and ferry; depends on freight and coastal. |
| FEAT-140 | P2 | feature | STRONG | GAME | 18 AC; hobbies and venues atop MOD-055. |
| FEAT-144 | P2 | feature | WEAK | GAME | Shared profile schema, no mkey, no doc, no gate. |
| FEAT-147 | P2 | feature | WEAK | GAME | Three-sentence wish, no numbers, no gate; overlaps FEAT-067 and FEAT-012. |
| FEAT-148 | P2 | feature | WEAK | GAME | Renewables by area allocation; no doc, no gate; overlaps MOD-081. |
| FEAT-149 | P2 | feature | WEAK | INFRA | tools/bow-server.js exists; named bow-ui html files absent. Mostly built; state the remaining delta. |
| FEAT-150 | P2 | feature | WEAK | INFRA | codejson-audit.js already has orphan-directory logic; the double-entry assertion is unproven. Add a test. |
| FEAT-152 | P2 | feature | WEAK | ENABLER | Story from ASM-1039; protocol-edge change with no gate or contract. |
| FEAT-153 | P2 | feature | WEAK | ENABLER | Story from ASM-1056; subscription surface undefined. |
| FEAT-154 | P2 | feature | WEAK | ENABLER | Story from ASM-1344; command kind undefined. |
| FEAT-155 | P2 | feature | DONE? | INFRA | feat.factorytypes is registered in code.json (line 11251). Close. |
| FEAT-156 | P2 | feature | WEAK | ENABLER | AddExports interface exists in fdi/pharmacampus.go; edge and FreightAPI.Export wiring unproven. |
| FEAT-157 | P2 | feature | WEAK | GAME | Pacing code exists in cmd/metropolis/boot.go and metroserve; remaining delta (8x ungated) not stated. |
| FEAT-161 | P2 | feature | STRONG | GAME | FEAT-161.md criteria exist (commit ce89cd5); keep as the AC carrier for MOD-084. |
| FEAT-162 | P2 | feature | STRONG | GAME | FEAT-162.md criteria exist; propose-at-cost with x2/x5/x10/x50 rates. |
| FEAT-170 | P2 | feature | WEAK | GAME | Strong player-felt idea; PersonsPerRoom exists but no relocation mechanic found; no gate. |
| FEAT-171 | P2 | feature | WEAK | GAME | Framework wish; no first deliverable, gate or default values. |
| FEAT-172 | P2 | feature | WEAK | GAME | Park policy; targets, tiers and numbers unspecified; no code found. |
| FEAT-173 | P2 | feature | WEAK | GAME | Cleanliness score; no formula, tiers or gate; no code found. |
| FEAT-175 | P2 | feature | WEAK | GAME | Rich research, no testable scoring contract; defer behind FEAT-172. |
| FEAT-176 | P2 | feature | WEAK | GAME | Rail priority routing; clear design, no gate; depends on unbuilt MOD-060. |
| FEAT-177 | P2 | feature | WEAK | GAME | Frequency tiers; depends on unbuilt MOD-060 and FEAT-176. |
| FEAT-180 | P2 | feature | WEAK | GAME | Interchange hubs; transfer penalty undefined; hyperloop belongs in a separate item. |
| FEAT-181 | P2 | feature | WEAK | GAME | Bus topology; hub-and-spoke default and surge trigger undefined; overlaps FEAT-161. |
| FEAT-182 | P2 | feature | WEAK | GAME | Off-map capacity pipe model; numbers and gate undefined. |
| FEAT-183 | P2 | feature | WEAK | GAME | Airport tiers; overlaps FEAT-096/MOD-075 tier ladder (ASM-1146..1164). |
| FEAT-185 | P2 | feature | WEAK | GAME | Hotel auto-provision plus unhappiness; rates unspecified; depends on FEAT-171. |
| FEAT-186 | P2 | feature | RULING | GAME | Style choice needs Aaron: which styles, density numbers, art and UI cost. |
| FEAT-193 | P2 | feature | DONE? | INFRA | WithSingleShardAssert exists in internal/foundation/integration/executor.go with tests. Close. |

## Improved criteria
Thresholds and values are placeholders for Aaron's balance pass; unverified facts are marked.

### MOD-075
1. Merge into FEAT-096 (18 AC in feat.airport.md); retarget engine.airport.md to those AC.
2. Gate: go test ./internal/engine/airport/... proves a tier upgrade is refused without land and that runway/terminal capacity caps flights.
3. Red when a provincial airport accepts international traffic.

### MOD-081
1. Add mkey engine.power and docs/planning/acceptance/engine.power.md.
2. Observable: a nuclear station without trained operators produces zero MW; with them it raises grid supply and leaves a decommission liability (FEAT-054).
3. Gate: unit test on supply versus consumption network; red when output ignores staffing.

### MOD-084
1. Point engine.transit.md at the FEAT-161 and FEAT-162 AC rather than duplicating.
2. Gate: a line gets a unique deterministic route id, ordered named stops and exactly one vehicle at creation.
3. Red when two runs of one seed give different ids.

### FEAT-067
1. Name the switch: seed flag WeatherDamage hard or easy, stored in the save.
2. Hard: storm events damage assets deterministically from the seed RNG; easy: none, with the stated trade (unverified what the trade is; needs Aaron).
3. Gate: same seed gives a bit-identical damage log; easy mode logs zero damage.

### FEAT-081
1. Each code.json module gets an integer version carried forward by tools/plan/generate.js.
2. Editing a module's files without bumping version fails the commit-path gate.
3. Gate: generate.js --check on a one-file edit; red when version is unchanged.

### FEAT-093
1. Define the logged events (build, demolish, policy change) as a typed list.
2. Graphs: population, money, maintenance spend, births and deaths derived from the log.
3. Gate: replay a seed; log length and graph series match engine totals.

### FEAT-135
1. Deliver one named package of helpers (input validation, encapsulation) under internal/foundation with tests.
2. Gate: astgate flags the recurring pattern when a helper is bypassed.
3. Metric: open input-validation findings fall from the item's 26.

### FEAT-144
1. Define the profile schema as a data file plus a JSON validation test.
2. Gate: every mega-facility in the catalogue has a profile; a missing field fails the validator.
3. Red when a profile references an unregistered commodity or network.

### FEAT-147
1. With weather ON, a storm above the wind threshold fails turbines at a data-driven rate.
2. With weather OFF, zero damage.
3. Gate: determinism test over a seed; red when OFF mode damages assets.

### FEAT-148
1. Allocating N cells of land or sea sets capacity MW by a data-driven formula.
2. Gate: grid supply rises by the formula; offshore requires sea cells.
3. Red when capacity stays flat after growth.

### FEAT-149
1. State the delta: add the HTML template and bow-ui.html, or drop them from the description.
2. Gate: node tools/bow-server.js serves the list on port 8765 and refuses writes.
3. Red when a write request succeeds.

### FEAT-150
1. Assert a directory cannot be both orphaned and registered in code.json.
2. Gate: a fixture code.json with a conflicting directory makes node tools/plan/codejson-audit.js exit non-zero.
3. Red when the fixture passes.

### FEAT-152
1. Add a CitizensAPI command writing attainment and stage history to the citizen record.
2. Gate: education reads attainment back from citizens and its own copy is removed.
3. Red when the two stores disagree.

### FEAT-153
1. CitizensAPI offers a subscription to life events (death, emigration).
2. Gate: education RemovePupil is driven by it and the manual bridge deleted.
3. Red when a death leaves a pupil enrolled.

### FEAT-154
1. Add a LifeEventSocial command kind carrying the AC-6 marker.
2. Gate: an engine.social test shows the marker written via the new kind, not LifeEventHealth.
3. Red when HealthBand changes from a social write.

### FEAT-156
1. Register the outbound edge pharmacampus to freight in master-plan and code.json.
2. Gate: TradeEdge.AddExports calls FreightAPI.Export; a test asserts tonnes reach freight stock.
3. Red when exports vanish.

### FEAT-157
1. List the delta: ungate 8x fastest in production and confirm a wall-clock pump drives AdvanceTicks.
2. Gate: a test that 1x/2x/4x/8x produce tick counts in ratio over a fake clock; pause yields zero.
3. Red when state differs between speeds (determinism).

### FEAT-170
1. A household of 4 in a 1-room home moves to a vacant adequate home within a stated tick count.
2. Crowding penalty falls after the move, tested through PersonsPerRoom.
3. Gate: deterministic seed test; red when no vacant home exists and the household still moves.

### FEAT-171
1. First deliverable: one policy target and toggle (default on early).
2. Gate: set the recreational target; the dept builds to reach it and stops.
3. Red when the player toggle is ignored.

### FEAT-172
1. Target m2 per person drives park creation; test with a growing population.
2. Maintenance tier changes wellbeing and upkeep by data-driven factors.
3. Gate: wellbeing green-space driver rises with tier.

### FEAT-173
1. Score per area degrades with footfall and is restored by cleaners to the policy level.
2. Gate: higher density gives a lower score after N ticks; attract reads it.
3. Red when cleaners at zero leave the score flat.

### FEAT-175
1. Park value formula as data: sum(features) x demographic fit x usability gate.
2. Gate: a park with broad coverage outscores a narrow one with equal raw points.
3. Red when raw points alone decide.

### FEAT-176
1. A single line forbids overtaking except at a siding.
2. Gate: an express passes a cargo at a loop in a deterministic scenario.
3. Red when two trains occupy one segment.

### FEAT-177
1. Frequency tier is chosen from demand, capped by line paths and depot trains.
2. Gate: raise demand to saturation; an expansion proposal is raised (FEAT-162), no automatic capacity change.
3. Red when the tier exceeds the path limit.

### FEAT-180
1. Transfer penalty in minutes per interchange as data.
2. Gate: a hub with a lower penalty raises mode share in a logit test.
3. Red when the penalty has no effect. Split hyperloop out.

### FEAT-181
1. Lines converge at a terminus by default; toggle off allowed.
2. A surge OD pair triggers a direct line, retired after the surge.
3. Gate: scenario test; red when the direct line persists.

### FEAT-182
1. Each off-map link is a capacity-bounded pipe; congestion rises toward capacity.
2. Gate: loading past M20 capacity raises delay and an expansion proposal appears.
3. Red when delay stays zero over capacity.

### FEAT-183
1. A tier upgrade is blocked without land and allowed with it.
2. Passenger volume creates rail, bus and hotel demand.
3. Gate: upgrade test; merge with FEAT-096.

### FEAT-185
1. Hotel supply grows with visitor demand under policy.
2. A resident in a hotel beyond N nights loses happiness points.
3. Gate: wellbeing drop and a housing-demand signal in a test.
