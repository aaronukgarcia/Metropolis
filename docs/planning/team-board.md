# Metropolis Team Board

**Maintained by:** Resource Manager (RM), advisory only — Bill executes all dispatches.
**Last updated:** 2026-08-10 (refresh #4 — Bill's own callout: the board had gone badly stale under a narrow serial queue while RM/BAs/Docs/QA sat idle; full rebuild from `docs/build-log.md` + live BOW + Bill's status. Sprint 1 closed, CI fixed twice (BUG-004/005, then BUG-021 self-raised), a security sweep ran to 25 findings, dev-team-process is now **v1.8** [Destructive agent stage, v1.7 assumption rule + fast path, "fixing a fix" logs as it goes, four weakness patterns, `git stash` banned for non-leads, golangci-lint in the Tester baseline]).
**Charter:** `docs/planning/dev-team-process.md` v1.8 — see §"The Destructive agent (v1.8)", §"Second Tester (v1.6)", §"Assumptions are logged or the work is rejected (v1.7)", §"Saturation rule & Resource Manager (v1.5)".

---

## Agent status

| Agent | Role | Current assignment | Status | Return point / next event |
|---|---|---|---|---|
| Bill | Lead | Dispatch, review, Aaron liaison; raised BUG-021 against himself and fixed it same day | — | — |
| RM (this agent) | Resource Manager | Board + checkpoint refresh #4 | busy | Delivering ranked recommendation + saturation flags now. |
| Dev-1 | Jnr developer | `foundation/registry` — SEC-020 wave 3 (one of the "two Registries") | busy | Tester, then Destructive re-attack, on completion. |
| Dev-2 | Jnr developer | `foundation/solver` + `foundation/errs` — SEC-020 wave 3 (`Logger` type) + likely BUG-012 (solver payload cap) | busy | Tester, then Destructive re-attack. |
| Dev-3 | Jnr developer | `internal/protocol` — SeqTracker (SEC-020 wave 3) + **SEC-023** (SubscriptionAllocator, same copyable-mutex class) | busy | Tester, then Destructive re-attack. |
| Dev-4 | Jnr developer | `internal/ui/screens` — both screens (SEC-020 wave 3) + **SEC-009** (MapScreen unbounded-Extent OOM, P1) | busy | Tester, then Destructive re-attack. |
| Tester-1 | Tester | **Idle**, cleared its queue | idle | Awaiting the first Dev-1..4 item to land — don't batch all four, feed Tester-1 the first one that's ready. |
| Tester-2 | Tester | **Idle**, cleared its queue | idle | Same — feed the second-ready item straight to Tester-2, independent of Tester-1's. |
| Destructive-1 | Destructive | **Idle** | idle | See saturation recommendations below — proposed: repo-wide sibling-hunt for SEC-024's pattern (exported mutable field, invariant-by-doc-comment-only) and/or the SEC-022 `%s`-vs-`%q` sibling-hunt ASM-060 flagged as scoped too narrowly. |
| Destructive-2 | Destructive | Re-attacking `debug.State` (SEC-020 wave 2 re-check), `StubEngine` queued behind it | busy | Continues to StubEngine on completion; both feed into wave 3's closure of the nine-type ruling. |
| Destructive-3 | Destructive | **Idle** | idle | Proposed: independent re-verification of BUG-021's self-fix (`8833e52`) — Bill fixed and verified it locally, but it hasn't had an independent adversarial or Tester pass, and it's literally about "can we trust what we think is true." |
| BA-1 | BA | Writing criteria for the security backlog (SEC-011, SEC-025, and the remaining BUG-* items) — closes a real gap: every security fix to date was dispatched off Bill's ad-hoc briefs, not BA criteria | busy | Criteria delivery, item by item. |
| BA-2 | BA | Refreshing Sprint 2 criteria to `active` (Sprint 2 has not started) | busy | Criteria ready the moment Sprint 2 opens. |
| Documentation | Documentation | **Idle** | idle | Proposed: doc pass on the four weakness-pattern write-ups' cross-links (already in `dev-team-process.md`) and on `data/security-scans.json`'s currency now that wave-3 modules are mid-flight — cheap, real, keeps it fed. |
| QA | QA | **Idle** — Bill dispatching next | idle → about to be busy | Proposed target: independently re-check the SEC-020 chain's closure claim (the "13 attack shapes, clean" re-attack) — the highest-stakes closed claim of the session, worth a second independent look before it's treated as settled. |

---

## Cap compliance

| Role | Cap (v1.8) | Current count | Holders | Status |
|---|---|---|---|---|
| Jnr developer | 4 | 4 | Dev-1..4 | **At cap.** |
| Tester | 2 | 2 | Tester-1, Tester-2 | At cap by headcount, **0 busy** — both idle. Live saturation opening, see below. |
| BA | uncapped (lifted 2026-08-09, disjoint ownership absolute) | 2 | BA-1 (security backlog), BA-2 (Sprint 2) | Fine — disjoint ownership holds. |
| Documentation | 1 | 1 | Docs | At cap, **idle**. |
| QA | 1 | 1 | QA | At cap, **idle → about to be dispatched**. |
| Resource Manager | 1 | 1 | RM (this agent) | At cap. |
| **Destructive** | **not documented in `dev-team-process.md` v1.8** | 3 | Destructive-1 (idle), Destructive-2 (busy), Destructive-3 (idle) | **Doc gap, flagged below** — every other role has an explicit cap in the Saturation-rule section; Destructive doesn't. Not a breach (nothing to breach), but worth writing down now that 3 are precedent. |

### Saturation gaps — flagged, including on Bill

This is the direct answer to "flag any breach or gap, including on me": **six of eleven active-role agents are idle right now** (Tester-1, Tester-2, Destructive-1, Destructive-3, Documentation, QA-until-dispatched) while four juniors work and one Destructive agent works. None of these are blocked by a genuine dependency — they're idle because the wave was dispatched as a narrow batch (4 devs) without lining up the next stage's work in parallel, which is exactly the pattern Bill named as the problem. Concretely, right now, before wave 3 even lands:
- **Both Testers** could be pre-briefed to take the first two wave-3 items the instant they're dev-complete, rather than waiting for a batch of four to finish together (this was the v1.6 rationale for a second Tester in the first place — don't let it revert to serial-by-default).
- **Destructive-1 and Destructive-3** have no dependency reason to be idle — see their proposed assignments above. Both are useful, independent, and don't touch the four packages Dev-1..4 are actively editing (avoiding the "never two agents in the same package concurrently" rule from the build log's resume order).
- **Documentation** has real, low-risk work available (scan-stamp currency, pattern write-up cross-links) that doesn't need to wait for wave 3.
- **QA** — Bill is already correcting this one.

