# HEAVY CHECKPOINT — session bounce point

**Rebuilt by RM, refresh #3, 2026-08-09 (post session-bounce recovery, then two same-day Bill updates: v1.6 second-Tester + Azure ruling, then v1.7 assumption-logging rule). HEAD is `27c8c3d`, pushed. This file is the authoritative recovery surface — a fresh session recovers from THIS + `node claude-bow.js list --by-seq` + `git log -15`.**

## 1. Where we are

- **Sprint 0: build scope 100% COMPLETE.** Cloud provider decision RESOLVED (2026-08-09 ruling): **Azure, confirmed** — see §6. Sprint 0 now closes on the **contract freeze review only** — packet in `docs/design/README.md` (4 contract docs + cross-cutting questions: OD f32-vs-f64 [f32 recommended], duplicate correlation-ID generators). **Still pending Aaron.**
- **Sprint 1: 9 of 10 items done+committed.** harness.stub (`112f880`), ui.core (`87d8efe`), ui.widgets (`bf65b9a`), foundation.det (`f24f9ff`), engine.core (`f81e5d7`, Tester-PASSED), feat.detgate (`47de5d0`, Tester-PASSED, closes BUG-002), ui.screen.map (`b721740`, Tester-PASSED), **ui.screen.debug DONE (`190f02a`)**, **feat.debugmode DONE (`a364855`)**. **Last item, feat.skeleton (FEAT-006): Tester-1 PASSED every hard-gated criterion (2026-08-09 13:12) — awaiting doc pass, then Bill's commit. Sprint 1 exit gate is one step from closed.**
- **Last commit:** `27c8c3d` (tool.bow — assumptions-as-BOW-items, v1.7 mechanics), pushed to origin/main.
- **Dev slots: 0/4 in flight** — the FEAT-007/FEAT-008 wave landed and closed; nothing new dispatched yet pending this refresh's recommendation.

## 2. Verification history (all three "sync git" deliveries now closed)

The three items Bill committed ahead of Tester verdict on Aaron's direct order (2026-08-09 AM) are **all Tester-PASSED and DONE**: engine.core (MOD-012, `f81e5d7`), feat.detgate (FEAT-004, `47de5d0`, also closes BUG-002), ui.screen.map (FEAT-005, `b721740`). BUG-003 (BOW hooks lookup drift, fix `f7815b7`) is also Tester-re-verified DONE. This section is kept for provenance only — no open action here.

## 3. v1.7 — Assumptions are logged or the work is rejected (Aaron, 2026-08-09, committed `27c8c3d`)

**Principle:** the standard is that the *criterion holds*, not that the *test passes*. A test proves what it asserts; a criterion states what must be true; the gap between them is where silent assumptions live.

**Mechanics (built, live now):**
- New BOW item type **`assumption`**, `ASM-` codes. `--code-path` and `--codejson` are mandatory and tool-enforced — an assumption untraceable to code is a note, not a record.
- **Reciprocal rejection duties**: developer must reject an ask if the BA's criteria rest on unlogged assumptions (bounce to lead, do not silently resolve it); Tester must actively hunt for assumptions and FAIL work carrying unlogged ones **even if every criterion passed**; BA logs assumptions made while writing criteria; **lead is bound too** — a lead ruling is itself an assumption unless written down.
- Every future agent spawn carries the **mandatory briefing block** recorded in `docs/planning/dev-team-process.md` §"Mandatory spawn block (v1.7)" — read it from there, do not re-type it into agent prompts from memory.

**Six assumptions logged retrospectively from 2026-08-09's work** (`node claude-bow.js show ASM-00N` for full text):

| Code | Pri | One-line | Status / owner action |
|---|---|---|---|
| ASM-001 | P1 | Sprint 1 exit gate does **not** prove `engine.core` participates in the live rendered path — the binary renders via `harness.stub`; `engine.core`'s determinism is proven only in isolation by `feat.detgate`. Satisfies criteria as written, matches stub-everything discipline, but is weaker than the informal reading of "exit gate". | **Bill's own disclosure item** — he states this plainly at Aaron's demo. No dev action. |
| ASM-002 | P2 | F12's stub/ok registry rendering is proven structurally, not by literal end-to-end execution. | Awaiting Bill's ruling (accept/correct/escalate) per v1.7. |
| ASM-003 | P2 | AC-12: a failed persist leaves the header flagged in memory while debug stays off (over-flag, never under-flag). | Awaiting Bill's ruling. |
| ASM-004 | P2 | The F12 phase-name mirror is acceptable duplication because the drift test catches divergence. | Awaiting Bill's ruling. |
| ASM-005 | P2 | engine.core's pacing constant as a named Go var satisfies GR#15 in the interim. | Superseded by **FEAT-030** (pacing constant to config) once built — see §4. |
| **ASM-006** | **P1** | Deferring the F1 overlay cycle assumes the renderer can accept a background-metric layer **additively** later. If wrong, **FEAT-031 is a renderer rewrite, not a feature.** | **Bill wants a deliberate check BEFORE FEAT-031 is estimated** — a spike/investigation, not a straight dev dispatch. See §4 ranking. |

