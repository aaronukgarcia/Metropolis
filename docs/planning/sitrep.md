# SITREP — Metropolis Project — 2026-08-18 REFRESH

**Date:** 2026-08-18 (morning) · **Author:** Bev (solo session, Aaron-directed) · **Supersedes:** the 08-17 evening sitrep (retained below as baseline/appendix — its R1–R6/A1–A7 numbering is referenced throughout).

**Method:** four parallel Sonnet evidence lanes (Windows event-log forensics; metro-DB overnight timeline via raw read-only SQL; git/CI/working-tree audit; re-verification of all fast-aging 08-17 claims), cross-reconciled by Bev. Every timestamp below was reconciled across four independent clocks (file mtimes, raw DB rows, Windows event log, gh) after discovering the claude-sync display skew (BUG-264).

---

## 0. The overnight incident — what actually happened

Aaron's morning report: four sessions (Bev; Bill-architect and Bob-RM on deepseek; Bob-watcher on gemini) were "flat out all night", and by morning every command shell was gone.

**Finding: they were not flat out all night, and they did not individually crash overnight. The night had three distinct events:**

| Time (local) | Event | Evidence |
|---|---|---|
| 18:00 → 23:22 | Heavy, continuous four-identity work — the sessions were executing the 08-17 sitrep's own A-list (see §1) | bow_comments/bow_items/dispatch_events continuous; ~270 ASM closes, 6 destructive verdicts, 16 new items, PR #8 opened |
| **23:22:29–44** | **Near-simultaneous death of the working sessions.** Last file write 23:22:29 (`astgate/accepted-findings.json`), last DB write 23:22:36, then a `powershell.exe` crash at 23:22:44 — `Win32Exception` in `ConsoleControl.GetConsoleScreenBufferInfo` (a known managed console-host fault; second identical crash 00:23:15). Zero node/WindowsTerminal/conhost/mysqld faults all night. Bev+Bob permits died unreleased (hard kill); Bill+Ben rows show clean release earlier. → BUG-265 | WER 1000/1001; sync_permits; mtime sweep |
| **05:21:17** | **Full clean machine reboot** via the Start-menu restart path: User32 1074 from `StartMenuExperienceHost.exe` "on behalf of AzureAD\aarongarcia", reason "Other (Unplanned)". **Not** Windows Update (only a post-boot Defender signature update at 05:33; no pre-boot restart-pending state), not sleep, not power loss (no 6008/41). This is what left zero windows by morning. | System log 1074/109/6006/6005; LastBootUpTime 05:22:11 |

**RESOLVED (Aaron, 08-18 interview): the 05:21 restart was Aaron himself.** Incident closed — the only real defect of the night is the 23:22 console-host death and the 10-hour unnoticed gap (BUG-265).

