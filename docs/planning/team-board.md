# Metropolis Team Board

**Maintained by:** Resource Manager (RM), advisory only — Bill executes all dispatches.
**Last updated:** 2026-08-09 (refresh #3 — Sprint 1 nearly closed: FEAT-007/FEAT-008 DONE, FEAT-006 Tester-1-PASSED awaiting doc pass; Bill amended `dev-team-process.md` to **v1.7** [assumption-logging + reciprocal rejection duties + mandatory spawn briefing block, committed `27c8c3d`]; six `ASM-` items logged retrospectively; ten follow-up items surfaced [FEAT-030–034, BUG-004–010] and triaged below; dev slots back to 0/4).
**Charter:** `docs/planning/dev-team-process.md` v1.7 §"Saturation rule & Resource Manager" / §"Second Tester (v1.6)" / §"Assumptions are logged or the work is rejected (v1.7)"

---

## Agent status

| Agent | Role | Current assignment | Status | Return point / next event |
|---|---|---|---|---|
| Bill | Lead | Dispatch, freeze-review liaison with Aaron, final review, Aaron's demo (will state ASM-001 plainly there) | — | — |
| RM (this agent) | Resource Manager | Board + checkpoint refresh #3 | busy | Delivering ranked dispatch recommendation for the post-FEAT-006 wave. |
| Tester-1 | Tester | Just delivered: **feat.skeleton (FEAT-006)** PASS on every hard-gated criterion (AC-1a,2,3,4,5a,7,9-local,10); surfaced ASM-001/ASM-002 under v1.7's hunt-for-assumptions duty | idle — cleared its queue | Awaiting Bill's next dispatch (§ dispatch recommendation below). |
| Tester-2 | Tester | Cleared its original queue (feat.detgate/BUG-002 PASS, BUG-003 re-verify PASS); also confirmed and sharpened **BUG-007** (protocol Close() race) | idle — cleared its queue | Awaiting Bill's next dispatch, most likely BUG-007's regression once dispatched to a dev. |
| BA-1 | BA | **Task 1 done** — FEAT-007/FEAT-008 criteria refreshed to `active`, both items shipped and closed off it. **Task 2 still queued**: freeze-risk audit of already-written S1-S4 criteria against the two pending freeze questions. | idle-pending-Task-2 | RM re-proposes Task 2 as BA-1's next assignment — no reason it should sit idle. |
| BA-2 | BA | Sprint 5 criteria: engine.traffic, engine.roads | busy (status not re-checked this refresh — assume in progress) | Criteria delivery. |
| Documentation | Documentation | **feat.skeleton's doc pass** — the one thing between FEAT-006's Tester-1 PASS and Bill's commit | busy | Doc pass complete → Bill commits FEAT-006 → Sprint 1 exit gate closes. |
| QA | QA | Trailing audit; today's wave (BUG-006/007/008/009, ASM-001–006) largely originates from QA/Tester findings | busy | Continues trailing cadence. |
| Devs | Jnr developer | **None spawned — 0/4.** FEAT-007/FEAT-008 wave landed and closed. | idle (cap) | Next wave per ranked recommendation below — Bill's call on exact slotting. |

---

## Cap compliance

| Role | Cap | Current count | Holders | Status |
|---|---|---|---|---|
| Jnr developer | 4 | 0 | — | Under cap, genuinely open — no dispatch blocker outstanding this time (contrast refresh #2, where 2 slots were held on a BA dependency). |
| Tester | 2 (v1.6) | 2 | Tester-1, Tester-2 — both idle-pending-dispatch, not busy | At cap by headcount, **0 busy right now** — both cleared their queues. This is a live saturation opening, not a breach. |
| BA | 2 | 2 | BA-1 (idle-pending-Task-2), BA-2 (busy) | At cap; BA-1 has a proposed next task (Task 2), not yet confirmed by Bill this refresh. |
| Documentation | 1 | 1 | Docs | At cap, on the critical path (feat.skeleton doc pass gates Sprint 1 close). |
| QA | 1 | 1 | QA | At cap. |
| Resource Manager | 1 | 1 | RM (this agent) | At cap. |

**No breaches.** Saturation notes: both Testers are idle-pending-dispatch — RM flags this because the next dev wave (BUG-007/BUG-008 recommended) will need Tester capacity almost immediately, so dispatching devs now rather than waiting keeps both Testers fed. BA-1's Task 2 is proposed, not yet confirmed for this refresh — flagging so it doesn't slip.

---

## v1.7 — Assumptions are logged or the work is rejected (Aaron ruling, 2026-08-09, committed `27c8c3d`)

**Principle:** the standard is that the *criterion holds*, not that the *test passes*. New BOW type `assumption` / `ASM-` codes, `--code-path` + `--codejson` mandatory and tool-enforced. Reciprocal rejection duties: dev rejects asks resting on unlogged BA assumptions; **Tester actively hunts for assumptions and FAILs on any unlogged one even if every criterion passed**; BA logs assumptions made while writing criteria; **lead is bound too** — an unwritten lead ruling is itself an unlogged assumption. Every future agent spawn carries the mandatory briefing block now living in `docs/planning/dev-team-process.md` — read it from there, don't reconstruct it from memory.

### Assumption log (own tracking row per Bill's instruction — these are live risk, not sprint items)

| Code | Pri | Claim | Disposition |
|---|---|---|---|
| ASM-001 | P1 | Sprint 1 exit gate does **not** prove `engine.core` participates in the live rendered path — render path is via `harness.stub`; `engine.core` determinism proven only in isolation by `feat.detgate`. Satisfies criteria as written. | Bill's own disclosure — stated plainly at Aaron's demo. No dev action, tracked for visibility only. |
| ASM-002 | P2 | F12's stub/ok registry rendering is proven structurally, not by literal end-to-end execution. | Awaiting Bill's ruling (accept/correct/escalate). |
| ASM-003 | P2 | AC-12: a failed persist leaves the header flagged in memory while debug stays off (over-flag, never under-flag). | Awaiting Bill's ruling. |
| ASM-004 | P2 | The F12 phase-name mirror is acceptable duplication because the drift test catches divergence. | Awaiting Bill's ruling. |
| ASM-005 | P2 | engine.core's pacing constant as a named Go var satisfies GR#15 in the interim. | Superseded once **FEAT-030** ships (pacing constant → config). |
| **ASM-006** | **P1** | Deferring the F1 overlay cycle assumes the renderer accepts a background-metric layer **additively** later. If wrong, FEAT-031 is a renderer rewrite, not a feature. | **Bill wants a deliberate spike BEFORE FEAT-031 is estimated** — do not let FEAT-031 get dispatched on dependency-readiness alone. See dispatch recommendation. |

---

## Sprint 0 / Sprint 1 gate status

- **Sprint 0**: cloud half of the gate RESOLVED (Azure confirmed, see incident/ruling log). Contract-freeze half still pending Aaron (`docs/design/README.md`).
- **Sprint 1**: 9/10 done. **feat.skeleton (FEAT-006) Tester-1-PASSED on every hard-gated criterion — doc pass is the only remaining step before Bill's commit closes the sprint.** AC-1b/AC-5b were split out, not weakened, into FEAT-032/FEAT-033 (blocked on Sprint 2 items MOD-014/MOD-011).

---

## RM's ranked dispatch recommendation for the post-FEAT-006 wave

Ten follow-up items surfaced today; checked each individually rather than assumed — BUG-004 and BUG-005 are already **DONE**.

| Rank | Item | Pri | Why |
|---|---|---|---|
| **1** | **BUG-007** — `internal/protocol` transport `Close()` TOCTOU race | P1 | Real shipped-code bug, not a test artifact: `Close()` races an in-flight send, failure mode is a **send-on-closed-channel panic**. Tester-2 confirmed **zero coverage in either direction** — the one test that looks like it covers this completes goroutines before calling `Close()`. `internal/protocol` carries every UI⇄engine command/event/delta — highest blast radius of any open defect. Recommend first, ahead of Sprint 2 features. |
| **2** | **BUG-008** — error registry incomplete, no mechanical check | P1 | **Answers Bill's direct question: yes, jump the queue.** Already caused a real collision (feat.debugmode junior legitimately claimed `E100-E199` as "free," colliding with detgate's already-shipped `E100-E103` which only existed in source; caught by luck at lead review, not by any mechanism). DoD's mechanical CI check *is* the guardrail Bill is asking about — every day it's open is a live collision risk for the next module that claims a range, and today's near-miss is direct evidence manual review isn't reliable here. |
| **3** | **BUG-006** — no post-push CI visibility / no branch protection | P1 | Root cause of BUG-004 surviving since commit 1. Interim control (`gh run list --limit 1` after every push) already mitigates the acute risk, so lower urgency than 1/2, but DoD is cheap (branch protection config + `/commit` skill edit) — bundle as a low-cost item, doesn't need its own dev slot. |
| 4 | **BUG-009** — `handleSetSpeed` ignores the debug gate | P2 | Small, dependency (FEAT-008) now satisfied. Ready-now filler for spare slot capacity. |
| 5 | **FEAT-030** — pacing constant to config | P2 | Directly resolves ASM-005. Ready, no blockers. |
| — | **ASM-006 spike** | P1 (gating) | **Hold on FEAT-031** (F1 overlay cycle) until someone deliberately checks whether the renderer accepts an additive background-metric layer. Not a straight dev dispatch — a short investigation reporting to Bill first. FEAT-031 itself must not be estimated before this lands. |
| — | **FEAT-032 / FEAT-033** | P2 | Blocked — depend on MOD-014 (ui.harness) / MOD-011 (ui.keys), neither built. Resume once Sprint 2 restarts. |
| — | **FEAT-034** | P3 | Trivial (buildinfo host field). No urgency, park as filler whenever a slot is otherwise idle. |

**Suggested slotting (dev cap 4, currently 0 in flight):** BUG-007 · BUG-008 · (BUG-006 + BUG-009 paired in one slot — both small) · 4th slot to FEAT-030 or reopening Sprint 2 via **harness.replay (MOD-013)** — Bill's call, RM has no strong preference between those two.

**Sprint 2 resumption note:** harness.replay (MOD-013) and ui.keys (MOD-011) were only held last refresh on Bill's dev-width choice, not a dependency block — both are still dep-ready (INT-001/INT-002 done; MOD-009 done) and are the natural Sprint 2 openers once the BUG-007/BUG-008 wave has a slot free. Their freeze-exposure ratings from refresh #2 (harness.replay HIGH, ui.keys MEDIUM) stand unchanged — BA-1's Task 2 freeze-risk audit, still queued, would sharpen these.

---

## Incident / constraint log

- **v1.7 assumption-logging rule (2026-08-09, `27c8c3d`):** see dedicated section above — tracked here as a process-incident-class entry because it changes every agent's reporting obligations going forward, not just this wave's work.
- **BUG-008 near-miss (2026-08-09):** direct evidence for why BUG-008 is ranked #2 above — a legitimate range-claim process collided with an unregistered code because the registry lagged source. Caught at lead review, not by tooling.
- **BUG-007 zero-coverage finding (2026-08-09, Tester-2):** `TestInProcTransport_Race` looked like it should cover the Close()-vs-send race and doesn't — it sequences goroutine completion before `Close()`. Worth remembering as a pattern to watch for elsewhere (a test named after a race that doesn't actually race the two operations in question).
- **VERSION-fixture staging incident (2026-08-09):** a junior's staged `VERSION` test fixture rode along into an unrelated docs commit via a concurrent agent's dirty staging area; caught and reverted within two commits (`a6885e5`). Staging-area discipline (v1.5.1) is the standing fix.
- **Session bounce (2026-08-09):** previous RM died mid-cycle with no surviving transcript; board and checkpoint were rebuilt cold from BOW + git, per the heavy-checkpointing recovery protocol. No work was redone.