---

## Security backlog — live findings

25 findings total, 8 open (per `node claude-bow.js weakness`). Two weakness classes are flagged RECURRING (≥3): **input-validation ×10**, **concurrency-safety ×8** — both training signals per the process, not just defect counts.

| Code | Pri | What | Status |
|---|---|---|---|
| **SEC-024** | **P0** | `serialize.Header.DebugTouched` is a plain exported bool — any `*Header` holder can clear it directly, bypassing `debug.State` and every SEC-020 guard entirely. Found this morning re-attacking SEC-020 wave 2. Fix belongs to `int.serializer` (unexport the field, `TouchDebug`/`MergeDebugTouched` as the only mutation path). | **OPEN, UNASSIGNED.** Not part of any of Dev-1..4's current work. See ranked recommendation — this is the single most urgent item on the whole board. |
| SEC-020 | P1 | The class itself: nine copyable mutex-bearing structs repo-wide. Aaron ruled full pipeline on all nine, no tiering. Wave 1 (`InProcTransport`) and wave 2 (`StubEngine` + `debug.State`, latter under re-attack now) done; wave 3 (two Registries, `Logger`, `SeqTracker`, two UI screens) is Dev-1..4's current work — this is the **last wave** for the nine-type scope. | 6 of 9 types in flight or re-attacked now; 3 already closed. |
| SEC-023 | P2 | `SubscriptionAllocator` — same copyable-mutex-plus-aliased-reference shape as `InProcTransport` pre-fix, no `checkNotCopied` guard. | Bundled into Dev-3's protocol work. |
| SEC-011 | P1 | No render path in `ui.core`/`ui.widgets`/`ui.screen.debug` filters control/escape characters before writing to the terminal — terminal-escape injection via any untrusted string (e.g. F12's error tail). Linked to SEC-022 (already fixed, same root cause, different surface) — this is weakness pattern #3 in the flesh: "fix the class, not the demonstrated instance." | **OPEN, unassigned** — broader scope than Dev-4's `ui.screens` work (spans `ui.core`/`ui.widgets` too). BA-1 is writing its criteria now. |
| SEC-009 | P1 | `MapScreen.applyFullLocked` allocates a grid sized directly from an unbounded wire-supplied `Extent` — a crafted patch can OOM/crash the UI process. | Bundled into Dev-4's UI-screens work. |
| SEC-025 | P2 | `IsOn()` fails closed to `false` on a copy with no error surfaced — a plausible `IsOn()`-only save-carry-forward helper could misreport a debug-touched session as clean. | Open, BA-1's backlog. |
| SEC-021 | P3 | Secret guard's high-entropy detector flags descriptive hyphenated correlation IDs — a real project convention, false-positive class. | Open, low urgency. |

**BUG-021 (P1, Bill raised against himself)** — `main` red for 3 commits because a CI run was watched *starting*, not confirmed *finished*. Fix committed `8833e52`; **both DoD items are only half-closed**: golangci-lint is now in the Tester baseline (`f12c8ac`, done), but **branch protection on `main` is still not configured** — the mechanical half of BUG-006's original interim-control fix.

Remaining lower-priority BOW backlog (none P0/P1, none currently blocking): BUG-011 (P3, error-code conflation), BUG-012 (P2, solver payload cap — Dev-2), BUG-013 (P3, buildinfo registry key), BUG-014 (P2, BOW schema missing ASM/SEC enum values on fresh DB), BUG-015 (P3, plan-guard NUL byte), BUG-016 (P3, seal() perf), BUG-017 (P2, BOW description shell-expansion corruption), BUG-018 (P2, `RenderLoop` atomic.Bool copy defeat), BUG-019 (P3, `StartSubscriptionPump` no copy guard), BUG-020 (P2, `StubEngine.Run` silent-exit ambiguity).

---

## RM's honest read on the security/Sprint-2 balance (Bill's direct question)

**The spend to date was justified.** SEC-001 was a real path-traversal with both read and write impact; the SEC-003→014→016→018→019 chain was a genuine fatal-crash class that passed every test in the suite; BUG-007 was a live panic path; five hook bypasses defeated guards meant to be fail-closed. None of that was theoretical, and Aaron's "all nine, full pipeline" ruling on SEC-020 was correct to overrule the tiered option — this session demonstrated twice that `go vet copylocks` doesn't catch every copy form that matters.

**But the balance is now due to shift, and the fresh P0 (SEC-024) is the reason to say "one more wave," not "keep going indefinitely."** Sprint 2 has had zero movement across a very long session. Once SEC-024 is fixed and wave 3 lands (closing the nine-type SEC-020 scope) and SEC-011 (the one other live P1) is fixed, **there is no remaining P0/P1 justification for holding Sprint 2 fully paused.** Everything left after that point (SEC-021/025, BUG-011/012/013/014/016/017/018/019/020) is P2/P3 — real, worth doing, but not gate-severity. Recommend running the remaining backlog and Sprint 2 as **two parallel tracks** from that point rather than fully sequencing them — that's what the 4-dev-cap and 2-BA structure exist for.

## RM's ranked dispatch recommendation

| Rank | Item | Action |
|---|---|---|
| 1 | **SEC-024 (P0)** | Assign now — doesn't need to wait for wave 3 to fully clear. It's a live bypass of the exact guarantee `feat.debugmode` exists for (AC-3/4/12/15), found *this morning*, currently touching nobody's queue. If dev cap is genuinely full, this is the textbook case for a P0 preempting a P2 in the current wave rather than queuing behind it. |
| 2 | **Let wave 3 land** (Dev-1..4) | Already in flight — closes the SEC-020 nine-type scope. Feed Tester-1/Tester-2 incrementally as each of the four completes, not as a batch (see saturation section). |
| 3 | **SEC-011 (P1)** | Next dev slot freed by wave 3. Cross-cutting, same root cause as SEC-022 (already fixed) — a single shared sanitiser fix per weakness pattern #3's own lesson, not four separate patches. BA-1 is already writing its criteria, so this should be dispatch-ready the moment a slot opens. |
| 4 | **Branch protection on `main`** (BUG-021's second DoD item) | Cheap, config-only, not a dev-slot item — can run in parallel with anything above, recommend Bill or a fast tooling task does it directly. |
| 5 | **Declare the P0/P1 security scope closed and open Sprint 2 in parallel** — `MOD-013 harness.replay` (dep-ready: `INT-001`/`INT-002` both done) is the keystone Bill named. Don't wait for SEC-021/025 or the P2/P3 BUG-* backlog — run those alongside Sprint 2, not ahead of it. |

**On today's idle capacity specifically** (don't wait for rank 1-4 to create work): dispatch Destructive-1/Destructive-3 and both Testers per the saturation section above, immediately, in parallel with the SEC-024 assignment — there's no dependency reason any of them should still be idle five minutes from now.

---

## Incident / process log (v1.8 additions this refresh)

- **CI had never been green, project history** — BUG-004 (CRLF vs `gofmt` on Windows runners, invisible locally) + BUG-005 (scheduling-dependent test) both fixed (`d5c9c19`, `76e1961`). First green run `31314318798`.
- **BUG-021 — Bill raised against himself.** Watching a CI run *start* is not the control that was promised; confirming it *finished* is. Fixed (`8833e52`); the branch-protection half of its DoD is the one open item above. `golangci-lint` now in the Tester baseline (`f12c8ac`) because CI's blocking lint job was stricter than anything any human tool ran locally — a lint error walked past a junior, a Tester, a Destructive agent, and the lead because everyone ran a different tool and called it the same thing.
- **The Destructive agent stage (v1.8)** — sits after Tester, before Docs; may reject work back to the same junior exactly as a Tester FAIL does; findings are `SEC-` BOW items with a mandatory weakness class; never edits, never fixes.
- **Four weakness patterns documented** in `dev-team-process.md`: (1) an invariant stated in prose is not enforced — SEC-024 is a fresh instance of exactly this; (2) a value duplicated across a module boundary needs a provably-failing drift test; (3) fix the class, not the demonstrated instance — SEC-011/SEC-022 is the live example; (4) a value in a privileged position is input, however inert it looks (`input-validation` ×10, the largest recurring class, is mostly metadata — names-as-paths, sizes-as-allocations — not payload).
- **`git stash` banned for non-lead agents** (v1.5.1 addendum, 2026-08-09) — it sweeps the whole shared tree; a junior self-reported a near-miss.
- **v1.7 assumption rule** caught the lead three times on unwritten verbal steers; produced two structurally interesting finds (ASM-047 predicted its own failure mode before it happened; ASM-041 identified a test-mechanism gap only visible from the choice-maker's side). v1.7.1 rejected the Tester authoring assumptions on a FAIL's behalf (rationale must come from whoever made the trade-off). v1.7.2: fixing a fix logs as it goes.
- **Session bounce (2026-08-09, carried forward for history):** previous RM died mid-cycle with no surviving transcript; board and checkpoint were rebuilt cold from BOW + git. No work was redone.
