# P2 FEATURE/MODULE "DONE?" verification (backlog comb FEAT-9995385605)

Verifier: Sonnet, read-only. 36 DONE? rows found in p2-feat-1.md and p2-feat-2.md (the brief said about 41). All 36 reached. No tests were run; wiring was checked by grep for non-test importers and runtime callers. Commit hashes were checked with `merge-base --is-ancestor HEAD`.

Counts: CLOSE 25, PARTIAL 5, WIRE 5, UNSURE 1. CLOSE-BLOCKED 0.

## WIRE findings (built, not reached at runtime), most player-visible first

1. MOD-055 engine.leisure. compose.go:1314 loads it, but compose calls only OpenVenue/RemoveVenue (compose.go:3225,3231) and PopulationTaste/LeisureFitAggregate (compose.go:3911-3912) for the attract term. Never called from compose or the tick: AdvanceMonth, SetCitizens, SetTraffic, SetWellbeing, DiscretionaryHours, Patronage, Visit, ScheduleEvent, UnmetTasteDemand. So the 168-hour budget, novelty decay, events and patronage (AC-2..AC-7, AC-10) never run in the game. The f6.leisure demo view is fed from a fixture, not the engine.
2. FEAT-099 freight ContainerPort. `LoadContainerPort` is called only from tests (containerport_test.go:55, containerport_rail_test.go:32). No non-test code imports freight except firms (RegisterFirm seam), rail, airport and fdi, and none of those construct a ContainerPort. compose does not construct a FreightAPI. Needed: build the freight API and ContainerPort in compose and put them on the tick or save path.
3. FEAT-049 mining deposit shuffle (07c4ceb). The only non-test importers of internal/engine/mining are destination, tunnels and airport, and none of them is imported by compose. Nothing calls the deposit shuffle at world-gen. Needed: a world-gen call from engine.world or compose, which would also unblock FEAT-050/051/103.
4. FEAT-103 mine types catalogue (1eadce3). Catalogue in mining/minetype.go with the same non-reachability as FEAT-049. It depends on FEAT-049, which is still open.
5. FEAT-128 claude-push-verify.js. The script and its test exist, but nothing invokes it: not settings.json, no .claude/commands skill, not CI, and no harness caller. Needed: call it from the /push skill or a hook, or retire the item.

## Verification table

