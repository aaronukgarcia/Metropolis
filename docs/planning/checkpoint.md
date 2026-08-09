# Metropolis Recovery Checkpoint

**Maintained by:** Resource Manager (RM); committed by Bill alongside commit-bearing events.
**Last updated:** 2026-08-09 (refresh #1 — secretguard shipped, BA-1 delivered, cloud.md delivered, dev slots now 4/4: J10–J13)
**Purpose:** a COLD session with no memory of any agent must be able to resume mid-cycle from this file + `node claude-bow.js list --by-seq` + `git log` alone (`docs/planning/dev-team-process.md` v1.5 §"Heavy checkpointing").

---

## 1. Sprint state

### Sprint 0 (Contracts & bedrock) — exit gate: Aaron freezes int.protocol / int.serializer / int.solver / foundation.errors

| Item | mkey | Status | Commit(s) | Notes |
|---|---|---|---|---|
| Go monorepo skeleton | foundation.repo (MOD-003) | done | `5faf2ed` | Unblocks all Sprint 0/1 items with MOD-003 as a dep |
| Error registry + correlation IDs | foundation.errors (MOD-002) | done | `926f797` | doc `errors.md` tested PASS, awaiting freeze review |
| Protocol v1 | int.protocol (INT-001) | done | `00ee1d2` | doc `protocol.md` tested PASS, awaiting freeze review; 5 open questions |
| Serializer | int.serializer (INT-002) | done | `0ef4221` | doc `save-format.md` tested PASS, awaiting freeze review; A3 binary-size threshold still unset |
| Solver contract | int.solver (INT-003) | done | `bce6879` | doc `solver-contract.md` tested PASS, awaiting freeze review; f32/f64 OD precision open (cross-cutting Q(a)) |
| Plan-drift guard hook | tool.planguard | done | `5204799` | shipped |
| Georeference decision | feat.georef | done | `1bf6300`, `21a684a` | Aaron's tile-compression decision recorded |
| Secret/hardcoding guard hook | tool.secretguard (FEAT-028) | **DONE** | `0d09b04` | Build (J8) → Tester PASS all 23 ACs → committed. Standing order executed (commit without check-back). QA redaction audit still trailing (see §2). Fires on Bash-tool commits only — PowerShell matcher wiring sequenced to land after `legacy.versionguard` ships (see §4/§ sequencing note). |
| S0-remainder acceptance criteria (all 5 items) | — | **DELIVERED** | `4f5dd41` | BA-1 delivered `foundation.det.md`, `foundation.registry.md`, `foundation.data.md`, `tool.bow.md`, `legacy.versionguard.md` in one pass — all now `active` status, not draft-ahead. Same commit also carries the `cloud.md` provider adjudication and at-risk tracking updates to the freeze packet. |
| Determinism core | foundation.det (MOD-004) | **in_progress** | — (building) | Criteria active. Dispatched to **J11**. P0, blocks `MOD-012`. |
| Module registry | foundation.registry (MOD-005) | **in_progress** | — (building) | Criteria active. Dispatched to **J12**. P0, blocks `FEAT-007`/`FEAT-008`/`MOD-012`. |
| Config data files + loader | foundation.data (MOD-006) | open | — | Criteria active, **not yet dispatched** — queued for J8 rehire the moment a dev slot frees (currently 4/4). |
| BOW git integration | tool.bow (MOD-007) | open | — | Criteria active, **not yet dispatched** — queues behind `foundation.data` for the next freed slot. Deliberately sequenced after `legacy.versionguard` (J13) so `[mkey]`-enforcement work doesn't collide with the version-guard retarget on the same commit-hook surface. |
| Version guard retarget | legacy.versionguard (FEAT-002) | open (dispatch recorded, BOW status not yet flipped to in_progress) | — (building) | Criteria active. Dispatched to **J13, URGENT**. Scope confirmed narrower than the original 2026-08-08 decision comment: retire the two-file version check only; `[mkey]` enforcement work stays with `tool.bow` (MOD-007), not duplicated here. **This item gates the PowerShell hook-matcher fix — see §4.** |

**Sprint 0 exit gate status: still PENDING.** All four freeze-review docs remain tested PASS and doc-passed, awaiting Aaron. Cloud provider decision (`docs/cloud.md`) is now also pending on Aaron — see §3.

### Sprint 1 (Walking skeleton) — at-risk starts, pre-freeze

| Item | mkey | Status | Dev | Criteria file | Risk |
|---|---|---|---|---|---|
| H-STUB StubEngine | harness.stub (MOD-008) | **dev-complete, with Tester** | J9 (released from build, not yet rehired) | `harness.stub.md` (draft-ahead; refresh-at-dispatch flag against int.protocol — dev proceeded without a schema change in the interim) | High — full protocol implementation, exposed to freeze-review schema changes even post-commit |
| TUI renderer core | ui.core (MOD-009) | open, **build in progress** | J10 | `ui.core.md` (draft-ahead; tcell dep already sanctioned) | High — same protocol exposure via T-VIEWS delta client |

No other Sprint 1 items have started.

---

## 2. In-flight work — expected next events

| Agent | Doing | Expected next event |
|---|---|---|
| Tester | Verifying `harness.stub` (21 tests reported green by J9) | PASS or FAIL verdict — next in the Tester's queue after secretguard cleared. |
| J9 | **Released** (harness.stub dev-complete) | Awaiting Tester verdict; not yet rehired. No criteria-ready S1/S2 item is free once J8 claims `foundation.data`/`tool.bow` — RM to re-scan `docs/planning/acceptance/` for a fresh target when the verdict lands. |
| J10 | Building `ui.core` | Tester verification against `ui.core.md` once build completes. |
| J11 | Building `foundation.det` | Tester verification against `foundation.det.md` once build completes. |
| J12 | Building `foundation.registry` | Tester verification against `foundation.registry.md` once build completes. |
| J13 | Building `legacy.versionguard` (urgent) | Tester verification once complete; **then** the PowerShell hook-matcher fix can be scheduled (sequencing gate, §4). |
| J8 | **Released** (secretguard shipped) | Idle, rehire-queued for `foundation.data` (MOD-006) the instant a dev slot frees (4/4 currently held by J10–J13). |
| BA-1 | Delivered all 5 S0-remainder criteria (`4f5dd41`) | Idle-pending-next — no further S0 criteria outstanding. Candidate next work: refresh harness.stub/ui.core criteria from draft-ahead to active once int.protocol freezes, or new S1 items as they surface. |
| BA-2 | Sprint 4 (+S5) ownership | **Pending report:** §-ref fixes carried over from S2–S3 close-out, plus Sprint 4 user-stories/criteria delivery. |
| Docs | Acceptance-corpus audit + freeze-packet upkeep | **Pending report:** doc pass over the delivered `cloud.md`. |
| QA | Trailing audit of wave-1 commits + shipped hooks | **Pending report:** QA trailing audit (now covers both `claude-plan-guard.js` and the committed `claude-secret-guard.js`, including the redaction-audit flagged in FEAT-028's own git-ref note). Reports to Bill only. |
| Bill | Awaiting Aaron | Two items now pending on Aaron simultaneously — see §3. |

---

## 3. Pending verdicts / pending reports

**Pending verdicts:**
- **`harness.stub` Tester verdict** — in progress. Releases J9 fully and, on PASS, clears the item for commit.

**Pending on Aaron:**
- **Sprint 0 contract freeze review** — `int.protocol`/`int.serializer`/`int.solver`/`foundation.errors`, packet in `docs/design/README.md`. No ETA. Resolves J10's and (residually) J9's rebase exposure and the two cross-cutting freeze questions (OD f32/f64 precision, duplicate correlation-ID generators).
- **Cloud provider decision** — `docs/cloud.md`, delivered and committed (`4f5dd41`), awaiting Aaron's read/decision. Docs still owes a style pass over it independent of Aaron's decision.

**Pending reports (agents' output not yet landed):**
- QA trailing audit (wave-1 commits + hook pair).
- BA-2 §-ref fixes + Sprint 4 criteria delivery.
- Docs' `cloud.md` pass.
- J10 (`ui.core`), J11 (`foundation.det`), J12 (`foundation.registry`), J13 (`legacy.versionguard`) — all four builds in progress, none yet reached the Tester.

---

## 4. Standing orders (from Aaron, via Bill)

1. `tool.secretguard` commits without check-back once the Tester passes it — **executed** (`0d09b04`).
2. Sprint 1 (and beyond) items may start against dependencies that are code-complete even if the sprint's freeze gate hasn't opened yet — at-risk, RM tracks rebase exposure (see `team-board.md` §"At-risk parallel starts"). Confirmed still in force this refresh.
3. Team caps are hard: 4 Jnr devs / 1 Tester / 2 BAs (disjoint sprints) / 1 Docs / 1 QA / 1 RM. Growth beyond needs Aaron, not Bill.

**Sequencing constraint (new, record carefully):** the PowerShell hook-matcher fix — making all commit guards (plan-guard, secret-guard, etc.) actually fire on the lead's own commits, not just Bash-tool-issued ones — **lands only after `legacy.versionguard` (J13) ships**. Do not schedule or dispatch that fix earlier, even if a dev slot is free; ordering it before the version-guard retarget risks bricking commits to `cmd/`/`internal/`/`data/`.

---

## 5. If resuming cold — exact next steps

1. Read this file in full, then `docs/planning/team-board.md`.
2. Run `node claude-bow.js list --by-seq` and `node claude-bow.js ready` — confirm nothing in §1's tables has moved (items may have gone `done` with commit refs since this snapshot; trust the BOW over this file for status, trust this file for *why* and *what's next*).
3. Run `git log -15 --oneline` — check for commits past `0d09b04` (secretguard) and `4f5dd41` (BA-1 criteria + cloud.md): look for `foundation.det`/`foundation.registry`/`foundation.data`/`tool.bow`/`legacy.versionguard`/`harness.stub`/`ui.core` commit messages.
4. Check BOW comments on `harness.stub` (MOD-008) for a Tester verdict — if PASS and uncommitted, that's a high-priority action (release J9, prep commit). If FAIL, the report should already be routed to J9.
5. Check whether `legacy.versionguard` (J13) has shipped — if yes, the PowerShell hook-matcher fix becomes dispatchable (previously gated, §4); if no, do **not** schedule it regardless of free slots.
6. Reconstruct dev-slot occupancy: count only devs with an *open/in_progress* BOW item actually assigned as occupying one of the 4 caps. A dev released (item done, or dev-complete and with the Tester) is NOT occupying a slot in the sense that blocks rehire — but note J9's real-world slot is still "spoken for" until the Tester verdict lands and Bill/RM explicitly rehires him elsewhere; don't double-book.
7. `foundation.data` (MOD-006) and `tool.bow` (MOD-007) both have **active** criteria and no dev — these are the next dispatches the instant a slot frees (J8 first, in that order; `tool.bow` sequenced behind `legacy.versionguard`'s ship for hook-surface reasons, not a hard BOW dependency).
8. Re-verify cap counts before recommending any new dispatch (4 Jnr devs / 1 Tester / 2 BAs / 1 Docs / 1 QA / 1 RM) — the cloud.md writer slot is sunset and permanently off the roster, not available for reuse without Bill/Aaron standing up a new one-off.
