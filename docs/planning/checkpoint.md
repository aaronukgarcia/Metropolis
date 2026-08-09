# Metropolis Recovery Checkpoint

**Maintained by:** Resource Manager (RM); committed by Bill alongside commit-bearing events.
**Last updated:** 2026-08-09 (refresh #2 — corrects J8's status; adds staging-discipline incident + BUG-001/BUG-002; committed as of `b920ed5`)
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
| Secret/hardcoding guard hook | tool.secretguard (FEAT-028) | **DONE (shipped), with a trailing bug** | `0d09b04` | Build (J8) → Tester PASS all 23 ACs → committed. Standing order executed (commit without check-back). QA's trailing redaction audit found **BUG-001** (below) — `redact()` under-redacts 9–20 char secrets. Fires on Bash-tool commits only — PowerShell matcher wiring sequenced to land after `legacy.versionguard` ships (see §4/§ sequencing note). |
| BUG-001 (secret-guard under-redaction) | — | **open, dispatched** | — (fix in progress) | QA finding on `0d09b04`: `redact()` shows `min(len,8)` cleartext chars regardless of length — up to 89% disclosure for 9–20 char secrets. Fix: scale reveal to ≤25% of length + min-mask floor; also comment the top-level catch-all channel, add mandatory `reason` field to `allowedPaths` entries, add rationale comments on `ENTROPY_THRESHOLD`/`ENTROPY_MIN_LENGTH`. **Dispatched to J8 as a bounce — this does NOT consume a dev-cap slot** (process v1.5: bounces return to the originating junior, off-cap). Returns to Tester for re-verification once fixed. |
| BUG-002 (golangci v2 config defect) | — | **open, scheduled not dispatched** | — | QA finding on `foundation.repo` (MOD-003, commit `5faf2ed`): `.golangci.yml` is v1-syntax; golangci-lint v2 drops/errors on it, and the CI lint job is commented out — the GR#20 `ui→engine` import-ban has **zero live enforcement today**. Fix scheduled into the **Sprint-1 lint-arming pass** (`feat.detgate` sprint): migrate config via `golangci-lint migrate`, uncomment the CI job pinned to a specific version, prove the rule fires via a deliberate scratch violation. No dev assigned yet — not a Sprint 0 item. |
| S0-remainder acceptance criteria (all 5 items) | — | **DELIVERED** | `4f5dd41` | BA-1 delivered `foundation.det.md`, `foundation.registry.md`, `foundation.data.md`, `tool.bow.md`, `legacy.versionguard.md` in one pass — all now `active` status, not draft-ahead. Same commit also carries the `cloud.md` provider adjudication and at-risk tracking updates to the freeze packet. |
| Determinism core | foundation.det (MOD-004) | **in_progress** | — (building) | Criteria active. Dispatched to **J11**. P0, blocks `MOD-012`. |
| Module registry | foundation.registry (MOD-005) | **in_progress** | — (building) | Criteria active. Dispatched to **J12**. P0, blocks `FEAT-007`/`FEAT-008`/`MOD-012`. |
| Config data files + loader | foundation.data (MOD-006) | open | — | Criteria active, **not yet dispatched** — queued for the **first slot freed among J10–J13** (J8 is unavailable, busy off-cap on the BUG-001 bounce; J9 is next-available once his harness.stub verdict lands). |
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
| J8 | **Correction — NOT idle.** Fixing **BUG-001** (secret-guard redaction bounce, off-cap) | Returns to Tester for BUG-001 re-verification. Does not occupy a dev-cap slot while on this bounce (process v1.5: bounces return to originating junior, off-cap). |
| J9 | **Released** (harness.stub dev-complete) | Awaiting Tester verdict; not yet rehired. First in line for `foundation.data` (MOD-006) or `tool.bow` (MOD-007) once released — J8 is unavailable (on the BUG-001 bounce), so J9 is the more likely near-term recipient. |
| J10 | Building `ui.core` | Tester verification against `ui.core.md` once build completes. |
| J11 | Building `foundation.det` | Tester verification against `foundation.det.md` once build completes. |
| J12 | Building `foundation.registry` | Tester verification against `foundation.registry.md` once build completes. |
| J13 | Building `legacy.versionguard` (urgent) | Tester verification once complete; **then** the PowerShell hook-matcher fix can be scheduled (sequencing gate, §4). |
| BA-1 | Delivered all 5 S0-remainder criteria (`4f5dd41`) | Idle-pending-next — no further S0 criteria outstanding. Candidate next work: refresh harness.stub/ui.core criteria from draft-ahead to active once int.protocol freezes, or new S1 items as they surface. |
| BA-2 | Sprint 4 (+S5) ownership | **Pending report:** §-ref fixes carried over from S2–S3 close-out, plus Sprint 4 user-stories/criteria delivery. |
| Docs | Acceptance-corpus audit + freeze-packet upkeep | `cloud.md` doc pass **done** (commit `9e6587f` — house header, ref fixes; freeze packet now shows the cloud-provider decision as pending on Aaron). |
| QA | Trailing audit of wave-1 commits + shipped hooks | Already surfaced **BUG-001** and **BUG-002** from this pass (§1). Pending report: remainder of the trailing audit. Reports to Bill only. |
| Bill | Awaiting Aaron | Two items pending on Aaron simultaneously — see §3. |

---

## 3. Pending verdicts / pending reports

**Pending verdicts:**
- **`harness.stub` Tester verdict** — in progress. Releases J9 fully and, on PASS, clears the item for commit.
- **BUG-001 re-verification** — pending J8's fix; returns to the Tester once J8 hands it back.

**Pending on Aaron:**
- **Sprint 0 contract freeze review** — `int.protocol`/`int.serializer`/`int.solver`/`foundation.errors`, packet in `docs/design/README.md`. No ETA. Resolves J10's and (residually) J9's rebase exposure and the two cross-cutting freeze questions (OD f32/f64 precision, duplicate correlation-ID generators).
- **Cloud provider decision** — `docs/cloud.md`, delivered and committed (`4f5dd41`), awaiting Aaron's read/decision. Docs still owes a style pass over it independent of Aaron's decision.

**Pending reports (agents' output not yet landed):**
- QA trailing audit (wave-1 commits + hook pair) — partially landed as BUG-001/BUG-002, remainder still pending.
- BA-2 §-ref fixes + Sprint 4 criteria delivery.
- J10 (`ui.core`), J11 (`foundation.det`), J12 (`foundation.registry`), J13 (`legacy.versionguard`) — all four builds in progress, none yet reached the Tester.
- J8's BUG-001 fix.

---

## 3a. Incident / constraint log

- **VERSION-fixture staging incident (2026-08-09):** a junior's staged `VERSION` test fixture rode along into an unrelated docs commit via a concurrent agent's dirty staging area. Caught and reverted within two commits (`a6885e5` reverts the stray fixture; `9b6e1b7` records the rule fix). Root cause: the git index is shared mutable state across concurrent agents — nothing junior- or lead-specific went wrong beyond that shared-resource hazard.
- **New rule — staging-area discipline (v1.5.1)**, `docs/planning/dev-team-process.md`, commit `9b6e1b7`:
  1. Juniors never leave anything staged between tool calls — any stage→verify→reset test sequence must complete atomically inside a single command invocation.
  2. The lead commits using explicit pathspecs (`git commit -m "..." -- <paths>`) or verifies `git diff --cached --stat` matches the intended set immediately before committing — a concurrent agent's staged file must never ride along.
  - RM should treat any future commit touching unexpected paths as a v1.5.1 violation and flag it immediately in the next board refresh.
- **BUG-001** and **BUG-002** (both QA findings, 2026-08-09) are tracked in §1's Sprint 0 table and the dispatch queue in `team-board.md` — cross-referenced here since they're process-relevant (BUG-001 is the first bounce exercised under the new "bounces don't consume a dev slot" rule; BUG-002 is scheduled work, not yet dispatched).

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
6. Reconstruct dev-slot occupancy: the 4 cap slots are J10–J13 (all building). **J8 is off-cap on a bounce (BUG-001)** — bounces don't consume a slot per v1.5, so don't count him against the cap, but also don't treat him as available for a fresh dispatch until BUG-001 clears the Tester. J9 is released (harness.stub dev-complete, with Tester) and is the more likely near-term recipient of the next dispatch.
7. `foundation.data` (MOD-006) and `tool.bow` (MOD-007) both have **active** criteria and no dev — these are the next dispatches the instant a cap slot frees (J9 on his harness.stub verdict, or whichever of J10–J13 finishes first; `tool.bow` sequenced behind `legacy.versionguard`'s ship for hook-surface reasons, not a hard BOW dependency).
8. **BUG-002** (golangci v2 config defect) is scheduled into the Sprint-1 lint-arming pass (`feat.detgate`) — not dispatchable yet, don't assign a dev to it before that sprint context exists.
9. Re-verify cap counts before recommending any new dispatch (4 Jnr devs / 1 Tester / 2 BAs / 1 Docs / 1 QA / 1 RM) — the cloud.md writer slot is sunset and permanently off the roster, not available for reuse without Bill/Aaron standing up a new one-off.
10. Check `docs/planning/dev-team-process.md` for the v1.5.1 staging-area discipline rule (commit `9b6e1b7`) before running any commit on the lead's behalf — use explicit pathspecs or verify `git diff --cached --stat` first.