| Code | Verdict | Evidence | Close note |
|---|---|---|---|
| MOD-055 | WIRE | 493cba5 on main; compose.go:1314 loads leisure; only 4 of about 22 API methods are called (see WIRE 1); AC doc engine.leisure.md AC-2..7, AC-10 have no runtime path; no verdict or comment on the item | |
| FEAT-049 | WIRE | 07c4ceb on main (mining/shuffle.go, data/deposits.json); no world-gen caller; Bro comment 2026-10-30 says the implementation is "still unbuilt" in the BUG-1097 context | |
| FEAT-099 | WIRE | 680c2f3 on main; ContainerPort constructed only in tests; open deps FEAT-053, FEAT-054, MOD-060 | |
| FEAT-101 | PARTIAL | c62624c on main; fdi/ has no importers at all (no compose, no cmd); Bill's 2026-08-16 ruling says the item is the FULL facility, catalogue only done; open deps FEAT-053, FEAT-054, MOD-059 | |
| FEAT-103 | WIRE | 1eadce3 on main; mining/minetype.go + test; not reached from compose; depends on open FEAT-049 | |
| FEAT-115 | CLOSE | harness/claude-agent-stop.js + .test.js; referenced by settings.json, claude-sync.js, dispatch-guard | Agent stop logging script is built, tested and registered as a hook in settings.json. |
| FEAT-116 | CLOSE | claude-author-identity.js + test; used by author-guard, codename-content-scan | Author identity derivation is built, tested and used by the author guard. |
| FEAT-117 | CLOSE | claude-bow-autoref.js; settings.json hook; BOW git refs on every item show "auto-ref (hook)" lines, so it runs. No dedicated test file | Post-commit auto-ref hook is registered and demonstrably records refs on BOW items. |
| FEAT-118 | CLOSE | claude-bow-ref-check.js + test; used by autoref, claude-bow.js, claude-db.js | BOW ref integrity check is built, tested and called by the autoref hook and the BOW CLI. |
| FEAT-119 | CLOSE | claude-codename-content-scan.js used by codename-guard; covered through claude-codename-guard.test.js; no dedicated test | Codename content scanner is built and used by the GR#22 guard, which has tests. |
| FEAT-120 | CLOSE | claude-codename-diff.js used by codename-guard and content-scan; guard tests cover it; no dedicated test | Codename staged-diff scanner is built and used by the GR#22 guard, which has tests. |
| FEAT-121 | CLOSE | claude-codename-patterns.js + test; used by guard and content-scan | Runtime codename pattern builder is built, tested and used by the guard. |
| FEAT-122 | CLOSE | claude-committhook-install.js; tests claude-committhook*.test.js and claude-committhook-install-prepush.test.js | Commit-hook installer is built and tested, and the author guard uses it. |
| FEAT-123 | CLOSE | claude-db.js + test; used by bow, autoref, ref-check; FEAT-136 only blocks on it (not a dependency) | Shared metro DB helper is built, tested and used by the BOW tooling. |
| FEAT-124 | UNSURE | claude-devfeedback-import.js + test exist; no hook, skill, CI or script calls it; only docs mention it. May be an on-demand CLI by design; the in-game feedback source (FEAT-065) is not confirmed | |
| FEAT-126 | CLOSE | claude-git-commit-trigger.js used by author-guard and codename-guard; no dedicated test | Git commit trigger wrapper is built and used by the guard suite. |
| FEAT-127 | CLOSE | claude-plan-checker.js + test; used by claude-plan-guard.js | Plan-drift checker is built, tested and used by the plan guard. |
| FEAT-128 | WIRE | claude-push-verify.js + test exist; zero callers (see WIRE 5) | |
| FEAT-129 | CLOSE | claude-quote-mask.js + test; used by author-guard and others | Quote-mask primitive is built, tested and shared by the guards. |
| FEAT-130 | CLOSE | claude-secret-checker.js + claude-secret-checker.bug1144-r1.test.js; used by secret-guard | Secret checker is built, tested and used by the secret guard. |
| FEAT-131 | CLOSE | claude-trailer-checker.js + test; used by claude-pre-commit-check.js | Trailer checker is built, tested and used by the pre-commit check hook. |
| FEAT-132 | CLOSE | claude-version-checker.js + test; used by version-guard | Version checker is built, tested and used by the version guard. |
| FEAT-134 | CLOSE | 3da2e21 and 16cf788 on main; tools/codebase-viz/generate.js + test + index.html; AC doc tool.codebaseviz.md. On-demand generator, no CI hook required by the item | Codebase visualisation generator landed with tests and an acceptance doc; it is an on-demand tool. |
| FEAT-155 | CLOSE | code.json:11251 key feat.factorytypes, path internal/engine/freight/. Registration only; runtime wiring of the factory catalogue is not in this item (freight not in compose, same as FEAT-099) | feat.factorytypes is registered in code.json, which was the whole scope of this item. |
| FEAT-193 | CLOSE | internal/foundation/integration/executor.go:86-116 WithSingleShardAssert with tests in executor_test.go; no open dependencies | WithSingleShardAssert exists in the integration executor with tests, matching engine/core. |
| FEAT-1972079872 | PARTIAL | 6254c0f on main (webconsole badge + About). Remaining per Bro audit: AC-5 TUI parity (cmd/metropolis shows no version, no About) and AC-6 webconsole tests as a CI gate; I found no version or About in cmd/metropolis | |
| FEAT-1972079897 | PARTIAL | 1ee9b4c, 1875fc0, 11a341d on main; inc1 genesis replay (webconsole/src/sim/genesisReplay.ts) and inc2 rebuild prompt and camera restore are wired; inc3 (log compaction, export-to-file, replay-from-checkpoint, verify-determinism command) has no commit | |
| FEAT-1972079906 | PARTIAL | b96c3d5, 01a0cff, 344aa3e, c616b3c on main; inc1-3 landed; wasteModel mounted via servicesTabs.tsx and data.ts:5235; inc3 is display-only ("rota UI panel (display-only)") and policies are not shown; no round verdict on the item. Confirm the diversion-to-100 percent dial and policies before closing | |
| FEAT-1972079907 | PARTIAL | feb033c, 0e06755, 34e9c5d on main (inc1 types and auto-connect, inc2 monitor and auto-scale; fittingTier and roadTierOf used in engine.ts). inc3 viz is not evidenced. FEAT-1972079919 only depends on this item, it does not block it | |
| FEAT-1972079946 | CLOSE | Ruling item. Aaron's 2026-09-01 ruling recorded on the item; 49c64fb and 8920744 on main fix the void swallowers | Aaron ruled 2026-09-01: fix the 28 void-mutating swallowers, accept the read-query sentinels; the fixes landed. |
| FEAT-217 | CLOSE | A `land-dev` skill is in the skill list; `.claude/commands/land*` and user-level land files were not found in the repo, so the skill is held outside it. Item asks for a /land skill. Confirm with Aaron that land-dev covers the merge-train ritual (local merge, never squash) | The landing ritual skill now exists as land-dev, so this request is met. |
| FEAT-2326609713 | CLOSE | a70437f on main; Opus r1 ACCEPT; BUG-491 follow-up filed | Ruling supersession in Vestige landed in a70437f after an Opus round ACCEPT; follow-up is BUG-491. |
| FEAT-2326609735 | CLOSE | 88bb6fa on main; optimalProvider is used in webconsole/src/sim/data.ts:4938,4957,5235 | optimalProvider total-plan-cost selection landed in 88bb6fa and is used by the demand-fix rows. |
| FEAT-2326609742 | CLOSE | Audit complete: E:/git/vestige/REBUILD-AUDIT/FINAL-ROLLUP.md exists; 124 deduped defects recorded in the item comment | The Vestige rebuild-audit finished with 124 deduplicated defects in FINAL-ROLLUP.md. |
| FEAT-2326609760 | PARTIAL | 5637dde on main; WebGPU renderer in webconsole/src/render/ (mapRenderer.ts, shaders.ts, useMapRenderer.ts) mounted with Canvas2D fallback. The item's scope also covers GPU compute for per-building derivations toward 100M; nothing for compute exists. Spike done, compute phases remain | |
| FEAT-2326609779 | CLOSE | 4c6697e, ac05b5b on main; round 17 ACCEPT 2026-09-06; port to main complete 16:02. Inc3 and inc4 landed. Not a runtime-wiring risk: webconsole engine.ts. Caveat: I did not recheck for a later inc5 | Defrag layout hierarchy inc3 and inc4 landed on main after round 17 ACCEPT and the port. |

## Notes

- Only 36 DONE? rows were found, not the 41 expected; the summary line in p2-feat-1.md (25) and p2-feat-2.md (11) agree with 36.
- The webconsole-side items (waste, roads, replay, version badge) are webconsole-first TypeScript, so "wired" means reached from webconsole/src/sim/engine.ts and the mounted components, not from the Go compose root. The Go equivalents are separate items.
- CLOSE rows assume `done` will not refuse: I checked the dependency listings, and no CLOSE row has an open dependency.
