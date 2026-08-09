# Metropolis Team Board

**Maintained by:** Resource Manager (RM), advisory only — Bill executes all dispatches.
**Last updated:** 2026-08-09 (refresh #1 — secretguard shipped, BA-1 delivered, cloud.md delivered, dev slots now 4/4)
**Charter:** `docs/planning/dev-team-process.md` v1.5 §"Saturation rule & Resource Manager"

---

## Agent status

| Agent | Role | Current assignment | Status | Blocker | Return point / next event | At-risk |
|---|---|---|---|---|---|---|
| Bill | Lead | Dispatch, freeze-review liaison with Aaron, final review of test-clean work | — | — | — | — |
| RM (this agent) | Resource Manager | Board + checkpoint upkeep | busy | — | — | — |
| BA-1 | BA | **Delivered:** all 5 S0-remainder criteria files (`foundation.det`, `foundation.registry`, `foundation.data`, `tool.bow`, `legacy.versionguard`) — committed `4f5dd41`. Owns S0–S1. | idle-pending-next | — | No further S0 criteria outstanding. Next natural work: Sprint 1 criteria refresh (harness.stub/ui.core, currently `draft-ahead`) once int.protocol freezes, or new S1 items as they surface. **RM flags this as the next thing to hand BA-1** so it doesn't sit idle. | — |
| BA-2 | BA | Sprint 4 (+S5) ownership — `engine.market`, `engine.finance`, `engine.consumption`, `engine.unlocks`, `engine.services` | busy | — | **Pending report:** §-ref fixes (carried over from S2–S3 close-out) + Sprint 4 user-stories/criteria delivery. | — |
| Tester | Tester | Verifying `harness.stub` (MOD-008, J9's build — 21 tests reported green by dev) | busy | — | Verdict PASS/FAIL pending. secretguard cleared this queue (PASS, committed `0d09b04`) — harness.stub is next in line. | — |
| Docs | Documentation | Acceptance-corpus audit + freeze-packet upkeep; also owes a doc pass on `cloud.md` | busy | — | **Pending report:** Docs' `cloud.md` pass. | — |
| QA | QA | Trailing audit of wave-1 commits + the shipped hook pair (`claude-plan-guard.js`, `claude-secret-guard.js` — now committed at `0d09b04`, redaction-audit trailing per the item's own git-ref note) | busy | — | **Pending report:** QA trailing audit, findings to Bill only. | — |
| J8 | Jnr dev | **Correction — NOT idle.** Rehired on a QA bounce: **BUG-001** (secret-guard `redact()` under-redaction fix — QA found 9–20 char secrets show up to 89% cleartext, min-mask-floor fix + 3 hardening notes) | busy (bounce, off-cap) | — | Bounces return to the originating junior and **do not consume a dev-cap slot** (process v1.5). J8 returns to the Tester for BUG-001 re-verification once fixed; `foundation.data` (MOD-006, criteria active) and `tool.bow` (MOD-007, criteria active) still queue for the **first slot freed among J10–J13** — J8 no longer the presumed recipient by default, just next-available-junior once a real cap slot opens (J8 himself, if free by then, or a rehire of J9). | — |
| J9 | Jnr dev | **Released** — `harness.stub` dev-complete (21 tests green), now with the Tester | **idle — awaiting verdict, not yet rehired** | Tester verdict on harness.stub | On PASS: item closes, J9 available for rehire — first in line for `foundation.data` (MOD-006) or `tool.bow` (MOD-007) if either is still unclaimed, since J8 is now off-cap on the BUG-001 bounce rather than available. On FAIL: report returns to J9 directly. | — |
| J10 | Jnr dev | `ui.core` (MOD-009), Sprint 1 | busy — **still building** | — | Criteria `ui.core.md` (`draft-ahead`); tcell dependency already sanctioned. | **YES — high (pre-freeze)** |
| J11 | Jnr dev | `foundation.det` (MOD-004), Sprint 0, P0 | busy — building | — | Criteria active (`foundation.det.md`). Highest-priority open S0 item; blocks `MOD-012`. | — |
| J12 | Jnr dev | `foundation.registry` (MOD-005), Sprint 0, P0 | busy — building | — | Criteria active (`foundation.registry.md`). Blocks `FEAT-007`, `FEAT-008`, `MOD-012`. | — |
| J13 | Jnr dev | `legacy.versionguard` (FEAT-002), Sprint 0, P1 — **urgent** | busy — building | — | Criteria active (`legacy.versionguard.md`); scope: retire the two-file version check only, `[mkey]` enforcement stays with `tool.bow` (MOD-007). **Sequencing gate: the PowerShell hook-matcher fix ships only after this lands** (else all engine commits brick — hooks currently fire on Bash-tool commits only). | — |
| cloud.md writer | one-off | — | **SUNSET** | — | Delivered + committed (part of `4f5dd41`); one-off confirmed complete, removed from active roster. Docs still owes a pass over the delivered `cloud.md`. | — |

---

## At-risk parallel starts (Sprint-0-gate exposure)

Sprint 0 exit gate = **Aaron's freeze review of `int.protocol` / `int.serializer` / `int.solver` / `foundation.errors`** (the four contract docs, all "tested PASS — awaiting freeze review"). **Still PENDING.**

| Item | Agent | Depends on (frozen?) | Exposure |
|---|---|---|---|
| `harness.stub` (MOD-008) | J9 (dev-complete, now with Tester) | `int.protocol` (INT-001) — tested PASS, not yet Aaron-frozen | Dev-complete and in verification, so the exposure window is narrowing but not closed — a freeze-review schema change could still bounce it post-Tester, even post-commit if severe. |
| `ui.core` (MOD-009) | J10 | `int.protocol` (INT-001) — same | Still building; same protocol-shape exposure as before. |

Two cross-cutting freeze questions could independently ripple into both: (a) OD-matrix `f32`/`f64` precision on `int.solver`, (b) duplicate correlation-ID generators (`internal/protocol` vs `internal/foundation/errs`).

Sanctioned at-risk starts (standing order from Aaron via Bill, confirmed this refresh) — not an RM objection, flagged for fan-out visibility only.

---

## Dispatch queue (priority order)

Dev cap is now **4/4** (J10, J11, J12, J13). J8 is off-cap on a bounce (BUG-001, does not count against the 4). No new cap-slot dispatch fits until one of J10–J13 frees. Next up the instant one does:

1. **`foundation.data` (MOD-006, P1, seq 90) → first freed slot** (J8 if his BUG-001 bounce clears and re-passes first, otherwise J9 once released by the harness.stub verdict, otherwise whichever of J10–J13 finishes first). Criteria active. Large fan-out (blocks 7 later items).
2. **`tool.bow` (MOD-007, P1, seq 95) → next freed slot after (1)**. Criteria active. Commit-msg `[mkey]` validation + auto-ref-on-commit; deliberately sequenced to land after `legacy.versionguard` (J13) so the two don't collide on the same commit-hook surface.
3. **BUG-002 (golangci v2 config defect)** — QA finding on `foundation.repo` (MOD-003, commit `5faf2ed`): `.golangci.yml` is v1-syntax, CI lint job is commented out, so the GR#20 `ui→engine` import-ban depguard rule has zero live enforcement today. Not dispatched yet — **scheduled into the Sprint-1 lint-arming pass** (`feat.detgate` sprint work: migrate config to v2 via `golangci-lint migrate`, uncomment the CI job pinned to a specific version, prove the rule fires on a deliberate scratch violation). Add to the dispatch queue once `feat.detgate` is in play; P1, no dev assigned yet.

**Sequencing note carried into checkpoint:** the PowerShell hook-matcher fix (making all commit guards fire on the lead's own commits, not just Bash-tool commits) is gated on `legacy.versionguard` (J13) shipping first — do not schedule that fix earlier even if a slot is free.

---

## Caps table

| Role | Cap | Current count | Holders | Status |
|---|---|---|---|---|
| Jnr developer | 4 | **4** | J10, J11, J12, J13 (building) — J8 busy off-cap on BUG-001 bounce (does not count), J9 idle pending harness.stub verdict | **AT CAP** — the 4 slots are J10–J13; J8's bounce and J9's idle wait are both outside/behind the cap, not filling it |
| Tester | 1 | 1 | Tester (on harness.stub) | at cap |
| BA | 2 | 2 | BA-1 (S0–S1, deliverable landed, next assignment pending), BA-2 (S4–S5) | at cap, disjoint sprint ownership confirmed |
| Documentation | 1 | 1 | Docs | at cap |
| QA | 1 | 1 | QA | at cap |
| Resource Manager | 1 | 1 | RM (this agent) | at cap |
| — outside caps — | | | (none active — cloud.md writer sunset) | — |

**No cap breaches.** J8 is correctly off-cap on the BUG-001 bounce (bounces don't consume a dev slot per v1.5) — he is busy, not idle, and this refresh corrects the previous snapshot's error on that point. The real gap: `foundation.data` and `tool.bow` both have active criteria and no dev, and cannot be dispatched until one of J10–J13 frees **or** J9's harness.stub verdict lands and releases him for rehire. Recommend the first of those two events triggers immediate dispatch of `foundation.data`.

## Incident / constraint log

- **VERSION-fixture staging incident (2026-08-09):** a junior's staged `VERSION` test fixture rode along into an unrelated docs commit via a concurrent agent's dirty staging area; caught and reverted within two commits (`a6885e5` reverts the stray fixture). Root cause: the git index is shared mutable state across concurrent agents.
- **New rule — staging-area discipline (v1.5.1, `docs/planning/dev-team-process.md`, commit `9b6e1b7`):** (1) juniors must never leave anything staged between tool calls — stage→verify→reset sequences complete atomically inside one command invocation; (2) the lead commits with explicit pathspecs (`git commit -m "..." -- <paths>`) or verifies `git diff --cached --stat` matches the intended set immediately before committing. RM should treat any future commit that touches unexpected paths as a v1.5.1 violation to flag immediately.
- **BUG-002 (golangci v2 config defect)** — tracked above in the dispatch queue; recorded here too since it's a QA finding on already-shipped foundation.repo, not a fresh dispatch.