## 4. RM's ranked dispatch recommendation (once FEAT-006 lands — refresh #3, validated against live BOW)

Ten follow-up items surfaced today (FEAT-030–034, BUG-004–010) — status checked individually rather than assumed; BUG-004 and BUG-005 are already DONE.

**Rank 1 — BUG-007 (P1), internal/protocol transport `Close()` TOCTOU race.** Real shipped-code bug (not a test artifact): `Close()` closes `resultCh`/`eventCh`/`deltaCh` with no synchronisation against an in-flight send; `trySendEvictOldest`'s closed-check is a check-then-act race, failure mode is a **send-on-closed-channel panic**, not a benign data race. Tester-2 confirmed **zero test coverage in either direction** — the one test that looks like it should cover this (`TestInProcTransport_Race`) completes all goroutines before calling `Close()`. `internal/protocol` is the transport every UI⇄engine command/event/delta crosses — this is the highest blast-radius open defect in the repo. Lead-ruled DoD: dedicated `-race` regression exercising concurrent send-vs-Close, and BUG-005's subscription test must be able to call `Close()` again. **Recommend dispatching first, ahead of any Sprint 2 feature work.**

**Rank 2 — BUG-008 (P1), error registry incomplete — answers Bill's direct question: yes, jump the queue.** Codes raised in shipped Go source (`MET-E000-E010`, `E100-E103`, `F100-F105`, `F200-202+F220`, `F300`, `F600-606`, `P090/P091`) were never registered. This already caused a **real collision**: the feat.debugmode junior legitimately found `E100-E199` "free" in `data/errors.json` and claimed it, head-on into detgate's already-shipped `E100-E103` which only existed in source. Caught at lead review by luck, not by any mechanism. DoD explicitly includes the guardrail Bill is asking about: a check (script/CI step) that **fails the build** when source raises a code the registry doesn't know. That's the load-bearing reason to jump it — every day it stays open, every new module's error-range claim is a live collision risk, and today's near-miss is direct evidence the manual process doesn't catch it reliably. Bundle in the existing registration batch (MET-E/F/P ranges listed above) plus the mechanical check.

**Rank 3 — BUG-006 (P1), no post-push CI visibility / no branch protection.** Root cause of BUG-004 surviving since commit 1: nobody was watching. Interim control already in force (manual `gh run list --limit 1` after every push) mitigates the acute risk today, so this is lower urgency than 007/008, but DoD is cheap (branch-protection config + a `/commit` skill edit) — recommend bundling into the same wave as a low-cost item, not a full dev slot.

**Rank 4 — BUG-009 (P2), `handleSetSpeed` doesn't consult the debug gate.** Small, well-scoped, dependency (FEAT-008) is now satisfied (`a364855` landed) — ready now. Cheap filler for whichever slot has room.

**Rank 5 — FEAT-030 (P2), pacing constant to config.** Directly resolves ASM-005. Ready, no blockers, moderate value.

