# FEAT-085 — Bug-Backlog Remediation Plan

**PLAN ONLY — snapshot 2026-08-14; no code was modified; execution is gated behind the 2026-08-17 worker-budget reset.**

Scope: full open/in-progress BOW set read; every `BUG-*` desc + comments read in full; key `ASM-*` residuals read in full. No file, bug, or commit was touched.

**Count correction:** the brief estimated "~50 open bugs". The actual open/in-progress `BUG-*` set is **27 items** (26 open + 1 in_progress). The "~50" figure counts the 8 open `SEC-*` findings and the residual `ASM-*` tail. All 27 were read.

---

## A. Bugs that fall out / already resolved (verify + close, no new code)

| Bug | Reality |
|-----|---------|
| **BUG-035** | Stray `test <test@test.com>` fabricated-author commit is already gone from local main (Bob verified `git log --all`). Durable fix = `claude-author-guard.js`, which rides the guard cluster. Close as resolved. |
| **BUG-233** | STEP 1 (Bill: register G100–G108 + G100–G199 range row) and STEP 2 (Bob: repoint 9 constants) are both DONE per item comments; projections tests green, errs BUG-008 gate green. Only residual = a 10th borrowed code, `ErrCopiedValue` still on MET-E302, needs `MET-G109`. Resolved; the G109 one-liner folds into the registry-namespace cluster (C4). |
| **BUG-231** | Inline `-c alias.X` fix already preserved through Bob's uncommitted author-guard refactor (`resolveAlias` now consults `parseGitInvocation` overrides + shell-escape signal). Only residual (unparseable alias body → deny) rides BUG-232's sweep. |
| **BUG-213** | `--pathspec-from-file` fix already applied (`PROPORTIONALITY_PATHSPEC_LONG_FLAGS` → not-classifiable → full tier) + regression test. Uncommitted in the tree — lands with the guard cluster's commit. |
| **BUG-224** | Split by Aaron: author-guard half done (Bob, uncommitted), fail-closed sweep half = BUG-232. Round-3/4 partial fixes (`-a`/`--all`, pathspec, alias denial, inline `-c`) are coded but UNCOMMITTED in the working tree. |
| **BUG-087** | `in_progress`, correctly re-opened after a premature `done`; needs only a Tester pass + Bill commit/ref/done. No new code. |
| **BUG-012** | Direction already recorded: no code change now — gate the `Request.Payload` cap to land in the SAME change that first exposes the solver over gRPC/cloud (MOD-068/069/INT-004). Deferred by design. |
| **BUG-041** | P3, explicitly deferred in its own desc until the shared Joiner/OnceCloser exists. Deferred. |

**Live GR#24 hazard:** the working tree already carries modified `claude-destructive-guard.js` / `claude-destructive-guard.test.js` plus Bob's uncommitted author-guard refactor. The guard cluster (C3) is the natural commit vehicle for that; it should be snapshotted promptly regardless of game-first sequencing.

---

## B. Ordered cluster plan

### C1 — engine.invariant RegisterStockWithTerms (GAME · root-cause · unblocker)
- **Closes:** `BUG-067` (multi-term conservation identity) + `SEC-055` (RegisterStockWithTerms sum overflows int64 → silently "balanced") + `SEC-060` (newViolation wraps int64 with saturated deltas).
- **Residuals folded (CONFIRM-AND-CLOSE):** ASM-568, ASM-567, ASM-570, ASM-569; spec-fold ASM-155 (engine.invariant.md — untyped int64 balance). **Not swept — asm-disposition.md routing WINS:** ASM-306 (AARON-DECISION — goods-stock ownership) and ASM-571 (STORY — cross-module RegisterTransfer primitive).
- **Files:** `internal/engine/invariant/stock.go`, `violation.go`, `snapshot.go` (saturating-sum fix rides the same diff).
- **Tier:** game-engine code → Tester + ONE destructive round.
- **Why first:** real engine correctness; unblocks refuse/dispatch/education/social; the SEC-055 int64 overflow is a live silent-false-"balanced" defect in the API that already ships. Game-first (rule c) + unblocker (rule b) both point here.

