# HEAVY CHECKPOINT — session bounce point

**Written by Bill (lead) at Aaron's "save here, stand by to bounce" — 2026-08-09, after commit bf65b9a.**
**This file supersedes team-board.md (stale by ~8 events). A fresh session recovers from THIS + `node claude-bow.js list --by-seq` + `git log -15`.**

## 1. Where we are

- **Sprint 0: build scope 100% COMPLETE.** All items done + committed. Sprint 0 closes on ONE thing: **Aaron's freeze review** — packet in `docs/design/README.md` (4 contract docs + cross-cutting questions: OD f32-vs-f64 [f32 recommended], duplicate correlation-ID generators) and the **cloud provider decision** in `docs/cloud.md` (recommendation: keep Azure, low confidence, revisit at M2; 3 questions for Aaron).
- **Sprint 1: 5 of 10 done+committed** (harness.stub 112f880, ui.core 87d8efe, ui.widgets bf65b9a, foundation.det f24f9ff — det is S0 but committed in this run — plus registry/data). **20 BOW items done total.** Last commit: bf65b9a, pushed, tree state below.

## 2. UNCOMMITTED working-tree deliveries (files survive the bounce — do NOT redo, do NOT commit unverified)

Three juniors delivered, Tester verdicts PENDING — their work sits in the working tree:
1. **engine.core (MOD-012, J16 delivered)**: `internal/engine/core/**` — orchestrator, 35 tests, 18ns/0-alloc benchmark. Verify per `docs/planning/acceptance/engine.core.md` + my queued checks (re-run benchmark, pool 1-vs-14 byte-compare, scrambled barrier test, month rollover 65→2m5d, T-PERSIST non-blocking without wall-clock).
2. **ui.screen.map (FEAT-005, J19 delivered)**: `internal/ui/screens/map/**` + `data/errors.json` gains MET-U100/U100-199 range. Lead rulings recorded on the BOW item: AC-3/AC-6 N/A for S1; dir `map/` package `mapscreen`; per-module error-subrange claim-at-build = convention.
3. **feat.detgate (FEAT-004, J18 delivered)**: `internal/engine/detgate/**`, `.github/workflows/ci.yml` (determinism-gate + lint jobs armed, golangci-lint PINNED v2.5.0), `.golangci.yml` (v2 migration = BUG-002 closure + second glob bug fixed + MY edit: `!**/*_test.go` depguard exemption — lead ruling: test files may import stub), `build.ps1` (-DetGate switch), ~20 mechanical lint fixes across other packages (lead-attributed to this item).

**BUG-003 fix is COMMITTED (f7815b7)** but the item stays OPEN pending Tester re-verdict (grep both hooks: no bespoke SELECTs; GUID ref must silent-allow).

## 3. Verification queue to re-dispatch (fresh Tester agent — old transcript is gone)

In order: **engine.core → ui.screen.map → feat.detgate(+BUG-002 evidence) → BUG-003 re-verify.** The exact per-item check lists live in this file §2, the acceptance files, and the BOW items' comments. On PASS: commit per item via Bash tool (guards fire; `[mkey]` tag mandatory — autoref links automatically), `done` the item with pipeline provenance note.

## 4. After the queue drains → next dispatches (criteria all exist)

1. **feat.skeleton (FEAT-006)** — wire stub engine + ui.core + map screen into `cmd/metropolis`: the runnable walking skeleton (Aaron's demo). Integration criteria: `docs/planning/acceptance/feat.skeleton.md`.
2. **ui.screen.debug (FEAT-007)** + **feat.debugmode (FEAT-008)** — criteria exist, deps committed.
3. **Error-code registration batch** (lead task, small): add to `data/errors.json`: MET-F100-105 (registry), F200-202+F220 (det), F600-606 (data), E000-E010 (engine.core), E100-E103 (detgate), P090/091/093 (stub) + reserve ranges. Single `[foundation.errors]` chore commit.
4. Sprint 2 items (criteria exist): harness.replay, ui.harness, harness.synth, ui.keys, engine.invariant, data.catalogue.

## 5. Team to re-spawn (persistent agents died with the session)

Per `docs/planning/dev-team-process.md` v1.5.1 (caps: 4 dev / 1 tester / 2 BA / 1 docs / 1 QA / 1 RM):
- **Tester** — re-spawn with standing rules + §3 queue. **BA-1** (owns S0-S1) idle; **BA-2** (owns S2-S5) was writing Sprint 5 criteria (engine.traffic, engine.roads) — re-spawn to finish. **Docs** — freeze packet upkeep + acceptance-corpus § conventions. **QA** — next trailing audit after the S1 commits. **RM** — rebuild team-board.md from this checkpoint.
- Devs are spawned per item as needed.

## 6. Standing orders & rulings from Aaron (STILL IN FORCE)

- Dev-team pipeline v1.5.1 mandatory (BA criteria → Sonnet junior → Tester pass/fail-never-fixes → Docs .md-only → Bill final review → commit). Saturation rule; heavy checkpointing; staging-area discipline (juniors atomic stage-test-reset; lead commits by pathspec).
- Sprint-1 at-risk-pre-freeze starts sanctioned; freeze changes fan out to harness.stub/ui.core/ui.widgets/map (RM tracks).
- "update" (bare word) = run /update skill. Tile decision: option (a) artistic compression (in data/georef.json). Go confirmed over C#. MOD-001 cancelled; metro BOW is the project BOW.
- OPEN question to Aaron (asked, unanswered): approve a SECOND Tester? (queue latency; default = keep one).

## 7. Cold-resume procedure

1. `metro` launch → checkin prints BOW summary (metro DB health).
2. Read this file + `node claude-bow.js list --by-seq` + `git log -15 --oneline`.
3. `git status` — confirm §2's uncommitted deliveries are present (if the tree was cleaned, the three items bounce back to fresh devs with their acceptance files).
4. Re-spawn Tester with §3's queue; re-spawn RM/BAs/Docs/QA per §5.
5. Do NOT redo committed work (git log + BOW `done` status = truth). Hooks are LIVE on both Bash and PowerShell — every commit needs a valid `[mkey]` tag when touching cmd/internal/data.