**Hold — ASM-006 spike before FEAT-031 is estimated.** FEAT-031 (F1 overlay cycle, P1) must **not** be dispatched or estimated until someone deliberately checks whether the renderer accepts an additive background-metric layer. Recommend a short investigation (BA or dev spike, Bill's call on who) reporting back to Bill before FEAT-031 goes anywhere near a junior.

**Blocked, not ready:** FEAT-032/FEAT-033 (regression tests via ui.harness/ui.keys) depend on MOD-014/MOD-011, neither built yet — resume once Sprint 2 (harness.replay MOD-013 → ui.harness MOD-014, and ui.keys MOD-011) is back in motion. FEAT-034 (P3, buildinfo host field) is trivial, no urgency, park as filler.

**Resuming Sprint 2 proper:** harness.replay (MOD-013) and ui.keys (MOD-011) were held last refresh purely on Bill's dev-width choice, not a dependency block — both are dep-ready. With FEAT-006 about to close Sprint 1, these are the natural Sprint 2 openers once the BUG-007/BUG-008 wave has a slot free.

**Suggested slotting (dev cap 4, currently 0 in flight):** BUG-007, BUG-008, then BUG-006+BUG-009 paired in one slot (both small/process), 4th slot to FEAT-030 or MOD-013 (harness.replay) — Bill's call on which; RM has no strong preference between those two.

## 5. Team (live now)

Per `docs/planning/dev-team-process.md` **v1.7** (caps unchanged from v1.6: 4 dev / 2 tester / 2 BA / 1 docs / 1 QA / 1 RM; v1.7 adds the assumption-logging rule + mandatory spawn briefing block, not a cap change):
- **Tester-1 / Tester-2** — both cleared their original queues; Tester-1 just delivered the FEAT-006 hard-gate PASS + surfaced ASM-001/ASM-002. Next assignment follows Bill's dispatch from §4.
- **BA-1** — Task 1 (FEAT-007/FEAT-008 criteria refresh) is done (both items shipped and closed off it). Task 2 (freeze-risk audit of S1-S4 criteria) still queued — re-propose to Bill if it hasn't been picked up.
- **BA-2** — Sprint 5 criteria (engine.traffic, engine.roads) — status not re-checked this refresh, assume still in progress unless told otherwise.
- **Documentation** — feat.skeleton's doc pass is the one thing standing between FEAT-006 and commit — highest-priority live task.
- **QA** — trailing audit cadence continues; BUG-006/007/008/009 and ASM-001–006 all originate from QA/Tester findings today, a productive wave.
- **RM** — this file + team-board.md, refresh #3.
- **Devs** — 0/4 spawned; §4 above is the recommendation for the next wave.

## 6. Standing orders & rulings from Aaron (STILL IN FORCE)

- Dev-team pipeline **v1.7** mandatory (BA criteria → Sonnet junior → Tester pass/fail-never-fixes → Docs .md-only → Bill final review → commit). Saturation rule; heavy checkpointing; staging-area discipline; v1.6 Second-Tester independence section; **v1.7 assumption-logging + reciprocal rejection duties + mandatory spawn briefing block** (full text: `docs/planning/dev-team-process.md`).
- **v1.7 in one line, for anyone skimming:** log an `ASM-` item (with `--code-path`/`--codejson`) for anything you decided that the spec/criteria/brief didn't decide for you; devs reject asks resting on unlogged BA assumptions; Testers FAIL work carrying unlogged assumptions even on all-criteria-PASS; Bill's own rulings count too.
- **SECOND TESTER, v1.6** (still in force): 2 independent Testers, disjoint items, never communicate, one item never gets two verdicts.
- **CLOUD DECISION, 2026-08-09: Azure, confirmed, until otherwise agreed.** Existing garcia.ltd Azure estate reusable (storage account `garcialtdstorage`, RG `garcia`, region `uksouth`; ACR `prixsixacr`; Container Apps env `prixsix-env`, scale-to-zero) — full detail on BOW item **MOD-069**, cite via `node claude-bow.js show MOD-069` rather than duplicating. **Key ruling: Metropolis Blob saves get their OWN container — never reuse `whatsapp-session`.**
- **Interim CI control (BUG-006, in force immediately):** after ANY push, run `gh run list --limit 1` and eyeball the result before declaring anything done — do not trust a local "build OK, tests green" as if it were CI-green.
- "update" (bare word) = run /update skill. Tile decision: option (a) artistic compression (in data/georef.json). Go confirmed over C#. MOD-001 cancelled; metro BOW is the project BOW.
- OPEN question to Aaron: none outstanding.

## 7. Cold-resume procedure

1. `metro` launch → checkin prints BOW summary (metro DB health).
2. Read this file + `node claude-bow.js list --by-seq` + `git log -15 --oneline`.
3. `git status` — tree should be clean. HEAD should be at or ahead of `27c8c3d`.
4. Re-spawn Tester-1/Tester-2 per §5's "next assignment follows §4"; re-spawn RM/BAs/Docs/QA per §5. **Every spawn uses the v1.7 mandatory briefing block from `dev-team-process.md`** — do not paraphrase it from memory.
5. Do NOT redo committed work (git log + BOW `done` status = truth). Hooks are LIVE on both Bash and PowerShell — every commit needs a valid `[mkey]` tag when touching cmd/internal/data.
6. Before dispatching FEAT-031, confirm ASM-006's spike has actually happened — do not let it get skipped just because the item looks "ready" by dependency alone.