### C2 — foundation.data NamingCorpus loader (GAME · unblocker)
- **Closes:** `BUG-225` (skeleton `Categories map[string][]string` can't unmarshal the committed `naming_corpus.json` `roadSuffixes` object → `LoadNamingCorpus` returns MET-F604 on the real file).
- **Residuals folded:** ASM-558.
- **Files:** `internal/foundation/data/types.go`, new `internal/foundation/data/naming_corpus.go`, `naming_corpus_test.go`.
- **Tier:** game data → Tester + ONE destructive round.
- **Why second:** blocks `engine.roads` AC-9/10/13; small and self-contained; game-first. (ASM-498 — the `data.unlocktrees` skeleton-loader gap — is the same class in `foundation/data`; one loader-shape pass here covers both, else it rides C5's BUG-228.)

### C3 — Guard-recognition cluster (SECURITY · one root cause, one sweep)
- **Closes:** `BUG-232` (fail-closed: deny `parsed:false` except `--version`/`--help`, deny `shellEscapeAlias`, deny verbs not in `git --list-cmds`) which absorbs `BUG-224`, `BUG-231`, `BUG-213`, `BUG-214`, `BUG-216`, `BUG-084`; plus `BUG-215` via `FEAT-080` (worktree-guard) and `BUG-230` (add tests that fail on the specific defect re-introduction).
- **Residuals folded (CONFIRM-AND-CLOSE only):** ASM-348/359/225 (KNOWN_COMMIT_VERBS), ASM-229/357/350/344/345/349/351/368/424/227/425/430/431/436, ASM-226/336/420, ASM-340/341/193/187/185/186/366/367.
- **Note (reconciliation):** C3 closes MECHANICAL residuals only; where asm-disposition.md routes an ASM as AARON-DECISION or STORY, that routing WINS. **Not swept:** ASM-078 (AARON-DECISION policy) and ASM-360/224/230/188/228 (STORY residual guard gaps), tracked separately, not free closes.
- **Files:** `claude-author-guard.js`(+test), `claude-destructive-guard.js`(+test), `claude-worktree-guard.js` (new), shared quote-mask primitive, `.claude/settings.json`. Note the `classifyCommitArgv` trailing-pipe false-positive (BUG-232 comment): bound args via `entry.tail` like `findGitAddInvocations` does.
- **Tier:** guard/security → fix the whole cluster, then ONE destructive round over the batch. No per-bug sieges.
- **Why third:** largest structural fix — one recognition primitive dissolves ~6 bugs + 28 mechanical residuals (rules a, d). It is also the mechanical GR#23 gate and carries the existing uncommitted guard work, so it clears the GR#24 hazard. It ranks after game bugs per the explicit game-first hint, but it is P0-critical; file-claim note: `claude-author-guard.js` is Bob's, `claude-destructive-guard.js` is Bill's — coordinate before crossing.

### C4 — Error-registry namespace (ROOT-CAUSE · registry hygiene)
- **Closes:** `BUG-234` (widen to `MET-[A-Z][0-9]{4}` or re-subdivide E/U/V/G — the structural fix) which also resolves the residual `MET-G109` from BUG-233; `BUG-220` (MET-H202 placeholder → dedicated transport-failure code).
- **Residuals folded:** none CONFIRM-AND-CLOSE. ASM-527/534/543/547 are STORY (Bill reconcile V/U/E layers) and ASM-073 is a copy-guard-standard SPEC-FOLD — per asm-disposition.md these route separately, not as free closes.
- **Files:** `data/errors.json` (Bill's claim), `internal/foundation/errs/registry.go` (format + parse), `internal/ui/harness/errors.go`.
- **Tier:** docs/registry hygiene → Tester-level only, no destructive round.
- **Why fourth:** root-cause-before-symptom (rule a) — stops the V/E950/G/H/J/K range-fight negotiation permanently (which may moot the U/E-layer STORY reconciliations ASM-527/534/543/547, but those reconciliations stay with Bill per the disposition map). It no longer blocks MOD-031 (BUG-233 already unblocked it), so it ranks below the live game and guard work.

### C5 — code.json / GUID / plan-drift (MECHANICAL · docs-tier)
- **Closes:** `BUG-226` (57 orphan root JS files → `tool.*` modules + GUID headers), `BUG-227` (re-key 4 BOW GUIDs to match code.json), `BUG-228` (register `data.unlocktrees` via master-plan + generate), `BUG-229` (doc.go GUID coverage 35/37 + buildinfo + metctl key), `BUG-180` (feat.debugmode stale registry edge → fix plan or close as duplicate of FEAT-035 scope).
- **Residuals folded (CONFIRM-AND-CLOSE):** ASM-557, ASM-199, ASM-462, ASM-463, ASM-499. **Not swept — asm-disposition.md routing WINS:** ASM-281 (AARON-DECISION — call-edge direction ruling) and ASM-444/445/451/454/472/473/477/506 (STORY — separate register-guid / master-plan amendments).
- **Files:** `docs/planning/master-plan-v2.1.json`, generated `code.json`, doc.go headers, `claude-bow.js` (GUID re-key), `tools/plan/codejson-audit.js` for verification.
- **Tier:** docs/registry/GUID → Tester-level only, no destructive round (exactly the tier Aaron carved out).
- **Why fifth:** mechanical bookkeeping with zero game/security risk — the cheapest cluster, done in one registration sweep, never competing for lanes.

### C6 — Perf-gate soundness (CI · gate-soundness)
- **Closes:** `ASM-353` (a genuine `Regressed=true` run is still `AppendResult`'d as the new baseline — the real gate-soundness hole), `ASM-355` (results.go unbounded line read), `ASM-374` (PerfResult plausibility zero-values), `ASM-375` (accept-regression hatch only in perf-1m-probe).
- **FIX, not accepted-default:** each of these is a real code/CI hole to fix, not a confirm-and-close — a genuinely-regressed run must never become the new baseline (ASM-353); the unbounded per-line read (ASM-355), the zero-value plausibility gap (ASM-374), and the missing perf-smoke accept-regression gate (ASM-375) are each closed in code/CI.
- **Files:** `internal/harness/synth/cmd/perfci/main.go`, `perf.go`, `results.go`, `.github/workflows/ci.yml`.
- **Tier:** CI/gate-soundness (protects BUG-034's merge gate) → Tester + one lightweight destructive round.
- **Why sixth:** below game/security value but above pure bookkeeping — a poisoned baseline silently corrupts the whole 1M gate. No `BUG-*` items; ASM-only cluster.

### C7 — History/process cruft (CHEAP · close-or-note)
- **Closes:** `BUG-030` + `BUG-032` (process-doc rules: never `git add` shared files by path; breaking signature changes get their own dispatch), `BUG-203` (one shared `connect()` helper for the 6 `claude-*.js` DB scripts), `BUG-223` (FK-cascade regression test).
- **Carries:** `BUG-087` (in_progress → Tester pass + commit only), `BUG-041` and `BUG-012` (deferred, gated to future work as noted in A).
- **Files:** `docs/planning/dev-team-process.md`, `claude-bow.js` + 5 sibling scripts, `claude-bow.test.js`, `internal/harness/synth/astgate*` (BUG-087).
- **Tier:** docs/process/tests → Tester-level only, no destructive round.
- **Why last:** background-lane framework debt; nothing here is game-blocking.

---

## C. Totals

- **Open/in_progress `BUG-*` items found: 27.**
- **Plan fixes or absorbs: 25 of 27** (C1–C5 + C7 fix/absorb 24; BUG-087 in_progress → commit-only).
- **Already-resolved, verify-and-close: BUG-035, BUG-233** (and BUG-231/213/224 are absorbed into C3's sweep).
- **Deferred by design (not closed, gated to future work): BUG-012, BUG-041.**
- **Also folded in:** 2 `SEC-*` real bugs (SEC-055, SEC-060) and 38 CONFIRM-AND-CLOSE `ASM-*` residuals (+1 spec-fold) closed free with their parents; the AARON-DECISION and STORY ASMs routed by asm-disposition.md are tracked separately, not swept.
- **Out of scope of this plan (separate open security backlog, not `BUG-*`):** SEC-056/057/058/059/021/017.

**Key soundness call for Aaron:** the brief's "~50 open bugs" is high — the true `BUG-*` backlog is 27, and over a third of it (9 items) dissolves in the single C3 guard sweep. The plan is game-first (C1/C2), then the one destructive round over the guard batch (C3), then registry/code.json/perf/cruft with Tester-only or single-destructive verification per the proportionality tiers. Nothing here was modified.
