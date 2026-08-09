# Metropolis Team Board

**Maintained by:** Resource Manager (RM), advisory only — Bill executes all dispatches.
**Last updated:** 2026-08-09 (refresh #2 — Bill amended dev-team-process.md to v1.6 [Tester cap 2, Second Tester independence section]; BA-1 given a dispatch-blocking Task 1 [refresh draft-ahead FEAT-007/FEAT-008 criteria before build]; FEAT-007/FEAT-008 dev dispatch held pending that refresh; Bill deliberately holding dev slots at 2-wide, not filling to cap).
**Charter:** `docs/planning/dev-team-process.md` v1.6 §"Saturation rule & Resource Manager" / §"Second Tester (v1.6)"

---

## Agent status

| Agent | Role | Current assignment | Status | Blocker | Return point / next event |
|---|---|---|---|---|---|
| Bill | Lead | Dispatch, freeze-review liaison with Aaron, final review of test-clean work | — | — | — |
| RM (this agent) | Resource Manager | Rebuilding board + checkpoint post-bounce | busy | — | Delivering this rebuild + ranked dispatch recommendation to Bill now. |
| Tester-1 | Tester | Verification queue, part 1: **engine.core** (MOD-012, commit `f81e5d7`) → then **ui.screen.map** (FEAT-005, commit `b721740`) | busy | — | PASS/FAIL evidence per item. On PASS: Bill `done`s the item with a provenance note. On FAIL: bounces to J16 (engine.core) or J19 (ui.screen.map) respectively. |
| Tester-2 | Tester | Verification queue, part 2: **feat.detgate** (FEAT-004, commit `47de5d0`) + **BUG-002** evidence → then **BUG-003** re-verify (commit `f7815b7`) | busy | — | Same PASS/FAIL protocol; FAIL bounces to J18 (feat.detgate/BUG-002) or J15 (BUG-003). |
| BA-1 | BA | Owns S0-S1. **Task 1 (dispatch-blocking, assigned by Bill):** refresh `ui.screen.debug.md` (FEAT-007) and `feat.debugmode.md` (FEAT-008) — both currently `status: draft-ahead` with escalation notes to verify specific API references against landed code (`errs.Recent()`, the registry API, the widgets sparkline, engine.core's phase names/order, serialize's `Header` methods). Reports to Bill before starting anything else. **Task 2 (queued):** freeze-risk audit of already-written S1-S4 criteria against the two pending freeze questions. | busy | — | Task 1 report to Bill unblocks the FEAT-007/FEAT-008 dev dispatch (see below). Task 2 follows. |
| BA-2 | BA | Owns S2-S5 in practice (S2-S4 criteria already delivered: data.catalogue, harness.replay, ui.harness, harness.synth, ui.keys, engine.invariant, engine.world, engine.citizens, engine.season, engine.market, engine.finance, engine.consumption, engine.unlocks, engine.services — all present in `docs/planning/acceptance/`). Now finishing Sprint 5: **engine.traffic, engine.roads**. | busy | — | Criteria delivery for engine.traffic/engine.roads. |
| Documentation | Documentation | Freeze-packet upkeep (`docs/design/README.md`) + acceptance-corpus conventions | busy | — | Next doc pass alongside the verification-queue PASSes. |
| QA | QA | Trailing audit of the three unverified-but-committed deliveries: `f81e5d7` (engine.core), `47de5d0` (feat.detgate), `b721740` (ui.screen.map) | busy | — | Findings report to Bill only (QA never talks to RM/Tester/BA/Docs/devs). |
| Devs | Jnr developer | **None currently spawned.** | idle (cap) | — | Bill is deliberately holding dev width at **2** (FEAT-007 + FEAT-008), not filling the 4-cap — both Testers are already loaded, and a 4-wide dev dispatch would just queue behind them without unblocking anything (bottleneck moves, doesn't disappear). Those 2 slots are further gated on BA-1's Task 1 criteria refresh landing first. |

---

## Cap compliance

| Role | Cap | Current count | Holders | Status |
|---|---|---|---|---|
| Jnr developer | 4 | 0 (2 queued, pending BA-1 Task 1) | — | Under cap by design — Bill is holding width at 2 deliberately, not a saturation gap (see Devs row above). |
| **Tester** | **2** (v1.6, `docs/planning/dev-team-process.md` amended by Bill 2026-08-09 — Tester cap raised to 2 + new "Second Tester (v1.6)" independence section: disjoint items, never communicate, one item never gets two verdicts) | 2 | Tester-1, Tester-2 (disjoint queues, independent, both report only to Bill) | At cap, doc and reality now match — closed, no longer tracked as a divergence. |
| BA | 2 | 2 | BA-1 (busy, Task 1 dispatch-blocking), BA-2 (busy, S5) | At cap, disjoint sprint ownership holds |
| Documentation | 1 | 1 | Docs | At cap |
| QA | 1 | 1 | QA | At cap |
| Resource Manager | 1 | 1 | RM (this agent) | At cap |

**No breaches.** BA-1 is no longer idle (Task 1 assigned by Bill, dispatch-blocking). Dev slots sit at 0/4 spawned by Bill's deliberate choice, not an unflagged saturation gap — RM's view, on record: reasonable while both Testers are loaded; RM will re-raise if the Tester queue drains and dev width still isn't following.

---

## Sprint 0 gate — narrowed by ruling (2026-08-09)

Cloud provider decision is **RESOLVED**: Azure confirmed as ruling (was: `docs/cloud.md` recommendation, low confidence). Existing garcia.ltd Azure estate (storage account `garcialtdstorage`, RG `garcia`, region `uksouth`) is reusable for Metropolis, in a **new, separate blob container** — never `whatsapp-session`. Full detail on BOW item **MOD-069** (do not duplicate here — `node claude-bow.js show MOD-069`).

Sprint 0 now closes on the **contract freeze review alone**: `docs/design/README.md` (4 contract docs: int.protocol, int.serializer, int.solver, foundation.errors) + 2 cross-cutting questions (OD f32-vs-f64, duplicate correlation-ID generators). Still pending Aaron.

---

## At-risk parallel starts (contract-freeze exposure)

Everything built before the contract freeze lands carries some exposure; severity depends on how directly the item touches `int.protocol`/`int.serializer` shape.

| Item | Depends on (unfrozen?) | Exposure |
|---|---|---|
| engine.core (MOD-012), feat.detgate (FEAT-004), ui.screen.map (FEAT-005) | INT-001/INT-002, `done` status but not yet Aaron-frozen | Already committed and in the Tester queue — narrowing but not closed; a freeze change could still force a follow-up patch even post-PASS. |
| harness.replay (MOD-013) — ready to dispatch, S2 | INT-001, INT-002 directly (fixture format IS the save format) | **High** — this item's whole job is serialising the protocol/serializer envelope. A freeze change to either would touch it directly. |
| ui.keys (MOD-011) — dep-ready, dispatch held (Bill: 2-wide, Tester-bound) | MOD-009 (ui.core, itself built against unfrozen INT-001) | Medium — indirect, inherits ui.core's exposure. |
| ui.screen.debug (FEAT-007), feat.debugmode (FEAT-008) — dep-ready, dispatch held pending BA-1 Task 1 | MOD-002/005/010, none of which are protocol-shape-sensitive | **Low** — these are registry/error-tail/widget assembly, not protocol consumers. Their real current blocker isn't freeze exposure, it's stale API references in the draft-ahead criteria (BA-1 Task 1). |

Sanctioned at-risk starts remain standing policy per Aaron (confirmed prior refresh) — flagged here for fan-out visibility, not as an RM objection. BA-1's Task 2 (freeze-risk audit) will sharpen these exposure calls once delivered — treat the table above as RM's best estimate, not BA-confirmed.

---

## Dispatch queue — status per Bill's decision (2026-08-09 refresh #2)

RM's original ranking below is **accepted by Bill as the ranking**, but dispatch of (1)/(2) is now explicitly gated and (3)/(4) explicitly held — not a disagreement, a sequencing call:

1. **ui.screen.debug (FEAT-007), P0, S1** — all deps DONE, but criteria are `draft-ahead` with known-stale API refs. **Status: dispatch pending BA-1 Task 1 refresh.** Will be the first dev dispatched once BA-1 reports.
2. **feat.debugmode (FEAT-008), P1, S1** — same criteria staleness issue. **Status: dispatch pending BA-1 Task 1 refresh**, dispatched alongside (1) — this is the 2-wide dev dispatch Bill is holding for, not a 4-wide one.
3. **harness.replay (MOD-013), P1, S2** — dep-ready, highest freeze exposure. **Status: held**, stays queued at-risk-into-S2; not part of the current 2-wide plan.
4. **ui.keys (MOD-011), P0, S2** — dep-ready, medium freeze exposure. **Status: held**, same as (3).

Bill's stated reasoning for not filling all 4 dev slots now: both Testers are already loaded, so a 4-wide dev dispatch would build a queue behind them rather than remove the bottleneck. RM's saturation-rule view: reasonable while the verification queue is the pacing constraint; RM will re-flag if the Tester queue clears and dev width still sits below 4 with dep-ready, criteria-active work (3)/(4) waiting.

**Explicitly NOT ready** — do not dispatch yet:
- **feat.skeleton (FEAT-006)** — depends on MOD-012/FEAT-004/FEAT-005, all still `in_progress` pending Tester. This was checkpoint §4's #1 pick; RM's dependency check says it's actually **gated on the verification queue draining first**, not independently dispatchable. Re-rank to top of queue the moment all three PASS.
- **harness.synth (MOD-016)**, **engine.invariant (MOD-019)** — both depend directly on MOD-012 (`in_progress`).
- **ui.harness (MOD-014)** — depends on MOD-013 (harness.replay), which isn't built yet even if dispatched today.

**Cloud-ruling reassessment (per Bill's request):** checked MOD-069 (Azure tiers, P3/future) and FEAT-011 (Save/load UX, P1/M3) against the new platform ruling. **Neither moves up.** MOD-069 still gates on MOD-036 (balance harness, open, several sprints out); FEAT-011 still gates on MOD-011 (ui.keys — open, but even once ui.keys lands FEAT-011 is an M3 item, well past the current build horizon). The Azure ruling closes half the Sprint 0 gate and de-risks planning, but does not pull any cloud-adjacent build work into Sprint 1.

**BA-1 — resolved by Bill (refresh #2):** RM had flagged BA-1 idle and offered options (a) freeze-risk audit / (b) S6 criteria. Bill found a third, dispatch-blocking need RM's pass didn't catch: FEAT-007/FEAT-008 criteria are `draft-ahead` with escalation notes calling out specific stale API references (`errs.Recent()`, registry API, widgets sparkline, engine.core phase names/order, serialize `Header` methods) that must be refreshed against landed code before a junior builds to them. That's now BA-1's Task 1, reporting to Bill before anything else. RM's option (a) survives as **Task 2**, sequenced after — Bill agreed with the reasoning (de-risking written criteria beats drafting further ahead) but had to clear the dispatch blocker first.

---

## Incident / constraint log (carried forward)

- **VERSION-fixture staging incident (2026-08-09):** a junior's staged `VERSION` test fixture rode along into an unrelated docs commit via a concurrent agent's dirty staging area; caught and reverted within two commits (`a6885e5`). Root cause: the git index is shared mutable state across concurrent agents. Staging-area discipline (v1.5.1) is the standing fix — see `docs/planning/dev-team-process.md`.
- **BUG-002 (golangci v2 config defect)** — fix bundled into `feat.detgate` (commit `47de5d0`), BOW item stays OPEN pending Tester-2's verdict.
- **BUG-003 (BOW hooks duplicate lookup SQL)** — fix committed `f7815b7`, BOW item stays OPEN pending Tester-2 re-verdict.
- **Session bounce (2026-08-09):** previous RM died mid-cycle with no surviving transcript; this board and `checkpoint.md` were rebuilt cold from BOW + git + the prior checkpoint text, per the v1.5 heavy-checkpointing recovery protocol. No work was redone — commits + BOW `done` status were treated as ground truth throughout.
