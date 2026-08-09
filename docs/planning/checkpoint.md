# HEAVY CHECKPOINT — session bounce point

**Rebuilt by RM at Bill's direction, 2026-08-09, post-recovery from the bounce that killed the previous RM. HEAD is now `b721740`, pushed, tree clean (only an untracked Word lock file `docs/~$cloud.md`, harmless). This file supersedes the prior bf65b9a-era checkpoint and team-board.md (rebuilt alongside this file). A fresh session recovers from THIS + `node claude-bow.js list --by-seq` + `git log -15`.**

## 1. Where we are

- **Sprint 0: build scope 100% COMPLETE.** All items done + committed. **Cloud provider decision is RESOLVED (2026-08-09 ruling): Azure, confirmed** — `docs/cloud.md`'s recommendation is now a ruling, its 3 open questions superseded. Sprint 0 now closes on the narrower **contract freeze review only** — packet in `docs/design/README.md` (4 contract docs + cross-cutting questions: OD f32-vs-f64 [f32 recommended], duplicate correlation-ID generators). **Still pending.**
- **Sprint 1: 8 of 10 items done+committed.** harness.stub (112f880), ui.core (87d8efe), ui.widgets (bf65b9a), foundation.det (f24f9ff, S0 item committed in this run), plus the three items below now COMMITTED but Tester-unverified, plus tool.bow/BUG-003 fix committed. Remaining open: MOD-011 ui.keys (actually Sprint 2 per BOW), FEAT-006/007/008.
- **Last commit:** `b721740`, pushed to origin/main, tree clean.

## 2. COMMITTED-but-unverified deliveries (Aaron's direct "sync git" order, 2026-08-09 — BEFORE Tester verdicts existed)

Three juniors delivered and Bill committed them ahead of the normal Tester-gate on Aaron's explicit instruction. All three BOW items remain `in_progress` — this is deliberate, not an error — pending Tester verdicts:

1. **engine.core (MOD-012, J16 delivered, commit `f81e5d7`)**: `internal/engine/core/**` — orchestrator, 35 tests, 18ns/0-alloc benchmark. Verify per `docs/planning/acceptance/engine.core.md` + queued checks (re-run benchmark, POOL-SIM pool 1-vs-14 byte-compare, scrambled barrier test, month rollover 65→2m5d, T-PERSIST non-blocking without wall-clock).
2. **ui.screen.map (FEAT-005, J19 delivered, commit `b721740`)**: `internal/ui/screens/map/**` + `data/errors.json` gains MET-U100/U100-199 range. Lead rulings recorded on the BOW item: AC-3/AC-6 N/A for S1; dir `map/` package `mapscreen`; per-module error-subrange claim-at-build = convention.
3. **feat.detgate (FEAT-004, J18 delivered, commit `47de5d0`)**: `internal/engine/detgate/**`, `.github/workflows/ci.yml` (determinism-gate + lint jobs armed, golangci-lint PINNED v2.5.0), `.golangci.yml` (v2 migration = BUG-002 closure + second glob bug fixed + lead edit: `!**/*_test.go` depguard exemption — lead ruling: test files may import stub), `build.ps1` (-DetGate switch), ~20 mechanical lint fixes across other packages (lead-attributed to this item). **BUG-002 stays OPEN in the BOW** (fix bundled in `47de5d0` but not yet Tester-confirmed).

**BUG-003 fix is also COMMITTED (`f7815b7`)** but the item stays OPEN pending Tester re-verdict (grep both hooks: no bespoke SELECTs; GUID ref must silent-allow).

## 3. Verification queue (Tester re-spawned and live, working this now)

In order: **engine.core → ui.screen.map → feat.detgate (+ BUG-002 evidence) → BUG-003 re-verify.** The exact per-item check lists live in §2 above, the acceptance files, and the BOW items' comments. On PASS: item closes (`done` with pipeline provenance note); on FAIL: bounces to the *same* junior (J16/J19/J18 respectively), never redispatched fresh.

## 4. After the queue drains → next dispatches (criteria all exist, but two are stale)