Two forensic contaminants were identified and filed: **BUG-264** — claude-sync.js displays timestamps 1h behind local (the "22:01 silence" was really 23:01, and this morning's "08:13" checkin was 09:13); **BUG-265** — no supervision/relaunch exists for dead shells (the RF 2026-05-25 hardening pattern is the prior art). Also of note: the sessions left **no Vestige summaries** — no `/bye` ran anywhere, consistent with hard death.

---

## 1. What the overnight sessions delivered (A-list scorecard vs the 08-17 sitrep)

The four sessions spent 18:00–23:22 executing the 08-17 recommendations. Verified state this morning:

| 08-17 rec | Status this morning |
|---|---|
| A1 CI diagnosis | **Done-diagnosed, not fixed**: BUG-254 (new P0) carries a 4-cause root-cause analysis (lint dead code, stale test tripwire needing doc adjudication, perf-noise floor/measurement-window). Fix lanes dispatched 18:08 — **never confirmed landed; no CI run since 20:40**. Likeliest work-in-flight at death. |
| A2 open the PR | **Done**: PR #8 (feature/services-astgate → main) opened 18:14. **CI ran twice, both red** (same BUG-254 causes). The wave is no longer CI-blind — it is CI-covered-and-failing, a materially different state. |
| A3 close Baseline One | **Substantially done**: MOD-031 closed `done` 18:06 with refs + Destructive ACCEPT; a **12/24-month deterministic headless run passed with conservation invariants holding**. Every FEAT-083 dependency now shows done. FEAT-083 itself (P0) awaits the watchable `cmd/metropolis` run + Aaron's close. |
| A4 BUG-253 backdoor | **Remediated on disk, uncommitted**: rebuilt claude-sync.js = HEAD + reviewed Bev-slot change only; `e8ce967d` immortal-window, auto-evict, keeper-spawn, BOOT_ID regression all verifiably absent; 38/38 tests pass; no keeper process exists or runs; DHCP rewrite quarantined to scratch. BUG-253 stays open pending commit. |
| A5 graph remediation | **Started in order**: code.json regenerated (+1,717 lines uncommitted, 22:53), spec-lint.js + codejson-audit.js reworked (23:18–23:21) — the very cluster being written at the moment of death. `claude-spec-guard.js` still does not exist (correct per A5's ordering — it comes last). GR#25 remains prose-only. |
| A6 FEAT-084 re-baseline | **Done + execution begun**: re-baselined 330→1,204; ~270 ASMs closed overnight (open ASM now **885**, down from 1,204); BA prep doc written; bulk execution held pending the commit sweep. |
| A7 checkpoint + hygiene | **Done**: checkpoint REFRESH #13 corrects #12's falsehoods; 6 of 7 lagging statuses flipped (MOD-059 open **by design** — its ref was mis-filed, real FDI engine unbuilt); debris deleted (_edges_scratch, bow-ui.html, spiral *.bak); BUG-255 filed for the refuse/disposal races. |

Also overnight: MOD-044 coastal ACCEPT + commit 863bd31 (21:35, the night's last commit); 4 destructive REJECTs (FEAT-107, BUG-246, FEAT-149/150); guard audit clean bill (FEAT-040/028/080/136/024); 13 new audit-finding items (BUG-257..263, FEAT-151..156).

## 2. Red flags — delta vs 08-17

- **R1 (CI red / zero coverage) → now BUG-254 (P0)**: improved in kind (PR #8 exists, causes diagnosed) but still red on both main and the PR; branch now **53 ahead** of origin/main. The fix wave died mid-flight — **this is the top thread to pick up**.
- **R2 (permit backdoor) → defused, uncommitted**: procedural risk only — the good fix sits in a modified-unstaged file (GR#24 exposure).
- **R3 (GR#25 unenforced) → unchanged**, being remediated in the correct order; the death froze the A5 lane at spec-lint/codejson-audit rework.
- **R4 (BOW exhaust) → improving**: open 1,545→~1,238; ASM 1,204→885 (72% of open). Treadmill slowed, not stopped.
- **R5 (misleading recovery surface) → fixed** by REFRESH #13. New instance of the class: the tz display skew (BUG-264) is itself a mislead-the-next-session hazard.
- **R6 (hygiene) → partially cleared**; new debris: 5 `_tmp_*`/`_baseline_audit` scratch files in tools/plan.
- **NEW R7 — the frozen sweep**: 121 uncommitted files (63 acceptance docs, 32 Go engine files, code.json +1,717, reworked audit tooling, the BUG-253-clean claude-sync.js, 4 legit new tools) is a coherent snapshot of five workstreams frozen at 23:22, **none of it committed anywhere**. Per GR#24 this is the largest single loss-surface in the project right now.
- **NEW R8 — session fragility**: two console-host crashes took down the working sessions and nothing noticed for 10 hours; no watchdog, no relaunch, no dead-man alert (BUG-265).

## 3. Recommended actions (for Aaron's approval)

1. **Answer the 05:21 reboot question** (§0) — if nobody clicked Restart, something on this box can reboot it and that needs its own investigation.
2. **Commit sweep of the frozen tree first** (GR#24): the BUG-253-clean claude-sync.js + tests, the acceptance-doc wave, the Go engine batch, code.json regen + tooling — in dependency order, verdicts where required. Requires Bill authority or your explicit direction to Bev.
3. **Resume BUG-254 (P0)**: re-dispatch the four fix lanes, re-run PR #8 CI, merge (rebase-only) once green — closes R1 and gives the 53-commit wave its evidence trail.
4. **Close FEAT-083** with the watchable run you asked for — the engine side is done and verified.
5. **Session hardening** (BUG-265): move long-running CLI shells off Windows PowerShell 5.1 console hosts and/or add a watchdog relaunch + dead-man alert; re-arm standing loops at checkin.
6. Housekeeping: delete tools/plan `_tmp_*` debris at sweep time; BUG-264 tz fix.

## 4. Aaron's rulings (08-18 interview — STANDING ORDERS)

1. **05:21 reboot was Aaron** — incident closed.
2. **Bev has commit authority this session** and sweeps the frozen tree now, dependency-ordered, small pushes.
3. **BUG-254** fix lanes resume **after** the sweep; PR #8 re-runs CI.
4. **Bev rebase-merges PR #8 on green** (never squash; verify noreply authorship after).
5. **FEAT-083** closes after sweep + green CI, with the watchable run as capstone.
6. **Hardening = cheap fix first**: move metro launcher to Windows Terminal/pwsh 7 (dodges the ConsoleControl fault class) + BUG-264 tz fix; watchdog/relaunch stays parked P2.
7. **All 40 pre-regime unverdicted done items get real retroactive destructive rounds** (waiver option declined) — **batches of ~8**, spine-critical first (MOD-020, INT-001/002 in batch 1), spread so game-code lanes keep priority.
8. **Accelerator utility magnitudes: row-by-row review with Aaron NOW** (not deferred to balance pass).
9. **FEAT-041 numeric ruling**: Bev prepares a one-page decision brief after the sweep; Aaron rules on it.
10. **No night runs** until hardening + sweep debt are cleared; the 4-window multi-model crew stays down.
11. **FEAT-084 ASM fold resumes after PR #8 merges** (not merely after the sweep).

---

# APPENDIX — SITREP baseline of 2026-08-17 (evening), retained verbatim

**Date:** 2026-08-17 (evening) · **Author:** Bev (master review, Aaron-directed) · **Status at the time:** point-in-time snapshot; Bill's commit sweep was in flight while this was compiled. **Read the 08-18 refresh above for current truth — §1/§2 there track what changed.**

**Method:** Vestige memory sweep + four independent read-only review lanes (code.json registry audit, BOW database triage, git/CI/reality cross-check, working-tree classification), findings cross-verified against each other. Verification standard applied: verify the thing, not the report of the thing — every headline number below was counted, not quoted.

---

## 1. Mission & aim

From `docs/METROPOLIS-MASTER-v2.1.md` §I.1, verbatim:

> "The player starts with money, a two-kilometre-square tile of real Folkestone topography wiped of everything man-made except the M20 and one junction, and zero inhabitants. The objective is Centopolis: one hundred million citizens, reached across game-centuries of migration-driven growth over an expanding map of real East Kent. The failure states are insolvency and the Detroit spiral — a mass-emigration death whose mechanism is the same attractiveness engine that drives growth, run in reverse."

Design north star (Aaron, 08-11): **"the game is the juggle"** — finite pie, skin in the game, snowball from a hamlet, fine-grained options as content, long-horizon bets that pay. Every feature is held to its five-question test.

**Active milestone: FEAT-083 "Baseline One"** (Aaron, 08-14: "it's a game, not NASA code") — the loop must RUN: citizens consume, money moves, you build, migration responds, watchable via cmd/metropolis on the real engine.

---

## 2. Bottom line

**The game is one BOW item away from Baseline One, and the code delivery machine is working. What has broken is everything around it: main's CI is red and unrecorded, a 50-commit wave has never been CI-tested, the coordination tooling has acquired an unreviewed rewrite containing a permit-system backdoor, the BOW is 78% untriaged machine exhaust, and the newest Golden Rule's enforcement mechanism was never actually written.**

The positive claims in the paper trail are sound — all 10 sampled BOW done-claims verified against git perfectly, every done module/feature has a commit ref. The drift is all in the *negative space*: what is open, what is red, what was never built.

---

## 3. Where the build actually is

### Baseline One spine: 9 of 10 done

| Item | Role | Status |
|---|---|---|
| MOD-017 world · MOD-018 citizens · MOD-020 market · MOD-021 consumption · MOD-022 finance · MOD-026 build · MOD-028 households · MOD-029 attract · FEAT-013 alerts | spine modules | **done**, committed 08-14/08-15 |
| FEAT-082 composition root | the keystone | **done** (`ca0f7f8`, Destructive ACCEPT) — six modules wired to the tick, `cmd/metropolis` is OFF StubEngine |
| **MOD-031 projections** | **sole blocker** | in_progress — but Tester PASS 21/21 ACs, Destructive ACCEPT 08-14, error range assigned. **It needs a commit sweep and a close, nothing else. FEAT-083 has been one administrative step from unblocking for 3 days.** |

### Build surface

50 engine module directories exist on disk (every one has ≥1 test file; 13 have exactly one — thinnest: market 3 src/1 test, spiral 12/2, freight 14/4). The services wave (14 commits) plus the 08-17 wave (26 more) landed engine.roads, news, coastal, mining, defence, crime, farming, rail, census, airport, accelerator, fiscal, social, refuse, education, wellbeing, chemicals, comms, capexport, checkpoint, maintenance, and more. Branch `feature/services-astgate` is 50 ahead / 0 behind origin/main, fully pushed (no GR#24 violation).

Notable structural facts:
- The build has run **far ahead of and out of order from** the sprint plan: S9–S11 modules have committed code while their sprint gates were never run (sprint 0 still shows 29 open items).
- **11 engine modules on disk appear nowhere in the sprint plan** (accelerator, airport, census, checkpoint, compose, helper, maintenance, spaceport, worklife, debug, save); `compose` (the FEAT-082 keystone!), `save`, and `debug` are **absent from master-plan-v2.1.json** — the keystone module is in code.json's blind spot.
- **12 sprint-plan modules have no directory at all**, including engine.traffic (S5) — roads is complete while traffic doesn't exist, so S5's exit gate cannot be met. (Traffic is deliberately deferred/coarse for Baseline One; MOD-023 remains blocked on the FEAT-041 numeric-type ruling, which is Aaron's open decision.)
- Commit `[mkey]` tag convention flipped mid-wave (dotted keys like `[engine.fiscal]` → BOW codes like `[MOD-024]`); one commit (`3d1973f`) is tagged `[foundation.num]` with a `foundation.registry` subject.

### Velocity

617 items closed in 10 days, peak 214/day (08-13), **last-3-day mean 8.3/day (−93%)**. Meanwhile 1,056 new items were created in the same 3 days — a **42:1 create-to-close ratio**. The scary 1545-open headline is a creation-rate artifact (ASM/SEC generators), not a build-throughput failure.

---

## 4. Red flags, ranked

### R1 — main is RED and a 50-commit wave has had ZERO CI  *(P0)*
The last 10 CI runs all failed; main has been red for ≥7 consecutive pushes since 16 Aug. On main's tip: build-test-vet ✅, determinism-gate ✅, **node-test ❌, lint ❌, perf-1m-probe ❌, perf-smoke ❌**. This contradicts GR#21's stop-the-line posture, the ci.yml header ("main is always green"), and checkpoint REFRESH #8's "CI green" claim — nobody recorded it going red. Worse: CI triggers only on push/PR to main, PR #7 merged on the 16th, no new PR exists — so **all 40 commits of 17 Aug have never been built, linted, vetted, or determinism-gated by CI**. perf-1m-probe, documented as "LIVE, REQUIRED and HONEST", is among the failures.

### R2 — unreviewed rewrite of the permit system with a hardcoded backdoor  *(P1, BUG-253 filed)*
The uncommitted `claude-sync.js` diff fuses the small Bev 4th-slot change with a large unreviewed "DHCP rewrite": (a) `isProcessAlive()` returns true unconditionally for hardcoded window id `e8ce967d-…` ("Gemini CLI … ALWAYS alive") — one session made **immortal** in the permit system; (b) `detectAndResolveConflicts()` auto-releases *other live sessions'* permits on every checkin/renew, contradicting the human-only force-evict contract; (c) BOOT_ID granularity changed 10 s → 1 hour, silently breaking reboot-voids-permits; (d) hardcoded `C:\Users\aarongarcia` path, fails **open** on error; (e) it spawns untracked `coordination-keeper.js` (a live unregistered dependency — commit them together or reject together). Bill was warned mid-sweep before this file could be committed.

### R3 — GR#25 is enforced by a guard that does not exist, over a graph that is wrong  *(P1)*
`claude-spec-guard.js` — named by CLAUDE.md, golden-rules-detail, and the checkpoint as GR#25's mechanical enforcement — **was never written**. No hook, no pre-commit, no CI wiring. GR#25 is currently prose-only. The received story also needs two corrections:
- **code.json is NOT stale relative to the master plan** — they are equivalent in every measurable dimension (160/160 modules, 315/315 edges, 480 unique GUIDs, 0 scalar diffs). "Regenerate code.json" fixes nothing. The real drift is **plan-vs-Go-source**: 292 actual cross-module imports (259 prod-only) have no registered edge; 190 registered edges have no backing import (103 among built modules). The checkpoint's "131 phantom edges" figure is not reproducible. Fix route: edit `master-plan-v2.1.json`, then regenerate.
- **The linter that would do the blocking is defective**: spec-lint's Go-symbol check is a whole-file substring match that has never fired; its key regex cannot see `foundation./harness./tool.` keys at all (which is where 178 of the missing edges point); 53 of 201 acceptance files are silently exempt by filename; direction is collapsed (outbound ∪ inbound). Wiring the guard today would block **104 of 201 acceptance files (441 findings)** — mostly correct prose — while passing the categories containing the real gaps.
- `codejson-audit.js` only checks registry→code, never code→registry — which is exactly why 292 unregistered imports were invisible to it. Its uncommitted 4-line "invariant throw" is dead code (unreachable branch) — reject or drop.

### R4 — the BOW is drowning in its own exhaust  *(P1)*
Of 1,545 open items, **1,204 (78%) are ASM assumptions — 95% with zero comments**, 609 created on 16 Aug alone; 184 more are machine-emitted SEC findings. Real open build work is ~157 items (~10%). 8 of the 14 "P0s" are uncommented ASM assumptions. FEAT-084 (the ASM close-out programme, gated on today's 18:00 worker-budget reset) has a plan baselined at 332 open ASMs — **it is 3.6× out of date before it starts**. Aaron's fold-don't-bare-close directive stands; the fold plan needs re-baselining first.

### R5 — the recovery surface would mislead a cold session  *(P2)*
checkpoint.md's top refresh (#12) claims roads is "the ONLY module still fully uncommitted" (it's committed at HEAD), lists six "commit-ready awaiting sweep" items that are all already committed, and cites two "in-flight" destructives that have landed. Older refreshes still assert present-tense states 50+ commits stale; a cold session following #9's resume order would redo committed work — against the file's own rule 5. **BOW status also lags git**: seven committed modules (MOD-043/044/046/059/067, FEAT-015, MOD-024) still read open/in_progress.

### R6 — hygiene debt (accumulating, not acute)  *(P2/P3)*
50 done BUGs have no git ref; `bow_git_refs` double-writes every commit (manual + hook, no dedup); `mkey` is NULL on all BUG/ASM rows; MOD-023's "blocked" exists only in comment prose (no blocked status); 40 pre-regime done items (incl. spine dep MOD-020 and both INTs) have no destructive verdict; 45 items hold ACCEPT verdicts while still open. Working-tree debris confirmed: `_edges_scratch.txt` (contains two real refuse/disposal `-race` findings that deserve a BUG item, then delete), `bow-ui.html` (434 KB rendered DB snapshot — gitignore), `spiral/*.other-agent.bak` (orphaned — the files they back up no longer exist). Legit new tooling: `tools/bow-server.js` + `bow-ui-template.html` (a real BOW web UI pair), `spec-lint.js` (WIP). The coastal SEC-233/234 fix set + regression tests look complete and commit-ready gated on the MOD-044 verdict; the buildings/consumption accelerator data pair is fine (magnitudes worth a balance-pass glance).

Secrets scan clean. Email scan clean. GR#22 codename scan clean (including the 434 KB HTML, line by line).

---

## 5. What is genuinely healthy

- **Delivery integrity**: 40 of 40 BOW git refs checked resolve to real ancestor commits; every touched file exists; every done MOD/FEAT/INT has ≥1 ref. Zero dependency-graph cycles or dangling edges.
- **The spine landed** and the composition root works — the 08-14 "nothing is wired to the tick" blocker is gone.
- **Determinism gate and build-test-vet are green** even on red main — the two failing-open risks the project fears most are currently the two things passing.
- The permit/coordination system survived a four-slot extension cleanly (Bev is live), and the destructive-review regime demonstrably bites (206 rejects / 256 accepts).

---

## 6. Recommended actions (for Aaron's approval — nothing dispatched yet)

| # | Action | Why first | Suggested lane |
|---|---|---|---|
| A1 | **Diagnose and green main's CI** (node-test, lint, perf-smoke, perf-1m-probe failures on `ba6b0aa`) | GR#21: a red gate stops the line; it's been red since the 16th unnoticed | Ben (QA) — independent diagnosis before anyone "fixes" symptoms |
| A2 | **Open the PR for `feature/services-astgate` → main** so the 50-commit wave finally gets CI; merge only rebase-merge after green | 40 commits with zero CI evidence is the single largest unverified surface | Bill (owns commits/PRs) |
| A3 | **Close Baseline One**: sweep MOD-031's commit, flip it done, flip the seven lagging module statuses, then run cmd/metropolis for a multi-month watch and close FEAT-083 with evidence | Game-first standing order; it's one administrative step away | Bill |
| A4 | **BUG-253**: split the Bev slot change out of `claude-sync.js`, strip the backdoor + auto-evict + boot-id regression, put the DHCP rewrite through the normal pipeline or revert via scratch-copy (never git-restore, GR#24) | A backdoored permit system undermines every coordination guarantee | Bill/Ben, full-tier |
| A5 | **Graph remediation in the correct order**: register the ~292 real edges in `master-plan-v2.1.json` → regenerate → add code→registry direction to codejson-audit → fix spec-lint's dead checks → only then write and wire `claude-spec-guard.js` | Wiring GR#25 enforcement now would block half the correct spec estate | Bob (BA-lead) planning, juniors executing |
| A6 | **Re-baseline FEAT-084** (ASM fold plan: 1,204 open, not 332) and start the fold per Aaron's directive; consider throttling the ASM/SEC generators until close-rate recovers | 42:1 creation:close is a treadmill | Bob |
| A7 | Checkpoint refresh #13 correcting R5's falsehoods; BOW hygiene batch (status flips, ref dedup, blocked status for MOD-023, BUG item for the refuse/disposal races); delete confirmed debris | Cheap, stops the next cold session inheriting the drift | Bob/Ben |

Standing decisions this sitrep surfaces for Aaron specifically: the FEAT-041 numeric-type ruling (still gating MOD-023), whether the 40 pre-regime unverdicted done items get retroactive review or a recorded waiver, and whether the accelerator utility magnitudes (5 GL water / 2 GWh electricity per month) go to the balance-number regime now or at the balance pass.

---

*Compiled by Bev, 2026-08-17. Sources: Vestige (design north star, state chronology 08-08→08-17), metro MariaDB (bow_items/bow_dependencies/bow_git_refs/bow_destructive_verdicts, direct read-only SELECTs), git/gh (branch, refs, CI runs), code.json + master-plan-v2.1.json + generate.js --check, and full diffs of the uncommitted tree. This file is docs-only (Tester-tier, GR#23).*