RM validated this list against the live BOW dependency graph (2026-08-09); Bill then set explicit sequencing on top (refresh #2) — see `docs/planning/team-board.md` "Dispatch queue" for the full ranked table. Headline:

- **Dep-ready but held pending BA-1 Task 1**: **ui.screen.debug (FEAT-007)**, **feat.debugmode (FEAT-008)** — both S1, deps all `done`, but their criteria files are `status: draft-ahead` with escalation notes flagging stale API references (`errs.Recent()`, registry API, widgets sparkline, engine.core phase names/order, serialize `Header` methods) that must be refreshed against landed code before a junior builds to them. This is the **next dev dispatch** (2-wide), the instant BA-1 reports Task 1 done.
- **Dep-ready but held by Bill's sequencing choice**: **harness.replay (MOD-013)** and **ui.keys (MOD-011)** — both S2, dep-clear, but at-risk-pre-freeze parallel starts (dependency chain runs through `int.protocol`/`ui.core`, still unfrozen). Bill is deliberately NOT filling all 4 dev slots right now — both Testers are already loaded, so a 4-wide dev dispatch would just queue behind them. These two stay queued, not dispatched, until Tester capacity or Bill's call changes.
- **Blocked until the verification queue drains** (depend on MOD-012/FEAT-004/FEAT-005 while those are still `in_progress`): **feat.skeleton (FEAT-006)** — the walking-skeleton integration, `docs/planning/acceptance/feat.skeleton.md`, re-ranks to #1 the moment engine.core/feat.detgate/ui.screen.map all PASS; **harness.synth (MOD-016)**, **engine.invariant (MOD-019)** (both depend on MOD-012 directly); **ui.harness (MOD-014)** (depends on MOD-013, which isn't built yet either way).
- **Error-code registration batch** (lead task, small, no dev needed): add to `data/errors.json`: MET-F100-105 (registry), F200-202+F220 (det), F600-606 (data), E000-E010 (engine.core), E100-E103 (detgate), P090/091/093 (stub) + reserve ranges. Single `[foundation.errors]` chore commit.
- Sprint 3/4 criteria (engine.world, engine.citizens, engine.season, engine.market, engine.finance, engine.consumption, engine.unlocks, engine.services) are **already written** — BAs are running well ahead of the N+1..N+3 guideline (measured from build-sprint N=1, they're complete through S4 and BA-2 is mid-S5). This is itself a minor risk: criteria this far ahead are written against contracts not yet frozen — exactly what BA-1's Task 2 (freeze-risk audit, queued after Task 1) checks for.

## 5. Team (re-spawned 2026-08-09, live now; refreshed again same day — Bill's second pass)

Per `docs/planning/dev-team-process.md` **v1.6** (Bill amended it mid-session: caps: 4 dev / **2 tester** / 2 BA / 1 docs / 1 QA / 1 RM; new "Second Tester (v1.6)" section — disjoint items, never communicate, one item never gets two verdicts):
- **Tester-1** — live: engine.core (MOD-012, f81e5d7) → ui.screen.map (FEAT-005, b721740).
- **Tester-2** — live: feat.detgate (FEAT-004, 47de5d0) + BUG-002 → BUG-003 re-verify (f7815b7).
- **BA-1** (owns S0-S1) — **Task 1 (dispatch-blocking):** refresh FEAT-007/FEAT-008 criteria's stale API references, report to Bill first. **Task 2 (queued):** freeze-risk audit of already-written S1-S4 criteria.
- **BA-2** (owns S2-S5, in practice now S4-S5) — live, finishing Sprint 5 criteria (engine.traffic, engine.roads).
- **Documentation** — live, freeze-packet upkeep + acceptance-corpus conventions.
- **QA** — live, trailing audit of the three new commits (f81e5d7, 47de5d0, b721740).
- **RM** — live, this file + team-board.md.
- **Devs** — none spawned yet. Next dispatch is FEAT-007 + FEAT-008 (2-wide, not 4-wide — Bill's deliberate choice while both Testers are loaded), gated on BA-1's Task 1 report.

## 6. Standing orders & rulings from Aaron (STILL IN FORCE)

- Dev-team pipeline v1.6 mandatory (BA criteria → Sonnet junior → Tester pass/fail-never-fixes → Docs .md-only → Bill final review → commit). Saturation rule; heavy checkpointing; staging-area discipline (juniors atomic stage-test-reset; lead commits by pathspec); v1.6 adds the Second Tester independence section.
- Sprint-1 at-risk-pre-freeze starts sanctioned; freeze changes fan out to harness.stub/ui.core/ui.widgets/map (RM tracks).
- "update" (bare word) = run /update skill. Tile decision: option (a) artistic compression (in data/georef.json). Go confirmed over C#. MOD-001 cancelled; metro BOW is the project BOW.
- **SECOND TESTER APPROVED (2026-08-09) — resolves the prior open question.** Team cap is now **2 Testers**, each independent, neither talks to the other, both report only to Bill. **Bill amended `docs/planning/dev-team-process.md` to v1.6 same day** — Tester cap 2 in the caps line + a new "Second Tester (v1.6)" section codifying the independence rule (disjoint items, never communicate, one item never gets two verdicts). Doc and reality now match; no divergence outstanding. Current split: Tester-1 = engine.core (MOD-012, f81e5d7) → ui.screen.map (FEAT-005, b721740). Tester-2 = feat.detgate (FEAT-004, 47de5d0) + BUG-002 → BUG-003 re-verify (f7815b7).
- **BA-1 dispatch-blocking assignment (Bill, refresh #2):** FEAT-007 (ui.screen.debug) and FEAT-008 (feat.debugmode) criteria are `status: draft-ahead` with escalation notes calling out stale API references against landed code (`errs.Recent()`, registry API, widgets sparkline, engine.core phase names/order, serialize `Header` methods). BA-1 refreshes both and reports to Bill before touching anything else (including RM's earlier freeze-risk-audit suggestion, now Task 2). This unblocks the next dev dispatch.
- **Dev dispatch width — Bill's deliberate choice, not a gap:** Bill is holding dev slots at 2 (FEAT-007 + FEAT-008, pending the BA-1 refresh above) rather than filling the 4-cap, because both Testers are already loaded and a wider dev dispatch would only queue behind them. RM's saturation view is on record in team-board.md: reasonable now, worth re-raising if the Tester queue clears and width still lags.
- **CLOUD DECISION MADE (2026-08-09): Azure, confirmed, until otherwise agreed.** `docs/cloud.md`'s "keep Azure, low confidence" recommendation is now a ruling; its 3 open questions are superseded on the platform choice. **This narrows the Sprint 0 freeze gate to the CONTRACT questions only** (`docs/design/README.md`: OD f32-vs-f64, duplicate correlation-ID generators) — the cloud half of the gate is closed.
- **Existing Azure estate is reusable** (full detail + lead notes on **MOD-069**, cite via `node claude-bow.js show MOD-069` rather than duplicating): storage account `garcialtdstorage` (RG `garcia`, region `uksouth`, Pay-As-You-Go sub `8e1afaa3-1ce8-4269-9f57-71fdd88c70c3`), already serving Prix Six's WhatsApp worker via blob container `whatsapp-session`; ACR `prixsixacr`; Container Apps env `prixsix-env`, scale-to-zero. **Key ruling: Metropolis Blob saves get their OWN container in that storage account — never reuse `whatsapp-session`.** MOD-069 (P3/future) still gates on MOD-036 (balance harness, open, far downstream) — the platform ruling does not pull cloud work into Sprint 1; RM checked FEAT-011 (Save/load UX) too — still gated on MOD-011 (ui.keys, open) — neither moves up the near-term queue as a result of this ruling.
- OPEN question to Aaron: none outstanding as of this update (the Tester-count question above is now closed).

## 7. Cold-resume procedure

1. `metro` launch → checkin prints BOW summary (metro DB health).
2. Read this file + `node claude-bow.js list --by-seq` + `git log -15 --oneline`.
3. `git status` — tree should be clean (only the harmless `docs/~$cloud.md` Word lock file untracked). If §2's three items show as uncommitted working-tree changes instead, the bounce happened *before* the "sync git" commit — treat them as pre-commit deliveries per the original recovery path (verify, then commit by pathspec) rather than re-dispatching to fresh devs.
4. Re-spawn Tester with §3's queue; re-spawn RM/BAs/Docs/QA per §5.
5. Do NOT redo committed work (git log + BOW `done` status = truth). Hooks are LIVE on both Bash and PowerShell — every commit needs a valid `[mkey]` tag when touching cmd/internal/data.
